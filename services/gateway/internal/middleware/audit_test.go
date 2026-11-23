package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type stubAuditUserClient struct {
	userv1.UserServiceClient
	mu       sync.Mutex
	failN    int
	attempts int
	reqs     []*userv1.WriteAuditLogRequest
	doneCh   chan struct{}
	targetN  int
}

func (s *stubAuditUserClient) WriteAuditLog(_ context.Context, in *userv1.WriteAuditLogRequest, _ ...grpc.CallOption) (*userv1.WriteAuditLogResponse, error) {
	s.mu.Lock()
	s.attempts++
	attempt := s.attempts
	s.reqs = append(s.reqs, in)
	shouldFail := s.failN < 0 || attempt <= s.failN
	reached := s.targetN > 0 && attempt == s.targetN
	s.mu.Unlock()

	if reached && s.doneCh != nil {
		defer close(s.doneCh)
	}
	if shouldFail {
		return nil, status.Error(codes.Unavailable, "user service unavailable")
	}
	return &userv1.WriteAuditLogResponse{
		AuditLog: &userv1.AuditLog{Id: "audit-ok"},
	}, nil
}

func TestAdminAuditRetriesAndDoesNotFailResponse(t *testing.T) {
	shutdown, err := telemetry.Init(context.Background(), telemetry.Config{ServiceName: "audit-test"})
	if err != nil {
		t.Fatalf("telemetry.Init: %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	authMgr, err := auth.NewManager("audit-retry-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	adminTok, _, _, _ := authMgr.SignAccessToken("admin-1", "admin", "")
	tp := NewTrustedProxies("10.0.0.0/8", nil)

	t.Run("TransientFailureRetriesAndSucceeds", func(t *testing.T) {
		var logBuf syncBuffer
		log := logger.New("audit-test", logger.WithWriter(&logBuf))
		stub := &stubAuditUserClient{
			failN:   1, // fail attempt 1, succeed attempt 2
			targetN: 2,
			doneCh:  make(chan struct{}),
		}

		r := chi.NewRouter()
		r.Use(telemetry.HTTPMiddleware("audit-test"))
		r.Use(Auth(authMgr, nil, log, true))
		r.Use(AdminAudit(AdminAuditConfig{
			UserClient:          stub,
			TrustedProxies:      tp,
			Logger:              log,
			MaxAttempts:         3,
			FirstAttemptTimeout: 200 * time.Millisecond,
			RetryTimeout:        200 * time.Millisecond,
			RetryBackoff:        5 * time.Millisecond,
		}))
		r.Post("/api/v1/admin/products/{id}/review", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"product":{"id":"prod-7"}}`))
		})

		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/products/prod-7/review", nil)
		req.RemoteAddr = "10.0.0.9:1234"
		req.Header.Set("X-Forwarded-For", "198.51.100.77")
		req.Header.Set("Authorization", "Bearer "+adminTok)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("audit failure must not alter HTTP 200 response, got %d", rec.Code)
		}

		select {
		case <-stub.doneCh:
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for audit retry")
		}

		stub.mu.Lock()
		gotAttempts := stub.attempts
		gotIP := stub.reqs[0].GetIpAddress()
		stub.mu.Unlock()

		if gotAttempts != 2 {
			t.Fatalf("expected 2 attempts (1 fail + 1 retry success), got %d", gotAttempts)
		}
		if gotIP != "198.51.100.77" {
			t.Fatalf("expected trusted proxy ClientIP 198.51.100.77, got %q", gotIP)
		}
	})

	t.Run("ExhaustedRetriesLogsErrorWithTraceID", func(t *testing.T) {
		var logBuf syncBuffer
		log := logger.New("audit-test", logger.WithWriter(&logBuf))
		stub := &stubAuditUserClient{
			failN:   -1, // always fail
			targetN: 3,
			doneCh:  make(chan struct{}),
		}

		r := chi.NewRouter()
		r.Use(telemetry.HTTPMiddleware("audit-test"))
		r.Use(Auth(authMgr, nil, log, true))
		r.Use(AdminAudit(AdminAuditConfig{
			UserClient:          stub,
			TrustedProxies:      tp,
			Logger:              log,
			MaxAttempts:         3,
			FirstAttemptTimeout: 200 * time.Millisecond,
			RetryTimeout:        200 * time.Millisecond,
			RetryBackoff:        5 * time.Millisecond,
		}))
		r.Delete("/api/v1/admin/categories/{id}", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		})

		req := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/categories/cat-9", nil)
		req.RemoteAddr = "198.51.100.3:8080"
		req.Header.Set("Authorization", "Bearer "+adminTok)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("exhausted audit failure must not alter HTTP 200 response, got %d", rec.Code)
		}
		traceID := rec.Header().Get("X-Trace-Id")

		select {
		case <-stub.doneCh:
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for bounded retries")
		}
		// Give logger a moment after WriteAuditLog returns on the 3rd attempt.
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			if strings.Contains(logBuf.String(), "after retries") {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}

		stub.mu.Lock()
		gotAttempts := stub.attempts
		stub.mu.Unlock()
		if gotAttempts != 3 {
			t.Fatalf("expected bounded 3 attempts, got %d", gotAttempts)
		}

		lines := strings.Split(strings.TrimSpace(logBuf.String()), "\n")
		var lastEntry map[string]any
		if err := json.Unmarshal([]byte(lines[len(lines)-1]), &lastEntry); err != nil {
			t.Fatalf("decode final error log %q: %v", logBuf.String(), err)
		}
		if lastEntry["level"] != "ERROR" || lastEntry["trace_id"] != traceID {
			t.Fatalf("expected final ERROR log with trace_id=%q, got %s", traceID, lines[len(lines)-1])
		}
	})

	t.Run("SkipsNon2xxResponses", func(t *testing.T) {
		stub := &stubAuditUserClient{}
		r := chi.NewRouter()
		r.Use(Auth(authMgr, nil, nil, true))
		r.Use(AdminAudit(AdminAuditConfig{UserClient: stub}))
		r.Post("/api/v1/admin/products/{id}/review", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
		})

		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/products/prod-1/review", nil)
		req.Header.Set("Authorization", "Bearer "+adminTok)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
		stub.mu.Lock()
		n := stub.attempts
		stub.mu.Unlock()
		if n != 0 {
			t.Fatalf("4xx response must not write audit log, got %d calls", n)
		}
	})
}
