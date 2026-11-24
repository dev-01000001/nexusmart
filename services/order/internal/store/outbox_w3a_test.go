package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nexusmart/core/pkg/common/jetstream"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/order/internal/db"
	"github.com/nexusmart/core/services/order/internal/events"
)

type stubOutboxPublisher struct {
	mu           sync.Mutex
	publishedIDs []string
	onPublish    func(m *nats.Msg, opts ...nats.PubOpt) error
}

func (p *stubOutboxPublisher) PublishMsg(m *nats.Msg, opts ...nats.PubOpt) (*nats.PubAck, error) {
	if p.onPublish != nil {
		if err := p.onPublish(m, opts...); err != nil {
			return nil, err
		}
	}
	p.mu.Lock()
	p.publishedIDs = append(p.publishedIDs, m.Header.Get(nats.MsgIdHdr))
	p.mu.Unlock()
	return &nats.PubAck{Stream: "NEXUSMART", Sequence: 1}, nil
}

type countingOutboxStore struct {
	inner events.OutboxStore
	calls atomic.Int32
}

func (c *countingOutboxStore) ProcessPendingOutbox(ctx context.Context, limit int32, publishFn func(ctx context.Context, ev db.OutboxEvent) error) (int, error) {
	c.calls.Add(1)
	return c.inner.ProcessPendingOutbox(ctx, limit, publishFn)
}

// TestOutboxRelayW3A_D9_SlowPublisherStopsBeforeLeaseExpires verifies that the relay measures lease budget
// with the Go clock captured BEFORE claiming and stops publishing + releases remaining claims once
// remaining lease < publishTimeout, preventing a slow worker from publishing after lease expiration.
func TestOutboxRelayW3A_D9_SlowPublisherStopsBeforeLeaseExpires(t *testing.T) {
	st := NewMemoryStore()
	st.SetOutboxLease(15 * time.Second)
	st.SetOutboxPublishTimeout(5 * time.Second)

	var clockMu sync.Mutex
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	getNow := func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return now
	}
	advance := func(d time.Duration) {
		clockMu.Lock()
		now = now.Add(d)
		clockMu.Unlock()
	}
	st.SetOutboxClock(getNow)

	const totalEvents = 5
	for i := 0; i < totalEvents; i++ {
		raw, _ := json.Marshal(events.OrderCreatedPayload{OrderID: "ord-slow"})
		st.InsertOutboxEvent(db.OutboxEvent{
			ID:          "ev-slow-" + string(rune('a'+i)),
			EventType:   events.SubjectOrderCreated,
			PayloadJSON: raw,
			CreatedAt:   now.Add(time.Duration(i) * time.Millisecond),
		})
	}

	// Each publish takes 4s on the fake clock. Lease = 15s, pubTimeout = 5s:
	//   ev-a at t=0  (rem=15s >= 5s) -> finishes at t=4s
	//   ev-b at t=4  (rem=11s >= 5s) -> finishes at t=8s
	//   ev-c at t=8  (rem=7s  >= 5s) -> finishes at t=12s
	//   ev-d at t=12 (rem=3s  <  5s) -> MUST STOP and release ev-d, ev-e without publishing!
	pub := &stubOutboxPublisher{
		onPublish: func(_ *nats.Msg, _ ...nats.PubOpt) error {
			advance(4 * time.Second)
			return nil
		},
	}

	relay := events.NewOutboxRelay(st, pub, logger.New("order-test", logger.WithWriter(io.Discard)), 100*time.Millisecond, nil)
	n1 := relay.FlushOnce(context.Background())
	if n1 != 3 {
		t.Fatalf("D9 lease budget violation: want 3 published before stopping, got %d (published=%v)", n1, pub.publishedIDs)
	}
	for _, ev := range st.OutboxEvents() {
		if ev.ID == "ev-slow-d" || ev.ID == "ev-slow-e" {
			if ev.PublishedAt != nil || ev.ClaimedUntil != nil || ev.ClaimToken != "" || ev.Attempts != 0 {
				t.Fatalf("unreached event %s must be released cleanly: %+v", ev.ID, ev)
			}
		}
	}

	n2 := relay.FlushOnce(context.Background())
	if n2 != 2 || len(pub.publishedIDs) != totalEvents {
		t.Fatalf("second flush: want 2 published (total %d), got n2=%d all=%v", totalEvents, n2, pub.publishedIDs)
	}
	for i := 0; i < totalEvents; i++ {
		wantID := "ev-slow-" + string(rune('a'+i))
		if pub.publishedIDs[i] != wantID {
			t.Fatalf("published[%d] = %q, want %q (all=%v)", i, pub.publishedIDs[i], wantID, pub.publishedIDs)
		}
	}
}

// TestOutboxRelayW3A_D9_TwoConcurrentWorkersSameAggregateSlowPublisher verifies D9-bis §7 with
// second-scale lease (>= 3s), 1s publish timeout, and slow publish <= 400ms (zero flake under load):
// two concurrent workers never publish events of the same aggregate concurrently and preserve strict seq order.
func TestOutboxRelayW3A_D9_TwoConcurrentWorkersSameAggregateSlowPublisher(t *testing.T) {
	st := NewMemoryStore()
	st.SetOutboxLease(3 * time.Second)
	st.SetOutboxPublishTimeout(1 * time.Second)

	t0 := time.Now().UTC()
	const totalEvents = 10
	for i := 0; i < totalEvents; i++ {
		raw, _ := json.Marshal(events.OrderCreatedPayload{OrderID: "ord-race-agg"})
		st.InsertOutboxEvent(db.OutboxEvent{
			ID:          fmt.Sprintf("ev-race-%02d", i),
			EventType:   events.SubjectOrderCreated,
			PayloadJSON: raw,
			CreatedAt:   t0.Add(time.Duration(i) * time.Millisecond),
		})
	}

	var mu sync.Mutex
	var publishedOrder []string
	var inFlight int
	var sawConcurrent bool
	pub := &stubOutboxPublisher{
		onPublish: func(m *nats.Msg, _ ...nats.PubOpt) error {
			mu.Lock()
			inFlight++
			if inFlight > 1 {
				sawConcurrent = true
			}
			mu.Unlock()

			time.Sleep(350 * time.Millisecond)

			mu.Lock()
			inFlight--
			publishedOrder = append(publishedOrder, m.Header.Get(nats.MsgIdHdr))
			mu.Unlock()
			return nil
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	log := logger.New("order-test", logger.WithWriter(io.Discard))
	relay1 := events.NewOutboxRelay(st, pub, log, 50*time.Millisecond, nil)
	relay2 := events.NewOutboxRelay(st, pub, log, 50*time.Millisecond, nil)

	var wg sync.WaitGroup
	runWorker := func(r *events.OutboxRelay) {
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
			time.Sleep(50 * time.Millisecond)
		}
	}
	wg.Add(2)
	go runWorker(relay1)
	go runWorker(relay2)
	wg.Wait()

	if sawConcurrent {
		t.Fatalf("D9 P1 violation: two workers published events of the same aggregate concurrently (lease overrun!)")
	}
	if len(publishedOrder) != totalEvents {
		t.Fatalf("expected %d unique publishes, got %d: %v", totalEvents, len(publishedOrder), publishedOrder)
	}
	for i := 0; i < totalEvents; i++ {
		wantID := fmt.Sprintf("ev-race-%02d", i)
		if publishedOrder[i] != wantID {
			t.Fatalf("publishedOrder[%d] = %q, want %q (all=%v)", i, publishedOrder[i], wantID, publishedOrder)
		}
	}
}

// TestOutboxRelayW3A_D9_NATSTimeoutAndInfraErrorsAbortBatch verifies:
//  1. On NATS/transport infra error, ProcessPendingOutbox aborts the batch immediately and releases
//     all remaining events without attempting them (even across distinct aggregates).
//  2. OutboxRelay passes nats.Context(ctx) with the single-source publish timeout from ProcessPendingOutbox.
func TestOutboxRelayW3A_D9_NATSTimeoutAndInfraErrorsAbortBatch(t *testing.T) {
	infraCases := []struct {
		name string
		err  error
	}{
		{"ErrTimeout", nats.ErrTimeout},
		{"ErrConnectionClosed", nats.ErrConnectionClosed},
		{"ErrDisconnected", nats.ErrDisconnected},
		{"ErrNoResponders", nats.ErrNoResponders},
		{"ErrNoStreamResponse", nats.ErrNoStreamResponse},
		{"DeadlineExceeded", context.DeadlineExceeded},
	}

	for _, tc := range infraCases {
		t.Run(tc.name, func(t *testing.T) {
			st := NewMemoryStore()
			t0 := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
			for i := 0; i < 5; i++ {
				agg := "ord-infra-" + string(rune('a'+i))
				raw, _ := json.Marshal(events.OrderCreatedPayload{OrderID: agg})
				st.InsertOutboxEvent(db.OutboxEvent{
					ID:          "ev-infra-" + string(rune('a'+i)),
					EventType:   events.SubjectOrderCreated,
					PayloadJSON: raw,
					CreatedAt:   t0.Add(time.Duration(i) * time.Second),
				})
			}

			attempts := 0
			pub := &stubOutboxPublisher{
				onPublish: func(_ *nats.Msg, opts ...nats.PubOpt) error {
					attempts++
					if len(opts) == 0 {
						t.Errorf("expected PublishMsg to receive nats.Context(pubCtx) option")
					}
					return tc.err
				},
			}

			relay := events.NewOutboxRelay(st, pub, logger.New("order-test", logger.WithWriter(io.Discard)), 100*time.Millisecond, nil)
			if n := relay.FlushOnce(context.Background()); n != 0 {
				t.Fatalf("expected 0 published on infra error %s, got %d", tc.name, n)
			}
			if attempts != 1 {
				t.Fatalf("D9 infra abort violation (%s): expected batch to stop after 1st attempt, got %d attempts", tc.name, attempts)
			}
			for _, ev := range st.OutboxEvents() {
				if ev.ID == "ev-infra-a" {
					if ev.Attempts != 1 || ev.ClaimToken != "" || ev.ClaimedUntil != nil {
						t.Fatalf("first failed event state invalid: %+v", ev)
					}
				} else {
					if ev.Attempts != 0 || ev.ClaimToken != "" || ev.ClaimedUntil != nil {
						t.Fatalf("remaining event %s must be released untouched (attempts=0), got %+v", ev.ID, ev)
					}
				}
			}
		})
	}

	t.Run("PublishCtxTimeoutEnforcedOnHangingPublisher", func(t *testing.T) {
		st := NewMemoryStore()
		st.SetOutboxLease(3 * time.Second)
		st.SetOutboxPublishTimeout(1 * time.Second)
		for i := 0; i < 3; i++ {
			raw, _ := json.Marshal(events.OrderCreatedPayload{OrderID: "ord-hang-" + string(rune('a'+i))})
			st.InsertOutboxEvent(db.OutboxEvent{
				ID:          "ev-hang-" + string(rune('a'+i)),
				EventType:   events.SubjectOrderCreated,
				PayloadJSON: raw,
				CreatedAt:   time.Now().UTC().Add(time.Duration(i) * time.Millisecond),
			})
		}

		calls := 0
		n, err := st.ProcessPendingOutbox(context.Background(), 10, func(pubCtx context.Context, _ db.OutboxEvent) error {
			calls++
			if _, ok := pubCtx.Deadline(); !ok {
				t.Fatalf("expected explicit deadline on publishFn ctx")
			}
			<-pubCtx.Done()
			return pubCtx.Err()
		})
		if n != 0 || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected n=0 err=DeadlineExceeded, got n=%d err=%v", n, err)
		}
		if calls != 1 {
			t.Fatalf("expected hanging timeout to abort batch after 1 call, got %d", calls)
		}
	})
}

// TestOutboxRelayW3A_D9Bis_OrderingBackoffLostLeaseAndTriggerThrottle covers D9-bis + P3-8 on MemoryStore:
//   - P3-8: pubTimeout >= lease is clamped strictly below lease (never deadlocks at 0 published).
//   - 10(d): inverted created_at vs seq still publishes strictly by seq ASC.
//   - 10(e): 50 poison events / 1 poison + 49 same-order events back off without starving other orders,
//     log ERROR at attempts >= 10 with event_id & event_type (NO payload), and never dead-letter outbox rows.
//   - 10(f): affected == 0 mid-batch (lost lease) immediately releases remaining claims and stops batch.
//   - 10(g): NATS dead + continuous triggerCh hammering is throttled by RunOutboxRelayLoop backoff and recovers.
func TestOutboxRelayW3A_D9Bis_OrderingBackoffLostLeaseAndTriggerThrottle(t *testing.T) {
	t.Run("P3_8_PublishTimeoutClampedBelowLease", func(t *testing.T) {
		st := NewMemoryStore()
		st.SetOutboxLease(3 * time.Second)
		st.SetOutboxPublishTimeout(5 * time.Second) // >= lease! must clamp to < 3s (1.5s)
		raw, _ := json.Marshal(events.OrderCreatedPayload{OrderID: "ord-clamp"})
		st.InsertOutboxEvent(db.OutboxEvent{ID: "ev-clamp-1", EventType: events.SubjectOrderCreated, PayloadJSON: raw})

		var observedDeadline time.Duration
		n, err := st.ProcessPendingOutbox(context.Background(), 10, func(pubCtx context.Context, _ db.OutboxEvent) error {
			dl, ok := pubCtx.Deadline()
			if !ok {
				t.Fatalf("expected deadline on pubCtx")
			}
			observedDeadline = time.Until(dl)
			return nil
		})
		if err != nil || n != 1 {
			t.Fatalf("P3-8 violation: pubTimeout >= lease deadlocked batch: n=%d err=%v", n, err)
		}
		if observedDeadline >= 3*time.Second {
			t.Fatalf("expected clamped publish timeout < 3s lease, got remaining %v", observedDeadline)
		}
	})

	t.Run("10d_CreatedAtInvertedVsSeq_OrdersStrictlyBySeq", func(t *testing.T) {
		st := NewMemoryStore()
		tNow := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
		raw1, _ := json.Marshal(events.OrderCreatedPayload{OrderID: "ord-inv"})
		raw2, _ := json.Marshal(events.OrderConfirmedPayload{OrderID: "ord-inv"})
		// Earlier seq=10 has LATER created_at; later seq=11 has EARLIER created_at (PostgreSQL BEGIN TX timestamp inversion).
		st.InsertOutboxEvent(db.OutboxEvent{ID: "ev-seq-10", Seq: 10, EventType: events.SubjectOrderShipped, PayloadJSON: raw1, CreatedAt: tNow.Add(5 * time.Minute)})
		st.InsertOutboxEvent(db.OutboxEvent{ID: "ev-seq-11", Seq: 11, EventType: events.SubjectOrderDelivered, PayloadJSON: raw2, CreatedAt: tNow.Add(-5 * time.Minute)})

		var seen []string
		n, err := st.ProcessPendingOutbox(context.Background(), 10, func(_ context.Context, ev db.OutboxEvent) error {
			seen = append(seen, ev.ID)
			return nil
		})
		if err != nil || n != 2 {
			t.Fatalf("ProcessPendingOutbox = %d, %v; want 2, nil", n, err)
		}
		if fmt.Sprint(seen) != "[ev-seq-10 ev-seq-11]" {
			t.Fatalf("D9-bis §1 violation: expected strict seq order [ev-seq-10 ev-seq-11], got %v", seen)
		}
	})

	t.Run("10e_PoisonBackoffNoStarvationAndErrorLogAt10NoPayload", func(t *testing.T) {
		st := NewMemoryStore()
		var logBuf bytes.Buffer
		st.SetLogger(slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))

		var clockMu sync.Mutex
		fakeNow := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
		st.SetOutboxClock(func() time.Time {
			clockMu.Lock()
			defer clockMu.Unlock()
			return fakeNow
		})
		advance := func(d time.Duration) {
			clockMu.Lock()
			fakeNow = fakeNow.Add(d)
			clockMu.Unlock()
		}

		// 1 poison event + 49 trailing events on ord-poison, followed by healthy ord-healthy (batch_limit=50).
		const secretMarker = "SECRET-DO-NOT-LOG-PAYLOAD"
		poisonPayload, _ := json.Marshal(map[string]string{"order_id": "ord-poison", "secret": secretMarker})
		st.InsertOutboxEvent(db.OutboxEvent{ID: "ev-poison-head", EventType: events.SubjectOrderCreated, PayloadJSON: poisonPayload})
		for i := 1; i < 50; i++ {
			raw, _ := json.Marshal(events.OrderConfirmedPayload{OrderID: "ord-poison"})
			st.InsertOutboxEvent(db.OutboxEvent{ID: fmt.Sprintf("ev-poison-tail-%02d", i), EventType: events.SubjectOrderConfirmed, PayloadJSON: raw})
		}
		healthyPayload, _ := json.Marshal(events.OrderCreatedPayload{OrderID: "ord-healthy"})
		st.InsertOutboxEvent(db.OutboxEvent{ID: "ev-healthy", EventType: events.SubjectOrderCreated, PayloadJSON: healthyPayload})

		var published []string
		pubFn := func(_ context.Context, ev db.OutboxEvent) error {
			if ev.ID == "ev-poison-head" {
				return errors.New("non-infra bad payload")
			}
			published = append(published, ev.ID)
			return nil
		}

		// Pass 1 claims 50 events of ord-poison: ev-poison-head fails into backoff (2s), tails released untouched.
		n1, err := st.ProcessPendingOutbox(context.Background(), 50, pubFn)
		if err != nil || n1 != 0 {
			t.Fatalf("pass 1 = %d, %v; want 0, nil", n1, err)
		}
		// Pass 2 immediately afterward MUST skip all 50 ord-poison events (blocked by head's backoff) and publish ev-healthy!
		n2, err := st.ProcessPendingOutbox(context.Background(), 50, pubFn)
		if err != nil || n2 != 1 || fmt.Sprint(published) != "[ev-healthy]" {
			t.Fatalf("D9-bis §2/§4 starvation violation: pass 2 = %d err=%v published=%v, want [ev-healthy]", n2, err, published)
		}

		// Drive ev-poison-head to attempt 10 by advancing fake clock past each backoff -> ERROR log without payload, 0 DLQ.
		for attempt := int32(2); attempt <= jetstream.OutboxPoisonLogAttempt; attempt++ {
			advance(61 * time.Second)
			if _, err := st.ProcessPendingOutbox(context.Background(), 50, pubFn); err != nil {
				t.Fatalf("attempt %d err: %v", attempt, err)
			}
		}
		logged := logBuf.String()
		if !strings.Contains(logged, `"level":"ERROR"`) || !strings.Contains(logged, "ev-poison-head") || !strings.Contains(logged, events.SubjectOrderCreated) {
			t.Fatalf("D9-bis §4 violation: expected ERROR log with event_id and event_type at attempts>=10, got %q", logged)
		}
		if strings.Contains(logged, secretMarker) {
			t.Fatalf("D9-bis §4 violation: poison ERROR log leaked payload data: %q", logged)
		}
		if len(st.DeadLetters()) != 0 {
			t.Fatalf("D9-bis §4 violation: outbox poison event must NOT be dead-lettered, got %d DLQ rows", len(st.DeadLetters()))
		}
	})

	t.Run("10f_LostLeaseAffectedZeroStopsBatchAndReleasesRemaining", func(t *testing.T) {
		st := NewMemoryStore()
		for i := 0; i < 3; i++ {
			raw, _ := json.Marshal(events.OrderCreatedPayload{OrderID: fmt.Sprintf("ord-lose-%d", i)})
			st.InsertOutboxEvent(db.OutboxEvent{ID: fmt.Sprintf("ev-lose-%d", i), EventType: events.SubjectOrderCreated, PayloadJSON: raw})
		}

		var seen []string
		n, err := st.ProcessPendingOutbox(context.Background(), 10, func(_ context.Context, ev db.OutboxEvent) error {
			seen = append(seen, ev.ID)
			if ev.ID == "ev-lose-0" {
				// Simulate another worker stealing ev-lose-0 after lease expiry so MarkPublished affects 0 rows.
				st.mu.Lock()
				for j := range st.outbox {
					if st.outbox[j].ID == "ev-lose-0" {
						st.outbox[j].ClaimToken = "other-worker-token"
						break
					}
				}
				st.mu.Unlock()
			}
			return nil
		})
		if err != nil || n != 0 {
			t.Fatalf("lost lease batch = %d, %v; want 0, nil", n, err)
		}
		if fmt.Sprint(seen) != "[ev-lose-0]" {
			t.Fatalf("D9-bis §3 violation: worker kept publishing after affected==0! seen=%v", seen)
		}
		for _, ev := range st.OutboxEvents() {
			if ev.ID == "ev-lose-1" || ev.ID == "ev-lose-2" {
				if ev.ClaimToken != "" || ev.ClaimedUntil != nil || ev.Attempts != 0 {
					t.Fatalf("D9-bis §3 violation: remaining event %s not released after lost lease: %+v", ev.ID, ev)
				}
			}
		}
	})

	t.Run("10g_NATSDeadWithContinuousTriggers_RelayBackoffCapsClaimRate", func(t *testing.T) {
		st := NewMemoryStore()
		raw, _ := json.Marshal(events.OrderCreatedPayload{OrderID: "ord-trig-1"})
		st.InsertOutboxEvent(db.OutboxEvent{ID: "ev-trig-1", EventType: events.SubjectOrderCreated, PayloadJSON: raw})

		counted := &countingOutboxStore{inner: st}
		var natsDown atomic.Bool
		natsDown.Store(true)
		pub := &stubOutboxPublisher{
			onPublish: func(_ *nats.Msg, _ ...nats.PubOpt) error {
				if natsDown.Load() {
					return nats.ErrDisconnected
				}
				return nil
			},
		}

		trigCh := make(chan struct{}, 1)
		relay := events.NewOutboxRelay(counted, pub, logger.New("order-test", logger.WithWriter(io.Discard)), 25*time.Millisecond, trigCh)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer close(done)
			relay.Run(ctx)
		}()

		// Hammer triggerCh every 20ms for 1.1s while NATS is disconnected -> backoff (250ms->500ms->1s) must cap claims!
		endHammer := time.Now().Add(1100 * time.Millisecond)
		for time.Now().Before(endHammer) {
			select {
			case trigCh <- struct{}{}:
			default:
			}
			time.Sleep(20 * time.Millisecond)
		}
		downCalls := counted.calls.Load()
		if downCalls < 1 || downCalls > 5 {
			cancel()
			<-done
			t.Fatalf("D9-bis §5 violation: NATS dead + 20ms triggerCh caused hot loop (%d claims in 1.1s; want 1..5)", downCalls)
		}

		// Bring NATS back -> relay recovers and publishes ev-trig-1 promptly.
		natsDown.Store(false)
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			evs := st.OutboxEvents()
			if len(evs) == 1 && evs[0].PublishedAt != nil {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		cancel()
		<-done
		if evs := st.OutboxEvents(); len(evs) != 1 || evs[0].PublishedAt == nil {
			t.Fatalf("expected relay to recover and publish ev-trig-1 once NATS returned")
		}
	})

	// (h) D9-bis §11: *nats.APIError >= 500 is classified as infra error (apply_backoff=false,
	//     claimed_until cleared, batch aborted), whereas *nats.APIError < 500 is non-infra
	//     (apply_backoff=true, per-row backoff applied, other aggregates still published).
	t.Run("APIError5xxInfraNoRowBackoff_4xxRowBackoff", func(t *testing.T) {
		st := NewMemoryStore()
		p1, _ := json.Marshal(events.OrderCreatedPayload{OrderID: "ord-api-1"})
		p2, _ := json.Marshal(events.OrderCreatedPayload{OrderID: "ord-api-2"})
		st.InsertOutboxEvent(db.OutboxEvent{ID: "ev-api-1", EventType: events.SubjectOrderCreated, PayloadJSON: p1})
		st.InsertOutboxEvent(db.OutboxEvent{ID: "ev-api-2", EventType: events.SubjectOrderCreated, PayloadJSON: p2})

		// 1. 503 APIError -> infra: aborts batch before ev-api-2, leaves both claimed_until=nil.
		api503 := &nats.APIError{Code: 503, Description: "no responders / stream unavailable"}
		n, err := st.ProcessPendingOutbox(context.Background(), 10, func(_ context.Context, ev db.OutboxEvent) error {
			if ev.ID == "ev-api-1" {
				return api503
			}
			t.Fatalf("batch should have stopped after 503 infra error before publishing %s", ev.ID)
			return nil
		})
		if !errors.Is(err, api503) || n != 0 {
			t.Fatalf("503 infra: got n=%d err=%v, want n=0 err=%v", n, err, api503)
		}
		for _, ev := range st.OutboxEvents() {
			if ev.ClaimedUntil != nil || ev.ClaimToken != "" {
				t.Fatalf("503 infra must clear claimed_until & claim_token (apply_backoff=false), got %+v", ev)
			}
		}

		// 2. 400 APIError -> non-infra: applies per-row backoff on ev-api-1, still publishes ev-api-2.
		api400 := &nats.APIError{Code: 400, Description: "bad request"}
		n, err = st.ProcessPendingOutbox(context.Background(), 10, func(_ context.Context, ev db.OutboxEvent) error {
			if ev.ID == "ev-api-1" {
				return api400
			}
			return nil
		})
		if err != nil || n != 1 {
			t.Fatalf("400 non-infra: got n=%d err=%v, want n=1 err=nil", n, err)
		}
		for _, ev := range st.OutboxEvents() {
			if ev.ID == "ev-api-1" && (ev.ClaimedUntil == nil || !ev.ClaimedUntil.After(time.Now().UTC())) {
				t.Fatalf("400 non-infra must apply per-row backoff on ev-api-1, got %+v", ev)
			}
			if ev.ID == "ev-api-2" && ev.PublishedAt == nil {
				t.Fatalf("ev-api-2 must have been published despite ev-api-1 400 error")
			}
		}
	})

	// (i) P3-2: Mixing PersistCheckoutBatch (normal store write) and InsertOutboxEvent preserves monotonic seq order.
	t.Run("MemoryStore_MixedOutboxAppendPreservesMonotonicSeq", func(t *testing.T) {
		st := NewMemoryStore()
		ctx := context.Background()
		_, _ = st.CreateCheckoutGroup(ctx, db.CreateCheckoutGroupParams{
			ID: "grp-seq-mix", BuyerID: "b1", IdempotencyKey: "idem-seq-mix",
			RequestHash: "h", Status: "IN_PROGRESS", PaymentMethod: "COD",
		})
		pFirst, _ := json.Marshal(events.OrderCreatedPayload{OrderID: "ord-seq-mix"})
		_, err := st.PersistCheckoutBatch(ctx, CheckoutBatch{
			CheckoutGroupID: "grp-seq-mix",
			Orders: []NewOrderRecord{{
				Order: db.InsertOrderParams{
					ID: "ord-seq-mix", OrderCode: "NM-SEQ-MIX", CheckoutGroupID: "grp-seq-mix",
					BuyerID: "b1", ShopID: "shop-1", Status: "CONFIRMED",
					PaymentMethod: "COD", PaymentStatus: "PENDING", TotalPayableVND: 100000,
					ShippingAddressJSON: []byte(`{}`),
				},
				Outbox: []db.InsertOutboxEventParams{{
					ID: "ev-first-persist", EventType: events.SubjectOrderCreated, PayloadJSON: pFirst,
				}},
			}},
		})
		if err != nil {
			t.Fatalf("PersistCheckoutBatch: %v", err)
		}
		pSecond, _ := json.Marshal(events.OrderConfirmedPayload{OrderID: "ord-seq-mix"})
		st.InsertOutboxEvent(db.OutboxEvent{ID: "ev-second-direct", EventType: events.SubjectOrderConfirmed, PayloadJSON: pSecond})

		evs := st.OutboxEvents()
		if len(evs) != 2 || evs[0].Seq <= 0 || evs[0].Seq >= evs[1].Seq {
			t.Fatalf("P3-2 violation: mixed outbox appends inverted seq: %+v", evs)
		}
	})
}
