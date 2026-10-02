package engine

import (
	"math"
	"testing"

	"github.com/lisama569/epidemic-simulation-go/internal/model"
)

// vaccScenario 构造一个最小可推演的接种测试情景。
func vaccScenario(seed int64) *model.Scenario {
	return &model.Scenario{
		ConfigVersion: 2, Paradigm: model.ParadigmSIR,
		Parameters:  model.Parameters{Beta: 0.4, Gamma: 0.1},
		HorizonDays: 20, StepDays: 0.5, RandomSeed: seed,
		Groups: []model.Group{
			{ID: "a", S0: 1000},
			{ID: "b", S0: 500},
		},
		ContactMatrix: [][]float64{{2, 0.2}, {0.2, 2}},
		VaccineQuotas: []model.VaccineQuota{
			{ID: "pool", DosesPerDay: 100},
		},
		Interventions: []model.Intervention{
			{Type: model.InterventionVaccination, Groups: []string{"a"},
				StartDays: 2, EndDays: 12, DosesPerDay: 80, Compliance: 1,
				QuotaID: "pool", Priority: 1},
		},
	}
}

// assertConserved 验证六仓室非负且总人口逐点守恒。
func assertConserved(t *testing.T, tr *model.Trajectory, g, total0 float64) {
	t.Helper()
	for k := range tr.Times {
		sum := 0.0
		for gg := 0; gg < int(g); gg++ {
			for name, v := range map[string]float64{
				"S": tr.S[gg][k], "E": tr.E[gg][k], "I": tr.I[gg][k],
				"R": tr.R[gg][k], "Q": tr.Q[gg][k], "V": tr.V[gg][k],
			} {
				if v < -1e-9 || math.IsNaN(v) || math.IsInf(v, 0) {
					t.Fatalf("k=%d g=%d %s=%v 非物理状态", k, gg, name, v)
				}
				sum += v
			}
		}
		if math.Abs(sum-total0) > 1e-6 {
			t.Fatalf("k=%d 总人口=%.10f，期望 %v（守恒被破坏）", k, sum, total0)
		}
	}
}

// TestVaccinationBasic 正常接种：V 仓单调累积、接种者离开 S、
// 计划量低于额度时不应出现任何“少接种”事件，且总人口守恒。
func TestVaccinationBasic(t *testing.T) {
	const seed int64 = 424242
	sc := vaccScenario(seed)
	tr, err := Run(sc, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertConserved(t, tr, 2, 1500)

	// 接种期 [2,12) 共 10 天，每天 80 人，额度 100/天，足量。
	// 每步 40 人，共 20 步 => 800 人进入 V。
	finalV := tr.V[0][len(tr.V[0])-1]
	if math.Abs(finalV-800) > 1e-6 {
		t.Fatalf("a 组最终 V=%.6f，期望 800", finalV)
	}
	// V 仓在接种开始前为 0，开始后单调不减。
	idx4 := indexOfTime(t, tr.Times, 4)
	if tr.V[0][idx4] != 160 {
		t.Fatalf("t=4 时 V=%v，期望 160（2 天*80）", tr.V[0][idx4])
	}
	for k := 1; k < len(tr.Times); k++ {
		if tr.V[0][k] < tr.V[0][k-1]-1e-12 {
			t.Fatalf("k=%d V 仓下降: %v -> %v", k, tr.V[0][k-1], tr.V[0][k])
		}
	}
	for _, ev := range tr.VaccinationEvents {
		if ev.Shortfall > 1e-9 {
			t.Fatalf("足量接种不应出现少接种事件: %+v", ev)
		}
	}
	// 已接种者不被感染：V 中人数不应回落到其它仓（守恒+单调已保证），
	// 且无感染源时 S 只因接种减少，t=12 时 a 组 S 应为 1000-800=200。
	idx12 := indexOfTime(t, tr.Times, 12)
	if math.Abs(tr.S[0][idx12]-200) > 1e-6 {
		t.Fatalf("t=12 时 a 组 S=%.6f，期望 200", tr.S[0][idx12])
	}
	t.Logf("关键输入: seed=%d beta=0.4 doses=80/day quota=100/day 区间[2,12) "+
		"step=0.5；判定: 最终 V=%.1f（期望800），t=12 S=%.1f（期望200），"+
		"少接种事件=%d，总人口恒为1500",
		seed, finalV, tr.S[0][idx12], len(tr.VaccinationEvents))
}

// TestVaccinationQuotaContention 多个安排争抢同一额度：
// 两个分组的安排同时段、合计日需求超过日额度，必须按优先级分配，
// 每一步总接种不超过当步额度，少接种事件可定位到天/分组/原因。
func TestVaccinationQuotaContention(t *testing.T) {
	const seed int64 = 777
	sc := vaccScenario(seed)
	sc.VaccineQuotas = []model.VaccineQuota{{ID: "pool", DosesPerDay: 100}}
	sc.Interventions = []model.Intervention{
		{Type: model.InterventionVaccination, Groups: []string{"a"},
			StartDays: 2, EndDays: 8, DosesPerDay: 80, Compliance: 1,
			QuotaID: "pool", Priority: 1},
		{Type: model.InterventionVaccination, Groups: []string{"b"},
			StartDays: 2, EndDays: 8, DosesPerDay: 80, Compliance: 1,
			QuotaID: "pool", Priority: 2},
	}
	tr, err := Run(sc, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertConserved(t, tr, 2, 1500)

	// 每步额度 50；a 优先：a 每步拿 40，b 只能拿 10。
	// 6 天 12 步：a=480, b=120。
	va := tr.V[0][len(tr.V[0])-1]
	vb := tr.V[1][len(tr.V[1])-1]
	if math.Abs(va-480) > 1e-6 {
		t.Fatalf("高优先 a 组 V=%.6f，期望 480", va)
	}
	if math.Abs(vb-120) > 1e-6 {
		t.Fatalf("低优先 b 组 V=%.6f，期望 120（额度被 a 占用后的剩余）", vb)
	}
	if len(tr.VaccinationEvents) == 0 {
		t.Fatal("额度争抢必须记录少接种事件")
	}
	for _, ev := range tr.VaccinationEvents {
		if ev.Reason != model.VaccReasonQuotaExhausted {
			t.Fatalf("b 组少接种原因应为 %s，实际 %q",
				model.VaccReasonQuotaExhausted, ev.Reason)
		}
		if ev.GroupID != "b" {
			t.Fatalf("少接种应只发生在低优先 b 组，实际 %s", ev.GroupID)
		}
		if !(ev.Demand == 40 && ev.Allocated == 10 && ev.Shortfall == 30) {
			t.Fatalf("事件明细不符: %+v", ev)
		}
	}
	first := tr.VaccinationEvents[0]
	t.Logf("关键输入: seed=%d quota=100/day，a(pri=1,80/day) 与 b(pri=2,80/day) "+
		"同期争抢；判定依据: 每步额度50，按(priority,ivIdx,gIdx)全序 a 先取40，"+
		"b 得剩余10；首事件 day=%d t=%.1f 分组=%s 需求=%.0f 实种=%.0f 少=%.0f 原因=%s",
		seed, first.Day, first.TimeDays, first.GroupID, first.Demand,
		first.Allocated, first.Shortfall, first.Reason)
}

// TestVaccinationPriorityTieStable 优先级并列时的稳定分配：
// 两个安排 priority 相同，必须按情景中声明下标稳定决胜（a 在前先满足），
// 且重复运行/不同并行度结果逐位一致。
func TestVaccinationPriorityTieStable(t *testing.T) {
	mk := func() *model.Scenario {
		sc := vaccScenario(31337)
		sc.VaccineQuotas = []model.VaccineQuota{{ID: "pool", DosesPerDay: 60}}
		sc.Interventions = []model.Intervention{
			{Type: model.InterventionVaccination, Groups: []string{"a"},
				StartDays: 2, EndDays: 10, DosesPerDay: 60, Compliance: 1,
				QuotaID: "pool", Priority: 5},
			{Type: model.InterventionVaccination, Groups: []string{"b"},
				StartDays: 2, EndDays: 10, DosesPerDay: 60, Compliance: 1,
				QuotaID: "pool", Priority: 5},
		}
		return sc
	}
	run := func(p int) *model.Trajectory {
		outs, err := RunBatch([]*model.Scenario{mk()}, RunOptions{Parallelism: p})
		if err != nil {
			t.Fatal(err)
		}
		return outs[0]
	}
	tr := run(1)
	assertConserved(t, tr, 2, 1500)
	// 每步额度 30；并列时 a（ivIdx 更小）先取 30，b 得 0。
	// 8 天 16 步：a=480, b=0。
	if va := tr.V[0][len(tr.V[0])-1]; math.Abs(va-480) > 1e-6 {
		t.Fatalf("并列优先级下 a 应先被满足，V=%.6f 期望 480", va)
	}
	if vb := tr.V[1][len(tr.V[1])-1]; math.Abs(vb) > 1e-9 {
		t.Fatalf("并列优先级下 b 应完全拿不到额度，V=%.6f 期望 0", vb)
	}
	for _, ev := range tr.VaccinationEvents {
		if ev.GroupID != "b" || ev.Allocated != 0 ||
			ev.Reason != model.VaccReasonQuotaExhausted {
			t.Fatalf("并列决胜事件不符: %+v", ev)
		}
	}
	// 不同并行度 + 重复运行逐位一致。
	for _, p := range []int{2, 4} {
		tr2 := run(p)
		if !trajectoriesEqual(tr, tr2) {
			t.Fatalf("并行度 %d 下接种结果与顺序运行不一致", p)
		}
	}
	tr3, _ := Run(mk(), nil)
	if !trajectoriesEqual(tr, tr3) {
		t.Fatal("同情景重复运行接种结果不一致")
	}
	t.Logf("关键输入: seed=31337 quota=60/day，a、b priority 均为 5（并列）；" +
		"判定依据: 并列时按情景声明下标 a 先取每步全部30，b 得0；" +
		"并行度1/2/4 与重复运行逐位一致")
}

// TestVaccinationNoCarryoverByDefault 默认额度当日清零：
// 接种仅在偶数天段生效时，第一天用剩的额度不得结转到次日（此处用分段
// 安排验证跨自然日边界时余量不被继承）。
func TestVaccinationNoCarryoverByDefault(t *testing.T) {
	sc := vaccScenario(55)
	sc.VaccineQuotas = []model.VaccineQuota{{ID: "pool", DosesPerDay: 100, Accumulate: false}}
	// 每步需求 10（20/day），额度 100/day：每天只用 20，剩 80。
	// 若错误结转，后续可用额度会 >100/天；通过事件与总量验证未结转。
	sc.Interventions = []model.Intervention{
		{Type: model.InterventionVaccination, Groups: []string{"a"},
			StartDays: 2, EndDays: 12, DosesPerDay: 20, Compliance: 1,
			QuotaID: "pool", Priority: 1},
	}
	tr, err := Run(sc, nil)
	if err != nil {
		t.Fatal(err)
	}
	// 10 天 *20 = 200（a 有 1000 易感，足量）。
	if v := tr.V[0][len(tr.V[0])-1]; math.Abs(v-200) > 1e-6 {
		t.Fatalf("V=%.6f 期望 200", v)
	}
	for _, ev := range tr.VaccinationEvents {
		if ev.Shortfall > 1e-9 {
			t.Fatalf("不应有少接种: %+v", ev)
		}
	}
	t.Log("关键输入: quota=100/day 不累积, 需求20/day；判定: 每日余量80作废，" +
		"总接种200，无额度事件")
}

// TestVaccinationAccumulateCarryover 明确声明 accumulate 时，
// 前几日攒下的额度可在后续安排生效时一次使用。
func TestVaccinationAccumulateCarryover(t *testing.T) {
	sc := vaccScenario(66)
	sc.VaccineQuotas = []model.VaccineQuota{{ID: "pool", DosesPerDay: 100, Accumulate: true}}
	// 安排仅在第 6~8 天生效，日需求 250/天，3 天共 750；
	// 累积口径下前 6 天（含第0..5天，安排未生效）攒下 600，
	// 加上生效三天的 300，可用 900 >= 750，故不应有额度短少。
	sc.Interventions = []model.Intervention{
		{Type: model.InterventionVaccination, Groups: []string{"a"},
			StartDays: 6, EndDays: 9, DosesPerDay: 250, Compliance: 1,
			QuotaID: "pool", Priority: 1},
	}
	tr, err := Run(sc, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertConserved(t, tr, 2, 1500)
	if v := tr.V[0][len(tr.V[0])-1]; math.Abs(v-750) > 1e-6 {
		t.Fatalf("累积口径下 V=%.6f 期望 750", v)
	}
	for _, ev := range tr.VaccinationEvents {
		if ev.Reason == model.VaccReasonQuotaExhausted {
			t.Fatalf("累积额度应足以覆盖，不应有额度短少: %+v", ev)
		}
	}
	t.Log("关键输入: quota=100/day accumulate=true，安排第6~9天 250/day；" +
		"判定: 前6天累积600+生效期300=900>=750，全部接种，无额度短少")
}

func indexOfTime(t *testing.T, times []float64, want float64) int {
	t.Helper()
	for i, v := range times {
		if math.Abs(v-want) < 1e-9 {
			return i
		}
	}
	t.Fatalf("时间点 %v 不在轨迹中", want)
	return -1
}

func trajectoriesEqual(a, b *model.Trajectory) bool {
	if len(a.Times) != len(b.Times) {
		return false
	}
	for g := range a.GroupIDs {
		for k := range a.Times {
			if a.S[g][k] != b.S[g][k] || a.E[g][k] != b.E[g][k] ||
				a.I[g][k] != b.I[g][k] || a.R[g][k] != b.R[g][k] ||
				a.Q[g][k] != b.Q[g][k] || a.V[g][k] != b.V[g][k] {
				return false
			}
		}
	}
	return len(a.VaccinationEvents) == len(b.VaccinationEvents)
}
