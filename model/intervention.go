package model

// InterventionKind identifies an intervention measure.
type InterventionKind string

const (
	// InterventionContactReduction multiplies the contact rates of the named
	// groups by (1 - compliance*strength).
	InterventionContactReduction InterventionKind = "contact_reduction"
	// InterventionVaccination moves people S -> R at a per-capita rate,
	// capped per day by a daily capacity (people/day).
	InterventionVaccination InterventionKind = "vaccination"
	// InterventionIsolation moves people E or I -> Q at a per-capita rate,
	// capped per day by a daily capacity (people/day).
	InterventionIsolation InterventionKind = "isolation"
)

// Intervention is one time-bounded measure.
type Intervention struct {
	Kind       InterventionKind `json:"kind"`
	StartDay   float64          `json:"start_day"`
	EndDay     float64          `json:"end_day"`
	Compliance float64          `json:"compliance"`
	// Strength is the contact-reduction fraction in [0,1].
	Strength float64 `json:"strength,omitempty"`
	// Groups lists targeted group indices; empty means all groups.
	Groups []int `json:"groups,omitempty"`
	// Rate is the per-capita daily uptake/tracing rate (vaccination/isolation).
	Rate float64 `json:"rate,omitempty"`
	// Capacity is the maximum number of people moved per day. <= 0 means
	// unlimited.
	Capacity float64 `json:"capacity,omitempty"`
}
