package server

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/product/internal/events"
	"github.com/nexusmart/core/services/product/internal/store"
)

func TestW3B3_B1_ListProductsForIndex_PaginationFieldsAndRBAC(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)
	baseTime := time.Date(2026, 11, 25, 13, 0, 0, 0, time.UTC)
	srv.SetClock(func() time.Time { return baseTime })

	adminCtx := ctxWithRole("admin-1", auth.RoleAdmin, "")
	superCtx := ctxWithRole("super-1", auth.RoleSuperAdmin, "")
	opsCtx := ctxWithRole("ops-1", auth.RoleOps, "")
	modCtx := ctxWithRole("mod-1", auth.RoleModerator, "")
	cskhCtx := ctxWithRole("cskh-1", auth.RoleCSKH, "")
	acctCtx := ctxWithRole("acct-1", auth.RoleAccountant, "")
	buyerCtx := ctxWithRole("buyer-1", auth.RoleBuyer, "")
	sellerCtx := ctxWithRole("seller-1", auth.RoleSeller, "shop-idx")
	svcCtx := auth.WithClaims(context.Background(), &auth.Claims{
		Role:    auth.ServiceRole,
		Service: "search-service",
	})

	// 3-level category tree: Root -> Mid -> Leaf.
	catRoot, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name: "Thời trang", Slug: "thoi-trang", CommissionRateBps: 750,
	})
	if err != nil {
		t.Fatalf("CreateCategory root: %v", err)
	}
	catMid, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name: "Áo nam", Slug: "ao-nam", ParentId: catRoot.GetCategory().GetId(), CommissionRateBps: 650,
	})
	if err != nil {
		t.Fatalf("CreateCategory mid: %v", err)
	}
	catLeaf, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name: "Áo thun", Slug: "ao-thun", ParentId: catMid.GetCategory().GetId(), CommissionRateBps: 550,
	})
	if err != nil {
		t.Fatalf("CreateCategory leaf: %v", err)
	}
	leafID := catLeaf.GetCategory().GetId()

	// Add REVIEW keyword so flagged_keywords is populated on an approved product, proving it is NEVER leaked.
	if _, err := srv.CreateBannedKeyword(adminCtx, &productv1.CreateBannedKeywordRequest{
		Term: "xách tay", Action: productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_REVIEW,
	}); err != nil {
		t.Fatalf("CreateBannedKeyword: %v", err)
	}

	// Create 5 products:
	// p1: APPROVED (high price > 2^31 VND, 2 SKUs with colors/sizes, 1st SKU stock=0 so default_sku_id = 2nd SKU)
	// p2: OUT_OF_STOCK (approved with stock=0)
	// p3: PENDING (excluded)
	// p4: HIDDEN (excluded)
	// p5: REJECTED (excluded)
	// p6: Soft-deleted (excluded)
	p1Resp, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId:  leafID,
		Name:        "Áo Thun Compact xách tay",
		Description: "Chất cotton mềm mịn",
		Images:      []string{"/products/p1-thumb.jpg", "/products/p1-2.jpg"},
		Skus: []*productv1.SKUInput{
			{SkuCode: "IDX-P1-S", Name: "Đen / S", Attributes: map[string]string{"color": "Đen", "size": "S"}, PriceVnd: 2500000000, Stock: 0, WeightGrams: 200},
			{SkuCode: "IDX-P1-L", Name: "Trắng / L", Attributes: map[string]string{"Màu sắc": "Trắng", "Kích cỡ": "L"}, PriceVnd: 3000000000, Stock: 15, WeightGrams: 220},
		},
	})
	if err != nil {
		t.Fatalf("CreateProduct p1: %v", err)
	}
	p1ID := p1Resp.GetProduct().GetId()
	if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: p1ID, Decision: productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	}); err != nil {
		t.Fatalf("Approve p1: %v", err)
	}

	// Add a real review to p1 to verify rating_avg_x100 & review_count in IndexableProduct.
	srv.SetOrderClient(&fakeOrderClient{
		fn: func(_ context.Context, _ *orderv1.GetOrderRequest) (*orderv1.GetOrderResponse, error) {
			return &orderv1.GetOrderResponse{Order: &orderv1.Order{
				Id: "ord-idx-1", BuyerId: "buyer-1", ShopId: "shop-idx",
				Status: orderv1.OrderStatus_ORDER_STATUS_COMPLETED,
				Items:  []*orderv1.OrderItem{{Id: "oi-idx-1", OrderId: "ord-idx-1", ProductId: p1ID, SkuId: p1Resp.GetProduct().GetSkus()[1].GetId(), SkuName: "Trắng / L"}},
			}}, nil
		},
	})
	if _, err := srv.CreateReview(ctxWithRoleAndToken("buyer-1", auth.RoleBuyer, "", "Bearer tok-b1"), &productv1.CreateReviewRequest{
		OrderId: "ord-idx-1", OrderItemId: "oi-idx-1", Rating: 4, Comment: "Áo đẹp",
	}); err != nil {
		t.Fatalf("CreateReview p1: %v", err)
	}

	p2Resp, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: leafID, Name: "Áo Hết Hàng",
		Skus: []*productv1.SKUInput{{SkuCode: "IDX-P2", PriceVnd: 180000, Stock: 0, ImageUrl: "/products/p2-sku.jpg"}},
	})
	if err != nil {
		t.Fatalf("CreateProduct p2: %v", err)
	}
	p2ID := p2Resp.GetProduct().GetId()
	if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: p2ID, Decision: productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	}); err != nil {
		t.Fatalf("Approve p2: %v", err)
	}

	// p3 PENDING
	if _, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: leafID, Name: "SP Pending",
		Skus: []*productv1.SKUInput{{SkuCode: "IDX-P3", PriceVnd: 100000, Stock: 5}},
	}); err != nil {
		t.Fatalf("CreateProduct p3: %v", err)
	}
	// p4 HIDDEN
	p4Resp, _ := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: leafID, Name: "SP Hidden",
		Skus: []*productv1.SKUInput{{SkuCode: "IDX-P4", PriceVnd: 100000, Stock: 5}},
	})
	_, _ = srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{ProductId: p4Resp.GetProduct().GetId(), Decision: productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE})
	_, _ = srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{Id: p4Resp.GetProduct().GetId(), Status: productv1.ProductStatus_PRODUCT_STATUS_HIDDEN})
	// p5 REJECTED
	p5Resp, _ := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: leafID, Name: "SP Rejected",
		Skus: []*productv1.SKUInput{{SkuCode: "IDX-P5", PriceVnd: 100000, Stock: 5}},
	})
	_, _ = srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{ProductId: p5Resp.GetProduct().GetId(), Decision: productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_REJECT, Reason: "bad"})
	// p6 Soft-deleted
	p6Resp, _ := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: leafID, Name: "SP Deleted",
		Skus: []*productv1.SKUInput{{SkuCode: "IDX-P6", PriceVnd: 100000, Stock: 5}},
	})
	_, _ = srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{ProductId: p6Resp.GetProduct().GetId(), Decision: productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE})
	_, _ = srv.DeleteProduct(sellerCtx, &productv1.DeleteProductRequest{Id: p6Resp.GetProduct().GetId()})

	// RBAC attack checks:
	if _, err := srv.ListProductsForIndex(context.Background(), &productv1.ListProductsForIndexRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anon ListProductsForIndex: want Unauthenticated, got %v", err)
	}
	for name, c := range map[string]context.Context{
		"buyer":      buyerCtx,
		"seller":     sellerCtx,
		"accountant": acctCtx,
	} {
		if _, err := srv.ListProductsForIndex(c, &productv1.ListProductsForIndexRequest{}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("%s ListProductsForIndex: want PermissionDenied, got %v", name, err)
		}
	}
	for name, c := range map[string]context.Context{
		"service":     svcCtx,
		"super_admin": superCtx,
		"admin":       adminCtx,
		"ops":         opsCtx,
		"moderator":   modCtx,
		"cskh":        cskhCtx,
	} {
		if _, err := srv.ListProductsForIndex(c, &productv1.ListProductsForIndexRequest{PageSize: 10}); err != nil {
			t.Fatalf("%s ListProductsForIndex: unexpected err %v", name, err)
		}
	}

	// Keyset pagination (page_size=1) returns ONLY p1 and p2 in strict id ASC order.
	var collected []*productv1.IndexableProduct
	cursor := ""
	for {
		resp, err := srv.ListProductsForIndex(svcCtx, &productv1.ListProductsForIndexRequest{
			AfterId: cursor, PageSize: 1,
		})
		if err != nil {
			t.Fatalf("ListProductsForIndex cursor=%q: %v", cursor, err)
		}
		collected = append(collected, resp.GetItems()...)
		if !resp.GetHasMore() {
			if resp.GetNextCursor() != "" {
				t.Fatalf("expected empty next_cursor when has_more=false, got %q", resp.GetNextCursor())
			}
			break
		}
		if resp.GetNextCursor() == "" || resp.GetNextCursor() <= cursor {
			t.Fatalf("non-advancing cursor: prev=%q next=%q", cursor, resp.GetNextCursor())
		}
		cursor = resp.GetNextCursor()
	}
	if len(collected) != 2 {
		t.Fatalf("expected exactly 2 indexable products (APPROVED + OUT_OF_STOCK), got %d", len(collected))
	}
	if collected[0].GetId() >= collected[1].GetId() {
		t.Fatalf("expected strict id ASC order, got %q then %q", collected[0].GetId(), collected[1].GetId())
	}

	byID := map[string]*productv1.IndexableProduct{}
	for _, it := range collected {
		byID[it.GetId()] = it
	}
	idx1 := byID[p1ID]
	if idx1 == nil {
		t.Fatalf("missing p1 in ListProductsForIndex")
	}
	if idx1.GetMinPriceVnd() != 2500000000 || idx1.GetMaxPriceVnd() != 3000000000 {
		t.Fatalf("int64 price mismatch: min=%d max=%d", idx1.GetMinPriceVnd(), idx1.GetMaxPriceVnd())
	}
	if idx1.GetRatingAvgX100() != 400 || idx1.GetReviewCount() != 1 {
		t.Fatalf("rating snapshot mismatch: avg=%d count=%d", idx1.GetRatingAvgX100(), idx1.GetReviewCount())
	}
	if idx1.GetDefaultSkuId() != p1Resp.GetProduct().GetSkus()[1].GetId() {
		t.Fatalf("expected in-stock SKU as default_sku_id, got %q", idx1.GetDefaultSkuId())
	}
	if idx1.GetThumbnailUrl() != "/products/p1-thumb.jpg" || idx1.GetTotalStock() != 15 || idx1.GetSkuCount() != 2 {
		t.Fatalf("unexpected p1 summary fields: %+v", idx1)
	}
	if len(idx1.GetCategoryAncestorIds()) != 3 ||
		idx1.GetCategoryAncestorIds()[0] != leafID ||
		idx1.GetCategoryAncestorIds()[1] != catMid.GetCategory().GetId() ||
		idx1.GetCategoryAncestorIds()[2] != catRoot.GetCategory().GetId() {
		t.Fatalf("unexpected category_ancestor_ids: %v", idx1.GetCategoryAncestorIds())
	}
	if len(idx1.GetColors()) != 2 || len(idx1.GetSizes()) != 2 {
		t.Fatalf("expected 2 colors and 2 sizes, got colors=%v sizes=%v", idx1.GetColors(), idx1.GetSizes())
	}
	if idx1.GetShopSuspended() {
		t.Fatalf("expected shop_suspended=false")
	}

	idx2 := byID[p2ID]
	if idx2 == nil || idx2.GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_OUT_OF_STOCK ||
		idx2.GetThumbnailUrl() != "/products/p2-sku.jpg" || idx2.GetTotalStock() != 0 {
		t.Fatalf("unexpected p2 indexable product: %+v", idx2)
	}

	// Verify shop_suspended is mirrored accurately when shop is suspended.
	if err := st.SetProductsShopSuspendedByShop(context.Background(), "shop-idx", true); err != nil {
		t.Fatalf("SetProductsShopSuspendedByShop: %v", err)
	}
	afterSusp, err := srv.ListProductsForIndex(svcCtx, &productv1.ListProductsForIndexRequest{PageSize: 10})
	if err != nil || len(afterSusp.GetItems()) != 2 {
		t.Fatalf("ListProductsForIndex after suspend: %+v err=%v", afterSusp, err)
	}
	for _, it := range afterSusp.GetItems() {
		if !it.GetShopSuspended() {
			t.Fatalf("expected shop_suspended=true on %s", it.GetId())
		}
		raw, _ := json.Marshal(it)
		if strings.Contains(string(raw), "commission_rate_bps") || strings.Contains(string(raw), "flagged_keywords") {
			t.Fatalf("forbidden field leaked in IndexableProduct: %s", string(raw))
		}
	}
}

func TestW3B3_B1_SearchOutboxSync_AllTransitions(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)
	now := time.Date(2026, 11, 25, 14, 0, 0, 0, time.UTC)
	srv.SetClock(func() time.Time { return now })

	adminCtx := ctxWithRole("admin-1", auth.RoleAdmin, "")
	sellerCtx := ctxWithRole("seller-1", auth.RoleSeller, "shop-sync")
	buyerCtx := ctxWithRoleAndToken("buyer-1", auth.RoleBuyer, "", "Bearer tok-sync")

	// 1. Category CRUD emits product.category_updated with aggregate_id="cat:"+id.
	catA, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name: "Cat Sync A", Slug: "cat-sync-a", CommissionRateBps: 500, SortOrder: 1,
	})
	if err != nil {
		t.Fatalf("CreateCategory A: %v", err)
	}
	catAID := catA.GetCategory().GetId()
	if _, err := srv.UpdateCategory(adminCtx, &productv1.UpdateCategoryRequest{
		Id: catAID, Name: "Cat Sync A Updated", SortOrder: 2, HasSortOrder: true,
	}); err != nil {
		t.Fatalf("UpdateCategory A: %v", err)
	}
	catDel, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name: "Cat Del", Slug: "cat-del", CommissionRateBps: 500,
	})
	if err != nil {
		t.Fatalf("CreateCategory Del: %v", err)
	}
	catDelID := catDel.GetCategory().GetId()
	if _, err := srv.DeleteCategory(adminCtx, &productv1.DeleteCategoryRequest{Id: catDelID}); err != nil {
		t.Fatalf("DeleteCategory Del: %v", err)
	}

	var catUpserts, catDeletes int
	for _, ev := range st.OutboxEvents() {
		if ev.EventType != events.SubjectProductCategoryUpdated {
			continue
		}
		if !strings.HasPrefix(ev.AggregateID, "cat:") {
			t.Fatalf("category event aggregate_id=%q must start with cat:", ev.AggregateID)
		}
		var p events.ProductCategoryUpdatedPayload
		if err := json.Unmarshal(ev.PayloadJSON, &p); err != nil {
			t.Fatalf("unmarshal category payload: %v", err)
		}
		if ev.AggregateID != "cat:"+p.CategoryID {
			t.Fatalf("aggregate_id=%q != cat:%s", ev.AggregateID, p.CategoryID)
		}
		if p.Action == "UPSERT" {
			catUpserts++
		} else if p.Action == "DELETE" && p.CategoryID == catDelID {
			catDeletes++
		}
	}
	if catUpserts != 3 || catDeletes != 1 {
		t.Fatalf("category outbox events: upserts=%d deletes=%d, want 3 and 1", catUpserts, catDeletes)
	}

	// 2. CreateProduct (PENDING) must NOT emit product.upserted (only product.created).
	beforeCreate := len(st.OutboxEvents())
	pResp, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: catAID, Name: "Áo Sync",
		Skus: []*productv1.SKUInput{{SkuCode: "SYN-1", PriceVnd: 200000, Stock: 5, Attributes: map[string]string{"color": "Đỏ", "size": "M"}}},
	})
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	for _, ev := range st.OutboxEvents()[beforeCreate:] {
		if ev.EventType == events.SubjectProductUpserted {
			t.Fatalf("CreateProduct (PENDING) must not emit product.upserted, got %+v", ev)
		}
	}
	prodID := pResp.GetProduct().GetId()
	sku := pResp.GetProduct().GetSkus()[0]

	countEventsSince := func(since int, evType string) []store.OutboxEvent {
		all := st.OutboxEvents()
		var out []store.OutboxEvent
		for _, ev := range all[since:] {
			if ev.EventType == evType {
				out = append(out, ev)
			}
		}
		return out
	}

	// 3. AdminReviewProduct(APPROVE) emits product.upserted.
	mark := len(st.OutboxEvents())
	now = now.Add(time.Minute)
	if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: prodID, Decision: productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	}); err != nil {
		t.Fatalf("APPROVE: %v", err)
	}
	if upserts := countEventsSince(mark, events.SubjectProductUpserted); len(upserts) != 1 {
		t.Fatalf("APPROVE: want 1 product.upserted, got %d", len(upserts))
	}

	// 4. UpdateProduct (staying public) emits product.upserted.
	mark = len(st.OutboxEvents())
	now = now.Add(time.Minute)
	if _, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id: prodID, Name: "Áo Sync V2",
	}); err != nil {
		t.Fatalf("UpdateProduct public: %v", err)
	}
	if upserts := countEventsSince(mark, events.SubjectProductUpserted); len(upserts) != 1 {
		t.Fatalf("UpdateProduct public: want 1 product.upserted, got %d", len(upserts))
	}

	// 5. CreateReview emits both review.created AND product.upserted with new rating.
	srv.SetOrderClient(&fakeOrderClient{
		fn: func(_ context.Context, _ *orderv1.GetOrderRequest) (*orderv1.GetOrderResponse, error) {
			return &orderv1.GetOrderResponse{Order: &orderv1.Order{
				Id: "ord-sync-1", BuyerId: "buyer-1", ShopId: "shop-sync",
				Status: orderv1.OrderStatus_ORDER_STATUS_COMPLETED,
				Items:  []*orderv1.OrderItem{{Id: "oi-sync-1", OrderId: "ord-sync-1", ProductId: prodID, SkuId: sku.GetId(), SkuName: "Default"}},
			}}, nil
		},
	})
	mark = len(st.OutboxEvents())
	now = now.Add(time.Minute)
	if _, err := srv.CreateReview(buyerCtx, &productv1.CreateReviewRequest{
		OrderId: "ord-sync-1", OrderItemId: "oi-sync-1", Rating: 5, Comment: "Rất tốt",
	}); err != nil {
		t.Fatalf("CreateReview: %v", err)
	}
	revUpserts := countEventsSince(mark, events.SubjectProductUpserted)
	if len(revUpserts) != 1 {
		t.Fatalf("CreateReview: want 1 product.upserted, got %d", len(revUpserts))
	}
	var revUpPayload events.ProductUpsertedPayload
	if err := json.Unmarshal(revUpserts[0].PayloadJSON, &revUpPayload); err != nil {
		t.Fatalf("unmarshal review product.upserted: %v", err)
	}
	if revUpPayload.RatingAvgX100 != 500 || revUpPayload.ReviewCount != 1 {
		t.Fatalf("review product.upserted rating mismatch: %+v", revUpPayload)
	}

	// 6. Stock transitions:
	// 6a. UpdateStock 5 -> 3 (stays APPROVED, in_stock unchanged) -> 0 product.upserted.
	mark = len(st.OutboxEvents())
	now = now.Add(time.Minute)
	updS1, err := srv.UpdateStock(sellerCtx, &productv1.UpdateStockRequest{
		SkuId: sku.GetId(), NewStock: 3, ExpectedVersion: sku.GetVersion(),
	})
	if err != nil {
		t.Fatalf("UpdateStock 5->3: %v", err)
	}
	if upserts := countEventsSince(mark, events.SubjectProductUpserted); len(upserts) != 0 {
		t.Fatalf("UpdateStock without status/in_stock change: want 0 product.upserted, got %d", len(upserts))
	}

	// 6b. ConfirmStock drains 3 -> 0 (APPROVED -> OUT_OF_STOCK) -> 1 product.upserted.
	if _, err := srv.ReserveStock(buyerCtx, &productv1.ReserveStockRequest{
		OrderId: "ord-drain-1", Items: []*productv1.StockReservationItem{{SkuId: sku.GetId(), Quantity: 3}},
	}); err != nil {
		t.Fatalf("ReserveStock 3: %v", err)
	}
	mark = len(st.OutboxEvents())
	now = now.Add(time.Minute)
	if _, err := srv.ConfirmStock(adminCtx, &productv1.ConfirmStockRequest{OrderId: "ord-drain-1"}); err != nil {
		t.Fatalf("ConfirmStock drain: %v", err)
	}
	if upserts := countEventsSince(mark, events.SubjectProductUpserted); len(upserts) != 1 {
		t.Fatalf("ConfirmStock APPROVED->OUT_OF_STOCK: want 1 product.upserted, got %d", len(upserts))
	}

	// 6c. RestockOrderItems brings 0 -> 2 (OUT_OF_STOCK -> APPROVED) -> 1 product.upserted.
	mark = len(st.OutboxEvents())
	now = now.Add(time.Minute)
	if _, err := srv.RestockOrderItems(adminCtx, &productv1.RestockOrderItemsRequest{
		OrderId: "ord-drain-1", Items: []*productv1.StockReservationItem{{SkuId: sku.GetId(), Quantity: 2}},
	}); err != nil {
		t.Fatalf("RestockOrderItems: %v", err)
	}
	if upserts := countEventsSince(mark, events.SubjectProductUpserted); len(upserts) != 1 {
		t.Fatalf("RestockOrderItems OUT_OF_STOCK->APPROVED: want 1 product.upserted, got %d", len(upserts))
	}

	// 6d. UpdateStock 2 -> 0 (APPROVED -> OUT_OF_STOCK) -> 1 product.upserted.
	curSKU, err := st.GetSKUWithContext(context.Background(), updS1.GetSku().GetId())
	if err != nil {
		t.Fatalf("GetSKUWithContext: %v", err)
	}
	mark = len(st.OutboxEvents())
	now = now.Add(time.Minute)
	if _, err := srv.UpdateStock(sellerCtx, &productv1.UpdateStockRequest{
		SkuId: curSKU.ID, NewStock: 0, ExpectedVersion: curSKU.Version,
	}); err != nil {
		t.Fatalf("UpdateStock 2->0: %v", err)
	}
	if upserts := countEventsSince(mark, events.SubjectProductUpserted); len(upserts) != 1 {
		t.Fatalf("UpdateStock APPROVED->OUT_OF_STOCK: want 1 product.upserted, got %d", len(upserts))
	}

	// 7. UpdateProduct leaving public (OUT_OF_STOCK -> HIDDEN) emits product.deleted.
	mark = len(st.OutboxEvents())
	now = now.Add(time.Minute)
	if _, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id: prodID, Status: productv1.ProductStatus_PRODUCT_STATUS_HIDDEN,
	}); err != nil {
		t.Fatalf("UpdateProduct HIDDEN: %v", err)
	}
	if dels := countEventsSince(mark, events.SubjectProductDeleted); len(dels) != 1 {
		t.Fatalf("UpdateProduct -> HIDDEN: want 1 product.deleted, got %d", len(dels))
	}

	// 8. Unhide (HIDDEN -> APPROVED/OUT_OF_STOCK) emits product.upserted; REJECT emits product.deleted.
	mark = len(st.OutboxEvents())
	now = now.Add(time.Minute)
	if _, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id: prodID, Status: productv1.ProductStatus_PRODUCT_STATUS_APPROVED,
	}); err != nil {
		t.Fatalf("UpdateProduct unhide: %v", err)
	}
	if upserts := countEventsSince(mark, events.SubjectProductUpserted); len(upserts) != 1 {
		t.Fatalf("UpdateProduct unhide: want 1 product.upserted, got %d", len(upserts))
	}

	mark = len(st.OutboxEvents())
	now = now.Add(time.Minute)
	if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: prodID, Decision: productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_REJECT, Reason: "Vi phạm",
	}); err != nil {
		t.Fatalf("AdminReviewProduct REJECT: %v", err)
	}
	if dels := countEventsSince(mark, events.SubjectProductDeleted); len(dels) != 1 {
		t.Fatalf("AdminReviewProduct REJECT: want 1 product.deleted, got %d", len(dels))
	}

	// 9. Rescan BLOCK -> REJECTED emits product.deleted; DeleteProduct emits product.deleted.
	pRescanResp, _ := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: catAID, Name: "SP Quét Lại VuKhi",
		Skus: []*productv1.SKUInput{{SkuCode: "SYN-RES-1", PriceVnd: 100000, Stock: 4}},
	})
	_, _ = srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: pRescanResp.GetProduct().GetId(), Decision: productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	})
	_, _ = srv.CreateBannedKeyword(adminCtx, &productv1.CreateBannedKeywordRequest{
		Term: "vukhi", Action: productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_BLOCK,
	})
	mark = len(st.OutboxEvents())
	now = now.Add(time.Minute)
	if _, err := srv.RescanProductsForBannedContent(adminCtx, &productv1.RescanProductsForBannedContentRequest{}); err != nil {
		t.Fatalf("Rescan: %v", err)
	}
	if dels := countEventsSince(mark, events.SubjectProductDeleted); len(dels) != 1 {
		t.Fatalf("Rescan REJECT: want 1 product.deleted, got %d", len(dels))
	}

	mark = len(st.OutboxEvents())
	now = now.Add(time.Minute)
	if _, err := srv.DeleteProduct(sellerCtx, &productv1.DeleteProductRequest{Id: prodID}); err != nil {
		t.Fatalf("DeleteProduct: %v", err)
	}
	if dels := countEventsSince(mark, events.SubjectProductDeleted); len(dels) != 1 {
		t.Fatalf("DeleteProduct: want 1 product.deleted, got %d", len(dels))
	}

	// Verify ZERO outbox payloads contain commission_rate_bps or flagged_keywords.
	for _, ev := range st.OutboxEvents() {
		raw := string(ev.PayloadJSON)
		if strings.Contains(raw, "commission_rate_bps") || strings.Contains(raw, "flagged_keywords") {
			t.Fatalf("forbidden field leaked in outbox event %s (%s): %s", ev.EventType, ev.ID, raw)
		}
	}
}

func TestW3B3_B1_ConcurrentUpdateDeleteStock_NoDeadlockAndOutboxOrder(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)
	adminCtx := ctxWithRole("admin-1", auth.RoleAdmin, "")
	sellerCtx := ctxWithRole("seller-conc", auth.RoleSeller, "shop-conc")

	cat, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name: "Conc Cat", Slug: "conc-cat", CommissionRateBps: 500,
	})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}

	for round := 0; round < 5; round++ {
		pResp, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
			CategoryId: cat.GetCategory().GetId(),
			Name:       fmt.Sprintf("Conc Prod %d", round),
			Skus:       []*productv1.SKUInput{{SkuCode: fmt.Sprintf("CONC-%d", round), PriceVnd: 150000, Stock: 10}},
		})
		if err != nil {
			t.Fatalf("CreateProduct round %d: %v", round, err)
		}
		prodID := pResp.GetProduct().GetId()
		skuID := pResp.GetProduct().GetSkus()[0].GetId()
		if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
			ProductId: prodID, Decision: productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
		}); err != nil {
			t.Fatalf("APPROVE round %d: %v", round, err)
		}

		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(3)
		go func() {
			defer wg.Done()
			<-start
			_, _ = srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
				Id: prodID, Name: fmt.Sprintf("Conc Prod %d Edited", round),
			})
		}()
		go func() {
			defer wg.Done()
			<-start
			_, _ = srv.UpdateStock(sellerCtx, &productv1.UpdateStockRequest{
				SkuId: skuID, NewStock: 0, ExpectedVersion: 1,
			})
		}()
		go func() {
			defer wg.Done()
			<-start
			_, _ = srv.DeleteProduct(sellerCtx, &productv1.DeleteProductRequest{Id: prodID})
		}()
		close(start)
		wg.Wait()

		// Inspect outbox events for prodID: seq must be strictly increasing, and once product.deleted
		// appears, no product.upserted may appear after it.
		var prevSeq int64
		var prevUpdatedAt time.Time
		sawDelete := false
		for _, ev := range st.OutboxEvents() {
			if ev.AggregateID != prodID {
				continue
			}
			if ev.Seq <= prevSeq {
				t.Fatalf("round %d: non-increasing outbox seq %d <= %d", round, ev.Seq, prevSeq)
			}
			prevSeq = ev.Seq
			switch ev.EventType {
			case events.SubjectProductUpserted:
				if sawDelete {
					t.Fatalf("round %d: product.upserted (seq=%d) appeared AFTER product.deleted!", round, ev.Seq)
				}
				var p events.ProductUpsertedPayload
				if err := json.Unmarshal(ev.PayloadJSON, &p); err != nil {
					t.Fatalf("unmarshal upserted: %v", err)
				}
				ts, err := time.Parse(time.RFC3339Nano, p.UpdatedAt)
				if err != nil {
					t.Fatalf("parse upserted updated_at %q: %v", p.UpdatedAt, err)
				}
				if !prevUpdatedAt.IsZero() && ts.Before(prevUpdatedAt) {
					t.Fatalf("round %d: outbox updated_at went backwards (%v < %v)", round, ts, prevUpdatedAt)
				}
				prevUpdatedAt = ts
			case events.SubjectProductDeleted:
				sawDelete = true
				var p events.ProductDeletedPayload
				if err := json.Unmarshal(ev.PayloadJSON, &p); err != nil {
					t.Fatalf("unmarshal deleted: %v", err)
				}
				ts, err := time.Parse(time.RFC3339Nano, p.UpdatedAt)
				if err != nil {
					t.Fatalf("parse deleted updated_at %q: %v", p.UpdatedAt, err)
				}
				if !prevUpdatedAt.IsZero() && ts.Before(prevUpdatedAt) {
					t.Fatalf("round %d: deleted updated_at went backwards (%v < %v)", round, ts, prevUpdatedAt)
				}
				prevUpdatedAt = ts
			}
		}
		if !sawDelete {
			t.Fatalf("round %d: expected product.deleted outbox event", round)
		}
	}
}

// TestW3B3_B1_ColorCodesProjection: color_code feeds color_codes (lowercase, deduplicated) and never leaks into colors,
// in both the RPC projection and the product.upserted payload.
func TestW3B3_B1_ColorCodesProjection(t *testing.T) {
	proj := buildProductIndexProjection(store.Product{ID: "p1", CategoryID: "c1"}, []store.SKU{
		{ID: "s1", PriceVND: 1000, Attributes: map[string]string{"color": "Trắng tinh", "color_code": "Trang"}},
		{ID: "s2", PriceVND: 1000, Attributes: map[string]string{"color": "Trắng tinh", "Mã màu": "trang"}},
		{ID: "s3", PriceVND: 1000, Attributes: map[string]string{"color": "Đen", "ma_mau": "den"}},
	}, nil, time.Now())
	wantColors, wantCodes := []string{"Trắng tinh", "Đen"}, []string{"trang", "den"}
	pb, ev := proj.toProto(), proj.toOutboxPayload()
	if !slices.Equal(pb.GetColors(), wantColors) || !slices.Equal(pb.GetColorCodes(), wantCodes) {
		t.Fatalf("proto colors=%v codes=%v, want %v %v", pb.GetColors(), pb.GetColorCodes(), wantColors, wantCodes)
	}
	if !slices.Equal(ev.Colors, wantColors) || !slices.Equal(ev.ColorCodes, wantCodes) {
		t.Fatalf("event colors=%v codes=%v, want %v %v", ev.Colors, ev.ColorCodes, wantColors, wantCodes)
	}
}

// TestW3B3_P7_ColorCodesConcatenatedKeys verifies audit 2.4: colorCode/colourCode/mamau/maMau
// feed color_codes (lowercase, deduplicated) and never leak into colors.
func TestW3B3_P7_ColorCodesConcatenatedKeys(t *testing.T) {
	proj := buildProductIndexProjection(store.Product{ID: "p-p7", CategoryID: "c1"}, []store.SKU{
		{ID: "s1", PriceVND: 1000, Attributes: map[string]string{"color": "Trắng tinh", "color_code": "Trang"}},
		{ID: "s2", PriceVND: 1000, Attributes: map[string]string{"colorCode": "Xanh"}},
		{ID: "s3", PriceVND: 1000, Attributes: map[string]string{"colourCode": "Do"}},
		{ID: "s4", PriceVND: 1000, Attributes: map[string]string{"mamau": "Vang", "maMau": "vang"}},
	}, nil, time.Now())
	wantColors := []string{"Trắng tinh"}
	wantCodes := []string{"trang", "xanh", "do", "vang"}
	pb, ev := proj.toProto(), proj.toOutboxPayload()
	if !slices.Equal(pb.GetColors(), wantColors) || !slices.Equal(pb.GetColorCodes(), wantCodes) {
		t.Fatalf("proto colors=%v codes=%v, want %v %v", pb.GetColors(), pb.GetColorCodes(), wantColors, wantCodes)
	}
	if !slices.Equal(ev.Colors, wantColors) || !slices.Equal(ev.ColorCodes, wantCodes) {
		t.Fatalf("event colors=%v codes=%v, want %v %v", ev.Colors, ev.ColorCodes, wantColors, wantCodes)
	}
}
