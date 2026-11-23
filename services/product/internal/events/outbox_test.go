package events

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
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
	cp.Header = nats.Header{}
	for k, vals := range m.Header {
		cp.Header[k] = append([]string(nil), vals...)
	}
	cp.Data = append([]byte(nil), m.Data...)
	p.msgs = append(p.msgs, &cp)
	return &nats.PubAck{Stream: "NEXUSMART", Sequence: uint64(len(p.msgs))}, nil
}

type fakeStreamManager struct {
	streams      map[string]*nats.StreamInfo
	updateCalled int
	addCalled    int
}

func (f *fakeStreamManager) StreamInfo(stream string, _ ...nats.JSOpt) (*nats.StreamInfo, error) {
	if info, ok := f.streams[stream]; ok {
		cp := *info
		cp.Config.Subjects = append([]string(nil), info.Config.Subjects...)
		return &cp, nil
	}
	return nil, nats.ErrStreamNotFound
}

func (f *fakeStreamManager) AddStream(cfg *nats.StreamConfig, _ ...nats.JSOpt) (*nats.StreamInfo, error) {
	f.addCalled++
	cp := *cfg
	cp.Subjects = append([]string(nil), cfg.Subjects...)
	info := &nats.StreamInfo{Config: cp}
	f.streams[cfg.Name] = info
	return info, nil
}

func (f *fakeStreamManager) UpdateStream(cfg *nats.StreamConfig, _ ...nats.JSOpt) (*nats.StreamInfo, error) {
	f.updateCalled++
	cp := *cfg
	cp.Subjects = append([]string(nil), cfg.Subjects...)
	info := &nats.StreamInfo{Config: cp}
	f.streams[cfg.Name] = info
	return info, nil
}

func TestOutboxRelay_PublishesCanonicalEnvelopeAndRetriesFailures(t *testing.T) {
	st := store.NewMemoryStore()
	now := time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC)
	payloadBytes, err := json.Marshal(ProductReviewedPayload{
		ProductID:   "prod-1",
		ProductName: "Áo Thun",
		ShopID:      "shop-1",
		Decision:    "APPROVE",
		Reason:      "",
		OccurredAt:  now.Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if err := st.InsertOutboxEvent(context.Background(), store.OutboxEvent{
		ID:          "evt-prod-rev-1",
		EventType:   SubjectProductReviewed,
		TraceID:     "4bf92f3577b34da6a3ce929d0e0e4736",
		Traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		PayloadJSON: payloadBytes,
		CreatedAt:   now,
	}); err != nil {
		t.Fatalf("InsertOutboxEvent: %v", err)
	}

	pub := &recordingPublisher{err: errors.New("nats temporarily down")}
	relay := NewOutboxRelay(st, pub, nil, 50*time.Millisecond, nil)

	// 1. Transient failure keeps event unpublished and increments attempts.
	if n := relay.FlushOnce(context.Background()); n != 0 {
		t.Fatalf("expected 0 published on transient error, got %d", n)
	}
	evs := st.OutboxEvents()
	if len(evs) != 1 || evs[0].PublishedAt != nil || evs[0].Attempts != 1 || evs[0].LastError == "" {
		t.Fatalf("expected failed attempt recorded without published_at, got %+v", evs)
	}

	// 2. Retry succeeds and publishes canonical JSON envelope + headers.
	pub.err = nil
	if n := relay.FlushOnce(context.Background()); n != 1 {
		t.Fatalf("expected 1 published on retry, got %d", n)
	}
	if len(pub.msgs) != 1 {
		t.Fatalf("expected 1 published NATS message, got %d", len(pub.msgs))
	}
	msg := pub.msgs[0]
	if msg.Subject != SubjectProductReviewed {
		t.Fatalf("subject = %q, want %q", msg.Subject, SubjectProductReviewed)
	}
	if msg.Header.Get(nats.MsgIdHdr) != "evt-prod-rev-1" {
		t.Fatalf("Nats-Msg-Id = %q, want evt-prod-rev-1", msg.Header.Get(nats.MsgIdHdr))
	}
	if msg.Header.Get("traceparent") != "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01" {
		t.Fatalf("traceparent = %q", msg.Header.Get("traceparent"))
	}

	var env Envelope
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if env.EventID != "evt-prod-rev-1" || env.EventType != SubjectProductReviewed || env.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("unexpected envelope: %+v", env)
	}
	var payload ProductReviewedPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.ProductID != "prod-1" || payload.ShopID != "shop-1" || payload.Decision != "APPROVE" {
		t.Fatalf("unexpected payload: %+v", payload)
	}

	// 3. Subsequent flush is a no-op.
	if n := relay.FlushOnce(context.Background()); n != 0 {
		t.Fatalf("expected 0 published after drain, got %d", n)
	}
}

func TestEnsureStreams_UnionMergesMissingProductSubject(t *testing.T) {
	mgr := &fakeStreamManager{
		streams: map[string]*nats.StreamInfo{
			"NEXUSMART": {
				Config: nats.StreamConfig{
					Name:     "NEXUSMART",
					Subjects: []string{"user.>", "order.>", "payment.>", "shipment.>", "custom.>"},
				},
			},
		},
	}

	if err := EnsureStreams(mgr, "NEXUSMART"); err != nil {
		t.Fatalf("EnsureStreams: %v", err)
	}
	if mgr.updateCalled != 1 {
		t.Fatalf("expected UpdateStream called once to merge missing subjects, got %d", mgr.updateCalled)
	}
	gotSubjects := mgr.streams["NEXUSMART"].Config.Subjects
	want := []string{"user.>", "order.>", "payment.>", "shipment.>", "custom.>", "product.>", "promotion.>"}
	if len(gotSubjects) != len(want) {
		t.Fatalf("merged subjects = %v, want %v", gotSubjects, want)
	}
	for i := range want {
		if gotSubjects[i] != want[i] {
			t.Fatalf("subject[%d] = %q, want %q (all=%v)", i, gotSubjects[i], want[i], gotSubjects)
		}
	}
	if _, ok := mgr.streams[DLQStreamName]; !ok {
		t.Fatalf("expected DLQ stream %s to be created", DLQStreamName)
	}
}
