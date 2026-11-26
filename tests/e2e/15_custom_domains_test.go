package e2e

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func createAndPublishSiteForDomainTest(
	t *testing.T,
	env *testEnv,
	seller *sellerFixture,
	subdomain string,
	heroTitle string,
	accentHex string,
) SiteDTO {
	t.Helper()

	env.mustDoJSON(t, http.MethodPut, "/api/v1/seller/site/draft", seller.AccessToken, nil, map[string]any{
		"subdomain": subdomain,
		"config":    validSiteConfigPayload(seller.ShopName, heroTitle, accentHex),
	}, http.StatusOK)

	pubResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/site/publish", seller.AccessToken, nil, map[string]any{
		"note": "E2E custom domains publish",
	}, http.StatusOK)
	var pubOut SitePublishResponse
	if err := DecodeJSON(pubResp, &pubOut); err != nil {
		t.Fatalf("decode publish site (%s): %v", subdomain, err)
	}
	if pubOut.Site.ID == "" || pubOut.Site.Status != "PUBLISHED" {
		t.Fatalf("unexpected published site (%s): %+v", subdomain, pubOut.Site)
	}
	return pubOut.Site
}

func listSellerSiteDomains(t *testing.T, env *testEnv, sellerToken string) []SiteDomainDTO {
	t.Helper()
	resp := env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/site/domains", sellerToken, nil, nil, http.StatusOK)
	var out SiteDomainsListResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode seller domains list: %v", err)
	}
	return out.Domains
}

func listAdminSiteDomains(t *testing.T, env *testEnv, adminToken, shopID string) []SiteDomainDTO {
	t.Helper()
	path := "/api/v1/admin/sites/" + url.PathEscape(shopID) + "/domains"
	resp := env.mustDoJSON(t, http.MethodGet, path, adminToken, nil, nil, http.StatusOK)
	var out SiteDomainsListResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode admin domains list for shop %s: %v", shopID, err)
	}
	return out.Domains
}

func findSiteDomainByHost(domains []SiteDomainDTO, host string) *SiteDomainDTO {
	for i := range domains {
		if strings.EqualFold(domains[i].Host, host) {
			return &domains[i]
		}
	}
	return nil
}

func findSiteDomainByID(domains []SiteDomainDTO, id string) *SiteDomainDTO {
	for i := range domains {
		if domains[i].ID == id {
			return &domains[i]
		}
	}
	return nil
}

func assertAPIErrorCode(t *testing.T, resp *RawResponse, wantCode string) {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := DecodeJSON(resp, &body); err != nil {
		t.Fatalf("decode error response (want code=%q): %v", wantCode, err)
	}
	if body.Error.Code != wantCode {
		t.Fatalf("expected error.code=%q, got %q (body=%s)", wantCode, body.Error.Code, string(resp.Body))
	}
}

func cleanupCustomDomainsForShop(env *testEnv, adminToken, shopID string) {
	if shopID == "" || adminToken == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	listPath := "/api/v1/admin/sites/" + url.PathEscape(shopID) + "/domains"
	resp, err := env.client.DoJSON(ctx, http.MethodGet, listPath, adminToken, nil, nil)
	if err != nil || resp.StatusCode != http.StatusOK {
		return
	}
	var out SiteDomainsListResponse
	if err := DecodeJSON(resp, &out); err != nil {
		return
	}
	for _, d := range out.Domains {
		if d.Kind != "CUSTOM" || d.ID == "" {
			continue
		}
		delPath := "/api/v1/admin/sites/" + url.PathEscape(shopID) + "/domains/" + url.PathEscape(d.ID)
		_, _ = env.client.DoJSON(ctx, http.MethodDelete, delPath, adminToken, nil, nil)
	}
}

func TestE2E_CustomDomains_LifecycleValidationRBACAndPrimary(t *testing.T) {
	env := getEnv()
	adminTok := env.adminLogin(t)

	resetRateLimits := func() {
		if env.cfg.RedisAddr == "" {
			return
		}
		rCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = ResetRateLimitKeys(rCtx, env.cfg.RedisAddr, env.cfg.RedisPassword)
	}

	// Start with a fresh rate-limit window and register Redis reset first so LIFO t.Cleanup
	// runs it last, after domain deletion and throwaway site suspension.
	resetRateLimits()
	t.Cleanup(resetRateLimits)

	sellerA := env.createApprovedSeller(t, "cdom-a")
	sellerB := env.createApprovedSeller(t, "cdom-b")

	runTag := strings.ToLower(env.runID)
	subA := fmt.Sprintf("e2e-cdom-%s-a", runTag)
	subB := fmt.Sprintf("e2e-cdom-%s-b", runTag)
	subHostA := subA + "." + env.cfg.SiteRootDomain
	subHostB := subB + "." + env.cfg.SiteRootDomain

	pendingHost := fmt.Sprintf("e2e-pend-%s.pending.test", runTag)
	secondCustomHost := fmt.Sprintf("e2e-extra-%s.pending.test", runTag)
	activeHost := fmt.Sprintf("e2e-act-%s.verified.test", runTag)
	failHost := fmt.Sprintf("e2e-fail-%s.fail.test", runTag)
	squatPendingHost := fmt.Sprintf("squat-%s.pending.test", runTag)
	squatFailHost := fmt.Sprintf("x-%s.fail.test", runTag)

	var (
		siteA     SiteDTO
		siteB     SiteDTO
		pendingID string
		activeID  string
	)

	// Cleanup any remaining custom domains and suspend throwaway sites so repeated runs stay
	// 100% idempotent and do not pollute the public site directory.
	t.Cleanup(func() {
		cleanupCustomDomainsForShop(env, adminTok, sellerA.ShopID)
		cleanupCustomDomainsForShop(env, adminTok, sellerB.ShopID)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, siteID := range []string{siteA.ID, siteB.ID} {
			if siteID == "" {
				continue
			}
			_, _ = env.client.DoJSON(ctx, http.MethodPost, "/api/v1/admin/sites/"+url.PathEscape(siteID)+"/suspend", adminTok, nil, map[string]any{
				"suspend": true,
				"reason":  "e2e custom domains cleanup",
			})
		}
	})

	// (a) Create & publish 2 sites; verify initial SUBDOMAIN domain rows (ACTIVE, is_primary=true).
	t.Run("SetupSites_InitialSubdomainDomainsActivePrimary", func(t *testing.T) {
		siteA = createAndPublishSiteForDomainTest(t, env, sellerA, subA, "Custom Domains Site A", "#1D4ED8")
		siteB = createAndPublishSiteForDomainTest(t, env, sellerB, subB, "Custom Domains Site B", "#0F766E")

		if siteA.PrimaryHost != subHostA {
			t.Fatalf("siteA expected primary_host=%q, got %q", subHostA, siteA.PrimaryHost)
		}
		if siteB.PrimaryHost != subHostB {
			t.Fatalf("siteB expected primary_host=%q, got %q", subHostB, siteB.PrimaryHost)
		}

		for _, tc := range []struct {
			name    string
			token   string
			subHost string
		}{
			{"sellerA", sellerA.AccessToken, subHostA},
			{"sellerB", sellerB.AccessToken, subHostB},
		} {
			domains := listSellerSiteDomains(t, env, tc.token)
			subDom := findSiteDomainByHost(domains, tc.subHost)
			if subDom == nil {
				t.Fatalf("%s domains missing initial subdomain %q: %+v", tc.name, tc.subHost, domains)
			}
			if subDom.ID == "" || subDom.Kind != "SUBDOMAIN" || subDom.Status != "ACTIVE" || !subDom.IsPrimary {
				t.Fatalf("%s unexpected initial subdomain row: %+v", tc.name, *subDom)
			}
		}

		byHostResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/sites/by-host?host="+url.QueryEscape(subHostA), "", nil, nil, http.StatusOK)
		var pubOut PublicSiteResponse
		if err := DecodeJSON(byHostResp, &pubOut); err != nil {
			t.Fatalf("decode initial by-host for %s: %v", subHostA, err)
		}
		if pubOut.Site.ID != siteA.ID || pubOut.Site.PrimaryHost != subHostA {
			t.Fatalf("unexpected initial by-host response for %s: %+v", subHostA, pubOut.Site)
		}
	})

	// (b) Bad hostnames must be rejected with HTTP 400.
	t.Run("BadHostnames_RejectedWith400", func(t *testing.T) {
		badHosts := []struct {
			name string
			host string
		}{
			{"ipv4 loopback", "127.0.0.1"},
			{"ipv4 private", "192.168.1.10"},
			{"ipv6 bracketed loopback", "[::1]"},
			{"ipv6 address", "2001:db8::1"},
			{"root domain", env.cfg.SiteRootDomain},
			{"subdomain of root", "shop." + env.cfg.SiteRootDomain},
			{"wildcard example", "*.example.test"},
			{"wildcard verified", "*.shop.verified.test"},
			{"host with port", "shop.verified.test:3300"},
			{"no dot localhost", "localhost"},
			{"no dot onlylabel", "onlylabel"},
			{"forbidden .local", "shop.local"},
			{"forbidden .internal", "shop.internal"},
			{"forbidden .localhost", "foo.localhost"},
			{"empty hostname", ""},
			{"whitespace hostname", "   "},
			{"space in hostname", "bad host.test"},
			{"underscore in hostname", "bad_host.test"},
		}

		for _, tc := range badHosts {
			resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/site/domains", sellerA.AccessToken, nil, map[string]any{
				"hostname": tc.host,
			}, http.StatusBadRequest)
			assertAPIErrorCode(t, resp, "invalid_hostname")
			t.Logf("Bad hostname [%s] (%q) rejected cleanly: HTTP %d (invalid_hostname)", tc.name, tc.host, resp.StatusCode)
		}

		domains := listSellerSiteDomains(t, env, sellerA.AccessToken)
		for _, d := range domains {
			if d.Kind == "CUSTOM" {
				t.Fatalf("unexpected CUSTOM domain created from invalid hostname tests: %+v", d)
			}
		}
	})

	// (c) Add PENDING custom domain -> 201 with verification records; by-host -> 404; check keeps PENDING & primary=subdomain.
	t.Run("PendingCustomDomain_CreateCheckAndNotPublic", func(t *testing.T) {
		addResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/site/domains", sellerA.AccessToken, nil, map[string]any{
			"hostname": pendingHost,
		}, http.StatusCreated)
		var addOut SiteDomainResponse
		if err := DecodeJSON(addResp, &addOut); err != nil {
			t.Fatalf("decode add pending domain: %v", err)
		}
		dom := addOut.Domain
		if dom.ID == "" || dom.Host != pendingHost || dom.Kind != "CUSTOM" || dom.Status != "PENDING" || dom.IsPrimary {
			t.Fatalf("unexpected pending domain creation response: %+v", dom)
		}
		if len(dom.Verification) == 0 {
			t.Fatalf("expected non-empty verification records for pending domain %+v", dom)
		}
		for i, rec := range dom.Verification {
			if strings.TrimSpace(rec.Type) == "" || strings.TrimSpace(rec.Name) == "" || strings.TrimSpace(rec.Value) == "" {
				t.Fatalf("verification record #%d has empty fields: %+v", i, rec)
			}
		}
		pendingID = dom.ID

		// PENDING custom domain must not resolve via public by-host.
		env.mustDoJSON(t, http.MethodGet, "/api/v1/sites/by-host?host="+url.QueryEscape(pendingHost), "", nil, nil, http.StatusNotFound)

		// Checking a .pending.test domain keeps it PENDING and non-primary.
		checkResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/site/domains/"+url.PathEscape(pendingID)+"/check", sellerA.AccessToken, nil, nil, http.StatusOK)
		var checkOut SiteDomainResponse
		if err := DecodeJSON(checkResp, &checkOut); err != nil {
			t.Fatalf("decode check pending domain: %v", err)
		}
		if checkOut.Domain.ID != pendingID || checkOut.Domain.Host != pendingHost ||
			checkOut.Domain.Kind != "CUSTOM" || checkOut.Domain.Status != "PENDING" || checkOut.Domain.IsPrimary {
			t.Fatalf("expected pending domain to stay PENDING and non-primary after check, got %+v", checkOut.Domain)
		}

		// Subdomain remains primary_host and PENDING host still returns 404.
		env.mustDoJSON(t, http.MethodGet, "/api/v1/sites/by-host?host="+url.QueryEscape(pendingHost), "", nil, nil, http.StatusNotFound)
		subResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/sites/by-host?host="+url.QueryEscape(subHostA), "", nil, nil, http.StatusOK)
		var subOut PublicSiteResponse
		if err := DecodeJSON(subResp, &subOut); err != nil {
			t.Fatalf("decode by-host subdomain while custom pending: %v", err)
		}
		if subOut.Site.PrimaryHost != subHostA {
			t.Fatalf("expected primary_host=%q while custom domain is PENDING, got %q", subHostA, subOut.Site.PrimaryHost)
		}
	})

	// (d) Max 1 custom domain per site -> 409; duplicate host on sellerB (lower & upper) -> 409.
	t.Run("CustomDomainLimitAndDuplicateHost_Conflict409", func(t *testing.T) {
		if pendingID == "" {
			t.Fatal("prerequisite pending domain was not created")
		}

		// Seller A already has 1 CUSTOM domain -> adding a second returns 409 domain_limit.
		limitResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/site/domains", sellerA.AccessToken, nil, map[string]any{
			"hostname": secondCustomHost,
		}, http.StatusConflict)
		assertAPIErrorCode(t, limitResp, "domain_limit")

		// Seller B trying to claim Seller A's host (lowercase & uppercase) -> 409 domain_taken.
		takenLower := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/site/domains", sellerB.AccessToken, nil, map[string]any{
			"hostname": pendingHost,
		}, http.StatusConflict)
		assertAPIErrorCode(t, takenLower, "domain_taken")
		takenUpper := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/site/domains", sellerB.AccessToken, nil, map[string]any{
			"hostname": strings.ToUpper(pendingHost),
		}, http.StatusConflict)
		assertAPIErrorCode(t, takenUpper, "domain_taken")
	})

	// (e) RBAC & cross-tenant isolation across seller, buyer, and admin endpoints.
	t.Run("RBAC_CrossTenantAndRoleIsolation", func(t *testing.T) {
		if pendingID == "" {
			t.Fatal("prerequisite pending domain was not created")
		}

		// Seller B cannot check or delete Seller A's domain -> 403 or 404.
		env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/site/domains/"+url.PathEscape(pendingID)+"/check", sellerB.AccessToken, nil, nil, http.StatusForbidden, http.StatusNotFound)
		env.mustDoJSON(t, http.MethodDelete, "/api/v1/seller/site/domains/"+url.PathEscape(pendingID), sellerB.AccessToken, nil, nil, http.StatusForbidden, http.StatusNotFound)

		// Buyer cannot call seller domain endpoints -> 403.
		buyerTok := MintAccessToken(env.cfg.JWTSecret, uuid.NewString(), "buyer")
		env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/site/domains", buyerTok, nil, nil, http.StatusForbidden)
		env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/site/domains", buyerTok, nil, map[string]any{
			"hostname": fmt.Sprintf("buyer-%s.pending.test", runTag),
		}, http.StatusForbidden)
		env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/site/domains/"+url.PathEscape(pendingID)+"/check", buyerTok, nil, nil, http.StatusForbidden)
		env.mustDoJSON(t, http.MethodDelete, "/api/v1/seller/site/domains/"+url.PathEscape(pendingID), buyerTok, nil, nil, http.StatusForbidden)

		// Seller cannot call admin domain endpoints -> 403.
		adminListPath := "/api/v1/admin/sites/" + url.PathEscape(sellerA.ShopID) + "/domains"
		adminDelPath := adminListPath + "/" + url.PathEscape(pendingID)
		env.mustDoJSON(t, http.MethodGet, adminListPath, sellerA.AccessToken, nil, nil, http.StatusForbidden)
		env.mustDoJSON(t, http.MethodDelete, adminDelPath, sellerA.AccessToken, nil, nil, http.StatusForbidden)
		env.mustDoJSON(t, http.MethodGet, adminListPath, sellerB.AccessToken, nil, nil, http.StatusForbidden)
		env.mustDoJSON(t, http.MethodDelete, adminDelPath, sellerB.AccessToken, nil, nil, http.StatusForbidden)
	})

	// (f) Delete PENDING custom domain -> 204 and disappears from seller domain list.
	t.Run("DeletePendingCustomDomain_204AndRemoved", func(t *testing.T) {
		if pendingID == "" {
			t.Fatal("prerequisite pending domain was not created")
		}

		env.mustDoJSON(t, http.MethodDelete, "/api/v1/seller/site/domains/"+url.PathEscape(pendingID), sellerA.AccessToken, nil, nil, http.StatusNoContent)

		domains := listSellerSiteDomains(t, env, sellerA.AccessToken)
		if findSiteDomainByID(domains, pendingID) != nil || findSiteDomainByHost(domains, pendingHost) != nil {
			t.Fatalf("deleted pending domain %s (%s) still present in list: %+v", pendingID, pendingHost, domains)
		}
		subDom := findSiteDomainByHost(domains, subHostA)
		if subDom == nil || subDom.Status != "ACTIVE" || !subDom.IsPrimary {
			t.Fatalf("expected subdomain %q to remain ACTIVE primary after deleting pending custom domain, got %+v", subHostA, domains)
		}
	})

	// (g) Add ACTIVE custom domain (.verified.test) -> check -> ACTIVE & is_primary=true; by-host resolves both custom and subdomain with primary_host=customHost; sellerB blocked.
	t.Run("ActiveCustomDomain_PromotesPrimaryAndResolvesByHost", func(t *testing.T) {
		addResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/site/domains", sellerA.AccessToken, nil, map[string]any{
			"hostname": activeHost,
		}, http.StatusCreated)
		var addOut SiteDomainResponse
		if err := DecodeJSON(addResp, &addOut); err != nil {
			t.Fatalf("decode add verified domain: %v", err)
		}
		dom := addOut.Domain
		if dom.ID == "" || dom.Host != activeHost || dom.Kind != "CUSTOM" {
			t.Fatalf("unexpected verified domain creation response: %+v", dom)
		}
		if dom.Status != "PENDING" && dom.Status != "ACTIVE" {
			t.Fatalf("expected newly created verified domain status PENDING or ACTIVE, got %q", dom.Status)
		}
		activeID = dom.ID

		// Check activates .verified.test (also accepts the case where the 5s dev background poller activated it first).
		checkResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/site/domains/"+url.PathEscape(activeID)+"/check", sellerA.AccessToken, nil, nil, http.StatusOK)
		var checkOut SiteDomainResponse
		if err := DecodeJSON(checkResp, &checkOut); err != nil {
			t.Fatalf("decode check verified domain: %v", err)
		}
		if checkOut.Domain.ID != activeID || checkOut.Domain.Host != activeHost ||
			checkOut.Domain.Kind != "CUSTOM" || checkOut.Domain.Status != "ACTIVE" || !checkOut.Domain.IsPrimary {
			t.Fatalf("expected verified domain ACTIVE and primary after check, got %+v", checkOut.Domain)
		}

		// Seller list reflects CUSTOM is_primary=true and SUBDOMAIN is_primary=false.
		domains := listSellerSiteDomains(t, env, sellerA.AccessToken)
		customDom := findSiteDomainByID(domains, activeID)
		subDom := findSiteDomainByHost(domains, subHostA)
		if customDom == nil || customDom.Kind != "CUSTOM" || customDom.Status != "ACTIVE" || !customDom.IsPrimary {
			t.Fatalf("expected CUSTOM domain %q ACTIVE+primary in list, got %+v", activeHost, domains)
		}
		if subDom == nil || subDom.Kind != "SUBDOMAIN" || subDom.Status != "ACTIVE" || subDom.IsPrimary {
			t.Fatalf("expected SUBDOMAIN %q ACTIVE+non-primary in list, got %+v", subHostA, domains)
		}

		// Public by-host on custom host returns 200 with primary_host == activeHost.
		byCustomResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/sites/by-host?host="+url.QueryEscape(activeHost), "", nil, nil, http.StatusOK)
		var byCustomOut PublicSiteResponse
		if err := DecodeJSON(byCustomResp, &byCustomOut); err != nil {
			t.Fatalf("decode by-host custom %s: %v", activeHost, err)
		}
		if byCustomOut.Site.ID != siteA.ID || byCustomOut.Shop.ID != sellerA.ShopID ||
			byCustomOut.Site.Status != "PUBLISHED" || byCustomOut.Site.PrimaryHost != activeHost {
			t.Fatalf("unexpected by-host response for custom domain %s: %+v", activeHost, byCustomOut)
		}

		// Public by-host on subdomain still returns 200, now advertising primary_host == activeHost.
		bySubResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/sites/by-host?host="+url.QueryEscape(subHostA), "", nil, nil, http.StatusOK)
		var bySubOut PublicSiteResponse
		if err := DecodeJSON(bySubResp, &bySubOut); err != nil {
			t.Fatalf("decode by-host subdomain %s after custom activation: %v", subHostA, err)
		}
		if bySubOut.Site.ID != siteA.ID || bySubOut.Site.PrimaryHost != activeHost {
			t.Fatalf("expected by-host on subdomain %s to report primary_host=%q, got %+v", subHostA, activeHost, bySubOut.Site)
		}

		// Seller B cannot claim duplicate ACTIVE host (lower/upper) or check/delete Seller A's domain.
		activeTakenLower := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/site/domains", sellerB.AccessToken, nil, map[string]any{
			"hostname": activeHost,
		}, http.StatusConflict)
		assertAPIErrorCode(t, activeTakenLower, "domain_taken")
		activeTakenUpper := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/site/domains", sellerB.AccessToken, nil, map[string]any{
			"hostname": strings.ToUpper(activeHost),
		}, http.StatusConflict)
		assertAPIErrorCode(t, activeTakenUpper, "domain_taken")
		env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/site/domains/"+url.PathEscape(activeID)+"/check", sellerB.AccessToken, nil, nil, http.StatusForbidden, http.StatusNotFound)
		env.mustDoJSON(t, http.MethodDelete, "/api/v1/seller/site/domains/"+url.PathEscape(activeID), sellerB.AccessToken, nil, nil, http.StatusForbidden, http.StatusNotFound)
	})

	// (h) Admin lists shopA domains (SUBDOMAIN + CUSTOM); sellerB adds .fail.test -> FAILED, by-host 404 -> admin deletes it.
	t.Run("AdminListDomains_And_FailedCustomDomainDeletedByAdmin", func(t *testing.T) {
		if activeID == "" {
			t.Fatal("prerequisite active domain was not created")
		}

		adminDomainsA := listAdminSiteDomains(t, env, adminTok, sellerA.ShopID)
		subAInAdmin := findSiteDomainByHost(adminDomainsA, subHostA)
		customAInAdmin := findSiteDomainByID(adminDomainsA, activeID)
		if subAInAdmin == nil || subAInAdmin.Kind != "SUBDOMAIN" || subAInAdmin.IsPrimary {
			t.Fatalf("admin list for shopA missing expected SUBDOMAIN row: %+v", adminDomainsA)
		}
		if customAInAdmin == nil || customAInAdmin.Kind != "CUSTOM" || customAInAdmin.Host != activeHost ||
			customAInAdmin.Status != "ACTIVE" || !customAInAdmin.IsPrimary {
			t.Fatalf("admin list for shopA missing expected CUSTOM ACTIVE primary row: %+v", adminDomainsA)
		}

		// Seller B adds .fail.test -> check transitions to FAILED -> by-host returns 404.
		addFailResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/site/domains", sellerB.AccessToken, nil, map[string]any{
			"hostname": failHost,
		}, http.StatusCreated)
		var addFailOut SiteDomainResponse
		if err := DecodeJSON(addFailResp, &addFailOut); err != nil {
			t.Fatalf("decode add fail domain: %v", err)
		}
		failID := addFailOut.Domain.ID
		if failID == "" || addFailOut.Domain.Host != failHost || addFailOut.Domain.Kind != "CUSTOM" {
			t.Fatalf("unexpected fail domain creation response: %+v", addFailOut.Domain)
		}

		checkFailResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/site/domains/"+url.PathEscape(failID)+"/check", sellerB.AccessToken, nil, nil, http.StatusOK)
		var checkFailOut SiteDomainResponse
		if err := DecodeJSON(checkFailResp, &checkFailOut); err != nil {
			t.Fatalf("decode check fail domain: %v", err)
		}
		if checkFailOut.Domain.ID != failID || checkFailOut.Domain.Status != "FAILED" || checkFailOut.Domain.IsPrimary {
			t.Fatalf("expected fail domain to be FAILED and non-primary after check, got %+v", checkFailOut.Domain)
		}
		t.Logf("Fail domain %s transitioned cleanly to FAILED (last_error=%q)", failHost, checkFailOut.Domain.LastError)

		// FAILED custom domain must return 404 on by-host; shopB subdomain stays primary.
		env.mustDoJSON(t, http.MethodGet, "/api/v1/sites/by-host?host="+url.QueryEscape(failHost), "", nil, nil, http.StatusNotFound)
		subBResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/sites/by-host?host="+url.QueryEscape(subHostB), "", nil, nil, http.StatusOK)
		var subBOut PublicSiteResponse
		if err := DecodeJSON(subBResp, &subBOut); err != nil {
			t.Fatalf("decode by-host for shopB subdomain: %v", err)
		}
		if subBOut.Site.PrimaryHost != subHostB {
			t.Fatalf("expected shopB primary_host=%q while custom domain FAILED, got %q", subHostB, subBOut.Site.PrimaryHost)
		}

		// Admin deletes Seller B's FAILED custom domain -> 204 and removed from both admin & seller lists.
		adminDelPath := "/api/v1/admin/sites/" + url.PathEscape(sellerB.ShopID) + "/domains/" + url.PathEscape(failID)
		env.mustDoJSON(t, http.MethodDelete, adminDelPath, adminTok, nil, nil, http.StatusNoContent)

		adminDomainsB := listAdminSiteDomains(t, env, adminTok, sellerB.ShopID)
		if findSiteDomainByID(adminDomainsB, failID) != nil || findSiteDomainByHost(adminDomainsB, failHost) != nil {
			t.Fatalf("admin-deleted domain %s still present in admin list: %+v", failID, adminDomainsB)
		}
		sellerDomainsB := listSellerSiteDomains(t, env, sellerB.AccessToken)
		if findSiteDomainByID(sellerDomainsB, failID) != nil {
			t.Fatalf("admin-deleted domain %s still present in sellerB list: %+v", failID, sellerDomainsB)
		}
		subBInList := findSiteDomainByHost(sellerDomainsB, subHostB)
		if subBInList == nil || subBInList.Status != "ACTIVE" || !subBInList.IsPrimary {
			t.Fatalf("expected shopB subdomain %q ACTIVE+primary after admin delete, got %+v", subHostB, sellerDomainsB)
		}
	})

	// (i) Seller A deletes ACTIVE custom domain -> 204; by-host custom 404; by-host subdomain reverts primary_host; list restores SUBDOMAIN is_primary=true.
	t.Run("DeleteActiveCustomDomain_RevertsPrimaryToSubdomain", func(t *testing.T) {
		if activeID == "" {
			t.Fatal("prerequisite active domain was not created")
		}

		env.mustDoJSON(t, http.MethodDelete, "/api/v1/seller/site/domains/"+url.PathEscape(activeID), sellerA.AccessToken, nil, nil, http.StatusNoContent)

		// Custom host is no longer served.
		env.mustDoJSON(t, http.MethodGet, "/api/v1/sites/by-host?host="+url.QueryEscape(activeHost), "", nil, nil, http.StatusNotFound)

		// Subdomain still resolves 200 and primary_host has reverted to subHostA.
		bySubResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/sites/by-host?host="+url.QueryEscape(subHostA), "", nil, nil, http.StatusOK)
		var bySubOut PublicSiteResponse
		if err := DecodeJSON(bySubResp, &bySubOut); err != nil {
			t.Fatalf("decode by-host subdomain after deleting active custom domain: %v", err)
		}
		if bySubOut.Site.ID != siteA.ID || bySubOut.Site.PrimaryHost != subHostA {
			t.Fatalf("expected primary_host to revert to %q after deleting active custom domain, got %+v", subHostA, bySubOut.Site)
		}

		// Seller domain list no longer includes activeHost, and SUBDOMAIN is back to is_primary=true.
		domains := listSellerSiteDomains(t, env, sellerA.AccessToken)
		if findSiteDomainByID(domains, activeID) != nil || findSiteDomainByHost(domains, activeHost) != nil {
			t.Fatalf("deleted active domain %s (%s) still present in list: %+v", activeID, activeHost, domains)
		}
		subDom := findSiteDomainByHost(domains, subHostA)
		if subDom == nil || subDom.Kind != "SUBDOMAIN" || subDom.Status != "ACTIVE" || !subDom.IsPrimary {
			t.Fatalf("expected SUBDOMAIN %q to revert to ACTIVE+primary, got %+v", subHostA, domains)
		}
	})

	// (j) Anti-squatting: PENDING blocks for 48h, >48h PENDING is reclaimed by B (and removed from A),
	// FAILED is reclaimed immediately by B, and same site can re-add its own FAILED host without domain_limit.
	t.Run("AntiSquatting_PendingHold48h_FailedReclaim_And_SameSiteRetry", func(t *testing.T) {
		// 1. Shop A adds squatPendingHost -> PENDING; Shop B claiming same host immediately -> 409 domain_taken.
		addAResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/site/domains", sellerA.AccessToken, nil, map[string]any{
			"hostname": squatPendingHost,
		}, http.StatusCreated)
		var addAOut SiteDomainResponse
		if err := DecodeJSON(addAResp, &addAOut); err != nil {
			t.Fatalf("decode shopA add %s: %v", squatPendingHost, err)
		}
		domAID := addAOut.Domain.ID
		if domAID == "" || addAOut.Domain.Host != squatPendingHost || addAOut.Domain.Status != "PENDING" {
			t.Fatalf("unexpected shopA pending domain: %+v", addAOut.Domain)
		}

		conflictResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/site/domains", sellerB.AccessToken, nil, map[string]any{
			"hostname": squatPendingHost,
		}, http.StatusConflict)
		assertAPIErrorCode(t, conflictResp, "domain_taken")

		// 2. Backdate Shop A's PENDING domain created_at by 49 hours (>48h hold, <7d poller expiry) -> Shop B claims it and Shop A loses the row.
		dbCtx, dbCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer dbCancel()
		if err := SetDomainCreatedAtOffset(dbCtx, env.cfg.UserDatabaseURL, domAID, -49*time.Hour); err != nil {
			t.Fatalf("backdate domain %s by 49h: %v", domAID, err)
		}

		// Confirm row A is still PENDING (proves reclaim does not wait for the 7-day job).
		domainsABefore := listSellerSiteDomains(t, env, sellerA.AccessToken)
		domABefore := findSiteDomainByID(domainsABefore, domAID)
		if domABefore == nil || domABefore.Status != "PENDING" {
			t.Fatalf("expected shopA domain %s to still be PENDING at 49h, got %+v", domAID, domainsABefore)
		}

		reclaimBResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/site/domains", sellerB.AccessToken, nil, map[string]any{
			"hostname": squatPendingHost,
		}, http.StatusCreated)
		var reclaimBOut SiteDomainResponse
		if err := DecodeJSON(reclaimBResp, &reclaimBOut); err != nil {
			t.Fatalf("decode shopB reclaim %s: %v", squatPendingHost, err)
		}
		if reclaimBOut.Domain.ID == "" || reclaimBOut.Domain.ID == domAID ||
			reclaimBOut.Domain.Host != squatPendingHost || reclaimBOut.Domain.Status != "PENDING" {
			t.Fatalf("unexpected shopB reclaimed pending domain: %+v (old id=%s)", reclaimBOut.Domain, domAID)
		}

		domainsAAfter := listSellerSiteDomains(t, env, sellerA.AccessToken)
		if findSiteDomainByID(domainsAAfter, domAID) != nil || findSiteDomainByHost(domainsAAfter, squatPendingHost) != nil {
			t.Fatalf("shopA must no longer have %s after shopB reclaimed it: %+v", squatPendingHost, domainsAAfter)
		}
		domainsBAfter := listSellerSiteDomains(t, env, sellerB.AccessToken)
		if findSiteDomainByID(domainsBAfter, reclaimBOut.Domain.ID) == nil {
			t.Fatalf("shopB missing reclaimed domain %s in list: %+v", reclaimBOut.Domain.ID, domainsBAfter)
		}

		// Remove Shop B's reclaimed pending domain so Shop B can test reclaiming a FAILED host.
		env.mustDoJSON(t, http.MethodDelete, "/api/v1/seller/site/domains/"+url.PathEscape(reclaimBOut.Domain.ID), sellerB.AccessToken, nil, nil, http.StatusNoContent)

		// 3. Shop A adds squatFailHost -> check -> FAILED -> Shop B adds same host immediately (201), Shop A loses the row.
		addFailAResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/site/domains", sellerA.AccessToken, nil, map[string]any{
			"hostname": squatFailHost,
		}, http.StatusCreated)
		var addFailAOut SiteDomainResponse
		if err := DecodeJSON(addFailAResp, &addFailAOut); err != nil {
			t.Fatalf("decode shopA add %s: %v", squatFailHost, err)
		}
		failAID := addFailAOut.Domain.ID
		chkFailAResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/site/domains/"+url.PathEscape(failAID)+"/check", sellerA.AccessToken, nil, nil, http.StatusOK)
		var chkFailAOut SiteDomainResponse
		if err := DecodeJSON(chkFailAResp, &chkFailAOut); err != nil {
			t.Fatalf("decode shopA check %s: %v", squatFailHost, err)
		}
		if chkFailAOut.Domain.Status != "FAILED" {
			t.Fatalf("expected shopA %s to transition to FAILED, got %+v", squatFailHost, chkFailAOut.Domain)
		}

		reclaimFailBResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/site/domains", sellerB.AccessToken, nil, map[string]any{
			"hostname": squatFailHost,
		}, http.StatusCreated)
		var reclaimFailBOut SiteDomainResponse
		if err := DecodeJSON(reclaimFailBResp, &reclaimFailBOut); err != nil {
			t.Fatalf("decode shopB reclaim %s: %v", squatFailHost, err)
		}
		failBID := reclaimFailBOut.Domain.ID
		if failBID == "" || failBID == failAID || reclaimFailBOut.Domain.Host != squatFailHost || reclaimFailBOut.Domain.Status != "PENDING" {
			t.Fatalf("unexpected shopB domain after reclaiming FAILED host: %+v (old id=%s)", reclaimFailBOut.Domain, failAID)
		}
		if findSiteDomainByHost(listSellerSiteDomains(t, env, sellerA.AccessToken), squatFailHost) != nil {
			t.Fatalf("shopA must no longer have FAILED domain %s after shopB reclaimed it", squatFailHost)
		}

		// 4. Same-site retry of own FAILED host succeeds without hitting domain_limit.
		chkFailBResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/site/domains/"+url.PathEscape(failBID)+"/check", sellerB.AccessToken, nil, nil, http.StatusOK)
		var chkFailBOut SiteDomainResponse
		if err := DecodeJSON(chkFailBResp, &chkFailBOut); err != nil {
			t.Fatalf("decode shopB check %s: %v", squatFailHost, err)
		}
		if chkFailBOut.Domain.Status != "FAILED" {
			t.Fatalf("expected shopB %s to transition to FAILED, got %+v", squatFailHost, chkFailBOut.Domain)
		}

		retryBResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/site/domains", sellerB.AccessToken, nil, map[string]any{
			"hostname": strings.ToUpper(squatFailHost),
		}, http.StatusCreated)
		var retryBOut SiteDomainResponse
		if err := DecodeJSON(retryBResp, &retryBOut); err != nil {
			t.Fatalf("decode shopB same-site retry %s: %v", squatFailHost, err)
		}
		if retryBOut.Domain.ID == "" || retryBOut.Domain.ID == failBID ||
			retryBOut.Domain.Host != squatFailHost || retryBOut.Domain.Status != "PENDING" {
			t.Fatalf("unexpected shopB domain after same-site FAILED retry: %+v (old id=%s)", retryBOut.Domain, failBID)
		}

		env.mustDoJSON(t, http.MethodDelete, "/api/v1/seller/site/domains/"+url.PathEscape(retryBOut.Domain.ID), sellerB.AccessToken, nil, nil, http.StatusNoContent)
	})
}
