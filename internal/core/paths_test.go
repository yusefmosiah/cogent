package core

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePathsUsesCogentOverrides(t *testing.T) {
	env := map[string]string{
		"FASE_CONFIG_DIR": "/tmp/fase-config",
		"FASE_STATE_DIR":  "/tmp/fase-state",
		"FASE_CACHE_DIR":  "/tmp/fase-cache",
	}

	paths, err := ResolvePathsFromEnv("/Users/tester", func(key string) string { return env[key] })
	if err != nil {
		t.Fatalf("ResolvePathsFromEnv returned error: %v", err)
	}

	if paths.ConfigDir != "/tmp/fase-config" {
		t.Fatalf("expected config override, got %q", paths.ConfigDir)
	}
	if paths.StateDir != "/tmp/fase-state" {
		t.Fatalf("expected state override, got %q", paths.StateDir)
	}
	if paths.CacheDir != "/tmp/fase-cache" {
		t.Fatalf("expected cache override, got %q", paths.CacheDir)
	}
	if paths.DBPath != "/tmp/fase-state/cogent.db" {
		t.Fatalf("expected DB path under state dir, got %q", paths.DBPath)
	}
}

func TestResolvePathsUsesXDGFallbacks(t *testing.T) {
	env := map[string]string{
		"XDG_CONFIG_HOME": "/tmp/xdg-config",
		"XDG_STATE_HOME":  "/tmp/xdg-state",
		"XDG_CACHE_HOME":  "/tmp/xdg-cache",
	}

	paths, err := ResolvePathsFromEnv("/Users/tester", func(key string) string { return env[key] })
	if err != nil {
		t.Fatalf("ResolvePathsFromEnv returned error: %v", err)
	}

	if paths.ConfigDir != "/tmp/xdg-config/cogent" {
		t.Fatalf("expected XDG config dir, got %q", paths.ConfigDir)
	}
	if paths.StateDir != "/tmp/xdg-state/cogent" {
		t.Fatalf("expected XDG state dir, got %q", paths.StateDir)
	}
	if paths.CacheDir != "/tmp/xdg-cache/cogent" {
		t.Fatalf("expected XDG cache dir, got %q", paths.CacheDir)
	}
}

func TestResolvePathsUsesHomeFallbacks(t *testing.T) {
	paths, err := ResolvePathsFromEnv("/Users/tester", func(string) string { return "" })
	if err != nil {
		t.Fatalf("ResolvePathsFromEnv returned error: %v", err)
	}

	if paths.ConfigDir != filepath.Join("/Users/tester", ".config", "cogent") {
		t.Fatalf("unexpected config dir: %q", paths.ConfigDir)
	}
	if paths.StateDir != filepath.Join("/Users/tester", ".local", "state", "cogent") {
		t.Fatalf("unexpected state dir: %q", paths.StateDir)
	}
	if paths.CacheDir != filepath.Join("/Users/tester", ".cache", "cogent") {
		t.Fatalf("unexpected cache dir: %q", paths.CacheDir)
	}
}

func TestResolveRepoStateDirFromReturnsCogentDir(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir git: %v", err)
	}

	expected := filepath.Join(root, ".cogent")
	if got := ResolveRepoStateDirFrom(root); got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestMigrateLegacyRepoStateDirFromRenamesLegacyState(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir git: %v", err)
	}

	legacyDir := filepath.Join(root, ".fase")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatalf("mkdir legacy state dir: %v", err)
	}
	dbPath := filepath.Join(legacyDir, "fase.db")
	privateDBPath := filepath.Join(legacyDir, "fase-private.db")
	if err := os.WriteFile(dbPath, []byte("public-db"), 0o644); err != nil {
		t.Fatalf("write legacy db: %v", err)
	}
	if err := os.WriteFile(privateDBPath, []byte("private-db"), 0o644); err != nil {
		t.Fatalf("write legacy private db: %v", err)
	}

	var logs []string
	if err := MigrateLegacyRepoStateDirFrom(root, func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	}); err != nil {
		t.Fatalf("migrate legacy state dir: %v", err)
	}

	if _, err := os.Stat(legacyDir); !os.IsNotExist(err) {
		t.Fatalf("expected legacy state dir to be removed, got err=%v", err)
	}

	stateDir := filepath.Join(root, ".cogent")
	if _, err := os.Stat(stateDir); err != nil {
		t.Fatalf("expected new state dir to exist: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(stateDir, "cogent.db"))
	if err != nil {
		t.Fatalf("read migrated db: %v", err)
	}
	if string(data) != "public-db" {
		t.Fatalf("unexpected migrated db contents: %q", string(data))
	}
	data, err = os.ReadFile(filepath.Join(stateDir, "cogent-private.db"))
	if err != nil {
		t.Fatalf("read migrated private db: %v", err)
	}
	if string(data) != "private-db" {
		t.Fatalf("unexpected migrated private db contents: %q", string(data))
	}
	if len(logs) == 0 {
		t.Fatal("expected migration log output")
	}
}
