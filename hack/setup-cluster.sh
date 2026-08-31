#!/usr/bin/env bash
# Idempotent script to create the KIND cluster, install Calico, and verify connectivity.
# Usage: ./hack/setup-cluster.sh
set -euo pipefail

CLUSTER_NAME="pod-pod-selfheal"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CALICO_VERSION="v3.28.0"
CALICO_URL="https://raw.githubusercontent.com/projectcalico/calico/${CALICO_VERSION}/manifests/calico.yaml"

echo "=== Self-Heal Network Operator — Cluster Setup ==="
echo "Cluster name: ${CLUSTER_NAME}"
echo

# --- Step 1: Create or reuse KIND cluster ---
if kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
  echo "[INFO] KIND cluster '${CLUSTER_NAME}' already exists. Reusing it."
else
  echo "[STEP 1/5] Creating KIND cluster..."
  kind create cluster --name "${CLUSTER_NAME}" --config "${SCRIPT_DIR}/kind-cluster.yaml"
  echo "[OK] Cluster created."
fi

# --- Step 2: Install Calico CNI ---
echo
echo "[STEP 2/5] Installing Calico ${CALICO_VERSION}..."
kubectl apply -f "${CALICO_URL}"
echo "[OK] Calico manifests applied."

# --- Step 3: Wait for all nodes to be Ready ---
echo
echo "[STEP 3/5] Waiting for all nodes to be Ready (timeout: 300s)..."
kubectl wait --for=condition=Ready nodes --all --timeout=300s
echo "[OK] All nodes Ready."

# --- Step 4: Wait for Calico pods ---
echo
echo "[STEP 4/5] Waiting for Calico node pods..."
kubectl wait --for=condition=Ready pods -n kube-system -l k8s-app=calico-node --timeout=300s
echo "[OK] Calico pods Ready."
kubectl get pods -n kube-system -l k8s-app=calico-node

# --- Step 5: Verify cross-node connectivity ---
echo
echo "[STEP 5/5] Verifying cross-node pod connectivity..."

# Create netprobe-system namespace if it doesn't exist
kubectl create namespace netprobe-system --dry-run=client -o yaml | kubectl apply -f -

# Get worker node names
WORKER1=$(kubectl get nodes --no-headers -l '!node-role.kubernetes.io/control-plane' -o custom-columns=':metadata.name' | head -n1)
WORKER2=$(kubectl get nodes --no-headers -l '!node-role.kubernetes.io/control-plane' -o custom-columns=':metadata.name' | tail -n1)

echo "  Testing connectivity between ${WORKER1} and ${WORKER2}..."

# Deploy two busybox pods on different workers
kubectl run ping-test-1 --image=busybox:1.36 --restart=Never \
  --overrides='{"spec":{"nodeName":"'"${WORKER1}"'"}}' \
  --command -- sleep 30 2>/dev/null || true
kubectl run ping-test-2 --image=busybox:1.36 --restart=Never \
  --overrides='{"spec":{"nodeName":"'"${WORKER2}"'"}}' \
  --command -- sleep 30 2>/dev/null || true

# Wait for pods
kubectl wait --for=condition=Ready pod/ping-test-1 --timeout=60s
kubectl wait --for=condition=Ready pod/ping-test-2 --timeout=60s

# Get pod IP of ping-test-2
POD2_IP=$(kubectl get pod ping-test-2 -o jsonpath='{.status.podIP}')

# Ping from pod 1 to pod 2
if kubectl exec ping-test-1 -- ping -c 3 -W 2 "${POD2_IP}" > /dev/null 2>&1; then
  echo "  [OK] Cross-node ping SUCCEEDED (${WORKER1} -> ${WORKER2})."
else
  echo "  [FAIL] Cross-node ping FAILED. Check Calico installation."
fi

# Cleanup test pods
kubectl delete pod ping-test-1 ping-test-2 --ignore-not-found --grace-period=0 --force 2>/dev/null || true

echo
echo "=== Cluster setup complete ==="
echo "Nodes:"
kubectl get nodes -o wide
