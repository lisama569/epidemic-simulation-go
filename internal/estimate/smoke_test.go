package estimate

import (
	"path/filepath"
	"testing"

	"github.com/lisama569/epidemic-simulation-go/internal/model"
	"github.com/lisama569/epidemic-simulation-go/internal/synthetic"
)

func sampleTimes() []float64 {
	ts := make([]float64, 0, 21)
	for t := 2.0; t <= 40; t += 2 {
		ts = append(ts, t)
	}
	return ts
}

func optsFor(dir string, p int) Options {
	return Options{
		Bounds:     Bounds{BetaMin: 0.05, BetaMax: 0.9},
		CoarseGrid: 41, RefineRounds: 8, TolBeta: 5e-4,
		Parallelism: p, CheckpointDir: dir,
	}
}

func TestSmokeEstimate(t *testing.T) {
	sc := synthetic.ReferenceScenario()
	obs, err := synthetic.GenerateObserved(sc, synthetic.GenerateOptions{
		SampleTimes: sampleTimes(), Compartment: model.CompI, NoiseSD: 0, Seed: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	est1, err := Run(sc, obs, optsFor("", 1))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("serial: betaHat=%.8f interval=[%.6f,%.6f] converged=%v shift=%.2e sse=%.4f reason=%s",
		est1.BetaHat, est1.Lower, est1.Upper, est1.Converged, est1.FinalShift, est1.BestSSE, est1.Reason)
	if !est1.Converged {
		t.Fatalf("expected convergence: %s", est1.Reason)
	}
	if d := est1.BetaHat - sc.Parameters.Beta; d > 0.02 || d < -0.02 {
		t.Fatalf("betaHat %.6f too far from true %.6f", est1.BetaHat, sc.Parameters.Beta)
	}
	if !(est1.Lower <= est1.BetaHat && est1.BetaHat <= est1.Upper) {
		t.Fatalf("betaHat outside interval")
	}

	// 并行度变化必须逐位一致。
	est8, err := Run(sc, obs, optsFor("", 8))
	if err != nil {
		t.Fatal(err)
	}
	if est8.BetaHat != est1.BetaHat || est8.BestSSE != est1.BestSSE ||
		est8.Lower != est1.Lower || est8.Upper != est1.Upper {
		t.Fatalf("parallelism changed result: %+v vs %+v", est8, est1)
	}

	// 检查点续算：在不同的提前终止轮数下中断再恢复，最终结果必须一致。
	for stop := 1; stop <= 8; stop++ {
		dir := filepath.Join(t.TempDir(), "cp")
		partial := optsFor(dir, 1)
		partial.MaxCompletedRounds = stop // 先跑 stop 轮后中断
		e0, err := Run(sc, obs, partial)
		if err != nil {
			t.Fatalf("partial run stop=%d: %v", stop, err)
		}
		if e0.Converged {
			t.Fatalf("stop=%d: 中断运行不应报告收敛", stop)
		}
		full := optsFor(dir, 4)
		estR, err := Run(sc, obs, full)
		if err != nil {
			t.Fatalf("resume stop=%d: %v", stop, err)
		}
		if !estR.CheckpointRestored {
			t.Fatalf("stop=%d: expected restored flag", stop)
		}
		if estR.BetaHat != est1.BetaHat || estR.BestSSE != est1.BestSSE {
			t.Fatalf("stop=%d resume mismatch: %.10f vs %.10f", stop, estR.BetaHat, est1.BetaHat)
		}
	}
}
