#!/bin/bash
set -e

CONTAINER_NAME="warp-test-redis"
REDIS_PORT="6379"

cleanup() {
    echo "Stopping Redis container..."
    podman stop $CONTAINER_NAME > /dev/null 2>&1 || true
}
trap cleanup EXIT

echo "Starting Redis container..."
podman rm -f $CONTAINER_NAME > /dev/null 2>&1 || true
podman run --rm -d -p $REDIS_PORT:6379 --name $CONTAINER_NAME redis:alpine

echo "Waiting for Redis to be ready..."
for i in {1..30}; do
    if podman exec $CONTAINER_NAME redis-cli ping | grep PONG > /dev/null 2>&1; then
        echo "Redis is ready!"
        break
    fi
    echo "Waiting for Redis..."
    sleep 0.5
done

echo "Running tests against real Redis..."
export WARP_TEST_REDIS_ADDR="localhost:$REDIS_PORT"
export WARP_TEST_FORCE_REAL=true
go test -v ./v1/adapter ./v1/syncbus -run Redis

echo "Tests completed successfully."
