# Self-Healing Network Operator for Kubernetes

**Pod-to-Pod Data Plane Connectivity via Synthetic Probing**

A Kubernetes operator that maintains a periodically refreshed east-west reachability matrix using distributed synthetic TCP probes, classifies the blast radius of data plane failures, and exposes results as a bounded `NetworkHealthCheck` status and Prometheus metrics. It never reports `Healthy` without evidence, separates hard failure from partial degradation and missing data, and never performs destructive remediation.

## Architecture

```
                   ┌──────────────────────────────────────────┐
                   │  NetworkHealthCheck (CRD, cluster-scoped)│
                   │  spec: interval, probe settings,         │
                   │        thresholds, remediation           │
                   │  status: bounded reachability (≤500),    │
                   │          pair counts, verdict, condition │
                   └───────────────┬──────────────────────────┘
                                   │ poll every POLL_INTERVAL (15s),
                                   │ run when spec.interval has elapsed
                   ┌───────────────▼──────────────────────────┐
                   │  Manager (Deployment, 1 replica)         │
                   │  - discovers agent pods (app=netprobe-   │
                   │    agent, Running, with a pod IP)        │
                   │  - probes every directed agent pair      │
                   │    (bounded by maxConcurrentProbes)      │
                   │  - debounces with hysteresis             │
                   │  - classifies blast radius               │
                   │  - writes status + /metrics on :8080     │
                   └───────┬───────────┬──────────┬───────────┘
                           │ HTTP :8080│          │  Authorization: Bearer
              ┌────────────▼──┐  ┌─────▼───────┐  ┌▼─────────────┐
              │ agent (node1) │  │agent (node2)│  │agent (node3) │
              │ /probe?target=│  │             │  │              │
              │   <podIP>     │  │  DaemonSet, pod network       │
              │ /healthz      │  │             │  │              │
              │ /metrics      │  │             │  │              │
              └───────────────┘  └─────────────┘  └──────────────┘
                    └─ TCP :9376 data traffic + 1-byte ack ──┘
```

The manager never sends data plane traffic itself; it only orchestrates. For each source → destination pair it asks the source agent (control port 8080) to probe the destination agent's pod IP on data port 9376, so the traffic traverses the same pod network a real workload would.

## Prerequisites

- [Docker](https://docs.docker.com/get-docker/) (running)
- [KIND](https://kind.sigs.k8s.io/docs/user/quick-start/#installation) v0.20+
- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [Go](https://go.dev/dl/) 1.22+
- [jq](https://jqlang.github.io/jq/download/) (for measurement scripts)

## Quick Start

All source code is in `code/`. Run every `make` target and script from there; paths in the rest of this README are relative to `code/`.

```bash
# 0. Enter the source directory
cd code

# 1. Create the 4-node KIND cluster (1 control-plane + 3 workers) with Calico CNI
make cluster-up

# 2. Build Docker images and load them into KIND
make images load

# 3. Deploy the CRD, RBAC, manager, manager Service, agent Secret, DaemonSet, and Service
make deploy

# 4. Apply the sample NetworkHealthCheck (cluster-mesh)
make deploy-sample

# 5. Watch the mesh health
kubectl get nhc -w

# 6. Inject a fault and watch detection
make inject-fault SCENARIO=F3

# 7. Check the operator's verdict
kubectl get nhc cluster-mesh -o yaml

# 8. Clear the fault
make clear-fault SCENARIO=F3
```

## NetworkHealthCheck Spec

Every probe and threshold field below changes behavior. The remediation fields are recorded on the remediation plan, but no remediation action executes regardless of their values.

| Field | Default | Effect |
|:------|:--------|:-------|
| `interval` | `15s` | Minimum time between probe rounds for this object (compared against `status.lastProbeTime`) |
| `probeTimeout` | `2s` | Per-probe deadline for dial + write + ack read |
| `probeType` | `TCP` | Only `TCP` is accepted (CRD enum) |
| `probeCount` | `5` | Probes per pair per round |
| `probePayloadBytes` | `1024` | Payload size per probe |
| `failureThreshold` | `3` | Consecutive failed rounds before a pair is reported failed |
| `successThreshold` | `2` | Consecutive successful rounds before a failed pair recovers |
| `maxConcurrentProbes` | `1` | Upper bound on concurrent pair probes |
| `remediation.enabled` | `false` | Recorded on the plan; network failures are always planned as `AlertOnly` |
| `remediation.dryRun` | `true` | Recorded on the plan; even with `enabled: true, dryRun: false` no pod is deleted |

## Classifications

| Classification | Meaning |
|:---------------|:--------|
| `Healthy` | At least one pair measured, no hard failures, every pair below the 5% loss threshold |
| `Degraded` | Every pair reachable, but at least one has loss ≥ 5% (configurable policy default) |
| `NoData` | Empty matrix: nothing was measured. Not a network verdict; never remediated |
| `Unknown` | An agent/control-channel error, or no hypothesis reaches the 0.80 confidence floor |
| `PairLocalFailure` | Exactly one directed pair failed |
| `NodeIngressFailure` | Every probe *into* one node failed |
| `NodeEgressFailure` | Every probe *out of* one node failed |
| `NodeIsolated` | Every probe into and out of one node failed |
| `PolicyScopedFailure` | Every probe failed with "connection refused", so a NetworkPolicy or firewall rule is the likely cause |
| `ClusterPartition` | At least 80% of probes failed |

A pair "succeeds" when at least one probe completed a bidirectional exchange (payload written, 1-byte ack read). Packet quality is carried separately by loss rate, RTT, and jitter. Hysteresis is applied before classification: failures on a healthy pair are suppressed until `failureThreshold` consecutive rounds fail, and a failed pair needs `successThreshold` consecutive good rounds to recover.

## Status

`kubectl get nhc cluster-mesh -o yaml` shows:

| Field | Content |
|:------|:--------|
| `verdict`, `classification` | Latest classification |
| `classificationConfidence` | Classifier confidence (0 for `NoData`) |
| `summary` | Human-readable reason from the classifier |
| `suspectNodes` | Nodes implicated by the classification |
| `reachability` | Per-pair results: success, loss, RTT min/avg/max, jitter, burst loss, throughput, bytes, probe counts, error. **At most 500 entries**, sorted by pair key |
| `reachabilityTotal` | Number of pairs in the full matrix |
| `reachabilityTruncated` | `true` when `reachabilityTotal > 500` |
| `reachabilitySummary` | `total`, `healthy`, `degraded`, `failed` counts over the **full** matrix |
| `degradedPairs` | Up to 50 sorted keys of reachable pairs with loss ≥ 5% |
| `lastProbeTime`, `observedGeneration` | When the round ran and which spec generation it used |
| `conditions` | One `Healthy` condition: `True` only for `Healthy`; `reason` is the classification (`NoData`, `Degraded`, …) |

**Why the cap:** a full mesh has N×(N−1) directed pairs, so status grows O(N²). At 23 nodes the matrix passes 500 pairs, and every entry is rewritten each round. etcd rejects objects over ~1.5 MB, and every status write is sent to every watcher. Above 500 pairs the status keeps a deterministic sample plus full-matrix counts; full per-pair detail stays in Prometheus metrics, not etcd.

## Fault Scenarios

`TARGET_NODE` defaults to `pod-pod-selfheal-worker2`. On KIND the node name is also the Docker container name.

| ID | Scenario | Injection point | Expected Classification |
|:---|:---------|:----------------|:------------------------|
| F1 | Baseline (negative control) | None | `Healthy` |
| F2 | Node inbound block | `docker exec <node> iptables -I INPUT -p tcp --dport 9376 -j DROP` | `NodeIngressFailure` |
| F3 | Node outbound block | `docker exec <node> iptables -I OUTPUT -p tcp --dport 9376 -j DROP` | `NodeEgressFailure` |
| F4 | CNI agent kill | Delete the `calico-node` pod on the node | `NodeIsolated` |
| F5 | Partial packet loss (40%) | `tc qdisc add dev <default-route iface> root netem loss 40%` on the node | `Degraded` |
| F6 | Deny-all NetworkPolicy | `kubectl apply -f hack/deny-all.yaml` | `ClusterPartition` |

Run with `make inject-fault SCENARIO=Fx` and clear with `make clear-fault SCENARIO=Fx` (clearing is idempotent).

## Monitoring

Deploy the Prometheus + Grafana stack:

```bash
make deploy-monitoring
```

This applies `config/monitoring/` (Prometheus, Grafana, and the provisioned "Mesh Health" dashboard ConfigMap). The `ServiceMonitor` objects in `servicemonitor.yaml` are applied only when the Prometheus Operator CRDs exist; the bundled Prometheus scrapes the manager and agents directly via pod discovery.

Access:
- **Prometheus:** `kubectl port-forward -n monitoring svc/prometheus 9090:9090`
- **Grafana:** `kubectl port-forward -n monitoring svc/grafana 3000:3000` (admin/admin)
- **Raw manager metrics:** `kubectl port-forward -n netprobe-system svc/selfheal-manager 8080:8080` then `curl localhost:8080/metrics`

### Exported Metrics

All metrics are hand-written Prometheus text-format gauges describing the **latest** probe round; there are no counters or histograms. Pair metrics use the stabilized (post-hysteresis) matrix.

Manager (`selfheal-manager:8080/metrics`):

| Metric | Labels | Description |
|:-------|:-------|:------------|
| `netprobe_manager_up` | — | Manager is running (always 1) |
| `netprobe_pair_reachable` | `src_node`, `dst_node` | 1 if the directed pair succeeded, 0 if it failed |
| `netprobe_pair_rtt_seconds` | `src_node`, `dst_node` | Average probe RTT |
| `netprobe_pair_jitter_seconds` | `src_node`, `dst_node` | RTT jitter |
| `netprobe_pair_loss_rate` | `src_node`, `dst_node` | Fraction of probes that failed |
| `netprobe_pair_loss_burst` | `src_node`, `dst_node` | Longest run of consecutive failed probes |
| `netprobe_pair_throughput_bytes_per_second` | `src_node`, `dst_node` | Synthetic payload throughput |
| `netprobe_pair_bytes_sent` | `src_node`, `dst_node` | Bytes sent in the latest round |
| `netprobe_pair_probes_sent` | `src_node`, `dst_node` | Probes attempted in the latest round |
| `netprobe_round_duration_seconds` | — | Wall-clock duration of the latest round |
| `netprobe_mesh_health` | `verdict` | 1 if the verdict is `Healthy`, 0 otherwise |
| `netprobe_classification` | `classification` | Info gauge: always 1; the classification is the label |
| `netprobe_classification_confidence` | `classification` | Confidence of the current classification |
| `netprobe_reachability_pairs` | `state` | Pair counts: `healthy` (reachable, loss < 5%), `degraded` (reachable, loss ≥ 5%), `failed` (no successful probe, including agent errors); `nodata` is 1 when there is no matrix at all |

Agent (`<pod IP>:8080/metrics`):

| Metric | Labels | Description |
|:-------|:-------|:------------|
| `netprobe_agent_up` | — | Agent is running (always 1) |

The Grafana dashboard (`config/monitoring/grafana-dashboard.json`) queries only the manager metrics above. A unit test (`TestDashboardAndReadmeReferenceOnlyEmittedMetrics`) fails if this README or the dashboard references a metric that is not emitted.

## P5 Evaluation

P5 measures detection latency from the live `NetworkHealthCheck` status (run from `code/`):

```bash
# One scenario
make measure-mttd SCENARIO=F5 TRIALS=3

# Full evaluation sweep
TRIALS=10 ./hack/run-p5-evaluation.sh
```

Outputs:
- Raw CSV: `code/docs/results/mttd-results.csv`
- Summary table and confusion matrix: `code/docs/results/p5-summary.md`

Captured metrics include MTTD, RTT, jitter, loss rate, maximum burst loss, synthetic throughput, bytes per round, and probe count. The scripts measure detection (MTTD) only, not recovery time.

## Project Structure

```
├── README.md
├── code/                                 # All source code (run make/scripts from here)
│   ├── cmd/
│   │   ├── manager/main.go               # Manager entry point: poll loop, /healthz, /metrics
│   │   └── agent/main.go                 # Agent entry point
│   ├── config/
│   │   ├── namespace.yaml                # netprobe-system namespace
│   │   ├── crd/                          # NetworkHealthCheck CRD (v1alpha1)
│   │   ├── rbac/                         # ClusterRole, binding, ServiceAccount
│   │   ├── manager/                      # Manager Deployment + metrics Service
│   │   ├── agent/                        # Agent DaemonSet, headless Service, probe-token Secret
│   │   ├── samples/                      # Example NetworkHealthCheck (cluster-mesh)
│   │   ├── monitoring/                   # Prometheus, Grafana, dashboard, ServiceMonitors
│   │   └── kustomization.yaml            # Root kustomization
│   ├── contracts/fixtures/               # Classifier test matrices (13 scenarios)
│   ├── docs/results/                     # P5 evaluation outputs (raw CSV + summary)
│   ├── hack/
│   │   ├── kind-cluster.yaml             # KIND cluster config
│   │   ├── setup-cluster.sh              # Cluster creation + Calico
│   │   ├── reset-cluster.sh              # Cluster teardown
│   │   ├── inject-fault.sh               # Fault scenarios F1–F6
│   │   ├── deny-all.yaml                 # NetworkPolicy for F6
│   │   ├── measure-mttd.sh               # MTTD measurement
│   │   ├── run-p5-evaluation.sh          # Scenario sweep
│   │   ├── summarize-results.py          # CSV → p5-summary.md
│   │   ├── test-self-healing.sh          # End-to-end smoke test
│   │   └── demo.sh                       # Demo script
│   ├── internal/controller/
│   │   ├── networkhealthcheck_controller.go  # Poll loop, spec parsing, interval gating
│   │   ├── controller.go                 # Reconcile: discover → probe → hysteresis → classify → status
│   │   ├── prober.go / probe.go          # Concurrent probe round + agent HTTP client
│   │   ├── status.go                     # Bounded status builder
│   │   ├── metrics.go                    # Prometheus text exposition
│   │   ├── remediation.go                # Alert-only remediation planner
│   │   └── classifier_stub.go            # Early P3 classifier, used only by tests
│   ├── pkg/
│   │   ├── contract/types.go             # Shared contract types
│   │   ├── analysis/                     # Classifier + hysteresis tracker
│   │   ├── prober.go                     # Agent TCP prober
│   │   └── server.go                     # Agent HTTP server, auth, target allowlist
│   ├── Dockerfile                        # Manager image
│   ├── Dockerfile.agent                  # Agent image
│   ├── Makefile                          # Build orchestration
│   └── go.mod
├── report/                               # Final report (.docx and PDF)
├── ppt/                                  # Presentation slides
└── ai/                                   # AI prompts and conversations
```

## Make Targets

| Target | Description |
|:-------|:------------|
| `make all` | Build images, load into KIND, deploy |
| `make build` | Build manager + agent binaries locally |
| `make images` | Build manager + agent Docker images |
| `make load` | Load Docker images into KIND cluster |
| `make deploy` | Apply all Kubernetes manifests (`kubectl apply -k config/`) |
| `make deploy-sample` | Apply the sample `NetworkHealthCheck` |
| `make deploy-monitoring` | Deploy Prometheus + Grafana + dashboard |
| `make undeploy` | Remove all deployed resources |
| `make cluster-up` | Create KIND cluster with Calico |
| `make cluster-down` | Tear down KIND cluster |
| `make test` | Run `go vet` + `go test` |
| `make inject-fault SCENARIO=F3` | Inject a fault scenario |
| `make clear-fault SCENARIO=F3` | Clear a fault scenario |
| `make measure-mttd SCENARIO=F5 TRIALS=3` | Measure MTTD for one scenario |
| `make p5-evaluation` | Run the P5 scenario sweep |
| `make demo` | Run the demo script |
| `make status` | Show cluster and operator status |
| `make logs-manager` / `make logs-agent` | Tail manager / agent logs |
| `make clean` | Remove built binaries and images |
| `make help` | Show all available targets |

## Tech Stack

| Category | Choice |
|:---------|:-------|
| Cluster | KIND (1 control-plane + 3 workers) |
| CNI | Calico v3.28.0 |
| Language | Go 1.22 |
| Kubernetes client | k8s.io/client-go (dynamic + typed), polling loop |
| Metrics | Hand-written Prometheus text format |
| Concurrency | Goroutines + channel semaphore + `sync.WaitGroup` |
| Testing | Go standard `testing` package + client-go fake clients |
| Fault injection | `docker exec` + iptables / tc netem, Calico pod kill, NetworkPolicy |
| Monitoring | Prometheus + Grafana |

## Security

The agent `/probe` endpoint is protected:

- **Bearer token** (`PROBE_TOKEN` on the agent, `AGENT_PROBE_TOKEN` on the manager, both from the `netprobe-agent-secret` Secret): requests without the correct token receive `401`. If `PROBE_TOKEN` is unset the agent logs a warning and runs unauthenticated (dev mode).
- **CIDR allowlist** (`PROBE_ALLOWED_CIDRS`, default `192.168.0.0/16,10.0.0.0/8,172.16.0.0/12`): probes to loopback, link-local (`169.254.0.0/16`), multicast, unspecified, or off-allowlist addresses receive `403`.
- **No hostname targets**: only IP literals are accepted, so DNS-based bypass is not possible.
- **Probe success = bidirectional exchange**: the data listener writes a 1-byte acknowledgement; the prober reads it before reporting success.
- **Capabilities**: the agent drops `NET_RAW` and adds none.
- `/healthz` and `/metrics` do not require the probe token.

## Limitations

- **Polling, not watches.** The manager lists `NetworkHealthCheck` objects every `POLL_INTERVAL` (15s), so detection latency is at least one interval plus `failureThreshold` rounds.
- **Single replica, no leader election.** Run exactly one manager replica.
- **Hysteresis state is in memory.** A manager restart resets the per-pair failure/success counters.
- **Bounded status.** Above 500 pairs, `status.reachability` is a sorted sample; use `reachabilitySummary` or metrics for the full picture.
- **One condition.** Status has a single `Healthy` condition whose reason carries `NoData`/`Degraded`/failure classes; there are no separate `ProbeReady` or `Stale` conditions.
- **TCP probes only.** No HTTP, ICMP, or DNS probing.
- **Alert-only remediation.** The operator reports; it does not repair the network.
- **Small evaluation.** Trial counts are 5–10 per scenario on one KIND cluster; results show ranges, not statistical significance.

## What Is Intentionally Not Implemented

These were deliberately left out because the required evidence, safety model, or correctness property was not yet in place.

- **`NetworkIntent` CRD / intent compiler**: a PathIntent DSL for desired-state specification
- **eBPF / Hubble / Retina integration**: kernel-level flow data fusion
- **`v1beta1` API + conversion webhook**: extended condition types and a versioned CRD schema
- **Automatic policy repair**: blocked on a safe-rollback model
- **Traffic shifting, node drain**: automatic remediation of any kind; current remediation is `AlertOnly`
- **HTTP / ICMP probe types**: advertised in earlier versions but never implemented; removed from the CRD enum
- **Multi-cluster or 200-node scale**: not tested or claimed
- **ML classifier model**: a deterministic rule-based classifier is used instead

## Team Details

| Field | Value |
|:------|:------|
| Team ID | 11 |
| Project ID | 7 |

| Name | Roll Number |
|:-----|:------------|
| Nilay Verma | 23110219 |
| Siddhesh Umarjee | 23110347 |
| Patel Ridham Vijaykumar | 23110238 |
| Eshaan Ashish Jaiswal | 24110116 |
| Anurag Singh | 24110061 |

## Team Roles

| Person | Artifact | Directory |
|:-------|:---------|:----------|
| Ridham | Integration, CRD, remediation | `cmd/manager/`, `internal/controller/`, `config/` |
| Anurag | Analysis, classifier, hysteresis | `pkg/analysis/`, `pkg/contract/`, `contracts/fixtures/` |
| Eshaan | Probe agent, security | `pkg/prober.go`, `pkg/server.go`, `cmd/agent/`, `config/agent/` |
| Nilay | Status, metrics, docs | `internal/controller/status.go`, `internal/controller/metrics.go`, `config/monitoring/`, `README.md` |
| Siddhesh | Evaluation, fault harness, CI | `hack/`, `docs/results/`, `.github/workflows/` |

## License

This is a research project for the Computer Networks course at IIT Gandhinagar (Semester 7).
