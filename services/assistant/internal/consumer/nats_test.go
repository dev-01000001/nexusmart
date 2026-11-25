package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nexusmart/core/services/assistant/internal/service"
	"github.com/nexusmart/core/services/assistant/internal/store"
)

type fakeMsg struct {
	subject      string
	data         []byte
	headers      map[string]string
	numDelivered uint64
	acked        bool
	naked        bool
	nakDelay     time.Duration
}

func (m *fakeMsg) Subject() string        { return m.subject }
func (m *fakeMsg) Data() []byte           { return m.data }
func (m *fakeMsg) Header(k string) string { return m.headers[k] }
func (m *fakeMsg) NumDelivered() uint64   { return m.numDelivered }
func (m *fakeMsg) Ack() error             { m.acked = true; return nil }
func (m *fakeMsg) NakWithDelay(d time.Duration) error {
	m.naked = true
	m.nakDelay = d
	return nil
}

type fakeRefresher struct {
	calls       int
	err         error
	lastP       string
	lastShopID  string
	lastReviews []service.ReviewInput
}

func (r *fakeRefresher) RefreshReviewSummary(_ context.Context, productID, shopID string, reviews []service.ReviewInput) (*service.ReviewSummaryResult, error) {
	r.calls++
	r.lastP = productID
	r.lastShopID = shopID
	r.lastReviews = reviews
	if r.err != nil {
		return nil, r.err
	}
	return &service.ReviewSummaryResult{ProductID: productID, Available: true}, nil
}

type fakeDLQ struct {
	subjects []string
	payloads [][]byte
}

func (d *fakeDLQ) PublishDLQ(subject string, data []byte) error {
	d.subjects = append(d.subjects, subject)
	d.payloads = append(d.payloads, data)
	return nil
}

func TestConsumerIdempotentProcessing(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	ref := &fakeRefresher{}
	dlq := &fakeDLQ{}
	c := NewTestableConsumer(ref, st, dlq, 3, nil)

	payload, _ := json.Marshal(map[string]any{
		"event_id": "evt-rev-1",
		"payload": map[string]any{
			"review_id":  "rev-1",
			"product_id": "prod-1",
			"shop_id":    "shop-1",
			"rating":     5,
			"comment":    "Rất đẹp",
		},
	})

	msg1 := &fakeMsg{subject: "review.created", data: payload, numDelivered: 1}
	c.HandleMessage(ctx, msg1)
	if !msg1.acked || ref.calls != 1 || ref.lastP != "prod-1" {
		t.Fatalf("expected ack and 1 refresh call, got acked=%v calls=%d", msg1.acked, ref.calls)
	}

	// Duplicate event must be skipped cleanly!
	msg2 := &fakeMsg{subject: "review.created", data: payload, numDelivered: 1}
	c.HandleMessage(ctx, msg2)
	if !msg2.acked || ref.calls != 1 {
		t.Fatalf("expected duplicate skipped, got acked=%v calls=%d", msg2.acked, ref.calls)
	}
	if len(dlq.subjects) != 0 {
		t.Fatalf("expected no DLQ publish, got %v", dlq.subjects)
	}
}

func TestConsumerRetryAndPoisonDLQ(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	ref := &fakeRefresher{err: errors.New("transient llm error")}
	dlq := &fakeDLQ{}
	c := NewTestableConsumer(ref, st, dlq, 3, nil)

	payload, _ := json.Marshal(map[string]any{
		"event_id":   "evt-rev-fail",
		"review_id":  "rev-2",
		"product_id": "prod-2",
		"shop_id":    "shop-1",
		"rating":     4,
		"comment":    "Ổn",
	})

	// Attempt 1 -> NakWithDelay.
	m1 := &fakeMsg{subject: "product.review_created", data: payload, numDelivered: 1}
	c.HandleMessage(ctx, m1)
	if !m1.naked || m1.acked || len(dlq.subjects) != 0 {
		t.Fatalf("expected Nak on attempt 1, got naked=%v acked=%v dlq=%d", m1.naked, m1.acked, len(dlq.subjects))
	}

	// Attempt 3 (>= maxAttempts) -> routed to DLQ and Acked.
	m3 := &fakeMsg{subject: "product.review_created", data: payload, numDelivered: 3}
	c.HandleMessage(ctx, m3)
	if !m3.acked || len(dlq.subjects) != 1 || dlq.subjects[0] != "dlq.product.review_created" {
		t.Fatalf("expected DLQ routing on attempt 3, got acked=%v dlq=%v", m3.acked, dlq.subjects)
	}

	// Malformed poison JSON -> immediately DLQ + Ack.
	bad := &fakeMsg{subject: "review.created", data: []byte("{invalid"), numDelivered: 1}
	c.HandleMessage(ctx, bad)
	if !bad.acked || len(dlq.subjects) != 2 || dlq.subjects[1] != "dlq.review.created" {
		t.Fatalf("expected malformed payload routed to DLQ immediately, got acked=%v dlq=%v", bad.acked, dlq.subjects)
	}
}

func TestConsumerMainReviewCreatedEnvelopeShape(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	ref := &fakeRefresher{}
	dlq := &fakeDLQ{}
	c := NewTestableConsumer(ref, st, dlq, 3, nil)

	// Exact envelope emitted by merged product service (has comment, order_item_id, review_count,
	// rating_avg_x100; omits buyer_id and order_id).
	payload, _ := json.Marshal(map[string]any{
		"event_id":    "evt-main-review-1",
		"event_type":  "review.created",
		"trace_id":    "0123456789abcdef0123456789abcdef",
		"occurred_at": "2026-11-25T10:00:00Z",
		"payload": map[string]any{
			"review_id":       "rev-main-1",
			"product_id":      "prod-main-1",
			"shop_id":         "shop-bach-dang",
			"order_item_id":   "oi-1",
			"rating":          5,
			"comment":         "Vải cotton 220gsm dày mịn, đúng phom.",
			"review_count":    12,
			"rating_avg_x100": 485,
			"occurred_at":     "2026-11-25T10:00:00Z",
		},
	})

	msg := &fakeMsg{subject: "review.created", data: payload, numDelivered: 1}
	c.HandleMessage(ctx, msg)
	if !msg.acked || len(dlq.subjects) != 0 {
		t.Fatalf("expected ack with 0 DLQ, got acked=%v dlq=%v", msg.acked, dlq.subjects)
	}
	if ref.calls != 1 || ref.lastP != "prod-main-1" || ref.lastShopID != "shop-bach-dang" {
		t.Fatalf("unexpected refresher call: calls=%d prod=%q shop=%q", ref.calls, ref.lastP, ref.lastShopID)
	}
	if len(ref.lastReviews) != 1 || ref.lastReviews[0].ReviewID != "rev-main-1" || ref.lastReviews[0].Rating != 5 || ref.lastReviews[0].Comment != "Vải cotton 220gsm dày mịn, đúng phom." {
		t.Fatalf("expected review comment preserved, got %+v", ref.lastReviews)
	}
}

type fakeStreamManager struct {
	streams map[string]*nats.StreamConfig
}

func (f *fakeStreamManager) StreamInfo(stream string, _ ...nats.JSOpt) (*nats.StreamInfo, error) {
	cfg, ok := f.streams[stream]
	if !ok {
		return nil, nats.ErrStreamNotFound
	}
	cp := *cfg
	return &nats.StreamInfo{Config: cp}, nil
}

func (f *fakeStreamManager) AddStream(cfg *nats.StreamConfig, _ ...nats.JSOpt) (*nats.StreamInfo, error) {
	cp := *cfg
	f.streams[cfg.Name] = &cp
	return &nats.StreamInfo{Config: cp}, nil
}

func (f *fakeStreamManager) UpdateStream(cfg *nats.StreamConfig, _ ...nats.JSOpt) (*nats.StreamInfo, error) {
	cp := *cfg
	f.streams[cfg.Name] = &cp
	return &nats.StreamInfo{Config: cp}, nil
}

func TestEnsureStreamsIncludesCanonicalSubjects(t *testing.T) {
	mgr := &fakeStreamManager{streams: map[string]*nats.StreamConfig{}}
	if err := EnsureStreams(mgr, "NEXUSMART", "NEXUSMART_DLQ"); err != nil {
		t.Fatalf("EnsureStreams failed: %v", err)
	}
	primary := mgr.streams["NEXUSMART"]
	if primary == nil {
		t.Fatal("primary stream not created")
	}
	has := map[string]bool{}
	for _, s := range primary.Subjects {
		has[s] = true
	}
	for _, want := range []string{"user.>", "order.>", "payment.>", "shipment.>", "product.>", "promotion.>", "review.>"} {
		if !has[want] {
			t.Errorf("missing canonical subject %q in %v", want, primary.Subjects)
		}
	}
	if mgr.streams["NEXUSMART_DLQ"] == nil {
		t.Fatal("DLQ stream not created")
	}
}
