package model

import "fmt"

// groupedDynamics implements the grouped compartment model.
//
// Fixed per-step operation ordering (all flows clamped to their source pool,
// so no compartment can ever become negative):
//
//  1. infection        S -> E (seir/sei) or S -> I (sir), force of infection
//     lambda_a = beta * sum_b Ceff[a][b] * I_b / N_b
//  2. progression      E -> I at rate sigma (seir/sei)
//  3. isolation        I -> Q then E -> Q, per-intervention daily capacity,
//     effective rate rate*compliance
//  4. recovery         I -> R at gamma, Q -> R at gamma_q (sei: none)
//  5. vaccination      S -> R, per-intervention daily capacity
//  6. migration        (metapop only, see metapop.go)
//
// Contact reduction multiplies the ROWS of the contact matrix belonging to
// targeted groups by (1 - compliance*strength).
type groupedDynamics struct {
	sc    *Scenario
	baseC ContactMatrix
}

func newDynamics(sc *Scenario) (Dynamics, error) {
	switch sc.Paradigm {
	case ParadigmGrouped:
		return &groupedDynamics{sc: sc, baseC: sc.Contacts}, nil
	case ParadigmMetapop:
		inner := &groupedDynamics{sc: sc, baseC: identityContacts(len(sc.Groups))}
		return &metapopDynamics{sc: sc, baseC: inner.baseC, inner: inner}, nil
	default:
		return nil, validationError(ReasonUnknownParadigm, "cannot build dynamics for %q", sc.Paradigm)
	}
}

func identityContacts(n int) ContactMatrix {
	c := make(ContactMatrix, n)
	for i := range c {
		c[i] = make([]float64, n)
		c[i][i] = 1
	}
	return c
}

func (d *groupedDynamics) Paradigm() Paradigm { return ParadigmGrouped }

func (d *groupedDynamics) effectiveContacts(active []Intervention) ContactMatrix {
	n := len(d.baseC)
	c := make(ContactMatrix, n)
	for i := range d.baseC {
		c[i] = append([]float64(nil), d.baseC[i]...)
	}
	for _, iv := range active {
		if iv.Kind != InterventionContactReduction {
			continue
		}
		mult := 1 - iv.Compliance*iv.Strength
		targets := targetedGroups(iv, n)
		for a := 0; a < n; a++ {
			if targets[a] {
				for b := 0; b < n; b++ {
					c[a][b] *= mult
				}
			}
		}
	}
	return c
}

func targetedGroups(iv Intervention, n int) []bool {
	m := make([]bool, n)
	if len(iv.Groups) == 0 {
		for i := range m {
			m[i] = true
		}
		return m
	}
	for _, g := range iv.Groups {
		m[g] = true
	}
	return m
}

func (d *groupedDynamics) Step(st *State, dt float64, active []Intervention, stepIndex int) (*StepOutcome, error) {
	n := len(st.Pop)
	out := &StepOutcome{NewInfections: make([]float64, n)}
	ceff := d.effectiveContacts(active)
	day := st.T

	// Totals at step start.
	tot := make([]float64, n)
	for g := range st.Pop {
		tot[g] = st.GroupTotal(g)
		if tot[g] <= 0 {
			return nil, &CapacityError{Day: day, StepIndex: stepIndex, Group: g,
				Reason: ReasonGroupNotClosed, Message: "group population is zero or negative"}
		}
	}

	// 1. Infection.
	for a := 0; a < n; a++ {
		var lambda float64
		for b := 0; b < n; b++ {
			lambda += ceff[a][b] * st.Pop[b][Infected] / tot[b]
		}
		lambda *= d.sc.Beta
		s := st.Pop[a][Susceptible]
		want := s * lambda * dt
		got := clampFlow(s, want)
		st.Pop[a][Susceptible] -= got
		if d.sc.ModelType == "sir" {
			st.Pop[a][Infected] += got
		} else {
			st.Pop[a][Exposed] += got
		}
		out.NewInfections[a] = got
	}

	// 2. Progression E -> I.
	if d.sc.ModelType != "sir" {
		for g := 0; g < n; g++ {
			e := st.Pop[g][Exposed]
			got := clampFlow(e, d.sc.Sigma*e*dt)
			st.Pop[g][Exposed] -= got
			st.Pop[g][Infected] += got
		}
	}

	// 3. Isolation I -> Q, then E -> Q.
	for _, iv := range active {
		if iv.Kind != InterventionIsolation {
			continue
		}
		targets := targetedGroups(iv, n)
		for g := 0; g < n; g++ {
			if !targets[g] {
				continue
			}
			rate := iv.Rate * iv.Compliance
			iPool := st.Pop[g][Infected]
			wantI := rate * iPool * dt
			gotI, ev := applyCapacity(st.T, stepIndex, g, iv, iPool, wantI, dt)
			if ev != nil {
				out.CapacityEvents = append(out.CapacityEvents, *ev)
			}
			st.Pop[g][Infected] -= gotI
			st.Pop[g][Quarantined] += gotI
			if d.sc.ModelType != "sir" {
				ePool := st.Pop[g][Exposed]
				wantE := rate * ePool * dt
				gotE, ev2 := applyCapacity(st.T, stepIndex, g, iv, ePool, wantE, dt)
				if ev2 != nil {
					out.CapacityEvents = append(out.CapacityEvents, *ev2)
				}
				st.Pop[g][Exposed] -= gotE
				st.Pop[g][Quarantined] += gotE
			}
		}
	}

	// 4. Recovery.
	for g := 0; g < n; g++ {
		iPool := st.Pop[g][Infected]
		gotI := clampFlow(iPool, d.sc.Gamma*iPool*dt)
		st.Pop[g][Infected] -= gotI
		st.Pop[g][Recovered] += gotI

		qPool := st.Pop[g][Quarantined]
		gotQ := clampFlow(qPool, d.sc.GammaQ*qPool*dt)
		st.Pop[g][Quarantined] -= gotQ
		st.Pop[g][Recovered] += gotQ
		// For model_type "sei" the recovery path is closed: validation
		// requires gamma == gamma_q == 0, so both flows above are zero.
	}

	// 5. Vaccination S -> R.
	for _, iv := range active {
		if iv.Kind != InterventionVaccination {
			continue
		}
		targets := targetedGroups(iv, n)
		for g := 0; g < n; g++ {
			if !targets[g] {
				continue
			}
			sPool := st.Pop[g][Susceptible]
			want := iv.Rate * iv.Compliance * sPool * dt
			got, ev := applyCapacity(st.T, stepIndex, g, iv, sPool, want, dt)
			if ev != nil {
				out.CapacityEvents = append(out.CapacityEvents, *ev)
			}
			st.Pop[g][Susceptible] -= got
			st.Pop[g][Recovered] += got
		}
	}

	st.T += dt
	if !st.IsPhysical() {
		return nil, &CapacityError{Day: st.T, StepIndex: stepIndex, Reason: ReasonNonFinite,
			Message: fmt.Sprintf("state became non-physical after grouped step")}
	}
	return out, nil
}

// clampFlow caps a proposed flow at the available source pool.
func clampFlow(pool, want float64) float64 {
	if want < 0 || !isFinite(want) {
		return 0
	}
	if want > pool {
		return pool
	}
	return want
}

// applyCapacity limits a proposed per-group flow by the intervention's daily
// capacity (people/day). Capacity <= 0 means unlimited. A non-nil event
// explains the capping.
func applyCapacity(day float64, stepIndex, g int, iv Intervention, pool, want, dt float64) (float64, *CapacityEvent) {
	got := clampFlow(pool, want)
	if iv.Capacity <= 0 {
		return got, nil
	}
	capStep := iv.Capacity * dt
	if got <= capStep {
		return got, nil
	}
	ev := &CapacityEvent{
		Day:       day,
		StepIndex: stepIndex,
		Group:     g,
		Kind:      iv.Kind,
		Requested: got,
		Applied:   capStep,
		Capacity:  iv.Capacity,
		Reason: fmt.Sprintf("requested %.4f people but daily capacity %.4f allows %.4f per %.4g-day step",
			got, iv.Capacity, capStep, dt),
	}
	return capStep, ev
}
