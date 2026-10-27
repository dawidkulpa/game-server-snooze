# syntax=docker/dockerfile:1.28@sha256:bb22d9815c728170f72750f4e5b0d672e06176142e1d602c7e66c050100b7e5b

FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine3.23@sha256:8a5046497cda39860b832ffdbe8dd66018a0f05bb01352f533a36e7e2ced66a0 AS builder
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG VCS_REF=unknown
ENV CGO_ENABLED=0
WORKDIR /src
RUN apk add --no-cache ca-certificates=20261020-r0
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/root/.cache/go-build \
    GOOS=$TARGETOS GOARCH=$TARGETARCH go build \
    -trimpath -ldflags="-s -w -buildid=" \
    -o /out/game-server-snooze ./cmd

FROM scratch
ARG VERSION=dev
ARG VCS_REF=unknown
LABEL org.opencontainers.image.title="game-server-snooze" \
      org.opencontainers.image.description="Wake-on-demand UDP proxy for Pterodactyl-managed game servers" \
      org.opencontainers.image.source="https://github.com/dawidkulpa/game-server-snooze" \
      org.opencontainers.image.version=$VERSION \
      org.opencontainers.image.revision=$VCS_REF
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /out/game-server-snooze /game-server-snooze
WORKDIR /app
USER 65532:65532
EXPOSE 8211/udp 8080/tcp
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD ["/game-server-snooze", "healthcheck", "http://127.0.0.1:8080/healthz"]
ENTRYPOINT ["/game-server-snooze"]
