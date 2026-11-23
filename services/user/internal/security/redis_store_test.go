package security

import (
	"errors"
	"testing"
	"time"
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

func TestEvalTokenRevocationBoundaries(t *testing.T) {
	tBefore := time.Unix(1700000099, 0)
	tEqual := time.Unix(1700000100, 0)
	tAfter := time.Unix(1700000101, 0)

	cases := []struct {
		name      string
		jtiVal    any
		revVal    any
		iat       *time.Time
		wantRev   bool
		wantError bool
	}{
		{"neither set, nil iat", nil, nil, nil, false, false},
		{"jti revoked", "1", nil, &tAfter, true, false},
		{"iat < revoked_before", nil, "1700000100", &tBefore, true, false},
		{"iat == revoked_before", nil, "1700000100", &tEqual, true, false},
		{"iat > revoked_before", nil, "1700000100", &tAfter, false, false},
		{"missing iat when revoked_before set", nil, "1700000100", nil, true, false},
		{"malformed revoked_before fails closed", nil, "not-a-number", &tAfter, false, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rev, err := evalTokenRevocation(tc.jtiVal, tc.revVal, tc.iat)
			if tc.wantError {
				if err == nil {
					t.Fatalf("expected error, got nil (rev=%v)", rev)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if rev != tc.wantRev {
				t.Fatalf("want revoked=%v, got %v", tc.wantRev, rev)
			}
		})
	}

	if got := revokedBeforeKey("user-123"); got != "user:revoked_before:user-123" {
		t.Fatalf("unexpected revokedBeforeKey: %q", got)
	}
}
