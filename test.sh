#!/bin/bash

echo "======================================"
echo "Testing DCDOT Microservices"
echo "======================================"

# Wait for services to be fully ready
echo ""
echo "Ensuring all services are ready..."
sleep 5

# Test 1: Single request
echo ""
echo "Test 1: Sending single order request..."
RESPONSE=$(curl -s -X POST http://localhost:30080/order \
  -H 'Content-Type: application/json' \
  -d '{
    "order_id": "ORD-001",
    "customer_id": "CUST-123",
    "amount": 99.99
  }')

echo "Response:"
echo $RESPONSE | jq .

# Extract trace ID from response
TRACE_ID=$(echo $RESPONSE | jq -r .trace_id)
echo ""
echo "✅ Trace ID: $TRACE_ID"

# Test 2: Multiple concurrent requests
echo ""
echo "Test 2: Sending 5 concurrent requests..."
for i in {1..5}; do
  curl -s -X POST http://localhost:30080/order \
    -H 'Content-Type: application/json' \
    -d "{
      \"order_id\": \"ORD-00$i\",
      \"customer_id\": \"CUST-$((100 + i))\",
      \"amount\": $((50 + i * 10))
    }" > /dev/null &
done

wait

echo "✅ All requests sent"

# Test 3: Check database
echo ""
echo "Test 3: Verifying database records..."
kubectl exec -it $(kubectl get pod -l app=postgres -o jsonpath='{.items[0].metadata.name}') -- \
  psql -U dcdot -d payments -c "SELECT COUNT(*) as payment_count FROM payments;"

echo ""
echo "======================================"
echo "✅ Testing Complete!"
echo "======================================"
echo ""
echo "Next steps:"
echo "1. Open Jaeger UI: http://localhost:30686"
echo "2. Select 'service-a-gateway' from the Service dropdown"
echo "3. Click 'Find Traces' to see your request traces"
echo "4. Click on any trace to see the full request flow through all 3 services"
echo ""
echo "You should see:"
echo "  - Complete trace spanning service-a → service-b → service-c"
echo "  - Consistent trace ID across all spans"
echo "  - Timing information for each operation"
echo "  - Service dependencies visualized"
echo ""

echo "======================================"
echo "Testing Phase 2: Control Plane"
echo "======================================"

# Test 1: Health check
echo ""
echo "Test 1: Control Plane health check..."
curl -s http://localhost:30081/health | jq .

# Test 2: Register a breakpoint
echo ""
echo "Test 2: Registering breakpoint for service-a-gateway..."
BP_RESPONSE=$(curl -s -X POST http://localhost:30081/breakpoint/register \
  -H 'Content-Type: application/json' \
  -d '{
    "service_name": "service-a-gateway",
    "endpoint": "/order",
    "conditions": {}
  }')

echo $BP_RESPONSE | jq .
BP_ID=$(echo $BP_RESPONSE | jq -r .breakpoint_id)

# Test 3: Register conditional breakpoint
echo ""
echo "Test 3: Registering conditional breakpoint for service-b..."
curl -s -X POST http://localhost:30081/breakpoint/register \
  -H 'Content-Type: application/json' \
  -d '{
    "service_name": "service-b-processing",
    "endpoint": "/process",
    "conditions": {
      "customer_id": "CUST-123"
    }
  }' | jq .

# Test 4: List breakpoints
echo ""
echo "Test 4: Listing all breakpoints..."
curl -s http://localhost:30081/breakpoint/list | jq .

# Test 5: Delete breakpoint
echo ""
echo "Test 5: Deleting breakpoint $BP_ID..."
curl -s -X DELETE "http://localhost:30081/breakpoint/delete?id=$BP_ID" | jq .

# Test 6: Verify deletion
echo ""
echo "Test 6: Listing breakpoints after deletion..."
curl -s http://localhost:30081/breakpoint/list | jq .

# Test 7: Check Istio sidecars
echo ""
echo "Test 7: Verifying Istio sidecars are injected..."
kubectl get pods -o custom-columns=NAME:.metadata.name,READY:.status.containerStatuses[*].ready

echo ""
echo "======================================"
echo "✅ Phase 2 Testing Complete!"
echo "======================================"
