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
	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/order/internal/db"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const defaultMaxDeliverAttempts = 5

// EventHandler handles domain events consumed by the order service.
type EventHandler interface {
	HandlePaymentSucceeded(ctx context.Context, eventID string, payload PaymentSucceededPayload) error
	HandlePaymentFailed(ctx context.Context, eventID string, payload PaymentFailedPayload) error
	HandlePaymentExpired(ctx context.Context, eventID string, payload PaymentFailedPayload) error
	HandlePaymentRefunded(ctx context.Context, eventID string, payload PaymentRefundedPayload) error
	HandlePaymentRefundRejected(ctx context.Context, eventID string, payload PaymentRefundRejectedPayload) error
	HandleShipmentUpdated(ctx context.Context, eventID string, payload ShipmentUpdatedPayload) error
	HandleShipmentCreateFailed(ctx context.Context, eventID string, payload ShipmentCreateFailedPayload) error
}

// DeadLetterStore records dead-lettered messages locally alongside NATS DLQ stream publishing.
type DeadLetterStore interface {
	InsertDeadLetterEvent(ctx context.Context, arg db.InsertDeadLetterEventParams) error
}

// Consumer subscribes to NEXUSMART payment and shipment subjects with idempotency and DLQ routing.
type Consumer struct {
	js          nats.JetStreamContext
	dlqPub      Publisher
	dlqStore    DeadLetterStore
	handler     EventHandler
	logger      *slog.Logger
	streamName  string
	maxAttempts uint64
	mu          sync.Mutex
	stopping    bool
	wg          sync.WaitGroup
}

// NewConsumer creates an idempotent NATS JetStream consumer with DLQ handling.
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
		streamName = "NEXUSMART"
	}
	return &Consumer{
		js:          js,
		dlqPub:      dlqPub,
		dlqStore:    dlqStore,
		handler:     handler,
		logger:      logger,
		streamName:  streamName,
		maxAttempts: defaultMaxDeliverAttempts,
	}
}

// Start registers JetStream queue subscriptions for payment.> and shipment.>.
func (c *Consumer) Start(ctx context.Context) error {
	specs := []struct {
		subject string
		durable string
		queue   string
	}{
		{subject: "payment.>", durable: "order-payment-consumer", queue: "order-payment-workers"},
		{subject: "shipment.>", durable: "order-shipment-consumer", queue: "order-shipment-workers"},
	}

	c.mu.Lock()
	c.stopping = false
	c.mu.Unlock()

	for _, sp := range specs {
		_, err := c.js.QueueSubscribe(
			sp.subject,
			sp.queue,
			func(msg *nats.Msg) {
				c.onMessage(ctx, msg)
			},
			nats.BindStream(c.streamName),
			nats.Durable(sp.durable),
			nats.ManualAck(),
			nats.AckExplicit(),
			nats.AckWait(30*time.Second),
			nats.MaxDeliver(int(c.maxAttempts)+1),
			nats.DeliverAll(),
		)
		if err != nil {
			_ = c.Stop()
			return fmt.Errorf("subscribe %s (%s): %w", sp.subject, sp.durable, err)
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
		// P1-4: Do NOT Nak while shutting down — immediate Nak causes JetStream to redeliver
		// in a tight loop and burn MaxDeliver; AckWait (30s) redelivers cleanly after nc.Close().
		return
	}
	c.wg.Add(1)
	c.mu.Unlock()
	defer c.wg.Done()
	c.handleNATSMessage(ctx, msg)
}

// Stop prevents new message callbacks from entering and waits for every in-flight message
// handler to finish before returning (safe to call multiple times).
// Intentionally does NOT call sub.Drain() or sub.Unsubscribe(): in nats.go v1.36, QueueSubscribe
// marks library-created durable consumers with jsi.dc=true (js.go:1819), so Drain/Unsubscribe
// deletes the shared durable consumer on the server (nats.go:4653), breaking other replicas and
// replaying the stream on restart. Closing nc in main detaches cleanly while preserving durables.
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
	tracer := otel.Tracer("order")
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
	// P0-2: Never route to DLQ when shutdown cancelled the context; return ActionNak for redelivery.
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
		// P1-4: Return without Nak on shutdown so AckWait redelivers without burning MaxDeliver.
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
		// P0-2 / P1-4: Context cancelled during processing (shutdown) — neither Ack nor Nak.
		if action == ActionAck {
			if err := msg.Ack(); err != nil {
				c.logger.WarnContext(procCtx, "failed to ack NATS message", "subject", msg.Subject, "error", err)
			}
		}
		return
	}
	switch action {
	case ActionAck, ActionDLQ:
		if err := msg.Ack(); err != nil {
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
	case SubjectPaymentSucceeded:
		var p PaymentSucceededPayload
		if err := json.Unmarshal(rawPayload, &p); err != nil {
			return fmt.Errorf("unmarshal payment.succeeded payload: %w", err)
		}
		return c.handler.HandlePaymentSucceeded(ctx, eventID, p)

	case SubjectPaymentFailed:
		var p PaymentFailedPayload
		if err := json.Unmarshal(rawPayload, &p); err != nil {
			return fmt.Errorf("unmarshal payment.failed payload: %w", err)
		}
		return c.handler.HandlePaymentFailed(ctx, eventID, p)

	case SubjectPaymentExpired:
		var p PaymentFailedPayload
		if err := json.Unmarshal(rawPayload, &p); err != nil {
			return fmt.Errorf("unmarshal payment.expired payload: %w", err)
		}
		return c.handler.HandlePaymentExpired(ctx, eventID, p)

	case SubjectPaymentRefunded:
		var p PaymentRefundedPayload
		if err := json.Unmarshal(rawPayload, &p); err != nil {
			return fmt.Errorf("unmarshal payment.refunded payload: %w", err)
		}
		return c.handler.HandlePaymentRefunded(ctx, eventID, p)

	case SubjectPaymentRefundRejected:
		var p PaymentRefundRejectedPayload
		if err := json.Unmarshal(rawPayload, &p); err != nil {
			return fmt.Errorf("unmarshal payment.refund_rejected payload: %w", err)
		}
		return c.handler.HandlePaymentRefundRejected(ctx, eventID, p)

	case SubjectShipmentUpdated:
		var p ShipmentUpdatedPayload
		if err := json.Unmarshal(rawPayload, &p); err != nil {
			return fmt.Errorf("unmarshal shipment.updated payload: %w", err)
		}
		return c.handler.HandleShipmentUpdated(ctx, eventID, p)

	case SubjectShipmentCreateFailed:
		var p ShipmentCreateFailedPayload
		if err := json.Unmarshal(rawPayload, &p); err != nil {
			return fmt.Errorf("unmarshal shipment.create_failed payload: %w", err)
		}
		return c.handler.HandleShipmentCreateFailed(ctx, eventID, p)

	default:
		// Ignore unrelated subjects on payment.> / shipment.> safely.
		return nil
	}
}

// sendToDLQ persists the poison message to dead_letter_events and/or NEXUSMART_DLQ using a
// detached 5s context. Returns true iff at least one durable DLQ sink succeeded (P0-2).
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
