# P5 Evaluation Summary

> **Generated automatically by `hack/summarize-results.py`.** Edit the static sections at the top of that script to update cluster environment details.

- Total trials recorded: 60 (50 fault injections across F2-F6, 10 negative control baselines)
- Completed fault detections: 50 / 50 (100% detection rate)
- Fault timeouts: 0
- Baseline true negatives: 10 / 10 (0 false alarms)

> [!WARNING]
> **Small sample sizes -- no statistical significance claimed.** N <= 10 per scenario. Results are exploratory.

## Cluster Environment

| Property | Value |
|:--|:--|
| KIND version | v0.23.0 |
| Kubernetes version | v1.30.x (KIND default for v0.23.0) |
| CNI | Calico v3.28.0 |
| Node layout | 1 control-plane + 3 workers (4-node KIND cluster) |
| Manager image | selfheal-manager:latest (see git commit SHA in CI run) |
| Agent image | selfheal-agent:latest (see git commit SHA in CI run) |
| Data port | 9376 (TCP probe data listener) |
| Control port | 8080 (agent HTTP API) |

> To reproduce: `make cluster-up && make images && make load && make deploy`

## Scenario Reference

| Scenario | Description | Expected / True Class | Injection mechanism |
|:--|:--|:--|:--|
| F1 | Healthy baseline -- no fault injected | `Healthy` (control) | None; watch for false positives |
| F2 | Node inbound TCP block (port 9376) | `NodeIngressFailure` | `docker exec <node> iptables -I INPUT -p tcp --dport 9376 -j DROP` |
| F3 | Node outbound TCP block (port 9376) | `NodeEgressFailure` | `docker exec <node> iptables -I OUTPUT -p tcp --dport 9376 -j DROP` |
| F4 | Calico node pod killed | `NodeIsolated` | `kubectl delete pod -n kube-system -l k8s-app=calico-node` |
| F5 | 40% random packet loss via tc netem | `Degraded` | `docker exec <node> tc qdisc add dev <iface> root netem loss 40%` |
| F6 | Deny-all NetworkPolicy | `ClusterPartition` | `kubectl apply -f hack/deny-all.yaml` |

> [!IMPORTANT]
> F5 expected / true class is **`Degraded`** (requires classifier v1.1 with `DegradedLossThreshold`).
> F1 is a negative control: a false positive is any transition of the `Healthy` condition to `False` during the observation window.

## Detection Metrics

MTTD column format: `mean (med X; sd X; min-max; N=n)` -- all values in **milliseconds**.

| Scenario | N | Accuracy | MTTD ms (mean / med / sd / min-max) |
|:--|--:|--:|:--|
| F1 | 10 | 1.00 | n/a |
| F2 | 10 | 1.00 | 84974 (med 85174; sd 756; 82874-85546; N=10) |
| F3 | 10 | 1.00 | 84963 (med 85062; sd 384; 83893-85238; N=10) |
| F4 | 10 | 1.00 | 85084 (med 84802; sd 714; 84446-86195; N=10) |
| F5 | 10 | 1.00 | 25847 (med 20668; sd 12080; 18670-49261; N=10) |
| F6 | 10 | 1.00 | 84994 (med 84746; sd 567; 84638-86105; N=10) |

## Non-Functional Metrics (averages over completed detections)

| Scenario | N | Avg RTT ms | Avg jitter ms | Avg loss rate | Max burst loss | Avg throughput Bps | Avg bytes/round | Avg probes/round |
|:--|--:|--:|--:|--:|--:|--:|--:|--:|
| F1 | 0 | n/a | n/a | n/a | n/a | n/a | n/a | n/a |
| F2 | 10 | 0.404 | 0.191 | 0.333 | 5 | 1167093.40 | 20480.0 | 30.0 |
| F3 | 10 | 0.596 | 0.604 | 0.333 | 5 | 1158409.82 | 20480.0 | 30.0 |
| F4 | 10 | 0.137 | 0.025 | 0.667 | 5 | 713020.38 | 10240.0 | 30.0 |
| F5 | 10 | 292.745 | 301.642 | 0.073 | 2 | 666340.59 | 28467.2 | 30.0 |
| F6 | 10 | 0.000 | 0.000 | 1.000 | 5 | 0.00 | 0.0 | 30.0 |

## Confusion Matrix

| True class | Predicted class | Count |
|:--|:--|--:|
| ClusterPartition | ClusterPartition | 10 |
| Degraded | Degraded | 10 |
| Healthy | Healthy | 10 |
| NodeEgressFailure | NodeEgressFailure | 10 |
| NodeIngressFailure | NodeIngressFailure | 10 |
| NodeIsolated | NodeIsolated | 10 |

## Threats to Validity

| Threat | Mitigation |
|:--|:--|
| **Small sample size** | N <= 10 per scenario. Results are exploratory; no statistical significance is claimed. Report distributions (median, min/max) rather than a single mean. |
| **KIND virtualisation** | KIND nodes are Docker containers sharing the host kernel. `tc netem` and `iptables` behaviour may differ from bare-metal or VM clusters. |
| **F5 interface scope** | `tc netem loss 40%` on the node's default-route interface (eth0) affects all traffic, not only port 9376. Results represent the scenario with that caveat. |
| **Probe interval alignment** | MTTD is bounded below by the probe interval (default 15 s). A fault injected just after a probe round will not be detected until the next round. The distribution captures this jitter; the mean alone would obscure it. |
| **F4 recovery time** | F4 clear waits for the Calico DaemonSet pod to become Ready, which is dominated by container restart time, not operator detection latency. |
| **Single-node fault target** | All faults target `pod-pod-selfheal-worker2`. Behaviour on other nodes or multi-node faults is not evaluated. |
| **MTTD vs MTTR** | Only MTTD is measured. The operator performs alert-only remediation; recovery time reflects manual clear or Kubernetes pod restart, not operator repair. |

## Metric Notes

- **MTTD** (Mean Time to Detect): elapsed time from fault injection timestamp to the operator's first `Healthy` condition transition to `False`. Measured in milliseconds.
- **MTTD != MTTR**: The operator performs alert-only remediation. Recovery time is not measured because it reflects Kubernetes pod restarts or manual fault clear, not operator repair.
- **RTT**: mean TCP connect-plus-payload-write duration reported by agents.
- **Jitter**: standard deviation of successful probe RTT samples in one directed pair round.
- **Throughput**: synthetic payload-write rate; useful for comparing runs in the same KIND setup, not an absolute NIC bandwidth benchmark.
- **Burst loss**: longest consecutive failed probe streak inside a directed pair round.
- **F1 accuracy**: F1 is a negative control. `accuracy=1.00` means zero false positives during the observation window. A false positive (Healthy->False in a healthy cluster) is recorded as a detection row; a true negative (no flip) is recorded with blank `predicted_class`.
- Timeout rows **are** included in accuracy denominators but excluded from RTT/jitter/loss averages.
