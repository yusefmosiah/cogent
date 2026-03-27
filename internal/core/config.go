package core

import (
	"errors"
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Store    StoreConfig    `toml:"store"`
	Defaults DefaultsConfig `toml:"defaults"`
	Adapters AdaptersConfig `toml:"adapters"`
	Pricing  PricingConfig  `toml:"pricing"`
	Rotation RotationConfig `toml:"rotation"`
}

type StoreConfig struct {
	StateDir string `toml:"state_dir"`
}

type DefaultsConfig struct {
	JSON bool `toml:"json"`
}

type AdaptersConfig struct {
	Claude AdapterConfig `toml:"claude"`
	Native AdapterConfig `toml:"native"`
}

type AdapterConfig struct {
	Binary  string   `toml:"binary"`
	Enabled bool     `toml:"enabled"`
	Summary string   `toml:"summary"`
	Speed   string   `toml:"speed"`
	Cost    string   `toml:"cost"`
	Tags    []string `toml:"tags"`
}

type PricingConfig struct {
	Models []ModelPricingOverride `toml:"models"`
}

// RotationEntry defines a single entry in the configurable model rotation pool.
type RotationEntry struct {
	Adapter       string   `toml:"adapter"`
	Model         string   `toml:"model"`
	MaxRunsPerDay int      `toml:"max_runs_per_day"` // 0 = unlimited
	Roles         []string `toml:"roles"`            // empty = all roles; e.g. ["implement","design"]
}

// RotationConfig holds the ordered list of adapter/model pairs for round-robin dispatch.
// If empty, the supervisor falls back to its hard-coded defaults.
type RotationConfig struct {
	Entries []RotationEntry `toml:"entries"`
}

type ModelPricingOverride struct {
	Provider                string  `toml:"provider"`
	Model                   string  `toml:"model"`
	InputUSDPerMTok         float64 `toml:"input_usd_per_mtok"`
	OutputUSDPerMTok        float64 `toml:"output_usd_per_mtok"`
	CachedInputUSDPerMTok   float64 `toml:"cached_input_usd_per_mtok"`
	CacheReadUSDPerMTok     float64 `toml:"cache_read_usd_per_mtok"`
	CacheCreationUSDPerMTok float64 `toml:"cache_creation_usd_per_mtok"`
	Source                  string  `toml:"source"`
	SourceURL               string  `toml:"source_url"`
}

func DefaultConfig(paths Paths) Config {
	return Config{
		Store: StoreConfig{
			StateDir: paths.StateDir,
		},
		Defaults: DefaultsConfig{
			JSON: false,
		},
		Adapters: AdaptersConfig{
			Claude: AdapterConfig{Binary: "claude", Enabled: true},
			Native: AdapterConfig{Binary: "cogent", Enabled: true},
		},
	}
}

func (c AdaptersConfig) ByName(name string) (AdapterConfig, bool) {
	switch name {
	case "claude":
		return c.Claude, true
	case "native":
		return c.Native, true
	default:
		return AdapterConfig{}, false
	}
}

func LoadConfig(path string) (Config, error) {
	paths, err := ResolvePathsForRepo()
	if err != nil {
		return Config{}, err
	}

	cfg := DefaultConfig(paths)
	target := path
	if target == "" {
		target = paths.ConfigPath
	}

	target, err = expandUser(target)
	if err != nil {
		return Config{}, fmt.Errorf("expand config path: %w", err)
	}

	if _, err := os.Stat(target); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return Config{}, fmt.Errorf("stat config %q: %w", target, err)
	}

	if _, err := toml.DecodeFile(target, &cfg); err != nil {
		return Config{}, fmt.Errorf("decode config %q: %w", target, err)
	}

	return cfg, nil
}
