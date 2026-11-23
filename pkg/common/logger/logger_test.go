package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type stringerVal string

func (s stringerVal) String() string { return string(s) }

func TestLoggerRedactionAndTraceInjection(t *testing.T) {
	var buf bytes.Buffer
	log := New("order-service", WithWriter(&buf), WithLevel(slog.LevelDebug))

	tp := sdktrace.NewTracerProvider()
	defer func() { _ = tp.Shutdown(context.Background()) }()

	ctx, span := tp.Tracer("test").Start(context.Background(), "checkout")
	expectedTraceID := span.SpanContext().TraceID().String()
	expectedSpanID := span.SpanContext().SpanID().String()
	defer span.End()

	log.With(slog.String("api_key", "sk-live-secret-999")).InfoContext(
		ctx,
		"user checkout",
		slog.String("password", "hunter2"),
		slog.String("refresh_token", "tok_abc123"),
		slog.String("authorization", "Bearer eyJhbGciOiJIUzI1NiJ9.payload.sig"),
		slog.String("otp", "482910"),
		slog.String("email", "nguyen.van.a@nexusmart.vn"),
		slog.String("phone", "0901234567"),
		slog.String("raw_header", "Bearer secret-jwt-token"),
		slog.Group("nested", slog.String("webhook_secret", "whsec_12345"), slog.String("order_id", "ord-1")),
	)

	raw := buf.String()
	for _, leaked := range []string{"hunter2", "tok_abc123", "eyJhbGciOiJIUzI1NiJ9", "482910", "sk-live-secret-999", "whsec_12345", "secret-jwt-token", "nguyen.van.a@", "0901234567"} {
		if strings.Contains(raw, leaked) {
			t.Fatalf("log output leaked sensitive value %q: %s", leaked, raw)
		}
	}

	var parsed map[string]any
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("failed to parse JSON log: %v\nraw=%s", err, raw)
	}

	if parsed["service"] != "order-service" {
		t.Fatalf("service = %v, want order-service", parsed["service"])
	}
	if parsed["trace_id"] != expectedTraceID {
		t.Fatalf("trace_id = %v, want %s", parsed["trace_id"], expectedTraceID)
	}
	if parsed["span_id"] != expectedSpanID {
		t.Fatalf("span_id = %v, want %s", parsed["span_id"], expectedSpanID)
	}
	if parsed["password"] != redactedMask {
		t.Fatalf("password = %v, want %s", parsed["password"], redactedMask)
	}
	if parsed["otp"] != redactedMask {
		t.Fatalf("otp = %v, want %s", parsed["otp"], redactedMask)
	}
	if parsed["email"] != "ng***@nexusmart.vn" {
		t.Fatalf("email = %v, want ng***@nexusmart.vn", parsed["email"])
	}
	if parsed["phone"] != "090****567" {
		t.Fatalf("phone = %v, want 090****567", parsed["phone"])
	}
	if parsed["raw_header"] != "Bearer [REDACTED]" {
		t.Fatalf("raw_header = %v, want Bearer [REDACTED]", parsed["raw_header"])
	}
	nested, ok := parsed["nested"].(map[string]any)
	if !ok {
		t.Fatalf("expected nested group map, got %T", parsed["nested"])
	}
	if nested["webhook_secret"] != redactedMask || nested["order_id"] != "ord-1" {
		t.Fatalf("unexpected nested group: %#v", nested)
	}
}

func TestMaskHelpers(t *testing.T) {
	if got := MaskEmail("a@b.vn"); got != "*@b.vn" {
		t.Fatalf("MaskEmail short got %q", got)
	}
	if got := MaskEmail("invalid"); got != redactedMask {
		t.Fatalf("MaskEmail invalid got %q", got)
	}
	if got := MaskPhone("1234"); got != redactedMask {
		t.Fatalf("MaskPhone short got %q", got)
	}
	if got := MaskPhone("098765"); got != "0***65" {
		t.Fatalf("MaskPhone medium got %q", got)
	}
}

func TestLoggerRedactsNonStringErrorsEmbeddedTokensAndExpandedKeys(t *testing.T) {
	var buf bytes.Buffer
	log := New("payment-service", WithWriter(&buf), WithLevel(slog.LevelDebug))

	dbErr := errors.New("connect failed: postgres://nexusmart:superSecretPass@postgres:5432/payment_db?sslmode=disable")
	upstreamErr := errors.New("gateway rejected request with Bearer eyJhbGciOiJIUzI1NiJ9.secretpayload.sig123")

	log.ErrorContext(
		context.Background(),
		"request failed with Bearer inline-tok-999 and redis://default:redisPass@redis:6379",
		slog.Any("err", dbErr),
		slog.Any("upstream_err", upstreamErr),
		slog.Any("email", stringerVal("buyer@example.com")),
		slog.Int64("phone", 901234567),
		slog.String("hmac", "a1b2c3d4"),
		slog.String("jwt", "eyJ.abc.def"),
		slog.String("dsn", "postgres://u:p@h/db"),
		slog.String("database_url", "postgres://u:p2@h/db"),
		slog.String("credential", "cred-xyz"),
		slog.String("street_address", "123 Nguyen Hue"),
		slog.String("detail", "downstream auth header was Bearer embedded-secret-777 done"),
	)

	raw := buf.String()
	for _, leaked := range []string{
		"superSecretPass",
		"secretpayload",
		"inline-tok-999",
		"redisPass",
		"buyer@example.com",
		"901234567",
		"a1b2c3d4",
		"eyJ.abc.def",
		"p2@h",
		"cred-xyz",
		"123 Nguyen Hue",
		"embedded-secret-777",
	} {
		if strings.Contains(raw, leaked) {
			t.Fatalf("log output leaked sensitive value %q: %s", leaked, raw)
		}
	}

	var parsed map[string]any
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("invalid JSON log: %v", err)
	}
	if parsed["email"] != "bu***@example.com" {
		t.Fatalf("email = %v, want bu***@example.com", parsed["email"])
	}
	if parsed["phone"] != "901****567" {
		t.Fatalf("phone = %v, want 901****567", parsed["phone"])
	}
	if !strings.Contains(parsed["err"].(string), "postgres://nexusmart:[REDACTED]@postgres:5432") {
		t.Fatalf("err not scrubbed properly: %v", parsed["err"])
	}
	if !strings.Contains(parsed["upstream_err"].(string), "Bearer [REDACTED]") {
		t.Fatalf("upstream_err not scrubbed properly: %v", parsed["upstream_err"])
	}
	if !strings.Contains(parsed["detail"].(string), "Bearer [REDACTED] done") {
		t.Fatalf("detail not scrubbed properly: %v", parsed["detail"])
	}
	for _, k := range []string{"hmac", "jwt", "dsn", "database_url", "credential", "street_address"} {
		if parsed[k] != redactedMask {
			t.Fatalf("%s = %v, want %s", k, parsed[k], redactedMask)
		}
	}
}

func TestLoggerRedactsBankAccountNumberWithoutRedactingMasked(t *testing.T) {
	var buf bytes.Buffer
	log := New("payment-service", WithWriter(&buf), WithLevel(slog.LevelDebug))

	log.InfoContext(
		context.Background(),
		"refund bank details recorded",
		slog.String("bank_account_number", "19036123456789"),
		slog.String("account_number", "001122334455"),
		slog.String("bank_account_masked", "**********6789"),
		slog.String("bank_code", "TCB"),
	)

	raw := buf.String()
	for _, leaked := range []string{"19036123456789", "001122334455"} {
		if strings.Contains(raw, leaked) {
			t.Fatalf("log output leaked sensitive bank account %q: %s", leaked, raw)
		}
	}

	var parsed map[string]any
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("invalid JSON log: %v\nraw=%s", err, raw)
	}
	if parsed["bank_account_number"] != redactedMask {
		t.Fatalf("bank_account_number = %v, want %s", parsed["bank_account_number"], redactedMask)
	}
	if parsed["account_number"] != redactedMask {
		t.Fatalf("account_number = %v, want %s", parsed["account_number"], redactedMask)
	}
	if parsed["bank_account_masked"] != "**********6789" {
		t.Fatalf("bank_account_masked = %v, want **********6789 (must not be redacted)", parsed["bank_account_masked"])
	}
	if parsed["bank_code"] != "TCB" {
		t.Fatalf("bank_code = %v, want TCB", parsed["bank_code"])
	}

	if got := RedactValue("bank_account_number", "19036123456789"); got != redactedMask {
		t.Fatalf("RedactValue(bank_account_number) = %q, want %s", got, redactedMask)
	}
	if got := RedactValue("account_number", "001122334455"); got != redactedMask {
		t.Fatalf("RedactValue(account_number) = %q, want %s", got, redactedMask)
	}
	if got := RedactValue("bank_account_masked", "**********6789"); got != "**********6789" {
		t.Fatalf("RedactValue(bank_account_masked) = %q, want **********6789", got)
	}
}
