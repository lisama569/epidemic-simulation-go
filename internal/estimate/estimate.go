// Package estimate 基于观测序列对传播参数 beta 做确定性网格估计。
//
// 方法：粗网格全枚举 + 围绕当前最优点逐轮减半网格宽度的细化网格。
// 每轮全部格点独立做一次确定性推演并累计 SSE，使用有界工作协程并行，
// 结果按格点位置归并，因此与并行度无关、逐位可复现。
//
// 收敛判定：细化轮最优点位移 |Δβ| 连续两轮小于 TolBeta（消除单轮抖动），
// 或达到细化轮数上限。到达上限仍未满足阈值则 Converged=false 并给出原因，
// 绝不输出看似有效的结论。
//
// 不确定性：基于 SSE 剖面的似然阈值区间
// {β : SSE(β) ≤ SSE_min·(1+τ)}，τ=3.84/nObs（大样本 95% 卡方近似），
// 在最终网格两侧以最终步长外推采样，直到阈值被越过，区间端点线性插值。
package estimate

import (
	"fmt"
	"math"
	"sync"

	"github.com/lisama569/epidemic-simulation-go/internal/checkpoint"
	"github.com/lisama569/epidemic-simulation-go/internal/model"
)

// Bounds 是待估参数的可行域（闭区间，边界含端点）。
type Bounds struct {
	BetaMin float64
	BetaMax float64
}

// Options 控制网格估计过程。
type Options struct {
	Bounds Bounds
	// CoarseGrid 为粗网格点数（含两端，>=5）；RefineRounds 为细化轮数（>=1）。
	CoarseGrid   int
	RefineRounds int
	// TolBeta 为收敛阈值：最终轮最优点位移小于该值视为收敛。
	TolBeta float64
	// Parallelism 为评估工作协程数；结果与并行度无关。<=0 视为 1。
	Parallelism int
	// CheckpointDir 非空时在每轮结束写入检查点，存在一致检查点时自动续算。
	CheckpointDir string
	// MaxCompletedRounds >0 时，完成指定轮数（含已恢复轮数）后即返回
	// （用于模拟中断后续算）。轮数口径与 Estimate.Iterations 相同；
	// 此时未达到总轮数会如实标记 Converged=false。0 表示不限制。
	MaxCompletedRounds int
}

// Estimate 是参数估计结果。Converged 为 false 时 Interval 仍如实给出，
// 并由 Reason 说明未收敛/越界原因，绝不伪装为有效结论。
type Estimate struct {
	Converged          bool           `json:"converged"`
	BetaHat            float64        `json:"betaHat"`
	Lower              float64        `json:"lower"`
	Upper              float64        `json:"upper"`
	Reason             string         `json:"reason"`
	Iterations         int            `json:"iterations"`
	FinalShift         float64        `json:"finalShift"`
	BestSSE            float64        `json:"bestSse"`
	ProfilePoints      []ProfilePoint `json:"profilePoints"`
	CheckpointRestored bool           `json:"checkpointRestored"`
}

// ProfilePoint 是 SSE 剖面采样点，支撑区间的可解释性。
type ProfilePoint struct {
	Beta float64 `json:"beta"`
	SSE  float64 `json:"sse"`
}

const (
	defaultCoarseGrid   = 41
	defaultRefineRounds = 6
	defaultTolBeta      = 1e-5
	// chi95 为卡方分布 df=1 的 95% 分位 3.841，用于 SSE 阈值区间。
	chi95 = 3.841
)

// Run 对情景与观测执行参数估计。
func Run(sc *model.Scenario, obs model.ObservationSeries, opts Options) (*Estimate, error) {
	fillDefaults(&opts)
	if err := validateOptions(opts); err != nil {
		return nil, err
	}
	if err := validateInputs(sc, obs, opts.Bounds); err != nil {
		return nil, err
	}

	nObs := len(obs.Observations)
	tau := chi95 / float64(nObs)

	est := &Estimate{}
	gridLow, gridHigh := opts.Bounds.BetaMin, opts.Bounds.BetaMax
	gridStep := (gridHigh - gridLow) / float64(opts.CoarseGrid-1)
	bestBeta, bestSSE := math.NaN(), math.Inf(1)
	round := 0
	consecutive := 0
	optsDigest := checkpoint.Digest(stripCheckpointDir(opts))
	totalRounds := 1 + opts.RefineRounds
	refine := func(betaHat, oldStep float64) (float64, float64, float64) {
		return refineWindow(betaHat, oldStep, opts.CoarseGrid,
			opts.Bounds.BetaMin, opts.Bounds.BetaMax)
	}

	// 检查点恢复：摘要一致则从下一未完成轮次继续。
	// 检查点记录已完成轮数与其所用网格；若还有后续轮次，则按与主循环完全
	// 相同的确定规则推导下一轮（细化）网格，保证续算点位与不间断运行一致。
	if opts.CheckpointDir != "" && checkpoint.Exists(opts.CheckpointDir) {
		cp, err := checkpoint.Load(opts.CheckpointDir)
		if err != nil {
			return nil, err
		}
		if err := cp.Verify(sc, optsDigest, obs); err != nil {
			return nil, err
		}
		round = cp.Round // Round 表示已完成的轮数
		bestBeta, bestSSE = cp.BetaHat, cp.BestSSE
		est.CheckpointRestored = true
		if round > 0 && round < totalRounds {
			gridLow, gridHigh, gridStep = refine(bestBeta, cp.GridStep)
		} else {
			gridLow, gridHigh, gridStep = cp.GridLow, cp.GridHigh, cp.GridStep
		}
	}

	for round < totalRounds {
		pts := gridPoints(gridLow, gridHigh, gridStep, opts.CoarseGrid)
		vals := evaluateGrid(sc, obs, pts, opts.Parallelism)
		for i, sse := range vals {
			// 严格小于决胜，格点并列时取靠左（较小 beta）——确定且可解释。
			if sse < bestSSE {
				bestSSE, bestBeta = sse, pts[i]
			}
		}
		var shift float64
		if round > 0 {
			// 上一轮最优点必然是本轮网格中心点，shift 由本轮结果给出。
			shift = math.Abs(bestBeta - gridLow - (gridHigh-gridLow)/2)
		}
		if round > 0 && shift < opts.TolBeta {
			consecutive++
		} else if round > 0 {
			consecutive = 0
		}

		if opts.CheckpointDir != "" {
			cp := &checkpoint.State{
				Round: round + 1, GridLow: gridLow, GridHigh: gridHigh, GridStep: gridStep,
				BetaHat: bestBeta, BestSSE: bestSSE, LastShift: shift,
				ScenarioDigest: checkpoint.Digest(sc), OptionsDigest: optsDigest,
				ObsDigest: checkpoint.Digest(obs),
			}
			if err := checkpoint.Save(opts.CheckpointDir, cp); err != nil {
				return nil, err
			}
		}

		round++
		if round < totalRounds {
			gridLow, gridHigh, gridStep = refine(bestBeta, gridStep)
		}
		est.FinalShift = shift

		// 确定性中断点：用于检查点续算验证。已完成轮数达到指定值即返回，
		// 检查点已先行落盘。结果标记为未完成（不可当作收敛结论）。
		if opts.MaxCompletedRounds > 0 && round >= opts.MaxCompletedRounds && round < totalRounds {
			est.BetaHat, est.BestSSE, est.Iterations = bestBeta, bestSSE, round
			est.Converged = false
			est.Reason = fmt.Sprintf("在第 %d 轮后中断（检查点已写入 %q），等待续算",
				round, opts.CheckpointDir)
			return est, nil
		}
	}

	est.BetaHat, est.BestSSE, est.Iterations = bestBeta, bestSSE, round
	est.Converged = consecutive >= 2

	// 可行域检查：最优点贴在可行域边界时，剖面会被截断，区间无意义，
	// 必须如实报告越界而不是输出看似有效的区间。
	if bestBeta <= opts.Bounds.BetaMin+gridStep || bestBeta >= opts.Bounds.BetaMax-gridStep {
		l, u, prof := profileInterval(sc, obs, bestBeta, bestSSE, gridStep, tau, opts.Bounds)
		est.Lower, est.Upper, est.ProfilePoints = l, u, prof
		est.Converged = false
		est.Reason = fmt.Sprintf(
			"估计值 beta=%.6g 落在可行域 [%v,%v] 边界附近，剖面被截断，结果不可作为有效结论",
			bestBeta, opts.Bounds.BetaMin, opts.Bounds.BetaMax)
		return est, model.NewError(model.ErrFeasibleDomain, "%s", est.Reason)
	}

	lower, upper, prof := profileInterval(sc, obs, bestBeta, bestSSE, gridStep, tau, opts.Bounds)
	est.Lower, est.Upper, est.ProfilePoints = lower, upper, prof
	if !est.Converged {
		est.Reason = fmt.Sprintf(
			"达到细化轮数上限 %d 后最优点末轮位移 %.3g 仍不小于阈值 %.3g",
			opts.RefineRounds, est.FinalShift, opts.TolBeta)
	} else {
		est.Reason = fmt.Sprintf(
			"末轮最优点位移 %.3g < 阈值 %.3g，连续 %d 轮稳定，判定收敛",
			est.FinalShift, opts.TolBeta, consecutive)
	}
	return est, nil
}

// evaluateGrid 对格点做有界并行评估；按格点顺序写回，结果与并行度无关。
func evaluateGrid(sc *model.Scenario, obs model.ObservationSeries, pts []float64, p int) []float64 {
	vals := make([]float64, len(pts))
	if p <= 1 {
		for i, b := range pts {
			vals[i] = scoreSSE(sc, obs, b)
		}
		return vals
	}
	sem := make(chan struct{}, p)
	var wg sync.WaitGroup
	for i, b := range pts {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, b float64) {
			defer wg.Done()
			defer func() { <-sem }()
			vals[i] = scoreSSE(sc, obs, b)
		}(i, b)
	}
	wg.Wait()
	return vals
}

// refineWindow 给出下一轮细化网格：步长减半、格点数 n（奇数）不变，
// 初始以 betaHat 为中心格点，窗口半宽 (n-1)/2 个新步长。随后整体平移到
// 可行域 [bmin,bmax] 内（保持步长与格点数不变，越下界上移、越上界下移），
// 因此任何评估点都不会越出可行域；贴边时最优点会落在端格点，由调用方
// 据此判定“越出可行域”。
func refineWindow(betaHat, oldStep float64, n int, bmin, bmax float64) (float64, float64, float64) {
	gridN := int(n)
	newStep := oldStep / 2
	half := float64((gridN - 1) / 2)
	low := betaHat - half*newStep
	high := betaHat + half*newStep
	width := high - low
	if low < bmin {
		low = bmin
		high = bmin + width
	}
	if high > bmax {
		high = bmax
		low = bmax - width
		if low < bmin { // 可行域窄于窗口：退化为贴边窗口（理论上粗网格已覆盖全域）
			low = bmin
			high = bmax
		}
	}
	return low, high, newStep
}

// gridPoints 生成等距格点。步长与端值由调用方完全指定，避免浮点累积：
// 第 j 点 = low + j*step。
func gridPoints(low, high, step float64, n int) []float64 {
	pts := make([]float64, n)
	for j := 0; j < n; j++ {
		pts[j] = low + float64(j)*step
	}
	return pts
}

// profileInterval 以最优点为中心向两侧以 step 等距外推采样 SSE 剖面，
// 直到越过 SSE 阈值或可行域边界；阈值跨越点之间做线性插值得到端点。
func profileInterval(sc *model.Scenario, obs model.ObservationSeries,
	bestBeta, bestSSE, step, tau float64, b Bounds) (float64, float64, []ProfilePoint) {
	threshold := bestSSE * (1 + tau)
	cache := map[float64]float64{bestBeta: bestSSE}
	eval := func(beta float64) float64 {
		if v, ok := cache[beta]; ok {
			return v
		}
		v := scoreSSE(sc, obs, beta)
		cache[beta] = v
		return v
	}
	// 单侧外推最多 10000 点，资源占用有界（正常剖面在数点内即越过阈值）。
	const maxSpan = 10000
	lower := b.BetaMin
	upper := b.BetaMax

	var inPts, outPts []ProfilePoint
	// 向左
	prevBeta, prevSSE := bestBeta, bestSSE
	inPts = append(inPts, ProfilePoint{bestBeta, bestSSE})
	for j := 1; j <= maxSpan; j++ {
		beta := bestBeta - float64(j)*step
		if beta < b.BetaMin {
			break
		}
		sse := eval(beta)
		inPts = append(inPts, ProfilePoint{beta, sse})
		if sse > threshold {
			lower = interpCross(prevBeta, prevSSE, beta, sse, threshold)
			outPts = append(outPts, ProfilePoint{beta, sse})
			break
		}
		prevBeta, prevSSE = beta, sse
	}
	// 向右
	prevBeta, prevSSE = bestBeta, bestSSE
	for j := 1; j <= maxSpan; j++ {
		beta := bestBeta + float64(j)*step
		if beta > b.BetaMax {
			break
		}
		sse := eval(beta)
		inPts = append(inPts, ProfilePoint{beta, sse})
		if sse > threshold {
			upper = interpCross(prevBeta, prevSSE, beta, sse, threshold)
			outPts = append(outPts, ProfilePoint{beta, sse})
			break
		}
		prevBeta, prevSSE = beta, sse
	}

	// 合并并按 beta 排序输出全部剖面点，供解释与复算。
	prof := append(inPts, outPts...)
	for i := 0; i < len(prof); i++ {
		for j := i + 1; j < len(prof); j++ {
			if prof[j].Beta < prof[i].Beta {
				prof[i], prof[j] = prof[j], prof[i]
			}
		}
	}
	return lower, upper, prof
}

// interpCross 在两点 SSE 跨越阈值处线性插值求 beta。
func interpCross(b1, s1, b2, s2, threshold float64) float64 {
	if s2 == s1 {
		return b2
	}
	f := (threshold - s1) / (s2 - s1)
	return b1 + f*(b2-b1)
}

func fillDefaults(o *Options) {
	if o.CoarseGrid == 0 {
		o.CoarseGrid = defaultCoarseGrid
	}
	if o.RefineRounds == 0 {
		o.RefineRounds = defaultRefineRounds
	}
	if o.TolBeta == 0 {
		o.TolBeta = defaultTolBeta
	}
}

func validateOptions(o Options) error {
	if o.CoarseGrid < 5 || o.CoarseGrid%2 == 0 {
		return model.NewError(model.ErrParameterOutOfRange,
			"coarseGrid=%d 必须为 >=5 的奇数（细化窗口需要唯一中心格点）", o.CoarseGrid)
	}
	if o.RefineRounds < 1 {
		return model.NewError(model.ErrParameterOutOfRange, "refineRounds=%d 必须 >=1", o.RefineRounds)
	}
	if o.TolBeta <= 0 {
		return model.NewError(model.ErrParameterOutOfRange, "tolBeta=%v 必须为正", o.TolBeta)
	}
	return nil
}

// optionsDigest 参与摘要的结构。检查点目录、并行度与中断轮数不纳入摘要：
// 它们不改变逐位结果，恢复时允许不同。
type optionsDigest struct {
	BetaMin      float64
	BetaMax      float64
	CoarseGrid   int
	RefineRounds int
	TolBeta      float64
}

func stripCheckpointDir(o Options) optionsDigest {
	return optionsDigest{
		BetaMin: o.Bounds.BetaMin, BetaMax: o.Bounds.BetaMax,
		CoarseGrid: o.CoarseGrid, RefineRounds: o.RefineRounds,
		TolBeta: o.TolBeta,
	}
}
