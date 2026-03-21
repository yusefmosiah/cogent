package native

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseProviderModel(t *testing.T) {
	t.Setenv("AWS_REGION", "us-east-1")

	tests := []struct {
		model string
		check func(t *testing.T, p Provider)
	}{
		{
			model: "zai/glm-4.7",
			check: func(t *testing.T, p Provider) {
				if p.Name != providerZAI || p.APIFormat != apiFormatAnthropic || p.BaseURL != zaiBaseURL || p.ModelID != "glm-4.7" {
					t.Fatalf("unexpected zai provider: %+v", p)
				}
			},
		},
		{
			model: "bedrock/claude-sonnet-4-6",
			check: func(t *testing.T, p Provider) {
				if p.Name != providerBedrock || !p.ModelInPath || p.AnthropicVersion != bedrockAnthropicVersion {
					t.Fatalf("unexpected bedrock provider: %+v", p)
				}
				if !strings.Contains(p.BaseURL, "bedrock-runtime.us-east-1.amazonaws.com") {
					t.Fatalf("unexpected bedrock base url: %s", p.BaseURL)
				}
			},
		},
		{
			model: "chatgpt/gpt-5.4-mini",
			check: func(t *testing.T, p Provider) {
				if p.Name != providerChatGPT || p.APIFormat != apiFormatOpenAI || p.BaseURL != chatGPTResponsesURL {
					t.Fatalf("unexpected chatgpt provider: %+v", p)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			p, err := ParseProviderModel(tt.model)
			if err != nil {
				t.Fatalf("ParseProviderModel(%q): %v", tt.model, err)
			}
			tt.check(t, p)
		})
	}
}

func TestChatGPTAuthRefreshesStaleToken(t *testing.T) {
	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")
	stale := codexAuthFile{
		AuthMode: "chatgpt",
		Tokens: codexAuthTokens{
			AccessToken:  "old-access",
			RefreshToken: "refresh-123",
			AccountID:    "acct-123",
		},
		LastRefresh: time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339),
	}
	data, err := json.Marshal(stale)
	if err != nil {
		t.Fatalf("marshal stale auth: %v", err)
	}
	if err := os.WriteFile(authPath, data, 0o600); err != nil {
		t.Fatalf("write auth file: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if got := r.Header.Get("Content-Type"); !strings.Contains(got, "application/x-www-form-urlencoded") {
			t.Fatalf("unexpected content-type: %s", got)
		}
		bodyBytes, _ := io.ReadAll(r.Body)
		body := string(bodyBytes)
		if !strings.Contains(body, "grant_type=refresh_token") || !strings.Contains(body, "refresh_token=refresh-123") {
			t.Fatalf("unexpected refresh body: %s", body)
		}
		_ = json.NewEncoder(w).Encode(oauthRefreshResponse{
			AccessToken:  "new-access",
			RefreshToken: "new-refresh",
		})
	}))
	defer srv.Close()

	now := time.Now().UTC()
	headerFn := NewChatGPTAuth(ChatGPTAuthOptions{
		Path:          authPath,
		RefreshURL:    srv.URL,
		RefreshBefore: 30 * time.Minute,
		HTTPClient:    srv.Client(),
		Now:           func() time.Time { return now },
	})

	header, err := headerFn(context.Background())
	if err != nil {
		t.Fatalf("auth header: %v", err)
	}
	if header != "Bearer new-access" {
		t.Fatalf("unexpected header: %s", header)
	}

	updatedBytes, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatalf("read updated auth file: %v", err)
	}
	var updated codexAuthFile
	if err := json.Unmarshal(updatedBytes, &updated); err != nil {
		t.Fatalf("decode updated auth file: %v", err)
	}
	if updated.Tokens.AccessToken != "new-access" || updated.Tokens.RefreshToken != "new-refresh" {
		t.Fatalf("unexpected updated tokens: %+v", updated.Tokens)
	}
	if updated.LastRefresh != now.Format(time.RFC3339) {
		t.Fatalf("unexpected last_refresh: %s", updated.LastRefresh)
	}
}
