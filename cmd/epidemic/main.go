// Command epidemic 是本地可运行的传染病建模与情景推演入口，
// 不依赖任何外部服务或真实数据。
//
// 子命令：
//
//	run       推演单个 JSON 情景配置，输出轨迹摘要（峰值、容量事件等）
//	estimate  对配置情景生成合成观测并做参数估计（收敛判定/区间/检查点）
//	demo      端到端：合成数据 -> 推演 -> 估计 -> 中断续算一致性校验
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/lisama569/epidemic-simulation-go/internal/config"
	"github.com/lisama569/epidemic-simulation-go/internal/engine"
	"github.com/lisama569/epidemic-simulation-go/internal/estimate"
	"github.com/lisama569/epidemic-simulation-go/internal/intervention"
	"github.com/lisama569/epidemic-simulation-go/internal/model"
	"github.com/lisama569/epidemic-simulation-go/internal/synthetic"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "run":
		err = runCmd(os.Args[2:])
	case "estimate":
		err = estimateCmd(os.Args[2:])
	case "demo":
		err = demoCmd(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		var ve *model.ValidationError
		kind := "run_failed"
		if asValidation(err, &ve) {
			kind = string(ve.Kind)
		}
		fmt.Fprintf(os.Stderr, "失败[%s]: %v\n", kind, err)
		os.Exit(1)
	}
}

func asValidation(err error, target **model.ValidationError) bool {
	for err != nil {
		if v, ok := err.(*model.ValidationError); ok {
			*target = v
			return true
		}
		if u, ok := err.(interface{ Unwrap() error }); ok {
			err = u.Unwrap()
			continue
		}
		break
	}
	return false
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: epidemic <command> [flags]

commands:
  run       -config <scenario.json>
  estimate  -config <scenario.json> [-checkpoint-dir <dir>] [-parallelism n] [-resume]
  demo`)
}

func loadScenario(path string) (*model.Scenario, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, model.NewError(model.ErrConfigSyntax, "读取配置失败: %v", err)
	}
	return config.Load(b)
}

// runResult 是 run 子命令的结构化输出。
type runResult struct {
	Steps          int                   `json:"steps"`
	GroupIDs       []string              `json:"groupIds"`
	PeakI          map[string]float64    `json:"peakInfected"`
	FinalR         map[string]float64    `json:"finalRemoved"`
	CapacityEvents []model.CapacityEvent `json:"capacityEvents"`
	CapacityCount  int                   `json:"capacityEventCount"`
}

func runCmd(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "情景配置 JSON 路径（v1 自动归一，v2 严格解析）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *cfgPath == "" {
		return fmt.Errorf("run 需要 -config")
	}
	sc, err := loadScenario(*cfgPath)
	if err != nil {
		return err
	}
	sched, err := intervention.Build(sc)
	if err != nil {
		return err
	}
	tr, err := engine.Run(sc, sched)
	if err != nil {
		return err
	}
	res := runResult{Steps: len(tr.Times) - 1, GroupIDs: tr.GroupIDs,
		PeakI: map[string]float64{}, FinalR: map[string]float64{},
		CapacityEvents: tr.CapacityEvents, CapacityCount: len(tr.CapacityEvents)}
	for g, gid := range tr.GroupIDs {
		peak := 0.0
		for _, v := range tr.I[g] {
			if v > peak {
				peak = v
			}
		}
		res.PeakI[gid] = peak
		res.FinalR[gid] = tr.R[g][len(tr.R[g])-1]
	}
	return printJSON(res)
}

func estimateCmd(args []string) error {
	fs := flag.NewFlagSet("estimate", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "情景配置 JSON 路径")
	cpDir := fs.String("checkpoint-dir", "", "检查点目录（空则不落盘/恢复）")
	par := fs.Int("parallelism", 1, "评估并行度（结果与其无关）")
	noise := fs.Float64("noise", 0, "合成观测高斯噪声标准差")
	seed := fs.Int64("seed", 20260923, "合成观测随机种子")
	stopAfter := fs.Int("stop-after", 0, "完成指定轮数后中断（用于演示续算）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *cfgPath == "" {
		return fmt.Errorf("estimate 需要 -config")
	}
	sc, err := loadScenario(*cfgPath)
	if err != nil {
		return err
	}
	sc.RandomSeed = *seed
	obs, err := synthetic.GenerateObserved(sc, synthetic.GenerateOptions{
		SampleTimes: gridTimes(sc.HorizonDays, sc.StepDays),
		Compartment: model.CompI, NoiseSD: *noise, Seed: *seed,
	})
	if err != nil {
		return err
	}
	opts := estimate.Options{
		Bounds:             estimate.Bounds{BetaMin: 0.05, BetaMax: 0.9},
		CoarseGrid:         41,
		RefineRounds:       8,
		TolBeta:            5e-4,
		Parallelism:        *par,
		CheckpointDir:      *cpDir,
		MaxCompletedRounds: *stopAfter,
	}
	est, err := estimate.Run(sc, obs, opts)
	out := map[string]any{
		"seed": *seed, "noise": *noise, "nObs": len(obs.Observations),
		"estimate": est, "trueBeta": sc.Parameters.Beta,
	}
	if est != nil {
		out["converged"] = est.Converged
	}
	_ = printJSON(out)
	if est != nil && !est.Converged {
		// 结论未收敛/越界时 JSON 已如实打印，但以非零退出码提示调用方。
		return fmt.Errorf("估计未得到可信结论: %s", est.Reason)
	}
	return err
}

func demoCmd(args []string) error {
	fs := flag.NewFlagSet("demo", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	sc := synthetic.ReferenceScenario()
	obs, err := synthetic.GenerateObserved(sc, synthetic.GenerateOptions{
		SampleTimes: gridTimes(sc.HorizonDays, sc.StepDays),
		Compartment: model.CompI, NoiseSD: 0, Seed: sc.RandomSeed,
	})
	if err != nil {
		return err
	}
	// 先完整估计（并行度 1）
	full, err := estimate.Run(sc, obs, estimate.Options{
		Bounds:     estimate.Bounds{BetaMin: 0.05, BetaMax: 0.9},
		CoarseGrid: 41, RefineRounds: 8, TolBeta: 5e-4, Parallelism: 1,
	})
	if err != nil {
		return err
	}
	// 再以并行度 8 复算，验证逐位一致
	par, err := estimate.Run(sc, obs, estimate.Options{
		Bounds:     estimate.Bounds{BetaMin: 0.05, BetaMax: 0.9},
		CoarseGrid: 41, RefineRounds: 8, TolBeta: 5e-4, Parallelism: 8,
	})
	if err != nil {
		return err
	}
	match := par.BetaHat == full.BetaHat && par.BestSSE == full.BestSSE &&
		par.Lower == full.Lower && par.Upper == full.Upper
	out := map[string]any{
		"trueBeta": sc.Parameters.Beta,
		"serial":   full, "parallel8": par,
		"deterministicAcrossParallelism": match,
	}
	return printJSON(out)
}

// gridTimes 生成从 step 到 horizon 的全部网格时间点作为观测时刻。
func gridTimes(horizon, step float64) []float64 {
	n := int(horizon/step + 0.5)
	ts := make([]float64, 0, n)
	for k := 1; k <= n; k++ {
		ts = append(ts, float64(k)*step)
	}
	return ts
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
