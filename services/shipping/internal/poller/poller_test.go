package poller

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nexusmart/core/services/shipping/internal/address"
	"github.com/nexusmart/core/services/shipping/internal/carrier"
	"github.com/nexusmart/core/services/shipping/internal/events"
	"github.com/nexusmart/core/services/shipping/internal/service"
	"github.com/nexusmart/core/services/shipping/internal/store"
)

type pollerMemRepo struct {
	mu          sync.Mutex
	shipments   map[string]*store.Shipment // key: orderID|direction
	byTracking  map[string]*store.Shipment
	events      map[string][]store.ShipmentEvent
	webhookSeen map[string]bool
	outbox      []store.OutboxEvent
}

func newPollerMemRepo() *pollerMemRepo {
	return &pollerMemRepo{
		shipments:   make(map[string]*store.Shipment),
		byTracking:  make(map[string]*store.Shipment),
		events:      make(map[string][]store.ShipmentEvent),
		webhookSeen: make(map[string]bool),
	}
}

func keyDir(orderID, dir string) string {
	return orderID + "|" + store.NormalizeDirection(dir)
}

func (m *pollerMemRepo) ListProvinces(_ context.Context, _ string) ([]address.Province, error) {
	return nil, nil
}
func (m *pollerMemRepo) GetProvince(_ context.Context, _ string) (*address.Province, error) {
	return nil, store.ErrNotFound
}
func (m *pollerMemRepo) ListWards(_ context.Context, _, _ string) ([]address.Ward, error) {
	return nil, nil
}
func (m *pollerMemRepo) GetWard(_ context.Context, _ string) (*address.Ward, error) {
	return nil, store.ErrNotFound
}
func (m *pollerMemRepo) GetRepresentativeWard(_ context.Context, _ string) (*address.Ward, error) {
	return nil, store.ErrNotFound
}
func (m *pollerMemRepo) MapLegacyAddress(_ context.Context, _, _, _ string) (*address.LegacyMatch, error) {
	return &address.LegacyMatch{}, nil
}
func (m *pollerMemRepo) GetShipmentByOrderID(_ context.Context, orderID, direction string) (*store.Shipment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sh, ok := m.shipments[keyDir(orderID, direction)]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *sh
	return &cp, nil
}
func (m *pollerMemRepo) GetActiveShipmentByOrderID(ctx context.Context, orderID, direction string) (*store.Shipment, error) {
	sh, err := m.GetShipmentByOrderID(ctx, orderID, direction)
	if err != nil {
		return nil, err
	}
	if store.IsInactiveShipment(direction, sh.Status) {
		return nil, store.ErrNotFound
	}
	return sh, nil
}
func (m *pollerMemRepo) CountInactiveShipmentsByOrder(_ context.Context, _, _ string) (int, error) {
	return 0, nil
}
func (m *pollerMemRepo) AcquireOrderDirectionLock(_ context.Context, _, _ string) (func(), error) {
	return func() {}, nil
}
func (m *pollerMemRepo) GetShipmentByTrackingCode(_ context.Context, trackingCode string) (*store.Shipment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sh, ok := m.byTracking[trackingCode]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *sh
	return &cp, nil
}
func (m *pollerMemRepo) GetShipmentsByTrackingCodes(_ context.Context, trackingCodes []string) ([]store.Shipment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]store.Shipment, 0, len(trackingCodes))
	for _, c := range trackingCodes {
		if sh, ok := m.byTracking[c]; ok {
			out = append(out, *sh)
		}
	}
	return out, nil
}
func (m *pollerMemRepo) ListShipmentEvents(_ context.Context, shipmentID string) ([]store.ShipmentEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]store.ShipmentEvent, len(m.events[shipmentID]))
	copy(out, m.events[shipmentID])
	return out, nil
}
func (m *pollerMemRepo) CreateShipment(_ context.Context, sh *store.Shipment, initEv *store.ShipmentEvent) (*store.Shipment, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sh.Direction = store.NormalizeDirection(sh.Direction)
	k := keyDir(sh.OrderID, sh.Direction)
	if existing, ok := m.shipments[k]; ok {
		cp := *existing
		return &cp, true, nil
	}
	cp := *sh
	m.shipments[k] = &cp
	m.byTracking[sh.TrackingCode] = &cp
	if initEv != nil {
		m.events[sh.ID] = append(m.events[sh.ID], *initEv)
	}
	ret := cp
	return &ret, false, nil
}
func (m *pollerMemRepo) CreateShipmentWithOutbox(ctx context.Context, sh *store.Shipment, initEv *store.ShipmentEvent, _ *store.JobOutboxParams) (*store.Shipment, bool, error) {
	return m.CreateShipment(ctx, sh, initEv)
}
func (m *pollerMemRepo) CancelShipmentByID(_ context.Context, _, _ string) (*store.Shipment, error) {
	return nil, nil
}
func (m *pollerMemRepo) CancelShipmentWithEvent(_ context.Context, _, _, _, _ string) (*store.Shipment, bool, error) {
	return nil, false, nil
}
func (m *pollerMemRepo) RecordRedelivery(_ context.Context, _, _ string, _ int32, _, _ string) (*store.Shipment, error) {
	return nil, nil
}
func (m *pollerMemRepo) ApplyWebhookEvent(_ context.Context, params store.WebhookUpdateParams) (*store.Shipment, store.WebhookUpdateOutcome, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sh := m.byTracking[params.TrackingCode]
	if sh == nil && params.OrderID != "" {
		sh = m.shipments[keyDir(params.OrderID, store.DirectionForward)]
	}
	if sh == nil {
		return nil, "", store.ErrNotFound
	}
	if strings.TrimSpace(params.CarrierCode) == "" || !strings.EqualFold(strings.TrimSpace(params.CarrierCode), sh.CarrierCode) {
		return nil, "", store.ErrCarrierMismatch
	}
	if m.webhookSeen[params.EventID] {
		return nil, store.WebhookOutcomeDuplicate, nil
	}
	m.webhookSeen[params.EventID] = true

	if !store.CanTransitionStatus(sh.Status, params.InternalStatus) || (sh.LastEventAt != nil && params.OccurredAt.Before(*sh.LastEventAt)) {
		cp := *sh
		return &cp, store.WebhookOutcomeStale, nil
	}
	ts := params.OccurredAt.UTC()
	sh.Status = params.InternalStatus
	sh.LastEventAt = &ts
	sh.UpdatedAt = time.Now().UTC()
	m.events[sh.ID] = append(m.events[sh.ID], store.ShipmentEvent{
		ID:          uuid.NewString(),
		ShipmentID:  sh.ID,
		Status:      params.InternalStatus,
		RawStatus:   params.RawStatus,
		Description: params.Description,
		OccurredAt:  ts,
	})
	if params.BuildOutboxPayload != nil {
		payload, err := params.BuildOutboxPayload(sh)
		if err != nil {
			return nil, "", err
		}
		m.outbox = append(m.outbox, store.OutboxEvent{
			ID:        uuid.NewString(),
			EventID:   params.OutboxEventID,
			Subject:   params.OutboxSubject,
			Payload:   payload,
			CreatedAt: time.Now().UTC(),
		})
	}
	cp := *sh
	return &cp, store.WebhookOutcomeApplied, nil
}
func (m *pollerMemRepo) ListActiveShipments(_ context.Context, limit int, afterUpdatedAt *time.Time, afterID string) ([]store.Shipment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var active []store.Shipment
	for _, sh := range m.shipments {
		if carrier.IsTerminalStatus(sh.Status) {
			continue
		}
		if afterUpdatedAt != nil && afterID != "" {
			if sh.UpdatedAt.Before(*afterUpdatedAt) || (sh.UpdatedAt.Equal(*afterUpdatedAt) && sh.ID <= afterID) {
				continue
			}
		}
		active = append(active, *sh)
	}
	sort.Slice(active, func(i, j int) bool {
		if !active[i].UpdatedAt.Equal(active[j].UpdatedAt) {
			return active[i].UpdatedAt.Before(active[j].UpdatedAt)
		}
		return active[i].ID < active[j].ID
	})
	if limit > 0 && len(active) > limit {
		active = active[:limit]
	}
	return active, nil
}
func (m *pollerMemRepo) GetActiveShipmentCreateJob(_ context.Context, _, _ string) (*store.ShipmentCreateJob, error) {
	return nil, store.ErrNotFound
}
func (m *pollerMemRepo) GetShipmentCreateJobByID(_ context.Context, _ string) (*store.ShipmentCreateJob, error) {
	return nil, store.ErrNotFound
}
func (m *pollerMemRepo) EnqueueShipmentCreateJob(_ context.Context, job *store.ShipmentCreateJob) (*store.ShipmentCreateJob, bool, error) {
	cp := *job
	return &cp, false, nil
}
func (m *pollerMemRepo) CancelActiveShipmentCreateJobs(_ context.Context, _, _, _ string) (int64, error) {
	return 0, nil
}
func (m *pollerMemRepo) ClaimDueShipmentCreateJobs(_ context.Context, _ int, _ time.Duration, _ time.Time) ([]store.ShipmentCreateJob, string, error) {
	return nil, "", nil
}
func (m *pollerMemRepo) CompleteShipmentCreateJob(ctx context.Context, _, _ string, sh *store.Shipment, initEv *store.ShipmentEvent, _ *store.JobOutboxParams) (*store.Shipment, bool, error) {
	return m.CreateShipment(ctx, sh, initEv)
}
func (m *pollerMemRepo) RecordShipmentCreateJobFailure(_ context.Context, _, _, _ string, _ *time.Time, _ bool, _ *store.JobOutboxParams, _ time.Time) (*store.ShipmentCreateJob, bool, error) {
	return nil, false, nil
}
func (m *pollerMemRepo) GetShopShippingSettings(_ context.Context, _ string) (*store.ShopShippingSettings, error) {
	return nil, store.ErrNotFound
}
func (m *pollerMemRepo) UpsertShopShippingSettings(_ context.Context, st *store.ShopShippingSettings) (*store.ShopShippingSettings, error) {
	return st, nil
}
func (m *pollerMemRepo) ListCarrierConfigs(_ context.Context) ([]store.CarrierConfig, error) {
	return nil, nil
}
func (m *pollerMemRepo) GetCarrierConfig(_ context.Context, _ string) (*store.CarrierConfig, error) {
	return nil, store.ErrNotFound
}
func (m *pollerMemRepo) UpdateCarrierConfig(_ context.Context, cfg *store.CarrierConfig, _ []store.FallbackRate) (*store.CarrierConfig, error) {
	return cfg, nil
}
func (m *pollerMemRepo) SeedCarrierTokenIfEmpty(_ context.Context, _ string, _ []byte) error {
	return nil
}
func (m *pollerMemRepo) ListFallbackRates(_ context.Context, _ string) ([]store.FallbackRate, error) {
	return nil, nil
}
func (m *pollerMemRepo) IsEventProcessed(_ context.Context, _ string) (bool, error) {
	return false, nil
}
func (m *pollerMemRepo) MarkEventProcessed(_ context.Context, _, _ string) (bool, error) {
	return true, nil
}
func (m *pollerMemRepo) InsertDeadLetter(_ context.Context, _, _ string, _ []byte, _ string, _ int) error {
	return nil
}

func (m *pollerMemRepo) outboxSnapshot() []store.OutboxEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]store.OutboxEvent, len(m.outbox))
	copy(out, m.outbox)
	return out
}

type fakeLiveCarrier struct {
	code      string
	statusFn  func(ctx context.Context, tracking string) (*carrier.TrackingStatus, error)
	pollCalls atomic.Int32
}

func (f *fakeLiveCarrier) Code() string { return f.code }
func (f *fakeLiveCarrier) Name() string { return f.code }
func (f *fakeLiveCarrier) IsMock() bool { return false }
func (f *fakeLiveCarrier) CalculateFee(_ context.Context, _ carrier.FeeRequest) ([]carrier.Quote, error) {
	return nil, nil
}
func (f *fakeLiveCarrier) CreateShipment(_ context.Context, req carrier.ShipmentRequest) (*carrier.ShipmentResult, error) {
	return &carrier.ShipmentResult{
		CarrierCode:  f.code,
		ServiceCode:  "STANDARD",
		TrackingCode: "LIVE-" + req.OrderID,
		Status:       carrier.StatusCreated,
	}, nil
}
func (f *fakeLiveCarrier) CancelShipment(_ context.Context, _ string) error { return nil }
func (f *fakeLiveCarrier) GetStatus(ctx context.Context, trackingCode string) (*carrier.TrackingStatus, error) {
	f.pollCalls.Add(1)
	if f.statusFn != nil {
		return f.statusFn(ctx, trackingCode)
	}
	return &carrier.TrackingStatus{TrackingCode: trackingCode, Changed: false}, nil
}
func (f *fakeLiveCarrier) RequestRedelivery(_ context.Context, _, _, _ string) error { return nil }
func (f *fakeLiveCarrier) TestConnection(_ context.Context, _ string) error          { return nil }
func (f *fakeLiveCarrier) SetToken(_ string)                                         {}

func TestPollerRunCatchesMissedWebhooksIdempotentlyAndIsolatesErrors(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := newPollerMemRepo()

	// 1. Live GHN carrier that fails on one tracking code and succeeds on another.
	ghnLive := &fakeLiveCarrier{
		code: carrier.CodeGHN,
		statusFn: func(_ context.Context, tracking string) (*carrier.TrackingStatus, error) {
			if tracking == "LIVE-ord-err" {
				return nil, errors.New("ghn upstream 503")
			}
			return &carrier.TrackingStatus{
				TrackingCode: tracking,
				Status:       carrier.StatusDelivering,
				RawStatus:    "delivering",
				Description:  "Synced from GHN",
				OccurredAt:   time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC),
				Changed:      true,
			}, nil
		},
	}
	// 2. Pollable GHTK mock adapter (using SetMockStatus).
	ghtk := carrier.NewGHTKAdapter(carrier.GHTKConfig{Mode: "mock"})
	agg := carrier.NewAggregator(logger, ghnLive, ghtk)
	svc := service.NewShippingService(repo, agg, nil, nil, logger)

	// Create 4 shipments:
	// - ord-err (GHN, returns carrier error -> must not block others)
	// - ord-ghn-ok (GHN, missed delivering webhook)
	// - ord-ghtk-ret (GHTK return shipment, missed returned webhook code "21")
	// - ord-terminal (GHN, already delivered -> must NOT be polled)
	_, _, err := svc.CreateShipment(context.Background(), service.CreateShipmentInput{
		OrderID:     "ord-err",
		ShopID:      "shop-1",
		CarrierCode: carrier.CodeGHN,
	})
	if err != nil {
		t.Fatalf("create ord-err: %v", err)
	}
	ghnSh, _, err := svc.CreateShipment(context.Background(), service.CreateShipmentInput{
		OrderID:     "ord-ghn-ok",
		ShopID:      "shop-1",
		CarrierCode: carrier.CodeGHN,
	})
	if err != nil {
		t.Fatalf("create ord-ghn-ok: %v", err)
	}
	retSh, _, err := svc.CreateReturnShipment(context.Background(), service.CreateReturnShipmentInput{
		OrderID:         "ord-ghtk-ret",
		ReturnRequestID: "ret-poll-1",
		ShopID:          "shop-1",
		CarrierCode:     carrier.CodeGHTK,
	})
	if err != nil {
		t.Fatalf("create return shipment: %v", err)
	}
	termSh, _, err := svc.CreateShipment(context.Background(), service.CreateShipmentInput{
		OrderID:     "ord-terminal",
		ShopID:      "shop-1",
		CarrierCode: carrier.CodeGHN,
	})
	if err != nil {
		t.Fatalf("create terminal shipment: %v", err)
	}
	// Move termSh to terminal delivered before polling starts.
	if _, _, err := svc.HandleWebhook(context.Background(), service.WebhookInput{
		EventID:      "evt-term-done",
		CarrierCode:  carrier.CodeGHN,
		TrackingCode: termSh.TrackingCode,
		RawStatus:    "delivered",
		OccurredAt:   time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("mark terminal: %v", err)
	}

	// Stage GHTK return status "21" (returned).
	ghtk.SetMockStatus(retSh.TrackingCode, "21")

	// Verify New defaults on zero interval & nil logger.
	defPoller := New(svc, 0, nil)
	if defPoller.interval != 60*time.Second || defPoller.logger == nil {
		t.Fatalf("expected default 60s interval and non-nil logger, got %+v", defPoller)
	}

	// Run poller across multiple ticks (~90ms with 15ms interval) to verify both catching & idempotent re-poll.
	p := New(svc, 15*time.Millisecond, logger)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		p.Run(ctx)
		close(done)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		gotGHN, _, _ := svc.GetShipment(context.Background(), ghnSh.OrderID, "", store.DirectionForward)
		gotRet, _, _ := svc.GetShipment(context.Background(), retSh.OrderID, "", store.DirectionReturn)
		if gotGHN != nil && gotGHN.Status == carrier.StatusDelivering &&
			gotRet != nil && gotRet.Status == carrier.StatusReturned {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	// Allow at least 2 extra poll ticks to prove idempotency (no duplicate outbox events).
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	gotGHN, _, err := svc.GetShipment(context.Background(), ghnSh.OrderID, "", store.DirectionForward)
	if err != nil || gotGHN.Status != carrier.StatusDelivering {
		t.Fatalf("expected GHN shipment delivering after poll, got %+v err=%v", gotGHN, err)
	}
	gotRet, _, err := svc.GetShipment(context.Background(), retSh.OrderID, "", store.DirectionReturn)
	if err != nil || gotRet.Status != carrier.StatusReturned {
		t.Fatalf("expected GHTK return shipment returned after poll, got %+v err=%v", gotRet, err)
	}

	// Outbox must contain: 1 initial terminal webhook + 1 GHN delivering + 1 GHTK return returned = 3 total (zero duplicates!).
	outbox := repo.outboxSnapshot()
	if len(outbox) != 3 {
		t.Fatalf("expected exactly 3 outbox events (no duplicate poll events), got %d", len(outbox))
	}

	foundReturnPayload := false
	for _, ev := range outbox {
		var payload events.ShipmentUpdatedPayload
		if err := json.Unmarshal(ev.Payload, &payload); err != nil {
			t.Fatalf("unmarshal outbox payload: %v", err)
		}
		if payload.OrderID == "ord-ghtk-ret" && payload.Direction == store.DirectionReturn &&
			payload.ReturnRequestID == "ret-poll-1" && payload.Status == carrier.StatusReturned {
			foundReturnPayload = true
		}
	}
	if !foundReturnPayload {
		t.Fatalf("expected shipment.updated outbox payload with direction=RETURN & return_request_id=ret-poll-1")
	}
}
