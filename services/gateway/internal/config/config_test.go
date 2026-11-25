package config

import (
	"testing"
	"time"
)

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

func TestLoadW3B3DefaultsAndOverrides(t *testing.T) {
	t.Run("Defaults", func(t *testing.T) {
		t.Setenv("SEARCH_GRPC_ADDR", "")
		t.Setenv("SEARCH_FALLBACK_TIMEOUT_MS", "")
		t.Setenv("RATE_LIMIT_ORDER_LOOKUP_PER_MIN", "")
		t.Setenv("RATE_LIMIT_ORDER_LOOKUP_CODE_PER_10MIN", "")
		t.Setenv("PRODUCT_IMPORT_MAX_ROWS", "")
		t.Setenv("PRODUCT_IMPORT_MAX_BYTES", "")

		cfg := Load()
		if cfg.SearchGRPCAddr != "search:50059" {
			t.Fatalf("SearchGRPCAddr default = %q, want search:50059", cfg.SearchGRPCAddr)
		}
		if cfg.SearchFallbackTimeout != 1500*time.Millisecond {
			t.Fatalf("SearchFallbackTimeout default = %v, want 1500ms", cfg.SearchFallbackTimeout)
		}
		if cfg.RateLimitOrderLookupPerMin != 10 {
			t.Fatalf("RateLimitOrderLookupPerMin default = %d, want 10", cfg.RateLimitOrderLookupPerMin)
		}
		if cfg.RateLimitOrderLookupCodePer10Min != 5 {
			t.Fatalf("RateLimitOrderLookupCodePer10Min default = %d, want 5", cfg.RateLimitOrderLookupCodePer10Min)
		}
		if cfg.ProductImportMaxRows != 1000 {
			t.Fatalf("ProductImportMaxRows default = %d, want 1000", cfg.ProductImportMaxRows)
		}
		if cfg.ProductImportMaxBytes != 1048576 {
			t.Fatalf("ProductImportMaxBytes default = %d, want 1048576", cfg.ProductImportMaxBytes)
		}
	})

	t.Run("Overrides", func(t *testing.T) {
		t.Setenv("SEARCH_GRPC_ADDR", "127.0.0.1:59999")
		t.Setenv("SEARCH_FALLBACK_TIMEOUT_MS", "2200")
		t.Setenv("RATE_LIMIT_ORDER_LOOKUP_PER_MIN", "15")
		t.Setenv("RATE_LIMIT_ORDER_LOOKUP_CODE_PER_10MIN", "7")
		t.Setenv("PRODUCT_IMPORT_MAX_ROWS", "500")
		t.Setenv("PRODUCT_IMPORT_MAX_BYTES", "524288")

		cfg := Load()
		if cfg.SearchGRPCAddr != "127.0.0.1:59999" {
			t.Fatalf("SearchGRPCAddr override = %q, want 127.0.0.1:59999", cfg.SearchGRPCAddr)
		}
		if cfg.SearchFallbackTimeout != 2200*time.Millisecond {
			t.Fatalf("SearchFallbackTimeout override = %v, want 2200ms", cfg.SearchFallbackTimeout)
		}
		if cfg.RateLimitOrderLookupPerMin != 15 {
			t.Fatalf("RateLimitOrderLookupPerMin override = %d, want 15", cfg.RateLimitOrderLookupPerMin)
		}
		if cfg.RateLimitOrderLookupCodePer10Min != 7 {
			t.Fatalf("RateLimitOrderLookupCodePer10Min override = %d, want 7", cfg.RateLimitOrderLookupCodePer10Min)
		}
		if cfg.ProductImportMaxRows != 500 {
			t.Fatalf("ProductImportMaxRows override = %d, want 500", cfg.ProductImportMaxRows)
		}
		if cfg.ProductImportMaxBytes != 524288 {
			t.Fatalf("ProductImportMaxBytes override = %d, want 524288", cfg.ProductImportMaxBytes)
		}
	})
}

func TestLoadTrustedProxiesFallbackToCIDRs(t *testing.T) {
	t.Run("DefaultEmpty", func(t *testing.T) {
		t.Setenv("TRUSTED_PROXIES", "")
		t.Setenv("TRUSTED_PROXY_CIDRS", "")
		if cfg := Load(); cfg.TrustedProxies != "" {
			t.Fatalf("TrustedProxies = %q, want empty", cfg.TrustedProxies)
		}
	})

	t.Run("FallbackToTrustedProxyCIDRs", func(t *testing.T) {
		t.Setenv("TRUSTED_PROXIES", "")
		t.Setenv("TRUSTED_PROXY_CIDRS", "172.31.250.0/28")
		if cfg := Load(); cfg.TrustedProxies != "172.31.250.0/28" {
			t.Fatalf("TrustedProxies = %q, want 172.31.250.0/28", cfg.TrustedProxies)
		}
	})

	t.Run("TrustedProxiesTakesPrecedence", func(t *testing.T) {
		t.Setenv("TRUSTED_PROXIES", "10.0.0.0/8,web")
		t.Setenv("TRUSTED_PROXY_CIDRS", "172.31.250.0/28")
		if cfg := Load(); cfg.TrustedProxies != "10.0.0.0/8,web" {
			t.Fatalf("TrustedProxies = %q, want 10.0.0.0/8,web", cfg.TrustedProxies)
		}
	})
}
