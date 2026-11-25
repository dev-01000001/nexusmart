package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nexusmart/core/pkg/common/jetstream"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/notification/internal/service"
	"github.com/nexusmart/core/services/notification/internal/store"
)

// EventProcessor processes a decoded canonical event.
type EventProcessor interface {
	ProcessEvent(ctx context.Context, subject string, env service.Envelope) (duplicate bool, err error)
}

// DeadLetterStore persists dead-lettered messages in Postgres.
type DeadLetterStore interface {
	InsertDeadLetter(ctx context.Context, eventID, subject string, payload []byte, errMsg string, attempts int) error
}

const (
	messageProcessTimeout = 25 * time.Second
	dlqWriteTimeout       = 5 * time.Second
)

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

// EnsureStreams idempotently creates or union-updates NEXUSMART and NEXUSMART_DLQ streams.
func EnsureStreams(js nats.JetStreamContext, primaryStream, dlqStream string) error {
	return jetstream.EnsureStreams(js, primaryStream, dlqStream)
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
	mu          sync.Mutex
	stopping    bool
	wg          sync.WaitGroup
}

type jsDLQPublisher struct {
	js nats.JetStreamContext
}

func (p jsDLQPublisher) PublishDLQ(ctx context.Context, subject string, data []byte) error {
	msg := nats.NewMsg(subject)
	msg.Data = data
	_, err := p.js.PublishMsg(msg, nats.Context(ctx))
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
	c.mu.Lock()
	c.stopping = false
	c.mu.Unlock()

	_, err := c.js.QueueSubscribe(
		"",
		"notification-workers",
		func(msg *nats.Msg) {
			c.onMessage(ctx, natsMessageAdapter{msg: msg})
		},
		nats.BindStream(c.stream),
		nats.Durable("notification-events-v1"),
		nats.ManualAck(),
		nats.AckWait(30*time.Second),
		nats.MaxDeliver(int(c.maxAttempts)+1),
	)
	if err != nil {
		_ = c.Close()
		return fmt.Errorf("subscribe notification consumer on stream %s: %w", c.stream, err)
	}
	return nil
}

func (c *Consumer) isStopping() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stopping
}

func (c *Consumer) isShuttingDown(ctx context.Context) bool {
	return c.isStopping() || errors.Is(ctx.Err(), context.Canceled)
}

func (c *Consumer) onMessage(ctx context.Context, msg Message) {
	c.mu.Lock()
	if c.stopping || errors.Is(ctx.Err(), context.Canceled) {
		c.mu.Unlock()
		// E1: Do NOT Ack or Nak while shutting down — AckWait redelivers cleanly after nc.Close().
		return
	}
	c.wg.Add(1)
	c.mu.Unlock()
	defer c.wg.Done()
	c.HandleMessage(ctx, msg)
}

// Close prevents new message callbacks from entering and waits for every in-flight message
// handler to finish before returning (safe to call multiple times).
// Intentionally does NOT call sub.Drain() or sub.Unsubscribe(): in nats.go v1.36, QueueSubscribe
// marks library-created durable consumers with jsi.dc=true, so Drain/Unsubscribe deletes the
// shared durable consumer on the server, disrupting peer replicas and replaying the stream on restart.
// Closing nc in main detaches cleanly while preserving durables.
func (c *Consumer) Close() error {
	c.mu.Lock()
	c.stopping = true
	c.mu.Unlock()
	c.wg.Wait()
	return nil
}

// HandleMessage decodes, traces, processes idempotently, retries transient errors,
// and routes poison/exhausted messages to NEXUSMART_DLQ + dead_letter_events.
func (c *Consumer) HandleMessage(baseCtx context.Context, msg Message) {
	if c.isShuttingDown(baseCtx) {
		return
	}

	procCtx, cancel := context.WithTimeout(baseCtx, messageProcessTimeout)
	defer cancel()

	carrier := map[string]string{}
	if tp := msg.Header("traceparent"); tp != "" {
		carrier["traceparent"] = tp
	}
	if ts := msg.Header("tracestate"); ts != "" {
		carrier["tracestate"] = ts
	}
	ctx, span := telemetry.StartConsumerSpan(procCtx, "notification", msg.Subject(), carrier)
	defer span.End()

	delivered := msg.NumDelivered()
	if delivered == 0 {
		delivered = 1
	}

	var env service.Envelope
	if err := json.Unmarshal(msg.Data(), &env); err != nil || strings.TrimSpace(env.EventID) == "" {
		if c.isShuttingDown(baseCtx) {
			return
		}
		errMsg := "malformed event envelope"
		if err != nil {
			errMsg = fmt.Sprintf("malformed event envelope: %v", err)
		}
		if c.routeToDLQ(ctx, env.EventID, msg.Subject(), msg.Data(), errMsg, int(delivered)) {
			_ = msg.Ack()
			return
		}
		if !c.isShuttingDown(baseCtx) {
			_ = msg.NakWithDelay(time.Duration(delivered) * time.Second)
		}
		return
	}

	duplicate, err := c.processor.ProcessEvent(ctx, msg.Subject(), env)
	if err != nil {
		// E1: Never route to DLQ, Ack, or Nak when shutdown cancelled processing.
		if c.isShuttingDown(baseCtx) {
			return
		}
		c.logger.ErrorContext(ctx, "notification event processing failed",
			slog.String("subject", msg.Subject()),
			slog.String("event_id", env.EventID),
			slog.Uint64("attempt", delivered),
			slog.String("error", err.Error()),
		)
		if errors.Is(err, service.ErrMalformedPayload) || delivered >= c.maxAttempts {
			if c.routeToDLQ(ctx, env.EventID, msg.Subject(), msg.Data(), err.Error(), int(delivered)) {
				_ = msg.Ack()
				return
			}
			if !c.isShuttingDown(baseCtx) {
				_ = msg.NakWithDelay(time.Duration(delivered) * time.Second)
			}
			return
		}
		_ = msg.NakWithDelay(time.Duration(delivered) * time.Second)
		return
	}

	c.logger.InfoContext(ctx, "processed notification event",
		slog.String("subject", msg.Subject()),
		slog.String("event_id", env.EventID),
		slog.Bool("duplicate", duplicate),
	)
	_ = msg.Ack()
}

// routeToDLQ persists the sanitized poison message to dead_letter_events and/or NEXUSMART_DLQ
// using a detached bounded context. Returns true iff at least one durable DLQ sink succeeded.
func (c *Consumer) routeToDLQ(ctx context.Context, eventID, subject string, raw []byte, errMsg string, attempts int) bool {
	safeRaw := sanitizeDLQPayload(subject, raw)
	c.logger.WarnContext(ctx, "routing poison message to DLQ",
		slog.String("event_id", eventID),
		slog.String("subject", subject),
		slog.Int("attempts", attempts),
		slog.String("error", errMsg),
	)

	dlqCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), dlqWriteTimeout)
	defer cancel()

	saved := false
	if c.dlqStore != nil {
		if err := c.dlqStore.InsertDeadLetter(dlqCtx, eventID, subject, safeRaw, errMsg, attempts); err != nil {
			c.logger.ErrorContext(dlqCtx, "failed to save dead_letter_events record",
				slog.String("event_id", eventID),
				slog.String("error", err.Error()),
			)
		} else {
			saved = true
		}
	}

	if c.dlqPub != nil {
		dlqPayload, err := json.Marshal(map[string]any{
			"event_id":         eventID,
			"original_subject": subject,
			"error":            errMsg,
			"attempts":         attempts,
			"failed_at":        time.Now().UTC().Format(time.RFC3339),
			"original_payload": string(safeRaw),
		})
		if err != nil {
			c.logger.ErrorContext(dlqCtx, "failed to marshal NEXUSMART_DLQ payload",
				slog.String("event_id", eventID),
				slog.String("error", err.Error()),
			)
			return saved
		}
		dlqSubject := "dlq." + subject
		if err := c.dlqPub.PublishDLQ(dlqCtx, dlqSubject, dlqPayload); err != nil {
			c.logger.ErrorContext(dlqCtx, "failed to publish to NEXUSMART_DLQ",
				slog.String("dlq_subject", dlqSubject),
				slog.String("error", err.Error()),
			)
		} else {
			saved = true
		}
	}
	return saved
}

var sensitivePayloadFields = map[string]struct{}{
	"reset_token":     {},
	"otp":             {},
	"account_number":  {},
	"street_address":  {},
	"address_line":    {},
	"recipient_phone": {},
	"phone":           {},
	"recipient_name":  {},
}

func isSensitiveSubject(subject string) bool {
	switch strings.TrimSpace(subject) {
	case "user.password_reset_requested", "user.registered", "user.shop_bank_account_otp", "user.phone_otp_requested", "order.address_updated":
		return true
	default:
		return false
	}
}

// sanitizeDLQPayload redacts secret tokens and recipient PII (e.g. reset_token, otp, account_number, street_address, phone) before persisting to dead_letter_events or NEXUSMART_DLQ.
func sanitizeDLQPayload(subject string, raw []byte) []byte {
	var top any
	if err := json.Unmarshal(raw, &top); err != nil {
		if isSensitiveSubject(subject) {
			return []byte(`{"redacted":true}`)
		}
		return raw
	}
	if !redactValue(top) {
		return raw
	}
	encoded, err := json.Marshal(top)
	if err != nil {
		return []byte(`{"redacted":true}`)
	}
	return encoded
}

func redactValue(v any) bool {
	changed := false
	switch node := v.(type) {
	case map[string]any:
		for k, val := range node {
			if _, sensitive := sensitivePayloadFields[strings.ToLower(strings.TrimSpace(k))]; sensitive {
				node[k] = "[REDACTED]"
				changed = true
				continue
			}
			if redactValue(val) {
				changed = true
			}
		}
	case []any:
		for _, item := range node {
			if redactValue(item) {
				changed = true
			}
		}
	}
	return changed
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
