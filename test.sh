#!/bin/bash
# test.sh — simulates a full delivery flow
# Run this after starting all 3 services and Kafka

BASE_ORDER="http://localhost:8090"
BASE_LOCATION="http://localhost:8091"

echo "=== Creating order ==="
ORDER=$(curl -s -X POST $BASE_ORDER/orders \
  -H "Content-Type: application/json" \
  -d '{"customer_id": "cust-001", "agent_id": "agent-007"}')
echo $ORDER | python3 -m json.tool

ORDER_ID=$(echo $ORDER | python3 -c "import sys,json; print(json.load(sys.stdin)['order_id'])")
echo ""
echo "Order ID: $ORDER_ID"

echo ""
echo "=== Simulating 5 GPS pings from agent ==="
for i in 1 2 3 4 5; do
  LAT=$(python3 -c "print(round(12.9716 + $i * 0.001, 6))")
  LNG=$(python3 -c "print(round(77.5946 + $i * 0.001, 6))")
  echo "Ping $i: lat=$LAT lng=$LNG"
  curl -s -X POST $BASE_LOCATION/ping \
    -H "Content-Type: application/json" \
    -d "{\"agent_id\": \"agent-007\", \"order_id\": \"$ORDER_ID\", \"latitude\": $LAT, \"longitude\": $LNG}"
  sleep 1
done

echo ""
echo "=== Updating order status to picked_up ==="
curl -s -X PATCH $BASE_ORDER/orders/$ORDER_ID \
  -H "Content-Type: application/json" \
  -d '{"status": "picked_up"}' | python3 -m json.tool

echo ""
echo "=== Updating order status to delivered ==="
curl -s -X PATCH $BASE_ORDER/orders/$ORDER_ID \
  -H "Content-Type: application/json" \
  -d '{"status": "delivered"}' | python3 -m json.tool

echo ""
echo "=== Connect a WebSocket client to watch live updates ==="
echo "Run this in another terminal:"
echo "  wscat -c 'ws://localhost:8092/ws?order_id=$ORDER_ID'"
