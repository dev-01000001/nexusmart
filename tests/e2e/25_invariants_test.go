package e2e

import (
	"net/http"
	"strings"
	"testing"
)

type invCarriersResponse struct {
	Carriers []struct {
		CarrierCode string `json:"carrier_code"`
		Enabled     bool   `json:"enabled"`
	} `json:"carriers"`
}

// TestE2E_FinalInvariants runs at the end of the suite using only public/admin REST APIs
// (no direct DB access) to verify accounting and operational invariants remain intact.
func TestE2E_FinalInvariants(t *testing.T) {
	env := getEnv()
	adminTok := env.adminLogin(t)

	t.Run("LedgerBalancedAndNonNegativeBalances", func(t *testing.T) {
		summary := env.getLedgerSummary(t)
		if !summary.IsBalanced {
			t.Fatalf("ledger summary is not balanced: debits=%d credits=%d", summary.TotalDebitsVND, summary.TotalCreditsVND)
		}
		if summary.TotalDebitsVND != summary.TotalCreditsVND {
			t.Fatalf("ledger total debits (%d) != total credits (%d)", summary.TotalDebitsVND, summary.TotalCreditsVND)
		}
		if summary.TotalDebitsVND < 0 {
			t.Fatalf("ledger total debits must be non-negative, got %d", summary.TotalDebitsVND)
		}

		var sumDebits, sumCredits int64
		for _, acc := range summary.Accounts {
			if acc.DebitTotalVND < 0 || acc.CreditTotalVND < 0 {
				t.Fatalf("account %s has negative totals: debit=%d credit=%d", acc.AccountCode, acc.DebitTotalVND, acc.CreditTotalVND)
			}
			sumDebits += acc.DebitTotalVND
			sumCredits += acc.CreditTotalVND

			switch {
			case acc.AccountCode == "escrow_liability":
				if acc.BalanceVND < 0 {
					t.Fatalf("escrow_liability balance must be >= 0, got %d (debits=%d credits=%d)", acc.BalanceVND, acc.DebitTotalVND, acc.CreditTotalVND)
				}
			case acc.AccountCode == "refund_payable":
				if acc.BalanceVND < 0 {
					t.Fatalf("refund_payable balance must be >= 0, got %d (debits=%d credits=%d)", acc.BalanceVND, acc.DebitTotalVND, acc.CreditTotalVND)
				}
			case strings.HasPrefix(acc.AccountCode, "seller_pending:") || strings.HasPrefix(acc.AccountCode, "seller_available:"):
				if acc.BalanceVND < 0 {
					t.Fatalf("shop account %s balance must be >= 0, got %d (debits=%d credits=%d)", acc.AccountCode, acc.BalanceVND, acc.DebitTotalVND, acc.CreditTotalVND)
				}
			}
		}

		if sumDebits != summary.TotalDebitsVND || sumCredits != summary.TotalCreditsVND {
			t.Fatalf("sum of account totals (debits=%d credits=%d) != summary totals (debits=%d credits=%d)",
				sumDebits, sumCredits, summary.TotalDebitsVND, summary.TotalCreditsVND)
		}
	})

	t.Run("PlatformCarriersHealthyEnabled", func(t *testing.T) {
		resp := env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/carriers", adminTok, nil, nil, http.StatusOK)
		var out invCarriersResponse
		if err := DecodeJSON(resp, &out); err != nil {
			t.Fatalf("decode admin carriers: %v", err)
		}
		enabledByCode := make(map[string]bool, len(out.Carriers))
		for _, c := range out.Carriers {
			enabledByCode[strings.ToLower(c.CarrierCode)] = c.Enabled
		}
		for _, code := range []string{"ghn", "mock_express"} {
			if !enabledByCode[code] {
				t.Fatalf("carrier %q expected enabled=true at end of suite, got carriers=%+v", code, out.Carriers)
			}
		}
	})
}
