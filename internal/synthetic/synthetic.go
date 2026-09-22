// Package synthetic 在本地生成确定性合成数据（基准情景与带噪观测），
// 全过程不依赖外部服务或真实数据。随机数只用于观测加噪，使用固定种子的
// 独立 rand.Rand，按 (时间点, 分组) 的确定顺序消费，因此同种子逐位复现。
package synthetic

import (
	"math"
	"math/rand"

	"github.com/lisama569/epidemic-simulation-go/internal/engine"
	"github.com/lisama569/epidemic-simulation-go/internal/intervention"
	"github.com/lisama569/epidemic-simulation-go/internal/model"
)

// GenerateOptions 控制合成观测的抽样时间点、观测仓室与噪声水平。
type GenerateOptions struct {
	SampleTimes []float64
	Compartment model.Compartment
	// NoiseSD 为加性高斯噪声标准差；0 表示无噪观测。
	NoiseSD float64
	// Seed 为随机种子；噪声消费顺序确定，保证可复现。
	Seed int64
}

// GenerateObserved 对基准情景推演并在抽样点生成带噪观测（截断到非负）。
// 抽样时间点必须落在推演网格上且在 [0,horizon] 内，否则返回错误而非静默调整。
func GenerateObserved(sc *model.Scenario, opts GenerateOptions) (model.ObservationSeries, error) {
	sched, err := intervention.Build(sc)
	if err != nil {
		return model.ObservationSeries{}, err
	}
	tr, err := engine.Run(sc, sched)
	if err != nil {
		return model.ObservationSeries{}, err
	}
	if opts.Compartment == "" {
		opts.Compartment = model.CompI
	}
	if opts.Compartment == model.CompE && sc.Paradigm != model.ParadigmSEIR {
		return model.ObservationSeries{}, model.NewError(model.ErrObservationMismatch,
			"sir 口径下不存在 E 仓，不能生成 E 仓观测")
	}
	switch opts.Compartment {
	case model.CompS, model.CompE, model.CompI, model.CompR, model.CompQ:
	default:
		return model.ObservationSeries{}, model.NewError(model.ErrObservationMismatch,
			"未知观测仓室 %q", opts.Compartment)
	}
	idxByTime := make(map[float64]int, len(tr.Times))
	for k, t := range tr.Times {
		idxByTime[t] = k
	}
	rng := rand.New(rand.NewSource(opts.Seed))
	series := model.ObservationSeries{Paradigm: sc.Paradigm}
	for _, ts := range opts.SampleTimes {
		k, ok := idxByTime[ts]
		if !ok {
			return series, model.NewError(model.ErrTimelineReversed,
				"抽样时间点 %v 不在推演网格上", ts)
		}
		for g, gid := range tr.GroupIDs {
			base := compartmentValue(tr, g, k, opts.Compartment)
			v := base
			if opts.NoiseSD > 0 {
				v += rng.NormFloat64() * opts.NoiseSD
			}
			if v < 0 {
				v = 0 // 观测人数物理非负：截断而非取绝对值，保持偏差方向可解释
			}
			series.Observations = append(series.Observations, model.Observation{
				TimeDays:    ts,
				GroupID:     gid,
				Compartment: opts.Compartment,
				Value:       v,
			})
		}
	}
	return series, nil
}

func compartmentValue(tr *model.Trajectory, g, k int, c model.Compartment) float64 {
	switch c {
	case model.CompS:
		return tr.S[g][k]
	case model.CompE:
		return tr.E[g][k]
	case model.CompI:
		return tr.I[g][k]
	case model.CompR:
		return tr.R[g][k]
	case model.CompQ:
		return tr.Q[g][k]
	default:
		return math.NaN()
	}
}

// ReferenceScenario 返回内置的确定性参考情景（两分组 SEIR + 迁移 +
// 分阶段接触缩减与容量受限隔离），用于本地演示与测试。
func ReferenceScenario() *model.Scenario {
	capIso := 40.0
	return &model.Scenario{
		ConfigVersion: 2,
		Paradigm:      model.ParadigmSEIR,
		Parameters:    model.Parameters{Beta: 0.32, Gamma: 0.1, Sigma: 0.2},
		HorizonDays:   60,
		StepDays:      0.5,
		RandomSeed:    20260923,
		Groups: []model.Group{
			{ID: "city", Name: "城区", S0: 990, I0: 10},
			{ID: "town", Name: "乡镇", S0: 600},
		},
		ContactMatrix:   [][]float64{{2.4, 0.3}, {0.3, 2.0}},
		MigrationMatrix: [][]float64{{0.95, 0.05}, {0.08, 0.92}},
		Interventions: []model.Intervention{
			{Type: model.InterventionContactReduction, Groups: []string{"city", "town"},
				StartDays: 10, EndDays: 30, Strength: 0.65, Compliance: 0.85},
			{Type: model.InterventionIsolation, Groups: []string{"city", "town"},
				StartDays: 12, EndDays: 40, RatePerDay: 0.4, Compliance: 0.9, Capacity: &capIso},
		},
	}
}
