package main

import (
	"fmt"
	"os"

	"github.com/lisama569/epidemic-simulation-go/estimate"
	"github.com/lisama569/epidemic-simulation-go/model"
	"github.com/lisama569/epidemic-simulation-go/synthetic"
)

// demoScenario builds a 2-group grouped SEIR scenario with staged
// interventions: an early contact reduction and a capacity-limited
// vaccination campaign.
func demoScenario() *model.Scenario {
	return &model.Scenario{
		SchemaVersion: 2,
		Paradigm:      model.ParadigmGrouped,
		ModelType:     "seir",
		HorizonDays:   120,
		StepDays:      0.5,
		Seed:          42,
		Beta:          0.5,
		Sigma:         0.2,
		Gamma:         0.1,
		GammaQ:        0.15,
		Groups: []model.Group{
			{ID: "young", Size: 8000, InitS: 7990, InitE: 0, InitI: 10, InitQ: 0, InitR: 0},
			{ID: "old", Size: 2000, InitS: 1995, InitE: 0, InitI: 5, InitQ: 0, InitR: 0},
		},
		Contacts: model.ContactMatrix{
			{0.9, 0.2},
			{0.2, 0.4},
		},
		Interventions: []model.Intervention{
			{Kind: model.InterventionContactReduction, StartDay: 10, EndDay: 30,
				Compliance: 0.8, Strength: 0.7},
			{Kind: model.InterventionVaccination, StartDay: 5, EndDay: 45,
				Compliance: 1, Rate: 0.2, Capacity: 40, Groups: []int{1}},
			{Kind: model.InterventionIsolation, StartDay: 15, EndDay: 60,
				Compliance: 0.9, Rate: 0.15, Capacity: 20},
		},
	}
}

// demoMetapop builds a 3-patch metapopulation SIR scenario with migration.
func demoMetapop() *model.Scenario {
	return &model.Scenario{
		SchemaVersion: 2,
		Paradigm:      model.ParadigmMetapop,
		ModelType:     "sir",
		HorizonDays:   40,
		StepDays:      1,
		Seed:          7,
		Beta:          0.4,
		Gamma:         0.1,
		GammaQ:        0.1,
		Groups: []model.Group{
			{ID: "city", Size: 10000, InitS: 9980, InitI: 20},
			{ID: "town", Size: 3000, InitS: 3000},
			{ID: "rural", Size: 1000, InitS: 1000},
		},
		Migration: model.MigrationMatrix{
			// dst rows, src columns: M[dst][src]
			{0, 0.01, 0.005},
			{0.02, 0, 0.01},
			{0.005, 0.01, 0},
		},
	}
}

// cmdResumeDemo runs an estimation with a tiny iteration budget so it stops
// un-converged and checkpoints, then resumes to completion, and shows the
// continued trajectory matches a single uninterrupted run.
func cmdResumeDemo() error {
	sc := demoScenario()
	obs, err := synthetic.Generate(sc)
	if err != nil {
		return err
	}
	path := filepathJoin(os.TempDir(), "episim-resume-demo.json")
	defer os.Remove(path)

	fmt.Println("segment 1: stop after 3 iterations (must checkpoint, not converge)")
	r1, err := estimate.Estimate(sc, obs, estimate.Options{
		BetaLow: 0.05, BetaHigh: 1.0, Tolerance: 1e-6, MaxIterations: 3,
		CheckpointPath: path,
	})
	if err != nil {
		return err
	}
	fmt.Printf("  status=%s iters=%d beta_hat=%.6f resumed=%v\n", r1.Status, r1.Iterations, r1.BetaHat, r1.Resumed)

	fmt.Println("segment 2: resume with full iteration budget")
	r2, err := estimate.Estimate(sc, obs, estimate.Options{
		BetaLow: 0.05, BetaHigh: 1.0, Tolerance: 1e-6, MaxIterations: 100,
		CheckpointPath: path,
	})
	if err != nil {
		return err
	}
	fmt.Printf("  status=%s iters=%d beta_hat=%.6f resumed=%v\n", r2.Status, r2.Iterations, r2.BetaHat, r2.Resumed)

	fmt.Println("reference: single uninterrupted run")
	r3, err := estimate.Estimate(sc, obs, estimate.Options{
		BetaLow: 0.05, BetaHigh: 1.0, Tolerance: 1e-6, MaxIterations: 100,
	})
	if err != nil {
		return err
	}
	fmt.Printf("  status=%s iters=%d beta_hat=%.6f CI95=[%.6f,%.6f]\n",
		r3.Status, r3.Iterations, r3.BetaHat, r3.CI95Lower, r3.CI95Upper)
	if r2.BetaHat != r3.BetaHat || r2.Iterations != r3.Iterations {
		return fmt.Errorf("resumed run disagrees with uninterrupted run: %+v vs %+v", r2, r3)
	}
	fmt.Println("resume OK: continued estimation is identical to the uninterrupted run")
	return nil
}
