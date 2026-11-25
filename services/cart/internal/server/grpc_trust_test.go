package server

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	cartv1 "github.com/nexusmart/core/gen/go/nexusmart/cart/v1"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/cart/internal/store"
)

// TestGRPCTrust_CartService_B1 exercises the real gRPC stack (auth interceptor + CartService)
// the way cmd/cart wires it, proving every Cart RPC rejects anonymous, forged-metadata,
// invalid-token, service-token, and cross-user callers.
func TestGRPCTrust_CartService_B1(t *testing.T) {
	users, err := auth.NewManager("cart-test-user-key", 15*time.Minute)
	if err != nil {
		t.Fatalf("new user manager: %v", err)
	}
	svc, err := auth.NewManager("cart-test-service-key", auth.ServiceTokenTTL)
	if err != nil {
		t.Fatalf("new service manager: %v", err)
	}

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	prod := &fakeProductClient{
		skus: map[string]*productv1.SKU{
			"sku-a1": {Id: "sku-a1", ProductId: "p1", ShopId: "shop-a", ProductName: "A1", Name: "M", PriceVnd: 100000, AvailableStock: 10, WeightGrams: 200, IsActive: true},
			"sku-b1": {Id: "sku-b1", ProductId: "p2", ShopId: "shop-b", ProductName: "B1", Name: "L", PriceVnd: 200000, AvailableStock: 10, WeightGrams: 300, IsActive: true},
		},
	}

	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer(grpc.ChainUnaryInterceptor(auth.GRPCUnaryServerInterceptor(users, svc)))
	cartv1.RegisterCartServiceServer(gs, New(store.NewRedisStore(rdb), prod, nil, nil))
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := cartv1.NewCartServiceClient(conn)

	md := func(kv ...string) context.Context {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		t.Cleanup(cancel)
		return metadata.NewOutgoingContext(ctx, metadata.Pairs(kv...))
	}
	bearer := func(tok string, kv ...string) context.Context {
		return md(append(kv, "authorization", "Bearer "+tok)...)
	}

	buyerATok, _, _, _ := users.SignAccessToken("buyer-a", "buyer", "")
	buyerBTok, _, _, _ := users.SignAccessToken("buyer-b", "buyer", "")
	adminTok, _, _, _ := users.SignAccessToken("admin-1", "admin", "")
	svcTok, _ := svc.SignServiceToken("order-service")

	wrongKeyMgr, _ := auth.NewManager("attacker-wrong-key", 15*time.Minute)
	wrongKeyTok, _, _, _ := wrongKeyMgr.SignAccessToken("buyer-a", "buyer", "")
	expiredMgr, _ := auth.NewManager("cart-test-user-key", time.Millisecond)
	expiredTok, _, _, _ := expiredMgr.SignAccessToken("buyer-a", "buyer", "")
	time.Sleep(10 * time.Millisecond)

	// Seed buyer-a cart legitimately.
	if _, err := client.AddItem(bearer(buyerATok), &cartv1.AddItemRequest{SkuId: "sku-a1", Quantity: 2}); err != nil {
		t.Fatalf("seed buyer-a cart: %v", err)
	}
	if _, err := client.AddItem(bearer(buyerATok), &cartv1.AddItemRequest{SkuId: "sku-b1", Quantity: 1}); err != nil {
		t.Fatalf("seed buyer-a cart sku-b1: %v", err)
	}

	// 1. Anonymous, forged metadata, wrong-key, and expired tokens -> Unauthenticated across ALL 5 Cart RPCs.
	badAuthCases := map[string]context.Context{
		"anonymous":      md(),
		"forged_admin":   md("x-user-id", "buyer-a", "x-user-role", "admin"),
		"forged_buyer":   md("x-user-id", "buyer-a", "x-user-role", "buyer"),
		"wrong_key_jwt":  bearer(wrongKeyTok),
		"expired_jwt":    bearer(expiredTok),
		"garbage_bearer": bearer("not-a-jwt"),
	}
	for name, ctx := range badAuthCases {
		if _, err := client.GetCart(ctx, &cartv1.GetCartRequest{UserId: "buyer-a"}); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("GetCart(%s): want Unauthenticated, got %v", name, err)
		}
		if _, err := client.AddItem(ctx, &cartv1.AddItemRequest{UserId: "buyer-a", SkuId: "sku-a1", Quantity: 1}); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("AddItem(%s): want Unauthenticated, got %v", name, err)
		}
		if _, err := client.UpdateItemQty(ctx, &cartv1.UpdateItemQtyRequest{UserId: "buyer-a", SkuId: "sku-a1", Quantity: 1}); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("UpdateItemQty(%s): want Unauthenticated, got %v", name, err)
		}
		if _, err := client.RemoveItem(ctx, &cartv1.RemoveItemRequest{UserId: "buyer-a", SkuId: "sku-a1"}); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("RemoveItem(%s): want Unauthenticated, got %v", name, err)
		}
		if _, err := client.ClearItems(ctx, &cartv1.ClearItemsRequest{UserId: "buyer-a"}); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("ClearItems(%s): want Unauthenticated, got %v", name, err)
		}
	}

	// 2. Service token is rejected with PermissionDenied on all 5 Cart RPCs (internalRPCs=false).
	if _, err := client.GetCart(bearer(svcTok), &cartv1.GetCartRequest{UserId: "buyer-a"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("GetCart(service token): want PermissionDenied, got %v", err)
	}
	if _, err := client.AddItem(bearer(svcTok), &cartv1.AddItemRequest{UserId: "buyer-a", SkuId: "sku-a1", Quantity: 1}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("AddItem(service token): want PermissionDenied, got %v", err)
	}
	if _, err := client.UpdateItemQty(bearer(svcTok), &cartv1.UpdateItemQtyRequest{UserId: "buyer-a", SkuId: "sku-a1", Quantity: 1}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("UpdateItemQty(service token): want PermissionDenied, got %v", err)
	}
	if _, err := client.RemoveItem(bearer(svcTok), &cartv1.RemoveItemRequest{UserId: "buyer-a", SkuId: "sku-a1"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("RemoveItem(service token): want PermissionDenied, got %v", err)
	}
	if _, err := client.ClearItems(bearer(svcTok), &cartv1.ClearItemsRequest{UserId: "buyer-a"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("ClearItems(service token): want PermissionDenied, got %v", err)
	}

	// 3. Cross-user calls (buyer-b targeting buyer-a, even with forged x-user-role: admin) -> PermissionDenied across ALL 5 RPCs.
	crossCtx := bearer(buyerBTok, "x-user-id", "buyer-a", "x-user-role", "admin")
	if _, err := client.GetCart(crossCtx, &cartv1.GetCartRequest{UserId: "buyer-a"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cross-user GetCart: want PermissionDenied, got %v", err)
	}
	if _, err := client.AddItem(crossCtx, &cartv1.AddItemRequest{UserId: "buyer-a", SkuId: "sku-a1", Quantity: 1}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cross-user AddItem: want PermissionDenied, got %v", err)
	}
	if _, err := client.UpdateItemQty(crossCtx, &cartv1.UpdateItemQtyRequest{UserId: "buyer-a", SkuId: "sku-a1", Quantity: 1}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cross-user UpdateItemQty: want PermissionDenied, got %v", err)
	}
	if _, err := client.RemoveItem(crossCtx, &cartv1.RemoveItemRequest{UserId: "buyer-a", SkuId: "sku-a1"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cross-user RemoveItem: want PermissionDenied, got %v", err)
	}
	if _, err := client.ClearItems(crossCtx, &cartv1.ClearItemsRequest{UserId: "buyer-a", SkuIds: []string{"sku-a1"}}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cross-user ClearItems: want PermissionDenied, got %v", err)
	}

	// 4. Verify buyer-a's cart is completely untouched after all attacks, and shop filter works over gRPC.
	filtered, err := client.GetCart(bearer(buyerATok), &cartv1.GetCartRequest{ShopId: "shop-a"})
	if err != nil {
		t.Fatalf("buyer-a GetCart(shop-a): %v", err)
	}
	if len(filtered.GetCart().GetShopGroups()) != 1 || filtered.GetCart().GetTotalItems() != 2 {
		t.Fatalf("expected only shop-a (2 items), got %+v", filtered.GetCart())
	}

	// 5. Admin JWT can clear buyer-a's shop-a items, leaving shop-b intact.
	if _, err := client.ClearItems(bearer(adminTok), &cartv1.ClearItemsRequest{UserId: "buyer-a", ShopId: "shop-a"}); err != nil {
		t.Fatalf("admin ClearItems(buyer-a, shop-a): %v", err)
	}
	remaining, err := client.GetCart(bearer(buyerATok), &cartv1.GetCartRequest{})
	if err != nil {
		t.Fatalf("buyer-a GetCart after admin shop clear: %v", err)
	}
	if len(remaining.GetCart().GetShopGroups()) != 1 || remaining.GetCart().GetShopGroups()[0].GetShopId() != "shop-b" {
		t.Fatalf("expected only shop-b remaining, got %+v", remaining.GetCart())
	}
}
