package scanner

import (
	"context"
	"errors"
	"net"
	"testing"
)

func TestDNSScannerScan(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		domain  string
		scanner DNSScanner
		wantErr bool
	}{
		{
			name:   "success",
			domain: "example.com",
			scanner: DNSScanner{
				LookupIPAddr: func(context.Context, string) ([]net.IPAddr, error) {
					return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
				},
				LookupNS: func(context.Context, string) ([]*net.NS, error) {
					return []*net.NS{{Host: "ns1.example.net."}}, nil
				},
				LookupCNAME: func(context.Context, string) (string, error) {
					return "alias.example.com.", nil
				},
			},
		},
		{
			name:    "invalid domain",
			domain:  "bad domain",
			scanner: DNSScanner{},
			wantErr: true,
		},
		{
			name:   "lookup failure",
			domain: "example.com",
			scanner: DNSScanner{
				LookupIPAddr: func(context.Context, string) ([]net.IPAddr, error) {
					return nil, errors.New("dns down")
				},
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			finding, err := tc.scanner.Scan(context.Background(), tc.domain)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if finding.Source != "dns" {
				t.Fatalf("unexpected source: %s", finding.Source)
			}
			if finding.Details["cname"] != "alias.example.com" {
				t.Fatalf("unexpected cname: %#v", finding.Details["cname"])
			}
		})
	}
}
