# Runtime Data Model

## Proxy

Fields:

- public UDP listener
- backend UDP address
- injected `ServerController`
- injected `WakeDetector`
- session map keyed by canonical client `IP:port`
- backend state enum: `unknown`, `offline`, `starting`, `running`, `stopping`
- optional in-flight startup operation
- optional auto-stop timer
- monotonically increasing zero-session generation
- global queued-startup byte count
- readiness flag and lifecycle cancellation

Invariants:

- map membership is the active-session authority;
- start operation is unique;
- stop callback is valid only for its captured generation;
- global queued bytes never exceed configuration.

## Session

Fields:

- canonical client address/key
- dedicated upstream UDP connection
- atomic last-activity timestamp
- `ready` flag guarded by session mutex
- startup packet queue and queued byte count
- context/cancel or done channel
- `sync.Once` close guard
- completion channel

Transitions:

```text
created-pending -> ready -> idle/closed
created-ready   -> idle/closed
created-pending -> startup-failed/closed
```

While `created-pending`, the proxy refreshes session activity only for the bounded startup operation; `STARTUP_TIMEOUT`, startup failure, buffer failure cleanup, or shutdown closes it. Normal `IDLE_TIMEOUT` expiry resumes after the gate becomes ready.

Closing is terminal and idempotent.

## Startup operation

Fields:

- operation identity/generation
- context with startup timeout
- completion result
- one-time start invocation state

Transitions:

```text
created -> checking
checking -> starting | running | failed
starting -> polling
polling -> settling | timeout/failed
settling -> ready
```

Only `ready` opens session gates.

## Wake detector

Fields:

- policy: `signature` or `any`
- decoded non-empty byte prefixes
- diagnostic enabled flag
- diagnostic prefix byte limit
- rate limiter state

Behavior:

- `signature`: match one configured prefix;
- `any`: accept any non-empty packet;
- diagnostics never change match outcome.

## Pterodactyl state

External values are normalized to the internal enum. Unknown external states remain explicit and are not treated as ready.
