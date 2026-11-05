package controller

import (
	"context"
	"log"

	"github.com/nilay-v3rma/pod_to_pod_connectivity_operator_kubernetes/pkg/contract"
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

// PlanRemediation deliberately selects AlertOnly for network failures.
// Deleting a probe agent is a measurement-plane restart, not a repair of the
// data plane, and it can erase an injected fault. Automatic network repair is
// deferred until the evidence and rollback model in the future design exists.
func PlanRemediation(verdict contract.Verdict, enabled, dryRun bool) RemediationPlan {
	plan := RemediationPlan{
		Action:  NoAction,
		Targets: append([]string(nil), verdict.SuspectNodes...),
		DryRun:  dryRun,
	}

	class := string(verdict.Class)
	switch class {
	case string(contract.Healthy):
		plan.Reason = "healthy verdict; no remediation required"
		return plan
	case string(contract.Unknown), "NoData":
		plan.Reason = "no conclusive data-plane evidence; remediation refused"
		return plan
	}
	if verdict.Confidence < minimumConfidence {
		plan.Reason = "low-confidence verdict; remediation refused"
		return plan
	}

	switch class {
	case string(contract.PairLocalFailure),
		string(contract.NodeIngressFailure),
		string(contract.NodeEgressFailure),
		string(contract.NodeIsolated),
		string(contract.ClusterPartition),
		string(contract.PolicyScoped),
		"Degraded":
		plan.Action = AlertOnly
		plan.Reason = "network fault requires investigation; automatic remediation is disabled"
	default:
		plan.Reason = "unrecognized verdict; remediation refused"
		return plan
	}

	if !enabled {
		plan.Reason += "; remediation disabled"
	}
	// AlertOnly never executes a mutating action.
	plan.Execute = false
	return plan
}

// ExecuteRemediation is intentionally non-destructive. It records the intended
// action and returns without touching pods or nodes.
func (r *Reconciler) ExecuteRemediation(ctx context.Context, plan RemediationPlan) error {
	if !plan.Execute || plan.Action == NoAction || plan.Action == AlertOnly {
		return nil
	}
	log.Printf("remediation: action %s requested for targets %v; destructive execution is disabled", plan.Action, plan.Targets)
	return nil
}
