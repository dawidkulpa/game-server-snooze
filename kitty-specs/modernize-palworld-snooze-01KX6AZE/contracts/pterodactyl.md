# Pterodactyl Client Contract

Interface:

```go
type ServerController interface {
    Status(ctx context.Context) (ServerState, error)
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
}
```

HTTP mapping:

- Status: `GET /api/client/servers/{serverID}/resources`, with the state read from `attributes.current_state` in a response shaped as `{"object":"stats","attributes":{"current_state":"running"}}`.
- Start: `POST /api/client/servers/{serverID}/power` with `{ "signal": "start" }`
- Stop: `POST /api/client/servers/{serverID}/power` with `{ "signal": "stop" }`

Requirements:

- bearer token and JSON accept headers;
- a hard HTTP client timeout of at most 10 seconds plus caller context;
- redirects are never followed, preventing bearer-token forwarding or HTTPS downgrade;
- all response bodies closed;
- non-2xx returns status and a bounded sanitized body excerpt;
- successful status bodies are capped at 64 KiB and contain exactly one JSON value;
- token and server ID are never interpolated into response, transport, or structured-log errors; caller cancellation remains discoverable with `errors.Is`;
- external current-state strings normalize to explicit internal states: `offline`, `starting`, `running`, and `stopping`; malformed or unknown states fail closed as permanent errors;
- non-retryable `4xx` responses other than `408` and `429` are permanent; transport errors, `5xx`, `408`, and `429` are retryable only within the caller's startup deadline;
- a failed status request never directly triggers a power request; only a later successful `offline` status permits one start;
- tests use representative response fixtures with `httptest.Server` and never production URLs.
