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

func TestLoadW3BRateLimitDefaultsAndOverrides(t *testing.T) {
	t.Run("Defaults", func(t *testing.T) {
		t.Setenv("RATE_LIMIT_PHONE_OTP_PER_MIN", "")
		t.Setenv("RATE_LIMIT_SHIPPING_FEE_PER_MIN", "")
		cfg := Load()
		if cfg.RateLimitPhoneOTPPerMin != 5 {
			t.Fatalf("RateLimitPhoneOTPPerMin default = %d, want 5", cfg.RateLimitPhoneOTPPerMin)
		}
		if cfg.RateLimitShippingFeePerMin != 30 {
			t.Fatalf("RateLimitShippingFeePerMin default = %d, want 30", cfg.RateLimitShippingFeePerMin)
		}
	})

	t.Run("Overrides", func(t *testing.T) {
		t.Setenv("RATE_LIMIT_PHONE_OTP_PER_MIN", "9")
		t.Setenv("RATE_LIMIT_SHIPPING_FEE_PER_MIN", "45")
		cfg := Load()
		if cfg.RateLimitPhoneOTPPerMin != 9 {
			t.Fatalf("RateLimitPhoneOTPPerMin override = %d, want 9", cfg.RateLimitPhoneOTPPerMin)
		}
		if cfg.RateLimitShippingFeePerMin != 45 {
			t.Fatalf("RateLimitShippingFeePerMin override = %d, want 45", cfg.RateLimitShippingFeePerMin)
		}
	})
}
