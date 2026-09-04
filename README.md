# Self-Healing Network Operator for Kubernetes

**Pod-to-Pod Data Plane Connectivity via Synthetic Probing**

A Kubernetes operator that maintains a continuously refreshed east-west reachability matrix using distributed synthetic probes, classifies the blast radius of data plane failures, and exposes results as first-class Kubernetes API objects and Prometheus metrics.

## Architecture

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

## 5-Minute Demo

```bash
# Run the interactive demo script
make demo
```

This walks through: healthy baseline → fault injection → detection → clearance → recovery.

## Project Structure

```
├── api/v1alpha1/                    # CRD types (P3)
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
├── hack/
│   ├── kind-cluster.yaml            # KIND cluster config
│   ├── setup-cluster.sh             # Cluster creation + Calico
│   ├── reset-cluster.sh             # Cluster teardown
│   ├── inject-fault.sh              # Fault scenarios F1–F6
│   ├── deny-all.yaml                # NetworkPolicy for F6
│   ├── measure-mttd.sh              # MTTD measurement
│   └── demo.sh                      # 5-minute demo
├── internal/controller/             # Controller logic (P3)
├── pkg/
│   ├── contract/types.go            # Frozen contract types
│   └── analysis/                    # Classifier + hysteresis (P4)
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
| `make demo` | Run 5-minute interactive demo |
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

| Metric | Type | Labels |
|:-------|:-----|:-------|
| `netprobe_pair_reachable` | Gauge (0/1) | `src_node`, `dst_node`, `src_pod`, `dst_pod` |
| `netprobe_pair_rtt_seconds` | Histogram | `src_node`, `dst_node` |
| `netprobe_pair_jitter_seconds` | Gauge | `src_node`, `dst_node` |
| `netprobe_pair_loss_burst` | Gauge | `src_node`, `dst_node` |
| `netprobe_pair_throughput_bytes_per_second` | Gauge | `src_node`, `dst_node` |
| `netprobe_probe_errors_total` | Counter | `src_node`, `dst_node`, `reason` |
| `netprobe_round_duration_seconds` | Histogram | `topology_mode` |
| `netprobe_mesh_health` | Gauge | `verdict` |
| `netprobe_classification` | Gauge (0/1) | `classification`, `suspect_node` |
| `netprobe_probes_sent_total` | Counter | `topology_mode` |

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
| Cluster | KIND (1 control-plane + 3 workers) |
| CNI | Calico v3.28.0 |
| Language | Go 1.22+ |
| Scaffolding | kubebuilder v4 + controller-runtime |
| Metrics | prometheus/client_golang |
| Concurrency | golang.org/x/sync/errgroup |
| Testing | envtest + Ginkgo/Gomega |
| Fault injection | docker exec + iptables/tc netem |
| Monitoring | Prometheus + Grafana |

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
| Eshan Jaiswal | 24110116 |
| Anurag Singh | 24110061 |

## Team Roles

| Person | Artifact | Directory |
|:-------|:---------|:----------|
| P1 (Platform) | Cluster, images, CI, manifests | `hack/`, `config/`, `Dockerfile*`, `Makefile` |
| P2 (Agent) | Probe agent binary | `cmd/agent/`, `pkg/agent/` |
| P3 (Operator) | CRD + controller | `api/`, `cmd/manager/`, `internal/controller/` |
| P4 (Analysis) | Classifier library | `pkg/analysis/` |
| P5 (Evaluation) | Experiments + report | `experiments/`, `test/`, `docs/` |

## License

This is a research project for the Computer Networks course at IIT Gandhinagar (Semester 7).
