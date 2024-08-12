FROM registry.hub.docker.com/pulumi/pulumi:latest

# override pulumi's entrypoint
ENTRYPOINT []

# Install pipenv
RUN apt-get update && \
    apt-get install -y python3-pip && \
    pip install pipenv && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

# install pulumictl
ENV PULUMICTL_TAG=master
WORKDIR /pulumictl
RUN git clone https://github.com/pulumi/pulumictl.git --branch $PULUMICTL_TAG . && \
    make install


# cache golang dependencies
WORKDIR /app/tests/integration
COPY tests/integration/go.mod .
COPY tests/integration/go.sum .
RUN go mod download

# TODO I know these steps are runned in environment.go but otherwise tests fails
# link @pulumi/policy locally to make it available for tests
WORKDIR /app/sdk/nodejs/policy
COPY sdk/nodejs/policy .
RUN yarn link
RUN yarn install


WORKDIR /app
# copy files required for python build separately during active development
#COPY .git .git
#COPY build build
#COPY sdk/python sdk/python
#COPY Makefile .
#COPY README.md .

# copy source (it shouldn't override any files from previous steps)
COPY . .

# install Python package
WORKDIR /app/sdk/python
RUN make ensure
RUN make build
RUN pipenv install --dev
RUN pipenv install

WORKDIR /app

#COPY tests tests

CMD ["make", "test_all"]
