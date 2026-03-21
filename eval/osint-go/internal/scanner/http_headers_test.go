package scanner

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPHeaderScannerScan(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "test-server")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	scanner := HTTPHeaderScanner{
		Client: server.Client(),
		BaseURL: func(string) []string {
			return []string{server.URL}
		},
	}

	finding, err := scanner.Scan(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if finding.Source != "http_headers" {
		t.Fatalf("unexpected source: %s", finding.Source)
	}
	if finding.Details["server"] != "test-server" {
		t.Fatalf("unexpected server: %#v", finding.Details["server"])
	}
	security, ok := finding.Details["security_headers"].(map[string]string)
	if !ok || security["X-Frame-Options"] != "DENY" {
		t.Fatalf("unexpected security headers: %#v", finding.Details["security_headers"])
	}
}
