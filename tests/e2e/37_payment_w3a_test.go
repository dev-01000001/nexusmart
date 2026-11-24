package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func payW3aAssertSSEBadUUID(t *testing.T, env *testEnv, fullURL, token string, wantStatus int, wantCodeSubstr string) {
	t.Helper()
	resp, err := env.client.DoAbsolute(env.ctx(t), http.MethodGet, fullURL, token, map[string]string{
		"Accept": "text/event-stream",
	}, nil)
	if err != nil {
		t.Fatalf("GET %s failed: %v", fullURL, err)
	}
	if resp.StatusCode != wantStatus {
		t.Fatalf("GET %s returned HTTP %d (want %d): %s", fullURL, resp.StatusCode, wantStatus, string(resp.Body))
	}
	if wantCodeSubstr != "" {
		var body map[string]any
		if err := json.Unmarshal(resp.Body, &body); err != nil {
			t.Fatalf("GET %s expected clean JSON error body, got %q: %v", fullURL, string(resp.Body), err)
		}
		if !strings.Contains(strings.ToUpper(string(resp.Body)), wantCodeSubstr) {
			t.Fatalf("GET %s body=%s missing expected error code %q", fullURL, string(resp.Body), wantCodeSubstr)
		}
	}
}

// TestE2E_PaymentW3a_SSEInvalidUUIDReturns400 verifies Wave 3a payment item 3:
// non-UUID payment IDs on both direct payment SSE (/payments/{id}/events) and gateway SSE
// (/api/v1/payments/{id}/events, including admin bypass of gateway GetPayment pre-check)
// return HTTP 400 cleanly without hitting DB or returning 500.
// Note: concurrent duplicate webhook replay (5 concurrent + 5 sequential -> 1 ledger posting)
// is already covered by TestE2E_PaymentWebhookIdempotencyAndSecurity/Replay10Times_RecordedOnce in 05_webhook_security_test.go.
func TestE2E_PaymentW3a_SSEInvalidUUIDReturns400(t *testing.T) {
	env := getEnv()
	adminTok := env.adminLogin(t)
	buyer := env.registerVerifyAndLogin(t, "payw3a-sse-buyer")

	badIDs := []string{"not-a-uuid", "ord-sub-12345", "123456"}

	t.Run("DirectUnauthenticatedStill401", func(t *testing.T) {
		directURL := fmt.Sprintf("%s/payments/%s/events", env.cfg.PaymentHTTPURL, url.PathEscape(badIDs[0]))
		payW3aAssertSSEBadUUID(t, env, directURL, "", http.StatusUnauthorized, "")
	})

	for _, badID := range badIDs {
		escaped := url.PathEscape(badID)
		directURL := fmt.Sprintf("%s/payments/%s/events", env.cfg.PaymentHTTPURL, escaped)
		gwURL := fmt.Sprintf("%s/api/v1/payments/%s/events", env.cfg.GatewayURL, escaped)

		t.Run("DirectBuyer_"+badID, func(t *testing.T) {
			payW3aAssertSSEBadUUID(t, env, directURL, buyer.AccessToken, http.StatusBadRequest, "VALIDATION_ERROR")
		})
		t.Run("DirectAdmin_"+badID, func(t *testing.T) {
			payW3aAssertSSEBadUUID(t, env, directURL, adminTok, http.StatusBadRequest, "VALIDATION_ERROR")
		})
		t.Run("GatewayBuyer_"+badID, func(t *testing.T) {
			payW3aAssertSSEBadUUID(t, env, gwURL, buyer.AccessToken, http.StatusBadRequest, "")
		})
		t.Run("GatewayAdmin_"+badID, func(t *testing.T) {
			payW3aAssertSSEBadUUID(t, env, gwURL, adminTok, http.StatusBadRequest, "VALIDATION_ERROR")
		})
	}
}
