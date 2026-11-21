package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/assistant/internal/service"
)

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
// so review.created and product.review_created subjects are captured.
func EnsureStreams(js nats.JetStreamContext, primaryStream, dlqStream string) error {
	if primaryStream == "" {
		primaryStream = "NEXUSMART"
	}
	if dlqStream == "" {
		dlqStream = "NEXUSMART_DLQ"
	}

	requiredSubjects := []string{"user.>", "order.>", "payment.>", "shipment.>", "product.>", "review.>"}
	if err := ensurePrimaryStream(js, primaryStream, requiredSubjects); err != nil {
		return fmt.Errorf("ensure stream %s: %w", primaryStream, err)
	}

	if err := ensureSingleStream(js, &nats.StreamConfig{
		Name:      dlqStream,
		Subjects:  []string{"dlq.>"},
		Retention: nats.LimitsPolicy,
		Storage:   nats.FileStorage,
	}); err != nil {
		return fmt.Errorf("ensure dlq stream %s: %w", dlqStream, err)
	}
	return nil
}

func ensurePrimaryStream(js nats.JetStreamContext, name string, requiredSubjects []string) error {
	info, err := js.StreamInfo(name)
	if err == nil && info != nil {
		existing := make(map[string]bool, len(info.Config.Subjects))
		for _, s := range info.Config.Subjects {
			existing[s] = true
		}
		updated := append([]string(nil), info.Config.Subjects...)
		changed := false
		for _, req := range requiredSubjects {
			if !existing[req] {
				updated = append(updated, req)
				changed = true
			}
		}
		if changed {
			cfg := info.Config
			cfg.Subjects = updated
			if _, updErr := js.UpdateStream(&cfg); updErr != nil {
				return updErr
			}
		}
		return nil
	}
	if !errors.Is(err, nats.ErrStreamNotFound) {
		return err
	}
	return ensureSingleStream(js, &nats.StreamConfig{
		Name:       name,
		Subjects:   requiredSubjects,
		Retention:  nats.LimitsPolicy,
		Storage:    nats.FileStorage,
		Duplicates: 2 * time.Minute,
	})
}

func ensureSingleStream(js nats.JetStreamContext, cfg *nats.StreamConfig) error {
	if _, err := js.StreamInfo(cfg.Name); err == nil {
		return nil
	} else if !errors.Is(err, nats.ErrStreamNotFound) {
		return err
	}
	if _, err := js.AddStream(cfg); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "already in use") {
			if _, infoErr := js.StreamInfo(cfg.Name); infoErr == nil {
				return nil
			}
		}
		return err
	}
	return nil
}

// Consumer consumes review creation events idempotently with DLQ routing on poison messages.
type Consumer struct {
	js          nats.JetStreamContext
	refresher   ReviewRefresher
	eventStore  EventStore
	dlqPub      DLQPublisher
	stream      string
	maxAttempts uint64
	logger      *slog.Logger
	subs        []*nats.Subscription
}

type jsDLQPublisher struct {
	js nats.JetStreamContext
}

func (p jsDLQPublisher) PublishDLQ(subject string, data []byte) error {
	msg := nats.NewMsg(subject)
	msg.Data = data
	_, err := p.js.PublishMsg(msg)
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
		logger:      logger,
	}
}

// Start registers durable JetStream queue subscriptions for review.created and product.review_created.
func (c *Consumer) Start(ctx context.Context) error {
	bindings := []struct {
		subject string
		durable string
	}{
		{subject: "review.created", durable: "assistant-review-summarizer"},
		{subject: "product.review_created", durable: "assistant-product-review-summarizer"},
	}

	for _, b := range bindings {
		sub, err := c.js.QueueSubscribe(
			b.subject,
			"assistant-review-workers",
			func(msg *nats.Msg) {
				c.HandleMessage(ctx, natsMessageAdapter{msg: msg})
			},
			nats.BindStream(c.stream),
			nats.Durable(b.durable),
			nats.ManualAck(),
			nats.AckWait(30*time.Second),
			nats.MaxDeliver(int(c.maxAttempts)+1),
		)
		if err != nil {
			_ = c.Close()
			return fmt.Errorf("subscribe %s on stream %s: %w", b.subject, c.stream, err)
		}
		c.subs = append(c.subs, sub)
	}
	return nil
}

// Close drains all active JetStream subscriptions gracefully.
func (c *Consumer) Close() error {
	var firstErr error
	for _, sub := range c.subs {
		if sub != nil {
			if err := sub.Drain(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	c.subs = nil
	return firstErr
}

// HandleMessage decodes, checks idempotency, updates the product review summary,
// retries transient failures with backoff, and routes poison messages to DLQ.
func (c *Consumer) HandleMessage(baseCtx context.Context, msg Message) {
	ctx := baseCtx
	carrier := map[string]string{}
	if tp := msg.Header("traceparent"); tp != "" {
		carrier["traceparent"] = tp
	}
	if ts := msg.Header("tracestate"); ts != "" {
		carrier["tracestate"] = ts
	}
	if len(carrier) > 0 {
		ctx = telemetry.ExtractTraceHeaders(ctx, carrier)
	}

	delivered := msg.NumDelivered()
	if delivered == 0 {
		delivered = 1
	}

	ev, err := decodeReviewEvent(msg)
	if err != nil {
		c.routeToDLQ(ctx, ev.EventID, msg.Subject(), msg.Data(), err.Error(), int(delivered))
		_ = msg.Ack()
		return
	}

	// Idempotency check before refreshing summary.
	if c.eventStore != nil {
		already, checkErr := c.eventStore.IsEventProcessed(ctx, ev.EventID)
		if checkErr != nil {
			c.retryOrDLQ(ctx, msg, ev.EventID, delivered, checkErr)
			return
		}
		if already {
			c.logger.DebugContext(ctx, "skipping duplicate review event",
				slog.String("event_id", ev.EventID),
				slog.String("subject", msg.Subject()),
			)
			_ = msg.Ack()
			return
		}
	}

	if c.refresher != nil {
		_, err = c.refresher.RefreshReviewSummary(ctx, ev.ProductID, ev.ShopID, []service.ReviewInput{
			{
				ReviewID: ev.ReviewID,
				Rating:   ev.Rating,
				Comment:  ev.Comment,
			},
		})
		if err != nil {
			c.retryOrDLQ(ctx, msg, ev.EventID, delivered, err)
			return
		}
	}

	if c.eventStore != nil {
		if _, markErr := c.eventStore.MarkEventProcessed(ctx, ev.EventID, msg.Subject()); markErr != nil {
			c.logger.WarnContext(ctx, "failed to record processed event id",
				slog.String("event_id", ev.EventID),
				slog.String("error", markErr.Error()),
			)
		}
	}

	_ = msg.Ack()
}

func (c *Consumer) retryOrDLQ(ctx context.Context, msg Message, eventID string, delivered uint64, err error) {
	c.logger.ErrorContext(ctx, "review event processing failed",
		slog.String("subject", msg.Subject()),
		slog.String("event_id", eventID),
		slog.Uint64("attempt", delivered),
		slog.String("error", err.Error()),
	)
	if delivered >= c.maxAttempts {
		c.routeToDLQ(ctx, eventID, msg.Subject(), msg.Data(), err.Error(), int(delivered))
		_ = msg.Ack()
		return
	}
	_ = msg.NakWithDelay(time.Duration(delivered) * time.Second)
}

func (c *Consumer) routeToDLQ(ctx context.Context, eventID, subject string, raw []byte, errMsg string, attempts int) {
	c.logger.WarnContext(ctx, "routing poison review message to DLQ",
		slog.String("event_id", eventID),
		slog.String("subject", subject),
		slog.Int("attempts", attempts),
		slog.String("error", errMsg),
	)
	if c.dlqPub == nil {
		return
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
	}
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
		ev.Rating = 5
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
