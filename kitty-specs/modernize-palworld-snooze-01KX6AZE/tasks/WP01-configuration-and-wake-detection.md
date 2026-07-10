---
work_package_id: WP01
title: Validated configuration and Palworld wake detection
dependencies: []
requirement_refs:
- FR-001
- FR-008
- FR-009
- FR-010
- FR-022
- FR-023
- FR-025
- NFR-005
- NFR-006
- NFR-008
tracker_refs: []
planning_base_branch: feat/palworld-1.0-snooze
merge_target_branch: feat/palworld-1.0-snooze
branch_strategy: Planning artifacts for this mission were generated on feat/palworld-1.0-snooze. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/palworld-1.0-snooze unless the human explicitly redirects the landing branch.
subtasks:
- T001
- T002
- T003
- T004
phase: Phase 1 - Boundaries
assignee: ''
agent: ''
history:
- timestamp: '2026-07-10T15:50:00Z'
  agent: hermes
  action: Prompt authored from committed spec
agent_profile: implementer
authoritative_surface: pkg/config/
create_intent:
- pkg/config/config_test.go
- pkg/games/palworld_test.go
execution_mode: code_change
owned_files:
- pkg/config/config.go
- pkg/config/config_test.go
- pkg/games/game.go
- pkg/games/palworld.go
- pkg/games/palworld_test.go
tags: []
---
# WP01 – Validated configuration and Palworld wake detection

Implements FR-001, FR-008, FR-009, FR-010, FR-022, FR-023, FR-025.

Use strict TDD. First add failing tests for defaults, YAML/env precedence, invalid values, UDP maximum, unsupported policy, required Pterodactyl values, and strict env parse errors. Then implement validation without changing `PTERO_API_TOKEN`. Add failing detector tests for configurable signatures, short/unmatched packets, explicit `any` mode, and bounded prefix diagnostics before implementation. Prove token redaction and that full packets are never logged. Run focused/full/race/vet/gofmt gates.

## Subtasks and evidence

- **T001:** Add RED config tests for defaults, strict YAML including trailing documents, environment precedence, all legacy variables, cross-field buffer bounds, loopback-only HTTP, and token redaction; record the focused failing command.
- **T002:** Implement config loading/validation until `go test ./pkg/config -v` passes.
- **T003:** Add RED/GREEN detector tests for the exact legacy signature, configured/trimmed signatures, `any`, short packets, concurrent rate limiting, and the 32-byte hard diagnostic bound; run `go test -race ./pkg/games`.
- **T004:** Wire configured options in `cmd`, update examples/contracts, and capture `gofmt`, `go test ./...`, `go test -race ./...`, and `go vet ./...` results.

## Activity Log

- 2026-07-10T16:10:46Z – user – Moved to in_progress
