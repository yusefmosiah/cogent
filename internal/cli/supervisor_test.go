package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverADRNumberingUsesHeadingTitlesOnly(t *testing.T) {
	cwd := t.TempDir()
	docsDir := filepath.Join(cwd, "docs")
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}

	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(docsDir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	write("adr-0010-alpha.md", "# Alpha\n\n## ADR-0002: Documentation Before Execution\n\nBody mentions ADR-9999 but should not count.\n")
	write("adr-0031-beta.md", "# Beta\n\n## ADR-0005: Verification Before Human Approval\n")
	write("adr-0040-gamma.md", "# Gamma\n\nNo ADR heading here, only ADR-7777 in the body.\n")

	info := discoverADRNumbering(cwd)
	if info.highest != 5 {
		t.Fatalf("highest ADR = %d, want 5", info.highest)
	}

	if len(info.samples) != 2 {
		t.Fatalf("samples = %d, want 2", len(info.samples))
	}
	if info.samples[0] != "docs/adr-0010-alpha.md [Alpha | ADR-0002: Documentation Before Execution]" {
		t.Fatalf("sample[0] = %q", info.samples[0])
	}
	if info.samples[1] != "docs/adr-0031-beta.md [Beta | ADR-0005: Verification Before Human Approval]" {
		t.Fatalf("sample[1] = %q", info.samples[1])
	}
}

func TestSupervisorAvailableSlotsAccountsForCompletionAttestations(t *testing.T) {
	cases := []struct {
		name                 string
		inFlightCount        int
		maxConcurrent        int
		spawnedCompletionJob bool
		want                 int
	}{
		{
			name:                 "single slot consumed by attestation",
			inFlightCount:        0,
			maxConcurrent:        1,
			spawnedCompletionJob: true,
			want:                 0,
		},
		{
			name:                 "spare capacity remains when no attestation spawned",
			inFlightCount:        0,
			maxConcurrent:        1,
			spawnedCompletionJob: false,
			want:                 1,
		},
		{
			name:                 "existing in-flight work reduces capacity",
			inFlightCount:        1,
			maxConcurrent:        2,
			spawnedCompletionJob: false,
			want:                 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := supervisorAvailableSlots(tc.inFlightCount, tc.maxConcurrent, tc.spawnedCompletionJob)
			if got != tc.want {
				t.Fatalf("supervisorAvailableSlots(%d, %d, %t) = %d, want %d", tc.inFlightCount, tc.maxConcurrent, tc.spawnedCompletionJob, got, tc.want)
			}
		})
	}
}
