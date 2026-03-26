# Cogent Worker Skill

## Goal

Implement the assigned work item end to end, verify it locally, then close the loop with an independent checker before signaling completion.

## Protocol

1. Read the assignment carefully: objective, acceptance, recent notes, and attached evidence.
2. Make the smallest focused code change that satisfies the work item.
3. Run the relevant local verification before asking for review.
4. Spawn a peer checker through the channel tools when you need an independent verification pass:
   - `spawn_agent` a checker peer for the same work stream
   - `post_message` with a concise "please verify" summary, changed files, and commands already run
   - use `wait_for_message` / `read_messages` to collect findings
   - address issues and repeat until the result is acceptable
   - `close_agent` when verification is complete
5. Record useful findings and risks as work notes while you go.

## Verification Expectations

- Run build/tests/lint or the closest project validators relevant to your change.
- Do not ask the checker to replace your own local verification; use the checker as an independent second pass.
- If the checker reports a failure, fix the issue or explicitly report why it remains unresolved.

## Commit And Completion

- Commit using: `cogent(<scope>): <description>`
- On success, update the work item with `--execution-state done`.
- On failure, update the work item with `--execution-state failed`.
- Always send a final report summarizing files changed and verification results before exiting.
