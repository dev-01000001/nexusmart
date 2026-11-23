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
		pollInterval = 250 * time.Millisecond
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

// Run polls and flushes outbox events until ctx is cancelled.
func (r *OutboxRelay) Run(ctx context.Context) {
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()

	for {
		r.FlushOnce(ctx)

		select {
		case <-ctx.Done():
			return
		case <-r.triggerCh:
		case <-ticker.C:
		}
	}
}

// FlushOnce publishes pending outbox events in batches until drained or error.
func (r *OutboxRelay) FlushOnce(ctx context.Context) int {
	totalPublished := 0
	for {
		if ctx.Err() != nil {
			return totalPublished
		}
		n, err := r.store.ProcessPendingOutbox(ctx, r.batchSize, r.publishOutboxEvent)
		if err != nil {
			r.logger.WarnContext(ctx, "outbox relay poll failed", "error", err)
			return totalPublished
		}
		totalPublished += n
		if int32(n) < r.batchSize {
			return totalPublished
		}
	}
}

func (r *OutboxRelay) publishOutboxEvent(_ context.Context, ev db.OutboxEvent) error {
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

	if _, err := r.publisher.PublishMsg(msg); err != nil {
		return fmt.Errorf("publish %s (%s): %w", ev.EventType, ev.ID, err)
	}
	return nil
}
