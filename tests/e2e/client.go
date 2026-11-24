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
	"log"
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

const (
	rateLimitDefaultRetryAfter = 5 * time.Second
	rateLimitMaxWait           = 150 * time.Second
)

func parseRetryAfter(headerVal string) time.Duration {
	v := strings.TrimSpace(headerVal)
	if v == "" {
		return rateLimitDefaultRetryAfter
	}
	if sec, err := strconv.Atoi(v); err == nil {
		if sec > 0 {
			return time.Duration(sec) * time.Second
		}
		return rateLimitDefaultRetryAfter
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return rateLimitDefaultRetryAfter
}

// DoAbsolute sends an HTTP request to any full URL and returns the raw response.
// On HTTP 429 with a Retry-After header (gateway rate limiter), it waits per Retry-After
// and retries up to 150s total wait. HTTP 429 without Retry-After (business RESOURCE_EXHAUSTED
// such as sold-out flash sales) is returned immediately without sleeping.
func (c *Client) DoAbsolute(
	ctx context.Context,
	method string,
	fullURL string,
	token string,
	extraHeaders map[string]string,
	reqBody any,
) (*RawResponse, error) {
	var bodyBytes []byte
	hasBody := reqBody != nil
	if hasBody {
		switch v := reqBody.(type) {
		case []byte:
			bodyBytes = v
		case string:
			bodyBytes = []byte(v)
		default:
			encoded, err := json.Marshal(reqBody)
			if err != nil {
				return nil, fmt.Errorf("marshal request body: %w", err)
			}
			bodyBytes = encoded
		}
	}

	var totalWaited time.Duration
	for {
		var bodyReader io.Reader
		if hasBody {
			bodyReader = bytes.NewReader(bodyBytes)
		}

		req, err := http.NewRequestWithContext(ctx, method, fullURL, bodyReader)
		if err != nil {
			return nil, fmt.Errorf("create request %s %s: %w", method, fullURL, err)
		}
		if hasBody {
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

		rawBytes, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read response %s %s: %w", method, fullURL, readErr)
		}

		rawResp := &RawResponse{
			StatusCode: resp.StatusCode,
			Headers:    resp.Header.Clone(),
			Body:       rawBytes,
		}
		if resp.StatusCode != http.StatusTooManyRequests {
			return rawResp, nil
		}
		retryAfterHeader := strings.TrimSpace(resp.Header.Get("Retry-After"))
		if retryAfterHeader == "" {
			return rawResp, nil
		}

		wait := parseRetryAfter(retryAfterHeader) + 250*time.Millisecond
		if totalWaited+wait > rateLimitMaxWait {
			return rawResp, nil
		}
		totalWaited += wait
		log.Printf("[429] %s %s rate limited; waiting %s before retry (total %s/%s)",
			method, req.URL.Path, wait.Round(time.Millisecond), totalWaited.Round(time.Millisecond), rateLimitMaxWait)

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("wait retry-after %s %s: %w", method, req.URL.Path, ctx.Err())
		case <-timer.C:
		}
	}
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
