package controller

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nilay-v3rma/pod_to_pod_connectivity_operator_kubernetes/pkg/analysis"
	"github.com/nilay-v3rma/pod_to_pod_connectivity_operator_kubernetes/pkg/contract"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
)

// [P3] Minimal operator skeleton.
// The goal is to: discover agents, probe them once, build a matrix, call the classifier,
// and update status for a NetworkHealthCheck resource.

type NetworkHealthCheckSpec struct {
	Interval            time.Duration
	ProbeTimeout        time.Duration
	ProbeType           string
	ProbeCount          int
	ProbePayloadBytes   int
	Topology            string
	MaxConcurrentProbes int
	RemediationEnabled  bool
	RemediationDryRun   bool
	FailureThreshold    int
	SuccessThreshold    int
}

type Reconciler struct {
	Agents        []contract.Endpoint
	Client        kubernetes.Interface
	DynamicClient dynamic.Interface
	Namespace     string
	metricsMu     sync.RWMutex
	lastMatrix     contract.Matrix
	lastVerdict    contract.Verdict
	lastRound      time.Duration
}

var networkHealthCheckGVR = schema.GroupVersionResource{
	Group:    "network.selfheal.io",
	Version:  "v1alpha1",
	Resource: "networkhealthchecks",
}

func (r *Reconciler) Reconcile(ctx context.Context, spec NetworkHealthCheckSpec) (contract.Verdict, contract.Matrix, error) {
	if spec.Interval == 0 {
		spec.Interval = 15 * time.Second
	}
	if spec.ProbeTimeout == 0 {
		spec.ProbeTimeout = 2 * time.Second
	}
	if spec.MaxConcurrentProbes <= 0 {
		spec.MaxConcurrentProbes = 1
	}
	if spec.ProbeCount <= 0 {
		spec.ProbeCount = 5
	}
	if spec.ProbePayloadBytes <= 0 {
		spec.ProbePayloadBytes = 1024
	}

	if endpoints, err := r.discoverAgents(ctx); err != nil {
		return contract.Verdict{}, nil, err
	} else {
		r.Agents = endpoints
	}

	roundStarted := time.Now()
	matrix, err := r.runProbeRound(ctx, spec, r.Agents)
	if err != nil {
		return contract.Verdict{}, nil, err
	}
	roundDuration := time.Since(roundStarted)

	cfg := analysis.DefaultConfig()
	if spec.FailureThreshold > 0 {
		cfg.FailureThreshold = spec.FailureThreshold
	}
	if spec.SuccessThreshold > 0 {
		cfg.SuccessThreshold = spec.SuccessThreshold
	}
	_, verdict := analysis.Evaluate(matrix, cfg)
	r.recordMetrics(matrix, verdict, roundDuration)
	return verdict, matrix, nil
}

func (r *Reconciler) recordMetrics(matrix contract.Matrix, verdict contract.Verdict, roundDuration time.Duration) {
	r.metricsMu.Lock()
	defer r.metricsMu.Unlock()
	r.lastMatrix = matrix
	r.lastVerdict = verdict
	r.lastRound = roundDuration
}

func (r *Reconciler) WriteMetrics(w io.Writer) {
	r.metricsMu.RLock()
	defer r.metricsMu.RUnlock()

	fmt.Fprintln(w, "# HELP netprobe_manager_up Manager is running")
	fmt.Fprintln(w, "# TYPE netprobe_manager_up gauge")
	fmt.Fprintln(w, "netprobe_manager_up 1")
	fmt.Fprintln(w, "# HELP netprobe_pair_reachable Directed pair reachability, 1 for reachable and 0 for failed")
	fmt.Fprintln(w, "# TYPE netprobe_pair_reachable gauge")
	fmt.Fprintln(w, "# HELP netprobe_pair_rtt_seconds Directed pair average probe RTT")
	fmt.Fprintln(w, "# TYPE netprobe_pair_rtt_seconds gauge")
	fmt.Fprintln(w, "# HELP netprobe_pair_jitter_seconds Directed pair RTT jitter")
	fmt.Fprintln(w, "# TYPE netprobe_pair_jitter_seconds gauge")
	fmt.Fprintln(w, "# HELP netprobe_pair_loss_rate Directed pair probe loss rate")
	fmt.Fprintln(w, "# TYPE netprobe_pair_loss_rate gauge")
	fmt.Fprintln(w, "# HELP netprobe_pair_loss_burst Directed pair maximum consecutive probe failures")
	fmt.Fprintln(w, "# TYPE netprobe_pair_loss_burst gauge")
	fmt.Fprintln(w, "# HELP netprobe_pair_throughput_bytes_per_second Directed pair synthetic payload throughput")
	fmt.Fprintln(w, "# TYPE netprobe_pair_throughput_bytes_per_second gauge")
	fmt.Fprintln(w, "# HELP netprobe_pair_bytes_sent Directed pair bytes sent during the latest round")
	fmt.Fprintln(w, "# TYPE netprobe_pair_bytes_sent gauge")
	fmt.Fprintln(w, "# HELP netprobe_pair_probes_sent Directed pair probes attempted during the latest round")
	fmt.Fprintln(w, "# TYPE netprobe_pair_probes_sent gauge")

	keys := make([]string, 0, len(r.lastMatrix))
	for key := range r.lastMatrix {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		src, dst := splitPairKey(key)
		labels := fmt.Sprintf(`src_node="%s",dst_node="%s"`, promLabel(src), promLabel(dst))
		result := r.lastMatrix[key]
		reachable := 0
		if result.Success {
			reachable = 1
		}
		fmt.Fprintf(w, "netprobe_pair_reachable{%s} %d\n", labels, reachable)
		fmt.Fprintf(w, "netprobe_pair_rtt_seconds{%s} %.9f\n", labels, result.RTTMillis/1000.0)
		fmt.Fprintf(w, "netprobe_pair_jitter_seconds{%s} %.9f\n", labels, result.JitterMillis/1000.0)
		fmt.Fprintf(w, "netprobe_pair_loss_rate{%s} %.6f\n", labels, result.LossRate)
		fmt.Fprintf(w, "netprobe_pair_loss_burst{%s} %d\n", labels, result.BurstLossMax)
		fmt.Fprintf(w, "netprobe_pair_throughput_bytes_per_second{%s} %.6f\n", labels, result.ThroughputBPS)
		fmt.Fprintf(w, "netprobe_pair_bytes_sent{%s} %d\n", labels, result.BytesSent)
		fmt.Fprintf(w, "netprobe_pair_probes_sent{%s} %d\n", labels, result.ProbeCount)
	}

	fmt.Fprintln(w, "# HELP netprobe_round_duration_seconds Latest probe round wall-clock duration")
	fmt.Fprintln(w, "# TYPE netprobe_round_duration_seconds gauge")
	fmt.Fprintf(w, "netprobe_round_duration_seconds %.9f\n", r.lastRound.Seconds())
	fmt.Fprintln(w, "# HELP netprobe_mesh_health Mesh health, 1 when latest verdict is Healthy")
	fmt.Fprintln(w, "# TYPE netprobe_mesh_health gauge")
	meshHealthy := 0
	if r.lastVerdict.Class == contract.Healthy {
		meshHealthy = 1
	}
	fmt.Fprintf(w, "netprobe_mesh_health{verdict=\"%s\"} %d\n", promLabel(string(r.lastVerdict.Class)), meshHealthy)
	fmt.Fprintln(w, "# HELP netprobe_classification Latest classification confidence")
	fmt.Fprintln(w, "# TYPE netprobe_classification gauge")
	fmt.Fprintf(w, "netprobe_classification{classification=\"%s\"} %.6f\n", promLabel(string(r.lastVerdict.Class)), r.lastVerdict.Confidence)
}

func buildAgentEndpointsFromPods(pods []corev1.Pod) []contract.Endpoint {
	endpoints := make([]contract.Endpoint, 0, len(pods))
	for _, pod := range pods {
		if pod.Labels["app"] != "netprobe-agent" {
			continue
		}
		if pod.Status.Phase != corev1.PodRunning {
			continue
		}
		if pod.Status.PodIP == "" {
			continue
		}

		endpoints = append(endpoints, contract.Endpoint{
			NodeName:   pod.Spec.NodeName,
			PodIP:      pod.Status.PodIP,
			ControlURL: fmt.Sprintf("http://%s:8080", pod.Status.PodIP),
		})
	}

	return endpoints
}

func (r *Reconciler) discoverAgents(ctx context.Context) ([]contract.Endpoint, error) {
	if r.Client == nil {
		if len(r.Agents) == 0 {
			return []contract.Endpoint{}, nil
		}
		return r.Agents, nil
	}

	namespace := r.Namespace
	if namespace == "" {
		namespace = metav1.NamespaceAll
	}

	list, err := r.Client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=netprobe-agent",
	})
	if err != nil {
		return nil, err
	}

	endpoints := buildAgentEndpointsFromPods(list.Items)
	r.Agents = endpoints
	return endpoints, nil
}

func (r *Reconciler) ReconcileNetworkHealthCheck(ctx context.Context, name string, spec NetworkHealthCheckSpec) (contract.Verdict, contract.Matrix, error) {
	if r.DynamicClient == nil {
		return contract.Verdict{}, nil, fmt.Errorf("dynamic Kubernetes client is required")
	}

	resourceClient := r.DynamicClient.Resource(networkHealthCheckGVR)
	networkHealthCheck, err := resourceClient.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return contract.Verdict{}, nil, err
	}

	verdict, matrix, err := r.Reconcile(ctx, spec)
	if err != nil {
		return contract.Verdict{}, nil, err
	}

	status := buildNetworkHealthCheckStatus(networkHealthCheck, verdict, matrix)
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current, getErr := resourceClient.Get(ctx, name, metav1.GetOptions{})
		if getErr != nil {
			return getErr
		}
		current.Object["status"] = status
		_, updateErr := resourceClient.UpdateStatus(ctx, current, metav1.UpdateOptions{})
		return updateErr
	})
	if err != nil {
		return contract.Verdict{}, nil, err
	}

	plan := PlanRemediation(verdict, spec.RemediationEnabled, spec.RemediationDryRun)
	if err := r.ExecuteRemediation(ctx, plan); err != nil {
		return contract.Verdict{}, nil, err
	}

	return verdict, matrix, nil
}

func buildNetworkHealthCheckStatus(resource *unstructured.Unstructured, verdict contract.Verdict, matrix contract.Matrix) map[string]interface{} {
	status := map[string]interface{}{
		"observedGeneration":        resource.GetGeneration(),
		"lastProbeTime":             time.Now().UTC().Format(time.RFC3339),
		"verdict":                   string(verdict.Class),
		"classification":            string(verdict.Class),
		"classificationConfidence":  verdict.Confidence,
		"suspectNodes":              stringSliceToInterfaces(verdict.SuspectNodes),
		"reachability":              matrixToStatusEntries(matrix),
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

func matrixToStatusEntries(matrix contract.Matrix) []interface{} {
	keys := make([]string, 0, len(matrix))
	for key := range matrix {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	entries := make([]interface{}, 0, len(keys))
	for _, key := range keys {
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
			"burstLossMax":     result.BurstLossMax,
			"throughputBps":    result.ThroughputBPS,
			"bandwidthBps":     result.BandwidthBPS,
			"bytesSent":        result.BytesSent,
			"probeCount":       result.ProbeCount,
			"successfulProbes": result.Successful,
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

func promLabel(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(value, `"`, `\"`)
}
