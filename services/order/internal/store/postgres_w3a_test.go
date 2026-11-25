package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	"github.com/nexusmart/core/pkg/common/jetstream"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/order/internal/db"
	"github.com/nexusmart/core/services/order/internal/events"
	"github.com/nexusmart/core/services/order/internal/statemachine"
	"github.com/nexusmart/core/services/order/migrations"
)

func insertOutboxEventRaw(t *testing.T, st *PostgresStore, id, eventType, orderID string) {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"order_id": orderID, "event_id": id})
	if err := st.queries.InsertOutboxEvent(context.Background(), db.InsertOutboxEventParams{
		ID:          id,
		EventType:   eventType,
		TraceID:     "tr-" + id,
		PayloadJSON: payload,
	}); err != nil {
		t.Fatalf("InsertOutboxEvent(%s): %v", id, err)
	}
}

// TestPostgresW3A_D9_OutboxLeaseAndOrdering verifies D9 against real PostgreSQL:
//  1. Publish runs OUTSIDE any DB transaction / row lock (concurrent FOR UPDATE NOWAIT on claimed row succeeds).
//  2. Two concurrent workers never double-claim and never overtake an earlier unpublished event of the same aggregate.
//  3. Failure on an event blocks later events of the SAME aggregate in the batch (claim released, attempts=0) while
//     publishing events of OTHER aggregates normally.
//  4. Lease expiration allows another worker to reclaim; the stalled worker's stale claim_token mark affects 0 rows.
func TestPostgresW3A_D9_OutboxLeaseAndOrdering(t *testing.T) {
	t.Run("PublishRunsOutsideTransaction_NoRowLockHeldDuringPublish", func(t *testing.T) {
		st, pool := openTempPostgresStore(t)
		ctx := context.Background()

		evID := "ob-nolock-1"
		insertOutboxEventRaw(t, st, evID, events.SubjectOrderCreated, "ord-nolock-1")

		var lockCheckErr error
		var seenClaimedUntil *time.Time
		var seenClaimToken string

		n, err := st.ProcessPendingOutbox(ctx, 10, func(pubCtx context.Context, ev db.OutboxEvent) error {
			// Verify from a SECOND DB connection that:
			//   (a) claimed_until and claim_token are ALREADY committed in Postgres, AND
			//   (b) NO row lock is held on the claimed row (FOR UPDATE NOWAIT succeeds immediately!).
			tx, bErr := pool.Begin(pubCtx)
			if bErr != nil {
				lockCheckErr = bErr
				return nil
			}
			defer func() { _ = tx.Rollback(pubCtx) }()
			lockCheckErr = tx.QueryRow(
				pubCtx,
				`SELECT claimed_until, claim_token FROM outbox_events WHERE id = $1 FOR UPDATE NOWAIT`,
				ev.ID,
			).Scan(&seenClaimedUntil, &seenClaimToken)
			return nil
		})
		if err != nil || n != 1 {
			t.Fatalf("ProcessPendingOutbox = %d, %v; want 1, nil", n, err)
		}
		if lockCheckErr != nil {
			t.Fatalf("D9 violation: row lock was held during publishFn (TX stayed open!): %v", lockCheckErr)
		}
		if seenClaimedUntil == nil || !seenClaimedUntil.After(time.Now().UTC()) || seenClaimToken == "" {
			t.Fatalf("D9 violation: expected committed claimed_until in future and non-empty claim_token during publishFn, got until=%v tok=%q",
				seenClaimedUntil, seenClaimToken)
		}

		// After publish completes, published_at is set and claim lease/token are cleared.
		var pubAt *time.Time
		var afterTok string
		var attempts int32
		if err := pool.QueryRow(ctx, `SELECT published_at, claim_token, attempts FROM outbox_events WHERE id = $1`, evID).
			Scan(&pubAt, &afterTok, &attempts); err != nil {
			t.Fatalf("query outbox after publish: %v", err)
		}
		if pubAt == nil || afterTok != "" || attempts != 1 {
			t.Fatalf("unexpected post-publish row: pubAt=%v token=%q attempts=%d", pubAt, afterTok, attempts)
		}
	})

	t.Run("ConcurrentWorkers_PreserveSameAggregateOrderAndAllowOtherAggregate", func(t *testing.T) {
		st, pool := openTempPostgresStore(t)
		ctx := context.Background()

		// Insert two events for ord-A inside the SAME transaction (identical NOW() created_at, distinct seq!)
		// plus one event for ord-B.
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin seed tx: %v", err)
		}
		q := st.queries.WithTx(tx)
		pA1, _ := json.Marshal(map[string]string{"order_id": "ord-A", "step": "1"})
		pA2, _ := json.Marshal(map[string]string{"order_id": "ord-A", "step": "2"})
		pB1, _ := json.Marshal(map[string]string{"order_id": "ord-B", "step": "1"})
		_ = q.InsertOutboxEvent(ctx, db.InsertOutboxEventParams{ID: "ev-a-1", EventType: events.SubjectOrderCreated, PayloadJSON: pA1})
		_ = q.InsertOutboxEvent(ctx, db.InsertOutboxEventParams{ID: "ev-a-2", EventType: events.SubjectOrderConfirmed, PayloadJSON: pA2})
		_ = q.InsertOutboxEvent(ctx, db.InsertOutboxEventParams{ID: "ev-b-1", EventType: events.SubjectOrderCreated, PayloadJSON: pB1})
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit seed tx: %v", err)
		}

		worker1InPublish := make(chan struct{})
		releaseWorker1 := make(chan struct{})
		var mu sync.Mutex
		var publishedOrder []string

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Worker 1 claims ONLY ev-a-1 (limit=1) and blocks inside publishFn.
			_, wErr := st.ProcessPendingOutbox(ctx, 1, func(_ context.Context, ev db.OutboxEvent) error {
				close(worker1InPublish)
				<-releaseWorker1
				mu.Lock()
				publishedOrder = append(publishedOrder, ev.ID)
				mu.Unlock()
				return nil
			})
			if wErr != nil {
				t.Errorf("worker 1 error: %v", wErr)
			}
		}()

		<-worker1InPublish

		// While Worker 1 holds lease on ev-a-1, Worker 2 scans limit=10:
		// MUST skip ev-a-2 (earlier unpublished ev-a-1 for ord-A is actively leased) and MUST publish ev-b-1!
		n2, err := st.ProcessPendingOutbox(ctx, 10, func(_ context.Context, ev db.OutboxEvent) error {
			mu.Lock()
			publishedOrder = append(publishedOrder, ev.ID)
			mu.Unlock()
			return nil
		})
		if err != nil {
			t.Fatalf("worker 2 error: %v", err)
		}
		if n2 != 1 {
			t.Fatalf("D9 violation: worker 2 should publish ONLY ev-b-1 while ev-a-1 is leased, got n=%d order=%v", n2, publishedOrder)
		}

		close(releaseWorker1)
		wg.Wait()

		// Now drain remaining ev-a-2.
		n3, err := st.ProcessPendingOutbox(ctx, 10, func(_ context.Context, ev db.OutboxEvent) error {
			mu.Lock()
			publishedOrder = append(publishedOrder, ev.ID)
			mu.Unlock()
			return nil
		})
		if err != nil || n3 != 1 {
			t.Fatalf("drain ev-a-2 = %d, %v; want 1, nil", n3, err)
		}

		want := []string{"ev-b-1", "ev-a-1", "ev-a-2"}
		if fmt.Sprint(publishedOrder) != fmt.Sprint(want) {
			t.Fatalf("D9 ordering violation: published=%v, want %v", publishedOrder, want)
		}
	})

	t.Run("FailureBlocksSameAggregateInBatch_AllowsOtherAggregateAndPreservesAttempts", func(t *testing.T) {
		st, pool := openTempPostgresStore(t)
		ctx := context.Background()

		insertOutboxEventRaw(t, st, "ev-fail-a1", events.SubjectOrderCreated, "ord-fail-A")
		insertOutboxEventRaw(t, st, "ev-fail-a2", events.SubjectOrderConfirmed, "ord-fail-A")
		insertOutboxEventRaw(t, st, "ev-ok-b1", events.SubjectOrderCreated, "ord-ok-B")

		var seenFirstPass []string
		n1, err := st.ProcessPendingOutbox(ctx, 10, func(_ context.Context, ev db.OutboxEvent) error {
			seenFirstPass = append(seenFirstPass, ev.ID)
			if ev.ID == "ev-fail-a1" {
				return errors.New("nats transient error")
			}
			return nil
		})
		if err != nil || n1 != 1 {
			t.Fatalf("first pass = %d, %v; want 1, nil", n1, err)
		}
		// ev-fail-a2 MUST NOT have been passed to publishFn because ev-fail-a1 failed for ord-fail-A!
		if fmt.Sprint(seenFirstPass) != fmt.Sprint([]string{"ev-fail-a1", "ev-ok-b1"}) {
			t.Fatalf("D9 violation: expected [ev-fail-a1 ev-ok-b1] on first pass, got %v", seenFirstPass)
		}

		// Verify DB state: ev-fail-a1 attempts=1 last_error set + backoff claimed_until > NOW();
		// ev-fail-a2 attempts=0 claim released; ev-ok-b1 published!
		var a1Att, a2Att int32
		var a1Err, a1Tok, a2Tok string
		var a1Until, a2Until *time.Time
		_ = pool.QueryRow(ctx, `SELECT attempts, last_error, claim_token, claimed_until FROM outbox_events WHERE id = 'ev-fail-a1'`).Scan(&a1Att, &a1Err, &a1Tok, &a1Until)
		_ = pool.QueryRow(ctx, `SELECT attempts, claim_token, claimed_until FROM outbox_events WHERE id = 'ev-fail-a2'`).Scan(&a2Att, &a2Tok, &a2Until)
		if a1Att != 1 || a1Err == "" || a1Tok != "" || a1Until == nil || !a1Until.After(time.Now().UTC()) || a2Att != 0 || a2Tok != "" || a2Until != nil {
			t.Fatalf("unexpected failed/skipped state: a1=(att=%d err=%q tok=%q until=%v) a2=(att=%d tok=%q until=%v)", a1Att, a1Err, a1Tok, a1Until, a2Att, a2Tok, a2Until)
		}

		// While ev-fail-a1 is in backoff, neither ev-fail-a1 nor ev-fail-a2 may be claimed!
		if nBackoff, bErr := st.ProcessPendingOutbox(ctx, 10, func(_ context.Context, ev db.OutboxEvent) error {
			t.Errorf("unexpected publish during backoff: %s", ev.ID)
			return nil
		}); bErr != nil || nBackoff != 0 {
			t.Fatalf("during non-infra backoff want 0 claimed, got %d err=%v", nBackoff, bErr)
		}

		// Expire backoff window in Postgres -> second pass recovers and publishes ev-fail-a1 THEN ev-fail-a2 in exact order!
		if _, err := pool.Exec(ctx, `UPDATE outbox_events SET claimed_until = NOW() - INTERVAL '1 second' WHERE id = 'ev-fail-a1'`); err != nil {
			t.Fatalf("expire backoff: %v", err)
		}
		var seenSecondPass []string
		n2, err := st.ProcessPendingOutbox(ctx, 10, func(_ context.Context, ev db.OutboxEvent) error {
			seenSecondPass = append(seenSecondPass, ev.ID)
			return nil
		})
		if err != nil || n2 != 2 {
			t.Fatalf("second pass = %d, %v; want 2, nil", n2, err)
		}
		if fmt.Sprint(seenSecondPass) != fmt.Sprint([]string{"ev-fail-a1", "ev-fail-a2"}) {
			t.Fatalf("second pass order = %v, want [ev-fail-a1 ev-fail-a2]", seenSecondPass)
		}
	})

	t.Run("LeaseExpiryReclaimAndStaleClaimTokenGuard", func(t *testing.T) {
		st, pool := openTempPostgresStore(t)
		st.SetOutboxLease(3 * time.Second)
		st.SetOutboxPublishTimeout(1 * time.Second)
		ctx := context.Background()

		insertOutboxEventRaw(t, st, "ev-lease-1", events.SubjectOrderCreated, "ord-lease-1")

		stalledStarted := make(chan struct{})
		releaseStalled := make(chan struct{})
		var stalledN int
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			var err error
			stalledN, err = st.ProcessPendingOutbox(ctx, 5, func(_ context.Context, _ db.OutboxEvent) error {
				close(stalledStarted)
				// Intentionally ignore pubCtx to simulate a GC pause / stalled process outliving the lease.
				<-releaseStalled
				return nil
			})
			if err != nil {
				t.Errorf("stalled worker err: %v", err)
			}
		}()

		<-stalledStarted
		// Immediately before lease expiry, Worker 2 sees 0 claimable rows.
		if nImm, err := st.ProcessPendingOutbox(ctx, 5, func(_ context.Context, _ db.OutboxEvent) error { return nil }); err != nil || nImm != 0 {
			t.Fatalf("expected 0 claimed while lease active, got %d err=%v", nImm, err)
		}

		// Expire the 3s lease deterministically in Postgres -> Worker 2 reclaims with a fresh claim_token and publishes!
		if _, err := pool.Exec(ctx, `UPDATE outbox_events SET claimed_until = NOW() - INTERVAL '1 second' WHERE id = 'ev-lease-1'`); err != nil {
			t.Fatalf("expire lease in postgres: %v", err)
		}
		var reclaimed atomic.Int32
		n2, err := st.ProcessPendingOutbox(ctx, 5, func(_ context.Context, ev db.OutboxEvent) error {
			if ev.ID == "ev-lease-1" {
				reclaimed.Add(1)
			}
			return nil
		})
		if err != nil || n2 != 1 || reclaimed.Load() != 1 {
			t.Fatalf("expected reclaim after lease expiry, got n=%d reclaimed=%d err=%v", n2, reclaimed.Load(), err)
		}

		// Stalled worker wakes up with its STALE claim_token: MarkOutboxEventPublished affects 0 rows (stalledN == 0, attempts stays 1).
		close(releaseStalled)
		wg.Wait()
		if stalledN != 0 {
			t.Fatalf("D9 stale token guard violation: stalled worker counted published=%d after losing lease", stalledN)
		}
		var attempts int32
		_ = pool.QueryRow(ctx, `SELECT attempts FROM outbox_events WHERE id = 'ev-lease-1'`).Scan(&attempts)
		if attempts != 1 {
			t.Fatalf("D9 stale token guard violation: attempts=%d, want 1", attempts)
		}
	})

	t.Run("SlowPublisherRespectsLeaseBudgetAndReleasesRemaining", func(t *testing.T) {
		st, pool := openTempPostgresStore(t)
		st.SetOutboxLease(15 * time.Second)
		st.SetOutboxPublishTimeout(5 * time.Second)

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

		ctx := context.Background()
		for i := 0; i < 5; i++ {
			insertOutboxEventRaw(t, st, fmt.Sprintf("ev-pg-slow-%d", i), events.SubjectOrderCreated, "ord-pg-slow")
		}

		// 4s per publish on 15s lease with 5s pubTimeout -> publishes 0, 1, 2 (at t=0,4,8),
		// then stops before 3 (at t=12s, rem=3s < 5s) and releases rows 3 & 4 in Postgres!
		var firstPass []string
		n1, err := st.ProcessPendingOutbox(ctx, 10, func(_ context.Context, ev db.OutboxEvent) error {
			firstPass = append(firstPass, ev.ID)
			advance(4 * time.Second)
			return nil
		})
		if err != nil || n1 != 3 {
			t.Fatalf("D9 P1 Postgres lease budget violation: want n=3 err=nil, got n=%d err=%v seen=%v", n1, err, firstPass)
		}
		for _, id := range []string{"ev-pg-slow-3", "ev-pg-slow-4"} {
			var pubAt, claimedUntil *time.Time
			var tok string
			var attempts int32
			if err := pool.QueryRow(ctx, `SELECT published_at, claimed_until, claim_token, attempts FROM outbox_events WHERE id = $1`, id).
				Scan(&pubAt, &claimedUntil, &tok, &attempts); err != nil {
				t.Fatalf("query remaining %s: %v", id, err)
			}
			if pubAt != nil || claimedUntil != nil || tok != "" || attempts != 0 {
				t.Fatalf("remaining event %s must be released cleanly in Postgres: pubAt=%v until=%v tok=%q att=%d", id, pubAt, claimedUntil, tok, attempts)
			}
		}

		// Second pass drains remaining 2 events in strict seq order.
		var secondPass []string
		n2, err := st.ProcessPendingOutbox(ctx, 10, func(_ context.Context, ev db.OutboxEvent) error {
			secondPass = append(secondPass, ev.ID)
			return nil
		})
		if err != nil || n2 != 2 {
			t.Fatalf("second pass = %d, %v; want 2, nil", n2, err)
		}
		if fmt.Sprint(secondPass) != fmt.Sprint([]string{"ev-pg-slow-3", "ev-pg-slow-4"}) {
			t.Fatalf("second pass order = %v, want [ev-pg-slow-3 ev-pg-slow-4]", secondPass)
		}
	})

	t.Run("TwoConcurrentWorkersSameAggregateSlowPublisher", func(t *testing.T) {
		// D9-bis §7: second-scale lease (>= 3s), 1s publish timeout, slow publish <= 400ms (0 flake under load).
		st, pool := openTempPostgresStore(t)
		st.SetOutboxLease(3 * time.Second)
		st.SetOutboxPublishTimeout(1 * time.Second)

		const totalEvents = 10
		for i := 0; i < totalEvents; i++ {
			insertOutboxEventRaw(t, st, fmt.Sprintf("ev-pg-race-%02d", i), events.SubjectOrderCreated, "ord-pg-race")
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

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		log := logger.New("order-pg-test", logger.WithWriter(io.Discard))
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
				var remaining int
				if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM outbox_events WHERE published_at IS NULL`).Scan(&remaining); err == nil && remaining == 0 {
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
			t.Fatalf("D9 P1 Postgres violation: two workers published events of same aggregate concurrently!")
		}
		if len(publishedOrder) != totalEvents {
			t.Fatalf("expected %d unique publishes, got %d: %v", totalEvents, len(publishedOrder), publishedOrder)
		}
		for i := 0; i < totalEvents; i++ {
			want := fmt.Sprintf("ev-pg-race-%02d", i)
			if publishedOrder[i] != want {
				t.Fatalf("publishedOrder[%d] = %q, want %q (all=%v)", i, publishedOrder[i], want, publishedOrder)
			}
		}
	})

	t.Run("NATSTimeoutAndInfraErrorAbortBatch", func(t *testing.T) {
		t.Run("ErrTimeoutViaRelayAbortsAndReleasesRest", func(t *testing.T) {
			st, pool := openTempPostgresStore(t)
			ctx := context.Background()
			for i := 0; i < 5; i++ {
				insertOutboxEventRaw(t, st, fmt.Sprintf("ev-pg-to-%d", i), events.SubjectOrderCreated, fmt.Sprintf("ord-pg-to-%d", i))
			}

			var calls atomic.Int32
			pub := &stubOutboxPublisher{
				onPublish: func(_ *nats.Msg, opts ...nats.PubOpt) error {
					calls.Add(1)
					if len(opts) == 0 {
						t.Errorf("expected nats.Context(pubCtx) PubOpt")
					}
					return nats.ErrTimeout
				},
			}
			relay := events.NewOutboxRelay(st, pub, logger.New("order-pg-test", logger.WithWriter(io.Discard)), 100*time.Millisecond, nil)
			if n := relay.FlushOnce(ctx); n != 0 {
				t.Fatalf("expected 0 published on nats.ErrTimeout, got %d", n)
			}
			if calls.Load() != 1 {
				t.Fatalf("D9 P1 Postgres infra abort violation: expected 1 attempt before aborting batch, got %d", calls.Load())
			}

			var att0 int32
			var err0 string
			var until0 *time.Time
			_ = pool.QueryRow(ctx, `SELECT attempts, last_error, claimed_until FROM outbox_events WHERE id = 'ev-pg-to-0'`).Scan(&att0, &err0, &until0)
			if att0 != 1 || err0 == "" || until0 != nil {
				t.Fatalf("first infra-failed event must have attempts=1, error set, claimed_until=NULL: att=%d err=%q until=%v", att0, err0, until0)
			}
			for i := 1; i < 5; i++ {
				id := fmt.Sprintf("ev-pg-to-%d", i)
				var att int32
				var tok string
				var until *time.Time
				_ = pool.QueryRow(ctx, `SELECT attempts, claim_token, claimed_until FROM outbox_events WHERE id = $1`, id).Scan(&att, &tok, &until)
				if att != 0 || tok != "" || until != nil {
					t.Fatalf("remaining event %s must be released untouched: att=%d tok=%q until=%v", id, att, tok, until)
				}
			}
		})

		t.Run("HangingPublishHitsPublishTimeoutAndAbortsBatch", func(t *testing.T) {
			st, pool := openTempPostgresStore(t)
			st.SetOutboxLease(3 * time.Second)
			st.SetOutboxPublishTimeout(1 * time.Second)
			ctx := context.Background()
			for i := 0; i < 4; i++ {
				insertOutboxEventRaw(t, st, fmt.Sprintf("ev-pg-hang-%d", i), events.SubjectOrderCreated, fmt.Sprintf("ord-pg-hang-%d", i))
			}

			var calls atomic.Int32
			n, err := st.ProcessPendingOutbox(ctx, 10, func(pubCtx context.Context, _ db.OutboxEvent) error {
				calls.Add(1)
				if _, ok := pubCtx.Deadline(); !ok {
					t.Errorf("expected pubCtx deadline")
				}
				<-pubCtx.Done()
				return pubCtx.Err()
			})
			if n != 0 || !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("expected n=0 err=DeadlineExceeded, got n=%d err=%v", n, err)
			}
			if calls.Load() != 1 {
				t.Fatalf("expected hanging timeout to abort after 1st event, got %d calls", calls.Load())
			}
			var untouched int
			_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM outbox_events WHERE attempts = 0 AND claim_token = '' AND claimed_until IS NULL`).Scan(&untouched)
			if untouched != 3 {
				t.Fatalf("expected 3 remaining rows cleanly released in Postgres, got %d", untouched)
			}
		})
	})

	// 10(d) D9-bis §1 Real Postgres: created_at inverted vs seq (simulating TX2 BEGIN < TX1 BEGIN but TX1 locked first).
	t.Run("10d_CreatedAtInvertedVsSeq_RealPostgres", func(t *testing.T) {
		st, pool := openTempPostgresStore(t)
		ctx := context.Background()

		insertOutboxEventRaw(t, st, "ev-inv-shipped", events.SubjectOrderShipped, "ord-inv-pg")
		insertOutboxEventRaw(t, st, "ev-inv-delivered", events.SubjectOrderDelivered, "ord-inv-pg")
		// Invert created_at: lower-seq ev-inv-shipped gets LATER created_at, higher-seq ev-inv-delivered gets EARLIER created_at!
		if _, err := pool.Exec(ctx, `UPDATE outbox_events SET created_at = NOW() + INTERVAL '5 minutes' WHERE id = 'ev-inv-shipped'`); err != nil {
			t.Fatalf("set later created_at: %v", err)
		}
		if _, err := pool.Exec(ctx, `UPDATE outbox_events SET created_at = NOW() - INTERVAL '5 minutes' WHERE id = 'ev-inv-delivered'`); err != nil {
			t.Fatalf("set earlier created_at: %v", err)
		}

		// Even with batch limit=1 (proving SQL ORDER BY seq ASC, not only Go sort!), ev-inv-shipped MUST come first!
		var seen []string
		for pass := 0; pass < 2; pass++ {
			n, err := st.ProcessPendingOutbox(ctx, 1, func(_ context.Context, ev db.OutboxEvent) error {
				seen = append(seen, ev.ID)
				return nil
			})
			if err != nil || n != 1 {
				t.Fatalf("pass %d = %d, %v; want 1, nil", pass, n, err)
			}
		}
		if fmt.Sprint(seen) != "[ev-inv-shipped ev-inv-delivered]" {
			t.Fatalf("D9-bis §1 Postgres violation (10d): expected [ev-inv-shipped ev-inv-delivered], got %v", seen)
		}
	})

	// 10(e) D9-bis §2 + §4 Real Postgres: 50 poison events OR 1 poison + 49 same-order events do NOT starve other orders;
	// poison event backs off min(2^attempts, 60)s, logs ERROR at attempts>=10 without payload, and is NEVER dead-lettered.
	t.Run("10e_PoisonAnd50SameOrderDoNotStarveOtherOrders_RealPostgres", func(t *testing.T) {
		t.Run("50DistinctPoisonEventsThenHealthyOrder", func(t *testing.T) {
			st, pool := openTempPostgresStore(t)
			var logBuf bytes.Buffer
			st.SetLogger(slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
			ctx := context.Background()

			const secret = "TOP-SECRET-ORDER-PAYLOAD"
			for i := 0; i < 50; i++ {
				payload, _ := json.Marshal(map[string]string{"order_id": fmt.Sprintf("ord-p50-%02d", i), "secret": secret})
				if err := st.queries.InsertOutboxEvent(ctx, db.InsertOutboxEventParams{
					ID: fmt.Sprintf("ev-p50-%02d", i), EventType: events.SubjectOrderCreated, PayloadJSON: payload,
				}); err != nil {
					t.Fatalf("insert poison %d: %v", i, err)
				}
			}
			insertOutboxEventRaw(t, st, "ev-healthy-after-50", events.SubjectOrderCreated, "ord-healthy-50")

			pubFn := func(_ context.Context, ev db.OutboxEvent) error {
				if strings.HasPrefix(ev.ID, "ev-p50-") {
					return errors.New("non-infra marshal error")
				}
				return nil
			}
			// Pass 1 (limit=50): all 50 poison events fail into backoff (claimed_until > NOW()).
			n1, err := st.ProcessPendingOutbox(ctx, 50, pubFn)
			if err != nil || n1 != 0 {
				t.Fatalf("pass 1 = %d, %v; want 0, nil", n1, err)
			}
			// Pass 2 (limit=50): MUST immediately skip the 50 backed-off poison rows and publish ev-healthy-after-50!
			var pass2Seen []string
			n2, err := st.ProcessPendingOutbox(ctx, 50, func(_ context.Context, ev db.OutboxEvent) error {
				pass2Seen = append(pass2Seen, ev.ID)
				return pubFn(ctx, ev)
			})
			if err != nil || n2 != 1 || fmt.Sprint(pass2Seen) != "[ev-healthy-after-50]" {
				t.Fatalf("D9-bis §4 Postgres starvation violation (50 poison): n2=%d err=%v seen=%v", n2, err, pass2Seen)
			}

			// Push ev-p50-00 from attempts=9 -> attempts=10 -> ERROR log with event_id + event_type, NO payload, 0 DLQ!
			if _, err := pool.Exec(ctx, `UPDATE outbox_events SET attempts = $1, claimed_until = NOW() - INTERVAL '1 second' WHERE id = 'ev-p50-00'`, jetstream.OutboxPoisonLogAttempt-1); err != nil {
				t.Fatalf("prime attempts=9: %v", err)
			}
			if _, err := st.ProcessPendingOutbox(ctx, 10, pubFn); err != nil {
				t.Fatalf("attempt 10 pass: %v", err)
			}
			logged := logBuf.String()
			if !strings.Contains(logged, `"level":"ERROR"`) || !strings.Contains(logged, "ev-p50-00") || !strings.Contains(logged, events.SubjectOrderCreated) {
				t.Fatalf("D9-bis §4 Postgres ERROR log missing event_id/event_type: %q", logged)
			}
			if strings.Contains(logged, secret) {
				t.Fatalf("D9-bis §4 Postgres ERROR log leaked payload: %q", logged)
			}
			var dlqCount int
			_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM dead_letter_events`).Scan(&dlqCount)
			if dlqCount != 0 {
				t.Fatalf("D9-bis §4 violation: outbox poison event must NOT enter dead_letter_events, got %d", dlqCount)
			}
		})

		t.Run("1PoisonPlus49SameOrderThenHealthyOrder", func(t *testing.T) {
			st, pool := openTempPostgresStore(t)
			ctx := context.Background()

			insertOutboxEventRaw(t, st, "ev-same-poison-00", events.SubjectOrderCreated, "ord-blocked-50")
			for i := 1; i < 50; i++ {
				insertOutboxEventRaw(t, st, fmt.Sprintf("ev-same-tail-%02d", i), events.SubjectOrderConfirmed, "ord-blocked-50")
			}
			insertOutboxEventRaw(t, st, "ev-other-healthy", events.SubjectOrderCreated, "ord-other-ok")

			// Pass 1 (limit=50): claims all 50 of ord-blocked-50, fails head into backoff, releases 49 tails in 1 batch query.
			n1, err := st.ProcessPendingOutbox(ctx, 50, func(_ context.Context, ev db.OutboxEvent) error {
				if ev.ID == "ev-same-poison-00" {
					return errors.New("non-infra bad event")
				}
				return nil
			})
			if err != nil || n1 != 0 {
				t.Fatalf("pass 1 = %d, %v; want 0, nil", n1, err)
			}
			// Verify all 49 tails were released cleanly with attempts=0.
			var releasedTails int
			_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM outbox_events WHERE id LIKE 'ev-same-tail-%' AND attempts = 0 AND claim_token = '' AND claimed_until IS NULL`).Scan(&releasedTails)
			if releasedTails != 49 {
				t.Fatalf("expected 49 trailing events cleanly released, got %d", releasedTails)
			}

			// Pass 2 (limit=50): NOT EXISTS inside CTE `locked` BEFORE LIMIT 50 MUST exclude all 49 tails of ord-blocked-50
			// and immediately claim & publish ev-other-healthy!
			var pass2Seen []string
			n2, err := st.ProcessPendingOutbox(ctx, 50, func(_ context.Context, ev db.OutboxEvent) error {
				pass2Seen = append(pass2Seen, ev.ID)
				return nil
			})
			if err != nil || n2 != 1 || fmt.Sprint(pass2Seen) != "[ev-other-healthy]" {
				t.Fatalf("D9-bis §2 Postgres starvation violation (1 poison + 49 same-order): n2=%d err=%v seen=%v", n2, err, pass2Seen)
			}
		})
	})

	// 10(f) D9-bis §3 Real Postgres: MarkOutboxEventPublished returns affected == 0 mid-batch ->
	// immediately stops batch and releases remaining claims in Postgres with a single UPDATE = ANY(...)!
	t.Run("10f_LostLeaseAffectedZeroMidBatch_RealPostgres", func(t *testing.T) {
		st, pool := openTempPostgresStore(t)
		ctx := context.Background()

		for i := 0; i < 3; i++ {
			insertOutboxEventRaw(t, st, fmt.Sprintf("ev-pg-lose-%d", i), events.SubjectOrderCreated, fmt.Sprintf("ord-pg-lose-%d", i))
		}

		var seen []string
		n, err := st.ProcessPendingOutbox(ctx, 10, func(_ context.Context, ev db.OutboxEvent) error {
			seen = append(seen, ev.ID)
			if ev.ID == "ev-pg-lose-0" {
				// Simulate another worker reclaiming and publishing ev-pg-lose-0 before our MarkOutboxEventPublished runs.
				if _, uErr := pool.Exec(ctx, `UPDATE outbox_events SET claim_token = 'other-worker', published_at = NOW() WHERE id = 'ev-pg-lose-0'`); uErr != nil {
					t.Errorf("steal ev-pg-lose-0: %v", uErr)
				}
			}
			return nil
		})
		if err != nil || n != 0 {
			t.Fatalf("lost-lease batch = %d, %v; want 0, nil", n, err)
		}
		if fmt.Sprint(seen) != "[ev-pg-lose-0]" {
			t.Fatalf("D9-bis §3 Postgres violation (10f): worker kept publishing after affected==0! seen=%v", seen)
		}
		for _, id := range []string{"ev-pg-lose-1", "ev-pg-lose-2"} {
			var tok string
			var until *time.Time
			var att int32
			_ = pool.QueryRow(ctx, `SELECT claim_token, claimed_until, attempts FROM outbox_events WHERE id = $1`, id).Scan(&tok, &until, &att)
			if tok != "" || until != nil || att != 0 {
				t.Fatalf("D9-bis §3 Postgres violation: remaining %s not released: tok=%q until=%v att=%d", id, tok, until, att)
			}
		}
	})

	// 10(g) D9-bis §5 Real Postgres: NATS disconnected + continuous triggerCh hammering ->
	// OutboxRelay.Run backs off (250ms -> 5s, ignoring triggerCh), capping DB claim rate and recovering once NATS returns.
	t.Run("10g_NATSDeadContinuousTriggers_BackoffCapsClaimsRealPostgres", func(t *testing.T) {
		st, pool := openTempPostgresStore(t)
		ctx := context.Background()
		insertOutboxEventRaw(t, st, "ev-pg-trig-1", events.SubjectOrderCreated, "ord-pg-trig-1")

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
		relay := events.NewOutboxRelay(counted, pub, logger.New("order-pg-test", logger.WithWriter(io.Discard)), 25*time.Millisecond, trigCh)
		runCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			defer close(done)
			relay.Run(runCtx)
		}()

		endHammer := time.Now().Add(1100 * time.Millisecond)
		for time.Now().Before(endHammer) {
			select {
			case trigCh <- struct{}{}:
			default:
			}
			time.Sleep(20 * time.Millisecond)
		}
		downClaims := counted.calls.Load()
		if downClaims < 1 || downClaims > 5 {
			cancel()
			<-done
			t.Fatalf("D9-bis §5 Postgres violation (10g): NATS dead + 20ms triggerCh caused %d claims in 1.1s (want 1..5)", downClaims)
		}

		// Bring NATS back -> relay recovers and publishes ev-pg-trig-1 in Postgres.
		natsDown.Store(false)
		waitUntil := time.Now().Add(4 * time.Second)
		var pubAt *time.Time
		for time.Now().Before(waitUntil) {
			_ = pool.QueryRow(ctx, `SELECT published_at FROM outbox_events WHERE id = 'ev-pg-trig-1'`).Scan(&pubAt)
			if pubAt != nil {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		cancel()
		<-done
		if pubAt == nil {
			t.Fatalf("10g: expected relay to recover and publish ev-pg-trig-1 once NATS returned")
		}
	})

	t.Run("P3_8_PublishTimeoutClampedBelowLease_RealPostgres", func(t *testing.T) {
		st, _ := openTempPostgresStore(t)
		st.SetOutboxLease(3 * time.Second)
		st.SetOutboxPublishTimeout(5 * time.Second) // >= lease! must clamp to < 3s
		insertOutboxEventRaw(t, st, "ev-pg-clamp-1", events.SubjectOrderCreated, "ord-pg-clamp")

		n, err := st.ProcessPendingOutbox(context.Background(), 10, func(pubCtx context.Context, _ db.OutboxEvent) error {
			dl, ok := pubCtx.Deadline()
			if !ok || time.Until(dl) >= 3*time.Second {
				t.Errorf("expected clamped publish timeout < 3s, ok=%v rem=%v", ok, time.Until(dl))
			}
			return nil
		})
		if err != nil || n != 1 {
			t.Fatalf("P3-8 Postgres clamp violation: n=%d err=%v (want 1, nil)", n, err)
		}
	})
}

// TestPostgresW3A_D10_LatePreSettlementRefundAndSQLReports verifies D10/N11 on real Postgres SQL:
// UpdateOrderRefunded updates settlement_refunded_vnd for late is_post_settlement=false refunds,
// preserves it for is_post_settlement=true or legacy nil, dedupes duplicate event_id,
// and ReportSellerCompletedDaily / ReportPlatformCompletedDaily match pgComputeSettlementSplit.
func TestPostgresW3A_D10_LatePreSettlementRefundAndSQLReports(t *testing.T) {
	st, _ := openTempPostgresStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	isFalse := false
	isTrue := true

	// Seed delivered orders on shop-1 with valid P3-10 fixture (D=0, R=1,000,000, Ship=30,000 => P=1,030,000;
	// C_cat=50,000, C_pay=20,600 (2% of 1,030,000) => C_total=70,600, raw_net=929,400; refunded 940k+10k=950k <= P).
	seedDelivered := func(id string) {
		seedPGCheckoutGroupWithOrder(t, st, "grp-"+id, "buyer-d10", "shop-1", "idem-"+id, db.InsertOrderParams{
			ID:                         id,
			OrderCode:                  "NM-" + id,
			Status:                     "DELIVERED",
			PaymentMethod:              "VIETQR",
			PaymentID:                  "pay-" + id,
			PaymentStatus:              "SUCCEEDED",
			SubtotalVND:                1000000,
			PlatformVoucherDiscountVND: 0,
			ShippingFeeVND:             30000,
			CommissionVND:              50000,
			PaymentFeeVND:              20600,
			PaymentFeeBPS:              200,
			TotalPayableVND:            1030000,
		}, now)
	}

	completeOrder := func(id string) {
		t.Helper()
		_, err := st.TransitionOrderCompleted(ctx, CompleteOrderParams{
			OrderID: id, ActorID: "buyer-d10", ActorRole: "buyer", Note: "confirm",
			OutboxEvent: db.InsertOutboxEventParams{ID: uuid.NewString(), EventType: events.SubjectOrderCompleted, PayloadJSON: []byte(`{"order_id":"` + id + `"}`)},
			Now:         now,
		})
		if err != nil {
			t.Fatalf("TransitionOrderCompleted(%s): %v", id, err)
		}
	}

	// Order 1: late pre-settlement refund (940k <= P=1,030k, is_post_settlement=false) AFTER completed, plus duplicate event_id!
	seedDelivered("ord-pg-d10-late")
	completeOrder("ord-pg-d10-late")
	dup, err := st.RecordPaymentRefunded(ctx, PaymentRefundedParams{
		EventID: "ev-pg-d10-1", EventType: events.SubjectPaymentRefunded,
		PaymentID: "pay-ord-pg-d10-late", OrderID: "ord-pg-d10-late",
		RefundID: "rf-1", AmountVND: 940000, Reason: "late pre-settlement", IsPostSettlement: &isFalse,
	})
	if err != nil || dup {
		t.Fatalf("RecordPaymentRefunded(late pre) = %v, %v", dup, err)
	}
	dup2, err := st.RecordPaymentRefunded(ctx, PaymentRefundedParams{
		EventID: "ev-pg-d10-1", EventType: events.SubjectPaymentRefunded,
		PaymentID: "pay-ord-pg-d10-late", OrderID: "ord-pg-d10-late",
		RefundID: "rf-1", AmountVND: 940000, Reason: "dup", IsPostSettlement: &isFalse,
	})
	if err != nil || !dup2 {
		t.Fatalf("expected duplicate event_id to return dup=true, got %v err=%v", dup2, err)
	}
	// Also add a genuine post-settlement refund of 10k (total 950k <= P=1,030k) -> settlement_refunded_vnd stays 940k.
	if _, err := st.RecordPaymentRefunded(ctx, PaymentRefundedParams{
		EventID: "ev-pg-d10-2", EventType: events.SubjectPaymentRefunded,
		PaymentID: "pay-ord-pg-d10-late", OrderID: "ord-pg-d10-late",
		RefundID: "rf-2", AmountVND: 10000, Reason: "post-settlement", IsPostSettlement: &isTrue,
	}); err != nil {
		t.Fatalf("RecordPaymentRefunded(post): %v", err)
	}

	agg1, _ := st.GetOrderWithDetails(ctx, "ord-pg-d10-late")
	if agg1.Order.RefundedVND != 950000 || agg1.Order.SettlementRefundedVND != 940000 {
		t.Fatalf("ord-pg-d10-late: refunded=%d settlement_refunded=%d, want 950000/940000",
			agg1.Order.RefundedVND, agg1.Order.SettlementRefundedVND)
	}

	// Verify SQL seller & platform reports match pgComputeSettlementSplit waterfall!
	startTS := now.Add(-24 * time.Hour)
	endTS := now.Add(48 * time.Hour)
	sellerRep, err := st.QueryOrderReport(ctx, OrderReportParams{ShopID: "shop-1", StartTS: startTS, EndTS: endTS, TopLimit: 10})
	if err != nil {
		t.Fatalf("QueryOrderReport(seller): %v", err)
	}
	platRep, err := st.QueryOrderReport(ctx, OrderReportParams{ShopID: "", StartTS: startTS, EndTS: endTS, TopLimit: 10})
	if err != nil {
		t.Fatalf("QueryOrderReport(platform): %v", err)
	}
	wantSellerNetAtSettle, wantTotalEffFees, _ := pgComputeSettlementSplit(1000000, 70600, 30000, 940000)
	wantNet := wantSellerNetAtSettle - 10000 // minus post-settlement 10k
	if len(sellerRep.CompletedDaily) != 1 || len(platRep.CompletedDaily) != 1 {
		t.Fatalf("expected 1 completed daily bucket, got seller=%d plat=%d", len(sellerRep.CompletedDaily), len(platRep.CompletedDaily))
	}
	sd := sellerRep.CompletedDaily[0]
	pd := platRep.CompletedDaily[0]
	if sd.NetRevenueVND != wantNet || sd.CommissionVND+sd.PaymentFeeVND != wantTotalEffFees || sd.CommissionVND != 50000 || sd.PaymentFeeVND != 10000 {
		t.Fatalf("Postgres seller report mismatch: got %+v; want net=%d comm=50000 payFee=10000", sd, wantNet)
	}
	if pd.NetRevenueVND != wantNet || pd.CommissionVND != 50000 || pd.PaymentFeeVND != 10000 || pd.PlatformSubsidyVND != 0 {
		t.Fatalf("Postgres platform report mismatch: got %+v; want net=%d comm=50000 payFee=10000 subsidy=0", pd, wantNet)
	}

	// Order 2: legacy event missing is_post_settlement (nil) after completed keeps settlement_refunded_vnd=0.
	seedDelivered("ord-pg-d10-legacy")
	completeOrder("ord-pg-d10-legacy")
	if _, err := st.RecordPaymentRefunded(ctx, PaymentRefundedParams{
		EventID: "ev-pg-d10-legacy", EventType: events.SubjectPaymentRefunded,
		PaymentID: "pay-ord-pg-d10-legacy", OrderID: "ord-pg-d10-legacy",
		RefundID: "rf-leg", AmountVND: 200000, Reason: "legacy", IsPostSettlement: nil,
	}); err != nil {
		t.Fatalf("RecordPaymentRefunded(legacy): %v", err)
	}
	aggLeg, _ := st.GetOrderWithDetails(ctx, "ord-pg-d10-legacy")
	if aggLeg.Order.RefundedVND != 200000 || aggLeg.Order.SettlementRefundedVND != 0 {
		t.Fatalf("legacy nil must keep settlement_refunded_vnd=0, got refunded=%d settlement_refunded=%d",
			aggLeg.Order.RefundedVND, aggLeg.Order.SettlementRefundedVND)
	}

	// Order 3: group-level full refund (OrderID = checkout_group_id, AmountVND = P = 1,030,000) with is_post_settlement=false after completed!
	seedDelivered("ord-pg-d10-grp")
	completeOrder("ord-pg-d10-grp")
	if _, err := st.RecordPaymentRefunded(ctx, PaymentRefundedParams{
		EventID: "ev-pg-d10-grp", EventType: events.SubjectPaymentRefunded,
		PaymentID: "pay-ord-pg-d10-grp", OrderID: "grp-ord-pg-d10-grp",
		RefundID: "rf-grp", AmountVND: 1030000, Reason: "group pre-settlement", IsPostSettlement: &isFalse,
	}); err != nil {
		t.Fatalf("RecordPaymentRefunded(group): %v", err)
	}
	aggGrp, _ := st.GetOrderWithDetails(ctx, "ord-pg-d10-grp")
	if aggGrp.Order.RefundedVND != 1030000 || aggGrp.Order.SettlementRefundedVND != 1030000 || aggGrp.Order.PaymentStatus != "REFUNDED" {
		t.Fatalf("group late pre-settlement refund failed: refunded=%d settlement_refunded=%d payStatus=%q",
			aggGrp.Order.RefundedVND, aggGrp.Order.SettlementRefundedVND, aggGrp.Order.PaymentStatus)
	}
}

// TestPostgresW3A_ExpiredPreservedAndLatestCheckoutGroup verifies item 4 and D11 SQL & concurrency guards on real Postgres.
func TestPostgresW3A_ExpiredPreservedAndLatestCheckoutGroup(t *testing.T) {
	st, pool := openTempPostgresStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	// 1. FailCheckoutGroup SQL preserves EXPIRED (never overwrites with CANCELLED).
	gid := "grp-pg-exp-1"
	seedPGCheckoutGroupWithOrder(t, st, gid, "buyer-exp", "shop-1", "idem-pg-exp", db.InsertOrderParams{
		ID: "ord-pg-exp-1", OrderCode: "NM-PG-EXP-1", Status: "PENDING",
		PaymentMethod: "VIETQR", PaymentStatus: "PENDING", SubtotalVND: 500000, TotalPayableVND: 500000,
	}, now)
	if _, err := st.TransitionOrdersPaymentResult(ctx, PaymentResultTransitionParams{
		CheckoutGroupID: gid, ResultStatus: orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_EXPIRED,
		Reason: "expired", ActorID: "watchdog", ActorRole: "system", Now: now,
	}); err != nil {
		t.Fatalf("TransitionOrdersPaymentResult(EXPIRED): %v", err)
	}
	if err := st.FailCheckoutGroup(ctx, gid, "CANCELLED"); err != nil {
		t.Fatalf("FailCheckoutGroup: %v", err)
	}
	grp, err := st.GetCheckoutGroupByID(ctx, gid)
	if err != nil {
		t.Fatalf("GetCheckoutGroupByID: %v", err)
	}
	if grp.Status != "FAILED" || grp.PaymentStatus != "EXPIRED" {
		t.Fatalf("Postgres FailCheckoutGroup overwrote EXPIRED: status=%q payment_status=%q", grp.Status, grp.PaymentStatus)
	}

	// 2. D11: FAILED group WITH persisted orders -> GetCheckoutGroupForIdempotency returns HasOrders=true,
	//    AND CreateCheckoutGroup rejects with ErrCheckoutGroupFailedWithOrders (Reason=EXPIRED) without creating a 2nd group!
	lookup, err := st.GetCheckoutGroupForIdempotency(ctx, "buyer-exp", "idem-pg-exp")
	if err != nil || lookup.Group.ID != gid || !lookup.HasOrders || lookup.Group.Status != "FAILED" || lookup.Group.PaymentStatus != "EXPIRED" {
		t.Fatalf("GetCheckoutGroupForIdempotency(failed with orders) = %+v err=%v", lookup, err)
	}
	_, err = st.CreateCheckoutGroup(ctx, db.CreateCheckoutGroupParams{
		ID: "grp-pg-exp-second", BuyerID: "buyer-exp", IdempotencyKey: "idem-pg-exp", RequestHash: "h2",
		Status: "IN_PROGRESS", PaymentMethod: "VIETQR", TotalPayableVND: 500000,
	})
	var failedWithOrders *FailedCheckoutWithOrdersError
	if !errors.Is(err, ErrCheckoutGroupFailedWithOrders) || !errors.As(err, &failedWithOrders) || failedWithOrders.Reason != "EXPIRED" {
		t.Fatalf("D11 Postgres violation: CreateCheckoutGroup after FAILED-with-orders = %v, want FailedCheckoutWithOrdersError(EXPIRED)", err)
	}
	var groupCount int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM checkout_groups WHERE buyer_id = 'buyer-exp' AND idempotency_key = 'idem-pg-exp'`).Scan(&groupCount)
	if groupCount != 1 {
		t.Fatalf("expected 1 checkout_group row for failed-with-orders key, got %d", groupCount)
	}

	// 3. D11 + N9: FAILED group with 0 orders + needs_cleanup=true blocks CreateCheckoutGroup until cleared, then allows retry.
	gidNC := "grp-pg-d11-nc"
	if _, err := st.CreateCheckoutGroup(ctx, db.CreateCheckoutGroupParams{
		ID: gidNC, BuyerID: "buyer-d11", IdempotencyKey: "idem-pg-nc", RequestHash: "h-nc",
		Status: "IN_PROGRESS", PaymentMethod: "COD", TotalPayableVND: 0,
	}); err != nil {
		t.Fatalf("CreateCheckoutGroup(nc): %v", err)
	}
	if claimed, err := st.ClaimStaleCheckoutGroupForCleanup(ctx, gidNC, "STOCK_RESERVE_FAILED"); err != nil || !claimed {
		t.Fatalf("ClaimStaleCheckoutGroupForCleanup: claimed=%v err=%v", claimed, err)
	}
	ncLookup, err := st.GetCheckoutGroupForIdempotency(ctx, "buyer-d11", "idem-pg-nc")
	if err != nil || ncLookup.Group.ID != gidNC || ncLookup.HasOrders || !ncLookup.Group.NeedsCleanup {
		t.Fatalf("GetCheckoutGroupForIdempotency(needs_cleanup) = %+v err=%v", ncLookup, err)
	}
	if _, err := st.CreateCheckoutGroup(ctx, db.CreateCheckoutGroupParams{
		ID: "grp-pg-d11-nc-blocked", BuyerID: "buyer-d11", IdempotencyKey: "idem-pg-nc", RequestHash: "h-nc2",
		Status: "IN_PROGRESS", PaymentMethod: "VIETQR", TotalPayableVND: 0,
	}); !errors.Is(err, ErrDuplicateIdempotencyKey) {
		t.Fatalf("expected ErrDuplicateIdempotencyKey while needs_cleanup=true, got %v", err)
	}
	if err := st.ClearCheckoutGroupNeedsCleanup(ctx, gidNC); err != nil {
		t.Fatalf("ClearCheckoutGroupNeedsCleanup: %v", err)
	}
	if _, err := st.GetCheckoutGroupForIdempotency(ctx, "buyer-d11", "idem-pg-nc"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after clearing needs_cleanup on 0-order FAILED group, got %v", err)
	}

	// 4. D11(iv) Postgres race: 10 concurrent goroutines calling CreateCheckoutGroup simultaneously on the same
	//    (buyer_id, idempotency_key) right after a 0-order FAILED group -> strictly 1 wins, 9 get ErrDuplicateIdempotencyKey!
	gidZero := "grp-pg-d11-zero"
	if _, err := st.CreateCheckoutGroup(ctx, db.CreateCheckoutGroupParams{
		ID: gidZero, BuyerID: "buyer-d11", IdempotencyKey: "idem-pg-race10", RequestHash: "h-fail",
		Status: "IN_PROGRESS", PaymentMethod: "COD", TotalPayableVND: 0,
	}); err != nil {
		t.Fatalf("CreateCheckoutGroup(zero): %v", err)
	}
	if err := st.FailCheckoutGroup(ctx, gidZero, "COD_LIMIT_EXCEEDED"); err != nil {
		t.Fatalf("FailCheckoutGroup(zero): %v", err)
	}

	const workers = 10
	start := make(chan struct{})
	var succeeded atomic.Int32
	var dupCount atomic.Int32
	var unexpectedErrs sync.Map
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(idx int) {
			defer wg.Done()
			<-start
			_, cErr := st.CreateCheckoutGroup(ctx, db.CreateCheckoutGroupParams{
				ID: fmt.Sprintf("grp-pg-race-win-%d", idx), BuyerID: "buyer-d11", IdempotencyKey: "idem-pg-race10",
				RequestHash: "h-retry", Status: "IN_PROGRESS", PaymentMethod: "VIETQR", TotalPayableVND: 0,
			})
			switch {
			case cErr == nil:
				succeeded.Add(1)
			case errors.Is(cErr, ErrDuplicateIdempotencyKey):
				dupCount.Add(1)
			default:
				unexpectedErrs.Store(idx, cErr)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	unexpectedErrs.Range(func(k, v any) bool {
		t.Fatalf("worker[%v] unexpected error: %v", k, v)
		return false
	})
	if succeeded.Load() != 1 || dupCount.Load() != workers-1 {
		t.Fatalf("D11(iv) Postgres advisory-lock violation: succeeded=%d dup=%d (want 1 and %d)", succeeded.Load(), dupCount.Load(), workers-1)
	}
	var activeCount int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM checkout_groups WHERE buyer_id = 'buyer-d11' AND idempotency_key = 'idem-pg-race10' AND status <> 'FAILED'`).Scan(&activeCount)
	if activeCount != 1 {
		t.Fatalf("expected strictly 1 active group in Postgres, got %d", activeCount)
	}
}

// TestPostgresW3A_ShipmentsReturnsManualReviewAndWatchdogs exercises real Postgres behavioral paths
// across postgres.go and postgres_returns.go (locks, stale tokens, orphan shipment manual review,
// full return lifecycle, and watchdog finders).
func TestPostgresW3A_ShipmentsReturnsManualReviewAndWatchdogs(t *testing.T) {
	st, pool := openTempPostgresStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	// 1. Forward shipment lifecycle + stale token guard + ApplyShipmentUpdate (delivering -> delivered).
	gid1 := "grp-pg-life-1"
	ord1ID := "ord-pg-life-1"
	seedPGCheckoutGroupWithOrder(t, st, gid1, "buyer-life", "shop-1", "idem-pg-life-1", db.InsertOrderParams{
		ID: ord1ID, OrderCode: "NM-PG-LIFE-1", Status: "CONFIRMED",
		PaymentMethod: "COD", PaymentStatus: "PENDING", SubtotalVND: 400000, ShippingFeeVND: 30000, TotalPayableVND: 430000,
		ConfirmedAt: &now,
	}, now)
	_ = st.CompleteCheckoutWithPayment(ctx, UpdateCheckoutPaymentParams{
		CheckoutGroupID: gid1, PaymentID: "pay-life-1", PaymentStatus: "PENDING",
	})
	if byPay, err := st.GetCheckoutGroupByPaymentID(ctx, "pay-life-1"); err != nil || byPay.ID != gid1 {
		t.Fatalf("GetCheckoutGroupByPaymentID = %+v err=%v", byPay, err)
	}

	tok1, err := st.MarkOrderShipmentCreating(ctx, ord1ID, now)
	if err != nil || tok1 == "" {
		t.Fatalf("MarkOrderShipmentCreating 1: %q err=%v", tok1, err)
	}
	// Rotating token supersedes tok1 -> TransitionOrderPreparedForShipment with stale tok1 MUST fail with ErrStaleShipmentAttempt.
	tok2, err := st.MarkOrderShipmentCreating(ctx, ord1ID, now.Add(time.Second))
	if err != nil || tok2 == tok1 {
		t.Fatalf("MarkOrderShipmentCreating 2: %q err=%v", tok2, err)
	}
	if _, err := st.TransitionOrderPreparedForShipment(ctx, ShipOrderParams{
		OrderID: ord1ID, CarrierCode: "ghn", ServiceCode: "standard", TrackingCode: "TRK-STALE",
		ExpectedCreatingToken: tok1, ActorID: "seller-1", ActorRole: "seller", Now: now,
	}); !errors.Is(err, ErrStaleShipmentAttempt) {
		t.Fatalf("expected ErrStaleShipmentAttempt for superseded token, got %v", err)
	}

	prepAgg, err := st.TransitionOrderPreparedForShipment(ctx, ShipOrderParams{
		OrderID: ord1ID, CarrierCode: "ghn", ServiceCode: "standard", TrackingCode: "TRK-PG-1",
		LabelURL: "https://label/1", ExpectedCreatingToken: tok2, ActorID: "seller-1", ActorRole: "seller", Note: "shipped", Now: now,
	})
	if err != nil || prepAgg.Order.Status != "PREPARING" || prepAgg.Order.TrackingCode != "TRK-PG-1" {
		t.Fatalf("TransitionOrderPreparedForShipment = %+v err=%v", prepAgg, err)
	}

	// Webhook delivering -> SHIPPING, then delivered -> DELIVERED (idempotent on dup event_id).
	shipOut, err := st.ApplyShipmentUpdate(ctx, ShipmentUpdateParams{
		EventID: "ev-pg-shp-1", EventType: events.SubjectShipmentUpdated,
		OrderID: ord1ID, ShipmentStatus: "delivering", TrackingCode: "TRK-PG-1", Description: "picked up", Now: now,
	})
	if err != nil || shipOut.TransitionedTo != orderv1.OrderStatus_ORDER_STATUS_SHIPPING {
		t.Fatalf("ApplyShipmentUpdate(delivering) = %+v err=%v", shipOut, err)
	}
	delivOut, err := st.ApplyShipmentUpdate(ctx, ShipmentUpdateParams{
		EventID: "ev-pg-shp-2", EventType: events.SubjectShipmentUpdated,
		OrderID: ord1ID, ShipmentStatus: "delivered", TrackingCode: "TRK-PG-1", Description: "delivered", Now: now,
	})
	if err != nil || delivOut.TransitionedTo != orderv1.OrderStatus_ORDER_STATUS_DELIVERED {
		t.Fatalf("ApplyShipmentUpdate(delivered) = %+v err=%v", delivOut, err)
	}
	dupShip, err := st.ApplyShipmentUpdate(ctx, ShipmentUpdateParams{
		EventID: "ev-pg-shp-2", EventType: events.SubjectShipmentUpdated,
		OrderID: ord1ID, ShipmentStatus: "delivered", TrackingCode: "TRK-PG-1", Now: now,
	})
	if err != nil || !dupShip.AlreadyProcessed {
		t.Fatalf("expected dup shipment event AlreadyProcessed=true, got %+v err=%v", dupShip, err)
	}

	// 2. Full Return Request lifecycle on Postgres: PENDING -> AWAITING_SHIPMENT -> SaveReturnShipmentInfo
	//    -> IN_TRANSIT -> RECEIVED -> REFUNDING -> UpdateReturnSideEffectProgress -> REFUNDED.
	retID := "ret-pg-1"
	dl := now.Add(48 * time.Hour)
	_, retAgg, err := st.TransitionOrderReturnRequested(ctx, ReturnRequestParams{
		OrderID: ord1ID, OrderCode: "NM-PG-LIFE-1", BuyerID: "buyer-life", ShopID: "shop-1",
		ReturnRequestID: retID, Reason: "defective", EvidenceURLsJSON: []byte(`["https://img/1.jpg"]`),
		RefundAmountVND: 430000, DeadlineAt: &dl, ActorID: "buyer-life", ActorRole: "buyer", Note: "req",
		OutboxEvent: db.InsertOutboxEventParams{ID: uuid.NewString(), EventType: events.SubjectOrderReturnUpdated, PayloadJSON: []byte(`{"order_id":"` + ord1ID + `"}`)},
		Now:         now,
	})
	if err != nil || retAgg.Request.Status != "PENDING" {
		t.Fatalf("TransitionOrderReturnRequested = %+v err=%v", retAgg, err)
	}

	// Duplicate return request on already RETURN_REQUESTED order MUST fail with ErrInvalidTransition.
	if _, _, err := st.TransitionOrderReturnRequested(ctx, ReturnRequestParams{
		OrderID: ord1ID, OrderCode: "NM-PG-LIFE-1", BuyerID: "buyer-life", ShopID: "shop-1",
		ReturnRequestID: "ret-pg-dup", Reason: "dup", EvidenceURLsJSON: []byte(`[]`),
		RefundAmountVND: 430000, DeadlineAt: &dl, ActorID: "buyer-life", ActorRole: "buyer",
		OutboxEvent: db.InsertOutboxEventParams{ID: uuid.NewString(), EventType: events.SubjectOrderReturnUpdated, PayloadJSON: []byte(`{"order_id":"` + ord1ID + `"}`)},
		Now:         now,
	}); !errors.Is(err, statemachine.ErrInvalidTransition) {
		t.Fatalf("expected ErrInvalidTransition for second return on RETURN_REQUESTED order, got %v", err)
	}

	// Accept return -> AWAITING_SHIPMENT
	respStr := "accepted"
	trAcc, err := st.TransitionReturnRequest(ctx, ReturnTransitionParams{
		ReturnRequestID: retID,
		ExpectedStatus:  orderv1.ReturnStatus_RETURN_STATUS_PENDING,
		TargetStatus:    orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT,
		ActorID:         "seller-1", ActorRole: "seller", SellerResponse: &respStr, Note: "accept", Now: now,
	})
	if err != nil || trAcc.Return.Request.Status != "AWAITING_SHIPMENT" {
		t.Fatalf("TransitionReturnRequest(AWAITING_SHIPMENT) = %+v err=%v", trAcc, err)
	}
	awaitingIDs, err := st.FindAwaitingShipmentReturnsWithoutTracking(ctx, 10)
	if err != nil || len(awaitingIDs) != 1 || awaitingIDs[0] != retID {
		t.Fatalf("FindAwaitingShipmentReturnsWithoutTracking = %v err=%v", awaitingIDs, err)
	}

	// Attach return tracking + extend deadline
	retDL := now.Add(5 * 24 * time.Hour)
	savedRet, didSave, err := st.SaveReturnShipmentInfo(ctx, SaveReturnShipmentParams{
		ReturnRequestID: retID, TrackingCode: "RTRK-1", LabelURL: "https://label/ret1", FeeVND: 25000, DeadlineAt: &retDL, Now: now,
	})
	if err != nil || !didSave || savedRet.Request.ReturnTrackingCode != "RTRK-1" {
		t.Fatalf("SaveReturnShipmentInfo = %+v didSave=%v err=%v", savedRet, didSave, err)
	}
	extended, err := st.ExtendReturnDeadline(ctx, retID, orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT, retDL.Add(24*time.Hour), retDL.Add(time.Minute), 1)
	if err != nil || !extended {
		t.Fatalf("ExtendReturnDeadline = %v err=%v", extended, err)
	}
	// Exceeding maxExtensions=1 returns false.
	extended2, err := st.ExtendReturnDeadline(ctx, retID, orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT, retDL.Add(48*time.Hour), retDL.Add(25*time.Hour), 1)
	if err != nil || extended2 {
		t.Fatalf("ExtendReturnDeadline(2nd) = %v err=%v; want false", extended2, err)
	}

	// RETURN shipment webhooks: delivering -> IN_TRANSIT, delivered -> RECEIVED.
	ru1, err := st.ApplyReturnShipmentUpdate(ctx, ReturnShipmentUpdateParams{
		EventID: "ev-pg-ret-1", EventType: events.SubjectShipmentUpdated,
		OrderID: ord1ID, ReturnRequestID: retID, ShipmentStatus: "delivering", TrackingCode: "RTRK-1",
		TransitDuration: 7 * 24 * time.Hour, InspectDuration: 3 * 24 * time.Hour, Now: now,
	})
	if err != nil || !ru1.Transitioned || ru1.Return.Request.Status != "IN_TRANSIT" {
		t.Fatalf("ApplyReturnShipmentUpdate(delivering) = %+v err=%v", ru1, err)
	}
	ru2, err := st.ApplyReturnShipmentUpdate(ctx, ReturnShipmentUpdateParams{
		EventID: "ev-pg-ret-2", EventType: events.SubjectShipmentUpdated,
		OrderID: ord1ID, ReturnRequestID: retID, ShipmentStatus: "delivered", TrackingCode: "RTRK-1",
		TransitDuration: 7 * 24 * time.Hour, InspectDuration: 3 * 24 * time.Hour, Now: now,
	})
	if err != nil || !ru2.Transitioned || ru2.Return.Request.Status != "RECEIVED" {
		t.Fatalf("ApplyReturnShipmentUpdate(delivered) = %+v err=%v", ru2, err)
	}

	// Move RECEIVED -> REFUNDING -> track side effects -> REFUNDED.
	goodsRecv := true
	shouldRestock := true
	if _, err := st.TransitionReturnRequest(ctx, ReturnTransitionParams{
		ReturnRequestID: retID,
		ExpectedStatus:  orderv1.ReturnStatus_RETURN_STATUS_RECEIVED,
		TargetStatus:    orderv1.ReturnStatus_RETURN_STATUS_REFUNDING,
		GoodsReceived:   &goodsRecv, ShouldRestock: &shouldRestock,
		ActorID: "seller-1", ActorRole: "seller", Note: "inspect ok", Now: now,
	}); err != nil {
		t.Fatalf("TransitionReturnRequest(REFUNDING): %v", err)
	}
	refundingIDs, err := st.FindRefundingReturnRequests(ctx, 10)
	if err != nil || len(refundingIDs) != 1 || refundingIDs[0] != retID {
		t.Fatalf("FindRefundingReturnRequests = %v err=%v", refundingIDs, err)
	}
	refAmt := int64(430000)
	if _, err := st.UpdateReturnSideEffectProgress(ctx, retID, true, true, true, &refAmt); err != nil {
		t.Fatalf("UpdateReturnSideEffectProgress: %v", err)
	}
	trDone, err := st.TransitionReturnRequest(ctx, ReturnTransitionParams{
		ReturnRequestID: retID,
		ExpectedStatus:  orderv1.ReturnStatus_RETURN_STATUS_REFUNDING,
		TargetStatus:    orderv1.ReturnStatus_RETURN_STATUS_REFUNDED,
		ActorID:         "system", ActorRole: "system", Note: "refunded", Now: now,
	})
	if err != nil || trDone.Return.Request.Status != "REFUNDED" || trDone.Order.Order.Status != "RETURNED" {
		t.Fatalf("TransitionReturnRequest(REFUNDED) = %+v err=%v", trDone, err)
	}
	listRets, totalRets, err := st.ListReturnRequestsFiltered(ctx, "buyer-life", "shop-1", "REFUNDED", 10, 0)
	if err != nil || totalRets != 1 || len(listRets) != 1 {
		t.Fatalf("ListReturnRequestsFiltered = %d/%d err=%v", len(listRets), totalRets, err)
	}

	// 3. Concurrent cancel during shipment creation -> ErrOrphanShipmentNeedsCancel -> Manual review queue & resolve.
	gid2 := "grp-pg-orph-2"
	ord2ID := "ord-pg-orph-2"
	seedPGCheckoutGroupWithOrder(t, st, gid2, "buyer-life", "shop-1", "idem-pg-orph-2", db.InsertOrderParams{
		ID: ord2ID, OrderCode: "NM-PG-ORPH-2", Status: "CONFIRMED",
		PaymentMethod: "COD", PaymentStatus: "PENDING", SubtotalVND: 200000, TotalPayableVND: 200000, ConfirmedAt: &now,
	}, now)
	orphTok, err := st.MarkOrderShipmentCreating(ctx, ord2ID, now.Add(-10*time.Minute))
	if err != nil {
		t.Fatalf("MarkOrderShipmentCreating(ord2): %v", err)
	}
	// Cancel order2 (past PendingShipmentCancelTTL with LastCancelNotFound=true succeeds under guard).
	if _, err := st.TransitionOrderStatus(ctx, SingleOrderTransitionParams{
		OrderID: ord2ID, TargetStatus: orderv1.OrderStatus_ORDER_STATUS_CANCELLED,
		CancelReason: "buyer cancel", PaymentStatus: "CANCELLED", ActorID: "buyer-life", ActorRole: "buyer",
		VerifyShipmentCancel: true, LastCancelNotFound: true, Now: now,
	}); err != nil {
		t.Fatalf("TransitionOrderStatus(cancel ord2): %v", err)
	}
	// Late TransitionOrderPreparedForShipment on CANCELLED order returns ErrOrphanShipmentNeedsCancel!
	if _, err := st.TransitionOrderPreparedForShipment(ctx, ShipOrderParams{
		OrderID: ord2ID, CarrierCode: "ghn", ServiceCode: "standard", TrackingCode: "TRK-ORPH",
		ExpectedCreatingToken: orphTok, ActorID: "seller-1", ActorRole: "seller", Now: now,
	}); !errors.Is(err, ErrOrphanShipmentNeedsCancel) {
		t.Fatalf("expected ErrOrphanShipmentNeedsCancel (is %v)", err)
	}
	if err := st.MarkOrderOrphanShipmentManualReview(ctx, ord2ID, "manual cancel needed"); err != nil {
		t.Fatalf("MarkOrderOrphanShipmentManualReview: %v", err)
	}
	revList, revTotal, err := st.ListOrdersNeedingManualReview(ctx, 10, 0)
	if err != nil || revTotal != 1 || len(revList) != 1 || revList[0].Order.ID != ord2ID {
		t.Fatalf("ListOrdersNeedingManualReview = %d/%d err=%v", len(revList), revTotal, err)
	}
	resolved, err := st.ResolveOrderManualReview(ctx, ord2ID, "admin-1", "admin", "cancelled at carrier", now)
	if err != nil || resolved.Order.OrphanShipmentNeedsManualReview {
		t.Fatalf("ResolveOrderManualReview = %+v err=%v", resolved, err)
	}
	if _, err := st.ResolveOrderManualReview(ctx, ord2ID, "admin-1", "admin", "second", now); !errors.Is(err, ErrOrderNotNeedingReview) {
		t.Fatalf("expected ErrOrderNotNeedingReview on second resolve, got %v", err)
	}

	// 4. ListOrdersFiltered, GetOrderTimeline, and watchdog finder queries.
	ordList, ordTotal, err := st.ListOrdersFiltered(ctx, "buyer-life", "shop-1", "", 10, 0)
	if err != nil || ordTotal != 2 || len(ordList) != 2 {
		t.Fatalf("ListOrdersFiltered = %d/%d err=%v", len(ordList), ordTotal, err)
	}
	tl, err := st.GetOrderTimeline(ctx, ord1ID)
	if err != nil || len(tl) < 3 {
		t.Fatalf("GetOrderTimeline = %d err=%v", len(tl), err)
	}

	// Exercise remaining watchdog finders + Ping + DeadLetterEvent on real Postgres.
	if err := st.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if err := st.InsertDeadLetterEvent(ctx, db.InsertDeadLetterEventParams{
		ID: uuid.NewString(), Subject: "payment.failed", EventID: "ev-dlq-1", Payload: []byte(`{}`), ErrorMessage: "boom", Attempts: 3,
	}); err != nil {
		t.Fatalf("InsertDeadLetterEvent: %v", err)
	}
	// Seed a PENDING expired order, an old CONFIRMED order, and a DELIVERED order past auto-complete cutoff.
	past := now.Add(-10 * 24 * time.Hour)
	seedPGCheckoutGroupWithOrder(t, st, "grp-wd-exp", "buyer-wd", "shop-1", "idem-wd-exp", db.InsertOrderParams{
		ID: "ord-wd-exp", OrderCode: "NM-WD-EXP", Status: "PENDING", PaymentMethod: "VIETQR",
		PaymentStatus: "PENDING", SubtotalVND: 100000, TotalPayableVND: 100000, PaymentExpiresAt: &past,
	}, past)
	if cands, err := st.FindExpiredPendingGroups(ctx, 10); err != nil || len(cands) != 1 || cands[0].CheckoutGroupID != "grp-wd-exp" {
		t.Fatalf("FindExpiredPendingGroups = %+v err=%v", cands, err)
	}
	seedPGCheckoutGroupWithOrder(t, st, "grp-wd-conf", "buyer-wd", "shop-1", "idem-wd-conf", db.InsertOrderParams{
		ID: "ord-wd-conf", OrderCode: "NM-WD-CONF", Status: "CONFIRMED", PaymentMethod: "COD",
		PaymentStatus: "PENDING", SubtotalVND: 100000, TotalPayableVND: 100000, ConfirmedAt: &past,
	}, past)
	if ids, err := st.FindConfirmedOrdersForAutoCancel(ctx, now.Add(-48*time.Hour), 10); err != nil || len(ids) != 1 {
		t.Fatalf("FindConfirmedOrdersForAutoCancel = %v err=%v", ids, err)
	}
	seedPGCheckoutGroupWithOrder(t, st, "grp-wd-del", "buyer-wd", "shop-1", "idem-wd-del", db.InsertOrderParams{
		ID: "ord-wd-del", OrderCode: "NM-WD-DEL", Status: "DELIVERED", PaymentMethod: "COD",
		PaymentStatus: "SUCCEEDED", SubtotalVND: 100000, TotalPayableVND: 100000,
	}, past)
	_, _ = pool.Exec(ctx, `UPDATE orders SET delivered_at = $1 WHERE id = 'ord-wd-del'`, past)
	if ids, err := st.FindDeliveredOrdersForAutoComplete(ctx, now.Add(-7*24*time.Hour), 10); err != nil || len(ids) != 1 {
		t.Fatalf("FindDeliveredOrdersForAutoComplete = %v err=%v", ids, err)
	}
}

// TestPostgresW3A_Migration0014_PendingSeqBackfillByCreatedAt verifies D9-bis §10 on real PostgreSQL:
// When older pending event E1 is UPDATEd prior to running migration 0014 (so PostgreSQL MVCC places
// E1 at a higher physical heap ctid than newer E2), migration 0014 MUST reassign seq for all
// published_at IS NULL rows strictly in (created_at ASC, id ASC) order via nextval on the seq sequence
// so E1 is still published before E2 (and equal-created_at ties break by id ASC).
func TestPostgresW3A_Migration0014_PendingSeqBackfillByCreatedAt(t *testing.T) {
	adminDSN := strings.TrimSpace(os.Getenv("TEST_ORDER_DB_URL"))
	if adminDSN == "" {
		t.Skip("TEST_ORDER_DB_URL not set; skipping real Postgres migration 0014 test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	parsed, err := url.Parse(adminDSN)
	if err != nil {
		t.Fatalf("parse TEST_ORDER_DB_URL: %v", err)
	}
	adminConn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatalf("connect admin postgres: %v", err)
	}
	tempDBName := "order_mig0014_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	if _, err := adminConn.Exec(ctx, "CREATE DATABASE "+tempDBName); err != nil {
		_ = adminConn.Close(ctx)
		t.Fatalf("create temp db %s: %v", tempDBName, err)
	}
	_ = adminConn.Close(ctx)

	testURL := *parsed
	testURL.Path = "/" + tempDBName
	pool, err := pgxpool.New(ctx, testURL.String())
	if err != nil {
		dropTempDatabase(adminDSN, tempDBName)
		t.Fatalf("open temp db pool %s: %v", tempDBName, err)
	}
	t.Cleanup(func() {
		pool.Close()
		dropTempDatabase(adminDSN, tempDBName)
	})

	// 1. Apply ONLY pre-outbox-lease migrations (0001..0016) to simulate a live DB right before upgrading.
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			filename TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	entries, err := fs.ReadDir(migrations.Files, ".")
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	var pre0017 []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		if entry.Name() >= "0017_" {
			continue
		}
		pre0017 = append(pre0017, entry.Name())
	}
	sort.Strings(pre0017)
	for _, name := range pre0017 {
		sqlBytes, rErr := migrations.Files.ReadFile(name)
		if rErr != nil {
			t.Fatalf("read %s: %v", name, rErr)
		}
		if _, eErr := pool.Exec(ctx, string(sqlBytes)); eErr != nil {
			t.Fatalf("exec %s: %v", name, eErr)
		}
		if _, rErr := pool.Exec(ctx, `INSERT INTO schema_migrations (filename) VALUES ($1)`, name); rErr != nil {
			t.Fatalf("record %s: %v", name, rErr)
		}
	}

	// 2. Insert E0 (published), E1 (older pending), E2 (newer pending), plus equal-created_at E3a & E3b.
	base := time.Now().UTC().Truncate(time.Second)
	payload := `{"order_id":"ord-mig-0014"}`
	insertPre := func(id, evType string, createdAt time.Time, pubAt *time.Time) {
		t.Helper()
		if _, err := pool.Exec(ctx, `
			INSERT INTO outbox_events (id, event_type, trace_id, traceparent, payload_json, published_at, created_at)
			VALUES ($1, $2, 'tr-mig', '', $3::jsonb, $4, $5)
		`, id, evType, payload, pubAt, createdAt); err != nil {
			t.Fatalf("insert pre-0014 outbox %s: %v", id, err)
		}
	}
	pubTS := base.Add(-20 * time.Second)
	insertPre("ev-e0-published", events.SubjectOrderCreated, base.Add(-25*time.Second), &pubTS)
	insertPre("ev-e1-older", events.SubjectOrderCreated, base.Add(-10*time.Second), nil)
	insertPre("ev-e2-newer", events.SubjectOrderConfirmed, base.Add(-5*time.Second), nil)
	insertPre("ev-tie-a", events.SubjectOrderShipped, base.Add(-2*time.Second), nil)
	insertPre("ev-tie-b", events.SubjectOrderDelivered, base.Add(-2*time.Second), nil)

	// 3. UPDATE older E1 and tie-first E3a so MVCC rewrites their tuples to higher physical ctids
	//    than E2 and E3b respectively.
	if _, err := pool.Exec(ctx, `
		UPDATE outbox_events
		SET attempts = attempts + 1, last_error = 'transient pre-migration retry'
		WHERE id IN ('ev-e1-older', 'ev-tie-a')
	`); err != nil {
		t.Fatalf("update E1/E3a to invert ctid: %v", err)
	}

	var e1AfterE2, tieAAfterTieB bool
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT ctid FROM outbox_events WHERE id = 'ev-e1-older') > (SELECT ctid FROM outbox_events WHERE id = 'ev-e2-newer'),
			(SELECT ctid FROM outbox_events WHERE id = 'ev-tie-a') > (SELECT ctid FROM outbox_events WHERE id = 'ev-tie-b')
	`).Scan(&e1AfterE2, &tieAAfterTieB); err != nil {
		t.Fatalf("verify ctid inversion: %v", err)
	}
	if !e1AfterE2 || !tieAAfterTieB {
		t.Fatalf("expected MVCC ctid inversion before 0014 (e1AfterE2=%v, tieAAfterTieB=%v)", e1AfterE2, tieAAfterTieB)
	}

	// 4. Apply 0014_outbox_lease.sql (and any subsequent migrations) via standard ApplyMigrations.
	if err := db.ApplyMigrations(ctx, pool); err != nil {
		t.Fatalf("ApplyMigrations(0014): %v", err)
	}

	// P3-3: Verify superseded idx_outbox_events_unpublished index was dropped.
	var oldIdxExists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE tablename = 'outbox_events' AND indexname = 'idx_outbox_events_unpublished')`).Scan(&oldIdxExists); err != nil {
		t.Fatalf("check old index dropped: %v", err)
	}
	if oldIdxExists {
		t.Fatalf("P3-3 violation: idx_outbox_events_unpublished was not dropped by migration 0014")
	}

	// 5. Insert a brand-new post-migration event to confirm sequence continues monotonically after backfill.
	st := NewPostgresStore(pool)
	insertOutboxEventRaw(t, st, "ev-post-mig", events.SubjectOrderCompleted, "ord-mig-0014")

	// Verify exact seq ordering in DB: E1 < E2 < E3a < E3b < E_post.
	seqOf := func(id string) int64 {
		t.Helper()
		var s int64
		if err := pool.QueryRow(ctx, `SELECT seq FROM outbox_events WHERE id = $1`, id).Scan(&s); err != nil {
			t.Fatalf("query seq(%s): %v", id, err)
		}
		return s
	}
	s1, s2, s3a, s3b, sPost := seqOf("ev-e1-older"), seqOf("ev-e2-newer"), seqOf("ev-tie-a"), seqOf("ev-tie-b"), seqOf("ev-post-mig")
	if !(s1 < s2 && s2 < s3a && s3a < s3b && s3b < sPost) {
		t.Fatalf("D9-bis §10 seq backfill violation: E1=%d E2=%d E3a=%d E3b=%d Post=%d (want strictly ascending)", s1, s2, s3a, s3b, sPost)
	}

	// 6. ProcessPendingOutbox must publish E1 before E2 and preserve full order.
	var delivered []string
	n, err := st.ProcessPendingOutbox(ctx, 10, func(_ context.Context, ev db.OutboxEvent) error {
		delivered = append(delivered, ev.ID)
		return nil
	})
	if err != nil || n != 5 {
		t.Fatalf("ProcessPendingOutbox = %d, err=%v; want 5", n, err)
	}
	want := []string{"ev-e1-older", "ev-e2-newer", "ev-tie-a", "ev-tie-b", "ev-post-mig"}
	for i := range want {
		if delivered[i] != want[i] {
			t.Fatalf("publish order mismatch after 0014: got %v, want %v", delivered, want)
		}
	}
}

// TestPostgresW3A_D9Bis9_OutboxInsertHoldsOrderLockInterleaved verifies D9-bis §9 on real PostgreSQL:
// Every store transaction inserting an outbox event keyed by order_id (including all return-request
// transitions in postgres_returns.go) MUST acquire FOR UPDATE on the orders row BEFORE inserting into
// outbox_events, so interleaved concurrent transactions on the same order_id strictly serialize their
// outbox seq allocation without inversion or deadlock.
func TestPostgresW3A_D9Bis9_OutboxInsertHoldsOrderLockInterleaved(t *testing.T) {
	st, pool := openTempPostgresStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	runInterleavedCase := func(
		t *testing.T,
		name string,
		setup func(ordID, retID string),
		runTx2 func(ordID, retID string) error,
	) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			ordID := "ord-lock-" + uuid.NewString()[:8]
			retID := "ret-lock-" + uuid.NewString()[:8]
			setup(ordID, retID)

			if _, err := pool.Exec(ctx, `DELETE FROM outbox_events`); err != nil {
				t.Fatalf("clear outbox_events: %v", err)
			}

			// TX1 holds FOR UPDATE on orders(ordID), simulating a concurrent order-level state change.
			tx1, err := pool.Begin(ctx)
			if err != nil {
				t.Fatalf("begin tx1: %v", err)
			}
			defer func() { _ = tx1.Rollback(ctx) }()

			var lockedID string
			if err := tx1.QueryRow(ctx, `SELECT id FROM orders WHERE id = $1 FOR UPDATE`, ordID).Scan(&lockedID); err != nil {
				t.Fatalf("tx1 lock orders(%s): %v", ordID, err)
			}

			// TX2 attempts the return-request operation emitting an outbox event for ordID.
			tx2Done := make(chan error, 1)
			go func() {
				tx2Done <- runTx2(ordID, retID)
			}()

			// TX2 MUST block waiting for TX1's orders FOR UPDATE lock (cannot insert outbox ahead of TX1).
			select {
			case tx2Err := <-tx2Done:
				t.Fatalf("D9-bis §9 violation (%s): TX2 completed while TX1 held FOR UPDATE on orders (err=%v)", name, tx2Err)
			case <-time.After(180 * time.Millisecond):
			}

			// TX1 inserts its outbox event while still holding orders FOR UPDATE, then commits.
			tx1EvID := "ev-tx1-" + uuid.NewString()[:8]
			tx1Payload, _ := json.Marshal(map[string]string{"order_id": ordID, "source": "tx1"})
			if err := st.queries.WithTx(tx1).InsertOutboxEvent(ctx, db.InsertOutboxEventParams{
				ID:          tx1EvID,
				EventType:   events.SubjectOrderDelivered,
				TraceID:     "tr-tx1",
				PayloadJSON: tx1Payload,
			}); err != nil {
				t.Fatalf("tx1 InsertOutboxEvent: %v", err)
			}
			if err := tx1.Commit(ctx); err != nil {
				t.Fatalf("commit tx1: %v", err)
			}

			select {
			case tx2Err := <-tx2Done:
				if tx2Err != nil {
					t.Fatalf("tx2 failed after tx1 commit: %v", tx2Err)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("tx2 deadlocked or timed out after tx1 commit")
			}

			// Verify strict seq order and actual publish order: TX1 event strictly precedes TX2 event.
			var delivered []string
			n, err := st.ProcessPendingOutbox(ctx, 10, func(_ context.Context, ev db.OutboxEvent) error {
				delivered = append(delivered, ev.ID+":"+ev.EventType)
				return nil
			})
			if err != nil || n != 2 {
				t.Fatalf("ProcessPendingOutbox = %d err=%v; want 2 events (%v)", n, err, delivered)
			}
			if delivered[0] != tx1EvID+":"+events.SubjectOrderDelivered {
				t.Fatalf("D9-bis §9 seq inversion (%s): want TX1 event first, got %v", name, delivered)
			}
			if !strings.HasSuffix(delivered[1], ":"+events.SubjectOrderReturnUpdated) {
				t.Fatalf("expected TX2 order.return_updated second, got %v", delivered)
			}
		})
	}

	seedDeliveredOrderWithReturn := func(ordID, retID string) {
		gid := "grp-" + ordID
		seedPGCheckoutGroupWithOrder(t, st, gid, "buyer-lock", "shop-1", "idem-"+ordID, db.InsertOrderParams{
			ID: ordID, OrderCode: "NM-" + ordID[len(ordID)-6:], Status: "DELIVERED",
			PaymentMethod: "COD", PaymentStatus: "SUCCEEDED", SubtotalVND: 200000, TotalPayableVND: 200000,
		}, now)
		dl := now.Add(48 * time.Hour)
		if _, _, err := st.TransitionOrderReturnRequested(ctx, ReturnRequestParams{
			OrderID: ordID, OrderCode: "NM-" + ordID[len(ordID)-6:], BuyerID: "buyer-lock", ShopID: "shop-1",
			ReturnRequestID: retID, Reason: "damaged", EvidenceURLsJSON: []byte(`[]`),
			RefundAmountVND: 200000, DeadlineAt: &dl, ActorID: "buyer-lock", ActorRole: "buyer", Note: "req", Now: now,
		}); err != nil {
			t.Fatalf("TransitionOrderReturnRequested(%s): %v", ordID, err)
		}
	}

	// Case 1: TransitionReturnRequest with non-order-mutating target status (PENDING -> AWAITING_SHIPMENT).
	runInterleavedCase(t, "TransitionReturnRequest_NonOrderMutating", seedDeliveredOrderWithReturn, func(_, retID string) error {
		resp := "accepted"
		_, err := st.TransitionReturnRequest(ctx, ReturnTransitionParams{
			ReturnRequestID: retID,
			ExpectedStatus:  orderv1.ReturnStatus_RETURN_STATUS_PENDING,
			TargetStatus:    orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT,
			SellerResponse:  &resp,
			ActorID:         "seller-1",
			ActorRole:       "seller",
			Note:            "accept",
			Now:             now,
		})
		return err
	})

	// Case 2: SaveReturnShipmentInfo (AWAITING_SHIPMENT -> persists tracking + emits order.return_updated).
	runInterleavedCase(t, "SaveReturnShipmentInfo", func(ordID, retID string) {
		seedDeliveredOrderWithReturn(ordID, retID)
		resp := "accepted"
		if _, err := st.TransitionReturnRequest(ctx, ReturnTransitionParams{
			ReturnRequestID: retID,
			ExpectedStatus:  orderv1.ReturnStatus_RETURN_STATUS_PENDING,
			TargetStatus:    orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT,
			SellerResponse:  &resp,
			ActorID:         "seller-1",
			ActorRole:       "seller",
			Note:            "accept",
			Now:             now,
		}); err != nil {
			t.Fatalf("setup AWAITING_SHIPMENT: %v", err)
		}
	}, func(_, retID string) error {
		dl := now.Add(72 * time.Hour)
		_, saved, err := st.SaveReturnShipmentInfo(ctx, SaveReturnShipmentParams{
			ReturnRequestID: retID,
			TrackingCode:    "RTRK-LOCK-2",
			LabelURL:        "https://label/lock2",
			FeeVND:          20000,
			DeadlineAt:      &dl,
			Now:             now,
		})
		if err != nil {
			return err
		}
		if !saved {
			return errors.New("SaveReturnShipmentInfo returned saved=false")
		}
		return nil
	})

	// Case 3: ApplyReturnShipmentUpdate (AWAITING_SHIPMENT -> IN_TRANSIT + emits order.return_updated).
	runInterleavedCase(t, "ApplyReturnShipmentUpdate", func(ordID, retID string) {
		seedDeliveredOrderWithReturn(ordID, retID)
		resp := "accepted"
		if _, err := st.TransitionReturnRequest(ctx, ReturnTransitionParams{
			ReturnRequestID: retID,
			ExpectedStatus:  orderv1.ReturnStatus_RETURN_STATUS_PENDING,
			TargetStatus:    orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT,
			SellerResponse:  &resp,
			ActorID:         "seller-1",
			ActorRole:       "seller",
			Note:            "accept",
			Now:             now,
		}); err != nil {
			t.Fatalf("setup AWAITING_SHIPMENT: %v", err)
		}
		dl := now.Add(72 * time.Hour)
		if _, _, err := st.SaveReturnShipmentInfo(ctx, SaveReturnShipmentParams{
			ReturnRequestID: retID,
			TrackingCode:    "RTRK-LOCK-3",
			LabelURL:        "https://label/lock3",
			FeeVND:          20000,
			DeadlineAt:      &dl,
			Now:             now,
		}); err != nil {
			t.Fatalf("setup SaveReturnShipmentInfo: %v", err)
		}
	}, func(ordID, retID string) error {
		out, err := st.ApplyReturnShipmentUpdate(ctx, ReturnShipmentUpdateParams{
			EventID:         "evt-lock-ret-3-" + uuid.NewString()[:8],
			EventType:       events.SubjectShipmentUpdated,
			OrderID:         ordID,
			ReturnRequestID: retID,
			ShipmentStatus:  "delivering",
			TrackingCode:    "RTRK-LOCK-3",
			TransitDuration: 7 * 24 * time.Hour,
			InspectDuration: 3 * 24 * time.Hour,
			Now:             now,
		})
		if err != nil {
			return err
		}
		if !out.Transitioned {
			return errors.New("ApplyReturnShipmentUpdate did not transition")
		}
		return nil
	})
}

// TestPostgresW3A_R6_CompleteCheckoutReemitCancelledAndLiveTotal verifies Directive 1 (P0-1 + P1-2) on real Postgres:
//   - (a) VIETQR single order cancelled before CreatePayment (group still IN_PROGRESS) -> CompleteCheckoutWithPayment
//     closes FAILED, sets total_payable_vnd=0, and emits 1 fresh order.cancelled outbox event with new event_id.
//   - (b) Same as (a), but group was ALREADY closed FAILED before CompleteCheckoutWithPayment -> still commits 1 fresh
//     order.cancelled outbox event without mutating orders or checkout_groups.
//   - (c) COD 2-shop group with 1 shop cancelled before CreatePayment -> CompleteCheckoutWithPayment closes COMPLETED,
//     sets group total_payable_vnd strictly to the live shop's payable, and emits 1 fresh order.cancelled event ONLY
//     for the cancelled shop.
//   - (d) No order cancelled -> 0 order.cancelled events emitted.
//   - (e) Group already COMPLETED -> 0 order.cancelled events emitted.
func TestPostgresW3A_R6_CompleteCheckoutReemitCancelledAndLiveTotal(t *testing.T) {
	st, pool := openTempPostgresStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	listCancelledOutboxForOrder := func(t *testing.T, ordID string) []string {
		t.Helper()
		rows, err := pool.Query(ctx,
			`SELECT id FROM outbox_events WHERE event_type = $1 AND payload_json->>'order_id' = $2 ORDER BY seq ASC`,
			events.SubjectOrderCancelled, ordID)
		if err != nil {
			t.Fatalf("query outbox for %s: %v", ordID, err)
		}
		defer rows.Close()
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatalf("scan outbox id: %v", err)
			}
			ids = append(ids, id)
		}
		return ids
	}

	t.Run("a_VIETQR_CancelledBeforeCreatePayment_InProgressClosesFailedAndReemitsEvent", func(t *testing.T) {
		gid := "grp-r6-a-" + uuid.NewString()[:8]
		ordID := "ord-r6-a-" + uuid.NewString()[:8]
		exp := now.Add(15 * time.Minute)
		seedPGCheckoutGroupWithOrder(t, st, gid, "buyer-r6", "shop-1", "idem-"+gid, db.InsertOrderParams{
			ID: ordID, OrderCode: "NM-R6-A-" + ordID[len(ordID)-4:], Status: "PENDING",
			PaymentMethod: "VIETQR", PaymentStatus: "PENDING",
			SubtotalVND: 1000000, ShippingFeeVND: 30000, TotalPayableVND: 1030000, PaymentExpiresAt: &exp,
		}, now)
		if _, err := st.TransitionOrderStatus(ctx, SingleOrderTransitionParams{
			OrderID: ordID, TargetStatus: orderv1.OrderStatus_ORDER_STATUS_CANCELLED,
			CancelReason: "buyer cancelled before payment", PaymentStatus: "CANCELLED",
			ActorID: "buyer-r6", ActorRole: "buyer", Note: "cancel", Now: now,
		}); err != nil {
			t.Fatalf("cancel ord: %v", err)
		}
		beforeIDs := listCancelledOutboxForOrder(t, ordID)

		if err := st.CompleteCheckoutWithPayment(ctx, UpdateCheckoutPaymentParams{
			CheckoutGroupID: gid, PaymentID: "pay-r6-a", PaymentStatus: "PENDING", PaymentExpiresAt: &exp,
		}); err != nil {
			t.Fatalf("CompleteCheckoutWithPayment: %v", err)
		}
		grp, _ := st.GetCheckoutGroupByID(ctx, gid)
		if grp.Status != "FAILED" || grp.TotalPayableVND != 0 {
			t.Fatalf("(a): want FAILED / total=0, got status=%q total=%d", grp.Status, grp.TotalPayableVND)
		}
		afterIDs := listCancelledOutboxForOrder(t, ordID)
		if len(afterIDs) != len(beforeIDs)+1 {
			t.Fatalf("(a) P0-1 violation: expected +1 fresh order.cancelled event after payment exists, before=%v after=%v", beforeIDs, afterIDs)
		}
	})

	t.Run("b_VIETQR_GroupAlreadyClosedFailedBeforeComplete_StillReemitsAndCommitsEvent", func(t *testing.T) {
		gid := "grp-r6-b-" + uuid.NewString()[:8]
		ordID := "ord-r6-b-" + uuid.NewString()[:8]
		exp := now.Add(15 * time.Minute)
		seedPGCheckoutGroupWithOrder(t, st, gid, "buyer-r6", "shop-1", "idem-"+gid, db.InsertOrderParams{
			ID: ordID, OrderCode: "NM-R6-B-" + ordID[len(ordID)-4:], Status: "PENDING",
			PaymentMethod: "VIETQR", PaymentStatus: "PENDING",
			SubtotalVND: 1000000, ShippingFeeVND: 30000, TotalPayableVND: 1030000, PaymentExpiresAt: &exp,
		}, now)
		if _, err := st.CancelCheckoutGroupOrders(ctx, CancelGroupOrdersParams{
			CheckoutGroupID: gid, Reason: "expired before CreatePayment returned",
			ActorID: "watchdog", ActorRole: "system", PaymentStatus: "EXPIRED",
		}); err != nil {
			t.Fatalf("CancelCheckoutGroupOrders: %v", err)
		}
		grpBefore, _ := st.GetCheckoutGroupByID(ctx, gid)
		if grpBefore.Status != "FAILED" {
			t.Fatalf("setup expected FAILED group, got %q", grpBefore.Status)
		}
		beforeIDs := listCancelledOutboxForOrder(t, ordID)

		if err := st.CompleteCheckoutWithPayment(ctx, UpdateCheckoutPaymentParams{
			CheckoutGroupID: gid, PaymentID: "pay-r6-b", PaymentStatus: "PENDING", PaymentExpiresAt: &exp,
		}); err != nil {
			t.Fatalf("CompleteCheckoutWithPayment(already FAILED): %v", err)
		}
		afterIDs := listCancelledOutboxForOrder(t, ordID)
		if len(afterIDs) != len(beforeIDs)+1 {
			t.Fatalf("(b) P0-1 violation: expected +1 fresh order.cancelled event on already-FAILED group, before=%v after=%v", beforeIDs, afterIDs)
		}
		// Group and order rows themselves remain unmutated (N4).
		grpAfter, _ := st.GetCheckoutGroupByID(ctx, gid)
		ordAfter, _ := st.GetOrderWithDetails(ctx, ordID)
		if grpAfter.Status != "FAILED" || grpAfter.PaymentID != "" || ordAfter.Order.PaymentID != "" {
			t.Fatalf("(b) N4 violation: already-FAILED group/order mutated: grp=%+v ord=%+v", grpAfter, ordAfter.Order)
		}
	})

	t.Run("c_COD_TwoShopsOneCancelled_CompletedWithLiveTotalAndCancelledSubOrderEventOnly", func(t *testing.T) {
		gid := "grp-r6-c-" + uuid.NewString()[:8]
		ord1 := "ord-r6-c1-" + uuid.NewString()[:8]
		ord2 := "ord-r6-c2-" + uuid.NewString()[:8]
		_, _ = st.CreateCheckoutGroup(ctx, db.CreateCheckoutGroupParams{
			ID: gid, BuyerID: "buyer-r6", IdempotencyKey: "idem-" + gid,
			RequestHash: "h-r6-c", Status: "IN_PROGRESS", PaymentMethod: "COD", TotalPayableVND: 1560000,
		})
		if _, err := st.PersistCheckoutBatch(ctx, CheckoutBatch{
			CheckoutGroupID: gid,
			Orders: []NewOrderRecord{
				{Order: db.InsertOrderParams{
					ID: ord1, OrderCode: "NM-R6-C1-" + ord1[len(ord1)-4:], CheckoutGroupID: gid,
					BuyerID: "buyer-r6", ShopID: "shop-1", Status: "CONFIRMED",
					PaymentMethod: "COD", PaymentStatus: "PENDING",
					SubtotalVND: 1000000, ShippingFeeVND: 30000, TotalPayableVND: 1030000,
					ShippingAddressJSON: []byte(`{"province_code":"79"}`),
				}},
				{Order: db.InsertOrderParams{
					ID: ord2, OrderCode: "NM-R6-C2-" + ord2[len(ord2)-4:], CheckoutGroupID: gid,
					BuyerID: "buyer-r6", ShopID: "shop-2", Status: "CONFIRMED",
					PaymentMethod: "COD", PaymentStatus: "PENDING",
					SubtotalVND: 500000, ShippingFeeVND: 30000, TotalPayableVND: 530000,
					ShippingAddressJSON: []byte(`{"province_code":"79"}`),
				}},
			},
		}); err != nil {
			t.Fatalf("PersistCheckoutBatch: %v", err)
		}
		// Cancel shop-1 before CreatePayment completes.
		if _, err := st.TransitionOrderStatus(ctx, SingleOrderTransitionParams{
			OrderID: ord1, TargetStatus: orderv1.OrderStatus_ORDER_STATUS_CANCELLED,
			CancelReason: "buyer cancelled shop-1", PaymentStatus: "CANCELLED",
			ActorID: "buyer-r6", ActorRole: "buyer", Note: "cancel", Now: now,
		}); err != nil {
			t.Fatalf("cancel ord1: %v", err)
		}
		before1 := listCancelledOutboxForOrder(t, ord1)
		before2 := listCancelledOutboxForOrder(t, ord2)

		var snapGrp db.CheckoutGroup
		if err := st.CompleteCheckoutWithPayment(ctx, UpdateCheckoutPaymentParams{
			CheckoutGroupID: gid, PaymentID: "pay-r6-c", PaymentStatus: "PENDING",
			BuildSnapshot: func(grp db.CheckoutGroup, _ []OrderAggregate) ([]byte, error) {
				snapGrp = grp
				return []byte(`{"ok":true}`), nil
			},
		}); err != nil {
			t.Fatalf("CompleteCheckoutWithPayment: %v", err)
		}
		grp, _ := st.GetCheckoutGroupByID(ctx, gid)
		if grp.Status != "COMPLETED" || grp.TotalPayableVND != 530000 || snapGrp.TotalPayableVND != 530000 {
			t.Fatalf("(c) P1-2 violation: want COMPLETED with live total=530000, got status=%q dbTotal=%d snapTotal=%d",
				grp.Status, grp.TotalPayableVND, snapGrp.TotalPayableVND)
		}
		after1 := listCancelledOutboxForOrder(t, ord1)
		after2 := listCancelledOutboxForOrder(t, ord2)
		if len(after1) != len(before1)+1 {
			t.Fatalf("(c) P1-2 violation: expected +1 order.cancelled for cancelled ord1, before=%v after=%v", before1, after1)
		}
		if len(after2) != len(before2) {
			t.Fatalf("(c) P1-2 violation: live ord2 must emit 0 order.cancelled events, got %v", after2)
		}
	})

	t.Run("d_NoOrderCancelled_EmitsZeroCancelEvents", func(t *testing.T) {
		gid := "grp-r6-d-" + uuid.NewString()[:8]
		ordID := "ord-r6-d-" + uuid.NewString()[:8]
		seedPGCheckoutGroupWithOrder(t, st, gid, "buyer-r6", "shop-1", "idem-"+gid, db.InsertOrderParams{
			ID: ordID, OrderCode: "NM-R6-D-" + ordID[len(ordID)-4:], Status: "CONFIRMED",
			PaymentMethod: "COD", PaymentStatus: "PENDING",
			SubtotalVND: 300000, ShippingFeeVND: 20000, TotalPayableVND: 320000,
		}, now)
		if err := st.CompleteCheckoutWithPayment(ctx, UpdateCheckoutPaymentParams{
			CheckoutGroupID: gid, PaymentID: "pay-r6-d", PaymentStatus: "PENDING",
		}); err != nil {
			t.Fatalf("CompleteCheckoutWithPayment: %v", err)
		}
		grp, _ := st.GetCheckoutGroupByID(ctx, gid)
		if grp.Status != "COMPLETED" || grp.TotalPayableVND != 320000 {
			t.Fatalf("(d): want COMPLETED / 320000, got %q / %d", grp.Status, grp.TotalPayableVND)
		}
		if ids := listCancelledOutboxForOrder(t, ordID); len(ids) != 0 {
			t.Fatalf("(d): expected 0 order.cancelled events when no order cancelled, got %v", ids)
		}
	})

	t.Run("e_GroupAlreadyCompleted_EmitsZeroCancelEvents", func(t *testing.T) {
		gid := "grp-r6-e-" + uuid.NewString()[:8]
		ordID := "ord-r6-e-" + uuid.NewString()[:8]
		seedPGCheckoutGroupWithOrder(t, st, gid, "buyer-r6", "shop-1", "idem-"+gid, db.InsertOrderParams{
			ID: ordID, OrderCode: "NM-R6-E-" + ordID[len(ordID)-4:], Status: "CONFIRMED",
			PaymentMethod: "COD", PaymentStatus: "PENDING",
			SubtotalVND: 300000, ShippingFeeVND: 20000, TotalPayableVND: 320000,
		}, now)
		if err := st.CompleteCheckoutWithPayment(ctx, UpdateCheckoutPaymentParams{
			CheckoutGroupID: gid, PaymentID: "pay-r6-e", PaymentStatus: "PENDING",
		}); err != nil {
			t.Fatalf("initial CompleteCheckoutWithPayment: %v", err)
		}
		// Simulate an order cancelled afterward (even artificially clearing payment_id) on an already-COMPLETED group:
		// a late retry of CompleteCheckoutWithPayment MUST NOT emit any new order.cancelled event!
		_, _ = pool.Exec(ctx, `UPDATE orders SET status = 'CANCELLED', payment_id = '', payment_status = 'CANCELLED' WHERE id = $1`, ordID)
		beforeIDs := listCancelledOutboxForOrder(t, ordID)

		if err := st.CompleteCheckoutWithPayment(ctx, UpdateCheckoutPaymentParams{
			CheckoutGroupID: gid, PaymentID: "pay-r6-e", PaymentStatus: "PENDING",
		}); err != nil {
			t.Fatalf("late CompleteCheckoutWithPayment on COMPLETED group: %v", err)
		}
		if afterIDs := listCancelledOutboxForOrder(t, ordID); len(afterIDs) != len(beforeIDs) {
			t.Fatalf("(e) violation: already-COMPLETED group must emit 0 new order.cancelled events, before=%v after=%v", beforeIDs, afterIDs)
		}
	})
}

// TestPostgresW3A_R6_P2_2_SingleOrderRefundClamp verifies P2-2 on both PostgresStore and MemoryStore:
// oversized or repeated single-order refund events (different event_id) clamp delta to TotalPayableVND - RefundedVND
// so refunded_vnd and settlement_refunded_vnd never exceed TotalPayableVND.
func TestPostgresW3A_R6_P2_2_SingleOrderRefundClamp(t *testing.T) {
	st, _ := openTempPostgresStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	runClampCheck := func(t *testing.T, label string, target Store) {
		t.Helper()
		gid := "grp-clamp-" + label + "-" + uuid.NewString()[:8]
		ordID := "ord-clamp-" + label + "-" + uuid.NewString()[:8]
		_, _ = target.CreateCheckoutGroup(ctx, db.CreateCheckoutGroupParams{
			ID: gid, BuyerID: "buyer-clamp", IdempotencyKey: "idem-" + gid,
			RequestHash: "h", Status: "IN_PROGRESS", PaymentMethod: "VIETQR", TotalPayableVND: 200000,
		})
		_, err := target.PersistCheckoutBatch(ctx, CheckoutBatch{
			CheckoutGroupID: gid,
			Orders: []NewOrderRecord{{Order: db.InsertOrderParams{
				ID: ordID, OrderCode: "NM-CLAMP-" + ordID[len(ordID)-4:], CheckoutGroupID: gid,
				BuyerID: "buyer-clamp", ShopID: "shop-1", Status: "COMPLETED",
				PaymentMethod: "VIETQR", PaymentID: "pay-clamp", PaymentStatus: "SUCCEEDED",
				SubtotalVND: 200000, TotalPayableVND: 200000, ShippingAddressJSON: []byte(`{}`),
			}}},
		})
		if err != nil {
			t.Fatalf("%s PersistCheckoutBatch: %v", label, err)
		}
		pre := false
		// Event 1 refunds 150,000; Event 2 tries another 150,000 (different event_id!) -> clamped to remaining 50,000.
		if _, err := target.RecordPaymentRefunded(ctx, PaymentRefundedParams{
			EventID: "ev-clamp-1-" + ordID, EventType: events.SubjectPaymentRefunded,
			RefundID: "rf-1", PaymentID: "pay-clamp", OrderID: ordID, AmountVND: 150000, IsPostSettlement: &pre,
		}); err != nil {
			t.Fatalf("%s refund 1: %v", label, err)
		}
		if _, err := target.RecordPaymentRefunded(ctx, PaymentRefundedParams{
			EventID: "ev-clamp-2-" + ordID, EventType: events.SubjectPaymentRefunded,
			RefundID: "rf-1", PaymentID: "pay-clamp", OrderID: ordID, AmountVND: 150000, IsPostSettlement: &pre,
		}); err != nil {
			t.Fatalf("%s refund 2: %v", label, err)
		}
		agg, err := target.GetOrderWithDetails(ctx, ordID)
		if err != nil {
			t.Fatalf("%s GetOrderWithDetails: %v", label, err)
		}
		if agg.Order.RefundedVND != 200000 || agg.Order.SettlementRefundedVND != 200000 || agg.Order.PaymentStatus != "REFUNDED" {
			t.Fatalf("%s P2-2 violation: expected RefundedVND=200000 SettlementRefundedVND=200000 REFUNDED, got %+v", label, agg.Order)
		}
		_ = now
	}

	runClampCheck(t, "postgres", st)
	runClampCheck(t, "memory", NewMemoryStore())
}
