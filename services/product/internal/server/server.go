package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/product/internal/events"
	"github.com/nexusmart/core/services/product/internal/store"
)

const (
	defaultPageSize      = 20
	maxPageSize          = 100
	maxOptimisticRetries = 16
	retryBaseDelay       = 5 * time.Millisecond
)

// Server implements productv1.ProductServiceServer.
type Server struct {
	productv1.UnimplementedProductServiceServer
	store          store.Store
	logger         *slog.Logger
	nowFn          func() time.Time
	notifierMu     sync.RWMutex
	outboxNotifier func()
}

// New constructs a ProductService gRPC server.
func New(st store.Store, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		store:  st,
		logger: logger,
		nowFn:  func() time.Time { return time.Now().UTC() },
	}
}

// SetClock overrides the wall clock function (for deterministic unit tests).
func (s *Server) SetClock(fn func() time.Time) {
	if fn != nil {
		s.nowFn = fn
	}
}

// SetOutboxNotifier registers an optional callback invoked after a transaction commits outbox events.
func (s *Server) SetOutboxNotifier(fn func()) {
	s.notifierMu.Lock()
	s.outboxNotifier = fn
	s.notifierMu.Unlock()
}

func (s *Server) notifyOutbox() {
	s.notifierMu.RLock()
	fn := s.outboxNotifier
	s.notifierMu.RUnlock()
	if fn != nil {
		fn()
	}
}

func (s *Server) enqueueProductEvent(ctx context.Context, q store.Queries, eventType, productID, shopID string) error {
	version, err := q.NextProductEventVersion(ctx)
	if err != nil {
		return fmt.Errorf("next product event version: %w", err)
	}
	occurredAt := s.nowFn().UTC()
	payloadBytes, err := json.Marshal(events.ProductEventPayload{
		ProductID:  productID,
		ShopID:     shopID,
		Version:    version,
		OccurredAt: occurredAt.Format(time.RFC3339Nano),
	})
	if err != nil {
		return fmt.Errorf("marshal product event payload: %w", err)
	}
	traceID, traceparent := traceMetaFromContext(ctx)
	return q.InsertOutboxEvent(ctx, store.OutboxEvent{
		ID:          uuid.NewString(),
		EventType:   eventType,
		TraceID:     traceID,
		Traceparent: traceparent,
		PayloadJSON: payloadBytes,
		CreatedAt:   occurredAt,
	})
}

func traceMetaFromContext(ctx context.Context) (string, string) {
	traceID := telemetry.TraceIDFromContext(ctx)
	carrier := map[string]string{}
	telemetry.InjectTraceHeaders(ctx, carrier)
	return traceID, carrier["traceparent"]
}

// --- Categories ---

func (s *Server) CreateCategory(ctx context.Context, req *productv1.CreateCategoryRequest) (*productv1.CreateCategoryResponse, error) {
	if _, err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.GetName())
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "category name is required")
	}
	if req.GetCommissionRateBps() < 0 || req.GetCommissionRateBps() > 10000 {
		return nil, status.Error(codes.InvalidArgument, "commission_rate_bps must be between 0 and 10000")
	}

	parentID := strings.TrimSpace(req.GetParentId())
	if parentID != "" {
		if _, err := s.store.GetCategoryByID(ctx, parentID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil, status.Error(codes.NotFound, "parent category not found")
			}
			return nil, status.Errorf(codes.Internal, "lookup parent category: %v", err)
		}
	}

	id := uuid.NewString()
	slug := strings.TrimSpace(req.GetSlug())
	if slug == "" {
		slug = slugify(name, id[:8])
	}

	cat, err := s.store.CreateCategory(ctx, store.Category{
		ID:                id,
		Name:              name,
		Slug:              slug,
		ParentID:          parentID,
		CommissionRateBps: req.GetCommissionRateBps(),
		SortOrder:         req.GetSortOrder(),
	})
	if err != nil {
		if errors.Is(err, store.ErrAlreadyExists) {
			return nil, status.Error(codes.AlreadyExists, "category slug already exists")
		}
		return nil, status.Errorf(codes.Internal, "create category: %v", err)
	}
	return &productv1.CreateCategoryResponse{Category: toProtoCategory(cat)}, nil
}

func (s *Server) UpdateCategory(ctx context.Context, req *productv1.UpdateCategoryRequest) (*productv1.UpdateCategoryResponse, error) {
	if _, err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	id := strings.TrimSpace(req.GetId())
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "category id is required")
	}
	if req.GetCommissionRateBps() < 0 || req.GetCommissionRateBps() > 10000 {
		return nil, status.Error(codes.InvalidArgument, "commission_rate_bps must be between 0 and 10000")
	}

	existing, err := s.store.GetCategoryByID(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "category not found")
		}
		return nil, status.Errorf(codes.Internal, "get category: %v", err)
	}

	name := strings.TrimSpace(req.GetName())
	if name == "" {
		name = existing.Name
	}
	slug := strings.TrimSpace(req.GetSlug())
	if slug == "" {
		slug = existing.Slug
	}
	parentID := strings.TrimSpace(req.GetParentId())
	if parentID == id {
		return nil, status.Error(codes.InvalidArgument, "category cannot be its own parent")
	}
	if parentID != "" {
		if _, err := s.store.GetCategoryByID(ctx, parentID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil, status.Error(codes.NotFound, "parent category not found")
			}
			return nil, status.Errorf(codes.Internal, "lookup parent category: %v", err)
		}
	}

	updated, err := s.store.UpdateCategory(ctx, store.Category{
		ID:                id,
		Name:              name,
		Slug:              slug,
		ParentID:          parentID,
		CommissionRateBps: req.GetCommissionRateBps(),
		SortOrder:         req.GetSortOrder(),
	})
	if err != nil {
		if errors.Is(err, store.ErrAlreadyExists) {
			return nil, status.Error(codes.AlreadyExists, "category slug already exists")
		}
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "category not found")
		}
		return nil, status.Errorf(codes.Internal, "update category: %v", err)
	}
	return &productv1.UpdateCategoryResponse{Category: toProtoCategory(updated)}, nil
}

func (s *Server) DeleteCategory(ctx context.Context, req *productv1.DeleteCategoryRequest) (*productv1.DeleteCategoryResponse, error) {
	if _, err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	id := strings.TrimSpace(req.GetId())
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "category id is required")
	}

	if _, err := s.store.GetCategoryByID(ctx, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "category not found")
		}
		return nil, status.Errorf(codes.Internal, "get category: %v", err)
	}

	prodCount, err := s.store.CountProductsInCategory(ctx, id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "count products in category: %v", err)
	}
	if prodCount > 0 {
		return nil, status.Errorf(codes.FailedPrecondition, "category still has %d products; reassign products before deleting", prodCount)
	}

	childCount, err := s.store.CountChildCategories(ctx, id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "count child categories: %v", err)
	}
	if childCount > 0 {
		return nil, status.Errorf(codes.FailedPrecondition, "category still has %d child categories", childCount)
	}

	deleted, err := s.store.DeleteCategory(ctx, id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "delete category: %v", err)
	}
	if !deleted {
		return nil, status.Error(codes.NotFound, "category not found")
	}
	return &productv1.DeleteCategoryResponse{Ok: true}, nil
}

func (s *Server) ListCategories(ctx context.Context, req *productv1.ListCategoriesRequest) (*productv1.ListCategoriesResponse, error) {
	cats, err := s.store.ListCategories(ctx, strings.TrimSpace(req.GetParentId()))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list categories: %v", err)
	}
	out := make([]*productv1.Category, 0, len(cats))
	for _, c := range cats {
		out = append(out, toProtoCategory(c))
	}
	return &productv1.ListCategoriesResponse{Categories: out}, nil
}

// --- Products ---

func (s *Server) CreateProduct(ctx context.Context, req *productv1.CreateProductRequest) (*productv1.CreateProductResponse, error) {
	shopID, _, _, err := resolveSellerOrAdminShop(ctx, req.GetShopId())
	if err != nil {
		return nil, err
	}
	if shopID == "" {
		return nil, status.Error(codes.InvalidArgument, "shop_id is required")
	}

	categoryID := strings.TrimSpace(req.GetCategoryId())
	if categoryID == "" {
		return nil, status.Error(codes.InvalidArgument, "category_id is required")
	}
	name := strings.TrimSpace(req.GetName())
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "product name is required")
	}
	if len(req.GetSkus()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "at least one SKU is required")
	}

	cat, err := s.store.GetCategoryByID(ctx, categoryID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "category not found")
		}
		return nil, status.Errorf(codes.Internal, "lookup category: %v", err)
	}

	skuInputs, minPrice, maxPrice, err := validateSKUInputs(req.GetSkus())
	if err != nil {
		return nil, err
	}

	var (
		createdProd store.Product
		createdSKUs []store.SKU
	)

	err = s.store.WithTx(ctx, func(q store.Queries) error {
		prodID := uuid.NewString()
		p, createErr := q.CreateProduct(ctx, store.Product{
			ID:          prodID,
			ShopID:      shopID,
			CategoryID:  categoryID,
			Name:        name,
			Description: strings.TrimSpace(req.GetDescription()),
			Images:      req.GetImages(),
			Status:      store.ProductStatusPending,
			MinPriceVND: minPrice,
			MaxPriceVND: maxPrice,
		})
		if createErr != nil {
			return createErr
		}
		createdProd = p

		createdSKUs = make([]store.SKU, 0, len(skuInputs))
		for _, in := range skuInputs {
			skuStatus := store.SKUStatusActive
			if in.Stock == 0 {
				skuStatus = store.SKUStatusOutOfStock
			}
			sk, skuErr := q.CreateSKU(ctx, store.SKU{
				ID:                uuid.NewString(),
				ProductID:         prodID,
				ShopID:            shopID,
				ProductName:       p.Name,
				SKUCode:           in.SKUCode,
				Name:              in.Name,
				Attributes:        in.Attributes,
				PriceVND:          in.PriceVND,
				Stock:             in.Stock,
				WeightGrams:       in.WeightGrams,
				ImageURL:          in.ImageURL,
				CategoryID:        categoryID,
				CommissionRateBps: cat.CommissionRateBps,
				Status:            skuStatus,
				ProductStatus:     p.Status,
			})
			if skuErr != nil {
				return skuErr
			}
			createdSKUs = append(createdSKUs, sk)
		}
		if evErr := s.enqueueProductEvent(ctx, q, events.SubjectProductCreated, createdProd.ID, createdProd.ShopID); evErr != nil {
			return evErr
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, store.ErrAlreadyExists) {
			return nil, status.Error(codes.AlreadyExists, "duplicate sku_code within product")
		}
		return nil, status.Errorf(codes.Internal, "create product: %v", err)
	}
	s.notifyOutbox()

	return &productv1.CreateProductResponse{
		Product: toProtoProduct(createdProd, createdSKUs),
	}, nil
}

func (s *Server) UpdateProduct(ctx context.Context, req *productv1.UpdateProductRequest) (*productv1.UpdateProductResponse, error) {
	shopID, _, isAdmin, err := resolveSellerOrAdminShop(ctx, req.GetShopId())
	if err != nil {
		return nil, err
	}

	id := strings.TrimSpace(req.GetId())
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "product id is required")
	}

	existing, err := s.store.GetProductByID(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "product not found")
		}
		return nil, status.Errorf(codes.Internal, "get product: %v", err)
	}
	if !isAdmin && existing.ShopID != shopID {
		return nil, status.Error(codes.PermissionDenied, "cannot update product belonging to another shop")
	}

	categoryID := strings.TrimSpace(req.GetCategoryId())
	if categoryID == "" {
		categoryID = existing.CategoryID
	}
	cat, err := s.store.GetCategoryByID(ctx, categoryID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "category not found")
		}
		return nil, status.Errorf(codes.Internal, "lookup category: %v", err)
	}

	name := strings.TrimSpace(req.GetName())
	if name == "" {
		name = existing.Name
	}
	desc := existing.Description
	if req.GetDescription() != "" {
		desc = strings.TrimSpace(req.GetDescription())
	}
	images := existing.Images
	if len(req.GetImages()) > 0 {
		images = req.GetImages()
	}

	nextStatus, err := resolveUpdatedProductStatus(existing.Status, req.GetStatus(), isAdmin)
	if err != nil {
		return nil, err
	}

	var skuInputs []normalizedSKUInput
	minPrice := existing.MinPriceVND
	maxPrice := existing.MaxPriceVND
	if len(req.GetSkus()) > 0 {
		skuInputs, minPrice, maxPrice, err = validateSKUInputs(req.GetSkus())
		if err != nil {
			return nil, err
		}
	}

	var (
		updatedProd store.Product
		resultSKUs  []store.SKU
	)

	err = s.store.WithTx(ctx, func(q store.Queries) error {
		if len(skuInputs) > 0 {
			existingSKUs, listErr := q.ListSKUsByProductIDs(ctx, []string{id})
			if listErr != nil {
				return listErr
			}
			byID := make(map[string]store.SKU, len(existingSKUs))
			for _, sk := range existingSKUs {
				byID[sk.ID] = sk
			}

			keptIDs := make(map[string]bool, len(skuInputs))
			for _, in := range skuInputs {
				skuStatus := store.SKUStatusActive
				if in.Stock == 0 {
					skuStatus = store.SKUStatusOutOfStock
				}
				if in.ID != "" {
					cur, ok := byID[in.ID]
					if !ok {
						return status.Errorf(codes.NotFound, "sku %s does not belong to product %s", in.ID, id)
					}
					if in.Stock < cur.TotalReservedStock() {
						return status.Errorf(codes.FailedPrecondition, "sku %s stock (%d) cannot be less than reserved stock (%d)", cur.ID, in.Stock, cur.TotalReservedStock())
					}
					keptIDs[cur.ID] = true
					_, updErr := q.UpdateSKUDetails(ctx, store.SKU{
						ID:          cur.ID,
						ProductID:   id,
						SKUCode:     in.SKUCode,
						Name:        in.Name,
						Attributes:  in.Attributes,
						PriceVND:    in.PriceVND,
						Stock:       in.Stock,
						WeightGrams: in.WeightGrams,
						ImageURL:    in.ImageURL,
						Status:      skuStatus,
					})
					if updErr != nil {
						return updErr
					}
				} else {
					newID := uuid.NewString()
					keptIDs[newID] = true
					_, createErr := q.CreateSKU(ctx, store.SKU{
						ID:                newID,
						ProductID:         id,
						ShopID:            existing.ShopID,
						ProductName:       name,
						SKUCode:           in.SKUCode,
						Name:              in.Name,
						Attributes:        in.Attributes,
						PriceVND:          in.PriceVND,
						Stock:             in.Stock,
						WeightGrams:       in.WeightGrams,
						ImageURL:          in.ImageURL,
						CategoryID:        categoryID,
						CommissionRateBps: cat.CommissionRateBps,
						Status:            skuStatus,
						ProductStatus:     nextStatus,
					})
					if createErr != nil {
						return createErr
					}
				}
			}

			for oldID := range byID {
				if !keptIDs[oldID] {
					if deactErr := q.DeactivateSKU(ctx, oldID); deactErr != nil {
						return deactErr
					}
				}
			}
		}

		rejectionReason := existing.RejectionReason
		if nextStatus == store.ProductStatusPending || nextStatus == store.ProductStatusApproved {
			rejectionReason = ""
		}

		p, updErr := q.UpdateProduct(ctx, store.Product{
			ID:              id,
			ShopID:          existing.ShopID,
			CategoryID:      categoryID,
			Name:            name,
			Description:     desc,
			Images:          images,
			Status:          nextStatus,
			RejectionReason: rejectionReason,
			MinPriceVND:     minPrice,
			MaxPriceVND:     maxPrice,
		})
		if updErr != nil {
			return updErr
		}
		updatedProd = p

		skus, listErr := q.ListSKUsByProductIDs(ctx, []string{id})
		if listErr != nil {
			return listErr
		}
		resultSKUs = skus
		if evErr := s.enqueueProductEvent(ctx, q, events.SubjectProductUpdated, updatedProd.ID, updatedProd.ShopID); evErr != nil {
			return evErr
		}
		return nil
	})
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		if errors.Is(err, store.ErrAlreadyExists) {
			return nil, status.Error(codes.AlreadyExists, "duplicate sku_code within product")
		}
		return nil, status.Errorf(codes.Internal, "update product: %v", err)
	}
	s.notifyOutbox()

	return &productv1.UpdateProductResponse{
		Product: toProtoProduct(updatedProd, resultSKUs),
	}, nil
}

func (s *Server) DeleteProduct(ctx context.Context, req *productv1.DeleteProductRequest) (*productv1.DeleteProductResponse, error) {
	shopID, _, isAdmin, err := resolveSellerOrAdminShop(ctx, req.GetShopId())
	if err != nil {
		return nil, err
	}
	id := strings.TrimSpace(req.GetId())
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "product id is required")
	}

	existing, err := s.store.GetProductByID(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "product not found")
		}
		return nil, status.Errorf(codes.Internal, "get product: %v", err)
	}
	if !isAdmin && existing.ShopID != shopID {
		return nil, status.Error(codes.PermissionDenied, "cannot delete product belonging to another shop")
	}

	err = s.store.WithTx(ctx, func(q store.Queries) error {
		deleted, delErr := q.SoftDeleteProduct(ctx, id)
		if delErr != nil {
			return delErr
		}
		if !deleted {
			return store.ErrNotFound
		}
		return q.DeactivateSKUsByProduct(ctx, id)
	})
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "product not found")
		}
		return nil, status.Errorf(codes.Internal, "delete product: %v", err)
	}
	return &productv1.DeleteProductResponse{Ok: true}, nil
}

func (s *Server) AdminReviewProduct(ctx context.Context, req *productv1.AdminReviewProductRequest) (*productv1.AdminReviewProductResponse, error) {
	if _, err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	productID := strings.TrimSpace(req.GetProductId())
	if productID == "" {
		return nil, status.Error(codes.InvalidArgument, "product_id is required")
	}

	var targetStatus, reason string
	switch req.GetDecision() {
	case productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE:
		targetStatus = store.ProductStatusApproved
		reason = ""
	case productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_REJECT:
		targetStatus = store.ProductStatusRejected
		reason = strings.TrimSpace(req.GetReason())
	default:
		return nil, status.Error(codes.InvalidArgument, "review decision must be APPROVE or REJECT")
	}

	updated, err := s.store.UpdateProductReview(ctx, productID, targetStatus, reason)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "product not found")
		}
		return nil, status.Errorf(codes.Internal, "review product: %v", err)
	}

	skus, err := s.store.ListSKUsByProductIDs(ctx, []string{productID})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "load product skus: %v", err)
	}

	return &productv1.AdminReviewProductResponse{
		Product: toProtoProduct(updated, skus),
	}, nil
}

func (s *Server) GetProduct(ctx context.Context, req *productv1.GetProductRequest) (*productv1.GetProductResponse, error) {
	id := strings.TrimSpace(req.GetId())
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "product id is required")
	}
	prod, err := s.store.GetProductByID(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "product not found")
		}
		return nil, status.Errorf(codes.Internal, "get product: %v", err)
	}
	if !canViewProduct(ctx, prod) {
		return nil, status.Error(codes.NotFound, "product not found")
	}
	skus, err := s.store.ListSKUsByProductIDs(ctx, []string{id})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list product skus: %v", err)
	}
	return &productv1.GetProductResponse{
		Product: toProtoProduct(prod, skus),
	}, nil
}

func (s *Server) ListProducts(ctx context.Context, req *productv1.ListProductsRequest) (*productv1.ListProductsResponse, error) {
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

	statusFilter := fromProtoProductStatus(req.GetStatus())
	shopID := strings.TrimSpace(req.GetShopId())

	publicOnly := true
	if claims, ok := auth.ClaimsFromContext(ctx); ok {
		if auth.HasRole(claims, "admin", "super_admin") {
			publicOnly = false
		} else if auth.HasRole(claims, "seller") && shopID != "" && claims.ShopID == shopID {
			publicOnly = false
		}
	}

	filter := store.ListProductsFilter{
		CategoryID:  strings.TrimSpace(req.GetCategoryId()),
		Keyword:     strings.TrimSpace(req.GetKeyword()),
		ShopID:      shopID,
		Status:      statusFilter,
		PublicOnly:  publicOnly,
		MinPriceVND: req.GetMinPriceVnd(),
		MaxPriceVND: req.GetMaxPriceVnd(),
		SortBy:      normalizeSortBy(req.GetSortBy()),
		Limit:       pageSize,
		Offset:      (page - 1) * pageSize,
	}

	prods, total, err := s.store.ListProducts(ctx, filter)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list products: %v", err)
	}
	if len(prods) == 0 {
		return &productv1.ListProductsResponse{
			Products: []*productv1.Product{},
			Total:    total,
		}, nil
	}

	prodIDs := make([]string, len(prods))
	for i, p := range prods {
		prodIDs[i] = p.ID
	}
	allSKUs, err := s.store.ListSKUsByProductIDs(ctx, prodIDs)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "load skus for products: %v", err)
	}
	skusByProd := make(map[string][]store.SKU, len(prods))
	for _, sk := range allSKUs {
		skusByProd[sk.ProductID] = append(skusByProd[sk.ProductID], sk)
	}

	out := make([]*productv1.Product, 0, len(prods))
	for _, p := range prods {
		out = append(out, toProtoProduct(p, skusByProd[p.ID]))
	}
	return &productv1.ListProductsResponse{
		Products: out,
		Total:    total,
	}, nil
}

func (s *Server) GetSKUsBatch(ctx context.Context, req *productv1.GetSKUsBatchRequest) (*productv1.GetSKUsBatchResponse, error) {
	rawIDs := req.GetSkuIds()
	if len(rawIDs) == 0 {
		return &productv1.GetSKUsBatchResponse{Skus: []*productv1.SKU{}}, nil
	}

	seen := make(map[string]struct{}, len(rawIDs))
	uniqueIDs := make([]string, 0, len(rawIDs))
	for _, id := range rawIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; !exists {
			seen[id] = struct{}{}
			uniqueIDs = append(uniqueIDs, id)
		}
	}
	if len(uniqueIDs) == 0 {
		return &productv1.GetSKUsBatchResponse{Skus: []*productv1.SKU{}}, nil
	}

	skus, err := s.store.GetSKUsBatch(ctx, uniqueIDs)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get skus batch: %v", err)
	}

	out := make([]*productv1.SKU, 0, len(skus))
	for _, sk := range skus {
		out = append(out, toProtoSKU(sk))
	}
	return &productv1.GetSKUsBatchResponse{Skus: out}, nil
}

// --- Inventory & Optimistic Locking ---

func (s *Server) UpdateStock(ctx context.Context, req *productv1.UpdateStockRequest) (*productv1.UpdateStockResponse, error) {
	shopID, claims, isAdmin, err := resolveSellerOrAdminShop(ctx, req.GetShopId())
	if err != nil {
		return nil, err
	}

	skuID := strings.TrimSpace(req.GetSkuId())
	if skuID == "" {
		return nil, status.Error(codes.InvalidArgument, "sku_id is required")
	}
	if req.GetNewStock() < 0 {
		return nil, status.Error(codes.InvalidArgument, "new_stock must be >= 0")
	}
	if req.GetExpectedVersion() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "expected_version must be > 0")
	}

	var updatedSKU store.SKU
	err = s.store.WithTx(ctx, func(q store.Queries) error {
		cur, getErr := q.GetSKUWithContext(ctx, skuID)
		if getErr != nil {
			return getErr
		}
		if cur.ProductDeleted {
			return store.ErrNotFound
		}
		if !isAdmin && cur.ShopID != shopID {
			return status.Error(codes.PermissionDenied, "cannot update stock of another shop's SKU")
		}
		if req.GetNewStock() < cur.TotalReservedStock() {
			return store.ErrInvalidStock
		}

		upd, optErr := q.UpdateSKUStockOptimistic(ctx, skuID, req.GetNewStock(), req.GetExpectedVersion())
		if optErr != nil {
			return optErr
		}

		if histErr := q.InsertStockHistory(ctx, store.StockHistoryEntry{
			ID:             uuid.NewString(),
			SKUID:          skuID,
			ChangeType:     "MANUAL_UPDATE",
			Delta:          req.GetNewStock() - cur.Stock,
			StockBefore:    cur.Stock,
			StockAfter:     upd.Stock,
			ReservedBefore: cur.ReservedStock,
			ReservedAfter:  upd.ReservedStock,
			ActorID:        claims.UserID(),
			Reason:         "seller manual stock update",
		}); histErr != nil {
			return histErr
		}

		if syncErr := syncProductStockStatus(ctx, q, cur.ProductID, cur.ProductStatus, upd.Stock); syncErr != nil {
			return syncErr
		}

		refreshed, refErr := q.GetSKUWithContext(ctx, skuID)
		if refErr != nil {
			updatedSKU = upd
		} else {
			updatedSKU = refreshed
		}
		return nil
	})
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		switch {
		case errors.Is(err, store.ErrNotFound):
			return nil, status.Error(codes.NotFound, "sku not found")
		case errors.Is(err, store.ErrVersionConflict):
			return nil, status.Error(codes.Aborted, "STOCK_VERSION_CONFLICT: sku was concurrently modified; please reload and retry")
		case errors.Is(err, store.ErrInvalidStock):
			return nil, status.Error(codes.FailedPrecondition, "new_stock cannot be less than reserved_stock")
		default:
			return nil, status.Errorf(codes.Internal, "update stock: %v", err)
		}
	}

	return &productv1.UpdateStockResponse{Sku: toProtoSKU(updatedSKU)}, nil
}

func (s *Server) ReserveStock(ctx context.Context, req *productv1.ReserveStockRequest) (*productv1.ReserveStockResponse, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok || strings.TrimSpace(claims.UserID()) == "" {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}
	orderID := strings.TrimSpace(req.GetOrderId())
	if orderID == "" {
		return nil, status.Error(codes.InvalidArgument, "order_id is required")
	}
	items, err := normalizeAndSortReservationItems(req.GetItems())
	if err != nil {
		return nil, err
	}

	for attempt := 0; attempt < maxOptimisticRetries; attempt++ {
		if attempt > 0 {
			if err := sleepWithContext(ctx, retryBaseDelay*time.Duration(attempt)); err != nil {
				return nil, status.Error(codes.Canceled, "context canceled while retrying stock reservation")
			}
		}

		var resp *productv1.ReserveStockResponse
		txErr := s.store.WithTx(ctx, func(q store.Queries) error {
			existing, listErr := q.ListReservationsByOrderID(ctx, orderID, false)
			if listErr != nil {
				return listErr
			}
			if len(existing) > 0 {
				allReservedOrConfirmed := true
				for _, r := range existing {
					if r.Status == store.ReservationStatusReleased {
						return status.Error(codes.FailedPrecondition, "stock reservation for this order was already released")
					}
					if r.Status != store.ReservationStatusReserved && r.Status != store.ReservationStatusConfirmed {
						allReservedOrConfirmed = false
					}
				}
				if allReservedOrConfirmed {
					resp = &productv1.ReserveStockResponse{
						Reserved:        true,
						ReservationId:   existing[0].ReservationGroupID,
						AlreadyReserved: true,
					}
					return nil
				}
			}

			groupID := uuid.NewString()
			now := s.nowFn()
			for _, item := range items {
				if item.flashSaleItemID != "" {
					sku, getErr := q.GetSKUWithContextForUpdate(ctx, item.skuID)
					if getErr != nil {
						if errors.Is(getErr, store.ErrNotFound) {
							return status.Errorf(codes.NotFound, "sku %s not found", item.skuID)
						}
						return getErr
					}
					if !sku.IsActive() {
						return status.Errorf(codes.FailedPrecondition, "SKU_NOT_ACTIVE: sku %s is not active", item.skuID)
					}

					fsRes, fsErr := q.GetFlashSaleReservation(ctx, item.flashSaleItemID, true)
					if fsErr != nil {
						if errors.Is(fsErr, store.ErrNotFound) {
							return status.Errorf(codes.FailedPrecondition, "FLASH_SALE_RESERVATION_NOT_FOUND: flash sale item %s has no active stock reservation", item.flashSaleItemID)
						}
						return fsErr
					}
					if fsRes.SKUID != item.skuID {
						return status.Errorf(codes.InvalidArgument, "FLASH_SALE_SKU_MISMATCH: flash sale item %s belongs to sku %s, not %s", item.flashSaleItemID, fsRes.SKUID, item.skuID)
					}
					if fsRes.Status != store.FlashReservationStatusActive || !fsRes.EndsAt.After(now) {
						return status.Errorf(codes.FailedPrecondition, "FLASH_SALE_RESERVATION_INACTIVE: flash sale item %s is no longer active", item.flashSaleItemID)
					}
					if fsRes.ReservedQty-fsRes.ConsumedQty < item.quantity {
						return status.Errorf(codes.FailedPrecondition, "INSUFFICIENT_FLASH_STOCK: flash sale item %s has %d remaining reserved units, requested %d", item.flashSaleItemID, fsRes.ReservedQty-fsRes.ConsumedQty, item.quantity)
					}
					if sku.FlashSaleReservedStock < item.quantity {
						return status.Errorf(codes.FailedPrecondition, "INSUFFICIENT_FLASH_STOCK: sku %s has %d flash reserved units, requested %d", item.skuID, sku.FlashSaleReservedStock, item.quantity)
					}

					if _, cErr := q.ConsumeFlashSaleReservation(ctx, item.flashSaleItemID, item.quantity, now); cErr != nil {
						return cErr
					}
					updated, optErr := q.ConsumeFlashSaleStockToOrder(ctx, item.skuID, item.quantity)
					if optErr != nil {
						return optErr
					}

					if _, resErr := q.CreateStockReservation(ctx, store.StockReservation{
						ID:                 uuid.NewString(),
						ReservationGroupID: groupID,
						OrderID:            orderID,
						SKUID:              item.skuID,
						Quantity:           item.quantity,
						Status:             store.ReservationStatusReserved,
						FlashSaleItemID:    item.flashSaleItemID,
					}); resErr != nil {
						return resErr
					}

					if histErr := q.InsertStockHistory(ctx, store.StockHistoryEntry{
						ID:             uuid.NewString(),
						SKUID:          item.skuID,
						OrderID:        orderID,
						ChangeType:     "FLASH_ORDER_RESERVE",
						Delta:          0,
						StockBefore:    sku.Stock,
						StockAfter:     updated.Stock,
						ReservedBefore: sku.ReservedStock,
						ReservedAfter:  updated.ReservedStock,
						Reason:         "flash sale order stock reservation (" + item.flashSaleItemID + ")",
					}); histErr != nil {
						return histErr
					}
					continue
				}

				sku, getErr := q.GetSKUWithContext(ctx, item.skuID)
				if getErr != nil {
					if errors.Is(getErr, store.ErrNotFound) {
						return status.Errorf(codes.NotFound, "sku %s not found", item.skuID)
					}
					return getErr
				}
				if !sku.IsActive() {
					return status.Errorf(codes.FailedPrecondition, "SKU_NOT_ACTIVE: sku %s is not active", item.skuID)
				}
				if sku.AvailableStock() < item.quantity {
					return status.Errorf(codes.FailedPrecondition, "INSUFFICIENT_STOCK: sku %s has %d available, requested %d", item.skuID, sku.AvailableStock(), item.quantity)
				}

				updated, optErr := q.ReserveSKUOptimistic(ctx, item.skuID, item.quantity, sku.Version)
				if optErr != nil {
					return optErr
				}

				if _, resErr := q.CreateStockReservation(ctx, store.StockReservation{
					ID:                 uuid.NewString(),
					ReservationGroupID: groupID,
					OrderID:            orderID,
					SKUID:              item.skuID,
					Quantity:           item.quantity,
					Status:             store.ReservationStatusReserved,
				}); resErr != nil {
					return resErr
				}

				if histErr := q.InsertStockHistory(ctx, store.StockHistoryEntry{
					ID:             uuid.NewString(),
					SKUID:          item.skuID,
					OrderID:        orderID,
					ChangeType:     "RESERVE",
					Delta:          0,
					StockBefore:    sku.Stock,
					StockAfter:     updated.Stock,
					ReservedBefore: sku.ReservedStock,
					ReservedAfter:  updated.ReservedStock,
					Reason:         "order stock reservation",
				}); histErr != nil {
					return histErr
				}
			}

			resp = &productv1.ReserveStockResponse{
				Reserved:        true,
				ReservationId:   groupID,
				AlreadyReserved: false,
			}
			return nil
		})

		if txErr == nil {
			return resp, nil
		}
		if _, ok := status.FromError(txErr); ok {
			return nil, txErr
		}
		if errors.Is(txErr, store.ErrInsufficientStock) {
			return nil, status.Error(codes.FailedPrecondition, "INSUFFICIENT_STOCK: not enough available stock")
		}
		if errors.Is(txErr, store.ErrFlashSaleReservationInactive) {
			return nil, status.Error(codes.FailedPrecondition, "INSUFFICIENT_FLASH_STOCK: flash sale stock reservation is exhausted or inactive")
		}
		if errors.Is(txErr, store.ErrVersionConflict) || errors.Is(txErr, store.ErrDuplicateReservation) {
			s.logger.DebugContext(ctx, "retrying stock reservation after concurrent conflict",
				slog.String("order_id", orderID),
				slog.Int("attempt", attempt+1),
				slog.String("error", txErr.Error()),
			)
			continue
		}
		return nil, status.Errorf(codes.Internal, "reserve stock: %v", txErr)
	}

	return nil, status.Error(codes.Aborted, "STOCK_VERSION_CONFLICT: concurrent stock contention exceeded retries")
}

func (s *Server) ReleaseStock(ctx context.Context, req *productv1.ReleaseStockRequest) (*productv1.ReleaseStockResponse, error) {
	if _, err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	orderID := strings.TrimSpace(req.GetOrderId())
	if orderID == "" {
		return nil, status.Error(codes.InvalidArgument, "order_id is required")
	}

	for attempt := 0; attempt < maxOptimisticRetries; attempt++ {
		if attempt > 0 {
			if err := sleepWithContext(ctx, retryBaseDelay*time.Duration(attempt)); err != nil {
				return nil, status.Error(codes.Canceled, "context canceled while retrying stock release")
			}
		}

		var resp *productv1.ReleaseStockResponse
		txErr := s.store.WithTx(ctx, func(q store.Queries) error {
			rows, listErr := q.ListReservationsByOrderID(ctx, orderID, true)
			if listErr != nil {
				return listErr
			}
			if len(rows) == 0 {
				resp = &productv1.ReleaseStockResponse{
					Released:        false,
					AlreadyReleased: false,
				}
				return nil
			}

			var reservedRows []store.StockReservation
			allReleased := true
			for _, r := range rows {
				if r.Status == store.ReservationStatusReserved {
					reservedRows = append(reservedRows, r)
					allReleased = false
				} else if r.Status != store.ReservationStatusReleased {
					allReleased = false
				}
			}

			if len(reservedRows) == 0 {
				resp = &productv1.ReleaseStockResponse{
					Released:        allReleased,
					AlreadyReleased: allReleased,
				}
				return nil
			}

			now := s.nowFn()
			for _, r := range reservedRows {
				changed, trErr := q.TransitionStockReservation(ctx, r.ID, store.ReservationStatusReleased, store.ReservationStatusReserved)
				if trErr != nil {
					return trErr
				}
				if !changed {
					return store.ErrVersionConflict
				}

				if r.FlashSaleItemID != "" {
					sku, getErr := q.GetSKUWithContextForUpdate(ctx, r.SKUID)
					if getErr != nil {
						return getErr
					}
					var returnToFlash int32
					fsRes, fsErr := q.GetFlashSaleReservation(ctx, r.FlashSaleItemID, true)
					if fsErr == nil && fsRes.Status == store.FlashReservationStatusActive && fsRes.EndsAt.After(now) {
						returnToFlash = r.Quantity
						if returnToFlash > fsRes.ConsumedQty {
							returnToFlash = fsRes.ConsumedQty
						}
						if returnToFlash > 0 {
							if _, rErr := q.RestoreFlashSaleReservationConsumed(ctx, r.FlashSaleItemID, returnToFlash); rErr != nil {
								return rErr
							}
						}
					} else if fsErr != nil && !errors.Is(fsErr, store.ErrNotFound) {
						return fsErr
					}

					updated, optErr := q.RestoreOrderStockToFlashSale(ctx, r.SKUID, r.Quantity, returnToFlash)
					if optErr != nil {
						return optErr
					}

					if histErr := q.InsertStockHistory(ctx, store.StockHistoryEntry{
						ID:             uuid.NewString(),
						SKUID:          r.SKUID,
						OrderID:        orderID,
						ChangeType:     "RELEASE",
						Delta:          0,
						StockBefore:    sku.Stock,
						StockAfter:     updated.Stock,
						ReservedBefore: sku.ReservedStock,
						ReservedAfter:  updated.ReservedStock,
						Reason:         "flash sale order stock release (" + r.FlashSaleItemID + ")",
					}); histErr != nil {
						return histErr
					}
					continue
				}

				sku, getErr := q.GetSKUWithContext(ctx, r.SKUID)
				if getErr != nil {
					return getErr
				}

				updated, optErr := q.ReleaseSKUOptimistic(ctx, r.SKUID, r.Quantity, sku.Version)
				if optErr != nil {
					return optErr
				}

				if histErr := q.InsertStockHistory(ctx, store.StockHistoryEntry{
					ID:             uuid.NewString(),
					SKUID:          r.SKUID,
					OrderID:        orderID,
					ChangeType:     "RELEASE",
					Delta:          0,
					StockBefore:    sku.Stock,
					StockAfter:     updated.Stock,
					ReservedBefore: sku.ReservedStock,
					ReservedAfter:  updated.ReservedStock,
					Reason:         "order stock release",
				}); histErr != nil {
					return histErr
				}
			}

			resp = &productv1.ReleaseStockResponse{
				Released:        true,
				AlreadyReleased: false,
			}
			return nil
		})

		if txErr == nil {
			return resp, nil
		}
		if errors.Is(txErr, store.ErrVersionConflict) {
			continue
		}
		return nil, status.Errorf(codes.Internal, "release stock: %v", txErr)
	}

	return nil, status.Error(codes.Aborted, "STOCK_VERSION_CONFLICT: concurrent release contention exceeded retries")
}

func (s *Server) ConfirmStock(ctx context.Context, req *productv1.ConfirmStockRequest) (*productv1.ConfirmStockResponse, error) {
	if _, err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	orderID := strings.TrimSpace(req.GetOrderId())
	if orderID == "" {
		return nil, status.Error(codes.InvalidArgument, "order_id is required")
	}

	for attempt := 0; attempt < maxOptimisticRetries; attempt++ {
		if attempt > 0 {
			if err := sleepWithContext(ctx, retryBaseDelay*time.Duration(attempt)); err != nil {
				return nil, status.Error(codes.Canceled, "context canceled while retrying stock confirm")
			}
		}

		var resp *productv1.ConfirmStockResponse
		txErr := s.store.WithTx(ctx, func(q store.Queries) error {
			rows, listErr := q.ListReservationsByOrderID(ctx, orderID, true)
			if listErr != nil {
				return listErr
			}
			if len(rows) == 0 {
				return status.Error(codes.NotFound, "stock reservation not found for order")
			}

			var reservedRows []store.StockReservation
			allConfirmed := true
			hasReleased := false
			for _, r := range rows {
				switch r.Status {
				case store.ReservationStatusReserved:
					reservedRows = append(reservedRows, r)
					allConfirmed = false
				case store.ReservationStatusReleased:
					hasReleased = true
					allConfirmed = false
				case store.ReservationStatusConfirmed:
				default:
					allConfirmed = false
				}
			}

			if len(reservedRows) == 0 {
				if allConfirmed {
					resp = &productv1.ConfirmStockResponse{
						Confirmed:        true,
						AlreadyConfirmed: true,
					}
					return nil
				}
				if hasReleased {
					return status.Error(codes.FailedPrecondition, "cannot confirm stock reservation that was already released")
				}
				return status.Error(codes.FailedPrecondition, "stock reservation is not in RESERVED state")
			}

			touchedProducts := make(map[string]string)
			for _, r := range reservedRows {
				changed, trErr := q.TransitionStockReservation(ctx, r.ID, store.ReservationStatusConfirmed, store.ReservationStatusReserved)
				if trErr != nil {
					return trErr
				}
				if !changed {
					return store.ErrVersionConflict
				}

				sku, getErr := q.GetSKUWithContext(ctx, r.SKUID)
				if getErr != nil {
					return getErr
				}

				updated, optErr := q.ConfirmSKUOptimistic(ctx, r.SKUID, r.Quantity, sku.Version)
				if optErr != nil {
					return optErr
				}
				touchedProducts[sku.ProductID] = sku.ProductStatus

				if histErr := q.InsertStockHistory(ctx, store.StockHistoryEntry{
					ID:             uuid.NewString(),
					SKUID:          r.SKUID,
					OrderID:        orderID,
					ChangeType:     "CONFIRM",
					Delta:          -r.Quantity,
					StockBefore:    sku.Stock,
					StockAfter:     updated.Stock,
					ReservedBefore: sku.ReservedStock,
					ReservedAfter:  updated.ReservedStock,
					Reason:         "order stock confirmation",
				}); histErr != nil {
					return histErr
				}
			}

			for prodID, prodStatus := range touchedProducts {
				if syncErr := syncProductStockStatus(ctx, q, prodID, prodStatus, 0); syncErr != nil {
					return syncErr
				}
			}

			resp = &productv1.ConfirmStockResponse{
				Confirmed:        true,
				AlreadyConfirmed: false,
			}
			return nil
		})

		if txErr == nil {
			return resp, nil
		}
		if _, ok := status.FromError(txErr); ok {
			return nil, txErr
		}
		if errors.Is(txErr, store.ErrVersionConflict) {
			continue
		}
		return nil, status.Errorf(codes.Internal, "confirm stock: %v", txErr)
	}

	return nil, status.Error(codes.Aborted, "STOCK_VERSION_CONFLICT: concurrent confirm contention exceeded retries")
}

// --- Flash Sale Stock Reservations (Phase 2b) ---

func (s *Server) ReserveFlashSaleStock(ctx context.Context, req *productv1.ReserveFlashSaleStockRequest) (*productv1.ReserveFlashSaleStockResponse, error) {
	claims, err := requireAdminOrPromotionService(ctx)
	if err != nil {
		return nil, err
	}
	flashSaleItemID := strings.TrimSpace(req.GetFlashSaleItemId())
	if flashSaleItemID == "" {
		return nil, status.Error(codes.InvalidArgument, "flash_sale_item_id is required")
	}
	skuID := strings.TrimSpace(req.GetSkuId())
	if skuID == "" {
		return nil, status.Error(codes.InvalidArgument, "sku_id is required")
	}
	qty := req.GetQuantity()
	if qty <= 0 {
		return nil, status.Error(codes.InvalidArgument, "quantity must be > 0")
	}
	if req.GetEndsAt() == nil || !req.GetEndsAt().IsValid() {
		return nil, status.Error(codes.InvalidArgument, "ends_at is required")
	}
	endsAt := req.GetEndsAt().AsTime().UTC()
	now := s.nowFn()
	if !endsAt.After(now) {
		return nil, status.Error(codes.FailedPrecondition, "FLASH_SALE_ENDED: ends_at must be in the future")
	}

	for attempt := 0; attempt < maxOptimisticRetries; attempt++ {
		if attempt > 0 {
			if err := sleepWithContext(ctx, retryBaseDelay*time.Duration(attempt)); err != nil {
				return nil, status.Error(codes.Canceled, "context canceled while retrying flash sale reservation")
			}
		}

		var resp *productv1.ReserveFlashSaleStockResponse
		txErr := s.store.WithTx(ctx, func(q store.Queries) error {
			// Always lock SKU row first to prevent deadlocks across concurrent operations.
			sku, getErr := q.GetSKUWithContextForUpdate(ctx, skuID)
			if getErr != nil {
				if errors.Is(getErr, store.ErrNotFound) {
					return status.Errorf(codes.NotFound, "sku %s not found", skuID)
				}
				return getErr
			}

			existing, fsErr := q.GetFlashSaleReservation(ctx, flashSaleItemID, true)
			if fsErr == nil {
				if existing.Status == store.FlashReservationStatusActive &&
					existing.SKUID == skuID &&
					existing.ReservedQty == qty {
					resp = &productv1.ReserveFlashSaleStockResponse{
						ReservedQty:     existing.ReservedQty,
						AvailableStock:  sku.AvailableStock(),
						AlreadyReserved: true,
					}
					return nil
				}
				if existing.Status == store.FlashReservationStatusReleased {
					return status.Errorf(codes.FailedPrecondition, "FLASH_SALE_ALREADY_RELEASED: flash sale item %s reservation was already released", flashSaleItemID)
				}
				return status.Errorf(codes.FailedPrecondition, "FLASH_SALE_RESERVATION_CONFLICT: flash sale item %s already reserved with different parameters", flashSaleItemID)
			} else if !errors.Is(fsErr, store.ErrNotFound) {
				return fsErr
			}

			if !sku.IsActive() {
				return status.Errorf(codes.FailedPrecondition, "SKU_NOT_ACTIVE: sku %s is not active", skuID)
			}
			if sku.AvailableStock() < qty {
				return status.Errorf(codes.FailedPrecondition, "INSUFFICIENT_STOCK: sku %s has %d available, requested %d for flash sale", skuID, sku.AvailableStock(), qty)
			}

			updated, addErr := q.AddFlashSaleReservedStock(ctx, skuID, qty)
			if addErr != nil {
				return addErr
			}

			if _, createErr := q.CreateFlashSaleReservation(ctx, store.FlashSaleStockReservation{
				FlashSaleItemID: flashSaleItemID,
				SKUID:           skuID,
				ReservedQty:     qty,
				EndsAt:          endsAt,
			}); createErr != nil {
				return createErr
			}

			if histErr := q.InsertStockHistory(ctx, store.StockHistoryEntry{
				ID:             uuid.NewString(),
				SKUID:          skuID,
				ChangeType:     "FLASH_RESERVE",
				Delta:          0,
				StockBefore:    sku.Stock,
				StockAfter:     updated.Stock,
				ReservedBefore: sku.TotalReservedStock(),
				ReservedAfter:  updated.TotalReservedStock(),
				ActorID:        claims.UserID(),
				Reason:         "flash sale slot stock reservation (" + flashSaleItemID + ")",
			}); histErr != nil {
				return histErr
			}

			resp = &productv1.ReserveFlashSaleStockResponse{
				ReservedQty:     qty,
				AvailableStock:  updated.AvailableStock(),
				AlreadyReserved: false,
			}
			return nil
		})

		if txErr == nil {
			return resp, nil
		}
		if _, ok := status.FromError(txErr); ok {
			return nil, txErr
		}
		if errors.Is(txErr, store.ErrInsufficientStock) {
			return nil, status.Error(codes.FailedPrecondition, "INSUFFICIENT_STOCK: not enough available stock for flash sale")
		}
		if errors.Is(txErr, store.ErrAlreadyExists) || errors.Is(txErr, store.ErrVersionConflict) {
			continue
		}
		return nil, status.Errorf(codes.Internal, "reserve flash sale stock: %v", txErr)
	}

	return nil, status.Error(codes.Aborted, "STOCK_VERSION_CONFLICT: concurrent flash sale reservation exceeded retries")
}

func (s *Server) ReleaseFlashSaleStock(ctx context.Context, req *productv1.ReleaseFlashSaleStockRequest) (*productv1.ReleaseFlashSaleStockResponse, error) {
	claims, err := requireAdminOrPromotionService(ctx)
	if err != nil {
		return nil, err
	}
	flashSaleItemID := strings.TrimSpace(req.GetFlashSaleItemId())
	if flashSaleItemID == "" {
		return nil, status.Error(codes.InvalidArgument, "flash_sale_item_id is required")
	}
	return s.releaseFlashSaleStock(ctx, flashSaleItemID, false, claims.UserID(), "flash sale stock release")
}

func (s *Server) releaseFlashSaleStock(
	ctx context.Context,
	flashSaleItemID string,
	onlyIfExpired bool,
	actorID string,
	reason string,
) (*productv1.ReleaseFlashSaleStockResponse, error) {
	for attempt := 0; attempt < maxOptimisticRetries; attempt++ {
		if attempt > 0 {
			if err := sleepWithContext(ctx, retryBaseDelay*time.Duration(attempt)); err != nil {
				return nil, status.Error(codes.Canceled, "context canceled while retrying flash sale release")
			}
		}

		// Lookup sku_id outside lock so we always lock skus row BEFORE flash_sale_stock_reservations.
		meta, metaErr := s.store.GetFlashSaleReservation(ctx, flashSaleItemID, false)
		if metaErr != nil {
			if errors.Is(metaErr, store.ErrNotFound) {
				return &productv1.ReleaseFlashSaleStockResponse{
					ReleasedQty:     0,
					AvailableStock:  0,
					AlreadyReleased: true,
				}, nil
			}
			return nil, status.Errorf(codes.Internal, "lookup flash sale reservation: %v", metaErr)
		}

		var resp *productv1.ReleaseFlashSaleStockResponse
		txErr := s.store.WithTx(ctx, func(q store.Queries) error {
			sku, getErr := q.GetSKUWithContextForUpdate(ctx, meta.SKUID)
			if getErr != nil {
				return getErr
			}

			fsRes, fsErr := q.GetFlashSaleReservation(ctx, flashSaleItemID, true)
			if fsErr != nil {
				if errors.Is(fsErr, store.ErrNotFound) {
					resp = &productv1.ReleaseFlashSaleStockResponse{
						ReleasedQty:     0,
						AvailableStock:  sku.AvailableStock(),
						AlreadyReleased: true,
					}
					return nil
				}
				return fsErr
			}
			if fsRes.Status == store.FlashReservationStatusReleased {
				resp = &productv1.ReleaseFlashSaleStockResponse{
					ReleasedQty:     0,
					AvailableStock:  sku.AvailableStock(),
					AlreadyReleased: true,
				}
				return nil
			}
			if onlyIfExpired && fsRes.EndsAt.After(s.nowFn()) {
				resp = &productv1.ReleaseFlashSaleStockResponse{
					ReleasedQty:     0,
					AvailableStock:  sku.AvailableStock(),
					AlreadyReleased: false,
				}
				return nil
			}

			if _, markErr := q.MarkFlashSaleReservationReleased(ctx, flashSaleItemID); markErr != nil {
				return markErr
			}

			remaining := fsRes.ReservedQty - fsRes.ConsumedQty
			if remaining < 0 {
				remaining = 0
			}
			updated := sku
			if remaining > 0 {
				var subErr error
				updated, subErr = q.SubFlashSaleReservedStock(ctx, sku.ID, remaining)
				if subErr != nil {
					return subErr
				}
			}

			if histErr := q.InsertStockHistory(ctx, store.StockHistoryEntry{
				ID:             uuid.NewString(),
				SKUID:          sku.ID,
				ChangeType:     "FLASH_RELEASE",
				Delta:          0,
				StockBefore:    sku.Stock,
				StockAfter:     updated.Stock,
				ReservedBefore: sku.TotalReservedStock(),
				ReservedAfter:  updated.TotalReservedStock(),
				ActorID:        actorID,
				Reason:         reason + " (" + flashSaleItemID + ")",
			}); histErr != nil {
				return histErr
			}

			resp = &productv1.ReleaseFlashSaleStockResponse{
				ReleasedQty:     remaining,
				AvailableStock:  updated.AvailableStock(),
				AlreadyReleased: false,
			}
			return nil
		})

		if txErr == nil {
			return resp, nil
		}
		if errors.Is(txErr, store.ErrVersionConflict) {
			continue
		}
		return nil, status.Errorf(codes.Internal, "release flash sale stock: %v", txErr)
	}

	return nil, status.Error(codes.Aborted, "STOCK_VERSION_CONFLICT: concurrent flash sale release exceeded retries")
}

// SweepExpiredFlashSaleReservations releases all ACTIVE flash sale stock reservations whose ends_at <= now.
func (s *Server) SweepExpiredFlashSaleReservations(ctx context.Context) (int, error) {
	expired, err := s.store.ListExpiredActiveFlashSaleReservations(ctx, s.nowFn(), 100)
	if err != nil {
		return 0, fmt.Errorf("list expired flash sale reservations: %w", err)
	}
	released := 0
	var firstErr error
	for _, r := range expired {
		resp, relErr := s.releaseFlashSaleStock(ctx, r.FlashSaleItemID, true, "product-sweeper", "flash sale expired sweep")
		if relErr != nil {
			if firstErr == nil {
				firstErr = relErr
			}
			s.logger.WarnContext(ctx, "failed to sweep expired flash sale reservation",
				slog.String("flash_sale_item_id", r.FlashSaleItemID),
				slog.String("sku_id", r.SKUID),
				slog.String("error", relErr.Error()),
			)
			continue
		}
		if !resp.GetAlreadyReleased() {
			released++
		}
	}
	return released, firstErr
}

// StartFlashSaleSweeper periodically releases expired flash sale stock reservations until ctx is canceled.
func (s *Server) StartFlashSaleSweeper(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sweepCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				n, err := s.SweepExpiredFlashSaleReservations(sweepCtx)
				cancel()
				if err != nil && ctx.Err() == nil {
					s.logger.Warn("flash sale reservation sweep error", slog.String("error", err.Error()))
				} else if n > 0 {
					s.logger.Info("swept expired flash sale stock reservations", slog.Int("released_count", n))
				}
			}
		}
	}()
}

// --- Helpers ---

func requireAdminOrPromotionService(ctx context.Context) (*auth.Claims, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}
	if claims.Service != "" {
		if claims.Service != "promotion-service" {
			return nil, status.Error(codes.PermissionDenied, "only promotion-service or admin can manage flash sale stock reservations")
		}
		return claims, nil
	}
	if !auth.HasRole(claims, "admin", "super_admin") {
		return nil, status.Error(codes.PermissionDenied, "admin role required")
	}
	return claims, nil
}

func requireAdmin(ctx context.Context) (*auth.Claims, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}
	if !auth.HasRole(claims, "admin", "super_admin") {
		return nil, status.Error(codes.PermissionDenied, "admin role required")
	}
	return claims, nil
}

func canViewProduct(ctx context.Context, prod store.Product) bool {
	if prod.Status == store.ProductStatusApproved || prod.Status == store.ProductStatusOutOfStock {
		return true
	}
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return false
	}
	if auth.HasRole(claims, "admin", "super_admin") {
		return true
	}
	if auth.HasRole(claims, "seller") && claims.ShopID != "" && claims.ShopID == prod.ShopID {
		return true
	}
	return false
}

func resolveSellerOrAdminShop(ctx context.Context, reqShopID string) (string, *auth.Claims, bool, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return "", nil, false, status.Error(codes.Unauthenticated, "authentication required")
	}
	reqShopID = strings.TrimSpace(reqShopID)
	if auth.HasRole(claims, "admin", "super_admin") {
		return reqShopID, claims, true, nil
	}
	if !auth.HasRole(claims, "seller") {
		return "", nil, false, status.Error(codes.PermissionDenied, "seller role required")
	}
	claimShopID := strings.TrimSpace(claims.ShopID)
	if claimShopID == "" {
		return "", nil, false, status.Error(codes.PermissionDenied, "seller token missing shop_id")
	}
	if reqShopID != "" && reqShopID != claimShopID {
		return "", nil, false, status.Error(codes.PermissionDenied, "cannot manage products for another shop")
	}
	return claimShopID, claims, false, nil
}

type normalizedSKUInput struct {
	ID          string
	SKUCode     string
	Name        string
	Attributes  map[string]string
	PriceVND    int64
	Stock       int32
	WeightGrams int32
	ImageURL    string
}

func validateSKUInputs(in []*productv1.SKUInput) ([]normalizedSKUInput, int64, int64, error) {
	out := make([]normalizedSKUInput, 0, len(in))
	seenCodes := make(map[string]bool, len(in))
	var minPrice, maxPrice int64

	for i, s := range in {
		if s.GetPriceVnd() <= 0 {
			return nil, 0, 0, status.Errorf(codes.InvalidArgument, "sku[%d] price_vnd must be > 0", i)
		}
		if s.GetStock() < 0 {
			return nil, 0, 0, status.Errorf(codes.InvalidArgument, "sku[%d] stock must be >= 0", i)
		}
		if s.GetWeightGrams() < 0 {
			return nil, 0, 0, status.Errorf(codes.InvalidArgument, "sku[%d] weight_grams must be >= 0", i)
		}

		code := strings.TrimSpace(s.GetSkuCode())
		if code == "" {
			code = fmt.Sprintf("SKU-%s", uuid.NewString()[:8])
		}
		if seenCodes[code] {
			return nil, 0, 0, status.Errorf(codes.InvalidArgument, "duplicate sku_code %q in request", code)
		}
		seenCodes[code] = true

		skuName := strings.TrimSpace(s.GetName())
		if skuName == "" {
			skuName = "Default"
		}

		attrs := make(map[string]string, len(s.GetAttributes()))
		for k, v := range s.GetAttributes() {
			attrs[k] = v
		}

		if i == 0 || s.GetPriceVnd() < minPrice {
			minPrice = s.GetPriceVnd()
		}
		if i == 0 || s.GetPriceVnd() > maxPrice {
			maxPrice = s.GetPriceVnd()
		}

		out = append(out, normalizedSKUInput{
			ID:          strings.TrimSpace(s.GetId()),
			SKUCode:     code,
			Name:        skuName,
			Attributes:  attrs,
			PriceVND:    s.GetPriceVnd(),
			Stock:       s.GetStock(),
			WeightGrams: s.GetWeightGrams(),
			ImageURL:    strings.TrimSpace(s.GetImageUrl()),
		})
	}
	return out, minPrice, maxPrice, nil
}

func resolveUpdatedProductStatus(current string, requested productv1.ProductStatus, isAdmin bool) (string, error) {
	if requested == productv1.ProductStatus_PRODUCT_STATUS_UNSPECIFIED {
		return current, nil
	}
	next := fromProtoProductStatus(requested)
	if next == "" {
		return "", status.Error(codes.InvalidArgument, "invalid product status")
	}
	if isAdmin || next == current {
		return next, nil
	}
	switch next {
	case store.ProductStatusHidden:
		return next, nil
	case store.ProductStatusApproved:
		if current == store.ProductStatusHidden || current == store.ProductStatusOutOfStock {
			return next, nil
		}
		return "", status.Error(codes.PermissionDenied, "only admins can approve pending or rejected products")
	case store.ProductStatusPending:
		if current == store.ProductStatusRejected {
			return next, nil
		}
		return "", status.Error(codes.InvalidArgument, "cannot transition product to PENDING from current status")
	default:
		return "", status.Error(codes.PermissionDenied, "seller cannot set requested product status")
	}
}

type sortedReservationItem struct {
	skuID           string
	quantity        int32
	flashSaleItemID string
}

func normalizeAndSortReservationItems(raw []*productv1.StockReservationItem) ([]sortedReservationItem, error) {
	if len(raw) == 0 {
		return nil, status.Error(codes.InvalidArgument, "at least one reservation item is required")
	}
	merged := make(map[string]*sortedReservationItem, len(raw))
	for i, it := range raw {
		skuID := strings.TrimSpace(it.GetSkuId())
		if skuID == "" {
			return nil, status.Errorf(codes.InvalidArgument, "items[%d].sku_id is required", i)
		}
		if it.GetQuantity() <= 0 {
			return nil, status.Errorf(codes.InvalidArgument, "items[%d].quantity must be > 0", i)
		}
		fsID := strings.TrimSpace(it.GetFlashSaleItemId())
		if cur, ok := merged[skuID]; ok {
			if cur.flashSaleItemID != fsID {
				return nil, status.Errorf(codes.InvalidArgument, "items[%d] has conflicting flash_sale_item_id for sku %s", i, skuID)
			}
			cur.quantity += it.GetQuantity()
		} else {
			merged[skuID] = &sortedReservationItem{
				skuID:           skuID,
				quantity:        it.GetQuantity(),
				flashSaleItemID: fsID,
			}
		}
	}
	out := make([]sortedReservationItem, 0, len(merged))
	for _, it := range merged {
		out = append(out, *it)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].skuID < out[j].skuID
	})
	return out, nil
}

func syncProductStockStatus(ctx context.Context, q store.Queries, productID, currentProdStatus string, latestSKUStock int32) error {
	if currentProdStatus == store.ProductStatusApproved {
		inStockCount, err := q.CountActiveInStockSKUsByProduct(ctx, productID)
		if err != nil {
			return err
		}
		if inStockCount == 0 {
			return q.UpdateProductStatus(ctx, productID, store.ProductStatusOutOfStock)
		}
	} else if currentProdStatus == store.ProductStatusOutOfStock && latestSKUStock > 0 {
		return q.UpdateProductStatus(ctx, productID, store.ProductStatusApproved)
	}
	return nil
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func normalizeSortBy(sortBy string) string {
	switch strings.ToLower(strings.TrimSpace(sortBy)) {
	case "price_asc":
		return "price_asc"
	case "price_desc":
		return "price_desc"
	case "name_asc":
		return "name_asc"
	case "oldest", "created_at_asc":
		return "oldest"
	default:
		return "newest"
	}
}

func slugify(name, fallback string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
		} else if unicode.IsSpace(r) || r == '-' || r == '_' {
			if b.Len() > 0 && !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		return "cat-" + fallback
	}
	return s
}

func toProtoCategory(c store.Category) *productv1.Category {
	return &productv1.Category{
		Id:                c.ID,
		Name:              c.Name,
		Slug:              c.Slug,
		ParentId:          c.ParentID,
		CommissionRateBps: c.CommissionRateBps,
		SortOrder:         c.SortOrder,
		CreatedAt:         timestamppb.New(c.CreatedAt),
		UpdatedAt:         timestamppb.New(c.UpdatedAt),
	}
}

func toProtoSKU(s store.SKU) *productv1.SKU {
	attrs := make(map[string]string, len(s.Attributes))
	for k, v := range s.Attributes {
		attrs[k] = v
	}
	return &productv1.SKU{
		Id:                     s.ID,
		ProductId:              s.ProductID,
		ShopId:                 s.ShopID,
		ProductName:            s.ProductName,
		SkuCode:                s.SKUCode,
		Name:                   s.Name,
		Attributes:             attrs,
		PriceVnd:               s.PriceVND,
		Stock:                  s.Stock,
		ReservedStock:          s.ReservedStock,
		AvailableStock:         s.AvailableStock(),
		Version:                s.Version,
		WeightGrams:            s.WeightGrams,
		ImageUrl:               s.ImageURL,
		CategoryId:             s.CategoryID,
		CommissionRateBps:      s.CommissionRateBps,
		IsActive:               s.IsActive(),
		FlashSaleReservedStock: s.FlashSaleReservedStock,
	}
}

func toProtoProduct(p store.Product, skus []store.SKU) *productv1.Product {
	minPrice := p.MinPriceVND
	maxPrice := p.MaxPriceVND
	protoSKUs := make([]*productv1.SKU, 0, len(skus))
	for i, sk := range skus {
		protoSKUs = append(protoSKUs, toProtoSKU(sk))
		if i == 0 || sk.PriceVND < minPrice {
			minPrice = sk.PriceVND
		}
		if i == 0 || sk.PriceVND > maxPrice {
			maxPrice = sk.PriceVND
		}
	}
	imgs := make([]string, len(p.Images))
	copy(imgs, p.Images)

	return &productv1.Product{
		Id:              p.ID,
		ShopId:          p.ShopID,
		CategoryId:      p.CategoryID,
		Name:            p.Name,
		Description:     p.Description,
		Images:          imgs,
		Status:          toProtoProductStatus(p.Status),
		RejectionReason: p.RejectionReason,
		MinPriceVnd:     minPrice,
		MaxPriceVnd:     maxPrice,
		Skus:            protoSKUs,
		CreatedAt:       timestamppb.New(p.CreatedAt),
		UpdatedAt:       timestamppb.New(p.UpdatedAt),
	}
}

func toProtoProductStatus(s string) productv1.ProductStatus {
	switch s {
	case store.ProductStatusPending:
		return productv1.ProductStatus_PRODUCT_STATUS_PENDING
	case store.ProductStatusApproved:
		return productv1.ProductStatus_PRODUCT_STATUS_APPROVED
	case store.ProductStatusRejected:
		return productv1.ProductStatus_PRODUCT_STATUS_REJECTED
	case store.ProductStatusHidden:
		return productv1.ProductStatus_PRODUCT_STATUS_HIDDEN
	case store.ProductStatusOutOfStock:
		return productv1.ProductStatus_PRODUCT_STATUS_OUT_OF_STOCK
	default:
		return productv1.ProductStatus_PRODUCT_STATUS_UNSPECIFIED
	}
}

func fromProtoProductStatus(s productv1.ProductStatus) string {
	switch s {
	case productv1.ProductStatus_PRODUCT_STATUS_PENDING:
		return store.ProductStatusPending
	case productv1.ProductStatus_PRODUCT_STATUS_APPROVED:
		return store.ProductStatusApproved
	case productv1.ProductStatus_PRODUCT_STATUS_REJECTED:
		return store.ProductStatusRejected
	case productv1.ProductStatus_PRODUCT_STATUS_HIDDEN:
		return store.ProductStatusHidden
	case productv1.ProductStatus_PRODUCT_STATUS_OUT_OF_STOCK:
		return store.ProductStatusOutOfStock
	default:
		return ""
	}
}
