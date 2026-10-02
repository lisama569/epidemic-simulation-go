package engine

import (
	"math"
	"testing"

	"github.com/lisama569/epidemic-simulation-go/internal/model"
)

// vaxScenario 构造一个含单额度池与接种安排的基础情景（SIR、无迁移）。
// quotaLimit 为每日额度，accumulate 控制是否结转。
func vaxScenario(quotaLimit float64, accumulate bool) *model.Scenario {
	return &model.Scenario{
		ConfigVersion: 2, Paradigm: model.ParadigmSIR,
		Parameters:  model.Parameters{Beta: 0.4, Gamma: 0.1, Sigma: 0},
		HorizonDays: 20, StepDays: 1,
		Groups: []model.Group{
			{ID: "a", S0: 1000, I0: 10},
			{ID: "b", S0: 800, I0: 0},
		},
		ContactMatrix: [][]float64{{2, 0.2}, {0.2, 2}},
		VaccineQuotas: []model.VaccineQuota{
			{ID: "nat", DailyLimit: quotaLimit, StartDays: 0, EndDays: 20, Accumulate: accumulate},
		},
	}
}

func totalPop(tr *model.Trajectory, k int) float64 {
	tot := 0.0
	for g := range tr.GroupIDs {
		tot += tr.S[g][k] + tr.E[g][k] + tr.I[g][k] + tr.R[g][k] + tr.Q[g][k] + tr.V[g][k]
	}
	return tot
}

// TestVaccinationBasic 验证正常接种：S->V 单调、总人口守恒、
// 每日接种不超过当日额度、额度不足事件可定位。
func TestVaccinationBasic(t *testing.T) {
	sc := vaxScenario(50, false)
	sc.Interventions = []model.Intervention{
		{Type: model.InterventionVaccination, Groups: []string{"a"},
			StartDays: 2, EndDays: 20, RatePerDay: 0.5, Compliance: 1.0, QuotaID: "nat"},
	}
	tr, err := Run(sc, nil)
	if err != nil {
		t.Fatal(err)
	}
	const pop0 = 1810.0
	for k := range tr.Times {
		if math.Abs(totalPop(tr, k)-pop0) > 1e-6 {
			t.Fatalf("k=%d 总人口不守恒: %.10f", k, totalPop(tr, k))
		}
		for g := range tr.GroupIDs {
			for name, v := range map[string]float64{
				"S": tr.S[g][k], "I": tr.I[g][k], "R": tr.R[g][k], "V": tr.V[g][k],
			} {
				if v < -1e-9 || math.IsNaN(v) || math.IsInf(v, 0) {
					t.Fatalf("k=%d g=%d %s=%v 非物理状态", k, g, name, v)
				}
			}
		}
		if k > 0 {
			// V 单调不减；每日接种量不超过当日额度（dt=1 -> 50）。
			dv := tr.V[0][k] - tr.V[0][k-1]
			if dv < -1e-9 {
				t.Fatalf("k=%d V 出现下降: %v", k, dv)
			}
			if dv > 50+1e-9 {
				t.Fatalf("k=%d 单日接种 %.6f 超过当日额度 50", k, dv)
			}
		}
	}
	// 需求 0.5*S 远大于额度 50，必须产生可定位的额度事件。
	if len(tr.VaccinationEvents) == 0 {
		t.Fatal("需求超过额度应产生接种事件")
	}
	for _, ev := range tr.VaccinationEvents {
		if ev.Reason != "vaccine_quota_exhausted" || ev.QuotaID != "nat" || ev.GroupID != "a" {
			t.Fatalf("接种事件内容异常: %+v", ev)
		}
		if ev.Allocated > ev.Demand+1e-9 || ev.Allocated < -1e-9 {
			t.Fatalf("接种事件数值异常: %+v", ev)
		}
		if !(ev.TimeDays >= 2 && ev.TimeDays < 20) {
			t.Fatalf("接种事件时间 %v 不在接种窗口内", ev.TimeDays)
		}
	}
	t.Logf("关键输入: S0=1000, rate=0.5/day, 每日额度=50(不结转); "+
		"共 %d 次额度不足(首次 t=%.0f 组=%s 需求=%.2f 实配=%.2f), 期末 V[a]=%.1f",
		len(tr.VaccinationEvents), tr.VaccinationEvents[0].TimeDays,
		tr.VaccinationEvents[0].GroupID, tr.VaccinationEvents[0].Demand,
		tr.VaccinationEvents[0].Allocated, tr.V[0][len(tr.V[0])-1])
}

// TestVaccinationImmuneExcluded 验证 V 仓个体不再被感染也不参与传播：
// 高额度快速覆盖全人群后，几乎不再出现新感染。
func TestVaccinationImmuneExcluded(t *testing.T) {
	sc := vaxScenario(100000, false) // 额度充足
	sc.Interventions = []model.Intervention{
		{Type: model.InterventionVaccination, Groups: []string{"a", "b"},
			StartDays: 0, EndDays: 20, RatePerDay: 10, Compliance: 1.0, QuotaID: "nat"},
	}
	tr, err := Run(sc, nil)
	if err != nil {
		t.Fatal(err)
	}
	last := len(tr.Times) - 1
	vTot := tr.V[0][last] + tr.V[1][last]
	rTot := tr.R[0][last] + tr.R[1][last]
	if vTot < 1750 {
		t.Fatalf("接种覆盖不足: V 合计=%.2f", vTot)
	}
	// 初始 I0=10，若 V 仍会被感染，R 会远大于初始感染者量级。
	if rTot > 30 {
		t.Fatalf("免疫者疑似仍参与传播: R 合计=%.2f（初始 I0=10）", rTot)
	}
	if math.Abs(totalPop(tr, last)-1810) > 1e-6 {
		t.Fatalf("总人口不守恒: %.10f", totalPop(tr, last))
	}
	t.Logf("判定依据: 全覆盖接种后 V 合计=%.1f(>=1750), R 合计=%.2f(<=30, 仅初始感染者恢复)",
		vTot, rTot)
}

// TestVaccinationQuotaContention 验证多个接种安排争抢同一额度池：
// 任意时刻不超过当日额度，并列优先级按声明顺序稳定分配。
func TestVaccinationQuotaContention(t *testing.T) {
	sc := vaxScenario(50, false)
	// 两项安排同优先级、同窗口、不同分组，共用额度池 nat。
	sc.Interventions = []model.Intervention{
		{Type: model.InterventionVaccination, Groups: []string{"a"},
			StartDays: 0, EndDays: 20, RatePerDay: 0.5, Compliance: 1.0, QuotaID: "nat"},
		{Type: model.InterventionVaccination, Groups: []string{"b"},
			StartDays: 0, EndDays: 20, RatePerDay: 0.5, Compliance: 1.0, QuotaID: "nat"},
	}
	tr, err := Run(sc, nil)
	if err != nil {
		t.Fatal(err)
	}
	// 稳定规则：声明在前的 a 先分配。第 1 天 a 需求 500>50，独占全部额度，b 为 0。
	if got := tr.V[0][1]; math.Abs(got-50) > 1e-9 {
		t.Fatalf("第 1 天 a 应独占额度 50，实际 %.6f", got)
	}
	if got := tr.V[1][1]; math.Abs(got) > 1e-9 {
		t.Fatalf("第 1 天 b 应分得 0，实际 %.6f", got)
	}
	for k := 1; k < len(tr.Times); k++ {
		dv := (tr.V[0][k] - tr.V[0][k-1]) + (tr.V[1][k] - tr.V[1][k-1])
		if dv > 50+1e-9 {
			t.Fatalf("k=%d 当日合计接种 %.6f 超过额度 50", k, dv)
		}
	}
	if len(tr.VaccinationEvents) == 0 {
		t.Fatal("争抢额度应产生事件")
	}
	t.Logf("稳定规则=声明顺序优先: 第 1 天 a 得 %.1f、b 得 %.1f（总额度 50）; 事件数=%d",
		tr.V[0][1], tr.V[1][1], len(tr.VaccinationEvents))
}

// TestVaccinationPriorityOrder 验证高优先级安排先分配额度。
func TestVaccinationPriorityOrder(t *testing.T) {
	sc := vaxScenario(50, false)
	sc.Interventions = []model.Intervention{
		{Type: model.InterventionVaccination, Groups: []string{"a"},
			StartDays: 0, EndDays: 20, RatePerDay: 0.5, Compliance: 1.0, QuotaID: "nat", Priority: 1},
		{Type: model.InterventionVaccination, Groups: []string{"b"},
			StartDays: 0, EndDays: 20, RatePerDay: 0.5, Compliance: 1.0, QuotaID: "nat", Priority: 9},
	}
	tr, err := Run(sc, nil)
	if err != nil {
		t.Fatal(err)
	}
	// b 优先级更高：第 1 天 b 需求 0.5*800=400>50，独占额度；a 为 0。
	if got := tr.V[1][1]; math.Abs(got-50) > 1e-9 {
		t.Fatalf("高优先级的 b 应独占额度 50，实际 %.6f", got)
	}
	if got := tr.V[0][1]; math.Abs(got) > 1e-9 {
		t.Fatalf("低优先级的 a 第 1 天应分得 0，实际 %.6f", got)
	}
	t.Logf("判定依据: priority(b)=9 > priority(a)=1, 第 1 天 b=%.1f a=%.1f（额度 50）",
		tr.V[1][1], tr.V[0][1])
}

// TestVaccinationAccumulate 验证额度结转语义：accumulate=true 时
// 未用完额度累积，false（默认）时当日作废。
func TestVaccinationAccumulate(t *testing.T) {
	mk := func(acc bool) *model.Scenario {
		sc := vaxScenario(100, acc)
		// 接种窗口从第 5 天开始，前 5 天额度闲置。
		sc.Interventions = []model.Intervention{
			{Type: model.InterventionVaccination, Groups: []string{"a"},
				StartDays: 5, EndDays: 20, RatePerDay: 1.0, Compliance: 1.0, QuotaID: "nat"},
		}
		return sc
	}
	trNo, err := Run(mk(false), nil)
	if err != nil {
		t.Fatal(err)
	}
	trAcc, err := Run(mk(true), nil)
	if err != nil {
		t.Fatal(err)
	}
	// 第 5->6 天这一步：不结转时可用 100；结转时可用 100*5(闲置)+100=600。
	if got := trNo.V[0][6]; math.Abs(got-100) > 1e-6 {
		t.Fatalf("不结转时第 6 天 V 应为 100，实际 %.6f", got)
	}
	if got := trAcc.V[0][6]; math.Abs(got-600) > 1e-6 {
		t.Fatalf("结转时第 6 天 V 应为 600，实际 %.6f", got)
	}
	t.Logf("判定依据: 前 5 天闲置额度 5*100=500; 不结转首日接种=%.1f, 结转首日接种=%.1f",
		trNo.V[0][6], trAcc.V[0][6])
}

// TestVaccinationCoexists 验证接种与接触缩减、隔离同情景同时生效，
// 状态不互相算乱：容量、额度各自约束成立，总人口守恒。
func TestVaccinationCoexists(t *testing.T) {
	capIso := 5.0
	sc := vaxScenario(40, false)
	sc.Paradigm = model.ParadigmSEIR
	sc.Parameters = model.Parameters{Beta: 0.5, Gamma: 0.1, Sigma: 0.25}
	sc.Interventions = []model.Intervention{
		{Type: model.InterventionContactReduction, Groups: []string{"a", "b"},
			StartDays: 2, EndDays: 18, Strength: 0.6, Compliance: 0.9},
		{Type: model.InterventionIsolation, Groups: []string{"a", "b"},
			StartDays: 2, EndDays: 18, RatePerDay: 2.0, Compliance: 0.9, Capacity: &capIso},
		{Type: model.InterventionVaccination, Groups: []string{"a"},
			StartDays: 4, EndDays: 16, RatePerDay: 0.4, Compliance: 1.0, QuotaID: "nat"},
		{Type: model.InterventionVaccination, Groups: []string{"b"},
			StartDays: 4, EndDays: 16, RatePerDay: 0.4, Compliance: 1.0, QuotaID: "nat", Priority: 2},
	}
	tr, err := Run(sc, nil)
	if err != nil {
		t.Fatal(err)
	}
	const pop0 = 1810.0
	for k := range tr.Times {
		if math.Abs(totalPop(tr, k)-pop0) > 1e-6 {
			t.Fatalf("k=%d 总人口不守恒: %.10f", k, totalPop(tr, k))
		}
		for g := range tr.GroupIDs {
			if tr.Q[g][k] > capIso+1e-6 {
				t.Fatalf("k=%d g=%d Q=%.6f 超过隔离容量", k, g, tr.Q[g][k])
			}
		}
		if k > 0 {
			dv := (tr.V[0][k] - tr.V[0][k-1]) + (tr.V[1][k] - tr.V[1][k-1])
			if dv > 40+1e-9 {
				t.Fatalf("k=%d 当日合计接种 %.6f 超过额度 40", k, dv)
			}
		}
	}
	if len(tr.CapacityEvents) == 0 || len(tr.VaccinationEvents) == 0 {
		t.Fatalf("共存场景应同时产生容量事件(%d)与接种事件(%d)",
			len(tr.CapacityEvents), len(tr.VaccinationEvents))
	}
	// 确定性：同情景重复运行逐位一致。
	tr2, err := Run(sc, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k := range tr.Times {
		for g := range tr.GroupIDs {
			if tr.V[g][k] != tr2.V[g][k] || tr.Q[g][k] != tr2.Q[g][k] {
				t.Fatalf("k=%d g=%d 重复运行结果不一致", k, g)
			}
		}
	}
	if len(tr.VaccinationEvents) != len(tr2.VaccinationEvents) {
		t.Fatal("重复运行接种事件数不一致")
	}
	t.Logf("共存场景: 容量事件 %d 次、接种事件 %d 次；容量/额度各自约束成立，"+
		"人口守恒，重复运行逐位一致", len(tr.CapacityEvents), len(tr.VaccinationEvents))
}

// TestVaccinationParallelDeterminism 验证含接种的情景在不同并行度下
// 批量推演结果（含接种事件序列）逐位一致。
func TestVaccinationParallelDeterminism(t *testing.T) {
	mk := func(limit float64, acc bool, prio int) *model.Scenario {
		sc := vaxScenario(limit, acc)
		sc.Interventions = []model.Intervention{
			{Type: model.InterventionVaccination, Groups: []string{"a"},
				StartDays: 1, EndDays: 19, RatePerDay: 0.6, Compliance: 1.0, QuotaID: "nat", Priority: prio},
			{Type: model.InterventionVaccination, Groups: []string{"b"},
				StartDays: 1, EndDays: 19, RatePerDay: 0.6, Compliance: 0.8, QuotaID: "nat"},
		}
		return sc
	}
	scs := []*model.Scenario{mk(50, false, 3), mk(80, true, 0), mk(20, false, 1)}
	seq, err := RunBatch(scs, RunOptions{Parallelism: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []int{2, 8} {
		par, err := RunBatch(scs, RunOptions{Parallelism: p})
		if err != nil {
			t.Fatal(err)
		}
		for i := range seq {
			if len(seq[i].VaccinationEvents) != len(par[i].VaccinationEvents) {
				t.Fatalf("p=%d sc=%d 接种事件数不一致", p, i)
			}
			for j, ev := range seq[i].VaccinationEvents {
				if ev != par[i].VaccinationEvents[j] {
					t.Fatalf("p=%d sc=%d 第 %d 条接种事件不一致", p, i, j)
				}
			}
			for k := range seq[i].Times {
				for g := range seq[i].GroupIDs {
					if seq[i].V[g][k] != par[i].V[g][k] {
						t.Fatalf("p=%d sc=%d k=%d g=%d V 不一致", p, i, k, g)
					}
				}
			}
		}
	}
	t.Log("3 个含接种的情景在并行度 1/2/8 下轨迹与接种事件逐位一致")
}
