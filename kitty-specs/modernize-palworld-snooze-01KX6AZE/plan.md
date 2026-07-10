# Implementation Plan: Modernize Palworld Snooze Proxy

**Branch**: `feat/palworld-1.0-snooze` | **Date**: 2026-07-10 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `kitty-specs/modernize-palworld-snooze-01KX6AZE/spec.md`

## Summary

Refactor the global-state UDP proxy into injected, testable components with explicit session ownership and a synchronized server lifecycle. A bounded startup gate will single-flight Pterodactyl startup, retain accepted handshake retries until the backend is ready, and release them after a configurable settle delay. Auto-stop will use generation-checked timers so stale callbacks cannot stop an active server. Palworld wake signatures will be configurable and safely diagnosable, while a running backend may adopt traffic after proxy restart. CI will enforce race, vet, build, container, and Docker Hub publication gates. The private deployment remains on proxy UDP 8212 and the existing environment-token mechanism.

## Technical Context

**Language/Version**: Go 1.26.5; module currently declares Go 1.23.5 and will be upgraded deliberately.
**Primary Dependencies**: Go standard library networking/concurrency/HTTP packages, `github.com/sirupsen/logrus`, `gopkg.in/yaml.v3`; GitHub Actions with Docker Buildx.
**Storage**: N/A; all proxy state is in memory and intentionally ephemeral.
**Testing**: Go unit tests, `httptest` fake Pterodactyl API, localhost UDP integration tests, `go test -race ./...`, `go vet`, `gofmt`, binary and Docker builds.
**Target Platform**: Linux containers on Docker Swarm; images for `linux/amd64` and `linux/arm64`.
**Project Type**: Single Go network service and container image.
**Performance Goals**: Sustain at least 32 concurrent UDP flows with bounded startup buffering and no per-expired-session resource growth; preserve full UDP payload support up to 65507 bytes.
**Constraints**: Unauthenticated public UDP input; Pterodactyl may take minutes to start; no durable state/database; credentials remain environment variables; production backend must never be stopped while active traffic exists.
**Scale/Scope**: One proxy process, one Palworld backend, up to 16 intended players and 32 configured flows.

## Charter Check

Pre-flight gate:

- Spec exists and is committed before application changes: PASS.
- TDD is mandatory for behavior changes: REQUIRED.
- No secrets may enter the public repository or test output: REQUIRED.
- Every external/network operation is bounded by context or timeout: REQUIRED.
- Production mutation is separate from code verification: REQUIRED.
- Independent immutable-diff spec and security/concurrency review before commit: REQUIRED.

Post-design re-check:

- Architecture has bounded queues, explicit ownership, and no durable state: PASS.
- Human current-client validation is retained as a final gate rather than replaced with fabricated protocol evidence: PASS.
- Deployment preserves the user-approved secret and port mechanisms: PASS.

## Architecture and state model

### Proxy

A `proxy.Proxy` object owns the public UDP listener, session store, readiness state, startup operation, zero-session generation, and lifecycle cancellation. It has no package-level mutable state. Main constructs dependencies and runs it under signal cancellation.

### Session

A session owns:

- immutable client and backend addresses;
- one upstream UDP socket;
- atomically updated last-activity time;
- bounded startup packet queue;
- ready/not-ready state;
- cancellation channel/context;
- `sync.Once` close path;
- completion signal used by tests and proxy shutdown.

Removing a session first detaches it from the store under the proxy mutex and then closes it outside the mutex. All removal paths call the same idempotent operation.

### Server lifecycle

The proxy maintains explicit backend states: `unknown`, `offline`, `starting`, `running`, `stopping`. The Pterodactyl client remains stateless and performs bounded requests. It classifies non-retryable `4xx` authorization/configuration failures and malformed/unknown status payloads as permanent. No power request follows a failed status query; transient failures are polled within the startup deadline until a successful `offline` response permits one start. The proxy single-flights startup and controls polling/settle timing. Startup success opens all pending session gates. Startup failure closes only pending sessions and leaves the listener available for later retries.

Auto-stop captures the current zero-session generation. Its callback acquires the admission mutex, verifies the generation and zero active sessions, and keeps new-session admission blocked for the bounded Pterodactyl `stop` request. The ordinary state mutex is not held during HTTP. A waiting session add proceeds only after the stop result is known, increments the generation, and cancels any retry timer; this is the linearization point tested with a barrier-controlled stop/add race.

### Wake policy

`signature` is the production default: a stopped backend accepts only configured hex prefixes. `any` is an explicit diagnostic fallback for controlled validation, never the documented production default. When backend state is `running`, new flows can be adopted from any valid non-empty UDP packet so a proxy restart does not strand existing clients.

A bounded, rate-limited unmatched-prefix diagnostic records packet length and a configurable short prefix only. Full packet logging is removed.

### Startup buffering

Each pending session has packet-count and byte limits. Both byte budgets must hold at least one `MAX_PACKET_SIZE` datagram. The proxy also tracks a global startup byte budget. Overflow uses a documented deterministic policy: preserve the first accepted handshake packet, drop subsequent overflow packets, and emit a rate-limited warning. Pending sessions are activity-refreshed only for the bounded startup attempt so `IDLE_TIMEOUT` cannot discard the initial flow before `STARTUP_TIMEOUT`; on readiness, queued packets are flushed in arrival order per session and normal idle expiry resumes.

## Project Structure

### Documentation

```text
kitty-specs/modernize-palworld-snooze-01KX6AZE/
├── spec.md
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── configuration.md
│   ├── health.md
│   └── pterodactyl.md
├── checklists/requirements.md
└── tasks/
```

### Source code

```text
cmd/
└── main.go
pkg/
├── config/
│   ├── config.go
│   └── config_test.go
├── games/
│   ├── game.go
│   ├── palworld.go
│   └── palworld_test.go
├── server/
│   ├── controller.go
│   ├── pterodactyl.go
│   └── pterodactyl_test.go
├── session/
│   ├── session.go
│   ├── store.go
│   └── session_test.go
└── proxy/
    ├── proxy.go
    ├── lifecycle.go
    ├── health.go
    ├── proxy_test.go
    └── integration_test.go
.github/workflows/
├── ci.yml
└── publish.yml
Dockerfile
README.md
config.example.yaml
docker-compose.yml
```

**Structure Decision**: Preserve recognizable `config`, `games`, `server`, and `session` packages while introducing `pkg/proxy` as the sole orchestration owner. Remove package globals rather than adding synchronization around hidden global dependencies.

## Implementation Concern Map

### IC-01 — Configuration and redaction

- **Purpose**: Define validated, bounded runtime configuration and keep credentials out of logs.
- **Relevant requirements**: FR-001, FR-009, FR-010, FR-013, FR-014, FR-022, FR-023, FR-025.
- **Affected surfaces**: `pkg/config`, examples, README.
- **Sequencing/depends-on**: none.
- **Risks**: Accidentally logging the token through `%+v`; environment/YAML representation drift.

### IC-02 — Session resource ownership

- **Purpose**: Eliminate leaked sockets/goroutines and make close/removal idempotent.
- **Relevant requirements**: FR-002–FR-007, FR-021; NFR-004.
- **Affected surfaces**: `pkg/session`, `pkg/proxy`.
- **Sequencing/depends-on**: IC-01 for limits/timeouts.
- **Risks**: double-close panic, goroutine blocked on UDP read, mutex/close callback deadlocks.

### IC-03 — Pterodactyl client boundary

- **Purpose**: Make status and power signals bounded, injected, testable, and credential-safe.
- **Relevant requirements**: FR-015, FR-016, FR-020, FR-023.
- **Affected surfaces**: `pkg/server/pterodactyl.go`.
- **Sequencing/depends-on**: IC-01.
- **Risks**: response-body leaks, error-body flooding, API state ambiguity.

### IC-04 — Startup state machine and bounded buffering

- **Purpose**: Single-flight server startup and prevent initial packet loss while preserving bounded memory.
- **Relevant requirements**: FR-012–FR-016; NFR-002, NFR-003, NFR-005.
- **Affected surfaces**: `pkg/proxy`, `pkg/session`, `pkg/server`.
- **Sequencing/depends-on**: IC-02, IC-03.
- **Risks**: queue overflow, server reports running before UDP readiness, concurrent startup result fan-out.

### IC-05 — Wake compatibility and restart adoption

- **Purpose**: Support current Palworld handshake updates without recompilation and recover after proxy restarts.
- **Relevant requirements**: FR-008–FR-011, FR-030.
- **Affected surfaces**: `pkg/games`, `pkg/proxy`, docs.
- **Sequencing/depends-on**: IC-01, IC-04.
- **Risks**: scanner-triggered wakeups, insufficient diagnostics, treating unverified signature as proven.

### IC-06 — Generation-safe auto-stop

- **Purpose**: Stop only after the last session and prevent stale timers from stopping active players.
- **Relevant requirements**: FR-017–FR-020.
- **Affected surfaces**: `pkg/proxy/lifecycle.go`.
- **Sequencing/depends-on**: IC-02, IC-03.
- **Risks**: timer callback/add-session race, API call under mutex, duplicate stop.

### IC-07 — Health, shutdown, and operator visibility

- **Purpose**: Provide Docker health signals and graceful proxy cleanup without implicitly stopping Palworld.
- **Relevant requirements**: FR-021, FR-024; NFR-006.
- **Affected surfaces**: `cmd/main.go`, `pkg/proxy/health.go`, Dockerfile.
- **Sequencing/depends-on**: IC-02, IC-04, IC-06.
- **Risks**: restart loops during legitimate startup, health listener lifecycle leaks.

### IC-08 — CI, image release, and private deployment

- **Purpose**: Gate changes and produce a testable Docker Hub candidate plus minimal deployment update.
- **Relevant requirements**: FR-026–FR-030; NFR-001, NFR-008, NFR-009.
- **Affected surfaces**: `.github/workflows`, Dockerfile, public examples, private `docker-compose` repository.
- **Sequencing/depends-on**: all implementation concerns.
- **Risks**: publishing from untrusted PRs, overwriting stable tags, leaking private deployment data, changing replicas before candidate exists.

## Delivery phases

1. Commit substantive specification and plan artifacts.
2. Generate work packages with complete FR mapping.
3. TDD configuration and Palworld detector boundaries.
4. TDD session ownership and repeated cleanup.
5. TDD Pterodactyl client and startup state machine.
6. TDD generation-safe auto-stop and proxy-restart adoption.
7. Add health/shutdown path, CI, image publication, and public documentation.
8. Run full local gates and immutable independent reviews.
9. Open the public code PR.
10. After Docker Hub secrets exist, publish a candidate image explicitly.
11. Prepare the private Compose PR with new server identifier, candidate tag, preserved port/token mechanics, and replicas set for validation.
12. After user merge/deployment confirmation, verify Pterodactyl authorization while read-only.
13. Tell the user to connect only when the proxy is healthy and Palworld is confirmed stopped; observe the full live acceptance flow.
14. If the current handshake differs, update signature fixture/default through spec-first TDD, re-review, publish a new candidate, and repeat the controlled live test.

## Verification gates

### Pre-flight gate

- Spec and plan substantive and committed.
- Baseline test/build state recorded.
- No production changes.

### Revision gates per work package

- Focused test observed failing for the intended reason.
- Minimal implementation passes focused test.
- Full test/race/vet/build remains green.
- Spec reviewer passes before quality reviewer.

### Release gate

- `gofmt` clean.
- `go test ./...` green.
- `go test -race ./...` green.
- `go vet ./...` green.
- Production binary and both target container architectures build.
- Staged diff digest receives matching independent approval.
- Candidate image digest recorded.

### Live-validation gate

- Proxy candidate healthy.
- Pterodactyl read access succeeds for the new identifier.
- Backend confirmed stopped.
- User receives an explicit connect-now instruction.
- Start, readiness, join, disconnect/idle, and delayed stop evidence captured without secret disclosure.

## Complexity Tracking

| Decision | Why Needed | Simpler Alternative Rejected Because |
|---|---|---|
| Introduce `pkg/proxy` orchestration object | Removes globals and gives timers/state one owner | Adding mutexes around existing package globals would remain difficult to test and reason about. |
| Bounded startup queues | Palworld startup loses initial UDP packets | Blind forwarding relies on client retry timing; unbounded queues permit memory exhaustion. |
| Configurable signature policy | Current handshake cannot be proven until a human client test | Wake-on-any makes public UDP scanning capable of starting the expensive backend. |
| Multi-architecture image | Swarm includes ARM64 and AMD64 nodes | An AMD64-only image without placement constraints can fail scheduling; a pure-Go multi-arch build is straightforward. |
