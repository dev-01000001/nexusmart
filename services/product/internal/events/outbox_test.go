package events

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nexusmart/core/pkg/common/jetstream"
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

	pub := &recordingPublisher{err: nats.ErrDisconnected}
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
	existing := []string{"user.>", "order.>", "payment.>", "shipment.>", "custom.>"}
	mgr := &fakeStreamManager{
		streams: map[string]*nats.StreamInfo{
			"NEXUSMART": {
				Config: nats.StreamConfig{
					Name:     "NEXUSMART",
					Subjects: append([]string(nil), existing...),
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
	want, _ := jetstream.MergeSubjects(existing, jetstream.CanonicalPrimarySubjects())
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

type blockingFailPublisher struct {
	mu           sync.Mutex
	publishedIDs []string
	failIDs      map[string]error
	onPublish    func(eventID string)
}

func (p *blockingFailPublisher) PublishMsg(m *nats.Msg, _ ...nats.PubOpt) (*nats.PubAck, error) {
	id := m.Header.Get(nats.MsgIdHdr)
	if p.onPublish != nil {
		p.onPublish(id)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err, ok := p.failIDs[id]; ok && err != nil {
		return nil, err
	}
	p.publishedIDs = append(p.publishedIDs, id)
	return &nats.PubAck{Stream: "NEXUSMART", Sequence: uint64(len(p.publishedIDs))}, nil
}

func TestOutboxRelay_D9_AggregateOrderingAndConcurrentLease(t *testing.T) {
	st := store.NewMemoryStore()
	var clkMu sync.Mutex
	t0 := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	curNow := t0
	st.SetOutboxClock(func() time.Time {
		clkMu.Lock()
		defer clkMu.Unlock()
		return curNow
	})

	insertReview := func(id, productID, decision string, createdAt time.Time) {
		t.Helper()
		raw, err := json.Marshal(ProductReviewedPayload{
			ProductID:   productID,
			ProductName: "Prod " + productID,
			ShopID:      "shop-1",
			Decision:    decision,
			OccurredAt:  createdAt.Format(time.RFC3339),
		})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if err := st.InsertOutboxEvent(context.Background(), store.OutboxEvent{
			ID:          id,
			EventType:   SubjectProductReviewed,
			PayloadJSON: raw,
			CreatedAt:   createdAt,
		}); err != nil {
			t.Fatalf("InsertOutboxEvent %s: %v", id, err)
		}
	}

	// 1. Aggregate ordering: prod-A has [ev-a1, ev-a2], prod-B has [ev-b1].
	// When ev-a1 fails with non-infra error, ev-b1 (different aggregate) MUST publish, while ev-a2 (same aggregate as ev-a1)
	// MUST NOT be published out of order, and ev-a1 enters 2s backoff.
	insertReview("ev-a1", "prod-A", "REJECT", t0)
	insertReview("ev-b1", "prod-B", "APPROVE", t0.Add(time.Second))
	insertReview("ev-a2", "prod-A", "APPROVE", t0.Add(2*time.Second))

	pub := &blockingFailPublisher{
		failIDs: map[string]error{"ev-a1": errors.New("non-infra per-event error")},
	}
	relay := NewOutboxRelay(st, pub, nil, 50*time.Millisecond, nil)

	if n := relay.FlushOnce(context.Background()); n != 1 {
		t.Fatalf("first flush: want 1 published (ev-b1 only), got %d (published=%v)", n, pub.publishedIDs)
	}
	if len(pub.publishedIDs) != 1 || pub.publishedIDs[0] != "ev-b1" {
		t.Fatalf("first flush published=%v, want [ev-b1] (ev-a2 must not overtake failed ev-a1)", pub.publishedIDs)
	}

	// While ev-a1 is in 2s backoff, immediate flush must skip both ev-a1 and ev-a2.
	pub.failIDs = nil
	if n := relay.FlushOnce(context.Background()); n != 0 {
		t.Fatalf("immediate retry during 2s backoff: want 0 published, got %d (%v)", n, pub.publishedIDs)
	}

	// Advance fake clock past 2s backoff -> ev-a1 and ev-a2 publish in strict seq order.
	clkMu.Lock()
	curNow = curNow.Add(3 * time.Second)
	clkMu.Unlock()
	if n := relay.FlushOnce(context.Background()); n != 2 {
		t.Fatalf("retry flush after backoff: want 2 published, got %d (published=%v)", n, pub.publishedIDs)
	}
	wantOrder := []string{"ev-b1", "ev-a1", "ev-a2"}
	if len(pub.publishedIDs) != len(wantOrder) {
		t.Fatalf("published=%v, want %v", pub.publishedIDs, wantOrder)
	}
	for i := range wantOrder {
		if pub.publishedIDs[i] != wantOrder[i] {
			t.Fatalf("published[%d]=%q, want %q (all=%v)", i, pub.publishedIDs[i], wantOrder[i], pub.publishedIDs)
		}
	}

	// 2. Concurrent relay workers must not double-publish an in-flight leased event.
	insertReview("ev-conc-1", "prod-C", "APPROVE", t0.Add(3*time.Second))
	started := make(chan struct{})
	release := make(chan struct{})
	var startOnce sync.Once
	concPub := &blockingFailPublisher{
		onPublish: func(eventID string) {
			if eventID == "ev-conc-1" {
				startOnce.Do(func() {
					close(started)
					<-release
				})
			}
		},
	}
	relay1 := NewOutboxRelay(st, concPub, nil, 50*time.Millisecond, nil)
	relay2 := NewOutboxRelay(st, concPub, nil, 50*time.Millisecond, nil)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		relay1.FlushOnce(context.Background())
	}()
	<-started
	// While relay1 is mid-publish (outside lock/tx), relay2 MUST skip the leased event.
	if n2 := relay2.FlushOnce(context.Background()); n2 != 0 {
		close(release)
		wg.Wait()
		t.Fatalf("concurrent relay2 published %d events while relay1 held active lease (want 0)", n2)
	}
	close(release)
	wg.Wait()
	if len(concPub.publishedIDs) != 1 || concPub.publishedIDs[0] != "ev-conc-1" {
		t.Fatalf("expected ev-conc-1 published exactly once, got %v", concPub.publishedIDs)
	}
}

func TestOutboxRelay_D9_CrashLeaseExpiryStaleTokenAndCrossWorkerOrder(t *testing.T) {
	st := store.NewMemoryStore()
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	st.SetOutboxClock(func() time.Time { return now })
	st.SetOutboxLease(10 * time.Second)

	insert := func(id, aggID string, at time.Time) {
		t.Helper()
		raw, _ := json.Marshal(ProductReviewedPayload{
			ProductID: aggID, ProductName: "P " + aggID, ShopID: "shop-1", Decision: "APPROVE", OccurredAt: at.Format(time.RFC3339),
		})
		if err := st.InsertOutboxEvent(context.Background(), store.OutboxEvent{
			ID: id, EventType: SubjectProductReviewed, AggregateID: aggID, PayloadJSON: raw, CreatedAt: at,
		}); err != nil {
			t.Fatalf("InsertOutboxEvent %s: %v", id, err)
		}
	}

	insert("ev-crash-1", "prod-crash", now)
	insert("ev-crash-2", "prod-crash", now.Add(time.Second))
	insert("ev-other-1", "prod-other", now.Add(2*time.Second))

	// Worker 1 claims only ev-crash-1 (limit=1) and "crashes" without marking published.
	claimed1, staleToken, err := st.ClaimPendingOutbox(context.Background(), 1)
	if err != nil || len(claimed1) != 1 || claimed1[0].ID != "ev-crash-1" || staleToken == "" {
		t.Fatalf("worker 1 claim: got=%+v token=%q err=%v", claimed1, staleToken, err)
	}

	// While Worker 1's lease on ev-crash-1 is active (t + 5s < t + 10s), Worker 2 MUST skip ev-crash-1 AND ev-crash-2
	// (same aggregate prod-crash!), publishing only ev-other-1.
	now = now.Add(5 * time.Second)
	pub := &blockingFailPublisher{}
	relay2 := NewOutboxRelay(st, pub, nil, 50*time.Millisecond, nil)
	if n := relay2.FlushOnce(context.Background()); n != 1 {
		t.Fatalf("while ev-crash-1 leased: want 1 published (ev-other-1 only), got %d (%v)", n, pub.publishedIDs)
	}
	if len(pub.publishedIDs) != 1 || pub.publishedIDs[0] != "ev-other-1" {
		t.Fatalf("while ev-crash-1 leased: published=%v, want [ev-other-1]", pub.publishedIDs)
	}

	// Once Worker 1's lease expires (t + 11s), Worker 2 re-claims and publishes ev-crash-1 then ev-crash-2 in order!
	now = now.Add(6 * time.Second)
	if n := relay2.FlushOnce(context.Background()); n != 2 {
		t.Fatalf("after lease expiry: want 2 published, got %d (%v)", n, pub.publishedIDs)
	}
	want := []string{"ev-other-1", "ev-crash-1", "ev-crash-2"}
	if len(pub.publishedIDs) != len(want) {
		t.Fatalf("after lease expiry: published=%v, want %v", pub.publishedIDs, want)
	}
	for i := range want {
		if pub.publishedIDs[i] != want[i] {
			t.Fatalf("published[%d]=%q, want %q", i, pub.publishedIDs[i], want[i])
		}
	}

	// Zombie Worker 1 waking up with staleToken MUST NOT succeed in MarkOutboxPublished.
	marked, err := st.MarkOutboxPublished(context.Background(), "ev-crash-1", staleToken)
	if err != nil || marked {
		t.Fatalf("zombie MarkOutboxPublished with stale token: want marked=false err=nil, got marked=%v err=%v", marked, err)
	}
}

type slowClockPublisher struct {
	mu           sync.Mutex
	onPublish    func(m *nats.Msg, opts ...nats.PubOpt) error
	publishedIDs []string
	attempts     int
}

func (p *slowClockPublisher) PublishMsg(m *nats.Msg, opts ...nats.PubOpt) (*nats.PubAck, error) {
	p.mu.Lock()
	p.attempts++
	fn := p.onPublish
	p.mu.Unlock()

	if fn != nil {
		if err := fn(m, opts...); err != nil {
			return nil, err
		}
	}
	p.mu.Lock()
	p.publishedIDs = append(p.publishedIDs, m.Header.Get(nats.MsgIdHdr))
	seq := uint64(len(p.publishedIDs))
	p.mu.Unlock()
	return &nats.PubAck{Stream: "NEXUSMART", Sequence: seq}, nil
}

func TestOutboxRelay_D9_SlowPublisherStopsBeforeLeaseExpiry(t *testing.T) {
	st := store.NewMemoryStore()
	var clockMu sync.Mutex
	now := time.Date(2026, 11, 24, 11, 0, 0, 0, time.UTC)
	getNow := func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return now
	}
	advance := func(d time.Duration) time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		now = now.Add(d)
		return now
	}
	st.SetOutboxClock(getNow)
	// 30s lease with default 5s max publish timeout; each publish takes 4s -> at most 7 events fit (7*4s=28s, remaining 2s < 5s).
	st.SetOutboxLease(30 * time.Second)

	const totalEvents = 12
	for i := 0; i < totalEvents; i++ {
		raw, _ := json.Marshal(ProductReviewedPayload{
			ProductID: "prod-slow", ProductName: "Slow", ShopID: "shop-1", Decision: "APPROVE",
			OccurredAt: now.Add(time.Duration(i) * time.Millisecond).Format(time.RFC3339Nano),
		})
		if err := st.InsertOutboxEvent(context.Background(), store.OutboxEvent{
			ID:          "ev-slow-" + string(rune('a'+i)),
			EventType:   SubjectProductReviewed,
			AggregateID: "prod-slow",
			PayloadJSON: raw,
			CreatedAt:   now.Add(time.Duration(i) * time.Millisecond),
		}); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	batchStart := getNow()
	leaseDeadline := batchStart.Add(30 * time.Second)
	var sawAfterLease bool
	var sawPubOpt bool
	pub := &slowClockPublisher{
		onPublish: func(_ *nats.Msg, opts ...nats.PubOpt) error {
			if len(opts) > 0 {
				sawPubOpt = true
			}
			// Simulate NATS taking 4s to return PubAck.
			finishedAt := advance(4 * time.Second)
			if finishedAt.After(leaseDeadline) {
				sawAfterLease = true
			}
			return nil
		},
	}

	relay := NewOutboxRelay(st, pub, nil, 50*time.Millisecond, nil)
	n1 := relay.FlushOnce(context.Background())
	if sawAfterLease {
		t.Fatalf("publisher executed past lease deadline (published %d events in first flush)", n1)
	}
	if !sawPubOpt {
		t.Fatalf("expected PublishMsg to receive explicit timeout PubOpt (nats.Context)")
	}
	if n1 != 7 {
		t.Fatalf("first flush with 30s lease, 5s max publish timeout, 4s/event: want 7 published, got %d", n1)
	}

	// Remaining 5 events MUST have their leases immediately released with attempts == 0.
	unpubCount := 0
	for _, ev := range st.OutboxEvents() {
		if ev.PublishedAt == nil {
			unpubCount++
			if ev.ClaimToken != "" || ev.ClaimedUntil != nil {
				t.Fatalf("unprocessed event %s still holds lease after early exit: token=%q until=%v", ev.ID, ev.ClaimToken, ev.ClaimedUntil)
			}
			if ev.Attempts != 0 {
				t.Fatalf("unprocessed event %s should have attempts=0, got %d", ev.ID, ev.Attempts)
			}
		}
	}
	if unpubCount != totalEvents-n1 {
		t.Fatalf("remaining unpublished = %d, want %d", unpubCount, totalEvents-n1)
	}

	// Next flush picks up remaining 5 events with a fresh 30s lease and finishes in exact order.
	leaseDeadline = getNow().Add(30 * time.Second)
	n2 := relay.FlushOnce(context.Background())
	if n2 != totalEvents-n1 {
		t.Fatalf("second flush: want %d published, got %d", totalEvents-n1, n2)
	}
	if len(pub.publishedIDs) != totalEvents {
		t.Fatalf("total published = %d, want %d", len(pub.publishedIDs), totalEvents)
	}
	for i := 0; i < totalEvents; i++ {
		wantID := "ev-slow-" + string(rune('a'+i))
		if pub.publishedIDs[i] != wantID {
			t.Fatalf("published[%d] = %q, want %q (all=%v)", i, pub.publishedIDs[i], wantID, pub.publishedIDs)
		}
	}
}

func TestOutboxRelay_D9_TwoConcurrentWorkersSlowPublisherPreserveAggregateOrder(t *testing.T) {
	st := store.NewMemoryStore()
	// Second-scale real-time lease (3s) & publish timeout (1s) with slow publisher (350ms/event <= 400ms):
	// Resilient under heavy host CPU load while still exceeding 3s if lease-budget stop is absent (10 * 350ms = 3.5s).
	st.SetOutboxLease(3 * time.Second)
	st.SetOutboxPublishTimeout(1 * time.Second)

	t0 := time.Now().UTC()
	const totalEvents = 10
	for i := 0; i < totalEvents; i++ {
		raw, _ := json.Marshal(ProductReviewedPayload{
			ProductID: "prod-race", ProductName: "Race", ShopID: "shop-1", Decision: "APPROVE",
			OccurredAt: t0.Add(time.Duration(i) * time.Millisecond).Format(time.RFC3339Nano),
		})
		if err := st.InsertOutboxEvent(context.Background(), store.OutboxEvent{
			ID:          fmt.Sprintf("ev-race-%02d", i),
			EventType:   SubjectProductReviewed,
			AggregateID: "prod-race",
			PayloadJSON: raw,
			CreatedAt:   t0.Add(time.Duration(i) * time.Millisecond),
		}); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	var mu sync.Mutex
	var firstSeenOrder []string
	seenMsgID := make(map[string]string, totalEvents)
	var dupHeaderMismatch bool
	pub := &slowClockPublisher{
		onPublish: func(m *nats.Msg, _ ...nats.PubOpt) error {
			time.Sleep(350 * time.Millisecond)

			msgID := m.Header.Get(nats.MsgIdHdr)
			var env Envelope
			if uErr := json.Unmarshal(m.Data, &env); uErr != nil {
				return uErr
			}

			mu.Lock()
			if msgID == "" || msgID != env.EventID {
				dupHeaderMismatch = true
			}
			if prev, ok := seenMsgID[env.EventID]; !ok {
				seenMsgID[env.EventID] = msgID
				firstSeenOrder = append(firstSeenOrder, env.EventID)
			} else if prev != msgID {
				dupHeaderMismatch = true
			}
			mu.Unlock()
			return nil
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	relay1 := NewOutboxRelay(st, pub, nil, 100*time.Millisecond, nil)
	relay2 := NewOutboxRelay(st, pub, nil, 100*time.Millisecond, nil)

	var wg sync.WaitGroup
	runWorker := func(r *OutboxRelay) {
		defer wg.Done()
		for {
			if ctx.Err() != nil {
				return
			}
			r.FlushOnce(ctx)
			allDone := true
			for _, ev := range st.OutboxEvents() {
				if ev.PublishedAt == nil {
					allDone = false
					break
				}
			}
			if allDone {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	wg.Add(2)
	go runWorker(relay1)
	go runWorker(relay2)
	wg.Wait()

	// Per D9-bis #7: duplicates after lease expiry under host stall are valid when Nats-Msg-Id matches;
	// assert all events delivered, identical Nats-Msg-Id on duplicates, and strict FIRST delivery order.
	if dupHeaderMismatch {
		t.Fatalf("D9 violation: duplicate or published message had mismatched Nats-Msg-Id header")
	}
	if len(firstSeenOrder) != totalEvents {
		t.Fatalf("expected all %d events published, got %d: %v", totalEvents, len(firstSeenOrder), firstSeenOrder)
	}
	for i := 0; i < totalEvents; i++ {
		wantID := fmt.Sprintf("ev-race-%02d", i)
		if firstSeenOrder[i] != wantID {
			t.Fatalf("firstSeenOrder[%d] = %q, want %q (all=%v)", i, firstSeenOrder[i], wantID, firstSeenOrder)
		}
	}
}

func TestOutboxRelay_D9_InfraErrorAbortsBatchAndReleasesRemaining(t *testing.T) {
	infraCases := []struct {
		name string
		err  error
	}{
		{"ErrTimeout", nats.ErrTimeout},
		{"ErrConnectionClosed", nats.ErrConnectionClosed},
		{"ErrNoResponders", nats.ErrNoResponders},
		{"ErrDisconnected", nats.ErrDisconnected},
		{"ErrNoStreamResponse", nats.ErrNoStreamResponse},
	}

	for _, tc := range infraCases {
		t.Run(tc.name, func(t *testing.T) {
			st := store.NewMemoryStore()
			t0 := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

			// 5 events across 5 DIFFERENT aggregates: on NATS infra error, batch MUST stop after 1st failure
			// and release the remaining 4 without attempting them!
			for i := 0; i < 5; i++ {
				agg := "prod-infra-" + string(rune('a'+i))
				raw, _ := json.Marshal(ProductReviewedPayload{
					ProductID: agg, ProductName: agg, ShopID: "shop-1", Decision: "APPROVE",
					OccurredAt: t0.Add(time.Duration(i) * time.Second).Format(time.RFC3339),
				})
				if err := st.InsertOutboxEvent(context.Background(), store.OutboxEvent{
					ID:          "ev-infra-" + string(rune('a'+i)),
					EventType:   SubjectProductReviewed,
					AggregateID: agg,
					PayloadJSON: raw,
					CreatedAt:   t0.Add(time.Duration(i) * time.Second),
				}); err != nil {
					t.Fatalf("insert %d: %v", i, err)
				}
			}

			pub := &slowClockPublisher{
				onPublish: func(_ *nats.Msg, _ ...nats.PubOpt) error {
					return tc.err
				},
			}
			relay := NewOutboxRelay(st, pub, nil, 50*time.Millisecond, nil)
			n := relay.FlushOnce(context.Background())
			if n != 0 {
				t.Fatalf("expected 0 published on %s, got %d", tc.name, n)
			}
			if pub.attempts != 1 {
				t.Fatalf("on %s: expected ONLY 1 publish attempt in batch (abort immediately), got %d", tc.name, pub.attempts)
			}

			evs := st.OutboxEvents()
			for i, ev := range evs {
				if ev.ClaimToken != "" || ev.ClaimedUntil != nil {
					t.Fatalf("event %s still holds lease after infra error abort: token=%q until=%v", ev.ID, ev.ClaimToken, ev.ClaimedUntil)
				}
				if i == 0 {
					if ev.Attempts != 1 || ev.LastError == "" {
						t.Fatalf("first event should record attempt=1 and last_error, got %+v", ev)
					}
				} else if ev.Attempts != 0 {
					t.Fatalf("remaining event %s should be released without attempt (attempts=0), got %d", ev.ID, ev.Attempts)
				}
			}

			// Once NATS recovers, all 5 events are immediately claimable and publish cleanly.
			pub.onPublish = nil
			if nRec := relay.FlushOnce(context.Background()); nRec != 5 {
				t.Fatalf("after recovery: want 5 published, got %d", nRec)
			}
		})
	}
}

func TestOutboxRelay_D9bis_MemoryStore_SeqOrderLostLeasePoisonBackoffAndErrorLog(t *testing.T) {
	// 1. Inverted created_at vs seq still publishes strictly by seq.
	stSeq := store.NewMemoryStore()
	tBase := time.Date(2026, 11, 24, 13, 0, 0, 0, time.UTC)
	insertMem := func(st *store.MemoryStore, id, agg, secret string, at time.Time) {
		t.Helper()
		raw, _ := json.Marshal(ProductReviewedPayload{
			ProductID: agg, ProductName: secret, ShopID: "shop-1", Decision: "APPROVE", OccurredAt: at.Format(time.RFC3339),
		})
		if err := st.InsertOutboxEvent(context.Background(), store.OutboxEvent{
			ID: id, EventType: SubjectProductReviewed, AggregateID: agg, PayloadJSON: raw, CreatedAt: at,
		}); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	insertMem(stSeq, "mem-seq-1", "agg-seq", "first", tBase.Add(10*time.Second))
	insertMem(stSeq, "mem-seq-2", "agg-seq", "second", tBase)
	pubSeq := &slowClockPublisher{}
	if n := NewOutboxRelay(stSeq, pubSeq, nil, 50*time.Millisecond, nil).FlushOnce(context.Background()); n != 2 {
		t.Fatalf("seq order flush = %d, want 2", n)
	}
	if fmt.Sprint(pubSeq.publishedIDs) != "[mem-seq-1 mem-seq-2]" {
		t.Fatalf("memory store seq order = %v, want [mem-seq-1 mem-seq-2]", pubSeq.publishedIDs)
	}

	// 2. Lost lease mid-batch releases remaining claims and aborts immediately.
	stLost := store.NewMemoryStore()
	insertMem(stLost, "mem-lost-1", "agg-l1", "p1", tBase)
	insertMem(stLost, "mem-lost-2", "agg-l2", "p2", tBase.Add(time.Second))
	insertMem(stLost, "mem-lost-3", "agg-l3", "p3", tBase.Add(2*time.Second))
	var attempted []string
	nLost, err := stLost.ProcessPendingOutbox(context.Background(), 10, func(_ context.Context, ev store.OutboxEvent) error {
		attempted = append(attempted, ev.ID)
		if ev.ID == "mem-lost-1" {
			stLost.StealOutboxClaimForTest("mem-lost-1", "stolen-mem-tok")
		}
		return nil
	})
	if err != nil || nLost != 0 || fmt.Sprint(attempted) != "[mem-lost-1]" {
		t.Fatalf("lost lease mid-batch = (n=%d, err=%v, attempted=%v), want (0, nil, [mem-lost-1])", nLost, err, attempted)
	}
	for _, ev := range stLost.OutboxEvents() {
		if ev.ID != "mem-lost-1" && (ev.ClaimToken != "" || ev.ClaimedUntil != nil) {
			t.Fatalf("remaining event %s still leased after lost lease: tok=%q until=%v", ev.ID, ev.ClaimToken, ev.ClaimedUntil)
		}
	}

	// 3. Poison backoff (1+49 same aggregate & 50 distinct aggregates) never starves healthy aggregate,
	//    and attempts >= 10 logs ERROR with event_id & event_type WITHOUT payload contents.
	stPoison := store.NewMemoryStore()
	var clkMu sync.Mutex
	now := tBase
	stPoison.SetOutboxClock(func() time.Time {
		clkMu.Lock()
		defer clkMu.Unlock()
		return now
	})
	var logBuf bytes.Buffer
	stPoison.SetLogger(slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo})))

	const secretMarker = "DO-NOT-LOG-THIS-PAYLOAD-SECRET-98765"
	for i := 0; i < 50; i++ {
		insertMem(stPoison, fmt.Sprintf("mem-p-%02d", i), "agg-poison-same", secretMarker, tBase.Add(time.Duration(i)*time.Millisecond))
	}
	insertMem(stPoison, "mem-healthy", "agg-healthy", "ok", tBase.Add(time.Second))

	n1, err := stPoison.ProcessPendingOutbox(context.Background(), 50, func(_ context.Context, ev store.OutboxEvent) error {
		if ev.ID == "mem-p-00" {
			return errors.New("poison error")
		}
		return nil
	})
	if err != nil || n1 != 0 {
		t.Fatalf("poison pass 1 = (%d, %v), want (0, nil)", n1, err)
	}
	var pass2 []string
	n2, err := stPoison.ProcessPendingOutbox(context.Background(), 50, func(_ context.Context, ev store.OutboxEvent) error {
		pass2 = append(pass2, ev.ID)
		return nil
	})
	if err != nil || n2 != 1 || fmt.Sprint(pass2) != "[mem-healthy]" {
		t.Fatalf("healthy aggregate starved in MemoryStore: n=%d ids=%v err=%v", n2, pass2, err)
	}

	// Drive mem-p-00 through attempts 2..10 advancing fake clock past each exponential backoff (capped at 60s).
	for attempt := 2; attempt <= 10; attempt++ {
		clkMu.Lock()
		now = now.Add(61 * time.Second)
		clkMu.Unlock()
		logBuf.Reset()
		_, _ = stPoison.ProcessPendingOutbox(context.Background(), 50, func(_ context.Context, ev store.OutboxEvent) error {
			if ev.ID == "mem-p-00" {
				return errors.New("persistent poison error")
			}
			return nil
		})
		out := logBuf.String()
		if attempt < 10 && strings.Contains(out, `"level":"ERROR"`) {
			t.Fatalf("attempt %d unexpectedly logged ERROR: %s", attempt, out)
		}
		if attempt == 10 {
			if !strings.Contains(out, `"level":"ERROR"`) || !strings.Contains(out, "mem-p-00") || !strings.Contains(out, SubjectProductReviewed) {
				t.Fatalf("attempt 10 missing expected ERROR log fields: %s", out)
			}
			if strings.Contains(out, secretMarker) {
				t.Fatalf("attempt 10 ERROR log leaked payload secret: %s", out)
			}
		}
	}
}
