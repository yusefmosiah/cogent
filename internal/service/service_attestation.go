package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yusefmosiah/cogent/internal/core"
)

func (s *Service) CreateCheckRecord(ctx context.Context, req CheckRecordCreateRequest) (core.CheckRecord, error) {
	if req.WorkID == "" {
		return core.CheckRecord{}, fmt.Errorf("%w: work_id must not be empty", ErrInvalidInput)
	}
	if req.Result != "pass" && req.Result != "fail" {
		return core.CheckRecord{}, fmt.Errorf("%w: result must be 'pass' or 'fail'", ErrInvalidInput)
	}

	work, err := s.store.GetWorkItem(ctx, req.WorkID)
	if err != nil {
		return core.CheckRecord{}, normalizeStoreError("work", req.WorkID, err)
	}

	req.Report = normalizeCheckReport(req.Report)

	if req.Result == "pass" {
		if !req.Report.BuildOK {
			return core.CheckRecord{}, fmt.Errorf("%w: passing check records must set build_ok=true", ErrInvalidInput)
		}
		if req.Report.TestsFailed > 0 {
			return core.CheckRecord{}, fmt.Errorf("%w: passing check records cannot report failed tests", ErrInvalidInput)
		}
		if strings.TrimSpace(req.Report.TestOutput) == "" {
			return core.CheckRecord{}, fmt.Errorf("%w: passing check records must include test_output with reproducible command provenance", ErrInvalidInput)
		}
		if strings.TrimSpace(req.Report.CheckerNotes) == "" {
			return core.CheckRecord{}, fmt.Errorf("%w: passing check records must include checker_notes describing verified evidence", ErrInvalidInput)
		}
		if checkRecordNeedsUIEvidence(work, req.Report) && len(req.Report.Screenshots) == 0 {
			return core.CheckRecord{}, fmt.Errorf("%w: passing UI checks must include at least one existing screenshot path", ErrInvalidInput)
		}
		for _, deliverablePath := range objectiveDeliverablePaths(work.Objective) {
			if !checkReportMentionsPath(req.Report, deliverablePath) {
				return core.CheckRecord{}, fmt.Errorf("%w: passing check records must mention verified deliverable path %q in notes, diff, test output, or artifact paths", ErrInvalidInput, deliverablePath)
			}
		}
	}

	// Persist screenshots and videos from the check to a durable artifacts directory.
	// This ensures they remain reviewable even after a worktree is cleaned up.
	if req.Report.Screenshots, err = s.prepareCheckArtifactPaths(ctx, req.WorkID, req.Report.Screenshots); err != nil {
		return core.CheckRecord{}, err
	}
	if req.Report.Videos, err = s.prepareCheckArtifactPaths(ctx, req.WorkID, req.Report.Videos); err != nil {
		return core.CheckRecord{}, err
	}
	if err := s.persistCheckTextArtifacts(ctx, req.WorkID, req.Report); err != nil {
		return core.CheckRecord{}, err
	}

	rec := core.CheckRecord{
		CheckID:      core.GenerateID("chk"),
		WorkID:       req.WorkID,
		CheckerModel: req.CheckerModel,
		WorkerModel:  req.WorkerModel,
		Result:       req.Result,
		Report:       req.Report,
		CreatedAt:    time.Now().UTC(),
	}
	if err := s.store.CreateCheckRecord(ctx, rec); err != nil {
		return core.CheckRecord{}, err
	}
	s.Events.Publish(WorkEvent{
		Kind:   WorkEventCheckRecorded,
		WorkID: req.WorkID,
		State:  req.Result,
		Actor:  ActorFromCreatedBy(req.CreatedBy),
		Cause:  CauseCheckRecorded,
		Metadata: map[string]string{
			"check_id": rec.CheckID,
			"result":   req.Result,
		},
	})
	return rec, nil
}

func (s *Service) GetCheckRecord(ctx context.Context, checkID string) (core.CheckRecord, error) {
	rec, err := s.store.GetCheckRecord(ctx, checkID)
	if err != nil {
		return core.CheckRecord{}, normalizeStoreError("check_record", checkID, err)
	}
	return rec, nil
}

func checkRecordNeedsUIEvidence(work core.WorkItemRecord, report core.CheckReport) bool {
	if workNeedsUIVerification(work) {
		return true
	}
	return checkerUIEvidencePattern.MatchString(report.DiffStat)
}

func normalizeCheckReport(report core.CheckReport) core.CheckReport {
	const maxTestOutput = 50 * 1024
	if len(report.TestOutput) > maxTestOutput {
		report.TestOutput = report.TestOutput[:maxTestOutput] + "\n[truncated]"
	}
	report.TestOutput = strings.TrimSpace(report.TestOutput)
	report.DiffStat = strings.TrimSpace(report.DiffStat)
	report.CheckerNotes = strings.TrimSpace(report.CheckerNotes)
	return report
}

func objectiveDeliverablePaths(objective string) []string {
	seen := make(map[string]struct{})
	var paths []string
	for _, field := range strings.Fields(objective) {
		candidate := strings.Trim(field, " \t\r\n\"'`()[]{}<>,;:!?")
		if candidate == "" || strings.Contains(candidate, "://") || !strings.Contains(candidate, "/") {
			continue
		}
		base := filepath.Base(candidate)
		if base == "" || base == "." || !strings.Contains(base, ".") {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		paths = append(paths, candidate)
	}
	return paths
}

func checkReportMentionsPath(report core.CheckReport, path string) bool {
	if path == "" {
		return true
	}
	for _, text := range []string{report.TestOutput, report.DiffStat, report.CheckerNotes} {
		if strings.Contains(text, path) {
			return true
		}
	}
	for _, artifactPath := range append(append([]string{}, report.Screenshots...), report.Videos...) {
		if strings.Contains(artifactPath, path) {
			return true
		}
	}
	return false
}

func (s *Service) prepareCheckArtifactPaths(ctx context.Context, workID string, srcPaths []string) ([]string, error) {
	if len(srcPaths) == 0 {
		return nil, nil
	}

	var missing []string
	for _, path := range srcPaths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err != nil {
			missing = append(missing, path)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%w: check artifact paths do not exist: %s", ErrInvalidInput, strings.Join(missing, ", "))
	}
	paths, err := s.persistCheckScreenshots(ctx, workID, srcPaths)
	if err != nil {
		return nil, err
	}
	return paths, nil
}

func (s *Service) ListCheckRecords(ctx context.Context, workID string, limit int) ([]core.CheckRecord, error) {
	return s.store.ListCheckRecords(ctx, workID, limit)
}

func (s *Service) checkArtifactDir(ctx context.Context, workID string) string {
	if projectRoot := s.findProjectRoot(ctx, workID); projectRoot != "" {
		return filepath.Join(projectRoot, ".cogent", "artifacts", workID)
	}
	return filepath.Join(s.Paths.StateDir, "artifacts", workID)
}

// persistCheckScreenshots copies screenshot/video files from their source paths to a durable
// artifacts directory and returns the persisted paths.
func (s *Service) persistCheckScreenshots(ctx context.Context, workID string, srcPaths []string) ([]string, error) {
	if len(srcPaths) == 0 {
		return nil, nil
	}

	destDir := filepath.Join(s.checkArtifactDir(ctx, workID), "screenshots")
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return nil, fmt.Errorf("create check artifact dir: %w", err)
	}

	var newPaths []string
	filenameCounts := make(map[string]int)
	for _, srcPath := range srcPaths {
		srcPath = strings.TrimSpace(srcPath)
		if srcPath == "" {
			continue
		}

		data, err := os.ReadFile(srcPath)
		if err != nil {
			return nil, fmt.Errorf("read check artifact %q: %w", srcPath, err)
		}

		filename := filepath.Base(srcPath)
		if filename == "" || filename == "." {
			filename = "artifact"
		}
		if count := filenameCounts[filename]; count > 0 {
			ext := filepath.Ext(filename)
			stem := strings.TrimSuffix(filename, ext)
			filename = fmt.Sprintf("%s-%d%s", stem, count+1, ext)
		}
		filenameCounts[filepath.Base(srcPath)]++

		destPath := filepath.Join(destDir, filename)
		if err := os.WriteFile(destPath, data, 0644); err != nil {
			return nil, fmt.Errorf("persist check artifact %q: %w", srcPath, err)
		}
		newPaths = append(newPaths, destPath)
	}

	return newPaths, nil
}

func (s *Service) persistCheckTextArtifacts(ctx context.Context, workID string, report core.CheckReport) error {
	artifactDir := s.checkArtifactDir(ctx, workID)
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return fmt.Errorf("create check artifact dir: %w", err)
	}

	files := map[string]string{
		"go-test-output.txt": report.TestOutput,
		"diff-stat.txt":      report.DiffStat,
		"checker-notes.md":   report.CheckerNotes,
	}
	for name, content := range files {
		if strings.TrimSpace(content) == "" {
			continue
		}
		if err := os.WriteFile(filepath.Join(artifactDir, name), []byte(content), 0o644); err != nil {
			return fmt.Errorf("persist check artifact %q: %w", name, err)
		}
	}
	return nil
}

// CreateCheckRecordDirect is an acyclic bridge for the native adapter's in-process tool registration.
// It accepts only core and primitive types so the native adapter can define a matching interface
// without importing the service package (which would create an import cycle).
// The createdBy parameter enables proper provenance tracking for supervisor vs worker calls.
func (s *Service) CreateCheckRecordDirect(ctx context.Context, workID, result, checkerModel, workerModel string, report core.CheckReport, createdBy string) (core.CheckRecord, error) {
	if createdBy == "" {
		createdBy = "worker"
	}
	return s.CreateCheckRecord(ctx, CheckRecordCreateRequest{
		WorkID:       workID,
		Result:       result,
		CheckerModel: checkerModel,
		WorkerModel:  workerModel,
		Report:       report,
		CreatedBy:    createdBy,
	})
}
