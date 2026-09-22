package engine

import (
	"math"
	"testing"

	"github.com/lisama569/epidemic-simulation-go/internal/model"
)

// TestCapacityNeverNegative 验证容量上线下 Q 不超限、所有仓室非负，
// 且容量事件可定位到具体时间点与分组。
func TestCapacityNeverNegative(t *testing.T) {
	capacity := 10.0
	sc := &model.Scenario{
		ConfigVersion: 2, Paradigm: model.ParadigmSIR,
		Parameters:  model.Parameters{Beta: 0.8, Gamma: 0.05, Sigma: 0},
		HorizonDays: 30, StepDays: 0.25,
		Groups: []model.Group{
			{ID: "a", S0: 9900, I0: 100},
			{ID: "b", S0: 2000, I0: 0},
		},
		ContactMatrix: [][]float64{{3, 0.2}, {0.2, 3}},
		Interventions: []model.Intervention{
			{Type: model.InterventionIsolation, Groups: []string{"a", "b"},
				StartDays: 2, EndDays: 28, RatePerDay: 2.0, Compliance: 0.9, Capacity: &capacity},
		},
	}
	tr, err := Run(sc, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k := range tr.Times {
		for g := 0; g < 2; g++ {
			vals := map[string]float64{"S": tr.S[g][k], "I": tr.I[g][k], "R": tr.R[g][k], "Q": tr.Q[g][k], "E": tr.E[g][k]}
			for name, v := range vals {
				if v < -1e-9 || math.IsNaN(v) || math.IsInf(v, 0) {
					t.Fatalf("k=%d g=%d %s=%v 非物理状态", k, g, name, v)
				}
			}
			if tr.Q[g][k] > capacity+1e-6 {
				t.Fatalf("k=%d g=%d Q=%.6f 超过容量 %.1f", k, g, tr.Q[g][k], capacity)
			}
		}
	}
	if len(tr.CapacityEvents) == 0 {
		t.Fatal("高传播+小容量应当触发容量事件")
	}
	for _, ev := range tr.CapacityEvents {
		if ev.Reason != "isolation_capacity_exhausted" || ev.Admitted > ev.Available+1e-9 {
			t.Fatalf("容量事件内容异常: %+v", ev)
		}
		if !(ev.TimeDays >= 2 && ev.TimeDays < 28) {
			t.Fatalf("容量事件时间 %v 不在干预期内", ev.TimeDays)
		}
	}
	t.Logf("关键输入: I0=100, rate=2.0/day, compliance=0.9, capacity=%.0f; "+
		"共记录 %d 次容量受限（首次 t=%.2f 分组=%s 需求=%.2f 可用=%.2f）",
		capacity, len(tr.CapacityEvents),
		tr.CapacityEvents[0].TimeDays, tr.CapacityEvents[0].GroupID,
		tr.CapacityEvents[0].Demand, tr.CapacityEvents[0].Available)
}

// TestRunBatchParallelDeterminism 验证不同并行度下批量结论逐位一致。
func TestRunBatchParallelDeterminism(t *testing.T) {
	mk := func(beta float64, capV float64) *model.Scenario {
		sc := syntheticish(beta, capV)
		return sc
	}
	scs := []*model.Scenario{mk(0.2, 0), mk(0.35, 30), mk(0.5, 20), mk(0.7, 50)}
	seq, err := RunBatch(scs, RunOptions{Parallelism: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []int{2, 4, 16} {
		par, err := RunBatch(scs, RunOptions{Parallelism: p})
		if err != nil {
			t.Fatal(err)
		}
		for i := range seq {
			for k := range seq[i].Times {
				for g := range seq[i].GroupIDs {
					if seq[i].I[g][k] != par[i].I[g][k] {
						t.Fatalf("p=%d sc=%d k=%d 并行导致结果不一致", p, i, k)
					}
				}
			}
		}
	}
	t.Log("4 个情景在并行度 1/2/4/16 下轨迹逐位一致")
}

func syntheticish(beta, capV float64) *model.Scenario {
	sc := &model.Scenario{
		ConfigVersion: 2, Paradigm: model.ParadigmSEIR,
		Parameters:  model.Parameters{Beta: beta, Gamma: 0.1, Sigma: 0.2},
		HorizonDays: 20, StepDays: 0.5,
		Groups: []model.Group{
			{ID: "a", S0: 900, I0: 5},
			{ID: "b", S0: 300},
		},
		ContactMatrix:   [][]float64{{2, 0.4}, {0.4, 2}},
		MigrationMatrix: [][]float64{{0.9, 0.1}, {0.15, 0.85}},
	}
	if capV > 0 {
		c := capV
		sc.Interventions = []model.Intervention{
			{Type: model.InterventionIsolation, Groups: []string{"a", "b"},
				StartDays: 3, EndDays: 18, RatePerDay: 0.5, Compliance: 0.8, Capacity: &c},
		}
	}
	return sc
}

// TestRunBatchRejectsInvalid 验证批量中任一非法情景都返回归类错误。
func TestRunBatchRejectsInvalid(t *testing.T) {
	bad := syntheticish(0.3, 0)
	bad.MigrationMatrix = [][]float64{{0.5, 0.4}, {0.15, 0.85}} // 行和 0.9 不闭合
	if _, err := RunBatch([]*model.Scenario{syntheticish(0.3, 0), bad},
		RunOptions{Parallelism: 2}); err == nil {
		t.Fatal("批量推演应拒绝迁移不闭合的情景")
	} else {
		t.Logf("批量推演正确拒绝: %v", err)
	}
}

// TestMigrationConservationAnyStep 验证迁移在不同步长下都精确守恒且非负，
// 包括外迁比例很大（子步进必须触发）的情形。
func TestMigrationConservationAnyStep(t *testing.T) {
	for _, dt := range []float64{0.1, 0.5, 1, 2, 3.5} {
		sc := &model.Scenario{
			ConfigVersion: 2, Paradigm: model.ParadigmSIR,
			Parameters:  model.Parameters{Beta: 0.2, Gamma: 0.1, Sigma: 0},
			HorizonDays: 14, StepDays: dt,
			Groups: []model.Group{
				{ID: "a", S0: 800, I0: 10},
				{ID: "b", S0: 190},
			},
			// 高迁移率：每日 40% 外迁，子步进在 dt>=2.5 时必须细分
			ContactMatrix:   [][]float64{{2, 0.1}, {0.1, 2}},
			MigrationMatrix: [][]float64{{0.6, 0.4}, {0.3, 0.7}},
		}
		tr, err := Run(sc, nil)
		if err != nil {
			t.Fatalf("dt=%v: %v", dt, err)
		}
		for k := range tr.Times {
			tot := 0.0
			for g := 0; g < 2; g++ {
				for _, v := range []float64{tr.S[g][k], tr.I[g][k], tr.R[g][k]} {
					if v < -1e-9 {
						t.Fatalf("dt=%v k=%d 负值 %v", dt, k, v)
					}
					tot += v
				}
			}
			if math.Abs(tot-1000) > 1e-6 {
				t.Fatalf("dt=%v k=%d 人口不守恒 tot=%.10f", dt, k, tot)
			}
		}
	}
	t.Log("迁移矩阵在 dt∈{0.1,0.5,1,2,3.5} 下均非负且总人口恒为 1000")
}
