package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nexusmart/core/pkg/common/jetstream"
	"github.com/nexusmart/core/services/shipping/internal/store"
)

// OutboxStore is the minimal persistence contract required by OutboxRelay.
type OutboxStore interface {
	ProcessPendingOutbox(ctx context.Context, limit int, publishFn func(ctx context.Context, ev store.OutboxEvent) error) (int, error)
}

// Publisher abstracts JetStream PublishMsg for testability.
type Publisher interface {
	PublishMsg(m *nats.Msg, opts ...nats.PubOpt) (*nats.PubAck, error)
}

// OutboxRelay polls outbox_events via lease-based claiming and publishes canonical envelopes to NATS JetStream.
type OutboxRelay struct {
	store          OutboxStore
	pub            Publisher
	interval       time.Duration
	publishTimeout time.Duration
	batchSize      int
	notifyCh       chan struct{}
	logger         *slog.Logger
}

// NewOutboxRelay constructs an outbox relay for publishing shipping events.
func NewOutboxRelay(st OutboxStore, pub Publisher, interval time.Duration, logger *slog.Logger) *OutboxRelay {
	if interval <= 0 {
		interval = jetstream.DefaultOutboxPollInterval
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &OutboxRelay{
		store:          st,
		pub:            pub,
		interval:       interval,
		publishTimeout: jetstream.DefaultOutboxPublishTimeout,
		batchSize:      50,
		notifyCh:       make(chan struct{}, 1),
		logger:         logger,
	}
}

// Wake nudges the relay to immediately flush pending outbox rows.
func (r *OutboxRelay) Wake() {
	select {
	case r.notifyCh <- struct{}{}:
	default:
	}
}

// Run polls and relays pending outbox events until ctx is cancelled,
// backing off exponentially on infra/DB errors while ignoring Wake() nudges.
func (r *OutboxRelay) Run(ctx context.Context) {
	jetstream.RunOutboxRelayLoop(ctx, r.interval, r.notifyCh, func(c context.Context) error {
		_, err := r.flushOnce(c)
		return err
	})
}

// FlushOnce publishes pending outbox events in batches until drained, lease-budget exhausted, or error.
func (r *OutboxRelay) FlushOnce(ctx context.Context) int {
	n, _ := r.flushOnce(ctx)
	return n
}

func (r *OutboxRelay) flushOnce(ctx context.Context) (int, error) {
	if r.pub == nil || r.store == nil {
		return 0, nil
	}
	totalPublished := 0
	for {
		if ctx.Err() != nil {
			return totalPublished, ctx.Err()
		}
		n, err := r.store.ProcessPendingOutbox(ctx, r.batchSize, r.publishOutboxEvent)
		totalPublished += n
		if err != nil {
			r.logger.ErrorContext(ctx, "process pending outbox events failed", slog.String("error", err.Error()))
			return totalPublished, err
		}
		if n < r.batchSize {
			return totalPublished, nil
		}
	}
}

func (r *OutboxRelay) publishOutboxEvent(ctx context.Context, ev store.OutboxEvent) error {
	env := Envelope{
		EventID:    ev.EventID,
		EventType:  ev.Subject,
		TraceID:    ev.TraceID,
		OccurredAt: ev.CreatedAt.UTC().Format(time.RFC3339),
		Payload:    json.RawMessage(ev.Payload),
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal outbox envelope %s: %w", ev.EventID, err)
	}

	msg := nats.NewMsg(ev.Subject)
	msg.Data = raw
	msg.Header.Set(nats.MsgIdHdr, ev.EventID)
	if ev.Traceparent != "" {
		msg.Header.Set("traceparent", ev.Traceparent)
	}

	pubCtx := ctx
	var cancel context.CancelFunc
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && r.publishTimeout > 0 {
		pubCtx, cancel = context.WithTimeout(ctx, r.publishTimeout)
		defer cancel()
	}

	if _, err := r.pub.PublishMsg(msg, nats.Context(pubCtx)); err != nil {
		return fmt.Errorf("publish outbox event %s (%s): %w", ev.Subject, ev.EventID, err)
	}
	return nil
}
