package service

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/user/internal/db"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func sampleSiteConfigJSON(t *testing.T, brandName, heroTitle string) string {
	t.Helper()
	cfg := map[string]any{
		"theme": map[string]any{
			"bg_color":          "#F8F6F1",
			"surface_color":     "#FFFFFF",
			"text_color":        "#182026",
			"accent_color":      "#1D4ED8",
			"accent_text_color": "#FFFFFF",
			"font_family":       "work-sans",
			"logo_url":          "/banners/vietnam-workshop.jpg",
			"banner_url":        "/banners/campaign-everyday.jpg",
			"brand_name":        brandName,
			"tagline":           "Xưởng may nội địa chuẩn chất lượng",
		},
		"sections": []any{
			map[string]any{
				"id":      "hero-main",
				"type":    "hero",
				"visible": true,
				"props": map[string]any{
					"eyebrow":   "Chính hãng",
					"title":     heroTitle,
					"subtitle":  "Vải cotton 220gsm thoáng khí",
					"image_url": "/banners/campaign-everyday.jpg",
					"cta_label": "Mua ngay",
					"cta_href":  "/san-pham",
				},
			},
			map[string]any{
				"id":      "policies-main",
				"type":    "policies",
				"visible": true,
				"props": map[string]any{
					"title": "Cam kết",
					"items": []any{
						map[string]any{
							"heading": "Đổi trả 14 ngày",
							"detail":  "Miễn phí đổi size trên toàn quốc.",
						},
					},
				},
			},
		},
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	return string(b)
}

func seedActiveShop(t *testing.T, store *memStore, ownerID, name, shopStatus string) db.Shop {
	t.Helper()
	sh, err := store.CreateShop(context.Background(), db.CreateShopParams{
		ID:                 uuid.NewString(),
		OwnerUserID:        ownerID,
		Name:               name,
		Description:        "Mô tả công khai của " + name,
		PickupAddressID:    uuid.NewString(),
		ContactPhone:       "0901234567",
		BusinessLicenseDoc: "secret-license.pdf",
		Status:             shopStatus,
	})
	if err != nil {
		t.Fatalf("seed shop %s: %v", name, err)
	}
	return sh
}

func startSiteBufconn(t *testing.T) (userv1.UserServiceClient, *memStore, *auth.Manager, *auth.Manager) {
	t.Helper()
	srv, store, _, _, users := newTestHarness(t)
	svcMgr, err := auth.NewManager("site-test-service-key", auth.ServiceTokenTTL)
	if err != nil {
		t.Fatalf("new service auth manager: %v", err)
	}

	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer(grpc.ChainUnaryInterceptor(auth.GRPCUnaryServerInterceptor(users, svcMgr)))
	userv1.RegisterUserServiceServer(gs, srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return userv1.NewUserServiceClient(conn), store, users, svcMgr
}

func ctxWithBearer(t *testing.T, token string, extraKV ...string) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	kv := append([]string{}, extraKV...)
	if token != "" {
		kv = append(kv, "authorization", "Bearer "+token)
	}
	return metadata.NewOutgoingContext(ctx, metadata.Pairs(kv...))
}

func TestSites_LifecycleRBACValidationAndAudit(t *testing.T) {
	client, store, users, svcMgr := startSiteBufconn(t)

	sellerAUserID := uuid.NewString()
	sellerBUserID := uuid.NewString()
	pendingUserID := uuid.NewString()
	buyerUserID := uuid.NewString()
	adminUserID := uuid.NewString()

	shopA := seedActiveShop(t, store, sellerAUserID, "Xưởng Dệt Bạch Đằng", ShopStatusActive)
	shopB := seedActiveShop(t, store, sellerBUserID, "Kho Basic Sài Gòn", ShopStatusActive)
	shopPending := seedActiveShop(t, store, pendingUserID, "Shop Chờ Duyệt", ShopStatusPending)

	sellerATok, _, _, _ := users.SignAccessToken(sellerAUserID, RoleSeller, shopA.ID)
	sellerBTok, _, _, _ := users.SignAccessToken(sellerBUserID, RoleSeller, shopB.ID)
	pendingTok, _, _, _ := users.SignAccessToken(pendingUserID, RoleSeller, shopPending.ID)
	buyerTok, _, _, _ := users.SignAccessToken(buyerUserID, RoleBuyer, "")
	adminTok, _, _, _ := users.SignAccessToken(adminUserID, RoleAdmin, "")
	svcTok, _ := svcMgr.SignServiceToken("order-service")

	spoof := []string{"x-user-id", adminUserID, "x-user-role", "admin", "x-shop-id", shopA.ID}

	// 1. Anonymous, spoofed metadata, and service tokens MUST be rejected on all 7 protected site RPCs.
	v1Config := sampleSiteConfigJSON(t, "Bạch Đằng V1", "Hero V1")
	if _, err := client.GetSiteDraft(ctxWithBearer(t, "", spoof...), &userv1.GetSiteDraftRequest{ShopId: shopA.ID}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anon GetSiteDraft: want Unauthenticated, got %v", err)
	}
	if _, err := client.SaveSiteDraft(ctxWithBearer(t, "", spoof...), &userv1.SaveSiteDraftRequest{ShopId: shopA.ID, Subdomain: "bach-dang", ConfigJson: v1Config}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anon SaveSiteDraft: want Unauthenticated, got %v", err)
	}
	if _, err := client.PublishSite(ctxWithBearer(t, "", spoof...), &userv1.PublishSiteRequest{ShopId: shopA.ID}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anon PublishSite: want Unauthenticated, got %v", err)
	}
	if _, err := client.RollbackSite(ctxWithBearer(t, "", spoof...), &userv1.RollbackSiteRequest{ShopId: shopA.ID, VersionId: uuid.NewString()}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anon RollbackSite: want Unauthenticated, got %v", err)
	}
	if _, err := client.ListSiteVersions(ctxWithBearer(t, "", spoof...), &userv1.ListSiteVersionsRequest{ShopId: shopA.ID}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anon ListSiteVersions: want Unauthenticated, got %v", err)
	}
	if _, err := client.ListSites(ctxWithBearer(t, "", spoof...), &userv1.ListSitesRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anon ListSites: want Unauthenticated, got %v", err)
	}
	if _, err := client.SuspendSite(ctxWithBearer(t, "", spoof...), &userv1.SuspendSiteRequest{SiteId: uuid.NewString(), Suspend: true}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anon SuspendSite: want Unauthenticated, got %v", err)
	}
	if _, err := client.SaveSiteDraft(ctxWithBearer(t, svcTok), &userv1.SaveSiteDraftRequest{ShopId: shopA.ID, Subdomain: "bach-dang", ConfigJson: v1Config}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service token SaveSiteDraft: want PermissionDenied, got %v", err)
	}
	if _, err := client.ListSites(ctxWithBearer(t, svcTok), &userv1.ListSitesRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service token ListSites: want PermissionDenied, got %v", err)
	}
	// Buyer is denied seller/admin site RPCs.
	if _, err := client.GetSiteDraft(ctxWithBearer(t, buyerTok), &userv1.GetSiteDraftRequest{ShopId: shopA.ID}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("buyer GetSiteDraft: want PermissionDenied, got %v", err)
	}
	if _, err := client.ListSites(ctxWithBearer(t, sellerATok), &userv1.ListSitesRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("seller ListSites: want PermissionDenied, got %v", err)
	}
	if _, err := client.SuspendSite(ctxWithBearer(t, sellerATok), &userv1.SuspendSiteRequest{SiteId: uuid.NewString(), Suspend: true}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("seller SuspendSite: want PermissionDenied, got %v", err)
	}

	// 2. Validation failures via gRPC SaveSiteDraft.
	badSubdomains := []string{"ab", "-bad", "bad-", "www", "admin", "Vietcombank", "vietcombank-uudai", "nexusmart-sale"}
	for _, bad := range badSubdomains {
		if _, err := client.SaveSiteDraft(ctxWithBearer(t, sellerATok), &userv1.SaveSiteDraftRequest{
			Subdomain:  bad,
			ConfigJson: v1Config,
		}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("subdomain %q: want InvalidArgument, got %v", bad, err)
		}
	}

	// Low WCAG contrast rejected.
	lowContrast := strings.Replace(v1Config, `"#182026"`, `"#DDDDDD"`, 1)
	if _, err := client.SaveSiteDraft(ctxWithBearer(t, sellerATok), &userv1.SaveSiteDraftRequest{
		Subdomain:  "bach-dang",
		ConfigJson: lowContrast,
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("low contrast: want InvalidArgument, got %v", err)
	}

	// Unknown JSON field rejected.
	unknownField := strings.Replace(v1Config, `"tagline":`, `"custom_html":"<b>x</b>","tagline":`, 1)
	if _, err := client.SaveSiteDraft(ctxWithBearer(t, sellerATok), &userv1.SaveSiteDraftRequest{
		Subdomain:  "bach-dang",
		ConfigJson: unknownField,
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("unknown field: want InvalidArgument, got %v", err)
	}

	// javascript: URL rejected.
	jsURL := strings.Replace(v1Config, `"/san-pham"`, `"javascript:alert(1)"`, 1)
	if _, err := client.SaveSiteDraft(ctxWithBearer(t, sellerATok), &userv1.SaveSiteDraftRequest{
		Subdomain:  "bach-dang",
		ConfigJson: jsURL,
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("javascript url: want InvalidArgument, got %v", err)
	}

	// data: URL rejected.
	dataURL := strings.Replace(v1Config, `"/banners/vietnam-workshop.jpg"`, `"data:image/svg+xml,<svg/>"`, 1)
	if _, err := client.SaveSiteDraft(ctxWithBearer(t, sellerATok), &userv1.SaveSiteDraftRequest{
		Subdomain:  "bach-dang",
		ConfigJson: dataURL,
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("data url: want InvalidArgument, got %v", err)
	}

	// >20 sections rejected.
	secs := make([]map[string]any, 21)
	for i := range secs {
		secs[i] = map[string]any{"id": "t", "type": "text", "visible": true, "props": map[string]any{"title": "T", "body": "B"}}
	}
	var parsed map[string]any
	_ = json.Unmarshal([]byte(v1Config), &parsed)
	parsed["sections"] = secs
	tooManyBytes, _ := json.Marshal(parsed)
	if _, err := client.SaveSiteDraft(ctxWithBearer(t, sellerATok), &userv1.SaveSiteDraftRequest{
		Subdomain:  "bach-dang",
		ConfigJson: string(tooManyBytes),
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf(">20 sections: want InvalidArgument, got %v", err)
	}

	// 3. Seller A saves draft (with mixed case subdomain -> normalized to lowercase).
	saveResp, err := client.SaveSiteDraft(ctxWithBearer(t, sellerATok), &userv1.SaveSiteDraftRequest{
		Subdomain:  "  Bach-Dang ",
		ConfigJson: v1Config,
	})
	if err != nil {
		t.Fatalf("SaveSiteDraft v1: %v", err)
	}
	siteA := saveResp.GetSite()
	if siteA.GetSubdomain() != "bach-dang" || siteA.GetPrimaryHost() != "bach-dang.nexusmart.localhost" {
		t.Fatalf("unexpected subdomain/host: %q / %q", siteA.GetSubdomain(), siteA.GetPrimaryHost())
	}
	if siteA.GetStatus() != userv1.SiteStatus_SITE_STATUS_DRAFT {
		t.Fatalf("expected DRAFT status, got %v", siteA.GetStatus())
	}

	// Draft MUST NOT be returned publicly by GetSiteByHost or GetSiteByShop.
	if _, err := client.GetSiteByHost(context.Background(), &userv1.GetSiteByHostRequest{Host: "bach-dang.nexusmart.localhost"}); status.Code(err) != codes.NotFound {
		t.Fatalf("draft GetSiteByHost: want NotFound, got %v", err)
	}
	if _, err := client.GetSiteByShop(context.Background(), &userv1.GetSiteByShopRequest{ShopId: shopA.ID}); status.Code(err) != codes.NotFound {
		t.Fatalf("draft GetSiteByShop: want NotFound, got %v", err)
	}

	// 4. Case-insensitive duplicate subdomain rejected with AlreadyExists.
	if _, err := client.SaveSiteDraft(ctxWithBearer(t, sellerBTok), &userv1.SaveSiteDraftRequest{
		Subdomain:  "BACH-DANG",
		ConfigJson: sampleSiteConfigJSON(t, "Shop B", "Hero B"),
	}); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("duplicate subdomain: want AlreadyExists, got %v", err)
	}

	// 5. Cross-shop RBAC: Seller B MUST be rejected on Seller A's shop_id.
	if _, err := client.GetSiteDraft(ctxWithBearer(t, sellerBTok), &userv1.GetSiteDraftRequest{ShopId: shopA.ID}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("seller B GetSiteDraft(shopA): want PermissionDenied, got %v", err)
	}
	if _, err := client.SaveSiteDraft(ctxWithBearer(t, sellerBTok), &userv1.SaveSiteDraftRequest{ShopId: shopA.ID, Subdomain: "hijack-sub", ConfigJson: v1Config}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("seller B SaveSiteDraft(shopA): want PermissionDenied, got %v", err)
	}
	if _, err := client.PublishSite(ctxWithBearer(t, sellerBTok), &userv1.PublishSiteRequest{ShopId: shopA.ID}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("seller B PublishSite(shopA): want PermissionDenied, got %v", err)
	}
	if _, err := client.ListSiteVersions(ctxWithBearer(t, sellerBTok), &userv1.ListSiteVersionsRequest{ShopId: shopA.ID}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("seller B ListSiteVersions(shopA): want PermissionDenied, got %v", err)
	}

	// 6. Pending (non-ACTIVE) shop cannot publish.
	if _, err := client.SaveSiteDraft(ctxWithBearer(t, pendingTok), &userv1.SaveSiteDraftRequest{
		Subdomain:  "pending-shop",
		ConfigJson: sampleSiteConfigJSON(t, "Pending Brand", "Pending Hero"),
	}); err != nil {
		t.Fatalf("pending shop SaveSiteDraft: %v", err)
	}
	if _, err := client.PublishSite(ctxWithBearer(t, pendingTok), &userv1.PublishSiteRequest{Note: "try"}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("pending shop PublishSite: want FailedPrecondition, got %v", err)
	}

	// 7. Seller A publishes v1.
	pub1, err := client.PublishSite(ctxWithBearer(t, sellerATok), &userv1.PublishSiteRequest{Note: "Initial launch"})
	if err != nil {
		t.Fatalf("PublishSite v1: %v", err)
	}
	if pub1.GetSite().GetStatus() != userv1.SiteStatus_SITE_STATUS_PUBLISHED || pub1.GetVersion().GetVersionNumber() != 1 {
		t.Fatalf("unexpected pub1: site=%+v ver=%+v", pub1.GetSite(), pub1.GetVersion())
	}

	// Public GetSiteByHost (with uppercase & :port) and GetSiteByShop return v1 and public shop fields only.
	byHost1, err := client.GetSiteByHost(context.Background(), &userv1.GetSiteByHostRequest{Host: "BACH-DANG.NEXUSMART.LOCALHOST:3300"})
	if err != nil {
		t.Fatalf("GetSiteByHost v1: %v", err)
	}
	if !strings.Contains(byHost1.GetSite().GetConfigJson(), "Hero V1") ||
		byHost1.GetShop().GetId() != shopA.ID ||
		byHost1.GetShop().GetName() != shopA.Name ||
		byHost1.GetShop().GetStatus() != userv1.ShopStatus_SHOP_STATUS_ACTIVE {
		t.Fatalf("unexpected GetSiteByHost v1 response: %+v", byHost1)
	}
	byShop1, err := client.GetSiteByShop(context.Background(), &userv1.GetSiteByShopRequest{ShopId: shopA.ID})
	if err != nil || byShop1.GetSite().GetPrimaryHost() != "bach-dang.nexusmart.localhost" {
		t.Fatalf("GetSiteByShop v1: resp=%+v err=%v", byShop1, err)
	}

	// 8. Save draft v2 MUST NOT affect live GetSiteByHost or status/published_version_id!
	v2Config := sampleSiteConfigJSON(t, "Bạch Đằng V2", "Draft Hero V2")
	save2, err := client.SaveSiteDraft(ctxWithBearer(t, sellerATok), &userv1.SaveSiteDraftRequest{
		Subdomain:  "bach-dang",
		ConfigJson: v2Config,
	})
	if err != nil {
		t.Fatalf("SaveSiteDraft v2: %v", err)
	}
	if save2.GetSite().GetStatus() != userv1.SiteStatus_SITE_STATUS_PUBLISHED ||
		save2.GetSite().GetPublishedVersionId() != pub1.GetVersion().GetId() {
		t.Fatalf("SaveSiteDraft mutated status or published_version_id: %+v", save2.GetSite())
	}
	draftResp, err := client.GetSiteDraft(ctxWithBearer(t, sellerATok), &userv1.GetSiteDraftRequest{})
	if err != nil || !strings.Contains(draftResp.GetSite().GetConfigJson(), "Draft Hero V2") {
		t.Fatalf("GetSiteDraft should return v2 draft, got %+v err=%v", draftResp, err)
	}
	byHostStillV1, err := client.GetSiteByHost(context.Background(), &userv1.GetSiteByHostRequest{Host: "bach-dang.nexusmart.localhost"})
	if err != nil || !strings.Contains(byHostStillV1.GetSite().GetConfigJson(), "Hero V1") || strings.Contains(byHostStillV1.GetSite().GetConfigJson(), "Draft Hero V2") {
		t.Fatalf("GetSiteByHost leaked draft_config! got: %s", byHostStillV1.GetSite().GetConfigJson())
	}

	// 9. Publish v2 -> version_number=2, live returns v2.
	pub2, err := client.PublishSite(ctxWithBearer(t, sellerATok), &userv1.PublishSiteRequest{Note: "Autumn update"})
	if err != nil || pub2.GetVersion().GetVersionNumber() != 2 {
		t.Fatalf("PublishSite v2: resp=%+v err=%v", pub2, err)
	}
	byHost2, err := client.GetSiteByHost(context.Background(), &userv1.GetSiteByHostRequest{Host: "bach-dang.nexusmart.localhost"})
	if err != nil || !strings.Contains(byHost2.GetSite().GetConfigJson(), "Draft Hero V2") {
		t.Fatalf("GetSiteByHost after v2 publish: got %s err=%v", byHost2.GetSite().GetConfigJson(), err)
	}

	versResp, err := client.ListSiteVersions(ctxWithBearer(t, sellerATok), &userv1.ListSiteVersionsRequest{Page: 1, PageSize: 10})
	if err != nil || versResp.GetTotal() != 2 || len(versResp.GetVersions()) != 2 || versResp.GetVersions()[0].GetVersionNumber() != 2 {
		t.Fatalf("ListSiteVersions: got %+v err=%v", versResp, err)
	}

	// 10. Cross-site version rollback is rejected.
	if _, err := client.SaveSiteDraft(ctxWithBearer(t, sellerBTok), &userv1.SaveSiteDraftRequest{
		Subdomain:  "sai-gon",
		ConfigJson: sampleSiteConfigJSON(t, "Sài Gòn V1", "SG Hero"),
	}); err != nil {
		t.Fatalf("seller B SaveSiteDraft: %v", err)
	}
	if _, err := client.RollbackSite(ctxWithBearer(t, sellerBTok), &userv1.RollbackSiteRequest{
		VersionId: pub1.GetVersion().GetId(),
	}); status.Code(err) != codes.NotFound {
		t.Fatalf("cross-site rollback: want NotFound, got %v", err)
	}

	// 11. Seller A rolls back to v1 -> published_version_id=v1, draft_config=v1, live returns v1.
	rbResp, err := client.RollbackSite(ctxWithBearer(t, sellerATok), &userv1.RollbackSiteRequest{
		VersionId: pub1.GetVersion().GetId(),
		Note:      "Revert to v1",
	})
	if err != nil {
		t.Fatalf("RollbackSite v1: %v", err)
	}
	if rbResp.GetSite().GetPublishedVersionId() != pub1.GetVersion().GetId() ||
		rbResp.GetVersion().GetVersionNumber() != 1 ||
		!strings.Contains(rbResp.GetSite().GetConfigJson(), "Hero V1") {
		t.Fatalf("unexpected RollbackSite response: %+v", rbResp)
	}
	byHostRB, err := client.GetSiteByHost(context.Background(), &userv1.GetSiteByHostRequest{Host: "bach-dang.nexusmart.localhost"})
	if err != nil || !strings.Contains(byHostRB.GetSite().GetConfigJson(), "Hero V1") {
		t.Fatalf("GetSiteByHost after rollback: got %s err=%v", byHostRB.GetSite().GetConfigJson(), err)
	}

	// 12. Admin ListSites & SuspendSite / Unsuspend.
	listAll, err := client.ListSites(ctxWithBearer(t, adminTok), &userv1.ListSitesRequest{Page: 1, PageSize: 10})
	if err != nil || listAll.GetTotal() != 3 {
		t.Fatalf("admin ListSites all: want total=3, got %+v err=%v", listAll, err)
	}
	listPub, err := client.ListSites(ctxWithBearer(t, adminTok), &userv1.ListSitesRequest{Status: userv1.SiteStatus_SITE_STATUS_PUBLISHED, Query: "bach"})
	if err != nil || listPub.GetTotal() != 1 || listPub.GetSites()[0].GetSubdomain() != "bach-dang" {
		t.Fatalf("admin ListSites filtered: got %+v err=%v", listPub, err)
	}

	// Suspend site A -> public returns NotFound; Publish & Rollback return FailedPrecondition.
	suspResp, err := client.SuspendSite(ctxWithBearer(t, adminTok), &userv1.SuspendSiteRequest{
		SiteId:  siteA.GetId(),
		Suspend: true,
		Reason:  "Vi phạm chính sách thương hiệu",
	})
	if err != nil || suspResp.GetSite().GetStatus() != userv1.SiteStatus_SITE_STATUS_SUSPENDED || suspResp.GetSite().GetSuspendedReason() == "" {
		t.Fatalf("SuspendSite true: got %+v err=%v", suspResp, err)
	}
	if _, err := client.GetSiteByHost(context.Background(), &userv1.GetSiteByHostRequest{Host: "bach-dang.nexusmart.localhost"}); status.Code(err) != codes.NotFound {
		t.Fatalf("suspended GetSiteByHost: want NotFound, got %v", err)
	}
	if _, err := client.GetSiteByShop(context.Background(), &userv1.GetSiteByShopRequest{ShopId: shopA.ID}); status.Code(err) != codes.NotFound {
		t.Fatalf("suspended GetSiteByShop: want NotFound, got %v", err)
	}
	if _, err := client.PublishSite(ctxWithBearer(t, sellerATok), &userv1.PublishSiteRequest{}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("suspended PublishSite: want FailedPrecondition, got %v", err)
	}
	if _, err := client.RollbackSite(ctxWithBearer(t, sellerATok), &userv1.RollbackSiteRequest{VersionId: pub1.GetVersion().GetId()}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("suspended RollbackSite: want FailedPrecondition, got %v", err)
	}

	// Unsuspend published site A -> restores PUBLISHED.
	unsuspResp, err := client.SuspendSite(ctxWithBearer(t, adminTok), &userv1.SuspendSiteRequest{
		SiteId:  siteA.GetId(),
		Suspend: false,
		Reason:  "Đã khắc phục",
	})
	if err != nil || unsuspResp.GetSite().GetStatus() != userv1.SiteStatus_SITE_STATUS_PUBLISHED || unsuspResp.GetSite().GetSuspendedReason() != "" {
		t.Fatalf("SuspendSite false (published): got %+v err=%v", unsuspResp, err)
	}
	if _, err := client.GetSiteByHost(context.Background(), &userv1.GetSiteByHostRequest{Host: "bach-dang.nexusmart.localhost"}); err != nil {
		t.Fatalf("unsuspended GetSiteByHost: %v", err)
	}

	// Suspend and unsuspend DRAFT site B -> restores DRAFT (never PUBLISHED).
	siteBDraft, err := client.GetSiteDraft(ctxWithBearer(t, sellerBTok), &userv1.GetSiteDraftRequest{})
	if err != nil {
		t.Fatalf("get site B draft: %v", err)
	}
	if _, err := client.SuspendSite(ctxWithBearer(t, adminTok), &userv1.SuspendSiteRequest{
		SiteId:  siteBDraft.GetSite().GetId(),
		Suspend: true,
		Reason:  "Kiểm tra",
	}); err != nil {
		t.Fatalf("suspend draft site B: %v", err)
	}
	unsuspB, err := client.SuspendSite(ctxWithBearer(t, adminTok), &userv1.SuspendSiteRequest{
		SiteId:  siteBDraft.GetSite().GetId(),
		Suspend: false,
	})
	if err != nil || unsuspB.GetSite().GetStatus() != userv1.SiteStatus_SITE_STATUS_DRAFT {
		t.Fatalf("unsuspend draft site B: want DRAFT, got %+v err=%v", unsuspB, err)
	}

	// Suspending the underlying shop immediately hides the published site from public lookups.
	if _, err := store.UpdateShopReview(context.Background(), db.UpdateShopReviewParams{
		ID:              shopA.ID,
		Status:          ShopStatusSuspended,
		RejectionReason: "shop suspended",
	}); err != nil {
		t.Fatalf("suspend shopA: %v", err)
	}
	if _, err := client.GetSiteByHost(context.Background(), &userv1.GetSiteByHostRequest{Host: "bach-dang.nexusmart.localhost"}); status.Code(err) != codes.NotFound {
		t.Fatalf("inactive shop GetSiteByHost: want NotFound, got %v", err)
	}
	if _, err := client.GetSiteByShop(context.Background(), &userv1.GetSiteByShopRequest{ShopId: shopA.ID}); status.Code(err) != codes.NotFound {
		t.Fatalf("inactive shop GetSiteByShop: want NotFound, got %v", err)
	}

	// 13. Audit logs recorded for save_draft, publish, rollback, suspend, unsuspend.
	auditResp, err := client.ListAuditLogs(ctxWithBearer(t, adminTok), &userv1.ListAuditLogsRequest{
		TargetType: "site",
		TargetId:   siteA.GetId(),
		Page:       1,
		PageSize:   50,
	})
	if err != nil {
		t.Fatalf("ListAuditLogs: %v", err)
	}
	seenActions := make(map[string]int)
	for _, entry := range auditResp.GetLogs() {
		seenActions[entry.GetAction()]++
	}
	for _, want := range []string{"site.save_draft", "site.publish", "site.rollback", "admin.site.suspend", "admin.site.unsuspend"} {
		if seenActions[want] == 0 {
			t.Errorf("expected audit log action %q for siteA, got %+v", want, seenActions)
		}
	}
}
