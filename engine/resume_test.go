package engine_test

import (
	"testing"

	"github.com/lisama569/epidemic-simulation-go/engine"
	"github.com/lisama569/epidemic-simulation-go/internal/testutil"
)

func TestRunFromSnapshotMatchesUninterrupted(t *testing.T) {
	sc := testutil.GroupedSEIR(31)
	sc = testutil.WithContactReduction(sc, 5, 30, 0.6)
	sc = testutil.WithVaccineCapacity(sc, 2, 40, 0.1, 100, 1)

	full, err := engine.Run(sc)
	if err != nil {
		t.Fatal(err)
	}
	// Split at an arbitrary mid point: snapshot index k contains the state
	// AFTER k steps (Trajectory[k] at time k*dt). Resume from that state.
	split := sc.NumSteps() / 3
	snapState := full.Trajectory[split]
	rest, err := engine.RunFrom(sc, split, snapState)
	if err != nil {
		t.Fatal(err)
	}
	// Compare states from split onward between uninterrupted and resumed.
	for j := 0; j < len(rest.Trajectory); j++ {
		want := full.Trajectory[split+j]
		got := rest.Trajectory[j]
		if want.T != got.T {
			t.Fatalf("time mismatch at j=%d: %.4f vs %.4f", j, want.T, got.T)
		}
		for g := range want.Pop {
			for c := range want.Pop[g] {
				if want.Pop[g][c] != got.Pop[g][c] {
					t.Fatalf("state mismatch at resumed j=%d group=%d comp=%d: %.10f vs %.10f",
						j, g, c, want.Pop[g][c], got.Pop[g][c])
				}
			}
		}
		if split+j < len(full.NewInfections) {
			for g := range want.Pop {
				if full.NewInfections[split+j][g] != rest.NewInfections[split+j][g] {
					t.Fatalf("incidence mismatch at resumed step j=%d", j)
				}
			}
		}
	}
	t.Logf("judgement: resumed trajectory (split at step %d, day %.1f) is bit-for-bit identical to uninterrupted run",
		split, snapState.T)
}
