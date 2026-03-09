package service

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yusefmosiah/cagent/internal/core"
)

func TestRunPersistsFailedJobForUnavailableAdapter(t *testing.T) {
	stateDir := t.TempDir()
	configDir := t.TempDir()
	cacheDir := t.TempDir()

	t.Setenv("CAGENT_STATE_DIR", stateDir)
	t.Setenv("CAGENT_CONFIG_DIR", configDir)
	t.Setenv("CAGENT_CACHE_DIR", cacheDir)

	configPath := filepath.Join(configDir, "config.toml")
	configBody := []byte("[adapters.factory]\nbinary = \"/definitely/missing/droid\"\nenabled = true\n")
	if err := os.WriteFile(configPath, configBody, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	svc, err := Open(context.Background(), configPath)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer func() { _ = svc.Close() }()

	cwd := t.TempDir()
	result, err := svc.Run(context.Background(), RunRequest{
		Adapter:      "factory",
		CWD:          cwd,
		Prompt:       "build milestone 1",
		PromptSource: "prompt",
		Label:        "bootstrap",
	})
	if err == nil {
		t.Fatal("expected run to fail for unavailable adapter binary")
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

func TestRunCompletesWithFakeCodexAdapter(t *testing.T) {
	stateDir := t.TempDir()
	configDir := t.TempDir()
	cacheDir := t.TempDir()

	t.Setenv("CAGENT_STATE_DIR", stateDir)
	t.Setenv("CAGENT_CONFIG_DIR", configDir)
	t.Setenv("CAGENT_CACHE_DIR", cacheDir)

	fakeBinary, err := filepath.Abs(filepath.Join("..", "..", "testdata", "fake_clis", "codex"))
	if err != nil {
		t.Fatalf("resolve fake codex path: %v", err)
	}
	if err := os.Chmod(fakeBinary, 0o755); err != nil {
		t.Fatalf("chmod fake codex: %v", err)
	}

	configPath := filepath.Join(configDir, "config.toml")
	configBody := []byte("[adapters.codex]\nbinary = \"" + fakeBinary + "\"\nenabled = true\n")
	if err := os.WriteFile(configPath, configBody, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	svc, err := Open(context.Background(), configPath)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer func() { _ = svc.Close() }()

	result, err := svc.Run(context.Background(), RunRequest{
		Adapter:      "codex",
		CWD:          t.TempDir(),
		Prompt:       "build milestone 2",
		PromptSource: "prompt",
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.Job.State != core.JobStateCompleted {
		t.Fatalf("expected completed job state, got %s", result.Job.State)
	}
	if result.Job.NativeSessionID != "codex-session-123" {
		t.Fatalf("expected discovered native session, got %q", result.Job.NativeSessionID)
	}

	status, err := svc.Status(context.Background(), result.Job.JobID)
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if status.Job.State != core.JobStateCompleted {
		t.Fatalf("expected completed status, got %s", status.Job.State)
	}

	rawLogs, err := svc.RawLogs(context.Background(), result.Job.JobID, 100)
	if err != nil {
		t.Fatalf("RawLogs returned error: %v", err)
	}
	if len(rawLogs) == 0 {
		t.Fatal("expected raw logs for fake codex run")
	}

	eventLogs, err := svc.Logs(context.Background(), result.Job.JobID, 100)
	if err != nil {
		t.Fatalf("Logs returned error: %v", err)
	}
	var foundAssistant bool
	for _, event := range eventLogs {
		if event.Kind == "assistant.message" && bytes.Contains(event.Payload, []byte("Codex completed the task")) {
			foundAssistant = true
		}
	}
	if !foundAssistant {
		t.Fatal("expected translated assistant.message event")
	}
}

func TestSendContinuesFakeCodexSession(t *testing.T) {
	stateDir := t.TempDir()
	configDir := t.TempDir()
	cacheDir := t.TempDir()

	t.Setenv("CAGENT_STATE_DIR", stateDir)
	t.Setenv("CAGENT_CONFIG_DIR", configDir)
	t.Setenv("CAGENT_CACHE_DIR", cacheDir)

	fakeBinary, err := filepath.Abs(filepath.Join("..", "..", "testdata", "fake_clis", "codex"))
	if err != nil {
		t.Fatalf("resolve fake codex path: %v", err)
	}
	if err := os.Chmod(fakeBinary, 0o755); err != nil {
		t.Fatalf("chmod fake codex: %v", err)
	}

	configPath := filepath.Join(configDir, "config.toml")
	configBody := []byte("[adapters.codex]\nbinary = \"" + fakeBinary + "\"\nenabled = true\n")
	if err := os.WriteFile(configPath, configBody, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	svc, err := Open(context.Background(), configPath)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer func() { _ = svc.Close() }()

	first, err := svc.Run(context.Background(), RunRequest{
		Adapter:      "codex",
		CWD:          t.TempDir(),
		Prompt:       "initial prompt",
		PromptSource: "prompt",
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	second, err := svc.Send(context.Background(), SendRequest{
		SessionID:    first.Session.SessionID,
		Prompt:       "follow up",
		PromptSource: "prompt",
	})
	if err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
	if second.Job.State != core.JobStateCompleted {
		t.Fatalf("expected completed send job state, got %s", second.Job.State)
	}
	if second.Job.NativeSessionID != first.Job.NativeSessionID {
		t.Fatalf("expected same native session id, got %q want %q", second.Job.NativeSessionID, first.Job.NativeSessionID)
	}
	if !strings.Contains(second.Message, "continued") {
		t.Fatalf("expected continuation message, got %q", second.Message)
	}
}

func TestRunCompletesWithFakeFactoryAdapter(t *testing.T) {
	stateDir := t.TempDir()
	configDir := t.TempDir()
	cacheDir := t.TempDir()

	t.Setenv("CAGENT_STATE_DIR", stateDir)
	t.Setenv("CAGENT_CONFIG_DIR", configDir)
	t.Setenv("CAGENT_CACHE_DIR", cacheDir)

	fakeBinary, err := filepath.Abs(filepath.Join("..", "..", "testdata", "fake_clis", "droid"))
	if err != nil {
		t.Fatalf("resolve fake droid path: %v", err)
	}
	if err := os.Chmod(fakeBinary, 0o755); err != nil {
		t.Fatalf("chmod fake droid: %v", err)
	}

	configPath := filepath.Join(configDir, "config.toml")
	configBody := []byte("[adapters.factory]\nbinary = \"" + fakeBinary + "\"\nenabled = true\n")
	if err := os.WriteFile(configPath, configBody, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	svc, err := Open(context.Background(), configPath)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer func() { _ = svc.Close() }()

	result, err := svc.Run(context.Background(), RunRequest{
		Adapter:      "factory",
		CWD:          t.TempDir(),
		Prompt:       "build milestone 3",
		PromptSource: "prompt",
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.Job.State != core.JobStateCompleted {
		t.Fatalf("expected completed factory job state, got %s", result.Job.State)
	}
	if result.Job.NativeSessionID != "factory-session-123" {
		t.Fatalf("expected discovered factory native session, got %q", result.Job.NativeSessionID)
	}
}

func TestRunAndSessionWithFakePiAdapter(t *testing.T) {
	stateDir := t.TempDir()
	configDir := t.TempDir()
	cacheDir := t.TempDir()
	piDir := t.TempDir()

	t.Setenv("CAGENT_STATE_DIR", stateDir)
	t.Setenv("CAGENT_CONFIG_DIR", configDir)
	t.Setenv("CAGENT_CACHE_DIR", cacheDir)
	t.Setenv("PI_CODING_AGENT_DIR", piDir)

	fakeBinary, err := filepath.Abs(filepath.Join("..", "..", "testdata", "fake_clis", "pi"))
	if err != nil {
		t.Fatalf("resolve fake pi path: %v", err)
	}
	if err := os.Chmod(fakeBinary, 0o755); err != nil {
		t.Fatalf("chmod fake pi: %v", err)
	}

	configPath := filepath.Join(configDir, "config.toml")
	configBody := []byte("[adapters.pi]\nbinary = \"" + fakeBinary + "\"\nenabled = true\n")
	if err := os.WriteFile(configPath, configBody, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	svc, err := Open(context.Background(), configPath)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer func() { _ = svc.Close() }()

	first, err := svc.Run(context.Background(), RunRequest{
		Adapter:      "pi",
		CWD:          t.TempDir(),
		Prompt:       "initial pi prompt",
		PromptSource: "prompt",
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if first.Job.NativeSessionID != "pi-session-123" {
		t.Fatalf("expected pi native session id, got %q", first.Job.NativeSessionID)
	}

	second, err := svc.Send(context.Background(), SendRequest{
		SessionID:    first.Session.SessionID,
		Prompt:       "continue pi prompt",
		PromptSource: "prompt",
	})
	if err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
	if second.Job.NativeSessionID != first.Job.NativeSessionID {
		t.Fatalf("expected same pi session id, got %q want %q", second.Job.NativeSessionID, first.Job.NativeSessionID)
	}

	session, err := svc.Session(context.Background(), first.Session.SessionID)
	if err != nil {
		t.Fatalf("Session returned error: %v", err)
	}
	if len(session.NativeSessions) != 1 {
		t.Fatalf("expected one native session, got %d", len(session.NativeSessions))
	}
	if got, _ := session.NativeSessions[0].Metadata["session_path"].(string); !strings.HasSuffix(got, ".jsonl") {
		t.Fatalf("expected pi session_path metadata, got %q", got)
	}
	if len(session.Turns) != 2 {
		t.Fatalf("expected two turns, got %d", len(session.Turns))
	}
	if len(session.Actions) == 0 || !session.Actions[0].Available {
		t.Fatalf("expected available send action, got %+v", session.Actions)
	}
}
