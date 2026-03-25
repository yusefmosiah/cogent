# User Testing

Testing-surface findings and validator guidance for the readiness mission.

**What belongs here:** authoritative user-facing surfaces, validation tools, runtime setup, concurrency guidance, and known limitations.
**What does NOT belong here:** implementation details that belong in architecture or environment notes.

---

## Validation Surface

Authoritative surfaces for this mission:

- CLI
- local `fase serve` HTTP/API surface
- browser UI via `agent-browser`

### Dry-run readiness results

- `make build`: passed during planning dry run
- `make lint`: passed during planning dry run
- focused readiness-area tests: passed during planning dry run
- local `fase serve` was started successfully on port `5380`
- API smoke succeeded against:
  - `/api/runtime`
  - `/api/dashboard`
  - `/api/work/ready`
  - `/api/git/status`
- browser validation succeeded using `agent-browser` against the served UI, including interactive snapshot and annotated screenshot capture

### Known limitations at mission start

- `make test` is red at baseline and is reserved as an end-of-mission gate unless a feature explicitly targets full-suite stabilization earlier
- Final readiness baseline should still aim to make `make test` pass before mission completion

## Validation Concurrency

- Machine observed during planning: 8 logical CPUs, 16 GiB RAM
- Focused validation cost: medium
- Full repo `make test` cost: heavy

Recommended limits:

- Focused validation: up to **2** concurrent lanes
- Full `make test`: **1** concurrent lane (serialize)

## Tooling Guidance

- For UI/browser validation, use `agent-browser` and capture an annotated screenshot
- For runtime validation, prefer one temporary local `fase serve` instance at a time inside the mission port range `5380-5389`

## Flow Validator Guidance: contract-freeze

### Isolation Rules

Contract-freeze assertions test work-item lifecycle and attestation/review contract behavior. All assertions share the same work-graph state and should run in a single validator to avoid interference.

### Resources and Boundaries

- Use the already-running `fase serve` on port `5380`
- Do NOT start additional serve instances
- Work items created for testing should use distinct identifiers to avoid collision
- Clean up any test work items after validation (or leave them for synthesis inspection)

### Assertions to Test

- **VAL-CONTRACT-003**: Review contract freezes and can only become stricter after first live execution. Verify that once a work item begins first live execution, its review contract becomes durable and cannot be weakened. Stricter changes must flow through an explicit audited escalation path.
- **VAL-CONTRACT-004**: Terminal success is gated by one frozen completion contract. Verify that terminal success cannot be recorded until all blocking review requirements are satisfied, and exactly one canonical path owns final success authorization.
- **VAL-CONTRACT-005**: Code, docs, and persisted work-graph state share one explicit precedence rule. Verify that README, ADR/spec docs, and CLI help text describe one explicit precedence rule for resolving conflicts between runtime code, committed docs, and persisted work-graph state.

### Testing Approach

1. Use CLI commands to create and manipulate work items with attestation requirements
2. Use HTTP API to inspect work item state and attestation records
3. Verify frozen contract behavior by attempting to weaken review requirements after first dispatch
4. Verify escalation path by attempting stricter changes and checking for audited escalation records
5. Verify precedence rule by inspecting committed docs and comparing to runtime help text

## Flow Validator Guidance: supervisor-wake-causality

### Isolation Rules

Supervisor wake assertions share the same runtime event stream and work graph, so validate them in one serialized lane to avoid cross-talk from shared state or overlapping supervisor turns.

### Resources and Boundaries

- Use the already-running `fase serve` on port `5380`
- Do NOT start additional serve instances
- Prefer CLI and HTTP/API assertions for wake/provenance traces; only use browser UI if an assertion unexpectedly requires it
- Keep all test work under a unique, milestone-scoped prefix so provenance traces stay easy to attribute

### Assertions to Test

- **VAL-SUPERVISOR-001**: Only actionable events wake the supervisor.
- **VAL-SUPERVISOR-002**: Supervisor-originated mutations never self-wake.
- **VAL-SUPERVISOR-003**: Wake-relevant events carry trustworthy provenance across CLI, HTTP, and MCP transport boundaries.
- **VAL-SUPERVISOR-004**: External worker, checker, attestation, host, and housekeeping signals wake exactly when needed.
- **VAL-SUPERVISOR-005**: Idle suppression, burst batching, and recovery avoid churn without losing context.
- **VAL-SUPERVISOR-006**: Self-wake suppression never hides later legitimate external events.

### Testing Approach

1. Use CLI commands to create or mutate work items and trigger supervisor-relevant events
2. Use HTTP API snapshots and event traces to confirm provenance and wake behavior
3. Verify supervisor turn counts and event logs to prove no self-wake loops or missed actionable wakeups
4. Keep the entire milestone in a single validator run so event ordering remains deterministic
