package events

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/order/internal/db"
)

type recordingPublisher struct {
	mu   sync.Mutex
	msgs []*nats.Msg
	err  error
}

func (p *recordingPublisher) PublishMsg(m *nats.Msg, _ ...nats.PubOpt) (*nats.PubAck, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return nil, p.err
	}
	cp := *m
	cp.Header = nats.Header{}
	for k, vals := range m.Header {
		cp.Header[k] = append([]string(nil), vals...)
	}
	p.msgs = append(p.msgs, &cp)
	return &nats.PubAck{Stream: "NEXUSMART", Sequence: uint64(len(p.msgs))}, nil
}

type fakeOutboxStore struct {
	events []db.OutboxEvent
}

func (f *fakeOutboxStore) ProcessPendingOutbox(ctx context.Context, limit int32, publishFn func(ctx context.Context, ev db.OutboxEvent) error) (int, error) {
	published := 0
	now := time.Now().UTC()
	for i := range f.events {
		if int32(published) >= limit {
			break
		}
		if f.events[i].PublishedAt != nil {
			continue
		}
		if err := publishFn(ctx, f.events[i]); err != nil {
			f.events[i].Attempts++
			f.events[i].LastError = err.Error()
			continue
		}
		f.events[i].PublishedAt = &now
		f.events[i].Attempts++
		published++
	}
	return published, nil
}

type fakeDLQStore struct {
	mu      sync.Mutex
	records []db.InsertDeadLetterEventParams
}

func (f *fakeDLQStore) InsertDeadLetterEvent(_ context.Context, arg db.InsertDeadLetterEventParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records = append(f.records, arg)
	return nil
}

type fakeEventHandler struct {
	mu              sync.Mutex
	failErr         error
	succeededEvents []string
	failedEvents    []string
	expiredEvents   []string
	refundedEvents  []string
	shipmentEvents  []string
}

func (h *fakeEventHandler) HandlePaymentSucceeded(_ context.Context, eventID string, _ PaymentSucceededPayload) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.succeededEvents = append(h.succeededEvents, eventID)
	return h.failErr
}

func (h *fakeEventHandler) HandlePaymentFailed(_ context.Context, eventID string, _ PaymentFailedPayload) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.failedEvents = append(h.failedEvents, eventID)
	return h.failErr
}

func (h *fakeEventHandler) HandlePaymentExpired(_ context.Context, eventID string, _ PaymentFailedPayload) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.expiredEvents = append(h.expiredEvents, eventID)
	return h.failErr
}

func (h *fakeEventHandler) HandlePaymentRefunded(_ context.Context, eventID string, _ PaymentRefundedPayload) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.refundedEvents = append(h.refundedEvents, eventID)
	return h.failErr
}

func (h *fakeEventHandler) HandleShipmentUpdated(_ context.Context, eventID string, _ ShipmentUpdatedPayload) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.shipmentEvents = append(h.shipmentEvents, eventID)
	return h.failErr
}

func TestOutboxRelay_PublishesCanonicalEnvelopeWithHeaders(t *testing.T) {
	payload, _ := json.Marshal(OrderCreatedPayload{
		CheckoutGroupID: "grp-1",
		OrderID:         "ord-1",
		OrderCode:       "NM-20261120-ABCDEF01",
		BuyerID:         "buyer-1",
		ShopID:          "shop-1",
		PaymentMethod:   "COD",
		TotalPayableVND: 250000,
	})
	st := &fakeOutboxStore{
		events: []db.OutboxEvent{
			{
				ID:          "evt-001",
				EventType:   SubjectOrderCreated,
				TraceID:     "4bf92f3577b34da6a3ce929d0e0e4736",
				Traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
				PayloadJSON: payload,
				CreatedAt:   time.Now().UTC(),
			},
		},
	}
	pub := &recordingPublisher{}
	log := logger.New("order-test", logger.WithWriter(io.Discard))

	relay := NewOutboxRelay(st, pub, log, 100*time.Millisecond, nil)
	n := relay.FlushOnce(context.Background())
	if n != 1 {
		t.Fatalf("expected 1 published event, got %d", n)
	}
	if len(pub.msgs) != 1 {
		t.Fatalf("expected 1 NATS msg, got %d", len(pub.msgs))
	}

	msg := pub.msgs[0]
	if msg.Subject != SubjectOrderCreated {
		t.Fatalf("subject = %q, want %q", msg.Subject, SubjectOrderCreated)
	}
	if got := msg.Header.Get(nats.MsgIdHdr); got != "evt-001" {
		t.Fatalf("Nats-Msg-Id = %q, want evt-001", got)
	}
	if got := msg.Header.Get("traceparent"); got == "" {
		t.Fatalf("expected traceparent header to be propagated")
	}

	var env Envelope
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		t.Fatalf("unmarshal published envelope: %v", err)
	}
	if env.EventID != "evt-001" || env.EventType != SubjectOrderCreated || env.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("unexpected envelope: %+v", env)
	}
}

func TestConsumer_DispatchAndDLQRouting(t *testing.T) {
	pub := &recordingPublisher{}
	dlqStore := &fakeDLQStore{}
	handler := &fakeEventHandler{}
	log := logger.New("order-test", logger.WithWriter(io.Discard))

	consumer := NewConsumer(nil, pub, dlqStore, handler, log, "NEXUSMART")

	// 1. Valid payment.succeeded -> ActionAck
	validPayload, _ := json.Marshal(PaymentSucceededPayload{PaymentID: "p1", OrderGroupID: "g1"})
	validEnv, _ := json.Marshal(Envelope{
		EventID:    "evt-ok-1",
		EventType:  SubjectPaymentSucceeded,
		OccurredAt: time.Now().UTC(),
		Payload:    validPayload,
	})
	action, err := consumer.ProcessRawMessage(context.Background(), SubjectPaymentSucceeded, validEnv, nil, 1)
	if err != nil || action != ActionAck {
		t.Fatalf("expected ActionAck, got action=%v err=%v", action, err)
	}
	if len(handler.succeededEvents) != 1 || handler.succeededEvents[0] != "evt-ok-1" {
		t.Fatalf("expected succeeded handler invoked with evt-ok-1, got %v", handler.succeededEvents)
	}

	// 2. Malformed JSON -> immediately ActionDLQ + recorded in DLQ stream & DB.
	action, _ = consumer.ProcessRawMessage(context.Background(), SubjectPaymentFailed, []byte("{bad-json"), nil, 1)
	if action != ActionDLQ {
		t.Fatalf("expected ActionDLQ for malformed JSON, got %v", action)
	}
	if len(pub.msgs) != 1 || pub.msgs[0].Subject != "dlq.payment.failed" {
		t.Fatalf("expected DLQ publish to dlq.payment.failed, got %+v", pub.msgs)
	}
	if len(dlqStore.records) != 1 {
		t.Fatalf("expected 1 DLQ DB record, got %d", len(dlqStore.records))
	}

	// 3. Transient failure on attempts 1..4 -> ActionNak; attempt 5 -> ActionDLQ!
	handler.failErr = errors.New("downstream unavailable")
	failPayload, _ := json.Marshal(PaymentFailedPayload{PaymentID: "p2", OrderGroupID: "g2", Reason: "err"})
	failEnv, _ := json.Marshal(Envelope{
		EventID:    "evt-fail-retry",
		EventType:  SubjectPaymentFailed,
		OccurredAt: time.Now().UTC(),
		Payload:    failPayload,
	})
	for attempt := uint64(1); attempt <= 4; attempt++ {
		act, _ := consumer.ProcessRawMessage(context.Background(), SubjectPaymentFailed, failEnv, nil, attempt)
		if act != ActionNak {
			t.Fatalf("attempt %d: expected ActionNak, got %v", attempt, act)
		}
	}
	act5, _ := consumer.ProcessRawMessage(context.Background(), SubjectPaymentFailed, failEnv, nil, 5)
	if act5 != ActionDLQ {
		t.Fatalf("attempt 5: expected ActionDLQ, got %v", act5)
	}
	if len(pub.msgs) != 2 || len(dlqStore.records) != 2 {
		t.Fatalf("expected 2 DLQ publishes and 2 DLQ DB records, got pub=%d db=%d", len(pub.msgs), len(dlqStore.records))
	}
}

func TestConsumer_AllSubjectsMissingEventIDAndOutboxRun(t *testing.T) {
	pub := &recordingPublisher{}
	dlqStore := &fakeDLQStore{}
	handler := &fakeEventHandler{}
	consumer := NewConsumer(nil, pub, dlqStore, handler, nil, "")

	// Missing event_id -> DLQ immediately.
	noIDEnv, _ := json.Marshal(Envelope{EventType: SubjectPaymentExpired, Payload: json.RawMessage(`{}`)})
	if act, _ := consumer.ProcessRawMessage(context.Background(), SubjectPaymentExpired, noIDEnv, nil, 0); act != ActionDLQ {
		t.Fatalf("missing event_id: want ActionDLQ, got %v", act)
	}

	// Dispatch payment.expired, payment.refunded, shipment.updated (including empty EventType fallback and trace headers).
	cases := []struct {
		subject string
		evType  string
		id      string
		payload any
	}{
		{SubjectPaymentExpired, SubjectPaymentExpired, "ev-exp", PaymentFailedPayload{OrderGroupID: "g1"}},
		{SubjectPaymentRefunded, SubjectPaymentRefunded, "ev-ref", PaymentRefundedPayload{OrderID: "o1", RefundID: "r1"}},
		{SubjectShipmentUpdated, "", "ev-ship", ShipmentUpdatedPayload{OrderID: "o1", Status: "returned", Direction: "return", ReturnRequestID: "rr-1"}},
		{"payment.unknown", "payment.unknown", "ev-unk", map[string]string{"a": "b"}},
	}
	for _, tc := range cases {
		pBytes, _ := json.Marshal(tc.payload)
		envBytes, _ := json.Marshal(Envelope{EventID: tc.id, EventType: tc.evType, Payload: pBytes})
		act, err := consumer.ProcessRawMessage(context.Background(), tc.subject, envBytes, map[string]string{
			"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		}, 1)
		if err != nil || act != ActionAck {
			t.Fatalf("%s: want ActionAck, got act=%v err=%v", tc.subject, act, err)
		}
	}
	if len(handler.expiredEvents) != 1 || len(handler.refundedEvents) != 1 || len(handler.shipmentEvents) != 1 {
		t.Fatalf("unexpected dispatch counts: exp=%d ref=%d ship=%d",
			len(handler.expiredEvents), len(handler.refundedEvents), len(handler.shipmentEvents))
	}

	// Malformed payload for each known subject returns ActionNak on attempt 1.
	for _, subj := range []string{SubjectPaymentSucceeded, SubjectPaymentFailed, SubjectPaymentExpired, SubjectPaymentRefunded, SubjectShipmentUpdated} {
		envBytes, _ := json.Marshal(Envelope{EventID: "bad-" + subj, EventType: subj, Payload: json.RawMessage(`"not-an-object"`)})
		if act, _ := consumer.ProcessRawMessage(context.Background(), subj, envBytes, nil, 1); act != ActionNak {
			t.Fatalf("bad payload for %s: want ActionNak, got %v", subj, act)
		}
	}

	// Stop on empty consumer is safe no-op.
	if err := consumer.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// OutboxRelay.Run drains via trigger and exits cleanly on context cancellation.
	trigger := make(chan struct{}, 1)
	st := &fakeOutboxStore{
		events: []db.OutboxEvent{
			{ID: "ob-run-1", EventType: SubjectOrderRefunded, PayloadJSON: []byte(`{"order_id":"o1"}`), CreatedAt: time.Now().UTC()},
		},
	}
	relayPub := &recordingPublisher{}
	relay := NewOutboxRelay(st, relayPub, nil, 0, trigger)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		relay.Run(ctx)
	}()
	trigger <- struct{}{}
	time.Sleep(30 * time.Millisecond)
	cancel()
	<-done
	if len(relayPub.msgs) != 1 || relayPub.msgs[0].Subject != SubjectOrderRefunded {
		t.Fatalf("OutboxRelay.Run expected 1 published order.refunded event, got %+v", relayPub.msgs)
	}

	// FlushOnce handles publish errors gracefully.
	relayPub.err = errors.New("nats down")
	st.events = append(st.events, db.OutboxEvent{ID: "ob-fail", EventType: SubjectOrderReturnApproved, PayloadJSON: []byte(`{}`), CreatedAt: time.Now().UTC()})
	if n := relay.FlushOnce(context.Background()); n != 0 {
		t.Fatalf("expected 0 published on error, got %d", n)
	}
}
