package controller

import (
	"fmt"
	"testing"

	"github.com/nilay-v3rma/pod_to_pod_connectivity_operator_kubernetes/pkg/contract"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestBoundedStatus600Pairs(t *testing.T) {
	// Build a matrix with 600 synthetic pairs, one unique key per index.
	matrix := make(contract.Matrix, 600)
	for i := 0; i < 600; i++ {
		key := fmt.Sprintf("node-%03d->node-%03d", i, (i+1)%600)
		matrix[key] = contract.ProbeResult{
			Success:  true,
			LossRate: 0.0,
		}
	}

	// Create a minimal unstructured object with Generation=1.
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "network.selfheal.io/v1alpha1",
			"kind":       "NetworkHealthCheck",
			"metadata": map[string]interface{}{
				"name":       "test",
				"generation": int64(1),
			},
		},
	}

	verdict := contract.Verdict{
		Class:      contract.Healthy,
		Confidence: 1.0,
		Summary:    "All pairs healthy",
	}

	status := buildNetworkHealthCheckStatus(obj, verdict, matrix)

	// Assert reachability is capped at 500.
	reachability, ok := status["reachability"].([]interface{})
	if !ok {
		t.Fatalf("reachability is not []interface{}")
	}
	if len(reachability) != 500 {
		t.Errorf("expected len(reachability)==500, got %d", len(reachability))
	}

	// Assert reachabilityTruncated == true.
	truncated, ok := status["reachabilityTruncated"].(bool)
	if !ok || !truncated {
		t.Errorf("expected reachabilityTruncated==true, got %v (ok=%v)", status["reachabilityTruncated"], ok)
	}

	// Assert reachabilityTotal == 600.
	total, ok := status["reachabilityTotal"].(int)
	if !ok || total != 600 {
		t.Errorf("expected reachabilityTotal==600, got %v (ok=%v)", status["reachabilityTotal"], ok)
	}
}
