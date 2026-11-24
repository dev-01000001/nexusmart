package events

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nexusmart/core/services/user/internal/db"
)

type fakeOutboxStore struct {
	mu      sync.Mutex
	batches [][]db.OutboxEvent
	err     error
	calls   int
}

func (f *fakeOutboxStore) ProcessPendingOutbox(ctx context.Context, _ int32, publishFn func(ctx context.Context, ev db.OutboxEvent) error) (int, error) {
	f.mu.Lock()
	f.calls++
	if f.err != nil {
		err := f.err
		f.mu.Unlock()
		return 0, err
	}
	if len(f.batches) == 0 {
		f.mu.Unlock()
		return 0, nil
	}
	batch := f.batches[0]
	f.batches = f.batches[1:]
	f.mu.Unlock()

	published := 0
	for _, ev := range batch {
		if err := publishFn(ctx, ev); err != nil {
			return published, err
		}
		published++
	}
	return published, nil
}

type fakeJSPublisher struct {
	mu   sync.Mutex
	msgs []*nats.Msg
	err  error
}

func (f *fakeJSPublisher) PublishMsg(m *nats.Msg, _ ...nats.PubOpt) (*nats.PubAck, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	hdrCopy := nats.Header{}
	for k, vals := range m.Header {
		hdrCopy[k] = append([]string(nil), vals...)
	}
	cp := &nats.Msg{
		Subject: m.Subject,
		Data:    append([]byte(nil), m.Data...),
		Header:  hdrCopy,
	}
	f.msgs = append(f.msgs, cp)
	return &nats.PubAck{Stream: "NEXUSMART", Sequence: uint64(len(f.msgs))}, nil
}

func TestShopStatusChangedPayloadJSONContract(t *testing.T) {
	occurred := "2026-11-24T15:30:00Z"
	payload := ShopStatusChangedPayload{
		ShopID:      "shop-1",
		OwnerUserID: "owner-1",
		ShopName:    "Cua Hang A",
		FromStatus:  "ACTIVE",
		ToStatus:    "SUSPENDED",
		Reason:      "vi pham chinh sach",
		ActorID:     "maker-1",
		ApproverID:  "checker-2",
		Version:     2,
		OccurredAt:  occurred,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if _, forbidden := decoded["status_version"]; forbidden {
		t.Fatalf("payload must use JSON key \"version\", not \"status_version\": %s", string(raw))
	}
	wantKeys := []string{
		"shop_id", "owner_user_id", "shop_name", "from_status", "to_status",
		"reason", "actor_id", "approver_id", "version", "occurred_at",
	}
	if len(decoded) != len(wantKeys) {
		t.Fatalf("field count = %d, want %d: %s", len(decoded), len(wantKeys), string(raw))
	}
	for _, k := range wantKeys {
		if _, ok := decoded[k]; !ok {
			t.Fatalf("missing contract key %q in payload: %s", k, string(raw))
		}
	}
	if v, ok := decoded["version"].(float64); !ok || int64(v) != 2 {
		t.Fatalf("version = %#v; want integer 2", decoded["version"])
	}
	if gotTS, ok := decoded["occurred_at"].(string); !ok || gotTS != occurred {
		t.Fatalf("occurred_at = %#v; want %q", decoded["occurred_at"], occurred)
	}
	if _, err := time.Parse(time.RFC3339, occurred); err != nil {
		t.Fatalf("occurred_at not RFC3339: %v", err)
	}
}

func TestOutboxRelay_PublishesCanonicalEnvelopeAndHeaders(t *testing.T) {
	now := time.Date(2026, 11, 24, 15, 30, 0, 0, time.UTC)
	payload, err := json.Marshal(ShopStatusChangedPayload{
		ShopID:      "shop-1",
		OwnerUserID: "owner-1",
		ShopName:    "Cua Hang A",
		FromStatus:  "ACTIVE",
		ToStatus:    "SUSPENDED",
		Reason:      "vi pham chinh sach",
		ActorID:     "maker-1",
		ApproverID:  "checker-2",
		Version:     2,
		OccurredAt:  now.Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	store := &fakeOutboxStore{
		batches: [][]db.OutboxEvent{
			{
				{
					ID:          "evt-1",
					Seq:         1,
					AggregateID: "shop-1",
					EventType:   SubjectShopStatusChanged,
					TraceID:     "trace-123",
					Traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
					PayloadJSON: payload,
					CreatedAt:   now,
				},
			},
		},
	}
	pub := &fakeJSPublisher{}
	relay := NewOutboxRelay(store, pub, nil, 10*time.Millisecond, nil)

	n := relay.FlushOnce(context.Background())
	if n != 1 {
		t.Fatalf("FlushOnce = %d; want 1", n)
	}
	if len(pub.msgs) != 1 {
		t.Fatalf("published msg count = %d; want 1", len(pub.msgs))
	}

	msg := pub.msgs[0]
	if msg.Subject != SubjectShopStatusChanged {
		t.Fatalf("subject = %q; want %q", msg.Subject, SubjectShopStatusChanged)
	}
	if msg.Header.Get(nats.MsgIdHdr) != "evt-1" {
		t.Fatalf("Nats-Msg-Id = %q; want evt-1", msg.Header.Get(nats.MsgIdHdr))
	}
	if msg.Header.Get("traceparent") != "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01" {
		t.Fatalf("traceparent mismatch: %q", msg.Header.Get("traceparent"))
	}

	var decoded struct {
		EventID    string                   `json:"event_id"`
		EventType  string                   `json:"event_type"`
		TraceID    string                   `json:"trace_id"`
		OccurredAt string                   `json:"occurred_at"`
		Payload    ShopStatusChangedPayload `json:"payload"`
	}
	if err := json.Unmarshal(msg.Data, &decoded); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if decoded.EventID != "evt-1" || decoded.EventType != SubjectShopStatusChanged || decoded.TraceID != "trace-123" {
		t.Fatalf("unexpected envelope header fields: %+v", decoded)
	}
	if decoded.OccurredAt != now.Format(time.RFC3339) {
		t.Fatalf("occurred_at = %q; want %q", decoded.OccurredAt, now.Format(time.RFC3339))
	}
	if decoded.Payload.ShopID != "shop-1" || decoded.Payload.Version != 2 || decoded.Payload.ToStatus != "SUSPENDED" || decoded.Payload.OccurredAt != now.Format(time.RFC3339) {
		t.Fatalf("unexpected payload: %+v", decoded.Payload)
	}
}

func TestOutboxRelay_RunReactsToTriggerAndStopsOnCancel(t *testing.T) {
	trigger := make(chan struct{}, 1)
	store := &fakeOutboxStore{
		batches: [][]db.OutboxEvent{
			{
				{
					ID:          "evt-trig",
					Seq:         1,
					AggregateID: "shop-2",
					EventType:   SubjectShopStatusChanged,
					PayloadJSON: []byte(`{"shop_id":"shop-2","status_version":2}`),
					CreatedAt:   time.Now().UTC(),
				},
			},
		},
	}
	pub := &fakeJSPublisher{}
	relay := NewOutboxRelay(store, pub, nil, time.Hour, trigger)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		relay.Run(ctx)
	}()

	trigger <- struct{}{}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		pub.mu.Lock()
		got := len(pub.msgs)
		pub.mu.Unlock()
		if got == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("OutboxRelay.Run did not stop after ctx cancellation")
	}

	pub.mu.Lock()
	defer pub.mu.Unlock()
	if len(pub.msgs) != 1 || pub.msgs[0].Header.Get(nats.MsgIdHdr) != "evt-trig" {
		t.Fatalf("expected 1 triggered message evt-trig, got %d", len(pub.msgs))
	}
}

func TestOutboxRelay_FlushReturnsErrorWhenPublishFails(t *testing.T) {
	store := &fakeOutboxStore{
		batches: [][]db.OutboxEvent{
			{
				{
					ID:          "evt-fail",
					Seq:         1,
					AggregateID: "shop-3",
					EventType:   SubjectShopStatusChanged,
					PayloadJSON: []byte(`{"shop_id":"shop-3"}`),
					CreatedAt:   time.Now().UTC(),
				},
			},
		},
	}
	pub := &fakeJSPublisher{err: errors.New("nats unavailable")}
	relay := NewOutboxRelay(store, pub, nil, 10*time.Millisecond, nil)

	n, err := relay.flushOnceErr(context.Background())
	if err == nil || n != 0 {
		t.Fatalf("flushOnceErr = (%d, %v); want (0, error)", n, err)
	}
}
