#!/bin/bash
set -e

# Build the smoke test app
echo "Building smoke test app..."
go build -o smoke-app ./cmd/smoke-cluster/main.go

cleanup() {
    echo "Cleaning up processes..."
    kill $PID1 $PID2 2>/dev/null || true
    rm smoke-app
}
trap cleanup EXIT

# Clean up any leftover data
podman exec bench-redis redis-cli del smoke-key > /dev/null 2>&1 || true

echo "Starting Node A on :8081 (mesh :7951)..."
./smoke-app -port 8081 -mesh-port 7951 -peers 127.0.0.1:7952 &
PID1=$!

echo "Starting Node B on :8082 (mesh :7952)..."
./smoke-app -port 8082 -mesh-port 7952 -peers 127.0.0.1:7951 &
PID2=$!

# Wait for nodes to start and gossip
sleep 3

echo "Step 1: Get initial state from Node B (should be empty/404)"
curl -s "http://localhost:8082/get?key=smoke-key" || echo " (Node B says 404 - expected)"

echo -e "\nStep 2: Set value on Node A"
curl -s "http://localhost:8081/set?key=smoke-key&val=WarpSpeed"
echo " (Node A Set to WarpSpeed)"

sleep 1

echo "Step 3: Get value from Node B (should be WarpSpeed if invalidation/consistency worked)"
RESULT=$(curl -s "http://localhost:8082/get?key=smoke-key")
echo "Node B says: $RESULT"

if [ "$RESULT" == "WarpSpeed" ]; then
    echo "SUCCESS: Cluster is talking!"
else
    echo "FAILURE: Node B is out of sync"
    exit 1
fi

echo -e "\nStep 4: Set new value on Node B"
curl -s "http://localhost:8082/set?key=smoke-key&val=GoGoGo"
echo " (Node B Set to GoGoGo)"

sleep 1

echo "Step 5: Get value from Node A"
RESULT2=$(curl -s "http://localhost:8081/get?key=smoke-key")
echo "Node A says: $RESULT2"

if [ "$RESULT2" == "GoGoGo" ]; then
    echo "SUCCESS: Bi-directional communication verified!"
else
    echo "FAILURE: Node A is out of sync"
    exit 1
fi
