#!/bin/bash

CONTAINER_NAME="integration_test"
IMAGE_NAME="policy_integration_test:latest"
COMMAND="docker"

print_help() {
    echo "Usage: $0 [OPTIONS]"
    echo
    echo "Options:"
    echo "  --podman                   Use podman instead of docker"
    echo "  --pulumi VERSION           Set the Pulumi version for the build."
    echo "                             Docker tag from https://hub.docker.com/r/pulumi/pulumi/tags"
    echo "                             Default is 'latest'"
    echo "  --testprocs NUM            Set the number of concurrent test processes (GOMAXPROCS)"
    echo "                             Default is '12'"
    echo "  --timeout DURATION         Set the timeout duration."
    echo "                             Must be in Go time duration format (e.g., 30m, 2h)."
    echo "                             The default is '60m'."
    echo "  --runtimes RUNTIMES        Set the runtimes as a comma-separated string (e.g., 'go,python')"
    echo "                             Available runtimes: golang, python, nodejs, dotnet."
    echo "                             If not set, it runs all runtimes."
    echo "  --help                     Show this help message and exit"
}

# docker build args
BUILD_ARGS=()
append_arg() {
    BUILD_ARGS+=("--build-arg" "$1")
}

# docker run envs
RUN_ENVS=()
append_env() {
    RUN_ENVS+=("-e" "$1")
}

while [[ "$#" -gt 0 ]]; do
    case $1 in
        --podman) COMMAND="podman"; shift ;;
        --pulumi) append_arg "PULUMI_VERSION=$2"; shift 2 ;;
        --pulumictl) append_arg "PULUMICTL_TAG=$2"; shift 2 ;;
        --testprocs) append_env "GOMAXPROCS=$2"; shift 2 ;;
        --timeout) append_env "TEST_TIMEOUT=$2"; shift 2 ;;
        --runtimes) append_env "RUNTIMES=$2"; shift 2 ;;
        --help) print_help; exit 0 ;;
        *) print_help; exit 1 ;;
    esac
done

# Function to check if the container exists (running or stopped)
is_container_existing() {
    [ "$($COMMAND ps -a -q -f name=$CONTAINER_NAME)" ]
}

# TODO: For some reason, neither Ctrl+Z nor Ctrl+C kill the container (using --init didn't help).
# Check if the container exists and kill it
if is_container_existing; then
    echo "Stopping and removing existing $CONTAINER_NAME container..."
    $COMMAND kill $CONTAINER_NAME

    # Wait for the container to be removed
    while is_container_existing; do
        sleep 1
    done
fi

# Build the new image and run the container with the provided versions and arguments
$COMMAND build -t $IMAGE_NAME "${BUILD_ARGS[@]}" -f Dockerfile ../..  && \
$COMMAND run --rm --init --name $CONTAINER_NAME "${RUN_ENVS[@]}" $IMAGE_NAME
