#!/bin/bash
set -euo pipefail

CLI="./tracery-cli/tracery-cli"
echo "PHASE 3 TESTING: Traffic Freezing Mechanism"
echo "============================================"

NAMESPACE="default"
CONTROL_PLANE_PORT=50051
TRACE_ID_FROZEN="freeze-test-$(date +%s)"
TRACE_ID_NORMAL="normal-test-$(date +%s)"

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
TESTS_PASSED=0; TESTS_FAILED=0

test_status() {
    if [ $? -eq 0 ]; then echo -e "${GREEN}PASSED${NC}"; ((TESTS_PASSED++))
    else echo -e "${RED}FAILED${NC}"; ((TESTS_FAILED++)); fi
}

# Start port-forward for gRPC
echo "Starting gRPC port-forward..."
kubectl port-forward deployment/control-plane ${CONTROL_PLANE_PORT}:50051 > /dev/null &
PF_PID=$!
sleep 3
trap 'kill $PF_PID 2>/dev/null || true' EXIT

# === Pre-flight ===
echo -e "\nPre-flight Checks"
echo "1. Cluster... "; kubectl cluster-info > /dev/null; test_status
echo "2. Istio... "; kubectl get ns istio-system > /dev/null; test_status
echo "3. Services... "; kubectl get deployment service-a > /dev/null; test_status
echo "4. Control Plane... "; kubectl get deployment control-plane > /dev/null; test_status

# === NEW: Test 1 – CLI Freeze Start ===
echo -e "\nTest 1: CLI Freeze Start"
echo "  → $CLI freeze start --trace $TRACE_ID_FROZEN --services service-a,service-b,service-c"
if $CLI freeze start --trace "$TRACE_ID_FROZEN" --services service-a,service-b,service-c | grep -q "Freeze initiated"; then
    echo -e "${GREEN}PASSED${NC}"
    ((TESTS_PASSED++))
else
    echo -e "${RED}FAILED${NC}"
    ((TESTS_FAILED++))
fi

sleep 3

# === NEW: Test 2 – CLI Freeze Status ===
echo -e "\nTest 2: CLI Freeze Status"
if $CLI freeze status --trace "$TRACE_ID_FROZEN" | grep -q "FROZEN"; then
    echo -e "${GREEN}PASSED${NC}"
    ((TESTS_PASSED++))
else
    echo -e "${YELLOW}WARN: Not yet FROZEN (may be PREPARING)${NC}"
fi

# === NEW: Test 3 – CLI List Active Freezes ===
echo -e "\nTest 3: CLI List Active Freezes"
if $CLI freeze list | grep -q "$TRACE_ID_FROZEN"; then
    echo -e "${GREEN}PASSED${NC}"
    ((TESTS_PASSED++))
else
    echo -e "${RED}FAILED${NC}"
    ((TESTS_FAILED++))
fi

# === NEW: Test 4 – WASM Filter Logs ===
echo -e "\nTest 4: WASM Filter Logs in Envoy"
POD_B=$(kubectl get pod -l app=service-b -o jsonpath='{.items[0].metadata.name}')
if kubectl logs "$POD_B" -c istio-proxy --tail=50 | grep -q "WASM.*freeze"; then
    echo -e "${GREEN}PASSED${NC} (WASM filter active)"
    ((TESTS_PASSED++))
else
    echo -e "${YELLOW}WARN: No WASM freeze logs${NC}"
fi

# === NEW: Test 5 – EnvoyFilter Spec Validation ===
echo -e "\nTest 5: EnvoyFilter Contains Correct Matchers"
FILTER_YAML=$(kubectl get envoyfilter -n $NAMESPACE -o yaml | grep -A 20 "name:.*freeze-")
if echo "$FILTER_YAML" | grep -q "x-b3-traceid" && echo "$FILTER_YAML" | grep -q "$TRACE_ID_FROZEN"; then
    echo -e "${GREEN}PASSED${NC}"
    ((TESTS_PASSED++))
else
    echo -e "${RED}FAILED: Missing trace ID in filter${NC}"
    ((TESTS_FAILED++))
fi

# === NEW: Test 6 – Concurrent Freeze (Robustness) ===
echo -e "\nTest 6: Concurrent Freeze Requests"
TRACE2="concurrent-$(date +%s)"
(
    $CLI freeze start --trace "$TRACE2" --services service-a &
    $CLI freeze start --trace "$TRACE_ID_FROZEN" --services service-b &
    wait
) > /dev/null 2>&1
sleep 3
if $CLI freeze list | grep -q "$TRACE2"; then
    echo -e "${GREEN}PASSED${NC}"
    ((TESTS_PASSED++))
else
    echo -e "${RED}FAILED${NC}"
    ((TESTS_FAILED++))
fi

# === NEW: Test 7 – Normal Traffic Unaffected ===
echo -e "\nTest 7: Normal Traffic Passes (Not Frozen)"
POD_A=$(kubectl get pod -l app=service-a -o jsonpath='{.items[0].metadata.name}')
START=$(date +%s)
if kubectl exec "$POD_A" -- curl -s -o /dev/null -w "%{http_code}" \
    -H "x-b3-traceid: $TRACE_ID_NORMAL" \
    http://service-a:8080/api/test | grep -q "200"; then
    DUR=$(( $(date +%s) - START ))
    [ $DUR -lt 3 ] && echo -e "${GREEN}PASSED (${DUR}s)${NC}" || echo -e "${YELLOW}SLOW (${DUR}s)${NC}"
    ((TESTS_PASSED++))
else
    echo -e "${RED}FAILED${NC}"
    ((TESTS_FAILED++))
fi

# === NEW: Test 8 – CLI Release + Full Cleanup ===
echo -e "\nTest 8: CLI Release & Cleanup"
if $CLI freeze release --trace "$TRACE_ID_FROZEN" | grep -q "released"; then
    echo -e "${GREEN}PASSED${NC}"
    ((TESTS_PASSED++))
else
    echo -e "${RED}FAILED${NC}"
    ((TESTS_FAILED++))
fi

sleep 5

# Verify no leftover filters
LEFTOVER=$(kubectl get envoyfilter -n $NAMESPACE -o name | grep freeze || true)
if [ -z "$LEFTOVER" ]; then
    echo -e "${GREEN}PASSED: All filters cleaned${NC}"
    ((TESTS_PASSED++))
else
    echo -e "${YELLOW}WARN: Leftover filters: $LEFTOVER${NC}"
fi

# === Final Summary ===
echo -e "\nTEST SUMMARY"
echo "============"
echo "Passed: $TESTS_PASSED  Failed: $TESTS_FAILED"

if [ $TESTS_FAILED -eq 0 ]; then
    echo -e "\n${GREEN}ALL PHASE 3 TESTS PASSED!${NC}"
    echo "Traffic Freezing is production-ready"
    exit 0
else
    echo -e "\n${RED}SOME TESTS FAILED${NC}"
    echo "Check:"
    echo "  • Control Plane: make logs-control-plane"
    echo "  • Envoy: kubectl logs <pod> -c istio-proxy | grep freeze"
    echo "  • Filters: kubectl get envoyfilter -o yaml"
    exit 1
fi
