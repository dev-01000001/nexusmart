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
