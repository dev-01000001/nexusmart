package webhook

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/shipping/internal/carrier"
	"github.com/nexusmart/core/services/shipping/internal/service"
	"github.com/nexusmart/core/services/shipping/internal/store"
)

func TestLabelHandler_W3B2_StaffRoleMatrixAndServiceTokenRejected(t *testing.T) {
	repo := newStubRepo()
	_, _, _ = repo.CreateShipment(context.Background(), &store.Shipment{
		ID:               uuid.NewString(),
		OrderID:          "ord-label-rbac-1",
		ShopID:           "shop-1",
		BuyerID:          "buyer-owner",
		Direction:        store.DirectionForward,
		CarrierCode:      carrier.CodeGHN,
		ServiceCode:      "STANDARD",
		TrackingCode:     "GHN-LBL-RBAC-1",
		Status:           carrier.StatusCreated,
		FeeVND:           24000,
		CODAmountVND:     200000,
		WeightGrams:      500,
		FromProvinceCode: "79",
		FromWardCode:     "26740",
		ToProvinceCode:   "01",
		ToWardCode:       "00070",
		CreatedAt:        time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC),
	}, nil)

	jwtMgr, err := auth.NewManager("test-w3b2-label-user-secret", auth.DefaultAccessTTL)
	if err != nil {
		t.Fatalf("NewManager(user): %v", err)
	}
	svcMgr, err := auth.NewManager("test-w3b2-label-svc-secret", auth.ServiceTokenTTL)
	if err != nil {
		t.Fatalf("NewManager(svc): %v", err)
	}

	agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc := service.NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	lh := NewLabelHandler(svc)

	r := chi.NewRouter()
	r.Group(func(authed chi.Router) {
		authed.Use(auth.HTTPMiddleware(jwtMgr, true))
		authed.Get("/labels/{tracking_code}", lh.ServeHTTP)
	})

	sign := func(uid, role, shopID string) string {
		tok, _, _, sErr := jwtMgr.SignAccessToken(uid, role, shopID)
		if sErr != nil {
			t.Fatalf("SignAccessToken(%s): %v", role, sErr)
		}
		return tok
	}

	svcTok, err := svcMgr.SignServiceToken("order-service")
	if err != nil {
		t.Fatalf("SignServiceToken: %v", err)
	}
	// Also test a service token signed with the user key (defense-in-depth: VerifyAccessToken rejects claims.Service != "").
	sameKeySvcTok, err := jwtMgr.SignServiceToken("order-service")
	if err != nil {
		t.Fatalf("jwtMgr.SignServiceToken: %v", err)
	}

	cases := []struct {
		name       string
		token      string
		wantStatus int
	}{
		{name: "super_admin", token: sign("sa-1", auth.RoleSuperAdmin, ""), wantStatus: http.StatusOK},
		{name: "admin", token: sign("adm-1", auth.RoleAdmin, ""), wantStatus: http.StatusOK},
		{name: "cskh", token: sign("cskh-1", auth.RoleCSKH, ""), wantStatus: http.StatusOK},
		{name: "accountant", token: sign("acc-1", auth.RoleAccountant, ""), wantStatus: http.StatusOK},
		{name: "ops", token: sign("ops-1", auth.RoleOps, ""), wantStatus: http.StatusOK},
		{name: "moderator_denied", token: sign("mod-1", auth.RoleModerator, ""), wantStatus: http.StatusForbidden},
		{name: "moderator_stale_shop_denied", token: sign("mod-2", auth.RoleModerator, "shop-1"), wantStatus: http.StatusForbidden},
		{name: "buyer_owner", token: sign("buyer-owner", auth.RoleBuyer, ""), wantStatus: http.StatusOK},
		{name: "other_buyer_denied", token: sign("buyer-other", auth.RoleBuyer, ""), wantStatus: http.StatusForbidden},
		{name: "seller_owner", token: sign("seller-1", auth.RoleSeller, "shop-1"), wantStatus: http.StatusOK},
		{name: "seller_as_buyer", token: sign("buyer-owner", auth.RoleSeller, "shop-2"), wantStatus: http.StatusOK},
		{name: "other_seller_denied", token: sign("seller-2", auth.RoleSeller, "shop-2"), wantStatus: http.StatusForbidden},
		{name: "service_token_other_key", token: svcTok, wantStatus: http.StatusUnauthorized},
		{name: "service_token_same_key", token: sameKeySvcTok, wantStatus: http.StatusUnauthorized},
		{name: "anonymous", token: "", wantStatus: http.StatusUnauthorized},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/labels/GHN-LBL-RBAC-1", nil)
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("GET /labels (%s): status=%d, want %d (body=%s)", tc.name, rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantStatus == http.StatusOK {
				if !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
					t.Fatalf("GET /labels (%s): expected text/html, got %q", tc.name, rec.Header().Get("Content-Type"))
				}
				if !strings.Contains(rec.Body.String(), "GHN-LBL-RBAC-1") {
					t.Fatalf("GET /labels (%s): missing tracking code in body", tc.name)
				}
			} else if tc.wantStatus == http.StatusForbidden {
				if !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
					t.Fatalf("GET /labels (%s): expected JSON 403 error, got %q", tc.name, rec.Header().Get("Content-Type"))
				}
			}
		})
	}

	// Direct handler call with injected service claims (without middleware) must still reject service token.
	t.Run("direct_handler_rejects_service_claims", func(t *testing.T) {
		rDirect := chi.NewRouter()
		rDirect.Get("/labels/{tracking_code}", lh.ServeHTTP)
		req := httptest.NewRequest(http.MethodGet, "/labels/GHN-LBL-RBAC-1", nil)
		req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{
			Role:    auth.ServiceRole,
			Service: "order-service",
		}))
		rec := httptest.NewRecorder()
		rDirect.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("direct LabelHandler with service claims: status=%d, want 401", rec.Code)
		}
	})
}
