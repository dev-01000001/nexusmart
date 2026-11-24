package config

import "testing"

func TestLoadDefaults_UserGRPCAddrLocalhost(t *testing.T) {
	t.Setenv("USER_GRPC_ADDR", "")
	cfg := Load()
	if cfg.UserGRPCAddr != "localhost:50051" {
		t.Fatalf("default UserGRPCAddr = %q, want %q", cfg.UserGRPCAddr, "localhost:50051")
	}

	t.Setenv("USER_GRPC_ADDR", "user:50051")
	cfg = Load()
	if cfg.UserGRPCAddr != "user:50051" {
		t.Fatalf("overridden UserGRPCAddr = %q, want %q", cfg.UserGRPCAddr, "user:50051")
	}
}

func TestLoadDefaults_W2AConfig(t *testing.T) {
	t.Setenv("SHIPPING_MAX_REDELIVERY", "")
	t.Setenv("SHIPPING_CONFIG_KEY", "")
	t.Setenv("SHIPPING_LABEL_BASE_URL", "")
	cfg := Load()
	if cfg.MaxRedelivery != 3 {
		t.Fatalf("default MaxRedelivery = %d, want 3", cfg.MaxRedelivery)
	}
	if cfg.ConfigKey == "" {
		t.Fatalf("default ConfigKey must not be empty")
	}
	if cfg.LabelBaseURL != "http://localhost:8080/api/v1/shipping/labels" {
		t.Fatalf("default LabelBaseURL = %q, want default gateway labels URL", cfg.LabelBaseURL)
	}

	t.Setenv("SHIPPING_MAX_REDELIVERY", "5")
	t.Setenv("SHIPPING_LABEL_BASE_URL", "https://shop.example.com/api/v1/shipping/labels/")
	cfg = Load()
	if cfg.MaxRedelivery != 5 {
		t.Fatalf("overridden MaxRedelivery = %d, want 5", cfg.MaxRedelivery)
	}
	if cfg.LabelBaseURL != "https://shop.example.com/api/v1/shipping/labels" {
		t.Fatalf("overridden LabelBaseURL = %q, expected trimmed trailing slash", cfg.LabelBaseURL)
	}
}

func TestLoadDefaults_W3BConfig(t *testing.T) {
	t.Setenv("SHIPPING_FEE_CACHE_TTL_SECONDS", "")
	t.Setenv("QUOTE_CACHE_TTL", "")
	t.Setenv("SHIPPING_CREATE_RETRY_MAX", "")
	t.Setenv("SHIPPING_CREATE_RETRY_BASE_MS", "")
	t.Setenv("SHIPPING_CREATE_RETRY_MAX_BACKOFF_MS", "")
	t.Setenv("SHIPPING_CREATE_WORKER_INTERVAL_MS", "")
	t.Setenv("SHIPPING_TEST_FAULT_INJECTION", "")
	t.Setenv("SHIPPING_TEST_FAIL_CREATE_COUNT", "")

	cfg := Load()
	if cfg.QuoteCacheTTL.Seconds() != 60 {
		t.Fatalf("default QuoteCacheTTL = %v, want 60s (SHIPPING_FEE_CACHE_TTL_SECONDS)", cfg.QuoteCacheTTL)
	}
	if cfg.CreateRetryMax != 8 {
		t.Fatalf("default CreateRetryMax = %d, want 8", cfg.CreateRetryMax)
	}
	if cfg.CreateRetryBase.Milliseconds() != 2000 {
		t.Fatalf("default CreateRetryBase = %v, want 2000ms", cfg.CreateRetryBase)
	}
	if cfg.CreateRetryMaxBackoff.Milliseconds() != 300000 {
		t.Fatalf("default CreateRetryMaxBackoff = %v, want 300000ms", cfg.CreateRetryMaxBackoff)
	}
	if cfg.CreateWorkerInterval.Milliseconds() != 2000 {
		t.Fatalf("default CreateWorkerInterval = %v, want 2000ms", cfg.CreateWorkerInterval)
	}
	if cfg.TestFaultInjection {
		t.Fatalf("default TestFaultInjection must be false")
	}

	t.Setenv("SHIPPING_FEE_CACHE_TTL_SECONDS", "45")
	t.Setenv("SHIPPING_CREATE_RETRY_MAX", "5")
	t.Setenv("SHIPPING_CREATE_RETRY_BASE_MS", "1500")
	t.Setenv("SHIPPING_CREATE_RETRY_MAX_BACKOFF_MS", "60000")
	t.Setenv("SHIPPING_CREATE_WORKER_INTERVAL_MS", "500")
	t.Setenv("SHIPPING_TEST_FAULT_INJECTION", "true")
	t.Setenv("SHIPPING_TEST_FAIL_CREATE_COUNT", "3")
	cfg = Load()
	if cfg.QuoteCacheTTL.Seconds() != 45 || cfg.CreateRetryMax != 5 ||
		cfg.CreateRetryBase.Milliseconds() != 1500 || cfg.CreateRetryMaxBackoff.Milliseconds() != 60000 ||
		cfg.CreateWorkerInterval.Milliseconds() != 500 || !cfg.TestFaultInjection || cfg.TestFailCreateCount != 3 {
		t.Fatalf("unexpected overridden W3B config: %+v", cfg)
	}
}
