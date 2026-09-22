package estimate_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lisama569/epidemic-simulation-go/estimate"
	"github.com/lisama569/epidemic-simulation-go/internal/testutil"
	"github.com/lisama569/epidemic-simulation-go/model"
	"github.com/lisama569/epidemic-simulation-go/synthetic"
)

func TestEstimateConvergesNearTruth(t *testing.T) {
	sc := testutil.GroupedSEIR(20260923)
	obs, err := synthetic.Generate(sc)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("key inputs: seed=%d true_beta=%.4f n_obs=%d bracket=[0.05,1.0] tol=1e-5",
		sc.Seed, sc.Beta, len(obs.Records))

	res, err := estimate.Estimate(sc, obs, estimate.Options{
		BetaLow: 0.05, BetaHigh: 1.0, Tolerance: 1e-5, MaxIterations: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("judgement: status=%s beta_hat=%.6f CI=[%.6f,%.6f] iters=%d nll=%.3f\n  %s",
		res.Status, res.BetaHat, res.CI95Lower, res.CI95Upper, res.Iterations, res.FinalNLL, res.Why)
	if res.Status != estimate.Converged {
		t.Fatalf("expected converged, got %s: %s", res.Status, res.Why)
	}
	if abs(res.BetaHat-sc.Beta) > 0.03 {
		t.Fatalf("beta_hat %.4f too far from truth %.4f", res.BetaHat, sc.Beta)
	}
	if !(res.CI95Lower <= sc.Beta && sc.Beta <= res.CI95Upper) {
		t.Fatalf("true beta %.4f outside 95%% CI [%.6f,%.6f]", sc.Beta, res.CI95Lower, res.CI95Upper)
	}
	if !(res.CI95Lower <= res.BetaHat && res.BetaHat <= res.CI95Upper) {
		t.Fatal("beta_hat outside its own CI")
	}
}

func TestEstimateDeterministicAcrossRuns(t *testing.T) {
	sc := testutil.GroupedSEIR(55)
	obs, _ := synthetic.Generate(sc)
	opts := estimate.Options{BetaLow: 0.05, BetaHigh: 1.0, Tolerance: 1e-6, MaxIterations: 100}
	a, err := estimate.Estimate(sc, obs, opts)
	if err != nil {
		t.Fatal(err)
	}
	for k := 0; k < 3; k++ {
		b, err := estimate.Estimate(sc, obs, opts)
		if err != nil {
			t.Fatal(err)
		}
		if a.BetaHat != b.BetaHat || a.CI95Lower != b.CI95Lower || a.CI95Upper != b.CI95Upper {
			t.Fatalf("estimation not deterministic: %+v vs %+v", a, b)
		}
	}
}

func TestNotConvergedWhenIterationBudgetTooSmall(t *testing.T) {
	sc := testutil.GroupedSEIR(8)
	obs, _ := synthetic.Generate(sc)
	t.Logf("key inputs: seed=%d beta=%.3f max_iterations=2 (deliberately too small)", sc.Seed, sc.Beta)
	res, err := estimate.Estimate(sc, obs, estimate.Options{
		BetaLow: 0.001, BetaHigh: 5, Tolerance: 1e-8, MaxIterations: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("judgement: status=%s iters=%d beta_hat=%.6f\n  %s",
		res.Status, res.Iterations, res.BetaHat, res.Why)
	if res.Status != estimate.NotConvergedMaxIter {
		t.Fatalf("expected not_converged_max_iterations, got %s", res.Status)
	}
	if res.Why == "" {
		t.Fatal("non-convergence must be explained")
	}
}

func TestIncompatibleObservationParadigm(t *testing.T) {
	sc := testutil.GroupedSEIR(8)
	other := testutil.MetapopSIR(8)
	obs, err := synthetic.Generate(other)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("incompatible input: scenario paradigm=%s vs observations paradigm=%s",
		sc.Paradigm, obs.Paradigm)
	res, err := estimate.Estimate(sc, obs, estimate.Options{BetaLow: 0.05, BetaHigh: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != estimate.IncompatibleObs {
		t.Fatalf("expected incompatible_observations, got %s: %s", res.Status, res.Why)
	}
	t.Logf("judgement: %s", res.Why)
}

func TestOutOfFeasibleDomainAtBracketEdge(t *testing.T) {
	sc := testutil.GroupedSEIR(8)
	obs, _ := synthetic.Generate(sc)
	// A bracket entirely below the true/likely optimum: the minimizer piles up
	// at the upper edge and must be flagged out-of-domain, not "converged".
	res, err := estimate.Estimate(sc, obs, estimate.Options{
		BetaLow: 0.001, BetaHigh: 0.01, Tolerance: 1e-6, MaxIterations: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("judgement: status=%s beta_hat=%.6f bracket=[%g,%g]\n  %s",
		res.Status, res.BetaHat, res.BracketLow, res.BracketHigh, res.Why)
	if res.Status != estimate.OutOfFeasibleDomain {
		t.Fatalf("expected out_of_feasible_domain, got %s", res.Status)
	}
}

func TestResumeMatchesUninterrupted(t *testing.T) {
	sc := testutil.GroupedSEIR(77)
	obs, _ := synthetic.Generate(sc)
	cpPath := filepath.Join(t.TempDir(), "estimate-cp.json")
	opts := estimate.Options{BetaLow: 0.05, BetaHigh: 1.0, Tolerance: 1e-6, MaxIterations: 100,
		CheckpointPath: cpPath}

	// Segment 1: deliberately stop early.
	seg1 := opts
	seg1.MaxIterations = 3
	r1, err := estimate.Estimate(sc, obs, seg1)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("segment1: seed=%d status=%s iters=%d resumed=%v", sc.Seed, r1.Status, r1.Iterations, r1.Resumed)
	if r1.Status == estimate.Converged {
		t.Fatal("segment 1 should not converge in 3 iterations")
	}

	// Segment 2: resume with full budget.
	r2, err := estimate.Estimate(sc, obs, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("segment2: status=%s iters=%d resumed=%v beta_hat=%.8f",
		r2.Status, r2.Iterations, r2.Resumed, r2.BetaHat)
	if !r2.Resumed {
		t.Fatal("segment 2 must report that it resumed from a checkpoint")
	}

	// Reference: uninterrupted run, no checkpoint.
	ref := opts
	ref.CheckpointPath = ""
	r3, err := estimate.Estimate(sc, obs, ref)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("reference: status=%s iters=%d beta_hat=%.8f CI=[%.8f,%.8f]",
		r3.Status, r3.Iterations, r3.BetaHat, r3.CI95Lower, r3.CI95Upper)
	if r2.Status != r3.Status || r2.BetaHat != r3.BetaHat ||
		r2.CI95Lower != r3.CI95Lower || r2.CI95Upper != r3.CI95Upper ||
		r2.Iterations != r3.Iterations {
		t.Fatalf("resumed run differs from uninterrupted: %+v vs %+v", r2, r3)
	}
}

func TestCheckpointBoundedSize(t *testing.T) {
	small := testutil.GroupedSEIR(77)
	obsSmall, err := synthetic.Generate(small)
	if err != nil {
		t.Fatal(err)
	}
	// Same estimation problem, but with 1000x population: checkpoints must
	// stay essentially identical in size because they hold only optimizer
	// state, never the population vector.
	big := testutil.GroupedSEIR(77)
	for g := range big.Groups {
		big.Groups[g].Size *= 1000
		big.Groups[g].InitS *= 1000
		big.Groups[g].InitI *= 1000
	}
	obsBig, err := synthetic.Generate(big)
	if err != nil {
		t.Fatal(err)
	}
	run := func(sc *model.Scenario, obs *synthetic.Sequence, path string, maxIter int) {
		t.Helper()
		// Stop deliberately un-converged so a checkpoint file is left behind.
		_, err := estimate.Estimate(sc, obs, estimate.Options{
			BetaLow: 0.05, BetaHigh: 1.0, Tolerance: 1e-12, MaxIterations: maxIter,
			CheckpointPath: path,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	pSmall := filepath.Join(t.TempDir(), "small.json")
	pBig := filepath.Join(t.TempDir(), "big.json")
	run(small, obsSmall, pSmall, 3)
	run(big, obsBig, pBig, 3)
	fiSmall, _ := os.Stat(pSmall)
	fiBig, _ := os.Stat(pBig)
	t.Logf("checkpoint sizes: small_pop=%d bytes, big_pop=%d bytes", fiSmall.Size(), fiBig.Size())
	if fiSmall.Size() <= 0 || fiBig.Size() <= 0 {
		t.Fatal("expected non-empty checkpoint files")
	}
	// Optimizer-state-only checkpoints: at most 30% slack between sizes,
	// regardless of a 1000x population difference.
	ratio := float64(fiBig.Size()) / float64(fiSmall.Size())
	if ratio > 1.3 {
		t.Fatalf("checkpoint grows with population: ratio %.2f", ratio)
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
