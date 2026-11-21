package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/search/internal/indexer"
)

const (
	DefaultPrimaryStream = "NEXUSMART"
	DefaultDLQStream     = "NEXUSMART_DLQ"
	ProductSubjectFilter = "product.>"
	DurableConsumerName  = "search-product-indexer-v1"
	QueueGroupName       = "search-workers"
)

// EventProcessor processes a decoded canonical event.
type EventProcessor interface {
	ProcessEvent(ctx context.Context, subject string, env indexer.Envelope) (duplicate bool, err error)
}

// DeadLetterStore persists dead-lettered messages in Postgres.
type DeadLetterStore interface {
	InsertDeadLetter(ctx context.Context, eventID, subject string, payload []byte, errMsg string, attempts int) error
}

// DLQPublisher publishes poison messages to NEXUSMART_DLQ (subject dlq.<original_subject>).
type DLQPublisher interface {
	PublishDLQ(subject string, data []byte) error
}

// StreamManager abstracts JetStream stream administration for EnsureStreams unit testing.
type StreamManager interface {
	StreamInfo(stream string, opts ...nats.JSOpt) (*nats.StreamInfo, error)
	AddStream(cfg *nats.StreamConfig, opts ...nats.JSOpt) (*nats.StreamInfo, error)
	UpdateStream(cfg *nats.StreamConfig, opts ...nats.JSOpt) (*nats.StreamInfo, error)
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

// EnsureStreams idempotently creates or updates NEXUSMART (ensuring "product.>" is subscribed)
// and ensures NEXUSMART_DLQ ("dlq.>") exists.
func EnsureStreams(js StreamManager, primaryStream, dlqStream string) error {
	if primaryStream == "" {
		primaryStream = DefaultPrimaryStream
	}
	if dlqStream == "" {
		dlqStream = DefaultDLQStream
	}

	if err := ensurePrimaryStreamWithProduct(js, primaryStream); err != nil {
		return fmt.Errorf("ensure stream %s: %w", primaryStream, err)
	}

	if err := ensureDLQStream(js, dlqStream); err != nil {
		return fmt.Errorf("ensure dlq stream %s: %w", dlqStream, err)
	}
	return nil
}

func ensurePrimaryStreamWithProduct(js StreamManager, streamName string) error {
	info, err := js.StreamInfo(streamName)
	if err == nil && info != nil {
		if containsSubject(info.Config.Subjects, ProductSubjectFilter) {
			return nil
		}
		updated := info.Config
		updated.Subjects = append(append([]string(nil), info.Config.Subjects...), ProductSubjectFilter)
		if _, updErr := js.UpdateStream(&updated); updErr != nil {
			return updErr
		}
		return nil
	}
	if err != nil && !errors.Is(err, nats.ErrStreamNotFound) {
		return err
	}

	cfg := &nats.StreamConfig{
		Name:       streamName,
		Subjects:   []string{"user.>", "order.>", "payment.>", "shipment.>", ProductSubjectFilter},
		Retention:  nats.LimitsPolicy,
		Storage:    nats.FileStorage,
		Duplicates: 2 * time.Minute,
	}
	if _, addErr := js.AddStream(cfg); addErr != nil {
		if strings.Contains(strings.ToLower(addErr.Error()), "already in use") {
			existing, infoErr := js.StreamInfo(streamName)
			if infoErr == nil && existing != nil {
				if containsSubject(existing.Config.Subjects, ProductSubjectFilter) {
					return nil
				}
				updated := existing.Config
				updated.Subjects = append(append([]string(nil), existing.Config.Subjects...), ProductSubjectFilter)
				_, updErr := js.UpdateStream(&updated)
				return updErr
			}
		}
		return addErr
	}
	return nil
}

func ensureDLQStream(js StreamManager, dlqStream string) error {
	if _, err := js.StreamInfo(dlqStream); err == nil {
		return nil
	} else if !errors.Is(err, nats.ErrStreamNotFound) {
		return err
	}

	cfg := &nats.StreamConfig{
		Name:      dlqStream,
		Subjects:  []string{"dlq.>"},
		Retention: nats.LimitsPolicy,
		Storage:   nats.FileStorage,
	}
	if _, err := js.AddStream(cfg); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "already in use") {
			if _, infoErr := js.StreamInfo(dlqStream); infoErr == nil {
				return nil
			}
		}
		return err
	}
	return nil
}

func containsSubject(subjects []string, target string) bool {
	for _, s := range subjects {
		if s == target {
			return true
		}
	}
	return false
}

// Consumer consumes product.> events from NEXUSMART idempotently with retry and DLQ routing.
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

func (p jsDLQPublisher) PublishDLQ(subject string, data []byte) error {
	msg := nats.NewMsg(subject)
	msg.Data = data
	_, err := p.js.PublishMsg(msg)
	return err
}

// NewConsumer creates a JetStream consumer backed by NATS and Postgres DLQ storage.
func NewConsumer(
	js nats.JetStreamContext,
	processor EventProcessor,
	dlqStore DeadLetterStore,
	stream string,
	maxAttempts uint64,
	logger *slog.Logger,
) *Consumer {
	if stream == "" {
		stream = DefaultPrimaryStream
	}
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
		dlqStore:    dlqStore,
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

// Start creates a durable JetStream queue subscription consuming product.> on NEXUSMART.
func (c *Consumer) Start(ctx context.Context) error {
	sub, err := c.js.QueueSubscribe(
		ProductSubjectFilter,
		QueueGroupName,
		func(msg *nats.Msg) {
			c.HandleMessage(ctx, natsMessageAdapter{msg: msg})
		},
		nats.BindStream(c.stream),
		nats.Durable(DurableConsumerName),
		nats.ManualAck(),
		nats.AckWait(30*time.Second),
		nats.MaxDeliver(int(c.maxAttempts)+1),
	)
	if err != nil {
		return fmt.Errorf("subscribe search consumer on stream %s: %w", c.stream, err)
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

	delivered := msg.NumDelivered()
	if delivered == 0 {
		delivered = 1
	}

	var env indexer.Envelope
	if err := json.Unmarshal(msg.Data(), &env); err != nil || strings.TrimSpace(env.EventID) == "" {
		errMsg := "malformed event envelope"
		if err != nil {
			errMsg = fmt.Sprintf("malformed event envelope: %v", err)
		}
		c.routeToDLQ(ctx, env.EventID, msg.Subject(), msg.Data(), errMsg, int(delivered))
		_ = msg.Ack()
		return
	}

	duplicate, err := c.processor.ProcessEvent(ctx, msg.Subject(), env)
	if err != nil {
		c.logger.ErrorContext(ctx, "search event processing failed",
			slog.String("subject", msg.Subject()),
			slog.String("event_id", env.EventID),
			slog.Uint64("attempt", delivered),
			slog.String("error", err.Error()),
		)
		if errors.Is(err, indexer.ErrMalformedPayload) || delivered >= c.maxAttempts {
			c.routeToDLQ(ctx, env.EventID, msg.Subject(), msg.Data(), err.Error(), int(delivered))
			_ = msg.Ack()
			return
		}
		_ = msg.NakWithDelay(time.Duration(delivered) * time.Second)
		return
	}

	if duplicate {
		c.logger.DebugContext(ctx, "skipped duplicate search event",
			slog.String("subject", msg.Subject()),
			slog.String("event_id", env.EventID),
		)
	}
	_ = msg.Ack()
}

func (c *Consumer) routeToDLQ(ctx context.Context, eventID, subject string, raw []byte, errMsg string, attempts int) {
	c.logger.WarnContext(ctx, "routing poison message to DLQ",
		slog.String("event_id", eventID),
		slog.String("subject", subject),
		slog.Int("attempts", attempts),
		slog.String("error", errMsg),
	)

	if c.dlqStore != nil {
		if err := c.dlqStore.InsertDeadLetter(ctx, eventID, subject, raw, errMsg, attempts); err != nil {
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
			"error":            errMsg,
			"attempts":         attempts,
			"failed_at":        time.Now().UTC().Format(time.RFC3339),
			"original_payload": string(raw),
		})
		dlqSubject := "dlq." + subject
		if err := c.dlqPub.PublishDLQ(dlqSubject, dlqPayload); err != nil {
			c.logger.ErrorContext(ctx, "failed to publish to NEXUSMART_DLQ",
				slog.String("dlq_subject", dlqSubject),
				slog.String("error", err.Error()),
			)
		}
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
