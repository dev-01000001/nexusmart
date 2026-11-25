package router

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	searchv1 "github.com/nexusmart/core/gen/go/nexusmart/search/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/pkg/common/metrics"
	"github.com/nexusmart/core/services/gateway/internal/clients"
	"github.com/nexusmart/core/services/gateway/internal/config"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type b1SearchClient struct {
	searchv1.SearchServiceClient
	mu             sync.Mutex
	lastSearchReq  *searchv1.SearchProductsRequest
	lastSearchMD   metadata.MD
	searchErr      error
	searchDelay    time.Duration
	lastSuggestReq *searchv1.SuggestRequest
	lastSuggestMD  metadata.MD
	suggestErr     error
	lastHistReq    *searchv1.ListSearchHistoryRequest
	lastHistMD     metadata.MD
	lastDelReq     *searchv1.DeleteSearchHistoryRequest
	lastReindexReq *searchv1.ReindexAllRequest
	reindexErr     error
	lastStatusReq  *searchv1.GetReindexStatusRequest
}

func (s *b1SearchClient) SearchProducts(ctx context.Context, in *searchv1.SearchProductsRequest, _ ...grpc.CallOption) (*searchv1.SearchProductsResponse, error) {
	md, _ := metadata.FromOutgoingContext(ctx)
	s.mu.Lock()
	s.lastSearchReq = in
	s.lastSearchMD = md.Copy()
	delay := s.searchDelay
	err := s.searchErr
	s.mu.Unlock()

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, err
	}
	return &searchv1.SearchProductsResponse{
		Items: []*searchv1.SearchProductHit{
			{
				Id:            "prod-1",
				ShopId:        "shop-1",
				ShopName:      "Xưởng Áo",
				ShopSlug:      "xuong-ao",
				CategoryId:    "cat-1",
				CategorySlug:  "ao-thun",
				CategoryName:  "Áo thun",
				Name:          "Áo thun cotton",
				Slug:          "ao-thun-cotton",
				ThumbnailUrl:  "/products/ao-thun.png",
				MinPriceVnd:   150000,
				MaxPriceVnd:   190000,
				Status:        "APPROVED",
				RatingAvgX100: 475,
				ReviewCount:   12,
				InStock:       true,
				SkuCount:      2,
				Score:         0.95,
				SkuIds:        []string{"sku-1", "sku-2"},
				DefaultSkuId:  "sku-1",
				Colors:        []string{"Đen"},
				Sizes:         []string{"M", "L"},
			},
		},
		Total:          1,
		Page:           in.GetPage(),
		PageSize:       in.GetPageSize(),
		CategoryFacets: []*searchv1.FacetCount{{Id: "cat-1", Name: "Áo thun", Slug: "ao-thun", Count: 1}},
		ShopFacets:     []*searchv1.FacetCount{{Id: "shop-1", Name: "Xưởng Áo", Slug: "xuong-ao", Count: 1}},
		DegradedMode:   "",
		UsedVector:     true,
	}, nil
}

func (s *b1SearchClient) Suggest(ctx context.Context, in *searchv1.SuggestRequest, _ ...grpc.CallOption) (*searchv1.SuggestResponse, error) {
	md, _ := metadata.FromOutgoingContext(ctx)
	s.mu.Lock()
	s.lastSuggestReq = in
	s.lastSuggestMD = md.Copy()
	err := s.suggestErr
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &searchv1.SuggestResponse{
		Keywords:   []string{"áo thun", "áo polo"},
		Categories: []*searchv1.SuggestEntity{{Id: "cat-1", Name: "Áo thun", Slug: "ao-thun"}},
		Shops:      []*searchv1.SuggestEntity{{Id: "shop-1", Name: "Xưởng Áo", Slug: "xuong-ao"}},
		Trending:   []string{"áo chạy bộ"},
		Products: []*searchv1.SearchProductHit{
			{Id: "prod-1", Name: "Áo thun cotton", Slug: "ao-thun-cotton", MinPriceVnd: 150000},
		},
	}, nil
}

func (s *b1SearchClient) ListSearchHistory(ctx context.Context, in *searchv1.ListSearchHistoryRequest, _ ...grpc.CallOption) (*searchv1.ListSearchHistoryResponse, error) {
	md, _ := metadata.FromOutgoingContext(ctx)
	s.mu.Lock()
	s.lastHistReq = in
	s.lastHistMD = md.Copy()
	s.mu.Unlock()
	return &searchv1.ListSearchHistoryResponse{
		Items: []*searchv1.SearchHistoryItem{
			{Id: "hist-1", Query: "ao thun", CreatedAt: timestamppb.Now()},
		},
	}, nil
}

func (s *b1SearchClient) DeleteSearchHistory(_ context.Context, in *searchv1.DeleteSearchHistoryRequest, _ ...grpc.CallOption) (*searchv1.DeleteSearchHistoryResponse, error) {
	s.mu.Lock()
	s.lastDelReq = in
	s.mu.Unlock()
	return &searchv1.DeleteSearchHistoryResponse{Ok: true}, nil
}

func (s *b1SearchClient) ReindexAll(_ context.Context, in *searchv1.ReindexAllRequest, _ ...grpc.CallOption) (*searchv1.ReindexAllResponse, error) {
	s.mu.Lock()
	s.lastReindexReq = in
	err := s.reindexErr
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &searchv1.ReindexAllResponse{
		JobId:  "reindex-job-42",
		Status: searchv1.ReindexJobStatus_REINDEX_JOB_STATUS_RUNNING,
	}, nil
}

func (s *b1SearchClient) GetReindexStatus(_ context.Context, in *searchv1.GetReindexStatusRequest, _ ...grpc.CallOption) (*searchv1.GetReindexStatusResponse, error) {
	s.mu.Lock()
	s.lastStatusReq = in
	s.mu.Unlock()
	return &searchv1.GetReindexStatusResponse{
		JobId:             in.GetJobId(),
		Status:            searchv1.ReindexJobStatus_REINDEX_JOB_STATUS_COMPLETED,
		ProductsIndexed:   20000,
		EmbeddingsUpdated: 20000,
		StartedAt:         timestamppb.Now(),
		FinishedAt:        timestamppb.Now(),
	}, nil
}

type b1ProductFallbackClient struct {
	productv1.ProductServiceClient
	mu          sync.Mutex
	lastListReq *productv1.ListProductsRequest
	lastListMD  metadata.MD
	listErr     error
	customItems []*productv1.Product
}

func (p *b1ProductFallbackClient) ListProducts(ctx context.Context, in *productv1.ListProductsRequest, _ ...grpc.CallOption) (*productv1.ListProductsResponse, error) {
	md, _ := metadata.FromOutgoingContext(ctx)
	p.mu.Lock()
	p.lastListReq = in
	p.lastListMD = md.Copy()
	err := p.listErr
	custom := p.customItems
	p.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if custom != nil {
		return &productv1.ListProductsResponse{
			Products: custom,
			Total:    int64(len(custom)),
		}, nil
	}
	return &productv1.ListProductsResponse{
		Products: []*productv1.Product{
			{
				Id:            "prod-fb-1",
				ShopId:        "shop-1",
				CategoryId:    "cat-1",
				Name:          "Áo Fallback",
				Slug:          "ao-fallback",
				Description:   "Mô tả áo fallback",
				Images:        []string{"/products/fb.png"},
				Status:        productv1.ProductStatus_PRODUCT_STATUS_APPROVED,
				MinPriceVnd:   120000,
				MaxPriceVnd:   180000,
				RatingAvgX100: 450,
				ReviewCount:   7,
				Skus: []*productv1.SKU{
					{
						Id:                "sku-fb-1",
						PriceVnd:          120000,
						Stock:             10,
						AvailableStock:    10,
						CommissionRateBps: 850, // MUST NEVER leak!
						IsActive:          true,
						Attributes:        map[string]string{"color": "Trắng", "size": "M"},
					},
				},
			},
			{
				Id:            "prod-fb-hidden",
				ShopId:        "shop-1",
				Name:          "SP Ẩn",
				Status:        productv1.ProductStatus_PRODUCT_STATUS_HIDDEN,
				MinPriceVnd:   99000,
				MaxPriceVnd:   99000,
				ShopSuspended: false,
			},
			{
				Id:            "prod-fb-suspended",
				ShopId:        "shop-2",
				Name:          "SP Gian Khoá",
				Status:        productv1.ProductStatus_PRODUCT_STATUS_APPROVED,
				ShopSuspended: true,
			},
		},
		Total: 3,
	}, nil
}

type b1AuditUserClient struct {
	userv1.UserServiceClient
	mu        sync.Mutex
	auditReqs []*userv1.WriteAuditLogRequest
}

func (u *b1AuditUserClient) WriteAuditLog(_ context.Context, in *userv1.WriteAuditLogRequest, _ ...grpc.CallOption) (*userv1.WriteAuditLogResponse, error) {
	u.mu.Lock()
	u.auditReqs = append(u.auditReqs, in)
	u.mu.Unlock()
	return &userv1.WriteAuditLogResponse{AuditLog: &userv1.AuditLog{Id: "audit-b1"}}, nil
}

func TestB1_SearchOptionalAuthFallbackAndTimeoutG13(t *testing.T) {
	authMgr, err := auth.NewManager("w3b3-b1-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	buyerTok, _, _, _ := authMgr.SignAccessToken("buyer-1", auth.RoleBuyer, "")
	adminTok, _, _, _ := authMgr.SignAccessToken("admin-1", auth.RoleAdmin, "")

	searchMock := &b1SearchClient{}
	prodMock := &b1ProductFallbackClient{}
	h := New(Dependencies{
		ServiceName: "gateway-b1-test",
		Clients: &clients.Clients{
			Search:  searchMock,
			Product: prodMock,
			Timeout: 5 * time.Second,
		},
		AuthManager:           authMgr,
		SearchFallbackTimeout: 1500 * time.Millisecond,
		Logger:                logger.New("b1-test", logger.WithWriter(io.Discard)),
	})

	// 1. Authenticated GET /api/v1/search forwards JWT (G14) and query filters, omits commission_rate_bps.
	reqAuth := httptest.NewRequest(http.MethodGet, "/api/v1/search?q=ao+thun&min_price_vnd=100000&max_price_vnd=300000&min_rating_x100=400&in_stock_only=true&sort_by=price_asc&mode=hybrid&size=M&color=Den&page=1&page_size=20", nil)
	reqAuth.Header.Set("Authorization", "Bearer "+buyerTok)
	recAuth := httptest.NewRecorder()
	h.ServeHTTP(recAuth, reqAuth)
	if recAuth.Code != http.StatusOK {
		t.Fatalf("GET /search authed: want 200, got %d (%s)", recAuth.Code, recAuth.Body.String())
	}
	if vals := searchMock.lastSearchMD.Get(auth.MDAuthorization); len(vals) == 0 || vals[0] != "Bearer "+buyerTok {
		t.Fatalf("GET /search authed must forward buyer JWT for history (G14), got %v", vals)
	}
	if searchMock.lastSearchReq.GetMinRatingX100() != 400 || searchMock.lastSearchReq.GetMode() != searchv1.SearchMode_SEARCH_MODE_HYBRID || !searchMock.lastSearchReq.GetInStockOnly() {
		t.Fatalf("unexpected SearchProductsRequest: %+v", searchMock.lastSearchReq)
	}
	if strings.Contains(recAuth.Body.String(), "commission_rate_bps") {
		t.Fatalf("GET /search leaked commission_rate_bps: %s", recAuth.Body.String())
	}

	// 2. Fallback on Unavailable -> 200 degraded_mode="product_fallback", strips JWT (even admin!), no hidden/suspended/commission_rate_bps.
	searchMock.mu.Lock()
	searchMock.searchErr = status.Error(codes.Unavailable, "meilisearch down")
	searchMock.mu.Unlock()
	reqUnavail := httptest.NewRequest(http.MethodGet, "/api/v1/search?q=ao+thun", nil)
	reqUnavail.Header.Set("Authorization", "Bearer "+adminTok)
	recUnavail := httptest.NewRecorder()
	h.ServeHTTP(recUnavail, reqUnavail)
	if recUnavail.Code != http.StatusOK {
		t.Fatalf("fallback on Unavailable: want 200, got %d (%s)", recUnavail.Code, recUnavail.Body.String())
	}
	if vals := prodMock.lastListMD.Get(auth.MDAuthorization); len(vals) != 0 {
		t.Fatalf("fallback Product.ListProducts must use PublicOutgoingContext (no JWT), got %v", vals)
	}
	bodyFB := recUnavail.Body.String()
	if !strings.Contains(bodyFB, `"degraded_mode":"product_fallback"`) || !strings.Contains(bodyFB, `"prod-fb-1"`) {
		t.Fatalf("fallback response missing degraded_mode or product: %s", bodyFB)
	}
	for _, forbidden := range []string{"commission_rate_bps", "prod-fb-hidden", "prod-fb-suspended"} {
		if strings.Contains(bodyFB, forbidden) {
			t.Fatalf("fallback response leaked %q: %s", forbidden, bodyFB)
		}
	}

	// 3. Fallback G13 timeout: SearchService sleeps 2s -> gateway responds 200 in < 1.6s with degraded_mode="product_fallback".
	searchMock.mu.Lock()
	searchMock.searchErr = nil
	searchMock.searchDelay = 2 * time.Second
	searchMock.mu.Unlock()

	start := time.Now()
	recSlow := httptest.NewRecorder()
	h.ServeHTTP(recSlow, httptest.NewRequest(http.MethodGet, "/api/v1/search?q=ao+chay+bo", nil))
	elapsed := time.Since(start)
	if recSlow.Code != http.StatusOK {
		t.Fatalf("fallback on 2s slow SearchService: want 200, got %d (%s)", recSlow.Code, recSlow.Body.String())
	}
	if elapsed >= 1600*time.Millisecond {
		t.Fatalf("fallback took %v, want < 1.6s", elapsed)
	}
	if !strings.Contains(recSlow.Body.String(), `"degraded_mode":"product_fallback"`) || strings.Contains(recSlow.Body.String(), "commission_rate_bps") {
		t.Fatalf("slow fallback body mismatch: %s", recSlow.Body.String())
	}

	// Reset delay and verify Suggest fallback -> Product.ListProducts(page_size=5).
	searchMock.mu.Lock()
	searchMock.searchDelay = 0
	searchMock.suggestErr = status.Error(codes.Unavailable, "search down")
	searchMock.mu.Unlock()
	recSug := httptest.NewRecorder()
	h.ServeHTTP(recSug, httptest.NewRequest(http.MethodGet, "/api/v1/search/suggest?q=ao", nil))
	if recSug.Code != http.StatusOK || prodMock.lastListReq.GetPageSize() != 5 {
		t.Fatalf("suggest fallback: code=%d page_size=%d body=%s", recSug.Code, prodMock.lastListReq.GetPageSize(), recSug.Body.String())
	}
	if strings.Contains(recSug.Body.String(), "commission_rate_bps") {
		t.Fatalf("suggest fallback leaked commission_rate_bps: %s", recSug.Body.String())
	}

	// 4. SearchClient == nil also degrades cleanly to product_fallback.
	hNil := New(Dependencies{
		ServiceName: "gateway-b1-nil",
		Clients: &clients.Clients{
			Search:  nil,
			Product: prodMock,
			Timeout: 5 * time.Second,
		},
		AuthManager: authMgr,
		Logger:      logger.New("b1-nil", logger.WithWriter(io.Discard)),
	})
	recNil := httptest.NewRecorder()
	hNil.ServeHTTP(recNil, httptest.NewRequest(http.MethodGet, "/api/v1/search?q=ao", nil))
	if recNil.Code != http.StatusOK || !strings.Contains(recNil.Body.String(), `"degraded_mode":"product_fallback"`) {
		t.Fatalf("nil SearchClient fallback: code=%d body=%s", recNil.Code, recNil.Body.String())
	}
}

func TestB1_SearchHistoryAndAdminReindexRBACMatrixG11G14(t *testing.T) {
	authMgr, err := auth.NewManager("w3b3-b1-rbac-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	tokFor := func(role string) string {
		shopID := ""
		if role == auth.RoleSeller {
			shopID = "shop-1"
		}
		tok, _, _, _ := authMgr.SignAccessToken("u-"+role, role, shopID)
		return tok
	}

	searchMock := &b1SearchClient{}
	userMock := &b1AuditUserClient{}
	h := New(Dependencies{
		ServiceName: "gateway-b1-rbac",
		Clients: &clients.Clients{
			Search:  searchMock,
			User:    userMock,
			Timeout: 5 * time.Second,
		},
		AuthManager: authMgr,
		Logger:      logger.New("b1-rbac", logger.WithWriter(io.Discard)),
	})

	// 1. Search history requires auth; GET/DELETE/DELETE/{id} work when authenticated.
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/search/history"},
		{http.MethodDelete, "/api/v1/search/history"},
		{http.MethodDelete, "/api/v1/search/history/hist-1"},
	} {
		recAnon := httptest.NewRecorder()
		h.ServeHTTP(recAnon, httptest.NewRequest(tc.method, tc.path, nil))
		if recAnon.Code != http.StatusUnauthorized {
			t.Fatalf("anon %s %s: want 401, got %d", tc.method, tc.path, recAnon.Code)
		}
	}

	buyerTok := tokFor(auth.RoleBuyer)
	reqHist := httptest.NewRequest(http.MethodGet, "/api/v1/search/history?limit=15", nil)
	reqHist.Header.Set("Authorization", "Bearer "+buyerTok)
	recHist := httptest.NewRecorder()
	h.ServeHTTP(recHist, reqHist)
	if recHist.Code != http.StatusOK || searchMock.lastHistReq.GetLimit() != 15 || !strings.Contains(recHist.Body.String(), `"hist-1"`) {
		t.Fatalf("GET /search/history: code=%d req=%+v body=%s", recHist.Code, searchMock.lastHistReq, recHist.Body.String())
	}

	reqDelOne := httptest.NewRequest(http.MethodDelete, "/api/v1/search/history/hist-1", nil)
	reqDelOne.Header.Set("Authorization", "Bearer "+buyerTok)
	recDelOne := httptest.NewRecorder()
	h.ServeHTTP(recDelOne, reqDelOne)
	if recDelOne.Code != http.StatusOK || searchMock.lastDelReq.GetId() != "hist-1" || searchMock.lastDelReq.GetClearAll() {
		t.Fatalf("DELETE /search/history/hist-1: code=%d req=%+v", recDelOne.Code, searchMock.lastDelReq)
	}

	reqDelAll := httptest.NewRequest(http.MethodDelete, "/api/v1/search/history", nil)
	reqDelAll.Header.Set("Authorization", "Bearer "+buyerTok)
	recDelAll := httptest.NewRecorder()
	h.ServeHTTP(recDelAll, reqDelAll)
	if recDelAll.Code != http.StatusOK || !searchMock.lastDelReq.GetClearAll() {
		t.Fatalf("DELETE /search/history: code=%d req=%+v", recDelAll.Code, searchMock.lastDelReq)
	}

	// 2. RBAC matrix: 6 internal roles + buyer + seller × POST/GET /admin/search/reindex.
	roleMatrix := []struct {
		role     string
		wantPost int
		wantGet  int
	}{
		{auth.RoleSuperAdmin, http.StatusAccepted, http.StatusOK},
		{auth.RoleAdmin, http.StatusAccepted, http.StatusOK},
		{auth.RoleOps, http.StatusAccepted, http.StatusOK},
		{auth.RoleModerator, http.StatusForbidden, http.StatusForbidden},
		{auth.RoleCSKH, http.StatusForbidden, http.StatusForbidden},
		{auth.RoleAccountant, http.StatusForbidden, http.StatusForbidden},
		{auth.RoleBuyer, http.StatusForbidden, http.StatusForbidden},
		{auth.RoleSeller, http.StatusForbidden, http.StatusForbidden},
	}

	userMock.mu.Lock()
	userMock.auditReqs = nil
	userMock.mu.Unlock()

	for _, rm := range roleMatrix {
		tok := tokFor(rm.role)
		reqP := httptest.NewRequest(http.MethodPost, "/api/v1/admin/search/reindex", strings.NewReader(`{"batch_size":250}`))
		reqP.Header.Set("Authorization", "Bearer "+tok)
		recP := httptest.NewRecorder()
		h.ServeHTTP(recP, reqP)
		if recP.Code != rm.wantPost {
			t.Fatalf("POST /admin/search/reindex role=%s: want %d, got %d (%s)", rm.role, rm.wantPost, recP.Code, recP.Body.String())
		}

		reqG := httptest.NewRequest(http.MethodGet, "/api/v1/admin/search/reindex/reindex-job-42", nil)
		reqG.Header.Set("Authorization", "Bearer "+tok)
		recG := httptest.NewRecorder()
		h.ServeHTTP(recG, reqG)
		if recG.Code != rm.wantGet {
			t.Fatalf("GET /admin/search/reindex/{job_id} role=%s: want %d, got %d (%s)", rm.role, rm.wantGet, recG.Code, recG.Body.String())
		}
	}

	// Verify AdminAudit recorded the 3 allowed POST reindex calls with job_id as target_id.
	userMock.mu.Lock()
	audits := append([]*userv1.WriteAuditLogRequest(nil), userMock.auditReqs...)
	userMock.mu.Unlock()
	if len(audits) != 3 {
		t.Fatalf("expected 3 AdminAudit entries for allowed POST /admin/search/reindex, got %d", len(audits))
	}
	if audits[0].GetTargetId() != "reindex-job-42" {
		t.Fatalf("audit target_id = %q, want reindex-job-42", audits[0].GetTargetId())
	}

	// Concurrent reindex conflict -> 409 REINDEX_ALREADY_RUNNING.
	searchMock.mu.Lock()
	searchMock.reindexErr = status.Error(codes.FailedPrecondition, "REINDEX_ALREADY_RUNNING: busy")
	searchMock.mu.Unlock()
	reqDup := httptest.NewRequest(http.MethodPost, "/api/v1/admin/search/reindex", nil)
	reqDup.Header.Set("Authorization", "Bearer "+tokFor(auth.RoleOps))
	recDup := httptest.NewRecorder()
	h.ServeHTTP(recDup, reqDup)
	if recDup.Code != http.StatusConflict || !strings.Contains(recDup.Body.String(), `"code":"REINDEX_ALREADY_RUNNING"`) {
		t.Fatalf("concurrent reindex: want 409 REINDEX_ALREADY_RUNNING, got %d (%s)", recDup.Code, recDup.Body.String())
	}
}

func TestB1_GatewayHealthyWhenSearchDownAtStartupAndNoKeywordLeakG14(t *testing.T) {
	// 1. Reserve and immediately close a local TCP port to simulate `search` being down at startup.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	closedAddr := lis.Addr().String()
	_ = lis.Close()

	cfg := config.Load()
	cfg.SearchGRPCAddr = closedAddr
	cfg.SearchFallbackTimeout = 500 * time.Millisecond

	startDial := time.Now()
	grpcClients, err := clients.New(cfg)
	if err != nil {
		t.Fatalf("clients.New must succeed lazily when search is down: %v", err)
	}
	defer grpcClients.Close()
	if time.Since(startDial) > 500*time.Millisecond {
		t.Fatalf("clients.New blocked while dialing dead search service (%v)", time.Since(startDial))
	}

	// Replace Product with fake fallback while keeping the real dead Search gRPC client.
	prodMock := &b1ProductFallbackClient{}
	grpcClients.Product = prodMock

	// Capture OTel spans and JSON logs to verify G14 (no raw search keyword in logs, spans, or audit — even on errors!).
	spanRec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRec))
	prevTP := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	defer func() {
		otel.SetTracerProvider(prevTP)
		_ = tp.Shutdown(context.Background())
	}()

	var logBuf bytes.Buffer
	log := logger.New("gateway-g14-test", logger.WithWriter(&logBuf))
	reg := prometheus.NewRegistry()
	ops := metrics.NewOpsHandler("gateway", nil, reg)
	userMock := &b1AuditUserClient{}
	grpcClients.User = userMock

	h := New(Dependencies{
		ServiceName:           "gateway",
		Clients:               grpcClients,
		OpsHandler:            ops,
		SearchFallbackTimeout: 500 * time.Millisecond,
		Logger:                log,
	})

	// Gateway /healthz stays 200 OK while search is down.
	recHealth := httptest.NewRecorder()
	h.ServeHTTP(recHealth, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recHealth.Code != http.StatusOK {
		t.Fatalf("/healthz want 200 when search is down, got %d", recHealth.Code)
	}

	const secretKeyword = "bi-mat-tu-khoa-98765"

	// Search with dead real Search gRPC client -> degrades to product_fallback (200).
	recFB := httptest.NewRecorder()
	h.ServeHTTP(recFB, httptest.NewRequest(http.MethodGet, "/api/v1/search?q="+secretKeyword, nil))
	if recFB.Code != http.StatusOK || !strings.Contains(recFB.Body.String(), `"degraded_mode":"product_fallback"`) {
		t.Fatalf("search against dead search client want 200 product_fallback, got %d (%s)", recFB.Code, recFB.Body.String())
	}

	// Search with invalid query param -> 400 error.
	recBad := httptest.NewRecorder()
	h.ServeHTTP(recBad, httptest.NewRequest(http.MethodGet, "/api/v1/search?q="+secretKeyword+"&min_rating_x100=999", nil))
	if recBad.Code != http.StatusBadRequest {
		t.Fatalf("invalid min_rating_x100 want 400, got %d", recBad.Code)
	}

	// Search where BOTH search and fallback product fail (500/503) AND upstream error echoes the keyword!
	prodMock.mu.Lock()
	prodMock.listErr = status.Error(codes.Internal, "db failed for query "+secretKeyword)
	prodMock.mu.Unlock()
	recErr := httptest.NewRecorder()
	h.ServeHTTP(recErr, httptest.NewRequest(http.MethodGet, "/api/v1/search?q="+secretKeyword, nil))
	if recErr.Code != http.StatusInternalServerError {
		t.Fatalf("search + fallback both failing: want 500, got %d (%s)", recErr.Code, recErr.Body.String())
	}
	recSugErr := httptest.NewRecorder()
	h.ServeHTTP(recSugErr, httptest.NewRequest(http.MethodGet, "/api/v1/search/suggest?q="+secretKeyword, nil))
	if recSugErr.Code != http.StatusInternalServerError {
		t.Fatalf("suggest + fallback both failing: want 500, got %d (%s)", recSugErr.Code, recSugErr.Body.String())
	}

	// Assert secretKeyword NEVER leaked into access/error logs!
	if strings.Contains(logBuf.String(), secretKeyword) {
		t.Fatalf("G14 violation: search keyword leaked into logs:\n%s", logBuf.String())
	}

	// Assert secretKeyword NEVER leaked into any OTel span name, attribute, event, or status description!
	for _, sp := range spanRec.Ended() {
		if strings.Contains(sp.Name(), secretKeyword) || strings.Contains(sp.Status().Description, secretKeyword) {
			t.Fatalf("G14 violation: search keyword leaked into span %q status %q", sp.Name(), sp.Status().Description)
		}
		for _, attr := range sp.Attributes() {
			if strings.Contains(attr.Value.Emit(), secretKeyword) {
				t.Fatalf("G14 violation: search keyword leaked into span attr %s=%s", attr.Key, attr.Value.Emit())
			}
		}
		for _, ev := range sp.Events() {
			for _, attr := range ev.Attributes {
				if strings.Contains(attr.Value.Emit(), secretKeyword) {
					t.Fatalf("G14 violation: search keyword leaked into span event %s attr %s=%s", ev.Name, attr.Key, attr.Value.Emit())
				}
			}
		}
	}

	// Assert secretKeyword NEVER entered audit logs!
	userMock.mu.Lock()
	for _, a := range userMock.auditReqs {
		raw, _ := json.Marshal(a)
		if strings.Contains(string(raw), secretKeyword) {
			t.Fatalf("G14 violation: search keyword leaked into audit log: %s", string(raw))
		}
	}
	userMock.mu.Unlock()
}

func TestB1_SearchFallbackSizeColorColorCodeAndVietnameseFacetKeysGcGe(t *testing.T) {
	prodMock := &b1ProductFallbackClient{
		customItems: []*productv1.Product{
			{
				Id:          "prod-vn-keys",
				ShopId:      "shop-1",
				Name:        "Áo Polo Việt",
				Slug:        "ao-polo-viet",
				Status:      productv1.ProductStatus_PRODUCT_STATUS_APPROVED,
				MinPriceVnd: 200000,
				MaxPriceVnd: 200000,
				Skus: []*productv1.SKU{
					{
						Id:             "sku-vn-1",
						IsActive:       true,
						Stock:          10,
						AvailableStock: 10,
						Attributes: map[string]string{
							"Màu sắc":    "Trắng tinh",
							"color_code": "trang",
							"Kích thước": "XL",
						},
					},
					{
						Id:             "sku-vn-inactive",
						IsActive:       false,
						Stock:          5,
						AvailableStock: 5,
						Attributes: map[string]string{
							"Màu sắc":    "Đỏ rực",
							"color_code": "do-ruc",
							"Kích thước": "XXL",
						},
					},
				},
			},
			{
				Id:          "prod-combo",
				ShopId:      "shop-1",
				Name:        "Combo Áo Thun",
				Slug:        "combo-ao-thun",
				Status:      productv1.ProductStatus_PRODUCT_STATUS_APPROVED,
				MinPriceVnd: 350000,
				MaxPriceVnd: 350000,
				Skus: []*productv1.SKU{
					{
						Id:             "sku-combo-1",
						IsActive:       true,
						Stock:          8,
						AvailableStock: 8,
						Attributes: map[string]string{
							"mau-sac":    "Combo Xám / Than / Đen",
							"color_code": "combo-trungtinh",
							"kich-co":    "L",
						},
					},
				},
			},
		},
	}

	h := New(Dependencies{
		ServiceName: "gateway-b1-fallback-variants",
		Clients: &clients.Clients{
			Search:  nil, // force product_fallback
			Product: prodMock,
			Timeout: 5 * time.Second,
		},
		Logger: logger.New("b1-fb-variants", logger.WithWriter(io.Discard)),
	})

	doSearch := func(query string) struct {
		Items        []searchProductHitDTO `json:"items"`
		Total        int64                 `json:"total"`
		DegradedMode string                `json:"degraded_mode"`
	} {
		t.Helper()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/search?"+query, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /search?%s: want 200, got %d (%s)", query, rec.Code, rec.Body.String())
		}
		var resp struct {
			Items        []searchProductHitDTO `json:"items"`
			Total        int64                 `json:"total"`
			DegradedMode string                `json:"degraded_mode"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode /search?%s: %v", query, err)
		}
		if resp.DegradedMode != "product_fallback" {
			t.Fatalf("degraded_mode = %q, want product_fallback", resp.DegradedMode)
		}
		return resp
	}

	// 1. G-c: Vietnamese facet keys ("Màu sắc", "Kích thước", "mau-sac", "kich-co") populate colors/sizes on active SKUs only.
	all := doSearch("q=ao")
	if len(all.Items) != 2 {
		t.Fatalf("unfiltered fallback want 2 items, got %d", len(all.Items))
	}
	if len(all.Items[0].Colors) != 1 || all.Items[0].Colors[0] != "Trắng tinh" ||
		len(all.Items[0].Sizes) != 1 || all.Items[0].Sizes[0] != "XL" {
		t.Fatalf("prod-vn-keys facets = colors:%v sizes:%v, want [Trắng tinh] [XL]", all.Items[0].Colors, all.Items[0].Sizes)
	}
	if len(all.Items[1].Colors) != 1 || all.Items[1].Colors[0] != "Combo Xám / Than / Đen" ||
		len(all.Items[1].Sizes) != 1 || all.Items[1].Sizes[0] != "L" {
		t.Fatalf("prod-combo facets = colors:%v sizes:%v, want [Combo Xám / Than / Đen] [L]", all.Items[1].Colors, all.Items[1].Sizes)
	}

	// 2. G-c: size filter matches normalized size and excludes non-matching products.
	bySize := doSearch("size=xl")
	if len(bySize.Items) != 1 || bySize.Items[0].ID != "prod-vn-keys" || bySize.Total != 1 {
		t.Fatalf("size=xl fallback: got items=%+v total=%d", bySize.Items, bySize.Total)
	}

	// 3. G-c: color filter matches normalized Vietnamese color name (accent-folded).
	byColorName := doSearch("color=trang+tinh")
	if len(byColorName.Items) != 1 || byColorName.Items[0].ID != "prod-vn-keys" {
		t.Fatalf("color=trang+tinh fallback: got %+v", byColorName.Items)
	}

	// 4. G-e: color filter matches active SKU color_code ("trang" and "combo-trungtinh").
	byColorCode1 := doSearch("color=trang")
	if len(byColorCode1.Items) != 1 || byColorCode1.Items[0].ID != "prod-vn-keys" {
		t.Fatalf("color=trang (color_code) fallback: got %+v", byColorCode1.Items)
	}
	byColorCode2 := doSearch("color=combo-trungtinh&size=l")
	if len(byColorCode2.Items) != 1 || byColorCode2.Items[0].ID != "prod-combo" {
		t.Fatalf("color=combo-trungtinh&size=l fallback: got %+v", byColorCode2.Items)
	}

	// 5. G-e: inactive SKU color / color_code / size MUST NOT match.
	byInactiveColor := doSearch("color=do-ruc")
	if len(byInactiveColor.Items) != 0 || byInactiveColor.Total != 0 {
		t.Fatalf("inactive SKU color_code matched in fallback: %+v", byInactiveColor.Items)
	}
	byInactiveSize := doSearch("size=XXL")
	if len(byInactiveSize.Items) != 0 || byInactiveSize.Total != 0 {
		t.Fatalf("inactive SKU size matched in fallback: %+v", byInactiveSize.Items)
	}
}
