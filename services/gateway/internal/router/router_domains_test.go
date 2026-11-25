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

	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/gateway/internal/clients"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type domainUserMock struct {
	userv1.UserServiceClient

	listCalls int
	lastList  *userv1.ListSiteDomainsRequest
	listResp  *userv1.ListSiteDomainsResponse
	listErr   error

	addCalls int
	lastAdd  *userv1.AddCustomDomainRequest
	addResp  *userv1.AddCustomDomainResponse
	addErr   error

	checkCalls int
	lastCheck  *userv1.CheckCustomDomainRequest
	checkResp  *userv1.CheckCustomDomainResponse
	checkErr   error

	removeCalls int
	lastRemove  *userv1.RemoveCustomDomainRequest
	removeResp  *userv1.RemoveCustomDomainResponse
	removeErr   error

	byHostCalls int
	lastByHost  *userv1.GetSiteByHostRequest
	byHostResp  *userv1.GetSiteByHostResponse
	byHostErr   error
}

func (m *domainUserMock) ListSiteDomains(_ context.Context, in *userv1.ListSiteDomainsRequest, _ ...grpc.CallOption) (*userv1.ListSiteDomainsResponse, error) {
	m.listCalls++
	m.lastList = in
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.listResp, nil
}

func (m *domainUserMock) AddCustomDomain(_ context.Context, in *userv1.AddCustomDomainRequest, _ ...grpc.CallOption) (*userv1.AddCustomDomainResponse, error) {
	m.addCalls++
	m.lastAdd = in
	if m.addErr != nil {
		return nil, m.addErr
	}
	return m.addResp, nil
}

func (m *domainUserMock) CheckCustomDomain(_ context.Context, in *userv1.CheckCustomDomainRequest, _ ...grpc.CallOption) (*userv1.CheckCustomDomainResponse, error) {
	m.checkCalls++
	m.lastCheck = in
	if m.checkErr != nil {
		return nil, m.checkErr
	}
	return m.checkResp, nil
}

func (m *domainUserMock) RemoveCustomDomain(_ context.Context, in *userv1.RemoveCustomDomainRequest, _ ...grpc.CallOption) (*userv1.RemoveCustomDomainResponse, error) {
	m.removeCalls++
	m.lastRemove = in
	if m.removeErr != nil {
		return nil, m.removeErr
	}
	return m.removeResp, nil
}

func (m *domainUserMock) GetSiteByHost(_ context.Context, in *userv1.GetSiteByHostRequest, _ ...grpc.CallOption) (*userv1.GetSiteByHostResponse, error) {
	m.byHostCalls++
	m.lastByHost = in
	if m.byHostErr != nil {
		return nil, m.byHostErr
	}
	return m.byHostResp, nil
}

func TestSiteDomainsGatewayRBACHandlersAndErrorMapping(t *testing.T) {
	authMgr, err := auth.NewManager("domains-router-test-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	buyerTok, _, _, _ := authMgr.SignAccessToken("buyer-1", "buyer", "")
	sellerTok, _, _, _ := authMgr.SignAccessToken("seller-1", "seller", "shop-a")
	sellerEmptyShopTok, _, _, _ := authMgr.SignAccessToken("seller-empty", "seller", "")
	adminTok, _, _, _ := authMgr.SignAccessToken("admin-1", "admin", "")
	superAdminTok, _, _, _ := authMgr.SignAccessToken("super-1", "super_admin", "")

	createdAt := timestamppb.New(time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC))
	verifiedAt := timestamppb.New(time.Date(2026, 11, 21, 10, 5, 0, 0, time.UTC))

	pendingCustom := &userv1.Domain{
		Id:        "dom-custom-1",
		Host:      "hanoi-knit.verified.test",
		Kind:      "CUSTOM",
		Status:    "PENDING",
		IsPrimary: false,
		Verification: []*userv1.DomainVerificationRecord{
			{Type: "CNAME", Name: "hanoi-knit.verified.test", Value: "shops.nexusmart.localhost"},
			{Type: "TXT", Name: "_cf-custom-hostname.hanoi-knit.verified.test", Value: "mock-verify-hanoi-knit.verified.test"},
		},
		LastError: "",
		CreatedAt: createdAt,
	}
	activeCustom := &userv1.Domain{
		Id:        "dom-custom-1",
		Host:      "hanoi-knit.verified.test",
		Kind:      "CUSTOM",
		Status:    "ACTIVE",
		IsPrimary: true,
		Verification: []*userv1.DomainVerificationRecord{
			{Type: "CNAME", Name: "hanoi-knit.verified.test", Value: "shops.nexusmart.localhost"},
		},
		LastError:  "",
		VerifiedAt: verifiedAt,
		CreatedAt:  createdAt,
	}
	subdomainEntry := &userv1.Domain{
		Id:           "dom-sub-1",
		Host:         "hanoi-knit.nexusmart.localhost",
		Kind:         "SUBDOMAIN",
		Status:       "ACTIVE",
		IsPrimary:    false,
		Verification: nil,
		LastError:    "",
		VerifiedAt:   createdAt,
		CreatedAt:    createdAt,
	}

	userMock := &domainUserMock{
		listResp: &userv1.ListSiteDomainsResponse{
			Domains: []*userv1.Domain{activeCustom, subdomainEntry},
		},
		addResp:    &userv1.AddCustomDomainResponse{Domain: pendingCustom},
		checkResp:  &userv1.CheckCustomDomainResponse{Domain: activeCustom},
		removeResp: &userv1.RemoveCustomDomainResponse{},
		byHostResp: &userv1.GetSiteByHostResponse{
			Site: &userv1.Site{
				Id:          "site-1",
				ShopId:      "shop-a",
				ShopName:    "Hanoi Knit",
				Subdomain:   "hanoi-knit",
				PrimaryHost: "hanoi-knit.verified.test",
				Status:      userv1.SiteStatus_SITE_STATUS_PUBLISHED,
				ConfigJson:  `{"sections":[]}`,
				CreatedAt:   createdAt,
				UpdatedAt:   verifiedAt,
			},
			Shop: &userv1.PublicShop{
				Id:          "shop-a",
				Name:        "Hanoi Knit",
				Description: "Knitwear",
				Status:      userv1.ShopStatus_SHOP_STATUS_ACTIVE,
			},
		},
	}

	log := logger.New("domains-router-test", logger.WithWriter(io.Discard))
	h := New(Dependencies{
		ServiceName: "domains-router-test",
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

	// 1. Seller RBAC matrix: 401 anonymous, 403 buyer/admin/super_admin/seller-without-shop, 0 RPC calls.
	sellerRoutes := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/v1/seller/site/domains", ""},
		{http.MethodPost, "/api/v1/seller/site/domains", `{"hostname":"hanoi-knit.verified.test"}`},
		{http.MethodPost, "/api/v1/seller/site/domains/dom-custom-1/check", ""},
		{http.MethodDelete, "/api/v1/seller/site/domains/dom-custom-1", ""},
	}
	for _, rt := range sellerRoutes {
		if rec := call(rt.method, rt.path, rt.body, ""); rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s anonymous: got %d, want 401", rt.method, rt.path, rec.Code)
		}
		for label, tok := range map[string]string{
			"buyer":            buyerTok,
			"admin":            adminTok,
			"super_admin":      superAdminTok,
			"seller-no-shopid": sellerEmptyShopTok,
		} {
			if rec := call(rt.method, rt.path, rt.body, tok); rec.Code != http.StatusForbidden {
				t.Fatalf("%s %s as %s: got %d, want 403", rt.method, rt.path, label, rec.Code)
			}
		}
	}

	// 2. Admin RBAC matrix: 401 anonymous, 403 buyer/seller/seller-without-shop, 0 RPC calls.
	adminRoutes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/admin/sites/shop-a/domains"},
		{http.MethodDelete, "/api/v1/admin/sites/shop-a/domains/dom-custom-1"},
	}
	for _, rt := range adminRoutes {
		if rec := call(rt.method, rt.path, "", ""); rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s anonymous: got %d, want 401", rt.method, rt.path, rec.Code)
		}
		for label, tok := range map[string]string{
			"buyer":            buyerTok,
			"seller":           sellerTok,
			"seller-no-shopid": sellerEmptyShopTok,
		} {
			if rec := call(rt.method, rt.path, "", tok); rec.Code != http.StatusForbidden {
				t.Fatalf("%s %s as %s: got %d, want 403", rt.method, rt.path, label, rec.Code)
			}
		}
	}

	if userMock.listCalls != 0 || userMock.addCalls != 0 || userMock.checkCalls != 0 || userMock.removeCalls != 0 {
		t.Fatalf("unauthorized callers must make 0 RPC calls, got list=%d add=%d check=%d remove=%d",
			userMock.listCalls, userMock.addCalls, userMock.checkCalls, userMock.removeCalls)
	}

	// 3. Direct handler defense-in-depth check: empty JWT shop_id must reject 403 bypassing router middleware.
	directH := &handler{clients: &clients.Clients{User: userMock, Timeout: 5 * time.Second}, log: log}
	emptySellerClaims := &auth.Claims{Role: "seller", ShopID: ""}
	for name, fn := range map[string]http.HandlerFunc{
		"handleSellerListSiteDomains":    directH.handleSellerListSiteDomains,
		"handleSellerAddCustomDomain":    directH.handleSellerAddCustomDomain,
		"handleSellerCheckCustomDomain":  directH.handleSellerCheckCustomDomain,
		"handleSellerRemoveCustomDomain": directH.handleSellerRemoveCustomDomain,
	} {
		recDirect := httptest.NewRecorder()
		reqDirect := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"hostname":"shop.example.com"}`)).
			WithContext(auth.WithClaims(context.Background(), emptySellerClaims))
		fn(recDirect, reqDirect)
		if recDirect.Code != http.StatusForbidden {
			t.Fatalf("direct %s with empty shop_id: got %d, want 403", name, recDirect.Code)
		}
	}
	if userMock.listCalls != 0 || userMock.addCalls != 0 || userMock.checkCalls != 0 || userMock.removeCalls != 0 {
		t.Fatalf("direct empty shop_id calls must make 0 RPC calls")
	}

	// 4. Seller POST /api/v1/seller/site/domains:
	//    - Invalid JSON -> 400 INVALID_JSON (0 RPC calls)
	//    - Empty/whitespace hostname -> 400 invalid_hostname (0 RPC calls)
	//    - Valid request ignores spoofed shop_id in body & query, returns 201 {domain} with snake_case fields
	rec := call(http.MethodPost, "/api/v1/seller/site/domains", `{bad-json`, sellerTok)
	if rec.Code != http.StatusBadRequest || userMock.addCalls != 0 {
		t.Fatalf("invalid JSON: code=%d calls=%d body=%s", rec.Code, userMock.addCalls, rec.Body.String())
	}
	var errEnv struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &errEnv); err != nil || errEnv.Error.Code != "INVALID_JSON" {
		t.Fatalf("expected error.code=INVALID_JSON, got %s", rec.Body.String())
	}

	for _, emptyPayload := range []string{`{}`, `{"hostname":""}`, `{"hostname":"   "}`} {
		rec = call(http.MethodPost, "/api/v1/seller/site/domains", emptyPayload, sellerTok)
		if rec.Code != http.StatusBadRequest || userMock.addCalls != 0 {
			t.Fatalf("empty hostname %s: code=%d calls=%d body=%s", emptyPayload, rec.Code, userMock.addCalls, rec.Body.String())
		}
		errEnv.Error.Code = ""
		if err := json.Unmarshal(rec.Body.Bytes(), &errEnv); err != nil || errEnv.Error.Code != "invalid_hostname" {
			t.Fatalf("empty hostname %s: want error.code=invalid_hostname, got %s", emptyPayload, rec.Body.String())
		}
	}

	rec = call(
		http.MethodPost,
		"/api/v1/seller/site/domains?shop_id=victim-shop",
		`{"shop_id":"victim-shop","hostname":"  hanoi-knit.verified.test  "}`,
		sellerTok,
	)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add custom domain: want 201, got %d body=%s", rec.Code, rec.Body.String())
	}
	if userMock.addCalls != 1 || userMock.lastAdd.GetShopId() != "shop-a" || userMock.lastAdd.GetHostname() != "hanoi-knit.verified.test" {
		t.Fatalf("unexpected AddCustomDomain RPC request: calls=%d req=%+v", userMock.addCalls, userMock.lastAdd)
	}
	var addBody map[string]map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &addBody); err != nil {
		t.Fatalf("decode add response: %v", err)
	}
	dom := addBody["domain"]
	if dom["id"] != "dom-custom-1" ||
		dom["host"] != "hanoi-knit.verified.test" ||
		dom["kind"] != "CUSTOM" ||
		dom["status"] != "PENDING" ||
		dom["is_primary"] != false ||
		dom["last_error"] != "" ||
		dom["created_at"] != "2026-11-21T10:00:00Z" {
		t.Fatalf("unexpected domain DTO: %+v", dom)
	}
	if _, hasVerified := dom["verified_at"]; hasVerified {
		t.Fatalf("unset verified_at should be omitted, got %+v", dom["verified_at"])
	}
	verRecs, ok := dom["verification"].([]any)
	if !ok || len(verRecs) != 2 {
		t.Fatalf("expected 2 verification records, got %+v", dom["verification"])
	}
	firstRec, _ := verRecs[0].(map[string]any)
	if firstRec["type"] != "CNAME" || firstRec["name"] != "hanoi-knit.verified.test" || firstRec["value"] != "shops.nexusmart.localhost" {
		t.Fatalf("unexpected first verification record: %+v", firstRec)
	}

	// 5. gRPC error mapping on POST /api/v1/seller/site/domains.
	for _, tc := range []struct {
		name     string
		grpcErr  error
		wantHTTP int
		wantCode string
	}{
		{
			name:     "invalid_hostname",
			grpcErr:  status.Error(codes.InvalidArgument, "invalid_hostname: cannot use root domain"),
			wantHTTP: http.StatusBadRequest,
			wantCode: "invalid_hostname",
		},
		{
			name:     "domain_taken",
			grpcErr:  status.Error(codes.AlreadyExists, "domain_taken: host already registered"),
			wantHTTP: http.StatusConflict,
			wantCode: "domain_taken",
		},
		{
			name:     "domain_limit",
			grpcErr:  status.Error(codes.FailedPrecondition, "domain_limit: maximum 1 custom domain per site"),
			wantHTTP: http.StatusConflict,
			wantCode: "domain_limit",
		},
		{
			name:     "unavailable",
			grpcErr:  status.Error(codes.Unavailable, "cloudflare api unreachable"),
			wantHTTP: http.StatusServiceUnavailable,
			wantCode: "UNAVAILABLE",
		},
		{
			name:     "generic_invalid_argument",
			grpcErr:  status.Error(codes.InvalidArgument, "shop_id is required"),
			wantHTTP: http.StatusBadRequest,
			wantCode: "INVALID_ARGUMENT",
		},
	} {
		userMock.addErr = tc.grpcErr
		rec = call(http.MethodPost, "/api/v1/seller/site/domains", `{"hostname":"shop.example.com"}`, sellerTok)
		if rec.Code != tc.wantHTTP {
			t.Fatalf("%s: got HTTP %d, want %d (body=%s)", tc.name, rec.Code, tc.wantHTTP, rec.Body.String())
		}
		errEnv.Error.Code = ""
		if err := json.Unmarshal(rec.Body.Bytes(), &errEnv); err != nil || errEnv.Error.Code != tc.wantCode {
			t.Fatalf("%s: got code %q, want %q (body=%s)", tc.name, errEnv.Error.Code, tc.wantCode, rec.Body.String())
		}
	}
	userMock.addErr = nil

	// 6. Seller GET /api/v1/seller/site/domains:
	//    - Empty list serializes as {"domains":[]} (never null)
	//    - Populated list ignores spoofed shop_id, serializes empty verification as [] (never null)
	userMock.listResp = &userv1.ListSiteDomainsResponse{Domains: nil}
	rec = call(http.MethodGet, "/api/v1/seller/site/domains?shop_id=victim-shop", "", sellerTok)
	if rec.Code != http.StatusOK || userMock.lastList.GetShopId() != "shop-a" {
		t.Fatalf("empty seller list domains: code=%d req=%+v body=%s", rec.Code, userMock.lastList, rec.Body.String())
	}
	var rawList map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &rawList); err != nil {
		t.Fatalf("decode raw empty list: %v", err)
	}
	if string(rawList["domains"]) != "[]" {
		t.Fatalf("empty domains slice must serialize as [], got %s", rawList["domains"])
	}

	userMock.listResp = &userv1.ListSiteDomainsResponse{Domains: []*userv1.Domain{activeCustom, subdomainEntry}}
	rec = call(http.MethodGet, "/api/v1/seller/site/domains?shop_id=victim-shop", "", sellerTok)
	if rec.Code != http.StatusOK || userMock.lastList.GetShopId() != "shop-a" {
		t.Fatalf("populated seller list domains: code=%d req=%+v body=%s", rec.Code, userMock.lastList, rec.Body.String())
	}
	var listBody struct {
		Domains []map[string]json.RawMessage `json:"domains"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listBody); err != nil || len(listBody.Domains) != 2 {
		t.Fatalf("unexpected populated list body: err=%v body=%s", err, rec.Body.String())
	}
	if string(listBody.Domains[1]["verification"]) != "[]" {
		t.Fatalf("empty domain verification must serialize as [], got %s", listBody.Domains[1]["verification"])
	}
	if string(listBody.Domains[0]["verified_at"]) != `"2026-11-21T10:05:00Z"` ||
		string(listBody.Domains[0]["is_primary"]) != "true" {
		t.Fatalf("unexpected primary custom domain fields: %+v", listBody.Domains[0])
	}

	// 7. Seller POST /api/v1/seller/site/domains/{id}/check -> 200 {domain}, ignores spoofed shop_id.
	rec = call(http.MethodPost, "/api/v1/seller/site/domains/dom-custom-1/check?shop_id=victim-shop", "", sellerTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("check custom domain: want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if userMock.lastCheck.GetShopId() != "shop-a" || userMock.lastCheck.GetDomainId() != "dom-custom-1" {
		t.Fatalf("unexpected CheckCustomDomain RPC request: %+v", userMock.lastCheck)
	}
	var checkBody map[string]map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &checkBody); err != nil {
		t.Fatalf("decode check response: %v", err)
	}
	if checkBody["domain"]["id"] != "dom-custom-1" ||
		checkBody["domain"]["status"] != "ACTIVE" ||
		checkBody["domain"]["is_primary"] != true ||
		checkBody["domain"]["verified_at"] != "2026-11-21T10:05:00Z" {
		t.Fatalf("unexpected check domain DTO: %+v", checkBody["domain"])
	}

	// 8. Seller DELETE /api/v1/seller/site/domains/{id} -> 204 No Content empty body, ignores spoofed shop_id.
	rec = call(http.MethodDelete, "/api/v1/seller/site/domains/dom-custom-1?shop_id=victim-shop", "", sellerTok)
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("seller delete domain: want 204 empty body, got code=%d body=%q", rec.Code, rec.Body.String())
	}
	if userMock.lastRemove.GetShopId() != "shop-a" || userMock.lastRemove.GetDomainId() != "dom-custom-1" {
		t.Fatalf("unexpected seller RemoveCustomDomain RPC request: %+v", userMock.lastRemove)
	}

	// 9. Admin GET /api/v1/admin/sites/{shop_id}/domains & DELETE /api/v1/admin/sites/{shop_id}/domains/{id}.
	for label, tok := range map[string]string{"admin": adminTok, "super_admin": superAdminTok} {
		rec = call(http.MethodGet, "/api/v1/admin/sites/shop-z/domains", "", tok)
		if rec.Code != http.StatusOK || userMock.lastList.GetShopId() != "shop-z" {
			t.Fatalf("%s list site domains: code=%d req=%+v body=%s", label, rec.Code, userMock.lastList, rec.Body.String())
		}
		var adminList map[string][]any
		if err := json.Unmarshal(rec.Body.Bytes(), &adminList); err != nil || len(adminList["domains"]) != 2 {
			t.Fatalf("%s list site domains payload: err=%v body=%s", label, err, rec.Body.String())
		}

		rec = call(http.MethodDelete, "/api/v1/admin/sites/shop-z/domains/dom-99", "", tok)
		if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
			t.Fatalf("%s delete custom domain: want 204 empty body, got code=%d body=%q", label, rec.Code, rec.Body.String())
		}
		if userMock.lastRemove.GetShopId() != "shop-z" || userMock.lastRemove.GetDomainId() != "dom-99" {
			t.Fatalf("%s unexpected RemoveCustomDomain RPC request: %+v", label, userMock.lastRemove)
		}
	}

	// 10. Public GET /api/v1/sites/by-host?host=hanoi-knit.verified.test returns 200 with primary_host set to custom host.
	rec = call(http.MethodGet, "/api/v1/sites/by-host?host=hanoi-knit.verified.test", "", "")
	if rec.Code != http.StatusOK || userMock.lastByHost.GetHost() != "hanoi-knit.verified.test" {
		t.Fatalf("by-host custom domain failed: code=%d req=%+v body=%s", rec.Code, userMock.lastByHost, rec.Body.String())
	}
	var byHostBody map[string]map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &byHostBody); err != nil {
		t.Fatalf("decode by-host response: %v", err)
	}
	if byHostBody["site"]["primary_host"] != "hanoi-knit.verified.test" ||
		byHostBody["site"]["subdomain"] != "hanoi-knit" ||
		byHostBody["shop"]["id"] != "shop-a" {
		t.Fatalf("unexpected by-host custom domain response: %+v", byHostBody)
	}
}
