package server

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/product/internal/store"
)

// TestGRPCTrust_SpoofedMetadataAndServiceTokens exercises the real gRPC stack
// (auth interceptor + ProductService) the way cmd/product wires it.
func TestGRPCTrust_SpoofedMetadataAndServiceTokens(t *testing.T) {
	users, _ := auth.NewManager("product-test-user-key", 15*time.Minute)
	svc, _ := auth.NewManager("product-test-service-key", auth.ServiceTokenTTL)

	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer(grpc.ChainUnaryInterceptor(auth.GRPCUnaryServerInterceptor(users, svc)))
	productv1.RegisterProductServiceServer(gs, New(store.NewMemoryStore(), nil))
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := productv1.NewProductServiceClient(conn)

	md := func(kv ...string) context.Context {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		t.Cleanup(cancel)
		return metadata.NewOutgoingContext(ctx, metadata.Pairs(kv...))
	}
	bearer := func(tok string, kv ...string) context.Context {
		return md(append(kv, "authorization", "Bearer "+tok)...)
	}

	// 1. Admin RPC with forged admin metadata, no JWT -> Unauthenticated.
	_, err = client.CreateCategory(md("x-user-id", "attacker", "x-user-role", "admin"),
		&productv1.CreateCategoryRequest{Name: "Forged", CommissionRateBps: 100})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("forged admin metadata: want Unauthenticated, got %v", err)
	}

	adminTok, _, _, _ := users.SignAccessToken("admin-1", "admin", "")
	cat, err := client.CreateCategory(bearer(adminTok), &productv1.CreateCategoryRequest{Name: "Books", CommissionRateBps: 400})
	if err != nil {
		t.Fatalf("CreateCategory with admin JWT: %v", err)
	}

	newProduct := func(shopID string) *productv1.CreateProductRequest {
		return &productv1.CreateProductRequest{
			ShopId:     shopID,
			CategoryId: cat.GetCategory().GetId(),
			Name:       "Trust Book",
			Skus:       []*productv1.SKUInput{{SkuCode: "T-1", PriceVnd: 100000, Stock: 3}},
		}
	}

	// 2. Shop RPC with forged seller/shop metadata, no JWT -> Unauthenticated.
	_, err = client.CreateProduct(md("x-user-id", "seller-b", "x-user-role", "seller", "x-shop-id", "shop-b"), newProduct(""))
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("forged seller metadata: want Unauthenticated, got %v", err)
	}

	// 3. Valid shop-A JWT + x-shop-id of shop B: metadata ignored, product lands in shop A.
	sellerATok, _, _, _ := users.SignAccessToken("seller-a", "seller", "shop-a")
	created, err := client.CreateProduct(bearer(sellerATok, "x-shop-id", "shop-b", "x-user-role", "admin"), newProduct(""))
	if err != nil {
		t.Fatalf("CreateProduct with shop-A JWT: %v", err)
	}
	if got := created.GetProduct().GetShopId(); got != "shop-a" {
		t.Fatalf("product shop = %q, want shop-a (x-shop-id must be ignored)", got)
	}
	// ... and explicitly targeting shop B in the request body is refused.
	if _, err = client.CreateProduct(bearer(sellerATok, "x-shop-id", "shop-b"), newProduct("shop-b")); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("shop-A JWT creating in shop-b: want PermissionDenied, got %v", err)
	}

	// 4. Service tokens & caller RBAC on ReleaseStock and ConfirmStock.
	buyerTok, _, _, _ := users.SignAccessToken("buyer-1", "buyer", "")
	release := func(ctx context.Context) error {
		_, err := client.ReleaseStock(ctx, &productv1.ReleaseStockRequest{OrderId: "grp-trust-1"})
		return err
	}
	confirm := func(ctx context.Context) error {
		_, err := client.ConfirmStock(ctx, &productv1.ConfirmStockRequest{OrderId: "grp-trust-missing"})
		return err
	}
	wrongKey, _ := auth.NewManager("attacker-guessed-key", auth.ServiceTokenTTL)
	forged, _ := wrongKey.SignServiceToken("order-service")
	expiredMgr, _ := auth.NewManager("product-test-service-key", time.Millisecond)
	expired, _ := expiredMgr.SignServiceToken("order-service")
	time.Sleep(10 * time.Millisecond)
	for name, ctx := range map[string]context.Context{
		"no token":  md("x-user-id", "order-service", "x-user-role", "admin"),
		"wrong key": bearer(forged),
		"expired":   bearer(expired),
	} {
		if err := release(ctx); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("ReleaseStock with %s: want Unauthenticated, got %v", name, err)
		}
		if err := confirm(ctx); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("ConfirmStock with %s: want Unauthenticated, got %v", name, err)
		}
	}
	for name, ctx := range map[string]context.Context{
		"buyer JWT":  bearer(buyerTok),
		"seller JWT": bearer(sellerATok),
	} {
		if err := release(ctx); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("ReleaseStock with %s: want PermissionDenied, got %v", name, err)
		}
		if err := confirm(ctx); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("ConfirmStock with %s: want PermissionDenied, got %v", name, err)
		}
	}
	good, _ := svc.SignServiceToken("order-service")
	if err := release(bearer(good)); status.Code(err) == codes.Unauthenticated || status.Code(err) == codes.PermissionDenied {
		t.Fatalf("ReleaseStock with valid service token rejected: %v", err)
	}
	if err := release(bearer(adminTok)); status.Code(err) == codes.Unauthenticated || status.Code(err) == codes.PermissionDenied {
		t.Fatalf("ReleaseStock with admin JWT rejected: %v", err)
	}
	if err := confirm(bearer(good)); status.Code(err) == codes.Unauthenticated || status.Code(err) == codes.PermissionDenied {
		t.Fatalf("ConfirmStock with valid service token rejected: %v", err)
	}
	if err := confirm(bearer(adminTok)); status.Code(err) == codes.Unauthenticated || status.Code(err) == codes.PermissionDenied {
		t.Fatalf("ConfirmStock with admin JWT rejected: %v", err)
	}
	// Service identity is not an admin outside the internal RPC allowlist.
	if _, err = client.CreateCategory(bearer(good), &productv1.CreateCategoryRequest{Name: "Svc", CommissionRateBps: 1}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service token on admin RPC: want PermissionDenied, got %v", err)
	}

	// 5. RestockOrderItems: internal RPC allowing service tokens & admin, rejecting anonymous & seller.
	skuID := created.GetProduct().GetSkus()[0].GetId()
	restockReq := &productv1.RestockOrderItemsRequest{
		OrderId: "sub-order-trust-1",
		Items:   []*productv1.StockReservationItem{{SkuId: skuID, Quantity: 2}},
	}
	for name, ctx := range map[string]context.Context{
		"no token":  md("x-user-id", "order-service", "x-user-role", "admin"),
		"wrong key": bearer(forged),
		"expired":   bearer(expired),
	} {
		if _, err := client.RestockOrderItems(ctx, restockReq); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("RestockOrderItems with %s: want Unauthenticated, got %v", name, err)
		}
	}
	if _, err := client.RestockOrderItems(bearer(sellerATok), restockReq); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("RestockOrderItems with seller JWT: want PermissionDenied, got %v", err)
	}
	rstResp, err := client.RestockOrderItems(bearer(good), restockReq)
	if err != nil || !rstResp.GetRestocked() || rstResp.GetAlreadyRestocked() {
		t.Fatalf("RestockOrderItems with valid service token: resp=%+v err=%v", rstResp, err)
	}
	rstDup, err := client.RestockOrderItems(bearer(good), restockReq)
	if err != nil || !rstDup.GetRestocked() || !rstDup.GetAlreadyRestocked() {
		t.Fatalf("duplicate RestockOrderItems with valid service token: resp=%+v err=%v", rstDup, err)
	}
	if _, err := client.RestockOrderItems(bearer(adminTok), &productv1.RestockOrderItemsRequest{
		OrderId: "sub-order-trust-2",
		Items:   []*productv1.StockReservationItem{{SkuId: skuID, Quantity: 1}},
	}); err != nil {
		t.Fatalf("RestockOrderItems with admin JWT failed: %v", err)
	}
}
