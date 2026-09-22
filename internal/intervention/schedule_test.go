package intervention

import (
	"testing"

	"github.com/lisama569/epidemic-simulation-go/internal/model"
)

func scenarioWith(ivs ...model.Intervention) *model.Scenario {
	return &model.Scenario{
		ConfigVersion: 2, Paradigm: model.ParadigmSIR,
		Parameters:  model.Parameters{Beta: 0.3, Gamma: 0.1},
		HorizonDays: 10, StepDays: 1,
		Groups: []model.Group{
			{ID: "a", S0: 90, I0: 5},
			{ID: "b", S0: 50},
		},
		ContactMatrix: [][]float64{{2, 0}, {0, 2}},
		Interventions: ivs,
	}
}

func TestHalfOpenActiveTimes(t *testing.T) {
	sc := scenarioWith(
		model.Intervention{Type: model.InterventionContactReduction, Groups: []string{"a"},
			StartDays: 2, EndDays: 5, Strength: 0.5, Compliance: 1},
		model.Intervention{Type: model.InterventionContactReduction, Groups: []string{"a"},
			StartDays: 5, EndDays: 8, Strength: 0.3, Compliance: 1},
	)
	s, err := Build(sc)
	if err != nil {
		t.Fatal(err)
	}
	check := func(tm float64, wantN int, wantStrength float64) {
		got := s.ActiveAt(tm)
		if len(got) != wantN {
			t.Fatalf("t=%v 生效数=%d 期望 %d", tm, len(got), wantN)
		}
		if wantN > 0 && got[0].Strength != wantStrength {
			t.Fatalf("t=%v strength=%v 期望 %v", tm, got[0].Strength, wantStrength)
		}
	}
	check(1.999, 0, 0)
	check(2, 1, 0.5) // 开始时点立即生效
	check(4.999, 1, 0.5)
	check(5, 1, 0.3) // 首尾相接：前者已失效、后者生效，不重叠
	check(8, 0, 0)   // 结束时点已失效
	t.Log("半开区间 [start,end) 判定正确；t=5 时旧措施失效、新措施生效")
}

func TestDifferentTypesMayCoexist(t *testing.T) {
	sc := scenarioWith(
		model.Intervention{Type: model.InterventionContactReduction, Groups: []string{"a"},
			StartDays: 2, EndDays: 6, Strength: 0.5, Compliance: 1},
		model.Intervention{Type: model.InterventionIsolation, Groups: []string{"a"},
			StartDays: 2, EndDays: 6, RatePerDay: 0.3, Compliance: 1},
	)
	s, err := Build(sc)
	if err != nil {
		t.Fatalf("不同类型同时生效应允许: %v", err)
	}
	if n := len(s.ActiveAt(3)); n != 2 {
		t.Fatalf("t=3 应有两项不同类型干预生效, got %d", n)
	}
	// 返回顺序按原始下标，确定可复现。
	got := s.ActiveAt(3)
	if got[0].Type != model.InterventionContactReduction ||
		got[1].Type != model.InterventionIsolation {
		t.Fatalf("生效干预顺序不确定: %v %v", got[0].Type, got[1].Type)
	}
	t.Log("不同类型干预可同时段生效，且返回顺序按原始下标确定")
}
