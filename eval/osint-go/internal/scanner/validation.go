package scanner

import (
	"errors"
	"net"
	"strings"
)

var ErrInvalidDomain = errors.New("invalid domain")

func validateDomain(domain string) error {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return ErrInvalidDomain
	}
	if strings.ContainsAny(domain, " /\\@") {
		return ErrInvalidDomain
	}
	if len(domain) > 253 {
		return ErrInvalidDomain
	}
	if strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return ErrInvalidDomain
	}
	if ip := net.ParseIP(domain); ip != nil {
		return ErrInvalidDomain
	}
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return ErrInvalidDomain
	}
	for _, label := range labels {
		if label == "" || len(label) > 63 {
			return ErrInvalidDomain
		}
		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return ErrInvalidDomain
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return ErrInvalidDomain
			}
		}
	}
	return nil
}

func ValidateDomain(domain string) error {
	return validateDomain(domain)
}
