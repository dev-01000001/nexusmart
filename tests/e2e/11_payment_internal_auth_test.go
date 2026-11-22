package e2e

import (
	"net/http"
	"testing"
)

func TestE2E_PaymentInternalRoutesAuth(t *testing.T) {
	env := getEnv()
	ctx := env.ctx(t)

	adminToken := env.adminLogin(t)
	buyer := env.registerVerifyAndLogin(t, "buyer-payment-internal")

	doInternal := func(t *testing.T, method, path, token string, wantStatus int) *RawResponse {
		t.Helper()
		fullURL := env.cfg.PaymentHTTPURL + path
		resp, err := env.client.DoAbsolute(ctx, method, fullURL, token, nil, nil)
		if err != nil {
			t.Fatalf("%s %s failed: %v", method, fullURL, err)
		}
		if resp.StatusCode != wantStatus {
			t.Fatalf("%s %s returned HTTP %d (want %d): %s", method, fullURL, resp.StatusCode, wantStatus, string(resp.Body))
		}
		return resp
	}

	t.Run("LedgerSummary", func(t *testing.T) {
		doInternal(t, http.MethodGet, "/internal/ledger/summary", "", http.StatusUnauthorized)
		doInternal(t, http.MethodGet, "/internal/ledger/summary", buyer.AccessToken, http.StatusForbidden)

		resp := doInternal(t, http.MethodGet, "/internal/ledger/summary", adminToken, http.StatusOK)
		var summary LedgerSummaryResponse
		if err := DecodeJSON(resp, &summary); err != nil {
			t.Fatalf("decode internal ledger summary: %v", err)
		}
		if !summary.IsBalanced {
			t.Fatalf("expected internal ledger summary is_balanced=true, got false (debits=%d credits=%d)",
				summary.TotalDebitsVND, summary.TotalCreditsVND)
		}
	})

	t.Run("ReconciliationQueue", func(t *testing.T) {
		doInternal(t, http.MethodGet, "/internal/reconciliation/queue", "", http.StatusUnauthorized)
		doInternal(t, http.MethodGet, "/internal/reconciliation/queue", buyer.AccessToken, http.StatusForbidden)
		doInternal(t, http.MethodGet, "/internal/reconciliation/queue", adminToken, http.StatusOK)
	})

	t.Run("ReconciliationRun", func(t *testing.T) {
		doInternal(t, http.MethodPost, "/internal/reconciliation/run", "", http.StatusUnauthorized)
		doInternal(t, http.MethodPost, "/internal/reconciliation/run", buyer.AccessToken, http.StatusForbidden)
	})
}
