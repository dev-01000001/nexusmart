package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nexusmart/core/services/shipping/internal/address"
	"github.com/nexusmart/core/services/shipping/internal/carrier"
	"github.com/nexusmart/core/services/shipping/internal/events"
	"github.com/nexusmart/core/services/shipping/internal/store"
)

type memRepo struct {
	mu          sync.Mutex
	provinces   []address.Province
	wards       []address.Ward
	legacy      []address.LegacyMapping
	shipments   map[string]*store.Shipment // keyed by orderID
	byTracking  map[string]*store.Shipment // keyed by trackingCode
	events      map[string][]store.ShipmentEvent
	webhookSeen map[string]bool
	outbox      []store.OutboxEvent
	processed   map[string]bool
}

func newMemRepo() *memRepo {
	return &memRepo{
		provinces:   address.Provinces(),
		wards:       address.Wards(),
		legacy:      address.LegacyMappings(),
		shipments:   make(map[string]*store.Shipment),
		byTracking:  make(map[string]*store.Shipment),
		events:      make(map[string][]store.ShipmentEvent),
		webhookSeen: make(map[string]bool),
		processed:   make(map[string]bool),
	}
}

func (m *memRepo) ListProvinces(_ context.Context, query string) ([]address.Province, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	var out []address.Province
	for _, p := range m.provinces {
		if q == "" || strings.Contains(strings.ToLower(p.Name), q) || p.Code == q {
			out = append(out, p)
		}
	}
	return out, nil
}

func (m *memRepo) ListWards(_ context.Context, provinceCode, query string) ([]address.Ward, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	var out []address.Ward
	for _, w := range m.wards {
		if w.ProvinceCode == provinceCode && (q == "" || strings.Contains(strings.ToLower(w.Name), q)) {
			out = append(out, w)
		}
	}
	return out, nil
}

func (m *memRepo) GetWard(_ context.Context, wardCode string) (*address.Ward, error) {
	for _, w := range m.wards {
		if w.Code == wardCode {
			cp := w
			return &cp, nil
		}
	}
	return nil, store.ErrNotFound
}

func (m *memRepo) MapLegacyAddress(_ context.Context, legacyProv, legacyDist, legacyWard string) (*address.LegacyMatch, error) {
	pNorm := address.NormalizeName(legacyProv)
	dNorm := address.NormalizeName(legacyDist)
	wNorm := address.NormalizeName(legacyWard)

	var best *address.LegacyMapping
	bestScore := 0
	for i := range m.legacy {
		cand := &m.legacy[i]
		if address.NormalizeName(cand.LegacyProvince) != pNorm {
			continue
		}
		cd := address.NormalizeName(cand.LegacyDistrict)
		cw := address.NormalizeName(cand.LegacyWard)
		score := 0
		if dNorm != "" && wNorm != "" && cd == dNorm && cw == wNorm {
			score = 3
		} else if dNorm != "" && cd == dNorm {
			score = 2
		} else if cd == "" && cw == "" {
			score = 1
		}
		if score > bestScore {
			bestScore = score
			best = cand
		}
	}
	if best == nil {
		return &address.LegacyMatch{Found: false}, nil
	}
	var pName, wName string
	for _, p := range m.provinces {
		if p.Code == best.ProvinceCode {
			pName = p.Name
			break
		}
	}
	for _, w := range m.wards {
		if w.Code == best.WardCode {
			wName = w.Name
			break
		}
	}
	return &address.LegacyMatch{
		Found:              true,
		ProvinceCode:       best.ProvinceCode,
		ProvinceName:       pName,
		WardCode:           best.WardCode,
		WardName:           wName,
		LegacyProvinceCode: best.LegacyProvinceCode,
		LegacyDistrictCode: best.LegacyDistrictCode,
		LegacyWardCode:     best.LegacyWardCode,
	}, nil
}

func (m *memRepo) GetShipmentByOrderID(_ context.Context, orderID string) (*store.Shipment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sh, ok := m.shipments[orderID]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *sh
	return &cp, nil
}

func (m *memRepo) GetShipmentByTrackingCode(_ context.Context, trackingCode string) (*store.Shipment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sh, ok := m.byTracking[trackingCode]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *sh
	return &cp, nil
}

func (m *memRepo) ListShipmentEvents(_ context.Context, shipmentID string) ([]store.ShipmentEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ev := m.events[shipmentID]
	out := make([]store.ShipmentEvent, len(ev))
	copy(out, ev)
	return out, nil
}

func (m *memRepo) CreateShipment(_ context.Context, sh *store.Shipment, initialEvent *store.ShipmentEvent) (*store.Shipment, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.shipments[sh.OrderID]; ok {
		cp := *existing
		return &cp, true, nil
	}
	cp := *sh
	m.shipments[sh.OrderID] = &cp
	m.byTracking[sh.TrackingCode] = &cp
	if initialEvent != nil {
		m.events[sh.ID] = append(m.events[sh.ID], *initialEvent)
	}
	ret := cp
	return &ret, false, nil
}

func (m *memRepo) CancelShipment(_ context.Context, orderID, reason string) (*store.Shipment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sh, ok := m.shipments[orderID]
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
	m.events[sh.ID] = append(m.events[sh.ID], store.ShipmentEvent{
		ID:          uuid.NewString(),
		ShipmentID:  sh.ID,
		Status:      carrier.StatusCancelled,
		RawStatus:   "cancelled",
		Description: reason,
		OccurredAt:  sh.UpdatedAt,
	})
	cp := *sh
	return &cp, nil
}

func (m *memRepo) CancelShipmentWithEvent(ctx context.Context, eventID, _, orderID, reason string) (*store.Shipment, bool, error) {
	m.mu.Lock()
	if m.processed[eventID] {
		m.mu.Unlock()
		return nil, true, nil
	}
	m.processed[eventID] = true
	sh, ok := m.shipments[orderID]
	if !ok {
		m.mu.Unlock()
		return nil, false, nil
	}
	if !carrier.IsCancellableStatus(sh.Status) {
		cp := *sh
		m.mu.Unlock()
		return &cp, false, nil
	}
	m.mu.Unlock()
	updated, err := m.CancelShipment(ctx, orderID, reason)
	return updated, false, err
}

func (m *memRepo) ApplyWebhookEvent(_ context.Context, params store.WebhookUpdateParams) (*store.Shipment, store.WebhookUpdateOutcome, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.webhookSeen[params.EventID] {
		return nil, store.WebhookOutcomeDuplicate, nil
	}
	m.webhookSeen[params.EventID] = true

	var sh *store.Shipment
	if params.TrackingCode != "" {
		sh = m.byTracking[params.TrackingCode]
	}
	if sh == nil && params.OrderID != "" {
		sh = m.shipments[params.OrderID]
	}
	if sh == nil {
		return nil, "", store.ErrNotFound
	}

	if carrier.IsTerminalStatus(sh.Status) || (sh.LastEventAt != nil && params.OccurredAt.Before(*sh.LastEventAt)) {
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
			ID:          uuid.NewString(),
			EventID:     params.OutboxEventID,
			Subject:     params.OutboxSubject,
			Payload:     payload,
			Traceparent: params.Traceparent,
			TraceID:     params.TraceID,
			CreatedAt:   time.Now().UTC(),
		})
	}
	cp := *sh
	return &cp, store.WebhookOutcomeApplied, nil
}

func (m *memRepo) ListActiveShipments(_ context.Context, _ int) ([]store.Shipment, error) {
	return nil, nil
}
func (m *memRepo) FetchPendingOutbox(_ context.Context, _ int) ([]store.OutboxEvent, error) {
	return nil, nil
}
func (m *memRepo) MarkOutboxPublished(_ context.Context, _ string) error { return nil }
func (m *memRepo) IsEventProcessed(_ context.Context, eventID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.processed[eventID], nil
}
func (m *memRepo) MarkEventProcessed(_ context.Context, eventID, _ string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.processed[eventID] {
		return false, nil
	}
	m.processed[eventID] = true
	return true, nil
}
func (m *memRepo) InsertDeadLetter(_ context.Context, _, _ string, _ []byte, _ string, _ int) error {
	return nil
}

type countingCarrier struct {
	carrier.Carrier
	createCalls atomic.Int32
	cancelCalls atomic.Int32
}

func (c *countingCarrier) CreateShipment(ctx context.Context, req carrier.ShipmentRequest) (*carrier.ShipmentResult, error) {
	c.createCalls.Add(1)
	return c.Carrier.CreateShipment(ctx, req)
}

func (c *countingCarrier) CancelShipment(ctx context.Context, trackingCode string) error {
	c.cancelCalls.Add(1)
	return c.Carrier.CancelShipment(ctx, trackingCode)
}

func TestCreateShipmentIdempotentByOrderID(t *testing.T) {
	repo := newMemRepo()
	ghn := &countingCarrier{Carrier: carrier.NewGHNAdapter(carrier.GHNConfig{Mode: "mock"})}
	agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), ghn)
	svc := NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	in := CreateShipmentInput{
		OrderID:      "ord-1001",
		ShopID:       "shop-1",
		CarrierCode:  carrier.CodeGHN,
		ServiceCode:  "STANDARD",
		CODAmountVND: 250000,
		WeightGrams:  1200,
		FromAddress:  carrier.Address{ProvinceCode: "79", WardCode: "79001"},
		ToAddress:    carrier.Address{ProvinceCode: "01", WardCode: "01001"},
	}

	first, existed1, err := svc.CreateShipment(context.Background(), in)
	if err != nil {
		t.Fatalf("first CreateShipment failed: %v", err)
	}
	if existed1 {
		t.Fatalf("expected alreadyExisted=false on first call")
	}

	second, existed2, err := svc.CreateShipment(context.Background(), in)
	if err != nil {
		t.Fatalf("second CreateShipment failed: %v", err)
	}
	if !existed2 {
		t.Fatalf("expected alreadyExisted=true on replay")
	}
	if first.TrackingCode != second.TrackingCode || first.ID != second.ID {
		t.Fatalf("replay returned different shipment: first=%+v second=%+v", first, second)
	}
	if got := ghn.createCalls.Load(); got != 1 {
		t.Fatalf("carrier CreateShipment must be called exactly once, got %d", got)
	}
}

func TestWebhookIdempotencyOutOfOrderAndOutbox(t *testing.T) {
	repo := newMemRepo()
	ghn := carrier.NewGHNAdapter(carrier.GHNConfig{Mode: "mock"})
	agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), ghn)
	svc := NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	sh, _, err := svc.CreateShipment(context.Background(), CreateShipmentInput{
		OrderID:     "ord-wh-1",
		ShopID:      "shop-7",
		CarrierCode: carrier.CodeGHN,
		WeightGrams: 500,
	})
	if err != nil {
		t.Fatalf("CreateShipment failed: %v", err)
	}

	t1 := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	tOlder := time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC)
	t2 := time.Date(2026, 11, 20, 11, 0, 0, 0, time.UTC)

	// 1. Valid delivering event.
	updated, outcome, err := svc.HandleWebhook(context.Background(), WebhookInput{
		EventID:      "evt-1",
		CarrierCode:  "ghn",
		TrackingCode: sh.TrackingCode,
		RawStatus:    "delivering",
		Description:  "Đang giao hàng",
		OccurredAt:   t1,
	})
	if err != nil || outcome != store.WebhookOutcomeApplied || updated.Status != carrier.StatusDelivering {
		t.Fatalf("expected applied delivering, got sh=%+v outcome=%s err=%v", updated, outcome, err)
	}
	if len(repo.outbox) != 1 {
		t.Fatalf("expected 1 outbox event, got %d", len(repo.outbox))
	}

	// Verify canonical shipment.updated payload fields.
	var payload events.ShipmentUpdatedPayload
	if err := json.Unmarshal(repo.outbox[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal outbox payload: %v", err)
	}
	if payload.OrderID != "ord-wh-1" || payload.ShopID != "shop-7" || payload.Status != carrier.StatusDelivering {
		t.Fatalf("unexpected shipment.updated payload: %+v", payload)
	}

	// 2. Duplicate event_id must be ignored without creating another outbox entry.
	_, dupOutcome, err := svc.HandleWebhook(context.Background(), WebhookInput{
		EventID:      "evt-1",
		CarrierCode:  "ghn",
		TrackingCode: sh.TrackingCode,
		RawStatus:    "delivering",
		OccurredAt:   t1,
	})
	if err != nil || dupOutcome != store.WebhookOutcomeDuplicate {
		t.Fatalf("expected duplicate outcome, got outcome=%s err=%v", dupOutcome, err)
	}
	if len(repo.outbox) != 1 {
		t.Fatalf("duplicate webhook must not emit outbox event, got %d", len(repo.outbox))
	}

	// 3. Out-of-order stale event (occurred_at < last_event_at) must be ignored (UC-SHIP04).
	staleSh, staleOutcome, err := svc.HandleWebhook(context.Background(), WebhookInput{
		EventID:      "evt-stale",
		CarrierCode:  "ghn",
		TrackingCode: sh.TrackingCode,
		RawStatus:    "picking",
		OccurredAt:   tOlder,
	})
	if err != nil || staleOutcome != store.WebhookOutcomeStale {
		t.Fatalf("expected stale outcome, got outcome=%s err=%v", staleOutcome, err)
	}
	if staleSh.Status != carrier.StatusDelivering {
		t.Fatalf("stale webhook must not regress shipment status, got %s", staleSh.Status)
	}
	if len(repo.outbox) != 1 {
		t.Fatalf("stale webhook must not emit outbox event, got %d", len(repo.outbox))
	}

	// 4. Newer delivered event applies cleanly.
	deliveredSh, deliveredOutcome, err := svc.HandleWebhook(context.Background(), WebhookInput{
		EventID:      "evt-2",
		CarrierCode:  "ghn",
		TrackingCode: sh.TrackingCode,
		RawStatus:    "delivered",
		Description:  "Giao hàng thành công",
		OccurredAt:   t2,
	})
	if err != nil || deliveredOutcome != store.WebhookOutcomeApplied || deliveredSh.Status != carrier.StatusDelivered {
		t.Fatalf("expected applied delivered, got sh=%+v outcome=%s err=%v", deliveredSh, deliveredOutcome, err)
	}
	if len(repo.outbox) != 2 {
		t.Fatalf("expected 2 outbox events after delivered, got %d", len(repo.outbox))
	}

	// 5. Late non-terminal webhook (even with newer timestamp!) on delivered shipment must be rejected as stale.
	regSh, regOutcome, err := svc.HandleWebhook(context.Background(), WebhookInput{
		EventID:      "evt-late-delivering",
		CarrierCode:  "ghn",
		TrackingCode: sh.TrackingCode,
		RawStatus:    "delivering",
		OccurredAt:   t2.Add(15 * time.Minute),
	})
	if err != nil || regOutcome != store.WebhookOutcomeStale || regSh.Status != carrier.StatusDelivered {
		t.Fatalf("expected stale rejection for non-terminal webhook on delivered shipment, got sh=%+v outcome=%s err=%v", regSh, regOutcome, err)
	}
	if len(repo.outbox) != 2 {
		t.Fatalf("rejected terminal regression webhook must not emit outbox event, got %d", len(repo.outbox))
	}

	// 6. CancelShipment must reject delivered shipment.
	if _, err := svc.CancelShipment(context.Background(), "ord-wh-1", "late cancel"); !errors.Is(err, ErrNotCancellable) {
		t.Fatalf("expected ErrNotCancellable for delivered shipment, got %v", err)
	}

	// 7. Store-level cancel guard rejects overwriting a delivering/delivered shipment even if raced.
	if _, err := repo.CancelShipment(context.Background(), "ord-wh-1", "raced cancel"); !errors.Is(err, store.ErrCannotCancel) {
		t.Fatalf("expected store.ErrCannotCancel for delivered shipment, got %v", err)
	}
}

func TestCancelledShipmentRejectsLateNonTerminalWebhook(t *testing.T) {
	repo := newMemRepo()
	ghn := carrier.NewGHNAdapter(carrier.GHNConfig{Mode: "mock"})
	agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), ghn)
	svc := NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	sh, _, err := svc.CreateShipment(context.Background(), CreateShipmentInput{
		OrderID:     "ord-term-cancel",
		ShopID:      "shop-1",
		CarrierCode: carrier.CodeGHN,
	})
	if err != nil {
		t.Fatalf("CreateShipment failed: %v", err)
	}
	if _, err := svc.CancelShipment(context.Background(), "ord-term-cancel", "buyer cancelled"); err != nil {
		t.Fatalf("CancelShipment failed: %v", err)
	}

	// Late non-terminal webhook arriving after cancellation must NOT overwrite cancelled status.
	res, outcome, err := svc.HandleWebhook(context.Background(), WebhookInput{
		EventID:      "evt-after-cancel",
		CarrierCode:  "ghn",
		TrackingCode: sh.TrackingCode,
		RawStatus:    "picking",
		OccurredAt:   time.Now().UTC().Add(5 * time.Minute),
	})
	if err != nil || outcome != store.WebhookOutcomeStale || res.Status != carrier.StatusCancelled {
		t.Fatalf("expected stale rejection on cancelled shipment, got sh=%+v outcome=%s err=%v", res, outcome, err)
	}
	if len(repo.outbox) != 0 {
		t.Fatalf("expected 0 outbox events for late webhook on cancelled shipment, got %d", len(repo.outbox))
	}
}

func TestLegacyAddressMapping(t *testing.T) {
	repo := newMemRepo()
	agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc := NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Merged legacy province: Bình Dương -> Thành phố Hồ Chí Minh (79)
	match, err := svc.MapLegacyAddress(context.Background(), "Tỉnh Bình Dương", "TP Thủ Dầu Một", "Phường Phú Cường")
	if err != nil {
		t.Fatalf("MapLegacyAddress failed: %v", err)
	}
	if !match.Found || match.ProvinceCode != "79" || match.WardCode != "79006" || match.LegacyProvinceCode != "74" {
		t.Fatalf("unexpected Binh Duong -> HCMC mapping: %+v", match)
	}

	// Merged legacy province without accents: Quang Nam -> Da Nang (48)
	match2, err := svc.MapLegacyAddress(context.Background(), "Quang Nam", "Hoi An", "")
	if err != nil {
		t.Fatalf("MapLegacyAddress failed: %v", err)
	}
	if !match2.Found || match2.ProvinceCode != "48" || match2.WardCode != "48005" {
		t.Fatalf("unexpected Quang Nam -> Da Nang mapping: %+v", match2)
	}

	// Province-only fallback for another merged province: Ha Giang -> Tuyen Quang (08)
	match3, err := svc.MapLegacyAddress(context.Background(), "Hà Giang", "", "")
	if err != nil {
		t.Fatalf("MapLegacyAddress failed: %v", err)
	}
	if !match3.Found || match3.ProvinceCode != "08" {
		t.Fatalf("unexpected Ha Giang -> Tuyen Quang mapping: %+v", match3)
	}
}

func TestOrderCancelledCancelsPrePickupShipment(t *testing.T) {
	repo := newMemRepo()
	ghn := &countingCarrier{Carrier: carrier.NewGHNAdapter(carrier.GHNConfig{Mode: "mock"})}
	agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), ghn)
	svc := NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	_, _, err := svc.CreateShipment(context.Background(), CreateShipmentInput{
		OrderID:     "ord-cancel-1",
		ShopID:      "shop-1",
		CarrierCode: carrier.CodeGHN,
	})
	if err != nil {
		t.Fatalf("CreateShipment failed: %v", err)
	}

	if err := svc.HandleOrderCancelled(context.Background(), "evt-cancel-1", "ord-cancel-1", "buyer cancelled"); err != nil {
		t.Fatalf("HandleOrderCancelled failed: %v", err)
	}

	sh, _, err := svc.GetShipment(context.Background(), "ord-cancel-1", "")
	if err != nil {
		t.Fatalf("GetShipment failed: %v", err)
	}
	if sh.Status != carrier.StatusCancelled {
		t.Fatalf("expected cancelled status, got %s", sh.Status)
	}
	if ghn.cancelCalls.Load() != 1 {
		t.Fatalf("expected 1 carrier cancel call, got %d", ghn.cancelCalls.Load())
	}
}
