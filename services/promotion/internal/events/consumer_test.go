package events_test

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/promotion/internal/events"
	"github.com/nexusmart/core/services/promotion/internal/store"
)

type recordingDLQ struct {
	subjects []string
	reasons  []string
}

func (r *recordingDLQ) PublishDLQ(_ context.Context, subject string, _ []byte, headers map[string]string) error {
	r.subjects = append(r.subjects, subject)
	r.reasons = append(r.reasons, headers["x-dlq-reason"])
	return nil
}

func TestOrderCompletedConsumer_IdempotentCoinCredit(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemoryStore()
	log := logger.New("promotion-events-test", logger.WithWriter(io.Discard))
	consumer := events.NewOrderCompletedConsumer(mem, nil, "NEXUSMART", log)
	dlq := &recordingDLQ{}
	consumer.SetDLQPublisher(dlq)

	makeEvent := func(eventID, orderID, buyerID string, coins int64) []byte {
		payload, _ := json.Marshal(events.OrderCompletedPayload{
			OrderID:         orderID,
			OrderCode:       "NM-001",
			BuyerID:         buyerID,
			ShopID:          "shop-1",
			SubtotalVND:     200000,
			CommissionVND:   10000,
			TotalPayableVND: 200000,
			CoinsReward:     coins,
		})
		raw, _ := json.Marshal(events.Envelope{
			EventID:    eventID,
			EventType:  events.SubjectOrderCompleted,
			TraceID:    "4bf92f3577b34da6a3ce929d0e0e4736",
			OccurredAt: "2026-11-20T02:35:00Z",
			Payload:    payload,
		})
		return raw
	}

	raw := makeEvent("evt-001", "ord-001", "buyer-1", 1500)

	// Deliver exact same event 5 times -> credited ONCE
	for i := 0; i < 5; i++ {
		ack, err := consumer.ProcessRawMessage(ctx, raw, nil, 1)
		if err != nil || !ack {
			t.Fatalf("attempt %d unexpected: ack=%v err=%v", i, ack, err)
		}
	}

	// Deliver a DIFFERENT event_id for the SAME order_id -> still credited ONCE (reference_id = order_id)
	dupOrderRaw := makeEvent("evt-002", "ord-001", "buyer-1", 1500)
	ack, err := consumer.ProcessRawMessage(ctx, dupOrderRaw, nil, 1)
	if err != nil || !ack {
		t.Fatalf("dup order event unexpected: ack=%v err=%v", ack, err)
	}

	bal, txns, err := mem.GetCoinBalance(ctx, "buyer-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if bal != 1500 || len(txns) != 1 || mem.AccountBalance("buyer-1") != 1500 || mem.LedgerSum("buyer-1") != 1500 {
		t.Fatalf("expected exactly one 1500 coin credit, got bal=%d txns=%d acct=%d sum=%d",
			bal, len(txns), mem.AccountBalance("buyer-1"), mem.LedgerSum("buyer-1"))
	}
	if len(dlq.subjects) != 0 {
		t.Fatalf("expected no DLQ messages, got %v", dlq.subjects)
	}

	// Zero coins_reward should process cleanly without creating a zero-delta coin tx
	zeroRaw := makeEvent("evt-003", "ord-002", "buyer-1", 0)
	ack, err = consumer.ProcessRawMessage(ctx, zeroRaw, nil, 1)
	if err != nil || !ack {
		t.Fatalf("zero reward event unexpected: ack=%v err=%v", ack, err)
	}
	if _, txns, _ = mem.GetCoinBalance(ctx, "buyer-1", 10); len(txns) != 1 {
		t.Fatalf("expected still 1 coin tx after 0-reward order, got %d", len(txns))
	}
}

func TestOrderCompletedConsumer_PoisonMessageSentToDLQ(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemoryStore()
	log := logger.New("promotion-events-test", logger.WithWriter(io.Discard))
	consumer := events.NewOrderCompletedConsumer(mem, nil, "NEXUSMART", log)
	dlq := &recordingDLQ{}
	consumer.SetDLQPublisher(dlq)

	// Invalid JSON -> sent to DLQ and acked
	ack, err := consumer.ProcessRawMessage(ctx, []byte("{not-json"), nil, 1)
	if err != nil || !ack {
		t.Fatalf("expected poison message acked after DLQ, got ack=%v err=%v", ack, err)
	}
	// Missing event_id -> sent to DLQ and acked
	ack, err = consumer.ProcessRawMessage(ctx, []byte(`{"event_type":"order.completed","payload":{}}`), nil, 1)
	if err != nil || !ack {
		t.Fatalf("expected missing event_id acked after DLQ, got ack=%v err=%v", ack, err)
	}

	if len(dlq.subjects) != 2 || dlq.subjects[0] != "dlq.order.completed" || dlq.subjects[1] != "dlq.order.completed" {
		t.Fatalf("expected 2 DLQ publishes to dlq.order.completed, got %v", dlq.subjects)
	}
}
