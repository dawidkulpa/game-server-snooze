# syntax=docker/dockerfile:1.29@sha256:572cebbc2b15e6f84c077742952674889cb63cd5e9b80abdffcbe587b6f19183

FROM --platform=$BUILDPLATFORM golang:1.27.4-alpine3.23@sha256:146e3b9c7c1e2ec3d3f5348c8a686151a75e1976e50efac9fc022ebe446a9a82 AS builder
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
