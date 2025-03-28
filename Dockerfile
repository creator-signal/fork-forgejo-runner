FROM --platform=$BUILDPLATFORM code.forgejo.org/oci/xx AS xx

FROM --platform=$BUILDPLATFORM code.forgejo.org/oci/golang:1.23-alpine3.20 AS build-env

#
# Transparently cross compile for the target platform
#
COPY --from=xx / /
ARG TARGETPLATFORM
RUN apk --no-cache add clang lld
RUN xx-apk --no-cache add gcc musl-dev
RUN xx-go --wrap

# Do not remove `git` here, it is required for getting runner version when executing `make build`
RUN apk add --no-cache build-base git

COPY . /srv
WORKDIR /srv

RUN make clean && make build

FROM code.forgejo.org/oci/alpine:3.20
ARG RELEASE_VERSION
RUN apk add --no-cache git bash

COPY --from=build-env /srv/forgejo-runner /bin/forgejo-runner

LABEL maintainer="contact@forgejo.org" \
      org.opencontainers.image.authors="Forgejo" \
      org.opencontainers.image.url="https://forgejo.org" \
      org.opencontainers.image.documentation="https://forgejo.org/docs/latest/admin/actions/#forgejo-runner" \
      org.opencontainers.image.source="https://code.forgejo.org/forgejo/runner" \
      org.opencontainers.image.version="${RELEASE_VERSION}" \
      org.opencontainers.image.vendor="Forgejo" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.title="Forgejo Runner" \
      org.opencontainers.image.description="A runner for Forgejo Actions."

ENV HOME=/data

USER 1000:1000

WORKDIR /data

VOLUME ["/data"]

CMD ["/bin/forgejo-runner"]
