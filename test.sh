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
