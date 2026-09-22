// Package synthetic generates fully local, seeded synthetic observations.
// Nothing here uses real data or external services: a deterministic
// simulation produces true incidence, then a seeded RNG (never shared across
// goroutines) applies Poisson measurement noise.
package synthetic

import (
	"math"
	"math/rand/v2"

	"github.com/lisama569/epidemic-simulation-go/engine"
	"github.com/lisama569/epidemic-simulation-go/model"
)

// Observation is one noisy measurement of new infections.
type Observation struct {
	Day         float64 `json:"day"`
	NewInfected float64 `json:"new_infected"`
	Group       int     `json:"group"` // -1 = total across groups
}

// Sequence is an ordered observation sequence with its provenance.
type Sequence struct {
	Seed      int64          `json:"seed"`
	Beta      float64        `json:"beta"`
	Paradigm  model.Paradigm `json:"paradigm"`
	ModelType string         `json:"model_type"`
	Records   []Observation  `json:"records"`
}

// Config controls observation generation.
type Config struct {
	// Group, when >= 0, observes only that group; otherwise the total.
	Group int
	// Scale multiplies the true step incidence before Poisson noise
	// (e.g. an ascertainment/reporting fraction).
	Scale float64
}

// DefaultConfig returns total-population observations with full ascertainment.
func DefaultConfig() Config { return Config{Group: -1, Scale: 1} }

// Generate deterministically produces noisy incidence observations from a
// validated scenario using its Seed. One RNG is created locally and consumed
// in strict step order, so the output is independent of parallelism.
func Generate(sc *model.Scenario) (*Sequence, error) {
	return GenerateWith(sc, DefaultConfig())
}

// GenerateWith is Generate with an explicit observation configuration.
func GenerateWith(sc *model.Scenario, cfg Config) (*Sequence, error) {
	if err := sc.Validate(); err != nil {
		return nil, err
	}
	scale := cfg.Scale
	if scale <= 0 {
		scale = 1
	}
	if cfg.Group >= len(sc.Groups) {
		return nil, model.Errorf(model.ReasonGroupRefOutOfRange,
			"synthetic observation group %d out of range", cfg.Group)
	}

	res, err := engine.Run(sc)
	if err != nil {
		return nil, err
	}
	rng := rand.New(rand.NewPCG(uint64(sc.Seed), uint64(sc.Seed)^0x9E3779B97F4A7C15))
	seq := &Sequence{
		Seed:      sc.Seed,
		Beta:      sc.Beta,
		Paradigm:  sc.Paradigm,
		ModelType: sc.ModelType,
		Records:   make([]Observation, 0, len(res.NewInfections)),
	}
	for step, row := range res.NewInfections {
		var mean float64
		if cfg.Group >= 0 {
			mean = row[cfg.Group]
		} else {
			for _, v := range row {
				mean += v
			}
		}
		mean *= scale
		day := float64(step+1) * sc.StepDays
		seq.Records = append(seq.Records, Observation{
			Day:         day,
			NewInfected: float64(poisson(rng, mean)),
			Group:       cfg.Group,
		})
	}
	return seq, nil
}

// poisson draws one Poisson(lambda) integer with a fixed algorithm so the
// result is reproducible across Go versions.
func poisson(rng *rand.Rand, lambda float64) int {
	if lambda <= 0 {
		return 0
	}
	if lambda < 30 {
		// Knuth's algorithm.
		l := math.Exp(-lambda)
		k := 0
		p := 1.0
		for {
			k++
			p *= rng.Float64()
			if p <= l {
				return k - 1
			}
		}
	}
	// Normal approximation for large means, always non-negative.
	x := lambda + math.Sqrt(lambda)*rng.NormFloat64()
	if x < 0 {
		return 0
	}
	return int(math.Round(x))
}
