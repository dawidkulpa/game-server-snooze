---
work_package_id: WP02
title: Leak-free UDP session ownership
dependencies:
- WP01
requirement_refs:
- FR-002
- FR-003
- FR-004
- FR-005
- FR-006
- FR-007
- FR-021
- NFR-001
- NFR-002
- NFR-004
tracker_refs: []
planning_base_branch: feat/palworld-1.0-snooze
merge_target_branch: feat/palworld-1.0-snooze
branch_strategy: Planning artifacts for this mission were generated on feat/palworld-1.0-snooze. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/palworld-1.0-snooze unless the human explicitly redirects the landing branch.
subtasks:
- T005
- T006
- T007
- T008
phase: Phase 2 - Session core
assignee: ''
agent: ''
history:
- timestamp: '2026-07-10T15:50:00Z'
  agent: hermes
  action: Prompt authored from committed spec
agent_profile: implementer
authoritative_surface: pkg/session/
create_intent:
- pkg/session/store.go
- pkg/session/session_test.go
execution_mode: code_change
owned_files:
- pkg/session/session.go
- pkg/session/store.go
- pkg/session/session_test.go
tags: []
---
# WP02 – Leak-free UDP session ownership

Implements FR-002 through FR-007 and the session/shutdown portion of FR-021.

Use strict TDD. Prove localhost UDP routing, idempotent close, read-loop termination, bidirectional activity, idle expiry, and repeated create/expire cycles with bounded resources. Implement a non-global store and session owning one upstream socket, cancellation, completion, activity, and bounded pending queue. Never perform blocking close logic while holding the store mutex. Run focused/full/race/vet/gofmt gates.

## Subtasks and evidence

- **T005:** Add RED localhost UDP tests for dedicated upstream sockets, bidirectional routing/activity, truncation handling, and parent cancellation; implement the session owner.
- **T006:** Add RED tests for idempotent close, reader termination, backend/public write failures, and stale closed-session replacement; implement exact cleanup and `Done`.
- **T007:** Add concurrent RED tests for atomic `GetOrCreate`, maximum capacity, identity-aware removal, and expiry without callbacks under the mutex; implement the injected store.
- **T008:** Run repeated connect/expire and `go test -race ./pkg/session ./pkg/proxy -count=50`; record bounded resource/cleanup evidence and full gates.
