package router

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	"github.com/nexusmart/core/services/gateway/internal/httpx"
	"github.com/nexusmart/core/services/gateway/internal/middleware"
)

const maxRescanBatchLimit int32 = 500

type bannedKeywordDTO struct {
	ID             string `json:"id"`
	Term           string `json:"term"`
	NormalizedTerm string `json:"normalized_term"`
	Category       string `json:"category"`
	Action         string `json:"action"`
	IsActive       bool   `json:"is_active"`
	CreatedBy      string `json:"created_by"`
	CreatedAt      string `json:"created_at,omitempty"`
	UpdatedAt      string `json:"updated_at,omitempty"`
}

type bannedContentProductSampleDTO struct {
	ProductID     string   `json:"product_id"`
	ShopID        string   `json:"shop_id"`
	Name          string   `json:"name"`
	Slug          string   `json:"slug"`
	Status        string   `json:"status"`
	MatchedTerms  []string `json:"matched_terms"`
	HighestAction string   `json:"highest_action"`
}

func bannedKeywordActionString(a productv1.BannedKeywordAction) string {
	switch a {
	case productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_BLOCK:
		return "BLOCK"
	case productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_REVIEW:
		return "REVIEW"
	default:
		return ""
	}
}

func parseBannedKeywordAction(s string) productv1.BannedKeywordAction {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "BLOCK", "BANNED_KEYWORD_ACTION_BLOCK":
		return productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_BLOCK
	case "REVIEW", "BANNED_KEYWORD_ACTION_REVIEW":
		return productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_REVIEW
	default:
		return productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_UNSPECIFIED
	}
}

func mapBannedKeyword(k *productv1.BannedKeyword) *bannedKeywordDTO {
	if k == nil {
		return nil
	}
	return &bannedKeywordDTO{
		ID:             k.GetId(),
		Term:           k.GetTerm(),
		NormalizedTerm: k.GetNormalizedTerm(),
		Category:       k.GetCategory(),
		Action:         bannedKeywordActionString(k.GetAction()),
		IsActive:       k.GetIsActive(),
		CreatedBy:      k.GetCreatedBy(),
		CreatedAt:      httpx.FormatTimestamp(k.GetCreatedAt()),
		UpdatedAt:      httpx.FormatTimestamp(k.GetUpdatedAt()),
	}
}

func mapBannedContentProductSamples(in []*productv1.BannedContentProductSample) []*bannedContentProductSampleDTO {
	out := make([]*bannedContentProductSampleDTO, 0, len(in))
	for _, s := range in {
		if s == nil {
			continue
		}
		out = append(out, &bannedContentProductSampleDTO{
			ProductID:     s.GetProductId(),
			ShopID:        s.GetShopId(),
			Name:          s.GetName(),
			Slug:          s.GetSlug(),
			Status:        productStatusString(s.GetStatus()),
			MatchedTerms:  nonNilStrings(s.GetMatchedTerms()),
			HighestAction: bannedKeywordActionString(s.GetHighestAction()),
		})
	}
	return out
}

func (h *handler) handleAdminListBannedKeywords(w http.ResponseWriter, r *http.Request) {
	page, pageSize, err := httpx.ParsePagination(r)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	q := r.URL.Query()

	rawAction := strings.TrimSpace(q.Get("action"))
	action := parseBannedKeywordAction(rawAction)
	if rawAction != "" && action == productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_UNSPECIFIED {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "action must be BLOCK or REVIEW")
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Product.ListBannedKeywords(ctx, &productv1.ListBannedKeywordsRequest{
		Query:           strings.TrimSpace(q.Get("query")),
		Action:          action,
		IncludeInactive: httpx.ParseBoolQuery(r, "include_inactive"),
		Page:            page,
		PageSize:        pageSize,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	items := make([]*bannedKeywordDTO, 0, len(resp.GetKeywords()))
	for _, kw := range resp.GetKeywords() {
		items = append(items, mapBannedKeyword(kw))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items":     items,
		"page":      page,
		"page_size": pageSize,
		"total":     resp.GetTotal(),
	})
}

func (h *handler) handleAdminCreateBannedKeyword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Term     string `json:"term"`
		Category string `json:"category"`
		Action   string `json:"action"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	term := strings.TrimSpace(body.Term)
	if term == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "term is required")
		return
	}
	action := parseBannedKeywordAction(body.Action)
	if action == productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_UNSPECIFIED {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "action must be BLOCK or REVIEW")
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Product.CreateBannedKeyword(ctx, &productv1.CreateBannedKeywordRequest{
		Term:     term,
		Category: strings.TrimSpace(body.Category),
		Action:   action,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"keyword": mapBannedKeyword(resp.GetKeyword()),
	})
}

func (h *handler) handleAdminUpdateBannedKeyword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Term     string `json:"term"`
		Category string `json:"category"`
		Action   string `json:"action"`
		IsActive *bool  `json:"is_active"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	rawAction := strings.TrimSpace(body.Action)
	action := parseBannedKeywordAction(rawAction)
	if rawAction != "" && action == productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_UNSPECIFIED {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "action must be BLOCK or REVIEW")
		return
	}

	req := &productv1.UpdateBannedKeywordRequest{
		Id:       chi.URLParam(r, "id"),
		Term:     strings.TrimSpace(body.Term),
		Category: strings.TrimSpace(body.Category),
		Action:   action,
	}
	if body.IsActive != nil {
		req.IsActive = *body.IsActive
		req.HasIsActive = true
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Product.UpdateBannedKeyword(ctx, req)
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"keyword": mapBannedKeyword(resp.GetKeyword()),
	})
}

func (h *handler) handleAdminDeleteBannedKeyword(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Product.DeleteBannedKeyword(ctx, &productv1.DeleteBannedKeywordRequest{
		Id: chi.URLParam(r, "id"),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": resp.GetOk()})
}

func (h *handler) handleAdminPreviewBannedKeyword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Term string `json:"term"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	term := strings.TrimSpace(body.Term)
	if term == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "term is required")
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Product.PreviewBannedKeyword(ctx, &productv1.PreviewBannedKeywordRequest{
		Term: term,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"affected_count": resp.GetAffectedCount(),
		"samples":        mapBannedContentProductSamples(resp.GetSamples()),
	})
}

func (h *handler) handleAdminRescanBannedContent(w http.ResponseWriter, r *http.Request) {
	var body struct {
		KeywordIDs []string `json:"keyword_ids"`
		DryRun     bool     `json:"dry_run"`
		Cursor     string   `json:"cursor"`
		Limit      int32    `json:"limit"`
	}
	if err := httpx.DecodeOptionalJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	if body.Limit < 0 {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "limit must be non-negative")
		return
	}
	limit := body.Limit
	if limit > maxRescanBatchLimit {
		limit = maxRescanBatchLimit
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Product.RescanProductsForBannedContent(ctx, &productv1.RescanProductsForBannedContentRequest{
		KeywordIds: body.KeywordIDs,
		DryRun:     body.DryRun,
		Cursor:     strings.TrimSpace(body.Cursor),
		Limit:      limit,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	kwIDs := nonNilStrings(body.KeywordIDs)
	if len(kwIDs) > 50 {
		kwIDs = kwIDs[:50]
	}
	middleware.SetAuditSummary(r.Context(), "", map[string]any{
		"keyword_ids":       kwIDs,
		"keyword_ids_count": len(body.KeywordIDs),
		"dry_run":           body.DryRun,
		"cursor":            strings.TrimSpace(body.Cursor),
		"limit":             limit,
		"scanned_count":     resp.GetScannedCount(),
		"blocked_count":     resp.GetBlockedCount(),
		"flagged_count":     resp.GetFlaggedCount(),
		"samples_count":     len(resp.GetSamples()),
		"has_more":          resp.GetHasMore(),
		"next_cursor":       resp.GetNextCursor(),
	})

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"scanned_count": resp.GetScannedCount(),
		"blocked_count": resp.GetBlockedCount(),
		"flagged_count": resp.GetFlaggedCount(),
		"samples":       mapBannedContentProductSamples(resp.GetSamples()),
		"has_more":      resp.GetHasMore(),
		"next_cursor":   resp.GetNextCursor(),
	})
}
