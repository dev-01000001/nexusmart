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

type siteTemplateUserMock struct {
	userv1.UserServiceClient

	listResp *userv1.ListSiteTemplatesResponse
	listErr  error

	lastGetReq *userv1.GetSiteTemplateRequest
	getResp    *userv1.GetSiteTemplateResponse
	getErr     error

	lastApplyReq *userv1.ApplySiteTemplateRequest
	applyResp    *userv1.ApplySiteTemplateResponse
	applyErr     error
}

func (m *siteTemplateUserMock) ListSiteTemplates(_ context.Context, _ *userv1.ListSiteTemplatesRequest, _ ...grpc.CallOption) (*userv1.ListSiteTemplatesResponse, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.listResp, nil
}

func (m *siteTemplateUserMock) GetSiteTemplate(_ context.Context, in *userv1.GetSiteTemplateRequest, _ ...grpc.CallOption) (*userv1.GetSiteTemplateResponse, error) {
	m.lastGetReq = in
	if m.getErr != nil {
		return nil, m.getErr
	}
	return m.getResp, nil
}

func (m *siteTemplateUserMock) ApplySiteTemplate(_ context.Context, in *userv1.ApplySiteTemplateRequest, _ ...grpc.CallOption) (*userv1.ApplySiteTemplateResponse, error) {
	m.lastApplyReq = in
	if m.applyErr != nil {
		return nil, m.applyErr
	}
	return m.applyResp, nil
}

func TestGatewaySiteTemplateRoutes(t *testing.T) {
	authMgr, err := auth.NewManager("router-t-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	buyerTok, _, _, _ := authMgr.SignAccessToken("buyer-1", "buyer", "")
	sellerTok, _, _, _ := authMgr.SignAccessToken("seller-1", "seller", "shop-jwt-99")
	sellerNoShopTok, _, _, _ := authMgr.SignAccessToken("seller-2", "seller", "")
	adminTok, _, _, _ := authMgr.SignAccessToken("admin-1", "admin", "")

	now := timestamppb.New(time.Now().UTC())
	sampleTemplate := &userv1.SiteTemplate{
		Id:              "editorial",
		Slug:            "editorial",
		Name:            "Tạp Chí Editorial",
		Category:        "editorial",
		Archetype:       "editorial",
		Description:     "Bố cục tạp chí",
		PreviewImageUrl: "/banners/campaign-office.jpg",
		Tags:            []string{"editorial", "magazine"},
		ConfigJson:      `{"schema_version":2,"template_id":"editorial"}`,
		IsActive:        true,
		SortOrder:       2,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	userMock := &siteTemplateUserMock{
		listResp: &userv1.ListSiteTemplatesResponse{
			Templates: []*userv1.SiteTemplate{sampleTemplate},
		},
		getResp: &userv1.GetSiteTemplateResponse{
			Template: sampleTemplate,
		},
		applyResp: &userv1.ApplySiteTemplateResponse{
			Site: &userv1.Site{
				Id:         "site-1",
				ShopId:     "shop-jwt-99",
				ShopName:   "Xưởng Bạch Đằng",
				Subdomain:  "bach-dang",
				Status:     userv1.SiteStatus_SITE_STATUS_PUBLISHED,
				ConfigJson: `{"schema_version":2,"template_id":"editorial"}`,
				CreatedAt:  now,
				UpdatedAt:  now,
			},
			Template: sampleTemplate,
		},
	}

	h := New(Dependencies{
		ServiceName: "gateway-t-test",
		Clients: &clients.Clients{
			User:    userMock,
			Timeout: 5 * time.Second,
		},
		AuthManager: authMgr,
		Logger:      logger.New("gateway-t-test", logger.WithWriter(io.Discard)),
	})

	// 1. Public GET /api/v1/site-templates succeeds without JWT.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/site-templates", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/site-templates: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var listBody struct {
		Templates []siteTemplateDTO `json:"templates"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listBody); err != nil {
		t.Fatalf("decode list body: %v", err)
	}
	if len(listBody.Templates) != 1 || listBody.Templates[0].ID != "editorial" || len(listBody.Templates[0].Tags) != 2 {
		t.Fatalf("unexpected templates payload: %+v", listBody)
	}

	// 2. Public GET /api/v1/site-templates/{id} succeeds without JWT.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/site-templates/editorial", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/site-templates/editorial: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if userMock.lastGetReq == nil || userMock.lastGetReq.GetId() != "editorial" {
		t.Fatalf("unexpected GetSiteTemplate req: %+v", userMock.lastGetReq)
	}

	// 3. Unknown template maps NotFound -> 404.
	userMock.getErr = status.Error(codes.NotFound, "site template not found")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/site-templates/missing", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET unknown template: want 404, got %d", rec.Code)
	}
	userMock.getErr = nil

	// 4. POST /api/v1/seller/site/apply-template RBAC checks.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/seller/site/apply-template", strings.NewReader(`{"template_id":"editorial"}`)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anon apply-template: want 401, got %d", rec.Code)
	}

	for name, tok := range map[string]string{
		"buyer":           buyerTok,
		"admin":           adminTok,
		"seller-no-shop":  sellerNoShopTok,
	} {
		rec = httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/seller/site/apply-template", strings.NewReader(`{"template_id":"editorial"}`))
		req.Header.Set("Authorization", "Bearer "+tok)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s apply-template: want 403, got %d", name, rec.Code)
		}
	}

	// 5. Invalid JSON and empty template_id return 400.
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/seller/site/apply-template", strings.NewReader(`{invalid`))
	req.Header.Set("Authorization", "Bearer "+sellerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid json apply-template: want 400, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/seller/site/apply-template", strings.NewReader(`{"template_id":"   "}`))
	req.Header.Set("Authorization", "Bearer "+sellerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty template_id apply-template: want 400, got %d", rec.Code)
	}

	// 6. Valid seller request MUST ignore any client-supplied shop_id and forward JWT shop_id.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/seller/site/apply-template", strings.NewReader(`{"template_id":"editorial","shop_id":"spoofed-shop-id","subdomain":"bach-dang"}`))
	req.Header.Set("Authorization", "Bearer "+sellerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid seller apply-template: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if userMock.lastApplyReq == nil {
		t.Fatalf("expected ApplySiteTemplate gRPC call")
	}
	if userMock.lastApplyReq.GetShopId() != "shop-jwt-99" {
		t.Fatalf("JWT shop_id must be enforced, got %q", userMock.lastApplyReq.GetShopId())
	}
	if userMock.lastApplyReq.GetTemplateId() != "editorial" || userMock.lastApplyReq.GetSubdomain() != "bach-dang" {
		t.Fatalf("unexpected ApplySiteTemplate req: %+v", userMock.lastApplyReq)
	}
	var applyBody struct {
		Site     *siteDTO         `json:"site"`
		Template *siteTemplateDTO `json:"template"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &applyBody); err != nil {
		t.Fatalf("decode apply response: %v", err)
	}
	if applyBody.Site == nil || applyBody.Site.ShopID != "shop-jwt-99" || applyBody.Template == nil || applyBody.Template.ID != "editorial" {
		t.Fatalf("unexpected apply response body: %+v", applyBody)
	}
}
