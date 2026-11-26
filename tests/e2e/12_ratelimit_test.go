package e2e

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestE2E_RateLimit_Real429AndNoXFFBypass proves that under the default stack rate limits
// (RATE_LIMIT_FLASH_SALE_PER_MIN=60, RATE_LIMIT_IP_PER_MIN=300), genuine HTTP 429 blocking
// still enforces limits AND spoofed X-Forwarded-For / X-Real-IP headers from an untrusted IP
// cannot bypass the 429 block. Cleans up its own rl:* keys afterwards so subsequent runs stay clean.
func TestE2E_RateLimit_Real429AndNoXFFBypass(t *testing.T) {
	env := getEnv()
	ctx := env.ctx(t)

	resetKeys := func(label string) {
		if env.cfg.RedisAddr == "" {
			return
		}
		rCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := ResetRateLimitKeys(rCtx, env.cfg.RedisAddr, env.cfg.RedisPassword); err != nil {
			t.Fatalf("reset rl:* keys (%s) on %s: %v", label, env.cfg.RedisAddr, err)
		}
	}

	// Start from a clean rate-limit window and guarantee cleanup afterwards so no later test or
	// chained command (`make seed`, `make e2e-web`) is poisoned.
	resetKeys("before 429 test")
	t.Cleanup(func() {
		resetKeys("after 429 test")
	})

	// Hit GET /api/v1/flash-sales (default RATE_LIMIT_FLASH_SALE_PER_MIN=60) without any XFF header.
	const maxProbes = 75
	allowedCount := 0
	var blockedResp *RawResponse

	for i := 1; i <= maxProbes; i++ {
		resp, err := env.client.DoJSONNoRetry(ctx, http.MethodGet, "/api/v1/flash-sales", "", nil, nil)
		if err != nil {
			t.Fatalf("probe #%d GET /api/v1/flash-sales failed: %v", i, err)
		}
		if resp.StatusCode == http.StatusOK {
			allowedCount++
			continue
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			blockedResp = resp
			break
		}
		t.Fatalf("probe #%d returned unexpected HTTP %d (want 200 or 429): %s", i, resp.StatusCode, string(resp.Body))
	}

	if blockedResp == nil {
		t.Fatalf("expected genuine HTTP 429 within %d requests to /api/v1/flash-sales, but all %d succeeded", maxProbes, allowedCount)
	}
	if allowedCount < 1 {
		t.Fatalf("expected at least 1 allowed request before 429, got %d", allowedCount)
	}

	limitHdr := strings.TrimSpace(blockedResp.Headers.Get("X-RateLimit-Limit"))
	remainingHdr := strings.TrimSpace(blockedResp.Headers.Get("X-RateLimit-Remaining"))
	retryAfterHdr := strings.TrimSpace(blockedResp.Headers.Get("Retry-After"))
	t.Logf("Genuine 429 observed after %d allowed requests: X-RateLimit-Limit=%q X-RateLimit-Remaining=%q Retry-After=%q body=%s",
		allowedCount, limitHdr, remainingHdr, retryAfterHdr, string(blockedResp.Body))

	if remainingHdr != "0" {
		t.Fatalf("expected X-RateLimit-Remaining=0 on 429 response, got %q", remainingHdr)
	}
	retrySec, err := strconv.Atoi(retryAfterHdr)
	if err != nil || retrySec < 1 {
		t.Fatalf("expected Retry-After >= 1 on 429 response, got %q", retryAfterHdr)
	}
	if !strings.Contains(string(blockedResp.Body), "RATE_LIMITED") {
		t.Fatalf("expected RATE_LIMITED error code in 429 body, got: %s", string(blockedResp.Body))
	}

	// Prove B2: sending spoofed X-Forwarded-For / X-Real-IP headers from an untrusted IP
	// (e2e container is outside gateway's TRUSTED_PROXY_CIDRS=172.31.250.0/28) MUST NOT bypass 429!
	spoofCases := []struct {
		name    string
		headers map[string]string
	}{
		{
			name:    "spoofed X-Forwarded-For single IP",
			headers: map[string]string{"X-Forwarded-For": "203.0.113.42"},
		},
		{
			name:    "spoofed X-Forwarded-For multi-hop chain",
			headers: map[string]string{"X-Forwarded-For": "198.51.100.7, 203.0.113.43"},
		},
		{
			name:    "spoofed X-Real-IP",
			headers: map[string]string{"X-Real-IP": "203.0.113.44"},
		},
		{
			name: "spoofed X-Forwarded-For + X-Real-IP",
			headers: map[string]string{
				"X-Forwarded-For": "198.51.100.99",
				"X-Real-IP":       "203.0.113.99",
			},
		},
	}

	for _, sc := range spoofCases {
		resp, err := env.client.DoJSONNoRetry(ctx, http.MethodGet, "/api/v1/flash-sales", "", sc.headers, nil)
		if err != nil {
			t.Fatalf("spoof probe (%s) failed: %v", sc.name, err)
		}
		if resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("B2 XFF BYPASS FAILURE (%s): expected HTTP 429, got HTTP %d (body=%s)",
				sc.name, resp.StatusCode, string(resp.Body))
		}
	}
}
