package e2e

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	cartv1 "github.com/nexusmart/core/gen/go/nexusmart/cart/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type gwW2BErrorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		TraceID string `json:"trace_id"`
	} `json:"error"`
}

func gwW2BGetRaw(t *testing.T, baseURL, path, token string) (int, http.Header, []byte) {
	t.Helper()
	cli := &http.Client{Timeout: 20 * time.Second}
	req, err := http.NewRequestWithContext(getEnv().ctx(t), http.MethodGet, baseURL+path, nil)
	if err != nil {
		t.Fatalf("create raw GET %s: %v", path, err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := cli.Do(req)
	if err != nil {
		t.Fatalf("execute raw GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read raw GET %s body: %v", path, err)
	}
	return resp.StatusCode, resp.Header.Clone(), body
}

func TestE2E_GatewayW2B(t *testing.T) {
	env := getEnv()
	adminTok := env.adminLogin(t)
	cat, seller1, seller2 := env.ensureBaseMarketplace(t)
	buyer := env.registerVerifyAndLogin(t, "gw-w2b-buyer")

	todayVN := time.Now().In(time.FixedZone("Asia/Ho_Chi_Minh", 7*3600)).Format("2006-01-02")
	fromDate := time.Now().In(time.FixedZone("Asia/Ho_Chi_Minh", 7*3600)).AddDate(0, 0, -7).Format("2006-01-02")

	// 1. RBAC matrix on new Wave 2b routes.
	t.Run("RBACMatrix", func(t *testing.T) {
		// Public active shop discounts: requires sku_ids (<= 200) or shop_id -> 400 otherwise, 200 without auth when filtered.
		env.mustDoJSON(t, http.MethodGet, "/api/v1/shop-discounts/active", "", nil, nil, http.StatusBadRequest)
		tooManySKUs := make([]string, 201)
		for i := range tooManySKUs {
			tooManySKUs[i] = fmt.Sprintf("sku-%d", i+1)
		}
		env.mustDoJSON(t, http.MethodGet, "/api/v1/shop-discounts/active?sku_ids="+url.QueryEscape(strings.Join(tooManySKUs, ",")), "", nil, nil, http.StatusBadRequest)
		env.mustDoJSON(t, http.MethodGet, "/api/v1/shop-discounts/active?shop_id="+url.QueryEscape(seller1.ShopID), "", nil, nil, http.StatusOK)

		sellerGetPaths := []string{
			"/api/v1/seller/bank-account",
			"/api/v1/seller/flash-sales",
			"/api/v1/seller/flash-sales/items",
			"/api/v1/seller/discount-programs",
			"/api/v1/seller/reports?from_date=" + fromDate + "&to_date=" + todayVN,
			"/api/v1/seller/reports/export?from_date=" + fromDate + "&to_date=" + todayVN,
			"/api/v1/seller/ledger",
		}
		for _, p := range sellerGetPaths {
			env.mustDoJSON(t, http.MethodGet, p, "", nil, nil, http.StatusUnauthorized)
			env.mustDoJSON(t, http.MethodGet, p, buyer.AccessToken, nil, nil, http.StatusForbidden)
			env.mustDoJSON(t, http.MethodGet, p, adminTok, nil, nil, http.StatusForbidden)
		}

		adminGetPaths := []string{
			"/api/v1/admin/shops/" + url.PathEscape(seller1.ShopID) + "/bank-account",
			"/api/v1/admin/fees/categories",
			"/api/v1/admin/fees/payment",
			"/api/v1/admin/vouchers",
			"/api/v1/admin/flash-sales",
			"/api/v1/admin/flash-sale-items",
			"/api/v1/admin/orders/needs-review",
			"/api/v1/admin/reports?from_date=" + fromDate + "&to_date=" + todayVN,
			"/api/v1/admin/reports/export?from_date=" + fromDate + "&to_date=" + todayVN,
		}
		for _, p := range adminGetPaths {
			env.mustDoJSON(t, http.MethodGet, p, "", nil, nil, http.StatusUnauthorized)
			env.mustDoJSON(t, http.MethodGet, p, buyer.AccessToken, nil, nil, http.StatusForbidden)
			env.mustDoJSON(t, http.MethodGet, p, seller1.AccessToken, nil, nil, http.StatusForbidden)
		}
	})

	// 2. Gateway validation & Idempotency-Key guard.
	t.Run("GatewayValidationAndIdempotency", func(t *testing.T) {
		const dummyID = "00000000-0000-0000-0000-000000000001"
		env.mustDoJSON(t, http.MethodPatch, "/api/v1/seller/vouchers/"+dummyID+"/status", seller1.AccessToken, nil,
			map[string]any{"status": "DELETED"}, http.StatusBadRequest)
		env.mustDoJSON(t, http.MethodPatch, "/api/v1/admin/vouchers/"+dummyID+"/status", adminTok, nil,
			map[string]any{"status": "DELETED"}, http.StatusBadRequest)
		env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/fees/payment", adminTok, nil,
			map[string]any{"payment_method": "CRYPTO", "fee_bps": 100}, http.StatusBadRequest)
		// Required bool fields: missing approve on flash-sale-items/{id}/approve -> 400; missing enabled on PUT carriers/{code} -> 400.
		env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/flash-sale-items/"+dummyID+"/approve", adminTok, nil,
			map[string]any{"reason": "missing approve field"}, http.StatusBadRequest)
		env.mustDoJSON(t, http.MethodPut, "/api/v1/admin/carriers/ghn", adminTok, nil,
			map[string]any{"fallback_rates": []any{}}, http.StatusBadRequest)

		resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/payments/"+dummyID+"/refund", adminTok, nil,
			map[string]any{"order_id": dummyID, "amount_vnd": 1000, "reason": "test"}, http.StatusBadRequest)
		var errEnv gwW2BErrorEnvelope
		if err := DecodeJSON(resp, &errEnv); err != nil || errEnv.Error.Code != "MISSING_IDEMPOTENCY_KEY" {
			t.Fatalf("expected MISSING_IDEMPOTENCY_KEY, got %s", string(resp.Body))
		}
	})

	// 3. Server-authoritative preview + SKU min_price_30d_vnd + shop freeship settings.
	prod1, sku1 := env.createApprovedProduct(t, seller1, cat.ID, "GW-W2B Preview Item "+env.runID, 250000, 30, 400)

	t.Run("ShippingFreeshipAndAuthoritativePreview", func(t *testing.T) {
		// Verify SKU includes min_price_30d_vnd on public product fetch.
		prodResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/products/"+url.PathEscape(prod1.ID), "", nil, nil, http.StatusOK)
		var prodOut struct {
			Product struct {
				SKUs []struct {
					ID            string `json:"id"`
					PriceVND      int64  `json:"price_vnd"`
					MinPrice30dVN int64  `json:"min_price_30d_vnd"`
				} `json:"skus"`
			} `json:"product"`
		}
		if err := DecodeJSON(prodResp, &prodOut); err != nil {
			t.Fatalf("decode product: %v", err)
		}
		if len(prodOut.Product.SKUs) == 0 || prodOut.Product.SKUs[0].MinPrice30dVN != 250000 {
			t.Fatalf("expected min_price_30d_vnd=250000, got body=%s", string(prodResp.Body))
		}

		// Restore shared seller1 shipping-settings on exit so later tests relying on mock_express or no freeship are unaffected.
		t.Cleanup(func() {
			cleanCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			resp, err := env.client.DoJSON(cleanCtx, http.MethodPut, "/api/v1/seller/shipping-settings", seller1.AccessToken, nil, map[string]any{
				"enabled_carrier_codes":   []string{"ghn", "mock_express"},
				"default_weight_grams":    500,
				"default_length_cm":       20,
				"default_width_cm":        15,
				"default_height_cm":       10,
				"free_ship_enabled":       false,
				"free_ship_min_order_vnd": 0,
				"free_ship_max_vnd":       0,
			})
			if err != nil {
				t.Errorf("cleanup restore seller1 shipping-settings failed: %v", err)
				return
			}
			if resp.StatusCode != http.StatusOK {
				t.Errorf("cleanup restore seller1 shipping-settings returned HTTP %d: %s", resp.StatusCode, string(resp.Body))
			}
		})

		// Configure shop freeship on seller1.
		putResp := env.mustDoJSON(t, http.MethodPut, "/api/v1/seller/shipping-settings", seller1.AccessToken, nil, map[string]any{
			"enabled_carrier_codes":   []string{"ghn"},
			"default_weight_grams":    400,
			"default_length_cm":       20,
			"default_width_cm":        15,
			"default_height_cm":       10,
			"free_ship_enabled":       true,
			"free_ship_min_order_vnd": 100000,
			"free_ship_max_vnd":       15000,
		}, http.StatusOK)
		var shipSettings struct {
			Settings struct {
				FreeShipEnabled     bool  `json:"free_ship_enabled"`
				FreeShipMinOrderVND int64 `json:"free_ship_min_order_vnd"`
				FreeShipMaxVND      int64 `json:"free_ship_max_vnd"`
			} `json:"settings"`
		}
		if err := DecodeJSON(putResp, &shipSettings); err != nil {
			t.Fatalf("decode shipping-settings: %v", err)
		}
		if !shipSettings.Settings.FreeShipEnabled || shipSettings.Settings.FreeShipMinOrderVND != 100000 || shipSettings.Settings.FreeShipMaxVND != 15000 {
			t.Fatalf("unexpected shipping-settings freeship response: %s", string(putResp.Body))
		}

		// Cross-shop SKU spoof and unknown SKU in preview must both return identical 400 INVALID_ARGUMENT
		// code and message (P2-3: prevent SKU existence / shop-ownership oracle).
		crossShopResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/promotions/preview", buyer.AccessToken, nil, map[string]any{
			"sales_channel": "MARKETPLACE",
			"shop_items": []map[string]any{
				{
					"shop_id":          seller2.ShopID,
					"shipping_fee_vnd": 30000,
					"items":            []map[string]any{{"sku_id": sku1.ID, "quantity": 1}},
				},
			},
		}, http.StatusBadRequest)
		var crossShopErr gwW2BErrorEnvelope
		if err := DecodeJSON(crossShopResp, &crossShopErr); err != nil || crossShopErr.Error.Code != "INVALID_ARGUMENT" {
			t.Fatalf("cross-shop preview error envelope want INVALID_ARGUMENT, got err=%v body=%s", err, string(crossShopResp.Body))
		}

		// Unknown SKU in preview must return 400 INVALID_ARGUMENT with the exact same code and message as cross-shop SKU.
		unknownSKUResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/promotions/preview", buyer.AccessToken, nil, map[string]any{
			"sales_channel": "MARKETPLACE",
			"shop_items": []map[string]any{
				{
					"shop_id":          seller1.ShopID,
					"shipping_fee_vnd": 30000,
					"items":            []map[string]any{{"sku_id": "00000000-0000-0000-0000-000000000999", "quantity": 1}},
				},
			},
		}, http.StatusBadRequest)
		var unknownSKUErr gwW2BErrorEnvelope
		if err := DecodeJSON(unknownSKUResp, &unknownSKUErr); err != nil || unknownSKUErr.Error.Code != "INVALID_ARGUMENT" {
			t.Fatalf("unknown SKU preview error envelope want INVALID_ARGUMENT, got err=%v body=%s", err, string(unknownSKUResp.Body))
		}
		if unknownSKUErr.Error.Code != crossShopErr.Error.Code || unknownSKUErr.Error.Message != crossShopErr.Error.Message {
			t.Fatalf("unknown SKU preview error (%q: %q) must match cross-shop SKU error (%q: %q) to avoid existence oracle",
				unknownSKUErr.Error.Code, unknownSKUErr.Error.Message,
				crossShopErr.Error.Code, crossShopErr.Error.Message)
		}

		// Tampered client price (1 VND) and tampered freeship (999999 VND) must be overridden by server values.
		prevResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/promotions/preview", buyer.AccessToken, nil, map[string]any{
			"sales_channel": "MARKETPLACE",
			"shop_items": []map[string]any{
				{
					"shop_id":                 seller1.ShopID,
					"subtotal_vnd":            1,
					"shipping_fee_vnd":        30000,
					"free_ship_enabled":       true,
					"free_ship_min_order_vnd": 0,
					"free_ship_max_vnd":       999999,
					"items": []map[string]any{
						{
							"sku_id":                        sku1.ID,
							"quantity":                      1,
							"unit_price_vnd":                1,
							"category_id":                   "spoofed-cat",
							"shop_discount_program_item_id": "spoofed-item",
						},
					},
				},
			},
		}, http.StatusOK)
		var prevOut struct {
			Breakdown struct {
				TotalSubtotalVND             int64    `json:"total_subtotal_vnd"`
				TotalShopFreeshipDiscountVND int64    `json:"total_shop_freeship_discount_vnd"`
				Warnings                     []string `json:"warnings"`
				Shops                        []struct {
					ShopID                    string   `json:"shop_id"`
					SubtotalVND               int64    `json:"subtotal_vnd"`
					ShopFreeshipDiscountVND   int64    `json:"shop_freeship_discount_vnd"`
					AppliedShopProgramItemIDs []string `json:"applied_shop_program_item_ids"`
				} `json:"shops"`
			} `json:"breakdown"`
		}
		if err := DecodeJSON(prevResp, &prevOut); err != nil {
			t.Fatalf("decode preview response: %v", err)
		}
		if prevOut.Breakdown.TotalSubtotalVND != 250000 {
			t.Fatalf("preview must use authoritative SKU price 250000, got %d (body=%s)", prevOut.Breakdown.TotalSubtotalVND, string(prevResp.Body))
		}
		if prevOut.Breakdown.TotalShopFreeshipDiscountVND != 15000 {
			t.Fatalf("preview must use authoritative shop freeship 15000, got %d (body=%s)", prevOut.Breakdown.TotalShopFreeshipDiscountVND, string(prevResp.Body))
		}
		if len(prevOut.Breakdown.Shops) != 1 || prevOut.Breakdown.Shops[0].AppliedShopProgramItemIDs == nil {
			t.Fatalf("preview shop breakdown must include non-null applied_shop_program_item_ids array: %s", string(prevResp.Body))
		}
		if prevOut.Breakdown.Warnings == nil {
			t.Fatalf("preview breakdown must include non-null warnings array: %s", string(prevResp.Body))
		}
	})

	// 4. Wave 2b CRUD/list routes: vouchers, flash sales, shop discounts, fees, bank account, ledger, needs-review.
	t.Run("Wave2BRoutesEndToEnd", func(t *testing.T) {
		now := time.Now().UTC()
		// Seller voucher create -> update -> pause -> resume -> delete.
		vResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/vouchers", seller1.AccessToken, nil, map[string]any{
			"code":           env.uniqueCode("GWW2BV"),
			"name":           "GW W2B Voucher",
			"discount_type":  "FIXED",
			"value_vnd":      10000,
			"min_order_vnd":  50000,
			"quota":          10,
			"per_user_limit": 1,
			"budget_vnd":     100000,
			"starts_at":      now.Add(-5 * time.Minute).Format(time.RFC3339),
			"ends_at":        now.Add(24 * time.Hour).Format(time.RFC3339),
		}, http.StatusCreated)
		var vOut struct {
			Voucher struct {
				ID     string `json:"id"`
				Name   string `json:"name"`
				Status string `json:"status"`
			} `json:"voucher"`
		}
		if err := DecodeJSON(vResp, &vOut); err != nil || vOut.Voucher.ID == "" {
			t.Fatalf("decode created voucher: err=%v body=%s", err, string(vResp.Body))
		}
		vID := url.PathEscape(vOut.Voucher.ID)
		env.mustDoJSON(t, http.MethodPut, "/api/v1/seller/vouchers/"+vID, seller1.AccessToken, nil,
			map[string]any{"name": "GW W2B Voucher Updated"}, http.StatusOK)
		env.mustDoJSON(t, http.MethodPatch, "/api/v1/seller/vouchers/"+vID+"/status", seller1.AccessToken, nil,
			map[string]any{"status": "PAUSED"}, http.StatusOK)
		env.mustDoJSON(t, http.MethodPatch, "/api/v1/seller/vouchers/"+vID+"/status", seller1.AccessToken, nil,
			map[string]any{"status": "ACTIVE"}, http.StatusOK)
		env.mustDoJSON(t, http.MethodDelete, "/api/v1/seller/vouchers/"+vID, seller1.AccessToken, nil, nil, http.StatusOK)
		env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/vouchers?include_inactive=true", adminTok, nil, nil, http.StatusOK)

		// Flash sales list & item list (seller + admin).
		env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/flash-sales", seller1.AccessToken, nil, nil, http.StatusOK)
		env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/flash-sales/items", seller1.AccessToken, nil, nil, http.StatusOK)
		env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/flash-sales", adminTok, nil, nil, http.StatusOK)
		env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/flash-sale-items", adminTok, nil, nil, http.StatusOK)

		// Shop discount program create -> list -> public active -> end.
		progResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/discount-programs", seller1.AccessToken, nil, map[string]any{
			"name":      "GW W2B Weekend Deal",
			"starts_at": now.Add(-2 * time.Minute).Format(time.RFC3339),
			"ends_at":   now.Add(2 * time.Hour).Format(time.RFC3339),
			"items": []map[string]any{
				{
					"sku_id":               sku1.ID,
					"discounted_price_vnd": 210000,
					"per_user_limit":       2,
				},
			},
		}, http.StatusCreated)
		var progOut struct {
			Program struct {
				ID string `json:"id"`
			} `json:"program"`
		}
		if err := DecodeJSON(progResp, &progOut); err != nil || progOut.Program.ID == "" {
			t.Fatalf("decode discount program: err=%v body=%s", err, string(progResp.Body))
		}
		env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/discount-programs", seller1.AccessToken, nil, nil, http.StatusOK)
		actResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/shop-discounts/active?sku_ids="+url.QueryEscape(sku1.ID), "", nil, nil, http.StatusOK)
		if !strings.Contains(string(actResp.Body), sku1.ID) {
			t.Fatalf("active shop discounts missing sku %s: %s", sku1.ID, string(actResp.Body))
		}
		env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/discount-programs/"+url.PathEscape(progOut.Program.ID)+"/end", seller1.AccessToken, nil, nil, http.StatusOK)

		// Category update presence flags + category fee schedules + payment fee config.
		catUpd := env.mustDoJSON(t, http.MethodPut, "/api/v1/admin/categories/"+url.PathEscape(cat.ID), adminTok, nil,
			map[string]any{"name": cat.Name}, http.StatusOK)
		var catOut CategoryResponse
		if err := DecodeJSON(catUpd, &catOut); err != nil || catOut.Category.CommissionRateBps != cat.CommissionRateBps {
			t.Fatalf("omitted commission_rate_bps must preserve %d, got body=%s", cat.CommissionRateBps, string(catUpd.Body))
		}
		env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/fees/categories?category_id="+url.QueryEscape(cat.ID), adminTok, nil, nil, http.StatusOK)
		env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/fees/categories/"+url.PathEscape(cat.ID), adminTok, nil, map[string]any{
			"commission_rate_bps": 550,
			"effective_from":      now.Add(48 * time.Hour).Format(time.RFC3339),
			"note":                "gw w2b e2e future schedule",
		}, http.StatusCreated)

		env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/fees/payment?include_history=true", adminTok, nil, nil, http.StatusOK)
		env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/fees/payment", adminTok, nil, map[string]any{
			"payment_method": "VIETQR",
			"fee_bps":        120,
			"effective_from": now.Add(48 * time.Hour).Format(time.RFC3339),
			"note":           "gw w2b e2e future payment fee",
		}, http.StatusCreated)

		// Shop bank account: never return raw account_number on REST.
		const rawAcc = "01234567897890"
		baAdd := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/bank-account", seller1.AccessToken, nil, map[string]any{
			"bank_code":           "VCB",
			"bank_name":           "Vietcombank",
			"account_number":      rawAcc,
			"account_holder_name": "E2E seller1",
		}, http.StatusCreated)
		if strings.Contains(string(baAdd.Body), rawAcc) {
			t.Fatalf("POST /seller/bank-account leaked raw account number")
		}
		baGet := env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/bank-account", seller1.AccessToken, nil, nil, http.StatusOK)
		if strings.Contains(string(baGet.Body), rawAcc) || !strings.Contains(string(baGet.Body), "7890") {
			t.Fatalf("GET /seller/bank-account masking mismatch: %s", string(baGet.Body))
		}
		baAdmin := env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/shops/"+url.PathEscape(seller1.ShopID)+"/bank-account", adminTok, nil, nil, http.StatusOK)
		if strings.Contains(string(baAdmin.Body), rawAcc) {
			t.Fatalf("GET /admin/shops/{id}/bank-account leaked raw account number")
		}

		// Admin shops bank_review_pending filter + admin-only field (absent on public & seller routes).
		admShopsResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/shops?bank_review_pending=true", adminTok, nil, nil, http.StatusOK)
		var admShopsOut struct {
			Items []map[string]any `json:"items"`
		}
		if err := DecodeJSON(admShopsResp, &admShopsOut); err != nil {
			t.Fatalf("decode /admin/shops?bank_review_pending=true: %v", err)
		}
		for _, item := range admShopsOut.Items {
			if _, ok := item["bank_review_pending"]; !ok {
				t.Fatalf("admin shop item missing bank_review_pending: %+v", item)
			}
		}
		pubShopsResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/shops", "", nil, nil, http.StatusOK)
		if strings.Contains(string(pubShopsResp.Body), "bank_review_pending") {
			t.Fatalf("public GET /shops leaked bank_review_pending")
		}
		sellerShopResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/shop", seller1.AccessToken, nil, nil, http.StatusOK)
		if strings.Contains(string(sellerShopResp.Body), "bank_review_pending") {
			t.Fatalf("seller GET /seller/shop leaked bank_review_pending")
		}

		// Buyer order fee privacy: buyer checkout & GET /orders/{id} zero out commission/payment fees; seller/admin keep them.
		env.verifyBuyerPhone(t, buyer)
		env.addAddress(t, buyer)
		coResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "gw-w2b-privacy-" + env.runID,
		}, map[string]any{
			"sales_channel":  "MARKETPLACE",
			"address_id":     buyer.AddressID,
			"payment_method": "COD",
			"items":          []map[string]any{{"sku_id": sku1.ID, "quantity": 1}},
		}, http.StatusCreated)
		var coOut struct {
			Orders []struct {
				ID            string `json:"id"`
				CommissionVND int64  `json:"commission_vnd"`
				PaymentFeeVND int64  `json:"payment_fee_vnd"`
				PaymentFeeBps int32  `json:"payment_fee_bps"`
				Items         []struct {
					CommissionBps int32 `json:"commission_bps"`
				} `json:"items"`
			} `json:"orders"`
		}
		if err := DecodeJSON(coResp, &coOut); err != nil || len(coOut.Orders) != 1 {
			t.Fatalf("decode buyer privacy checkout: err=%v body=%s", err, string(coResp.Body))
		}
		bOrd := coOut.Orders[0]
		if bOrd.CommissionVND != 0 || bOrd.PaymentFeeVND != 0 || bOrd.PaymentFeeBps != 0 || (len(bOrd.Items) > 0 && bOrd.Items[0].CommissionBps != 0) {
			t.Fatalf("buyer checkout leaked platform/seller fees: %+v", bOrd)
		}
		sOrdResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/orders/"+url.PathEscape(bOrd.ID), seller1.AccessToken, nil, nil, http.StatusOK)
		var sOrdOut struct {
			Order struct {
				CommissionVND int64 `json:"commission_vnd"`
				Items         []struct {
					CommissionBps int32 `json:"commission_bps"`
				} `json:"items"`
			} `json:"order"`
		}
		if err := DecodeJSON(sOrdResp, &sOrdOut); err != nil || sOrdOut.Order.CommissionVND <= 0 || len(sOrdOut.Order.Items) == 0 || sOrdOut.Order.Items[0].CommissionBps <= 0 {
			t.Fatalf("seller GET /seller/orders/{id} must retain commission fields: err=%v body=%s", err, string(sOrdResp.Body))
		}

		// Seller ledger & Admin needs-review list.
		env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/ledger", seller1.AccessToken, nil, nil, http.StatusOK)
		env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/orders/needs-review", adminTok, nil, nil, http.StatusOK)
	})

	// 5. Seller & Admin JSON reports + CSV export (UTF-8 BOM, headers, valid RFC-4180 CSV).
	t.Run("ReportsAndCSVExport", func(t *testing.T) {
		q := fmt.Sprintf("?from_date=%s&to_date=%s", url.QueryEscape(fromDate), url.QueryEscape(todayVN))

		sRep := env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/reports"+q, seller1.AccessToken, nil, nil, http.StatusOK)
		var sRepOut struct {
			Report struct {
				ShopID   string `json:"shop_id"`
				FromDate string `json:"from_date"`
				ToDate   string `json:"to_date"`
			} `json:"report"`
		}
		if err := DecodeJSON(sRep, &sRepOut); err != nil || sRepOut.Report.ShopID != seller1.ShopID {
			t.Fatalf("GET /seller/reports mismatch: err=%v body=%s", err, string(sRep.Body))
		}

		aRep := env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/reports"+q, adminTok, nil, nil, http.StatusOK)
		var aRepOut struct {
			Report struct {
				FromDate  string `json:"from_date"`
				ToDate    string `json:"to_date"`
				UserStats *struct {
					TotalUsers int64 `json:"total_users"`
					TotalShops int64 `json:"total_shops"`
				} `json:"user_stats"`
			} `json:"report"`
		}
		if err := DecodeJSON(aRep, &aRepOut); err != nil || aRepOut.Report.UserStats == nil || aRepOut.Report.UserStats.TotalUsers <= 0 {
			t.Fatalf("GET /admin/reports missing aggregated user_stats: err=%v body=%s", err, string(aRep.Body))
		}

		utf8BOM := []byte{0xEF, 0xBB, 0xBF}
		for _, tc := range []struct {
			name, path, tok, wantPrefix string
		}{
			{"SellerCSV", "/api/v1/seller/reports/export" + q, seller1.AccessToken, "seller-report-"},
			{"AdminCSV", "/api/v1/admin/reports/export" + q, adminTok, "admin-report-"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				code, hdr, body := gwW2BGetRaw(t, env.cfg.GatewayURL, tc.path, tc.tok)
				if code != http.StatusOK {
					var e gwW2BErrorEnvelope
					_ = json.Unmarshal(body, &e)
					t.Fatalf("GET %s: want 200, got %d (%s)", tc.path, code, string(body))
				}
				if ct := hdr.Get("Content-Type"); ct != "text/csv; charset=utf-8" {
					t.Fatalf("Content-Type = %q, want text/csv; charset=utf-8", ct)
				}
				cd := hdr.Get("Content-Disposition")
				if !strings.HasPrefix(cd, "attachment; filename=\"") || !strings.Contains(cd, tc.wantPrefix) || !strings.HasSuffix(cd, ".csv\"") {
					t.Fatalf("Content-Disposition = %q, want attachment filename containing %q", cd, tc.wantPrefix)
				}
				if !bytes.HasPrefix(body, utf8BOM) {
					t.Fatalf("%s missing UTF-8 BOM prefix", tc.name)
				}
				rows, err := csv.NewReader(bytes.NewReader(body[len(utf8BOM):])).ReadAll()
				if err != nil {
					t.Fatalf("%s invalid CSV: %v", tc.name, err)
				}
				if len(rows) < 2 {
					t.Fatalf("%s expected header + summary rows, got %d rows", tc.name, len(rows))
				}
			})
		}
	})

	// 6. CartService gRPC fail-closed auth across all 5 RPCs:
	// anonymous -> Unauthenticated, service token -> PermissionDenied, cross-user JWT -> PermissionDenied.
	t.Run("CartGRPCAuthAllFiveRPCs", func(t *testing.T) {
		cartAddr := strings.TrimSpace(os.Getenv("CART_GRPC_ADDR"))
		if cartAddr == "" {
			if strings.Contains(env.cfg.CartHTTPURL, "://cart:") {
				cartAddr = "cart:50053"
			} else {
				cartAddr = "localhost:50053"
			}
		}
		conn, err := grpc.NewClient(cartAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Fatalf("dial CartService gRPC %s: %v", cartAddr, err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		cartClient := cartv1.NewCartServiceClient(conn)

		svcSecret := envOr("SERVICE_JWT_SECRET", "dev-only-nexusmart-service-hs256-secret-change-me")
		enc := base64.RawURLEncoding
		nowUnix := time.Now().Unix()
		claimsJSON, _ := json.Marshal(map[string]any{
			"sub":     "order-service",
			"role":    "admin",
			"service": "order-service",
			"iss":     "nexusmart",
			"jti":     uuid.NewString(),
			"iat":     nowUnix,
			"exp":     nowUnix + 180,
		})
		unsigned := enc.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." + enc.EncodeToString(claimsJSON)
		mac := hmac.New(sha256.New, []byte(svcSecret))
		_, _ = mac.Write([]byte(unsigned))
		svcTok := unsigned + "." + enc.EncodeToString(mac.Sum(nil))

		anonCtx := env.ctx(t)
		svcCtx := metadata.NewOutgoingContext(env.ctx(t), metadata.Pairs("authorization", "Bearer "+svcTok))
		buyerCtx := metadata.NewOutgoingContext(env.ctx(t), metadata.Pairs("authorization", "Bearer "+buyer.AccessToken))

		checkAll := func(label string, callCtx context.Context, targetUID string, want codes.Code) {
			t.Helper()
			if _, err := cartClient.GetCart(callCtx, &cartv1.GetCartRequest{UserId: targetUID}); status.Code(err) != want {
				t.Fatalf("%s GetCart: want %v, got %v", label, want, err)
			}
			if _, err := cartClient.AddItem(callCtx, &cartv1.AddItemRequest{UserId: targetUID, SkuId: sku1.ID, Quantity: 1}); status.Code(err) != want {
				t.Fatalf("%s AddItem: want %v, got %v", label, want, err)
			}
			if _, err := cartClient.UpdateItemQty(callCtx, &cartv1.UpdateItemQtyRequest{UserId: targetUID, SkuId: sku1.ID, Quantity: 1}); status.Code(err) != want {
				t.Fatalf("%s UpdateItemQty: want %v, got %v", label, want, err)
			}
			if _, err := cartClient.RemoveItem(callCtx, &cartv1.RemoveItemRequest{UserId: targetUID, SkuId: sku1.ID}); status.Code(err) != want {
				t.Fatalf("%s RemoveItem: want %v, got %v", label, want, err)
			}
			if _, err := cartClient.ClearItems(callCtx, &cartv1.ClearItemsRequest{UserId: targetUID, SkuIds: []string{sku1.ID}}); status.Code(err) != want {
				t.Fatalf("%s ClearItems: want %v, got %v", label, want, err)
			}
		}

		checkAll("anonymous", anonCtx, buyer.UserID, codes.Unauthenticated)
		checkAll("service token", svcCtx, buyer.UserID, codes.PermissionDenied)
		checkAll("cross-user buyer", buyerCtx, seller1.UserID, codes.PermissionDenied)
	})

	// 7. Gateway r6 + Shipping r4: POST /api/v1/shipping/calculate-fee with authoritative items[] + payment_method
	// resolves SKU=0g via shop's default_weight_grams (not 500g fallback / tampered client weight) and matches
	// real checkout shipping_fee_vnd for both VIETQR and COD; cross-shop and unknown SKUs return uniform 400.
	t.Run("CalculateShippingFeeAuthoritativeItemsMatchesCheckout", func(t *testing.T) {
		sShip := env.createApprovedSeller(t, "gw-w2b-ship-items")
		shipBuyer := env.registerVerifyAndLogin(t, "gw-w2b-ship-buyer")
		env.verifyBuyerPhone(t, shipBuyer)
		addr := env.addAddress(t, shipBuyer)

		// Configure shop default_weight_grams=1800 (distinct from 500g fallback) so qty=2 -> 3600g.
		env.mustDoJSON(t, http.MethodPut, "/api/v1/seller/shipping-settings", sShip.AccessToken, nil, map[string]any{
			"enabled_carrier_codes":   []string{"ghn", "mock_express"},
			"default_weight_grams":    1800,
			"default_length_cm":       25,
			"default_width_cm":        20,
			"default_height_cm":       12,
			"free_ship_enabled":       false,
			"free_ship_min_order_vnd": 0,
			"free_ship_max_vnd":       0,
		}, http.StatusOK)

		_, sku0g := env.createApprovedProduct(t, sShip, cat.ID, "GW-W2B ZeroWeight SKU "+env.runID, 220000, 20, 0)
		_, skuOtherShop := env.createApprovedProduct(t, seller2, cat.ID, "GW-W2B OtherShop SKU "+env.runID, 150000, 10, 300)

		// Baseline legacy quote at 1000g (2 * 500g fallback) to confirm 3600g (2 * 1800g) produces a higher fee.
		base500Resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/shipping/calculate-fee", shipBuyer.AccessToken, nil, map[string]any{
			"shop_id":          sShip.ShopID,
			"to_province_code": addr.ProvinceCode,
			"to_ward_code":     addr.WardCode,
			"weight_grams":     1000,
			"cod_amount_vnd":   0,
		}, http.StatusOK)
		var base500Out CalculateFeeResponse
		if err := DecodeJSON(base500Resp, &base500Out); err != nil || len(base500Out.Quotes) == 0 {
			t.Fatalf("decode 1000g baseline quote: err=%v body=%s", err, string(base500Resp.Body))
		}
		baseFeeByKey := map[string]int64{}
		for _, q := range base500Out.Quotes {
			baseFeeByKey[q.CarrierCode+":"+q.ServiceCode] = q.FeeVND
		}

		// Case A: VIETQR with items[] (SKU 0g, qty=2 -> 3600g; tampered weight=100, cod=999999 ignored).
		vQuoteResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/shipping/calculate-fee", shipBuyer.AccessToken, nil, map[string]any{
			"shop_id":          sShip.ShopID,
			"to_province_code": addr.ProvinceCode,
			"to_ward_code":     addr.WardCode,
			"weight_grams":     100,
			"cod_amount_vnd":   999999,
			"payment_method":   "VIETQR",
			"items":            []map[string]any{{"sku_id": sku0g.ID, "quantity": 2}},
		}, http.StatusOK)
		var vQuoteOut CalculateFeeResponse
		if err := DecodeJSON(vQuoteResp, &vQuoteOut); err != nil || len(vQuoteOut.Quotes) == 0 {
			t.Fatalf("decode VIETQR items quote: err=%v body=%s", err, string(vQuoteResp.Body))
		}
		vQuote := vQuoteOut.Quotes[0]
		if bFee, ok := baseFeeByKey[vQuote.CarrierCode+":"+vQuote.ServiceCode]; ok && vQuote.FeeVND <= bFee {
			t.Fatalf("expected 3600g (2*1800g) VIETQR fee > 1000g (2*500g) fee; got 3600g=%d 1000g=%d (default_weight_grams not applied?)",
				vQuote.FeeVND, bFee)
		}

		coVietQRResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", shipBuyer.AccessToken, map[string]string{
			"Idempotency-Key": "gw-w2b-ship-vqr-" + uuid.NewString(),
		}, CheckoutRequest{
			SalesChannel:  "MARKETPLACE",
			AddressID:     addr.ID,
			Items:         []CheckoutItemInputDTO{{SKUID: sku0g.ID, Quantity: 2}},
			PaymentMethod: "VIETQR",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: sShip.ShopID, CarrierCode: vQuote.CarrierCode, ServiceCode: vQuote.ServiceCode},
			},
		}, http.StatusCreated, http.StatusOK)
		var coVietQR CheckoutResponse
		if err := DecodeJSON(coVietQRResp, &coVietQR); err != nil || len(coVietQR.Orders) != 1 {
			t.Fatalf("decode VIETQR checkout: err=%v body=%s", err, string(coVietQRResp.Body))
		}
		if coVietQR.Orders[0].ShippingFeeVND != vQuote.FeeVND {
			t.Fatalf("VIETQR checkout shipping_fee_vnd=%d != calculate-fee(items) quote=%d",
				coVietQR.Orders[0].ShippingFeeVND, vQuote.FeeVND)
		}

		// Case B: COD with items[] (SKU 0g, qty=2 -> 3600g, cod=440000; tampered weight=100, cod=1 ignored).
		cQuoteResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/shipping/calculate-fee", shipBuyer.AccessToken, nil, map[string]any{
			"shop_id":          sShip.ShopID,
			"to_province_code": addr.ProvinceCode,
			"to_ward_code":     addr.WardCode,
			"weight_grams":     100,
			"cod_amount_vnd":   1,
			"payment_method":   "COD",
			"items":            []map[string]any{{"sku_id": sku0g.ID, "quantity": 2}},
		}, http.StatusOK)
		var cQuoteOut CalculateFeeResponse
		if err := DecodeJSON(cQuoteResp, &cQuoteOut); err != nil || len(cQuoteOut.Quotes) == 0 {
			t.Fatalf("decode COD items quote: err=%v body=%s", err, string(cQuoteResp.Body))
		}
		cQuote := cQuoteOut.Quotes[0]

		coCODResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", shipBuyer.AccessToken, map[string]string{
			"Idempotency-Key": "gw-w2b-ship-cod-" + uuid.NewString(),
		}, CheckoutRequest{
			SalesChannel:  "MARKETPLACE",
			AddressID:     addr.ID,
			Items:         []CheckoutItemInputDTO{{SKUID: sku0g.ID, Quantity: 2}},
			PaymentMethod: "COD",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: sShip.ShopID, CarrierCode: cQuote.CarrierCode, ServiceCode: cQuote.ServiceCode},
			},
		}, http.StatusCreated, http.StatusOK)
		var coCOD CheckoutResponse
		if err := DecodeJSON(coCODResp, &coCOD); err != nil || len(coCOD.Orders) != 1 {
			t.Fatalf("decode COD checkout: err=%v body=%s", err, string(coCODResp.Body))
		}
		if coCOD.Orders[0].ShippingFeeVND != cQuote.FeeVND {
			t.Fatalf("COD checkout shipping_fee_vnd=%d != calculate-fee(items) quote=%d",
				coCOD.Orders[0].ShippingFeeVND, cQuote.FeeVND)
		}

		// Case C: Cross-shop SKU and unknown SKU both return uniform 400 INVALID_ARGUMENT (no existence oracle).
		crossResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/shipping/calculate-fee", shipBuyer.AccessToken, nil, map[string]any{
			"shop_id":          sShip.ShopID,
			"to_province_code": addr.ProvinceCode,
			"to_ward_code":     addr.WardCode,
			"payment_method":   "VIETQR",
			"items":            []map[string]any{{"sku_id": skuOtherShop.ID, "quantity": 1}},
		}, http.StatusBadRequest)
		unknownResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/shipping/calculate-fee", shipBuyer.AccessToken, nil, map[string]any{
			"shop_id":          sShip.ShopID,
			"to_province_code": addr.ProvinceCode,
			"to_ward_code":     addr.WardCode,
			"payment_method":   "VIETQR",
			"items":            []map[string]any{{"sku_id": "00000000-0000-0000-0000-000000000999", "quantity": 1}},
		}, http.StatusBadRequest)
		var crossErr, unknownErr gwW2BErrorEnvelope
		if err := DecodeJSON(crossResp, &crossErr); err != nil {
			t.Fatalf("decode cross-shop error: %v", err)
		}
		if err := DecodeJSON(unknownResp, &unknownErr); err != nil {
			t.Fatalf("decode unknown SKU error: %v", err)
		}
		if crossErr.Error.Code != "INVALID_ARGUMENT" || unknownErr.Error.Code != "INVALID_ARGUMENT" ||
			crossErr.Error.Message != unknownErr.Error.Message {
			t.Fatalf("cross-shop vs unknown SKU errors must be uniform 400 INVALID_ARGUMENT: cross=%+v unknown=%+v",
				crossErr.Error, unknownErr.Error)
		}
	})
}
