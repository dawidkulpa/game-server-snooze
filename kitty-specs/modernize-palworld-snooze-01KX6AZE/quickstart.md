# Quickstart for Development and Validation

## Local toolchain

```bash
export PATH="$HOME/.local/toolchains/go1.26.5/bin:$PATH"
go version
go mod download
```

## Quality gates

```bash
test -z "$(gofmt -l cmd pkg)"
go test ./...
go test -race ./...
go vet ./...
go build ./...
```

## Local smoke test

Run a fake Pterodactyl API and UDP echo backend through the integration test:

```bash
go test -run TestColdStartProxyIntegration -v ./pkg/proxy
```

Run the service only with non-production placeholders or an isolated fake API. Never point local negative tests at production Pterodactyl.

## Candidate publication

1. Ensure `DOCKER_USERNAME` and `DOCKER_PASSWORD` repository secrets exist; `DOCKER_PASSWORD` must contain a scoped Docker Hub access token.
2. Merge the reviewed public PR after CI passes.
3. Publish a GitHub pre-release for an explicit protected SemVer candidate tag such as `0.5.0-rc.1` on that reviewed commit. Release tags MUST NOT use a leading `v` prefix.
4. Record the successful release workflow, immutable image digest, and amd64/arm64 manifest in the deployment PR.

## Live validation handoff

The user must not connect until the agent confirms all of the following:

- candidate image is deployed and healthy;
- proxy listens on internal UDP 8212;
- public UDP 8211 maps to it at the gateway;
- Pterodactyl authorization succeeds for the new server identifier;
- Palworld backend is stopped;
- logs are being observed with credentials redacted.

The agent then sends a clear `CONNECT NOW` instruction. After successful join, the user remains connected long enough to verify traffic, then disconnects only when asked so idle cleanup and auto-stop can be observed.
