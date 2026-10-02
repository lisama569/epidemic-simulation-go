// Package model 定义传染病推演引擎的核心领域类型与状态约定。
//
// 状态约定：每个分组(group)在任一时刻持有 S/E/I/R/Q/V 六个仓室：
//   - S 易感, E 潜伏(仅 seir 口径), I 感染且可传播, R 移除, Q 被隔离(已从传播环移除),
//     V 免疫接种成功（既不会被感染也不参与传播，但仍计入总人口）
//
// sir 口径下 E 恒为 0。所有仓室人数必须非负、有限。
package model

// Paradigm 是建模口径标识。
type Paradigm string

const (
	// ParadigmSIR 群体均仓 SIR 口径（无 E 仓）。
	ParadigmSIR Paradigm = "sir"
	// ParadigmSEIR 分仓 SEIR 口径。
	ParadigmSEIR Paradigm = "seir"
)

// Compartment 是观测可引用的仓室标识。
type Compartment string

const (
	CompS Compartment = "S"
	CompE Compartment = "E"
	CompI Compartment = "I"
	CompR Compartment = "R"
	CompQ Compartment = "Q"
	// CompV 免疫接种成功仓室：独立可观测，不感染、不传播。
	CompV Compartment = "V"
)

// Group 描述一个人口分组及其初始状态。
type Group struct {
	ID   string  `json:"id"`
	Name string  `json:"name,omitempty"`
	S0   float64 `json:"s0"`
	E0   float64 `json:"e0,omitempty"`
	I0   float64 `json:"i0"`
	R0   float64 `json:"r0,omitempty"`
}

// Parameters 是与时间无关的疾病学参数（每日速率）。
// Sigma 仅在 seir 口径下使用且必须为正；sir 口径下必须为 0。
type Parameters struct {
	Beta  float64 `json:"beta"`  // 每接触传播概率（每日）
	Gamma float64 `json:"gamma"` // 恢复速率（每日）
	Sigma float64 `json:"sigma"` // E->I 速率（每日）
}

// InterventionType 标识干预类型。
type InterventionType string

const (
	// InterventionContactReduction 接触缩减：按比例缩放目标分组的接触行。
	InterventionContactReduction InterventionType = "contact_reduction"
	// InterventionIsolation 隔离：以固定速率将 I 仓个体移入 Q 仓，受容量约束。
	InterventionIsolation InterventionType = "isolation"
	// InterventionVaccination 免疫接种：在指定时段给目标分组每日接种，
	// 成功者从 S 仓移入 V 仓；接种量受共享的每日疫苗额度约束。
	InterventionVaccination InterventionType = "vaccination"
)

// Intervention 描述一项分阶段干预。
// 区间为半开 [StartDays, EndDays)；同类型、目标分组有交集且区间重叠的两项干预
// 必须被拒绝（叠加口径不唯一）。不同类型允许同时生效。
type Intervention struct {
	Type       InterventionType `json:"type"`
	Groups     []string         `json:"groups"`
	StartDays  float64          `json:"startDays"`
	EndDays    float64          `json:"endDays"`
	Strength   float64          `json:"strength,omitempty"`   // contact_reduction: 缩减比例 [0,1]
	RatePerDay float64          `json:"ratePerDay,omitempty"` // isolation: 隔离速率（每日）
	Compliance float64          `json:"compliance"`           // 依从度 [0,1]
	// Capacity 为隔离容量上限（同时在 Q 仓人数）。nil 表示容量无界；
	// 该语义是显式约定，零值不会被当作无界。
	Capacity *float64 `json:"capacity,omitempty"`

	// 以下字段仅 vaccination 使用：
	// DosesPerDay 为该安排每日计划给 Groups 中每个分组接种的人数（速率语义，
	// 按步长折算到每个积分步）；
	// QuotaID 引用 Scenario.VaccineQuotas 中的一个共享额度池；
	// Priority 为额度不足时的分配优先级（数值越小越优先，并列时按情景中的
	// 稳定次序分配）。
	DosesPerDay float64 `json:"dosesPerDay,omitempty"`
	QuotaID     string  `json:"quotaId,omitempty"`
	Priority    int     `json:"priority,omitempty"`
}

// VaccineQuota 描述一个在多个接种安排之间共享的每日疫苗额度池。
// DosesPerDay 为每个自然日 0 点补充的额度（按步长折算到积分步）；
// Accumulate=true 时当日未用完的额度结转到下一天，否则当日清零。
type VaccineQuota struct {
	ID          string  `json:"id"`
	DosesPerDay float64 `json:"dosesPerDay"`
	Accumulate  bool    `json:"accumulate,omitempty"`
}

// Scenario 是一次推演的完整输入。
type Scenario struct {
	ConfigVersion int        `json:"configVersion"`
	Paradigm      Paradigm   `json:"paradigm"`
	Parameters    Parameters `json:"parameters"`
	HorizonDays   float64    `json:"horizonDays"`
	StepDays      float64    `json:"stepDays"`
	Groups        []Group    `json:"groups"`
	// ContactMatrix[g][h]：分组 g 中每人每日与分组 h 的接触次数。
	ContactMatrix [][]float64 `json:"contactMatrix"`
	// MigrationMatrix[i][j]（i≠j）：每日从分组 i 迁移到 j 的人口比例（速率语义）。
	// 每行之和必须等于 1（全局闭合约束）；对角项 M[i][i] 等价于“不外迁比例”，
	// 其值由行闭合约束隐含确定。引擎以稳定子步进欧拉应用（任意步长非负守恒）。
	// nil 表示无迁移。
	MigrationMatrix [][]float64    `json:"migrationMatrix,omitempty"`
	Interventions   []Intervention `json:"interventions,omitempty"`
	// VaccineQuotas 声明可被 vaccination 干预按 quotaId 共享引用的每日额度池。
	// 无接种干预时必须为空；旧版本配置归一后亦为空。
	VaccineQuotas []VaccineQuota `json:"vaccineQuotas,omitempty"`
	// RandomSeed 仅用于合成观测加噪等随机环节；确定性推演本身不使用随机数。
	RandomSeed int64 `json:"randomSeed,omitempty"`
}

// State 是某一时刻全部分组的仓室状态，按分组下标索引。
type State struct {
	S, E, I, R, Q, V []float64
}

// CapacityEvent 记录一次容量受限事件，可定位时间、分组与原因。
type CapacityEvent struct {
	TimeDays  float64 `json:"timeDays"`
	GroupID   string  `json:"groupId"`
	Demand    float64 `json:"demand"`
	Available float64 `json:"available"`
	Admitted  float64 `json:"admitted"`
	Reason    string  `json:"reason"`
}

// 接种额度受限/少接种事件的原因取值。
const (
	// VaccReasonQuotaExhausted 当日共享额度不足导致少接种。
	VaccReasonQuotaExhausted = "vaccine_quota_exhausted"
	// VaccReasonEligibleExhausted 目标分组可接种（S 仓）人数不足导致少接种。
	VaccReasonEligibleExhausted = "eligible_susceptible_exhausted"
)

// VaccinationEvent 记录某一天某接种安排在某分组上的接种分配明细。
// 任何“计划接种 > 实际接种”的情形都会记录一条，Reason 区分是额度不足
// 还是可接种人口不足；Demand 为折算到该步的原计划人数，Allocated 为
// 实际接种人数，QuotaID/Priority 记录当时的分配依据。
type VaccinationEvent struct {
	TimeDays       float64 `json:"timeDays"`
	Day            int     `json:"day"`
	GroupID        string  `json:"groupId"`
	QuotaID        string  `json:"quotaId"`
	Priority       int     `json:"priority"`
	InterventionID int     `json:"interventionIndex"`
	Demand         float64 `json:"demand"`
	Allocated      float64 `json:"allocated"`
	Shortfall      float64 `json:"shortfall"`
	Reason         string  `json:"reason"`
}

// Trajectory 是一次推演的完整离散轨迹。
// S/E/I/R/Q/V[g][k] 为分组 g 在第 k 个时间点的仓室人数。
type Trajectory struct {
	GroupIDs          []string           `json:"groupIds"`
	Times             []float64          `json:"times"`
	S, E, I, R, Q, V  [][]float64        `json:"-"`
	CapacityEvents    []CapacityEvent    `json:"capacityEvents,omitempty"`
	VaccinationEvents []VaccinationEvent `json:"vaccinationEvents,omitempty"`
}

// Observation 是一条观测：某时刻某分组某仓室的观测人数。
type Observation struct {
	TimeDays    float64     `json:"timeDays"`
	GroupID     string      `json:"groupId"`
	Compartment Compartment `json:"compartment"`
	Value       float64     `json:"value"`
}

// ObservationSeries 是带建模口径标识的观测序列，口径不兼容必须拒绝。
type ObservationSeries struct {
	Paradigm     Paradigm      `json:"paradigm"`
	Observations []Observation `json:"observations"`
}
