# Self-Healing Network Operator for Kubernetes
### Phase 1 Scope: Pod-to-Pod Data Plane Connectivity via Synthetic Probing

**Type:** Research + systems implementation project
**Duration:** 4 days
**Primary language:** Go (kubebuilder / controller-runtime)

---

## 1. Abstract

Kubernetes self-healing is *pod-scoped*: liveness and readiness probes test a container against itself, and the scheduler reacts to node-level `NotReady` conditions. Neither mechanism observes the **path between two pods**. A pod can pass every probe it owns while being unable to reach any pod on another node because of a hung CNI agent, a stale `iptables`/eBPF entry, or an over-broad `NetworkPolicy`.

This project builds a Kubernetes operator that maintains a continuously refreshed **east-west reachability matrix** using distributed synthetic probes, classifies the *blast radius* of a failure from the shape of that matrix, and exposes the result as a first-class Kubernetes API object plus Prometheus metrics.

Phase 1 (this document) delivers detection, localization, and observability. Remediation is designed for but gated behind a feature flag, with hooks left in place.

---

## 2. Problem Statement (scoped)

| Aspect | Statement |
| :--- | :--- |
| **Observed gap** | Kubernetes has no built-in, continuous, active measurement of inter-pod reachability across nodes. |
| **Failure class targeted** | Silent data plane partitions: pods `Running` + `Ready`, traffic dropped. |
| **Why it matters** | These failures are detected only when a user workload times out, so MTTD is bounded by application error budgets, not by the platform. |
| **Research question** | Can a full-mesh synthetic probe matrix, run as a reconciliation loop, detect and *localize* data plane partitions faster and more precisely than reactive application-level alerting, at acceptable overhead? |
| **Out of scope (Phase 1)** | DNS probing, policy simulation, automated remediation actions, multi-cluster. |

---

## 3. Background and Related Work

| System | What it does | Limitation for this problem |
| :--- | :--- | :--- |
| Liveness / readiness / startup probes | kubelet probes a container from its own node | Self-referential. Never crosses the pod network. |
| Node conditions + `node-problem-detector` | Surfaces kernel, runtime, and node-agent problems | Node-scoped. Does not test the *pair* (A, B). |
| Goldpinger (Bloomberg) | DaemonSet that pings peers, renders a mesh graph | Closest prior art. Passive dashboard: no CRD, no controller, no reconciliation, no failure classification, no remediation contract. |
| kube-netchecker / kubeprober | Periodic agent-based connectivity checks | Largely unmaintained; results are logs/metrics only, not cluster state. |
| Service mesh telemetry (Istio, Linkerd, Cilium Hubble) | Rich L4/L7 flow observability | **Passive**: requires live application traffic. An idle path that is broken stays invisible until a request arrives. Also heavy (sidecar or eBPF agent) for a diagnostic use case. |
| Prometheus blackbox exporter | Synthetic HTTP/TCP/ICMP probing | Probes *from a single vantage point*. Cannot express "from every node to every node" without hand-written targets and gives no topology-aware verdict. |

**Synthesis:** active east-west probing exists (Goldpinger), and the operator pattern exists, but nothing combines synthetic mesh probing with the reconciliation loop, a declarative API surface, topology-aware fault attribution, and a safe remediation contract.

---

## 4. Research Gaps

| # | Gap | How this project addresses it |
| :--- | :--- | :--- |
| **G1** | Kubernetes health semantics are self-referential; no first-class notion of *pairwise* reachability. | Introduce a `NetworkHealthCheck` CRD whose `.status` carries a pairwise reachability matrix as cluster state. |
| **G2** | Existing east-west probers are observability tools, not controllers. Results never become desired-vs-actual state. | Implement the mesh probe as a reconciliation loop with a real `.status` subresource, conditions, and Kubernetes Events. |
| **G3** | No automated *localization*. Operators see "something is broken" and manually bisect. | Blast-radius classifier that reads the matrix shape and emits a hypothesis (node-ingress, node-egress, pair-local, cluster-wide, policy-scoped). |
| **G4** | Full-mesh probing is O(n²) in nodes and is treated as an accepted cost. Scaling is unstudied. | Measure the cost explicitly; implement and evaluate a sampled-topology mode (k-regular random graph) against full mesh for detection fidelity. |
| **G5** | No distinction between transient loss and sustained partition, so alerting is noisy. | Sliding-window state machine with `failureThreshold` / `successThreshold` hysteresis; report loss rate and RTT distribution, not a boolean. |
| **G6** | Closed-loop network remediation lacks safety guardrails. Naive "restart the CNI pod" is a documented cause of cascading outages. | Define (and partially implement) a remediation contract: rate limiting, cooldown, blast-radius caps, dry-run mode, and refusal to act when the classifier is not confident. |

---

## 5. Proposed Solution

### 5.1 Architecture

```
                   ┌──────────────────────────────────────┐
                   │  NetworkHealthCheck (CRD, cluster)   │
                   │  spec: interval, selectors, probe,   │
                   │        thresholds, topology mode     │
                   │  status: matrix, conditions, verdict │
                   └───────────────┬──────────────────────┘
                                   │ watch / reconcile
                   ┌───────────────▼──────────────────────┐
                   │  Operator (Deployment, 1 replica)    │
                   │  - discovers agent pods              │
                   │  - builds probe plan (mesh/sampled)  │
                   │  - fans out probe RPCs (bounded)     │
                   │  - aggregates + hysteresis           │
                   │  - classifies blast radius           │
                   │  - writes status, Events, metrics    │
                   └───────┬───────────┬──────────┬───────┘
                           │ HTTP      │          │
              ┌────────────▼──┐  ┌─────▼───────┐  ┌▼─────────────┐
              │ agent (node1) │  │agent (node2)│  │agent (node3) │
              │ /probe?to=IP  │  │             │  │              │
              │ /healthz      │  │  DaemonSet, pod network       │
              └───────────────┘  └─────────────┘  └──────────────┘
                    └──────── actual data plane traffic ─────────┘
```

The operator never sends the data plane traffic itself. It only *orchestrates*. The traffic that matters is agent-to-agent, so it traverses exactly the path a real workload would.

### 5.2 Why an agent DaemonSet (design decision)

| Option | Pros | Cons | Verdict |
| :--- | :--- | :--- | :--- |
| Operator pings pod IPs directly | Trivial, no agent | Only tests *one* source node. Misses asymmetric and node-local faults entirely. | Rejected |
| `kubectl exec` / `pods/exec` into existing workload pods | Zero extra pods | Needs `pods/exec` RBAC (a privilege escalation path), needs a shell + ping in every image, ~200 to 800 ms per exec, breaks on distroless. | Rejected |
| Ephemeral Job/Pod per probe round | No long-lived footprint | Pod startup dominates (seconds), churns the API server and the CNI IPAM, which is the thing under test. | Rejected |
| **Dedicated agent DaemonSet, HTTP control + TCP/ICMP data** | Fixed footprint, sub-ms overhead per probe, uniform image, no exec RBAC, tests real pod network | One extra pod per node | **Chosen** |

### 5.3 CRD design (`network.selfheal.io/v1alpha1`)

```yaml
apiVersion: network.selfheal.io/v1alpha1
kind: NetworkHealthCheck
metadata:
  name: cluster-mesh
spec:
  interval: 15s
  probeTimeout: 2s
  probeType: TCP            # TCP | HTTP | ICMP
  topology: FullMesh        # FullMesh | Sampled
  sampleDegree: 3           # used when topology=Sampled
  nodeSelector: {}          # limit to a subset of nodes
  failureThreshold: 3       # consecutive rounds before Degraded
  successThreshold: 2       # consecutive rounds before recovery
  maxConcurrentProbes: 64
  remediation:
    enabled: false          # Phase 2
    dryRun: true
status:
  observedGeneration: 4
  lastProbeTime: "2026-08-30T10:04:12Z"
  verdict: Degraded         # Healthy | Degraded | Partitioned | Unknown
  classification: NodeIngressFailure
  suspectNodes: ["kind-worker2"]
  reachability:
    - source: kind-worker
      target: kind-worker2
      success: false
      lossRate: 1.0
      rttMillis: 0
      consecutiveFailures: 4
      lastError: "dial tcp 10.244.2.5:9376: i/o timeout"
  conditions:
    - type: MeshHealthy
      status: "False"
      reason: NodeIngressFailure
      message: "9/12 pairs failing; all failures target kind-worker2"
```

### 5.4 Blast-radius classifier (core contribution)

Given the boolean matrix `M[src][dst]` over `n` nodes:

| Matrix signature | Emitted classification | Likely root cause |
| :--- | :--- | :--- |
| All pairs OK | `Healthy` | - |
| Single directed pair fails, both nodes otherwise fine | `PairLocalFailure` | Transient loss, MTU/fragmentation, single stale conntrack entry |
| Entire **column** for node X fails, X's row is fine | `NodeIngressFailure` | Inbound firewall/policy on X, broken VXLAN decap on X |
| Entire **row** for node X fails, X's column is fine | `NodeEgressFailure` | X's CNI agent hung, missing route, broken encap on X |
| Both row and column for X fail | `NodeIsolated` | Node-level CNI crash, kubelet/CNI plugin desync |
| Failure rate above `partitionThreshold` across all nodes | `ClusterPartition` | Control plane, cluster-wide NetworkPolicy, overlay/underlay outage |
| Failures cluster by namespace rather than by node | `PolicyScopedFailure` | `NetworkPolicy` misconfiguration (Phase 2 confirmation step) |
| Agent unreachable on control channel | `Unknown` | Do not classify, do not remediate |

Localization rule: rank hypotheses by the fraction of observed failures they explain, and require that fraction to exceed a confidence floor (default 0.8) before setting `classification`. Otherwise report `Unknown`. This is the guardrail that keeps Phase 2 remediation from firing on ambiguous evidence.

### 5.5 Exported metrics

| Metric | Type | Labels |
| :--- | :--- | :--- |
| `netprobe_pair_reachable` | Gauge (0/1) | `src_node`, `dst_node`, `src_pod`, `dst_pod` |
| `netprobe_pair_rtt_seconds` | Histogram | `src_node`, `dst_node` |
| `netprobe_probe_errors_total` | Counter | `src_node`, `dst_node`, `reason` |
| `netprobe_round_duration_seconds` | Histogram | `topology_mode` |
| `netprobe_mesh_health` | Gauge | `verdict` |
| `netprobe_classification` | Gauge (0/1) | `classification`, `suspect_node` |
| `netprobe_probes_sent_total` | Counter | `topology_mode` |

---

## 6. Tech Stack

| Category | Choice | Why |
| :--- | :--- | :--- |
| Cluster | **KIND**, 1 control plane + 3 workers | Multi-node is mandatory (Minikube single node cannot express cross-node failures). KIND nodes are containers, so `docker exec` gives clean fault injection. |
| CNI | **Calico** (default `kindnet` disabled) | Real `iptables`/IPIP data plane; policy support for Phase 2. |
| Language | **Go 1.22+** | Required |
| Scaffolding | **kubebuilder v4** + `controller-runtime` | CRD codegen, manager, informers, leader election, status subresource |
| Client | `client-go` (via controller-runtime) | Pod/Node listing, Events |
| CLI | **kubectl** | Required |
| Metrics | `prometheus/client_golang`, kube-prometheus-stack | Operator already exposes `:8080/metrics` by default |
| Agent HTTP | Go stdlib `net/http`, `net.DialTimeout` | No dependency needed |
| Concurrency | `golang.org/x/sync/errgroup` + semaphore | Bounded fan-out for O(n²) probes |
| Testing | `envtest` + Ginkgo/Gomega (kubebuilder default) | Controller unit tests without a live cluster |
| Fault injection | `docker exec` into KIND nodes: `iptables`, `tc netem` | Reproducible, scriptable |
| Optional | Grafana dashboard, `kube-burner` for scale test | Nice-to-have on Day 4 |

Suggested additions beyond your list: **KIND** (over Minikube), **kubebuilder**, **client_golang**, **errgroup**, **envtest**, and **`tc netem`** for graded (not just binary) fault injection.

---

## 7. Repository Layout

```
selfheal-network-operator/
├── api/v1alpha1/
│   ├── networkhealthcheck_types.go     # CRD structs
│   └── zz_generated.deepcopy.go
├── internal/controller/
│   ├── networkhealthcheck_controller.go # Reconcile loop
│   ├── prober.go                        # fan-out client, bounded concurrency
│   ├── topology.go                      # FullMesh vs Sampled plan builder
│   ├── classifier.go                    # blast-radius logic
│   ├── hysteresis.go                    # sliding window state machine
│   └── metrics.go
├── cmd/
│   ├── main.go                          # operator manager
│   └── agent/main.go                    # DaemonSet agent binary
├── config/                              # kustomize: CRD, RBAC, manager, samples
├── hack/
│   ├── kind-cluster.yaml
│   ├── setup-cluster.sh
│   ├── inject-fault.sh                  # fault scenarios F1..F6
│   └── measure-mttd.sh
├── test/e2e/
├── docs/
│   ├── report.md                        # research write-up
│   └── results/                         # CSVs, plots
└── Makefile
```

---

## 8. Implementation Steps

### Step 0: Cluster bootstrap

`hack/kind-cluster.yaml`:
```yaml
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
networking:
  disableDefaultCNI: true
  podSubnet: "192.168.0.0/16"
nodes:
  - role: control-plane
  - role: worker
  - role: worker
  - role: worker
```

```bash
kind create cluster --name selfheal --config hack/kind-cluster.yaml
kubectl apply -f https://raw.githubusercontent.com/projectcalico/calico/v3.28.0/manifests/calico.yaml
kubectl wait --for=condition=Ready nodes --all --timeout=300s
kubectl get pods -n kube-system -l k8s-app=calico-node
```

### Step 1: Scaffold

```bash
mkdir selfheal-network-operator && cd $_
go mod init github.com/<you>/selfheal-network-operator
kubebuilder init --domain selfheal.io --repo github.com/<you>/selfheal-network-operator
kubebuilder create api --group network --version v1alpha1 --kind NetworkHealthCheck \
  --resource --controller
make manifests generate
```

### Step 2: Agent binary

| Endpoint | Behaviour |
| :--- | :--- |
| `GET /healthz` | 200, identifies node name + pod IP from downward API |
| `GET /probe?target=<ip>:<port>&timeout=2s&count=5` | Dials target `count` times, returns JSON `{success, lossRate, rttMillisP50, error}` |
| `GET /metrics` | Agent-local counters |
| `:9376` (TCP listener) | The probe *target* port, deliberately separate from the control port |

Key detail: the control port (`:8080`, scraped by the operator) and the data port (`:9376`, dialed by peers) must be separate. If they are the same, a control-channel failure is indistinguishable from a data plane failure and the classifier loses its `Unknown` escape hatch.

Deploy as a DaemonSet with `NET_RAW` only if ICMP mode is used; TCP mode needs no extra capability. Pods must be on the **pod network** (`hostNetwork: false`), otherwise the CNI path is bypassed and the whole experiment is void.

### Step 3: Controller reconcile loop

```
Reconcile(ctx, req):
  1. Fetch NetworkHealthCheck; if deleted, drop probe schedule and return.
  2. List agent pods (label selector, field selector status.phase=Running).
     Build endpoint set {nodeName, podIP, controlURL}.
  3. Build probe plan from spec.topology:
       FullMesh -> all ordered pairs (n*(n-1))
       Sampled  -> k-regular random directed graph, reseeded per round
  4. Fan out with errgroup + semaphore(spec.maxConcurrentProbes):
       GET {src.controlURL}/probe?target={dst.podIP}:9376
     Per-probe context timeout = spec.probeTimeout.
  5. Feed each result into the sliding window; apply failure/success thresholds.
  6. Run classifier over the debounced matrix.
  7. Update .status (matrix, verdict, classification, conditions, suspectNodes).
  8. Emit Event on verdict transition only (not every round).
  9. Update Prometheus gauges.
 10. return ctrl.Result{RequeueAfter: spec.interval}, nil
```

Correctness notes to get right:
- Update **status only** via `r.Status().Update()`; retry on conflict with `retry.RetryOnConflict`.
- Compare against `observedGeneration` so spec changes force a fresh window.
- Never block the reconcile worker on a slow probe: every HTTP call carries a context deadline strictly less than `spec.interval`.
- Emit Events on *transition*, otherwise you will flood etcd at a 15s interval.
- RBAC needed: `get/list/watch` on pods and nodes, full verbs on your CRD + `/status`, `create` on events. Nothing more in Phase 1. Keep it minimal and say so in the report.

### Step 4: Fault injection harness

| ID | Scenario | Command | Expected classification |
| :--- | :--- | :--- | :--- |
| F1 | Baseline, healthy | none | `Healthy` |
| F2 | Node inbound block | `docker exec selfheal-worker2 iptables -I INPUT -p tcp --dport 9376 -j DROP` | `NodeIngressFailure` |
| F3 | Node outbound block | `docker exec selfheal-worker2 iptables -I OUTPUT -p tcp --dport 9376 -j DROP` | `NodeEgressFailure` |
| F4 | CNI agent kill | `kubectl delete pod -n kube-system -l k8s-app=calico-node --field-selector spec.nodeName=selfheal-worker2` | `NodeIsolated` or `Healthy` if recovery beats detection (report both) |
| F5 | Partial packet loss | `docker exec selfheal-worker2 tc qdisc add dev eth0 root netem loss 40%` | `Degraded` with lossRate ~0.4, not a hard partition |
| F6 | Deny-all NetworkPolicy in agent namespace | `kubectl apply -f hack/deny-all.yaml` | `ClusterPartition` (Phase 2: `PolicyScopedFailure`) |

Each script prints a timestamp on injection. MTTD = `status.lastTransitionTime` minus injection timestamp.

### Step 5: Evaluation protocol

| Experiment | Independent variable | Measured |
| :--- | :--- | :--- |
| E1: Detection latency | Fault type F2..F6 | MTTD, vs theoretical floor `interval * failureThreshold` |
| E2: Localization accuracy | Fault type, 10 repeats each | Confusion matrix of predicted vs ground-truth classification; precision/recall per class |
| E3: False positive rate | 60 min steady state under load | Number of spurious `Degraded` transitions per hour, swept over `failureThreshold` in {1,2,3,5} |
| E4: Overhead | n = 2, 3, 4 nodes (extrapolate to n=10 analytically) | Probes/round, bytes/round, operator CPU + RSS, round wall-clock |
| E5: Sampled vs full mesh | `topology` mode, `sampleDegree` in {2,3} | Detection recall and MTTD penalty vs probe count reduction |

E5 is the experiment that turns G4 from a stated gap into a result. Prioritize it if time is short on Day 4.

---

## 9. Work Timeline (4 days)

Assume roughly 8 to 9 focused hours per day.

### Day 1: Environment, API, and agent

| Slot | Task | Deliverable / checkpoint |
| :--- | :--- | :--- |
| 0.0 to 1.5h | KIND multi-node cluster + Calico; verify cross-node pod ping manually | 4 nodes Ready, manual `kubectl exec` ping across nodes succeeds |
| 1.5 to 2.5h | kubebuilder scaffold, module init, `make manifests` runs clean | Empty controller reconciles, `make run` starts |
| 2.5 to 4.5h | Write `NetworkHealthCheck` types (spec + status structs, kubebuilder markers, `+kubebuilder:subresource:status`, printcolumns) | `make install` succeeds; sample CR applies; `kubectl get nhc` shows columns |
| 4.5 to 7.0h | Agent binary: `/healthz`, `/probe`, TCP listener on 9376, JSON response struct | `go run ./cmd/agent` locally, probe against localhost works |
| 7.0 to 8.5h | Dockerfile for agent, `kind load docker-image`, DaemonSet manifest, downward API env for node name and pod IP | Agent pod on every node; `curl` from one agent to another via port-forward returns success |

**Day 1 exit criterion:** you can manually curl `agent-on-worker1/probe?target=<worker2-agent-ip>:9376` and get `{"success":true}`.

### Day 2: Controller, probing, status

| Slot | Task | Deliverable / checkpoint |
| :--- | :--- | :--- |
| 0.0 to 1.5h | Agent discovery in reconcile (list pods by label, filter Running + has IP), build endpoint set | Log prints n endpoints on each reconcile |
| 1.5 to 3.5h | `prober.go`: bounded concurrent fan-out, per-probe context deadline, structured result type | Full n×n matrix collected in one round, round duration logged |
| 3.5 to 5.0h | `hysteresis.go`: per-pair sliding window, consecutive failure/success counters, generation reset | Unit tests for the state machine pass (pure logic, no cluster needed) |
| 5.0 to 6.5h | Status writer: matrix into `.status`, conditions, `RetryOnConflict`, `RequeueAfter: spec.interval` | `kubectl get nhc cluster-mesh -o yaml` shows a live, updating matrix |
| 6.5 to 8.5h | Deploy operator into cluster (`make docker-build`, `kind load`, `make deploy`), RBAC fixes, leader election on | Operator running as a Deployment in-cluster, not just `make run` |

**Day 2 exit criterion:** `kubectl get nhc -w` shows the matrix flipping to failures within seconds of `iptables -I INPUT ... -j DROP` on a worker.

### Day 3: Classification, metrics, experiments

| Slot | Task | Deliverable / checkpoint |
| :--- | :--- | :--- |
| 0.0 to 2.0h | `classifier.go`: row/column/pair analysis, confidence floor, `Unknown` fallback | Table-driven unit tests over synthetic matrices for every class in section 5.4 |
| 2.0 to 3.0h | Wire classification into status + Events on transition only | Event stream shows one Event per state change, not per round |
| 3.0 to 4.5h | `metrics.go`: register the seven metrics, expose on `:8080/metrics`, ServiceMonitor or scrape config | `curl` metrics endpoint shows per-pair gauges |
| 4.5 to 6.0h | `hack/inject-fault.sh` implementing F1..F6 with timestamps + `measure-mttd.sh` | One command runs a scenario and prints MTTD |
| 6.0 to 8.5h | Run E1 and E2 (10 repeats per fault type), record raw CSVs in `docs/results/` | Confusion matrix + MTTD table populated with real numbers |

**Day 3 exit criterion:** you can state, with data, "F3 is detected in X seconds and classified correctly in Y of 10 trials."

### Day 4: Scale study, hardening, write-up

| Slot | Task | Deliverable / checkpoint |
| :--- | :--- | :--- |
| 0.0 to 1.5h | `topology.go`: Sampled mode (k-regular random digraph), reseed per round | Probe count drops from n(n-1) to k·n, verified in metrics |
| 1.5 to 3.0h | Run E4 and E5: overhead + sampled vs full mesh recall/MTTD | Plots: probes/round vs n; MTTD vs sampleDegree |
| 3.0 to 4.0h | Run E3: false positive sweep over `failureThreshold` | Recommended default with justification |
| 4.0 to 5.0h | Remediation *contract* stub: `remediation.enabled` flag, dry-run path that logs the action it would take, cooldown + rate limiter skeleton, refusal on `Unknown` | Code path exists and is tested; no destructive action taken |
| 5.0 to 6.5h | `docs/report.md`: abstract, gaps, design, results tables, threats to validity, future work | Report complete with the Day 3 and 4 numbers |
| 6.5 to 8.0h | README, quickstart, architecture diagram, demo script (5 min: healthy, inject F3, watch classification, remove fault, watch recovery) | Reproducible from a clean machine |
| 8.0 to 8.5h | Buffer | - |

**Slip plan:** if Day 2 overruns, cut Sampled mode (E5) and the Grafana dashboard first. Do not cut E2 (localization accuracy), since that is the evidence for G3, the project's main claim.

---

## 10. Threats to Validity (include in the report)

| Threat | Mitigation / disclosure |
| :--- | :--- |
| KIND nodes are containers on one host, so the "network" is a Linux bridge, not real NICs across real links | State it plainly. Claims are about *detection and classification logic*, not about absolute latency numbers. |
| n = 4 nodes is tiny; O(n²) claims are extrapolated | Report measured probe counts and derive the analytic curve; do not present extrapolated MTTD as measured. |
| Agent pods are not representative of real workloads (no memory pressure, no sidecars) | Note as a limitation; suggest co-locating with a load generator in future work. |
| Fault injection via `iptables` on the node is coarser than a genuine CNI bug | F4 (killing calico-node) is the more realistic case; report it alongside the synthetic ones. |
| Classifier is evaluated on the same fault taxonomy it was designed around | Acknowledge the risk of overfitting; propose a held-out fault set as future work. |

---

## 11. Future Work

| Phase | Item | Notes |
| :--- | :--- | :--- |
| 2 | **Closed-loop remediation** | Restart CNI DaemonSet pod on the suspect node, re-apply `NetworkPolicy` to flush stale conntrack/eBPF state. Requires: cooldown, per-node action budget, cluster-wide rate limit, mandatory dry-run soak, and a hard refusal when `classification == Unknown`. |
| 2 | **DNS plane** | CoreDNS latency and NXDOMAIN rate via Prometheus; synthetic `A` record resolution from each agent; ConfigMap failover to upstream resolvers. |
| 2 | **Policy validation** | Compute the reachability implied by active `NetworkPolicy` objects and diff it against the observed matrix. A mismatch localizes the offending policy directly, which no existing tool does. |
| 3 | **eBPF data path attribution** | Replace TCP dial with an eBPF-traced probe to identify *where* the packet was dropped (iptables chain, conntrack, encap) rather than only *that* it dropped. |
| 3 | **Adaptive probing** | Increase probe frequency and degree around a suspect node, decrease elsewhere. Turns the fixed O(n²) cost into a demand-driven one. |
| 3 | **Predictive detection** | Time-series model over RTT/loss to flag degradation before hard failure. |
| 3 | **Multi-cluster / service mesh interop** | Extend the matrix across clusters; export verdicts as Hubble/Istio-compatible signals. |
| 3 | **L7 and MTU probing** | HTTP semantics, path MTU discovery for the classic "large payloads hang, small ones work" overlay bug. |

---

## 12. Deliverables Checklist

- [ ] `NetworkHealthCheck` CRD with status subresource and print columns
- [ ] Operator Deployment with minimal RBAC and leader election
- [ ] Agent DaemonSet with split control (`:8080`) and data (`:9376`) ports
- [ ] Full-mesh probe orchestration with bounded concurrency and hysteresis
- [ ] Blast-radius classifier with confidence floor and `Unknown` fallback
- [ ] 7 Prometheus metrics + scrape config
- [ ] Fault injection harness F1 to F6 with MTTD measurement
- [ ] Results for E1 to E5 with raw CSVs
- [ ] Remediation contract stub (dry-run only)
- [ ] `docs/report.md`: gaps, design, results, threats to validity, future work
- [ ] README + 5 minute reproducible demo script
