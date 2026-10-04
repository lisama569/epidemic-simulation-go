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
	// ErrVaccinationInvalid 接种安排本身非法（速率/依从度/携带其他类型字段等）。
	ErrVaccinationInvalid ErrorKind = "vaccination_invalid"
	// ErrVaccineQuotaInvalid 疫苗额度池定义非法（额度为负/非有限、时间轴非法、id 重复等）。
	ErrVaccineQuotaInvalid ErrorKind = "vaccine_quota_invalid"
	// ErrVaccineQuotaReference 接种安排（含其优先级声明）引用了不存在的额度池。
	ErrVaccineQuotaReference ErrorKind = "vaccine_quota_reference_invalid"
	// ErrVaccinationScheduleConflict 同一分组同一时段的接种安排互相矛盾
	// （与其他类型干预的区间重叠 intervention_interval_overlap 区分开）。
	ErrVaccinationScheduleConflict ErrorKind = "vaccination_schedule_conflict"
	// ErrVaccinationTimeline 接种相关时间轴非法：额度池或接种安排的区间
	// 倒退/越界/不对齐步长网格（与一般时间轴问题 timeline_reversed 区分开）。
	ErrVaccinationTimeline ErrorKind = "vaccination_timeline_invalid"
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
