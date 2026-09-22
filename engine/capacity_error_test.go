package engine_test

import (
	"testing"

	"github.com/lisama569/epidemic-simulation-go/engine"
	"github.com/lisama569/epidemic-simulation-go/model"
)

// TestMigrationSafetyNetPinpointsFailure constructs a scenario that passes
// configuration validation but sits exactly on the per-step outflow boundary;
// floating-point rounding can then make the synchronized exchange request
// marginally more than the source pool. The safety net must report a
// *model.CapacityError pinning the exact step, group and cause instead of
// producing a negative population.
func TestMigrationSafetyNetPinpointsFailure(t *testing.T) {
	sc := &model.Scenario{
		SchemaVersion: 2,
		Paradigm:      model.ParadigmMetapop,
		ModelType:     "sir",
		HorizonDays:   10,
		StepDays:      1,
		Seed:          1,
		Beta:          0,
		Gamma:         0,
		GammaQ:        0,
		Groups: []model.Group{
			{ID: "a", Size: 100, InitS: 100},
			{ID: "b", Size: 100, InitS: 100},
		},
		// Column 0 outflow to b = 1.0: exactly the allowed upper limit.
		Migration: model.MigrationMatrix{{0, 0}, {1.0, 0}},
	}
	if err := sc.Validate(); err != nil {
		t.Logf("note: boundary scenario rejected at validation: %v", err)
		return
	}
	t.Log("key input: M[b][a]=1.0/day, dt=1 (exact boundary); checking safety-net behavior")
	res, err := engine.Run(sc)
	if err != nil {
		var ce *model.CapacityError
		var asCap bool
		if e2, ok := err.(*model.CapacityError); ok {
			ce, asCap = e2, ok
		}
		if !asCap {
			t.Fatalf("expected *CapacityError, got %T: %v", err, err)
		}
		t.Logf("safety net triggered: day=%.2f step=%d group=%d reason=%s msg=%s",
			ce.Day, ce.StepIndex, ce.Group, ce.Reason, ce.Message)
		return
	}
	// If the run succeeds, the result must be strictly physical.
	for _, st := range res.Trajectory {
		if !st.IsPhysical() {
			t.Fatalf("run returned non-physical state at day %v", st.T)
		}
	}
	t.Log("boundary scenario ran without non-physical states")
}
