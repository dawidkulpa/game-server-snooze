---
work_package_id: WP06
title: CI, multi-architecture image, and public documentation
dependencies:
- WP05
requirement_refs:
- FR-025
- FR-026
- FR-027
- NFR-001
- NFR-007
- NFR-008
- NFR-010
tracker_refs: []
planning_base_branch: feat/palworld-1.0-snooze
merge_target_branch: feat/palworld-1.0-snooze
branch_strategy: Planning artifacts for this mission were generated on feat/palworld-1.0-snooze. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/palworld-1.0-snooze unless the human explicitly redirects the landing branch.
subtasks:
- T020
- T021
- T022
- T023
phase: Phase 5 - Delivery
assignee: ''
agent: ''
history:
- timestamp: '2026-07-10T15:50:00Z'
  agent: hermes
  action: Prompt authored from committed spec
agent_profile: implementer
authoritative_surface: .github/workflows/
create_intent:
- .github/workflows/ci.yml
- .github/workflows/release.yml
execution_mode: code_change
owned_files:
- .github/workflows/ci.yml
- .github/workflows/release.yml
- Dockerfile
- README.md
- config.example.yaml
- docker-compose.yml
- go.mod
- go.sum
tags: []
---
# WP06 – CI, multi-architecture image, and public documentation

Implements FR-025, FR-026, FR-027.

Add PR CI for gofmt, tests, race, vet, binary and Docker builds without registry login. Add explicit semver-tag Docker Hub publication using the existing `DOCKER_USERNAME` and `DOCKER_PASSWORD` secrets, never automatic publication of untrusted PR code. Use Go 1.26.5, CA certificates, non-root runtime, healthcheck, and amd64/arm64 Buildx. Update public examples with generic placeholders only. Run full gates and workflow syntax checks where available.

## Subtasks and evidence

- **T020:** Add pinned-action PR CI for formatting, tests, race, vet, build, and container health without registry credentials; validate with Actionlint.
- **T021:** Harden and lint the pinned multi-stage Dockerfile for static amd64/arm64 builds, CA certificates, non-root runtime, minimal context, and image healthcheck; validate with Hadolint and CI image execution.
- **T022:** Add pinned-action semver release publication, prerelease-safe tags, SBOM/provenance, and existing Docker secret names; verify no PR path receives registry credentials.
- **T023:** Update README/examples/contracts with every setting, generic public data, release/live-validation limits, and Go-upgrade rationale; run module verification, staticcheck, gosec, govulncheck, Gitleaks, and all build gates.
