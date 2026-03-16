---
name: cagent
description: Use cagent as a bash-callable subagent runtime for coding-agent CLIs. Prefer this when you need to choose among available adapters, launch work through one stable JSON CLI, continue same-vendor sessions, or hand work off across vendors.
---

# cagent

## When to use

Use `cagent` when you want a local control plane over installed coding-agent CLIs instead of talking to one vendor CLI directly.

Typical cases:
- choose among Codex, Claude, Pi, Gemini, Factory, or OpenCode at runtime,
- inspect discovered provider/model/auth-mode inventory before routing work,
- launch a task through one stable `--json` interface,
- participate in a durable `work` graph and coordinate through the work API,
- continue a same-vendor session through `send`,
- ask a still-live session to land the plane with `debrief`,
- export an explicit transfer bundle and launch it on another adapter when failover is required,
- inspect durable local session and artifact state.
- search canonical local history before falling back to vendor-native state.

## Core workflow

1. Inspect the local runtime first:

```bash
cagent runtime --json
cagent catalog sync --json
cagent catalog show --json
cagent catalog probe --json --adapter opencode --provider openai
cagent history search --json --query "deployment rollback"
```

2. Choose an adapter using:
- `enabled`
- `available`
- capability flags
- configured traits like `speed`, `cost`, `summary`, and `tags`
- best-effort provider/model pricing when discovered locally

3. Start work:

```bash
cagent run --json --adapter codex --cwd /path/to/repo --prompt "Fix the failing tests."
```

`run` always queues background work and returns immediately.

4. Use returned IDs for follow-up operations:

```bash
cagent status --json --wait <job-id>
cagent logs --json <job-id>
cagent session --json <session-id>
cagent artifacts list --json --job <job-id>
```

For work-runtime usage:

```bash
cagent work list --json                    # list all work items
cagent work show <work-id>                 # show details + docs + attestations
cagent work ready --json                   # list actionable work
cagent work create --title "..." --objective "..." --kind implement
cagent inbox "quick idea"                  # shorthand for work create --kind idea
cagent work update <work-id> --message "Started implementation"
cagent work complete <work-id> --message "Done"
cagent work note-add <work-id> --type finding --text "..."
cagent work private-note <work-id> --text "SSH creds..." --type credential  # gitignored DB
cagent work doc-set --file docs/adr-001.md                                 # auto-creates work item from doc
cagent work doc-set <work-id> --file docs/adr-001.md --title "ADR-001"     # attach doc to existing work item
cagent work attest <work-id> --result passed --summary "Tests pass" --verifier-kind deterministic --method test
cagent work claim <work-id> --claimant worker-a
cagent work release <work-id> --claimant worker-a
cagent work renew-lease <work-id> --claimant worker-a --lease 15m          # heartbeat
cagent work children <work-id> --json
cagent work discover <work-id> --title "..." --objective "..." --kind verify --rationale "..."
cagent work proposal create --type add_edge --target <work-id> --rationale "..." --patch '{"edge_type":"blocks","source_work_id":"<id>"}'
cagent work approve <work-id> --message "Approved"
cagent work reject <work-id> --message "Needs rework"
cagent work lock <work-id>                 # human lock (prevents agent claim)
cagent work unlock <work-id>
cagent reconcile --json                    # release orphaned work with expired leases
cagent artifacts attach --work <work-id> --path ./report.md --kind report
```

Supervisor (autonomous dispatch loop):

```bash
cagent supervisor --default-adapter codex --max-concurrent 1  # run forever, dispatch work
cagent supervisor --dry-run --json                            # show what would dispatch
cagent supervisor --cwd /path/to/repo                        # target a specific repo
```

The supervisor auto-bootstraps empty repos: if no work exists, it creates a bootstrap
work item from the repo's docs/README, dispatches an agent to analyze and create the
work graph. Each repo gets its own `.cagent/` directory (per-repo state isolation).

Split databases:
- `.cagent/cagent.db` — public work graph (tracked in git)
- `.cagent/cagent-private.db` — private notes, credentials (gitignored, never committed)

Doc-work coupling (IMPORTANT):
- Every doc MUST have a corresponding work item. Use `work doc-set` to guarantee this.
- `work doc-set --file <path>` without a work-id auto-creates a work item from the doc
- `work doc-set <work-id> --file <path>` attaches to an existing work item
- `work show` returns docs in the response
- The mind-graph UI renders docs in the detail panel
- Principle: documentation commits before execution commits (ADR-0002)

Child-work policy:
- create child work directly only for:
  - unexpected local work discovered during execution,
  - fanout work that can run in parallel with distinct bounded outputs,
  - sequential context isolation where the next step benefits from a fresh bounded context.
- create a child only when you can stay ignorant of implementation details and still name the required result, artifact, or attestation bundle up front.
- if the proposed child does not have a clear cheap verifier or attestation target, do not create it directly; create a proposal instead.
- do not create children just to offload thinking or explore vaguely. If scope may expand or verification is unclear, use `cagent work proposal create`.

5. Continue same-vendor work:

```bash
cagent send --json --session <session-id> --prompt "Continue from the last result."
```

`send` always queues a new background job against the existing native session.

6. Ask for a debrief when you need the live agent's own world model:

```bash
cagent debrief --json --session <session-id> --reason "prepare a debugging summary"
```

`debrief` queues a continuation job and produces a markdown artifact when it finishes.

7. Transfer only when native continuation is impossible or undesirable:

```bash
cagent transfer export --json --job <job-id> --reason "provider outage" --mode recovery
cagent transfer run --json --transfer <transfer-id-or-path> --adapter gemini --cwd /path/to/repo
```

`transfer run` should also return immediately with a queued background job.

## Operating rules

- Prefer `runtime --json` as the machine-facing inventory command.
- Prefer `catalog show --json` when choosing among providers/models and auth modes.
- Prefer `catalog probe --json` when listed models may not match actual plan entitlement.
- Prefer models with recent successful `catalog show` history over merely listed-but-unused ones.
- Prefer `status --json` when you need normalized token usage or cost for a completed job.
- Treat `cagent` as machine-facing first. Use `--json` unless a human-readable summary is explicitly better.
- Treat `work` as the source of truth; use prompts only as compiled hydration views over work state.
- If the host gives you an isolated runtime via inherited `CAGENT_*` env vars or a runtime-specific wrapper on `PATH`, use bare `cagent` so all graph mutations land in the same runtime.
- Treat `run`, `send`, and `transfer run` as launch operations, not blocking operations.
- Treat `debrief` as a debugging/recovery workflow, not a normal orchestration step.
- Use `status --wait` when you want `cagent` to own the polling loop.
- Use `artifacts show` to read a debrief or transfer artifact by id.
- Use `history search` to query prior canonical jobs, turns, events, and artifact content.
- Do not assume every adapter supports `send`; inspect capability flags first.
- Same-vendor continuation is `send`. Cross-vendor failover is `transfer`.
- Do not expect `cagent` to perform vendor auth flows for you.
- Persisted session history and raw artifacts are part of the intended model.
- Prefer fresh `run` jobs for normal orchestration; use `transfer` for failover/recovery.
- Treat `catalog show` as discovered inventory and `catalog probe` as a best-effort entitlement/runability check.
- Treat adapter-native history import as a future special case when the session was not created by `cagent`.
- Publish structured updates at phase boundaries, and use notes for findings, feedback, and recovery context.
- Use proposals for structural graph edits instead of silently rewriting the work graph.
- Use direct child creation only for unexpected work, fanout work, or sequential context isolation with bounded and easily verified outputs.
- If a possible child cannot be verified cheaply or clearly, propose it instead of creating it.
- Do not self-approve implementation work; verification and review are separate work.
- Before claiming a planning phase succeeded, verify the expected child work actually exists with `cagent work children` or `cagent work show`.
- Stay within the repo and declared target paths; do not run broad home-directory scans like `find /Users/...` when the target path is already known.
- Treat assigned work scope as a hard boundary. If you own scaffold work, finish scaffold, update work state, and stop instead of drifting into core UI implementation.
- Prefer explicit `cagent work update`, `cagent work note-add`, and `cagent work complete` calls over burying progress in tool transcripts.

## Adapter selection heuristics

- Prefer adapters with `enabled=true` and `available=true`.
- Treat estimated cost as a routing/debugging hint, not authoritative billing.
- Prefer adapters with `native_resume=true` when you expect iterative follow-up turns.
- Prefer adapters with `session_export=true` when durable native-session linkage matters.
