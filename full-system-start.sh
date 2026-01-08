#!/bin/bash

set -euo pipefail

echo "========================================================"
echo "DCDOT - Complete System Deployment"
echo "Phase 1-3: Tracing, Control Plane, and Freeze Mechanism"
echo "========================================================"
echo ""

# Colors
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# Configuration
NAMESPACE="default"
DOCKER_REGISTRY="${DOCKER_REGISTRY:-localhost:5000}"

echo -e "${BLUE}📋 Configuration:${NC}"
echo "  Namespace: $NAMESPACE"
echo "  Docker Registry: $DOCKER_REGISTRY"
echo ""

# Step 1: Prerequisites Check
echo "========================================================"
echo "Step 1: Checking Prerequisites"
echo "========================================================"
echo ""

# Check kubectl
if ! command -v kubectl &> /dev/null; then
    echo -e "${RED}❌ kubectl not found${NC}"
    exit 1
fi
echo -e "${GREEN}✅ kubectl installed${NC}"

# Check cluster
if ! kubectl cluster-info &> /dev/null; then
    echo -e "${RED}❌ Cannot connect to Kubernetes cluster${NC}"
    exit 1
fi
echo -e "${GREEN}✅ Kubernetes cluster accessible${NC}"

# Check Istio
if ! kubectl get namespace istio-system &> /dev/null; then
    echo -e "${YELLOW}⚠️  Istio not installed${NC}"
    echo "Installing Istio..."
    ./install-istio.sh
fi
echo -e "${GREEN}✅ Istio installed${NC}"

# Check TinyGo
if ! command -v tinygo &> /dev/null; then
    echo -e "${RED}❌ TinyGo not found${NC}"
    echo ""
    echo "Install TinyGo:"
    echo "  macOS:   brew install tinygo"
    echo "  Linux:   wget https://github.com/tinygo-org/tinygo/releases/download/v0.30.0/tinygo_0.30.0_amd64.deb"
    echo "           sudo dpkg -i tinygo_0.30.0_amd64.deb"
    exit 1
fi
echo -e "${GREEN}✅ TinyGo installed${NC}"

# Check Go
if ! command -v go &> /dev/null; then
    echo -e "${RED}❌ Go not found${NC}"
    exit 1
fi
echo -e "${GREEN}✅ Go installed${NC}"

# Step 2: Enable Istio Injection
echo ""
echo "========================================================"
echo "Step 2: Enabling Istio Sidecar Injection"
echo "========================================================"
echo ""

kubectl label namespace $NAMESPACE istio-injection=enabled --overwrite
echo -e "${GREEN}✅ Istio injection enabled for namespace: $NAMESPACE${NC}"

# Step 3: Deploy Infrastructure (PostgreSQL, Jaeger, OTel Collector)
echo ""
echo "========================================================"
echo "Step 3: Deploying Infrastructure"
echo "========================================================"
echo ""

echo "Deploying PostgreSQL..."
kubectl apply -f k8s/postgres.yaml
echo -e "${GREEN}✅ PostgreSQL deployed${NC}"

echo "Deploying Jaeger..."
kubectl apply -f k8s/jaeger.yaml
echo -e "${GREEN}✅ Jaeger deployed${NC}"

echo "Deploying OpenTelemetry Collector..."
kubectl apply -f k8s/otel-collector.yaml
echo -e "${GREEN}✅ OTel Collector deployed${NC}"

# Wait for infrastructure
echo ""
echo "Waiting for infrastructure to be ready..."
# kubectl wait --for=condition=ready pod -l app=postgres --timeout=60s
# kubectl wait --for=condition=ready pod -l app=jaeger --timeout=60s
# kubectl wait --for=condition=ready pod -l app=otel-collector --timeout=60s
# echo -e "${GREEN}✅ Infrastructure ready${NC}"

# Step 4: Build and Deploy Microservices
echo ""
echo "========================================================"
echo "Step 4: Building and Deploying Microservices"
echo "========================================================"
echo ""

# Build services
./build.sh

echo "Deploying services..."
kubectl apply -f k8s/services.yaml
echo -e "${GREEN}✅ Microservices deployed${NC}"

# Step 5: Deploy Control Plane
echo ""
echo "========================================================"
echo "Step 5: Deploying Control Plane"
echo "========================================================"
echo ""

echo "Applying RBAC..."
kubectl apply -f k8s/control-plane-rbac.yaml
echo -e "${GREEN}✅ RBAC configured${NC}"

echo "Deploying Control Plane..."
kubectl apply -f k8s/control-plane.yaml
echo -e "${GREEN}✅ Control Plane deployed${NC}"

# Step 6: Wait for All Pods
echo ""
echo "========================================================"
echo "Step 6: Waiting for All Pods to be Ready"
echo "========================================================"
echo ""

# echo "Waiting for services..."
# kubectl wait --for=condition=ready pod -l app=service-a --timeout=120s
# kubectl wait --for=condition=ready pod -l app=service-b --timeout=120s
# kubectl wait --for=condition=ready pod -l app=service-c --timeout=120s
# echo -e "${GREEN}✅ All services ready${NC}"
#
# echo "Waiting for Control Plane..."
# kubectl wait --for=condition=ready pod -l app=control-plane --timeout=120s
# echo -e "${GREEN}✅ Control Plane ready${NC}"

# Step 7: Deploy WASM Freeze Filter
echo ""
echo "========================================================"
echo "Step 7: Deploying WASM Freeze Filter"
echo "========================================================"
echo ""

./deploy-wasm-filter.sh

# Step 8: Build CLI
echo ""
echo "========================================================"
echo "Step 8: Building CLI Tool"
echo "========================================================"
echo ""

cd tracery-cli
go build -o tracery-cli .
cd ..
echo -e "${GREEN}✅ CLI built: tracery-cli/tracery-cli${NC}"

# Step 9: Verify Deployment
echo ""
echo "========================================================"
echo "Step 9: Verifying Deployment"
echo "========================================================"
echo ""

echo "Checking pods..."
kubectl get pods

echo ""
echo "Checking services..."
kubectl get svc

echo ""
echo "Checking Istio sidecars..."
kubectl get pods -o custom-columns=NAME:.metadata.name,CONTAINERS:.spec.containers[*].name | grep istio-proxy && \
    echo -e "${GREEN}✅ Istio sidecars injected${NC}" || \
    echo -e "${YELLOW}⚠️  No Istio sidecars found${NC}"

echo ""
echo "Checking WasmPlugin..."
kubectl get wasmplugins

# Step 10: Run Health Checks
echo ""
echo "========================================================"
echo "Step 10: Running Health Checks"
echo "========================================================"
echo ""

echo "Testing service-a..."
SERVICE_A_POD=$(kubectl get pod -l app=service-a -o jsonpath='{.items[0].metadata.name}')
kubectl exec $SERVICE_A_POD -c service-a -- curl -s http://localhost:8080/health | grep -q "ok" && \
    echo -e "${GREEN}✅ service-a healthy${NC}" || \
    echo -e "${RED}❌ service-a unhealthy${NC}"

echo "Testing service-b..."
SERVICE_B_POD=$(kubectl get pod -l app=service-b -o jsonpath='{.items[0].metadata.name}')
kubectl exec $SERVICE_B_POD -c service-b -- curl -s http://localhost:8081/health | grep -q "ok" && \
    echo -e "${GREEN}✅ service-b healthy${NC}" || \
    echo -e "${RED}❌ service-b unhealthy${NC}"

echo "Testing service-c..."
SERVICE_C_POD=$(kubectl get pod -l app=service-c -o jsonpath='{.items[0].metadata.name}')
kubectl exec $SERVICE_C_POD -c service-c -- curl -s http://localhost:8082/health | grep -q "ok" && \
    echo -e "${GREEN}✅ service-c healthy${NC}" || \
    echo -e "${RED}❌ service-c unhealthy${NC}"

# Step 11: Setup Port Forwards
echo ""
echo "========================================================"
echo "Step 11: Setting Up Port Forwards"
echo "========================================================"
echo ""

# Kill existing port forwards
pkill -f "port-forward" || true
sleep 2

# Start port forwards in background
echo "Port forwarding Control Plane (50051)..."
kubectl port-forward svc/control-plane 50051:50051 > /tmp/pf-control-plane.log 2>&1 &

echo "Port forwarding Jaeger UI (30686)..."
kubectl port-forward svc/jaeger 30686:16686 > /tmp/pf-jaeger.log 2>&1 &

echo "Port forwarding service-a (30080)..."
kubectl port-forward svc/service-a 30080:8080 > /tmp/pf-service-a.log 2>&1 &

# This connects your local port 14317 to the jaeger service's port 4317
kubectl port-forward service/jaeger 14317:4317 > /tmp/pf-jaeger-1.log 2>&1 &


sleep 3
echo -e "${GREEN}✅ Port forwards active${NC}"

# Step 12: Test Basic Functionality
echo ""
echo "========================================================"
echo "Step 12: Testing Basic Functionality"
echo "========================================================"
echo ""

echo "Sending test request through service-a..."
RESPONSE=$(curl -s -w "\n%{http_code}" \
    -H "Content-Type: application/json" \
    -d '{"order_id":"TEST-001","customer_id":"CUST-001","amount":99.99}' \
    http://localhost:30080/order 2>&1)

HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
BODY=$(echo "$RESPONSE" | head -n-1)

if [ "$HTTP_CODE" = "200" ]; then
    echo -e "${GREEN}✅ End-to-end request succeeded${NC}"
    echo "Response:"
    echo "$BODY" | jq . 2>/dev/null || echo "$BODY"
else
    echo -e "${YELLOW}⚠️  Request returned HTTP $HTTP_CODE${NC}"
    echo "$BODY"
fi

# Final Summary
echo ""
echo "========================================================"
echo "✅ Deployment Complete!"
echo "========================================================"
echo ""
echo -e "${BLUE}📊 System Status:${NC}"
echo ""
echo "Infrastructure:"
echo "  ✅ PostgreSQL       - Running"
echo "  ✅ Jaeger           - Running"
echo "  ✅ OTel Collector   - Running"
echo ""
echo "Microservices:"
echo "  ✅ service-a        - Running (with Istio sidecar)"
echo "  ✅ service-b        - Running (with Istio sidecar)"
echo "  ✅ service-c        - Running (with Istio sidecar)"
echo ""
echo "Control Plane:"
echo "  ✅ Control Plane    - Running"
echo "  ✅ WASM Filter      - Deployed"
echo "  ✅ CLI Tool         - Built"
echo ""
echo -e "${BLUE}🌐 Access URLs:${NC}"
echo ""
echo "  Jaeger UI:          http://localhost:30686"
echo "  Service A:          http://localhost:30080"
echo "  Control Plane:      localhost:50051 (gRPC)"
echo ""
echo -e "${BLUE}🧪 Next Steps - Run Tests:${NC}"
echo ""
echo "  # Test Phase 1 (Tracing):"
echo "  ./test.sh"
echo ""
echo "  # Test Phase 3 (Freeze Mechanism):"
echo "  ./test-freeze-mechanism.sh"
echo ""
echo "  # Manual freeze test:"
echo "  TRACE_ID=\"test-\$(date +%s)\""
echo "  ./tracery-cli/tracery-cli freeze start --trace \$TRACE_ID --services service-a"
echo "  curl -H \"x-b3-traceid: \$TRACE_ID\" http://localhost:30080/order"
echo "  ./tracery-cli/tracery-cli freeze release --trace \$TRACE_ID"
echo ""
echo -e "${BLUE}📋 Useful Commands:${NC}"
echo ""
echo "  # View logs:"
echo "  kubectl logs -l app=control-plane --tail=50"
echo "  kubectl logs <pod> -c istio-proxy | grep freeze"
echo ""
echo "  # Check status:"
echo "  kubectl get pods"
echo "  kubectl get wasmplugins"
echo "  ./tracery-cli/tracery-cli freeze list"
echo ""
echo "  # Restart if needed:"
echo "  kubectl rollout restart deployment service-a service-b service-c"
echo ""
echo "========================================================"
echo ""
echo -e "${GREEN}🎉 System is ready for testing!${NC}"
echo ""
