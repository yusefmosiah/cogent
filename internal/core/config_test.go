package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigParsesAdapterTraits(t *testing.T) {
	tempDir := t.TempDir()

	t.Setenv("COGENT_CONFIG_DIR", tempDir)
	t.Setenv("COGENT_STATE_DIR", filepath.Join(tempDir, "state"))
	t.Setenv("COGENT_CACHE_DIR", filepath.Join(tempDir, "cache"))

	configPath := filepath.Join(tempDir, "config.toml")
	configBody := []byte(`
[adapters.native]
binary = "cogent"
enabled = true
summary = "primary code-editing adapter"
speed = "fast"
cost = "high"
tags = ["default", "tools"]

[[pricing.models]]
provider = "openai"
model = "gpt-5-mini"
input_usd_per_mtok = 0.25
output_usd_per_mtok = 2
cached_input_usd_per_mtok = 0.025
source = "manual"
`)
	if err := os.WriteFile(configPath, configBody, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}

	if cfg.Adapters.Native.Summary != "primary code-editing adapter" {
		t.Fatalf("unexpected summary: %q", cfg.Adapters.Native.Summary)
	}
	if cfg.Adapters.Native.Speed != "fast" {
		t.Fatalf("unexpected speed: %q", cfg.Adapters.Native.Speed)
	}
	if cfg.Adapters.Native.Cost != "high" {
		t.Fatalf("unexpected cost: %q", cfg.Adapters.Native.Cost)
	}
	if len(cfg.Adapters.Native.Tags) != 2 || cfg.Adapters.Native.Tags[0] != "default" || cfg.Adapters.Native.Tags[1] != "tools" {
		t.Fatalf("unexpected tags: %#v", cfg.Adapters.Native.Tags)
	}
	if len(cfg.Pricing.Models) != 1 {
		t.Fatalf("expected one pricing override, got %d", len(cfg.Pricing.Models))
	}
	override := cfg.Pricing.Models[0]
	if override.Provider != "openai" || override.Model != "gpt-5-mini" {
		t.Fatalf("unexpected pricing override: %+v", override)
	}
	if override.InputUSDPerMTok != 0.25 || override.OutputUSDPerMTok != 2 {
		t.Fatalf("unexpected pricing override values: %+v", override)
	}
}
