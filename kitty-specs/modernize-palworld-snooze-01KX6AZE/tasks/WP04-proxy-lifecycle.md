---
work_package_id: WP04
title: Startup gate and generation-safe auto-stop
dependencies:
- WP02
- WP03
requirement_refs:
- FR-011
- FR-012
- FR-013
- FR-014
- FR-015
- FR-016
- FR-017
- FR-018
- FR-019
- NFR-002
- NFR-003
- NFR-005
tracker_refs: []
planning_base_branch: feat/palworld-1.0-snooze
merge_target_branch: feat/palworld-1.0-snooze
branch_strategy: Planning artifacts for this mission were generated on feat/palworld-1.0-snooze. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/palworld-1.0-snooze unless the human explicitly redirects the landing branch.
subtasks:
- T012
- T013
- T014
- T015
- T016
phase: Phase 3 - Orchestration
assignee: ''
agent: ''
history:
- timestamp: '2026-07-10T15:50:00Z'
  agent: hermes
  action: Prompt authored from committed spec
agent_profile: implementer
authoritative_surface: pkg/proxy/
create_intent:
- pkg/proxy/proxy.go
- pkg/proxy/lifecycle.go
- pkg/proxy/proxy_test.go
- pkg/readiness/a2s.go
- pkg/readiness/a2s_test.go
execution_mode: code_change
owned_files:
- pkg/proxy/proxy.go
- pkg/proxy/lifecycle.go
- pkg/proxy/proxy_test.go
- pkg/readiness/a2s.go
- pkg/readiness/a2s_test.go
tags: []
---
# WP04 – Startup gate and generation-safe auto-stop

Implements FR-011 through FR-019.

Use strict TDD. Cover one start under concurrent cold flows, per-flow/global queue limits and deterministic overflow, polling/timeout/settle/ordered flush, startup failure cleanup, running-backend adoption after proxy restart, and deterministic timer-generation races proving no stop while active. Implement one orchestration owner; keep the bounded stop request outside the ordinary state mutex while holding the admission mutex so stop and new-flow admission are linearized. Run focused/full/race/vet/gofmt gates.

## Subtasks and evidence

- **T012:** Add RED concurrent cold-start tests proving one logical start and independent session routing; implement serialized startup generation.
- **T013:** Add RED tests for packet/per-session/global limits, first-packet capacity, ordered flush, overflow log suppression, and startup longer than idle but shorter than startup timeout; implement bounded queues and pending-session retention.
- **T014:** Add RED tests for A2S challenge/readiness, settle delay, transient/permanent status failures, deterministic startup cleanup, and immediate later retry; implement the gate and failure generation.
- **T015:** Add barrier-controlled RED tests for admission during stop dispatch, stale timer generation, bounded retries, readiness gating, and shutdown waiting; implement admission-linearized auto-stop.
- **T016:** Test running-backend restart adoption, truncated datagram drops, closed-session replacement, capacity warning rate limiting, and race stress; record focused and full gate output.
