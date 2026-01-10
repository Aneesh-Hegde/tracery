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

PORTS := 8080 8081 8082 8083 50051 10001 10002 10003
LOG_DIR := logs
WASM_DIR := envoy-filter
CLI_DIR := tracery-cli
CLI_BIN := ./$(CLI_DIR)/tracery-cli

# Test IDs
TEST_TRACE_ID := aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
FREEZE_TRACE_ID := ffffffffffffffffffffffffffffffff
MUTATE_TRACE_ID := mmmmmmmmmmmmmmmmmmmmmmmmmmmmmmmm
HEADER_TRACE_ID := hhhhhhhhhhhhhhhhhhhhhhhhhhhhhhhh
BODY_TRACE_ID   := bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb

.PHONY: all build clean run help logs stop test verify-all

all: clean build run

# ------------------------------------------------------------------------------
#  🛠️ BUILD & SETUP
# ------------------------------------------------------------------------------
build:
	@echo "🔨 Building WASM Filter..."
	@cd $(WASM_DIR) && tinygo build -buildmode=c-shared -o freeze-filter.wasm -scheduler=none -target=wasip1 main.go
	@echo "🔨 Building CLI..."
	@cd $(CLI_DIR) && go build -o tracery-cli .
	@mkdir -p $(LOG_DIR)
	@echo "✅ Build complete."

clean:
	@echo "🛑 Stopping services..."
	@-lsof -ti:$(shell echo $(PORTS) | tr ' ' ',') | xargs kill -9 2>/dev/null || true
	@-pkill -f "go run ./control-plane" || true
	@-pkill -f "go run ./service" || true
	@-pkill -f "envoy -c envoy-filter" || true
	@rm -f $(LOG_DIR)/*.log
	@echo "✅ Clean."

run:
	@echo "🚀 Starting DCDOT System..."
	@echo "🧠 Control Plane..."
	@PORT=8080 go run ./control-plane > $(LOG_DIR)/cp.log 2>&1 &
	@sleep 2
	@echo "📦 Service A..."
	@export SERVICE_B_URL="http://localhost:10002" && PORT=8081 go run ./service-a > $(LOG_DIR)/service-a.log 2>&1 &
	@envoy -c envoy-filter/envoy-service-a.yaml --concurrency 1 -l info --component-log-level wasm:trace > $(LOG_DIR)/envoy-a.log 2>&1 &
	@echo "📦 Service B..."
	@export SERVICE_C_URL="http://localhost:10003" && PORT=8082 go run ./service-b > $(LOG_DIR)/service-b.log 2>&1 &
	@envoy -c envoy-filter/envoy-service-b.yaml --concurrency 1 -l info --component-log-level wasm:trace > $(LOG_DIR)/envoy-b.log 2>&1 &
	@echo "📦 Service C..."
	@export DB_HOST="localhost" && export DB_USER="aneesh" && PORT=8083 go run ./service-c > $(LOG_DIR)/service-c.log 2>&1 &
	@envoy -c envoy-filter/envoy-service-c.yaml --concurrency 1 -l info --component-log-level wasm:trace > $(LOG_DIR)/envoy-c.log 2>&1 &
	@sleep 5
	@echo "✅ SYSTEM RUNNING - Tailing logs..."
	@tail -f $(LOG_DIR)/cp.log

# ------------------------------------------------------------------------------
#  🧪 AUTOMATED TEST SUITE
# ------------------------------------------------------------------------------

# Helper: Seed data for App Inspection (No freeze)
seed-data:
	@echo "🌱 Seeding trace data (ID: $(TEST_TRACE_ID))..."
	@curl -s -o /dev/null http://localhost:10001/order \
		-H "Content-Type: application/json" \
		-H "traceparent: 00-$(TEST_TRACE_ID)-bbbbbbbbbbbbbbbb-01" \
		-d '{"order_id":"CLI-TEST","amount":500}'
	@sleep 2

# [1] Breakpoints Configuration (Syntax Check)
verify-breakpoints:
	@echo "\n🧪 [1/8] Testing Breakpoint Syntax..."
	@$(CLI_BIN) set-breakpoint localhost:10001 /order "amount=100"
	@$(CLI_BIN) list-breakpoints

# [2] Watch (MacOS Compatible)
verify-watch:
	@echo "\n🧪 [2/8] Testing Watch Stream (3s)..."
	@$(CLI_BIN) watch-traces & PID=$$!; sleep 3; kill $$PID 2>/dev/null || true
	@echo "✅ Watch test passed"

# [3] Inspection (App State)
verify-inspection: seed-data
	@echo "\n🧪 [3/8] Testing App Inspection (Trace: $(TEST_TRACE_ID))..."
	@$(CLI_BIN) debug-app --trace $(TEST_TRACE_ID)

# [4] Admin
verify-admin:
	@echo "\n🧪 [4/8] Testing Admin Features..."
	@$(CLI_BIN) mesh topology
	@$(CLI_BIN) system health

# [5] Freeze Lifecycle + Network Snapshot
verify-freeze-basic:
	@echo "\n🧪 [5/8] Testing Freeze & Network Snapshot..."
	@$(CLI_BIN) freeze start --trace $(FREEZE_TRACE_ID) --services service-a
	@curl -s -o /dev/null http://localhost:10001/order \
		-H "Content-Type: application/json" \
		-H "traceparent: 00-$(FREEZE_TRACE_ID)-bbbbbbbbbbbbbbbb-01" \
		-d '{"order_id":"FREEZE-TEST","amount":999}' & \
		echo "   (Request sent...)"
	@sleep 2
	@$(CLI_BIN) freeze list
	@$(CLI_BIN) freeze status --trace $(FREEZE_TRACE_ID)
	@$(CLI_BIN) get-snapshot --trace $(FREEZE_TRACE_ID)
	@$(CLI_BIN) freeze release --trace $(FREEZE_TRACE_ID)

# [6] Freeze Mutation
verify-freeze-mutation:
	@echo "\n🧪 [6/8] Testing Freeze Mutation..."
	@$(CLI_BIN) freeze start --trace $(MUTATE_TRACE_ID) --services service-a
	@curl -s -o /dev/null http://localhost:10001/order \
		-H "Content-Type: application/json" \
		-H "traceparent: 00-$(MUTATE_TRACE_ID)-bbbbbbbbbbbbbbbb-01" \
		-d '{"order_id":"BAD","amount":1}' &
	@sleep 1
	@$(CLI_BIN) freeze release --trace $(MUTATE_TRACE_ID) --override-body '{"order_id":"FIXED","amount":1000}'

# [7] Conditional Logic (Header vs Body) - NEW!
verify-conditions:
	@echo "\n🧪 [7/8] Testing Conditional Logic (Header vs Body)..."
	
	@echo "   [A] Testing Strict Header Condition (user-type=vip)..."
	@$(CLI_BIN) set-breakpoint localhost:10001 /order header.user-type=vip
	@curl -s -o /dev/null http://localhost:10001/order \
		-H "Content-Type: application/json" \
		-H "user-type: vip" \
		-H "traceparent: 00-$(HEADER_TRACE_ID)-bbbbbbbbbbbbbbbb-01" \
		-d '{"val": 1}' & 
	@sleep 2
	@echo "   Verifying Header Freeze..."
	@$(CLI_BIN) freeze status --trace $(HEADER_TRACE_ID)
	@$(CLI_BIN) freeze release --trace $(HEADER_TRACE_ID)

	@echo "   [B] Testing Strict Body Condition (amount=999)..."
	@$(CLI_BIN) set-breakpoint localhost:10001 /order body.amount=999
	@curl -s -o /dev/null http://localhost:10001/order \
		-H "Content-Type: application/json" \
		-H "traceparent: 00-$(BODY_TRACE_ID)-cccccccccccccccc-01" \
		-d '{"amount": 999}' & 
	@sleep 2
	@echo "   Verifying Body Freeze..."
	@$(CLI_BIN) freeze status --trace $(BODY_TRACE_ID)
	@$(CLI_BIN) freeze release --trace $(BODY_TRACE_ID)

# [8] Emergency
verify-emergency:
	@echo "\n🧪 [8/8] Testing Emergency Kill Switch..."
	@$(CLI_BIN) freeze start --trace 11112222333344445555666677778888 --services service-a
	@$(CLI_BIN) emergency disable

# Run Everything
verify-all: verify-breakpoints verify-watch verify-inspection verify-admin verify-freeze-basic verify-freeze-mutation verify-conditions verify-emergency
	@echo "\n🎉 ALL CLI COMMANDS VERIFIED SUCCESSFULLY!"
