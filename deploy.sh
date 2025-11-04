#!/bin/bash

set -euo pipefail

echo "======================================"
echo "Deploying DCDOT to Kubernetes"
echo "======================================"

# Deploy PostgreSQL
echo ""
echo "Deploying PostgreSQL..."
kubectl apply -f k8s/postgres.yaml
kubectl wait --for=condition=ready pod -l app=postgres --timeout=120s

# Deploy OpenTelemetry Collector
echo ""
echo "Deploying OpenTelemetry Collector..."
kubectl apply -f k8s/otel-collector.yaml
kubectl wait --for=condition=ready pod -l app=otel-collector --timeout=60s

# Deploy Jaeger
echo ""
echo "Deploying Jaeger..."
kubectl apply -f k8s/jaeger.yaml
kubectl wait --for=condition=ready pod -l app=jaeger --timeout=60s

# Delete existing service deployments
echo ""
echo "Removing old service deployments..."
kubectl delete deployment service-a service-b service-c
sleep 5

# Deploy Microservices
echo ""
echo "Deploying Microservices..."
kubectl apply -f k8s/services.yaml

echo ""
echo "Waiting for services to be ready..."
kubectl wait --for=condition=ready pod -l app=service-a --timeout=120s --namespace=default
kubectl wait --for=condition=ready pod -l app=service-b --timeout=120s --namespace=default
kubectl wait --for=condition=ready pod -l app=service-c --timeout=120s --namespace=default

echo ""
echo "======================================"
echo "✅ Deployment Complete!"
echo "======================================"
echo ""
echo "Service endpoints:"
echo "  - Service A (API Gateway): http://localhost:30080"
echo "  - Jaeger UI: http://localhost:30686"
echo ""
echo "To test the system, run:"
echo "  curl -X POST http://localhost:30080/order \\"
echo "    -H 'Content-Type: application/json' \\"
echo "    -d '{\"order_id\":\"ORD-001\",\"customer_id\":\"CUST-123\",\"amount\":99.99}'"
echo ""




echo "======================================"
echo "Deploying Phase 2: Service Mesh & Control Plane"
echo "======================================"

# Step 1: Install Istio
echo ""
echo "Step 1: Installing Istio..."
if [ ! -f "install-istio.sh" ]; then
    echo "Error: install-istio.sh not found"
    exit 1
fi
chmod +x install-istio.sh
./install-istio.sh

# Step 2: Redeploy services with Istio sidecars
echo ""
echo "Step 2: Redeploying services with Istio sidecars..."
if [ ! -f "redeploy-istio.sh" ]; then
    echo "Error: redeploy-istio.sh not found"
    exit 1
fi
chmod +x redeploy-istio.sh
./redeploy-istio.sh

# Step 3: Deploy Control Plane
echo ""
echo "Step 3: Deploying Control Plane..."
kubectl apply -f k8s/control-plane.yaml

echo ""
echo "Waiting for Control Plane to be ready..."
kubectl wait --for=condition=ready pod -l app=control-plane --timeout=120s--namespace=default

echo ""                                                                   
echo "======================================"
echo "✅ Phase 2 Deployment Complete!"
echo "======================================"
echo ""
echo "Verification:"
echo "1. Check Istio installation:"
echo "   kubectl get pods -n istio-system"
echo ""
echo "2. Check services have sidecars (should show 2/2):"
echo "   kubectl get pods"
echo ""
echo "3. Check Control Plane:"
echo "   kubectl logs -l app=control-plane"
echo ""
echo "4. Test CLI:"
echo "   ./dcdot-cli/dcdot-cli list-breakpoints"
echo ""


echo "======================================"
echo "Deploying Phase 3: Traffic Freezing"
echo "======================================"

kubectl apply -f k8s/control-plane-rbac.yaml

# Restart Control Plane with new image
echo ""
echo "Restarting Control Plane with Phase 3 features..."
kubectl delete pod -l app=control-plane

echo "Waiting for Control Plane to restart..."
kubectl wait --for=condition=ready pod -l app=control-plane --timeout=120s

echo ""
echo "======================================"
echo "✅ Phase 3 Deployment Complete!"
echo "======================================"
echo ""
echo "Verification:"
kubectl get pods -l app=control-plane
echo ""
kubectl logs -l app=control-plane --tail=20
echo ""
echo "Control Plane should show: 'Phase 3: Traffic Freezing ENABLED'"
