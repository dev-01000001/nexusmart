package server

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"sort"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	cartv1 "github.com/nexusmart/core/gen/go/nexusmart/cart/v1"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/cart/internal/resilience"
	"github.com/nexusmart/core/services/cart/internal/store"
)

const (
	unavailableReasonSKUInactive = "SKU_INACTIVE"
	unavailableReasonSKUNotFound = "SKU_NOT_FOUND"
)

// ProductClient is the subset of productv1.ProductServiceClient required by CartService.
type ProductClient interface {
	GetSKUsBatch(ctx context.Context, in *productv1.GetSKUsBatchRequest, opts ...grpc.CallOption) (*productv1.GetSKUsBatchResponse, error)
}

// UserClient is the optional subset of userv1.UserServiceClient used to enrich shop names.
type UserClient interface {
	GetShop(ctx context.Context, in *userv1.GetShopRequest, opts ...grpc.CallOption) (*userv1.GetShopResponse, error)
}

// Server implements cartv1.CartServiceServer.
type Server struct {
	cartv1.UnimplementedCartServiceServer
	store          store.Store
	productClient  ProductClient
	userClient     UserClient
	productBreaker *resilience.CircuitBreaker
	userBreaker    *resilience.CircuitBreaker
	logger         *slog.Logger
}

// New constructs a CartService gRPC server.
func New(st store.Store, productClient ProductClient, userClient UserClient, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		store:          st,
		productClient:  productClient,
		userClient:     userClient,
		productBreaker: resilience.NewCircuitBreaker(5, 15*time.Second),
		userBreaker:    resilience.NewCircuitBreaker(5, 15*time.Second),
		logger:         logger,
	}
}

func (s *Server) GetCart(ctx context.Context, req *cartv1.GetCartRequest) (*cartv1.GetCartResponse, error) {
	userID, err := resolveUserID(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	cart, err := s.buildCart(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &cartv1.GetCartResponse{Cart: cart}, nil
}

func (s *Server) AddItem(ctx context.Context, req *cartv1.AddItemRequest) (*cartv1.AddItemResponse, error) {
	userID, err := resolveUserID(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	skuID := strings.TrimSpace(req.GetSkuId())
	if skuID == "" {
		return nil, status.Error(codes.InvalidArgument, "sku_id is required")
	}
	if req.GetQuantity() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "quantity must be > 0")
	}

	sku, err := s.fetchSingleSKU(ctx, skuID)
	if err != nil {
		return nil, err
	}
	if !sku.GetIsActive() {
		return nil, status.Errorf(codes.FailedPrecondition, "SKU_NOT_ACTIVE: sku %s is not active", skuID)
	}
	if sku.GetPriceVnd() <= 0 {
		return nil, status.Errorf(codes.FailedPrecondition, "INVALID_PRICE: sku %s has invalid price", skuID)
	}
	if sku.GetAvailableStock() <= 0 {
		return nil, status.Errorf(codes.FailedPrecondition, "OUT_OF_STOCK: sku %s has 0 available stock", skuID)
	}

	existing, existed, err := s.store.Get(ctx, userID, skuID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "read cart item: %v", err)
	}

	desiredQty := int64(req.GetQuantity())
	addedPrice := sku.GetPriceVnd()
	if existed {
		if existing.Quantity > 0 {
			desiredQty += int64(existing.Quantity)
		}
		if existing.AddedPriceVND > 0 {
			addedPrice = existing.AddedPriceVND
		}
	}

	avail := int64(sku.GetAvailableStock())
	clamped := false
	if desiredQty > avail {
		desiredQty = avail
		clamped = true
	}
	if desiredQty > math.MaxInt32 {
		desiredQty = math.MaxInt32
		clamped = true
	}
	finalQty := int32(desiredQty)

	if err := s.store.Set(ctx, userID, skuID, store.Item{
		Quantity:      finalQty,
		AddedPriceVND: addedPrice,
		Selected:      true,
	}); err != nil {
		return nil, status.Errorf(codes.Internal, "save cart item: %v", err)
	}

	cart, err := s.buildCart(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &cartv1.AddItemResponse{
		Cart:           cart,
		ClampedToStock: clamped,
	}, nil
}

func (s *Server) UpdateItemQty(ctx context.Context, req *cartv1.UpdateItemQtyRequest) (*cartv1.UpdateItemQtyResponse, error) {
	userID, err := resolveUserID(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	skuID := strings.TrimSpace(req.GetSkuId())
	if skuID == "" {
		return nil, status.Error(codes.InvalidArgument, "sku_id is required")
	}
	if req.GetQuantity() < 0 {
		return nil, status.Error(codes.InvalidArgument, "quantity must be >= 0")
	}

	if req.GetQuantity() == 0 {
		if err := s.store.Delete(ctx, userID, skuID); err != nil {
			return nil, status.Errorf(codes.Internal, "remove cart item: %v", err)
		}
		cart, err := s.buildCart(ctx, userID)
		if err != nil {
			return nil, err
		}
		return &cartv1.UpdateItemQtyResponse{Cart: cart, ClampedToStock: false}, nil
	}

	sku, err := s.fetchSingleSKU(ctx, skuID)
	if err != nil {
		return nil, err
	}
	if !sku.GetIsActive() {
		return nil, status.Errorf(codes.FailedPrecondition, "SKU_NOT_ACTIVE: sku %s is not active", skuID)
	}
	if sku.GetPriceVnd() <= 0 {
		return nil, status.Errorf(codes.FailedPrecondition, "INVALID_PRICE: sku %s has invalid price", skuID)
	}
	if sku.GetAvailableStock() <= 0 {
		return nil, status.Errorf(codes.FailedPrecondition, "OUT_OF_STOCK: sku %s has 0 available stock", skuID)
	}

	existing, existed, err := s.store.Get(ctx, userID, skuID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "read cart item: %v", err)
	}

	addedPrice := sku.GetPriceVnd()
	selected := true
	if existed {
		if existing.AddedPriceVND > 0 {
			addedPrice = existing.AddedPriceVND
		}
		selected = existing.Selected
	}

	clamped := false
	finalQty := req.GetQuantity()
	if finalQty > sku.GetAvailableStock() {
		finalQty = sku.GetAvailableStock()
		clamped = true
	}

	if err := s.store.Set(ctx, userID, skuID, store.Item{
		Quantity:      finalQty,
		AddedPriceVND: addedPrice,
		Selected:      selected,
	}); err != nil {
		return nil, status.Errorf(codes.Internal, "update cart item: %v", err)
	}

	cart, err := s.buildCart(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &cartv1.UpdateItemQtyResponse{
		Cart:           cart,
		ClampedToStock: clamped,
	}, nil
}

func (s *Server) RemoveItem(ctx context.Context, req *cartv1.RemoveItemRequest) (*cartv1.RemoveItemResponse, error) {
	userID, err := resolveUserID(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	skuID := strings.TrimSpace(req.GetSkuId())
	if skuID == "" {
		return nil, status.Error(codes.InvalidArgument, "sku_id is required")
	}

	if err := s.store.Delete(ctx, userID, skuID); err != nil {
		return nil, status.Errorf(codes.Internal, "remove cart item: %v", err)
	}

	cart, err := s.buildCart(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &cartv1.RemoveItemResponse{Cart: cart}, nil
}

func (s *Server) ClearItems(ctx context.Context, req *cartv1.ClearItemsRequest) (*cartv1.ClearItemsResponse, error) {
	userID, err := resolveUserID(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}

	rawIDs := req.GetSkuIds()
	if len(rawIDs) == 0 {
		if err := s.store.ClearAll(ctx, userID); err != nil {
			return nil, status.Errorf(codes.Internal, "clear cart: %v", err)
		}
		return &cartv1.ClearItemsResponse{Ok: true}, nil
	}

	cleanIDs := make([]string, 0, len(rawIDs))
	for _, id := range rawIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			cleanIDs = append(cleanIDs, id)
		}
	}
	if len(cleanIDs) > 0 {
		if err := s.store.Delete(ctx, userID, cleanIDs...); err != nil {
			return nil, status.Errorf(codes.Internal, "clear cart items: %v", err)
		}
	}
	return &cartv1.ClearItemsResponse{Ok: true}, nil
}

// --- Internal helpers ---

func (s *Server) fetchSKUsBatch(ctx context.Context, skuIDs []string) (map[string]*productv1.SKU, error) {
	resp, err := resilience.CallWithRetry(
		ctx,
		s.productBreaker,
		3,
		50*time.Millisecond,
		3*time.Second,
		func(attemptCtx context.Context) (*productv1.GetSKUsBatchResponse, error) {
			return s.productClient.GetSKUsBatch(attemptCtx, &productv1.GetSKUsBatchRequest{SkuIds: skuIDs})
		},
	)
	if err != nil {
		if errors.Is(err, resilience.ErrCircuitOpen) {
			return nil, status.Error(codes.Unavailable, "product service temporarily unavailable")
		}
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		return nil, status.Errorf(codes.Unavailable, "product service error: %v", err)
	}

	byID := make(map[string]*productv1.SKU, len(resp.GetSkus()))
	for _, sk := range resp.GetSkus() {
		if sk == nil || sk.GetId() == "" {
			continue
		}
		byID[sk.GetId()] = sk
	}
	return byID, nil
}

func (s *Server) fetchSingleSKU(ctx context.Context, skuID string) (*productv1.SKU, error) {
	byID, err := s.fetchSKUsBatch(ctx, []string{skuID})
	if err != nil {
		return nil, err
	}
	sku, ok := byID[skuID]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "sku %s not found", skuID)
	}
	return sku, nil
}

func (s *Server) fetchShopName(ctx context.Context, shopID string) string {
	if s.userClient == nil || shopID == "" {
		return ""
	}
	resp, err := resilience.CallWithRetry(
		ctx,
		s.userBreaker,
		2,
		30*time.Millisecond,
		1*time.Second,
		func(attemptCtx context.Context) (*userv1.GetShopResponse, error) {
			return s.userClient.GetShop(attemptCtx, &userv1.GetShopRequest{ShopId: shopID})
		},
	)
	if err != nil {
		s.logger.DebugContext(ctx, "best-effort shop name lookup failed",
			slog.String("shop_id", shopID),
			slog.String("error", err.Error()),
		)
		return ""
	}
	return resp.GetShop().GetName()
}

func (s *Server) buildCart(ctx context.Context, userID string) (*cartv1.Cart, error) {
	storedItems, err := s.store.GetAll(ctx, userID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "load cart from redis: %v", err)
	}
	if len(storedItems) == 0 {
		return &cartv1.Cart{
			UserId:     userID,
			ShopGroups: []*cartv1.CartShopGroup{},
			TotalItems: 0,
			TotalVnd:   0,
		}, nil
	}

	skuIDs := make([]string, 0, len(storedItems))
	for skuID, stItem := range storedItems {
		if stItem.Quantity <= 0 {
			continue
		}
		skuIDs = append(skuIDs, skuID)
	}
	if len(skuIDs) == 0 {
		return &cartv1.Cart{
			UserId:     userID,
			ShopGroups: []*cartv1.CartShopGroup{},
			TotalItems: 0,
			TotalVnd:   0,
		}, nil
	}
	sort.Strings(skuIDs)

	skuMap, err := s.fetchSKUsBatch(ctx, skuIDs)
	if err != nil {
		return nil, err
	}

	groupsByShop := make(map[string]*cartv1.CartShopGroup)
	for _, skuID := range skuIDs {
		stItem := storedItems[skuID]
		sku, ok := skuMap[skuID]

		var (
			shopID string
			item   *cartv1.CartItem
		)
		switch {
		case !ok:
			item = &cartv1.CartItem{
				SkuId:             skuID,
				PriceVnd:          0,
				AddedPriceVnd:     stItem.AddedPriceVND,
				PriceChanged:      false,
				Quantity:          stItem.Quantity,
				AvailableStock:    0,
				WeightGrams:       0,
				Selected:          stItem.Selected,
				Unavailable:       true,
				UnavailableReason: unavailableReasonSKUNotFound,
			}
		case !sku.GetIsActive() || sku.GetPriceVnd() <= 0:
			shopID = sku.GetShopId()
			item = &cartv1.CartItem{
				SkuId:             skuID,
				ProductId:         sku.GetProductId(),
				ShopId:            shopID,
				ProductName:       sku.GetProductName(),
				SkuName:           sku.GetName(),
				ImageUrl:          sku.GetImageUrl(),
				PriceVnd:          0,
				AddedPriceVnd:     stItem.AddedPriceVND,
				PriceChanged:      false,
				Quantity:          stItem.Quantity,
				AvailableStock:    0,
				WeightGrams:       0,
				Selected:          stItem.Selected,
				Unavailable:       true,
				UnavailableReason: unavailableReasonSKUInactive,
			}
		default:
			shopID = sku.GetShopId()
			livePrice := sku.GetPriceVnd()
			priceChanged := stItem.AddedPriceVND > 0 && livePrice != stItem.AddedPriceVND
			item = &cartv1.CartItem{
				SkuId:          skuID,
				ProductId:      sku.GetProductId(),
				ShopId:         shopID,
				ProductName:    sku.GetProductName(),
				SkuName:        sku.GetName(),
				ImageUrl:       sku.GetImageUrl(),
				PriceVnd:       livePrice,
				AddedPriceVnd:  stItem.AddedPriceVND,
				PriceChanged:   priceChanged,
				Quantity:       stItem.Quantity,
				AvailableStock: sku.GetAvailableStock(),
				WeightGrams:    sku.GetWeightGrams(),
				Selected:       stItem.Selected,
			}
		}

		grp, exists := groupsByShop[shopID]
		if !exists {
			grp = &cartv1.CartShopGroup{
				ShopId:   shopID,
				ShopName: s.fetchShopName(ctx, shopID),
				Items:    []*cartv1.CartItem{},
			}
			groupsByShop[shopID] = grp
		}
		grp.Items = append(grp.Items, item)
		if !item.Unavailable {
			grp.SubtotalVnd += item.PriceVnd * int64(item.Quantity)
			grp.TotalWeightGrams += item.WeightGrams * item.Quantity
		}
	}

	shopIDs := make([]string, 0, len(groupsByShop))
	for shopID := range groupsByShop {
		shopIDs = append(shopIDs, shopID)
	}
	sort.Slice(shopIDs, func(i, j int) bool {
		if shopIDs[i] == "" {
			return false
		}
		if shopIDs[j] == "" {
			return true
		}
		return shopIDs[i] < shopIDs[j]
	})

	shopGroups := make([]*cartv1.CartShopGroup, 0, len(shopIDs))
	var totalItems int32
	var totalVND int64
	for _, shopID := range shopIDs {
		grp := groupsByShop[shopID]
		sort.Slice(grp.Items, func(i, j int) bool {
			return grp.Items[i].SkuId < grp.Items[j].SkuId
		})
		for _, it := range grp.Items {
			if !it.Unavailable {
				totalItems += it.Quantity
			}
		}
		totalVND += grp.SubtotalVnd
		shopGroups = append(shopGroups, grp)
	}

	return &cartv1.Cart{
		UserId:     userID,
		ShopGroups: shopGroups,
		TotalItems: totalItems,
		TotalVnd:   totalVND,
	}, nil
}

// resolveUserID enforces authenticated user_id ownership across all CartService RPCs.
// Missing claims return Unauthenticated and service tokens return PermissionDenied.
// Non-admin callers may only access their own cart (mismatched reqUserID returns
// PermissionDenied); admin/super_admin end-user callers may target reqUserID or their own sub.
func resolveUserID(ctx context.Context, reqUserID string) (string, error) {
	reqUserID = strings.TrimSpace(reqUserID)
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return "", status.Error(codes.Unauthenticated, "authentication required")
	}
	if claims.Service != "" {
		return "", status.Error(codes.PermissionDenied, "service identity not allowed for cart operations")
	}

	if auth.HasRole(claims, "admin", "super_admin") {
		if reqUserID != "" {
			return reqUserID, nil
		}
		if claims.UserID() != "" {
			return claims.UserID(), nil
		}
		return "", status.Error(codes.InvalidArgument, "user_id is required")
	}

	claimUID := strings.TrimSpace(claims.UserID())
	if claimUID == "" {
		return "", status.Error(codes.Unauthenticated, "invalid authentication claims")
	}
	if reqUserID != "" && reqUserID != claimUID {
		return "", status.Error(codes.PermissionDenied, "cannot access another user's cart")
	}
	return claimUID, nil
}
