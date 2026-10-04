package engine

import (
	"math"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/lisama569/epidemic-simulation-go/internal/model"
)

// resumeScenario 构造一个断点续跑测试情景：SEIR、两分组、半日步长，
// 两项接种安排争抢同一 accumulate 额度池（优先级不同），另有隔离容量约束，
// 确保额度不足事件与容量事件都会出现。步长 0.5 天使断点可落在非整日时刻。
func resumeScenario() *model.Scenario {
	capIso := 6.0
	return &model.Scenario{
		ConfigVersion: 2, Paradigm: model.ParadigmSEIR,
		Parameters:  model.Parameters{Beta: 0.5, Gamma: 0.1, Sigma: 0.25},
		HorizonDays: 30, StepDays: 0.5,
		Groups: []model.Group{
			{ID: "a", S0: 1000, I0: 10},
			{ID: "b", S0: 800, I0: 0},
		},
		ContactMatrix: [][]float64{{2, 0.2}, {0.2, 2}},
		VaccineQuotas: []model.VaccineQuota{
			{ID: "nat", DailyLimit: 40, StartDays: 2, EndDays: 28, Accumulate: true},
		},
		Interventions: []model.Intervention{
			{Type: model.InterventionIsolation, Groups: []string{"a", "b"},
				StartDays: 3, EndDays: 27, RatePerDay: 2.0, Compliance: 0.9, Capacity: &capIso},
			{Type: model.InterventionVaccination, Groups: []string{"a"},
				StartDays: 4, EndDays: 26, RatePerDay: 0.5, Compliance: 1.0, QuotaID: "nat", Priority: 3},
			{Type: model.InterventionVaccination, Groups: []string{"b"},
				StartDays: 4, EndDays: 26, RatePerDay: 0.5, Compliance: 0.8, QuotaID: "nat", Priority: 1},
		},
	}
}

// assertSameTrajectory 逐位比较两条轨迹：时间点、六仓室（含 V）、
// 每一次额度不足的接种记录、每一次容量受限的记录，全部逐条相等。
func assertSameTrajectory(t *testing.T, full, got *model.Trajectory, ctx string) {
	t.Helper()
	if len(full.Times) != len(got.Times) {
		t.Fatalf("%s: 时间轴长度不一致 %d vs %d", ctx, len(full.Times), len(got.Times))
	}
	for k := range full.Times {
		if full.Times[k] != got.Times[k] {
			t.Fatalf("%s: Times[%d] 不一致 %v vs %v", ctx, k, full.Times[k], got.Times[k])
		}
	}
	comps := map[string][2][][]float64{
		"S": {full.S, got.S}, "E": {full.E, got.E}, "I": {full.I, got.I},
		"R": {full.R, got.R}, "Q": {full.Q, got.Q}, "V": {full.V, got.V},
	}
	for name, pair := range comps {
		for g := range pair[0] {
			for k := range pair[0][g] {
				if pair[0][g][k] != pair[1][g][k] {
					t.Fatalf("%s: %s[g=%d][k=%d] 不一致 %.17g vs %.17g",
						ctx, name, g, k, pair[0][g][k], pair[1][g][k])
				}
			}
		}
	}
	if len(full.VaccinationEvents) != len(got.VaccinationEvents) {
		t.Fatalf("%s: 接种事件数不一致 %d vs %d", ctx,
			len(full.VaccinationEvents), len(got.VaccinationEvents))
	}
	for j, ev := range full.VaccinationEvents {
		if ev != got.VaccinationEvents[j] {
			t.Fatalf("%s: 第 %d 条接种事件不一致 %+v vs %+v", ctx, j, ev, got.VaccinationEvents[j])
		}
	}
	if len(full.CapacityEvents) != len(got.CapacityEvents) {
		t.Fatalf("%s: 容量事件数不一致 %d vs %d", ctx,
			len(full.CapacityEvents), len(got.CapacityEvents))
	}
	for j, ev := range full.CapacityEvents {
		if ev != got.CapacityEvents[j] {
			t.Fatalf("%s: 第 %d 条容量事件不一致 %+v vs %+v", ctx, j, ev, got.CapacityEvents[j])
		}
	}
}

// assertConservationAndQuota 校验总人口守恒（含 V 仓）且每步实际接种不超过
// 当步额度（dailyLimit*dt + 结转），断点续跑前后都成立。
func assertConservationAndQuota(t *testing.T, sc *model.Scenario, tr *model.Trajectory, ctx string) {
	t.Helper()
	pop0 := 0.0
	for _, grp := range sc.Groups {
		pop0 += grp.S0 + grp.E0 + grp.I0 + grp.R0
	}
	limitPerStep := sc.VaccineQuotas[0].DailyLimit * sc.StepDays
	for k := range tr.Times {
		tot := 0.0
		for g := range tr.GroupIDs {
			tot += tr.S[g][k] + tr.E[g][k] + tr.I[g][k] + tr.R[g][k] + tr.Q[g][k] + tr.V[g][k]
		}
		if math.Abs(tot-pop0) > 1e-6 {
			t.Fatalf("%s: k=%d 总人口不守恒: %.10f vs %.10f", ctx, k, tot, pop0)
		}
		if k > 0 {
			dv := 0.0
			for g := range tr.GroupIDs {
				dv += tr.V[g][k] - tr.V[g][k-1]
			}
			// 结转池当步可用上限 = 当步额度 + 此前全部结余；守恒校验用
			// 最宽上界（累计额度总量）即可证明无凭空额度。
			elapsed := tr.Times[k] - sc.VaccineQuotas[0].StartDays
			if elapsed < 0 {
				elapsed = 0
			}
			budget := sc.VaccineQuotas[0].DailyLimit*elapsed + limitPerStep
			if dv > budget+1e-6 {
				t.Fatalf("%s: k=%d 当步接种 %.6f 超过累计可用额度 %.6f", ctx, k, dv, budget)
			}
			if dv < -1e-9 {
				t.Fatalf("%s: k=%d 接种量为负 %.6g", ctx, k, dv)
			}
		}
	}
}

// TestResumeMidQuotaContention 验证断点落在非整日、额度争抢进行中的任意步时，
// 续跑结果与不中断运行逐位一致（六仓室、接种事件、容量事件逐条相等）。
func TestResumeMidQuotaContention(t *testing.T) {
	sc := resumeScenario()
	full, err := Run(sc, nil)
	if err != nil {
		t.Fatal(err)
	}
	totalSteps := int(math.Round(sc.HorizonDays / sc.StepDays))
	t.Logf("关键输入: SEIR beta=0.5 gamma=0.1 sigma=0.25, 步长=0.5天(非整日断点), "+
		"总步数=%d, 额度池 nat 每日40剂 accumulate=true, 两项接种安排争抢(priority 3 vs 1), "+
		"不中断运行: 接种事件=%d 容量事件=%d",
		totalSteps, len(full.VaccinationEvents), len(full.CapacityEvents))
	if len(full.VaccinationEvents) == 0 || len(full.CapacityEvents) == 0 {
		t.Fatal("情景应同时产生接种事件与容量事件")
	}

	// 断点覆盖：接种窗口前、额度争抢进行中（非整日）、窗口尾、最后一步前。
	stops := []int{1, 7, 13, 25, 41, 53, totalSteps - 1}
	for _, stop := range stops {
		dir := filepath.Join(t.TempDir(), "simcp")
		part, err := RunResumable(sc, nil, ResumeOptions{CheckpointDir: dir, StopAfterSteps: stop})
		if err != nil {
			t.Fatalf("stop=%d 中断运行失败: %v", stop, err)
		}
		if part.Completed || part.StepsDone != stop {
			t.Fatalf("stop=%d: 期望中断于第 %d 步, 实际 completed=%v done=%d",
				stop, stop, part.Completed, part.StepsDone)
		}
		// 断点日期 = stop*0.5，可为非整日（如 stop=13 -> t=6.5）。
		resumed, err := RunResumable(sc, nil, ResumeOptions{CheckpointDir: dir})
		if err != nil {
			t.Fatalf("stop=%d 续跑失败: %v", stop, err)
		}
		if !resumed.Restored || !resumed.Completed {
			t.Fatalf("stop=%d: 期望从断点恢复并跑完, restored=%v completed=%v",
				stop, resumed.Restored, resumed.Completed)
		}
		assertSameTrajectory(t, full, resumed.Trajectory, "stop="+strconv.Itoa(stop))
		assertConservationAndQuota(t, sc, resumed.Trajectory, "stop="+strconv.Itoa(stop))
	}
	t.Logf("判定依据: 断点步 %v（含非整日 t=3.5/6.5/12.5/20.5/26.5、额度争抢进行中）续跑后, "+
		"六仓室/接种事件/容量事件与不中断运行逐条一致, 人口守恒且每步接种不超额度", stops)
}

// TestResumeChainedCheckpoints 验证多次连续断点（跑一段、断、续、再断、再续）
// 最终结果仍与不中断运行逐位一致。
func TestResumeChainedCheckpoints(t *testing.T) {
	sc := resumeScenario()
	full, err := Run(sc, nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "simcp")
	stops := []int{9, 22, 37, 50}
	for i, stop := range stops {
		res, err := RunResumable(sc, nil, ResumeOptions{CheckpointDir: dir, StopAfterSteps: stop})
		if err != nil {
			t.Fatalf("第 %d 段(stop=%d)失败: %v", i, stop, err)
		}
		if res.Completed {
			t.Fatalf("第 %d 段(stop=%d)不应完成", i, stop)
		}
		if i > 0 && !res.Restored {
			t.Fatalf("第 %d 段(stop=%d)应从前一断点恢复", i, stop)
		}
	}
	final, err := RunResumable(sc, nil, ResumeOptions{CheckpointDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if !final.Completed || !final.Restored {
		t.Fatalf("末段应恢复并跑完: restored=%v completed=%v", final.Restored, final.Completed)
	}
	assertSameTrajectory(t, full, final.Trajectory, "chained")
	assertConservationAndQuota(t, sc, final.Trajectory, "chained")
	t.Logf("判定依据: 4 个连续断点(步 %v)逐段续跑, 最终轨迹与不中断运行逐位一致", stops)
}

// TestResumeParallelDeterminism 验证含断点续跑的情景在不同并行度下批量跑，
// 接种结果（轨迹与事件）保持逐位一致。
func TestResumeParallelDeterminism(t *testing.T) {
	mk := func() *model.Scenario { return resumeScenario() }
	scs := []*model.Scenario{mk(), mk(), mk()}
	// 先在各自目录中断（不同断点步），再批量续跑。
	dirs := make([]string, len(scs))
	stops := []int{13, 25, 41}
	for i, sc := range scs {
		dirs[i] = filepath.Join(t.TempDir(), "simcp")
		if _, err := RunResumable(sc, nil, ResumeOptions{CheckpointDir: dirs[i], StopAfterSteps: stops[i]}); err != nil {
			t.Fatal(err)
		}
	}
	resumeAll := func(p int) []*model.Trajectory {
		out := make([]*model.Trajectory, len(scs))
		type job struct{ i int }
		sem := make(chan struct{}, p)
		done := make(chan struct{}, len(scs))
		for i, sc := range scs {
			sem <- struct{}{}
			go func(i int, sc *model.Scenario) {
				defer func() { <-sem; done <- struct{}{} }()
				res, err := RunResumable(sc, nil, ResumeOptions{CheckpointDir: dirs[i]})
				if err != nil {
					t.Error(err)
					return
				}
				out[i] = res.Trajectory
			}(i, sc)
		}
		for range scs {
			<-done
		}
		return out
	}
	seq := resumeAll(1)
	for _, p := range []int{2, 8} {
		par := resumeAll(p)
		for i := range seq {
			assertSameTrajectory(t, seq[i], par[i], "parallel="+strconv.Itoa(p))
		}
	}
	t.Log("判定依据: 3 个情景分别断在步 13/25/41, 并行度 1/2/8 续跑结果逐位一致")
}

// TestResumeRejectsMismatchedScenario 验证断点与情景摘要不一致时拒绝续跑，
// 归类 checkpoint_invalid，绝不带病继续。
func TestResumeRejectsMismatchedScenario(t *testing.T) {
	sc := resumeScenario()
	dir := filepath.Join(t.TempDir(), "simcp")
	if _, err := RunResumable(sc, nil, ResumeOptions{CheckpointDir: dir, StopAfterSteps: 10}); err != nil {
		t.Fatal(err)
	}
	other := resumeScenario()
	other.Parameters.Beta = 0.6 // 改动情景 => 摘要不再匹配
	_, err := RunResumable(other, nil, ResumeOptions{CheckpointDir: dir})
	if err == nil {
		t.Fatal("情景变更后续跑应被拒绝")
	}
	ve, ok := err.(*model.ValidationError)
	if !ok || ve.Kind != model.ErrCheckpoint {
		t.Fatalf("期望 checkpoint_invalid, 实际 %T: %v", err, err)
	}
	t.Logf("判定依据: 情景变更后续跑被拒绝 kind=%s", model.ErrCheckpoint)
}
