# Health Contract

The proxy exposes local HTTP endpoints on `HEALTH_ADDR`.

## `GET /healthz`

- `200 OK` with body `ok\n` while the process and health server are responsive.
- Does not depend on Pterodactyl availability or Palworld running state.

## `GET /readyz`

- `200 OK` with body `ready\n` after configuration validation and successful UDP listener bind.
- `503 Service Unavailable` before listener readiness or during shutdown.
- Does not become unready merely because the expensive backend is intentionally stopped.

Only GET and HEAD are accepted. Responses contain no configuration or credentials.
