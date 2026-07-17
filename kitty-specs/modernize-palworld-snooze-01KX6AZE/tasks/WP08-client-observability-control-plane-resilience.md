---
work_package_id: WP08
title: Client IP observability and control-plane-resilient forwarding
dependencies:
- WP04
- WP05
requirement_refs:
- FR-031
- FR-032
- FR-033
- FR-034
- NFR-006
- NFR-009
tracker_refs: []
planning_base_branch: fix/client-observability-health
merge_target_branch: fix/client-observability-health
branch_strategy: This corrective work package is implemented directly on fix/client-observability-health from origin/master and must land through a reviewed pull request to master.
subtasks:
- T028
- T029
- T030
- T031
phase: Phase 7 - Production follow-up
assignee: ''
agent: hermes
history:
- timestamp: '2026-07-17T00:00:00Z'
  agent: hermes
  action: Production incident follow-up authored from correlated proxy, Traefik, and node logs
agent_profile: implementer
authoritative_surface: .
create_intent: []
execution_mode: code_change
owned_files:
- pkg/proxy/proxy.go
- pkg/proxy/proxy_test.go
- pkg/proxy/proxy_logging_test.go
- pkg/readiness/pterodactyl.go
- pkg/readiness/pterodactyl_test.go
- README.md
- kitty-specs/modernize-palworld-snooze-01KX6AZE/spec.md
- kitty-specs/modernize-palworld-snooze-01KX6AZE/plan.md
- kitty-specs/modernize-palworld-snooze-01KX6AZE/tasks/WP08-client-observability-control-plane-resilience.md
tags:
- incident-follow-up
---
# WP08 – Client IP observability and control-plane-resilient forwarding

Implements FR-031 through FR-034 and the related logging/resilience acceptance scenarios.

Use strict vertical TDD. First prove that accepted IPv4 and IPv6 session-open logs contain `client_ip` without source ports. Then reproduce the production failure with a real Pterodactyl readiness probe whose controller times out: repeated inconclusive status failures must leave the established gate and sessions open. Separately prove that three consecutive successful recognized non-running states still close the gate, while `running` or an inconclusive result resets the confirmation sequence. Preserve startup fail-closed behavior and legacy A2S semantics.

## Subtasks and evidence

- **T028:** Record the incident timeline and update the spec/plan before application code.
- **T029:** Add and run RED IPv4/IPv6 structured logging tests; implement normalized `client_ip` on every successful session creation.
- **T030:** Add and run RED Pterodactyl timeout and confirmed-state sequence tests; implement inconclusive-error classification and confirmation-only gating.
- **T031:** Update public operational documentation, run focused/full/race/vet/format/build gates, create an immutable diff snapshot, obtain independent review, open the PR, and verify CI.

## Activity Log

- 2026-07-17T10:34:21Z – hermes – Planned from the correlated production incident evidence; application implementation not started.