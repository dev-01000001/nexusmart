package router

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	searchv1 "github.com/nexusmart/core/gen/go/nexusmart/search/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/gateway/internal/clients"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type fakeSearchClient struct {
	searchv1.SearchServiceClient
	lastSearchReq  *searchv1.SearchRequest
	lastSuggestReq *searchv1.SuggestRequest
	searchResp     *searchv1.SearchResponse
	searchErr      error
	suggestResp    *searchv1.SuggestResponse
	suggestErr     error
}

func (f *fakeSearchClient) Search(_ context.Context, in *searchv1.SearchRequest, _ ...grpc.CallOption) (*searchv1.SearchResponse, error) {
	f.lastSearchReq = in
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	return f.searchResp, nil
}

func (f *fakeSearchClient) Suggest(_ context.Context, in *searchv1.SuggestRequest, _ ...grpc.CallOption) (*searchv1.SuggestResponse, error) {
	f.lastSuggestReq = in
	if f.suggestErr != nil {
		return nil, f.suggestErr
	}
	return f.suggestResp, nil
}

type fakeFallbackProductClient struct {
	productv1.ProductServiceClient
	lastListReq *productv1.ListProductsRequest
	listResp    *productv1.ListProductsResponse
	listErr     error
}

func (f *fakeFallbackProductClient) ListProducts(_ context.Context, in *productv1.ListProductsRequest, _ ...grpc.CallOption) (*productv1.ListProductsResponse, error) {
	f.lastListReq = in
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.listResp, nil
}

func newSearchTestRouter(t *testing.T, searchCli searchv1.SearchServiceClient, prodCli productv1.ProductServiceClient) http.Handler {
	t.Helper()
	authMgr, err := auth.NewManager("search-router-test-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	log := logger.New("gateway-search-test", logger.WithWriter(io.Discard))
	return New(Dependencies{
		ServiceName: "gateway-search-test",
		Clients: &clients.Clients{
			Search:  searchCli,
			Product: prodCli,
			Timeout: 5 * time.Second,
		},
		AuthManager: authMgr,
		Logger:      log,
	})
}

func TestGatewaySearchForwardsParamsAndReturnsNonNilSlices(t *testing.T) {
	shutdown, err := telemetry.Init(context.Background(), telemetry.Config{ServiceName: "gateway-search-test"})
	if err != nil {
		t.Fatalf("telemetry.Init: %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	now := time.Date(2026, 11, 21, 7, 30, 0, 0, time.UTC)
	searchMock := &fakeSearchClient{
		searchResp: &searchv1.SearchResponse{
			Items: []*searchv1.SearchItem{
				{
					Id:           "prod-1",
					ShopId:       "shop-bach-dang",
					CategoryId:   "cat-1",
					CategorySlug: "ao-thun",
					CategoryName: "Áo thun",
					Name:         "Áo thun thể thao đẹp",
					Description:  "Cotton Compact 220gsm",
					Images:       []string{"/products/ao-thun-cotton-trang.png"},
					Status:       "APPROVED",
					MinPriceVnd:  199000,
					MaxPriceVnd:  229000,
					TotalStock:   45,
					InStock:      true,
					Score:        0.92,
					CreatedAt:    timestamppb.New(now),
					UpdatedAt:    timestamppb.New(now),
				},
			},
			Total:    123,
			Page:     2,
			PageSize: 15,
			Facets: &searchv1.SearchFacets{
				Categories: []*searchv1.FacetBucket{
					{Value: "ao-thun", Label: "Áo thun", Count: 42},
				},
				Shops: []*searchv1.FacetBucket{
					{Value: "shop-bach-dang", Label: "shop-bach-dang", Count: 42},
				},
				PriceRanges: nil, // verify nil facet slice still serializes as []
			},
		},
	}

	h := newSearchTestRouter(t, searchMock, &fakeFallbackProductClient{})
	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/search?q=%C3%A1o+thun&shop_id=shop-bach-dang&category=ao-thun&min_price=100000&max_price=500000&sort=price_asc&page=2&page_size=15&mode=hybrid&in_stock_only=true",
		nil,
	)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if searchMock.lastSearchReq == nil {
		t.Fatal("expected SearchService.Search to be called")
	}
	got := searchMock.lastSearchReq
	if got.GetQuery() != "áo thun" ||
		got.GetShopId() != "shop-bach-dang" ||
		got.GetCategory() != "ao-thun" ||
		got.GetMinPriceVnd() != 100000 ||
		got.GetMaxPriceVnd() != 500000 ||
		got.GetSort() != "price_asc" ||
		got.GetPage() != 2 ||
		got.GetPageSize() != 15 ||
		got.GetMode() != "hybrid" ||
		!got.GetInStockOnly() {
		t.Fatalf("unexpected forwarded SearchRequest: %+v", got)
	}

	var body struct {
		Items    []map[string]any `json:"items"`
		Total    int64            `json:"total"`
		Page     int32            `json:"page"`
		PageSize int32            `json:"page_size"`
		Facets   struct {
			Categories  []facetBucketDTO `json:"categories"`
			Shops       []facetBucketDTO `json:"shops"`
			PriceRanges []facetBucketDTO `json:"price_ranges"`
		} `json:"facets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if body.Total != 123 || body.Page != 2 || body.PageSize != 15 {
		t.Fatalf("unexpected pagination envelope: total=%d page=%d page_size=%d", body.Total, body.Page, body.PageSize)
	}
	if len(body.Items) != 1 || body.Items[0]["id"] != "prod-1" || body.Items[0]["min_price_vnd"] != float64(199000) {
		t.Fatalf("unexpected items: %+v", body.Items)
	}
	if len(body.Facets.Categories) != 1 || body.Facets.Categories[0].Value != "ao-thun" {
		t.Fatalf("unexpected category facets: %+v", body.Facets.Categories)
	}
	if len(body.Facets.Shops) != 1 || body.Facets.Shops[0].Count != 42 {
		t.Fatalf("unexpected shop facets: %+v", body.Facets.Shops)
	}
	if body.Facets.PriceRanges == nil {
		t.Fatal("expected price_ranges to be non-nil JSON array []")
	}

	// Verify fallback query param aliases (category_id, min_price_vnd, max_price_vnd) and empty non-nil items/facets.
	searchMock.searchResp = &searchv1.SearchResponse{Total: 0}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(
		http.MethodGet,
		"/api/v1/search?q=test&category_id=cat-9&min_price_vnd=50000&max_price_vnd=300000",
		nil,
	)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on alias params, got %d", rec.Code)
	}
	if searchMock.lastSearchReq.GetCategory() != "cat-9" ||
		searchMock.lastSearchReq.GetMinPriceVnd() != 50000 ||
		searchMock.lastSearchReq.GetMaxPriceVnd() != 300000 {
		t.Fatalf("alias params not forwarded: %+v", searchMock.lastSearchReq)
	}
	var emptyBody map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &emptyBody); err != nil {
		t.Fatalf("unmarshal empty response: %v", err)
	}
	if items, ok := emptyBody["items"].([]any); !ok || items == nil {
		t.Fatalf("expected non-nil empty items slice, got %#v", emptyBody["items"])
	}
}

func TestGatewaySearchSuggestAndFallback(t *testing.T) {
	searchMock := &fakeSearchClient{
		suggestResp: &searchv1.SuggestResponse{
			Suggestions: []*searchv1.Suggestion{
				{
					Text:         "Áo thun Cotton Compact",
					Kind:         "product",
					ProductId:    "prod-1",
					CategorySlug: "ao-thun",
					MinPriceVnd:  199000,
				},
				{
					Text:         "Áo thun",
					Kind:         "category",
					CategorySlug: "ao-thun",
				},
			},
		},
	}

	h := newSearchTestRouter(t, searchMock, &fakeFallbackProductClient{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/search/suggest?q=ao+thun&shop_id=shop-1&limit=8", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if searchMock.lastSuggestReq.GetQuery() != "ao thun" ||
		searchMock.lastSuggestReq.GetShopId() != "shop-1" ||
		searchMock.lastSuggestReq.GetLimit() != 8 {
		t.Fatalf("unexpected SuggestRequest: %+v", searchMock.lastSuggestReq)
	}

	var body struct {
		Suggestions []suggestionDTO `json:"suggestions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal suggest response: %v", err)
	}
	if len(body.Suggestions) != 2 || body.Suggestions[0].ProductID != "prod-1" || body.Suggestions[0].MinPriceVND != 199000 {
		t.Fatalf("unexpected suggestions: %+v", body.Suggestions)
	}

	// When SearchService is Unavailable, suggest gracefully returns 200 with empty suggestions [].
	searchMock.suggestErr = status.Error(codes.Unavailable, "search down")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/search/suggest?q=ao", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 fallback on suggest Unavailable, got %d", rec.Code)
	}
	var fbBody struct {
		Suggestions []suggestionDTO `json:"suggestions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &fbBody); err != nil {
		t.Fatalf("unmarshal suggest fallback: %v", err)
	}
	if fbBody.Suggestions == nil || len(fbBody.Suggestions) != 0 {
		t.Fatalf("expected empty non-nil suggestions slice, got %#v", fbBody.Suggestions)
	}
}

func TestGatewaySearchFallbackToProductServiceWhenUnavailable(t *testing.T) {
	now := time.Date(2026, 11, 21, 8, 0, 0, 0, time.UTC)
	searchMock := &fakeSearchClient{
		searchErr: status.Error(codes.Unavailable, "opensearch cluster unreachable"),
	}
	prodMock := &fakeFallbackProductClient{
		listResp: &productv1.ListProductsResponse{
			Products: []*productv1.Product{
				{
					Id:          "prod-fb-1",
					ShopId:      "shop-sai-gon",
					CategoryId:  "cat-short",
					Name:        "Quần đùi chạy bộ 5 inch",
					Description: "Siêu nhẹ thoáng khí",
					Images:      []string{"/products/quan-short-run-xam-dam.png"},
					Status:      productv1.ProductStatus_PRODUCT_STATUS_APPROVED,
					MinPriceVnd: 249000,
					MaxPriceVnd: 269000,
					Skus: []*productv1.SKU{
						{Id: "sku-1", Stock: 30, AvailableStock: 25},
						{Id: "sku-2", Stock: 15, AvailableStock: 15},
					},
					CreatedAt: timestamppb.New(now),
					UpdatedAt: timestamppb.New(now),
				},
			},
			Total: 1,
		},
	}

	h := newSearchTestRouter(t, searchMock, prodMock)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/search?q=chay+bo&shop_id=shop-sai-gon&category=cat-short&min_price=100000&max_price=400000&sort=price_desc&page=1&page_size=10",
		nil,
	)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 fallback response, got %d (%s)", rec.Code, rec.Body.String())
	}
	if prodMock.lastListReq == nil {
		t.Fatal("expected ProductService.ListProducts fallback to be called")
	}
	if prodMock.lastListReq.GetKeyword() != "chay bo" ||
		prodMock.lastListReq.GetShopId() != "shop-sai-gon" ||
		prodMock.lastListReq.GetCategoryId() != "cat-short" ||
		prodMock.lastListReq.GetMinPriceVnd() != 100000 ||
		prodMock.lastListReq.GetMaxPriceVnd() != 400000 ||
		prodMock.lastListReq.GetSortBy() != "price_desc" {
		t.Fatalf("unexpected fallback ListProductsRequest: %+v", prodMock.lastListReq)
	}

	var body searchResponseDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal fallback response: %v", err)
	}
	if body.Total != 1 || body.Page != 1 || body.PageSize != 10 {
		t.Fatalf("unexpected fallback pagination: %+v", body)
	}
	if len(body.Items) != 1 || body.Items[0].ID != "prod-fb-1" || body.Items[0].TotalStock != 40 || !body.Items[0].InStock {
		t.Fatalf("unexpected fallback items: %+v", body.Items)
	}
	if body.Facets == nil || body.Facets.Categories == nil || body.Facets.Shops == nil || body.Facets.PriceRanges == nil {
		t.Fatalf("expected non-nil empty facets in fallback, got %+v", body.Facets)
	}
}

func TestGatewaySearchErrorPropagation(t *testing.T) {
	shutdown, err := telemetry.Init(context.Background(), telemetry.Config{ServiceName: "gateway-search-err-test"})
	if err != nil {
		t.Fatalf("telemetry.Init: %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	searchMock := &fakeSearchClient{
		searchErr: status.Error(codes.InvalidArgument, "min_price_vnd cannot exceed max_price_vnd"),
	}
	prodMock := &fakeFallbackProductClient{}

	h := newSearchTestRouter(t, searchMock, prodMock)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/search?min_price=500000&max_price=100000", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on InvalidArgument, got %d (%s)", rec.Code, rec.Body.String())
	}
	if prodMock.lastListReq != nil {
		t.Fatal("InvalidArgument must NOT trigger product fallback")
	}

	var errResp struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			TraceID string `json:"trace_id"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("unmarshal error envelope: %v", err)
	}
	if errResp.Error.Code != "INVALID_ARGUMENT" || errResp.Error.Message != "min_price_vnd cannot exceed max_price_vnd" {
		t.Fatalf("unexpected error envelope: %+v", errResp)
	}
	if errResp.Error.TraceID == "" {
		t.Fatal("expected non-empty trace_id in error envelope")
	}
}
