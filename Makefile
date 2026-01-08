# # =============================================================================
# # Tracery – Local Kind Workflow: build.sh → istio → redeploy → deploy → test
# # =============================================================================
# .PHONY: all build load deploy port-forward test clean
#
# KIND_CLUSTER   ?= tracery
# NAMESPACE      ?= default
# CLI            ?= ./tracery-cli/tracery-cli
#
# # ------------------------------------------------------------------
# # 1. BUILD (run your build.sh)
# # ------------------------------------------------------------------
# build:
# 	@echo "Running build.sh..."
# 	@chmod +x build.sh
# 	@./build.sh
#
# # ------------------------------------------------------------------
# # 2. LOAD images into kind (no registry)
# # ------------------------------------------------------------------
# load:
# 	@echo "Loading images into kind cluster $(KIND_CLUSTER)"
# 	@kind load docker-image dcdot-service-a:latest --name $(KIND_CLUSTER)
# 	@kind load docker-image dcdot-service-b:latest --name $(KIND_CLUSTER)
# 	@kind load docker-image dcdot-service-c:latest --name $(KIND_CLUSTER)
# 	@kind load docker-image dcdot-control-plane:latest --name $(KIND_CLUSTER)
#
# # ------------------------------------------------------------------
# # 3. DEPLOY (your existing scripts)
# # ------------------------------------------------------------------
# deploy: load
# 	@echo "Installing Istio..."
# 	@chmod +x install-istio.sh
# 	@./install-istio.sh
#
# 	# @echo "Redeploying services with Istio sidecars..."
# 	# @chmod +x redeploy-istio.sh
# 	# @./redeploy-istio.sh
#
# 	@echo "Deploying core stack (Postgres, OTel, Jaeger, Services)..."
# 	@chmod +x deploy.sh
# 	@./deploy.sh
#
# 	@echo "Deploying Control Plane RBAC + WASM plugin..."
# 	@kubectl apply -f k8s/control-plane-rbac.yaml
# 	@kubectl apply -f k8s/wasm-plugin.yaml
#
# 	@echo "Waiting for Control Plane..."
# 	@kubectl wait --for=condition=ready pod -l app=control-plane --timeout=120s
#
# # ------------------------------------------------------------------
# # 4. PORT-FORWARD (required for gRPC & HTTP)
# # ------------------------------------------------------------------
# port-forward:
# 	@echo "Starting port-forwarding..."
# 	@kubectl port-forward svc/service-a      30080:80     > /dev/null 2>&1 & echo $$! > .pf_api.pid
# 	@kubectl port-forward svc/jaeger         30686:16686 > /dev/null 2>&1 & echo $$! > .pf_jaeger.pid
# 	@kubectl port-forward deployment/control-plane 50051:50051 > /dev/null 2>&1 & echo $$! > .pf_grpc.pid
# 	@echo "Active:"
# 	@echo "   API → http://localhost:30080"
# 	@echo "   Jaeger → http://localhost:30686"
# 	@echo "   gRPC CP → localhost:50051"
# 	@trap 'echo "Stopping..."; kill $$(cat .pf_*.pid 2>/dev/null) 2>/dev/null || true; rm -f .pf_*.pid' EXIT
#
# # ------------------------------------------------------------------
# # 5. TEST PHASE 3 (with port-forwarding)
# # ------------------------------------------------------------------
# test: deploy port-forward
# 	@echo "Waiting 10s for stabilization..."
# 	@sleep 10
# 	@echo "Running Phase 3 test suite..."
# 	@chmod +x test-phase3.sh
# 	@./test-phase3.sh
#
# # ------------------------------------------------------------------
# # 6. ONE COMMAND
# # ------------------------------------------------------------------
# all: build test
#
# # ------------------------------------------------------------------
# # 7. CLEANUP
# # ------------------------------------------------------------------
# clean:
# 	@echo "Cleaning up..."
# 	@kill $$(cat .pf_*.pid 2>/dev/null) 2>/dev/null || true
# 	@rm -f .pf_*.pid
# 	@kubectl delete -f k8s/services.yaml --ignore-not-found
# 	@kubectl delete -f k8s/control-plane.yaml --ignore-not-found
# 	@kubectl delete -f k8s/wasm-plugin.yaml --ignore-not-found
# 	@kubectl delete -f k8s/otel-collector.yaml --ignore-not-found
# 	@kubectl delete -f k8s/jaeger.yaml --ignore-not-found
# 	@kubectl delete -f k8s/postgres.yaml --ignore-not-found
# 	@kubectl delete envoyfilter --all --ignore-not-found

# ==============================================================================
#  DCDOT - Universal Distributed Debugger (Local Runner)
# ==============================================================================

# Ports configuration
# 8080=CP_HTTP, 50051=CP_GRPC
# 8081-8083=Go_Apps
# 10001-10003=Envoys
PORTS := 8080 8081 8082 8083 50051 10001 10002 10003

# Directories
LOG_DIR := logs
WASM_DIR := envoy-filter
CLI_DIR := tracery-cli

.PHONY: all build clean run help logs stop test

# Default target
all: clean build run

# ------------------------------------------------------------------------------
#  🛠️ BUILD
# ------------------------------------------------------------------------------
build:
	@echo "🔨 [1/2] Building WASM Filter..."
	@cd $(WASM_DIR) && tinygo build -buildmode=c-shared -o freeze-filter.wasm -scheduler=none -target=wasip1 main.go
	@echo "🔨 [2/2] Building CLI..."
	@cd $(CLI_DIR) && go build -o tracery-cli .
	@mkdir -p $(LOG_DIR)
	@echo "✅ Build complete."

# ------------------------------------------------------------------------------
#  🧹 CLEAN / STOP
# ------------------------------------------------------------------------------
clean:
	@echo "🛑 Stopping services and cleaning ports..."
	@# Kill any process listening on our specific ports
	@-lsof -ti:$(shell echo $(PORTS) | tr ' ' ',') | xargs kill -9 2>/dev/null || true
	@# Force kill specific binary names just in case
	@-pkill -f "go run ./control-plane" || true
	@-pkill -f "go run ./service" || true
	@-pkill -f "envoy -c envoy-filter" || true
	@rm -f $(LOG_DIR)/*.log
	@echo "✅ Ports $(PORTS) are clear."

stop: clean

# ------------------------------------------------------------------------------
#  🚀 RUN
# ------------------------------------------------------------------------------
run:
	@echo "🚀 Starting DCDOT System..."
	
	@# 1. Start Control Plane
	@echo "🧠 Starting Control Plane..."
	@PORT=8080 go run ./control-plane > $(LOG_DIR)/cp.log 2>&1 &
	@sleep 2

	@# 2. Start Service A + Envoy (Calls B at 10002)
	@echo "📦 Starting Service A..."
	@export SERVICE_B_URL="http://localhost:10002" && \
		PORT=8081 go run ./service-a > $(LOG_DIR)/service-a.log 2>&1 &
	@envoy -c envoy-filter/envoy-service-a.yaml --concurrency 1 -l info --component-log-level wasm:trace > $(LOG_DIR)/envoy-a.log 2>&1 &

	@# 3. Start Service B + Envoy (Calls C at 10003)
	@echo "📦 Starting Service B..."
	@export SERVICE_C_URL="http://localhost:10003" && \
		PORT=8082 go run ./service-b > $(LOG_DIR)/service-b.log 2>&1 &
	@envoy -c envoy-filter/envoy-service-b.yaml --concurrency 1 -l info --component-log-level wasm:trace > $(LOG_DIR)/envoy-b.log 2>&1 &

	@# 4. Start Service C + Envoy (Connects to Local DB)
	@echo "📦 Starting Service C..."
	@export DB_HOST="localhost" && export DB_USER="aneesh" && \
		PORT=8083 go run ./service-c > $(LOG_DIR)/service-c.log 2>&1 &
	@envoy -c envoy-filter/envoy-service-c.yaml --concurrency 1 -l info --component-log-level wasm:trace > $(LOG_DIR)/envoy-c.log 2>&1 &

	@sleep 2
	@echo "✅ SYSTEM RUNNING"
	@echo "📝 Tailing logs (Ctrl+C to stop, 'make clean' to cleanup)..."
	@echo "------------------------------------------------------------"
	@tail -f $(LOG_DIR)/cp.log $(LOG_DIR)/envoy-a.log

# ------------------------------------------------------------------------------
#  🔍 UTILS
# ------------------------------------------------------------------------------
logs:
	@tail -f $(LOG_DIR)/*.log

logs-wasm:
	@echo "📝 Watching WASM logs across all envoys..."
	@tail -f $(LOG_DIR)/envoy-*.log | grep -E "\[wasm|\[TICK|\[HTTP|\[PLUGIN|\[VM"

logs-cp:
	@tail -f $(LOG_DIR)/cp.log

logs-envoy-a:
	@tail -f $(LOG_DIR)/envoy-a.log

test:
	@echo "🧪 Sending Test Request..."
	@curl -v http://localhost:10001/order \
		-H "Content-Type: application/json" \
		-H "traceparent: 00-11111111111111111111111111111111-2222222222222222-01" \
		-d '{"order_id":"TEST-MAKE","customer_id":"MAKEFILE","amount":100.0}'

# Test with a request that should be frozen
test-freeze:
	@echo "🧪 Sending Test Request (should FREEZE)..."
	@curl -v http://localhost:10001/order \
		-H "Content-Type: application/json" \
		-H "traceparent: 00-ffffffffffffffffffffffffffffffff-2222222222222222-01" \
		-d '{"order_id":"FREEZE-TEST","customer_id":"MAKEFILE","amount":999.0}'

help:
	@echo "Available commands:"
	@echo "  make run         - Build and start everything (tails logs)"
	@echo "  make clean       - Force kill all processes on ports $(PORTS)"
	@echo "  make test        - Send a sample curl request to Service A"
	@echo "  make test-freeze - Send a request with trace ID that triggers freeze"
	@echo "  make logs        - Tail all logs"
	@echo "  make logs-wasm   - Tail only WASM-related logs"
	@echo "  make logs-cp     - Tail control plane logs"
	@echo "  make logs-envoy-a- Tail envoy-a logs"
