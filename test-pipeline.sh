#!/bin/bash
set -e

# Start proxy if not running (assuming test-proxy.sh stopped it)
echo "Building warp-proxy..."
go build -o warp-proxy ./cmd/warp-proxy/main.go
./warp-proxy -port 6381 &
PID=$!

cleanup() {
    kill $PID
    wait $PID 2>/dev/null || true
    rm -f warp-proxy
}
trap cleanup EXIT

sleep 1

echo "Testing Pipelining..."
# Send 2 PINGs in one go. PING as array: *1\r\n$4\r\nPING\r\n
CMD="*1\r\n\$4\r\nPING\r\n"
PAYLOAD="${CMD}${CMD}"

RESPONSE=$(echo -ne "$PAYLOAD" | timeout 1 nc localhost 6381 || true)

# Expected: +PONG\r\n+PONG\r\n
# Count occurrences of PONG
COUNT=$(echo "$RESPONSE" | grep -c "PONG")

if [ "$COUNT" -eq 2 ]; then
    echo "Pipelining passed: Received 2 PONGs"
else
    echo "Pipelining failed: Received $COUNT PONGs"
    echo "Response: $RESPONSE"
    exit 1
fi
