# =============================================================================
# Tracery – Local Kind Workflow: build.sh → istio → redeploy → deploy → test
# =============================================================================
.PHONY: all build load deploy port-forward test clean

KIND_CLUSTER   ?= tracery
NAMESPACE      ?= default
CLI            ?= ./tracery-cli/tracery-cli

# ------------------------------------------------------------------
# 1. BUILD (run your build.sh)
# ------------------------------------------------------------------
build:
	@echo "Running build.sh..."
	@chmod +x build.sh
	@./build.sh

# ------------------------------------------------------------------
# 2. LOAD images into kind (no registry)
# ------------------------------------------------------------------
load:
	@echo "Loading images into kind cluster $(KIND_CLUSTER)"
	@kind load docker-image dcdot-service-a:latest --name $(KIND_CLUSTER)
	@kind load docker-image dcdot-service-b:latest --name $(KIND_CLUSTER)
	@kind load docker-image dcdot-service-c:latest --name $(KIND_CLUSTER)
	@kind load docker-image dcdot-control-plane:latest --name $(KIND_CLUSTER)

# ------------------------------------------------------------------
# 3. DEPLOY (your existing scripts)
# ------------------------------------------------------------------
deploy: load
	@echo "Installing Istio..."
	@chmod +x install-istio.sh
	@./install-istio.sh

	# @echo "Redeploying services with Istio sidecars..."
	# @chmod +x redeploy-istio.sh
	# @./redeploy-istio.sh

	@echo "Deploying core stack (Postgres, OTel, Jaeger, Services)..."
	@chmod +x deploy.sh
	@./deploy.sh

	@echo "Deploying Control Plane RBAC + WASM plugin..."
	@kubectl apply -f k8s/control-plane-rbac.yaml
	@kubectl apply -f k8s/wasm-plugin.yaml

	@echo "Waiting for Control Plane..."
	@kubectl wait --for=condition=ready pod -l app=control-plane --timeout=120s

# ------------------------------------------------------------------
# 4. PORT-FORWARD (required for gRPC & HTTP)
# ------------------------------------------------------------------
port-forward:
	@echo "Starting port-forwarding..."
	@kubectl port-forward svc/service-a      30080:80     > /dev/null 2>&1 & echo $$! > .pf_api.pid
	@kubectl port-forward svc/jaeger         30686:16686 > /dev/null 2>&1 & echo $$! > .pf_jaeger.pid
	@kubectl port-forward deployment/control-plane 50051:50051 > /dev/null 2>&1 & echo $$! > .pf_grpc.pid
	@echo "Active:"
	@echo "   API → http://localhost:30080"
	@echo "   Jaeger → http://localhost:30686"
	@echo "   gRPC CP → localhost:50051"
	@trap 'echo "Stopping..."; kill $$(cat .pf_*.pid 2>/dev/null) 2>/dev/null || true; rm -f .pf_*.pid' EXIT

# ------------------------------------------------------------------
# 5. TEST PHASE 3 (with port-forwarding)
# ------------------------------------------------------------------
test: deploy port-forward
	@echo "Waiting 10s for stabilization..."
	@sleep 10
	@echo "Running Phase 3 test suite..."
	@chmod +x test-phase3.sh
	@./test-phase3.sh

# ------------------------------------------------------------------
# 6. ONE COMMAND
# ------------------------------------------------------------------
all: build test

# ------------------------------------------------------------------
# 7. CLEANUP
# ------------------------------------------------------------------
clean:
	@echo "Cleaning up..."
	@kill $$(cat .pf_*.pid 2>/dev/null) 2>/dev/null || true
	@rm -f .pf_*.pid
	@kubectl delete -f k8s/services.yaml --ignore-not-found
	@kubectl delete -f k8s/control-plane.yaml --ignore-not-found
	@kubectl delete -f k8s/wasm-plugin.yaml --ignore-not-found
	@kubectl delete -f k8s/otel-collector.yaml --ignore-not-found
	@kubectl delete -f k8s/jaeger.yaml --ignore-not-found
	@kubectl delete -f k8s/postgres.yaml --ignore-not-found
	@kubectl delete envoyfilter --all --ignore-not-found
