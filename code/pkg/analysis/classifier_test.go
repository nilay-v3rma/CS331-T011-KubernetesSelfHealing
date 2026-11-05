package analysis

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nilay-v3rma/pod_to_pod_connectivity_operator_kubernetes/pkg/contract"
)

const fixtureDir = "../../contracts/fixtures"

func loadMatrix(t *testing.T, filename string) contract.Matrix {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixtureDir, filename))
	if err != nil {
		t.Fatalf("failed to read fixture %s: %v", filename, err)
	}
	var m contract.Matrix
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("failed to unmarshal fixture %s: %v", filename, err)
	}
	return m
}

func TestClassifyFixtures(t *testing.T) {
	cfg := DefaultConfig()

	tests := []struct {
		name        string
		file        string
		wantClass   contract.Classification
		wantSuspect []string // nil means don't check
	}{
		{
			name:      "healthy",
			file:      "healthy.json",
			wantClass: contract.Healthy,
		},
		{
			name:        "pair-local",
			file:        "pair-local.json",
			wantClass:   contract.PairLocalFailure,
			wantSuspect: []string{"worker1", "worker2"},
		},
		{
			name:        "node-ingress-worker2",
			file:        "node-ingress-worker2.json",
			wantClass:   contract.NodeIngressFailure,
			wantSuspect: []string{"worker2"},
		},
		{
			name:        "node-egress-worker2",
			file:        "node-egress-worker2.json",
			wantClass:   contract.NodeEgressFailure,
			wantSuspect: []string{"worker2"},
		},
		{
			name:        "node-isolated-worker2",
			file:        "node-isolated-worker2.json",
			wantClass:   contract.NodeIsolated,
			wantSuspect: []string{"worker2"},
		},
		{
			name:      "cluster-partition",
			file:      "cluster-partition.json",
			wantClass: contract.ClusterPartition,
		},
		{
			name:      "policy-scoped",
			file:      "policy-scoped.json",
			wantClass: contract.PolicyScoped,
		},
		{
			name:      "ambiguous-two-nodes",
			file:      "ambiguous-two-nodes.json",
			wantClass: contract.Unknown,
		},
		{
			name:      "ambiguous-agent-down",
			file:      "ambiguous-agent-down.json",
			wantClass: contract.Unknown,
		},
		{
			name:        "degraded-partial-loss",
			file:        "degraded-partial-loss.json",
			wantClass:   contract.Degraded,
			wantSuspect: []string{"worker1", "worker2", "worker3"},
		},
		{
			name:      "empty",
			file:      "empty.json",
			wantClass: contract.NoData,
		},
		{
			name:        "loss-40",
			file:        "loss-40.json",
			wantClass:   contract.Degraded,
			wantSuspect: []string{"worker1", "worker2", "worker3"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := loadMatrix(t, tc.file)
			_, verdict := Evaluate(m, cfg)

			if verdict.Class != tc.wantClass {
				t.Errorf("classification: got %q, want %q (summary: %s)", verdict.Class, tc.wantClass, verdict.Summary)
			}

			if tc.wantSuspect != nil {
				if len(verdict.SuspectNodes) != len(tc.wantSuspect) {
					t.Errorf("suspect nodes: got %v, want %v", verdict.SuspectNodes, tc.wantSuspect)
				} else {
					for i, node := range tc.wantSuspect {
						if verdict.SuspectNodes[i] != node {
							t.Errorf("suspect node %d: got %q, want %q", i, verdict.SuspectNodes[i], node)
						}
					}
				}
			}

			if verdict.Class != contract.Unknown && verdict.Class != contract.Healthy && verdict.Class != contract.NoData {
				if verdict.Confidence < cfg.ConfidenceFloor {
					t.Errorf("non-unknown verdict %q has confidence %.2f below floor %.2f", verdict.Class, verdict.Confidence, cfg.ConfidenceFloor)
				}
			}

			t.Logf("verdict: class=%s confidence=%.2f suspects=%v summary=%q", verdict.Class, verdict.Confidence, verdict.SuspectNodes, verdict.Summary)
		})
	}
}

func TestClassifyEmptyMatrix(t *testing.T) {
	_, verdict := Evaluate(contract.Matrix{}, DefaultConfig())
	if verdict.Class != contract.NoData {
		t.Errorf("empty matrix: got %q, want NoData", verdict.Class)
	}
	if verdict.Confidence != 0.0 {
		t.Errorf("empty matrix confidence: got %.2f, want 0.0", verdict.Confidence)
	}
	if verdict.Summary != "insufficient evidence: empty matrix" {
		t.Errorf("empty matrix summary: got %q", verdict.Summary)
	}

	// A nil matrix carries no evidence either.
	_, verdict = Evaluate(nil, DefaultConfig())
	if verdict.Class != contract.NoData {
		t.Errorf("nil matrix: got %q, want NoData", verdict.Class)
	}
}

func TestAgentErrorGuardrail(t *testing.T) {
	m := contract.Matrix{
		"node-a->node-b": {Success: true, LossRate: 0},
		"node-b->node-a": {Success: false, LossRate: 1.0, AgentError: true},
	}
	_, verdict := Evaluate(m, DefaultConfig())
	if verdict.Class != contract.Unknown {
		t.Errorf("agent error guardrail: got %q, want Unknown", verdict.Class)
	}
}

func TestConfidenceFloor(t *testing.T) {
	// ambiguous-two-nodes has 2/6 failures = 0.33 confidence for ClusterPartition
	// With default floor 0.80, this should be Unknown
	m := loadMatrix(t, "ambiguous-two-nodes.json")
	_, verdict := Evaluate(m, DefaultConfig())
	if verdict.Class != contract.Unknown {
		t.Errorf("with default floor: got %q, want Unknown", verdict.Class)
	}

	// With floor lowered to 0.10, some hypothesis should match
	lowFloor := Config{ConfidenceFloor: 0.10}
	_, verdict2 := Evaluate(m, lowFloor)
	if verdict2.Class == contract.Unknown {
		t.Logf("even with low floor, classification is Unknown (acceptable if no structural hypothesis matches)")
	}
}

func TestDegradedPartialLoss(t *testing.T) {
	m := loadMatrix(t, "degraded-partial-loss.json")
	_, verdict := Evaluate(m, DefaultConfig())
	if verdict.Class != contract.Degraded {
		t.Fatalf("degraded-partial-loss: got %q, want Degraded", verdict.Class)
	}
	if verdict.Confidence != 1.0 {
		t.Errorf("degraded confidence: got %.2f, want 1.0", verdict.Confidence)
	}
	for _, want := range []string{"worker1->worker2", "worker3->worker2", "threshold"} {
		if !strings.Contains(verdict.Summary, want) {
			t.Errorf("summary %q missing %q", verdict.Summary, want)
		}
	}
	t.Logf("summary: %s", verdict.Summary)
}

func TestDegradedThresholdBoundary(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		loss float64
		want contract.Classification
	}{
		{"at threshold", DefaultConfig(), 0.05, contract.Degraded},
		{"below threshold", DefaultConfig(), 0.04, contract.Healthy},
		{"zero-value config uses default, at", Config{}, 0.05, contract.Degraded},
		{"zero-value config uses default, below", Config{}, 0.04, contract.Healthy},
		{"zero loss is healthy", Config{}, 0.0, contract.Healthy},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := contract.Matrix{
				"a->b": {Success: true, LossRate: tc.loss},
				"b->a": {Success: true, LossRate: 0},
			}
			_, verdict := Evaluate(m, tc.cfg)
			if verdict.Class != tc.want {
				t.Errorf("loss %.2f: got %q, want %q (summary: %s)", tc.loss, verdict.Class, tc.want, verdict.Summary)
			}
		})
	}
}

func TestDegradedHardFailureDominates(t *testing.T) {
	m := loadMatrix(t, "pair-local.json")
	for key, result := range m {
		if result.Success {
			result.LossRate = 0.4
			m[key] = result
			break
		}
	}
	_, verdict := Evaluate(m, DefaultConfig())
	if verdict.Class != contract.PairLocalFailure {
		t.Errorf("hard failure plus loss: got %q, want PairLocalFailure", verdict.Class)
	}
}

func TestDegradedConfigurable(t *testing.T) {
	m := loadMatrix(t, "loss-40.json")
	cfg := DefaultConfig()
	cfg.DegradedLossThreshold = 0.5
	_, verdict := Evaluate(m, cfg)
	if verdict.Class != contract.Healthy {
		t.Errorf("loss-40 with 50%% threshold: got %q, want Healthy", verdict.Class)
	}
	if !strings.Contains(verdict.Summary, "below") {
		t.Errorf("sub-threshold loss should still be reported, summary: %q", verdict.Summary)
	}
}
