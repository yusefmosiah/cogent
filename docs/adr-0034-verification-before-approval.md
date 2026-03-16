Date: 2026-03-16
Kind: Architecture decision
Status: Proposed  
Priority: 1

## ADR-0005: Verification Before Human Approval

### Decision

Human approval is the last step, not the first review. Before any human
sees a change, the system must prove it works with no regressions via
automated verification at every level available to the project.

### Verification Ladder

Each level adds confidence. The supervisor climbs the ladder before
requesting human approval:

1. Build passes (cargo check / go build)
2. Unit tests pass (cargo test / go test)
3. Integration tests pass
4. Deploy to staging (NixOS, CI/CD)
5. E2E Playwright tests pass with video artifacts
6. Load tests pass (capacity, latency, concurrency)
7. Canary deploy to production (feature flags, % rollout)
8. A/B test metrics (error rate, latency p50/p99)

The supervisor records each level as an attestation. Only after all
required attestations pass does the work item move to awaiting_approval.

### The Human's Job

The human reviews attestation bundles, not code. They see:
- Build: passed
- Tests: 234/234 pass
- Staging deploy: healthy
- E2E video: 31/31 scenarios pass
- Load test: 60 VMs, p50 < 200ms

And approves. Or sees:
- E2E video: 29/31 pass, 2 failures in writer flow
And rejects with a note.

### Dev Sleeps, Wakes Up to Results

The overnight loop:
1. Supervisor claims work
2. Agent implements
3. Agent runs verification ladder
4. CI deploys to staging
5. E2E tests run with video capture
6. Load tests validate capacity
7. All attestations recorded
8. Work item moves to awaiting_approval
9. Dev wakes up, opens mind-graph, reviews attestation bundle
10. Approves or rejects

No code review by the human. The code was reviewed by the agent,
verified by tests, validated on staging. The human reviews evidence.

### Feature Flags and Canary Deploys

For user-facing changes:
- Dev/vibecoder: sees new features immediately (feature flag on)
- Canary users: 5% rollout, monitored for regressions
- All users: promoted after canary period with no regressions

The verification ladder extends to production:
- Deploy behind feature flag
- Monitor metrics for canary cohort
- Auto-promote if metrics are healthy
- Auto-rollback if regression detected
- Record promotion attestation with metrics evidence

### Living Documents as Software

The end state: everyone is a software developer. The form this takes
is living documents — computational essays with programmatic artifacts.

A living document is:
- Text that describes intent
- Code that implements the intent
- Artifacts that prove the intent was achieved (screenshots, videos, data)
- All versioned together in the work graph

The Writer agent manages this: the human writes text, the agent writes
code, the system produces artifacts, attestation verifies consistency.
Text that writes code that makes video — and the video is the proof.
