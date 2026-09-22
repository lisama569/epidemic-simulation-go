package estimate_test

import (
	"sync"
	"testing"

	"github.com/lisama569/epidemic-simulation-go/estimate"
	"github.com/lisama569/epidemic-simulation-go/internal/testutil"
	"github.com/lisama569/epidemic-simulation-go/synthetic"
)

// TestEstimateStableUnderConcurrentCalls runs many estimations concurrently
// (each with its own scenario/options copies) and requires identical answers.
func TestEstimateStableUnderConcurrentCalls(t *testing.T) {
	base := testutil.GroupedSEIR(321)
	obs, err := synthetic.Generate(base)
	if err != nil {
		t.Fatal(err)
	}
	const n = 16
	var wg sync.WaitGroup
	results := make([]struct {
		beta, lo, hi float64
		status       estimate.ConvergenceStatus
	}, n)
	errs := make([]error, n)
	t.Logf("key inputs: seed=%d true_beta=%.3f concurrent_estimations=%d", base.Seed, base.Beta, n)
	for w := 1; w <= 4; w++ {
		sem := make(chan struct{}, w)
		for i := 0; i < n; i++ {
			wg.Add(1)
			sem <- struct{}{}
			go func(i int) {
				defer wg.Done()
				defer func() { <-sem }()
				sc := testutil.GroupedSEIR(321)
				r, err := estimate.Estimate(sc, obs, estimate.Options{
					BetaLow: 0.05, BetaHigh: 1.0, Tolerance: 1e-6, MaxIterations: 100,
				})
				errs[i] = err
				if r != nil {
					results[i] = struct {
						beta, lo, hi float64
						status       estimate.ConvergenceStatus
					}{r.BetaHat, r.CI95Lower, r.CI95Upper, r.Status}
				}
			}(i)
		}
		wg.Wait()
		for i := 0; i < n; i++ {
			if errs[i] != nil {
				t.Fatalf("worker-width %d run %d failed: %v", w, i, errs[i])
			}
			if results[i].beta != results[0].beta ||
				results[i].lo != results[0].lo || results[i].hi != results[0].hi ||
				results[i].status != results[0].status {
				t.Fatalf("worker-width %d: run %d = %+v, run 0 = %+v", w, i, results[i], results[0])
			}
		}
		t.Logf("judgement: %d concurrent estimations identical with max-parallel=%d", n, w)
	}
}
