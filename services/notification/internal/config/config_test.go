package config

import (
	"strings"
	"testing"
)

func TestConfigValidateSMTP(t *testing.T) {
	t.Run("empty credentials valid", func(t *testing.T) {
		cfg := Config{SMTPFrom: "no-reply@nexusmart.local"}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("expected valid config, got %v", err)
		}
	})

	t.Run("both credentials set valid", func(t *testing.T) {
		cfg := Config{
			SMTPFrom:     "NexusMart <no-reply@nexusmartai.store>",
			SMTPUsername: "resend",
			SMTPPassword: "re_test_key",
		}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("expected valid config, got %v", err)
		}
	})

	t.Run("only username set fails without leaking value", func(t *testing.T) {
		cfg := Config{
			SMTPFrom:     "no-reply@nexusmartai.store",
			SMTPUsername: "resend-only-user",
		}
		err := cfg.Validate()
		if err == nil {
			t.Fatalf("expected error when SMTP_PASSWORD is missing")
		}
		if strings.Contains(err.Error(), "resend-only-user") {
			t.Fatalf("error leaked SMTP_USERNAME: %v", err)
		}
	})

	t.Run("only password set fails without leaking value", func(t *testing.T) {
		cfg := Config{
			SMTPFrom:     "no-reply@nexusmartai.store",
			SMTPPassword: "re_secret_password",
		}
		err := cfg.Validate()
		if err == nil {
			t.Fatalf("expected error when SMTP_USERNAME is missing")
		}
		if strings.Contains(err.Error(), "re_secret_password") {
			t.Fatalf("error leaked SMTP_PASSWORD: %v", err)
		}
	})
}

func TestConfigLoadSMTPDefaults(t *testing.T) {
	t.Setenv("SMTP_USERNAME", "")
	t.Setenv("SMTP_PASSWORD", "")
	cfg := Load()
	if cfg.SMTPUsername != "" || cfg.SMTPPassword != "" {
		t.Fatalf("expected empty default SMTP credentials, got user=%q pass_len=%d", cfg.SMTPUsername, len(cfg.SMTPPassword))
	}
}
