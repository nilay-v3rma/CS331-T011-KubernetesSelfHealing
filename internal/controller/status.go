package controller

import (
	"sort"
	"strings"
	"time"

	"github.com/nilay-v3rma/pod_to_pod_connectivity_operator_kubernetes/pkg/contract"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const maxStatusEntries = 500

// buildNetworkHealthCheckStatus constructs the status map for a NetworkHealthCheck resource.
// It caps the reachability entries to maxStatusEntries (500) and annotates the status with
// reachabilityTruncated / reachabilityTotal when the matrix exceeds that limit.
// It also collects up to 50 degraded pair keys (Success && LossRate >= 0.05).
func buildNetworkHealthCheckStatus(resource *unstructured.Unstructured, verdict contract.Verdict, matrix contract.Matrix) map[string]interface{} {
	truncated := len(matrix) > maxStatusEntries

	status := map[string]interface{}{
		"observedGeneration":       resource.GetGeneration(),
		"lastProbeTime":            time.Now().UTC().Format(time.RFC3339),
		"verdict":                  string(verdict.Class),
		"classification":           string(verdict.Class),
		"classificationConfidence": verdict.Confidence,
		"suspectNodes":             stringSliceToInterfaces(verdict.SuspectNodes),
		"reachability":             matrixToStatusEntries(matrix),
		"summary":                  verdict.Summary,
	}

	if truncated {
		status["reachabilityTruncated"] = true
		status["reachabilityTotal"] = len(matrix)
	}

	// Collect degraded pairs (Success && LossRate >= 0.05), bounded to 50.
	sortedKeys := make([]string, 0, len(matrix))
	for k := range matrix {
		sortedKeys = append(sortedKeys, k)
	}
	sort.Strings(sortedKeys)

	const maxDegradedPairs = 50
	degraded := make([]interface{}, 0, maxDegradedPairs)
	for _, key := range sortedKeys {
		if len(degraded) >= maxDegradedPairs {
			break
		}
		r := matrix[key]
		if r.Success && r.LossRate >= 0.05 {
			degraded = append(degraded, key)
		}
	}
	status["degradedPairs"] = degraded

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

// matrixToStatusEntries converts the matrix to a slice of status entries,
// capping at maxStatusEntries (500). Keys are sorted before iteration.
func matrixToStatusEntries(matrix contract.Matrix) []interface{} {
	keys := make([]string, 0, len(matrix))
	for key := range matrix {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	limit := len(keys)
	if limit > maxStatusEntries {
		limit = maxStatusEntries
	}

	entries := make([]interface{}, 0, limit)
	for _, key := range keys[:limit] {
		parts := strings.SplitN(key, "->", 2)
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
		if len(parts) == 2 {
			entry["source"] = parts[0]
			entry["destination"] = parts[1]
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
