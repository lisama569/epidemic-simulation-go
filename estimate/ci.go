package estimate

// ciThresholdDelta is the log-likelihood ratio threshold for a 95%
// profile-likelihood interval with one estimated parameter:
// 0.5 * chi^2_{0.95, df=1} = 0.5 * 3.841459 = 1.920729.
const ciThresholdDelta = 1.920729

// ciResult is a profile-likelihood confidence bound.
type ciResult struct {
	bound   float64
	clipped bool // true if the threshold is never reached inside the bracket
	reason  string
}

// findBound bisects [lo,hi] for the point where NLL crosses the target level.
// The caller guarantees betaHat lies inside [lo,hi] and the NLL decreases
// toward betaHat on each side, so on the lower side f(lo) >= target and
// f(hi) <= target hold whenever the threshold is reached inside the bracket.
//
// If the feasible-domain edge never reaches the threshold, the bound is
// reported as clipped at that edge rather than presenting a false finite
// bound; the orchestrator flags the whole result out-of-feasible-domain.
func findBound(lo, hi, target, tol float64, maxIter int, f evalFn, cache map[float64]float64) ciResult {
	score := func(x float64) (float64, error) {
		if v, ok := cache[x]; ok {
			return v, nil
		}
		v, _, err := f(x)
		if err != nil {
			return 0, err
		}
		cache[x] = v
		return v, nil
	}
	edgeLow, err := score(lo)
	if err != nil {
		return ciResult{clipped: true, reason: "evaluation error at domain edge: " + err.Error()}
	}
	edgeHigh, err := score(hi)
	if err != nil {
		return ciResult{clipped: true, reason: "evaluation error at domain edge: " + err.Error()}
	}
	// No crossing inside the bracket when both edge levels are still below
	// the target: the uncertainty extends beyond the feasible region.
	if edgeLow < target && edgeHigh < target {
		return ciResult{bound: hi, clipped: true,
			reason: "NLL never reaches the 95% threshold within the feasible bracket; bound clipped to domain edge"}
	}
	a, b := lo, hi
	fa := edgeLow
	for i := 0; i < maxIter; i++ {
		m := (a + b) / 2
		fm, err := score(m)
		if err != nil {
			return ciResult{clipped: true, reason: "evaluation error during bisection: " + err.Error()}
		}
		if (fa-target)*(fm-target) <= 0 {
			b = m
		} else {
			a, fa = m, fm
		}
		if b-a <= tol {
			return ciResult{bound: (a + b) / 2}
		}
	}
	return ciResult{bound: (a + b) / 2, clipped: true,
		reason: "bisection reached iteration limit; bound approximate"}
}
