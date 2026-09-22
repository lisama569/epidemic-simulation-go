package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
)

// Scenario is a normalized version-2 scenario configuration.
type Scenario struct {
	// SchemaVersion is the normalized configuration version (always 2).
	SchemaVersion int `json:"schema_version"`
	// Paradigm selects the modeling convention.
	Paradigm Paradigm `json:"paradigm"`
	// ModelType selects the disease-state subset: "sir", "seir", "sei".
	ModelType string `json:"model_type"`
	// HorizonDays is the simulation length in days; step 0 .. horizon.
	HorizonDays float64 `json:"horizon_days"`
	// StepDays is the fixed integration step (dt); it must divide the horizon
	// into an integer number of steps.
	StepDays float64 `json:"step_days"`
	// Seed is the deterministic RNG seed used for synthetic observations and
	// for any stochastic decision. Integration itself is deterministic.
	Seed int64 `json:"seed"`

	// Beta is the per-contact transmission probability per day.
	Beta float64 `json:"beta"`
	// Sigma is the E -> I rate per day (unused by SIR).
	Sigma float64 `json:"sigma"`
	// Gamma is the recovery rate per day (I -> R; also E -> R in SEIR).
	Gamma float64 `json:"gamma"`
	// GammaQ is the Q -> R rate per day.
	GammaQ float64 `json:"gamma_q"`

	Groups    []Group         `json:"groups"`
	Contacts  ContactMatrix   `json:"contacts,omitempty"`
	Migration MigrationMatrix `json:"migration,omitempty"`

	Interventions []Intervention `json:"interventions,omitempty"`
}

// LegacyScenarioV1 is the old on-disk configuration version ("version":"1").
type LegacyScenarioV1 struct {
	Version          string  `json:"version"`
	Days             float64 `json:"days"`
	PopSize          float64 `json:"pop_size"`
	InitI            float64 `json:"init_i"`
	R0               float64 `json:"r0"`
	Recovery         float64 `json:"recovery"`
	LockdownStart    float64 `json:"lockdown_start"`
	LockdownEnd      float64 `json:"lockdown_end"`
	LockdownStrength float64 `json:"lockdown_strength"`
}

// Normalize decodes a raw JSON document and migrates legacy versions to the
// canonical version-2 form. Normalization follows fixed rules and never
// silently repairs contradictory values: unknown versions or malformed JSON
// are rejected with a distinguished reason.
//
// Legacy v1 -> v2 rules (deterministic):
//
//	days               -> horizon_days
//	step_days          -> 1.0 (fixed daily legacy cadence)
//	seed               -> 1 (legacy carried no seed)
//	single group "all" -> size=pop_size, init_s=pop_size-init_i, init_i=init_i
//	contacts           -> [[1]] (legacy well-mixed single group)
//	beta               -> r0 * recovery
//	gamma, gamma_q     -> recovery; model_type "sir"; paradigm "grouped"
//	lockdown_*         -> one contact_reduction intervention, compliance 1
func Normalize(raw []byte) (*Scenario, error) {
	var head struct {
		SchemaVersion *json.Number `json:"schema_version"`
		Version       string       `json:"version"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&head); err != nil {
		return nil, validationError(ReasonMalformedJSON, "cannot parse scenario: %v", err)
	}

	switch {
	case head.SchemaVersion != nil:
		v, err := head.SchemaVersion.Int64()
		if err != nil {
			return nil, validationError(ReasonMalformedJSON, "schema_version is not an integer: %v", err)
		}
		switch int(v) {
		case 2:
			var sc Scenario
			if err := json.Unmarshal(raw, &sc); err != nil {
				return nil, validationError(ReasonMalformedJSON, "cannot decode v2 scenario: %v", err)
			}
			sc.SchemaVersion = 2
			return &sc, nil
		case 1:
			return normalizeV1(raw)
		default:
			return nil, validationError(ReasonUnsupportedVersion, "schema_version %d is not supported", v)
		}
	case head.Version == "1":
		return normalizeV1(raw)
	case head.Version != "":
		return nil, validationError(ReasonUnsupportedVersion, "legacy %q is not supported", head.Version)
	default:
		return nil, validationError(ReasonUnsupportedVersion, "missing schema_version/version marker")
	}
}

func normalizeV1(raw []byte) (*Scenario, error) {
	var l LegacyScenarioV1
	if err := json.Unmarshal(raw, &l); err != nil {
		return nil, validationError(ReasonMalformedJSON, "cannot decode v1 scenario: %v", err)
	}
	sc := &Scenario{
		SchemaVersion: 2,
		Paradigm:      ParadigmGrouped,
		ModelType:     "sir",
		HorizonDays:   l.Days,
		StepDays:      1.0,
		Seed:          1,
		Beta:          l.R0 * l.Recovery,
		Gamma:         l.Recovery,
		GammaQ:        l.Recovery,
		Groups: []Group{{
			ID:    "all",
			Size:  l.PopSize,
			InitS: l.PopSize - l.InitI,
			InitI: l.InitI,
		}},
		Contacts: ContactMatrix{{1}},
	}
	if l.LockdownStart > 0 || l.LockdownEnd > 0 || l.LockdownStrength > 0 {
		sc.Interventions = []Intervention{{
			Kind:       InterventionContactReduction,
			StartDay:   l.LockdownStart,
			EndDay:     l.LockdownEnd,
			Compliance: 1,
			Strength:   l.LockdownStrength,
		}}
	}
	return sc, nil
}

const (
	closureTol = 1e-6
	alignTol   = 1e-6
)

// Validate checks every self-consistency constraint. Contradictory inputs
// return *ValidationError with a stable Reason; no default is substituted.
func (sc *Scenario) Validate() error {
	switch sc.Paradigm {
	case ParadigmGrouped, ParadigmMetapop:
	default:
		return validationError(ReasonUnknownParadigm, "paradigm %q is not supported", sc.Paradigm)
	}
	switch sc.ModelType {
	case "sir", "seir", "sei":
	default:
		return validationError(ReasonUnknownModelType, "model_type %q is not supported", sc.ModelType)
	}
	if err := checkFinite("beta", sc.Beta); err != nil {
		return err
	}
	if sc.Beta < 0 {
		return validationError(ReasonNegativeValue, "beta must be >= 0, got %g", sc.Beta)
	}
	for name, v := range map[string]float64{"sigma": sc.Sigma, "gamma": sc.Gamma, "gamma_q": sc.GammaQ,
		"horizon_days": sc.HorizonDays, "step_days": sc.StepDays} {
		if err := checkFinite(name, v); err != nil {
			return err
		}
	}
	if sc.Sigma < 0 || sc.Gamma < 0 || sc.GammaQ < 0 {
		return validationError(ReasonNegativeValue, "rates sigma/gamma/gamma_q must be >= 0")
	}
	if sc.ModelType == "sei" && (sc.Gamma != 0 || sc.GammaQ != 0) {
		return validationError(ReasonBadRate,
			"model_type %q closes the recovery path, so gamma and gamma_q must be 0, got %g/%g",
			sc.ModelType, sc.Gamma, sc.GammaQ)
	}
	if sc.StepDays <= 0 {
		return validationError(ReasonNonPositiveStep, "step_days must be > 0, got %g", sc.StepDays)
	}
	if sc.HorizonDays <= 0 {
		return validationError(ReasonBadHorizon, "horizon_days must be > 0, got %g", sc.HorizonDays)
	}
	n := math.Round(sc.HorizonDays / sc.StepDays)
	if math.Abs(n*sc.StepDays-sc.HorizonDays) > alignTol {
		return validationError(ReasonStepNotAligned,
			"horizon_days %g is not an integer multiple of step_days %g", sc.HorizonDays, sc.StepDays)
	}

	if len(sc.Groups) == 0 {
		return validationError(ReasonEmptyGroups, "at least one group is required")
	}
	ids := map[string]bool{}
	for i, g := range sc.Groups {
		if g.ID == "" {
			return validationError(ReasonEmptyGroups, "group %d has empty id", i)
		}
		if ids[g.ID] {
			return validationError(ReasonDuplicateGroupID, "group id %q is duplicated", g.ID)
		}
		ids[g.ID] = true
		if !isFinite(g.Size) || g.Size <= 0 {
			return validationError(ReasonNegativeValue, "group %q size must be > 0, got %g", g.ID, g.Size)
		}
		init := []float64{g.InitS, g.InitE, g.InitI, g.InitQ, g.InitR}
		var sum float64
		for c, v := range init {
			if !isFinite(v) || v < 0 {
				return validationError(ReasonNonFinite, "group %q initial %s = %g is invalid",
					g.ID, Compartment(c).String(), v)
			}
			sum += v
		}
		if math.Abs(sum-g.Size) > closureTol*math.Max(1, g.Size) {
			return validationError(ReasonGroupNotClosed,
				"group %q initial compartments sum to %.9g but size is %.9g (gap %.3g)",
				g.ID, sum, g.Size, sum-g.Size)
		}
	}

	switch sc.Paradigm {
	case ParadigmGrouped:
		if sc.Contacts == nil {
			return validationError(ReasonMissingRelation, "grouped paradigm requires a contact matrix")
		}
		if err := ValidateContacts(sc.Contacts, len(sc.Groups)); err != nil {
			return err
		}
	case ParadigmMetapop:
		if sc.Migration == nil {
			return validationError(ReasonMissingRelation, "metapop paradigm requires a migration matrix")
		}
		if err := ValidateMigration(sc.Migration, len(sc.Groups)); err != nil {
			return err
		}
		ng := len(sc.Groups)
		for src := 0; src < ng; src++ {
			var outflow float64
			for dst := 0; dst < ng; dst++ {
				if dst != src {
					outflow += sc.Migration[dst][src]
				}
			}
			if outflow*sc.StepDays > 1+1e-9 {
				return validationError(ReasonMigrationOutflow,
					"migration outflows from patch %d (%.6f/day) times step_days %.4g exceed 1",
					src, outflow, sc.StepDays)
			}
		}
	}

	if err := sc.validateInterventions(); err != nil {
		return err
	}
	return nil
}

func (sc *Scenario) validateInterventions() error {
	n := len(sc.Groups)
	for i, iv := range sc.Interventions {
		switch iv.Kind {
		case InterventionContactReduction, InterventionVaccination, InterventionIsolation:
		default:
			return validationError(ReasonUnknownIntervention, "intervention %d has unknown kind %q", i, iv.Kind)
		}
		if !isFinite(iv.StartDay) || !isFinite(iv.EndDay) {
			return validationError(ReasonNonFinite, "intervention %d has non-finite time bounds", i)
		}
		if iv.StartDay < 0 || iv.EndDay > sc.HorizonDays {
			return validationError(ReasonTimelineReversal,
				"intervention %d [%s] interval [%.4f,%.4f] lies outside timeline [0,%.4f]",
				i, iv.Kind, iv.StartDay, iv.EndDay, sc.HorizonDays)
		}
		if iv.StartDay >= iv.EndDay {
			return validationError(ReasonBadTimeOrder,
				"intervention %d [%s] starts (%.4f) before/at its end (%.4f)",
				i, iv.Kind, iv.StartDay, iv.EndDay)
		}
		if !isFinite(iv.Compliance) || iv.Compliance < 0 || iv.Compliance > 1 {
			return validationError(ReasonBadCompliance,
				"intervention %d compliance must be in [0,1], got %g", i, iv.Compliance)
		}
		if iv.Kind == InterventionContactReduction {
			if !isFinite(iv.Strength) || iv.Strength < 0 || iv.Strength > 1 {
				return validationError(ReasonBadStrength,
					"intervention %d strength must be in [0,1], got %g", i, iv.Strength)
			}
		}
		if iv.Kind == InterventionVaccination || iv.Kind == InterventionIsolation {
			if !isFinite(iv.Rate) || iv.Rate < 0 {
				return validationError(ReasonBadRate,
					"intervention %d rate must be >= 0, got %g", i, iv.Rate)
			}
			if !isFinite(iv.Capacity) || iv.Capacity < 0 {
				return validationError(ReasonBadCapacity,
					"intervention %d capacity must be >= 0, got %g", i, iv.Capacity)
			}
		}
		for _, g := range iv.Groups {
			if g < 0 || g >= n {
				return validationError(ReasonGroupRefOutOfRange,
					"intervention %d references group index %d but only %d groups exist", i, g, n)
			}
		}
		// Same-kind interventions may not overlap: stacking two reductions or
		// two capacity-limited programs on the same timeline is ambiguous.
		for j := 0; j < i; j++ {
			other := sc.Interventions[j]
			if other.Kind != iv.Kind {
				continue
			}
			if iv.StartDay < other.EndDay && other.StartDay < iv.EndDay {
				return validationError(ReasonInterventionOverlap,
					"interventions %d and %d of kind %s overlap: [%.4f,%.4f) vs [%.4f,%.4f)",
					j, i, iv.Kind, other.StartDay, other.EndDay, iv.StartDay, iv.EndDay)
			}
		}
	}
	return nil
}

func checkFinite(name string, v float64) error {
	if !isFinite(v) {
		return validationError(ReasonNonFinite, "%s must be finite, got %g", name, v)
	}
	return nil
}

// NumSteps returns the number of integration steps.
func (sc *Scenario) NumSteps() int {
	return int(math.Round(sc.HorizonDays / sc.StepDays))
}

// InitialState builds the starting state from the group definitions.
func (sc *Scenario) InitialState() *State {
	st := NewState(len(sc.Groups), 0)
	for g, gr := range sc.Groups {
		st.Pop[g][Susceptible] = gr.InitS
		st.Pop[g][Exposed] = gr.InitE
		st.Pop[g][Infected] = gr.InitI
		st.Pop[g][Quarantined] = gr.InitQ
		st.Pop[g][Recovered] = gr.InitR
	}
	return st
}

// String renders a compact deterministic summary for logging.
func (sc *Scenario) String() string {
	return fmt.Sprintf("Scenario(v=%d, paradigm=%s, type=%s, horizon=%g, dt=%g, seed=%d, beta=%g, groups=%d, interventions=%d)",
		sc.SchemaVersion, sc.Paradigm, sc.ModelType, sc.HorizonDays, sc.StepDays,
		sc.Seed, sc.Beta, len(sc.Groups), len(sc.Interventions))
}
