package model_test

import (
	"testing"

	"github.com/lisama569/epidemic-simulation-go/internal/testutil"
	"github.com/lisama569/epidemic-simulation-go/model"
)

// assertReason validates sc and asserts the failure reason is want.
func assertReason(t *testing.T, sc *model.Scenario, want model.Reason) {
	t.Helper()
	err := sc.Validate()
	if err == nil {
		t.Fatalf("expected validation error %q, got nil; input=%s", want, sc.String())
	}
	ve, ok := model.AsValidationError(err)
	if !ok {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	t.Logf("input=%s -> rejected reason=%q message=%q", sc.String(), ve.Reason, ve.Message)
	if ve.Reason != want {
		t.Fatalf("reason = %q, want %q (%s)", ve.Reason, want, ve.Message)
	}
}

func TestValidBaseline(t *testing.T) {
	sc := testutil.GroupedSEIR(1)
	t.Logf("key input: %s; seed=1", sc.String())
	if err := sc.Validate(); err != nil {
		t.Fatalf("baseline scenario must be valid: %v", err)
	}
	if err := testutil.MetapopSIR(1).Validate(); err != nil {
		t.Fatalf("metapop baseline must be valid: %v", err)
	}
}

func TestRejectGroupNotClosed(t *testing.T) {
	sc := testutil.GroupedSEIR(1)
	sc.Groups[0].InitS -= 50 // compartments no longer sum to size
	assertReason(t, sc, model.ReasonGroupNotClosed)
}

func TestRejectDuplicateGroupID(t *testing.T) {
	sc := testutil.GroupedSEIR(1)
	sc.Groups[1].ID = sc.Groups[0].ID
	assertReason(t, sc, model.ReasonDuplicateGroupID)
}

func TestRejectUnknownParadigmAndType(t *testing.T) {
	sc := testutil.GroupedSEIR(1)
	sc.Paradigm = "agent_based"
	assertReason(t, sc, model.ReasonUnknownParadigm)

	sc = testutil.GroupedSEIR(1)
	sc.ModelType = "svir"
	assertReason(t, sc, model.ReasonUnknownModelType)
}

func TestRejectTimelineReversal(t *testing.T) {
	sc := testutil.GroupedSEIR(1)
	sc.HorizonDays = 10
	sc.StepDays = 1
	sc.Interventions = []model.Intervention{{
		Kind: model.InterventionContactReduction, StartDay: 8, EndDay: 12,
		Compliance: 1, Strength: 0.5,
	}}
	assertReason(t, sc, model.ReasonTimelineReversal)

	sc = testutil.GroupedSEIR(1)
	sc.Interventions = []model.Intervention{{
		Kind: model.InterventionContactReduction, StartDay: 10, EndDay: 5,
		Compliance: 1, Strength: 0.5,
	}}
	assertReason(t, sc, model.ReasonBadTimeOrder)
}

func TestRejectOverlappingSameKindInterventions(t *testing.T) {
	sc := testutil.GroupedSEIR(1)
	sc.Interventions = []model.Intervention{
		{Kind: model.InterventionContactReduction, StartDay: 5, EndDay: 15, Compliance: 1, Strength: .5},
		{Kind: model.InterventionContactReduction, StartDay: 10, EndDay: 20, Compliance: 1, Strength: .5},
	}
	assertReason(t, sc, model.ReasonInterventionOverlap)
}

func TestDifferentKindsMayOverlap(t *testing.T) {
	sc := testutil.GroupedSEIR(1)
	sc = testutil.WithContactReduction(sc, 5, 20, .5)
	sc = testutil.WithVaccineCapacity(sc, 5, 20, .05, 100, 0)
	if err := sc.Validate(); err != nil {
		t.Fatalf("different-kind overlapping interventions must be allowed: %v", err)
	}
}

func TestRejectBadComplianceAndCapacity(t *testing.T) {
	sc := testutil.GroupedSEIR(1)
	sc.Interventions = []model.Intervention{{
		Kind: model.InterventionVaccination, StartDay: 1, EndDay: 10,
		Compliance: 1.5, Rate: .1, Capacity: 10,
	}}
	assertReason(t, sc, model.ReasonBadCompliance)

	sc = testutil.GroupedSEIR(1)
	sc.Interventions = []model.Intervention{{
		Kind: model.InterventionVaccination, StartDay: 1, EndDay: 10,
		Compliance: 1, Rate: .1, Capacity: -3,
	}}
	assertReason(t, sc, model.ReasonBadCapacity)
}

func TestRejectMigrationOutflowAndStep(t *testing.T) {
	sc := testutil.MetapopSIR(1)
	// Column 0 outflow 0.02+0.005 = 0.025/day: valid at dt=1, invalid at
	// dt=50 (>1 over a step). Horizon kept a multiple of the step.
	sc.HorizonDays = 100
	sc.StepDays = 50
	assertReason(t, sc, model.ReasonMigrationOutflow)

	sc = testutil.MetapopSIR(1)
	sc.Migration[1][0] = 0.9
	sc.Migration[2][0] = 0.2 // column outflow 1.1/day > 1 even at dt=1
	assertReason(t, sc, model.ReasonMigrationOutflow)
}

func TestRejectStepNotAligned(t *testing.T) {
	sc := testutil.GroupedSEIR(1)
	sc.HorizonDays = 10.3
	sc.StepDays = 0.5
	assertReason(t, sc, model.ReasonStepNotAligned)
}

func TestRejectGroupRefOutOfRange(t *testing.T) {
	sc := testutil.GroupedSEIR(1)
	sc = testutil.WithVaccineCapacity(sc, 1, 10, .1, 100, 7)
	assertReason(t, sc, model.ReasonGroupRefOutOfRange)
}

func TestRejectMissingRelationMatrix(t *testing.T) {
	sc := testutil.GroupedSEIR(1)
	sc.Contacts = nil
	assertReason(t, sc, model.ReasonMissingRelation)
}

func TestReasonsAreDistinguishable(t *testing.T) {
	// Guard against accidental code collisions that would merge categories.
	seen := map[model.Reason]bool{}
	for _, r := range []model.Reason{
		model.ReasonGroupNotClosed, model.ReasonMigrationOutflow,
		model.ReasonTimelineReversal, model.ReasonInterventionOverlap,
		model.ReasonBadTimeOrder, model.ReasonNonFinite, model.ReasonNegativeValue,
	} {
		if seen[r] {
			t.Fatalf("duplicate reason %q", r)
		}
		seen[r] = true
	}
}

func TestRejectSEIWithRecoveryRate(t *testing.T) {
	sc := testutil.GroupedSEIR(1)
	sc.ModelType = "sei"
	// gamma/gamma_q must be exactly 0 for the closed-recovery variant.
	assertReason(t, sc, model.ReasonBadRate)
	sc.Gamma, sc.GammaQ = 0, 0
	if err := sc.Validate(); err != nil {
		t.Fatalf("sei with zero recovery rates must validate: %v", err)
	}
}
