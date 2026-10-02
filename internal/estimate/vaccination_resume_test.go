package estimate

import (
	"path/filepath"
	"testing"

	"github.com/lisama569/epidemic-simulation-go/internal/model"
	"github.com/lisama569/epidemic-simulation-go/internal/synthetic"
)

// vaxScenario 构造一个含疫苗额度池与两项接种安排的 SEIR 情景。
func vaxScenario() *model.Scenario {
	return &model.Scenario{
		ConfigVersion: 2, Paradigm: model.ParadigmSEIR,
		Parameters:  model.Parameters{Beta: 0.35, Gamma: 0.1, Sigma: 0.2},
		HorizonDays: 40, StepDays: 0.5,
		RandomSeed: 20261003,
		Groups: []model.Group{
			{ID: "city", S0: 980, I0: 20},
			{ID: "town", S0: 500},
		},
		ContactMatrix: [][]float64{{2.2, 0.4}, {0.4, 2.0}},
		VaccineQuotas: []model.VaccineQuota{
			{ID: "nat", DailyLimit: 30, StartDays: 5, EndDays: 35, Accumulate: false},
		},
		Interventions: []model.Intervention{
			{Type: model.InterventionVaccination, Groups: []string{"city"},
				StartDays: 5, EndDays: 35, RatePerDay: 0.08, Compliance: 1.0,
				QuotaID: "nat", Priority: 5},
			{Type: model.InterventionVaccination, Groups: []string{"town"},
				StartDays: 5, EndDays: 35, RatePerDay: 0.08, Compliance: 0.9,
				QuotaID: "nat", Priority: 1},
		},
	}
}

// TestVaccinationCheckpointResume 验证含接种的推演在估计中途从检查点恢复后，
// 与不中断运行结果逐位一致；同时覆盖 V 仓观测路径。
func TestVaccinationCheckpointResume(t *testing.T) {
	sc := vaxScenario()
	// 观测同时包含 I 仓与 V 仓（接种后的免疫状态可观测）。
	obsI, err := synthetic.GenerateObserved(sc, synthetic.GenerateOptions{
		SampleTimes: sampleTimes(), Compartment: model.CompI, NoiseSD: 0, Seed: 42,
	})
	if err != nil {
		t.Fatal(err)
	}
	obsV, err := synthetic.GenerateObserved(sc, synthetic.GenerateOptions{
		SampleTimes: sampleTimes(), Compartment: model.CompV, NoiseSD: 0, Seed: 42,
	})
	if err != nil {
		t.Fatal(err)
	}
	obs := obsI
	obs.Observations = append(obs.Observations, obsV.Observations...)

	full, err := Run(sc, obs, optsFor("", 1))
	if err != nil {
		t.Fatal(err)
	}
	if !full.Converged {
		t.Fatalf("不中断运行应收敛: %s", full.Reason)
	}
	t.Logf("关键输入: seed=42 noise=0 nObs=%d(含 V 仓观测); 不中断: betaHat=%.8f sse=%.6f",
		len(obs.Observations), full.BetaHat, full.BestSSE)

	// 在不同的中断轮数下中断再恢复，最终结果必须与不中断运行逐位一致。
	for stop := 1; stop <= 8; stop++ {
		dir := filepath.Join(t.TempDir(), "cp")
		partial := optsFor(dir, 1)
		partial.MaxCompletedRounds = stop
		if _, err := Run(sc, obs, partial); err != nil {
			t.Fatalf("stop=%d 中断运行失败: %v", stop, err)
		}
		resumed, err := Run(sc, obs, optsFor(dir, 4))
		if err != nil {
			t.Fatalf("stop=%d 恢复失败: %v", stop, err)
		}
		if !resumed.CheckpointRestored {
			t.Fatalf("stop=%d: 期望从检查点恢复", stop)
		}
		if resumed.BetaHat != full.BetaHat || resumed.BestSSE != full.BestSSE ||
			resumed.Lower != full.Lower || resumed.Upper != full.Upper {
			t.Fatalf("stop=%d: 续算结果与不中断运行不一致: %+v vs %+v",
				stop, resumed, full)
		}
	}
	t.Logf("判定依据: stop=1..8 轮中断续算的 betaHat/SSE/区间均与不中断运行逐位一致; "+
		"betaHat=%.8f 区间=[%.6f,%.6f]", full.BetaHat, full.Lower, full.Upper)
}
