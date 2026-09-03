#!/bin/bash
set -euo pipefail

SCENARIO="${1:-}"
TRIALS="${2:-1}"
RESULTS_DIR="${RESULTS_DIR:-docs/results}"
NHC_NAME="${NHC_NAME:-cluster-mesh}"
POLL_TIMEOUT_SECONDS="${POLL_TIMEOUT_SECONDS:-120}"

if [[ -z "$SCENARIO" ]]; then
  echo "Usage: $0 <F2|F3|F4|F5|F6> [trials]"
  exit 1
fi

mkdir -p "$RESULTS_DIR"
RESULTS_FILE="$RESULTS_DIR/mttd-results.csv"

if [[ ! -f "$RESULTS_FILE" ]]; then
  echo "scenario,trial,inject_ts,detect_ts,mttd_ms,predicted_class,true_class,confidence,avg_rtt_ms,avg_jitter_ms,avg_loss_rate,max_burst_loss,avg_throughput_bps,total_bytes_sent,probe_count" > "$RESULTS_FILE"
fi

TRUE_CLASS="Unknown"
case "$SCENARIO" in
  F2) TRUE_CLASS="NodeIngressFailure" ;;
  F3) TRUE_CLASS="NodeEgressFailure" ;;
  F4) TRUE_CLASS="NodeIsolated" ;;
  F5) TRUE_CLASS="Degraded" ;;
  F6) TRUE_CLASS="ClusterPartition" ;;
esac

extract_detection() {
  local inject_ts="$1"
  local status_file="$2"
  python3 - "$inject_ts" "$status_file" <<'PY'
import json
import sys
from datetime import datetime

inject_ms = int(sys.argv[1])
status_file = sys.argv[2]
try:
    with open(status_file) as handle:
        data = json.load(handle)
except Exception:
    print("None")
    raise SystemExit

status = data.get("status", {})
classification = status.get("classification", "Unknown")
confidence = status.get("classificationConfidence", 0.0)
detected_ms = None

for condition in status.get("conditions", []):
    if condition.get("type") != "Healthy" or condition.get("status") != "False":
        continue
    raw = condition.get("lastTransitionTime")
    if not raw:
        continue
    dt = datetime.fromisoformat(raw.replace("Z", "+00:00"))
    candidate_ms = int(dt.timestamp() * 1000)
    if candidate_ms >= inject_ms:
        detected_ms = candidate_ms
        break

if detected_ms is None:
    print("None")
    raise SystemExit

reachability = status.get("reachability", [])
def avg(name):
    values = [float(item.get(name, 0) or 0) for item in reachability]
    return sum(values) / len(values) if values else 0.0

avg_rtt = avg("rttMillis")
avg_jitter = avg("jitterMillis")
avg_loss = avg("lossRate")
avg_throughput = avg("throughputBps")
max_burst = max([int(item.get("burstLossMax", 0) or 0) for item in reachability], default=0)
total_bytes = sum([int(item.get("bytesSent", 0) or 0) for item in reachability])
probe_count = sum([int(item.get("probeCount", 0) or 0) for item in reachability])

print(f"{detected_ms},{classification},{confidence},{avg_rtt:.6f},{avg_jitter:.6f},{avg_loss:.6f},{max_burst},{avg_throughput:.2f},{total_bytes},{probe_count}")
PY
}

now_ms() {
  python3 - <<'PY'
import time
print(int(time.time() * 1000))
PY
}

for (( trial=1; trial<=TRIALS; trial++ )); do
  echo "--- Trial ${trial} for ${SCENARIO} ---"

  ./hack/inject-fault.sh F1 --clear >/dev/null
  ./hack/inject-fault.sh "$SCENARIO" --clear >/dev/null
  sleep 5

  INJECT_TS="$(now_ms)"
  echo "Injecting fault at ${INJECT_TS}"
  ./hack/inject-fault.sh "$SCENARIO" >/dev/null

  DETECTION=""
  for (( second=1; second<=POLL_TIMEOUT_SECONDS; second++ )); do
    STATUS_JSON="$(kubectl get nhc "$NHC_NAME" -o json 2>/dev/null || echo '{}')"
    STATUS_FILE="$(mktemp)"
    printf '%s' "$STATUS_JSON" > "$STATUS_FILE"
    DETECTION="$(extract_detection "$INJECT_TS" "$STATUS_FILE")"
    rm -f "$STATUS_FILE"
    if [[ "$DETECTION" != "None" ]]; then
      break
    fi
    sleep 1
  done

  if [[ "$DETECTION" != "None" ]]; then
    IFS=',' read -r DETECT_TS PRED_CLASS CONFIDENCE AVG_RTT AVG_JITTER AVG_LOSS MAX_BURST AVG_THROUGHPUT TOTAL_BYTES PROBE_COUNT <<< "$DETECTION"
    MTTD=$(( DETECT_TS - INJECT_TS ))
    echo "Detected in ${MTTD}ms. Class: ${PRED_CLASS} (confidence ${CONFIDENCE})"
    echo "${SCENARIO},${trial},${INJECT_TS},${DETECT_TS},${MTTD},${PRED_CLASS},${TRUE_CLASS},${CONFIDENCE},${AVG_RTT},${AVG_JITTER},${AVG_LOSS},${MAX_BURST},${AVG_THROUGHPUT},${TOTAL_BYTES},${PROBE_COUNT}" >> "$RESULTS_FILE"
  else
    echo "Failed to detect within ${POLL_TIMEOUT_SECONDS}s"
    echo "${SCENARIO},${trial},${INJECT_TS},,TIMEOUT,,${TRUE_CLASS},,,,,,,," >> "$RESULTS_FILE"
  fi

  echo "Clearing fault"
  ./hack/inject-fault.sh "$SCENARIO" --clear >/dev/null
  sleep 10
done
