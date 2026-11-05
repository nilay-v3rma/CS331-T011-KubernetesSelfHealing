package controller

import (
	"fmt"
	"sort"
	"testing"

	"github.com/nilay-v3rma/pod_to_pod_connectivity_operator_kubernetes/pkg/contract"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func newStatusTestObject() *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "network.selfheal.io/v1alpha1",
			"kind":       "NetworkHealthCheck",
			"metadata": map[string]interface{}{
				"name":       "test",
				"generation": int64(1),
			},
		},
	}
}

func reachabilitySummaryCount(t *testing.T, status map[string]interface{}, field string) int64 {
	t.Helper()
	summary, ok := status["reachabilitySummary"].(map[string]interface{})
	if !ok {
		t.Fatalf("reachabilitySummary is not a map: %#v", status["reachabilitySummary"])
	}
	value, ok := summary[field].(int64)
	if !ok {
		t.Fatalf("reachabilitySummary.%s is not int64: %#v", field, summary[field])
	}
	return value
}

func TestBoundedStatus600Pairs(t *testing.T) {
	// Build a matrix with 600 synthetic pairs, one unique key per index.
	// Pairs 550..599 sort after the 500-entry cut and are failed, so the summary
	// only counts them if it is computed over the full matrix.
	matrix := make(contract.Matrix, 600)
	for i := 0; i < 600; i++ {
		key := fmt.Sprintf("node-%03d->node-%03d", i, (i+1)%600)
		matrix[key] = contract.ProbeResult{
			Success:  i < 550,
			LossRate: 0.0,
		}
	}

	verdict := contract.Verdict{
		Class:      contract.Healthy,
		Confidence: 1.0,
		Summary:    "All pairs healthy",
	}

	status := buildNetworkHealthCheckStatus(newStatusTestObject(), verdict, matrix)

	// Assert reachability is capped at 500.
	reachability, ok := status["reachability"].([]interface{})
	if !ok {
		t.Fatalf("reachability is not []interface{}")
	}
	if len(reachability) != 500 {
		t.Errorf("expected len(reachability)==500, got %d", len(reachability))
	}

	// Assert the kept entries are the first 500 sorted keys.
	keys := make([]string, 0, len(matrix))
	for key := range matrix {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	first := reachability[0].(map[string]interface{})["key"]
	last := reachability[len(reachability)-1].(map[string]interface{})["key"]
	if first != keys[0] || last != keys[499] {
		t.Errorf("expected entries %q..%q, got %v..%v", keys[0], keys[499], first, last)
	}

	// Assert reachabilityTruncated == true.
	truncated, ok := status["reachabilityTruncated"].(bool)
	if !ok || !truncated {
		t.Errorf("expected reachabilityTruncated==true, got %v (ok=%v)", status["reachabilityTruncated"], ok)
	}

	// Assert reachabilityTotal == 600.
	total, ok := status["reachabilityTotal"].(int64)
	if !ok || total != 600 {
		t.Errorf("expected reachabilityTotal==600, got %v (ok=%v)", status["reachabilityTotal"], ok)
	}

	// Assert the summary counts the full matrix, including truncated rows.
	if got := reachabilitySummaryCount(t, status, "total"); got != 600 {
		t.Errorf("expected reachabilitySummary.total==600, got %d", got)
	}
	if got := reachabilitySummaryCount(t, status, "healthy"); got != 550 {
		t.Errorf("expected reachabilitySummary.healthy==550, got %d", got)
	}
	if got := reachabilitySummaryCount(t, status, "failed"); got != 50 {
		t.Errorf("expected reachabilitySummary.failed==50, got %d", got)
	}
}

func TestStatusSmallMatrixNotTruncated(t *testing.T) {
	matrix := contract.Matrix{
		"a->b": {Success: true},
		"b->a": {Success: true, LossRate: 0.4},
		"a->c": {Success: false, LossRate: 1},
	}

	status := buildNetworkHealthCheckStatus(newStatusTestObject(), contract.Verdict{Class: contract.PairLocalFailure}, matrix)

	if truncated, ok := status["reachabilityTruncated"].(bool); !ok || truncated {
		t.Errorf("expected reachabilityTruncated==false, got %#v", status["reachabilityTruncated"])
	}
	if total, ok := status["reachabilityTotal"].(int64); !ok || total != 3 {
		t.Errorf("expected reachabilityTotal==3, got %#v", status["reachabilityTotal"])
	}
	if reachability := status["reachability"].([]interface{}); len(reachability) != 3 {
		t.Errorf("expected 3 reachability entries, got %d", len(reachability))
	}
	for field, want := range map[string]int64{"total": 3, "healthy": 1, "degraded": 1, "failed": 1} {
		if got := reachabilitySummaryCount(t, status, field); got != want {
			t.Errorf("expected reachabilitySummary.%s==%d, got %d", field, want, got)
		}
	}
}

func TestStatusDegradedPairsBounded(t *testing.T) {
	matrix := make(contract.Matrix)
	for i := 0; i < 60; i++ {
		matrix[fmt.Sprintf("d-%02d->x", i)] = contract.ProbeResult{Success: true, LossRate: 0.4}
	}
	matrix["a-healthy->x"] = contract.ProbeResult{Success: true, LossRate: 0.01}
	matrix["a-failed->x"] = contract.ProbeResult{Success: false, LossRate: 1}

	status := buildNetworkHealthCheckStatus(newStatusTestObject(), contract.Verdict{Class: contract.Degraded}, matrix)

	degraded, ok := status["degradedPairs"].([]interface{})
	if !ok {
		t.Fatalf("degradedPairs is not []interface{}: %#v", status["degradedPairs"])
	}
	if len(degraded) != maxDegradedPairs {
		t.Fatalf("expected %d degraded pairs, got %d", maxDegradedPairs, len(degraded))
	}
	for i, item := range degraded {
		if want := fmt.Sprintf("d-%02d->x", i); item != want {
			t.Errorf("degradedPairs[%d]: expected %q, got %v", i, want, item)
		}
	}
	if got := reachabilitySummaryCount(t, status, "degraded"); got != 60 {
		t.Errorf("expected reachabilitySummary.degraded==60, got %d", got)
	}
}
