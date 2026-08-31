# ============================================================================
# Self-Heal Network Operator — Makefile
# ============================================================================
# Central build and deploy orchestration for the pod-to-pod connectivity
# operator. All images are built locally and loaded into KIND (never pushed
# to a remote registry during development).
# ============================================================================

# --- Configuration ---
CLUSTER_NAME    ?= pod-pod-selfheal
MANAGER_IMG     ?= selfheal-manager:latest
AGENT_IMG       ?= selfheal-agent:latest
NAMESPACE       ?= netprobe-system
SCENARIO        ?= F1

# --- Go ---
GOFLAGS         ?=
GO              := go

# ============================================================================
# Default target
# ============================================================================
.PHONY: all
all: images load deploy  ## Build images, load into KIND, and deploy

# ============================================================================
# Go targets
# ============================================================================
.PHONY: test
test:  ## Run go vet + go test
	$(GO) vet ./...
	$(GO) test ./... -v

.PHONY: build-manager
build-manager:  ## Build manager binary locally
	$(GO) build $(GOFLAGS) -o bin/manager ./cmd/manager/

.PHONY: build-agent
build-agent:  ## Build agent binary locally
	$(GO) build $(GOFLAGS) -o bin/agent ./cmd/agent/

.PHONY: build
build: build-manager build-agent  ## Build both binaries locally

# ============================================================================
# Docker targets
# ============================================================================
.PHONY: docker-build-manager
docker-build-manager:  ## Build manager Docker image
	docker build -t $(MANAGER_IMG) -f Dockerfile .

.PHONY: docker-build-agent
docker-build-agent:  ## Build agent Docker image
	docker build -t $(AGENT_IMG) -f Dockerfile.agent .

.PHONY: images
images: docker-build-manager docker-build-agent  ## Build both Docker images

# ============================================================================
# KIND targets
# ============================================================================
.PHONY: load
load:  ## Load Docker images into KIND cluster
	kind load docker-image $(MANAGER_IMG) --name $(CLUSTER_NAME)
	kind load docker-image $(AGENT_IMG) --name $(CLUSTER_NAME)

.PHONY: cluster-up
cluster-up:  ## Create KIND cluster with Calico
	./hack/setup-cluster.sh

.PHONY: cluster-down
cluster-down:  ## Tear down KIND cluster
	./hack/reset-cluster.sh

# ============================================================================
# Deploy targets
# ============================================================================
.PHONY: deploy
deploy:  ## Deploy all resources via kustomize
	kubectl apply -k config/

.PHONY: undeploy
undeploy:  ## Remove all deployed resources
	kubectl delete -k config/ --ignore-not-found

.PHONY: deploy-sample
deploy-sample:  ## Apply sample NetworkHealthCheck CR
	kubectl apply -f config/samples/networkhealthcheck-sample.yaml

.PHONY: deploy-monitoring
deploy-monitoring:  ## Deploy Prometheus + Grafana monitoring stack
	kubectl apply -f config/monitoring/prometheus-stack.yaml
	kubectl apply -f config/monitoring/servicemonitor.yaml

# ============================================================================
# Fault injection targets
# ============================================================================
.PHONY: inject-fault
inject-fault:  ## Inject a fault scenario (SCENARIO=F1..F6)
	./hack/inject-fault.sh $(SCENARIO)

.PHONY: clear-fault
clear-fault:  ## Clear a fault scenario (SCENARIO=F1..F6)
	./hack/inject-fault.sh $(SCENARIO) --clear

.PHONY: measure-mttd
measure-mttd:  ## Measure MTTD for a scenario (SCENARIO=F2..F6, TRIALS=1)
	./hack/measure-mttd.sh $(SCENARIO) $(TRIALS)

# ============================================================================
# Demo
# ============================================================================
.PHONY: demo
demo:  ## Run the 5-minute reproducible demo
	./hack/demo.sh

# ============================================================================
# Utility targets
# ============================================================================
.PHONY: status
status:  ## Show cluster and operator status
	@echo "=== Nodes ==="
	@kubectl get nodes -o wide 2>/dev/null || echo "Cluster not running"
	@echo
	@echo "=== Operator ==="
	@kubectl get pods -n $(NAMESPACE) -l app.kubernetes.io/component=manager 2>/dev/null || echo "Not deployed"
	@echo
	@echo "=== Agents ==="
	@kubectl get pods -n $(NAMESPACE) -l app=netprobe-agent -o wide 2>/dev/null || echo "Not deployed"
	@echo
	@echo "=== NetworkHealthCheck ==="
	@kubectl get nhc 2>/dev/null || echo "No CRD or CR"

.PHONY: logs-manager
logs-manager:  ## Tail operator logs
	kubectl logs -n $(NAMESPACE) -l app.kubernetes.io/component=manager -f

.PHONY: logs-agent
logs-agent:  ## Tail agent logs (all pods)
	kubectl logs -n $(NAMESPACE) -l app=netprobe-agent -f --max-log-requests=5

.PHONY: clean
clean:  ## Remove built binaries and images
	rm -rf bin/
	docker rmi $(MANAGER_IMG) 2>/dev/null || true
	docker rmi $(AGENT_IMG) 2>/dev/null || true

.PHONY: help
help:  ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-22s\033[0m %s\n", $$1, $$2}'
