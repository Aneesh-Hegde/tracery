#!/bin/bash

set -e

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
kubectl wait --for=condition=ready pod -l app=service-a --timeout=120s
kubectl wait --for=condition=ready pod -l app=service-b --timeout=120s
kubectl wait --for=condition=ready pod -l app=service-c --timeout=120s

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
