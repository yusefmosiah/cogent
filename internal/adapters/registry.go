package adapters

import (
	"os/exec"
	"sort"

	"github.com/yusefmosiah/cagent/internal/core"
)

type Capabilities struct {
	HeadlessRun      bool `json:"headless_run"`
	StreamJSON       bool `json:"stream_json"`
	NativeResume     bool `json:"native_resume"`
	NativeFork       bool `json:"native_fork"`
	StructuredOutput bool `json:"structured_output"`
	InteractiveMode  bool `json:"interactive_mode"`
	RPCMode          bool `json:"rpc_mode"`
	MCP              bool `json:"mcp"`
	Checkpointing    bool `json:"checkpointing"`
	SessionExport    bool `json:"session_export"`
}

type Descriptor struct {
	Adapter      string       `json:"adapter"`
	Binary       string       `json:"binary"`
	Version      *string      `json:"version"`
	Available    bool         `json:"available"`
	Enabled      bool         `json:"enabled"`
	Implemented  bool         `json:"implemented"`
	Capabilities Capabilities `json:"capabilities"`
}

func CatalogFromConfig(cfg core.Config) []Descriptor {
	entries := []Descriptor{
		makeDescriptor("claude", cfg.Adapters.Claude),
		makeDescriptor("codex", cfg.Adapters.Codex),
		makeDescriptor("factory", cfg.Adapters.Factory),
		makeDescriptor("gemini", cfg.Adapters.Gemini),
		makeDescriptor("opencode", cfg.Adapters.OpenCode),
		makeDescriptor("pi", cfg.Adapters.Pi),
		makeDescriptor("pi_rust", cfg.Adapters.PiRust),
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Adapter < entries[j].Adapter
	})

	return entries
}

func Lookup(cfg core.Config, name string) (Descriptor, bool) {
	for _, entry := range CatalogFromConfig(cfg) {
		if entry.Adapter == name {
			return entry, true
		}
	}

	return Descriptor{}, false
}

func makeDescriptor(name string, cfg core.AdapterConfig) Descriptor {
	_, err := exec.LookPath(cfg.Binary)
	return Descriptor{
		Adapter:      name,
		Binary:       cfg.Binary,
		Available:    err == nil,
		Enabled:      cfg.Enabled,
		Implemented:  false,
		Capabilities: Capabilities{},
	}
}
