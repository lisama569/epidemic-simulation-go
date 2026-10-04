package engine

import (
	"math"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/lisama569/epidemic-simulation-go/internal/model"
)

// resumeScenario 构造一个断点续跑压力情景：非整日步长（0.5 天）、
// 两个分组争抢同一个可结转额度池（需求远大于额度，全程额度紧张）、
// 隔离容量受限、接触缩减与迁移同时生效，确保断点前后各类事件都有产生。
func resumeScenario() *model.Scenario {
	capIso := 6.0
	return &model.Scenario{
		ConfigVersion: 2, Paradigm: model.ParadigmSEIR,
		Parameters:  model.Parameters{Beta: 0.5, Gamma: 0.1, Sigma: 0.25},
		HorizonDays: 30, StepDays: 0.5, // n=60 步，断点可落在半天边界
		Groups: []model.Group{
			{ID: "city", S0: 1200, E0: 15, I0: 25},
			{ID: "town", S0: 700, I0: 5},
		},
		ContactMatrix:   [][]float64{{2.4, 0.5}, {0.5, 2.0}},
		MigrationMatrix: [][]float64{{0.98, 0.02}, {0.01, 0.99}},
		VaccineQuotas: []model.VaccineQuota{
			{ID: "nat", DailyLimit: 40, StartDays: 3, EndDays: 27, Accumulate: true},
		},
		Interventions: []model.Intervention{
			{Type: model.InterventionContactReduction, Groups: []string{"city", "town"},
				StartDays: 4, EndDays: 26, Strength: 0.5, Compliance: 0.8},
			{Type: model.InterventionIsolation, Groups: []string{"city", "town"},
				StartDays: 4, EndDays: 26, RatePerDay: 1.5, Compliance: 0.9, Capacity: &capIso},
			{Type: model.InterventionVaccination, Groups: []string{"city"},
				StartDays: 3, EndDays: 27, RatePerDay: 0.6, Compliance: 1.0,
				QuotaID: "nat", Priority: 5},
			{Type: model.InterventionVaccination, Groups: []string{"town"},
				StartDays: 3, EndDays: 27, RatePerDay: 0.5, Compliance: 0.9,
				QuotaID: "nat", Priority: 1},
		},
	}
}

// assertSameTrajectory 要求两条轨迹逐位一致：时间轴、六仓室（含 V）、
// 每一次容量受限记录与每一次额度不足接种记录都逐条相等。
func assertSameTrajectory(t *testing.T, full, got *model.Trajectory, ctx string) {
	t.Helper()
	if len(full.Times) != len(got.Times) {
		t.Fatalf("%s: 时间轴长度 %d != %d", ctx, len(got.Times), len(full.Times))
	}
	for k := range full.Times {
		if full.Times[k] != got.Times[k] {
			t.Fatalf("%s: Times[%d] %.17g != %.17g", ctx, k, got.Times[k], full.Times[k])
		}
	}
	for g := range full.GroupIDs {
		for k := range full.Times {
			for name, pair := range map[string][2]float64{
				"S": {full.S[g][k], got.S[g][k]}, "E": {full.E[g][k], got.E[g][k]},
				"I": {full.I[g][k], got.I[g][k]}, "R": {full.R[g][k], got.R[g][k]},
				"Q": {full.Q[g][k], got.Q[g][k]}, "V": {full.V[g][k], got.V[g][k]},
			} {
				if pair[0] != pair[1] {
					t.Fatalf("%s: 组 %s 仓室 %s 在 k=%d 不一致: %.17g != %.17g",
						ctx, full.GroupIDs[g], name, k, pair[1], pair[0])
				}
			}
		}
	}
	if len(full.CapacityEvents) != len(got.CapacityEvents) {
		t.Fatalf("%s: 容量事件数 %d != %d", ctx, len(got.CapacityEvents), len(full.CapacityEvents))
	}
	for j, ev := range full.CapacityEvents {
		if ev != got.CapacityEvents[j] {
			t.Fatalf("%s: 第 %d 条容量事件不一致: %+v != %+v", ctx, j, got.CapacityEvents[j], ev)
		}
	}
	if len(full.VaccinationEvents) != len(got.VaccinationEvents) {
		t.Fatalf("%s: 接种事件数 %d != %d", ctx, len(got.VaccinationEvents), len(full.VaccinationEvents))
	}
	for j, ev := range full.VaccinationEvents {
		if ev != got.VaccinationEvents[j] {
			t.Fatalf("%s: 第 %d 条接种事件不一致: %+v != %+v", ctx, j, got.VaccinationEvents[j], ev)
		}
	}
}

// runInterrupted 先跑到 stop 步中断落盘，再从断点续跑到结束。
func runInterrupted(t *testing.T, sc *model.Scenario, stop int) *RunOutcome {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "cp")
	part, err := RunWithResume(sc, nil, ResumeOptions{CheckpointDir: dir, StopAfterSteps: stop})
	if err != nil {
		t.Fatalf("stop=%d 中断运行失败: %v", stop, err)
	}
	if part.Completed {
		t.Fatalf("stop=%d 应中断而非跑完", stop)
	}
	if part.Steps != stop {
		t.Fatalf("stop=%d 已完成步数=%d", stop, part.Steps)
	}
	out, err := RunWithResume(sc, nil, ResumeOptions{CheckpointDir: dir})
	if err != nil {
		t.Fatalf("stop=%d 续跑失败: %v", stop, err)
	}
	if !out.CheckpointRestored {
		t.Fatalf("stop=%d 期望从检查点恢复", stop)
	}
	if !out.Completed {
		t.Fatalf("stop=%d 续跑应跑完", stop)
	}
	return out
}

// TestResumeEveryStepBoundary 验证断点落在任意步边界（含非整日、额度争抢
// 进行中）时，续跑结果与不中断运行逐位一致。步长 0.5 天、共 60 步，
// 逐个步边界中断再续跑。
func TestResumeEveryStepBoundary(t *testing.T) {
	sc := resumeScenario()
	full, err := Run(sc, nil)
	if err != nil {
		t.Fatal(err)
	}
	n := int(math.Round(sc.HorizonDays / sc.StepDays))
	if len(full.VaccinationEvents) == 0 || len(full.CapacityEvents) == 0 {
		t.Fatalf("情景应同时产生接种事件(%d)与容量事件(%d)",
			len(full.VaccinationEvents), len(full.CapacityEvents))
	}
	t.Logf("关键输入: stepDays=0.5 n=%d, 额度池 nat dailyLimit=40 accumulate=true, "+
		"优先级 city=5/town=1 争抢同一池; 不中断运行产生接种事件 %d 条、容量事件 %d 条",
		n, len(full.VaccinationEvents), len(full.CapacityEvents))

	// 断点覆盖：接种开始前(1..5)、额度争抢进行中(6..53)、池失效后(54..59)。
	for stop := 1; stop < n; stop++ {
		out := runInterrupted(t, sc, stop)
		assertSameTrajectory(t, full, out.Trajectory, "stop="+strconv.Itoa(stop))
	}
	t.Logf("判定依据: stop=1..%d 全部步边界（含 t=%.1f 等非整日时刻与额度争抢中段）"+
		"续跑的六仓室轨迹、%d 条接种事件、%d 条容量事件均与不中断运行逐位一致",
		n-1, float64(7)*sc.StepDays, len(full.VaccinationEvents), len(full.CapacityEvents))
}

// TestResumeMultipleBreakpoints 验证同一次运行可多次中断：每个断点落盘后
// 续跑，再中断再续跑，最终结果仍与不中断运行逐位一致；且结转池余额在
// 多个断点间原样传递（accumulate=true）。
func TestResumeMultipleBreakpoints(t *testing.T) {
	sc := resumeScenario()
	full, err := Run(sc, nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "cp")
	stops := []int{7, 19, 33, 47} // t=3.5 / 9.5 / 16.5 / 23.5，均在接种窗口内
	for i, stop := range stops {
		out, err := RunWithResume(sc, nil, ResumeOptions{CheckpointDir: dir, StopAfterSteps: stop})
		if err != nil {
			t.Fatalf("第 %d 段(stop=%d)失败: %v", i, stop, err)
		}
		if out.Completed {
			t.Fatalf("第 %d 段(stop=%d)应中断", i, stop)
		}
		if i > 0 && !out.CheckpointRestored {
			t.Fatalf("第 %d 段应从上一断点恢复", i)
		}
	}
	out, err := RunWithResume(sc, nil, ResumeOptions{CheckpointDir: dir})
	if err != nil {
		t.Fatalf("末段续跑失败: %v", err)
	}
	assertSameTrajectory(t, full, out.Trajectory, "多断点")
	t.Logf("判定依据: 依次在步 %v（t=3.5/9.5/16.5/23.5，均非整日且额度争抢中）中断续跑，"+
		"最终轨迹与事件序列与不中断运行逐位一致", stops)
}

// TestResumeAfterCompletion 验证跑完后留下的检查点再次被加载时，
// 直接返回完整轨迹且与不中断运行一致（幂等续跑）。
func TestResumeAfterCompletion(t *testing.T) {
	sc := resumeScenario()
	dir := filepath.Join(t.TempDir(), "cp")
	first, err := RunWithResume(sc, nil, ResumeOptions{CheckpointDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if !first.Completed {
		t.Fatal("首次运行应跑完")
	}
	again, err := RunWithResume(sc, nil, ResumeOptions{CheckpointDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if !again.CheckpointRestored || !again.Completed {
		t.Fatalf("重跑应命中已完成检查点: restored=%v completed=%v",
			again.CheckpointRestored, again.Completed)
	}
	assertSameTrajectory(t, first.Trajectory, again.Trajectory, "完成后重跑")
	t.Log("跑完后的检查点再次加载：直接复现完整轨迹，逐位一致")
}

// TestResumeDigestMismatch 验证情景被改动后摘要不匹配，拒绝恢复并归类
// checkpoint_invalid，绝不带病续算。
func TestResumeDigestMismatch(t *testing.T) {
	sc := resumeScenario()
	dir := filepath.Join(t.TempDir(), "cp")
	if _, err := RunWithResume(sc, nil, ResumeOptions{CheckpointDir: dir, StopAfterSteps: 10}); err != nil {
		t.Fatal(err)
	}
	tampered := resumeScenario()
	tampered.Parameters.Beta = 0.45 // 改动情景，摘要应失配
	_, err := RunWithResume(tampered, nil, ResumeOptions{CheckpointDir: dir})
	if err == nil {
		t.Fatal("情景被改动后应拒绝恢复")
	}
	ve, ok := err.(*model.ValidationError)
	if !ok || ve.Kind != model.ErrCheckpoint {
		t.Fatalf("期望 kind=%s，实际 %v", model.ErrCheckpoint, err)
	}
	t.Logf("判定依据: 改动 beta 后恢复被拒绝 kind=%s（%v）", ve.Kind, ve)
}

// TestResumeParallelConsistency 验证含接种情景断点续跑的结果，与同一情景
// 在不同并行度下批量推演的结果逐位一致。
func TestResumeParallelConsistency(t *testing.T) {
	mk := func(limit float64, acc bool, prio int) *model.Scenario {
		sc := resumeScenario()
		sc.VaccineQuotas[0].DailyLimit = limit
		sc.VaccineQuotas[0].Accumulate = acc
		sc.Interventions[2].Priority = prio
		return sc
	}
	scs := []*model.Scenario{mk(40, true, 5), mk(25, false, 3), mk(60, true, 0)}
	par8, err := RunBatch(scs, RunOptions{Parallelism: 8})
	if err != nil {
		t.Fatal(err)
	}
	stops := []int{13, 29, 41}
	for i, sc := range scs {
		out := runInterrupted(t, sc, stops[i])
		assertSameTrajectory(t, par8[i], out.Trajectory, "并行对照 sc="+strconv.Itoa(i))
	}
	t.Logf("判定依据: 3 个含接种情景分别在步 %v 中断续跑，结果与并行度 8 批量推演逐位一致", stops)
}
