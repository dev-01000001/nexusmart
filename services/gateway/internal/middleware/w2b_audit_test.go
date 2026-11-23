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

func TestW2BAdminAuditRedactsOTPAndBankAccountAndResolvesTargets(t *testing.T) {
	authMgr, err := auth.NewManager("w2b-audit-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	adminTok, _, _, _ := authMgr.SignAccessToken("admin-w2b", "admin", "")

	log := logger.New("w2b-audit-test", logger.WithWriter(io.Discard))
	stub := &stubAuditUserClient{}

	r := chi.NewRouter()
	r.Use(Auth(authMgr, nil, log, true))
	r.Use(AdminAudit(AdminAuditConfig{
		UserClient: stub,
		Logger:     log,
	}))

	// 1. Self-audited by services/user: must be skipped by gateway middleware to avoid duplicate records.
	r.Post("/api/v1/admin/shops/{id}/bank-account/review", func(w http.ResponseWriter, req *http.Request) {
		var body map[string]any
		_ = httpx.DecodeJSON(req, &body)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"bank_account": map[string]any{
				"id":                    "ba-77",
				"shop_id":               chi.URLParam(req, "id"),
				"account_number_masked": "****7890",
				"status":                "VERIFIED",
			},
		})
	})

	// 2. Audited admin route carrying OTP, verification_code, and nested bank_account with raw account_number.
	r.Post("/api/v1/admin/refunds/{id}/settle", func(w http.ResponseWriter, req *http.Request) {
		var body map[string]any
		_ = httpx.DecodeJSON(req, &body)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"refund": map[string]any{
				"id": chi.URLParam(req, "id"),
			},
			"bank_account": map[string]any{
				"id":                    "ba-77",
				"bank_code":             "VCB",
				"account_number_masked": "****7890",
				"account_number":        "99887766557890",
				"status":                "VERIFIED",
				"otp_sent":              false,
			},
		})
	})

	// 3. Payment fee schedule (no path param; target_id extracted from config.id).
	r.Post("/api/v1/admin/fees/payment", func(w http.ResponseWriter, req *http.Request) {
		var body map[string]any
		_ = httpx.DecodeJSON(req, &body)
		httpx.WriteJSON(w, http.StatusCreated, map[string]any{
			"config": map[string]any{
				"id":             "pfee-42",
				"payment_method": "VIETQR",
				"fee_bps":        150,
			},
		})
	})

	// 4. Category fee schedule.
	r.Post("/api/v1/admin/fees/categories/{id}", func(w http.ResponseWriter, req *http.Request) {
		var body map[string]any
		_ = httpx.DecodeJSON(req, &body)
		httpx.WriteJSON(w, http.StatusCreated, map[string]any{
			"schedule": map[string]any{
				"id":                  "cfee-9",
				"category_id":         chi.URLParam(req, "id"),
				"commission_rate_bps": 600,
			},
		})
	})

	// 5. Resolve manual review order.
	r.Post("/api/v1/admin/orders/{id}/resolve-review", func(w http.ResponseWriter, req *http.Request) {
		var body map[string]any
		_ = httpx.DecodeOptionalJSON(req, &body)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"order": map[string]any{
				"id":                  chi.URLParam(req, "id"),
				"needs_manual_review": false,
			},
		})
	})

	do := func(path, body string, wantCode int) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+adminTok)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != wantCode {
			t.Fatalf("POST %s: want %d, got %d (%s)", path, wantCode, rec.Code, rec.Body.String())
		}
	}

	const (
		rawAccNum = "99887766557890"
		rawOTP    = "654321"
	)
	// Skipped (self-audited by services/user).
	do("/api/v1/admin/shops/shop-9/bank-account/review",
		`{"bank_account_id":"ba-77","decision":"APPROVE","reason":"verified"}`,
		http.StatusOK)
	// Audited routes:
	do("/api/v1/admin/refunds/ref-9/settle",
		`{"bank_account_id":"ba-77","bank_account_name":"NGUYEN VAN A","bank_account_number":"`+rawAccNum+`","provider_refund_id":"pr-1","otp":"`+rawOTP+`","verification_code":"987654","account_number":"`+rawAccNum+`"}`,
		http.StatusOK)
	do("/api/v1/admin/fees/payment", `{"payment_method":"VIETQR","fee_bps":150,"note":"q4"}`, http.StatusCreated)
	do("/api/v1/admin/fees/categories/cat-5", `{"commission_rate_bps":600,"note":"q4"}`, http.StatusCreated)
	do("/api/v1/admin/orders/ord-9/resolve-review", `{"note":"handled"}`, http.StatusOK)

	stub.mu.Lock()
	reqs := append([]*userv1.WriteAuditLogRequest(nil), stub.reqs...)
	stub.mu.Unlock()
	if len(reqs) != 4 {
		t.Fatalf("expected 4 audit records (bank-account review skipped as user-self-audited), got %d", len(reqs))
	}
	if got := auditTargetType("/api/v1/admin/shops/{id}/bank-account/review"); got != "shop_bank_account" {
		t.Fatalf("auditTargetType bank-account: want shop_bank_account, got %q", got)
	}

	// Check 1: OTP, verification_code, and raw account_number (top-level & nested) redacted while keeping masked & metadata.
	baAudit := reqs[0]
	afterBA := baAudit.GetAfterJson()
	if strings.Contains(afterBA, rawAccNum) || strings.Contains(afterBA, rawOTP) || strings.Contains(afterBA, "987654") {
		t.Fatalf("audit leaked raw account_number or otp: %s", afterBA)
	}
	if !strings.Contains(afterBA, `"account_number_masked":"****7890"`) {
		t.Fatalf("audit should preserve account_number_masked: %s", afterBA)
	}
	if !strings.Contains(afterBA, `"bank_account_id":"ba-77"`) {
		t.Fatalf("audit should preserve bank_account_id: %s", afterBA)
	}
	if !strings.Contains(afterBA, `"bank_account_name":"NGUYEN VAN A"`) {
		t.Fatalf("audit should preserve non-sensitive bank_account_name: %s", afterBA)
	}
	if !strings.Contains(afterBA, `"bank_account_number":"[REDACTED]"`) {
		t.Fatalf("audit must redact bank_account_number: %s", afterBA)
	}
	if !strings.Contains(afterBA, `"status":"VERIFIED"`) {
		t.Fatalf("audit should preserve nested bank_account.status: %s", afterBA)
	}
	if !strings.Contains(afterBA, `"account_number":"[REDACTED]"`) ||
		!strings.Contains(afterBA, `"otp":"[REDACTED]"`) ||
		!strings.Contains(afterBA, `"verification_code":"[REDACTED]"`) {
		t.Fatalf("audit must redact account_number, otp, and verification_code to [REDACTED]: %s", afterBA)
	}

	// Check 2: payment fee config extracts target_id from config.id and uses target_type=payment_fee_config.
	pfAudit := reqs[1]
	if pfAudit.GetTargetType() != "payment_fee_config" || pfAudit.GetTargetId() != "pfee-42" {
		t.Fatalf("payment fee audit target mismatch: type=%q id=%q", pfAudit.GetTargetType(), pfAudit.GetTargetId())
	}

	// Check 3: category fee schedule target_type=category_fee_schedule, target_id=cat-5.
	cfAudit := reqs[2]
	if cfAudit.GetTargetType() != "category_fee_schedule" || cfAudit.GetTargetId() != "cat-5" {
		t.Fatalf("category fee audit target mismatch: type=%q id=%q", cfAudit.GetTargetType(), cfAudit.GetTargetId())
	}

	// Check 4: order manual review target_type=order, target_id=ord-9.
	ordAudit := reqs[3]
	if ordAudit.GetTargetType() != "order" || ordAudit.GetTargetId() != "ord-9" {
		t.Fatalf("order review audit target mismatch: type=%q id=%q", ordAudit.GetTargetType(), ordAudit.GetTargetId())
	}
}
