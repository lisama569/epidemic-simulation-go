package config

import (
	"os"
	"testing"

	"github.com/lisama569/epidemic-simulation-go/internal/model"
	"github.com/lisama569/epidemic-simulation-go/internal/validate"
)

// TestV2VaccinationParsing 验证 v2 配置中的接种字段被严格解析到模型。
func TestV2VaccinationParsing(t *testing.T) {
	v2 := `{"configVersion":2,"paradigm":"sir",
"parameters":{"beta":0.3,"gamma":0.1,"sigma":0},
"horizonDays":10,"stepDays":0.5,
"groups":[{"id":"a","s0":990,"i0":10}],
"contactMatrix":[[2]],
"vaccineQuotas":[{"id":"nat","dailyLimit":50,"startDays":0,"endDays":10,"accumulate":true}],
"interventions":[
  {"type":"vaccination","groups":["a"],"startDays":1,"endDays":9,
   "ratePerDay":0.2,"compliance":1.0,"quotaId":"nat","priority":7}
]}`
	sc, err := Load([]byte(v2))
	if err != nil {
		t.Fatal(err)
	}
	q := sc.VaccineQuotas[0]
	if q.ID != "nat" || q.DailyLimit != 50 || !q.Accumulate {
		t.Fatalf("额度池解析错误: %+v", q)
	}
	iv := sc.Interventions[0]
	if iv.Type != model.InterventionVaccination || iv.QuotaID != "nat" || iv.Priority != 7 {
		t.Fatalf("接种干预解析错误: %+v", iv)
	}
	if err := validate.Scenario(sc); err != nil {
		t.Fatalf("解析后情景应通过校验: %v", err)
	}
	t.Logf("v2 接种配置解析成功: quota=%+v intervention.priority=%d", q, iv.Priority)
}

// TestV2RejectsUnknownVaccinationField 验证严格解析对接种字段同样生效：
// 拼写错误的字段必须被拒绝而不是静默忽略。
func TestV2RejectsUnknownVaccinationField(t *testing.T) {
	v2 := `{"configVersion":2,"paradigm":"sir",
"parameters":{"beta":0.3,"gamma":0.1,"sigma":0},
"horizonDays":10,"stepDays":0.5,
"groups":[{"id":"a","s0":990,"i0":10}],
"contactMatrix":[[2]],
"vaccineQuotas":[{"id":"nat","dailyLimit":50,"startDays":0,"endDays":10}],
"interventions":[
  {"type":"vaccination","groups":["a"],"startDays":1,"endDays":9,
   "ratePerDay":0.2,"compliance":1.0,"quotaId":"nat","priorty":7}
]}`
	if _, err := Load([]byte(v2)); kindOf(t, err) != model.ErrConfigSyntax {
		t.Fatalf("未知字段 priorty 应拒绝: %v", err)
	}
	t.Log("拼写错误的 priorty 字段被严格解析拒绝 kind=config_syntax_invalid")
}

// TestLegacyFilesStillLoad 确认旧配置文件（v1 与不含接种的 v2）
// 在引入接种能力后仍按原归一规则正常加载。
func TestLegacyFilesStillLoad(t *testing.T) {
	for _, path := range []string{
		"../../testdata/scenario-v1-legacy.json",
		"../../testdata/scenario-v2.json",
	} {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("读取 %s: %v", path, err)
		}
		sc, err := Load(b)
		if err != nil {
			t.Fatalf("旧配置 %s 加载失败: %v", path, err)
		}
		if err := validate.Scenario(sc); err != nil {
			t.Fatalf("旧配置 %s 校验失败: %v", path, err)
		}
		if len(sc.VaccineQuotas) != 0 {
			t.Fatalf("旧配置 %s 不应产生额度池", path)
		}
		t.Logf("旧配置 %s 正常加载: version=%d groups=%d interventions=%d",
			path, sc.ConfigVersion, len(sc.Groups), len(sc.Interventions))
	}
}

// TestVaccinationScenarioFileLoads 验证新增的接种示例配置可加载并通过校验。
func TestVaccinationScenarioFileLoads(t *testing.T) {
	b, err := os.ReadFile("../../testdata/scenario-v2-vaccination.json")
	if err != nil {
		t.Fatal(err)
	}
	sc, err := Load(b)
	if err != nil {
		t.Fatal(err)
	}
	if err := validate.Scenario(sc); err != nil {
		t.Fatalf("接种示例配置校验失败: %v", err)
	}
	nVax := 0
	for _, iv := range sc.Interventions {
		if iv.Type == model.InterventionVaccination {
			nVax++
		}
	}
	if nVax != 2 || len(sc.VaccineQuotas) != 1 {
		t.Fatalf("示例配置解析不完整: vaccinations=%d quotas=%d", nVax, len(sc.VaccineQuotas))
	}
	t.Logf("接种示例配置加载成功: quotas=%d vaccinations=%d", len(sc.VaccineQuotas), nVax)
}
