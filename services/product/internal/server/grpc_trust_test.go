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

	// 4. Service tokens.
	release := func(ctx context.Context) error {
		_, err := client.ReleaseStock(ctx, &productv1.ReleaseStockRequest{OrderId: "grp-trust-1"})
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
	}
	good, _ := svc.SignServiceToken("order-service")
	if err := release(bearer(good)); status.Code(err) == codes.Unauthenticated || status.Code(err) == codes.PermissionDenied {
		t.Fatalf("ReleaseStock with valid service token rejected: %v", err)
	}
	// Service identity is not an admin outside the internal RPC allowlist.
	if _, err = client.CreateCategory(bearer(good), &productv1.CreateCategoryRequest{Name: "Svc", CommissionRateBps: 1}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service token on admin RPC: want PermissionDenied, got %v", err)
	}

	// 5. Phase 1 B1: UpdateStock, ReserveStock, ReleaseStock, ConfirmStock strict rejection checks.
	skuID := created.GetProduct().GetSkus()[0].GetId()
	buyerTok, _, _, _ := users.SignAccessToken("buyer-1", "buyer", "")
	sellerBTok, _, _, _ := users.SignAccessToken("seller-b", "seller", "shop-b")

	// UpdateStock: anonymous -> Unauthenticated; buyer / other seller -> PermissionDenied; service token -> OK (restock).
	if _, err = client.UpdateStock(md("x-user-role", "admin", "x-shop-id", "shop-a"), &productv1.UpdateStockRequest{
		SkuId: skuID, ShopId: "shop-a", NewStock: 5, ExpectedVersion: 1,
	}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("UpdateStock anonymous: want Unauthenticated, got %v", err)
	}
	if _, err = client.UpdateStock(bearer(buyerTok), &productv1.UpdateStockRequest{
		SkuId: skuID, ShopId: "shop-a", NewStock: 5, ExpectedVersion: 1,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("UpdateStock buyer: want PermissionDenied, got %v", err)
	}
	if _, err = client.UpdateStock(bearer(sellerBTok), &productv1.UpdateStockRequest{
		SkuId: skuID, ShopId: "shop-a", NewStock: 5, ExpectedVersion: 1,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("UpdateStock other seller: want PermissionDenied, got %v", err)
	}
	if resp, err := client.UpdateStock(bearer(good), &productv1.UpdateStockRequest{
		SkuId: skuID, ShopId: "shop-a", NewStock: 6, ExpectedVersion: 1,
	}); err != nil || resp.GetSku().GetStock() != 6 {
		t.Fatalf("UpdateStock valid service token (order restock): want stock=6, got resp=%+v err=%v", resp, err)
	}

	// ReserveStock: anonymous -> Unauthenticated; service token -> PermissionDenied.
	if _, err = client.ReserveStock(md(), &productv1.ReserveStockRequest{
		OrderId: "ord-anon", Items: []*productv1.StockReservationItem{{SkuId: skuID, Quantity: 1}},
	}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("ReserveStock anonymous: want Unauthenticated, got %v", err)
	}
	if _, err = client.ReserveStock(bearer(good), &productv1.ReserveStockRequest{
		OrderId: "ord-svc", Items: []*productv1.StockReservationItem{{SkuId: skuID, Quantity: 1}},
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("ReserveStock service token: want PermissionDenied, got %v", err)
	}

	// ReleaseStock & ConfirmStock: buyer & seller JWTs must be rejected with PermissionDenied.
	for name, tok := range map[string]string{"buyer": buyerTok, "seller": sellerATok} {
		if _, err = client.ReleaseStock(bearer(tok), &productv1.ReleaseStockRequest{OrderId: "grp-trust-1"}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("ReleaseStock with %s JWT: want PermissionDenied, got %v", name, err)
		}
		if _, err = client.ConfirmStock(bearer(tok), &productv1.ConfirmStockRequest{OrderId: "grp-trust-1"}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("ConfirmStock with %s JWT: want PermissionDenied, got %v", name, err)
		}
	}
	if _, err = client.ConfirmStock(md(), &productv1.ConfirmStockRequest{OrderId: "grp-trust-1"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("ConfirmStock anonymous: want Unauthenticated, got %v", err)
	}
}
