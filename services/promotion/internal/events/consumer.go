package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/promotion/internal/store"
)

const (
	SubjectOrderCompleted = "order.completed"
	DurableConsumerName   = "promotion-order-completed"
	DefaultDLQStream      = "NEXUSMART_DLQ"
	MaxDeliveryAttempts   = 5
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
	store      store.Store
	logger     *slog.Logger
	streamName string
	js         nats.JetStreamContext
	dlq        DLQPublisher
	sub        *nats.Subscription
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
		store:      st,
		logger:     logger,
		streamName: streamName,
		js:         js,
	}
	if js != nil {
		c.dlq = &jetStreamDLQ{js: js}
	}
	return c
}

// SetDLQPublisher overrides the DLQ publisher (useful for unit tests).
func (c *OrderCompletedConsumer) SetDLQPublisher(pub DLQPublisher) {
	c.dlq = pub
}

// EnsureStreams ensures the primary NEXUSMART stream and NEXUSMART_DLQ stream exist on JetStream.
func (c *OrderCompletedConsumer) EnsureStreams() error {
	if c.js == nil {
		return nil
	}
	if _, err := c.js.StreamInfo(c.streamName); err != nil {
		if !errors.Is(err, nats.ErrStreamNotFound) {
			return fmt.Errorf("inspect stream %s: %w", c.streamName, err)
		}
		_, addErr := c.js.AddStream(&nats.StreamConfig{
			Name:       c.streamName,
			Subjects:   []string{"user.>", "order.>", "payment.>", "shipment.>"},
			Retention:  nats.LimitsPolicy,
			Storage:    nats.FileStorage,
			Duplicates: 2 * time.Minute,
		})
		if addErr != nil && !errors.Is(addErr, nats.ErrStreamNameAlreadyInUse) {
			return fmt.Errorf("create stream %s: %w", c.streamName, addErr)
		}
	}

	if _, err := c.js.StreamInfo(DefaultDLQStream); err != nil {
		if !errors.Is(err, nats.ErrStreamNotFound) {
			return fmt.Errorf("inspect dlq stream: %w", err)
		}
		_, addErr := c.js.AddStream(&nats.StreamConfig{
			Name:      DefaultDLQStream,
			Subjects:  []string{"dlq.>"},
			Retention: nats.LimitsPolicy,
			Storage:   nats.FileStorage,
		})
		if addErr != nil && !errors.Is(addErr, nats.ErrStreamNameAlreadyInUse) {
			return fmt.Errorf("create dlq stream: %w", addErr)
		}
	}
	return nil
}

// Start subscribes durably to `order.completed` on JetStream.
func (c *OrderCompletedConsumer) Start(ctx context.Context) error {
	if c.js == nil {
		return nil
	}
	if err := c.EnsureStreams(); err != nil {
		return err
	}

	sub, err := c.js.Subscribe(
		SubjectOrderCompleted,
		func(msg *nats.Msg) {
			c.handleNATSMsg(ctx, msg)
		},
		nats.BindStream(c.streamName),
		nats.Durable(DurableConsumerName),
		nats.ManualAck(),
		nats.AckExplicit(),
		nats.MaxDeliver(MaxDeliveryAttempts),
		nats.AckWait(30*time.Second),
	)
	if err != nil {
		return fmt.Errorf("subscribe %s: %w", SubjectOrderCompleted, err)
	}
	c.sub = sub
	c.logger.Info("subscribed to order.completed jetstream consumer",
		slog.String("stream", c.streamName),
		slog.String("durable", DurableConsumerName),
	)
	return nil
}

// Stop drains and unsubscribes the JetStream consumer.
func (c *OrderCompletedConsumer) Stop() error {
	if c.sub != nil {
		return c.sub.Drain()
	}
	return nil
}

func (c *OrderCompletedConsumer) handleNATSMsg(parentCtx context.Context, msg *nats.Msg) {
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

	msgCtx, cancel := context.WithTimeout(parentCtx, 15*time.Second)
	defer cancel()

	shouldAck, retryErr := c.ProcessRawMessage(msgCtx, msg.Data, headers, delivered)
	if shouldAck {
		if ackErr := msg.Ack(); ackErr != nil {
			c.logger.Warn("failed to ack order.completed message", slog.String("error", ackErr.Error()))
		}
		return
	}
	delay := time.Duration(delivered) * time.Second
	if delay > 10*time.Second {
		delay = 10 * time.Second
	}
	c.logger.Warn("nacking order.completed message for retry",
		slog.Uint64("attempt", delivered),
		slog.String("error", fmt.Sprint(retryErr)),
	)
	_ = msg.NakWithDelay(delay)
}

// ProcessRawMessage parses, traces, and idempotently processes an `order.completed` event.
// Returns (ack=true, nil) when handled or dead-lettered, or (ack=false, err) when transient retry is needed.
func (c *OrderCompletedConsumer) ProcessRawMessage(ctx context.Context, data []byte, headers map[string]string, attempt uint64) (bool, error) {
	ctx = telemetry.ExtractTraceHeaders(ctx, headers)
	tracer := otel.Tracer("promotion")
	ctx, span := tracer.Start(ctx, "nats.consume "+SubjectOrderCompleted,
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(attribute.String("messaging.destination", SubjectOrderCompleted)),
	)
	defer span.End()

	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		c.sendToDLQ(ctx, data, headers, "invalid event envelope json: "+err.Error())
		return true, nil
	}
	if strings.TrimSpace(env.EventID) == "" {
		c.sendToDLQ(ctx, data, headers, "missing event_id")
		return true, nil
	}

	var payload OrderCompletedPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		c.sendToDLQ(ctx, data, headers, "invalid order.completed payload json: "+err.Error())
		return true, nil
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
		if attempt >= MaxDeliveryAttempts {
			c.sendToDLQ(ctx, data, headers, "max retries exceeded: "+err.Error())
			return true, nil
		}
		return false, err
	}

	c.logger.InfoContext(ctx, "processed order.completed event",
		slog.String("event_id", env.EventID),
		slog.String("order_id", payload.OrderID),
		slog.Int64("coins_reward", payload.CoinsReward),
		slog.Bool("already_processed", already),
	)
	return true, nil
}

func (c *OrderCompletedConsumer) sendToDLQ(ctx context.Context, data []byte, origHeaders map[string]string, reason string) {
	c.logger.ErrorContext(ctx, "sending order.completed message to DLQ",
		slog.String("reason", reason),
	)
	if c.dlq == nil {
		return
	}
	dlqHeaders := make(map[string]string, len(origHeaders)+2)
	for k, v := range origHeaders {
		dlqHeaders[k] = v
	}
	dlqHeaders["x-dlq-reason"] = reason
	dlqHeaders["x-original-subject"] = SubjectOrderCompleted
	telemetry.InjectTraceHeaders(ctx, dlqHeaders)

	if err := c.dlq.PublishDLQ(ctx, "dlq."+SubjectOrderCompleted, data, dlqHeaders); err != nil {
		c.logger.ErrorContext(ctx, "failed to publish message to DLQ", slog.String("error", err.Error()))
	}
}

type jetStreamDLQ struct {
	js nats.JetStreamContext
}

func (p *jetStreamDLQ) PublishDLQ(_ context.Context, subject string, payload []byte, headers map[string]string) error {
	msg := nats.NewMsg(subject)
	msg.Data = payload
	for k, v := range headers {
		msg.Header.Set(k, v)
	}
	_, err := p.js.PublishMsg(msg)
	return err
}
