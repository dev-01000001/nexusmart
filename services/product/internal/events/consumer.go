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
	"github.com/nexusmart/core/services/product/internal/store"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	defaultMaxDeliverAttempts = 5

	// DefaultShopStatusDurable is the canonical durable consumer name on NEXUSMART for shop status events.
	DefaultShopStatusDurable = "product-shop-status-consumer"
	// DefaultShopStatusQueue is the canonical JetStream queue group name for product replicas.
	DefaultShopStatusQueue = "product-shop-status-workers"
)

// EventHandler handles domain events consumed by the product service.
type EventHandler interface {
	HandleShopStatusChanged(ctx context.Context, eventID string, payload ShopStatusChangedPayload) error
}

// DeadLetterStore records dead-lettered messages locally alongside NATS DLQ stream publishing.
type DeadLetterStore interface {
	InsertDeadLetterEvent(ctx context.Context, ev store.DeadLetterEvent) error
}

// ShopStatusHandler persists user.shop_status_changed transitions atomically with idempotency
// (processed_events) and monotonic version gating (shop_suspension_state, F12).
type ShopStatusHandler struct {
	store  store.Store
	logger *slog.Logger
}

// NewShopStatusHandler builds a store-backed handler for user.shop_status_changed events.
func NewShopStatusHandler(st store.Store, logger *slog.Logger) *ShopStatusHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &ShopStatusHandler{
		store:  st,
		logger: logger,
	}
}

// HandleShopStatusChanged applies a shop status transition idempotently inside a single DB transaction.
// Duplicate event_ids and stale/equal versions (version <= stored version) are safely ignored.
func (h *ShopStatusHandler) HandleShopStatusChanged(ctx context.Context, eventID string, payload ShopStatusChangedPayload) error {
	shopID := strings.TrimSpace(payload.ShopID)
	if shopID == "" {
		return errors.New("missing shop_id in user.shop_status_changed payload")
	}
	suspended, err := parseShopStatusSuspended(payload.ToStatus)
	if err != nil {
		return err
	}
	if payload.Version <= 0 {
		return fmt.Errorf("invalid non-positive version %d in user.shop_status_changed payload (must be > 0)", payload.Version)
	}

	var (
		duplicate bool
		stale     bool
	)
	err = h.store.WithTx(ctx, func(q store.Queries) error {
		if lockErr := q.LockShopSuspension(ctx, shopID); lockErr != nil {
			return lockErr
		}
		inserted, procErr := q.TryInsertProcessedEvent(ctx, eventID, SubjectShopStatusChanged)
		if procErr != nil {
			return procErr
		}
		if !inserted {
			duplicate = true
			return nil
		}
		_, applied, upErr := q.UpsertShopSuspensionState(ctx, shopID, suspended, payload.Version)
		if upErr != nil {
			return upErr
		}
		if !applied {
			stale = true
			return nil
		}
		return q.SetProductsShopSuspendedByShop(ctx, shopID, suspended)
	})
	if err != nil {
		return err
	}
	if duplicate {
		h.logger.DebugContext(ctx, "ignored duplicate user.shop_status_changed event",
			slog.String("event_id", eventID),
			slog.String("shop_id", shopID),
		)
	} else if stale {
		h.logger.InfoContext(ctx, "ignored stale user.shop_status_changed event",
			slog.String("event_id", eventID),
			slog.String("shop_id", shopID),
			slog.Int64("version", payload.Version),
		)
	}
	return nil
}

func parseShopStatusSuspended(raw string) (bool, error) {
	norm := strings.ToUpper(strings.TrimSpace(raw))
	norm = strings.TrimPrefix(norm, "SHOP_STATUS_")
	switch norm {
	case "SUSPENDED":
		return true, nil
	case "ACTIVE", "PENDING", "REJECTED":
		return false, nil
	default:
		return false, fmt.Errorf("unknown to_status %q in user.shop_status_changed payload", raw)
	}
}

// Consumer subscribes to user.shop_status_changed with E1 shutdown/DLQ guarantees and idempotency.
type Consumer struct {
	js          nats.JetStreamContext
	dlqPub      Publisher
	dlqStore    DeadLetterStore
	handler     EventHandler
	logger      *slog.Logger
	streamName  string
	subject     string
	durable     string
	queue       string
	maxAttempts uint64
	mu          sync.Mutex
	stopping    bool
	wg          sync.WaitGroup
}

// NewConsumer creates an idempotent NATS JetStream consumer matching services/order/internal/events/consumer.go (E1).
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
		subject:     SubjectShopStatusChanged,
		durable:     DefaultShopStatusDurable,
		queue:       DefaultShopStatusQueue,
		maxAttempts: defaultMaxDeliverAttempts,
	}
}

// SetMaxAttempts overrides the retry threshold before DLQ routing (must be called before Start; useful in tests).
func (c *Consumer) SetMaxAttempts(n uint64) {
	if n > 0 {
		c.maxAttempts = n
	}
}

// SetSubscription overrides subject/durable/queue names (must be called before Start; useful for isolated NATS tests).
func (c *Consumer) SetSubscription(subject, durable, queue string) {
	if strings.TrimSpace(subject) != "" {
		c.subject = strings.TrimSpace(subject)
	}
	if strings.TrimSpace(durable) != "" {
		c.durable = strings.TrimSpace(durable)
	}
	if strings.TrimSpace(queue) != "" {
		c.queue = strings.TrimSpace(queue)
	}
}

// Start registers the JetStream queue subscription for user.shop_status_changed.
func (c *Consumer) Start(ctx context.Context) error {
	c.mu.Lock()
	c.stopping = false
	c.mu.Unlock()

	_, err := c.js.QueueSubscribe(
		c.subject,
		c.queue,
		func(msg *nats.Msg) {
			c.onMessage(ctx, msg)
		},
		nats.BindStream(c.streamName),
		nats.Durable(c.durable),
		nats.ManualAck(),
		nats.AckExplicit(),
		nats.AckWait(30*time.Second),
		nats.MaxDeliver(int(c.maxAttempts)+1),
		nats.DeliverAll(),
	)
	if err != nil {
		_ = c.Stop()
		return fmt.Errorf("subscribe %s (%s): %w", c.subject, c.durable, err)
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
		// E1: Do NOT Nak while shutting down — AckWait redelivers cleanly without burning MaxDeliver.
		return
	}
	c.wg.Add(1)
	c.mu.Unlock()
	defer c.wg.Done()
	c.handleNATSMessage(ctx, msg)
}

// Stop prevents new message callbacks from entering and waits for every in-flight message
// handler to finish before returning (safe to call multiple times).
// E1: Intentionally does NOT call sub.Drain() or sub.Unsubscribe() because nats.go deletes
// library-created durable consumers on Drain/Unsubscribe, causing stream replay on restart.
func (c *Consumer) Stop() error {
	c.mu.Lock()
	c.stopping = true
	c.mu.Unlock()
	c.wg.Wait()
	return nil
}

// DeliveryAction indicates what the caller should do with a NATS message.
type DeliveryAction int

const (
	ActionAck DeliveryAction = iota
	ActionNak
	ActionDLQ
)

// ProcessRawMessage decodes, traces, dispatches, and routes poison messages to DLQ.
// Exposed for direct unit testing without requiring a live NATS server.
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
	tracer := otel.Tracer("product")
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

	handleErr := c.dispatch(ctx, eventType, env.EventID, env.Payload)
	if handleErr == nil {
		return ActionAck, nil
	}
	// E1: Never route to DLQ when shutdown cancelled the context; return ActionNak for redelivery.
	if errors.Is(ctx.Err(), context.Canceled) || c.isStopping() {
		return ActionNak, handleErr
	}

	c.logger.WarnContext(ctx, "event handler failed",
		"subject", subject,
		"event_type", eventType,
		"event_id", env.EventID,
		"attempt", numDelivered,
		"error", handleErr,
	)

	if numDelivered >= c.maxAttempts {
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

	procCtx, cancel := context.WithTimeout(baseCtx, 25*time.Second)
	defer cancel()

	action, _ := c.ProcessRawMessage(procCtx, msg.Subject, msg.Data, headers, numDelivered)
	if baseCtx.Err() != nil || c.isStopping() {
		// E1: Context cancelled during processing (shutdown) — only Ack if handler committed cleanly; never Nak.
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
		delay := time.Duration(numDelivered) * 500 * time.Millisecond
		if err := msg.NakWithDelay(delay); err != nil {
			c.logger.WarnContext(procCtx, "failed to nak NATS message", "subject", msg.Subject, "error", err)
		}
	}
}

func (c *Consumer) dispatch(ctx context.Context, eventType, eventID string, rawPayload json.RawMessage) error {
	switch eventType {
	case SubjectShopStatusChanged:
		var p ShopStatusChangedPayload
		if err := json.Unmarshal(rawPayload, &p); err != nil {
			return fmt.Errorf("unmarshal user.shop_status_changed payload: %w", err)
		}
		return c.handler.HandleShopStatusChanged(ctx, eventID, p)
	default:
		// Ignore unrelated subjects safely.
		return nil
	}
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
		if err := c.dlqStore.InsertDeadLetterEvent(dlqCtx, store.DeadLetterEvent{
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
