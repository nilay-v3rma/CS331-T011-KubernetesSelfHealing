# Ridham — integration, spec truth, safe remediation

### prompt 1
```text
This is the repository of the 1st part of our CN project. Few things from our presentation:

1. Question every design choice (each and every) in the project as mentioned above, and see if we can have a better choice with a reason in what we are currently doing.
2. Additionally, look at the problem. Extending Kubernetes Self-Healing for Networking Failure is the aim. Look at what the SOTA is solving. What are the problems left out with existing soln in the repo and the SOTA (should have the practical usability relevance).

Create a separate lookforward.md where you write down everything
```

### prompt 2
```text
You are the integration lead on a 4-day viva delivery for a Kubernetes network self-healing operator. Work in the repository that contains pkg/analysis/classifier.go, pkg/prober.go, internal/controller/, config/crd/, and config/samples/. Read this entire prompt before editing.

CONTEXT YOU MUST UNDERSTAND
The current operator discovers netprobe-agent pods, probes every agent-to-agent TCP path, classifies the matrix, writes status to a cluster-scoped NetworkHealthCheck CR, and can "remediate" by deleting agent pods. The viva is in a few days. We are not building the full future design in lookforward.md. We are fixing the trust and safety bugs so the system cannot claim health without evidence.

Current concrete bugs:
- An empty matrix is classified Healthy with confidence 1.0.
- spec.interval, spec.failureThreshold, spec.successThreshold, spec.probeType, spec.topology, and spec.sampleDegree are parsed or declared but do not affect the live path.
- remediation defaults to enabled/dryRun false in the sample, and ExecuteRemediation deletes agent pods. Deleting a probe agent is not a network fix and can erase an injected fault.
- The status writes the full matrix, risking the etcd object-size limit.

YOUR SCOPE — do only this
1. V4 spec truth:
   - Honor spec.interval per NetworkHealthCheck object. In NetworkHealthCheckController.ReconcileAll, read status.lastProbeTime, parse RFC3339, and skip the round if time.Since(last) < spec.Interval. If absent/invalid, run.
   - Pass spec.probeType through to probeEndpoint. Support only TCP. Change the CRD enum to only TCP. Remove HTTP/ICMP.
   - Remove topology and sampleDegree from NetworkHealthCheckSpec, networkHealthCheckSpecFromResource, the CRD, and the sample YAML. Do not leave inert fields.
   - Keep failureThreshold and successThreshold parsing and wire them into the analysis tracker (Anurag owns Tracker; coordinate).
2. V7 safe remediation:
   - Default remediation.enabled to false and dryRun to true in the CRD/sample.
   - PlanRemediation must return AlertOnly for every network failure class (PairLocalFailure, NodeIngressFailure, NodeEgressFailure, NodeIsolated, ClusterPartition, PolicyScopedFailure, Degraded). NoData, Unknown, and Healthy must never execute.
   - ExecuteRemediation must not delete pods. Log the intended action and return nil. Remove the destructive delete loop.
   - Add a test with the fake Kubernetes client asserting zero delete actions for NodeIsolated, Unknown, NoData, and Degraded.
3. Manager auth wiring:
   - Add AGENT_PROBE_TOKEN to config/manager/deployment.yaml, sourced from the agent Secret (coordinate with Eeshan, who owns config/agent/secret.yaml).
   - Do not edit internal/controller/probe.go directly if Eeshan is active on it; if you must, coordinate first.
4. Integration:
   - Merge order: Anurag -> Eeshan -> you -> Nilay -> Siddhesh.
   - Run go vet ./... and go test ./... -count=1 after each merge.
   - Build and deploy on KIND, then run the demo steps.
5. Viva narrative:
   - Own the explanation of the problem, architecture, CRD/API, controller loop, why remediation is disabled, and what is deferred to lookforward.md.

FILES YOU OWN
internal/controller/controller.go
internal/controller/networkhealthcheck_controller.go
internal/controller/remediation.go
config/crd/networkhealthcheck-crd.yaml
config/samples/networkhealthcheck-sample.yaml
config/manager/deployment.yaml
config/kustomization.yaml
README.md is owned by Nilay; do not edit it.

DO NOT
- Do not implement NetworkIntent, v1beta1, eBPF, policy repair, or auto-remediation.
- Do not refactor unrelated code.
- Do not edit pkg/contract/types.go (Anurag), pkg/analysis/* (Anurag), pkg/prober.go/pkg/server.go (Eeshan), hack/* (Siddhesh), or config/monitoring/* (Nilay).
- Do not remove tests to make the build pass.

IMPLEMENTATION NOTES
- For interval: status.lastProbeTime is already written by buildNetworkHealthCheckStatus. Add a small helper shouldRun(lastProbeTime string, interval time.Duration) bool with unit tests.
- For spec fields: after removal, grep the repo for topology and sampleDegree and make sure there are zero references.
- For remediation: keep the RemediationPlan type and DryRun/Execute fields for the future, but make the safety invariant explicit in comments and tests.
- For status: do not change its shape beyond what is needed; Nilay owns the status cap.
- If a change requires a contract field, send the exact request to Anurag instead of editing his file.

TESTS AND ACCEPTANCE
- go test ./... -count=1 must pass.
- Test that an object with lastProbeTime 5s ago and interval 15s is skipped; 20s ago runs.
- Test that an unsupported probeType cannot be created (CRD enum) and that TCP is passed through.
- Test that remediation never calls Delete on pods.
- kubectl apply -k config/ succeeds; kubectl get nhc shows a status.
- After the change, a reviewer should be able to ask "which spec fields are real?" and you can point to code for every field.

FINAL RESPONSE
Report:
1. Files changed and why.
2. Tests run and exact results.
3. Any coordination that is still required.
4. A 10-line viva explanation of the controller loop, spec truth, and safe remediation.

TEACH ME FOR THE VIVA
In your final response, explain in plain language:
- How a reconcile round flows from CR -> discovery -> probe -> analysis -> status.
- Why an inert YAML field is a correctness bug.
- Why deleting the probe agent is not remediation.
- What you would build next from lookforward.md and why it is not in this week's scope.
```
