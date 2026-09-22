// Package model 定义传染病推演引擎的核心领域类型与状态约定。
//
// 状态约定：每个分组(group)在任一时刻持有 S/E/I/R/Q 五个仓室：
//   - S 易感, E 潜伏(仅 seir 口径), I 感染且可传播, R 移除, Q 被隔离(已从传播环移除)
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
	// RandomSeed 仅用于合成观测加噪等随机环节；确定性推演本身不使用随机数。
	RandomSeed int64 `json:"randomSeed,omitempty"`
}

// State 是某一时刻全部分组的仓室状态，按分组下标索引。
type State struct {
	S, E, I, R, Q []float64
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

// Trajectory 是一次推演的完整离散轨迹。
// S/E/I/R/Q[g][k] 为分组 g 在第 k 个时间点的仓室人数。
type Trajectory struct {
	GroupIDs       []string        `json:"groupIds"`
	Times          []float64       `json:"times"`
	S, E, I, R, Q  [][]float64     `json:"-"`
	CapacityEvents []CapacityEvent `json:"capacityEvents,omitempty"`
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
