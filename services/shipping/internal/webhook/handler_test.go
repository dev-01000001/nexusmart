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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/nexusmart/core/pkg/common/auth"
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
func (s *stubRepo) GetProvince(_ context.Context, _ string) (*address.Province, error) {
	return nil, store.ErrNotFound
}
func (s *stubRepo) ListWards(_ context.Context, _, _ string) ([]address.Ward, error) {
	return nil, nil
}
func (s *stubRepo) GetWard(_ context.Context, _ string) (*address.Ward, error) {
	return nil, store.ErrNotFound
}
func (s *stubRepo) GetRepresentativeWard(_ context.Context, _ string) (*address.Ward, error) {
	return nil, store.ErrNotFound
}
func (s *stubRepo) MapLegacyAddress(_ context.Context, _, _, _ string) (*address.LegacyMatch, error) {
	return &address.LegacyMatch{}, nil
}
func (s *stubRepo) GetShipmentByOrderID(_ context.Context, orderID, direction string) (*store.Shipment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := store.NormalizeDirection(direction)
	sh, ok := s.shipments[orderID+":"+dir]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *sh
	return &cp, nil
}
func (s *stubRepo) GetActiveShipmentByOrderID(ctx context.Context, orderID, direction string) (*store.Shipment, error) {
	sh, err := s.GetShipmentByOrderID(ctx, orderID, direction)
	if err != nil {
		return nil, err
	}
	if store.IsInactiveShipment(direction, sh.Status) {
		return nil, store.ErrNotFound
	}
	return sh, nil
}
func (s *stubRepo) CountInactiveShipmentsByOrder(_ context.Context, _ string, _ string) (int, error) {
	return 0, nil
}
func (s *stubRepo) AcquireOrderDirectionLock(_ context.Context, _, _ string) (func(), error) {
	return func() {}, nil
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
func (s *stubRepo) GetShipmentsByTrackingCodes(_ context.Context, trackingCodes []string) ([]store.Shipment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]store.Shipment, 0, len(trackingCodes))
	seen := make(map[string]bool, len(trackingCodes))
	for _, code := range trackingCodes {
		if seen[code] {
			continue
		}
		seen[code] = true
		if sh, ok := s.byTracking[code]; ok {
			out = append(out, *sh)
		}
	}
	return out, nil
}
func (s *stubRepo) ListShipmentEvents(_ context.Context, _ string) ([]store.ShipmentEvent, error) {
	return nil, nil
}
func (s *stubRepo) CreateShipment(_ context.Context, sh *store.Shipment, _ *store.ShipmentEvent) (*store.Shipment, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sh.Direction = store.NormalizeDirection(sh.Direction)
	cp := *sh
	cp.Direction = store.NormalizeDirection(cp.Direction)
	s.shipments[cp.OrderID+":"+cp.Direction] = &cp
	s.byTracking[cp.TrackingCode] = &cp
	ret := cp
	return &ret, false, nil
}
func (s *stubRepo) CreateShipmentWithOutbox(ctx context.Context, sh *store.Shipment, initEv *store.ShipmentEvent, _ *store.JobOutboxParams) (*store.Shipment, bool, error) {
	return s.CreateShipment(ctx, sh, initEv)
}
func (s *stubRepo) GetActiveShipmentCreateJob(_ context.Context, _, _ string) (*store.ShipmentCreateJob, error) {
	return nil, store.ErrNotFound
}
func (s *stubRepo) GetShipmentCreateJobByID(_ context.Context, _ string) (*store.ShipmentCreateJob, error) {
	return nil, store.ErrNotFound
}
func (s *stubRepo) EnqueueShipmentCreateJob(_ context.Context, job *store.ShipmentCreateJob) (*store.ShipmentCreateJob, bool, error) {
	cp := *job
	return &cp, false, nil
}
func (s *stubRepo) CancelActiveShipmentCreateJobs(_ context.Context, _, _, _ string) (int64, error) {
	return 0, nil
}
func (s *stubRepo) ClaimDueShipmentCreateJobs(_ context.Context, _ int, _ time.Duration, _ time.Time) ([]store.ShipmentCreateJob, string, error) {
	return nil, "", nil
}
func (s *stubRepo) CompleteShipmentCreateJob(ctx context.Context, _, _ string, sh *store.Shipment, initEv *store.ShipmentEvent, _ *store.JobOutboxParams) (*store.Shipment, bool, error) {
	return s.CreateShipment(ctx, sh, initEv)
}
func (s *stubRepo) RecordShipmentCreateJobFailure(_ context.Context, _, _, _ string, _ *time.Time, _ bool, _ *store.JobOutboxParams, _ time.Time) (*store.ShipmentCreateJob, bool, error) {
	return nil, false, nil
}
func (s *stubRepo) CancelShipmentByID(_ context.Context, _ string, _ string) (*store.Shipment, error) {
	return nil, nil
}
func (s *stubRepo) CancelShipmentWithEvent(_ context.Context, _, _, _, _ string) (*store.Shipment, bool, error) {
	return nil, false, nil
}
func (s *stubRepo) RecordRedelivery(_ context.Context, _, _ string, _ int32, _, _ string) (*store.Shipment, error) {
	return nil, nil
}
func (s *stubRepo) ApplyWebhookEvent(_ context.Context, params store.WebhookUpdateParams) (*store.Shipment, store.WebhookUpdateOutcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var sh *store.Shipment
	if params.TrackingCode != "" {
		sh = s.byTracking[params.TrackingCode]
	} else if params.OrderID != "" {
		sh = s.shipments[params.OrderID+":"+store.DirectionForward]
	}
	if sh == nil {
		return nil, "", store.ErrNotFound
	}
	if strings.TrimSpace(params.CarrierCode) == "" || !strings.EqualFold(strings.TrimSpace(params.CarrierCode), sh.CarrierCode) {
		return nil, "", store.ErrCarrierMismatch
	}
	if s.webhookSeen[params.EventID] {
		return nil, store.WebhookOutcomeDuplicate, nil
	}
	s.webhookSeen[params.EventID] = true
	if !store.CanTransitionStatus(sh.Status, params.InternalStatus) || (sh.LastEventAt != nil && params.OccurredAt.Before(*sh.LastEventAt)) {
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
func (s *stubRepo) ListActiveShipments(_ context.Context, _ int, _ *time.Time, _ string) ([]store.Shipment, error) {
	return nil, nil
}
func (s *stubRepo) GetShopShippingSettings(_ context.Context, _ string) (*store.ShopShippingSettings, error) {
	return nil, store.ErrNotFound
}
func (s *stubRepo) UpsertShopShippingSettings(_ context.Context, st *store.ShopShippingSettings) (*store.ShopShippingSettings, error) {
	return st, nil
}
func (s *stubRepo) ListCarrierConfigs(_ context.Context) ([]store.CarrierConfig, error) {
	return nil, nil
}
func (s *stubRepo) GetCarrierConfig(_ context.Context, _ string) (*store.CarrierConfig, error) {
	return nil, store.ErrNotFound
}
func (s *stubRepo) UpdateCarrierConfig(_ context.Context, cfg *store.CarrierConfig, _ []store.FallbackRate) (*store.CarrierConfig, error) {
	return cfg, nil
}
func (s *stubRepo) SeedCarrierTokenIfEmpty(_ context.Context, _ string, _ []byte) error { return nil }
func (s *stubRepo) ListFallbackRates(_ context.Context, _ string) ([]store.FallbackRate, error) {
	return nil, nil
}
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
	sh, _ := repo.GetShipmentByOrderID(context.Background(), "ord-1", store.DirectionForward)
	if sh.Status != carrier.StatusDelivering {
		t.Fatalf("expected status to remain delivering, got %s", sh.Status)
	}
}

func TestWebhookHandlerInvalidSignatureDoesNotLogSignature(t *testing.T) {
	const secret = "test-shipping-webhook-secret"
	const badSig = "deadbeefcafebabe0123456789abcdef"

	var logBuf bytes.Buffer
	jsonLog := slog.New(slog.NewJSONHandler(&logBuf, nil))

	repo := newStubRepo()
	agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc := service.NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h := NewHandler(secret, svc, jsonLog)
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	h.nowFn = func() time.Time { return now }

	r := chi.NewRouter()
	r.Post("/webhooks/shipping/{carrier}", h.ServeHTTP)

	raw, err := json.Marshal(map[string]any{
		"event_id":      "evt-bad-log",
		"tracking_code": "GHN-TEST-001",
		"raw_status":    "delivering",
		"occurred_at":   now.Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("marshal webhook body: %v", err)
	}

	// 1. Non-empty invalid signature: raw signature must not appear in log output.
	req := httptest.NewRequest(http.MethodPost, "/webhooks/shipping/ghn", bytes.NewReader(raw))
	req.Header.Set("X-Signature", badSig)
	req.Header.Set("X-Timestamp", strconv.FormatInt(now.Unix(), 10))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on invalid signature, got %d body=%s", rec.Code, rec.Body.String())
	}
	logged := logBuf.String()
	if strings.Contains(logged, badSig) {
		t.Fatalf("log output must not contain raw signature %q, got: %s", badSig, logged)
	}
	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(logBuf.Bytes()), &entry); err != nil {
		t.Fatalf("decode JSON log entry: %v (raw=%s)", err, logged)
	}
	if _, exists := entry["signature"]; exists {
		t.Fatalf("log entry must not include raw signature field, got: %s", logged)
	}
	if present, ok := entry["signature_present"].(bool); !ok || !present {
		t.Fatalf("expected signature_present=true, got %v (raw=%s)", entry["signature_present"], logged)
	}

	// 2. Missing signature: signature_present=false.
	logBuf.Reset()
	req = httptest.NewRequest(http.MethodPost, "/webhooks/shipping/ghn", bytes.NewReader(raw))
	req.Header.Set("X-Timestamp", strconv.FormatInt(now.Unix(), 10))
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on missing signature, got %d body=%s", rec.Code, rec.Body.String())
	}
	entry = nil
	if err := json.Unmarshal(bytes.TrimSpace(logBuf.Bytes()), &entry); err != nil {
		t.Fatalf("decode JSON log entry: %v (raw=%s)", err, logBuf.String())
	}
	if present, ok := entry["signature_present"].(bool); !ok || present {
		t.Fatalf("expected signature_present=false, got %v (raw=%s)", entry["signature_present"], logBuf.String())
	}
}

func TestWebhookHandlerRejectsCarrierMismatch(t *testing.T) {
	const secret = "test-shipping-webhook-secret"
	repo := newStubRepo()
	_, _, _ = repo.CreateShipment(context.Background(), &store.Shipment{
		ID:           uuid.NewString(),
		OrderID:      "ord-mismatch-http",
		ShopID:       "shop-1",
		CarrierCode:  "ghn",
		TrackingCode: "GHN-MISMATCH-001",
		Status:       carrier.StatusCreated,
	}, nil)

	agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc := service.NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h := NewHandler(secret, svc, slog.New(slog.NewTextHandler(io.Discard, nil)))
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	h.nowFn = func() time.Time { return now }

	r := chi.NewRouter()
	r.Post("/webhooks/shipping/{carrier}", h.ServeHTTP)

	postTo := func(carrierPath string, body map[string]any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/webhooks/shipping/"+carrierPath, bytes.NewReader(raw))
		req.Header.Set("X-Signature", Sign(secret, raw))
		req.Header.Set("X-Timestamp", strconv.FormatInt(now.Unix(), 10))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	// Wrong carrier path with valid HMAC signature must be rejected with 4xx and not change state.
	for _, wrongCarrier := range []string{"fake_carrier", "mock_express"} {
		rec := postTo(wrongCarrier, map[string]any{
			"event_id":      "evt-wrong-" + wrongCarrier,
			"tracking_code": "GHN-MISMATCH-001",
			"raw_status":    "delivering",
			"occurred_at":   now.Format(time.RFC3339),
		})
		if rec.Code < 400 || rec.Code >= 500 {
			t.Fatalf("expected 4xx for wrong carrier %q, got HTTP %d body=%s", wrongCarrier, rec.Code, rec.Body.String())
		}
	}

	sh, _ := repo.GetShipmentByOrderID(context.Background(), "ord-mismatch-http", store.DirectionForward)
	if sh.Status != carrier.StatusCreated {
		t.Fatalf("shipment status must remain created after mismatched carrier webhooks, got %s", sh.Status)
	}
	if repo.outboxCount != 0 {
		t.Fatalf("expected 0 outbox events after mismatched carrier webhooks, got %d", repo.outboxCount)
	}

	// Matching carrier with the same event_id must still be accepted cleanly (not poisoned by rejected call).
	rec := postTo("ghn", map[string]any{
		"event_id":      "evt-wrong-fake_carrier",
		"tracking_code": "GHN-MISMATCH-001",
		"raw_status":    "delivering",
		"occurred_at":   now.Format(time.RFC3339),
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on matching carrier after rejected mismatch, got HTTP %d body=%s", rec.Code, rec.Body.String())
	}
	sh, _ = repo.GetShipmentByOrderID(context.Background(), "ord-mismatch-http", store.DirectionForward)
	if sh.Status != carrier.StatusDelivering {
		t.Fatalf("expected status delivering after matching carrier webhook, got %s", sh.Status)
	}
}

func TestLabelHandlerAuthAndPrintableHTML(t *testing.T) {
	repo := newStubRepo()
	_, _, _ = repo.CreateShipment(context.Background(), &store.Shipment{
		ID:               uuid.NewString(),
		OrderID:          "ord-label-1",
		ShopID:           "shop-1",
		BuyerID:          "buyer-1",
		Direction:        store.DirectionForward,
		CarrierCode:      "ghn",
		ServiceCode:      "STANDARD",
		TrackingCode:     "GHN-LBL-001",
		Status:           carrier.StatusCreated,
		FeeVND:           24000,
		CODAmountVND:     200000,
		WeightGrams:      500,
		FromProvinceCode: "79",
		FromWardCode:     "79001",
		ToProvinceCode:   "01",
		ToWardCode:       "01001",
		CreatedAt:        time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC),
	}, nil)

	jwtMgr, err := auth.NewManager("test-label-jwt-secret", auth.DefaultAccessTTL)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	buyerTok, _, _, _ := jwtMgr.SignAccessToken("buyer-1", "buyer", "")
	otherBuyerTok, _, _, _ := jwtMgr.SignAccessToken("buyer-2", "buyer", "")
	sellerTok, _, _, _ := jwtMgr.SignAccessToken("seller-1", "seller", "shop-1")
	otherSellerTok, _, _, _ := jwtMgr.SignAccessToken("seller-2", "seller", "shop-2")
	adminTok, _, _, _ := jwtMgr.SignAccessToken("admin-1", "admin", "")

	agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc := service.NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	lh := NewLabelHandler(svc)

	r := chi.NewRouter()
	r.Group(func(authed chi.Router) {
		authed.Use(auth.HTTPMiddleware(jwtMgr, true))
		authed.Get("/labels/{tracking_code}", lh.ServeHTTP)
	})

	get := func(path, tok string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	if rec := get("/labels/GHN-LBL-001", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", rec.Code)
	}
	if rec := get("/labels/GHN-LBL-001", otherBuyerTok); rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for other buyer, got %d", rec.Code)
	}
	if rec := get("/labels/GHN-LBL-001", otherSellerTok); rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for other seller, got %d", rec.Code)
	}
	if rec := get("/labels/GHN-MISSING", adminTok); rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown tracking code, got %d", rec.Code)
	}

	for _, tc := range []struct {
		name string
		tok  string
	}{
		{"owner buyer", buyerTok},
		{"owner seller", sellerTok},
		{"admin", adminTok},
	} {
		rec := get("/labels/GHN-LBL-001", tc.tok)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d body=%s", tc.name, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
			t.Fatalf("%s: expected text/html, got %q", tc.name, rec.Header().Get("Content-Type"))
		}
		if !strings.Contains(rec.Body.String(), "GHN-LBL-001") || !strings.Contains(rec.Body.String(), "Phiếu vận đơn") {
			t.Fatalf("%s: unexpected label body: %s", tc.name, rec.Body.String())
		}
	}
}

func TestReview_LabelHandlerSecurityCSPXSSJSONErrorsAndSellerAsBuyer(t *testing.T) {
	repo := newStubRepo()
	xssPayload := `<script>alert("xss")</script><img src=x onerror=alert(1)>`
	_, _, _ = repo.CreateShipment(context.Background(), &store.Shipment{
		ID:               uuid.NewString(),
		OrderID:          "ord-" + xssPayload,
		ShopID:           "shop-2",
		BuyerID:          "user-seller-1",
		Direction:        store.DirectionForward,
		CarrierCode:      "ghn",
		ServiceCode:      "STD-" + xssPayload,
		TrackingCode:     "GHN-SEC-001",
		Status:           carrier.StatusCreated,
		FromProvinceCode: "79" + xssPayload,
		ToWardCode:       "01001" + xssPayload,
		CreatedAt:        time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC),
	}, nil)

	jwtMgr, _ := auth.NewManager("test-label-sec-secret", auth.DefaultAccessTTL)
	// Seller of shop-1 is the buyer of this shop-2 shipment.
	sellerAsBuyerTok, _, _, _ := jwtMgr.SignAccessToken("user-seller-1", "seller", "shop-1")
	otherSellerTok, _, _, _ := jwtMgr.SignAccessToken("user-seller-3", "seller", "shop-3")
	// Mục 1: Demoted buyer whose stale JWT still carries shop_id="shop-2" must be denied.
	demotedBuyerStaleShopTok, _, _, _ := jwtMgr.SignAccessToken("user-demoted", "buyer", "shop-2")

	agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc := service.NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	lh := NewLabelHandler(svc)

	r := chi.NewRouter()
	r.Group(func(authed chi.Router) {
		authed.Use(auth.HTTPMiddleware(jwtMgr, true))
		authed.Get("/labels/{tracking_code}", lh.ServeHTTP)
	})

	get := func(path, tok string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	// 1. Seller who is buyer_id must get 200 OK.
	rec := get("/labels/GHN-SEC-001", sellerAsBuyerTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("seller-as-buyer expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	// 2. Security headers: exact CSP sandbox + X-Content-Type-Options: nosniff.
	wantCSP := "sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data:; frame-ancestors 'none'"
	if got := rec.Header().Get("Content-Security-Policy"); got != wantCSP {
		t.Fatalf("Content-Security-Policy = %q, want %q", got, wantCSP)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}

	// 3. No inline JS / event handlers and all injected <script>/<img> tags must be HTML-escaped.
	body := rec.Body.String()
	for _, forbidden := range []string{"onclick", "window.print", "<script>", "<img "} {
		if strings.Contains(strings.ToLower(body), forbidden) {
			t.Fatalf("label HTML must not contain %q; body=%s", forbidden, body)
		}
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Fatalf("expected HTML-escaped &lt;script&gt; in body, got: %s", body)
	}

	// 4. Error responses (403, 404) must have Content-Type application/json, NOT text/plain.
	for _, tc := range []struct {
		name     string
		path     string
		tok      string
		wantCode int
	}{
		{"forbidden other seller", "/labels/GHN-SEC-001", otherSellerTok, http.StatusForbidden},
		{"forbidden demoted buyer with stale shop_id", "/labels/GHN-SEC-001", demotedBuyerStaleShopTok, http.StatusForbidden},
		{"not found", "/labels/GHN-NOPE", sellerAsBuyerTok, http.StatusNotFound},
	} {
		errRec := get(tc.path, tc.tok)
		if errRec.Code != tc.wantCode {
			t.Fatalf("%s: expected HTTP %d, got %d", tc.name, tc.wantCode, errRec.Code)
		}
		if !strings.HasPrefix(errRec.Header().Get("Content-Type"), "application/json") {
			t.Fatalf("%s: expected Content-Type application/json, got %q", tc.name, errRec.Header().Get("Content-Type"))
		}
	}
}

func TestGHTKWebhookCanonicalNativeReturnAndErrors(t *testing.T) {
	const secret = "test-ghtk-webhook-secret"
	repo := newStubRepo()
	_, _, _ = repo.CreateShipment(context.Background(), &store.Shipment{
		ID:           uuid.NewString(),
		OrderID:      "ord-ghtk-1",
		ShopID:       "shop-1",
		CarrierCode:  carrier.CodeGHTK,
		TrackingCode: "GHTK-AABBCCDD11",
		Status:       carrier.StatusCreated,
		Direction:    store.DirectionForward,
	}, nil)
	_, _, _ = repo.CreateShipment(context.Background(), &store.Shipment{
		ID:              uuid.NewString(),
		OrderID:         "ord-ghtk-1",
		ShopID:          "shop-1",
		CarrierCode:     carrier.CodeGHTK,
		TrackingCode:    "GHTK-RET1122334",
		Status:          carrier.StatusCreated,
		Direction:       store.DirectionReturn,
		ReturnRequestID: "ret-wh-1",
	}, nil)

	var logBuf bytes.Buffer
	jsonLog := slog.New(slog.NewJSONHandler(&logBuf, nil))
	agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc := service.NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h := NewHandler(secret, svc, jsonLog)
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	h.nowFn = func() time.Time { return now }

	r := chi.NewRouter()
	r.Post("/webhooks/shipping/{carrier}", h.ServeHTTP)

	sendRawTo := func(carrierPath string, raw []byte, sigPrefix bool, tsHeader string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/shipping/"+carrierPath, bytes.NewReader(raw))
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
	sendRaw := func(raw []byte, sigPrefix bool, tsHeader string) *httptest.ResponseRecorder {
		return sendRawTo("ghtk", raw, sigPrefix, tsHeader)
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
	fwd, _ := repo.GetShipmentByOrderID(context.Background(), "ord-ghtk-1", store.DirectionForward)
	if fwd.Status != carrier.StatusDelivering {
		t.Fatalf("expected forward status delivering, got %s", fwd.Status)
	}

	// 1b. Native GHTK payload sent to mismatched carrier path must log resolved event_id (=hash), not empty payload.EventID.
	logBuf.Reset()
	mismatchNative, _ := json.Marshal(map[string]any{
		"hash":        "ghtk-hash-mismatch-99",
		"label_id":    "GHTK-AABBCCDD11",
		"status_id":   6,
		"action_time": "2026-11-20 12:01:00",
	})
	recMismatch := sendRawTo("ghn", mismatchNative, false, validTS)
	if recMismatch.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 CARRIER_MISMATCH for GHTK shipment on /ghn path, got %d body=%s", recMismatch.Code, recMismatch.Body.String())
	}
	if !strings.Contains(logBuf.String(), `"event_id":"ghtk-hash-mismatch-99"`) {
		t.Fatalf("expected mismatch log to include resolved hash event_id, got: %s", logBuf.String())
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
		t.Fatalf("expected status=returned direction=RETURN, got %+v", respMap)
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
