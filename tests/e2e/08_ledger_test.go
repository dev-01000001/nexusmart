package e2e

import "testing"

func TestE2E_LedgerAlwaysBalanced(t *testing.T) {
	env := getEnv()
	summary := env.getLedgerSummary(t)

	t.Logf("Double-Entry Ledger Summary: total_debits_vnd=%d total_credits_vnd=%d is_balanced=%v accounts=%d",
		summary.TotalDebitsVND, summary.TotalCreditsVND, summary.IsBalanced, len(summary.Accounts))

	for _, acc := range summary.Accounts {
		t.Logf("  account=%-42s type=%-10s debit=%10d credit=%10d balance=%10d",
			acc.AccountCode, acc.AccountType, acc.DebitTotalVND, acc.CreditTotalVND, acc.BalanceVND)
	}

	if !summary.IsBalanced {
		t.Fatalf("LEDGER IMBALANCE: is_balanced=false (debits=%d credits=%d)",
			summary.TotalDebitsVND, summary.TotalCreditsVND)
	}
	if summary.TotalDebitsVND != summary.TotalCreditsVND {
		t.Fatalf("LEDGER IMBALANCE: total_debits_vnd (%d) != total_credits_vnd (%d)",
			summary.TotalDebitsVND, summary.TotalCreditsVND)
	}
	if summary.TotalDebitsVND <= 0 {
		t.Fatalf("expected total_debits_vnd > 0 after COD, VietQR, refund, and completed order scenarios, got %d",
			summary.TotalDebitsVND)
	}
	if len(summary.Accounts) == 0 {
		t.Fatalf("expected non-empty ledger accounts breakdown")
	}

	var sumAccountDebits, sumAccountCredits int64
	for _, acc := range summary.Accounts {
		sumAccountDebits += acc.DebitTotalVND
		sumAccountCredits += acc.CreditTotalVND
	}
	if sumAccountDebits != summary.TotalDebitsVND || sumAccountCredits != summary.TotalCreditsVND {
		t.Fatalf("account totals mismatch summary: sum_debits=%d (summary=%d) sum_credits=%d (summary=%d)",
			sumAccountDebits, summary.TotalDebitsVND, sumAccountCredits, summary.TotalCreditsVND)
	}
}
