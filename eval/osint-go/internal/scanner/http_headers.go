package scanner

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/yusefmosiah/fase/eval/osint-go/internal/model"
)

type HTTPHeaderScanner struct {
	Client  *http.Client
	BaseURL func(domain string) []string
}

func NewHTTPHeaderScanner() HTTPHeaderScanner {
	return HTTPHeaderScanner{
		Client:  &http.Client{Timeout: 10 * time.Second},
		BaseURL: defaultBaseURL,
	}
}

func (s HTTPHeaderScanner) Name() string { return "http_headers" }

func (s HTTPHeaderScanner) Scan(ctx context.Context, domain string) (model.Finding, error) {
	if err := validateDomain(domain); err != nil {
		return model.Finding{}, err
	}
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	baseURLs := s.BaseURL
	if baseURLs == nil {
		baseURLs = defaultBaseURL
	}

	var lastErr error
	for _, endpoint := range baseURLs(domain) {
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, endpoint, nil)
		if err != nil {
			return model.Finding{}, err
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		resp.Body.Close()
		details := analyzeHeaders(resp.Header)
		details["url"] = endpoint
		details["status_code"] = resp.StatusCode
		details["protocol"] = resp.Proto
		return model.Finding{
			Source:  s.Name(),
			Summary: headerSummary(details),
			Details: details,
		}, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no endpoints tried")
	}
	return model.Finding{}, lastErr
}

func defaultBaseURL(domain string) []string {
	return []string{
		"https://" + domain,
		"http://" + domain,
	}
}

func analyzeHeaders(headers http.Header) map[string]any {
	interesting := []string{
		"Server",
		"X-Powered-By",
		"Strict-Transport-Security",
		"Content-Security-Policy",
		"X-Frame-Options",
		"X-Content-Type-Options",
		"Referrer-Policy",
		"Permissions-Policy",
	}
	details := make(map[string]any, len(interesting))
	security := map[string]string{}
	for _, name := range interesting {
		if value := headers.Get(name); value != "" {
			details[strings.ToLower(strings.ReplaceAll(name, "-", "_"))] = value
			if name != "Server" && name != "X-Powered-By" {
				security[name] = value
			}
		}
	}
	if len(security) > 0 {
		details["security_headers"] = security
	}
	return details
}

func headerSummary(details map[string]any) string {
	server, _ := details["server"].(string)
	powered, _ := details["x_powered_by"].(string)
	parts := []string{"http headers analyzed"}
	if server != "" {
		parts = append(parts, "server "+server)
	}
	if powered != "" {
		parts = append(parts, "powered by "+powered)
	}
	return strings.Join(parts, ", ")
}

func normalizeURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return u.String()
}
