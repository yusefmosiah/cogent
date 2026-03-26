package notify

import (
	"context"
	"strings"
	"testing"
)

func TestDigestCollectorFlushBuildsMeaningfulEmail(t *testing.T) {
	collector := NewDigestCollector("test-api-key", "user@example.com")

	var (
		gotSubject string
		gotHTML    string
		sendCalls  int
	)
	collector.send = func(_ context.Context, _, _, subject, htmlBody string, _ []ResendEmailAttachment) {
		sendCalls++
		gotSubject = subject
		gotHTML = htmlBody
	}

	collector.Collect(DigestItem{
		WorkID:    "work_digest_01",
		Title:     "Fix email digest",
		Objective: "Repair the hourly digest so serve sends meaningful summaries.",
		Event:     "done",
	})
	collector.Collect(DigestItem{
		WorkID:  "work_digest_02",
		Title:   "Stabilize checker flow",
		Event:   "check_fail",
		Summary: "Checker reported 3 failing tests in the notify package.",
	})

	collector.Flush(context.Background())

	if sendCalls != 1 {
		t.Fatalf("expected exactly one email send, got %d", sendCalls)
	}
	if strings.TrimSpace(gotSubject) == "" {
		t.Fatal("expected non-empty digest subject")
	}
	if !strings.Contains(gotSubject, "Fix email digest") {
		t.Fatalf("expected subject to mention a collected work title, got %q", gotSubject)
	}
	if strings.TrimSpace(gotHTML) == "" {
		t.Fatal("expected non-empty digest html body")
	}
	for _, want := range []string{
		"Fix email digest",
		"Repair the hourly digest so serve sends meaningful summaries.",
		"Stabilize checker flow",
		"Checker reported 3 failing tests in the notify package.",
		"work_digest_01",
		"work_digest_02",
	} {
		if !strings.Contains(gotHTML, want) {
			t.Fatalf("expected digest html to contain %q, got %q", want, gotHTML)
		}
	}

	collector.mu.Lock()
	defer collector.mu.Unlock()
	if len(collector.items) != 0 {
		t.Fatalf("expected flush to clear queued digest items, got %d remaining", len(collector.items))
	}
}
