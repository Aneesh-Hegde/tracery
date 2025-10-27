#!/bin/bash

set -e

echo "======================================"
echo "Building DCDOT Microservices"
echo "======================================"

# Set the kind cluster name
export KIND_CLUSTER="tracery"

# Build Service A
echo ""
echo "Building Service A (API Gateway)..."
cd service-a
docker build -t dcdot-service-a:latest .
kind load docker-image dcdot-service-a:latest --name $KIND_CLUSTER
cd ..

# Build Service B
echo ""
echo "Building Service B (Order Processing)..."
cd service-b
docker build -t dcdot-service-b:latest .
kind load docker-image dcdot-service-b:latest --name $KIND_CLUSTER
cd ..

# Build Service C
echo ""
echo "Building Service C (Payment Service)..."
cd service-c
docker build -t dcdot-service-c:latest .
kind load docker-image dcdot-service-c:latest --name $KIND_CLUSTER
cd ..

echo ""
echo "======================================"
echo "✅ All services built successfully!"
echo "======================================"
