package metrics

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestHTTPAndGRPCREDMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	red := NewRED("gateway", reg)

	r := chi.NewRouter()
	r.Use(red.HTTPMiddleware())
	r.Get("/api/v1/orders/{id}", func(w http.ResponseWriter, r *http.Request) {
		if chi.URLParam(r, "id") == "boom" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/orders/ord-1", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/orders/boom", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", rr.Code)
	}

	if got := testutil.ToFloat64(red.httpRequestsTotal.WithLabelValues("gateway", "GET", "/api/v1/orders/{id}", "200")); got != 1 {
		t.Fatalf("http_requests_total 200 = %v, want 1", got)
	}
	if got := testutil.ToFloat64(red.httpErrorsTotal.WithLabelValues("gateway", "GET", "/api/v1/orders/{id}", "500")); got != 1 {
		t.Fatalf("http_errors_total 500 = %v, want 1", got)
	}

	// Unmatched 404 routes must map to "unmatched" to avoid label cardinality explosion
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/non-existent/random-path-999", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rr.Code)
	}
	if got := testutil.ToFloat64(red.httpRequestsTotal.WithLabelValues("gateway", "GET", "unmatched", "404")); got != 1 {
		t.Fatalf("http_requests_total unmatched 404 = %v, want 1", got)
	}

	// gRPC RED interceptor
	interceptor := red.GRPCUnaryServerInterceptor()
	_, err := interceptor(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/nexusmart.order.v1.OrderService/GetOrder"}, func(context.Context, any) (any, error) {
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("unexpected grpc err: %v", err)
	}
	_, _ = interceptor(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/nexusmart.order.v1.OrderService/GetOrder"}, func(context.Context, any) (any, error) {
		return nil, status.Error(codes.NotFound, "not found")
	})

	if got := testutil.ToFloat64(red.grpcRequestsTotal.WithLabelValues("gateway", "/nexusmart.order.v1.OrderService/GetOrder", "OK")); got != 1 {
		t.Fatalf("grpc_requests_total OK = %v, want 1", got)
	}
	if got := testutil.ToFloat64(red.grpcErrorsTotal.WithLabelValues("gateway", "/nexusmart.order.v1.OrderService/GetOrder", "NotFound")); got != 1 {
		t.Fatalf("grpc_errors_total NotFound = %v, want 1", got)
	}

	// Ops handler: /metrics, /healthz, /readyz
	ops := NewOpsHandler("gateway", func(context.Context) error { return nil }, reg)
	rr = httptest.NewRecorder()
	ops.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"status":"ok"`) {
		t.Fatalf("/healthz unexpected: %d %s", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	ops.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "http_requests_total") {
		t.Fatalf("/metrics unexpected: %d", rr.Code)
	}

	notReadyOps := NewOpsHandler("gateway", func(context.Context) error { return errors.New("db down") }, reg)
	rr = httptest.NewRecorder()
	notReadyOps.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("/readyz unhealthy code=%d, want 503", rr.Code)
	}
}

func TestHTTPMiddlewareSupportsSSEFlush(t *testing.T) {
	reg := prometheus.NewRegistry()
	red := NewRED("gateway", reg)

	r := chi.NewRouter()
	r.Use(red.HTTPMiddleware())
	r.Get("/api/v1/payments/{id}/events", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: paid\n\n"))
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Errorf("ResponseController.Flush failed: %v", err)
		}
		if flusher, ok := w.(http.Flusher); !ok {
			t.Errorf("ResponseWriter does not implement http.Flusher")
		} else {
			flusher.Flush()
		}
	})

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/payments/p1/events", nil))
	if !rr.Flushed {
		t.Fatalf("expected ResponseRecorder to be flushed")
	}
}
