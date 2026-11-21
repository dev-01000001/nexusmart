package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nexusmart/core/pkg/common/metrics"
	"github.com/nexusmart/core/pkg/common/telemetry"
)

func TestUserOpsHandlerExposesCollectorsREDAndTraceMiddleware(t *testing.T) {
	shutdown, err := telemetry.Init(context.Background(), telemetry.Config{
		ServiceName: "user",
	})
	if err != nil {
		t.Fatalf("init telemetry: %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	reg := newMetricsRegistry()
	red := metrics.NewRED("user", reg)
	ops := newOpsHandler("user", nil, reg, red)

	// Verify trace propagation + X-Trace-Id response header + HTTP RED recording on /healthz.
	const incomingTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	healthReq := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	healthReq.Header.Set("traceparent", "00-"+incomingTraceID+"-00f067aa0ba902b7-01")
	healthRec := httptest.NewRecorder()
	ops.ServeHTTP(healthRec, healthReq)
	if healthRec.Code != http.StatusOK {
		t.Fatalf("expected 200 on /healthz, got %d", healthRec.Code)
	}
	if got := healthRec.Header().Get("X-Trace-Id"); got != incomingTraceID {
		t.Fatalf("expected X-Trace-Id %q, got %q", incomingTraceID, got)
	}

	// Verify /metrics exposes ProcessCollector, GoCollector, and HTTP RED metrics.
	metricsRec := httptest.NewRecorder()
	ops.ServeHTTP(metricsRec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if metricsRec.Code != http.StatusOK {
		t.Fatalf("expected 200 on /metrics, got %d", metricsRec.Code)
	}
	body := metricsRec.Body.String()
	for _, metricName := range []string{
		"process_cpu_seconds_total",
		"process_resident_memory_bytes",
		"go_goroutines",
		`http_requests_total{method="GET",route="/healthz",service="user",status="200"}`,
		`http_request_duration_seconds_count{method="GET",route="/healthz",service="user"}`,
	} {
		if !strings.Contains(body, metricName) {
			t.Fatalf("expected /metrics to expose %s", metricName)
		}
	}
}
