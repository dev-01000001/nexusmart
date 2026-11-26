package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"sync"
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
	mu       sync.Mutex
	subjects []string
	payloads [][]byte
	err      error
}

func (d *fakeDLQ) PublishDLQ(subject string, data []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.err != nil {
		return d.err
	}
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

type failingMarkEventStore struct {
	store.Store
	markErr error
}

func (s *failingMarkEventStore) MarkEventProcessed(_ context.Context, _, _ string) (bool, error) {
	return false, s.markErr
}

type fnRefresher struct {
	fn func(ctx context.Context, productID, shopID string, reviews []service.ReviewInput) (*service.ReviewSummaryResult, error)
}

func (r *fnRefresher) RefreshReviewSummary(ctx context.Context, productID, shopID string, reviews []service.ReviewInput) (*service.ReviewSummaryResult, error) {
	return r.fn(ctx, productID, shopID, reviews)
}

func TestConsumerNoDrainASTGuard(t *testing.T) {
	fset := token.NewFileSet()
	fileAST, err := parser.ParseFile(fset, "nats.go", nil, 0)
	if err != nil {
		t.Fatalf("parse nats.go: %v", err)
	}
	ast.Inspect(fileAST, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if ok && (sel.Sel.Name == "Drain" || sel.Sel.Name == "Unsubscribe") {
			t.Fatalf("E1: nats.go must NEVER call .%s() on durable subscriptions", sel.Sel.Name)
		}
		return true
	})
}

func TestConsumerDLQFailureNaksNeverAcks(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	ref := &fakeRefresher{err: errors.New("permanent upstream error")}
	dlq := &fakeDLQ{err: errors.New("nats dlq down")}
	c := NewTestableConsumer(ref, st, dlq, 3, nil)

	// 1. Malformed JSON with failing DLQ must NakWithDelay, never Ack.
	bad := &fakeMsg{subject: "review.created", data: []byte("{bad-json"), numDelivered: 1}
	c.HandleMessage(ctx, bad)
	if bad.acked || !bad.naked {
		t.Fatalf("malformed message with failing DLQ must Nak and never Ack: acked=%v naked=%v", bad.acked, bad.naked)
	}

	// 2. Max attempts failure with failing DLQ must NakWithDelay, never Ack.
	validPayload, _ := json.Marshal(map[string]any{
		"event_id":   "evt-dlq-fail-1",
		"review_id":  "rev-dlq-1",
		"product_id": "prod-1",
		"shop_id":    "shop-1",
		"rating":     4,
		"comment":    "Ổn",
	})
	mMax := &fakeMsg{subject: "review.created", data: validPayload, numDelivered: 3}
	c.HandleMessage(ctx, mMax)
	if mMax.acked || !mMax.naked {
		t.Fatalf("max-attempts failure with failing DLQ must Nak and never Ack: acked=%v naked=%v", mMax.acked, mMax.naked)
	}
}

func TestConsumerMarkEventProcessedErrorNotSwallowed(t *testing.T) {
	ctx := context.Background()
	st := &failingMarkEventStore{
		Store:   store.NewMemoryStore(),
		markErr: errors.New("postgres write failed"),
	}
	ref := &fakeRefresher{}
	dlq := &fakeDLQ{}
	c := NewTestableConsumer(ref, st, dlq, 3, nil)

	payload, _ := json.Marshal(map[string]any{
		"event_id":   "evt-mark-fail-1",
		"review_id":  "rev-mark-1",
		"product_id": "prod-1",
		"shop_id":    "shop-1",
		"rating":     5,
		"comment":    "Rất tốt",
	})

	// Attempt 1: MarkEventProcessed error must NakWithDelay, never Ack.
	m1 := &fakeMsg{subject: "review.created", data: payload, numDelivered: 1}
	c.HandleMessage(ctx, m1)
	if m1.acked || !m1.naked || len(dlq.subjects) != 0 {
		t.Fatalf("MarkEventProcessed failure on attempt 1 must Nak and never Ack: acked=%v naked=%v dlq=%v", m1.acked, m1.naked, dlq.subjects)
	}

	// Attempt 3 (>= maxAttempts): routes to DLQ and Acks only after DLQ succeeds.
	m3 := &fakeMsg{subject: "review.created", data: payload, numDelivered: 3}
	c.HandleMessage(ctx, m3)
	if !m3.acked || m3.naked || len(dlq.subjects) != 1 {
		t.Fatalf("MarkEventProcessed failure on attempt 3 must route to DLQ and Ack: acked=%v naked=%v dlq=%v", m3.acked, m3.naked, dlq.subjects)
	}
}

func TestConsumerShutdownDetachedContextAndCloseWaitsInFlight(t *testing.T) {
	baseCtx, cancel := context.WithCancel(context.Background())
	st := store.NewMemoryStore()
	dlq := &fakeDLQ{}

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var sawHandlerCtxErr error
	var sawDeadline bool
	ref := &fnRefresher{
		fn: func(hCtx context.Context, productID, _ string, _ []service.ReviewInput) (*service.ReviewSummaryResult, error) {
			started <- struct{}{}
			<-release
			_, sawDeadline = hCtx.Deadline()
			sawHandlerCtxErr = hCtx.Err()
			return &service.ReviewSummaryResult{ProductID: productID, Available: true}, nil
		},
	}
	c := NewTestableConsumer(ref, st, dlq, 3, nil)

	payload, _ := json.Marshal(map[string]any{
		"event_id":   "evt-inflight-1",
		"review_id":  "rev-inflight-1",
		"product_id": "prod-1",
		"shop_id":    "shop-1",
		"rating":     5,
		"comment":    "Tốt",
	})
	msg := &fakeMsg{subject: "review.created", data: payload, numDelivered: 1}

	done := make(chan struct{})
	go func() {
		defer close(done)
		c.HandleMessage(baseCtx, msg)
	}()
	<-started

	// Cancel shutdown context and call Close() while message is in-flight.
	cancel()
	closeDone := make(chan error, 1)
	go func() {
		closeDone <- c.Close()
	}()

	select {
	case err := <-closeDone:
		close(release)
		t.Fatalf("Close returned early while message handler was in-flight (err=%v)", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	<-done
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return after in-flight message finished")
	}

	if sawHandlerCtxErr != nil || !sawDeadline {
		t.Fatalf("in-flight message must run with detached bounded context (no shutdown cancel), got err=%v hasDeadline=%v", sawHandlerCtxErr, sawDeadline)
	}
	if !msg.acked || msg.naked || len(dlq.subjects) != 0 {
		t.Fatalf("in-flight message finishing cleanly during shutdown should Ack without DLQ/Nak: acked=%v naked=%v dlq=%v", msg.acked, msg.naked, dlq.subjects)
	}

	// Pre-canceled context or stopping consumer must skip quietly without Ack, Nak, or DLQ.
	msgAfter := &fakeMsg{subject: "review.created", data: payload, numDelivered: 3}
	c.HandleMessage(baseCtx, msgAfter)
	if msgAfter.acked || msgAfter.naked || len(dlq.subjects) != 0 {
		t.Fatalf("stopping consumer must neither Ack nor Nak nor DLQ: acked=%v naked=%v dlq=%v", msgAfter.acked, msgAfter.naked, dlq.subjects)
	}
}

func TestConsumerOutOfRangeRatingRoutedToDLQ(t *testing.T) {
	ctx := context.Background()
	for _, rating := range []int32{0, -1, 6, 99} {
		st := store.NewMemoryStore()
		ref := &fakeRefresher{}
		dlq := &fakeDLQ{}
		c := NewTestableConsumer(ref, st, dlq, 3, nil)

		payload, _ := json.Marshal(map[string]any{
			"event_id":   "evt-bad-rating",
			"review_id":  "rev-bad-rating",
			"product_id": "prod-1",
			"shop_id":    "shop-1",
			"rating":     rating,
			"comment":    "Payload hỏng rating",
		})
		msg := &fakeMsg{subject: "review.created", data: payload, numDelivered: 1}
		c.HandleMessage(ctx, msg)
		if ref.calls != 0 {
			t.Fatalf("rating=%d: must NOT call RefreshReviewSummary (no default to 5), got calls=%d reviews=%+v", rating, ref.calls, ref.lastReviews)
		}
		if len(dlq.subjects) != 1 || dlq.subjects[0] != "dlq.review.created" || !msg.acked {
			t.Fatalf("rating=%d: expected routed to DLQ + Ack, got dlq=%v acked=%v naked=%v", rating, dlq.subjects, msg.acked, msg.naked)
		}
	}
}
