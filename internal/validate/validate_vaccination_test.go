package validate

import (
	"math"
	"testing"

	"github.com/lisama569/epidemic-simulation-go/internal/model"
)

// vaxBase 返回一个含合法额度池与接种安排的情景，各用例只改一处制造矛盾。
func vaxBase() *model.Scenario {
	s := base()
	s.VaccineQuotas = []model.VaccineQuota{
		{ID: "nat", DailyLimit: 100, StartDays: 0, EndDays: 10},
	}
	s.Interventions = []model.Intervention{
		{Type: model.InterventionVaccination, Groups: []string{"a"},
			StartDays: 2, EndDays: 8, RatePerDay: 0.3, Compliance: 1.0, QuotaID: "nat"},
	}
	return s
}

func TestVaccinationValidPasses(t *testing.T) {
	if err := Scenario(vaxBase()); err != nil {
		t.Fatalf("合法接种情景被拒绝: %v", err)
	}
	t.Log("合法接种情景通过校验（额度池+接种安排+优先级缺省 0）")
}

func TestVaccinationQuotaDefinitionInvalid(t *testing.T) {
	cases := map[string]func(*model.Scenario){
		"额度为负":    func(s *model.Scenario) { s.VaccineQuotas[0].DailyLimit = -1 },
		"额度非有限":   func(s *model.Scenario) { s.VaccineQuotas[0].DailyLimit = math.NaN() },
		"额度池id为空": func(s *model.Scenario) { s.VaccineQuotas[0].ID = "" },
		"额度池id重复": func(s *model.Scenario) {
			s.VaccineQuotas = append(s.VaccineQuotas, model.VaccineQuota{
				ID: "nat", DailyLimit: 10, StartDays: 0, EndDays: 5})
		},
	}
	for name, mut := range cases {
		s := vaxBase()
		mut(s)
		if k := kindOf(t, Scenario(s)); k != model.ErrVaccineQuotaInvalid {
			t.Fatalf("%s: kind=%s 期望 %s", name, k, model.ErrVaccineQuotaInvalid)
		}
		t.Logf("[%s] 拒绝原因 kind=%s", name, model.ErrVaccineQuotaInvalid)
	}
}

func TestVaccinationTimelineInvalid(t *testing.T) {
	cases := map[string]func(*model.Scenario){
		"额度区间倒退":    func(s *model.Scenario) { s.VaccineQuotas[0].StartDays = 8; s.VaccineQuotas[0].EndDays = 2 },
		"额度区间越界":    func(s *model.Scenario) { s.VaccineQuotas[0].EndDays = 12 },
		"额度区间不对齐":   func(s *model.Scenario) { s.VaccineQuotas[0].StartDays = 0.3 },
		"接种区间倒退":    func(s *model.Scenario) { s.Interventions[0].StartDays = 8; s.Interventions[0].EndDays = 2 },
		"接种区间越界":    func(s *model.Scenario) { s.Interventions[0].EndDays = 12 },
		"接种区间不对齐网格": func(s *model.Scenario) { s.Interventions[0].StartDays = 2.3 },
	}
	for name, mut := range cases {
		s := vaxBase()
		mut(s)
		if k := kindOf(t, Scenario(s)); k != model.ErrTimelineReversed {
			t.Fatalf("%s: kind=%s 期望 %s", name, k, model.ErrTimelineReversed)
		}
		t.Logf("[%s] 拒绝原因 kind=%s", name, model.ErrTimelineReversed)
	}
}

func TestVaccinationQuotaReferenceInvalid(t *testing.T) {
	cases := map[string]func(*model.Scenario){
		"引用不存在的额度池": func(s *model.Scenario) { s.Interventions[0].QuotaID = "ghost" },
		"缺少quotaId": func(s *model.Scenario) { s.Interventions[0].QuotaID = "" },
	}
	for name, mut := range cases {
		s := vaxBase()
		mut(s)
		if k := kindOf(t, Scenario(s)); k != model.ErrVaccineQuotaReference {
			t.Fatalf("%s: kind=%s 期望 %s", name, k, model.ErrVaccineQuotaReference)
		}
		t.Logf("[%s] 拒绝原因 kind=%s", name, model.ErrVaccineQuotaReference)
	}
}

func TestVaccinationInterventionInvalid(t *testing.T) {
	cases := map[string]func(*model.Scenario){
		"速率为负":       func(s *model.Scenario) { s.Interventions[0].RatePerDay = -0.1 },
		"速率非有限":      func(s *model.Scenario) { s.Interventions[0].RatePerDay = math.Inf(1) },
		"携带strength": func(s *model.Scenario) { s.Interventions[0].Strength = 0.5 },
		"携带capacity": func(s *model.Scenario) { c := 10.0; s.Interventions[0].Capacity = &c },
		"接种区间超出额度池": func(s *model.Scenario) {
			s.Interventions[0].StartDays = 0
			s.Interventions[0].EndDays = 10
			s.VaccineQuotas[0].StartDays = 2
			s.VaccineQuotas[0].EndDays = 8
		},
	}
	for name, mut := range cases {
		s := vaxBase()
		mut(s)
		if k := kindOf(t, Scenario(s)); k != model.ErrVaccinationInvalid {
			t.Fatalf("%s: kind=%s 期望 %s", name, k, model.ErrVaccinationInvalid)
		}
		t.Logf("[%s] 拒绝原因 kind=%s", name, model.ErrVaccinationInvalid)
	}
}

// TestVaccinationOverlapRejected 验证同一分组同一时段互相矛盾的两项接种安排
// 被归类为区间重叠（与既有干预同一口径）。
func TestVaccinationOverlapRejected(t *testing.T) {
	s := vaxBase()
	s.Interventions = append(s.Interventions, model.Intervention{
		Type: model.InterventionVaccination, Groups: []string{"a"},
		StartDays: 4, EndDays: 6, RatePerDay: 0.2, Compliance: 1.0, QuotaID: "nat"})
	if k := kindOf(t, Scenario(s)); k != model.ErrIntervalOverlap {
		t.Fatalf("同组同时段接种冲突: kind=%s 期望 %s", k, model.ErrIntervalOverlap)
	}
	t.Logf("同组同时段接种冲突拒绝原因 kind=%s", model.ErrIntervalOverlap)
}

// TestVaccinationCrossTypeDistinguishable 验证接种与其他干预撞车时
// 失败原因可区分：隔离容量非法仍是 intervention_invalid，
// 接种速率非法是 vaccination_invalid，二者不会混淆。
func TestVaccinationCrossTypeDistinguishable(t *testing.T) {
	s := vaxBase()
	// 合法共存：接种 + 接触缩减 + 隔离同时生效。
	s.Interventions = append(s.Interventions,
		model.Intervention{Type: model.InterventionContactReduction, Groups: []string{"a"},
			StartDays: 2, EndDays: 8, Strength: 0.5, Compliance: 1.0},
		model.Intervention{Type: model.InterventionIsolation, Groups: []string{"a"},
			StartDays: 2, EndDays: 8, RatePerDay: 0.5, Compliance: 1.0})
	if err := Scenario(s); err != nil {
		t.Fatalf("三类干预合法共存被拒绝: %v", err)
	}
	// 隔离非法 -> intervention_invalid；接种非法 -> vaccination_invalid。
	badIso := vaxBase()
	badIso.Interventions = append(badIso.Interventions, model.Intervention{
		Type: model.InterventionIsolation, Groups: []string{"a"},
		StartDays: 2, EndDays: 8, RatePerDay: -1, Compliance: 1.0})
	if k := kindOf(t, Scenario(badIso)); k != model.ErrInterventionInvalid {
		t.Fatalf("隔离非法: kind=%s 期望 %s", k, model.ErrInterventionInvalid)
	}
	badVax := vaxBase()
	badVax.Interventions[0].RatePerDay = 0
	if k := kindOf(t, Scenario(badVax)); k != model.ErrVaccinationInvalid {
		t.Fatalf("接种非法: kind=%s 期望 %s", k, model.ErrVaccinationInvalid)
	}
	t.Log("三类干预共存合法；隔离非法=intervention_invalid，接种非法=vaccination_invalid，可区分")
}

// TestNonVaccinationRejectsQuotaFields 验证非接种干预不得携带接种字段。
func TestNonVaccinationRejectsQuotaFields(t *testing.T) {
	s := base()
	s.Interventions = []model.Intervention{
		{Type: model.InterventionContactReduction, Groups: []string{"a"},
			StartDays: 2, EndDays: 8, Strength: 0.5, Compliance: 1.0, QuotaID: "x"},
	}
	if k := kindOf(t, Scenario(s)); k != model.ErrInterventionInvalid {
		t.Fatalf("contact_reduction 携带 quotaId: kind=%s 期望 %s", k, model.ErrInterventionInvalid)
	}
	t.Log("contact_reduction 携带 quotaId 被拒绝 kind=intervention_invalid")
}
