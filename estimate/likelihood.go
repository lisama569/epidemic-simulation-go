package estimate

import (
	"math"

	"github.com/lisama569/epidemic-simulation-go/engine"
	"github.com/lisama569/epidemic-simulation-go/model"
	"github.com/lisama569/epidemic-simulation-go/synthetic"
)

// alignTol is the tolerance for snapping observation days to the step grid.
const alignTol = 1e-6

// checkCompatibility verifies that the observation series can be evaluated
// under the scenario's modeling convention.
func checkCompatibility(sc *model.Scenario, obs *synthetic.Sequence) error {
	if obs == nil || len(obs.Records) == 0 {
		return model.Errorf(model.ReasonBadHorizon, "observation sequence is empty")
	}
	if obs.Paradigm != sc.Paradigm {
		return model.Errorf(model.ReasonUnknownParadigm,
			"observations use paradigm %q but scenario uses %q", obs.Paradigm, sc.Paradigm)
	}
	if obs.ModelType != sc.ModelType {
		return model.Errorf(model.ReasonUnknownModelType,
			"observations use model_type %q but scenario uses %q", obs.ModelType, sc.ModelType)
	}
	for _, r := range obs.Records {
		if r.Group < -1 || r.Group >= len(sc.Groups) {
			return model.Errorf(model.ReasonGroupRefOutOfRange,
				"observation at day %g references group %d; scenario has %d groups",
				r.Day, r.Group, len(sc.Groups))
		}
		idx := r.Day/sc.StepDays - 1
		nearest := math.Round(idx)
		if math.Abs(idx-nearest) > alignTol {
			return model.Errorf(model.ReasonStepNotAligned,
				"observation day %g does not lie on the step grid (dt=%g)", r.Day, sc.StepDays)
		}
		if nearest < 0 || int(nearest) >= sc.NumSteps() {
			return model.Errorf(model.ReasonTimelineReversal,
				"observation day %g lies outside the simulated horizon %g", r.Day, sc.HorizonDays)
		}
	}
	return nil
}

// simulatedMean returns the model-predicted mean incidence for one
// observation, computed deterministically (no noise is added).
func simulatedMean(res *engine.Result, sc *model.Scenario, r synthetic.Observation) float64 {
	step := int(math.Round(r.Day/sc.StepDays)) - 1
	row := res.NewInfections[step]
	if r.Group >= 0 {
		return row[r.Group]
	}
	var sum float64
	for _, v := range row {
		sum += v
	}
	return sum
}

// nll computes the negative log Poisson likelihood of obs given beta.
// Deterministic incidence is treated as the Poisson mean; a small epsilon
// floor keeps the likelihood finite when the model predicts zero.
func nll(res *engine.Result, sc *model.Scenario, obs *synthetic.Sequence) float64 {
	const floor = 1e-9
	var total float64
	for _, r := range obs.Records {
		mean := simulatedMean(res, sc, r)
		if mean < floor {
			mean = floor
		}
		k := r.NewInfected
		// k log(mean) - mean - log(k!)
		total += mean - k*math.Log(mean) + logFactorial(k)
	}
	return total
}

func logFactorial(k float64) float64 {
	n := int(k + 0.5)
	if n < 2 {
		return 0
	}
	// lgamma(n+1) = log(n!)
	res, _ := math.Lgamma(float64(n + 1))
	return res
}

// evalAt runs the deterministic model at a trial beta and scores it.
func evalAt(base *model.Scenario, obs *synthetic.Sequence, beta float64) (float64, *engine.Result, error) {
	trial := *base
	trial.Beta = beta
	res, err := engine.Run(&trial)
	if err != nil {
		return math.Inf(1), nil, err
	}
	return nll(res, &trial, obs), res, nil
}
