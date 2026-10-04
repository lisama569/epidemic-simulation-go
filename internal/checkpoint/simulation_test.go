package checkpoint

import (
	"math"
	"testing"

	"github.com/lisama569/epidemic-simulation-go/internal/model"
)

// TestSimulationCheckpointRoundTrip 验证推演断点落盘/读取后浮点状态逐位一致
// （JSON 最短表示精确往返），且摘要校验能区分情景变更。
func TestSimulationCheckpointRoundTrip(t *testing.T) {
	sc := &model.Scenario{
		ConfigVersion: 2, Paradigm: model.ParadigmSIR,
		Parameters:  model.Parameters{Beta: 0.3, Gamma: 0.1},
		HorizonDays: 10, StepDays: 0.5,
		Groups:        []model.Group{{ID: "a", S0: 95, I0: 5}},
		ContactMatrix: [][]float64{{2.0}},
		VaccineQuotas: []model.VaccineQuota{
			{ID: "nat", DailyLimit: 40, StartDays: 0, EndDays: 10, Accumulate: true},
		},
	}
	st := &SimState{
		Step: 13, TimeDays: 6.5, // 非整日断点
		S:          []float64{math.Nextafter(90.123456789, 0)},
		E:          []float64{0},
		I:          []float64{7.654321098},
		R:          []float64{2.000000001},
		Q:          []float64{0.5},
		V:          []float64{0.25},
		QuotaCarry: []float64{123.456789012345},
		Times:      []float64{0, 0.5, 1.0},
		CompS:      [][]float64{{95, 94.9, 94.8}},
		CompE:      [][]float64{{0, 0, 0}},
		CompI:      [][]float64{{5, 5.1, 5.2}},
		CompR:      [][]float64{{0, 0.1, 0.2}},
		CompQ:      [][]float64{{0, 0, 0}},
		CompV:      [][]float64{{0, 0.1, 0.2}},
		VaccinationEvents: []model.VaccinationEvent{
			{TimeDays: 6.0, GroupID: "a", QuotaID: "nat", Demand: 10.5, Allocated: 4.25,
				Reason: "vaccine_quota_exhausted"},
		},
		CapacityEvents: []model.CapacityEvent{
			{TimeDays: 5.5, GroupID: "a", Demand: 3, Available: 1, Admitted: 1,
				Reason: "isolation_capacity_exhausted"},
		},
		ScenarioDigest: Digest(sc),
	}
	dir := t.TempDir()
	if SimulationExists(dir) {
		t.Fatal("落盘前不应存在断点")
	}
	if err := SaveSimulation(dir, st); err != nil {
		t.Fatal(err)
	}
	if !SimulationExists(dir) {
		t.Fatal("落盘后应存在断点")
	}
	got, err := LoadSimulation(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Step != st.Step || got.TimeDays != st.TimeDays {
		t.Fatalf("步数/日期不一致: %+v", got)
	}
	for name, pair := range map[string][2][]float64{
		"S": {st.S, got.S}, "I": {st.I, got.I}, "V": {st.V, got.V},
		"carry": {st.QuotaCarry, got.QuotaCarry},
	} {
		for i := range pair[0] {
			if pair[0][i] != pair[1][i] {
				t.Fatalf("%s[%d] 未逐位往返: %.17g vs %.17g", name, i, pair[0][i], pair[1][i])
			}
		}
	}
	if got.VaccinationEvents[0] != st.VaccinationEvents[0] ||
		got.CapacityEvents[0] != st.CapacityEvents[0] {
		t.Fatal("事件记录未逐条往返")
	}
	if err := got.VerifyScenario(sc); err != nil {
		t.Fatalf("同一情景摘要应通过: %v", err)
	}
	other := *sc
	other.Parameters.Beta = 0.31
	if err := got.VerifyScenario(&other); err == nil {
		t.Fatal("情景变更后摘要校验应拒绝")
	}
	t.Logf("关键输入: 断点步=13(非整日 t=6.5), 结转余额=%.15g; "+
		"判定依据: 浮点状态/事件逐位往返, 摘要可区分情景变更", st.QuotaCarry[0])
}
