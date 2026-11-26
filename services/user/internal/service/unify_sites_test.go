package service

import (
	"context"
	"io"
	"io/fs"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/user/internal/db"
	"github.com/nexusmart/core/services/user/internal/password"
	"github.com/nexusmart/core/services/user/migrations"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

func TestUnify_SuspendedShopBlocksSaveSiteDraftAddCustomDomainAndApplyTemplate(t *testing.T) {
	cf := newSpyCFClient("shops.nexusmart.localhost")
	_, client, store, users, _ := startDomainBufconn(t, Options{
		Cloudflare: cf,
		CFMode:     "mock",
	})

	ownerID := uuid.NewString()
	opsID := uuid.NewString()
	shop := seedActiveShop(t, store, ownerID, "Shop Suspend Site Guard", ShopStatusActive)

	sellerTok, _, _, _ := users.SignAccessToken(ownerID, RoleSeller, shop.ID)
	opsTok, _, _, _ := users.SignAccessToken(opsID, RoleOps, "")

	// Seed site + active custom domain while shop is ACTIVE.
	if _, err := client.SaveSiteDraft(ctxWithBearer(t, sellerTok), &userv1.SaveSiteDraftRequest{
		Subdomain:  "suspend-guard",
		ConfigJson: sampleSiteConfigJSON(t, "Suspend Guard V1", "Hero V1"),
	}); err != nil {
		t.Fatalf("initial SaveSiteDraft: %v", err)
	}
	if _, err := client.PublishSite(ctxWithBearer(t, sellerTok), &userv1.PublishSiteRequest{Note: "v1"}); err != nil {
		t.Fatalf("initial PublishSite: %v", err)
	}
	addedDom, err := client.AddCustomDomain(ctxWithBearer(t, sellerTok), &userv1.AddCustomDomainRequest{
		Hostname: "guard.verified.test",
	})
	if err != nil {
		t.Fatalf("initial AddCustomDomain: %v", err)
	}

	// Suspend the shop.
	store.mu.Lock()
	sh := store.shops[shop.ID]
	sh.Status = ShopStatusSuspended
	store.shops[shop.ID] = sh
	store.mu.Unlock()

	ensureBefore := len(cf.ensureSubdomainCalls)
	createBefore := len(cf.createCustomCalls)

	// 1. SaveSiteDraft MUST fail with FailedPrecondition (SHOP_SUSPENDED) without mutating draft or DNS.
	if _, err := client.SaveSiteDraft(ctxWithBearer(t, sellerTok), &userv1.SaveSiteDraftRequest{
		Subdomain:  "suspend-guard-new",
		ConfigJson: sampleSiteConfigJSON(t, "Suspend Guard V2", "Hero V2"),
	}); status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "SHOP_SUSPENDED") {
		t.Fatalf("SUSPENDED shop SaveSiteDraft: want FailedPrecondition(SHOP_SUSPENDED), got %v", err)
	}
	if len(cf.ensureSubdomainCalls) != ensureBefore {
		t.Fatalf("SUSPENDED shop SaveSiteDraft must not call Cloudflare DNS")
	}
	draftAfter, err := client.GetSiteDraft(ctxWithBearer(t, sellerTok), &userv1.GetSiteDraftRequest{})
	if err != nil || draftAfter.GetSite().GetSubdomain() != "suspend-guard" || strings.Contains(draftAfter.GetSite().GetConfigJson(), "Hero V2") {
		t.Fatalf("SUSPENDED shop SaveSiteDraft mutated draft: err=%v site=%+v", err, draftAfter.GetSite())
	}

	// 2. ApplySiteTemplate MUST also fail with FailedPrecondition (SHOP_SUSPENDED).
	if _, err := client.ApplySiteTemplate(ctxWithBearer(t, sellerTok), &userv1.ApplySiteTemplateRequest{
		TemplateId: "editorial",
	}); status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "SHOP_SUSPENDED") {
		t.Fatalf("SUSPENDED shop ApplySiteTemplate: want FailedPrecondition(SHOP_SUSPENDED), got %v", err)
	}

	// 3. RemoveCustomDomain by ops (sites.domains.write) MUST still be allowed on SUSPENDED shop.
	if _, err := client.RemoveCustomDomain(ctxWithBearer(t, opsTok), &userv1.RemoveCustomDomainRequest{
		ShopId:   shop.ID,
		DomainId: addedDom.GetDomain().GetId(),
	}); err != nil {
		t.Fatalf("ops RemoveCustomDomain on SUSPENDED shop: %v", err)
	}

	// 4. AddCustomDomain MUST fail with FailedPrecondition (SHOP_SUSPENDED) and 0 Cloudflare calls.
	if _, err := client.AddCustomDomain(ctxWithBearer(t, sellerTok), &userv1.AddCustomDomainRequest{
		Hostname: "blocked-suspended.verified.test",
	}); status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "SHOP_SUSPENDED") {
		t.Fatalf("SUSPENDED shop AddCustomDomain: want FailedPrecondition(SHOP_SUSPENDED), got %v", err)
	}
	if len(cf.createCustomCalls) != createBefore {
		t.Fatalf("SUSPENDED shop AddCustomDomain must not call Cloudflare: before=%d after=%d", createBefore, len(cf.createCustomCalls))
	}

	// 5. Unsuspend shop -> SaveSiteDraft, ApplySiteTemplate, and AddCustomDomain succeed again.
	store.mu.Lock()
	sh = store.shops[shop.ID]
	sh.Status = ShopStatusActive
	store.shops[shop.ID] = sh
	store.mu.Unlock()

	if _, err := client.SaveSiteDraft(ctxWithBearer(t, sellerTok), &userv1.SaveSiteDraftRequest{
		Subdomain:  "suspend-guard",
		ConfigJson: sampleSiteConfigJSON(t, "Suspend Guard V2", "Hero V2"),
	}); err != nil {
		t.Fatalf("unsuspended SaveSiteDraft: %v", err)
	}
	if _, err := client.AddCustomDomain(ctxWithBearer(t, sellerTok), &userv1.AddCustomDomainRequest{
		Hostname: "restored.verified.test",
	}); err != nil {
		t.Fatalf("unsuspended AddCustomDomain: %v", err)
	}
	if _, err := client.ApplySiteTemplate(ctxWithBearer(t, sellerTok), &userv1.ApplySiteTemplateRequest{
		TemplateId: "editorial",
	}); err != nil {
		t.Fatalf("unsuspended ApplySiteTemplate: %v", err)
	}
}

func TestUnify_SiteAdminCapabilityMatrixAllRoles(t *testing.T) {
	srv, store, _, pub, _ := newTestHarness(t)

	allRoles := []string{
		RoleSuperAdmin,
		RoleAdmin,
		RoleModerator,
		RoleCSKH,
		RoleAccountant,
		RoleOps,
		RoleBuyer,
		RoleSeller,
	}
	actorIDs := make(map[string]string, len(allRoles))
	for i, role := range allRoles {
		phone := "09088000" + string(rune('1'+i)) + "0"
		actorIDs[role] = seedUserWithRole(t, srv, store, pub, "site-cap-"+role+"@example.com", phone, role)
	}

	ownerID := uuid.NewString()
	shop := seedActiveShop(t, store, ownerID, "Site Cap Matrix Shop", ShopStatusActive)
	ownerCtx := ctxWithUser(ownerID, RoleSeller, shop.ID)

	saveResp, err := srv.SaveSiteDraft(ownerCtx, &userv1.SaveSiteDraftRequest{
		Subdomain:  "site-cap-matrix",
		ConfigJson: sampleSiteConfigJSON(t, "Cap Matrix", "Hero"),
	})
	if err != nil {
		t.Fatalf("seed SaveSiteDraft: %v", err)
	}
	pubResp, err := srv.PublishSite(ownerCtx, &userv1.PublishSiteRequest{Note: "v1"})
	if err != nil {
		t.Fatalf("seed PublishSite: %v", err)
	}
	siteID := saveResp.GetSite().GetId()
	verID := pubResp.GetVersion().GetId()

	type capCase struct {
		name       string
		allowRole  func(role string) bool
		invoke     func(ctx context.Context, tag string) error
		setupAfter func()
	}

	cases := []capCase{
		{
			name:      "ListSites",
			allowRole: func(r string) bool { return auth.RoleHasCapability(r, auth.CapSitesRead) },
			invoke: func(ctx context.Context, _ string) error {
				_, err := srv.ListSites(ctx, &userv1.ListSitesRequest{Page: 1, PageSize: 10})
				return err
			},
		},
		{
			name:      "GetSiteDraft_Admin",
			allowRole: func(r string) bool { return auth.RoleHasCapability(r, auth.CapSitesRead) },
			invoke: func(ctx context.Context, _ string) error {
				_, err := srv.GetSiteDraft(ctx, &userv1.GetSiteDraftRequest{ShopId: shop.ID})
				return err
			},
		},
		{
			name:      "ListSiteVersions_Admin",
			allowRole: func(r string) bool { return auth.RoleHasCapability(r, auth.CapSitesRead) },
			invoke: func(ctx context.Context, _ string) error {
				_, err := srv.ListSiteVersions(ctx, &userv1.ListSiteVersionsRequest{ShopId: shop.ID, Page: 1, PageSize: 10})
				return err
			},
		},
		{
			name:      "ListSiteDomains_Admin",
			allowRole: func(r string) bool { return auth.RoleHasCapability(r, auth.CapSitesRead) },
			invoke: func(ctx context.Context, _ string) error {
				_, err := srv.ListSiteDomains(ctx, &userv1.ListSiteDomainsRequest{ShopId: shop.ID})
				return err
			},
		},
		{
			name:      "SuspendSite",
			allowRole: func(r string) bool { return auth.RoleHasCapability(r, auth.CapSitesSuspend) },
			invoke: func(ctx context.Context, _ string) error {
				_, err := srv.SuspendSite(ctx, &userv1.SuspendSiteRequest{
					SiteId:  siteID,
					Suspend: true,
					Reason:  "matrix suspend",
				})
				return err
			},
			setupAfter: func() {
				_, _ = srv.SuspendSite(ctxWithUser(actorIDs[RoleSuperAdmin], RoleSuperAdmin, ""), &userv1.SuspendSiteRequest{
					SiteId:  siteID,
					Suspend: false,
				})
			},
		},
		{
			name:      "AddCheckRemoveCustomDomain_Admin",
			allowRole: func(r string) bool { return auth.RoleHasCapability(r, auth.CapSitesDomainsWrite) },
			invoke: func(ctx context.Context, tag string) error {
				label := strings.ReplaceAll(strings.ToLower(tag), "_", "-")
				addResp, err := srv.AddCustomDomain(ctx, &userv1.AddCustomDomainRequest{
					ShopId:   shop.ID,
					Hostname: "cap-" + label + ".verified.test",
				})
				if err != nil {
					return err
				}
				domID := addResp.GetDomain().GetId()
				if _, err := srv.CheckCustomDomain(ctx, &userv1.CheckCustomDomainRequest{
					ShopId:   shop.ID,
					DomainId: domID,
				}); err != nil {
					return err
				}
				_, err = srv.RemoveCustomDomain(ctx, &userv1.RemoveCustomDomainRequest{
					ShopId:   shop.ID,
					DomainId: domID,
				})
				return err
			},
		},
		{
			name:      "SaveSiteDraft_NonOwnerStaff",
			allowRole: func(r string) bool { return r == RoleSuperAdmin || r == RoleAdmin },
			invoke: func(ctx context.Context, _ string) error {
				_, err := srv.SaveSiteDraft(ctx, &userv1.SaveSiteDraftRequest{
					ShopId:     shop.ID,
					Subdomain:  "site-cap-matrix",
					ConfigJson: sampleSiteConfigJSON(t, "Cap Matrix", "Hero"),
				})
				return err
			},
		},
		{
			name:      "PublishSite_NonOwnerStaff",
			allowRole: func(r string) bool { return r == RoleSuperAdmin || r == RoleAdmin },
			invoke: func(ctx context.Context, _ string) error {
				_, err := srv.PublishSite(ctx, &userv1.PublishSiteRequest{ShopId: shop.ID})
				return err
			},
		},
		{
			name:      "RollbackSite_NonOwnerStaff",
			allowRole: func(r string) bool { return r == RoleSuperAdmin || r == RoleAdmin },
			invoke: func(ctx context.Context, _ string) error {
				_, err := srv.RollbackSite(ctx, &userv1.RollbackSiteRequest{ShopId: shop.ID, VersionId: verID})
				return err
			},
		},
		{
			name:      "ApplySiteTemplate_NonOwnerStaff",
			allowRole: func(r string) bool { return r == RoleSuperAdmin || r == RoleAdmin },
			invoke: func(ctx context.Context, _ string) error {
				_, err := srv.ApplySiteTemplate(ctx, &userv1.ApplySiteTemplateRequest{ShopId: shop.ID, TemplateId: "editorial"})
				return err
			},
		},
	}

	for _, tc := range cases {
		for _, role := range allRoles {
			wantAllowed := tc.allowRole(role)
			ctx := ctxWithUser(actorIDs[role], role, "")
			err := tc.invoke(ctx, role)
			if wantAllowed {
				if err != nil {
					t.Errorf("%s as %s: want OK, got %v", tc.name, role, err)
				}
				if tc.setupAfter != nil {
					tc.setupAfter()
				}
			} else if status.Code(err) != codes.PermissionDenied {
				t.Errorf("%s as %s: want PermissionDenied, got %v", tc.name, role, err)
			}
		}

		// Service tokens MUST be rejected on every site/domain RPC.
		if err := tc.invoke(ctxWithService("gateway-service"), "svc"); status.Code(err) != codes.PermissionDenied {
			t.Errorf("%s with service token: want PermissionDenied, got %v", tc.name, err)
		}
	}
}

func TestUnify_SiteDomainTemplateAuditLogsRecordClientIP(t *testing.T) {
	srv, store, _, _, _ := newTestHarness(t)

	ownerID := uuid.NewString()
	modID := uuid.NewString()
	shop := seedActiveShop(t, store, ownerID, "Audit IP Site Shop", ShopStatusActive)

	const wantClientIP = "203.0.113.42"
	sellerCtx := ctxWithClientIP(ctxWithUser(ownerID, RoleSeller, shop.ID), wantClientIP)
	modCtx := ctxWithClientIP(ctxWithUser(modID, RoleModerator, ""), wantClientIP)

	saveResp, err := srv.SaveSiteDraft(sellerCtx, &userv1.SaveSiteDraftRequest{
		Subdomain:  "audit-ip-shop",
		ConfigJson: sampleSiteConfigJSON(t, "Audit IP", "Hero"),
	})
	if err != nil {
		t.Fatalf("SaveSiteDraft: %v", err)
	}
	pubResp, err := srv.PublishSite(sellerCtx, &userv1.PublishSiteRequest{Note: "v1"})
	if err != nil {
		t.Fatalf("PublishSite: %v", err)
	}
	if _, err := srv.RollbackSite(sellerCtx, &userv1.RollbackSiteRequest{
		VersionId: pubResp.GetVersion().GetId(),
		Note:      "rb",
	}); err != nil {
		t.Fatalf("RollbackSite: %v", err)
	}
	if _, err := srv.ApplySiteTemplate(sellerCtx, &userv1.ApplySiteTemplateRequest{
		TemplateId: "editorial",
	}); err != nil {
		t.Fatalf("ApplySiteTemplate: %v", err)
	}
	addDom, err := srv.AddCustomDomain(sellerCtx, &userv1.AddCustomDomainRequest{
		Hostname: "audit-ip.verified.test",
	})
	if err != nil {
		t.Fatalf("AddCustomDomain: %v", err)
	}
	if _, err := srv.CheckCustomDomain(sellerCtx, &userv1.CheckCustomDomainRequest{
		DomainId: addDom.GetDomain().GetId(),
	}); err != nil {
		t.Fatalf("CheckCustomDomain: %v", err)
	}
	if _, err := srv.RemoveCustomDomain(sellerCtx, &userv1.RemoveCustomDomainRequest{
		DomainId: addDom.GetDomain().GetId(),
	}); err != nil {
		t.Fatalf("RemoveCustomDomain: %v", err)
	}
	if _, err := srv.SuspendSite(modCtx, &userv1.SuspendSiteRequest{
		SiteId:  saveResp.GetSite().GetId(),
		Suspend: true,
		Reason:  "audit ip test",
	}); err != nil {
		t.Fatalf("SuspendSite: %v", err)
	}

	wantActions := map[string]bool{
		"site.save_draft":       false,
		"site.publish":          false,
		"site.rollback":         false,
		"site.apply_template":   false,
		"site.domain.add":       false,
		"site.domain.activated": false,
		"site.domain.remove":    false,
		"admin.site.suspend":    false,
	}
	store.mu.Lock()
	for _, entry := range store.auditLogs {
		if _, tracked := wantActions[entry.Action]; !tracked {
			continue
		}
		if entry.IPAddress != wantClientIP {
			store.mu.Unlock()
			t.Fatalf("audit action %q recorded IPAddress=%q, want %q", entry.Action, entry.IPAddress, wantClientIP)
		}
		wantActions[entry.Action] = true
	}
	store.mu.Unlock()

	for action, seen := range wantActions {
		if !seen {
			t.Errorf("missing expected audit action %q", action)
		}
	}

	// Untrusted public peer spoofing x-client-ip MUST fail closed to peer IP.
	untrustedCtx := peer.NewContext(ctxWithUser(modID, RoleModerator, ""), &peer.Peer{
		Addr: &net.TCPAddr{IP: net.ParseIP("198.51.100.9"), Port: 54321},
	})
	untrustedCtx = metadata.NewIncomingContext(untrustedCtx, metadata.Pairs("x-client-ip", "1.2.3.4"))
	if _, err := srv.SuspendSite(untrustedCtx, &userv1.SuspendSiteRequest{
		SiteId:  saveResp.GetSite().GetId(),
		Suspend: false,
	}); err != nil {
		t.Fatalf("SuspendSite (unsuspend from untrusted peer): %v", err)
	}
	store.mu.Lock()
	last := store.auditLogs[len(store.auditLogs)-1]
	store.mu.Unlock()
	if last.Action != "admin.site.unsuspend" || last.IPAddress != "198.51.100.9" {
		t.Fatalf("untrusted peer x-client-ip spoof must fall back to peer IP: got action=%q ip=%q", last.Action, last.IPAddress)
	}
}

func TestUnify_MigrationsLexicalOrderAndShopSlugBackfillSQL(t *testing.T) {
	entries, err := fs.ReadDir(migrations.Files, ".")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	var got []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			got = append(got, e.Name())
		}
	}
	sort.Strings(got)

	want := []string{
		"0001_init.sql",
		"0002_sites.sql",
		"0003_domains_cloudflare.sql",
		"0004_site_templates.sql",
		"0005_add_shop_slug.sql",
		"0006_cod_rejections.sql",
		"0007_password_resets_and_lock_reason.sql",
		"0008_shop_bank_accounts.sql",
		"0009_shop_verified_owner_name_and_otp_publish.sql",
		"0010_shop_bank_account_encryption.sql",
		"0011_phone_canonical_verified_unique.sql",
		"0012_governance_approvals_activity.sql",
		"0013_pickup_address_guard_index.sql",
	}
	if len(got) != len(want) {
		t.Fatalf("migration count = %d, want %d; got=%v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("migration[%d] = %q, want %q (all=%v)", i, got[i], want[i], got)
		}
	}

	slugSQLBytes, err := fs.ReadFile(migrations.Files, "0005_add_shop_slug.sql")
	if err != nil {
		t.Fatalf("read 0005_add_shop_slug.sql: %v", err)
	}
	slugSQL := string(slugSQLBytes)
	for _, token := range []string{"FROM sites", "subdomain", "LOWER(sh.id::text)", "idx_shops_slug_nonempty"} {
		if !strings.Contains(slugSQL, token) {
			t.Errorf("0005_add_shop_slug.sql missing expected backfill/index clause %q", token)
		}
	}
}

func TestPG_Unify_Phase0BUpgradeAndShopSlugBackfill(t *testing.T) {
	if strings.TrimSpace(os.Getenv("TEST_USER_DB_URL")) == "" {
		t.Skip("set TEST_USER_DB_URL to run real Postgres integration tests")
	}

	ctx := context.Background()
	pool, _ := openTempUserPGPool(t)

	if _, err := pool.Exec(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
	version TEXT PRIMARY KEY,
	applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	phase0bVersions := []string{
		"0001_init.sql",
		"0002_sites.sql",
		"0003_domains_cloudflare.sql",
		"0004_site_templates.sql",
	}
	for _, v := range phase0bVersions {
		sqlBytes, err := fs.ReadFile(migrations.Files, v)
		if err != nil {
			t.Fatalf("read %s: %v", v, err)
		}
		if _, err := pool.Exec(ctx, string(sqlBytes)); err != nil {
			t.Fatalf("exec %s: %v", v, err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, v); err != nil {
			t.Fatalf("record %s: %v", v, err)
		}
	}

	// Seed two phase-0b shops: one with a site subdomain, one without a site.
	owner1, shopWithSite := uuid.NewString(), uuid.NewString()
	owner2, shopWithoutSite := uuid.NewString(), uuid.NewString()
	for _, pair := range []struct {
		uid, sid, email, name string
	}{
		{owner1, shopWithSite, "p0b-site@example.com", "Shop Co Site"},
		{owner2, shopWithoutSite, "p0b-nosite@example.com", "Shop Khong Site"},
	} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO users (id, full_name, email, phone, password_hash, role, status)
			VALUES ($1, 'Owner', $2, '', 'hash', 'seller', 'ACTIVE')
		`, pair.uid, pair.email); err != nil {
			t.Fatalf("seed user %s: %v", pair.email, err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO shops (id, owner_user_id, name, status)
			VALUES ($1, $2, $3, 'ACTIVE')
		`, pair.sid, pair.uid, pair.name); err != nil {
			t.Fatalf("seed shop %s: %v", pair.name, err)
		}
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO sites (id, shop_id, subdomain, status, draft_config)
		VALUES ($1, $2, 'xuong-bach-dang', 'DRAFT', '{}'::jsonb)
	`, uuid.NewString(), shopWithSite); err != nil {
		t.Fatalf("seed site: %v", err)
	}

	// Upgrade phase-0b DB through 0005..0013 and verify idempotent second run.
	if err := db.RunMigrations(ctx, pool); err != nil {
		t.Fatalf("RunMigrations upgrade from phase-0b: %v", err)
	}
	if err := db.RunMigrations(ctx, pool); err != nil {
		t.Fatalf("RunMigrations idempotent rerun: %v", err)
	}

	var gotWithSite, gotWithoutSite string
	if err := pool.QueryRow(ctx, `SELECT slug FROM shops WHERE id = $1`, shopWithSite).Scan(&gotWithSite); err != nil {
		t.Fatalf("query shopWithSite slug: %v", err)
	}
	if gotWithSite != "xuong-bach-dang" {
		t.Fatalf("shopWithSite slug = %q, want %q", gotWithSite, "xuong-bach-dang")
	}
	if err := pool.QueryRow(ctx, `SELECT slug FROM shops WHERE id = $1`, shopWithoutSite).Scan(&gotWithoutSite); err != nil {
		t.Fatalf("query shopWithoutSite slug: %v", err)
	}
	if gotWithoutSite != strings.ToLower(shopWithoutSite) {
		t.Fatalf("shopWithoutSite slug = %q, want %q", gotWithoutSite, strings.ToLower(shopWithoutSite))
	}
}

type lockOrderSpyStore struct {
	*memStore
	mu     sync.Mutex
	events []string
}

type lockOrderSpyQuerier struct {
	db.Querier
	parent *lockOrderSpyStore
}

func (q *lockOrderSpyQuerier) GetShopByIDForUpdate(ctx context.Context, id string) (db.Shop, error) {
	q.parent.mu.Lock()
	q.parent.events = append(q.parent.events, "shop")
	q.parent.mu.Unlock()
	return q.Querier.GetShopByIDForUpdate(ctx, id)
}

func (q *lockOrderSpyQuerier) GetSiteByShopIDForUpdate(ctx context.Context, shopID string) (db.Site, error) {
	q.parent.mu.Lock()
	q.parent.events = append(q.parent.events, "site")
	q.parent.mu.Unlock()
	return q.Querier.GetSiteByShopIDForUpdate(ctx, shopID)
}

func (q *lockOrderSpyQuerier) GetSiteByIDForUpdate(ctx context.Context, id string) (db.Site, error) {
	q.parent.mu.Lock()
	q.parent.events = append(q.parent.events, "site")
	q.parent.mu.Unlock()
	return q.Querier.GetSiteByIDForUpdate(ctx, id)
}

func (s *lockOrderSpyStore) WithTx(ctx context.Context, fn func(q db.Querier) error) error {
	return s.memStore.WithTx(ctx, func(q db.Querier) error {
		return fn(&lockOrderSpyQuerier{Querier: q, parent: s})
	})
}

func (s *lockOrderSpyStore) resetLockEvents() {
	s.mu.Lock()
	s.events = nil
	s.mu.Unlock()
}

func (s *lockOrderSpyStore) lockEvents() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.events))
	copy(out, s.events)
	return out
}

func newLockOrderTestServer(t *testing.T, cf *spyCFClient) (*Server, *lockOrderSpyStore) {
	t.Helper()
	base := newMemStore()
	spy := &lockOrderSpyStore{memStore: base}
	sec := newMemSecurity()
	pub := &memPublisher{}
	hasher := password.NewDefaultHasher()
	authMgr, err := auth.NewManager("lock-order-test-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	log := logger.New("user-lock-order-test", logger.WithWriter(io.Discard))
	srv := NewServer(spy, sec, hasher, authMgr, pub, log, Options{
		Cloudflare: cf,
		CFMode:     "mock",
	})
	return srv, spy
}

func TestRollbackSite_RejectsSuspendedAndNonActiveShop(t *testing.T) {
	cf := newSpyCFClient("shops.nexusmart.localhost")
	srv, spy := newLockOrderTestServer(t, cf)

	ownerID := uuid.NewString()
	shop := seedActiveShop(t, spy.memStore, ownerID, "Rollback Guard Shop", ShopStatusActive)
	sellerCtx := ctxWithUser(ownerID, RoleSeller, shop.ID)

	if _, err := srv.SaveSiteDraft(sellerCtx, &userv1.SaveSiteDraftRequest{
		Subdomain:  "rb-guard",
		ConfigJson: sampleSiteConfigJSON(t, "RB V1", "Hero V1"),
	}); err != nil {
		t.Fatalf("SaveSiteDraft v1: %v", err)
	}
	pub1, err := srv.PublishSite(sellerCtx, &userv1.PublishSiteRequest{Note: "v1"})
	if err != nil {
		t.Fatalf("PublishSite v1: %v", err)
	}
	if _, err := srv.SaveSiteDraft(sellerCtx, &userv1.SaveSiteDraftRequest{
		Subdomain:  "rb-guard",
		ConfigJson: sampleSiteConfigJSON(t, "RB V2", "Hero V2"),
	}); err != nil {
		t.Fatalf("SaveSiteDraft v2: %v", err)
	}
	pub2, err := srv.PublishSite(sellerCtx, &userv1.PublishSiteRequest{Note: "v2"})
	if err != nil {
		t.Fatalf("PublishSite v2: %v", err)
	}

	// Suspend shop while site stays PUBLISHED.
	spy.mu.Lock()
	spy.memStore.mu.Lock()
	sh := spy.memStore.shops[shop.ID]
	sh.Status = ShopStatusSuspended
	spy.memStore.shops[shop.ID] = sh
	spy.memStore.mu.Unlock()
	spy.mu.Unlock()

	spy.resetLockEvents()
	_, err = srv.RollbackSite(sellerCtx, &userv1.RollbackSiteRequest{
		VersionId: pub1.GetVersion().GetId(),
		Note:      "blocked rollback",
	})
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "SHOP_SUSPENDED") {
		t.Fatalf("SUSPENDED shop RollbackSite: want FailedPrecondition(SHOP_SUSPENDED), got %v", err)
	}
	// Must lock shops BEFORE sites (and stop before touching sites when shop is suspended).
	locks := spy.lockEvents()
	if len(locks) == 0 || locks[0] != "shop" {
		t.Fatalf("RollbackSite must lock shops before sites in WithTx, got lock order=%v", locks)
	}

	// Verify neither published_version_id nor draft_config mutated.
	stAfter, err := spy.GetSiteByShopID(context.Background(), shop.ID)
	if err != nil {
		t.Fatalf("GetSiteByShopID: %v", err)
	}
	if stAfter.PublishedVersionID == nil || *stAfter.PublishedVersionID != pub2.GetVersion().GetId() || !strings.Contains(string(stAfter.DraftConfig), "Hero V2") {
		t.Fatalf("SUSPENDED shop RollbackSite mutated site: pubVer=%v draft=%s", stAfter.PublishedVersionID, string(stAfter.DraftConfig))
	}

	// Non-ACTIVE (PENDING) shop must fail with FailedPrecondition(SHOP_NOT_ACTIVE).
	spy.memStore.mu.Lock()
	sh = spy.memStore.shops[shop.ID]
	sh.Status = ShopStatusPending
	spy.memStore.shops[shop.ID] = sh
	spy.memStore.mu.Unlock()

	if _, err := srv.RollbackSite(sellerCtx, &userv1.RollbackSiteRequest{
		VersionId: pub1.GetVersion().GetId(),
	}); status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "SHOP_NOT_ACTIVE") {
		t.Fatalf("PENDING shop RollbackSite: want FailedPrecondition(SHOP_NOT_ACTIVE), got %v", err)
	}

	// Active shop succeeds and locks shops before sites.
	spy.memStore.mu.Lock()
	sh = spy.memStore.shops[shop.ID]
	sh.Status = ShopStatusActive
	spy.memStore.shops[shop.ID] = sh
	spy.memStore.mu.Unlock()

	spy.resetLockEvents()
	if _, err := srv.RollbackSite(sellerCtx, &userv1.RollbackSiteRequest{
		VersionId: pub1.GetVersion().GetId(),
		Note:      "ok rollback",
	}); err != nil {
		t.Fatalf("ACTIVE shop RollbackSite: %v", err)
	}
	locks = spy.lockEvents()
	if len(locks) < 2 || locks[0] != "shop" || locks[1] != "site" {
		t.Fatalf("ACTIVE RollbackSite must lock [shop, site], got %v", locks)
	}
}

func TestPublishSite_SuspendedShopErrorCodeAndTOCTOULockOrder(t *testing.T) {
	cf := newSpyCFClient("shops.nexusmart.localhost")
	srv, spy := newLockOrderTestServer(t, cf)

	ownerID := uuid.NewString()
	shop := seedActiveShop(t, spy.memStore, ownerID, "Publish TOCTOU Shop", ShopStatusActive)
	sellerCtx := ctxWithUser(ownerID, RoleSeller, shop.ID)

	if _, err := srv.SaveSiteDraft(sellerCtx, &userv1.SaveSiteDraftRequest{
		Subdomain:  "pub-toctou",
		ConfigJson: sampleSiteConfigJSON(t, "Pub V1", "Hero V1"),
	}); err != nil {
		t.Fatalf("SaveSiteDraft: %v", err)
	}

	// 1. Pre-suspended shop must return SHOP_SUSPENDED (same code as SaveSiteDraft/AddCustomDomain/ApplySiteTemplate) with 0 CF calls.
	spy.memStore.mu.Lock()
	sh := spy.memStore.shops[shop.ID]
	sh.Status = ShopStatusSuspended
	spy.memStore.shops[shop.ID] = sh
	spy.memStore.mu.Unlock()

	if _, err := srv.PublishSite(sellerCtx, &userv1.PublishSiteRequest{Note: "blocked"}); status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "SHOP_SUSPENDED") {
		t.Fatalf("SUSPENDED shop PublishSite: want FailedPrecondition(SHOP_SUSPENDED), got %v", err)
	}
	if len(cf.ensureSubdomainCalls) != 0 {
		t.Fatalf("pre-suspended PublishSite must not call Cloudflare DNS")
	}

	// 2. TOCTOU race: shop is ACTIVE at pre-check, gets SUSPENDED during Cloudflare EnsureSubdomainRecord before WithTx.
	spy.memStore.mu.Lock()
	sh = spy.memStore.shops[shop.ID]
	sh.Status = ShopStatusActive
	spy.memStore.shops[shop.ID] = sh
	spy.memStore.mu.Unlock()

	cf.mu.Lock()
	cf.onEnsureSubdomain = func(_ string) {
		spy.memStore.mu.Lock()
		cur := spy.memStore.shops[shop.ID]
		cur.Status = ShopStatusSuspended
		spy.memStore.shops[shop.ID] = cur
		spy.memStore.mu.Unlock()
	}
	cf.mu.Unlock()

	spy.resetLockEvents()
	_, err := srv.PublishSite(sellerCtx, &userv1.PublishSiteRequest{Note: "race"})
	cf.mu.Lock()
	cf.onEnsureSubdomain = nil
	cf.mu.Unlock()
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "SHOP_SUSPENDED") {
		t.Fatalf("TOCTOU suspended PublishSite: want FailedPrecondition(SHOP_SUSPENDED), got %v", err)
	}
	locks := spy.lockEvents()
	if len(locks) == 0 || locks[0] != "shop" {
		t.Fatalf("PublishSite must lock shops before sites in WithTx, got %v", locks)
	}
	stAfter, err := spy.GetSiteByShopID(context.Background(), shop.ID)
	if err != nil {
		t.Fatalf("GetSiteByShopID: %v", err)
	}
	if stAfter.Status != SiteStatusDraft || stAfter.PublishedVersionID != nil {
		t.Fatalf("TOCTOU suspended PublishSite must not publish site, got status=%s pubVer=%v", stAfter.Status, stAfter.PublishedVersionID)
	}

	// 3. Unsuspend shop -> PublishSite succeeds and locks [shop, site].
	spy.memStore.mu.Lock()
	sh = spy.memStore.shops[shop.ID]
	sh.Status = ShopStatusActive
	spy.memStore.shops[shop.ID] = sh
	spy.memStore.mu.Unlock()

	spy.resetLockEvents()
	if _, err := srv.PublishSite(sellerCtx, &userv1.PublishSiteRequest{Note: "ok"}); err != nil {
		t.Fatalf("ACTIVE PublishSite: %v", err)
	}
	locks = spy.lockEvents()
	if len(locks) < 2 || locks[0] != "shop" || locks[1] != "site" {
		t.Fatalf("ACTIVE PublishSite must lock [shop, site], got %v", locks)
	}
}

func TestCheckCustomDomain_RejectsSuspendedShopAndTOCTOU(t *testing.T) {
	cf := newSpyCFClient("shops.nexusmart.localhost")
	srv, spy := newLockOrderTestServer(t, cf)

	ownerID := uuid.NewString()
	shop := seedActiveShop(t, spy.memStore, ownerID, "Check Domain Guard Shop", ShopStatusActive)
	sellerCtx := ctxWithUser(ownerID, RoleSeller, shop.ID)

	if _, err := srv.SaveSiteDraft(sellerCtx, &userv1.SaveSiteDraftRequest{
		Subdomain:  "chk-guard",
		ConfigJson: sampleSiteConfigJSON(t, "Chk", "Hero"),
	}); err != nil {
		t.Fatalf("SaveSiteDraft: %v", err)
	}
	if _, err := srv.PublishSite(sellerCtx, &userv1.PublishSiteRequest{}); err != nil {
		t.Fatalf("PublishSite: %v", err)
	}
	added, err := srv.AddCustomDomain(sellerCtx, &userv1.AddCustomDomainRequest{
		Hostname: "chk-guard.verified.test",
	})
	if err != nil {
		t.Fatalf("AddCustomDomain: %v", err)
	}
	domID := added.GetDomain().GetId()

	// 1. Pre-suspended shop -> FailedPrecondition(SHOP_SUSPENDED) with 0 Cloudflare calls.
	spy.memStore.mu.Lock()
	sh := spy.memStore.shops[shop.ID]
	sh.Status = ShopStatusSuspended
	spy.memStore.shops[shop.ID] = sh
	spy.memStore.mu.Unlock()

	getBefore := len(cf.getCustomCalls)
	if _, err := srv.CheckCustomDomain(sellerCtx, &userv1.CheckCustomDomainRequest{
		DomainId: domID,
	}); status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "SHOP_SUSPENDED") {
		t.Fatalf("SUSPENDED shop CheckCustomDomain: want FailedPrecondition(SHOP_SUSPENDED), got %v", err)
	}
	if len(cf.getCustomCalls) != getBefore {
		t.Fatalf("SUSPENDED shop CheckCustomDomain must not call Cloudflare")
	}

	// 2. TOCTOU: shop suspended while GetCustomHostname is in-flight -> WithTx locks shop before site and rejects without activating domain.
	spy.memStore.mu.Lock()
	sh = spy.memStore.shops[shop.ID]
	sh.Status = ShopStatusActive
	spy.memStore.shops[shop.ID] = sh
	spy.memStore.mu.Unlock()

	cf.mu.Lock()
	cf.onGetCustom = func(_, _ string) {
		spy.memStore.mu.Lock()
		cur := spy.memStore.shops[shop.ID]
		cur.Status = ShopStatusSuspended
		spy.memStore.shops[shop.ID] = cur
		spy.memStore.mu.Unlock()
	}
	cf.mu.Unlock()

	spy.resetLockEvents()
	_, err = srv.CheckCustomDomain(sellerCtx, &userv1.CheckCustomDomainRequest{DomainId: domID})
	cf.mu.Lock()
	cf.onGetCustom = nil
	cf.mu.Unlock()
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "SHOP_SUSPENDED") {
		t.Fatalf("TOCTOU CheckCustomDomain: want FailedPrecondition(SHOP_SUSPENDED), got %v", err)
	}
	locks := spy.lockEvents()
	if len(locks) == 0 || locks[0] != "shop" {
		t.Fatalf("CheckCustomDomain must lock shop before site in WithTx, got %v", locks)
	}
	domAfter, err := spy.GetDomainByID(context.Background(), domID)
	if err != nil {
		t.Fatalf("GetDomainByID: %v", err)
	}
	if domAfter.Status != DomainStatusPending || domAfter.IsPrimary {
		t.Fatalf("TOCTOU suspended CheckCustomDomain must not activate domain, got status=%s primary=%v", domAfter.Status, domAfter.IsPrimary)
	}
}
