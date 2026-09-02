package controller

import "github.com/nilay-v3rma/pod_to_pod_connectivity_operator_kubernetes/pkg/contract"

// [ESON] Temporary classifier stub until P4 provides the real analysis library.
func StubClassifier(matrix contract.Matrix) contract.Verdict {
	if len(matrix) == 0 {
		return contract.Verdict{
			Class:      contract.Healthy,
			Confidence: 1.0,
			Summary:    "no probe data; using stub healthy verdict",
		}
	}

	return contract.Verdict{
		Class:      contract.Healthy,
		Confidence: 1.0,
		Summary:    "stub classifier used by P3 pending analysis integration",
	}
}
