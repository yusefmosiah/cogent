package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/yusefmosiah/cogent/internal/adapters"
	"github.com/yusefmosiah/cogent/internal/core"
)

type Runner interface {
	CombinedOutput(ctx context.Context, binary string, args ...string) ([]byte, error)
}

type ExecRunner struct{}

func (ExecRunner) CombinedOutput(ctx context.Context, binary string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	return cmd.CombinedOutput()
}

func Snapshot(ctx context.Context, cfg core.Config, runner Runner) core.CatalogSnapshot {
	if runner == nil {
		runner = ExecRunner{}
	}

	snapshot := core.CatalogSnapshot{
		SnapshotID: core.GenerateID("cat"),
		CreatedAt:  time.Now().UTC(),
		Entries:    []core.CatalogEntry{},
		Issues:     []core.CatalogIssue{},
	}

	for _, diag := range adapters.CatalogFromConfig(cfg) {
		if !diag.Enabled {
			continue
		}
		cfgEntry, ok := cfg.Adapters.ByName(diag.Adapter)
		if !ok {
			continue
		}
		if !diag.Available {
			snapshot.Issues = append(snapshot.Issues, core.CatalogIssue{
				Adapter:  diag.Adapter,
				Severity: "warning",
				Message:  fmt.Sprintf("binary %q is not available on PATH", cfgEntry.Binary),
			})
			continue
		}

		entries, issues := discoverAdapter(ctx, runner, diag.Adapter, cfgEntry.Binary, snapshot.CreatedAt)
		snapshot.Entries = append(snapshot.Entries, entries...)
		snapshot.Issues = append(snapshot.Issues, issues...)
	}

	sort.Slice(snapshot.Entries, func(i, j int) bool {
		left := snapshot.Entries[i]
		right := snapshot.Entries[j]
		if left.Adapter != right.Adapter {
			return left.Adapter < right.Adapter
		}
		if left.Provider != right.Provider {
			return left.Provider < right.Provider
		}
		return left.Model < right.Model
	})

	return snapshot
}

func discoverAdapter(ctx context.Context, runner Runner, adapter, binary string, observedAt time.Time) ([]core.CatalogEntry, []core.CatalogIssue) {
	switch adapter {
	case "claude":
		return discoverClaude(ctx, runner, binary, observedAt)
	case "native":
		return discoverNative(ctx, runner, binary, observedAt)
	default:
		return nil, []core.CatalogIssue{{
			Adapter:  adapter,
			Severity: "warning",
			Message:  "no catalog discoverer implemented",
		}}
	}
}

func discoverNative(ctx context.Context, runner Runner, binary string, observedAt time.Time) ([]core.CatalogEntry, []core.CatalogIssue) {
	entries := []core.CatalogEntry{
		{
			Adapter:      "native",
			Provider:     "zai",
			Model:        "glm-5-turbo",
			Available:    os.Getenv("ZAI_API_KEY") != "",
			AuthMethod:   "api_key",
			BillingClass: "metered_api",
			Source:       "env",
			Provenance: core.CatalogProvenance{
				Source:     "env",
				ObservedAt: observedAt,
			},
		},
		{
			Adapter:      "native",
			Provider:     "bedrock",
			Model:        "claude-haiku-4-5",
			Available:    os.Getenv("AWS_REGION") != "" && os.Getenv("AWS_BEARER_TOKEN_BEDROCK") != "",
			AuthMethod:   "api_key",
			BillingClass: "cloud_project",
			Source:       "env",
			Provenance: core.CatalogProvenance{
				Source:     "env",
				ObservedAt: observedAt,
			},
		},
	}

	output, err := runner.CombinedOutput(ctx, binary, "login", "status")
	authMethod := "unknown"
	available := false
	if err == nil {
		available = true
		authMethod = "chatgpt"
	}
	entries = append(entries, core.CatalogEntry{
		Adapter:      "native",
		Provider:     "chatgpt",
		Model:        "gpt-5.4-mini",
		Available:    available,
		AuthMethod:   authMethod,
		BillingClass: "subscription",
		Source:       "cli",
		Provenance: core.CatalogProvenance{
			Source:     "cli",
			Command:    binary + " login status",
			ObservedAt: observedAt,
		},
		Metadata: map[string]any{
			"status_text": strings.TrimSpace(stripANSI(string(output))),
		},
	})

	return entries, nil
}

func discoverClaude(ctx context.Context, runner Runner, binary string, observedAt time.Time) ([]core.CatalogEntry, []core.CatalogIssue) {
	output, err := runner.CombinedOutput(ctx, binary, "auth", "status")
	if err != nil {
		return nil, []core.CatalogIssue{{
			Adapter:  "claude",
			Severity: "warning",
			Message:  fmt.Sprintf("claude auth status failed: %v", err),
		}}
	}

	var payload struct {
		LoggedIn         bool   `json:"loggedIn"`
		AuthMethod       string `json:"authMethod"`
		APIProvider      string `json:"apiProvider"`
		SubscriptionType string `json:"subscriptionType"`
	}
	if err := json.Unmarshal(output, &payload); err != nil {
		return nil, []core.CatalogIssue{{
			Adapter:  "claude",
			Severity: "warning",
			Message:  fmt.Sprintf("parse claude auth status: %v", err),
		}}
	}

	entry := core.CatalogEntry{
		Adapter:      "claude",
		Provider:     providerOrDefault(payload.APIProvider, "anthropic"),
		Available:    payload.LoggedIn,
		AuthMethod:   normalizeClaudeAuthMethod(payload.AuthMethod),
		BillingClass: normalizeClaudeBilling(payload.AuthMethod, payload.APIProvider),
		Selected:     payload.LoggedIn,
		Source:       "cli",
		Provenance: core.CatalogProvenance{
			Source:     "cli",
			Command:    binary + " auth status",
			ObservedAt: observedAt,
		},
		Metadata: map[string]any{
			"subscription_type": payload.SubscriptionType,
		},
	}
	return []core.CatalogEntry{entry}, nil
}





func normalizeClaudeAuthMethod(authMethod string) string {
	switch strings.ToLower(strings.TrimSpace(authMethod)) {
	case "claude.ai":
		return "claude_ai"
	case "apikey", "api_key":
		return "api_key"
	default:
		return strings.ToLower(strings.TrimSpace(authMethod))
	}
}

func normalizeClaudeBilling(authMethod, apiProvider string) string {
	switch strings.ToLower(strings.TrimSpace(authMethod)) {
	case "claude.ai":
		return "subscription"
	case "apikey", "api_key":
		if strings.Contains(strings.ToLower(apiProvider), "bedrock") || strings.Contains(strings.ToLower(apiProvider), "vertex") {
			return "cloud_project"
		}
		return "metered_api"
	default:
		if strings.Contains(strings.ToLower(apiProvider), "bedrock") || strings.Contains(strings.ToLower(apiProvider), "vertex") {
			return "cloud_project"
		}
		return "unknown"
	}
}



func providerOrDefault(value, fallback string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return fallback
	}
	return value
}



var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

func stripANSI(text string) string {
	text = ansiPattern.ReplaceAllString(text, "")
	text = strings.ReplaceAll(text, "\r", "")
	text = string(bytes.TrimSpace([]byte(text)))
	return text
}
