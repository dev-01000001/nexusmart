package events

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nexusmart/core/pkg/common/jetstream"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/product/internal/store"
)

type stubShopStatusHandler struct {
	mu       sync.Mutex
	onHandle func(ctx context.Context, eventID string, payload ShopStatusChangedPayload) error
	calls    atomic.Int32
}

func (h *stubShopStatusHandler) HandleShopStatusChanged(ctx context.Context, eventID string, payload ShopStatusChangedPayload) error {
	h.calls.Add(1)
	h.mu.Lock()
	fn := h.onHandle
	h.mu.Unlock()
	if fn != nil {
		return fn(ctx, eventID, payload)
	}
	return nil
}

type stubDLQStore struct {
	mu      sync.Mutex
	err     error
	lastCtx error
	records []store.DeadLetterEvent
}

func (s *stubDLQStore) InsertDeadLetterEvent(ctx context.Context, ev store.DeadLetterEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastCtx = ctx.Err()
	if s.err != nil {
		return s.err
	}
	s.records = append(s.records, ev)
	return nil
}

func makeShopStatusEvent(t *testing.T, eventID, shopID, fromStatus, toStatus string, version int64) []byte {
	t.Helper()
	payloadBytes, err := json.Marshal(ShopStatusChangedPayload{
		ShopID:      shopID,
		OwnerUserID: "owner-" + shopID,
		ShopName:    "Shop " + shopID,
		FromStatus:  fromStatus,
		ToStatus:    toStatus,
		Version:     version,
		Reason:      "policy review",
		ActorID:     "ops-1",
		ApproverID:  "ops-2",
		OccurredAt:  time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	body, err := json.Marshal(Envelope{
		EventID:    eventID,
		EventType:  SubjectShopStatusChanged,
		OccurredAt: time.Now().UTC(),
		Payload:    payloadBytes,
	})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return body
}

func TestShopStatusConsumer_IdempotencyStaleVersionAndProductSync(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()

	cat, err := st.CreateCategory(ctx, store.Category{
		ID: "cat-1", Name: "Electronics", Slug: "electronics", CommissionRateBps: 500,
	})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	createProd := func(id, shopID, status string) store.Product {
		t.Helper()
		p, cErr := st.CreateProduct(ctx, store.Product{
			ID: id, ShopID: shopID, CategoryID: cat.ID, Name: "Prod " + id, Slug: "slug-" + id, Status: status,
		})
		if cErr != nil {
			t.Fatalf("CreateProduct %s: %v", id, cErr)
		}
		return p
	}

	pApproved := createProd("p-appr", "shop-1", store.ProductStatusApproved)
	pHidden := createProd("p-hid", "shop-1", store.ProductStatusHidden)
	pPending := createProd("p-pend", "shop-1", store.ProductStatusPending)
	pOtherShop := createProd("p-other", "shop-2", store.ProductStatusApproved)

	dlqPub := &recordingPublisher{}
	handler := NewShopStatusHandler(st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	c := NewConsumer(nil, dlqPub, st, handler, slog.New(slog.NewTextHandler(io.Discard, nil)), "NEXUSMART")

	// 1. Suspend shop-1 (version 1) -> all shop-1 products become shop_suspended=true, original statuses unchanged, shop-2 untouched.
	ev1 := makeShopStatusEvent(t, "evt-s1-v1", "shop-1", "ACTIVE", "SUSPENDED", 1)
	if action, pErr := c.ProcessRawMessage(ctx, SubjectShopStatusChanged, ev1, nil, 1); pErr != nil || action != ActionAck {
		t.Fatalf("v1 suspend: want ActionAck nil, got action=%v err=%v", action, pErr)
	}

	state1, err := st.GetShopSuspensionState(ctx, "shop-1")
	if err != nil || !state1.Suspended || state1.Version != 1 {
		t.Fatalf("state after v1: got %+v err=%v", state1, err)
	}
	for _, tc := range []struct {
		id         string
		wantStatus string
		wantSusp   bool
	}{
		{pApproved.ID, store.ProductStatusApproved, true},
		{pHidden.ID, store.ProductStatusHidden, true},
		{pPending.ID, store.ProductStatusPending, true},
		{pOtherShop.ID, store.ProductStatusApproved, false},
	} {
		got, gErr := st.GetProductByID(ctx, tc.id)
		if gErr != nil {
			t.Fatalf("GetProductByID %s: %v", tc.id, gErr)
		}
		if got.Status != tc.wantStatus || got.ShopSuspended != tc.wantSusp {
			t.Fatalf("product %s after v1: status=%s suspended=%v, want status=%s suspended=%v",
				tc.id, got.Status, got.ShopSuspended, tc.wantStatus, tc.wantSusp)
		}
	}

	// New product created for suspended shop-1 inherits shop_suspended=true from shop_suspension_state.
	pCreatedWhileSusp := createProd("p-while-susp", "shop-1", store.ProductStatusPending)
	if !pCreatedWhileSusp.ShopSuspended {
		t.Fatalf("product created while shop-1 is suspended must have shop_suspended=true")
	}

	// 2. Duplicate event_id (even with different status in payload) MUST be deduplicated via processed_events.
	dupEv := makeShopStatusEvent(t, "evt-s1-v1", "shop-1", "SUSPENDED", "ACTIVE", 99)
	if action, pErr := c.ProcessRawMessage(ctx, SubjectShopStatusChanged, dupEv, nil, 1); pErr != nil || action != ActionAck {
		t.Fatalf("duplicate event_id: want ActionAck nil, got action=%v err=%v", action, pErr)
	}
	stateAfterDup, _ := st.GetShopSuspensionState(ctx, "shop-1")
	if !stateAfterDup.Suspended || stateAfterDup.Version != 1 {
		t.Fatalf("duplicate event_id mutated state: %+v", stateAfterDup)
	}

	// 3. Unsuspend shop-1 (version 2) -> APPROVED & new product become shop_suspended=false, HIDDEN remains HIDDEN.
	ev2 := makeShopStatusEvent(t, "evt-s1-v2", "shop-1", "SUSPENDED", "ACTIVE", 2)
	if action, pErr := c.ProcessRawMessage(ctx, SubjectShopStatusChanged, ev2, nil, 1); pErr != nil || action != ActionAck {
		t.Fatalf("v2 unsuspend: want ActionAck nil, got action=%v err=%v", action, pErr)
	}
	state2, _ := st.GetShopSuspensionState(ctx, "shop-1")
	if state2.Suspended || state2.Version != 2 {
		t.Fatalf("state after v2: got %+v", state2)
	}
	gotHid, _ := st.GetProductByID(ctx, pHidden.ID)
	if gotHid.Status != store.ProductStatusHidden || gotHid.ShopSuspended {
		t.Fatalf("HIDDEN product after unsuspend: want HIDDEN + shop_suspended=false, got %+v", gotHid)
	}
	gotNew, _ := st.GetProductByID(ctx, pCreatedWhileSusp.ID)
	if gotNew.ShopSuspended {
		t.Fatalf("product created during suspension must clear shop_suspended after unsuspend")
	}

	// 4. Stale older event arriving late (version 1 <= 2, new event_id) AND equal version (version 2 <= 2) MUST be ignored (F12).
	for _, tc := range []struct {
		name    string
		eventID string
		version int64
	}{
		{"stale_v1_after_v2", "evt-s1-late-v1", 1},
		{"equal_v2_after_v2", "evt-s1-eq-v2", 2},
	} {
		ev := makeShopStatusEvent(t, tc.eventID, "shop-1", "ACTIVE", "SUSPENDED", tc.version)
		if action, pErr := c.ProcessRawMessage(ctx, SubjectShopStatusChanged, ev, nil, 1); pErr != nil || action != ActionAck {
			t.Fatalf("%s: want ActionAck nil, got action=%v err=%v", tc.name, action, pErr)
		}
		curState, _ := st.GetShopSuspensionState(ctx, "shop-1")
		if curState.Suspended || curState.Version != 2 {
			t.Fatalf("%s overwrote newer state: %+v", tc.name, curState)
		}
		gotAppr, _ := st.GetProductByID(ctx, pApproved.ID)
		if gotAppr.ShopSuspended {
			t.Fatalf("%s unexpectedly re-suspended product %s", tc.name, pApproved.ID)
		}
	}

	// 5. Invalid payload (missing shop_id, unknown status, missing/zero/negative version) fails & routes to DLQ at maxAttempts.
	missingVersionRaw := []byte(`{
		"event_id": "evt-bad-missing-ver",
		"event_type": "user.shop_status_changed",
		"occurred_at": "2026-11-24T12:00:00Z",
		"payload": {
			"shop_id": "shop-2",
			"from_status": "ACTIVE",
			"to_status": "SUSPENDED"
		}
	}`)
	for _, tc := range []struct {
		name string
		raw  []byte
	}{
		{"missing_shop_id", makeShopStatusEvent(t, "evt-bad-1", "", "ACTIVE", "SUSPENDED", 3)},
		{"unknown_status", makeShopStatusEvent(t, "evt-bad-2", "shop-1", "ACTIVE", "BANISHED", 3)},
		{"negative_version", makeShopStatusEvent(t, "evt-bad-3", "shop-1", "ACTIVE", "SUSPENDED", -1)},
		{"zero_version", makeShopStatusEvent(t, "evt-bad-4", "shop-2", "ACTIVE", "SUSPENDED", 0)},
		{"missing_version_field", missingVersionRaw},
	} {
		if action, pErr := c.ProcessRawMessage(ctx, SubjectShopStatusChanged, tc.raw, nil, 1); pErr == nil || action != ActionNak {
			t.Fatalf("%s attempt 1: want ActionNak + err, got action=%v err=%v", tc.name, action, pErr)
		}
		if action, pErr := c.ProcessRawMessage(ctx, SubjectShopStatusChanged, tc.raw, nil, defaultMaxDeliverAttempts); pErr == nil || action != ActionDLQ {
			t.Fatalf("%s attempt %d: want ActionDLQ + err, got action=%v err=%v", tc.name, defaultMaxDeliverAttempts, action, pErr)
		}
	}
	if len(st.DeadLetterEvents()) != 5 || len(dlqPub.msgs) != 5 {
		t.Fatalf("expected 5 DLQ records in DB and NATS, got db=%d nats=%d", len(st.DeadLetterEvents()), len(dlqPub.msgs))
	}
	// shop-2 had no prior suspension state: missing/zero version MUST NOT insert state or suspend its products.
	if _, sErr := st.GetShopSuspensionState(ctx, "shop-2"); !errors.Is(sErr, store.ErrNotFound) {
		t.Fatalf("missing/zero version must not persist shop_suspension_state for shop-2, got err=%v", sErr)
	}
	if gotOther, gErr := st.GetProductByID(ctx, pOtherShop.ID); gErr != nil || gotOther.ShopSuspended {
		t.Fatalf("missing/zero version must not suspend shop-2 product: got %+v err=%v", gotOther, gErr)
	}
}

func TestShopStatusConsumer_E1_ShutdownAndDLQGuarantees(t *testing.T) {
	t.Run("CanceledCtxAtAttempt5_NoDLQAndNoAck", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		var logBuf bytes.Buffer
		log := logger.New("product-test", logger.WithWriter(&logBuf))
		dlqStore := &stubDLQStore{}
		dlqPub := &recordingPublisher{}
		h := &stubShopStatusHandler{
			onHandle: func(hCtx context.Context, _ string, _ ShopStatusChangedPayload) error {
				cancel()
				return hCtx.Err()
			},
		}
		c := NewConsumer(nil, dlqPub, dlqStore, h, log, "NEXUSMART")
		body := makeShopStatusEvent(t, "evt-cancel-att5", "shop-1", "ACTIVE", "SUSPENDED", 1)

		action, err := c.ProcessRawMessage(ctx, SubjectShopStatusChanged, body, nil, defaultMaxDeliverAttempts)
		if err == nil || action != ActionNak {
			t.Fatalf("E1: canceled ctx at attempt 5 must return ActionNak (not ActionDLQ), got action=%v err=%v", action, err)
		}
		if len(dlqStore.records) != 0 || len(dlqPub.msgs) != 0 {
			t.Fatalf("E1: canceled ctx at attempt 5 must NOT write DLQ (store=%d pub=%d)", len(dlqStore.records), len(dlqPub.msgs))
		}

		ctx2, cancel2 := context.WithCancel(context.Background())
		h.onHandle = func(hCtx context.Context, _ string, _ ShopStatusChangedPayload) error {
			cancel2()
			return hCtx.Err()
		}
		logBuf.Reset()
		c.handleNATSMessage(ctx2, &nats.Msg{Subject: SubjectShopStatusChanged, Data: body})
		if strings.Contains(logBuf.String(), "failed to ack") || strings.Contains(logBuf.String(), "failed to nak") {
			t.Fatalf("E1: shutdown handleNATSMessage must neither Ack nor Nak, got logs: %s", logBuf.String())
		}
	})

	t.Run("DLQWriteFails_ReturnsNakNeverAck_IncludingMalformed", func(t *testing.T) {
		var logBuf bytes.Buffer
		log := logger.New("product-test", logger.WithWriter(&logBuf))
		dlqStore := &stubDLQStore{err: errors.New("postgres down")}
		dlqPub := &recordingPublisher{err: nats.ErrDisconnected}
		h := &stubShopStatusHandler{
			onHandle: func(_ context.Context, _ string, _ ShopStatusChangedPayload) error {
				return errors.New("poison handler error")
			},
		}
		c := NewConsumer(nil, dlqPub, dlqStore, h, log, "NEXUSMART")

		cases := []struct {
			name    string
			payload []byte
			attempt uint64
		}{
			{"Attempt5HandlerError", makeShopStatusEvent(t, "evt-dlq-fail-5", "shop-1", "ACTIVE", "SUSPENDED", 1), defaultMaxDeliverAttempts},
			{"MalformedJSON", []byte("{not-json"), 1},
			{"MissingEventID", []byte(`{"event_type":"user.shop_status_changed","payload":{}}`), 1},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				action, err := c.ProcessRawMessage(context.Background(), SubjectShopStatusChanged, tc.payload, nil, tc.attempt)
				if err == nil || action != ActionNak {
					t.Fatalf("E1 (%s): when both DLQ sinks fail, must return ActionNak (never ActionDLQ/Ack), got action=%v err=%v", tc.name, action, err)
				}
				logBuf.Reset()
				c.handleNATSMessage(context.Background(), &nats.Msg{Subject: SubjectShopStatusChanged, Data: tc.payload})
				if strings.Contains(logBuf.String(), "failed to ack") {
					t.Fatalf("E1 (%s): handleNATSMessage attempted Ack despite DLQ failure! logs=%s", tc.name, logBuf.String())
				}
				if !strings.Contains(logBuf.String(), "failed to nak") {
					t.Fatalf("E1 (%s): handleNATSMessage should attempt NakWithDelay when DLQ fails, logs=%s", tc.name, logBuf.String())
				}
			})
		}
	})

	t.Run("DLQOneSinkSucceedsWithExpiredHandlerDeadline_UsesWithoutCancel", func(t *testing.T) {
		dlqStore := &stubDLQStore{}
		dlqPub := &recordingPublisher{err: nats.ErrTimeout}
		expiredCtx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		h := &stubShopStatusHandler{
			onHandle: func(hCtx context.Context, _ string, _ ShopStatusChangedPayload) error {
				return hCtx.Err()
			},
		}
		c := NewConsumer(nil, dlqPub, dlqStore, h, logger.New("product-test", logger.WithWriter(&bytes.Buffer{})), "NEXUSMART")
		action, err := c.ProcessRawMessage(expiredCtx, SubjectShopStatusChanged, makeShopStatusEvent(t, "evt-dl-exp", "shop-1", "ACTIVE", "SUSPENDED", 1), nil, defaultMaxDeliverAttempts)
		if err == nil || action != ActionDLQ {
			t.Fatalf("expected ActionDLQ when DB DLQ succeeds via WithoutCancel, got action=%v err=%v", action, err)
		}
		if len(dlqStore.records) != 1 || dlqStore.lastCtx != nil {
			t.Fatalf("expected 1 DLQ record saved with live detached ctx (err=nil), got records=%d lastCtx=%v", len(dlqStore.records), dlqStore.lastCtx)
		}
	})

	t.Run("StopWaitsInFlightAndStoppingSkipsHandlerWithoutNak", func(t *testing.T) {
		started := make(chan struct{}, 1)
		release := make(chan struct{})
		var logBuf bytes.Buffer
		log := logger.New("product-test", logger.WithWriter(&logBuf))
		h := &stubShopStatusHandler{
			onHandle: func(_ context.Context, _ string, _ ShopStatusChangedPayload) error {
				started <- struct{}{}
				<-release
				return nil
			},
		}
		c := NewConsumer(nil, nil, nil, h, log, "NEXUSMART")
		msg := &nats.Msg{Subject: SubjectShopStatusChanged, Data: makeShopStatusEvent(t, "evt-inflight-1", "shop-1", "ACTIVE", "SUSPENDED", 1)}

		go c.onMessage(context.Background(), msg)
		<-started

		stopDone := make(chan error, 1)
		go func() {
			stopDone <- c.Stop()
		}()

		select {
		case err := <-stopDone:
			t.Fatalf("Consumer.Stop returned early while handler was still in-flight (err=%v)", err)
		case <-time.After(60 * time.Millisecond):
		}

		close(release)
		select {
		case err := <-stopDone:
			if err != nil {
				t.Fatalf("Consumer.Stop returned error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Consumer.Stop did not return after in-flight handler completed")
		}

		logBuf.Reset()
		c.onMessage(context.Background(), msg)
		if got := h.calls.Load(); got != 1 {
			t.Fatalf("E1: stopping consumer must not invoke handler, got %d calls", got)
		}
		if logBuf.Len() != 0 {
			t.Fatalf("E1: stopping consumer must return quietly without Ack/Nak, got logs: %s", logBuf.String())
		}
	})
}

func requireTestNATSURL(t *testing.T) string {
	t.Helper()
	url := strings.TrimSpace(os.Getenv("TEST_NATS_URL"))
	if url == "" {
		t.Skip("TEST_NATS_URL not set; run via scripts/test-db.sh")
	}
	return url
}

type failFirstPublisher struct {
	calls atomic.Int32
	inner Publisher
}

func (f *failFirstPublisher) PublishMsg(m *nats.Msg, opts ...nats.PubOpt) (*nats.PubAck, error) {
	if f.calls.Add(1) == 1 {
		return nil, errors.New("transient nats dlq failure on attempt 1")
	}
	return f.inner.PublishMsg(m, opts...)
}

func TestShopStatusConsumer_RealNATS_E1RestartNoReplayAndDLQHeadroom(t *testing.T) {
	natsURL := requireTestNATSURL(t)

	t.Run("RestartTwicePreservesDurableAndDoesNotReplay", func(t *testing.T) {
		suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
		streamName := "PRODUCT_SHOP_TEST_" + strings.ToUpper(suffix)
		subject := fmt.Sprintf("prodtest.%s.user.shop_status_changed", suffix)
		durable := "product-shop-status-test-" + suffix
		queue := "product-shop-status-workers-" + suffix

		adminNC, err := nats.Connect(natsURL)
		if err != nil {
			t.Fatalf("connect admin nats: %v", err)
		}
		adminJS, err := adminNC.JetStream()
		if err != nil {
			adminNC.Close()
			t.Fatalf("admin jetstream: %v", err)
		}
		t.Cleanup(func() {
			_ = adminJS.DeleteStream(streamName)
			adminNC.Close()
		})
		if err := EnsureStreams(adminJS, jetstream.DefaultPrimaryStream); err != nil {
			t.Fatalf("EnsureStreams: %v", err)
		}
		if err := jetstream.EnsureStream(adminJS, &nats.StreamConfig{
			Name:      streamName,
			Subjects:  []string{subject},
			Retention: nats.LimitsPolicy,
			Storage:   nats.FileStorage,
		}); err != nil {
			t.Fatalf("create test stream: %v", err)
		}

		var mu sync.Mutex
		counts := make(map[string]int)
		var totalHandled atomic.Int32
		receivedCh := make(chan string, 16)

		handler := &stubShopStatusHandler{
			onHandle: func(_ context.Context, eventID string, _ ShopStatusChangedPayload) error {
				mu.Lock()
				counts[eventID]++
				mu.Unlock()
				totalHandled.Add(1)
				receivedCh <- eventID
				return nil
			},
		}

		publish := func(eventID, toStatus string, ver int64) {
			t.Helper()
			raw := makeShopStatusEvent(t, eventID, "shop-r1", "ACTIVE", toStatus, ver)
			if _, pubErr := adminJS.Publish(subject, raw); pubErr != nil {
				t.Fatalf("publish %s: %v", eventID, pubErr)
			}
		}

		waitFor := func(wantID string) {
			t.Helper()
			timeout := time.After(5 * time.Second)
			for {
				select {
				case got := <-receivedCh:
					if got == wantID {
						return
					}
				case <-timeout:
					t.Fatalf("timed out waiting for %s", wantID)
				}
			}
		}

		startInstance := func() (*nats.Conn, context.CancelFunc, *Consumer) {
			t.Helper()
			nc, cErr := nats.Connect(natsURL)
			if cErr != nil {
				t.Fatalf("connect consumer nats: %v", cErr)
			}
			js, jErr := nc.JetStream()
			if jErr != nil {
				nc.Close()
				t.Fatalf("consumer jetstream: %v", jErr)
			}
			ctx, cancel := context.WithCancel(context.Background())
			c := NewConsumer(js, js, nil, handler, slog.New(slog.NewTextHandler(io.Discard, nil)), streamName)
			c.SetSubscription(subject, durable, queue)
			if sErr := c.Start(ctx); sErr != nil {
				cancel()
				nc.Close()
				t.Fatalf("consumer start: %v", sErr)
			}
			return nc, cancel, c
		}

		// Boot 1: consume 2 events, stop cleanly; durable consumer MUST survive both before & after nc.Close().
		publish("evt-p-boot1-a", "SUSPENDED", 1)
		publish("evt-p-boot1-b", "ACTIVE", 2)
		nc1, cancel1, c1 := startInstance()
		waitFor("evt-p-boot1-a")
		waitFor("evt-p-boot1-b")
		time.Sleep(100 * time.Millisecond)
		cancel1()
		if err := c1.Stop(); err != nil {
			t.Fatalf("boot 1 Stop: %v", err)
		}
		time.Sleep(150 * time.Millisecond)
		if _, err := adminJS.ConsumerInfo(streamName, durable); err != nil {
			t.Fatalf("E1: durable consumer %s deleted by Stop() while nc open: %v", durable, err)
		}
		nc1.Close()

		info1, err := adminJS.ConsumerInfo(streamName, durable)
		if err != nil {
			t.Fatalf("E1: durable consumer %s missing after nc.Close(): %v", durable, err)
		}
		if info1.AckFloor.Stream < 2 {
			t.Fatalf("E1: expected AckFloor.Stream >= 2 after boot 1, got %+v", info1.AckFloor)
		}

		// Boot 2 (restart #1): must NOT replay boot 1 events; only process evt-p-boot2-c.
		nc2, cancel2, c2 := startInstance()
		publish("evt-p-boot2-c", "SUSPENDED", 3)
		waitFor("evt-p-boot2-c")
		time.Sleep(100 * time.Millisecond)
		cancel2()
		_ = c2.Stop()
		nc2.Close()

		// Boot 3 (restart #2): must NOT replay earlier events; only process evt-p-boot3-d.
		nc3, cancel3, c3 := startInstance()
		publish("evt-p-boot3-d", "ACTIVE", 4)
		waitFor("evt-p-boot3-d")
		time.Sleep(100 * time.Millisecond)
		cancel3()
		_ = c3.Stop()
		nc3.Close()

		mu.Lock()
		defer mu.Unlock()
		for _, id := range []string{"evt-p-boot1-a", "evt-p-boot1-b", "evt-p-boot2-c", "evt-p-boot3-d"} {
			if counts[id] != 1 {
				t.Errorf("E1: event %s handled %d times across 2 restarts, want 1 (counts=%v)", id, counts[id], counts)
			}
		}
		if got := totalHandled.Load(); got != 4 {
			t.Fatalf("E1: expected total 4 handled messages across 2 restarts, got %d (counts=%v)", got, counts)
		}
	})

	t.Run("RealNATSDLQRetryWithMaxDeliverHeadroom", func(t *testing.T) {
		suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
		streamName := "PRODUCT_DLQ_TEST_" + strings.ToUpper(suffix)
		subject := fmt.Sprintf("proddlq.%s.user.shop_status_changed", suffix)
		durable := "product-dlq-test-" + suffix
		queue := "product-dlq-workers-" + suffix

		adminNC, err := nats.Connect(natsURL)
		if err != nil {
			t.Fatalf("connect admin nats: %v", err)
		}
		adminJS, err := adminNC.JetStream()
		if err != nil {
			adminNC.Close()
			t.Fatalf("admin jetstream: %v", err)
		}
		t.Cleanup(func() {
			_ = adminJS.DeleteStream(streamName)
			adminNC.Close()
		})
		if err := EnsureStreams(adminJS, jetstream.DefaultPrimaryStream); err != nil {
			t.Fatalf("EnsureStreams: %v", err)
		}
		if err := jetstream.EnsureStream(adminJS, &nats.StreamConfig{
			Name:      streamName,
			Subjects:  []string{subject},
			Retention: nats.LimitsPolicy,
			Storage:   nats.FileStorage,
		}); err != nil {
			t.Fatalf("create stream: %v", err)
		}

		nc, err := nats.Connect(natsURL)
		if err != nil {
			t.Fatalf("connect consumer nats: %v", err)
		}
		defer nc.Close()
		js, err := nc.JetStream()
		if err != nil {
			t.Fatalf("consumer jetstream: %v", err)
		}

		var attempts atomic.Int32
		dlqStore := &stubDLQStore{err: errors.New("db unavailable")}
		flakyPub := &failFirstPublisher{inner: js}

		c := NewConsumer(
			js,
			flakyPub,
			dlqStore,
			&stubShopStatusHandler{
				onHandle: func(_ context.Context, _ string, _ ShopStatusChangedPayload) error {
					attempts.Add(1)
					return errors.New("permanent poison error")
				},
			},
			slog.New(slog.NewTextHandler(io.Discard, nil)),
			streamName,
		)
		c.SetMaxAttempts(1) // MaxDeliver=2 gives headroom for 1 retry after dual DLQ failure
		c.SetSubscription(subject, durable, queue)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if err := c.Start(ctx); err != nil {
			t.Fatalf("start consumer: %v", err)
		}
		defer func() { _ = c.Stop() }()

		raw := makeShopStatusEvent(t, "evt-real-dlq-headroom", "shop-dlq", "ACTIVE", "SUSPENDED", 1)
		if _, err := adminJS.Publish(subject, raw); err != nil {
			t.Fatalf("publish event: %v", err)
		}

		deadline := time.Now().Add(8 * time.Second)
		var dlqMsg *nats.RawStreamMsg
		for time.Now().Before(deadline) {
			msg, getErr := adminJS.GetLastMsg(DLQStreamName, "dlq."+subject)
			if getErr == nil && msg != nil {
				dlqMsg = msg
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if dlqMsg == nil {
			t.Fatalf("E1: expected message to reach %s on MaxDeliver headroom retry (attempts=%d)", DLQStreamName, attempts.Load())
		}
		if got := attempts.Load(); got != 2 {
			t.Fatalf("E1: expected 2 delivery attempts (1st Nak on dual DLQ fail, 2nd Ack on DLQ recovery), got %d", got)
		}
		if dlqMsg.Subject != "dlq."+subject {
			t.Fatalf("unexpected DLQ subject: %q", dlqMsg.Subject)
		}
	})
}

func TestShopStatusChanged_ContractsSection5PayloadDecoding(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()

	cat, err := st.CreateCategory(ctx, store.Category{
		ID: "cat-contract", Name: "Contract Cat", Slug: "contract-cat", CommissionRateBps: 500,
	})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	if _, err := st.CreateProduct(ctx, store.Product{
		ID: "p-contract-appr", ShopID: "shop-contract-1", CategoryID: cat.ID,
		Name: "Contract Approved", Slug: "contract-approved", Status: store.ProductStatusApproved,
	}); err != nil {
		t.Fatalf("CreateProduct appr: %v", err)
	}
	if _, err := st.CreateProduct(ctx, store.Product{
		ID: "p-contract-hid", ShopID: "shop-contract-1", CategoryID: cat.ID,
		Name: "Contract Hidden", Slug: "contract-hidden", Status: store.ProductStatusHidden,
	}); err != nil {
		t.Fatalf("CreateProduct hid: %v", err)
	}

	// Exact canonical envelope + payload schema from docs/contracts.md §5:
	// {"shop_id","owner_user_id","shop_name","from_status":"ACTIVE"|"SUSPENDED","to_status":"SUSPENDED"|"ACTIVE","reason","actor_id","approver_id","version":1,"occurred_at"}
	const rawSuspendContract = `{
		"event_id": "evt-contract-suspend-1",
		"event_type": "user.shop_status_changed",
		"trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
		"occurred_at": "2026-11-24T12:00:00Z",
		"payload": {
			"shop_id": "shop-contract-1",
			"owner_user_id": "user-owner-1",
			"shop_name": "Nexus Flagship Store",
			"from_status": "ACTIVE",
			"to_status": "SUSPENDED",
			"reason": "counterfeit complaint verified",
			"actor_id": "staff-ops-1",
			"approver_id": "staff-admin-2",
			"version": 1,
			"occurred_at": "2026-11-24T12:00:00Z"
		}
	}`

	var env Envelope
	if err := json.Unmarshal([]byte(rawSuspendContract), &env); err != nil {
		t.Fatalf("unmarshal contract envelope: %v", err)
	}
	if env.EventID != "evt-contract-suspend-1" || env.EventType != SubjectShopStatusChanged || env.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("unexpected decoded envelope: %+v", env)
	}
	var payload ShopStatusChangedPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		t.Fatalf("unmarshal contract payload: %v", err)
	}
	if payload.ShopID != "shop-contract-1" ||
		payload.OwnerUserID != "user-owner-1" ||
		payload.ShopName != "Nexus Flagship Store" ||
		payload.FromStatus != "ACTIVE" ||
		payload.ToStatus != "SUSPENDED" ||
		payload.Reason != "counterfeit complaint verified" ||
		payload.ActorID != "staff-ops-1" ||
		payload.ApproverID != "staff-admin-2" ||
		payload.Version != 1 ||
		payload.OccurredAt != "2026-11-24T12:00:00Z" {
		t.Fatalf("decoded contract payload mismatch: %+v", payload)
	}

	dlqPub := &recordingPublisher{}
	handler := NewShopStatusHandler(st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	c := NewConsumer(nil, dlqPub, st, handler, slog.New(slog.NewTextHandler(io.Discard, nil)), "NEXUSMART")

	// End-to-end processing of the contract suspend sample sets shop_suspended=true, version=1.
	if action, err := c.ProcessRawMessage(ctx, SubjectShopStatusChanged, []byte(rawSuspendContract), nil, 1); err != nil || action != ActionAck {
		t.Fatalf("process contract suspend sample: want ActionAck nil, got action=%v err=%v", action, err)
	}
	state1, err := st.GetShopSuspensionState(ctx, "shop-contract-1")
	if err != nil || !state1.Suspended || state1.Version != 1 {
		t.Fatalf("state after contract suspend: got %+v err=%v", state1, err)
	}
	gotAppr, _ := st.GetProductByID(ctx, "p-contract-appr")
	if !gotAppr.ShopSuspended || gotAppr.Status != store.ProductStatusApproved {
		t.Fatalf("approved product after contract suspend: %+v", gotAppr)
	}

	// Verify unsuspend sample with >32-bit int64 version decodes accurately without truncation.
	const bigVersion int64 = 9007199254740993 // 2^53 + 1 (> float64 exact int & > int32 max)
	rawUnsuspendContract := fmt.Sprintf(`{
		"event_id": "evt-contract-unsuspend-bigver",
		"event_type": "user.shop_status_changed",
		"trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
		"occurred_at": "2026-11-24T13:00:00Z",
		"payload": {
			"shop_id": "shop-contract-1",
			"owner_user_id": "user-owner-1",
			"shop_name": "Nexus Flagship Store",
			"from_status": "SUSPENDED",
			"to_status": "ACTIVE",
			"reason": "appeal approved",
			"actor_id": "staff-ops-1",
			"approver_id": "staff-admin-2",
			"version": %d,
			"occurred_at": "2026-11-24T13:00:00Z"
		}
	}`, bigVersion)

	if action, err := c.ProcessRawMessage(ctx, SubjectShopStatusChanged, []byte(rawUnsuspendContract), nil, 1); err != nil || action != ActionAck {
		t.Fatalf("process contract unsuspend sample: want ActionAck nil, got action=%v err=%v", action, err)
	}
	state2, err := st.GetShopSuspensionState(ctx, "shop-contract-1")
	if err != nil || state2.Suspended || state2.Version != bigVersion {
		t.Fatalf("state after int64 version unsuspend: want suspended=false version=%d, got %+v err=%v", bigVersion, state2, err)
	}
	gotHid, _ := st.GetProductByID(ctx, "p-contract-hid")
	if gotHid.ShopSuspended || gotHid.Status != store.ProductStatusHidden {
		t.Fatalf("hidden product after contract unsuspend must preserve HIDDEN: %+v", gotHid)
	}
}

func TestShopStatusChanged_MissingOrZeroVersionRoutesToDLQ(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()

	cat, err := st.CreateCategory(ctx, store.Category{
		ID: "cat-missing-ver", Name: "Missing Ver Cat", Slug: "missing-ver-cat", CommissionRateBps: 500,
	})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	if _, err := st.CreateProduct(ctx, store.Product{
		ID: "p-fresh-shop", ShopID: "shop-fresh", CategoryID: cat.ID,
		Name: "Fresh Shop Product", Slug: "fresh-shop-product", Status: store.ProductStatusApproved,
	}); err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}

	dlqPub := &recordingPublisher{}
	handler := NewShopStatusHandler(st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	c := NewConsumer(nil, dlqPub, st, handler, slog.New(slog.NewTextHandler(io.Discard, nil)), "NEXUSMART")

	cases := []struct {
		name string
		raw  []byte
	}{
		{
			name: "missing_version_field",
			raw: []byte(`{
				"event_id": "evt-mv-1",
				"event_type": "user.shop_status_changed",
				"occurred_at": "2026-11-24T12:00:00Z",
				"payload": {
					"shop_id": "shop-fresh",
					"from_status": "ACTIVE",
					"to_status": "SUSPENDED"
				}
			}`),
		},
		{
			name: "wrong_field_name_status_version",
			raw: []byte(`{
				"event_id": "evt-mv-2",
				"event_type": "user.shop_status_changed",
				"occurred_at": "2026-11-24T12:00:00Z",
				"payload": {
					"shop_id": "shop-fresh",
					"from_status": "ACTIVE",
					"to_status": "SUSPENDED",
					"status_version": 1
				}
			}`),
		},
		{
			name: "explicit_zero_version",
			raw:  makeShopStatusEvent(t, "evt-mv-3", "shop-fresh", "ACTIVE", "SUSPENDED", 0),
		},
		{
			name: "negative_version",
			raw:  makeShopStatusEvent(t, "evt-mv-4", "shop-fresh", "ACTIVE", "SUSPENDED", -2),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			action, err := c.ProcessRawMessage(ctx, SubjectShopStatusChanged, tc.raw, nil, 1)
			if err == nil || action != ActionNak {
				t.Fatalf("attempt 1: want ActionNak + error, got action=%v err=%v", action, err)
			}
			action, err = c.ProcessRawMessage(ctx, SubjectShopStatusChanged, tc.raw, nil, defaultMaxDeliverAttempts)
			if err == nil || action != ActionDLQ {
				t.Fatalf("attempt %d: want ActionDLQ + error, got action=%v err=%v", defaultMaxDeliverAttempts, action, err)
			}
		})
	}

	if len(st.DeadLetterEvents()) != len(cases) || len(dlqPub.msgs) != len(cases) {
		t.Fatalf("expected %d DLQ records in DB and NATS, got db=%d nats=%d", len(cases), len(st.DeadLetterEvents()), len(dlqPub.msgs))
	}
	if _, err := st.GetShopSuspensionState(ctx, "shop-fresh"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("invalid version <= 0 must NOT create shop_suspension_state, got err=%v", err)
	}
	prod, err := st.GetProductByID(ctx, "p-fresh-shop")
	if err != nil {
		t.Fatalf("GetProductByID: %v", err)
	}
	if prod.ShopSuspended {
		t.Fatalf("invalid version <= 0 must NOT suspend shop-fresh product")
	}
}
