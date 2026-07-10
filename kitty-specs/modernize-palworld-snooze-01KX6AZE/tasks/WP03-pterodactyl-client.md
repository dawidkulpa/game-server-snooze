---
work_package_id: WP03
title: Bounded Pterodactyl client
dependencies:
- WP01
requirement_refs:
- FR-015
- FR-016
- FR-020
- FR-023
- NFR-003
- NFR-006
tracker_refs: []
planning_base_branch: feat/palworld-1.0-snooze
merge_target_branch: feat/palworld-1.0-snooze
branch_strategy: Planning artifacts for this mission were generated on feat/palworld-1.0-snooze. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/palworld-1.0-snooze unless the human explicitly redirects the landing branch.
subtasks:
- T009
- T010
- T011
phase: Phase 2 - Control boundary
assignee: ''
agent: ''
history:
- timestamp: '2026-07-10T15:50:00Z'
  agent: hermes
  action: Prompt authored from committed spec
agent_profile: implementer
authoritative_surface: pkg/server/
create_intent:
- pkg/server/controller.go
- pkg/server/pterodactyl_test.go
execution_mode: code_change
owned_files:
- pkg/server/controller.go
- pkg/server/pterodactyl.go
- pkg/server/pterodactyl_test.go
tags: []
---
# WP03 – Bounded Pterodactyl client

Implements FR-015, FR-016, FR-020, and API-error redaction from FR-023.

Use `httptest.Server` under strict TDD. Cover status normalization, power request method/path/headers/body, caller cancellation, client timeout, non-2xx, bounded error body, malformed JSON, and response-body closure. Implement an injected context-aware interface with no globals. Unknown state must never mean running; token must never appear in errors/logs. Run focused/full/race/vet/gofmt gates.

## Subtasks and evidence

- **T009:** Add RED `httptest` fixtures for the real `attributes.current_state` schema and every supported, malformed, and unknown state.
- **T010:** Add RED tests for methods/paths/headers/power JSON and HTTP classification; implement fail-closed permanent `4xx`/schema errors and retryable transport/`5xx`/`408`/`429` errors without status-failure power requests.
- **T011:** Test caller cancellation, client timeout, response closure, 4096-byte bounded sanitized bodies, direct-constructor HTTP restrictions, and token redaction; run `go test -race ./pkg/server`, full tests, vet, and formatting.
