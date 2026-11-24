package main

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestW3A_D5_EmptyOrInvalidBankAccountKeyRefusesStartup(t *testing.T) {
	badKeys := []struct {
		name      string
		val       string
		mustNotIn string
	}{
		{"empty", "", ""},
		{"whitespace", "   \t\n  ", ""},
		{"invalid_base64", "!!!bad-secret-bank-key!!!", "!!!bad-secret-bank-key!!!"},
		{"wrong_length_16bytes", base64.StdEncoding.EncodeToString([]byte("0123456789abcdef")), base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))},
	}

	for _, tc := range badKeys {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("BANK_ACCOUNT_KEY", tc.val)
			t.Setenv("DATABASE_URL", "postgres://invalid:invalid@127.0.0.1:1/user_db?sslmode=disable")
			err := run()
			if err == nil {
				t.Fatalf("run() with BANK_ACCOUNT_KEY=%s succeeded; want error", tc.name)
			}
			if !strings.Contains(err.Error(), "BANK_ACCOUNT_KEY") {
				t.Fatalf("run() error = %v; want error mentioning BANK_ACCOUNT_KEY", err)
			}
			if tc.mustNotIn != "" && strings.Contains(err.Error(), tc.mustNotIn) {
				t.Fatalf("run() error leaked key material")
			}
		})
	}
}
