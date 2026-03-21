package native

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
)

const (
	providerZAI     = "zai"
	providerBedrock = "bedrock"
	providerChatGPT = "chatgpt"

	zaiBaseURL              = "https://api.z.ai/api/anthropic"
	chatGPTResponsesURL     = "https://chatgpt.com/backend-api/codex/responses"
	bedrockAnthropicVersion = "bedrock-2023-05-31"
)

// AuthFunc returns a fully-formed Authorization header value.
type AuthFunc func(context.Context) (string, error)

// Provider describes one concrete native LLM backend.
type Provider struct {
	Name             string
	APIFormat        string
	BaseURL          string
	AuthFunc         AuthFunc
	AuthHeader       AuthFunc
	ModelID          string
	AnthropicVersion string
	ModelInPath      bool
	ForceNoStream    bool // Bedrock: streaming uses binary EventStream, not SSE
}

// ParseProviderModel resolves provider configuration from a "provider/model" ref.
func ParseProviderModel(model string) (Provider, error) {
	providerName, modelID, ok := strings.Cut(strings.TrimSpace(model), "/")
	if !ok || providerName == "" || strings.TrimSpace(modelID) == "" {
		return Provider{}, fmt.Errorf("native provider model must be provider/model, got %q", model)
	}

	switch providerName {
	case providerZAI:
		return Provider{
			Name:      providerZAI,
			APIFormat: apiFormatAnthropic,
			BaseURL:   zaiBaseURL,
			AuthFunc:  BearerAuthFromEnv("ZAI_API_KEY"),
			ModelID:   modelID,
		}, nil
	case providerBedrock:
		region := strings.TrimSpace(os.Getenv("AWS_REGION"))
		if region == "" {
			return Provider{}, fmt.Errorf("bedrock provider requires AWS_REGION")
		}
		return Provider{
			Name:             providerBedrock,
			APIFormat:        apiFormatAnthropic,
			BaseURL:          fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com", region),
			AuthFunc:         BearerAuthFromEnv("AWS_BEARER_TOKEN_BEDROCK"),
			ModelID:          bedrockModelID(modelID),
			AnthropicVersion: bedrockAnthropicVersion,
			ModelInPath:      true,
			ForceNoStream:    true,
		}, nil
	case providerChatGPT:
		return Provider{
			Name:      providerChatGPT,
			APIFormat: apiFormatOpenAI,
			BaseURL:   chatGPTResponsesURL,
			AuthFunc:  NewChatGPTAuth(ChatGPTAuthOptions{}),
			ModelID:   modelID,
		}, nil
	default:
		return Provider{}, fmt.Errorf("unknown native provider %q", providerName)
	}
}

// ParseProvider is kept as a compatibility alias while the Phase 1 API settles.
func ParseProvider(model string) (Provider, error) { return ParseProviderModel(model) }

// NewLLMClient builds the API-specific client for a provider.
func NewLLMClient(provider Provider, httpClient HTTPDoer) (LLMClient, error) {
	switch provider.APIFormat {
	case apiFormatAnthropic:
		return NewAnthropicClient(provider, httpClient)
	case apiFormatOpenAI:
		return NewOpenAIClient(provider, httpClient)
	default:
		return nil, fmt.Errorf("unsupported native api format %q", provider.APIFormat)
	}
}

func (p Provider) authHeader(ctx context.Context) (string, error) {
	if p.AuthHeader != nil {
		return p.AuthHeader(ctx)
	}
	if p.AuthFunc == nil {
		return "", fmt.Errorf("provider %q has no auth function", p.Name)
	}
	return p.AuthFunc(ctx)
}

// bedrockModelID maps user-friendly model names to Bedrock model identifiers.
func bedrockModelID(name string) string {
	mapping := map[string]string{
		"claude-haiku-4-5":  "us.anthropic.claude-haiku-4-5-20251001-v1:0",
		"claude-sonnet-4-6": "us.anthropic.claude-sonnet-4-6",
		"claude-opus-4-6":   "us.anthropic.claude-opus-4-6-v1",
	}
	if mapped, ok := mapping[name]; ok {
		return mapped
	}
	return name // pass through if already a full Bedrock ID
}

func (p Provider) anthropicEndpoint(stream bool) (string, error) {
	if p.ModelInPath {
		path := "/model/" + url.PathEscape(p.ModelID) + "/"
		if stream {
			return strings.TrimRight(p.BaseURL, "/") + path + "invoke-with-response-stream", nil
		}
		return strings.TrimRight(p.BaseURL, "/") + path + "invoke", nil
	}
	return strings.TrimRight(p.BaseURL, "/") + "/v1/messages", nil
}
