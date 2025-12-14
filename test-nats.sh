#!/bin/bash
set -e

CONTAINER_NAME="warp-test-nats"
NATS_PORT="4222"

cleanup() {
    echo "Stopping NATS container..."
    podman stop $CONTAINER_NAME > /dev/null 2>&1 || true
}
trap cleanup EXIT

echo "Starting NATS container..."
podman rm -f $CONTAINER_NAME > /dev/null 2>&1 || true
podman run --rm -d -p $NATS_PORT:4222 --name $CONTAINER_NAME nats:alpine

echo "Waiting for NATS to be ready..."
for i in {1..30}; do
    if podman logs $CONTAINER_NAME 2>&1 | grep "Server is ready"; then
       echo "NATS is ready!"
       break
    fi
    echo "Waiting for NATS..."
    sleep 0.5
done

echo "Running tests against real NATS..."
export WARP_TEST_NATS_ADDR="nats://localhost:$NATS_PORT"
export WARP_TEST_FORCE_REAL="true"

# Run only NATS tests
go test -v ./v1/syncbus/... -run TestNATS

echo "Tests completed successfully."
