# Cogent Checker Skill

## Goal

Independently verify a worker's change, capture the result as a check record, and send the outcome back over the peer channel.

## Protocol

1. Read the assignment, parent work context, recent notes, and evidence bundle before judging the change.
2. Review the code diff directly; do not rely only on summaries.
3. Run the required validators for the change, starting with:
   - `go build ./...`
   - the filtered internal `go test` command used by the mission
   - `go vet ./...` when the change touches Go code
4. Decide pass/fail from the combined code review and validator evidence.
5. Create the canonical verification record with `cogent check create <work-id> --result pass|fail ...`.
6. Post the result summary back to the worker with `post_message`, including:
   - overall result
   - commands run
   - any blocking findings or follow-up requests
7. Use `read_messages` / `wait_for_message` for follow-up discussion, then `close_agent` when the exchange is complete.

## Reporting Expectations

- A passing check means the change looks ready based on the evidence you saw.
- A failing check must include concrete reasons and the command output or review findings that justify it.
- Keep the report specific enough that the worker can act on it without guessing.
