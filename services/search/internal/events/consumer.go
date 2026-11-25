package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nexusmart/core/pkg/common/jetstream"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/search/internal/db"
	"github.com/nexusmart/core/services/search/internal/indexer"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	// S13: Up to 20 retries with linear-capped backoff min(2s*n, 30s) survives multi-minute
	// Meilisearch/Postgres restarts without prematurely dumping transient failures into DLQ.
	defaultMaxDeliverAttempts = 20
	nakBackoffBase            = 2 * time.Second
	nakBackoffMax             = 30 * time.Second
	// K2: Handlers run sequentially one message at a time with processTimeout = 25s.
	// Keep maxAckPending * processTimeout < ackWait (4 * 25s = 100s < 120s) so messages
	// buffered in the client never expire AckWait and burn MaxDeliver during outages.
	maxAckPending  = 4
	processTimeout = 25 * time.Second
	ackWait        = 120 * time.Second
)

func nakDelay(numDelivered uint64) time.Duration {
	if numDelivered == 0 {
		numDelivered = 1
	}
	if numDelivered >= 15 {
		return nakBackoffMax
	}
	d := time.Duration(numDelivered) * nakBackoffBase
	if d > nakBackoffMax {
		return nakBackoffMax
	}
	return d
}

// EventHandler processes product and user events consumed by the search service.
type EventHandler interface {
	HandleProductUpserted(ctx context.Context, eventID string, in indexer.ProductUpsertInput) error
	HandleProductDeleted(ctx context.Context, eventID, productID, shopID string, updatedAt time.Time) error
	HandleCategoryUpdated(ctx context.Context, eventID string, in indexer.CategoryUpdateInput) error
	HandleShopReviewed(ctx context.Context, eventID string, in indexer.ShopReviewedInput) error
	HandleShopStatusChanged(ctx context.Context, eventID string, in indexer.ShopStatusChangedInput) error
}

// DeadLetterStore persists dead-lettered messages locally alongside NEXUSMART_DLQ.
type DeadLetterStore interface {
	InsertDeadLetterEvent(ctx context.Context, arg db.InsertDeadLetterEventParams) error
}

// SubscriptionSpec configures one JetStream durable queue subscription.
type SubscriptionSpec struct {
	Subject string
	Durable string
	Queue   string
}

// Consumer subscribes to product.> and user.> with E1 shutdown/DLQ hygiene, idempotency,
// and out-of-order / version protections (G10/G11).
type Consumer struct {
	js          nats.JetStreamContext
	dlqPub      Publisher
	dlqStore    DeadLetterStore
	handler     EventHandler
	logger      *slog.Logger
	streamName  string
	maxAttempts uint64
	specs       []SubscriptionSpec

	mu       sync.Mutex
	stopping bool
	wg       sync.WaitGroup
}

// NewConsumer builds an E1-compliant JetStream consumer for search.
func NewConsumer(
	js nats.JetStreamContext,
	dlqPub Publisher,
	dlqStore DeadLetterStore,
	handler EventHandler,
	logger *slog.Logger,
	streamName string,
) *Consumer {
	if logger == nil {
		logger = slog.Default()
	}
	if strings.TrimSpace(streamName) == "" {
		streamName = jetstream.DefaultPrimaryStream
	}
	return &Consumer{
		js:          js,
		dlqPub:      dlqPub,
		dlqStore:    dlqStore,
		handler:     handler,
		logger:      logger,
		streamName:  streamName,
		maxAttempts: defaultMaxDeliverAttempts,
		specs: []SubscriptionSpec{
			{Subject: "product.>", Durable: DefaultProductDurable, Queue: DefaultProductQueue},
			{Subject: "user.>", Durable: DefaultUserDurable, Queue: DefaultUserQueue},
		},
	}
}

// SetMaxAttempts overrides the retry threshold before DLQ routing (must be called before Start).
func (c *Consumer) SetMaxAttempts(n uint64) {
	if n > 0 {
		c.maxAttempts = n
	}
}

// SetSubscriptions overrides default durable subscription specs (useful in isolated NATS tests).
func (c *Consumer) SetSubscriptions(specs []SubscriptionSpec) {
	if len(specs) > 0 {
		c.specs = append([]SubscriptionSpec(nil), specs...)
	}
}

// Start registers durable JetStream queue subscriptions for product.> and user.>.
func (c *Consumer) Start(ctx context.Context) error {
	c.mu.Lock()
	c.stopping = false
	c.mu.Unlock()

	for _, sp := range c.specs {
		_, err := c.js.QueueSubscribe(
			sp.Subject,
			sp.Queue,
			func(msg *nats.Msg) {
				c.onMessage(ctx, msg)
			},
			nats.BindStream(c.streamName),
			nats.Durable(sp.Durable),
			nats.ManualAck(),
			nats.AckExplicit(),
			nats.AckWait(ackWait),
			nats.MaxDeliver(int(c.maxAttempts)+1),
			nats.MaxAckPending(maxAckPending),
			nats.DeliverAll(),
		)
		if err != nil {
			_ = c.Stop()
			return fmt.Errorf("subscribe %s (%s): %w", sp.Subject, sp.Durable, err)
		}
	}
	return nil
}

func (c *Consumer) isStopping() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stopping
}

func (c *Consumer) onMessage(ctx context.Context, msg *nats.Msg) {
	c.mu.Lock()
	if c.stopping || ctx.Err() != nil {
		c.mu.Unlock()
		// E1: Do NOT Nak while shutting down; AckWait redelivers cleanly without burning MaxDeliver.
		return
	}
	c.wg.Add(1)
	c.mu.Unlock()
	defer c.wg.Done()
	c.handleNATSMessage(ctx, msg)
}

// Stop prevents new callbacks and waits for every in-flight handler to finish (safe to call repeatedly).
// E1: Intentionally does NOT call sub.Drain() or sub.Unsubscribe() on durable consumers.
func (c *Consumer) Stop() error {
	c.mu.Lock()
	c.stopping = true
	c.mu.Unlock()
	c.wg.Wait()
	return nil
}

// DeliveryAction describes the ack/nak/dlq outcome of processing a raw message.
type DeliveryAction int

const (
	ActionAck DeliveryAction = iota
	ActionNak
	ActionDLQ
)

// ProcessRawMessage decodes, traces, dispatches, and routes poison messages to DLQ.
func (c *Consumer) ProcessRawMessage(
	ctx context.Context,
	subject string,
	data []byte,
	headers map[string]string,
	numDelivered uint64,
) (DeliveryAction, error) {
	if numDelivered == 0 {
		numDelivered = 1
	}
	if errors.Is(ctx.Err(), context.Canceled) || c.isStopping() {
		return ActionNak, ctx.Err()
	}

	if len(headers) > 0 {
		ctx = telemetry.ExtractTraceHeaders(ctx, headers)
	}
	tracer := otel.Tracer("search")
	ctx, span := tracer.Start(ctx, "nats.consume "+subject,
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(attribute.String("messaging.destination", subject)),
	)
	defer span.End()

	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		if errors.Is(ctx.Err(), context.Canceled) || c.isStopping() {
			return ActionNak, err
		}
		if c.sendToDLQ(ctx, subject, "", data, fmt.Sprintf("malformed event envelope: %v", err), numDelivered) {
			return ActionDLQ, err
		}
		return ActionNak, err
	}
	if strings.TrimSpace(env.EventID) == "" {
		err := fmt.Errorf("missing event_id on subject %s", subject)
		if errors.Is(ctx.Err(), context.Canceled) || c.isStopping() {
			return ActionNak, err
		}
		if c.sendToDLQ(ctx, subject, "", data, err.Error(), numDelivered) {
			return ActionDLQ, err
		}
		return ActionNak, err
	}

	eventType := strings.TrimSpace(env.EventType)
	if eventType == "" {
		eventType = subject
	}

	handleErr := c.dispatch(ctx, eventType, env.EventID, env.OccurredAt, env.Payload)
	if handleErr == nil {
		return ActionAck, nil
	}
	if errors.Is(ctx.Err(), context.Canceled) || c.isStopping() {
		return ActionNak, handleErr
	}

	c.logger.WarnContext(ctx, "event handler failed",
		"subject", subject,
		"event_type", eventType,
		"event_id", env.EventID,
		"attempt", numDelivered,
		"error", handleErr.Error(),
	)

	if errors.Is(handleErr, indexer.ErrPermanentPayload) || numDelivered >= c.maxAttempts {
		if c.sendToDLQ(ctx, subject, env.EventID, data, handleErr.Error(), numDelivered) {
			return ActionDLQ, handleErr
		}
		return ActionNak, handleErr
	}
	return ActionNak, handleErr
}

func (c *Consumer) handleNATSMessage(baseCtx context.Context, msg *nats.Msg) {
	if baseCtx.Err() != nil || c.isStopping() {
		return
	}

	var numDelivered uint64 = 1
	if meta, err := msg.Metadata(); err == nil && meta != nil && meta.NumDelivered > 0 {
		numDelivered = meta.NumDelivered
	}

	headers := make(map[string]string, len(msg.Header))
	for k, vals := range msg.Header {
		if len(vals) > 0 {
			headers[strings.ToLower(k)] = vals[0]
		}
	}

	procCtx, cancel := context.WithTimeout(baseCtx, processTimeout)
	defer cancel()

	action, _ := c.ProcessRawMessage(procCtx, msg.Subject, msg.Data, headers, numDelivered)
	if baseCtx.Err() != nil || c.isStopping() {
		if action == ActionAck {
			if err := msg.AckSync(); err != nil {
				c.logger.WarnContext(procCtx, "failed to ack NATS message", "subject", msg.Subject, "error", err)
			}
		}
		return
	}
	switch action {
	case ActionAck, ActionDLQ:
		if err := msg.AckSync(); err != nil {
			c.logger.WarnContext(procCtx, "failed to ack NATS message", "subject", msg.Subject, "error", err)
		}
	case ActionNak:
		delay := nakDelay(numDelivered)
		if err := msg.NakWithDelay(delay); err != nil {
			c.logger.WarnContext(procCtx, "failed to nak NATS message", "subject", msg.Subject, "error", err)
		}
	}
}

func (c *Consumer) dispatch(ctx context.Context, eventType, eventID string, occurredAt time.Time, rawPayload json.RawMessage) error {
	switch eventType {
	case SubjectProductUpserted:
		var p ProductUpsertedPayload
		if err := json.Unmarshal(rawPayload, &p); err != nil {
			return fmt.Errorf("%w: unmarshal product.upserted payload: %w", indexer.ErrPermanentPayload, err)
		}
		ts, err := parseTimestamp(p.UpdatedAt, occurredAt)
		if err != nil {
			return fmt.Errorf("%w: invalid updated_at in product.upserted: %w", indexer.ErrPermanentPayload, err)
		}
		return c.handler.HandleProductUpserted(ctx, eventID, indexer.ProductUpsertInput{
			ProductID:           p.ProductID,
			ShopID:              p.ShopID,
			CategoryID:          p.CategoryID,
			CategoryAncestorIDs: p.CategoryAncestorIDs,
			Name:                p.Name,
			Slug:                p.Slug,
			Description:         p.Description,
			ThumbnailURL:        p.ThumbnailURL,
			MinPriceVND:         p.MinPriceVND,
			MaxPriceVND:         p.MaxPriceVND,
			Status:              p.Status,
			SKUCodes:            p.SKUCodes,
			SKUAttributesText:   p.SKUAttributesText,
			TotalStock:          p.TotalStock,
			SKUCount:            p.SKUCount,
			SKUIDs:              p.SKUIDs,
			DefaultSKUID:        p.DefaultSKUID,
			Colors:              p.Colors,
			ColorCodes:          p.ColorCodes,
			Sizes:               p.Sizes,
			RatingAvgX100:       p.RatingAvgX100,
			ReviewCount:         p.ReviewCount,
			ShopSuspended:       p.ShopSuspended,
			UpdatedAt:           ts,
		})

	case SubjectProductDeleted:
		var p ProductDeletedPayload
		if err := json.Unmarshal(rawPayload, &p); err != nil {
			return fmt.Errorf("%w: unmarshal product.deleted payload: %w", indexer.ErrPermanentPayload, err)
		}
		ts, err := parseTimestamp(p.UpdatedAt, occurredAt)
		if err != nil {
			return fmt.Errorf("%w: invalid updated_at in product.deleted: %w", indexer.ErrPermanentPayload, err)
		}
		return c.handler.HandleProductDeleted(ctx, eventID, p.ProductID, p.ShopID, ts)

	case SubjectProductCategoryUpdated:
		var p ProductCategoryUpdatedPayload
		if err := json.Unmarshal(rawPayload, &p); err != nil {
			return fmt.Errorf("%w: unmarshal product.category_updated payload: %w", indexer.ErrPermanentPayload, err)
		}
		ts, err := parseTimestamp(p.UpdatedAt, occurredAt)
		if err != nil {
			return fmt.Errorf("%w: invalid updated_at in product.category_updated: %w", indexer.ErrPermanentPayload, err)
		}
		return c.handler.HandleCategoryUpdated(ctx, eventID, indexer.CategoryUpdateInput{
			CategoryID: p.CategoryID,
			Name:       p.Name,
			Slug:       p.Slug,
			ParentID:   p.ParentID,
			SortOrder:  p.SortOrder,
			Action:     p.Action,
			UpdatedAt:  ts,
		})

	case SubjectUserShopReviewed:
		var p ShopReviewedPayload
		if err := json.Unmarshal(rawPayload, &p); err != nil {
			return fmt.Errorf("%w: unmarshal user.shop_reviewed payload: %w", indexer.ErrPermanentPayload, err)
		}
		return c.handler.HandleShopReviewed(ctx, eventID, indexer.ShopReviewedInput{
			ShopID:      p.ShopID,
			OwnerUserID: p.OwnerUserID,
			ShopName:    p.ShopName,
			Decision:    p.Decision,
			Reason:      p.Reason,
		})

	case SubjectUserShopStatusChanged:
		var p ShopStatusChangedPayload
		if err := json.Unmarshal(rawPayload, &p); err != nil {
			return fmt.Errorf("%w: unmarshal user.shop_status_changed payload: %w", indexer.ErrPermanentPayload, err)
		}
		ts, err := parseTimestamp(p.OccurredAt, occurredAt)
		if err != nil {
			return fmt.Errorf("%w: invalid occurred_at in user.shop_status_changed: %w", indexer.ErrPermanentPayload, err)
		}
		return c.handler.HandleShopStatusChanged(ctx, eventID, indexer.ShopStatusChangedInput{
			ShopID:     p.ShopID,
			ShopName:   p.ShopName,
			FromStatus: p.FromStatus,
			ToStatus:   p.ToStatus,
			Version:    p.Version,
			OccurredAt: ts,
		})

	default:
		// Safely ignore unrelated subjects on product.> and user.> (e.g. product.reviewed,
		// user.registered, user.phone_otp_requested, user.password_reset_requested).
		return nil
	}
}

func parseTimestamp(raw string, fallback time.Time) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if !fallback.IsZero() {
			return fallback.UTC(), nil
		}
		return time.Now().UTC(), nil
	}
	if ts, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return ts.UTC(), nil
	}
	ts, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, err
	}
	return ts.UTC(), nil
}

// sendToDLQ persists the poison message to dead_letter_events and/or NEXUSMART_DLQ using a
// detached 5s context. Returns true iff at least one durable DLQ sink succeeded (E1).
func (c *Consumer) sendToDLQ(ctx context.Context, subject, eventID string, payload []byte, errMsg string, attempts uint64) bool {
	c.logger.ErrorContext(ctx, "routing poison message to DLQ",
		"subject", subject,
		"event_id", eventID,
		"attempts", attempts,
		"error", errMsg,
	)

	dlqCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	saved := false
	if c.dlqStore != nil {
		if err := c.dlqStore.InsertDeadLetterEvent(dlqCtx, db.InsertDeadLetterEventParams{
			ID:           uuid.NewString(),
			Subject:      subject,
			EventID:      eventID,
			Payload:      payload,
			ErrorMessage: errMsg,
			Attempts:     int32(attempts),
		}); err != nil {
			c.logger.ErrorContext(dlqCtx, "failed to persist dead_letter_event", "subject", subject, "error", err)
		} else {
			saved = true
		}
	}

	if c.dlqPub != nil {
		dlqMsg := &nats.Msg{
			Subject: "dlq." + subject,
			Data:    payload,
			Header:  nats.Header{},
		}
		dlqMsg.Header.Set("X-DLQ-Original-Subject", subject)
		dlqMsg.Header.Set("X-DLQ-Error", errMsg)
		dlqMsg.Header.Set("X-DLQ-Attempts", strconv.FormatUint(attempts, 10))
		if eventID != "" {
			dlqMsg.Header.Set("X-DLQ-Event-Id", eventID)
		}
		if _, err := c.dlqPub.PublishMsg(dlqMsg, nats.Context(dlqCtx)); err != nil {
			c.logger.ErrorContext(dlqCtx, "failed to publish message to NATS DLQ", "dlq_subject", dlqMsg.Subject, "error", err)
		} else {
			saved = true
		}
	}
	return saved
}
