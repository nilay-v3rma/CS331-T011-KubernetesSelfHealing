#!/usr/bin/env bash
# Tears down the KIND cluster.
# Usage: ./hack/reset-cluster.sh
set -euo pipefail

CLUSTER_NAME="pod-pod-selfheal"

echo "=== Tearing down KIND cluster '${CLUSTER_NAME}' ==="

if kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
  kind delete cluster --name "${CLUSTER_NAME}"
  echo "[OK] Cluster '${CLUSTER_NAME}' deleted."
else
  echo "[INFO] Cluster '${CLUSTER_NAME}' does not exist. Nothing to do."
fi

# Clean up any leftover Docker networks
echo "Cleaning up leftover Docker networks..."
docker network prune -f 2>/dev/null || true

echo "=== Teardown complete ==="
