package synthetic

import (
	"testing"

	"github.com/lisama569/epidemic-simulation-go/internal/model"
)

func TestGenerationDeterministicBySeed(t *testing.T) {
	sc := ReferenceScenario()
	times := func() []float64 {
		ts := []float64{}
		for x := 1.0; x <= 30; x += 1 {
			ts = append(ts, x)
		}
		return ts
	}()
	mk := func(seed int64) model.ObservationSeries {
		obs, err := GenerateObserved(sc, GenerateOptions{
			SampleTimes: times, Compartment: model.CompI, NoiseSD: 5, Seed: seed})
		if err != nil {
			t.Fatal(err)
		}
		return obs
	}
	a, b, c := mk(42), mk(42), mk(7)
	anyDiff := false
	for i := range a.Observations {
		if a.Observations[i].Value != b.Observations[i].Value {
			t.Fatalf("同种子结果不一致 i=%d", i)
		}
		if a.Observations[i].Value != c.Observations[i].Value {
			anyDiff = true
		}
	}
	if !anyDiff {
		t.Fatal("不同种子应至少产生一个不同观测值")
	}
	// 非负性
	for _, o := range a.Observations {
		if o.Value < 0 {
			t.Fatalf("观测出现负值: %+v", o)
		}
	}
	t.Logf("关键输入: seed=42 noiseSD=5 n=%d；同种子逐位一致、异种子不同，观测均非负",
		len(a.Observations))
}

func TestRejectOffGridSampleTime(t *testing.T) {
	sc := ReferenceScenario()
	_, err := GenerateObserved(sc, GenerateOptions{
		SampleTimes: []float64{0.37}, Compartment: model.CompI, Seed: 1})
	if err == nil {
		t.Fatal("非网格抽样时间必须拒绝")
	}
	t.Logf("非网格抽样点被拒绝: %v", err)
}
