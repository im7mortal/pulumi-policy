ARG PULUMI_VERSION="latest"
FROM registry.hub.docker.com/pulumi/pulumi-provider-build-environment:$PULUMI_VERSION

# Override Pulumi's default entrypoint
ENTRYPOINT []

# Install pipenv for Python dependency management
RUN pip install pipenv

WORKDIR /app

COPY . .

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
