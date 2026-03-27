# Environment

Environment variables, external dependencies, and setup notes.

**What belongs here:** required env vars, toolchain assumptions, local-only setup notes.
**What does NOT belong here:** service ports or commands (use `.factory/services.yaml`).

---

- No external credentials or third-party services are required for this mission
- Go 1.25.x is the expected toolchain
- `staticcheck` is available at `/Users/wiz/go/bin/staticcheck`
- Workers should operate from `/Users/wiz/cogent`
