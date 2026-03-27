package service

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yusefmosiah/cogent/internal/core"
)

func (s *Service) SetDocContent(ctx context.Context, workID, path, title, body, format string) (*core.DocContentRecord, string, error) {
	if format == "" {
		format = "markdown"
	}
	path, err := s.normalizeDocPath(ctx, path)
	if err != nil {
		return nil, "", err
	}

	// Auto-create work item if none specified
	createdWork := false
	if workID == "" {
		// Check if a work item already exists for this doc path
		existing, err := s.store.GetDocContentByPath(ctx, path)
		if err == nil && existing != nil {
			workID = existing.WorkID
		} else {
			// Infer kind from path
			kind := "doc"
			if strings.Contains(path, "/decisions/") || strings.Contains(path, "adr-") {
				kind = "plan"
			} else if strings.Contains(path, "/guides/") {
				kind = "implement"
			} else if strings.Contains(path, "/reports/") || strings.Contains(path, "/snapshots/") {
				kind = "review"
			}

			// Infer title from content if not provided
			if title == "" {
				title = inferTitleFromMarkdown(body)
			}
			if title == "" {
				title = filepath.Base(path)
			}

			// Extract first paragraph as objective
			objective := path + ": " + extractFirstParagraph(body)

			work, err := s.CreateWork(ctx, WorkCreateRequest{
				Title:     title,
				Objective: objective,
				Kind:      kind,
				CreatedBy: "service",
			})
			if err != nil {
				return nil, "", fmt.Errorf("auto-create work item for doc: %w", err)
			}
			workID = work.WorkID
			createdWork = true
		}
	} else {
		if _, err := s.store.GetWorkItem(ctx, workID); err != nil {
			return nil, "", normalizeStoreError("work", workID, err)
		}
		existing, err := s.store.GetDocContentByPath(ctx, path)
		if err == nil && existing != nil && existing.WorkID != workID {
			return nil, "", fmt.Errorf("%w: doc path %s is already linked to work %s", ErrInvalidInput, path, existing.WorkID)
		}
	}

	rec := core.DocContentRecord{
		DocID:  core.GenerateID("doc"),
		WorkID: workID,
		Path:   path,
		Title:  title,
		Body:   body,
		Format: format,
	}
	if err := s.store.UpsertDocContent(ctx, rec); err != nil {
		return nil, workID, err
	}
	_ = createdWork // could return this to caller
	if stored, err := s.store.GetDocContentByPath(ctx, path); err == nil && stored != nil {
		enriched := s.enrichDocRecord(ctx, *stored)
		return &enriched, workID, nil
	}
	enriched := s.enrichDocRecord(ctx, rec)
	return &enriched, workID, nil
}

func inferTitleFromMarkdown(body string) string {
	for _, line := range strings.SplitN(body, "\n", 30) {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimPrefix(line, "# ")
		}
		if strings.HasPrefix(line, "## ") {
			return strings.TrimPrefix(line, "## ")
		}
	}
	return ""
}

func extractFirstParagraph(body string) string {
	lines := strings.Split(body, "\n")
	var para []string
	inContent := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed == "---" {
			if inContent && len(para) > 0 {
				break
			}
			continue
		}
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "Date:") ||
			strings.HasPrefix(trimmed, "Kind:") || strings.HasPrefix(trimmed, "Status:") ||
			strings.HasPrefix(trimmed, "Priority:") || strings.HasPrefix(trimmed, "Requires:") {
			continue
		}
		inContent = true
		para = append(para, trimmed)
	}
	result := strings.Join(para, " ")
	if len(result) > 300 {
		result = result[:300] + "..."
	}
	return result
}

func (s *Service) GetDocContent(ctx context.Context, workID string) ([]core.DocContentRecord, error) {
	docs, err := s.store.GetDocContent(ctx, workID)
	if err != nil {
		return nil, err
	}
	for i := range docs {
		docs[i] = s.enrichDocRecord(ctx, docs[i])
	}
	return docs, nil
}

func (s *Service) normalizeRequiredDocPaths(ctx context.Context, raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(raw))
	paths := make([]string, 0, len(raw))
	for _, candidate := range raw {
		path, err := s.normalizeDocPath(ctx, candidate)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid required doc path %q: %v", ErrInvalidInput, candidate, err)
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	return paths, nil
}

func (s *Service) normalizeDocPath(ctx context.Context, raw string) (string, error) {
	path := strings.TrimSpace(raw)
	if path == "" {
		return "", fmt.Errorf("%w: document path must not be empty", ErrInvalidInput)
	}

	if filepath.IsAbs(path) {
		repoRoot := s.docRepoRoot(ctx)
		if repoRoot == "" {
			return "", fmt.Errorf("%w: cannot resolve repo-relative path for %s", ErrInvalidInput, path)
		}
		rel, err := filepath.Rel(repoRoot, filepath.Clean(path))
		if err != nil {
			return "", fmt.Errorf("%w: resolve repo-relative path for %s: %v", ErrInvalidInput, path, err)
		}
		path = rel
	}

	path = filepath.Clean(path)
	if path == "." || path == "" {
		return "", fmt.Errorf("%w: document path must not be empty", ErrInvalidInput)
	}
	if path == ".." || strings.HasPrefix(path, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("%w: document path %s must stay within the repository", ErrInvalidInput, raw)
	}
	return filepath.ToSlash(path), nil
}

func (s *Service) docRepoRoot(ctx context.Context) string {
	base := strings.TrimSpace(s.Paths.StateDir)
	if base != "" {
		if root, err := gitMainRepoRoot(ctx, base); err == nil && root != "" {
			return root
		}
		if filepath.Base(base) == ".cogent" {
			return filepath.Dir(base)
		}
	}
	if cwd, err := os.Getwd(); err == nil && strings.TrimSpace(cwd) != "" {
		if root, err := gitMainRepoRoot(ctx, cwd); err == nil && root != "" {
			return root
		}
		return cwd
	}
	return ""
}

func (s *Service) enrichDocRecord(ctx context.Context, rec core.DocContentRecord) core.DocContentRecord {
	repoRoot := s.docRepoRoot(ctx)
	if repoRoot == "" {
		return rec
	}
	absolute := filepath.Join(repoRoot, filepath.FromSlash(rec.Path))
	info, err := os.Stat(absolute)
	if err != nil || info.IsDir() {
		rec.RepoFileExists = false
		rec.MatchesRepo = false
		return rec
	}
	rec.RepoFileExists = true
	data, err := os.ReadFile(absolute)
	if err != nil {
		rec.MatchesRepo = false
		return rec
	}
	rec.MatchesRepo = bytes.Equal(data, []byte(rec.Body))
	return rec
}
