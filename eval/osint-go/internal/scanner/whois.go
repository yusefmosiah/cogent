package scanner

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/yusefmosiah/fase/eval/osint-go/internal/model"
)

type WhoisScanner struct {
	Lookup func(ctx context.Context, domain string) (string, error)
}

func NewWhoisScanner() WhoisScanner {
	return WhoisScanner{
		Lookup: commandWhois,
	}
}

func (s WhoisScanner) Name() string { return "whois" }

func (s WhoisScanner) Scan(ctx context.Context, domain string) (model.Finding, error) {
	if err := validateDomain(domain); err != nil {
		return model.Finding{}, err
	}
	lookup := s.Lookup
	if lookup == nil {
		lookup = commandWhois
	}

	output, err := lookup(ctx, domain)
	if err != nil {
		return model.Finding{}, err
	}

	record := ParseWhois(output)
	record["raw_lines"] = countNonEmptyLines(output)
	return model.Finding{
		Source:  s.Name(),
		Summary: whoisSummary(record),
		Details: record,
	}, nil
}

func commandWhois(ctx context.Context, domain string) (string, error) {
	cmd := exec.CommandContext(ctx, "whois", domain)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("whois lookup failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func ParseWhois(output string) map[string]any {
	fields := map[string][]string{}
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "%") || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		fields[key] = append(fields[key], value)
	}

	result := map[string]any{}
	for _, key := range []string{"registrar", "creation date", "updated date", "expiration date", "name server", "domain status", "org", "organization"} {
		if values := fields[key]; len(values) > 0 {
			result[strings.ReplaceAll(key, " ", "_")] = unique(values)
		}
	}

	if values := fields["name server"]; len(values) > 0 {
		result["name_servers"] = unique(values)
	}
	return result
}

func whoisSummary(details map[string]any) string {
	registrar, _ := details["registrar"].([]string)
	expiration, _ := details["expiration_date"].([]string)
	parts := []string{"whois record parsed"}
	if len(registrar) > 0 {
		parts = append(parts, "registrar "+registrar[0])
	}
	if len(expiration) > 0 {
		parts = append(parts, "expires "+expiration[0])
	}
	return strings.Join(parts, ", ")
}

func countNonEmptyLines(output string) int {
	count := 0
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) != "" {
			count++
		}
	}
	return count
}

func unique(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

var errWhoisOutputEmpty = errors.New("empty whois output")
