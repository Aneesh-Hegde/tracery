#!/bin/bash
set -e

echo "Building WASM filter..."

# Install TinyGo if not present (required for WASM compilation)
if ! command -v tinygo &> /dev/null; then
    echo "TinyGo not found. Please install it from https://tinygo.org/getting-started/install/"
    exit 1
fi

# Build the WASM binary
tinygo build -o main.wasm -scheduler=none -target=wasi -no-debug main.go

# Verify the build
if [ -f "main.wasm" ]; then
    SIZE=$(wc -c < main.wasm)
    echo "✓ WASM binary built successfully: main.wasm (${SIZE} bytes)"
    
    # Check if it's under 1MB for ConfigMap deployment
    if [ $SIZE -lt 1048576 ]; then
        echo "✓ Binary is under 1MB, suitable for ConfigMap deployment"
    else
        echo "⚠ Binary is over 1MB, must use OCI image deployment"
    fi
else
    echo "✗ Build failed"
    exit 1
fi

echo ""
echo "Next steps:"
echo "1. Build Docker image: docker build -t your-registry/freeze-filter:v1 ."
echo "2. Push to registry: docker push your-registry/freeze-filter:v1"
echo "3. Update k8s/wasm-plugin.yaml with your image URL"
echo "4. Deploy: kubectl apply -f k8s/wasm-plugin.yaml"
