# Configuration Contract

Existing settings remain supported. New settings use YAML snake_case and uppercase environment overrides.

| YAML | Environment | Type | Required/default | Validation |
|---|---|---|---|---|
| `listen_addr` | `LISTEN_ADDR` | string | `:8211` | valid UDP address |
| `server_addr` | `SERVER_ADDR` | string | required | valid UDP address |
| `max_sessions` | `MAX_SESSIONS` | integer | `32` | > 0 |
| `max_packet_size` | `MAX_PACKET_SIZE` | integer | `65507` | 1..65507 |
| `game` | `GAME` | enum | `palworld` | currently only `palworld` |
| `idle_timeout` | `IDLE_TIMEOUT` | duration | `30s` | > 0 |
| `auto_stop_delay` | `AUTO_STOP_DELAY` | duration | `1m` | > 0 |
| `startup_timeout` | `STARTUP_TIMEOUT` | duration | `5m` | > 0 |
| `startup_poll_interval` | `STARTUP_POLL_INTERVAL` | duration | `2s` | > 0 and < startup timeout |
| `startup_settle_delay` | `STARTUP_SETTLE_DELAY` | duration | `5s` | >= 0 and < startup timeout |
| `startup_buffer_packets` | `STARTUP_BUFFER_PACKETS` | integer | `32` | > 0 |
| `startup_buffer_bytes_per_session` | `STARTUP_BUFFER_BYTES_PER_SESSION` | integer | `262144` | >= `max_packet_size` |
| `startup_buffer_bytes_global` | `STARTUP_BUFFER_BYTES_GLOBAL` | integer | `4194304` | >= per-session bytes and `max_packet_size` |
| `backend_readiness_mode` | `BACKEND_READINESS_MODE` | enum | `a2s` | `a2s` or compatibility `delay` |
| `backend_readiness_addr` | `BACKEND_READINESS_ADDR` | string | backend host, UDP 27015 | empty or valid UDP address |
| `wake_policy` | `WAKE_POLICY` | enum | `signature` | `signature` or `any` |
| `wake_signatures` | `WAKE_SIGNATURES` | string list | Palworld compatibility prefix | non-empty decoded hex prefixes under signature policy |
| `log_unmatched_prefixes` | `LOG_UNMATCHED_PREFIXES` | boolean | `false` | strict boolean |
| `diagnostic_prefix_bytes` | `DIAGNOSTIC_PREFIX_BYTES` | integer | `8` | 1..32 |
| `health_addr` | `HEALTH_ADDR` | string | `127.0.0.1:8080` | loopback TCP address |
| `pterodactyl.base_url` | `PTERO_BASE_URL` | URL | required | HTTPS; plaintext HTTP only for loopback tests; no URL user-info |
| `pterodactyl.api_token` | `PTERO_API_TOKEN` | string | required | non-empty; always redacted |
| `pterodactyl.server_id` | `PTERO_SERVER_ID` | string | required | non-empty |
| `log_level` | `LOG_LEVEL` | enum | `info` | `debug`, `info`, `warn`, `error`, or `fatal` |

Unknown YAML fields and additional YAML documents fail closed. Environment values override YAML values. Invalid environment overrides return configuration errors rather than silently keeping defaults. Pending startup sessions are retained only for the bounded `startup_timeout`; ordinary `idle_timeout` expiry resumes after the startup gate opens.
