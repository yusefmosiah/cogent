# User Testing

Testing surface, required testing skills/tools, and resource cost classification.

---

## Validation Surface

**Primary surface:** CLI (terminal commands)

The cogent binary is a CLI tool. Validation involves:
1. Running CLI commands (`cogent --help`, `cogent version`, `cogent work list`, `cogent check create --help`, `cogent work attest --help`)
2. Running build/test validators (`go build ./...`, `go test`, `go vet ./...`)
3. Grepping source code for stale references or structural assertions
4. Reading specific code sections to verify structural changes

**Tools:** Shell command execution (no browser, no TUI framework). All validation is done via direct command execution and output inspection.

**Setup required:** `make install` to build and install the binary to `~/.local/bin/cogent`.

**No auth/login required.** CLI commands work without authentication for local operations.

## Validation Concurrency

**Surface: CLI/shell**
- Each validator instance: ~50 MB (Go binary execution + shell)
- Machine: 16 GB RAM, 8 CPU cores, ~10 GB available
- Max concurrent validators: **5**
- Rationale: CLI execution is extremely lightweight. 5 concurrent shell processes consume <250 MB total. Well within 70% headroom (7 GB).

## Flow Validator Guidance: CLI

**Isolation boundaries:**
- Most CLI commands (help, version, work list, check create) are read-only and can run concurrently without interference
- State directory tests (migration, state file operations) MUST use isolated temporary directories with custom HOME or working directory
- Build/test commands operate on the shared codebase but don't modify state - safe to run concurrently
- Code search operations (grep, file reading) are read-only - safe concurrent

**Constraints for safe concurrent testing:**
- Do NOT modify the actual ~/.cogent/ or /Users/wiz/fase/.cogent/ directory during testing
- For state directory migration tests, create a temporary directory and use HOME=<tempdir> or cd to isolated workdir
- Build/test commands should run from the repo root (/Users/wiz/fase) - no isolation needed
- GitHub repo checks are read-only via gh CLI - no isolation needed

**Evidence collection:**
- Save terminal output as .txt files in the evidence directory
- For grep results, save both the command and full output
- For migration tests, capture before/after ls output
