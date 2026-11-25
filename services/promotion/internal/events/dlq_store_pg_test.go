package events_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"

	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/promotion/internal/events"
	"github.com/nexusmart/core/services/promotion/internal/store"
)

func openTestEventsPG(t *testing.T) (*store.PostgresStore, *pgxpool.Pool) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_PROMOTION_DB_URL"))
	if dsn == "" {
		t.Skip("set TEST_PROMOTION_DB_URL to run real Postgres tests")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)

	st := store.NewPostgresStore(pool)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate (1st): %v", err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate (2nd idempotency): %v", err)
	}
	return st, pool
}

func TestPostgresDeadLetterStore_E2_MigrationSchemaAndConsumerPersistence(t *testing.T) {
	pgStore, pool := openTestEventsPG(t)
	ctx := context.Background()

	// 1. Verify 006_dead_letter_events.sql schema matches order_db.dead_letter_events columns exactly.
	rows, err := pool.Query(ctx, `
		SELECT column_name, data_type, is_nullable
		FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'dead_letter_events'
		ORDER BY ordinal_position
	`)
	if err != nil {
		t.Fatalf("query dead_letter_events columns: %v", err)
	}
	defer rows.Close()

	type colSpec struct {
		dataType   string
		isNullable string
	}
	gotCols := make(map[string]colSpec)
	for rows.Next() {
		var name, dtype, nullable string
		if err := rows.Scan(&name, &dtype, &nullable); err != nil {
			t.Fatalf("scan column: %v", err)
		}
		gotCols[name] = colSpec{dataType: dtype, isNullable: nullable}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows err: %v", err)
	}

	wantCols := map[string]colSpec{
		"id":            {dataType: "text", isNullable: "NO"},
		"subject":       {dataType: "text", isNullable: "NO"},
		"event_id":      {dataType: "text", isNullable: "NO"},
		"payload":       {dataType: "bytea", isNullable: "NO"},
		"error_message": {dataType: "text", isNullable: "NO"},
		"attempts":      {dataType: "integer", isNullable: "NO"},
		"created_at":    {dataType: "timestamp with time zone", isNullable: "NO"},
	}
	if len(gotCols) != len(wantCols) {
		t.Fatalf("E2: dead_letter_events column count mismatch: got=%v want=%v", gotCols, wantCols)
	}
	for col, want := range wantCols {
		got, ok := gotCols[col]
		if !ok || got != want {
			t.Fatalf("E2: column %q mismatch: got=%+v want=%+v", col, got, want)
		}
	}

	// 2. Wire consumer with real PostgresDeadLetterStore + failing NATS DLQ.
	dlqStore := events.NewPostgresDeadLetterStore(pool)
	failingNatsDLQ := &spyDLQPublisher{err: nats.ErrDisconnected}
	log := logger.New("promo-pg-dlq-test", logger.WithWriter(io.Discard))

	// Case A: Malformed non-JSON raw bytes (including invalid UTF-8) must persist as BYTEA and Ack.
	c := events.NewOrderCompletedConsumer(pgStore, nil, "NEXUSMART", log)
	c.SetDeadLetterStore(dlqStore)
	c.SetDLQPublisher(failingNatsDLQ)

	malformedBytes := []byte("{malformed-\xff-payload-" + uuid.NewString()[:8])
	ack, err := c.ProcessRawMessage(ctx, malformedBytes, nil, 1)
	if !ack || err != nil {
		t.Fatalf("E1/E2: expected Ack when Postgres dead_letter_events succeeded despite NATS DLQ outage, got ack=%v err=%v", ack, err)
	}

	var gotSubject, gotEventID, gotErrMsg string
	var gotPayload []byte
	var gotAttempts int
	var gotCreatedAt time.Time
	if err := pool.QueryRow(ctx, `
		SELECT subject, event_id, payload, error_message, attempts, created_at
		FROM dead_letter_events
		WHERE payload = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, malformedBytes).Scan(&gotSubject, &gotEventID, &gotPayload, &gotErrMsg, &gotAttempts, &gotCreatedAt); err != nil {
		t.Fatalf("E2: query inserted malformed dead_letter_events row: %v", err)
	}
	if gotSubject != events.SubjectOrderCompleted || gotEventID != "" || !bytes.Equal(gotPayload, malformedBytes) || gotAttempts != 1 || gotCreatedAt.IsZero() {
		t.Fatalf("E2: unexpected malformed DLQ row: subject=%q event_id=%q attempts=%d err=%q", gotSubject, gotEventID, gotAttempts, gotErrMsg)
	}

	// Case B: Handler failing at MaxDeliveryAttempts with expired handler deadline still persists via WithoutCancel.
	evtID := "pg-dlq-evt-" + uuid.NewString()[:8]
	failingStore := &hookOrderStore{
		Store: pgStore,
		hook: func(hCtx context.Context, _, _, _, _ string, _ int64) (bool, error) {
			return false, hCtx.Err()
		},
	}
	cFail := events.NewOrderCompletedConsumer(failingStore, nil, "NEXUSMART", log)
	cFail.SetDeadLetterStore(dlqStore)
	cFail.SetDLQPublisher(failingNatsDLQ)

	expiredCtx, cancelExpired := context.WithDeadline(ctx, time.Now().Add(-time.Second))
	defer cancelExpired()
	raw := makeOrderCompletedRaw(t, evtID, "ord-"+evtID, "buyer-"+evtID, 250)
	ack, err = cFail.ProcessRawMessage(expiredCtx, raw, nil, events.MaxDeliveryAttempts)
	if !ack || err != nil {
		t.Fatalf("E1/E2: expected Ack after DB DLQ persisted via detached ctx, got ack=%v err=%v", ack, err)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM dead_letter_events WHERE event_id = $1 AND attempts = $2`, evtID, events.MaxDeliveryAttempts).Scan(&count); err != nil {
		t.Fatalf("query event_id count: %v", err)
	}
	if count != 1 {
		t.Fatalf("E2: expected 1 dead_letter_events row for %s, got %d", evtID, count)
	}

	// Case C: Shutdown context.Canceled at MaxDeliveryAttempts writes 0 rows to dead_letter_events.
	shutdownEvtID := "pg-shutdown-evt-" + uuid.NewString()[:8]
	cancelCtx, cancel := context.WithCancel(ctx)
	cCancel := events.NewOrderCompletedConsumer(&hookOrderStore{
		Store: pgStore,
		hook: func(hCtx context.Context, _, _, _, _ string, _ int64) (bool, error) {
			cancel()
			return false, hCtx.Err()
		},
	}, nil, "NEXUSMART", log)
	cCancel.SetDeadLetterStore(dlqStore)
	cCancel.SetDLQPublisher(failingNatsDLQ)

	shutdownRaw := makeOrderCompletedRaw(t, shutdownEvtID, "ord-"+shutdownEvtID, "buyer-"+shutdownEvtID, 100)
	ack, err = cCancel.ProcessRawMessage(cancelCtx, shutdownRaw, nil, events.MaxDeliveryAttempts)
	if ack || err == nil {
		t.Fatalf("E1: shutdown cancel must NOT Ack, got ack=%v err=%v", ack, err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM dead_letter_events WHERE event_id = $1`, shutdownEvtID).Scan(&count); err != nil {
		t.Fatalf("query shutdown event_id count: %v", err)
	}
	if count != 0 {
		t.Fatalf("E1: shutdown cancel must write 0 rows to dead_letter_events, got %d", count)
	}
}

func TestOrderCompletedConsumer_E1_E2_RealPGAndNATS_ConcurrentLegacyUpgrade(t *testing.T) {
	pgStore, pool := openTestEventsPG(t)
	env := setupTestNATS(t, true)
	natsURL := env.url
	streamName := env.streamName
	jsAdmin := env.adminJS
	log := logger.New("promo-pg-nats-upgrade", logger.WithWriter(io.Discard))
	suffix := uuid.NewString()[:8]
	buyerID := "buyer-pg-nats-" + suffix
	legacyID1 := "legacy-" + suffix + "-1"
	legacyID2 := "legacy-" + suffix + "-2"
	pendingID := "pending-" + suffix
	livePrefix := "live-" + suffix + "-"

	// 1. Seed legacy non-queue durable consumer (DeliverGroup="", MaxDeliver=5) that already acked 2 events.
	ncLegacy, err := nats.Connect(natsURL)
	if err != nil {
		t.Fatalf("connect legacy nats: %v", err)
	}
	jsLegacy, err := ncLegacy.JetStream()
	if err != nil {
		ncLegacy.Close()
		t.Fatalf("jetstream legacy: %v", err)
	}

	legacyCh := make(chan *nats.Msg, 32)
	if _, err := jsLegacy.Subscribe(
		events.SubjectOrderCompleted,
		func(m *nats.Msg) { legacyCh <- m },
		nats.BindStream(streamName),
		nats.Durable(events.DurableConsumerName),
		nats.ManualAck(),
		nats.AckExplicit(),
		nats.MaxDeliver(events.MaxDeliveryAttempts),
		nats.AckWait(30*time.Second),
	); err != nil {
		ncLegacy.Close()
		t.Fatalf("legacy subscribe: %v", err)
	}
	for _, id := range []string{legacyID1, legacyID2} {
		if _, err := jsLegacy.Publish(events.SubjectOrderCompleted, makeOrderCompletedRaw(t, id, "ord-"+id, buyerID, 100)); err != nil {
			ncLegacy.Close()
			t.Fatal(err)
		}
	}
	acked := map[string]bool{}
	deadlineLegacy := time.After(4 * time.Second)
	for !(acked[legacyID1] && acked[legacyID2]) {
		select {
		case m := <-legacyCh:
			if err := m.AckSync(); err != nil {
				ncLegacy.Close()
				t.Fatalf("ack legacy: %v", err)
			}
			acked[extractEnvelopeEventID(m.Data)] = true
		case <-deadlineLegacy:
			ncLegacy.Close()
			t.Fatalf("timeout waiting for legacy acks (got=%v)", acked)
		}
	}
	ncLegacy.Close()

	// Publish 1 pending event while down.
	if _, err := jsAdmin.Publish(events.SubjectOrderCompleted, makeOrderCompletedRaw(t, pendingID, "ord-"+pendingID, buyerID, 100)); err != nil {
		t.Fatal(err)
	}

	// 2. Launch 2 replicas CONCURRENTLY against the legacy consumer using real PostgresDeadLetterStore (advisory lock).
	var handledMu sync.Mutex
	handledIDs := make(map[string]int)
	var totalHandled atomic.Int32
	countingStore := &hookOrderStore{
		Store: pgStore,
		hook: func(ctx context.Context, eventID, subject, orderID, buyerID string, coinsReward int64) (bool, error) {
			if eventID == legacyID1 || eventID == legacyID2 || eventID == pendingID || strings.HasPrefix(eventID, livePrefix) {
				handledMu.Lock()
				handledIDs[eventID]++
				handledMu.Unlock()
				totalHandled.Add(1)
			}
			return pgStore.ProcessOrderCompletedEvent(ctx, eventID, subject, orderID, buyerID, coinsReward)
		},
	}

	dlqStore := events.NewPostgresDeadLetterStore(pool)
	startBarrier := make(chan struct{})
	var startWG sync.WaitGroup
	startErrs := make([]error, 2)
	conns := make([]*nats.Conn, 2)
	consumers := make([]*events.OrderCompletedConsumer, 2)
	cancels := make([]context.CancelFunc, 2)

	for i := 0; i < 2; i++ {
		nc, err := nats.Connect(natsURL)
		if err != nil {
			t.Fatalf("replica %d connect: %v", i, err)
		}
		js, err := nc.JetStream()
		if err != nil {
			nc.Close()
			t.Fatalf("replica %d jetstream: %v", i, err)
		}
		c := events.NewOrderCompletedConsumer(countingStore, js, streamName, log)
		c.SetDeadLetterStore(dlqStore)
		rCtx, rCancel := context.WithCancel(context.Background())
		conns[i] = nc
		consumers[i] = c
		cancels[i] = rCancel

		idx := i
		startWG.Add(1)
		go func() {
			defer startWG.Done()
			<-startBarrier
			startErrs[idx] = consumers[idx].Start(rCtx)
		}()
	}
	t.Cleanup(func() {
		for i := 0; i < 2; i++ {
			if cancels[i] != nil {
				cancels[i]()
			}
			if consumers[i] != nil {
				_ = consumers[i].Stop()
			}
			if conns[i] != nil {
				conns[i].Close()
			}
		}
	})

	close(startBarrier)
	startWG.Wait()
	for i, sErr := range startErrs {
		if sErr != nil {
			t.Fatalf("E1: concurrent legacy upgrade replica %d failed: %v", i, sErr)
		}
	}

	// Publish 10 live valid events + 1 malformed poison event.
	for i := 0; i < 10; i++ {
		id := livePrefix + uuid.NewString()[:6]
		if _, err := jsAdmin.Publish(events.SubjectOrderCompleted, makeOrderCompletedRaw(t, id, "ord-"+id, buyerID, 100)); err != nil {
			t.Fatal(err)
		}
	}
	poisonPayload := []byte("{poison-" + suffix)
	if _, err := jsAdmin.Publish(events.SubjectOrderCompleted, poisonPayload); err != nil {
		t.Fatal(err)
	}

	// Wait for 11 valid events (1 pending + 10 live) + 1 poison DLQ row in real Postgres.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var dlqCount int
		_ = pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM dead_letter_events WHERE payload = $1`, poisonPayload).Scan(&dlqCount)
		if totalHandled.Load() >= 11 && dlqCount >= 1 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	time.Sleep(150 * time.Millisecond)

	handledMu.Lock()
	for id, cnt := range handledIDs {
		if strings.HasPrefix(id, "legacy-"+suffix) {
			handledMu.Unlock()
			t.Fatalf("E1: concurrent legacy upgrade MUST NOT replay already-acked event %s (counts=%v)", id, handledIDs)
		}
		if cnt != 1 {
			handledMu.Unlock()
			t.Fatalf("E1: event %s processed %d times across 2 queue replicas, expected 1", id, cnt)
		}
	}
	handledMu.Unlock()

	if totalHandled.Load() != 11 {
		t.Fatalf("E1: expected exactly 11 valid events processed (1 pending + 10 live), got %d", totalHandled.Load())
	}

	bal, _, err := pgStore.GetCoinBalance(context.Background(), buyerID, 20)
	if err != nil {
		t.Fatalf("GetCoinBalance: %v", err)
	}
	if bal != 1100 {
		t.Fatalf("expected buyer coin balance 1100 (11 * 100), got %d", bal)
	}

	dlqStreamInfo, err := jsAdmin.StreamInfo(events.DefaultDLQStream)
	if err != nil {
		t.Fatalf("StreamInfo(%s): %v", events.DefaultDLQStream, err)
	}
	if delta := dlqStreamInfo.State.Msgs - env.dlqMsgsBefore; delta != 1 {
		t.Fatalf("E1/E2: expected 1 new poison message in %s, got delta=%d (total=%d before=%d)",
			events.DefaultDLQStream, delta, dlqStreamInfo.State.Msgs, env.dlqMsgsBefore)
	}
	foundPoison := false
	for seq := env.dlqSeqBefore + 1; seq <= dlqStreamInfo.State.LastSeq; seq++ {
		if m, gErr := jsAdmin.GetMsg(events.DefaultDLQStream, seq); gErr == nil && m != nil && bytes.Equal(m.Data, poisonPayload) {
			foundPoison = true
			break
		}
	}
	if !foundPoison {
		t.Fatalf("E1/E2: expected poison payload in %s (seq > %d)", events.DefaultDLQStream, env.dlqSeqBefore)
	}

	info, err := jsAdmin.ConsumerInfo(streamName, events.DurableConsumerName)
	if err != nil {
		t.Fatalf("ConsumerInfo: %v", err)
	}
	if info.Config.DeliverGroup != events.QueueGroupName || info.Config.MaxDeliver != events.JetStreamMaxDeliver || !info.PushBound {
		t.Fatalf("unexpected upgraded consumer info: DeliverGroup=%q MaxDeliver=%d PushBound=%v",
			info.Config.DeliverGroup, info.Config.MaxDeliver, info.PushBound)
	}
}
