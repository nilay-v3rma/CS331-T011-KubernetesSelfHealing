# Self-Healing Network Operator for Kubernetes

**Pod-to-Pod Data Plane Connectivity via Synthetic Probing**

A Kubernetes operator that maintains a continuously refreshed east-west reachability matrix using distributed synthetic probes, classifies the blast radius of data plane failures, and exposes results as first-class Kubernetes API objects and Prometheus metrics.

## Architecture

```
                   ┌──────────────────────────────────────┐
                   │  NetworkHealthCheck (CRD, cluster)   │
                   │  spec: interval, selectors, probe,   │
                   │        thresholds                    │
                   │  status: matrix, conditions, verdict │
                   └───────────────┬──────────────────────┘
                                   │ watch / reconcile
                   ┌───────────────▼──────────────────────┐
                   │  Operator (Deployment, 1 replica)    │
                   │  - discovers agent pods              │
                   │  - builds probe matrix               │
                   │  - fans out probe RPCs (bounded)     │
                   │  - aggregates + hysteresis           │
                   │  - classifies blast radius           │
                   │  - writes status + metrics           │
                   └───────┬───────────┬──────────┬───────┘
                           │ HTTP      │          │
              ┌────────────▼──┐  ┌─────▼───────┐  ┌▼─────────────┐
              │ agent (node1) │  │agent (node2)│  │agent (node3) │
              │ /probe?to=IP  │  │             │  │              │
              │ /healthz      │  │  DaemonSet, pod network       │
              └───────────────┘  └─────────────┘  └──────────────┘
                    └──────── actual data plane traffic ─────────┘
```

The operator never sends data plane traffic itself. It only *orchestrates*. Agent-to-agent traffic traverses exactly the path a real workload would.

## Prerequisites

- [Docker](https://docs.docker.com/get-docker/) (running)
- [KIND](https://kind.sigs.k8s.io/docs/user/quick-start/#installation) v0.20+
- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [Go](https://go.dev/dl/) 1.22+
- [jq](https://jqlang.github.io/jq/download/) (for measurement scripts)

## Quick Start

```bash
# 1. Create the 4-node KIND cluster with Calico CNI
make cluster-up

# 2. Build Docker images and load them into KIND
make images load

# 3. Deploy the operator, agents, CRD, and RBAC
make deploy

# 4. Apply a sample NetworkHealthCheck
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

## Project Structure

```
├── cmd/
│   ├── manager/main.go              # Operator entry point
│   └── agent/main.go                # Agent entry point
├── config/
│   ├── namespace.yaml               # netprobe-system namespace
│   ├── crd/                         # NetworkHealthCheck CRD
│   ├── rbac/                        # ClusterRole, binding, SA
│   ├── manager/                     # Operator Deployment
│   ├── agent/                       # Agent DaemonSet + Service
│   ├── samples/                     # Example CRs
│   ├── monitoring/                  # Prometheus + Grafana
│   └── kustomization.yaml           # Root kustomization
├── contracts/fixtures/              # Test matrices (11 scenarios)
├── docs/results/                    # P5 evaluation outputs
├── hack/
│   ├── kind-cluster.yaml            # KIND cluster config
│   ├── setup-cluster.sh             # Cluster creation + Calico
│   ├── reset-cluster.sh             # Cluster teardown
│   ├── inject-fault.sh              # Fault scenarios F1–F6
│   ├── deny-all.yaml                # NetworkPolicy for F6
│   ├── measure-mttd.sh              # MTTD measurement
│   └── demo.sh                      # Demo script
├── internal/controller/             # Controller logic
├── pkg/
│   ├── contract/types.go            # Frozen contract types
│   ├── analysis/                    # Classifier + hysteresis
│   ├── prober.go                    # Agent prober code
│   └── server.go                    # Agent server code
├── Dockerfile                       # Manager image
├── Dockerfile.agent                 # Agent image
├── Makefile                         # Build orchestration
└── go.mod
```

## Fault Scenarios

| ID | Scenario | Command | Expected Classification |
|:---|:---------|:--------|:-----------------------|
| F1 | Baseline (healthy) | `make inject-fault SCENARIO=F1` | `Healthy` |
| F2 | Node inbound block | `make inject-fault SCENARIO=F2` | `NodeIngressFailure` |
| F3 | Node outbound block | `make inject-fault SCENARIO=F3` | `NodeEgressFailure` |
| F4 | CNI agent kill | `make inject-fault SCENARIO=F4` | `NodeIsolated` |
| F5 | Partial packet loss (40%) | `make inject-fault SCENARIO=F5` | `Degraded` |
| F6 | Deny-all NetworkPolicy | `make inject-fault SCENARIO=F6` | `ClusterPartition` |

## Make Targets

| Target | Description |
|:-------|:------------|
| `make all` | Build images, load into KIND, deploy |
| `make images` | Build manager + agent Docker images |
| `make load` | Load images into KIND cluster |
| `make deploy` | Apply all Kubernetes manifests |
| `make undeploy` | Remove all deployed resources |
| `make cluster-up` | Create KIND cluster with Calico |
| `make cluster-down` | Tear down KIND cluster |
| `make test` | Run `go vet` + `go test` |
| `make inject-fault SCENARIO=F3` | Inject a fault scenario |
| `make clear-fault SCENARIO=F3` | Clear a fault scenario |
| `make status` | Show cluster and operator status |
| `make help` | Show all available targets |

## Monitoring

Deploy the Prometheus + Grafana stack:

```bash
make deploy-monitoring
```

Access:
- **Prometheus:** `kubectl port-forward -n monitoring svc/prometheus 9090:9090`
- **Grafana:** `kubectl port-forward -n monitoring svc/grafana 3000:3000` (admin/admin)

### Exported Metrics

| Metric | Type | Labels | Description |
|:-------|:-----|:-------|:------------|
| `netprobe_manager_up` | Gauge | — | Manager is running (always 1) |
| `netprobe_pair_reachable` | Gauge (0/1) | `src_node`, `dst_node` | Whether a directed pair is reachable |
| `netprobe_pair_rtt_seconds` | Gauge | `src_node`, `dst_node` | Average RTT for the pair |
| `netprobe_pair_loss_rate` | Gauge | `src_node`, `dst_node` | Measured loss rate for the pair |
| `netprobe_round_duration_seconds` | Gauge | — | Duration of last probe round |
| `netprobe_mesh_health` | Gauge (0/1) | — | 1 if verdict is Healthy, 0 otherwise |
| `netprobe_classification` | Gauge (always 1) | `classification` | Current classification label |
| `netprobe_classification_confidence` | Gauge | `classification` | Confidence score of current classification |
| `netprobe_reachability_pairs` | Gauge | `state` (healthy/degraded/failed/nodata) | Count of pairs in each state |

## P5 Evaluation

P5 measures detection and non-functional behavior from the live `NetworkHealthCheck` status:

```bash
# One scenario
make measure-mttd SCENARIO=F5 TRIALS=3

# Full evaluation sweep
TRIALS=10 ./hack/run-p5-evaluation.sh
```

Outputs:
- Raw CSV: `docs/results/mttd-results.csv`
- Summary table and confusion matrix: `docs/results/p5-summary.md`

Captured metrics include MTTD, RTT/delay, jitter, loss rate, maximum burst loss, synthetic throughput/bandwidth, bytes per round, and probe count.

## Tech Stack

| Category | Choice |
|:---------|:-------|
| Cluster | KIND (1 control-plane + 2 workers) |
| CNI | Calico v3.28 |
| Language | Go 1.22 |
| Kubernetes client | k8s.io/client-go (dynamic + typed) |
| Metrics | Hand-written Prometheus text format |
| Concurrency | Go goroutines + semaphore |
| Testing | Go standard `testing` package + `k8s.io/client-go/kubernetes/fake` |
| Fault injection | `docker exec` + iptables/tc netem |
| Monitoring | Prometheus + Grafana |

## What Is Intentionally Not Implemented

The following capabilities are designed in `lookforward.md` but deliberately outside this week's scope.
For each item the prerequisite evidence, safety model, or correctness property was not yet met.

- **`NetworkIntent` CRD / intent compiler** — PathIntent DSL for desired-state specification
- **eBPF / Hubble / Retina integration** — kernel-level flow data fusion
- **`v1beta1` API + conversion webhook** — extended condition types and versioned CRD schema
- **Automatic policy repair** — blocking on the safe-rollback model in `lookforward.md`
- **Traffic shifting, node drain** — automatic remediation of any kind; current remediation is `AlertOnly`
- **HTTP / ICMP probe types** — advertised but never implemented; removed from CRD enum to avoid lying
- **Multi-cluster or 200-node scale** — not tested or claimed
- **ML / classifier model** — purely rule-based deterministic classifier used instead

See `lookforward.md` for the full future design.

## Security

The agent `/probe` endpoint is protected:

- **Bearer token** (`PROBE_TOKEN` env var on agent, `AGENT_PROBE_TOKEN` on manager): requests without the correct token receive `401`.
- **CIDR allowlist** (`PROBE_ALLOWED_CIDRS`, default `192.168.0.0/16,10.0.0.0/8,172.16.0.0/12`): probes to loopback, link-local (`169.254.0.0/16`), multicast, or outside the allowlist receive `403`.
- **No hostname targets**: only IP literals are accepted; DNS-based bypass is not possible.
- **Probe success = bidirectional exchange**: the data listener writes a 1-byte acknowledgement; the prober reads it before reporting success.
- `/healthz` and `/metrics` do not require the probe token.

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
| Eeshan | Probe agent, security | `pkg/prober.go`, `pkg/server.go`, `cmd/agent/`, `config/agent/` |
| Nilay | Status, metrics, docs | `internal/controller/status.go`, `internal/controller/metrics.go`, `config/monitoring/`, `README.md` |
| Siddhesh | Evaluation, fault harness, CI | `hack/`, `docs/results/`, `.github/workflows/` |

## License

This is a research project for the Computer Networks course at IIT Gandhinagar (Semester 7).
