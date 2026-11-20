package router

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/gateway/internal/clients"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type siteUserMock struct {
	userv1.UserServiceClient

	getByHostCalls int
	lastByHost     *userv1.GetSiteByHostRequest
	byHostResp     *userv1.GetSiteByHostResponse
	byHostErr      error

	lastByShop *userv1.GetSiteByShopRequest
	byShopResp *userv1.GetSiteByShopResponse
	byShopErr  error

	getDraftCalls int
	lastGetDraft  *userv1.GetSiteDraftRequest
	getDraftResp  *userv1.GetSiteDraftResponse
	getDraftErr   error

	saveDraftCalls int
	lastSaveDraft  *userv1.SaveSiteDraftRequest
	saveDraftResp  *userv1.SaveSiteDraftResponse
	saveDraftErr   error

	lastPublish *userv1.PublishSiteRequest
	publishResp *userv1.PublishSiteResponse
	publishErr  error

	lastRollback *userv1.RollbackSiteRequest
	rollbackResp *userv1.RollbackSiteResponse
	rollbackErr  error

	lastListVersions *userv1.ListSiteVersionsRequest
	listVersionsResp *userv1.ListSiteVersionsResponse
	listVersionsErr  error

	lastListSites *userv1.ListSitesRequest
	listSitesResp *userv1.ListSitesResponse
	listSitesErr  error

	lastSuspend *userv1.SuspendSiteRequest
	suspendResp *userv1.SuspendSiteResponse
	suspendErr  error
}

func (m *siteUserMock) GetSiteByHost(_ context.Context, in *userv1.GetSiteByHostRequest, _ ...grpc.CallOption) (*userv1.GetSiteByHostResponse, error) {
	m.getByHostCalls++
	m.lastByHost = in
	if m.byHostErr != nil {
		return nil, m.byHostErr
	}
	return m.byHostResp, nil
}

func (m *siteUserMock) GetSiteByShop(_ context.Context, in *userv1.GetSiteByShopRequest, _ ...grpc.CallOption) (*userv1.GetSiteByShopResponse, error) {
	m.lastByShop = in
	if m.byShopErr != nil {
		return nil, m.byShopErr
	}
	return m.byShopResp, nil
}

func (m *siteUserMock) GetSiteDraft(_ context.Context, in *userv1.GetSiteDraftRequest, _ ...grpc.CallOption) (*userv1.GetSiteDraftResponse, error) {
	m.getDraftCalls++
	m.lastGetDraft = in
	if m.getDraftErr != nil {
		return nil, m.getDraftErr
	}
	return m.getDraftResp, nil
}

func (m *siteUserMock) SaveSiteDraft(_ context.Context, in *userv1.SaveSiteDraftRequest, _ ...grpc.CallOption) (*userv1.SaveSiteDraftResponse, error) {
	m.saveDraftCalls++
	m.lastSaveDraft = in
	if m.saveDraftErr != nil {
		return nil, m.saveDraftErr
	}
	return m.saveDraftResp, nil
}

func (m *siteUserMock) PublishSite(_ context.Context, in *userv1.PublishSiteRequest, _ ...grpc.CallOption) (*userv1.PublishSiteResponse, error) {
	m.lastPublish = in
	if m.publishErr != nil {
		return nil, m.publishErr
	}
	return m.publishResp, nil
}

func (m *siteUserMock) RollbackSite(_ context.Context, in *userv1.RollbackSiteRequest, _ ...grpc.CallOption) (*userv1.RollbackSiteResponse, error) {
	m.lastRollback = in
	if m.rollbackErr != nil {
		return nil, m.rollbackErr
	}
	return m.rollbackResp, nil
}

func (m *siteUserMock) ListSiteVersions(_ context.Context, in *userv1.ListSiteVersionsRequest, _ ...grpc.CallOption) (*userv1.ListSiteVersionsResponse, error) {
	m.lastListVersions = in
	if m.listVersionsErr != nil {
		return nil, m.listVersionsErr
	}
	return m.listVersionsResp, nil
}

func (m *siteUserMock) ListSites(_ context.Context, in *userv1.ListSitesRequest, _ ...grpc.CallOption) (*userv1.ListSitesResponse, error) {
	m.lastListSites = in
	if m.listSitesErr != nil {
		return nil, m.listSitesErr
	}
	return m.listSitesResp, nil
}

func (m *siteUserMock) SuspendSite(_ context.Context, in *userv1.SuspendSiteRequest, _ ...grpc.CallOption) (*userv1.SuspendSiteResponse, error) {
	m.lastSuspend = in
	if m.suspendErr != nil {
		return nil, m.suspendErr
	}
	return m.suspendResp, nil
}

type flashEndPromoMock struct {
	promotionv1.PromotionServiceClient
	endCalls int
	lastEnd  *promotionv1.EndFlashSaleWindowRequest
	endResp  *promotionv1.EndFlashSaleWindowResponse
	endErr   error
}

func (m *flashEndPromoMock) EndFlashSaleWindow(_ context.Context, in *promotionv1.EndFlashSaleWindowRequest, _ ...grpc.CallOption) (*promotionv1.EndFlashSaleWindowResponse, error) {
	m.endCalls++
	m.lastEnd = in
	if m.endErr != nil {
		return nil, m.endErr
	}
	return m.endResp, nil
}

type flashStockProductMock struct {
	productv1.ProductServiceClient
	product *productv1.Product
}

func (m *flashStockProductMock) GetProduct(_ context.Context, _ *productv1.GetProductRequest, _ ...grpc.CallOption) (*productv1.GetProductResponse, error) {
	return &productv1.GetProductResponse{Product: m.product}, nil
}

func (m *flashStockProductMock) ListProducts(_ context.Context, _ *productv1.ListProductsRequest, _ ...grpc.CallOption) (*productv1.ListProductsResponse, error) {
	return &productv1.ListProductsResponse{Products: []*productv1.Product{m.product}, Total: 1}, nil
}

func TestSitesGatewayEndpointsRBACAndRawConfigPassthrough(t *testing.T) {
	authMgr, err := auth.NewManager("sites-router-test-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	buyerTok, _, _, _ := authMgr.SignAccessToken("buyer-1", "buyer", "")
	sellerTok, _, _, _ := authMgr.SignAccessToken("seller-1", "seller", "shop-a")
	sellerEmptyShopTok, _, _, _ := authMgr.SignAccessToken("seller-empty", "seller", "")
	adminTok, _, _, _ := authMgr.SignAccessToken("admin-1", "admin", "")
	superAdminTok, _, _, _ := authMgr.SignAccessToken("super-1", "super_admin", "")

	now := timestamppb.New(time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC))
	sampleCfg := `{"theme":{"bg_color":"#F8F6F1","surface_color":"#FFFFFF","text_color":"#182026","accent_color":"#1D4ED8","accent_text_color":"#FFFFFF","font_family":"work-sans","brand_name":"Bach Dang"},"sections":[]}`
	sampleSite := &userv1.Site{
		Id:                     "site-1",
		ShopId:                 "shop-a",
		ShopName:               "Xuong Det Bach Dang",
		Subdomain:              "bach-dang",
		PrimaryHost:            "bach-dang.nexusmart.localhost",
		Status:                 userv1.SiteStatus_SITE_STATUS_PUBLISHED,
		ConfigJson:             sampleCfg,
		PublishedVersionId:     "ver-1",
		PublishedVersionNumber: 1,
		PublishedAt:            now,
		CreatedAt:              now,
		UpdatedAt:              now,
	}
	sampleShop := &userv1.PublicShop{
		Id:          "shop-a",
		Name:        "Xuong Det Bach Dang",
		Description: "Workshop",
		Status:      userv1.ShopStatus_SHOP_STATUS_ACTIVE,
	}
	sampleVersion := &userv1.SiteVersion{
		Id:            "ver-1",
		SiteId:        "site-1",
		VersionNumber: 1,
		ConfigJson:    sampleCfg,
		PublishedBy:   "seller-1",
		Note:          "initial publish",
		CreatedAt:     now,
	}

	userMock := &siteUserMock{
		byHostResp: &userv1.GetSiteByHostResponse{Site: sampleSite, Shop: sampleShop},
		byShopResp: &userv1.GetSiteByShopResponse{Site: sampleSite, Shop: sampleShop},
		getDraftResp: &userv1.GetSiteDraftResponse{
			Site: &userv1.Site{
				Id:          "site-1",
				ShopId:      "shop-a",
				ShopName:    "Xuong Det Bach Dang",
				Subdomain:   "bach-dang",
				PrimaryHost: "bach-dang.nexusmart.localhost",
				Status:      userv1.SiteStatus_SITE_STATUS_DRAFT,
				ConfigJson:  "",
			},
		},
		saveDraftResp: &userv1.SaveSiteDraftResponse{Site: sampleSite},
		publishResp:   &userv1.PublishSiteResponse{Site: sampleSite, Version: sampleVersion},
		rollbackResp:  &userv1.RollbackSiteResponse{Site: sampleSite, Version: sampleVersion},
		listVersionsResp: &userv1.ListSiteVersionsResponse{
			Versions: []*userv1.SiteVersion{sampleVersion},
			Total:    1,
		},
		listSitesResp: &userv1.ListSitesResponse{
			Sites: []*userv1.Site{sampleSite},
			Total: 1,
		},
		suspendResp: &userv1.SuspendSiteResponse{
			Site: &userv1.Site{
				Id:              "site-1",
				ShopId:          "shop-a",
				Subdomain:       "bach-dang",
				PrimaryHost:     "bach-dang.nexusmart.localhost",
				Status:          userv1.SiteStatus_SITE_STATUS_SUSPENDED,
				ConfigJson:      sampleCfg,
				SuspendedReason: "policy violation",
			},
		},
	}

	log := logger.New("sites-router-test", logger.WithWriter(io.Discard))
	h := New(Dependencies{
		ServiceName: "sites-router-test",
		Clients: &clients.Clients{
			User:    userMock,
			Timeout: 5 * time.Second,
		},
		AuthManager: authMgr,
		Logger:      log,
	})

	call := func(method, path, body, tok string) *httptest.ResponseRecorder {
		t.Helper()
		var rBody io.Reader
		if body != "" {
			rBody = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, path, rBody)
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		req.Header.Set("Origin", "https://evil.example")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		for hdr := range rec.Header() {
			if strings.HasPrefix(strings.ToLower(hdr), "access-control-") {
				t.Fatalf("gateway must emit 0 CORS headers on %s %s, found %s", method, path, hdr)
			}
		}
		return rec
	}

	// 1. Public GET /api/v1/sites/by-host
	rec := call(http.MethodGet, "/api/v1/sites/by-host", "", "")
	if rec.Code != http.StatusBadRequest || userMock.getByHostCalls != 0 {
		t.Fatalf("missing host must return 400 with 0 RPC calls: code=%d calls=%d body=%s", rec.Code, userMock.getByHostCalls, rec.Body.String())
	}

	rec = call(http.MethodGet, "/api/v1/sites/by-host?host=bach-dang.nexusmart.localhost", "", "")
	if rec.Code != http.StatusOK || userMock.lastByHost.GetHost() != "bach-dang.nexusmart.localhost" {
		t.Fatalf("by-host failed: code=%d req=%+v body=%s", rec.Code, userMock.lastByHost, rec.Body.String())
	}
	var byHostBody map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &byHostBody); err != nil {
		t.Fatalf("decode by-host: %v", err)
	}
	siteMap, _ := byHostBody["site"].(map[string]any)
	shopMap, _ := byHostBody["shop"].(map[string]any)
	if siteMap["status"] != "PUBLISHED" || siteMap["primary_host"] != "bach-dang.nexusmart.localhost" ||
		siteMap["published_version_id"] != "ver-1" || siteMap["published_version_number"] != float64(1) {
		t.Fatalf("unexpected site DTO: %+v", siteMap)
	}
	if _, ok := siteMap["config"].(map[string]any); !ok {
		t.Fatalf("expected site.config to be decoded JSON object, got %T (%+v)", siteMap["config"], siteMap["config"])
	}
	if shopMap["id"] != "shop-a" || shopMap["status"] != "ACTIVE" {
		t.Fatalf("unexpected public shop DTO: %+v", shopMap)
	}

	userMock.byHostErr = status.Error(codes.NotFound, "site not found")
	rec = call(http.MethodGet, "/api/v1/sites/by-host?host=unknown.nexusmart.localhost", "", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 on unknown host, got %d", rec.Code)
	}
	userMock.byHostErr = nil

	// 2. Public GET /api/v1/sites/by-shop/{shop_id}
	rec = call(http.MethodGet, "/api/v1/sites/by-shop/shop-a", "", "")
	if rec.Code != http.StatusOK || userMock.lastByShop.GetShopId() != "shop-a" {
		t.Fatalf("by-shop failed: code=%d req=%+v", rec.Code, userMock.lastByShop)
	}
	userMock.byShopErr = status.Error(codes.NotFound, "site not found")
	rec = call(http.MethodGet, "/api/v1/sites/by-shop/shop-b", "", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 on unpublished shop, got %d", rec.Code)
	}
	userMock.byShopErr = nil

	// 3. Seller RBAC (401 anonymous, 403 buyer/admin/seller-without-shop) across all seller site routes
	sellerRoutes := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/v1/seller/site", ""},
		{http.MethodPut, "/api/v1/seller/site/draft", `{"subdomain":"bach-dang","config":{}}`},
		{http.MethodPost, "/api/v1/seller/site/publish", `{"note":"v1"}`},
		{http.MethodPost, "/api/v1/seller/site/rollback", `{"version_id":"ver-1"}`},
		{http.MethodGet, "/api/v1/seller/site/versions", ""},
	}
	for _, rt := range sellerRoutes {
		if rec := call(rt.method, rt.path, rt.body, ""); rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s anonymous: got %d, want 401", rt.method, rt.path, rec.Code)
		}
		for label, tok := range map[string]string{
			"buyer":            buyerTok,
			"admin":            adminTok,
			"seller-no-shopid": sellerEmptyShopTok,
		} {
			if rec := call(rt.method, rt.path, rt.body, tok); rec.Code != http.StatusForbidden {
				t.Fatalf("%s %s as %s: got %d, want 403", rt.method, rt.path, label, rec.Code)
			}
		}
	}

	// Direct handler defense-in-depth check: empty JWT shop_id must reject 403 even bypassing router middleware.
	directH := &handler{clients: &clients.Clients{User: userMock, Timeout: 5 * time.Second}, log: log}
	emptySellerClaims := &auth.Claims{Role: "seller", ShopID: ""}
	for name, fn := range map[string]http.HandlerFunc{
		"handleSellerGetSite":          directH.handleSellerGetSite,
		"handleSellerSaveSiteDraft":    directH.handleSellerSaveSiteDraft,
		"handleSellerPublishSite":      directH.handleSellerPublishSite,
		"handleSellerRollbackSite":     directH.handleSellerRollbackSite,
		"handleSellerListSiteVersions": directH.handleSellerListSiteVersions,
	} {
		recDirect := httptest.NewRecorder()
		reqDirect := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(auth.WithClaims(context.Background(), emptySellerClaims))
		fn(recDirect, reqDirect)
		if recDirect.Code != http.StatusForbidden {
			t.Fatalf("direct %s with empty shop_id: got %d, want 403", name, recDirect.Code)
		}
	}

	// 4. Seller GET /api/v1/seller/site ignores query shop_id, maps empty config_json -> {}
	rec = call(http.MethodGet, "/api/v1/seller/site?shop_id=victim-shop", "", sellerTok)
	if rec.Code != http.StatusOK || userMock.lastGetDraft.GetShopId() != "shop-a" {
		t.Fatalf("seller get site: code=%d req=%+v body=%s", rec.Code, userMock.lastGetDraft, rec.Body.String())
	}
	var draftResp map[string]map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &draftResp)
	if draftResp["site"]["status"] != "DRAFT" {
		t.Fatalf("expected DRAFT status, got %+v", draftResp["site"]["status"])
	}
	if cfgObj, ok := draftResp["site"]["config"].(map[string]any); !ok || len(cfgObj) != 0 {
		t.Fatalf("expected empty config_json fallback to {}, got %+v", draftResp["site"]["config"])
	}

	// 5. Seller PUT /api/v1/seller/site/draft:
	//    - Ignores client-supplied shop_id in body & query
	//    - Preserves unknown keys inside config verbatim so services/user DisallowUnknownFields() sees them!
	payloadWithUnknowns := `{
		"shop_id": "victim-shop",
		"subdomain": "bach-dang",
		"config": {
			"theme": {"bg_color": "#FFFFFF", "evil_unknown_theme_key": "boom"},
			"sections": [{"id": "s1", "type": "hero", "visible": true, "props": {"title": "Hi", "injected_prop": 123}}],
			"extra_root_key": true
		}
	}`
	rec = call(http.MethodPut, "/api/v1/seller/site/draft?shop_id=victim-shop", payloadWithUnknowns, sellerTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("seller save draft: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if userMock.lastSaveDraft.GetShopId() != "shop-a" {
		t.Fatalf("seller save draft must use JWT shop_id=shop-a, got %q", userMock.lastSaveDraft.GetShopId())
	}
	forwardedCfg := userMock.lastSaveDraft.GetConfigJson()
	for _, needle := range []string{`"evil_unknown_theme_key"`, `"injected_prop"`, `"extra_root_key"`} {
		if !strings.Contains(forwardedCfg, needle) {
			t.Fatalf("gateway stripped unknown key %s from config_json: %s", needle, forwardedCfg)
		}
	}

	// Missing config -> 400 without calling RPC.
	prevSaveCalls := userMock.saveDraftCalls
	rec = call(http.MethodPut, "/api/v1/seller/site/draft", `{"subdomain":"bach-dang"}`, sellerTok)
	if rec.Code != http.StatusBadRequest || userMock.saveDraftCalls != prevSaveCalls {
		t.Fatalf("missing config must return 400 without RPC: code=%d calls=%d", rec.Code, userMock.saveDraftCalls)
	}

	// Backend gRPC error mapping check on SaveSiteDraft.
	for _, tc := range []struct {
		code     codes.Code
		wantHTTP int
	}{
		{codes.AlreadyExists, http.StatusConflict},
		{codes.InvalidArgument, http.StatusBadRequest},
		{codes.FailedPrecondition, http.StatusConflict},
		{codes.PermissionDenied, http.StatusForbidden},
		{codes.NotFound, http.StatusNotFound},
	} {
		userMock.saveDraftErr = status.Error(tc.code, "mock error")
		rec = call(http.MethodPut, "/api/v1/seller/site/draft", `{"subdomain":"bach-dang","config":{}}`, sellerTok)
		if rec.Code != tc.wantHTTP {
			t.Fatalf("gRPC %v mapped to HTTP %d, want %d", tc.code, rec.Code, tc.wantHTTP)
		}
	}
	userMock.saveDraftErr = nil

	// 6. Seller POST /api/v1/seller/site/publish (both with JSON body and empty body)
	rec = call(http.MethodPost, "/api/v1/seller/site/publish", `{"shop_id":"victim-shop","note":"launch"}`, sellerTok)
	if rec.Code != http.StatusOK || userMock.lastPublish.GetShopId() != "shop-a" || userMock.lastPublish.GetNote() != "launch" {
		t.Fatalf("publish failed: code=%d req=%+v body=%s", rec.Code, userMock.lastPublish, rec.Body.String())
	}
	var pubBody map[string]map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &pubBody)
	if pubBody["site"]["status"] != "PUBLISHED" || pubBody["version"]["id"] != "ver-1" {
		t.Fatalf("unexpected publish response: %+v", pubBody)
	}
	if _, ok := pubBody["version"]["config"].(map[string]any); !ok {
		t.Fatalf("expected version.config as JSON object, got %T", pubBody["version"]["config"])
	}
	rec = call(http.MethodPost, "/api/v1/seller/site/publish", "", sellerTok)
	if rec.Code != http.StatusOK || userMock.lastPublish.GetNote() != "" {
		t.Fatalf("publish with empty body failed: code=%d req=%+v", rec.Code, userMock.lastPublish)
	}

	// 7. Seller POST /api/v1/seller/site/rollback
	rec = call(http.MethodPost, "/api/v1/seller/site/rollback", `{"note":"missing id"}`, sellerTok)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("rollback without version_id must return 400, got %d", rec.Code)
	}
	rec = call(http.MethodPost, "/api/v1/seller/site/rollback", `{"shop_id":"victim-shop","version_id":"ver-1","note":"restore v1"}`, sellerTok)
	if rec.Code != http.StatusOK || userMock.lastRollback.GetShopId() != "shop-a" || userMock.lastRollback.GetVersionId() != "ver-1" {
		t.Fatalf("rollback failed: code=%d req=%+v body=%s", rec.Code, userMock.lastRollback, rec.Body.String())
	}

	// 8. Seller GET /api/v1/seller/site/versions
	rec = call(http.MethodGet, "/api/v1/seller/site/versions?shop_id=victim-shop&page=2&page_size=5", "", sellerTok)
	if rec.Code != http.StatusOK || userMock.lastListVersions.GetShopId() != "shop-a" ||
		userMock.lastListVersions.GetPage() != 2 || userMock.lastListVersions.GetPageSize() != 5 {
		t.Fatalf("list versions failed: code=%d req=%+v body=%s", rec.Code, userMock.lastListVersions, rec.Body.String())
	}
	var versBody map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &versBody)
	vers, _ := versBody["versions"].([]any)
	if len(vers) != 1 || versBody["total"] != float64(1) {
		t.Fatalf("unexpected versions response: %+v", versBody)
	}

	// 9. Admin routes RBAC (401 anonymous, 403 buyer & seller; 200 admin & super_admin)
	adminRoutes := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/v1/admin/sites", ""},
		{http.MethodGet, "/api/v1/admin/sites/shop-a", ""},
		{http.MethodPost, "/api/v1/admin/sites/site-1/suspend", `{"suspend":true,"reason":"abuse"}`},
	}
	for _, rt := range adminRoutes {
		if rec := call(rt.method, rt.path, rt.body, ""); rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s anonymous: got %d, want 401", rt.method, rt.path, rec.Code)
		}
		for label, tok := range map[string]string{"buyer": buyerTok, "seller": sellerTok} {
			if rec := call(rt.method, rt.path, rt.body, tok); rec.Code != http.StatusForbidden {
				t.Fatalf("%s %s as %s: got %d, want 403", rt.method, rt.path, label, rec.Code)
			}
		}
	}

	// Admin GET /api/v1/admin/sites with filters
	rec = call(http.MethodGet, "/api/v1/admin/sites?status=PUBLISHED&q=bach&page=1&page_size=10", "", adminTok)
	if rec.Code != http.StatusOK ||
		userMock.lastListSites.GetStatus() != userv1.SiteStatus_SITE_STATUS_PUBLISHED ||
		userMock.lastListSites.GetQuery() != "bach" ||
		userMock.lastListSites.GetPageSize() != 10 {
		t.Fatalf("admin list sites failed: code=%d req=%+v body=%s", rec.Code, userMock.lastListSites, rec.Body.String())
	}
	var sitesBody map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &sitesBody)
	sitesArr, _ := sitesBody["sites"].([]any)
	if len(sitesArr) != 1 || sitesBody["total"] != float64(1) {
		t.Fatalf("unexpected admin list sites body: %+v", sitesBody)
	}
	if rec = call(http.MethodGet, "/api/v1/admin/sites?status=BOGUS", "", adminTok); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid status filter must return 400, got %d", rec.Code)
	}

	// Admin GET /api/v1/admin/sites/{shop_id}
	rec = call(http.MethodGet, "/api/v1/admin/sites/shop-a", "", superAdminTok)
	if rec.Code != http.StatusOK || userMock.lastGetDraft.GetShopId() != "shop-a" {
		t.Fatalf("admin get site by shop failed: code=%d req=%+v", rec.Code, userMock.lastGetDraft)
	}

	// Admin POST /api/v1/admin/sites/{site_id}/suspend
	rec = call(http.MethodPost, "/api/v1/admin/sites/site-1/suspend", `{"reason":"missing bool"}`, adminTok)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("suspend missing boolean flag must return 400, got %d", rec.Code)
	}
	rec = call(http.MethodPost, "/api/v1/admin/sites/site-1/suspend", `{"suspend":true,"reason":"policy violation"}`, superAdminTok)
	if rec.Code != http.StatusOK || userMock.lastSuspend.GetSiteId() != "site-1" ||
		!userMock.lastSuspend.GetSuspend() || userMock.lastSuspend.GetReason() != "policy violation" {
		t.Fatalf("admin suspend site failed: code=%d req=%+v body=%s", rec.Code, userMock.lastSuspend, rec.Body.String())
	}
	var suspBody map[string]map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &suspBody)
	if suspBody["site"]["status"] != "SUSPENDED" || suspBody["site"]["suspended_reason"] != "policy violation" {
		t.Fatalf("unexpected suspend response: %+v", suspBody)
	}
}

func TestFlashSaleEndWindowAndSKUReservedStockDTO(t *testing.T) {
	authMgr, err := auth.NewManager("flash2b-router-test-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	buyerTok, _, _, _ := authMgr.SignAccessToken("buyer-1", "buyer", "")
	sellerTok, _, _, _ := authMgr.SignAccessToken("seller-1", "seller", "shop-a")
	adminTok, _, _, _ := authMgr.SignAccessToken("admin-1", "admin", "")
	superAdminTok, _, _, _ := authMgr.SignAccessToken("super-1", "super_admin", "")

	promoMock := &flashEndPromoMock{
		endResp: &promotionv1.EndFlashSaleWindowResponse{
			Window: &promotionv1.FlashSale{
				Id:     "fs-win-1",
				Name:   "Slot 12h",
				Status: "ENDED",
			},
			ReleasedItems: 3,
		},
	}
	productMock := &flashStockProductMock{
		product: &productv1.Product{
			Id:     "prod-1",
			ShopId: "shop-a",
			Name:   "Tee",
			Status: productv1.ProductStatus_PRODUCT_STATUS_APPROVED,
			Skus: []*productv1.SKU{
				{
					Id:                     "sku-1",
					ProductId:              "prod-1",
					ShopId:                 "shop-a",
					SkuCode:                "TEE-M",
					Stock:                  100,
					ReservedStock:          5,
					FlashSaleReservedStock: 20,
					AvailableStock:         75,
					IsActive:               true,
				},
			},
		},
	}

	h := New(Dependencies{
		ServiceName: "flash2b-router-test",
		Clients: &clients.Clients{
			Promotion: promoMock,
			Product:   productMock,
			Timeout:   5 * time.Second,
		},
		AuthManager: authMgr,
		Logger:      logger.New("flash2b-router-test", logger.WithWriter(io.Discard)),
	})

	call := func(method, path, tok string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, nil)
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// RBAC for POST /api/v1/admin/flash-sales/{id}/end
	if rec := call(http.MethodPost, "/api/v1/admin/flash-sales/fs-win-1/end", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous end flash sale: got %d, want 401", rec.Code)
	}
	if rec := call(http.MethodPost, "/api/v1/admin/flash-sales/fs-win-1/end", buyerTok); rec.Code != http.StatusForbidden {
		t.Fatalf("buyer end flash sale: got %d, want 403", rec.Code)
	}
	if rec := call(http.MethodPost, "/api/v1/admin/flash-sales/fs-win-1/end", sellerTok); rec.Code != http.StatusForbidden {
		t.Fatalf("seller end flash sale: got %d, want 403", rec.Code)
	}
	if promoMock.endCalls != 0 {
		t.Fatalf("unauthorized callers must not invoke EndFlashSaleWindow, got %d calls", promoMock.endCalls)
	}

	// Admin & super_admin succeed.
	for _, tok := range []string{adminTok, superAdminTok} {
		rec := call(http.MethodPost, "/api/v1/admin/flash-sales/fs-win-1/end", tok)
		if rec.Code != http.StatusOK || promoMock.lastEnd.GetWindowId() != "fs-win-1" {
			t.Fatalf("admin end flash sale failed: code=%d req=%+v body=%s", rec.Code, promoMock.lastEnd, rec.Body.String())
		}
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if body["released_items"] != float64(3) {
			t.Fatalf("expected released_items=3, got %+v", body)
		}
		fsMap, _ := body["flash_sale"].(map[string]any)
		winMap, _ := body["window"].(map[string]any)
		if fsMap["id"] != "fs-win-1" || fsMap["status"] != "ENDED" || winMap["id"] != "fs-win-1" {
			t.Fatalf("unexpected end flash sale payload: %+v", body)
		}
	}

	// NotFound maps to 404.
	promoMock.endErr = status.Error(codes.NotFound, "flash sale window not found")
	if rec := call(http.MethodPost, "/api/v1/admin/flash-sales/missing/end", adminTok); rec.Code != http.StatusNotFound {
		t.Fatalf("missing flash sale window: got %d, want 404", rec.Code)
	}
	promoMock.endErr = nil

	// SKU DTO passes through flash_sale_reserved_stock while preserving available_stock.
	for _, path := range []string{"/api/v1/products/prod-1", "/api/v1/products"} {
		rec := call(http.MethodGet, path, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s failed: %d", path, rec.Code)
		}
		raw := rec.Body.String()
		for _, want := range []string{
			`"stock":100`,
			`"reserved_stock":5`,
			`"flash_sale_reserved_stock":20`,
			`"available_stock":75`,
		} {
			if !strings.Contains(raw, want) {
				t.Fatalf("GET %s missing %s in response: %s", path, want, raw)
			}
		}
	}
}
