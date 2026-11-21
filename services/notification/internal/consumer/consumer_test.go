package consumer

import (
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
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
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
	mu          sync.Mutex
	records     []dlqRecord
	pubSubj     []string
	pubData     [][]byte
	pubTraceIDs []string
	pubSpanIDs  []string
	pubHeaders  []map[string]string
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

func (d *stubDLQ) PublishDLQ(ctx context.Context, subject string, data []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pubSubj = append(d.pubSubj, subject)
	d.pubData = append(d.pubData, data)
	sc := trace.SpanFromContext(ctx).SpanContext()
	d.pubTraceIDs = append(d.pubTraceIDs, sc.TraceID().String())
	d.pubSpanIDs = append(d.pubSpanIDs, sc.SpanID().String())
	natsMsg := newDLQMessage(ctx, subject, data)
	hdrs := map[string]string{}
	for k, vals := range natsMsg.Header {
		if len(vals) > 0 {
			hdrs[strings.ToLower(k)] = vals[0]
		}
	}
	d.pubHeaders = append(d.pubHeaders, hdrs)
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

func TestConsumerOpensConsumerSpanPropagatesDLQTraceAndRedactsOTP(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prevTP := otel.GetTracerProvider()
	prevProp := otel.GetTextMapPropagator()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	})

	const parentTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	const parentSpanID = "00f067aa0ba902b7"
	const incomingTP = "00-" + parentTraceID + "-" + parentSpanID + "-01"
	const incomingTS = "vendor=nexusmart"

	dlq := &stubDLQ{}
	var procSpanCtx trace.SpanContext
	c := NewTestableConsumer(
		stubProcessor{fn: func(ctx context.Context, _ string, _ service.Envelope) (bool, error) {
			procSpanCtx = trace.SpanFromContext(ctx).SpanContext()
			return false, errors.New("smtp RCPT TO <secret.user@nexusmart.vn> failed")
		}},
		dlq,
		dlq,
		5,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	raw, _ := json.Marshal(service.Envelope{
		EventID:   "evt-reg-otp-1",
		EventType: "user.registered",
		Payload:   json.RawMessage(`{"user_id":"user-123","email":"secret.user@nexusmart.vn","otp":"482910","full_name":"Nguyen Van Secret"}`),
	})
	msg := &fakeMsg{
		subject: "user.registered",
		data:    raw,
		headers: map[string]string{
			"traceparent": incomingTP,
			"tracestate":  incomingTS,
		},
		delivered: 5,
	}
	c.HandleMessage(context.Background(), msg)

	// 1. Consumer opened a SpanKindConsumer child span under the incoming traceparent.
	if !procSpanCtx.IsValid() {
		t.Fatal("expected valid consumer span context inside ProcessEvent")
	}
	if procSpanCtx.TraceID().String() != parentTraceID {
		t.Fatalf("consumer trace_id = %s, want %s", procSpanCtx.TraceID(), parentTraceID)
	}
	if procSpanCtx.SpanID().String() == parentSpanID {
		t.Fatalf("expected new child span ID, got parent span ID %s", parentSpanID)
	}
	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected recorded consumer span")
	}
	lastSpan := spans[len(spans)-1]
	if lastSpan.SpanKind != trace.SpanKindConsumer {
		t.Fatalf("span kind = %v, want SpanKindConsumer", lastSpan.SpanKind)
	}
	if lastSpan.Parent.SpanID().String() != parentSpanID {
		t.Fatalf("parent span ID = %s, want %s", lastSpan.Parent.SpanID(), parentSpanID)
	}

	// 2. DLQ NATS message carries W3C traceparent pointing to consumer span + tracestate.
	if len(dlq.pubHeaders) != 1 {
		t.Fatalf("expected 1 DLQ publish, got %d", len(dlq.pubHeaders))
	}
	gotDLQTP := dlq.pubHeaders[0]["traceparent"]
	wantDLQTP := "00-" + parentTraceID + "-" + procSpanCtx.SpanID().String() + "-01"
	if gotDLQTP != wantDLQTP {
		t.Fatalf("DLQ traceparent = %q, want %q", gotDLQTP, wantDLQTP)
	}
	if dlq.pubHeaders[0]["tracestate"] != incomingTS {
		t.Fatalf("DLQ tracestate = %q, want %q", dlq.pubHeaders[0]["tracestate"], incomingTS)
	}

	// 3. Neither dead_letter_events nor NEXUSMART_DLQ contains plaintext OTP, email, or full_name.
	if len(dlq.records) != 1 || len(dlq.pubData) != 1 {
		t.Fatalf("expected 1 DB record and 1 DLQ publish, got records=%d pub=%d", len(dlq.records), len(dlq.pubData))
	}
	dbPayload := string(dlq.records[0].payload)
	dlqMsg := string(dlq.pubData[0])
	for _, target := range []string{dbPayload, dlqMsg, dlq.records[0].errMsg} {
		for _, secret := range []string{"482910", "secret.user@nexusmart.vn", "Nguyen Van Secret"} {
			if strings.Contains(target, secret) {
				t.Fatalf("DLQ output leaked %q: %s", secret, target)
			}
		}
	}
	if !strings.Contains(dbPayload, `"user-123"`) || !strings.Contains(dbPayload, `"[REDACTED]"`) {
		t.Fatalf("expected user_id preserved and otp redacted in DB payload: %s", dbPayload)
	}

	// 4. Malformed non-JSON poison payload containing raw otp field is also redacted.
	malformed := sanitizeDLQPayload([]byte(`{"event_id":"e2","otp":"999888","email":"bad@example.com"`))
	if strings.Contains(string(malformed), "999888") || strings.Contains(string(malformed), "bad@example.com") {
		t.Fatalf("malformed DLQ payload leaked sensitive data: %s", string(malformed))
	}
}
