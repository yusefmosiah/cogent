package native

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/yusefmosiah/fase/internal/service"
)

func RegisterFASETools(registry *ToolRegistry, svc *service.Service) error {
	if svc == nil {
		return fmt.Errorf("fase service is nil")
	}
	for _, tool := range NewFASETools(svc) {
		if err := registry.Register(tool); err != nil {
			return err
		}
	}
	return nil
}

func NewFASETools(svc *service.Service) []Tool {
	return []Tool{
		newWorkListTool(svc),
		newWorkShowTool(svc),
		newWorkCreateTool(svc),
		newWorkUpdateTool(svc),
		newWorkAttestTool(svc),
		newWorkNoteAddTool(svc),
		newWorkClaimTool(svc),
		newReadyWorkTool(svc),
		newProjectHydrateTool(svc),
	}
}

func newWorkListTool(svc *service.Service) Tool {
	return toolFromFunc(
		"work_list",
		"List work items from the FASE queue.",
		jsonSchemaObject(map[string]any{
			"limit":            map[string]any{"type": "integer"},
			"kind":             map[string]any{"type": "string"},
			"execution_state":  map[string]any{"type": "string"},
			"approval_state":   map[string]any{"type": "string"},
			"include_archived": map[string]any{"type": "boolean"},
		}, nil, false),
		func(ctx context.Context, raw json.RawMessage) (string, error) {
			var req service.WorkListRequest
			if err := json.Unmarshal(raw, &req); err != nil {
				return "", fmt.Errorf("decode work_list args: %w", err)
			}
			result, err := svc.ListWork(ctx, req)
			if err != nil {
				return "", err
			}
			return jsonString(result)
		},
	)
}

func newWorkShowTool(svc *service.Service) Tool {
	type args struct {
		WorkID string `json:"work_id"`
	}
	return toolFromFunc(
		"work_show",
		"Show a work item with updates, notes, attestations, and related records.",
		jsonSchemaObject(map[string]any{
			"work_id": map[string]any{"type": "string"},
		}, []string{"work_id"}, false),
		func(ctx context.Context, raw json.RawMessage) (string, error) {
			var in args
			if err := json.Unmarshal(raw, &in); err != nil {
				return "", fmt.Errorf("decode work_show args: %w", err)
			}
			result, err := svc.Work(ctx, in.WorkID)
			if err != nil {
				return "", err
			}
			return jsonString(result)
		},
	)
}

func newWorkCreateTool(svc *service.Service) Tool {
	return toolFromFunc(
		"work_create",
		"Create a new work item in the FASE queue.",
		jsonSchemaObject(map[string]any{
			"title":                 map[string]any{"type": "string"},
			"objective":             map[string]any{"type": "string"},
			"kind":                  map[string]any{"type": "string"},
			"parent_work_id":        map[string]any{"type": "string"},
			"lock_state":            map[string]any{"type": "string"},
			"priority":              map[string]any{"type": "integer"},
			"position":              map[string]any{"type": "integer"},
			"configuration_class":   map[string]any{"type": "string"},
			"budget_class":          map[string]any{"type": "string"},
			"required_capabilities": stringArraySchema(),
			"required_model_traits": stringArraySchema(),
			"preferred_adapters":    stringArraySchema(),
			"forbidden_adapters":    stringArraySchema(),
			"preferred_models":      stringArraySchema(),
			"avoid_models":          stringArraySchema(),
			"required_attestations": map[string]any{"type": "array", "items": jsonSchemaObject(map[string]any{"verifier_kind": map[string]any{"type": "string"}, "method": map[string]any{"type": "string"}, "blocking": map[string]any{"type": "boolean"}, "metadata": map[string]any{"type": "object", "additionalProperties": true}}, nil, false)},
			"acceptance":            map[string]any{"type": "object", "additionalProperties": true},
			"metadata":              map[string]any{"type": "object", "additionalProperties": true},
			"head_commit_oid":       map[string]any{"type": "string"},
		}, []string{"title", "objective"}, false),
		func(ctx context.Context, raw json.RawMessage) (string, error) {
			var req service.WorkCreateRequest
			if err := json.Unmarshal(raw, &req); err != nil {
				return "", fmt.Errorf("decode work_create args: %w", err)
			}
			result, err := svc.CreateWork(ctx, req)
			if err != nil {
				return "", err
			}
			return jsonString(result)
		},
	)
}

func newWorkUpdateTool(svc *service.Service) Tool {
	return toolFromFunc(
		"work_update",
		"Record a work update and optionally change work state.",
		jsonSchemaObject(map[string]any{
			"work_id":         map[string]any{"type": "string"},
			"execution_state": map[string]any{"type": "string"},
			"approval_state":  map[string]any{"type": "string"},
			"lock_state":      map[string]any{"type": "string"},
			"phase":           map[string]any{"type": "string"},
			"message":         map[string]any{"type": "string"},
			"job_id":          map[string]any{"type": "string"},
			"session_id":      map[string]any{"type": "string"},
			"artifact_id":     map[string]any{"type": "string"},
			"metadata":        map[string]any{"type": "object", "additionalProperties": true},
			"created_by":      map[string]any{"type": "string"},
			"force_done":      map[string]any{"type": "boolean"},
		}, []string{"work_id"}, false),
		func(ctx context.Context, raw json.RawMessage) (string, error) {
			var req service.WorkUpdateRequest
			if err := json.Unmarshal(raw, &req); err != nil {
				return "", fmt.Errorf("decode work_update args: %w", err)
			}
			result, err := svc.UpdateWork(ctx, req)
			if err != nil {
				return "", err
			}
			return jsonString(result)
		},
	)
}

func newWorkAttestTool(svc *service.Service) Tool {
	return toolFromFunc(
		"work_attest",
		"Create an attestation for a work item.",
		jsonSchemaObject(map[string]any{
			"work_id":                   map[string]any{"type": "string"},
			"result":                    map[string]any{"type": "string", "enum": []any{"passed", "failed"}},
			"summary":                   map[string]any{"type": "string"},
			"artifact_id":               map[string]any{"type": "string"},
			"job_id":                    map[string]any{"type": "string"},
			"session_id":                map[string]any{"type": "string"},
			"method":                    map[string]any{"type": "string"},
			"verifier_kind":             map[string]any{"type": "string"},
			"verifier_identity":         map[string]any{"type": "string"},
			"confidence":                map[string]any{"type": "number"},
			"blocking":                  map[string]any{"type": "boolean"},
			"supersedes_attestation_id": map[string]any{"type": "string"},
			"metadata":                  map[string]any{"type": "object", "additionalProperties": true},
			"created_by":                map[string]any{"type": "string"},
			"nonce":                     map[string]any{"type": "string"},
			"signer_pubkey":             map[string]any{"type": "string"},
		}, []string{"work_id", "result"}, false),
		func(ctx context.Context, raw json.RawMessage) (string, error) {
			var req service.WorkAttestRequest
			if err := json.Unmarshal(raw, &req); err != nil {
				return "", fmt.Errorf("decode work_attest args: %w", err)
			}
			attestation, work, err := svc.AttestWork(ctx, req)
			if err != nil {
				return "", err
			}
			return jsonString(map[string]any{
				"attestation": attestation,
				"work":        work,
			})
		},
	)
}

func newWorkNoteAddTool(svc *service.Service) Tool {
	return toolFromFunc(
		"work_note_add",
		"Add a note to a work item.",
		jsonSchemaObject(map[string]any{
			"work_id":    map[string]any{"type": "string"},
			"note_type":  map[string]any{"type": "string"},
			"body":       map[string]any{"type": "string"},
			"metadata":   map[string]any{"type": "object", "additionalProperties": true},
			"created_by": map[string]any{"type": "string"},
		}, []string{"work_id", "body"}, false),
		func(ctx context.Context, raw json.RawMessage) (string, error) {
			var req service.WorkNoteRequest
			if err := json.Unmarshal(raw, &req); err != nil {
				return "", fmt.Errorf("decode work_note_add args: %w", err)
			}
			result, err := svc.AddWorkNote(ctx, req)
			if err != nil {
				return "", err
			}
			return jsonString(result)
		},
	)
}

func newWorkClaimTool(svc *service.Service) Tool {
	type args struct {
		WorkID       string `json:"work_id"`
		Claimant     string `json:"claimant"`
		LeaseSeconds int    `json:"lease_seconds,omitempty"`
	}
	return toolFromFunc(
		"work_claim",
		"Claim a work item for a claimant.",
		jsonSchemaObject(map[string]any{
			"work_id":       map[string]any{"type": "string"},
			"claimant":      map[string]any{"type": "string"},
			"lease_seconds": map[string]any{"type": "integer"},
		}, []string{"work_id", "claimant"}, false),
		func(ctx context.Context, raw json.RawMessage) (string, error) {
			var in args
			if err := json.Unmarshal(raw, &in); err != nil {
				return "", fmt.Errorf("decode work_claim args: %w", err)
			}
			req := service.WorkClaimRequest{
				WorkID:        in.WorkID,
				Claimant:      in.Claimant,
				LeaseDuration: time.Duration(in.LeaseSeconds) * time.Second,
			}
			result, err := svc.ClaimWork(ctx, req)
			if err != nil {
				return "", err
			}
			return jsonString(result)
		},
	)
}

func newReadyWorkTool(svc *service.Service) Tool {
	type args struct {
		Limit           int  `json:"limit,omitempty"`
		IncludeArchived bool `json:"include_archived,omitempty"`
	}
	return toolFromFunc(
		"ready_work",
		"List work items that are ready to run.",
		jsonSchemaObject(map[string]any{
			"limit":            map[string]any{"type": "integer"},
			"include_archived": map[string]any{"type": "boolean"},
		}, nil, false),
		func(ctx context.Context, raw json.RawMessage) (string, error) {
			var in args
			if len(strings.TrimSpace(string(raw))) > 0 {
				if err := json.Unmarshal(raw, &in); err != nil {
					return "", fmt.Errorf("decode ready_work args: %w", err)
				}
			}
			result, err := svc.ReadyWork(ctx, in.Limit, in.IncludeArchived)
			if err != nil {
				return "", err
			}
			return jsonString(result)
		},
	)
}

func newProjectHydrateTool(svc *service.Service) Tool {
	return toolFromFunc(
		"project_hydrate",
		"Compile a project-scoped FASE briefing.",
		jsonSchemaObject(map[string]any{
			"mode":   map[string]any{"type": "string"},
			"format": map[string]any{"type": "string"},
		}, nil, false),
		func(ctx context.Context, raw json.RawMessage) (string, error) {
			var req service.ProjectHydrateRequest
			if len(strings.TrimSpace(string(raw))) > 0 {
				if err := json.Unmarshal(raw, &req); err != nil {
					return "", fmt.Errorf("decode project_hydrate args: %w", err)
				}
			}
			result, err := svc.ProjectHydrate(ctx, req)
			if err != nil {
				return "", err
			}
			return jsonString(result)
		},
	)
}

func stringArraySchema() map[string]any {
	return map[string]any{
		"type":  "array",
		"items": map[string]any{"type": "string"},
	}
}
