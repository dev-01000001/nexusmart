package config

import "testing"

func TestLoadRateLimitBankOTPPerMin(t *testing.T) {
	t.Run("DefaultIs5", func(t *testing.T) {
		t.Setenv("RATE_LIMIT_BANK_OTP_PER_MIN", "")
		cfg := Load()
		if cfg.RateLimitBankOTPPerMin != 5 {
			t.Fatalf("RateLimitBankOTPPerMin default = %d, want 5", cfg.RateLimitBankOTPPerMin)
		}
	})

	t.Run("EnvOverride", func(t *testing.T) {
		t.Setenv("RATE_LIMIT_BANK_OTP_PER_MIN", "12")
		cfg := Load()
		if cfg.RateLimitBankOTPPerMin != 12 {
			t.Fatalf("RateLimitBankOTPPerMin override = %d, want 12", cfg.RateLimitBankOTPPerMin)
		}
	})
}
