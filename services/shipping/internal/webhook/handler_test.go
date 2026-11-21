package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/nexusmart/core/services/shipping/internal/address"
	"github.com/nexusmart/core/services/shipping/internal/carrier"
	"github.com/nexusmart/core/services/shipping/internal/service"
	"github.com/nexusmart/core/services/shipping/internal/store"
)

type stubRepo struct {
	mu          sync.Mutex
	shipments   map[string]*store.Shipment
	byTracking  map[string]*store.Shipment
	webhookSeen map[string]bool
	outboxCount int
}

func newStubRepo() *stubRepo {
	return &stubRepo{
		shipments:   make(map[string]*store.Shipment),
		byTracking:  make(map[string]*store.Shipment),
		webhookSeen: make(map[string]bool),
	}
}

func (s *stubRepo) ListProvinces(_ context.Context, _ string) ([]address.Province, error) {
	return nil, nil
}
func (s *stubRepo) ListWards(_ context.Context, _, _ string) ([]address.Ward, error) {
	return nil, nil
}
func (s *stubRepo) GetWard(_ context.Context, _ string) (*address.Ward, error) {
	return nil, store.ErrNotFound
}
func (s *stubRepo) MapLegacyAddress(_ context.Context, _, _, _ string) (*address.LegacyMatch, error) {
	return &address.LegacyMatch{}, nil
}
func whShipKey(orderID, dir string) string {
	return orderID + "|" + store.NormalizeDirection(dir)
}

func (s *stubRepo) GetShipmentByOrderID(ctx context.Context, orderID string) (*store.Shipment, error) {
	return s.GetShipmentByOrderIDAndDirection(ctx, orderID, store.DirectionForward)
}
func (s *stubRepo) GetShipmentByOrderIDAndDirection(_ context.Context, orderID, direction string) (*store.Shipment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sh, ok := s.shipments[whShipKey(orderID, direction)]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *sh
	return &cp, nil
}
func (s *stubRepo) GetShipmentByTrackingCode(_ context.Context, trackingCode string) (*store.Shipment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sh, ok := s.byTracking[trackingCode]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *sh
	return &cp, nil
}
func (s *stubRepo) ListShipmentEvents(_ context.Context, _ string) ([]store.ShipmentEvent, error) {
	return nil, nil
}
func (s *stubRepo) CreateShipment(_ context.Context, sh *store.Shipment, _ *store.ShipmentEvent) (*store.Shipment, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sh.Direction = store.NormalizeDirection(sh.Direction)
	cp := *sh
	s.shipments[whShipKey(sh.OrderID, sh.Direction)] = &cp
	s.byTracking[sh.TrackingCode] = &cp
	ret := cp
	return &ret, false, nil
}
func (s *stubRepo) CancelShipment(_ context.Context, _ string, _ string) (*store.Shipment, error) {
	return nil, nil
}
func (s *stubRepo) CancelShipmentWithEvent(_ context.Context, _, _, _, _ string) (*store.Shipment, bool, error) {
	return nil, false, nil
}
func (s *stubRepo) ApplyWebhookEvent(_ context.Context, params store.WebhookUpdateParams) (*store.Shipment, store.WebhookUpdateOutcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.webhookSeen[params.EventID] {
		return nil, store.WebhookOutcomeDuplicate, nil
	}
	s.webhookSeen[params.EventID] = true

	sh := s.byTracking[params.TrackingCode]
	if sh == nil {
		sh = s.shipments[whShipKey(params.OrderID, store.DirectionForward)]
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
	s.outboxCount++
	cp := *sh
	return &cp, store.WebhookOutcomeApplied, nil
}
func (s *stubRepo) ListActiveShipments(_ context.Context, _ int) ([]store.Shipment, error) {
	return nil, nil
}
func (s *stubRepo) FetchPendingOutbox(_ context.Context, _ int) ([]store.OutboxEvent, error) {
	return nil, nil
}
func (s *stubRepo) MarkOutboxPublished(_ context.Context, _ string) error { return nil }
func (s *stubRepo) IsEventProcessed(_ context.Context, _ string) (bool, error) {
	return false, nil
}
func (s *stubRepo) MarkEventProcessed(_ context.Context, _, _ string) (bool, error) {
	return true, nil
}
func (s *stubRepo) InsertDeadLetter(_ context.Context, _, _ string, _ []byte, _ string, _ int) error {
	return nil
}

func TestWebhookHandlerHMACIdempotencyAndOutOfOrder(t *testing.T) {
	const secret = "test-shipping-webhook-secret"
	repo := newStubRepo()
	repo.CreateShipment(context.Background(), &store.Shipment{
		ID:           uuid.NewString(),
		OrderID:      "ord-1",
		ShopID:       "shop-1",
		CarrierCode:  "ghn",
		TrackingCode: "GHN-TEST-001",
		Status:       carrier.StatusCreated,
	}, nil)

	agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc := service.NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h := NewHandler(secret, svc, slog.New(slog.NewTextHandler(io.Discard, nil)))
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	h.nowFn = func() time.Time { return now }

	r := chi.NewRouter()
	r.Post("/webhooks/shipping/{carrier}", h.ServeHTTP)

	post := func(body map[string]any, sig string, tsHeader string) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/webhooks/shipping/ghn", bytes.NewReader(raw))
		if sig == "AUTO" {
			sig = Sign(secret, raw)
		}
		if sig != "" {
			req.Header.Set("X-Signature", sig)
		}
		if tsHeader != "" {
			req.Header.Set("X-Timestamp", tsHeader)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	validTS := strconv.FormatInt(now.Unix(), 10)

	// 1. Invalid signature -> 401
	rec := post(map[string]any{
		"event_id":      "evt-bad",
		"tracking_code": "GHN-TEST-001",
		"raw_status":    "delivering",
		"occurred_at":   now.Format(time.RFC3339),
	}, "deadbeef", validTS)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on invalid HMAC, got %d body=%s", rec.Code, rec.Body.String())
	}

	// 2. Missing X-Timestamp -> 401 (replay protection required)
	rec = post(map[string]any{
		"event_id":      "evt-no-ts",
		"tracking_code": "GHN-TEST-001",
		"raw_status":    "delivering",
		"occurred_at":   now.Format(time.RFC3339),
	}, "AUTO", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on missing X-Timestamp, got %d body=%s", rec.Code, rec.Body.String())
	}

	// 3. Stale X-Timestamp (> 5 min) -> 401
	rec = post(map[string]any{
		"event_id":      "evt-old-ts",
		"tracking_code": "GHN-TEST-001",
		"raw_status":    "delivering",
		"occurred_at":   now.Format(time.RFC3339),
	}, "AUTO", strconv.FormatInt(now.Add(-10*time.Minute).Unix(), 10))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on stale X-Timestamp, got %d body=%s", rec.Code, rec.Body.String())
	}

	// 4. Valid webhook -> 200 OK, applied
	rec = post(map[string]any{
		"event_id":      "evt-ok-1",
		"tracking_code": "GHN-TEST-001",
		"order_id":      "ord-1",
		"raw_status":    "delivering",
		"description":   "Đang giao",
		"occurred_at":   now.Format(time.RFC3339),
	}, "AUTO", validTS)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on valid webhook, got %d body=%s", rec.Code, rec.Body.String())
	}
	if repo.outboxCount != 1 {
		t.Fatalf("expected 1 outbox event, got %d", repo.outboxCount)
	}

	// 5. Duplicate event_id -> 200 OK idempotently, no extra outbox event
	rec = post(map[string]any{
		"event_id":      "evt-ok-1",
		"tracking_code": "GHN-TEST-001",
		"order_id":      "ord-1",
		"raw_status":    "delivering",
		"occurred_at":   now.Format(time.RFC3339),
	}, "AUTO", validTS)
	if rec.Code != http.StatusOK || repo.outboxCount != 1 {
		t.Fatalf("expected 200 duplicate ignored, code=%d outbox=%d", rec.Code, repo.outboxCount)
	}

	// 6. Out-of-order older webhook -> 200 OK stale ignored, status stays delivering
	rec = post(map[string]any{
		"event_id":      "evt-stale-1",
		"tracking_code": "GHN-TEST-001",
		"order_id":      "ord-1",
		"raw_status":    "picking",
		"occurred_at":   now.Add(-5 * time.Minute).Format(time.RFC3339),
	}, "AUTO", validTS)
	if rec.Code != http.StatusOK || repo.outboxCount != 1 {
		t.Fatalf("expected 200 stale ignored, code=%d outbox=%d", rec.Code, repo.outboxCount)
	}
	sh, _ := repo.GetShipmentByOrderID(context.Background(), "ord-1")
	if sh.Status != carrier.StatusDelivering {
		t.Fatalf("expected status to remain delivering, got %s", sh.Status)
	}
}

func TestGHTKWebhookCanonicalNativeReturnAndErrors(t *testing.T) {
	const secret = "test-ghtk-webhook-secret"
	repo := newStubRepo()
	repo.CreateShipment(context.Background(), &store.Shipment{
		ID:           uuid.NewString(),
		OrderID:      "ord-ghtk-1",
		ShopID:       "shop-1",
		CarrierCode:  carrier.CodeGHTK,
		TrackingCode: "GHTK-AABBCCDD11",
		Status:       carrier.StatusCreated,
		Direction:    store.DirectionForward,
	}, nil)
	repo.CreateShipment(context.Background(), &store.Shipment{
		ID:              uuid.NewString(),
		OrderID:         "ord-ghtk-1",
		ShopID:          "shop-1",
		CarrierCode:     carrier.CodeGHTK,
		TrackingCode:    "GHTK-RET1122334",
		Status:          carrier.StatusCreated,
		Direction:       store.DirectionReturn,
		ReturnRequestID: "ret-wh-1",
	}, nil)

	agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc := service.NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h := NewHandler(secret, svc, nil)
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	h.nowFn = func() time.Time { return now }

	r := chi.NewRouter()
	r.Post("/webhooks/shipping/{carrier}", h.ServeHTTP)

	sendRaw := func(raw []byte, sigPrefix bool, tsHeader string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/shipping/ghtk", bytes.NewReader(raw))
		sig := Sign(secret, raw)
		if sigPrefix {
			sig = "sha256=" + sig
		}
		req.Header.Set("X-Signature", sig)
		req.Header.Set("X-Timestamp", tsHeader)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	validTS := strconv.FormatInt(now.Unix(), 10)

	// 1. Native GHTK payload with sha256= signature prefix, numeric status_id=5 (delivering), SQL action_time.
	nativeBody, _ := json.Marshal(map[string]any{
		"hash":        "ghtk-hash-1",
		"label_id":    "GHTK-AABBCCDD11",
		"partner_id":  "ord-ghtk-1",
		"status_id":   5,
		"action_time": "2026-11-20 12:00:00",
		"reason":      "Đã giao hàng/Chưa đối soát",
	})
	rec := sendRaw(nativeBody, true, validTS)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for native GHTK webhook, got %d body=%s", rec.Code, rec.Body.String())
	}
	fwd, _ := repo.GetShipmentByOrderID(context.Background(), "ord-ghtk-1")
	if fwd.Status != carrier.StatusDelivering {
		t.Fatalf("expected forward status delivering, got %s", fwd.Status)
	}

	// 2. Canonical GHTK webhook updating return shipment via numeric code "21" (returned).
	retBody, _ := json.Marshal(map[string]any{
		"event_id":      "ghtk-ret-21",
		"tracking_code": "GHTK-RET1122334",
		"raw_status":    "21",
		"description":   "Đã trả hàng về shop",
		"occurred_at":   now.Format(time.RFC3339),
	})
	rec = sendRaw(retBody, false, validTS)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for return GHTK webhook, got %d body=%s", rec.Code, rec.Body.String())
	}
	var respMap map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &respMap)
	if respMap["status"] != carrier.StatusReturned || respMap["direction"] != store.DirectionReturn {
		t.Fatalf("expected status=returned direction=return, got %+v", respMap)
	}

	// 3. Duplicate & stale out-of-order rejection on GHTK return shipment (now terminal).
	recDup := sendRaw(retBody, false, validTS)
	if recDup.Code != http.StatusOK {
		t.Fatalf("expected 200 on duplicate GHTK webhook, got %d", recDup.Code)
	}
	var dupMap map[string]any
	_ = json.Unmarshal(recDup.Body.Bytes(), &dupMap)
	if dupMap["outcome"] != string(store.WebhookOutcomeDuplicate) {
		t.Fatalf("expected duplicate outcome, got %+v", dupMap)
	}

	staleBody, _ := json.Marshal(map[string]any{
		"event_id":      "ghtk-ret-stale",
		"tracking_code": "GHTK-RET1122334",
		"raw_status":    "20",
		"occurred_at":   now.Add(time.Minute).Format(time.RFC3339),
	})
	recStale := sendRaw(staleBody, false, validTS)
	if recStale.Code != http.StatusOK {
		t.Fatalf("expected 200 on stale terminal GHTK webhook, got %d", recStale.Code)
	}
	var staleMap map[string]any
	_ = json.Unmarshal(recStale.Body.Bytes(), &staleMap)
	if staleMap["outcome"] != string(store.WebhookOutcomeStale) {
		t.Fatalf("expected stale outcome on terminal return shipment, got %+v", staleMap)
	}

	// 4. Error responses: non-numeric X-Timestamp (401), malformed JSON (400), invalid occurred_at (400), unknown status (400), missing shipment (404).
	if rec := sendRaw(retBody, false, "not-an-int"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on non-numeric X-Timestamp, got %d", rec.Code)
	}
	if rec := sendRaw([]byte("{bad-json"), false, validTS); rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on malformed JSON, got %d", rec.Code)
	}
	badTime, _ := json.Marshal(map[string]any{"event_id": "e-t", "tracking_code": "GHTK-AABBCCDD11", "raw_status": "6", "occurred_at": "bad-time"})
	if rec := sendRaw(badTime, false, validTS); rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on invalid occurred_at, got %d", rec.Code)
	}
	badStatus, _ := json.Marshal(map[string]any{"event_id": "e-s", "tracking_code": "GHTK-AABBCCDD11", "raw_status": "9999"})
	if rec := sendRaw(badStatus, false, validTS); rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on unknown status, got %d", rec.Code)
	}
	notFound, _ := json.Marshal(map[string]any{"event_id": "e-nf", "tracking_code": "GHTK-MISSING", "raw_status": "6"})
	if rec := sendRaw(notFound, false, validTS); rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 on unknown shipment, got %d", rec.Code)
	}

	// VerifySignature edge cases.
	if VerifySignature(nil, []byte("body"), "abcd") || VerifySignature([]byte(secret), []byte("body"), "not-hex!") {
		t.Fatalf("expected VerifySignature false for empty secret / invalid hex")
	}
}
