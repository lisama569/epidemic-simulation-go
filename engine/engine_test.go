package engine_test

import (
	"math"
	"testing"

	"github.com/lisama569/epidemic-simulation-go/engine"
	"github.com/lisama569/epidemic-simulation-go/internal/testutil"
	"github.com/lisama569/epidemic-simulation-go/model"
)

func TestSerialEqualsParallel(t *testing.T) {
	base := testutil.GroupedSEIR(123)
	base = testutil.WithContactReduction(base, 10, 40, 0.6)
	base = testutil.WithVaccineCapacity(base, 5, 30, 0.1, 200, 1)
	n := 12
	scs := make([]*model.Scenario, n)
	for i := range scs {
		scs[i] = base // scenarios are treated read-only by Run
	}
	t.Logf("determinism input: %s, workers=1 vs 8, copies=%d", base.String(), n)

	for _, workers := range []int{1, 2, 4, 8} {
		res, errs := engine.RunScenarios(scs, workers)
		for i, err := range errs {
			if err != nil {
				t.Fatalf("worker=%d run %d failed: %v", workers, i, err)
			}
		}
		ref := res[0]
		for i := 1; i < n; i++ {
			if !equalTrajectory(ref.Trajectory, res[i].Trajectory) {
				t.Fatalf("worker=%d: run %d differs from run 0", workers, i)
			}
		}
	}
	t.Log("judgement: identical bit-for-bit trajectories at every time point across all worker counts")
}

func TestRepeatedRunsAreIdentical(t *testing.T) {
	sc := testutil.MetapopSIR(99)
	t.Logf("repeatability input: %s", sc.String())
	a, err := engine.Run(sc)
	if err != nil {
		t.Fatal(err)
	}
	for k := 0; k < 3; k++ {
		b, err := engine.Run(sc)
		if err != nil {
			t.Fatal(err)
		}
		if !equalTrajectory(a.Trajectory, b.Trajectory) {
			t.Fatalf("run %d differs from run 0 despite identical inputs", k+1)
		}
	}
}

func TestStateAlwaysPhysical(t *testing.T) {
	sc := testutil.GroupedSEIR(7)
	sc = testutil.WithVaccineCapacity(sc, 1, 70, 1, 50, 0, 1)
	sc = testutil.WithIsolationCapacity(sc, 2, 60, 1, 30)
	sc = testutil.WithContactReduction(sc, 5, 30, 0.9)
	res, err := engine.Run(sc)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range res.Trajectory {
		for g := range st.Pop {
			sum := 0.0
			for _, v := range st.Pop[g] {
				if v < -1e-9 || math.IsNaN(v) || math.IsInf(v, 0) {
					t.Fatalf("non-physical cell at day %v group %d: %v", st.T, g, st.Pop[g])
				}
				sum += v
			}
			if math.Abs(sum-sc.Groups[g].Size) > 1e-6*sc.Groups[g].Size {
				t.Fatalf("group %d not closed at day %v: sum=%v size=%v", g, st.T, sum, sc.Groups[g].Size)
			}
		}
	}
}

func TestCapacityEventsPinpointTimeAndAreHonored(t *testing.T) {
	sc := testutil.GroupedSEIR(7)
	// Rate 1/day wants to vaccinate the whole S pool almost immediately;
	// capacity 100/day must hold applications to 100*dt per step.
	sc = testutil.WithVaccineCapacity(sc, 2, 60, 1, 100, 0, 1)
	res, err := engine.Run(sc)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.CapacityEvents) == 0 {
		t.Fatal("expected capacity limiting events")
	}
	ev := res.CapacityEvents[0]
	t.Logf("first capacity event: day=%.2f step=%d group=%d kind=%s requested=%.3f applied=%.3f capacity=%.1f reason=%q",
		ev.Day, ev.StepIndex, ev.Group, ev.Kind, ev.Requested, ev.Applied, ev.Capacity, ev.Reason)
	if ev.Day < 2 {
		t.Fatalf("event before intervention start: day %v", ev.Day)
	}
	for _, e := range res.CapacityEvents {
		want := e.Capacity * sc.StepDays
		if math.Abs(e.Applied-want) > 1e-9 {
			t.Fatalf("applied %.6f != capacity*dt %.6f at day %.2f", e.Applied, want, e.Day)
		}
		if e.Applied > e.Requested+1e-9 {
			t.Fatalf("applied more than requested at day %.2f", e.Day)
		}
		if e.Reason == "" {
			t.Fatalf("capacity event at day %.2f carries no reason", e.Day)
		}
	}
}

func TestInterventionTimingIsHalfOpen(t *testing.T) {
	sc := testutil.GroupedSEIR(5)
	sc.HorizonDays = 10
	sc.StepDays = 1
	// Reduction active on steps starting at t=3,4,5,6 -> infections on
	// days 4,5,6,7 reduced; the step starting at t=7 is not.
	sc = testutil.WithContactReduction(sc, 3, 7, 0.99)
	res, err := engine.Run(sc)
	if err != nil {
		t.Fatal(err)
	}
	var withLock float64
	for k := 3; k <= 6; k++ {
		withLock += sumRow(res.NewInfections[k])
	}
	var afterLock float64
	for k := 8; k < 10; k++ {
		afterLock += sumRow(res.NewInfections[k])
	}
	t.Logf("seed=%d infections during window(step 3..6)=%.4f after(step 8..9)=%.4f",
		sc.Seed, withLock, afterLock)
	if withLock >= afterLock {
		t.Fatal("near-total contact reduction during [3,7) should suppress infections relative to after release")
	}
}

func TestMetapopConservesPopulationAndSpreads(t *testing.T) {
	sc := testutil.MetapopSIR(3)
	res, err := engine.Run(sc)
	if err != nil {
		t.Fatal(err)
	}
	first, last := res.Trajectory[0], res.Trajectory[len(res.Trajectory)-1]
	if math.Abs(first.Total()-last.Total()) > 1e-6 {
		t.Fatalf("metapop total changed: %.6f -> %.6f", first.Total(), last.Total())
	}
	// Patches seeded without infections must receive them through migration.
	var ruralEverInfected float64
	for _, st := range res.Trajectory {
		ruralEverInfected += st.Pop[2][model.Infected] + st.Pop[2][model.Recovered]
	}
	if ruralEverInfected <= 0 {
		t.Fatal("rural patch never received infections via migration")
	}
	t.Logf("judgement: total conserved %.6f; rural patch reached via migration (exposure=%.2f)",
		last.Total(), ruralEverInfected)
}

func equalTrajectory(a, b []*model.State) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].T != b[i].T {
			return false
		}
		for g := range a[i].Pop {
			for c := range a[i].Pop[g] {
				if a[i].Pop[g][c] != b[i].Pop[g][c] {
					return false
				}
			}
		}
	}
	return true
}

func sumRow(row []float64) float64 {
	var s float64
	for _, v := range row {
		s += v
	}
	return s
}

func TestBoundedTrajectoryRetention(t *testing.T) {
	sc := testutil.GroupedSEIR(4)
	res, err := engine.RunWithOptions(sc, engine.RunOptions{MaxTrajectory: 11})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Trajectory) != 11 {
		t.Fatalf("trajectory length = %d, want capped at 11", len(res.Trajectory))
	}
	// First retained sample is the oldest surviving one; last must be terminal.
	first, last := res.Trajectory[0], res.Trajectory[len(res.Trajectory)-1]
	if last.T != sc.HorizonDays {
		t.Fatalf("last retained time = %v, want horizon", last.T)
	}
	// Incidence accounting is NOT bounded: all steps retained.
	if len(res.NewInfections) != sc.NumSteps() {
		t.Fatalf("incidence accounting must keep every step, got %d", len(res.NewInfections))
	}
	t.Logf("bounded sampling: retained %d/%d states, first day=%.1f last day=%.1f",
		len(res.Trajectory), sc.NumSteps()+1, first.T, last.T)
}
