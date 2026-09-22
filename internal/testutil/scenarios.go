// Package testutil builds deterministic scenario fixtures for tests.
package testutil

import "github.com/lisama569/epidemic-simulation-go/model"

// GroupedSEIR returns a valid 2-group grouped SEIR scenario with the given
// seed. All numeric inputs are logged by the tests that use it.
func GroupedSEIR(seed int64) *model.Scenario {
	return &model.Scenario{
		SchemaVersion: 2,
		Paradigm:      model.ParadigmGrouped,
		ModelType:     "seir",
		HorizonDays:   80,
		StepDays:      0.5,
		Seed:          seed,
		Beta:          0.35,
		Sigma:         0.2,
		Gamma:         0.1,
		GammaQ:        0.15,
		Groups: []model.Group{
			{ID: "g0", Size: 8000, InitS: 7990, InitI: 10},
			{ID: "g1", Size: 2000, InitS: 1996, InitI: 4},
		},
		Contacts: model.ContactMatrix{{0.9, 0.2}, {0.2, 0.4}},
	}
}

// WithVaccineCapacity returns sc plus a capacity-limited vaccination
// intervention targeting group g.
func WithVaccineCapacity(sc *model.Scenario, start, end, rate, capacity float64, groups ...int) *model.Scenario {
	sc.Interventions = append(sc.Interventions, model.Intervention{
		Kind: model.InterventionVaccination, StartDay: start, EndDay: end,
		Compliance: 1, Rate: rate, Capacity: capacity, Groups: groups,
	})
	return sc
}

// WithIsolationCapacity returns sc plus a capacity-limited isolation
// intervention.
func WithIsolationCapacity(sc *model.Scenario, start, end, rate, capacity float64) *model.Scenario {
	sc.Interventions = append(sc.Interventions, model.Intervention{
		Kind: model.InterventionIsolation, StartDay: start, EndDay: end,
		Compliance: 1, Rate: rate, Capacity: capacity,
	})
	return sc
}

// WithContactReduction returns sc plus a staged contact reduction.
func WithContactReduction(sc *model.Scenario, start, end, strength float64) *model.Scenario {
	sc.Interventions = append(sc.Interventions, model.Intervention{
		Kind: model.InterventionContactReduction, StartDay: start, EndDay: end,
		Compliance: 1, Strength: strength,
	})
	return sc
}

// MetapopSIR returns a valid 3-patch metapopulation SIR scenario.
func MetapopSIR(seed int64) *model.Scenario {
	return &model.Scenario{
		SchemaVersion: 2,
		Paradigm:      model.ParadigmMetapop,
		ModelType:     "sir",
		HorizonDays:   40,
		StepDays:      1,
		Seed:          seed,
		Beta:          0.4,
		Gamma:         0.1,
		GammaQ:        0.1,
		Groups: []model.Group{
			{ID: "city", Size: 10000, InitS: 9980, InitI: 20},
			{ID: "town", Size: 3000, InitS: 3000},
			{ID: "rural", Size: 1000, InitS: 1000},
		},
		Migration: model.MigrationMatrix{
			{0, 0.01, 0.005},
			{0.02, 0, 0.01},
			{0.005, 0.01, 0},
		},
	}
}
