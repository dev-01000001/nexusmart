package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/shipping/internal/store"
)

// OrderCancelHandler handles order.cancelled events for the shipping service.
type OrderCancelHandler interface {
	HandleOrderCancelled(ctx context.Context, eventID, orderID, reason string) error
}

// Consumer subscribes to order.cancelled events idempotently with DLQ routing after max retries.
type Consumer struct {
	js          nats.JetStreamContext
	repo        store.Repository
	handler     OrderCancelHandler
	stream      string
	maxAttempts uint64
	logger      *slog.Logger
	sub         *nats.Subscription
}

// NewConsumer constructs an idempotent JetStream consumer for order.cancelled.
func NewConsumer(js nats.JetStreamContext, repo store.Repository, handler OrderCancelHandler, stream string, maxAttempts uint64, logger *slog.Logger) *Consumer {
	if maxAttempts == 0 {
		maxAttempts = 5
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Consumer{
		js:          js,
		repo:        repo,
		handler:     handler,
		stream:      stream,
		maxAttempts: maxAttempts,
		logger:      logger,
	}
}

// Start registers the durable JetStream queue consumer on order.cancelled.
func (c *Consumer) Start(ctx context.Context) error {
	sub, err := c.js.QueueSubscribe(
		SubjectOrderCancelled,
		"shipping-workers",
		func(msg *nats.Msg) {
			c.handleMsg(ctx, msg)
		},
		nats.BindStream(c.stream),
		nats.Durable("shipping-order-cancelled"),
		nats.ManualAck(),
		nats.AckWait(30*time.Second),
		nats.MaxDeliver(int(c.maxAttempts)+1),
	)
	if err != nil {
		return fmt.Errorf("subscribe %s: %w", SubjectOrderCancelled, err)
	}
	c.sub = sub
	return nil
}

// Close drains the NATS subscription.
func (c *Consumer) Close() error {
	if c.sub != nil {
		return c.sub.Drain()
	}
	return nil
}

func (c *Consumer) handleMsg(baseCtx context.Context, msg *nats.Msg) {
	ctx := baseCtx
	if msg.Header != nil {
		carrierMap := map[string]string{}
		if tp := msg.Header.Get("traceparent"); tp != "" {
			carrierMap["traceparent"] = tp
		}
		if ts := msg.Header.Get("tracestate"); ts != "" {
			carrierMap["tracestate"] = ts
		}
		if len(carrierMap) > 0 {
			ctx = telemetry.ExtractTraceHeaders(ctx, carrierMap)
		}
	}

	var delivered uint64 = 1
	if meta, err := msg.Metadata(); err == nil && meta != nil {
		delivered = meta.NumDelivered
	}

	var env Envelope
	if err := json.Unmarshal(msg.Data, &env); err != nil || env.EventID == "" {
		c.sendToDLQ(ctx, "", msg.Subject, msg.Data, fmt.Sprintf("malformed envelope: %v", err), int(delivered))
		_ = msg.Ack()
		return
	}

	var payload OrderCancelledPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil || payload.OrderID == "" {
		c.sendToDLQ(ctx, env.EventID, msg.Subject, msg.Data, fmt.Sprintf("malformed order.cancelled payload: %v", err), int(delivered))
		_ = msg.Ack()
		return
	}

	processed, err := c.repo.IsEventProcessed(ctx, env.EventID)
	if err == nil && processed {
		_ = msg.Ack()
		return
	}

	if err := c.handler.HandleOrderCancelled(ctx, env.EventID, payload.OrderID, payload.Reason); err != nil {
		c.logger.ErrorContext(ctx, "handle order.cancelled failed",
			slog.String("event_id", env.EventID),
			slog.String("order_id", payload.OrderID),
			slog.Uint64("attempt", delivered),
			slog.String("error", err.Error()),
		)
		if delivered >= c.maxAttempts {
			c.sendToDLQ(ctx, env.EventID, msg.Subject, msg.Data, err.Error(), int(delivered))
			_ = msg.Ack()
			return
		}
		_ = msg.NakWithDelay(time.Duration(delivered) * time.Second)
		return
	}

	_ = msg.Ack()
}

func (c *Consumer) sendToDLQ(ctx context.Context, eventID, subject string, raw []byte, errMsg string, attempts int) {
	if err := c.repo.InsertDeadLetter(ctx, eventID, subject, raw, errMsg, attempts); err != nil {
		c.logger.ErrorContext(ctx, "persist dead letter event failed",
			slog.String("event_id", eventID),
			slog.String("subject", subject),
			slog.String("error", err.Error()),
		)
	}

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
	if _, err := c.js.PublishMsg(dlqMsg); err != nil {
		c.logger.ErrorContext(ctx, "publish to NEXUSMART_DLQ failed",
			slog.String("subject", "dlq."+subject),
			slog.String("error", err.Error()),
		)
	}
}
