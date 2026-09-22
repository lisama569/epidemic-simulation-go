package estimate

import (
	"math"

	"github.com/lisama569/epidemic-simulation-go/engine"
	"github.com/lisama569/epidemic-simulation-go/model"
	"github.com/lisama569/epidemic-simulation-go/synthetic"
)

var goldenRatio = (math.Sqrt(5) - 1) / 2 // ~0.618

// optimizerState is the complete resumable state of a golden-section search.
// Persisting it and resuming yields exactly the same sequence of trial points
// as an uninterrupted run.
type optimizerState struct {
	Low         float64 `json:"low"`
	High        float64 `json:"high"`
	X1          float64 `json:"x1"`
	F1          float64 `json:"f1"`
	X2          float64 `json:"x2"`
	F2          float64 `json:"f2"`
	Iterations  int     `json:"iterations"`
	BestX       float64 `json:"best_x"`
	BestF       float64 `json:"best_f"`
	FlatStreak  int     `json:"flat_streak"`
	Done        bool    `json:"done"`
	Initialized bool    `json:"initialized"`
}

// evalFn scores a trial beta deterministically.
type evalFn func(beta float64) (nll float64, res *engine.Result, err error)

// runGoldenSection minimizes f on [lo, hi] with a deterministic golden-section
// search. state may be nil (fresh start) or a previously persisted state
// (resume); cache contains NLL values already evaluated in prior segments.
// Each completed iteration is persisted via save if non-nil.
func runGoldenSection(lo, hi, tol float64, maxIter int, f evalFn,
	state *optimizerState, cache map[float64]float64, save func(*optimizerState) error) (*optimizerState, *engine.Result, error) {

	if state == nil {
		state = &optimizerState{Low: lo, High: hi, BestX: math.NaN(), BestF: math.Inf(1)}
		d := (hi - lo) * goldenRatio
		state.X1, state.X2 = hi-d, lo+d
	}
	var bestRes *engine.Result
	score := func(x float64) (float64, *engine.Result, error) {
		if v, ok := cache[x]; ok {
			return v, nil, nil
		}
		v, res, err := f(x)
		if err != nil {
			return math.Inf(1), nil, err
		}
		cache[x] = v
		return v, res, nil
	}
	track := func(x, v float64, res *engine.Result) {
		if v < state.BestF {
			state.BestF, state.BestX = v, x
			if res != nil {
				bestRes = res
			}
		}
	}

	if !state.Initialized {
		var res *engine.Result
		var err error
		if state.F1, res, err = score(state.X1); err != nil {
			return state, nil, err
		}
		track(state.X1, state.F1, res)
		if state.F2, res, err = score(state.X2); err != nil {
			return state, nil, err
		}
		track(state.X2, state.F2, res)
		state.Initialized = true
	}

	for state.Iterations < maxIter {
		// Convergence: interval narrower than tolerance.
		if state.High-state.Low <= tol {
			state.Done = true
			break
		}
		prevF := state.BestF
		if state.F2 < state.F1 {
			state.Low = state.X1
			state.X1, state.F1 = state.X2, state.F2
			state.X2 = state.Low + (state.High-state.Low)*goldenRatio
			v, res, err := score(state.X2)
			if err != nil {
				return state, nil, err
			}
			state.F2 = v
			track(state.X2, v, res)
		} else {
			state.High = state.X2
			state.X2, state.F2 = state.X1, state.F1
			state.X1 = state.High - (state.High-state.Low)*goldenRatio
			v, res, err := score(state.X1)
			if err != nil {
				return state, nil, err
			}
			state.F1 = v
			track(state.X1, v, res)
		}
		state.Iterations++
		if math.Abs(prevF-state.BestF) <= tol*math.Max(1, math.Abs(state.BestF)) {
			state.FlatStreak++
		} else {
			state.FlatStreak = 0
		}
		if save != nil {
			if err := save(state); err != nil {
				return state, nil, err
			}
		}
	}

	if state.High-state.Low <= tol {
		state.Done = true
	}
	return state, bestRes, nil
}

// evalAtNLL adapts evalAt to evalFn and keeps the simulation result of the
// best point for the final report.
func evalAtNLL(base *model.Scenario, obs *synthetic.Sequence) evalFn {
	return func(beta float64) (float64, *engine.Result, error) {
		return evalAt(base, obs, beta)
	}
}
