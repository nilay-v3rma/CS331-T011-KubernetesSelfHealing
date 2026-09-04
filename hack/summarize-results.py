#!/usr/bin/env python3
import csv
import statistics
import sys
from collections import Counter, defaultdict
from pathlib import Path


def number(value):
    try:
        return float(value)
    except (TypeError, ValueError):
        return None


def mean(values):
    clean = [value for value in values if value is not None]
    return statistics.fmean(clean) if clean else 0.0


def fmt_metric(rows, field, precision=3):
    values = [number(row.get(field)) for row in rows]
    clean = [value for value in values if value is not None]
    if not clean:
        return "n/a"
    return f"{statistics.fmean(clean):.{precision}f}"


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
    completed = [row for row in rows if row.get("mttd_ms") not in ("", "TIMEOUT", None)]
    by_scenario = defaultdict(list)
    confusion = Counter()

    for row in rows:
        by_scenario[row["scenario"]].append(row)
        predicted = row.get("predicted_class") or "TIMEOUT"
        confusion[(row.get("true_class", "Unknown"), predicted)] += 1

    lines = [
        "# P5 Evaluation Summary",
        "",
        f"- Total trials: {len(rows)}",
        f"- Completed detections: {len(completed)}",
        f"- Timeouts: {len(rows) - len(completed)}",
        "",
        "## Detection and Non-Functional Metrics",
        "",
        "| Scenario | Trials | Accuracy | Avg MTTD ms | Avg RTT ms | Avg jitter ms | Avg loss rate | Max burst loss | Avg throughput Bps | Avg bytes/round | Avg probes/round |",
        "|:--|--:|--:|--:|--:|--:|--:|--:|--:|--:|--:|",
    ]

    for scenario in sorted(by_scenario):
        group = by_scenario[scenario]
        completed_group = [row for row in group if row.get("mttd_ms") not in ("", "TIMEOUT", None)]
        correct = sum(1 for row in group if row.get("predicted_class") == row.get("true_class"))
        accuracy = correct / len(group) if group else 0
        mttd = fmt_metric(completed_group, "mttd_ms", precision=1)
        rtt = fmt_metric(completed_group, "avg_rtt_ms", precision=3)
        jitter = fmt_metric(completed_group, "avg_jitter_ms", precision=3)
        loss = fmt_metric(completed_group, "avg_loss_rate", precision=3)
        burst_values = [number(row.get("max_burst_loss")) for row in completed_group]
        burst_clean = [value for value in burst_values if value is not None]
        burst = f"{max(burst_clean):.0f}" if burst_clean else "n/a"
        throughput = fmt_metric(completed_group, "avg_throughput_bps", precision=2)
        bytes_sent = fmt_metric(completed_group, "total_bytes_sent", precision=1)
        probes = fmt_metric(completed_group, "probe_count", precision=1)
        lines.append(
            "| {scenario} | {trials} | {accuracy:.2f} | {mttd} | {rtt} | {jitter} | {loss} | {burst} | {throughput} | {bytes_sent} | {probes} |".format(
                scenario=scenario,
                trials=len(group),
                accuracy=accuracy,
                mttd=mttd,
                rtt=rtt,
                jitter=jitter,
                loss=loss,
                burst=burst,
                throughput=throughput,
                bytes_sent=bytes_sent,
                probes=probes,
            )
        )

    lines.extend([
        "",
        "## Confusion Matrix",
        "",
        "| True class | Predicted class | Count |",
        "|:--|:--|--:|",
    ])
    for (true_class, predicted_class), count in sorted(confusion.items()):
        lines.append(f"| {true_class} | {predicted_class} | {count} |")

    lines.extend([
        "",
        "## Notes",
        "",
        "- RTT is the mean TCP connect-plus-payload-write duration reported by agents.",
        "- Jitter is the standard deviation of successful probe RTT samples in one directed pair round.",
        "- Throughput is a synthetic payload-write rate, useful for comparing runs in the same KIND setup, not an absolute NIC bandwidth benchmark.",
        "- Burst loss is the longest consecutive failed probe streak inside a directed pair round.",
    ])

    output_path.parent.mkdir(parents=True, exist_ok=True)
    output_path.write_text("\n".join(lines) + "\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
