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

func TestW3B2AdminAuditSkipsNewUserSelfAuditedRoutesAndMapsNewTargets(t *testing.T) {
	authMgr, err := auth.NewManager("w3b2-audit-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	adminTok, _, _, _ := authMgr.SignAccessToken("super-1", "super_admin", "")
	log := logger.New("w3b2-audit-test", logger.WithWriter(io.Discard))
	stub := &stubAuditUserClient{}

	r := chi.NewRouter()
	r.Use(Auth(authMgr, nil, log, true))
	r.Use(AdminAudit(AdminAuditConfig{
		UserClient: stub,
		Logger:     log,
	}))

	// 4 new user-service self-audited routes MUST be skipped by gateway middleware.
	r.Post("/api/v1/admin/staff", func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteJSON(w, http.StatusCreated, map[string]any{"user": map[string]any{"id": "staff-1"}, "password_reset_sent": true})
	})
	r.Post("/api/v1/admin/shops/{id}/suspend", func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteJSON(w, http.StatusCreated, map[string]any{"approval": map[string]any{"id": "appr-1"}})
	})
	r.Post("/api/v1/admin/shops/{id}/unsuspend", func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"shop": map[string]any{"id": "shop-1"}})
	})
	r.Post("/api/v1/admin/approvals/shop/{id}/decide", func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"approval": map[string]any{"id": "appr-1"}})
	})

	// Non-user mutations MUST be audited by gateway middleware with clean target_type/target_id and unredacted preview metadata.
	r.Put("/api/v1/admin/payment-methods/{method}", func(w http.ResponseWriter, req *http.Request) {
		var body map[string]any
		_ = httpx.DecodeJSON(req, &body)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"method": map[string]any{
				"method":                 chi.URLParam(req, "method"),
				"enabled":                true,
				"min_order_vnd":          int64(10000),
				"max_order_vnd":          int64(5000000),
				"expiry_minutes":         15,
				"credentials_configured": true,
				"credential_preview":     "***1234",
				"version":                2,
			},
		})
	})
	r.Post("/api/v1/admin/approvals/refund/{id}/decide", func(w http.ResponseWriter, req *http.Request) {
		var body map[string]any
		_ = httpx.DecodeJSON(req, &body)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"refund": map[string]any{
				"refund_id": chi.URLParam(req, "id"),
				"status":    "PENDING",
			},
		})
	})

	do := func(method, path, body string, wantCode int) {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+adminTok)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != wantCode {
			t.Fatalf("%s %s: want %d, got %d (%s)", method, path, wantCode, rec.Code, rec.Body.String())
		}
	}

	do(http.MethodPost, "/api/v1/admin/staff", `{"full_name":"Ops","email":"ops@example.com","role":"ops"}`, http.StatusCreated)
	do(http.MethodPost, "/api/v1/admin/shops/shop-1/suspend", `{"reason":"policy violation"}`, http.StatusCreated)
	do(http.MethodPost, "/api/v1/admin/shops/shop-1/unsuspend", `{"reason":"resolved"}`, http.StatusOK)
	do(http.MethodPost, "/api/v1/admin/approvals/shop/appr-1/decide", `{"decision":"APPROVE","note":"confirmed"}`, http.StatusOK)

	do(http.MethodPut, "/api/v1/admin/payment-methods/ZALOPAY", `{"enabled":true,"min_order_vnd":10000,"max_order_vnd":5000000,"expiry_minutes":15,"expected_version":1}`, http.StatusOK)
	do(http.MethodPost, "/api/v1/admin/approvals/refund/ref-55/decide", `{"approve":true,"note":"verified"}`, http.StatusOK)

	stub.mu.Lock()
	reqs := append([]*userv1.WriteAuditLogRequest(nil), stub.reqs...)
	stub.mu.Unlock()

	if len(reqs) != 2 {
		t.Fatalf("expected exactly 2 gateway audit logs (4 user self-audited routes skipped), got %d: %+v", len(reqs), reqs)
	}

	pmAudit := reqs[0]
	if pmAudit.GetTargetType() != "payment_method_config" || pmAudit.GetTargetId() != "ZALOPAY" {
		t.Fatalf("payment-methods audit target = (%q, %q), want (payment_method_config, ZALOPAY)", pmAudit.GetTargetType(), pmAudit.GetTargetId())
	}
	if !strings.Contains(pmAudit.GetAfterJson(), `"credentials_configured":true`) || !strings.Contains(pmAudit.GetAfterJson(), `"credential_preview":"***1234"`) {
		t.Fatalf("payment-methods audit should preserve masked credential metadata: %s", pmAudit.GetAfterJson())
	}

	refAudit := reqs[1]
	if refAudit.GetTargetType() != "refund" || refAudit.GetTargetId() != "ref-55" {
		t.Fatalf("refund approval audit target = (%q, %q), want (refund, ref-55)", refAudit.GetTargetType(), refAudit.GetTargetId())
	}
}

func TestW3B2RequireCapabilityAndSetAuditBefore(t *testing.T) {
	authMgr, err := auth.NewManager("w3b2-cap-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	log := logger.New("w3b2-cap-test", logger.WithWriter(io.Discard))

	// 1. RequireCapability: enforces capabilities.json (including F9-bis coins.credit) and rejects service tokens.
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mwCoins := RequireCapability(auth.CapCoinsCredit)(next)

	// Unauthenticated -> 401.
	rec := httptest.NewRecorder()
	mwCoins.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/admin/coins/credit", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated RequireCapability: want 401, got %d", rec.Code)
	}

	// F9-bis: cskh, ops, moderator, buyer, seller MUST get 403 on coins.credit.
	for _, denied := range []string{auth.RoleCSKH, auth.RoleOps, auth.RoleModerator, auth.RoleBuyer, auth.RoleSeller} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/coins/credit", nil)
		req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{Role: denied}))
		rec = httptest.NewRecorder()
		mwCoins.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("role=%s on coins.credit: want 403, got %d", denied, rec.Code)
		}
	}

	// Service token MUST get 403 even with Role == "admin".
	svcReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/coins/credit", nil)
	svcReq = svcReq.WithContext(auth.WithClaims(svcReq.Context(), &auth.Claims{Role: auth.ServiceRole, Service: "order-service"}))
	rec = httptest.NewRecorder()
	mwCoins.ServeHTTP(rec, svcReq)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("service token on RequireCapability: want 403, got %d", rec.Code)
	}

	// super_admin, admin, accountant MUST get 200 on coins.credit.
	for _, allowed := range []string{auth.RoleSuperAdmin, auth.RoleAdmin, auth.RoleAccountant} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/coins/credit", nil)
		req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{Role: allowed}))
		rec = httptest.NewRecorder()
		mwCoins.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("role=%s on coins.credit: want 200, got %d", allowed, rec.Code)
		}
	}

	// 2. SetAuditBefore records sanitized before_json while redacting secrets.
	stub := &stubAuditUserClient{}
	opsTok, _, _, _ := authMgr.SignAccessToken("ops-1", auth.RoleOps, "")
	r := chi.NewRouter()
	r.Use(Auth(authMgr, nil, log, true))
	r.Use(AdminAudit(AdminAuditConfig{UserClient: stub, Logger: log}))
	r.Put("/api/v1/admin/payment-methods/{method}", func(w http.ResponseWriter, req *http.Request) {
		SetAuditBefore(req.Context(), map[string]any{
			"enabled":                true,
			"version":                1,
			"credential_preview":     "***1234",
			"credentials_configured": true,
			"secret_key":             "raw-secret-must-redact",
		})
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"method": map[string]any{"enabled": false, "version": 2}})
	})

	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/payment-methods/ZALOPAY", strings.NewReader(`{"enabled":false,"expected_version":1}`))
	req.Header.Set("Authorization", "Bearer "+opsTok)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT payment-methods: want 200, got %d", rec.Code)
	}
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if len(stub.reqs) != 1 {
		t.Fatalf("expected 1 audit record, got %d", len(stub.reqs))
	}
	before := stub.reqs[0].GetBeforeJson()
	if !strings.Contains(before, `"version":1`) || !strings.Contains(before, `"credential_preview":"***1234"`) || strings.Contains(before, "raw-secret-must-redact") || !strings.Contains(before, `"secret_key":"[REDACTED]"`) {
		t.Fatalf("unexpected sanitized before_json: %s", before)
	}
}
