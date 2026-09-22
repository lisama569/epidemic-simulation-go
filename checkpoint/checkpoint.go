// Package checkpoint provides bounded, resumable run snapshots.
//
// A Store keeps exactly ONE latest snapshot per scenario (a single JSON
// file replaced atomically), so disk and memory usage stay O(1) when the
// scenario size or horizon grows: resuming never replays an unbounded log.
package checkpoint

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/lisama569/epidemic-simulation-go/model"
)

// ErrNoCheckpoint reports that no snapshot exists yet.
var ErrNoCheckpoint = errors.New("checkpoint: no snapshot stored")

// SnapshotVersion is the on-disk snapshot schema version.
const SnapshotVersion = 1

// Snapshot is one resumable point in a deterministic run.
type Snapshot struct {
	SchemaVersion int         `json:"schema_version"`
	Seed          int64       `json:"seed"`
	StepIndex     int         `json:"step_index"`
	TimeDays      float64     `json:"time_days"`
	State         [][]float64 `json:"state"`
}

// FromState captures a snapshot of st.
func FromState(st *model.State, stepIndex int, seed int64) *Snapshot {
	pop := make([][]float64, len(st.Pop))
	for i := range st.Pop {
		pop[i] = append([]float64(nil), st.Pop[i]...)
	}
	return &Snapshot{
		SchemaVersion: SnapshotVersion,
		Seed:          seed,
		StepIndex:     stepIndex,
		TimeDays:      st.T,
		State:         pop,
	}
}

// ToState rebuilds a model.State from the snapshot.
func (s *Snapshot) ToState() *model.State {
	st := model.NewState(len(s.State), s.TimeDays)
	for g := range s.State {
		copy(st.Pop[g], s.State[g])
	}
	return st
}

// Store keeps exactly one latest snapshot (bounded storage).
type Store struct {
	path string
}

// NewStore opens or creates a checkpoint store at path.
func NewStore(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("checkpoint: empty path")
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	return &Store{path: path}, nil
}

// Save atomically replaces the stored snapshot (write temp file + rename).
func (s *Store) Save(snap *Snapshot) error {
	if snap == nil {
		return errors.New("checkpoint: cannot save nil snapshot")
	}
	snap.SchemaVersion = SnapshotVersion
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Load reads the stored snapshot, returning ErrNoCheckpoint when absent.
func (s *Store) Load() (*Snapshot, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoCheckpoint
	}
	if err != nil {
		return nil, err
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, err
	}
	if snap.SchemaVersion != SnapshotVersion {
		return nil, errors.New("checkpoint: unsupported snapshot schema version")
	}
	return &snap, nil
}

// Clear removes any stored snapshot.
func (s *Store) Clear() error {
	err := os.Remove(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// SaveJSON atomically writes v as indented JSON to path (temp file + rename).
func SaveJSON(path string, v any) error {
	if path == "" {
		return errors.New("checkpoint: empty path")
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadJSON reads JSON written by SaveJSON, returning ErrNoCheckpoint if the
// file is absent.
func LoadJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ErrNoCheckpoint
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
