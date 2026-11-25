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
	"github.com/nexusmart/core/services/shipping/internal/store"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
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

func (f *fakeJS) UpdateStream(cfg *nats.StreamConfig, _ ...nats.JSOpt) (*nats.StreamInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
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

func (r *eventsStubRepo) ProcessPendingOutbox(ctx context.Context, limit int, publishFn func(ctx context.Context, ev store.OutboxEvent) error) (int, error) {
	r.mu.Lock()
	batch := make([]store.OutboxEvent, 0, len(r.pending))
	for i := 0; i < len(r.pending) && (limit <= 0 || i < limit); i++ {
		batch = append(batch, r.pending[i])
	}
	r.mu.Unlock()

	published := 0
	for _, ev := range batch {
		if err := publishFn(ctx, ev); err != nil {
			return published, err
		}
		r.mu.Lock()
		r.publishedID = append(r.publishedID, ev.ID)
		var remaining []store.OutboxEvent
		for _, cur := range r.pending {
			if cur.ID != ev.ID {
				remaining = append(remaining, cur)
			}
		}
		r.pending = remaining
		r.mu.Unlock()
		published++
	}
	return published, nil
}

func (r *eventsStubRepo) IsEventProcessed(_ context.Context, eventID string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.processed[eventID], nil
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
		Direction:       store.DirectionReturn,
		ReturnRequestID: "ret-1",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	var decoded ShipmentUpdatedPayload
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if decoded.Direction != store.DirectionReturn || decoded.ReturnRequestID != "ret-1" {
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
		Direction:       store.DirectionReturn,
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
	tp := sdktrace.NewTracerProvider()
	prevTP := otel.GetTracerProvider()
	prevProp := otel.GetTextMapPropagator()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	})

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := newEventsStubRepo()
	js := newFakeJS()
	handler := &fakeCancelHandler{}

	c := NewConsumer(js, repo, handler, "NEXUSMART", 1, logger)
	if err := c.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	// Reset stopping state by constructing a fresh consumer for ProcessRawMessage assertions.
	c = NewConsumer(js, repo, handler, "NEXUSMART", 1, logger)

	const incomingTraceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	hdrs := map[string]string{"traceparent": incomingTraceparent}

	// 1. Valid order.cancelled message
	payloadRaw, _ := json.Marshal(OrderCancelledPayload{OrderID: "ord-c1", Reason: "buyer cancel"})
	envRaw, _ := json.Marshal(Envelope{EventID: "evt-c1", EventType: SubjectOrderCancelled, Payload: payloadRaw})
	act, err := c.ProcessRawMessage(context.Background(), SubjectOrderCancelled, envRaw, hdrs, 1)
	if err != nil || act != ActionAck {
		t.Fatalf("expected ActionAck on valid message, got act=%v err=%v", act, err)
	}
	if handler.calls.Load() != 1 || handler.last != "ord-c1" {
		t.Fatalf("expected handler called once with ord-c1, got calls=%d last=%s", handler.calls.Load(), handler.last)
	}

	// 2. Duplicate processed event skipped
	repo.processed["evt-c1"] = true
	act, err = c.ProcessRawMessage(context.Background(), SubjectOrderCancelled, envRaw, hdrs, 1)
	if err != nil || act != ActionAck || handler.calls.Load() != 1 {
		t.Fatalf("expected duplicate event skipped (ActionAck, calls=1), got act=%v calls=%d err=%v", act, handler.calls.Load(), err)
	}

	// 3. Malformed envelope -> DLQ (propagating traceparent header)
	act, _ = c.ProcessRawMessage(context.Background(), SubjectOrderCancelled, []byte("{not-json"), hdrs, 1)
	if act != ActionDLQ {
		t.Fatalf("expected ActionDLQ for malformed envelope, got %v", act)
	}

	// 4. Malformed payload (missing order_id) -> DLQ
	badPayEnv, _ := json.Marshal(Envelope{EventID: "evt-bad-pay", Payload: []byte(`{"order_id":""}`)})
	act, _ = c.ProcessRawMessage(context.Background(), SubjectOrderCancelled, badPayEnv, hdrs, 1)
	if act != ActionDLQ {
		t.Fatalf("expected ActionDLQ for missing order_id, got %v", act)
	}

	// 5. Handler error reaching maxAttempts (1) -> DLQ
	handler.err = errors.New("carrier down")
	errEnv, _ := json.Marshal(Envelope{EventID: "evt-fail-1", Payload: payloadRaw})
	act, _ = c.ProcessRawMessage(context.Background(), SubjectOrderCancelled, errEnv, hdrs, 1)
	if act != ActionDLQ {
		t.Fatalf("expected ActionDLQ on maxAttempts exhaustion, got %v", act)
	}

	if len(repo.deadLetters) != 3 || len(js.published) != 3 {
		t.Fatalf("expected 3 dead letters (malformed env, malformed payload, max retries), got db=%d nats=%d", len(repo.deadLetters), len(js.published))
	}
	for i, dlqMsg := range js.published {
		if dlqMsg.Header.Get("X-DLQ-Original-Subject") != SubjectOrderCancelled {
			t.Fatalf("dlq[%d] missing X-DLQ-Original-Subject: %+v", i, dlqMsg.Header)
		}
		if dlqMsg.Header.Get("traceparent") == "" {
			t.Fatalf("dlq[%d] missing propagated traceparent header: %+v", i, dlqMsg.Header)
		}
	}
}
