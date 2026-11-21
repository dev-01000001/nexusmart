package events

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/product/internal/store"
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
	cp.Data = append([]byte(nil), m.Data...)
	cp.Header = nats.Header{}
	for k, vals := range m.Header {
		cp.Header[k] = append([]string(nil), vals...)
	}
	p.msgs = append(p.msgs, &cp)
	return &nats.PubAck{Stream: DefaultStreamName, Sequence: uint64(len(p.msgs))}, nil
}

func (p *recordingPublisher) setErr(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.err = err
}

func (p *recordingPublisher) messages() []*nats.Msg {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]*nats.Msg, len(p.msgs))
	copy(out, p.msgs)
	return out
}

type fakeJetStream struct {
	nats.JetStreamContext

	mu             sync.Mutex
	streams        map[string]*nats.StreamInfo
	addCalls       int
	updateCalls    int
	raceOnFirstAdd bool
}

func newFakeJetStream() *fakeJetStream {
	return &fakeJetStream{
		streams: make(map[string]*nats.StreamInfo),
	}
}

func (f *fakeJetStream) StreamInfo(name string, _ ...nats.JSOpt) (*nats.StreamInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	info, ok := f.streams[name]
	if !ok {
		return nil, nats.ErrStreamNotFound
	}
	cp := *info
	cp.Config.Subjects = append([]string(nil), info.Config.Subjects...)
	return &cp, nil
}

func (f *fakeJetStream) AddStream(cfg *nats.StreamConfig, _ ...nats.JSOpt) (*nats.StreamInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addCalls++
	if f.raceOnFirstAdd && cfg.Name == DefaultStreamName {
		f.raceOnFirstAdd = false
		// Simulate another service (e.g. order) creating NEXUSMART without "product.>" concurrently.
		f.streams[cfg.Name] = &nats.StreamInfo{
			Config: nats.StreamConfig{
				Name:       cfg.Name,
				Subjects:   []string{"user.>", "order.>", "payment.>", "shipment.>"},
				Retention:  nats.LimitsPolicy,
				Storage:    nats.FileStorage,
				Duplicates: 2 * time.Minute,
			},
		}
		return nil, errors.New("stream name already in use")
	}
	if _, exists := f.streams[cfg.Name]; exists {
		return nil, errors.New("stream name already in use")
	}
	cp := *cfg
	cp.Subjects = append([]string(nil), cfg.Subjects...)
	info := &nats.StreamInfo{Config: cp}
	f.streams[cfg.Name] = info
	return info, nil
}

func (f *fakeJetStream) UpdateStream(cfg *nats.StreamConfig, _ ...nats.JSOpt) (*nats.StreamInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updateCalls++
	if _, exists := f.streams[cfg.Name]; !exists {
		return nil, nats.ErrStreamNotFound
	}
	cp := *cfg
	cp.Subjects = append([]string(nil), cfg.Subjects...)
	info := &nats.StreamInfo{Config: cp}
	f.streams[cfg.Name] = info
	return info, nil
}

func TestOutboxRelay_FlushOnce_CanonicalEnvelopeHeadersAndRetry(t *testing.T) {
	st := store.NewMemoryStore()
	occurred := time.Date(2026, 11, 21, 8, 30, 0, 0, time.UTC)
	payload, err := json.Marshal(ProductEventPayload{
		ProductID:  "prod-1",
		ShopID:     "shop-1",
		Version:    42,
		OccurredAt: occurred.Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	ev := store.OutboxEvent{
		ID:          "evt-prod-001",
		EventType:   SubjectProductCreated,
		TraceID:     "4bf92f3577b34da6a3ce929d0e0e4736",
		Traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		PayloadJSON: payload,
		CreatedAt:   occurred,
	}
	if err := st.InsertOutboxEvent(context.Background(), ev); err != nil {
		t.Fatalf("insert outbox event: %v", err)
	}

	pub := &recordingPublisher{err: errors.New("nats temporarily unavailable")}
	log := logger.New("product-test", logger.WithWriter(io.Discard))
	relay := NewOutboxRelay(st, pub, log, 50*time.Millisecond, nil)

	// 1. Publish failure must record attempt + last_error without marking published.
	if n := relay.FlushOnce(context.Background()); n != 0 {
		t.Fatalf("expected 0 published on error, got %d", n)
	}
	rows := st.OutboxEvents()
	if len(rows) != 1 {
		t.Fatalf("expected 1 outbox row, got %d", len(rows))
	}
	if rows[0].PublishedAt != nil {
		t.Fatalf("expected PublishedAt nil after failed publish, got %v", rows[0].PublishedAt)
	}
	if rows[0].Attempts != 1 || rows[0].LastError == "" {
		t.Fatalf("expected Attempts=1 and non-empty LastError, got attempts=%d last_error=%q", rows[0].Attempts, rows[0].LastError)
	}

	// 2. Once publisher recovers, retry publishes canonical envelope with headers and marks published.
	pub.setErr(nil)
	if n := relay.FlushOnce(context.Background()); n != 1 {
		t.Fatalf("expected 1 published on retry, got %d", n)
	}
	if n := relay.FlushOnce(context.Background()); n != 0 {
		t.Fatalf("expected 0 published after already drained, got %d", n)
	}

	rows = st.OutboxEvents()
	if rows[0].PublishedAt == nil || rows[0].Attempts != 2 || rows[0].LastError != "" {
		t.Fatalf("expected published row with Attempts=2 and cleared LastError, got %+v", rows[0])
	}

	msgs := pub.messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 published NATS msg, got %d", len(msgs))
	}
	msg := msgs[0]
	if msg.Subject != SubjectProductCreated {
		t.Fatalf("subject = %q, want %q", msg.Subject, SubjectProductCreated)
	}
	if got := msg.Header.Get(nats.MsgIdHdr); got != "evt-prod-001" {
		t.Fatalf("Nats-Msg-Id = %q, want evt-prod-001", got)
	}
	if got := msg.Header.Get("traceparent"); got != ev.Traceparent {
		t.Fatalf("traceparent = %q, want %q", got, ev.Traceparent)
	}

	var env Envelope
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if env.EventID != ev.ID || env.EventType != SubjectProductCreated || env.TraceID != ev.TraceID {
		t.Fatalf("unexpected envelope metadata: %+v", env)
	}
	if !env.OccurredAt.Equal(occurred) {
		t.Fatalf("envelope occurred_at = %v, want %v", env.OccurredAt, occurred)
	}

	var gotPayload ProductEventPayload
	if err := json.Unmarshal(env.Payload, &gotPayload); err != nil {
		t.Fatalf("unmarshal envelope payload: %v", err)
	}
	if gotPayload.ProductID != "prod-1" || gotPayload.ShopID != "shop-1" || gotPayload.Version != 42 || gotPayload.OccurredAt != occurred.Format(time.RFC3339Nano) {
		t.Fatalf("unexpected payload: %+v", gotPayload)
	}
}

func TestOutboxRelay_RunWakesImmediatelyOnTrigger(t *testing.T) {
	st := store.NewMemoryStore()
	pub := &recordingPublisher{}
	triggerCh := make(chan struct{}, 1)
	log := logger.New("product-test", logger.WithWriter(io.Discard))

	// Use a very long poll interval so publishing must happen via trigger channel.
	relay := NewOutboxRelay(st, pub, log, 10*time.Second, triggerCh)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		relay.Run(ctx)
	}()

	payload, _ := json.Marshal(ProductEventPayload{
		ProductID:  "prod-wake",
		ShopID:     "shop-wake",
		Version:    1,
		OccurredAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err := st.InsertOutboxEvent(context.Background(), store.OutboxEvent{
		ID:          "evt-wake-1",
		EventType:   SubjectProductUpdated,
		PayloadJSON: payload,
	}); err != nil {
		t.Fatalf("insert outbox event: %v", err)
	}

	triggerCh <- struct{}{}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(pub.messages()) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done

	if got := len(pub.messages()); got != 1 {
		t.Fatalf("expected trigger wake to publish 1 message quickly, got %d", got)
	}
}

func TestEnsureStreams_CreatesAndUpdatesExistingStream(t *testing.T) {
	t.Run("creates missing NEXUSMART with product.> and DLQ", func(t *testing.T) {
		js := newFakeJetStream()
		if err := EnsureStreams(js, ""); err != nil {
			t.Fatalf("EnsureStreams failed: %v", err)
		}
		info, err := js.StreamInfo(DefaultStreamName)
		if err != nil {
			t.Fatalf("expected NEXUSMART stream created: %v", err)
		}
		for _, want := range []string{"user.>", "order.>", "payment.>", "shipment.>", "product.>"} {
			if !slices.Contains(info.Config.Subjects, want) {
				t.Fatalf("NEXUSMART missing subject %q: %v", want, info.Config.Subjects)
			}
		}
		dlq, err := js.StreamInfo(DLQStreamName)
		if err != nil {
			t.Fatalf("expected NEXUSMART_DLQ stream created: %v", err)
		}
		if !slices.Contains(dlq.Config.Subjects, "dlq.>") {
			t.Fatalf("NEXUSMART_DLQ missing dlq.>: %v", dlq.Config.Subjects)
		}
		if js.updateCalls != 0 {
			t.Fatalf("expected 0 UpdateStream calls on fresh creation, got %d", js.updateCalls)
		}
	})

	t.Run("appends product.> to existing NEXUSMART lacking it", func(t *testing.T) {
		js := newFakeJetStream()
		js.streams[DefaultStreamName] = &nats.StreamInfo{
			Config: nats.StreamConfig{
				Name:       DefaultStreamName,
				Subjects:   []string{"user.>", "order.>", "payment.>", "shipment.>"},
				Retention:  nats.LimitsPolicy,
				Storage:    nats.FileStorage,
				Duplicates: 2 * time.Minute,
			},
		}
		js.streams[DLQStreamName] = &nats.StreamInfo{
			Config: nats.StreamConfig{
				Name:      DLQStreamName,
				Subjects:  []string{"dlq.>"},
				Retention: nats.LimitsPolicy,
				Storage:   nats.FileStorage,
			},
		}

		if err := EnsureStreams(js, DefaultStreamName); err != nil {
			t.Fatalf("EnsureStreams failed: %v", err)
		}
		if js.updateCalls != 1 {
			t.Fatalf("expected 1 UpdateStream call, got %d", js.updateCalls)
		}
		info, _ := js.StreamInfo(DefaultStreamName)
		wantSubjects := []string{"user.>", "order.>", "payment.>", "shipment.>", "product.>"}
		if !slices.Equal(info.Config.Subjects, wantSubjects) {
			t.Fatalf("updated subjects = %v, want %v", info.Config.Subjects, wantSubjects)
		}

		// Second call should be a clean no-op without another UpdateStream.
		if err := EnsureStreams(js, DefaultStreamName); err != nil {
			t.Fatalf("second EnsureStreams failed: %v", err)
		}
		if js.updateCalls != 1 {
			t.Fatalf("expected idempotent 2nd call to not call UpdateStream, got %d calls", js.updateCalls)
		}
	})

	t.Run("handles concurrent stream creation race without product.>", func(t *testing.T) {
		js := newFakeJetStream()
		js.raceOnFirstAdd = true
		if err := EnsureStreams(js, DefaultStreamName); err != nil {
			t.Fatalf("EnsureStreams failed on race: %v", err)
		}
		info, err := js.StreamInfo(DefaultStreamName)
		if err != nil {
			t.Fatalf("StreamInfo after race: %v", err)
		}
		if !slices.Contains(info.Config.Subjects, "product.>") {
			t.Fatalf("expected product.> added after concurrent AddStream race, got %v", info.Config.Subjects)
		}
		if js.updateCalls != 1 {
			t.Fatalf("expected 1 UpdateStream call to patch raced stream, got %d", js.updateCalls)
		}
	})
}
