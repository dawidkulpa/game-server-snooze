# Research and Design Decisions

## R-001: Do not use close-packet parsing as lifecycle authority

The existing `ControlChannelClose` heuristic is protocol-version-sensitive and is not required for correctness. Session activity timeout is the authority. A recognized close may remain only as an optional acceleration after live validation, never as the sole cleanup path.

## R-002: Keep signature-gated cold wake

Wake-on-any UDP traffic is compatible but permits Internet scanning or spoofed datagrams to start an expensive server. Production therefore remains signature-gated while signatures become configuration. To learn an unknown handshake safely, keep `signature` policy and enable bounded, rate-limited unmatched-prefix diagnostics; `any` is only a controlled compatibility fallback and does not produce unmatched diagnostics because every non-empty packet is accepted.

## R-003: Adopt any non-empty traffic when backend is already running

After proxy restart, existing client traffic may not repeat the initial handshake. The proxy may safely create a flow from non-empty traffic when its initial Pterodactyl status is `running`; no expensive wake is caused.

## R-004: Buffer before readiness

Sending initial datagrams to an offline backend loses them and depends on client retry behavior. Accepted startup sessions keep bounded per-flow queues. One startup operation polls Pterodactyl until `running`, waits a settle duration, then opens all pending gates.

## R-005: One orchestration owner

Mutable package globals are replaced by a `Proxy` instance. Session store, backend state, startup result, zero-session generation, and stop timer share one clear synchronization boundary. API calls and socket closes occur outside the main mutex.

## R-006: Generation-checked auto-stop

Stopping based only on a timer plus an unsynchronized integer races with new sessions. Each zero-session transition increments a generation. A callback is valid only when its captured generation still matches and the map is empty.

## R-007: Environment token remains

The user explicitly deferred token-file/Swarm-secret integration. This release keeps `PTERO_API_TOKEN`; implementation and logs must still redact it. Public examples use placeholders.

## R-008: Docker Hub candidate is explicit

PR workflows test without registry credentials. A semver-tag release workflow creates candidate/stable tags from reviewed commits using the existing `DOCKER_USERNAME` and `DOCKER_PASSWORD` repository secrets; the latter stores a scoped access token. Stable semver tags are published only after live acceptance.

## R-009: Real-client evidence remains mandatory

No public protocol documentation or repository evidence proves that the 2025 eight-byte prefix is current. Automated tests can prove configurable behavior but cannot replace one connection from the current game client. Diagnostic mode records only a bounded prefix and packet length.

## R-010: Pterodactyl authorization is a deployment precondition

A read-only request using the currently committed token and the new server identifier returned HTTP 403 during planning. Code work is not blocked, but live deployment is blocked until that token can access the server. The token itself must not be printed or changed by this mission.
