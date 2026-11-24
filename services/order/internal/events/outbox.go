package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nexusmart/core/pkg/common/jetstream"
	"github.com/nexusmart/core/services/order/internal/db"
)

// OutboxStore is the minimal store interface needed by OutboxRelay.
type OutboxStore interface {
	ProcessPendingOutbox(ctx context.Context, limit int32, publishFn func(ctx context.Context, ev db.OutboxEvent) error) (int, error)
}

// Publisher abstracts JetStream PublishMsg for testability.
type Publisher interface {
	PublishMsg(m *nats.Msg, opts ...nats.PubOpt) (*nats.PubAck, error)
}

// EnsureStreams idempotently ensures NEXUSMART and NEXUSMART_DLQ JetStream streams exist
// with all canonical subjects union-merged.
func EnsureStreams(js nats.JetStreamContext, streamName string) error {
	return jetstream.EnsureStreams(js, streamName, DLQStreamName)
}

// OutboxRelay polls outbox_events and publishes canonical JSON envelopes to NATS JetStream.
type OutboxRelay struct {
	store        OutboxStore
	publisher    Publisher
	logger       *slog.Logger
	pollInterval time.Duration
	batchSize    int32
	triggerCh    <-chan struct{}
}

// NewOutboxRelay builds a new OutboxRelay.
func NewOutboxRelay(store OutboxStore, publisher Publisher, logger *slog.Logger, pollInterval time.Duration, triggerCh <-chan struct{}) *OutboxRelay {
	if logger == nil {
		logger = slog.Default()
	}
	if pollInterval <= 0 {
		pollInterval = jetstream.DefaultOutboxPollInterval
	}
	return &OutboxRelay{
		store:        store,
		publisher:    publisher,
		logger:       logger,
		pollInterval: pollInterval,
		batchSize:    50,
		triggerCh:    triggerCh,
	}
}

// Run polls and flushes outbox events until ctx is cancelled, applying shared D9-bis exponential
// backoff (250ms -> 5s, ignoring triggerCh during backoff) whenever a poll/publish fails.
func (r *OutboxRelay) Run(ctx context.Context) {
	jetstream.RunOutboxRelayLoop(ctx, r.pollInterval, r.triggerCh, func(c context.Context) error {
		_, err := r.flushOnceErr(c)
		return err
	})
}

// FlushOnce publishes pending outbox events in batches until drained or error.
func (r *OutboxRelay) FlushOnce(ctx context.Context) int {
	n, _ := r.flushOnceErr(ctx)
	return n
}

func (r *OutboxRelay) flushOnceErr(ctx context.Context) (int, error) {
	totalPublished := 0
	for {
		if ctx.Err() != nil {
			return totalPublished, ctx.Err()
		}
		n, err := r.store.ProcessPendingOutbox(ctx, r.batchSize, r.publishOutboxEvent)
		totalPublished += n
		if err != nil {
			r.logger.WarnContext(ctx, "outbox relay poll failed", "error", err)
			return totalPublished, err
		}
		if int32(n) < r.batchSize {
			return totalPublished, nil
		}
	}
}

// publishOutboxEvent marshals the canonical envelope and publishes it to NATS JetStream.
// Per-event publish timeout is enforced once by ProcessPendingOutbox on ctx (single source of truth).
func (r *OutboxRelay) publishOutboxEvent(ctx context.Context, ev db.OutboxEvent) error {
	env := Envelope{
		EventID:    ev.ID,
		EventType:  ev.EventType,
		TraceID:    ev.TraceID,
		OccurredAt: ev.CreatedAt.UTC(),
		Payload:    json.RawMessage(ev.PayloadJSON),
	}
	body, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal outbox envelope %s: %w", ev.ID, err)
	}

	msg := &nats.Msg{
		Subject: ev.EventType,
		Data:    body,
		Header:  nats.Header{},
	}
	msg.Header.Set(nats.MsgIdHdr, ev.ID)
	if ev.Traceparent != "" {
		msg.Header.Set("traceparent", ev.Traceparent)
	}

	if _, err := r.publisher.PublishMsg(msg, nats.Context(ctx)); err != nil {
		return fmt.Errorf("publish %s (%s): %w", ev.EventType, ev.ID, err)
	}
	return nil
}
