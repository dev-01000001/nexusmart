package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nexusmart/core/services/shipping/internal/store"
)

// OutboxRelay polls outbox_events and publishes canonical envelopes to NATS JetStream.
type OutboxRelay struct {
	repo     store.Repository
	js       nats.JetStreamContext
	interval time.Duration
	notifyCh chan struct{}
	logger   *slog.Logger
}

// NewOutboxRelay constructs an outbox relay for publishing shipping events.
func NewOutboxRelay(repo store.Repository, js nats.JetStreamContext, interval time.Duration, logger *slog.Logger) *OutboxRelay {
	if interval <= 0 {
		interval = 250 * time.Millisecond
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &OutboxRelay{
		repo:     repo,
		js:       js,
		interval: interval,
		notifyCh: make(chan struct{}, 1),
		logger:   logger,
	}
}

// Wake nudges the relay to immediately flush pending outbox rows.
func (r *OutboxRelay) Wake() {
	select {
	case r.notifyCh <- struct{}{}:
	default:
	}
}

// Run polls and relays pending outbox events until ctx is cancelled.
func (r *OutboxRelay) Run(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		r.FlushOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-r.notifyCh:
		case <-ticker.C:
		}
	}
}

// FlushOnce publishes up to 50 pending outbox events to NATS JetStream.
func (r *OutboxRelay) FlushOnce(ctx context.Context) {
	if r.js == nil {
		return
	}
	pending, err := r.repo.FetchPendingOutbox(ctx, 50)
	if err != nil {
		r.logger.ErrorContext(ctx, "fetch pending outbox events failed", slog.String("error", err.Error()))
		return
	}
	for _, ev := range pending {
		env := Envelope{
			EventID:    ev.EventID,
			EventType:  ev.Subject,
			TraceID:    ev.TraceID,
			OccurredAt: ev.CreatedAt.UTC().Format(time.RFC3339),
			Payload:    json.RawMessage(ev.Payload),
		}
		raw, marshalErr := json.Marshal(env)
		if marshalErr != nil {
			r.logger.ErrorContext(ctx, "marshal outbox envelope failed", slog.String("event_id", ev.EventID), slog.String("error", marshalErr.Error()))
			continue
		}

		msg := nats.NewMsg(ev.Subject)
		msg.Data = raw
		msg.Header.Set(nats.MsgIdHdr, ev.EventID)
		if ev.Traceparent != "" {
			msg.Header.Set("traceparent", ev.Traceparent)
		}

		if _, pubErr := r.js.PublishMsg(msg); pubErr != nil {
			r.logger.ErrorContext(ctx, "publish outbox event to NATS failed",
				slog.String("subject", ev.Subject),
				slog.String("event_id", ev.EventID),
				slog.String("error", pubErr.Error()),
			)
			continue
		}
		if markErr := r.repo.MarkOutboxPublished(ctx, ev.ID); markErr != nil {
			r.logger.ErrorContext(ctx, "mark outbox event published failed",
				slog.String("outbox_id", ev.ID),
				slog.String("error", markErr.Error()),
			)
		}
	}
}
