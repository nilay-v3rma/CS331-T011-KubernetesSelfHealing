package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nilay-v3rma/pod_to_pod_connectivity_operator_kubernetes/pkg/contract"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestBuildAgentEndpointsFromPodsFilters(t *testing.T) {
	pods := []corev1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "netprobe-agent"}},
			Spec:       corev1.PodSpec{NodeName: "node-a"},
			Status:     corev1.PodStatus{Phase: corev1.PodRunning, PodIP: "10.0.0.1"},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "netprobe-agent"}},
			Spec:       corev1.PodSpec{NodeName: "node-b"},
			Status:     corev1.PodStatus{Phase: corev1.PodPending, PodIP: "10.0.0.2"},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "netprobe-agent"}},
			Spec:       corev1.PodSpec{NodeName: "node-c"},
			Status:     corev1.PodStatus{Phase: corev1.PodRunning},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "other"}},
			Spec:       corev1.PodSpec{NodeName: "node-d"},
			Status:     corev1.PodStatus{Phase: corev1.PodRunning, PodIP: "10.0.0.4"},
		},
	}

	for _, pod := range pods {
		t.Logf("input pod: labels=%v phase=%s node=%q podIP=%q", pod.Labels, pod.Status.Phase, pod.Spec.NodeName, pod.Status.PodIP)
	}

	endpoints := buildAgentEndpointsFromPods(pods)
	t.Logf("discovered endpoints: %#v", endpoints)
	if len(endpoints) != 1 {
		t.Fatalf("expected 1 valid agent endpoint, got %d: %#v", len(endpoints), endpoints)
	}

	want := []contract.Endpoint{{
		NodeName:   "node-a",
		PodIP:      "10.0.0.1",
		ControlURL: "http://10.0.0.1:8080",
	}}

	if !reflect.DeepEqual(endpoints, want) {
		t.Fatalf("unexpected endpoints: got %#v want %#v", endpoints, want)
	}
}

func TestRunProbeRoundBuildsMatrix(t *testing.T) {
	serverA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/probe" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(contract.ProbeResult{
			Success:    true,
			LossRate:   0,
			RTTMillis:  12.5,
			Error:      "",
			AgentError: false,
		})
	}))
	defer serverA.Close()

	serverB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/probe" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(contract.ProbeResult{
			Success:    true,
			LossRate:   0,
			RTTMillis:  9.1,
			Error:      "",
			AgentError: false,
		})
	}))
	defer serverB.Close()

	reconciler := &Reconciler{
		Agents: []contract.Endpoint{
			{NodeName: "node-a", PodIP: "127.0.0.1", ControlURL: serverA.URL},
			{NodeName: "node-b", PodIP: "127.0.0.2", ControlURL: serverB.URL},
		},
	}

	matrix, err := reconciler.runProbeRound(context.Background(), NetworkHealthCheckSpec{ProbeTimeout: 2 * time.Second}, reconciler.Agents)
	if err != nil {
		t.Fatalf("runProbeRound returned error: %v", err)
	}

	if len(matrix) == 0 {
		t.Fatalf("expected non-empty matrix")
	}

	if _, ok := matrix["node-a->node-b"]; !ok {
		t.Fatalf("expected matrix key node-a->node-b, got %#v", matrix)
	}

	if _, ok := matrix["node-b->node-a"]; !ok {
		t.Fatalf("expected matrix key node-b->node-a, got %#v", matrix)
	}

	for key, value := range matrix {
		t.Logf("matrix[%s] = success=%v lossRate=%v rttMillis=%v error=%q agentError=%v", key, value.Success, value.LossRate, value.RTTMillis, value.Error, value.AgentError)
	}

	verdict := StubClassifier(matrix)
	if verdict.Class != contract.Healthy {
		t.Fatalf("expected healthy verdict from stub classifier, got %q", verdict.Class)
	}
}

func TestProbeRoundPrintsMatrixAndPassFail(t *testing.T) {
	var activeProbes atomic.Int32
	var maxActiveProbes atomic.Int32

	newAgentServer := func() *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/probe" {
				t.Errorf("FAIL: unexpected probe path %q", r.URL.Path)
			}
			if r.URL.Query().Get("target") == "" {
				t.Errorf("FAIL: probe target is empty")
			}

			current := activeProbes.Add(1)
			for {
				previous := maxActiveProbes.Load()
				if current <= previous || maxActiveProbes.CompareAndSwap(previous, current) {
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
			activeProbes.Add(-1)

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(contract.ProbeResult{
				Success:    true,
				LossRate:   0,
				RTTMillis:  10,
				Error:      "",
				AgentError: false,
			})
		}))
	}

	serverA := newAgentServer()
	defer serverA.Close()
	serverB := newAgentServer()
	defer serverB.Close()
	serverC := newAgentServer()
	defer serverC.Close()

	endpoints := []contract.Endpoint{
		{NodeName: "node-a", PodIP: "10.0.0.1", ControlURL: serverA.URL},
		{NodeName: "node-b", PodIP: "10.0.0.2", ControlURL: serverB.URL},
		{NodeName: "node-c", PodIP: "10.0.0.3", ControlURL: serverC.URL},
	}
	reconciler := &Reconciler{}
	matrix, err := reconciler.runProbeRound(context.Background(), NetworkHealthCheckSpec{
		ProbeTimeout:        2 * time.Second,
		MaxConcurrentProbes: 2,
	}, endpoints)
	if err != nil {
		t.Fatalf("runProbeRound returned error: %v", err)
	}

	check := func(name string, passed bool, detail string) {
		status := "PASS"
		if !passed {
			status = "FAIL"
			t.Errorf("%s: %s", name, detail)
		}
		t.Logf("%s: %s - %s", status, name, detail)
	}

	check("matrix size", len(matrix) == 6, fmt.Sprintf("got %d entries, want 6", len(matrix)))
	check("bounded concurrency", maxActiveProbes.Load() <= 2, fmt.Sprintf("maximum active probes=%d, limit=2", maxActiveProbes.Load()))

	for _, source := range endpoints {
		for _, destination := range endpoints {
			if source.NodeName == destination.NodeName {
				continue
			}

			key := fmt.Sprintf("%s->%s", source.NodeName, destination.NodeName)
			result, found := matrix[key]
			check(key, found, "directed pair exists in matrix")
			if !found {
				continue
			}

			fieldsValid := result.Success && result.LossRate == 0 && result.RTTMillis == 10 && result.Error == "" && !result.AgentError
			check(key+" result", fieldsValid, fmt.Sprintf("success=%v lossRate=%v rttMillis=%v error=%q agentError=%v", result.Success, result.LossRate, result.RTTMillis, result.Error, result.AgentError))
			t.Logf("matrix[%s] = success=%v lossRate=%v rttMillis=%v error=%q agentError=%v", key, result.Success, result.LossRate, result.RTTMillis, result.Error, result.AgentError)
		}
	}
}

func TestDiscoverAgentsUsesKubernetesPods(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "agent-a",
				Namespace: "netprobe-system",
				Labels:    map[string]string{"app": "netprobe-agent"},
			},
			Spec:   corev1.PodSpec{NodeName: "node-a"},
			Status: corev1.PodStatus{Phase: corev1.PodRunning, PodIP: "10.0.0.1"},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "agent-b",
				Namespace: "netprobe-system",
				Labels:    map[string]string{"app": "netprobe-agent"},
			},
			Spec:   corev1.PodSpec{NodeName: "node-b"},
			Status: corev1.PodStatus{Phase: corev1.PodPending, PodIP: "10.0.0.2"},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "other",
				Namespace: "netprobe-system",
				Labels:    map[string]string{"app": "other"},
			},
			Spec:   corev1.PodSpec{NodeName: "node-c"},
			Status: corev1.PodStatus{Phase: corev1.PodRunning, PodIP: "10.0.0.3"},
		},
	)

	reconciler := &Reconciler{Client: client, Namespace: "netprobe-system"}
	endpoints, err := reconciler.discoverAgents(context.Background())
	if err != nil {
		t.Fatalf("discoverAgents returned error: %v", err)
	}

	if len(endpoints) != 1 {
		t.Fatalf("expected 1 discovered agent, got %d: %#v", len(endpoints), endpoints)
	}

	if endpoints[0].NodeName != "node-a" || endpoints[0].PodIP != "10.0.0.1" || endpoints[0].ControlURL != "http://10.0.0.1:8080" {
		t.Fatalf("unexpected discovered endpoint: %#v", endpoints[0])
	}
}

func TestReconcileNetworkHealthCheckUpdatesStatus(t *testing.T) {
	newProbeServer := func() *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(contract.ProbeResult{
				Success:    true,
				LossRate:   0,
				RTTMillis:  12.5,
				AgentError: false,
			})
		}))
	}

	serverA := newProbeServer()
	defer serverA.Close()
	serverB := newProbeServer()
	defer serverB.Close()

	resource := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "network.selfheal.io/v1alpha1",
		"kind":       "NetworkHealthCheck",
		"metadata": map[string]interface{}{
			"name":       "cluster-mesh",
			"generation": int64(7),
		},
	}}
	dynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), resource)
	updateAttempts := 0
	dynamicClient.PrependReactor("update", "networkhealthchecks", func(action k8stesting.Action) (bool, runtime.Object, error) {
		updateAttempts++
		if updateAttempts == 1 {
			return true, nil, apierrors.NewConflict(networkHealthCheckGVR.GroupResource(), "cluster-mesh", fmt.Errorf("simulated conflict"))
		}
		return false, nil, nil
	})

	reconciler := &Reconciler{
		DynamicClient: dynamicClient,
		Agents: []contract.Endpoint{
			{NodeName: "node-a", PodIP: "10.0.0.1", ControlURL: serverA.URL},
			{NodeName: "node-b", PodIP: "10.0.0.2", ControlURL: serverB.URL},
		},
	}
	verdict, matrix, err := reconciler.ReconcileNetworkHealthCheck(context.Background(), "cluster-mesh", NetworkHealthCheckSpec{
		ProbeTimeout:        2 * time.Second,
		MaxConcurrentProbes: 2,
	})
	if err != nil {
		t.Fatalf("ReconcileNetworkHealthCheck returned error: %v", err)
	}

	for key, result := range matrix {
		t.Logf("matrix[%s] = success=%v lossRate=%v rttMillis=%v error=%q agentError=%v", key, result.Success, result.LossRate, result.RTTMillis, result.Error, result.AgentError)
	}

	check := func(name string, passed bool, detail string) {
		status := "PASS"
		if !passed {
			status = "FAIL"
			t.Errorf("%s: %s", name, detail)
		}
		t.Logf("%s: %s - %s", status, name, detail)
	}

	check("classifier verdict", verdict.Class == contract.Healthy, fmt.Sprintf("got %q", verdict.Class))
	check("conflict retry", updateAttempts == 2, fmt.Sprintf("status update attempts=%d, want 2", updateAttempts))

	updated, err := dynamicClient.Resource(networkHealthCheckGVR).Get(context.Background(), "cluster-mesh", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get updated NetworkHealthCheck: %v", err)
	}
	status, ok := updated.Object["status"].(map[string]interface{})
	if !ok {
		t.Fatalf("status was not written as an object: %#v", updated.Object["status"])
	}

	observedGeneration, generationOK := status["observedGeneration"].(int64)
	check("observed generation", generationOK && observedGeneration == 7, fmt.Sprintf("got %#v", status["observedGeneration"]))
	check("last probe time", status["lastProbeTime"] != "", fmt.Sprintf("got %#v", status["lastProbeTime"]))
	check("verdict status", status["verdict"] == "Healthy", fmt.Sprintf("got %#v", status["verdict"]))
	check("classification status", status["classification"] == "Healthy", fmt.Sprintf("got %#v", status["classification"]))
	reachability, reachabilityOK := status["reachability"].([]interface{})
	check("reachability status", reachabilityOK && len(reachability) == 2, fmt.Sprintf("got %#v", status["reachability"]))
	conditions, conditionsOK := status["conditions"].([]interface{})
	check("conditions status", conditionsOK && len(conditions) == 1, fmt.Sprintf("got %#v", status["conditions"]))
}

func TestProbeEndpointParsesResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/probe" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(contract.ProbeResult{
			Success:    true,
			LossRate:   0,
			RTTMillis:  13.2,
			Error:      "",
			AgentError: false,
		})
	}))
	defer server.Close()

	result, err := probeEndpoint(context.Background(), server.URL, "10.0.0.1", 2*time.Second)
	if err != nil {
		t.Fatalf("probeEndpoint returned error: %v", err)
	}

	if !result.Success {
		t.Fatalf("expected probe result success=true")
	}

	if result.LossRate != 0 {
		t.Fatalf("expected lossRate 0, got %v", result.LossRate)
	}
}

func TestPlanRemediationPrintsMatrixAndPassFail(t *testing.T) {
	matrix := contract.Matrix{
		"node-a->node-b": {Success: false, LossRate: 1, RTTMillis: 0, Error: "timeout", AgentError: false},
		"node-b->node-a": {Success: true, LossRate: 0, RTTMillis: 12.5, AgentError: false},
	}
	for key, result := range matrix {
		t.Logf("matrix[%s] = success=%v lossRate=%v rttMillis=%v error=%q agentError=%v", key, result.Success, result.LossRate, result.RTTMillis, result.Error, result.AgentError)
	}

	check := func(name string, passed bool, detail string) {
		status := "PASS"
		if !passed {
			status = "FAIL"
			t.Errorf("%s: %s", name, detail)
		}
		t.Logf("%s: %s - %s", status, name, detail)
	}

	tests := []struct {
		name   string
		class  contract.Classification
		action RemediationAction
	}{
		{name: "healthy", class: contract.Healthy, action: NoAction},
		{name: "pair local failure", class: contract.PairLocalFailure, action: RestartAffectedPair},
		{name: "node ingress failure", class: contract.NodeIngressFailure, action: RestartNodeIngress},
		{name: "node egress failure", class: contract.NodeEgressFailure, action: RestartNodeEgress},
		{name: "node isolated", class: contract.NodeIsolated, action: RestartSuspectNode},
		{name: "cluster partition", class: contract.ClusterPartition, action: AlertOnly},
		{name: "policy scoped failure", class: contract.PolicyScoped, action: AlertOnly},
		{name: "unknown", class: contract.Unknown, action: NoAction},
	}

	for _, test := range tests {
		verdict := contract.Verdict{Class: test.class, Confidence: 1, SuspectNodes: []string{"node-a"}}
		plan := PlanRemediation(verdict, true, true)
		check(test.name+" action", plan.Action == test.action, fmt.Sprintf("got %q, want %q", plan.Action, test.action))
		check(test.name+" dry-run", plan.DryRun, fmt.Sprintf("dryRun=%v", plan.DryRun))
		executionBlocked := !plan.Execute
		check(test.name+" execution safety", executionBlocked, fmt.Sprintf("execute=%v", plan.Execute))
		t.Logf("remediation[%s] = action=%s targets=%v dryRun=%v execute=%v reason=%q", test.name, plan.Action, plan.Targets, plan.DryRun, plan.Execute, plan.Reason)
	}

	lowConfidence := PlanRemediation(contract.Verdict{
		Class:      contract.NodeIsolated,
		Confidence: 0.5,
	}, true, false)
	check("low confidence refusal", lowConfidence.Action == NoAction && !lowConfidence.Execute, fmt.Sprintf("action=%q execute=%v reason=%q", lowConfidence.Action, lowConfidence.Execute, lowConfidence.Reason))
}

func TestExecuteRemediationDeletesOnlySuspectAgent(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "agent-a",
				Namespace: "netprobe-system",
				Labels:    map[string]string{"app": "netprobe-agent"},
			},
			Spec: corev1.PodSpec{NodeName: "node-a"},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "agent-b",
				Namespace: "netprobe-system",
				Labels:    map[string]string{"app": "netprobe-agent"},
			},
			Spec: corev1.PodSpec{NodeName: "node-b"},
		},
	)

	plan := PlanRemediation(contract.Verdict{
		Class:        contract.NodeIsolated,
		Confidence:   1,
		SuspectNodes: []string{"node-b"},
	}, true, false)
	if !plan.Execute {
		t.Fatalf("expected non-dry-run plan to execute: %#v", plan)
	}

	reconciler := &Reconciler{Client: client, Namespace: "netprobe-system"}
	if err := reconciler.ExecuteRemediation(context.Background(), plan); err != nil {
		t.Fatalf("ExecuteRemediation returned error: %v", err)
	}

	check := func(name string, passed bool, detail string) {
		status := "PASS"
		if !passed {
			status = "FAIL"
			t.Errorf("%s: %s", name, detail)
		}
		t.Logf("%s: %s - %s", status, name, detail)
	}

	_, suspectErr := client.CoreV1().Pods("netprobe-system").Get(context.Background(), "agent-b", metav1.GetOptions{})
	_, otherErr := client.CoreV1().Pods("netprobe-system").Get(context.Background(), "agent-a", metav1.GetOptions{})
	check("suspect agent deleted", apierrors.IsNotFound(suspectErr), fmt.Sprintf("agent-b lookup error=%v", suspectErr))
	check("non-suspect agent preserved", otherErr == nil, fmt.Sprintf("agent-a lookup error=%v", otherErr))
}
