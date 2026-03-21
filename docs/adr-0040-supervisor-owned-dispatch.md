# ADR-0040: Supervisor-Owned Dispatch — Remove Edge DAG, Supervisor Decides Order

**Status:** Accepted
**Date:** 2026-03-21

## Context

The current dispatch system has three layers of ordering:

1. **Graph edges** (`work_edges` table): `blocks`, `depends_on`, `supersedes` edge types that filter items out of the ready query via `NOT EXISTS` subqueries
2. **Position field**: integer ordering within the queue, with shift/move/reorder operations
3. **Priority field**: semantic tier (higher = more urgent)

This is too much machinery. In practice:
- Edges are high-friction to maintain and create invisible blocking — items silently disappear from `ready_work()` with no indication why
- Position ordering is redundant with priority
- The supervisor is a dumb consumer of `ready_work()` — it dispatches whatever the store returns, in order

Meanwhile, the agentic supervisor (ADR-0038) is an LLM that reads objectives, understands dependencies, and reasons about parallelism. It doesn't need the store to enforce ordering — it needs the store to track state, and it makes scheduling decisions itself.

## Decision

### Remove the edge system from dispatch

- **Keep** the `work_edges` table and CRUD operations — edges are useful for metadata (parent_of, related_to) and the graph visualization
- **Remove** edge filtering from `ListReadyWork` — the ready query becomes: `execution_state = 'ready' AND unclaimed AND not human_locked`
- **Remove** `depends_on` and `blocks` edge types from having any effect on dispatch
- **Keep** `supersedes` as metadata only (no query filtering)

### Simplify ReadyWork

The new `ListReadyWork` query:

```sql
SELECT ... FROM work_items wi
WHERE wi.execution_state = 'ready'
  AND (wi.claimed_by IS NULL OR wi.claimed_by = ''
       OR (wi.claimed_until IS NOT NULL AND wi.claimed_until <= ?))
  AND wi.lock_state <> 'human_locked'
ORDER BY wi.priority DESC, wi.position ASC, wi.updated_at DESC
LIMIT ?
```

No edge joins. No subqueries. The supervisor sees everything that's ready.

### Supervisor owns scheduling

The supervisor agent (LLM) decides:
- **What to dispatch**: reads objectives, infers dependencies ("Phase 2 needs Phase 1's interface")
- **How many to dispatch**: "these 3 are independent → dispatch in parallel; these 2 share files → sequence"
- **When to scale**: "3 VMs available, 3 independent items → use all 3"

### New dispatch API

```go
// AvailableWork returns all ready, unclaimed, unlocked work items.
// No edge filtering. Supervisor decides ordering.
func (s *Service) AvailableWork(ctx context.Context, limit int) ([]core.WorkItemRecord, error)

// DispatchBatch claims and dispatches multiple work items in parallel.
// Each entry specifies the adapter and model to use.
func (s *Service) DispatchBatch(ctx context.Context, items []DispatchRequest) ([]DispatchResult, error)

type DispatchRequest struct {
    WorkID  string
    Adapter string
    Model   string
}
```

### Keep position for default ordering

Position stays as a hint for the supervisor and for the CLI `work ready` display. But it doesn't gate dispatch — it's a suggestion, not a constraint.

## Implementation

1. Remove edge-based `NOT EXISTS` subqueries from `ListReadyWork`
2. Add `AvailableWork` as alias for the simplified `ReadyWork`
3. Update supervisor to call `AvailableWork` instead of `ReadyWork`
4. Keep edge CRUD, CLI commands, and visualization — just stop filtering on them

## Consequences

- **Supervisor unblocked**: All ready items are visible, supervisor reasons about order
- **Simpler store**: No edge joins in hot path query
- **Dynamic concurrency**: Supervisor decides parallelism per-cycle, not static config
- **Edge system preserved**: Graph visualization and metadata still work
- **Migration path**: When multi-VM lands, supervisor dispatches to specific VMs
