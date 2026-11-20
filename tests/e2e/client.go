package e2e

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Client is a typed HTTP client for NexusMart Gateway REST endpoints and webhooks.
type Client struct {
	cfg  Config
	http *http.Client
}

// RawResponse captures raw HTTP status, headers, and body for precise error reporting.
type RawResponse struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
}

// NewClient creates a new Gateway HTTP client.
func NewClient(cfg Config) *Client {
	return &Client{
		cfg: cfg,
		http: &http.Client{
			Timeout: cfg.HTTPTimeout,
		},
	}
}

// MintAccessToken signs a short-lived HS256 user access token (same shape as pkg/common/auth).
func MintAccessToken(secret, userID, role string) string {
	enc := base64.RawURLEncoding
	now := time.Now().Unix()
	claims, _ := json.Marshal(map[string]any{
		"sub": userID, "role": role, "iss": "nexusmart", "jti": uuid.NewString(), "iat": now, "exp": now + 300,
	})
	unsigned := enc.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." + enc.EncodeToString(claims)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(unsigned))
	return unsigned + "." + enc.EncodeToString(mac.Sum(nil))
}

// ComputeHMACSHA256 computes lowercase hex HMAC-SHA256(secret, payload).
func ComputeHMACSHA256(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

// DoJSON sends a JSON HTTP request to Gateway and returns the raw response.
func (c *Client) DoJSON(
	ctx context.Context,
	method string,
	path string,
	token string,
	extraHeaders map[string]string,
	reqBody any,
) (*RawResponse, error) {
	return c.DoAbsolute(ctx, method, c.cfg.GatewayURL+path, token, extraHeaders, reqBody)
}

// DoAbsolute sends an HTTP request to any full URL and returns the raw response.
func (c *Client) DoAbsolute(
	ctx context.Context,
	method string,
	fullURL string,
	token string,
	extraHeaders map[string]string,
	reqBody any,
) (*RawResponse, error) {
	var bodyReader io.Reader
	if reqBody != nil {
		switch v := reqBody.(type) {
		case []byte:
			bodyReader = bytes.NewReader(v)
		case string:
			bodyReader = strings.NewReader(v)
		default:
			encoded, err := json.Marshal(reqBody)
			if err != nil {
				return nil, fmt.Errorf("marshal request body: %w", err)
			}
			bodyReader = bytes.NewReader(encoded)
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request %s %s: %w", method, fullURL, err)
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute %s %s: %w", method, fullURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	rawBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response %s %s: %w", method, fullURL, err)
	}

	return &RawResponse{
		StatusCode: resp.StatusCode,
		Headers:    resp.Header.Clone(),
		Body:       rawBytes,
	}, nil
}

// DecodeJSON unmarshals a RawResponse body into dst with rich error context.
func DecodeJSON(resp *RawResponse, dst any) error {
	if dst == nil {
		return nil
	}
	if err := json.Unmarshal(resp.Body, dst); err != nil {
		return fmt.Errorf("decode JSON (HTTP %d): %w; body=%s", resp.StatusCode, err, string(resp.Body))
	}
	return nil
}

// PostSignedWebhook sends a raw JSON webhook with X-Signature (HMAC-SHA256) and X-Timestamp headers.
func (c *Client) PostSignedWebhook(
	ctx context.Context,
	path string,
	payload any,
	secret string,
	timestamp time.Time,
	overrideSig string,
) (*RawResponse, error) {
	rawBody, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal webhook payload: %w", err)
	}

	sig := overrideSig
	if sig == "" && secret != "" {
		sig = ComputeHMACSHA256(secret, rawBody)
	}

	headers := map[string]string{}
	if sig != "" {
		headers["X-Signature"] = sig
	}
	if !timestamp.IsZero() {
		headers["X-Timestamp"] = strconv.FormatInt(timestamp.Unix(), 10)
	}

	return c.DoJSON(ctx, http.MethodPost, path, "", headers, rawBody)
}
