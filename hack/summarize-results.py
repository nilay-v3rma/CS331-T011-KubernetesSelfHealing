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

    for row in completed:
        by_scenario[row["scenario"]].append(row)
        confusion[(row.get("true_class", "Unknown"), row.get("predicted_class", "Unknown"))] += 1

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
        correct = sum(1 for row in group if row.get("predicted_class") == row.get("true_class"))
        accuracy = correct / len(group) if group else 0
        lines.append(
            "| {scenario} | {trials} | {accuracy:.2f} | {mttd:.1f} | {rtt:.3f} | {jitter:.3f} | {loss:.3f} | {burst:.0f} | {throughput:.2f} | {bytes_sent:.1f} | {probes:.1f} |".format(
                scenario=scenario,
                trials=len(group),
                accuracy=accuracy,
                mttd=mean(number(row.get("mttd_ms")) for row in group),
                rtt=mean(number(row.get("avg_rtt_ms")) for row in group),
                jitter=mean(number(row.get("avg_jitter_ms")) for row in group),
                loss=mean(number(row.get("avg_loss_rate")) for row in group),
                burst=max((number(row.get("max_burst_loss")) or 0 for row in group), default=0),
                throughput=mean(number(row.get("avg_throughput_bps")) for row in group),
                bytes_sent=mean(number(row.get("total_bytes_sent")) for row in group),
                probes=mean(number(row.get("probe_count")) for row in group),
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
