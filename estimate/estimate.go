// Package estimate fits model parameters to synthetic observations.
//
// Estimation is deliberately one-dimensional (beta) so the convergence
// criterion is fully explainable:
//
//   - Objective: negative Poisson log-likelihood of observed incidence given
//     deterministic model incidence.
//   - Search:    bounded golden-section minimization on [BetaLow, BetaHigh].
//   - Converged: the bracket width is <= Tolerance within MaxIterations and
//     the optimum lies strictly inside the feasible bracket.
//   - 95% CI:    profile-likelihood interval where NLL <= NLL(betaHat)+1.9207,
//     bisected from betaHat toward both bracket edges. A bound that cannot be
//     found inside the feasible region is reported as clipped, and the whole
//     result is flagged OutOfFeasibleDomain rather than dressed up as a
//     finite interval.
//
// Non-convergence, observation/model incompatibility and out-of-domain
// outcomes always come back as explicit statuses with an explanatory Why;
// no apparently-valid estimate is ever returned in those cases.
package estimate

import (
	"errors"
	"fmt"
	"math"

	"github.com/lisama569/epidemic-simulation-go/checkpoint"
	"github.com/lisama569/epidemic-simulation-go/engine"
	"github.com/lisama569/epidemic-simulation-go/model"
	"github.com/lisama569/epidemic-simulation-go/synthetic"
)

// ConvergenceStatus reports why an estimation did or did not converge.
type ConvergenceStatus string

const (
	Converged           ConvergenceStatus = "converged"
	NotConvergedMaxIter ConvergenceStatus = "not_converged_max_iterations"
	NotConvergedFlat    ConvergenceStatus = "not_converged_flat_likelihood"
	IncompatibleObs     ConvergenceStatus = "incompatible_observations"
	OutOfFeasibleDomain ConvergenceStatus = "out_of_feasible_domain"
)

// Result is the estimation outcome, including uncertainty semantics.
type Result struct {
	Status      ConvergenceStatus `json:"status"`
	BetaHat     float64           `json:"beta_hat"`
	CI95Lower   float64           `json:"ci95_lower"`
	CI95Upper   float64           `json:"ci95_upper"`
	CIClipped   bool              `json:"ci_clipped"`
	Iterations  int               `json:"iterations"`
	FinalNLL    float64           `json:"final_nll"`
	Tolerance   float64           `json:"tolerance"`
	BracketLow  float64           `json:"bracket_low"`
	BracketHigh float64           `json:"bracket_high"`
	Resumed     bool              `json:"resumed_from_checkpoint"`
	Why         string            `json:"why"`
	Sim         *engine.Result    `json:"-"`
}

// Options configures the estimation and resumable continuation.
type Options struct {
	BetaLow       float64
	BetaHigh      float64
	Tolerance     float64
	MaxIterations int
	// CheckpointPath, when set, enables resume-from-checkpoint continuation.
	CheckpointPath string
}

func (o Options) withDefaults() Options {
	if o.BetaHigh <= o.BetaLow {
		o.BetaLow, o.BetaHigh = 0, 10
	}
	if o.Tolerance <= 0 {
		o.Tolerance = 1e-4
	}
	if o.MaxIterations <= 0 {
		o.MaxIterations = 100
	}
	return o
}

// Estimate performs a bounded 1-D profile (golden-section) likelihood
// estimation of beta. It never substitutes defaults silently for bad input
// (brackets/tolerances are validated); non-convergence and incompatibility
// are reported via Result.Status.
func Estimate(sc *model.Scenario, obs *synthetic.Sequence, opts Options) (*Result, error) {
	if err := sc.Validate(); err != nil {
		return nil, err
	}
	if err := checkCompatibility(sc, obs); err != nil {
		return &Result{Status: IncompatibleObs, Why: err.Error()}, nil
	}
	o := opts.withDefaults()
	if math.IsNaN(o.BetaLow) || math.IsNaN(o.BetaHigh) || o.BetaLow < 0 || o.BetaHigh <= o.BetaLow {
		return nil, model.Errorf(model.ReasonBadRate,
			"beta bracket must satisfy 0 <= low < high, got [%g,%g]", opts.BetaLow, opts.BetaHigh)
	}

	var store cpStore
	if o.CheckpointPath != "" {
		store = fileStore{path: o.CheckpointPath}
	}
	cache := map[float64]float64{}
	var state *optimizerState
	resumed := false
	if store != nil {
		if cp, err := store.load(); err == nil {
			if cp.BetaLow == o.BetaLow && cp.BetaHigh == o.BetaHigh {
				state = &cp.State
				resumed = true
				for _, e := range cp.Cache {
					cache[e.X] = e.F
				}
			} else if !errors.Is(err, checkpoint.ErrNoCheckpoint) {
				// Bracket mismatch means the checkpoint describes a different
				// problem; refuse rather than mix two searches.
				return nil, model.Errorf(model.ReasonBadRate,
					"checkpoint bracket [%g,%g] does not match requested [%g,%g]",
					cp.BetaLow, cp.BetaHigh, o.BetaLow, o.BetaHigh)
			}
		} else if !errors.Is(err, checkpoint.ErrNoCheckpoint) {
			return nil, err
		}
	}

	ef := evalAtNLL(sc, obs)
	var saver func(*optimizerState) error
	if store != nil {
		saver = func(st *optimizerState) error {
			cp := &estimateCheckpoint{State: *st, BetaLow: o.BetaLow, BetaHigh: o.BetaHigh}
			for x, fv := range cache {
				cp.Cache = append(cp.Cache, cacheEntry{X: x, F: fv})
			}
			return store.save(cp)
		}
	}

	final, bestRes, err := runGoldenSection(o.BetaLow, o.BetaHigh, o.Tolerance, o.MaxIterations, ef, state, cache, saver)
	if err != nil {
		return nil, err
	}

	res := &Result{
		Tolerance:   o.Tolerance,
		BracketLow:  o.BetaLow,
		BracketHigh: o.BetaHigh,
		Iterations:  final.Iterations,
		FinalNLL:    final.BestF,
		BetaHat:     final.BestX,
		Resumed:     resumed,
	}

	// Re-run at betaHat if it came from the cache (no result attached).
	if bestRes == nil {
		if _, bestRes, err = evalAt(sc, obs, final.BestX); err != nil {
			return nil, err
		}
	}
	res.Sim = bestRes

	// Convergence judgement.
	edgeTol := o.Tolerance
	atEdge := final.BestX <= o.BetaLow+edgeTol || final.BestX >= o.BetaHigh-edgeTol
	switch {
	case !final.Done && final.FlatStreak >= 3:
		res.Status = NotConvergedFlat
		res.Why = fmt.Sprintf("likelihood improved by less than tolerance for %d consecutive iterations at beta=%.6g",
			final.FlatStreak, final.BestX)
		return res, nil
	case !final.Done:
		res.Status = NotConvergedMaxIter
		res.Why = fmt.Sprintf("bracket width %.6g still exceeds tolerance %.6g after %d iterations",
			final.High-final.Low, o.Tolerance, final.Iterations)
		return res, nil
	case atEdge:
		res.Status = OutOfFeasibleDomain
		res.Why = fmt.Sprintf("optimum beta=%.6g lies on the feasible bracket edge [%.6g,%.6g]; widen the search range",
			final.BestX, o.BetaLow, o.BetaHigh)
		return res, nil
	}

	// 95% profile-likelihood interval.
	target := final.BestF + ciThresholdDelta
	lo := findBound(o.BetaLow, final.BestX, target, o.Tolerance, o.MaxIterations, ef, cache)
	up := findBound(final.BestX, o.BetaHigh, target, o.Tolerance, o.MaxIterations, ef, cache)
	res.CI95Lower, res.CI95Upper = lo.bound, up.bound
	res.CIClipped = lo.clipped || up.clipped
	if lo.reason != "" || up.reason != "" {
		res.CIClipped = true
	}
	if res.CIClipped {
		res.Status = OutOfFeasibleDomain
		res.Why = fmt.Sprintf("point estimate converged at %.6g but the 95%% interval cannot be closed inside [%g,%g] (lower:%s upper:%s)",
			final.BestX, o.BetaLow, o.BetaHigh, clipNote(lo), clipNote(up))
		if store != nil {
			_ = store.clear()
		}
		return res, nil
	}
	if store != nil {
		if err := store.clear(); err != nil {
			return nil, err
		}
	}
	res.Status = Converged
	res.Why = fmt.Sprintf("bracket width %.3g <= tolerance %.3g after %d iterations; NLL=%.6g; 95%% profile interval [%.6g,%.6g]",
		final.High-final.Low, o.Tolerance, final.Iterations, final.BestF, res.CI95Lower, res.CI95Upper)
	return res, nil
}

func clipNote(r ciResult) string {
	if r.reason == "" {
		return "closed"
	}
	return r.reason
}
