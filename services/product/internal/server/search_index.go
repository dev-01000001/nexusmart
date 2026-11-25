package server

import (
	"context"
	"encoding/json"
	"fmt"
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
	"github.com/nexusmart/core/services/product/internal/store"
)

const (
	defaultIndexPageSize = 200
	maxIndexPageSize     = 500
)

func isPublicProductStatusStr(st string) bool {
	return st == store.ProductStatusApproved || st == store.ProductStatusOutOfStock
}

// ListProductsForIndex returns keyset-paginated public products (APPROVED / OUT_OF_STOCK, not deleted)
// for SearchService full reindex (B1, G9, G11).
// Strictly allows service tokens or staff with search.reindex / products.read; buyers and sellers receive PermissionDenied.
func (s *Server) ListProductsForIndex(ctx context.Context, req *productv1.ListProductsForIndexRequest) (*productv1.ListProductsForIndexResponse, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}
	if strings.TrimSpace(claims.Service) == "" &&
		!auth.HasCapability(claims, auth.CapSearchReindex) &&
		!auth.HasCapability(claims, auth.CapProductsRead) {
		return nil, status.Error(codes.PermissionDenied, "service identity or search.reindex/products.read capability required")
	}

	afterID := strings.TrimSpace(req.GetAfterId())
	pageSize := req.GetPageSize()
	if pageSize <= 0 {
		pageSize = defaultIndexPageSize
	}
	if pageSize > maxIndexPageSize {
		pageSize = maxIndexPageSize
	}

	ctx = store.WithAsOf(ctx, s.now())
	rows, err := s.store.ListProductsForIndex(ctx, afterID, pageSize+1)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list products for index: %v", err)
	}

	hasMore := false
	if int32(len(rows)) > pageSize {
		hasMore = true
		rows = rows[:pageSize]
	}
	nextCursor := ""
	if hasMore && len(rows) > 0 {
		nextCursor = rows[len(rows)-1].ID
	}
	if len(rows) == 0 {
		return &productv1.ListProductsForIndexResponse{
			Items:      []*productv1.IndexableProduct{},
			NextCursor: "",
			HasMore:    false,
		}, nil
	}

	prodIDs := make([]string, len(rows))
	for i, p := range rows {
		prodIDs[i] = p.ID
	}
	allSKUs, err := s.store.ListSKUsByProductIDs(ctx, prodIDs)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "load skus for index: %v", err)
	}
	skusByProd := make(map[string][]store.SKU, len(rows))
	for _, sk := range allSKUs {
		skusByProd[sk.ProductID] = append(skusByProd[sk.ProductID], sk)
	}

	allCats, err := s.store.ListCategories(ctx, "")
	if err != nil {
		return nil, status.Errorf(codes.Internal, "load categories for index: %v", err)
	}
	parentByCatID := make(map[string]string, len(allCats))
	for _, c := range allCats {
		parentByCatID[c.ID] = c.ParentID
	}

	items := make([]*productv1.IndexableProduct, 0, len(rows))
	for _, p := range rows {
		ancestors := resolveCategoryAncestorsFromParents(p.CategoryID, parentByCatID)
		proj := buildProductIndexProjection(p, skusByProd[p.ID], ancestors, p.UpdatedAt)
		items = append(items, proj.toProto())
	}

	return &productv1.ListProductsForIndexResponse{
		Items:      items,
		NextCursor: nextCursor,
		HasMore:    hasMore,
	}, nil
}

func resolveCategoryAncestorsFromParents(categoryID string, parentByID map[string]string) []string {
	out := make([]string, 0, 4)
	visited := make(map[string]bool)
	cur := categoryID
	for cur != "" && len(out) < 32 {
		if visited[cur] {
			break
		}
		if _, exists := parentByID[cur]; !exists {
			if len(out) == 0 {
				out = append(out, cur)
			}
			break
		}
		visited[cur] = true
		out = append(out, cur)
		cur = parentByID[cur]
	}
	return out
}

type productIndexProjection struct {
	id                  string
	shopID              string
	categoryID          string
	categoryAncestorIDs []string
	name                string
	slug                string
	description         string
	thumbnailURL        string
	minPriceVND         int64
	maxPriceVND         int64
	status              string
	skuCodes            []string
	skuAttributesText   string
	totalStock          int32
	skuCount            int32
	updatedAt           time.Time
	skuIDs              []string
	defaultSKUID        string
	colors              []string
	colorCodes          []string
	sizes               []string
	ratingAvgX100       int32
	reviewCount         int32
	shopSuspended       bool
}

func buildProductIndexProjection(p store.Product, skus []store.SKU, ancestorIDs []string, updatedAt time.Time) productIndexProjection {
	if updatedAt.IsZero() {
		updatedAt = p.UpdatedAt
	}
	updatedAt = updatedAt.UTC()

	ancestors := make([]string, 0, len(ancestorIDs))
	ancestors = append(ancestors, ancestorIDs...)
	if len(ancestors) == 0 && p.CategoryID != "" {
		ancestors = append(ancestors, p.CategoryID)
	}

	thumbnail := ""
	for _, img := range p.Images {
		if u := strings.TrimSpace(img); u != "" {
			thumbnail = u
			break
		}
	}

	minPrice := p.MinPriceVND
	maxPrice := p.MaxPriceVND
	skuIDs := make([]string, 0, len(skus))
	skuCodes := make([]string, 0, len(skus))
	colors := make([]string, 0)
	colorCodes := make([]string, 0)
	sizes := make([]string, 0)
	seenColors := make(map[string]bool)
	seenColorCodes := make(map[string]bool)
	seenSizes := make(map[string]bool)
	attrTokens := make([]string, 0, len(skus)*3)
	seenAttrTokens := make(map[string]bool)

	var (
		totalStock   int32
		defaultSKUID string
	)
	addAttrToken := func(raw string) {
		t := strings.TrimSpace(raw)
		if t == "" {
			return
		}
		key := strings.ToLower(t)
		if !seenAttrTokens[key] {
			seenAttrTokens[key] = true
			attrTokens = append(attrTokens, t)
		}
	}

	for i, sk := range skus {
		skuIDs = append(skuIDs, sk.ID)
		if code := strings.TrimSpace(sk.SKUCode); code != "" {
			skuCodes = append(skuCodes, code)
		}
		if i == 0 || sk.PriceVND < minPrice {
			minPrice = sk.PriceVND
		}
		if i == 0 || sk.PriceVND > maxPrice {
			maxPrice = sk.PriceVND
		}
		if sk.Stock > 0 {
			totalStock += sk.Stock
			if defaultSKUID == "" {
				defaultSKUID = sk.ID
			}
		}
		if thumbnail == "" && strings.TrimSpace(sk.ImageURL) != "" {
			thumbnail = strings.TrimSpace(sk.ImageURL)
		}
		if sn := strings.TrimSpace(sk.Name); sn != "" && !strings.EqualFold(sn, "default") {
			addAttrToken(sn)
		}
		if len(sk.Attributes) > 0 {
			keys := make([]string, 0, len(sk.Attributes))
			for k := range sk.Attributes {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				v := strings.TrimSpace(sk.Attributes[k])
				if v == "" {
					continue
				}
				addAttrToken(v)
				normKey := normalizeAttributeFacetKey(k)
				switch normKey {
				case "color", "colour", "mau", "mau_sac", "mausac":
					lk := strings.ToLower(v)
					if !seenColors[lk] {
						seenColors[lk] = true
						colors = append(colors, v)
					}
				case "color_code", "colorcode", "colour_code", "colourcode", "ma_mau", "mamau":
					if code := strings.ToLower(v); !seenColorCodes[code] {
						seenColorCodes[code] = true
						colorCodes = append(colorCodes, code)
					}
				case "size", "kich_co", "kichco", "kich_thuoc", "kichthuoc":
					lk := strings.ToLower(v)
					if !seenSizes[lk] {
						seenSizes[lk] = true
						sizes = append(sizes, v)
					}
				}
			}
		}
	}
	if defaultSKUID == "" && len(skus) > 0 {
		defaultSKUID = skus[0].ID
	}

	return productIndexProjection{
		id:                  p.ID,
		shopID:              p.ShopID,
		categoryID:          p.CategoryID,
		categoryAncestorIDs: ancestors,
		name:                p.Name,
		slug:                p.Slug,
		description:         p.Description,
		thumbnailURL:        thumbnail,
		minPriceVND:         minPrice,
		maxPriceVND:         maxPrice,
		status:              p.Status,
		skuCodes:            skuCodes,
		skuAttributesText:   strings.Join(attrTokens, " "),
		totalStock:          totalStock,
		skuCount:            int32(len(skus)),
		updatedAt:           updatedAt,
		skuIDs:              skuIDs,
		defaultSKUID:        defaultSKUID,
		colors:              colors,
		colorCodes:          colorCodes,
		sizes:               sizes,
		ratingAvgX100:       p.RatingAvgX100(),
		reviewCount:         p.ReviewCount,
		shopSuspended:       p.ShopSuspended,
	}
}

func normalizeAttributeFacetKey(k string) string {
	norm := store.NormalizeSearchText(k)
	norm = strings.ReplaceAll(norm, "-", "_")
	norm = strings.ReplaceAll(norm, " ", "_")
	return norm
}

func (p productIndexProjection) toProto() *productv1.IndexableProduct {
	return &productv1.IndexableProduct{
		Id:                  p.id,
		ShopId:              p.shopID,
		CategoryId:          p.categoryID,
		CategoryAncestorIds: p.categoryAncestorIDs,
		Name:                p.name,
		Slug:                p.slug,
		Description:         p.description,
		ThumbnailUrl:        p.thumbnailURL,
		MinPriceVnd:         p.minPriceVND,
		MaxPriceVnd:         p.maxPriceVND,
		Status:              toProtoProductStatus(p.status),
		SkuCodes:            p.skuCodes,
		SkuAttributesText:   p.skuAttributesText,
		TotalStock:          p.totalStock,
		SkuCount:            p.skuCount,
		UpdatedAt:           timestamppb.New(p.updatedAt),
		SkuIds:              p.skuIDs,
		Colors:              p.colors,
		ColorCodes:          p.colorCodes,
		Sizes:               p.sizes,
		RatingAvgX100:       p.ratingAvgX100,
		ReviewCount:         p.reviewCount,
		DefaultSkuId:        p.defaultSKUID,
		ShopSuspended:       p.shopSuspended,
	}
}

func (p productIndexProjection) toOutboxPayload() events.ProductUpsertedPayload {
	return events.ProductUpsertedPayload{
		ProductID:           p.id,
		ShopID:              p.shopID,
		CategoryID:          p.categoryID,
		CategoryAncestorIDs: p.categoryAncestorIDs,
		Name:                p.name,
		Slug:                p.slug,
		Description:         p.description,
		ThumbnailURL:        p.thumbnailURL,
		MinPriceVND:         p.minPriceVND,
		MaxPriceVND:         p.maxPriceVND,
		Status:              p.status,
		SKUCodes:            p.skuCodes,
		SKUAttributesText:   p.skuAttributesText,
		TotalStock:          p.totalStock,
		SKUCount:            p.skuCount,
		SKUIDs:              p.skuIDs,
		DefaultSKUID:        p.defaultSKUID,
		Colors:              p.colors,
		ColorCodes:          p.colorCodes,
		Sizes:               p.sizes,
		RatingAvgX100:       p.ratingAvgX100,
		ReviewCount:         p.reviewCount,
		ShopSuspended:       p.shopSuspended,
		UpdatedAt:           p.updatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func emitProductUpsertedOutbox(
	ctx context.Context,
	q store.Queries,
	prod store.Product,
	skus []store.SKU,
	eventUpdatedAt time.Time,
	createdAt time.Time,
) error {
	ancestors, err := q.GetCategoryAncestors(ctx, prod.CategoryID)
	if err != nil {
		return fmt.Errorf("load category ancestors for product.upserted: %w", err)
	}
	proj := buildProductIndexProjection(prod, skus, ancestors, eventUpdatedAt)
	payloadBytes, err := json.Marshal(proj.toOutboxPayload())
	if err != nil {
		return fmt.Errorf("marshal product.upserted payload: %w", err)
	}
	traceID, traceparent := traceMetaFromContext(ctx)
	return q.InsertOutboxEvent(ctx, store.OutboxEvent{
		ID:          uuid.NewString(),
		EventType:   events.SubjectProductUpserted,
		AggregateID: prod.ID,
		TraceID:     traceID,
		Traceparent: traceparent,
		PayloadJSON: payloadBytes,
		CreatedAt:   createdAt.UTC(),
	})
}

func emitProductDeletedOutbox(
	ctx context.Context,
	q store.Queries,
	productID, shopID, reason string,
	eventUpdatedAt time.Time,
	createdAt time.Time,
) error {
	if eventUpdatedAt.IsZero() {
		eventUpdatedAt = createdAt
	}
	ver, err := q.NextProductEventVersion(ctx)
	if err != nil {
		return fmt.Errorf("next product event version: %w", err)
	}
	payloadBytes, err := json.Marshal(events.ProductDeletedPayload{
		ProductID:  productID,
		ShopID:     shopID,
		Reason:     reason,
		UpdatedAt:  eventUpdatedAt.UTC().Format(time.RFC3339Nano),
		Version:    ver,
		OccurredAt: createdAt.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return fmt.Errorf("marshal product.deleted payload: %w", err)
	}
	traceID, traceparent := traceMetaFromContext(ctx)
	return q.InsertOutboxEvent(ctx, store.OutboxEvent{
		ID:          uuid.NewString(),
		EventType:   events.SubjectProductDeleted,
		AggregateID: productID,
		TraceID:     traceID,
		Traceparent: traceparent,
		PayloadJSON: payloadBytes,
		CreatedAt:   createdAt.UTC(),
	})
}

func emitCategoryUpdatedOutbox(
	ctx context.Context,
	q store.Queries,
	cat store.Category,
	action string,
	eventUpdatedAt time.Time,
	createdAt time.Time,
) error {
	if eventUpdatedAt.IsZero() {
		eventUpdatedAt = cat.UpdatedAt
	}
	if eventUpdatedAt.IsZero() {
		eventUpdatedAt = createdAt
	}
	payloadBytes, err := json.Marshal(events.ProductCategoryUpdatedPayload{
		CategoryID: cat.ID,
		Name:       cat.Name,
		Slug:       cat.Slug,
		ParentID:   cat.ParentID,
		SortOrder:  cat.SortOrder,
		Action:     action,
		UpdatedAt:  eventUpdatedAt.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return fmt.Errorf("marshal product.category_updated payload: %w", err)
	}
	traceID, traceparent := traceMetaFromContext(ctx)
	return q.InsertOutboxEvent(ctx, store.OutboxEvent{
		ID:          uuid.NewString(),
		EventType:   events.SubjectProductCategoryUpdated,
		AggregateID: "cat:" + cat.ID,
		TraceID:     traceID,
		Traceparent: traceparent,
		PayloadJSON: payloadBytes,
		CreatedAt:   createdAt.UTC(),
	})
}

func emitProductStockSyncUpsertedIfChanged(
	ctx context.Context,
	q store.Queries,
	prodID string,
	beforeStatus string,
	beforeInStock bool,
	now time.Time,
) (bool, error) {
	if !isPublicProductStatusStr(beforeStatus) {
		return false, nil
	}
	afterInStockCount, err := q.CountActiveInStockSKUsByProduct(ctx, prodID)
	if err != nil {
		return false, err
	}
	afterInStock := afterInStockCount > 0
	refProd, err := q.GetProductByID(ctx, prodID)
	if err != nil {
		return false, err
	}
	if !isPublicProductStatusStr(refProd.Status) {
		return false, nil
	}
	if refProd.Status == beforeStatus && afterInStock == beforeInStock {
		return false, nil
	}
	// If only in_stock changed without a status transition, bump updated_at monotonically.
	if refProd.Status == beforeStatus {
		if err := q.UpdateProductStatus(ctx, prodID, refProd.Status); err != nil {
			return false, err
		}
		refProd, err = q.GetProductByID(ctx, prodID)
		if err != nil {
			return false, err
		}
	}
	skus, err := q.ListSKUsByProductIDs(ctx, []string{prodID})
	if err != nil {
		return false, err
	}
	if err := emitProductUpsertedOutbox(ctx, q, refProd, skus, refProd.UpdatedAt, now); err != nil {
		return false, err
	}
	return true, nil
}
