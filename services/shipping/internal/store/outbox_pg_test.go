package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/nexusmart/core/pkg/common/jetstream"
	"github.com/nexusmart/core/pkg/common/secretbox"
	"github.com/nexusmart/core/services/shipping/migrations"
)

func openIsolatedShippingTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_SHIPPING_DB_URL"))
	if dsn == "" {
		t.Skip("set TEST_SHIPPING_DB_URL to run Postgres shipping tests")
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
	dbName := "ship_store_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if _, err := adminConn.Exec(ctx, "CREATE DATABASE "+dbName); err != nil {
		_ = adminConn.Close(ctx)
		t.Fatalf("create isolated test db %s: %v", dbName, err)
	}
	_ = adminConn.Close(ctx)

	testURL := *u
	testURL.Path = "/" + dbName
	pool, err := pgxpool.New(ctx, testURL.String())
	if err != nil {
		dropIsolatedShippingDB(adminURL.String(), dbName)
		t.Fatalf("pgxpool.New(%s): %v", dbName, err)
	}
	t.Cleanup(func() {
		pool.Close()
		dropIsolatedShippingDB(adminURL.String(), dbName)
	})
	return pool
}

func dropIsolatedShippingDB(adminDSN, dbName string) {
	dropCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := pgx.Connect(dropCtx, adminDSN)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close(dropCtx) }()
	_, _ = conn.Exec(dropCtx, "DROP DATABASE IF EXISTS "+dbName+" WITH (FORCE)")
}

func TestOutboxHelpersUnit(t *testing.T) {
	if got := OutboxAggregateKey(OutboxEvent{AggregateID: " sh-explicit ", Payload: []byte(`{"order_id":"ord-json","shipment_id":"sh-json"}`)}); got != "sh-explicit" {
		t.Fatalf("explicit AggregateID = %q, want sh-explicit", got)
	}
	if got := OutboxAggregateKey(OutboxEvent{Payload: []byte(`{"order_id":"ord-json","shipment_id":"sh-json"}`)}); got != "sh-json" {
		t.Fatalf("json shipment_id fallback = %q, want sh-json", got)
	}
	if got := OutboxAggregateKey(OutboxEvent{Payload: []byte(`{"order_id":"ord-only"}`)}); got != "ord-only" {
		t.Fatalf("json order_id legacy fallback = %q, want ord-only", got)
	}
	if got := OutboxAggregateKey(OutboxEvent{Payload: []byte(`not-json`)}); got != "" {
		t.Fatalf("invalid json fallback = %q, want empty", got)
	}

	t0 := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	if !OutboxEventBefore(
		OutboxEvent{ID: "z", CreatedAt: t0.Add(10 * time.Second), Seq: 1},
		OutboxEvent{ID: "a", CreatedAt: t0, Seq: 2},
	) {
		t.Fatalf("smaller Seq must sort before larger Seq even when CreatedAt is later")
	}
	if OutboxEventBefore(
		OutboxEvent{ID: "a", CreatedAt: t0, Seq: 2},
		OutboxEvent{ID: "z", CreatedAt: t0.Add(10 * time.Second), Seq: 1},
	) {
		t.Fatalf("larger Seq must not sort before smaller Seq even when CreatedAt is earlier")
	}
	if !OutboxEventBefore(
		OutboxEvent{ID: "a", CreatedAt: t0, Seq: 5},
		OutboxEvent{ID: "b", CreatedAt: t0, Seq: 5},
	) {
		t.Fatalf("smaller ID must break equal-Seq ties")
	}
	if !jetstream.IsInfraError(nats.ErrTimeout) || jetstream.IsInfraError(errors.New("marshal failure")) {
		t.Fatalf("unexpected jetstream.IsInfraError classification")
	}
}

// TestPGStoreW3A_D9_OutboxLeaseAndOrdering verifies D9/D9-bis transactional outbox guarantees and
// legacy carrier token readability against an isolated PostgreSQL DB when TEST_SHIPPING_DB_URL is set.
func TestPGStoreW3A_D9_OutboxLeaseAndOrdering(t *testing.T) {
	pool := openIsolatedShippingTestPool(t)
	ctx := context.Background()

	baseStore := NewPGStore(pool)
	if err := baseStore.MigrateAndSeed(ctx); err != nil {
		t.Fatalf("MigrateAndSeed: %v", err)
	}

	// Ensure a clean outbox table before/after this test so concurrent/order checks are isolated.
	cleanOutbox := func(t *testing.T) {
		t.Helper()
		if _, err := pool.Exec(context.Background(), `DELETE FROM outbox_events`); err != nil {
			t.Fatalf("clean outbox_events: %v", err)
		}
	}
	cleanOutbox(t)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM outbox_events`)
	})

	insertOutbox := func(t *testing.T, eventID, aggID string, createdAt time.Time) string {
		t.Helper()
		rowID := uuid.NewString()
		payload, _ := json.Marshal(map[string]string{
			"shipment_id": "sh-" + aggID,
			"order_id":    aggID,
			"event_id":    eventID,
			"status":      "delivering",
		})
		if _, err := pool.Exec(ctx, `
			INSERT INTO outbox_events (id, event_id, subject, aggregate_id, payload, traceparent, trace_id, created_at)
			VALUES ($1, $2, 'shipment.updated', $3, $4::jsonb, '00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01', '4bf92f3577b34da6a3ce929d0e0e4736', $5)
		`, rowID, eventID, aggID, string(payload), createdAt); err != nil {
			t.Fatalf("insert outbox %s: %v", eventID, err)
		}
		return rowID
	}

	t.Run("PublishOutsideTransaction_NoLocksHeldDuringPublish", func(t *testing.T) {
		cleanOutbox(t)
		st := NewPGStore(pool)
		rowID := insertOutbox(t, "ev-outtx-1", "ord-outtx-1", time.Now().UTC().Add(-time.Minute))

		var lockCountDuringPublish int64
		var nowaitErr error
		var seenUntil *time.Time
		var seenToken string

		n, err := st.ProcessPendingOutbox(ctx, 10, func(pubCtx context.Context, ev OutboxEvent) error {
			if err := pool.QueryRow(pubCtx, `
				SELECT COUNT(*)
				FROM pg_locks l
				JOIN pg_class c ON c.oid = l.relation
				WHERE c.relname = 'outbox_events'
				  AND l.mode IN ('RowExclusiveLock', 'ShareRowExclusiveLock', 'ExclusiveLock', 'AccessExclusiveLock')
				  AND l.granted = true
			`).Scan(&lockCountDuringPublish); err != nil {
				return err
			}

			// Verify from a separate connection that NO row lock is held on the claimed row
			// AND that claimed_until + claim_token are ALREADY committed and visible!
			tx, bErr := pool.Begin(pubCtx)
			if bErr != nil {
				return bErr
			}
			defer func() { _ = tx.Rollback(pubCtx) }()
			nowaitErr = tx.QueryRow(
				pubCtx,
				`SELECT claimed_until, claim_token FROM outbox_events WHERE id = $1 FOR UPDATE NOWAIT`,
				ev.ID,
			).Scan(&seenUntil, &seenToken)
			return nil
		})
		if err != nil || n != 1 {
			t.Fatalf("ProcessPendingOutbox = (%d, %v), want (1, nil)", n, err)
		}
		if lockCountDuringPublish != 0 {
			t.Fatalf("D9 violation: held %d write locks on outbox_events during publishFn", lockCountDuringPublish)
		}
		if nowaitErr != nil {
			t.Fatalf("D9 violation: row lock held during publishFn (FOR UPDATE NOWAIT failed): %v", nowaitErr)
		}
		if seenUntil == nil || !seenUntil.After(time.Now().UTC().Add(-time.Second)) || seenToken == "" {
			t.Fatalf("D9 violation: expected committed future claimed_until & non-empty claim_token during publish, got until=%v tok=%q", seenUntil, seenToken)
		}

		var pubAt *time.Time
		var afterToken string
		var attempts int32
		if err := pool.QueryRow(ctx, `SELECT published_at, claim_token, attempts FROM outbox_events WHERE id = $1`, rowID).
			Scan(&pubAt, &afterToken, &attempts); err != nil {
			t.Fatalf("query post-publish row: %v", err)
		}
		if pubAt == nil || afterToken != "" || attempts != 1 {
			t.Fatalf("unexpected post-publish row: pubAt=%v token=%q attempts=%d", pubAt, afterToken, attempts)
		}
	})

	t.Run("ConcurrentWorkers_NoDuplicateClaim_And_AggregateOrderPreserved", func(t *testing.T) {
		cleanOutbox(t)
		st := NewPGStore(pool)

		// Insert two events for ord-A in ONE transaction (identical created_at, distinct seq!) plus one event for ord-B.
		sameTS := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Microsecond)
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin seed tx: %v", err)
		}
		for _, spec := range []struct {
			eventID string
			aggID   string
		}{
			{"ev-conc-a1", "ord-conc-A"},
			{"ev-conc-a2", "ord-conc-A"},
			{"ev-conc-b1", "ord-conc-B"},
		} {
			p, _ := json.Marshal(map[string]string{"order_id": spec.aggID, "event_id": spec.eventID})
			if _, err := tx.Exec(ctx, `
				INSERT INTO outbox_events (id, event_id, subject, aggregate_id, payload, created_at)
				VALUES ($1, $2, 'shipment.updated', $3, $4::jsonb, $5)
			`, uuid.NewString(), spec.eventID, spec.aggID, string(p), sameTS); err != nil {
				_ = tx.Rollback(ctx)
				t.Fatalf("insert %s: %v", spec.eventID, err)
			}
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit seed tx: %v", err)
		}

		worker1Started := make(chan struct{})
		releaseWorker1 := make(chan struct{})
		var mu sync.Mutex
		var publishedIDs []string

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Worker 1 claims ONLY ev-conc-a1 (limit=1) and blocks mid-publish.
			n1, wErr := st.ProcessPendingOutbox(ctx, 1, func(_ context.Context, ev OutboxEvent) error {
				close(worker1Started)
				<-releaseWorker1
				mu.Lock()
				publishedIDs = append(publishedIDs, ev.EventID)
				mu.Unlock()
				return nil
			})
			if wErr != nil || n1 != 1 {
				t.Errorf("worker 1 = (%d, %v), want (1, nil)", n1, wErr)
			}
		}()

		<-worker1Started

		// While Worker 1 holds active lease on ev-conc-a1, Worker 2 MUST NOT re-claim ev-conc-a1
		// AND MUST NOT claim ev-conc-a2 (same aggregate ord-conc-A), publishing ONLY ev-conc-b1!
		n2, err := st.ProcessPendingOutbox(ctx, 10, func(_ context.Context, ev OutboxEvent) error {
			mu.Lock()
			publishedIDs = append(publishedIDs, ev.EventID)
			mu.Unlock()
			return nil
		})
		if err != nil || n2 != 1 {
			close(releaseWorker1)
			wg.Wait()
			t.Fatalf("worker 2 while ev-conc-a1 leased: got n=%d err=%v published=%v, want n=1 [ev-conc-b1]", n2, err, publishedIDs)
		}

		close(releaseWorker1)
		wg.Wait()

		// Drain ev-conc-a2 after ev-conc-a1 finishes.
		n3, err := st.ProcessPendingOutbox(ctx, 10, func(_ context.Context, ev OutboxEvent) error {
			mu.Lock()
			publishedIDs = append(publishedIDs, ev.EventID)
			mu.Unlock()
			return nil
		})
		if err != nil || n3 != 1 {
			t.Fatalf("drain ev-conc-a2 = (%d, %v), want (1, nil)", n3, err)
		}

		want := []string{"ev-conc-b1", "ev-conc-a1", "ev-conc-a2"}
		if fmt.Sprint(publishedIDs) != fmt.Sprint(want) {
			t.Fatalf("published order = %v, want %v", publishedIDs, want)
		}
	})

	t.Run("CrashRecovery_LeaseExpiryReclaim_And_StaleClaimTokenGuard", func(t *testing.T) {
		cleanOutbox(t)
		st := NewPGStore(pool)
		base := time.Now().UTC().Add(-3 * time.Minute)
		rowC1 := insertOutbox(t, "ev-crash-1", "ord-crash", base)
		insertOutbox(t, "ev-crash-2", "ord-crash", base.Add(time.Second))
		insertOutbox(t, "ev-free-1", "ord-free", base.Add(2*time.Second))

		// Worker 1 claims only ev-crash-1 and "crashes" without marking published.
		claimed1, staleToken, err := st.ClaimPendingOutbox(ctx, 1)
		if err != nil || len(claimed1) != 1 || claimed1[0].EventID != "ev-crash-1" || staleToken == "" {
			t.Fatalf("worker 1 claim: got=%+v token=%q err=%v", claimed1, staleToken, err)
		}

		// While lease is active, Worker 2 skips ev-crash-1 & ev-crash-2, publishing only ev-free-1.
		var w2IDs []string
		n2, err := st.ProcessPendingOutbox(ctx, 10, func(_ context.Context, ev OutboxEvent) error {
			w2IDs = append(w2IDs, ev.EventID)
			return nil
		})
		if err != nil || n2 != 1 || fmt.Sprint(w2IDs) != "[ev-free-1]" {
			t.Fatalf("worker 2 while leased = (%d, %v, %v), want (1, [ev-free-1], nil)", n2, w2IDs, err)
		}

		// Simulate lease expiration in Postgres -> Worker 2 reclaims and publishes [ev-crash-1, ev-crash-2] in order!
		if _, err := pool.Exec(ctx, `UPDATE outbox_events SET claimed_until = NOW() - INTERVAL '1 second' WHERE id = $1`, rowC1); err != nil {
			t.Fatalf("expire lease: %v", err)
		}
		var recovered []string
		nRec, err := st.ProcessPendingOutbox(ctx, 10, func(_ context.Context, ev OutboxEvent) error {
			recovered = append(recovered, ev.EventID)
			return nil
		})
		if err != nil || nRec != 2 || fmt.Sprint(recovered) != "[ev-crash-1 ev-crash-2]" {
			t.Fatalf("reclaim after lease expiry = (%d, %v, %v), want (2, [ev-crash-1 ev-crash-2], nil)", nRec, recovered, err)
		}

		// Zombie Worker 1 waking up with staleToken MUST affect 0 rows (marked=false, attempts stays 1).
		zombieMarked, err := st.MarkOutboxPublished(ctx, rowC1, staleToken)
		if err != nil || zombieMarked {
			t.Fatalf("zombie MarkOutboxPublished = (%v, %v), want (false, nil)", zombieMarked, err)
		}
		var attempts int32
		if err := pool.QueryRow(ctx, `SELECT attempts FROM outbox_events WHERE id = $1`, rowC1).Scan(&attempts); err != nil {
			t.Fatalf("query attempts: %v", err)
		}
		if attempts != 1 {
			t.Fatalf("stale token guard violation: attempts = %d, want 1", attempts)
		}

		// Also verify real-time stalled ProcessPendingOutbox waking up after lease expiry counts 0 published.
		// Uses second-scale lease (3s) & publish timeout (1s) so heavy host CPU load never flakes.
		cleanOutbox(t)
		shortStore := NewPGStore(pool)
		shortStore.SetOutboxLease(3 * time.Second)
		shortStore.SetOutboxPublishTimeout(1 * time.Second)
		rowStall := insertOutbox(t, "ev-stall-1", "ord-stall", base.Add(10*time.Second))

		stalledStarted := make(chan struct{})
		releaseStalled := make(chan struct{})
		var stalledN int
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			var sErr error
			stalledN, sErr = shortStore.ProcessPendingOutbox(ctx, 5, func(_ context.Context, _ OutboxEvent) error {
				close(stalledStarted)
				<-releaseStalled
				return nil
			})
			if sErr != nil {
				t.Errorf("stalled worker err: %v", sErr)
			}
		}()

		<-stalledStarted
		if nImm, err := shortStore.ProcessPendingOutbox(ctx, 5, func(_ context.Context, _ OutboxEvent) error { return nil }); err != nil || nImm != 0 {
			close(releaseStalled)
			wg.Wait()
			t.Fatalf("expected 0 claimed before lease expiry, got (%d, %v)", nImm, err)
		}

		time.Sleep(3200 * time.Millisecond)
		var reclaimed atomic.Int32
		nReclaim, err := shortStore.ProcessPendingOutbox(ctx, 5, func(_ context.Context, ev OutboxEvent) error {
			if ev.ID == rowStall {
				reclaimed.Add(1)
			}
			return nil
		})
		if err != nil || nReclaim != 1 || reclaimed.Load() != 1 {
			close(releaseStalled)
			wg.Wait()
			t.Fatalf("expected reclaim after 3s lease expiry, got n=%d reclaimed=%d err=%v", nReclaim, reclaimed.Load(), err)
		}

		close(releaseStalled)
		wg.Wait()
		if stalledN != 0 {
			t.Fatalf("stalled worker with stale token counted published=%d, want 0", stalledN)
		}
		if err := pool.QueryRow(ctx, `SELECT attempts FROM outbox_events WHERE id = $1`, rowStall).Scan(&attempts); err != nil {
			t.Fatalf("query stalled attempts: %v", err)
		}
		if attempts != 1 {
			t.Fatalf("stalled worker incremented attempts to %d, want 1", attempts)
		}
	})

	t.Run("FailureBlocksSameAggregateInBatch_AllowsOtherAggregates", func(t *testing.T) {
		cleanOutbox(t)
		st := NewPGStore(pool)
		base := time.Now().UTC().Add(-4 * time.Minute)
		rowA1 := insertOutbox(t, "ev-fail-a1", "ord-fail-A", base)
		rowA2 := insertOutbox(t, "ev-fail-a2", "ord-fail-A", base.Add(time.Second))
		insertOutbox(t, "ev-ok-b1", "ord-ok-B", base.Add(2*time.Second))

		var pass1 []string
		n1, err := st.ProcessPendingOutbox(ctx, 10, func(_ context.Context, ev OutboxEvent) error {
			pass1 = append(pass1, ev.EventID)
			if ev.EventID == "ev-fail-a1" {
				return errors.New("non-infra per-event publish error")
			}
			return nil
		})
		if err != nil || n1 != 1 {
			t.Fatalf("pass 1 = (%d, %v), want (1, nil)", n1, err)
		}
		if fmt.Sprint(pass1) != "[ev-fail-a1 ev-ok-b1]" {
			t.Fatalf("pass 1 attempted %v, want [ev-fail-a1 ev-ok-b1] (ev-fail-a2 must not overtake!)", pass1)
		}

		var a1Att, a2Att int32
		var a1Err, a1Tok, a2Tok string
		var a1Until, a2Until *time.Time
		_ = pool.QueryRow(ctx, `SELECT attempts, last_error, claim_token, claimed_until FROM outbox_events WHERE id = $1`, rowA1).Scan(&a1Att, &a1Err, &a1Tok, &a1Until)
		_ = pool.QueryRow(ctx, `SELECT attempts, claim_token, claimed_until FROM outbox_events WHERE id = $1`, rowA2).Scan(&a2Att, &a2Tok, &a2Until)
		if a1Att != 1 || a1Err == "" || a1Tok != "" || a1Until == nil || !a1Until.After(time.Now().UTC()) || a2Att != 0 || a2Tok != "" || a2Until != nil {
			t.Fatalf("unexpected state after aggregate failure: a1=(att=%d err=%q tok=%q until=%v) a2=(att=%d tok=%q until=%v)", a1Att, a1Err, a1Tok, a1Until, a2Att, a2Tok, a2Until)
		}

		// Immediate retry while rowA1 is in 2s backoff must claim 0 rows.
		if nImm, err := st.ProcessPendingOutbox(ctx, 10, func(_ context.Context, _ OutboxEvent) error { return nil }); err != nil || nImm != 0 {
			t.Fatalf("immediate retry during backoff = (%d, %v), want (0, nil)", nImm, err)
		}

		// Expire backoff and verify [ev-fail-a1, ev-fail-a2] drain in strict seq order.
		if _, err := pool.Exec(ctx, `UPDATE outbox_events SET claimed_until = NOW() - INTERVAL '1 second' WHERE id = $1`, rowA1); err != nil {
			t.Fatalf("expire rowA1 backoff: %v", err)
		}
		var pass2 []string
		n2, err := st.ProcessPendingOutbox(ctx, 10, func(_ context.Context, ev OutboxEvent) error {
			pass2 = append(pass2, ev.EventID)
			return nil
		})
		if err != nil || n2 != 2 || fmt.Sprint(pass2) != "[ev-fail-a1 ev-fail-a2]" {
			t.Fatalf("pass 2 = (%d, %v, %v), want (2, [ev-fail-a1 ev-fail-a2], nil)", n2, pass2, err)
		}
	})

	t.Run("SlowPublisherStopsBeforeLeaseExpiry_ReleasesRemaining", func(t *testing.T) {
		cleanOutbox(t)
		slowStore := NewPGStore(pool)
		var clkMu sync.Mutex
		fakeNow := time.Now().UTC()
		getNow := func() time.Time {
			clkMu.Lock()
			defer clkMu.Unlock()
			return fakeNow
		}
		advance := func(d time.Duration) time.Time {
			clkMu.Lock()
			defer clkMu.Unlock()
			fakeNow = fakeNow.Add(d)
			return fakeNow
		}
		slowStore.SetOutboxLease(30 * time.Second)
		slowStore.SetOutboxPublishTimeout(5 * time.Second)
		slowStore.SetOutboxClock(getNow)

		const total = 10
		base := time.Now().UTC().Add(-5 * time.Minute)
		rowIDs := make([]string, total)
		wantEventIDs := make([]string, total)
		for i := 0; i < total; i++ {
			evID := fmt.Sprintf("ev-slow-%02d", i)
			wantEventIDs[i] = evID
			rowIDs[i] = insertOutbox(t, evID, "ord-slow", base.Add(time.Duration(i)*time.Second))
		}

		// 30s lease, 5s max PubAck timeout, 4s per event -> 7 events (28s elapsed, 2s < 5s left), stops before expiry!
		deadline1 := getNow().Add(30 * time.Second)
		var published []string
		var overran bool
		n1, err := slowStore.ProcessPendingOutbox(ctx, 50, func(_ context.Context, ev OutboxEvent) error {
			if advance(4 * time.Second).After(deadline1) {
				overran = true
			}
			published = append(published, ev.EventID)
			return nil
		})
		if err != nil {
			t.Fatalf("slow batch 1 err: %v", err)
		}
		if overran {
			t.Fatalf("D9 violation: worker published after lease deadline (published=%d)", n1)
		}
		if n1 != 7 {
			t.Fatalf("slow batch 1 published=%d, want 7", n1)
		}

		// Remaining 3 rows MUST be released immediately with attempts=0.
		for _, id := range rowIDs[7:] {
			var att int32
			var tok string
			var until *time.Time
			if err := pool.QueryRow(ctx, `SELECT attempts, claim_token, claimed_until FROM outbox_events WHERE id = $1`, id).Scan(&att, &tok, &until); err != nil {
				t.Fatalf("query released row %s: %v", id, err)
			}
			if tok != "" || until != nil || att != 0 {
				t.Fatalf("remaining row %s not cleanly released: att=%d tok=%q until=%v", id, att, tok, until)
			}
		}

		n2, err := slowStore.ProcessPendingOutbox(ctx, 50, func(_ context.Context, ev OutboxEvent) error {
			advance(4 * time.Second)
			published = append(published, ev.EventID)
			return nil
		})
		if err != nil || n2 != 3 || fmt.Sprint(published) != fmt.Sprint(wantEventIDs) {
			t.Fatalf("slow batch 2 = (%d, %v), published=%v want=%v", n2, err, published, wantEventIDs)
		}
	})

	t.Run("TwoConcurrentWorkers_ShortLease_SlowPublisher_NoOverlapOrDuplicates", func(t *testing.T) {
		cleanOutbox(t)
		concStore := NewPGStore(pool)
		concStore.SetOutboxLease(3 * time.Second)
		concStore.SetOutboxPublishTimeout(1 * time.Second)

		const total = 10
		base := time.Now().UTC().Add(-6 * time.Minute)
		wantIDs := make([]string, total)
		for i := 0; i < total; i++ {
			evID := fmt.Sprintf("ev-race-%02d", i)
			wantIDs[i] = evID
			insertOutbox(t, evID, "ord-race-slow", base.Add(time.Duration(i)*time.Second))
		}

		var mu sync.Mutex
		var firstSeen []string
		seenMsgID := make(map[string]string, total)
		var dupMsgIDMismatch bool
		pubFn := func(_ context.Context, ev OutboxEvent) error {
			time.Sleep(350 * time.Millisecond)

			mu.Lock()
			if prev, ok := seenMsgID[ev.ID]; !ok {
				seenMsgID[ev.ID] = ev.EventID
				firstSeen = append(firstSeen, ev.EventID)
			} else if prev != ev.EventID {
				dupMsgIDMismatch = true
			}
			mu.Unlock()
			return nil
		}

		workCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		var wg sync.WaitGroup
		worker := func() {
			defer wg.Done()
			for workCtx.Err() == nil {
				_, _ = concStore.ProcessPendingOutbox(workCtx, 50, pubFn)
				var left int64
				if err := pool.QueryRow(workCtx, `SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = 'ord-race-slow' AND published_at IS NULL`).Scan(&left); err == nil && left == 0 {
					return
				}
				time.Sleep(100 * time.Millisecond)
			}
		}
		wg.Add(2)
		go worker()
		go worker()
		wg.Wait()

		// Per D9-bis #7: redelivery after lease expiry under heavy host stall is valid as long as
		// all events are delivered, duplicates carry the same Nats-Msg-Id (EventID), and FIRST delivery order is strictly preserved.
		if dupMsgIDMismatch {
			t.Fatalf("D9 violation: duplicate delivery used different Nats-Msg-Id/EventID")
		}
		if fmt.Sprint(firstSeen) != fmt.Sprint(wantIDs) {
			t.Fatalf("first-delivery order=%v, want strict seq order %v", firstSeen, wantIDs)
		}
	})

	t.Run("NATSInfraError_AbortsBatchAfterFirstAttempt_And_ReleasesRemaining", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			err  error
		}{
			{"ErrTimeout", nats.ErrTimeout},
			{"ErrDisconnected", nats.ErrDisconnected},
			{"ErrConnectionClosed", nats.ErrConnectionClosed},
			{"ErrNoResponders", nats.ErrNoResponders},
			{"ErrNoStreamResponse", nats.ErrNoStreamResponse},
		} {
			t.Run(tc.name, func(t *testing.T) {
				cleanOutbox(t)
				st := NewPGStore(pool)
				base := time.Now().UTC().Add(-7 * time.Minute)
				rowIDs := make([]string, 4)
				for i := 0; i < 4; i++ {
					rowIDs[i] = insertOutbox(t, fmt.Sprintf("ev-infra-%s-%d", tc.name, i), fmt.Sprintf("ord-infra-%d", i), base.Add(time.Duration(i)*time.Second))
				}

				attempts := 0
				n, pubErr := st.ProcessPendingOutbox(ctx, 10, func(_ context.Context, _ OutboxEvent) error {
					attempts++
					return fmt.Errorf("broker down: %w", tc.err)
				})
				if n != 0 || !errors.Is(pubErr, tc.err) {
					t.Fatalf("want n=0 err=%v, got n=%d err=%v", tc.err, n, pubErr)
				}
				if attempts != 1 {
					t.Fatalf("on %s: expected 1 attempt (immediate abort), got %d", tc.name, attempts)
				}
				for i, id := range rowIDs {
					var att int32
					var tok string
					var until *time.Time
					if err := pool.QueryRow(ctx, `SELECT attempts, claim_token, claimed_until FROM outbox_events WHERE id = $1`, id).Scan(&att, &tok, &until); err != nil {
						t.Fatalf("query row %d: %v", i, err)
					}
					if tok != "" || until != nil {
						t.Fatalf("row %d still leased after infra abort: tok=%q until=%v", i, tok, until)
					}
					if i == 0 && att != 1 {
						t.Fatalf("row 0 attempts=%d, want 1", att)
					}
					if i > 0 && att != 0 {
						t.Fatalf("row %d attempts=%d, want 0 (must be released without attempt)", i, att)
					}
				}
				// Recovery drains all 4 rows cleanly.
				nRec, err := st.ProcessPendingOutbox(ctx, 10, func(_ context.Context, _ OutboxEvent) error { return nil })
				if err != nil || nRec != 4 {
					t.Fatalf("recovery drain = (%d, %v), want (4, nil)", nRec, err)
				}
			})
		}
	})

	t.Run("ApplyWebhookEvent_PopulatesAggregateID_MigrationIdempotent_And_LegacyCarrierTokenPG", func(t *testing.T) {
		cleanOutbox(t)
		st := NewPGStore(pool)
		suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
		ordID := "ord-wh-agg-" + suffix
		tracking := "TRK-W3A-" + suffix
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(), `DELETE FROM shipments WHERE order_id = $1`, ordID)
			_, _ = pool.Exec(context.Background(), `DELETE FROM webhook_events WHERE tracking_code = $1`, tracking)
			_, _ = pool.Exec(context.Background(), `DELETE FROM carrier_configs WHERE carrier_code = $1`, "legacy_test_"+suffix)
		})

		now := time.Now().UTC().Truncate(time.Second)
		shipID := uuid.NewString()
		if _, _, err := st.CreateShipment(ctx, &Shipment{
			ID: shipID, OrderID: ordID, ShopID: "shop-w3a", BuyerID: "buyer-w3a",
			Direction: DirectionForward, CarrierCode: "ghn", ServiceCode: "STANDARD",
			TrackingCode: tracking, Status: "created", FeeVND: 22000, WeightGrams: 500,
			CreatedAt: now, UpdatedAt: now,
		}, nil); err != nil {
			t.Fatalf("CreateShipment: %v", err)
		}

		outboxEvID := "wh-outbox-" + suffix
		_, outcome, err := st.ApplyWebhookEvent(ctx, WebhookUpdateParams{
			EventID:        "wh-evt-" + suffix,
			CarrierCode:    "ghn",
			TrackingCode:   tracking,
			OrderID:        ordID,
			InternalStatus: "delivering",
			RawStatus:      "delivering",
			Description:    "Đang giao hàng",
			OccurredAt:     now.Add(time.Second),
			OutboxEventID:  outboxEvID,
			OutboxSubject:  "shipment.updated",
			BuildOutboxPayload: func(sh *Shipment) ([]byte, error) {
				return json.Marshal(map[string]string{"shipment_id": sh.ID, "order_id": sh.OrderID, "status": sh.Status})
			},
		})
		if err != nil || outcome != WebhookOutcomeApplied {
			t.Fatalf("ApplyWebhookEvent = (%s, %v), want applied", outcome, err)
		}

		var gotAgg string
		var gotSeq int64
		if err := pool.QueryRow(ctx, `SELECT aggregate_id, seq FROM outbox_events WHERE event_id = $1`, outboxEvID).Scan(&gotAgg, &gotSeq); err != nil {
			t.Fatalf("query webhook outbox row: %v", err)
		}
		if gotAgg != shipID || gotSeq <= 0 {
			t.Fatalf("webhook outbox row got aggregate_id=%q seq=%d, want shipment_id %q seq>0", gotAgg, gotSeq, shipID)
		}

		// Re-running MigrateAndSeed with populated outbox_events must be 100% idempotent (no BIGSERIAL sequence error!).
		if err := st.MigrateAndSeed(ctx); err != nil {
			t.Fatalf("repeat MigrateAndSeed with outbox rows failed: %v", err)
		}
		if err := st.MigrateAndSeed(ctx); err != nil {
			t.Fatalf("third MigrateAndSeed failed: %v", err)
		}

		// Verify legacy AES-256-GCM carrier token stored in carrier_configs BYTEA is decrypted cleanly by pkg/common/secretbox
		// and preserved by SeedCarrierTokenIfEmpty.
		const (
			devKeyB64          = "ZGV2LW9ubHktc2hpcHBpbmctY29uZmlnLWtleS0zMmI="
			legacyFixtureB64   = "ABEiM0RVZneImaq7TlU5RzSvZ/GeBsb3kSirRjZIrzDkiA1BqE6j9wNu7BAGAiM61ibmd30N9wRX"
			legacyFixturePlain = "ghn-legacy-carrier-token-2026"
		)
		legacyBlob, err := base64.StdEncoding.DecodeString(legacyFixtureB64)
		if err != nil {
			t.Fatalf("decode legacy fixture: %v", err)
		}
		testCarrier := "legacy_test_" + suffix
		if _, err := pool.Exec(ctx, `
			INSERT INTO carrier_configs (carrier_code, name, enabled, encrypted_token)
			VALUES ($1, 'Legacy Test Carrier', TRUE, $2)
		`, testCarrier, legacyBlob); err != nil {
			t.Fatalf("insert legacy carrier_config: %v", err)
		}
		box, err := secretbox.NewFromBase64(devKeyB64, "SHIPPING_CONFIG_KEY")
		if err != nil {
			t.Fatalf("secretbox.NewFromBase64: %v", err)
		}
		otherEnc, err := box.Encrypt("should-not-overwrite")
		if err != nil {
			t.Fatalf("box.Encrypt: %v", err)
		}
		if err := st.SeedCarrierTokenIfEmpty(ctx, testCarrier, otherEnc); err != nil {
			t.Fatalf("SeedCarrierTokenIfEmpty: %v", err)
		}
		cfg, err := st.GetCarrierConfig(ctx, testCarrier)
		if err != nil {
			t.Fatalf("GetCarrierConfig: %v", err)
		}
		plain, err := box.Decrypt(cfg.EncryptedToken)
		if err != nil || plain != legacyFixturePlain {
			t.Fatalf("PG round-trip legacy token decrypt = (%q, %v), want %q", plain, err, legacyFixturePlain)
		}
	})

	t.Run("W3A_D9bis_CreatedAtInvertedVsSeq_OrdersBySeqOnly", func(t *testing.T) {
		cleanOutbox(t)
		st := NewPGStore(pool)
		tBase := time.Now().UTC().Add(-2 * time.Minute)
		// Insert E1 first (smaller seq) with LATER created_at, then E2 (larger seq) with EARLIER created_at.
		insertOutbox(t, "ev-seq-1", "ord-seq-inv", tBase.Add(10*time.Second))
		insertOutbox(t, "ev-seq-2", "ord-seq-inv", tBase)

		var got []string
		n, err := st.ProcessPendingOutbox(ctx, 10, func(_ context.Context, ev OutboxEvent) error {
			got = append(got, ev.EventID)
			return nil
		})
		if err != nil || n != 2 || fmt.Sprint(got) != "[ev-seq-1 ev-seq-2]" {
			t.Fatalf("inverted created_at vs seq: want [ev-seq-1 ev-seq-2], got n=%d ids=%v err=%v", n, got, err)
		}
	})

	t.Run("W3A_D9bis_LockedExpiredRow_BlocksLaterSameAggregateEvent", func(t *testing.T) {
		cleanOutbox(t)
		st := NewPGStore(pool)
		t0 := time.Now().UTC().Add(-2 * time.Minute)
		rowE1 := insertOutbox(t, "ev-lock-1", "ord-lock-agg", t0)
		insertOutbox(t, "ev-lock-2", "ord-lock-agg", t0.Add(time.Second))
		insertOutbox(t, "ev-lock-free", "ord-lock-free", t0.Add(2*time.Second))

		if _, err := pool.Exec(ctx, `UPDATE outbox_events SET claimed_until = NOW() - INTERVAL '1 second', claim_token = 'expired-tok' WHERE id = $1`, rowE1); err != nil {
			t.Fatalf("expire e1: %v", err)
		}
		lockTx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin lockTx: %v", err)
		}
		defer func() { _ = lockTx.Rollback(ctx) }()
		var lid string
		if err := lockTx.QueryRow(ctx, `SELECT id FROM outbox_events WHERE id = $1 FOR UPDATE`, rowE1).Scan(&lid); err != nil {
			t.Fatalf("lock e1 FOR UPDATE: %v", err)
		}

		var w2 []string
		n, err := st.ProcessPendingOutbox(ctx, 10, func(_ context.Context, ev OutboxEvent) error {
			w2 = append(w2, ev.EventID)
			return nil
		})
		if err != nil || n != 1 || fmt.Sprint(w2) != "[ev-lock-free]" {
			t.Fatalf("worker 2 while E1 row-locked: want [ev-lock-free], got n=%d ids=%v err=%v", n, w2, err)
		}
		if err := lockTx.Rollback(ctx); err != nil {
			t.Fatalf("rollback lockTx: %v", err)
		}
		var after []string
		nAfter, err := st.ProcessPendingOutbox(ctx, 10, func(_ context.Context, ev OutboxEvent) error {
			after = append(after, ev.EventID)
			return nil
		})
		if err != nil || nAfter != 2 || fmt.Sprint(after) != "[ev-lock-1 ev-lock-2]" {
			t.Fatalf("after unlocking E1: want [ev-lock-1 ev-lock-2], got n=%d ids=%v err=%v", nAfter, after, err)
		}
	})

	t.Run("W3A_D9bis_PoisonBackoff_DoesNotStarveHealthyAggregate", func(t *testing.T) {
		st := NewPGStore(pool)
		t0 := time.Now().UTC().Add(-2 * time.Minute)

		// d1: 1 poison + 49 events same aggregate + 1 healthy aggregate.
		cleanOutbox(t)
		var p0RowID string
		for i := 0; i < 50; i++ {
			id := insertOutbox(t, fmt.Sprintf("ev-poison-same-%02d", i), "ord-poison-same", t0.Add(time.Duration(i)*time.Millisecond))
			if i == 0 {
				p0RowID = id
			}
		}
		insertOutbox(t, "ev-healthy-1", "ord-healthy-1", t0.Add(time.Second))

		n1, err := st.ProcessPendingOutbox(ctx, 50, func(_ context.Context, ev OutboxEvent) error {
			if ev.EventID == "ev-poison-same-00" {
				return errors.New("poison non-infra error")
			}
			return nil
		})
		if err != nil || n1 != 0 {
			t.Fatalf("d1 pass 1: want (0, nil), got (%d, %v)", n1, err)
		}
		var p0Until *time.Time
		var p0Tok string
		if err := pool.QueryRow(ctx, `SELECT claimed_until, claim_token FROM outbox_events WHERE id = $1`, p0RowID).Scan(&p0Until, &p0Tok); err != nil {
			t.Fatalf("query p0 backoff: %v", err)
		}
		if p0Until == nil || !p0Until.After(time.Now().UTC()) || p0Tok != "" {
			t.Fatalf("d1 p0 must be in future backoff with empty token, got until=%v tok=%q", p0Until, p0Tok)
		}

		var d1Pass2 []string
		n2, err := st.ProcessPendingOutbox(ctx, 50, func(_ context.Context, ev OutboxEvent) error {
			d1Pass2 = append(d1Pass2, ev.EventID)
			return nil
		})
		if err != nil || n2 != 1 || fmt.Sprint(d1Pass2) != "[ev-healthy-1]" {
			t.Fatalf("d1 pass 2 starved healthy aggregate! want [ev-healthy-1], got n=%d ids=%v err=%v", n2, d1Pass2, err)
		}

		// d2: 50 poisons on 50 distinct aggregates + 1 healthy aggregate.
		cleanOutbox(t)
		for i := 0; i < 50; i++ {
			insertOutbox(t, fmt.Sprintf("ev-poison-dist-%02d", i), fmt.Sprintf("ord-poison-dist-%02d", i), t0.Add(time.Duration(i)*time.Millisecond))
		}
		insertOutbox(t, "ev-healthy-2", "ord-healthy-2", t0.Add(time.Second))

		nx1, err := st.ProcessPendingOutbox(ctx, 50, func(_ context.Context, ev OutboxEvent) error {
			if strings.HasPrefix(ev.EventID, "ev-poison-dist-") {
				return errors.New("poison distinct agg")
			}
			return nil
		})
		if err != nil || nx1 != 0 {
			t.Fatalf("d2 pass 1: want (0, nil), got (%d, %v)", nx1, err)
		}

		var d2Pass2 []string
		nx2, err := st.ProcessPendingOutbox(ctx, 50, func(_ context.Context, ev OutboxEvent) error {
			d2Pass2 = append(d2Pass2, ev.EventID)
			return nil
		})
		if err != nil || nx2 != 1 || fmt.Sprint(d2Pass2) != "[ev-healthy-2]" {
			t.Fatalf("d2 pass 2 starved healthy aggregate! want [ev-healthy-2], got n=%d ids=%v err=%v", nx2, d2Pass2, err)
		}
	})

	t.Run("W3A_D9bis_NonInfraError_ErrorLogAtAttempts10WithoutPayload", func(t *testing.T) {
		cleanOutbox(t)
		logStore := NewPGStore(pool)
		var logBuf bytes.Buffer
		logStore.SetLogger(slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo})))

		const secretSentinel = "SHIPPING-SECRET-PAYLOAD-NEVER-LOG-10"
		rowID := uuid.NewString()
		evID := "ev-ship-poison-10"
		raw, _ := json.Marshal(map[string]string{
			"order_id":    "ord-poison-10",
			"shipment_id": "sh-poison-10",
			"secret_note": secretSentinel,
		})
		if _, err := pool.Exec(ctx, `
			INSERT INTO outbox_events (id, event_id, subject, aggregate_id, payload, attempts, created_at)
			VALUES ($1, $2, 'shipment.updated', 'ord-poison-10', $3::jsonb, 8, NOW())
		`, rowID, evID, string(raw)); err != nil {
			t.Fatalf("insert poison10: %v", err)
		}

		// Attempt 9 -> must NOT log ERROR yet.
		_, err := logStore.ProcessPendingOutbox(ctx, 10, func(_ context.Context, _ OutboxEvent) error {
			return errors.New("non-infra envelope rejection")
		})
		if err != nil {
			t.Fatalf("attempt 9 err: %v", err)
		}
		if strings.Contains(logBuf.String(), `"level":"ERROR"`) {
			t.Fatalf("attempt 9 unexpectedly logged ERROR: %s", logBuf.String())
		}

		// Expire backoff so attempt 10 executes -> must log ERROR with event_id & subject, NO payload!
		if _, err := pool.Exec(ctx, `UPDATE outbox_events SET claimed_until = NOW() - INTERVAL '1 second' WHERE id = $1`, rowID); err != nil {
			t.Fatalf("expire attempt 9 backoff: %v", err)
		}
		logBuf.Reset()
		_, err = logStore.ProcessPendingOutbox(ctx, 10, func(_ context.Context, _ OutboxEvent) error {
			return errors.New("non-infra envelope rejection")
		})
		if err != nil {
			t.Fatalf("attempt 10 err: %v", err)
		}
		logged := logBuf.String()
		if !strings.Contains(logged, `"level":"ERROR"`) || !strings.Contains(logged, evID) || !strings.Contains(logged, "shipment.updated") {
			t.Fatalf("attempt 10 missing expected ERROR log fields: %s", logged)
		}
		if strings.Contains(logged, secretSentinel) {
			t.Fatalf("attempt 10 ERROR log leaked payload secret: %s", logged)
		}
	})

	t.Run("W3A_D9bisR2_P1_1_ShipmentAggregateID_SameShipmentSerializes_FwdRetIndependent", func(t *testing.T) {
		cleanOutbox(t)
		st := NewPGStore(pool)
		suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
		ordID := "ord-r2-p11-" + suffix
		fwdID := uuid.NewString()
		retID := uuid.NewString()
		fwdTrk := "TRK-FWD-" + suffix
		retTrk := "TRK-RET-" + suffix
		now := time.Now().UTC().Truncate(time.Second)

		if _, _, err := st.CreateShipment(ctx, &Shipment{
			ID: fwdID, OrderID: ordID, ShopID: "shop-r2", BuyerID: "buyer-r2",
			Direction: DirectionForward, CarrierCode: "ghn", ServiceCode: "STANDARD",
			TrackingCode: fwdTrk, Status: "created", FeeVND: 22000, WeightGrams: 500,
			CreatedAt: now, UpdatedAt: now,
		}, nil); err != nil {
			t.Fatalf("create FORWARD shipment: %v", err)
		}
		if _, _, err := st.CreateShipment(ctx, &Shipment{
			ID: retID, OrderID: ordID, ShopID: "shop-r2", BuyerID: "buyer-r2",
			Direction: DirectionReturn, ReturnRequestID: "rr-" + suffix, CarrierCode: "ghn", ServiceCode: "STANDARD",
			TrackingCode: retTrk, Status: "created", FeeVND: 22000, WeightGrams: 500,
			CreatedAt: now, UpdatedAt: now,
		}, nil); err != nil {
			t.Fatalf("create RETURN shipment: %v", err)
		}

		// 1. Interleaved webhooks on the SAME FORWARD shipment:
		//    tx1 holds FOR UPDATE on fwdID, writes ev1 (picking) to outbox, and pauses before COMMIT;
		//    concurrent ApplyWebhookEvent (ev2: delivering) MUST wait for the FOR UPDATE lock on fwdID
		//    until tx1 commits, so ev1 always gets smaller seq and publishes BEFORE ev2.
		tx1, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin tx1: %v", err)
		}
		defer func() { _ = tx1.Rollback(ctx) }()
		var lockedFwd string
		if err := tx1.QueryRow(ctx, `SELECT id FROM shipments WHERE id = $1 FOR UPDATE`, fwdID).Scan(&lockedFwd); err != nil {
			t.Fatalf("tx1 lock fwdID: %v", err)
		}
		evFwd1 := "wh-fwd-1-" + suffix
		p1, _ := json.Marshal(map[string]string{"shipment_id": fwdID, "order_id": ordID, "status": "picking", "direction": DirectionForward})
		if _, err := tx1.Exec(ctx, `UPDATE shipments SET status = 'picking', last_event_at = $2, updated_at = $2 WHERE id = $1`, fwdID, now.Add(time.Second)); err != nil {
			t.Fatalf("tx1 update shipment: %v", err)
		}
		if _, err := tx1.Exec(ctx, `
			INSERT INTO outbox_events (id, event_id, subject, aggregate_id, payload)
			VALUES ($1, $2, 'shipment.updated', $3, $4::jsonb)
		`, uuid.NewString(), evFwd1, fwdID, string(p1)); err != nil {
			t.Fatalf("tx1 insert outbox: %v", err)
		}

		evFwd2 := "wh-fwd-2-" + suffix
		tx2Done := make(chan error, 1)
		go func() {
			_, outcome, wErr := st.ApplyWebhookEvent(ctx, WebhookUpdateParams{
				EventID: "wh-e2-" + suffix, CarrierCode: "ghn", TrackingCode: fwdTrk, OrderID: ordID,
				InternalStatus: "delivering", RawStatus: "delivering", Description: "Đang giao", OccurredAt: now.Add(2 * time.Second),
				OutboxEventID: evFwd2, OutboxSubject: "shipment.updated",
				BuildOutboxPayload: func(sh *Shipment) ([]byte, error) {
					return json.Marshal(map[string]string{"shipment_id": sh.ID, "order_id": sh.OrderID, "status": sh.Status, "direction": sh.Direction})
				},
			})
			if wErr != nil {
				tx2Done <- wErr
				return
			}
			if outcome != WebhookOutcomeApplied {
				tx2Done <- fmt.Errorf("tx2 outcome=%s, want applied", outcome)
				return
			}
			tx2Done <- nil
		}()

		// Confirm tx2 is blocked waiting on shipments(id) FOR UPDATE while tx1 is uncommitted.
		select {
		case wErr := <-tx2Done:
			t.Fatalf("tx2 ApplyWebhookEvent completed before tx1 released FOR UPDATE lock! err=%v", wErr)
		case <-time.After(150 * time.Millisecond):
		}

		if err := tx1.Commit(ctx); err != nil {
			t.Fatalf("commit tx1: %v", err)
		}
		select {
		case wErr := <-tx2Done:
			if wErr != nil {
				t.Fatalf("tx2 ApplyWebhookEvent failed after tx1 commit: %v", wErr)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("tx2 ApplyWebhookEvent did not finish after tx1 commit")
		}

		// Verify ApplyWebhookEvent populated aggregate_id = shipment_id (fwdID), NOT order_id.
		var fwd2Agg string
		if err := pool.QueryRow(ctx, `SELECT aggregate_id FROM outbox_events WHERE event_id = $1`, evFwd2).Scan(&fwd2Agg); err != nil {
			t.Fatalf("query evFwd2 aggregate_id: %v", err)
		}
		if fwd2Agg != fwdID {
			t.Fatalf("ApplyWebhookEvent aggregate_id = %q, want shipment_id %q (not order_id %q)", fwd2Agg, fwdID, ordID)
		}

		// 2. FORWARD and RETURN for the SAME order_id are independent aggregates:
		//    Add a RETURN webhook event, lease evFwd1 on FORWARD -> RETURN MUST still claim & publish immediately!
		evRet1 := "wh-ret-1-" + suffix
		_, retOutcome, err := st.ApplyWebhookEvent(ctx, WebhookUpdateParams{
			EventID: "wh-ret-e1-" + suffix, CarrierCode: "ghn", TrackingCode: retTrk, OrderID: ordID,
			InternalStatus: "delivering", RawStatus: "delivering", Description: "Đang hoàn", OccurredAt: now.Add(3 * time.Second),
			OutboxEventID: evRet1, OutboxSubject: "shipment.updated",
			BuildOutboxPayload: func(sh *Shipment) ([]byte, error) {
				return json.Marshal(map[string]string{"shipment_id": sh.ID, "order_id": sh.OrderID, "status": sh.Status, "direction": sh.Direction})
			},
		})
		if err != nil || retOutcome != WebhookOutcomeApplied {
			t.Fatalf("RETURN ApplyWebhookEvent = (%s, %v), want applied", retOutcome, err)
		}
		var retAgg string
		if err := pool.QueryRow(ctx, `SELECT aggregate_id FROM outbox_events WHERE event_id = $1`, evRet1).Scan(&retAgg); err != nil {
			t.Fatalf("query evRet1 aggregate_id: %v", err)
		}
		if retAgg != retID {
			t.Fatalf("RETURN aggregate_id = %q, want shipment_id %q", retAgg, retID)
		}

		// Worker 1 claims ONLY evFwd1 (limit=1) -> holds active lease on fwdID.
		claimed1, tok1, err := st.ClaimPendingOutbox(ctx, 1)
		if err != nil || len(claimed1) != 1 || claimed1[0].EventID != evFwd1 {
			t.Fatalf("worker 1 claim: got=%+v err=%v", claimed1, err)
		}

		// Worker 2 runs while evFwd1 is leased: MUST skip evFwd2 (same fwdID) AND publish evRet1 (independent retID)!
		var w2IDs []string
		n2, err := st.ProcessPendingOutbox(ctx, 10, func(_ context.Context, ev OutboxEvent) error {
			w2IDs = append(w2IDs, ev.EventID)
			return nil
		})
		if err != nil || n2 != 1 || fmt.Sprint(w2IDs) != "["+evRet1+"]" {
			t.Fatalf("worker 2 while FORWARD leased: want [%s] (independent RETURN aggregate), got n=%d ids=%v err=%v", evRet1, n2, w2IDs, err)
		}

		// Complete evFwd1 then drain evFwd2 -> strict order [evFwd1, evFwd2] for FORWARD.
		if ok, err := st.MarkOutboxPublished(ctx, claimed1[0].ID, tok1); err != nil || !ok {
			t.Fatalf("MarkOutboxPublished evFwd1 = (%v, %v)", ok, err)
		}
		var fwdRem []string
		n3, err := st.ProcessPendingOutbox(ctx, 10, func(_ context.Context, ev OutboxEvent) error {
			fwdRem = append(fwdRem, ev.EventID)
			return nil
		})
		if err != nil || n3 != 1 || fmt.Sprint(fwdRem) != "["+evFwd2+"]" {
			t.Fatalf("drain evFwd2: want [%s], got n=%d ids=%v err=%v", evFwd2, n3, fwdRem, err)
		}
	})

	t.Run("W3A_D9bisR2_P1_3_RepeatMigrate_NoAccessExclusive_NoCtidRewrite", func(t *testing.T) {
		cleanOutbox(t)
		// Insert an unpublished row with blank/whitespace shipment_id & order_id -> backfill must not touch it on repeat MigrateAndSeed.
		blankID := uuid.NewString()
		if _, err := pool.Exec(ctx, `
			INSERT INTO outbox_events (id, event_id, subject, aggregate_id, payload)
			VALUES ($1, 'ev-blank-ctid', 'shipment.updated', '', '{"order_id":"   ","shipment_id":"  "}'::jsonb)
		`, blankID); err != nil {
			t.Fatalf("insert blank outbox row: %v", err)
		}
		var ctidBefore string
		if err := pool.QueryRow(ctx, `SELECT ctid::text FROM outbox_events WHERE id = $1`, blankID).Scan(&ctidBefore); err != nil {
			t.Fatalf("read ctid before: %v", err)
		}

		// Hold ACCESS SHARE lock on outbox_events in an open transaction.
		readerTx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin readerTx: %v", err)
		}
		defer func() { _ = readerTx.Rollback(ctx) }()
		var dummy string
		if err := readerTx.QueryRow(ctx, `SELECT id FROM outbox_events WHERE id = $1`, blankID).Scan(&dummy); err != nil {
			t.Fatalf("readerTx select outbox_events: %v", err)
		}

		// Run repeat MigrateAndSeed using a pool configured with short lock_timeout (300ms):
		// if 000005 tries ALTER TABLE (ACCESS EXCLUSIVE) on outbox_events, Postgres will fail with SQLSTATE 55P03!
		cfg := pool.Config().Copy()
		if cfg.ConnConfig.RuntimeParams == nil {
			cfg.ConnConfig.RuntimeParams = map[string]string{}
		}
		cfg.ConnConfig.RuntimeParams["lock_timeout"] = "300ms"
		migPool, err := pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			t.Fatalf("pgxpool.NewWithConfig(lock_timeout): %v", err)
		}
		defer migPool.Close()

		if err := NewPGStore(migPool).MigrateAndSeed(ctx); err != nil {
			t.Fatalf("repeat MigrateAndSeed under ACCESS SHARE + lock_timeout=300ms failed (acquired ACCESS EXCLUSIVE!): %v", err)
		}

		var ctidAfter string
		if err := pool.QueryRow(ctx, `SELECT ctid::text FROM outbox_events WHERE id = $1`, blankID).Scan(&ctidAfter); err != nil {
			t.Fatalf("read ctid after: %v", err)
		}
		if ctidAfter != ctidBefore {
			t.Fatalf("repeat MigrateAndSeed rewrote blank unpublished row! ctid %s -> %s", ctidBefore, ctidAfter)
		}
	})

	t.Run("W3A_D9bisR2_P1_4_MigrationReassignsSeqByCreatedAt_AfterMVCCUpdate", func(t *testing.T) {
		// Create a fresh isolated DB and apply ONLY 000001..000004 (pre-000005 schema without seq).
		migPool := openIsolatedShippingTestPool(t)
		for _, fname := range []string{
			"000001_init.sql",
			"000002_shipping_ops.sql",
			"000003_return_recreate_index.sql",
			"000004_shop_freeship_settings.sql",
		} {
			raw, rErr := fs.ReadFile(migrations.FS, fname)
			if rErr != nil {
				t.Fatalf("read %s: %v", fname, rErr)
			}
			if _, eErr := migPool.Exec(ctx, string(raw)); eErr != nil {
				t.Fatalf("exec %s: %v", fname, eErr)
			}
		}

		t0 := time.Date(2026, 11, 24, 17, 0, 0, 0, time.UTC)
		shID := uuid.NewString()
		e1ID := uuid.NewString()
		e2ID := uuid.NewString()
		wsID := uuid.NewString()
		p1, _ := json.Marshal(map[string]string{"shipment_id": shID, "order_id": "ord-mig", "status": "picking"})
		p2, _ := json.Marshal(map[string]string{"shipment_id": shID, "order_id": "ord-mig", "status": "delivered"})
		pWS, _ := json.Marshal(map[string]string{"shipment_id": "   ", "order_id": "   "})

		if _, err := migPool.Exec(ctx, `
			INSERT INTO outbox_events (id, event_id, subject, payload, created_at)
			VALUES ($1, 'ev-mig-1', 'shipment.updated', $2::jsonb, $3),
			       ($4, 'ev-mig-2', 'shipment.updated', $5::jsonb, $6),
			       ($7, 'ev-mig-ws', 'shipment.updated', $8::jsonb, $9)
		`, e1ID, string(p1), t0, e2ID, string(p2), t0.Add(5*time.Second), wsID, string(pWS), t0.Add(10*time.Second)); err != nil {
			t.Fatalf("insert pre-000005 rows: %v", err)
		}

		// Force MVCC update on older E1 so its physical ctid moves AFTER E2!
		if _, err := migPool.Exec(ctx, `UPDATE outbox_events SET trace_id = 'mvcc-moved' WHERE id = $1`, e1ID); err != nil {
			t.Fatalf("update E1 to move ctid: %v", err)
		}
		var ctid1, ctid2 string
		_ = migPool.QueryRow(ctx, `SELECT ctid::text FROM outbox_events WHERE id = $1`, e1ID).Scan(&ctid1)
		_ = migPool.QueryRow(ctx, `SELECT ctid::text FROM outbox_events WHERE id = $1`, e2ID).Scan(&ctid2)
		if ctid1 <= ctid2 {
			t.Fatalf("expected MVCC update to place E1 ctid (%s) after E2 ctid (%s)", ctid1, ctid2)
		}

		// Now apply 000005_outbox_lease.sql via MigrateAndSeed!
		migStore := NewPGStore(migPool)
		if err := migStore.MigrateAndSeed(ctx); err != nil {
			t.Fatalf("apply 000005 via MigrateAndSeed: %v", err)
		}

		// Verify E1 publishes BEFORE E2 despite ctid(E1) > ctid(E2)!
		var got []string
		n, err := migStore.ProcessPendingOutbox(ctx, 10, func(_ context.Context, ev OutboxEvent) error {
			got = append(got, ev.EventID)
			return nil
		})
		if err != nil || n != 3 {
			t.Fatalf("ProcessPendingOutbox post-000005 = (%d, %v), got=%v", n, err, got)
		}
		if fmt.Sprint(got) != "[ev-mig-1 ev-mig-2 ev-mig-ws]" {
			t.Fatalf("post-000005 publish order = %v, want [ev-mig-1 ev-mig-2 ev-mig-ws] (MVCC ctid inverted seq!)", got)
		}

		// Whitespace payload must backfill to empty string '' (P3-11).
		var wsAgg string
		if err := migPool.QueryRow(ctx, `SELECT aggregate_id FROM outbox_events WHERE id = $1`, wsID).Scan(&wsAgg); err != nil {
			t.Fatalf("query ws aggregate_id: %v", err)
		}
		if wsAgg != "" {
			t.Fatalf("whitespace payload backfilled to %q, want empty string", wsAgg)
		}
	})
}
