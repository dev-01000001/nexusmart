package events_test

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/promotion/internal/events"
	"github.com/nexusmart/core/services/promotion/internal/store"
)

func makeShopStatusChangedRaw(t *testing.T, eventID, shopID, fromStatus, toStatus string, version int64) []byte {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"shop_id":       shopID,
		"owner_user_id": "owner-" + shopID,
		"shop_name":     "Shop " + shopID,
		"from_status":   fromStatus,
		"to_status":     toStatus,
		"reason":        "violation",
		"actor_id":      "ops-1",
		"approver_id":   "ops-2",
		"version":       version,
		"occurred_at":   "2026-11-24T12:00:00Z",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	raw, err := json.Marshal(events.Envelope{
		EventID:    eventID,
		EventType:  "user.shop_status_changed",
		TraceID:    "4bf92f3577b34da6a3ce929d0e0e4736",
		OccurredAt: "2026-11-24T12:00:00Z",
		Payload:    payload,
	})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return raw
}

func TestShopStatusConsumer_F12_ValidationAndDLQ(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemoryStore()
	log := logger.New("promo-f12-events-test", logger.WithWriter(io.Discard))
	c := events.NewOrderCompletedConsumer(mem, nil, "NEXUSMART", log)
	dlq := &spyDLQPublisher{}
	c.SetDLQPublisher(dlq)

	badCases := []struct {
		name string
		raw  []byte
	}{
		{"VersionZero", makeShopStatusChangedRaw(t, "evt-v0", "shop-bad", "ACTIVE", "SUSPENDED", 0)},
		{"VersionNegative", makeShopStatusChangedRaw(t, "evt-vneg", "shop-bad", "ACTIVE", "SUSPENDED", -3)},
		{"MissingShopID", makeShopStatusChangedRaw(t, "evt-noshop", "", "ACTIVE", "SUSPENDED", 1)},
		{"UnknownToStatus", makeShopStatusChangedRaw(t, "evt-badst", "shop-bad", "ACTIVE", "UNKNOWN", 1)},
		{"MalformedPayloadJSON", []byte(`{"event_id":"evt-badjson","event_type":"user.shop_status_changed","payload":"oops"}`)},
	}

	for _, tc := range badCases {
		t.Run(tc.name, func(t *testing.T) {
			before := dlq.calls
			ack, err := c.ProcessRawMessage(ctx, tc.raw, nil, 1)
			if err != nil || !ack {
				t.Fatalf("%s: expected poison message acked after DLQ, got ack=%v err=%v", tc.name, ack, err)
			}
			if dlq.calls != before+1 {
				t.Fatalf("%s: expected 1 DLQ publish, got delta=%d", tc.name, dlq.calls-before)
			}
			gotSubj := dlq.subjects[len(dlq.subjects)-1]
			if gotSubj != "dlq.user.shop_status_changed" {
				t.Fatalf("%s: expected DLQ subject dlq.user.shop_status_changed, got %q", tc.name, gotSubj)
			}
		})
	}
}

func TestShopStatusConsumer_F12_RealPGAndNATS_IdempotencyStaleVersionAndDLQ(t *testing.T) {
	pgStore, pool := openTestEventsPG(t)
	env := setupTestNATS(t, true)
	ctx := context.Background()
	suffix := uuid.NewString()[:8]
	shopID := "shop-nats-" + suffix
	buyerID := "buyer-nats-" + suffix

	t.Cleanup(func() {
		_ = env.adminJS.DeleteConsumer(env.streamName, "promotion-shop-status")
	})

	// 1. Verify shop_suspension_state table exists from migration 006.
	var tblExists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = current_schema() AND table_name = 'shop_suspension_state'
		)
	`).Scan(&tblExists); err != nil {
		t.Fatalf("check shop_suspension_state table: %v", err)
	}
	if !tblExists {
		t.Fatal("shop_suspension_state table does not exist after Migrate()")
	}

	nc, err := nats.Connect(env.url)
	if err != nil {
		t.Fatalf("connect nats: %v", err)
	}
	defer nc.Close()
	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}

	log := logger.New("promo-f12-nats-pg", logger.WithWriter(io.Discard))
	c := events.NewOrderCompletedConsumer(pgStore, js, env.streamName, log)
	c.SetDeadLetterStore(events.NewPostgresDeadLetterStore(pool))

	runCtx, cancel := context.WithCancel(context.Background())
	if err := c.Start(runCtx); err != nil {
		cancel()
		t.Fatalf("consumer.Start: %v", err)
	}

	// Publish:
	// (a) valid suspend v2
	// (b) stale active v1 (must be ignored)
	// (c) duplicate event_id of suspend v2 with ACTIVE v99 payload (must be ignored via processed_events)
	// (d) poison version=0 (must land in dead_letter_events with subject=user.shop_status_changed)
	// (e) order.completed on same consumer (must still credit coins)
	evSuspV2 := "evt-pg-susp-v2-" + suffix
	evStaleV1 := "evt-pg-stale-v1-" + suffix
	evPoison := "evt-pg-poison-" + suffix
	evOrder := "evt-pg-oc-" + suffix

	if _, err := js.Publish("user.shop_status_changed", makeShopStatusChangedRaw(t, evSuspV2, shopID, "ACTIVE", "SUSPENDED", 2)); err != nil {
		t.Fatalf("publish suspend v2: %v", err)
	}
	// Wait until suspend v2 is persisted before publishing stale/dup so ordering is deterministic.
	deadline := time.Now().Add(5 * time.Second)
	var gotSusp bool
	var gotVer int64
	for time.Now().Before(deadline) {
		err := pool.QueryRow(ctx, `SELECT suspended, version FROM shop_suspension_state WHERE shop_id = $1`, shopID).Scan(&gotSusp, &gotVer)
		if err == nil && gotSusp && gotVer == 2 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !gotSusp || gotVer != 2 {
		t.Fatalf("expected shop_suspension_state (%s, true, 2), got suspended=%v version=%d", shopID, gotSusp, gotVer)
	}

	if _, err := js.Publish("user.shop_status_changed", makeShopStatusChangedRaw(t, evStaleV1, shopID, "SUSPENDED", "ACTIVE", 1)); err != nil {
		t.Fatalf("publish stale v1: %v", err)
	}
	if _, err := js.Publish("user.shop_status_changed", makeShopStatusChangedRaw(t, evSuspV2, shopID, "SUSPENDED", "ACTIVE", 99)); err != nil {
		t.Fatalf("publish dup event_id: %v", err)
	}
	if _, err := js.Publish("user.shop_status_changed", makeShopStatusChangedRaw(t, evPoison, shopID, "ACTIVE", "SUSPENDED", 0)); err != nil {
		t.Fatalf("publish poison v0: %v", err)
	}
	if _, err := js.Publish(events.SubjectOrderCompleted, makeOrderCompletedRaw(t, evOrder, "ord-f12-"+suffix, buyerID, 333)); err != nil {
		t.Fatalf("publish order.completed: %v", err)
	}

	// Wait for stale v1 processed_events, poison dead_letter_events, and order.completed coin credit.
	deadline2 := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline2) {
		var staleSeen bool
		var dlqCount int
		_ = pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM processed_events WHERE event_id = $1)`, evStaleV1).Scan(&staleSeen)
		_ = pool.QueryRow(ctx, `SELECT COUNT(1) FROM dead_letter_events WHERE event_id = $1 AND subject = 'user.shop_status_changed'`, evPoison).Scan(&dlqCount)
		bal, _, _ := pgStore.GetCoinBalance(ctx, buyerID, 5)
		if staleSeen && dlqCount >= 1 && bal == 333 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}

	if err := pool.QueryRow(ctx, `SELECT suspended, version FROM shop_suspension_state WHERE shop_id = $1`, shopID).Scan(&gotSusp, &gotVer); err != nil {
		t.Fatalf("re-query shop_suspension_state: %v", err)
	}
	if !gotSusp || gotVer != 2 {
		t.Fatalf("stale/duplicate/poison events corrupted shop_suspension_state: got suspended=%v version=%d", gotSusp, gotVer)
	}

	var dlqCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(1) FROM dead_letter_events WHERE event_id = $1 AND subject = 'user.shop_status_changed'`, evPoison).Scan(&dlqCount); err != nil || dlqCount != 1 {
		t.Fatalf("expected 1 dead_letter_events row for %s, got count=%d err=%v", evPoison, dlqCount, err)
	}

	// Publish unsuspend v3 -> state flips cleanly to suspended=false, version=3.
	evUnsuspV3 := "evt-pg-unsusp-v3-" + suffix
	if _, err := js.Publish("user.shop_status_changed", makeShopStatusChangedRaw(t, evUnsuspV3, shopID, "SUSPENDED", "ACTIVE", 3)); err != nil {
		t.Fatalf("publish unsuspend v3: %v", err)
	}
	deadline3 := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline3) {
		if err := pool.QueryRow(ctx, `SELECT suspended, version FROM shop_suspension_state WHERE shop_id = $1`, shopID).Scan(&gotSusp, &gotVer); err == nil && !gotSusp && gotVer == 3 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if gotSusp || gotVer != 3 {
		t.Fatalf("expected unsuspended (false, 3), got suspended=%v version=%d", gotSusp, gotVer)
	}

	// Graceful stop preserves BOTH durable consumers (E1).
	cancel()
	if err := c.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	nc.Close()
	if _, err := env.adminJS.ConsumerInfo(env.streamName, "promotion-shop-status"); err != nil {
		t.Fatalf("durable promotion-shop-status must survive Stop()+nc.Close(): %v", err)
	}
}
