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

const stabilizePair = "worker1->worker2"

var (
	stabilizeFailed  = contract.Matrix{stabilizePair: {Success: false, LossRate: 1, Error: "dial tcp: i/o timeout"}}
	stabilizeHealthy = contract.Matrix{stabilizePair: {Success: true, LossRate: 0, RTTMillis: 0.5}}
)

// round feeds one matrix through the tracker and returns the stabilized view.
func round(tracker *Tracker, m contract.Matrix) contract.Matrix {
	tracker.Update(m)
	return tracker.Stabilize(m)
}

func newStabilizeTracker() *Tracker {
	return NewTracker(Config{FailureThreshold: 3, SuccessThreshold: 2})
}

func TestHysteresisStabilizeSingleFailureSuppressed(t *testing.T) {
	tracker := newStabilizeTracker()
	input := contract.Matrix{stabilizePair: stabilizeFailed[stabilizePair]}

	got := round(tracker, input)[stabilizePair]
	if !got.Success || got.LossRate != 0 || got.Error != "hysteresis: transient failure suppressed" {
		t.Errorf("1 failure: got %+v, want suppressed success", got)
	}
	if input[stabilizePair].Success || input[stabilizePair].Error != "dial tcp: i/o timeout" {
		t.Errorf("Stabilize mutated caller's matrix: %+v", input[stabilizePair])
	}
}

func TestHysteresisStabilizeThreeFailuresSurface(t *testing.T) {
	tracker := newStabilizeTracker()
	for i := 1; i <= 2; i++ {
		if got := round(tracker, stabilizeFailed)[stabilizePair]; !got.Success {
			t.Fatalf("failure %d should be suppressed, got %+v", i, got)
		}
	}
	got := round(tracker, stabilizeFailed)[stabilizePair]
	if got.Success || got.Error != "dial tcp: i/o timeout" {
		t.Errorf("3 failures: got %+v, want real failure", got)
	}
}

func TestHysteresisStabilizeOneSuccessStillDegraded(t *testing.T) {
	tracker := newStabilizeTracker()
	for i := 0; i < 3; i++ {
		round(tracker, stabilizeFailed)
	}
	got := round(tracker, stabilizeHealthy)[stabilizePair]
	if got.Success || got.LossRate != 1 || got.Error != "hysteresis: recovery pending" {
		t.Errorf("1 success after degraded: got %+v, want recovery pending", got)
	}
}

func TestHysteresisStabilizeTwoSuccessesRecover(t *testing.T) {
	tracker := newStabilizeTracker()
	for i := 0; i < 3; i++ {
		round(tracker, stabilizeFailed)
	}
	round(tracker, stabilizeHealthy)
	got := round(tracker, stabilizeHealthy)[stabilizePair]
	if got != stabilizeHealthy[stabilizePair] {
		t.Errorf("2 successes: got %+v, want real result %+v", got, stabilizeHealthy[stabilizePair])
	}
	if tracker.GetState(stabilizePair).Status != PairHealthy {
		t.Error("expected tracker state Healthy after 2 successes")
	}
}

func TestHysteresisStabilizeAgentErrorPassthrough(t *testing.T) {
	tracker := newStabilizeTracker()
	m := contract.Matrix{stabilizePair: {Success: false, LossRate: 1, AgentError: true, Error: "control channel down"}}
	got := round(tracker, m)[stabilizePair]
	if got != m[stabilizePair] {
		t.Errorf("agent error rewritten: got %+v", got)
	}
}

func TestHysteresisStabilizeThenEvaluate(t *testing.T) {
	tracker := newStabilizeTracker()
	m := loadMatrix(t, "pair-local.json")
	cfg := DefaultConfig()

	for i := 1; i <= 2; i++ {
		if _, v := Evaluate(round(tracker, m), cfg); v.Class != contract.Healthy {
			t.Fatalf("round %d: got %q, want Healthy (transient suppressed)", i, v.Class)
		}
	}
	if _, v := Evaluate(round(tracker, m), cfg); v.Class != contract.PairLocalFailure {
		t.Errorf("round 3: got %q, want PairLocalFailure", v.Class)
	}
}
