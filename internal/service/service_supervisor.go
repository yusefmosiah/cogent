package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/yusefmosiah/cogent/internal/core"
)

func (s *Service) DiscoverWork(ctx context.Context, sourceWorkID, title, objective, kind, rationale string) (*core.WorkProposalRecord, error) {
	if _, err := s.store.GetWorkItem(ctx, sourceWorkID); err != nil {
		return nil, normalizeStoreError("work", sourceWorkID, err)
	}
	if strings.TrimSpace(title) == "" || strings.TrimSpace(objective) == "" {
		return nil, fmt.Errorf("%w: title and objective must not be empty", ErrInvalidInput)
	}
	if strings.TrimSpace(kind) == "" {
		kind = "task"
	}
	proposal := core.WorkProposalRecord{
		ProposalID:   core.GenerateID("wprop"),
		ProposalType: "promote_discovery",
		State:        "proposed",
		SourceWorkID: sourceWorkID,
		Rationale:    strings.TrimSpace(rationale),
		ProposedPatch: map[string]any{
			"title":     strings.TrimSpace(title),
			"objective": strings.TrimSpace(objective),
			"kind":      strings.TrimSpace(kind),
		},
		Metadata:  map[string]any{"discovered": true},
		CreatedBy: "service",
		CreatedAt: time.Now().UTC(),
	}
	if err := s.store.CreateWorkProposal(ctx, proposal); err != nil {
		return nil, err
	}
	return &proposal, nil
}

func (s *Service) CreateWorkProposal(ctx context.Context, req WorkProposalCreateRequest) (*core.WorkProposalRecord, error) {
	proposalType := strings.TrimSpace(req.ProposalType)
	if proposalType == "" {
		return nil, fmt.Errorf("%w: proposal type must not be empty", ErrInvalidInput)
	}
	if req.TargetWorkID != "" {
		if _, err := s.store.GetWorkItem(ctx, req.TargetWorkID); err != nil {
			return nil, normalizeStoreError("work", req.TargetWorkID, err)
		}
	}
	if req.SourceWorkID != "" {
		if _, err := s.store.GetWorkItem(ctx, req.SourceWorkID); err != nil {
			return nil, normalizeStoreError("work", req.SourceWorkID, err)
		}
	}
	proposal := core.WorkProposalRecord{
		ProposalID:    core.GenerateID("wprop"),
		ProposalType:  proposalType,
		State:         "proposed",
		TargetWorkID:  req.TargetWorkID,
		SourceWorkID:  req.SourceWorkID,
		Rationale:     strings.TrimSpace(req.Rationale),
		ProposedPatch: cloneMap(req.Patch),
		Metadata:      cloneMap(req.Metadata),
		CreatedBy:     req.CreatedBy,
		CreatedAt:     time.Now().UTC(),
	}
	if err := s.store.CreateWorkProposal(ctx, proposal); err != nil {
		return nil, err
	}
	return &proposal, nil
}

func (s *Service) ListWorkProposals(ctx context.Context, req WorkProposalListRequest) ([]core.WorkProposalRecord, error) {
	return s.store.ListWorkProposals(ctx, req.Limit, req.State, req.TargetWorkID, req.SourceWorkID)
}

func (s *Service) GetWorkProposal(ctx context.Context, proposalID string) (*core.WorkProposalRecord, error) {
	proposal, err := s.store.GetWorkProposal(ctx, proposalID)
	if err != nil {
		return nil, normalizeStoreError("proposal", proposalID, err)
	}
	return &proposal, nil
}

func (s *Service) ReviewWorkProposal(ctx context.Context, proposalID, decision string) (*core.WorkProposalRecord, *core.WorkItemRecord, error) {
	proposal, err := s.store.GetWorkProposal(ctx, proposalID)
	if err != nil {
		return nil, nil, normalizeStoreError("proposal", proposalID, err)
	}
	now := time.Now().UTC()
	switch decision {
	case "accept":
		proposal.State = "accepted"
		proposal.ReviewedBy = "service"
		proposal.ReviewedAt = &now
		var created *core.WorkItemRecord
		switch proposal.ProposalType {
		case "promote_discovery":
			item, err := s.createWorkFromPatch(ctx, proposal, now)
			if err != nil {
				return nil, nil, err
			}
			if proposal.SourceWorkID != "" {
				if err := s.store.CreateWorkEdge(ctx, core.WorkEdgeRecord{
					EdgeID:     core.GenerateID("wedge"),
					FromWorkID: item.WorkID,
					ToWorkID:   proposal.SourceWorkID,
					EdgeType:   "discovered_from",
					CreatedBy:  "service",
					CreatedAt:  now,
					Metadata:   map[string]any{},
				}); err != nil {
					return nil, nil, err
				}
			}
			proposal.TargetWorkID = item.WorkID
			created = item
		case "create_child":
			parentID := proposal.TargetWorkID
			if parentID == "" {
				parentID = proposal.SourceWorkID
			}
			if parentID == "" {
				return nil, nil, fmt.Errorf("%w: create_child requires target or source work id", ErrInvalidInput)
			}
			item, err := s.createWorkFromPatch(ctx, proposal, now)
			if err != nil {
				return nil, nil, err
			}
			if err := s.attachParentEdge(ctx, parentID, item.WorkID, "service", now, map[string]any{}, false); err != nil {
				return nil, nil, err
			}
			proposal.TargetWorkID = item.WorkID
			created = item
		case "add_edge":
			if err := s.applyAddEdgeProposal(ctx, proposal, now); err != nil {
				return nil, nil, err
			}
		case "remove_edge":
			if err := s.applyRemoveEdgeProposal(ctx, proposal); err != nil {
				return nil, nil, err
			}
		case "change_acceptance":
			if err := s.applyChangeAcceptanceProposal(ctx, proposal, now); err != nil {
				return nil, nil, err
			}
		case "reparent_work":
			if err := s.applyReparentProposal(ctx, proposal, now); err != nil {
				return nil, nil, err
			}
		case "supersede_work":
			item, err := s.applySupersedeProposal(ctx, proposal, now)
			if err != nil {
				return nil, nil, err
			}
			created = item
		case "escalate_contract":
			if err := s.applyEscalateContractProposal(ctx, proposal, now); err != nil {
				return nil, nil, err
			}
		}
		if err := s.store.UpdateWorkProposal(ctx, proposal); err != nil {
			return nil, nil, err
		}
		return &proposal, created, nil
	case "reject":
		proposal.State = "rejected"
		proposal.ReviewedBy = "service"
		proposal.ReviewedAt = &now
		if err := s.store.UpdateWorkProposal(ctx, proposal); err != nil {
			return nil, nil, err
		}
		return &proposal, nil, nil
	default:
		return nil, nil, fmt.Errorf("%w: decision must be accept or reject", ErrInvalidInput)
	}
}

func (s *Service) createWorkFromPatch(ctx context.Context, proposal core.WorkProposalRecord, now time.Time) (*core.WorkItemRecord, error) {
	title := summaryString(proposal.ProposedPatch, "title")
	objective := summaryString(proposal.ProposedPatch, "objective")
	kind := summaryString(proposal.ProposedPatch, "kind")
	if kind == "" {
		kind = "task"
	}
	if title == "" || objective == "" {
		return nil, fmt.Errorf("%w: proposal patch requires title and objective", ErrInvalidInput)
	}
	item := core.WorkItemRecord{
		WorkID:             core.GenerateID("work"),
		Title:              title,
		Objective:          objective,
		Kind:               kind,
		ExecutionState:     core.WorkExecutionStateReady,
		ApprovalState:      core.WorkApprovalStateNone,
		LockState:          core.WorkLockStateUnlocked,
		ConfigurationClass: summaryString(proposal.ProposedPatch, "configuration_class"),
		BudgetClass:        summaryString(proposal.ProposedPatch, "budget_class"),
		Metadata:           map[string]any{"proposal_id": proposal.ProposalID},
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	item.RequiredAttestations = resolvedRequiredAttestations(item, nil)
	if err := s.store.CreateWorkItem(ctx, item); err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *Service) applyAddEdgeProposal(ctx context.Context, proposal core.WorkProposalRecord, now time.Time) error {
	fromWorkID := summaryString(proposal.ProposedPatch, "from_work_id")
	toWorkID := summaryString(proposal.ProposedPatch, "to_work_id")
	edgeType := summaryString(proposal.ProposedPatch, "edge_type")
	if fromWorkID == "" || toWorkID == "" || edgeType == "" {
		return fmt.Errorf("%w: add_edge requires from_work_id, to_work_id, and edge_type", ErrInvalidInput)
	}
	if _, err := s.store.GetWorkItem(ctx, fromWorkID); err != nil {
		return normalizeStoreError("work", fromWorkID, err)
	}
	if _, err := s.store.GetWorkItem(ctx, toWorkID); err != nil {
		return normalizeStoreError("work", toWorkID, err)
	}
	if edgeType == "parent_of" {
		return s.attachParentEdge(ctx, fromWorkID, toWorkID, "service", now, cloneMap(proposal.Metadata), false)
	}
	return s.store.CreateWorkEdge(ctx, core.WorkEdgeRecord{
		EdgeID:     core.GenerateID("wedge"),
		FromWorkID: fromWorkID,
		ToWorkID:   toWorkID,
		EdgeType:   edgeType,
		CreatedBy:  "service",
		CreatedAt:  now,
		Metadata:   cloneMap(proposal.Metadata),
	})
}

func (s *Service) applyRemoveEdgeProposal(ctx context.Context, proposal core.WorkProposalRecord) error {
	edgeID := summaryString(proposal.ProposedPatch, "edge_id")
	if edgeID != "" {
		return s.store.DeleteWorkEdge(ctx, edgeID)
	}
	fromWorkID := summaryString(proposal.ProposedPatch, "from_work_id")
	toWorkID := summaryString(proposal.ProposedPatch, "to_work_id")
	edgeType := summaryString(proposal.ProposedPatch, "edge_type")
	edges, err := s.store.ListWorkEdges(ctx, 100, edgeType, fromWorkID, toWorkID)
	if err != nil {
		return err
	}
	if len(edges) == 0 {
		return fmt.Errorf("%w: no matching edge found", ErrNotFound)
	}
	for _, edge := range edges {
		if err := s.store.DeleteWorkEdge(ctx, edge.EdgeID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) applyChangeAcceptanceProposal(ctx context.Context, proposal core.WorkProposalRecord, now time.Time) error {
	targetID := proposal.TargetWorkID
	if targetID == "" {
		return fmt.Errorf("%w: change_acceptance requires target work id", ErrInvalidInput)
	}
	work, err := s.store.GetWorkItem(ctx, targetID)
	if err != nil {
		return normalizeStoreError("work", targetID, err)
	}
	for key, value := range proposal.ProposedPatch {
		work.Acceptance[key] = value
	}
	work.UpdatedAt = now
	return s.store.UpdateWorkItem(ctx, work)
}

func (s *Service) applyReparentProposal(ctx context.Context, proposal core.WorkProposalRecord, now time.Time) error {
	targetID := proposal.TargetWorkID
	newParentID := summaryString(proposal.ProposedPatch, "parent_work_id")
	if targetID == "" || newParentID == "" {
		return fmt.Errorf("%w: reparent_work requires target work id and parent_work_id", ErrInvalidInput)
	}
	if _, err := s.store.GetWorkItem(ctx, targetID); err != nil {
		return normalizeStoreError("work", targetID, err)
	}
	if _, err := s.store.GetWorkItem(ctx, newParentID); err != nil {
		return normalizeStoreError("work", newParentID, err)
	}
	if err := s.validateParentEdge(ctx, newParentID, targetID, true); err != nil {
		return err
	}
	existing, err := s.store.ListWorkEdges(ctx, 100, "parent_of", "", targetID)
	if err != nil {
		return err
	}
	for _, edge := range existing {
		if err := s.store.DeleteWorkEdge(ctx, edge.EdgeID); err != nil {
			return err
		}
	}
	return s.attachParentEdge(ctx, newParentID, targetID, "service", now, map[string]any{}, true)
}

func (s *Service) applySupersedeProposal(ctx context.Context, proposal core.WorkProposalRecord, now time.Time) (*core.WorkItemRecord, error) {
	targetID := proposal.TargetWorkID
	if targetID == "" {
		return nil, fmt.Errorf("%w: supersede_work requires target work id", ErrInvalidInput)
	}
	if _, err := s.store.GetWorkItem(ctx, targetID); err != nil {
		return nil, normalizeStoreError("work", targetID, err)
	}
	replacementID := summaryString(proposal.ProposedPatch, "existing_work_id")
	var created *core.WorkItemRecord
	if replacementID == "" {
		item, err := s.createWorkFromPatch(ctx, proposal, now)
		if err != nil {
			return nil, err
		}
		created = item
		replacementID = item.WorkID
	} else {
		if _, err := s.store.GetWorkItem(ctx, replacementID); err != nil {
			return nil, normalizeStoreError("work", replacementID, err)
		}
	}
	if err := s.store.CreateWorkEdge(ctx, core.WorkEdgeRecord{
		EdgeID:     core.GenerateID("wedge"),
		FromWorkID: replacementID,
		ToWorkID:   targetID,
		EdgeType:   "supersedes",
		CreatedBy:  "service",
		CreatedAt:  now,
		Metadata:   map[string]any{},
	}); err != nil {
		return nil, err
	}
	return created, nil
}

// applyEscalateContractProposal adds stricter attestation requirements to a frozen work item.
// This is the explicit audited escalation path for VAL-CONTRACT-003: post-freeze changes
// may only make the contract stricter, and must flow through this explicit path rather than
// silent in-place mutation.
// Per ADR-0036: escalation fields (EscalatedAt, EscalationBy, EscalationReason) are set on
// newly added attestation slots to distinguish them from original slots.
func (s *Service) applyEscalateContractProposal(ctx context.Context, proposal core.WorkProposalRecord, now time.Time) error {
	targetID := proposal.TargetWorkID
	if targetID == "" {
		return fmt.Errorf("%w: escalate_contract requires target work id", ErrInvalidInput)
	}

	work, err := s.store.GetWorkItem(ctx, targetID)
	if err != nil {
		return normalizeStoreError("work", targetID, err)
	}

	// Check that the contract is frozen - escalation only allowed after first execution
	if work.AttestationFrozenAt == nil {
		return fmt.Errorf("%w: contract escalation requires work to have started execution first", ErrInvalidInput)
	}

	// Get new attestation requirements from the proposal
	newAttestations := summaryAttestations(proposal.ProposedPatch, "required_attestations")
	if len(newAttestations) == 0 {
		return fmt.Errorf("%w: escalate_contract requires required_attestations in patch", ErrInvalidInput)
	}

	// Validate that the new requirements are stricter (not weaker)
	if !isStricterContract(work.RequiredAttestations, newAttestations) {
		return fmt.Errorf("%w: contract escalation must add stricter requirements, not weaken existing contract", ErrInvalidInput)
	}

	// Build a set of existing attestation keys to identify which are new (need escalation fields)
	existingKeys := make(map[string]bool)
	for _, att := range work.RequiredAttestations {
		key := att.VerifierKind + ":" + att.Method
		existingKeys[key] = true
	}

	// Set escalation fields on new attestation slots only (per ADR-0036)
	// Original slots retain nil escalation fields; new slots get them populated
	escalationTime := now
	escalationBy := proposal.CreatedBy
	escalationReason := proposal.Rationale

	for i := range newAttestations {
		key := newAttestations[i].VerifierKind + ":" + newAttestations[i].Method
		if !existingKeys[key] {
			// This is a new attestation slot - set escalation fields
			newAttestations[i].EscalatedAt = &escalationTime
			newAttestations[i].EscalationBy = escalationBy
			newAttestations[i].EscalationReason = escalationReason
		}
	}

	// Apply the stricter requirements and record the escalation
	work.RequiredAttestations = newAttestations
	work.UpdatedAt = now

	// Record escalation in metadata for audit trail
	if work.Metadata == nil {
		work.Metadata = map[string]any{}
	}
	work.Metadata["contract_escalated_at"] = now.Format(time.RFC3339)
	work.Metadata["contract_escalation_proposal"] = proposal.ProposalID

	if err := s.store.UpdateWorkItem(ctx, work); err != nil {
		return err
	}

	return nil
}

// isStricterContract checks if new requirements are stricter than existing ones.
// A stricter contract adds more required attestations or makes non-blocking ones blocking.
// Per ADR-0036: "A stricter contract adds more required attestations or makes non-blocking ones blocking"
func isStricterContract(existing, proposed []core.RequiredAttestation) bool {
	// Build a map of existing requirements by verifier_kind + method
	existingReqs := make(map[string]core.RequiredAttestation)
	for _, att := range existing {
		key := att.VerifierKind + ":" + att.Method
		existingReqs[key] = att
	}

	// Track if we found any stricter changes (new requirements or blocking-flag tightening)
	hasNewRequirement := false
	hasBlockingTightening := false

	// Check that all proposed requirements are accounted for
	for _, newAtt := range proposed {
		key := newAtt.VerifierKind + ":" + newAtt.Method
		if existingAtt, exists := existingReqs[key]; exists {
			// Existing requirement present - check we didn't make it less strict
			// (i.e., if it was blocking, it should still be blocking)
			if !existingAtt.Blocking && newAtt.Blocking {
				// Making a non-blocking requirement blocking is stricter - OK
				hasBlockingTightening = true
			} else if existingAtt.Blocking && !newAtt.Blocking {
				// Weakening: blocking → non-blocking is NOT allowed
				return false
			}
			// If both are blocking or both are non-blocking, that's fine (no change)
		} else {
			// New requirement - OK (adding new requirements is stricter)
			hasNewRequirement = true
		}
	}

	// Must have at least one stricter change: either new requirements or blocking-flag tightening
	// Also ensure we didn't remove any existing requirements
	if len(proposed) < len(existing) {
		// Proposed has fewer requirements than existing - we removed something (weakening)
		return false
	}

	// If same length, must have at least one blocking-flag tightening
	if len(proposed) == len(existing) && !hasBlockingTightening {
		return false
	}

	return hasNewRequirement || hasBlockingTightening
}

// summaryAttestations extracts RequiredAttestations from a proposal patch.
func summaryAttestations(patch map[string]any, key string) []core.RequiredAttestation {
	if patch == nil {
		return nil
	}
	val, ok := patch[key]
	if !ok {
		return nil
	}
	attestations, ok := val.([]any)
	if !ok {
		return nil
	}
	var result []core.RequiredAttestation
	for _, a := range attestations {
		attMap, ok := a.(map[string]any)
		if !ok {
			continue
		}
		var att core.RequiredAttestation
		if v, ok := attMap["verifier_kind"].(string); ok {
			att.VerifierKind = v
		}
		if v, ok := attMap["method"].(string); ok {
			att.Method = v
		}
		if v, ok := attMap["blocking"].(bool); ok {
			att.Blocking = v
		}
		if v, ok := attMap["metadata"].(map[string]any); ok {
			att.Metadata = v
		}
		result = append(result, att)
	}
	return result
}
