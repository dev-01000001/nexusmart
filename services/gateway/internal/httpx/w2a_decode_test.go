package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeJSONOversizedReturns413ViaWriteDecodeError(t *testing.T) {
	oversized := `{"password":"` + strings.Repeat("x", MaxBodyBytes+1024) + `"}`

	t.Run("DecodeJSON_Oversized", func(t *testing.T) {
		var dst map[string]any
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(oversized))
		err := DecodeJSON(req, &dst)
		if err == nil {
			t.Fatalf("expected error for oversized body")
		}
		rec := httptest.NewRecorder()
		WriteDecodeError(rec, req, err)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("expected 413, got %d (body=%s)", rec.Code, rec.Body.String())
		}
		var env ErrorEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || env.Error.Code != "PAYLOAD_TOO_LARGE" {
			t.Fatalf("expected PAYLOAD_TOO_LARGE envelope, got %s", rec.Body.String())
		}
	})

	t.Run("DecodeOptionalJSON_Oversized", func(t *testing.T) {
		var dst map[string]any
		req := httptest.NewRequest(http.MethodPost, "/api/v1/orders/ord-1/cancel", strings.NewReader(oversized))
		err := DecodeOptionalJSON(req, &dst)
		if err == nil {
			t.Fatalf("expected error for oversized optional body")
		}
		rec := httptest.NewRecorder()
		WriteDecodeError(rec, req, err)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("expected 413, got %d (body=%s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("DecodeJSON_MalformedReturns400", func(t *testing.T) {
		var dst map[string]any
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{invalid`))
		err := DecodeJSON(req, &dst)
		if err == nil {
			t.Fatalf("expected error for malformed json")
		}
		rec := httptest.NewRecorder()
		WriteDecodeError(rec, req, err)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d (body=%s)", rec.Code, rec.Body.String())
		}
		var env ErrorEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || env.Error.Code != "INVALID_JSON" {
			t.Fatalf("expected INVALID_JSON envelope, got %s", rec.Body.String())
		}
	})
}
