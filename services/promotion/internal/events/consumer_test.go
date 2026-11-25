package events_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/nats-io/nats.go"

	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/promotion/internal/events"
	"github.com/nexusmart/core/services/promotion/internal/store"
)

type recordingDLQ struct {
	subjects []string
	reasons  []string
	headers  []map[string]string
	err      error
}

func (r *recordingDLQ) PublishDLQ(_ context.Context, subject string, _ []byte, headers map[string]string) error {
	r.subjects = append(r.subjects, subject)
	r.reasons = append(r.reasons, headers["x-dlq-reason"])
	cp := make(map[string]string, len(headers))
	for k, v := range headers {
		cp[k] = v
	}
	r.headers = append(r.headers, cp)
	return r.err
}

type failingEventStore struct {
	*store.MemoryStore
	completedErr error
	checkErr     error
}

func (f *failingEventStore) IsEventProcessed(ctx context.Context, eventID string) (bool, error) {
	if f.checkErr != nil {
		return false, f.checkErr
	}
	return f.MemoryStore.IsEventProcessed(ctx, eventID)
}

func (f *failingEventStore) ProcessOrderCompletedEvent(ctx context.Context, eventID, subject, orderID, buyerID string, coinsReward int64) (bool, error) {
	if f.completedErr != nil {
		return false, f.completedErr
	}
	return f.MemoryStore.ProcessOrderCompletedEvent(ctx, eventID, subject, orderID, buyerID, coinsReward)
}

type fakeJetStream struct {
	nats.JetStreamContext
	streams       map[string]*nats.StreamInfo
	consumers     map[string]*nats.ConsumerInfo
	streamInfoErr map[string]error
	addStreamErr  map[string]error
	subErr        map[string]error
	subscribed    []string
	publishedMsgs []*nats.Msg
	publishErr    error
}

func newFakeJetStream() *fakeJetStream {
	return &fakeJetStream{
		streams:       make(map[string]*nats.StreamInfo),
		consumers:     make(map[string]*nats.ConsumerInfo),
		streamInfoErr: make(map[string]error),
		addStreamErr:  make(map[string]error),
		subErr:        make(map[string]error),
	}
}

func (f *fakeJetStream) StreamInfo(stream string, _ ...nats.JSOpt) (*nats.StreamInfo, error) {
	if err := f.streamInfoErr[stream]; err != nil {
		return nil, err
	}
	if info, ok := f.streams[stream]; ok {
		return info, nil
	}
	return nil, nats.ErrStreamNotFound
}

func (f *fakeJetStream) AddStream(cfg *nats.StreamConfig, _ ...nats.JSOpt) (*nats.StreamInfo, error) {
	if err := f.addStreamErr[cfg.Name]; err != nil {
		return nil, err
	}
	info := &nats.StreamInfo{Config: *cfg}
	f.streams[cfg.Name] = info
	return info, nil
}

func (f *fakeJetStream) UpdateStream(cfg *nats.StreamConfig, _ ...nats.JSOpt) (*nats.StreamInfo, error) {
	info := &nats.StreamInfo{Config: *cfg}
	f.streams[cfg.Name] = info
	return info, nil
}

func (f *fakeJetStream) ConsumerInfo(stream, consumer string, _ ...nats.JSOpt) (*nats.ConsumerInfo, error) {
	if info, ok := f.consumers[stream+"/"+consumer]; ok {
		return info, nil
	}
	return nil, nats.ErrConsumerNotFound
}

func (f *fakeJetStream) AddConsumer(stream string, cfg *nats.ConsumerConfig, _ ...nats.JSOpt) (*nats.ConsumerInfo, error) {
	info := &nats.ConsumerInfo{Stream: stream, Name: cfg.Durable, Config: *cfg}
	f.consumers[stream+"/"+cfg.Durable] = info
	return info, nil
}

func (f *fakeJetStream) UpdateConsumer(stream string, cfg *nats.ConsumerConfig, _ ...nats.JSOpt) (*nats.ConsumerInfo, error) {
	info := &nats.ConsumerInfo{Stream: stream, Name: cfg.Durable, Config: *cfg}
	f.consumers[stream+"/"+cfg.Durable] = info
	return info, nil
}

func (f *fakeJetStream) QueueSubscribe(subj, _ string, _ nats.MsgHandler, _ ...nats.SubOpt) (*nats.Subscription, error) {
	if err := f.subErr[subj]; err != nil {
		return nil, err
	}
	f.subscribed = append(f.subscribed, subj)
	return nil, nil
}

func (f *fakeJetStream) PublishMsg(m *nats.Msg, _ ...nats.PubOpt) (*nats.PubAck, error) {
	if f.publishErr != nil {
		return nil, f.publishErr
	}
	f.publishedMsgs = append(f.publishedMsgs, m)
	return &nats.PubAck{Stream: events.DefaultDLQStream, Sequence: uint64(len(f.publishedMsgs))}, nil
}

func makeCompletedEvent(eventID, orderID, buyerID string, coins int64) []byte {
	payload, _ := json.Marshal(events.OrderCompletedPayload{
		OrderID:         orderID,
		OrderCode:       "NM-001",
		BuyerID:         buyerID,
		ShopID:          "shop-1",
		SubtotalVND:     200000,
		CommissionVND:   10000,
		TotalPayableVND: 200000,
		CoinsReward:     coins,
	})
	raw, _ := json.Marshal(events.Envelope{
		EventID:    eventID,
		EventType:  events.SubjectOrderCompleted,
		TraceID:    "4bf92f3577b34da6a3ce929d0e0e4736",
		OccurredAt: "2026-11-20T02:35:00Z",
		Payload:    payload,
	})
	return raw
}

func TestOrderCompletedConsumer_IdempotentCoinCredit(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemoryStore()
	log := logger.New("promotion-events-test", logger.WithWriter(io.Discard))
	consumer := events.NewOrderCompletedConsumer(mem, nil, "NEXUSMART", log)
	dlq := &recordingDLQ{}
	consumer.SetDLQPublisher(dlq)

	raw := makeCompletedEvent("evt-001", "ord-001", "buyer-1", 1500)

	// Deliver exact same event 5 times -> credited ONCE
	for i := 0; i < 5; i++ {
		ack, err := consumer.ProcessRawMessage(ctx, raw, nil, 1)
		if err != nil || !ack {
			t.Fatalf("attempt %d unexpected: ack=%v err=%v", i, ack, err)
		}
	}

	// Deliver a DIFFERENT event_id for the SAME order_id -> still credited ONCE (reference_id = order_id)
	dupOrderRaw := makeCompletedEvent("evt-002", "ord-001", "buyer-1", 1500)
	ack, err := consumer.ProcessRawMessage(ctx, dupOrderRaw, nil, 1)
	if err != nil || !ack {
		t.Fatalf("dup order event unexpected: ack=%v err=%v", ack, err)
	}

	bal, txns, err := mem.GetCoinBalance(ctx, "buyer-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if bal != 1500 || len(txns) != 1 || mem.AccountBalance("buyer-1") != 1500 || mem.LedgerSum("buyer-1") != 1500 {
		t.Fatalf("expected exactly one 1500 coin credit, got bal=%d txns=%d acct=%d sum=%d",
			bal, len(txns), mem.AccountBalance("buyer-1"), mem.LedgerSum("buyer-1"))
	}
	if len(dlq.subjects) != 0 {
		t.Fatalf("expected no DLQ messages, got %v", dlq.subjects)
	}

	// Zero coins_reward should process cleanly without creating a zero-delta coin tx
	zeroRaw := makeCompletedEvent("evt-003", "ord-002", "buyer-1", 0)
	ack, err = consumer.ProcessRawMessage(ctx, zeroRaw, nil, 1)
	if err != nil || !ack {
		t.Fatalf("zero reward event unexpected: ack=%v err=%v", ack, err)
	}
	if _, txns, _ = mem.GetCoinBalance(ctx, "buyer-1", 10); len(txns) != 1 {
		t.Fatalf("expected still 1 coin tx after 0-reward order, got %d", len(txns))
	}
}

func TestOrderCompletedConsumer_PoisonMessageSentToDLQ(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemoryStore()
	log := logger.New("promotion-events-test", logger.WithWriter(io.Discard))
	consumer := events.NewOrderCompletedConsumer(mem, nil, "NEXUSMART", log)
	dlq := &recordingDLQ{}
	consumer.SetDLQPublisher(dlq)

	// Invalid JSON -> sent to DLQ and acked
	ack, err := consumer.ProcessRawMessage(ctx, []byte("{not-json"), nil, 1)
	if err != nil || !ack {
		t.Fatalf("expected poison message acked after DLQ, got ack=%v err=%v", ack, err)
	}
	// Missing event_id -> sent to DLQ and acked
	ack, err = consumer.ProcessRawMessage(ctx, []byte(`{"event_type":"order.completed","payload":{}}`), nil, 1)
	if err != nil || !ack {
		t.Fatalf("expected missing event_id acked after DLQ, got ack=%v err=%v", ack, err)
	}
	// Invalid payload JSON -> sent to DLQ and acked
	ack, err = consumer.ProcessRawMessage(ctx, []byte(`{"event_id":"e-bad-p","event_type":"order.completed","payload":"not-an-object"}`), nil, 1)
	if err != nil || !ack {
		t.Fatalf("expected invalid payload acked after DLQ, got ack=%v err=%v", ack, err)
	}

	if len(dlq.subjects) != 3 || dlq.subjects[0] != "dlq.order.completed" || dlq.subjects[1] != "dlq.order.completed" || dlq.subjects[2] != "dlq.order.completed" {
		t.Fatalf("expected 3 DLQ publishes to dlq.order.completed, got %v", dlq.subjects)
	}
}

func TestOrderCompletedConsumer_TransientRetryToDLQAndFailSafeNak(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemoryStore()
	fStore := &failingEventStore{MemoryStore: mem}
	log := logger.New("promotion-events-test", logger.WithWriter(io.Discard))
	consumer := events.NewOrderCompletedConsumer(fStore, nil, "NEXUSMART", log)
	dlq := &recordingDLQ{}
	consumer.SetDLQPublisher(dlq)

	// Transient DB error on order.completed: attempts 1..4 NAK, attempt 5 -> dlq.order.completed
	fStore.completedErr = errors.New("db timeout")
	compRaw := makeCompletedEvent("evt-retry-c", "ord-r-c", "buyer-r", 100)
	for attempt := uint64(1); attempt < events.MaxDeliveryAttempts; attempt++ {
		ack, err := consumer.ProcessRawMessage(ctx, compRaw, nil, attempt)
		if ack || err == nil {
			t.Fatalf("attempt %d: expected NAK + err, got ack=%v err=%v", attempt, ack, err)
		}
	}
	ack, err := consumer.ProcessRawMessage(ctx, compRaw, nil, events.MaxDeliveryAttempts)
	if !ack || err != nil {
		t.Fatalf("attempt 5 order.completed: expected ack after DLQ, got ack=%v err=%v", ack, err)
	}
	if len(dlq.subjects) != 1 || dlq.subjects[0] != "dlq.order.completed" || !strings.Contains(dlq.reasons[0], "max retries exceeded") {
		t.Fatalf("expected dlq.order.completed max retries, got subj=%v reasons=%v", dlq.subjects, dlq.reasons)
	}
	fStore.completedErr = nil

	// E1 fail-safe: when NO durable DLQ sink succeeds (publish fails or publisher is nil without DB DLQ),
	// consumer MUST NAK instead of silently dropping the message.
	dlq.err = errors.New("dlq broker down")
	if ack, err := consumer.ProcessRawMessage(ctx, []byte("{bad"), nil, 1); ack || err == nil {
		t.Fatalf("DLQ publish failure with no DB DLQ should NAK: ack=%v err=%v", ack, err)
	}
	consumer.SetDLQPublisher(nil)
	if ack, err := consumer.ProcessRawMessage(ctx, []byte("{bad"), nil, 1); ack || err == nil {
		t.Fatalf("nil DLQ publisher with no DB DLQ should NAK: ack=%v err=%v", ack, err)
	}
}

func TestOrderCompletedConsumer_JetStreamLifecycleAndDLQ(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemoryStore()

	// Nil js & nil logger defaults cleanly
	nilConsumer := events.NewOrderCompletedConsumer(mem, nil, "", nil)
	if err := nilConsumer.Start(ctx); err != nil {
		t.Fatalf("Start with nil js: %v", err)
	}
	if err := nilConsumer.Stop(); err != nil {
		t.Fatalf("Stop with nil subs: %v", err)
	}

	// Fake JetStream creates both streams and queue-subscribes to order.completed and user.shop_status_changed
	fjs := newFakeJetStream()
	c := events.NewOrderCompletedConsumer(mem, fjs, "NEXUSMART", nil)
	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start with fake js: %v", err)
	}
	if _, ok := fjs.streams["NEXUSMART"]; !ok {
		t.Fatal("expected NEXUSMART stream created")
	}
	if _, ok := fjs.streams[events.DefaultDLQStream]; !ok {
		t.Fatal("expected NEXUSMART_DLQ stream created")
	}
	if len(fjs.subscribed) != 2 || fjs.subscribed[0] != events.SubjectOrderCompleted || fjs.subscribed[1] != events.SubjectShopStatusChanged {
		t.Fatalf("expected subscriptions to order.completed and user.shop_status_changed, got %v", fjs.subscribed)
	}
	if err := c.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Poison message exercises real jetStreamDLQ.PublishDLQ
	c2 := events.NewOrderCompletedConsumer(mem, fjs, "NEXUSMART", nil)
	if ack, err := c2.ProcessRawMessage(ctx, []byte("{corrupt"), map[string]string{"x-test": "1"}, 1); !ack || err != nil {
		t.Fatalf("poison via jetStreamDLQ: ack=%v err=%v", ack, err)
	}
	if len(fjs.publishedMsgs) != 1 || fjs.publishedMsgs[0].Subject != "dlq.order.completed" {
		t.Fatalf("expected 1 DLQ msg to dlq.order.completed, got %+v", fjs.publishedMsgs)
	}

	// Error branches in EnsureStreams and Start
	fjsErr1 := newFakeJetStream()
	fjsErr1.streamInfoErr["NEXUSMART"] = errors.New("nats permission denied")
	if err := events.NewOrderCompletedConsumer(mem, fjsErr1, "NEXUSMART", nil).Start(ctx); err == nil {
		t.Fatal("expected StreamInfo error")
	}

	fjsErr2 := newFakeJetStream()
	fjsErr2.addStreamErr["NEXUSMART"] = errors.New("disk full")
	if err := events.NewOrderCompletedConsumer(mem, fjsErr2, "NEXUSMART", nil).EnsureStreams(); err == nil {
		t.Fatal("expected AddStream primary error")
	}

	fjsErr3 := newFakeJetStream()
	fjsErr3.streamInfoErr[events.DefaultDLQStream] = errors.New("dlq inspect failed")
	if err := events.NewOrderCompletedConsumer(mem, fjsErr3, "NEXUSMART", nil).EnsureStreams(); err == nil {
		t.Fatal("expected StreamInfo DLQ error")
	}

	fjsErr4 := newFakeJetStream()
	fjsErr4.addStreamErr[events.DefaultDLQStream] = errors.New("dlq add failed")
	if err := events.NewOrderCompletedConsumer(mem, fjsErr4, "NEXUSMART", nil).EnsureStreams(); err == nil {
		t.Fatal("expected AddStream DLQ error")
	}

	fjsErr5 := newFakeJetStream()
	fjsErr5.subErr[events.SubjectOrderCompleted] = errors.New("sub completed failed")
	if err := events.NewOrderCompletedConsumer(mem, fjsErr5, "NEXUSMART", nil).Start(ctx); err == nil {
		t.Fatal("expected QueueSubscribe order.completed error")
	}

	fjsErr6 := newFakeJetStream()
	fjsErr6.subErr[events.SubjectShopStatusChanged] = errors.New("sub shop status failed")
	if err := events.NewOrderCompletedConsumer(mem, fjsErr6, "NEXUSMART", nil).Start(ctx); err == nil {
		t.Fatal("expected QueueSubscribe user.shop_status_changed error")
	}
}
