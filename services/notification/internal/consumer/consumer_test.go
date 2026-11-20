package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/nexusmart/core/services/notification/internal/service"
)

type fakeMsg struct {
	subject   string
	data      []byte
	headers   map[string]string
	delivered uint64
	acked     bool
	naked     bool
	nakDelay  time.Duration
}

func (f *fakeMsg) Subject() string                    { return f.subject }
func (f *fakeMsg) Data() []byte                       { return f.data }
func (f *fakeMsg) Header(key string) string           { return f.headers[key] }
func (f *fakeMsg) NumDelivered() uint64               { return f.delivered }
func (f *fakeMsg) Ack() error                         { f.acked = true; return nil }
func (f *fakeMsg) NakWithDelay(d time.Duration) error { f.naked = true; f.nakDelay = d; return nil }

type stubProcessor struct {
	fn func(ctx context.Context, subject string, env service.Envelope) (bool, error)
}

func (s stubProcessor) ProcessEvent(ctx context.Context, subject string, env service.Envelope) (bool, error) {
	return s.fn(ctx, subject, env)
}

type dlqRecord struct {
	eventID  string
	subject  string
	errMsg   string
	attempts int
}

type stubDLQ struct {
	mu      sync.Mutex
	records []dlqRecord
	pubSubj []string
	pubData [][]byte
}

func (d *stubDLQ) InsertDeadLetter(_ context.Context, eventID, subject string, _ []byte, errMsg string, attempts int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.records = append(d.records, dlqRecord{
		eventID:  eventID,
		subject:  subject,
		errMsg:   errMsg,
		attempts: attempts,
	})
	return nil
}

func (d *stubDLQ) PublishDLQ(subject string, data []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pubSubj = append(d.pubSubj, subject)
	d.pubData = append(d.pubData, data)
	return nil
}

func TestConsumerRoutesMalformedPoisonMessageDirectlyToDLQ(t *testing.T) {
	dlq := &stubDLQ{}
	c := NewTestableConsumer(
		stubProcessor{fn: func(_ context.Context, _ string, _ service.Envelope) (bool, error) {
			t.Fatal("processor must not be called for invalid JSON envelope")
			return false, nil
		}},
		dlq,
		dlq,
		5,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	msg := &fakeMsg{
		subject:   "order.created",
		data:      []byte("{not-valid-json"),
		delivered: 1,
	}
	c.HandleMessage(context.Background(), msg)

	if !msg.acked || msg.naked {
		t.Fatalf("poison message must be Acked after DLQ routing, acked=%v naked=%v", msg.acked, msg.naked)
	}
	if len(dlq.records) != 1 || dlq.records[0].subject != "order.created" {
		t.Fatalf("expected 1 DB dead_letter_events record, got %+v", dlq.records)
	}
	if len(dlq.pubSubj) != 1 || dlq.pubSubj[0] != "dlq.order.created" {
		t.Fatalf("expected 1 NATS DLQ publish to dlq.order.created, got %+v", dlq.pubSubj)
	}
}

func TestConsumerRetriesThenRoutesToDLQAfterMaxAttempts(t *testing.T) {
	dlq := &stubDLQ{}
	c := NewTestableConsumer(
		stubProcessor{fn: func(_ context.Context, _ string, _ service.Envelope) (bool, error) {
			return false, errors.New("smtp unavailable")
		}},
		dlq,
		dlq,
		5,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	raw, _ := json.Marshal(service.Envelope{
		EventID:   "evt-fail-99",
		EventType: "payment.succeeded",
		Payload:   json.RawMessage(`{"user_id":"u1"}`),
	})

	// Attempts 1..4 must NakWithDelay and NOT send to DLQ yet.
	for attempt := uint64(1); attempt <= 4; attempt++ {
		msg := &fakeMsg{subject: "payment.succeeded", data: raw, delivered: attempt}
		c.HandleMessage(context.Background(), msg)
		if !msg.naked || msg.acked {
			t.Fatalf("attempt %d should NakWithDelay, got acked=%v naked=%v", attempt, msg.acked, msg.naked)
		}
		if len(dlq.records) != 0 || len(dlq.pubSubj) != 0 {
			t.Fatalf("attempt %d should not route to DLQ yet", attempt)
		}
	}

	// Attempt 5 (>= maxAttempts) must store in dead_letter_events + publish dlq.payment.succeeded + Ack!
	finalMsg := &fakeMsg{subject: "payment.succeeded", data: raw, delivered: 5}
	c.HandleMessage(context.Background(), finalMsg)
	if !finalMsg.acked || finalMsg.naked {
		t.Fatalf("final attempt must Ack after DLQ routing, got acked=%v naked=%v", finalMsg.acked, finalMsg.naked)
	}
	if len(dlq.records) != 1 || dlq.records[0].eventID != "evt-fail-99" || dlq.records[0].attempts != 5 {
		t.Fatalf("unexpected DLQ DB record: %+v", dlq.records)
	}
	if len(dlq.pubSubj) != 1 || dlq.pubSubj[0] != "dlq.payment.succeeded" {
		t.Fatalf("unexpected DLQ NATS subject: %+v", dlq.pubSubj)
	}
}
