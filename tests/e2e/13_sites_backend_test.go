package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func validSiteConfigPayload(brandName, heroTitle, accentHex string) map[string]any {
	return map[string]any{
		"theme": map[string]any{
			"bg_color":          "#F8F6F1",
			"surface_color":     "#FFFFFF",
			"text_color":        "#182026",
			"accent_color":      accentHex,
			"accent_text_color": "#FFFFFF",
			"font_family":       "work-sans",
			"logo_url":          "/banners/vietnam-workshop.jpg",
			"banner_url":        "https://sandbox.nexusmart.local/banners/site-hero.jpg",
			"brand_name":        brandName,
			"tagline":           "Cửa hàng chính hãng vận hành trên NexusMart",
		},
		"sections": []map[string]any{
			{
				"id":      "hero-1",
				"type":    "hero",
				"visible": true,
				"props": map[string]any{
					"eyebrow":   "BỘ SƯU TẬP MỚI",
					"title":     heroTitle,
					"subtitle":  "Vải cotton định lượng 220gsm may trực tiếp tại xưởng",
					"image_url": "/banners/vietnam-workshop.jpg",
					"cta_label": "Xem sản phẩm",
					"cta_href":  "/san-pham",
				},
			},
			{
				"id":      "text-1",
				"type":    "text",
				"visible": true,
				"props": map[string]any{
					"title": "Về xưởng",
					"body":  "Mọi sản phẩm đều được kiểm tra chất lượng trước khi xuất kho.",
					"align": "left",
				},
			},
		},
	}
}

func TestE2E_SitesBackend_ValidationLifecycleRBACAndAudit(t *testing.T) {
	env := getEnv()
	adminTok := env.adminLogin(t)
	_, seller1, seller2 := env.ensureBaseMarketplace(t)

	// Reset gateway rate-limit keys cleanly after this test so subsequent suites start with fresh quotas.
	t.Cleanup(func() {
		if env.cfg.RedisAddr == "" {
			return
		}
		rCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = ResetRateLimitKeys(rCtx, env.cfg.RedisAddr)
	})

	subA := fmt.Sprintf("e2e-site-%s-a", env.runID)
	hostA := subA + "." + env.cfg.SiteRootDomain

	const (
		v1Title = "Phien ban v1 Storefront E2E"
		v2Title = "Phien ban v2 Cap nhat E2E"
	)

	// 1. Subdomain validation: invalid syntax, reserved, phishing/brand-impersonation -> 400.
	t.Run("SubdomainValidation_RejectsInvalidReservedAndPhishing", func(t *testing.T) {
		baseCfg := validSiteConfigPayload("Shop E2E", v1Title, "#1D4ED8")
		badSubs := []struct {
			name string
			sub  string
		}{
			{"too short", "ab"},
			{"leading hyphen", "-abc"},
			{"trailing hyphen", "abc-"},
			{"underscore", "bad_sub"},
			{"dot multi-level", "a.b"},
			{"reserved www", "www"},
			{"reserved admin", "admin"},
			{"reserved api", "api"},
			{"reserved seller", "seller"},
			{"reserved nexusmart", "nexusmart"},
			{"phishing bank exact", "vietcombank"},
			{"phishing bank prefix", "vietcombank-uudai"},
			{"phishing brand prefix", "nexusmart-official"},
			{"punycode prefix", "xn--phish"},
		}
		for _, tc := range badSubs {
			env.mustDoJSON(t, http.MethodPut, "/api/v1/seller/site/draft", seller1.AccessToken, nil, map[string]any{
				"subdomain": tc.sub,
				"config":    baseCfg,
			}, http.StatusBadRequest)
		}
	})

	// 2. Strict JSON config validation: unknown fields, unsafe URLs, low contrast, bad font, >20 sections -> 400.
	t.Run("StrictConfigValidation_RejectsUnknownFieldsUnsafeURLsLowContrastAndTooManySections", func(t *testing.T) {
		rejectCases := []struct {
			name string
			cfg  any
		}{
			{
				name: "unknown root field",
				cfg: map[string]any{
					"theme":        validSiteConfigPayload("Shop", v1Title, "#1D4ED8")["theme"],
					"sections":     []any{},
					"unknown_root": true,
				},
			},
			{
				name: "unknown theme field",
				cfg: map[string]any{
					"theme": map[string]any{
						"bg_color":          "#F8F6F1",
						"surface_color":     "#FFFFFF",
						"text_color":        "#182026",
						"accent_color":      "#1D4ED8",
						"accent_text_color": "#FFFFFF",
						"font_family":       "work-sans",
						"brand_name":        "Shop",
						"custom_css":        "body{display:none}",
					},
					"sections": []any{},
				},
			},
			{
				name: "unknown section field",
				cfg: map[string]any{
					"theme": validSiteConfigPayload("Shop", v1Title, "#1D4ED8")["theme"],
					"sections": []map[string]any{
						{
							"id":       "sec-bad",
							"type":     "text",
							"visible":  true,
							"raw_html": "<script>alert(1)</script>",
							"props": map[string]any{
								"title": "Hi",
								"body":  "Body",
							},
						},
					},
				},
			},
			{
				name: "unknown section props field",
				cfg: map[string]any{
					"theme": validSiteConfigPayload("Shop", v1Title, "#1D4ED8")["theme"],
					"sections": []map[string]any{
						{
							"id":      "sec-bad-prop",
							"type":    "text",
							"visible": true,
							"props": map[string]any{
								"title":   "Hi",
								"body":    "Body",
								"onclick": "alert(1)",
							},
						},
					},
				},
			},
			{
				name: "javascript URL in theme.logo_url",
				cfg: map[string]any{
					"theme": map[string]any{
						"bg_color":          "#F8F6F1",
						"surface_color":     "#FFFFFF",
						"text_color":        "#182026",
						"accent_color":      "#1D4ED8",
						"accent_text_color": "#FFFFFF",
						"font_family":       "work-sans",
						"logo_url":          "javascript:alert(1)",
						"brand_name":        "Shop",
					},
					"sections": []any{},
				},
			},
			{
				name: "data URL in theme.banner_url",
				cfg: map[string]any{
					"theme": map[string]any{
						"bg_color":          "#F8F6F1",
						"surface_color":     "#FFFFFF",
						"text_color":        "#182026",
						"accent_color":      "#1D4ED8",
						"accent_text_color": "#FFFFFF",
						"font_family":       "work-sans",
						"banner_url":        "data:text/html,<script>alert(1)</script>",
						"brand_name":        "Shop",
					},
					"sections": []any{},
				},
			},
			{
				name: "javascript URL in hero cta_href",
				cfg: map[string]any{
					"theme": validSiteConfigPayload("Shop", v1Title, "#1D4ED8")["theme"],
					"sections": []map[string]any{
						{
							"id":      "hero-js",
							"type":    "hero",
							"visible": true,
							"props": map[string]any{
								"title":     "Unsafe Hero",
								"cta_label": "Click",
								"cta_href":  "javascript:void(0)",
							},
						},
					},
				},
			},
			{
				name: "insecure http URL in hero image_url",
				cfg: map[string]any{
					"theme": validSiteConfigPayload("Shop", v1Title, "#1D4ED8")["theme"],
					"sections": []map[string]any{
						{
							"id":      "hero-http",
							"type":    "hero",
							"visible": true,
							"props": map[string]any{
								"title":     "Insecure Hero",
								"image_url": "http://insecure.example.com/a.png",
							},
						},
					},
				},
			},
			{
				name: "low contrast bg/text (< 4.5)",
				cfg: map[string]any{
					"theme": map[string]any{
						"bg_color":          "#FFFFFF",
						"surface_color":     "#FFFFFF",
						"text_color":        "#CCCCCC",
						"accent_color":      "#1D4ED8",
						"accent_text_color": "#FFFFFF",
						"font_family":       "work-sans",
						"brand_name":        "Low Contrast",
					},
					"sections": []any{},
				},
			},
			{
				name: "low contrast accent/accent_text (< 4.5)",
				cfg: map[string]any{
					"theme": map[string]any{
						"bg_color":          "#FFFFFF",
						"surface_color":     "#F8F6F1",
						"text_color":        "#182026",
						"accent_color":      "#DDDDDD",
						"accent_text_color": "#FFFFFF",
						"font_family":       "work-sans",
						"brand_name":        "Low Accent Contrast",
					},
					"sections": []any{},
				},
			},
			{
				name: "disallowed font family",
				cfg: map[string]any{
					"theme": map[string]any{
						"bg_color":          "#FFFFFF",
						"surface_color":     "#F8F6F1",
						"text_color":        "#182026",
						"accent_color":      "#1D4ED8",
						"accent_text_color": "#FFFFFF",
						"font_family":       "comic-sans",
						"brand_name":        "Bad Font",
					},
					"sections": []any{},
				},
			},
			{
				name: "more than 20 sections",
				cfg: func() map[string]any {
					secs := make([]map[string]any, 21)
					for i := 0; i < 21; i++ {
						secs[i] = map[string]any{
							"id":      fmt.Sprintf("sec-%d", i+1),
							"type":    "text",
							"visible": true,
							"props": map[string]any{
								"title": fmt.Sprintf("Block %d", i+1),
								"body":  "Content block",
							},
						}
					}
					return map[string]any{
						"theme":    validSiteConfigPayload("Too Many", v1Title, "#1D4ED8")["theme"],
						"sections": secs,
					}
				}(),
			},
		}

		for _, tc := range rejectCases {
			resp := env.mustDoJSON(t, http.MethodPut, "/api/v1/seller/site/draft", seller1.AccessToken, nil, map[string]any{
				"subdomain": subA,
				"config":    tc.cfg,
			}, http.StatusBadRequest)
			t.Logf("Validation case [%s] rejected cleanly: HTTP %d", tc.name, resp.StatusCode)
		}
	})

	// 3. Draft -> duplicate case-insensitive 409 -> Publish v1 -> Draft v2 (no public leak) -> Publish v2 -> Rollback v1.
	var (
		siteA SiteDTO
		v1ID  string
		v2ID  string
	)
	t.Run("DraftPublishV2Rollback_And_DuplicateCaseInsensitive", func(t *testing.T) {
		// Save valid draft v1 on Seller 1.
		saveResp := env.mustDoJSON(t, http.MethodPut, "/api/v1/seller/site/draft", seller1.AccessToken, nil, map[string]any{
			"subdomain": subA,
			"config":    validSiteConfigPayload(seller1.ShopName, v1Title, "#1D4ED8"),
		}, http.StatusOK)
		var saveOut SellerSiteResponse
		if err := DecodeJSON(saveResp, &saveOut); err != nil {
			t.Fatalf("decode save draft v1: %v", err)
		}
		siteA = saveOut.Site
		if siteA.ID == "" || siteA.ShopID != seller1.ShopID {
			t.Fatalf("unexpected saved site: %+v", siteA)
		}
		if siteA.Status != "DRAFT" {
			t.Fatalf("expected newly saved site status DRAFT, got %q", siteA.Status)
		}
		if siteA.PrimaryHost != hostA {
			t.Fatalf("expected primary_host=%q, got %q", hostA, siteA.PrimaryHost)
		}

		// Duplicate subdomain (both uppercase and lowercase) on Seller 2 -> 409 Conflict.
		env.mustDoJSON(t, http.MethodPut, "/api/v1/seller/site/draft", seller2.AccessToken, nil, map[string]any{
			"subdomain": strings.ToUpper(subA),
			"config":    validSiteConfigPayload(seller2.ShopName, "Dup Upper", "#0F766E"),
		}, http.StatusConflict)
		env.mustDoJSON(t, http.MethodPut, "/api/v1/seller/site/draft", seller2.AccessToken, nil, map[string]any{
			"subdomain": subA,
			"config":    validSiteConfigPayload(seller2.ShopName, "Dup Lower", "#0F766E"),
		}, http.StatusConflict)

		// Draft MUST NOT be returned by public by-host or by-shop endpoints -> 404.
		env.mustDoJSON(t, http.MethodGet, "/api/v1/sites/by-host?host="+url.QueryEscape(hostA), "", nil, nil, http.StatusNotFound)
		env.mustDoJSON(t, http.MethodGet, "/api/v1/sites/by-shop/"+url.PathEscape(seller1.ShopID), "", nil, nil, http.StatusNotFound)

		// Publish v1 -> 200.
		pub1Resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/site/publish", seller1.AccessToken, nil, map[string]any{
			"note": "E2E publish v1",
		}, http.StatusOK)
		var pub1Out SitePublishResponse
		if err := DecodeJSON(pub1Resp, &pub1Out); err != nil {
			t.Fatalf("decode publish v1: %v", err)
		}
		if pub1Out.Site.Status != "PUBLISHED" || pub1Out.Version.VersionNumber != 1 || pub1Out.Version.ID == "" {
			t.Fatalf("unexpected publish v1 response: %+v", pub1Out)
		}
		v1ID = pub1Out.Version.ID

		// Public by-host (including uppercase + :port normalization) and by-shop now return v1.
		byHost1 := env.mustDoJSON(t, http.MethodGet, "/api/v1/sites/by-host?host="+url.QueryEscape(strings.ToUpper(hostA)+":3300"), "", nil, nil, http.StatusOK)
		var pubSite1 PublicSiteResponse
		if err := DecodeJSON(byHost1, &pubSite1); err != nil {
			t.Fatalf("decode by-host v1: %v", err)
		}
		if pubSite1.Site.ID != siteA.ID || pubSite1.Shop.ID != seller1.ShopID || pubSite1.Site.Status != "PUBLISHED" {
			t.Fatalf("unexpected public site v1: %+v", pubSite1)
		}
		if !strings.Contains(string(pubSite1.Site.Config), v1Title) {
			t.Fatalf("expected public site v1 config to contain %q, got %s", v1Title, string(pubSite1.Site.Config))
		}

		byShop1 := env.mustDoJSON(t, http.MethodGet, "/api/v1/sites/by-shop/"+url.PathEscape(seller1.ShopID), "", nil, nil, http.StatusOK)
		var pubShop1 PublicSiteResponse
		if err := DecodeJSON(byShop1, &pubShop1); err != nil {
			t.Fatalf("decode by-shop v1: %v", err)
		}
		if pubShop1.Site.ID != siteA.ID || !strings.Contains(string(pubShop1.Site.Config), v1Title) {
			t.Fatalf("unexpected by-shop v1 response: %+v", pubShop1)
		}

		// Save draft v2 WITHOUT publishing -> seller draft sees v2, public by-host STILL returns v1!
		v2Cfg := validSiteConfigPayload(seller1.ShopName, v2Title, "#0F766E")
		env.mustDoJSON(t, http.MethodPut, "/api/v1/seller/site/draft", seller1.AccessToken, nil, map[string]any{
			"subdomain": subA,
			"config":    v2Cfg,
		}, http.StatusOK)

		draftAfterV2Resp := env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/site", seller1.AccessToken, nil, nil, http.StatusOK)
		var draftAfterV2 SellerSiteResponse
		if err := DecodeJSON(draftAfterV2Resp, &draftAfterV2); err != nil {
			t.Fatalf("decode seller draft v2: %v", err)
		}
		if !strings.Contains(string(draftAfterV2.Site.Config), v2Title) {
			t.Fatalf("expected seller draft to contain v2 title %q, got %s", v2Title, string(draftAfterV2.Site.Config))
		}

		byHostStillV1 := env.mustDoJSON(t, http.MethodGet, "/api/v1/sites/by-host?host="+url.QueryEscape(hostA), "", nil, nil, http.StatusOK)
		var pubStillV1 PublicSiteResponse
		if err := DecodeJSON(byHostStillV1, &pubStillV1); err != nil {
			t.Fatalf("decode by-host still v1: %v", err)
		}
		if !strings.Contains(string(pubStillV1.Site.Config), v1Title) || strings.Contains(string(pubStillV1.Site.Config), v2Title) {
			t.Fatalf("draft v2 leaked to public by-host before publish! config=%s", string(pubStillV1.Site.Config))
		}

		// Publish v2 -> public by-host now returns v2.
		pub2Resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/site/publish", seller1.AccessToken, nil, map[string]any{
			"note": "E2E publish v2",
		}, http.StatusOK)
		var pub2Out SitePublishResponse
		if err := DecodeJSON(pub2Resp, &pub2Out); err != nil {
			t.Fatalf("decode publish v2: %v", err)
		}
		if pub2Out.Version.VersionNumber != 2 || pub2Out.Version.ID == "" {
			t.Fatalf("expected version 2 on second publish, got %+v", pub2Out)
		}
		v2ID = pub2Out.Version.ID

		byHostV2 := env.mustDoJSON(t, http.MethodGet, "/api/v1/sites/by-host?host="+url.QueryEscape(hostA), "", nil, nil, http.StatusOK)
		var pubSite2 PublicSiteResponse
		if err := DecodeJSON(byHostV2, &pubSite2); err != nil {
			t.Fatalf("decode by-host v2: %v", err)
		}
		if !strings.Contains(string(pubSite2.Site.Config), v2Title) {
			t.Fatalf("expected public site to reflect v2 title %q, got %s", v2Title, string(pubSite2.Site.Config))
		}

		// List versions -> contains both v1 and v2.
		verResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/site/versions?page=1&page_size=20", seller1.AccessToken, nil, nil, http.StatusOK)
		var verOut SiteVersionsListResponse
		if err := DecodeJSON(verResp, &verOut); err != nil {
			t.Fatalf("decode list versions: %v", err)
		}
		if verOut.Total < 2 || len(verOut.Versions) < 2 {
			t.Fatalf("expected >= 2 site versions, got total=%d len=%d", verOut.Total, len(verOut.Versions))
		}
		foundV1, foundV2 := false, false
		for _, v := range verOut.Versions {
			if v.ID == v1ID {
				foundV1 = true
			}
			if v.ID == v2ID {
				foundV2 = true
			}
		}
		if !foundV1 || !foundV2 {
			t.Fatalf("versions list missing expected ids v1=%v v2=%v: %+v", foundV1, foundV2, verOut.Versions)
		}

		// Rollback to v1 -> public by-host returns v1 again!
		rbResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/site/rollback", seller1.AccessToken, nil, map[string]any{
			"version_id": v1ID,
			"note":       "E2E rollback to v1",
		}, http.StatusOK)
		var rbOut SitePublishResponse
		if err := DecodeJSON(rbResp, &rbOut); err != nil {
			t.Fatalf("decode rollback to v1: %v", err)
		}
		if rbOut.Site.PublishedVersionID != v1ID {
			t.Fatalf("expected published_version_id=%s after rollback, got %s", v1ID, rbOut.Site.PublishedVersionID)
		}

		byHostAfterRB := env.mustDoJSON(t, http.MethodGet, "/api/v1/sites/by-host?host="+url.QueryEscape(hostA), "", nil, nil, http.StatusOK)
		var pubSiteRB PublicSiteResponse
		if err := DecodeJSON(byHostAfterRB, &pubSiteRB); err != nil {
			t.Fatalf("decode by-host after rollback: %v", err)
		}
		if !strings.Contains(string(pubSiteRB.Site.Config), v1Title) || strings.Contains(string(pubSiteRB.Site.Config), v2Title) {
			t.Fatalf("expected rollback to restore v1 title %q, got %s", v1Title, string(pubSiteRB.Site.Config))
		}
	})

	// 4. Cross-tenant & RBAC isolation: Seller B cannot read/edit/publish/rollback Seller A's site (REST + direct gRPC).
	t.Run("CrossTenantIsolation_SellerBCannotAccessSellerA", func(t *testing.T) {
		// REST checks: Seller 2 has no site -> 404, cannot rollback to Seller 1's version -> 404, cannot call admin endpoints -> 403.
		env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/site", seller2.AccessToken, nil, nil, http.StatusNotFound)
		env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/site/rollback", seller2.AccessToken, nil, map[string]any{
			"version_id": v1ID,
			"note":       "cross-shop hijack attempt",
		}, http.StatusNotFound, http.StatusForbidden)
		env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/sites", seller2.AccessToken, nil, nil, http.StatusForbidden)
		env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/sites/"+url.PathEscape(seller1.ShopID), seller2.AccessToken, nil, nil, http.StatusForbidden)
		env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/sites/"+url.PathEscape(siteA.ID)+"/suspend", seller2.AccessToken, nil, map[string]any{
			"suspend": true,
			"reason":  "unauthorized",
		}, http.StatusForbidden)

		// Buyer cannot call seller site endpoints -> 403.
		buyerTok := MintAccessToken(env.cfg.JWTSecret, uuid.NewString(), "buyer")
		env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/site", buyerTok, nil, nil, http.StatusForbidden)
		env.mustDoJSON(t, http.MethodPut, "/api/v1/seller/site/draft", buyerTok, nil, map[string]any{
			"subdomain": "buyer-bad-sub",
			"config":    validSiteConfigPayload("Buyer", v1Title, "#1D4ED8"),
		}, http.StatusForbidden)

		// Direct gRPC UserService defense-in-depth checks: Seller 2 targeting Seller 1's shop_id MUST get PermissionDenied!
		conn, err := grpc.NewClient(env.cfg.UserGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Fatalf("dial UserService gRPC %s: %v", env.cfg.UserGRPCAddr, err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		userClient := userv1.NewUserServiceClient(conn)

		s2Ctx, cancel := context.WithTimeout(
			metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+seller2.AccessToken)),
			10*time.Second,
		)
		defer cancel()

		rawCfgBytes, _ := json.Marshal(validSiteConfigPayload("Hijack", "Hijack", "#1D4ED8"))
		if _, err := userClient.GetSiteDraft(s2Ctx, &userv1.GetSiteDraftRequest{ShopId: seller1.ShopID}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("gRPC GetSiteDraft cross-shop: want PermissionDenied, got %v", err)
		}
		if _, err := userClient.SaveSiteDraft(s2Ctx, &userv1.SaveSiteDraftRequest{
			ShopId:     seller1.ShopID,
			Subdomain:  subA,
			ConfigJson: string(rawCfgBytes),
		}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("gRPC SaveSiteDraft cross-shop: want PermissionDenied, got %v", err)
		}
		if _, err := userClient.PublishSite(s2Ctx, &userv1.PublishSiteRequest{ShopId: seller1.ShopID}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("gRPC PublishSite cross-shop: want PermissionDenied, got %v", err)
		}
		if _, err := userClient.RollbackSite(s2Ctx, &userv1.RollbackSiteRequest{
			ShopId:    seller1.ShopID,
			VersionId: v1ID,
		}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("gRPC RollbackSite cross-shop: want PermissionDenied, got %v", err)
		}
		if _, err := userClient.SuspendSite(s2Ctx, &userv1.SuspendSiteRequest{
			SiteId:  siteA.ID,
			Suspend: true,
			Reason:  "seller hijack",
		}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("gRPC SuspendSite as seller: want PermissionDenied, got %v", err)
		}

		// Anonymous gRPC call to GetSiteDraft / SuspendSite must return Unauthenticated.
		anonCtx, anonCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer anonCancel()
		if _, err := userClient.GetSiteDraft(anonCtx, &userv1.GetSiteDraftRequest{ShopId: seller1.ShopID}); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("gRPC GetSiteDraft anonymous: want Unauthenticated, got %v", err)
		}
		if _, err := userClient.SuspendSite(anonCtx, &userv1.SuspendSiteRequest{SiteId: siteA.ID, Suspend: true}); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("gRPC SuspendSite anonymous: want Unauthenticated, got %v", err)
		}
	})

	// 5. Admin list, suspend -> 404, unsuspend -> PUBLISHED 200, and audit log verification!
	t.Run("AdminSuspendUnsuspend_And_AuditLogs", func(t *testing.T) {
		listResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/sites?status=PUBLISHED&q="+url.QueryEscape(subA), adminTok, nil, nil, http.StatusOK)
		var listOut AdminSitesListResponse
		if err := DecodeJSON(listResp, &listOut); err != nil {
			t.Fatalf("decode admin list sites: %v", err)
		}
		found := false
		for _, s := range listOut.Sites {
			if s.ID == siteA.ID {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected site %s (%s) in GET /api/v1/admin/sites?status=PUBLISHED&q=%s, got %+v", siteA.ID, subA, subA, listOut.Sites)
		}

		adminDetail := env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/sites/"+url.PathEscape(seller1.ShopID), adminTok, nil, nil, http.StatusOK)
		var detailOut SellerSiteResponse
		if err := DecodeJSON(adminDetail, &detailOut); err != nil {
			t.Fatalf("decode admin get site: %v", err)
		}
		if detailOut.Site.ID != siteA.ID {
			t.Fatalf("expected admin site detail id=%s, got %s", siteA.ID, detailOut.Site.ID)
		}

		// Suspend site -> status SUSPENDED, public by-host & by-shop return 404, seller publish/rollback rejected 409.
		suspResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/sites/"+url.PathEscape(siteA.ID)+"/suspend", adminTok, nil, map[string]any{
			"suspend": true,
			"reason":  "E2E compliance review",
		}, http.StatusOK)
		var suspOut SellerSiteResponse
		if err := DecodeJSON(suspResp, &suspOut); err != nil {
			t.Fatalf("decode suspend site: %v", err)
		}
		if suspOut.Site.Status != "SUSPENDED" || suspOut.Site.SuspendedReason != "E2E compliance review" {
			t.Fatalf("unexpected suspended site state: %+v", suspOut.Site)
		}

		env.mustDoJSON(t, http.MethodGet, "/api/v1/sites/by-host?host="+url.QueryEscape(hostA), "", nil, nil, http.StatusNotFound)
		env.mustDoJSON(t, http.MethodGet, "/api/v1/sites/by-shop/"+url.PathEscape(seller1.ShopID), "", nil, nil, http.StatusNotFound)
		env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/site/publish", seller1.AccessToken, nil, map[string]any{
			"note": "publish while suspended",
		}, http.StatusConflict)
		env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/site/rollback", seller1.AccessToken, nil, map[string]any{
			"version_id": v1ID,
			"note":       "rollback while suspended",
		}, http.StatusConflict)

		// Unsuspend site -> restores PUBLISHED & public by-host returns 200 again!
		unsuspResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/sites/"+url.PathEscape(siteA.ID)+"/suspend", adminTok, nil, map[string]any{
			"suspend": false,
			"reason":  "E2E review cleared",
		}, http.StatusOK)
		var unsuspOut SellerSiteResponse
		if err := DecodeJSON(unsuspResp, &unsuspOut); err != nil {
			t.Fatalf("decode unsuspend site: %v", err)
		}
		if unsuspOut.Site.Status != "PUBLISHED" {
			t.Fatalf("expected unsuspended site status PUBLISHED, got %q", unsuspOut.Site.Status)
		}

		restoredResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/sites/by-host?host="+url.QueryEscape(hostA), "", nil, nil, http.StatusOK)
		var restoredOut PublicSiteResponse
		if err := DecodeJSON(restoredResp, &restoredOut); err != nil {
			t.Fatalf("decode by-host after unsuspend: %v", err)
		}
		if !strings.Contains(string(restoredOut.Site.Config), v1Title) {
			t.Fatalf("expected restored site config to contain %q, got %s", v1Title, string(restoredOut.Site.Config))
		}

		// Verify audit logs recorded save_draft, publish, rollback, suspend, and unsuspend!
		auditPath := "/api/v1/admin/audit-logs?target_type=site&target_id=" + url.QueryEscape(siteA.ID) + "&page=1&page_size=50"
		auditResp := env.mustDoJSON(t, http.MethodGet, auditPath, adminTok, nil, nil, http.StatusOK)
		var auditOut AuditLogsListResponse
		if err := DecodeJSON(auditResp, &auditOut); err != nil {
			t.Fatalf("decode audit logs: %v", err)
		}

		seenActions := make(map[string]int)
		for _, entry := range auditOut.Items {
			seenActions[entry.Action]++
		}
		requiredActions := []string{
			"site.save_draft",
			"site.publish",
			"site.rollback",
			"admin.site.suspend",
			"admin.site.unsuspend",
		}
		for _, act := range requiredActions {
			if seenActions[act] == 0 {
				t.Fatalf("missing required site audit log action %q for site %s (seen=%v)", act, siteA.ID, seenActions)
			}
		}
		t.Logf("Site %s audit trail verified: %v", siteA.ID, seenActions)
	})
}
