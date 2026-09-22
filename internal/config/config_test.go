package config

import (
	"errors"
	"testing"

	"github.com/lisama569/epidemic-simulation-go/internal/model"
)

func kindOf(t *testing.T, err error) model.ErrorKind {
	t.Helper()
	var ve *model.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("期望 *ValidationError, 实际 %T: %v", err, err)
	}
	return ve.Kind
}

func TestRejectUnknownVersion(t *testing.T) {
	_, err := Load([]byte(`{"configVersion": 99, "paradigm": "sir"}`))
	if k := kindOf(t, err); k != model.ErrUnsupportedVersion {
		t.Fatalf("kind=%s", k)
	}
	t.Logf("非法版本被拒绝: %v", err)
}

func TestRejectSyntaxAndUnknownFields(t *testing.T) {
	if _, err := Load([]byte(`{not json`)); kindOf(t, err) != model.ErrConfigSyntax {
		t.Fatalf("语法错误归类错误: %v", err)
	}
	// v2 严格解析：未知字段必须拒绝而不是静默忽略。
	v2 := `{"configVersion":2,"paradigm":"sir","parameters":{"beta":0.3,"gamma":0.1,"sigma":0},
"horizonDays":10,"stepDays":0.5,"groups":[{"id":"a","s0":99,"i0":1}],
"contactMatrix":[[1]],"transmision":0.4}`
	if _, err := Load([]byte(v2)); kindOf(t, err) != model.ErrConfigSyntax {
		t.Fatalf("未知字段应拒绝: %v", err)
	}
	t.Logf("语法/未知字段均被拒绝")
}

func TestV1NormalizationDeterministic(t *testing.T) {
	v1 := `{
 "configVersion": 1, "paradigm": "SEIR",
 "disease": {"transmission": 0.4, "recovery": 0.12, "incubation": 0.25},
 "timeHorizon": 20, "timeStep": 1,
 "population": [{"id":"a","susceptible":95,"infected":5}],
 "contacts": [[2]],
 "measures": [
   {"kind":"lockdown","targets":["a"],"startTime":2,"endTime":8,"efficacy":0.5},
   {"kind":"quarantine","targets":["a"],"startTime":9,"endTime":15,"rate":0.3,"maxIsolated":10}
 ]}`
	sc, err := Load([]byte(v1))
	if err != nil {
		t.Fatal(err)
	}
	if sc.ConfigVersion != 2 || sc.Paradigm != model.ParadigmSEIR {
		t.Fatalf("归一错误: %+v", sc)
	}
	if sc.Parameters.Beta != 0.4 || sc.Parameters.Sigma != 0.25 {
		t.Fatalf("参数归一错误: %+v", sc.Parameters)
	}
	if got := sc.Interventions[0]; got.Type != model.InterventionContactReduction ||
		got.Strength != 0.5 || got.Compliance != 1.0 { // adherence 缺省确定归一为 1.0
		t.Fatalf("lockdown 归一错误: %+v", got)
	}
	if got := sc.Interventions[1]; got.Type != model.InterventionIsolation ||
		got.RatePerDay != 0.3 || got.Capacity == nil || *got.Capacity != 10 {
		t.Fatalf("quarantine 归一错误: %+v", got)
	}
	// 重复解析必须得到相同结果（归一规则确定）。
	sc2, _ := Load([]byte(v1))
	if sc2.Parameters.Beta != sc.Parameters.Beta ||
		sc2.Interventions[0].Compliance != sc.Interventions[0].Compliance {
		t.Fatal("v1 归一不确定")
	}
	t.Logf("v1 归一成功: paradigm=%s beta=%.2f measures=%d (adherence 缺省->1.0)",
		sc.Paradigm, sc.Parameters.Beta, len(sc.Interventions))
}

func TestV1RejectsMissingEfficacy(t *testing.T) {
	v1 := `{"configVersion":1,"paradigm":"SIR",
"disease":{"transmission":0.4,"recovery":0.1},
"timeHorizon":20,"timeStep":1,
"population":[{"id":"a","susceptible":95,"infected":5}],"contacts":[[2]],
"measures":[{"kind":"lockdown","targets":["a"],"startTime":2,"endTime":8}]}`
	if _, err := Load([]byte(v1)); kindOf(t, err) != model.ErrConfigSyntax {
		t.Fatalf("缺少 efficacy 应拒绝: %v", err)
	}
}
