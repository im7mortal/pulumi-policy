#!/bin/bash

CONTAINER_NAME="integration_test"
IMAGE_NAME="policy_integration_test:latest"
COMMAND="docker"

while [[ "$#" -gt 0 ]]; do
    case $1 in
        --podman) COMMAND="podman"; shift ;;
        *) echo "Unknown parameter passed: $1"; exit 1 ;;
    esac
done

# Function to check if the container exists (running or stopped)
is_container_existing() {
    [ "$($COMMAND ps -a -q -f name=$CONTAINER_NAME)" ]
}

# Check if the container exists
if is_container_existing; then
    echo "Stopping and removing existing $CONTAINER_NAME container..."
    $COMMAND kill $CONTAINER_NAME

    # Wait for the container to be removed
    while is_container_existing; do
        sleep 1
    done
fi

# Build the new image and run the container
$COMMAND build -t $IMAGE_NAME -f Dockerfile ../..  && \
$COMMAND run --rm --init --name $CONTAINER_NAME $IMAGE_NAME
