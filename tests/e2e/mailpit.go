package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// MailpitClient reads messages captured by the local Mailpit SMTP sandbox.
type MailpitClient struct {
	baseURL string
	http    *http.Client
}

// MailpitAddress represents an email address in Mailpit API payloads.
type MailpitAddress struct {
	Name    string `json:"Name"`
	Address string `json:"Address"`
}

// MailpitSummary is a single message entry returned by GET /api/v1/messages.
type MailpitSummary struct {
	ID      string           `json:"ID"`
	From    MailpitAddress   `json:"From"`
	To      []MailpitAddress `json:"To"`
	Subject string           `json:"Subject"`
	Snippet string           `json:"Snippet"`
	Created string           `json:"Created"`
}

// MailpitListResponse is the response envelope from GET /api/v1/messages.
type MailpitListResponse struct {
	Total    int              `json:"total"`
	Count    int              `json:"count"`
	Messages []MailpitSummary `json:"messages"`
}

// MailpitMessage is the full message body from GET /api/v1/message/{id}.
type MailpitMessage struct {
	ID      string           `json:"ID"`
	Subject string           `json:"Subject"`
	Text    string           `json:"Text"`
	HTML    string           `json:"HTML"`
	To      []MailpitAddress `json:"To"`
}

var (
	otpContextualRe = regexp.MustCompile(`(?i)(?:otp|code|verification|mã|xác\s*thực)[^0-9]{0,24}(\d{6})\b`)
	otpGenericRe    = regexp.MustCompile(`\b(\d{6})\b`)
)

// NewMailpitClient creates a Mailpit REST API client.
func NewMailpitClient(baseURL string, timeout time.Duration) *MailpitClient {
	return &MailpitClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: timeout},
	}
}

// ListMessages fetches the most recent messages from Mailpit.
func (m *MailpitClient) ListMessages(ctx context.Context, limit int) (*MailpitListResponse, error) {
	url := fmt.Sprintf("%s/api/v1/messages?limit=%d", m.baseURL, limit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build mailpit list request: %w", err)
	}
	resp, err := m.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mailpit list messages: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read mailpit list body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mailpit list returned HTTP %d: %s", resp.StatusCode, string(body))
	}
	var out MailpitListResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode mailpit list: %w", err)
	}
	return &out, nil
}

// GetMessage retrieves the full content of a Mailpit message by ID.
func (m *MailpitClient) GetMessage(ctx context.Context, id string) (*MailpitMessage, error) {
	url := fmt.Sprintf("%s/api/v1/message/%s", m.baseURL, id)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build mailpit message request: %w", err)
	}
	resp, err := m.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mailpit get message %s: %w", id, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read mailpit message body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mailpit get message %s returned HTTP %d: %s", id, resp.StatusCode, string(body))
	}
	var out MailpitMessage
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode mailpit message: %w", err)
	}
	return &out, nil
}

// WaitForEmail polls Mailpit until an email addressed to `toEmail` matching optional `contains` arrives.
func (m *MailpitClient) WaitForEmail(
	ctx context.Context,
	toEmail string,
	contains string,
	timeout time.Duration,
) (*MailpitMessage, error) {
	deadline := time.Now().Add(timeout)
	targetEmail := strings.ToLower(strings.TrimSpace(toEmail))
	needle := strings.ToLower(strings.TrimSpace(contains))

	var lastErr error
	for time.Now().Before(deadline) {
		list, err := m.ListMessages(ctx, 100)
		if err != nil {
			lastErr = err
		} else {
			for _, summary := range list.Messages {
				if !hasRecipient(summary.To, targetEmail) {
					continue
				}
				full, getErr := m.GetMessage(ctx, summary.ID)
				if getErr != nil {
					lastErr = getErr
					continue
				}
				if needle == "" || messageContains(full, summary.Snippet, needle) {
					return full, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}

	if lastErr != nil {
		return nil, fmt.Errorf("timed out waiting for Mailpit email to %s (contains=%q); last error: %w", toEmail, contains, lastErr)
	}
	return nil, fmt.Errorf("timed out waiting for Mailpit email to %s (contains=%q)", toEmail, contains)
}

// WaitForOTP polls Mailpit for a 6-digit OTP verification email addressed to `toEmail`.
func (m *MailpitClient) WaitForOTP(ctx context.Context, toEmail string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	targetEmail := strings.ToLower(strings.TrimSpace(toEmail))

	var lastErr error
	for time.Now().Before(deadline) {
		list, err := m.ListMessages(ctx, 100)
		if err != nil {
			lastErr = err
		} else {
			for _, summary := range list.Messages {
				if !hasRecipient(summary.To, targetEmail) {
					continue
				}
				full, getErr := m.GetMessage(ctx, summary.ID)
				if getErr != nil {
					lastErr = getErr
					continue
				}
				if otp := ExtractOTP(full.Subject, full.Text, full.HTML, summary.Snippet); otp != "" {
					return otp, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}

	if lastErr != nil {
		return "", fmt.Errorf("timed out waiting for OTP email to %s; last error: %w", toEmail, lastErr)
	}
	return "", fmt.Errorf("timed out waiting for 6-digit OTP email to %s", toEmail)
}

// SnapshotRecipientMessageIDs captures the current set of Mailpit message IDs addressed to `toEmail`.
// Call before triggering a new email (such as phone OTP) so polling ignores older emails like registration OTP.
func (m *MailpitClient) SnapshotRecipientMessageIDs(ctx context.Context, toEmail string) (map[string]struct{}, error) {
	seen := make(map[string]struct{})
	targetEmail := strings.ToLower(strings.TrimSpace(toEmail))
	list, err := m.ListMessages(ctx, 200)
	if err != nil {
		return nil, err
	}
	for _, summary := range list.Messages {
		if hasRecipient(summary.To, targetEmail) {
			seen[summary.ID] = struct{}{}
		}
	}
	return seen, nil
}

// WaitForNewOTPMessage polls Mailpit for a newly arrived email addressed to `toEmail` whose ID is not
// in `seenIDs` and extracts a 6-digit OTP strictly from the email body (Text/HTML, never Subject).
func (m *MailpitClient) WaitForNewOTPMessage(
	ctx context.Context,
	toEmail string,
	seenIDs map[string]struct{},
	timeout time.Duration,
) (*MailpitMessage, string, error) {
	deadline := time.Now().Add(timeout)
	targetEmail := strings.ToLower(strings.TrimSpace(toEmail))

	var lastErr error
	for time.Now().Before(deadline) {
		list, err := m.ListMessages(ctx, 100)
		if err != nil {
			lastErr = err
		} else {
			for _, summary := range list.Messages {
				if !hasRecipient(summary.To, targetEmail) {
					continue
				}
				if _, old := seenIDs[summary.ID]; old {
					continue
				}
				full, getErr := m.GetMessage(ctx, summary.ID)
				if getErr != nil {
					lastErr = getErr
					continue
				}
				if otp := ExtractOTP(full.Text, full.HTML); otp != "" {
					return full, otp, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}

	if lastErr != nil {
		return nil, "", fmt.Errorf("timed out waiting for new OTP email to %s; last error: %w", toEmail, lastErr)
	}
	return nil, "", fmt.Errorf("timed out waiting for new 6-digit OTP email to %s", toEmail)
}

// ExtractOTP pulls a 6-digit numeric OTP from email subject/body text.
func ExtractOTP(parts ...string) string {
	combined := strings.Join(parts, "\n")
	if m := otpContextualRe.FindStringSubmatch(combined); len(m) == 2 {
		return m[1]
	}
	if m := otpGenericRe.FindStringSubmatch(combined); len(m) == 2 {
		return m[1]
	}
	return ""
}

func hasRecipient(addrs []MailpitAddress, lowerEmail string) bool {
	for _, a := range addrs {
		if strings.ToLower(strings.TrimSpace(a.Address)) == lowerEmail {
			return true
		}
	}
	return false
}

func messageContains(msg *MailpitMessage, snippet, lowerNeedle string) bool {
	haystack := strings.ToLower(msg.Subject + "\n" + msg.Text + "\n" + msg.HTML + "\n" + snippet)
	return strings.Contains(haystack, lowerNeedle)
}
