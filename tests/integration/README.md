# Integration Test Runner
This repository contains a script and a Dockerfile to automate integration tests for Pulumi projects.
The script can switch between Docker and Podman, and it supports customizing the Pulumi version.
Using Docker (or Podman) isolates the test environment from the host system, ensuring a clean,
consistent environment for each test run. This approach eliminates the need to install dependencies
locally and allows you to test against specific versions of Pulumi without affecting your host system.
Additionally, Docker ensures that the environment is reproducible, making it easier to run tests
consistently across different machines.
## Prerequisites
You do **not** need to have `Git`, `Python`, `NodeJS`, or `Make` installed on your host machine, as
these tools are included inside the container. The container will handle everything needed for
running the tests and building the project.
However, you **do need**:
- Podman or Docker: To build and run the container.
## Usage
You can control the test environment by passing options to the `run.sh` script.
### Options
- `--podman`: Use Podman instead of Docker.
- `--pulumi VERSION`: Set the Pulumi version for the build. Defaults to `latest`. Docker tags can be
  found [here](https://hub.docker.com/r/pulumi/pulumi/tags).
- `--testprocs NUM`: Set the number of concurrent test processes (`GOMAXPROCS`). Defaults to
  `12`.
  - `--timeout DURATION`: Set the test timeout duration. Must be in Go time duration format (e.g., 30m, 2h).
  Defaults to 60m.
- `--runtimes RUNTIMES`: Set the runtimes as a comma-separated string (e.g., `go,python`).
  Available runtimes: `golang`, `python`, `nodejs`, `dotnet`. If not set, it runs all runtimes.
- `--help`: Show usage information.
### Example
```bash
./run.sh --podman --runtimes python,nodejs --testprocs 8 --timeout 30m --pulumi 3.112.0
```
This example command:
- Uses Podman instead of Docker.
- Sets the runtimes to Python and Node.js.
- Limits the number of concurrent test processes to 8.
- Specifies Pulumi version.
## Dockerfile Explanation for usage without `run.sh`
The Dockerfile builds a custom Pulumi image with additional dependencies for integration tests.
### Key Sections
- **Pulumi Version**: The `PULUMI_VERSION` build argument sets the Pulumi version (default is
  `latest`).
- **Pipenv**: Installs `pipenv` for Python dependency management.
- **GOMAXPROCS**: Sets the environment variable `GOMAXPROCS` to manage the number of
  parallel tests for Go.
- **Commands**: By default, the container will run a sequence of Make commands: `ensure`,
  `only_build`, `only_test_fast`, and `test_all`.
### Building the Docker Image
```bash
docker build -t policy_integration_test:latest --build-arg PULUMI_VERSION=latest --build-arg
```
### Running the Container
```bash
docker run --rm --init --name integration_test -e GOMAXPROCS=12 -e RUNTIMES=go,nodejs
policy_integration_test:latest
```
This will run the integration tests inside the container with the specified environment variables.