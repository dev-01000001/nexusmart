package router

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/services/gateway/internal/httpx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type siteDTO struct {
	ID                     string          `json:"id"`
	ShopID                 string          `json:"shop_id"`
	ShopName               string          `json:"shop_name"`
	Subdomain              string          `json:"subdomain"`
	PrimaryHost            string          `json:"primary_host"`
	Status                 string          `json:"status"`
	Config                 json.RawMessage `json:"config"`
	PublishedVersionID     string          `json:"published_version_id"`
	PublishedVersionNumber int32           `json:"published_version_number"`
	PublishedAt            string          `json:"published_at,omitempty"`
	SuspendedReason        string          `json:"suspended_reason"`
	CreatedAt              string          `json:"created_at,omitempty"`
	UpdatedAt              string          `json:"updated_at,omitempty"`
}

type siteVersionDTO struct {
	ID            string          `json:"id"`
	SiteID        string          `json:"site_id"`
	VersionNumber int32           `json:"version_number"`
	Config        json.RawMessage `json:"config"`
	PublishedBy   string          `json:"published_by"`
	Note          string          `json:"note"`
	CreatedAt     string          `json:"created_at,omitempty"`
}

func rawConfigJSON(raw string) json.RawMessage {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || !json.Valid([]byte(trimmed)) {
		return json.RawMessage("{}")
	}
	return json.RawMessage(trimmed)
}

func siteStatusString(st userv1.SiteStatus) string {
	switch st {
	case userv1.SiteStatus_SITE_STATUS_DRAFT:
		return "DRAFT"
	case userv1.SiteStatus_SITE_STATUS_PUBLISHED:
		return "PUBLISHED"
	case userv1.SiteStatus_SITE_STATUS_SUSPENDED:
		return "SUSPENDED"
	default:
		return ""
	}
}

func parseSiteStatus(s string) (userv1.SiteStatus, bool) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "", "ALL":
		return userv1.SiteStatus_SITE_STATUS_UNSPECIFIED, true
	case "DRAFT", "SITE_STATUS_DRAFT":
		return userv1.SiteStatus_SITE_STATUS_DRAFT, true
	case "PUBLISHED", "SITE_STATUS_PUBLISHED":
		return userv1.SiteStatus_SITE_STATUS_PUBLISHED, true
	case "SUSPENDED", "SITE_STATUS_SUSPENDED":
		return userv1.SiteStatus_SITE_STATUS_SUSPENDED, true
	default:
		return userv1.SiteStatus_SITE_STATUS_UNSPECIFIED, false
	}
}

func mapSite(s *userv1.Site) *siteDTO {
	if s == nil {
		return nil
	}
	return &siteDTO{
		ID:                     s.GetId(),
		ShopID:                 s.GetShopId(),
		ShopName:               s.GetShopName(),
		Subdomain:              s.GetSubdomain(),
		PrimaryHost:            s.GetPrimaryHost(),
		Status:                 siteStatusString(s.GetStatus()),
		Config:                 rawConfigJSON(s.GetConfigJson()),
		PublishedVersionID:     s.GetPublishedVersionId(),
		PublishedVersionNumber: s.GetPublishedVersionNumber(),
		PublishedAt:            httpx.FormatTimestamp(s.GetPublishedAt()),
		SuspendedReason:        s.GetSuspendedReason(),
		CreatedAt:              httpx.FormatTimestamp(s.GetCreatedAt()),
		UpdatedAt:              httpx.FormatTimestamp(s.GetUpdatedAt()),
	}
}

func mapSiteVersion(v *userv1.SiteVersion) *siteVersionDTO {
	if v == nil {
		return nil
	}
	return &siteVersionDTO{
		ID:            v.GetId(),
		SiteID:        v.GetSiteId(),
		VersionNumber: v.GetVersionNumber(),
		Config:        rawConfigJSON(v.GetConfigJson()),
		PublishedBy:   v.GetPublishedBy(),
		Note:          v.GetNote(),
		CreatedAt:     httpx.FormatTimestamp(v.GetCreatedAt()),
	}
}

func mapSitePublicShop(s *userv1.PublicShop) *publicShopDTO {
	if s == nil {
		return nil
	}
	return &publicShopDTO{
		ID:          s.GetId(),
		Name:        s.GetName(),
		Description: s.GetDescription(),
		Status:      shopStatusString(s.GetStatus()),
	}
}

// handleGetSiteByHost serves the public published site lookup by hostname (no JWT).
func (h *handler) handleGetSiteByHost(w http.ResponseWriter, r *http.Request) {
	host := strings.TrimSpace(r.URL.Query().Get("host"))
	if host == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "host query parameter is required")
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.GetSiteByHost(ctx, &userv1.GetSiteByHostRequest{
		Host: host,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"site": mapSite(resp.GetSite()),
		"shop": mapSitePublicShop(resp.GetShop()),
	})
}

// handleGetSiteByShop serves the public published site lookup by shop_id (no JWT).
func (h *handler) handleGetSiteByShop(w http.ResponseWriter, r *http.Request) {
	shopID := strings.TrimSpace(chi.URLParam(r, "shop_id"))
	if shopID == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "shop_id is required")
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.GetSiteByShop(ctx, &userv1.GetSiteByShopRequest{
		ShopId: shopID,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"site": mapSite(resp.GetSite()),
		"shop": mapSitePublicShop(resp.GetShop()),
	})
}

// handleSellerGetSite returns the caller's own site draft; shop_id comes strictly from JWT.
func (h *handler) handleSellerGetSite(w http.ResponseWriter, r *http.Request) {
	shopID, ok := requireSellerShopID(w, r)
	if !ok {
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.GetSiteDraft(ctx, &userv1.GetSiteDraftRequest{
		ShopId: shopID,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"site": mapSite(resp.GetSite()),
	})
}

// handleSellerSaveSiteDraft saves the seller's site draft, forwarding raw config JSON
// verbatim so services/user's strict schema validator rejects unknown fields.
func (h *handler) handleSellerSaveSiteDraft(w http.ResponseWriter, r *http.Request) {
	shopID, ok := requireSellerShopID(w, r)
	if !ok {
		return
	}
	var body struct {
		Subdomain string          `json:"subdomain"`
		Config    json.RawMessage `json:"config"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	rawCfg := bytes.TrimSpace(body.Config)
	if len(rawCfg) == 0 {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "config is required")
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.SaveSiteDraft(ctx, &userv1.SaveSiteDraftRequest{
		ShopId:     shopID,
		Subdomain:  body.Subdomain,
		ConfigJson: string(rawCfg),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"site": mapSite(resp.GetSite()),
	})
}

// handleSellerPublishSite publishes the caller's current draft; shop_id comes strictly from JWT.
func (h *handler) handleSellerPublishSite(w http.ResponseWriter, r *http.Request) {
	shopID, ok := requireSellerShopID(w, r)
	if !ok {
		return
	}
	var body struct {
		Note string `json:"note"`
	}
	if err := httpx.DecodeOptionalJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.PublishSite(ctx, &userv1.PublishSiteRequest{
		ShopId: shopID,
		Note:   body.Note,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"site":    mapSite(resp.GetSite()),
		"version": mapSiteVersion(resp.GetVersion()),
	})
}

// handleSellerRollbackSite restores a previous immutable site version for the caller's JWT shop_id.
func (h *handler) handleSellerRollbackSite(w http.ResponseWriter, r *http.Request) {
	shopID, ok := requireSellerShopID(w, r)
	if !ok {
		return
	}
	var body struct {
		VersionID string `json:"version_id"`
		Note      string `json:"note"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	versionID := strings.TrimSpace(body.VersionID)
	if versionID == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "version_id is required")
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.RollbackSite(ctx, &userv1.RollbackSiteRequest{
		ShopId:    shopID,
		VersionId: versionID,
		Note:      body.Note,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"site":    mapSite(resp.GetSite()),
		"version": mapSiteVersion(resp.GetVersion()),
	})
}

// handleSellerListSiteVersions lists immutable published versions for the caller's JWT shop_id.
func (h *handler) handleSellerListSiteVersions(w http.ResponseWriter, r *http.Request) {
	shopID, ok := requireSellerShopID(w, r)
	if !ok {
		return
	}
	page, pageSize, err := httpx.ParsePagination(r)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.ListSiteVersions(ctx, &userv1.ListSiteVersionsRequest{
		ShopId:   shopID,
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	versions := make([]*siteVersionDTO, 0, len(resp.GetVersions()))
	for _, v := range resp.GetVersions() {
		versions = append(versions, mapSiteVersion(v))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"versions":  versions,
		"page":      page,
		"page_size": pageSize,
		"total":     resp.GetTotal(),
	})
}

// handleAdminListSites lists sites across all shops with optional status & query filters.
func (h *handler) handleAdminListSites(w http.ResponseWriter, r *http.Request) {
	page, pageSize, err := httpx.ParsePagination(r)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	q := r.URL.Query()
	st, ok := parseSiteStatus(q.Get("status"))
	if !ok {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "status must be DRAFT, PUBLISHED, or SUSPENDED")
		return
	}
	search := strings.TrimSpace(q.Get("q"))
	if search == "" {
		search = strings.TrimSpace(q.Get("query"))
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.ListSites(ctx, &userv1.ListSitesRequest{
		Status:   st,
		Query:    search,
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	sites := make([]*siteDTO, 0, len(resp.GetSites()))
	for _, s := range resp.GetSites() {
		sites = append(sites, mapSite(s))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"sites":     sites,
		"page":      page,
		"page_size": pageSize,
		"total":     resp.GetTotal(),
	})
}

// handleAdminGetSiteByShop reads any shop's site draft for admin review.
func (h *handler) handleAdminGetSiteByShop(w http.ResponseWriter, r *http.Request) {
	shopID := strings.TrimSpace(chi.URLParam(r, "shop_id"))
	if shopID == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "shop_id is required")
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.GetSiteDraft(ctx, &userv1.GetSiteDraftRequest{
		ShopId: shopID,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"site": mapSite(resp.GetSite()),
	})
}

// handleAdminSuspendSite suspends or unsuspends a shop site (admin/super_admin only).
func (h *handler) handleAdminSuspendSite(w http.ResponseWriter, r *http.Request) {
	siteID := strings.TrimSpace(chi.URLParam(r, "site_id"))
	if siteID == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "site_id is required")
		return
	}
	var body struct {
		Suspend *bool  `json:"suspend"`
		Reason  string `json:"reason"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	if body.Suspend == nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "suspend boolean field is required")
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.SuspendSite(ctx, &userv1.SuspendSiteRequest{
		SiteId:  siteID,
		Suspend: *body.Suspend,
		Reason:  body.Reason,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"site": mapSite(resp.GetSite()),
	})
}

type publishedSiteDTO struct {
	SiteID      string `json:"site_id"`
	ShopID      string `json:"shop_id"`
	ShopName    string `json:"shop_name"`
	Subdomain   string `json:"subdomain"`
	PrimaryHost string `json:"primary_host"`
	LogoURL     string `json:"logo_url"`
	CoverURL    string `json:"cover_url"`
	Tagline     string `json:"tagline"`
}

const (
	publishedSitesDefaultPageSize int32 = 24
	publishedSitesMaxPageSize     int32 = 48
)

// parsePublishedSitesPagination mirrors httpx.ParsePagination with the public directory bounds
// (default 24, max 48) and rejects page values whose 0-based SQL offset exceeds math.MaxInt32.
func parsePublishedSitesPagination(r *http.Request) (page int32, pageSize int32, err error) {
	pageSize = parseBoundedPageSize(r, publishedSitesDefaultPageSize, publishedSitesMaxPageSize)

	rawPage := strings.TrimSpace(r.URL.Query().Get("page"))
	if rawPage == "" {
		return 1, pageSize, nil
	}
	page64, parseErr := strconv.ParseInt(rawPage, 10, 64)
	if parseErr != nil {
		if errors.Is(parseErr, strconv.ErrRange) && !strings.HasPrefix(rawPage, "-") {
			return 0, 0, errors.New("page exceeds maximum pagination offset")
		}
		return 1, pageSize, nil
	}
	if page64 <= 0 {
		return 1, pageSize, nil
	}
	if page64 > math.MaxInt32 || (page64-1)*int64(pageSize) > math.MaxInt32 {
		return 0, 0, errors.New("page exceeds maximum pagination offset")
	}
	return int32(page64), pageSize, nil
}

func parseBoundedPageSize(r *http.Request, def, maxVal int32) int32 {
	raw := strings.TrimSpace(r.URL.Query().Get("page_size"))
	if raw == "" {
		return def
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		if errors.Is(err, strconv.ErrRange) && !strings.HasPrefix(raw, "-") {
			return maxVal
		}
		return def
	}
	if n <= 0 {
		return def
	}
	if n > int64(maxVal) {
		return maxVal
	}
	return int32(n)
}

// handleListPublishedSites serves the public platform site directory (no JWT): PUBLISHED sites of
// ACTIVE shops only, newest publish first. page_size defaults to 24 and is capped at 48.
func (h *handler) handleListPublishedSites(w http.ResponseWriter, r *http.Request) {
	page, pageSize, err := parsePublishedSitesPagination(r)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.ListPublishedSites(ctx, &userv1.ListPublishedSitesRequest{
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	sites := make([]publishedSiteDTO, 0, len(resp.GetSites()))
	for _, s := range resp.GetSites() {
		sites = append(sites, publishedSiteDTO{
			SiteID:      s.GetSiteId(),
			ShopID:      s.GetShopId(),
			ShopName:    s.GetShopName(),
			Subdomain:   s.GetSubdomain(),
			PrimaryHost: s.GetPrimaryHost(),
			LogoURL:     s.GetLogoUrl(),
			CoverURL:    s.GetCoverUrl(),
			Tagline:     s.GetTagline(),
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"sites":     sites,
		"total":     resp.GetTotal(),
		"page":      resp.GetPage(),
		"page_size": resp.GetPageSize(),
	})
}

type domainVerificationDTO struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

type domainDTO struct {
	ID           string                  `json:"id"`
	Host         string                  `json:"host"`
	Kind         string                  `json:"kind"`
	Status       string                  `json:"status"`
	IsPrimary    bool                    `json:"is_primary"`
	Verification []domainVerificationDTO `json:"verification"`
	LastError    string                  `json:"last_error"`
	VerifiedAt   string                  `json:"verified_at,omitempty"`
	CreatedAt    string                  `json:"created_at,omitempty"`
}

func mapDomain(d *userv1.Domain) *domainDTO {
	if d == nil {
		return nil
	}
	records := d.GetVerification()
	verification := make([]domainVerificationDTO, 0, len(records))
	for _, rec := range records {
		if rec == nil {
			continue
		}
		verification = append(verification, domainVerificationDTO{
			Type:  rec.GetType(),
			Name:  rec.GetName(),
			Value: rec.GetValue(),
		})
	}
	return &domainDTO{
		ID:           d.GetId(),
		Host:         d.GetHost(),
		Kind:         d.GetKind(),
		Status:       d.GetStatus(),
		IsPrimary:    d.GetIsPrimary(),
		Verification: verification,
		LastError:    d.GetLastError(),
		VerifiedAt:   httpx.FormatTimestamp(d.GetVerifiedAt()),
		CreatedAt:    httpx.FormatTimestamp(d.GetCreatedAt()),
	}
}

func mapDomains(items []*userv1.Domain) []*domainDTO {
	domains := make([]*domainDTO, 0, len(items))
	for _, d := range items {
		if dto := mapDomain(d); dto != nil {
			domains = append(domains, dto)
		}
	}
	return domains
}

// writeDomainGRPCError maps contract-specific domain gRPC errors to their stable REST codes.
func writeDomainGRPCError(w http.ResponseWriter, r *http.Request, err error) {
	if st, ok := status.FromError(err); ok {
		msg := st.Message()
		trimmed := strings.TrimSpace(msg)
		switch {
		case st.Code() == codes.InvalidArgument && strings.HasPrefix(trimmed, "invalid_hostname"):
			httpx.WriteError(w, r, http.StatusBadRequest, "invalid_hostname", msg)
			return
		case st.Code() == codes.AlreadyExists && strings.HasPrefix(trimmed, "domain_taken"):
			httpx.WriteError(w, r, http.StatusConflict, "domain_taken", msg)
			return
		case st.Code() == codes.FailedPrecondition && strings.HasPrefix(trimmed, "domain_limit"):
			httpx.WriteError(w, r, http.StatusConflict, "domain_limit", msg)
			return
		}
	}
	httpx.WriteGRPCError(w, r, err)
}

// handleSellerListSiteDomains lists all domains (subdomain + custom) for the seller's JWT shop_id.
func (h *handler) handleSellerListSiteDomains(w http.ResponseWriter, r *http.Request) {
	shopID, ok := requireSellerShopID(w, r)
	if !ok {
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.ListSiteDomains(ctx, &userv1.ListSiteDomainsRequest{
		ShopId: shopID,
	})
	if err != nil {
		writeDomainGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"domains": mapDomains(resp.GetDomains()),
	})
}

// handleSellerAddCustomDomain attaches a custom domain to the seller's site (shop_id strictly from JWT).
func (h *handler) handleSellerAddCustomDomain(w http.ResponseWriter, r *http.Request) {
	shopID, ok := requireSellerShopID(w, r)
	if !ok {
		return
	}
	var body struct {
		Hostname string `json:"hostname"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	hostname := strings.TrimSpace(body.Hostname)
	if hostname == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_hostname", "hostname is required")
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.AddCustomDomain(ctx, &userv1.AddCustomDomainRequest{
		ShopId:   shopID,
		Hostname: hostname,
	})
	if err != nil {
		writeDomainGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"domain": mapDomain(resp.GetDomain()),
	})
}

// handleSellerCheckCustomDomain triggers a Cloudflare verification check for a custom domain of the seller's shop.
func (h *handler) handleSellerCheckCustomDomain(w http.ResponseWriter, r *http.Request) {
	shopID, ok := requireSellerShopID(w, r)
	if !ok {
		return
	}
	domainID := strings.TrimSpace(chi.URLParam(r, "id"))
	if domainID == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "domain id is required")
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.CheckCustomDomain(ctx, &userv1.CheckCustomDomainRequest{
		ShopId:   shopID,
		DomainId: domainID,
	})
	if err != nil {
		writeDomainGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"domain": mapDomain(resp.GetDomain()),
	})
}

// handleSellerRemoveCustomDomain removes a custom domain belonging to the seller's JWT shop_id.
func (h *handler) handleSellerRemoveCustomDomain(w http.ResponseWriter, r *http.Request) {
	shopID, ok := requireSellerShopID(w, r)
	if !ok {
		return
	}
	domainID := strings.TrimSpace(chi.URLParam(r, "id"))
	if domainID == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "domain id is required")
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	if _, err := h.clients.User.RemoveCustomDomain(ctx, &userv1.RemoveCustomDomainRequest{
		ShopId:   shopID,
		DomainId: domainID,
	}); err != nil {
		writeDomainGRPCError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleAdminListSiteDomains lists all domains for a shop's site (admin/super_admin only).
func (h *handler) handleAdminListSiteDomains(w http.ResponseWriter, r *http.Request) {
	shopID := strings.TrimSpace(chi.URLParam(r, "shop_id"))
	if shopID == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "shop_id is required")
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.ListSiteDomains(ctx, &userv1.ListSiteDomainsRequest{
		ShopId: shopID,
	})
	if err != nil {
		writeDomainGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"domains": mapDomains(resp.GetDomains()),
	})
}

// handleAdminRemoveCustomDomain removes a custom domain from a shop's site (admin/super_admin only).
func (h *handler) handleAdminRemoveCustomDomain(w http.ResponseWriter, r *http.Request) {
	shopID := strings.TrimSpace(chi.URLParam(r, "shop_id"))
	if shopID == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "shop_id is required")
		return
	}
	domainID := strings.TrimSpace(chi.URLParam(r, "id"))
	if domainID == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "domain id is required")
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	if _, err := h.clients.User.RemoveCustomDomain(ctx, &userv1.RemoveCustomDomainRequest{
		ShopId:   shopID,
		DomainId: domainID,
	}); err != nil {
		writeDomainGRPCError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
