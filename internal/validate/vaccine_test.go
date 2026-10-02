package validate

import (
	"errors"
	"math"
	"testing"

	"github.com/lisama569/epidemic-simulation-go/internal/model"
)

func vaccBase() *model.Scenario {
	return &model.Scenario{
		ConfigVersion: 2, Paradigm: model.ParadigmSIR,
		Parameters:  model.Parameters{Beta: 0.3, Gamma: 0.1},
		HorizonDays: 20, StepDays: 0.5,
		Groups: []model.Group{
			{ID: "a", S0: 100, I0: 1},
			{ID: "b", S0: 100},
		},
		ContactMatrix: [][]float64{{2, 0}, {0, 2}},
		VaccineQuotas: []model.VaccineQuota{{ID: "pool", DosesPerDay: 50}},
		Interventions: []model.Intervention{
			{Type: model.InterventionVaccination, Groups: []string{"a"},
				StartDays: 2, EndDays: 10, DosesPerDay: 40, Compliance: 1,
				QuotaID: "pool", Priority: 1},
		},
	}
}

func wantKind(t *testing.T, sc *model.Scenario, want model.ErrorKind) {
	t.Helper()
	err := Scenario(sc)
	if err == nil {
		t.Fatalf("情景应当被拒绝（期望 %s），但通过了", want)
	}
	var ve *model.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("错误不是 ValidationError: %v", err)
	}
	if ve.Kind != want {
		t.Fatalf("错误归类=%s，期望 %s；细节: %v", ve.Kind, want, ve.Detail)
	}
	t.Logf("正确拒绝[%s]: %s", want, ve.Detail)
}

// TestVaccConfigValid 合法基线必须通过。
func TestVaccConfigValid(t *testing.T) {
	if err := Scenario(vaccBase()); err != nil {
		t.Fatalf("合法接种情景被拒绝: %v", err)
	}
}

// TestVaccConfigQuotaReferenceMissing 引用不存在的额度池。
func TestVaccConfigQuotaReferenceMissing(t *testing.T) {
	sc := vaccBase()
	sc.Interventions[0].QuotaID = "ghost"
	wantKind(t, sc, model.ErrVaccineQuotaInvalid)
}

// TestVaccConfigQuotaNegativeOrNonFinite 额度为负 / NaN / Inf，分别拒绝。
func TestVaccConfigQuotaNegativeOrNonFinite(t *testing.T) {
	for _, v := range []float64{-1, math.NaN(), math.Inf(1)} {
		sc := vaccBase()
		sc.VaccineQuotas[0].DosesPerDay = v
		wantKind(t, sc, model.ErrVaccineQuotaInvalid)
	}
	// 接种安排本身的日剂量为负/零/非有限也要拒绝（归类 vaccination_invalid）。
	for _, v := range []float64{0, -5, math.NaN()} {
		sc := vaccBase()
		sc.Interventions[0].DosesPerDay = v
		wantKind(t, sc, model.ErrVaccinationInvalid)
	}
}

// TestVaccConfigTimeline 接种区间未对齐自然日 / 倒退 / 越界 / 步长不整除天。
func TestVaccConfigTimeline(t *testing.T) {
	// 端点未落在自然日边界。
	sc := vaccBase()
	sc.Interventions[0].StartDays = 2.5
	wantKind(t, sc, model.ErrVaccinationTimeline)

	// 区间倒退（end<=start，由通用时间轴检查以 timeline_reversed 拒绝）。
	sc = vaccBase()
	sc.Interventions[0].EndDays = sc.Interventions[0].StartDays
	wantKind(t, sc, model.ErrTimelineReversed)

	// 越界 horizon。
	sc = vaccBase()
	sc.Interventions[0].EndDays = 21
	wantKind(t, sc, model.ErrTimelineReversed)

	// 1/stepDays 不是整数。
	sc = vaccBase()
	sc.StepDays = 0.4
	wantKind(t, sc, model.ErrVaccinationTimeline)

	// 端点不落在步长网格（通用检查 timeline_reversed）。
	sc = vaccBase()
	sc.Interventions[0].EndDays = 10.25
	wantKind(t, sc, model.ErrTimelineReversed)
}

// TestVaccConfigConflictingArrangements 同一分组同一时段两个接种安排，
// 必须以 vaccination_conflict 单独拒绝（区别于普通 interval_overlap）。
func TestVaccConfigConflictingArrangements(t *testing.T) {
	sc := vaccBase()
	sc.Interventions = append(sc.Interventions, model.Intervention{
		Type: model.InterventionVaccination, Groups: []string{"a"},
		StartDays: 5, EndDays: 12, DosesPerDay: 10, Compliance: 1,
		QuotaID: "pool", Priority: 2,
	})
	wantKind(t, sc, model.ErrVaccinationConflict)

	// 同一时段不同分组是允许的（共享额度靠优先级分配，不视为矛盾）。
	sc2 := vaccBase()
	sc2.Interventions = append(sc2.Interventions, model.Intervention{
		Type: model.InterventionVaccination, Groups: []string{"b"},
		StartDays: 2, EndDays: 10, DosesPerDay: 10, Compliance: 1,
		QuotaID: "pool", Priority: 2,
	})
	if err := Scenario(sc2); err != nil {
		t.Fatalf("不同分组同期接种应允许: %v", err)
	}
	// 同一分组首尾相接（不重叠）也允许。
	sc3 := vaccBase()
	sc3.Interventions = append(sc3.Interventions, model.Intervention{
		Type: model.InterventionVaccination, Groups: []string{"a"},
		StartDays: 10, EndDays: 14, DosesPerDay: 10, Compliance: 1,
		QuotaID: "pool", Priority: 2,
	})
	if err := Scenario(sc3); err != nil {
		t.Fatalf("同分组首尾相接的接种安排应允许: %v", err)
	}
	t.Log("判定依据: 重叠且同分组=>vaccination_conflict；不同分组/首尾相接=>允许")
}

// TestVaccConfigCollisionWithOtherTypes 接种与接触缩减/隔离撞车允许，
// 但其它类型携带接种专属字段、或接种携带其它类型字段要分清拒绝类别。
func TestVaccConfigCollisionWithOtherTypes(t *testing.T) {
	sc := vaccBase()
	// 同期接触缩减与隔离：不同类型，允许。
	sc.Interventions = append(sc.Interventions,
		model.Intervention{
			Type: model.InterventionContactReduction, Groups: []string{"a", "b"},
			StartDays: 2, EndDays: 10, Strength: 0.5, Compliance: 1,
		},
		model.Intervention{
			Type: model.InterventionIsolation, Groups: []string{"a"},
			StartDays: 2, EndDays: 10, RatePerDay: 0.3, Compliance: 1,
		},
	)
	if err := Scenario(sc); err != nil {
		t.Fatalf("接种与其它类型同期生效应允许: %v", err)
	}

	// 接触缩减误带 quotaId => intervention_invalid（不是额度类错误）。
	bad := vaccBase()
	bad.Interventions = append(bad.Interventions, model.Intervention{
		Type: model.InterventionContactReduction, Groups: []string{"a"},
		StartDays: 11, EndDays: 14, Strength: 0.5, Compliance: 1, QuotaID: "pool",
	})
	wantKind(t, bad, model.ErrInterventionInvalid)

	// 接种误带 capacity => vaccination_invalid。
	bad2 := vaccBase()
	c := 10.0
	bad2.Interventions[0].Capacity = &c
	wantKind(t, bad2, model.ErrVaccinationInvalid)

	// 接种缺少 quotaId。
	bad3 := vaccBase()
	bad3.Interventions[0].QuotaID = ""
	wantKind(t, bad3, model.ErrVaccinationInvalid)

	// 引用不存在的分组仍归 group_reference_invalid。
	bad4 := vaccBase()
	bad4.Interventions[0].Groups = []string{"nobody"}
	wantKind(t, bad4, model.ErrGroupReference)

	// 声明了额度池却没有接种安排。
	bad5 := vaccBase()
	bad5.Interventions = nil
	wantKind(t, bad5, model.ErrVaccineQuotaInvalid)

	// 额度池 id 重复。
	bad6 := vaccBase()
	bad6.VaccineQuotas = append(bad6.VaccineQuotas,
		model.VaccineQuota{ID: "pool", DosesPerDay: 10})
	wantKind(t, bad6, model.ErrVaccineQuotaInvalid)
}
