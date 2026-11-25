package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultBaseURL     = "https://api.cloudflare.com/client/v4"
	defaultTimeout     = 10 * time.Second
	defaultMaxRetries  = 3
	defaultBaseBackoff = 200 * time.Millisecond
	maxRetryAfter      = 5 * time.Second
	maxResponseBytes   = 1 << 20 // 1 MiB
)

// LiveConfig configures the live Cloudflare v4 API adapter.
type LiveConfig struct {
	APIToken    string
	ZoneID      string
	OriginHost  string
	CNAMETarget string
	BaseURL     string
	Timeout     time.Duration
	MaxRetries  int
	BaseBackoff time.Duration
	HTTPClient  *http.Client
}

// LiveClient communicates with the Cloudflare v4 REST API over stdlib net/http.
type LiveClient struct {
	apiToken    string
	zoneID      string
	originHost  string
	cnameTarget string
	baseURL     string
	maxRetries  int
	baseBackoff time.Duration
	httpClient  *http.Client
}

var _ Client = (*LiveClient)(nil)

// NewLive validates cfg and constructs a LiveClient.
func NewLive(cfg LiveConfig) (*LiveClient, error) {
	apiToken := strings.TrimSpace(cfg.APIToken)
	zoneID := strings.TrimSpace(cfg.ZoneID)
	originHost := strings.TrimSpace(cfg.OriginHost)
	cnameTarget := strings.TrimSpace(cfg.CNAMETarget)

	if apiToken == "" {
		return nil, errors.New("cloudflare: APIToken is required")
	}
	if zoneID == "" {
		return nil, errors.New("cloudflare: ZoneID is required")
	}
	if originHost == "" {
		return nil, errors.New("cloudflare: OriginHost is required")
	}
	if cnameTarget == "" {
		return nil, errors.New("cloudflare: CNAMETarget is required")
	}

	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	maxRetries := cfg.MaxRetries
	if maxRetries <= 0 {
		maxRetries = defaultMaxRetries
	}

	baseBackoff := cfg.BaseBackoff
	if baseBackoff <= 0 {
		baseBackoff = defaultBaseBackoff
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: timeout}
	}

	return &LiveClient{
		apiToken:    apiToken,
		zoneID:      zoneID,
		originHost:  originHost,
		cnameTarget: cnameTarget,
		baseURL:     baseURL,
		maxRetries:  maxRetries,
		baseBackoff: baseBackoff,
		httpClient:  httpClient,
	}, nil
}

type cfAPIError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type cfEnvelope struct {
	Success bool            `json:"success"`
	Errors  []cfAPIError    `json:"errors"`
	Result  json.RawMessage `json:"result"`
}

type cfDNSRecord struct {
	ID string `json:"id"`
}

type createDNSRecordRequest struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
	TTL     int    `json:"ttl"`
}

type createCustomHostnameRequest struct {
	Hostname string                        `json:"hostname"`
	SSL      createCustomHostnameSSLConfig `json:"ssl"`
}

type createCustomHostnameSSLConfig struct {
	Method string `json:"method"`
	Type   string `json:"type"`
}

type cfOwnershipVerification struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

type cfSSLValidationRecord struct {
	TXTName  string `json:"txt_name"`
	TXTValue string `json:"txt_value"`
}

type cfSSLValidationError struct {
	Message string `json:"message"`
}

type cfCustomHostnameSSL struct {
	Status            string                  `json:"status"`
	ValidationRecords []cfSSLValidationRecord `json:"validation_records"`
	ValidationErrors  []cfSSLValidationError  `json:"validation_errors"`
}

type cfCustomHostname struct {
	ID                    string                   `json:"id"`
	Hostname              string                   `json:"hostname"`
	Status                string                   `json:"status"`
	VerificationErrors    []string                 `json:"verification_errors"`
	OwnershipVerification *cfOwnershipVerification `json:"ownership_verification"`
	SSL                   cfCustomHostnameSSL      `json:"ssl"`
}

// redactedError exposes only a sanitized message while optionally preserving
// safe context sentinel errors for errors.Is checks.
type redactedError struct {
	msg      string
	sentinel error
}

func (e *redactedError) Error() string { return e.msg }
func (e *redactedError) Unwrap() error { return e.sentinel }

func (c *LiveClient) sanitizeErr(err error) error {
	if err == nil {
		return nil
	}
	var sentinel error
	switch {
	case errors.Is(err, context.Canceled):
		sentinel = context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		sentinel = context.DeadlineExceeded
	}
	return &redactedError{
		msg:      SanitizeError(err, c.apiToken),
		sentinel: sentinel,
	}
}

func (c *LiveClient) EnsureSubdomainRecord(ctx context.Context, host string) (string, error) {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "" {
		return "", errors.New("cloudflare: host is required")
	}

	queryPath := fmt.Sprintf("/zones/%s/dns_records?type=CNAME&name=%s",
		url.PathEscape(c.zoneID),
		url.QueryEscape(h),
	)
	var existing []cfDNSRecord
	if _, err := c.doJSON(ctx, http.MethodGet, queryPath, nil, false, &existing); err != nil {
		return "", err
	}
	if len(existing) > 0 && existing[0].ID != "" {
		return existing[0].ID, nil
	}

	createPath := fmt.Sprintf("/zones/%s/dns_records", url.PathEscape(c.zoneID))
	reqBody := createDNSRecordRequest{
		Type:    "CNAME",
		Name:    h,
		Content: c.originHost,
		Proxied: true,
		TTL:     1,
	}
	var created cfDNSRecord
	if _, err := c.doJSON(ctx, http.MethodPost, createPath, reqBody, false, &created); err != nil {
		return "", err
	}
	if created.ID == "" {
		return "", errors.New("cloudflare: created dns record missing id")
	}
	return created.ID, nil
}

func (c *LiveClient) DeleteDNSRecord(ctx context.Context, recordID string) error {
	id := strings.TrimSpace(recordID)
	if id == "" {
		return errors.New("cloudflare: recordID is required")
	}
	path := fmt.Sprintf("/zones/%s/dns_records/%s", url.PathEscape(c.zoneID), url.PathEscape(id))
	_, err := c.doJSON(ctx, http.MethodDelete, path, nil, true, nil)
	return err
}

func (c *LiveClient) CreateCustomHostname(ctx context.Context, hostname string) (CustomHostnameStatus, error) {
	h := strings.ToLower(strings.TrimSpace(hostname))
	if h == "" {
		return CustomHostnameStatus{}, errors.New("cloudflare: hostname is required")
	}
	path := fmt.Sprintf("/zones/%s/custom_hostnames", url.PathEscape(c.zoneID))
	reqBody := createCustomHostnameRequest{
		Hostname: h,
		SSL: createCustomHostnameSSLConfig{
			Method: "txt",
			Type:   "dv",
		},
	}
	var res cfCustomHostname
	if _, err := c.doJSON(ctx, http.MethodPost, path, reqBody, false, &res); err != nil {
		return CustomHostnameStatus{}, err
	}
	return c.mapCustomHostname(res, h), nil
}

func (c *LiveClient) GetCustomHostname(ctx context.Context, cfHostnameID, hostname string) (CustomHostnameStatus, error) {
	id := strings.TrimSpace(cfHostnameID)
	if id == "" {
		return CustomHostnameStatus{}, errors.New("cloudflare: custom hostname id is required")
	}
	path := fmt.Sprintf("/zones/%s/custom_hostnames/%s", url.PathEscape(c.zoneID), url.PathEscape(id))
	var res cfCustomHostname
	if _, err := c.doJSON(ctx, http.MethodGet, path, nil, false, &res); err != nil {
		return CustomHostnameStatus{}, err
	}
	return c.mapCustomHostname(res, strings.ToLower(strings.TrimSpace(hostname))), nil
}

func (c *LiveClient) DeleteCustomHostname(ctx context.Context, cfHostnameID string) error {
	id := strings.TrimSpace(cfHostnameID)
	if id == "" {
		return errors.New("cloudflare: custom hostname id is required")
	}
	path := fmt.Sprintf("/zones/%s/custom_hostnames/%s", url.PathEscape(c.zoneID), url.PathEscape(id))
	_, err := c.doJSON(ctx, http.MethodDelete, path, nil, true, nil)
	return err
}

func (c *LiveClient) mapCustomHostname(res cfCustomHostname, fallbackHost string) CustomHostnameStatus {
	host := strings.ToLower(strings.TrimSpace(res.Hostname))
	if host == "" {
		host = fallbackHost
	}

	records := make([]VerificationRecord, 0, 3)
	seen := make(map[string]struct{}, 4)
	addRecord := func(recType, name, value string) {
		t := strings.ToUpper(strings.TrimSpace(recType))
		n := strings.TrimSpace(name)
		v := strings.TrimSpace(value)
		if t == "" || n == "" || v == "" {
			return
		}
		key := t + "\x00" + strings.ToLower(n) + "\x00" + v
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		records = append(records, VerificationRecord{
			Type:  t,
			Name:  n,
			Value: v,
		})
	}

	addRecord("CNAME", host, c.cnameTarget)
	if res.OwnershipVerification != nil {
		recType := res.OwnershipVerification.Type
		if strings.TrimSpace(recType) == "" {
			recType = "TXT"
		}
		addRecord(recType, res.OwnershipVerification.Name, res.OwnershipVerification.Value)
	}
	for _, vr := range res.SSL.ValidationRecords {
		addRecord("TXT", vr.TXTName, vr.TXTValue)
	}

	hostStatus := strings.ToLower(strings.TrimSpace(res.Status))
	sslStatus := strings.ToLower(strings.TrimSpace(res.SSL.Status))

	status := StatusPending
	switch {
	case hostStatus == "active" && sslStatus == "active":
		status = StatusActive
	case hostStatus == "blocked" || hostStatus == "moved" || hostStatus == "deleted":
		status = StatusFailed
	case sslStatus == "validation_timed_out" || sslStatus == "expired" || sslStatus == "deleted":
		status = StatusFailed
	}

	lastErr := ""
	if status != StatusActive {
		errSeen := make(map[string]struct{})
		errParts := make([]string, 0, len(res.VerificationErrors)+len(res.SSL.ValidationErrors))
		appendErr := func(msg string) {
			m := strings.TrimSpace(msg)
			if m == "" {
				return
			}
			if _, ok := errSeen[m]; ok {
				return
			}
			errSeen[m] = struct{}{}
			errParts = append(errParts, m)
		}
		for _, ve := range res.VerificationErrors {
			appendErr(ve)
		}
		for _, se := range res.SSL.ValidationErrors {
			appendErr(se.Message)
		}
		if len(errParts) > 0 {
			lastErr = sanitizeString(strings.Join(errParts, "; "), c.apiToken)
		} else if status == StatusFailed {
			lastErr = fmt.Sprintf("custom hostname verification failed (status=%s, ssl_status=%s)", hostStatus, sslStatus)
		}
	}

	return CustomHostnameStatus{
		ID:           strings.TrimSpace(res.ID),
		Hostname:     host,
		Status:       status,
		Verification: records,
		LastError:    lastErr,
	}
}

func (c *LiveClient) doJSON(ctx context.Context, method, path string, reqBody any, allowNotFound bool, out any) (bool, error) {
	var payload []byte
	if reqBody != nil {
		var err error
		payload, err = json.Marshal(reqBody)
		if err != nil {
			return false, c.sanitizeErr(fmt.Errorf("cloudflare marshal request: %w", err))
		}
	}

	fullURL := c.baseURL + path

	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return false, c.sanitizeErr(err)
		}

		status, body, retryAfter, err := c.executeOnce(ctx, method, fullURL, payload)
		if err != nil {
			if ctx.Err() != nil {
				return false, c.sanitizeErr(ctx.Err())
			}
			if attempt < c.maxRetries {
				if sleepErr := sleepWithContext(ctx, c.backoffDelay(attempt, "")); sleepErr != nil {
					return false, c.sanitizeErr(sleepErr)
				}
				continue
			}
			return false, c.sanitizeErr(fmt.Errorf("cloudflare %s %s: %w", method, path, err))
		}

		if allowNotFound && status == http.StatusNotFound {
			return false, nil
		}

		if status == http.StatusTooManyRequests || status >= http.StatusInternalServerError {
			if attempt < c.maxRetries {
				if sleepErr := sleepWithContext(ctx, c.backoffDelay(attempt, retryAfter)); sleepErr != nil {
					return false, c.sanitizeErr(sleepErr)
				}
				continue
			}
			return false, c.sanitizeErr(formatAPIError(method, path, status, body))
		}

		if status < http.StatusOK || status >= http.StatusMultipleChoices {
			return false, c.sanitizeErr(formatAPIError(method, path, status, body))
		}

		var env cfEnvelope
		if err := json.Unmarshal(body, &env); err != nil {
			return false, c.sanitizeErr(fmt.Errorf("cloudflare %s %s: decode response: %w", method, path, err))
		}
		if !env.Success {
			return false, c.sanitizeErr(formatAPIError(method, path, status, body))
		}
		if out != nil && len(env.Result) > 0 && string(env.Result) != "null" {
			if err := json.Unmarshal(env.Result, out); err != nil {
				return false, c.sanitizeErr(fmt.Errorf("cloudflare %s %s: decode result: %w", method, path, err))
			}
		}
		return true, nil
	}

	return false, c.sanitizeErr(fmt.Errorf("cloudflare %s %s: retry budget exhausted", method, path))
}

func (c *LiveClient) executeOnce(ctx context.Context, method, fullURL string, payload []byte) (int, []byte, string, error) {
	var bodyReader io.Reader
	if payload != nil {
		bodyReader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, bodyReader)
	if err != nil {
		return 0, nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiToken)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, nil, "", err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return resp.StatusCode, nil, resp.Header.Get("Retry-After"), err
	}
	return resp.StatusCode, body, resp.Header.Get("Retry-After"), nil
}

func (c *LiveClient) backoffDelay(attempt int, retryAfter string) time.Duration {
	if trimmed := strings.TrimSpace(retryAfter); trimmed != "" {
		if secs, err := strconv.Atoi(trimmed); err == nil && secs >= 0 {
			d := time.Duration(secs) * time.Second
			if d > maxRetryAfter {
				return maxRetryAfter
			}
			return d
		}
	}
	shift := attempt
	if shift > 10 {
		shift = 10
	}
	d := c.baseBackoff << shift
	if d <= 0 || d > maxRetryAfter {
		return maxRetryAfter
	}
	return d
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func formatAPIError(method, path string, status int, body []byte) error {
	var env cfEnvelope
	if len(body) > 0 && json.Unmarshal(body, &env) == nil && len(env.Errors) > 0 {
		parts := make([]string, 0, len(env.Errors))
		for _, e := range env.Errors {
			if e.Code != 0 {
				parts = append(parts, fmt.Sprintf("%d: %s", e.Code, e.Message))
			} else if e.Message != "" {
				parts = append(parts, e.Message)
			}
		}
		if len(parts) > 0 {
			return fmt.Errorf("cloudflare %s %s failed (status=%d): %s", method, path, status, strings.Join(parts, "; "))
		}
	}
	snippet := strings.TrimSpace(string(body))
	if len(snippet) > 256 {
		snippet = snippet[:256]
	}
	if snippet != "" {
		return fmt.Errorf("cloudflare %s %s failed (status=%d): %s", method, path, status, snippet)
	}
	return fmt.Errorf("cloudflare %s %s failed (status=%d)", method, path, status)
}
