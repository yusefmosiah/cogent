package service

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestRunPersistsFailedJobForUnimplementedAdapter(t *testing.T) {
	stateDir := t.TempDir()
	configDir := t.TempDir()
	cacheDir := t.TempDir()

	t.Setenv("CAGENT_STATE_DIR", stateDir)
	t.Setenv("CAGENT_CONFIG_DIR", configDir)
	t.Setenv("CAGENT_CACHE_DIR", cacheDir)

	svc, err := Open(context.Background(), "")
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer func() { _ = svc.Close() }()

	cwd := t.TempDir()
	result, err := svc.Run(context.Background(), RunRequest{
		Adapter:      "codex",
		CWD:          cwd,
		Prompt:       "build milestone 1",
		PromptSource: "prompt",
		Label:        "bootstrap",
	})
	if err == nil {
		t.Fatal("expected run to fail until adapter execution exists")
	}
	if !errors.Is(err, ErrUnsupported) && !errors.Is(err, ErrAdapterUnavailable) {
		t.Fatalf("expected unsupported or unavailable error, got %v", err)
	}

	status, err := svc.Status(context.Background(), result.Job.JobID)
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}

	if status.Job.State != "failed" {
		t.Fatalf("expected failed job state, got %s", status.Job.State)
	}
	if len(status.Events) < 5 {
		t.Fatalf("expected persisted events, got %d", len(status.Events))
	}

	rawLogs, err := svc.RawLogs(context.Background(), result.Job.JobID, 50)
	if err != nil {
		t.Fatalf("RawLogs returned error: %v", err)
	}
	if len(rawLogs) == 0 {
		t.Fatal("expected at least one raw artifact")
	}
	if filepath.Base(rawLogs[0].Path) == "" {
		t.Fatalf("expected raw log path to be populated: %+v", rawLogs[0])
	}
}
