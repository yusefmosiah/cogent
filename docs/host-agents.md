# cagent for Host Agents

`cagent` is designed to be called by another coding agent as a local subprocess.

Use it when the host agent wants:
- one stable JSON CLI instead of vendor-specific command lines,
- durable local sessions and artifacts,
- same-vendor continuation through `send`,
- explicit cross-vendor failover through `transfer`.

## Recommended host workflow

1. Query runtime inventory:

```bash
cagent runtime --json
```

2. Choose an adapter based on:
- `enabled`
- `available`
- capability flags
- operator-provided traits like `speed`, `cost`, and `tags`

3. Launch work:

```bash
cagent run --json --adapter codex --cwd /repo --prompt "Fix the failing tests."
```

`run` queues background work and returns immediately with a job id and session id.

4. Poll or inspect:

```bash
cagent status --json <job-id>
cagent logs --json <job-id>
cagent session --json <session-id>
```

5. Continue same-vendor work:

```bash
cagent send --json --session <session-id> --prompt "Continue from the last result."
```

`send` also queues background work and returns a new job id immediately.

6. Transfer to another adapter only when native continuation is not possible:

```bash
cagent transfer export --json --job <job-id> --reason "anthropic outage" --mode recovery
cagent transfer run --json --transfer <transfer-id-or-path> --adapter codex --cwd /repo
```

`transfer run` should follow the same background-job contract as `run`.

## Important behavior

- `cagent` does not perform vendor auth flows.
- `cagent` preserves native session IDs and raw vendor output.
- `runtime --json` is the preferred machine-facing inventory command.
- Use `status`, `logs --follow`, `session`, and `cancel` as the control surface after launch.
- Treat `transfer` as explicit failover/recovery, not as the normal orchestration path.
- The transfer prompt should explicitly disclose the source adapter and reason for transfer.
