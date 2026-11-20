package grpcserver

import (
	"context"
	"io"
	"log/slog"
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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
