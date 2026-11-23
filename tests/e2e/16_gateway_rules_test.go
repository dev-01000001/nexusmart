package e2e

import (
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

type gwFixErrorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		TraceID string `json:"trace_id"`
	} `json:"error"`
}

type gwFixProductListResponse struct {
	Items []ProductDTO `json:"items"`
	Total int64        `json:"total"`
}

type gwFixAuditLogDTO struct {
	ID         string `json:"id"`
	ActorID    string `json:"actor_id"`
	ActorRole  string `json:"actor_role"`
	Action     string `json:"action"`
	TargetType string `json:"target_type"`
	TargetID   string `json:"target_id"`
	IPAddress  string `json:"ip_address"`
}

type gwFixAuditLogsResponse struct {
	Items []gwFixAuditLogDTO `json:"items"`
	Total int64              `json:"total"`
}

func gwFixHasProductID(items []ProductDTO, targetID string) bool {
	for _, p := range items {
		if p.ID == targetID {
			return true
		}
	}
	return false
}

func TestE2E_GatewayRules(t *testing.T) {
	env := getEnv()
	cat, seller1, _ := env.ensureBaseMarketplace(t)
	adminTok := env.adminLogin(t)

	// 1. PLT-B2: Internal gRPC errors must return generic "internal server error" without leaking SQL/internal details.
	t.Run("InternalErrorMasked", func(t *testing.T) {
		slug := strings.ToLower(env.uniqueCode("gwcat-"))
		catResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/categories", adminTok, nil, CreateCategoryRequest{
			Name:              "E2E GW Mask Cat " + env.runID,
			Slug:              slug,
			CommissionRateBps: 500,
			SortOrder:         99,
		}, http.StatusCreated, http.StatusOK)
		var catOut CategoryResponse
		if err := DecodeJSON(catResp, &catOut); err != nil {
			t.Fatalf("decode temp category: %v", err)
		}

		prodResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/products", seller1.AccessToken, nil, CreateProductRequest{
			CategoryID:  catOut.Category.ID,
			Name:        "E2E GW Mask Product " + env.runID,
			Description: "temp product for internal error masking check",
			Images:      []string{"https://sandbox.nexusmart.local/img/p1.png"},
			SKUs: []SKUInputDTO{
				{
					SKUCode:     env.uniqueCode("GWMSK"),
					Name:        "Default",
					PriceVND:    150000,
					Stock:       5,
					WeightGrams: 200,
				},
			},
		}, http.StatusCreated, http.StatusOK)
		var prodOut ProductResponse
		if err := DecodeJSON(prodResp, &prodOut); err != nil {
			t.Fatalf("decode temp product: %v", err)
		}

		env.mustDoJSON(t, http.MethodDelete, fmt.Sprintf("/api/v1/seller/products/%s", url.PathEscape(prodOut.Product.ID)),
			seller1.AccessToken, nil, nil, http.StatusOK)

		// Deleting category with a soft-deleted product triggers FK SQLSTATE 23503 -> codes.Internal in product service.
		delResp, err := env.client.DoJSON(env.ctx(t), http.MethodDelete,
			fmt.Sprintf("/api/v1/admin/categories/%s", url.PathEscape(catOut.Category.ID)),
			adminTok, nil, nil)
		if err != nil {
			t.Fatalf("DELETE temp category: %v", err)
		}
		if delResp.StatusCode != http.StatusInternalServerError {
			t.Fatalf("expected 500 INTERNAL_ERROR to exercise gateway masking, got %d (body=%s)", delResp.StatusCode, string(delResp.Body))
		}

		var envErr gwFixErrorEnvelope
		if err := DecodeJSON(delResp, &envErr); err != nil {
			t.Fatalf("decode error envelope: %v", err)
		}
		if envErr.Error.Code != "INTERNAL_ERROR" || envErr.Error.Message != "internal server error" {
			t.Fatalf("internal error detail leaked: code=%q message=%q body=%s", envErr.Error.Code, envErr.Error.Message, string(delResp.Body))
		}
		if envErr.Error.TraceID == "" {
			t.Fatalf("expected non-empty trace_id in 500 error envelope")
		}
		rawBody := string(delResp.Body)
		for _, forbidden := range []string{"SQLSTATE", "products_category_id_fkey", "delete category", "foreign key"} {
			if strings.Contains(rawBody, forbidden) {
				t.Fatalf("500 response body leaked %q: %s", forbidden, rawBody)
			}
		}
	})

	// Create a PENDING product shared by CAT-B2 (public visibility) and R8 (admin review audit log).
	pendingCreateResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/products", seller1.AccessToken, nil, CreateProductRequest{
		CategoryID:  cat.ID,
		Name:        "E2E GW Pending Visibility " + env.runID,
		Description: "product used for CAT-B2 and admin audit verification",
		Images:      []string{"https://sandbox.nexusmart.local/img/p1.png"},
		SKUs: []SKUInputDTO{
			{
				SKUCode:     env.uniqueCode("GWPND"),
				Name:        "M",
				PriceVND:    180000,
				Stock:       10,
				WeightGrams: 250,
			},
		},
	}, http.StatusCreated, http.StatusOK)
	var pendingOut ProductResponse
	if err := DecodeJSON(pendingCreateResp, &pendingOut); err != nil {
		t.Fatalf("decode pending product: %v", err)
	}
	pendingProductID := pendingOut.Product.ID
	if !StatusEquals(pendingOut.Product.Status, "PENDING") {
		t.Fatalf("expected new seller product to be PENDING, got %q", pendingOut.Product.Status)
	}

	// 2. CAT-B2: Public product endpoints must never show PENDING products even with admin/seller tokens.
	t.Run("PublicProductsHidePendingFromAdminAndSeller", func(t *testing.T) {
		pubPath := fmt.Sprintf("/api/v1/products?shop_id=%s&page=1&page_size=100", url.QueryEscape(seller1.ShopID))

		adminPubResp := env.mustDoJSON(t, http.MethodGet, pubPath, adminTok, nil, nil, http.StatusOK)
		var adminPubList gwFixProductListResponse
		if err := DecodeJSON(adminPubResp, &adminPubList); err != nil {
			t.Fatalf("decode admin public products: %v", err)
		}
		if gwFixHasProductID(adminPubList.Items, pendingProductID) {
			t.Fatalf("public GET /api/v1/products with admin token leaked PENDING product %s", pendingProductID)
		}

		sellerPubResp := env.mustDoJSON(t, http.MethodGet, pubPath, seller1.AccessToken, nil, nil, http.StatusOK)
		var sellerPubList gwFixProductListResponse
		if err := DecodeJSON(sellerPubResp, &sellerPubList); err != nil {
			t.Fatalf("decode seller public products: %v", err)
		}
		if gwFixHasProductID(sellerPubList.Items, pendingProductID) {
			t.Fatalf("public GET /api/v1/products with seller token leaked PENDING product %s", pendingProductID)
		}

		// Public GET /api/v1/products/{id} with admin token must return 404 for PENDING product.
		env.mustDoJSON(t, http.MethodGet, fmt.Sprintf("/api/v1/products/%s", url.PathEscape(pendingProductID)),
			adminTok, nil, nil, http.StatusNotFound)

		// Admin endpoint /api/v1/admin/products must still return the PENDING product.
		adminListPath := fmt.Sprintf("/api/v1/admin/products?shop_id=%s&status=PENDING&page=1&page_size=100", url.QueryEscape(seller1.ShopID))
		adminListResp := env.mustDoJSON(t, http.MethodGet, adminListPath, adminTok, nil, nil, http.StatusOK)
		var adminList gwFixProductListResponse
		if err := DecodeJSON(adminListResp, &adminList); err != nil {
			t.Fatalf("decode admin products list: %v", err)
		}
		if !gwFixHasProductID(adminList.Items, pendingProductID) {
			t.Fatalf("GET /api/v1/admin/products missing PENDING product %s", pendingProductID)
		}
	})

	// 3. R8 (CAT-B7, PLT-B11, USR-B4): Admin approving a product creates an audit log,
	// and user self-audited admin.shop.review receives x-client-ip matching gateway's resolved ClientIP.
	var productAuditIP string
	t.Run("AdminProductReviewCreatesAuditLog", func(t *testing.T) {
		reviewPath := fmt.Sprintf("/api/v1/admin/products/%s/review", url.PathEscape(pendingProductID))
		reviewResp := env.mustDoJSON(t, http.MethodPost, reviewPath, adminTok, nil, ReviewRequest{
			Decision: "APPROVE",
			Reason:   "E2E gateway audit verification",
		}, http.StatusOK)
		var reviewed ProductResponse
		if err := DecodeJSON(reviewResp, &reviewed); err != nil {
			t.Fatalf("decode reviewed product: %v", err)
		}
		if !StatusEquals(reviewed.Product.Status, "APPROVED") {
			t.Fatalf("expected APPROVED status, got %q", reviewed.Product.Status)
		}

		auditPath := fmt.Sprintf("/api/v1/admin/audit-logs?target_id=%s&page=1&page_size=20", url.QueryEscape(pendingProductID))
		env.pollUntil(t, 5*time.Second, 200*time.Millisecond, "admin product review audit log", func() (bool, string) {
			resp, err := env.client.DoJSON(env.ctx(t), http.MethodGet, auditPath, adminTok, nil, nil)
			if err != nil {
				return false, fmt.Sprintf("request error: %v", err)
			}
			if resp.StatusCode != http.StatusOK {
				return false, fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(resp.Body))
			}
			var logs gwFixAuditLogsResponse
			if err := DecodeJSON(resp, &logs); err != nil {
				return false, fmt.Sprintf("decode error: %v", err)
			}
			for _, item := range logs.Items {
				if item.TargetID == pendingProductID &&
					item.Action == "POST /api/v1/admin/products/{id}/review" &&
					item.ActorID != "" &&
					item.ActorRole != "" &&
					item.IPAddress != "" {
					productAuditIP = item.IPAddress
					return true, ""
				}
			}
			return false, fmt.Sprintf("total=%d items=%+v", logs.Total, logs.Items)
		})
	})

	t.Run("UserSelfAuditedShopReviewRecordsClientIP", func(t *testing.T) {
		auditPath := fmt.Sprintf("/api/v1/admin/audit-logs?target_id=%s&page=1&page_size=20", url.QueryEscape(seller1.ShopID))
		resp := env.mustDoJSON(t, http.MethodGet, auditPath, adminTok, nil, nil, http.StatusOK)
		var logs gwFixAuditLogsResponse
		if err := DecodeJSON(resp, &logs); err != nil {
			t.Fatalf("decode shop audit logs: %v", err)
		}
		var found *gwFixAuditLogDTO
		for i := range logs.Items {
			if logs.Items[i].TargetID == seller1.ShopID && logs.Items[i].Action == "admin.shop.review" {
				found = &logs.Items[i]
				break
			}
		}
		if found == nil {
			t.Fatalf("expected admin.shop.review audit log for shop %s, got %+v", seller1.ShopID, logs.Items)
		}
		if _, err := netip.ParseAddr(found.IPAddress); err != nil {
			t.Fatalf("admin.shop.review ip_address must be a bare client IP (not gateway host:port): got %q (%v)", found.IPAddress, err)
		}
		if productAuditIP != "" && found.IPAddress != productAuditIP {
			t.Fatalf("admin.shop.review ip_address %q does not match gateway ClientIP %q", found.IPAddress, productAuditIP)
		}
	})

	// 4. R9 (PLT-B1): Spoofed X-Forwarded-For from an untrusted peer (e2e container != web) must not reset IP rate limit.
	t.Run("SpoofedXFFDoesNotBypassIPRateLimit", func(t *testing.T) {
		r1 := env.mustDoJSON(t, http.MethodGet, "/api/v1/categories", "", map[string]string{
			"X-Forwarded-For": "198.51.100.11",
		}, nil, http.StatusOK)
		r2 := env.mustDoJSON(t, http.MethodGet, "/api/v1/categories", "", map[string]string{
			"X-Forwarded-For": "198.51.100.11",
		}, nil, http.StatusOK)
		r3 := env.mustDoJSON(t, http.MethodGet, "/api/v1/categories", "", map[string]string{
			"X-Forwarded-For": "198.51.100.12",
		}, nil, http.StatusOK)

		rem1, err1 := strconv.Atoi(r1.Headers.Get("X-RateLimit-Remaining"))
		rem2, err2 := strconv.Atoi(r2.Headers.Get("X-RateLimit-Remaining"))
		rem3, err3 := strconv.Atoi(r3.Headers.Get("X-RateLimit-Remaining"))
		if err1 != nil || err2 != nil || err3 != nil {
			t.Fatalf("missing X-RateLimit-Remaining headers: %q, %q, %q",
				r1.Headers.Get("X-RateLimit-Remaining"),
				r2.Headers.Get("X-RateLimit-Remaining"),
				r3.Headers.Get("X-RateLimit-Remaining"))
		}
		if !(rem3 < rem2 && rem2 < rem1) {
			t.Fatalf("spoofed X-Forwarded-For bypassed IP rate limit: remaining=%d -> %d -> %d", rem1, rem2, rem3)
		}
	})
}
