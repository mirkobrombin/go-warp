#!/bin/bash
set -e

# Build the proxy
echo "Building warp-proxy..."
go build -o warp-proxy ./cmd/warp-proxy/main.go

# Start the proxy in the background
echo "Starting warp-proxy on port 6380..."
./warp-proxy -port 6380 &
PID=$!

# Ensure cleanup on exit
cleanup() {
    echo "Stopping warp-proxy (PID $PID)..."
    kill $PID
    wait $PID 2>/dev/null || true
    rm -f warp-proxy
}
trap cleanup EXIT

# Wait for proxy to be ready
sleep 1

# Helper to send RESP commands via nc
# Usage: send_resp "SET" "foo" "bar"
send_cmd() {
    # Construct RESP array
    local argc=$#
    local payload="*$argc\r\n"
    for arg in "$@"; do
        local len=${#arg}
        payload+="\$${len}\r\n${arg}\r\n"
    done
    echo -ne "$payload" | timeout 1 nc localhost 6380 || true
}

echo "Running tests..."

# Test 1: PING
echo "Test PING..."
RESPONSE=$(send_cmd "PING")
if [[ "$RESPONSE" == "+PONG"* ]]; then
    echo "PING passed"
else
    echo "PING failed: $RESPONSE"
    exit 1
fi

# Test 2: SET
echo "Test SET foo bar..."
RESPONSE=$(send_cmd "SET" "foo" "bar")
if [[ "$RESPONSE" == "+OK"* ]]; then
    echo "SET passed"
else
    echo "SET failed: $RESPONSE"
    exit 1
fi

# Test 3: GET
echo "Test GET foo..."
RESPONSE=$(send_cmd "GET" "foo")
# Expect bulk string: $3\r\nbar\r\n
if [[ "$RESPONSE" == *"\$3"* && "$RESPONSE" == *"bar"* ]]; then
    echo "GET passed"
else
    echo "GET failed: $RESPONSE"
    exit 1
fi

# Test 4: Unknown Command
echo "Test INVALID..."
RESPONSE=$(send_cmd "INVALID")
if [[ "$RESPONSE" == "-ERR unknown command"* ]]; then
    echo "Unknown command passed"
else
    echo "Unknown command failed: $RESPONSE"
    exit 1
fi

echo "All proxy tests passed!"
