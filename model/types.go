// Package model defines the modeling conventions, state layout and scenario
// configuration for the epidemic simulation engine.
//
// # State convention
//
// Every paradigm shares the fixed SEIQR compartment vector per group:
//
//	[S, E, I, Q, R]  (Susceptible, Exposed, Infected, Quarantined, Recovered)
//
// Compartments unused by a chosen model type stay exactly zero:
//
//   - "sir"  : E is unused; infection flows S -> I directly.
//   - "seir" : all of S,E,I,Q,R are usable (Q only via isolation).
//   - "sei"  : R is unreachable except via vaccination; exposed people
//     progress E -> I and the E -> R recovery path is closed.
//
// All flows are deterministic (fixed-step semi-implicit Euler with fixed
// operation ordering), so identical inputs and identical step sizes always
// yield bit-for-bit identical trajectories, regardless of parallelism.
package model

import "math"

// Compartment is a fixed SEIQR state slot.
type Compartment int

const (
	Susceptible Compartment = iota
	Exposed
	Infected
	Quarantined
	Recovered
	NumCompartments
)

// String returns the canonical compartment name.
func (c Compartment) String() string {
	switch c {
	case Susceptible:
		return "S"
	case Exposed:
		return "E"
	case Infected:
		return "I"
	case Quarantined:
		return "Q"
	case Recovered:
		return "R"
	default:
		return "?"
	}
}

// Paradigm identifies the modeling convention.
type Paradigm string

const (
	// ParadigmGrouped is the grouped (well-mixed-per-group) model driven by a
	// contact matrix.
	ParadigmGrouped Paradigm = "grouped"
	// ParadigmMetapop is the metapopulation model driven by a migration matrix.
	ParadigmMetapop Paradigm = "metapop"
)

// Group describes one population group (or metapopulation patch).
type Group struct {
	ID    string  `json:"id"`
	Size  float64 `json:"size"`
	InitS float64 `json:"init_s"`
	InitE float64 `json:"init_e"`
	InitI float64 `json:"init_i"`
	InitQ float64 `json:"init_q"`
	InitR float64 `json:"init_r"`
}

// State holds one deterministic snapshot of the population, indexed
// [group][compartment].
type State struct {
	T   float64     `json:"t"`
	Pop [][]float64 `json:"pop"`
}

// NewState allocates a zeroed state for n groups at time t.
func NewState(n int, t float64) *State {
	st := &State{T: t, Pop: make([][]float64, n)}
	for g := range st.Pop {
		st.Pop[g] = make([]float64, NumCompartments)
	}
	return st
}

// Clone returns an independent deep copy.
func (s *State) Clone() *State {
	c := NewState(len(s.Pop), s.T)
	for g := range s.Pop {
		copy(c.Pop[g], s.Pop[g])
	}
	return c
}

// GroupTotal returns the summed population of group g.
func (s *State) GroupTotal(g int) float64 {
	row := s.Pop[g]
	var sum float64
	for _, v := range row {
		sum += v
	}
	return sum
}

// Total returns the summed population across all groups.
func (s *State) Total() float64 {
	var sum float64
	for g := range s.Pop {
		sum += s.GroupTotal(g)
	}
	return sum
}

// IsPhysical reports whether the state contains no NaN/Inf or negative
// populations. -0 is treated as zero.
func (s *State) IsPhysical() bool {
	for g := range s.Pop {
		row := s.Pop[g]
		for c := range row {
			v := s.Pop[g][c]
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return false
			}
			if v < 0 {
				return false
			}
		}
	}
	return true
}
