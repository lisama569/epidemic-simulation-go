package estimate

import "github.com/lisama569/epidemic-simulation-go/checkpoint"

// cacheEntry is one already-evaluated (beta -> NLL) pair.
type cacheEntry struct {
	X float64 `json:"x"`
	F float64 `json:"f"`
}

// estimateCheckpoint is the persisted optimization state. It stores only the
// four-point golden-section state plus a flat evaluation cache, so its size
// depends on iterations for a fixed tolerance (bounded) and never on the
// scenario population or horizon length.
type estimateCheckpoint struct {
	State     optimizerState `json:"state"`
	Cache     []cacheEntry   `json:"cache"`
	Completed bool           `json:"completed"`
	BetaLow   float64        `json:"beta_low"`
	BetaHigh  float64        `json:"beta_high"`
}

type cpStore interface {
	save(v *estimateCheckpoint) error
	load() (*estimateCheckpoint, error)
	clear() error
}

type fileStore struct{ path string }

func (f fileStore) save(v *estimateCheckpoint) error {
	return checkpoint.SaveJSON(f.path, v)
}

func (f fileStore) load() (*estimateCheckpoint, error) {
	var v estimateCheckpoint
	if err := checkpoint.LoadJSON(f.path, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

func (f fileStore) clear() error {
	st, err := checkpoint.NewStore(f.path)
	if err != nil {
		return err
	}
	return st.Clear()
}
