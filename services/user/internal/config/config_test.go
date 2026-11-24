package config

import (
	"testing"
	"time"
)

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

func TestLoadPhoneOTPConfigDefaultsAndEnv(t *testing.T) {
	t.Setenv("USER_PHONE_OTP_TTL_SECONDS", "")
	t.Setenv("USER_PHONE_OTP_COOLDOWN_SECONDS", "")
	t.Setenv("USER_PHONE_OTP_MAX_PER_HOUR", "")

	cfg := Load()
	if cfg.PhoneOTPTTL != 300*time.Second {
		t.Fatalf("default PhoneOTPTTL = %v; want 300s", cfg.PhoneOTPTTL)
	}
	if cfg.PhoneOTPCooldown != 60*time.Second {
		t.Fatalf("default PhoneOTPCooldown = %v; want 60s", cfg.PhoneOTPCooldown)
	}
	if cfg.PhoneOTPMaxPerHour != 5 {
		t.Fatalf("default PhoneOTPMaxPerHour = %d; want 5", cfg.PhoneOTPMaxPerHour)
	}

	t.Setenv("USER_PHONE_OTP_TTL_SECONDS", "180")
	t.Setenv("USER_PHONE_OTP_COOLDOWN_SECONDS", "45")
	t.Setenv("USER_PHONE_OTP_MAX_PER_HOUR", "3")

	cfg = Load()
	if cfg.PhoneOTPTTL != 180*time.Second {
		t.Fatalf("env PhoneOTPTTL = %v; want 180s", cfg.PhoneOTPTTL)
	}
	if cfg.PhoneOTPCooldown != 45*time.Second {
		t.Fatalf("env PhoneOTPCooldown = %v; want 45s", cfg.PhoneOTPCooldown)
	}
	if cfg.PhoneOTPMaxPerHour != 3 {
		t.Fatalf("env PhoneOTPMaxPerHour = %d; want 3", cfg.PhoneOTPMaxPerHour)
	}
}
