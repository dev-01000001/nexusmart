package events_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/promotion/internal/events"
	"github.com/nexusmart/core/services/promotion/internal/store"
)

type spyDLQPublisher struct {
	mu          sync.Mutex
	err         error
	calls       int
	subjects    []string
	headers     []map[string]string
	lastCtxErr  error
	hasDeadline bool
}

func (s *spyDLQPublisher) PublishDLQ(ctx context.Context, subject string, _ []byte, headers map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.lastCtxErr = ctx.Err()
	_, s.hasDeadline = ctx.Deadline()
	if s.err != nil {
		return s.err
	}
	s.subjects = append(s.subjects, subject)
	hdrCopy := make(map[string]string, len(headers))
	for k, v := range headers {
		hdrCopy[k] = v
	}
	s.headers = append(s.headers, hdrCopy)
	return nil
}

type hookOrderStore struct {
	store.Store
	mu   sync.Mutex
	hook func(ctx context.Context, eventID, subject, orderID, buyerID string, coinsReward int64) (bool, error)
}

func (h *hookOrderStore) ProcessOrderCompletedEvent(ctx context.Context, eventID, subject, orderID, buyerID string, coinsReward int64) (bool, error) {
	h.mu.Lock()
	fn := h.hook
	h.mu.Unlock()
	if fn != nil {
		return fn(ctx, eventID, subject, orderID, buyerID, coinsReward)
	}
	return h.Store.ProcessOrderCompletedEvent(ctx, eventID, subject, orderID, buyerID, coinsReward)
}

func makeOrderCompletedRaw(t *testing.T, eventID, orderID, buyerID string, coins int64) []byte {
	t.Helper()
	payload, err := json.Marshal(events.OrderCompletedPayload{
		OrderID:         orderID,
		OrderCode:       "NM-" + orderID,
		BuyerID:         buyerID,
		ShopID:          "shop-1",
		SubtotalVND:     100000,
		CommissionVND:   5000,
		TotalPayableVND: 100000,
		CoinsReward:     coins,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	raw, err := json.Marshal(events.Envelope{
		EventID:    eventID,
		EventType:  events.SubjectOrderCompleted,
		TraceID:    "4bf92f3577b34da6a3ce929d0e0e4736",
		OccurredAt: "2026-11-24T10:00:00Z",
		Payload:    payload,
	})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return raw
}

func requireNATSURL(t *testing.T) string {
	t.Helper()
	url := strings.TrimSpace(os.Getenv("TEST_NATS_URL"))
	if url == "" {
		t.Skip("TEST_NATS_URL not set; run via scripts/test-db.sh")
	}
	return url
}

type testNATSEnv struct {
	url           string
	streamName    string
	adminNC       *nats.Conn
	adminJS       nats.JetStreamContext
	dlqMsgsBefore uint64
	dlqSeqBefore  uint64
}

// setupTestNATS connects to the shared TEST_NATS_URL without assuming a clean server.
// It creates a unique per-test primary stream unless another stream already owns order.completed,
// tracks whether NEXUSMART_DLQ pre-existed, and deletes only the consumer/streams created by this test in t.Cleanup.
func setupTestNATS(t *testing.T, createPrimaryStream bool) *testNATSEnv {
	t.Helper()
	natsURL := requireNATSURL(t)

	adminNC, err := nats.Connect(natsURL)
	if err != nil {
		t.Fatalf("connect admin nats: %v", err)
	}
	adminJS, err := adminNC.JetStream()
	if err != nil {
		adminNC.Close()
		t.Fatalf("admin jetstream: %v", err)
	}

	dlqPreExisted := false
	var dlqMsgsBefore, dlqSeqBefore uint64
	if dlqInfo, dlqErr := adminJS.StreamInfo(events.DefaultDLQStream); dlqErr == nil && dlqInfo != nil {
		dlqPreExisted = true
		dlqMsgsBefore = dlqInfo.State.Msgs
		dlqSeqBefore = dlqInfo.State.LastSeq
	}

	streamPreExisted := false
	streamName, findErr := adminJS.StreamNameBySubject(events.SubjectOrderCompleted)
	if findErr == nil && streamName != "" {
		streamPreExisted = true
		_ = adminJS.DeleteConsumer(streamName, events.DurableConsumerName)
	} else {
		streamName = fmt.Sprintf("NEXUSMART_PROMO_%s", uuid.NewString()[:8])
		if createPrimaryStream {
			if _, addErr := adminJS.AddStream(&nats.StreamConfig{
				Name:     streamName,
				Subjects: []string{"user.>", "order.>", "payment.>", "shipment.>", "product.>", "promotion.>", "review.>"},
			}); addErr != nil {
				adminNC.Close()
				t.Fatalf("add primary stream %s: %v", streamName, addErr)
			}
		}
	}

	if createPrimaryStream && !dlqPreExisted {
		if _, addErr := adminJS.AddStream(&nats.StreamConfig{
			Name:     events.DefaultDLQStream,
			Subjects: []string{"dlq.>"},
		}); addErr != nil {
			if !streamPreExisted {
				_ = adminJS.DeleteStream(streamName)
			}
			adminNC.Close()
			t.Fatalf("add dlq stream %s: %v", events.DefaultDLQStream, addErr)
		}
	}

	t.Cleanup(func() {
		_ = adminJS.DeleteConsumer(streamName, events.DurableConsumerName)
		if !streamPreExisted {
			_ = adminJS.DeleteStream(streamName)
		}
		if !dlqPreExisted {
			_ = adminJS.DeleteStream(events.DefaultDLQStream)
		}
		adminNC.Close()
	})

	return &testNATSEnv{
		url:           natsURL,
		streamName:    streamName,
		adminNC:       adminNC,
		adminJS:       adminJS,
		dlqMsgsBefore: dlqMsgsBefore,
		dlqSeqBefore:  dlqSeqBefore,
	}
}

func extractEnvelopeEventID(data []byte) string {
	var env events.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return ""
	}
	return env.EventID
}

func TestOrderCompletedConsumer_E1_ShutdownCanceledCtxNoDLQ(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	mem := store.NewMemoryStore()
	dlq := &spyDLQPublisher{}
	st := &hookOrderStore{
		Store: mem,
		hook: func(hCtx context.Context, _, _, _, _ string, _ int64) (bool, error) {
			cancel()
			return false, hCtx.Err()
		},
	}
	log := logger.New("promo-e1-test", logger.WithWriter(io.Discard))
	c := events.NewOrderCompletedConsumer(st, nil, "NEXUSMART", log)
	c.SetDLQPublisher(dlq)

	raw := makeOrderCompletedRaw(t, "evt-shutdown-5", "ord-shutdown-5", "buyer-1", 100)
	ack, err := c.ProcessRawMessage(ctx, raw, nil, events.MaxDeliveryAttempts)
	if ack || err == nil {
		t.Fatalf("E1: canceled ctx at MaxDeliveryAttempts must NOT Ack (ack=%v err=%v)", ack, err)
	}
	if dlq.calls != 0 {
		t.Fatalf("E1: canceled ctx during shutdown must NOT write DLQ, got %d DLQ calls", dlq.calls)
	}
}

func TestOrderCompletedConsumer_E1_AllDLQSinksFailReturnsNak(t *testing.T) {
	mem := store.NewMemoryStore()
	dlq := &spyDLQPublisher{err: nats.ErrDisconnected}
	st := &hookOrderStore{
		Store: mem,
		hook: func(_ context.Context, _, _, _, _ string, _ int64) (bool, error) {
			return false, errors.New("db unavailable")
		},
	}
	log := logger.New("promo-e1-test", logger.WithWriter(io.Discard))
	c := events.NewOrderCompletedConsumer(st, nil, "NEXUSMART", log)
	c.SetDLQPublisher(dlq)

	cases := []struct {
		name    string
		data    []byte
		attempt uint64
	}{
		{"MalformedJSON", []byte("{broken"), 1},
		{"MissingEventID", []byte(`{"event_type":"order.completed","payload":{}}`), 1},
		{"MalformedPayload", []byte(`{"event_id":"e-bad-p","payload":"not-an-object"}`), 1},
		{"MaxAttempts", makeOrderCompletedRaw(t, "e-max", "o-max", "b-1", 50), events.MaxDeliveryAttempts},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ack, err := c.ProcessRawMessage(context.Background(), tc.data, nil, tc.attempt)
			if ack || err == nil {
				t.Fatalf("E1 (%s): when DLQ fails, must NOT Ack (got ack=%v err=%v)", tc.name, ack, err)
			}
		})
	}
}

func TestOrderCompletedConsumer_E1_ExpiredHandlerDeadlineStillWritesDLQViaWithoutCancel(t *testing.T) {
	expiredCtx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	mem := store.NewMemoryStore()
	dlq := &spyDLQPublisher{}
	st := &hookOrderStore{
		Store: mem,
		hook: func(hCtx context.Context, _, _, _, _ string, _ int64) (bool, error) {
			return false, hCtx.Err() // DeadlineExceeded (NOT shutdown Canceled)
		},
	}
	log := logger.New("promo-e1-test", logger.WithWriter(io.Discard))
	c := events.NewOrderCompletedConsumer(st, nil, "NEXUSMART", log)
	c.SetDLQPublisher(dlq)

	raw := makeOrderCompletedRaw(t, "evt-deadline", "ord-deadline", "buyer-1", 100)
	ack, err := c.ProcessRawMessage(expiredCtx, raw, nil, events.MaxDeliveryAttempts)
	if !ack || err != nil {
		t.Fatalf("E1: expected Ack after DLQ persisted via detached ctx, got ack=%v err=%v", ack, err)
	}
	if dlq.calls != 1 || dlq.lastCtxErr != nil || !dlq.hasDeadline {
		t.Fatalf("E1: DLQ must use detached bounded context, got calls=%d ctxErr=%v hasDeadline=%v",
			dlq.calls, dlq.lastCtxErr, dlq.hasDeadline)
	}
}

func TestOrderCompletedConsumer_E1_NoDrainOrUnsubscribeAST(t *testing.T) {
	fset := token.NewFileSet()
	fileAST, err := parser.ParseFile(fset, "consumer.go", nil, 0)
	if err != nil {
		t.Fatalf("parse consumer.go: %v", err)
	}
	ast.Inspect(fileAST, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if ok && (sel.Sel.Name == "Drain" || sel.Sel.Name == "Unsubscribe") {
			t.Fatalf("E1: consumer.go must NEVER call .%s() on durable subscriptions", sel.Sel.Name)
		}
		return true
	})
}

func TestOrderCompletedConsumer_E1_RealNATS_UpgradeFromLegacyAndTwoRestartsNoReplayNoLoss(t *testing.T) {
	env := setupTestNATS(t, true)
	natsURL := env.url
	streamName := env.streamName
	jsAdmin := env.adminJS
	log := logger.New("promo-nats-test", logger.WithWriter(io.Discard))
	suffix := uuid.NewString()[:8]
	prefix := "evt-upg-" + suffix + "-"

	ev1ID := prefix + "1"
	ev2ID := prefix + "2"
	ev3ID := prefix + "3"
	ev4ID := prefix + "4"
	ev5ID := prefix + "5"
	ev6ID := prefix + "6"

	ev1 := makeOrderCompletedRaw(t, ev1ID, "ord-1-"+suffix, "buyer-1-"+suffix, 100)
	ev2 := makeOrderCompletedRaw(t, ev2ID, "ord-2-"+suffix, "buyer-1-"+suffix, 200)
	ev3Unacked := makeOrderCompletedRaw(t, ev3ID, "ord-3-"+suffix, "buyer-1-"+suffix, 300)
	ev4WhileDown := makeOrderCompletedRaw(t, ev4ID, "ord-4-"+suffix, "buyer-1-"+suffix, 400)
	ev5AfterRestart1 := makeOrderCompletedRaw(t, ev5ID, "ord-5-"+suffix, "buyer-1-"+suffix, 500)
	ev6AfterRestart2 := makeOrderCompletedRaw(t, ev6ID, "ord-6-"+suffix, "buyer-1-"+suffix, 600)

	// 1. Seed events + create LEGACY non-queue durable consumer (Subscribe, DeliverGroup="", MaxDeliver=5).
	ncLegacy, err := nats.Connect(natsURL)
	if err != nil {
		t.Fatalf("connect legacy nats: %v", err)
	}
	jsLegacy, err := ncLegacy.JetStream()
	if err != nil {
		ncLegacy.Close()
		t.Fatalf("jetstream legacy: %v", err)
	}

	if _, err := jsLegacy.Publish(events.SubjectOrderCompleted, ev1); err != nil {
		ncLegacy.Close()
		t.Fatal(err)
	}
	if _, err := jsLegacy.Publish("payment.succeeded", []byte(`{"unrelated":true}`)); err != nil {
		ncLegacy.Close()
		t.Fatal(err)
	}
	if _, err := jsLegacy.Publish(events.SubjectOrderCompleted, ev2); err != nil {
		ncLegacy.Close()
		t.Fatal(err)
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

	acked := map[string]bool{}
	deadlineLegacy := time.After(4 * time.Second)
	for !(acked[ev1ID] && acked[ev2ID]) {
		select {
		case m := <-legacyCh:
			if err := m.AckSync(); err != nil {
				ncLegacy.Close()
				t.Fatalf("legacy ack: %v", err)
			}
			acked[extractEnvelopeEventID(m.Data)] = true
		case <-deadlineLegacy:
			ncLegacy.Close()
			t.Fatalf("timeout waiting for legacy ev1/ev2 ack (got=%v)", acked)
		}
	}

	// Publish ev3: legacy consumer receives it but crashes BEFORE Ack!
	if _, err := jsLegacy.Publish(events.SubjectOrderCompleted, ev3Unacked); err != nil {
		ncLegacy.Close()
		t.Fatal(err)
	}
	deadlineEv3 := time.After(4 * time.Second)
	for {
		select {
		case m := <-legacyCh:
			if extractEnvelopeEventID(m.Data) == ev3ID {
				// Intentionally do NOT Ack ev3 to prove upgrade does not lose in-flight unacked messages.
				goto legacyCrash
			}
			_ = m.AckSync()
		case <-deadlineEv3:
			ncLegacy.Close()
			t.Fatal("timeout waiting for legacy ev3")
		}
	}
legacyCrash:
	ncLegacy.Close()

	// Publish ev4 while promotion service is down between legacy stop and upgraded start.
	if _, err := jsAdmin.Publish(events.SubjectOrderCompleted, ev4WhileDown); err != nil {
		t.Fatal(err)
	}

	// Count EVERY handler invocation for our test events (including duplicates if stream were replayed!).
	var mu sync.Mutex
	counts := make(map[string]int)
	var totalHandled atomic.Int32
	mem := store.NewMemoryStore()
	countingStore := &hookOrderStore{
		Store: mem,
		hook: func(ctx context.Context, eventID, subject, orderID, buyerID string, coinsReward int64) (bool, error) {
			if strings.HasPrefix(eventID, prefix) {
				mu.Lock()
				counts[eventID]++
				mu.Unlock()
				totalHandled.Add(1)
			}
			return mem.ProcessOrderCompletedEvent(ctx, eventID, subject, orderID, buyerID, coinsReward)
		},
	}

	waitForTotal := func(want int32, label string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if totalHandled.Load() >= want {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("%s: timeout waiting for totalHandled>=%d, got %d (counts=%v)", label, want, totalHandled.Load(), counts)
	}

	startInstance := func(label string) (*nats.Conn, *events.OrderCompletedConsumer, context.CancelFunc) {
		t.Helper()
		nc, err := nats.Connect(natsURL)
		if err != nil {
			t.Fatalf("%s connect: %v", label, err)
		}
		js, err := nc.JetStream()
		if err != nil {
			nc.Close()
			t.Fatalf("%s jetstream: %v", label, err)
		}
		c := events.NewOrderCompletedConsumer(countingStore, js, streamName, log)
		ctx, cancel := context.WithCancel(context.Background())
		if err := c.Start(ctx); err != nil {
			cancel()
			nc.Close()
			t.Fatalf("%s Start: %v", label, err)
		}
		return nc, c, cancel
	}

	// 2. Upgraded boot #1: must upgrade legacy consumer to QueueSubscribe (same durable),
	// deliver ev3 (unacked) + ev4 (published while down), and NOT replay ev1 or ev2!
	nc1, c1, cancel1 := startInstance("upgrade-boot-1")
	waitForTotal(2, "upgrade-boot-1")
	time.Sleep(150 * time.Millisecond)

	info1, err := jsAdmin.ConsumerInfo(streamName, events.DurableConsumerName)
	if err != nil {
		t.Fatalf("ConsumerInfo after upgrade: %v", err)
	}
	if info1.Config.DeliverGroup == "" {
		t.Fatalf("E1: upgraded durable consumer %q must have non-empty DeliverGroup (QueueSubscribe)", events.DurableConsumerName)
	}
	if info1.Config.MaxDeliver <= events.MaxDeliveryAttempts {
		t.Fatalf("E1: MaxDeliver (%d) must be > MaxDeliveryAttempts (%d) for DLQ retry headroom",
			info1.Config.MaxDeliver, events.MaxDeliveryAttempts)
	}

	// Stop #1 cleanly (cancel + Stop + nc.Close) and verify durable STILL EXISTS on NATS!
	cancel1()
	if err := c1.Stop(); err != nil {
		t.Fatalf("c1.Stop: %v", err)
	}
	nc1.Close()

	if _, err := jsAdmin.ConsumerInfo(streamName, events.DurableConsumerName); err != nil {
		t.Fatalf("E1: durable consumer %q MUST survive Stop()+nc.Close() (no Drain/Unsubscribe): %v",
			events.DurableConsumerName, err)
	}

	// 3. Restart #1: publish ev5, start new consumer, verify ONLY ev5 is processed (0 replays).
	if _, err := jsAdmin.Publish(events.SubjectOrderCompleted, ev5AfterRestart1); err != nil {
		t.Fatal(err)
	}
	nc2, c2, cancel2 := startInstance("restart-1")
	waitForTotal(3, "restart-1")
	time.Sleep(150 * time.Millisecond)
	cancel2()
	if err := c2.Stop(); err != nil {
		t.Fatalf("c2.Stop: %v", err)
	}
	nc2.Close()

	// 4. Restart #2: publish ev6, start new consumer, verify ONLY ev6 is processed (0 replays).
	if _, err := jsAdmin.Publish(events.SubjectOrderCompleted, ev6AfterRestart2); err != nil {
		t.Fatal(err)
	}
	nc3, c3, cancel3 := startInstance("restart-2")
	waitForTotal(4, "restart-2")
	time.Sleep(150 * time.Millisecond)
	cancel3()
	if err := c3.Stop(); err != nil {
		t.Fatalf("c3.Stop: %v", err)
	}
	nc3.Close()

	mu.Lock()
	defer mu.Unlock()
	if counts[ev1ID] != 0 || counts[ev2ID] != 0 {
		t.Fatalf("E1: upgraded consumer MUST NOT replay already-acked legacy events ev1/ev2, got counts=%v", counts)
	}
	for _, wantID := range []string{ev3ID, ev4ID, ev5ID, ev6ID} {
		if counts[wantID] != 1 {
			t.Fatalf("E1: expected %s processed exactly 1 time across upgrade + 2 restarts, got counts=%v", wantID, counts)
		}
	}
	if totalHandled.Load() != 4 {
		t.Fatalf("E1: expected exactly 4 handler calls total (ev3..ev6), got %d (counts=%v)", totalHandled.Load(), counts)
	}
}

func TestOrderCompletedConsumer_E1_RealNATS_LegacyUpgradeAtZeroAckFloor(t *testing.T) {
	env := setupTestNATS(t, true)
	natsURL := env.url
	streamName := env.streamName
	jsAdmin := env.adminJS
	log := logger.New("promo-zero-floor-test", logger.WithWriter(io.Discard))
	suffix := uuid.NewString()[:8]
	evZeroID := "evt-zero-" + suffix
	buyerZero := "buyer-zero-" + suffix

	// Create legacy consumer that NEVER acked any message (AckFloor.Stream == 0) with 1 pending event.
	if _, err := jsAdmin.AddConsumer(streamName, &nats.ConsumerConfig{
		Durable:        events.DurableConsumerName,
		FilterSubject:  events.SubjectOrderCompleted,
		DeliverSubject: nats.NewInbox(),
		DeliverPolicy:  nats.DeliverAllPolicy,
		AckPolicy:      nats.AckExplicitPolicy,
		AckWait:        30 * time.Second,
		MaxDeliver:     events.MaxDeliveryAttempts,
	}); err != nil {
		t.Fatalf("add legacy consumer: %v", err)
	}
	if _, err := jsAdmin.Publish(events.SubjectOrderCompleted, makeOrderCompletedRaw(t, evZeroID, "ord-zero-"+suffix, buyerZero, 120)); err != nil {
		t.Fatal(err)
	}

	nc, err := nats.Connect(natsURL)
	if err != nil {
		t.Fatalf("connect nats: %v", err)
	}
	defer nc.Close()
	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}

	mem := store.NewMemoryStore()
	var handled atomic.Int32
	st := &hookOrderStore{
		Store: mem,
		hook: func(ctx context.Context, eventID, subject, orderID, buyerID string, coins int64) (bool, error) {
			if eventID == evZeroID {
				handled.Add(1)
			}
			return mem.ProcessOrderCompletedEvent(ctx, eventID, subject, orderID, buyerID, coins)
		},
	}

	c := events.NewOrderCompletedConsumer(st, js, streamName, log)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = c.Stop() }()

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) && handled.Load() < 1 {
		time.Sleep(20 * time.Millisecond)
	}
	if handled.Load() != 1 {
		t.Fatalf("E1: legacy upgrade at AckFloor=0 must deliver pending event, got handled=%d", handled.Load())
	}
	bal, _, err := mem.GetCoinBalance(context.Background(), buyerZero, 10)
	if err != nil || bal != 120 {
		t.Fatalf("expected %s balance 120, got bal=%d err=%v", buyerZero, bal, err)
	}
	info, err := js.ConsumerInfo(streamName, events.DurableConsumerName)
	if err != nil {
		t.Fatalf("ConsumerInfo: %v", err)
	}
	if info.Config.DeliverGroup != events.QueueGroupName || info.Config.MaxDeliver != events.JetStreamMaxDeliver {
		t.Fatalf("unexpected upgraded consumer config: %+v", info.Config)
	}
}

func TestOrderCompletedConsumer_E1_RealNATS_TwoReplicasQueueLoadShareNoDuplicates(t *testing.T) {
	env := setupTestNATS(t, false)
	natsURL := env.url
	streamName := env.streamName
	log := logger.New("promo-replicas-test", logger.WithWriter(io.Discard))
	suffix := uuid.NewString()[:8]
	prefix := "evt-q-" + suffix + "-"
	buyerQ := "buyer-q-" + suffix

	mem := store.NewMemoryStore()
	var mu sync.Mutex
	seen := make(map[string]int)
	var total atomic.Int32
	st := &hookOrderStore{
		Store: mem,
		hook: func(ctx context.Context, eventID, subject, orderID, buyerID string, coins int64) (bool, error) {
			if strings.HasPrefix(eventID, prefix) {
				mu.Lock()
				seen[eventID]++
				mu.Unlock()
				total.Add(1)
			}
			return mem.ProcessOrderCompletedEvent(ctx, eventID, subject, orderID, buyerID, coins)
		},
	}

	// Start 2 replicas simultaneously.
	barrier := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		nc, err := nats.Connect(natsURL)
		if err != nil {
			t.Fatalf("replica %d connect: %v", i, err)
		}
		t.Cleanup(nc.Close)
		js, err := nc.JetStream()
		if err != nil {
			t.Fatalf("replica %d jetstream: %v", i, err)
		}
		c := events.NewOrderCompletedConsumer(st, js, streamName, log)
		rCtx, rCancel := context.WithCancel(context.Background())
		t.Cleanup(func() {
			rCancel()
			_ = c.Stop()
		})
		idx := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-barrier
			errs[idx] = c.Start(rCtx)
		}()
	}
	close(barrier)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("replica %d Start failed: %v", i, err)
		}
	}

	const nEvents = 20
	for i := 0; i < nEvents; i++ {
		evID := fmt.Sprintf("%s%02d", prefix, i)
		ordID := fmt.Sprintf("ord-q-%s-%02d", suffix, i)
		if _, err := env.adminJS.Publish(events.SubjectOrderCompleted, makeOrderCompletedRaw(t, evID, ordID, buyerQ, 100)); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && total.Load() < nEvents {
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(150 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if total.Load() != nEvents || len(seen) != nEvents {
		t.Fatalf("E1: expected %d unique single deliveries across 2 queue replicas, got total=%d unique=%d", nEvents, total.Load(), len(seen))
	}
	for id, cnt := range seen {
		if cnt != 1 {
			t.Fatalf("E1: duplicate delivery for %s across queue group: count=%d", id, cnt)
		}
	}
	bal, _, err := mem.GetCoinBalance(context.Background(), buyerQ, 30)
	if err != nil || bal != nEvents*100 {
		t.Fatalf("expected %s balance %d, got bal=%d err=%v", buyerQ, nEvents*100, bal, err)
	}
}

func TestOrderCompletedConsumer_E1_RealNATS_ExistingQueueConsumerUpdatesMaxDeliverInPlace(t *testing.T) {
	env := setupTestNATS(t, true)
	natsURL := env.url
	streamName := env.streamName
	log := logger.New("promo-inplace-maxdeliver", logger.WithWriter(io.Discard))
	suffix := uuid.NewString()[:8]
	evOldID := "evt-old-" + suffix
	evNewID := "evt-new-" + suffix
	buyerU := "buyer-u-" + suffix

	ncSetup, err := nats.Connect(natsURL)
	if err != nil {
		t.Fatal(err)
	}
	jsSetup, err := ncSetup.JetStream()
	if err != nil {
		ncSetup.Close()
		t.Fatal(err)
	}
	// Pre-create queue consumer with old MaxDeliver = 5 and ack 1 event.
	gotOld := make(chan *nats.Msg, 16)
	if _, err := jsSetup.QueueSubscribe(
		events.SubjectOrderCompleted,
		events.QueueGroupName,
		func(m *nats.Msg) { gotOld <- m },
		nats.BindStream(streamName),
		nats.Durable(events.DurableConsumerName),
		nats.ManualAck(),
		nats.AckExplicit(),
		nats.MaxDeliver(events.MaxDeliveryAttempts),
		nats.AckWait(30*time.Second),
	); err != nil {
		ncSetup.Close()
		t.Fatalf("setup queue sub: %v", err)
	}
	if _, err := jsSetup.Publish(events.SubjectOrderCompleted, makeOrderCompletedRaw(t, evOldID, "ord-old-"+suffix, buyerU, 50)); err != nil {
		ncSetup.Close()
		t.Fatal(err)
	}
	deadlineOld := time.After(4 * time.Second)
	for {
		select {
		case m := <-gotOld:
			if err := m.AckSync(); err != nil {
				ncSetup.Close()
				t.Fatal(err)
			}
			if extractEnvelopeEventID(m.Data) == evOldID {
				goto doneOld
			}
		case <-deadlineOld:
			ncSetup.Close()
			t.Fatal("timeout waiting for evt-old")
		}
	}
doneOld:
	ncSetup.Close()

	// Start new consumer -> must update MaxDeliver 5->6 in-place, 0 replays of evOldID, deliver evNewID.
	nc, err := nats.Connect(natsURL)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, err := nc.JetStream()
	if err != nil {
		t.Fatal(err)
	}

	mem := store.NewMemoryStore()
	var mu sync.Mutex
	seen := make(map[string]int)
	var total atomic.Int32
	st := &hookOrderStore{
		Store: mem,
		hook: func(ctx context.Context, eventID, subject, orderID, buyerID string, coins int64) (bool, error) {
			if eventID == evOldID || eventID == evNewID {
				mu.Lock()
				seen[eventID]++
				mu.Unlock()
				total.Add(1)
			}
			return mem.ProcessOrderCompletedEvent(ctx, eventID, subject, orderID, buyerID, coins)
		},
	}

	c := events.NewOrderCompletedConsumer(st, js, streamName, log)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = c.Stop() }()

	if _, err := js.Publish(events.SubjectOrderCompleted, makeOrderCompletedRaw(t, evNewID, "ord-new-"+suffix, buyerU, 75)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) && total.Load() < 1 {
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(150 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if seen[evOldID] != 0 || seen[evNewID] != 1 || total.Load() != 1 {
		t.Fatalf("E1: expected only %s processed once (0 replay of %s), got seen=%v", evNewID, evOldID, seen)
	}
	info, err := js.ConsumerInfo(streamName, events.DurableConsumerName)
	if err != nil {
		t.Fatal(err)
	}
	if info.Config.MaxDeliver != events.JetStreamMaxDeliver || !info.PushBound {
		t.Fatalf("E1: expected in-place MaxDeliver=%d and PushBound=true, got MaxDeliver=%d PushBound=%v",
			events.JetStreamMaxDeliver, info.Config.MaxDeliver, info.PushBound)
	}
}
