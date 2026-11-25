package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// --- Site Templates DTOs ---

type e2eSiteTemplateDTO struct {
	ID              string          `json:"id"`
	Slug            string          `json:"slug"`
	Name            string          `json:"name"`
	Category        string          `json:"category"`
	Archetype       string          `json:"archetype"`
	Description     string          `json:"description"`
	PreviewImageURL string          `json:"preview_image_url"`
	Tags            []string        `json:"tags"`
	Config          json.RawMessage `json:"config"`
	IsActive        bool            `json:"is_active"`
	SortOrder       int32           `json:"sort_order"`
}

type e2eSiteTemplatesListResponse struct {
	Templates []e2eSiteTemplateDTO `json:"templates"`
	Total     int                  `json:"total"`
}

type e2eSiteTemplateDetailResponse struct {
	Template e2eSiteTemplateDTO `json:"template"`
}

type e2eApplySiteTemplateResponse struct {
	Site       SiteDTO            `json:"site"`
	Template   e2eSiteTemplateDTO `json:"template"`
	TemplateID string             `json:"template_id"`
}

func cloneJSONMap(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal site config map: %v (raw=%s)", err, string(raw))
	}
	return m
}

func setConfigThemeField(t *testing.T, cfg map[string]any, field, val string) {
	t.Helper()
	theme, ok := cfg["theme"].(map[string]any)
	if !ok {
		t.Fatalf("site config missing theme object: %+v", cfg)
	}
	theme[field] = val
}

func setFirstHeroCTAHref(t *testing.T, cfg map[string]any, href string) {
	t.Helper()
	secs, ok := cfg["sections"].([]any)
	if !ok {
		t.Fatalf("site config missing sections array: %+v", cfg)
	}
	for _, s := range secs {
		secMap, ok := s.(map[string]any)
		if !ok {
			continue
		}
		if secMap["type"] == "hero" {
			if props, ok := secMap["props"].(map[string]any); ok {
				props["cta_href"] = href
				return
			}
		}
	}
	t.Fatalf("no hero section found in template config to set cta_href")
}

func TestE2E_SiteTemplates_DiscoveryApplyPublishRollbackSecurityAndRBAC(t *testing.T) {
	outerT := t
	env := getEnv()
	registerRateLimitResetCleanup(t, env)

	adminTok := env.adminLogin(t)

	// 1. Public template discovery: >= 8 active templates with distinct archetypes.
	var templates []e2eSiteTemplateDTO
	t.Run("PublicCatalog_AtLeast8ActiveDistinctArchetypes", func(t *testing.T) {
		listResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/site-templates", "", nil, nil, http.StatusOK)
		var listOut e2eSiteTemplatesListResponse
		if err := DecodeJSON(listResp, &listOut); err != nil {
			t.Fatalf("decode GET /api/v1/site-templates: %v", err)
		}
		if len(listOut.Templates) < 8 {
			t.Fatalf("expected >= 8 active site templates, got %d", len(listOut.Templates))
		}

		archetypes := make(map[string]struct{}, len(listOut.Templates))
		for _, tmpl := range listOut.Templates {
			if !tmpl.IsActive {
				t.Fatalf("expected only active templates in public list, got inactive %q", tmpl.ID)
			}
			if tmpl.ID == "" || tmpl.Slug == "" || tmpl.Name == "" || tmpl.Archetype == "" {
				t.Fatalf("template missing required metadata: %+v", tmpl)
			}
			if len(tmpl.Config) == 0 {
				t.Fatalf("template %q missing config", tmpl.ID)
			}
			archetypes[tmpl.Archetype] = struct{}{}
		}
		if len(archetypes) < 8 {
			t.Fatalf("expected >= 8 distinct template archetypes, got %d (%v)", len(archetypes), archetypes)
		}
		templates = listOut.Templates
	})

	if len(templates) < 2 {
		t.Fatalf("cannot continue without >= 2 site templates")
	}

	// 2. Get single template detail with full v2 config + 404 for unknown template.
	chosen := templates[0]
	second := templates[1]
	t.Run("GetTemplateByID_FullConfigAndNotFound", func(t *testing.T) {
		detResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/site-templates/"+url.PathEscape(chosen.ID), "", nil, nil, http.StatusOK)
		var detOut e2eSiteTemplateDetailResponse
		if err := DecodeJSON(detResp, &detOut); err != nil {
			t.Fatalf("decode GET /api/v1/site-templates/%s: %v", chosen.ID, err)
		}
		if detOut.Template.ID != chosen.ID || detOut.Template.Archetype != chosen.Archetype {
			t.Fatalf("template detail mismatch: got %+v want id=%s", detOut.Template, chosen.ID)
		}
		cfgMap := cloneJSONMap(t, detOut.Template.Config)
		if ver, ok := cfgMap["schema_version"].(float64); !ok || int(ver) != 2 {
			t.Fatalf("expected schema_version=2 in template config, got %v", cfgMap["schema_version"])
		}
		for _, requiredKey := range []string{"theme", "layout", "sections", "pages"} {
			if _, exists := cfgMap[requiredKey]; !exists {
				t.Fatalf("template %s config missing key %q", chosen.ID, requiredKey)
			}
		}

		// Unknown template -> 404.
		env.mustDoJSON(t, http.MethodGet, "/api/v1/site-templates/does-not-exist-"+env.runID, "", nil, nil, http.StatusNotFound)
	})

	// Create a dedicated ephemeral seller so site lifecycle never collides with other tests or repeated runs.
	tmplSeller := env.createApprovedSeller(t, "seller-tmpl")
	sub := fmt.Sprintf("e2e-tmpl-%s-%d", env.runID, env.nextSeq())
	host := sub + "." + env.cfg.SiteRootDomain

	var (
		siteID    string
		baseDraft json.RawMessage
		v1ID      string
	)
	const (
		v1Tagline = "Phien ban template v1 da tuy chinh E2E"
		v2Tagline = "Phien ban template v2 cap nhat E2E"
	)

	// 3. Apply template -> Edit draft -> Publish v1 -> Verify by-host -> Publish v2 -> Rollback to v1.
	t.Run("ApplyEditPublishByHostAndRollback", func(t *testing.T) {
		applyResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/site/apply-template", tmplSeller.AccessToken, nil, map[string]any{
			"template_id": chosen.ID,
			"subdomain":   sub,
		}, http.StatusOK)
		var applyOut e2eApplySiteTemplateResponse
		if err := DecodeJSON(applyResp, &applyOut); err != nil {
			t.Fatalf("decode apply-template: %v", err)
		}
		if applyOut.Site.ID == "" || applyOut.Site.ShopID != tmplSeller.ShopID {
			t.Fatalf("unexpected applied site: %+v", applyOut.Site)
		}
		if applyOut.Site.Subdomain != sub || applyOut.Site.Status != "DRAFT" {
			t.Fatalf("expected DRAFT site on subdomain %q, got %+v", sub, applyOut.Site)
		}
		if applyOut.TemplateID != chosen.ID || applyOut.Template.ID != chosen.ID {
			t.Fatalf("expected applied template_id=%q, got %+v", chosen.ID, applyOut)
		}
		siteID = applyOut.Site.ID
		baseDraft = applyOut.Site.Config

		// Always suspend ephemeral site in outerT.Cleanup (after all subtests finish) so repeated runs
		// never pollute the public directory and subsequent security subtests still operate on the active site.
		ephemeralSiteID := siteID
		outerT.Cleanup(func() {
			_, _ = env.client.DoJSON(context.Background(), http.MethodPost,
				"/api/v1/admin/sites/"+url.PathEscape(ephemeralSiteID)+"/suspend",
				adminTok, nil, map[string]any{
					"suspend": true,
					"reason":  "e2e template test cleanup",
				})
		})

		// Draft must NOT be publicly visible before publish.
		env.mustDoJSON(t, http.MethodGet, "/api/v1/sites/by-host?host="+url.QueryEscape(host), "", nil, nil, http.StatusNotFound)

		// Edit draft v1 via PUT /api/v1/seller/site/draft.
		v1Cfg := cloneJSONMap(t, baseDraft)
		setConfigThemeField(t, v1Cfg, "tagline", v1Tagline)
		editResp := env.mustDoJSON(t, http.MethodPut, "/api/v1/seller/site/draft", tmplSeller.AccessToken, nil, map[string]any{
			"subdomain": sub,
			"config":    v1Cfg,
		}, http.StatusOK)
		var editOut SellerSiteResponse
		if err := DecodeJSON(editResp, &editOut); err != nil {
			t.Fatalf("decode draft edit v1: %v", err)
		}
		if !strings.Contains(string(editOut.Site.Config), v1Tagline) {
			t.Fatalf("expected edited draft to contain %q, got %s", v1Tagline, string(editOut.Site.Config))
		}
		baseDraft = editOut.Site.Config

		// Publish v1 -> verify public GET /api/v1/sites/by-host.
		pub1Resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/site/publish", tmplSeller.AccessToken, nil, map[string]any{
			"note": "E2E template v1",
		}, http.StatusOK)
		var pub1Out SitePublishResponse
		if err := DecodeJSON(pub1Resp, &pub1Out); err != nil {
			t.Fatalf("decode publish v1: %v", err)
		}
		if pub1Out.Site.Status != "PUBLISHED" || pub1Out.Version.VersionNumber != 1 || pub1Out.Version.ID == "" {
			t.Fatalf("unexpected publish v1 response: %+v", pub1Out)
		}
		v1ID = pub1Out.Version.ID

		byHost1Resp := env.mustDoJSON(t, http.MethodGet, "/api/v1/sites/by-host?host="+url.QueryEscape(host), "", nil, nil, http.StatusOK)
		var byHost1 PublicSiteResponse
		if err := DecodeJSON(byHost1Resp, &byHost1); err != nil {
			t.Fatalf("decode by-host v1: %v", err)
		}
		if byHost1.Site.ID != siteID || byHost1.Shop.ID != tmplSeller.ShopID || !strings.Contains(string(byHost1.Site.Config), v1Tagline) {
			t.Fatalf("unexpected public site v1: %+v", byHost1)
		}

		// Apply a second template, customize draft to v2Tagline, publish v2, then rollback to v1.
		apply2Resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/site/apply-template", tmplSeller.AccessToken, nil, map[string]any{
			"template_id": second.ID,
		}, http.StatusOK)
		var apply2Out e2eApplySiteTemplateResponse
		if err := DecodeJSON(apply2Resp, &apply2Out); err != nil {
			t.Fatalf("decode second apply-template: %v", err)
		}
		v2Cfg := cloneJSONMap(t, apply2Out.Site.Config)
		setConfigThemeField(t, v2Cfg, "tagline", v2Tagline)
		env.mustDoJSON(t, http.MethodPut, "/api/v1/seller/site/draft", tmplSeller.AccessToken, nil, map[string]any{
			"subdomain": sub,
			"config":    v2Cfg,
		}, http.StatusOK)

		pub2Resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/site/publish", tmplSeller.AccessToken, nil, map[string]any{
			"note": "E2E template v2",
		}, http.StatusOK)
		var pub2Out SitePublishResponse
		if err := DecodeJSON(pub2Resp, &pub2Out); err != nil {
			t.Fatalf("decode publish v2: %v", err)
		}
		if pub2Out.Version.VersionNumber != 2 {
			t.Fatalf("expected version_number=2 on second publish, got %+v", pub2Out.Version)
		}

		byHost2Resp := env.mustDoJSON(t, http.MethodGet, "/api/v1/sites/by-host?host="+url.QueryEscape(host), "", nil, nil, http.StatusOK)
		var byHost2 PublicSiteResponse
		if err := DecodeJSON(byHost2Resp, &byHost2); err != nil {
			t.Fatalf("decode by-host v2: %v", err)
		}
		if !strings.Contains(string(byHost2.Site.Config), v2Tagline) {
			t.Fatalf("expected public site v2 to contain %q, got %s", v2Tagline, string(byHost2.Site.Config))
		}

		// Rollback to v1 -> public by-host returns v1 again.
		rbResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/site/rollback", tmplSeller.AccessToken, nil, map[string]any{
			"version_id": v1ID,
			"note":       "E2E rollback template to v1",
		}, http.StatusOK)
		var rbOut SitePublishResponse
		if err := DecodeJSON(rbResp, &rbOut); err != nil {
			t.Fatalf("decode rollback v1: %v", err)
		}
		if rbOut.Site.PublishedVersionID != v1ID {
			t.Fatalf("expected published_version_id=%s after rollback, got %s", v1ID, rbOut.Site.PublishedVersionID)
		}

		byHostRBResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/sites/by-host?host="+url.QueryEscape(host), "", nil, nil, http.StatusOK)
		var byHostRB PublicSiteResponse
		if err := DecodeJSON(byHostRBResp, &byHostRB); err != nil {
			t.Fatalf("decode by-host after rollback: %v", err)
		}
		if !strings.Contains(string(byHostRB.Site.Config), v1Tagline) || strings.Contains(string(byHostRB.Site.Config), v2Tagline) {
			t.Fatalf("expected rollback to restore %q (without %q), got %s", v1Tagline, v2Tagline, string(byHostRB.Site.Config))
		}
	})

	// 4. Strict XSS, SSRF, SVG, and CSS @import rejection on v2 draft updates -> HTTP 400.
	t.Run("SecurityValidation_RejectsXSS_SSRF_SVG_AndCSSImport", func(t *testing.T) {
		badCases := []struct {
			name   string
			mutate func(t *testing.T, cfg map[string]any)
		}{
			{
				name: "script tag in tagline",
				mutate: func(t *testing.T, cfg map[string]any) {
					setConfigThemeField(t, cfg, "tagline", "Hello <script>alert(1)</script>")
				},
			},
			{
				name: "javascript scheme in hero cta_href",
				mutate: func(t *testing.T, cfg map[string]any) {
					setFirstHeroCTAHref(t, cfg, "javascript:alert(1)")
				},
			},
			{
				name: "javascript scheme in tagline",
				mutate: func(t *testing.T, cfg map[string]any) {
					setConfigThemeField(t, cfg, "tagline", "Click javascript:void(0)")
				},
			},
			{
				name: "inline onerror handler",
				mutate: func(t *testing.T, cfg map[string]any) {
					setConfigThemeField(t, cfg, "tagline", "img src=x onerror=alert(1)")
				},
			},
			{
				name: "svg external logo_url",
				mutate: func(t *testing.T, cfg map[string]any) {
					setConfigThemeField(t, cfg, "logo_url", "https://cdn.example.com/evil-logo.svg")
				},
			},
			{
				name: "svg relative logo_url",
				mutate: func(t *testing.T, cfg map[string]any) {
					setConfigThemeField(t, cfg, "logo_url", "/icons/evil.svg")
				},
			},
			{
				name: "ssrf loopback http 127.0.0.1",
				mutate: func(t *testing.T, cfg map[string]any) {
					setConfigThemeField(t, cfg, "banner_url", "http://127.0.0.1/admin.png")
				},
			},
			{
				name: "ssrf loopback https 127.0.0.1",
				mutate: func(t *testing.T, cfg map[string]any) {
					setConfigThemeField(t, cfg, "banner_url", "https://127.0.0.1/admin.png")
				},
			},
			{
				name: "ssrf metadata ip http 169.254.169.254",
				mutate: func(t *testing.T, cfg map[string]any) {
					setConfigThemeField(t, cfg, "banner_url", "http://169.254.169.254/latest/meta-data/img.png")
				},
			},
			{
				name: "ssrf metadata ip https 169.254.169.254",
				mutate: func(t *testing.T, cfg map[string]any) {
					setConfigThemeField(t, cfg, "banner_url", "https://169.254.169.254/latest/meta-data/img.png")
				},
			},
			{
				name: "css @import injection",
				mutate: func(t *testing.T, cfg map[string]any) {
					setConfigThemeField(t, cfg, "tagline", `@import url("https://evil.example/a.css");`)
				},
			},
		}

		for _, tc := range badCases {
			cfg := cloneJSONMap(t, baseDraft)
			tc.mutate(t, cfg)
			resp := env.mustDoJSON(t, http.MethodPut, "/api/v1/seller/site/draft", tmplSeller.AccessToken, nil, map[string]any{
				"subdomain": sub,
				"config":    cfg,
			}, http.StatusBadRequest)
			t.Logf("Site template security case [%s] rejected: HTTP %d", tc.name, resp.StatusCode)
		}
	})

	// 5. RBAC: anonymous gets 401; buyer and admin get 403 on seller apply-template.
	t.Run("RBAC_BuyerAdminAndAnonRejectedOnApplyTemplate", func(t *testing.T) {
		env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/site/apply-template", "", nil, map[string]any{
			"template_id": chosen.ID,
		}, http.StatusUnauthorized)

		buyerTok := MintAccessToken(env.cfg.JWTSecret, uuid.NewString(), "buyer")
		env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/site/apply-template", buyerTok, nil, map[string]any{
			"template_id": chosen.ID,
		}, http.StatusForbidden)

		env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/site/apply-template", adminTok, nil, map[string]any{
			"template_id": chosen.ID,
		}, http.StatusForbidden)
	})
}
