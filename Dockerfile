FROM golang:1.21-alpine3.18 as builder
# Do not remove `git` here, it is required for getting runner version when executing `make build`
RUN apk add --no-cache make git

COPY . /srv
WORKDIR /srv

RUN make clean && make build

FROM alpine:3.18
RUN apk add --no-cache git bash tini

COPY --from=builder /srv/forgejo-runner /usr/local/bin/forgejo-runner
COPY scripts/run.sh /opt/act/run.sh

ENTRYPOINT ["/sbin/tini","--","/opt/act/run.sh"]
