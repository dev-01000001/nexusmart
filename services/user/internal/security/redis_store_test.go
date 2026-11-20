package security

import (
	"errors"
	"testing"
)

func TestHashOTPAndResultMapping(t *testing.T) {
	d1 := hashOTP("Buyer@Example.com", "123456")
	d2 := hashOTP("  buyer@example.com ", " 123456 ")
	d3 := hashOTP("buyer@example.com", "654321")
	if d1 != d2 {
		t.Fatalf("expected normalized email+otp digest match, got %q != %q", d1, d2)
	}
	if d1 == d3 {
		t.Fatalf("expected different digests for different OTP codes")
	}

	if err := mapVerifyOTPResult(1); err != nil {
		t.Fatalf("expected nil for 1, got %v", err)
	}
	if !errors.Is(mapVerifyOTPResult(-1), ErrOTPNotFound) {
		t.Fatalf("expected ErrOTPNotFound for -1")
	}
	if !errors.Is(mapVerifyOTPResult(-2), ErrOTPTooManyAttempts) {
		t.Fatalf("expected ErrOTPTooManyAttempts for -2")
	}
	if !errors.Is(mapVerifyOTPResult(0), ErrOTPInvalid) {
		t.Fatalf("expected ErrOTPInvalid for 0")
	}
}
