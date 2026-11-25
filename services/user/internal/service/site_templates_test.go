package service

import (
	"context"
	"encoding/json"
	"io"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/user/internal/db"
	"github.com/nexusmart/core/services/user/internal/site"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Keep memStore template storage in a per-instance map so server_test.go needs zero edits.
type memTemplateBucket struct {
	mu        sync.Mutex
	templates map[string]db.SiteTemplate
}

var memStoreTemplates sync.Map // map[*memStore]*memTemplateBucket

func (m *memStore) templateBucket() *memTemplateBucket {
	if v, ok := memStoreTemplates.Load(m); ok {
		return v.(*memTemplateBucket)
	}
	b := &memTemplateBucket{templates: make(map[string]db.SiteTemplate)}
	actual, _ := memStoreTemplates.LoadOrStore(m, b)
	return actual.(*memTemplateBucket)
}

func (m *memStore) ListActiveSiteTemplates(_ context.Context) ([]db.SiteTemplate, error) {
	b := m.templateBucket()
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]db.SiteTemplate, 0, len(b.templates))
	for _, t := range b.templates {
		if t.IsActive {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SortOrder != out[j].SortOrder {
			return out[i].SortOrder < out[j].SortOrder
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (m *memStore) GetSiteTemplateByID(_ context.Context, idOrSlug string) (db.SiteTemplate, error) {
	b := m.templateBucket()
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, t := range b.templates {
		if !t.IsActive {
			continue
		}
		if t.ID == idOrSlug || strings.EqualFold(t.Slug, idOrSlug) {
			return t, nil
		}
	}
	return db.SiteTemplate{}, pgx.ErrNoRows
}

func (m *memStore) UpsertSiteTemplate(_ context.Context, arg db.UpsertSiteTemplateParams) (db.SiteTemplate, error) {
	b := m.templateBucket()
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now().UTC()
	createdAt := now
	if prev, ok := b.templates[arg.ID]; ok {
		createdAt = prev.CreatedAt
	}
	tags := make([]string, len(arg.Tags))
	copy(tags, arg.Tags)
	rec := db.SiteTemplate{
		ID:              arg.ID,
		Slug:            arg.Slug,
		Name:            arg.Name,
		Category:        arg.Category,
		Archetype:       arg.Archetype,
		Description:     arg.Description,
		PreviewImageUrl: arg.PreviewImageUrl,
		Tags:            tags,
		Config:          append([]byte(nil), arg.Config...),
		IsActive:        arg.IsActive,
		SortOrder:       arg.SortOrder,
		CreatedAt:       createdAt,
		UpdatedAt:       now,
	}
	b.templates[rec.ID] = rec
	return rec, nil
}

func (m *memStore) UpdateSiteTemplateID(_ context.Context, arg db.UpdateSiteTemplateIDParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.sites[arg.ID]
	if !ok {
		return pgx.ErrNoRows
	}
	st.TemplateID = arg.TemplateID
	st.UpdatedAt = time.Now().UTC()
	m.sites[st.ID] = st
	return nil
}

func TestSiteTemplates_ListGetApplyRBACBrandAndDraftPreservation(t *testing.T) {
	client, store, users, svcMgr := startSiteBufconn(t)
	log := logger.New("user-templates-test", logger.WithWriter(io.Discard))

	if err := BootstrapSiteTemplates(context.Background(), store, log); err != nil {
		t.Fatalf("BootstrapSiteTemplates: %v", err)
	}
	// Re-running bootstrap must be idempotent.
	if err := BootstrapSiteTemplates(context.Background(), store, log); err != nil {
		t.Fatalf("BootstrapSiteTemplates idempotent rerun: %v", err)
	}

	// 1. Public ListSiteTemplates and GetSiteTemplate require no JWT and return 8 valid templates.
	listResp, err := client.ListSiteTemplates(context.Background(), &userv1.ListSiteTemplatesRequest{})
	if err != nil {
		t.Fatalf("ListSiteTemplates: %v", err)
	}
	if len(listResp.GetTemplates()) != 8 {
		t.Fatalf("expected 8 templates, got %d", len(listResp.GetTemplates()))
	}

	getResp, err := client.GetSiteTemplate(context.Background(), &userv1.GetSiteTemplateRequest{Id: "craft-atelier"})
	if err != nil {
		t.Fatalf("GetSiteTemplate(craft-atelier): %v", err)
	}
	if getResp.GetTemplate().GetArchetype() != "craft-atelier" {
		t.Fatalf("unexpected template archetype: %+v", getResp.GetTemplate())
	}
	if _, _, err := site.ValidateAndCanonicalizeConfigV2([]byte(getResp.GetTemplate().GetConfigJson())); err != nil {
		t.Fatalf("returned template config_json invalid v2: %v", err)
	}

	// Unknown template -> NotFound.
	if _, err := client.GetSiteTemplate(context.Background(), &userv1.GetSiteTemplateRequest{Id: "does-not-exist"}); status.Code(err) != codes.NotFound {
		t.Fatalf("unknown GetSiteTemplate: want NotFound, got %v", err)
	}

	// Deactivated template -> NotFound (not leaked via fallback).
	_, _ = store.UpsertSiteTemplate(context.Background(), db.UpsertSiteTemplateParams{
		ID: "inactive-one", Slug: "inactive-one", Name: "Inactive", Category: "fashion",
		Archetype: "minimal-fashion", Description: "Off", Config: []byte(getResp.GetTemplate().GetConfigJson()), IsActive: false,
	})
	if _, err := client.GetSiteTemplate(context.Background(), &userv1.GetSiteTemplateRequest{Id: "inactive-one"}); status.Code(err) != codes.NotFound {
		t.Fatalf("inactive GetSiteTemplate: want NotFound, got %v", err)
	}

	// 2. Setup shops and users.
	sellerAUserID := uuid.NewString()
	sellerBUserID := uuid.NewString()
	buyerUserID := uuid.NewString()
	adminUserID := uuid.NewString()

	shopA := seedActiveShop(t, store, sellerAUserID, "Xưởng Dệt Bạch Đằng", ShopStatusActive)
	shopB := seedActiveShop(t, store, sellerBUserID, "Kho Basic Sài Gòn", ShopStatusActive)

	sellerATok, _, _, _ := users.SignAccessToken(sellerAUserID, RoleSeller, shopA.ID)
	sellerBTok, _, _, _ := users.SignAccessToken(sellerBUserID, RoleSeller, shopB.ID)
	buyerTok, _, _, _ := users.SignAccessToken(buyerUserID, RoleBuyer, "")
	adminTok, _, _, _ := users.SignAccessToken(adminUserID, RoleAdmin, "")
	svcTok, _ := svcMgr.SignServiceToken("order-service")

	// 3. Auth & RBAC enforcement on ApplySiteTemplate.
	if _, err := client.ApplySiteTemplate(ctxWithBearer(t, ""), &userv1.ApplySiteTemplateRequest{
		ShopId: shopA.ID, TemplateId: "editorial",
	}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anon ApplySiteTemplate: want Unauthenticated, got %v", err)
	}
	if _, err := client.ApplySiteTemplate(ctxWithBearer(t, svcTok), &userv1.ApplySiteTemplateRequest{
		ShopId: shopA.ID, TemplateId: "editorial",
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service token ApplySiteTemplate: want PermissionDenied, got %v", err)
	}
	if _, err := client.ApplySiteTemplate(ctxWithBearer(t, buyerTok), &userv1.ApplySiteTemplateRequest{
		ShopId: shopA.ID, TemplateId: "editorial",
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("buyer ApplySiteTemplate: want PermissionDenied, got %v", err)
	}
	if _, err := client.ApplySiteTemplate(ctxWithBearer(t, sellerBTok), &userv1.ApplySiteTemplateRequest{
		ShopId: shopA.ID, TemplateId: "editorial",
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cross-shop ApplySiteTemplate: want PermissionDenied, got %v", err)
	}
	if _, err := client.ApplySiteTemplate(ctxWithBearer(t, sellerATok), &userv1.ApplySiteTemplateRequest{
		TemplateId: "nonexistent-template",
	}); status.Code(err) != codes.NotFound {
		t.Fatalf("unknown template ApplySiteTemplate: want NotFound, got %v", err)
	}

	// 4. Seller A saves & publishes a v1 site first.
	v1Config := sampleSiteConfigJSON(t, "Bạch Đằng Chính Hãng", "Hero V1 Live")
	if _, err := client.SaveSiteDraft(ctxWithBearer(t, sellerATok), &userv1.SaveSiteDraftRequest{
		Subdomain:  "bach-dang",
		ConfigJson: v1Config,
	}); err != nil {
		t.Fatalf("SaveSiteDraft v1: %v", err)
	}
	pubResp, err := client.PublishSite(ctxWithBearer(t, sellerATok), &userv1.PublishSiteRequest{Note: "v1 live"})
	if err != nil {
		t.Fatalf("PublishSite v1: %v", err)
	}

	// 5. Seller A applies 'editorial' template -> draft becomes valid v2, brand preserved, live published untouched!
	applyResp, err := client.ApplySiteTemplate(ctxWithBearer(t, sellerATok), &userv1.ApplySiteTemplateRequest{
		TemplateId: "editorial",
	})
	if err != nil {
		t.Fatalf("ApplySiteTemplate(editorial): %v", err)
	}
	if applyResp.GetSite().GetStatus() != userv1.SiteStatus_SITE_STATUS_PUBLISHED {
		t.Fatalf("ApplySiteTemplate mutated status: got %v, want PUBLISHED", applyResp.GetSite().GetStatus())
	}
	if applyResp.GetSite().GetPublishedVersionId() != pubResp.GetVersion().GetId() {
		t.Fatalf("ApplySiteTemplate mutated published_version_id")
	}

	var appliedCfg site.ConfigV2
	if err := json.Unmarshal([]byte(applyResp.GetSite().GetConfigJson()), &appliedCfg); err != nil {
		t.Fatalf("unmarshal applied v2 draft: %v", err)
	}
	if appliedCfg.SchemaVersion != 2 || appliedCfg.TemplateID != "editorial" || appliedCfg.Layout.Archetype != "editorial" {
		t.Fatalf("unexpected applied template config: version=%d tmpl=%q archetype=%q", appliedCfg.SchemaVersion, appliedCfg.TemplateID, appliedCfg.Layout.Archetype)
	}
	if appliedCfg.Theme.BrandName != "Bạch Đằng Chính Hãng" || appliedCfg.Theme.LogoURL != "/banners/vietnam-workshop.jpg" {
		t.Fatalf("ApplySiteTemplate failed to preserve seller brand/logo: %+v", appliedCfg.Theme)
	}

	// Verify live published site STILL serves v1 Hero until seller publishes the new draft!
	liveSite, err := client.GetSiteByHost(context.Background(), &userv1.GetSiteByHostRequest{Host: "bach-dang.nexusmart.localhost"})
	if err != nil {
		t.Fatalf("GetSiteByHost: %v", err)
	}
	if !strings.Contains(liveSite.GetSite().GetConfigJson(), "Hero V1 Live") {
		t.Fatalf("live site unexpectedly mutated before publish: %s", liveSite.GetSite().GetConfigJson())
	}

	// Verify sites.template_id updated in store.
	storedSite, err := store.GetSiteByShopID(context.Background(), shopA.ID)
	if err != nil || storedSite.TemplateID == nil || *storedSite.TemplateID != "editorial" {
		t.Fatalf("expected stored sites.template_id='editorial', got %+v err=%v", storedSite.TemplateID, err)
	}

	// 6. Seller B has NO site yet -> ApplySiteTemplate creates a DRAFT site with derived subdomain and shop metadata!
	applyB, err := client.ApplySiteTemplate(ctxWithBearer(t, sellerBTok), &userv1.ApplySiteTemplateRequest{
		TemplateId: "sport-active",
	})
	if err != nil {
		t.Fatalf("first-time ApplySiteTemplate for shopB: %v", err)
	}
	if applyB.GetSite().GetStatus() != userv1.SiteStatus_SITE_STATUS_DRAFT {
		t.Fatalf("expected DRAFT for first-time site, got %v", applyB.GetSite().GetStatus())
	}
	if _, err := site.ValidateSubdomain(applyB.GetSite().GetSubdomain()); err != nil {
		t.Fatalf("derived subdomain %q invalid: %v", applyB.GetSite().GetSubdomain(), err)
	}
	if !strings.Contains(applyB.GetSite().GetConfigJson(), "Kho Basic Sài Gòn") {
		t.Fatalf("expected shopB brand name in first-time applied draft: %s", applyB.GetSite().GetConfigJson())
	}

	// 7. Audit log records site.apply_template.
	auditResp, err := client.ListAuditLogs(ctxWithBearer(t, adminTok), &userv1.ListAuditLogsRequest{
		TargetType: "site",
		TargetId:   applyResp.GetSite().GetId(),
		Page:       1,
		PageSize:   50,
	})
	if err != nil {
		t.Fatalf("ListAuditLogs: %v", err)
	}
	foundApplyAudit := false
	for _, entry := range auditResp.GetLogs() {
		if entry.GetAction() == "site.apply_template" && entry.GetReason() == "editorial" {
			foundApplyAudit = true
			break
		}
	}
	if !foundApplyAudit {
		t.Fatalf("expected site.apply_template audit log entry, got %+v", auditResp.GetLogs())
	}
}
