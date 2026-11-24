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
	mu                       sync.Mutex
	failErr                  error
	succeededEvents          []string
	failedEvents             []string
	expiredEvents            []string
	refundedEvents           []string
	refundRejectedEvents     []string
	shipmentEvents           []string
	shipmentCreateFailEvents []string
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

func (h *fakeEventHandler) HandlePaymentRefundRejected(_ context.Context, eventID string, _ PaymentRefundRejectedPayload) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.refundRejectedEvents = append(h.refundRejectedEvents, eventID)
	return h.failErr
}

func (h *fakeEventHandler) HandleShipmentUpdated(_ context.Context, eventID string, _ ShipmentUpdatedPayload) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.shipmentEvents = append(h.shipmentEvents, eventID)
	return h.failErr
}

func (h *fakeEventHandler) HandleShipmentCreateFailed(_ context.Context, eventID string, _ ShipmentCreateFailedPayload) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.shipmentCreateFailEvents = append(h.shipmentCreateFailEvents, eventID)
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

func TestOutboxRelay_PublishesOrderReturnedEvent(t *testing.T) {
	payload, err := json.Marshal(OrderReturnedPayload{
		OrderID:         "ord-ret-1",
		OrderCode:       "NM-20261123-RET00001",
		CheckoutGroupID: "grp-ret-1",
		BuyerID:         "buyer-1",
		ShopID:          "shop-1",
		PaymentMethod:   "COD",
		FromStatus:      "SHIPPING",
		TotalPayableVND: 250000,
		Reason:          "Buyer refused COD delivery",
		WasPaid:         false,
	})
	if err != nil {
		t.Fatalf("marshal OrderReturnedPayload: %v", err)
	}
	st := &fakeOutboxStore{
		events: []db.OutboxEvent{
			{
				ID:          "evt-ret-001",
				EventType:   SubjectOrderReturned,
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
	if n := relay.FlushOnce(context.Background()); n != 1 {
		t.Fatalf("expected 1 published order.returned event, got %d", n)
	}
	msg := pub.msgs[0]
	if msg.Subject != SubjectOrderReturned {
		t.Fatalf("subject = %q, want %q", msg.Subject, SubjectOrderReturned)
	}
	if msg.Header.Get(nats.MsgIdHdr) != "evt-ret-001" {
		t.Fatalf("Nats-Msg-Id = %q, want evt-ret-001", msg.Header.Get(nats.MsgIdHdr))
	}
	var env Envelope
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	var got OrderReturnedPayload
	if err := json.Unmarshal(env.Payload, &got); err != nil {
		t.Fatalf("unmarshal order.returned payload: %v", err)
	}
	if got.OrderID != "ord-ret-1" || got.FromStatus != "SHIPPING" || got.TotalPayableVND != 250000 || got.CheckoutGroupID != "grp-ret-1" {
		t.Fatalf("unexpected order.returned payload: %+v", got)
	}
}

func TestOutboxRelay_PublishesOrderReturnUpdatedEvent(t *testing.T) {
	payload, err := json.Marshal(OrderReturnUpdatedPayload{
		ReturnRequestID:    "rr-1",
		OrderID:            "ord-1",
		OrderCode:          "NM-20261123-RET00002",
		CheckoutGroupID:    "grp-1",
		BuyerID:            "buyer-1",
		ShopID:             "shop-1",
		Status:             "AWAITING_SHIPMENT",
		PreviousStatus:     "PENDING",
		Reason:             "Seller accepted return",
		ReturnTrackingCode: "RET-123",
		DeadlineAt:         "2026-11-28T00:00:00Z",
		ActorRole:          "seller",
	})
	if err != nil {
		t.Fatalf("marshal OrderReturnUpdatedPayload: %v", err)
	}
	st := &fakeOutboxStore{
		events: []db.OutboxEvent{
			{
				ID:          "evt-ru-001",
				EventType:   SubjectOrderReturnUpdated,
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
	if n := relay.FlushOnce(context.Background()); n != 1 {
		t.Fatalf("expected 1 published order.return_updated event, got %d", n)
	}
	msg := pub.msgs[0]
	if msg.Subject != SubjectOrderReturnUpdated {
		t.Fatalf("subject = %q, want %q", msg.Subject, SubjectOrderReturnUpdated)
	}
	var env Envelope
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	var got OrderReturnUpdatedPayload
	if err := json.Unmarshal(env.Payload, &got); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if got.ReturnRequestID != "rr-1" || got.Status != "AWAITING_SHIPMENT" || got.PreviousStatus != "PENDING" || got.ReturnTrackingCode != "RET-123" {
		t.Fatalf("unexpected order.return_updated payload: %+v", got)
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

func TestConsumer_ShipmentCreateFailed_DispatchAndMalformedDLQ(t *testing.T) {
	pub := &recordingPublisher{}
	dlqStore := &fakeDLQStore{}
	handler := &fakeEventHandler{}
	log := logger.New("order-test", logger.WithWriter(io.Discard))

	consumer := NewConsumer(nil, pub, dlqStore, handler, log, "NEXUSMART")

	// 1. Valid shipment.create_failed -> dispatches to HandleShipmentCreateFailed + ActionAck.
	validPayload, _ := json.Marshal(ShipmentCreateFailedPayload{
		JobID:        "job-1",
		OrderID:      "ord-1",
		ShopID:       "shop-1",
		CarrierCode:  "ghn",
		ServiceCode:  "standard",
		Attempts:     8,
		ErrorMessage: "carrier 503 exhausted",
		OccurredAt:   "2026-11-24T10:00:00Z",
	})
	validEnv, _ := json.Marshal(Envelope{
		EventID:    "evt-scf-1",
		EventType:  SubjectShipmentCreateFailed,
		OccurredAt: time.Now().UTC(),
		Payload:    validPayload,
	})
	action, err := consumer.ProcessRawMessage(context.Background(), SubjectShipmentCreateFailed, validEnv, nil, 1)
	if err != nil || action != ActionAck {
		t.Fatalf("expected ActionAck for shipment.create_failed, got action=%v err=%v", action, err)
	}
	if len(handler.shipmentCreateFailEvents) != 1 || handler.shipmentCreateFailEvents[0] != "evt-scf-1" {
		t.Fatalf("expected shipmentCreateFailEvents=[evt-scf-1], got %v", handler.shipmentCreateFailEvents)
	}

	// 2. Malformed outer JSON envelope on shipment.create_failed -> immediate ActionDLQ.
	action, _ = consumer.ProcessRawMessage(context.Background(), SubjectShipmentCreateFailed, []byte("{bad-json"), nil, 1)
	if action != ActionDLQ {
		t.Fatalf("expected ActionDLQ for malformed shipment.create_failed envelope, got %v", action)
	}
	if len(pub.msgs) != 1 || pub.msgs[0].Subject != "dlq.shipment.create_failed" {
		t.Fatalf("expected DLQ subject dlq.shipment.create_failed, got %+v", pub.msgs)
	}
	if len(dlqStore.records) != 1 {
		t.Fatalf("expected 1 DLQ DB record, got %d", len(dlqStore.records))
	}

	// 3. Malformed inner payload JSON -> ActionNak on attempt 1, ActionDLQ at maxAttempts (5).
	badPayloadEnv, _ := json.Marshal(Envelope{
		EventID:    "evt-scf-bad",
		EventType:  SubjectShipmentCreateFailed,
		OccurredAt: time.Now().UTC(),
		Payload:    json.RawMessage(`"not-an-object"`),
	})
	if act1, _ := consumer.ProcessRawMessage(context.Background(), SubjectShipmentCreateFailed, badPayloadEnv, nil, 1); act1 != ActionNak {
		t.Fatalf("expected ActionNak on attempt 1 for malformed inner payload, got %v", act1)
	}
	if act5, _ := consumer.ProcessRawMessage(context.Background(), SubjectShipmentCreateFailed, badPayloadEnv, nil, 5); act5 != ActionDLQ {
		t.Fatalf("expected ActionDLQ at attempt 5 for malformed inner payload, got %v", act5)
	}
	if len(pub.msgs) != 2 || pub.msgs[1].Subject != "dlq.shipment.create_failed" || len(dlqStore.records) != 2 {
		t.Fatalf("expected 2 DLQ publishes/records for shipment.create_failed, got pub=%d db=%d", len(pub.msgs), len(dlqStore.records))
	}
}
