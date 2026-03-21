package scanner

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestParseWhois(t *testing.T) {
	t.Parallel()

	output := `
Registrar: Example Registrar, Inc.
Creation Date: 2001-01-01
Expiration Date: 2031-01-01
Name Server: NS1.EXAMPLE.NET
Name Server: NS2.EXAMPLE.NET
`
	got := ParseWhois(output)
	if got["registrar"].([]string)[0] != "Example Registrar, Inc." {
		t.Fatalf("unexpected registrar: %#v", got["registrar"])
	}
	if len(got["name_servers"].([]string)) != 2 {
		t.Fatalf("unexpected name servers: %#v", got["name_servers"])
	}
}

func TestWhoisScannerScan(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		lookup  func(context.Context, string) (string, error)
		domain  string
		wantErr bool
	}{
		{
			name:   "success",
			domain: "example.com",
			lookup: func(context.Context, string) (string, error) {
				return "Registrar: Example Registrar, Inc.\nExpiration Date: 2031-01-01\n", nil
			},
		},
		{
			name:    "invalid domain",
			domain:  "not a domain",
			wantErr: true,
			lookup:  func(context.Context, string) (string, error) { return "", nil },
		},
		{
			name:    "lookup failure",
			domain:  "example.com",
			wantErr: true,
			lookup:  func(context.Context, string) (string, error) { return "", errors.New("whois timeout") },
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			finding, err := WhoisScanner{Lookup: tc.lookup}.Scan(context.Background(), tc.domain)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if finding.Source != "whois" {
				t.Fatalf("unexpected source: %s", finding.Source)
			}
			if !strings.Contains(finding.Summary, "registrar Example Registrar, Inc.") {
				t.Fatalf("unexpected summary: %s", finding.Summary)
			}
		})
	}
}
