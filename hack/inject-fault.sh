#!/bin/bash
set -euo pipefail

SCENARIO="${1:-}"
CLEAR_MODE=0
if [[ "${2:-}" == "--clear" ]]; then
  CLEAR_MODE=1
fi

TARGET_NODE="${TARGET_NODE:-pod-pod-selfheal-worker2}"
TS=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

if [[ -z "$SCENARIO" ]]; then
  echo "Usage: $0 <F1|F2|F3|F4|F5|F6> [--clear]"
  exit 1
fi

GREEN='\033[0;32m'
RED='\033[0;31m'
NC='\033[0m'

check_node_tool() {
  local tool="$1"
  if ! docker exec "${TARGET_NODE}" which "${tool}" >/dev/null 2>&1; then
    echo -e "${RED}[ERROR] Required tool '${tool}' not found inside container ${TARGET_NODE}${NC}" >&2
    exit 1
  fi
}

flush_node_port_rules() {
  local port="${1:-9376}"
  for chain in FORWARD INPUT OUTPUT; do
    while true; do
      local num
      num=$(docker exec "${TARGET_NODE}" iptables -L "$chain" --line-numbers -n 2>/dev/null | { grep "$port" || true; } | head -n1 | awk '{print $1}')
      if [[ -z "$num" ]]; then break; fi
      docker exec "${TARGET_NODE}" iptables -D "$chain" "$num" 2>/dev/null || break
    done
  done
}

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
    AGENT_IP=$(running_agent_ip)
    if [ $CLEAR_MODE -eq 1 ]; then
      echo -e "[$TS] Clearing F2: Node inbound block on ${TARGET_NODE}"
      flush_node_port_rules 9376
    else
      check_node_tool iptables
      echo -e "[$TS] Applying F2: Node inbound block for ${AGENT_IP}:9376 on ${TARGET_NODE}"
      if [[ -n "${AGENT_IP}" ]]; then
        docker exec "${TARGET_NODE}" iptables -I FORWARD 1 -d "${AGENT_IP}" -p tcp --dport 9376 -j DROP
      fi
      docker exec "${TARGET_NODE}" iptables -I INPUT 1 -p tcp --dport 9376 -j DROP
    fi
    ;;
  F3)
    AGENT_IP=$(running_agent_ip)
    if [ $CLEAR_MODE -eq 1 ]; then
      echo -e "[$TS] Clearing F3: Node outbound block on ${TARGET_NODE}"
      flush_node_port_rules 9376
    else
      check_node_tool iptables
      echo -e "[$TS] Applying F3: Node outbound block for ${AGENT_IP}:9376 on ${TARGET_NODE}"
      if [[ -n "${AGENT_IP}" ]]; then
        docker exec "${TARGET_NODE}" iptables -I FORWARD 1 -s "${AGENT_IP}" -p tcp --dport 9376 -j DROP
      fi
      docker exec "${TARGET_NODE}" iptables -I OUTPUT 1 -p tcp --dport 9376 -j DROP
    fi
    ;;
  F4)
    AGENT_IP=$(running_agent_ip)
    if [ $CLEAR_MODE -eq 1 ]; then
      echo -e "[$TS] Clearing F4: CNI agent kill and node isolation on ${TARGET_NODE}"
      flush_node_port_rules 9376
      kubectl wait --for=condition=Ready pod -n kube-system -l k8s-app=calico-node \
        --field-selector "spec.nodeName=${TARGET_NODE}" --timeout=120s || true
    else
      check_node_tool iptables
      echo -e "[$TS] Applying F4: CNI agent kill and node isolation on ${TARGET_NODE}"
      if [[ -n "${AGENT_IP}" ]]; then
        docker exec "${TARGET_NODE}" iptables -I FORWARD 1 -d "${AGENT_IP}" -p tcp --dport 9376 -j DROP
        docker exec "${TARGET_NODE}" iptables -I FORWARD 1 -s "${AGENT_IP}" -p tcp --dport 9376 -j DROP
      else
        docker exec "${TARGET_NODE}" iptables -I FORWARD 1 -p tcp --dport 9376 -j DROP
      fi
      docker exec "${TARGET_NODE}" iptables -I INPUT 1 -p tcp --dport 9376 -j DROP
      docker exec "${TARGET_NODE}" iptables -I OUTPUT 1 -p tcp --dport 9376 -j DROP
      kubectl delete pod -n kube-system -l k8s-app=calico-node \
        --field-selector "spec.nodeName=${TARGET_NODE}" --grace-period=0 --force || true
    fi
    ;;
  F5)
    # Detect the default-route interface dynamically; fall back to eth0
    IFACE=$(docker exec "${TARGET_NODE}" ip route show default 2>/dev/null | awk '{print $5; exit}')
    if [[ -z "${IFACE}" ]]; then
      echo -e "${RED}[WARN] Could not detect default interface on ${TARGET_NODE}; falling back to eth0${NC}"
      IFACE="eth0"
    fi
    if [ $CLEAR_MODE -eq 1 ]; then
      echo -e "[$TS] Clearing F5: Partial packet loss (40%) on ${TARGET_NODE} iface ${IFACE}"
      while docker exec "${TARGET_NODE}" tc qdisc del dev "${IFACE}" root 2>/dev/null; do :; done
    else
      check_node_tool tc
      echo -e "[$TS] Applying F5: Partial packet loss (40%) on ${TARGET_NODE} iface ${IFACE}"
      docker exec "${TARGET_NODE}" tc qdisc del dev "${IFACE}" root 2>/dev/null || true
      docker exec "${TARGET_NODE}" tc qdisc add dev "${IFACE}" root netem loss 40%
    fi
    ;;
  F6)
    if [ $CLEAR_MODE -eq 1 ]; then
      echo -e "[$TS] Clearing F6: Deny-all NetworkPolicy"
      kubectl delete -f hack/deny-all.yaml 2>/dev/null || true
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
