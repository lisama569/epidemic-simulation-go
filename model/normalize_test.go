package model_test

import (
	"testing"

	"github.com/lisama569/epidemic-simulation-go/model"
)

func TestNormalizeV1Legacy(t *testing.T) {
	raw := []byte(`{"version":"1","days":30,"pop_size":10000,"init_i":20,
"r0":2.5,"recovery":0.1,"lockdown_start":10,"lockdown_end":20,"lockdown_strength":0.6}`)
	sc, err := model.Normalize(raw)
	if err != nil {
		t.Fatalf("v1 config must normalize: %v", err)
	}
	t.Logf("normalized: %s", sc.String())
	if sc.SchemaVersion != 2 {
		t.Fatalf("schema version = %d, want 2", sc.SchemaVersion)
	}
	// Deterministic migration rule: beta = r0*recovery = 2.5*0.1 = 0.25.
	if sc.Beta != 0.25 {
		t.Fatalf("beta = %v, want 0.25", sc.Beta)
	}
	if sc.StepDays != 1 || sc.ModelType != "sir" || sc.Paradigm != model.ParadigmGrouped {
		t.Fatalf("unexpected normalized conventions: dt=%v type=%s paradigm=%s",
			sc.StepDays, sc.ModelType, sc.Paradigm)
	}
	if len(sc.Groups) != 1 || sc.Groups[0].Size != 10000 {
		t.Fatalf("unexpected groups: %+v", sc.Groups)
	}
	if len(sc.Interventions) != 1 {
		t.Fatalf("expected one lockdown intervention, got %d", len(sc.Interventions))
	}
	if err := sc.Validate(); err != nil {
		t.Fatalf("normalized v1 config must validate: %v", err)
	}
}

func TestNormalizeV2Passthrough(t *testing.T) {
	raw := []byte(`{"schema_version":2,"paradigm":"grouped","model_type":"sir",
"horizon_days":10,"step_days":1,"seed":3,"beta":0.2,"gamma":0.1,"gamma_q":0.1,
"groups":[{"id":"a","size":100,"init_s":99,"init_i":1}],"contacts":[[1]]}`)
	sc, err := model.Normalize(raw)
	if err != nil {
		t.Fatalf("v2 must parse: %v", err)
	}
	if sc.Seed != 3 {
		t.Fatalf("seed lost: %d", sc.Seed)
	}
}

func TestRejectUnknownVersionAndMalformed(t *testing.T) {
	if _, err := model.Normalize([]byte(`{"schema_version":9,...`)); err == nil {
		t.Fatal("malformed JSON must be rejected")
	}
	for _, raw := range []string{
		`{"schema_version":7,"paradigm":"x"}`,
		`{"version":"3","days":1}`,
		`{"paradigm":"grouped"}`,
		`not json`,
	} {
		_, err := model.Normalize([]byte(raw))
		if err == nil {
			t.Fatalf("input %q must be rejected", raw)
		}
		ve, ok := model.AsValidationError(err)
		if !ok {
			t.Fatalf("input %q: want ValidationError, got %v", raw, err)
		}
		t.Logf("input=%q -> reason=%q", raw, ve.Reason)
	}
}

func TestNormalizeV1DeterministicAcrossCalls(t *testing.T) {
	raw := []byte(`{"version":"1","days":12,"pop_size":500,"init_i":3,"r0":1.8,"recovery":0.2}`)
	a, _ := model.Normalize(raw)
	b, _ := model.Normalize(raw)
	if a.Beta != b.Beta || a.Seed != b.Seed || a.StepDays != b.StepDays {
		t.Fatal("repeated normalization must be identical")
	}
}
