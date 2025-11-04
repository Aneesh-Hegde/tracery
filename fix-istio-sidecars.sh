#!/bin/bash

set -e

echo "========================================================"
echo "Quick Fix: Istio Sidecar Issues"
echo "========================================================"
echo ""

# Colors
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# Step 1: Check current status
echo "Current pod status:"
kubectl get pods
echo ""

# Step 2: Most common issue - WASM filter problems
echo -e "${BLUE}Step 1: Removing potentially problematic WASM filters...${NC}"
kubectl delete wasmplugins --all 2>/dev/null || echo "No WasmPlugins to delete"
kubectl delete configmap freeze-filter-wasm 2>/dev/null || echo "No ConfigMap to delete"
echo -e "${GREEN}✅ Cleaned up WASM filters${NC}"
echo ""

# Step 3: Check for any EnvoyFilters
echo -e "${BLUE}Step 2: Checking for EnvoyFilters...${NC}"
ENVOY_FILTERS=$(kubectl get envoyfilters 2>/dev/null | grep -v NAME | wc -l)
if [ "$ENVOY_FILTERS" -gt 0 ]; then
  echo "Found EnvoyFilters, removing them..."
  kubectl delete envoyfilters --all
  echo -e "${GREEN}✅ Cleaned up EnvoyFilters${NC}"
else
  echo "No EnvoyFilters found"
fi
echo ""

# Step 4: Restart all services
echo -e "${BLUE}Step 3: Restarting all services...${NC}"
for deployment in service-a service-b service-c control-plane jaeger otel-collector postgres; do
  if kubectl get deployment $deployment &>/dev/null; then
    echo "Restarting $deployment..."
    kubectl rollout restart deployment $deployment
  fi
done
echo -e "${GREEN}✅ Restart initiated${NC}"
echo ""

# Step 5: Wait for rollouts
echo -e "${BLUE}Step 4: Waiting for rollouts to complete...${NC}"
echo "This may take 1-2 minutes..."
echo ""

for deployment in service-a service-b service-c; do
  if kubectl get deployment $deployment &>/dev/null; then
    echo "Waiting for $deployment..."
    kubectl rollout status deployment $deployment --timeout=120s || echo "Timeout waiting for $deployment"
  fi
done
echo ""

# Step 6: Check new status
echo -e "${BLUE}Step 5: Checking new pod status...${NC}"
kubectl get pods
echo ""

# Step 7: Verify readiness
echo -e "${BLUE}Step 6: Verifying pod readiness...${NC}"
READY_COUNT=$(kubectl get pods -l app=service-a -o jsonpath='{.items[0].status.containerStatuses[*].ready}' | grep -o "true" | wc -l)

if [ "$READY_COUNT" -eq 2 ]; then
  echo -e "${GREEN}✅ Service-a is fully ready (2/2)${NC}"
else
  echo -e "${YELLOW}⚠️  Service-a containers ready: $READY_COUNT/2${NC}"
  echo ""
  echo "Checking logs for issues..."
  POD=$(kubectl get pod -l app=service-a -o jsonpath='{.items[0].metadata.name}')
  kubectl logs $POD -c istio-proxy --tail=20
fi

echo ""
echo "========================================================"
echo "Summary"
echo "========================================================"
echo ""

ALL_READY=true
for app in service-a service-b service-c control-plane; do
  if kubectl get pods -l app=$app &>/dev/null; then
    POD=$(kubectl get pod -l app=$app -o jsonpath='{.items[0].metadata.name}')
    STATUS=$(kubectl get pod $POD -o jsonpath='{.status.containerStatuses[*].ready}' | grep -o "true" | wc -l)
    TOTAL=$(kubectl get pod $POD -o jsonpath='{.status.containerStatuses[*].name}' | wc -w)

    if [ "$STATUS" -eq "$TOTAL" ]; then
      echo -e "${GREEN}✅ $app: Ready ($STATUS/$TOTAL)${NC}"
    else
      echo -e "${RED}❌ $app: Not Ready ($STATUS/$TOTAL)${NC}"
      ALL_READY=false
    fi
  fi
done

echo ""

if [ "$ALL_READY" = true ]; then
  echo -e "${GREEN}🎉 All pods are ready!${NC}"
  echo ""
  echo "You can now proceed with testing:"
  echo "  ./test-freeze-mechanism.sh"
else
  echo -e "${YELLOW}⚠️  Some pods are still not ready${NC}"
  echo ""
  echo "Next steps to diagnose:"
  echo ""
  echo "1. Run detailed diagnostics:"
  echo "   ./diagnose-istio-sidecar.sh"
  echo ""
  echo "2. Check specific pod logs:"
  echo "   POD=\$(kubectl get pod -l app=service-a -o jsonpath='{.items[0].metadata.name}')"
  echo "   kubectl logs \$POD -c istio-proxy"
  echo "   kubectl describe pod \$POD"
  echo ""
  echo "3. Check Istio installation:"
  echo "   kubectl get pods -n istio-system"
  echo ""
  echo "4. If still failing, reinstall Istio:"
  echo "   kubectl delete namespace istio-system"
  echo "   ./install-istio.sh"
fi

echo ""
echo "========================================================"
