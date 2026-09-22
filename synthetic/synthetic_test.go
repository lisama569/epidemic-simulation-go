package synthetic_test

import (
	"testing"

	"github.com/lisama569/epidemic-simulation-go/internal/testutil"
	"github.com/lisama569/epidemic-simulation-go/synthetic"
)

func TestGenerateDeterministic(t *testing.T) {
	sc := testutil.GroupedSEIR(20260923)
	t.Logf("synthetic input: %s", sc.String())
	a, err := synthetic.Generate(sc)
	if err != nil {
		t.Fatal(err)
	}
	for k := 0; k < 3; k++ {
		b, err := synthetic.Generate(sc)
		if err != nil {
			t.Fatal(err)
		}
		if len(a.Records) != len(b.Records) {
			t.Fatal("length differs")
		}
		for i := range a.Records {
			if a.Records[i] != b.Records[i] {
				t.Fatalf("seed=%d must reproduce identical observations, mismatch at record %d", sc.Seed, i)
			}
		}
	}
	if a.Seed != sc.Seed {
		t.Fatalf("seed not recorded: %d", a.Seed)
	}
}

func TestDifferentSeedsDiffer(t *testing.T) {
	a, _ := synthetic.Generate(testutil.GroupedSEIR(1))
	b, _ := synthetic.Generate(testutil.GroupedSEIR(2))
	t.Logf("key inputs: seeds 1 vs 2, records=%d; judgement: series must differ somewhere", len(a.Records))
	same := true
	for i := range a.Records {
		if a.Records[i].NewInfected != b.Records[i].NewInfected {
			same = false
			break
		}
	}
	if same {
		t.Fatal("distinct seeds produced identical series (suspicious)")
	}
}

func TestObservationsAreNonNegative(t *testing.T) {
	for _, seed := range []int64{1, 2, 3, 999} {
		t.Logf("key input: seed=%d; judgement: every observation must be >= 0", seed)
		seq, err := synthetic.Generate(testutil.GroupedSEIR(seed))
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range seq.Records {
			if r.NewInfected < 0 {
				t.Fatalf("negative observation %v for seed %d", r, seed)
			}
		}
	}
}
