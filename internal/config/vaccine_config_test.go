package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lisama569/epidemic-simulation-go/internal/engine"
	"github.com/lisama569/epidemic-simulation-go/internal/intervention"
	"github.com/lisama569/epidemic-simulation-go/internal/model"
)

// TestV2VaccinationStrictParse v2 接种配置严格解析：未知字段拒绝、
// 合法配置加载后可直接推演，且额度池字段不被静默丢弃。
func TestV2VaccinationStrictParse(t *testing.T) {
	v2 := `{
 "configVersion": 2, "paradigm": "sir",
 "parameters": {"beta": 0.3, "gamma": 0.1, "sigma": 0},
 "horizonDays": 20, "stepDays": 0.5,
 "groups": [{"id":"a","s0":900,"i0":5},{"id":"b","s0":400}],
 "contactMatrix": [[2,0.2],[0.2,2]],
 "vaccineQuotas": [
   {"id":"daily","dosesPerDay":120},
   {"id":"saved","dosesPerDay":50,"accumulate":true}
 ],
 "interventions": [
   {"type":"vaccination","groups":["a"],"startDays":2,"endDays":12,
    "dosesPerDay":100,"compliance":0.9,"quotaId":"daily","priority":1},
   {"type":"vaccination","groups":["b"],"startDays":2,"endDays":12,
    "dosesPerDay":60,"compliance":1,"quotaId":"saved","priority":2}
 ]}`
	sc, err := Load([]byte(v2))
	if err != nil {
		t.Fatalf("合法 v2 接种配置被拒绝: %v", err)
	}
	if len(sc.VaccineQuotas) != 2 || sc.VaccineQuotas[1].Accumulate != true {
		t.Fatalf("额度池解析错误: %+v", sc.VaccineQuotas)
	}
	if sc.Interventions[0].DosesPerDay != 100 ||
		sc.Interventions[0].QuotaID != "daily" || sc.Interventions[0].Priority != 1 {
		t.Fatalf("接种干预字段解析错误: %+v", sc.Interventions[0])
	}
	tr, err := engine.Run(sc, nil)
	if err != nil {
		t.Fatalf("加载后推演失败: %v", err)
	}
	// a: 100*0.9=90/day，额度120/天充足，理论上限 900；感染会同步消耗 S，
	// 实际 V 低于 900 但应是主要去向（接种速率远高于感染）。
	va := tr.V[0][len(tr.V[0])-1]
	if va < 500 || va > 900+1e-6 {
		t.Fatalf("a 组最终 V=%.3f 不在合理区间 (500,900]", va)
	}
	t.Logf("关键输入: seed=0(确定性推演) quota daily=120/saved=50(累积), "+
		"a 90/day(compliance0.9) b 60/day；判定: 严格解析通过并可推演，a 最终V=%.2f", va)

	// 严格模式：额度池内拼错字段必须拒绝。
	bad := []byte(`{
 "configVersion": 2, "paradigm": "sir",
 "parameters": {"beta": 0.3, "gamma": 0.1, "sigma": 0},
 "horizonDays": 20, "stepDays": 0.5,
 "groups": [{"id":"a","s0":100,"i0":1}], "contactMatrix": [[2]],
 "vaccineQuotas": [{"id":"p","dosesPerDayy":10}],
 "interventions": [
   {"type":"vaccination","groups":["a"],"startDays":2,"endDays":10,
    "dosesPerDay":10,"compliance":1,"quotaId":"p","priority":1}]}`)
	if _, err := Load(bad); err == nil {
		t.Fatal("额度池内的未知字段 dosesperday 必须被严格拒绝")
	} else if k := kindOf(t, err); k != model.ErrConfigSyntax {
		t.Fatalf("未知字段错误归类=%s 期望 config_syntax_invalid: %v", k, err)
	}

	// 接种干预里拼错字段同样拒绝。
	bad2 := []byte(`{
 "configVersion": 2, "paradigm": "sir",
 "parameters": {"beta": 0.3, "gamma": 0.1, "sigma": 0},
 "horizonDays": 20, "stepDays": 0.5,
 "groups": [{"id":"a","s0":100,"i0":1}], "contactMatrix": [[2]],
 "vaccineQuotas": [{"id":"p","dosesPerDay":10}],
 "interventions": [
   {"type":"vaccination","groups":["a"],"startDays":2,"endDays":10,
    "dosesPerDay":10,"compliance":1,"quotaId":"p","priorty":1}]}`)
	if _, err := Load(bad2); kindOf(t, err) != model.ErrConfigSyntax {
		t.Fatalf("接种干预未知字段应拒绝: %v", err)
	}
}

// TestV1LegacyUnaffectedByVaccine 旧 v1 配置不含任何接种概念，必须按原
// 归一规则照常加载，VaccineQuotas 为空，且推演轨迹中 V 恒为 0。
func TestV1LegacyUnaffectedByVaccine(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "scenario-v1-legacy.json"))
	if err != nil {
		t.Fatal(err)
	}
	sc, err := Load(b)
	if err != nil {
		t.Fatalf("v1 旧配置应正常归一加载: %v", err)
	}
	if len(sc.VaccineQuotas) != 0 {
		t.Fatalf("v1 归一后不应出现额度池: %+v", sc.VaccineQuotas)
	}
	for _, iv := range sc.Interventions {
		if iv.QuotaID != "" || iv.DosesPerDay != 0 {
			t.Fatalf("v1 归一干预不应带接种字段: %+v", iv)
		}
	}
	sched, err := intervention.Build(sc)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := engine.Run(sc, sched)
	if err != nil {
		t.Fatalf("v1 情景推演失败: %v", err)
	}
	for g := range tr.GroupIDs {
		for k, v := range tr.V[g] {
			if v != 0 {
				t.Fatalf("g=%d k=%d 旧配置轨迹 V=%v 必须恒为 0", g, k, v)
			}
		}
	}
	t.Log("判定依据: v1 配置无疫苗字段，归一规则与引擎行为不变，V 仓恒 0，" +
		"新增接种能力没有让旧配置报错")
}
