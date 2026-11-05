package analysis

import (
	"github.com/nilay-v3rma/pod_to_pod_connectivity_operator_kubernetes/pkg/contract"
)

// Evaluate is the frozen entry point for the analysis library.
// It takes a probe matrix and configuration, performs stateless classification,
// and returns the matrix alongside the computed verdict.
func Evaluate(m contract.Matrix, cfg Config) (contract.Matrix, contract.Verdict) {
	verdict := classify(m, cfg)
	return m, verdict
}
