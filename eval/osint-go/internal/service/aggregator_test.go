package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yusefmosiah/fase/eval/osint-go/internal/model"
	"github.com/yusefmosiah/fase/eval/osint-go/internal/scanner"
)

type fakeStore struct {
	result model.ScanResult
}

func (f *fakeStore) Save(_ context.Context, result model.ScanResult) { f.result = result }
func (f *fakeStore) Get(_ context.Context, id string) (model.ScanResult, bool) {
	if f.result.ID == id {
		return f.result, true
	}
	return model.ScanResult{}, false
}

type fakeScanner struct {
	name    string
	delay   time.Duration
	finding model.Finding
	err     error
}

func (f fakeScanner) Name() string { return f.name }

func (f fakeScanner) Scan(ctx context.Context, domain string) (model.Finding, error) {
	select {
	case <-time.After(f.delay):
	case <-ctx.Done():
		return model.Finding{}, ctx.Err()
	}
	if f.err != nil {
		return model.Finding{}, f.err
	}
	f.finding.Source = f.name
	f.finding.Details = map[string]any{"domain": domain}
	return f.finding, nil
}

func TestAggregatorScan(t *testing.T) {
	t.Parallel()

	t.Run("parallel scanners", func(t *testing.T) {
		t.Parallel()
		store := &fakeStore{}
		agg := NewAggregator(store, 500*time.Millisecond,
			fakeScanner{name: "dns", delay: 75 * time.Millisecond, finding: model.Finding{Summary: "dns ok"}},
			fakeScanner{name: "whois", delay: 75 * time.Millisecond, finding: model.Finding{Summary: "whois ok"}},
			fakeScanner{name: "http_headers", delay: 75 * time.Millisecond, finding: model.Finding{Summary: "headers ok"}},
		)
		start := time.Now()
		result, err := agg.Scan(context.Background(), "example.com")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := time.Since(start); got >= 180*time.Millisecond {
			t.Fatalf("expected concurrent execution, took %s", got)
		}
		if len(result.Findings) != 3 {
			t.Fatalf("expected 3 findings, got %d", len(result.Findings))
		}
		if store.result.ID == "" {
			t.Fatalf("expected cached result")
		}
	})

	tests := []struct {
		name        string
		domain      string
		scanners    []scanner.Scanner
		timeout     time.Duration
		wantErr     bool
		wantPartial bool
	}{
		{
			name:   "invalid domain",
			domain: "bad domain",
			scanners: []scanner.Scanner{
				fakeScanner{name: "dns"},
			},
			timeout: 100 * time.Millisecond,
			wantErr: true,
		},
		{
			name:   "partial failure",
			domain: "example.com",
			scanners: []scanner.Scanner{
				fakeScanner{name: "dns", finding: model.Finding{Summary: "ok"}},
				fakeScanner{name: "whois", err: errors.New("whois unavailable")},
				fakeScanner{name: "http_headers", finding: model.Finding{Summary: "ok"}},
			},
			timeout:     100 * time.Millisecond,
			wantPartial: true,
		},
		{
			name:   "timeout",
			domain: "example.com",
			scanners: []scanner.Scanner{
				fakeScanner{name: "dns", delay: 200 * time.Millisecond, finding: model.Finding{Summary: "slow"}},
			},
			timeout:     25 * time.Millisecond,
			wantErr:     false,
			wantPartial: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			agg := NewAggregator(&fakeStore{}, tc.timeout, tc.scanners...)
			result, err := agg.Scan(context.Background(), tc.domain)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.PartialFailure != tc.wantPartial {
				t.Fatalf("unexpected partial failure state: %+v", result)
			}
		})
	}
}
