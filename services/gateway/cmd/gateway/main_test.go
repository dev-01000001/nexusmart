package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nexusmart/core/pkg/common/metrics"
)

func TestGatewayMetricsRegistryExposesProcessAndGoCollectors(t *testing.T) {
	reg := newMetricsRegistry()
	_ = metrics.NewRED("gateway", reg)
	ops := metrics.NewOpsHandler("gateway", nil, reg)

	rec := httptest.NewRecorder()
	ops.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on /metrics, got %d", rec.Code)
	}
	body := rec.Body.String()
	for _, metricName := range []string{
		"process_cpu_seconds_total",
		"process_resident_memory_bytes",
		"go_goroutines",
	} {
		if !strings.Contains(body, metricName) {
			t.Fatalf("expected /metrics to expose %s", metricName)
		}
	}
}
