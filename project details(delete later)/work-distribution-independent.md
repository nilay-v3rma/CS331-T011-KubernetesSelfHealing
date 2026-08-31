# Work Distribution (Low-Coupling Variant)
### 5 people, 4 days, pod-to-pod connectivity operator

Replaces the layered split with an **artifact split**. Each person owns a separately buildable, separately testable, separately demoable thing. Nobody waits on anybody for 2.5 of the 4 days.

Companion to `k8s-connectivity-operator-plan.md`, which defines what is being built.

---

## 1. The Coupling Problem, and the Fix

A layer split (types, controller, prober, classifier) forces everyone into `internal/controller/` at the same time. This version splits by artifact instead.

| Mechanism | Effect |
| :--- | :--- |
| **One frozen contract file**, written in the first 2 hours, read-only thereafter | Removes all mid-project negotiation |
| **Every person writes their own fake** for whatever they consume | P3 writes a fake agent so they never wait for P2. P4 and P5 work off recorded JSON fixtures so they never need a cluster at all. |
| **Zero shared source files** | Each person owns a disjoint directory. No merge conflicts are structurally possible. |
| **Integration confined to one 3-hour window** on Day 3 | Instead of continuous coordination |

Total mandatory joint time: **about 6.5 hours out of 34**. Everything else is solo.

The trade: writing fakes is duplicated effort, and integration risk concentrates into Day 3 morning. For a 4-day project with 5 people, that is the right trade. Continuous coordination costs more than it saves at this size.

---

## 2. Artifact Ownership (disjoint, no overlaps)

| # | Person | Artifact | Directory (exclusive) | Runs without other people's code? |
| :--- | :--- | :--- | :--- | :--- |
| **P1** | Platform | Cluster lab + images + CI | `hack/`, `config/`, `Dockerfile*`, `Makefile`, `.github/` | Yes, completely |
| **P2** | Agent | Standalone probe agent binary | `cmd/agent/`, `pkg/agent/` | Yes, `go run` on localhost |
| **P3** | Operator | CRD + controller + prober | `api/`, `cmd/manager/`, `internal/controller/` | Yes, against a fake agent P3 writes |
| **P4** | Analysis | Pure Go decision library | `pkg/analysis/` | Yes, offline, no network, no k8s |
| **P5** | Evaluation | Experiment harness + report | `experiments/`, `test/`, `docs/` | Yes, against recorded fixtures |

**Hard rule:** you edit only your own directory. If you need something to change in someone else's, you do not edit it, you file it as an issue at the evening sync.

The only shared file is `pkg/contract/types.go`, which becomes read-only after hour 2.

---

## 3. The Contract (Day 1, hours 0 to 2, everyone in one room)

This is the **only** mandatory collaboration before Day 3. Produce three files, commit them, then disperse.

### 3.1 `pkg/contract/types.go`

```go
package contract

// Agent /probe response. Frozen.
type ProbeResult struct {
    Success    bool    `json:"success"`
    LossRate   float64 `json:"lossRate"`
    RTTMillis  float64 `json:"rttMillis"`
    Error      string  `json:"error,omitempty"`
    AgentError bool    `json:"agentError"` // true = control channel failed, NOT a data plane fault
}

type Endpoint struct {
    NodeName   string `json:"nodeName"`
    PodIP      string `json:"podIP"`
    ControlURL string `json:"controlURL"`
}

// Key format: "srcNode->dstNode"
type Matrix map[string]ProbeResult

type Classification string

const (
    Healthy            Classification = "Healthy"
    PairLocalFailure   Classification = "PairLocalFailure"
    NodeIngressFailure Classification = "NodeIngressFailure"
    NodeEgressFailure  Classification = "NodeEgressFailure"
    NodeIsolated       Classification = "NodeIsolated"
    ClusterPartition   Classification = "ClusterPartition"
    PolicyScoped       Classification = "PolicyScopedFailure"
    Unknown            Classification = "Unknown"
)

type Verdict struct {
    Class        Classification `json:"classification"`
    Confidence   float64        `json:"confidence"`
    SuspectNodes []string       `json:"suspectNodes"`
    Summary      string         `json:"summary"`
}
```

### 3.2 Frozen constants

| Item | Value |
| :--- | :--- |
| Agent control port | `8080` |
| Agent data port | `9376` |
| Agent probe path | `GET /probe?target=<ip>:<port>&timeout=<dur>&count=<n>` |
| Agent label selector | `app=netprobe-agent` |
| Agent namespace | `netprobe-system` |
| CRD group/version/kind | `network.selfheal.io/v1alpha1`, `NetworkHealthCheck`, cluster-scoped, shortName `nhc` |
| Analysis entry point | `analysis.Evaluate(m Matrix, cfg Config) (Matrix, Verdict)` |
| Results CSV columns | `scenario,trial,inject_ts,detect_ts,mttd_ms,predicted_class,true_class,confidence` |

### 3.3 `contracts/fixtures/*.json`

Hand-write 8 example matrices, one per classification, plus 3 ambiguous ones. **This is the most important artifact of the freeze session.** It is what lets P4 and P5 work for three days without touching a cluster.

```
contracts/fixtures/
  healthy.json
  node-ingress-worker2.json
  node-egress-worker2.json
  node-isolated-worker2.json
  pair-local.json
  cluster-partition.json
  policy-scoped.json
  ambiguous-two-nodes.json
  ambiguous-agent-down.json
  flapping-sequence.json      # array of matrices over 10 rounds, for hysteresis
  degraded-partial-loss.json
```

### 3.4 Contract change protocol

| Situation | Action |
| :--- | :--- |
| Need a new field | **Additive only.** Add it, announce at evening sync. Existing consumers keep compiling. |
| Need to rename or remove a field | Not allowed before Day 3 integration. Work around it. |
| Contract is genuinely wrong | Raise it same day at evening sync, whole team decides in 10 minutes |

---

## 4. P1: Platform

**Independence: total.** Zero Go code. Never blocked, never blocks anyone before Day 3.

| Day | Work | Solo exit criterion |
| :--- | :--- | :--- |
| **1** | KIND 4-node config (`disableDefaultCNI`, podSubnet `192.168.0.0/16`), Calico v3.28.0 install, verify manual cross-node pod ping | Cluster up, cross-node ping works, documented in `hack/README.md` |
| **1** | `hack/setup-cluster.sh` and `hack/reset-cluster.sh` (idempotent teardown and rebuild) | Runs twice in a row cleanly |
| **2** | Dockerfiles for agent and manager that build **whatever is in `cmd/`**, using a hello-world stub as a placeholder. `make images && kind load` | Images build and load against stubs |
| **2** | All kustomize manifests: CRD placeholder, RBAC, DaemonSet, Deployment, Service. Minimal RBAC only (`get/list/watch` pods and nodes, CRD + status, `create` events) | `kubectl apply -k config/` succeeds with stub images |
| **2** | `hack/inject-fault.sh` implementing F1 to F6, each printing an injection timestamp | Each scenario verified by hand with `ping` between two plain busybox pods |
| **3** | Prometheus + Grafana stack, scrape config for both components, mesh heatmap dashboard | Prometheus scrapes the stub `/metrics` |
| **3** | GitHub Actions CI: `go vet`, `go test ./...`, `kind` smoke test | CI green on `main` |
| **4** | Clean-machine reproducibility run, README, architecture diagram, 5 min demo script | An outsider reproduces the demo from README alone |

**P1 needs from others:** nothing until Day 3, when real images replace the stubs. The Dockerfile does not care what the Go code does.

---

## 5. P2: Agent

**Independence: total.** A standalone HTTP server. Testable entirely with `go run` and `curl` on a laptop.

| Day | Work | Solo exit criterion |
| :--- | :--- | :--- |
| **1** | Binary skeleton, two listeners: control `:8080`, data `:9376`. Split is mandatory, since a shared port makes control failure indistinguishable from data plane failure. | Both listeners bind |
| **1** | `GET /healthz` returning node name + pod IP from downward API env, with local fallback defaults | `curl localhost:8080/healthz` works outside k8s |
| **1** | `GET /probe`: parse `target`, `timeout`, `count`; `net.DialTimeout` loop; return `contract.ProbeResult` | Two local instances on ports 8080/8081 probe each other |
| **2** | Error taxonomy: dial refused, timeout, DNS failure, no route. Set `AgentError` correctly. | Table test covering all error branches |
| **2** | ICMP and HTTP probe modes behind `probeType`; TCP is the default and needs no extra capability | All three modes work locally |
| **2** | `GET /metrics` with agent-local counters; graceful shutdown; resource limits in the DaemonSet spec | `/metrics` scrapes clean |
| **3** | Integration window, then hardening: concurrent probe safety, target IP validation, timeout tuning | Survives 100 concurrent `/probe` calls |
| **3** | `hack/agent-loadtest.sh`, measure per-probe overhead | Latency and CPU numbers handed to P5 |
| **4** | Support P5's E4/E5 runs, tune defaults, agent-side documentation | Overhead table complete |

**P2 needs from others:** nothing. Not the cluster, not the operator, not the CRD.

**P2's self-test rig:** a `docker compose` file running 4 agent containers on a bridge network. Full mesh probing testable with zero Kubernetes.

---

## 6. P3: Operator

**Independence: high**, via a fake agent that P3 writes on Day 1 in about 30 lines.

| Day | Work | Solo exit criterion |
| :--- | :--- | :--- |
| **1** | `internal/testutil/fakeagent.go`: an `httptest.Server` returning scripted `ProbeResult`s. **Write this first, before anything else.** | Fake agent serves `/probe` |
| **1** | CRD types: spec, status, markers (`+kubebuilder:subresource:status`, cluster scope, printcolumns), `make manifests generate` | `make install` succeeds, sample CR applies |
| **2** | `prober.go`: bounded fan-out with `errgroup` + semaphore, per-probe context deadline strictly less than `spec.interval` | n×n round against 4 fake agents completes in-process |
| **2** | Endpoint discovery: list pods by label + `status.phase=Running`, filter non-empty `podIP` | envtest test with fake pod objects |
| **2** | Reconcile loop, status writer with `retry.RetryOnConflict`, `RequeueAfter: spec.interval`, `observedGeneration` | envtest: status matrix populates and updates |
| **3** | Integration window, then Events on **transition only** (per-round Events flood etcd at 15s), conditions with `lastTransitionTime` | One Event per state change under `kubectl get events -w` |
| **3** | `topology.go`: `FullMesh` = n(n-1) pairs, `Sampled` = k-regular random digraph reseeded per round | Probe count drops to k·n, verified in metrics |
| **3** | Edge cases: zero agents, pod IP churn mid-round, CR deleted mid-probe, node added or removed | Tests for each |
| **4** | Remediation contract stub: `enabled` flag, `dryRun` default true, cooldown, per-node action budget, hard refusal on `Unknown` | Dry-run path tested, nothing destructive can fire |

**P3 needs from others:** `analysis.Evaluate()` from P4, stubbed on Day 1 as `return m, contract.Verdict{Class: contract.Healthy}`. Real agent from P2, replaced only at integration.

---

## 7. P4: Analysis Library

**Independence: total.** Pure Go. No Kubernetes import, no network, no cluster. Runs on a laptop with no Docker.

| Day | Work | Solo exit criterion |
| :--- | :--- | :--- |
| **1** | `hysteresis.go`: per-pair sliding window, consecutive failure/success counters, threshold transitions, generation reset | Tests pass against `flapping-sequence.json` |
| **1** | `Evaluate()` entry point wired and returning a stub verdict, so P3 can compile against it from hour 3 | P3 can `go build` |
| **2** | `classifier.go`: row analysis, column analysis, pair analysis. All 8 classifications. | Every fixture in `contracts/fixtures/` classified correctly |
| **2** | Confidence scoring: rank hypotheses by fraction of observed failures explained; require > 0.8 or return `Unknown` | Both ambiguous fixtures return `Unknown` |
| **2** | Handle `AgentError` explicitly: control channel down means `Unknown`, never a data plane verdict | Test for `ambiguous-agent-down.json` |
| **3** | `metrics.go`: register 7 Prometheus metrics; cardinality check at n=10; drop pod-level labels if they explode | `/metrics` output verified with a fake registry |
| **3** | Property tests: random matrix generation, assert invariants (never classify with confidence below floor; symmetric failures never yield a directional verdict) | Property suite green |
| **4** | **E3 false positive sweep**: 60 min synthetic flapping input, sweep `failureThreshold` in {1,2,3,5}, recommend a default | Justified default backed by numbers |

**P4 needs from others:** nothing. Everything runs against fixtures.

**Why this is the strongest independence position:** P4 can deliver a fully tested, fully evaluated library by Day 3 without ever running `kubectl`.

---

## 8. P5: Evaluation and Research

**Independence: high**, via recorded fixtures on Days 1 to 2, real cluster from Day 3.

| Day | Work | Solo exit criterion |
| :--- | :--- | :--- |
| **1** | Related work section: Goldpinger, kube-netchecker, blackbox exporter, service mesh telemetry, node-problem-detector. Differentiate this project from Goldpinger explicitly. | Section drafted in `docs/report.md` |
| **1** | Author `contracts/fixtures/` (co-owned with P4 during the freeze, then P5 extends). This is P5's leverage over everyone's schedule. | 11 fixtures committed |
| **2** | `experiments/measure-mttd.sh`: inject, poll `status.conditions[].lastTransitionTime`, compute MTTD, append CSV. Develop against a **recorded CR YAML**, not a live cluster. | Correct MTTD from a replayed YAML sequence |
| **2** | `experiments/run-matrix.sh`: orchestrate N trials × M scenarios with reset between each | Dry-run mode prints the full plan |
| **2** | Analysis notebook or Go program: confusion matrix, precision, recall, plots | Produces plots from synthetic CSV |
| **3** | Integration window, then **E1** (detection latency) and **E2** (localization accuracy, 10 repeats per fault) | Confusion matrix with real numbers |
| **4** | **E4** (overhead) and **E5** (sampled vs full mesh), plots, `docs/report.md` complete, threats to validity | Report done |

**P5 needs from others:** a running cluster from Day 3. Until then, everything is developed against replayed fixtures.

---

## 9. Integration Windows (the only mandatory joint time)

| When | Duration | Who | Purpose |
| :--- | :--- | :--- | :--- |
| Day 1, 0900 to 1100 | 2h | All 5 | Contract freeze. Produce `types.go`, constants, fixtures. Commit and disperse. |
| Day 3, 0900 to 1200 | 3h | All 5 | Real integration. Swap fakes for real components. |
| Day 4, 1600 to 1730 | 1.5h | All 5 | Final demo assembly and rehearsal |

**Everything else is solo work.** Evening syncs are 15 minutes, asynchronous-friendly, and exist only to merge and flag contract issues.

### Day 3 integration checklist (run in this exact order)

| # | Step | Owner | Rollback if it fails |
| :--- | :--- | :--- | :--- |
| 1 | Replace stub images with real agent + manager builds | P1 | Keep stubs, debug the Dockerfile only |
| 2 | Deploy agent DaemonSet, verify `/healthz` on all 3 workers | P2 | Agent bug, P2 fixes solo, others continue on fakes |
| 3 | Point operator at real agents instead of the fake | P3 | Revert to `fakeagent.go`, keep developing |
| 4 | Swap P4's stub `Evaluate()` for the real one | P4 | Revert to stub, classifier keeps passing fixture tests |
| 5 | Run F2 end to end, confirm `NodeIngressFailure` appears in `kubectl get nhc` | P5 | Diagnose with fixtures to isolate which stage broke |

If any step fails, the person whose step it is fixes it solo. Everyone else reverts one step and keeps working. Nobody sits and watches somebody else debug.

---

## 10. Independence Summary

| Person | Blocked by anyone before Day 3? | Fallback if the cluster is down | Can demo solo? |
| :--- | :--- | :--- | :--- |
| P1 | No | Not applicable, P1 owns the cluster | Yes: cluster + fault injection between busybox pods |
| P2 | No | `docker compose` mesh of 4 agents | Yes: agents probing each other, no k8s |
| P3 | No | `envtest` + `fakeagent.go` | Yes: reconcile loop against fakes |
| P4 | No | Fixtures, no infrastructure at all | Yes: classifier over all 11 fixtures |
| P5 | No | Replayed CR YAML sequences | Yes: MTTD computation from recorded data |

Every person has a standalone demo by Day 2 evening. If integration on Day 3 goes badly, you still have five working components and can present the system as five verified parts plus a partial integration, which is a survivable outcome for a research project.

---

## 11. Contingency

| Scenario | Response |
| :--- | :--- |
| P1's Calico install fights KIND | Others are unaffected for 2 days. P2 lends a hand on Day 2 if needed. |
| Someone drops out entirely | P4's and P5's artifacts are independently valuable and can be presented alone. P3 absorbs P2's agent (it is the smallest binary). |
| Integration fails on Day 3 | Extend the window into the afternoon. P4 and P5 are unaffected and keep producing results from fixtures. |
| Behind schedule on Day 4 | Cut in order: Grafana dashboard, Sampled mode (E5), CI. **Never cut E2**, since it is the only evidence for research gap G3. |
| Contract turns out wrong mid-project | Additive fix only. A rename costs everyone a rebuild and is banned before Day 3. |

---

## 12. Branch and Merge Policy

| Rule | Detail |
| :--- | :--- |
| Branches | `p1-platform`, `p2-agent`, `p3-operator`, `p4-analysis`, `p5-eval`. One per person, long-lived. |
| Merge cadence | Evening sync only, after your solo exit criterion passes |
| Conflicts | Structurally impossible outside `pkg/contract/`, since directories are disjoint |
| `pkg/contract/` | Read-only after Day 1 hour 2. Changes require a sync-time announcement. |
| CI gate | `go test ./...` must pass before any merge to `main` |
