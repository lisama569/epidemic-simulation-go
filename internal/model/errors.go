package model

import "fmt"

// ErrorKind 对输入失败原因进行可区分归类。
type ErrorKind string

const (
	ErrUnsupportedVersion   ErrorKind = "unsupported_config_version"
	ErrPopulationNotClosed  ErrorKind = "population_not_closed"
	ErrContactInvalid       ErrorKind = "contact_matrix_invalid"
	ErrMigrationInvalid     ErrorKind = "migration_matrix_invalid"
	ErrTimelineReversed     ErrorKind = "timeline_reversed"
	ErrIntervalOverlap      ErrorKind = "intervention_interval_overlap"
	ErrParameterOutOfRange  ErrorKind = "parameter_out_of_range"
	ErrInterventionInvalid  ErrorKind = "intervention_invalid"
	ErrGroupReference       ErrorKind = "group_reference_invalid"
	ErrObservationMismatch  ErrorKind = "observation_paradigm_mismatch"
	ErrEstimateNotConverged ErrorKind = "estimate_not_converged"
	ErrFeasibleDomain       ErrorKind = "estimate_outside_feasible_domain"
	ErrCheckpoint           ErrorKind = "checkpoint_invalid"
	// ErrVaccinationInvalid 接种安排本身非法（字段取值、缺失等）。
	ErrVaccinationInvalid ErrorKind = "vaccination_invalid"
	// ErrVaccineQuotaInvalid 疫苗额度池非法（负/非有限、id 重复等）或引用不存在。
	ErrVaccineQuotaInvalid ErrorKind = "vaccine_quota_invalid"
	// ErrVaccinationTimeline 接种时间轴问题：未按整日对齐、倒退、越界等。
	ErrVaccinationTimeline ErrorKind = "vaccination_timeline_invalid"
	// ErrVaccinationConflict 同一分组同一时段存在互相矛盾的多个接种安排。
	ErrVaccinationConflict ErrorKind = "vaccination_conflict"
)

// ValidationError 携带归类原因与可读细节，便于调用方按 Kind 区分处理。
type ValidationError struct {
	Kind   ErrorKind
	Detail string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Kind, e.Detail)
}

// NewError 构造一个归类错误。
func NewError(kind ErrorKind, format string, args ...any) error {
	return &ValidationError{Kind: kind, Detail: fmt.Sprintf(format, args...)}
}

const (
	// ErrConfigSyntax 配置 JSON 语法错误或含未知字段。
	ErrConfigSyntax ErrorKind = "config_syntax_invalid"
)
