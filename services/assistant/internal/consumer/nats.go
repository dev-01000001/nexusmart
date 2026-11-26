package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nexusmart/core/pkg/common/jetstream"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/assistant/internal/service"
)

const (
	defaultMsgTimeout      = 25 * time.Second
	defaultDLQWriteTimeout = 5 * time.Second
	nakBackoffMax          = 30 * time.Second
)

func nakDelay(delivered uint64) time.Duration {
	if delivered == 0 {
		delivered = 1
	}
	d := time.Duration(delivered) * time.Second
	if d > nakBackoffMax {
		return nakBackoffMax
	}
	return d
}

// ReviewRefresher refreshes the cached AI summary for a product upon new reviews.
type ReviewRefresher interface {
	RefreshReviewSummary(ctx context.Context, productID, shopID string, reviews []service.ReviewInput) (*service.ReviewSummaryResult, error)
}

// EventStore persists processed event IDs for idempotent consumption.
type EventStore interface {
	IsEventProcessed(ctx context.Context, eventID string) (bool, error)
	MarkEventProcessed(ctx context.Context, eventID, subject string) (inserted bool, err error)
}

// DLQPublisher publishes poison messages to NEXUSMART_DLQ (subject dlq.<original_subject>).
type DLQPublisher interface {
	PublishDLQ(subject string, data []byte) error
}

// Message abstracts *nats.Msg for deterministic unit testing of retry + DLQ routing.
type Message interface {
	Subject() string
	Data() []byte
	Header(key string) string
	NumDelivered() uint64
	Ack() error
	NakWithDelay(delay time.Duration) error
}

// ReviewEventPayload represents the review payload carried on review.created / product.review_created.
type ReviewEventPayload struct {
	EventID   string `json:"event_id"`
	ReviewID  string `json:"review_id"`
	ProductID string `json:"product_id"`
	ShopID    string `json:"shop_id"`
	Rating    int32  `json:"rating"`
	Comment   string `json:"comment"`
}

type eventEnvelope struct {
	EventID string          `json:"event_id"`
	Payload json.RawMessage `json:"payload"`
}

// EnsureStreams idempotently creates or updates NEXUSMART and NEXUSMART_DLQ streams
// using the canonical NexusMart subject list (including promotion.> and review.>).
func EnsureStreams(js jetstream.StreamManager, primaryStream, dlqStream string) error {
	return jetstream.EnsureStreams(js, primaryStream, dlqStream)
}

// Consumer consumes review creation events idempotently with DLQ routing on poison messages.
type Consumer struct {
	js          nats.JetStreamContext
	refresher   ReviewRefresher
	eventStore  EventStore
	dlqPub      DLQPublisher
	stream      string
	maxAttempts uint64
	msgTimeout  time.Duration
	logger      *slog.Logger

	mu       sync.Mutex
	stopping bool
	wg       sync.WaitGroup
}

type jsDLQPublisher struct {
	js nats.JetStreamContext
}

func (p jsDLQPublisher) PublishDLQ(subject string, data []byte) error {
	dlqCtx, cancel := context.WithTimeout(context.Background(), defaultDLQWriteTimeout)
	defer cancel()
	msg := nats.NewMsg(subject)
	msg.Data = data
	_, err := p.js.PublishMsg(msg, nats.Context(dlqCtx))
	return err
}

// NewConsumer constructs a NATS JetStream consumer for review summarization.
func NewConsumer(
	js nats.JetStreamContext,
	refresher ReviewRefresher,
	eventStore EventStore,
	stream string,
	maxAttempts uint64,
	logger *slog.Logger,
) *Consumer {
	if maxAttempts == 0 {
		maxAttempts = 5
	}
	if logger == nil {
		logger = slog.Default()
	}
	var pub DLQPublisher
	if js != nil {
		pub = jsDLQPublisher{js: js}
	}
	return &Consumer{
		js:          js,
		refresher:   refresher,
		eventStore:  eventStore,
		dlqPub:      pub,
		stream:      stream,
		maxAttempts: maxAttempts,
		msgTimeout:  defaultMsgTimeout,
		logger:      logger,
	}
}

// NewTestableConsumer constructs a Consumer with injectable DLQPublisher for unit tests.
func NewTestableConsumer(
	refresher ReviewRefresher,
	eventStore EventStore,
	dlqPub DLQPublisher,
	maxAttempts uint64,
	logger *slog.Logger,
) *Consumer {
	if maxAttempts == 0 {
		maxAttempts = 5
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Consumer{
		refresher:   refresher,
		eventStore:  eventStore,
		dlqPub:      dlqPub,
		maxAttempts: maxAttempts,
		msgTimeout:  defaultMsgTimeout,
		logger:      logger,
	}
}

// Start registers durable JetStream queue subscriptions for review.created and product.review_created.
func (c *Consumer) Start(ctx context.Context) error {
	c.mu.Lock()
	c.stopping = false
	c.mu.Unlock()

	bindings := []struct {
		subject string
		durable string
	}{
		{subject: "review.created", durable: "assistant-review-summarizer"},
		{subject: "product.review_created", durable: "assistant-product-review-summarizer"},
	}

	for _, b := range bindings {
		_, err := c.js.QueueSubscribe(
			b.subject,
			"assistant-review-workers",
			func(msg *nats.Msg) {
				c.HandleMessage(ctx, natsMessageAdapter{msg: msg})
			},
			nats.BindStream(c.stream),
			nats.Durable(b.durable),
			nats.ManualAck(),
			nats.AckExplicit(),
			nats.AckWait(30*time.Second),
			nats.MaxDeliver(int(c.maxAttempts)+1),
			nats.DeliverAll(),
		)
		if err != nil {
			_ = c.Close()
			return fmt.Errorf("subscribe %s on stream %s: %w", b.subject, c.stream, err)
		}
	}
	return nil
}

func (c *Consumer) isStopping() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stopping
}

func (c *Consumer) effectiveMsgTimeout() time.Duration {
	if c.msgTimeout > 0 {
		return c.msgTimeout
	}
	return defaultMsgTimeout
}

// Close prevents new message handlers from starting and waits for all in-flight handlers
// to finish (safe to call repeatedly).
// E1: Intentionally does NOT call sub.Drain() or sub.Unsubscribe() on durable subscriptions:
// in nats.go v1.36, QueueSubscribe sets jsi.dc=true, so Drain/Unsubscribe deletes the shared
// durable consumer on the server. Closing nc in main detaches cleanly while preserving durables.
func (c *Consumer) Close() error {
	c.mu.Lock()
	c.stopping = true
	c.mu.Unlock()
	c.wg.Wait()
	return nil
}

// HandleMessage decodes, checks idempotency, updates the product review summary,
// retries transient failures with backoff, and routes poison messages to DLQ.
func (c *Consumer) HandleMessage(baseCtx context.Context, msg Message) {
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	c.mu.Lock()
	if c.stopping || errors.Is(baseCtx.Err(), context.Canceled) {
		c.mu.Unlock()
		// E1: Do NOT Ack or Nak while shutting down; AckWait redelivers cleanly without burning MaxDeliver.
		return
	}
	c.wg.Add(1)
	c.mu.Unlock()
	defer c.wg.Done()

	// Detached per-message bounded context so shutdown cancellation does not abort in-flight work.
	msgCtx, cancel := context.WithTimeout(context.WithoutCancel(baseCtx), c.effectiveMsgTimeout())
	defer cancel()

	carrier := map[string]string{}
	if tp := msg.Header("traceparent"); tp != "" {
		carrier["traceparent"] = tp
	}
	if ts := msg.Header("tracestate"); ts != "" {
		carrier["tracestate"] = ts
	}
	if len(carrier) > 0 {
		msgCtx = telemetry.ExtractTraceHeaders(msgCtx, carrier)
	}

	delivered := msg.NumDelivered()
	if delivered == 0 {
		delivered = 1
	}

	ev, err := decodeReviewEvent(msg)
	if err != nil {
		if c.isStopping() || errors.Is(baseCtx.Err(), context.Canceled) {
			return
		}
		if c.routeToDLQ(msgCtx, ev.EventID, msg.Subject(), msg.Data(), err.Error(), int(delivered)) {
			_ = msg.Ack()
			return
		}
		_ = msg.NakWithDelay(nakDelay(delivered))
		return
	}

	// Idempotency check before refreshing summary.
	if c.eventStore != nil {
		already, checkErr := c.eventStore.IsEventProcessed(msgCtx, ev.EventID)
		if checkErr != nil {
			c.retryOrDLQ(baseCtx, msgCtx, msg, ev.EventID, delivered, checkErr)
			return
		}
		if already {
			c.logger.DebugContext(msgCtx, "skipping duplicate review event",
				slog.String("event_id", ev.EventID),
				slog.String("subject", msg.Subject()),
			)
			_ = msg.Ack()
			return
		}
	}

	if c.refresher != nil {
		_, err = c.refresher.RefreshReviewSummary(msgCtx, ev.ProductID, ev.ShopID, []service.ReviewInput{
			{
				ReviewID: ev.ReviewID,
				Rating:   ev.Rating,
				Comment:  ev.Comment,
			},
		})
		if err != nil {
			c.retryOrDLQ(baseCtx, msgCtx, msg, ev.EventID, delivered, err)
			return
		}
	}

	if c.eventStore != nil {
		if _, markErr := c.eventStore.MarkEventProcessed(msgCtx, ev.EventID, msg.Subject()); markErr != nil {
			c.retryOrDLQ(baseCtx, msgCtx, msg, ev.EventID, delivered, fmt.Errorf("mark event processed: %w", markErr))
			return
		}
	}

	_ = msg.Ack()
}

func (c *Consumer) retryOrDLQ(baseCtx, msgCtx context.Context, msg Message, eventID string, delivered uint64, err error) {
	if c.isStopping() || errors.Is(baseCtx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		// E1: Never route to DLQ or Nak during shutdown cancellation.
		return
	}
	c.logger.ErrorContext(msgCtx, "review event processing failed",
		slog.String("subject", msg.Subject()),
		slog.String("event_id", eventID),
		slog.Uint64("attempt", delivered),
		slog.String("error", err.Error()),
	)
	if delivered >= c.maxAttempts {
		if c.routeToDLQ(msgCtx, eventID, msg.Subject(), msg.Data(), err.Error(), int(delivered)) {
			_ = msg.Ack()
			return
		}
		_ = msg.NakWithDelay(nakDelay(delivered))
		return
	}
	_ = msg.NakWithDelay(nakDelay(delivered))
}

// routeToDLQ publishes the poison message to NEXUSMART_DLQ and returns true iff durable publish succeeded.
func (c *Consumer) routeToDLQ(ctx context.Context, eventID, subject string, raw []byte, errMsg string, attempts int) bool {
	c.logger.WarnContext(ctx, "routing poison review message to DLQ",
		slog.String("event_id", eventID),
		slog.String("subject", subject),
		slog.Int("attempts", attempts),
		slog.String("error", errMsg),
	)
	if c.dlqPub == nil {
		c.logger.ErrorContext(ctx, "cannot route review event to DLQ: publisher not configured",
			slog.String("event_id", eventID),
			slog.String("subject", subject),
		)
		return false
	}
	dlqPayload, _ := json.Marshal(map[string]any{
		"event_id":         eventID,
		"original_subject": subject,
		"error":            errMsg,
		"attempts":         attempts,
		"failed_at":        time.Now().UTC().Format(time.RFC3339),
		"original_payload": string(raw),
	})
	dlqSubject := "dlq." + subject
	if err := c.dlqPub.PublishDLQ(dlqSubject, dlqPayload); err != nil {
		c.logger.ErrorContext(ctx, "failed to publish review event to NEXUSMART_DLQ",
			slog.String("dlq_subject", dlqSubject),
			slog.String("error", err.Error()),
		)
		return false
	}
	return true
}

func decodeReviewEvent(msg Message) (ReviewEventPayload, error) {
	var env eventEnvelope
	if err := json.Unmarshal(msg.Data(), &env); err != nil {
		return ReviewEventPayload{}, fmt.Errorf("malformed review event json: %w", err)
	}

	var ev ReviewEventPayload
	if len(env.Payload) > 0 && string(env.Payload) != "null" {
		if err := json.Unmarshal(env.Payload, &ev); err != nil {
			return ReviewEventPayload{}, fmt.Errorf("malformed review payload: %w", err)
		}
		if ev.EventID == "" {
			ev.EventID = env.EventID
		}
	} else {
		if err := json.Unmarshal(msg.Data(), &ev); err != nil {
			return ReviewEventPayload{}, fmt.Errorf("malformed review event: %w", err)
		}
	}

	ev.EventID = strings.TrimSpace(ev.EventID)
	if ev.EventID == "" {
		ev.EventID = strings.TrimSpace(msg.Header("Nats-Msg-Id"))
	}
	if ev.EventID == "" {
		ev.EventID = strings.TrimSpace(ev.ReviewID)
	}
	ev.ProductID = strings.TrimSpace(ev.ProductID)
	ev.ShopID = strings.TrimSpace(ev.ShopID)
	ev.ReviewID = strings.TrimSpace(ev.ReviewID)
	if ev.EventID == "" {
		return ev, errors.New("missing event_id")
	}
	if ev.ProductID == "" {
		return ev, errors.New("missing product_id")
	}
	if ev.Rating < 1 || ev.Rating > 5 {
		return ev, fmt.Errorf("invalid rating %d: must be between 1 and 5", ev.Rating)
	}
	return ev, nil
}

type natsMessageAdapter struct {
	msg *nats.Msg
}

func (a natsMessageAdapter) Subject() string { return a.msg.Subject }
func (a natsMessageAdapter) Data() []byte    { return a.msg.Data }
func (a natsMessageAdapter) Header(key string) string {
	if a.msg.Header == nil {
		return ""
	}
	return a.msg.Header.Get(key)
}
func (a natsMessageAdapter) NumDelivered() uint64 {
	meta, err := a.msg.Metadata()
	if err != nil || meta == nil {
		return 1
	}
	return meta.NumDelivered
}
func (a natsMessageAdapter) Ack() error                         { return a.msg.Ack() }
func (a natsMessageAdapter) NakWithDelay(d time.Duration) error { return a.msg.NakWithDelay(d) }
