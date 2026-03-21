package native

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// BearerAuthFromEnv returns an auth function that reads an API key env var.
func BearerAuthFromEnv(envName string) AuthFunc {
	return func(context.Context) (string, error) {
		value := strings.TrimSpace(os.Getenv(envName))
		if value == "" {
			return "", fmt.Errorf("missing environment variable %s", envName)
		}
		return "Bearer " + value, nil
	}
}

type ChatGPTAuthOptions struct {
	Path          string
	RefreshURL    string
	RefreshBefore time.Duration
	HTTPClient    HTTPDoer
	Now           func() time.Time
}

type ChatGPTAuth struct {
	path          string
	refreshURL    string
	refreshBefore time.Duration
	httpClient    HTTPDoer
	now           func() time.Time

	mu sync.Mutex
}

type codexAuthFile struct {
	AuthMode     string          `json:"auth_mode"`
	OpenAIAPIKey string          `json:"OPENAI_API_KEY"`
	Tokens       codexAuthTokens `json:"tokens"`
	LastRefresh  string          `json:"last_refresh"`
}

type codexAuthTokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	AccountID    string `json:"account_id"`
}

type oauthRefreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
}

func NewChatGPTAuth(opts ChatGPTAuthOptions) AuthFunc {
	auth := &ChatGPTAuth{
		path:          opts.Path,
		refreshURL:    opts.RefreshURL,
		refreshBefore: opts.RefreshBefore,
		httpClient:    newHTTPClient(opts.HTTPClient),
		now:           opts.Now,
	}
	if auth.path == "" {
		auth.path = filepath.Join(userHomeDir(), ".codex", "auth.json")
	}
	if auth.refreshURL == "" {
		auth.refreshURL = "https://auth.openai.com/oauth/token"
	}
	if auth.refreshBefore == 0 {
		auth.refreshBefore = 45 * time.Minute
	}
	if auth.now == nil {
		auth.now = time.Now
	}
	return auth.Header
}

func (a *ChatGPTAuth) Header(ctx context.Context) (string, error) {
	record, err := a.AccessToken(ctx)
	if err != nil {
		return "", err
	}
	return "Bearer " + record.Tokens.AccessToken, nil
}

func (a *ChatGPTAuth) AccessToken(ctx context.Context) (*codexAuthFile, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	record, err := a.read()
	if err != nil {
		return nil, err
	}

	if record.Tokens.AccessToken == "" {
		record, err = a.refresh(ctx, record)
		if err != nil {
			return nil, err
		}
		return record, nil
	}

	if !a.needsRefresh(record) {
		return record, nil
	}

	refreshed, err := a.refresh(ctx, record)
	if err == nil {
		return refreshed, nil
	}

	// Keep the existing token if refresh fails; callers may still succeed if
	// the token remains valid.
	return record, nil
}

func (a *ChatGPTAuth) needsRefresh(record *codexAuthFile) bool {
	if record == nil {
		return true
	}
	if strings.TrimSpace(record.Tokens.AccessToken) == "" {
		return true
	}
	if strings.TrimSpace(record.LastRefresh) == "" {
		return false
	}
	lastRefresh, err := time.Parse(time.RFC3339, record.LastRefresh)
	if err != nil {
		return false
	}
	return a.now().UTC().After(lastRefresh.UTC().Add(a.refreshBefore))
}

func (a *ChatGPTAuth) read() (*codexAuthFile, error) {
	data, err := os.ReadFile(a.path)
	if err != nil {
		return nil, fmt.Errorf("read chatgpt auth file: %w", err)
	}
	var record codexAuthFile
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("decode chatgpt auth file: %w", err)
	}
	return &record, nil
}

func (a *ChatGPTAuth) refresh(ctx context.Context, record *codexAuthFile) (*codexAuthFile, error) {
	if record == nil {
		return nil, fmt.Errorf("refresh chatgpt auth: missing auth record")
	}
	if strings.TrimSpace(record.Tokens.RefreshToken) == "" {
		return nil, fmt.Errorf("refresh chatgpt auth: missing refresh token")
	}

	refreshed, err := a.refreshViaHTTP(ctx, record)
	if err == nil {
		return refreshed, nil
	}

	if cliErr := a.refreshViaCodexCLI(ctx); cliErr == nil {
		return a.read()
	}
	return nil, err
}

func (a *ChatGPTAuth) refreshViaHTTP(ctx context.Context, record *codexAuthFile) (*codexAuthFile, error) {
	values := url.Values{}
	values.Set("grant_type", "refresh_token")
	values.Set("refresh_token", record.Tokens.RefreshToken)
	if record.Tokens.AccountID != "" {
		values.Set("account_id", record.Tokens.AccountID)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.refreshURL, strings.NewReader(values.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("refresh chatgpt auth via http: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 32*1024))
		return nil, fmt.Errorf("refresh chatgpt auth via http: status %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var payload oauthRefreshResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode chatgpt refresh response: %w", err)
	}
	if strings.TrimSpace(payload.AccessToken) == "" {
		return nil, fmt.Errorf("chatgpt refresh response missing access_token")
	}

	record.Tokens.AccessToken = payload.AccessToken
	if payload.RefreshToken != "" {
		record.Tokens.RefreshToken = payload.RefreshToken
	}
	if payload.IDToken != "" {
		record.Tokens.IDToken = payload.IDToken
	}
	record.LastRefresh = a.now().UTC().Format(time.RFC3339)
	if err := a.write(record); err != nil {
		return nil, err
	}
	return record, nil
}

func (a *ChatGPTAuth) refreshViaCodexCLI(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "codex", "login", "status")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("refresh chatgpt auth via codex cli: %w", err)
	}
	return nil
}

func (a *ChatGPTAuth) write(record *codexAuthFile) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("encode refreshed chatgpt auth file: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(a.path), 0o700); err != nil {
		return fmt.Errorf("create chatgpt auth dir: %w", err)
	}
	if err := os.WriteFile(a.path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write chatgpt auth file: %w", err)
	}
	return nil
}

func userHomeDir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return home
	}
	return "."
}
