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

	"github.com/nexusmart/core/services/product/internal/store"
)

const (
	// DefaultPollInterval is the fallback poll cadence when no trigger arrives.
	DefaultPollInterval = 100 * time.Millisecond
	// DefaultBatchSize is the max outbox rows locked and published per transaction.
	DefaultBatchSize = 100
)

// OutboxStore is the minimal store interface needed by OutboxRelay.
type OutboxStore interface {
	ProcessPendingOutbox(ctx context.Context, limit int32, publishFn func(ctx context.Context, ev store.OutboxEvent) error) (int, error)
}

// Publisher abstracts JetStream PublishMsg for testability.
type Publisher interface {
	PublishMsg(m *nats.Msg, opts ...nats.PubOpt) (*nats.PubAck, error)
}

// EnsureStreams idempotently ensures NEXUSMART (including "product.>") and NEXUSMART_DLQ streams exist.
// If NEXUSMART already exists without "product.>", it appends "product.>" via UpdateStream.
func EnsureStreams(js nats.JetStreamContext, streamName string) error {
	if strings.TrimSpace(streamName) == "" {
		streamName = DefaultStreamName
	}

	primaryCfg := &nats.StreamConfig{
		Name:       streamName,
		Subjects:   []string{"user.>", "order.>", "payment.>", "shipment.>", "product.>"},
		Retention:  nats.LimitsPolicy,
		Storage:    nats.FileStorage,
		Duplicates: 2 * time.Minute,
	}
	if err := ensureStreamWithSubjects(js, primaryCfg, []string{"product.>"}); err != nil {
		return fmt.Errorf("ensure primary stream %s: %w", streamName, err)
	}

	dlqCfg := &nats.StreamConfig{
		Name:      DLQStreamName,
		Subjects:  []string{"dlq.>"},
		Retention: nats.LimitsPolicy,
		Storage:   nats.FileStorage,
	}
	if err := ensureStreamWithSubjects(js, dlqCfg, []string{"dlq.>"}); err != nil {
		return fmt.Errorf("ensure DLQ stream %s: %w", DLQStreamName, err)
	}

	return nil
}

func ensureStreamWithSubjects(js nats.JetStreamContext, cfg *nats.StreamConfig, requiredSubjects []string) error {
	info, err := js.StreamInfo(cfg.Name)
	if err == nil {
		return ensureRequiredSubjectsOnExisting(js, info, requiredSubjects)
	}
	if !errors.Is(err, nats.ErrStreamNotFound) {
		return err
	}

	if _, addErr := js.AddStream(cfg); addErr != nil {
		// Handle concurrent startup race where another service created the stream (possibly without product.>).
		info, infoErr := js.StreamInfo(cfg.Name)
		if infoErr == nil {
			return ensureRequiredSubjectsOnExisting(js, info, requiredSubjects)
		}
		if strings.Contains(strings.ToLower(addErr.Error()), "already in use") {
			return nil
		}
		return addErr
	}
	return nil
}

func ensureRequiredSubjectsOnExisting(js nats.JetStreamContext, info *nats.StreamInfo, requiredSubjects []string) error {
	if info == nil {
		return nil
	}
	existing := make(map[string]struct{}, len(info.Config.Subjects))
	for _, subj := range info.Config.Subjects {
		existing[subj] = struct{}{}
	}

	var missing []string
	for _, req := range requiredSubjects {
		if _, ok := existing[req]; !ok {
			missing = append(missing, req)
		}
	}
	if len(missing) == 0 {
		return nil
	}

	updatedCfg := info.Config
	updatedCfg.Subjects = append([]string(nil), info.Config.Subjects...)
	updatedCfg.Subjects = append(updatedCfg.Subjects, missing...)
	if _, err := js.UpdateStream(&updatedCfg); err != nil {
		return fmt.Errorf("update stream %s subjects: %w", info.Config.Name, err)
	}
	return nil
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
func NewOutboxRelay(st OutboxStore, publisher Publisher, logger *slog.Logger, pollInterval time.Duration, triggerCh <-chan struct{}) *OutboxRelay {
	if logger == nil {
		logger = slog.Default()
	}
	if pollInterval <= 0 {
		pollInterval = DefaultPollInterval
	}
	return &OutboxRelay{
		store:        st,
		publisher:    publisher,
		logger:       logger,
		pollInterval: pollInterval,
		batchSize:    DefaultBatchSize,
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
			r.logger.WarnContext(ctx, "outbox relay poll failed", slog.String("error", err.Error()))
			return totalPublished
		}
		totalPublished += n
		if int32(n) < r.batchSize {
			return totalPublished
		}
	}
}

func (r *OutboxRelay) publishOutboxEvent(_ context.Context, ev store.OutboxEvent) error {
	occurredAt := ev.CreatedAt.UTC()
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}

	env := Envelope{
		EventID:    ev.ID,
		EventType:  ev.EventType,
		TraceID:    ev.TraceID,
		OccurredAt: occurredAt,
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
