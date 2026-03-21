package scanner

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/yusefmosiah/fase/eval/osint-go/internal/model"
)

type DNSScanner struct {
	LookupIPAddr func(ctx context.Context, host string) ([]net.IPAddr, error)
	LookupNS     func(ctx context.Context, host string) ([]*net.NS, error)
	LookupCNAME  func(ctx context.Context, host string) (string, error)
}

func NewDNSScanner() DNSScanner {
	return DNSScanner{
		LookupIPAddr: net.DefaultResolver.LookupIPAddr,
		LookupNS:     net.DefaultResolver.LookupNS,
		LookupCNAME:  net.DefaultResolver.LookupCNAME,
	}
}

func (s DNSScanner) Name() string { return "dns" }

func (s DNSScanner) Scan(ctx context.Context, domain string) (model.Finding, error) {
	if err := validateDomain(domain); err != nil {
		return model.Finding{}, err
	}
	lookupIPAddr := s.LookupIPAddr
	if lookupIPAddr == nil {
		lookupIPAddr = net.DefaultResolver.LookupIPAddr
	}
	lookupNS := s.LookupNS
	if lookupNS == nil {
		lookupNS = net.DefaultResolver.LookupNS
	}
	lookupCNAME := s.LookupCNAME
	if lookupCNAME == nil {
		lookupCNAME = net.DefaultResolver.LookupCNAME
	}

	ips, err := lookupIPAddr(ctx, domain)
	if err != nil {
		return model.Finding{}, err
	}
	ns, nsErr := lookupNS(ctx, domain)
	cname, cnameErr := lookupCNAME(ctx, domain)

	details := map[string]any{
		"ips":   ipStrings(ips),
		"ns":    nsStrings(ns),
		"cname": strings.TrimSuffix(cname, "."),
	}

	if nsErr != nil {
		details["ns_error"] = nsErr.Error()
	}
	if cnameErr != nil {
		details["cname_error"] = cnameErr.Error()
	}

	summary := fmt.Sprintf("resolved %d addresses, %d nameservers", len(ips), len(ns))
	if cname != "" && cname != domain {
		summary += fmt.Sprintf(", cname %s", strings.TrimSuffix(cname, "."))
	}

	return model.Finding{
		Source:  s.Name(),
		Summary: summary,
		Details: details,
	}, nil
}

func ipStrings(ips []net.IPAddr) []string {
	out := make([]string, 0, len(ips))
	for _, ip := range ips {
		out = append(out, ip.IP.String())
	}
	return out
}

func nsStrings(entries []*net.NS) []string {
	out := make([]string, 0, len(entries))
	for _, ns := range entries {
		out = append(out, strings.TrimSuffix(ns.Host, "."))
	}
	return out
}
