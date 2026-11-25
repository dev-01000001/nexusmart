package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/product/internal/events"
	"github.com/nexusmart/core/services/product/internal/store"
)

// TestSKUStockConstraint_RespectsLegacyFlashSaleReservedStock verifies that all SKU stock write paths
// (ReserveStock, UpdateStock, UpdateProduct active edit & SKU reactivation) respect
// reserved_stock + flash_sale_reserved_stock so PostgreSQL CHECK skus_total_reserved_le_stock is never violated.
func TestSKUStockConstraint_RespectsLegacyFlashSaleReservedStock(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)

	adminCtx := ctxWithRole("admin-1", "admin", "")
	buyerCtx := ctxWithRole("buyer-1", "buyer", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")

	_, prod := setupApprovedProduct(t, srv, "shop-1", []*productv1.SKUInput{
		{SkuCode: "GUARD-1", Name: "Active SKU", PriceVnd: 200000, Stock: 20},
		{SkuCode: "GUARD-2", Name: "Reactivate SKU", PriceVnd: 250000, Stock: 10},
	})
	sku1ID := prod.Skus[0].Id
	sku2ID := prod.Skus[1].Id

	// Simulate legacy flash_sale_reserved_stock=6 on sku1 alongside reserved_stock=4 (total reserved = 10).
	st.SetSKUFlashSaleReservedStockForTest(sku1ID, 6)
	if _, err := srv.ReserveStock(buyerCtx, &productv1.ReserveStockRequest{
		OrderId: "ord-guard-1",
		Items:   []*productv1.StockReservationItem{{SkuId: sku1ID, Quantity: 4}},
	}); err != nil {
		t.Fatalf("ReserveStock(4): %v", err)
	}

	// GetSKUsBatch exposes flash_sale_reserved_stock=6 and available_stock=20-4-6=10.
	batchResp, err := srv.GetSKUsBatch(context.Background(), &productv1.GetSKUsBatchRequest{SkuIds: []string{sku1ID}})
	if err != nil || len(batchResp.GetSkus()) != 1 {
		t.Fatalf("GetSKUsBatch: resp=%+v err=%v", batchResp, err)
	}
	sk := batchResp.GetSkus()[0]
	if sk.GetStock() != 20 || sk.GetReservedStock() != 4 || sk.GetFlashSaleReservedStock() != 6 || sk.GetAvailableStock() != 10 {
		t.Fatalf("unexpected sku state: stock=%d reserved=%d flash=%d avail=%d",
			sk.GetStock(), sk.GetReservedStock(), sk.GetFlashSaleReservedStock(), sk.GetAvailableStock())
	}

	// ReserveStock cannot consume past available_stock (10).
	if _, err := srv.ReserveStock(buyerCtx, &productv1.ReserveStockRequest{
		OrderId: "ord-guard-over",
		Items:   []*productv1.StockReservationItem{{SkuId: sku1ID, Quantity: 11}},
	}); status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "INSUFFICIENT_STOCK") {
		t.Fatalf("ReserveStock over available: want FailedPrecondition INSUFFICIENT_STOCK, got %v", err)
	}

	// UpdateStock cannot lower stock below reserved + flash_sale_reserved (10).
	if _, err := srv.UpdateStock(sellerCtx, &productv1.UpdateStockRequest{
		SkuId:           sku1ID,
		ShopId:          "shop-1",
		NewStock:        9,
		ExpectedVersion: sk.GetVersion(),
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("UpdateStock below total reserved: want FailedPrecondition, got %v", err)
	}
	updResp, err := srv.UpdateStock(sellerCtx, &productv1.UpdateStockRequest{
		SkuId:           sku1ID,
		ShopId:          "shop-1",
		NewStock:        10,
		ExpectedVersion: sk.GetVersion(),
	})
	if err != nil || updResp.GetSku().GetStock() != 10 || updResp.GetSku().GetAvailableStock() != 0 {
		t.Fatalf("UpdateStock at exact total reserved (10): resp=%+v err=%v", updResp, err)
	}

	// UpdateProduct cannot lower active SKU stock below reserved + flash_sale_reserved (10).
	if _, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id: prod.Id,
		Skus: []*productv1.SKUInput{
			{SkuCode: "GUARD-1", Name: "Active SKU", PriceVnd: 200000, Stock: 9},
			{SkuCode: "GUARD-2", Name: "Reactivate SKU", PriceVnd: 250000, Stock: 10},
		},
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("UpdateProduct active SKU below total reserved: want FailedPrecondition, got %v", err)
	}

	// Deactivate sku2 (reserved_stock=0), then simulate legacy flash_sale_reserved_stock=5 on inactive sku2.
	// Reactivating sku2 with stock=4 (< 5) MUST fail cleanly instead of violating skus_total_reserved_le_stock.
	if _, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id: prod.Id,
		Skus: []*productv1.SKUInput{
			{SkuCode: "GUARD-1", Name: "Active SKU", PriceVnd: 200000, Stock: 12},
		},
	}); err != nil {
		t.Fatalf("UpdateProduct deactivate sku2: %v", err)
	}
	st.SetSKUFlashSaleReservedStockForTest(sku2ID, 5)
	if _, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id: prod.Id,
		Skus: []*productv1.SKUInput{
			{SkuCode: "GUARD-1", Name: "Active SKU", PriceVnd: 200000, Stock: 12},
			{SkuCode: "GUARD-2", Name: "Reactivate SKU", PriceVnd: 250000, Stock: 4},
		},
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("UpdateProduct reactivate SKU below flash_sale_reserved_stock: want FailedPrecondition, got %v", err)
	}
	if _, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id: prod.Id,
		Skus: []*productv1.SKUInput{
			{SkuCode: "GUARD-1", Name: "Active SKU", PriceVnd: 200000, Stock: 12},
			{SkuCode: "GUARD-2", Name: "Reactivate SKU", PriceVnd: 250000, Stock: 5},
		},
	}); err != nil {
		t.Fatalf("UpdateProduct reactivate SKU at exact flash_sale_reserved_stock (5): %v", err)
	}

	// Releasing order reservation frees reserved_stock=4 while keeping flash_sale_reserved_stock=6 intact.
	if _, err := srv.ReleaseStock(adminCtx, &productv1.ReleaseStockRequest{OrderId: "ord-guard-1"}); err != nil {
		t.Fatalf("ReleaseStock(ord-guard-1): %v", err)
	}
	afterRel, err := srv.GetSKUsBatch(context.Background(), &productv1.GetSKUsBatchRequest{SkuIds: []string{sku1ID}})
	if err != nil || len(afterRel.GetSkus()) != 1 {
		t.Fatalf("GetSKUsBatch after release: resp=%+v err=%v", afterRel, err)
	}
	skAfter := afterRel.GetSkus()[0]
	if skAfter.GetStock() != 12 || skAfter.GetReservedStock() != 0 || skAfter.GetFlashSaleReservedStock() != 6 || skAfter.GetAvailableStock() != 6 {
		t.Fatalf("post-release sku state mismatch: stock=%d reserved=%d flash=%d avail=%d",
			skAfter.GetStock(), skAfter.GetReservedStock(), skAfter.GetFlashSaleReservedStock(), skAfter.GetAvailableStock())
	}
}

// TestReviewCreatedOutbox_IncludesCommentAndOmitsPrivateIDs verifies the unified review.created event contract:
// carries comment for search/assistant consumers and strictly omits buyer_id and order_id.
func TestReviewCreatedOutbox_IncludesCommentAndOmitsPrivateIDs(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)
	now := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	srv.SetClock(func() time.Time { return now })

	_, prod := setupApprovedProduct(t, srv, "shop-rev", []*productv1.SKUInput{
		{SkuCode: "REV-1", Name: "Size M", PriceVnd: 150000, Stock: 10},
	})
	skuID := prod.Skus[0].Id

	srv.SetOrderClient(&fakeOrderClient{
		fn: func(_ context.Context, _ *orderv1.GetOrderRequest) (*orderv1.GetOrderResponse, error) {
			return &orderv1.GetOrderResponse{
				Order: &orderv1.Order{
					Id:      "ord-secret-1",
					BuyerId: "buyer-secret-1",
					ShopId:  "shop-rev",
					Status:  orderv1.OrderStatus_ORDER_STATUS_COMPLETED,
					Items: []*orderv1.OrderItem{
						{Id: "oi-rev-1", ProductId: prod.Id, SkuId: skuID, SkuName: "Size M", Quantity: 1},
					},
				},
			}, nil
		},
	})
	srv.SetUserClient(&fakeUserClient{
		fn: func(_ context.Context, _ *userv1.GetProfileRequest) (*userv1.GetProfileResponse, error) {
			return &userv1.GetProfileResponse{User: &userv1.User{Id: "buyer-secret-1", FullName: "Minh Anh"}}, nil
		},
	})

	buyerCtx := metadata.NewIncomingContext(
		ctxWithRole("buyer-secret-1", auth.RoleBuyer, ""),
		metadata.Pairs("authorization", "Bearer jwt-buyer"),
	)
	const wantComment = "Vải mát, đường may đẹp"
	revResp, err := srv.CreateReview(buyerCtx, &productv1.CreateReviewRequest{
		OrderId:     "ord-secret-1",
		OrderItemId: "oi-rev-1",
		Rating:      5,
		Comment:     wantComment,
	})
	if err != nil {
		t.Fatalf("CreateReview: %v", err)
	}

	var matched []store.OutboxEvent
	for _, ev := range st.OutboxEvents() {
		if ev.EventType == events.SubjectReviewCreated {
			matched = append(matched, ev)
		}
	}
	if len(matched) != 1 {
		t.Fatalf("expected 1 review.created event, got %d", len(matched))
	}

	var raw map[string]any
	if err := json.Unmarshal(matched[0].PayloadJSON, &raw); err != nil {
		t.Fatalf("unmarshal raw review.created JSON: %v", err)
	}
	if _, ok := raw["buyer_id"]; ok {
		t.Fatalf("review.created payload must omit buyer_id, got: %s", string(matched[0].PayloadJSON))
	}
	if _, ok := raw["order_id"]; ok {
		t.Fatalf("review.created payload must omit order_id, got: %s", string(matched[0].PayloadJSON))
	}

	var payload events.ReviewCreatedPayload
	if err := json.Unmarshal(matched[0].PayloadJSON, &payload); err != nil {
		t.Fatalf("unmarshal ReviewCreatedPayload: %v", err)
	}
	if payload.ReviewID != revResp.GetReview().GetId() ||
		payload.ProductID != prod.Id ||
		payload.ShopID != "shop-rev" ||
		payload.Rating != 5 ||
		payload.Comment != wantComment ||
		payload.ReviewCount != 1 ||
		payload.RatingAvgX100 != 500 ||
		payload.OccurredAt == "" {
		t.Fatalf("unexpected ReviewCreatedPayload: %+v", payload)
	}
}
