#!/usr/bin/env bash
set -euo pipefail

CLUSTER_NAME="${CLUSTER_NAME:-pod-pod-selfheal}"
NAMESPACE="${NAMESPACE:-netprobe-system}"
NHC_NAME="${NHC_NAME:-cluster-mesh}"
TARGET_NODE="${TARGET_NODE:-pod-pod-selfheal-worker2}"
TIMEOUT_SECONDS="${TIMEOUT_SECONDS:-120}"
SCENARIOS="${SCENARIOS:-F1 F2 F3 F4 F5 F6}"

log() {
  printf '[%s] %s\n' "$(date -Iseconds)" "$*"
}

fail() {
  log "FAIL: $*"
  exit 1
}

cleanup() {
  for scenario in ${SCENARIOS}; do
    log "Clearing ${scenario} fault"
    bash hack/inject-fault.sh "${scenario}" --clear >/dev/null 2>&1 || true
  done
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

expected_class() {
  case "$1" in
    F1|F5) printf '%s\n' "Healthy" ;;
    F2) printf '%s\n' "NodeIngressFailure" ;;
    F3) printf '%s\n' "NodeEgressFailure" ;;
    F4) printf '%s\n' "${F4_EXPECTED_CLASS:-NodeIsolated}" ;;
    F6) printf '%s\n' "ClusterPartition" ;;
    *) return 1 ;;
  esac
}

expected_suspect() {
  case "$1" in
    F2|F3|F4) printf '%s\n' "${TARGET_NODE}" ;;
    *) printf '%s\n' "" ;;
  esac
}

needs_restart() {
  case "$1" in
    NodeIngressFailure|NodeEgressFailure|NodeIsolated) return 0 ;;
    *) return 1 ;;
  esac
}

wait_for_classification() {
  local wanted_class="$1"
  local wanted_suspect="${2:-}"
  local deadline=$((SECONDS + TIMEOUT_SECONDS))
  classification=""
  suspect=""
  while (( SECONDS < deadline )); do
    classification="$(kubectl get nhc "${NHC_NAME}" -o jsonpath='{.status.classification}' 2>/dev/null || true)"
    suspect="$(kubectl get nhc "${NHC_NAME}" -o jsonpath='{.status.suspectNodes[0]}' 2>/dev/null || true)"
    log "classification=${classification:-<empty>} suspect=${suspect:-<empty>} expected=${wanted_class}"
    if [[ "${classification}" == "${wanted_class}" &&
      ( -z "${wanted_suspect}" || "${suspect}" == "${wanted_suspect}" ) ]]; then
      return 0
    fi
    sleep 5
  done
  return 1
}

wait_for_healthy() {
  wait_for_classification "Healthy"
}

for scenario in ${SCENARIOS}; do
  expected="$(expected_class "${scenario}")" || fail "unsupported scenario: ${scenario}"
  expected_target="$(expected_suspect "${scenario}")"
  agent="$(wait_for_agent)" || fail "no Running agent found on ${TARGET_NODE}"
  old_uid="${agent##* }"

  log "Testing ${scenario}: expected classification=${expected}"
  bash hack/inject-fault.sh "${scenario}"
  wait_for_classification "${expected}" "${expected_target}" || {
    kubectl get nhc "${NHC_NAME}" -o yaml || true
    fail "operator did not classify ${scenario} as ${expected}"
  }

  if needs_restart "${expected}"; then
    log "Waiting for the operator-initiated agent restart"
    deadline=$((SECONDS + TIMEOUT_SECONDS))
    restarted=0
    while (( SECONDS < deadline )); do
      agent="$(agent_pod_for_node || true)"
      new_uid="${agent##* }"
      if [[ -n "${new_uid}" && "${new_uid}" != "${old_uid}" ]]; then
        log "PASS: ${scenario} target agent restarted"
        restarted=1
        break
      fi
      sleep 3
    done
    [[ "${restarted}" -eq 1 ]] || fail "${scenario} target agent UID did not change"
  else
    log "PASS: ${scenario} produced ${expected}; no restart is expected"
  fi

  bash hack/inject-fault.sh "${scenario}" --clear
  wait_for_healthy || {
    kubectl get nhc "${NHC_NAME}" -o yaml || true
    fail "mesh did not return to Healthy after ${scenario}"
  }
  log "PASS: ${scenario} recovered to Healthy"
done

kubectl get nhc "${NHC_NAME}" -o yaml
log "PASS: all requested scenarios completed"
