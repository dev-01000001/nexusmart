package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

type gwW3AErrorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		TraceID string `json:"trace_id"`
	} `json:"error"`
}

// gwW3ADoRawJSON executes an HTTP JSON request directly against Gateway WITHOUT the e2e Client's
// automatic 429 Retry-After sleep/retry loop so tests can directly assert HTTP 429 rate limits.
func gwW3ADoRawJSON(t *testing.T, env *testEnv, method, path, token string, reqBody any) (int, http.Header, []byte) {
	t.Helper()
	var bodyReader io.Reader
	hasBody := reqBody != nil
	if hasBody {
		encoded, err := json.Marshal(reqBody)
		if err != nil {
			t.Fatalf("marshal raw %s %s body: %v", method, path, err)
		}
		bodyReader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(env.ctx(t), method, env.cfg.GatewayURL+path, bodyReader)
	if err != nil {
		t.Fatalf("create raw %s %s request: %v", method, path, err)
	}
	if hasBody {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	cli := &http.Client{Timeout: env.cfg.HTTPTimeout}
	resp, err := cli.Do(req)
	if err != nil {
		t.Fatalf("execute raw %s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read raw %s %s body: %v", method, path, err)
	}
	return resp.StatusCode, resp.Header.Clone(), body
}

func gwW3AAssertErrorCode(t *testing.T, label string, body []byte, wantCode string) {
	t.Helper()
	var env gwW3AErrorEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("%s: decode error envelope failed (%v): body=%s", label, err, string(body))
	}
	if env.Error.Code != wantCode {
		t.Fatalf("%s: error.code = %q, want %q (body=%s)", label, env.Error.Code, wantCode, string(body))
	}
}

func TestE2E_GatewayW3A(t *testing.T) {
	env := getEnv()
	adminTok := env.adminLogin(t)
	_, seller1, seller2 := env.ensureBaseMarketplace(t)
	buyer := env.registerVerifyAndLogin(t, "gw-w3a-buyer")
	prov, ward := env.ensureProvinceAndWard(t)

	// Dedicated category with a distinct commission_rate_bps so assertions are deterministic.
	catSlug := strings.ToLower(env.uniqueCode("gww3acat-"))
	catResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/categories", adminTok, nil, CreateCategoryRequest{
		Name:              "GW W3A Category " + env.runID,
		Slug:              catSlug,
		CommissionRateBps: 750,
		SortOrder:         10,
	}, http.StatusCreated, http.StatusOK)
	var catOut CategoryResponse
	if err := DecodeJSON(catResp, &catOut); err != nil || catOut.Category.ID == "" {
		t.Fatalf("create W3A category failed: err=%v body=%s", err, string(catResp.Body))
	}
	catID := catOut.Category.ID

	prod1, sku1 := env.createApprovedProduct(t, seller1, catID, "GW W3A Public Alpha "+env.runID, 220000, 25, 350)
	prod2, sku2 := env.createApprovedProduct(t, seller2, catID, "GW W3A Public Beta "+env.runID, 280000, 20, 400)

	// Hidden product owned by seller1 (for D2 and D8).
	prodHid, skuHid := env.createApprovedProduct(t, seller1, catID, "GW W3A Hidden Item "+env.runID, 190000, 15, 300)
	env.mustDoJSON(t, http.MethodPut, fmt.Sprintf("/api/v1/seller/products/%s", url.PathEscape(prodHid.ID)),
		seller1.AccessToken, nil, map[string]any{"status": "HIDDEN"}, http.StatusOK)

	// --- 1. D1: commission_rate_bps stripped on public routes, retained on admin & seller routes ---
	t.Run("D1_CommissionStrippedPublicAndKeptAdminSeller", func(t *testing.T) {
		// Fetch seller product first to obtain slug and confirm seller sees commission_rate_bps.
		sellerProdResp := env.mustDoJSON(t, http.MethodGet,
			fmt.Sprintf("/api/v1/seller/products/%s", url.PathEscape(prod1.ID)),
			seller1.AccessToken, nil, nil, http.StatusOK)
		var sellerProdOut struct {
			Product struct {
				ID   string `json:"id"`
				Slug string `json:"slug"`
				SKUs []struct {
					ID                string `json:"id"`
					CommissionRateBps *int32 `json:"commission_rate_bps"`
				} `json:"skus"`
			} `json:"product"`
		}
		if err := DecodeJSON(sellerProdResp, &sellerProdOut); err != nil {
			t.Fatalf("decode seller product: %v", err)
		}
		if len(sellerProdOut.Product.SKUs) == 0 || sellerProdOut.Product.SKUs[0].CommissionRateBps == nil ||
			*sellerProdOut.Product.SKUs[0].CommissionRateBps != 750 {
			t.Fatalf("GET /seller/products/{id} must retain commission_rate_bps=750, got body=%s", string(sellerProdResp.Body))
		}

		// Admin product route retains commission_rate_bps.
		adminProdResp := env.mustDoJSON(t, http.MethodGet,
			fmt.Sprintf("/api/v1/admin/products/%s", url.PathEscape(prod1.ID)),
			adminTok, nil, nil, http.StatusOK)
		var adminProdOut struct {
			Product struct {
				SKUs []struct {
					ID                string `json:"id"`
					CommissionRateBps *int32 `json:"commission_rate_bps"`
				} `json:"skus"`
			} `json:"product"`
		}
		if err := DecodeJSON(adminProdResp, &adminProdOut); err != nil {
			t.Fatalf("decode admin product: %v", err)
		}
		if len(adminProdOut.Product.SKUs) == 0 || adminProdOut.Product.SKUs[0].CommissionRateBps == nil ||
			*adminProdOut.Product.SKUs[0].CommissionRateBps != 750 {
			t.Fatalf("GET /admin/products/{id} must retain commission_rate_bps=750, got body=%s", string(adminProdResp.Body))
		}

		// Public routes MUST NOT contain commission_rate_bps (anonymous OR with admin JWT).
		publicPaths := []string{
			"/api/v1/categories",
			"/api/v1/products?shop_id=" + url.QueryEscape(seller1.ShopID),
			"/api/v1/products/" + url.PathEscape(prod1.ID),
		}
		if sellerProdOut.Product.Slug != "" {
			publicPaths = append(publicPaths, "/api/v1/products/slug/"+url.PathEscape(sellerProdOut.Product.Slug))
		}
		for _, path := range publicPaths {
			for _, tok := range []string{"", adminTok} {
				resp := env.mustDoJSON(t, http.MethodGet, path, tok, nil, nil, http.StatusOK)
				if bytes.Contains(resp.Body, []byte("commission_rate_bps")) {
					t.Fatalf("public GET %s (authed=%v) leaked commission_rate_bps: %s", path, tok != "", string(resp.Body))
				}
			}
		}

		// GET /api/v1/admin/categories enforces RBAC and retains commission_rate_bps.
		env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/categories", "", nil, nil, http.StatusUnauthorized)
		env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/categories", buyer.AccessToken, nil, nil, http.StatusForbidden)
		env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/categories", seller1.AccessToken, nil, nil, http.StatusForbidden)

		adminCatsResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/categories", adminTok, nil, nil, http.StatusOK)
		var adminCatsOut struct {
			Categories []struct {
				ID                string `json:"id"`
				CommissionRateBps *int32 `json:"commission_rate_bps"`
			} `json:"categories"`
		}
		if err := DecodeJSON(adminCatsResp, &adminCatsOut); err != nil {
			t.Fatalf("decode admin categories: %v", err)
		}
		found := false
		for _, c := range adminCatsOut.Categories {
			if c.ID == catID {
				found = true
				if c.CommissionRateBps == nil || *c.CommissionRateBps != 750 {
					t.Fatalf("GET /admin/categories category %s commission_rate_bps=%v, want 750", catID, c.CommissionRateBps)
				}
				break
			}
		}
		if !found {
			t.Fatalf("GET /admin/categories did not return created category %s", catID)
		}

		// GET /api/v1/seller/categories (D1 supplement): seller-only RBAC + retains commission_rate_bps.
		env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/categories", "", nil, nil, http.StatusUnauthorized)
		env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/categories", buyer.AccessToken, nil, nil, http.StatusForbidden)
		env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/categories", adminTok, nil, nil, http.StatusForbidden)

		sellerCatsResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/categories", seller1.AccessToken, nil, nil, http.StatusOK)
		var sellerCatsOut struct {
			Categories []struct {
				ID                string `json:"id"`
				CommissionRateBps *int32 `json:"commission_rate_bps"`
			} `json:"categories"`
		}
		if err := DecodeJSON(sellerCatsResp, &sellerCatsOut); err != nil {
			t.Fatalf("decode seller categories: %v", err)
		}
		foundSeller := false
		for _, c := range sellerCatsOut.Categories {
			if c.ID == catID {
				foundSeller = true
				if c.CommissionRateBps == nil || *c.CommissionRateBps != 750 {
					t.Fatalf("GET /seller/categories category %s commission_rate_bps=%v, want 750", catID, c.CommissionRateBps)
				}
				break
			}
		}
		if !foundSeller {
			t.Fatalf("GET /seller/categories did not return created category %s", catID)
		}
	})

	// --- 2. D2 & P2-3: hidden SKU returns 409 SKU_INACTIVE for matching shop; unknown/cross-shop/missing-shop return uniform 400 ---
	t.Run("D2_HiddenSKUHandling", func(t *testing.T) {
		prevResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/promotions/preview", buyer.AccessToken, nil, map[string]any{
			"shop_items": []map[string]any{
				{
					"shop_id":          seller1.ShopID,
					"shipping_fee_vnd": 25000,
					"items":            []map[string]any{{"sku_id": skuHid.ID, "quantity": 1}},
				},
			},
		}, http.StatusConflict)
		gwW3AAssertErrorCode(t, "preview hidden SKU", prevResp.Body, "SKU_INACTIVE")

		// P2-3 oracle suppression on preview: unknown SKU, cross-shop hidden SKU, and missing shop_id + hidden SKU
		// must all return identical 400 INVALID_ARGUMENT code & message.
		oracleReqs := []struct {
			name   string
			shopID string
			skuID  string
		}{
			{"unknown_sku", seller1.ShopID, "00000000-0000-0000-0000-000000000999"},
			{"cross_shop_hidden_sku", seller2.ShopID, skuHid.ID},
			{"missing_shop_hidden_sku", "", skuHid.ID},
		}
		var basePreviewErrMsg string
		for _, tc := range oracleReqs {
			r := env.mustDoJSON(t, http.MethodPost, "/api/v1/promotions/preview", buyer.AccessToken, nil, map[string]any{
				"shop_items": []map[string]any{
					{
						"shop_id":          tc.shopID,
						"shipping_fee_vnd": 25000,
						"items":            []map[string]any{{"sku_id": tc.skuID, "quantity": 1}},
					},
				},
			}, http.StatusBadRequest)
			var errEnv gwW3AErrorEnvelope
			if err := json.Unmarshal(r.Body, &errEnv); err != nil || errEnv.Error.Code != "INVALID_ARGUMENT" {
				t.Fatalf("preview %s: want INVALID_ARGUMENT, got %s", tc.name, string(r.Body))
			}
			if basePreviewErrMsg == "" {
				basePreviewErrMsg = errEnv.Error.Message
			} else if errEnv.Error.Message != basePreviewErrMsg {
				t.Fatalf("preview %s error message %q != baseline %q", tc.name, errEnv.Error.Message, basePreviewErrMsg)
			}
		}

		feeResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/shipping/calculate-fee", buyer.AccessToken, nil, map[string]any{
			"shop_id":            seller1.ShopID,
			"from_province_code": prov.Code,
			"from_ward_code":     ward.Code,
			"to_province_code":   prov.Code,
			"to_ward_code":       ward.Code,
			"payment_method":     "VIETQR",
			"items":              []map[string]any{{"sku_id": skuHid.ID, "quantity": 1}},
		}, http.StatusConflict)
		gwW3AAssertErrorCode(t, "calculate-fee hidden SKU", feeResp.Body, "SKU_INACTIVE")

		// Cross-shop seller registering another shop's hidden SKU for a flash sale -> 403 PERMISSION_DENIED.
		now := time.Now().UTC()
		fsResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/flash-sales", adminTok, nil, map[string]any{
			"name":                 "GW W3A Flash " + env.runID,
			"starts_at":            now.Add(2 * time.Hour).Format(time.RFC3339),
			"ends_at":              now.Add(4 * time.Hour).Format(time.RFC3339),
			"min_discount_percent": 10,
		}, http.StatusCreated, http.StatusOK)
		var fsOut struct {
			FlashSale struct {
				ID string `json:"id"`
			} `json:"flash_sale"`
		}
		if err := DecodeJSON(fsResp, &fsOut); err != nil || fsOut.FlashSale.ID == "" {
			t.Fatalf("create flash sale failed: err=%v body=%s", err, string(fsResp.Body))
		}
		regResp := env.mustDoJSON(t, http.MethodPost,
			fmt.Sprintf("/api/v1/seller/flash-sales/%s/items", url.PathEscape(fsOut.FlashSale.ID)),
			seller2.AccessToken, nil, map[string]any{
				"sku_id":          skuHid.ID,
				"flash_price_vnd": 150000,
				"total_slots":     5,
				"per_user_limit":  1,
			}, http.StatusForbidden)
		gwW3AAssertErrorCode(t, "cross-shop flash register hidden SKU", regResp.Body, "PERMISSION_DENIED")
	})

	// --- 3. D6: bank-account review rejects missing bank_account_id at gateway with 400 ---
	t.Run("D6_BankAccountReviewMissingID_400", func(t *testing.T) {
		reviewPath := fmt.Sprintf("/api/v1/admin/shops/%s/bank-account/review", url.PathEscape(seller1.ShopID))
		resp := env.mustDoJSON(t, http.MethodPost, reviewPath, adminTok, nil, map[string]any{
			"decision": "APPROVE",
			"reason":   "missing bank_account_id",
		}, http.StatusBadRequest)
		gwW3AAssertErrorCode(t, "bank-account review missing id", resp.Body, "INVALID_ARGUMENT")
	})

	// --- 4. D7: bank OTP endpoints per-user rate limit (default 5/min) -> 429 RATE_LIMITED + Retry-After ---
	t.Run("D7_BankOTPRateLimit429AndIsolation", func(t *testing.T) {
		// Dedicated fresh sellers so rate-limit and DB bank-account buckets are completely clean.
		sellerRL1 := env.createApprovedSeller(t, "gww3a-rl1")
		sellerRL2 := env.createApprovedSeller(t, "gww3a-rl2")

		addBody := map[string]any{
			"bank_code":           "VCB",
			"bank_name":           "Vietcombank",
			"account_number":      "10293847560011",
			"account_holder_name": "E2E GWW3A RL1",
		}
		code1, hdr1, body1 := gwW3ADoRawJSON(t, env, http.MethodPost, "/api/v1/seller/bank-account", sellerRL1.AccessToken, addBody)
		if code1 != http.StatusCreated && code1 != http.StatusOK {
			t.Fatalf("call 1 POST /seller/bank-account: want 201/200, got %d (%s)", code1, string(body1))
		}

		limit := 5
		if rawLim := strings.TrimSpace(hdr1.Get("X-RateLimit-Limit")); rawLim != "" {
			if parsed, err := strconv.Atoi(rawLim); err == nil && parsed >= 1 && parsed <= 50 {
				limit = parsed
			}
		}

		// Consume remaining calls up to limit on verify-otp; none should hit gateway RATE_LIMITED yet.
		for i := 2; i <= limit; i++ {
			code, _, body := gwW3ADoRawJSON(t, env, http.MethodPost, "/api/v1/seller/bank-account/verify-otp",
				sellerRL1.AccessToken, map[string]any{"otp": "000000"})
			if code == http.StatusTooManyRequests {
				var errEnv gwW3AErrorEnvelope
				_ = json.Unmarshal(body, &errEnv)
				if errEnv.Error.Code == "RATE_LIMITED" {
					t.Fatalf("call %d/%d hit gateway RATE_LIMITED prematurely: %s", i, limit, string(body))
				}
			}
		}

		// Next calls on BOTH POST /seller/bank-account and POST /seller/bank-account/verify-otp -> 429 RATE_LIMITED + Retry-After.
		for _, tc := range []struct {
			path string
			body any
		}{
			{"/api/v1/seller/bank-account", addBody},
			{"/api/v1/seller/bank-account/verify-otp", map[string]any{"otp": "000000"}},
		} {
			code, hdr, body := gwW3ADoRawJSON(t, env, http.MethodPost, tc.path, sellerRL1.AccessToken, tc.body)
			if code != http.StatusTooManyRequests {
				t.Fatalf("limit+1 POST %s: want 429, got %d (%s)", tc.path, code, string(body))
			}
			gwW3AAssertErrorCode(t, "limit+1 POST "+tc.path, body, "RATE_LIMITED")
			retryAfter, err := strconv.Atoi(strings.TrimSpace(hdr.Get("Retry-After")))
			if err != nil || retryAfter < 1 {
				t.Fatalf("limit+1 POST %s Retry-After = %q, want integer >= 1", tc.path, hdr.Get("Retry-After"))
			}
		}

		// GET /seller/bank-account for sellerRL1 is NOT blocked by the bank OTP limiter.
		getCode, _, getBody := gwW3ADoRawJSON(t, env, http.MethodGet, "/api/v1/seller/bank-account", sellerRL1.AccessToken, nil)
		if getCode != http.StatusOK {
			t.Fatalf("GET /seller/bank-account for sellerRL1 must stay 200, got %d (%s)", getCode, string(getBody))
		}

		// Another seller (sellerRL2) is NOT affected by sellerRL1's exhausted bucket.
		codeB, _, bodyB := gwW3ADoRawJSON(t, env, http.MethodPost, "/api/v1/seller/bank-account", sellerRL2.AccessToken, map[string]any{
			"bank_code":           "TCB",
			"bank_name":           "Techcombank",
			"account_number":      "10293847560022",
			"account_holder_name": "E2E GWW3A RL2",
		})
		if codeB != http.StatusCreated && codeB != http.StatusOK {
			t.Fatalf("sellerRL2 POST /seller/bank-account must stay 201/200, got %d (%s)", codeB, string(bodyB))
		}
	})

	// --- 5. D8: GET /api/v1/products?sku_ids=... filtering, public-only enforcement, and 400 validation ---
	t.Run("D8_ListProductsBySKUIDs", func(t *testing.T) {
		q := fmt.Sprintf("/api/v1/products?sku_ids=%s,%s,%s",
			url.QueryEscape(sku1.ID), url.QueryEscape(sku2.ID), url.QueryEscape(sku1.ID))
		resp := env.mustDoJSON(t, http.MethodGet, q, "", nil, nil, http.StatusOK)
		if bytes.Contains(resp.Body, []byte("commission_rate_bps")) {
			t.Fatalf("GET /products?sku_ids leaked commission_rate_bps: %s", string(resp.Body))
		}
		var out struct {
			Items []ProductDTO `json:"items"`
			Total int64        `json:"total"`
		}
		if err := DecodeJSON(resp, &out); err != nil {
			t.Fatalf("decode GET /products?sku_ids: %v", err)
		}
		gotIDs := make(map[string]bool, len(out.Items))
		for _, item := range out.Items {
			gotIDs[item.ID] = true
		}
		if !gotIDs[prod1.ID] || !gotIDs[prod2.ID] || len(out.Items) != 2 {
			t.Fatalf("GET /products?sku_ids expected exactly {%s, %s}, got %+v (body=%s)", prod1.ID, prod2.ID, gotIDs, string(resp.Body))
		}

		// Hidden SKU must return empty items on public listing.
		hidResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/products?sku_ids="+url.QueryEscape(skuHid.ID), "", nil, nil, http.StatusOK)
		var hidOut struct {
			Items []ProductDTO `json:"items"`
		}
		if err := DecodeJSON(hidResp, &hidOut); err != nil {
			t.Fatalf("decode hidden sku_ids: %v", err)
		}
		if len(hidOut.Items) != 0 {
			t.Fatalf("GET /products?sku_ids=<hidden> expected 0 items, got %d (%s)", len(hidOut.Items), string(hidResp.Body))
		}

		// P3-4: PENDING (unapproved) product SKU must never appear in public GET /products?sku_ids=...
		pendCreateResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/products", seller1.AccessToken, nil, CreateProductRequest{
			CategoryID:  catID,
			Name:        "GW W3A Pending Item " + env.runID,
			Description: "Unapproved product for D8 PENDING filter check",
			Images:      []string{"https://sandbox.nexusmart.local/img/pending.png"},
			SKUs: []SKUInputDTO{
				{
					SKUCode:     env.uniqueCode("PEND"),
					Name:        "Pending Default",
					PriceVND:    180000,
					Stock:       10,
					WeightGrams: 300,
				},
			},
		}, http.StatusCreated, http.StatusOK)
		var pendCreateOut ProductResponse
		if err := DecodeJSON(pendCreateResp, &pendCreateOut); err != nil || len(pendCreateOut.Product.SKUs) == 0 {
			t.Fatalf("create pending product failed: err=%v body=%s", err, string(pendCreateResp.Body))
		}
		skuPendID := pendCreateOut.Product.SKUs[0].ID

		pendOnlyResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/products?sku_ids="+url.QueryEscape(skuPendID), "", nil, nil, http.StatusOK)
		var pendOnlyOut struct {
			Items []ProductDTO `json:"items"`
		}
		if err := DecodeJSON(pendOnlyResp, &pendOnlyOut); err != nil {
			t.Fatalf("decode pending sku_ids: %v", err)
		}
		if len(pendOnlyOut.Items) != 0 {
			t.Fatalf("GET /products?sku_ids=<pending> expected 0 items, got %d (%s)", len(pendOnlyOut.Items), string(pendOnlyResp.Body))
		}

		mixedPendResp := env.mustDoJSON(t, http.MethodGet,
			fmt.Sprintf("/api/v1/products?sku_ids=%s,%s", url.QueryEscape(sku1.ID), url.QueryEscape(skuPendID)),
			"", nil, nil, http.StatusOK)
		var mixedPendOut struct {
			Items []ProductDTO `json:"items"`
		}
		if err := DecodeJSON(mixedPendResp, &mixedPendOut); err != nil {
			t.Fatalf("decode mixed pending sku_ids: %v", err)
		}
		if len(mixedPendOut.Items) != 1 || mixedPendOut.Items[0].ID != prod1.ID {
			t.Fatalf("GET /products?sku_ids=<approved>,<pending> expected only %s, got %+v (%s)",
				prod1.ID, mixedPendOut.Items, string(mixedPendResp.Body))
		}

		// Validation errors -> 400 INVALID_ARGUMENT.
		tooMany := make([]string, 201)
		for i := range tooMany {
			tooMany[i] = fmt.Sprintf("sku-e2e-%03d", i+1)
		}
		badPaths := []string{
			"/api/v1/products?sku_ids=",
			fmt.Sprintf("/api/v1/products?sku_ids=%s,,%s", url.QueryEscape(sku1.ID), url.QueryEscape(sku2.ID)),
			"/api/v1/products?sku_ids=" + url.QueryEscape("bad;sku"),
			"/api/v1/products?sku_ids=" + strings.Join(tooMany, ","),
		}
		for _, p := range badPaths {
			badResp := env.mustDoJSON(t, http.MethodGet, p, "", nil, nil, http.StatusBadRequest)
			gwW3AAssertErrorCode(t, "GET "+p, badResp.Body, "INVALID_ARGUMENT")
		}
	})
}
