# Architecture

How cogent is structured today and what this mission is changing.

**What belongs here:** component boundaries, retained vs deprecated surfaces, refactor seams.
**What does NOT belong here:** service commands and ports (use `.factory/services.yaml`).

---

## Repository shape

- `cmd/cogent/` — CLI entrypoint
- `internal/cli/` — cobra commands, HTTP server, housekeeping, supervisor wiring
- `internal/service/` — primary service layer; `service.go` is the monolith being split
- `internal/store/` — SQLite persistence
- `internal/core/` — shared types/config/path resolution
- `internal/adapters/native/` — retained in-process adapter
- `internal/adapters/claude/`, `internal/adapters/pi_rust/`, `internal/adapters/registry.go` — retained adapter surfaces if still needed
- `internal/adapterapi/` — shared adapter contracts; mission boundary says preserve unchanged
- `mind-graph/` — UI surface; mission boundary says untouched

## Deprecated surfaces being stripped

Milestone 1 removes:
- `internal/mcpserver/`
- `internal/cli/mcp.go`
- `internal/adapters/codex/`
- `internal/adapters/factory/`
- `internal/adapters/pi/`
- `internal/adapters/gemini/`
- `internal/adapters/opencode/`
- module dependencies that only supported those deleted packages

Any retained surfaces that reference deleted packages must be cleaned in place rather than removed wholesale.

## Service refactor seams

Milestone 2 is a same-package extraction only. The target files are:
- `service_usage.go`
- `service_proof.go`
- `service_docs.go`
- `service_graph.go`
- `service_notify.go`
- `service_briefing.go`
- `service_attestation.go`
- `service_state.go`
- `service_work.go`
- `service_supervisor.go`
- `service_job.go`

`service.go` should end the mission as the core shell for the `Service` type, open/constructor logic, and shared helpers still used across the extracted files.

## Invariants for this mission

- `internal/adapterapi/` must not change
- `mind-graph/` must not change
- The refactor must preserve buildable call paths across `internal/cli/`, `internal/service/`, and retained adapters
- Validation is CLI/source based; no browser surface is required for this mission
