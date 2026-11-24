package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/gateway/internal/httpx"
)

func TestW3BAdminAuditTargetTypesAndIDExtraction(t *testing.T) {
	authMgr, err := auth.NewManager("w3b-audit-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	adminTok, _, _, _ := authMgr.SignAccessToken("admin-w3b", "admin", "")
	log := logger.New("w3b-audit-test", logger.WithWriter(io.Discard))
	stub := &stubAuditUserClient{}

	r := chi.NewRouter()
	r.Use(Auth(authMgr, nil, log, true))
	r.Use(AdminAudit(AdminAuditConfig{
		UserClient: stub,
		Logger:     log,
	}))

	r.Post("/api/v1/admin/banned-keywords", func(w http.ResponseWriter, req *http.Request) {
		var body map[string]any
		_ = httpx.DecodeJSON(req, &body)
		httpx.WriteJSON(w, http.StatusCreated, map[string]any{
			"keyword": map[string]any{"id": "kw-101", "term": "hang gia", "action": "BLOCK"},
		})
	})
	r.Put("/api/v1/admin/banned-keywords/{id}", func(w http.ResponseWriter, req *http.Request) {
		var body map[string]any
		_ = httpx.DecodeJSON(req, &body)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"keyword": map[string]any{"id": chi.URLParam(req, "id"), "term": "hang gia", "action": "REVIEW"},
		})
	})
	r.Delete("/api/v1/admin/banned-keywords/{id}", func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	r.Post("/api/v1/admin/banned-keywords/preview", func(w http.ResponseWriter, req *http.Request) {
		var body map[string]any
		_ = httpx.DecodeJSON(req, &body)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"affected_count": 3, "samples": []any{}})
	})
	r.Post("/api/v1/admin/banned-keywords/rescan", func(w http.ResponseWriter, req *http.Request) {
		var body map[string]any
		_ = httpx.DecodeOptionalJSON(req, &body)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"scanned_count": 10, "blocked_count": 1, "flagged_count": 2})
	})
	r.Post("/api/v1/admin/reconciliation/cod/import", func(w http.ResponseWriter, req *http.Request) {
		_, _ = io.ReadAll(req.Body)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"run":   map[string]any{"run_id": "run-cod-88", "run_type": "cod_file"},
			"items": []any{},
		})
	})
	r.Post("/api/v1/admin/reconciliation/queue/{id}/resolve", func(w http.ResponseWriter, req *http.Request) {
		var body map[string]any
		_ = httpx.DecodeJSON(req, &body)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"item": map[string]any{"id": chi.URLParam(req, "id")}})
	})

	do := func(method, path, ct, body string, wantCode int) {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+adminTok)
		if ct != "" {
			req.Header.Set("Content-Type", ct)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != wantCode {
			t.Fatalf("%s %s: want %d, got %d (%s)", method, path, wantCode, rec.Code, rec.Body.String())
		}
	}

	do(http.MethodPost, "/api/v1/admin/banned-keywords", "application/json", `{"term":"hang gia","action":"BLOCK"}`, http.StatusCreated)
	do(http.MethodPut, "/api/v1/admin/banned-keywords/kw-101", "application/json", `{"action":"REVIEW"}`, http.StatusOK)
	do(http.MethodDelete, "/api/v1/admin/banned-keywords/kw-101", "", "", http.StatusOK)
	do(http.MethodPost, "/api/v1/admin/banned-keywords/preview", "application/json", `{"term":"hang gia"}`, http.StatusOK)
	do(http.MethodPost, "/api/v1/admin/banned-keywords/rescan", "application/json", `{"dry_run":true}`, http.StatusOK)
	do(http.MethodPost, "/api/v1/admin/reconciliation/cod/import", "text/csv", "tracking_code,cod_amount_vnd,delivered_at\nTRK-1,100000,2026-11-24\n", http.StatusOK)
	do(http.MethodPost, "/api/v1/admin/reconciliation/queue/rq-7/resolve", "application/json", `{"action":"SUSPEND"}`, http.StatusOK)

	stub.mu.Lock()
	reqs := append([]*userv1.WriteAuditLogRequest(nil), stub.reqs...)
	stub.mu.Unlock()

	want := []struct {
		action     string
		targetType string
		targetID   string
	}{
		{"POST /api/v1/admin/banned-keywords", "banned_keyword", "kw-101"},
		{"PUT /api/v1/admin/banned-keywords/{id}", "banned_keyword", "kw-101"},
		{"DELETE /api/v1/admin/banned-keywords/{id}", "banned_keyword", "kw-101"},
		{"POST /api/v1/admin/banned-keywords/preview", "banned_keyword", "/api/v1/admin/banned-keywords/preview"},
		{"POST /api/v1/admin/banned-keywords/rescan", "banned_keyword", "/api/v1/admin/banned-keywords/rescan"},
		{"POST /api/v1/admin/reconciliation/cod/import", "reconciliation_run", "run-cod-88"},
		{"POST /api/v1/admin/reconciliation/queue/{id}/resolve", "reconciliation_item", "rq-7"},
	}

	if len(reqs) != len(want) {
		t.Fatalf("expected %d audit logs, got %d", len(want), len(reqs))
	}
	for i, w := range want {
		got := reqs[i]
		if got.GetAction() != w.action || got.GetTargetType() != w.targetType || got.GetTargetId() != w.targetID {
			t.Fatalf("audit[%d] = (action=%q, type=%q, id=%q), want (%q, %q, %q)",
				i, got.GetAction(), got.GetTargetType(), got.GetTargetId(),
				w.action, w.targetType, w.targetID)
		}
	}
}

func TestW3BAdminAuditSummaryOverrideForLargeResponse(t *testing.T) {
	authMgr, err := auth.NewManager("w3b-audit-summary-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	adminTok, _, _, _ := authMgr.SignAccessToken("admin-w3b", "admin", "")
	log := logger.New("w3b-audit-summary-test", logger.WithWriter(io.Discard))
	stub := &stubAuditUserClient{}

	r := chi.NewRouter()
	r.Use(Auth(authMgr, nil, log, true))
	r.Use(AdminAudit(AdminAuditConfig{
		UserClient: stub,
		Logger:     log,
	}))

	r.Post("/api/v1/admin/reconciliation/cod/import", func(w http.ResponseWriter, req *http.Request) {
		_, _ = io.ReadAll(req.Body)
		SetAuditSummary(req.Context(), "run-large-99", map[string]any{
			"run_id":            "run-large-99",
			"total_rows":        5000,
			"matched_count":     4990,
			"total_matched_vnd": int64(99800000),
			"api_token":         "must-be-redacted",
		})
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"run":   map[string]any{"run_id": "run-large-99"},
			"items": strings.Repeat("x", auditResponseCaptureLimit+2048),
		})
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/reconciliation/cod/import", strings.NewReader("raw-csv-secret-line"))
	req.Header.Set("Authorization", "Bearer "+adminTok)
	req.Header.Set("Content-Type", "text/csv")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || len(rec.Body.Bytes()) <= auditResponseCaptureLimit {
		t.Fatalf("unexpected response: code=%d len=%d", rec.Code, len(rec.Body.Bytes()))
	}

	stub.mu.Lock()
	defer stub.mu.Unlock()
	if len(stub.reqs) != 1 {
		t.Fatalf("expected 1 audit log, got %d", len(stub.reqs))
	}
	got := stub.reqs[0]
	if got.GetTargetId() != "run-large-99" {
		t.Fatalf("target_id = %q, want run-large-99", got.GetTargetId())
	}
	if !strings.Contains(got.GetAfterJson(), `"total_rows":5000`) || !strings.Contains(got.GetAfterJson(), `"total_matched_vnd":99800000`) {
		t.Fatalf("after_json missing summary fields: %s", got.GetAfterJson())
	}
	if strings.Contains(got.GetAfterJson(), "must-be-redacted") || strings.Contains(got.GetAfterJson(), "raw-csv-secret-line") {
		t.Fatalf("after_json leaked sensitive/raw data: %s", got.GetAfterJson())
	}
}
