package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/nexusmart/core/pkg/common/telemetry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type fakePIIPayload struct {
	Email             string
	Phone             string
	Password          string
	OTP               string
	BankAccountNumber string
	StreetAddress     string
}

func TestGRPCUnaryServerInterceptorLogsSingleJSONLineWithTraceAndRedactsPII(t *testing.T) {
	shutdown, err := telemetry.Init(context.Background(), telemetry.Config{
		ServiceName: "order",
		SampleRatio: 1.0,
	})
	if err != nil {
		t.Fatalf("telemetry.Init error: %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	const (
		traceID     = "4bf92f3577b34da6a3ce929d0e0e4736"
		traceparent = "00-" + traceID + "-00f067aa0ba902b7-01"
	)

	reqPayload := &fakePIIPayload{
		Email:             "buyer.secret@nexusmart.vn",
		Phone:             "0901234567",
		Password:          "SuperSecretPass!99",
		OTP:               "654321",
		BankAccountNumber: "19036123456789",
		StreetAddress:     "123 Nguyen Hue District 1",
	}
	respPayload := &fakePIIPayload{
		Email:             "seller.secret@nexusmart.vn",
		Phone:             "0987654321",
		BankAccountNumber: "99887766554433",
	}

	makeIncomingCtx := func() context.Context {
		md := metadata.Pairs(
			"traceparent", traceparent,
			"authorization", "Bearer eyJhbGciOiJIUzI1NiJ9.supersecretjwt.sig",
			"cookie", "session=top-secret-cookie-value",
		)
		return metadata.NewIncomingContext(context.Background(), md)
	}

	leakedTokens := []string{
		"buyer.secret@nexusmart.vn",
		"seller.secret@nexusmart.vn",
		"0901234567",
		"0987654321",
		"SuperSecretPass!99",
		"654321",
		"19036123456789",
		"99887766554433",
		"123 Nguyen Hue District 1",
		"supersecretjwt",
		"top-secret-cookie-value",
		"dbSuperSecretPwd",
		"inlineBearerSecretToken",
	}

	cases := []struct {
		name      string
		method    string
		handlerFn grpc.UnaryHandler
		wantCode  string
		wantLevel string
		wantErr   bool
	}{
		{
			name:   "ok_rpc",
			method: "/nexusmart.order.v1.OrderService/CreateCheckout",
			handlerFn: func(_ context.Context, _ any) (any, error) {
				return respPayload, nil
			},
			wantCode:  "OK",
			wantLevel: "INFO",
		},
		{
			name:   "client_error_does_not_leak_pii_from_message",
			method: "/nexusmart.user.v1.UserService/Login",
			handlerFn: func(_ context.Context, _ any) (any, error) {
				return nil, status.Error(codes.InvalidArgument, "invalid login for buyer.secret@nexusmart.vn phone 0901234567")
			},
			wantCode:  "InvalidArgument",
			wantLevel: "WARN",
			wantErr:   true,
		},
		{
			name:   "internal_error_does_not_leak_dsn_or_bearer",
			method: "/nexusmart.order.v1.OrderService/GetOrder",
			handlerFn: func(_ context.Context, _ any) (any, error) {
				return nil, status.Error(codes.Internal, "postgres://user:dbSuperSecretPwd@pg:5432/db failed with Bearer inlineBearerSecretToken")
			},
			wantCode:  "Internal",
			wantLevel: "ERROR",
			wantErr:   true,
		},
	}

	traceInt := telemetry.GRPCUnaryServerInterceptor("order")

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			log := New("order", WithWriter(&buf), WithLevel(slog.LevelDebug))
			logInt := GRPCUnaryServerInterceptor(log)

			info := &grpc.UnaryServerInfo{FullMethod: tc.method}
			_, callErr := traceInt(makeIncomingCtx(), reqPayload, info, func(ctx context.Context, req any) (any, error) {
				return logInt(ctx, req, info, tc.handlerFn)
			})
			if (callErr != nil) != tc.wantErr {
				t.Fatalf("callErr = %v, wantErr = %v", callErr, tc.wantErr)
			}

			raw := strings.TrimSpace(buf.String())
			lines := strings.Split(raw, "\n")
			if len(lines) != 1 {
				t.Fatalf("expected exactly 1 JSON log line, got %d:\n%s", len(lines), raw)
			}

			for _, secret := range leakedTokens {
				if strings.Contains(raw, secret) {
					t.Fatalf("log line leaked sensitive/PII value %q: %s", secret, raw)
				}
			}

			var parsed map[string]any
			if err := json.Unmarshal([]byte(lines[0]), &parsed); err != nil {
				t.Fatalf("invalid JSON log line: %v\nraw=%s", err, lines[0])
			}

			if parsed["service"] != "order" {
				t.Fatalf("service = %v, want order", parsed["service"])
			}
			if parsed["msg"] != "grpc request" {
				t.Fatalf("msg = %v, want grpc request", parsed["msg"])
			}
			if parsed["level"] != tc.wantLevel {
				t.Fatalf("level = %v, want %s", parsed["level"], tc.wantLevel)
			}
			if parsed["method"] != tc.method {
				t.Fatalf("method = %v, want %s", parsed["method"], tc.method)
			}
			if parsed["code"] != tc.wantCode {
				t.Fatalf("code = %v, want %s", parsed["code"], tc.wantCode)
			}
			if parsed["trace_id"] != traceID {
				t.Fatalf("trace_id = %v, want %s", parsed["trace_id"], traceID)
			}
			spanID, _ := parsed["span_id"].(string)
			if spanID == "" || spanID == "00f067aa0ba902b7" {
				t.Fatalf("expected fresh child span_id, got %q", spanID)
			}
			if dur, ok := parsed["duration_ms"].(float64); !ok || dur < 0 {
				t.Fatalf("duration_ms = %v (%T), want non-negative number", parsed["duration_ms"], parsed["duration_ms"])
			}
		})
	}
}

func TestGRPCUnaryServerInterceptorNilLoggerNoPanic(t *testing.T) {
	logInt := GRPCUnaryServerInterceptor(nil)
	resp, err := logInt(
		context.Background(),
		"req",
		&grpc.UnaryServerInfo{FullMethod: "/nexusmart.test.v1.Svc/Ping"},
		func(_ context.Context, _ any) (any, error) { return "pong", nil },
	)
	if err != nil || resp != "pong" {
		t.Fatalf("got (%v, %v), want (pong, nil)", resp, err)
	}
}
