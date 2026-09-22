// Package engine executes validated scenarios deterministically.
package engine

import (
	"sort"
	"sync"

	"github.com/lisama569/epidemic-simulation-go/model"
)

// Result is the deterministic outcome of one scenario run.
type Result struct {
	Scenario       *model.Scenario
	Trajectory     []*model.State
	NewInfections  [][]float64 // [step][group], infections produced by step k
	CapacityEvents []model.CapacityEvent
}

// Run executes one validated scenario end to end.
//
// Intervention timing rule (half-open intervals): an intervention is active
// on step k (advancing t -> t+dt) iff start_day <= t < end_day. Thus the
// activation/deactivation instants are exact grid-independent points, and
// overlapping intervals of the same kind are rejected at validation time.
//
// Trajectory retention is bounded: if capSamples > 0 and the trajectory
// exceeds it, the oldest samples are discarded while the final state is
// always kept. Capacity events, step count and the final state are never
// discarded, so resumable continuation remains exact.
func Run(sc *model.Scenario) (*Result, error) {
	return run(sc, 0, nil)
}

func run(sc *model.Scenario, capSamples int, resume *resumePoint) (*Result, error) {
	if err := sc.Validate(); err != nil {
		return nil, err
	}
	dyn, err := model.NewDynamics(sc)
	if err != nil {
		return nil, err
	}

	var st *model.State
	startStep := 0
	if resume != nil {
		st = resume.state.Clone()
		startStep = resume.stepIndex
	} else {
		st = sc.InitialState()
	}

	steps := sc.NumSteps()
	res := &Result{Scenario: sc, NewInfections: make([][]float64, steps)}
	for k := range res.NewInfections {
		res.NewInfections[k] = make([]float64, len(sc.Groups))
	}
	keep := func(s *model.State) {
		res.Trajectory = appendBounded(res.Trajectory, s, capSamples)
	}
	// Both fresh starts and resumes first retain the starting snapshot, so
	// trajectory indexing is uniform: Trajectory[k] is the state AFTER k
	// steps. A resumed run therefore begins with the snapshot itself.
	keep(st.Clone())

	for k := startStep; k < steps; k++ {
		active := activeAt(sc.Interventions, st.T)
		out, err := dyn.Step(st, sc.StepDays, active, k)
		if err != nil {
			return nil, err
		}
		copy(res.NewInfections[k], out.NewInfections)
		res.CapacityEvents = append(res.CapacityEvents, out.CapacityEvents...)
		keep(st.Clone())
	}
	// Guarantee the terminal state is retained even under bounded sampling.
	if capSamples > 0 && len(res.Trajectory) > 0 {
		res.Trajectory[len(res.Trajectory)-1] = st.Clone()
	}
	sortCapacityEvents(res.CapacityEvents)
	return res, nil
}

func appendBounded(traj []*model.State, s *model.State, cap int) []*model.State {
	if cap <= 0 || len(traj) < cap {
		return append(traj, s)
	}
	copy(traj, traj[1:])
	traj[cap-1] = s
	return traj
}

func sortCapacityEvents(evs []model.CapacityEvent) {
	sort.SliceStable(evs, func(i, j int) bool {
		if evs[i].StepIndex != evs[j].StepIndex {
			return evs[i].StepIndex < evs[j].StepIndex
		}
		if evs[i].Group != evs[j].Group {
			return evs[i].Group < evs[j].Group
		}
		return evs[i].Kind < evs[j].Kind
	})
}

// activeAt returns the interventions active at time t using the half-open
// interval rule start_day <= t < end_day.
func activeAt(ivs []model.Intervention, t float64) []model.Intervention {
	var active []model.Intervention
	for _, iv := range ivs {
		if iv.StartDay <= t && t < iv.EndDay {
			active = append(active, iv)
		}
	}
	return active
}

// RunScenarios executes several scenarios, optionally in parallel. Workers
// run independent copies; no run reads another run's state and results are
// returned in input order, so worker count (including 0/1 = serial) cannot
// change any conclusion.
func RunScenarios(scenarios []*model.Scenario, workers int) ([]*Result, []error) {
	if workers <= 0 {
		workers = 1
	}
	results := make([]*Result, len(scenarios))
	errs := make([]error, len(scenarios))
	if workers == 1 || len(scenarios) == 1 {
		for i, sc := range scenarios {
			results[i], errs[i] = Run(sc)
		}
		return results, errs
	}
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for i, sc := range scenarios {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, sc *model.Scenario) {
			defer wg.Done()
			defer func() { <-sem }()
			results[i], errs[i] = Run(sc)
		}(i, sc)
	}
	wg.Wait()
	return results, errs
}
