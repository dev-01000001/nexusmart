package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestMapGRPCError(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"InvalidArgument", status.Error(codes.InvalidArgument, "bad input"), http.StatusBadRequest, "INVALID_ARGUMENT"},
		{"Unauthenticated", status.Error(codes.Unauthenticated, "no auth"), http.StatusUnauthorized, "UNAUTHENTICATED"},
		{"PermissionDenied", status.Error(codes.PermissionDenied, "denied"), http.StatusForbidden, "PERMISSION_DENIED"},
		{"NotFound", status.Error(codes.NotFound, "missing"), http.StatusNotFound, "NOT_FOUND"},
		{"AlreadyExists", status.Error(codes.AlreadyExists, "dup"), http.StatusConflict, "ALREADY_EXISTS"},
		{"Aborted", status.Error(codes.Aborted, "conflict"), http.StatusConflict, "ABORTED"},
		{"FailedPrecondition", status.Error(codes.FailedPrecondition, "precond"), http.StatusConflict, "FAILED_PRECONDITION"},
		{"ResourceExhausted", status.Error(codes.ResourceExhausted, "limit"), http.StatusTooManyRequests, "RESOURCE_EXHAUSTED"},
		{"DeadlineExceeded", status.Error(codes.DeadlineExceeded, "timeout"), http.StatusGatewayTimeout, "DEADLINE_EXCEEDED"},
		{"Unavailable", status.Error(codes.Unavailable, "down"), http.StatusServiceUnavailable, "UNAVAILABLE"},
		{"Internal", status.Error(codes.Internal, "boom"), http.StatusInternalServerError, "INTERNAL_ERROR"},
		{"NonGRPC", errors.New("plain error"), http.StatusInternalServerError, "INTERNAL_ERROR"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotStatus, gotCode, _ := MapGRPCError(tc.err)
			if gotStatus != tc.wantStatus || gotCode != tc.wantCode {
				t.Fatalf("got (%d, %s), want (%d, %s)", gotStatus, gotCode, tc.wantStatus, tc.wantCode)
			}
		})
	}
}

func TestMapGRPCErrorMasksInternalErrors(t *testing.T) {
	masked := []struct {
		name string
		err  error
	}{
		{"Internal", status.Error(codes.Internal, "load cart from redis: dial tcp 172.20.0.3:6379: connection refused")},
		{"Unknown", status.Error(codes.Unknown, "pq: relation users does not exist")},
		{"DataLoss", status.Error(codes.DataLoss, "corrupt wal segment 00001")},
		{"NonGRPC", errors.New("raw internal panic detail")},
	}
	for _, tc := range masked {
		t.Run(tc.name, func(t *testing.T) {
			gotStatus, gotCode, gotMsg := MapGRPCError(tc.err)
			if gotStatus != http.StatusInternalServerError || gotCode != "INTERNAL_ERROR" {
				t.Fatalf("got (%d, %s), want (500, INTERNAL_ERROR)", gotStatus, gotCode)
			}
			if gotMsg != "internal server error" {
				t.Fatalf("internal detail leaked: got %q, want %q", gotMsg, "internal server error")
			}
		})
	}

	preserved := []struct {
		name    string
		err     error
		wantMsg string
	}{
		{"InvalidArgument", status.Error(codes.InvalidArgument, "email is invalid"), "email is invalid"},
		{"NotFound", status.Error(codes.NotFound, "product not found"), "product not found"},
		{"FailedPrecondition", status.Error(codes.FailedPrecondition, "insufficient stock"), "insufficient stock"},
		{"PermissionDenied", status.Error(codes.PermissionDenied, "admin role required"), "admin role required"},
		{"Unauthenticated", status.Error(codes.Unauthenticated, "invalid token"), "invalid token"},
	}
	for _, tc := range preserved {
		t.Run(tc.name, func(t *testing.T) {
			_, _, gotMsg := MapGRPCError(tc.err)
			if gotMsg != tc.wantMsg {
				t.Fatalf("client error message changed: got %q, want %q", gotMsg, tc.wantMsg)
			}
		})
	}
}

func TestWriteGRPCErrorIncludesTraceID(t *testing.T) {
	shutdown, err := telemetry.Init(context.Background(), telemetry.Config{ServiceName: "gateway-test"})
	if err != nil {
		t.Fatalf("telemetry.Init failed: %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	h := telemetry.HTTPMiddleware("gateway-test")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteGRPCError(w, r, status.Error(codes.FailedPrecondition, "insufficient stock"))
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/checkout", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", rec.Code)
	}

	var env ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal error envelope: %v", err)
	}
	if env.Error.Code != "FAILED_PRECONDITION" || env.Error.Message != "insufficient stock" {
		t.Fatalf("unexpected error body: %+v", env.Error)
	}
	if env.Error.TraceID == "" {
		t.Fatalf("expected non-empty trace_id in error envelope")
	}
	if rec.Header().Get("X-Trace-Id") != env.Error.TraceID {
		t.Fatalf("expected X-Trace-Id %q to match body trace_id %q", rec.Header().Get("X-Trace-Id"), env.Error.TraceID)
	}
}

func TestWriteGRPCErrorMasksInternalAndLogsOriginalWithTraceID(t *testing.T) {
	shutdown, err := telemetry.Init(context.Background(), telemetry.Config{ServiceName: "gateway-test"})
	if err != nil {
		t.Fatalf("telemetry.Init failed: %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	var logBuf bytes.Buffer
	log := logger.New("gateway-test", logger.WithWriter(&logBuf))

	rawErr := status.Error(codes.Internal, "load cart from redis: dial tcp 172.20.0.3:6379: connection refused")
	h := telemetry.HTTPMiddleware("gateway-test")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteGRPCError(w, r.WithContext(WithLogger(r.Context(), log)), rawErr)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/cart", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}

	var env ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal error envelope: %v", err)
	}
	if env.Error.Code != "INTERNAL_ERROR" || env.Error.Message != "internal server error" {
		t.Fatalf("expected masked internal server error, got %+v", env.Error)
	}
	if strings.Contains(rec.Body.String(), "redis") || strings.Contains(rec.Body.String(), "172.20.0.3") {
		t.Fatalf("response leaked internal error details: %s", rec.Body.String())
	}
	if env.Error.TraceID == "" {
		t.Fatalf("expected non-empty trace_id")
	}

	var logEntry map[string]any
	if err := json.Unmarshal(logBuf.Bytes(), &logEntry); err != nil {
		t.Fatalf("decode internal error log %q: %v", logBuf.String(), err)
	}
	if logEntry["trace_id"] != env.Error.TraceID {
		t.Fatalf("expected logged trace_id=%q, got %v", env.Error.TraceID, logEntry["trace_id"])
	}
	if !strings.Contains(logBuf.String(), "load cart from redis") {
		t.Fatalf("expected original gRPC error logged server-side, got %s", logBuf.String())
	}
}

func TestWriteGRPCErrorMasksUnavailableAndDeadlineExceeded(t *testing.T) {
	shutdown, err := telemetry.Init(context.Background(), telemetry.Config{ServiceName: "gateway-test"})
	if err != nil {
		t.Fatalf("telemetry.Init failed: %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
		wantMsg    string
		leakNeedle string
	}{
		{
			name:       "Unavailable",
			err:        status.Error(codes.Unavailable, `connection error: desc = "transport: Error while dialing: dial tcp 172.20.0.7:50057: connect: connection refused"`),
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "UNAVAILABLE",
			wantMsg:    "service temporarily unavailable",
			leakNeedle: "172.20.0.7",
		},
		{
			name:       "DeadlineExceeded",
			err:        status.Error(codes.DeadlineExceeded, "context deadline exceeded calling 172.20.0.5:50055"),
			wantStatus: http.StatusGatewayTimeout,
			wantCode:   "DEADLINE_EXCEEDED",
			wantMsg:    "upstream service timed out",
			leakNeedle: "172.20.0.5",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotStatus, gotCode, gotMsg := MapGRPCError(tc.err)
			if gotStatus != tc.wantStatus || gotCode != tc.wantCode || gotMsg != tc.wantMsg {
				t.Fatalf("MapGRPCError got (%d, %q, %q), want (%d, %q, %q)",
					gotStatus, gotCode, gotMsg, tc.wantStatus, tc.wantCode, tc.wantMsg)
			}

			var logBuf bytes.Buffer
			log := logger.New("gateway-test", logger.WithWriter(&logBuf))
			h := telemetry.HTTPMiddleware("gateway-test")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				WriteGRPCError(w, r.WithContext(WithLogger(r.Context(), log)), tc.err)
			}))

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/shipping/provinces", nil)
			h.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("expected %d, got %d", tc.wantStatus, rec.Code)
			}
			if strings.Contains(rec.Body.String(), tc.leakNeedle) {
				t.Fatalf("response leaked internal address %q: %s", tc.leakNeedle, rec.Body.String())
			}

			var env ErrorEnvelope
			if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
				t.Fatalf("unmarshal error envelope: %v", err)
			}
			if env.Error.Code != tc.wantCode || env.Error.Message != tc.wantMsg || env.Error.TraceID == "" {
				t.Fatalf("unexpected error envelope: %+v", env.Error)
			}

			var logEntry map[string]any
			if err := json.Unmarshal(logBuf.Bytes(), &logEntry); err != nil {
				t.Fatalf("expected original %s error logged server-side, got %q (%v)", tc.name, logBuf.String(), err)
			}
			if logEntry["trace_id"] != env.Error.TraceID || !strings.Contains(logBuf.String(), tc.leakNeedle) {
				t.Fatalf("expected log with trace_id=%q and original error %q, got %s", env.Error.TraceID, tc.leakNeedle, logBuf.String())
			}
		})
	}
}
