#!/bin/bash
set -euo pipefail

SCENARIO="${1:-}"
TRIALS="${2:-1}"

if [[ -z "$SCENARIO" ]]; then
  echo "Usage: $0 <F2|F3|F4|F5|F6> [trials]"
  exit 1
fi

RESULTS_DIR="docs/results"
mkdir -p "$RESULTS_DIR"
RESULTS_FILE="$RESULTS_DIR/mttd-results.csv"

if [[ ! -f "$RESULTS_FILE" ]]; then
  echo "scenario,trial,inject_ts,detect_ts,mttd_ms,predicted_class,true_class,confidence" > "$RESULTS_FILE"
fi

TRUE_CLASS="Unknown"
case "$SCENARIO" in
  F2) TRUE_CLASS="NodeIngress" ;;
  F3) TRUE_CLASS="NodeEgress" ;;
  F4) TRUE_CLASS="ControlPlaneDown" ;;
  F5) TRUE_CLASS="Degraded" ;;
  F6) TRUE_CLASS="ClusterPartition" ;;
esac

for (( t=1; t<=TRIALS; t++ )); do
  echo "--- Trial $t for $SCENARIO ---"
  
  # Ensure healthy first
  ./hack/inject-fault.sh F1 --clear >/dev/null
  sleep 5
  
  INJECT_TS=$(date +%s%3N)
  echo "Injecting fault at $INJECT_TS"
  ./hack/inject-fault.sh "$SCENARIO" >/dev/null
  
  DETECT_TS=""
  PRED_CLASS="Unknown"
  CONFIDENCE="0.0"
  
  # Poll for detection
  for i in {1..120}; do
    STATUS_JSON=$(kubectl get nhc cluster-mesh -o json 2>/dev/null || echo "{}")
    
    # Simple check with jq (assuming jq is available)
    if [[ -n "$STATUS_JSON" && "$STATUS_JSON" != "{}" ]]; then
      # Need python or jq to parse lastTransitionTime and match injection time. 
      # Since we don't know if jq is installed, we'll try Python.
      RESULT=$(python3 -c "
import sys, json, dateutil.parser
try:
    data = json.loads(sys.stdin.read())
    conditions = data.get('status', {}).get('conditions', [])
    for c in conditions:
        if c.get('type') == 'MeshHealthy' and c.get('status') == 'False':
            ts = c.get('lastTransitionTime')
            if ts:
                from datetime import datetime
                import dateutil.parser
                dt = dateutil.parser.isoparse(ts)
                ms = int(dt.timestamp() * 1000)
                inject_ms = int('$INJECT_TS')
                if ms >= inject_ms:
                    cls = data.get('status', {}).get('classification', 'Unknown')
                    conf = data.get('status', {}).get('classificationConfidence', '0.0')
                    print(f'{ms},{cls},{conf}')
                    sys.exit(0)
except Exception as e:
    pass
print('None')
" <<< "$STATUS_JSON")

      if [[ "$RESULT" != "None" ]]; then
        DETECT_TS=$(echo "$RESULT" | cut -d',' -f1)
        PRED_CLASS=$(echo "$RESULT" | cut -d',' -f2)
        CONFIDENCE=$(echo "$RESULT" | cut -d',' -f3)
        break
      fi
    fi
    sleep 1
  done
  
  if [[ -n "$DETECT_TS" ]]; then
    MTTD=$(( DETECT_TS - INJECT_TS ))
    echo "Detected in ${MTTD}ms. Class: $PRED_CLASS (Confidence: $CONFIDENCE)"
    echo "$SCENARIO,$t,$INJECT_TS,$DETECT_TS,$MTTD,$PRED_CLASS,$TRUE_CLASS,$CONFIDENCE" >> "$RESULTS_FILE"
  else
    echo "Failed to detect within 120s"
    echo "$SCENARIO,$t,$INJECT_TS,,TIMEOUT,,$TRUE_CLASS," >> "$RESULTS_FILE"
  fi
  
  echo "Clearing fault"
  ./hack/inject-fault.sh "$SCENARIO" --clear >/dev/null
  sleep 10
done
