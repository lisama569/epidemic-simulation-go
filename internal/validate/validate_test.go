package validate

import (
	"errors"
	"testing"

	"github.com/lisama569/epidemic-simulation-go/internal/model"
)

func kindOf(t *testing.T, err error) model.ErrorKind {
	t.Helper()
	var ve *model.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("期望归类错误, 实际 %T: %v", err, err)
	}
	return ve.Kind
}

// base 返回一个合法的最小情景，各用例只改一处制造矛盾。
func base() *model.Scenario {
	return &model.Scenario{
		ConfigVersion: 2, Paradigm: model.ParadigmSIR,
		Parameters:  model.Parameters{Beta: 0.3, Gamma: 0.1, Sigma: 0},
		HorizonDays: 10, StepDays: 0.5,
		Groups:        []model.Group{{ID: "a", S0: 95, I0: 5}},
		ContactMatrix: [][]float64{{2.0}},
	}
}

func TestValidScenarioPasses(t *testing.T) {
	if err := Scenario(base()); err != nil {
		t.Fatalf("合法情景被拒绝: %v", err)
	}
}

func TestPopulationNotClosed(t *testing.T) {
	cases := map[string]func(*model.Scenario){
		"负初始人数":  func(s *model.Scenario) { s.Groups[0].S0 = -1 },
		"零总人口":   func(s *model.Scenario) { s.Groups[0].S0, s.Groups[0].I0 = 0, 0 },
		"分组id重复": func(s *model.Scenario) { s.Groups = append(s.Groups, model.Group{ID: "a", S0: 10}) },
	}
	for name, mut := range cases {
		s := base()
		mut(s)
		err := Scenario(s)
		if k := kindOf(t, err); k != model.ErrPopulationNotClosed {
			t.Fatalf("%s: kind=%s err=%v", name, k, err)
		}
		t.Logf("[%s] 拒绝原因=%s: %v", name, model.ErrPopulationNotClosed, err)
	}
}

func TestSIRRejectsExposed(t *testing.T) {
	s := base()
	s.Groups[0].E0 = 3
	if k := kindOf(t, Scenario(s)); k != model.ErrPopulationNotClosed {
		t.Fatalf("sir 带 e0 应归类人口不闭合, got %s", k)
	}
}

func TestContactInvalid(t *testing.T) {
	s := base()
	s.Groups = append(s.Groups, model.Group{ID: "b", S0: 10})
	s.ContactMatrix = [][]float64{{2, 0.5}} // 行数对、列数错
	if k := kindOf(t, Scenario(s)); k != model.ErrContactInvalid {
		t.Fatalf("got %s", k)
	}
	s.ContactMatrix = [][]float64{{2, -1}, {0.5, 2}} // 负接触
	if k := kindOf(t, Scenario(s)); k != model.ErrContactInvalid {
		t.Fatalf("负接触 got %s: %v", k, Scenario(s))
	}
	t.Log("接触矩阵形状/负值均被归类为 contact_matrix_invalid")
}

func TestMigrationInvalid(t *testing.T) {
	s := base()
	s.MigrationMatrix = [][]float64{{0.9}} // 行和 !=1 => 人口不闭合
	if k := kindOf(t, Scenario(s)); k != model.ErrMigrationInvalid {
		t.Fatalf("行和不归一 got %s", k)
	}
	s.MigrationMatrix = [][]float64{{0.5, 0.5}} // 形状错误
	if k := kindOf(t, Scenario(s)); k != model.ErrMigrationInvalid {
		t.Fatalf("形状错误 got %s", k)
	}
	t.Logf("迁移不闭合被拒绝: 行和 0.9 != 1")
}

func TestTimelineReversed(t *testing.T) {
	s := base()
	s.HorizonDays = 10.3 // 0.5 不能整除
	if k := kindOf(t, Scenario(s)); k != model.ErrTimelineReversed {
		t.Fatalf("非整数步 got %s", k)
	}
	s = base()
	iv := model.Intervention{Type: model.InterventionContactReduction, Groups: []string{"a"},
		StartDays: 6, EndDays: 5, Strength: 0.5, Compliance: 1} // 倒退
	s.Interventions = []model.Intervention{iv}
	if k := kindOf(t, Scenario(s)); k != model.ErrTimelineReversed {
		t.Fatalf("区间倒退 got %s: %v", k, Scenario(s))
	}
	s = base()
	iv = model.Intervention{Type: model.InterventionContactReduction, Groups: []string{"a"},
		StartDays: 0.3, EndDays: 2, Strength: 0.5, Compliance: 1} // 不对齐网格
	s.Interventions = []model.Intervention{iv}
	if k := kindOf(t, Scenario(s)); k != model.ErrTimelineReversed {
		t.Fatalf("网格不对齐 got %s", k)
	}
	t.Log("horizon 不整除、区间倒退、端点不对齐均归类 timeline_reversed")
}

func TestIntervalOverlap(t *testing.T) {
	s := base()
	mk := func(st, en float64) model.Intervention {
		return model.Intervention{Type: model.InterventionContactReduction, Groups: []string{"a"},
			StartDays: st, EndDays: en, Strength: 0.5, Compliance: 1}
	}
	s.Interventions = []model.Intervention{mk(1, 3), mk(2, 4)} // 同组同类重叠
	if k := kindOf(t, Scenario(s)); k != model.ErrIntervalOverlap {
		t.Fatalf("重叠 got %s: %v", k, Scenario(s))
	}
	// 首尾相接 [1,3) 与 [3,5) 不重叠，必须允许。
	s.Interventions = []model.Intervention{mk(1, 3), mk(3, 5)}
	if err := Scenario(s); err != nil {
		t.Fatalf("首尾相接不应拒绝: %v", err)
	}
	// 目标分组不相交的重叠区间也应允许。
	s.Groups = []model.Group{{ID: "a", S0: 90, I0: 5}, {ID: "b", S0: 5}}
	s.ContactMatrix = [][]float64{{2, 0}, {0, 2}}
	s.Interventions = []model.Intervention{
		{Type: model.InterventionContactReduction, Groups: []string{"a"},
			StartDays: 1, EndDays: 4, Strength: 0.5, Compliance: 1},
		{Type: model.InterventionContactReduction, Groups: []string{"b"},
			StartDays: 1, EndDays: 4, Strength: 0.5, Compliance: 1},
	}
	if err := Scenario(s); err != nil {
		t.Fatalf("不同分组重叠应允许: %v", err)
	}
	t.Log("同组同类重叠被拒绝；首尾相接与分组不相交被允许")
}

func TestParameterAndInterventionRange(t *testing.T) {
	s := base()
	s.Parameters.Beta = 0
	if k := kindOf(t, Scenario(s)); k != model.ErrParameterOutOfRange {
		t.Fatalf("beta=0 got %s", k)
	}
	s = base()
	s.Parameters.Sigma = 0.2 // sir 口径 sigma 必须 0
	if k := kindOf(t, Scenario(s)); k != model.ErrParameterOutOfRange {
		t.Fatalf("sir sigma!=0 got %s", k)
	}
	s = base()
	s.Interventions = []model.Intervention{{
		Type: model.InterventionIsolation, Groups: []string{"a"},
		StartDays: 1, EndDays: 4, RatePerDay: 0.3, Compliance: 1.5, // 依从度越界
	}}
	if k := kindOf(t, Scenario(s)); k != model.ErrInterventionInvalid {
		t.Fatalf("compliance=1.5 got %s: %v", k, Scenario(s))
	}
	s = base()
	s.Interventions = []model.Intervention{{
		Type: model.InterventionIsolation, Groups: []string{"ghost"},
		StartDays: 1, EndDays: 4, RatePerDay: 0.3, Compliance: 1,
	}}
	if k := kindOf(t, Scenario(s)); k != model.ErrGroupReference {
		t.Fatalf("未知分组 got %s", k)
	}
	t.Log("参数越界、干预字段越界、未知分组引用均被区分归类")
}
