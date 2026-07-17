# Specification: Modernize Palworld Snooze Proxy

Status: Draft
Mission: `modernize-palworld-snooze-01KX6AZE`
Target release: `0.5.0`

## 1. Purpose

Modernize `game-server-snooze` so current Palworld clients can connect through a UDP proxy that starts a Pterodactyl-managed game server on demand and stops it only after all observed player traffic has ended. The proxy must not accumulate sockets or goroutines across reconnects, must tolerate slow game-server startup, must keep an established UDP data plane available during inconclusive Pterodactyl control-plane failures, and must fail visibly rather than silently stopping an active server.

## 2. Deployment context

- The application is a public Go repository and Docker image.
- The application listens on configurable UDP and forwards to a separately configured private Palworld gameplay endpoint; documentation examples use reserved test-net addresses only.
- Edge NAT may map a public gameplay port to a different internal proxy port. Exact production topology and deployment paths remain in the private deployment repository.
- The Palworld server is controlled through the Pterodactyl Client API.
- Pterodactyl credentials remain supplied through the existing `PTERO_API_TOKEN` environment variable in this release. Token-file or external-secret integration is out of scope.
- Pterodactyl server identifiers and deployment endpoints are private and represented only as `[REDACTED]` in public artifacts.
- The user will configure Docker Hub repository secrets for image publication.

## 3. Definitions

- **Flow/session:** one observed client UDP source address and port mapped to one upstream UDP socket.
- **Active session:** a session whose client-to-server or server-to-client traffic occurred within `IDLE_TIMEOUT`.
- **Wake packet:** a new-flow packet accepted by the configured game detector while the backend is stopped.
- **Startup gate:** the period after a wake packet is accepted and before buffered packets may be sent to the backend.
- **Zero-session generation:** an identity associated with one transition to zero active sessions; it prevents stale timers from stopping a newly active server.
- **Inconclusive readiness failure:** a timeout, transport failure, HTTP error, authorization failure, malformed response, or canceled request that prevents the proxy from learning the current Pterodactyl state. It is not evidence that the already-forwarding gameplay backend stopped.
- **Confirmed backend unavailability:** a successful Pterodactyl status response whose recognized state is `offline`, `starting`, or `stopping`.

## 4. Functional requirements

| ID | Requirement |
|---|---|
| FR-001 | The proxy MUST preserve the existing YAML and environment-variable configuration mechanisms, including `PTERO_API_TOKEN` as an environment variable. |
| FR-002 | The proxy MUST listen for UDP client traffic and forward each accepted client flow through a dedicated upstream UDP socket so backend replies return to the correct client. |
| FR-003 | Each session MUST own a cancellable lifecycle and MUST close its upstream UDP socket exactly once when removed, including idle expiry, proxy shutdown, and read/write error paths. Version-sensitive protocol close markers MUST NOT be lifecycle authority. |
| FR-004 | Session removal MUST be idempotent and MUST not panic or leak a blocked goroutine when multiple close/expiry paths race. |
| FR-005 | Activity timestamps, session storage, controller state, counters, and stop timers MUST be race-free under `go test -race`. |
| FR-006 | Both client-to-server and server-to-client traffic MUST refresh session activity. |
| FR-007 | A centralized or otherwise bounded idle mechanism MUST remove sessions after `IDLE_TIMEOUT`; the design MUST NOT leave one permanent monitor goroutine per expired session. |
| FR-008 | When the backend is stopped, only a packet accepted by the Palworld wake detector MUST create the first session and request a server start. Random unmatched UDP packets MUST NOT wake the server in production mode. |
| FR-009 | Palworld wake signatures MUST be configurable without recompiling the proxy. The 2025 signature remains a default compatibility value until the current client handshake is confirmed by a live test. |
| FR-010 | A bounded diagnostic mode MUST be able to log only the prefix and length of rate-limited unmatched candidate packets, never an unlimited full-packet dump, so a current handshake can be learned safely. |
| FR-011 | If Pterodactyl reports the backend already `running`, the proxy MUST adopt a new client flow from traffic even when that packet is not a wake signature. This enables recovery after a proxy restart. |
| FR-012 | Exactly one logical start operation MUST be in flight when multiple players send wake packets concurrently. All accepted startup flows MUST share that operation. |
| FR-013 | Accepted startup packets MUST be held in a bounded buffer while Pterodactyl starts the server. Buffer limits MUST be configurable and enforced globally and per session so unauthenticated UDP traffic cannot cause unbounded memory growth. A pending startup session remains alive until the bounded startup attempt completes even when startup exceeds `IDLE_TIMEOUT`. |
| FR-014 | Buffered traffic MUST be released only after Pterodactyl reports `running` under the configured readiness mode and the startup settle delay has elapsed. `pterodactyl` is the default because the native Palworld 1.0 egg emits its startup marker only after authenticated REST and a marked gameplay UDP listener are ready; A2S is legacy compatibility only. Buffer overflow MUST be logged and handled deterministically without process failure. |
| FR-015 | Pterodactyl status/start/stop requests MUST use bounded HTTP timeouts, close response bodies, validate non-2xx responses, and return actionable errors without logging credentials. |
| FR-016 | A failed status request MUST NOT be followed by a power request until a later successful status response confirms `offline`. Permanent authorization/schema/unknown-state failures MUST fail the startup attempt promptly; retryable transport, `5xx`, `408`, and `429` failures may be polled only within `STARTUP_TIMEOUT`. The proxy MUST NOT pretend the backend is ready. |
| FR-017 | Transitioning from one or more sessions to zero sessions MUST schedule exactly one auto-stop operation after `AUTO_STOP_DELAY`. |
| FR-018 | Any newly active session MUST cancel/invalidate the pending auto-stop. A stale timer MUST re-check the current zero-session generation and session count before sending `stop`. |
| FR-019 | The proxy MUST never send `stop` while at least one active session is present. This invariant MUST be tested under concurrent session-add/timer-fire races. |
| FR-020 | Stop failures MUST be visible in logs and MUST NOT create an infinite retry loop. |
| FR-021 | Process shutdown MUST close the public UDP listener and every active upstream session without issuing an implicit Pterodactyl stop command. |
| FR-022 | Configuration loading MUST reject invalid addresses, non-positive limits/timeouts, packet sizes above the maximum UDP payload, missing Pterodactyl fields, and unsupported wake policies before opening the listener. |
| FR-023 | Configuration and logs MUST redact `PTERO_API_TOKEN`, including debug-level final-configuration logging. |
| FR-024 | The service MUST expose a local HTTP liveness/readiness endpoint suitable for a Docker healthcheck. Liveness means the process and health server are responsive; readiness additionally means configuration loaded, the UDP listener opened, and shutdown has not begun. Backend availability does not control either signal. |
| FR-025 | Existing operators MUST be able to continue using the documented environment variables; new settings MUST have safe defaults and be documented in `README.md` and `config.example.yaml`. |
| FR-026 | The public image MUST be published to Docker Hub as `buggy121/game-server-snooze` using the existing repository secrets `DOCKER_USERNAME` and `DOCKER_PASSWORD` (whose value must be a scoped Docker Hub access token). Pull requests MUST test/build without publishing. Publication MUST be explicit for release/candidate images. |
| FR-027 | The container build MUST be reproducible for `linux/amd64` and `linux/arm64`, run as a non-root user, include trusted CA certificates for Pterodactyl HTTPS, and have an image-level healthcheck. |
| FR-028 | The private deployment Compose MUST retain its existing UDP `8212:8212`, `LISTEN_ADDR=:8212`, private gameplay backend, and environment-token mechanism, while updating `PTERO_SERVER_ID` to the deployment-provided replacement value. |
| FR-029 | The deployment MUST pin a candidate/versioned image tag rather than `latest`; production replicas MUST change from zero to one only for the live validation/deployment stage. |
| FR-030 | The implementation MUST provide a controlled current-client validation sequence: stopped backend → connect attempt → one start request → readiness → successful join → disconnect/idle expiry → delayed stop. The agent MUST explicitly tell the user when to initiate the connection. |
| FR-031 | Normal operation MUST emit structured info-level lifecycle logs for UDP session creation/removal, backend readiness, auto-stop scheduling/cancellation, and successful stop requests. Logs MUST include bounded operational fields such as active-session counts, expiry counts, delays, and attempts without exposing backend addresses, credentials, Pterodactyl identifiers, client source ports, or packet bodies. UDP sessions MUST NOT be described as authenticated players. |
| FR-032 | Every successfully created UDP session MUST log the normalized source IP in a structured `client_ip` field. This is operator-requested connection attribution and MUST work for IPv4 and IPv6 without including the ephemeral source port. |
| FR-033 | While established UDP forwarding is open in `pterodactyl` readiness mode, an inconclusive readiness failure MUST be logged but MUST NOT close the forwarding gate, clear running state, or close existing sessions. Startup and recovery attempts remain fail-closed and MUST NOT infer `running` from an inconclusive failure. |
| FR-034 | Established forwarding in `pterodactyl` mode MAY be gated only after three consecutive confirmed backend-unavailability responses in the same readiness generation. A successful `running` response or any inconclusive result MUST reset the confirmation sequence. Legacy A2S mode retains its protocol-specific timeout behavior. |

## 5. Non-functional requirements

| ID | Requirement |
|---|---|
| NFR-001 | `go test ./...`, `go test -race ./...`, `go vet ./...`, formatting checks, and a production build MUST pass. |
| NFR-002 | Unit tests MUST use deterministic clocks/timers where practical; the normal suite MUST not depend on minute-long sleeps. |
| NFR-003 | Integration tests MUST use real localhost UDP sockets and an `httptest` Pterodactyl API, not a real production server. |
| NFR-004 | Tests MUST cover repeated connect/expire cycles and prove goroutine/socket counts do not grow without bound. Exact runtime goroutine counts may use a bounded tolerance. |
| NFR-005 | Startup buffering, maximum sessions, packet size, and diagnostic logging MUST remain bounded under hostile input. |
| NFR-006 | Logging MUST identify lifecycle state transitions, bounded flow counts, client source IPs on session creation, and stop/start outcomes sufficiently for diagnosis without emitting client source ports, private backend/controller values, credentials, or unlimited packet bodies. |
| NFR-007 | The implementation MUST remain a lightweight single-process proxy with no database, durable state store, or external queue. |
| NFR-008 | Public documentation and examples MUST use generic placeholders and MUST NOT contain private hostnames, tokens, or the production server identifier. The production identifier belongs only in the private Compose repository. |
| NFR-009 | Changes MUST receive independent spec-compliance and security/concurrency review of an immutable staged diff before commit. |
| NFR-010 | The Go toolchain upgrade to 1.26.5 MUST be deliberate and validated by module verification, tests, race tests, vet, static analysis, production builds, and the pinned container builder. |

## 6. Explicit invariants

1. `active sessions > 0` implies no valid auto-stop action may execute.
2. A session removed from the store eventually has its upstream socket closed and all lifecycle goroutines terminated.
3. A start transition has at most one in-flight Pterodactyl start operation.
4. Startup buffering never exceeds configured per-session or global limits.
5. The API token never appears in normal or debug logs, test failure output, committed public examples, or CI configuration.
6. Random unmatched UDP traffic cannot wake a stopped backend under production wake policy.
7. Proxy shutdown does not stop the game server.

## 7. Failure behavior

- Pterodactyl unavailable during startup/recovery: keep the proxy alive, emit a bounded actionable error, keep startup sessions bounded, and allow a later signed client retry to initiate another bounded attempt.
- Pterodactyl unavailable while gameplay forwarding is already established: keep forwarding and existing sessions intact, log the inconclusive control-plane failure, and resume state confirmation when the API recovers.
- Pterodactyl successfully reports `offline`, `starting`, or `stopping` three consecutive times while gameplay forwarding is established: close the forwarding gate and sessions using the existing generation-safe loss path.
- Start accepted but server never reaches `running`: expire the startup operation after a configured timeout, close affected startup sessions, and do not claim readiness.
- Startup buffer full: retain deterministic bounded behavior, log a rate-limited warning, and never allocate beyond limits.
- Backend UDP read/write error: remove and close only the affected session; re-evaluate zero-session auto-stop safely.
- Stop request failure: log it and leave the server state untouched; no unbounded retry.
- Health endpoint failure: Docker may restart the proxy, but restart recovery must adopt traffic when Pterodactyl reports the game already running.

## 8. Out of scope

- Changing from environment-token configuration to Docker secret files.
- Managing edge-gateway port-forwarding or firewall rules.
- Replacing Pterodactyl.
- Parsing player identities or querying Palworld REST/RCON for authoritative player counts.
- Preserving original client source addresses inside Palworld itself; the UDP proxy remains the backend peer and connection attribution is provided by proxy logs.
- Automatically changing cron/schedule intervals.
- Deploying or mutating production before a reviewed candidate image exists.

## 9. Acceptance scenarios

### AS-001: Cold start and join

Given Pterodactyl reports `offline`, when one valid Palworld wake packet arrives, exactly one start request is issued, startup packets remain bounded, and packets reach the backend only after `running`, successful configured application readiness, and the settle delay.

### AS-002: Concurrent cold start

Given Pterodactyl is offline, when sixteen client flows arrive concurrently, one logical start operation occurs, all accepted sessions remain independently routable, and race testing reports no data race.

### AS-003: Idle cleanup

Given repeated client flows become idle, each is removed exactly once, every upstream socket/read loop terminates, and process resources remain bounded across many cycles.

### AS-004: Stop cancellation race

Given the auto-stop timer is becoming due, when a new session is accepted concurrently, the stale timer cannot send a stop signal.

### AS-005: Proxy restart while game runs

Given Pterodactyl reports `running` and the proxy has an empty session store after restart, the next datagram from an existing client creates/adopts a flow even without the cold-start signature and traffic resumes.

### AS-006: Scanner packet

Given Pterodactyl reports `offline`, an unmatched datagram neither creates a session nor calls the Pterodactyl start endpoint.

### AS-007: Credential redaction

Given debug logging is enabled, loaded configuration and all API errors omit the token value.

### AS-008: Live current-client validation

Given the reviewed candidate proxy is deployed and the Palworld server is stopped, the user connects to public UDP `8211` only when prompted. Logs and Pterodactyl state prove the complete cold-start/join/disconnect/auto-stop path. If the signature does not match, bounded diagnostic output identifies only the required packet prefix; the spec/default signature is updated and the automated regression fixture is added before release.

### AS-009: Startup exceeds idle timeout

Given a valid flow is accepted and startup takes longer than `IDLE_TIMEOUT` but less than `STARTUP_TIMEOUT`, the bounded pending session and its initial packets remain available, become active when readiness succeeds, and are not expired by the normal idle sweeper during startup.

### AS-010: Pterodactyl control-plane timeout during active play

Given six active UDP sessions and a backend previously confirmed `running`, when three or more Pterodactyl status calls time out, the proxy logs bounded inconclusive failures, retains all six sessions, and continues forwarding without a reconnect. A later successful `running` response clears the warning sequence.

### AS-011: Confirmed backend stop during active forwarding

Given active forwarding, when Pterodactyl successfully reports a recognized non-running state three consecutive times in the same readiness generation, the proxy closes the forwarding gate and all affected sessions. A `running` response or an inconclusive control-plane result between confirmations prevents the sequence from completing.

### AS-012: Connection attribution

Given a new IPv4 or IPv6 UDP flow is accepted, the session-open log contains exactly the normalized source IP in `client_ip` and does not contain the source port, backend address, credentials, identifiers, or packet contents.

## 10. Completion gate

Implementation may be reported as **complete, awaiting live validation** after the automated/review/image/Compose gates pass. The release mission itself is complete only when:

- Every FR maps to an implementation work package and test or explicit deployment check.
- Local and CI quality gates pass.
- A Docker Hub candidate image is available.
- The private Compose PR uses the candidate/versioned image and new server identifier without altering secret handling or the existing internal port mapping.
- Independent immutable-diff review passes.
- The user is prompted at the live-test stage and the current Palworld client completes the acceptance sequence, or the user formally waives that release gate. A result recorded as blocked awaiting the human test does not complete the mission.
