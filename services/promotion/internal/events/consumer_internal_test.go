package events

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/nexusmart/core/services/promotion/internal/store"
)

type stubInternalStore struct {
	store.Store
	mu    sync.Mutex
	fn    func(ctx context.Context, eventID, subject, orderID, buyerID string, coinsReward int64) (bool, error)
	calls atomic.Int32
}

func (s *stubInternalStore) ProcessOrderCompletedEvent(ctx context.Context, eventID, subject, orderID, buyerID string, coinsReward int64) (bool, error) {
	s.calls.Add(1)
	s.mu.Lock()
	fn := s.fn
	s.mu.Unlock()
	if fn != nil {
		return fn(ctx, eventID, subject, orderID, buyerID, coinsReward)
	}
	return s.Store.ProcessOrderCompletedEvent(ctx, eventID, subject, orderID, buyerID, coinsReward)
}

type stubDLQDBStore struct {
	mu          sync.Mutex
	err         error
	fn          func(ctx context.Context, rec DeadLetterRecord) error
	records     []DeadLetterRecord
	lastCtxErr  error
	hasDeadline bool
}

func (s *stubDLQDBStore) InsertDeadLetterEvent(ctx context.Context, rec DeadLetterRecord) error {
	s.mu.Lock()
	s.lastCtxErr = ctx.Err()
	_, s.hasDeadline = ctx.Deadline()
	fn := s.fn
	err := s.err
	s.mu.Unlock()

	if fn != nil {
		if fErr := fn(ctx, rec); fErr != nil {
			return fErr
		}
	} else if err != nil {
		return err
	}
	s.mu.Lock()
	s.records = append(s.records, rec)
	s.mu.Unlock()
	return nil
}

type stubDLQPub struct {
	mu          sync.Mutex
	err         error
	fn          func(ctx context.Context, subject string, payload []byte, headers map[string]string) error
	calls       int
	subjects    []string
	headers     []map[string]string
	lastCtxErr  error
	hasDeadline bool
}

func (s *stubDLQPub) PublishDLQ(ctx context.Context, subject string, payload []byte, headers map[string]string) error {
	s.mu.Lock()
	s.calls++
	s.lastCtxErr = ctx.Err()
	_, s.hasDeadline = ctx.Deadline()
	fn := s.fn
	err := s.err
	s.mu.Unlock()

	if fn != nil {
		if fErr := fn(ctx, subject, payload, headers); fErr != nil {
			return fErr
		}
	} else if err != nil {
		return err
	}

	hdrCopy := make(map[string]string, len(headers))
	for k, v := range headers {
		hdrCopy[k] = v
	}
	s.mu.Lock()
	s.subjects = append(s.subjects, subject)
	s.headers = append(s.headers, hdrCopy)
	s.mu.Unlock()
	return nil
}

type stubPubAckJS struct {
	nats.JetStreamContext
	mu        sync.Mutex
	sawPubOpt bool
	lastMsg   *nats.Msg
	ack       *nats.PubAck
	err       error
}

func (s *stubPubAckJS) PublishMsg(m *nats.Msg, opts ...nats.PubOpt) (*nats.PubAck, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastMsg = m
	s.sawPubOpt = len(opts) > 0
	return s.ack, s.err
}

func makeInternalOrderCompletedBytes(t *testing.T, eventID, orderID, buyerID string, coins int64) []byte {
	t.Helper()
	payload, err := json.Marshal(OrderCompletedPayload{
		OrderID:     orderID,
		OrderCode:   "NM-" + orderID,
		BuyerID:     buyerID,
		ShopID:      "shop-1",
		CoinsReward: coins,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	raw, err := json.Marshal(Envelope{
		EventID:    eventID,
		EventType:  SubjectOrderCompleted,
		OccurredAt: "2026-11-24T10:00:00Z",
		Payload:    payload,
	})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return raw
}

func requireInternalNATSURL(t *testing.T) string {
	t.Helper()
	url := strings.TrimSpace(os.Getenv("TEST_NATS_URL"))
	if url == "" {
		t.Skip("TEST_NATS_URL not set; run via scripts/test-db.sh")
	}
	return url
}

type internalTestNATSEnv struct {
	url           string
	streamName    string
	dlqMsgsBefore uint64
	dlqSeqBefore  uint64
}

func setupInternalTestNATS(t *testing.T) *internalTestNATSEnv {
	t.Helper()
	natsURL := requireInternalNATSURL(t)

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
	if dlqInfo, dlqErr := adminJS.StreamInfo(DefaultDLQStream); dlqErr == nil && dlqInfo != nil {
		dlqPreExisted = true
		dlqMsgsBefore = dlqInfo.State.Msgs
		dlqSeqBefore = dlqInfo.State.LastSeq
	}

	streamPreExisted := false
	streamName, findErr := adminJS.StreamNameBySubject(SubjectOrderCompleted)
	if findErr == nil && streamName != "" {
		streamPreExisted = true
		_ = adminJS.DeleteConsumer(streamName, DurableConsumerName)
	} else {
		streamName = fmt.Sprintf("NEXUSMART_PROMO_INT_%s", uuid.NewString()[:8])
	}

	t.Cleanup(func() {
		_ = adminJS.DeleteConsumer(streamName, DurableConsumerName)
		if !streamPreExisted {
			_ = adminJS.DeleteStream(streamName)
		}
		if !dlqPreExisted {
			_ = adminJS.DeleteStream(DefaultDLQStream)
		}
		adminNC.Close()
	})

	return &internalTestNATSEnv{
		url:           natsURL,
		streamName:    streamName,
		dlqMsgsBefore: dlqMsgsBefore,
		dlqSeqBefore:  dlqSeqBefore,
	}
}

func TestOrderCompletedConsumer_E1_StopWaitsInFlightAndRejectsAfterStop(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var logBuf bytes.Buffer
	log := logger.New("promo-int-test", logger.WithWriter(&logBuf))

	st := &stubInternalStore{
		Store: store.NewMemoryStore(),
		fn: func(_ context.Context, _, _, _, _ string, _ int64) (bool, error) {
			started <- struct{}{}
			<-release
			return false, nil
		},
	}
	c := NewOrderCompletedConsumer(st, nil, "NEXUSMART", log)
	raw := makeInternalOrderCompletedBytes(t, "evt-inflight", "ord-inflight", "buyer-1", 100)

	go c.onMessage(context.Background(), &nats.Msg{Subject: SubjectOrderCompleted, Data: raw})
	<-started

	stopDone := make(chan error, 1)
	go func() {
		stopDone <- c.Stop()
	}()

	select {
	case err := <-stopDone:
		close(release)
		t.Fatalf("E1: Stop() returned early while handler was still in-flight (err=%v)", err)
	case <-time.After(60 * time.Millisecond):
	}

	close(release)
	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("Stop() returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() did not return after in-flight handler finished")
	}

	// Second Stop() must be idempotent.
	if err := c.Stop(); err != nil {
		t.Fatalf("second Stop() returned error: %v", err)
	}

	// Delivery while stopping must skip handler quietly without Ack/Nak.
	logBuf.Reset()
	c.onMessage(context.Background(), &nats.Msg{Subject: SubjectOrderCompleted, Data: raw})
	if got := st.calls.Load(); got != 1 {
		t.Fatalf("E1: stopping consumer must not invoke handler, got %d calls", got)
	}
	if strings.TrimSpace(logBuf.String()) != "" {
		t.Fatalf("E1: stopping consumer must return quietly without Ack/Nak, got logs: %s", logBuf.String())
	}

	// Delivery with pre-canceled ctx must also skip handler quietly.
	st2 := &stubInternalStore{Store: store.NewMemoryStore()}
	c2 := NewOrderCompletedConsumer(st2, nil, "NEXUSMART", log)
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	logBuf.Reset()
	c2.onMessage(canceledCtx, &nats.Msg{Subject: SubjectOrderCompleted, Data: raw})
	if got := st2.calls.Load(); got != 0 {
		t.Fatalf("E1: pre-canceled ctx must skip handler, got %d calls", got)
	}
	if strings.TrimSpace(logBuf.String()) != "" {
		t.Fatalf("E1: pre-canceled ctx must return quietly without Ack/Nak, got logs: %s", logBuf.String())
	}
}

func TestOrderCompletedConsumer_E1_HandleNATSMsg_ShutdownAndDLQActions(t *testing.T) {
	t.Run("ShutdownDuringFailingHandler_NeitherAcksNorNaksNorDLQs", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		var logBuf bytes.Buffer
		log := logger.New("promo-int-test", logger.WithWriter(&logBuf))
		dlqDB := &stubDLQDBStore{}
		dlqPub := &stubDLQPub{}
		st := &stubInternalStore{
			Store: store.NewMemoryStore(),
			fn: func(hCtx context.Context, _, _, _, _ string, _ int64) (bool, error) {
				cancel()
				return false, hCtx.Err()
			},
		}
		c := NewOrderCompletedConsumer(st, nil, "NEXUSMART", log)
		c.SetDeadLetterStore(dlqDB)
		c.SetDLQPublisher(dlqPub)

		raw := makeInternalOrderCompletedBytes(t, "evt-sh-fail", "ord-sh-fail", "buyer-1", 100)
		c.handleNATSMsg(ctx, &nats.Msg{Subject: SubjectOrderCompleted, Data: raw})

		if len(dlqDB.records) != 0 || dlqPub.calls != 0 {
			t.Fatalf("E1: shutdown error must not write DLQ (db=%d nats=%d)", len(dlqDB.records), dlqPub.calls)
		}
		if strings.Contains(logBuf.String(), "failed to ack") || strings.Contains(logBuf.String(), "failed to nak") {
			t.Fatalf("E1: shutdown failure must neither Ack nor Nak, logs=%s", logBuf.String())
		}
	})

	t.Run("StopDuringSucceededHandler_AcksCommittedWork", func(t *testing.T) {
		var logBuf bytes.Buffer
		log := logger.New("promo-int-test", logger.WithWriter(&logBuf))
		mem := store.NewMemoryStore()
		var c *OrderCompletedConsumer
		st := &stubInternalStore{
			Store: mem,
			fn: func(hCtx context.Context, eventID, subject, orderID, buyerID string, coins int64) (bool, error) {
				already, err := mem.ProcessOrderCompletedEvent(hCtx, eventID, subject, orderID, buyerID, coins)
				c.mu.Lock()
				c.stopping = true
				c.mu.Unlock()
				return already, err
			},
		}
		c = NewOrderCompletedConsumer(st, nil, "NEXUSMART", log)

		raw := makeInternalOrderCompletedBytes(t, "evt-sh-ok", "ord-sh-ok", "buyer-1", 100)
		c.handleNATSMsg(context.Background(), &nats.Msg{Subject: SubjectOrderCompleted, Data: raw})

		if !strings.Contains(logBuf.String(), "failed to ack") {
			t.Fatalf("expected committed handler to attempt Ack even if Stop toggled after commit, logs=%s", logBuf.String())
		}
		if strings.Contains(logBuf.String(), "failed to nak") {
			t.Fatalf("committed handler must not Nak, logs=%s", logBuf.String())
		}
	})

	t.Run("BothDLQSinksFail_HandleNATSMsgAttemptsNakNeverAck", func(t *testing.T) {
		var logBuf bytes.Buffer
		log := logger.New("promo-int-test", logger.WithWriter(&logBuf))
		dlqDB := &stubDLQDBStore{err: errors.New("pg down")}
		dlqPub := &stubDLQPub{err: nats.ErrDisconnected}
		c := NewOrderCompletedConsumer(store.NewMemoryStore(), nil, "NEXUSMART", log)
		c.SetDeadLetterStore(dlqDB)
		c.SetDLQPublisher(dlqPub)

		c.handleNATSMsg(context.Background(), &nats.Msg{Subject: SubjectOrderCompleted, Data: []byte("{bad-json")})
		if strings.Contains(logBuf.String(), "failed to ack") {
			t.Fatalf("E1: must NEVER attempt Ack when both DLQ sinks fail, logs=%s", logBuf.String())
		}
		if !strings.Contains(logBuf.String(), "failed to nak") {
			t.Fatalf("E1: must attempt NakWithDelay when both DLQ sinks fail, logs=%s", logBuf.String())
		}
	})
}

func TestOrderCompletedConsumer_E1_SingleSinkFallbackAndPubAck(t *testing.T) {
	t.Run("DBSinkSucceeds_NATSFails_ReturnsActionDLQ", func(t *testing.T) {
		expiredCtx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()

		dlqDB := &stubDLQDBStore{}
		dlqPub := &stubDLQPub{err: nats.ErrTimeout}
		st := &stubInternalStore{
			Store: store.NewMemoryStore(),
			fn: func(hCtx context.Context, _, _, _, _ string, _ int64) (bool, error) {
				return false, hCtx.Err()
			},
		}
		c := NewOrderCompletedConsumer(st, nil, "NEXUSMART", logger.New("promo-int-test", logger.WithWriter(io.Discard)))
		c.SetDeadLetterStore(dlqDB)
		c.SetDLQPublisher(dlqPub)

		raw := makeInternalOrderCompletedBytes(t, "evt-db-ok", "ord-db-ok", "buyer-1", 50)
		action, err := c.processMessage(expiredCtx, raw, nil, MaxDeliveryAttempts)
		if err == nil || action != ActionDLQ {
			t.Fatalf("E1: expected ActionDLQ when DB DLQ succeeds, got action=%v err=%v", action, err)
		}
		if len(dlqDB.records) != 1 || dlqDB.lastCtxErr != nil || !dlqDB.hasDeadline {
			t.Fatalf("E1: DB DLQ must use detached bounded context, got records=%d ctxErr=%v hasDeadline=%v",
				len(dlqDB.records), dlqDB.lastCtxErr, dlqDB.hasDeadline)
		}
		rec := dlqDB.records[0]
		if rec.Subject != SubjectOrderCompleted || rec.EventID != "evt-db-ok" || rec.Attempts != MaxDeliveryAttempts {
			t.Fatalf("unexpected DB DLQ record: %+v", rec)
		}
	})

	t.Run("NATSSinkSucceeds_DBFails_ReturnsActionDLQ", func(t *testing.T) {
		expiredCtx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()

		dlqDB := &stubDLQDBStore{err: errors.New("pg down")}
		dlqPub := &stubDLQPub{}
		st := &stubInternalStore{
			Store: store.NewMemoryStore(),
			fn: func(hCtx context.Context, _, _, _, _ string, _ int64) (bool, error) {
				return false, hCtx.Err()
			},
		}
		c := NewOrderCompletedConsumer(st, nil, "NEXUSMART", logger.New("promo-int-test", logger.WithWriter(io.Discard)))
		c.SetDeadLetterStore(dlqDB)
		c.SetDLQPublisher(dlqPub)

		raw := makeInternalOrderCompletedBytes(t, "evt-nats-ok", "ord-nats-ok", "buyer-1", 50)
		action, err := c.processMessage(expiredCtx, raw, nil, MaxDeliveryAttempts)
		if err == nil || action != ActionDLQ {
			t.Fatalf("E1: expected ActionDLQ when NATS DLQ PubAck succeeds, got action=%v err=%v", action, err)
		}
		if len(dlqPub.subjects) != 1 || dlqPub.subjects[0] != "dlq."+SubjectOrderCompleted {
			t.Fatalf("unexpected NATS DLQ subjects: %v", dlqPub.subjects)
		}
		if dlqPub.lastCtxErr != nil || !dlqPub.hasDeadline {
			t.Fatalf("E1: NATS DLQ must use detached bounded ctx, got ctxErr=%v hasDeadline=%v", dlqPub.lastCtxErr, dlqPub.hasDeadline)
		}
		hdr := dlqPub.headers[0]
		if hdr["X-DLQ-Event-Id"] != "evt-nats-ok" || hdr["X-DLQ-Original-Subject"] != SubjectOrderCompleted || hdr["X-DLQ-Attempts"] != "5" {
			t.Fatalf("unexpected canonical DLQ headers: %v", hdr)
		}
	})

	t.Run("NoDLQSinksConfigured_ReturnsActionNak", func(t *testing.T) {
		c := NewOrderCompletedConsumer(store.NewMemoryStore(), nil, "NEXUSMART", logger.New("promo-int-test", logger.WithWriter(io.Discard)))
		action, err := c.processMessage(context.Background(), []byte("{broken"), nil, 1)
		if err == nil || action != ActionNak {
			t.Fatalf("E1: with no DLQ sink configured, poison message must ActionNak (never Ack), got action=%v err=%v", action, err)
		}
	})

	t.Run("JetStreamDLQ_PassesContextAndRequiresNonNilPubAck", func(t *testing.T) {
		js := &stubPubAckJS{ack: &nats.PubAck{Stream: DefaultDLQStream, Sequence: 1}}
		pub := &jetStreamDLQ{js: js}
		if err := pub.PublishDLQ(context.Background(), "dlq.order.completed", []byte("x"), map[string]string{"X-DLQ-Event-Id": "e1"}); err != nil {
			t.Fatalf("expected success with non-nil PubAck, got %v", err)
		}
		if !js.sawPubOpt || js.lastMsg == nil || js.lastMsg.Header.Get("X-DLQ-Event-Id") != "e1" {
			t.Fatalf("expected PublishMsg with nats.Context PubOpt and headers, sawPubOpt=%v msg=%v", js.sawPubOpt, js.lastMsg)
		}

		// Nil PubAck must be rejected.
		jsNilAck := &stubPubAckJS{ack: nil, err: nil}
		pubNil := &jetStreamDLQ{js: jsNilAck}
		if err := pubNil.PublishDLQ(context.Background(), "dlq.order.completed", []byte("x"), nil); err == nil {
			t.Fatal("expected error when PublishMsg returns nil PubAck")
		}

		// PublishMsg error must propagate.
		jsErr := &stubPubAckJS{err: nats.ErrTimeout}
		pubErr := &jetStreamDLQ{js: jsErr}
		if err := pubErr.PublishDLQ(context.Background(), "dlq.order.completed", []byte("x"), nil); !errors.Is(err, nats.ErrTimeout) {
			t.Fatalf("expected nats.ErrTimeout, got %v", err)
		}
	})
}

func TestOrderCompletedConsumer_E1_RealNATS_MaxDeliverHeadroomAllowsDLQRetry(t *testing.T) {
	env := setupInternalTestNATS(t)
	natsURL := env.url
	streamName := env.streamName
	log := logger.New("promo-headroom-test", logger.WithWriter(io.Discard))
	suffix := uuid.NewString()[:8]
	targetEvtID := "evt-headroom-6-" + suffix

	nc, err := nats.Connect(natsURL)
	if err != nil {
		t.Fatalf("connect nats: %v", err)
	}
	defer nc.Close()
	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}

	var attempts atomic.Int32
	st := &stubInternalStore{
		Store: store.NewMemoryStore(),
		fn: func(_ context.Context, eventID, _, _, _ string, _ int64) (bool, error) {
			if eventID != targetEvtID {
				return false, nil
			}
			attempts.Add(1)
			return false, errors.New("persistent handler failure")
		},
	}

	// Both DB DLQ and NATS DLQ fail on attempt 5 (causing Nak!), then succeed on attempt 6 (proving MaxDeliver > MaxDeliveryAttempts).
	realNatsDLQ := &jetStreamDLQ{js: js}
	dlqDB := &stubDLQDBStore{
		fn: func(_ context.Context, rec DeadLetterRecord) error {
			if rec.Attempts <= MaxDeliveryAttempts {
				return errors.New("transient pg dlq outage on attempt 5")
			}
			return nil
		},
	}
	dlqPub := &stubDLQPub{
		fn: func(ctx context.Context, subject string, payload []byte, headers map[string]string) error {
			if headers["X-DLQ-Attempts"] == "5" {
				return nats.ErrTimeout
			}
			return realNatsDLQ.PublishDLQ(ctx, subject, payload, headers)
		},
	}

	c := NewOrderCompletedConsumer(st, js, streamName, log)
	c.nakBaseDelay = 20 * time.Millisecond
	c.SetDeadLetterStore(dlqDB)
	c.SetDLQPublisher(dlqPub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = c.Stop() }()

	raw := makeInternalOrderCompletedBytes(t, targetEvtID, "ord-headroom-6-"+suffix, "buyer-1", 100)
	if _, err := js.Publish(SubjectOrderCompleted, raw); err != nil {
		t.Fatalf("publish: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		dlqDB.mu.Lock()
		n := len(dlqDB.records)
		dlqDB.mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}

	dlqDB.mu.Lock()
	dbRecs := append([]DeadLetterRecord(nil), dlqDB.records...)
	dlqDB.mu.Unlock()
	if len(dbRecs) != 1 || dbRecs[0].Attempts != JetStreamMaxDeliver || dbRecs[0].EventID != targetEvtID {
		t.Fatalf("E1: expected DLQ persisted on attempt %d (headroom after attempt %d failed), got records=%+v totalHandlerAttempts=%d",
			JetStreamMaxDeliver, MaxDeliveryAttempts, dbRecs, attempts.Load())
	}

	// Verify NEXUSMART_DLQ stream also received our message (PubAck succeeded on attempt 6).
	dlqInfo, err := js.StreamInfo(DefaultDLQStream)
	if err != nil {
		t.Fatalf("StreamInfo(%s): %v", DefaultDLQStream, err)
	}
	if delta := dlqInfo.State.Msgs - env.dlqMsgsBefore; delta != 1 {
		t.Fatalf("E1: expected 1 new message in %s after attempt-6 DLQ retry, got delta=%d (total=%d before=%d)",
			DefaultDLQStream, delta, dlqInfo.State.Msgs, env.dlqMsgsBefore)
	}
	foundDLQ := false
	for seq := env.dlqSeqBefore + 1; seq <= dlqInfo.State.LastSeq; seq++ {
		if m, gErr := js.GetMsg(DefaultDLQStream, seq); gErr == nil && m != nil && strings.Contains(string(m.Data), targetEvtID) {
			foundDLQ = true
			break
		}
	}
	if !foundDLQ {
		t.Fatalf("E1: expected DLQ message for %s in %s (seq > %d)", targetEvtID, DefaultDLQStream, env.dlqSeqBefore)
	}

	// Verify consumer acknowledged the message after attempt 6 succeeded (0 pending / 0 ack pending).
	deadlineAck := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadlineAck) {
		info, err := js.ConsumerInfo(streamName, DurableConsumerName)
		if err == nil && info.NumPending == 0 && info.NumAckPending == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	info, _ := js.ConsumerInfo(streamName, DurableConsumerName)
	t.Fatalf("E1: expected consumer to Ack after attempt-6 DLQ persisted, got info=%+v", info)
}
