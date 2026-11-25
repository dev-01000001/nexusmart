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
	"github.com/nexusmart/core/pkg/common/secretbox"
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
	jobs         map[string]*store.ShipmentCreateJob
	shopSettings map[string]*store.ShopShippingSettings
	carriers     map[string]*store.CarrierConfig
	fallback     map[string][]store.FallbackRate
}

func newStubRepo() *stubRepo {
	return &stubRepo{
		events:       make(map[string][]store.ShipmentEvent),
		jobs:         make(map[string]*store.ShipmentCreateJob),
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
func (r *stubRepo) ListWards(_ context.Context, provinceCode, _ string) ([]address.Ward, error) {
	var out []address.Ward
	for _, w := range address.Wards() {
		if w.ProvinceCode == provinceCode && w.IsActive {
			out = append(out, w)
		}
	}
	return out, nil
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
func (r *stubRepo) GetRepresentativeWard(_ context.Context, provinceCode string) (*address.Ward, error) {
	var best *address.Ward
	for _, w := range address.Wards() {
		if w.ProvinceCode != provinceCode || !w.IsActive {
			continue
		}
		if best == nil || (w.CODSupported && !best.CODSupported) || (w.CODSupported == best.CODSupported && w.Code < best.Code) {
			cp := w
			best = &cp
		}
	}
	if best == nil {
		return nil, store.ErrNotFound
	}
	return best, nil
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
func (r *stubRepo) GetShipmentsByTrackingCodes(_ context.Context, trackingCodes []string) ([]store.Shipment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	want := make(map[string]bool, len(trackingCodes))
	for _, c := range trackingCodes {
		want[c] = true
	}
	var out []store.Shipment
	for _, sh := range r.shipments {
		if want[sh.TrackingCode] {
			out = append(out, *sh)
		}
	}
	return out, nil
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
func (r *stubRepo) CreateShipmentWithOutbox(ctx context.Context, sh *store.Shipment, initEv *store.ShipmentEvent, _ *store.JobOutboxParams) (*store.Shipment, bool, error) {
	return r.CreateShipment(ctx, sh, initEv)
}
func (r *stubRepo) GetActiveShipmentCreateJob(_ context.Context, orderID, direction string) (*store.ShipmentCreateJob, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	dir := store.NormalizeDirection(direction)
	for _, j := range r.jobs {
		if j.OrderID == orderID && j.Direction == dir && (j.Status == store.JobStatusPending || j.Status == store.JobStatusProcessing) {
			cp := *j
			return &cp, nil
		}
	}
	return nil, store.ErrNotFound
}
func (r *stubRepo) GetShipmentCreateJobByID(_ context.Context, jobID string) (*store.ShipmentCreateJob, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	j, ok := r.jobs[jobID]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *j
	return &cp, nil
}
func (r *stubRepo) EnqueueShipmentCreateJob(_ context.Context, job *store.ShipmentCreateJob) (*store.ShipmentCreateJob, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	dir := store.NormalizeDirection(job.Direction)
	for _, existing := range r.jobs {
		if existing.OrderID == job.OrderID && existing.Direction == dir && (existing.Status == store.JobStatusPending || existing.Status == store.JobStatusProcessing) {
			cp := *existing
			return &cp, true, nil
		}
	}
	cp := *job
	if cp.ID == "" {
		cp.ID = uuid.NewString()
	}
	cp.Direction = dir
	if cp.Status == "" {
		cp.Status = store.JobStatusPending
	}
	r.jobs[cp.ID] = &cp
	ret := cp
	return &ret, false, nil
}
func (r *stubRepo) CancelActiveShipmentCreateJobs(_ context.Context, orderID, direction, reason string) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	dir := store.NormalizeDirection(direction)
	var n int64
	for _, j := range r.jobs {
		if j.OrderID == orderID && j.Direction == dir && (j.Status == store.JobStatusPending || j.Status == store.JobStatusProcessing) {
			j.Status = store.JobStatusCancelled
			j.LastError = reason
			n++
		}
	}
	return n, nil
}
func (r *stubRepo) ClaimDueShipmentCreateJobs(_ context.Context, _ int, _ time.Duration, _ time.Time) ([]store.ShipmentCreateJob, string, error) {
	return nil, "", nil
}
func (r *stubRepo) CompleteShipmentCreateJob(ctx context.Context, jobID, _ string, sh *store.Shipment, initEv *store.ShipmentEvent, _ *store.JobOutboxParams) (*store.Shipment, bool, error) {
	r.mu.Lock()
	if j, ok := r.jobs[jobID]; ok {
		if j.Status == store.JobStatusCancelled {
			r.mu.Unlock()
			return nil, false, store.ErrJobCancelled
		}
		j.Status = store.JobStatusSucceeded
	}
	r.mu.Unlock()
	return r.CreateShipment(ctx, sh, initEv)
}
func (r *stubRepo) RecordShipmentCreateJobFailure(_ context.Context, _, _, _ string, _ *time.Time, _ bool, _ *store.JobOutboxParams, _ time.Time) (*store.ShipmentCreateJob, bool, error) {
	return nil, false, nil
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
		{ToProvinceCode: "79", ToWardCode: "26740", WeightGrams: 250},
		{FromProvinceCode: "79", FromWardCode: "26740", ToProvinceCode: "79", ToWardCode: "26740", WeightGrams: 0},
		{FromProvinceCode: "79", FromWardCode: "26740", ToProvinceCode: "99999", ToWardCode: "99999", WeightGrams: 250},
		{FromProvinceCode: "79", FromWardCode: "26740", ToProvinceCode: "79", ToWardCode: "00070", WeightGrams: 250},
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
		FromAddress:     &shippingv1.ShipmentAddress{ProvinceCode: "79", WardCode: "26740"},
		ToAddress:       &shippingv1.ShipmentAddress{ProvinceCode: "01", WardCode: "00070"},
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
	cipher, err := secretbox.NewFromBase64("ZGV2LW9ubHktc2hpcHBpbmctY29uZmlnLWtleS0zMmI=", "SHIPPING_CONFIG_KEY")
	if err != nil {
		t.Fatalf("secretbox.NewFromBase64: %v", err)
	}
	svc := service.NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.SetTokenCipher(cipher)
	srv := New(svc)

	buyerCtx := ctxWithRole("buyer-1", "buyer", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")
	adminCtx := ctxWithRole("admin-1", "admin", "")
	svcCtx := ctxWithService("order-service")

	// Buyer reading GetShopShippingSettings gets only public fields (shop_id, default_weight_grams, free_ship_*);
	// carriers, parcel dimensions, and updated_at stay empty; missing shop_id returns InvalidArgument;
	// mutations and admin RPCs remain denied.
	if _, err := srv.GetShopShippingSettings(buyerCtx, &shippingv1.GetShopShippingSettingsRequest{ShopId: ""}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for buyer GetShopShippingSettings with empty shop_id, got %v", err)
	}
	buyerPubResp, err := srv.GetShopShippingSettings(buyerCtx, &shippingv1.GetShopShippingSettingsRequest{ShopId: "shop-1"})
	if err != nil {
		t.Fatalf("expected buyer GetShopShippingSettings to return public shop projection, got %v", err)
	}
	if pub := buyerPubResp.GetSettings(); pub.GetShopId() != "shop-1" || pub.GetDefaultWeightGrams() != 500 ||
		len(pub.GetEnabledCarrierCodes()) != 0 || pub.GetDefaultLengthCm() != 0 || pub.GetDefaultWidthCm() != 0 ||
		pub.GetDefaultHeightCm() != 0 || pub.GetUpdatedAt() != nil {
		t.Fatalf("buyer GetShopShippingSettings unexpected public projection: %+v", pub)
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

	t.Run("transient carrier failure queues async retry job (E18) while return shipment still maps to Unavailable", func(t *testing.T) {
		repo := newStubRepo()
		c := &failingCreateCarrier{
			Carrier:   carrier.NewGHNAdapter(carrier.GHNConfig{Mode: "mock"}),
			createErr: status.Error(codes.Unavailable, "timeout"),
		}
		svc := service.NewShippingService(repo, carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), c), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		srv := New(svc)
		resp, err := srv.CreateShipment(sellerCtx, &shippingv1.CreateShipmentRequest{
			OrderId: "ord-unavail-1", ShopId: "shop-1", CarrierCode: "ghn", WeightGrams: 500,
		})
		if err != nil {
			t.Fatalf("E18: expected queued response on transient carrier error, got err=%v", err)
		}
		if !resp.GetQueued() || resp.GetJobId() == "" || resp.GetShipment() != nil {
			t.Fatalf("E18: expected queued=true with job_id and nil shipment, got %+v", resp)
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
			FromAddress: &shippingv1.ShipmentAddress{ProvinceCode: "79", WardCode: "26740"},
			ToAddress:   &shippingv1.ShipmentAddress{ProvinceCode: "01", WardCode: "00070"},
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
			FromAddress: &shippingv1.ShipmentAddress{ProvinceCode: "79", WardCode: "26740"},
			ToAddress:   &shippingv1.ShipmentAddress{ProvinceCode: "01", WardCode: "00070"},
		})
		if err != nil || resp.GetAlreadyExisted() || resp.GetShipment().GetTrackingCode() == "GHN-RET-DONE-1" {
			t.Fatalf("expected new RETURN after returned, got resp=%+v err=%v", resp, err)
		}
	})
}

func TestShippingGRPC_W2B_ShopFreeshipSettingsAndOrderAccess(t *testing.T) {
	repo := newStubRepo()
	agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)),
		carrier.NewGHNAdapter(carrier.GHNConfig{Mode: "mock", Token: "sandbox-ghn-token"}),
		carrier.NewMockExpressAdapter(),
	)
	svc := service.NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := New(svc)

	buyerCtx := ctxWithRole("buyer-1", "buyer", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-w2b-1")
	otherSellerCtx := ctxWithRole("seller-2", "seller", "shop-w2b-2")
	adminCtx := ctxWithRole("admin-1", "admin", "")
	orderSvcCtx := ctxWithService("order-service")

	// 1. Order service reading unconfigured shop gets default freeship=false, 0, 0 and default_weight_grams=500.
	defResp, err := srv.GetShopShippingSettings(orderSvcCtx, &shippingv1.GetShopShippingSettingsRequest{ShopId: "shop-w2b-1"})
	if err != nil {
		t.Fatalf("order GetShopShippingSettings default: %v", err)
	}
	defSt := defResp.GetSettings()
	if defSt.GetFreeShipEnabled() || defSt.GetFreeShipMinOrderVnd() != 0 || defSt.GetFreeShipMaxVnd() != 0 || defSt.GetDefaultWeightGrams() != 500 {
		t.Fatalf("unexpected default settings: %+v", defSt)
	}

	// 2. Invalid freeship inputs map to codes.InvalidArgument.
	for _, bad := range []*shippingv1.UpdateShopShippingSettingsRequest{
		{EnabledCarrierCodes: []string{"ghn"}, DefaultWeightGrams: 500, FreeShipEnabled: true, FreeShipMinOrderVnd: 200000, FreeShipMaxVnd: 0},
		{EnabledCarrierCodes: []string{"ghn"}, DefaultWeightGrams: 500, FreeShipEnabled: true, FreeShipMinOrderVnd: -1, FreeShipMaxVnd: 30000},
		{EnabledCarrierCodes: []string{"ghn"}, DefaultWeightGrams: 500, FreeShipEnabled: false, FreeShipMinOrderVnd: 0, FreeShipMaxVnd: -100},
	} {
		if _, err := srv.UpdateShopShippingSettings(sellerCtx, bad); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("expected InvalidArgument for %+v, got %v", bad, err)
		}
	}

	// 3. RBAC: buyer, other seller, and service token cannot mutate shop shipping settings.
	validUpd := &shippingv1.UpdateShopShippingSettingsRequest{
		ShopId:              "shop-w2b-1",
		EnabledCarrierCodes: []string{"ghn"},
		DefaultWeightGrams:  1200,
		DefaultLengthCm:     25,
		DefaultWidthCm:      20,
		DefaultHeightCm:     15,
		FreeShipEnabled:     true,
		FreeShipMinOrderVnd: 200000,
		FreeShipMaxVnd:      30000,
	}
	if _, err := srv.UpdateShopShippingSettings(buyerCtx, validUpd); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for buyer UpdateShopShippingSettings, got %v", err)
	}
	if _, err := srv.UpdateShopShippingSettings(otherSellerCtx, validUpd); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for other seller UpdateShopShippingSettings, got %v", err)
	}
	if _, err := srv.UpdateShopShippingSettings(orderSvcCtx, validUpd); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for service token UpdateShopShippingSettings, got %v", err)
	}

	// Baseline quote before freeship.
	baseQ, err := srv.CalculateFee(buyerCtx, &shippingv1.CalculateFeeRequest{
		ShopId: "shop-w2b-1", FromProvinceCode: "79", FromWardCode: "26740",
		ToProvinceCode: "79", ToWardCode: "26740", WeightGrams: 1200,
	})
	if err != nil || len(baseQ.GetQuotes()) == 0 {
		t.Fatalf("baseline CalculateFee: resp=%+v err=%v", baseQ, err)
	}
	var baseGHNStd int64
	for _, q := range baseQ.GetQuotes() {
		if q.GetCarrierCode() == "ghn" && q.GetServiceCode() == "STANDARD" {
			baseGHNStd = q.GetFeeVnd()
		}
	}
	if baseGHNStd <= 0 {
		t.Fatalf("expected positive ghn STANDARD baseline quote, got %+v", baseQ.GetQuotes())
	}

	// 4. Seller saves freeship settings; seller, admin, and order service token all read back exact freeship fields.
	updResp, err := srv.UpdateShopShippingSettings(sellerCtx, &shippingv1.UpdateShopShippingSettingsRequest{
		EnabledCarrierCodes: []string{"ghn"},
		DefaultWeightGrams:  1200,
		DefaultLengthCm:     25,
		DefaultWidthCm:      20,
		DefaultHeightCm:     15,
		FreeShipEnabled:     true,
		FreeShipMinOrderVnd: 200000,
		FreeShipMaxVnd:      30000,
	})
	if err != nil {
		t.Fatalf("seller UpdateShopShippingSettings: %v", err)
	}
	st := updResp.GetSettings()
	if st.GetShopId() != "shop-w2b-1" || !st.GetFreeShipEnabled() || st.GetFreeShipMinOrderVnd() != 200000 || st.GetFreeShipMaxVnd() != 30000 {
		t.Fatalf("unexpected UpdateShopShippingSettings response: %+v", st)
	}

	for name, tc := range map[string]struct {
		ctx    context.Context
		shopID string
	}{
		"order-service":         {ctx: orderSvcCtx, shopID: "shop-w2b-1"},
		"seller-owner":          {ctx: sellerCtx, shopID: "shop-w2b-1"},
		"seller-owner-no-param": {ctx: sellerCtx, shopID: ""},
		"admin":                 {ctx: adminCtx, shopID: "shop-w2b-1"},
	} {
		got, err := srv.GetShopShippingSettings(tc.ctx, &shippingv1.GetShopShippingSettingsRequest{ShopId: tc.shopID})
		if err != nil {
			t.Fatalf("%s GetShopShippingSettings: %v", name, err)
		}
		gs := got.GetSettings()
		if gs.GetShopId() != "shop-w2b-1" || !gs.GetFreeShipEnabled() || gs.GetFreeShipMinOrderVnd() != 200000 ||
			gs.GetFreeShipMaxVnd() != 30000 || len(gs.GetEnabledCarrierCodes()) != 1 || gs.GetEnabledCarrierCodes()[0] != "ghn" ||
			gs.GetDefaultWeightGrams() != 1200 || gs.GetDefaultLengthCm() != 25 || gs.GetDefaultWidthCm() != 20 ||
			gs.GetDefaultHeightCm() != 15 || gs.GetUpdatedAt() == nil {
			t.Fatalf("%s expected full shop shipping settings, got %+v", name, gs)
		}
	}

	// Anonymous caller must be rejected with Unauthenticated.
	if _, err := srv.GetShopShippingSettings(context.Background(), &shippingv1.GetShopShippingSettingsRequest{ShopId: "shop-w2b-1"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated for anonymous GetShopShippingSettings, got %v", err)
	}

	// Buyer, another seller, and demoted buyer with stale shop_id see ONLY shop_id + default_weight_grams + free_ship_*
	// (no carrier/dimensions/updated_at leakage).
	demotedBuyerCtx := ctxWithRole("demoted-1", "buyer", "shop-w2b-1")
	for name, ctx := range map[string]context.Context{
		"buyer":         buyerCtx,
		"other-seller":  otherSellerCtx,
		"demoted-buyer": demotedBuyerCtx,
	} {
		if _, err := srv.GetShopShippingSettings(ctx, &shippingv1.GetShopShippingSettingsRequest{ShopId: "   "}); status.Code(err) != codes.InvalidArgument && name != "other-seller" {
			t.Fatalf("%s expected InvalidArgument for empty shop_id, got %v", name, err)
		}
		got, err := srv.GetShopShippingSettings(ctx, &shippingv1.GetShopShippingSettingsRequest{ShopId: "shop-w2b-1"})
		if err != nil {
			t.Fatalf("%s GetShopShippingSettings(shop-w2b-1): %v", name, err)
		}
		gs := got.GetSettings()
		if gs.GetShopId() != "shop-w2b-1" || gs.GetDefaultWeightGrams() != 1200 ||
			!gs.GetFreeShipEnabled() || gs.GetFreeShipMinOrderVnd() != 200000 || gs.GetFreeShipMaxVnd() != 30000 {
			t.Fatalf("%s expected default_weight_grams=1200 and freeship (true, 200000, 30000), got %+v", name, gs)
		}
		if len(gs.GetEnabledCarrierCodes()) != 0 || gs.GetDefaultLengthCm() != 0 ||
			gs.GetDefaultWidthCm() != 0 || gs.GetDefaultHeightCm() != 0 || gs.GetUpdatedAt() != nil {
			t.Fatalf("%s MUST NOT receive private carrier/dimensions/updated_at fields, got %+v", name, gs)
		}
	}
	// Seller with empty shop_id claim reading without req.shop_id gets InvalidArgument; reading explicit shop_id gets public projection only.
	sellerEmptyShopCtx := ctxWithRole("seller-empty", "seller", "")
	if _, err := srv.GetShopShippingSettings(sellerEmptyShopCtx, &shippingv1.GetShopShippingSettingsRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for seller with empty claims.shop_id and empty req.shop_id, got %v", err)
	}
	if got, err := srv.GetShopShippingSettings(sellerEmptyShopCtx, &shippingv1.GetShopShippingSettingsRequest{ShopId: "shop-w2b-1"}); err != nil ||
		!got.GetSettings().GetFreeShipEnabled() || got.GetSettings().GetDefaultWeightGrams() != 1200 ||
		len(got.GetSettings().GetEnabledCarrierCodes()) != 0 || got.GetSettings().GetDefaultLengthCm() != 0 ||
		got.GetSettings().GetDefaultWidthCm() != 0 || got.GetSettings().GetDefaultHeightCm() != 0 ||
		got.GetSettings().GetUpdatedAt() != nil {
		t.Fatalf("expected public projection (default_weight_grams + freeship) for seller with empty claims.shop_id, got resp=%+v err=%v", got, err)
	}

	// 5. CalculateFee MUST NOT deduct shop freeship (returns full raw carrier fee).
	afterQ, err := srv.CalculateFee(buyerCtx, &shippingv1.CalculateFeeRequest{
		ShopId: "shop-w2b-1", FromProvinceCode: "79", FromWardCode: "26740",
		ToProvinceCode: "79", ToWardCode: "26740", WeightGrams: 0, // uses default_weight_grams=1200
	})
	if err != nil || len(afterQ.GetQuotes()) == 0 {
		t.Fatalf("after freeship CalculateFee: resp=%+v err=%v", afterQ, err)
	}
	var afterGHNStd int64
	for _, q := range afterQ.GetQuotes() {
		if q.GetCarrierCode() == "ghn" && q.GetServiceCode() == "STANDARD" {
			afterGHNStd = q.GetFeeVnd()
		}
	}
	if afterGHNStd != baseGHNStd {
		t.Fatalf("CalculateFee must return unmodified carrier fee %d even with shop freeship enabled, got %d", baseGHNStd, afterGHNStd)
	}
}
