package engine

import (
	"math"
	"testing"

	"github.com/lisama569/epidemic-simulation-go/internal/model"
)

func TestSmokeConservation(t *testing.T) {
	cap1 := 5.0
	sc := &model.Scenario{
		ConfigVersion: 2, Paradigm: model.ParadigmSEIR,
		Parameters:  model.Parameters{Beta: 0.35, Gamma: 0.1, Sigma: 0.2},
		HorizonDays: 40, StepDays: 0.5,
		Groups: []model.Group{
			{ID: "a", S0: 990, I0: 10},
			{ID: "b", S0: 500},
		},
		ContactMatrix:   [][]float64{{2, 0.5}, {0.5, 2}},
		MigrationMatrix: [][]float64{{0.9, 0.1}, {0.2, 0.8}},
		Interventions: []model.Intervention{
			{Type: model.InterventionContactReduction, Groups: []string{"a"},
				StartDays: 5, EndDays: 15, Strength: 0.6, Compliance: 0.8},
			{Type: model.InterventionIsolation, Groups: []string{"a", "b"},
				StartDays: 10, EndDays: 30, RatePerDay: 0.5, Compliance: 1, Capacity: &cap1},
		},
	}
	tr, err := Run(sc, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Times) != 81 {
		t.Fatalf("times=%d", len(tr.Times))
	}
	for k := range tr.Times {
		tot := 0.0
		for g := 0; g < 2; g++ {
			for _, v := range []float64{tr.S[g][k], tr.E[g][k], tr.I[g][k], tr.R[g][k], tr.Q[g][k]} {
				if v < -1e-9 || math.IsNaN(v) {
					t.Fatalf("bad value k=%d g=%d v=%v", k, g, v)
				}
				tot += v
			}
		}
		if math.Abs(tot-1500) > 1e-6 {
			t.Fatalf("population not conserved k=%d tot=%.10f", k, tot)
		}
	}
	if len(tr.CapacityEvents) == 0 {
		t.Fatal("expected capacity events")
	}
	tr2, _ := Run(sc, nil)
	for k := range tr.Times {
		for g := 0; g < 2; g++ {
			if tr.I[g][k] != tr2.I[g][k] {
				t.Fatalf("nondeterministic k=%d", k)
			}
		}
	}
}
