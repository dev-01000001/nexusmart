package cloudflare

import (
	"context"
	"strings"
)

// MockClient is a stateless, zero-network Cloudflare adapter for local development and e2e tests.
type MockClient struct {
	cnameTarget string
}

var _ Client = (*MockClient)(nil)

// NewMock constructs a stateless mock Cloudflare client targeting cnameTarget.
func NewMock(cnameTarget string) *MockClient {
	return &MockClient{
		cnameTarget: strings.TrimSpace(cnameTarget),
	}
}

func (m *MockClient) EnsureSubdomainRecord(ctx context.Context, host string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	h := strings.ToLower(strings.TrimSpace(host))
	return "mock-dns-" + h, nil
}

func (m *MockClient) DeleteDNSRecord(ctx context.Context, _ string) error {
	return ctx.Err()
}

func (m *MockClient) CreateCustomHostname(ctx context.Context, hostname string) (CustomHostnameStatus, error) {
	if err := ctx.Err(); err != nil {
		return CustomHostnameStatus{}, err
	}
	h := strings.ToLower(strings.TrimSpace(hostname))
	return CustomHostnameStatus{
		ID:           "mock-ch-" + h,
		Hostname:     h,
		Status:       StatusPending,
		Verification: m.verificationRecords(h),
		LastError:    "",
	}, nil
}

func (m *MockClient) GetCustomHostname(ctx context.Context, cfHostnameID, hostname string) (CustomHostnameStatus, error) {
	if err := ctx.Err(); err != nil {
		return CustomHostnameStatus{}, err
	}
	h := strings.ToLower(strings.TrimSpace(hostname))
	id := strings.TrimSpace(cfHostnameID)
	if id == "" {
		id = "mock-ch-" + h
	}

	status := StatusPending
	lastErr := ""
	switch {
	case strings.HasSuffix(h, ".verified.test"):
		status = StatusActive
	case strings.HasSuffix(h, ".fail.test"):
		status = StatusFailed
		lastErr = "mock verification failed"
	}

	return CustomHostnameStatus{
		ID:           id,
		Hostname:     h,
		Status:       status,
		Verification: m.verificationRecords(h),
		LastError:    lastErr,
	}, nil
}

func (m *MockClient) DeleteCustomHostname(ctx context.Context, _ string) error {
	return ctx.Err()
}

func (m *MockClient) verificationRecords(hostname string) []VerificationRecord {
	return []VerificationRecord{
		{
			Type:  "CNAME",
			Name:  hostname,
			Value: m.cnameTarget,
		},
		{
			Type:  "TXT",
			Name:  "_cf-custom-hostname." + hostname,
			Value: "mock-verify-" + hostname,
		},
		{
			Type:  "TXT",
			Name:  "_acme-challenge." + hostname,
			Value: "mock-ssl-" + hostname,
		},
	}
}
