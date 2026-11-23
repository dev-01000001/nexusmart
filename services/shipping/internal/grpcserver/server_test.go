package grpcserver

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
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
	mu           sync.Mutex
	shipments    []*store.Shipment
	events       map[string][]store.ShipmentEvent
	shopSettings map[string]*store.ShopShippingSettings
	carriers     map[string]*store.CarrierConfig
	fallback     map[string][]store.FallbackRate
}

func newStubRepo() *stubRepo {
	return &stubRepo{
		events:       make(map[string][]store.ShipmentEvent),
		shopSettings: make(map[string]*store.ShopShippingSettings),
		carriers:     make(map[string]*store.CarrierConfig),
		fallback:     make(map[string][]store.FallbackRate),
	}
}

func (r *stubRepo) ListProvinces(_ context.Context, _ string) ([]address.Province, error) {
	return address.Provinces(), nil
}
func (r *stubRepo) GetProvince(_ context.Context, provinceCode string) (*address.Province, error) {
	for _, p := range address.Provinces() {
		if p.Code == provinceCode {
			cp := p
			return &cp, nil
		}
	}
	return nil, store.ErrNotFound
}
func (r *stubRepo) ListWards(_ context.Context, _, _ string) ([]address.Ward, error) {
	return nil, nil
}
func (r *stubRepo) GetWard(_ context.Context, wardCode string) (*address.Ward, error) {
	for _, w := range address.Wards() {
		if w.Code == wardCode {
			cp := w
			return &cp, nil
		}
	}
	return nil, store.ErrNotFound
}
func (r *stubRepo) MapLegacyAddress(_ context.Context, _, _, _ string) (*address.LegacyMatch, error) {
	return &address.LegacyMatch{}, nil
}
func (r *stubRepo) GetShipmentByOrderID(_ context.Context, orderID, direction string) (*store.Shipment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	dir := store.NormalizeDirection(direction)
	var active *store.Shipment
	var inactive *store.Shipment
	for _, sh := range r.shipments {
		if sh.OrderID == orderID && sh.Direction == dir {
			if !store.IsInactiveShipment(dir, sh.Status) {
				if active == nil || !sh.CreatedAt.Before(active.CreatedAt) {
					active = sh
				}
			} else if inactive == nil || !sh.CreatedAt.Before(inactive.CreatedAt) {
				inactive = sh
			}
		}
	}
	if active != nil {
		cp := *active
		return &cp, nil
	}
	if inactive != nil {
		cp := *inactive
		return &cp, nil
	}
	return nil, store.ErrNotFound
}
func (r *stubRepo) GetActiveShipmentByOrderID(_ context.Context, orderID, direction string) (*store.Shipment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	dir := store.NormalizeDirection(direction)
	var active *store.Shipment
	for _, sh := range r.shipments {
		if sh.OrderID == orderID && sh.Direction == dir && !store.IsInactiveShipment(dir, sh.Status) {
			if active == nil || !sh.CreatedAt.Before(active.CreatedAt) {
				active = sh
			}
		}
	}
	if active != nil {
		cp := *active
		return &cp, nil
	}
	return nil, store.ErrNotFound
}
func (r *stubRepo) CountInactiveShipmentsByOrder(_ context.Context, orderID, direction string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	dir := store.NormalizeDirection(direction)
	count := 0
	for _, sh := range r.shipments {
		if sh.OrderID == orderID && sh.Direction == dir && store.IsInactiveShipment(dir, sh.Status) {
			count++
		}
	}
	return count, nil
}
func (r *stubRepo) AcquireOrderDirectionLock(_ context.Context, _, _ string) (func(), error) {
	return func() {}, nil
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
func (r *stubRepo) ListShipmentEvents(_ context.Context, shipmentID string) ([]store.ShipmentEvent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ev := r.events[shipmentID]
	out := make([]store.ShipmentEvent, len(ev))
	copy(out, ev)
	return out, nil
}
func (r *stubRepo) CreateShipment(_ context.Context, sh *store.Shipment, initialEvent *store.ShipmentEvent) (*store.Shipment, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	dir := store.NormalizeDirection(sh.Direction)
	for _, existing := range r.shipments {
		if existing.OrderID == sh.OrderID && existing.Direction == dir && !store.IsInactiveShipment(dir, existing.Status) {
			cp := *existing
			return &cp, true, nil
		}
	}
	cp := *sh
	cp.Direction = dir
	r.shipments = append(r.shipments, &cp)
	if initialEvent != nil {
		ev := *initialEvent
		if ev.CreatedAt.IsZero() {
			ev.CreatedAt = time.Now().UTC()
		}
		r.events[cp.ID] = append(r.events[cp.ID], ev)
	}
	ret := cp
	return &ret, false, nil
}
func (r *stubRepo) CancelShipmentByID(_ context.Context, shipmentID, _ string) (*store.Shipment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, sh := range r.shipments {
		if sh.ID == shipmentID {
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
	}
	return nil, store.ErrNotFound
}
func (r *stubRepo) CancelShipmentWithEvent(_ context.Context, _, _, _, _ string) (*store.Shipment, bool, error) {
	return nil, false, nil
}
func (r *stubRepo) RecordRedelivery(_ context.Context, shipmentID, buyerID string, maxRedelivery int32, preferredDate, note string) (*store.Shipment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, sh := range r.shipments {
		if sh.ID == shipmentID {
			if sh.BuyerID == "" {
				return nil, store.ErrLegacyNoBuyer
			}
			if sh.BuyerID != buyerID {
				return nil, store.ErrBuyerMismatch
			}
			if sh.Status != carrier.StatusFailed {
				return nil, store.ErrRedeliveryNotAllowed
			}
			if sh.RedeliveryCount >= maxRedelivery {
				return nil, store.ErrMaxRedeliveryExceeded
			}
			var (
				latestRaw     string
				latestCreated time.Time
				foundFailed   bool
			)
			for _, ev := range r.events[sh.ID] {
				if ev.Status != carrier.StatusFailed {
					continue
				}
				if !foundFailed || !ev.CreatedAt.Before(latestCreated) {
					latestRaw = ev.RawStatus
					latestCreated = ev.CreatedAt
					foundFailed = true
				}
			}
			if (foundFailed && latestRaw == "redelivery_requested") || (!foundFailed && sh.RedeliveryCount > 0) {
				return nil, store.ErrRedeliveryAlreadyRequested
			}
			now := time.Now().UTC()
			sh.RedeliveryCount++
			sh.UpdatedAt = now
			r.events[sh.ID] = append(r.events[sh.ID], store.ShipmentEvent{
				ID:          uuid.NewString(),
				ShipmentID:  sh.ID,
				Status:      carrier.StatusFailed,
				RawStatus:   "redelivery_requested",
				Description: store.FormatRedeliveryDescriptionVI(sh.RedeliveryCount, preferredDate, note),
				OccurredAt:  now,
				CreatedAt:   now,
			})
			cp := *sh
			return &cp, nil
		}
	}
	return nil, store.ErrNotFound
}
func (r *stubRepo) ApplyWebhookEvent(_ context.Context, _ store.WebhookUpdateParams) (*store.Shipment, store.WebhookUpdateOutcome, error) {
	return nil, store.WebhookOutcomeApplied, nil
}
func (r *stubRepo) ListActiveShipments(_ context.Context, _ int, _ *time.Time, _ string) ([]store.Shipment, error) {
	return nil, nil
}
func (r *stubRepo) GetShopShippingSettings(_ context.Context, shopID string) (*store.ShopShippingSettings, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok := r.shopSettings[shopID]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *st
	cp.EnabledCarrierCodes = append([]string(nil), st.EnabledCarrierCodes...)
	return &cp, nil
}
func (r *stubRepo) UpsertShopShippingSettings(_ context.Context, st *store.ShopShippingSettings) (*store.ShopShippingSettings, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *st
	cp.EnabledCarrierCodes = append([]string(nil), st.EnabledCarrierCodes...)
	cp.UpdatedAt = time.Now().UTC()
	r.shopSettings[st.ShopID] = &cp
	ret := cp
	return &ret, nil
}
func (r *stubRepo) ListCarrierConfigs(_ context.Context) ([]store.CarrierConfig, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]store.CarrierConfig, 0, len(r.carriers))
	for _, c := range r.carriers {
		out = append(out, *c)
	}
	return out, nil
}
func (r *stubRepo) GetCarrierConfig(_ context.Context, carrierCode string) (*store.CarrierConfig, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.carriers[strings.ToLower(carrierCode)]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *c
	return &cp, nil
}
func (r *stubRepo) UpdateCarrierConfig(_ context.Context, cfg *store.CarrierConfig, rates []store.FallbackRate) (*store.CarrierConfig, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *cfg
	cp.UpdatedAt = time.Now().UTC()
	r.carriers[strings.ToLower(cfg.CarrierCode)] = &cp
	if len(rates) > 0 {
		r.fallback[strings.ToLower(cfg.CarrierCode)] = append([]store.FallbackRate(nil), rates...)
	}
	ret := cp
	return &ret, nil
}
func (r *stubRepo) SeedCarrierTokenIfEmpty(_ context.Context, _ string, _ []byte) error { return nil }
func (r *stubRepo) ListFallbackRates(_ context.Context, carrierCode string) ([]store.FallbackRate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if carrierCode == "" {
		var all []store.FallbackRate
		for _, list := range r.fallback {
			all = append(all, list...)
		}
		return all, nil
	}
	return append([]store.FallbackRate(nil), r.fallback[strings.ToLower(carrierCode)]...), nil
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

func ctxWithService(serviceName string) context.Context {
	return auth.WithClaims(context.Background(), &auth.Claims{
		Role:    auth.ServiceRole,
		Service: serviceName,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: serviceName,
		},
	})
}

func TestShippingGRPCRBACAndShopOwnership(t *testing.T) {
	repo := newStubRepo()
	agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), carrier.NewGHNAdapter(carrier.GHNConfig{Mode: "mock"}))
	svc := service.NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := New(svc)

	buyerCtx := ctxWithRole("buyer-1", "buyer", "")
	otherBuyerCtx := ctxWithRole("buyer-2", "buyer", "")
	sellerEmptyShopCtx := ctxWithRole("seller-x", "seller", "")
	sellerOtherShopCtx := ctxWithRole("seller-2", "seller", "shop-2")
	sellerOwnerCtx := ctxWithRole("seller-1", "seller", "shop-1")
	adminCtx := ctxWithRole("admin-1", "admin", "")

	createReq := &shippingv1.CreateShipmentRequest{
		OrderId:     "ord-rbac-1",
		ShopId:      "shop-1",
		BuyerId:     "buyer-1",
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

	// 2. GetShipment RBAC: wrong seller and wrong buyer are denied; owner seller, owner buyer, and admin succeed.
	getReq := &shippingv1.GetShipmentRequest{OrderId: "ord-rbac-1"}
	if _, err := srv.GetShipment(sellerEmptyShopCtx, getReq); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for seller with empty shop_id GetShipment, got %v", err)
	}
	if _, err := srv.GetShipment(sellerOtherShopCtx, getReq); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for mismatched seller GetShipment, got %v", err)
	}
	if _, err := srv.GetShipment(otherBuyerCtx, getReq); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for other buyer GetShipment, got %v", err)
	}
	if _, err := srv.GetShipment(buyerCtx, getReq); err != nil {
		t.Fatalf("expected owner buyer GetShipment to succeed, got %v", err)
	}
	if _, err := srv.GetShipment(sellerOwnerCtx, getReq); err != nil {
		t.Fatalf("expected owner seller GetShipment to succeed, got %v", err)
	}
	if _, err := srv.GetShipment(adminCtx, getReq); err != nil {
		t.Fatalf("expected admin GetShipment to succeed, got %v", err)
	}

	// 3. CancelShipment RBAC (w2a contract §4.10): buyer and seller JWTs are denied; admin or service token succeeds.
	cancelReq := &shippingv1.CancelShipmentRequest{OrderId: "ord-rbac-1", Reason: "test"}
	if _, err := srv.CancelShipment(buyerCtx, cancelReq); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for buyer CancelShipment, got %v", err)
	}
	if _, err := srv.CancelShipment(sellerEmptyShopCtx, cancelReq); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for seller with empty shop_id CancelShipment, got %v", err)
	}
	if _, err := srv.CancelShipment(sellerOwnerCtx, cancelReq); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for seller CancelShipment (only service/admin allowed), got %v", err)
	}
	if resp, err := srv.CancelShipment(adminCtx, cancelReq); err != nil || resp.GetShipment().GetStatus() != shippingv1.ShipmentStatus_SHIPMENT_STATUS_CANCELLED {
		t.Fatalf("expected admin CancelShipment to succeed, got resp=%+v err=%v", resp, err)
	}
}

func TestShippingGRPC_CalculateFeeValidationErrorCodes(t *testing.T) {
	repo := newStubRepo()
	agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), carrier.NewGHNAdapter(carrier.GHNConfig{Mode: "mock"}))
	svc := service.NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := New(svc)
	buyerCtx := ctxWithRole("buyer-1", "buyer", "")

	badReqs := []*shippingv1.CalculateFeeRequest{
		{ToProvinceCode: "79", ToWardCode: "79001", WeightGrams: 250},
		{FromProvinceCode: "79", FromWardCode: "79001", ToProvinceCode: "79", ToWardCode: "79001", WeightGrams: 0},
		{FromProvinceCode: "79", FromWardCode: "79001", ToProvinceCode: "99999", ToWardCode: "99999", WeightGrams: 250},
		{FromProvinceCode: "79", FromWardCode: "79001", ToProvinceCode: "79", ToWardCode: "01001", WeightGrams: 250},
	}
	for i, req := range badReqs {
		if _, err := srv.CalculateFee(buyerCtx, req); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("case %d: expected InvalidArgument, got %v", i, err)
		}
	}
}

func TestShippingGRPC_W2A_CancelShipmentRBACServiceAndAdminOnly(t *testing.T) {
	repo := newStubRepo()
	agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), carrier.NewGHNAdapter(carrier.GHNConfig{Mode: "mock"}))
	svc := service.NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := New(svc)

	sellerOwnerCtx := ctxWithRole("seller-1", "seller", "shop-1")
	buyerCtx := ctxWithRole("buyer-1", "buyer", "")
	svcCtx := ctxWithService("order-service")

	_, err := srv.CreateShipment(sellerOwnerCtx, &shippingv1.CreateShipmentRequest{
		OrderId:     "ord-w2a-cancel-rbac",
		ShopId:      "shop-1",
		CarrierCode: "ghn",
		WeightGrams: 500,
		BuyerId:     "buyer-1",
	})
	if err != nil {
		t.Fatalf("CreateShipment: %v", err)
	}

	cancelReq := &shippingv1.CancelShipmentRequest{
		OrderId:   "ord-w2a-cancel-rbac",
		Reason:    "cancel test",
		Direction: shippingv1.ShipmentDirection_SHIPMENT_DIRECTION_FORWARD,
	}
	if _, err := srv.CancelShipment(buyerCtx, cancelReq); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for buyer CancelShipment, got %v", err)
	}
	if _, err := srv.CancelShipment(sellerOwnerCtx, cancelReq); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for seller CancelShipment, got %v", err)
	}
	resp, err := srv.CancelShipment(svcCtx, cancelReq)
	if err != nil || resp.GetShipment().GetStatus() != shippingv1.ShipmentStatus_SHIPMENT_STATUS_CANCELLED {
		t.Fatalf("expected service token CancelShipment to succeed, got resp=%+v err=%v", resp, err)
	}
}

func TestShippingGRPC_W2A_ReturnShipmentAndRedelivery(t *testing.T) {
	repo := newStubRepo()
	agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), carrier.NewGHNAdapter(carrier.GHNConfig{Mode: "mock"}))
	svc := service.NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := New(svc)

	buyerCtx := ctxWithRole("buyer-1", "buyer", "")
	otherBuyerCtx := ctxWithRole("buyer-2", "buyer", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")
	svcCtx := ctxWithService("order-service")

	retReq := &shippingv1.CreateReturnShipmentRequest{
		OrderId:         "ord-w2a-ret-1",
		ReturnRequestId: "ret-req-1",
		ShopId:          "shop-1",
		WeightGrams:     500,
		CarrierCode:     "ghn",
		BuyerId:         "buyer-1",
		FromAddress:     &shippingv1.ShipmentAddress{ProvinceCode: "79", WardCode: "79001"},
		ToAddress:       &shippingv1.ShipmentAddress{ProvinceCode: "01", WardCode: "01001"},
	}
	if _, err := srv.CreateReturnShipment(sellerCtx, retReq); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for seller CreateReturnShipment, got %v", err)
	}
	if _, err := srv.CreateReturnShipment(buyerCtx, retReq); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for buyer CreateReturnShipment, got %v", err)
	}
	retResp, err := srv.CreateReturnShipment(svcCtx, retReq)
	if err != nil {
		t.Fatalf("service CreateReturnShipment failed: %v", err)
	}
	if retResp.GetShipment().GetDirection() != shippingv1.ShipmentDirection_SHIPMENT_DIRECTION_RETURN {
		t.Fatalf("expected RETURN direction, got %v", retResp.GetShipment().GetDirection())
	}
	if retResp.GetShipment().GetReturnRequestId() != "ret-req-1" {
		t.Fatalf("expected return_request_id=ret-req-1, got %q", retResp.GetShipment().GetReturnRequestId())
	}

	// RequestRedelivery: service token rejected; missing order -> NotFound; legacy without buyer_id -> FailedPrecondition; other buyer -> PermissionDenied.
	if _, err := srv.RequestRedelivery(svcCtx, &shippingv1.RequestRedeliveryRequest{OrderId: "ord-w2a-ret-1"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for service token RequestRedelivery, got %v", err)
	}
	if _, err := srv.RequestRedelivery(otherBuyerCtx, &shippingv1.RequestRedeliveryRequest{OrderId: "ord-missing"}); status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound for missing order RequestRedelivery, got %v", err)
	}

	// Seed a failed legacy shipment without buyer_id -> FailedPrecondition.
	_, _, _ = repo.CreateShipment(context.Background(), &store.Shipment{
		ID: uuid.NewString(), OrderID: "ord-legacy-fail", ShopID: "shop-1", BuyerID: "",
		Direction: store.DirectionForward, CarrierCode: "ghn", TrackingCode: "GHN-LEG-1", Status: carrier.StatusFailed,
	}, nil)
	if _, err := srv.RequestRedelivery(buyerCtx, &shippingv1.RequestRedeliveryRequest{OrderId: "ord-legacy-fail"}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition for legacy shipment without buyer_id, got %v", err)
	}

	// Seed a failed shipment owned by buyer-1 -> otherBuyer gets PermissionDenied,
	// each failed attempt allows ONLY 1 redelivery request, up to 3 total attempts, then FailedPrecondition.
	failShipID := uuid.NewString()
	_, _, _ = repo.CreateShipment(context.Background(), &store.Shipment{
		ID: failShipID, OrderID: "ord-fail-1", ShopID: "shop-1", BuyerID: "buyer-1",
		Direction: store.DirectionForward, CarrierCode: "ghn", TrackingCode: "GHN-FAIL-1", Status: carrier.StatusFailed,
	}, &store.ShipmentEvent{
		ID: uuid.NewString(), ShipmentID: failShipID, Status: carrier.StatusFailed, RawStatus: "delivery_fail",
		OccurredAt: time.Now().UTC(), CreatedAt: time.Now().UTC(),
	})
	if _, err := srv.RequestRedelivery(otherBuyerCtx, &shippingv1.RequestRedeliveryRequest{OrderId: "ord-fail-1"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for other buyer RequestRedelivery, got %v", err)
	}
	for i := int32(1); i <= 3; i++ {
		if i > 1 {
			// Record a new carrier failed delivery event before the next redelivery request.
			now := time.Now().UTC()
			repo.mu.Lock()
			repo.events[failShipID] = append(repo.events[failShipID], store.ShipmentEvent{
				ID: uuid.NewString(), ShipmentID: failShipID, Status: carrier.StatusFailed, RawStatus: "delivery_fail",
				OccurredAt: now.Add(time.Duration(i) * time.Second), CreatedAt: now,
			})
			repo.mu.Unlock()
		}
		resp, err := srv.RequestRedelivery(buyerCtx, &shippingv1.RequestRedeliveryRequest{
			OrderId: "ord-fail-1", PreferredDate: "2026-11-26", Note: "Gọi trước khi giao",
		})
		if err != nil || resp.GetShipment().GetRedeliveryCount() != i {
			t.Fatalf("attempt %d: expected count=%d, got resp=%+v err=%v", i, i, resp, err)
		}
		// Immediate duplicate redelivery without a new failed event must return FailedPrecondition.
		if _, dupErr := srv.RequestRedelivery(buyerCtx, &shippingv1.RequestRedeliveryRequest{
			OrderId: "ord-fail-1", PreferredDate: "2026-11-27",
		}); status.Code(dupErr) != codes.FailedPrecondition {
			t.Fatalf("attempt %d duplicate redelivery must return FailedPrecondition, got %v", i, dupErr)
		}
	}
	// Even with a 4th failed event, 4th redelivery must return FailedPrecondition (max 3).
	repo.mu.Lock()
	now4 := time.Now().UTC()
	repo.events[failShipID] = append(repo.events[failShipID], store.ShipmentEvent{
		ID: uuid.NewString(), ShipmentID: failShipID, Status: carrier.StatusFailed, RawStatus: "delivery_fail",
		OccurredAt: now4.Add(10 * time.Second), CreatedAt: now4,
	})
	repo.mu.Unlock()
	if _, err := srv.RequestRedelivery(buyerCtx, &shippingv1.RequestRedeliveryRequest{OrderId: "ord-fail-1"}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("4th redelivery must return FailedPrecondition, got %v", err)
	}
}

func TestShippingGRPC_W2A_ShopSettingsAndAdminCarriers(t *testing.T) {
	repo := newStubRepo()
	agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)),
		carrier.NewGHNAdapter(carrier.GHNConfig{Mode: "mock", Token: "sandbox-ghn-token"}),
		carrier.NewMockExpressAdapter(),
	)
	cipher, err := service.NewTokenCipherFromBase64("ZGV2LW9ubHktc2hpcHBpbmctY29uZmlnLWtleS0zMmI=")
	if err != nil {
		t.Fatalf("NewTokenCipherFromBase64: %v", err)
	}
	svc := service.NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.SetTokenCipher(cipher)
	srv := New(svc)

	buyerCtx := ctxWithRole("buyer-1", "buyer", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")
	adminCtx := ctxWithRole("admin-1", "admin", "")
	svcCtx := ctxWithService("order-service")

	// Buyer cannot read/update shop shipping settings or admin carriers.
	if _, err := srv.GetShopShippingSettings(buyerCtx, &shippingv1.GetShopShippingSettingsRequest{ShopId: "shop-1"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for buyer GetShopShippingSettings, got %v", err)
	}
	if _, err := srv.UpdateShopShippingSettings(buyerCtx, &shippingv1.UpdateShopShippingSettingsRequest{EnabledCarrierCodes: []string{"ghn"}}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for buyer UpdateShopShippingSettings, got %v", err)
	}
	if _, err := srv.AdminListCarriers(sellerCtx, &shippingv1.AdminListCarriersRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for seller AdminListCarriers, got %v", err)
	}
	if _, err := srv.AdminUpdateCarrier(svcCtx, &shippingv1.AdminUpdateCarrierRequest{CarrierCode: "ghn", Enabled: false}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for service token AdminUpdateCarrier, got %v", err)
	}

	// Seller turning off all carriers must fail with InvalidArgument (400).
	if _, err := srv.UpdateShopShippingSettings(sellerCtx, &shippingv1.UpdateShopShippingSettingsRequest{
		EnabledCarrierCodes: []string{},
		DefaultWeightGrams:  500,
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument when disabling all carriers, got %v", err)
	}

	// Seller updates settings (shop_id taken from claims) and service token can read it.
	updResp, err := srv.UpdateShopShippingSettings(sellerCtx, &shippingv1.UpdateShopShippingSettingsRequest{
		EnabledCarrierCodes: []string{"ghn"},
		DefaultWeightGrams:  800,
		DefaultLengthCm:     25,
		DefaultWidthCm:      20,
		DefaultHeightCm:     15,
	})
	if err != nil || updResp.GetSettings().GetShopId() != "shop-1" || updResp.GetSettings().GetDefaultWeightGrams() != 800 {
		t.Fatalf("unexpected UpdateShopShippingSettings resp=%+v err=%v", updResp, err)
	}
	getResp, err := srv.GetShopShippingSettings(svcCtx, &shippingv1.GetShopShippingSettingsRequest{ShopId: "shop-1"})
	if err != nil || len(getResp.GetSettings().GetEnabledCarrierCodes()) != 1 || getResp.GetSettings().GetEnabledCarrierCodes()[0] != "ghn" {
		t.Fatalf("unexpected GetShopShippingSettings resp=%+v err=%v", getResp, err)
	}

	// Admin lists, tests, and updates carriers.
	listResp, err := srv.AdminListCarriers(adminCtx, &shippingv1.AdminListCarriersRequest{})
	if err != nil || len(listResp.GetCarriers()) < 2 {
		t.Fatalf("expected at least 2 carriers from AdminListCarriers, got resp=%+v err=%v", listResp, err)
	}
	if _, err := srv.AdminUpdateCarrier(adminCtx, &shippingv1.AdminUpdateCarrierRequest{
		CarrierCode: "ghn",
		Enabled:     true,
		ApiToken:    "invalid-token",
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument when enabling ghn with invalid token, got %v", err)
	}
	testResp, err := srv.AdminTestCarrier(adminCtx, &shippingv1.AdminTestCarrierRequest{CarrierCode: "ghn"})
	if err != nil || !testResp.GetOk() {
		t.Fatalf("expected AdminTestCarrier ok=true for ghn with default sandbox token, got resp=%+v err=%v", testResp, err)
	}
}

func TestReview_GetShipmentAllowsSellerWhoIsBuyer(t *testing.T) {
	repo := newStubRepo()
	agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), carrier.NewGHNAdapter(carrier.GHNConfig{Mode: "mock"}))
	svc := service.NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := New(svc)

	// Shop-2 ships an order whose buyer happens to be a user with role="seller" owning shop-1.
	shop2SellerCtx := ctxWithRole("user-seller-2", "seller", "shop-2")
	shop1SellerAsBuyerCtx := ctxWithRole("user-seller-1", "seller", "shop-1")
	unrelatedSellerCtx := ctxWithRole("user-seller-3", "seller", "shop-3")

	if _, err := srv.CreateShipment(shop2SellerCtx, &shippingv1.CreateShipmentRequest{
		OrderId:     "ord-seller-as-buyer",
		ShopId:      "shop-2",
		BuyerId:     "user-seller-1",
		CarrierCode: "ghn",
		WeightGrams: 500,
	}); err != nil {
		t.Fatalf("CreateShipment: %v", err)
	}

	if _, err := srv.GetShipment(shop1SellerAsBuyerCtx, &shippingv1.GetShipmentRequest{OrderId: "ord-seller-as-buyer"}); err != nil {
		t.Fatalf("seller who is buyer_id of order must be allowed to GetShipment, got %v", err)
	}
	if _, err := srv.GetShipment(unrelatedSellerCtx, &shippingv1.GetShipmentRequest{OrderId: "ord-seller-as-buyer"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("unrelated seller must still get PermissionDenied, got %v", err)
	}
}

func TestReview3_GetShipmentAuthDemotedSellerAnonymousAndLegacy(t *testing.T) {
	repo := newStubRepo()
	agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), carrier.NewGHNAdapter(carrier.GHNConfig{Mode: "mock"}))
	svc := service.NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := New(svc)

	shop2SellerCtx := ctxWithRole("user-seller-2", "seller", "shop-2")
	demotedBuyerWithStaleShopCtx := ctxWithRole("user-demoted", "buyer", "shop-2")
	buyerCtx := ctxWithRole("other-buyer", "buyer", "")
	svcCtx := ctxWithService("order-service")

	if _, err := srv.CreateShipment(shop2SellerCtx, &shippingv1.CreateShipmentRequest{
		OrderId: "ord-r3-auth", ShopId: "shop-2", BuyerId: "real-buyer", CarrierCode: "ghn", WeightGrams: 500,
	}); err != nil {
		t.Fatalf("CreateShipment: %v", err)
	}
	_, _, _ = repo.CreateShipment(context.Background(), &store.Shipment{
		ID: uuid.NewString(), OrderID: "ord-r3-legacy", ShopID: "shop-2", BuyerID: "",
		Direction: store.DirectionForward, CarrierCode: "ghn", TrackingCode: "GHN-R3-LEG", Status: carrier.StatusCreated,
	}, nil)

	// Mục 1: Demoted buyer carrying stale shop_id in JWT must NOT pass shop ownership check.
	if _, err := srv.GetShipment(demotedBuyerWithStaleShopCtx, &shippingv1.GetShipmentRequest{OrderId: "ord-r3-auth"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("demoted buyer with stale shop_id must get PermissionDenied, got %v", err)
	}
	// Mục 2: Missing claims must return Unauthenticated.
	if _, err := srv.GetShipment(context.Background(), &shippingv1.GetShipmentRequest{OrderId: "ord-r3-auth"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anonymous GetShipment must return Unauthenticated, got %v", err)
	}
	// Mục 2: Service token must be allowed.
	if _, err := srv.GetShipment(svcCtx, &shippingv1.GetShipmentRequest{OrderId: "ord-r3-auth"}); err != nil {
		t.Fatalf("service token GetShipment must succeed, got %v", err)
	}
	// Mục 2: Legacy shipment without buyer_id must NOT be readable by arbitrary buyer.
	if _, err := srv.GetShipment(buyerCtx, &shippingv1.GetShipmentRequest{OrderId: "ord-r3-legacy"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("arbitrary buyer must NOT read legacy shipment with empty buyer_id, got %v", err)
	}
}

type failingCreateCarrier struct {
	carrier.Carrier
	createErr error
}

func (f *failingCreateCarrier) CreateShipment(_ context.Context, _ carrier.ShipmentRequest) (*carrier.ShipmentResult, error) {
	return nil, f.createErr
}

func TestReview_GRPCMapsCarrierRejectedVsUnavailable(t *testing.T) {
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")

	t.Run("non-retryable carrier rejection maps to FailedPrecondition with Vietnamese message", func(t *testing.T) {
		repo := newStubRepo()
		c := &failingCreateCarrier{
			Carrier:   carrier.NewGHNAdapter(carrier.GHNConfig{Mode: "mock"}),
			createErr: &carrier.NonRetryableError{Err: status.Error(codes.InvalidArgument, "bad ward")},
		}
		svc := service.NewShippingService(repo, carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), c), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		srv := New(svc)
		_, err := srv.CreateShipment(sellerCtx, &shippingv1.CreateShipmentRequest{
			OrderId: "ord-reject-1", ShopId: "shop-1", CarrierCode: "ghn", WeightGrams: 500,
		})
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("expected FailedPrecondition for non-retryable carrier error, got %v", err)
		}
		if !strings.Contains(err.Error(), "hãng vận chuyển từ chối") {
			t.Fatalf("expected clear Vietnamese message, got %q", err.Error())
		}
	})

	t.Run("transient carrier failure maps to Unavailable", func(t *testing.T) {
		repo := newStubRepo()
		c := &failingCreateCarrier{
			Carrier:   carrier.NewGHNAdapter(carrier.GHNConfig{Mode: "mock"}),
			createErr: status.Error(codes.Unavailable, "timeout"),
		}
		svc := service.NewShippingService(repo, carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), c), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		srv := New(svc)
		_, err := srv.CreateShipment(sellerCtx, &shippingv1.CreateShipmentRequest{
			OrderId: "ord-unavail-1", ShopId: "shop-1", CarrierCode: "ghn", WeightGrams: 500,
		})
		if status.Code(err) != codes.Unavailable {
			t.Fatalf("expected Unavailable for transient carrier error, got %v", err)
		}
	})

	t.Run("carrier 401/403 auth error maps to Unavailable (not FailedPrecondition / address rejected)", func(t *testing.T) {
		repo := newStubRepo()
		c := &failingCreateCarrier{
			Carrier:   carrier.NewGHNAdapter(carrier.GHNConfig{Mode: "mock"}),
			createErr: &carrier.NonRetryableError{Err: carrier.ErrInvalidCarrierToken},
		}
		svc := service.NewShippingService(repo, carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), c), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		srv := New(svc)
		_, err := srv.CreateShipment(sellerCtx, &shippingv1.CreateShipmentRequest{
			OrderId: "ord-auth-401", ShopId: "shop-1", CarrierCode: "ghn", WeightGrams: 500,
		})
		if status.Code(err) != codes.Unavailable {
			t.Fatalf("expected Unavailable for 401/403 carrier auth error, got %v", err)
		}
		if strings.Contains(err.Error(), "địa chỉ") {
			t.Fatalf("carrier auth error must not mention address rejection, got %q", err.Error())
		}
	})

	t.Run("CreateReturnShipment returns FailedPrecondition when existing RETURN is failed, succeeds when returned", func(t *testing.T) {
		repo := newStubRepo()
		svc := service.NewShippingService(repo, carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), carrier.NewGHNAdapter(carrier.GHNConfig{Mode: "mock"})), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		srv := New(svc)
		svcCtx := ctxWithService("order-service")

		_, _, _ = repo.CreateShipment(context.Background(), &store.Shipment{
			ID: uuid.NewString(), OrderID: "ord-ret-failed-grpc", ShopID: "shop-1", BuyerID: "buyer-1",
			Direction: store.DirectionReturn, ReturnRequestID: "rr-f1", CarrierCode: "ghn",
			TrackingCode: "GHN-RET-FAIL-1", Status: carrier.StatusFailed, CreatedAt: time.Now().UTC(),
		}, nil)

		_, err := srv.CreateReturnShipment(svcCtx, &shippingv1.CreateReturnShipmentRequest{
			OrderId: "ord-ret-failed-grpc", ReturnRequestId: "rr-f2", ShopId: "shop-1", BuyerId: "buyer-1",
			CarrierCode: "ghn", WeightGrams: 500,
			FromAddress: &shippingv1.ShipmentAddress{ProvinceCode: "79", WardCode: "79001"},
			ToAddress:   &shippingv1.ShipmentAddress{ProvinceCode: "01", WardCode: "01001"},
		})
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("expected FailedPrecondition when RETURN is failed, got %v", err)
		}
		if !strings.Contains(err.Error(), "thất bại") {
			t.Fatalf("expected Vietnamese message for failed RETURN, got %q", err.Error())
		}

		// When previous RETURN is returned, CreateReturnShipment creates a new one.
		_, _, _ = repo.CreateShipment(context.Background(), &store.Shipment{
			ID: uuid.NewString(), OrderID: "ord-ret-returned-grpc", ShopID: "shop-1", BuyerID: "buyer-1",
			Direction: store.DirectionReturn, ReturnRequestID: "rr-r1", CarrierCode: "ghn",
			TrackingCode: "GHN-RET-DONE-1", Status: carrier.StatusReturned, CreatedAt: time.Now().UTC().Add(-time.Minute),
		}, nil)
		resp, err := srv.CreateReturnShipment(svcCtx, &shippingv1.CreateReturnShipmentRequest{
			OrderId: "ord-ret-returned-grpc", ReturnRequestId: "rr-r2", ShopId: "shop-1", BuyerId: "buyer-1",
			CarrierCode: "ghn", WeightGrams: 500,
			FromAddress: &shippingv1.ShipmentAddress{ProvinceCode: "79", WardCode: "79001"},
			ToAddress:   &shippingv1.ShipmentAddress{ProvinceCode: "01", WardCode: "01001"},
		})
		if err != nil || resp.GetAlreadyExisted() || resp.GetShipment().GetTrackingCode() == "GHN-RET-DONE-1" {
			t.Fatalf("expected new RETURN after returned, got resp=%+v err=%v", resp, err)
		}
	})
}
