package config

import "testing"

func TestLoadBankAccountKeyHasNoInsecureDefault(t *testing.T) {
	t.Setenv("BANK_ACCOUNT_KEY", "")
	cfg := Load()
	if cfg.BankAccountKey != "" {
		t.Fatalf("Load().BankAccountKey with empty env = %q; want empty string so startup fails closed", cfg.BankAccountKey)
	}

	const sampleKey = "ZGV2LW9ubHktYmFuay1hY2NvdW50LWtleS0zMmJ5dGU="
	t.Setenv("BANK_ACCOUNT_KEY", "  "+sampleKey+"  ")
	cfg = Load()
	if cfg.BankAccountKey != sampleKey {
		t.Fatalf("Load().BankAccountKey with set env = %q; want %q", cfg.BankAccountKey, sampleKey)
	}
}
