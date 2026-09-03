package analysis

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nilay-v3rma/pod_to_pod_connectivity_operator_kubernetes/pkg/contract"
)

// classify performs single-round stateless classification of a probe matrix.
func classify(m contract.Matrix, cfg Config) contract.Verdict {
	if len(m) == 0 {
		return contract.Verdict{
			Class:      contract.Healthy,
			Confidence: 1.0,
			Summary:    "empty matrix; no probes to evaluate",
		}
	}

	// --- 1. Parse topology: extract unique nodes ---
	nodes := make(map[string]struct{})
	for key := range m {
		parts := strings.SplitN(key, "->", 2)
		if len(parts) == 2 {
			nodes[parts[0]] = struct{}{}
			nodes[parts[1]] = struct{}{}
		}
	}

	// --- 2. Agent error guardrail ---
	for _, result := range m {
		if result.AgentError {
			return contract.Verdict{
				Class:      contract.Unknown,
				Confidence: 0.0,
				Summary:    "control channel failure detected (agentError=true); cannot emit data plane verdict",
			}
		}
	}

	// --- 3. Identify failed pairs ---
	type failedPair struct {
		key string
		src string
		dst string
	}
	var failed []failedPair
	var degradedSummaries []string

	for key, result := range m {
		parts := strings.SplitN(key, "->", 2)
		if len(parts) != 2 {
			continue
		}
		if !result.Success {
			failed = append(failed, failedPair{key: key, src: parts[0], dst: parts[1]})
		} else if result.LossRate > 0 {
			degradedSummaries = append(degradedSummaries, fmt.Sprintf("%s (%.0f%% loss, %.1fms RTT)", key, result.LossRate*100, result.RTTMillis))
		}
	}

	// --- 4. No hard failures ---
	if len(failed) == 0 {
		summary := "all probes succeeded"
		if len(degradedSummaries) > 0 {
			sort.Strings(degradedSummaries)
			summary = fmt.Sprintf("all probes succeeded; degraded pairs detected: %s", strings.Join(degradedSummaries, ", "))
		}
		return contract.Verdict{
			Class:      contract.Healthy,
			Confidence: 1.0,
			Summary:    summary,
		}
	}

	totalPairs := len(m)
	totalFailed := len(failed)

	// --- 5. Build row/column failure maps ---
	rowFailures := make(map[string]int)
	colFailures := make(map[string]int)
	rowTotal := make(map[string]int)
	colTotal := make(map[string]int)

	for key := range m {
		parts := strings.SplitN(key, "->", 2)
		if len(parts) == 2 {
			rowTotal[parts[0]]++
			colTotal[parts[1]]++
		}
	}
	for _, fp := range failed {
		rowFailures[fp.src]++
		colFailures[fp.dst]++
	}

	confidenceFloor := cfg.ConfidenceFloor
	if confidenceFloor <= 0 {
		confidenceFloor = 0.80
	}

	// --- 6a. PairLocalFailure: exactly 1 failed pair ---
	if totalFailed == 1 {
		fp := failed[0]
		if rowFailures[fp.src] < rowTotal[fp.src] && colFailures[fp.dst] < colTotal[fp.dst] {
			suspects := []string{fp.src, fp.dst}
			sort.Strings(suspects)
			return contract.Verdict{
				Class:        contract.PairLocalFailure,
				Confidence:   1.0,
				SuspectNodes: suspects,
				Summary:      fmt.Sprintf("single pair failure: %s", fp.key),
			}
		}
	}

	// --- 6b. NodeIsolated: full row AND full column for a single node ---
	{
		var bestVerdict contract.Verdict
		var bestConf float64
		for node := range nodes {
			if rowFailures[node] == rowTotal[node] && colFailures[node] == colTotal[node] &&
				rowTotal[node] > 0 && colTotal[node] > 0 {
				explained := rowFailures[node] + colFailures[node]
				conf := float64(explained) / float64(totalFailed)
				if conf > 1.0 {
					conf = 1.0
				}
				if conf > bestConf {
					bestConf = conf
					bestVerdict = contract.Verdict{
						Class:        contract.NodeIsolated,
						Confidence:   conf,
						SuspectNodes: []string{node},
						Summary:      fmt.Sprintf("node %s is fully isolated: all %d incoming and %d outgoing probes failed", node, colFailures[node], rowFailures[node]),
					}
				}
			}
		}
		if bestConf >= confidenceFloor {
			return bestVerdict
		}
	}

	// --- 6c. NodeEgressFailure: full row, NOT full column ---
	{
		var bestVerdict contract.Verdict
		var bestConf float64
		for node := range nodes {
			if rowFailures[node] == rowTotal[node] && rowTotal[node] > 0 &&
				(colTotal[node] == 0 || colFailures[node] < colTotal[node]) {
				conf := float64(rowFailures[node]) / float64(totalFailed)
				if conf > bestConf {
					bestConf = conf
					bestVerdict = contract.Verdict{
						Class:        contract.NodeEgressFailure,
						Confidence:   conf,
						SuspectNodes: []string{node},
						Summary:      fmt.Sprintf("node %s egress failure: all %d outgoing probes failed", node, rowFailures[node]),
					}
				}
			}
		}
		if bestConf >= confidenceFloor {
			return bestVerdict
		}
	}

	// --- 6d. NodeIngressFailure: full column, NOT full row ---
	{
		var bestVerdict contract.Verdict
		var bestConf float64
		for node := range nodes {
			if colFailures[node] == colTotal[node] && colTotal[node] > 0 &&
				(rowTotal[node] == 0 || rowFailures[node] < rowTotal[node]) {
				conf := float64(colFailures[node]) / float64(totalFailed)
				if conf > bestConf {
					bestConf = conf
					bestVerdict = contract.Verdict{
						Class:        contract.NodeIngressFailure,
						Confidence:   conf,
						SuspectNodes: []string{node},
						Summary:      fmt.Sprintf("node %s ingress failure: all %d incoming probes failed", node, colFailures[node]),
					}
				}
			}
		}
		if bestConf >= confidenceFloor {
			return bestVerdict
		}
	}

	// --- 6e. PolicyScopedFailure: all pairs fail with "refused" errors ---
	if totalFailed == totalPairs {
		allRefused := true
		for _, result := range m {
			if !strings.Contains(result.Error, "refused") {
				allRefused = false
				break
			}
		}
		if allRefused {
			var nodeList []string
			for node := range nodes {
				nodeList = append(nodeList, node)
			}
			sort.Strings(nodeList)
			return contract.Verdict{
				Class:        contract.PolicyScoped,
				Confidence:   1.0,
				SuspectNodes: nodeList,
				Summary:      "all probes failed with connection refused; likely network policy or firewall rule",
			}
		}
	}

	// --- 6f. ClusterPartition: broad failures ---
	{
		conf := float64(totalFailed) / float64(totalPairs)
		if conf >= confidenceFloor {
			var nodeList []string
			for node := range nodes {
				nodeList = append(nodeList, node)
			}
			sort.Strings(nodeList)
			return contract.Verdict{
				Class:        contract.ClusterPartition,
				Confidence:   conf,
				SuspectNodes: nodeList,
				Summary:      fmt.Sprintf("cluster partition: %d of %d probes failed (%.0f%%)", totalFailed, totalPairs, conf*100),
			}
		}
	}

	// --- 7. Fallback: Unknown ---
	return contract.Verdict{
		Class:      contract.Unknown,
		Confidence: float64(totalFailed) / float64(totalPairs),
		Summary:    fmt.Sprintf("%d of %d probes failed; no hypothesis exceeds confidence floor %.2f", totalFailed, totalPairs, confidenceFloor),
	}
}
