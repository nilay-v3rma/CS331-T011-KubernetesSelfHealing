package controller

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nilay-v3rma/pod_to_pod_connectivity_operator_kubernetes/pkg/contract"
)

// [P3] Temporary local classifier for KIND integration until P4's classifier is available.
// It recognizes directional row/column failures and refuses ambiguous or control-channel failures.
func StubClassifier(matrix contract.Matrix) contract.Verdict {
	nodes := matrixNodes(matrix)
	if len(nodes) == 0 {
		return contract.Verdict{Class: contract.Unknown, Summary: "no probe data"}
	}

	failures := 0
	for _, result := range matrix {
		if probeFailed(result) {
			failures++
		}
	}
	// Directional classifications require the complete set of observed pairs.
	// Otherwise a missing probe could be mistaken for a healthy direction.
	completeMatrix := len(matrix) == len(nodes)*(len(nodes)-1)
	if !completeMatrix {
		return contract.Verdict{Class: contract.Unknown, Summary: "incomplete probe matrix; classification refused"}
	}
	if failures == 0 {
		return contract.Verdict{Class: contract.Healthy, Confidence: 1, Summary: "all observed probe paths are reachable"}
	}

	if failures == len(nodes)*(len(nodes)-1) {
		return contract.Verdict{Class: contract.ClusterPartition, Confidence: 1, Summary: "all observed inter-node paths failed"}
	}

	// [P3] Simplified KIND rule: one failed row or column is treated as one node failure.
	// This intentionally does not distinguish ingress from egress for the basic restart demo.
	for _, node := range nodes {
		outFailures, outTotal := 0, 0
		inFailures, inTotal := 0, 0
		for _, other := range nodes {
			if node == other {
				continue
			}
			if result, ok := matrix[pairKey(node, other)]; ok {
				outTotal++
				if probeFailed(result) {
					outFailures++
				}
			}
			if result, ok := matrix[pairKey(other, node)]; ok {
				inTotal++
				if probeFailed(result) {
					inFailures++
				}
			}
		}
		if (outTotal > 0 && outFailures == outTotal) || (inTotal > 0 && inFailures == inTotal) {
			return contract.Verdict{Class: contract.NodeIsolated, Confidence: 1, SuspectNodes: []string{node}, Summary: fmt.Sprintf("all observed traffic in one direction involving %s failed", node)}
		}
	}

	if failures == 1 {
		for key, result := range matrix {
			if !result.Success {
				parts := strings.SplitN(key, "->", 2)
				if len(parts) == 2 {
					return contract.Verdict{Class: contract.PairLocalFailure, Confidence: 1, SuspectNodes: []string{parts[0], parts[1]}, Summary: fmt.Sprintf("only path %s failed", key)}
				}
			}
		}
	}

	return contract.Verdict{Class: contract.Unknown, Confidence: 0, Summary: "failure pattern is ambiguous"}
}

func probeFailed(result contract.ProbeResult) bool {
	return !result.Success || result.LossRate > 0
}

func matrixNodes(matrix contract.Matrix) []string {
	seen := make(map[string]struct{})
	for key := range matrix {
		parts := strings.SplitN(key, "->", 2)
		if len(parts) == 2 {
			seen[parts[0]] = struct{}{}
			seen[parts[1]] = struct{}{}
		}
	}
	nodes := make([]string, 0, len(seen))
	for node := range seen {
		nodes = append(nodes, node)
	}
	sort.Strings(nodes)
	return nodes
}

func pairKey(source, destination string) string {
	return source + "->" + destination
}
