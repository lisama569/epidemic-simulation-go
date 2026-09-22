package checkpoint_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/lisama569/epidemic-simulation-go/checkpoint"
	"github.com/lisama569/epidemic-simulation-go/model"
)

func TestSaveLoadClearRoundTrip(t *testing.T) {
	st := model.NewState(2, 3.5)
	st.Pop[0][model.Susceptible] = 90
	st.Pop[0][model.Infected] = 10
	st.Pop[1][model.Recovered] = 5
	snap := checkpoint.FromState(st, 7, 42)

	path := filepath.Join(t.TempDir(), "cp.json")
	s, err := checkpoint.NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(); !errors.Is(err, checkpoint.ErrNoCheckpoint) {
		t.Fatalf("empty store should return ErrNoCheckpoint, got %v", err)
	}
	if err := s.Save(snap); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.StepIndex != 7 || got.Seed != 42 || got.TimeDays != 3.5 {
		t.Fatalf("snapshot metadata mismatch: %+v", got)
	}
	back := got.ToState()
	if back.T != st.T || back.Pop[0][model.Susceptible] != 90 || back.Pop[1][model.Recovered] != 5 {
		t.Fatalf("state round-trip mismatch: %+v", back.Pop)
	}
	// Store keeps ONE latest snapshot.
	snap.StepIndex = 99
	if err := s.Save(snap); err != nil {
		t.Fatal(err)
	}
	got2, _ := s.Load()
	if got2.StepIndex != 99 {
		t.Fatal("latest snapshot was not retained")
	}
	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(); !errors.Is(err, checkpoint.ErrNoCheckpoint) {
		t.Fatalf("after clear want ErrNoCheckpoint, got %v", err)
	}
}

func TestJSONHelpersRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.json")
	payload := map[string]int{"a": 1, "b": 2}
	if err := checkpoint.SaveJSON(path, payload); err != nil {
		t.Fatal(err)
	}
	var back map[string]int
	if err := checkpoint.LoadJSON(path, &back); err != nil {
		t.Fatal(err)
	}
	if back["a"] != 1 || back["b"] != 2 {
		t.Fatalf("payload mismatch: %v", back)
	}
	if err := checkpoint.LoadJSON(filepath.Join(t.TempDir(), "missing.json"), &back); !errors.Is(err, checkpoint.ErrNoCheckpoint) {
		t.Fatalf("missing file should be ErrNoCheckpoint, got %v", err)
	}
}
