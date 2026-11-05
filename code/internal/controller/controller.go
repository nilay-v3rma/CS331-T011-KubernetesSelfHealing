package controller

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nilay-v3rma/pod_to_pod_connectivity_operator_kubernetes/pkg/analysis"
	"github.com/nilay-v3rma/pod_to_pod_connectivity_operator_kubernetes/pkg/contract"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	lastMatrix    contract.Matrix
	lastVerdict   contract.Verdict
	lastRound     time.Duration
	tracker       *analysis.Tracker
	trackerConfig analysis.Config
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
	if spec.ProbeType == "" {
		spec.ProbeType = "TCP"
	}
	if !strings.EqualFold(spec.ProbeType, "TCP") {
		return contract.Verdict{}, nil, fmt.Errorf("unsupported probe type %q: only TCP is implemented", spec.ProbeType)
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

	if r.tracker == nil ||
		r.trackerConfig.FailureThreshold != cfg.FailureThreshold ||
		r.trackerConfig.SuccessThreshold != cfg.SuccessThreshold {
		r.tracker = analysis.NewTracker(cfg)
		r.trackerConfig = cfg
	}
	r.tracker.Update(matrix)
	stableMatrix := stabilizeMatrix(r.tracker, matrix)

	_, verdict := analysis.Evaluate(stableMatrix, cfg)
	r.recordMetrics(stableMatrix, verdict, roundDuration)
	return verdict, stableMatrix, nil
}

// stabilizeMatrix applies the per-pair failure/success thresholds before
// classification so a single probe round cannot flip the reported health.
func stabilizeMatrix(tracker *analysis.Tracker, matrix contract.Matrix) contract.Matrix {
	stable := make(contract.Matrix, len(matrix))
	for key, result := range matrix {
		state := tracker.GetState(key)
		if state == nil {
			stable[key] = result
			continue
		}

		switch state.Status {
		case analysis.PairHealthy:
			if !result.Success {
				result.Success = true
				result.LossRate = 0
				result.Error = "hysteresis: transient failure suppressed"
			}
		case analysis.PairDegraded:
			if result.Success {
				result.Success = false
				result.LossRate = 1
				result.Error = "hysteresis: recovery pending"
			}
		}
		stable[key] = result
	}
	return stable
}

func (r *Reconciler) recordMetrics(matrix contract.Matrix, verdict contract.Verdict, roundDuration time.Duration) {
	r.metricsMu.Lock()
	defer r.metricsMu.Unlock()
	r.lastMatrix = matrix
	r.lastVerdict = verdict
	r.lastRound = roundDuration
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
