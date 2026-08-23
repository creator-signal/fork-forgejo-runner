FROM --platform=$BUILDPLATFORM data.forgejo.org/oci/xx AS xx

FROM --platform=$BUILDPLATFORM data.forgejo.org/oci/golang:1.26-alpine3.23 AS build-env

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

FROM data.forgejo.org/oci/alpine:3.23
ARG RELEASE_VERSION
RUN apk add --no-cache git bash dumb-init

COPY --from=build-env /srv/forgejo-runner /bin/forgejo-runner

LABEL maintainer="contact@forgejo.org" \
      org.opencontainers.image.authors="Forgejo" \
      org.opencontainers.image.url="https://forgejo.org" \
      org.opencontainers.image.documentation="https://forgejo.org/docs/latest/admin/actions/#forgejo-runner" \
      org.opencontainers.image.source="https://code.forgejo.org/forgejo/runner" \
      org.opencontainers.image.version="${RELEASE_VERSION}" \
      org.opencontainers.image.vendor="Forgejo" \
      org.opencontainers.image.licenses="GPL-3.0-or-later" \
      org.opencontainers.image.title="Forgejo Runner" \
      org.opencontainers.image.description="A runner for Forgejo Actions."

ENV HOME=/data

USER 1000:1000

WORKDIR /data

VOLUME ["/data"]

# Run under dumb-init so that processes reparented to PID 1 are reaped instead of accumulating as zombies.  This is a
# risk when runner executes subcommands in the host-based runner that may escape their process group and not be
# `wait()'d` by their parents, or when subprocesses like `git` could be terminated during job cancellation and may leave
# grandchild processes that aren't reaped.
ENTRYPOINT ["/usr/bin/dumb-init"]
CMD ["/bin/forgejo-runner"]
