#!/bin/bash
set -e

echo "Starting Benchmark infrastructure..."

# Cleanup previous runs
podman rm -f bench-redis bench-dragonfly 2>/dev/null || true

# Start Redis
echo "Starting Redis on :6379..."
podman run -d --name bench-redis -p 6379:6379 redis:alpine

# Start DragonFly
echo "Starting DragonFly on :6380..."
podman run -d --name bench-dragonfly -p 6380:6379 docker.dragonflydb.io/dragonflydb/dragonfly --proactor_threads=1

# Wait for healthy
sleep 3

echo "Running benchmarks..."
# Build Go benchmark tool
go build -o bench-tool cmd/bench/main.go

# Run Go benchmarks first
./bench-tool -target warp-local
./bench-tool -target warp-mesh
./bench-tool -target ristretto
./bench-tool -target warp-redis
./bench-tool -target redis
./bench-tool -target dragonfly

# Run Polyglot benchmarks against Warp Proxy (built and running)
echo "Building and starting Warp Proxy for Polyglot tests..."
go build -o warp-proxy ./cmd/warp-proxy
./warp-proxy -port 6381 &
PROXY_PID=$!
sleep 2

echo "--- Node.js Benchmark ---"
if command -v npm >/dev/null; then
    pushd cmd/bench/node >/dev/null
    if [ ! -d "node_modules" ]; then
        npm install redis >/dev/null 2>&1
    fi
    node bench.js
    popd >/dev/null
else
    echo "Node.js not found, skipping."
fi

echo "--- Python Benchmark ---"
if command -v python3 >/dev/null; then
    # Ensure redis is installed
    pip install redis >/dev/null 2>&1 || true
    python3 cmd/bench/python/bench.py
else
    echo "Python3 not found, skipping."
fi

kill $PROXY_PID

echo "Cleaning up..."
podman rm -f bench-redis bench-dragonfly
