ARG PULUMI_VERSION="latest"
FROM registry.hub.docker.com/pulumi/pulumi:$PULUMI_VERSION

# Override Pulumi's default entrypoint
ENTRYPOINT []

# Install pipenv for Python dependency management
RUN apt-get update && \
    apt-get install -y python3-pip && \
    pip install pipenv && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

# Install pulumictl
ARG PULUMICTL_TAG=master
WORKDIR /pulumictl
RUN git clone https://github.com/pulumi/pulumictl.git --branch $PULUMICTL_TAG . && \
    make install

WORKDIR /app

COPY . .

# golang testing library use GOMAXPROCS to decide on number of parallel tests
ENV GOMAXPROCS=12

# TEST_TIMEOUT env set's timeout for `go test` in Makefile under `test_all` target
ENV TEST_TIMEOUT="30m"

CMD ["make", "ensure", "only_build", "only_test_fast", "test_all"]



## Following section for active development of integration tests runner
#COPY README.md .
## .git is required by build; but invalidates cache too often
#RUN git config --global user.email "you@example.com"; \
#    git config --global user.name "Your Name"; \
#    git init; git add README.md; git commit -m"i"; git tag "v0.0.1"
#COPY build build
#COPY scripts scripts
#COPY sdk sdk
#COPY Makefile .
#COPY LICENSE .
#COPY tests/integration/go.mod tests/integration/go.mod
#COPY tests/integration/go.sum tests/integration/go.sum
#RUN make ensure only_build only_test_fast
#COPY tests tests

#CMD ["make", "test_all"]
