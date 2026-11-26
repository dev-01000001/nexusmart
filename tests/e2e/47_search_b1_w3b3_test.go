package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	searchv1 "github.com/nexusmart/core/gen/go/nexusmart/search/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// --- W3B3 B1 Search (G9–G14) DTOs & Helpers ---

type w3b47SearchHitDTO struct {
	ID                 string   `json:"id"`
	ShopID             string   `json:"shop_id"`
	ShopName           string   `json:"shop_name"`
	ShopSlug           string   `json:"shop_slug"`
	CategoryID         string   `json:"category_id"`
	CategorySlug       string   `json:"category_slug"`
	CategoryName       string   `json:"category_name"`
	Name               string   `json:"name"`
	Slug               string   `json:"slug"`
	ThumbnailURL       string   `json:"thumbnail_url"`
	MinPriceVND        int64    `json:"min_price_vnd"`
	MaxPriceVND        int64    `json:"max_price_vnd"`
	Status             string   `json:"status"`
	RatingAvgX100      int32    `json:"rating_avg_x100"`
	ReviewCount        int32    `json:"review_count"`
	InStock            bool     `json:"in_stock"`
	SKUCount           int32    `json:"sku_count"`
	Score              float64  `json:"score"`
	DescriptionSnippet string   `json:"description_snippet"`
	SKUIDs             []string `json:"sku_ids"`
	DefaultSKUID       string   `json:"default_sku_id"`
	Colors             []string `json:"colors"`
	Sizes              []string `json:"sizes"`
}

type w3b47FacetCountDTO struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Slug  string `json:"slug"`
	Count int64  `json:"count"`
}

type w3b47SearchResponse struct {
	Items          []w3b47SearchHitDTO  `json:"items"`
	Total          int64                `json:"total"`
	Page           int32                `json:"page"`
	PageSize       int32                `json:"page_size"`
	CategoryFacets []w3b47FacetCountDTO `json:"category_facets"`
	ShopFacets     []w3b47FacetCountDTO `json:"shop_facets"`
	DidYouMean     string               `json:"did_you_mean"`
	DegradedMode   string               `json:"degraded_mode"`
	UsedVector     bool                 `json:"used_vector"`
}

type w3b47SuggestEntityDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type w3b47SuggestResponse struct {
	Keywords   []string                `json:"keywords"`
	Categories []w3b47SuggestEntityDTO `json:"categories"`
	Shops      []w3b47SuggestEntityDTO `json:"shops"`
	Trending   []string                `json:"trending"`
	Products   []w3b47SearchHitDTO     `json:"products"`
}

type w3b47HistoryItemDTO struct {
	ID        string `json:"id"`
	Query     string `json:"query"`
	CreatedAt string `json:"created_at"`
}

type w3b47HistoryResponse struct {
	Items []w3b47HistoryItemDTO `json:"items"`
}

type w3b47ReindexResponse struct {
	JobID             string `json:"job_id"`
	Status            string `json:"status"`
	ProductsIndexed   int64  `json:"products_indexed"`
	EmbeddingsUpdated int64  `json:"embeddings_updated"`
	ErrorMessage      string `json:"error_message"`
	StartedAt         string `json:"started_at"`
	FinishedAt        string `json:"finished_at"`
}

func w3b47DialSearchGRPC(t *testing.T, env *testEnv) searchv1.SearchServiceClient {
	t.Helper()
	conn, err := grpc.NewClient(env.cfg.SearchGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial search gRPC %s: %v", env.cfg.SearchGRPCAddr, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return searchv1.NewSearchServiceClient(conn)
}

func w3b47Search(t *testing.T, env *testEnv, token string, params url.Values) (w3b47SearchResponse, []byte) {
	t.Helper()
	path := "/api/v1/search"
	if len(params) > 0 {
		path += "?" + params.Encode()
	}
	resp := env.mustDoJSON(t, http.MethodGet, path, token, nil, nil, http.StatusOK)
	var out w3b47SearchResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode GET %s: %v (body=%s)", path, err, string(resp.Body))
	}
	return out, resp.Body
}

func w3b47Suggest(t *testing.T, env *testEnv, token, query string, limit int) (w3b47SuggestResponse, []byte, time.Duration) {
	t.Helper()
	v := url.Values{}
	if query != "" {
		v.Set("q", query)
	}
	if limit > 0 {
		v.Set("limit", fmt.Sprintf("%d", limit))
	}
	path := "/api/v1/search/suggest"
	if len(v) > 0 {
		path += "?" + v.Encode()
	}
	start := time.Now()
	resp := env.mustDoJSON(t, http.MethodGet, path, token, nil, nil, http.StatusOK)
	elapsed := time.Since(start)
	var out w3b47SuggestResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode GET %s: %v (body=%s)", path, err, string(resp.Body))
	}
	return out, resp.Body, elapsed
}

func w3b47FindHit(items []w3b47SearchHitDTO, productID string) (*w3b47SearchHitDTO, bool) {
	for i := range items {
		if items[i].ID == productID {
			return &items[i], true
		}
	}
	return nil, false
}

func w3b47CreateProductWithSKUs(
	t *testing.T,
	env *testEnv,
	seller *sellerFixture,
	categoryID, name, description string,
	skus []SKUInputDTO,
	approve bool,
) ProductDTO {
	t.Helper()
	resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/products", seller.AccessToken, nil, CreateProductRequest{
		CategoryID:  categoryID,
		Name:        name,
		Description: description,
		Images:      []string{"https://sandbox.nexusmart.local/img/search.png"},
		SKUs:        skus,
	}, http.StatusCreated)
	var out ProductResponse
	if err := DecodeJSON(resp, &out); err != nil || out.Product.ID == "" {
		t.Fatalf("create product %q: err=%v body=%s", name, err, string(resp.Body))
	}
	prod := out.Product
	if approve {
		adminTok := env.adminLogin(t)
		revResp := env.mustDoJSON(t, http.MethodPost,
			fmt.Sprintf("/api/v1/admin/products/%s/review", url.PathEscape(prod.ID)),
			adminTok, nil, map[string]string{
				"decision": "APPROVE",
				"reason":   "w3b47 e2e search approval",
			}, http.StatusOK)
		var revOut ProductResponse
		if err := DecodeJSON(revResp, &revOut); err != nil {
			t.Fatalf("approve product %s: %v", prod.ID, err)
		}
		prod = revOut.Product
	}
	return prod
}

func w3b47SetProductStatus(t *testing.T, env *testEnv, seller *sellerFixture, prod ProductDTO, newStatus string) {
	t.Helper()
	skus := make([]map[string]any, 0, len(prod.SKUs))
	for _, sk := range prod.SKUs {
		skus = append(skus, map[string]any{
			"id":           sk.ID,
			"sku_code":     sk.SKUCode,
			"name":         sk.Name,
			"attributes":   sk.Attributes,
			"price_vnd":    sk.PriceVND,
			"stock":        sk.Stock,
			"weight_grams": sk.WeightGrams,
		})
	}
	path := fmt.Sprintf("/api/v1/seller/products/%s", url.PathEscape(prod.ID))
	env.mustDoJSON(t, http.MethodPut, path, seller.AccessToken, nil, map[string]any{
		"category_id": prod.CategoryID,
		"name":        prod.Name,
		"description": prod.Description,
		"images":      []string{"https://sandbox.nexusmart.local/img/search.png"},
		"status":      newStatus,
		"skus":        skus,
	}, http.StatusOK)
}

func w3b47CreateCategory(t *testing.T, env *testEnv, adminTok, name, slug string) CategoryDTO {
	t.Helper()
	resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/categories", adminTok, nil, map[string]any{
		"name":                name,
		"slug":                slug,
		"commission_rate_bps": 500,
		"sort_order":          99,
	}, http.StatusCreated)
	var out CategoryResponse
	if err := DecodeJSON(resp, &out); err != nil || out.Category.ID == "" {
		t.Fatalf("create category %q: err=%v body=%s", name, err, string(resp.Body))
	}
	return out.Category
}

func w3b47EnsureShopUnsuspendedCleanup(t *testing.T, env *testEnv, shopID string, isSuspended *bool) {
	t.Helper()
	t.Cleanup(func() {
		if !*isSuspended {
			return
		}
		cleanCtx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		cleanTok, err := env.adminTokenCtx(cleanCtx)
		if err != nil {
			t.Errorf("cleanup admin login for shop %s unsuspend: %v", shopID, err)
			return
		}
		resp, err := env.client.DoJSON(cleanCtx, http.MethodPost,
			fmt.Sprintf("/api/v1/admin/shops/%s/unsuspend", url.PathEscape(shopID)),
			cleanTok, nil, map[string]string{"reason": "w3b47 cleanup restore shop"})
		if err != nil || (resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusConflict) {
			t.Errorf("cleanup unsuspend shop %s failed: err=%v resp=%+v", shopID, err, resp)
		}
	})
}

func w3b47SuspendShopTwoPerson(t *testing.T, env *testEnv, makerTok, checkerTok, shopID, reason string) {
	t.Helper()
	reqResp := env.mustDoJSON(t, http.MethodPost,
		fmt.Sprintf("/api/v1/admin/shops/%s/suspend", url.PathEscape(shopID)),
		makerTok, nil, map[string]string{"reason": reason}, http.StatusCreated)
	var reqOut struct {
		Approval struct {
			ID string `json:"id"`
		} `json:"approval"`
	}
	if err := DecodeJSON(reqResp, &reqOut); err != nil || reqOut.Approval.ID == "" {
		t.Fatalf("decode suspend shop approval: err=%v body=%s", err, string(reqResp.Body))
	}
	env.mustDoJSON(t, http.MethodPost,
		fmt.Sprintf("/api/v1/admin/approvals/shop/%s/decide", url.PathEscape(reqOut.Approval.ID)),
		checkerTok, nil, map[string]string{"decision": "APPROVE", "note": "w3b47 approve suspension"}, http.StatusOK)
}

func w3b47AssertSearchHealth(t *testing.T, env *testEnv) {
	t.Helper()
	for _, ep := range []string{"/healthz", "/readyz"} {
		resp, err := env.client.DoAbsolute(env.ctx(t), http.MethodGet, env.cfg.SearchHTTPURL+ep, "", nil, nil)
		if err != nil {
			t.Fatalf("search %s failed: %v", ep, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("search %s returned HTTP %d: %s", ep, resp.StatusCode, string(resp.Body))
		}
	}
}

// =================================================================================================
// 1. Product Sync (<5s), Size/Color Filters, Category Update, Real Rating (<5s),
//    Visibility Transitions (HIDDEN/DELETE/REJECT <5s), and Notification DLQ Safety (G9, G11)
// =================================================================================================

func TestE2E_W3B3_B1_ProductSyncRatingCategoryAndNotificationSafety(t *testing.T) {
	env := getEnv()
	w3b47AssertSearchHealth(t, env)

	adminTok := env.adminLogin(t)
	_, seller1, _ := env.ensureBaseMarketplace(t)
	w3b46EnsureKnownShippingAndPaymentState(t, env, adminTok, seller1)
	buyer := env.ensureVerifiedBuyer(t)
	addr := env.addAddress(t, buyer)

	dlqStartSeq := w3b43DLQNextSeq(t, env.cfg.NATSURL)

	tag := w3b41AlphaTag(env)
	catOrigName := "Danh Mục Search " + tag
	catRenamed := "Danh Mục Đã Đổi " + tag
	testCat := w3b47CreateCategory(t, env, adminTok, catOrigName, "cat-search-"+tag)

	// 1. Create PENDING multi-variant product -> MUST NOT appear in /api/v1/search (anonymous or admin JWT).
	prodName := "Áo Khoác Gió Search " + tag
	skuCode1 := env.uniqueCode("SRC1")
	skuCode2 := env.uniqueCode("SRC2")
	prod := w3b47CreateProductWithSKUs(t, env, seller1, testCat.ID, prodName, "Áo khoác chống gió nhẹ cho chạy bộ", []SKUInputDTO{
		{
			SKUCode:     skuCode1,
			Name:        "Đen than - XL (60kg)",
			Attributes:  map[string]string{"size": "XL (60kg)", "color": "Đen than", "color_code": "den"},
			PriceVND:    199000,
			Stock:       25,
			WeightGrams: 350,
			ImageURL:    "https://sandbox.nexusmart.local/img/search-1.png",
		},
		{
			SKUCode:     skuCode2,
			Name:        "Xanh rêu - M",
			Attributes:  map[string]string{"size": "M", "color": "Xanh rêu", "color_code": "reu"},
			PriceVND:    349000,
			Stock:       15,
			WeightGrams: 350,
			ImageURL:    "https://sandbox.nexusmart.local/img/search-2.png",
		},
	}, false)

	for label, tok := range map[string]string{"anonymous": "", "admin_jwt": adminTok} {
		res, _ := w3b47Search(t, env, tok, url.Values{"q": {tag}, "shop_id": {seller1.ShopID}})
		if _, found := w3b47FindHit(res.Items, prod.ID); found {
			t.Fatalf("G10/G11 PENDING product %s leaked in search (%s): %+v", prod.ID, label, res.Items)
		}
	}

	// 2. Admin APPROVE -> MUST appear in /api/v1/search within STRICT < 5s, with exact prices & zero commission_rate_bps.
	env.mustDoJSON(t, http.MethodPost,
		fmt.Sprintf("/api/v1/admin/products/%s/review", url.PathEscape(prod.ID)),
		adminTok, nil, map[string]string{"decision": "APPROVE", "reason": "w3b47 sync test"}, http.StatusOK)

	var approvedHit *w3b47SearchHitDTO
	var approvedRaw []byte
	env.pollUntil(t, 5*time.Second, 150*time.Millisecond, "approved product synced to search (<5s)", func() (bool, string) {
		res, raw := w3b47Search(t, env, "", url.Values{"q": {tag}, "shop_id": {seller1.ShopID}})
		if h, ok := w3b47FindHit(res.Items, prod.ID); ok {
			approvedHit = h
			approvedRaw = raw
			return res.DegradedMode == "", fmt.Sprintf("degraded_mode=%q", res.DegradedMode)
		}
		return false, fmt.Sprintf("total=%d", res.Total)
	})

	if approvedHit.MinPriceVND != 199000 || approvedHit.MaxPriceVND != 349000 || !approvedHit.InStock || approvedHit.SKUCount != 2 {
		t.Fatalf("G9 search hit projection mismatch: %+v", approvedHit)
	}
	if strings.Contains(string(approvedRaw), "commission_rate_bps") || strings.Contains(string(approvedRaw), "flagged_keywords") {
		t.Fatalf("G9 public search response leaked restricted fields: %s", string(approvedRaw))
	}

	// 3. Manager review: size="XL (60kg)" (parentheses allowed!) + color by color_code AND normalized Vietnamese name.
	for _, tc := range []struct {
		name   string
		params url.Values
	}{
		{"size_with_parentheses", url.Values{"q": {tag}, "shop_id": {seller1.ShopID}, "size": {"XL (60kg)"}}},
		{"color_by_code_den", url.Values{"q": {tag}, "shop_id": {seller1.ShopID}, "color": {"den"}}},
		{"color_by_vietnamese_name", url.Values{"q": {tag}, "shop_id": {seller1.ShopID}, "color": {"Đen than"}}},
		{"color_by_code_reu", url.Values{"q": {tag}, "shop_id": {seller1.ShopID}, "color": {"reu"}}},
		{"color_by_name_xanh_reu", url.Values{"q": {tag}, "shop_id": {seller1.ShopID}, "color": {"Xanh rêu"}}},
		{"price_and_in_stock", url.Values{"q": {tag}, "shop_id": {seller1.ShopID}, "min_price_vnd": {"150000"}, "max_price_vnd": {"250000"}, "in_stock_only": {"true"}}},
	} {
		res, raw := w3b47Search(t, env, "", tc.params)
		if _, ok := w3b47FindHit(res.Items, prod.ID); !ok {
			t.Fatalf("B1 filter %s (%s) missed product %s: body=%s", tc.name, tc.params.Encode(), prod.ID, string(raw))
		}
	}

	// 4. product.category_updated: rename category -> search facets / hit category_name updated within < 5s.
	env.mustDoJSON(t, http.MethodPut,
		fmt.Sprintf("/api/v1/admin/categories/%s", url.PathEscape(testCat.ID)),
		adminTok, nil, map[string]any{
			"name": catRenamed,
			"slug": testCat.Slug,
		}, http.StatusOK)

	env.pollUntil(t, 5*time.Second, 150*time.Millisecond, "category rename reflected in search (<5s)", func() (bool, string) {
		res, _ := w3b47Search(t, env, "", url.Values{"q": {tag}, "category_id": {testCat.ID}})
		for _, f := range res.CategoryFacets {
			if f.ID == testCat.ID && f.Name == catRenamed {
				return true, ""
			}
		}
		if h, ok := w3b47FindHit(res.Items, prod.ID); ok && h.CategoryName == catRenamed {
			return true, ""
		}
		return false, fmt.Sprintf("facets=%+v items=%d", res.CategoryFacets, len(res.Items))
	})

	// 5. Real rating (< 5s): complete COD order -> buyer posts 5-star review -> rating_avg_x100=500 & review_count=1.
	delivered := w3b41PlaceShipAndDeliverCOD(t, env, buyer, addr, seller1, prod.SKUs[0])
	env.mustDoJSON(t, http.MethodPost,
		fmt.Sprintf("/api/v1/orders/%s/confirm-received", url.PathEscape(delivered.ID)),
		buyer.AccessToken, nil, nil, http.StatusOK)
	env.pollUntil(t, env.cfg.PollTimeout, 200*time.Millisecond, "order COMPLETED for review", func() (bool, string) {
		cur := env.getOrder(t, buyer.AccessToken, delivered.ID)
		return StatusEquals(cur.Status, "COMPLETED"), cur.Status
	})
	ordFull := env.getOrder(t, buyer.AccessToken, delivered.ID)
	if len(ordFull.Items) == 0 || ordFull.Items[0].ID == "" {
		t.Fatalf("completed order %s missing order items", delivered.ID)
	}
	env.mustDoJSON(t, http.MethodPost,
		fmt.Sprintf("/api/v1/orders/%s/reviews", url.PathEscape(delivered.ID)),
		buyer.AccessToken, nil, map[string]any{
			"order_item_id": ordFull.Items[0].ID,
			"rating":        5,
			"comment":       "Áo khoác rất đẹp và nhẹ, giao hàng nhanh!",
		}, http.StatusCreated)

	env.pollUntil(t, 5*time.Second, 150*time.Millisecond, "real 5-star review synced to search (<5s)", func() (bool, string) {
		res, _ := w3b47Search(t, env, "", url.Values{"q": {tag}, "shop_id": {seller1.ShopID}})
		h, ok := w3b47FindHit(res.Items, prod.ID)
		if !ok {
			return false, "product missing"
		}
		return h.RatingAvgX100 == 500 && h.ReviewCount == 1, fmt.Sprintf("rating=%d count=%d", h.RatingAvgX100, h.ReviewCount)
	})
	// min_rating_x100=400 includes the 5-star product.
	ratedRes, _ := w3b47Search(t, env, "", url.Values{"q": {tag}, "shop_id": {seller1.ShopID}, "min_rating_x100": {"400"}})
	if _, ok := w3b47FindHit(ratedRes.Items, prod.ID); !ok {
		t.Fatalf("G9 min_rating_x100=400 did not return 5-star product %s", prod.ID)
	}

	// 6. HIDDEN -> disappears < 5s; Unhide -> reappears < 5s; DELETE -> disappears < 5s.
	w3b41HideProduct(t, env, seller1, prod)
	env.pollUntil(t, 5*time.Second, 150*time.Millisecond, "HIDDEN product removed from search (<5s)", func() (bool, string) {
		res, _ := w3b47Search(t, env, "", url.Values{"q": {tag}, "shop_id": {seller1.ShopID}})
		_, found := w3b47FindHit(res.Items, prod.ID)
		return !found, fmt.Sprintf("found=%v", found)
	})

	w3b47SetProductStatus(t, env, seller1, prod, "APPROVED")
	env.pollUntil(t, 5*time.Second, 150*time.Millisecond, "unhidden product restored in search (<5s)", func() (bool, string) {
		res, _ := w3b47Search(t, env, "", url.Values{"q": {tag}, "shop_id": {seller1.ShopID}})
		_, found := w3b47FindHit(res.Items, prod.ID)
		return found, fmt.Sprintf("found=%v", found)
	})

	env.mustDoJSON(t, http.MethodDelete, fmt.Sprintf("/api/v1/seller/products/%s", url.PathEscape(prod.ID)), seller1.AccessToken, nil, nil, http.StatusOK)
	env.pollUntil(t, 5*time.Second, 150*time.Millisecond, "deleted product removed from search (<5s)", func() (bool, string) {
		res, _ := w3b47Search(t, env, "", url.Values{"q": {tag}, "shop_id": {seller1.ShopID}})
		_, found := w3b47FindHit(res.Items, prod.ID)
		return !found, fmt.Sprintf("found=%v", found)
	})

	// 7. Admin REJECT on an APPROVED product -> disappears < 5s.
	rejTag := w3b41AlphaTag(env)
	rejProd := w3b47CreateProductWithSKUs(t, env, seller1, testCat.ID, "Áo Chờ Từ Chối "+rejTag, "Sản phẩm kiểm thử reject", []SKUInputDTO{
		{SKUCode: env.uniqueCode("REJ"), Name: "Default", PriceVND: 120000, Stock: 10, WeightGrams: 300},
	}, true)
	env.pollUntil(t, 5*time.Second, 150*time.Millisecond, "reject-candidate indexed (<5s)", func() (bool, string) {
		res, _ := w3b47Search(t, env, "", url.Values{"q": {rejTag}, "shop_id": {seller1.ShopID}})
		_, found := w3b47FindHit(res.Items, rejProd.ID)
		return found, fmt.Sprintf("found=%v", found)
	})
	env.mustDoJSON(t, http.MethodPost,
		fmt.Sprintf("/api/v1/admin/products/%s/review", url.PathEscape(rejProd.ID)),
		adminTok, nil, map[string]string{"decision": "REJECT", "reason": "w3b47 policy rejection test"}, http.StatusOK)
	env.pollUntil(t, 5*time.Second, 150*time.Millisecond, "REJECTED product removed from search (<5s)", func() (bool, string) {
		res, _ := w3b47Search(t, env, "", url.Values{"q": {rejTag}, "shop_id": {seller1.ShopID}})
		_, found := w3b47FindHit(res.Items, rejProd.ID)
		return !found, fmt.Sprintf("found=%v", found)
	})

	// 8. Notification DLQ safety (notification-r0 ca #4): product.upserted / product.deleted / product.category_updated
	//    must NEVER land in NEXUSMART_DLQ.
	for _, subj := range []string{"dlq.product.upserted", "dlq.product.deleted", "dlq.product.category_updated"} {
		msg, _, err := w3bNATSFindDLQSince(env.cfg.NATSURL, subj, dlqStartSeq, func(m *w3bJSStoredMsg) bool {
			return true
		})
		if err != nil {
			t.Fatalf("scan NEXUSMART_DLQ for %s: %v", subj, err)
		}
		if msg != nil {
			t.Fatalf("notification-r0 ca #4 violated: unexpected DLQ message on %s (seq=%d): %s", subj, msg.Seq, string(msg.Data))
		}
	}
}

// =================================================================================================
// 2. Vietnamese Diacritics (đ->d), Typo Tolerance, Synonyms, Hybrid RRF, Exact SKU Priority,
//    and Absolute price_asc/price_desc Keyword Filtering (G12 + Manager Review)
// =================================================================================================

func TestE2E_W3B3_B1_VietnameseTypoSynonymsHybridRRFAndSort(t *testing.T) {
	env := getEnv()
	w3b47AssertSearchHealth(t, env)

	cat, _, _ := env.ensureBaseMarketplace(t)
	searchSeller := env.createApprovedSeller(t, "w3b47-viet-seller")
	tag := w3b41AlphaTag(env)

	exactSKUCode := "EXK" + strings.ToUpper(env.runID) + "99"

	pTeeLight := w3b47CreateProductWithSKUs(t, env, searchSeller, cat.ID,
		"Áo thun cotton thoáng khí "+tag,
		"Áo thun thể thao mặc mát thấm hút mồ hôi cho mùa hè",
		[]SKUInputDTO{{SKUCode: exactSKUCode, Name: "Trắng M", PriceVND: 180000, Stock: 30, WeightGrams: 220}}, true)

	pTeeMid := w3b47CreateProductWithSKUs(t, env, searchSeller, cat.ID,
		"Áo thun thể thao cổ tròn "+tag,
		"Áo thun co giãn bốn chiều vận động thoải mái",
		[]SKUInputDTO{{SKUCode: env.uniqueCode("TM"), Name: "Đen L", PriceVND: 250000, Stock: 20, WeightGrams: 240}}, true)

	pTeePremium := w3b47CreateProductWithSKUs(t, env, searchSeller, cat.ID,
		"Áo thun cotton cao cấp "+tag,
		"Áo thun cotton hữu cơ dày dặn đứng phom",
		[]SKUInputDTO{{SKUCode: env.uniqueCode("TP"), Name: "Xám XL", PriceVND: 420000, Stock: 15, WeightGrams: 260}}, true)

	pSugar := w3b47CreateProductWithSKUs(t, env, searchSeller, cat.ID,
		"Đường phèn hữu cơ "+tag,
		"Đường phèn nguyên chất tinh khiết",
		[]SKUInputDTO{{SKUCode: env.uniqueCode("DG"), Name: "Gói 500g", PriceVND: 95000, Stock: 40, WeightGrams: 500}}, true)

	pBoxer := w3b47CreateProductWithSKUs(t, env, searchSeller, cat.ID,
		"Quần sịp boxer modal "+tag,
		"Quần lót nam sợi gỗ sồi mềm mịn thoáng mát",
		[]SKUInputDTO{{SKUCode: env.uniqueCode("BX"), Name: "Đen L", PriceVND: 150000, Stock: 25, WeightGrams: 180}}, true)

	// Intentionally dirt-cheap off-topic product: must NEVER leak into keyword-filtered price_asc results!
	pCheapUnrelated := w3b47CreateProductWithSKUs(t, env, searchSeller, cat.ID,
		"Bàn chải gỗ nội thất "+tag,
		"Dụng cụ vệ sinh gỗ gia đình",
		[]SKUInputDTO{{SKUCode: env.uniqueCode("BC"), Name: "Gỗ tự nhiên", PriceVND: 15000, Stock: 50, WeightGrams: 150}}, true)

	wantIDs := []string{pTeeLight.ID, pTeeMid.ID, pTeePremium.ID, pSugar.ID, pBoxer.ID, pCheapUnrelated.ID}
	env.pollUntil(t, 5*time.Second, 150*time.Millisecond, "all 6 search test products indexed (<5s)", func() (bool, string) {
		res, _ := w3b47Search(t, env, "", url.Values{"shop_id": {searchSeller.ShopID}, "page_size": {"20"}})
		present := 0
		for _, id := range wantIDs {
			if _, ok := w3b47FindHit(res.Items, id); ok {
				present++
			}
		}
		return present == len(wantIDs) && res.DegradedMode == "", fmt.Sprintf("indexed=%d/%d degraded=%q", present, len(wantIDs), res.DegradedMode)
	})

	// 1. Vietnamese diacritic-insensitive, đ->d, typo tolerance, and synonyms.
	for _, tc := range []struct {
		name     string
		query    string
		wantID   string
		forbidID string
	}{
		{"diacritic_insensitive_ao_thun", "ao thun", pTeeLight.ID, pCheapUnrelated.ID},
		{"d_stroke_duong", "duong", pSugar.ID, pTeeLight.ID},
		{"typo_tolerance_ao_thun", "ao thn", pTeeLight.ID, pCheapUnrelated.ID}, //nolint:misspell // intentional typo query for "ao thun"
		{"synonym_ao_phong_to_ao_thun", "ao phong", pTeeLight.ID, pCheapUnrelated.ID},
		{"synonym_do_lot_to_quan_sip_boxer", "do lot", pBoxer.ID, pCheapUnrelated.ID},
	} {
		res, raw := w3b47Search(t, env, "", url.Values{"q": {tc.query}, "shop_id": {searchSeller.ShopID}, "page_size": {"20"}})
		if res.DegradedMode != "" {
			t.Fatalf("G12 %s degraded_mode=%q, want empty", tc.name, res.DegradedMode)
		}
		if _, ok := w3b47FindHit(res.Items, tc.wantID); !ok {
			t.Fatalf("G12 %s (q=%q) missed expected product %s: body=%s", tc.name, tc.query, tc.wantID, string(raw))
		}
		if tc.forbidID != "" {
			if _, leaked := w3b47FindHit(res.Items, tc.forbidID); leaked {
				t.Fatalf("G12 %s (q=%q) unexpectedly returned unrelated product %s", tc.name, tc.query, tc.forbidID)
			}
		}
	}

	// Unscoped global search sanity check.
	unscoped, _ := w3b47Search(t, env, "", url.Values{"q": {"ao thun"}, "page_size": {"5"}})
	if unscoped.Total == 0 || unscoped.DegradedMode != "" {
		t.Fatalf("G12 unscoped q='ao thun' unexpected total=%d degraded=%q", unscoped.Total, unscoped.DegradedMode)
	}

	// 2. Hybrid RRF (>=3-word descriptive query -> used_vector=true) vs exact SKU keyword priority (used_vector=false).
	for _, mode := range []string{"auto", "hybrid"} {
		hyb, raw := w3b47Search(t, env, "", url.Values{
			"q":       {"ao thun cotton thoang khi"},
			"shop_id": {searchSeller.ShopID},
			"mode":    {mode},
			"sort_by": {"relevance"},
		})
		if !hyb.UsedVector || hyb.DegradedMode != "" {
			t.Fatalf("G12 mode=%s >=3-word query expected used_vector=true degraded='', got used_vector=%v degraded=%q (body=%s)",
				mode, hyb.UsedVector, hyb.DegradedMode, string(raw))
		}
		if _, ok := w3b47FindHit(hyb.Items, pTeeLight.ID); !ok {
			t.Fatalf("G12 mode=%s hybrid query missed primary hit %s: body=%s", mode, pTeeLight.ID, string(raw))
		}
	}

	skuRes, skuRaw := w3b47Search(t, env, "", url.Values{"q": {exactSKUCode}, "mode": {"auto"}})
	if skuRes.UsedVector {
		t.Fatalf("G12 exact SKU query %q must use keyword path (used_vector=false), got true", exactSKUCode)
	}
	if len(skuRes.Items) == 0 || skuRes.Items[0].ID != pTeeLight.ID {
		t.Fatalf("G12 exact SKU query %q expected top hit %s, got body=%s", exactSKUCode, pTeeLight.ID, string(skuRaw))
	}

	// 3. Manager review finalized behavior: price_asc & price_desc with keywords MUST enforce strict price order
	//    AND include ONLY products matching all query words (never off-topic cheap vector neighbors!).
	ascAll, ascAllRaw := w3b47Search(t, env, "", url.Values{
		"q":         {"ao thun"},
		"shop_id":   {searchSeller.ShopID},
		"sort_by":   {"price_asc"},
		"page_size": {"20"},
	})
	if len(ascAll.Items) != 3 {
		t.Fatalf("B1 price_asc q='ao thun' expected 3 matching tees, got %d: %s", len(ascAll.Items), string(ascAllRaw))
	}
	wantAscPrices := []int64{180000, 250000, 420000}
	for i, wantP := range wantAscPrices {
		if ascAll.Items[i].MinPriceVND != wantP {
			t.Fatalf("B1 price_asc[%d]=%d (%s), want %d", i, ascAll.Items[i].MinPriceVND, ascAll.Items[i].Name, wantP)
		}
	}
	if _, leaked := w3b47FindHit(ascAll.Items, pCheapUnrelated.ID); leaked {
		t.Fatalf("B1 price_asc leaked unrelated cheap product %s", pCheapUnrelated.ID)
	}

	descAll, descAllRaw := w3b47Search(t, env, "", url.Values{
		"q":         {"ao thun"},
		"shop_id":   {searchSeller.ShopID},
		"sort_by":   {"price_desc"},
		"page_size": {"20"},
	})
	if len(descAll.Items) != 3 {
		t.Fatalf("B1 price_desc q='ao thun' expected 3 matching tees, got %d: %s", len(descAll.Items), string(descAllRaw))
	}
	wantDescPrices := []int64{420000, 250000, 180000}
	for i, wantP := range wantDescPrices {
		if descAll.Items[i].MinPriceVND != wantP {
			t.Fatalf("B1 price_desc[%d]=%d (%s), want %d", i, descAll.Items[i].MinPriceVND, descAll.Items[i].Name, wantP)
		}
	}

	// >=3-word query with price_asc / price_desc: must match ALL words ("ao", "thun", "cotton") — only 2 items,
	// strictly ordered, excluding pTeeMid (missing "cotton") and pCheapUnrelated (off-topic 15,000 VND)!
	asc3, asc3Raw := w3b47Search(t, env, "", url.Values{
		"q":         {"ao thun cotton"},
		"shop_id":   {searchSeller.ShopID},
		"sort_by":   {"price_asc"},
		"page_size": {"20"},
	})
	if len(asc3.Items) != 2 || asc3.Items[0].ID != pTeeLight.ID || asc3.Items[1].ID != pTeePremium.ID {
		t.Fatalf("B1 >=3-word price_asc q='ao thun cotton' expected [%s(180k), %s(420k)], got %s",
			pTeeLight.ID, pTeePremium.ID, string(asc3Raw))
	}
	desc3, desc3Raw := w3b47Search(t, env, "", url.Values{
		"q":         {"ao thun cotton"},
		"shop_id":   {searchSeller.ShopID},
		"sort_by":   {"price_desc"},
		"page_size": {"20"},
	})
	if len(desc3.Items) != 2 || desc3.Items[0].ID != pTeePremium.ID || desc3.Items[1].ID != pTeeLight.ID {
		t.Fatalf("B1 >=3-word price_desc q='ao thun cotton' expected [%s(420k), %s(180k)], got %s",
			pTeePremium.ID, pTeeLight.ID, string(desc3Raw))
	}
}

// =================================================================================================
// 3. Suspended Shop Visibility Across Search & Suggest (< 5s) + Unsuspend Preserves HIDDEN (G10)
// =================================================================================================

func TestE2E_W3B3_B1_SuspendedShopVisibility(t *testing.T) {
	env := getEnv()
	w3b47AssertSearchHealth(t, env)

	superTok := env.adminLogin(t)
	cat, _, _ := env.ensureBaseMarketplace(t)

	ops1 := w3b45ProvisionStaff(t, t, env, superTok, "w3b47-ops1", "ops")
	ops2 := w3b45ProvisionStaff(t, t, env, superTok, "w3b47-ops2", "ops")

	susSeller := env.createApprovedSeller(t, "w3b47-sus-seller")
	shopSuspended := false
	w3b47EnsureShopUnsuspendedCleanup(t, env, susSeller.ShopID, &shopSuspended)

	tag := w3b41AlphaTag(env)
	pAppr := w3b47CreateProductWithSKUs(t, env, susSeller, cat.ID, "Áo Len Đóng Gian "+tag, "Sản phẩm kiểm thử khoá gian", []SKUInputDTO{
		{SKUCode: env.uniqueCode("SUS1"), Name: "Default", PriceVND: 210000, Stock: 10, WeightGrams: 300},
	}, true)
	pHidden := w3b47CreateProductWithSKUs(t, env, susSeller, cat.ID, "Áo Len Đã Ẩn "+tag, "Sản phẩm ẩn trước khi khoá gian", []SKUInputDTO{
		{SKUCode: env.uniqueCode("SUS2"), Name: "Default", PriceVND: 220000, Stock: 10, WeightGrams: 300},
	}, true)
	w3b41HideProduct(t, env, susSeller, pHidden)

	// Confirm pAppr is indexed and pHidden is absent before suspension.
	env.pollUntil(t, 5*time.Second, 150*time.Millisecond, "approved product visible and hidden product absent pre-suspend", func() (bool, string) {
		res, _ := w3b47Search(t, env, "", url.Values{"q": {tag}})
		_, hasAppr := w3b47FindHit(res.Items, pAppr.ID)
		_, hasHid := w3b47FindHit(res.Items, pHidden.ID)
		return hasAppr && !hasHid, fmt.Sprintf("appr=%v hid=%v", hasAppr, hasHid)
	})

	// 2-person suspend -> MUST vanish from BOTH /search and /search/suggest within STRICT < 5s.
	w3b47SuspendShopTwoPerson(t, env, ops1.AccessToken, ops2.AccessToken, susSeller.ShopID, "w3b47 search suspension test")
	shopSuspended = true

	env.pollUntil(t, 5*time.Second, 150*time.Millisecond, "suspended shop hidden from search & suggest (<5s)", func() (bool, string) {
		sr, _ := w3b47Search(t, env, "", url.Values{"q": {tag}})
		if _, found := w3b47FindHit(sr.Items, pAppr.ID); found {
			return false, "product still in /search?q"
		}
		byShop, _ := w3b47Search(t, env, "", url.Values{"shop_id": {susSeller.ShopID}})
		if byShop.Total != 0 || len(byShop.Items) != 0 {
			return false, fmt.Sprintf("/search?shop_id total=%d", byShop.Total)
		}
		sug, _, _ := w3b47Suggest(t, env, "", tag, 10)
		for _, p := range sug.Products {
			if p.ShopID == susSeller.ShopID {
				return false, "product still in /search/suggest"
			}
		}
		for _, sh := range sug.Shops {
			if sh.ID == susSeller.ShopID {
				return false, "shop still in /search/suggest"
			}
		}
		return true, ""
	})

	// Unsuspend -> pAppr MUST reappear within STRICT < 5s, while seller-HIDDEN pHidden stays hidden!
	env.mustDoJSON(t, http.MethodPost,
		fmt.Sprintf("/api/v1/admin/shops/%s/unsuspend", url.PathEscape(susSeller.ShopID)),
		ops2.AccessToken, nil, map[string]string{"reason": "w3b47 search unsuspend test"}, http.StatusOK)
	shopSuspended = false

	env.pollUntil(t, 5*time.Second, 150*time.Millisecond, "unsuspended shop restores APPROVED only (<5s)", func() (bool, string) {
		sr, _ := w3b47Search(t, env, "", url.Values{"q": {tag}})
		_, hasAppr := w3b47FindHit(sr.Items, pAppr.ID)
		_, hasHid := w3b47FindHit(sr.Items, pHidden.ID)
		return hasAppr && !hasHid, fmt.Sprintf("appr=%v hid=%v", hasAppr, hasHid)
	})
}

// =================================================================================================
// 4. Suggest (< 80ms), Search History + IDOR, Trending PII Privacy, and Filter Injection (G14)
// =================================================================================================

func TestE2E_W3B3_B1_SuggestHistorySecurityAndPrivacy(t *testing.T) {
	env := getEnv()
	w3b47AssertSearchHealth(t, env)

	buyerA := env.registerVerifyAndLogin(t, "w3b47-hist-a")
	buyerB := env.registerVerifyAndLogin(t, "w3b47-hist-b")

	// 1. Suggest latency (< 80ms after warm-up).
	_, _, _ = w3b47Suggest(t, env, "", "ao", 8)
	var fastest time.Duration
	for i := 0; i < 3; i++ {
		_, _, d := w3b47Suggest(t, env, "", "ao", 8)
		if fastest == 0 || d < fastest {
			fastest = d
		}
	}
	if fastest >= 80*time.Millisecond {
		t.Fatalf("G14 suggest latency too slow: fastest of 3 calls = %v (want < 80ms)", fastest)
	}

	// 2. Search history requires auth (401 for anonymous).
	env.mustDoJSON(t, http.MethodGet, "/api/v1/search/history", "", nil, nil, http.StatusUnauthorized)
	env.mustDoJSON(t, http.MethodDelete, "/api/v1/search/history", "", nil, nil, http.StatusUnauthorized)

	// Clear -> search 2 queries -> list -> buyerB IDOR delete blocked -> buyerA deletes 1 -> clears all.
	env.mustDoJSON(t, http.MethodDelete, "/api/v1/search/history", buyerA.AccessToken, nil, nil, http.StatusOK)

	q1 := "ao thun hist " + w3b41AlphaTag(env)
	q2 := "quan short hist " + w3b41AlphaTag(env)
	_, _ = w3b47Search(t, env, buyerA.AccessToken, url.Values{"q": {q1}})
	_, _ = w3b47Search(t, env, buyerA.AccessToken, url.Values{"q": {q2}})

	getHistory := func(tok string) []w3b47HistoryItemDTO {
		t.Helper()
		resp := env.mustDoJSON(t, http.MethodGet, "/api/v1/search/history?limit=20", tok, nil, nil, http.StatusOK)
		var out w3b47HistoryResponse
		if err := DecodeJSON(resp, &out); err != nil {
			t.Fatalf("decode search history: %v (body=%s)", err, string(resp.Body))
		}
		return out.Items
	}

	histA := getHistory(buyerA.AccessToken)
	var id1, id2 string
	for _, it := range histA {
		if it.Query == q1 {
			id1 = it.ID
		}
		if it.Query == q2 {
			id2 = it.ID
		}
	}
	if id1 == "" || id2 == "" {
		t.Fatalf("G14 search history missing saved queries (%q, %q): got %+v", q1, q2, histA)
	}

	// IDOR: buyerB attempting to delete buyerA's history entry must NOT remove it.
	_, _, _ = w3b42RawHTTP(env.ctx(t), t, http.MethodDelete,
		env.cfg.GatewayURL+"/api/v1/search/history/"+url.PathEscape(id1), buyerB.AccessToken, "", nil)
	histAAfterIDOR := getHistory(buyerA.AccessToken)
	stillHasID1 := false
	for _, it := range histAAfterIDOR {
		if it.ID == id1 {
			stillHasID1 = true
		}
	}
	if !stillHasID1 {
		t.Fatalf("G14 IDOR vulnerability: buyerB deleted buyerA's search history item %s", id1)
	}

	// buyerA deletes single item id1 -> only id2 remains.
	env.mustDoJSON(t, http.MethodDelete, "/api/v1/search/history/"+url.PathEscape(id1), buyerA.AccessToken, nil, nil, http.StatusOK)
	histAAfterOne := getHistory(buyerA.AccessToken)
	if len(histAAfterOne) != 1 || histAAfterOne[0].ID != id2 {
		t.Fatalf("G14 single delete expected only %s remaining, got %+v", id2, histAAfterOne)
	}

	// buyerA clears all -> 0 items remain.
	env.mustDoJSON(t, http.MethodDelete, "/api/v1/search/history", buyerA.AccessToken, nil, nil, http.StatusOK)
	if histEmpty := getHistory(buyerA.AccessToken); len(histEmpty) != 0 {
		t.Fatalf("G14 clear all expected 0 items, got %+v", histEmpty)
	}

	// 3. Manager review: PII (phone/email) searched repeatedly MUST NEVER leak into public suggest/trending.
	secretPhone := "0987654321"
	secretEmail := "piileak" + w3b41AlphaTag(env) + "@private.example"
	for i := 0; i < 5; i++ {
		_, _ = w3b47Search(t, env, buyerA.AccessToken, url.Values{"q": {secretPhone}})
		_, _ = w3b47Search(t, env, buyerA.AccessToken, url.Values{"q": {secretEmail}})
	}
	for _, probeQ := range []string{"", "0987", "piileak"} {
		for _, tok := range []string{"", buyerB.AccessToken} {
			_, raw, _ := w3b47Suggest(t, env, tok, probeQ, 20)
			bodyLower := strings.ToLower(string(raw))
			if strings.Contains(bodyLower, secretPhone) || strings.Contains(bodyLower, strings.ToLower(secretEmail)) {
				t.Fatalf("B1 privacy violation: suggest(q=%q) leaked searched phone/email PII: %s", probeQ, string(raw))
			}
		}
	}

	// 4. Filter injection payloads MUST be rejected (400) or return 0 hits without leaking HIDDEN/PENDING items.
	for _, tc := range []struct {
		name  string
		param string
		val   string
	}{
		{"shop_id_injection", "shop_id", `x" OR status="HIDDEN`},
		{"category_id_injection", "category_id", `x" OR status="PENDING`},
		{"size_injection", "size", `M" OR status="HIDDEN`},
		{"color_injection", "color", `den" OR 1=1`},
	} {
		q := url.Values{tc.param: {tc.val}}
		st, _, body := w3b42RawHTTP(env.ctx(t), t, http.MethodGet, env.cfg.GatewayURL+"/api/v1/search?"+q.Encode(), "", "", nil)
		switch st {
		case http.StatusBadRequest:
			// Clean rejection.
		case http.StatusOK:
			var out w3b47SearchResponse
			if err := json.Unmarshal(body, &out); err != nil {
				t.Fatalf("filter injection %s invalid JSON: %v", tc.name, err)
			}
			if len(out.Items) != 0 {
				t.Fatalf("filter injection %s returned %d items (want 0 or 400): %s", tc.name, len(out.Items), string(body))
			}
		default:
			t.Fatalf("filter injection %s unexpected HTTP %d: %s", tc.name, st, string(body))
		}
	}
	// Legitimate attribute value with parentheses MUST succeed with HTTP 200.
	stParen, _, bodyParen := w3b42RawHTTP(env.ctx(t), t, http.MethodGet,
		env.cfg.GatewayURL+"/api/v1/search?"+url.Values{"size": {"XL (60kg)"}}.Encode(), "", "", nil)
	if stParen != http.StatusOK {
		t.Fatalf("B1 size='XL (60kg)' must return 200, got %d: %s", stParen, string(bodyParen))
	}
}

// =================================================================================================
// 5. Reindex RBAC, Tombstone Preservation (No Resurrection), and Direct gRPC Auth (G11)
// =================================================================================================

func TestE2E_W3B3_B1_ReindexAndRBAC(t *testing.T) {
	env := getEnv()
	w3b47AssertSearchHealth(t, env)

	superTok := env.adminLogin(t)
	cat, seller1, _ := env.ensureBaseMarketplace(t)
	buyer := env.ensureVerifiedBuyer(t)

	adminStaff := w3b45ProvisionStaff(t, t, env, superTok, "w3b47-adm", "admin")
	opsStaff := w3b45ProvisionStaff(t, t, env, superTok, "w3b47-ops", "ops")
	modStaff := w3b45ProvisionStaff(t, t, env, superTok, "w3b47-mod", "moderator")
	cskhStaff := w3b45ProvisionStaff(t, t, env, superTok, "w3b47-cskh", "cskh")
	accStaff := w3b45ProvisionStaff(t, t, env, superTok, "w3b47-acc", "accountant")

	// Prepare 1 deleted (tombstoned) product and 1 active product to verify post-reindex invariants.
	delTag := w3b41AlphaTag(env)
	keepTag := w3b41AlphaTag(env)
	delProd := w3b47CreateProductWithSKUs(t, env, seller1, cat.ID, "SP Đã Xoá Reindex "+delTag, "Tombstone check", []SKUInputDTO{
		{SKUCode: env.uniqueCode("TDL"), Name: "Default", PriceVND: 130000, Stock: 5, WeightGrams: 250},
	}, true)
	keepProd := w3b47CreateProductWithSKUs(t, env, seller1, cat.ID, "SP Còn Tồn Tại Reindex "+keepTag, "Active reindex check", []SKUInputDTO{
		{SKUCode: env.uniqueCode("TKP"), Name: "Default", PriceVND: 140000, Stock: 5, WeightGrams: 250},
	}, true)

	env.pollUntil(t, 5*time.Second, 150*time.Millisecond, "reindex test products initially indexed", func() (bool, string) {
		r1, _ := w3b47Search(t, env, "", url.Values{"q": {delTag}, "shop_id": {seller1.ShopID}})
		r2, _ := w3b47Search(t, env, "", url.Values{"q": {keepTag}, "shop_id": {seller1.ShopID}})
		_, ok1 := w3b47FindHit(r1.Items, delProd.ID)
		_, ok2 := w3b47FindHit(r2.Items, keepProd.ID)
		return ok1 && ok2, fmt.Sprintf("del=%v keep=%v", ok1, ok2)
	})

	env.mustDoJSON(t, http.MethodDelete, fmt.Sprintf("/api/v1/seller/products/%s", url.PathEscape(delProd.ID)), seller1.AccessToken, nil, nil, http.StatusOK)
	env.pollUntil(t, 5*time.Second, 150*time.Millisecond, "tombstoned product absent before reindex", func() (bool, string) {
		r1, _ := w3b47Search(t, env, "", url.Values{"q": {delTag}, "shop_id": {seller1.ShopID}})
		_, found := w3b47FindHit(r1.Items, delProd.ID)
		return !found, fmt.Sprintf("found=%v", found)
	})

	// 1. REST RBAC on POST /api/v1/admin/search/reindex.
	env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/search/reindex", "", nil, map[string]any{}, http.StatusUnauthorized)
	for name, tok := range map[string]string{
		"buyer":      buyer.AccessToken,
		"seller":     seller1.AccessToken,
		"moderator":  modStaff.AccessToken,
		"cskh":       cskhStaff.AccessToken,
		"accountant": accStaff.AccessToken,
	} {
		resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/search/reindex", tok, nil, map[string]any{}, http.StatusForbidden)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("G11 %s expected 403 on reindex", name)
		}
	}

	const (
		reindexWaitTimeout  = 5 * time.Minute
		reindexPollInterval = 2 * time.Second
	)

	// Trigger reindex as ops (retrying if a prior reindex job is still releasing the advisory lock).
	var jobID string
	env.pollUntil(t, reindexWaitTimeout, reindexPollInterval, "trigger search reindex job (202)", func() (bool, string) {
		payload, _ := json.Marshal(map[string]any{
			"reindex_products":   true,
			"reindex_embeddings": true,
			"reindex_categories": true,
			"reindex_shops":      true,
			"batch_size":         200,
		})
		st, _, body := w3b42RawHTTP(env.ctx(t), t, http.MethodPost,
			env.cfg.GatewayURL+"/api/v1/admin/search/reindex", opsStaff.AccessToken, "application/json", payload)
		if st == http.StatusConflict {
			w3b42AssertRawErrorCode(t, "concurrent reindex conflict", body, "REINDEX_ALREADY_RUNNING")
			return false, "waiting for active reindex job to finish"
		}
		if st != http.StatusAccepted {
			t.Fatalf("G11 ops reindex want 202, got HTTP %d: %s", st, string(body))
		}
		var out w3b47ReindexResponse
		if err := json.Unmarshal(body, &out); err != nil || out.JobID == "" {
			t.Fatalf("decode reindex 202 response: err=%v body=%s", err, string(body))
		}
		jobID = out.JobID
		return true, ""
	})

	// 2. REST RBAC on GET /api/v1/admin/search/reindex/{job_id}.
	statusPath := "/api/v1/admin/search/reindex/" + url.PathEscape(jobID)
	env.mustDoJSON(t, http.MethodGet, statusPath, "", nil, nil, http.StatusUnauthorized)
	for _, tok := range []string{buyer.AccessToken, seller1.AccessToken, modStaff.AccessToken, cskhStaff.AccessToken, accStaff.AccessToken} {
		env.mustDoJSON(t, http.MethodGet, statusPath, tok, nil, nil, http.StatusForbidden)
	}

	// Poll until COMPLETED using admin staff token.
	var finalJob w3b47ReindexResponse
	env.pollUntil(t, reindexWaitTimeout, reindexPollInterval, "reindex job COMPLETED", func() (bool, string) {
		resp := env.mustDoJSON(t, http.MethodGet, statusPath, adminStaff.AccessToken, nil, nil, http.StatusOK)
		if err := DecodeJSON(resp, &finalJob); err != nil {
			return false, err.Error()
		}
		if StatusEquals(finalJob.Status, "FAILED") {
			t.Fatalf("G11 reindex job %s FAILED: %s", jobID, finalJob.ErrorMessage)
		}
		return StatusEquals(finalJob.Status, "COMPLETED"), fmt.Sprintf("status=%s indexed=%d", finalJob.Status, finalJob.ProductsIndexed)
	})
	if finalJob.ProductsIndexed <= 0 {
		t.Fatalf("G11 reindex COMPLETED with products_indexed=%d, want > 0", finalJob.ProductsIndexed)
	}

	// Tombstoned deleted product MUST NOT be resurrected; active product MUST remain indexed.
	rDel, _ := w3b47Search(t, env, "", url.Values{"q": {delTag}, "shop_id": {seller1.ShopID}})
	if _, found := w3b47FindHit(rDel.Items, delProd.ID); found {
		t.Fatalf("G11 reindex resurrected deleted product %s", delProd.ID)
	}
	rKeep, _ := w3b47Search(t, env, "", url.Values{"q": {keepTag}, "shop_id": {seller1.ShopID}})
	if _, found := w3b47FindHit(rKeep.Items, keepProd.ID); !found {
		t.Fatalf("G11 active product %s missing after reindex", keepProd.ID)
	}

	// 3. Direct gRPC RBAC checks on SearchService & ProductService.ListProductsForIndex.
	searchGRPC := w3b47DialSearchGRPC(t, env)
	if _, gErr := searchGRPC.HybridSearch(env.ctx(t), &searchv1.HybridSearchRequest{Query: keepTag, Limit: 5}); status.Code(gErr) != codes.Unauthenticated {
		t.Fatalf("G11 anonymous HybridSearch want Unauthenticated, got %v", gErr)
	}
	if _, gErr := searchGRPC.HybridSearch(w3b45AuthCtx(env.ctx(t), promoMintServiceToken("order-service")), &searchv1.HybridSearchRequest{Query: keepTag, Limit: 5}); gErr != nil {
		t.Fatalf("G11 service-token HybridSearch failed: %v", gErr)
	}
	if _, gErr := searchGRPC.ReindexAll(env.ctx(t), &searchv1.ReindexAllRequest{}); status.Code(gErr) != codes.Unauthenticated {
		t.Fatalf("G11 anonymous ReindexAll want Unauthenticated, got %v", gErr)
	}
	if _, gErr := searchGRPC.GetReindexStatus(env.ctx(t), &searchv1.GetReindexStatusRequest{JobId: jobID}); status.Code(gErr) != codes.Unauthenticated {
		t.Fatalf("G11 anonymous GetReindexStatus want Unauthenticated, got %v", gErr)
	}
	for name, tok := range map[string]string{
		"buyer":     buyer.AccessToken,
		"moderator": modStaff.AccessToken,
	} {
		if _, gErr := searchGRPC.ReindexAll(w3b45AuthCtx(env.ctx(t), tok), &searchv1.ReindexAllRequest{}); status.Code(gErr) != codes.PermissionDenied {
			t.Fatalf("G11 ReindexAll with %s want PermissionDenied, got %v", name, gErr)
		}
		if _, gErr := searchGRPC.GetReindexStatus(w3b45AuthCtx(env.ctx(t), tok), &searchv1.GetReindexStatusRequest{JobId: jobID}); status.Code(gErr) != codes.PermissionDenied {
			t.Fatalf("G11 GetReindexStatus with %s want PermissionDenied, got %v", name, gErr)
		}
	}
	// Per docs/contracts.md §4.10 & pkg/common/auth/auth.go (internalRPCs), ReindexAll/GetReindexStatus
	// permit internal service tokens; verify positive GetReindexStatus access without triggering an extra reindex.
	svcStatusResp, svcStatusErr := searchGRPC.GetReindexStatus(w3b45AuthCtx(env.ctx(t), promoMintServiceToken("search-service")), &searchv1.GetReindexStatusRequest{JobId: jobID})
	if svcStatusErr != nil || svcStatusResp.GetJobId() != jobID {
		t.Fatalf("G11 service-token GetReindexStatus want OK for job %s, got resp=%+v err=%v", jobID, svcStatusResp, svcStatusErr)
	}

	prodGRPC := w3b46DialProductGRPC(t, env)
	if _, gErr := prodGRPC.ListProductsForIndex(env.ctx(t), &productv1.ListProductsForIndexRequest{PageSize: 10}); status.Code(gErr) != codes.Unauthenticated {
		t.Fatalf("G11 anonymous ListProductsForIndex want Unauthenticated, got %v", gErr)
	}
	// Per docs/contracts.md §4.10 & §4.11, ListProductsForIndex allows service tokens or staff with
	// search.reindex / products.read; buyer, seller, and accountant lack both -> PermissionDenied,
	// whereas moderator (holds products.read) and search-service token succeed.
	for name, tok := range map[string]string{
		"buyer":      buyer.AccessToken,
		"seller":     seller1.AccessToken,
		"accountant": accStaff.AccessToken,
	} {
		if _, gErr := prodGRPC.ListProductsForIndex(w3b45AuthCtx(env.ctx(t), tok), &productv1.ListProductsForIndexRequest{PageSize: 10}); status.Code(gErr) != codes.PermissionDenied {
			t.Fatalf("G11 ListProductsForIndex with %s want PermissionDenied, got %v", name, gErr)
		}
	}
	modIdxResp, modIdxErr := prodGRPC.ListProductsForIndex(w3b45AuthCtx(env.ctx(t), modStaff.AccessToken), &productv1.ListProductsForIndexRequest{PageSize: 5})
	if modIdxErr != nil || len(modIdxResp.GetItems()) == 0 {
		t.Fatalf("G11 moderator (products.read) ListProductsForIndex want OK, got err=%v count=%d", modIdxErr, len(modIdxResp.GetItems()))
	}
	idxResp, idxErr := prodGRPC.ListProductsForIndex(w3b45AuthCtx(env.ctx(t), promoMintServiceToken("search-service")), &productv1.ListProductsForIndexRequest{PageSize: 5})
	if idxErr != nil || len(idxResp.GetItems()) == 0 {
		t.Fatalf("G11 search-service ListProductsForIndex failed: err=%v count=%d", idxErr, len(idxResp.GetItems()))
	}
}

// =================================================================================================
// 6. Manual Fault Test Helper (NO Docker calls): Search Service Stopped -> product_fallback (G13)
//
// Run by manager AFTER stopping `search` container:
//   docker compose stop search
//   E2E_SEARCH_DOWN_TEST=1 go test -v -run TestE2E_W3B3_B1_ManualFault_SearchStoppedFallback ./...
//   docker compose start search
// =================================================================================================

func TestE2E_W3B3_B1_ManualFault_SearchStoppedFallback(t *testing.T) {
	if os.Getenv("E2E_SEARCH_DOWN_TEST") != "1" {
		t.Skip("set E2E_SEARCH_DOWN_TEST=1 after stopping the search container to run G13 product_fallback E2E")
	}

	env := getEnv()
	superTok := env.adminLogin(t)
	cat, _, _ := env.ensureBaseMarketplace(t)

	ops1 := w3b45ProvisionStaff(t, t, env, superTok, "w3b47-fb-ops1", "ops")
	ops2 := w3b45ProvisionStaff(t, t, env, superTok, "w3b47-fb-ops2", "ops")

	activeSeller := env.createApprovedSeller(t, "w3b47-fb-active")
	susSeller := env.createApprovedSeller(t, "w3b47-fb-sus")
	shopSuspended := false
	w3b47EnsureShopUnsuspendedCleanup(t, env, susSeller.ShopID, &shopSuspended)

	tag := w3b41AlphaTag(env)

	pMatch := w3b47CreateProductWithSKUs(t, env, activeSeller, cat.ID, "Áo Fallback Khớp "+tag, "Khớp size/color/stock", []SKUInputDTO{
		{
			SKUCode:     env.uniqueCode("FBM"),
			Name:        "Đen XL",
			Attributes:  map[string]string{"size": "XL (60kg)", "color": "Đen than", "color_code": "den"},
			PriceVND:    200000,
			Stock:       10,
			WeightGrams: 300,
		},
	}, true)
	pWrongSize := w3b47CreateProductWithSKUs(t, env, activeSeller, cat.ID, "Áo Fallback Sai Size "+tag, "Sai size", []SKUInputDTO{
		{
			SKUCode:     env.uniqueCode("FBW"),
			Name:        "Đen S",
			Attributes:  map[string]string{"size": "S", "color": "Đen than", "color_code": "den"},
			PriceVND:    200000,
			Stock:       10,
			WeightGrams: 300,
		},
	}, true)
	pOutOfStock := w3b47CreateProductWithSKUs(t, env, activeSeller, cat.ID, "Áo Fallback Hết Hàng "+tag, "Hết tồn kho", []SKUInputDTO{
		{
			SKUCode:     env.uniqueCode("FBO"),
			Name:        "Đen XL Hết",
			Attributes:  map[string]string{"size": "XL (60kg)", "color": "Đen than", "color_code": "den"},
			PriceVND:    200000,
			Stock:       1,
			WeightGrams: 300,
		},
	}, true)
	// Zero out stock on pOutOfStock.
	env.mustDoJSON(t, http.MethodPatch,
		fmt.Sprintf("/api/v1/seller/skus/%s/stock", url.PathEscape(pOutOfStock.SKUs[0].ID)),
		activeSeller.AccessToken, nil, map[string]any{
			"new_stock":        0,
			"expected_version": pOutOfStock.SKUs[0].Version,
		}, http.StatusOK)

	pSuspended := w3b47CreateProductWithSKUs(t, env, susSeller, cat.ID, "Áo Fallback Gian Khoá "+tag, "Gian bị khoá", []SKUInputDTO{
		{
			SKUCode:     env.uniqueCode("FBS"),
			Name:        "Đen XL",
			Attributes:  map[string]string{"size": "XL (60kg)", "color": "Đen than", "color_code": "den"},
			PriceVND:    200000,
			Stock:       10,
			WeightGrams: 300,
		},
	}, true)
	w3b47SuspendShopTwoPerson(t, env, ops1.AccessToken, ops2.AccessToken, susSeller.ShopID, "w3b47 fallback suspend test")
	shopSuspended = true

	// Wait until product service marks shop_suspended=true so public product catalog hides pSuspended.
	env.pollUntil(t, env.cfg.PollTimeout, 200*time.Millisecond, "product service hides suspended shop in fallback", func() (bool, string) {
		resp := env.mustDoJSON(t, http.MethodGet, "/api/v1/products?shop_id="+url.QueryEscape(susSeller.ShopID), "", nil, nil, http.StatusOK)
		var out struct {
			Total int64 `json:"total"`
		}
		_ = DecodeJSON(resp, &out)
		return out.Total == 0, fmt.Sprintf("total=%d", out.Total)
	})

	fbRes, fbRaw := w3b47Search(t, env, "", url.Values{
		"q":             {tag},
		"size":          {"XL (60kg)"},
		"color":         {"den"},
		"in_stock_only": {"true"},
	})
	if fbRes.DegradedMode != "product_fallback" || fbRes.UsedVector {
		t.Fatalf("G13 expected degraded_mode='product_fallback' used_vector=false, got mode=%q vector=%v (body=%s)",
			fbRes.DegradedMode, fbRes.UsedVector, string(fbRaw))
	}
	if _, ok := w3b47FindHit(fbRes.Items, pMatch.ID); !ok {
		t.Fatalf("G13 fallback missed matching product %s: body=%s", pMatch.ID, string(fbRaw))
	}
	for label, badID := range map[string]string{
		"wrong_size":     pWrongSize.ID,
		"out_of_stock":   pOutOfStock.ID,
		"suspended_shop": pSuspended.ID,
	} {
		if _, leaked := w3b47FindHit(fbRes.Items, badID); leaked {
			t.Fatalf("G13 fallback leaked %s product %s: body=%s", label, badID, string(fbRaw))
		}
	}
	if strings.Contains(string(fbRaw), "commission_rate_bps") {
		t.Fatalf("G13 fallback leaked commission_rate_bps: %s", string(fbRaw))
	}

	// Suggest fallback must also return 200 and exclude suspended shop products.
	sugRes, sugRaw, _ := w3b47Suggest(t, env, "", tag, 10)
	for _, p := range sugRes.Products {
		if p.ShopID == susSeller.ShopID {
			t.Fatalf("G13 suggest fallback leaked suspended shop product: %s", string(sugRaw))
		}
	}
}
