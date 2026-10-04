package engine

import (
	"math"

	"github.com/lisama569/epidemic-simulation-go/internal/checkpoint"
	"github.com/lisama569/epidemic-simulation-go/internal/intervention"
	"github.com/lisama569/epidemic-simulation-go/internal/model"
	"github.com/lisama569/epidemic-simulation-go/internal/validate"
)

// ResumeOptions 控制推演的断点保存与续跑。
type ResumeOptions struct {
	// CheckpointDir 非空时启用断点：中断时把当时状态落盘到该目录；
	// 目录中已有一致断点时自动从断点续跑。
	CheckpointDir string
	// StopAfterSteps >0 时，完成指定步数（含已恢复步数）后即中断并落盘，
	// 用于确定性地模拟"跑到一半被打断"。步数口径与 RunResult.StepsDone 相同；
	// 可落在任意步上，不要求整日边界。0 表示不主动中断。
	StopAfterSteps int
}

// RunResult 是一次（可能中断的）推演结果。
type RunResult struct {
	Trajectory *model.Trajectory
	// Completed 为 false 表示在 StopAfterSteps 处中断，断点已落盘，可续跑。
	Completed bool
	// StepsDone 为已完成的推演步数；TotalSteps 为情景总步数。
	StepsDone  int
	TotalSteps int
	// Restored 表示本次运行从断点恢复（含断点已完整的情形）。
	Restored bool
}

// RunResumable 执行一次可断点续跑的确定性推演。
//
// 断点可落在任意推演步（含非整日、额度争抢进行中）：落盘内容包括
// 已完成的步数与对应日期、六个仓室（含免疫仓 V）的各分组人数、
// 每个额度池当时的结转余额（含 accumulate 池累积余额）、已记录的
// 轨迹与容量/接种事件。续跑时按摘要校验情景一致后原样接上，
// 续跑结果与不中断运行逐位一致。
func RunResumable(sc *model.Scenario, sched *intervention.Schedule, opts ResumeOptions) (*RunResult, error) {
	if err := validate.Scenario(sc); err != nil {
		return nil, err
	}
	if sched == nil {
		var err error
		sched, err = intervention.Build(sc)
		if err != nil {
			return nil, err
		}
	}

	g := len(sc.Groups)
	n := int(math.Round(sc.HorizonDays / sc.StepDays))
	dt := sc.StepDays

	st := &model.State{
		S: make([]float64, g), E: make([]float64, g),
		I: make([]float64, g), R: make([]float64, g),
		Q: make([]float64, g), V: make([]float64, g),
	}
	groupIDs := make([]string, g)
	for i, grp := range sc.Groups {
		groupIDs[i] = grp.ID
	}

	tr := &model.Trajectory{GroupIDs: groupIDs}
	ledger := newVaccineLedger(sc)
	startK := 0
	restored := false

	// 断点恢复：摘要一致则从已完成的步数继续；断点已覆盖全程时直接返回。
	if opts.CheckpointDir != "" && checkpoint.SimulationExists(opts.CheckpointDir) {
		cp, err := checkpoint.LoadSimulation(opts.CheckpointDir)
		if err != nil {
			return nil, err
		}
		if err := cp.VerifyScenario(sc); err != nil {
			return nil, err
		}
		if len(cp.S) != g || len(cp.QuotaCarry) != len(sc.VaccineQuotas) {
			return nil, model.NewError(model.ErrCheckpoint,
				"推演断点维度与情景不符（分组 %d/%d，额度池 %d/%d），拒绝续跑",
				len(cp.S), g, len(cp.QuotaCarry), len(sc.VaccineQuotas))
		}
		st.S, st.E, st.I, st.R, st.Q, st.V = cp.S, cp.E, cp.I, cp.R, cp.Q, cp.V
		copy(ledger.carry, cp.QuotaCarry)
		tr.Times = cp.Times
		tr.S, tr.E, tr.I, tr.R, tr.Q, tr.V = cp.CompS, cp.CompE, cp.CompI, cp.CompR, cp.CompQ, cp.CompV
		tr.CapacityEvents = cp.CapacityEvents
		tr.VaccinationEvents = cp.VaccinationEvents
		startK = cp.Step
		restored = true
		if startK >= n {
			return &RunResult{Trajectory: tr, Completed: true,
				StepsDone: n, TotalSteps: n, Restored: true}, nil
		}
	} else {
		for i, grp := range sc.Groups {
			st.S[i], st.E[i], st.I[i], st.R[i] = grp.S0, grp.E0, grp.I0, grp.R0
		}
		tr.Times = append(tr.Times, 0)
		for i := 0; i < g; i++ {
			tr.S = append(tr.S, []float64{st.S[i]})
			tr.E = append(tr.E, []float64{st.E[i]})
			tr.I = append(tr.I, []float64{st.I[i]})
			tr.R = append(tr.R, []float64{st.R[i]})
			tr.Q = append(tr.Q, []float64{st.Q[i]})
			tr.V = append(tr.V, []float64{st.V[i]})
		}
	}

	save := func(done int) error {
		cp := &checkpoint.SimState{
			Step: done, TimeDays: float64(done) * dt,
			S: st.S, E: st.E, I: st.I, R: st.R, Q: st.Q, V: st.V,
			QuotaCarry: append([]float64(nil), ledger.carry...),
			Times:      tr.Times,
			CompS:      tr.S, CompE: tr.E, CompI: tr.I,
			CompR: tr.R, CompQ: tr.Q, CompV: tr.V,
			CapacityEvents:    tr.CapacityEvents,
			VaccinationEvents: tr.VaccinationEvents,
			ScenarioDigest:    checkpoint.Digest(sc),
		}
		return checkpoint.SaveSimulation(opts.CheckpointDir, cp)
	}

	for k := startK; k < n; k++ {
		t := float64(k) * dt
		active := sched.ActiveAt(t)
		step(sc, st, active, dt, t, ledger, &tr.CapacityEvents, &tr.VaccinationEvents)
		if sc.MigrationMatrix != nil {
			applyMigration(sc.MigrationMatrix, st, dt)
		}
		tr.Times = append(tr.Times, float64(k+1)*dt)
		for i := 0; i < g; i++ {
			tr.S[i] = append(tr.S[i], st.S[i])
			tr.E[i] = append(tr.E[i], st.E[i])
			tr.I[i] = append(tr.I[i], st.I[i])
			tr.R[i] = append(tr.R[i], st.R[i])
			tr.Q[i] = append(tr.Q[i], st.Q[i])
			tr.V[i] = append(tr.V[i], st.V[i])
		}
		done := k + 1
		// 确定性中断点：落盘当时完整状态（含各额度池结转余额）后返回。
		if opts.StopAfterSteps > 0 && done >= opts.StopAfterSteps && done < n {
			if opts.CheckpointDir != "" {
				if err := save(done); err != nil {
					return nil, err
				}
			}
			return &RunResult{Trajectory: tr, Completed: false,
				StepsDone: done, TotalSteps: n, Restored: restored}, nil
		}
	}
	return &RunResult{Trajectory: tr, Completed: true,
		StepsDone: n, TotalSteps: n, Restored: restored}, nil
}
