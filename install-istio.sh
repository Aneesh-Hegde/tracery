#!/bin/bash

set -e

echo "======================================"
echo "Installing Istio Service Mesh"
echo "======================================"

# Download Istio
echo ""
echo "Downloading Istio..."
curl -L https://istio.io/downloadIstio | ISTIO_VERSION=1.20.0 sh -

# Move to istio directory
cd istio-1.20.0

# Add istioctl to PATH for this session
export PATH=$PWD/bin:$PATH

# Install Istio with demo profile
echo ""
echo "Installing Istio to Kubernetes cluster..."
istioctl install --set profile=demo -y

# Enable automatic sidecar injection for default namespace
echo ""
echo "Enabling automatic sidecar injection..."
kubectl label namespace default istio-injection=enabled --overwrite

# Verify installation
echo ""
echo "Verifying Istio installation..."
kubectl get pods -n istio-system

echo ""
echo "======================================"
echo "✅ Istio installed successfully!"
echo "======================================"
echo ""
echo "Istio components:"
kubectl get svc -n istio-system

cd ..

echo ""
echo "Note: You may need to add istioctl to your PATH:"
echo "  export PATH=\$PWD/istio-1.20.0/bin:\$PATH"
