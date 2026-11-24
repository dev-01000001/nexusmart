package middleware

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/gateway/internal/httpx"
)

func TestW3BRateLimitPhoneOTP(t *testing.T) {
	authMgr, err := auth.NewManager("gw-phone-otp-rl-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	buyerATok, _, _, _ := authMgr.SignAccessToken("buyer-a", "buyer", "")
	buyerBTok, _, _, _ := authMgr.SignAccessToken("buyer-b", "buyer", "")

	limiter := newRecordingRateLimiter()
	var logBuf syncBuffer
	log := logger.New("gw-phone-otp-rl-test", logger.WithWriter(&logBuf))

	okHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	phoneRL := Auth(authMgr, nil, log, true)(RateLimitPhoneOTP(limiter, 5, log)(okHandler))
	bankRL := Auth(authMgr, nil, log, true)(RateLimitBankOTP(limiter, 5, log)(okHandler))
	userRL := Auth(authMgr, nil, log, true)(RateLimitByUser(limiter, 20, log)(okHandler))

	withToken := func(tok string) *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/users/me/phone/otp", strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+tok)
		return req
	}

	// First 5 calls for buyer-a succeed.
	for i := 1; i <= 5; i++ {
		rec := httptest.NewRecorder()
		phoneRL.ServeHTTP(rec, withToken(buyerATok))
		if rec.Code != http.StatusOK {
			t.Fatalf("call %d for buyer-a: want 200, got %d", i, rec.Code)
		}
	}

	// 6th call -> 429 RATE_LIMITED with Retry-After.
	rec6 := httptest.NewRecorder()
	phoneRL.ServeHTTP(rec6, withToken(buyerATok))
	if rec6.Code != http.StatusTooManyRequests {
		t.Fatalf("call 6 for buyer-a: want 429, got %d (%s)", rec6.Code, rec6.Body.String())
	}
	if sec, err := strconv.Atoi(rec6.Header().Get("Retry-After")); err != nil || sec < 1 {
		t.Fatalf("Retry-After = %q, want integer >= 1", rec6.Header().Get("Retry-After"))
	}
	var errEnv httpx.ErrorEnvelope
	if err := json.Unmarshal(rec6.Body.Bytes(), &errEnv); err != nil || errEnv.Error.Code != "RATE_LIMITED" {
		t.Fatalf("want RATE_LIMITED envelope, got %s", rec6.Body.String())
	}

	// Dedicated key namespace disjoint from bank OTP and general user buckets.
	foundPhoneKey := false
	for _, k := range limiter.CheckedKeys() {
		if k == "rl:phone_otp:user:buyer-a" {
			foundPhoneKey = true
		}
	}
	if !foundPhoneKey {
		t.Fatalf("expected key rl:phone_otp:user:buyer-a, got %v", limiter.CheckedKeys())
	}

	// General user & bank OTP buckets for buyer-a remain unaffected.
	recUser := httptest.NewRecorder()
	userRL.ServeHTTP(recUser, withToken(buyerATok))
	if recUser.Code != http.StatusOK {
		t.Fatalf("general user bucket should stay 200, got %d", recUser.Code)
	}
	recBank := httptest.NewRecorder()
	bankRL.ServeHTTP(recBank, withToken(buyerATok))
	if recBank.Code != http.StatusOK {
		t.Fatalf("bank OTP bucket should stay 200, got %d", recBank.Code)
	}

	// Buyer B is isolated from buyer A.
	recB := httptest.NewRecorder()
	phoneRL.ServeHTTP(recB, withToken(buyerBTok))
	if recB.Code != http.StatusOK {
		t.Fatalf("buyer-b should stay 200, got %d", recB.Code)
	}

	// Redis error fails open.
	limiter.SetError(errors.New("redis down"))
	recFail := httptest.NewRecorder()
	phoneRL.ServeHTTP(recFail, withToken(buyerATok))
	limiter.SetError(nil)
	if recFail.Code != http.StatusOK {
		t.Fatalf("redis error should fail open (200), got %d", recFail.Code)
	}
}

func TestW3BRateLimitShippingFee_E17Bis(t *testing.T) {
	authMgr, err := auth.NewManager("gw-ship-fee-rl-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	buyer1Tok, _, _, _ := authMgr.SignAccessToken("buyer-1", "buyer", "")
	buyer2Tok, _, _, _ := authMgr.SignAccessToken("buyer-2", "buyer", "")

	limiter := newRecordingRateLimiter()
	log := logger.New("gw-ship-fee-rl-test", logger.WithWriter(&syncBuffer{}))
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	feeChain := Auth(authMgr, nil, log, false)(RateLimitShippingFee(limiter, 2, log, nil)(okHandler))

	doReq := func(ip, tok string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/shipping/calculate-fee", strings.NewReader(`{}`))
		req.RemoteAddr = ip + ":12345"
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		rec := httptest.NewRecorder()
		feeChain.ServeHTTP(rec, req)
		return rec
	}

	// 1. Anonymous caller keyed by IP: 2 allowed, 3rd -> 429.
	for i := 1; i <= 3; i++ {
		rec := doReq("198.51.100.10", "")
		want := http.StatusOK
		if i == 3 {
			want = http.StatusTooManyRequests
		}
		if rec.Code != want {
			t.Fatalf("anon call %d: want %d, got %d", i, want, rec.Code)
		}
	}

	// 2. E17-bis: Authenticated buyer-1 behind the SAME exhausted NAT IP gets their own per-user bucket!
	for i := 1; i <= 3; i++ {
		rec := doReq("198.51.100.10", buyer1Tok)
		want := http.StatusOK
		if i == 3 {
			want = http.StatusTooManyRequests
		}
		if rec.Code != want {
			t.Fatalf("buyer-1 call %d on shared IP: want %d, got %d", i, want, rec.Code)
		}
	}

	// 3. E17-bis: Authenticated buyer-2 behind the SAME NAT IP is NOT blocked by buyer-1 or anon!
	recB2 := doReq("198.51.100.10", buyer2Tok)
	if recB2.Code != http.StatusOK {
		t.Fatalf("buyer-2 on shared IP should stay 200, got %d", recB2.Code)
	}

	// Verify exact key namespaces.
	hasKey := func(target string) bool {
		for _, k := range limiter.CheckedKeys() {
			if k == target {
				return true
			}
		}
		return false
	}
	for _, wantKey := range []string{
		"rl:shipping_fee:ip:198.51.100.10",
		"rl:shipping_fee:user:buyer-1",
		"rl:shipping_fee:user:buyer-2",
	} {
		if !hasKey(wantKey) {
			t.Fatalf("missing expected shipping-fee rate-limit key %q in %v", wantKey, limiter.CheckedKeys())
		}
	}
}
