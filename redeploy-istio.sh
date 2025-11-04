#!/bin/bash

set -euo pipefail

echo "======================================"
echo "Redeploying Services with Istio Sidecars"
echo "======================================"

# Delete existing service deployments (keeps DB, OTEL, Jaeger)
echo ""
echo "Removing old service deployments..."
kubectl delete deployment service-a service-b service-c

# Wait a moment
sleep 5

# Redeploy services (automatic sidecar injection will happen)
echo ""
echo "Deploying services with Istio sidecars..."
kubectl apply -f k8s/services.yaml

# Wait for services to be ready (now with 2 containers each)
echo ""
echo "Waiting for services to be ready (this may take 2-3 minutes)..."
kubectl wait --for=condition=ready pod -l app=service-a --timeout=180s --namespace=default
kubectl wait --for=condition=ready pod -l app=service-b --timeout=180s --namespace=default
kubectl wait --for=condition=ready pod -l app=service-c --timeout=180s --namespace=default

echo ""
echo "======================================"
echo "✅ Services redeployed with Istio!"
echo "======================================"
echo ""
echo "Verify sidecars are injected:"
kubectl get pods
echo ""
echo "Each pod should show READY 2/2 (app container + envoy sidecar)"
