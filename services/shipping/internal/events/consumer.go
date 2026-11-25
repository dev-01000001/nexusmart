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

	"github.com/nats-io/nats.go"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	defaultConsumerMaxAttempts uint64 = 5
	defaultConsumerMsgTimeout         = 25 * time.Second
	defaultDLQWriteTimeout            = 5 * time.Second
)

// OrderCancelHandler handles order.cancelled events for the shipping service.
type OrderCancelHandler interface {
	HandleOrderCancelled(ctx context.Context, eventID, orderID, reason string) error
}

// ConsumerStore is the minimal persistence contract required by Consumer.
type ConsumerStore interface {
	IsEventProcessed(ctx context.Context, eventID string) (bool, error)
	InsertDeadLetter(ctx context.Context, eventID, subject string, payload []byte, errMsg string, attempts int) error
}

// DeliveryAction indicates what the caller should do with a NATS message.
type DeliveryAction int

const (
	ActionAck DeliveryAction = iota
	ActionNak
	ActionDLQ
)

// Consumer subscribes to order.cancelled events idempotently with DLQ routing after max retries.
type Consumer struct {
	js          nats.JetStreamContext
	dlqPub      Publisher
	repo        ConsumerStore
	handler     OrderCancelHandler
	stream      string
	maxAttempts uint64
	logger      *slog.Logger
	mu          sync.Mutex
	stopping    bool
	wg          sync.WaitGroup
}

// NewConsumer constructs an idempotent JetStream consumer for order.cancelled.
func NewConsumer(js nats.JetStreamContext, repo ConsumerStore, handler OrderCancelHandler, stream string, maxAttempts uint64, logger *slog.Logger) *Consumer {
	if maxAttempts == 0 {
		maxAttempts = defaultConsumerMaxAttempts
	}
	if strings.TrimSpace(stream) == "" {
		stream = "NEXUSMART"
	}
	if logger == nil {
		logger = slog.Default()
	}
	var dlqPub Publisher
	if js != nil {
		dlqPub = js
	}
	return &Consumer{
		js:          js,
		dlqPub:      dlqPub,
		repo:        repo,
		handler:     handler,
		stream:      stream,
		maxAttempts: maxAttempts,
		logger:      logger,
	}
}

// Start registers the durable JetStream queue consumer on order.cancelled.
func (c *Consumer) Start(ctx context.Context) error {
	c.mu.Lock()
	c.stopping = false
	c.mu.Unlock()

	_, err := c.js.QueueSubscribe(
		SubjectOrderCancelled,
		"shipping-workers",
		func(msg *nats.Msg) {
			c.onMessage(ctx, msg)
		},
		nats.BindStream(c.stream),
		nats.Durable("shipping-order-cancelled"),
		nats.ManualAck(),
		nats.AckExplicit(),
		nats.AckWait(30*time.Second),
		nats.MaxDeliver(int(c.maxAttempts)+1),
		nats.DeliverAll(),
	)
	if err != nil {
		_ = c.Close()
		return fmt.Errorf("subscribe %s: %w", SubjectOrderCancelled, err)
	}
	return nil
}

// Close prevents new message callbacks from entering and waits for every in-flight message
// handler to finish before returning (safe to call multiple times).
// Intentionally does NOT call sub.Drain() or sub.Unsubscribe(): in nats.go v1.36, QueueSubscribe
// marks library-created durable consumers with jsi.dc=true, so Drain/Unsubscribe deletes the shared
// durable consumer on the server, disrupting peer replicas and replaying the stream on restart.
// Closing nc in main detaches cleanly while preserving the durable consumer.
func (c *Consumer) Close() error {
	c.mu.Lock()
	c.stopping = true
	c.mu.Unlock()
	c.wg.Wait()
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
		// Do NOT Nak while shutting down: immediate Nak causes tight redelivery loops and
		// burns MaxDeliver; AckWait redelivers cleanly after nc.Close().
		return
	}
	c.wg.Add(1)
	c.mu.Unlock()
	defer c.wg.Done()
	c.handleNATSMessage(ctx, msg)
}

func (c *Consumer) handleNATSMessage(baseCtx context.Context, msg *nats.Msg) {
	if baseCtx.Err() != nil || c.isStopping() {
		return
	}

	var delivered uint64 = 1
	if meta, err := msg.Metadata(); err == nil && meta != nil && meta.NumDelivered > 0 {
		delivered = meta.NumDelivered
	}

	headers := make(map[string]string, len(msg.Header))
	for k, vals := range msg.Header {
		if len(vals) > 0 {
			headers[strings.ToLower(k)] = vals[0]
		}
	}

	procCtx, cancel := context.WithTimeout(baseCtx, defaultConsumerMsgTimeout)
	defer cancel()

	action, _ := c.ProcessRawMessage(procCtx, msg.Subject, msg.Data, headers, delivered)
	if baseCtx.Err() != nil || c.isStopping() {
		// Context cancelled during shutdown: never route to DLQ or Nak; Ack only if handler already succeeded.
		if action == ActionAck {
			if err := msg.Ack(); err != nil {
				c.logger.WarnContext(procCtx, "failed to ack NATS message",
					slog.String("subject", msg.Subject),
					slog.String("error", err.Error()),
				)
			}
		}
		return
	}

	switch action {
	case ActionAck, ActionDLQ:
		if err := msg.Ack(); err != nil {
			c.logger.WarnContext(procCtx, "failed to ack NATS message",
				slog.String("subject", msg.Subject),
				slog.String("error", err.Error()),
			)
		}
	case ActionNak:
		delay := time.Duration(delivered) * 500 * time.Millisecond
		if err := msg.NakWithDelay(delay); err != nil {
			c.logger.WarnContext(procCtx, "failed to nak NATS message",
				slog.String("subject", msg.Subject),
				slog.String("error", err.Error()),
			)
		}
	}
}

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
	ctx, span := otel.Tracer("shipping").Start(ctx, "nats.consume "+subject,
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(attribute.String("messaging.destination", subject)),
	)
	defer span.End()

	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		if errors.Is(ctx.Err(), context.Canceled) || c.isStopping() {
			return ActionNak, err
		}
		if c.sendToDLQ(ctx, "", subject, data, fmt.Sprintf("malformed envelope: %v", err), int(numDelivered)) {
			return ActionDLQ, err
		}
		return ActionNak, err
	}
	if strings.TrimSpace(env.EventID) == "" {
		err := fmt.Errorf("missing event_id on subject %s", subject)
		if errors.Is(ctx.Err(), context.Canceled) || c.isStopping() {
			return ActionNak, err
		}
		if c.sendToDLQ(ctx, "", subject, data, err.Error(), int(numDelivered)) {
			return ActionDLQ, err
		}
		return ActionNak, err
	}

	var payload OrderCancelledPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		if errors.Is(ctx.Err(), context.Canceled) || c.isStopping() {
			return ActionNak, err
		}
		if c.sendToDLQ(ctx, env.EventID, subject, data, fmt.Sprintf("malformed order.cancelled payload: %v", err), int(numDelivered)) {
			return ActionDLQ, err
		}
		return ActionNak, err
	}
	if strings.TrimSpace(payload.OrderID) == "" {
		err := fmt.Errorf("missing order_id on subject %s (event_id=%s)", subject, env.EventID)
		if errors.Is(ctx.Err(), context.Canceled) || c.isStopping() {
			return ActionNak, err
		}
		if c.sendToDLQ(ctx, env.EventID, subject, data, err.Error(), int(numDelivered)) {
			return ActionDLQ, err
		}
		return ActionNak, err
	}

	if c.repo != nil {
		processed, err := c.repo.IsEventProcessed(ctx, env.EventID)
		if err == nil && processed {
			return ActionAck, nil
		}
		if err != nil && (errors.Is(ctx.Err(), context.Canceled) || c.isStopping()) {
			return ActionNak, err
		}
	}

	handleErr := c.handler.HandleOrderCancelled(ctx, env.EventID, payload.OrderID, payload.Reason)
	if handleErr == nil {
		return ActionAck, nil
	}
	if errors.Is(ctx.Err(), context.Canceled) || c.isStopping() {
		return ActionNak, handleErr
	}

	c.logger.ErrorContext(ctx, "handle order.cancelled failed",
		slog.String("event_id", env.EventID),
		slog.String("order_id", payload.OrderID),
		slog.Uint64("attempt", numDelivered),
		slog.String("error", handleErr.Error()),
	)
	if numDelivered >= c.maxAttempts {
		if c.sendToDLQ(ctx, env.EventID, subject, data, handleErr.Error(), int(numDelivered)) {
			return ActionDLQ, handleErr
		}
		return ActionNak, handleErr
	}
	return ActionNak, handleErr
}

// sendToDLQ persists the poison message to dead_letter_events and/or NEXUSMART_DLQ using a
// detached bounded context (awaiting JetStream PubAck). Returns true iff at least one durable
// DLQ sink succeeded (E1).
func (c *Consumer) sendToDLQ(ctx context.Context, eventID, subject string, raw []byte, errMsg string, attempts int) bool {
	c.logger.ErrorContext(ctx, "routing poison message to DLQ",
		slog.String("event_id", eventID),
		slog.String("subject", subject),
		slog.Int("attempts", attempts),
		slog.String("error", errMsg),
	)

	dlqCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultDLQWriteTimeout)
	defer cancel()

	saved := false
	if c.repo != nil {
		if err := c.repo.InsertDeadLetter(dlqCtx, eventID, subject, raw, errMsg, attempts); err != nil {
			c.logger.ErrorContext(dlqCtx, "persist dead letter event failed",
				slog.String("event_id", eventID),
				slog.String("subject", subject),
				slog.String("error", err.Error()),
			)
		} else {
			saved = true
		}
	}

	if c.dlqPub != nil {
		dlqPayload, _ := json.Marshal(map[string]any{
			"event_id":         eventID,
			"original_subject": subject,
			"error":            errMsg,
			"attempts":         attempts,
			"failed_at":        time.Now().UTC().Format(time.RFC3339),
			"raw_payload":      string(raw),
		})
		dlqMsg := nats.NewMsg("dlq." + subject)
		dlqMsg.Data = dlqPayload
		dlqMsg.Header.Set("X-DLQ-Original-Subject", subject)
		dlqMsg.Header.Set("X-DLQ-Error", errMsg)
		dlqMsg.Header.Set("X-DLQ-Attempts", strconv.Itoa(attempts))
		if eventID != "" {
			dlqMsg.Header.Set("X-DLQ-Event-Id", eventID)
		}
		traceHdrs := map[string]string{}
		telemetry.InjectTraceHeaders(dlqCtx, traceHdrs)
		for k, v := range traceHdrs {
			dlqMsg.Header.Set(k, v)
		}
		if _, err := c.dlqPub.PublishMsg(dlqMsg, nats.Context(dlqCtx)); err != nil {
			c.logger.ErrorContext(dlqCtx, "publish to NEXUSMART_DLQ failed",
				slog.String("subject", "dlq."+subject),
				slog.String("error", err.Error()),
			)
		} else {
			saved = true
		}
	}

	return saved
}
