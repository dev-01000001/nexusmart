package router

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/services/gateway/internal/httpx"
)

func (h *handler) mountSiteTemplatePublicRoutes(api chi.Router) {
	api.Get("/site-templates", h.handleListSiteTemplates)
	api.Get("/site-templates/{id}", h.handleGetSiteTemplate)
}

func (h *handler) mountSiteTemplateSellerRoutes(seller chi.Router) {
	seller.Post("/site/apply-template", h.handleSellerApplySiteTemplate)
}

type siteTemplateDTO struct {
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
	CreatedAt       string          `json:"created_at,omitempty"`
	UpdatedAt       string          `json:"updated_at,omitempty"`
}

func mapSiteTemplate(t *userv1.SiteTemplate) *siteTemplateDTO {
	if t == nil {
		return nil
	}
	tags := make([]string, len(t.GetTags()))
	copy(tags, t.GetTags())
	return &siteTemplateDTO{
		ID:              t.GetId(),
		Slug:            t.GetSlug(),
		Name:            t.GetName(),
		Category:        t.GetCategory(),
		Archetype:       t.GetArchetype(),
		Description:     t.GetDescription(),
		PreviewImageURL: t.GetPreviewImageUrl(),
		Tags:            tags,
		Config:          rawConfigJSON(t.GetConfigJson()),
		IsActive:        t.GetIsActive(),
		SortOrder:       t.GetSortOrder(),
		CreatedAt:       httpx.FormatTimestamp(t.GetCreatedAt()),
		UpdatedAt:       httpx.FormatTimestamp(t.GetUpdatedAt()),
	}
}

// handleListSiteTemplates returns all active platform site templates (public, no JWT).
func (h *handler) handleListSiteTemplates(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.ListSiteTemplates(ctx, &userv1.ListSiteTemplatesRequest{})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	templates := make([]*siteTemplateDTO, 0, len(resp.GetTemplates()))
	for _, t := range resp.GetTemplates() {
		if dto := mapSiteTemplate(t); dto != nil {
			templates = append(templates, dto)
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"templates": templates,
		"total":     len(templates),
	})
}

// handleGetSiteTemplate returns one active platform site template by ID or slug (public, no JWT).
func (h *handler) handleGetSiteTemplate(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	if id == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "template id is required")
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.GetSiteTemplate(ctx, &userv1.GetSiteTemplateRequest{
		Id: id,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"template": mapSiteTemplate(resp.GetTemplate()),
	})
}

// handleSellerApplySiteTemplate applies a platform template to the caller's site draft.
// shop_id is taken strictly from the verified seller JWT, never from the request body.
func (h *handler) handleSellerApplySiteTemplate(w http.ResponseWriter, r *http.Request) {
	shopID, ok := requireSellerShopID(w, r)
	if !ok {
		return
	}

	var body struct {
		TemplateID string `json:"template_id"`
		Subdomain  string `json:"subdomain"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
		return
	}
	templateID := strings.TrimSpace(body.TemplateID)
	if templateID == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "template_id is required")
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.ApplySiteTemplate(ctx, &userv1.ApplySiteTemplateRequest{
		ShopId:     shopID,
		TemplateId: templateID,
		Subdomain:  strings.TrimSpace(body.Subdomain),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"site":        mapSite(resp.GetSite()),
		"template":    mapSiteTemplate(resp.GetTemplate()),
		"template_id": resp.GetTemplate().GetId(),
	})
}
