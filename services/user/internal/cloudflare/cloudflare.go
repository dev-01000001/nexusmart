package cloudflare

import (
	"context"
	"net/url"
	"strings"
)

const (
	StatusPending = "PENDING"
	StatusActive  = "ACTIVE"
	StatusFailed  = "FAILED"
)

type VerificationRecord struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

type CustomHostnameStatus struct {
	ID           string
	Hostname     string
	Status       string // "PENDING", "ACTIVE", "FAILED"
	Verification []VerificationRecord
	LastError    string
}

type Client interface {
	EnsureSubdomainRecord(ctx context.Context, host string) (string, error)
	DeleteDNSRecord(ctx context.Context, recordID string) error
	CreateCustomHostname(ctx context.Context, hostname string) (CustomHostnameStatus, error)
	GetCustomHostname(ctx context.Context, cfHostnameID, hostname string) (CustomHostnameStatus, error)
	DeleteCustomHostname(ctx context.Context, cfHostnameID string) error
}

// SanitizeError formats err as a string with any provided secret values redacted.
func SanitizeError(err error, secrets ...string) string {
	if err == nil {
		return ""
	}
	return sanitizeString(err.Error(), secrets...)
}

func sanitizeString(s string, secrets ...string) string {
	out := s
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		out = strings.ReplaceAll(out, secret, "[REDACTED]")
		trimmed := strings.TrimSpace(secret)
		if trimmed != "" && trimmed != secret {
			out = strings.ReplaceAll(out, trimmed, "[REDACTED]")
		}
		if len(trimmed) >= 4 {
			if escaped := url.QueryEscape(trimmed); escaped != "" && escaped != trimmed {
				out = strings.ReplaceAll(out, escaped, "[REDACTED]")
			}
		}
	}
	return out
}
