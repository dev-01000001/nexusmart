package grpcserver

import (
	"context"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/shipping/internal/address"
	"github.com/nexusmart/core/services/shipping/internal/carrier"
	"github.com/nexusmart/core/services/shipping/internal/service"
	"github.com/nexusmart/core/services/shipping/internal/store"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type stubRepo struct {
	mu        sync.Mutex
	shipments map[string]*store.Shipment
}

func newStubRepo() *stubRepo {
	return &stubRepo{shipments: make(map[string]*store.Shipment)}
}

func (r *stubRepo) ListProvinces(_ context.Context, _ string) ([]address.Province, error) {
	return nil, nil
}
func (r *stubRepo) ListWards(_ context.Context, _, _ string) ([]address.Ward, error) {
	return nil, nil
}
func (r *stubRepo) GetWard(_ context.Context, _ string) (*address.Ward, error) {
	return nil, store.ErrNotFound
}
func (r *stubRepo) MapLegacyAddress(_ context.Context, _, _, _ string) (*address.LegacyMatch, error) {
	return &address.LegacyMatch{}, nil
}
func (r *stubRepo) GetShipmentByOrderID(_ context.Context, orderID string) (*store.Shipment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sh, ok := r.shipments[orderID]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *sh
	return &cp, nil
}
func (r *stubRepo) GetShipmentByTrackingCode(_ context.Context, trackingCode string) (*store.Shipment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, sh := range r.shipments {
		if sh.TrackingCode == trackingCode {
			cp := *sh
			return &cp, nil
		}
	}
	return nil, store.ErrNotFound
}
func (r *stubRepo) ListShipmentEvents(_ context.Context, _ string) ([]store.ShipmentEvent, error) {
	return nil, nil
}
func (r *stubRepo) CreateShipment(_ context.Context, sh *store.Shipment, _ *store.ShipmentEvent) (*store.Shipment, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.shipments[sh.OrderID]; ok {
		cp := *existing
		return &cp, true, nil
	}
	cp := *sh
	r.shipments[sh.OrderID] = &cp
	ret := cp
	return &ret, false, nil
}
func (r *stubRepo) CancelShipment(_ context.Context, orderID, _ string) (*store.Shipment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sh, ok := r.shipments[orderID]
	if !ok {
		return nil, store.ErrNotFound
	}
	if sh.Status == carrier.StatusCancelled {
		cp := *sh
		return &cp, nil
	}
	if !carrier.IsCancellableStatus(sh.Status) {
		return nil, store.ErrCannotCancel
	}
	sh.Status = carrier.StatusCancelled
	sh.UpdatedAt = time.Now().UTC()
	cp := *sh
	return &cp, nil
}
func (r *stubRepo) CancelShipmentWithEvent(_ context.Context, _, _, _, _ string) (*store.Shipment, bool, error) {
	return nil, false, nil
}
func (r *stubRepo) ApplyWebhookEvent(_ context.Context, _ store.WebhookUpdateParams) (*store.Shipment, store.WebhookUpdateOutcome, error) {
	return nil, store.WebhookOutcomeApplied, nil
}
func (r *stubRepo) ListActiveShipments(_ context.Context, _ int) ([]store.Shipment, error) {
	return nil, nil
}
func (r *stubRepo) FetchPendingOutbox(_ context.Context, _ int) ([]store.OutboxEvent, error) {
	return nil, nil
}
func (r *stubRepo) MarkOutboxPublished(_ context.Context, _ string) error { return nil }
func (r *stubRepo) IsEventProcessed(_ context.Context, _ string) (bool, error) {
	return false, nil
}
func (r *stubRepo) MarkEventProcessed(_ context.Context, _, _ string) (bool, error) {
	return true, nil
}
func (r *stubRepo) InsertDeadLetter(_ context.Context, _, _ string, _ []byte, _ string, _ int) error {
	return nil
}

func ctxWithRole(userID, role, shopID string) context.Context {
	return auth.WithClaims(context.Background(), &auth.Claims{
		Role:   role,
		ShopID: shopID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: userID,
		},
	})
}

func TestShippingGRPCRBACAndShopOwnership(t *testing.T) {
	repo := newStubRepo()
	agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), carrier.NewGHNAdapter(carrier.GHNConfig{Mode: "mock"}))
	svc := service.NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := New(svc)

	buyerCtx := ctxWithRole("buyer-1", "buyer", "")
	sellerEmptyShopCtx := ctxWithRole("seller-x", "seller", "")
	sellerOtherShopCtx := ctxWithRole("seller-2", "seller", "shop-2")
	sellerOwnerCtx := ctxWithRole("seller-1", "seller", "shop-1")
	adminCtx := ctxWithRole("admin-1", "admin", "")

	createReq := &shippingv1.CreateShipmentRequest{
		OrderId:     "ord-rbac-1",
		ShopId:      "shop-1",
		CarrierCode: "ghn",
		WeightGrams: 500,
	}

	// 1. CreateShipment RBAC: buyer, seller with empty shop_id, and seller with mismatched shop_id are denied.
	if _, err := srv.CreateShipment(buyerCtx, createReq); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for buyer CreateShipment, got %v", err)
	}
	if _, err := srv.CreateShipment(sellerEmptyShopCtx, createReq); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for seller with empty shop_id CreateShipment, got %v", err)
	}
	if _, err := srv.CreateShipment(sellerOtherShopCtx, createReq); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for mismatched seller CreateShipment, got %v", err)
	}

	// Owner seller succeeds.
	if _, err := srv.CreateShipment(sellerOwnerCtx, createReq); err != nil {
		t.Fatalf("expected owner seller CreateShipment to succeed, got %v", err)
	}

	// 2. GetShipment RBAC: seller with empty or mismatched shop_id is denied; owner seller and admin succeed.
	getReq := &shippingv1.GetShipmentRequest{OrderId: "ord-rbac-1"}
	if _, err := srv.GetShipment(sellerEmptyShopCtx, getReq); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for seller with empty shop_id GetShipment, got %v", err)
	}
	if _, err := srv.GetShipment(sellerOtherShopCtx, getReq); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for mismatched seller GetShipment, got %v", err)
	}
	if _, err := srv.GetShipment(sellerOwnerCtx, getReq); err != nil {
		t.Fatalf("expected owner seller GetShipment to succeed, got %v", err)
	}
	if _, err := srv.GetShipment(adminCtx, getReq); err != nil {
		t.Fatalf("expected admin GetShipment to succeed, got %v", err)
	}

	// 3. CancelShipment RBAC: buyer, seller with empty shop_id, and seller with mismatched shop_id are denied.
	cancelReq := &shippingv1.CancelShipmentRequest{OrderId: "ord-rbac-1", Reason: "test"}
	if _, err := srv.CancelShipment(buyerCtx, cancelReq); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for buyer CancelShipment, got %v", err)
	}
	if _, err := srv.CancelShipment(sellerEmptyShopCtx, cancelReq); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for seller with empty shop_id CancelShipment, got %v", err)
	}
	if _, err := srv.CancelShipment(sellerOtherShopCtx, cancelReq); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for mismatched seller CancelShipment, got %v", err)
	}

	// Owner seller succeeds.
	if resp, err := srv.CancelShipment(sellerOwnerCtx, cancelReq); err != nil || resp.GetShipment().GetStatus() != shippingv1.ShipmentStatus_SHIPMENT_STATUS_CANCELLED {
		t.Fatalf("expected owner seller CancelShipment to succeed, got resp=%+v err=%v", resp, err)
	}
}

func TestShippingGRPCTrust_B1RejectsAnonymousAndServiceTokens(t *testing.T) {
	repo := newStubRepo()
	agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), carrier.NewGHNAdapter(carrier.GHNConfig{Mode: "mock"}))
	svc := service.NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := New(svc)

	// Direct handler calls without claims must NEVER be treated as internal/trusted.
	createReq := &shippingv1.CreateShipmentRequest{
		OrderId:     "ord-b1-1",
		ShopId:      "shop-1",
		CarrierCode: "ghn",
		WeightGrams: 500,
	}
	if _, err := srv.CreateShipment(context.Background(), createReq); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("direct anonymous CreateShipment: want Unauthenticated, got %v", err)
	}
	if _, err := srv.CancelShipment(context.Background(), &shippingv1.CancelShipmentRequest{OrderId: "ord-b1-1"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("direct anonymous CancelShipment: want Unauthenticated, got %v", err)
	}
	if _, err := srv.GetShipment(context.Background(), &shippingv1.GetShipmentRequest{OrderId: "ord-b1-1"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("direct anonymous GetShipment: want Unauthenticated, got %v", err)
	}

	// Full gRPC bufconn stack with auth.GRPCUnaryServerInterceptor.
	users, _ := auth.NewManager("shipping-test-user-key", 15*time.Minute)
	svcMgr, _ := auth.NewManager("shipping-test-service-key", auth.ServiceTokenTTL)

	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer(grpc.ChainUnaryInterceptor(auth.GRPCUnaryServerInterceptor(users, svcMgr)))
	shippingv1.RegisterShippingServiceServer(gs, srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := shippingv1.NewShippingServiceClient(conn)

	md := func(kv ...string) context.Context {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		t.Cleanup(cancel)
		return metadata.NewOutgoingContext(ctx, metadata.Pairs(kv...))
	}
	bearer := func(tok string, kv ...string) context.Context {
		return md(append(kv, "authorization", "Bearer "+tok)...)
	}

	buyerTok, _, _, _ := users.SignAccessToken("buyer-1", "buyer", "")
	seller1Tok, _, _, _ := users.SignAccessToken("seller-1", "seller", "shop-1")
	seller2Tok, _, _, _ := users.SignAccessToken("seller-2", "seller", "shop-2")
	svcTok, _ := svcMgr.SignServiceToken("order-service")

	// Spoofed metadata without JWT -> Unauthenticated on all 3 RPCs.
	spoof := []string{"x-user-id", "attacker", "x-user-role", "admin", "x-shop-id", "shop-1"}
	if _, err := client.CreateShipment(md(spoof...), createReq); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("bufconn anon CreateShipment: want Unauthenticated, got %v", err)
	}
	if _, err := client.CancelShipment(md(spoof...), &shippingv1.CancelShipmentRequest{OrderId: "ord-b1-1"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("bufconn anon CancelShipment: want Unauthenticated, got %v", err)
	}
	if _, err := client.GetShipment(md(spoof...), &shippingv1.GetShipmentRequest{OrderId: "ord-b1-1"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("bufconn anon GetShipment: want Unauthenticated, got %v", err)
	}

	// Service tokens are disallowed on CreateShipment/CancelShipment/GetShipment -> PermissionDenied.
	if _, err := client.CreateShipment(bearer(svcTok), createReq); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service token CreateShipment: want PermissionDenied, got %v", err)
	}
	if _, err := client.CancelShipment(bearer(svcTok), &shippingv1.CancelShipmentRequest{OrderId: "ord-b1-1"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service token CancelShipment: want PermissionDenied, got %v", err)
	}
	if _, err := client.GetShipment(bearer(svcTok), &shippingv1.GetShipmentRequest{OrderId: "ord-b1-1"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service token GetShipment: want PermissionDenied, got %v", err)
	}

	// Buyer and mismatched seller rejected; owning seller succeeds.
	if _, err := client.CreateShipment(bearer(buyerTok), createReq); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("buyer CreateShipment: want PermissionDenied, got %v", err)
	}
	if _, err := client.CreateShipment(bearer(seller2Tok), createReq); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("other seller CreateShipment: want PermissionDenied, got %v", err)
	}
	if _, err := client.CreateShipment(bearer(seller1Tok), createReq); err != nil {
		t.Fatalf("owner seller CreateShipment: %v", err)
	}
	if _, err := client.GetShipment(bearer(seller2Tok), &shippingv1.GetShipmentRequest{OrderId: "ord-b1-1"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("other seller GetShipment: want PermissionDenied, got %v", err)
	}
	if _, err := client.GetShipment(bearer(seller1Tok), &shippingv1.GetShipmentRequest{OrderId: "ord-b1-1"}); err != nil {
		t.Fatalf("owner seller GetShipment: %v", err)
	}
	if _, err := client.CancelShipment(bearer(seller2Tok), &shippingv1.CancelShipmentRequest{OrderId: "ord-b1-1"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("other seller CancelShipment: want PermissionDenied, got %v", err)
	}
	if _, err := client.CancelShipment(bearer(seller1Tok), &shippingv1.CancelShipmentRequest{OrderId: "ord-b1-1"}); err != nil {
		t.Fatalf("owner seller CancelShipment: %v", err)
	}
}
