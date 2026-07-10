---
work_package_id: WP07
title: Reviewed candidate, private deployment, and live Palworld validation
dependencies:
- WP06
requirement_refs:
- FR-028
- FR-029
- FR-030
- NFR-009
tracker_refs: []
planning_base_branch: feat/palworld-1.0-snooze
merge_target_branch: feat/palworld-1.0-snooze
branch_strategy: Planning artifacts for this mission were generated on feat/palworld-1.0-snooze. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into feat/palworld-1.0-snooze unless the human explicitly redirects the landing branch.
subtasks:
- T024
- T025
- T026
- T027
phase: Phase 6 - Review and live validation
assignee: ''
agent: ''
history:
- timestamp: '2026-07-10T15:50:00Z'
  agent: hermes
  action: Prompt authored from committed spec
agent_profile: reviewer
authoritative_surface: Cross-repository release evidence, deployment handoff, and current-client acceptance
create_intent: []
execution_mode: planning_artifact
owned_files: []
tags: []
---
# WP07 – Reviewed candidate, private deployment, and live Palworld validation

Implements FR-028, FR-029, FR-030.

Run all gates and obtain matching independent spec/security/concurrency approval for an immutable staged diff. Open the public PR and explicitly publish a candidate only after the configured Docker Hub secrets are verified. In the private deployment repository, preserve its existing UDP mapping, listen address, private gameplay backend, and `PTERO_API_TOKEN` mechanism; update the server ID to the deployment-provided replacement value, pin the candidate, and keep replicas at zero until publication, credential authorization, placement, and readiness prerequisites pass. Required evidence is: public PR URL and reviewed diff digest, release workflow run, immutable image digest and architecture manifest, private Compose PR URL and reviewed diff digest, Compose-schema result, deployment/service state, and live-test observations. Independently review and open that PR. Set one replica only at the controlled validation stage, prove health, Pterodactyl authorization, routing contract, and stopped backend, then send `CONNECT NOW`. Observe the complete live lifecycle; if the signature differs, update the spec first and repeat TDD/review/publish. Implementation may be complete while this WP remains blocked awaiting the user test; mission/release acceptance cannot.

## Subtasks and evidence

- **T024:** Stage the complete public diff, record its SHA-256, run every quality/security gate, and obtain independent spec, concurrency, and security approval for that exact digest.
- **T025:** Open the public PR, let CI pass, merge without force-push, create the reviewed candidate tag, and record the release run plus immutable amd64/arm64 image digest.
- **T026:** Validate and independently review the private Compose diff digest, prove credentials are unchanged and replicas remain zero, then open its PR with the image/public-PR evidence.
- **T027:** After explicit deployment authorization, verify Pterodactyl access, placement, health, routing, and stopped state; only then prompt the user to connect and record join/disconnect/idle-stop evidence. Keep this subtask blocked until the human test succeeds or is formally waived.
