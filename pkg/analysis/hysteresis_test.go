package analysis

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/nilay-v3rma/pod_to_pod_connectivity_operator_kubernetes/pkg/contract"
)

func loadFlappingSequence(t *testing.T) []contract.Matrix {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixtureDir, "flapping-sequence.json"))
	if err != nil {
		t.Fatalf("failed to read flapping-sequence.json: %v", err)
	}
	var seq []contract.Matrix
	if err := json.Unmarshal(data, &seq); err != nil {
		t.Fatalf("failed to unmarshal flapping-sequence.json: %v", err)
	}
	return seq
}

func TestHysteresisFlappingSequence(t *testing.T) {
	seq := loadFlappingSequence(t)
	if len(seq) != 10 {
		t.Fatalf("expected 10 rounds in flapping sequence, got %d", len(seq))
	}

	cfg := Config{FailureThreshold: 3, SuccessThreshold: 2}
	tracker := NewTracker(cfg)

	pair := "worker1->worker2"

	// Rounds 0-2: all healthy
	for i := 0; i <= 2; i++ {
		tracker.Update(seq[i])
		state := tracker.GetState(pair)
		if state == nil {
			t.Fatalf("round %d: no state for %s", i, pair)
		}
		if state.Status != PairHealthy {
			t.Errorf("round %d: expected Healthy, got %d", i, state.Status)
		}
		if len(tracker.DegradedPairs()) != 0 {
			t.Errorf("round %d: expected 0 degraded pairs, got %d", i, len(tracker.DegradedPairs()))
		}
	}

	// Round 3: first failure on worker1->worker2
	tracker.Update(seq[3])
	state := tracker.GetState(pair)
	if state.ConsecutiveFailures != 1 {
		t.Errorf("round 3: expected 1 consecutive failure, got %d", state.ConsecutiveFailures)
	}
	if state.Status != PairHealthy {
		t.Errorf("round 3: should still be Healthy (1 < threshold 3)")
	}

	// Round 4: second failure
	tracker.Update(seq[4])
	state = tracker.GetState(pair)
	if state.ConsecutiveFailures != 2 {
		t.Errorf("round 4: expected 2 consecutive failures, got %d", state.ConsecutiveFailures)
	}
	if state.Status != PairHealthy {
		t.Errorf("round 4: should still be Healthy (2 < threshold 3)")
	}

	// Round 5: recovery (healthy) — resets consecutive failures
	tracker.Update(seq[5])
	state = tracker.GetState(pair)
	if state.ConsecutiveFailures != 0 {
		t.Errorf("round 5: expected 0 consecutive failures after recovery, got %d", state.ConsecutiveFailures)
	}
	if state.ConsecutiveSuccesses != 1 {
		t.Errorf("round 5: expected 1 consecutive success, got %d", state.ConsecutiveSuccesses)
	}
	if state.Status != PairHealthy {
		t.Errorf("round 5: should be Healthy")
	}

	// Rounds 6, 7, 8: three consecutive failures → transition to Degraded
	for i := 6; i <= 8; i++ {
		tracker.Update(seq[i])
	}
	state = tracker.GetState(pair)
	if state.ConsecutiveFailures != 3 {
		t.Errorf("round 8: expected 3 consecutive failures, got %d", state.ConsecutiveFailures)
	}
	if state.Status != PairDegraded {
		t.Errorf("round 8: expected Degraded after 3 consecutive failures, got %d", state.Status)
	}
	if len(tracker.DegradedPairs()) != 1 {
		t.Errorf("round 8: expected 1 degraded pair, got %d", len(tracker.DegradedPairs()))
	}

	// Round 9: healthy — 1 consecutive success, still Degraded (needs 2)
	tracker.Update(seq[9])
	state = tracker.GetState(pair)
	if state.ConsecutiveSuccesses != 1 {
		t.Errorf("round 9: expected 1 consecutive success, got %d", state.ConsecutiveSuccesses)
	}
	if state.Status != PairDegraded {
		t.Errorf("round 9: should still be Degraded (1 < successThreshold 2)")
	}
}

func TestHysteresisRecovery(t *testing.T) {
	cfg := Config{FailureThreshold: 2, SuccessThreshold: 2}
	tracker := NewTracker(cfg)
	pair := "a->b"

	// Two failures → Degraded
	for i := 0; i < 2; i++ {
		tracker.Update(contract.Matrix{pair: {Success: false, LossRate: 1.0}})
	}
	if tracker.GetState(pair).Status != PairDegraded {
		t.Fatal("expected Degraded after 2 failures")
	}

	// One success — not yet recovered
	tracker.Update(contract.Matrix{pair: {Success: true, LossRate: 0}})
	if tracker.GetState(pair).Status != PairDegraded {
		t.Fatal("expected still Degraded after 1 success")
	}

	// Second success → recovered to Healthy
	tracker.Update(contract.Matrix{pair: {Success: true, LossRate: 0}})
	if tracker.GetState(pair).Status != PairHealthy {
		t.Fatal("expected Healthy after 2 consecutive successes")
	}
}

func TestHysteresisReset(t *testing.T) {
	cfg := Config{FailureThreshold: 1, SuccessThreshold: 1}
	tracker := NewTracker(cfg)

	tracker.Update(contract.Matrix{"a->b": {Success: false, LossRate: 1.0}})
	if len(tracker.DegradedPairs()) != 1 {
		t.Fatal("expected 1 degraded pair")
	}

	tracker.Reset()
	if len(tracker.DegradedPairs()) != 0 {
		t.Fatal("expected 0 degraded pairs after reset")
	}
	if tracker.GetState("a->b") != nil {
		t.Fatal("expected nil state after reset")
	}
}
