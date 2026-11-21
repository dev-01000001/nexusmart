package events

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nexusmart/core/services/shipping/internal/address"
	"github.com/nexusmart/core/services/shipping/internal/store"
)

type fakeJS struct {
	nats.JetStreamContext
	mu           sync.Mutex
	published    []*nats.Msg
	publishErr   error
	streams      map[string]*nats.StreamInfo
	addStreamErr error
}

func newFakeJS() *fakeJS {
	return &fakeJS{streams: make(map[string]*nats.StreamInfo)}
}

func (f *fakeJS) PublishMsg(m *nats.Msg, _ ...nats.PubOpt) (*nats.PubAck, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.publishErr != nil {
		return nil, f.publishErr
	}
	f.published = append(f.published, m)
	return &nats.PubAck{Stream: "NEXUSMART", Sequence: uint64(len(f.published))}, nil
}

func (f *fakeJS) StreamInfo(stream string, _ ...nats.JSOpt) (*nats.StreamInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if info, ok := f.streams[stream]; ok {
		return info, nil
	}
	return nil, nats.ErrStreamNotFound
}

func (f *fakeJS) AddStream(cfg *nats.StreamConfig, _ ...nats.JSOpt) (*nats.StreamInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.addStreamErr != nil {
		return nil, f.addStreamErr
	}
	info := &nats.StreamInfo{Config: *cfg}
	f.streams[cfg.Name] = info
	return info, nil
}

type eventsStubRepo struct {
	mu          sync.Mutex
	pending     []store.OutboxEvent
	publishedID []string
	processed   map[string]bool
	deadLetters []string
}

func newEventsStubRepo() *eventsStubRepo {
	return &eventsStubRepo{processed: make(map[string]bool)}
}

func (r *eventsStubRepo) ListProvinces(_ context.Context, _ string) ([]address.Province, error) {
	return nil, nil
}
func (r *eventsStubRepo) ListWards(_ context.Context, _, _ string) ([]address.Ward, error) {
	return nil, nil
}
func (r *eventsStubRepo) GetWard(_ context.Context, _ string) (*address.Ward, error) {
	return nil, store.ErrNotFound
}
func (r *eventsStubRepo) MapLegacyAddress(_ context.Context, _, _, _ string) (*address.LegacyMatch, error) {
	return &address.LegacyMatch{}, nil
}
func (r *eventsStubRepo) GetShipmentByOrderID(_ context.Context, _ string) (*store.Shipment, error) {
	return nil, store.ErrNotFound
}
func (r *eventsStubRepo) GetShipmentByOrderIDAndDirection(_ context.Context, _, _ string) (*store.Shipment, error) {
	return nil, store.ErrNotFound
}
func (r *eventsStubRepo) GetShipmentByTrackingCode(_ context.Context, _ string) (*store.Shipment, error) {
	return nil, store.ErrNotFound
}
func (r *eventsStubRepo) ListShipmentEvents(_ context.Context, _ string) ([]store.ShipmentEvent, error) {
	return nil, nil
}
func (r *eventsStubRepo) CreateShipment(_ context.Context, sh *store.Shipment, _ *store.ShipmentEvent) (*store.Shipment, bool, error) {
	return sh, false, nil
}
func (r *eventsStubRepo) CancelShipment(_ context.Context, _, _ string) (*store.Shipment, error) {
	return nil, nil
}
func (r *eventsStubRepo) CancelShipmentWithEvent(_ context.Context, _, _, _, _ string) (*store.Shipment, bool, error) {
	return nil, false, nil
}
func (r *eventsStubRepo) ApplyWebhookEvent(_ context.Context, _ store.WebhookUpdateParams) (*store.Shipment, store.WebhookUpdateOutcome, error) {
	return nil, store.WebhookOutcomeApplied, nil
}
func (r *eventsStubRepo) ListActiveShipments(_ context.Context, _ int) ([]store.Shipment, error) {
	return nil, nil
}
func (r *eventsStubRepo) FetchPendingOutbox(_ context.Context, _ int) ([]store.OutboxEvent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]store.OutboxEvent, len(r.pending))
	copy(out, r.pending)
	return out, nil
}
func (r *eventsStubRepo) MarkOutboxPublished(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.publishedID = append(r.publishedID, id)
	var remaining []store.OutboxEvent
	for _, ev := range r.pending {
		if ev.ID != id {
			remaining = append(remaining, ev)
		}
	}
	r.pending = remaining
	return nil
}
func (r *eventsStubRepo) IsEventProcessed(_ context.Context, eventID string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.processed[eventID], nil
}
func (r *eventsStubRepo) MarkEventProcessed(_ context.Context, eventID, _ string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.processed[eventID] {
		return false, nil
	}
	r.processed[eventID] = true
	return true, nil
}
func (r *eventsStubRepo) InsertDeadLetter(_ context.Context, eventID, _ string, _ []byte, _ string, _ int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deadLetters = append(r.deadLetters, eventID)
	return nil
}

type fakeCancelHandler struct {
	err   error
	calls atomic.Int32
	last  string
}

func (h *fakeCancelHandler) HandleOrderCancelled(_ context.Context, _, orderID, _ string) error {
	h.calls.Add(1)
	h.last = orderID
	return h.err
}

func TestShipmentUpdatedPayloadJSONAndEnsureStreams(t *testing.T) {
	raw, err := json.Marshal(ShipmentUpdatedPayload{
		ShipmentID:      "sh-1",
		OrderID:         "ord-1",
		ShopID:          "shop-1",
		CarrierCode:     "ghtk",
		TrackingCode:    "GHTK-1234567890",
		Status:          "returned",
		RawStatus:       "21",
		Description:     "Returned to shop",
		OccurredAt:      "2026-11-20T10:00:00Z",
		Direction:       "return",
		ReturnRequestID: "ret-1",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	var decoded ShipmentUpdatedPayload
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if decoded.Direction != "return" || decoded.ReturnRequestID != "ret-1" {
		t.Fatalf("unexpected decoded payload: %+v", decoded)
	}

	js := newFakeJS()
	if err := EnsureStreams(js, "", ""); err != nil {
		t.Fatalf("EnsureStreams failed: %v", err)
	}
	if _, ok := js.streams["NEXUSMART"]; !ok {
		t.Fatalf("expected NEXUSMART stream created")
	}
	if _, ok := js.streams["NEXUSMART_DLQ"]; !ok {
		t.Fatalf("expected NEXUSMART_DLQ stream created")
	}
	// Second call is idempotent
	if err := EnsureStreams(js, "NEXUSMART", "NEXUSMART_DLQ"); err != nil {
		t.Fatalf("second EnsureStreams failed: %v", err)
	}
}

func TestOutboxRelayFlushAndRun(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := newEventsStubRepo()
	js := newFakeJS()

	payloadBytes, _ := json.Marshal(ShipmentUpdatedPayload{
		ShipmentID:      "sh-1",
		OrderID:         "ord-1",
		Direction:       "return",
		ReturnRequestID: "ret-5",
		Status:          "returned",
	})
	repo.pending = append(repo.pending, store.OutboxEvent{
		ID:          "ob-1",
		EventID:     "evt-ob-1",
		Subject:     SubjectShipmentUpdated,
		Payload:     payloadBytes,
		Traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		TraceID:     "4bf92f3577b34da6a3ce929d0e0e4736",
		CreatedAt:   time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC),
	})

	relay := NewOutboxRelay(repo, js, 0, nil)
	if relay.interval != 250*time.Millisecond {
		t.Fatalf("expected default 250ms interval, got %v", relay.interval)
	}
	relay.Wake()
	relay.Wake() // second wake non-blocking

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		relay.Run(ctx)
		close(done)
	}()
	time.Sleep(30 * time.Millisecond)
	cancel()
	<-done

	if len(js.published) != 1 || len(repo.publishedID) != 1 || repo.publishedID[0] != "ob-1" {
		t.Fatalf("expected 1 outbox event published, got js=%d marked=%+v", len(js.published), repo.publishedID)
	}
	if js.published[0].Header.Get(nats.MsgIdHdr) != "evt-ob-1" || js.published[0].Header.Get("traceparent") == "" {
		t.Fatalf("expected MsgIdHdr and traceparent headers set, got %+v", js.published[0].Header)
	}

	// Nil js safe no-op
	nilRelay := NewOutboxRelay(repo, nil, 10*time.Millisecond, logger)
	nilRelay.FlushOnce(context.Background())
}

func TestConsumerHandleMsgIdempotencyAndDLQ(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := newEventsStubRepo()
	js := newFakeJS()
	handler := &fakeCancelHandler{}

	c := NewConsumer(js, repo, handler, "NEXUSMART", 1, logger)
	if err := c.Close(); err != nil {
		t.Fatalf("Close with nil sub failed: %v", err)
	}

	// 1. Valid order.cancelled message
	payloadRaw, _ := json.Marshal(OrderCancelledPayload{OrderID: "ord-c1", Reason: "buyer cancel"})
	envRaw, _ := json.Marshal(Envelope{EventID: "evt-c1", EventType: SubjectOrderCancelled, Payload: payloadRaw})
	msg := nats.NewMsg(SubjectOrderCancelled)
	msg.Data = envRaw
	msg.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	c.handleMsg(context.Background(), msg)
	if handler.calls.Load() != 1 || handler.last != "ord-c1" {
		t.Fatalf("expected handler called once with ord-c1, got calls=%d last=%s", handler.calls.Load(), handler.last)
	}

	// 2. Duplicate processed event skipped
	repo.processed["evt-c1"] = true
	c.handleMsg(context.Background(), msg)
	if handler.calls.Load() != 1 {
		t.Fatalf("expected duplicate event skipped, got calls=%d", handler.calls.Load())
	}

	// 3. Malformed envelope -> DLQ
	badEnv := nats.NewMsg(SubjectOrderCancelled)
	badEnv.Data = []byte("{not-json")
	c.handleMsg(context.Background(), badEnv)

	// 4. Malformed payload (missing order_id) -> DLQ
	badPayEnv, _ := json.Marshal(Envelope{EventID: "evt-bad-pay", Payload: []byte(`{"order_id":""}`)})
	badPayMsg := nats.NewMsg(SubjectOrderCancelled)
	badPayMsg.Data = badPayEnv
	c.handleMsg(context.Background(), badPayMsg)

	// 5. Handler error reaching maxAttempts (1) -> DLQ
	handler.err = errors.New("carrier down")
	errEnv, _ := json.Marshal(Envelope{EventID: "evt-fail-1", Payload: payloadRaw})
	errMsg := nats.NewMsg(SubjectOrderCancelled)
	errMsg.Data = errEnv
	c.handleMsg(context.Background(), errMsg)

	if len(repo.deadLetters) != 3 || len(js.published) != 3 {
		t.Fatalf("expected 3 dead letters (malformed env, malformed payload, max retries), got db=%d nats=%d", len(repo.deadLetters), len(js.published))
	}
}
