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
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/nexusmart/core/pkg/common/jetstream"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/promotion/internal/store"
)

const (
	SubjectOrderCompleted = "order.completed"
	DurableConsumerName   = "promotion-order-completed"
	QueueGroupName        = "promotion-order-completed-workers"
	DefaultDLQStream      = "NEXUSMART_DLQ"
	MaxDeliveryAttempts   = 5
	JetStreamMaxDeliver   = MaxDeliveryAttempts + 1

	defaultConsumerMsgTimeout = 25 * time.Second
	defaultConsumerAckWait    = 30 * time.Second
	defaultConsumerNakDelay   = 500 * time.Millisecond
	defaultDLQWriteTimeout    = 5 * time.Second
	defaultMigrationTimeout   = 10 * time.Second
)

// ErrConsumerStopping is returned when message processing is aborted because the consumer is stopping.
var ErrConsumerStopping = errors.New("order completed consumer is stopping")

// DeliveryAction indicates what the NATS handler should do with a delivered message.
type DeliveryAction int

const (
	ActionAck DeliveryAction = iota
	ActionNak
	ActionDLQ
)

// Envelope matches the canonical NexusMart NATS JetStream JSON envelope (docs/contracts.md Section 5).
type Envelope struct {
	EventID    string          `json:"event_id"`
	EventType  string          `json:"event_type"`
	TraceID    string          `json:"trace_id"`
	OccurredAt string          `json:"occurred_at"`
	Payload    json.RawMessage `json:"payload"`
}

// OrderCompletedPayload matches the payload schema of `order.completed`.
type OrderCompletedPayload struct {
	OrderID         string `json:"order_id"`
	OrderCode       string `json:"order_code"`
	BuyerID         string `json:"buyer_id"`
	ShopID          string `json:"shop_id"`
	SubtotalVND     int64  `json:"subtotal_vnd"`
	CommissionVND   int64  `json:"commission_vnd"`
	TotalPayableVND int64  `json:"total_payable_vnd"`
	CoinsReward     int64  `json:"coins_reward"`
}

// DLQPublisher publishes unprocessable messages to a dead letter subject.
type DLQPublisher interface {
	PublishDLQ(ctx context.Context, subject string, payload []byte, headers map[string]string) error
}

// OrderCompletedConsumer consumes `order.completed` from NATS JetStream and idempotently credits reward coins.
type OrderCompletedConsumer struct {
	store           store.Store
	logger          *slog.Logger
	streamName      string
	js              nats.JetStreamContext
	dlq             DLQPublisher
	dlqStore        DeadLetterStore
	migrationLocker consumerMigrationLocker
	maxAttempts     uint64
	ackWait         time.Duration
	nakBaseDelay    time.Duration
	mu              sync.Mutex
	stopping        bool
	wg              sync.WaitGroup
}

// NewOrderCompletedConsumer builds a JetStream consumer for `order.completed`.
func NewOrderCompletedConsumer(st store.Store, js nats.JetStreamContext, streamName string, logger *slog.Logger) *OrderCompletedConsumer {
	if logger == nil {
		logger = slog.Default()
	}
	if strings.TrimSpace(streamName) == "" {
		streamName = "NEXUSMART"
	}
	c := &OrderCompletedConsumer{
		store:        st,
		logger:       logger,
		streamName:   streamName,
		js:           js,
		maxAttempts:  MaxDeliveryAttempts,
		ackWait:      defaultConsumerAckWait,
		nakBaseDelay: defaultConsumerNakDelay,
	}
	if js != nil {
		c.dlq = &jetStreamDLQ{js: js}
	}
	return c
}

// SetDLQPublisher overrides the NATS DLQ publisher (useful for unit tests).
func (c *OrderCompletedConsumer) SetDLQPublisher(pub DLQPublisher) {
	c.dlq = pub
}

// SetDeadLetterStore configures the PostgreSQL dead_letter_events sink (and optional migration lock).
func (c *OrderCompletedConsumer) SetDeadLetterStore(dlqStore DeadLetterStore) {
	c.dlqStore = dlqStore
	if locker, ok := dlqStore.(consumerMigrationLocker); ok {
		c.migrationLocker = locker
	} else {
		c.migrationLocker = nil
	}
}

// EnsureStreams ensures the primary NEXUSMART stream (with canonical subjects union-merged)
// and NEXUSMART_DLQ stream exist on JetStream.
func (c *OrderCompletedConsumer) EnsureStreams() error {
	if c.js == nil {
		return nil
	}
	return jetstream.EnsureStreams(c.js, c.streamName, DefaultDLQStream)
}

// Start registers a durable JetStream queue subscription on `order.completed`.
func (c *OrderCompletedConsumer) Start(ctx context.Context) error {
	if c.js == nil {
		return nil
	}
	if err := c.EnsureStreams(); err != nil {
		return err
	}
	if err := c.ensureQueueConsumer(ctx); err != nil {
		return err
	}

	c.mu.Lock()
	c.stopping = false
	c.mu.Unlock()

	_, err := c.js.QueueSubscribe(
		SubjectOrderCompleted,
		QueueGroupName,
		func(msg *nats.Msg) {
			c.onMessage(ctx, msg)
		},
		nats.Bind(c.streamName, DurableConsumerName),
		nats.ManualAck(),
	)
	if err != nil {
		_ = c.Stop()
		return fmt.Errorf("queue subscribe %s (%s): %w", SubjectOrderCompleted, DurableConsumerName, err)
	}
	c.logger.Info("subscribed to order.completed jetstream queue consumer",
		slog.String("stream", c.streamName),
		slog.String("durable", DurableConsumerName),
		slog.String("queue", QueueGroupName),
		slog.Int("max_deliver", int(c.maxAttempts)+1),
	)
	return nil
}

// ensureQueueConsumer ensures the durable consumer exists with QueueGroupName and MaxDeliver headroom.
// If an older non-queue durable consumer (DeliverGroup == "") exists from pre-3b-1 deployments,
// it is safely recreated at AckFloor.Stream + 1 so already-acked messages are not replayed and
// unacked/pending messages are not lost.
func (c *OrderCompletedConsumer) ensureQueueConsumer(ctx context.Context) error {
	desiredMaxDeliver := int(c.maxAttempts) + 1
	if desiredMaxDeliver < 2 {
		desiredMaxDeliver = 2
	}

	opCtx, cancel := context.WithTimeout(ctx, defaultMigrationTimeout)
	defer cancel()

	info, err := c.js.ConsumerInfo(c.streamName, DurableConsumerName, nats.Context(opCtx))
	if err != nil {
		if !errors.Is(err, nats.ErrConsumerNotFound) {
			return fmt.Errorf("inspect consumer %s: %w", DurableConsumerName, err)
		}
		return c.createQueueConsumer(opCtx, nats.DeliverAllPolicy, 0, desiredMaxDeliver)
	}

	if info.Config.DeliverGroup == "" {
		// NATS JetStream 2.10 returns nil on UpdateConsumer when adding DeliverGroup to a push
		// consumer, but does not re-bind queue interest (PushBound remains false and delivery stalls).
		// Recreate the durable consumer from AckFloor.Stream + 1 (or DeliverAll when AckFloor == 0)
		// to preserve exact durable naming without replaying acked messages or losing pending ones.
		return c.migrateLegacyConsumer(opCtx, desiredMaxDeliver)
	}
	if info.Config.DeliverGroup != QueueGroupName {
		return fmt.Errorf("existing durable consumer %q has incompatible deliver_group %q (expected %q)",
			DurableConsumerName, info.Config.DeliverGroup, QueueGroupName)
	}
	return c.syncQueueConsumerConfig(opCtx, info, desiredMaxDeliver)
}

func (c *OrderCompletedConsumer) migrateLegacyConsumer(ctx context.Context, desiredMaxDeliver int) error {
	doMigrate := func(mCtx context.Context) error {
		info, err := c.js.ConsumerInfo(c.streamName, DurableConsumerName, nats.Context(mCtx))
		if err != nil {
			if errors.Is(err, nats.ErrConsumerNotFound) {
				return c.createQueueConsumer(mCtx, nats.DeliverAllPolicy, 0, desiredMaxDeliver)
			}
			return fmt.Errorf("re-inspect consumer %s during migration: %w", DurableConsumerName, err)
		}
		if info.Config.DeliverGroup == QueueGroupName {
			return c.syncQueueConsumerConfig(mCtx, info, desiredMaxDeliver)
		}
		if info.Config.DeliverGroup != "" {
			return fmt.Errorf("existing durable consumer %q has incompatible deliver_group %q (expected %q)",
				DurableConsumerName, info.Config.DeliverGroup, QueueGroupName)
		}

		deliverPolicy := nats.DeliverAllPolicy
		var optStartSeq uint64
		if info.AckFloor.Stream > 0 {
			deliverPolicy = nats.DeliverByStartSequencePolicy
			optStartSeq = info.AckFloor.Stream + 1
		}

		c.logger.InfoContext(mCtx, "migrating legacy non-queue durable consumer to queue group",
			slog.String("stream", c.streamName),
			slog.String("durable", DurableConsumerName),
			slog.String("deliver_group", QueueGroupName),
			slog.Uint64("ack_floor_stream", info.AckFloor.Stream),
			slog.Uint64("opt_start_seq", optStartSeq),
			slog.Int("max_deliver", desiredMaxDeliver),
		)

		if err := c.js.DeleteConsumer(c.streamName, DurableConsumerName, nats.Context(mCtx)); err != nil && !errors.Is(err, nats.ErrConsumerNotFound) {
			return fmt.Errorf("delete legacy consumer %s: %w", DurableConsumerName, err)
		}
		return c.createQueueConsumer(mCtx, deliverPolicy, optStartSeq, desiredMaxDeliver)
	}

	if c.migrationLocker != nil {
		return c.migrationLocker.WithConsumerMigrationLock(ctx, doMigrate)
	}
	return doMigrate(ctx)
}

func (c *OrderCompletedConsumer) createQueueConsumer(
	ctx context.Context,
	deliverPolicy nats.DeliverPolicy,
	optStartSeq uint64,
	desiredMaxDeliver int,
) error {
	cfg := &nats.ConsumerConfig{
		Durable:        DurableConsumerName,
		FilterSubject:  SubjectOrderCompleted,
		DeliverSubject: nats.NewInbox(),
		DeliverGroup:   QueueGroupName,
		DeliverPolicy:  deliverPolicy,
		OptStartSeq:    optStartSeq,
		AckPolicy:      nats.AckExplicitPolicy,
		AckWait:        c.ackWait,
		MaxDeliver:     desiredMaxDeliver,
		ReplayPolicy:   nats.ReplayInstantPolicy,
	}
	if _, err := c.js.AddConsumer(c.streamName, cfg, nats.Context(ctx)); err != nil {
		// Handle concurrent startup race when another replica created the queue consumer first.
		existing, infoErr := c.js.ConsumerInfo(c.streamName, DurableConsumerName, nats.Context(ctx))
		if infoErr == nil && existing != nil && existing.Config.DeliverGroup == QueueGroupName {
			return c.syncQueueConsumerConfig(ctx, existing, desiredMaxDeliver)
		}
		return fmt.Errorf("create queue consumer %s: %w", DurableConsumerName, err)
	}
	return nil
}

func (c *OrderCompletedConsumer) syncQueueConsumerConfig(ctx context.Context, info *nats.ConsumerInfo, desiredMaxDeliver int) error {
	if info.Config.MaxDeliver == desiredMaxDeliver && info.Config.AckWait == c.ackWait {
		return nil
	}
	updated := info.Config
	updated.MaxDeliver = desiredMaxDeliver
	updated.AckWait = c.ackWait
	if _, err := c.js.UpdateConsumer(c.streamName, &updated, nats.Context(ctx)); err != nil {
		return fmt.Errorf("update consumer %s config: %w", DurableConsumerName, err)
	}
	return nil
}

func (c *OrderCompletedConsumer) isStopping() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stopping
}

func (c *OrderCompletedConsumer) onMessage(ctx context.Context, msg *nats.Msg) {
	c.mu.Lock()
	if c.stopping || ctx.Err() != nil {
		c.mu.Unlock()
		// Do NOT Nak while shutting down: immediate Nak causes tight redelivery loops and
		// burns MaxDeliver; AckWait redelivers cleanly after nc.Close().
		return
	}
	c.wg.Add(1)
	c.mu.Unlock()
	defer c.wg.Done()
	c.handleNATSMsg(ctx, msg)
}

// Stop prevents new message callbacks from entering and waits for every in-flight message
// handler to finish before returning (safe to call multiple times).
// Intentionally does NOT call sub.Drain() or sub.Unsubscribe(): in nats.go v1.36, library-created
// durable subscriptions mark jsi.dc=true so Drain/Unsubscribe deletes the shared durable consumer
// on the server, disrupting peer replicas and replaying the stream on restart. Closing nc in main
// detaches cleanly while preserving the durable consumer.
func (c *OrderCompletedConsumer) Stop() error {
	c.mu.Lock()
	c.stopping = true
	c.mu.Unlock()
	c.wg.Wait()
	return nil
}

func (c *OrderCompletedConsumer) handleNATSMsg(parentCtx context.Context, msg *nats.Msg) {
	if parentCtx.Err() != nil || c.isStopping() {
		return
	}

	headers := make(map[string]string, len(msg.Header))
	for k, vals := range msg.Header {
		if len(vals) > 0 {
			headers[strings.ToLower(k)] = vals[0]
		}
	}

	var delivered uint64 = 1
	if meta, err := msg.Metadata(); err == nil && meta != nil && meta.NumDelivered > 0 {
		delivered = meta.NumDelivered
	}

	msgCtx, cancel := context.WithTimeout(parentCtx, defaultConsumerMsgTimeout)
	defer cancel()

	action, retryErr := c.processMessage(msgCtx, msg.Data, headers, delivered)
	if parentCtx.Err() != nil || c.isStopping() {
		// Shutdown occurred during processing: never route to DLQ or Nak; Ack only if handler already succeeded.
		if action == ActionAck {
			if ackErr := msg.Ack(); ackErr != nil {
				c.logger.WarnContext(msgCtx, "failed to ack order.completed message", slog.String("error", ackErr.Error()))
			}
		}
		return
	}

	switch action {
	case ActionAck, ActionDLQ:
		if ackErr := msg.Ack(); ackErr != nil {
			c.logger.WarnContext(msgCtx, "failed to ack order.completed message", slog.String("error", ackErr.Error()))
		}
	case ActionNak:
		baseDelay := c.nakBaseDelay
		if baseDelay <= 0 {
			baseDelay = defaultConsumerNakDelay
		}
		delay := time.Duration(delivered) * baseDelay
		if delay > 10*time.Second {
			delay = 10 * time.Second
		}
		c.logger.WarnContext(msgCtx, "nacking order.completed message for retry",
			slog.Uint64("attempt", delivered),
			slog.String("error", fmt.Sprint(retryErr)),
		)
		if nakErr := msg.NakWithDelay(delay); nakErr != nil {
			c.logger.WarnContext(msgCtx, "failed to nak order.completed message", slog.String("error", nakErr.Error()))
		}
	}
}

// ProcessRawMessage parses, traces, and idempotently processes an `order.completed` event.
// Returns (ack=true, nil) when handled or persisted to at least one durable DLQ sink,
// or (ack=false, err) when retry/redelivery is required.
func (c *OrderCompletedConsumer) ProcessRawMessage(ctx context.Context, data []byte, headers map[string]string, attempt uint64) (bool, error) {
	action, err := c.processMessage(ctx, data, headers, attempt)
	switch action {
	case ActionAck, ActionDLQ:
		return true, nil
	default:
		return false, err
	}
}

func (c *OrderCompletedConsumer) shutdownErr(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return ErrConsumerStopping
}

func (c *OrderCompletedConsumer) processMessage(ctx context.Context, data []byte, headers map[string]string, attempt uint64) (DeliveryAction, error) {
	if attempt == 0 {
		attempt = 1
	}
	if errors.Is(ctx.Err(), context.Canceled) || c.isStopping() {
		return ActionNak, c.shutdownErr(ctx)
	}

	if len(headers) > 0 {
		ctx = telemetry.ExtractTraceHeaders(ctx, headers)
	}
	tracer := otel.Tracer("promotion")
	ctx, span := tracer.Start(ctx, "nats.consume "+SubjectOrderCompleted,
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(attribute.String("messaging.destination", SubjectOrderCompleted)),
	)
	defer span.End()

	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		if errors.Is(ctx.Err(), context.Canceled) || c.isStopping() {
			return ActionNak, err
		}
		if c.sendToDLQ(ctx, "", data, headers, "invalid event envelope json: "+err.Error(), attempt) {
			return ActionDLQ, err
		}
		return ActionNak, err
	}
	if strings.TrimSpace(env.EventID) == "" {
		err := errors.New("missing event_id")
		if errors.Is(ctx.Err(), context.Canceled) || c.isStopping() {
			return ActionNak, err
		}
		if c.sendToDLQ(ctx, "", data, headers, err.Error(), attempt) {
			return ActionDLQ, err
		}
		return ActionNak, err
	}

	var payload OrderCompletedPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		if errors.Is(ctx.Err(), context.Canceled) || c.isStopping() {
			return ActionNak, err
		}
		if c.sendToDLQ(ctx, env.EventID, data, headers, "invalid order.completed payload json: "+err.Error(), attempt) {
			return ActionDLQ, err
		}
		return ActionNak, err
	}

	already, err := c.store.ProcessOrderCompletedEvent(
		ctx,
		env.EventID,
		SubjectOrderCompleted,
		strings.TrimSpace(payload.OrderID),
		strings.TrimSpace(payload.BuyerID),
		payload.CoinsReward,
	)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) || c.isStopping() {
			return ActionNak, err
		}
		c.logger.WarnContext(ctx, "order.completed handler failed",
			slog.String("event_id", env.EventID),
			slog.String("order_id", payload.OrderID),
			slog.Uint64("attempt", attempt),
			slog.String("error", err.Error()),
		)
		if attempt >= c.maxAttempts {
			if c.sendToDLQ(ctx, env.EventID, data, headers, "max retries exceeded: "+err.Error(), attempt) {
				return ActionDLQ, err
			}
			return ActionNak, err
		}
		return ActionNak, err
	}

	c.logger.InfoContext(ctx, "processed order.completed event",
		slog.String("event_id", env.EventID),
		slog.String("order_id", payload.OrderID),
		slog.Int64("coins_reward", payload.CoinsReward),
		slog.Bool("already_processed", already),
	)
	return ActionAck, nil
}

// sendToDLQ persists the poison message to dead_letter_events and/or NEXUSMART_DLQ using a
// detached bounded context (awaiting JetStream PubAck). Returns true iff at least one durable
// DLQ sink succeeded (E1).
func (c *OrderCompletedConsumer) sendToDLQ(
	ctx context.Context,
	eventID string,
	data []byte,
	origHeaders map[string]string,
	reason string,
	attempts uint64,
) bool {
	c.logger.ErrorContext(ctx, "sending order.completed message to DLQ",
		slog.String("event_id", eventID),
		slog.Uint64("attempts", attempts),
		slog.String("reason", reason),
	)

	dlqCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultDLQWriteTimeout)
	defer cancel()

	saved := false
	if c.dlqStore != nil {
		if err := c.dlqStore.InsertDeadLetterEvent(dlqCtx, DeadLetterRecord{
			ID:           uuid.NewString(),
			Subject:      SubjectOrderCompleted,
			EventID:      eventID,
			Payload:      data,
			ErrorMessage: reason,
			Attempts:     int(attempts),
		}); err != nil {
			c.logger.ErrorContext(dlqCtx, "failed to persist dead_letter_events record",
				slog.String("subject", SubjectOrderCompleted),
				slog.String("event_id", eventID),
				slog.String("error", err.Error()),
			)
		} else {
			saved = true
		}
	}

	if c.dlq != nil {
		dlqHeaders := make(map[string]string, len(origHeaders)+6)
		for k, v := range origHeaders {
			dlqHeaders[k] = v
		}
		dlqHeaders["x-dlq-reason"] = reason
		dlqHeaders["x-original-subject"] = SubjectOrderCompleted
		dlqHeaders["X-DLQ-Original-Subject"] = SubjectOrderCompleted
		dlqHeaders["X-DLQ-Error"] = reason
		dlqHeaders["X-DLQ-Attempts"] = strconv.FormatUint(attempts, 10)
		if eventID != "" {
			dlqHeaders["X-DLQ-Event-Id"] = eventID
		}
		telemetry.InjectTraceHeaders(dlqCtx, dlqHeaders)

		if err := c.dlq.PublishDLQ(dlqCtx, "dlq."+SubjectOrderCompleted, data, dlqHeaders); err != nil {
			c.logger.ErrorContext(dlqCtx, "failed to publish message to NATS DLQ",
				slog.String("dlq_subject", "dlq."+SubjectOrderCompleted),
				slog.String("event_id", eventID),
				slog.String("error", err.Error()),
			)
		} else {
			saved = true
		}
	}

	return saved
}

type jetStreamDLQ struct {
	js nats.JetStreamContext
}

func (p *jetStreamDLQ) PublishDLQ(ctx context.Context, subject string, payload []byte, headers map[string]string) error {
	msg := nats.NewMsg(subject)
	msg.Data = payload
	for k, v := range headers {
		msg.Header.Set(k, v)
	}
	pubAck, err := p.js.PublishMsg(msg, nats.Context(ctx))
	if err != nil {
		return err
	}
	if pubAck == nil {
		return errors.New("nil jetstream pub ack for dlq publish")
	}
	return nil
}
