package model

import (
	"errors"
	"fmt"
)

// Reason is a stable, distinguishable machine-readable failure code. Callers
// can switch on it to tell apart every class of rejected input.
type Reason string

const (
	ReasonUnknown             Reason = "unknown"
	ReasonUnsupportedVersion  Reason = "unsupported_version"
	ReasonMalformedJSON       Reason = "malformed_json"
	ReasonUnknownParadigm     Reason = "unknown_paradigm"
	ReasonUnknownModelType    Reason = "unknown_model_type"
	ReasonNonFinite           Reason = "non_finite_value"
	ReasonNegativeValue       Reason = "negative_value"
	ReasonNonPositiveStep     Reason = "non_positive_step"
	ReasonBadHorizon          Reason = "bad_horizon"
	ReasonStepNotAligned      Reason = "step_not_aligned"
	ReasonEmptyGroups         Reason = "empty_groups"
	ReasonDuplicateGroupID    Reason = "duplicate_group_id"
	ReasonGroupNotClosed      Reason = "group_not_closed"
	ReasonContactShape        Reason = "contact_matrix_shape"
	ReasonMigrationShape      Reason = "migration_matrix_shape"
	ReasonMigrationOutflow    Reason = "migration_outflow_exceeds_one"
	ReasonMatrixNotSquare     Reason = "matrix_not_square"
	ReasonMissingRelation     Reason = "missing_relation_matrix"
	ReasonBadTimeOrder        Reason = "bad_time_order"
	ReasonTimelineReversal    Reason = "timeline_reversal"
	ReasonInterventionOverlap Reason = "intervention_interval_overlap"
	ReasonBadCompliance       Reason = "bad_compliance"
	ReasonBadStrength         Reason = "bad_strength"
	ReasonBadCapacity         Reason = "bad_capacity"
	ReasonBadRate             Reason = "bad_rate"
	ReasonUnknownIntervention Reason = "unknown_intervention_kind"
	ReasonGroupRefOutOfRange  Reason = "group_reference_out_of_range"
)

// ValidationError is the distinguished error returned for contradictory
// configuration inputs. It is never returned for runtime capacity limiting:
// capacity limits are reported via CapacityEvent; a genuine non-physical
// state is reported via CapacityError.
type ValidationError struct {
	Reason  Reason
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("configuration invalid [%s]: %s", e.Reason, e.Message)
}

// AsValidationError extracts a *ValidationError from err.
func AsValidationError(err error) (*ValidationError, bool) {
	var ve *ValidationError
	if errors.As(err, &ve) {
		return ve, true
	}
	return nil, false
}

func validationError(r Reason, format string, args ...any) error {
	return &ValidationError{Reason: r, Message: fmt.Sprintf(format, args...)}
}

// CapacityEvent records one capacity-limited application during a run. It is
// informational: the run continues with the capped (physical) amount.
type CapacityEvent struct {
	Day       float64          `json:"day"`
	StepIndex int              `json:"step_index"`
	Group     int              `json:"group"`
	Kind      InterventionKind `json:"kind"`
	Requested float64          `json:"requested"`
	Applied   float64          `json:"applied"`
	Capacity  float64          `json:"capacity"`
	Reason    string           `json:"reason"`
}

// CapacityError reports a non-physical state that could not be prevented,
// pinpointing the exact time step, group and cause.
type CapacityError struct {
	Day       float64
	StepIndex int
	Group     int
	Reason    Reason
	Message   string
}

func (e *CapacityError) Error() string {
	return fmt.Sprintf("non-physical state at day %.4f (step %d) group %d [%s]: %s",
		e.Day, e.StepIndex, e.Group, e.Reason, e.Message)
}

// Errorf builds a ValidationError with the given reason; it lets other
// packages reject inputs with the same distinguishable error type.
func Errorf(r Reason, format string, args ...any) error {
	return validationError(r, format, args...)
}
