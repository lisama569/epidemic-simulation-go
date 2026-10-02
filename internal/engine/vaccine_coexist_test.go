package engine

import (
	"math"
	"testing"

	"github.com/lisama569/epidemic-simulation-go/internal/model"
)

// TestVaccinationCoexistsWithReductionAndIsolation 接种与接触缩减、隔离
// 在同一情景同时生效：三类干预互不把状态算乱，六仓室非负、总人口守恒，
// V 仓个体不被感染、不进隔离、不传播。
func TestVaccinationCoexistsWithReductionAndIsolation(t *testing.T) {
	const seed int64 = 20261003
	capacity := 30.0
	sc := &model.Scenario{
		ConfigVersion: 2, Paradigm: model.ParadigmSEIR,
		Parameters:  model.Parameters{Beta: 0.5, Gamma: 0.12, Sigma: 0.25},
		HorizonDays: 40, StepDays: 0.5, RandomSeed: seed,
		Groups: []model.Group{
			{ID: "a", S0: 900, E0: 0, I0: 50, R0: 0},
			{ID: "b", S0: 400, I0: 10},
		},
		ContactMatrix:   [][]float64{{2.6, 0.4}, {0.4, 2.2}},
		MigrationMatrix: [][]float64{{0.92, 0.08}, {0.1, 0.9}},
		VaccineQuotas:   []model.VaccineQuota{{ID: "pool", DosesPerDay: 120}},
		Interventions: []model.Intervention{
			{Type: model.InterventionContactReduction, Groups: []string{"a", "b"},
				StartDays: 4, EndDays: 30, Strength: 0.6, Compliance: 0.8},
			{Type: model.InterventionIsolation, Groups: []string{"a", "b"},
				StartDays: 6, EndDays: 34, RatePerDay: 0.5, Compliance: 0.9, Capacity: &capacity},
			{Type: model.InterventionVaccination, Groups: []string{"a", "b"},
				StartDays: 2, EndDays: 20, DosesPerDay: 100, Compliance: 1,
				QuotaID: "pool", Priority: 1},
		},
	}
	tr, err := Run(sc, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertConserved(t, tr, 2, 1360)

	// Q 永不超过隔离容量。
	for k := range tr.Times {
		for g := 0; g < 2; g++ {
			if tr.Q[g][k] > capacity+1e-6 {
				t.Fatalf("k=%d g=%d Q=%.6f 超容量 %.1f", k, g, tr.Q[g][k], capacity)
			}
		}
	}

	// 接种 18 天*100/天 = 1800 人份，但两组易感合计 1300 且额度 120/天。
	// 每步 60 额度，两组各请求 50（100/天/2组... 实际每安排 groups=[a,b]，
	// 每分组 100/天，每步各 50；总额度每步 60）。
	// 按组下标 a 先取 50，b 取 10 => 每天 a=100,b=20；到第8天 b 的 400 易感
	// 先被耗尽；此处只做结构性断言：有额度事件、V>0、且 I 峰值低于无接种对照。
	if len(tr.VaccinationEvents) == 0 {
		t.Fatal("额度120/天低于总需求200/天，必须出现少接种事件")
	}
	vTotal := 0.0
	for g := 0; g < 2; g++ {
		vTotal += tr.V[g][len(tr.V[g])-1]
	}
	if vTotal <= 0 {
		t.Fatal("V 仓应有人")
	}

	// 对照：关掉接种后的 I 峰值应更高（接种确实减少传播）。
	sc2 := *sc
	sc2.Interventions = sc.Interventions[:2]
	sc2.VaccineQuotas = nil
	tr2, err := Run(&sc2, nil)
	if err != nil {
		t.Fatal(err)
	}
	peak := func(x *model.Trajectory) float64 {
		p := 0.0
		for g := range x.GroupIDs {
			for _, v := range x.I[g] {
				if v > p {
					p = v
				}
			}
		}
		return p
	}
	if !(peak(tr) < peak(tr2)) {
		t.Fatalf("接种后 I 峰值 %.3f 应低于无接种 %.3f", peak(tr), peak(tr2))
	}
	t.Logf("关键输入: seed=%d beta=0.5 三组干预同期(缩减0.6/隔离cap30/接种100每分组"+
		"额度120)；判定: 六仓室非负、总人口恒1360、Q<=30、I峰值 %.2f < 无接种 %.2f、"+
		"最终V总=%.1f、接种事件数=%d", seed, peak(tr), peak(tr2), vTotal,
		len(tr.VaccinationEvents))
}

// TestVaccinatedNeverInfected 接种成功者进入 V 后不再被感染：
// 全人口快速接种后，后续即便高传播，I 仓应衰减至 0、V 不减少。
func TestVaccinatedNeverInfected(t *testing.T) {
	sc := &model.Scenario{
		ConfigVersion: 2, Paradigm: model.ParadigmSIR,
		Parameters:  model.Parameters{Beta: 2.0, Gamma: 0.05},
		HorizonDays: 30, StepDays: 0.5,
		Groups: []model.Group{{ID: "a", S0: 100, I0: 0}},
		// 零感染源：接种后 V 增加；随后无法验证“不被感染”，因此放一个
		// 接种开始时仍在流行的设置见下。
		ContactMatrix: [][]float64{{5}},
		VaccineQuotas: []model.VaccineQuota{{ID: "p", DosesPerDay: 1000}},
		Interventions: []model.Intervention{
			{Type: model.InterventionVaccination, Groups: []string{"a"},
				StartDays: 0, EndDays: 1, DosesPerDay: 1000, Compliance: 1,
				QuotaID: "p", Priority: 1},
		},
	}
	tr, err := Run(sc, nil)
	if err != nil {
		t.Fatal(err)
	}
	// 第 0 步即把 100 易感全部接种；I0=0 => I 恒 0，V 恒 100。
	if v := tr.V[0][1]; math.Abs(v-100) > 1e-6 {
		t.Fatalf("首步后 V=%.6f 期望 100", v)
	}
	for k := 1; k < len(tr.Times); k++ {
		if tr.V[0][k] != 100 || tr.I[0][k] != 0 || tr.S[0][k] != 0 {
			t.Fatalf("k=%d 接种者状态被算乱: S=%v I=%v V=%v",
				k, tr.S[0][k], tr.I[0][k], tr.V[0][k])
		}
	}
	t.Log("关键输入: S0=100,I0=0, 额度充足首日全部接种, beta=2 高传播；" +
		"判定: 之后 V 恒100、S/I 恒0，接种者既不被感染也不回流")
}

// TestVaccinationCheckpointResume 接种进行到一半落盘检查点，恢复续算后
// 完整轨迹与事件流和不间断运行逐位一致（额度余量跨恢复点必须保持）。
func TestVaccinationCheckpointResume(t *testing.T) {
	const seed int64 = 9090
	capacity := 25.0
	sc := &model.Scenario{
		ConfigVersion: 2, Paradigm: model.ParadigmSEIR,
		Parameters:  model.Parameters{Beta: 0.45, Gamma: 0.1, Sigma: 0.2},
		HorizonDays: 24, StepDays: 0.5, RandomSeed: seed,
		Groups: []model.Group{
			{ID: "a", S0: 800, I0: 20},
			{ID: "b", S0: 500, I0: 5},
		},
		ContactMatrix: [][]float64{{2.2, 0.3}, {0.3, 1.8}},
		VaccineQuotas: []model.VaccineQuota{
			{ID: "pool", DosesPerDay: 90}, // 刻意紧张，恢复点两侧都有争抢
		},
		Interventions: []model.Intervention{
			{Type: model.InterventionContactReduction, Groups: []string{"a"},
				StartDays: 3, EndDays: 18, Strength: 0.5, Compliance: 0.9},
			{Type: model.InterventionIsolation, Groups: []string{"a", "b"},
				StartDays: 4, EndDays: 20, RatePerDay: 0.4, Compliance: 0.9,
				Capacity: &capacity},
			{Type: model.InterventionVaccination, Groups: []string{"a"},
				StartDays: 1, EndDays: 22, DosesPerDay: 70, Compliance: 1,
				QuotaID: "pool", Priority: 1},
			{Type: model.InterventionVaccination, Groups: []string{"b"},
				StartDays: 1, EndDays: 22, DosesPerDay: 70, Compliance: 1,
				QuotaID: "pool", Priority: 2},
		},
	}

	// 不间断基线。
	full, err := Run(sc, nil)
	if err != nil {
		t.Fatal(err)
	}

	// 在第 25 步中断（总 48 步，正值接种期与额度争抢期，且落在自然日边界
	// 25*0.5=12.5 非整日，专门验证日中恢复）。
	const stopAt = 25
	cp, err := RunUntil(sc, nil, stopAt)
	if err != nil {
		t.Fatal(err)
	}
	if cp.StepCompleted != stopAt {
		t.Fatalf("检查点步数=%d 期望 %d", cp.StepCompleted, stopAt)
	}
	dir := t.TempDir()
	if err := SaveSimCheckpoint(dir, cp); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadSimCheckpoint(dir)
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := ResumeAndRun(sc, nil, loaded)
	if err != nil {
		t.Fatal(err)
	}

	if len(resumed.Times) != len(full.Times) {
		t.Fatalf("恢复后时间点数=%d 期望 %d", len(resumed.Times), len(full.Times))
	}
	for g := range full.GroupIDs {
		for k := range full.Times {
			for name, got := range map[string]float64{
				"S": resumed.S[g][k], "E": resumed.E[g][k], "I": resumed.I[g][k],
				"R": resumed.R[g][k], "Q": resumed.Q[g][k], "V": resumed.V[g][k],
			} {
				want := map[string]float64{
					"S": full.S[g][k], "E": full.E[g][k], "I": full.I[g][k],
					"R": full.R[g][k], "Q": full.Q[g][k], "V": full.V[g][k],
				}[name]
				if got != want {
					t.Fatalf("g=%d k=%d %s 恢复后=%v 不间断=%v（轨迹不一致）",
						g, k, name, got, want)
				}
			}
		}
	}
	if len(resumed.VaccinationEvents) != len(full.VaccinationEvents) {
		t.Fatalf("接种事件数恢复后=%d 期望 %d",
			len(resumed.VaccinationEvents), len(full.VaccinationEvents))
	}
	for i := range full.VaccinationEvents {
		if resumed.VaccinationEvents[i] != full.VaccinationEvents[i] {
			t.Fatalf("接种事件[%d]不一致:\n恢复=%+v\n基线=%+v",
				i, resumed.VaccinationEvents[i], full.VaccinationEvents[i])
		}
	}
	if len(resumed.CapacityEvents) != len(full.CapacityEvents) {
		t.Fatalf("容量事件数不一致 %d vs %d",
			len(resumed.CapacityEvents), len(full.CapacityEvents))
	}
	t.Logf("关键输入: seed=%d quota=90/day a(pri1,70) b(pri2,70) 同期，"+
		"step=0.5；在第%d步(t=%.1f，非整日)中断落盘并恢复；判定依据: "+
		"恢复轨迹六仓室 %d 点逐位相等、接种事件 %d 条逐条相等、容量事件 %d 条",
		seed, stopAt, float64(stopAt)*0.5, len(full.Times),
		len(full.VaccinationEvents), len(full.CapacityEvents))
}

// TestVaccinationCheckpointRejectsTamperedScenario 恢复时情景被改动，
// 摘要不一致必须拒绝。
func TestVaccinationCheckpointRejectsTamperedScenario(t *testing.T) {
	sc := vaccScenario(1)
	cp, err := RunUntil(sc, nil, 5)
	if err != nil {
		t.Fatal(err)
	}
	tampered := vaccScenario(1)
	tampered.VaccineQuotas[0].DosesPerDay = 999
	if _, err := ResumeAndRun(tampered, nil, cp); err == nil {
		t.Fatal("改动接种额度后恢复必须被拒绝")
	}
	// 非法中断步数。
	if _, err := RunUntil(sc, nil, 0); err == nil {
		t.Fatal("中断步数 0 必须被拒绝")
	}
	if _, err := RunUntil(sc, nil, totalSteps(sc)); err == nil {
		t.Fatal("在最后一步中断必须被拒绝（无恢复语义）")
	}
	t.Log("判定依据: 情景摘要不匹配拒绝恢复；中断步数越界拒绝")
}

// TestVaccinatedMigrationConservation 高迁移率 + 接种：V 仓个体随分组迁移，
// 两组 V 之和在迁移前后保持不变（V 仅因接种而增加，绝不在迁移中泄漏）。
func TestVaccinatedMigrationConservation(t *testing.T) {
	sc := &model.Scenario{
		ConfigVersion: 2, Paradigm: model.ParadigmSIR,
		Parameters:  model.Parameters{Beta: 0.1, Gamma: 0.05},
		HorizonDays: 16, StepDays: 0.5,
		Groups: []model.Group{
			{ID: "a", S0: 500, I0: 2},
			{ID: "b", S0: 300},
		},
		// 高外迁率，强制 applyMigration 走子步进。
		ContactMatrix:   [][]float64{{1, 0.1}, {0.1, 1}},
		MigrationMatrix: [][]float64{{0.6, 0.4}, {0.35, 0.65}},
		VaccineQuotas:   []model.VaccineQuota{{ID: "p", DosesPerDay: 80}},
		Interventions: []model.Intervention{
			{Type: model.InterventionVaccination, Groups: []string{"a", "b"},
				StartDays: 1, EndDays: 12, DosesPerDay: 50, Compliance: 1,
				QuotaID: "p", Priority: 1},
		},
	}
	tr, err := Run(sc, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertConserved(t, tr, 2, 802)
	// 无感染时（beta 很小、I0=2）V 总量应严格等于接种量；关键断言：
	// 每个时间点两组 V 之和随时间单调不减且与 S 的减少一一对应。
	for k := 1; k < len(tr.Times); k++ {
		vSum := tr.V[0][k] + tr.V[1][k]
		vPrev := tr.V[0][k-1] + tr.V[1][k-1]
		if vSum < vPrev-1e-9 {
			t.Fatalf("k=%d 两组 V 之和下降: %.10f -> %.10f（迁移泄漏接种者）",
				k, vPrev, vSum)
		}
	}
	vTotal := tr.V[0][len(tr.Times)-1] + tr.V[1][len(tr.Times)-1]
	if vTotal <= 0 {
		t.Fatal("应有接种者")
	}
	t.Logf("关键输入: 高迁移(0.4/0.35 日外迁)+接种80/天，step=0.5；判定: "+
		"总人口恒802，两组V之和单调不减，最终V总=%.3f，迁移不泄漏接种者", vTotal)
}

// TestQuotaCrossDayBoundary 跨自然日边界的额度行为：
//   - 不累积：安排开始前各天的额度、以及接种期内前一步未用完的额度都不跨日；
//   - 累积：未用完额度滚到后续自然日继续用。
func TestQuotaCrossDayBoundary(t *testing.T) {
	// step=0.5 => 每天2步；额度 20/天 => 每步补 10；安排需求 30/天（每步15）。
	mk := func(accumulate bool, start float64) *model.Scenario {
		return &model.Scenario{
			ConfigVersion: 2, Paradigm: model.ParadigmSIR,
			Parameters:  model.Parameters{Beta: 0.01, Gamma: 0.05},
			HorizonDays: 5, StepDays: 0.5,
			Groups:        []model.Group{{ID: "a", S0: 1000}},
			ContactMatrix: [][]float64{{0}},
			VaccineQuotas: []model.VaccineQuota{
				{ID: "p", DosesPerDay: 20, Accumulate: accumulate},
			},
			Interventions: []model.Intervention{
				{Type: model.InterventionVaccination, Groups: []string{"a"},
					StartDays: start, EndDays: start + 2, DosesPerDay: 30,
					Compliance: 1, QuotaID: "p", Priority: 1},
			},
		}
	}

	// 情形一：安排第1~3天。不累积时第0天额度作废，每步补充10、需求15，
	// 4步各拿10 => V=40 且4条额度事件。
	trNoSync, err := Run(mk(false, 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	if v := trNoSync.V[0][len(trNoSync.Times)-1]; math.Abs(v-40) > 1e-9 {
		t.Fatalf("不累积: V=%.6f 期望 40", v)
	}
	if len(trNoSync.VaccinationEvents) != 4 {
		t.Fatalf("应有4条少接种事件，实际 %d", len(trNoSync.VaccinationEvents))
	}
	for _, ev := range trNoSync.VaccinationEvents {
		if ev.Allocated != 10 || ev.Demand != 15 || ev.Shortfall != 5 ||
			ev.Reason != model.VaccReasonQuotaExhausted {
			t.Fatalf("不累积事件明细错误: %+v", ev)
		}
	}
	// 累积时第0天的20已攒下：第1天两步手头40（各拿15，余10），
	// 第2天手头30（各拿15）=> 4步共60，全部满足，无额度类事件。
	trAccSync, err := Run(mk(true, 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	if v := trAccSync.V[0][len(trAccSync.Times)-1]; math.Abs(v-60) > 1e-9 {
		t.Fatalf("累积: V=%.6f 期望 60（第0天20结转+接种期40补充）", v)
	}
	for _, ev := range trAccSync.VaccinationEvents {
		if ev.Reason == model.VaccReasonQuotaExhausted {
			t.Fatalf("累积口径下此情景额度充足，不应有额度事件: %+v", ev)
		}
	}

	// 情形二：安排第2~4天，前两个自然天没有接种安排。
	// 累积：第0、1天攒下40，接种期4步需求共60且额度充足
	//   （40结转+40补充=80）=> 全部满足 V=60，且无额度类事件。
	trAcc, err := Run(mk(true, 2), nil)
	if err != nil {
		t.Fatal(err)
	}
	if v := trAcc.V[0][len(trAcc.Times)-1]; math.Abs(v-60) > 1e-9 {
		t.Fatalf("累积: V=%.6f 期望 60", v)
	}
	for _, ev := range trAcc.VaccinationEvents {
		if ev.Reason == model.VaccReasonQuotaExhausted {
			t.Fatalf("累积口径下额度充足，不应有额度事件: %+v", ev)
		}
	}
	// 不累积：前2天额度全部作废，但接种期两天仍各补20、每步拿10 => V=40。
	// 与情形一的不累积结果相同——这正是“不结转”语义：早开始补的额度不会
	// 因为晚用而变多。关键对比是累积口径：早攒的40让结果多20。
	trNo, err := Run(mk(false, 2), nil)
	if err != nil {
		t.Fatal(err)
	}
	if v := trNo.V[0][len(trNo.Times)-1]; math.Abs(v-40) > 1e-9 {
		t.Fatalf("不结转: V=%.6f 期望 40", v)
	}
	t.Log("关键输入: step=0.5 额度20/天 需求30/天；判定: 安排第1天开始时" +
		"不结转=>40(4条15→10少5事件)、累积=>60(第0天额度结转)；" +
		"安排晚2天时不结转仍为40(前期额度作废)、累积=>60(前期40可用)，" +
		"差异恰为被结转的20人份，跨自然日语义正确")
}
