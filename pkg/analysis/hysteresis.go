package analysis

import (
	"github.com/nilay-v3rma/pod_to_pod_connectivity_operator_kubernetes/pkg/contract"
)

// PairStatus represents the health status of a directed pair.
type PairStatus int

const (
	PairHealthy  PairStatus = iota
	PairDegraded
)

// PairState tracks consecutive failure/success counts for a single directed pair.
type PairState struct {
	ConsecutiveFailures  int
	ConsecutiveSuccesses int
	Status               PairStatus
}

// Tracker maintains per-pair hysteresis state across probe rounds.
type Tracker struct {
	pairs            map[string]*PairState
	failureThreshold int
	successThreshold int
}

// NewTracker creates a new hysteresis tracker with the given config.
func NewTracker(cfg Config) *Tracker {
	ft := cfg.FailureThreshold
	if ft <= 0 {
		ft = 3
	}
	st := cfg.SuccessThreshold
	if st <= 0 {
		st = 2
	}
	return &Tracker{
		pairs:            make(map[string]*PairState),
		failureThreshold: ft,
		successThreshold: st,
	}
}

// Update feeds one probe round into the tracker, updating consecutive
// counters and performing state transitions.
func (t *Tracker) Update(m contract.Matrix) {
	for key, result := range m {
		state, ok := t.pairs[key]
		if !ok {
			state = &PairState{Status: PairHealthy}
			t.pairs[key] = state
		}

		failed := !result.Success
		if failed {
			state.ConsecutiveFailures++
			state.ConsecutiveSuccesses = 0
		} else {
			state.ConsecutiveSuccesses++
			state.ConsecutiveFailures = 0
		}

		// State transitions
		switch state.Status {
		case PairHealthy:
			if state.ConsecutiveFailures >= t.failureThreshold {
				state.Status = PairDegraded
			}
		case PairDegraded:
			if state.ConsecutiveSuccesses >= t.successThreshold {
				state.Status = PairHealthy
			}
		}
	}
}

// DegradedPairs returns the set of pair keys currently in Degraded state.
func (t *Tracker) DegradedPairs() []string {
	var result []string
	for key, state := range t.pairs {
		if state.Status == PairDegraded {
			result = append(result, key)
		}
	}
	return result
}

// GetState returns the state for a specific pair, or nil if not tracked.
func (t *Tracker) GetState(key string) *PairState {
	return t.pairs[key]
}

// Reset clears all tracked state (e.g., on generation reset).
func (t *Tracker) Reset() {
	t.pairs = make(map[string]*PairState)
}
