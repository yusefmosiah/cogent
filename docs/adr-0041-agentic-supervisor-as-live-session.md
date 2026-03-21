# ADR-0041: Agentic Supervisor as a LiveSession — Delete Deterministic Loop

**Status:** Accepted
**Date:** 2026-03-21

## Context

The current supervisor is a Go loop (`supervisor_event_driven.go`, `supervisor_loop.go`, `supervisor_routing.go`) with hardcoded scoring, rotation pools, kind affinity, health tracking, circuit breakers, retry backoff, and budget filtering. It's 1500+ lines of deterministic dispatch logic that we keep debugging and patching.

Meanwhile, we have the live adapter protocol — `LiveAgentAdapter`/`LiveSession` — implemented for claude, codex, opencode, and pi. Every adapter supports persistent sessions, tool use, and steering. The supervisor should be a FASE agent running on any of these adapters, not a Go loop.

ADR-0040 already removed edge-based dispatch filtering. This ADR completes the transition: the supervisor is an LLM session with FASE tools.

## Decision

### Supervisor = LiveSession

`fase serve --auto` starts a `LiveSession` on a configurable adapter (default: claude/claude-sonnet-4-6). The session receives a supervisor system prompt hydrated by `project_hydrate`. The LLM reads the work graph, reasons about dependencies and parallelism, dispatches work by spawning co-agent sessions, monitors progress via events, and attests completed work.

### Hydration Strategy

The supervisor session is cold-started with `project_hydrate` output as its system context. This replaces CLAUDE.md/MEMORY.md.

**`project_hydrate` for supervisor must include:**
1. **Role prompt**: "You are the FASE supervisor. Your job is to dispatch ready work items to worker agents, monitor their progress, and attest completed work."
2. **Conventions**: Project-specific rules from convention notes
3. **Ready queue**: All ready work items with title, objective summary, priority, preferred adapters/models
4. **Active work**: Currently claimed/in-progress items
5. **Pending attestations**: Work awaiting review
6. **Available adapters**: Which adapters are available and their capabilities
7. **Contract**: Available FASE tools and how to use them
8. **Dispatch instructions**: How to claim work, hydrate a briefing, spawn a worker session, monitor via events

**Token budget:** `project_hydrate` should target ~4K tokens for the supervisor role. The supervisor can call `work_show` for details on specific items. The hydration is a summary, not a dump.

### Dispatch Flow

The supervisor LLM decides everything the Go loop used to decide:

```
1. Call ready_work → see what's available
2. Reason: "Phase 1 has priority 10, needs codex/gpt-5.4. Phase 2 depends on Phase 1's output. Dispatch Phase 1 only."
3. Call work_claim on Phase 1
4. Call work_hydrate to get the worker briefing
5. Spawn a co-agent session: adapter=codex, model=gpt-5.4, prompt=briefing
6. Monitor co-agent events for completion
7. On completion: review output, call work_attest
8. Check ready_work again, dispatch next item
```

### Steering for Real-Time Updates

The supervisor session subscribes to work graph events via the EventBus. When a worker completes, fails, or stalls, a steer message is injected into the supervisor session:

```
[fase:steer] Work item work_XYZ completed. Worker output: "All tests pass."
Please review and attest, then check for next dispatchable work.
```

This replaces the polling heartbeat and process exit watchers.

### What Gets Deleted

| File | Lines | Purpose |
|------|-------|---------|
| `supervisor_event_driven.go` | ~720 | Event-driven dispatch loop, scoring, retry |
| `supervisor_loop.go` | ~370 | Polling dispatch loop |
| `supervisor_routing.go` | ~200 | Health tracking, scoring, circuit breakers |
| `supervisor.go` (partial) | ~300 | Rotation pool, fallback routing, state writing |
| `budget.go` (partial) | ~150 | Daily usage tracking, budget filtering |

**Replaced by:** ~100 lines in `serve.go` that start a LiveSession with the supervisor prompt and wire EventBus events to steer messages.

### Configuration

```toml
[supervisor]
adapter = "claude"           # any live adapter
model = "claude-sonnet-4-6"  # model for the supervisor
hydrate_mode = "standard"    # project_hydrate mode
```

Or via flags: `fase serve --auto --supervisor-adapter claude --supervisor-model claude-sonnet-4-6`

### What Stays

- **`fase serve`**: HTTP server, web UI, MCP endpoint — unchanged
- **`project_hydrate`**: Enhanced to include supervisor-specific context
- **Live adapter infrastructure**: All adapters, sessions, events — unchanged
- **Work graph**: Items, attestations, notes, claims — unchanged
- **EventBus**: Used to steer the supervisor session

## Implementation

### Phase 1: Supervisor Session Startup
1. Add supervisor config (adapter, model) to serve command
2. On `--auto`, start a LiveSession on the configured adapter
3. Inject `project_hydrate` output as the initial turn
4. Wire EventBus → steer channel for the supervisor session

### Phase 2: Supervisor Prompt Engineering
5. Write the supervisor system prompt: role, tools, dispatch protocol, attestation protocol
6. Tune `project_hydrate` for supervisor context (~4K tokens, focused on actionable state)
7. Test supervisor dispatch loop end-to-end

### Phase 3: Cleanup
8. Delete `supervisor_event_driven.go`, `supervisor_loop.go`, `supervisor_routing.go`
9. Remove deterministic dispatch code from `supervisor.go`
10. Remove `--max-concurrent`, `--default-adapter` flags (supervisor decides)
11. Update `serve.go` to use the new supervisor session

## Consequences

- **Simpler codebase**: ~1500 lines of Go dispatch logic replaced by ~100 lines + a prompt
- **Intelligent dispatch**: LLM reasons about dependencies, parallelism, rate limits
- **Adapter-agnostic**: Supervisor runs on any adapter — can switch between claude/codex/opencode
- **Observable**: Supervisor's reasoning is visible in its output events (web UI shows it thinking)
- **Flexible**: Adding new dispatch strategies = editing the prompt, not Go code
- **Cost**: Supervisor LLM calls add cost (~$0.01-0.05 per dispatch cycle with sonnet)
- **Latency**: LLM reasoning adds 2-5s per dispatch decision vs <1ms for Go loop
- **Risk**: LLM may make suboptimal dispatch decisions — mitigated by conventions in hydration
