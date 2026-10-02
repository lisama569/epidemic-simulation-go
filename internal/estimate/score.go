package estimate

import (
	"math"

	"github.com/lisama569/epidemic-simulation-go/internal/engine"
	"github.com/lisama569/epidemic-simulation-go/internal/intervention"
	"github.com/lisama569/epidemic-simulation-go/internal/model"
)

// timeGridTol 与引擎保持一致的网格对齐容差。
const timeGridTol = 1e-9

// validateInputs 校验观测与情景口径兼容、引用合法，并校验估计可行域。
// 口径不兼容（paradigm 不一致、sir 观测含 E 仓、未知分组、越界时间点、
// 负值观测等）一律明确拒绝。
func validateInputs(sc *model.Scenario, obs model.ObservationSeries, b Bounds) error {
	if err := engineValidate(sc); err != nil {
		return err
	}
	if obs.Paradigm != sc.Paradigm {
		return model.NewError(model.ErrObservationMismatch,
			"观测口径 %q 与情景口径 %q 不兼容", obs.Paradigm, sc.Paradigm)
	}
	if len(obs.Observations) == 0 {
		return model.NewError(model.ErrObservationMismatch, "观测序列为空，无法估计")
	}
	gidIdx := map[string]int{}
	for i, g := range sc.Groups {
		gidIdx[g.ID] = i
	}
	for i, o := range obs.Observations {
		if _, ok := gidIdx[o.GroupID]; !ok {
			return model.NewError(model.ErrObservationMismatch,
				"observations[%d] 引用未知分组 %q", i, o.GroupID)
		}
		switch o.Compartment {
		case model.CompS, model.CompI, model.CompR, model.CompQ, model.CompV:
		case model.CompE:
			if sc.Paradigm != model.ParadigmSEIR {
				return model.NewError(model.ErrObservationMismatch,
					"observations[%d]: sir 口径下不允许观测 E 仓", i)
			}
		default:
			return model.NewError(model.ErrObservationMismatch,
				"observations[%d]: 未知仓室 %q", i, o.Compartment)
		}
		if math.IsNaN(o.Value) || math.IsInf(o.Value, 0) || o.Value < 0 {
			return model.NewError(model.ErrObservationMismatch,
				"observations[%d]: 观测值 %v 必须为非负有限值", i, o.Value)
		}
		if o.TimeDays < 0 || o.TimeDays > sc.HorizonDays+timeGridTol {
			return model.NewError(model.ErrObservationMismatch,
				"observations[%d]: 时间点 %v 超出推演区间 [0,%v]", i, o.TimeDays, sc.HorizonDays)
		}
		r := math.Round(o.TimeDays / sc.StepDays)
		if math.Abs(o.TimeDays-r*sc.StepDays) > timeGridTol {
			return model.NewError(model.ErrObservationMismatch,
				"observations[%d]: 时间点 %v 未对齐步长网格", i, o.TimeDays)
		}
	}
	if !(b.BetaMin > 0 && b.BetaMax > b.BetaMin) ||
		math.IsNaN(b.BetaMin) || math.IsInf(b.BetaMin, 0) ||
		math.IsNaN(b.BetaMax) || math.IsInf(b.BetaMax, 0) {
		return model.NewError(model.ErrParameterOutOfRange,
			"beta 可行域非法: [%v,%v]，要求 0<min<max 且有限", b.BetaMin, b.BetaMax)
	}
	return nil
}

// engineValidate 通过干预时间轴构建完成全部情景校验（避免绕过统一口径）。
func engineValidate(sc *model.Scenario) error {
	sched, err := intervention.Build(sc)
	if err != nil {
		return err
	}
	_ = sched
	return nil
}

// scoreSSE 在给定 beta 下推演并计算观测的残差平方和。
// 每次评估克隆情景（仅替换参数），不修改输入。
func scoreSSE(sc *model.Scenario, obs model.ObservationSeries, beta float64) float64 {
	probe := *sc
	probe.Parameters = sc.Parameters
	probe.Parameters.Beta = beta
	tr, err := engine.Run(&probe, nil)
	if err != nil {
		return math.Inf(1)
	}
	timeIdx := make(map[float64]int, len(tr.Times))
	for k, t := range tr.Times {
		timeIdx[t] = k
	}
	gIdx := map[string]int{}
	for i, gid := range tr.GroupIDs {
		gIdx[gid] = i
	}
	sse := 0.0
	for _, o := range obs.Observations {
		k := timeIdx[o.TimeDays]
		g := gIdx[o.GroupID]
		var modelVal float64
		switch o.Compartment {
		case model.CompS:
			modelVal = tr.S[g][k]
		case model.CompE:
			modelVal = tr.E[g][k]
		case model.CompI:
			modelVal = tr.I[g][k]
		case model.CompR:
			modelVal = tr.R[g][k]
		case model.CompQ:
			modelVal = tr.Q[g][k]
		case model.CompV:
			modelVal = tr.V[g][k]
		}
		d := modelVal - o.Value
		sse += d * d
	}
	return sse
}
