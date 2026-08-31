# hack/ — Scripts & Cluster Tooling

## Prerequisites

- [Docker](https://docs.docker.com/get-docker/) (running)
- [kind](https://kind.sigs.k8s.io/docs/user/quick-start/#installation) v0.20+
- [kubectl](https://kubernetes.io/docs/tasks/tools/) configured
- [jq](https://jqlang.github.io/jq/download/) (for `measure-mttd.sh`)

## Scripts

| Script | Purpose | Usage |
|:-------|:--------|:------|
| `setup-cluster.sh` | Create the 4-node KIND cluster, install Calico CNI, verify cross-node connectivity | `./hack/setup-cluster.sh` |
| `reset-cluster.sh` | Tear down the KIND cluster and clean up | `./hack/reset-cluster.sh` |
| `inject-fault.sh` | Inject/clear fault scenarios F1–F6 for testing | `./hack/inject-fault.sh <F1\|F2\|...\|F6> [--clear]` |
| `measure-mttd.sh` | Measure Mean Time To Detect after fault injection | `./hack/measure-mttd.sh <scenario> [trials]` |
| `demo.sh` | 5-minute reproducible demo: healthy → fault → detect → recover | `./hack/demo.sh` |

## Cluster Details

- **Cluster name:** `pod-pod-selfheal`
- **Nodes:** 1 control-plane + 3 workers
- **CNI:** Calico v3.28.0
- **Pod subnet:** `192.168.0.0/16`

## KIND Cluster Config

See [`kind-cluster.yaml`](kind-cluster.yaml) for the cluster topology.

## Fault Scenarios

| ID | Scenario | Expected Classification |
|:---|:---------|:-----------------------|
| F1 | Baseline (healthy) | `Healthy` |
| F2 | Node inbound block (iptables DROP on :9376 INPUT) | `NodeIngressFailure` |
| F3 | Node outbound block (iptables DROP on :9376 OUTPUT) | `NodeEgressFailure` |
| F4 | CNI agent kill (delete calico-node pod) | `NodeIsolated` |
| F5 | Partial packet loss (40% via tc netem) | `Degraded` |
| F6 | Deny-all NetworkPolicy in agent namespace | `ClusterPartition` |
