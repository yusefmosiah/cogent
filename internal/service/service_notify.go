package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/yusefmosiah/cogent/internal/core"
	"github.com/yusefmosiah/cogent/internal/notify"
)

func (s *Service) sendAttestationNotification(_ context.Context, work core.WorkItemRecord, attestation core.AttestationRecord) {
	// Skip internal work items (attest children, cleanup tasks).
	if strings.EqualFold(work.Kind, "attest") || strings.EqualFold(work.Kind, "task") {
		return
	}
	event := "check_fail"
	if attestation.Result == "passed" {
		event = "check_pass"
	}
	s.DigestCollector.Collect(digestItemForWork(work, event, firstNonEmpty(attestation.Summary, formatAttestationDigestSummary(attestation), work.Objective)))
}

// sendWorkFailureNotification collects a "failed" event into the digest.
func (s *Service) sendWorkFailureNotification(_ context.Context, work core.WorkItemRecord, message string) {
	s.DigestCollector.Collect(digestItemForWork(work, "failed", firstNonEmpty(message, work.Objective, "Work failed.")))
}

func digestItemForWork(work core.WorkItemRecord, event, summary string) notify.DigestItem {
	return notify.DigestItem{
		Time:      time.Now(),
		WorkID:    work.WorkID,
		Title:     work.Title,
		Objective: work.Objective,
		Event:     event,
		Summary:   strings.TrimSpace(summary),
	}
}

func formatAttestationDigestSummary(attestation core.AttestationRecord) string {
	result := strings.TrimSpace(attestation.Result)
	if result == "" {
		result = "updated"
	}
	summary := fmt.Sprintf("Attestation %s", result)
	if verifier := strings.TrimSpace(attestation.VerifierKind); verifier != "" {
		summary += " by " + verifier
	}
	if method := strings.TrimSpace(attestation.Method); method != "" {
		summary += " via " + method
	}
	return summary + "."
}

// SendSpecEscalationEmail emails the human when a work item has failed checks 3+ times.
func (s *Service) SendSpecEscalationEmail(ctx context.Context, workID, summary, recommendation string) {
	apiKey := os.Getenv("RESEND_API_KEY")
	to := os.Getenv("EMAIL_TO")
	if apiKey == "" || to == "" {
		return
	}
	work, err := s.store.GetWorkItem(ctx, workID)
	if err != nil {
		return
	}
	subject := fmt.Sprintf("[Cogent] spec question: %s", work.Title)
	html := notify.BuildSpecEscalationEmail(s.notificationProofBundle(ctx, work), summary, recommendation)
	notify.SendEmail(ctx, apiKey, to, subject, html, nil)
}

// FlushDigest sends the accumulated digest email if there are any collected events.
// Called periodically by the housekeeping timer.
func (s *Service) FlushDigest(ctx context.Context) {
	s.DigestCollector.Flush(ctx)
}
func (s *Service) notificationProofBundle(ctx context.Context, work core.WorkItemRecord) notify.ProofBundle {
	result, err := s.Work(ctx, work.WorkID)
	if err != nil {
		log.Printf("debug: notificationProofBundle fallback for work %s: %v", work.WorkID, err)
		return notify.ProofBundle{Work: work}
	}
	return notify.ProofBundle{
		Work:         result.Work,
		CheckRecords: result.CheckRecords,
		Attestations: result.Attestations,
		Artifacts:    result.Artifacts,
		Docs:         result.Docs,
	}
}

func formatProofBundleRefs(bundle notify.ProofBundle) string {
	parts := []string{
		fmt.Sprintf("work=%s", bundle.Work.WorkID),
		fmt.Sprintf("state=%s", bundle.Work.ExecutionState),
		fmt.Sprintf("approval=%s", bundle.Work.ApprovalState),
	}
	if refs := checkRefs(bundle.CheckRecords); len(refs) > 0 {
		parts = append(parts, "checks="+strings.Join(refs, ","))
	}
	if refs := proofBundleAttestationRefs(bundle.Attestations); len(refs) > 0 {
		parts = append(parts, "attestations="+strings.Join(refs, ","))
	}
	if refs := proofBundleArtifactRefs(bundle.Artifacts); len(refs) > 0 {
		parts = append(parts, "artifacts="+strings.Join(refs, ","))
	}
	if refs := proofBundleDocRefs(bundle.Docs); len(refs) > 0 {
		parts = append(parts, "docs="+strings.Join(refs, ","))
	}
	return strings.Join(parts, " ")
}

func checkRefs(records []core.CheckRecord) []string {
	if len(records) == 0 {
		return nil
	}
	limit := min(3, len(records))
	refs := make([]string, 0, limit)
	for _, record := range records[:limit] {
		refs = append(refs, fmt.Sprintf("%s(%s)", record.CheckID, record.Result))
	}
	return refs
}

func proofBundleAttestationRefs(records []core.AttestationRecord) []string {
	if len(records) == 0 {
		return nil
	}
	limit := min(3, len(records))
	refs := make([]string, 0, limit)
	for _, record := range records[:limit] {
		ref := fmt.Sprintf("%s(%s)", record.AttestationID, record.Result)
		if record.ArtifactID != "" {
			ref += ":" + record.ArtifactID
		}
		refs = append(refs, ref)
	}
	return refs
}

func proofBundleArtifactRefs(records []core.ArtifactRecord) []string {
	if len(records) == 0 {
		return nil
	}
	limit := min(3, len(records))
	refs := make([]string, 0, limit)
	for _, record := range records[:limit] {
		refs = append(refs, fmt.Sprintf("%s(%s)", record.ArtifactID, record.Kind))
	}
	return refs
}

func proofBundleDocRefs(records []core.DocContentRecord) []string {
	if len(records) == 0 {
		return nil
	}
	limit := min(3, len(records))
	refs := make([]string, 0, limit)
	for _, record := range records[:limit] {
		refs = append(refs, fmt.Sprintf("%s(%s)", record.DocID, record.Path))
	}
	return refs
}
func (s *Service) sendWorkNotification(_ context.Context, work core.WorkItemRecord, message string) {
	s.DigestCollector.Collect(digestItemForWork(work, "done", firstNonEmpty(message, work.Objective, "Work completed.")))
}

// collectCheckArtifacts collects screenshots from the check report's artifact paths
// and from .cogent/artifacts/<work-id>/screenshots/ in the project root.
func (s *Service) collectCheckArtifacts(ctx context.Context, workID string, cr core.CheckRecord) []notify.ResendEmailAttachment {
	var attachments []notify.ResendEmailAttachment

	// Collect screenshots referenced directly in the check report.
	for _, screenshotPath := range cr.Report.Screenshots {
		contentType, ok := playwrightArtifactContentType(screenshotPath)
		if !ok {
			continue
		}
		data, err := os.ReadFile(screenshotPath)
		if err != nil {
			continue
		}
		attachments = append(attachments, notify.ResendEmailAttachment{
			Filename:    filepath.Base(screenshotPath),
			Content:     base64.StdEncoding.EncodeToString(data),
			ContentType: contentType,
		})
	}
	if len(attachments) > 0 {
		return attachments
	}

	// Fallback: look in .cogent/artifacts/<work-id>/screenshots/ under the project root.
	projectRoot := s.findProjectRoot(ctx, workID)
	if projectRoot != "" {
		screenshotDir := filepath.Join(projectRoot, ".cogent", "artifacts", workID, "screenshots")
		if found := collectScreenshots(screenshotDir); len(found) > 0 {
			return found
		}
	}

	// Final fallback: Playwright test-results directories.
	return s.collectPlaywrightAttachments(ctx, workID)
}

// collectScreenshotPaths gathers Playwright artifact paths for a check record,
// including both explicit paths and those from fallback directories.
// This ensures screenshots are available for inline HTML and videos for attachments.
func (s *Service) collectScreenshotPaths(ctx context.Context, workID string, cr core.CheckRecord) []string {
	seen := make(map[string]bool)
	var paths []string

	// Start with explicit paths from the check report
	for _, p := range cr.Report.Screenshots {
		if p != "" && !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}

	// Try to find additional screenshots from the fallback directory
	projectRoot := s.findProjectRoot(ctx, workID)
	if projectRoot != "" {
		screenshotDir := filepath.Join(projectRoot, ".cogent", "artifacts", workID, "screenshots")
		if err := filepath.WalkDir(screenshotDir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if _, ok := playwrightArtifactContentType(path); ok {
				if !seen[path] {
					seen[path] = true
					paths = append(paths, path)
				}
			}
			return nil
		}); err != nil {
			// Ignore walk errors; we'll use what we found
		}
	}

	return paths
}

// gitMainRepoRoot returns the main repository root from any path in the repo or a worktree.
// Worktrees in this project follow the pattern <mainRepo>/.cogent/worktrees/<workID>.
// Unlike "git rev-parse --show-toplevel", this always returns the main repo root.
func gitMainRepoRoot(ctx context.Context, cwd string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", cwd, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", err
	}
	top := strings.TrimSpace(string(out))
	// Strip worktree suffix: <mainRepo>/.cogent/worktrees/<workID> → <mainRepo>
	const worktreeMarker = string(os.PathSeparator) + ".cogent" + string(os.PathSeparator) + "worktrees" + string(os.PathSeparator)
	if idx := strings.Index(top, worktreeMarker); idx >= 0 {
		return top[:idx], nil
	}
	return top, nil
}

// findProjectRoot finds the main git repo root from the job CWD for a work item.
func (s *Service) findProjectRoot(ctx context.Context, workID string) string {
	jobs, err := s.store.ListJobsByWork(ctx, workID, 10)
	if err != nil || len(jobs) == 0 {
		return ""
	}
	cwd := verifyRepoPath(jobs)
	if cwd == "" || cwd == "." {
		return ""
	}
	root, err := gitMainRepoRoot(ctx, cwd)
	if err != nil {
		return ""
	}
	return root
}

// collectPlaywrightAttachments looks up the job CWD for the work item and
// returns any PNG screenshots found in .cogent/artifacts/<work-id>/screenshots/ or test-results directories.
func (s *Service) collectPlaywrightAttachments(ctx context.Context, workID string) []notify.ResendEmailAttachment {
	// First, try to find screenshots in the persistent artifacts directory.
	jobs, err := s.store.ListJobsByWork(ctx, workID, 10)
	if err != nil || len(jobs) == 0 {
		return nil
	}
	cwd := verifyRepoPath(jobs)
	if cwd != "" && cwd != "." {
		// Try main project root .cogent/artifacts/<work-id>/screenshots/ first.
		// Use gitMainRepoRoot so worktree paths resolve to the main repo, not the worktree.
		if projectRoot, err := gitMainRepoRoot(ctx, cwd); err == nil {
			screenshotDir := filepath.Join(projectRoot, ".cogent", "artifacts", workID, "screenshots")
			if attachments := collectScreenshots(screenshotDir); len(attachments) > 0 {
				return attachments
			}
		}
	}

	// Fallback: check multiple possible locations in the job CWD for Playwright artifacts.
	if cwd == "" || cwd == "." {
		return nil
	}
	for _, subdir := range []string{"test-results", "tests/test-results", "mind-graph/test-results"} {
		dir := filepath.Join(cwd, subdir)
		if attachments := collectScreenshots(dir); len(attachments) > 0 {
			return attachments
		}
	}
	return nil
}

// collectScreenshots walks dir recursively and returns Playwright screenshots/videos as base64 attachments.
func collectScreenshots(dir string) []notify.ResendEmailAttachment {
	var attachments []notify.ResendEmailAttachment
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		contentType, ok := playwrightArtifactContentType(path)
		if !ok {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		attachments = append(attachments, notify.ResendEmailAttachment{
			Filename:    d.Name(),
			Content:     base64.StdEncoding.EncodeToString(data),
			ContentType: contentType,
		})
		return nil
	})
	return attachments
}

func playwrightArtifactContentType(path string) (string, bool) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png", true
	case ".jpg", ".jpeg":
		return "image/jpeg", true
	case ".webm":
		return "video/webm", true
	case ".mp4", ".mov":
		return "video/mp4", true
	default:
		return "", false
	}
}
