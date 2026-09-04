# P5 Evaluation Summary

- Total trials: 15
- Completed detections: 12
- Timeouts: 3

## Detection and Non-Functional Metrics

| Scenario | Trials | Accuracy | Avg MTTD ms | Avg RTT ms | Avg jitter ms | Avg loss rate | Max burst loss | Avg throughput Bps | Avg bytes/round | Avg probes/round |
|:--|--:|--:|--:|--:|--:|--:|--:|--:|--:|--:|
| F2 | 3 | 1.00 | 15073.0 | 0.396 | 0.073 | 0.333 | 5 | 1157342.16 | 20480.0 | 30.0 |
| F3 | 3 | 1.00 | 12991.7 | 0.416 | 0.097 | 0.333 | 5 | 1100736.50 | 20480.0 | 30.0 |
| F4 | 3 | 1.00 | 18614.7 | 0.194 | 0.039 | 0.667 | 5 | 585768.84 | 10240.0 | 30.0 |
| F5 | 3 | 0.00 | n/a | n/a | n/a | n/a | n/a | n/a | n/a | n/a |
| F6 | 3 | 1.00 | 14745.7 | 0.000 | 0.000 | 1.000 | 5 | 0.00 | 0.0 | 30.0 |

## Confusion Matrix

| True class | Predicted class | Count |
|:--|:--|--:|
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
