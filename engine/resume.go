package engine

import "github.com/lisama569/epidemic-simulation-go/model"

// resumePoint is an internal continuation point.
type resumePoint struct {
	stepIndex int
	state     *model.State
}

// RunFrom resumes an interrupted run from an explicit snapshot. The resumed
// trajectory is identical to an uninterrupted Run started from t=0 (the
// caller supplies the pre-snapshot prefix if it needs the full trajectory).
func RunFrom(sc *model.Scenario, stepIndex int, st *model.State) (*Result, error) {
	return run(sc, 0, &resumePoint{stepIndex: stepIndex, state: st})
}
