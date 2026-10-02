package engine

import (
	"math"

	"github.com/lisama569/epidemic-simulation-go/internal/intervention"
	"github.com/lisama569/epidemic-simulation-go/internal/model"
	"github.com/lisama569/epidemic-simulation-go/internal/validate"
)

const (
	// maxEulerRate 限制单步内单仓室被消耗的比例上界，保证欧拉积分不出现负数。
	// 采用 min(demand, available) 的守恒式实现物理非负，该常量仅用于事件阈值说明。
	nonNegTol = 1e-12
)

// Run 执行一次确定性推演。
//
// 时间轴：t_k = k*step，k=0..N（N=round(horizon/step)）。干预为半开区间，
// 在 t_k 生效当且仅当 start<=t_k<end。每步先做仓室动力学（固定步长欧拉），
// 再按迁移矩阵重排 S/E/I/R；Q（隔离中）不参与迁移。全程无随机数，
// 重复运行与任何并行度下结果逐位一致。
func Run(sc *model.Scenario, sched *intervention.Schedule) (*model.Trajectory, error) {
	tr, _, _, err := runFrom(sc, sched, 0, nil, nil, 0)
	return tr, err
}

// totalSteps 返回情景的总积分步数 N=round(horizon/step)。
func totalSteps(sc *model.Scenario) int {
	return int(math.Round(sc.HorizonDays / sc.StepDays))
}

// runFrom 从指定的已完成步数 stepCompleted 与该时刻状态继续推演。
// vaccSnaps 非空时恢复额度池跨步状态（接种中途恢复用）。
// stopAfter>0 时在完成 stopAfter 个总步数后提前返回（用于检查点），
// 0 表示一直推演到 horizon。
func runFrom(sc *model.Scenario, sched *intervention.Schedule,
	stepCompleted int, st0 *model.State, vaccSnaps []quotaSnapshot,
	stopAfter int) (
	*model.Trajectory, *model.State, []quotaSnapshot, error) {
	if err := validate.Scenario(sc); err != nil {
		return nil, nil, nil, err
	}
	if sched == nil {
		var err error
		sched, err = intervention.Build(sc)
		if err != nil {
			return nil, nil, nil, err
		}
	}

	g := len(sc.Groups)
	n := totalSteps(sc)
	end := n
	if stopAfter > 0 {
		end = stopAfter
	}
	if stepCompleted > end {
		return nil, nil, nil, model.NewError(model.ErrCheckpoint,
			"已完成步数 %d 超过中断目标 %d", stepCompleted, end)
	}
	dt := sc.StepDays

	st := &model.State{
		S: make([]float64, g), E: make([]float64, g),
		I: make([]float64, g), R: make([]float64, g),
		Q: make([]float64, g), V: make([]float64, g),
	}
	groupIDs := make([]string, g)
	for i, grp := range sc.Groups {
		groupIDs[i] = grp.ID
		st.S[i], st.E[i], st.I[i], st.R[i] = grp.S0, grp.E0, grp.I0, grp.R0
	}
	if st0 != nil {
		st.S, st.E, st.I, st.R, st.Q, st.V =
			append([]float64(nil), st0.S...), append([]float64(nil), st0.E...),
			append([]float64(nil), st0.I...), append([]float64(nil), st0.R...),
			append([]float64(nil), st0.Q...), append([]float64(nil), st0.V...)
	}

	tr := &model.Trajectory{GroupIDs: groupIDs}
	tr.Times = make([]float64, 0, n+1)
	alloc := func() [][]float64 {
		v := make([][]float64, g)
		for i := range v {
			v[i] = make([]float64, 0, n+1)
		}
		return v
	}
	tr.S, tr.E, tr.I, tr.R, tr.Q, tr.V = alloc(), alloc(), alloc(), alloc(), alloc(), alloc()

	record := func(t float64) {
		tr.Times = append(tr.Times, t)
		for i := 0; i < g; i++ {
			tr.S[i] = append(tr.S[i], st.S[i])
			tr.E[i] = append(tr.E[i], st.E[i])
			tr.I[i] = append(tr.I[i], st.I[i])
			tr.R[i] = append(tr.R[i], st.R[i])
			tr.Q[i] = append(tr.Q[i], st.Q[i])
			tr.V[i] = append(tr.V[i], st.V[i])
		}
	}
	record(float64(stepCompleted) * dt)

	// vacc 持有跨步的额度池运行态（剩余额度、当前自然日）。
	vacc := newVaccineRuntime(sc)
	if vaccSnaps != nil {
		vacc.Restore(vaccSnaps)
	}
	for k := stepCompleted; k < end; k++ {
		t := float64(k) * dt
		activeIdx := sched.ActiveIndicesAt(t)
		active := make([]model.Intervention, len(activeIdx))
		for i, ix := range activeIdx {
			active[i] = sc.Interventions[ix]
		}
		step(sc, st, active, activeIdx, dt, t, &tr.CapacityEvents, vacc, &tr.VaccinationEvents)
		if sc.MigrationMatrix != nil {
			applyMigration(sc.MigrationMatrix, st, dt)
		}
		record(float64(k+1) * dt)
	}
	return tr, st, vacc.Snapshot(), nil
}

// step 推进一个欧拉步：接触缩减 -> 感染力 -> SEIR 转化 -> 隔离（含容量）
// -> 接种（受共享额度约束）。
func step(sc *model.Scenario, st *model.State, active []model.Intervention,
	activeIdx []int, dt, t float64, events *[]model.CapacityEvent,
	vacc *vaccineRuntime, vaccEvents *[]model.VaccinationEvent) {
	g := len(sc.Groups)

	// 1) 有效接触矩阵：contact_reduction 对目标分组的接触行乘 (1-strength*compliance)。
	// 同一分组至多有一项同类干预生效（重叠已在校验阶段拒绝）。
	factor := make([]float64, g)
	for i := range factor {
		factor[i] = 1
	}
	for _, iv := range active {
		if iv.Type != model.InterventionContactReduction {
			continue
		}
		f := 1 - iv.Strength*iv.Compliance
		for _, gid := range iv.Groups {
			factor[indexOf(sc, gid)] = f
		}
	}

	// 2) 感染力。被隔离者与免疫接种者不在传播环内，流行率分母/分子都只计活动人口，
	// V 仓个体既不被感染也不参与传播，故分母排除 V。
	lambda := make([]float64, g)
	for a := 0; a < g; a++ {
		l := 0.0
		for h := 0; h < g; h++ {
			activeH := st.S[h] + st.E[h] + st.I[h] + st.R[h]
			if activeH <= 0 {
				continue
			}
			l += sc.ContactMatrix[a][h] * factor[a] * (st.I[h] / activeH)
		}
		lambda[a] = sc.Parameters.Beta * l
	}

	dS := make([]float64, g)
	dE := make([]float64, g)
	dI := make([]float64, g)
	dR := make([]float64, g)
	dQ := make([]float64, g)

	// 3) SEIR/SIR 转化（守恒、非负）。
	for a := 0; a < g; a++ {
		newInf := st.S[a] * lambda[a] * dt
		newInf = math.Min(newInf, st.S[a]) // 单步感染不超过易感存量
		dS[a] -= newInf
		switch sc.Paradigm {
		case model.ParadigmSEIR:
			prog := st.E[a] * sc.Parameters.Sigma * dt
			prog = math.Min(prog, st.E[a]+newInf)
			rec := st.I[a] * sc.Parameters.Gamma * dt
			rec = math.Min(rec, st.I[a]+prog)
			dE[a] += newInf - prog
			dI[a] += prog - rec
			dR[a] += rec
		case model.ParadigmSIR:
			rec := st.I[a] * sc.Parameters.Gamma * dt
			rec = math.Min(rec, st.I[a]+newInf)
			dI[a] += newInf - rec
			dR[a] += rec
		}
		// Q 仓中的隔离者以相同恢复速率移出隔离并进入 R。
		qRec := st.Q[a] * sc.Parameters.Gamma * dt
		qRec = math.Min(qRec, st.Q[a])
		dQ[a] -= qRec
		dR[a] += qRec
	}

	// 4) 隔离干预：effectiveRate=rate*compliance；受每分组总容量约束。
	admit := isolationAdmission(sc, st, active, dt, t, events)
	for a := 0; a < g; a++ {
		dI[a] -= admit[a]
		dQ[a] += admit[a]
	}

	// 5) 接种：成功者从 S 移入 V。感染已经从 dS 扣减，接种需求必须基于
	// “本步感染后的 S 存量”封顶，否则感染与接种各自封顶会叠加超扣 S。
	// 只复制 S 分量供额度分配读取，避免与真实状态共享底层切片。
	dV := make([]float64, g)
	sAfterInfection := make([]float64, g)
	for a := 0; a < g; a++ {
		sAfterInfection[a] = enforceNonNeg(st.S[a] + dS[a])
	}
	stProbe := *st
	stProbe.S = sAfterInfection
	vacc.apply(sc, &stProbe, active, activeIdx, dt, t, dV, vaccEvents)
	for a := 0; a < g; a++ {
		dS[a] -= dV[a]
	}

	for a := 0; a < g; a++ {
		st.S[a] = enforceNonNeg(st.S[a] + dS[a])
		st.E[a] = enforceNonNeg(st.E[a] + dE[a])
		st.I[a] = enforceNonNeg(st.I[a] + dI[a])
		st.R[a] = enforceNonNeg(st.R[a] + dR[a])
		st.Q[a] = enforceNonNeg(st.Q[a] + dQ[a])
		st.V[a] = enforceNonNeg(st.V[a] + dV[a])
	}
}

// isolationAdmission 计算每个分组本步实际准入隔离的人数。
// 容量规则：每项 isolation 干预可声明容量上限（同时在 Q 的总人数）；
// 多个干预落在同一分组时取最小上限为该分组的有效上限（更严格者生效），
// 无任何上限时容量无界。需求超过可用容量时按比例配给，绝不产生负数，
// 并记录定位到时间点与分组的 CapacityEvent。
func isolationAdmission(sc *model.Scenario, st *model.State, active []model.Intervention,
	dt, t float64, events *[]model.CapacityEvent) []float64 {
	g := len(sc.Groups)
	demand := make([]float64, g)
	capSet := make([]bool, g)
	capVal := make([]float64, g)

	for _, iv := range active {
		if iv.Type != model.InterventionIsolation {
			continue
		}
		rate := iv.RatePerDay * iv.Compliance
		for _, gid := range iv.Groups {
			a := indexOf(sc, gid)
			demand[a] += st.I[a] * rate * dt
			if iv.Capacity != nil {
				if !capSet[a] || *iv.Capacity < capVal[a] {
					capSet[a] = true
					capVal[a] = *iv.Capacity
				}
			}
		}
	}

	admit := make([]float64, g)
	for a := 0; a < g; a++ {
		demand[a] = math.Min(demand[a], st.I[a]) // 需求不超过当前感染存量
		if demand[a] <= 0 {
			continue
		}
		if !capSet[a] {
			admit[a] = demand[a]
			continue
		}
		available := capVal[a] - st.Q[a]
		if available <= nonNegTol {
			available = 0
		}
		if demand[a] > available+nonNegTol {
			admitted := available
			admit[a] = admitted
			*events = append(*events, model.CapacityEvent{
				TimeDays:  t,
				GroupID:   sc.Groups[a].ID,
				Demand:    demand[a],
				Available: available,
				Admitted:  admitted,
				Reason:    "isolation_capacity_exhausted",
			})
		} else {
			admit[a] = demand[a]
		}
	}
	return admit
}

// applyMigration 按迁移矩阵对 S/E/I/R 做闭流重排（Q 不参与迁移）。
//
// 口径约定（确定性、任意步长稳定）：M[i][j]（i≠j）为每日从 i 迁移到 j 的
// 人口比例；行和闭合约束（Σ_j M[i][j]=1）使 M[i][i] 等价于“不外迁比例”。
// 每个推演步长 dt 被等分为 s 个子步，s 取满足
//
//	h·max_i Σ_{j≠i} M[i][j] ≤ 1, h = dt/s
//
// 的最小整数，故单子步转移 T=I+Q·h 所有元素非负且行和恒为 1：
// 全局人口在任意 dt 下精确守恒、不出现负数。s=ceil(dt·maxOff)（至少 1）。
func applyMigration(m [][]float64, st *model.State, dt float64) {
	g := len(m)
	// 生成元（每行非对角和）
	maxOff := 0.0
	offSum := make([]float64, g)
	for i := 0; i < g; i++ {
		sum := 0.0
		for j := 0; j < g; j++ {
			if j != i {
				sum += m[i][j]
			}
		}
		offSum[i] = sum
		if sum > maxOff {
			maxOff = sum
		}
	}
	s := int(math.Ceil(dt*maxOff - 1e-12))
	if s < 1 {
		s = 1
	}
	h := dt / float64(s)

	// 单子步行随机转移：T[i][i]=1-h*offSum[i]，T[i][j]=h*M[i][j]。
	step := func(x []float64) []float64 {
		out := make([]float64, g)
		for i := 0; i < g; i++ {
			stay := x[i] * (1 - h*offSum[i])
			out[i] += stay
			for j := 0; j < g; j++ {
				if j != i {
					out[j] += x[i] * h * m[i][j]
				}
			}
		}
		return out
	}
	for k := 0; k < s; k++ {
		st.S = step(st.S)
		st.E = step(st.E)
		st.I = step(st.I)
		st.R = step(st.R)
		// V 仓同样参与迁移：接种者随其分组流动，但在新分组仍不感染、不传播。
		st.V = step(st.V)
	}
}

func indexOf(sc *model.Scenario, gid string) int {
	for i := range sc.Groups {
		if sc.Groups[i].ID == gid {
			return i
		}
	}
	return -1
}

func enforceNonNeg(v float64) float64 {
	if math.Abs(v) < nonNegTol {
		return 0
	}
	return v
}
