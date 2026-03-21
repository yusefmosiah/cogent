package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/yusefmosiah/fase/eval/osint-go/internal/model"
	"github.com/yusefmosiah/fase/eval/osint-go/internal/repository"
	"github.com/yusefmosiah/fase/eval/osint-go/internal/service"
)

type stubScanner struct {
	name string
}

func (s stubScanner) Name() string { return s.name }

func (s stubScanner) Scan(ctx context.Context, domain string) (model.Finding, error) {
	return model.Finding{Source: s.name, Summary: domain + " ok"}, nil
}

func TestHTTPAPI(t *testing.T) {
	t.Parallel()

	cache := repository.NewCache(time.Minute)
	t.Cleanup(cache.Close)
	agg := service.NewAggregator(cache, time.Second,
		stubScanner{name: "dns"},
		stubScanner{name: "whois"},
		stubScanner{name: "http_headers"},
	)

	h := New(agg, cache)
	server := httptest.NewServer(h.Routes())
	t.Cleanup(server.Close)

	resp, err := http.Post(server.URL+"/scan", "application/json", bytes.NewBufferString(`{"domain":"example.com"}`))
	if err != nil {
		t.Fatalf("post scan failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %s", resp.Status)
	}

	var result model.ScanResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode scan response: %v", err)
	}
	if result.ID == "" {
		t.Fatalf("missing result id")
	}

	got, err := http.Get(server.URL + "/results/" + result.ID)
	if err != nil {
		t.Fatalf("get result failed: %v", err)
	}
	defer got.Body.Close()
	if got.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %s", got.Status)
	}
}
