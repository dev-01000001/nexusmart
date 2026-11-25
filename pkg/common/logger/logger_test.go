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
		slog.String("account_number_masked", "********4455"),
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
	if parsed["account_number_masked"] != "********4455" {
		t.Fatalf("account_number_masked = %v, want ********4455 (must not be redacted)", parsed["account_number_masked"])
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
	if got := RedactValue("account_number_masked", "********4455"); got != "********4455" {
		t.Fatalf("RedactValue(account_number_masked) = %q, want ********4455", got)
	}
}

func TestScrubSecretsEmbeddedPIIJWTAndExtendedKeys(t *testing.T) {
	var buf bytes.Buffer
	log := New("notification-service", WithWriter(&buf), WithLevel(slog.LevelDebug))

	smtpErr := errors.New("smtp RCPT TO <buyer.secret@nexusmart.vn> failed: 550 mailbox unavailable")
	ghnErr := errors.New("ghn 400: invalid phone 0901234567 and backup +84 912 345 678,02838251234")
	rawJWT := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJ1c2VyLTEifQ.sig_secret_value"

	log.ErrorContext(
		context.Background(),
		"delivery failed for customer.one@example.com phone 0987654321 token="+rawJWT,
		slog.Any("smtp_err", smtpErr),
		slog.Any("ghn_err", ghnErr),
		slog.String("detail", "jwt="+rawJWT),
		slog.String("bank_account", "0123456789012"),
		slog.String("account_number", "9876543210"),
		slog.String("full_name", "Nguyen Van A"),
		slog.String("recipient_name", "Tran Thi B"),
		slog.String("address", "123 Le Loi, Q1"),
		slog.String("shipping_address", "456 Hai Ba Trung"),
		slog.String("id_card", "001234567890"),
		slog.String("cccd", "079200012345"),
		slog.String("tax_code", "0312345678"),
		slog.String("safe_meta", "trace=4bf92f3577b34da6a3ce929d0e0e4736 uuid=bb4650ca-1ba9-4c3c-a217-2e21260ad9cf amount=5000000 ts=1763600000 host=127.0.0.1:8080"),
	)

	raw := buf.String()
	for _, leaked := range []string{
		"buyer.secret@nexusmart.vn",
		"customer.one@example.com",
		"0901234567",
		"912 345 678",
		"02838251234",
		"0987654321",
		"eyJzdWIiOiJ1c2VyLTEifQ",
		"sig_secret_value",
		"0123456789012",
		"9876543210",
		"Nguyen Van A",
		"Tran Thi B",
		"123 Le Loi",
		"456 Hai Ba Trung",
		"001234567890",
		"079200012345",
		"0312345678",
	} {
		if strings.Contains(raw, leaked) {
			t.Fatalf("log output leaked sensitive value %q: %s", leaked, raw)
		}
	}

	var parsed map[string]any
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("invalid JSON log: %v", err)
	}
	if !strings.Contains(parsed["smtp_err"].(string), "bu***@nexusmart.vn") {
		t.Fatalf("expected masked email in smtp_err, got %q", parsed["smtp_err"])
	}
	if !strings.Contains(parsed["ghn_err"].(string), "090****567") || !strings.Contains(parsed["ghn_err"].(string), "028****234") {
		t.Fatalf("expected masked phones in ghn_err, got %q", parsed["ghn_err"])
	}
	if parsed["detail"] != "jwt=[REDACTED]" {
		t.Fatalf("detail = %q, want jwt=[REDACTED]", parsed["detail"])
	}
	for _, k := range []string{"bank_account", "account_number", "full_name", "recipient_name", "address", "shipping_address", "id_card", "cccd", "tax_code"} {
		if parsed[k] != redactedMask {
			t.Fatalf("%s = %v, want %s", k, parsed[k], redactedMask)
		}
	}
	wantSafe := "trace=4bf92f3577b34da6a3ce929d0e0e4736 uuid=bb4650ca-1ba9-4c3c-a217-2e21260ad9cf amount=5000000 ts=1763600000 host=127.0.0.1:8080"
	if parsed["safe_meta"] != wantSafe {
		t.Fatalf("safe_meta unexpectedly altered:\ngot:  %q\nwant: %q", parsed["safe_meta"], wantSafe)
	}
}
