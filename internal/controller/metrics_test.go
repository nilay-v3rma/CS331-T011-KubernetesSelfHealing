package controller

import (
	"bytes"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/nilay-v3rma/pod_to_pod_connectivity_operator_kubernetes/pkg/contract"
)

func writeMetricsString(r *Reconciler) string {
	var buf bytes.Buffer
	r.WriteMetrics(&buf)
	return buf.String()
}

func TestWriteMetricsClassificationAndCounts(t *testing.T) {
	r := &Reconciler{}
	r.recordMetrics(contract.Matrix{
		"a->b": {Success: true, LossRate: 0},
		"b->a": {Success: true, LossRate: 0.4},
		"a->c": {Success: false, LossRate: 1},
		"c->a": {Success: false, LossRate: 1, AgentError: true},
	}, contract.Verdict{Class: contract.Degraded, Confidence: 1.0}, 0)

	out := writeMetricsString(r)
	for _, line := range []string{
		`netprobe_classification{classification="Degraded"} 1`,
		`netprobe_classification_confidence{classification="Degraded"} 1.000000`,
		`netprobe_mesh_health{verdict="Degraded"} 0`,
		`netprobe_reachability_pairs{state="healthy"} 1`,
		`netprobe_reachability_pairs{state="degraded"} 1`,
		`netprobe_reachability_pairs{state="failed"} 2`,
		`netprobe_reachability_pairs{state="nodata"} 0`,
		`netprobe_pair_reachable{src_node="a",dst_node="b"} 1`,
	} {
		if !strings.Contains(out, line+"\n") {
			t.Errorf("metrics output missing %q", line)
		}
	}
}

func TestWriteMetricsEmptyMatrixReportsNoData(t *testing.T) {
	r := &Reconciler{}
	r.recordMetrics(contract.Matrix{}, contract.Verdict{Class: contract.NoData}, 0)

	out := writeMetricsString(r)
	for _, line := range []string{
		`netprobe_classification{classification="NoData"} 1`,
		`netprobe_reachability_pairs{state="healthy"} 0`,
		`netprobe_reachability_pairs{state="degraded"} 0`,
		`netprobe_reachability_pairs{state="failed"} 0`,
		`netprobe_reachability_pairs{state="nodata"} 1`,
	} {
		if !strings.Contains(out, line+"\n") {
			t.Errorf("metrics output missing %q", line)
		}
	}
}

// TestDashboardAndReadmeReferenceOnlyEmittedMetrics guards against doc drift:
// every netprobe_* name in the Grafana dashboard and README must be emitted.
func TestDashboardAndReadmeReferenceOnlyEmittedMetrics(t *testing.T) {
	r := &Reconciler{}
	r.recordMetrics(contract.Matrix{"a->b": {Success: true}}, contract.Verdict{Class: contract.Healthy, Confidence: 1}, 0)

	emitted := map[string]bool{
		// Emitted by the agent's /metrics (pkg/server.go), not the manager.
		"netprobe_agent_up": true,
	}
	typeLine := regexp.MustCompile(`(?m)^# TYPE (netprobe_[a-z_]+) `)
	for _, match := range typeLine.FindAllStringSubmatch(writeMetricsString(r), -1) {
		emitted[match[1]] = true
	}

	dashboard, err := os.ReadFile("../../config/monitoring/grafana-dashboard.json")
	if err != nil {
		t.Fatalf("read dashboard: %v", err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(dashboard, &parsed); err != nil {
		t.Fatalf("dashboard JSON does not parse: %v", err)
	}
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatalf("read README: %v", err)
	}

	metricName := regexp.MustCompile(`netprobe_[a-z_]+`)
	for file, content := range map[string][]byte{"grafana-dashboard.json": dashboard, "README.md": readme} {
		names := metricName.FindAllString(string(content), -1)
		if len(names) == 0 {
			t.Errorf("%s references no netprobe metrics", file)
		}
		for _, name := range names {
			if !emitted[name] {
				t.Errorf("%s references %s, which is not emitted", file, name)
			}
		}
	}
}
