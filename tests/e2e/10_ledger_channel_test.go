package e2e

import (
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestE2E_LedgerAndReconciliation_ChannelFilter(t *testing.T) {
	env := getEnv()
	adminTok := env.adminLogin(t)

	// 1. Ledger summary: unfiltered, MARKETPLACE, and STOREFRONT must all be balanced
	//    and MARKETPLACE + STOREFRONT must equal unfiltered total 100% exactly.
	t.Run("LedgerSummary_ByChannel_BalancedAndExactSum", func(t *testing.T) {
		total := env.getLedgerSummaryByChannel(t, "")
		mkt := env.getLedgerSummaryByChannel(t, "MARKETPLACE")
		sf := env.getLedgerSummaryByChannel(t, "STOREFRONT")

		t.Logf("Ledger totals: ALL=(debits=%d credits=%d balanced=%v) MARKETPLACE=(debits=%d credits=%d balanced=%v) STOREFRONT=(debits=%d credits=%d balanced=%v)",
			total.TotalDebitsVND, total.TotalCreditsVND, total.IsBalanced,
			mkt.TotalDebitsVND, mkt.TotalCreditsVND, mkt.IsBalanced,
			sf.TotalDebitsVND, sf.TotalCreditsVND, sf.IsBalanced)

		for _, entry := range []struct {
			name string
			s    LedgerSummaryResponse
		}{
			{"ALL", total},
			{"MARKETPLACE", mkt},
			{"STOREFRONT", sf},
		} {
			if !entry.s.IsBalanced || entry.s.TotalDebitsVND != entry.s.TotalCreditsVND {
				t.Fatalf("ledger [%s] imbalanced: debits=%d credits=%d is_balanced=%v",
					entry.name, entry.s.TotalDebitsVND, entry.s.TotalCreditsVND, entry.s.IsBalanced)
			}
		}

		if mkt.TotalDebitsVND <= 0 {
			t.Fatalf("expected MARKETPLACE ledger debits > 0, got %d", mkt.TotalDebitsVND)
		}
		if sf.TotalDebitsVND <= 0 {
			t.Fatalf("expected STOREFRONT ledger debits > 0, got %d", sf.TotalDebitsVND)
		}
		if mkt.TotalDebitsVND+sf.TotalDebitsVND != total.TotalDebitsVND {
			t.Fatalf("LEDGER CHANNEL SUM MISMATCH (debits): MARKETPLACE(%d) + STOREFRONT(%d) = %d != ALL(%d)",
				mkt.TotalDebitsVND, sf.TotalDebitsVND, mkt.TotalDebitsVND+sf.TotalDebitsVND, total.TotalDebitsVND)
		}
		if mkt.TotalCreditsVND+sf.TotalCreditsVND != total.TotalCreditsVND {
			t.Fatalf("LEDGER CHANNEL SUM MISMATCH (credits): MARKETPLACE(%d) + STOREFRONT(%d) = %d != ALL(%d)",
				mkt.TotalCreditsVND, sf.TotalCreditsVND, mkt.TotalCreditsVND+sf.TotalCreditsVND, total.TotalCreditsVND)
		}

		// Invalid sales_channel filter must be rejected with 400.
		env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/ledger/summary?sales_channel=INVALID_CHANNEL", adminTok, nil, nil, http.StatusBadRequest)
	})

	// 2. Reconciliation report filtered by channel: MARKETPLACE + STOREFRONT == ALL 100%.
	t.Run("Reconciliation_ByChannel_ExactSum", func(t *testing.T) {
		vnLoc := time.FixedZone("Asia/Ho_Chi_Minh", 7*3600)
		today := time.Now().In(vnLoc).Format("2006-01-02")
		total := env.runReconciliation(t, today, "")
		mkt := env.runReconciliation(t, today, "MARKETPLACE")
		sf := env.runReconciliation(t, today, "STOREFRONT")

		t.Logf("Reconciliation (%s): ALL=(items=%d matched=%d) MARKETPLACE=(items=%d matched=%d) STOREFRONT=(items=%d matched=%d)",
			today, len(total.Items), total.MatchedCount,
			len(mkt.Items), mkt.MatchedCount,
			len(sf.Items), sf.MatchedCount)

		if len(mkt.Items) == 0 {
			t.Fatalf("expected >= 1 MARKETPLACE reconciliation item for %s", today)
		}
		if len(sf.Items) == 0 {
			t.Fatalf("expected >= 1 STOREFRONT reconciliation item for %s", today)
		}
		var totalLocal, mktLocal, sfLocal int64
		var localItems int
		var localMatched, localMissingLocal int32
		for _, it := range total.Items {
			if it.PaymentID == "" {
				if it.LocalAmountVND != 0 {
					t.Fatalf("provider-only reconciliation item has non-zero local_amount_vnd: %+v", it)
				}
				continue
			}
			localItems++
			if StatusEquals(it.Status, "MATCHED") {
				localMatched++
			} else if StatusEquals(it.Status, "MISSING_LOCAL") {
				localMissingLocal++
			}
			totalLocal += it.LocalAmountVND
		}
		if len(mkt.Items)+len(sf.Items) != localItems {
			t.Fatalf("RECONCILIATION ITEM COUNT MISMATCH: MARKETPLACE(%d) + STOREFRONT(%d) != ALL_LOCAL(%d) (all=%d)",
				len(mkt.Items), len(sf.Items), localItems, len(total.Items))
		}
		if mkt.MatchedCount+sf.MatchedCount != localMatched {
			t.Fatalf("RECONCILIATION MATCHED COUNT MISMATCH: MARKETPLACE(%d) + STOREFRONT(%d) != ALL_LOCAL(%d) (all=%d)",
				mkt.MatchedCount, sf.MatchedCount, localMatched, total.MatchedCount)
		}
		if mkt.MissingLocalCount+sf.MissingLocalCount != localMissingLocal ||
			mkt.MissingProviderCount+sf.MissingProviderCount != total.MissingProviderCount ||
			mkt.AmountMismatchCount+sf.AmountMismatchCount != total.AmountMismatchCount {
			t.Fatalf("RECONCILIATION ANOMALY COUNT MISMATCH: mkt=%+v sf=%+v total=%+v", mkt, sf, total)
		}
		for _, it := range mkt.Items {
			if it.SalesChannel != "MARKETPLACE" {
				t.Fatalf("expected MARKETPLACE item in MARKETPLACE reconcile, got %+v", it)
			}
			mktLocal += it.LocalAmountVND
		}
		for _, it := range sf.Items {
			if it.SalesChannel != "STOREFRONT" || it.StorefrontShopID == "" {
				t.Fatalf("expected STOREFRONT item with storefront_shop_id in STOREFRONT reconcile, got %+v", it)
			}
			sfLocal += it.LocalAmountVND
		}
		if mktLocal+sfLocal != totalLocal {
			t.Fatalf("RECONCILIATION LOCAL AMOUNT SUM MISMATCH: MARKETPLACE(%d) + STOREFRONT(%d) = %d != ALL(%d)",
				mktLocal, sfLocal, mktLocal+sfLocal, totalLocal)
		}

		// Invalid sales_channel filter must be rejected with 400.
		env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/payments/reconcile", adminTok, nil, ReconciliationRequest{
			Date:         today,
			SalesChannel: "INVALID_CHANNEL",
		}, http.StatusBadRequest)
	})
}

func (e *testEnv) getLedgerSummaryByChannel(t *testing.T, channel string) LedgerSummaryResponse {
	t.Helper()
	adminTok := e.adminLogin(t)
	path := "/api/v1/admin/ledger/summary"
	if channel != "" {
		path += "?sales_channel=" + url.QueryEscape(channel)
	}
	resp := e.mustDoJSON(t, http.MethodGet, path, adminTok, nil, nil, http.StatusOK)
	var out LedgerSummaryResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode ledger summary (channel=%q): %v", channel, err)
	}
	return out
}

func (e *testEnv) runReconciliation(t *testing.T, date, channel string) ReconciliationResponse {
	t.Helper()
	adminTok := e.adminLogin(t)
	resp := e.mustDoJSON(t, http.MethodPost, "/api/v1/admin/payments/reconcile", adminTok, nil, ReconciliationRequest{
		Date:         date,
		SalesChannel: channel,
	}, http.StatusOK)
	var out ReconciliationResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode reconciliation (channel=%q): %v", channel, err)
	}
	return out
}
