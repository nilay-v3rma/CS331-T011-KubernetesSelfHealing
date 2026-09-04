#!/bin/bash
set -euo pipefail

SCENARIO="${1:-}"
CLEAR_MODE=0
if [[ "${2:-}" == "--clear" ]]; then
  CLEAR_MODE=1
fi

TARGET_NODE="${TARGET_NODE:-pod-pod-selfheal-worker2}"
TS=$(date -Iseconds)

if [[ -z "$SCENARIO" ]]; then
  echo "Usage: $0 <F1|F2|F3|F4|F5|F6> [--clear]"
  exit 1
fi

GREEN='\033[0;32m'
RED='\033[0;31m'
NC='\033[0m'

running_agent_field() {
  local field="$1"
  local values
  values=$(kubectl get pods -n netprobe-system -l app=netprobe-agent \
    --field-selector "spec.nodeName=${TARGET_NODE},status.phase=Running" \
    -o jsonpath="{range .items[*]}{.${field}}{\"\n\"}{end}" 2>/dev/null || true)
  printf '%s\n' "${values}" | sed -n '1p'
}

running_agent_pod() {
  running_agent_field "metadata.name"
}

running_agent_ip() {
  running_agent_field "status.podIP"
}

echo -e "[$TS] ${GREEN}Target Node:${NC} $TARGET_NODE"
echo -e "[$TS] ${GREEN}Scenario:${NC} $SCENARIO"

case "$SCENARIO" in
  F1)
    if [ $CLEAR_MODE -eq 1 ]; then
      echo -e "[$TS] Clearing F1: Baseline - nothing to do."
    else
      echo -e "[$TS] Applying F1: Baseline - nothing to do."
    fi
    ;;
  F2)
    if [ $CLEAR_MODE -eq 1 ]; then
      echo -e "[$TS] Clearing F2: Node inbound block"
      docker exec "${TARGET_NODE}" iptables -D INPUT -p tcp --dport 9376 -j DROP 2>/dev/null || true
      AGENT_POD=$(running_agent_pod)
      if [[ -n "${AGENT_POD}" ]]; then
        while kubectl exec -n netprobe-system "${AGENT_POD}" -- \
          iptables -D INPUT -p tcp --dport 9376 -j DROP 2>/dev/null; do :; done
      fi
    else
      echo -e "[$TS] Applying F2: Node inbound block"
      AGENT_POD=$(running_agent_pod)
      if [[ -z "${AGENT_POD}" ]]; then
        echo -e "${RED}Could not determine a Running agent pod on ${TARGET_NODE}${NC}"
        exit 1
      fi
      echo -e "[$TS] Applying F2: Node inbound block inside agent pod ${AGENT_POD}"
      kubectl exec -n netprobe-system "${AGENT_POD}" -- \
        iptables -I INPUT 1 -p tcp --dport 9376 -j DROP
    fi
    ;;
  F3)
    if [ $CLEAR_MODE -eq 1 ]; then
      echo -e "[$TS] Clearing F3: Node outbound block"
      AGENT_POD=$(running_agent_pod)
      if [[ -n "${AGENT_POD}" ]]; then
        while kubectl exec -n netprobe-system "${AGENT_POD}" -- \
          iptables -D OUTPUT -p tcp --dport 9376 -j DROP 2>/dev/null; do :; done
      fi
    else
      AGENT_POD_IP=$(running_agent_ip)
      AGENT_POD=$(running_agent_pod)
      if [[ -z "${AGENT_POD_IP}" || -z "${AGENT_POD}" ]]; then
        echo -e "${RED}Could not determine a Running agent pod on ${TARGET_NODE}${NC}"
        exit 1
      fi
      echo -e "[$TS] Applying F3: Node outbound block inside agent pod ${AGENT_POD} (${AGENT_POD_IP})"
      kubectl exec -n netprobe-system "${AGENT_POD}" -- \
        iptables -I OUTPUT 1 -p tcp --dport 9376 -j DROP
    fi
    ;;
  F4)
    if [ $CLEAR_MODE -eq 1 ]; then
      echo -e "[$TS] Clearing F4: CNI agent kill (waiting for DaemonSet)"
      AGENT_POD=$(running_agent_pod)
      if [[ -n "${AGENT_POD}" ]]; then
        while kubectl exec -n netprobe-system "${AGENT_POD}" -- \
          iptables -D INPUT -p tcp --dport 9376 -j DROP 2>/dev/null; do :; done
        while kubectl exec -n netprobe-system "${AGENT_POD}" -- \
          iptables -D OUTPUT -p tcp --dport 9376 -j DROP 2>/dev/null; do :; done
      fi
      sleep 5
      kubectl wait --for=condition=Ready pod -n kube-system -l k8s-app=calico-node --field-selector spec.nodeName=${TARGET_NODE} --timeout=60s || true
    else
      echo -e "[$TS] Applying F4: CNI agent kill"
      kubectl delete pod -n kube-system -l k8s-app=calico-node --field-selector spec.nodeName=${TARGET_NODE} --grace-period=0 --force || true
      AGENT_POD=$(running_agent_pod)
      if [[ -z "${AGENT_POD}" ]]; then
        echo -e "${RED}Could not determine a Running agent pod on ${TARGET_NODE}${NC}"
        exit 1
      fi
      echo -e "[$TS] Applying F4: isolating agent pod ${AGENT_POD} on data port 9376"
      kubectl exec -n netprobe-system "${AGENT_POD}" -- \
        iptables -I INPUT 1 -p tcp --dport 9376 -j DROP
      kubectl exec -n netprobe-system "${AGENT_POD}" -- \
        iptables -I OUTPUT 1 -p tcp --dport 9376 -j DROP
    fi
    ;;
  F5)
    if [ $CLEAR_MODE -eq 1 ]; then
      echo -e "[$TS] Clearing F5: Partial packet loss (40%)"
      docker exec "${TARGET_NODE}" tc qdisc del dev eth0 root || true
    else
      echo -e "[$TS] Applying F5: Partial packet loss (40%)"
      docker exec "${TARGET_NODE}" tc qdisc add dev eth0 root netem loss 40% || true
    fi
    ;;
  F6)
    if [ $CLEAR_MODE -eq 1 ]; then
      echo -e "[$TS] Clearing F6: Deny-all NetworkPolicy"
      kubectl delete -f hack/deny-all.yaml || true
    else
      echo -e "[$TS] Applying F6: Deny-all NetworkPolicy"
      kubectl apply -f hack/deny-all.yaml
    fi
    ;;
  *)
    echo -e "${RED}Unknown scenario: $SCENARIO${NC}"
    exit 1
    ;;
esac

echo -e "[$TS] Done."
