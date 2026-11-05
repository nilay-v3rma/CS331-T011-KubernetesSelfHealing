#!/bin/bash
set -euo pipefail

TRIALS="${TRIALS:-10}"
SCENARIOS="${SCENARIOS:-F1 F2 F3 F4 F5 F6}"
RESET="${RESET:-0}"

if [[ "$RESET" == "1" ]]; then
  echo "Resetting docs/results/mttd-results.csv for fresh run..."
  rm -f docs/results/mttd-results.csv
fi

echo "Running P5 evaluation: scenarios=(${SCENARIOS}) trials=${TRIALS}"
echo "Pre-clearing all scenarios to ensure clean cluster baseline..."
for s in F1 F2 F3 F4 F5 F6; do
  ./hack/inject-fault.sh "$s" --clear >/dev/null 2>&1 || true
done
echo "Results will be appended under docs/results/"

for scenario in ${SCENARIOS}; do
  ./hack/measure-mttd.sh "$scenario" "$TRIALS"
done

python3 ./hack/summarize-results.py docs/results/mttd-results.csv docs/results/p5-summary.md
echo "Wrote docs/results/p5-summary.md"
