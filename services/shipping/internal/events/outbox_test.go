package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/nexusmart/core/services/shipping/internal/store"
)

func openIsolatedEventsTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_SHIPPING_DB_URL"))
	if dsn == "" {
		t.Skip("set TEST_SHIPPING_DB_URL to run real Postgres OutboxRelay test")
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse TEST_SHIPPING_DB_URL: %v", err)
	}
	adminURL := *u
	adminURL.Path = "/postgres"

	ctx := context.Background()
	adminConn, err := pgx.Connect(ctx, adminURL.String())
	if err != nil {
		t.Fatalf("connect admin postgres: %v", err)
	}
	dbName := "ship_events_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if _, err := adminConn.Exec(ctx, "CREATE DATABASE "+dbName); err != nil {
		_ = adminConn.Close(ctx)
		t.Fatalf("create isolated test db %s: %v", dbName, err)
	}
	_ = adminConn.Close(ctx)

	testURL := *u
	testURL.Path = "/" + dbName
	pool, err := pgxpool.New(ctx, testURL.String())
	if err != nil {
		dropIsolatedEventsDB(adminURL.String(), dbName)
		t.Fatalf("pgxpool.New(%s): %v", dbName, err)
	}
	t.Cleanup(func() {
		pool.Close()
		dropIsolatedEventsDB(adminURL.String(), dbName)
	})
	return pool
}

func dropIsolatedEventsDB(adminDSN, dbName string) {
	dropCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := pgx.Connect(dropCtx, adminDSN)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close(dropCtx) }()
	_, _ = conn.Exec(dropCtx, "DROP DATABASE IF EXISTS "+dbName+" WITH (FORCE)")
}

type stubOutboxStore struct {
	batches [][]store.OutboxEvent
	calls   int
	retErr  error
}

func (s *stubOutboxStore) ProcessPendingOutbox(ctx context.Context, _ int, publishFn func(ctx context.Context, ev store.OutboxEvent) error) (int, error) {
	if s.retErr != nil {
		return 0, s.retErr
	}
	if s.calls >= len(s.batches) {
		return 0, nil
	}
	batch := s.batches[s.calls]
	s.calls++
	published := 0
	for _, ev := range batch {
		if err := publishFn(ctx, ev); err != nil {
			return published, err
		}
		published++
	}
	return published, nil
}

type recordingPublisher struct {
	mu           sync.Mutex
	msgs         []*nats.Msg
	sawPubOpt    bool
	failByID     map[string]error
	globalErr    error
	onPublish    func(m *nats.Msg, opts ...nats.PubOpt) error
	attemptCount int
}

func (p *recordingPublisher) PublishMsg(m *nats.Msg, opts ...nats.PubOpt) (*nats.PubAck, error) {
	p.mu.Lock()
	p.attemptCount++
	if len(opts) > 0 {
		p.sawPubOpt = true
	}
	fn := p.onPublish
	gErr := p.globalErr
	evID := m.Header.Get(nats.MsgIdHdr)
	idErr := p.failByID[evID]
	p.mu.Unlock()

	if fn != nil {
		if err := fn(m, opts...); err != nil {
			return nil, err
		}
	}
	if gErr != nil {
		return nil, gErr
	}
	if idErr != nil {
		return nil, idErr
	}

	cp := *m
	cp.Data = append([]byte(nil), m.Data...)
	cp.Header = nats.Header{}
	for k, v := range m.Header {
		cp.Header[k] = append([]string(nil), v...)
	}

	p.mu.Lock()
	p.msgs = append(p.msgs, &cp)
	seq := uint64(len(p.msgs))
	p.mu.Unlock()
	return &nats.PubAck{Stream: "NEXUSMART", Sequence: seq}, nil
}

func TestOutboxRelay_W3A_D9_EnvelopeHeadersTimeoutAndBatchLoop(t *testing.T) {
	discardLog := slog.New(slog.NewTextHandler(io.Discard, nil))
	t0 := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	payload, _ := json.Marshal(ShipmentUpdatedPayload{
		ShipmentID: "sh-1", OrderID: "ord-1", ShopID: "shop-1", CarrierCode: "ghn",
		TrackingCode: "GHN-1", Status: "delivering", Direction: store.DirectionForward,
	})

	// 1. Nil store/publisher is a safe no-op.
	if n := NewOutboxRelay(nil, nil, 10*time.Millisecond, discardLog).FlushOnce(context.Background()); n != 0 {
		t.Fatalf("nil relay FlushOnce = %d, want 0", n)
	}

	// 2. Publishes canonical Envelope JSON, Nats-Msg-Id=EventID, traceparent, and nats.Context PubOpt.
	st := &stubOutboxStore{
		batches: [][]store.OutboxEvent{{
			{
				ID:          uuid.NewString(),
				EventID:     "evt-ship-1",
				Subject:     SubjectShipmentUpdated,
				AggregateID: "ord-1",
				Payload:     payload,
				TraceID:     "4bf92f3577b34da6a3ce929d0e0e4736",
				Traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
				CreatedAt:   t0,
			},
		}},
	}
	pub := &recordingPublisher{}
	relay := NewOutboxRelay(st, pub, 50*time.Millisecond, discardLog)
	if n := relay.FlushOnce(context.Background()); n != 1 {
		t.Fatalf("FlushOnce = %d, want 1", n)
	}
	if !pub.sawPubOpt {
		t.Fatalf("expected PublishMsg to receive nats.Context timeout PubOpt")
	}
	if len(pub.msgs) != 1 {
		t.Fatalf("published msgs = %d, want 1", len(pub.msgs))
	}
	msg := pub.msgs[0]
	if msg.Subject != SubjectShipmentUpdated {
		t.Fatalf("subject = %q, want %q", msg.Subject, SubjectShipmentUpdated)
	}
	if msg.Header.Get(nats.MsgIdHdr) != "evt-ship-1" {
		t.Fatalf("Nats-Msg-Id = %q, want evt-ship-1", msg.Header.Get(nats.MsgIdHdr))
	}
	if msg.Header.Get("traceparent") != "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01" {
		t.Fatalf("traceparent = %q", msg.Header.Get("traceparent"))
	}
	var env Envelope
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if env.EventID != "evt-ship-1" || env.EventType != SubjectShipmentUpdated || env.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" || env.OccurredAt != t0.Format(time.RFC3339) {
		t.Fatalf("unexpected envelope: %+v", env)
	}

	// 3. Full batch triggers next loop iteration until drained.
	fullBatch := make([]store.OutboxEvent, 50)
	for i := range fullBatch {
		fullBatch[i] = store.OutboxEvent{
			ID: uuid.NewString(), EventID: fmt.Sprintf("evt-full-%02d", i),
			Subject: SubjectShipmentUpdated, Payload: payload, CreatedAt: t0,
		}
	}
	tailBatch := []store.OutboxEvent{{
		ID: uuid.NewString(), EventID: "evt-tail-1",
		Subject: SubjectShipmentUpdated, Payload: payload, CreatedAt: t0,
	}}
	stMulti := &stubOutboxStore{batches: [][]store.OutboxEvent{fullBatch, tailBatch}}
	pubMulti := &recordingPublisher{}
	relayMulti := NewOutboxRelay(stMulti, pubMulti, 50*time.Millisecond, discardLog)
	if n := relayMulti.FlushOnce(context.Background()); n != 51 || stMulti.calls != 2 {
		t.Fatalf("multi-batch drain = (%d, calls=%d), want (51, calls=2)", n, stMulti.calls)
	}

	// 4. Error from ProcessPendingOutbox stops loop cleanly.
	stErr := &stubOutboxStore{retErr: nats.ErrTimeout}
	if n := NewOutboxRelay(stErr, &recordingPublisher{}, 50*time.Millisecond, discardLog).FlushOnce(context.Background()); n != 0 {
		t.Fatalf("error FlushOnce = %d, want 0", n)
	}
}

// TestOutboxRelay_W3A_D9_RealPG_ConcurrentRelaysAndFailureHandling wires OutboxRelay + PGStore
// against an isolated temporary Postgres database when TEST_SHIPPING_DB_URL is set.
func TestOutboxRelay_W3A_D9_RealPG_ConcurrentRelaysAndFailureHandling(t *testing.T) {
	pool := openIsolatedEventsTestPool(t)
	ctx := context.Background()

	pgStore := store.NewPGStore(pool)
	if err := pgStore.MigrateAndSeed(ctx); err != nil {
		t.Fatalf("MigrateAndSeed: %v", err)
	}

	clean := func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM outbox_events`); err != nil {
			t.Fatalf("clean outbox_events: %v", err)
		}
	}
	clean()
	t.Cleanup(clean)

	discardLog := slog.New(slog.NewTextHandler(io.Discard, nil))
	insert := func(eventID, aggID, status string, at time.Time) {
		t.Helper()
		raw, _ := json.Marshal(ShipmentUpdatedPayload{
			ShipmentID: "sh-" + aggID, OrderID: aggID, ShopID: "shop-1", CarrierCode: "ghn",
			TrackingCode: "TRK-" + aggID, Status: status, Direction: store.DirectionForward,
		})
		if _, err := pool.Exec(ctx, `
			INSERT INTO outbox_events (id, event_id, subject, aggregate_id, payload, created_at)
			VALUES ($1, $2, $3, $4, $5::jsonb, $6)
		`, uuid.NewString(), eventID, SubjectShipmentUpdated, aggID, string(raw), at); err != nil {
			t.Fatalf("insert outbox %s: %v", eventID, err)
		}
	}

	base := time.Now().UTC().Add(-2 * time.Minute)

	// 1. Two concurrent OutboxRelay workers must publish every event exactly once (zero duplicates).
	const count = 12
	for i := 0; i < count; i++ {
		insert(fmt.Sprintf("relay-conc-%02d", i), fmt.Sprintf("ord-relay-%d", i%3), "delivering", base.Add(time.Duration(i)*time.Second))
	}
	pubConc := &recordingPublisher{}
	relay1 := NewOutboxRelay(pgStore, pubConc, 50*time.Millisecond, discardLog)
	relay2 := NewOutboxRelay(pgStore, pubConc, 50*time.Millisecond, discardLog)

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); <-start; relay1.FlushOnce(ctx) }()
	go func() { defer wg.Done(); <-start; relay2.FlushOnce(ctx) }()
	close(start)
	wg.Wait()

	// Ensure drained if one relay held remaining events briefly.
	relay1.FlushOnce(ctx)

	seen := make(map[string]int, len(pubConc.msgs))
	for _, m := range pubConc.msgs {
		id := m.Header.Get(nats.MsgIdHdr)
		seen[id]++
		if seen[id] > 1 {
			t.Fatalf("duplicate publish detected for Nats-Msg-Id=%s", id)
		}
	}
	if len(seen) != count {
		t.Fatalf("expected %d unique published messages, got %d", count, len(seen))
	}
	if !pubConc.sawPubOpt {
		t.Fatalf("expected OutboxRelay to pass nats.Context timeout PubOpt to PublishMsg")
	}

	// 2. Non-infra error on A1 blocks A2 while allowing B1, then retry drains [A1, A2] in order.
	clean()
	insert("relay-a1", "ord-rel-A", "delivering", base)
	insert("relay-b1", "ord-rel-B", "delivering", base.Add(time.Second))
	insert("relay-a2", "ord-rel-A", "delivered", base.Add(2*time.Second))

	pubOrder := &recordingPublisher{
		failByID: map[string]error{"relay-a1": errors.New("simulated per-event failure")},
	}
	relayOrder := NewOutboxRelay(pgStore, pubOrder, 50*time.Millisecond, discardLog)
	if n := relayOrder.FlushOnce(ctx); n != 1 {
		t.Fatalf("first flush published=%d, want 1 (relay-b1 only)", n)
	}
	if len(pubOrder.msgs) != 1 || pubOrder.msgs[0].Header.Get(nats.MsgIdHdr) != "relay-b1" {
		t.Fatalf("first flush msgs=%v, want [relay-b1]", pubOrder.msgs)
	}

	pubOrder.mu.Lock()
	pubOrder.failByID = nil
	pubOrder.mu.Unlock()
	// While relay-a1 is in 2s non-infra backoff, immediate flush must return 0.
	if n := relayOrder.FlushOnce(ctx); n != 0 {
		t.Fatalf("immediate retry during backoff published=%d, want 0", n)
	}
	if _, err := pool.Exec(ctx, `UPDATE outbox_events SET claimed_until = NOW() - INTERVAL '1 second' WHERE event_id = 'relay-a1'`); err != nil {
		t.Fatalf("expire relay-a1 backoff: %v", err)
	}
	if n := relayOrder.FlushOnce(ctx); n != 2 {
		t.Fatalf("retry flush after backoff published=%d, want 2", n)
	}
	gotIDs := []string{
		pubOrder.msgs[0].Header.Get(nats.MsgIdHdr),
		pubOrder.msgs[1].Header.Get(nats.MsgIdHdr),
		pubOrder.msgs[2].Header.Get(nats.MsgIdHdr),
	}
	if fmt.Sprint(gotIDs) != "[relay-b1 relay-a1 relay-a2]" {
		t.Fatalf("published order = %v, want [relay-b1 relay-a1 relay-a2]", gotIDs)
	}

	// 3. NATS infra error (ErrTimeout) aborts batch after 1 attempt and releases remaining events immediately.
	clean()
	insert("relay-infra-1", "ord-inf-1", "delivering", base)
	insert("relay-infra-2", "ord-inf-2", "delivering", base.Add(time.Second))
	insert("relay-infra-3", "ord-inf-3", "delivering", base.Add(2*time.Second))

	pubInfra := &recordingPublisher{globalErr: nats.ErrTimeout}
	relayInfra := NewOutboxRelay(pgStore, pubInfra, 50*time.Millisecond, discardLog)
	if n := relayInfra.FlushOnce(ctx); n != 0 {
		t.Fatalf("infra error flush published=%d, want 0", n)
	}
	if pubInfra.attemptCount != 1 {
		t.Fatalf("on nats.ErrTimeout expected 1 attempt (abort batch), got %d", pubInfra.attemptCount)
	}

	// After NATS recovers, all 3 events immediately claim and publish in order.
	pubInfra.mu.Lock()
	pubInfra.globalErr = nil
	pubInfra.mu.Unlock()
	if n := relayInfra.FlushOnce(ctx); n != 3 {
		t.Fatalf("recovery after nats.ErrTimeout published=%d, want 3", n)
	}

	// 4. D9-bis (e): NATS down -> relay Run backoff caps claim/publish attempts even under rapid Wake() calls.
	clean()
	insert("relay-natsdown-1", "ord-natsdown", "delivering", base)
	pubDown := &recordingPublisher{globalErr: nats.ErrDisconnected}
	relayDown := NewOutboxRelay(pgStore, pubDown, 25*time.Millisecond, discardLog)

	runCtx, runCancel := context.WithTimeout(ctx, 1200*time.Millisecond)
	defer runCancel()
	var downWG sync.WaitGroup
	downWG.Add(2)
	go func() {
		defer downWG.Done()
		relayDown.Run(runCtx)
	}()
	go func() {
		defer downWG.Done()
		tk := time.NewTicker(20 * time.Millisecond)
		defer tk.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-tk.C:
				relayDown.Wake()
			}
		}
	}()
	downWG.Wait()

	pubDown.mu.Lock()
	downAttempts := pubDown.attemptCount
	pubDown.mu.Unlock()
	if downAttempts < 1 || downAttempts > 6 {
		t.Fatalf("NATS down for 1.2s with 20ms Wake(): expected 1..6 backed-off attempts, got %d", downAttempts)
	}
}
