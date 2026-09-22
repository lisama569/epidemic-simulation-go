package engine

import "github.com/lisama569/epidemic-simulation-go/model"

// RunOptions controls a single run.
type RunOptions struct {
	// MaxTrajectory, if > 0, caps retained trajectory samples (the oldest
	// samples are dropped and the terminal state is always kept) so memory
	// stays bounded for long horizons. Incidence accounting and capacity
	// events are never dropped.
	MaxTrajectory int
}

// RunWithOptions executes a scenario with the given run options.
func RunWithOptions(sc *model.Scenario, opts RunOptions) (*Result, error) {
	return run(sc, opts.MaxTrajectory, nil)
}
