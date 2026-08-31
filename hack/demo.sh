#!/usr/bin/env bash
# ============================================================================
# Self-Heal Network Operator — 5-Minute Reproducible Demo
# ============================================================================
# Demonstrates: healthy mesh → fault injection → detection → recovery
#
# Prerequisites:
#   - KIND cluster running (run: make cluster-up)
#   - Operator and agents deployed (run: make all)
#   - Sample CR applied (run: make deploy-sample)
#
# Usage: ./hack/demo.sh
# ============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLUSTER_NAME="pod-pod-selfheal"

# Colors
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

banner() {
  echo
  echo -e "${CYAN}${BOLD}════════════════════════════════════════════════════════${NC}"
  echo -e "${CYAN}${BOLD}  $1${NC}"
  echo -e "${CYAN}${BOLD}════════════════════════════════════════════════════════${NC}"
  echo
}

pause() {
  echo -e "${YELLOW}  ▶ Press Enter to continue...${NC}"
  read -r
}

# ============================================================================
banner "SELF-HEAL NETWORK OPERATOR — LIVE DEMO"

echo -e "${BOLD}This demo walks through:${NC}"
echo "  1. Healthy mesh baseline"
echo "  2. Injecting a node-egress fault (F3)"
echo "  3. Watching the operator detect and classify the failure"
echo "  4. Clearing the fault"
echo "  5. Watching the mesh recover"
echo
echo -e "${BOLD}Cluster:${NC} ${CLUSTER_NAME}"
echo -e "${BOLD}Time:${NC}    $(date --iso-8601=seconds)"
echo

# --- Step 1: Show healthy baseline ---
banner "Step 1/5: Healthy Baseline"
echo -e "${GREEN}Checking cluster status...${NC}"
echo
kubectl get nodes -o wide
echo
kubectl get pods -n netprobe-system -o wide
echo
echo -e "${GREEN}NetworkHealthCheck status:${NC}"
kubectl get nhc cluster-mesh -o yaml 2>/dev/null || echo "(CR not yet applied or operator not running)"
echo
echo -e "${GREEN}✓ All systems nominal.${NC}"
pause

# --- Step 2: Inject fault F3 (node egress block) ---
banner "Step 2/5: Injecting Fault — Node Egress Block (F3)"
echo -e "${RED}Blocking all outbound TCP :9376 traffic from pod-pod-selfheal-worker2...${NC}"
echo
"${SCRIPT_DIR}/inject-fault.sh" F3
echo
echo -e "${RED}✗ Fault injected. The operator should detect this within ~45 seconds.${NC}"
echo -e "${YELLOW}  (interval=15s × failureThreshold=3 = 45s worst case)${NC}"
pause

# --- Step 3: Watch the operator detect and classify ---
banner "Step 3/5: Watching Detection"
echo -e "${CYAN}Polling NetworkHealthCheck status every 5 seconds (Ctrl+C to skip)...${NC}"
echo

DETECTED=false
for i in $(seq 1 24); do
  VERDICT=$(kubectl get nhc cluster-mesh -o jsonpath='{.status.verdict}' 2>/dev/null || echo "")
  CLASS=$(kubectl get nhc cluster-mesh -o jsonpath='{.status.classification}' 2>/dev/null || echo "")
  LAST=$(kubectl get nhc cluster-mesh -o jsonpath='{.status.lastProbeTime}' 2>/dev/null || echo "")

  if [ -n "${VERDICT}" ] && [ "${VERDICT}" != "Healthy" ]; then
    echo -e "  ${RED}[$(date +%T)] Verdict: ${VERDICT}  |  Classification: ${CLASS}  |  Last probe: ${LAST}${NC}"
    DETECTED=true
    break
  else
    echo -e "  ${GREEN}[$(date +%T)] Verdict: ${VERDICT:-pending}  |  Classification: ${CLASS:-pending}${NC}"
  fi
  sleep 5
done

echo
if [ "${DETECTED}" = true ]; then
  echo -e "${RED}${BOLD}✗ FAULT DETECTED AND CLASSIFIED!${NC}"
  echo
  echo -e "${BOLD}Full status:${NC}"
  kubectl get nhc cluster-mesh -o yaml 2>/dev/null | head -40
  echo "  ..."
else
  echo -e "${YELLOW}Detection still in progress (may need more time or the operator may not be running yet).${NC}"
fi
pause

# --- Step 4: Clear the fault ---
banner "Step 4/5: Clearing Fault"
echo -e "${GREEN}Removing the iptables rule from pod-pod-selfheal-worker2...${NC}"
echo
"${SCRIPT_DIR}/inject-fault.sh" F3 --clear
echo
echo -e "${GREEN}✓ Fault cleared.${NC}"
pause

# --- Step 5: Watch recovery ---
banner "Step 5/5: Watching Recovery"
echo -e "${CYAN}Polling for mesh recovery (up to 60 seconds)...${NC}"
echo

RECOVERED=false
for i in $(seq 1 12); do
  VERDICT=$(kubectl get nhc cluster-mesh -o jsonpath='{.status.verdict}' 2>/dev/null || echo "")

  if [ "${VERDICT}" = "Healthy" ]; then
    echo -e "  ${GREEN}[$(date +%T)] Verdict: ${VERDICT}  ✓ RECOVERED${NC}"
    RECOVERED=true
    break
  else
    echo -e "  ${YELLOW}[$(date +%T)] Verdict: ${VERDICT:-pending}  (recovering...)${NC}"
  fi
  sleep 5
done

echo
if [ "${RECOVERED}" = true ]; then
  echo -e "${GREEN}${BOLD}✓ MESH FULLY RECOVERED!${NC}"
else
  echo -e "${YELLOW}Recovery in progress (may need more time for successThreshold).${NC}"
fi

# --- Done ---
banner "DEMO COMPLETE"
echo -e "${BOLD}Summary:${NC}"
echo "  1. Started with a healthy 3-worker mesh"
echo "  2. Blocked egress traffic from worker2 (iptables)"
echo "  3. Operator detected and classified as NodeEgressFailure"
echo "  4. Cleared the fault"
echo "  5. Operator detected recovery and returned to Healthy"
echo
echo -e "${BOLD}Key metrics to explore:${NC}"
echo "  kubectl get nhc cluster-mesh -o yaml"
echo "  kubectl get events -n netprobe-system --sort-by=.lastTimestamp"
echo "  curl localhost:8080/metrics  (port-forward the operator)"
echo
echo -e "${CYAN}Thank you for watching!${NC}"
