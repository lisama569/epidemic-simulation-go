package model

import "fmt"

// metapopDynamics adds migration flows on top of the grouped local dynamics.
//
// Migration is applied AFTER all local epidemic/intervention flows, in a
// single synchronized exchange: for source patch src and compartment c the
// amount sent to dst is M[dst][src]*dt*X_src_c. Outflows are scaled down if
// they would exceed the source pool (validation guarantees this never
// triggers for valid inputs; it is the non-physical-state safety net).
// People from every compartment can migrate.
type metapopDynamics struct {
	sc    *Scenario
	baseC ContactMatrix
	inner *groupedDynamics
}

func (d *metapopDynamics) Paradigm() Paradigm { return ParadigmMetapop }

func (d *metapopDynamics) Step(st *State, dt float64, active []Intervention, stepIndex int) (*StepOutcome, error) {
	out, err := d.inner.Step(st, dt, active, stepIndex)
	if err != nil {
		return nil, err
	}
	if err := d.migrate(st, dt, stepIndex); err != nil {
		return nil, err
	}
	return out, nil
}

func (d *metapopDynamics) migrate(st *State, dt float64, stepIndex int) error {
	n := len(st.Pop)
	m := d.sc.Migration
	// delta[g][c] accumulates net changes; applied atomically after scanning.
	delta := make([][]float64, n)
	for i := range delta {
		delta[i] = make([]float64, NumCompartments)
	}
	for src := 0; src < n; src++ {
		for dst := 0; dst < n; dst++ {
			if dst == src {
				continue
			}
			rate := m[dst][src] * dt
			if rate <= 0 {
				continue
			}
			for c := 0; c < int(NumCompartments); c++ {
				pool := st.Pop[src][c]
				flow := rate * pool
				// Safety net: a single destination must not drain the pool.
				if flow > pool {
					flow = pool
				}
				delta[src][c] -= flow
				delta[dst][c] += flow
			}
		}
		// Safety net: total outflows per compartment cannot exceed the pool.
		for c := 0; c < int(NumCompartments); c++ {
			if -delta[src][c] > st.Pop[src][c] {
				return &CapacityError{
					Day: st.T, StepIndex: stepIndex, Group: src,
					Reason: ReasonMigrationOutflow,
					Message: fmt.Sprintf("migration would remove %.6f from compartment %s but only %.6f present",
						-delta[src][c], Compartment(c), st.Pop[src][c]),
				}
			}
		}
	}
	for g := 0; g < n; g++ {
		for c := 0; c < int(NumCompartments); c++ {
			st.Pop[g][c] += delta[g][c]
		}
	}
	if !st.IsPhysical() {
		return &CapacityError{Day: st.T, StepIndex: stepIndex, Reason: ReasonNonFinite,
			Message: "state became non-physical after migration"}
	}
	return nil
}
