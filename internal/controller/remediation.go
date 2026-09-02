package controller

import (
	"context"
	"fmt"
	"log"

	"github.com/nilay-v3rma/pod_to_pod_connectivity_operator_kubernetes/pkg/contract"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type RemediationAction string

const (
	NoAction            RemediationAction = "NoAction"
	RestartAffectedPair RemediationAction = "RestartAffectedPair"
	RestartNodeIngress  RemediationAction = "RestartNodeIngress"
	RestartNodeEgress   RemediationAction = "RestartNodeEgress"
	RestartSuspectNode  RemediationAction = "RestartSuspectNode"
	AlertOnly           RemediationAction = "AlertOnly"
	minimumConfidence                     = 0.8
)

type RemediationPlan struct {
	Action  RemediationAction
	Targets []string
	DryRun  bool
	Execute bool
	Reason  string
}

func PlanRemediation(verdict contract.Verdict, enabled, dryRun bool) RemediationPlan {
	plan := RemediationPlan{
		Action:  NoAction,
		Targets: append([]string(nil), verdict.SuspectNodes...),
		DryRun:  dryRun,
	}

	if verdict.Class == contract.Healthy {
		plan.Reason = "healthy verdict; no remediation required"
		return plan
	}
	if verdict.Class == contract.Unknown {
		plan.Reason = "unknown verdict; remediation refused"
		return plan
	}
	if verdict.Confidence < minimumConfidence {
		plan.Reason = "low-confidence verdict; remediation refused"
		return plan
	}

	switch verdict.Class {
	case contract.PairLocalFailure:
		plan.Action = RestartAffectedPair
	case contract.NodeIngressFailure:
		plan.Action = RestartNodeIngress
	case contract.NodeEgressFailure:
		plan.Action = RestartNodeEgress
	case contract.NodeIsolated:
		plan.Action = RestartSuspectNode
	case contract.ClusterPartition, contract.PolicyScoped:
		plan.Action = AlertOnly
		plan.Reason = "requires investigation; automatic restart is unsafe"
	default:
		plan.Reason = "unrecognized verdict; remediation refused"
		return plan
	}

	if plan.Reason == "" {
		plan.Reason = "dry-run remediation plan generated"
	}
	if plan.Action == AlertOnly {
		return plan
	}
	if !enabled {
		plan.Execute = false
		plan.Reason = "remediation disabled; action recorded only"
		return plan
	}
	plan.Execute = !dryRun
	return plan
}

func (r *Reconciler) ExecuteRemediation(ctx context.Context, plan RemediationPlan) error {
	if !plan.Execute || plan.Action == NoAction || plan.Action == AlertOnly {
		return nil
	}
	if r.Client == nil {
		return fmt.Errorf("Kubernetes client is required for remediation")
	}

	namespace := r.Namespace
	if namespace == "" {
		namespace = metav1.NamespaceAll
	}
	pods, err := r.Client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=netprobe-agent",
	})
	if err != nil {
		return err
	}

	targets := make(map[string]struct{}, len(plan.Targets))
	for _, target := range plan.Targets {
		targets[target] = struct{}{}
	}
	if len(targets) == 0 {
		return fmt.Errorf("remediation action %s has no targets", plan.Action)
	}

	for _, pod := range pods.Items {
		if _, ok := targets[pod.Spec.NodeName]; !ok {
			continue
		}
		if err := r.Client.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{}); err != nil {
			return fmt.Errorf("delete agent pod %s/%s: %w", pod.Namespace, pod.Name, err)
		}
		log.Printf("remediation: deleted agent pod %s/%s for action %s", pod.Namespace, pod.Name, plan.Action)
	}
	return nil
}
