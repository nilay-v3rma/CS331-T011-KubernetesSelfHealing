package controller

import (
	"context"
	"testing"
	"time"

	"github.com/nilay-v3rma/pod_to_pod_connectivity_operator_kubernetes/pkg/analysis"
	"github.com/nilay-v3rma/pod_to_pod_connectivity_operator_kubernetes/pkg/contract"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestShouldRunHonorsInterval(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name          string
		lastProbeTime string
		interval      time.Duration
		want          bool
	}{
		{name: "recent skips", lastProbeTime: now.Add(-5 * time.Second).Format(time.RFC3339), interval: 15 * time.Second, want: false},
		{name: "old runs", lastProbeTime: now.Add(-20 * time.Second).Format(time.RFC3339), interval: 15 * time.Second, want: true},
		{name: "empty runs", lastProbeTime: "", interval: 15 * time.Second, want: true},
		{name: "invalid runs", lastProbeTime: "not-a-time", interval: 15 * time.Second, want: true},
		{name: "zero interval runs", lastProbeTime: now.Format(time.RFC3339), interval: 0, want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldRun(tc.lastProbeTime, tc.interval); got != tc.want {
				t.Fatalf("shouldRun(%q, %s) = %v, want %v", tc.lastProbeTime, tc.interval, got, tc.want)
			}
		})
	}
}

func TestPlanRemediationIsAlertOnlyForNetworkFailures(t *testing.T) {
	classes := []contract.Classification{
		contract.PairLocalFailure,
		contract.NodeIngressFailure,
		contract.NodeEgressFailure,
		contract.NodeIsolated,
		contract.ClusterPartition,
		contract.PolicyScoped,
		contract.Classification("Degraded"),
	}

	for _, class := range classes {
		t.Run(string(class), func(t *testing.T) {
			plan := PlanRemediation(contract.Verdict{
				Class:        class,
				Confidence:   1.0,
				SuspectNodes: []string{"worker2"},
			}, true, false)

			if plan.Action != AlertOnly {
				t.Fatalf("action = %s, want AlertOnly", plan.Action)
			}
			if plan.Execute {
				t.Fatal("network remediation must not execute")
			}
		})
	}
}

func TestPlanRemediationRefusesNoEvidence(t *testing.T) {
	classes := []contract.Classification{
		contract.Healthy,
		contract.Unknown,
		contract.Classification("NoData"),
	}

	for _, class := range classes {
		t.Run(string(class), func(t *testing.T) {
			plan := PlanRemediation(contract.Verdict{Class: class, Confidence: 1.0}, true, false)
			if plan.Action != NoAction {
				t.Fatalf("action = %s, want NoAction", plan.Action)
			}
			if plan.Execute {
				t.Fatal("remediation must not execute without conclusive evidence")
			}
		})
	}
}

func TestExecuteRemediationNeverDeletesPods(t *testing.T) {
	client := fake.NewSimpleClientset()
	deletes := 0
	client.PrependReactor("delete", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		deletes++
		return true, nil, nil
	})

	reconciler := &Reconciler{Client: client, Namespace: "netprobe-system"}
	plan := RemediationPlan{
		Action:  RestartSuspectNode,
		Targets: []string{"worker2"},
		Execute: true,
	}

	if err := reconciler.ExecuteRemediation(context.Background(), plan); err != nil {
		t.Fatalf("ExecuteRemediation returned error: %v", err)
	}
	if deletes != 0 {
		t.Fatalf("ExecuteRemediation deleted %d pods, want 0", deletes)
	}
}

func TestStabilizeMatrixSuppressesTransientFailure(t *testing.T) {
	cfg := analysis.Config{FailureThreshold: 3, SuccessThreshold: 2}
	tracker := analysis.NewTracker(cfg)
	pair := "worker1->worker2"

	failed := contract.Matrix{pair: {Success: false, LossRate: 1, Error: "dial timeout"}}
	healthy := contract.Matrix{pair: {Success: true, LossRate: 0}}

	tracker.Update(failed)
	stable := stabilizeMatrix(tracker, failed)
	if !stable[pair].Success {
		t.Fatal("first failure should be suppressed by hysteresis")
	}

	tracker.Update(failed)
	tracker.Update(failed)
	stable = stabilizeMatrix(tracker, failed)
	if stable[pair].Success {
		t.Fatal("third consecutive failure should reach the classifier")
	}

	tracker.Update(healthy)
	stable = stabilizeMatrix(tracker, healthy)
	if stable[pair].Success {
		t.Fatal("one success should not recover a degraded pair")
	}

	tracker.Update(healthy)
	stable = stabilizeMatrix(tracker, healthy)
	if !stable[pair].Success {
		t.Fatal("two consecutive successes should recover the pair")
	}
}
