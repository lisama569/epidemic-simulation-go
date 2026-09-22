package model

// StepOutcome reports what happened during one deterministic step.
type StepOutcome struct {
	// NewInfections[g] is the number of new infections in group g.
	NewInfections []float64
	// CapacityEvents explains every capacity-capped application in the step.
	CapacityEvents []CapacityEvent
}

// Dynamics is one modeling convention able to advance the state one step.
type Dynamics interface {
	Paradigm() Paradigm
	// Step advances st from st.T to st.T+dt under the active interventions.
	// st is mutated in place with a fixed operation ordering.
	Step(st *State, dt float64, active []Intervention, stepIndex int) (*StepOutcome, error)
}

// NewDynamics constructs the dynamics implementation for a validated scenario.
func NewDynamics(sc *Scenario) (Dynamics, error) {
	return newDynamics(sc)
}
