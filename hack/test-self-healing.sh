#!/usr/bin/env bash
set -euo pipefail

CLUSTER_NAME="${CLUSTER_NAME:-pod-pod-selfheal}"
NAMESPACE="${NAMESPACE:-netprobe-system}"
NHC_NAME="${NHC_NAME:-cluster-mesh}"
TARGET_NODE="${TARGET_NODE:-pod-pod-selfheal-worker2}"
TIMEOUT_SECONDS="${TIMEOUT_SECONDS:-120}"

log() {
  printf '[%s] %s\n' "$(date -Iseconds)" "$*"
}

fail() {
  log "FAIL: $*"
  exit 1
}

cleanup() {
  log "Clearing F3 fault"
  bash hack/inject-fault.sh F3 --clear >/dev/null 2>&1 || true
}
trap cleanup EXIT

command -v kind >/dev/null 2>&1 || fail "kind is not installed"
command -v kubectl >/dev/null 2>&1 || fail "kubectl is not installed"
command -v docker >/dev/null 2>&1 || fail "docker is not installed"

kind get clusters | grep -Fxq "${CLUSTER_NAME}" || fail "KIND cluster ${CLUSTER_NAME} was not found"
kubectl get nodes >/dev/null || fail "kubectl cannot access the cluster"

log "Checking manager and agent readiness"
kubectl rollout status deployment/selfheal-manager -n "${NAMESPACE}" --timeout="${TIMEOUT_SECONDS}s"
kubectl rollout status daemonset/netprobe-agent -n "${NAMESPACE}" --timeout="${TIMEOUT_SECONDS}s"

kubectl apply -f config/samples/networkhealthcheck-sample.yaml >/dev/null

agent_pod_for_node() {
  kubectl get pods -n "${NAMESPACE}" -l app=netprobe-agent \
    -o custom-columns=':metadata.name,:spec.nodeName,:status.phase,:metadata.uid' --no-headers \
    | awk -v node="${TARGET_NODE}" '$2 == node && $3 == "Running" {print $1 " " $4; exit}'
}

wait_for_agent() {
  local deadline=$((SECONDS + TIMEOUT_SECONDS))
  local agent
  while (( SECONDS < deadline )); do
    agent="$(agent_pod_for_node || true)"
    if [[ -n "${agent}" ]]; then
      printf '%s\n' "${agent}"
      return 0
    fi
    sleep 2
  done
  return 1
}

agent="$(wait_for_agent)" || fail "no Running agent found on ${TARGET_NODE}"
old_pod="${agent%% *}"
old_uid="${agent##* }"
log "Target agent before fault: ${old_pod} UID=${old_uid}"

log "Clearing any stale F3 rule before the test"
bash hack/inject-fault.sh F3 --clear >/dev/null 2>&1 || true
log "Injecting F3 on ${TARGET_NODE}"
bash hack/inject-fault.sh F3

log "Waiting for NodeIsolated classification"
deadline=$((SECONDS + TIMEOUT_SECONDS))
while (( SECONDS < deadline )); do
  classification="$(kubectl get nhc "${NHC_NAME}" -o jsonpath='{.status.classification}' 2>/dev/null || true)"
  suspect="$(kubectl get nhc "${NHC_NAME}" -o jsonpath='{.status.suspectNodes[0]}' 2>/dev/null || true)"
  log "classification=${classification:-<empty>} suspect=${suspect:-<empty>}"
  if [[ "${classification}" == "NodeIsolated" && "${suspect}" == "${TARGET_NODE}" ]]; then
    break
  fi
  sleep 5
done
[[ "${classification:-}" == "NodeIsolated" ]] || fail "operator did not classify ${TARGET_NODE} as NodeIsolated"
[[ "${suspect:-}" == "${TARGET_NODE}" ]] || fail "unexpected suspect node: ${suspect:-<empty>}"

log "Waiting for the operator-initiated agent restart"
deadline=$((SECONDS + TIMEOUT_SECONDS))
restarted=0
while (( SECONDS < deadline )); do
  agent="$(agent_pod_for_node || true)"
  new_pod="${agent%% *}"
  new_uid="${agent##* }"
  if [[ -n "${new_uid}" && "${new_uid}" != "${old_uid}" ]]; then
    log "PASS: target agent restarted: ${new_pod} UID=${new_uid}"
    restarted=1
    break
  fi
  sleep 3
done
[[ "${restarted}" -eq 1 ]] || fail "target agent UID did not change"

log "Waiting for Healthy recovery"
deadline=$((SECONDS + TIMEOUT_SECONDS))
while (( SECONDS < deadline )); do
  classification="$(kubectl get nhc "${NHC_NAME}" -o jsonpath='{.status.classification}' 2>/dev/null || true)"
  log "classification=${classification:-<empty>}"
  if [[ "${classification}" == "Healthy" ]]; then
    log "PASS: operator recovered the mesh"
    kubectl get nhc "${NHC_NAME}" -o yaml
    exit 0
  fi
  sleep 5
done

kubectl get nhc "${NHC_NAME}" -o yaml || true
fail "mesh did not return to Healthy within ${TIMEOUT_SECONDS}s"
