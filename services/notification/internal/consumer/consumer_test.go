package consumer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
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
	payload  []byte
	errMsg   string
	attempts int
}

type stubDLQ struct {
	mu      sync.Mutex
	records []dlqRecord
	pubSubj []string
	pubData [][]byte
}

func (d *stubDLQ) InsertDeadLetter(_ context.Context, eventID, subject string, payload []byte, errMsg string, attempts int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.records = append(d.records, dlqRecord{
		eventID:  eventID,
		subject:  subject,
		payload:  append([]byte(nil), payload...),
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

func TestConsumerOutOfOrderShipmentRetriesThenDLQ(t *testing.T) {
	dlq := &stubDLQ{}
	c := NewTestableConsumer(
		stubProcessor{fn: func(_ context.Context, _ string, _ service.Envelope) (bool, error) {
			return false, service.ErrOrderParticipantNotFound
		}},
		dlq,
		dlq,
		5,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	raw, _ := json.Marshal(service.Envelope{
		EventID:   "evt-ooo-ship-1",
		EventType: "shipment.updated",
		Payload:   json.RawMessage(`{"order_id":"ord-missing","status":"delivering"}`),
	})

	for attempt := uint64(1); attempt <= 4; attempt++ {
		msg := &fakeMsg{subject: "shipment.updated", data: raw, delivered: attempt}
		c.HandleMessage(context.Background(), msg)
		if !msg.naked || msg.acked {
			t.Fatalf("attempt %d: out-of-order shipment.updated must NakWithDelay, acked=%v naked=%v", attempt, msg.acked, msg.naked)
		}
		if len(dlq.records) != 0 {
			t.Fatalf("attempt %d: must not DLQ before maxAttempts", attempt)
		}
	}

	finalMsg := &fakeMsg{subject: "shipment.updated", data: raw, delivered: 5}
	c.HandleMessage(context.Background(), finalMsg)
	if !finalMsg.acked || len(dlq.records) != 1 || len(dlq.pubSubj) != 1 || dlq.pubSubj[0] != "dlq.shipment.updated" {
		t.Fatalf("attempt 5 must route to DLQ and Ack: acked=%v records=%+v pub=%+v", finalMsg.acked, dlq.records, dlq.pubSubj)
	}
}

func TestConsumerRedactsResetTokenInDLQAndLogs(t *testing.T) {
	var logBuf bytes.Buffer
	dlq := &stubDLQ{}
	c := NewTestableConsumer(
		stubProcessor{fn: func(_ context.Context, _ string, _ service.Envelope) (bool, error) {
			return false, errors.New("smtp connection refused")
		}},
		dlq,
		dlq,
		2,
		slog.New(slog.NewJSONHandler(&logBuf, nil)),
	)

	secretToken := "super-secret-reset-token-abc123xyz"
	raw, _ := json.Marshal(service.Envelope{
		EventID:   "evt-reset-dlq-1",
		EventType: "user.password_reset_requested",
		Payload:   json.RawMessage(`{"user_id":"u-1","email":"u1@nexusmart.local","full_name":"User One","reset_token":"` + secretToken + `","expires_at":"2026-11-23T10:30:00Z"}`),
	})

	msg := &fakeMsg{subject: "user.password_reset_requested", data: raw, delivered: 2}
	c.HandleMessage(context.Background(), msg)

	if len(dlq.records) != 1 || len(dlq.pubData) != 1 {
		t.Fatalf("expected DLQ record and publish, got records=%d pub=%d", len(dlq.records), len(dlq.pubData))
	}
	if strings.Contains(string(dlq.records[0].payload), secretToken) {
		t.Fatalf("SECURITY: reset_token leaked into dead_letter_events payload: %s", string(dlq.records[0].payload))
	}
	if strings.Contains(string(dlq.pubData[0]), secretToken) {
		t.Fatalf("SECURITY: reset_token leaked into NEXUSMART_DLQ payload: %s", string(dlq.pubData[0]))
	}
	if strings.Contains(logBuf.String(), secretToken) {
		t.Fatalf("SECURITY: reset_token leaked into consumer logs: %s", logBuf.String())
	}
}

func TestConsumerRedactsShopBankAccountOTPInDLQAndLogs(t *testing.T) {
	var logBuf bytes.Buffer
	dlq := &stubDLQ{}
	c := NewTestableConsumer(
		stubProcessor{fn: func(_ context.Context, _ string, _ service.Envelope) (bool, error) {
			return false, errors.New("smtp connection refused")
		}},
		dlq,
		dlq,
		2,
		slog.New(slog.NewJSONHandler(&logBuf, nil)),
	)

	secretOTP := "837462"
	raw, _ := json.Marshal(service.Envelope{
		EventID:   "evt-bank-otp-dlq-1",
		EventType: "user.shop_bank_account_otp",
		Payload: json.RawMessage(
			`{"user_id":"seller-1","shop_id":"shop-1","email":"s1@nexusmart.local","full_name":"Chủ Shop","otp":"` + secretOTP + `","expires_at":"2026-11-23T11:00:00Z","bank_code":"VCB","account_number_masked":"******4321"}`,
		),
	})

	// 1. Valid JSON routed to DLQ after max attempts must redact OTP everywhere.
	msg := &fakeMsg{subject: "user.shop_bank_account_otp", data: raw, delivered: 2}
	c.HandleMessage(context.Background(), msg)

	if !msg.acked || len(dlq.records) != 1 || len(dlq.pubData) != 1 {
		t.Fatalf("expected Ack and 1 DLQ record/publish, acked=%v records=%d pub=%d", msg.acked, len(dlq.records), len(dlq.pubData))
	}
	if strings.Contains(string(dlq.records[0].payload), secretOTP) {
		t.Fatalf("SECURITY: bank OTP leaked into dead_letter_events payload: %s", string(dlq.records[0].payload))
	}
	if strings.Contains(string(dlq.pubData[0]), secretOTP) {
		t.Fatalf("SECURITY: bank OTP leaked into NEXUSMART_DLQ payload: %s", string(dlq.pubData[0]))
	}
	if strings.Contains(logBuf.String(), secretOTP) {
		t.Fatalf("SECURITY: bank OTP leaked into consumer logs: %s", logBuf.String())
	}

	// 2. Malformed JSON on user.shop_bank_account_otp containing OTP must ALSO be completely redacted in DLQ.
	malformedOTP := "651920"
	badMsg := &fakeMsg{
		subject:   "user.shop_bank_account_otp",
		data:      []byte(`{"event_id":"evt-bad","payload":{"otp":"` + malformedOTP + `" broken`),
		delivered: 1,
	}
	c.HandleMessage(context.Background(), badMsg)

	if !badMsg.acked || len(dlq.records) != 2 || len(dlq.pubData) != 2 {
		t.Fatalf("expected malformed bank OTP message Acked and DLQ'd, acked=%v records=%d pub=%d", badMsg.acked, len(dlq.records), len(dlq.pubData))
	}
	if strings.Contains(string(dlq.records[1].payload), malformedOTP) {
		t.Fatalf("SECURITY: malformed bank OTP leaked into dead_letter_events payload: %s", string(dlq.records[1].payload))
	}
	if strings.Contains(string(dlq.pubData[1]), malformedOTP) {
		t.Fatalf("SECURITY: malformed bank OTP leaked into NEXUSMART_DLQ payload: %s", string(dlq.pubData[1]))
	}
	if strings.Contains(logBuf.String(), malformedOTP) {
		t.Fatalf("SECURITY: malformed bank OTP leaked into consumer logs: %s", logBuf.String())
	}
}
