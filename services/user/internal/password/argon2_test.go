package password

import (
	"strings"
	"testing"
)

func TestArgon2idHashAndVerify(t *testing.T) {
	h := NewDefaultHasher()

	hash1, err := h.Hash("StrongPass123!")
	if err != nil {
		t.Fatalf("Hash failed: %v", err)
	}
	hash2, err := h.Hash("StrongPass123!")
	if err != nil {
		t.Fatalf("second Hash failed: %v", err)
	}
	if !strings.HasPrefix(hash1, "$argon2id$v=") {
		t.Fatalf("expected PHC argon2id prefix, got %q", hash1)
	}
	if hash1 == hash2 {
		t.Fatalf("expected unique random salt per hash")
	}

	ok, err := h.Verify("StrongPass123!", hash1)
	if err != nil || !ok {
		t.Fatalf("expected Verify to succeed, got ok=%v err=%v", ok, err)
	}

	ok, err = h.Verify("WrongPass123!", hash1)
	if err != nil || ok {
		t.Fatalf("expected wrong password to fail, got ok=%v err=%v", ok, err)
	}

	if _, err := h.Verify("StrongPass123!", "not-a-valid-hash"); err == nil {
		t.Fatalf("expected invalid hash format to return error")
	}
}

func TestValidateStrength(t *testing.T) {
	if err := ValidateStrength("short"); err == nil {
		t.Fatalf("expected weak password error")
	}
	if err := ValidateStrength("12345678"); err != nil {
		t.Fatalf("expected valid password, got %v", err)
	}
}
