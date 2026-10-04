package engine

import (
	"math"

	"github.com/lisama569/epidemic-simulation-go/internal/intervention"
	"github.com/lisama569/epidemic-simulation-go/internal/model"
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
// 再按迁移矩阵重排 S/E/I/R；Q（隔离中）与 V（已免疫）不参与迁移。
// 全程无随机数，重复运行与任何并行度下结果逐位一致。
// 主循环与断点续跑共用同一实现（见 resume.go），保证两种路径逐位一致。
func Run(sc *model.Scenario, sched *intervention.Schedule) (*model.Trajectory, error) {
	out, err := RunWithResume(sc, sched, ResumeOptions{})
	if err != nil {
		return nil, err
	}
	return out.Trajectory, nil
}

// step 推进一个欧拉步：接触缩减 -> 感染力 -> SEIR 转化 -> 隔离（含容量）
// -> 免疫接种（含额度配给）。V（免疫）与 Q（隔离中）一样不参与传播与迁移。
func step(sc *model.Scenario, st *model.State, active []model.Intervention, dt, t float64,
	ledger *vaccineLedger, events *[]model.CapacityEvent, vaxEvents *[]model.VaccinationEvent) {
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

	// 2) 感染力。被隔离者(Q)与已免疫者(V)都不在传播环内，
	// 流行率分母/分子都只计活动人口 S+E+I+R。
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
	dV := make([]float64, g)
	// newInf 记录本步各分组的新感染流量，供接种需求扣除（保证 S 不为负）。
	newInf := make([]float64, g)

	// 3) SEIR/SIR 转化（守恒、非负）。
	for a := 0; a < g; a++ {
		ni := st.S[a] * lambda[a] * dt
		ni = math.Min(ni, st.S[a]) // 单步感染不超过易感存量
		newInf[a] = ni
		dS[a] -= ni
		switch sc.Paradigm {
		case model.ParadigmSEIR:
			prog := st.E[a] * sc.Parameters.Sigma * dt
			prog = math.Min(prog, st.E[a]+ni)
			rec := st.I[a] * sc.Parameters.Gamma * dt
			rec = math.Min(rec, st.I[a]+prog)
			dE[a] += ni - prog
			dI[a] += prog - rec
			dR[a] += rec
		case model.ParadigmSIR:
			rec := st.I[a] * sc.Parameters.Gamma * dt
			rec = math.Min(rec, st.I[a]+ni)
			dI[a] += ni - rec
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

	// 5) 免疫接种：S->V，受额度池每日额度约束；需求扣除本步新感染后
	// 的剩余易感，保证 S 不为负。
	vax := ledger.allocate(sc, st, newInf, active, dt, t, vaxEvents)
	for a := 0; a < g; a++ {
		dS[a] -= vax[a]
		dV[a] += vax[a]
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

// applyMigration 按迁移矩阵对 S/E/I/R 做闭流重排（Q 与 V 不参与迁移）。
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
