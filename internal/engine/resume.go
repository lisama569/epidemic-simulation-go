package engine

import (
	"math"

	"github.com/lisama569/epidemic-simulation-go/internal/checkpoint"
	"github.com/lisama569/epidemic-simulation-go/internal/intervention"
	"github.com/lisama569/epidemic-simulation-go/internal/model"
	"github.com/lisama569/epidemic-simulation-go/internal/validate"
)

// ResumeOptions 控制推演级断点续跑。零值表示不启用（与 Run 行为一致）。
type ResumeOptions struct {
	// CheckpointDir 非空时：存在一致检查点则从断点续算；
	// 中断或跑完时把当前可恢复状态原子覆盖写入该目录。
	CheckpointDir string
	// StopAfterSteps >0 时，完成指定步数（含已恢复步数）后即中断返回，
	// 检查点已先行落盘；断点可落在任意步边界（不必是整天）。
	// 0 表示不限制，一路跑到 horizon。
	StopAfterSteps int
}

// RunOutcome 是带断点控制的推演结果。
type RunOutcome struct {
	Trajectory *model.Trajectory
	// Completed 为 false 表示在 StopAfterSteps 处中断（检查点已落盘），
	// 结果不可当作完整轨迹使用。
	Completed bool
	// Steps 为已完成的步数；断点时刻 t=Steps*stepDays。
	Steps int
	// CheckpointRestored 表示本次运行从检查点恢复。
	CheckpointRestored bool
}

// RunWithResume 执行一次可断点续跑的确定性推演。
//
// 断点状态（见 checkpoint.RunState）包含：六仓室全分组状态、各疫苗额度池
// 结转余额、已产生的容量/接种事件与轨迹前缀、情景摘要。恢复时摘要必须匹配，
// 否则归类 checkpoint_invalid 拒绝；续算与不中断运行逐位一致。
func RunWithResume(sc *model.Scenario, sched *intervention.Schedule, opts ResumeOptions) (*RunOutcome, error) {
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
		st.S[i], st.E[i], st.I[i], st.R[i] = grp.S0, grp.E0, grp.I0, grp.R0
	}

	tr := &model.Trajectory{GroupIDs: groupIDs}
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

	// 疫苗额度台账：每个额度池的结转余额（仅 accumulate 池非零）。
	ledger := newVaccineLedger(sc)

	out := &RunOutcome{Trajectory: tr}
	startStep := 0

	// 断点恢复：摘要一致则从已完成的步边界原样续算（含额度池结转余额、
	// 已产生事件与轨迹前缀），后续步进规则与不中断运行完全相同。
	if opts.CheckpointDir != "" && checkpoint.RunExists(opts.CheckpointDir) {
		cp, err := checkpoint.LoadRun(opts.CheckpointDir)
		if err != nil {
			return nil, err
		}
		if err := cp.Verify(sc); err != nil {
			return nil, err
		}
		if err := restoreRunState(cp, sc, st, tr, ledger); err != nil {
			return nil, err
		}
		startStep = cp.Step
		out.CheckpointRestored = true
	}
	if startStep == 0 {
		record(0)
	}

	save := func(step int) error {
		return checkpoint.SaveRun(opts.CheckpointDir, snapshotRunState(step, sc, st, tr, ledger))
	}

	for k := startStep; k < n; k++ {
		t := float64(k) * dt
		active := sched.ActiveAt(t)
		step(sc, st, active, dt, t, ledger, &tr.CapacityEvents, &tr.VaccinationEvents)
		if sc.MigrationMatrix != nil {
			applyMigration(sc.MigrationMatrix, st, dt)
		}
		record(float64(k+1) * dt)

		// 确定性中断点：完成指定步数即落盘并返回（未跑完则 Completed=false）。
		if opts.StopAfterSteps > 0 && k+1 >= opts.StopAfterSteps && k+1 < n {
			out.Steps = k + 1
			if opts.CheckpointDir != "" {
				if err := save(k + 1); err != nil {
					return nil, err
				}
			}
			return out, nil
		}
	}
	out.Steps = n
	out.Completed = true
	if opts.CheckpointDir != "" {
		// 跑完也落一份最终检查点：之后以同一目录重跑可直接得到完整轨迹。
		if err := save(n); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// snapshotRunState 把第 step 步边界的全部可恢复状态导出为检查点。
func snapshotRunState(step int, sc *model.Scenario, st *model.State,
	tr *model.Trajectory, ledger *vaccineLedger) *checkpoint.RunState {
	cp := &checkpoint.RunState{
		Step:     step,
		GroupIDs: tr.GroupIDs,
		Times:    tr.Times,
		S:        tr.S, E: tr.E, I: tr.I,
		R: tr.R, Q: tr.Q, V: tr.V,
		QuotaCarry:        append([]float64(nil), ledger.carry...),
		CapacityEvents:    tr.CapacityEvents,
		VaccinationEvents: tr.VaccinationEvents,
		ScenarioDigest:    checkpoint.Digest(sc),
	}
	return cp
}

// restoreRunState 把检查点状态写回引擎状态、轨迹与额度台账。
// 形状与情景不一致时归类 checkpoint_invalid，绝不带病续算。
func restoreRunState(cp *checkpoint.RunState, sc *model.Scenario, st *model.State,
	tr *model.Trajectory, ledger *vaccineLedger) error {
	g := len(sc.Groups)
	bad := func(format string, args ...any) error {
		return model.NewError(model.ErrCheckpoint, "推演检查点形状与情景不一致: "+format, args...)
	}
	if len(cp.GroupIDs) != g || len(cp.QuotaCarry) != len(sc.VaccineQuotas) {
		return bad("分组数或额度池数不匹配")
	}
	for i, grp := range sc.Groups {
		if cp.GroupIDs[i] != grp.ID {
			return bad("第 %d 个分组 id=%q 与情景 %q 不符", i, cp.GroupIDs[i], grp.ID)
		}
	}
	series := [][][]float64{cp.S, cp.E, cp.I, cp.R, cp.Q, cp.V}
	for _, s := range series {
		if len(s) != g {
			return bad("仓室序列分组数不匹配")
		}
		for i := range s {
			if len(s[i]) != cp.Step+1 {
				return bad("仓室序列长度与步数不符")
			}
		}
	}
	if len(cp.Times) != cp.Step+1 {
		return bad("时间轴长度与步数不符")
	}

	last := cp.Step
	for i := 0; i < g; i++ {
		st.S[i], st.E[i], st.I[i] = cp.S[i][last], cp.E[i][last], cp.I[i][last]
		st.R[i], st.Q[i], st.V[i] = cp.R[i][last], cp.Q[i][last], cp.V[i][last]
	}
	copy(ledger.carry, cp.QuotaCarry)

	tr.Times = append(tr.Times, cp.Times...)
	for i := 0; i < g; i++ {
		tr.S[i] = append(tr.S[i], cp.S[i]...)
		tr.E[i] = append(tr.E[i], cp.E[i]...)
		tr.I[i] = append(tr.I[i], cp.I[i]...)
		tr.R[i] = append(tr.R[i], cp.R[i]...)
		tr.Q[i] = append(tr.Q[i], cp.Q[i]...)
		tr.V[i] = append(tr.V[i], cp.V[i]...)
	}
	tr.CapacityEvents = append(tr.CapacityEvents, cp.CapacityEvents...)
	tr.VaccinationEvents = append(tr.VaccinationEvents, cp.VaccinationEvents...)
	return nil
}
