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
)

type fakeShopScopedSearchClient struct {
	searchv1.SearchServiceClient
	lastSearchReq  *searchv1.SearchProductsRequest
	lastSuggestReq *searchv1.SuggestRequest
	searchResp     *searchv1.SearchProductsResponse
	searchErr      error
	suggestResp    *searchv1.SuggestResponse
	suggestErr     error
}

func (f *fakeShopScopedSearchClient) SearchProducts(_ context.Context, in *searchv1.SearchProductsRequest, _ ...grpc.CallOption) (*searchv1.SearchProductsResponse, error) {
	f.lastSearchReq = in
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	return f.searchResp, nil
}

func (f *fakeShopScopedSearchClient) Suggest(_ context.Context, in *searchv1.SuggestRequest, _ ...grpc.CallOption) (*searchv1.SuggestResponse, error) {
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

func TestGatewaySearchSuggestForwardsShopIDAndFallbackPreservesShopScope(t *testing.T) {
	shutdown, err := telemetry.Init(context.Background(), telemetry.Config{ServiceName: "gateway-search-test"})
	if err != nil {
		t.Fatalf("telemetry.Init: %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	searchMock := &fakeShopScopedSearchClient{
		suggestResp: &searchv1.SuggestResponse{
			Keywords: []string{"áo thun cotton"},
			Products: []*searchv1.SearchProductHit{
				{
					Id:          "prod-1",
					ShopId:      "shop-bach-dang",
					Name:        "Áo thun Cotton Compact",
					Slug:        "ao-thun-cotton-compact",
					MinPriceVnd: 199000,
					MaxPriceVnd: 229000,
					InStock:     true,
				},
			},
		},
	}
	prodMock := &fakeFallbackProductClient{
		listResp: &productv1.ListProductsResponse{
			Products: []*productv1.Product{
				{
					Id:          "prod-fb-1",
					ShopId:      "shop-bach-dang",
					Name:        "Áo thun Fallback",
					Slug:        "ao-thun-fallback",
					Status:      productv1.ProductStatus_PRODUCT_STATUS_APPROVED,
					MinPriceVnd: 150000,
					MaxPriceVnd: 150000,
				},
			},
			Total: 1,
		},
	}

	h := newSearchTestRouter(t, searchMock, prodMock)

	// 1. Normal suggest forwards shop_id & limit to SearchService.Suggest.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/search/suggest?q=ao+thun&shop_id=shop-bach-dang&limit=8", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /search/suggest want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if searchMock.lastSuggestReq == nil ||
		searchMock.lastSuggestReq.GetQuery() != "ao thun" ||
		searchMock.lastSuggestReq.GetShopId() != "shop-bach-dang" ||
		searchMock.lastSuggestReq.GetLimit() != 8 {
		t.Fatalf("unexpected SuggestRequest: %+v", searchMock.lastSuggestReq)
	}
	var body struct {
		Keywords []string               `json:"keywords"`
		Products []*searchProductHitDTO `json:"products"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal suggest response: %v", err)
	}
	if len(body.Keywords) != 1 || len(body.Products) != 1 || body.Products[0].ShopID != "shop-bach-dang" {
		t.Fatalf("unexpected suggest payload: %+v", body)
	}

	// 2. When SearchService is Unavailable, suggest fallback to Product.ListProducts MUST preserve shop_id.
	searchMock.suggestErr = status.Error(codes.Unavailable, "search down")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/search/suggest?q=ao+thun&shop_id=shop-bach-dang", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("suggest fallback want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if prodMock.lastListReq == nil || prodMock.lastListReq.GetShopId() != "shop-bach-dang" || prodMock.lastListReq.GetKeyword() != "ao thun" {
		t.Fatalf("suggest fallback dropped shop_id isolation: req=%+v", prodMock.lastListReq)
	}

	// 3. SearchProducts fallback on Unavailable/DeadlineExceeded preserves shop_id and filters non-public items.
	searchMock.searchErr = status.Error(codes.DeadlineExceeded, "search timed out")
	prodMock.lastListReq = nil
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/search?q=ao+thun&shop_id=shop-bach-dang&min_price_vnd=100000&max_price_vnd=400000&sort_by=price_desc&page=1&page_size=10", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("search fallback want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if prodMock.lastListReq == nil || prodMock.lastListReq.GetShopId() != "shop-bach-dang" || prodMock.lastListReq.GetKeyword() != "ao thun" {
		t.Fatalf("search fallback dropped shop_id isolation: req=%+v", prodMock.lastListReq)
	}

	// 4. InvalidArgument does NOT trigger product fallback.
	searchMock.searchErr = status.Error(codes.InvalidArgument, "bad query")
	prodMock.lastListReq = nil
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/search?q=bad&shop_id=shop-bach-dang", nil))
	if rec.Code != http.StatusBadRequest || prodMock.lastListReq != nil {
		t.Fatalf("InvalidArgument must return 400 without fallback: code=%d listReq=%+v", rec.Code, prodMock.lastListReq)
	}
}
