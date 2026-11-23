package e2e

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
)

type prodFixSKUInput struct {
	ID          string            `json:"id,omitempty"`
	SKUCode     string            `json:"sku_code"`
	Name        string            `json:"name"`
	Attributes  map[string]string `json:"attributes,omitempty"`
	PriceVND    int64             `json:"price_vnd"`
	Stock       int32             `json:"stock"`
	WeightGrams int32             `json:"weight_grams"`
	ImageURL    string            `json:"image_url,omitempty"`
}

type prodFixUpdateProductReq struct {
	CategoryID  string            `json:"category_id,omitempty"`
	Name        string            `json:"name,omitempty"`
	Description string            `json:"description,omitempty"`
	Status      string            `json:"status,omitempty"`
	SKUs        []prodFixSKUInput `json:"skus,omitempty"`
}

type prodFixListResponse struct {
	Items    []ProductDTO `json:"items"`
	Page     int32        `json:"page"`
	PageSize int32        `json:"page_size"`
	Total    int64        `json:"total"`
}

type prodFixUpdateStockReq struct {
	NewStock        int32 `json:"new_stock"`
	ExpectedVersion int64 `json:"expected_version"`
}

func prodFixHasProductID(items []ProductDTO, targetID string) bool {
	for _, it := range items {
		if it.ID == targetID {
			return true
		}
	}
	return false
}

func prodFixCreateMultiSKUApprovedProduct(
	t *testing.T,
	env *testEnv,
	seller *sellerFixture,
	categoryID string,
	name string,
	description string,
	skus []SKUInputDTO,
) ProductDTO {
	t.Helper()
	createResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/products", seller.AccessToken, nil, CreateProductRequest{
		CategoryID:  categoryID,
		Name:        name,
		Description: description,
		Images:      []string{"https://sandbox.nexusmart.local/img/p18.png"},
		SKUs:        skus,
	}, http.StatusCreated, http.StatusOK)

	var created ProductResponse
	if err := DecodeJSON(createResp, &created); err != nil {
		t.Fatalf("decode create product %q: %v", name, err)
	}

	adminTok := env.adminLogin(t)
	reviewPath := fmt.Sprintf("/api/v1/admin/products/%s/review", url.PathEscape(created.Product.ID))
	revResp := env.mustDoJSON(t, http.MethodPost, reviewPath, adminTok, nil, ReviewRequest{
		Decision: "APPROVE",
		Reason:   "E2E product rules approval",
	}, http.StatusOK)

	var reviewed ProductResponse
	if err := DecodeJSON(revResp, &reviewed); err != nil {
		t.Fatalf("decode review product %q: %v", name, err)
	}
	if !StatusEquals(reviewed.Product.Status, "APPROVED") {
		t.Fatalf("expected APPROVED for %q, got %q", name, reviewed.Product.Status)
	}
	return reviewed.Product
}

func prodFixSearchProducts(t *testing.T, env *testEnv, query url.Values) prodFixListResponse {
	t.Helper()
	path := "/api/v1/products?" + query.Encode()
	resp := env.mustDoJSON(t, http.MethodGet, path, "", nil, nil, http.StatusOK)
	var out prodFixListResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return out
}

func TestE2E_ProductRules_AccentSearchStockSyncReactivateAndGuards(t *testing.T) {
	env := getEnv()
	cat, seller1, _ := env.ensureBaseMarketplace(t)

	// --- 1. R10 (CAT-B1): Accent-insensitive Vietnamese search + wildcard escaping ---
	tag := env.uniqueCode("P18")
	tee := prodFixCreateMultiSKUApprovedProduct(
		t, env, seller1, cat.ID,
		"Áo thun Cotton Compact "+tag,
		"Vải cotton thoáng mát "+tag,
		[]SKUInputDTO{{SKUCode: env.uniqueCode("AT"), Name: "Trắng / M", PriceVND: 179000, Stock: 10, WeightGrams: 220}},
	)
	dress := prodFixCreateMultiSKUApprovedProduct(
		t, env, seller1, cat.ID,
		"Đầm Linen Dáng Suông "+tag,
		"Ưu đãi 50% chất liệu_linen "+tag,
		[]SKUInputDTO{{SKUCode: env.uniqueCode("DM"), Name: "Kem / S", PriceVND: 420000, Stock: 8, WeightGrams: 300}},
	)

	// "ao thun" finds "Áo thun..."
	aoRes := prodFixSearchProducts(t, env, url.Values{
		"keyword":   {"ao thun"},
		"page":      {"1"},
		"page_size": {"100"},
	})
	if aoRes.Total < 1 || !prodFixHasProductID(aoRes.Items, tee.ID) || prodFixHasProductID(aoRes.Items, dress.ID) {
		t.Fatalf("expected keyword='ao thun' to include %s and exclude %s (total=%d)", tee.ID, dress.ID, aoRes.Total)
	}

	// "dam" finds "Đầm..." (handles đ/Đ + diacritics) without assuming Total == 1 on shared data
	damRes := prodFixSearchProducts(t, env, url.Values{
		"keyword":     {"dam"},
		"category_id": {cat.ID},
		"page":        {"1"},
		"page_size":   {"100"},
	})
	if damRes.Total < 1 || !prodFixHasProductID(damRes.Items, dress.ID) || prodFixHasProductID(damRes.Items, tee.ID) {
		t.Fatalf("expected keyword='dam' in category %s to include %s and exclude %s, got total=%d items=%+v", cat.ID, dress.ID, tee.ID, damRes.Total, damRes.Items)
	}

	// "%" and "_" must be escaped (must not match products lacking literal % or _)
	pctRes := prodFixSearchProducts(t, env, url.Values{
		"keyword":     {"%"},
		"category_id": {cat.ID},
		"page":        {"1"},
		"page_size":   {"100"},
	})
	if !prodFixHasProductID(pctRes.Items, dress.ID) {
		t.Fatalf("expected keyword='%%' to match product with literal '50%%' (%s)", dress.ID)
	}
	if prodFixHasProductID(pctRes.Items, tee.ID) {
		t.Fatalf("keyword='%%' acted as SQL wildcard and matched %s", tee.ID)
	}

	usRes := prodFixSearchProducts(t, env, url.Values{
		"keyword":     {"_"},
		"category_id": {cat.ID},
		"page":        {"1"},
		"page_size":   {"100"},
	})
	if !prodFixHasProductID(usRes.Items, dress.ID) {
		t.Fatalf("expected keyword='_' to match product with literal '_' (%s)", dress.ID)
	}
	if prodFixHasProductID(usRes.Items, tee.ID) {
		t.Fatalf("keyword='_' acted as SQL wildcard and matched %s", tee.ID)
	}

	// --- 2. CAT-B4: PUT stock change uses optimistic path, records stock_history, syncs OUT_OF_STOCK/APPROVED ---
	skuCode1 := env.uniqueCode("B4A")
	skuCode2 := env.uniqueCode("B4B")
	multi := prodFixCreateMultiSKUApprovedProduct(
		t, env, seller1, cat.ID,
		"Áo Polo Kiểm Tồn "+tag,
		"Sản phẩm test PUT đổi tồn",
		[]SKUInputDTO{
			{SKUCode: skuCode1, Name: "Đen / M", PriceVND: 150000, Stock: 5, WeightGrams: 220},
			{SKUCode: skuCode2, Name: "Trắng / L", PriceVND: 250000, Stock: 3, WeightGrams: 240},
		},
	)
	var sku1, sku2 SKUDTO
	for _, s := range multi.SKUs {
		switch s.SKUCode {
		case skuCode1:
			sku1 = s
		case skuCode2:
			sku2 = s
		}
	}
	if sku1.ID == "" || sku2.ID == "" {
		t.Fatalf("missing created SKUs in product response: %+v", multi.SKUs)
	}
	putPath := fmt.Sprintf("/api/v1/seller/products/%s", url.PathEscape(multi.ID))

	// Set all SKUs to 0 via PUT (even with status=APPROVED from seller form) -> OUT_OF_STOCK + version bump.
	zeroResp := env.mustDoJSON(t, http.MethodPut, putPath, seller1.AccessToken, nil, prodFixUpdateProductReq{
		Status: "APPROVED",
		SKUs: []prodFixSKUInput{
			{ID: sku1.ID, SKUCode: skuCode1, Name: "Đen / M", PriceVND: 150000, Stock: 0, WeightGrams: 220},
			{ID: sku2.ID, SKUCode: skuCode2, Name: "Trắng / L", PriceVND: 250000, Stock: 0, WeightGrams: 240},
		},
	}, http.StatusOK)
	var zeroOut ProductResponse
	if err := DecodeJSON(zeroResp, &zeroOut); err != nil {
		t.Fatalf("decode zero stock PUT: %v", err)
	}
	if !StatusEquals(zeroOut.Product.Status, "OUT_OF_STOCK") {
		t.Fatalf("expected product status OUT_OF_STOCK after PUT stock=0, got %q", zeroOut.Product.Status)
	}
	for _, s := range zeroOut.Product.SKUs {
		if s.ID == sku1.ID && s.Version <= sku1.Version {
			t.Fatalf("expected sku1 version bump on PUT stock change: before=%d after=%d", sku1.Version, s.Version)
		}
		if s.ID == sku2.ID && s.Version <= sku2.Version {
			t.Fatalf("expected sku2 version bump on PUT stock change: before=%d after=%d", sku2.Version, s.Version)
		}
	}

	// Seller cannot force OUT_OF_STOCK -> APPROVED while total stock is still 0.
	env.mustDoJSON(t, http.MethodPut, putPath, seller1.AccessToken, nil, prodFixUpdateProductReq{
		Status: "APPROVED",
	}, http.StatusConflict)

	// Restock sku1 via PUT -> product automatically returns to APPROVED.
	restockResp := env.mustDoJSON(t, http.MethodPut, putPath, seller1.AccessToken, nil, prodFixUpdateProductReq{
		Status: "OUT_OF_STOCK",
		SKUs: []prodFixSKUInput{
			{ID: sku1.ID, SKUCode: skuCode1, Name: "Đen / M", PriceVND: 150000, Stock: 6, WeightGrams: 220},
			{ID: sku2.ID, SKUCode: skuCode2, Name: "Trắng / L", PriceVND: 250000, Stock: 0, WeightGrams: 240},
		},
	}, http.StatusOK)
	var restockOut ProductResponse
	if err := DecodeJSON(restockResp, &restockOut); err != nil {
		t.Fatalf("decode restock PUT: %v", err)
	}
	if !StatusEquals(restockOut.Product.Status, "APPROVED") {
		t.Fatalf("expected product status APPROVED after PUT restock, got %q", restockOut.Product.Status)
	}
	var sku2AfterZero SKUDTO
	for _, s := range restockOut.Product.SKUs {
		if s.ID == sku2.ID {
			sku2AfterZero = s
			break
		}
	}
	if sku2AfterZero.ID == "" {
		t.Fatalf("missing sku2 %s in restock response: %+v", sku2.ID, restockOut.Product.SKUs)
	}

	// --- 3. CAT-B11 & CAT-B6: Deactivate sku2 -> PATCH returns 409 -> re-add same sku_code succeeds ---
	env.mustDoJSON(t, http.MethodPut, putPath, seller1.AccessToken, nil, prodFixUpdateProductReq{
		SKUs: []prodFixSKUInput{
			{ID: sku1.ID, SKUCode: skuCode1, Name: "Đen / M", PriceVND: 150000, Stock: 6, WeightGrams: 220},
		},
	}, http.StatusOK)

	// CAT-B11: PATCH stock on INACTIVE sku2 must return 409.
	patchPath := fmt.Sprintf("/api/v1/seller/skus/%s/stock", url.PathEscape(sku2.ID))
	env.mustDoJSON(t, http.MethodPatch, patchPath, seller1.AccessToken, nil, prodFixUpdateStockReq{
		NewStock:        9,
		ExpectedVersion: sku2AfterZero.Version,
	}, http.StatusConflict)

	// CAT-B6: Re-adding variant with same sku_code reactivates old SKU (no 409).
	readdResp := env.mustDoJSON(t, http.MethodPut, putPath, seller1.AccessToken, nil, prodFixUpdateProductReq{
		SKUs: []prodFixSKUInput{
			{ID: sku1.ID, SKUCode: skuCode1, Name: "Đen / M", PriceVND: 150000, Stock: 6, WeightGrams: 220},
			{SKUCode: skuCode2, Name: "Trắng / L mới", PriceVND: 260000, Stock: 4, WeightGrams: 245},
		},
	}, http.StatusOK)
	var readdOut ProductResponse
	if err := DecodeJSON(readdResp, &readdOut); err != nil {
		t.Fatalf("decode re-add SKU PUT: %v", err)
	}
	if len(readdOut.Product.SKUs) != 2 {
		t.Fatalf("expected 2 SKUs after re-adding %s, got %d", skuCode2, len(readdOut.Product.SKUs))
	}
	var reactivated *SKUDTO
	for i := range readdOut.Product.SKUs {
		if readdOut.Product.SKUs[i].SKUCode == skuCode2 {
			reactivated = &readdOut.Product.SKUs[i]
			break
		}
	}
	if reactivated == nil {
		t.Fatalf("expected reactivated SKU %s in response: %+v", skuCode2, readdOut.Product.SKUs)
	}
	if reactivated.ID != sku2.ID {
		t.Fatalf("expected reactivated SKU to reuse original id %s, got %s", sku2.ID, reactivated.ID)
	}
	if reactivated.Name != "Trắng / L mới" || reactivated.PriceVND != 260000 || reactivated.Stock != 4 {
		t.Fatalf("unexpected reactivated SKU fields: %+v", reactivated)
	}

	// --- 4. CAT-B10: Admin REJECT with empty reason must return 400 ---
	pendingResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/products", seller1.AccessToken, nil, CreateProductRequest{
		CategoryID:  cat.ID,
		Name:        "Sản phẩm kiểm từ chối " + tag,
		Description: "Test CAT-B10",
		Images:      []string{"https://sandbox.nexusmart.local/img/p18.png"},
		SKUs:        []SKUInputDTO{{SKUCode: env.uniqueCode("B10"), Name: "Std", PriceVND: 120000, Stock: 5, WeightGrams: 200}},
	}, http.StatusCreated, http.StatusOK)
	var pendingOut ProductResponse
	if err := DecodeJSON(pendingResp, &pendingOut); err != nil {
		t.Fatalf("decode pending product: %v", err)
	}

	adminTok := env.adminLogin(t)
	reviewPath := fmt.Sprintf("/api/v1/admin/products/%s/review", url.PathEscape(pendingOut.Product.ID))
	env.mustDoJSON(t, http.MethodPost, reviewPath, adminTok, nil, ReviewRequest{
		Decision: "REJECT",
		Reason:   "",
	}, http.StatusBadRequest)
	env.mustDoJSON(t, http.MethodPost, reviewPath, adminTok, nil, ReviewRequest{
		Decision: "REJECT",
		Reason:   "   ",
	}, http.StatusBadRequest)
	env.mustDoJSON(t, http.MethodPost, reviewPath, adminTok, nil, ReviewRequest{
		Decision: "REJECT",
		Reason:   "Thiếu ảnh thực tế sản phẩm",
	}, http.StatusOK)
}
