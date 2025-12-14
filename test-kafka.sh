#!/bin/bash
set -e

CONTAINER_NAME="warp-test-kafka"
KAFKA_PORT="9092"

cleanup() {
    echo "Stopping Kafka container..."
    podman stop $CONTAINER_NAME > /dev/null 2>&1 || true
}
trap cleanup EXIT

echo "Starting Redpanda (Kafka compatible) container..."
podman rm -f $CONTAINER_NAME > /dev/null 2>&1 || true
podman run --rm -d -p $KAFKA_PORT:9092 --name $CONTAINER_NAME docker.io/redpandadata/redpanda:latest redpanda start --mode dev-container

echo "Waiting for Redpanda to be ready..."
for i in {1..60}; do
    if podman logs $CONTAINER_NAME 2>&1 | grep -E "Initialized cluster_id|Kafka API server listening"; then
       echo "Redpanda is ready!"
       break
    fi
    echo "Waiting for Redpanda..."
    sleep 1
done
sleep 2

echo "Running tests against real Kafka (Redpanda)..."
export WARP_TEST_KAFKA_ADDR="localhost:$KAFKA_PORT"
export WARP_TEST_FORCE_REAL="true"

go test -v ./v1/syncbus/... -run TestKafka

echo "Tests completed successfully."
