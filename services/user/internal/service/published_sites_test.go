package service

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/services/user/internal/db"
)

// publishedSitesLocked mirrors the ListPublishedSites SQL: PUBLISHED sites with a published
// version whose shop is ACTIVE, card fields read from the published snapshot only.
func (m *memStore) publishedSitesLocked() []db.ListPublishedSitesRow {
	out := make([]db.ListPublishedSitesRow, 0)
	for _, st := range m.sites {
		if st.Status != SiteStatusPublished || st.PublishedVersionID == nil {
			continue
		}
		ver, ok := m.siteVersions[*st.PublishedVersionID]
		if !ok {
			continue
		}
		sh, ok := m.shops[st.ShopID]
		if !ok || sh.Status != ShopStatusActive {
			continue
		}
		var cfg struct {
			Theme struct {
				LogoURL   string `json:"logo_url"`
				BannerURL string `json:"banner_url"`
				Tagline   string `json:"tagline"`
			} `json:"theme"`
		}
		_ = json.Unmarshal(ver.Config, &cfg)
		tagline := cfg.Theme.Tagline
		if tagline == "" {
			tagline = sh.Description
		}
		out = append(out, db.ListPublishedSitesRow{
			ID:          st.ID,
			ShopID:      st.ShopID,
			ShopName:    sh.Name,
			Subdomain:   st.Subdomain,
			PrimaryHost: m.primaryHostLocked(st.ID),
			LogoUrl:     cfg.Theme.LogoURL,
			CoverUrl:    cfg.Theme.BannerURL,
			Tagline:     tagline,
			PublishedAt: st.PublishedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].PublishedAt, out[j].PublishedAt
		if a != nil && b != nil && !a.Equal(*b) {
			return a.After(*b)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (m *memStore) ListPublishedSites(_ context.Context, arg db.ListPublishedSitesParams) ([]db.ListPublishedSitesRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	all := m.publishedSitesLocked()
	start := int(arg.PageOffset)
	if start > len(all) {
		start = len(all)
	}
	end := start + int(arg.PageLimit)
	if end > len(all) {
		end = len(all)
	}
	return all[start:end], nil
}

func (m *memStore) CountPublishedSites(_ context.Context) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return int64(len(m.publishedSitesLocked())), nil
}

func TestListPublishedSites_PublicDirectory(t *testing.T) {
	client, store, users, _ := startSiteBufconn(t)
	adminTok, _, _, _ := users.SignAccessToken(uuid.NewString(), RoleAdmin, "")

	type seeded struct {
		shop db.Shop
		tok  string
	}
	mk := func(name string) seeded {
		owner := uuid.NewString()
		sh := seedActiveShop(t, store, owner, name, ShopStatusActive)
		tok, _, _, _ := users.SignAccessToken(owner, RoleSeller, sh.ID)
		return seeded{shop: sh, tok: tok}
	}
	saveAndPublish := func(s seeded, sub, cfg string, publish bool) {
		t.Helper()
		if _, err := client.SaveSiteDraft(ctxWithBearer(t, s.tok), &userv1.SaveSiteDraftRequest{Subdomain: sub, ConfigJson: cfg}); err != nil {
			t.Fatalf("save draft %s: %v", sub, err)
		}
		if !publish {
			return
		}
		if _, err := client.PublishSite(ctxWithBearer(t, s.tok), &userv1.PublishSiteRequest{}); err != nil {
			t.Fatalf("publish %s: %v", sub, err)
		}
	}

	bachDang := mk("Xưởng Dệt Bạch Đằng")
	saiGon := mk("Kho Basic Sài Gòn")
	draftOnly := mk("Shop Nháp")
	shopLocked := mk("Shop Bị Khóa")
	siteLocked := mk("Site Bị Khóa")

	saveAndPublish(bachDang, "bach-dang", sampleSiteConfigJSON(t, "Bạch Đằng", "Hero"), true)
	noTagline := strings.Replace(sampleSiteConfigJSON(t, "Sài Gòn", "Hero"), `"Xưởng may nội địa chuẩn chất lượng"`, `""`, 1)
	saveAndPublish(saiGon, "sai-gon", noTagline, true)
	saveAndPublish(draftOnly, "shop-nhap", sampleSiteConfigJSON(t, "Nháp", "Hero"), false)
	saveAndPublish(shopLocked, "shop-khoa", sampleSiteConfigJSON(t, "Khóa", "Hero"), true)
	saveAndPublish(siteLocked, "site-khoa", sampleSiteConfigJSON(t, "Site khóa", "Hero"), true)

	if _, err := store.UpdateShopReview(context.Background(), db.UpdateShopReviewParams{
		ID: shopLocked.shop.ID, Status: ShopStatusSuspended, RejectionReason: "khóa",
	}); err != nil {
		t.Fatalf("suspend shop: %v", err)
	}
	lockedDraft, err := client.GetSiteDraft(ctxWithBearer(t, siteLocked.tok), &userv1.GetSiteDraftRequest{})
	if err != nil {
		t.Fatalf("get draft: %v", err)
	}
	if _, err := client.SuspendSite(ctxWithBearer(t, adminTok), &userv1.SuspendSiteRequest{
		SiteId: lockedDraft.GetSite().GetId(), Suspend: true, Reason: "vi phạm",
	}); err != nil {
		t.Fatalf("suspend site: %v", err)
	}

	// A newer draft must never leak into the directory card.
	leaky := strings.Replace(sampleSiteConfigJSON(t, "Bạch Đằng", "Hero"), "Xưởng may nội địa chuẩn chất lượng", "DRAFT-TAGLINE", 1)
	saveAndPublish(bachDang, "bach-dang", leaky, false)

	// Anonymous caller (no JWT).
	resp, err := client.ListPublishedSites(context.Background(), &userv1.ListPublishedSitesRequest{})
	if err != nil {
		t.Fatalf("anonymous ListPublishedSites: %v", err)
	}
	if resp.GetTotal() != 2 || len(resp.GetSites()) != 2 || resp.GetPage() != 1 || resp.GetPageSize() != 24 {
		t.Fatalf("want 2 published sites page=1 size=24, got total=%d n=%d page=%d size=%d", resp.GetTotal(), len(resp.GetSites()), resp.GetPage(), resp.GetPageSize())
	}
	// Newest publish first: sai-gon was published after bach-dang.
	first, second := resp.GetSites()[0], resp.GetSites()[1]
	if first.GetSubdomain() != "sai-gon" || second.GetSubdomain() != "bach-dang" {
		t.Fatalf("want order sai-gon, bach-dang; got %s, %s", first.GetSubdomain(), second.GetSubdomain())
	}
	if second.GetTagline() != "Xưởng may nội địa chuẩn chất lượng" {
		t.Fatalf("directory must show published tagline, got %q", second.GetTagline())
	}
	if first.GetTagline() != "Mô tả công khai của Kho Basic Sài Gòn" {
		t.Fatalf("empty theme tagline must fall back to shop description, got %q", first.GetTagline())
	}
	if second.GetPrimaryHost() != "bach-dang.nexusmart.localhost" || second.GetShopName() != "Xưởng Dệt Bạch Đằng" ||
		second.GetShopId() != bachDang.shop.ID || second.GetSiteId() == "" ||
		second.GetLogoUrl() != "/banners/vietnam-workshop.jpg" || second.GetCoverUrl() != "/banners/campaign-everyday.jpg" ||
		second.GetPublishedAt() == nil {
		t.Fatalf("unexpected card: %+v", second)
	}

	// Pagination: page 2 of size 1, and page_size capped at 48.
	p2, err := client.ListPublishedSites(context.Background(), &userv1.ListPublishedSitesRequest{Page: 2, PageSize: 1})
	if err != nil || p2.GetTotal() != 2 || len(p2.GetSites()) != 1 || p2.GetSites()[0].GetSubdomain() != "bach-dang" {
		t.Fatalf("page 2 size 1: got %+v err=%v", p2, err)
	}
	big, err := client.ListPublishedSites(context.Background(), &userv1.ListPublishedSitesRequest{Page: -3, PageSize: 500})
	if err != nil || big.GetPageSize() != 48 || big.GetPage() != 1 {
		t.Fatalf("page_size cap: got page=%d size=%d err=%v", big.GetPage(), big.GetPageSize(), err)
	}
	beyond, err := client.ListPublishedSites(context.Background(), &userv1.ListPublishedSitesRequest{Page: 9, PageSize: 24})
	if err != nil || len(beyond.GetSites()) != 0 || beyond.GetTotal() != 2 {
		t.Fatalf("page beyond end: got %+v err=%v", beyond, err)
	}
}
