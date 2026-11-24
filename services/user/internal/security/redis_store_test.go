package security

import (
	"errors"
	"strings"
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

func TestPhoneOTPDigestKeysAndResultMapping(t *testing.T) {
	d1 := hashPhoneOTP("user-1", "0901234567", "0901234567", "123456")
	d2 := hashPhoneOTP(" user-1 ", " 0901234567 ", " 0901234567 ", " 123456 ")
	dDiffPhone := hashPhoneOTP("user-1", "0909999999", "0901234567", "123456")
	dDiffProfile := hashPhoneOTP("user-1", "0901234567", "0909999999", "123456")
	dDiffUser := hashPhoneOTP("user-2", "0901234567", "0901234567", "123456")
	dDiffOTP := hashPhoneOTP("user-1", "0901234567", "0901234567", "654321")

	if len(d1) != 64 {
		t.Fatalf("expected 64-char sha256 hex digest, got len=%d", len(d1))
	}
	if d1 != d2 {
		t.Fatalf("expected trimmed inputs to match digest")
	}
	for name, other := range map[string]string{
		"diffPhone":   dDiffPhone,
		"diffProfile": dDiffProfile,
		"diffUser":    dDiffUser,
		"diffOTP":     dDiffOTP,
	} {
		if d1 == other {
			t.Fatalf("expected distinct digest for %s", name)
		}
	}
	if strings.Contains(d1, "123456") || strings.Contains(d1, "0901234567") {
		t.Fatalf("digest must not contain raw OTP or phone")
	}

	if got := phoneOTPKey("u-1"); got != "user:phone_otp:u-1" {
		t.Fatalf("phoneOTPKey = %q", got)
	}
	if got := phoneOTPAttemptsKey("u-1"); got != "user:phone_otp_attempts:u-1" {
		t.Fatalf("phoneOTPAttemptsKey = %q", got)
	}
	if got := phoneOTPCooldownKey("u-1"); got != "user:phone_otp_cooldown:u-1" {
		t.Fatalf("phoneOTPCooldownKey = %q", got)
	}
	if got := phoneOTPHourlyKey("u-1"); got != "user:phone_otp_hourly:u-1" {
		t.Fatalf("phoneOTPHourlyKey = %q", got)
	}

	if d, err := mapSavePhoneOTPResult([]int64{1, 0}); err != nil || d != 0 {
		t.Fatalf("code 1: got d=%v err=%v", d, err)
	}
	if d, err := mapSavePhoneOTPResult([]int64{-1, 42500}); !errors.Is(err, ErrPhoneOTPCooldown) || d != 42500*time.Millisecond {
		t.Fatalf("code -1: got d=%v err=%v", d, err)
	}
	if d, err := mapSavePhoneOTPResult([]int64{-2, 1200000}); !errors.Is(err, ErrPhoneOTPRateLimitReached) || d != 1200000*time.Millisecond {
		t.Fatalf("code -2: got d=%v err=%v", d, err)
	}
	if _, err := mapSavePhoneOTPResult([]int64{}); err == nil {
		t.Fatalf("empty slice must return error")
	}
	if !strings.Contains(verifyPhoneOTPScript, "ct_eq") {
		t.Fatalf("verifyPhoneOTPScript must perform constant-time comparison")
	}
}
