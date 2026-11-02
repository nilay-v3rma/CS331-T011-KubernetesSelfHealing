package controller

import (
	"sort"
	"strings"
	"time"

	"github.com/nilay-v3rma/pod_to_pod_connectivity_operator_kubernetes/pkg/analysis"
	"github.com/nilay-v3rma/pod_to_pod_connectivity_operator_kubernetes/pkg/contract"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	// maxStatusEntries bounds status.reachability. A full mesh is O(N^2) pairs and
	// the whole object must stay well under the ~1.5 MB etcd value limit.
	maxStatusEntries = 500
	// maxDegradedPairs bounds status.degradedPairs.
	maxDegradedPairs = 50
)

// degradedLossThreshold is the classifier's default, so status counts, metrics,
// and the verdict all agree on what "degraded" means.
var degradedLossThreshold = analysis.DefaultConfig().DegradedLossThreshold

// Pair states used by status.reachabilitySummary and netprobe_reachability_pairs.
const (
	pairStateHealthy  = "healthy"
	pairStateDegraded = "degraded"
	pairStateFailed   = "failed"
)

// pairState buckets one directed pair: failed if no probe completed, degraded if
// reachable with loss at or above the threshold, healthy otherwise.
func pairState(result contract.ProbeResult) string {
	switch {
	case !result.Success:
		return pairStateFailed
	case result.LossRate >= degradedLossThreshold:
		return pairStateDegraded
	default:
		return pairStateHealthy
	}
}

// buildNetworkHealthCheckStatus constructs the status map for a NetworkHealthCheck resource.
// reachability holds at most maxStatusEntries sorted entries; reachabilityTotal,
// reachabilityTruncated, reachabilitySummary, and degradedPairs always describe the full matrix.
// Integers are int64 because unstructured objects only deep-copy int64.
func buildNetworkHealthCheckStatus(resource *unstructured.Unstructured, verdict contract.Verdict, matrix contract.Matrix) map[string]interface{} {
	keys := sortedMatrixKeys(matrix)
	limit := len(keys)
	if limit > maxStatusEntries {
		limit = maxStatusEntries
	}

	counts := map[string]int64{pairStateHealthy: 0, pairStateDegraded: 0, pairStateFailed: 0}
	degraded := make([]interface{}, 0)
	for _, key := range keys {
		state := pairState(matrix[key])
		counts[state]++
		if state == pairStateDegraded && len(degraded) < maxDegradedPairs {
			degraded = append(degraded, key)
		}
	}

	status := map[string]interface{}{
		"observedGeneration":       resource.GetGeneration(),
		"lastProbeTime":            time.Now().UTC().Format(time.RFC3339),
		"verdict":                  string(verdict.Class),
		"classification":           string(verdict.Class),
		"classificationConfidence": verdict.Confidence,
		"suspectNodes":             stringSliceToInterfaces(verdict.SuspectNodes),
		"reachability":             matrixToStatusEntries(matrix, keys[:limit]),
		"reachabilityTruncated":    len(keys) > maxStatusEntries,
		"reachabilityTotal":        int64(len(keys)),
		"reachabilitySummary": map[string]interface{}{
			"total":           int64(len(keys)),
			pairStateHealthy:  counts[pairStateHealthy],
			pairStateDegraded: counts[pairStateDegraded],
			pairStateFailed:   counts[pairStateFailed],
		},
		"degradedPairs": degraded,
		"summary":       verdict.Summary,
	}

	conditionStatus := "False"
	if verdict.Class == contract.Healthy {
		conditionStatus = "True"
	}
	transitionTime := time.Now().UTC().Format(time.RFC3339)
	if previousTransitionTime := previousConditionTransitionTime(resource, "Healthy", conditionStatus, string(verdict.Class)); previousTransitionTime != "" {
		transitionTime = previousTransitionTime
	}
	status["conditions"] = []interface{}{map[string]interface{}{
		"type":               "Healthy",
		"status":             conditionStatus,
		"lastTransitionTime": transitionTime,
		"reason":             string(verdict.Class),
		"message":            verdict.Summary,
	}}
	return status
}

func previousConditionTransitionTime(resource *unstructured.Unstructured, conditionType, conditionStatus, reason string) string {
	conditions, found, err := unstructured.NestedSlice(resource.Object, "status", "conditions")
	if err != nil || !found {
		return ""
	}
	for _, item := range conditions {
		condition, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if condition["type"] == conditionType && condition["status"] == conditionStatus && condition["reason"] == reason {
			if previous, ok := condition["lastTransitionTime"].(string); ok {
				return previous
			}
		}
	}
	return ""
}

// sortedMatrixKeys returns the matrix keys in sorted order so truncation is deterministic.
func sortedMatrixKeys(matrix contract.Matrix) []string {
	keys := make([]string, 0, len(matrix))
	for key := range matrix {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// matrixToStatusEntries converts the given matrix keys to status entries.
// Callers pass the already-sorted, already-capped key slice.
func matrixToStatusEntries(matrix contract.Matrix, keys []string) []interface{} {
	entries := make([]interface{}, 0, len(keys))
	for _, key := range keys {
		result := matrix[key]
		entry := map[string]interface{}{
			"key":              key,
			"success":          result.Success,
			"lossRate":         result.LossRate,
			"rttMillis":        result.RTTMillis,
			"rttMinMillis":     result.RTTMinMillis,
			"rttMaxMillis":     result.RTTMaxMillis,
			"jitterMillis":     result.JitterMillis,
			"burstLossMax":     int64(result.BurstLossMax),
			"throughputBps":    result.ThroughputBPS,
			"bandwidthBps":     result.BandwidthBPS,
			"bytesSent":        result.BytesSent,
			"probeCount":       int64(result.ProbeCount),
			"successfulProbes": int64(result.Successful),
			"durationMillis":   result.DurationMillis,
			"error":            result.Error,
			"agentError":       result.AgentError,
		}
		if src, dst := splitPairKey(key); dst != "" {
			entry["source"] = src
			entry["destination"] = dst
		}
		entries = append(entries, entry)
	}
	return entries
}

func stringSliceToInterfaces(values []string) []interface{} {
	result := make([]interface{}, len(values))
	for i, value := range values {
		result[i] = value
	}
	return result
}

func splitPairKey(key string) (string, string) {
	parts := strings.SplitN(key, "->", 2)
	if len(parts) != 2 {
		return key, ""
	}
	return parts[0], parts[1]
}
