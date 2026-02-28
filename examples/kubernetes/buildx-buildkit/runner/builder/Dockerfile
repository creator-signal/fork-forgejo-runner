FROM docker.io/node:24-alpine

# Not needed to install buildctl but could be useful to have it in the runner for debugging purposes
COPY --from=docker.io/moby/buildkit:v0.27.1 /usr/bin/buildctl /usr/bin/buildctl
# Mandatory to have the docker CLI in the runner to be able to use buildx with the BuildKit daemon deployed in the Kubernetes cluster
COPY --from=docker.io/docker:29.2.1-cli /usr/local/bin/docker /usr/local/bin/docker
COPY --from=docker.io/docker:29.2.1-cli /usr/local/libexec/docker/cli-plugins/docker-buildx /usr/local/libexec/docker/cli-plugins/docker-buildx
