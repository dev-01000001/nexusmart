package events_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/promotion/internal/domain"
	"github.com/nexusmart/core/services/promotion/internal/events"
	"github.com/nexusmart/core/services/promotion/internal/store"
)

type recordedPubMsg struct {
	Subject string
	MsgID   string
	Data    []byte
	Headers map[string]string
}

type recordingEventPublisher struct {
	mu      sync.Mutex
	msgs    []recordedPubMsg
	failN   atomic.Int32
	failErr error
}

func (p *recordingEventPublisher) Publish(_ context.Context, subject, msgID string, data []byte, headers map[string]string) error {
	if p.failN.Add(-1) >= 0 {
		if p.failErr != nil {
			return p.failErr
		}
		return errors.New("simulated nats publish failure")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	hdrCopy := make(map[string]string, len(headers))
	for k, v := range headers {
		hdrCopy[k] = v
	}
	p.msgs = append(p.msgs, recordedPubMsg{
		Subject: subject,
		MsgID:   msgID,
		Data:    append([]byte(nil), data...),
		Headers: hdrCopy,
	})
	return nil
}

func (p *recordingEventPublisher) Messages() []recordedPubMsg {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]recordedPubMsg, len(p.msgs))
	copy(out, p.msgs)
	return out
}

func TestVoucherExpiryJob_EmitsOncePerUserVoucher_RespectsWindowAndStatus(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemoryStore()
	pub := &recordingEventPublisher{}
	log := logger.New("promo-job-test", logger.WithWriter(io.Discard))

	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

	// 1. Active voucher expiring in 12h (within 24h window) -> SHOULD emit for both buyer-1 and buyer-2
	vSoon, err := mem.CreateVoucher(ctx, domain.Voucher{
		Code: "SOON12H", Name: "Soon", Scope: domain.VoucherScopePlatform,
		DiscountType: domain.DiscountTypeFixed, ValueVND: 15000, Quota: 10, PerUserLimit: 1,
		StartsAt: now.Add(-24 * time.Hour), EndsAt: now.Add(12 * time.Hour),
		IsActive: true, Status: domain.VoucherStatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	// 2. Active voucher expiring in 48h (outside 24h window) -> MUST NOT emit yet
	vFar, err := mem.CreateVoucher(ctx, domain.Voucher{
		Code: "FAR48H", Name: "Far", Scope: domain.VoucherScopePlatform,
		DiscountType: domain.DiscountTypeFixed, ValueVND: 15000, Quota: 10, PerUserLimit: 1,
		StartsAt: now.Add(-24 * time.Hour), EndsAt: now.Add(48 * time.Hour),
		IsActive: true, Status: domain.VoucherStatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	// 3. Already expired voucher -> MUST NOT emit
	vExpired, err := mem.CreateVoucher(ctx, domain.Voucher{
		Code: "PAST1H", Name: "Past", Scope: domain.VoucherScopePlatform,
		DiscountType: domain.DiscountTypeFixed, ValueVND: 15000, Quota: 10, PerUserLimit: 1,
		StartsAt: now.Add(-24 * time.Hour), EndsAt: now.Add(-time.Hour),
		IsActive: true, Status: domain.VoucherStatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	// 4. Paused voucher expiring in 6h -> MUST NOT emit
	vPaused, err := mem.CreateVoucher(ctx, domain.Voucher{
		Code: "PAUSED6H", Name: "Paused", Scope: domain.VoucherScopePlatform,
		DiscountType: domain.DiscountTypeFixed, ValueVND: 15000, Quota: 10, PerUserLimit: 1,
		StartsAt: now.Add(-24 * time.Hour), EndsAt: now.Add(6 * time.Hour),
		IsActive: false, Status: domain.VoucherStatusPaused,
	})
	if err != nil {
		t.Fatal(err)
	}
	// 5. Deleted voucher expiring in 6h -> MUST NOT emit
	vDeleted, err := mem.CreateVoucher(ctx, domain.Voucher{
		Code: "DEL6H", Name: "Deleted", Scope: domain.VoucherScopePlatform,
		DiscountType: domain.DiscountTypeFixed, ValueVND: 15000, Quota: 10, PerUserLimit: 1,
		StartsAt: now.Add(-24 * time.Hour), EndsAt: now.Add(6 * time.Hour),
		IsActive: false, Status: domain.VoucherStatusDeleted,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, _ = mem.SaveUserVoucher(ctx, "buyer-1", vSoon.ID)
	_, _ = mem.SaveUserVoucher(ctx, "buyer-2", vSoon.ID)
	_, _ = mem.SaveUserVoucher(ctx, "buyer-1", vFar.ID)
	_, _ = mem.SaveUserVoucher(ctx, "buyer-1", vExpired.ID)
	_, _ = mem.SaveUserVoucher(ctx, "buyer-1", vPaused.ID)
	_, _ = mem.SaveUserVoucher(ctx, "buyer-1", vDeleted.ID)

	job := events.NewVoucherExpiryJob(mem, pub, 24*time.Hour, log)
	job.SetClock(func() time.Time { return now })

	// Run 1: emits exactly 2 events (buyer-1 & buyer-2 for vSoon)
	sent1, err := job.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce 1: %v", err)
	}
	if sent1 != 2 {
		t.Fatalf("expected 2 expiring notifications on run 1, got %d", sent1)
	}

	msgs := pub.Messages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 published messages, got %d", len(msgs))
	}
	seenUsers := make(map[string]bool)
	for _, m := range msgs {
		if m.Subject != events.SubjectVoucherExpiring {
			t.Fatalf("unexpected subject %q", m.Subject)
		}
		var env events.Envelope
		if err := json.Unmarshal(m.Data, &env); err != nil {
			t.Fatalf("unmarshal envelope: %v", err)
		}
		if env.EventType != events.SubjectVoucherExpiring || env.EventID == "" || env.EventID != m.MsgID {
			t.Fatalf("invalid envelope metadata: env=%+v msgID=%s", env, m.MsgID)
		}
		var payload events.VoucherExpiringPayload
		if err := json.Unmarshal(env.Payload, &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		if payload.VoucherID != vSoon.ID || payload.Code != "SOON12H" || payload.EndsAt != vSoon.EndsAt.UTC().Format(time.RFC3339) {
			t.Fatalf("unexpected payload: %+v", payload)
		}
		seenUsers[payload.UserID] = true
	}
	if !seenUsers["buyer-1"] || !seenUsers["buyer-2"] {
		t.Fatalf("expected both buyer-1 and buyer-2 notified, got %v", seenUsers)
	}

	// Run 2 & 3 at same clock and 1h later: MUST NOT emit duplicates!
	sent2, err := job.RunOnce(ctx)
	if err != nil || sent2 != 0 {
		t.Fatalf("expected 0 duplicates on run 2, got sent=%d err=%v", sent2, err)
	}
	now = now.Add(time.Hour)
	sent3, err := job.RunOnce(ctx)
	if err != nil || sent3 != 0 {
		t.Fatalf("expected 0 duplicates on run 3, got sent=%d err=%v", sent3, err)
	}

	// Advance clock by 25h: vFar is now 22h from expiry (within 24h) -> emits ONCE for buyer-1!
	now = now.Add(25 * time.Hour)
	sent4, err := job.RunOnce(ctx)
	if err != nil || sent4 != 1 {
		t.Fatalf("expected 1 notification for vFar after clock advance, got sent=%d err=%v", sent4, err)
	}
}

func TestVoucherExpiryJob_RetriesOnTransientPublishFailure_AndConcurrentWorkersNoDup(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemoryStore()
	pub := &recordingEventPublisher{}
	pub.failN.Store(1) // fail first publish attempt
	log := logger.New("promo-job-retry-test", logger.WithWriter(io.Discard))

	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	v, err := mem.CreateVoucher(ctx, domain.Voucher{
		Code: "RETRY1", Name: "Retry", Scope: domain.VoucherScopePlatform,
		DiscountType: domain.DiscountTypeFixed, ValueVND: 10000, Quota: 10, PerUserLimit: 1,
		StartsAt: now.Add(-time.Hour), EndsAt: now.Add(5 * time.Hour),
		IsActive: true, Status: domain.VoucherStatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = mem.SaveUserVoucher(ctx, "buyer-retry", v.ID)

	job := events.NewVoucherExpiryJob(mem, pub, 24*time.Hour, log)
	job.SetClock(func() time.Time { return now })

	// Attempt 1 fails publish -> must NOT mark notified
	sent1, err := job.RunOnce(ctx)
	if err == nil || sent1 != 0 {
		t.Fatalf("expected transient error and 0 sent on attempt 1, got sent=%d err=%v", sent1, err)
	}

	// Now launch 10 concurrent workers -> exactly 1 succeeds and publishes ONCE
	var wg sync.WaitGroup
	var totalSent atomic.Int64
	start := make(chan struct{})
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			n, rErr := job.RunOnce(ctx)
			if rErr == nil {
				totalSent.Add(int64(n))
			}
		}()
	}
	close(start)
	wg.Wait()

	if totalSent.Load() != 1 || len(pub.Messages()) != 1 {
		t.Fatalf("expected exactly 1 notification across 10 concurrent workers, got totalSent=%d msgs=%d",
			totalSent.Load(), len(pub.Messages()))
	}
}

type failingOrderEventStore struct {
	store.Store
	err error
}

func (f *failingOrderEventStore) ProcessOrderCompletedEvent(_ context.Context, _, _, _, _ string, _ int64) (bool, error) {
	return false, f.err
}

func TestVoucherExpiryJob_StartAndConsumerRetryLifecycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	mem := store.NewMemoryStore()
	pub := &recordingEventPublisher{}
	log := logger.New("promo-job-start-test", logger.WithWriter(io.Discard))

	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	v, err := mem.CreateVoucher(ctx, domain.Voucher{
		Code: "START1", Name: "StartSweep", Scope: domain.VoucherScopePlatform,
		DiscountType: domain.DiscountTypeFixed, ValueVND: 5000, Quota: 10, PerUserLimit: 1,
		StartsAt: now.Add(-time.Hour), EndsAt: now.Add(6 * time.Hour),
		IsActive: true, Status: domain.VoucherStatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = mem.SaveUserVoucher(ctx, "buyer-start", v.ID)

	job := events.NewVoucherExpiryJob(mem, pub, 0, log)
	job.SetClock(func() time.Time { return now })

	done := make(chan struct{})
	go func() {
		job.Start(ctx, 10*time.Millisecond)
		close(done)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for len(pub.Messages()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	if len(pub.Messages()) != 1 {
		t.Fatalf("expected 1 published event from Start initial sweep, got %d", len(pub.Messages()))
	}

	// Consumer nil-JS lifecycle + retry -> DLQ on MaxDeliveryAttempts
	dlq := &recordingDLQ{}
	failC := events.NewOrderCompletedConsumer(&failingOrderEventStore{Store: mem, err: errors.New("db down")}, nil, "", log)
	failC.SetDLQPublisher(dlq)
	if err := failC.EnsureStreams(); err != nil {
		t.Fatalf("EnsureStreams: %v", err)
	}
	if err := failC.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := failC.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	badPayloadAck, badPayloadErr := failC.ProcessRawMessage(context.Background(), []byte(`{"event_id":"e1","payload":"bad"}`), map[string]string{"k": "v"}, 1)
	if !badPayloadAck || badPayloadErr != nil {
		t.Fatalf("bad payload: ack=%v err=%v", badPayloadAck, badPayloadErr)
	}
	validRaw := []byte(`{"event_id":"e2","payload":{"order_id":"o1","buyer_id":"b1","coins_reward":100}}`)
	retryAck, retryErr := failC.ProcessRawMessage(context.Background(), validRaw, nil, 1)
	if retryAck || retryErr == nil {
		t.Fatalf("attempt 1 should nack: ack=%v err=%v", retryAck, retryErr)
	}
	maxAck, maxErr := failC.ProcessRawMessage(context.Background(), validRaw, nil, events.MaxDeliveryAttempts)
	if !maxAck || maxErr != nil || len(dlq.subjects) != 2 {
		t.Fatalf("max attempts should DLQ: ack=%v err=%v dlq=%v", maxAck, maxErr, dlq.subjects)
	}
}

func TestVoucherExpiryJob_R3_SkipsBudgetExhaustedVoucher(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemoryStore()
	pub := &recordingEventPublisher{}
	log := logger.New("promo-job-bud-test", logger.WithWriter(io.Discard))
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

	vBud, err := mem.CreateVoucher(ctx, domain.Voucher{
		Code: "BUDEXH", Name: "Budget Exhausted", Scope: domain.VoucherScopePlatform,
		DiscountType: domain.DiscountTypeFixed, ValueVND: 20000,
		Quota: 10, PerUserLimit: 1, BudgetVND: 20000,
		StartsAt: now.Add(-time.Hour), EndsAt: now.Add(6 * time.Hour),
		IsActive: true, Status: domain.VoucherStatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = mem.SaveUserVoucher(ctx, "buyer-bud", vBud.ID)
	// Consume full budget via 1 reservation (used_count=1 < quota=10, used_budget_vnd=20000 == budget_vnd)
	if _, _, err := mem.CommitReservation(ctx, domain.PromotionReservation{
		OrderID:  "ord-bud-exh",
		UserID:   "buyer-other",
		Vouchers: []domain.ReservedVoucher{{VoucherID: vBud.ID, Scope: vBud.Scope, DiscountVND: 20000, HasBudget: true}},
	}, now); err != nil {
		t.Fatal(err)
	}

	job := events.NewVoucherExpiryJob(mem, pub, 24*time.Hour, log)
	job.SetClock(func() time.Time { return now })
	sent, err := job.RunOnce(ctx)
	if err != nil || sent != 0 || len(pub.Messages()) != 0 {
		t.Fatalf("budget-exhausted voucher must NOT trigger expiry notice, got sent=%d msgs=%d err=%v", sent, len(pub.Messages()), err)
	}
}
