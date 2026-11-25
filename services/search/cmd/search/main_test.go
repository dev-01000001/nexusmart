package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nexusmart/core/pkg/common/metrics"
	"github.com/prometheus/client_golang/prometheus"
)

func TestReadyz_HardDepsOnly_NotFailingWhenMeiliDown(t *testing.T) {
	okPing := func(context.Context) error { return nil }
	failPing := func(context.Context) error { return errors.New("connection refused") }

	t.Run("200 when postgres, redis, nats healthy even without Meilisearch", func(t *testing.T) {
		readyFn := buildReadyCheck(okPing, okPing, func() bool { return true })
		h := metrics.NewOpsHandler("search", readyFn, prometheus.NewRegistry())

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /readyz status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("503 when search-postgres fails", func(t *testing.T) {
		readyFn := buildReadyCheck(failPing, okPing, func() bool { return true })
		h := metrics.NewOpsHandler("search", readyFn, prometheus.NewRegistry())

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("GET /readyz status = %d, want 503", rec.Code)
		}
	})

	t.Run("503 when redis fails", func(t *testing.T) {
		readyFn := buildReadyCheck(okPing, failPing, func() bool { return true })
		h := metrics.NewOpsHandler("search", readyFn, prometheus.NewRegistry())

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("GET /readyz status = %d, want 503", rec.Code)
		}
	})

	t.Run("503 when nats disconnected", func(t *testing.T) {
		readyFn := buildReadyCheck(okPing, okPing, func() bool { return false })
		h := metrics.NewOpsHandler("search", readyFn, prometheus.NewRegistry())

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("GET /readyz status = %d, want 503", rec.Code)
		}
	})
}
