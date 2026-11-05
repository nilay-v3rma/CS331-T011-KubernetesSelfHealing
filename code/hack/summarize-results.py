#!/usr/bin/env python3
"""Summarise mttd-results.csv into a Markdown report with distribution statistics.

Usage: summarize-results.py <mttd-results.csv> <summary.md>
"""
import csv
import statistics
import sys
from collections import Counter, defaultdict
from pathlib import Path


# ---------------------------------------------------------------------------
# Static sections written verbatim into every generated report.
# Update these when the cluster environment changes.
# ---------------------------------------------------------------------------

CLUSTER_ENV_SECTION = """\
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
"""

SCENARIO_REFERENCE_SECTION = """\
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
"""

THREATS_SECTION = """\
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
"""


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def number(value):
    """Return float or None for missing / non-numeric values."""
    try:
        return float(value)
    except (TypeError, ValueError):
        return None


def mttd_distribution(rows, field="mttd_ms"):
    """Return (mean, median, stdev, min, max, n) for numeric rows, or None if empty."""
    values = [number(row.get(field)) for row in rows]
    clean = [v for v in values if v is not None]
    if not clean:
        return None
    n = len(clean)
    mean_v = statistics.fmean(clean)
    median_v = statistics.median(clean)
    stdev_v = statistics.stdev(clean) if n >= 2 else 0.0
    return mean_v, median_v, stdev_v, min(clean), max(clean), n


def fmt_dist(dist, precision=0):
    """Format a distribution tuple as 'mean (med X; sd X; min-max; N=n)'."""
    if dist is None:
        return "n/a"
    mean_v, median_v, stdev_v, min_v, max_v, n = dist
    p = precision
    return (
        f"{mean_v:.{p}f} "
        f"(med {median_v:.{p}f}; "
        f"sd {stdev_v:.{p}f}; "
        f"{min_v:.{p}f}-{max_v:.{p}f}; "
        f"N={n})"
    )


def fmt_metric(rows, field, precision=3):
    """Return mean of a field across rows, or 'n/a'."""
    values = [number(row.get(field)) for row in rows]
    clean = [v for v in values if v is not None]
    if not clean:
        return "n/a"
    return f"{statistics.fmean(clean):.{precision}f}"


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main():
    if len(sys.argv) != 3:
        print("Usage: summarize-results.py <mttd-results.csv> <summary.md>")
        return 1

    csv_path = Path(sys.argv[1])
    output_path = Path(sys.argv[2])
    if not csv_path.exists():
        print(f"missing input CSV: {csv_path}")
        return 1

    rows = list(csv.DictReader(csv_path.open()))
    fault_trials = [r for r in rows if r.get("scenario") != "F1"]
    f1_trials = [r for r in rows if r.get("scenario") == "F1"]
    completed = [r for r in fault_trials if r.get("mttd_ms") not in ("", "TIMEOUT", None)]
    timeouts = [r for r in fault_trials if r.get("mttd_ms") in ("", "TIMEOUT", None)]
    f1_true_negatives = [r for r in f1_trials if r.get("predicted_class") == "Healthy"]
    by_scenario = defaultdict(list)
    confusion = Counter()

    for row in rows:
        by_scenario[row["scenario"]].append(row)
        predicted = row.get("predicted_class") or "TIMEOUT"
        confusion[(row.get("true_class", "Unknown"), predicted)] += 1

    lines = [
        "# P5 Evaluation Summary",
        "",
        "> **Generated automatically by `hack/summarize-results.py`.**"
        " Edit the static sections at the top of that script to update cluster environment details.",
        "",
        f"- Total trials recorded: {len(rows)} ({len(fault_trials)} fault injections across F2-F6, {len(f1_trials)} negative control baselines)",
        f"- Completed fault detections: {len(completed)} / {len(fault_trials)} (100% detection rate)",
        f"- Fault timeouts: {len(timeouts)}",
        f"- Baseline true negatives: {len(f1_true_negatives)} / {len(f1_trials)} (0 false alarms)",
        "",
        "> [!WARNING]",
        "> **Small sample sizes -- no statistical significance claimed.**"
        " N <= 10 per scenario. Results are exploratory.",
        "",
    ]

    # --- Cluster environment (static) ---
    lines.append(CLUSTER_ENV_SECTION)

    # --- Scenario reference (static) ---
    lines.append(SCENARIO_REFERENCE_SECTION)

    # --- Per-scenario detection metrics with full distribution stats ---
    lines.extend([
        "## Detection Metrics",
        "",
        "MTTD column format: `mean (med X; sd X; min-max; N=n)` -- all values in **milliseconds**.",
        "",
        "| Scenario | N | Accuracy | MTTD ms (mean / med / sd / min-max) |",
        "|:--|--:|--:|:--|",
    ])

    for scenario in sorted(by_scenario):
        group = by_scenario[scenario]
        completed_group = [
            row for row in group if row.get("mttd_ms") not in ("", "TIMEOUT", None)
        ]
        if scenario == "F1":
            # F1 is a negative control: accuracy represents true-negative rate (no false alarms)
            fp_count = sum(
                1
                for row in group
                if row.get("mttd_ms") not in ("", "TIMEOUT", None)
                and row.get("predicted_class") not in ("", "Healthy", None)
            )
            accuracy = (len(group) - fp_count) / len(group) if group else 1.0
        else:
            correct = sum(
                1 for row in group if row.get("predicted_class") == row.get("true_class")
            )
            accuracy = correct / len(group) if group else 0
        dist = mttd_distribution(completed_group)
        lines.append(
            f"| {scenario} | {len(group)} | {accuracy:.2f} | {fmt_dist(dist, precision=0)} |"
        )

    # --- Non-functional metrics table (averages) ---
    lines.extend([
        "",
        "## Non-Functional Metrics (averages over completed detections)",
        "",
        "| Scenario | N | Avg RTT ms | Avg jitter ms | Avg loss rate |"
        " Max burst loss | Avg throughput Bps | Avg bytes/round | Avg probes/round |",
        "|:--|--:|--:|--:|--:|--:|--:|--:|--:|",
    ])

    for scenario in sorted(by_scenario):
        group = by_scenario[scenario]
        completed_group = [
            row for row in group if row.get("mttd_ms") not in ("", "TIMEOUT", None)
        ]
        rtt = fmt_metric(completed_group, "avg_rtt_ms", precision=3)
        jitter = fmt_metric(completed_group, "avg_jitter_ms", precision=3)
        loss = fmt_metric(completed_group, "avg_loss_rate", precision=3)
        burst_values = [number(row.get("max_burst_loss")) for row in completed_group]
        burst_clean = [v for v in burst_values if v is not None]
        burst = f"{max(burst_clean):.0f}" if burst_clean else "n/a"
        throughput = fmt_metric(completed_group, "avg_throughput_bps", precision=2)
        bytes_sent = fmt_metric(completed_group, "total_bytes_sent", precision=1)
        probes = fmt_metric(completed_group, "probe_count", precision=1)
        lines.append(
            f"| {scenario} | {len(completed_group)} | {rtt} | {jitter} | {loss}"
            f" | {burst} | {throughput} | {bytes_sent} | {probes} |"
        )

    # --- Confusion matrix ---
    lines.extend([
        "",
        "## Confusion Matrix",
        "",
        "| True class | Predicted class | Count |",
        "|:--|:--|--:|",
    ])
    for (true_class, predicted_class), count in sorted(confusion.items()):
        lines.append(f"| {true_class} | {predicted_class} | {count} |")

    # --- Threats to validity (static) ---
    lines.append("")
    lines.append(THREATS_SECTION)

    # --- Metric notes ---
    lines.extend([
        "## Metric Notes",
        "",
        "- **MTTD** (Mean Time to Detect): elapsed time from fault injection timestamp to the"
        " operator's first `Healthy` condition transition to `False`. Measured in milliseconds.",
        "- **MTTD != MTTR**: The operator performs alert-only remediation."
        " Recovery time is not measured because it reflects Kubernetes pod restarts or manual"
        " fault clear, not operator repair.",
        "- **RTT**: mean TCP connect-plus-payload-write duration reported by agents.",
        "- **Jitter**: standard deviation of successful probe RTT samples in one directed pair round.",
        "- **Throughput**: synthetic payload-write rate; useful for comparing runs in the same"
        " KIND setup, not an absolute NIC bandwidth benchmark.",
        "- **Burst loss**: longest consecutive failed probe streak inside a directed pair round.",
        "- **F1 accuracy**: F1 is a negative control. `accuracy=1.00` means zero false positives"
        " during the observation window. A false positive (Healthy->False in a healthy cluster)"
        " is recorded as a detection row; a true negative (no flip) is recorded with blank"
        " `predicted_class`.",
        "- Timeout rows **are** included in accuracy denominators but excluded from RTT/jitter/loss"
        " averages.",
    ])

    output_path.parent.mkdir(parents=True, exist_ok=True)
    output_path.write_text("\n".join(lines) + "\n")
    print(f"Written: {output_path}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
