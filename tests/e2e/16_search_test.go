package e2e

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// resolveServiceHTTPURL returns the HTTP ops base URL for an extended service
// (search, chat, assistant), honoring envKey override and Docker Compose service networking.
func resolveServiceHTTPURL(env *testEnv, envKey, dockerHost, port string) string {
	if v := strings.TrimSpace(os.Getenv(envKey)); v != "" {
		return strings.TrimRight(v, "/")
	}
	if strings.Contains(env.cfg.GatewayURL, "://gateway:") {
		return fmt.Sprintf("http://%s:%s", dockerHost, port)
	}
	return fmt.Sprintf("http://localhost:%s", port)
}

// verifyServiceOpsEndpoints asserts HTTP 200 on /healthz, /readyz, and /metrics
// and verifies Prometheus metrics are exposed.
func verifyServiceOpsEndpoints(t *testing.T, env *testEnv, svcName, baseURL string) {
	t.Helper()
	ctx := env.ctx(t)

	for _, ep := range []string{"/healthz", "/readyz"} {
		fullURL := baseURL + ep
		resp, err := env.client.DoAbsolute(ctx, http.MethodGet, fullURL, "", nil, nil)
		if err != nil {
			t.Fatalf("service=%s GET %s failed: %v", svcName, fullURL, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("service=%s GET %s returned HTTP %d (want 200): %s", svcName, fullURL, resp.StatusCode, string(resp.Body))
		}
	}

	metricsURL := baseURL + "/metrics"
	resp, err := env.client.DoAbsolute(ctx, http.MethodGet, metricsURL, "", nil, nil)
	if err != nil {
		t.Fatalf("service=%s GET %s failed: %v", svcName, metricsURL, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("service=%s GET %s returned HTTP %d (want 200): %s", svcName, metricsURL, resp.StatusCode, string(resp.Body))
	}
	body := string(resp.Body)
	if !strings.Contains(body, "http_requests_total") && !strings.Contains(body, "grpc_requests_total") {
		t.Fatalf("service=%s GET %s missing RED metrics (http_requests_total / grpc_requests_total); snippet=%q",
			svcName, metricsURL, truncateBody(body, 300))
	}
}

// registerRateLimitResetCleanup resets gateway rate-limit keys in Redis when a test finishes
// so back-to-back test files and repeated `make e2e` runs start with fresh quotas.
func registerRateLimitResetCleanup(t *testing.T, env *testEnv) {
	t.Helper()
	if env.cfg.RedisAddr == "" {
		return
	}
	rCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_, _ = ResetRateLimitKeys(rCtx, env.cfg.RedisAddr)
	cancel()

	t.Cleanup(func() {
		cCtx, cCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cCancel()
		_, _ = ResetRateLimitKeys(cCtx, env.cfg.RedisAddr)
	})
}

func truncateBody(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// --- Search DTOs ---

type e2eSearchItemDTO struct {
	ID           string   `json:"id"`
	ShopID       string   `json:"shop_id"`
	CategoryID   string   `json:"category_id"`
	CategorySlug string   `json:"category_slug"`
	CategoryName string   `json:"category_name"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Images       []string `json:"images"`
	Status       string   `json:"status"`
	MinPriceVND  int64    `json:"min_price_vnd"`
	MaxPriceVND  int64    `json:"max_price_vnd"`
	TotalStock   int32    `json:"total_stock"`
	InStock      bool     `json:"in_stock"`
	Score        float64  `json:"score"`
	CreatedAt    string   `json:"created_at,omitempty"`
	UpdatedAt    string   `json:"updated_at,omitempty"`
}

type e2eFacetBucketDTO struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Count int64  `json:"count"`
}

type e2eSearchFacetsDTO struct {
	Categories  []e2eFacetBucketDTO `json:"categories"`
	Shops       []e2eFacetBucketDTO `json:"shops"`
	PriceRanges []e2eFacetBucketDTO `json:"price_ranges"`
}

type e2eSearchResponseDTO struct {
	Items    []e2eSearchItemDTO  `json:"items"`
	Total    int64               `json:"total"`
	Page     int32               `json:"page"`
	PageSize int32               `json:"page_size"`
	Facets   *e2eSearchFacetsDTO `json:"facets"`
}

type e2eSearchSuggestionDTO struct {
	Text         string `json:"text"`
	Kind         string `json:"kind"`
	ProductID    string `json:"product_id"`
	CategorySlug string `json:"category_slug"`
	MinPriceVND  int64  `json:"min_price_vnd"`
}

type e2eSearchSuggestResponseDTO struct {
	Suggestions []e2eSearchSuggestionDTO `json:"suggestions"`
}

func executeSearchQuery(t *testing.T, env *testEnv, params url.Values) e2eSearchResponseDTO {
	t.Helper()
	path := "/api/v1/search"
	if encoded := params.Encode(); encoded != "" {
		path += "?" + encoded
	}
	resp := env.mustDoJSON(t, http.MethodGet, path, "", nil, nil, http.StatusOK)
	var out e2eSearchResponseDTO
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode GET %s: %v", path, err)
	}
	return out
}

func containsSearchProduct(items []e2eSearchItemDTO, productID string) bool {
	for _, it := range items {
		if it.ID == productID {
			return true
		}
	}
	return false
}

func waitForProductIndexed(t *testing.T, env *testEnv, query, shopID, productID string, timeout time.Duration) time.Duration {
	t.Helper()
	start := time.Now()
	env.pollUntil(t, timeout, 120*time.Millisecond, fmt.Sprintf("product %s indexed in search", productID), func() (bool, string) {
		q := url.Values{}
		q.Set("q", query)
		if shopID != "" {
			q.Set("shop_id", shopID)
		}
		q.Set("page_size", "50")
		resp, err := env.client.DoJSON(env.ctx(t), http.MethodGet, "/api/v1/search?"+q.Encode(), "", nil, nil)
		if err != nil {
			return false, err.Error()
		}
		if resp.StatusCode != http.StatusOK {
			return false, fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(resp.Body))
		}
		var out e2eSearchResponseDTO
		if err := DecodeJSON(resp, &out); err != nil {
			return false, err.Error()
		}
		if containsSearchProduct(out.Items, productID) {
			return true, ""
		}
		return false, fmt.Sprintf("returned %d items, product %s not yet visible", len(out.Items), productID)
	})
	return time.Since(start)
}

func TestE2E_Search_VietnameseIndexingFiltersFacetsAndSuggest(t *testing.T) {
	env := getEnv()
	registerRateLimitResetCleanup(t, env)

	searchHTTPURL := resolveServiceHTTPURL(env, "SEARCH_HTTP_URL", "search", "8089")

	// 1. Verify search service HTTP ops endpoints (/healthz, /readyz, /metrics).
	t.Run("ServiceOps_HealthReadyMetrics", func(t *testing.T) {
		verifyServiceOpsEndpoints(t, env, "search", searchHTTPURL)
	})

	cat, seller1, seller2 := env.ensureBaseMarketplace(t)

	// 2. Create & approve distinctive Vietnamese products with diacritics and measure indexing latency < 5s.
	tag := fmt.Sprintf("s%s%d", env.runID, env.nextSeq())
	accentedName1 := fmt.Sprintf("Áo Khoác Gió Chống Nắng Độc Quyền %s", tag)
	unaccentedQuery1 := fmt.Sprintf("ao khoac gio chong nang doc quyen %s", tag)

	prod1, _ := env.createApprovedProduct(t, seller1, cat.ID, accentedName1, 250000, 40, 450)
	indexLatency := waitForProductIndexed(t, env, accentedName1, seller1.ShopID, prod1.ID, 10*time.Second)
	t.Logf("Search indexing latency for product %s (%s): %s", prod1.ID, accentedName1, indexLatency)
	if indexLatency >= 5*time.Second {
		t.Fatalf("expected search indexing latency < 5s, got %s", indexLatency)
	}

	// Ensure prod2 has a strictly newer created_at_unix timestamp (seconds precision) for sort=newest assertions.
	time.Sleep(1100 * time.Millisecond)
	accentedName2 := fmt.Sprintf("Áo Khoác Gió Chống Nắng Cao Cấp %s", tag)
	prod2, _ := env.createApprovedProduct(t, seller1, cat.ID, accentedName2, 650000, 35, 520)

	accentedName3 := fmt.Sprintf("Áo Khoác Gió Chống Nắng Đối Thủ %s", tag)
	prod3, _ := env.createApprovedProduct(t, seller2, cat.ID, accentedName3, 420000, 25, 480)

	waitForProductIndexed(t, env, accentedName2, seller1.ShopID, prod2.ID, 10*time.Second)
	waitForProductIndexed(t, env, accentedName3, seller2.ShopID, prod3.ID, 10*time.Second)

	// 3. Accented and unaccented Vietnamese queries return the exact same product.
	t.Run("VietnameseDiacritics_AccentedAndUnaccentedMatch", func(t *testing.T) {
		qAcc := url.Values{}
		qAcc.Set("q", accentedName1)
		qAcc.Set("shop_id", seller1.ShopID)
		resAcc := executeSearchQuery(t, env, qAcc)
		if !containsSearchProduct(resAcc.Items, prod1.ID) {
			t.Fatalf("accented query %q missing expected product %s (got %+v)", accentedName1, prod1.ID, resAcc.Items)
		}

		qFold := url.Values{}
		qFold.Set("q", unaccentedQuery1)
		qFold.Set("shop_id", seller1.ShopID)
		resFold := executeSearchQuery(t, env, qFold)
		if !containsSearchProduct(resFold.Items, prod1.ID) {
			t.Fatalf("unaccented query %q missing expected product %s (got %+v)", unaccentedQuery1, prod1.ID, resFold.Items)
		}
		if resAcc.Items[0].ID != prod1.ID || resFold.Items[0].ID != prod1.ID {
			t.Fatalf("expected top match %s for both accented (%s) and unaccented (%s) queries",
				prod1.ID, resAcc.Items[0].ID, resFold.Items[0].ID)
		}
	})

	// 4. shop_id scoping strictly excludes other shop's products.
	t.Run("ShopIDScoping_ExcludesOtherShop", func(t *testing.T) {
		sharedQuery := fmt.Sprintf("ao khoac gio chong nang %s", tag)

		qShop1 := url.Values{}
		qShop1.Set("q", sharedQuery)
		qShop1.Set("shop_id", seller1.ShopID)
		qShop1.Set("page_size", "50")
		res1 := executeSearchQuery(t, env, qShop1)

		if !containsSearchProduct(res1.Items, prod1.ID) || !containsSearchProduct(res1.Items, prod2.ID) {
			t.Fatalf("shop1 search missing seller1 products (%s, %s); items=%+v", prod1.ID, prod2.ID, res1.Items)
		}
		for _, it := range res1.Items {
			if it.ShopID != seller1.ShopID {
				t.Fatalf("shop1 scoped search leaked item %s from shop %s", it.ID, it.ShopID)
			}
			if it.ID == prod3.ID {
				t.Fatalf("shop1 scoped search unexpectedly returned seller2 product %s", prod3.ID)
			}
		}

		qShop2 := url.Values{}
		qShop2.Set("q", sharedQuery)
		qShop2.Set("shop_id", seller2.ShopID)
		res2 := executeSearchQuery(t, env, qShop2)
		if !containsSearchProduct(res2.Items, prod3.ID) {
			t.Fatalf("shop2 search missing seller2 product %s; items=%+v", prod3.ID, res2.Items)
		}
		if containsSearchProduct(res2.Items, prod1.ID) || containsSearchProduct(res2.Items, prod2.ID) {
			t.Fatalf("shop2 search leaked seller1 products: %+v", res2.Items)
		}
	})

	// 5. min_price / max_price filtering + facets presence.
	t.Run("PriceFilterAndFacets", func(t *testing.T) {
		sharedQuery := fmt.Sprintf("ao khoac gio chong nang %s", tag)

		qAll := url.Values{}
		qAll.Set("q", sharedQuery)
		qAll.Set("page_size", "50")
		resAll := executeSearchQuery(t, env, qAll)
		if resAll.Facets == nil {
			t.Fatalf("expected non-nil facets in search response")
		}
		if len(resAll.Facets.Shops) < 2 {
			t.Fatalf("expected >= 2 shop facet buckets for shared query, got %+v", resAll.Facets.Shops)
		}
		if len(resAll.Facets.PriceRanges) == 0 {
			t.Fatalf("expected non-empty price_ranges facets, got %+v", resAll.Facets)
		}
		if len(resAll.Facets.Categories) == 0 {
			t.Fatalf("expected non-empty categories facets, got %+v", resAll.Facets)
		}

		qRange := url.Values{}
		qRange.Set("q", sharedQuery)
		qRange.Set("min_price", "300000")
		qRange.Set("max_price", "700000")
		qRange.Set("page_size", "50")
		resRange := executeSearchQuery(t, env, qRange)

		if !containsSearchProduct(resRange.Items, prod2.ID) || !containsSearchProduct(resRange.Items, prod3.ID) {
			t.Fatalf("price-filtered search [300k..700k] missing expected products (%s, %s); got %+v",
				prod2.ID, prod3.ID, resRange.Items)
		}
		if containsSearchProduct(resRange.Items, prod1.ID) {
			t.Fatalf("price-filtered search [300k..700k] included 250k product %s", prod1.ID)
		}
		for _, it := range resRange.Items {
			if it.MinPriceVND < 300000 || it.MinPriceVND > 700000 {
				t.Fatalf("item %s min_price_vnd=%d outside [300000..700000]", it.ID, it.MinPriceVND)
			}
		}
	})

	// 6. Sorting: price_asc, price_desc, newest.
	t.Run("Sorting_PriceAscPriceDescAndNewest", func(t *testing.T) {
		sharedQuery := fmt.Sprintf("ao khoac gio chong nang %s", tag)

		indexOf := func(items []e2eSearchItemDTO, id string) int {
			for i, it := range items {
				if it.ID == id {
					return i
				}
			}
			return -1
		}

		qAsc := url.Values{}
		qAsc.Set("q", sharedQuery)
		qAsc.Set("shop_id", seller1.ShopID)
		qAsc.Set("sort", "price_asc")
		resAsc := executeSearchQuery(t, env, qAsc)
		i1Asc, i2Asc := indexOf(resAsc.Items, prod1.ID), indexOf(resAsc.Items, prod2.ID)
		if i1Asc < 0 || i2Asc < 0 || i1Asc >= i2Asc {
			t.Fatalf("sort=price_asc expected prod1 (250k, idx=%d) before prod2 (650k, idx=%d); items=%+v",
				i1Asc, i2Asc, resAsc.Items)
		}
		for i := 1; i < len(resAsc.Items); i++ {
			if resAsc.Items[i].MinPriceVND < resAsc.Items[i-1].MinPriceVND {
				t.Fatalf("sort=price_asc out of order at idx %d: %d < %d",
					i, resAsc.Items[i].MinPriceVND, resAsc.Items[i-1].MinPriceVND)
			}
		}

		qDesc := url.Values{}
		qDesc.Set("q", sharedQuery)
		qDesc.Set("shop_id", seller1.ShopID)
		qDesc.Set("sort", "price_desc")
		resDesc := executeSearchQuery(t, env, qDesc)
		i1Desc, i2Desc := indexOf(resDesc.Items, prod1.ID), indexOf(resDesc.Items, prod2.ID)
		if i1Desc < 0 || i2Desc < 0 || i2Desc >= i1Desc {
			t.Fatalf("sort=price_desc expected prod2 (650k, idx=%d) before prod1 (250k, idx=%d); items=%+v",
				i2Desc, i1Desc, resDesc.Items)
		}
		for i := 1; i < len(resDesc.Items); i++ {
			if resDesc.Items[i].MaxPriceVND > resDesc.Items[i-1].MaxPriceVND {
				t.Fatalf("sort=price_desc out of order at idx %d: %d > %d",
					i, resDesc.Items[i].MaxPriceVND, resDesc.Items[i-1].MaxPriceVND)
			}
		}

		qNewest := url.Values{}
		qNewest.Set("q", sharedQuery)
		qNewest.Set("shop_id", seller1.ShopID)
		qNewest.Set("sort", "newest")
		resNewest := executeSearchQuery(t, env, qNewest)
		i1New, i2New := indexOf(resNewest.Items, prod1.ID), indexOf(resNewest.Items, prod2.ID)
		if i1New < 0 || i2New < 0 || i2New >= i1New {
			t.Fatalf("sort=newest expected newer prod2 (idx=%d) before older prod1 (idx=%d); items=%+v",
				i2New, i1New, resNewest.Items)
		}
	})

	// 7. Autocomplete suggestions scoped by shop_id.
	t.Run("Suggest_ScopedByShopID", func(t *testing.T) {
		q := url.Values{}
		q.Set("q", fmt.Sprintf("ao khoac gio chong nang %s", tag))
		q.Set("shop_id", seller1.ShopID)
		q.Set("limit", "10")

		resp := env.mustDoJSON(t, http.MethodGet, "/api/v1/search/suggest?"+q.Encode(), "", nil, nil, http.StatusOK)
		var out e2eSearchSuggestResponseDTO
		if err := DecodeJSON(resp, &out); err != nil {
			t.Fatalf("decode suggest response: %v", err)
		}
		if len(out.Suggestions) == 0 {
			t.Fatalf("expected non-empty suggestions for shop1 query, got empty (body=%s)", string(resp.Body))
		}
		foundProd1 := false
		for _, sg := range out.Suggestions {
			if sg.ProductID == prod1.ID || sg.ProductID == prod2.ID {
				foundProd1 = true
			}
			if sg.ProductID == prod3.ID {
				t.Fatalf("shop1 suggest leaked seller2 product %s: %+v", prod3.ID, out.Suggestions)
			}
		}
		if !foundProd1 {
			t.Fatalf("expected suggestions to include seller1 product (%s or %s), got %+v", prod1.ID, prod2.ID, out.Suggestions)
		}
	})
}
