# User Testing

Validation surface and concurrency guidance for the strip/refactor mission.

---

## Validation Surface

**Primary surface:** CLI and shell-level source inspection.

Validators prove this mission by:
1. Running repo-wide commands (`go build ./...`, `go test`, `go vet ./...`, `staticcheck ./...`, `make install`)
2. Running installed-binary checks (`cogent --help`, `cogent version`)
3. Verifying source structure with `ls`, `rg`, `wc -l`, and `git diff --exit-code`

No browser, TUI, or external-service validation is required.

## Phase-specific command guidance

### Before strip completes
```bash
go build ./...
go list ./internal/... | grep -v 'github.com/yusefmosiah/cogent/internal/adapters/codex' | grep -v 'github.com/yusefmosiah/cogent/internal/mcpserver' | xargs env GOMAXPROCS=4 go test
go vet ./...
```

### After strip completes
```bash
go build ./...
GOMAXPROCS=4 go test ./internal/...
go vet ./...
```

### Final quality validation
```bash
go build ./...
GOMAXPROCS=4 go test ./internal/...
go vet ./...
make lint
/Users/wiz/go/bin/staticcheck ./...
make install
cogent --help
cogent version
git diff --exit-code -- internal/adapterapi
git diff --exit-code -- mind-graph
```

## Validation Concurrency

**Surface: CLI / shell**
- Estimated per validator cost: low CPU + low memory (single Go command or grep pipeline)
- Conservative parallelism: **5 concurrent validators max**
- Rationale: shell-based validation is lightweight; `GOMAXPROCS=4` already bounds test parallelism inside each validator process.

## Validator notes

- Use `/Users/wiz/cogent` as the working directory
- Treat `internal/adapterapi/` and `mind-graph/` as boundary assertions, not places to “fix” issues
- Capture command output as evidence files when a validator runs a milestone or final contract pass
