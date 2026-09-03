#!/bin/bash
set -euo pipefail

TRIALS="${TRIALS:-10}"
SCENARIOS="${SCENARIOS:-F2 F3 F4 F5 F6}"

echo "Running P5 evaluation: scenarios=(${SCENARIOS}) trials=${TRIALS}"
echo "Results will be appended under docs/results/"

for scenario in ${SCENARIOS}; do
  ./hack/measure-mttd.sh "$scenario" "$TRIALS"
done

python3 ./hack/summarize-results.py docs/results/mttd-results.csv docs/results/p5-summary.md
echo "Wrote docs/results/p5-summary.md"
