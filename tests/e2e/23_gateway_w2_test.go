package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

type gwW2ErrorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		TraceID string `json:"trace_id"`
	} `json:"error"`
}

func gwW2RawPost(t *testing.T, baseURL, path, token string, headers map[string]string, rawBody []byte) (int, http.Header, []byte) {
	t.Helper()
	cli := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequestWithContext(getEnv().ctx(t), http.MethodPost, baseURL+path, bytes.NewReader(rawBody))
	if err != nil {
		t.Fatalf("create raw POST %s: %v", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := cli.Do(req)
	if err != nil {
		t.Fatalf("execute raw POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body %s: %v", path, err)
	}
	return resp.StatusCode, resp.Header.Clone(), body
}

func TestE2E_GatewayW2SecurityAndRoutes(t *testing.T) {
	env := getEnv()
	adminTok := env.adminLogin(t)

	// 1. PLT-B6: Security headers & oversized request body -> 413 PAYLOAD_TOO_LARGE.
	t.Run("SecurityHeadersAndOversizedBody413", func(t *testing.T) {
		resp := env.mustDoJSON(t, http.MethodGet, "/api/v1/categories", "", nil, nil, http.StatusOK)
		if got := resp.Headers.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
		}
		if got := resp.Headers.Get("X-Frame-Options"); got != "DENY" {
			t.Fatalf("X-Frame-Options = %q, want DENY", got)
		}
		if got := resp.Headers.Get("Referrer-Policy"); got == "" {
			t.Fatalf("missing Referrer-Policy header")
		}
		if got := resp.Headers.Get("Content-Security-Policy"); !strings.Contains(got, "default-src 'none'") {
			t.Fatalf("Content-Security-Policy = %q, want minimal API CSP", got)
		}

		oversized := []byte(`{"email":"a@b.c","password":"` + strings.Repeat("x", (2<<20)+1024) + `"}`)
		for _, path := range []string{"/api/v1/auth/login", "/webhooks/payment"} {
			code, _, body := gwW2RawPost(t, env.cfg.GatewayURL, path, "", nil, oversized)
			if code != http.StatusRequestEntityTooLarge {
				t.Fatalf("POST %s >2MiB: want 413, got %d (body=%s)", path, code, string(body))
			}
			var envErr gwW2ErrorEnvelope
			if err := json.Unmarshal(body, &envErr); err != nil || envErr.Error.Code != "PAYLOAD_TOO_LARGE" {
				t.Fatalf("POST %s >2MiB: want PAYLOAD_TOO_LARGE envelope, got %s", path, string(body))
			}
		}
	})

	// 2. Admin money-mutating endpoints require Idempotency-Key -> 400 MISSING_IDEMPOTENCY_KEY.
	t.Run("AdminMoneyRoutesRequireIdempotencyKey", func(t *testing.T) {
		const dummyID = "00000000-0000-0000-0000-000000000001"
		cases := []struct {
			name string
			path string
			body map[string]any
		}{
			{
				name: "AdminRefundPayment",
				path: "/api/v1/admin/payments/" + dummyID + "/refund",
				body: map[string]any{"order_id": dummyID, "amount_vnd": 1000, "reason": "test"},
			},
			{
				name: "AdminSettleRefund",
				path: "/api/v1/admin/refunds/" + dummyID + "/settle",
				body: map[string]any{"provider_refund_id": "TX-NO-IDEM", "note": "test"},
			},
			{
				name: "AdminResolveReconciliationItem",
				path: "/api/v1/admin/reconciliation/queue/" + dummyID + "/resolve",
				body: map[string]any{"action": "SUSPEND", "note": "test"},
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				resp := env.mustDoJSON(t, http.MethodPost, tc.path, adminTok, nil, tc.body, http.StatusBadRequest)
				var envErr gwW2ErrorEnvelope
				if err := DecodeJSON(resp, &envErr); err != nil {
					t.Fatalf("decode error envelope: %v", err)
				}
				if envErr.Error.Code != "MISSING_IDEMPOTENCY_KEY" {
					t.Fatalf("POST %s without Idempotency-Key: want MISSING_IDEMPOTENCY_KEY, got %q (%s)", tc.path, envErr.Error.Code, string(resp.Body))
				}
			})
		}
	})

	// 3. RBAC on new Wave 2a routes: unauthenticated -> 401, buyer on seller/admin routes -> 403.
	t.Run("NewRoutesRBAC", func(t *testing.T) {
		env.mustDoJSON(t, http.MethodGet, "/api/v1/returns", "", nil, nil, http.StatusUnauthorized)

		buyer := env.registerVerifyAndLogin(t, "gw-w2-rbac")
		for _, path := range []string{
			"/api/v1/seller/returns",
			"/api/v1/seller/shipping-settings",
			"/api/v1/admin/returns",
			"/api/v1/admin/refunds",
			"/api/v1/admin/reconciliation/queue",
			"/api/v1/admin/carriers",
		} {
			env.mustDoJSON(t, http.MethodGet, path, buyer.AccessToken, nil, nil, http.StatusForbidden)
		}
	})

	// 4. USR-B1: Locking an active user immediately revokes their existing access token (401 on auth & optional-auth routes).
	t.Run("LockedUserOldTokenImmediately401", func(t *testing.T) {
		victim := env.registerVerifyAndLogin(t, "gw-w2-lock")

		// Confirm old token works before lock.
		env.mustDoJSON(t, http.MethodGet, "/api/v1/users/me", victim.AccessToken, nil, nil, http.StatusOK)

		// Admin locks user.
		lockPath := fmt.Sprintf("/api/v1/admin/users/%s/status", url.PathEscape(victim.UserID))
		env.mustDoJSON(t, http.MethodPatch, lockPath, adminTok, nil, map[string]any{
			"status": "LOCKED",
			"reason": "e2e w2a gateway revocation test",
		}, http.StatusOK)

		// Old token must immediately be rejected with 401 on protected and optional-auth routes.
		for _, path := range []string{"/api/v1/users/me", "/api/v1/vouchers"} {
			resp := env.mustDoJSON(t, http.MethodGet, path, victim.AccessToken, nil, nil, http.StatusUnauthorized)
			var envErr gwW2ErrorEnvelope
			if err := DecodeJSON(resp, &envErr); err != nil || envErr.Error.Code != "UNAUTHENTICATED" {
				t.Fatalf("GET %s with locked user token: want UNAUTHENTICATED, got %s", path, string(resp.Body))
			}
		}
	})

	// 5. Tight IP rate limit on POST /api/v1/auth/forgot-password -> 429 RATE_LIMITED (run last).
	// Use a distinct email per request so we exercise gateway's per-IP limit rather than user's per-email limit.
	t.Run("ForgotPasswordRateLimited", func(t *testing.T) {
		hit429 := false
		var retryAfter time.Duration
		var seenStatuses []int
		ipRetries := 0
		for i := 1; i <= 15; i++ {
			payload := []byte(fmt.Sprintf(`{"email":"gw-w2-rl-%s-%d@nexusmart.test"}`, env.runID, i))
			code, hdr, body := gwW2RawPost(t, env.cfg.GatewayURL, "/api/v1/auth/forgot-password", "", nil, payload)
			if code == http.StatusTooManyRequests {
				if lim, err := strconv.Atoi(strings.TrimSpace(hdr.Get("X-RateLimit-Limit"))); err == nil && lim > 60 && ipRetries < 3 {
					// General IP rate limiter fired before auth rate limiter; wait it out and retry the auth probe.
					wait := parseRetryAfter(hdr.Get("Retry-After")) + 250*time.Millisecond
					t.Logf("general IP limiter (limit=%d) fired during forgot-password probe; waiting %s", lim, wait)
					time.Sleep(wait)
					ipRetries++
					i--
					continue
				}
				seenStatuses = append(seenStatuses, code)
				if hdr.Get("Retry-After") == "" {
					t.Fatalf("429 on forgot-password missing Retry-After header (body=%s)", string(body))
				}
				var envErr gwW2ErrorEnvelope
				if err := json.Unmarshal(body, &envErr); err != nil || envErr.Error.Code != "RATE_LIMITED" {
					t.Fatalf("429 on forgot-password want RATE_LIMITED envelope, got %s", string(body))
				}
				retryAfter = parseRetryAfter(hdr.Get("Retry-After"))
				hit429 = true
				break
			}
			seenStatuses = append(seenStatuses, code)
		}
		if !hit429 {
			t.Fatalf("expected 429 RATE_LIMITED within 15 calls to /api/v1/auth/forgot-password, got statuses=%v", seenStatuses)
		}
		// Isolate rl:auth:ip window so subsequent tests (e.g. 24_notification_w2_test.go) start with open auth quota.
		t.Cleanup(func() {
			wait := retryAfter + time.Second
			t.Logf("waiting %s for auth rate-limit window to reopen before next test", wait)
			time.Sleep(wait)
		})
	})
}
