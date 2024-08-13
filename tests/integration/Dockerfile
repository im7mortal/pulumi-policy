FROM registry.hub.docker.com/pulumi/pulumi:latest

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

#RUN pulumictl get version --language javascript; exit 1



# For Node.js test_all target
RUN yarn add istanbul

# Cache Golang dependencies
WORKDIR /app/tests/integration
COPY tests/integration/go.mod .
COPY tests/integration/go.sum .
RUN go mod download

# Switch to the main application directory
WORKDIR /app

# Copy files which are not required active for development
#COPY .git .git
#COPY build build
#COPY sdk/python sdk/python
#COPY sdk/nodejs sdk/nodejs
#COPY Makefile .
#COPY README.md .

COPY . .

RUN git config --global user.name "Random User"
RUN git config --global user.email "random.user@example.com"
RUN git add -A
RUN git commit -m "integration_test"
RUN git tag "v1.2345.345"

# copied from github actions just in case
ENV GO111MODULE="on"
ENV PROVIDER="policy"
ENV VERSION="v1.0.0"

# TODO I know these steps are runned in environment.go but otherwise tests fails
# Link @pulumi/policy locally to make it available for tests
WORKDIR /app/sdk/nodejs/policy
RUN make build
RUN make test_all
RUN yarn link

# Install Python package
WORKDIR /app/sdk/python
RUN make ensure
RUN make build
RUN make test_all
RUN pipenv install --dev
RUN pipenv install

# Switch back to the application root directory
WORKDIR /app

#COPY tests tests

# Execute the test_all command
CMD ["make", "test_all"]
