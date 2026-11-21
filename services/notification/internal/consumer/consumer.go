package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/notification/internal/service"
	"github.com/nexusmart/core/services/notification/internal/store"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

var rawOTPFieldPattern = regexp.MustCompile(`(?i)("otp"\s*:\s*)(?:"[^"]*"|\d+)`)

// EventProcessor processes a decoded canonical event.
type EventProcessor interface {
	ProcessEvent(ctx context.Context, subject string, env service.Envelope) (duplicate bool, err error)
}

// DeadLetterStore persists dead-lettered messages in Postgres.
type DeadLetterStore interface {
	InsertDeadLetter(ctx context.Context, eventID, subject string, payload []byte, errMsg string, attempts int) error
}

// DLQPublisher publishes poison messages to NEXUSMART_DLQ (subject dlq.<original_subject>).
type DLQPublisher interface {
	PublishDLQ(ctx context.Context, subject string, data []byte) error
}

// Message abstracts *nats.Msg for clean unit testing of retry + DLQ routing.
type Message interface {
	Subject() string
	Data() []byte
	Header(key string) string
	NumDelivered() uint64
	Ack() error
	NakWithDelay(delay time.Duration) error
}

// EnsureStreams idempotently creates NEXUSMART and NEXUSMART_DLQ streams.
func EnsureStreams(js nats.JetStreamContext, primaryStream, dlqStream string) error {
	if primaryStream == "" {
		primaryStream = "NEXUSMART"
	}
	if dlqStream == "" {
		dlqStream = "NEXUSMART_DLQ"
	}

	if err := ensureSingleStream(js, &nats.StreamConfig{
		Name:       primaryStream,
		Subjects:   []string{"user.>", "order.>", "payment.>", "shipment.>"},
		Retention:  nats.LimitsPolicy,
		Storage:    nats.FileStorage,
		Duplicates: 2 * time.Minute,
	}); err != nil {
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

// Consumer consumes all NEXUSMART events idempotently with Dead Letter Queue routing.
type Consumer struct {
	js          nats.JetStreamContext
	processor   EventProcessor
	dlqStore    DeadLetterStore
	dlqPub      DLQPublisher
	stream      string
	maxAttempts uint64
	logger      *slog.Logger
	sub         *nats.Subscription
}

type jsDLQPublisher struct {
	js nats.JetStreamContext
}

func newDLQMessage(ctx context.Context, subject string, data []byte) *nats.Msg {
	msg := nats.NewMsg(subject)
	msg.Data = data
	if msg.Header == nil {
		msg.Header = nats.Header{}
	}
	carrier := map[string]string{}
	telemetry.InjectTraceHeaders(ctx, carrier)
	for k, v := range carrier {
		msg.Header.Set(k, v)
	}
	return msg
}

func (p jsDLQPublisher) PublishDLQ(ctx context.Context, subject string, data []byte) error {
	_, err := p.js.PublishMsg(newDLQMessage(ctx, subject, data))
	return err
}

// NewConsumer creates a JetStream consumer backed by NATS and Postgres DLQ storage.
func NewConsumer(
	js nats.JetStreamContext,
	processor EventProcessor,
	repo store.Repository,
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
		processor:   processor,
		dlqStore:    repo,
		dlqPub:      pub,
		stream:      stream,
		maxAttempts: maxAttempts,
		logger:      logger,
	}
}

// NewTestableConsumer constructs a Consumer with injectable DLQ publisher for unit tests.
func NewTestableConsumer(
	processor EventProcessor,
	dlqStore DeadLetterStore,
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
		processor:   processor,
		dlqStore:    dlqStore,
		dlqPub:      dlqPub,
		maxAttempts: maxAttempts,
		logger:      logger,
	}
}

// Start creates a durable JetStream queue subscription consuming all subjects on NEXUSMART.
func (c *Consumer) Start(ctx context.Context) error {
	sub, err := c.js.QueueSubscribe(
		"",
		"notification-workers",
		func(msg *nats.Msg) {
			c.HandleMessage(ctx, natsMessageAdapter{msg: msg})
		},
		nats.BindStream(c.stream),
		nats.Durable("notification-events-v1"),
		nats.ManualAck(),
		nats.AckWait(30*time.Second),
		nats.MaxDeliver(int(c.maxAttempts)+1),
	)
	if err != nil {
		return fmt.Errorf("subscribe notification consumer on stream %s: %w", c.stream, err)
	}
	c.sub = sub
	return nil
}

// Close drains the NATS subscription gracefully.
func (c *Consumer) Close() error {
	if c.sub != nil {
		return c.sub.Drain()
	}
	return nil
}

// HandleMessage decodes, traces, processes idempotently, retries transient errors,
// and routes poison/exhausted messages to NEXUSMART_DLQ + dead_letter_events.
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

	ctx, span := otel.Tracer("notification").Start(ctx, "nats.consume "+msg.Subject(),
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			attribute.String("messaging.system", "nats"),
			attribute.String("messaging.destination.name", msg.Subject()),
		),
	)
	defer span.End()

	delivered := msg.NumDelivered()
	if delivered == 0 {
		delivered = 1
	}

	var env service.Envelope
	if err := json.Unmarshal(msg.Data(), &env); err != nil || strings.TrimSpace(env.EventID) == "" {
		errMsg := "malformed event envelope"
		if err != nil {
			errMsg = fmt.Sprintf("malformed event envelope: %v", err)
			span.RecordError(err)
		}
		span.SetStatus(otelcodes.Error, errMsg)
		c.routeToDLQ(ctx, env.EventID, msg.Subject(), msg.Data(), errMsg, int(delivered))
		_ = msg.Ack()
		return
	}

	duplicate, err := c.processor.ProcessEvent(ctx, msg.Subject(), env)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, err.Error())
		c.logger.ErrorContext(ctx, "notification event processing failed",
			slog.String("subject", msg.Subject()),
			slog.String("event_id", env.EventID),
			slog.Uint64("attempt", delivered),
			slog.String("error", err.Error()),
		)
		if errors.Is(err, service.ErrMalformedPayload) || delivered >= c.maxAttempts {
			c.routeToDLQ(ctx, env.EventID, msg.Subject(), msg.Data(), err.Error(), int(delivered))
			_ = msg.Ack()
			return
		}
		_ = msg.NakWithDelay(time.Duration(delivered) * time.Second)
		return
	}

	if duplicate {
		c.logger.DebugContext(ctx, "skipped duplicate notification event",
			slog.String("subject", msg.Subject()),
			slog.String("event_id", env.EventID),
		)
	}
	_ = msg.Ack()
}

func (c *Consumer) routeToDLQ(ctx context.Context, eventID, subject string, raw []byte, errMsg string, attempts int) {
	safeRaw := sanitizeDLQPayload(raw)
	safeErrMsg := logger.ScrubSecrets(errMsg)

	c.logger.WarnContext(ctx, "routing poison message to DLQ",
		slog.String("event_id", eventID),
		slog.String("subject", subject),
		slog.Int("attempts", attempts),
		slog.String("error", safeErrMsg),
	)

	if c.dlqStore != nil {
		if err := c.dlqStore.InsertDeadLetter(ctx, eventID, subject, safeRaw, safeErrMsg, attempts); err != nil {
			c.logger.ErrorContext(ctx, "failed to save dead_letter_events record",
				slog.String("event_id", eventID),
				slog.String("error", err.Error()),
			)
		}
	}

	if c.dlqPub != nil {
		dlqPayload, _ := json.Marshal(map[string]any{
			"event_id":         eventID,
			"original_subject": subject,
			"error":            safeErrMsg,
			"attempts":         attempts,
			"failed_at":        time.Now().UTC().Format(time.RFC3339),
			"original_payload": string(safeRaw),
		})
		dlqSubject := "dlq." + subject
		if err := c.dlqPub.PublishDLQ(ctx, dlqSubject, dlqPayload); err != nil {
			c.logger.ErrorContext(ctx, "failed to publish to NEXUSMART_DLQ",
				slog.String("dlq_subject", dlqSubject),
				slog.String("error", err.Error()),
			)
		}
	}
}

func sanitizeDLQPayload(raw []byte) []byte {
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err == nil {
		sanitized := sanitizeJSONValue(decoded)
		if out, marshalErr := json.Marshal(sanitized); marshalErr == nil {
			return out
		}
	}
	scrubbed := rawOTPFieldPattern.ReplaceAllString(string(raw), `${1}"[REDACTED]"`)
	return []byte(logger.ScrubSecrets(scrubbed))
}

func sanitizeJSONValue(v any) any {
	switch val := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, item := range val {
			switch child := item.(type) {
			case map[string]any, []any:
				out[k] = sanitizeJSONValue(child)
			case string:
				out[k] = logger.RedactValue(k, child)
			case nil:
				out[k] = nil
			default:
				lower := strings.ToLower(k)
				switch {
				case strings.Contains(lower, "phone"):
					out[k] = logger.MaskPhone(fmt.Sprint(child))
				case strings.Contains(lower, "email"):
					out[k] = logger.MaskEmail(fmt.Sprint(child))
				case logger.RedactValue(k, "probe") == "[REDACTED]":
					out[k] = "[REDACTED]"
				default:
					out[k] = child
				}
			}
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, item := range val {
			out[i] = sanitizeJSONValue(item)
		}
		return out
	case string:
		return logger.ScrubSecrets(val)
	default:
		return v
	}
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
