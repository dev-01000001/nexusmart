package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/product/internal/events"
	"github.com/nexusmart/core/services/product/internal/moderation"
	"github.com/nexusmart/core/services/product/internal/store"
)

const (
	defaultRescanBatchSize = 500
	maxRescanBatchSize     = 500
	maxModerationSamples   = 20
	prohibitedCategoryTag  = "PROHIBITED_CATEGORY"
)

type moderationSKU struct {
	Name       string
	Attributes map[string]string
}

// --- Banned Keywords CRUD (Admin — E9/E10/E11) ---

func (s *Server) ListBannedKeywords(ctx context.Context, req *productv1.ListBannedKeywordsRequest) (*productv1.ListBannedKeywordsResponse, error) {
	if _, err := requireCapability(ctx, auth.CapBannedKeywordsRead); err != nil {
		return nil, err
	}
	actionFilter := ""
	if req.GetAction() != productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_UNSPECIFIED {
		actionFilter = fromProtoBannedKeywordAction(req.GetAction())
		if actionFilter == "" {
			return nil, status.Error(codes.InvalidArgument, "invalid banned keyword action")
		}
	}
	page := req.GetPage()
	if page <= 0 {
		page = 1
	}
	pageSize := req.GetPageSize()
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}

	rows, total, err := s.store.ListBannedKeywords(ctx, store.ListBannedKeywordsFilter{
		Query:           strings.TrimSpace(req.GetQuery()),
		Action:          actionFilter,
		IncludeInactive: req.GetIncludeInactive(),
		Limit:           pageSize,
		Offset:          (page - 1) * pageSize,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list banned keywords: %v", err)
	}
	out := make([]*productv1.BannedKeyword, 0, len(rows))
	for _, r := range rows {
		out = append(out, toProtoBannedKeyword(r))
	}
	return &productv1.ListBannedKeywordsResponse{
		Keywords: out,
		Total:    total,
	}, nil
}

func (s *Server) CreateBannedKeyword(ctx context.Context, req *productv1.CreateBannedKeywordRequest) (*productv1.CreateBannedKeywordResponse, error) {
	claims, err := requireCapability(ctx, auth.CapBannedKeywordsWrite)
	if err != nil {
		return nil, err
	}
	term := strings.TrimSpace(req.GetTerm())
	normalized, ok := moderation.NormalizeTerm(term)
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "term must contain at least 2 alphanumeric characters")
	}
	action := fromProtoBannedKeywordAction(req.GetAction())
	if action == "" {
		return nil, status.Error(codes.InvalidArgument, "action must be BLOCK or REVIEW")
	}
	category := strings.TrimSpace(req.GetCategory())
	if category == "" {
		category = "GENERAL"
	}

	now := s.now()
	ctx = store.WithAsOf(ctx, now)
	created, err := s.store.CreateBannedKeyword(ctx, store.BannedKeyword{
		ID:             uuid.NewString(),
		Term:           term,
		NormalizedTerm: normalized,
		Category:       category,
		Action:         action,
		IsActive:       true,
		CreatedBy:      claims.UserID(),
		CreatedAt:      now,
	})
	if err != nil {
		if errors.Is(err, store.ErrAlreadyExists) {
			return nil, status.Error(codes.AlreadyExists, "banned keyword already exists")
		}
		return nil, status.Errorf(codes.Internal, "create banned keyword: %v", err)
	}
	return &productv1.CreateBannedKeywordResponse{
		Keyword: toProtoBannedKeyword(created),
	}, nil
}

func (s *Server) UpdateBannedKeyword(ctx context.Context, req *productv1.UpdateBannedKeywordRequest) (*productv1.UpdateBannedKeywordResponse, error) {
	if _, err := requireCapability(ctx, auth.CapBannedKeywordsWrite); err != nil {
		return nil, err
	}
	id := strings.TrimSpace(req.GetId())
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "banned keyword id is required")
	}

	now := s.now()
	ctx = store.WithAsOf(ctx, now)

	existing, err := s.store.GetBannedKeywordByID(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "banned keyword not found")
		}
		return nil, status.Errorf(codes.Internal, "get banned keyword: %v", err)
	}

	term := existing.Term
	normalized := existing.NormalizedTerm
	if rawTerm := strings.TrimSpace(req.GetTerm()); rawTerm != "" {
		norm, ok := moderation.NormalizeTerm(rawTerm)
		if !ok {
			return nil, status.Error(codes.InvalidArgument, "term must contain at least 2 alphanumeric characters")
		}
		term = rawTerm
		normalized = norm
	}

	category := existing.Category
	if rawCat := strings.TrimSpace(req.GetCategory()); rawCat != "" {
		category = rawCat
	}

	action := existing.Action
	if req.GetAction() != productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_UNSPECIFIED {
		parsed := fromProtoBannedKeywordAction(req.GetAction())
		if parsed == "" {
			return nil, status.Error(codes.InvalidArgument, "action must be BLOCK or REVIEW")
		}
		action = parsed
	}

	isActive := existing.IsActive
	if req.GetHasIsActive() || req.GetIsActive() {
		isActive = req.GetIsActive()
	}

	updated, err := s.store.UpdateBannedKeyword(ctx, store.BannedKeyword{
		ID:             id,
		Term:           term,
		NormalizedTerm: normalized,
		Category:       category,
		Action:         action,
		IsActive:       isActive,
	})
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			return nil, status.Error(codes.NotFound, "banned keyword not found")
		case errors.Is(err, store.ErrAlreadyExists):
			return nil, status.Error(codes.AlreadyExists, "banned keyword already exists")
		default:
			return nil, status.Errorf(codes.Internal, "update banned keyword: %v", err)
		}
	}
	return &productv1.UpdateBannedKeywordResponse{
		Keyword: toProtoBannedKeyword(updated),
	}, nil
}

func (s *Server) DeleteBannedKeyword(ctx context.Context, req *productv1.DeleteBannedKeywordRequest) (*productv1.DeleteBannedKeywordResponse, error) {
	if _, err := requireCapability(ctx, auth.CapBannedKeywordsWrite); err != nil {
		return nil, err
	}
	id := strings.TrimSpace(req.GetId())
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "banned keyword id is required")
	}
	deleted, err := s.store.DeleteBannedKeyword(ctx, id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "delete banned keyword: %v", err)
	}
	if !deleted {
		return nil, status.Error(codes.NotFound, "banned keyword not found")
	}
	return &productv1.DeleteBannedKeywordResponse{Ok: true}, nil
}

// PreviewBannedKeyword counts all non-deleted products matching a candidate term and returns up to 20 samples (E11).
func (s *Server) PreviewBannedKeyword(ctx context.Context, req *productv1.PreviewBannedKeywordRequest) (*productv1.PreviewBannedKeywordResponse, error) {
	if _, err := requireCapability(ctx, auth.CapBannedKeywordsRead); err != nil {
		return nil, err
	}
	term := strings.TrimSpace(req.GetTerm())
	compiled, ok := moderation.CompileRule(moderation.Rule{
		ID:     "preview",
		Term:   term,
		Action: moderation.ActionBlock,
	})
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "term must contain at least 2 alphanumeric characters")
	}

	ctx = store.WithAsOf(ctx, s.now())
	rules := []moderation.CompiledRule{compiled}
	var (
		affected int64
		samples  []*productv1.BannedContentProductSample
		cursor   string
	)
	for {
		batch, err := s.store.ListProductsForModerationScan(ctx, cursor, defaultRescanBatchSize)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "scan products for preview: %v", err)
		}
		if len(batch) == 0 {
			break
		}
		skusByProd, err := s.loadModerationSKUsByProducts(ctx, s.store, batch)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "load product skus for preview: %v", err)
		}
		for _, p := range batch {
			fields := collectProductModerationFields(p.Name, p.Description, productModerationSlug{
				ProductID: p.ID,
				Name:      p.Name,
				Slug:      p.Slug,
			}, skusByProd[p.ID])
			res := moderation.MatchCompiled(rules, fields...)
			if !res.Blocked {
				continue
			}
			affected++
			if len(samples) < maxModerationSamples {
				samples = append(samples, &productv1.BannedContentProductSample{
					ProductId:     p.ID,
					ShopId:        p.ShopID,
					Name:          p.Name,
					Slug:          p.Slug,
					Status:        toProtoProductStatus(p.Status),
					MatchedTerms:  res.MatchedTerms,
					HighestAction: productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_BLOCK,
				})
			}
		}
		if len(batch) < defaultRescanBatchSize {
			break
		}
		cursor = batch[len(batch)-1].ID
	}

	return &productv1.PreviewBannedKeywordResponse{
		AffectedCount: affected,
		Samples:       samples,
	}, nil
}

// RescanProductsForBannedContent scans products in cursor-paginated batches of up to 500 (E11).
//   - keyword_ids (when provided) only selects candidate products; flagged_keywords is always computed from ALL active keywords.
//   - Each candidate is re-evaluated inside its FOR UPDATE transaction against the latest locked row & SKUs.
//   - BLOCK rules / prohibited categories -> REJECTED + product.reviewed outbox (Vietnamese rejection_reason, idempotent on re-run).
//   - REVIEW rules -> never flip admin-approved products unless a NEW REVIEW keyword (not in flagged_keywords) matches;
//     never touch HIDDEN or REJECTED products.
func (s *Server) RescanProductsForBannedContent(ctx context.Context, req *productv1.RescanProductsForBannedContentRequest) (*productv1.RescanProductsForBannedContentResponse, error) {
	if _, err := requireCapability(ctx, auth.CapBannedKeywordsWrite); err != nil {
		return nil, err
	}

	limit := req.GetLimit()
	if limit <= 0 {
		limit = defaultRescanBatchSize
	}
	if limit > maxRescanBatchSize {
		limit = maxRescanBatchSize
	}
	cursor := strings.TrimSpace(req.GetCursor())

	now := s.now()
	ctx = store.WithAsOf(ctx, now)
	traceID, traceparent := traceMetaFromContext(ctx)

	keywordIDs := dedupeTrimmed(req.GetKeywordIds())
	keywordIDSet := make(map[string]bool, len(keywordIDs))
	for _, id := range keywordIDs {
		keywordIDSet[id] = true
	}

	// Always load ALL active keywords so flagged_keywords is never truncated to a subset.
	allActiveKWs, err := s.store.ListActiveBannedKeywords(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "load active banned keywords: %v", err)
	}
	allCompiledRules := compileStoreKeywords(allActiveKWs)

	var (
		candidateRules []moderation.CompiledRule
		prohibitedSet  map[string]bool
	)
	if len(keywordIDs) > 0 {
		candKWs := make([]store.BannedKeyword, 0, len(keywordIDs))
		for _, kw := range allActiveKWs {
			if keywordIDSet[kw.ID] {
				candKWs = append(candKWs, kw)
			}
		}
		candidateRules = compileStoreKeywords(candKWs)
	} else {
		candidateRules = allCompiledRules
		prohibitedIDs, pErr := s.store.ListProhibitedCategoryIDs(ctx)
		if pErr != nil {
			return nil, status.Errorf(codes.Internal, "load prohibited category ids: %v", pErr)
		}
		prohibitedSet = make(map[string]bool, len(prohibitedIDs))
		for _, id := range prohibitedIDs {
			prohibitedSet[id] = true
		}
	}

	// Fetch limit+1 rows to compute has_more + next_cursor accurately.
	rows, err := s.store.ListProductsForModerationScan(ctx, cursor, limit+1)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list products for moderation rescan: %v", err)
	}
	hasMore := false
	if int32(len(rows)) > limit {
		hasMore = true
		rows = rows[:limit]
	}
	nextCursor := ""
	if hasMore && len(rows) > 0 {
		nextCursor = rows[len(rows)-1].ID
	}

	skusByProd, err := s.loadModerationSKUsByProducts(ctx, s.store, rows)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "load product skus for moderation rescan: %v", err)
	}

	var (
		scannedCount  int32
		blockedCount  int32
		flaggedCount  int32
		samples       []*productv1.BannedContentProductSample
		emittedOutbox bool
	)

	for _, p := range rows {
		scannedCount++
		isProhibPre := prohibitedSet[p.CategoryID]
		candPre := evaluateStoredProductModeration(candidateRules, p, skusByProd[p.ID])
		if !candPre.hasAnyMatch() && !isProhibPre {
			continue
		}

		if req.GetDryRun() {
			fullPre := candPre
			if len(keywordIDs) > 0 {
				fullPre = evaluateStoredProductModeration(allCompiledRules, p, skusByProd[p.ID])
			}
			isBlock := len(fullPre.contentBlockedTerms) > 0 || isProhibPre
			if isBlock {
				if p.Status == store.ProductStatusRejected {
					continue
				}
				blockedCount++
				if len(samples) < maxModerationSamples {
					samples = append(samples, &productv1.BannedContentProductSample{
						ProductId:     p.ID,
						ShopId:        p.ShopID,
						Name:          p.Name,
						Slug:          p.Slug,
						Status:        toProtoProductStatus(p.Status),
						MatchedTerms:  buildMatchedTermsList(fullPre.matchedTerms, isProhibPre),
						HighestAction: productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_BLOCK,
					})
				}
				continue
			}
			if len(fullPre.effectiveReviewTerms) == 0 || p.Status == store.ProductStatusRejected || p.Status == store.ProductStatusHidden {
				continue
			}
			if !hasNewFlaggedKeywords(p.FlaggedKeywords, fullPre.effectiveReviewTerms) {
				continue
			}
			flaggedCount++
			if len(samples) < maxModerationSamples {
				samples = append(samples, &productv1.BannedContentProductSample{
					ProductId:     p.ID,
					ShopId:        p.ShopID,
					Name:          p.Name,
					Slug:          p.Slug,
					Status:        toProtoProductStatus(p.Status),
					MatchedTerms:  buildMatchedTermsList(fullPre.matchedTerms, false),
					HighestAction: productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_REVIEW,
				})
			}
			continue
		}

		// Non-dry-run: lock the product row and re-evaluate moderation strictly on the latest locked cur & SKUs.
		var (
			outcomeBlocked bool
			outcomeFlagged bool
			sample         *productv1.BannedContentProductSample
		)
		txErr := s.store.WithTx(ctx, func(q store.Queries) error {
			cur, getErr := q.GetProductByIDForUpdate(ctx, p.ID)
			if getErr != nil {
				if errors.Is(getErr, store.ErrNotFound) {
					return nil
				}
				return getErr
			}
			curSKUs, skuErr := q.ListSKUsByProductIDs(ctx, []string{cur.ID})
			if skuErr != nil {
				return skuErr
			}
			curModSKUs := toModerationSKUsFromStore(curSKUs)

			isProhibCur := false
			if len(keywordIDs) == 0 {
				var pErr error
				isProhibCur, pErr = q.IsCategoryProhibited(ctx, cur.CategoryID)
				if pErr != nil {
					return pErr
				}
			}

			candCur := evaluateStoredProductModeration(candidateRules, cur, curModSKUs)
			if !candCur.hasAnyMatch() && !isProhibCur {
				// Product was edited concurrently and no longer matches candidate criteria.
				return nil
			}
			fullCur := candCur
			if len(keywordIDs) > 0 {
				fullCur = evaluateStoredProductModeration(allCompiledRules, cur, curModSKUs)
			}

			isBlock := len(fullCur.contentBlockedTerms) > 0 || isProhibCur
			if isBlock {
				if cur.Status == store.ProductStatusRejected {
					return nil
				}
				reason := buildBlockRejectionReason(fullCur.allBlockedTerms, isProhibCur)
				upd, updErr := q.UpdateProductReview(ctx, cur.ID, store.ProductStatusRejected, reason, fullCur.flaggedOnlyTerms)
				if updErr != nil {
					return updErr
				}
				payloadBytes, mErr := json.Marshal(events.ProductReviewedPayload{
					ProductID:   upd.ID,
					ProductName: upd.Name,
					ShopID:      upd.ShopID,
					Decision:    "REJECT",
					Reason:      reason,
					OccurredAt:  now.Format(time.RFC3339),
				})
				if mErr != nil {
					return fmt.Errorf("marshal product.reviewed payload: %w", mErr)
				}
				if obErr := q.InsertOutboxEvent(ctx, store.OutboxEvent{
					ID:          uuid.NewString(),
					EventType:   events.SubjectProductReviewed,
					AggregateID: upd.ID,
					TraceID:     traceID,
					Traceparent: traceparent,
					PayloadJSON: payloadBytes,
					CreatedAt:   now,
				}); obErr != nil {
					return obErr
				}
				outcomeBlocked = true
				sample = &productv1.BannedContentProductSample{
					ProductId:     cur.ID,
					ShopId:        cur.ShopID,
					Name:          cur.Name,
					Slug:          cur.Slug,
					Status:        toProtoProductStatus(cur.Status),
					MatchedTerms:  buildMatchedTermsList(fullCur.matchedTerms, isProhibCur),
					HighestAction: productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_BLOCK,
				}
				return nil
			}

			if len(fullCur.effectiveReviewTerms) == 0 {
				return nil
			}
			// Never touch REJECTED or HIDDEN products for REVIEW keywords (Item 4).
			if cur.Status == store.ProductStatusRejected || cur.Status == store.ProductStatusHidden {
				return nil
			}

			// Item 3 & P1-1: Only demote APPROVED/OUT_OF_STOCK to PENDING if a NEW REVIEW (or slug-only BLOCK)
			// keyword (not already in cur.FlaggedKeywords) matched; if subset, preserve status and sync flagged_keywords.
			hasNew := hasNewFlaggedKeywords(cur.FlaggedKeywords, fullCur.effectiveReviewTerms)
			targetStatus := cur.Status
			if hasNew && (cur.Status == store.ProductStatusApproved || cur.Status == store.ProductStatusOutOfStock) {
				targetStatus = store.ProductStatusPending
			}
			if targetStatus == cur.Status && slices.Equal(cur.FlaggedKeywords, fullCur.effectiveReviewTerms) {
				return nil
			}
			if _, updErr := q.UpdateProductReview(ctx, cur.ID, targetStatus, cur.RejectionReason, fullCur.effectiveReviewTerms); updErr != nil {
				return updErr
			}
			if targetStatus == store.ProductStatusPending && (cur.Status != store.ProductStatusPending || hasNew) {
				outcomeFlagged = true
				sample = &productv1.BannedContentProductSample{
					ProductId:     cur.ID,
					ShopId:        cur.ShopID,
					Name:          cur.Name,
					Slug:          cur.Slug,
					Status:        toProtoProductStatus(cur.Status),
					MatchedTerms:  buildMatchedTermsList(fullCur.matchedTerms, false),
					HighestAction: productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_REVIEW,
				}
			}
			return nil
		})
		if txErr != nil {
			return nil, status.Errorf(codes.Internal, "rescan product %s: %v", p.ID, txErr)
		}
		if outcomeBlocked {
			emittedOutbox = true
			blockedCount++
			if sample != nil && len(samples) < maxModerationSamples {
				samples = append(samples, sample)
			}
		} else if outcomeFlagged {
			flaggedCount++
			if sample != nil && len(samples) < maxModerationSamples {
				samples = append(samples, sample)
			}
		}
	}

	if emittedOutbox {
		s.triggerOutbox()
	}

	return &productv1.RescanProductsForBannedContentResponse{
		ScannedCount: scannedCount,
		BlockedCount: blockedCount,
		FlaggedCount: flaggedCount,
		Samples:      samples,
		HasMore:      hasMore,
		NextCursor:   nextCursor,
	}, nil
}

// --- Shared moderation helpers used by CreateProduct, UpdateProduct, AdminReviewProduct, CreateReview ---

func (s *Server) loadCompiledModerationRules(ctx context.Context, q store.Queries) ([]moderation.CompiledRule, error) {
	kws, err := q.ListActiveBannedKeywords(ctx)
	if err != nil {
		return nil, err
	}
	return compileStoreKeywords(kws), nil
}

func compileStoreKeywords(kws []store.BannedKeyword) []moderation.CompiledRule {
	if len(kws) == 0 {
		return nil
	}
	rules := make([]moderation.Rule, 0, len(kws))
	for _, kw := range kws {
		rules = append(rules, moderation.Rule{
			ID:     kw.ID,
			Term:   kw.Term,
			Action: moderation.Action(kw.Action),
		})
	}
	return moderation.CompileRules(rules)
}

func toModerationSKUsFromStore(skus []store.SKU) []moderationSKU {
	out := make([]moderationSKU, 0, len(skus))
	for _, sk := range skus {
		out = append(out, moderationSKU{Name: sk.Name, Attributes: sk.Attributes})
	}
	return out
}

func toModerationSKUsFromInputs(in []normalizedSKUInput) []moderationSKU {
	out := make([]moderationSKU, 0, len(in))
	for _, sk := range in {
		out = append(out, moderationSKU{Name: sk.Name, Attributes: sk.Attributes})
	}
	return out
}

func (s *Server) loadModerationSKUsByProducts(ctx context.Context, q store.Queries, prods []store.Product) (map[string][]moderationSKU, error) {
	if len(prods) == 0 {
		return map[string][]moderationSKU{}, nil
	}
	ids := make([]string, len(prods))
	for i, p := range prods {
		ids[i] = p.ID
	}
	skus, err := q.ListSKUsByProductIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]moderationSKU, len(prods))
	for _, sk := range skus {
		out[sk.ProductID] = append(out[sk.ProductID], moderationSKU{
			Name:       sk.Name,
			Attributes: sk.Attributes,
		})
	}
	return out, nil
}

type productModerationSlug struct {
	ProductID   string
	Name        string
	Slug        string
	RawSlug     string
	PrevSlug    string
	HasPrevSlug bool
}

// isAutoGeneratedSlug reports whether slug matches the deterministic auto-slug generated from (name, productID):
// slugify(name, fallback) or slugify(name, fallback) + "-" + first 8 characters of productID.
//
// ponytail: without a persisted custom_slug column, renaming a product while keeping its old auto-slug
// causes later Rescan/AdminReview/Preview calls to treat the unchanged old slug as custom; Rescan and
// AdminReviewProduct(APPROVE) mitigate this via evaluateStoredProductModeration, which treats slug-only
// BLOCK matches as REVIEW (demote to PENDING, approvable by admin, persisted in flagged_keywords).
func isAutoGeneratedSlug(productID, name, slug string) bool {
	s := strings.TrimSpace(slug)
	if s == "" {
		return true
	}
	idPrefix := productID
	if len(idPrefix) > 8 {
		idPrefix = idPrefix[:8]
	}
	fallback := ""
	if idPrefix != "" {
		fallback = "prod-" + idPrefix
	}
	base := slugify(name, fallback)
	if base != "" && s == base {
		return true
	}
	if idPrefix != "" && base != "" && s == base+"-"+idPrefix {
		return true
	}
	return false
}

// moderationSlugFields is the single decision point shared by CreateProduct, UpdateProduct,
// AdminReviewProduct, PreviewBannedKeyword, and RescanProductsForBannedContent determining whether a
// product's slug should be scanned. Auto-generated slugs (and unchanged echoed slugs on UpdateProduct)
// are skipped so E9-bis accent-sensitive titles are never false-blocked by their ASCII auto-slug.
func moderationSlugFields(in productModerationSlug) []string {
	slug := strings.TrimSpace(in.Slug)
	rawSlug := strings.TrimSpace(in.RawSlug)
	if slug == "" && rawSlug == "" {
		return nil
	}
	if in.HasPrevSlug {
		prev := strings.TrimSpace(in.PrevSlug)
		if slug == prev && (rawSlug == "" || normalizeSlug(rawSlug) == prev) {
			return nil
		}
	}
	if isAutoGeneratedSlug(in.ProductID, in.Name, slug) &&
		(rawSlug == "" || isAutoGeneratedSlug(in.ProductID, in.Name, normalizeSlug(rawSlug))) {
		return nil
	}
	out := make([]string, 0, 2)
	if slug != "" {
		out = append(out, slug)
	}
	if rawSlug != "" && rawSlug != slug {
		out = append(out, rawSlug)
	}
	return out
}

// collectProductContentFields collects all non-slug user-authored text fields scanned by content moderation:
// product name, description, SKU names, and SKU attribute keys & values. sku_code and image URLs are excluded.
func collectProductContentFields(name, description string, skus []moderationSKU) []string {
	fields := make([]string, 0, 2+len(skus)*2)
	fields = append(fields, name, description)
	for _, sk := range skus {
		if n := strings.TrimSpace(sk.Name); n != "" {
			fields = append(fields, n)
		}
		if len(sk.Attributes) > 0 {
			keys := make([]string, 0, len(sk.Attributes))
			for k := range sk.Attributes {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if tk := strings.TrimSpace(k); tk != "" {
					fields = append(fields, tk)
				}
				if tv := strings.TrimSpace(sk.Attributes[k]); tv != "" {
					fields = append(fields, tv)
				}
			}
		}
	}
	return fields
}

// collectProductModerationFields collects all user-authored text fields scanned across
// CreateProduct, UpdateProduct, AdminReviewProduct, PreviewBannedKeyword, and RescanProductsForBannedContent:
// product name, description, custom slug (via moderationSlugFields), SKU names, and SKU attribute keys & values.
// sku_code and image URLs are excluded.
func collectProductModerationFields(name, description string, slugIn productModerationSlug, skus []moderationSKU) []string {
	content := collectProductContentFields(name, description, skus)
	slugFields := moderationSlugFields(slugIn)
	if len(slugFields) == 0 {
		return content
	}
	return append(content, slugFields...)
}

type storedProductModerationMatch struct {
	contentBlockedTerms  []string
	allBlockedTerms      []string
	flaggedOnlyTerms     []string
	effectiveReviewTerms []string
	matchedTerms         []string
}

func (m storedProductModerationMatch) hasAnyMatch() bool {
	return len(m.matchedTerms) > 0
}

// evaluateStoredProductModeration evaluates content fields separately from custom slug fields
// (shared by RescanProductsForBannedContent and AdminReviewProduct(APPROVE)) so that a BLOCK match
// occurring ONLY on the slug (e.g. after a product rename kept its old ASCII auto-slug) is treated
// as REVIEW: Rescan demotes to PENDING instead of auto-REJECTing, and admin can inspect & APPROVE it
// with the slug terms recorded in flagged_keywords.
func evaluateStoredProductModeration(rules []moderation.CompiledRule, p store.Product, skus []moderationSKU) storedProductModerationMatch {
	contentRes := moderation.MatchCompiled(rules, collectProductContentFields(p.Name, p.Description, skus)...)
	slugFields := moderationSlugFields(productModerationSlug{
		ProductID: p.ID,
		Name:      p.Name,
		Slug:      p.Slug,
	})
	var slugRes moderation.MatchResult
	if len(slugFields) > 0 {
		slugRes = moderation.MatchCompiled(rules, slugFields...)
	}

	flaggedOnly := unionSortedStrings(contentRes.FlaggedTerms, slugRes.FlaggedTerms)
	allBlocked := unionSortedStrings(contentRes.BlockedTerms, slugRes.BlockedTerms)
	effectiveReview := flaggedOnly
	if len(contentRes.BlockedTerms) == 0 && len(slugRes.BlockedTerms) > 0 {
		effectiveReview = unionSortedStrings(flaggedOnly, slugRes.BlockedTerms)
	}
	return storedProductModerationMatch{
		contentBlockedTerms:  contentRes.BlockedTerms,
		allBlockedTerms:      allBlocked,
		flaggedOnlyTerms:     flaggedOnly,
		effectiveReviewTerms: effectiveReview,
		matchedTerms:         unionSortedStrings(contentRes.MatchedTerms, slugRes.MatchedTerms),
	}
}

func unionSortedStrings(slicesIn ...[]string) []string {
	seen := make(map[string]struct{})
	for _, sl := range slicesIn {
		for _, s := range sl {
			seen[s] = struct{}{}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// hasNewFlaggedKeywords reports whether matched contains any REVIEW term not already present in existing.
func hasNewFlaggedKeywords(existing, matched []string) bool {
	if len(matched) == 0 {
		return false
	}
	seen := make(map[string]struct{}, len(existing))
	for _, k := range existing {
		seen[k] = struct{}{}
	}
	for _, k := range matched {
		if _, ok := seen[k]; !ok {
			return true
		}
	}
	return false
}

// intersectFlaggedKeywords returns terms present in both existing and matched (preserving matched order).
func intersectFlaggedKeywords(existing, matched []string) []string {
	if len(existing) == 0 || len(matched) == 0 {
		return nil
	}
	allowed := make(map[string]struct{}, len(existing))
	for _, k := range existing {
		allowed[k] = struct{}{}
	}
	out := make([]string, 0, len(matched))
	for _, k := range matched {
		if _, ok := allowed[k]; ok {
			out = append(out, k)
		}
	}
	return out
}

func moderationContentUnchanged(
	existing store.Product,
	existingSKUs []moderationSKU,
	name, desc, slug string,
	incomingSKUs []moderationSKU,
) bool {
	if len(existingSKUs) != len(incomingSKUs) {
		return false
	}
	if strings.TrimSpace(existing.Slug) != strings.TrimSpace(slug) {
		return false
	}
	oldFields := collectProductContentFields(existing.Name, existing.Description, canonicalModerationSKUs(existingSKUs))
	newFields := collectProductContentFields(name, desc, canonicalModerationSKUs(incomingSKUs))
	return slices.Equal(oldFields, newFields)
}

func canonicalModerationSKUs(skus []moderationSKU) []moderationSKU {
	cp := make([]moderationSKU, len(skus))
	copy(cp, skus)
	sort.Slice(cp, func(i, j int) bool {
		return compareModerationSKU(cp[i], cp[j]) < 0
	})
	return cp
}

func compareModerationSKU(a, b moderationSKU) int {
	if c := strings.Compare(strings.TrimSpace(a.Name), strings.TrimSpace(b.Name)); c != 0 {
		return c
	}
	ka := sortedAttrKeys(a.Attributes)
	kb := sortedAttrKeys(b.Attributes)
	n := min(len(ka), len(kb))
	for i := 0; i < n; i++ {
		if c := strings.Compare(strings.TrimSpace(ka[i]), strings.TrimSpace(kb[i])); c != 0 {
			return c
		}
		if c := strings.Compare(strings.TrimSpace(a.Attributes[ka[i]]), strings.TrimSpace(b.Attributes[kb[i]])); c != 0 {
			return c
		}
	}
	return len(ka) - len(kb)
}

func sortedAttrKeys(attrs map[string]string) []string {
	if len(attrs) == 0 {
		return nil
	}
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (s *Server) checkProductModeration(
	ctx context.Context,
	q store.Queries,
	categoryID, name, description string,
	slugIn productModerationSlug,
	skus []moderationSKU,
) (moderation.MatchResult, error) {
	prohibited, err := q.IsCategoryProhibited(ctx, categoryID)
	if err != nil {
		return moderation.MatchResult{}, status.Errorf(codes.Internal, "check category prohibition: %v", err)
	}
	if prohibited {
		return moderation.MatchResult{}, status.Error(codes.InvalidArgument, "PROHIBITED_CATEGORY: products cannot be listed or approved in a prohibited category")
	}

	rules, err := s.loadCompiledModerationRules(ctx, q)
	if err != nil {
		return moderation.MatchResult{}, status.Errorf(codes.Internal, "load moderation rules: %v", err)
	}
	if len(rules) == 0 {
		return moderation.MatchResult{}, nil
	}

	res := moderation.MatchCompiled(rules, collectProductModerationFields(name, description, slugIn, skus)...)
	if res.Blocked {
		return res, status.Errorf(codes.InvalidArgument, "BANNED_CONTENT: product contains banned keywords: %s", strings.Join(res.BlockedTerms, ", "))
	}
	return res, nil
}

// checkStoredProductApprovalModeration validates a stored product for AdminReviewProduct(APPROVE)
// using the same evaluateStoredProductModeration semantics as RescanProductsForBannedContent:
// prohibited categories and BLOCK keywords in name/description/SKUs still block approval,
// while BLOCK keywords matching ONLY on the slug are treated as REVIEW and recorded in flagged_keywords.
func (s *Server) checkStoredProductApprovalModeration(
	ctx context.Context,
	q store.Queries,
	p store.Product,
	skus []moderationSKU,
) (storedProductModerationMatch, error) {
	prohibited, err := q.IsCategoryProhibited(ctx, p.CategoryID)
	if err != nil {
		return storedProductModerationMatch{}, status.Errorf(codes.Internal, "check category prohibition: %v", err)
	}
	if prohibited {
		return storedProductModerationMatch{}, status.Error(codes.InvalidArgument, "PROHIBITED_CATEGORY: products cannot be listed or approved in a prohibited category")
	}

	rules, err := s.loadCompiledModerationRules(ctx, q)
	if err != nil {
		return storedProductModerationMatch{}, status.Errorf(codes.Internal, "load moderation rules: %v", err)
	}
	if len(rules) == 0 {
		return storedProductModerationMatch{}, nil
	}

	match := evaluateStoredProductModeration(rules, p, skus)
	if len(match.contentBlockedTerms) > 0 {
		return match, status.Errorf(codes.InvalidArgument, "BANNED_CONTENT: product contains banned keywords: %s", strings.Join(match.contentBlockedTerms, ", "))
	}
	return match, nil
}

func buildMatchedTermsList(matched []string, isProhibitedCat bool) []string {
	out := make([]string, 0, len(matched)+1)
	out = append(out, matched...)
	if isProhibitedCat {
		out = append(out, prohibitedCategoryTag)
	}
	return out
}

func buildBlockRejectionReason(blockedTerms []string, isProhibitedCat bool) string {
	switch {
	case isProhibitedCat && len(blockedTerms) > 0:
		return fmt.Sprintf("Danh mục thuộc diện cấm kinh doanh và nội dung chứa từ khoá cấm: %s", strings.Join(blockedTerms, ", "))
	case isProhibitedCat:
		return "Danh mục thuộc diện cấm kinh doanh"
	default:
		return fmt.Sprintf("Nội dung chứa từ khoá cấm: %s", strings.Join(blockedTerms, ", "))
	}
}

func dedupeTrimmed(raw []string) []string {
	if len(raw) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		v := strings.TrimSpace(item)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; !ok {
			seen[v] = struct{}{}
			out = append(out, v)
		}
	}
	return out
}

func toProtoBannedKeyword(k store.BannedKeyword) *productv1.BannedKeyword {
	return &productv1.BannedKeyword{
		Id:             k.ID,
		Term:           k.Term,
		NormalizedTerm: k.NormalizedTerm,
		Category:       k.Category,
		Action:         toProtoBannedKeywordAction(k.Action),
		IsActive:       k.IsActive,
		CreatedBy:      k.CreatedBy,
		CreatedAt:      timestamppb.New(k.CreatedAt),
		UpdatedAt:      timestamppb.New(k.UpdatedAt),
	}
}

func toProtoBannedKeywordAction(a string) productv1.BannedKeywordAction {
	switch a {
	case store.BannedKeywordActionBlock:
		return productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_BLOCK
	case store.BannedKeywordActionReview:
		return productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_REVIEW
	default:
		return productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_UNSPECIFIED
	}
}

func fromProtoBannedKeywordAction(a productv1.BannedKeywordAction) string {
	switch a {
	case productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_BLOCK:
		return store.BannedKeywordActionBlock
	case productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_REVIEW:
		return store.BannedKeywordActionReview
	default:
		return ""
	}
}
