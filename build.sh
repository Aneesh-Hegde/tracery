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


set -e

echo "======================================"
echo "Building Phase 2 Components"
echo "======================================"

# Build Control Plane
echo ""
echo "Building Control Plane..."
cd control-plane

# Initialize Go module if needed
if [ ! -f "go.sum" ]; then
    go mod tidy
fi

# Build Docker image (protobuf generation happens in Docker)
docker build -t dcdot-control-plane:latest .
kind load docker-image dcdot-control-plane:latest --name $KIND_CLUSTER
cd ..

# Build CLI tool
echo ""
echo "Building DCDOT CLI..."
cd tracery-cli

# Initialize Go module
if [ ! -f "go.sum" ]; then
    # Need to generate proto files first for CLI
    cd ../control-plane
    mkdir -p pb
    protoc --go_out=pb --go_opt=paths=source_relative \
        --go-grpc_out=pb --go-grpc_opt=paths=source_relative \
        proto/controlplane.proto || echo "Warning: protoc not found, will use Docker build"
    cd ../dcdot-cli
    go mod tidy
fi

go build -o tracery-cli main.go
echo "CLI built: dcdot-cli/dcdot-cli"
cd ..

echo ""
echo "======================================"
echo "✅ Phase 2 components built successfully!"
echo "======================================"
echo ""
echo "Note: If CLI build fails due to missing proto files,"
echo "the Control Plane Docker build will generate them."
echo "You can copy them out with:"
echo "  docker create --name temp dcdot-control-plane:latest"
echo "  docker cp temp:/app/pb control-plane/"
echo "  docker rm temp"

#
# echo "======================================"
# echo "Building Phase 3: Traffic Freezing"
# echo "======================================"
#
# export KIND_CLUSTER="tracery"
#
# # Build updated Control Plane
# echo ""
# echo "Building Control Plane with Freeze Coordinator..."
# cd control-plane
#
# # Update dependencies
# echo "Updating Go dependencies..."
# go mod tidy || true
#
# # Build Docker image
# docker build -t dcdot-control-plane:phase3 .
# kind load docker-image dcdot-control-plane:phase3 --name $KIND_CLUSTER
#
# # Also tag as latest
# docker tag dcdot-control-plane:phase3 dcdot-control-plane:latest
# kind load docker-image dcdot-control-plane:latest --name $KIND_CLUSTER
#
# cd ..
#
# # Rebuild CLI with freeze commands
# echo ""
# echo "Rebuilding CLI with freeze commands..."
# cd tracery-cli
#
# # Extract proto files from Docker image if needed
# if [ ! -d "../control-plane/pb" ]; then
#     echo "Extracting proto files from Docker image..."
#     docker create --name temp dcdot-control-plane:phase3
#     docker cp temp:/app/pb ../control-plane/
#     docker rm temp
# fi
#
# go mod tidy
# go build -o tracery-cli main.go
# cd ..
#
# echo ""
# echo "======================================"
# echo "✅ Phase 3 Build Complete!"
# echo "======================================"
# echo ""
# echo "Built components:"
# echo "  - Control Plane with Freeze Coordinator"
# echo "  - Envoy Filter Manager"
# echo "  - Updated CLI with freeze commands"
