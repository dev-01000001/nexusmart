package server

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	cartv1 "github.com/nexusmart/core/gen/go/nexusmart/cart/v1"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/cart/internal/store"
)

type fakeProductClient struct {
	mu           sync.RWMutex
	skus         map[string]*productv1.SKU
	failAttempts atomic.Int32
}

func (f *fakeProductClient) GetSKUsBatch(_ context.Context, in *productv1.GetSKUsBatchRequest, _ ...grpc.CallOption) (*productv1.GetSKUsBatchResponse, error) {
	if rem := f.failAttempts.Add(-1); rem >= 0 {
		return nil, status.Error(codes.Unavailable, "simulated transient product failure")
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]*productv1.SKU, 0, len(in.GetSkuIds()))
	for _, id := range in.GetSkuIds() {
		if sk, ok := f.skus[id]; ok {
			out = append(out, sk)
		}
	}
	return &productv1.GetSKUsBatchResponse{Skus: out}, nil
}

func (f *fakeProductClient) setPrice(skuID string, priceVND int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if sk, ok := f.skus[skuID]; ok {
		sk.PriceVnd = priceVND
	}
}

type fakeUserClient struct {
	shops map[string]string
}

func (f *fakeUserClient) GetShop(_ context.Context, in *userv1.GetShopRequest, _ ...grpc.CallOption) (*userv1.GetShopResponse, error) {
	name, ok := f.shops[in.GetShopId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "shop not found")
	}
	return &userv1.GetShopResponse{
		Shop: &userv1.Shop{
			Id:   in.GetShopId(),
			Name: name,
		},
	}, nil
}

func buyerCtx(userID string) context.Context {
	return auth.WithClaims(context.Background(), &auth.Claims{
		Role: "buyer",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: userID,
		},
	})
}

func newTestCartServer(t *testing.T, prod *fakeProductClient, usr UserClient) (*Server, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return New(store.NewRedisStore(rdb), prod, usr, nil), mr
}

func TestCartMultiShopGroupingAndPriceChangeDetection(t *testing.T) {
	prod := &fakeProductClient{
		skus: map[string]*productv1.SKU{
			"sku-a1": {
				Id: "sku-a1", ProductId: "p-1", ShopId: "shop-a",
				ProductName: "Coffee Beans", Name: "500g", PriceVnd: 120000,
				AvailableStock: 10, WeightGrams: 500, IsActive: true,
			},
			"sku-a2": {
				Id: "sku-a2", ProductId: "p-2", ShopId: "shop-a",
				ProductName: "Filter Paper", Name: "100pcs", PriceVnd: 50000,
				AvailableStock: 20, WeightGrams: 100, IsActive: true,
			},
			"sku-b1": {
				Id: "sku-b1", ProductId: "p-3", ShopId: "shop-b",
				ProductName: "Ceramic Mug", Name: "Blue", PriceVnd: 80000,
				AvailableStock: 5, WeightGrams: 350, IsActive: true,
			},
		},
	}
	usr := &fakeUserClient{
		shops: map[string]string{
			"shop-a": "Saigon Coffee Roasters",
			"shop-b": "Bat Trang Ceramics",
		},
	}

	srv, _ := newTestCartServer(t, prod, usr)
	ctx := buyerCtx("buyer-1")

	// Add items from two shops
	r1, err := srv.AddItem(ctx, &cartv1.AddItemRequest{SkuId: "sku-a1", Quantity: 2})
	if err != nil || r1.ClampedToStock {
		t.Fatalf("AddItem sku-a1 failed: resp=%+v err=%v", r1, err)
	}
	if _, err := srv.AddItem(ctx, &cartv1.AddItemRequest{SkuId: "sku-a2", Quantity: 1}); err != nil {
		t.Fatalf("AddItem sku-a2 failed: %v", err)
	}
	r3, err := srv.AddItem(ctx, &cartv1.AddItemRequest{SkuId: "sku-b1", Quantity: 3})
	if err != nil {
		t.Fatalf("AddItem sku-b1 failed: %v", err)
	}

	cart := r3.Cart
	if len(cart.ShopGroups) != 2 {
		t.Fatalf("expected 2 shop groups, got %d", len(cart.ShopGroups))
	}
	// Shop A: 2*120000 + 1*50000 = 290000, weight 2*500 + 1*100 = 1100
	grpA := cart.ShopGroups[0]
	if grpA.ShopId != "shop-a" || grpA.ShopName != "Saigon Coffee Roasters" || grpA.SubtotalVnd != 290000 || grpA.TotalWeightGrams != 1100 {
		t.Fatalf("unexpected shop-a group: %+v", grpA)
	}
	// Shop B: 3*80000 = 240000, weight 3*350 = 1050
	grpB := cart.ShopGroups[1]
	if grpB.ShopId != "shop-b" || grpB.ShopName != "Bat Trang Ceramics" || grpB.SubtotalVnd != 240000 || grpB.TotalWeightGrams != 1050 {
		t.Fatalf("unexpected shop-b group: %+v", grpB)
	}
	if cart.TotalItems != 6 || cart.TotalVnd != 530000 {
		t.Fatalf("expected total_items=6 total_vnd=530000, got items=%d total=%d", cart.TotalItems, cart.TotalVnd)
	}

	// Simulate live price change on sku-a1: 120000 -> 150000
	prod.setPrice("sku-a1", 150000)

	getResp, err := srv.GetCart(ctx, &cartv1.GetCartRequest{})
	if err != nil {
		t.Fatalf("GetCart failed: %v", err)
	}
	itemA1 := getResp.Cart.ShopGroups[0].Items[0]
	if itemA1.SkuId != "sku-a1" || !itemA1.PriceChanged || itemA1.AddedPriceVnd != 120000 || itemA1.PriceVnd != 150000 {
		t.Fatalf("expected price_changed=true (120000 -> 150000), got %+v", itemA1)
	}
	// New total: Shop A (2*150000 + 50000 = 350000) + Shop B (240000) = 590000
	if getResp.Cart.TotalVnd != 590000 {
		t.Fatalf("expected updated live total_vnd=590000, got %d", getResp.Cart.TotalVnd)
	}

	// Deactivate/soft-delete sku-a2 -> GetCart must skip inactive SKUs from groups and totals
	prod.mu.Lock()
	prod.skus["sku-a2"].IsActive = false
	prod.mu.Unlock()

	afterDeact, err := srv.GetCart(ctx, &cartv1.GetCartRequest{})
	if err != nil {
		t.Fatalf("GetCart after deactivate failed: %v", err)
	}
	if len(afterDeact.Cart.ShopGroups) != 2 {
		t.Fatalf("expected 2 shop groups, got %d", len(afterDeact.Cart.ShopGroups))
	}
	if len(afterDeact.Cart.ShopGroups[0].Items) != 1 || afterDeact.Cart.ShopGroups[0].Items[0].SkuId != "sku-a1" {
		t.Fatalf("expected inactive sku-a2 excluded from shop-a, got %+v", afterDeact.Cart.ShopGroups[0].Items)
	}
	// Shop A: 2*150000 = 300000; Shop B: 240000; Total: 540000, TotalItems: 5
	if afterDeact.Cart.ShopGroups[0].SubtotalVnd != 300000 || afterDeact.Cart.TotalItems != 5 || afterDeact.Cart.TotalVnd != 540000 {
		t.Fatalf("unexpected cart totals after excluding inactive sku: %+v", afterDeact.Cart)
	}
}

func TestCartStockClampingAndOutOfStockGuards(t *testing.T) {
	prod := &fakeProductClient{
		skus: map[string]*productv1.SKU{
			"sku-limited": {
				Id: "sku-limited", ProductId: "p-1", ShopId: "shop-a",
				ProductName: "Keyboard", Name: "TKL", PriceVnd: 1000000,
				AvailableStock: 3, WeightGrams: 800, IsActive: true,
			},
			"sku-oos": {
				Id: "sku-oos", ProductId: "p-2", ShopId: "shop-a",
				ProductName: "Mouse", Name: "Black", PriceVnd: 500000,
				AvailableStock: 0, WeightGrams: 120, IsActive: true,
			},
			"sku-inactive": {
				Id: "sku-inactive", ProductId: "p-3", ShopId: "shop-a",
				ProductName: "Draft Item", Name: "Default", PriceVnd: 100000,
				AvailableStock: 10, WeightGrams: 100, IsActive: false,
			},
		},
	}

	srv, _ := newTestCartServer(t, prod, nil)
	ctx := buyerCtx("buyer-1")

	// Missing SKU -> NotFound
	_, err := srv.AddItem(ctx, &cartv1.AddItemRequest{SkuId: "non-existent", Quantity: 1})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound, got %v", err)
	}

	// Inactive SKU -> FailedPrecondition
	_, err = srv.AddItem(ctx, &cartv1.AddItemRequest{SkuId: "sku-inactive", Quantity: 1})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition for inactive SKU, got %v", err)
	}

	// Zero stock SKU -> FailedPrecondition
	_, err = srv.AddItem(ctx, &cartv1.AddItemRequest{SkuId: "sku-oos", Quantity: 1})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition for 0 stock SKU, got %v", err)
	}

	// Add 5 when only 3 available -> clamped to 3 with clamped_to_stock=true
	addResp, err := srv.AddItem(ctx, &cartv1.AddItemRequest{SkuId: "sku-limited", Quantity: 5})
	if err != nil {
		t.Fatalf("AddItem failed: %v", err)
	}
	if !addResp.ClampedToStock || addResp.Cart.TotalItems != 3 || addResp.Cart.ShopGroups[0].Items[0].Quantity != 3 {
		t.Fatalf("expected clamped to 3, got clamped=%v cart=%+v", addResp.ClampedToStock, addResp.Cart)
	}

	// UpdateItemQty to 2 (within stock) -> clamped=false
	upd1, err := srv.UpdateItemQty(ctx, &cartv1.UpdateItemQtyRequest{SkuId: "sku-limited", Quantity: 2})
	if err != nil || upd1.ClampedToStock || upd1.Cart.TotalItems != 2 {
		t.Fatalf("expected qty 2 unclamped, got %+v err=%v", upd1, err)
	}

	// UpdateItemQty to 10 (exceeds stock 3) -> clamped to 3
	upd2, err := srv.UpdateItemQty(ctx, &cartv1.UpdateItemQtyRequest{SkuId: "sku-limited", Quantity: 10})
	if err != nil || !upd2.ClampedToStock || upd2.Cart.TotalItems != 3 {
		t.Fatalf("expected qty clamped to 3, got %+v err=%v", upd2, err)
	}

	// UpdateItemQty to 0 removes item
	upd3, err := srv.UpdateItemQty(ctx, &cartv1.UpdateItemQtyRequest{SkuId: "sku-limited", Quantity: 0})
	if err != nil || len(upd3.Cart.ShopGroups) != 0 || upd3.Cart.TotalItems != 0 {
		t.Fatalf("expected empty cart after qty=0, got %+v err=%v", upd3, err)
	}
}

func TestCartRemoveClearAndOwnership(t *testing.T) {
	prod := &fakeProductClient{
		skus: map[string]*productv1.SKU{
			"sku-1": {Id: "sku-1", ProductId: "p1", ShopId: "s1", ProductName: "A", Name: "1", PriceVnd: 10000, AvailableStock: 10, IsActive: true},
			"sku-2": {Id: "sku-2", ProductId: "p2", ShopId: "s1", ProductName: "B", Name: "2", PriceVnd: 20000, AvailableStock: 10, IsActive: true},
			"sku-3": {Id: "sku-3", ProductId: "p3", ShopId: "s2", ProductName: "C", Name: "3", PriceVnd: 30000, AvailableStock: 10, IsActive: true},
		},
	}

	srv, _ := newTestCartServer(t, prod, nil)
	ctxA := buyerCtx("buyer-a")
	ctxB := buyerCtx("buyer-b")

	// Unauthenticated AddItem rejected
	_, err := srv.AddItem(context.Background(), &cartv1.AddItemRequest{UserId: "buyer-a", SkuId: "sku-1", Quantity: 1})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got %v", err)
	}

	// Buyer A adds 3 SKUs
	_, _ = srv.AddItem(ctxA, &cartv1.AddItemRequest{SkuId: "sku-1", Quantity: 1})
	_, _ = srv.AddItem(ctxA, &cartv1.AddItemRequest{SkuId: "sku-2", Quantity: 1})
	_, _ = srv.AddItem(ctxA, &cartv1.AddItemRequest{SkuId: "sku-3", Quantity: 1})

	// Buyer B cannot read, mutate, or clear Buyer A's cart
	if _, err := srv.GetCart(ctxB, &cartv1.GetCartRequest{UserId: "buyer-a"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for cross-user GetCart, got %v", err)
	}
	if _, err := srv.RemoveItem(ctxB, &cartv1.RemoveItemRequest{UserId: "buyer-a", SkuId: "sku-1"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for cross-user RemoveItem, got %v", err)
	}
	if _, err := srv.ClearItems(ctxB, &cartv1.ClearItemsRequest{UserId: "buyer-a"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for cross-user ClearItems, got %v", err)
	}

	// Buyer A removes sku-1
	remResp, err := srv.RemoveItem(ctxA, &cartv1.RemoveItemRequest{SkuId: "sku-1"})
	if err != nil || remResp.Cart.TotalItems != 2 {
		t.Fatalf("expected 2 items after RemoveItem, got %+v err=%v", remResp, err)
	}

	// Unauthenticated GetCart and ClearItems MUST be rejected (B1).
	if _, err := srv.GetCart(context.Background(), &cartv1.GetCartRequest{UserId: "buyer-a"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated for anonymous GetCart, got %v", err)
	}
	if _, err := srv.ClearItems(context.Background(), &cartv1.ClearItemsRequest{
		UserId: "buyer-a",
		SkuIds: []string{"sku-2"},
	}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated for anonymous ClearItems, got %v", err)
	}

	// Authenticated buyer-a (forwarded buyer JWT during OrderService checkout) clears specific sku-2.
	clrResp, err := srv.ClearItems(ctxA, &cartv1.ClearItemsRequest{
		UserId: "buyer-a",
		SkuIds: []string{"sku-2"},
	})
	if err != nil || !clrResp.Ok {
		t.Fatalf("ClearItems partial failed: %+v err=%v", clrResp, err)
	}
	afterPartial, err := srv.GetCart(ctxA, &cartv1.GetCartRequest{})
	if err != nil || afterPartial.Cart.TotalItems != 1 || afterPartial.Cart.ShopGroups[0].Items[0].SkuId != "sku-3" {
		t.Fatalf("expected only sku-3 remaining, got %+v err=%v", afterPartial, err)
	}

	// ClearItems with empty sku_ids clears all remaining items
	if _, err := srv.ClearItems(ctxA, &cartv1.ClearItemsRequest{}); err != nil {
		t.Fatalf("ClearItems all failed: %v", err)
	}
	afterAll, err := srv.GetCart(ctxA, &cartv1.GetCartRequest{})
	if err != nil || afterAll.Cart.TotalItems != 0 || len(afterAll.Cart.ShopGroups) != 0 {
		t.Fatalf("expected completely empty cart, got %+v err=%v", afterAll, err)
	}
}

func TestCartShopFilterReadAndClearLeavesOtherShopsIntact(t *testing.T) {
	prod := &fakeProductClient{
		skus: map[string]*productv1.SKU{
			"sku-a1": {Id: "sku-a1", ProductId: "p1", ShopId: "shop-a", ProductName: "A1", Name: "M", PriceVnd: 100000, AvailableStock: 10, WeightGrams: 200, IsActive: true},
			"sku-a2": {Id: "sku-a2", ProductId: "p2", ShopId: "shop-a", ProductName: "A2", Name: "L", PriceVnd: 50000, AvailableStock: 10, WeightGrams: 150, IsActive: true},
			"sku-b1": {Id: "sku-b1", ProductId: "p3", ShopId: "shop-b", ProductName: "B1", Name: "S", PriceVnd: 80000, AvailableStock: 10, WeightGrams: 300, IsActive: true},
		},
	}
	usr := &fakeUserClient{
		shops: map[string]string{
			"shop-a": "Shop Alpha",
			"shop-b": "Shop Beta",
		},
	}

	srv, mr := newTestCartServer(t, prod, usr)
	ctx := buyerCtx("buyer-channel")

	_, _ = srv.AddItem(ctx, &cartv1.AddItemRequest{SkuId: "sku-a1", Quantity: 2})
	_, _ = srv.AddItem(ctx, &cartv1.AddItemRequest{SkuId: "sku-a2", Quantity: 1})
	_, _ = srv.AddItem(ctx, &cartv1.AddItemRequest{SkuId: "sku-b1", Quantity: 3})

	// Single Redis key invariant: only cart:<user_id> exists.
	if keys := mr.Keys(); len(keys) != 1 || keys[0] != "cart:buyer-channel" {
		t.Fatalf("expected single Redis key cart:buyer-channel, got %v", keys)
	}

	// Filtered GetCart(shop_id="shop-a") returns only shop-a items & recalculated totals.
	filtered, err := srv.GetCart(ctx, &cartv1.GetCartRequest{ShopId: "shop-a"})
	if err != nil {
		t.Fatalf("GetCart(shop-a): %v", err)
	}
	if len(filtered.Cart.ShopGroups) != 1 || filtered.Cart.ShopGroups[0].ShopId != "shop-a" {
		t.Fatalf("expected only shop-a group, got %+v", filtered.Cart.ShopGroups)
	}
	if filtered.Cart.TotalItems != 3 || filtered.Cart.TotalVnd != 250000 || filtered.Cart.ShopGroups[0].TotalWeightGrams != 550 {
		t.Fatalf("unexpected filtered totals: %+v", filtered.Cart)
	}

	// Filtered GetCart for a shop with no items in cart returns empty without modifying Redis.
	emptyShop, err := srv.GetCart(ctx, &cartv1.GetCartRequest{ShopId: "shop-other"})
	if err != nil || len(emptyShop.Cart.ShopGroups) != 0 || emptyShop.Cart.TotalItems != 0 || emptyShop.Cart.TotalVnd != 0 {
		t.Fatalf("expected empty cart for shop-other, got %+v err=%v", emptyShop, err)
	}

	// Unfiltered GetCart STILL returns both shops untouched in Redis!
	full, err := srv.GetCart(ctx, &cartv1.GetCartRequest{})
	if err != nil || len(full.Cart.ShopGroups) != 2 || full.Cart.TotalItems != 6 || full.Cart.TotalVnd != 490000 {
		t.Fatalf("filtered GetCart must not mutate Redis cart; got %+v err=%v", full, err)
	}

	// ClearItems with BOTH shop_id="shop-a" and mixed sku_ids=["sku-a1", "sku-b1"] must ONLY clear sku-a1!
	if _, err := srv.ClearItems(ctx, &cartv1.ClearItemsRequest{
		ShopId: "shop-a",
		SkuIds: []string{"sku-a1", "sku-b1"},
	}); err != nil {
		t.Fatalf("ClearItems(shop-a, mixed skus): %v", err)
	}
	afterMixed, err := srv.GetCart(ctx, &cartv1.GetCartRequest{})
	if err != nil || len(afterMixed.Cart.ShopGroups) != 2 || afterMixed.Cart.TotalItems != 4 {
		t.Fatalf("expected sku-a2 and sku-b1 remaining (total_items=4), got %+v err=%v", afterMixed, err)
	}
	if len(afterMixed.Cart.ShopGroups[0].Items) != 1 || afterMixed.Cart.ShopGroups[0].Items[0].SkuId != "sku-a2" {
		t.Fatalf("expected only sku-a2 left in shop-a, got %+v", afterMixed.Cart.ShopGroups[0].Items)
	}

	// ClearItems with shop_id="shop-a" (empty sku_ids) clears remaining shop-a items, leaving shop-b intact!
	if _, err := srv.ClearItems(ctx, &cartv1.ClearItemsRequest{ShopId: "shop-a"}); err != nil {
		t.Fatalf("ClearItems(shop-a): %v", err)
	}
	afterShopClear, err := srv.GetCart(ctx, &cartv1.GetCartRequest{})
	if err != nil {
		t.Fatalf("GetCart after shop clear: %v", err)
	}
	if len(afterShopClear.Cart.ShopGroups) != 1 || afterShopClear.Cart.ShopGroups[0].ShopId != "shop-b" {
		t.Fatalf("expected only shop-b remaining after clearing shop-a, got %+v", afterShopClear.Cart.ShopGroups)
	}
	if afterShopClear.Cart.TotalItems != 3 || afterShopClear.Cart.TotalVnd != 240000 {
		t.Fatalf("expected shop-b intact (3 items, 240000 VND), got %+v", afterShopClear.Cart)
	}
	// Still single Redis key.
	if keys := mr.Keys(); len(keys) != 1 || keys[0] != "cart:buyer-channel" {
		t.Fatalf("expected single Redis key cart:buyer-channel, got %v", keys)
	}
}

func TestCartProductTransientRetry(t *testing.T) {
	prod := &fakeProductClient{
		skus: map[string]*productv1.SKU{
			"sku-retry": {Id: "sku-retry", ProductId: "p1", ShopId: "s1", ProductName: "Item", Name: "Std", PriceVnd: 50000, AvailableStock: 5, IsActive: true},
		},
	}
	// Fail first 2 attempts transiently; 3rd attempt succeeds
	prod.failAttempts.Store(2)

	srv, _ := newTestCartServer(t, prod, nil)
	ctx := buyerCtx("buyer-retry")

	resp, err := srv.AddItem(ctx, &cartv1.AddItemRequest{SkuId: "sku-retry", Quantity: 1})
	if err != nil || resp.Cart.TotalItems != 1 {
		t.Fatalf("expected retry to recover after 2 transient failures, got resp=%+v err=%v", resp, err)
	}
}
