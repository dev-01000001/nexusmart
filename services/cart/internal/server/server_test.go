package server

import (
	"context"
	"encoding/json"
	"math"
	"strings"
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

	// Unauthenticated requests are rejected across all CartService RPCs (including GetCart & ClearItems).
	if _, err := srv.GetCart(context.Background(), &cartv1.GetCartRequest{UserId: "buyer-a"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated for anonymous GetCart with user_id, got %v", err)
	}
	if _, err := srv.GetCart(context.Background(), &cartv1.GetCartRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated for anonymous GetCart without user_id, got %v", err)
	}
	if _, err := srv.AddItem(context.Background(), &cartv1.AddItemRequest{UserId: "buyer-a", SkuId: "sku-1", Quantity: 1}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated for anonymous AddItem, got %v", err)
	}
	if _, err := srv.UpdateItemQty(context.Background(), &cartv1.UpdateItemQtyRequest{UserId: "buyer-a", SkuId: "sku-1", Quantity: 2}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated for anonymous UpdateItemQty, got %v", err)
	}
	if _, err := srv.RemoveItem(context.Background(), &cartv1.RemoveItemRequest{UserId: "buyer-a", SkuId: "sku-1"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated for anonymous RemoveItem, got %v", err)
	}
	if _, err := srv.ClearItems(context.Background(), &cartv1.ClearItemsRequest{UserId: "buyer-a", SkuIds: []string{"sku-1"}}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated for anonymous ClearItems with user_id, got %v", err)
	}

	// Service tokens are rejected at handler level too (defense-in-depth alongside interceptor internalRPCs=false).
	svcCtx := auth.WithClaims(context.Background(), &auth.Claims{
		Role:    auth.ServiceRole,
		Service: "order-service",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: "order-service",
		},
	})
	if _, err := srv.GetCart(svcCtx, &cartv1.GetCartRequest{UserId: "buyer-a"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for service-token GetCart, got %v", err)
	}
	if _, err := srv.ClearItems(svcCtx, &cartv1.ClearItemsRequest{UserId: "buyer-a", SkuIds: []string{"sku-1"}}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for service-token ClearItems, got %v", err)
	}

	// Buyer A adds 3 SKUs
	_, _ = srv.AddItem(ctxA, &cartv1.AddItemRequest{SkuId: "sku-1", Quantity: 1})
	_, _ = srv.AddItem(ctxA, &cartv1.AddItemRequest{SkuId: "sku-2", Quantity: 1})
	_, _ = srv.AddItem(ctxA, &cartv1.AddItemRequest{SkuId: "sku-3", Quantity: 1})

	// Buyer A cannot target Buyer B's user_id, and Buyer B cannot read/mutate/clear Buyer A's cart
	if _, err := srv.GetCart(ctxA, &cartv1.GetCartRequest{UserId: "buyer-b"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for buyer-a GetCart with user_id=buyer-b, got %v", err)
	}
	if _, err := srv.AddItem(ctxA, &cartv1.AddItemRequest{UserId: "buyer-b", SkuId: "sku-1", Quantity: 1}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for buyer-a AddItem with user_id=buyer-b, got %v", err)
	}
	if _, err := srv.UpdateItemQty(ctxA, &cartv1.UpdateItemQtyRequest{UserId: "buyer-b", SkuId: "sku-1", Quantity: 2}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for buyer-a UpdateItemQty with user_id=buyer-b, got %v", err)
	}
	if _, err := srv.RemoveItem(ctxA, &cartv1.RemoveItemRequest{UserId: "buyer-b", SkuId: "sku-1"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for buyer-a RemoveItem with user_id=buyer-b, got %v", err)
	}
	if _, err := srv.ClearItems(ctxA, &cartv1.ClearItemsRequest{UserId: "buyer-b", SkuIds: []string{"sku-1"}}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for buyer-a ClearItems with user_id=buyer-b, got %v", err)
	}
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

	// OrderService forwards buyer A's JWT claims when clearing ordered sku-2 after checkout
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

type rawMemoryStore struct {
	items map[string]map[string]store.Item
}

func (m *rawMemoryStore) GetAll(_ context.Context, userID string) (map[string]store.Item, error) {
	out := make(map[string]store.Item)
	for k, v := range m.items[userID] {
		out[k] = v
	}
	return out, nil
}

func (m *rawMemoryStore) Get(_ context.Context, userID, skuID string) (store.Item, bool, error) {
	it, ok := m.items[userID][skuID]
	return it, ok, nil
}

func (m *rawMemoryStore) Set(_ context.Context, userID, skuID string, item store.Item) error {
	if m.items[userID] == nil {
		m.items[userID] = make(map[string]store.Item)
	}
	m.items[userID][skuID] = item
	return nil
}

func (m *rawMemoryStore) Delete(_ context.Context, userID string, skuIDs ...string) error {
	for _, id := range skuIDs {
		delete(m.items[userID], id)
	}
	return nil
}

func (m *rawMemoryStore) ClearAll(_ context.Context, userID string) error {
	delete(m.items, userID)
	return nil
}

func (m *rawMemoryStore) Ping(_ context.Context) error { return nil }

func TestCartAddItemInt32OverflowAndNonPositiveStoredItems(t *testing.T) {
	prod := &fakeProductClient{
		skus: map[string]*productv1.SKU{
			"sku-1": {
				Id: "sku-1", ProductId: "p-1", ShopId: "shop-a",
				ProductName: "Coffee", Name: "Std", PriceVnd: 100000,
				AvailableStock: 10, WeightGrams: 250, IsActive: true,
			},
			"sku-2": {
				Id: "sku-2", ProductId: "p-2", ShopId: "shop-a",
				ProductName: "Tea", Name: "Std", PriceVnd: 50000,
				AvailableStock: 10, WeightGrams: 100, IsActive: true,
			},
			"sku-3": {
				Id: "sku-3", ProductId: "p-3", ShopId: "shop-a",
				ProductName: "Cup", Name: "Std", PriceVnd: 20000,
				AvailableStock: 10, WeightGrams: 150, IsActive: true,
			},
		},
	}

	srv, mr := newTestCartServer(t, prod, nil)
	ctx := buyerCtx("buyer-overflow")

	// 1. Existing qty=1 + MaxInt32 (2147483647) must NOT overflow int32 to negative;
	// must clamp to available stock (10), persist positive qty in Redis, and return positive totals.
	first, err := srv.AddItem(ctx, &cartv1.AddItemRequest{SkuId: "sku-1", Quantity: 1})
	if err != nil || first.ClampedToStock || first.Cart.TotalItems != 1 {
		t.Fatalf("initial AddItem failed: resp=%+v err=%v", first, err)
	}
	overflowResp, err := srv.AddItem(ctx, &cartv1.AddItemRequest{SkuId: "sku-1", Quantity: math.MaxInt32})
	if err != nil {
		t.Fatalf("overflow AddItem failed: %v", err)
	}
	if !overflowResp.ClampedToStock {
		t.Fatalf("expected ClampedToStock=true on int32 overflow addition, got false")
	}
	if len(overflowResp.Cart.ShopGroups) != 1 || len(overflowResp.Cart.ShopGroups[0].Items) != 1 {
		t.Fatalf("expected 1 shop group with 1 item, got %+v", overflowResp.Cart)
	}
	if got := overflowResp.Cart.ShopGroups[0].Items[0].Quantity; got != 10 {
		t.Fatalf("item quantity = %d, want 10", got)
	}
	if overflowResp.Cart.TotalItems != 10 || overflowResp.Cart.TotalVnd != 1000000 {
		t.Fatalf("unexpected cart totals after overflow clamp: items=%d total_vnd=%d",
			overflowResp.Cart.TotalItems, overflowResp.Cart.TotalVnd)
	}
	rawRedisVal := mr.HGet("cart:buyer-overflow", "sku-1")
	var persisted store.Item
	if err := json.Unmarshal([]byte(rawRedisVal), &persisted); err != nil {
		t.Fatalf("unmarshal persisted redis cart item %q: %v", rawRedisVal, err)
	}
	if persisted.Quantity != 10 {
		t.Fatalf("persisted redis quantity = %d, want 10 (raw=%s)", persisted.Quantity, rawRedisVal)
	}

	// 2. Corrupted <= 0 entries already present in Redis must be skipped when reading cart.
	mr.HSet("cart:buyer-overflow", "sku-2", `{"quantity":0,"added_price_vnd":50000,"selected":true}`)
	mr.HSet("cart:buyer-overflow", "sku-3", `{"quantity":-5,"added_price_vnd":20000,"selected":true}`)

	gotCart, err := srv.GetCart(ctx, &cartv1.GetCartRequest{})
	if err != nil {
		t.Fatalf("GetCart with corrupted Redis items failed: %v", err)
	}
	if len(gotCart.Cart.ShopGroups) != 1 || len(gotCart.Cart.ShopGroups[0].Items) != 1 || gotCart.Cart.ShopGroups[0].Items[0].SkuId != "sku-1" {
		t.Fatalf("expected only valid sku-1 in cart, got %+v", gotCart.Cart)
	}
	if gotCart.Cart.TotalItems != 10 || gotCart.Cart.TotalVnd != 1000000 {
		t.Fatalf("corrupted Redis rows leaked into totals: items=%d total_vnd=%d",
			gotCart.Cart.TotalItems, gotCart.Cart.TotalVnd)
	}

	// 3. Even if a Store implementation directly returns <= 0 items to buildCart or AddItem,
	// buildCart skips non-positive lines and AddItem treats negative existing qty as 0.
	rawStore := &rawMemoryStore{
		items: map[string]map[string]store.Item{
			"buyer-corrupt": {
				"sku-1": {Quantity: 2, AddedPriceVND: 100000, Selected: true},
				"sku-2": {Quantity: 0, AddedPriceVND: 50000, Selected: true},
				"sku-3": {Quantity: -4, AddedPriceVND: 20000, Selected: true},
			},
		},
	}
	corruptSrv := New(rawStore, prod, nil, nil)
	corruptCtx := buyerCtx("buyer-corrupt")

	readResp, err := corruptSrv.GetCart(corruptCtx, &cartv1.GetCartRequest{})
	if err != nil {
		t.Fatalf("GetCart on rawMemoryStore failed: %v", err)
	}
	if len(readResp.Cart.ShopGroups) != 1 || len(readResp.Cart.ShopGroups[0].Items) != 1 || readResp.Cart.ShopGroups[0].Items[0].SkuId != "sku-1" {
		t.Fatalf("expected buildCart to skip qty<=0 lines, got %+v", readResp.Cart)
	}
	if readResp.Cart.TotalItems != 2 || readResp.Cart.TotalVnd != 200000 {
		t.Fatalf("unexpected totals from rawMemoryStore: items=%d total_vnd=%d",
			readResp.Cart.TotalItems, readResp.Cart.TotalVnd)
	}

	// Adding qty=2 onto a corrupted -4 existing item must NOT produce -2; must store +2.
	addCorrupt, err := corruptSrv.AddItem(corruptCtx, &cartv1.AddItemRequest{SkuId: "sku-3", Quantity: 2})
	if err != nil {
		t.Fatalf("AddItem over negative existing qty failed: %v", err)
	}
	if rawStore.items["buyer-corrupt"]["sku-3"].Quantity != 2 {
		t.Fatalf("stored qty after adding 2 to -4 = %d, want 2", rawStore.items["buyer-corrupt"]["sku-3"].Quantity)
	}
	if addCorrupt.Cart.TotalItems != 4 || addCorrupt.Cart.TotalVnd != 240000 {
		t.Fatalf("unexpected totals after recovering sku-3: items=%d total_vnd=%d",
			addCorrupt.Cart.TotalItems, addCorrupt.Cart.TotalVnd)
	}

	// 4. Cart containing ONLY non-positive items returns an empty cart cleanly.
	onlyBadStore := &rawMemoryStore{
		items: map[string]map[string]store.Item{
			"buyer-only-bad": {
				"sku-2": {Quantity: 0, AddedPriceVND: 50000, Selected: true},
				"sku-3": {Quantity: -9, AddedPriceVND: 20000, Selected: true},
			},
		},
	}
	onlyBadSrv := New(onlyBadStore, prod, nil, nil)
	emptyResp, err := onlyBadSrv.GetCart(buyerCtx("buyer-only-bad"), &cartv1.GetCartRequest{})
	if err != nil {
		t.Fatalf("GetCart on all-corrupt cart failed: %v", err)
	}
	if len(emptyResp.Cart.ShopGroups) != 0 || emptyResp.Cart.TotalItems != 0 || emptyResp.Cart.TotalVnd != 0 {
		t.Fatalf("expected empty cart when all stored items <= 0, got %+v", emptyResp.Cart)
	}
}

func TestCartWithRedactedHiddenSKUs(t *testing.T) {
	// Redacted SKU shape matches ProductService.GetSKUsBatch D2 output for non-owner/non-admin callers:
	// only id, product_id, shop_id, product_name, name, image_url, is_active=false; all price/stock/weight fields are 0.
	redactedSKU := func(id, prodID, shopID, prodName, name string) *productv1.SKU {
		return &productv1.SKU{
			Id:          id,
			ProductId:   prodID,
			ShopId:      shopID,
			ProductName: prodName,
			Name:        name,
			ImageUrl:    "https://cdn.local/" + id + ".jpg",
			IsActive:    false,
		}
	}

	prod := &fakeProductClient{
		skus: map[string]*productv1.SKU{
			"sku-active-1": {
				Id: "sku-active-1", ProductId: "p-1", ShopId: "shop-a",
				ProductName: "Active Shirt", Name: "M", PriceVnd: 150000,
				AvailableStock: 10, WeightGrams: 250, IsActive: true,
			},
			"sku-will-hide": {
				Id: "sku-will-hide", ProductId: "p-2", ShopId: "shop-b",
				ProductName: "Soon Hidden Jacket", Name: "L", PriceVnd: 400000,
				AvailableStock: 5, WeightGrams: 500, IsActive: true,
			},
			"sku-redacted": redactedSKU("sku-redacted", "p-3", "shop-c", "Pending Item", "Default"),
			"sku-zero-price": {
				Id: "sku-zero-price", ProductId: "p-4", ShopId: "shop-d",
				ProductName: "Corrupt Zero Price", Name: "Std", PriceVnd: 0,
				AvailableStock: 5, WeightGrams: 200, IsActive: true,
			},
		},
	}

	srv, mr := newTestCartServer(t, prod, nil)
	ctx := buyerCtx("buyer-d2")

	// 1. AddItem on redacted hidden SKU -> FailedPrecondition with SKU_NOT_ACTIVE (NOT OUT_OF_STOCK) and not persisted.
	_, err := srv.AddItem(ctx, &cartv1.AddItemRequest{SkuId: "sku-redacted", Quantity: 1})
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "SKU_NOT_ACTIVE") {
		t.Fatalf("AddItem redacted SKU: want FailedPrecondition SKU_NOT_ACTIVE, got %v", err)
	}
	if mr.HGet("cart:buyer-d2", "sku-redacted") != "" {
		t.Fatalf("redacted SKU must not be stored in Redis cart")
	}

	// 2. Add two active SKUs from different shops, then redact sku-will-hide (simulating product hidden/deleted/deactivated).
	if _, err := srv.AddItem(ctx, &cartv1.AddItemRequest{SkuId: "sku-active-1", Quantity: 2}); err != nil {
		t.Fatalf("AddItem sku-active-1: %v", err)
	}
	if _, err := srv.AddItem(ctx, &cartv1.AddItemRequest{SkuId: "sku-will-hide", Quantity: 1}); err != nil {
		t.Fatalf("AddItem sku-will-hide: %v", err)
	}

	prod.mu.Lock()
	prod.skus["sku-will-hide"] = redactedSKU("sku-will-hide", "p-2", "shop-b", "Soon Hidden Jacket", "L")
	prod.mu.Unlock()

	// 3. UpdateItemQty > 0 on now-redacted SKU -> FailedPrecondition SKU_NOT_ACTIVE (NOT OUT_OF_STOCK).
	_, err = srv.UpdateItemQty(ctx, &cartv1.UpdateItemQtyRequest{SkuId: "sku-will-hide", Quantity: 3})
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "SKU_NOT_ACTIVE") {
		t.Fatalf("UpdateItemQty redacted SKU: want FailedPrecondition SKU_NOT_ACTIVE, got %v", err)
	}

	// 4. GetCart / buildCart skips redacted SKU without error, omits its empty shop group, and never shows price 0.
	gotCart, err := srv.GetCart(ctx, &cartv1.GetCartRequest{})
	if err != nil {
		t.Fatalf("GetCart with redacted SKU failed: %v", err)
	}
	if len(gotCart.Cart.ShopGroups) != 1 || gotCart.Cart.ShopGroups[0].ShopId != "shop-a" ||
		gotCart.Cart.TotalItems != 2 || gotCart.Cart.TotalVnd != 300000 {
		t.Fatalf("unexpected cart with redacted SKU: %+v", gotCart.Cart)
	}
	for _, g := range gotCart.Cart.ShopGroups {
		for _, it := range g.Items {
			if it.PriceVnd <= 0 {
				t.Fatalf("cart item leaked non-positive price: %+v", it)
			}
		}
	}

	// 5. Adding another unit of active SKU while redacted SKU is still in Redis succeeds cleanly without 0-price items.
	addAgain, err := srv.AddItem(ctx, &cartv1.AddItemRequest{SkuId: "sku-active-1", Quantity: 1})
	if err != nil {
		t.Fatalf("AddItem active while redacted SKU in cart: %v", err)
	}
	if addAgain.Cart.TotalItems != 3 || addAgain.Cart.TotalVnd != 450000 || len(addAgain.Cart.ShopGroups) != 1 {
		t.Fatalf("unexpected cart after AddItem alongside redacted SKU: %+v", addAgain.Cart)
	}

	// 6. UpdateItemQty(0) and RemoveItem on redacted SKU succeed cleanly.
	if _, err := srv.UpdateItemQty(ctx, &cartv1.UpdateItemQtyRequest{SkuId: "sku-will-hide", Quantity: 0}); err != nil {
		t.Fatalf("UpdateItemQty(0) on redacted SKU failed: %v", err)
	}
	if mr.HGet("cart:buyer-d2", "sku-will-hide") != "" {
		t.Fatalf("expected sku-will-hide removed from Redis on qty=0")
	}

	// 7. When ALL items in cart become redacted, GetCart returns empty cart (so from_cart checkout never sees price 0).
	prod.mu.Lock()
	prod.skus["sku-active-1"] = redactedSKU("sku-active-1", "p-1", "shop-a", "Active Shirt", "M")
	prod.mu.Unlock()
	allRedacted, err := srv.GetCart(ctx, &cartv1.GetCartRequest{})
	if err != nil {
		t.Fatalf("GetCart when all SKUs redacted failed: %v", err)
	}
	if len(allRedacted.Cart.ShopGroups) != 0 || allRedacted.Cart.TotalItems != 0 || allRedacted.Cart.TotalVnd != 0 {
		t.Fatalf("expected empty cart when all SKUs redacted, got %+v", allRedacted.Cart)
	}
	// RemoveItem on redacted SKU also works cleanly.
	if _, err := srv.RemoveItem(ctx, &cartv1.RemoveItemRequest{SkuId: "sku-active-1"}); err != nil {
		t.Fatalf("RemoveItem redacted SKU failed: %v", err)
	}

	// 8. Defensive price<=0 guard: even if upstream returns is_active=true with price_vnd=0,
	// AddItem and UpdateItemQty reject it and buildCart never emits a 0-price item.
	if _, err := srv.AddItem(ctx, &cartv1.AddItemRequest{SkuId: "sku-zero-price", Quantity: 1}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("AddItem price=0 SKU: want FailedPrecondition, got %v", err)
	}
	if _, err := srv.UpdateItemQty(ctx, &cartv1.UpdateItemQtyRequest{SkuId: "sku-zero-price", Quantity: 2}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("UpdateItemQty price=0 SKU: want FailedPrecondition, got %v", err)
	}
	mr.HSet("cart:buyer-d2", "sku-zero-price", `{"quantity":2,"added_price_vnd":10000,"selected":true}`)
	zeroCart, err := srv.GetCart(ctx, &cartv1.GetCartRequest{})
	if err != nil {
		t.Fatalf("GetCart with zero-price SKU failed: %v", err)
	}
	if len(zeroCart.Cart.ShopGroups) != 0 || zeroCart.Cart.TotalItems != 0 || zeroCart.Cart.TotalVnd != 0 {
		t.Fatalf("zero-price SKU must never appear in cart, got %+v", zeroCart.Cart)
	}
}
