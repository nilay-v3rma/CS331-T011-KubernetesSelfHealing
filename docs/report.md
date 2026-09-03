# P5 Evaluation Report

## Goal

P5 evaluates whether the operator detects and localizes pod-to-pod data-plane failures reliably, and whether its non-functional behavior is measurable. The evaluation focuses on performance, simplicity, and robustness.

## Metrics

| Requirement | Metric | How it is measured |
|:--|:--|:--|
| Performance | Detection latency / MTTD | Time from fault injection to the `Healthy=False` status transition. |
| Performance | Probe delay / latency | Mean TCP connect-plus-payload-write duration for each directed pair. |
| Performance | Bandwidth / throughput | Synthetic payload-write bytes per second from each source agent to each destination agent. |
| Robustness | Packet loss | Failed probes divided by attempted probes in a directed pair round. |
| Robustness | Burst loss | Longest consecutive failed probe streak within a directed pair round. |
| Robustness | Jitter | Standard deviation of successful RTT samples in a directed pair round. |
| Simplicity | Probe count and bytes per round | Directly reported from status and summarized from CSV. |

## Protocol

1. Start from a healthy cluster and clear any previous scenario state.
2. Inject one fault scenario (`F2` to `F6`) and record the injection timestamp.
3. Poll `NetworkHealthCheck/status` until the controller reports `Healthy=False`.
4. Append one CSV row with detection latency, classification result, confidence, and aggregate non-functional metrics.
5. Clear the fault and allow the cluster to stabilize before the next trial.

Run the full p5 evaluation with:

```bash
TRIALS=10 ./hack/run-p5-evaluation.sh
```

For a shorter demo run:

```bash
TRIALS=1 SCENARIOS="F2 F3 F5" ./hack/run-p5-evaluation.sh
```

Raw data is written to `docs/results/mttd-results.csv`. The generated summary is written to `docs/results/p5-summary.md`.

## Experiments

| Experiment | Independent variable | Output |
|:--|:--|:--|
| E1: Detection latency | Fault type `F2` to `F6` | MTTD per trial and mean MTTD per scenario. |
| E2: Localization accuracy | Fault type, repeated trials | Confusion matrix, accuracy per scenario. |
| E3: Robustness under repeated sampling | Loss and jitter from multi-sample probe rounds | Loss rate, burst loss, jitter. |
| E4: Overhead | Cluster size and topology mode | Probe count, bytes sent, round duration. |
| E5: Sampled vs full mesh | `topology`, `sampleDegree` | Probe-count reduction and detection penalty. |

## Current Instrumentation

Each agent probe round now reports:

- `lossRate`
- `rttMillis`, `rttMinMillis`, `rttMaxMillis`
- `jitterMillis`
- `burstLossMax`
- `throughputBps` and `bandwidthBps`
- `bytesSent`
- `probeCount` and `successfulProbes`
- `durationMillis`

The controller preserves these fields in `.status.reachability[]`, so p5 can measure non-functional behavior from Kubernetes state without scraping logs.

## Threats to Validity

KIND runs all nodes as containers on one machine. The results are valid for detection, classification, and relative comparison between scenarios, but they should not be presented as real physical-network bandwidth or latency.

TCP connect-plus-payload-write is a lightweight synthetic probe. It gives stable relative measurements for this project, but it is not a replacement for iperf-style saturation testing.

The classifier is evaluated against the same fault taxonomy used by the project. The report should describe this and avoid claiming universal localization accuracy.
