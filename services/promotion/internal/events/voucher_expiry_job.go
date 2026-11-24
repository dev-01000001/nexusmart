package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/promotion/internal/domain"
	"github.com/nexusmart/core/services/promotion/internal/store"
)

const (
	// SubjectVoucherExpiring is the NATS subject for saved voucher expiry reminders (docs/contracts.md §5).
	SubjectVoucherExpiring = "promotion.voucher_expiring"

	defaultExpiryNoticeWindow = 24 * time.Hour
	defaultExpiryBatchSize    = 100
)

// VoucherExpiringPayload matches the canonical payload schema for `promotion.voucher_expiring`.
type VoucherExpiringPayload struct {
	UserID    string `json:"user_id"`
	VoucherID string `json:"voucher_id"`
	Code      string `json:"code"`
	EndsAt    string `json:"ends_at"`
}

// EventPublisher abstracts JetStream publishing for testability.
type EventPublisher interface {
	Publish(ctx context.Context, subject, msgID string, data []byte, headers map[string]string) error
}

type jetStreamEventPublisher struct {
	js nats.JetStreamContext
}

// NewJetStreamPublisher wraps nats.JetStreamContext as an EventPublisher with Nats-Msg-Id dedup.
func NewJetStreamPublisher(js nats.JetStreamContext) EventPublisher {
	if js == nil {
		return nil
	}
	return &jetStreamEventPublisher{js: js}
}

func (p *jetStreamEventPublisher) Publish(ctx context.Context, subject, msgID string, data []byte, headers map[string]string) error {
	msg := nats.NewMsg(subject)
	msg.Data = data
	for k, v := range headers {
		msg.Header.Set(k, v)
	}
	if msgID != "" {
		msg.Header.Set(nats.MsgIdHdr, msgID)
	}
	_, err := p.js.PublishMsg(msg, nats.Context(ctx))
	return err
}

// VoucherExpiryJob periodically scans saved active vouchers expiring within NoticeWindow
// and publishes `promotion.voucher_expiring` at most once per (user_id, voucher_id).
type VoucherExpiryJob struct {
	store        store.Store
	publisher    EventPublisher
	noticeWindow time.Duration
	batchSize    int
	logger       *slog.Logger
	nowFn        func() time.Time
}

// NewVoucherExpiryJob builds a VoucherExpiryJob.
func NewVoucherExpiryJob(st store.Store, pub EventPublisher, noticeWindow time.Duration, log *slog.Logger) *VoucherExpiryJob {
	if noticeWindow <= 0 {
		noticeWindow = defaultExpiryNoticeWindow
	}
	if log == nil {
		log = slog.Default()
	}
	return &VoucherExpiryJob{
		store:        st,
		publisher:    pub,
		noticeWindow: noticeWindow,
		batchSize:    defaultExpiryBatchSize,
		logger:       log,
		nowFn:        func() time.Time { return time.Now().UTC() },
	}
}

// SetClock overrides the clock used by RunOnce (intended for deterministic unit tests).
func (j *VoucherExpiryJob) SetClock(fn func() time.Time) {
	if fn != nil {
		j.nowFn = fn
	}
}

// RunOnce processes all currently eligible expiring saved vouchers in bounded batches.
func (j *VoucherExpiryJob) RunOnce(ctx context.Context) (int, error) {
	if j == nil || j.store == nil || j.publisher == nil {
		return 0, nil
	}

	now := j.nowFn().UTC()
	totalSent := 0

	for {
		sent, scanned, err := j.store.ProcessExpiringSavedVouchers(
			ctx,
			now,
			j.noticeWindow,
			j.batchSize,
			func(notifyCtx context.Context, item domain.ExpiringSavedVoucher) error {
				return j.publishOne(notifyCtx, now, item)
			},
		)
		totalSent += sent
		if err != nil {
			return totalSent, err
		}
		if scanned < j.batchSize || sent == 0 {
			break
		}
		if ctx.Err() != nil {
			return totalSent, ctx.Err()
		}
	}
	return totalSent, nil
}

func (j *VoucherExpiryJob) publishOne(ctx context.Context, now time.Time, item domain.ExpiringSavedVoucher) error {
	tracer := otel.Tracer("promotion")
	ctx, span := tracer.Start(ctx, "nats.publish "+SubjectVoucherExpiring,
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			attribute.String("messaging.destination", SubjectVoucherExpiring),
			attribute.String("promotion.voucher_id", item.VoucherID),
		),
	)
	defer span.End()

	// Deterministic event_id guarantees JetStream dedup + notification processed_events idempotency
	// even if the worker crashes between publish and DB commit.
	eventID := fmt.Sprintf("voucher-expiring:%s:%s", item.UserID, item.VoucherID)

	payloadBytes, err := json.Marshal(VoucherExpiringPayload{
		UserID:    item.UserID,
		VoucherID: item.VoucherID,
		Code:      item.Code,
		EndsAt:    item.EndsAt.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return fmt.Errorf("marshal voucher expiring payload: %w", err)
	}

	traceID := ""
	if sc := span.SpanContext(); sc.IsValid() {
		traceID = sc.TraceID().String()
	}

	envBytes, err := json.Marshal(Envelope{
		EventID:    eventID,
		EventType:  SubjectVoucherExpiring,
		TraceID:    traceID,
		OccurredAt: now.UTC().Format(time.RFC3339),
		Payload:    payloadBytes,
	})
	if err != nil {
		return fmt.Errorf("marshal voucher expiring envelope: %w", err)
	}

	headers := make(map[string]string, 2)
	telemetry.InjectTraceHeaders(ctx, headers)

	if err := j.publisher.Publish(ctx, SubjectVoucherExpiring, eventID, envBytes, headers); err != nil {
		return fmt.Errorf("publish %s for user=%s voucher=%s: %w", SubjectVoucherExpiring, item.UserID, item.VoucherID, err)
	}
	return nil
}

// Start runs the voucher expiry scanner periodically until ctx is cancelled.
func (j *VoucherExpiryJob) Start(ctx context.Context, interval time.Duration) {
	if j == nil || j.store == nil || j.publisher == nil {
		return
	}
	if interval <= 0 {
		interval = time.Minute
	}

	run := func() {
		runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		sent, err := j.RunOnce(runCtx)
		if err != nil {
			j.logger.Warn("voucher expiry scan finished with error",
				slog.Int("sent", sent),
				slog.String("error", err.Error()),
			)
			return
		}
		if sent > 0 {
			j.logger.Info("published voucher expiring notifications", slog.Int("sent", sent))
		}
	}

	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
