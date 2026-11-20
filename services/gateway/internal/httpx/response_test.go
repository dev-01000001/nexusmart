package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

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
