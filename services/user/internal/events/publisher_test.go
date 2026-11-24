package events

import (
	"encoding/json"
	"testing"
)

func TestPhoneOTPRequestedPayloadJSONContract(t *testing.T) {
	payload := PhoneOTPRequestedPayload{
		UserID:      "u-123",
		Email:       "buyer@example.com",
		FullName:    "Nguyen Van A",
		PhoneMasked: "090****789",
		OTP:         "654321",
		ExpiresAt:   "2026-11-24T10:05:00Z",
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var decoded map[string]string
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	want := map[string]string{
		"user_id":      "u-123",
		"email":        "buyer@example.com",
		"full_name":    "Nguyen Van A",
		"phone_masked": "090****789",
		"otp":          "654321",
		"expires_at":   "2026-11-24T10:05:00Z",
	}
	if len(decoded) != len(want) {
		t.Fatalf("field count = %d, want %d: %v", len(decoded), len(want), decoded)
	}
	for k, v := range want {
		if decoded[k] != v {
			t.Fatalf("field %q = %q, want %q", k, decoded[k], v)
		}
	}
}
