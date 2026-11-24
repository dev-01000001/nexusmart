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
	"github.com/nexusmart/core/services/product/internal/resilience"
	"github.com/nexusmart/core/services/product/internal/store"
)

const (
	defaultPageSize       = 20
	maxPageSize           = 100
	maxListProductsSKUIDs = 200
	maxOptimisticRetries  = 8
	retryBaseDelay        = 5 * time.Millisecond
)

// Server implements productv1.ProductServiceServer.
type Server struct {
	productv1.UnimplementedProductServiceServer
	store           store.Store
	logger          *slog.Logger
	orderClient     OrderClient
	userClient      UserClient
	orderBreaker    *resilience.CircuitBreaker
	userBreaker     *resilience.CircuitBreaker
	upstreamTimeout time.Duration
	clockMu         sync.RWMutex
	nowFn           func() time.Time
	outboxTrigger   chan struct{}
}

// New constructs a ProductService gRPC server.
func New(st store.Store, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		store:           st,
		logger:          logger,
		orderBreaker:    resilience.NewCircuitBreaker(5, 15*time.Second),
		userBreaker:     resilience.NewCircuitBreaker(5, 15*time.Second),
		upstreamTimeout: defaultUpstreamTimeout,
		nowFn:           func() time.Time { return time.Now().UTC() },
		outboxTrigger:   make(chan struct{}, 1),
	}
}

// SetClock overrides the server clock (used in unit tests for deterministic time-based logic).
func (s *Server) SetClock(fn func() time.Time) {
	s.clockMu.Lock()
	defer s.clockMu.Unlock()
	if fn == nil {
		s.nowFn = func() time.Time { return time.Now().UTC() }
		return
	}
	s.nowFn = fn
}

func (s *Server) now() time.Time {
	s.clockMu.RLock()
	fn := s.nowFn
	s.clockMu.RUnlock()
	if fn == nil {
		return time.Now().UTC()
	}
	return fn().UTC()
}

// OutboxTrigger returns a signal channel notified whenever a new outbox event is committed.
func (s *Server) OutboxTrigger() <-chan struct{} {
	return s.outboxTrigger
}

func (s *Server) triggerOutbox() {
	select {
	case s.outboxTrigger <- struct{}{}:
	default:
	}
}

func traceMetaFromContext(ctx context.Context) (string, string) {
	traceID := telemetry.TraceIDFromContext(ctx)
	carrier := map[string]string{}
	telemetry.InjectTraceHeaders(ctx, carrier)
	return traceID, carrier["traceparent"]
}

// --- Categories ---

func (s *Server) CreateCategory(ctx context.Context, req *productv1.CreateCategoryRequest) (*productv1.CreateCategoryResponse, error) {
	claims, err := requireCapability(ctx, auth.CapCategoriesWrite)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.GetName())
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "category name is required")
	}
	if req.GetCommissionRateBps() < 0 || req.GetCommissionRateBps() > 10000 {
		return nil, status.Error(codes.InvalidArgument, "commission_rate_bps must be between 0 and 10000")
	}

	rawSlug := strings.TrimSpace(req.GetSlug())
	var explicitSlug string
	if rawSlug != "" {
		explicitSlug = normalizeSlug(rawSlug)
		if explicitSlug == "" {
			return nil, status.Error(codes.InvalidArgument, "slug must contain letters or digits")
		}
	}

	parentID := strings.TrimSpace(req.GetParentId())
	now := s.now()
	ctx = store.WithAsOf(ctx, now)

	var created store.Category
	err = s.store.WithTx(ctx, func(q store.Queries) error {
		if parentID != "" {
			if lockErr := q.LockCategoryTree(ctx); lockErr != nil {
				return lockErr
			}
			if _, pErr := q.GetCategoryByID(ctx, parentID); pErr != nil {
				if errors.Is(pErr, store.ErrNotFound) {
					return status.Error(codes.NotFound, "parent category not found")
				}
				return pErr
			}
		}

		id := uuid.NewString()
		slug := explicitSlug
		if slug == "" {
			base := slugify(name, id[:8])
			slug = base
			if _, lookupErr := q.GetCategoryBySlug(ctx, slug); lookupErr == nil {
				slug = base + "-" + id[:8]
			} else if !errors.Is(lookupErr, store.ErrNotFound) {
				return lookupErr
			}
		}

		cat, createErr := q.CreateCategory(ctx, store.Category{
			ID:                id,
			Name:              name,
			Slug:              slug,
			ParentID:          parentID,
			CommissionRateBps: req.GetCommissionRateBps(),
			SortOrder:         req.GetSortOrder(),
			IsProhibited:      req.GetIsProhibited(),
		})
		if createErr != nil {
			return createErr
		}

		if _, schedErr := q.InsertCategoryFeeSchedule(ctx, store.CategoryFeeSchedule{
			ID:                uuid.NewString(),
			CategoryID:        cat.ID,
			CommissionRateBps: cat.CommissionRateBps,
			EffectiveFrom:     now,
			CreatedBy:         claims.UserID(),
			Note:              "initial category commission rate",
			CreatedAt:         now,
		}); schedErr != nil {
			return schedErr
		}

		created = cat
		return nil
	})
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		switch {
		case errors.Is(err, store.ErrAlreadyExists):
			return nil, status.Error(codes.AlreadyExists, "category slug already exists")
		case errors.Is(err, store.ErrForeignKeyViolation):
			return nil, status.Error(codes.NotFound, "parent category not found")
		default:
			return nil, status.Errorf(codes.Internal, "create category: %v", err)
		}
	}
	return &productv1.CreateCategoryResponse{Category: toProtoCategory(created)}, nil
}

func (s *Server) UpdateCategory(ctx context.Context, req *productv1.UpdateCategoryRequest) (*productv1.UpdateCategoryResponse, error) {
	claims, err := requireCapability(ctx, auth.CapCategoriesWrite)
	if err != nil {
		return nil, err
	}
	id := strings.TrimSpace(req.GetId())
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "category id is required")
	}

	commissionExplicit := req.GetHasCommissionRateBps() || req.GetCommissionRateBps() != 0
	if commissionExplicit && (req.GetCommissionRateBps() < 0 || req.GetCommissionRateBps() > 10000) {
		return nil, status.Error(codes.InvalidArgument, "commission_rate_bps must be between 0 and 10000")
	}

	var explicitSlug string
	if rawSlug := strings.TrimSpace(req.GetSlug()); rawSlug != "" {
		normalized := normalizeSlug(rawSlug)
		if normalized == "" {
			return nil, status.Error(codes.InvalidArgument, "slug must contain letters or digits")
		}
		explicitSlug = normalized
	}

	now := s.now()
	ctx = store.WithAsOf(ctx, now)

	var updated store.Category
	err = s.store.WithTx(ctx, func(q store.Queries) error {
		if lockErr := q.LockCategoryTree(ctx); lockErr != nil {
			return lockErr
		}

		existing, getErr := q.GetCategoryByID(ctx, id)
		if getErr != nil {
			return getErr
		}

		name := strings.TrimSpace(req.GetName())
		if name == "" {
			name = existing.Name
		}
		slug := existing.Slug
		if explicitSlug != "" {
			slug = explicitSlug
		}

		parentID := existing.ParentID
		if req.GetHasParentId() {
			parentID = strings.TrimSpace(req.GetParentId())
		} else if rawParent := strings.TrimSpace(req.GetParentId()); rawParent != "" {
			parentID = rawParent
		}

		if parentID == id {
			return status.Error(codes.InvalidArgument, "category cannot be its own parent")
		}
		if parentID != "" {
			if _, pErr := q.GetCategoryByID(ctx, parentID); pErr != nil {
				if errors.Is(pErr, store.ErrNotFound) {
					return status.Error(codes.NotFound, "parent category not found")
				}
				return pErr
			}
			hasCycle, cycErr := q.CategoryHasAncestor(ctx, parentID, id)
			if cycErr != nil {
				return cycErr
			}
			if hasCycle {
				return status.Error(codes.InvalidArgument, "category parent_id would create a cycle")
			}
		}

		commissionRateBps := existing.CommissionRateBps
		if commissionExplicit {
			commissionRateBps = req.GetCommissionRateBps()
		}

		sortOrder := existing.SortOrder
		if req.GetHasSortOrder() || req.GetSortOrder() != 0 {
			sortOrder = req.GetSortOrder()
		}

		isProhibited := existing.IsProhibited
		if req.GetHasIsProhibited() || req.GetIsProhibited() {
			isProhibited = req.GetIsProhibited()
		}

		cat, updErr := q.UpdateCategory(ctx, store.Category{
			ID:                id,
			Name:              name,
			Slug:              slug,
			ParentID:          parentID,
			CommissionRateBps: commissionRateBps,
			SortOrder:         sortOrder,
			IsProhibited:      isProhibited,
		})
		if updErr != nil {
			return updErr
		}

		if commissionExplicit && commissionRateBps != existing.CommissionRateBps {
			if _, schedErr := q.InsertCategoryFeeSchedule(ctx, store.CategoryFeeSchedule{
				ID:                uuid.NewString(),
				CategoryID:        id,
				CommissionRateBps: commissionRateBps,
				EffectiveFrom:     now,
				CreatedBy:         claims.UserID(),
				Note:              "updated via category edit",
				CreatedAt:         now,
			}); schedErr != nil {
				return schedErr
			}
		}

		updated = cat
		return nil
	})
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		switch {
		case errors.Is(err, store.ErrCategoryCycle):
			return nil, status.Error(codes.InvalidArgument, "category parent_id would create a cycle")
		case errors.Is(err, store.ErrAlreadyExists):
			return nil, status.Error(codes.AlreadyExists, "category slug already exists")
		case errors.Is(err, store.ErrNotFound):
			return nil, status.Error(codes.NotFound, "category not found")
		case errors.Is(err, store.ErrForeignKeyViolation):
			return nil, status.Error(codes.NotFound, "parent category not found")
		default:
			return nil, status.Errorf(codes.Internal, "update category: %v", err)
		}
	}
	return &productv1.UpdateCategoryResponse{Category: toProtoCategory(updated)}, nil
}

func (s *Server) DeleteCategory(ctx context.Context, req *productv1.DeleteCategoryRequest) (*productv1.DeleteCategoryResponse, error) {
	if _, err := requireCapability(ctx, auth.CapCategoriesWrite); err != nil {
		return nil, err
	}
	id := strings.TrimSpace(req.GetId())
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "category id is required")
	}

	ctx = store.WithAsOf(ctx, s.now())
	err := s.store.WithTx(ctx, func(q store.Queries) error {
		if lockErr := q.LockCategoryTree(ctx); lockErr != nil {
			return lockErr
		}
		if _, getErr := q.GetCategoryByID(ctx, id); getErr != nil {
			return getErr
		}

		counts, cntErr := q.CountProductsInCategory(ctx, id)
		if cntErr != nil {
			return cntErr
		}
		if counts.Active > 0 {
			return status.Errorf(codes.FailedPrecondition, "category still has %d active products; reassign products before deleting", counts.Active)
		}
		if counts.Total > 0 {
			return status.Errorf(codes.FailedPrecondition, "category is still referenced by %d soft-deleted products and cannot be deleted", counts.Total)
		}

		childCount, childErr := q.CountChildCategories(ctx, id)
		if childErr != nil {
			return childErr
		}
		if childCount > 0 {
			return status.Errorf(codes.FailedPrecondition, "category still has %d child categories", childCount)
		}

		deleted, delErr := q.DeleteCategory(ctx, id)
		if delErr != nil {
			return delErr
		}
		if !deleted {
			return store.ErrNotFound
		}
		return nil
	})
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		switch {
		case errors.Is(err, store.ErrNotFound):
			return nil, status.Error(codes.NotFound, "category not found")
		case errors.Is(err, store.ErrForeignKeyViolation):
			return nil, status.Error(codes.FailedPrecondition, "category is still referenced by products or child categories and cannot be deleted")
		default:
			return nil, status.Errorf(codes.Internal, "delete category: %v", err)
		}
	}
	return &productv1.DeleteCategoryResponse{Ok: true}, nil
}

func (s *Server) ListCategories(ctx context.Context, req *productv1.ListCategoriesRequest) (*productv1.ListCategoriesResponse, error) {
	ctx = store.WithAsOf(ctx, s.now())
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

func (s *Server) GetCategoryDescendants(ctx context.Context, req *productv1.GetCategoryDescendantsRequest) (*productv1.GetCategoryDescendantsResponse, error) {
	if _, ok := auth.ClaimsFromContext(ctx); !ok {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}
	rawIDs := req.GetCategoryIds()
	if len(rawIDs) == 0 {
		return &productv1.GetCategoryDescendantsResponse{CategoryIds: []string{}}, nil
	}
	seen := make(map[string]struct{}, len(rawIDs))
	uniqueIDs := make([]string, 0, len(rawIDs))
	for _, raw := range rawIDs {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; !exists {
			seen[id] = struct{}{}
			uniqueIDs = append(uniqueIDs, id)
		}
	}
	if len(uniqueIDs) == 0 {
		return &productv1.GetCategoryDescendantsResponse{CategoryIds: []string{}}, nil
	}

	ids, err := s.store.GetCategoryDescendants(ctx, uniqueIDs)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get category descendants: %v", err)
	}
	return &productv1.GetCategoryDescendantsResponse{CategoryIds: ids}, nil
}

func (s *Server) SetCategoryFeeSchedule(ctx context.Context, req *productv1.SetCategoryFeeScheduleRequest) (*productv1.SetCategoryFeeScheduleResponse, error) {
	claims, err := requireCapability(ctx, auth.CapFeesCategoryWrite)
	if err != nil {
		return nil, err
	}
	categoryID := strings.TrimSpace(req.GetCategoryId())
	if categoryID == "" {
		return nil, status.Error(codes.InvalidArgument, "category_id is required")
	}
	bps := req.GetCommissionRateBps()
	if bps < 0 || bps > 10000 {
		return nil, status.Error(codes.InvalidArgument, "commission_rate_bps must be between 0 and 10000")
	}

	now := s.now()
	effectiveFrom := now
	if req.GetEffectiveFrom() != nil {
		if err := req.GetEffectiveFrom().CheckValid(); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid effective_from timestamp: %v", err)
		}
		effectiveFrom = req.GetEffectiveFrom().AsTime().UTC()
		if effectiveFrom.Before(now) {
			return nil, status.Error(codes.InvalidArgument, "effective_from must be >= current time; past fee schedules cannot be created or modified")
		}
	}

	ctx = store.WithAsOf(ctx, now)
	var created store.CategoryFeeSchedule
	err = s.store.WithTx(ctx, func(q store.Queries) error {
		if _, getErr := q.GetCategoryByID(ctx, categoryID); getErr != nil {
			return getErr
		}
		sched, insErr := q.InsertCategoryFeeSchedule(ctx, store.CategoryFeeSchedule{
			ID:                uuid.NewString(),
			CategoryID:        categoryID,
			CommissionRateBps: bps,
			EffectiveFrom:     effectiveFrom,
			CreatedBy:         claims.UserID(),
			Note:              strings.TrimSpace(req.GetNote()),
			CreatedAt:         now,
		})
		if insErr != nil {
			return insErr
		}
		if !effectiveFrom.After(now) {
			if updErr := q.UpdateCategoryCommissionRate(ctx, categoryID, bps); updErr != nil {
				return updErr
			}
		}
		created = sched
		return nil
	})
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrForeignKeyViolation):
			return nil, status.Error(codes.NotFound, "category not found")
		default:
			return nil, status.Errorf(codes.Internal, "set category fee schedule: %v", err)
		}
	}
	return &productv1.SetCategoryFeeScheduleResponse{Schedule: toProtoCategoryFeeSchedule(created)}, nil
}

func (s *Server) ListCategoryFeeSchedules(ctx context.Context, req *productv1.ListCategoryFeeSchedulesRequest) (*productv1.ListCategoryFeeSchedulesResponse, error) {
	if _, err := requireCapability(ctx, auth.CapFeesCategoryRead); err != nil {
		return nil, err
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

	schedules, total, err := s.store.ListCategoryFeeSchedules(ctx, strings.TrimSpace(req.GetCategoryId()), pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list category fee schedules: %v", err)
	}
	out := make([]*productv1.CategoryFeeSchedule, 0, len(schedules))
	for _, sc := range schedules {
		out = append(out, toProtoCategoryFeeSchedule(sc))
	}
	return &productv1.ListCategoryFeeSchedulesResponse{
		Schedules: out,
		Total:     total,
	}, nil
}

// --- Products ---

func (s *Server) CreateProduct(ctx context.Context, req *productv1.CreateProductRequest) (*productv1.CreateProductResponse, error) {
	shopID, claims, isAdmin, err := resolveSellerOrAdminShop(ctx, req.GetShopId())
	if err != nil {
		return nil, err
	}
	if shopID == "" {
		return nil, status.Error(codes.InvalidArgument, "shop_id is required")
	}
	if !isAdmin {
		suspended, suspErr := s.store.IsShopSuspended(ctx, shopID)
		if suspErr != nil {
			return nil, status.Errorf(codes.Internal, "check shop suspension: %v", suspErr)
		}
		if suspended {
			return nil, status.Error(codes.FailedPrecondition, "SHOP_SUSPENDED: shop is suspended and cannot create products")
		}
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

	now := s.now()
	ctx = store.WithAsOf(ctx, now)

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

	rawSlug := strings.TrimSpace(req.GetSlug())
	var explicitSlug string
	if rawSlug != "" {
		explicitSlug = normalizeSlug(rawSlug)
		if explicitSlug == "" {
			return nil, status.Error(codes.InvalidArgument, "slug must contain letters or digits")
		}
	}

	desc := strings.TrimSpace(req.GetDescription())
	modRes, err := s.checkProductModeration(ctx, s.store, categoryID, name, desc, productModerationSlug{
		Name:    name,
		Slug:    explicitSlug,
		RawSlug: rawSlug,
	}, toModerationSKUsFromInputs(skuInputs))
	if err != nil {
		return nil, err
	}

	var (
		createdProd store.Product
		createdSKUs []store.SKU
	)
	actorID := claims.UserID()

	err = s.store.WithTx(ctx, func(q store.Queries) error {
		if lockErr := q.LockShopSuspension(ctx, shopID); lockErr != nil {
			return lockErr
		}
		shopSuspended, suspErr := q.IsShopSuspended(ctx, shopID)
		if suspErr != nil {
			return suspErr
		}
		if !isAdmin && shopSuspended {
			return status.Error(codes.FailedPrecondition, "SHOP_SUSPENDED: shop is suspended and cannot create products")
		}

		prodID := uuid.NewString()
		prodSlug := explicitSlug
		if prodSlug != "" {
			if _, lookupErr := q.GetProductBySlug(ctx, prodSlug); lookupErr == nil {
				return status.Error(codes.AlreadyExists, "product slug already exists")
			} else if !errors.Is(lookupErr, store.ErrNotFound) {
				return lookupErr
			}
		} else {
			base := slugify(name, "prod-"+prodID[:8])
			prodSlug = base
			if _, lookupErr := q.GetProductBySlug(ctx, prodSlug); lookupErr == nil {
				prodSlug = base + "-" + prodID[:8]
			} else if !errors.Is(lookupErr, store.ErrNotFound) {
				return lookupErr
			}
		}

		p, createErr := q.CreateProduct(ctx, store.Product{
			ID:              prodID,
			ShopID:          shopID,
			CategoryID:      categoryID,
			Name:            name,
			Slug:            prodSlug,
			Description:     desc,
			Images:          req.GetImages(),
			Status:          store.ProductStatusPending,
			MinPriceVND:     minPrice,
			MaxPriceVND:     maxPrice,
			FlaggedKeywords: modRes.FlaggedTerms,
			ShopSuspended:   shopSuspended,
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
				MinPrice30dVND:    in.PriceVND,
				Stock:             in.Stock,
				WeightGrams:       in.WeightGrams,
				ImageURL:          in.ImageURL,
				CategoryID:        categoryID,
				CommissionRateBps: cat.CommissionRateBps,
				Status:            skuStatus,
				ProductStatus:     p.Status,
				ShopSuspended:     p.ShopSuspended,
			})
			if skuErr != nil {
				return skuErr
			}
			if phErr := q.InsertSKUPriceHistory(ctx, store.SKUPriceHistoryEntry{
				ID:         uuid.NewString(),
				SKUID:      sk.ID,
				PriceVND:   sk.PriceVND,
				ActorID:    actorID,
				Reason:     "create sku",
				RecordedAt: now,
			}); phErr != nil {
				return phErr
			}
			createdSKUs = append(createdSKUs, sk)
		}
		return nil
	})
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		switch {
		case errors.Is(err, store.ErrAlreadyExists):
			return nil, status.Error(codes.AlreadyExists, "duplicate product slug or sku_code")
		case errors.Is(err, store.ErrForeignKeyViolation):
			return nil, status.Error(codes.NotFound, "category not found")
		default:
			return nil, status.Errorf(codes.Internal, "create product: %v", err)
		}
	}

	return &productv1.CreateProductResponse{
		Product: toProtoProduct(createdProd, createdSKUs, true),
	}, nil
}

func (s *Server) UpdateProduct(ctx context.Context, req *productv1.UpdateProductRequest) (*productv1.UpdateProductResponse, error) {
	shopID, claims, isAdmin, err := resolveSellerOrAdminShop(ctx, req.GetShopId())
	if err != nil {
		return nil, err
	}

	id := strings.TrimSpace(req.GetId())
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "product id is required")
	}

	rawSlug := strings.TrimSpace(req.GetSlug())
	var normalizedReqSlug string
	if rawSlug != "" {
		normalizedReqSlug = normalizeSlug(rawSlug)
		if normalizedReqSlug == "" {
			return nil, status.Error(codes.InvalidArgument, "slug must contain letters or digits")
		}
	}

	var (
		skuInputs   []normalizedSKUInput
		reqMinPrice int64
		reqMaxPrice int64
	)
	if len(req.GetSkus()) > 0 {
		skuInputs, reqMinPrice, reqMaxPrice, err = validateSKUInputs(req.GetSkus())
		if err != nil {
			return nil, err
		}
	}

	now := s.now()
	ctx = store.WithAsOf(ctx, now)

	var (
		updatedProd store.Product
		resultSKUs  []store.SKU
	)

	// Lock order: shop_suspension (seller) -> products (FOR UPDATE) -> skus -> stock_history / sku_price_history.
	// Reading product + SKUs, verifying ownership, computing nextStatus/isHideOnly, running moderation,
	// and writing SKUs + product all happen inside the same transaction to eliminate TOCTOU races.
	err = s.store.WithTx(ctx, func(q store.Queries) error {
		if !isAdmin {
			if lockErr := q.LockShopSuspension(ctx, shopID); lockErr != nil {
				return lockErr
			}
			suspended, suspErr := q.IsShopSuspended(ctx, shopID)
			if suspErr != nil {
				return suspErr
			}
			if suspended {
				return status.Error(codes.FailedPrecondition, "SHOP_SUSPENDED: shop is suspended and cannot update products")
			}
		}

		existing, getErr := q.GetProductByIDForUpdate(ctx, id)
		if getErr != nil {
			if errors.Is(getErr, store.ErrNotFound) {
				return status.Error(codes.NotFound, "product not found")
			}
			return status.Errorf(codes.Internal, "get product: %v", getErr)
		}
		if !isAdmin && existing.ShopID != shopID {
			return status.Error(codes.PermissionDenied, "cannot update product belonging to another shop")
		}

		categoryID := strings.TrimSpace(req.GetCategoryId())
		if categoryID == "" {
			categoryID = existing.CategoryID
		}
		cat, catErr := q.GetCategoryByID(ctx, categoryID)
		if catErr != nil {
			if errors.Is(catErr, store.ErrNotFound) {
				return status.Error(codes.NotFound, "category not found")
			}
			return status.Errorf(codes.Internal, "lookup category: %v", catErr)
		}

		name := strings.TrimSpace(req.GetName())
		if name == "" {
			name = existing.Name
		}
		prodSlug := existing.Slug
		if normalizedReqSlug != "" {
			prodSlug = normalizedReqSlug
		}
		if prodSlug == "" {
			prodSlug = slugify(name, "prod-"+id[:min(8, len(id))])
		}
		desc := existing.Description
		if req.GetDescription() != "" {
			desc = strings.TrimSpace(req.GetDescription())
		}
		images := existing.Images
		if len(req.GetImages()) > 0 {
			images = req.GetImages()
		}

		nextStatus, statusErr := resolveUpdatedProductStatus(existing.Status, req.GetStatus(), isAdmin)
		if statusErr != nil {
			return statusErr
		}

		minPrice := existing.MinPriceVND
		maxPrice := existing.MaxPriceVND
		if len(skuInputs) > 0 {
			minPrice = reqMinPrice
			maxPrice = reqMaxPrice
		}

		var (
			allSKUs         []store.SKU
			activeByID      map[string]store.SKU
			inactiveByCode  map[string]store.SKU
			existingModSKUs []moderationSKU
			incomingModSKUs []moderationSKU
		)
		if len(skuInputs) > 0 {
			var listErr error
			allSKUs, listErr = q.ListAllSKUsByProductID(ctx, id)
			if listErr != nil {
				return listErr
			}
			activeByID = make(map[string]store.SKU, len(allSKUs))
			inactiveByCode = make(map[string]store.SKU)
			activeSKUs := make([]store.SKU, 0, len(allSKUs))
			for _, sk := range allSKUs {
				if sk.Status == store.SKUStatusInactive {
					inactiveByCode[sk.SKUCode] = sk
				} else {
					activeByID[sk.ID] = sk
					activeSKUs = append(activeSKUs, sk)
				}
			}
			existingModSKUs = toModerationSKUsFromStore(activeSKUs)
			incomingModSKUs = toModerationSKUsFromInputs(skuInputs)
		} else {
			curSKUs, listErr := q.ListSKUsByProductIDs(ctx, []string{id})
			if listErr != nil {
				return status.Errorf(codes.Internal, "list product skus: %v", listErr)
			}
			existingModSKUs = toModerationSKUsFromStore(curSKUs)
			incomingModSKUs = existingModSKUs
		}

		// Item 7 / B: hiding a violating product is permitted when nextStatus == HIDDEN, category is unchanged,
		// and the canonical set of moderated fields (name, description, slug, SKU names + SKU attributes) is unchanged.
		isHideOnly := nextStatus == store.ProductStatusHidden &&
			categoryID == existing.CategoryID &&
			moderationContentUnchanged(existing, existingModSKUs, name, desc, prodSlug, incomingModSKUs)

		flaggedKeywords := existing.FlaggedKeywords
		if !isHideOnly {
			modRes, modErr := s.checkProductModeration(ctx, q, categoryID, name, desc, productModerationSlug{
				ProductID:   id,
				Name:        name,
				Slug:        prodSlug,
				RawSlug:     rawSlug,
				PrevSlug:    existing.Slug,
				HasPrevSlug: true,
			}, incomingModSKUs)
			if modErr != nil {
				return modErr
			}
			if nextStatus == store.ProductStatusHidden {
				// A: Never record unapproved REVIEW keywords as pre-approved while hiding or editing under HIDDEN;
				// retain only the intersection with existing.FlaggedKeywords so unhiding still triggers PENDING.
				flaggedKeywords = intersectFlaggedKeywords(existing.FlaggedKeywords, modRes.FlaggedTerms)
			} else {
				flaggedKeywords = modRes.FlaggedTerms
				// Item 3 & E10: demote APPROVED/OUT_OF_STOCK to PENDING only when a NEW REVIEW keyword
				// (not already present in existing.FlaggedKeywords) matches; subsets preserve status.
				if hasNewFlaggedKeywords(existing.FlaggedKeywords, modRes.FlaggedTerms) &&
					(nextStatus == store.ProductStatusApproved || nextStatus == store.ProductStatusOutOfStock) {
					nextStatus = store.ProductStatusPending
				}
			}
		}

		if len(skuInputs) > 0 {
			keptIDs := make(map[string]bool, len(skuInputs))
			for _, in := range skuInputs {
				if in.ID != "" {
					if _, ok := activeByID[in.ID]; !ok {
						return status.Errorf(codes.NotFound, "sku %s does not belong to product %s", in.ID, id)
					}
					keptIDs[in.ID] = true
				}
			}

			toDeactivate := make([]string, 0)
			for oldID := range activeByID {
				if !keptIDs[oldID] {
					toDeactivate = append(toDeactivate, oldID)
				}
			}
			sort.Strings(toDeactivate)
			for _, oldID := range toDeactivate {
				if deactErr := q.DeactivateSKU(ctx, oldID); deactErr != nil {
					return deactErr
				}
				oldSKU := activeByID[oldID]
				oldSKU.Status = store.SKUStatusInactive
				inactiveByCode[oldSKU.SKUCode] = oldSKU
			}

			actorID := claims.UserID()
			for _, in := range skuInputs {
				skuStatus := store.SKUStatusActive
				if in.Stock == 0 {
					skuStatus = store.SKUStatusOutOfStock
				}
				if in.ID != "" {
					cur := activeByID[in.ID]
					if in.Stock != cur.Stock {
						if _, stockErr := applySKUStockChange(ctx, q, cur, in.Stock, in.ExpectedVersion, actorID, "seller product update"); stockErr != nil {
							return stockErr
						}
					} else if in.ExpectedVersion > 0 && in.ExpectedVersion != cur.Version {
						return store.ErrVersionConflict
					}
					if _, updErr := q.UpdateSKUMetadata(ctx, store.SKU{
						ID:          cur.ID,
						ProductID:   id,
						SKUCode:     in.SKUCode,
						Name:        in.Name,
						Attributes:  in.Attributes,
						PriceVND:    in.PriceVND,
						WeightGrams: in.WeightGrams,
						ImageURL:    in.ImageURL,
					}); updErr != nil {
						return updErr
					}
					if in.PriceVND != cur.PriceVND {
						if phErr := q.InsertSKUPriceHistory(ctx, store.SKUPriceHistoryEntry{
							ID:         uuid.NewString(),
							SKUID:      cur.ID,
							PriceVND:   in.PriceVND,
							ActorID:    actorID,
							Reason:     "update sku price",
							RecordedAt: now,
						}); phErr != nil {
							return phErr
						}
					}
				} else if old, ok := inactiveByCode[in.SKUCode]; ok {
					if in.Stock < old.ReservedStock {
						return store.ErrInvalidStock
					}
					ver := in.ExpectedVersion
					if ver <= 0 {
						ver = old.Version
					}
					upd, reactErr := q.ReactivateSKUOptimistic(ctx, store.SKU{
						ID:          old.ID,
						ProductID:   id,
						SKUCode:     in.SKUCode,
						Name:        in.Name,
						Attributes:  in.Attributes,
						PriceVND:    in.PriceVND,
						Stock:       in.Stock,
						WeightGrams: in.WeightGrams,
						ImageURL:    in.ImageURL,
					}, ver)
					if reactErr != nil {
						return reactErr
					}
					if in.PriceVND != old.PriceVND {
						if phErr := q.InsertSKUPriceHistory(ctx, store.SKUPriceHistoryEntry{
							ID:         uuid.NewString(),
							SKUID:      old.ID,
							PriceVND:   in.PriceVND,
							ActorID:    actorID,
							Reason:     "reactivate sku price",
							RecordedAt: now,
						}); phErr != nil {
							return phErr
						}
					}
					if in.Stock != old.Stock {
						if histErr := q.InsertStockHistory(ctx, store.StockHistoryEntry{
							ID:             uuid.NewString(),
							SKUID:          old.ID,
							ChangeType:     "MANUAL_UPDATE",
							Delta:          in.Stock - old.Stock,
							StockBefore:    old.Stock,
							StockAfter:     upd.Stock,
							ReservedBefore: old.ReservedStock,
							ReservedAfter:  upd.ReservedStock,
							ActorID:        actorID,
							Reason:         "seller product update",
						}); histErr != nil {
							return histErr
						}
					}
					delete(inactiveByCode, in.SKUCode)
				} else {
					createdSKU, createErr := q.CreateSKU(ctx, store.SKU{
						ID:                uuid.NewString(),
						ProductID:         id,
						ShopID:            existing.ShopID,
						ProductName:       name,
						SKUCode:           in.SKUCode,
						Name:              in.Name,
						Attributes:        in.Attributes,
						PriceVND:          in.PriceVND,
						MinPrice30dVND:    in.PriceVND,
						Stock:             in.Stock,
						WeightGrams:       in.WeightGrams,
						ImageURL:          in.ImageURL,
						CategoryID:        categoryID,
						CommissionRateBps: cat.CommissionRateBps,
						Status:            skuStatus,
						ProductStatus:     nextStatus,
						ShopSuspended:     existing.ShopSuspended,
					})
					if createErr != nil {
						return createErr
					}
					if phErr := q.InsertSKUPriceHistory(ctx, store.SKUPriceHistoryEntry{
						ID:         uuid.NewString(),
						SKUID:      createdSKU.ID,
						PriceVND:   createdSKU.PriceVND,
						ActorID:    actorID,
						Reason:     "create sku",
						RecordedAt: now,
					}); phErr != nil {
						return phErr
					}
				}
			}
		}

		if nextStatus == store.ProductStatusApproved || nextStatus == store.ProductStatusOutOfStock {
			inStockCount, cntErr := q.CountActiveInStockSKUsByProduct(ctx, id)
			if cntErr != nil {
				return cntErr
			}
			if nextStatus == store.ProductStatusApproved && inStockCount == 0 {
				if existing.Status == store.ProductStatusOutOfStock && req.GetStatus() == productv1.ProductStatus_PRODUCT_STATUS_APPROVED && !isAdmin {
					return status.Error(codes.FailedPrecondition, "cannot set product status to APPROVED when all SKUs have zero stock")
				}
				nextStatus = store.ProductStatusOutOfStock
			} else if nextStatus == store.ProductStatusOutOfStock && inStockCount > 0 {
				nextStatus = store.ProductStatusApproved
			}
		}

		rejectionReason := existing.RejectionReason
		if nextStatus == store.ProductStatusPending || nextStatus == store.ProductStatusApproved {
			rejectionReason = ""
		}

		if prodSlug != existing.Slug {
			if other, lookupErr := q.GetProductBySlug(ctx, prodSlug); lookupErr == nil && other.ID != id {
				return status.Error(codes.AlreadyExists, "product slug already exists")
			} else if lookupErr != nil && !errors.Is(lookupErr, store.ErrNotFound) {
				return lookupErr
			}
		}

		p, updErr := q.UpdateProduct(ctx, store.Product{
			ID:              id,
			ShopID:          existing.ShopID,
			CategoryID:      categoryID,
			Name:            name,
			Slug:            prodSlug,
			Description:     desc,
			Images:          images,
			Status:          nextStatus,
			RejectionReason: rejectionReason,
			MinPriceVND:     minPrice,
			MaxPriceVND:     maxPrice,
			FlaggedKeywords: flaggedKeywords,
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
		return nil
	})
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		switch {
		case errors.Is(err, store.ErrAlreadyExists):
			return nil, status.Error(codes.AlreadyExists, "duplicate sku_code within product")
		case errors.Is(err, store.ErrVersionConflict):
			return nil, status.Error(codes.Aborted, "STOCK_VERSION_CONFLICT: sku was concurrently modified; please reload and retry")
		case errors.Is(err, store.ErrInvalidStock):
			return nil, status.Error(codes.FailedPrecondition, "new_stock cannot be less than reserved_stock")
		case errors.Is(err, store.ErrSKUInactive):
			return nil, status.Error(codes.FailedPrecondition, "cannot update stock of inactive SKU")
		case errors.Is(err, store.ErrNotFound):
			return nil, status.Error(codes.NotFound, "sku not found")
		case errors.Is(err, store.ErrForeignKeyViolation):
			return nil, status.Error(codes.NotFound, "category not found")
		default:
			return nil, status.Errorf(codes.Internal, "update product: %v", err)
		}
	}

	return &productv1.UpdateProductResponse{
		Product: toProtoProduct(updatedProd, resultSKUs, true),
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

	now := s.now()
	ctx = store.WithAsOf(ctx, now)

	// Lock order: shop_suspension (seller) -> products (FOR UPDATE) -> skus (deactivate).
	err = s.store.WithTx(ctx, func(q store.Queries) error {
		if !isAdmin {
			if lockErr := q.LockShopSuspension(ctx, shopID); lockErr != nil {
				return lockErr
			}
			suspended, suspErr := q.IsShopSuspended(ctx, shopID)
			if suspErr != nil {
				return suspErr
			}
			if suspended {
				return status.Error(codes.FailedPrecondition, "SHOP_SUSPENDED: shop is suspended and cannot delete products")
			}
		}

		existing, getErr := q.GetProductByIDForUpdate(ctx, id)
		if getErr != nil {
			return getErr
		}
		if !isAdmin && existing.ShopID != shopID {
			return status.Error(codes.PermissionDenied, "cannot delete product belonging to another shop")
		}
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
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "product not found")
		}
		return nil, status.Errorf(codes.Internal, "delete product: %v", err)
	}
	return &productv1.DeleteProductResponse{Ok: true}, nil
}

func (s *Server) AdminReviewProduct(ctx context.Context, req *productv1.AdminReviewProductRequest) (*productv1.AdminReviewProductResponse, error) {
	if _, err := requireCapability(ctx, auth.CapProductsReview); err != nil {
		return nil, err
	}
	productID := strings.TrimSpace(req.GetProductId())
	if productID == "" {
		return nil, status.Error(codes.InvalidArgument, "product_id is required")
	}

	var decisionStr, reason string
	isApprove := false
	switch req.GetDecision() {
	case productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE:
		isApprove = true
		decisionStr = "APPROVE"
		reason = ""
	case productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_REJECT:
		decisionStr = "REJECT"
		reason = strings.TrimSpace(req.GetReason())
		if reason == "" {
			return nil, status.Error(codes.InvalidArgument, "rejection reason is required")
		}
	default:
		return nil, status.Error(codes.InvalidArgument, "review decision must be APPROVE or REJECT")
	}

	now := s.now()
	ctx = store.WithAsOf(ctx, now)
	traceID, traceparent := traceMetaFromContext(ctx)

	var (
		updated       store.Product
		skus          []store.SKU
		emittedOutbox bool
	)
	err := s.store.WithTx(ctx, func(q store.Queries) error {
		// Lock product row (excludes soft-deleted rows -> ErrNotFound).
		cur, getErr := q.GetProductByIDForUpdate(ctx, productID)
		if getErr != nil {
			return getErr
		}

		if isApprove {
			// Seller-hidden product must not be silently re-opened by admin APPROVE.
			if cur.Status == store.ProductStatusHidden {
				return status.Error(codes.FailedPrecondition, "cannot approve a product hidden by seller; seller must unhide or resubmit it")
			}
			// Idempotent no-op when already APPROVED or OUT_OF_STOCK: do not emit duplicate product.reviewed.
			if cur.Status == store.ProductStatusApproved || cur.Status == store.ProductStatusOutOfStock {
				loadedSKUs, skuErr := q.ListSKUsByProductIDs(ctx, []string{productID})
				if skuErr != nil {
					return skuErr
				}
				updated = cur
				skus = loadedSKUs
				return nil
			}

			// E10 & P1-1: AdminReviewProduct(APPROVE) blocks prohibited categories and content BLOCK keywords
			// (name/description/SKUs), while permitting REVIEW-flagged and slug-only BLOCK products after
			// moderator inspection using the same evaluation as RescanProductsForBannedContent.
			loadedSKUs, skuErr := q.ListSKUsByProductIDs(ctx, []string{productID})
			if skuErr != nil {
				return skuErr
			}
			modMatch, modErr := s.checkStoredProductApprovalModeration(ctx, q, cur, toModerationSKUsFromStore(loadedSKUs))
			if modErr != nil {
				return modErr
			}

			p, updErr := q.UpdateProductReview(ctx, productID, store.ProductStatusApproved, "", modMatch.effectiveReviewTerms)
			if updErr != nil {
				return updErr
			}
			// Transition to OUT_OF_STOCK immediately if all active SKUs have zero stock.
			if syncErr := syncProductStockStatus(ctx, q, productID, p.Status, 0); syncErr != nil {
				return syncErr
			}
			refreshed, refErr := q.GetProductByID(ctx, productID)
			if refErr != nil {
				return refErr
			}
			p = refreshed
			refreshedSKUs, skuErr := q.ListSKUsByProductIDs(ctx, []string{productID})
			if skuErr != nil {
				return skuErr
			}

			payloadBytes, mErr := json.Marshal(events.ProductReviewedPayload{
				ProductID:   p.ID,
				ProductName: p.Name,
				ShopID:      p.ShopID,
				Decision:    decisionStr,
				Reason:      reason,
				OccurredAt:  now.Format(time.RFC3339),
			})
			if mErr != nil {
				return fmt.Errorf("marshal product.reviewed payload: %w", mErr)
			}
			if obErr := q.InsertOutboxEvent(ctx, store.OutboxEvent{
				ID:          uuid.NewString(),
				EventType:   events.SubjectProductReviewed,
				AggregateID: p.ID,
				TraceID:     traceID,
				Traceparent: traceparent,
				PayloadJSON: payloadBytes,
				CreatedAt:   now,
			}); obErr != nil {
				return obErr
			}
			updated = p
			skus = refreshedSKUs
			emittedOutbox = true
			return nil
		}

		// Decision == REJECT: idempotent no-op when already REJECTED with the same reason.
		// Admin takedown from APPROVED / OUT_OF_STOCK / HIDDEN -> REJECTED remains permitted.
		if cur.Status == store.ProductStatusRejected && cur.RejectionReason == reason {
			loadedSKUs, skuErr := q.ListSKUsByProductIDs(ctx, []string{productID})
			if skuErr != nil {
				return skuErr
			}
			updated = cur
			skus = loadedSKUs
			return nil
		}

		p, updErr := q.UpdateProductReview(ctx, productID, store.ProductStatusRejected, reason, cur.FlaggedKeywords)
		if updErr != nil {
			return updErr
		}
		payloadBytes, mErr := json.Marshal(events.ProductReviewedPayload{
			ProductID:   p.ID,
			ProductName: p.Name,
			ShopID:      p.ShopID,
			Decision:    decisionStr,
			Reason:      reason,
			OccurredAt:  now.Format(time.RFC3339),
		})
		if mErr != nil {
			return fmt.Errorf("marshal product.reviewed payload: %w", mErr)
		}
		if obErr := q.InsertOutboxEvent(ctx, store.OutboxEvent{
			ID:          uuid.NewString(),
			EventType:   events.SubjectProductReviewed,
			AggregateID: p.ID,
			TraceID:     traceID,
			Traceparent: traceparent,
			PayloadJSON: payloadBytes,
			CreatedAt:   now,
		}); obErr != nil {
			return obErr
		}
		loadedSKUs, skuErr := q.ListSKUsByProductIDs(ctx, []string{productID})
		if skuErr != nil {
			return skuErr
		}
		updated = p
		skus = loadedSKUs
		emittedOutbox = true
		return nil
	})
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "product not found")
		}
		return nil, status.Errorf(codes.Internal, "review product: %v", err)
	}

	if emittedOutbox {
		s.triggerOutbox()
	}

	return &productv1.AdminReviewProductResponse{
		Product: toProtoProduct(updated, skus, true),
	}, nil
}

func (s *Server) GetProduct(ctx context.Context, req *productv1.GetProductRequest) (*productv1.GetProductResponse, error) {
	id := strings.TrimSpace(req.GetId())
	slug := strings.ToLower(strings.TrimSpace(req.GetSlug()))
	if id == "" && slug == "" {
		return nil, status.Error(codes.InvalidArgument, "product id or slug is required")
	}

	ctx = store.WithAsOf(ctx, s.now())
	var (
		prod store.Product
		err  error
	)
	if slug != "" {
		prod, err = s.store.GetProductBySlug(ctx, slug)
	} else {
		prod, err = s.store.GetProductByID(ctx, id)
	}
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// E13: Previously-approved soft-deleted products return a discontinued stub (no name, no SKUs/price) on slug lookup.
			if slug != "" && req.GetIncludeDiscontinuedStub() {
				delProd, delErr := s.store.GetDeletedApprovedProductBySlug(ctx, slug)
				if delErr == nil {
					return &productv1.GetProductResponse{
						Discontinued:           true,
						DiscontinuedCategoryId: delProd.CategoryID,
					}, nil
				}
				if !errors.Is(delErr, store.ErrNotFound) {
					return nil, status.Errorf(codes.Internal, "get discontinued product: %v", delErr)
				}
			}
			return nil, status.Error(codes.NotFound, "product not found")
		}
		return nil, status.Errorf(codes.Internal, "get product: %v", err)
	}
	if !canViewProduct(ctx, prod) {
		// E13 & F12: Previously-approved HIDDEN/REJECTED/shop-suspended products return a discontinued stub
		// on public slug lookup without leaking suspension reason. discontinued_name is populated ONLY when
		// current status is HIDDEN and shop is not suspended. Never-approved products still 404.
		if slug != "" && req.GetIncludeDiscontinuedStub() && prod.FirstApprovedAt != nil &&
			(prod.Status == store.ProductStatusHidden ||
				prod.Status == store.ProductStatusRejected ||
				(prod.ShopSuspended && (prod.Status == store.ProductStatusApproved || prod.Status == store.ProductStatusOutOfStock))) {
			discontinuedName := ""
			if prod.Status == store.ProductStatusHidden && !prod.ShopSuspended {
				discontinuedName = prod.Name
			}
			return &productv1.GetProductResponse{
				Discontinued:           true,
				DiscontinuedCategoryId: prod.CategoryID,
				DiscontinuedName:       discontinuedName,
			}, nil
		}
		return nil, status.Error(codes.NotFound, "product not found")
	}
	skus, err := s.store.ListSKUsByProductIDs(ctx, []string{prod.ID})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list product skus: %v", err)
	}
	return &productv1.GetProductResponse{
		Product: toProtoProduct(prod, skus, canViewInternalProductFields(ctx, prod)),
	}, nil
}

func (s *Server) ListProducts(ctx context.Context, req *productv1.ListProductsRequest) (*productv1.ListProductsResponse, error) {
	ctx = store.WithAsOf(ctx, s.now())
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

	rawSKUIDs := req.GetSkuIds()
	if len(rawSKUIDs) > maxListProductsSKUIDs {
		return nil, status.Errorf(codes.InvalidArgument, "sku_ids exceeds maximum limit of %d", maxListProductsSKUIDs)
	}
	var skuIDs []string
	if len(rawSKUIDs) > 0 {
		seen := make(map[string]struct{}, len(rawSKUIDs))
		skuIDs = make([]string, 0, len(rawSKUIDs))
		for _, raw := range rawSKUIDs {
			id := strings.TrimSpace(raw)
			if id == "" {
				return nil, status.Error(codes.InvalidArgument, "sku_ids must not contain empty values")
			}
			if _, exists := seen[id]; !exists {
				seen[id] = struct{}{}
				skuIDs = append(skuIDs, id)
			}
		}
	}

	statusFilter := fromProtoProductStatus(req.GetStatus())
	shopID := strings.TrimSpace(req.GetShopId())

	publicOnly := true
	if len(skuIDs) == 0 {
		if claims, ok := auth.ClaimsFromContext(ctx); ok {
			if auth.HasCapability(claims, auth.CapProductsRead) {
				publicOnly = false
			} else if auth.HasRole(claims, auth.RoleSeller) && shopID != "" && claims.ShopID == shopID {
				publicOnly = false
			}
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
		SKUIDs:      skuIDs,
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
		out = append(out, toProtoProduct(p, skusByProd[p.ID], canViewInternalProductFields(ctx, p)))
	}
	return &productv1.ListProductsResponse{
		Products: out,
		Total:    total,
	}, nil
}

func (s *Server) GetSKUsBatch(ctx context.Context, req *productv1.GetSKUsBatchRequest) (*productv1.GetSKUsBatchResponse, error) {
	ctx = store.WithAsOf(ctx, s.now())
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
		if sk.IsPubliclyActive() || canViewHiddenSKU(ctx, sk) {
			out = append(out, toProtoSKU(sk))
		} else {
			out = append(out, toRedactedProtoSKU(sk))
		}
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

	ctx = store.WithAsOf(ctx, s.now())
	var updatedSKU store.SKU
	// Lock order: shop_suspension (seller) -> products (FOR UPDATE) -> skus -> stock_history, matching UpdateProduct/AdminReview/Rescan.
	err = s.store.WithTx(ctx, func(q store.Queries) error {
		if !isAdmin {
			if lockErr := q.LockShopSuspension(ctx, shopID); lockErr != nil {
				return lockErr
			}
			suspended, suspErr := q.IsShopSuspended(ctx, shopID)
			if suspErr != nil {
				return suspErr
			}
			if suspended {
				return status.Error(codes.FailedPrecondition, "SHOP_SUSPENDED: shop is suspended and cannot update stock")
			}
		}

		lookup, getErr := q.GetSKUWithContext(ctx, skuID)
		if getErr != nil {
			return getErr
		}
		if lookup.ProductDeleted {
			return store.ErrNotFound
		}
		prod, lockErr := q.GetProductByIDForUpdate(ctx, lookup.ProductID)
		if lockErr != nil {
			return lockErr
		}
		if !isAdmin && prod.ShopID != shopID {
			return status.Error(codes.PermissionDenied, "cannot update stock of another shop's SKU")
		}
		cur, getErr := q.GetSKUWithContext(ctx, skuID)
		if getErr != nil {
			return getErr
		}

		upd, stockErr := applySKUStockChange(ctx, q, cur, req.GetNewStock(), req.GetExpectedVersion(), claims.UserID(), "seller manual stock update")
		if stockErr != nil {
			return stockErr
		}

		if syncErr := syncProductStockStatus(ctx, q, prod.ID, prod.Status, upd.Stock); syncErr != nil {
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
		case errors.Is(err, store.ErrSKUInactive):
			return nil, status.Error(codes.FailedPrecondition, "cannot update stock of inactive SKU")
		default:
			return nil, status.Errorf(codes.Internal, "update stock: %v", err)
		}
	}

	return &productv1.UpdateStockResponse{Sku: toProtoSKU(updatedSKU)}, nil
}

func (s *Server) ReserveStock(ctx context.Context, req *productv1.ReserveStockRequest) (*productv1.ReserveStockResponse, error) {
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
			if lockErr := q.LockStockOrder(ctx, orderID); lockErr != nil {
				return lockErr
			}
			tombstoned, tErr := q.HasStockReleaseTombstone(ctx, orderID)
			if tErr != nil {
				return tErr
			}
			if tombstoned {
				return status.Error(codes.FailedPrecondition, "stock reservation for this order was already released")
			}

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
			for _, item := range items {
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
		if errors.Is(txErr, store.ErrOrderAlreadyReleased) {
			return nil, status.Error(codes.FailedPrecondition, "stock reservation for this order was already released")
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
	if _, err := requireServiceOrAdmin(ctx); err != nil {
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
			if lockErr := q.LockStockOrder(ctx, orderID); lockErr != nil {
				return lockErr
			}
			rows, listErr := q.ListReservationsByOrderID(ctx, orderID, true)
			if listErr != nil {
				return listErr
			}
			if len(rows) == 0 {
				// Record release tombstone so any delayed ReserveStock for this order_id cannot leak stuck stock.
				// First tombstone insert reports Released=false, AlreadyReleased=false; repeat hits on an
				// existing tombstone report Released=true, AlreadyReleased=true (matching the allReleased branch).
				inserted, tErr := q.InsertStockReleaseTombstone(ctx, orderID)
				if tErr != nil {
					return tErr
				}
				resp = &productv1.ReleaseStockResponse{
					Released:        !inserted,
					AlreadyReleased: !inserted,
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

			for _, r := range reservedRows {
				changed, trErr := q.TransitionStockReservation(ctx, r.ID, store.ReservationStatusReleased, store.ReservationStatusReserved)
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
	if _, err := requireServiceOrAdmin(ctx); err != nil {
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
			if lockErr := q.LockStockOrder(ctx, orderID); lockErr != nil {
				return lockErr
			}
			rows, listErr := q.ListReservationsByOrderID(ctx, orderID, true)
			if listErr != nil {
				return listErr
			}
			if len(rows) == 0 {
				tombstoned, tErr := q.HasStockReleaseTombstone(ctx, orderID)
				if tErr != nil {
					return tErr
				}
				if tombstoned {
					return status.Error(codes.FailedPrecondition, "cannot confirm stock reservation that was already released")
				}
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

			// Lock owning products in sorted product_id order BEFORE updating SKUs so lock order stays
			// products -> skus -> stock_history (eliminating AB-BA deadlocks with UpdateProduct/UpdateStock).
			skuIDs := make([]string, 0, len(reservedRows))
			for _, r := range reservedRows {
				skuIDs = append(skuIDs, r.SKUID)
			}
			batchSKUs, batchErr := q.GetSKUsBatch(ctx, skuIDs)
			if batchErr != nil {
				return batchErr
			}
			lockedProdStatus := make(map[string]string, len(batchSKUs))
			prodIDSet := make(map[string]struct{}, len(batchSKUs))
			for _, sk := range batchSKUs {
				prodIDSet[sk.ProductID] = struct{}{}
			}
			sortedProdIDs := make([]string, 0, len(prodIDSet))
			for pid := range prodIDSet {
				sortedProdIDs = append(sortedProdIDs, pid)
			}
			sort.Strings(sortedProdIDs)
			for _, pid := range sortedProdIDs {
				if p, lockErr := q.GetProductByIDForUpdateAny(ctx, pid); lockErr == nil {
					lockedProdStatus[pid] = p.Status
				} else if !errors.Is(lockErr, store.ErrNotFound) {
					return lockErr
				}
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
				if st, ok := lockedProdStatus[sku.ProductID]; ok {
					touchedProducts[sku.ProductID] = st
				} else {
					touchedProducts[sku.ProductID] = sku.ProductStatus
				}

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

			for _, prodID := range sortedProdIDs {
				if prodStatus, ok := touchedProducts[prodID]; ok {
					if syncErr := syncProductStockStatus(ctx, q, prodID, prodStatus, 0); syncErr != nil {
						return syncErr
					}
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

func (s *Server) RestockOrderItems(ctx context.Context, req *productv1.RestockOrderItemsRequest) (*productv1.RestockOrderItemsResponse, error) {
	claims, err := requireServiceOrAdmin(ctx)
	if err != nil {
		return nil, err
	}
	orderID := strings.TrimSpace(req.GetOrderId())
	if orderID == "" {
		return nil, status.Error(codes.InvalidArgument, "order_id is required")
	}
	items, err := normalizeAndSortReservationItems(req.GetItems())
	if err != nil {
		return nil, err
	}

	actorID := strings.TrimSpace(claims.Service)
	if actorID == "" {
		actorID = claims.UserID()
	}

	var restockedCount int
	err = s.store.WithTx(ctx, func(q store.Queries) error {
		// Lock owning products in sorted product_id order BEFORE locking SKUs (products -> skus -> stock_history).
		skuIDs := make([]string, 0, len(items))
		for _, item := range items {
			skuIDs = append(skuIDs, item.skuID)
		}
		batchSKUs, batchErr := q.GetSKUsBatch(ctx, skuIDs)
		if batchErr != nil {
			return batchErr
		}
		lockedProdStatus := make(map[string]string, len(batchSKUs))
		prodIDSet := make(map[string]struct{}, len(batchSKUs))
		for _, sk := range batchSKUs {
			prodIDSet[sk.ProductID] = struct{}{}
		}
		sortedProdIDs := make([]string, 0, len(prodIDSet))
		for pid := range prodIDSet {
			sortedProdIDs = append(sortedProdIDs, pid)
		}
		sort.Strings(sortedProdIDs)
		for _, pid := range sortedProdIDs {
			if p, lockErr := q.GetProductByIDForUpdateAny(ctx, pid); lockErr == nil {
				lockedProdStatus[pid] = p.Status
			} else if !errors.Is(lockErr, store.ErrNotFound) {
				return lockErr
			}
		}

		type productSync struct {
			status      string
			latestStock int32
		}
		touchedProducts := make(map[string]productSync)

		for _, item := range items {
			sku, getErr := q.GetSKUWithContextForUpdate(ctx, item.skuID)
			if getErr != nil {
				if errors.Is(getErr, store.ErrNotFound) {
					return status.Errorf(codes.NotFound, "sku %s not found", item.skuID)
				}
				return getErr
			}

			inserted, histErr := q.TryInsertRestockHistory(ctx, store.StockHistoryEntry{
				ID:             uuid.NewString(),
				SKUID:          item.skuID,
				OrderID:        orderID,
				ChangeType:     "RESTOCK",
				Delta:          item.quantity,
				StockBefore:    sku.Stock,
				StockAfter:     sku.Stock + item.quantity,
				ReservedBefore: sku.ReservedStock,
				ReservedAfter:  sku.ReservedStock,
				ActorID:        actorID,
				Reason:         "order item restock",
			})
			if histErr != nil {
				return histErr
			}
			if !inserted {
				continue
			}

			updated, updErr := q.RestockSKUAtomic(ctx, item.skuID, item.quantity)
			if updErr != nil {
				return updErr
			}
			restockedCount++

			if updated.Status != store.SKUStatusInactive {
				ps := touchedProducts[sku.ProductID]
				if st, ok := lockedProdStatus[sku.ProductID]; ok {
					ps.status = st
				} else {
					ps.status = sku.ProductStatus
				}
				if updated.Stock > ps.latestStock {
					ps.latestStock = updated.Stock
				}
				touchedProducts[sku.ProductID] = ps
			}
		}

		prodIDs := make([]string, 0, len(touchedProducts))
		for prodID := range touchedProducts {
			prodIDs = append(prodIDs, prodID)
		}
		sort.Strings(prodIDs)
		for _, prodID := range prodIDs {
			ps := touchedProducts[prodID]
			if syncErr := syncProductStockStatus(ctx, q, prodID, ps.status, ps.latestStock); syncErr != nil {
				return syncErr
			}
		}
		return nil
	})
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "sku not found")
		}
		return nil, status.Errorf(codes.Internal, "restock order items: %v", err)
	}

	return &productv1.RestockOrderItemsResponse{
		Restocked:        true,
		AlreadyRestocked: restockedCount == 0,
	}, nil
}

// --- Helpers ---

func requireCapability(ctx context.Context, capName string) (*auth.Claims, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}
	if !auth.HasCapability(claims, capName) {
		return nil, status.Errorf(codes.PermissionDenied, "capability %s required", capName)
	}
	return claims, nil
}

func requireServiceOrAdmin(ctx context.Context) (*auth.Claims, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}
	if strings.TrimSpace(claims.Service) == "" && !auth.HasRole(claims, auth.RoleAdmin, auth.RoleSuperAdmin) {
		return nil, status.Error(codes.PermissionDenied, "service or admin identity required")
	}
	return claims, nil
}

func canViewProduct(ctx context.Context, prod store.Product) bool {
	if !prod.ShopSuspended && (prod.Status == store.ProductStatusApproved || prod.Status == store.ProductStatusOutOfStock) {
		return true
	}
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return false
	}
	if auth.HasCapability(claims, auth.CapProductsRead) {
		return true
	}
	if auth.HasRole(claims, auth.RoleSeller) && claims.ShopID != "" && claims.ShopID == prod.ShopID {
		return true
	}
	return false
}

func canViewHiddenSKU(ctx context.Context, sk store.SKU) bool {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return false
	}
	if auth.HasCapability(claims, auth.CapProductsRead) {
		return true
	}
	if auth.HasRole(claims, auth.RoleSeller) && strings.TrimSpace(claims.ShopID) != "" && claims.ShopID == sk.ShopID {
		return true
	}
	return false
}

// canViewInternalProductFields reports whether the caller is allowed to see internal product fields
// (flagged_keywords per E10-bis and shop_suspended per F12): owning shop seller and roles with products.read only.
func canViewInternalProductFields(ctx context.Context, prod store.Product) bool {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return false
	}
	if auth.HasCapability(claims, auth.CapProductsRead) {
		return true
	}
	if auth.HasRole(claims, auth.RoleSeller) && strings.TrimSpace(claims.ShopID) != "" && claims.ShopID == prod.ShopID {
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
	if auth.HasRole(claims, auth.RoleAdmin, auth.RoleSuperAdmin) {
		return reqShopID, claims, true, nil
	}
	if !auth.HasRole(claims, auth.RoleSeller) {
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
	ID              string
	SKUCode         string
	Name            string
	Attributes      map[string]string
	PriceVND        int64
	Stock           int32
	ExpectedVersion int64
	WeightGrams     int32
	ImageURL        string
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
		if current == store.ProductStatusApproved || current == store.ProductStatusOutOfStock {
			return next, nil
		}
		return "", status.Error(codes.PermissionDenied, "only approved products can be hidden by seller")
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
	skuID    string
	quantity int32
}

func normalizeAndSortReservationItems(raw []*productv1.StockReservationItem) ([]sortedReservationItem, error) {
	if len(raw) == 0 {
		return nil, status.Error(codes.InvalidArgument, "at least one reservation item is required")
	}
	merged := make(map[string]int32, len(raw))
	for i, it := range raw {
		skuID := strings.TrimSpace(it.GetSkuId())
		if skuID == "" {
			return nil, status.Errorf(codes.InvalidArgument, "items[%d].sku_id is required", i)
		}
		if it.GetQuantity() <= 0 {
			return nil, status.Errorf(codes.InvalidArgument, "items[%d].quantity must be > 0", i)
		}
		merged[skuID] += it.GetQuantity()
	}
	out := make([]sortedReservationItem, 0, len(merged))
	for skuID, qty := range merged {
		out = append(out, sortedReservationItem{skuID: skuID, quantity: qty})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].skuID < out[j].skuID
	})
	return out, nil
}

// applySKUStockChange routes SKU stock changes through optimistic locking and stock_history.
// If expectedVersion <= 0, it falls back to cur.Version read inside the same transaction.
func applySKUStockChange(
	ctx context.Context,
	q store.Queries,
	cur store.SKU,
	newStock int32,
	expectedVersion int64,
	actorID string,
	reason string,
) (store.SKU, error) {
	if cur.Status == store.SKUStatusInactive {
		return store.SKU{}, store.ErrSKUInactive
	}
	if newStock < cur.ReservedStock {
		return store.SKU{}, store.ErrInvalidStock
	}
	ver := expectedVersion
	if ver <= 0 {
		ver = cur.Version
	}
	upd, err := q.UpdateSKUStockOptimistic(ctx, cur.ID, newStock, ver)
	if err != nil {
		return store.SKU{}, err
	}
	if histErr := q.InsertStockHistory(ctx, store.StockHistoryEntry{
		ID:             uuid.NewString(),
		SKUID:          cur.ID,
		ChangeType:     "MANUAL_UPDATE",
		Delta:          newStock - cur.Stock,
		StockBefore:    cur.Stock,
		StockAfter:     upd.Stock,
		ReservedBefore: cur.ReservedStock,
		ReservedAfter:  upd.ReservedStock,
		ActorID:        actorID,
		Reason:         reason,
	}); histErr != nil {
		return store.SKU{}, histErr
	}
	return upd, nil
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
	s := normalizeSlug(name)
	if s == "" {
		if strings.Contains(fallback, "-") {
			return fallback
		}
		return "cat-" + fallback
	}
	return s
}

func normalizeSlug(input string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(input)) {
		r = store.FoldVietnameseRune(r)
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
		} else if unicode.IsSpace(r) || r == '-' || r == '_' || r == '/' || r == '.' {
			if b.Len() > 0 && !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func toProtoCategory(c store.Category) *productv1.Category {
	return &productv1.Category{
		Id:                c.ID,
		Name:              c.Name,
		Slug:              c.Slug,
		ParentId:          c.ParentID,
		CommissionRateBps: c.CommissionRateBps,
		SortOrder:         c.SortOrder,
		IsProhibited:      c.IsProhibited,
		CreatedAt:         timestamppb.New(c.CreatedAt),
		UpdatedAt:         timestamppb.New(c.UpdatedAt),
	}
}

func toProtoCategoryFeeSchedule(sc store.CategoryFeeSchedule) *productv1.CategoryFeeSchedule {
	return &productv1.CategoryFeeSchedule{
		Id:                sc.ID,
		CategoryId:        sc.CategoryID,
		CommissionRateBps: sc.CommissionRateBps,
		EffectiveFrom:     timestamppb.New(sc.EffectiveFrom),
		CreatedBy:         sc.CreatedBy,
		Note:              sc.Note,
		CreatedAt:         timestamppb.New(sc.CreatedAt),
	}
}

func toRedactedProtoSKU(s store.SKU) *productv1.SKU {
	return &productv1.SKU{
		Id:          s.ID,
		ProductId:   s.ProductID,
		ShopId:      s.ShopID,
		ProductName: s.ProductName,
		Name:        s.Name,
		ImageUrl:    s.ImageURL,
		IsActive:    false,
	}
}

func toProtoSKU(s store.SKU) *productv1.SKU {
	attrs := make(map[string]string, len(s.Attributes))
	for k, v := range s.Attributes {
		attrs[k] = v
	}
	minPrice30d := s.MinPrice30dVND
	if minPrice30d <= 0 {
		minPrice30d = s.PriceVND
	}
	return &productv1.SKU{
		Id:                s.ID,
		ProductId:         s.ProductID,
		ShopId:            s.ShopID,
		ProductName:       s.ProductName,
		SkuCode:           s.SKUCode,
		Name:              s.Name,
		Attributes:        attrs,
		PriceVnd:          s.PriceVND,
		MinPrice_30DVnd:   minPrice30d,
		Stock:             s.Stock,
		ReservedStock:     s.ReservedStock,
		AvailableStock:    s.AvailableStock(),
		Version:           s.Version,
		WeightGrams:       s.WeightGrams,
		ImageUrl:          s.ImageURL,
		CategoryId:        s.CategoryID,
		CommissionRateBps: s.CommissionRateBps,
		IsActive:          s.IsPubliclyActive(),
	}
}

func toProtoProduct(p store.Product, skus []store.SKU, includeInternal bool) *productv1.Product {
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

	var flagged []string
	if includeInternal && len(p.FlaggedKeywords) > 0 {
		flagged = make([]string, len(p.FlaggedKeywords))
		copy(flagged, p.FlaggedKeywords)
	}

	return &productv1.Product{
		Id:              p.ID,
		ShopId:          p.ShopID,
		CategoryId:      p.CategoryID,
		Name:            p.Name,
		Slug:            p.Slug,
		Description:     p.Description,
		Images:          imgs,
		Status:          toProtoProductStatus(p.Status),
		RejectionReason: p.RejectionReason,
		MinPriceVnd:     minPrice,
		MaxPriceVnd:     maxPrice,
		Skus:            protoSKUs,
		RatingAvgX100:   p.RatingAvgX100(),
		ReviewCount:     p.ReviewCount,
		FlaggedKeywords: flagged,
		ShopSuspended:   includeInternal && p.ShopSuspended,
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
