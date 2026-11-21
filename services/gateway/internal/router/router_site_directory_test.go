package router

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/gateway/internal/clients"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type directoryUserMock struct {
	userv1.UserServiceClient
	last    *userv1.ListPublishedSitesRequest
	lastMD  metadata.MD
	resp    *userv1.ListPublishedSitesResponse
	calls   int
	callErr error
}

func (m *directoryUserMock) ListPublishedSites(ctx context.Context, in *userv1.ListPublishedSitesRequest, _ ...grpc.CallOption) (*userv1.ListPublishedSitesResponse, error) {
	m.calls++
	m.last = in
	m.lastMD, _ = metadata.FromOutgoingContext(ctx)
	if m.callErr != nil {
		return nil, m.callErr
	}
	return m.resp, nil
}

func TestPublicSiteDirectory(t *testing.T) {
	mock := &directoryUserMock{resp: &userv1.ListPublishedSitesResponse{
		Sites: []*userv1.PublishedSiteSummary{{
			SiteId:      "site-1",
			ShopId:      "shop-1",
			ShopName:    "Xưởng Dệt Bạch Đằng",
			Subdomain:   "bach-dang",
			PrimaryHost: "bach-dang.nexusmart.localhost",
			LogoUrl:     "/logo.png",
			CoverUrl:    "",
			Tagline:     "Áo thun cotton",
			PublishedAt: timestamppb.Now(),
		}},
		Total:    7,
		Page:     2,
		PageSize: 48,
	}}
	h := New(Dependencies{
		ServiceName: "directory-test",
		Clients:     &clients.Clients{User: mock, Timeout: 5 * time.Second},
		Logger:      logger.New("directory-test", logger.WithWriter(io.Discard)),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sites?page=2&page_size=500", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("anonymous directory: want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if mock.calls != 1 || mock.last.GetPage() != 2 || mock.last.GetPageSize() != 500 {
		t.Fatalf("query params must be forwarded for upstream clamping, got %+v", mock.last)
	}
	if len(mock.lastMD.Get("authorization")) != 0 {
		t.Fatalf("anonymous request must not carry authorization upstream: %v", mock.lastMD)
	}

	var body struct {
		Sites []map[string]any `json:"sites"`
		Total int64            `json:"total"`
		Page  int32            `json:"page"`
		Size  int32            `json:"page_size"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Total != 7 || body.Page != 2 || body.Size != 48 || len(body.Sites) != 1 {
		t.Fatalf("unexpected envelope: %+v", body)
	}
	want := map[string]any{
		"site_id": "site-1", "shop_id": "shop-1", "shop_name": "Xưởng Dệt Bạch Đằng",
		"subdomain": "bach-dang", "primary_host": "bach-dang.nexusmart.localhost",
		"logo_url": "/logo.png", "cover_url": "", "tagline": "Áo thun cotton",
	}
	got := body.Sites[0]
	if len(got) != len(want) {
		t.Fatalf("directory card must expose exactly the contract fields, got %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("field %s: want %v, got %v", k, v, got[k])
		}
	}

	// Empty directory renders an empty array, not null.
	mock.resp = &userv1.ListPublishedSitesResponse{Page: 1, PageSize: 24}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/sites", nil))
	if rec.Code != http.StatusOK || mock.last.GetPage() != 1 || mock.last.GetPageSize() != 24 {
		t.Fatalf("defaults: code=%d req=%+v", rec.Code, mock.last)
	}
	var empty map[string]json.RawMessage
	_ = json.Unmarshal(rec.Body.Bytes(), &empty)
	if string(empty["sites"]) != "[]" {
		t.Fatalf("empty directory must be [], got %s", empty["sites"])
	}
}
