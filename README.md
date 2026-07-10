# Game Server Snooze

`game-server-snooze` is a lightweight UDP proxy for a Pterodactyl-managed Palworld dedicated server. It keeps the expensive backend stopped when no sessions are active, starts it on an accepted wake packet, buffers startup traffic until the backend is ready, and stops it only after every observed flow has been idle for the configured delay.

## Lifecycle

1. The proxy binds its public UDP listener immediately.
2. While Pterodactyl reports the backend offline, only a packet matching the configured Palworld wake policy can create a session.
3. One Pterodactyl start request is shared by all accepted startup flows. A failed status query never triggers a power request; only a later confirmed `offline` state may start the server. Permanent panel/auth/schema failures apply a global five-second wake cooldown so unauthenticated UDP cannot generate one API request per packet.
4. Client datagrams remain in bounded per-session and global buffers.
5. By default, the proxy waits for both Pterodactyl `running` and a successful Steam A2S query before releasing buffered datagrams. A settle delay is applied afterward.
6. Each client `IP:port` owns a connected upstream UDP socket. Backend replies can therefore return only from the configured backend endpoint.
7. Idle sessions close their sockets and reader goroutines. When the final session expires, a generation-safe timer starts. A new session invalidates that timer before a stop can be sent.

The proxy uses observed UDP traffic, not player identities or Palworld REST/RCON, to decide when a session is idle. Keep the backend gameplay port private so players cannot bypass the proxy.

## Configuration

Configuration is read from `config.yaml` in the process working directory. Environment variables override YAML values. Invalid addresses, durations, limits, policies, or required Pterodactyl values stop startup with an explicit error.

| Environment variable | Purpose | Default |
|---|---|---:|
| `LISTEN_ADDR` | Public UDP listen address | `:8211` |
| `SERVER_ADDR` | Palworld gameplay backend | required |
| `MAX_SESSIONS` | Maximum pending and active client flows | `32` |
| `MAX_PACKET_SIZE` | Maximum accepted UDP payload, at most 65507 | `65507` |
| `GAME` | Game module; currently `palworld` | `palworld` |
| `IDLE_TIMEOUT` | Inactivity before a ready flow is closed; pending startup flows remain bounded by `STARTUP_TIMEOUT` | `30s` |
| `AUTO_STOP_DELAY` | Zero-session delay before Pterodactyl stop | `1m` |
| `STARTUP_TIMEOUT` | Total Pterodactyl/readiness startup deadline | `5m` |
| `STARTUP_POLL_INTERVAL` | Pterodactyl and A2S polling interval | `2s` |
| `STARTUP_SETTLE_DELAY` | Additional delay after application readiness | `5s` |
| `STARTUP_BUFFER_PACKETS` | Packet limit per pending flow | `32` |
| `STARTUP_BUFFER_BYTES_PER_SESSION` | Byte limit per pending flow; must be at least `MAX_PACKET_SIZE` | `262144` |
| `STARTUP_BUFFER_BYTES_GLOBAL` | Byte limit across all pending flows; must hold at least one maximum packet | `4194304` |
| `BACKEND_READINESS_MODE` | `a2s` or weaker `delay` compatibility mode | `a2s` |
| `BACKEND_READINESS_ADDR` | Steam A2S endpoint; empty derives backend host with port 27015 | empty |
| `WAKE_POLICY` | `signature` for production or diagnostic `any` | `signature` |
| `WAKE_SIGNATURES` | Comma-separated hexadecimal packet prefixes | historical Palworld prefix |
| `LOG_UNMATCHED_PREFIXES` | Rate-limited unmatched prefix diagnostics | `false` |
| `DIAGNOSTIC_PREFIX_BYTES` | Maximum bytes included in a diagnostic | `8` |
| `HEALTH_ADDR` | Local HTTP health listener | `127.0.0.1:8080` |
| `PTERO_BASE_URL` | HTTPS Pterodactyl panel URL; plaintext HTTP is accepted only on loopback for tests | required |
| `PTERO_API_TOKEN` | Pterodactyl Client API token | required |
| `PTERO_SERVER_ID` | Pterodactyl server identifier | required |
| `LOG_LEVEL` | `debug`, `info`, `warn`, `error`, or `fatal` | `info` |

`PTERO_API_TOKEN` remains environment-based for compatibility. Do not commit a real token to this public repository or print it in deployment output.

### Palworld readiness

The default `a2s` mode sends the standard Steam `A2S_INFO` query to the Palworld query endpoint. It handles challenge responses and opens the startup gate only after an information response is received. This does not require or expose Palworld REST (`8212`) or RCON (`25575`). If the server uses a non-default query port, set `BACKEND_READINESS_ADDR` explicitly.

`delay` mode relies on Pterodactyl `running` plus `STARTUP_SETTLE_DELAY`. It is available for installations without A2S but cannot prove that Palworld has initialized its gameplay service.

### Wake compatibility

The bundled signature is retained for backward compatibility but has not yet been proven against every current Palworld build. Production should use `WAKE_POLICY=signature`.

For an attended compatibility test, enable `LOG_UNMATCHED_PREFIXES=true`. Diagnostics contain only a bounded prefix and packet length and are rate-limited. `WAKE_POLICY=any` can be used temporarily to determine whether the rest of the proxy path works, but arbitrary public UDP traffic can then wake the backend and it should not be left enabled without an explicit risk decision.

See [`config.example.yaml`](config.example.yaml) for the complete YAML form.

## Health endpoints

- `GET /healthz` returns `200 ok` while the process and health server are alive.
- `GET /readyz` returns `200 ready` after configuration validation and the UDP listener has opened.

Readiness does not depend on Pterodactyl availability or on the intentionally stopped Palworld backend. The container image uses the binary's built-in healthcheck command, so the final image needs no shell or HTTP utility.

## Docker

Published images support `linux/amd64` and `linux/arm64`, run as numeric user `65532:65532`, and include trusted CA certificates for Pterodactyl HTTPS.

```bash
docker run --rm \
  -p 8211:8211/udp \
  -e LISTEN_ADDR=:8211 \
  -e SERVER_ADDR=192.0.2.10:8211 \
  -e BACKEND_READINESS_ADDR=192.0.2.10:27015 \
  -e PTERO_BASE_URL=https://panel.example.com \
  -e PTERO_API_TOKEN='[REDACTED]' \
  -e PTERO_SERVER_ID=example-server-id \
  buggy121/game-server-snooze:0.5.0
```

To use YAML in the scratch-based image, mount it at the image working directory:

```bash
docker run --rm -v "$PWD/config.yaml:/app/config.yaml:ro" buggy121/game-server-snooze:0.5.0
```

Do not publish Palworld REST or RCON ports unless they are independently secured and intentionally required. Only the proxy's gameplay UDP port needs external routing.

## Development

Go 1.26.5 is required.

```bash
gofmt -w cmd pkg
go test ./...
go test -race ./...
go vet ./...
go build -trimpath ./cmd
```

The normal suite uses fake Pterodactyl HTTP servers and real localhost UDP sockets. It does not contact production infrastructure.

## Releases

Pull requests run formatting, module verification, vet, race tests, a production build, and an AMD64 container health smoke test. No registry credentials are exposed to pull requests.

Publishing a GitHub release for a protected SemVer tag such as `0.5.0-rc.1` or `0.5.0` runs the release workflow and publishes a multi-architecture Docker Hub manifest with SBOM and provenance. Release tags do not use a leading `v` prefix. The repository must provide:

- `DOCKER_USERNAME`
- `DOCKER_PASSWORD` — a Docker Hub access token with read/write access (the existing secret name is retained)

Prerelease tags do not update `latest`. Existing historical tags are never overwritten.

## Live Palworld validation

Automated tests cannot prove a current retail Palworld client handshake. A release candidate must still be validated through the normal public endpoint:

1. Confirm the backend is stopped.
2. Start the proxy and monitor its logs and Pterodactyl state.
3. Connect only when the operator explicitly requests the client test.
4. Verify one start request, A2S readiness, successful join, reconnect behavior, idle expiry, and delayed stop.
5. If the default signature does not match, capture only the bounded diagnostic prefix, add it as a regression fixture, and publish a revised candidate.
