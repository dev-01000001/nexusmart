package server

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/product/internal/store"
)

var validStockChangeTypes = map[string]bool{
	"MANUAL_UPDATE": true,
	"RESERVE":       true,
	"RELEASE":       true,
	"CONFIRM":       true,
	"RESTOCK":       true,
}

// ListStockHistory returns paginated SKU stock ledger entries ordered by (created_at DESC, id DESC) (S03, G8).
// Sellers are strictly scoped to claims.ShopID; querying another shop's shop_id, product_id, or sku_id returns PermissionDenied.
// Staff callers require products.read capability.
func (s *Server) ListStockHistory(ctx context.Context, req *productv1.ListStockHistoryRequest) (*productv1.ListStockHistoryResponse, error) {
	claims, err := requireEndUser(ctx)
	if err != nil {
		return nil, err
	}

	reqShopID := strings.TrimSpace(req.GetShopId())
	productID := strings.TrimSpace(req.GetProductId())
	skuID := strings.TrimSpace(req.GetSkuId())

	var (
		shopID   string
		isSeller bool
	)
	switch {
	case auth.HasCapability(claims, auth.CapProductsRead):
		shopID = reqShopID
	case auth.HasRole(claims, auth.RoleSeller):
		claimShopID := strings.TrimSpace(claims.ShopID)
		if claimShopID == "" {
			return nil, status.Error(codes.PermissionDenied, "seller token missing shop_id")
		}
		if reqShopID != "" && reqShopID != claimShopID {
			return nil, status.Error(codes.PermissionDenied, "cannot view stock history of another shop")
		}
		shopID = claimShopID
		isSeller = true
	default:
		return nil, status.Error(codes.PermissionDenied, "seller role or products.read capability required")
	}

	changeType := strings.ToUpper(strings.TrimSpace(req.GetChangeType()))
	if changeType != "" && !validStockChangeTypes[changeType] {
		return nil, status.Error(codes.InvalidArgument, "change_type must be MANUAL_UPDATE, RESERVE, RELEASE, CONFIRM, or RESTOCK")
	}

	var fromTime, toTime *time.Time
	if req.GetFromTime() != nil {
		if err := req.GetFromTime().CheckValid(); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid from_time: %v", err)
		}
		ts := req.GetFromTime().AsTime().UTC()
		fromTime = &ts
	}
	if req.GetToTime() != nil {
		if err := req.GetToTime().CheckValid(); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid to_time: %v", err)
		}
		ts := req.GetToTime().AsTime().UTC()
		toTime = &ts
	}
	if fromTime != nil && toTime != nil && fromTime.After(*toTime) {
		return nil, status.Error(codes.InvalidArgument, "from_time must not be after to_time")
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
	offset64 := int64(page-1) * int64(pageSize)
	if offset64 < 0 || offset64 > math.MaxInt32 {
		return nil, status.Error(codes.InvalidArgument, "pagination offset out of range")
	}

	ctx = store.WithAsOf(ctx, s.now())
	if isSeller {
		if skuID != "" {
			sk, getErr := s.store.GetSKUWithContext(ctx, skuID)
			if getErr != nil && !errors.Is(getErr, store.ErrNotFound) {
				return nil, status.Errorf(codes.Internal, "lookup sku ownership: %v", getErr)
			}
			if getErr == nil && sk.ShopID != shopID {
				return nil, status.Error(codes.PermissionDenied, "cannot view stock history of another shop's SKU")
			}
		}
		if productID != "" {
			ownerShopID, getErr := s.store.GetProductShopIDAny(ctx, productID)
			if getErr != nil && !errors.Is(getErr, store.ErrNotFound) {
				return nil, status.Errorf(codes.Internal, "lookup product ownership: %v", getErr)
			}
			if getErr == nil && ownerShopID != shopID {
				return nil, status.Error(codes.PermissionDenied, "cannot view stock history of another shop's product")
			}
		}
	}

	rows, total, err := s.store.ListStockHistory(ctx, store.ListStockHistoryFilter{
		ShopID:     shopID,
		ProductID:  productID,
		SKUID:      skuID,
		ChangeType: changeType,
		FromTime:   fromTime,
		ToTime:     toTime,
		Limit:      pageSize,
		Offset:     int32(offset64),
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list stock history: %v", err)
	}

	out := make([]*productv1.StockHistoryItem, 0, len(rows))
	for _, r := range rows {
		out = append(out, toProtoStockHistoryItem(r))
	}
	return &productv1.ListStockHistoryResponse{
		Items: out,
		Total: total,
	}, nil
}

func toProtoStockHistoryItem(r store.StockHistoryRecord) *productv1.StockHistoryItem {
	return &productv1.StockHistoryItem{
		Id:             r.ID,
		SkuId:          r.SKUID,
		ProductId:      r.ProductID,
		ShopId:         r.ShopID,
		ProductName:    r.ProductName,
		SkuCode:        r.SKUCode,
		SkuName:        r.SKUName,
		OrderId:        r.OrderID,
		ChangeType:     r.ChangeType,
		Delta:          r.Delta,
		StockBefore:    r.StockBefore,
		StockAfter:     r.StockAfter,
		ReservedBefore: r.ReservedBefore,
		ReservedAfter:  r.ReservedAfter,
		ActorId:        r.ActorID,
		Reason:         r.Reason,
		CreatedAt:      timestamppb.New(r.CreatedAt),
	}
}
