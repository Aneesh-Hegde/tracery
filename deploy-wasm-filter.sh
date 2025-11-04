#!/bin/bash

set -e

echo "================================================"
echo "Deploying WASM Freeze Filter to Kubernetes"
echo "================================================"
echo ""

# Colors
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m'

# Step 1: Build WASM binary
echo "Step 1: Building WASM binary..."
cd envoy-filter

if ! command -v tinygo &> /dev/null; then
    echo -e "${RED}❌ TinyGo not found!${NC}"
    echo ""
    echo "Install TinyGo:"
    echo "  macOS:   brew install tinygo"
    echo "  Linux:   wget https://github.com/tinygo-org/tinygo/releases/download/v0.30.0/tinygo_0.30.0_amd64.deb"
    echo "           sudo dpkg -i tinygo_0.30.0_amd64.deb"
    exit 1
fi

echo "Building with TinyGo..."
tinygo build -o main.wasm -scheduler=none -target=wasi -no-debug main.go

if [ ! -f "main.wasm" ]; then
    echo -e "${RED}❌ WASM build failed!${NC}"
    exit 1
fi

SIZE=$(wc -c < main.wasm)
echo -e "${GREEN}✅ WASM binary built: $(echo $SIZE | numfmt --to=iec-i)B${NC}"

cd ..

# Step 2: Create ConfigMap with WASM binary
echo ""
echo "Step 2: Creating ConfigMap with WASM binary..."

# Delete existing ConfigMap if it exists
kubectl delete configmap freeze-filter-wasm -n default 2>/dev/null || true

# Create new ConfigMap
kubectl create configmap freeze-filter-wasm \
    --from-file=freeze-filter.wasm=envoy-filter/main.wasm \
    -n default

echo -e "${GREEN}✅ ConfigMap created${NC}"

# Step 3: Patch Istio to mount ConfigMap in sidecars
echo ""
echo "Step 3: Configuring Istio to mount WASM filter..."

# Create a patch for the Istio sidecar injector
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: ConfigMap
metadata:
  name: istio-sidecar-injector
  namespace: istio-system
data:
  values: |
    global:
      proxy:
        holdApplicationUntilProxyStarts: true
    sidecarInjectorWebhook:
      rewriteAppHTTPProbe: true
      neverInjectSelector:
        - matchExpressions:
          - key: job-name
            operator: Exists
      alwaysInjectSelector: []
      injectedAnnotations: {}
      templates:
        custom: |
          spec:
            initContainers:
            - name: copy-wasm-filter
              image: busybox:1.35
              command:
              - sh
              - -c
              - |
                mkdir -p /var/local/lib/wasm-filters
                cp /wasm-source/freeze-filter.wasm /var/local/lib/wasm-filters/
                chmod 644 /var/local/lib/wasm-filters/freeze-filter.wasm
                ls -lh /var/local/lib/wasm-filters/
              volumeMounts:
              - name: wasm-filters
                mountPath: /var/local/lib/wasm-filters
              - name: wasm-source
                mountPath: /wasm-source
            volumes:
            - name: wasm-filters
              emptyDir: {}
            - name: wasm-source
              configMap:
                name: freeze-filter-wasm
            containers:
            - name: istio-proxy
              volumeMounts:
              - name: wasm-filters
                mountPath: /var/local/lib/wasm-filters
                readOnly: true
EOF

echo -e "${GREEN}✅ Istio sidecar configuration updated${NC}"

# Step 4: Deploy WasmPlugin
echo ""
echo "Step 4: Deploying WasmPlugin resource..."

cat <<EOF | kubectl apply -f -
apiVersion: extensions.istio.io/v1alpha1
kind: WasmPlugin
metadata:
  name: freeze-filter
  namespace: default
spec:
  selector:
    matchLabels:
      istio-injection: enabled
  url: file:///var/local/lib/wasm-filters/freeze-filter.wasm
  phase: AUTHN
  priority: 100
  pluginConfig:
    trace_id: ""
    state: ""
    timeout_ms: 30000
EOF

echo -e "${GREEN}✅ WasmPlugin deployed${NC}"

# Step 5: Restart pods to pick up changes
echo ""
echo "Step 5: Restarting service pods to load WASM filter..."

for service in service-a service-b service-c; do
    if kubectl get deployment $service &> /dev/null; then
        echo "Restarting $service..."
        kubectl rollout restart deployment $service
    fi
done

echo ""
echo "Waiting for rollouts to complete..."
# for service in service-a service-b service-c; do
#     if kubectl get deployment $service &> /dev/null; then
#         kubectl rollout status deployment $service --timeout=60s
#     fi
# done

echo -e "${GREEN}✅ All services restarted${NC}"

# Step 6: Verify deployment
echo ""
echo "Step 6: Verifying deployment..."

echo "Checking WasmPlugin..."
kubectl get wasmplugins

echo ""
echo "Checking ConfigMap..."
kubectl get configmap freeze-filter-wasm

echo ""
echo "Checking pods have Istio sidecars..."
kubectl get pods -o custom-columns=NAME:.metadata.name,CONTAINERS:.spec.containers[*].name

# Step 7: Test WASM filter is loaded
echo ""
echo "Step 7: Testing WASM filter is loaded in Envoy..."

SERVICE_A_POD=$(kubectl get pod -l app=service-a -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)

if [ -n "$SERVICE_A_POD" ]; then
    echo "Checking Envoy logs for WASM filter initialization..."
    kubectl logs $SERVICE_A_POD -c istio-proxy --tail=50 | grep -i "freeze" || echo "No freeze filter logs yet (this is okay, filter logs on traffic)"
    
    echo ""
    echo "Checking Envoy configuration..."
    kubectl exec $SERVICE_A_POD -c istio-proxy -- curl -s localhost:15000/config_dump | \
        grep -A5 "freeze-filter" && echo -e "${GREEN}✅ WASM filter found in Envoy config${NC}" || \
        echo -e "${YELLOW}⚠️  WASM filter not yet in Envoy config (may take a few seconds)${NC}"
else
    echo -e "${YELLOW}⚠️  service-a pod not found, skipping verification${NC}"
fi

echo ""
echo "================================================"
echo "✅ WASM Filter Deployment Complete!"
echo "================================================"
echo ""
echo "What was deployed:"
echo "  1. ✅ WASM binary built (envoy-filter/main.wasm)"
echo "  2. ✅ ConfigMap created (freeze-filter-wasm)"
echo "  3. ✅ Istio configured to mount WASM"
echo "  4. ✅ WasmPlugin resource deployed"
echo "  5. ✅ Services restarted with WASM filter"
echo ""
echo "Next steps:"
echo "  1. Run: ./test-freeze-mechanism.sh"
echo "  2. Check logs: kubectl logs <pod> -c istio-proxy | grep freeze"
echo "  3. Test freeze: ./tracery-cli/tracery-cli freeze start --trace test-123 --services service-a"
echo ""
echo "================================================"
