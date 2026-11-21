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
	SubjectOrderCompleted     = "order.completed"
	SubjectOrderRefunded      = "order.refunded"
	DurableConsumerName       = "promotion-order-completed"
	DurableRefundConsumerName = "promotion-order-refunded"
	DefaultDLQStream          = "NEXUSMART_DLQ"
	MaxDeliveryAttempts       = 5
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

// OrderRefundedPayload matches OrderService's `order.refunded` outbox event payload.
type OrderRefundedPayload struct {
	OrderID              string `json:"order_id"`
	OrderCode            string `json:"order_code"`
	CheckoutGroupID      string `json:"checkout_group_id"`
	BuyerID              string `json:"buyer_id"`
	ShopID               string `json:"shop_id"`
	ReturnRequestID      string `json:"return_request_id"`
	RefundID             string `json:"refund_id"`
	RefundAmountVND      int64  `json:"refund_amount_vnd"`
	CoinsDiscountVND     int64  `json:"coins_discount_vnd"`
	AllGroupOrdersClosed bool   `json:"all_group_orders_closed"`
	Reason               string `json:"reason"`
}

// ReleaseReservationFn releases vouchers, flash-sale slots, and coins reserved for a checkout group.
type ReleaseReservationFn func(ctx context.Context, orderID string) error

// DLQPublisher publishes unprocessable messages to a dead letter subject.
type DLQPublisher interface {
	PublishDLQ(ctx context.Context, subject string, payload []byte, headers map[string]string) error
}

// OrderCompletedConsumer consumes `order.completed` and `order.refunded` from NATS JetStream,
// idempotently crediting reward coins, clawing back rewards on refunds, restoring proportional
// coins, and releasing checkout-group promotion reservations.
type OrderCompletedConsumer struct {
	store                store.Store
	logger               *slog.Logger
	streamName           string
	js                   nats.JetStreamContext
	dlq                  DLQPublisher
	releaseReservationFn ReleaseReservationFn
	sub                  *nats.Subscription
	refundSub            *nats.Subscription
}

// NewOrderCompletedConsumer builds a JetStream consumer for `order.completed` and `order.refunded`.
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

// SetReleaseReservationFn wires the optional callback invoked when `order.refunded`
// closes all orders in a checkout group so vouchers, flash-sale slots, and coins are released idempotently.
func (c *OrderCompletedConsumer) SetReleaseReservationFn(fn ReleaseReservationFn) {
	c.releaseReservationFn = fn
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

// Start subscribes durably to `order.completed` and `order.refunded` on JetStream.
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
			c.handleNATSMsg(ctx, SubjectOrderCompleted, msg)
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

	refundSub, err := c.js.Subscribe(
		SubjectOrderRefunded,
		func(msg *nats.Msg) {
			c.handleNATSMsg(ctx, SubjectOrderRefunded, msg)
		},
		nats.BindStream(c.streamName),
		nats.Durable(DurableRefundConsumerName),
		nats.ManualAck(),
		nats.AckExplicit(),
		nats.MaxDeliver(MaxDeliveryAttempts),
		nats.AckWait(30*time.Second),
	)
	if err != nil {
		if sub != nil {
			_ = sub.Drain()
		}
		return fmt.Errorf("subscribe %s: %w", SubjectOrderRefunded, err)
	}

	c.sub = sub
	c.refundSub = refundSub
	c.logger.Info("subscribed to promotion order jetstream consumers",
		slog.String("stream", c.streamName),
		slog.String("completed_durable", DurableConsumerName),
		slog.String("refunded_durable", DurableRefundConsumerName),
	)
	return nil
}

// Stop drains and unsubscribes all JetStream consumers.
func (c *OrderCompletedConsumer) Stop() error {
	var errs []error
	if c.sub != nil {
		if err := c.sub.Drain(); err != nil {
			errs = append(errs, err)
		}
	}
	if c.refundSub != nil {
		if err := c.refundSub.Drain(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (c *OrderCompletedConsumer) handleNATSMsg(parentCtx context.Context, subject string, msg *nats.Msg) {
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
	if len(headers) > 0 {
		msgCtx = telemetry.ExtractTraceHeaders(msgCtx, headers)
	}

	var (
		shouldAck bool
		retryErr  error
	)
	if subject == SubjectOrderRefunded || msg.Subject == SubjectOrderRefunded {
		shouldAck, retryErr = c.ProcessOrderRefundedMessage(msgCtx, msg.Data, headers, delivered)
	} else {
		shouldAck, retryErr = c.ProcessRawMessage(msgCtx, msg.Data, headers, delivered)
	}
	if shouldAck {
		if ackErr := msg.Ack(); ackErr != nil {
			c.logger.WarnContext(msgCtx, "failed to ack nats message",
				slog.String("subject", subject),
				slog.String("error", ackErr.Error()),
			)
		}
		return
	}
	delay := time.Duration(delivered) * time.Second
	if delay > 10*time.Second {
		delay = 10 * time.Second
	}
	c.logger.WarnContext(msgCtx, "nacking nats message for retry",
		slog.String("subject", subject),
		slog.Uint64("attempt", delivered),
		slog.String("error", fmt.Sprint(retryErr)),
	)
	_ = msg.NakWithDelay(delay)
}

// ProcessRawMessage parses, traces, and idempotently processes an `order.completed` event
// (or dispatches to `ProcessOrderRefundedMessage` when `event_type == "order.refunded"`).
// Returns (ack=true, nil) when handled or dead-lettered, or (ack=false, err) when transient retry is needed.
func (c *OrderCompletedConsumer) ProcessRawMessage(ctx context.Context, data []byte, headers map[string]string, attempt uint64) (bool, error) {
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		ctx = telemetry.ExtractTraceHeaders(ctx, headers)
		tracer := otel.Tracer("promotion")
		ctx, span := tracer.Start(ctx, "nats.consume "+SubjectOrderCompleted,
			trace.WithSpanKind(trace.SpanKindConsumer),
			trace.WithAttributes(attribute.String("messaging.destination", SubjectOrderCompleted)),
		)
		defer span.End()
		c.sendToDLQ(ctx, SubjectOrderCompleted, data, headers, "invalid event envelope json: "+err.Error())
		return true, nil
	}
	if strings.TrimSpace(env.EventType) == SubjectOrderRefunded {
		return c.ProcessOrderRefundedMessage(ctx, data, headers, attempt)
	}

	ctx = telemetry.ExtractTraceHeaders(ctx, headers)
	tracer := otel.Tracer("promotion")
	ctx, span := tracer.Start(ctx, "nats.consume "+SubjectOrderCompleted,
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(attribute.String("messaging.destination", SubjectOrderCompleted)),
	)
	defer span.End()

	if strings.TrimSpace(env.EventID) == "" {
		c.sendToDLQ(ctx, SubjectOrderCompleted, data, headers, "missing event_id")
		return true, nil
	}

	var payload OrderCompletedPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		c.sendToDLQ(ctx, SubjectOrderCompleted, data, headers, "invalid order.completed payload json: "+err.Error())
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
			c.sendToDLQ(ctx, SubjectOrderCompleted, data, headers, "max retries exceeded: "+err.Error())
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

// ProcessOrderRefundedMessage parses, traces, and idempotently processes an `order.refunded` event:
//   - When AllGroupOrdersClosed && CheckoutGroupID != "", invokes the optional ReleaseReservationFn.
//   - Restores proportional coins for partial group refunds when !AllGroupOrdersClosed && CoinsDiscountVND > 0.
//   - Claws back any `order_completed_reward` coins previously credited for order_id (capped at balance).
//
// Returns (ack=true, nil) when handled or dead-lettered to `dlq.order.refunded`,
// or (ack=false, err) when transient retry is needed.
func (c *OrderCompletedConsumer) ProcessOrderRefundedMessage(ctx context.Context, data []byte, headers map[string]string, attempt uint64) (bool, error) {
	ctx = telemetry.ExtractTraceHeaders(ctx, headers)
	tracer := otel.Tracer("promotion")
	ctx, span := tracer.Start(ctx, "nats.consume "+SubjectOrderRefunded,
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(attribute.String("messaging.destination", SubjectOrderRefunded)),
	)
	defer span.End()

	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		c.sendToDLQ(ctx, SubjectOrderRefunded, data, headers, "invalid event envelope json: "+err.Error())
		return true, nil
	}
	eventID := strings.TrimSpace(env.EventID)
	if eventID == "" {
		c.sendToDLQ(ctx, SubjectOrderRefunded, data, headers, "missing event_id")
		return true, nil
	}

	var payload OrderRefundedPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		c.sendToDLQ(ctx, SubjectOrderRefunded, data, headers, "invalid order.refunded payload json: "+err.Error())
		return true, nil
	}

	already, err := c.store.IsEventProcessed(ctx, eventID)
	if err != nil {
		if attempt >= MaxDeliveryAttempts {
			c.sendToDLQ(ctx, SubjectOrderRefunded, data, headers, "max retries exceeded: "+err.Error())
			return true, nil
		}
		return false, err
	}
	if already {
		c.logger.InfoContext(ctx, "processed order.refunded event",
			slog.String("event_id", eventID),
			slog.String("order_id", payload.OrderID),
			slog.Bool("already_processed", true),
		)
		return true, nil
	}

	checkoutGroupID := strings.TrimSpace(payload.CheckoutGroupID)
	if payload.AllGroupOrdersClosed && checkoutGroupID != "" && c.releaseReservationFn != nil {
		if err := c.releaseReservationFn(ctx, checkoutGroupID); err != nil {
			if attempt >= MaxDeliveryAttempts {
				c.sendToDLQ(ctx, SubjectOrderRefunded, data, headers, "max retries exceeded: "+err.Error())
				return true, nil
			}
			return false, err
		}
	}

	already, err = c.store.ProcessOrderRefundedEvent(ctx, store.OrderRefundedInput{
		EventID:              eventID,
		Subject:              SubjectOrderRefunded,
		OrderID:              strings.TrimSpace(payload.OrderID),
		BuyerID:              strings.TrimSpace(payload.BuyerID),
		CoinsDiscountVND:     payload.CoinsDiscountVND,
		AllGroupOrdersClosed: payload.AllGroupOrdersClosed,
	})
	if err != nil {
		if attempt >= MaxDeliveryAttempts {
			c.sendToDLQ(ctx, SubjectOrderRefunded, data, headers, "max retries exceeded: "+err.Error())
			return true, nil
		}
		return false, err
	}

	c.logger.InfoContext(ctx, "processed order.refunded event",
		slog.String("event_id", eventID),
		slog.String("order_id", payload.OrderID),
		slog.String("checkout_group_id", checkoutGroupID),
		slog.Int64("coins_discount_vnd", payload.CoinsDiscountVND),
		slog.Bool("all_group_orders_closed", payload.AllGroupOrdersClosed),
		slog.Bool("already_processed", already),
	)
	return true, nil
}

func (c *OrderCompletedConsumer) sendToDLQ(ctx context.Context, subject string, data []byte, origHeaders map[string]string, reason string) {
	c.logger.ErrorContext(ctx, "sending message to DLQ",
		slog.String("subject", subject),
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
	dlqHeaders["x-original-subject"] = subject
	telemetry.InjectTraceHeaders(ctx, dlqHeaders)

	if err := c.dlq.PublishDLQ(ctx, "dlq."+subject, data, dlqHeaders); err != nil {
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
