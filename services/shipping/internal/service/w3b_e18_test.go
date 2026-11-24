package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nexusmart/core/services/shipping/internal/address"
	"github.com/nexusmart/core/services/shipping/internal/carrier"
	"github.com/nexusmart/core/services/shipping/internal/events"
	"github.com/nexusmart/core/services/shipping/internal/store"
)

type countingOutboxWake struct {
	count atomic.Int32
}

func (w *countingOutboxWake) Wake() {
	w.count.Add(1)
}

func TestE18_TwoWorkersInject503ThreeTimes_CreatesExactlyOneShipment(t *testing.T) {
	repo := newMemRepo()
	mockExp := carrier.NewMockExpressAdapter()
	mockExp.SetTestFaultInjectionEnabled(true)
	if err := mockExp.InjectCreateFault("ord-e18-503", http.StatusServiceUnavailable, 3); err != nil {
		t.Fatalf("InjectCreateFault: %v", err)
	}
	rec := &reviewRecordingCarrier{Carrier: mockExp}
	agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), rec)
	wake := &countingOutboxWake{}

	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	var clockMu sync.Mutex
	nowFn := func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return now
	}
	advance := func(d time.Duration) {
		clockMu.Lock()
		now = now.Add(d)
		clockMu.Unlock()
	}

	svc1 := NewShippingService(repo, agg, nil, wake, slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc2 := NewShippingService(repo, agg, nil, wake, slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc1.nowFn = nowFn
	svc2.nowFn = nowFn

	in := CreateShipmentInput{
		OrderID:     "ord-e18-503",
		ShopID:      "shop-1",
		BuyerID:     "buyer-1",
		CarrierCode: carrier.CodeMockExpress,
		ServiceCode: "STANDARD",
		WeightGrams: 500,
		FromAddress: carrier.Address{ProvinceCode: "79", WardCode: "26740"},
		ToAddress:   carrier.Address{ProvinceCode: "01", WardCode: "00070"},
	}

	// Attempt #1 (sync): hits injected 503 #1 -> queued=true, job persisted.
	out, err := svc1.CreateShipmentWithQueue(context.Background(), in)
	if err != nil {
		t.Fatalf("CreateShipmentWithQueue: %v", err)
	}
	if !out.Queued || out.JobID == "" || out.Shipment != nil {
		t.Fatalf("expected queued=true with job_id, got %+v", out)
	}

	// Before base backoff (2s) elapses, workers claim 0 jobs.
	if n, err := svc1.ProcessDueShipmentCreateJobs(context.Background(), 10); err != nil || n != 0 {
		t.Fatalf("before backoff expected 0 claimed jobs, got n=%d err=%v", n, err)
	}

	// Retry #1 (+2s): hits injected 503 #2 -> rescheduled (+4s).
	advance(2 * time.Second)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = svc1.ProcessDueShipmentCreateJobs(context.Background(), 10) }()
	go func() { defer wg.Done(); _, _ = svc2.ProcessDueShipmentCreateJobs(context.Background(), 10) }()
	wg.Wait()

	jobAfter1, err := repo.GetShipmentCreateJobByID(context.Background(), out.JobID)
	if err != nil || jobAfter1.Status != store.JobStatusPending || jobAfter1.Attempts != 1 {
		t.Fatalf("after 503 #2 expected pending attempts=1, got job=%+v err=%v", jobAfter1, err)
	}
	if diff := jobAfter1.NextRetryAt.Sub(nowFn()); diff != 4*time.Second {
		t.Fatalf("after attempt 1 backoff = %v, want 4s", diff)
	}

	// Retry #2 (+4s): hits injected 503 #3 -> rescheduled (+8s).
	advance(4 * time.Second)
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = svc1.ProcessDueShipmentCreateJobs(context.Background(), 10) }()
	go func() { defer wg.Done(); _, _ = svc2.ProcessDueShipmentCreateJobs(context.Background(), 10) }()
	wg.Wait()

	jobAfter2, err := repo.GetShipmentCreateJobByID(context.Background(), out.JobID)
	if err != nil || jobAfter2.Status != store.JobStatusPending || jobAfter2.Attempts != 2 {
		t.Fatalf("after 503 #3 expected pending attempts=2, got job=%+v err=%v", jobAfter2, err)
	}
	if diff := jobAfter2.NextRetryAt.Sub(nowFn()); diff != 8*time.Second {
		t.Fatalf("after attempt 2 backoff = %v, want 8s", diff)
	}

	// Retry #3 (+8s): 2 concurrent workers race -> carrier returns 200 -> EXACTLY ONE shipment & ONE outbox event!
	advance(8 * time.Second)
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = svc1.ProcessDueShipmentCreateJobs(context.Background(), 10) }()
	go func() { defer wg.Done(); _, _ = svc2.ProcessDueShipmentCreateJobs(context.Background(), 10) }()
	wg.Wait()

	jobFinal, err := repo.GetShipmentCreateJobByID(context.Background(), out.JobID)
	if err != nil || jobFinal.Status != store.JobStatusSucceeded {
		t.Fatalf("expected succeeded job, got %+v err=%v", jobFinal, err)
	}

	repo.mu.Lock()
	shipCount := len(repo.shipments)
	outboxEvents := append([]store.OutboxEvent(nil), repo.outbox...)
	repo.mu.Unlock()
	if shipCount != 1 {
		t.Fatalf("E18: expected EXACTLY 1 shipment after 3x503 + 2 workers, got %d", shipCount)
	}
	if len(outboxEvents) != 1 || outboxEvents[0].Subject != events.SubjectShipmentUpdated {
		t.Fatalf("E18: expected 1 shipment.updated outbox event, got %+v", outboxEvents)
	}
	var payload events.ShipmentUpdatedPayload
	if err := json.Unmarshal(outboxEvents[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal outbox payload: %v", err)
	}
	if payload.Status != carrier.StatusCreated || payload.TrackingCode == "" || payload.LabelURL == "" || payload.ServiceCode != "STANDARD" {
		t.Fatalf("E18: unexpected shipment.updated created payload: %+v", payload)
	}
	if wake.count.Load() < 1 {
		t.Fatalf("E18: expected outboxWake.Wake() to be called on job completion")
	}

	// Every carrier CreateShipment call used the exact same immutable client_order_code ("ord-e18-503").
	rec.mu.Lock()
	codes := append([]string(nil), rec.clientCodes...)
	rec.mu.Unlock()
	if len(codes) != 4 {
		t.Fatalf("expected 4 total carrier create calls (3x503 + 1x200), got %d (%v)", len(codes), codes)
	}
	for i, c := range codes {
		if c != "ord-e18-503" {
			t.Fatalf("call %d client_order_code = %q, want immutable ord-e18-503", i, c)
		}
	}
}

func TestE18_BackoffCapMaxAttemptsExhaustionAndSellerRetry(t *testing.T) {
	repo := newMemRepo()
	var failCarrier atomic.Bool
	failCarrier.Store(true)
	mockExp := carrier.NewMockExpressAdapter()
	rec := &reviewRecordingCarrier{
		Carrier: mockExp,
		createFn: func(ctx context.Context, req carrier.ShipmentRequest) (*carrier.ShipmentResult, error) {
			if failCarrier.Load() {
				return nil, errors.New("carrier 503 persistent outage")
			}
			return mockExp.CreateShipment(ctx, req)
		},
	}
	wake := &countingOutboxWake{}
	svc := NewShippingService(repo, carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), rec), nil, wake, slog.New(slog.NewTextHandler(io.Discard, nil)))
	now := time.Date(2026, 11, 24, 11, 0, 0, 0, time.UTC)
	svc.nowFn = func() time.Time { return now }

	// Verify backoff formula: base=2s, exponential, capped at 5m (300s).
	wantDelays := []time.Duration{
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		16 * time.Second,
		32 * time.Second,
		64 * time.Second,
		128 * time.Second,
		256 * time.Second,
		300 * time.Second,
		300 * time.Second,
	}
	for i, want := range wantDelays {
		if got := svc.createRetryBackoff(int32(i)); got != want {
			t.Fatalf("createRetryBackoff(%d) = %v, want %v", i, got, want)
		}
	}

	in := CreateShipmentInput{
		OrderID: "ord-e18-exhaust", ShopID: "shop-1", BuyerID: "buyer-1",
		CarrierCode: carrier.CodeMockExpress, ServiceCode: "STANDARD", WeightGrams: 500,
	}
	out, err := svc.CreateShipmentWithQueue(context.Background(), in)
	if err != nil || !out.Queued {
		t.Fatalf("CreateShipmentWithQueue: out=%+v err=%v", out, err)
	}

	// Run 8 retries; all fail -> transitions to FAILED and emits shipment.create_failed.
	for attempt := 1; attempt <= 8; attempt++ {
		now = now.Add(6 * time.Minute)
		n, err := svc.ProcessDueShipmentCreateJobs(context.Background(), 10)
		if err != nil || n != 1 {
			t.Fatalf("retry %d: n=%d err=%v", attempt, n, err)
		}
	}

	job, err := repo.GetShipmentCreateJobByID(context.Background(), out.JobID)
	if err != nil || job.Status != store.JobStatusFailed || job.Attempts != 8 {
		t.Fatalf("E18: after 8 failures expected status=failed attempts=8, got job=%+v err=%v", job, err)
	}

	repo.mu.Lock()
	outbox := append([]store.OutboxEvent(nil), repo.outbox...)
	repo.mu.Unlock()
	if len(outbox) != 1 || outbox[0].Subject != events.SubjectShipmentCreateFailed || outbox[0].AggregateID != out.JobID {
		t.Fatalf("E18: expected 1 shipment.create_failed outbox event with aggregate_id=%s, got %+v", out.JobID, outbox)
	}
	var failPayload events.ShipmentCreateFailedPayload
	if err := json.Unmarshal(outbox[0].Payload, &failPayload); err != nil {
		t.Fatalf("unmarshal shipment.create_failed: %v", err)
	}
	if failPayload.JobID != out.JobID || failPayload.OrderID != "ord-e18-exhaust" || failPayload.Attempts != 8 {
		t.Fatalf("unexpected shipment.create_failed payload: %+v", failPayload)
	}

	// E18: After FAILED, seller can click Ship again once carrier recovers and succeed immediately!
	failCarrier.Store(false)
	retryOut, err := svc.CreateShipmentWithQueue(context.Background(), in)
	if err != nil || retryOut.Queued || retryOut.Shipment == nil {
		t.Fatalf("E18: seller retry after FAILED job must create shipment, got out=%+v err=%v", retryOut, err)
	}
}

func TestE18_OrderCancelledMidFlight_CompensatingCancelShipment(t *testing.T) {
	t.Run("CancelWhilePending_PreventsCarrierCall", func(t *testing.T) {
		repo := newMemRepo()
		mockExp := carrier.NewMockExpressAdapter()
		mockExp.SetTestFaultInjectionEnabled(true)
		_ = mockExp.InjectCreateFault("ord-e18-cancel-pend", http.StatusServiceUnavailable, 1)
		rec := &reviewRecordingCarrier{Carrier: mockExp}
		svc := NewShippingService(repo, carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), rec), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
		svc.nowFn = func() time.Time { return now }

		out, err := svc.CreateShipmentWithQueue(context.Background(), CreateShipmentInput{
			OrderID: "ord-e18-cancel-pend", ShopID: "shop-1", CarrierCode: carrier.CodeMockExpress, WeightGrams: 500,
		})
		if err != nil || !out.Queued {
			t.Fatalf("CreateShipmentWithQueue: out=%+v err=%v", out, err)
		}

		// order.cancelled arrives while job is pending.
		if err := svc.HandleOrderCancelled(context.Background(), "evt-cancel-pend", "ord-e18-cancel-pend", "buyer_cancelled"); err != nil {
			t.Fatalf("HandleOrderCancelled: %v", err)
		}

		job, _ := repo.GetShipmentCreateJobByID(context.Background(), out.JobID)
		if job.Status != store.JobStatusCancelled {
			t.Fatalf("expected job status=cancelled, got %q", job.Status)
		}

		// Worker tick must find 0 due jobs and never call carrier again.
		now = now.Add(time.Minute)
		n, err := svc.ProcessDueShipmentCreateJobs(context.Background(), 10)
		if err != nil || n != 0 {
			t.Fatalf("expected 0 due jobs after cancel, got n=%d err=%v", n, err)
		}
		if len(repo.shipments) != 0 {
			t.Fatalf("expected 0 shipments, got %d", len(repo.shipments))
		}
	})

	t.Run("CancelMidFlightDuringCarrierCreate_CompensatesCancel", func(t *testing.T) {
		repo := newMemRepo()
		mockExp := carrier.NewMockExpressAdapter()
		var firstFail atomic.Bool
		firstFail.Store(true)
		enteredCarrier := make(chan struct{})
		releaseCarrier := make(chan struct{})
		var cancelledTracking atomic.Value
		var cancelCount atomic.Int32

		rec := &reviewRecordingCarrier{
			Carrier: mockExp,
			createFn: func(ctx context.Context, req carrier.ShipmentRequest) (*carrier.ShipmentResult, error) {
				if firstFail.Swap(false) {
					return nil, errors.New("503 initial")
				}
				close(enteredCarrier)
				<-releaseCarrier
				return mockExp.CreateShipment(ctx, req)
			},
			cancelFn: func(_ context.Context, trackingCode string) error {
				cancelCount.Add(1)
				cancelledTracking.Store(trackingCode)
				return nil
			},
		}
		svc := NewShippingService(repo, carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), rec), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		now := time.Date(2026, 11, 24, 12, 30, 0, 0, time.UTC)
		svc.nowFn = func() time.Time { return now }

		out, err := svc.CreateShipmentWithQueue(context.Background(), CreateShipmentInput{
			OrderID: "ord-e18-midflight", ShopID: "shop-1", CarrierCode: carrier.CodeMockExpress, WeightGrams: 500,
		})
		if err != nil || !out.Queued {
			t.Fatalf("CreateShipmentWithQueue: out=%+v err=%v", out, err)
		}

		now = now.Add(5 * time.Second)
		workerDone := make(chan error, 1)
		go func() {
			_, wErr := svc.ProcessDueShipmentCreateJobs(context.Background(), 10)
			workerDone <- wErr
		}()

		// Wait until worker is inside carrier.CreateShipment, then cancel order!
		<-enteredCarrier
		if err := svc.HandleOrderCancelled(context.Background(), "evt-cancel-mid", "ord-e18-midflight", "buyer_cancelled"); err != nil {
			t.Fatalf("HandleOrderCancelled mid-flight: %v", err)
		}
		close(releaseCarrier)

		if err := <-workerDone; err != nil {
			t.Fatalf("worker returned error: %v", err)
		}

		if cancelCount.Load() != 1 || cancelledTracking.Load() == "" {
			t.Fatalf("E18: expected 1 compensating CancelShipment call with tracking code, got count=%d tracking=%v",
				cancelCount.Load(), cancelledTracking.Load())
		}
		job, _ := repo.GetShipmentCreateJobByID(context.Background(), out.JobID)
		if job.Status != store.JobStatusCancelled {
			t.Fatalf("expected job to stay cancelled, got %q", job.Status)
		}
		repo.mu.Lock()
		shipLen := len(repo.shipments)
		outboxLen := len(repo.outbox)
		repo.mu.Unlock()
		if shipLen != 0 || outboxLen != 0 {
			t.Fatalf("E18: successfully compensated shipment must leave 0 active shipments and 0 outbox events, got ship=%d outbox=%d", shipLen, outboxLen)
		}
	})

	t.Run("CircuitBreakerOpen_QueuesJobInsteadOfFailing", func(t *testing.T) {
		repo := newMemRepo()
		mockExp := carrier.NewMockExpressAdapter()
		mockExp.SetTestFaultInjectionEnabled(true)
		_ = mockExp.InjectCreateFault("", http.StatusServiceUnavailable, 1)
		resCarrier := carrier.NewResilientCarrier(mockExp, carrier.ResilienceConfig{
			AttemptTimeout: 100 * time.Millisecond,
			MaxAttempts:    1,
			BaseBackoff:    time.Millisecond,
			MaxBackoff:     5 * time.Millisecond,
			CBMaxFailures:  1,
			CBOpenTimeout:  30 * time.Second,
			Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
		// Trip CB open.
		_, _ = resCarrier.CreateShipment(context.Background(), carrier.ShipmentRequest{OrderID: "trip-cb"})

		svc := NewShippingService(repo, carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), resCarrier), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		out, err := svc.CreateShipmentWithQueue(context.Background(), CreateShipmentInput{
			OrderID: "ord-e18-cb-open", ShopID: "shop-1", CarrierCode: carrier.CodeMockExpress, WeightGrams: 500,
		})
		if err != nil || !out.Queued || out.JobID == "" {
			t.Fatalf("E18: open circuit breaker must queue retry job, got out=%+v err=%v", out, err)
		}
	})

	t.Run("CancelMidFlight_DoubleFaultFallbackPersistsShipmentAndOutbox", func(t *testing.T) {
		repo := newMemRepo()
		mockExp := carrier.NewMockExpressAdapter()
		var firstFail atomic.Bool
		firstFail.Store(true)
		enteredCarrier := make(chan struct{})
		releaseCarrier := make(chan struct{})
		wake := &countingOutboxWake{}

		rec := &reviewRecordingCarrier{
			Carrier: mockExp,
			createFn: func(ctx context.Context, req carrier.ShipmentRequest) (*carrier.ShipmentResult, error) {
				if firstFail.Swap(false) {
					return nil, errors.New("503 initial")
				}
				close(enteredCarrier)
				<-releaseCarrier
				return mockExp.CreateShipment(ctx, req)
			},
			cancelFn: func(_ context.Context, _ string) error {
				return errors.New("carrier cancel 503 double-fault")
			},
		}
		svc := NewShippingService(repo, carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), rec), nil, wake, slog.New(slog.NewTextHandler(io.Discard, nil)))
		now := time.Date(2026, 11, 24, 12, 45, 0, 0, time.UTC)
		svc.nowFn = func() time.Time { return now }

		out, err := svc.CreateShipmentWithQueue(context.Background(), CreateShipmentInput{
			OrderID: "ord-e18-double-fault", ShopID: "shop-1", CarrierCode: carrier.CodeMockExpress, WeightGrams: 500,
		})
		if err != nil || !out.Queued {
			t.Fatalf("CreateShipmentWithQueue: out=%+v err=%v", out, err)
		}

		now = now.Add(5 * time.Second)
		workerDone := make(chan error, 1)
		go func() {
			_, wErr := svc.ProcessDueShipmentCreateJobs(context.Background(), 10)
			workerDone <- wErr
		}()

		<-enteredCarrier
		if err := svc.HandleOrderCancelled(context.Background(), "evt-cancel-df", "ord-e18-double-fault", "buyer_cancelled"); err != nil {
			t.Fatalf("HandleOrderCancelled: %v", err)
		}
		close(releaseCarrier)
		<-workerDone

		repo.mu.Lock()
		shipLen := len(repo.shipments)
		outbox := append([]store.OutboxEvent(nil), repo.outbox...)
		repo.mu.Unlock()
		if shipLen != 1 || len(outbox) != 1 || outbox[0].Subject != events.SubjectShipmentUpdated {
			t.Fatalf("double-fault fallback must persist shipment + shipment.updated outbox, got ship=%d outbox=%+v", shipLen, outbox)
		}
		if wake.count.Load() < 1 {
			t.Fatalf("expected outboxWake.Wake() on double-fault fallback")
		}
	})
}

func TestE18_Worker4xxNonRetryableFailsImmediately(t *testing.T) {
	repo := newMemRepo()
	mockExp := carrier.NewMockExpressAdapter()
	var callNum atomic.Int32
	rec := &reviewRecordingCarrier{
		Carrier: mockExp,
		createFn: func(_ context.Context, _ carrier.ShipmentRequest) (*carrier.ShipmentResult, error) {
			if callNum.Add(1) == 1 {
				return nil, errors.New("503 initial transient")
			}
			return nil, &carrier.NonRetryableError{Err: errors.New("400 invalid destination address")}
		},
	}
	wake := &countingOutboxWake{}
	svc := NewShippingService(repo, carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), rec), nil, wake, slog.New(slog.NewTextHandler(io.Discard, nil)))
	now := time.Date(2026, 11, 24, 13, 0, 0, 0, time.UTC)
	svc.nowFn = func() time.Time { return now }

	out, err := svc.CreateShipmentWithQueue(context.Background(), CreateShipmentInput{
		OrderID: "ord-e18-4xx-retry", ShopID: "shop-1", CarrierCode: carrier.CodeMockExpress, WeightGrams: 500,
	})
	if err != nil || !out.Queued {
		t.Fatalf("CreateShipmentWithQueue: out=%+v err=%v", out, err)
	}

	now = now.Add(5 * time.Second)
	if n, err := svc.ProcessDueShipmentCreateJobs(context.Background(), 10); err != nil || n != 1 {
		t.Fatalf("ProcessDueShipmentCreateJobs: n=%d err=%v", n, err)
	}

	job, err := repo.GetShipmentCreateJobByID(context.Background(), out.JobID)
	if err != nil || job.Status != store.JobStatusFailed || job.Attempts != 1 {
		t.Fatalf("E18: 4xx during worker retry must mark job failed immediately on attempt 1, got job=%+v err=%v", job, err)
	}
	repo.mu.Lock()
	outbox := append([]store.OutboxEvent(nil), repo.outbox...)
	repo.mu.Unlock()
	if len(outbox) != 1 || outbox[0].Subject != events.SubjectShipmentCreateFailed {
		t.Fatalf("expected 1 shipment.create_failed outbox event, got %+v", outbox)
	}
	if wake.count.Load() < 1 {
		t.Fatalf("expected outboxWake.Wake() on terminal 4xx failure")
	}
}

func TestE17_CalculateFee_LegacyInactiveWardFallbackWeightAndSanitizedOrigin(t *testing.T) {
	repo := newMemRepo()
	// Simulate an inactive legacy ward (is_active=false) that still exists for saved buyer addresses (E15).
	repo.mu.Lock()
	repo.wards = append(repo.wards, address.Ward{
		Code:          "79001",
		ProvinceCode:  "79",
		Name:          "Phường Sài Gòn (mẫu cũ)",
		GHNDistrictID: 1442,
		GHNWardCode:   "20101",
		CODSupported:  true,
		IsActive:      false,
	})
	repo.mu.Unlock()
	_, _ = repo.UpsertShopShippingSettings(context.Background(), &store.ShopShippingSettings{
		ShopID:              "shop-e17",
		EnabledCarrierCodes: []string{carrier.CodeGHN, carrier.CodeMockExpress},
		DefaultWeightGrams:  400,
	})

	ghn := carrier.NewGHNAdapter(carrier.GHNConfig{Mode: "mock"})
	mockExp := carrier.NewMockExpressAdapter()
	agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), ghn, mockExp)
	qc := newRecordingQuoteCache()
	svc := NewShippingService(repo, agg, qc, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.SetShopPickupResolver(&fakeShopPickup{
		byShop: map[string][2]string{
			"shop-e17":      {"79", "26740"},
			"shop-bad-orig": {"99", "99887"},
		},
	})

	// 1. Saved legacy inactive ward (79001) still resolves in CalculateFee (E15) and applies fallback_weight_item_qty.
	quotes, isEst, err := svc.CalculateFeeWithEstimate(context.Background(), carrier.FeeRequest{
		ShopID:                "shop-e17",
		ToProvinceCode:        "79",
		ToWardCode:            "79001",
		WeightGrams:           100,
		FallbackWeightItemQty: 3, // 100 + 3*400 = 1300g
	})
	if err != nil {
		t.Fatalf("CalculateFeeWithEstimate for legacy inactive ward failed: %v", err)
	}
	if isEst {
		t.Fatalf("explicit legacy ward must NOT set is_estimated=true")
	}
	if len(quotes) == 0 {
		t.Fatalf("expected quotes for legacy ward")
	}
	if len(qc.setReqs) != 1 || qc.setReqs[0].WeightGrams != 1300 {
		t.Fatalf("expected combined weight 1300g cached, got %+v", qc.setReqs)
	}

	// 2. Cache hit still stamps destination ward cod_supported accurately (e.g. Hoang Sa 20333 -> false).
	islandQuotes, isEstIsland, err := svc.CalculateFeeWithEstimate(context.Background(), carrier.FeeRequest{
		ShopID:         "shop-e17",
		ToProvinceCode: "48",
		ToWardCode:     "20333",
		WeightGrams:    500,
	})
	if err != nil || isEstIsland || len(islandQuotes) == 0 {
		t.Fatalf("Hoang Sa quote failed: quotes=%+v isEst=%v err=%v", islandQuotes, isEstIsland, err)
	}
	// Mutate cached entry to CODSupported=true to prove cache-hit re-stamps ward metadata (false).
	for k := range qc.entries {
		for i := range qc.entries[k] {
			qc.entries[k][i].CODSupported = true
		}
	}
	islandCached, _, err := svc.CalculateFeeWithEstimate(context.Background(), carrier.FeeRequest{
		ShopID:         "shop-e17",
		ToProvinceCode: "48",
		ToWardCode:     "20333",
		WeightGrams:    500,
	})
	if err != nil || len(islandCached) == 0 {
		t.Fatalf("Hoang Sa cached quote failed: %v", err)
	}
	for _, q := range islandCached {
		if q.CODSupported {
			t.Fatalf("E16/E17: cached quote for Hoang Sa must stamp cod_supported=false, got %+v", q)
		}
	}

	// 3. E17: Invalid shop pickup error MUST NOT leak shop warehouse province/ward codes.
	_, _, badErr := svc.CalculateFeeWithEstimate(context.Background(), carrier.FeeRequest{
		ShopID:         "shop-bad-orig",
		ToProvinceCode: "79",
		WeightGrams:    500,
	})
	if badErr == nil {
		t.Fatalf("expected error for invalid shop pickup address")
	}
	if stringsContainsAny(badErr.Error(), "99887", `"99"`) {
		t.Fatalf("E17: shop pickup error leaked warehouse address code: %q", badErr.Error())
	}
}

func TestE18_CancelShipmentRPC_CancelsActiveForwardJobAndCompensatesInFlight(t *testing.T) {
	t.Run("PendingJob_ForwardCancelMarksCancelledAndPreservesNotFound", func(t *testing.T) {
		repo := newMemRepo()
		mockExp := carrier.NewMockExpressAdapter()
		mockExp.SetTestFaultInjectionEnabled(true)
		_ = mockExp.InjectCreateFault("ord-rpc-cancel-pend", http.StatusServiceUnavailable, 1)
		var createCalls atomic.Int32
		rec := &reviewRecordingCarrier{
			Carrier: mockExp,
			createFn: func(ctx context.Context, req carrier.ShipmentRequest) (*carrier.ShipmentResult, error) {
				createCalls.Add(1)
				return mockExp.CreateShipment(ctx, req)
			},
		}
		svc := NewShippingService(repo, carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), rec), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		now := time.Date(2026, 11, 24, 17, 0, 0, 0, time.UTC)
		svc.nowFn = func() time.Time { return now }

		out, err := svc.CreateShipmentWithQueue(context.Background(), CreateShipmentInput{
			OrderID: "ord-rpc-cancel-pend", ShopID: "shop-1", CarrierCode: carrier.CodeMockExpress, WeightGrams: 500,
		})
		if err != nil || !out.Queued || out.JobID == "" {
			t.Fatalf("CreateShipmentWithQueue: out=%+v err=%v", out, err)
		}
		if createCalls.Load() != 1 {
			t.Fatalf("expected 1 initial carrier call, got %d", createCalls.Load())
		}

		// RETURN cancel must NOT touch active FORWARD shipment_create_jobs.
		if _, retErr := svc.CancelShipment(context.Background(), "ord-rpc-cancel-pend", store.DirectionReturn, "return cancel"); !errors.Is(retErr, store.ErrNotFound) {
			t.Fatalf("RETURN CancelShipment without shipment must return ErrNotFound, got %v", retErr)
		}
		jobAfterRet, err := repo.GetShipmentCreateJobByID(context.Background(), out.JobID)
		if err != nil || jobAfterRet.Status != store.JobStatusPending {
			t.Fatalf("RETURN CancelShipment must NOT cancel FORWARD job, got job=%+v err=%v", jobAfterRet, err)
		}

		// FORWARD CancelShipment (called by order compensation RPC) cancels pending job and still returns ErrNotFound.
		if _, cancelErr := svc.CancelShipment(context.Background(), "ord-rpc-cancel-pend", store.DirectionForward, "order_cancelled_comp"); !errors.Is(cancelErr, store.ErrNotFound) {
			t.Fatalf("FORWARD CancelShipment without shipment must still return ErrNotFound, got %v", cancelErr)
		}
		jobAfterFwd, err := repo.GetShipmentCreateJobByID(context.Background(), out.JobID)
		if err != nil || jobAfterFwd.Status != store.JobStatusCancelled {
			t.Fatalf("FORWARD CancelShipment must mark active job cancelled, got job=%+v err=%v", jobAfterFwd, err)
		}

		// Worker runs after backoff and MUST NOT call carrier again.
		now = now.Add(time.Minute)
		n, err := svc.ProcessDueShipmentCreateJobs(context.Background(), 10)
		if err != nil || n != 0 {
			t.Fatalf("expected 0 due jobs after CancelShipment, got n=%d err=%v", n, err)
		}
		if createCalls.Load() != 1 {
			t.Fatalf("worker must not call carrier after CancelShipment cancelled job; calls=%d", createCalls.Load())
		}
	})

	t.Run("ProcessingJobMidFlight_ForwardCancelTriggersCarrierCompensation", func(t *testing.T) {
		repo := newMemRepo()
		mockExp := carrier.NewMockExpressAdapter()
		var firstFail atomic.Bool
		firstFail.Store(true)
		enteredCarrier := make(chan struct{})
		releaseCarrier := make(chan struct{})
		var cancelCount atomic.Int32

		rec := &reviewRecordingCarrier{
			Carrier: mockExp,
			createFn: func(ctx context.Context, req carrier.ShipmentRequest) (*carrier.ShipmentResult, error) {
				if firstFail.Swap(false) {
					return nil, errors.New("503 initial")
				}
				close(enteredCarrier)
				<-releaseCarrier
				return mockExp.CreateShipment(ctx, req)
			},
			cancelFn: func(_ context.Context, _ string) error {
				cancelCount.Add(1)
				return nil
			},
		}
		svc := NewShippingService(repo, carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), rec), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		now := time.Date(2026, 11, 24, 17, 15, 0, 0, time.UTC)
		svc.nowFn = func() time.Time { return now }

		out, err := svc.CreateShipmentWithQueue(context.Background(), CreateShipmentInput{
			OrderID: "ord-rpc-cancel-mid", ShopID: "shop-1", CarrierCode: carrier.CodeMockExpress, WeightGrams: 500,
		})
		if err != nil || !out.Queued {
			t.Fatalf("CreateShipmentWithQueue: out=%+v err=%v", out, err)
		}

		now = now.Add(5 * time.Second)
		workerDone := make(chan error, 1)
		go func() {
			_, wErr := svc.ProcessDueShipmentCreateJobs(context.Background(), 10)
			workerDone <- wErr
		}()

		<-enteredCarrier
		// Order compensation RPC arrives while worker job is processing inside carrier.CreateShipment.
		if _, cancelErr := svc.CancelShipment(context.Background(), "ord-rpc-cancel-mid", store.DirectionForward, "order_cancelled_comp"); !errors.Is(cancelErr, store.ErrNotFound) {
			t.Fatalf("CancelShipment mid-flight without persisted shipment must return ErrNotFound, got %v", cancelErr)
		}
		close(releaseCarrier)

		if err := <-workerDone; err != nil {
			t.Fatalf("worker returned error: %v", err)
		}
		if cancelCount.Load() != 1 {
			t.Fatalf("expected 1 compensating carrier CancelShipment call, got %d", cancelCount.Load())
		}
		job, err := repo.GetShipmentCreateJobByID(context.Background(), out.JobID)
		if err != nil || job.Status != store.JobStatusCancelled {
			t.Fatalf("expected job to stay cancelled, got job=%+v err=%v", job, err)
		}
		repo.mu.Lock()
		shipLen := len(repo.shipments)
		repo.mu.Unlock()
		if shipLen != 0 {
			t.Fatalf("expected 0 active shipments after compensating cancel, got %d", shipLen)
		}
	})
}

func TestE18_WorkerTimeoutVsCarrierTimeoutAttemptAccounting(t *testing.T) {
	t.Run("WorkerTickTimeout_DoesNotBurnAttemptAndLeavesOtherJobsPending", func(t *testing.T) {
		repo := newMemRepo()
		mockExp := carrier.NewMockExpressAdapter()
		var slowMode atomic.Bool
		rec := &reviewRecordingCarrier{
			Carrier: mockExp,
			createFn: func(ctx context.Context, req carrier.ShipmentRequest) (*carrier.ShipmentResult, error) {
				if !slowMode.Load() {
					return nil, errors.New("503 initial")
				}
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(300 * time.Millisecond):
					return mockExp.CreateShipment(ctx, req)
				}
			},
		}
		resCarrier := carrier.NewResilientCarrier(rec, carrier.ResilienceConfig{
			AttemptTimeout: 5 * time.Second,
			MaxAttempts:    2,
			BaseBackoff:    5 * time.Millisecond,
			MaxBackoff:     10 * time.Millisecond,
			CBMaxFailures:  10,
			Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
		svc := NewShippingService(repo, carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), resCarrier), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		now := time.Date(2026, 11, 24, 18, 0, 0, 0, time.UTC)
		svc.nowFn = func() time.Time { return now }

		out1, err := svc.CreateShipmentWithQueue(context.Background(), CreateShipmentInput{
			OrderID: "ord-timeout-1", ShopID: "shop-1", CarrierCode: carrier.CodeMockExpress, WeightGrams: 500,
		})
		if err != nil || !out1.Queued {
			t.Fatalf("enqueue job 1: out=%+v err=%v", out1, err)
		}
		out2, err := svc.CreateShipmentWithQueue(context.Background(), CreateShipmentInput{
			OrderID: "ord-timeout-2", ShopID: "shop-1", CarrierCode: carrier.CodeMockExpress, WeightGrams: 500,
		})
		if err != nil || !out2.Queued {
			t.Fatalf("enqueue job 2: out=%+v err=%v", out2, err)
		}
		slowMode.Store(true)

		// Both jobs are due; worker tick ctx expires at 30ms while job 1 is calling carrier.
		now = now.Add(5 * time.Second)
		tickCtx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()
		_, _ = svc.ProcessDueShipmentCreateJobs(tickCtx, 20)

		j1, err := repo.GetShipmentCreateJobByID(context.Background(), out1.JobID)
		if err != nil {
			t.Fatalf("get j1: %v", err)
		}
		j2, err := repo.GetShipmentCreateJobByID(context.Background(), out2.JobID)
		if err != nil {
			t.Fatalf("get j2: %v", err)
		}
		// One job ran and hit worker timeout -> MUST NOT increment attempts.
		// The other job MUST NOT have been claimed up-front (must stay pending!).
		if j1.Attempts != 0 || j2.Attempts != 0 {
			t.Fatalf("worker tick timeout must NOT count as attempt: j1.attempts=%d j2.attempts=%d", j1.Attempts, j2.Attempts)
		}
		pendingCount := 0
		if j1.Status == store.JobStatusPending {
			pendingCount++
		}
		if j2.Status == store.JobStatusPending {
			pendingCount++
		}
		if pendingCount != 1 {
			t.Fatalf("single-job claim loop must leave the unstarted 2nd job pending (not stuck in processing): j1=%s j2=%s", j1.Status, j2.Status)
		}
	})

	t.Run("PerJobTimeoutShorterThanLease_DoesNotBurnAttemptAndNextJobStillRuns", func(t *testing.T) {
		repo := newMemRepo()
		mockExp := carrier.NewMockExpressAdapter()
		var slowMode atomic.Bool
		rec := &reviewRecordingCarrier{
			Carrier: mockExp,
			createFn: func(ctx context.Context, req carrier.ShipmentRequest) (*carrier.ShipmentResult, error) {
				if !slowMode.Load() {
					return nil, errors.New("503 initial")
				}
				if req.OrderID == "ord-perjob-slow" {
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-time.After(300 * time.Millisecond):
						return mockExp.CreateShipment(ctx, req)
					}
				}
				return mockExp.CreateShipment(ctx, req)
			},
		}
		svc := NewShippingService(repo, carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), rec), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		svc.SetJobLease(30 * time.Second)
		svc.SetJobExecTimeout(25 * time.Millisecond)
		now := time.Date(2026, 11, 24, 18, 5, 0, 0, time.UTC)
		svc.nowFn = func() time.Time { return now }

		outSlow, err := svc.CreateShipmentWithQueue(context.Background(), CreateShipmentInput{
			OrderID: "ord-perjob-slow", ShopID: "shop-1", CarrierCode: carrier.CodeMockExpress, WeightGrams: 500,
		})
		if err != nil || !outSlow.Queued {
			t.Fatalf("enqueue slow: out=%+v err=%v", outSlow, err)
		}
		outFast, err := svc.CreateShipmentWithQueue(context.Background(), CreateShipmentInput{
			OrderID: "ord-perjob-fast", ShopID: "shop-1", CarrierCode: carrier.CodeMockExpress, WeightGrams: 500,
		})
		if err != nil || !outFast.Queued {
			t.Fatalf("enqueue fast: out=%+v err=%v", outFast, err)
		}
		slowMode.Store(true)

		// Parent context is alive; slow job hits its own 25ms per-job timeout (0 attempts burned),
		// while fast job gets its OWN fresh 25ms timeout and succeeds in the same tick!
		now = now.Add(5 * time.Second)
		n, err := svc.ProcessDueShipmentCreateJobs(context.Background(), 20)
		if err != nil || n != 2 {
			t.Fatalf("ProcessDueShipmentCreateJobs: n=%d err=%v", n, err)
		}
		jSlow, _ := repo.GetShipmentCreateJobByID(context.Background(), outSlow.JobID)
		if jSlow.Attempts != 0 {
			t.Fatalf("per-job timeout must NOT burn attempt on slow job, got attempts=%d status=%s", jSlow.Attempts, jSlow.Status)
		}
		jFast, _ := repo.GetShipmentCreateJobByID(context.Background(), outFast.JobID)
		if jFast.Status != store.JobStatusSucceeded {
			t.Fatalf("independent 2nd job must succeed in its own per-job timeout, got %+v", jFast)
		}
	})

	t.Run("CarrierTimeoutInsideResilientCarrier_StillCountsAsAttempt", func(t *testing.T) {
		repo := newMemRepo()
		mockExp := carrier.NewMockExpressAdapter()
		var initialFail atomic.Bool
		initialFail.Store(true)
		rec := &reviewRecordingCarrier{
			Carrier: mockExp,
			createFn: func(ctx context.Context, req carrier.ShipmentRequest) (*carrier.ShipmentResult, error) {
				if initialFail.Swap(false) {
					return nil, errors.New("503 initial")
				}
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(300 * time.Millisecond):
					return mockExp.CreateShipment(ctx, req)
				}
			},
		}
		resCarrier := carrier.NewResilientCarrier(rec, carrier.ResilienceConfig{
			AttemptTimeout: 25 * time.Millisecond,
			MaxAttempts:    1,
			BaseBackoff:    5 * time.Millisecond,
			MaxBackoff:     10 * time.Millisecond,
			CBMaxFailures:  5,
			Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
		svc := NewShippingService(repo, carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), resCarrier), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		now := time.Date(2026, 11, 24, 18, 10, 0, 0, time.UTC)
		svc.nowFn = func() time.Time { return now }

		out, err := svc.CreateShipmentWithQueue(context.Background(), CreateShipmentInput{
			OrderID: "ord-carrier-to", ShopID: "shop-1", CarrierCode: carrier.CodeMockExpress, WeightGrams: 500,
		})
		if err != nil || !out.Queued {
			t.Fatalf("enqueue: out=%+v err=%v", out, err)
		}

		// Worker context stays alive (5s), only ResilientCarrier's AttemptTimeout (25ms) fires -> MUST burn 1 attempt!
		now = now.Add(5 * time.Second)
		n, err := svc.ProcessDueShipmentCreateJobs(context.Background(), 10)
		if err != nil || n != 1 {
			t.Fatalf("ProcessDueShipmentCreateJobs: n=%d err=%v", n, err)
		}
		job, err := repo.GetShipmentCreateJobByID(context.Background(), out.JobID)
		if err != nil || job.Status != store.JobStatusPending || job.Attempts != 1 {
			t.Fatalf("carrier timeout via ResilientCarrier must count as attempt=1 pending, got job=%+v err=%v", job, err)
		}
	})
}

func TestE18_StaleClaimToken_DoesNotWriteOrFlipFailedJob(t *testing.T) {
	t.Run("LostLeaseToFailedJob_StaleWorkerDoesNotFlipFailedOrWriteShipment", func(t *testing.T) {
		repo := newMemRepo()
		mockExp := carrier.NewMockExpressAdapter()
		var callNum atomic.Int32
		enteredW1 := make(chan struct{})
		releaseW1 := make(chan struct{})
		wake := &countingOutboxWake{}

		rec := &reviewRecordingCarrier{
			Carrier: mockExp,
			createFn: func(ctx context.Context, req carrier.ShipmentRequest) (*carrier.ShipmentResult, error) {
				n := callNum.Add(1)
				if n == 1 {
					return nil, errors.New("503 initial")
				}
				if n == 2 {
					// Worker 1 stalls in carrier long enough to lose its lease.
					close(enteredW1)
					<-releaseW1
					return mockExp.CreateShipment(ctx, req)
				}
				// Worker 2 gets a 400 non-retryable error and transitions job to FAILED.
				return nil, &carrier.NonRetryableError{Err: errors.New("400 rejected")}
			},
		}

		var clockMu sync.Mutex
		now := time.Date(2026, 11, 24, 18, 20, 0, 0, time.UTC)
		nowFn := func() time.Time {
			clockMu.Lock()
			defer clockMu.Unlock()
			return now
		}
		advance := func(d time.Duration) {
			clockMu.Lock()
			now = now.Add(d)
			clockMu.Unlock()
		}

		svc1 := NewShippingService(repo, carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), rec), nil, wake, slog.New(slog.NewTextHandler(io.Discard, nil)))
		svc1.nowFn = nowFn

		out, err := svc1.CreateShipmentWithQueue(context.Background(), CreateShipmentInput{
			OrderID: "ord-stale-fail", ShopID: "shop-1", CarrierCode: carrier.CodeMockExpress, WeightGrams: 500,
		})
		if err != nil || !out.Queued {
			t.Fatalf("enqueue: out=%+v err=%v", out, err)
		}

		// Start Worker 1 after backoff; it enters carrier.CreateShipment with claim_token_1.
		advance(5 * time.Second)
		w1Done := make(chan error, 1)
		go func() {
			_, wErr := svc1.ProcessDueShipmentCreateJobs(context.Background(), 1)
			w1Done <- wErr
		}()
		<-enteredW1

		// Simulate lease expiry: Worker 2 claims the job (new token) and exhausts/fails it (status=failed + shipment.create_failed).
		advance(35 * time.Second)
		reclaimed, tok2, err := repo.ClaimDueShipmentCreateJobs(context.Background(), 1, 30*time.Second, nowFn())
		if err != nil || len(reclaimed) != 1 || reclaimed[0].ID != out.JobID {
			t.Fatalf("expected Worker 2 to reclaim expired lease, got %+v err=%v", reclaimed, err)
		}
		failOutbox := &store.JobOutboxParams{
			EventID:     "evt-w2-fail",
			Subject:     events.SubjectShipmentCreateFailed,
			AggregateID: out.JobID,
			Payload:     []byte(`{"job_id":"` + out.JobID + `","order_id":"ord-stale-fail"}`),
		}
		if _, ok, err := repo.RecordShipmentCreateJobFailure(context.Background(), out.JobID, tok2, "400 rejected", nil, true, failOutbox, nowFn()); err != nil || !ok {
			t.Fatalf("Worker 2 RecordShipmentCreateJobFailure: ok=%v err=%v", ok, err)
		}

		// Now Worker 1's stalled carrier call returns 200 -> MUST NOT flip FAILED to SUCCEEDED or write shipment/outbox!
		close(releaseW1)
		if err := <-w1Done; err != nil {
			t.Fatalf("Worker 1 err: %v", err)
		}

		job, err := repo.GetShipmentCreateJobByID(context.Background(), out.JobID)
		if err != nil || job.Status != store.JobStatusFailed {
			t.Fatalf("stale worker must NOT flip failed job to %q (want failed)", job.Status)
		}
		repo.mu.Lock()
		shipCount := len(repo.shipments)
		outboxCopy := append([]store.OutboxEvent(nil), repo.outbox...)
		repo.mu.Unlock()
		if shipCount != 0 {
			t.Fatalf("stale worker must NOT insert shipment, got %d", shipCount)
		}
		if len(outboxCopy) != 1 || outboxCopy[0].Subject != events.SubjectShipmentCreateFailed {
			t.Fatalf("stale worker must NOT emit shipment.updated outbox, got %+v", outboxCopy)
		}
	})

	t.Run("LostLeaseToNewWorker_StaleWorkerWritesNothingAndNewWorkerSucceeds", func(t *testing.T) {
		repo := newMemRepo()
		mockExp := carrier.NewMockExpressAdapter()
		var callNum atomic.Int32
		enteredW1 := make(chan struct{})
		releaseW1 := make(chan struct{})
		var cancelCalls atomic.Int32

		rec := &reviewRecordingCarrier{
			Carrier: mockExp,
			createFn: func(ctx context.Context, req carrier.ShipmentRequest) (*carrier.ShipmentResult, error) {
				n := callNum.Add(1)
				if n == 1 {
					return nil, errors.New("503 initial")
				}
				if n == 2 {
					close(enteredW1)
					<-releaseW1
					return mockExp.CreateShipment(ctx, req)
				}
				return mockExp.CreateShipment(ctx, req)
			},
			cancelFn: func(_ context.Context, _ string) error {
				cancelCalls.Add(1)
				return nil
			},
		}

		var clockMu sync.Mutex
		now := time.Date(2026, 11, 24, 18, 30, 0, 0, time.UTC)
		nowFn := func() time.Time {
			clockMu.Lock()
			defer clockMu.Unlock()
			return now
		}
		advance := func(d time.Duration) {
			clockMu.Lock()
			now = now.Add(d)
			clockMu.Unlock()
		}

		svc1 := NewShippingService(repo, carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), rec), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		svc1.nowFn = nowFn

		out, err := svc1.CreateShipmentWithQueue(context.Background(), CreateShipmentInput{
			OrderID: "ord-stale-takeover", ShopID: "shop-1", CarrierCode: carrier.CodeMockExpress, WeightGrams: 500,
		})
		if err != nil || !out.Queued {
			t.Fatalf("enqueue: out=%+v err=%v", out, err)
		}

		advance(5 * time.Second)
		w1Done := make(chan error, 1)
		go func() {
			_, wErr := svc1.ProcessDueShipmentCreateJobs(context.Background(), 1)
			w1Done <- wErr
		}()
		<-enteredW1

		// Lease expires; Worker 2 reclaims job (tok-2) while Worker 1 is still stalled in carrier.
		advance(35 * time.Second)
		reclaimed, tok2, err := repo.ClaimDueShipmentCreateJobs(context.Background(), 1, 30*time.Second, nowFn())
		if err != nil || len(reclaimed) != 1 || reclaimed[0].ID != out.JobID {
			t.Fatalf("Worker 2 reclaim failed: %+v err=%v", reclaimed, err)
		}

		// Worker 1 returns 200 with stale tok-1 -> MUST write nothing, emit nothing, NOT cancel carrier!
		close(releaseW1)
		if err := <-w1Done; err != nil {
			t.Fatalf("Worker 1 err: %v", err)
		}
		if cancelCalls.Load() != 0 {
			t.Fatalf("stale worker must NOT cancel carrier shipment on lease loss (new worker will adopt it), got %d cancels", cancelCalls.Load())
		}
		repo.mu.Lock()
		shipBeforeW2 := len(repo.shipments)
		outboxBeforeW2 := len(repo.outbox)
		repo.mu.Unlock()
		if shipBeforeW2 != 0 || outboxBeforeW2 != 0 {
			t.Fatalf("stale worker must leave 0 shipments and 0 outbox, got ship=%d outbox=%d", shipBeforeW2, outboxBeforeW2)
		}
		jMid, _ := repo.GetShipmentCreateJobByID(context.Background(), out.JobID)
		if jMid.Status != store.JobStatusProcessing || jMid.ClaimToken != tok2 {
			t.Fatalf("stale worker must leave Worker 2's claim intact, got %+v", jMid)
		}

		// Now Worker 2 executes with tok-2 and completes cleanly (1 shipment, 1 outbox).
		if err := svc1.executeClaimedShipmentCreateJob(context.Background(), reclaimed[0], tok2); err != nil {
			t.Fatalf("Worker 2 execute: %v", err)
		}
		repo.mu.Lock()
		shipFinal := len(repo.shipments)
		outboxFinal := len(repo.outbox)
		repo.mu.Unlock()
		if shipFinal != 1 || outboxFinal != 1 {
			t.Fatalf("Worker 2 must persist exactly 1 shipment and 1 outbox, got ship=%d outbox=%d", shipFinal, outboxFinal)
		}
	})

	t.Run("StaleTokenBeforeCarrierCall_SkipsCarrierCompletely", func(t *testing.T) {
		repo := newMemRepo()
		mockExp := carrier.NewMockExpressAdapter()
		var createCalls atomic.Int32
		rec := &reviewRecordingCarrier{
			Carrier: mockExp,
			createFn: func(ctx context.Context, req carrier.ShipmentRequest) (*carrier.ShipmentResult, error) {
				if createCalls.Add(1) == 1 {
					return nil, errors.New("503 initial")
				}
				return mockExp.CreateShipment(ctx, req)
			},
		}
		svc := NewShippingService(repo, carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), rec), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		now := time.Date(2026, 11, 24, 18, 40, 0, 0, time.UTC)
		svc.nowFn = func() time.Time { return now }

		out, err := svc.CreateShipmentWithQueue(context.Background(), CreateShipmentInput{
			OrderID: "ord-stale-precall", ShopID: "shop-1", CarrierCode: carrier.CodeMockExpress, WeightGrams: 500,
		})
		if err != nil || !out.Queued {
			t.Fatalf("enqueue: out=%+v err=%v", out, err)
		}

		// Claim with tok-1, then advance lease and re-claim with tok-2 BEFORE tok-1 executes.
		now = now.Add(5 * time.Second)
		jobs1, tok1, err := repo.ClaimDueShipmentCreateJobs(context.Background(), 1, 30*time.Second, now)
		if err != nil || len(jobs1) != 1 {
			t.Fatalf("claim 1: %+v err=%v", jobs1, err)
		}
		now = now.Add(35 * time.Second)
		jobs2, tok2, err := repo.ClaimDueShipmentCreateJobs(context.Background(), 1, 30*time.Second, now)
		if err != nil || len(jobs2) != 1 || tok2 == tok1 {
			t.Fatalf("claim 2: %+v err=%v", jobs2, err)
		}

		// Executing with stale tok-1 MUST stop immediately without calling carrier.
		if err := svc.executeClaimedShipmentCreateJob(context.Background(), jobs1[0], tok1); err != nil {
			t.Fatalf("stale execute returned error: %v", err)
		}
		if createCalls.Load() != 1 {
			t.Fatalf("stale claim_token must skip carrier call completely, got %d total calls (want 1)", createCalls.Load())
		}
	})
}

func TestGHNSandbox_ServiceCalculateCreateAndReturn_UnmappedWardZeroHTTP(t *testing.T) {
	var httpCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		httpCalls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	repo := newMemRepo()
	// Enable ONLY GHN (sandbox) on the platform so CreateShipment/CreateReturnShipment/CalculateFee target GHN.
	_, _ = repo.UpdateCarrierConfig(context.Background(), &store.CarrierConfig{
		CarrierCode: carrier.CodeGHN, Name: carrier.NameGHN, Enabled: true,
	}, nil)
	_, _ = repo.UpdateCarrierConfig(context.Background(), &store.CarrierConfig{
		CarrierCode: carrier.CodeMockExpress, Name: carrier.NameMockExpress, Enabled: false,
	}, nil)

	ghnSandbox := carrier.NewGHNAdapter(carrier.GHNConfig{
		Mode:       "sandbox",
		BaseURL:    srv.URL,
		Token:      "valid-sandbox-token",
		ShopID:     999,
		HTTPClient: srv.Client(),
	})
	rc := carrier.NewResilientCarrier(ghnSandbox, carrier.ResilienceConfig{
		AttemptTimeout: 500 * time.Millisecond,
		MaxAttempts:    2,
		BaseBackoff:    5 * time.Millisecond,
		MaxBackoff:     10 * time.Millisecond,
		CBMaxFailures:  2,
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	svc := NewShippingService(repo, carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), rc), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// 1. CalculateFee to unmapped ward 31825 (Bac Lieu, province 96) falls back cleanly with 0 GHN HTTP calls.
	quotes, _, err := svc.CalculateFeeWithEstimate(context.Background(), carrier.FeeRequest{
		FromProvinceCode: "79", FromWardCode: "26740",
		ToProvinceCode: "96", ToWardCode: "31825",
		WeightGrams: 500,
	})
	if err != nil {
		t.Fatalf("CalculateFee on unmapped GHN ward must fall back, got err=%v", err)
	}
	if len(quotes) == 0 || !quotes[0].IsFallback {
		t.Fatalf("expected fallback quote for unmapped GHN ward, got %+v", quotes)
	}
	if httpCalls.Load() != 0 {
		t.Fatalf("expected 0 GHN HTTP calls on CalculateFee, got %d", httpCalls.Load())
	}

	// 2. Forward CreateShipmentWithQueue fails immediately (non-retryable, 0 jobs, clear Vietnamese message, 0 HTTP calls).
	_, createErr := svc.CreateShipmentWithQueue(context.Background(), CreateShipmentInput{
		OrderID: "ord-ghn-sb-unmapped", ShopID: "shop-1", BuyerID: "buyer-1",
		CarrierCode: carrier.CodeGHN, WeightGrams: 500,
		FromAddress: carrier.Address{ProvinceCode: "79", WardCode: "26740"},
		ToAddress:   carrier.Address{ProvinceCode: "96", WardCode: "31825"},
	})
	if !errors.Is(createErr, ErrCarrierRejected) || !strings.Contains(createErr.Error(), "phường chưa có mã GHN, chọn hãng khác") {
		t.Fatalf("expected ErrCarrierRejected with missing GHN message, got %v", createErr)
	}
	if len(repo.jobs) != 0 {
		t.Fatalf("missing GHN mapping must enqueue 0 retry jobs, got %d", len(repo.jobs))
	}
	if httpCalls.Load() != 0 {
		t.Fatalf("expected 0 GHN HTTP calls on CreateShipment, got %d", httpCalls.Load())
	}

	// 3. CreateReturnShipment to unmapped shop ward also fails immediately with clear message and 0 HTTP calls.
	_, _, retErr := svc.CreateReturnShipment(context.Background(), CreateReturnShipmentInput{
		OrderID: "ord-ghn-sb-ret", ReturnRequestID: "rr-ghn-1", ShopID: "shop-1", BuyerID: "buyer-1",
		CarrierCode: carrier.CodeGHN, WeightGrams: 500,
		FromAddress: carrier.Address{ProvinceCode: "79", WardCode: "26740"},
		ToAddress:   carrier.Address{ProvinceCode: "96", WardCode: "31825"},
	})
	if !errors.Is(retErr, ErrCarrierRejected) || !strings.Contains(retErr.Error(), "phường chưa có mã GHN, chọn hãng khác") {
		t.Fatalf("expected ErrCarrierRejected on CreateReturnShipment with missing GHN message, got %v", retErr)
	}
	if httpCalls.Load() != 0 {
		t.Fatalf("expected 0 GHN HTTP calls on CreateReturnShipment, got %d", httpCalls.Load())
	}
}

func stringsContainsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
	}
	return false
}
