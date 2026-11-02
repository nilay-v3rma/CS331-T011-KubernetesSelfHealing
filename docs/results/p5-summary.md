# P5 Evaluation Summary

- Total trials: 15 (3 per scenario × F2–F6) + F1 control trials (placeholder; see notes)
- Completed detections: 12
- Timeouts: 3 (all from F5 — packet-loss scenario not yet reliably detected)

> **Cluster shape:** KIND 3-node cluster (1 control-plane, 2 workers), Calico CNI

## Scenario Reference

| Scenario | Description | Expected / True Class | Injection mechanism |
|:--|:--|:--|:--|
| F1 | Healthy baseline — no fault | `Healthy` (control) | None; watch for false positives |
| F2 | Node inbound TCP block (port 9376) | `NodeIngressFailure` | `docker exec <node> iptables -I INPUT` |
| F3 | Node outbound TCP block (port 9376) | `NodeEgressFailure` | `docker exec <node> iptables -I OUTPUT` |
| F4 | Calico node pod killed | `NodeIsolated` | `kubectl delete pod -n kube-system -l k8s-app=calico-node` |
| F5 | 40 % random packet loss | `Degraded` | `docker exec <node> tc qdisc add … netem loss 40%` |
| F6 | Deny-all NetworkPolicy | `ClusterPartition` | `kubectl apply -f hack/deny-all.yaml` |

> [!IMPORTANT]
> F5 expected / true class is **`Degraded`** (updated from earlier versions that incorrectly listed it as `Healthy`).

## Detection and Non-Functional Metrics

| Scenario | Trials | Accuracy | Avg MTTD ms | Avg RTT ms | Avg jitter ms | Avg loss rate | Max burst loss | Avg throughput Bps | Avg bytes/round | Avg probes/round |
|:--|--:|--:|--:|--:|--:|--:|--:|--:|--:|--:|
| F1 | — | — (control) | — | — | — | — | — | — | — | — |
| F2 | 3 | 1.00 | 15073.0 | 0.396 | 0.073 | 0.333 | 5 | 1157342.16 | 20480.0 | 30.0 |
| F3 | 3 | 1.00 | 12991.7 | 0.416 | 0.097 | 0.333 | 5 | 1100736.50 | 20480.0 | 30.0 |
| F4 | 3 | 1.00 | 18614.7 | 0.194 | 0.039 | 0.667 | 5 | 585768.84 | 10240.0 | 30.0 |
| F5 | 3 | 0.00 | n/a | n/a | n/a | n/a | n/a | n/a | n/a | n/a |
| F6 | 3 | 1.00 | 14745.7 | 0.000 | 0.000 | 1.000 | 5 | 0.00 | 0.0 | 30.0 |

## Confusion Matrix

| True class | Predicted class | Count |
|:--|:--|--:|
| Healthy (F1 control) | — (placeholder; no trials run yet) | — |
| ClusterPartition | ClusterPartition | 3 |
| Degraded | TIMEOUT | 3 |
| NodeEgressFailure | NodeEgressFailure | 3 |
| NodeIngressFailure | NodeIngressFailure | 3 |
| NodeIsolated | NodeIsolated | 3 |

## Notes

- RTT is the mean TCP connect-plus-payload-write duration reported by agents.
- Jitter is the standard deviation of successful probe RTT samples in one directed pair round.
- Throughput is a synthetic payload-write rate, useful for comparing runs in the same KIND setup, not an absolute NIC bandwidth benchmark.
- Burst loss is the longest consecutive failed probe streak inside a directed pair round.
- Timeout rows are included in accuracy, but probe-level averages are marked n/a because no completed detection row was produced.

> [!WARNING]
> **Small sample sizes — no statistical significance.** Each non-F1 scenario has only 3 trials. Results should be treated as exploratory; conclusions require substantially larger sample sizes and repeated runs.

> [!NOTE]
> **F1 (Healthy baseline)** serves as a false-positive control. The detection loop watches for the `Healthy` condition flipping to `False` during the observation window; any such event is a false positive and is recorded in `mttd-results.csv` with `true_class=Healthy`. A timeout (no flip) is a true negative and is recorded with `predicted_class` left blank. F1 trials are excluded from accuracy calculations for fault scenarios.
