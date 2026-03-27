---
name: go-worker
description: Go development worker for deprecated-code stripping, same-package refactors, and repo quality cleanup
---

# Go Worker

NOTE: Startup and cleanup are handled by `worker-base`. This skill defines the WORK PROCEDURE.

## When to Use This Skill

Use for every feature in this mission: stripping deprecated Go packages/surfaces, same-package `internal/service/` extractions, and final static-analysis cleanup.

## Required Skills

None.

## Work Procedure

### 1. Understand the assigned feature
- Read the feature description, preconditions, expected behavior, and verification steps
- Inspect only the files needed for this feature using `Grep` and `Read`
- For strip features, enumerate every caller/import/reference before deleting anything
- For refactor features, identify the exact functions/helpers that move together

### 2. Plan the edit set
Before editing, explicitly identify:
- files to modify
- files/directories to delete (if any)
- verification commands you will run after the change

### 3. Implement narrowly
- **Strip features:** remove the deprecated code and all of its remaining references; preserve off-limits paths
- **Refactor features:** perform mechanical same-package extraction only; preserve signatures and behavior
- **Quality features:** update lint/staticcheck wiring, fix findings, then sweep dead code introduced by this mission

### 4. Verify immediately
Run the smallest complete validator set that matches the feature stage:

**Pre-strip / during strip**
```bash
go build ./...
go list ./internal/... | grep -v 'github.com/yusefmosiah/cogent/internal/adapters/codex' | grep -v 'github.com/yusefmosiah/cogent/internal/mcpserver' | xargs env GOMAXPROCS=4 go test
go vet ./...
```

**Post-strip / refactor**
```bash
go build ./...
GOMAXPROCS=4 go test ./internal/service/...
```
And when the feature requires it, finish with:
```bash
GOMAXPROCS=4 go test ./internal/...
go vet ./...
```

**Final quality / cleanup**
```bash
go build ./...
GOMAXPROCS=4 go test ./internal/...
go vet ./...
/Users/wiz/go/bin/staticcheck ./...
make install
cogent --help
cogent version
git diff --exit-code -- internal/adapterapi
git diff --exit-code -- mind-graph
```

### 5. Perform manual structural checks
Also run the feature’s specific verification steps such as:
- `ls`/`test -d` for deleted or preserved paths
- `rg` for removed imports/references
- `wc -l internal/service/service.go` for the final core reduction
- `git diff --exit-code -- internal/adapterapi` and `git diff --exit-code -- mind-graph` for boundary assertions

### 6. Handoff thoroughly
Your handoff must include:
- exactly what changed
- anything left undone (empty string if nothing)
- every command you ran with exit code and observation
- any manual/interactive structural checks you performed
- discovered issues if something outside the feature scope blocks full completion

## Example Handoff

```json
{
  "salientSummary": "Extracted the usage and cost-reporting helpers from internal/service/service.go into service_usage.go without changing behavior. service.go shrank by 780 lines and focused service tests remained green.",
  "whatWasImplemented": "Moved usage attribution helpers, catalog usage aggregation, cost estimation helpers, and their supporting private functions from internal/service/service.go to internal/service/service_usage.go. Updated imports in both files so the package still compiles without signature changes.",
  "whatWasLeftUndone": "",
  "verification": {
    "commandsRun": [
      {
        "command": "go build ./...",
        "exitCode": 0,
        "observation": "Repo builds cleanly after the extraction."
      },
      {
        "command": "GOMAXPROCS=4 go test ./internal/service/...",
        "exitCode": 0,
        "observation": "Focused service package tests passed after moving the usage helpers."
      }
    ],
    "interactiveChecks": [
      {
        "action": "Read internal/service/service_usage.go and internal/service/service.go to confirm only usage/cost helpers moved",
        "observed": "service_usage.go now contains the usage and pricing helpers, while service.go retains only shared core logic unrelated to usage."
      }
    ]
  },
  "tests": {
    "added": []
  },
  "discoveredIssues": []
}
```

## When to Return to Orchestrator

- Deleting a deprecated package reveals an unexpected dependency that requires a broader architectural decision
- The feature cannot satisfy the mission boundaries without editing `internal/adapterapi/` or `mind-graph/`
- A refactor extraction exposes cross-file coupling that no longer fits the planned extraction order
- Repo-wide validators fail for reasons clearly unrelated to the assigned feature and require reprioritization
