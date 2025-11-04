#!/bin/bash

echo "========================================================"
echo "Detailed Pod Status Check"
echo "========================================================"
echo ""

POD=$(kubectl get pod -l app=service-a -o jsonpath='{.items[0].metadata.name}')

echo "Checking pod: $POD"
echo ""

# Container statuses
echo "1. Container Ready Status:"
kubectl get pod $POD -o jsonpath='{range .status.containerStatuses[*]}{.name}{": ready="}{.ready}{", restartCount="}{.restartCount}{"\n"}{end}'
echo ""

# Container states
echo "2. Container States:"
kubectl get pod $POD -o jsonpath='{range .status.containerStatuses[*]}{.name}{": "}{.state}{"\n"}{end}' | jq .
echo ""

# Check istio-proxy specifically
echo "3. Istio-Proxy Container Details:"
kubectl get pod $POD -o json | jq '.status.containerStatuses[] | select(.name=="istio-proxy")'
echo ""

# Last 30 lines of istio-proxy logs
echo "4. Last 30 lines of istio-proxy logs:"
kubectl logs $POD -c istio-proxy --tail=30
echo ""

# Check for crash loop
echo "5. Checking for crash loops:"
RESTARTS=$(kubectl get pod $POD -o jsonpath='{.status.containerStatuses[?(@.name=="istio-proxy")].restartCount}')
echo "Istio-proxy restarts: $RESTARTS"

if [ "$RESTARTS" -gt 20 ]; then
  echo "⚠️  High restart count detected!"
  echo ""
  echo "Checking previous logs:"
  kubectl logs $POD -c istio-proxy --previous --tail=50 2>/dev/null || echo "No previous logs available"
fi

echo ""
echo "6. Pod Events:"
kubectl get events --field-selector involvedObject.name=$POD --sort-by='.lastTimestamp' | tail -5

echo ""
echo "========================================================"
echo "Quick Diagnosis"
echo "========================================================"

# Check readiness probe
echo ""
echo "Testing readiness probe:"
kubectl exec $POD -c istio-proxy -- curl -s -w "\nHTTP_CODE: %{http_code}\n" http://localhost:15021/healthz/ready 2>&1 || echo "Cannot reach readiness endpoint"

echo ""
echo "Checking Envoy admin port:"
kubectl exec $POD -c istio-proxy -- curl -s http://localhost:15000/server_info 2>&1 | head -10 || echo "Cannot reach admin port"
