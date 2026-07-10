---
work_package_id: WP05
title: Executable integration, health, and graceful shutdown
dependencies:
- WP04
requirement_refs:
- FR-021
- FR-024
- FR-030
- NFR-001
- NFR-003
- NFR-004
- NFR-006
tracker_refs: []
planning_base_branch: feat/palworld-1.0-snooze
merge_target_branch: feat/palworld-1.0-snooze
branch_strategy: Planning artifacts for this mission were generated on feat/palworld-1.0-snooze. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/palworld-1.0-snooze unless the human explicitly redirects the landing branch.
subtasks:
- T017
- T018
- T019
phase: Phase 4 - Integration
assignee: ''
agent: ''
history:
- timestamp: '2026-07-10T15:50:00Z'
  agent: hermes
  action: Prompt authored from committed spec
agent_profile: implementer
authoritative_surface: cmd/
create_intent:
- pkg/proxy/health.go
- pkg/proxy/integration_test.go
execution_mode: code_change
owned_files:
- cmd/main.go
- pkg/proxy/health.go
- pkg/proxy/integration_test.go
tags: []
---
# WP05 – Executable integration, health, and graceful shutdown

Implements FR-021, FR-024, and automated preparation for FR-030.

Use strict TDD. Test health methods/status/readiness/shutdown, then a real localhost flow using fake Pterodactyl and UDP echo: offline → start → running/settle → response → idle → delayed stop. Test process shutdown closes listeners/sessions without an implicit stop. Wire dependencies under signal cancellation. Run focused/full/race/vet/gofmt/build gates.

## Subtasks and evidence

- **T017:** Add RED tests for GET/HEAD-only health responses, liveness/readiness semantics, loopback-only healthcheck URL validation, and shutdown unready state; implement handlers and executable healthcheck.
- **T018:** Add a real localhost `run`/UDP/`httptest` integration covering startup, readiness, response, idle/stop, listener/session cleanup, and no shutdown power signal; wire configured dependencies and wait for both servers to exit.
- **T019:** Run focused integration tests repeatedly under race plus `go test ./...`, `go test -race ./...`, `go vet ./...`, and `go build -trimpath ./...`; record actual results.
