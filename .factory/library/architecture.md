# Architecture

Key architectural decisions and patterns for the cogent (formerly fase) codebase.

**What belongs here:** Architectural decisions, discovered patterns, module boundaries.
**What does NOT belong here:** Service ports/commands (use `.factory/services.yaml`).

---

## Module Structure

- `cmd/cogent/` (formerly `cmd/fase/`) — CLI entry point (cobra)
- `internal/service/` — Core service (~8800 lines), work lifecycle, briefings, notifications
- `internal/store/` — SQLite persistence layer (~4000 lines)
- `internal/core/` — Types, constants, work states
- `internal/adapters/native/` — Active multi-LLM adapter (GLM, GPT, Claude, Gemini)
- `internal/adapters/*/` — Deprecated subprocess adapters (do not modify)
- `internal/notify/` — Email via Resend API, digest collector
- `internal/mcpserver/` — MCP server (tools being disabled, channel relay kept)
- `internal/cli/` — CLI commands, serve.go (HTTP/WS server, housekeeping)
- `mind-graph/` — Poincaré disk visualization UI
- `skills/` — Worker/checker skill markdown files (loaded at runtime)

## Key Patterns

- Work items form a graph (parent-child, dependency edges) stored in SQLite
- Jobs are execution attempts on work items (multiple jobs per work)
- Event bus (`EventBus`) for internal notifications
- Housekeeping loop in serve.go: 30s tick for WAL checkpoint, lease reconciliation, stall/orphan detection; hourly tick for digest flush
- Config from TOML file + environment variables
- State directory (`.cogent/`, formerly `.fase/`): SQLite DB, supervisor brief, raw stdout

## Peer Agent Channel Pattern

The async peer coagent system (introduced in core-simplification) uses a background auto-loop pattern:

1. **Spawn**: `spawn_agent` starts a goroutine (`runAgent`) for the peer agent
2. **Auto-loop**: The goroutine continuously waits for channel messages via `wait_for_message`, processes each with a new LLM turn, and posts responses back via `post_message`
3. **Close**: `close_agent` cancels the context and shuts down the goroutine

This replaces the old synchronous `send_turn` pattern. The peer agent automatically responds to all non-self messages in the channel until closed. Key files: `internal/adapters/native/channel.go` (ChannelManager, AgentChannel), `internal/adapters/native/tools_coagent.go` (tool implementations, runAgent goroutine).
