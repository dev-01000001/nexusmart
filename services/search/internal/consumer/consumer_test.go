package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/nexusmart/core/services/search/internal/indexer"
	"github.com/nexusmart/core/services/search/internal/store"
)

type fakeMsg struct {
	subject   string
	data      []byte
	headers   map[string]string
	delivered uint64
	acked     bool
	nakDelay  time.Duration
}

func (m *fakeMsg) Subject() string          { return m.subject }
func (m *fakeMsg) Data() []byte             { return m.data }
func (m *fakeMsg) Header(key string) string { return m.headers[key] }
func (m *fakeMsg) NumDelivered() uint64     { return m.delivered }
func (m *fakeMsg) Ack() error               { m.acked = true; return nil }
func (m *fakeMsg) NakWithDelay(d time.Duration) error {
	m.nakDelay = d
	return nil
}

type fakeProcessor struct {
	fn func(ctx context.Context, subject string, env indexer.Envelope) (bool, error)
}

func (p fakeProcessor) ProcessEvent(ctx context.Context, subject string, env indexer.Envelope) (bool, error) {
	return p.fn(ctx, subject, env)
}

type fakeDLQPub struct {
	subjects []string
	payloads [][]byte
}

func (p *fakeDLQPub) PublishDLQ(subject string, data []byte) error {
	p.subjects = append(p.subjects, subject)
	p.payloads = append(p.payloads, data)
	return nil
}

type fakeStreamMgr struct {
	streams map[string]*nats.StreamConfig
	updated int
	added   int
}

func (m *fakeStreamMgr) StreamInfo(name string, _ ...nats.JSOpt) (*nats.StreamInfo, error) {
	cfg, ok := m.streams[name]
	if !ok {
		return nil, nats.ErrStreamNotFound
	}
	cp := *cfg
	cp.Subjects = append([]string(nil), cfg.Subjects...)
	return &nats.StreamInfo{Config: cp}, nil
}

func (m *fakeStreamMgr) AddStream(cfg *nats.StreamConfig, _ ...nats.JSOpt) (*nats.StreamInfo, error) {
	m.added++
	cp := *cfg
	cp.Subjects = append([]string(nil), cfg.Subjects...)
	m.streams[cfg.Name] = &cp
	return &nats.StreamInfo{Config: cp}, nil
}

func (m *fakeStreamMgr) UpdateStream(cfg *nats.StreamConfig, _ ...nats.JSOpt) (*nats.StreamInfo, error) {
	m.updated++
	cp := *cfg
	cp.Subjects = append([]string(nil), cfg.Subjects...)
	m.streams[cfg.Name] = &cp
	return &nats.StreamInfo{Config: cp}, nil
}

func TestEnsureStreamsUpdatesExistingPrimaryWithoutProductSubject(t *testing.T) {
	mgr := &fakeStreamMgr{
		streams: map[string]*nats.StreamConfig{
			"NEXUSMART": {
				Name:     "NEXUSMART",
				Subjects: []string{"user.>", "order.>", "payment.>", "shipment.>"},
			},
		},
	}

	if err := EnsureStreams(mgr, "NEXUSMART", "NEXUSMART_DLQ"); err != nil {
		t.Fatalf("EnsureStreams: %v", err)
	}
	if mgr.updated != 1 {
		t.Fatalf("expected UpdateStream called once, got %d", mgr.updated)
	}
	if !containsSubject(mgr.streams["NEXUSMART"].Subjects, "product.>") {
		t.Fatalf("expected product.> added to NEXUSMART, got %v", mgr.streams["NEXUSMART"].Subjects)
	}
	if _, ok := mgr.streams["NEXUSMART_DLQ"]; !ok {
		t.Fatal("expected NEXUSMART_DLQ to be created")
	}

	// Second call is a no-op.
	if err := EnsureStreams(mgr, "NEXUSMART", "NEXUSMART_DLQ"); err != nil {
		t.Fatalf("EnsureStreams idempotent: %v", err)
	}
	if mgr.updated != 1 {
		t.Fatalf("expected no second UpdateStream call, got %d", mgr.updated)
	}
}

func TestHandleMessageRetryAndDLQRouting(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dlqStore := store.NewMemoryStore()
	dlqPub := &fakeDLQPub{}

	transientErr := errors.New("temporary network failure")
	c := NewTestableConsumer(
		fakeProcessor{fn: func(_ context.Context, _ string, _ indexer.Envelope) (bool, error) {
			return false, transientErr
		}},
		dlqStore,
		dlqPub,
		3,
		logger,
	)

	validEnv, _ := json.Marshal(indexer.Envelope{
		EventID:   "evt-1",
		EventType: indexer.SubjectProductUpdated,
		Payload:   json.RawMessage(`{"product_id":"p1"}`),
	})

	// Attempt 1 -> NakWithDelay(1s), not DLQ.
	m1 := &fakeMsg{subject: "product.updated", data: validEnv, delivered: 1}
	c.HandleMessage(context.Background(), m1)
	if m1.acked || m1.nakDelay != time.Second {
		t.Fatalf("attempt 1: acked=%v nakDelay=%v, want false / 1s", m1.acked, m1.nakDelay)
	}
	if len(dlqStore.DeadLetters()) != 0 || len(dlqPub.subjects) != 0 {
		t.Fatal("expected no DLQ entry before maxAttempts")
	}

	// Attempt 3 (>= maxAttempts) -> DLQ + Ack.
	m3 := &fakeMsg{subject: "product.updated", data: validEnv, delivered: 3}
	c.HandleMessage(context.Background(), m3)
	if !m3.acked || m3.nakDelay != 0 {
		t.Fatalf("attempt 3: acked=%v nakDelay=%v, want true / 0", m3.acked, m3.nakDelay)
	}
	if len(dlqStore.DeadLetters()) != 1 {
		t.Fatalf("dead_letter_events count = %d, want 1", len(dlqStore.DeadLetters()))
	}
	if len(dlqPub.subjects) != 1 || dlqPub.subjects[0] != "dlq.product.updated" {
		t.Fatalf("unexpected DLQ subjects: %v", dlqPub.subjects)
	}

	// Malformed JSON -> immediate DLQ + Ack even on delivery 1.
	mBad := &fakeMsg{subject: "product.created", data: []byte(`{invalid-json`), delivered: 1}
	c.HandleMessage(context.Background(), mBad)
	if !mBad.acked {
		t.Fatal("expected malformed message to be acked after DLQ routing")
	}
	if len(dlqStore.DeadLetters()) != 2 || dlqPub.subjects[1] != "dlq.product.created" {
		t.Fatalf("expected malformed message routed to dlq.product.created, got %v", dlqPub.subjects)
	}
}
