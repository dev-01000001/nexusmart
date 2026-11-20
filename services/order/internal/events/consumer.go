package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
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
	HandleShipmentUpdated(ctx context.Context, eventID string, payload ShipmentUpdatedPayload) error
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
	subs        []*nats.Subscription
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

	for _, sp := range specs {
		sub, err := c.js.QueueSubscribe(
			sp.subject,
			sp.queue,
			func(msg *nats.Msg) {
				c.handleNATSMessage(ctx, msg)
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
		c.subs = append(c.subs, sub)
	}
	return nil
}

// Stop drains and unsubscribes all active JetStream subscriptions.
func (c *Consumer) Stop() error {
	var firstErr error
	for _, sub := range c.subs {
		if err := sub.Drain(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	c.subs = nil
	return firstErr
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
		c.sendToDLQ(ctx, subject, "", data, fmt.Sprintf("malformed event envelope: %v", err), numDelivered)
		return ActionDLQ, err
	}
	if strings.TrimSpace(env.EventID) == "" {
		err := fmt.Errorf("missing event_id on subject %s", subject)
		c.sendToDLQ(ctx, subject, "", data, err.Error(), numDelivered)
		return ActionDLQ, err
	}

	eventType := strings.TrimSpace(env.EventType)
	if eventType == "" {
		eventType = subject
	}

	handleErr := c.dispatch(ctx, eventType, env.EventID, env.Payload)
	if handleErr == nil {
		return ActionAck, nil
	}

	c.logger.WarnContext(ctx, "event handler failed",
		"subject", subject,
		"event_type", eventType,
		"event_id", env.EventID,
		"attempt", numDelivered,
		"error", handleErr,
	)

	if numDelivered >= c.maxAttempts {
		c.sendToDLQ(ctx, subject, env.EventID, data, handleErr.Error(), numDelivered)
		return ActionDLQ, handleErr
	}
	return ActionNak, handleErr
}

func (c *Consumer) handleNATSMessage(baseCtx context.Context, msg *nats.Msg) {
	if baseCtx.Err() != nil {
		_ = msg.Nak()
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

	case SubjectShipmentUpdated:
		var p ShipmentUpdatedPayload
		if err := json.Unmarshal(rawPayload, &p); err != nil {
			return fmt.Errorf("unmarshal shipment.updated payload: %w", err)
		}
		return c.handler.HandleShipmentUpdated(ctx, eventID, p)

	default:
		// Ignore unrelated subjects on payment.> / shipment.> safely.
		return nil
	}
}

func (c *Consumer) sendToDLQ(ctx context.Context, subject, eventID string, payload []byte, errMsg string, attempts uint64) {
	c.logger.ErrorContext(ctx, "routing poison message to DLQ",
		"subject", subject,
		"event_id", eventID,
		"attempts", attempts,
		"error", errMsg,
	)

	if c.dlqStore != nil {
		if err := c.dlqStore.InsertDeadLetterEvent(ctx, db.InsertDeadLetterEventParams{
			ID:           uuid.NewString(),
			Subject:      subject,
			EventID:      eventID,
			Payload:      payload,
			ErrorMessage: errMsg,
			Attempts:     int32(attempts),
		}); err != nil {
			c.logger.ErrorContext(ctx, "failed to persist dead_letter_event", "subject", subject, "error", err)
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
		if _, err := c.dlqPub.PublishMsg(dlqMsg); err != nil {
			c.logger.ErrorContext(ctx, "failed to publish message to NATS DLQ", "dlq_subject", dlqMsg.Subject, "error", err)
		}
	}
}
