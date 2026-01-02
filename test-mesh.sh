#!/bin/bash
set -e

# Build the mesh-test tool
echo "Building mesh-test tool..."
go build -o mesh-test ./cmd/mesh-test/main.go

cleanup() {
    echo "Cleaning up processes..."
    kill $(jobs -p) 2>/dev/null || true
    rm -f mesh-test
}
trap cleanup EXIT

NODE_COUNT=5
PORT=7946

echo "Starting $NODE_COUNT Mesh nodes..."

# Start Node 0 (Publisher)
./mesh-test -id 0 -port $PORT -adv "127.0.0.1:$PORT" > node_0.log 2>&1 &
echo "Started Node 0 (Publisher) on port $PORT"

# Wait for Node 0 to start
sleep 1

# Start other nodes (Subscribers)
for i in $(seq 1 $((NODE_COUNT-1))); do
    NODE_PORT=$((PORT + i))
    ./mesh-test -id $i -port $NODE_PORT -adv "127.0.0.1:$NODE_PORT" -peer "127.0.0.1:$PORT" > node_$i.log 2>&1 &
    echo "Started Node $i (Subscriber) on port $NODE_PORT"
done

echo "Waiting for propagation (15 seconds)..."
sleep 15

echo "Checking logs for received values..."
SUCCESS=true
for i in $(seq 1 $((NODE_COUNT-1))); do
    if grep -q "Received Invalidation" node_$i.log; then
        echo "Node $i: OK (received invalidations)"
    else
        echo "Node $i: FAIL (no updates found in node_$i.log)"
        echo "Last 10 lines of node_$i.log:"
        tail -n 10 node_$i.log
        SUCCESS=false
    fi
done

if [ "$SUCCESS" = true ]; then
    echo "Warp Mesh integration test PASSED!"
    rm -f node_*.log
else
    echo "Warp Mesh integration test FAILED!"
    # Don't delete logs if failed
    exit 1
fi
