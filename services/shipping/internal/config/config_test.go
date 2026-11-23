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
