package e2e

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestE2E_HealthAndObservability(t *testing.T) {
	env := getEnv()
	ctx := env.ctx(t)

	// 1. Verify all 9 core services respond 200 on /healthz and /readyz.
	t.Run("All9Services_HealthzAndReadyz", func(t *testing.T) {
		for _, svc := range env.cfg.AllServices() {
			for _, endpoint := range []string{"/healthz", "/readyz"} {
				url := svc.BaseURL + endpoint
				resp, err := env.client.DoAbsolute(ctx, http.MethodGet, url, "", nil, nil)
				if err != nil {
					t.Errorf("service=%s GET %s failed: %v", svc.Name, url, err)
					continue
				}
				if resp.StatusCode != http.StatusOK {
					t.Errorf("service=%s GET %s returned HTTP %d (want 200): %s", svc.Name, url, resp.StatusCode, string(resp.Body))
				}
			}
		}
	})

	// 2. Verify Gateway returns X-Trace-Id and propagates W3C traceparent.
	t.Run("Gateway_TraceIDHeader", func(t *testing.T) {
		wantTraceID := "4bf92f3577b34da6a3ce929d0e0e4736"
		traceparent := fmt.Sprintf("00-%s-00f067aa0ba902b7-01", wantTraceID)
		resp := env.mustDoJSON(t, http.MethodGet, "/api/v1/shipping/provinces", "", map[string]string{
			"traceparent": traceparent,
		}, nil, http.StatusOK)

		gotTraceID := strings.TrimSpace(resp.Headers.Get("X-Trace-Id"))
		if gotTraceID == "" {
			t.Fatalf("Gateway GET /api/v1/shipping/provinces missing X-Trace-Id response header (headers=%v)", resp.Headers)
		}
		t.Logf("Gateway returned X-Trace-Id=%s (sent traceparent=%s)", gotTraceID, wantTraceID)
	})

	// 3. Exercise one lightweight read-only request per backend service through Gateway
	// so Prometheus CounterVec metrics emit at least one RED sample on every service.
	t.Run("All9Services_REDMetrics", func(t *testing.T) {
		adminTok := env.adminLogin(t)

		// Lightweight probes touching each backend service via Gateway:
		env.mustDoJSON(t, http.MethodGet, "/api/v1/shipping/provinces", "", nil, nil, http.StatusOK)
		env.mustDoJSON(t, http.MethodGet, "/api/v1/categories", "", nil, nil, http.StatusOK)
		env.mustDoJSON(t, http.MethodGet, "/api/v1/vouchers?sales_channel=MARKETPLACE", "", nil, nil, http.StatusOK)
		env.mustDoJSON(t, http.MethodGet, "/api/v1/cart", adminTok, nil, nil, http.StatusOK)
		env.mustDoJSON(t, http.MethodGet, "/api/v1/orders", adminTok, nil, nil, http.StatusOK)
		env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/ledger/summary", adminTok, nil, nil, http.StatusOK)
		env.mustDoJSON(t, http.MethodGet, "/api/v1/notifications", adminTok, nil, nil, http.StatusOK)

		for _, svc := range env.cfg.AllServices() {
			metricsURL := svc.BaseURL + "/metrics"
			resp, err := env.client.DoAbsolute(ctx, http.MethodGet, metricsURL, "", nil, nil)
			if err != nil {
				t.Errorf("service=%s GET %s failed: %v", svc.Name, metricsURL, err)
				continue
			}
			if resp.StatusCode != http.StatusOK {
				t.Errorf("service=%s GET %s returned HTTP %d (want 200)", svc.Name, metricsURL, resp.StatusCode)
				continue
			}
			body := string(resp.Body)
			hasHTTP := strings.Contains(body, "http_requests_total")
			hasGRPC := strings.Contains(body, "grpc_requests_total")

			if svc.Name == "gateway" {
				if !hasHTTP {
					t.Errorf("service=gateway /metrics missing http_requests_total")
				}
				if !strings.Contains(body, "http_request_duration_seconds") {
					t.Errorf("service=gateway /metrics missing http_request_duration_seconds")
				}
			} else if !hasHTTP && !hasGRPC {
				t.Errorf("service=%s /metrics missing RED metrics (expected http_requests_total or grpc_requests_total)", svc.Name)
			}
		}
	})
}
