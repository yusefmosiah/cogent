package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/yusefmosiah/cogent/internal/core"
	"github.com/yusefmosiah/cogent/internal/store"
)

// Edge operations — direct, no proposal ceremony.

func (s *Service) CreateEdge(ctx context.Context, rec core.WorkEdgeRecord) error {
	// Validate both work items exist
	if _, err := s.store.GetWorkItem(ctx, rec.FromWorkID); err != nil {
		return normalizeStoreError("work", rec.FromWorkID, err)
	}
	if _, err := s.store.GetWorkItem(ctx, rec.ToWorkID); err != nil {
		return normalizeStoreError("work", rec.ToWorkID, err)
	}
	return s.store.CreateWorkEdge(ctx, rec)
}

func (s *Service) DeleteEdge(ctx context.Context, edgeID string) error {
	return s.store.DeleteWorkEdge(ctx, edgeID)
}

func (s *Service) ListEdges(ctx context.Context, limit int, edgeType, fromWorkID, toWorkID string) ([]core.WorkEdgeRecord, error) {
	return s.store.ListWorkEdges(ctx, limit, edgeType, fromWorkID, toWorkID)
}
func (s *Service) attachParentEdge(ctx context.Context, parentID, childID, createdBy string, createdAt time.Time, metadata map[string]any, allowReplace bool) error {
	if err := s.validateParentEdge(ctx, parentID, childID, allowReplace); err != nil {
		return err
	}
	return s.store.CreateWorkEdge(ctx, core.WorkEdgeRecord{
		EdgeID:     core.GenerateID("wedge"),
		FromWorkID: parentID,
		ToWorkID:   childID,
		EdgeType:   "parent_of",
		CreatedBy:  createdBy,
		CreatedAt:  createdAt,
		Metadata:   metadata,
	})
}

func (s *Service) validateParentEdge(ctx context.Context, parentID, childID string, allowReplace bool) error {
	if parentID == "" || childID == "" {
		return fmt.Errorf("%w: parent and child work ids must not be empty", ErrInvalidInput)
	}
	if parentID == childID {
		return fmt.Errorf("%w: parent edge cannot target the same work item", ErrInvalidInput)
	}
	existingParents, err := s.store.ListWorkEdges(ctx, 2, "parent_of", "", childID)
	if err != nil {
		return err
	}
	if len(existingParents) > 0 {
		if !allowReplace {
			return fmt.Errorf("%w: work item %s already has a parent", ErrInvalidInput, childID)
		}
		for _, edge := range existingParents {
			if edge.FromWorkID == parentID {
				return nil
			}
		}
	}
	current := parentID
	seen := map[string]bool{}
	for current != "" {
		if current == childID {
			return fmt.Errorf("%w: parent edge would create a cycle", ErrInvalidInput)
		}
		if seen[current] {
			return fmt.Errorf("%w: parent lineage already contains a cycle", ErrInvalidInput)
		}
		seen[current] = true
		edges, err := s.store.ListWorkEdges(ctx, 2, "parent_of", "", current)
		if err != nil {
			return err
		}
		if len(edges) == 0 {
			break
		}
		current = edges[0].FromWorkID
	}
	return nil
}

// RootWorkID walks parent edges from the given work item to find the root.
// Returns the workID of the root (the work item with no parent), or the
// input workID if it has no parent edge.
func (s *Service) RootWorkID(ctx context.Context, workID string) (string, error) {
	current := workID
	seen := map[string]bool{current: true}
	for {
		edges, err := s.store.ListWorkEdges(ctx, 2, "parent_of", "", current)
		if err != nil {
			return workID, err
		}
		if len(edges) == 0 {
			return current, nil
		}
		parentID := edges[0].FromWorkID
		if seen[parentID] {
			return current, nil
		}
		seen[parentID] = true
		current = parentID
	}
}

// ActiveRootWorkIDs returns the set of root work IDs that have at least one
// active (claimed or in_progress) work item in their subtree.
func (s *Service) ActiveRootWorkIDs(ctx context.Context) (map[string]bool, error) {
	items, err := s.store.ListWorkItems(ctx, 10000, "", "", "", false)
	if err != nil {
		return nil, err
	}
	activeRoots := map[string]bool{}
	for _, item := range items {
		if item.ExecutionState != core.WorkExecutionStateClaimed && item.ExecutionState != core.WorkExecutionStateInProgress {
			continue
		}
		rootID, rootErr := s.RootWorkID(ctx, item.WorkID)
		if rootErr != nil {
			continue
		}
		activeRoots[rootID] = true
	}
	return activeRoots, nil
}

// CountActiveRoots returns the number of distinct root work items that have
// active work in their subtree. Used for concurrency cap enforcement.
func (s *Service) CountActiveRoots(ctx context.Context) (int, error) {
	activeRoots, err := s.ActiveRootWorkIDs(ctx)
	if err != nil {
		return 0, err
	}
	return len(activeRoots), nil
}

// RenderWorkerBriefingMarkdown converts a worker briefing to a compact markdown
// document. Much more token-efficient than JSON for LLM consumption.

func (s *Service) firstRelatedWork(ctx context.Context, workID, edgeType string, outbound bool) (*core.WorkItemRecord, error) {
	items, err := s.relatedWork(ctx, workID, edgeType, outbound, 1)
	if err != nil || len(items) == 0 {
		return nil, err
	}
	return &items[0], nil
}

func (s *Service) relatedWork(ctx context.Context, workID, edgeType string, outbound bool, limit int) ([]core.WorkItemRecord, error) {
	var fromWorkID, toWorkID string
	if outbound {
		fromWorkID = workID
	} else {
		toWorkID = workID
	}
	edges, err := s.store.ListWorkEdges(ctx, limit, edgeType, fromWorkID, toWorkID)
	if err != nil {
		return nil, err
	}
	items := make([]core.WorkItemRecord, 0, len(edges))
	for _, edge := range edges {
		relatedID := edge.FromWorkID
		if outbound {
			relatedID = edge.ToWorkID
		}
		if relatedID == "" {
			continue
		}
		item, err := s.store.GetWorkItem(ctx, relatedID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *Service) listArtifactsForWork(ctx context.Context, workID string, limit int) ([]core.ArtifactRecord, error) {
	jobs, err := s.store.ListJobsByWork(ctx, workID, limit)
	if err != nil {
		return nil, err
	}
	if len(jobs) == 0 {
		return []core.ArtifactRecord{}, nil
	}
	artifacts := make([]core.ArtifactRecord, 0, limit)
	seen := map[string]bool{}
	for _, job := range jobs {
		jobArtifacts, err := s.store.ListArtifactsByJob(ctx, job.JobID, limit)
		if err != nil {
			return nil, err
		}
		for _, artifact := range jobArtifacts {
			if seen[artifact.ArtifactID] {
				continue
			}
			seen[artifact.ArtifactID] = true
			artifacts = append(artifacts, artifact)
			if len(artifacts) >= limit {
				return artifacts, nil
			}
		}
	}
	return artifacts, nil
}
