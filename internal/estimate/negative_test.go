package estimate

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/lisama569/epidemic-simulation-go/internal/checkpoint"
	"github.com/lisama569/epidemic-simulation-go/internal/model"
	"github.com/lisama569/epidemic-simulation-go/internal/synthetic"
)

func kindOf(t *testing.T, err error) model.ErrorKind {
	t.Helper()
	var ve *model.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("期望归类错误, 实际 %T: %v", err, err)
	}
	return ve.Kind
}

func obsFrom(sc *model.Scenario, noise float64) model.ObservationSeries {
	obs, err := synthetic.GenerateObserved(sc, synthetic.GenerateOptions{
		SampleTimes: sampleTimes(), Compartment: model.CompI, NoiseSD: noise, Seed: 7,
	})
	if err != nil {
		panic(err)
	}
	return obs
}

func TestRejectParadigmMismatch(t *testing.T) {
	seir := synthetic.ReferenceScenario()
	obs := obsFrom(seir, 0)
	// 把观测口径改成 sir：与 seir 情景不兼容。
	obs.Paradigm = model.ParadigmSIR
	_, err := Run(seir, obs, Options{
		Bounds: Bounds{0.05, 0.9}, CoarseGrid: 11, RefineRounds: 2, TolBeta: 1e-3})
	if k := kindOf(t, err); k != model.ErrObservationMismatch {
		t.Fatalf("口径不兼容 got %s: %v", k, err)
	}

	// sir 情景下观测 E 仓同样不兼容。
	sir := synthetic.ReferenceScenario()
	sir.Paradigm = model.ParadigmSIR
	sir.Parameters.Sigma = 0
	eObs, err := synthetic.GenerateObserved(sir, synthetic.GenerateOptions{
		SampleTimes: sampleTimes(), Compartment: model.CompE, Seed: 1})
	if err == nil {
		t.Fatal("生成 E 仓观测在 sir 下本应失败")
	}
	t.Logf("口径不兼容被拒绝: %v；sir 下 E 观测: %v", err, eObs)
}

func TestNonConvergenceReportedHonestly(t *testing.T) {
	sc := synthetic.ReferenceScenario()
	obs := obsFrom(sc, 25) // 大噪声：剖面平坦，网格无法稳定
	est, err := Run(sc, obs, Options{
		Bounds: Bounds{0.05, 0.9}, CoarseGrid: 11, RefineRounds: 2, TolBeta: 1e-9, Parallelism: 4})
	if err != nil {
		t.Fatalf("未收敛是结果而非 Go 错误: %v", err)
	}
	if est.Converged {
		t.Fatal("过严阈值 + 少轮数不应判定收敛")
	}
	if est.Reason == "" {
		t.Fatal("未收敛必须给出原因")
	}
	t.Logf("关键输入: noise=25 coarse=11 rounds=2 tol=1e-9 seed=7; "+
		"如实报告未收敛 betaHat=%.4f 原因=%s", est.BetaHat, est.Reason)
}

func TestFeasibleDomainViolationReported(t *testing.T) {
	sc := synthetic.ReferenceScenario() // 真值 beta=0.32
	obs := obsFrom(sc, 0)
	// 可行域上界远低于真值：最优点必贴上界，剖面被截断。
	est, err := Run(sc, obs, Options{
		Bounds: Bounds{0.01, 0.12}, CoarseGrid: 11, RefineRounds: 3, TolBeta: 1e-6})
	if k := kindOf(t, err); k != model.ErrFeasibleDomain {
		t.Fatalf("越界应返回 ErrFeasibleDomain, got %v (%s)", err, k)
	}
	if est == nil || est.Converged {
		t.Fatal("越界结果必须存在且不得标记收敛")
	}
	t.Logf("可行域 [0.01,0.12] 过窄，如实拒绝: %s", est.Reason)
}

func TestCheckpointDigestMismatchRejected(t *testing.T) {
	sc := synthetic.ReferenceScenario()
	obs := obsFrom(sc, 0)
	dir := filepath.Join(t.TempDir(), "cp")
	first := Options{Bounds: Bounds{0.05, 0.9}, CoarseGrid: 21, RefineRounds: 6,
		TolBeta: 5e-4, CheckpointDir: dir, MaxCompletedRounds: 2}
	if _, err := Run(sc, obs, first); err != nil {
		t.Fatal(err)
	}
	// 用不同情景（改 beta 真值不影响摘要？情景参与摘要——改 horizon）。
	other := *sc
	other.HorizonDays = 30
	cp, err := checkpoint.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := cp.Verify(&other, checkpoint.Digest(stripCheckpointDir(first)), obs); err == nil {
		t.Fatal("摘要不匹配的情景必须拒绝恢复")
	} else {
		var ve *model.ValidationError
		if !errors.As(err, &ve) || ve.Kind != model.ErrCheckpoint {
			t.Fatalf("应归类 ErrCheckpoint: %v", err)
		}
		t.Logf("检查点篡改被拒绝: %v", err)
	}
}
