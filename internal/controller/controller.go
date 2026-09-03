package controller

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

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
	Topology            string
	MaxConcurrentProbes int
	RemediationEnabled  bool
	RemediationDryRun   bool
}

type Reconciler struct {
	Agents        []contract.Endpoint
	Client        kubernetes.Interface
	DynamicClient dynamic.Interface
	Namespace     string
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

	if endpoints, err := r.discoverAgents(ctx); err != nil {
		return contract.Verdict{}, nil, err
	} else {
		r.Agents = endpoints
	}

	matrix, err := r.runProbeRound(ctx, spec, r.Agents)
	if err != nil {
		return contract.Verdict{}, nil, err
	}

	verdict := StubClassifier(matrix)
	return verdict, matrix, nil
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
		"observedGeneration": resource.GetGeneration(),
		"lastProbeTime":      time.Now().UTC().Format(time.RFC3339),
		"verdict":            string(verdict.Class),
		"classification":     string(verdict.Class),
		"suspectNodes":       stringSliceToInterfaces(verdict.SuspectNodes),
		"reachability":       matrixToStatusEntries(matrix),
	}
	conditionStatus := "False"
	if verdict.Class == contract.Healthy {
		conditionStatus = "True"
	}
	status["conditions"] = []interface{}{map[string]interface{}{
		"type":               "Healthy",
		"status":             conditionStatus,
		"lastTransitionTime": time.Now().UTC().Format(time.RFC3339),
		"reason":             string(verdict.Class),
		"message":            verdict.Summary,
	}}
	return status
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
			"key":        key,
			"success":    result.Success,
			"lossRate":   result.LossRate,
			"rttMillis":  result.RTTMillis,
			"error":      result.Error,
			"agentError": result.AgentError,
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
