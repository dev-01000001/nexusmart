package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nexusmart/core/services/shipping/internal/address"
	"github.com/nexusmart/core/services/shipping/internal/carrier"
	"github.com/nexusmart/core/services/shipping/internal/events"
	"github.com/nexusmart/core/services/shipping/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type memRepo struct {
	mu           sync.Mutex
	provinces    []address.Province
	wards        []address.Ward
	legacy       []address.LegacyMapping
	shipments    []*store.Shipment
	byTracking   map[string]*store.Shipment // keyed by trackingCode
	events       map[string][]store.ShipmentEvent
	webhookSeen  map[string]bool
	outbox       []store.OutboxEvent
	processed    map[string]bool
	shopSettings map[string]*store.ShopShippingSettings
	carriers     map[string]*store.CarrierConfig
	fallback     map[string][]store.FallbackRate
	fallbackGets atomic.Int32
	locks        map[string]chan struct{}
}

func newMemRepo() *memRepo {
	return &memRepo{
		provinces:    address.Provinces(),
		wards:        address.Wards(),
		legacy:       address.LegacyMappings(),
		byTracking:   make(map[string]*store.Shipment),
		events:       make(map[string][]store.ShipmentEvent),
		webhookSeen:  make(map[string]bool),
		processed:    make(map[string]bool),
		shopSettings: make(map[string]*store.ShopShippingSettings),
		carriers:     make(map[string]*store.CarrierConfig),
		fallback:     make(map[string][]store.FallbackRate),
		locks:        make(map[string]chan struct{}),
	}
}

func (m *memRepo) ListProvinces(_ context.Context, query string) ([]address.Province, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	qNorm := address.StripDiacritics(query)
	qAlias := address.NormalizeName(query)
	var out []address.Province
	for _, p := range m.provinces {
		pNorm := address.StripDiacritics(p.Name)
		if q == "" || strings.Contains(strings.ToLower(p.Name), q) ||
			(qNorm != "" && strings.Contains(pNorm, qNorm)) ||
			(qAlias != "" && strings.Contains(pNorm, qAlias)) ||
			p.Code == q {
			out = append(out, p)
		}
	}
	return out, nil
}

func (m *memRepo) GetProvince(_ context.Context, provinceCode string) (*address.Province, error) {
	for _, p := range m.provinces {
		if p.Code == provinceCode {
			cp := p
			return &cp, nil
		}
	}
	return nil, store.ErrNotFound
}

func (m *memRepo) ListWards(_ context.Context, provinceCode, query string) ([]address.Ward, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	qNorm := address.StripDiacritics(query)
	qAlias := address.NormalizeName(query)
	var out []address.Ward
	for _, w := range m.wards {
		wNorm := address.StripDiacritics(w.Name)
		if w.ProvinceCode == provinceCode && (q == "" || strings.Contains(strings.ToLower(w.Name), q) ||
			(qNorm != "" && strings.Contains(wNorm, qNorm)) ||
			(qAlias != "" && strings.Contains(wNorm, qAlias)) ||
			w.Code == q) {
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

func (m *memRepo) findByOrderLocked(orderID, direction string, activeOnly bool) *store.Shipment {
	dir := store.NormalizeDirection(direction)
	var latestActive *store.Shipment
	var latestInactive *store.Shipment
	for _, sh := range m.shipments {
		if sh.OrderID == orderID && sh.Direction == dir {
			if !store.IsInactiveShipment(dir, sh.Status) {
				if latestActive == nil || !sh.CreatedAt.Before(latestActive.CreatedAt) {
					latestActive = sh
				}
			} else if latestInactive == nil || !sh.CreatedAt.Before(latestInactive.CreatedAt) {
				latestInactive = sh
			}
		}
	}
	if latestActive != nil {
		return latestActive
	}
	if activeOnly {
		return nil
	}
	return latestInactive
}

func (m *memRepo) GetShipmentByOrderID(_ context.Context, orderID, direction string) (*store.Shipment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sh := m.findByOrderLocked(orderID, direction, false)
	if sh == nil {
		return nil, store.ErrNotFound
	}
	cp := *sh
	return &cp, nil
}

func (m *memRepo) GetActiveShipmentByOrderID(_ context.Context, orderID, direction string) (*store.Shipment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sh := m.findByOrderLocked(orderID, direction, true)
	if sh == nil {
		return nil, store.ErrNotFound
	}
	cp := *sh
	return &cp, nil
}

func (m *memRepo) CountInactiveShipmentsByOrder(_ context.Context, orderID, direction string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	dir := store.NormalizeDirection(direction)
	count := 0
	for _, sh := range m.shipments {
		if sh.OrderID == orderID && sh.Direction == dir && store.IsInactiveShipment(dir, sh.Status) {
			count++
		}
	}
	return count, nil
}

func (m *memRepo) AcquireOrderDirectionLock(ctx context.Context, orderID, direction string) (func(), error) {
	key := store.NormalizeDirection(direction) + ":" + strings.TrimSpace(orderID)
	m.mu.Lock()
	ch, ok := m.locks[key]
	if !ok {
		ch = make(chan struct{}, 1)
		m.locks[key] = ch
	}
	m.mu.Unlock()

	select {
	case ch <- struct{}{}:
		var once sync.Once
		return func() {
			once.Do(func() { <-ch })
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
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
	dir := store.NormalizeDirection(sh.Direction)
	if existing := m.findByOrderLocked(sh.OrderID, dir, true); existing != nil {
		cp := *existing
		return &cp, true, nil
	}
	cp := *sh
	cp.Direction = dir
	m.shipments = append(m.shipments, &cp)
	m.byTracking[cp.TrackingCode] = &cp
	if initialEvent != nil {
		ev := *initialEvent
		if ev.CreatedAt.IsZero() {
			ev.CreatedAt = time.Now().UTC()
		}
		m.events[cp.ID] = append(m.events[cp.ID], ev)
	}
	ret := cp
	return &ret, false, nil
}

func (m *memRepo) CancelShipmentByID(_ context.Context, shipmentID, reason string) (*store.Shipment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var sh *store.Shipment
	for _, cand := range m.shipments {
		if cand.ID == shipmentID {
			sh = cand
			break
		}
	}
	if sh == nil {
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
	desc := strings.TrimSpace(reason)
	if desc == "" {
		desc = "Vận đơn đã bị huỷ trước khi hãng lấy hàng"
	} else {
		desc = "Vận đơn đã bị huỷ: " + desc
	}
	m.events[sh.ID] = append(m.events[sh.ID], store.ShipmentEvent{
		ID:          uuid.NewString(),
		ShipmentID:  sh.ID,
		Status:      carrier.StatusCancelled,
		RawStatus:   "cancelled",
		Description: desc,
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
	sh := m.findByOrderLocked(orderID, store.DirectionForward, true)
	if sh == nil {
		m.mu.Unlock()
		return nil, false, nil
	}
	if !carrier.IsCancellableStatus(sh.Status) {
		cp := *sh
		m.mu.Unlock()
		return &cp, false, nil
	}
	shID := sh.ID
	m.mu.Unlock()
	updated, err := m.CancelShipmentByID(ctx, shID, reason)
	return updated, false, err
}

func (m *memRepo) RecordRedelivery(_ context.Context, shipmentID, buyerID string, maxRedelivery int32, preferredDate, note string) (*store.Shipment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var sh *store.Shipment
	for _, cand := range m.shipments {
		if cand.ID == shipmentID {
			sh = cand
			break
		}
	}
	if sh == nil {
		return nil, store.ErrNotFound
	}
	if sh.Direction != store.DirectionForward {
		return nil, store.ErrRedeliveryNotAllowed
	}
	if strings.TrimSpace(sh.BuyerID) == "" {
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
	for _, ev := range m.events[sh.ID] {
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
	m.events[sh.ID] = append(m.events[sh.ID], store.ShipmentEvent{
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

func (m *memRepo) ApplyWebhookEvent(_ context.Context, params store.WebhookUpdateParams) (*store.Shipment, store.WebhookUpdateOutcome, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var sh *store.Shipment
	if params.TrackingCode != "" {
		sh = m.byTracking[params.TrackingCode]
	} else if params.OrderID != "" {
		sh = m.findByOrderLocked(params.OrderID, store.DirectionForward, false)
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

	if carrier.IsTerminalStatus(sh.Status) ||
		!store.CanTransitionStatus(sh.Status, params.InternalStatus) ||
		(sh.LastEventAt != nil && params.OccurredAt.Before(*sh.LastEventAt)) {
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
		CreatedAt:   sh.UpdatedAt,
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

func (m *memRepo) ListActiveShipments(_ context.Context, _ int, _ *time.Time, _ string) ([]store.Shipment, error) {
	return nil, nil
}
func (m *memRepo) GetShopShippingSettings(_ context.Context, shopID string) (*store.ShopShippingSettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.shopSettings[shopID]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *st
	cp.EnabledCarrierCodes = append([]string(nil), st.EnabledCarrierCodes...)
	return &cp, nil
}
func (m *memRepo) UpsertShopShippingSettings(_ context.Context, st *store.ShopShippingSettings) (*store.ShopShippingSettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *st
	cp.EnabledCarrierCodes = append([]string(nil), st.EnabledCarrierCodes...)
	cp.UpdatedAt = time.Now().UTC()
	m.shopSettings[st.ShopID] = &cp
	ret := cp
	return &ret, nil
}
func (m *memRepo) ListCarrierConfigs(_ context.Context) ([]store.CarrierConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]store.CarrierConfig, 0, len(m.carriers))
	for _, c := range m.carriers {
		cp := *c
		if len(c.EncryptedToken) > 0 {
			cp.EncryptedToken = append([]byte(nil), c.EncryptedToken...)
		}
		out = append(out, cp)
	}
	return out, nil
}
func (m *memRepo) GetCarrierConfig(_ context.Context, carrierCode string) (*store.CarrierConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.carriers[strings.ToLower(carrierCode)]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *c
	if len(c.EncryptedToken) > 0 {
		cp.EncryptedToken = append([]byte(nil), c.EncryptedToken...)
	}
	return &cp, nil
}
func (m *memRepo) UpdateCarrierConfig(_ context.Context, cfg *store.CarrierConfig, rates []store.FallbackRate) (*store.CarrierConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	code := strings.ToLower(cfg.CarrierCode)
	cp := *cfg
	cp.CarrierCode = code
	if len(cfg.EncryptedToken) > 0 {
		cp.EncryptedToken = append([]byte(nil), cfg.EncryptedToken...)
	} else if prev, ok := m.carriers[code]; ok && len(prev.EncryptedToken) > 0 {
		cp.EncryptedToken = append([]byte(nil), prev.EncryptedToken...)
	}
	cp.UpdatedAt = time.Now().UTC()
	m.carriers[code] = &cp
	if len(rates) > 0 {
		byRegion := make(map[string]store.FallbackRate)
		for _, prev := range m.fallback[code] {
			byRegion[prev.Region] = prev
		}
		for _, r := range rates {
			r.CarrierCode = code
			r.UpdatedAt = cp.UpdatedAt
			byRegion[r.Region] = r
		}
		merged := make([]store.FallbackRate, 0, len(byRegion))
		for _, r := range byRegion {
			merged = append(merged, r)
		}
		m.fallback[code] = merged
	}
	ret := cp
	return &ret, nil
}
func (m *memRepo) SeedCarrierTokenIfEmpty(_ context.Context, carrierCode string, encryptedToken []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	code := strings.ToLower(carrierCode)
	c, ok := m.carriers[code]
	if !ok {
		m.carriers[code] = &store.CarrierConfig{
			CarrierCode:    code,
			Name:           code,
			Enabled:        true,
			EncryptedToken: append([]byte(nil), encryptedToken...),
			UpdatedAt:      time.Now().UTC(),
		}
		return nil
	}
	if len(c.EncryptedToken) == 0 {
		c.EncryptedToken = append([]byte(nil), encryptedToken...)
	}
	return nil
}
func (m *memRepo) ListFallbackRates(_ context.Context, carrierCode string) ([]store.FallbackRate, error) {
	m.fallbackGets.Add(1)
	m.mu.Lock()
	defer m.mu.Unlock()
	if carrierCode == "" {
		var all []store.FallbackRate
		for _, list := range m.fallback {
			all = append(all, list...)
		}
		return all, nil
	}
	return append([]store.FallbackRate(nil), m.fallback[strings.ToLower(carrierCode)]...), nil
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
	if _, err := svc.CancelShipment(context.Background(), "ord-wh-1", store.DirectionForward, "late cancel"); !errors.Is(err, ErrNotCancellable) {
		t.Fatalf("expected ErrNotCancellable for delivered shipment, got %v", err)
	}

	// 7. Store-level cancel guard rejects overwriting a delivering/delivered shipment even if raced.
	if _, err := repo.CancelShipmentByID(context.Background(), sh.ID, "raced cancel"); !errors.Is(err, store.ErrCannotCancel) {
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
	if _, err := svc.CancelShipment(context.Background(), "ord-term-cancel", store.DirectionForward, "buyer cancelled"); err != nil {
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

	sh, _, err := svc.GetShipment(context.Background(), "ord-cancel-1", "", store.DirectionForward)
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

func TestCalculateFee_RejectsMissingOriginAndShop(t *testing.T) {
	repo := newMemRepo()
	ghn := carrier.NewGHNAdapter(carrier.GHNConfig{Mode: "mock"})
	agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), ghn)
	svc := NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	_, err := svc.CalculateFee(context.Background(), carrier.FeeRequest{
		ToProvinceCode: "79",
		ToWardCode:     "79001",
		WeightGrams:    250,
		CODAmountVND:   200000,
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument when both shop_id and origin are empty, got err=%v", err)
	}
}

func TestCalculateFee_ValidationAndOutOfServiceArea(t *testing.T) {
	repo := newMemRepo()
	ghn := carrier.NewGHNAdapter(carrier.GHNConfig{Mode: "mock"})
	mockExp := carrier.NewMockExpressAdapter()
	agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), ghn, mockExp)
	svc := NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	cases := []struct {
		name string
		req  carrier.FeeRequest
	}{
		{
			name: "zero weight",
			req: carrier.FeeRequest{
				FromProvinceCode: "79", FromWardCode: "79001",
				ToProvinceCode: "79", ToWardCode: "79001",
				WeightGrams: 0,
			},
		},
		{
			name: "negative weight",
			req: carrier.FeeRequest{
				FromProvinceCode: "79", FromWardCode: "79001",
				ToProvinceCode: "79", ToWardCode: "79001",
				WeightGrams: -100,
			},
		},
		{
			name: "unknown destination province",
			req: carrier.FeeRequest{
				FromProvinceCode: "79", FromWardCode: "79001",
				ToProvinceCode: "99999", ToWardCode: "99999",
				WeightGrams: 250,
			},
		},
		{
			name: "legacy unmerged province code not in 34 provinces",
			req: carrier.FeeRequest{
				FromProvinceCode: "79", FromWardCode: "79001",
				ToProvinceCode: "74",
				WeightGrams:    250,
			},
		},
		{
			name: "unknown destination ward",
			req: carrier.FeeRequest{
				FromProvinceCode: "79", FromWardCode: "79001",
				ToProvinceCode: "79", ToWardCode: "79999",
				WeightGrams: 250,
			},
		},
		{
			name: "destination ward belongs to different province",
			req: carrier.FeeRequest{
				FromProvinceCode: "79", FromWardCode: "79001",
				ToProvinceCode: "79", ToWardCode: "01001",
				WeightGrams: 250,
			},
		},
		{
			name: "unknown origin province",
			req: carrier.FeeRequest{
				FromProvinceCode: "99999",
				ToProvinceCode:   "79", ToWardCode: "79001",
				WeightGrams: 250,
			},
		},
		{
			name: "unknown origin ward",
			req: carrier.FeeRequest{
				FromProvinceCode: "79", FromWardCode: "79999",
				ToProvinceCode: "79", ToWardCode: "79001",
				WeightGrams: 250,
			},
		},
		{
			name: "origin ward belongs to different province",
			req: carrier.FeeRequest{
				FromProvinceCode: "79", FromWardCode: "01001",
				ToProvinceCode: "79", ToWardCode: "79001",
				WeightGrams: 250,
			},
		},
		{
			name: "missing destination province",
			req: carrier.FeeRequest{
				FromProvinceCode: "79", FromWardCode: "79001",
				WeightGrams: 250,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			quotes, err := svc.CalculateFee(context.Background(), tc.req)
			if err == nil {
				t.Fatalf("expected error for %s, got quotes=%+v", tc.name, quotes)
			}
		})
	}
}

func TestHandleWebhook_RejectsCarrierMismatch(t *testing.T) {
	repo := newMemRepo()
	ghn := carrier.NewGHNAdapter(carrier.GHNConfig{Mode: "mock"})
	agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), ghn)
	svc := NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	sh, _, err := svc.CreateShipment(context.Background(), CreateShipmentInput{
		OrderID:     "ord-mismatch-1",
		ShopID:      "shop-1",
		CarrierCode: carrier.CodeGHN,
		WeightGrams: 500,
	})
	if err != nil {
		t.Fatalf("CreateShipment failed: %v", err)
	}

	_, _, err = svc.HandleWebhook(context.Background(), WebhookInput{
		EventID:      "evt-wrong-carrier-1",
		CarrierCode:  "fake_carrier",
		TrackingCode: sh.TrackingCode,
		RawStatus:    "delivering",
		OccurredAt:   time.Now().UTC(),
	})
	if err == nil {
		t.Fatalf("expected error on mismatched webhook carrier, got nil")
	}

	after, _, getErr := svc.GetShipment(context.Background(), "ord-mismatch-1", "", store.DirectionForward)
	if getErr != nil {
		t.Fatalf("GetShipment failed: %v", getErr)
	}
	if after.Status != carrier.StatusCreated {
		t.Fatalf("shipment status must remain created after carrier mismatch, got %s", after.Status)
	}
	if len(repo.outbox) != 0 {
		t.Fatalf("expected 0 outbox events after carrier mismatch, got %d", len(repo.outbox))
	}
}

type fakeShopPickup struct {
	byShop map[string][2]string
	calls  atomic.Int32
}

func (f *fakeShopPickup) GetShopPickupCodes(_ context.Context, shopID string) (string, string, error) {
	f.calls.Add(1)
	pair, ok := f.byShop[shopID]
	if !ok {
		return "", "", status.Error(codes.NotFound, "shop not found")
	}
	return pair[0], pair[1], nil
}

type recordingQuoteCache struct {
	mu      sync.Mutex
	getReqs []carrier.FeeRequest
	setReqs []carrier.FeeRequest
	entries map[string][]carrier.Quote
}

func newRecordingQuoteCache() *recordingQuoteCache {
	return &recordingQuoteCache{entries: make(map[string][]carrier.Quote)}
}

func (c *recordingQuoteCache) keyOf(req carrier.FeeRequest) string {
	return req.FromProvinceCode + ":" + req.FromWardCode + ":" + req.ToProvinceCode + ":" + req.ToWardCode + ":" + strings.Join(req.AllowedCarriers, ",") + ":" + string(rune(req.WeightGrams))
}

func (c *recordingQuoteCache) Get(_ context.Context, req carrier.FeeRequest) ([]carrier.Quote, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.getReqs = append(c.getReqs, req)
	q, ok := c.entries[c.keyOf(req)]
	return q, ok
}

func (c *recordingQuoteCache) Set(_ context.Context, req carrier.FeeRequest, quotes []carrier.Quote) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.setReqs = append(c.setReqs, req)
	c.entries[c.keyOf(req)] = quotes
}

func TestCalculateFee_ResolvesShopPickupAndCachesRealOrigin(t *testing.T) {
	repo := newMemRepo()
	ghn := carrier.NewGHNAdapter(carrier.GHNConfig{Mode: "mock"})
	mockExp := carrier.NewMockExpressAdapter()
	agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), ghn, mockExp)
	qc := newRecordingQuoteCache()
	svc := NewShippingService(repo, agg, qc, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	pickup := &fakeShopPickup{
		byShop: map[string][2]string{
			"shop-sg":       {"79", "79001"},
			"shop-no-addr":  {"", ""},
			"shop-bad-prov": {"99999", "99999"},
		},
	}
	svc.SetShopPickupResolver(pickup)

	// 1. Quote with shop_id only (no from_province_code/from_ward_code) resolves shop pickup (79/79001)
	//    and returns intra-province TP.HCM quotes (ghn/STANDARD=24000, mock_express/STANDARD=19500).
	quotesByShop, err := svc.CalculateFee(context.Background(), carrier.FeeRequest{
		ShopID:         "shop-sg",
		ToProvinceCode: "79",
		ToWardCode:     "79001",
		WeightGrams:    250,
		CODAmountVND:   200000,
	})
	if err != nil {
		t.Fatalf("CalculateFee with shop_id failed: %v", err)
	}

	// Verify cache lookup and write both used the REAL resolved origin (79/79001), never empty.
	if len(qc.getReqs) != 1 || qc.getReqs[0].FromProvinceCode != "79" || qc.getReqs[0].FromWardCode != "79001" {
		t.Fatalf("expected cache Get with real origin 79/79001, got %+v", qc.getReqs)
	}
	if len(qc.setReqs) != 1 || qc.setReqs[0].FromProvinceCode != "79" || qc.setReqs[0].FromWardCode != "79001" {
		t.Fatalf("expected cache Set with real origin 79/79001, got %+v", qc.setReqs)
	}

	// Compare with explicit origin (what OrderService sends at checkout).
	quotesExplicit, err := svc.CalculateFee(context.Background(), carrier.FeeRequest{
		ShopID:           "shop-sg",
		FromProvinceCode: "79",
		FromWardCode:     "79001",
		ToProvinceCode:   "79",
		ToWardCode:       "79001",
		WeightGrams:      250,
		CODAmountVND:     200000,
	})
	if err != nil {
		t.Fatalf("CalculateFee with explicit origin failed: %v", err)
	}
	if len(quotesByShop) != len(quotesExplicit) {
		t.Fatalf("quote count mismatch: shop=%d explicit=%d", len(quotesByShop), len(quotesExplicit))
	}
	for i := range quotesByShop {
		if quotesByShop[i] != quotesExplicit[i] {
			t.Fatalf("quote[%d] mismatch: shop=%+v explicit=%+v", i, quotesByShop[i], quotesExplicit[i])
		}
	}
	// Second call with explicit origin should hit cache (no extra Set, no extra shopPickup call).
	if len(qc.setReqs) != 1 {
		t.Fatalf("expected cache hit on explicit origin call, got %d cache Set calls", len(qc.setReqs))
	}
	if pickup.calls.Load() != 1 {
		t.Fatalf("expected 1 shop pickup lookup, got %d", pickup.calls.Load())
	}

	// Verify intra-province GHN STANDARD fee is 24000 VND (22000 base + 2000 COD), NOT inter-region 40000 VND.
	var ghnStdFee int64
	for _, q := range quotesByShop {
		if q.CarrierCode == carrier.CodeGHN && q.ServiceCode == "STANDARD" {
			ghnStdFee = q.FeeVND
		}
	}
	if ghnStdFee != 24000 {
		t.Fatalf("expected GHN STANDARD intra-province fee 24000 VND, got %d", ghnStdFee)
	}

	// 2. Non-existent shop returns store.ErrNotFound.
	if _, err := svc.CalculateFee(context.Background(), carrier.FeeRequest{
		ShopID:         "shop-missing",
		ToProvinceCode: "79",
		ToWardCode:     "79001",
		WeightGrams:    250,
	}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected store.ErrNotFound for missing shop, got %v", err)
	}

	// 3. Shop with unconfigured pickup address returns ErrInvalidArgument.
	if _, err := svc.CalculateFee(context.Background(), carrier.FeeRequest{
		ShopID:         "shop-no-addr",
		ToProvinceCode: "79",
		ToWardCode:     "79001",
		WeightGrams:    250,
	}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for shop missing pickup address, got %v", err)
	}

	// 4. Shop whose pickup address is outside 34-province catalog returns ErrOutOfServiceArea.
	if _, err := svc.CalculateFee(context.Background(), carrier.FeeRequest{
		ShopID:         "shop-bad-prov",
		ToProvinceCode: "79",
		ToWardCode:     "79001",
		WeightGrams:    250,
	}); !errors.Is(err, ErrOutOfServiceArea) {
		t.Fatalf("expected ErrOutOfServiceArea for unsupported shop pickup province, got %v", err)
	}
}

type nonRetryableCarrier struct{ code string }

func (n *nonRetryableCarrier) Code() string { return n.code }
func (n *nonRetryableCarrier) Name() string { return n.code }
func (n *nonRetryableCarrier) IsMock() bool { return true }
func (n *nonRetryableCarrier) CalculateFee(_ context.Context, _ carrier.FeeRequest) ([]carrier.Quote, error) {
	return nil, &carrier.NonRetryableError{Err: errors.New("route outside carrier coverage")}
}
func (n *nonRetryableCarrier) CreateShipment(_ context.Context, _ carrier.ShipmentRequest) (*carrier.ShipmentResult, error) {
	return nil, errors.New("unsupported")
}
func (n *nonRetryableCarrier) CancelShipment(_ context.Context, _ string) error { return nil }
func (n *nonRetryableCarrier) GetStatus(_ context.Context, _ string) (*carrier.TrackingStatus, error) {
	return nil, nil
}
func (n *nonRetryableCarrier) RequestRedelivery(_ context.Context, _, _, _ string) error { return nil }
func (n *nonRetryableCarrier) TestConnection(_ context.Context, _ string) error          { return nil }
func (n *nonRetryableCarrier) SetToken(_ string)                                         {}

func TestCalculateFee_AllCarriersRejectOutOfServiceArea(t *testing.T) {
	repo := newMemRepo()
	agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), &nonRetryableCarrier{code: "ghn"})
	svc := NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	_, err := svc.CalculateFee(context.Background(), carrier.FeeRequest{
		FromProvinceCode: "79",
		FromWardCode:     "79001",
		ToProvinceCode:   "96",
		ToWardCode:       "96003",
		WeightGrams:      500,
	})
	if !errors.Is(err, ErrOutOfServiceArea) {
		t.Fatalf("expected ErrOutOfServiceArea when all carriers reject route, got %v", err)
	}
}

type hookCancelCarrier struct {
	carrier.Carrier
	cancelFn func(ctx context.Context, trackingCode string) error
	feeFn    func(ctx context.Context, req carrier.FeeRequest) ([]carrier.Quote, error)
}

func (h *hookCancelCarrier) CancelShipment(ctx context.Context, trackingCode string) error {
	if h.cancelFn != nil {
		return h.cancelFn(ctx, trackingCode)
	}
	return h.Carrier.CancelShipment(ctx, trackingCode)
}

func (h *hookCancelCarrier) CalculateFee(ctx context.Context, req carrier.FeeRequest) ([]carrier.Quote, error) {
	if h.feeFn != nil {
		return h.feeFn(ctx, req)
	}
	return h.Carrier.CalculateFee(ctx, req)
}

func TestW2A_PartialUniqueCancelAndRecreate(t *testing.T) {
	repo := newMemRepo()
	ghn := carrier.NewGHNAdapter(carrier.GHNConfig{Mode: "mock"})
	agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), ghn)
	svc := NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	in := CreateShipmentInput{
		OrderID:     "ord-recreate-1",
		ShopID:      "shop-1",
		BuyerID:     "buyer-1",
		CarrierCode: carrier.CodeGHN,
		WeightGrams: 500,
	}
	first, existed, err := svc.CreateShipment(context.Background(), in)
	if err != nil || existed {
		t.Fatalf("first CreateShipment: sh=%+v existed=%v err=%v", first, existed, err)
	}

	// Cancel first shipment -> status becomes cancelled.
	cancelled, err := svc.CancelShipment(context.Background(), "ord-recreate-1", store.DirectionForward, "đổi hãng")
	if err != nil || cancelled.Status != carrier.StatusCancelled {
		t.Fatalf("CancelShipment failed: sh=%+v err=%v", cancelled, err)
	}

	// Recreate shipment for the SAME order_id must succeed with a NEW tracking code (partial unique index).
	second, existed2, err := svc.CreateShipment(context.Background(), in)
	if err != nil || existed2 {
		t.Fatalf("recreate after cancel must succeed as new shipment, got sh=%+v existed=%v err=%v", second, existed2, err)
	}
	if second.ID == first.ID || second.TrackingCode == first.TrackingCode {
		t.Fatalf("recreated shipment must have new ID and tracking code: first=%+v second=%+v", first, second)
	}

	// Replay while second is active returns second idempotently.
	replay, existed3, err := svc.CreateShipment(context.Background(), in)
	if err != nil || !existed3 || replay.ID != second.ID {
		t.Fatalf("expected idempotent replay of second shipment, got sh=%+v existed=%v err=%v", replay, existed3, err)
	}

	// GetShipment returns active second shipment, not cancelled first one.
	got, _, err := svc.GetShipment(context.Background(), "ord-recreate-1", "", store.DirectionForward)
	if err != nil || got.ID != second.ID || got.Status != carrier.StatusCreated {
		t.Fatalf("GetShipment must return active shipment, got %+v err=%v", got, err)
	}
}

func TestW2A_CancelRaceWithPickedWebhookAndCarrierError(t *testing.T) {
	t.Run("webhook picked arrives while carrier cancel in-flight prevents cancel overwrite", func(t *testing.T) {
		repo := newMemRepo()
		var svc *ShippingService
		inFlight := make(chan struct{})
		proceed := make(chan struct{})
		ghn := &hookCancelCarrier{
			Carrier: carrier.NewGHNAdapter(carrier.GHNConfig{Mode: "mock"}),
			cancelFn: func(_ context.Context, _ string) error {
				close(inFlight)
				<-proceed
				return nil
			},
		}
		agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), ghn)
		svc = NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

		sh, _, err := svc.CreateShipment(context.Background(), CreateShipmentInput{
			OrderID: "ord-race-1", ShopID: "shop-1", CarrierCode: carrier.CodeGHN, WeightGrams: 500,
		})
		if err != nil {
			t.Fatalf("CreateShipment: %v", err)
		}

		cancelErrCh := make(chan error, 1)
		go func() {
			_, cErr := svc.CancelShipment(context.Background(), "ord-race-1", store.DirectionForward, "raced cancel")
			cancelErrCh <- cErr
		}()

		<-inFlight
		// Concurrent webhook moves shipment to delivering via "picked" while CancelShipment is in-flight.
		updated, outcome, whErr := svc.HandleWebhook(context.Background(), WebhookInput{
			EventID: "evt-race-picked", CarrierCode: "ghn", TrackingCode: sh.TrackingCode,
			RawStatus: "picked", OccurredAt: time.Now().UTC(),
		})
		if whErr != nil || outcome != store.WebhookOutcomeApplied || updated.Status != carrier.StatusDelivering {
			t.Fatalf("expected picked->delivering applied, got sh=%+v outcome=%s err=%v", updated, outcome, whErr)
		}
		close(proceed)

		if err := <-cancelErrCh; !errors.Is(err, store.ErrCannotCancel) {
			t.Fatalf("expected store.ErrCannotCancel on raced cancel, got %v", err)
		}
		final, _, _ := svc.GetShipment(context.Background(), "ord-race-1", "", store.DirectionForward)
		if final.Status != carrier.StatusDelivering {
			t.Fatalf("shipment status must remain delivering after raced cancel, got %s", final.Status)
		}
	})

	t.Run("carrier cancel failure retries via resilient carrier and returns ErrCarrierUnavailable", func(t *testing.T) {
		repo := newMemRepo()
		var attempts atomic.Int32
		ghn := &hookCancelCarrier{
			Carrier: carrier.NewGHNAdapter(carrier.GHNConfig{Mode: "mock"}),
			cancelFn: func(_ context.Context, _ string) error {
				attempts.Add(1)
				return errors.New("carrier 503")
			},
		}
		rc := carrier.NewResilientCarrier(ghn, carrier.ResilienceConfig{
			AttemptTimeout: 200 * time.Millisecond,
			MaxAttempts:    3,
			BaseBackoff:    5 * time.Millisecond,
			MaxBackoff:     15 * time.Millisecond,
			CBMaxFailures:  10,
			Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
		agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), rc)
		svc := NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

		_, _, err := svc.CreateShipment(context.Background(), CreateShipmentInput{
			OrderID: "ord-cancel-503", ShopID: "shop-1", CarrierCode: carrier.CodeGHN, WeightGrams: 500,
		})
		if err != nil {
			t.Fatalf("CreateShipment: %v", err)
		}
		_, err = svc.CancelShipment(context.Background(), "ord-cancel-503", store.DirectionForward, "test")
		if !errors.Is(err, ErrCarrierUnavailable) {
			t.Fatalf("expected ErrCarrierUnavailable on carrier error, got %v", err)
		}
		if attempts.Load() != 3 {
			t.Fatalf("expected 3 retry attempts, got %d", attempts.Load())
		}
	})
}

func TestW2A_ReturnShipmentIdempotentAndIsolatedFromForward(t *testing.T) {
	repo := newMemRepo()
	ghn := carrier.NewGHNAdapter(carrier.GHNConfig{Mode: "mock"})
	mockExp := carrier.NewMockExpressAdapter()
	agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), ghn, mockExp)
	svc := NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// 1. Create and deliver FORWARD shipment.
	fwd, _, err := svc.CreateShipment(context.Background(), CreateShipmentInput{
		OrderID: "ord-ret-iso", ShopID: "shop-1", BuyerID: "buyer-1", CarrierCode: carrier.CodeGHN, WeightGrams: 500,
		FromAddress: carrier.Address{ProvinceCode: "79", WardCode: "79001"},
		ToAddress:   carrier.Address{ProvinceCode: "01", WardCode: "01001"},
	})
	if err != nil {
		t.Fatalf("CreateShipment: %v", err)
	}
	t0 := time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)
	if _, _, err := svc.HandleWebhook(context.Background(), WebhookInput{
		EventID: "evt-fwd-dlv", CarrierCode: "ghn", OrderID: "ord-ret-iso", RawStatus: "delivered", OccurredAt: t0,
	}); err != nil {
		t.Fatalf("deliver forward via order_id-only webhook: %v", err)
	}

	// 2. Create RETURN shipment (fee computed via aggregator; cheapest carrier mock_express chosen when omitted).
	ret, existed, err := svc.CreateReturnShipment(context.Background(), CreateReturnShipmentInput{
		OrderID: "ord-ret-iso", ReturnRequestID: "rr-100", ShopID: "shop-1", BuyerID: "buyer-1", WeightGrams: 500,
		FromAddress: carrier.Address{ProvinceCode: "01", WardCode: "01001"},
		ToAddress:   carrier.Address{ProvinceCode: "79", WardCode: "79001"},
	})
	if err != nil || existed {
		t.Fatalf("CreateReturnShipment: sh=%+v existed=%v err=%v", ret, existed, err)
	}
	if ret.Direction != store.DirectionReturn || ret.ReturnRequestID != "rr-100" || ret.FeeVND <= 0 || ret.CODAmountVND != 0 {
		t.Fatalf("unexpected return shipment: %+v", ret)
	}
	// Replay is idempotent.
	retReplay, existed2, err := svc.CreateReturnShipment(context.Background(), CreateReturnShipmentInput{
		OrderID: "ord-ret-iso", ReturnRequestID: "rr-100", ShopID: "shop-1", BuyerID: "buyer-1", WeightGrams: 500,
		FromAddress: carrier.Address{ProvinceCode: "01", WardCode: "01001"},
		ToAddress:   carrier.Address{ProvinceCode: "79", WardCode: "79001"},
	})
	if err != nil || !existed2 || retReplay.ID != ret.ID {
		t.Fatalf("expected idempotent return replay, got sh=%+v existed=%v err=%v", retReplay, existed2, err)
	}

	// 3. Drive RETURN shipment through picked -> delivered webhooks and verify outbox carries direction=RETURN + return_request_id.
	if _, outcome, err := svc.HandleWebhook(context.Background(), WebhookInput{
		EventID: "evt-ret-picked", CarrierCode: ret.CarrierCode, TrackingCode: ret.TrackingCode,
		RawStatus: "picked", OccurredAt: t0.Add(time.Hour),
	}); err != nil || outcome != store.WebhookOutcomeApplied {
		t.Fatalf("return picked webhook failed: outcome=%s err=%v", outcome, err)
	}
	if _, outcome, err := svc.HandleWebhook(context.Background(), WebhookInput{
		EventID: "evt-ret-dlv", CarrierCode: ret.CarrierCode, TrackingCode: ret.TrackingCode,
		RawStatus: "delivered", OccurredAt: t0.Add(2 * time.Hour),
	}); err != nil || outcome != store.WebhookOutcomeApplied {
		t.Fatalf("return delivered webhook failed: outcome=%s err=%v", outcome, err)
	}

	// Outbox last event must carry direction=RETURN, return_request_id=rr-100, and Vietnamese description.
	var lastPayload events.ShipmentUpdatedPayload
	if err := json.Unmarshal(repo.outbox[len(repo.outbox)-1].Payload, &lastPayload); err != nil {
		t.Fatalf("unmarshal return outbox payload: %v", err)
	}
	if lastPayload.Direction != store.DirectionReturn || lastPayload.ReturnRequestID != "rr-100" || lastPayload.Description != "Giao hàng thành công" {
		t.Fatalf("unexpected return shipment.updated payload: %+v", lastPayload)
	}

	// FORWARD shipment must remain untouched (status delivered, original tracking code).
	gotFwd, _, err := svc.GetShipment(context.Background(), "ord-ret-iso", "", store.DirectionForward)
	if err != nil || gotFwd.ID != fwd.ID || gotFwd.Status != carrier.StatusDelivered {
		t.Fatalf("FORWARD shipment was unexpectedly altered: %+v err=%v", gotFwd, err)
	}
	gotRet, _, err := svc.GetShipment(context.Background(), "ord-ret-iso", "", store.DirectionReturn)
	if err != nil || gotRet.ID != ret.ID || gotRet.Status != carrier.StatusDelivered {
		t.Fatalf("RETURN shipment lookup failed: %+v err=%v", gotRet, err)
	}
}

func TestW2A_TokenCipherAES256GCM(t *testing.T) {
	cipher, err := NewTokenCipherFromBase64("ZGV2LW9ubHktc2hpcHBpbmctY29uZmlnLWtleS0zMmI=")
	if err != nil {
		t.Fatalf("NewTokenCipherFromBase64: %v", err)
	}
	const secret = "ghn-live-secret-token-987654"
	ct1, err := cipher.Encrypt(secret)
	if err != nil {
		t.Fatalf("Encrypt #1: %v", err)
	}
	ct2, err := cipher.Encrypt(secret)
	if err != nil {
		t.Fatalf("Encrypt #2: %v", err)
	}
	if string(ct1) == string(ct2) {
		t.Fatalf("fresh random nonces must produce distinct ciphertexts")
	}
	if strings.Contains(string(ct1), secret) {
		t.Fatalf("ciphertext must not contain plaintext token")
	}
	got, err := cipher.Decrypt(ct1)
	if err != nil || got != secret {
		t.Fatalf("Decrypt = (%q, %v), want %q", got, err, secret)
	}

	// Tampered ciphertext must fail cleanly without leaking secret.
	tampered := append([]byte(nil), ct1...)
	tampered[len(tampered)-1] ^= 0xff
	if _, err := cipher.Decrypt(tampered); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("expected clean decrypt error on tampered payload, got %v", err)
	}

	// Wrong key fails to decrypt.
	otherKey := make([]byte, 32)
	otherCipher, _ := NewTokenCipher(otherKey)
	if _, err := otherCipher.Decrypt(ct1); err == nil {
		t.Fatalf("expected decrypt failure with wrong key")
	}
	// Invalid key lengths rejected.
	if _, err := NewTokenCipher([]byte("too-short")); err == nil {
		t.Fatalf("expected error for short AES key")
	}
}

func TestW2A_ShopSettingsCarrierFilterDefaultWeightAndAdminControls(t *testing.T) {
	repo := newMemRepo()
	var ghnFail atomic.Bool
	ghnAdapter := carrier.NewGHNAdapter(carrier.GHNConfig{Mode: "mock", Token: "sandbox-ghn-token"})
	ghnHook := &hookCancelCarrier{
		Carrier: ghnAdapter,
		feeFn: func(ctx context.Context, req carrier.FeeRequest) ([]carrier.Quote, error) {
			if ghnFail.Load() {
				return nil, errors.New("ghn transient error")
			}
			return ghnAdapter.CalculateFee(ctx, req)
		},
	}
	mockExp := carrier.NewMockExpressAdapter()
	agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), ghnHook, mockExp)
	qc := newRecordingQuoteCache()
	cipher, _ := NewTokenCipherFromBase64("ZGV2LW9ubHktc2hpcHBpbmctY29uZmlnLWtleS0zMmI=")

	svc := NewShippingService(repo, agg, qc, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.SetTokenCipher(cipher)
	if err := svc.InitCarrierConfigs(context.Background(), map[string]string{carrier.CodeGHN: "sandbox-ghn-token"}); err != nil {
		t.Fatalf("InitCarrierConfigs: %v", err)
	}

	// 1. Shop disables mock_express and sets default_weight_grams=1600.
	if _, err := svc.UpdateShopShippingSettings(context.Background(), &store.ShopShippingSettings{
		ShopID: "shop-filter", EnabledCarrierCodes: []string{"ghn"}, DefaultWeightGrams: 1600,
	}); err != nil {
		t.Fatalf("UpdateShopShippingSettings: %v", err)
	}

	// Quote with weight=0 uses shop's default_weight_grams=1600 and returns ONLY ghn!
	quotes, err := svc.CalculateFee(context.Background(), carrier.FeeRequest{
		ShopID: "shop-filter", FromProvinceCode: "79", FromWardCode: "79001",
		ToProvinceCode: "79", ToWardCode: "79001", WeightGrams: 0,
	})
	if err != nil || len(quotes) == 0 {
		t.Fatalf("CalculateFee with shop settings failed: quotes=%+v err=%v", quotes, err)
	}
	for _, q := range quotes {
		if q.CarrierCode != carrier.CodeGHN {
			t.Fatalf("disabled shop carrier %q leaked into quotes", q.CarrierCode)
		}
		// 1600g => 22000 base + ceil((1600-1000)/500)*5000 = 22000 + 2*5000 = 32000 for STANDARD
		if q.ServiceCode == "STANDARD" && q.FeeVND != 32000 {
			t.Fatalf("expected default_weight_grams=1600 fee 32000 VND, got %d", q.FeeVND)
		}
	}

	// Create a ghn shipment BEFORE admin disables ghn (to verify webhook still works after disable).
	sh, _, err := svc.CreateShipment(context.Background(), CreateShipmentInput{
		OrderID: "ord-before-disable", ShopID: "shop-filter", CarrierCode: carrier.CodeGHN, WeightGrams: 0,
	})
	if err != nil {
		t.Fatalf("CreateShipment: %v", err)
	}
	if sh.WeightGrams != 1600 {
		t.Fatalf("CreateShipment with weight=0 must apply shop default 1600g, got %d", sh.WeightGrams)
	}

	// 2. Admin updates DB fallback rates for ghn and verifies transient failure serves DB fallback rate.
	_, err = svc.AdminUpdateCarrier(context.Background(), AdminUpdateCarrierInput{
		CarrierCode: "ghn",
		Enabled:     true,
		FallbackRates: []carrier.FallbackRate{
			{Region: carrier.RegionSameProvince, BaseFeeVND: 29000, BaseWeightGrams: 1000, ExtraFeeVNDPer500g: 7000},
		},
	})
	if err != nil {
		t.Fatalf("AdminUpdateCarrier fallback rates: %v", err)
	}
	ghnFail.Store(true)
	fbQuotes, err := svc.CalculateFee(context.Background(), carrier.FeeRequest{
		ShopID: "shop-filter", FromProvinceCode: "79", FromWardCode: "79001",
		ToProvinceCode: "79", ToWardCode: "79001", WeightGrams: 1500,
	})
	ghnFail.Store(false)
	if err != nil || len(fbQuotes) != 1 || !fbQuotes[0].IsFallback || fbQuotes[0].FeeVND != 36000 {
		t.Fatalf("expected DB fallback quote 36000 VND (29000 + 7000), got %+v err=%v", fbQuotes, err)
	}

	// 3. Admin disables ghn -> shop-filter (only enables ghn) now gets ErrNoEnabledCarriers (400) for NEW CalculateFee quotes,
	//    explicit CreateReturnShipment(ghn) gets ErrCarrierDisabled,
	//    BUT pre-chosen FORWARD CreateShipment(ghn) and webhooks on existing shipments STILL succeed (UC-A12)!
	if _, err := svc.AdminUpdateCarrier(context.Background(), AdminUpdateCarrierInput{
		CarrierCode: "ghn", Enabled: false,
	}); err != nil {
		t.Fatalf("AdminUpdateCarrier disable ghn: %v", err)
	}
	if _, err := svc.CalculateFee(context.Background(), carrier.FeeRequest{
		ShopID: "shop-filter", FromProvinceCode: "79", FromWardCode: "79001",
		ToProvinceCode: "79", ToWardCode: "79001", WeightGrams: 500,
	}); !errors.Is(err, ErrNoEnabledCarriers) {
		t.Fatalf("expected ErrNoEnabledCarriers when shop∩platform is empty, got %v", err)
	}
	if fwdDisabled, _, err := svc.CreateShipment(context.Background(), CreateShipmentInput{
		OrderID: "ord-after-disable", ShopID: "shop-filter", CarrierCode: carrier.CodeGHN, WeightGrams: 500,
	}); err != nil || fwdDisabled.CarrierCode != carrier.CodeGHN {
		t.Fatalf("UC-A12: FORWARD CreateShipment for pre-chosen disabled carrier must still succeed, got sh=%+v err=%v", fwdDisabled, err)
	}
	if _, _, err := svc.CreateReturnShipment(context.Background(), CreateReturnShipmentInput{
		OrderID: "ord-after-disable", ReturnRequestID: "rr-disabled-ghn", ShopID: "shop-filter",
		CarrierCode: carrier.CodeGHN, WeightGrams: 500,
		FromAddress: carrier.Address{ProvinceCode: "79", WardCode: "79001"},
		ToAddress:   carrier.Address{ProvinceCode: "79", WardCode: "79001"},
	}); !errors.Is(err, ErrCarrierDisabled) {
		t.Fatalf("expected ErrCarrierDisabled for explicit disabled carrier CreateReturnShipment, got %v", err)
	}
	// Existing shipment still receives webhook cleanly even while carrier is disabled!
	updated, outcome, err := svc.HandleWebhook(context.Background(), WebhookInput{
		EventID: "evt-disabled-ok", CarrierCode: "ghn", TrackingCode: sh.TrackingCode,
		RawStatus: "delivering", Description: "delivering", OccurredAt: time.Now().UTC(),
	})
	if err != nil || outcome != store.WebhookOutcomeApplied || updated.Status != carrier.StatusDelivering {
		t.Fatalf("existing shipment must still accept webhook after carrier disable, got sh=%+v outcome=%s err=%v", updated, outcome, err)
	}
	// Verify raw English "delivering" was replaced with Vietnamese description.
	evList, _ := repo.ListShipmentEvents(context.Background(), sh.ID)
	if len(evList) == 0 || evList[len(evList)-1].Description != "Đang giao hàng" {
		t.Fatalf("expected Vietnamese description 'Đang giao hàng', got %+v", evList)
	}

	// 4. Re-enabling ghn with a bad token fails and leaves ghn disabled!
	if _, err := svc.AdminUpdateCarrier(context.Background(), AdminUpdateCarrierInput{
		CarrierCode: "ghn", Enabled: true, APIToken: "invalid-secret-tok",
	}); !errors.Is(err, ErrCarrierAuthFailed) {
		t.Fatalf("expected ErrCarrierAuthFailed on bad token, got %v", err)
	}
	cfgAfter, _ := repo.GetCarrierConfig(context.Background(), "ghn")
	if cfgAfter.Enabled {
		t.Fatalf("ghn must remain disabled after failed token check")
	}

	// 5. Re-enabling ghn with a valid token encrypts it at rest and succeeds.
	const newGoodToken = "ghn-rotated-token-2026"
	view, err := svc.AdminUpdateCarrier(context.Background(), AdminUpdateCarrierInput{
		CarrierCode: "ghn", Enabled: true, APIToken: newGoodToken,
	})
	if err != nil || !view.Enabled || !view.TokenConfigured {
		t.Fatalf("expected valid token enable to succeed, got view=%+v err=%v", view, err)
	}
	cfgRaw, _ := repo.GetCarrierConfig(context.Background(), "ghn")
	if strings.Contains(string(cfgRaw.EncryptedToken), newGoodToken) {
		t.Fatalf("stored DB token must be encrypted, not plaintext")
	}
}

func TestW2A_AccentInsensitiveAddressSearch(t *testing.T) {
	repo := newMemRepo()
	agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc := NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	provs, err := svc.ListProvinces(context.Background(), "Ha Noi")
	if err != nil || len(provs) != 1 || provs[0].Code != "01" {
		t.Fatalf("ListProvinces('Ha Noi') = (%+v, %v), want code 01", provs, err)
	}
	provsHCM, err := svc.ListProvinces(context.Background(), "HCM")
	if err != nil || len(provsHCM) != 1 || provsHCM[0].Code != "79" {
		t.Fatalf("ListProvinces('HCM') = (%+v, %v), want code 79", provsHCM, err)
	}
	wards, err := svc.ListWards(context.Background(), "79", "Ben Thanh")
	if err != nil || len(wards) != 1 || wards[0].Code != "79002" {
		t.Fatalf("ListWards('79', 'Ben Thanh') = (%+v, %v), want code 79002", wards, err)
	}
}

func TestW2A_OrderCancelledNeverTouchesReturnShipmentAndRedeliveryValidation(t *testing.T) {
	repo := newMemRepo()
	ghn := carrier.NewGHNAdapter(carrier.GHNConfig{Mode: "mock"})
	agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), ghn)
	svc := NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Create FORWARD shipment, deliver it, then create RETURN shipment.
	fwd, _, err := svc.CreateShipment(context.Background(), CreateShipmentInput{
		OrderID: "ord-ret-safe", ShopID: "shop-1", BuyerID: "buyer-1", CarrierCode: carrier.CodeGHN, WeightGrams: 500,
	})
	if err != nil {
		t.Fatalf("CreateShipment: %v", err)
	}
	if _, _, err := svc.HandleWebhook(context.Background(), WebhookInput{
		EventID: "evt-ret-safe-dlv", CarrierCode: "ghn", TrackingCode: fwd.TrackingCode,
		RawStatus: "delivered", OccurredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("deliver forward: %v", err)
	}
	ret, _, err := svc.CreateReturnShipment(context.Background(), CreateReturnShipmentInput{
		OrderID: "ord-ret-safe", ReturnRequestID: "rr-safe", ShopID: "shop-1", BuyerID: "buyer-1",
		CarrierCode: carrier.CodeGHN, WeightGrams: 500,
		FromAddress: carrier.Address{ProvinceCode: "79", WardCode: "79001"},
		ToAddress:   carrier.Address{ProvinceCode: "01", WardCode: "01001"},
	})
	if err != nil {
		t.Fatalf("CreateReturnShipment: %v", err)
	}

	// Late order.cancelled event must NOT cancel the RETURN shipment.
	if err := svc.HandleOrderCancelled(context.Background(), "evt-late-cancel", "ord-ret-safe", "late"); err != nil {
		t.Fatalf("HandleOrderCancelled: %v", err)
	}
	gotRet, _, err := svc.GetShipment(context.Background(), "ord-ret-safe", "", store.DirectionReturn)
	if err != nil || gotRet.ID != ret.ID || gotRet.Status != carrier.StatusCreated {
		t.Fatalf("order.cancelled must not touch RETURN shipment, got %+v err=%v", gotRet, err)
	}

	// Redelivery validation: bad date -> ErrInvalidArgument; non-failed status -> ErrRedeliveryNotAllowed.
	if _, err := svc.RequestRedelivery(context.Background(), "buyer-1", "ord-ret-safe", "2026/11/30", ""); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for invalid date format, got %v", err)
	}
	if _, err := svc.RequestRedelivery(context.Background(), "buyer-1", "ord-ret-safe", "2026-11-30", ""); !errors.Is(err, ErrRedeliveryNotAllowed) {
		t.Fatalf("expected ErrRedeliveryNotAllowed for delivered shipment, got %v", err)
	}
}

func TestReview_DisabledCarrierAllowsForwardAndRedeliveryNeedsNewFailedEvent(t *testing.T) {
	repo := newMemRepo()
	ghn := carrier.NewGHNAdapter(carrier.GHNConfig{Mode: "mock", Token: "sandbox-ghn-token"})
	mockExp := carrier.NewMockExpressAdapter()
	agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), ghn, mockExp)
	cipher, _ := NewTokenCipherFromBase64("ZGV2LW9ubHktc2hpcHBpbmctY29uZmlnLWtleS0zMmI=")
	svc := NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.SetTokenCipher(cipher)
	_ = svc.InitCarrierConfigs(context.Background(), map[string]string{carrier.CodeGHN: "sandbox-ghn-token"})

	// Disable ghn platform-wide.
	if _, err := svc.AdminUpdateCarrier(context.Background(), AdminUpdateCarrierInput{
		CarrierCode: "ghn", Enabled: false,
	}); err != nil {
		t.Fatalf("disable ghn: %v", err)
	}

	// Item 4: Existing order that already chose ghn MUST still be able to CreateShipment (FORWARD).
	sh, _, err := svc.CreateShipment(context.Background(), CreateShipmentInput{
		OrderID: "ord-existing-ghn", ShopID: "shop-1", BuyerID: "buyer-1", CarrierCode: "ghn", WeightGrams: 500,
	})
	if err != nil || sh.CarrierCode != "ghn" {
		t.Fatalf("FORWARD CreateShipment for pre-chosen disabled carrier must succeed, got sh=%+v err=%v", sh, err)
	}

	// Item 5: Single failed event allows ONLY ONE RequestRedelivery until a new failed webhook arrives.
	t0 := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	for attempt := int32(1); attempt <= 3; attempt++ {
		if _, _, err := svc.HandleWebhook(context.Background(), WebhookInput{
			EventID: "evt-redeliv-fail-" + string(rune('0'+attempt)), CarrierCode: "ghn", TrackingCode: sh.TrackingCode,
			RawStatus: "delivery_fail", OccurredAt: t0.Add(time.Duration(attempt) * time.Minute),
		}); err != nil {
			t.Fatalf("fail webhook #%d: %v", attempt, err)
		}
		got, err := svc.RequestRedelivery(context.Background(), "buyer-1", "ord-existing-ghn", "2026-11-26", "lần")
		if err != nil || got.RedeliveryCount != attempt {
			t.Fatalf("redelivery #%d must succeed with count=%d, got sh=%+v err=%v", attempt, attempt, got, err)
		}
		// Immediate duplicate redelivery on the SAME failed attempt MUST fail (already requested for 1..2, max exceeded after 3).
		_, dupErr := svc.RequestRedelivery(context.Background(), "buyer-1", "ord-existing-ghn", "2026-11-27", "trùng")
		if attempt < 3 {
			if !errors.Is(dupErr, ErrRedeliveryAlreadyRequested) {
				t.Fatalf("duplicate redelivery #%d must return ErrRedeliveryAlreadyRequested, got %v", attempt, dupErr)
			}
		} else if !errors.Is(dupErr, ErrMaxRedeliveryExceeded) {
			t.Fatalf("duplicate redelivery after max must return ErrMaxRedeliveryExceeded, got %v", dupErr)
		}
	}
	// After a 4th failed event, 4th redelivery must still hit ErrMaxRedeliveryExceeded (<= 3).
	if _, _, err := svc.HandleWebhook(context.Background(), WebhookInput{
		EventID: "evt-redeliv-fail-4", CarrierCode: "ghn", TrackingCode: sh.TrackingCode,
		RawStatus: "delivery_fail", OccurredAt: t0.Add(4 * time.Minute),
	}); err != nil {
		t.Fatalf("fail webhook #4: %v", err)
	}
	if _, err := svc.RequestRedelivery(context.Background(), "buyer-1", "ord-existing-ghn", "2026-11-28", "lần 4"); !errors.Is(err, ErrMaxRedeliveryExceeded) {
		t.Fatalf("4th redelivery must return ErrMaxRedeliveryExceeded, got %v", err)
	}
}

type reviewRecordingCarrier struct {
	carrier.Carrier
	mu          sync.Mutex
	clientCodes []string
	tokens      []string
	createFn    func(ctx context.Context, req carrier.ShipmentRequest) (*carrier.ShipmentResult, error)
	feeFn       func(ctx context.Context, req carrier.FeeRequest) ([]carrier.Quote, error)
	cancelFn    func(ctx context.Context, trackingCode string) error
}

func (r *reviewRecordingCarrier) CreateShipment(ctx context.Context, req carrier.ShipmentRequest) (*carrier.ShipmentResult, error) {
	r.mu.Lock()
	r.clientCodes = append(r.clientCodes, req.ClientOrderCode)
	r.mu.Unlock()
	if r.createFn != nil {
		return r.createFn(ctx, req)
	}
	return r.Carrier.CreateShipment(ctx, req)
}

func (r *reviewRecordingCarrier) CalculateFee(ctx context.Context, req carrier.FeeRequest) ([]carrier.Quote, error) {
	if r.feeFn != nil {
		return r.feeFn(ctx, req)
	}
	return r.Carrier.CalculateFee(ctx, req)
}

func (r *reviewRecordingCarrier) CancelShipment(ctx context.Context, trackingCode string) error {
	if r.cancelFn != nil {
		return r.cancelFn(ctx, trackingCode)
	}
	return r.Carrier.CancelShipment(ctx, trackingCode)
}

func (r *reviewRecordingCarrier) SetToken(token string) {
	r.mu.Lock()
	r.tokens = append(r.tokens, token)
	r.mu.Unlock()
	r.Carrier.SetToken(token)
}

func TestReview_ClientOrderCodeStableAndDistinctAcrossRecreateAndReturn(t *testing.T) {
	repo := newMemRepo()
	var failFirstCreate atomic.Bool
	failFirstCreate.Store(true)
	ghnInner := carrier.NewGHNAdapter(carrier.GHNConfig{Mode: "mock"})
	recCarrier := &reviewRecordingCarrier{
		Carrier: ghnInner,
		createFn: func(ctx context.Context, req carrier.ShipmentRequest) (*carrier.ShipmentResult, error) {
			if failFirstCreate.Swap(false) {
				return nil, errors.New("transient carrier 502")
			}
			return ghnInner.CreateShipment(ctx, req)
		},
	}
	agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), recCarrier)
	svc := NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	in := CreateShipmentInput{
		OrderID: "ord-idem-1", ShopID: "shop-1", BuyerID: "buyer-1", CarrierCode: "ghn", WeightGrams: 500,
	}
	// Attempt 1 fails transiently -> retry sends the SAME key "ord-idem-1".
	if _, _, err := svc.CreateShipment(context.Background(), in); !errors.Is(err, ErrCarrierUnavailable) {
		t.Fatalf("expected ErrCarrierUnavailable on transient create failure, got %v", err)
	}
	sh1, _, err := svc.CreateShipment(context.Background(), in)
	if err != nil {
		t.Fatalf("retry CreateShipment #1 failed: %v", err)
	}

	// Cancel #1 and recreate -> key must change to "ord-idem-1-R1", tracking code must differ.
	if _, err := svc.CancelShipment(context.Background(), "ord-idem-1", store.DirectionForward, "cancel 1"); err != nil {
		t.Fatalf("CancelShipment #1: %v", err)
	}
	sh2, _, err := svc.CreateShipment(context.Background(), in)
	if err != nil {
		t.Fatalf("recreate CreateShipment #2 failed: %v", err)
	}
	if sh2.TrackingCode == sh1.TrackingCode {
		t.Fatalf("recreated shipment must not reuse tracking code %s", sh1.TrackingCode)
	}

	// Create RETURN shipment -> key must use return_request_id ("RET-rr-99"), tracking distinct from FORWARD.
	ret1, _, err := svc.CreateReturnShipment(context.Background(), CreateReturnShipmentInput{
		OrderID: "ord-idem-1", ReturnRequestID: "rr-99", ShopID: "shop-1", BuyerID: "buyer-1",
		CarrierCode: "ghn", WeightGrams: 500,
		FromAddress: carrier.Address{ProvinceCode: "79", WardCode: "79001"},
		ToAddress:   carrier.Address{ProvinceCode: "01", WardCode: "01001"},
	})
	if err != nil {
		t.Fatalf("CreateReturnShipment #1: %v", err)
	}
	if ret1.TrackingCode == sh1.TrackingCode || ret1.TrackingCode == sh2.TrackingCode {
		t.Fatalf("RETURN tracking code must be distinct from FORWARD: %s", ret1.TrackingCode)
	}

	// Cancel RETURN #1 and recreate -> key must change to "RET-rr-99-R1".
	if _, err := svc.CancelShipment(context.Background(), "ord-idem-1", store.DirectionReturn, "cancel ret"); err != nil {
		t.Fatalf("CancelShipment RETURN: %v", err)
	}
	if _, _, err := svc.CreateReturnShipment(context.Background(), CreateReturnShipmentInput{
		OrderID: "ord-idem-1", ReturnRequestID: "rr-99", ShopID: "shop-1", BuyerID: "buyer-1",
		CarrierCode: "ghn", WeightGrams: 500,
		FromAddress: carrier.Address{ProvinceCode: "79", WardCode: "79001"},
		ToAddress:   carrier.Address{ProvinceCode: "01", WardCode: "01001"},
	}); err != nil {
		t.Fatalf("CreateReturnShipment #2: %v", err)
	}

	wantCodes := []string{"ord-idem-1", "ord-idem-1", "ord-idem-1-R1", "RET-rr-99", "RET-rr-99-R1"}
	recCarrier.mu.Lock()
	gotCodes := append([]string(nil), recCarrier.clientCodes...)
	recCarrier.mu.Unlock()
	if len(gotCodes) != len(wantCodes) {
		t.Fatalf("recorded client_order_codes = %v, want %v", gotCodes, wantCodes)
	}
	for i := range wantCodes {
		if gotCodes[i] != wantCodes[i] {
			t.Fatalf("client_order_code[%d] = %q, want %q (all=%v)", i, gotCodes[i], wantCodes[i], gotCodes)
		}
	}
}

type failOnceCancelRepo struct {
	*memRepo
	failOnce atomic.Bool
}

func (f *failOnceCancelRepo) CancelShipmentByID(ctx context.Context, shipmentID, reason string) (*store.Shipment, error) {
	if f.failOnce.Swap(false) {
		return nil, errors.New("simulated DB write failure after carrier cancel")
	}
	return f.memRepo.CancelShipmentByID(ctx, shipmentID, reason)
}

func TestReview_CancelRetryWhenCarrierAlreadyCancelled(t *testing.T) {
	repo := &failOnceCancelRepo{memRepo: newMemRepo()}
	repo.failOnce.Store(true)

	var carrierCancelled atomic.Bool
	recCarrier := &reviewRecordingCarrier{
		Carrier: carrier.NewGHNAdapter(carrier.GHNConfig{Mode: "mock"}),
		cancelFn: func(_ context.Context, _ string) error {
			// Simulate carrier cancelling on 1st call and returning "already cancelled" handled as nil on 2nd.
			if carrierCancelled.Swap(true) {
				if carrier.IsAlreadyCancelledMessage("Order is already cancelled") {
					return nil
				}
				return errors.New("unexpected")
			}
			return nil
		},
	}
	agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), recCarrier)
	svc := NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if _, _, err := svc.CreateShipment(context.Background(), CreateShipmentInput{
		OrderID: "ord-cancel-retry", ShopID: "shop-1", CarrierCode: "ghn", WeightGrams: 500,
	}); err != nil {
		t.Fatalf("CreateShipment: %v", err)
	}

	// 1st CancelShipment: carrier succeeds, DB write fails.
	if _, err := svc.CancelShipment(context.Background(), "ord-cancel-retry", store.DirectionForward, "retry test"); err == nil {
		t.Fatalf("expected DB failure on first CancelShipment")
	}

	// 2nd CancelShipment: carrier already cancelled (returns nil), DB write succeeds!
	sh, err := svc.CancelShipment(context.Background(), "ord-cancel-retry", store.DirectionForward, "retry test")
	if err != nil || sh.Status != carrier.StatusCancelled {
		t.Fatalf("retry CancelShipment after DB failure must succeed, got sh=%+v err=%v", sh, err)
	}
}

func TestReview_CreateErrorClassificationAndReturnCarrierAutoPick(t *testing.T) {
	repo := newMemRepo()
	ghnInner := carrier.NewGHNAdapter(carrier.GHNConfig{Mode: "mock", Token: "sandbox-ghn-token"})
	mockExp := carrier.NewMockExpressAdapter()

	var ghnRejectRoute atomic.Bool
	ghnRejectRoute.Store(true)
	ghnRec := &reviewRecordingCarrier{
		Carrier: ghnInner,
		feeFn: func(ctx context.Context, req carrier.FeeRequest) ([]carrier.Quote, error) {
			if ghnRejectRoute.Load() {
				return nil, &carrier.NonRetryableError{Err: errors.New("ghn does not serve this ward")}
			}
			return ghnInner.CalculateFee(ctx, req)
		},
		createFn: func(ctx context.Context, req carrier.ShipmentRequest) (*carrier.ShipmentResult, error) {
			if ghnRejectRoute.Load() {
				return nil, &carrier.NonRetryableError{Err: errors.New("invalid destination ward")}
			}
			return ghnInner.CreateShipment(ctx, req)
		},
	}

	agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), ghnRec, mockExp)
	svc := NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Item 3: Non-retryable carrier create error -> ErrCarrierRejected (Vietnamese message), NOT ErrCarrierUnavailable.
	_, _, err := svc.CreateShipment(context.Background(), CreateShipmentInput{
		OrderID: "ord-nonretry", ShopID: "shop-1", CarrierCode: "ghn", WeightGrams: 500,
	})
	if !errors.Is(err, ErrCarrierRejected) || errors.Is(err, ErrCarrierUnavailable) {
		t.Fatalf("expected ErrCarrierRejected for non-retryable error, got %v", err)
	}

	// Item 4: CreateReturnShipment with explicit carrier that does not serve route -> ErrCarrierRejected (FailedPrecondition).
	_, _, err = svc.CreateReturnShipment(context.Background(), CreateReturnShipmentInput{
		OrderID: "ord-ret-explicit", ReturnRequestID: "rr-exp-1", ShopID: "shop-1", BuyerID: "buyer-1",
		CarrierCode: "ghn", WeightGrams: 500,
		FromAddress: carrier.Address{ProvinceCode: "79", WardCode: "79001"},
		ToAddress:   carrier.Address{ProvinceCode: "01", WardCode: "01001"},
	})
	if !errors.Is(err, ErrCarrierRejected) {
		t.Fatalf("expected ErrCarrierRejected when explicit return carrier does not serve route, got %v", err)
	}

	// Item 4: CreateReturnShipment with empty carrier_code auto-picks enabled carrier that serves the route (mock_express).
	ret, _, err := svc.CreateReturnShipment(context.Background(), CreateReturnShipmentInput{
		OrderID: "ord-ret-auto", ReturnRequestID: "rr-auto-1", ShopID: "shop-1", BuyerID: "buyer-1",
		CarrierCode: "", WeightGrams: 500,
		FromAddress: carrier.Address{ProvinceCode: "79", WardCode: "79001"},
		ToAddress:   carrier.Address{ProvinceCode: "01", WardCode: "01001"},
	})
	if err != nil || ret.CarrierCode != carrier.CodeMockExpress {
		t.Fatalf("empty carrier_code must auto-pick enabled carrier serving route (mock_express), got sh=%+v err=%v", ret, err)
	}
}

func TestReview_MultiInstanceTokenRefreshOnCacheTTL(t *testing.T) {
	repo := newMemRepo()
	cipher, err := NewTokenCipherFromBase64("ZGV2LW9ubHktc2hpcHBpbmctY29uZmlnLWtleS0zMmI=")
	if err != nil {
		t.Fatalf("NewTokenCipherFromBase64: %v", err)
	}
	ghnRec := &reviewRecordingCarrier{
		Carrier: carrier.NewGHNAdapter(carrier.GHNConfig{Mode: "mock", Token: "initial-ghn-token"}),
	}
	agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), ghnRec)
	svc := NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.SetTokenCipher(cipher)
	svc.SetConfigCacheTTL(30 * time.Second)

	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	svc.nowFn = func() time.Time { return now }

	if err := svc.InitCarrierConfigs(context.Background(), map[string]string{carrier.CodeGHN: "initial-ghn-token"}); err != nil {
		t.Fatalf("InitCarrierConfigs: %v", err)
	}
	// Prime cache at T=0.
	if _, _, err := svc.CreateShipment(context.Background(), CreateShipmentInput{
		OrderID: "ord-tok-1", ShopID: "shop-1", CarrierCode: "ghn", WeightGrams: 500,
	}); err != nil {
		t.Fatalf("CreateShipment #1: %v", err)
	}

	// Simulate ANOTHER instance updating the encrypted token directly in DB.
	rotatedEnc, err := cipher.Encrypt("rotated-from-replica-2")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := repo.UpdateCarrierConfig(context.Background(), &store.CarrierConfig{
		CarrierCode: "ghn", Name: "GHN", Enabled: true, EncryptedToken: rotatedEnc,
	}, nil); err != nil {
		t.Fatalf("UpdateCarrierConfig: %v", err)
	}

	// Advance clock past TTL -> next operation refreshes config cache and syncs rotated token onto adapter!
	now = now.Add(31 * time.Second)
	if _, _, err := svc.CreateShipment(context.Background(), CreateShipmentInput{
		OrderID: "ord-tok-2", ShopID: "shop-1", CarrierCode: "ghn", WeightGrams: 500,
	}); err != nil {
		t.Fatalf("CreateShipment #2: %v", err)
	}

	ghnRec.mu.Lock()
	tokens := append([]string(nil), ghnRec.tokens...)
	ghnRec.mu.Unlock()
	if len(tokens) == 0 || tokens[len(tokens)-1] != "rotated-from-replica-2" {
		t.Fatalf("expected adapter to receive rotated token after TTL refresh, got tokens=%v", tokens)
	}
}

func TestReview2_ReturnRecreateAfterReturnedAndRejectWhenFailed(t *testing.T) {
	repo := newMemRepo()
	ghnInner := carrier.NewGHNAdapter(carrier.GHNConfig{Mode: "mock"})
	recCarrier := &reviewRecordingCarrier{Carrier: ghnInner}
	agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), recCarrier)
	svc := NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	now := time.Date(2026, 11, 23, 11, 0, 0, 0, time.UTC)
	svc.nowFn = func() time.Time { return now }

	// 1. Create RETURN #1 -> active replay returns RETURN #1 idempotently.
	ret1, existed, err := svc.CreateReturnShipment(context.Background(), CreateReturnShipmentInput{
		OrderID: "ord-ret-life", ReturnRequestID: "rr-1", ShopID: "shop-1", BuyerID: "buyer-1",
		CarrierCode: "ghn", WeightGrams: 500,
		FromAddress: carrier.Address{ProvinceCode: "79", WardCode: "79001"},
		ToAddress:   carrier.Address{ProvinceCode: "01", WardCode: "01001"},
	})
	if err != nil || existed {
		t.Fatalf("RETURN #1 create failed: sh=%+v existed=%v err=%v", ret1, existed, err)
	}
	replay, existedReplay, err := svc.CreateReturnShipment(context.Background(), CreateReturnShipmentInput{
		OrderID: "ord-ret-life", ReturnRequestID: "rr-1", ShopID: "shop-1", BuyerID: "buyer-1",
		CarrierCode: "ghn", WeightGrams: 500,
		FromAddress: carrier.Address{ProvinceCode: "79", WardCode: "79001"},
		ToAddress:   carrier.Address{ProvinceCode: "01", WardCode: "01001"},
	})
	if err != nil || !existedReplay || replay.ID != ret1.ID {
		t.Fatalf("active RETURN replay must return existing shipment, got sh=%+v existed=%v err=%v", replay, existedReplay, err)
	}

	// 2. RETURN #1 becomes failed -> CreateReturnShipment must return ErrReturnShipmentFailed (Vietnamese message), NOT replay old failed shipment.
	if _, _, err := svc.HandleWebhook(context.Background(), WebhookInput{
		EventID: "evt-ret1-fail", CarrierCode: "ghn", TrackingCode: ret1.TrackingCode,
		RawStatus: "delivery_fail", OccurredAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("fail webhook for RETURN #1: %v", err)
	}
	if got, existedFailed, err := svc.CreateReturnShipment(context.Background(), CreateReturnShipmentInput{
		OrderID: "ord-ret-life", ReturnRequestID: "rr-2", ShopID: "shop-1", BuyerID: "buyer-1",
		CarrierCode: "ghn", WeightGrams: 500,
		FromAddress: carrier.Address{ProvinceCode: "79", WardCode: "79001"},
		ToAddress:   carrier.Address{ProvinceCode: "01", WardCode: "01001"},
	}); !errors.Is(err, ErrReturnShipmentFailed) {
		t.Fatalf("CreateReturnShipment while RETURN is failed must return ErrReturnShipmentFailed, got sh=%+v existed=%v err=%v", got, existedFailed, err)
	}

	// 3. RETURN #1 becomes returned -> admin approves retry -> CreateReturnShipment MUST create RETURN #2 with distinct client_order_code!
	if _, _, err := svc.HandleWebhook(context.Background(), WebhookInput{
		EventID: "evt-ret1-returned", CarrierCode: "ghn", TrackingCode: ret1.TrackingCode,
		RawStatus: "returned", OccurredAt: now.Add(2 * time.Minute),
	}); err != nil {
		t.Fatalf("returned webhook for RETURN #1: %v", err)
	}
	now = now.Add(5 * time.Minute)
	ret2, existed2, err := svc.CreateReturnShipment(context.Background(), CreateReturnShipmentInput{
		OrderID: "ord-ret-life", ReturnRequestID: "rr-1", ShopID: "shop-1", BuyerID: "buyer-1",
		CarrierCode: "ghn", WeightGrams: 500,
		FromAddress: carrier.Address{ProvinceCode: "79", WardCode: "79001"},
		ToAddress:   carrier.Address{ProvinceCode: "01", WardCode: "01001"},
	})
	if err != nil || existed2 || ret2.ID == ret1.ID || ret2.TrackingCode == ret1.TrackingCode {
		t.Fatalf("RETURN #2 after returned must create a new shipment, got ret2=%+v existed=%v err=%v (ret1=%+v)", ret2, existed2, err, ret1)
	}

	// GetShipment(RETURN) must return RETURN #2 (the newest).
	latest, _, err := svc.GetShipment(context.Background(), "ord-ret-life", "", store.DirectionReturn)
	if err != nil || latest.ID != ret2.ID {
		t.Fatalf("GetShipment(RETURN) must return newest RETURN #2, got %+v err=%v", latest, err)
	}

	// 4. Cancel RETURN #2 -> GetShipment(RETURN) still returns RETURN #2 (newest among returned + cancelled),
	//    and RETURN #3 counts BOTH returned and cancelled -> RET-rr-3-R2!
	if _, err := svc.CancelShipment(context.Background(), "ord-ret-life", store.DirectionReturn, "huỷ lần 2"); err != nil {
		t.Fatalf("CancelShipment RETURN #2: %v", err)
	}
	latestAfterCancel, _, err := svc.GetShipment(context.Background(), "ord-ret-life", "", store.DirectionReturn)
	if err != nil || latestAfterCancel.ID != ret2.ID {
		t.Fatalf("GetShipment(RETURN) after cancelling RETURN #2 must still return newest RETURN #2, got %+v err=%v", latestAfterCancel, err)
	}
	now = now.Add(5 * time.Minute)
	ret3, existed3, err := svc.CreateReturnShipment(context.Background(), CreateReturnShipmentInput{
		OrderID: "ord-ret-life", ReturnRequestID: "rr-3", ShopID: "shop-1", BuyerID: "buyer-1",
		CarrierCode: "ghn", WeightGrams: 500,
		FromAddress: carrier.Address{ProvinceCode: "79", WardCode: "79001"},
		ToAddress:   carrier.Address{ProvinceCode: "01", WardCode: "01001"},
	})
	if err != nil || existed3 || ret3.ID == ret2.ID {
		t.Fatalf("RETURN #3 after returned+cancelled must create new shipment, got %+v existed=%v err=%v", ret3, existed3, err)
	}

	wantCodes := []string{"RET-rr-1", "RET-rr-1-R1", "RET-rr-3-R2"}
	recCarrier.mu.Lock()
	gotCodes := append([]string(nil), recCarrier.clientCodes...)
	recCarrier.mu.Unlock()
	if len(gotCodes) != len(wantCodes) {
		t.Fatalf("client_order_codes = %v, want %v", gotCodes, wantCodes)
	}
	for i := range wantCodes {
		if gotCodes[i] != wantCodes[i] {
			t.Fatalf("client_order_code[%d] = %q, want %q (all=%v)", i, gotCodes[i], wantCodes[i], gotCodes)
		}
	}

	// 5. FORWARD behavior is unchanged: a FORWARD shipment in returned status stays active (replayed, not recreated).
	fwd1, _, err := svc.CreateShipment(context.Background(), CreateShipmentInput{
		OrderID: "ord-fwd-unchanged", ShopID: "shop-1", BuyerID: "buyer-1", CarrierCode: "ghn", WeightGrams: 500,
	})
	if err != nil {
		t.Fatalf("FORWARD create: %v", err)
	}
	if _, _, err := svc.HandleWebhook(context.Background(), WebhookInput{
		EventID: "evt-fwd-ret", CarrierCode: "ghn", TrackingCode: fwd1.TrackingCode,
		RawStatus: "returned", OccurredAt: now.Add(20 * time.Minute),
	}); err != nil {
		t.Fatalf("FORWARD returned webhook: %v", err)
	}
	fwdReplay, fwdExisted, err := svc.CreateShipment(context.Background(), CreateShipmentInput{
		OrderID: "ord-fwd-unchanged", ShopID: "shop-1", BuyerID: "buyer-1", CarrierCode: "ghn", WeightGrams: 500,
	})
	if err != nil || !fwdExisted || fwdReplay.ID != fwd1.ID {
		t.Fatalf("FORWARD returned shipment must remain active/replayed, got sh=%+v existed=%v err=%v", fwdReplay, fwdExisted, err)
	}
}

func TestReview2_GHNAuthAndTimeoutCreateErrorsAreUnavailableNotRejected(t *testing.T) {
	for _, tc := range []struct {
		name       string
		carrierErr error
		wantConfig bool
	}{
		{"401/403 carrier auth config error", &carrier.NonRetryableError{Err: carrier.ErrInvalidCarrierToken}, true},
		{"408/429 transient error", errors.New("ghn transient error status=408"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newMemRepo()
			recCarrier := &reviewRecordingCarrier{
				Carrier: carrier.NewGHNAdapter(carrier.GHNConfig{Mode: "mock"}),
				createFn: func(_ context.Context, _ carrier.ShipmentRequest) (*carrier.ShipmentResult, error) {
					return nil, tc.carrierErr
				},
			}
			svc := NewShippingService(repo, carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), recCarrier), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
			_, _, err := svc.CreateShipment(context.Background(), CreateShipmentInput{
				OrderID: "ord-err-" + tc.name, ShopID: "shop-1", CarrierCode: "ghn", WeightGrams: 500,
			})
			if !errors.Is(err, ErrCarrierUnavailable) || errors.Is(err, ErrCarrierRejected) {
				t.Fatalf("expected ErrCarrierUnavailable (NOT ErrCarrierRejected), got %v", err)
			}
			if tc.wantConfig && strings.Contains(err.Error(), "địa chỉ") {
				t.Fatalf("401/403 error must NOT tell seller address was rejected, got %q", err.Error())
			}
		})
	}
}

func TestReview3_FailoverOnOpenCBNonRetryableCancelRedeliveryAndShopSettingsPlatformCheck(t *testing.T) {
	t.Run("CreateReturnShipment fails over to second carrier when first carrier circuit breaker is open", func(t *testing.T) {
		repo := newMemRepo()
		ghnFailing := &reviewRecordingCarrier{
			Carrier: carrier.NewGHNAdapter(carrier.GHNConfig{Mode: "mock"}),
			createFn: func(_ context.Context, _ carrier.ShipmentRequest) (*carrier.ShipmentResult, error) {
				return nil, errors.New("ghn 503")
			},
		}
		resGHN := carrier.NewResilientCarrier(ghnFailing, carrier.ResilienceConfig{
			AttemptTimeout: 100 * time.Millisecond,
			MaxAttempts:    1,
			BaseBackoff:    time.Millisecond,
			MaxBackoff:     5 * time.Millisecond,
			CBMaxFailures:  1,
			CBOpenTimeout:  30 * time.Second,
			Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
		// Trip GHN circuit breaker so next CreateShipment fails locally with gobreaker.ErrOpenState.
		_, _ = resGHN.CreateShipment(context.Background(), carrier.ShipmentRequest{OrderID: "trip-cb"})

		// Fail mock_express fee transiently so candidates falls back to platformEnabled [ghn, mock_express],
		// forcing CreateReturnShipment to hit resGHN.CreateShipment (ErrOpenState) first and fail over to mock_express!
		mockExp := &reviewRecordingCarrier{
			Carrier: carrier.NewMockExpressAdapter(),
			feeFn: func(_ context.Context, _ carrier.FeeRequest) ([]carrier.Quote, error) {
				return nil, errors.New("mock fee timeout")
			},
		}
		agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), resGHN, mockExp)
		svc := NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

		ret, _, err := svc.CreateReturnShipment(context.Background(), CreateReturnShipmentInput{
			OrderID: "ord-cb-failover", ReturnRequestID: "rr-cb-1", ShopID: "shop-1", BuyerID: "buyer-1",
			CarrierCode: "", WeightGrams: 500,
			FromAddress: carrier.Address{ProvinceCode: "79", WardCode: "79001"},
			ToAddress:   carrier.Address{ProvinceCode: "01", WardCode: "01001"},
		})
		if err != nil || ret.CarrierCode != carrier.CodeMockExpress {
			t.Fatalf("expected failover to mock_express when ghn CB is open, got sh=%+v err=%v", ret, err)
		}
	})

	t.Run("HandleWebhook rejects backward transitions monotonically without emitting outbox", func(t *testing.T) {
		repo := newMemRepo()
		svc := NewShippingService(repo, carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), carrier.NewGHNAdapter(carrier.GHNConfig{Mode: "mock"})), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		sh, _, err := svc.CreateShipment(context.Background(), CreateShipmentInput{
			OrderID: "ord-mono-svc", ShopID: "shop-1", BuyerID: "buyer-1", CarrierCode: "ghn", WeightGrams: 500,
		})
		if err != nil {
			t.Fatalf("CreateShipment: %v", err)
		}
		t0 := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
		if _, outcome, err := svc.HandleWebhook(context.Background(), WebhookInput{
			EventID: "evt-m1", CarrierCode: "ghn", TrackingCode: sh.TrackingCode, RawStatus: "picked", OccurredAt: t0,
		}); err != nil || outcome != store.WebhookOutcomeApplied {
			t.Fatalf("picked failed: outcome=%s err=%v", outcome, err)
		}
		outboxBefore := len(repo.outbox)
		// Same timestamp AND later timestamp regression delivering -> picking must be stale and emit NO outbox.
		for i, ts := range []time.Time{t0, t0.Add(time.Minute)} {
			got, outcome, err := svc.HandleWebhook(context.Background(), WebhookInput{
				EventID: "evt-m-reg-" + string(rune('a'+i)), CarrierCode: "ghn", TrackingCode: sh.TrackingCode,
				RawStatus: "picking", OccurredAt: ts,
			})
			if err != nil || outcome != store.WebhookOutcomeStale || got.Status != carrier.StatusDelivering {
				t.Fatalf("case %d: expected stale delivering, got sh=%+v outcome=%s err=%v", i, got, outcome, err)
			}
		}
		if len(repo.outbox) != outboxBefore {
			t.Fatalf("stale regression must NOT emit outbox events: before=%d after=%d", outboxBefore, len(repo.outbox))
		}
	})

	t.Run("NonRetryableError in CancelShipment and RequestRedelivery returns FailedPrecondition sentinel (not Unavailable)", func(t *testing.T) {
		repo := newMemRepo()
		var rejectCancel, rejectRedelivery atomic.Bool
		rejectCancel.Store(true)
		rejectRedelivery.Store(true)
		recCarrier := &reviewRecordingCarrier{
			Carrier: carrier.NewGHNAdapter(carrier.GHNConfig{Mode: "mock"}),
			cancelFn: func(_ context.Context, _ string) error {
				if rejectCancel.Load() {
					return &carrier.NonRetryableError{Err: errors.New("ghn cancel api rejected code=400 order already picked")}
				}
				return nil
			},
		}
		// Wrap RequestRedelivery via a small inline wrapper.
		redelivCarrier := &reviewRedeliveryCarrier{
			Carrier: recCarrier,
			redeliveryFn: func(_ context.Context, _, _, _ string) error {
				if rejectRedelivery.Load() {
					return &carrier.NonRetryableError{Err: errors.New("ghn redelivery api rejected code=400")}
				}
				return nil
			},
		}
		svc := NewShippingService(repo, carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), redelivCarrier), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

		sh, _, err := svc.CreateShipment(context.Background(), CreateShipmentInput{
			OrderID: "ord-nr-ops", ShopID: "shop-1", BuyerID: "buyer-1", CarrierCode: "ghn", WeightGrams: 500,
		})
		if err != nil {
			t.Fatalf("CreateShipment: %v", err)
		}
		if _, err := svc.CancelShipment(context.Background(), "ord-nr-ops", store.DirectionForward, "too late"); !errors.Is(err, ErrNotCancellable) {
			t.Fatalf("non-retryable cancel error must return ErrNotCancellable (FailedPrecondition), got %v", err)
		}

		// Move shipment to failed -> non-retryable redelivery error must return ErrRedeliveryNotAllowed.
		if _, _, err := svc.HandleWebhook(context.Background(), WebhookInput{
			EventID: "evt-nr-fail", CarrierCode: "ghn", TrackingCode: sh.TrackingCode,
			RawStatus: "delivery_fail", OccurredAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("HandleWebhook: %v", err)
		}
		if _, err := svc.RequestRedelivery(context.Background(), "buyer-1", "ord-nr-ops", "2026-11-30", "note"); !errors.Is(err, ErrRedeliveryNotAllowed) {
			t.Fatalf("non-retryable redelivery error must return ErrRedeliveryNotAllowed (FailedPrecondition), got %v", err)
		}
	})

	t.Run("UpdateShopShippingSettings rejects selecting only platform-disabled carriers", func(t *testing.T) {
		repo := newMemRepo()
		ghn := carrier.NewGHNAdapter(carrier.GHNConfig{Mode: "mock", Token: "sandbox-ghn-token"})
		mockExp := carrier.NewMockExpressAdapter()
		agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), ghn, mockExp)
		svc := NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

		if _, err := svc.AdminUpdateCarrier(context.Background(), AdminUpdateCarrierInput{
			CarrierCode: "mock_express", Enabled: false,
		}); err != nil {
			t.Fatalf("disable mock_express: %v", err)
		}
		if _, err := svc.UpdateShopShippingSettings(context.Background(), &store.ShopShippingSettings{
			ShopID: "shop-only-disabled", EnabledCarrierCodes: []string{"mock_express"}, DefaultWeightGrams: 500,
		}); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("selecting only platform-disabled carrier must return ErrInvalidArgument, got %v", err)
		}
		// Including at least one platform-enabled carrier succeeds.
		if _, err := svc.UpdateShopShippingSettings(context.Background(), &store.ShopShippingSettings{
			ShopID: "shop-only-disabled", EnabledCarrierCodes: []string{"mock_express", "ghn"}, DefaultWeightGrams: 500,
		}); err != nil {
			t.Fatalf("selecting at least one platform-enabled carrier must succeed, got %v", err)
		}
	})
}

type reviewRedeliveryCarrier struct {
	carrier.Carrier
	redeliveryFn func(ctx context.Context, trackingCode, preferredDate, note string) error
}

func (r *reviewRedeliveryCarrier) RequestRedelivery(ctx context.Context, trackingCode, preferredDate, note string) error {
	if r.redeliveryFn != nil {
		return r.redeliveryFn(ctx, trackingCode, preferredDate, note)
	}
	return r.Carrier.RequestRedelivery(ctx, trackingCode, preferredDate, note)
}

func TestReview4_ConcurrentCreateShipmentAndReturnCallsCarrierOnce(t *testing.T) {
	runConcurrentSuite := func(t *testing.T, repo store.Repository, prefix string) {
		t.Helper()

		t.Run("concurrent FORWARD CreateShipment calls carrier exactly once", func(t *testing.T) {
			var carrierCalls atomic.Int32
			mockInner := carrier.NewMockExpressAdapter()
			slowCarrier := &reviewRecordingCarrier{
				Carrier: mockInner,
				createFn: func(ctx context.Context, req carrier.ShipmentRequest) (*carrier.ShipmentResult, error) {
					carrierCalls.Add(1)
					time.Sleep(80 * time.Millisecond)
					return mockInner.CreateShipment(ctx, req)
				},
			}
			svc := NewShippingService(repo, carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), slowCarrier), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

			type res struct {
				sh      *store.Shipment
				existed bool
				err     error
			}
			ordID := prefix + "-fwd-" + uuid.NewString()[:8]
			results := make([]res, 2)
			var wg sync.WaitGroup
			start := make(chan struct{})
			for i := 0; i < 2; i++ {
				wg.Add(1)
				go func(idx int) {
					defer wg.Done()
					<-start
					sh, existed, err := svc.CreateShipment(context.Background(), CreateShipmentInput{
						OrderID: ordID, ShopID: "shop-conc", BuyerID: "buyer-conc",
						CarrierCode: carrier.CodeMockExpress, WeightGrams: 500,
					})
					results[idx] = res{sh: sh, existed: existed, err: err}
				}(i)
			}
			close(start)
			wg.Wait()

			if results[0].err != nil || results[1].err != nil {
				t.Fatalf("concurrent CreateShipment errors: r0=%v r1=%v", results[0].err, results[1].err)
			}
			if got := carrierCalls.Load(); got != 1 {
				t.Fatalf("carrier CreateShipment must be called ONCE across concurrent callers, got %d", got)
			}
			if results[0].sh.ID != results[1].sh.ID || results[0].sh.TrackingCode != results[1].sh.TrackingCode {
				t.Fatalf("both callers must receive the exact same shipment: r0=%+v r1=%+v", results[0].sh, results[1].sh)
			}
			if results[0].existed == results[1].existed {
				t.Fatalf("expected one caller alreadyExisted=false and one alreadyExisted=true, got %v and %v", results[0].existed, results[1].existed)
			}
		})

		t.Run("concurrent RETURN CreateReturnShipment calls carrier exactly once", func(t *testing.T) {
			var carrierCalls atomic.Int32
			mockInner := carrier.NewMockExpressAdapter()
			slowCarrier := &reviewRecordingCarrier{
				Carrier: mockInner,
				createFn: func(ctx context.Context, req carrier.ShipmentRequest) (*carrier.ShipmentResult, error) {
					carrierCalls.Add(1)
					time.Sleep(80 * time.Millisecond)
					return mockInner.CreateShipment(ctx, req)
				},
			}
			svc := NewShippingService(repo, carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), slowCarrier), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

			type res struct {
				sh      *store.Shipment
				existed bool
				err     error
			}
			ordID := prefix + "-ret-" + uuid.NewString()[:8]
			rrID := "rr-" + uuid.NewString()[:8]
			results := make([]res, 2)
			var wg sync.WaitGroup
			start := make(chan struct{})
			for i := 0; i < 2; i++ {
				wg.Add(1)
				go func(idx int) {
					defer wg.Done()
					<-start
					sh, existed, err := svc.CreateReturnShipment(context.Background(), CreateReturnShipmentInput{
						OrderID: ordID, ReturnRequestID: rrID, ShopID: "shop-conc", BuyerID: "buyer-conc",
						CarrierCode: carrier.CodeMockExpress, WeightGrams: 500,
						FromAddress: carrier.Address{ProvinceCode: "79", WardCode: "79001"},
						ToAddress:   carrier.Address{ProvinceCode: "01", WardCode: "01001"},
					})
					results[idx] = res{sh: sh, existed: existed, err: err}
				}(i)
			}
			close(start)
			wg.Wait()

			if results[0].err != nil || results[1].err != nil {
				t.Fatalf("concurrent CreateReturnShipment errors: r0=%v r1=%v", results[0].err, results[1].err)
			}
			if got := carrierCalls.Load(); got != 1 {
				t.Fatalf("carrier CreateShipment(RETURN) must be called ONCE across concurrent callers, got %d", got)
			}
			if results[0].sh.ID != results[1].sh.ID || results[0].sh.TrackingCode != results[1].sh.TrackingCode {
				t.Fatalf("both callers must receive the exact same RETURN shipment: r0=%+v r1=%+v", results[0].sh, results[1].sh)
			}
			if results[0].existed == results[1].existed {
				t.Fatalf("expected one caller alreadyExisted=false and one alreadyExisted=true, got %v and %v", results[0].existed, results[1].existed)
			}
		})
	}

	t.Run("in-memory repo", func(t *testing.T) {
		runConcurrentSuite(t, newMemRepo(), "mem-conc")
	})

	t.Run("real Postgres with pg_advisory_lock", func(t *testing.T) {
		dsn := strings.TrimSpace(os.Getenv("TEST_SHIPPING_DB_URL"))
		if dsn == "" {
			t.Skip("set TEST_SHIPPING_DB_URL to run real Postgres concurrency test")
		}
		ctx := context.Background()
		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			t.Fatalf("pgxpool.New: %v", err)
		}
		defer pool.Close()
		lockPool, err := store.NewLockPool(ctx, dsn)
		if err != nil {
			t.Fatalf("NewLockPool: %v", err)
		}
		defer lockPool.Close()

		pgStore := store.NewPGStoreWithLockPool(pool, lockPool)
		if err := pgStore.MigrateAndSeed(ctx); err != nil {
			t.Fatalf("MigrateAndSeed: %v", err)
		}
		pgPrefix := "pgconc-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(), `DELETE FROM shipments WHERE order_id LIKE $1`, pgPrefix+"%")
		})
		runConcurrentSuite(t, pgStore, pgPrefix)

		t.Run("advisory lock respects context timeout and releases cleanly", func(t *testing.T) {
			lockOrd := pgPrefix + "-lock-timeout"
			unlock, err := pgStore.AcquireOrderDirectionLock(ctx, lockOrd, store.DirectionReturn)
			if err != nil {
				t.Fatalf("first AcquireOrderDirectionLock: %v", err)
			}
			shortCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
			defer cancel()
			if _, err := pgStore.AcquireOrderDirectionLock(shortCtx, lockOrd, store.DirectionReturn); err == nil {
				t.Fatalf("expected timeout error while advisory lock is held")
			}
			unlock()
			// After unlock, acquiring the same lock must succeed immediately.
			unlock2, err := pgStore.AcquireOrderDirectionLock(ctx, lockOrd, store.DirectionReturn)
			if err != nil {
				t.Fatalf("AcquireOrderDirectionLock after unlock failed: %v", err)
			}
			unlock2()
		})

		t.Run("10 concurrent CreateShipment on MaxConns=2 main pool with 200ms carrier never starve", func(t *testing.T) {
			mainCfg, err := pgxpool.ParseConfig(dsn)
			if err != nil {
				t.Fatalf("ParseConfig: %v", err)
			}
			mainCfg.MaxConns = 2
			mainPool, err := pgxpool.NewWithConfig(ctx, mainCfg)
			if err != nil {
				t.Fatalf("NewWithConfig(main): %v", err)
			}
			defer mainPool.Close()

			narrowLockPool, err := store.NewLockPool(ctx, dsn)
			if err != nil {
				t.Fatalf("NewLockPool: %v", err)
			}
			defer narrowLockPool.Close()

			narrowStore := store.NewPGStoreWithLockPool(mainPool, narrowLockPool)
			mockInner := carrier.NewMockExpressAdapter()
			slowCarrier := &reviewRecordingCarrier{
				Carrier: mockInner,
				createFn: func(cctx context.Context, req carrier.ShipmentRequest) (*carrier.ShipmentResult, error) {
					time.Sleep(200 * time.Millisecond)
					return mockInner.CreateShipment(cctx, req)
				},
			}
			svc := NewShippingService(narrowStore, carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), slowCarrier), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

			runCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()

			const total = 10
			errs := make([]error, total)
			var wg sync.WaitGroup
			start := make(chan struct{})
			for i := 0; i < total; i++ {
				wg.Add(1)
				go func(idx int) {
					defer wg.Done()
					<-start
					_, _, errs[idx] = svc.CreateShipment(runCtx, CreateShipmentInput{
						OrderID:     pgPrefix + "-pool-" + string(rune('a'+idx)),
						ShopID:      "shop-pool",
						BuyerID:     "buyer-pool",
						CarrierCode: carrier.CodeMockExpress,
						WeightGrams: 500,
					})
				}(i)
			}
			close(start)
			wg.Wait()

			for i, e := range errs {
				if e != nil {
					t.Fatalf("order %d failed under MaxConns=2 main pool: %v", i, e)
				}
			}
		})

		t.Run("exhausted lock pool fails fast with ErrCarrierUnavailable without blocking main pool", func(t *testing.T) {
			oneCfg, err := pgxpool.ParseConfig(dsn)
			if err != nil {
				t.Fatalf("ParseConfig: %v", err)
			}
			oneCfg.MaxConns = 1
			tinyLockPool, err := pgxpool.NewWithConfig(ctx, oneCfg)
			if err != nil {
				t.Fatalf("NewWithConfig(tinyLockPool): %v", err)
			}
			defer tinyLockPool.Close()

			isoStore := store.NewPGStoreWithLockPool(pool, tinyLockPool)
			unlock, err := isoStore.AcquireOrderDirectionLock(ctx, pgPrefix+"-held", store.DirectionForward)
			if err != nil {
				t.Fatalf("hold single lock slot: %v", err)
			}
			defer unlock()

			svc := NewShippingService(isoStore, carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), carrier.NewMockExpressAdapter()), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
			shortCtx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
			defer cancel()
			if _, _, err := svc.CreateShipment(shortCtx, CreateShipmentInput{
				OrderID: pgPrefix + "-blocked", ShopID: "shop-pool", BuyerID: "buyer-pool",
				CarrierCode: carrier.CodeMockExpress, WeightGrams: 500,
			}); !errors.Is(err, ErrCarrierUnavailable) {
				t.Fatalf("expected ErrCarrierUnavailable when lock pool is exhausted, got %v", err)
			}
			// Main query pool must remain completely unblocked while lock pool is full.
			provs, err := svc.ListProvinces(ctx, "Ha Noi")
			if err != nil || len(provs) == 0 {
				t.Fatalf("main pool query must succeed while lock pool is full: provs=%+v err=%v", provs, err)
			}
		})
	})
}
