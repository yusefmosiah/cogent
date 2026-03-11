package events

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTranslateFixtures(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		adapter string
		fixture string
		golden  string
	}{
		{
			name:    "codex",
			adapter: "codex",
			fixture: filepath.Join("..", "..", "testdata", "fixtures", "codex", "success.jsonl"),
			golden:  filepath.Join("..", "..", "testdata", "golden", "codex", "success.events.json"),
		},
		{
			name:    "claude",
			adapter: "claude",
			fixture: filepath.Join("..", "..", "testdata", "fixtures", "claude", "success.jsonl"),
			golden:  filepath.Join("..", "..", "testdata", "golden", "claude", "success.events.json"),
		},
		{
			name:    "factory",
			adapter: "factory",
			fixture: filepath.Join("..", "..", "testdata", "fixtures", "factory", "success.jsonl"),
			golden:  filepath.Join("..", "..", "testdata", "golden", "factory", "success.events.json"),
		},
		{
			name:    "gemini",
			adapter: "gemini",
			fixture: filepath.Join("..", "..", "testdata", "fixtures", "gemini", "success.jsonl"),
			golden:  filepath.Join("..", "..", "testdata", "golden", "gemini", "success.events.json"),
		},
		{
			name:    "opencode",
			adapter: "opencode",
			fixture: filepath.Join("..", "..", "testdata", "fixtures", "opencode", "success.jsonl"),
			golden:  filepath.Join("..", "..", "testdata", "golden", "opencode", "success.events.json"),
		},
		{
			name:    "pi",
			adapter: "pi",
			fixture: filepath.Join("..", "..", "testdata", "fixtures", "pi", "success.jsonl"),
			golden:  filepath.Join("..", "..", "testdata", "golden", "pi", "success.events.json"),
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			file, err := os.Open(tc.fixture)
			if err != nil {
				t.Fatalf("open fixture: %v", err)
			}
			defer func() { _ = file.Close() }()

			var translated []Hint
			scanner := bufio.NewScanner(file)
			for scanner.Scan() {
				translated = append(translated, TranslateLine(tc.adapter, "stdout", scanner.Text())...)
			}
			if err := scanner.Err(); err != nil {
				t.Fatalf("scan fixture: %v", err)
			}

			wantBytes, err := os.ReadFile(tc.golden)
			if err != nil {
				t.Fatalf("read golden: %v", err)
			}

			var want []Hint
			if err := json.Unmarshal(wantBytes, &want); err != nil {
				t.Fatalf("unmarshal golden: %v", err)
			}

			gotBytes, err := json.MarshalIndent(translated, "", "  ")
			if err != nil {
				t.Fatalf("marshal translated output: %v", err)
			}
			wantNormalized, err := json.MarshalIndent(want, "", "  ")
			if err != nil {
				t.Fatalf("marshal normalized golden: %v", err)
			}

			if string(gotBytes) != string(wantNormalized) {
				t.Fatalf("translation mismatch\nwant:\n%s\n\ngot:\n%s", wantNormalized, gotBytes)
			}
		})
	}
}

func TestTranslateLineAggregatesModelUsage(t *testing.T) {
	t.Parallel()

	line := `{"type":"result","total_cost_usd":1.5,"modelUsage":{"claude-haiku-4-5-20251001":{"inputTokens":10,"outputTokens":20,"cacheReadInputTokens":30,"cacheCreationInputTokens":40,"costUSD":0.5},"claude-sonnet-4-6":{"inputTokens":100,"outputTokens":200,"cacheReadInputTokens":300,"cacheCreationInputTokens":400,"costUSD":1.0}}}`
	hints := TranslateLine("claude", "stdout", line)

	var usage *Hint
	for i := range hints {
		if hints[i].Kind == "usage.reported" {
			usage = &hints[i]
			break
		}
	}
	if usage == nil {
		t.Fatalf("expected usage.reported hint, got %+v", hints)
	}
	if got := usage.Payload["model"]; got != "multi" {
		t.Fatalf("expected model=multi, got %#v", got)
	}
	if got := usage.Payload["input_tokens"]; got != int64(110) {
		t.Fatalf("expected aggregated input tokens, got %#v", got)
	}
	if got := usage.Payload["output_tokens"]; got != int64(220) {
		t.Fatalf("expected aggregated output tokens, got %#v", got)
	}
	if got := usage.Payload["cache_read_input_tokens"]; got != int64(330) {
		t.Fatalf("expected aggregated cache read tokens, got %#v", got)
	}
	if got := usage.Payload["cache_creation_input_tokens"]; got != int64(440) {
		t.Fatalf("expected aggregated cache creation tokens, got %#v", got)
	}
	if got := usage.Payload["cost_usd"]; got != 1.5 {
		t.Fatalf("expected top-level vendor cost, got %#v", got)
	}
	rawModels, ok := usage.Payload["model_usage"].([]map[string]any)
	if !ok {
		items, ok := usage.Payload["model_usage"].([]any)
		if !ok {
			t.Fatalf("expected model_usage payload, got %#v", usage.Payload["model_usage"])
		}
		rawModels = make([]map[string]any, 0, len(items))
		for _, item := range items {
			entry, ok := item.(map[string]any)
			if ok {
				rawModels = append(rawModels, entry)
			}
		}
	}
	if len(rawModels) != 2 {
		t.Fatalf("expected two model usage entries, got %#v", usage.Payload["model_usage"])
	}
	if first := rawModels[0]["model"]; !strings.Contains(first.(string), "claude-haiku") {
		t.Fatalf("expected sorted model usage entries, got %#v", rawModels)
	}
}
