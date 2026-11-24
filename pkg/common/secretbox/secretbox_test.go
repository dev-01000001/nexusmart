package secretbox

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

const (
	shippingDevKeyB64 = "ZGV2LW9ubHktc2hpcHBpbmctY29uZmlnLWtleS0zMmI="
	// Fixed ciphertext fixture generated with shipping's former services/shipping/internal/service/cipher.go
	// using shippingDevKeyB64, nonce 0x00112233445566778899aabb, and plaintext "ghn-legacy-carrier-token-2026".
	legacyShippingCiphertextB64 = "ABEiM0RVZneImaq7TlU5RzSvZ/GeBsb3kSirRjZIrzDkiA1BqE6j9wNu7BAGAiM61ibmd30N9wRX"
	legacyShippingPlaintext     = "ghn-legacy-carrier-token-2026"
)

func TestBoxRoundTripAndRandomNonces(t *testing.T) {
	// Padded base64 key.
	box, err := NewFromBase64(shippingDevKeyB64, "SHIPPING_CONFIG_KEY")
	if err != nil {
		t.Fatalf("NewFromBase64 padded: %v", err)
	}
	// Unpadded raw base64 key must produce identical key material.
	rawBox, err := NewFromBase64(strings.TrimRight(shippingDevKeyB64, "="), "BANK_ACCOUNT_KEY")
	if err != nil {
		t.Fatalf("NewFromBase64 unpadded: %v", err)
	}

	cases := []string{
		"",
		"19034567890123",
		"ghn-live-secret-token-987654",
		"Tài khoản Nguyễn Văn Á — 0123456789",
		strings.Repeat("x", 4096),
	}
	for _, pt := range cases {
		ct1, err := box.Encrypt(pt)
		if err != nil {
			t.Fatalf("Encrypt(%q) #1: %v", pt, err)
		}
		ct2, err := box.Encrypt(pt)
		if err != nil {
			t.Fatalf("Encrypt(%q) #2: %v", pt, err)
		}
		if bytes.Equal(ct1, ct2) {
			t.Fatalf("Encrypt(%q) produced identical ciphertexts; expected fresh random nonces", pt)
		}
		if pt != "" && bytes.Contains(ct1, []byte(pt)) {
			t.Fatalf("ciphertext must not contain plaintext")
		}
		got1, err := box.Decrypt(ct1)
		if err != nil || got1 != pt {
			t.Fatalf("Decrypt #1 = (%q, %v), want %q", got1, err, pt)
		}
		got2, err := rawBox.Decrypt(ct2)
		if err != nil || got2 != pt {
			t.Fatalf("cross-decrypt with raw-base64 Box = (%q, %v), want %q", got2, err, pt)
		}
	}
}

func TestBoxWrongKeyAndTamperedCiphertext(t *testing.T) {
	box1, err := NewFromBase64(shippingDevKeyB64, "BANK_ACCOUNT_KEY")
	if err != nil {
		t.Fatalf("NewFromBase64: %v", err)
	}
	const secret = "19001234567890"
	ct, err := box1.Encrypt(secret)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	// Wrong key must fail without leaking plaintext or key.
	wrongKey := bytes.Repeat([]byte{0x42}, 32)
	box2, err := New(wrongKey, "BANK_ACCOUNT_KEY")
	if err != nil {
		t.Fatalf("New wrongKey: %v", err)
	}
	if got, err := box2.Decrypt(ct); err == nil || got != "" || strings.Contains(err.Error(), secret) {
		t.Fatalf("wrong key Decrypt = (%q, %v), want clean error without secret", got, err)
	}

	// Tampering nonce, ciphertext body, or GCM tag must fail cleanly.
	for _, pos := range []int{0, 12, len(ct) - 1} {
		tampered := append([]byte(nil), ct...)
		tampered[pos] ^= 0xff
		if got, err := box1.Decrypt(tampered); err == nil || got != "" || strings.Contains(err.Error(), secret) {
			t.Fatalf("tampered pos=%d Decrypt = (%q, %v), want clean error without secret", pos, got, err)
		}
	}
}

func TestBoxTruncatedCiphertext(t *testing.T) {
	box, err := NewFromBase64(shippingDevKeyB64, "BANK_ACCOUNT_KEY")
	if err != nil {
		t.Fatalf("NewFromBase64: %v", err)
	}
	for _, blob := range [][]byte{
		nil,
		{},
		make([]byte, 11),      // shorter than 12-byte nonce
		make([]byte, 12),      // nonce only, missing 16-byte GCM tag
		make([]byte, 12+16-1), // nonce + truncated tag
	} {
		if got, err := box.Decrypt(blob); err == nil || got != "" {
			t.Fatalf("Decrypt(len=%d) = (%q, %v), want truncation error", len(blob), got, err)
		}
	}
}

func TestBoxDecryptsLegacyShippingFixture(t *testing.T) {
	box, err := NewFromBase64(shippingDevKeyB64, "SHIPPING_CONFIG_KEY")
	if err != nil {
		t.Fatalf("NewFromBase64: %v", err)
	}
	legacyBlob, err := base64.StdEncoding.DecodeString(legacyShippingCiphertextB64)
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	got, err := box.Decrypt(legacyBlob)
	if err != nil {
		t.Fatalf("Decrypt legacy shipping fixture: %v", err)
	}
	if got != legacyShippingPlaintext {
		t.Fatalf("Decrypt legacy shipping fixture = %q, want %q", got, legacyShippingPlaintext)
	}
}

func TestBoxKeyValidationErrorsIncludeEnvNameAndNeverLeakKey(t *testing.T) {
	const badB64 = "!!!not-base64-secret-value!!!"
	if _, err := NewFromBase64("   ", "BANK_ACCOUNT_KEY"); err == nil || !strings.Contains(err.Error(), "BANK_ACCOUNT_KEY") {
		t.Fatalf("empty key error = %v, want BANK_ACCOUNT_KEY in message", err)
	}
	if _, err := NewFromBase64(badB64, "BANK_ACCOUNT_KEY"); err == nil ||
		!strings.Contains(err.Error(), "BANK_ACCOUNT_KEY") ||
		strings.Contains(err.Error(), badB64) {
		t.Fatalf("invalid base64 error = %v, want BANK_ACCOUNT_KEY without leaking key", err)
	}
	shortB64 := base64.StdEncoding.EncodeToString([]byte("short-key-16byte"))
	if _, err := NewFromBase64(shortB64, "SHIPPING_CONFIG_KEY"); err == nil ||
		!strings.Contains(err.Error(), "SHIPPING_CONFIG_KEY") ||
		strings.Contains(err.Error(), shortB64) {
		t.Fatalf("short key error = %v, want SHIPPING_CONFIG_KEY without leaking key", err)
	}
	if _, err := New([]byte("too-short"), ""); err == nil || !strings.Contains(err.Error(), "encryption key") {
		t.Fatalf("empty envName fallback error = %v, want 'encryption key'", err)
	}

	var nilBox *Box
	if _, err := nilBox.Encrypt("secret"); err == nil {
		t.Fatalf("nil Box.Encrypt must fail")
	}
	if _, err := nilBox.Decrypt([]byte("01234567890123456789012345678901")); err == nil {
		t.Fatalf("nil Box.Decrypt must fail")
	}
}
