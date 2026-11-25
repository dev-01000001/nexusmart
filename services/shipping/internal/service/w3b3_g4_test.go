package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nexusmart/core/services/shipping/internal/carrier"
	"github.com/nexusmart/core/services/shipping/internal/events"
	"github.com/nexusmart/core/services/shipping/internal/store"
)

func TestW3B3_G4_EnqueueOnlyWorkerCreatesShipmentWithFullAddressAndEmitsOutbox(t *testing.T) {
	t.Run("WorkerCreatesShipmentAndEmitsCreatedOutbox", func(t *testing.T) {
		repo := newMemRepo()
		mockExp := carrier.NewMockExpressAdapter()
		var createCalls atomic.Int32
		rec := &reviewRecordingCarrier{
			Carrier: mockExp,
			createFn: func(ctx context.Context, req carrier.ShipmentRequest) (*carrier.ShipmentResult, error) {
				createCalls.Add(1)
				return mockExp.CreateShipment(ctx, req)
			},
		}
		wake := &countingOutboxWake{}
		svc := NewShippingService(repo, carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), rec), nil, wake, slog.New(slog.NewTextHandler(io.Discard, nil)))
		now := time.Date(2026, 11, 25, 4, 0, 0, 0, time.UTC)
		svc.nowFn = func() time.Time { return now }

		out, err := svc.CreateShipmentWithQueue(context.Background(), CreateShipmentInput{
			OrderID:      "ord-g4-worker-ok",
			ShopID:       "shop-1",
			BuyerID:      "buyer-1",
			CarrierCode:  carrier.CodeMockExpress,
			ServiceCode:  "FAST",
			CODAmountVND: 275000,
			WeightGrams:  850,
			EnqueueOnly:  true,
			FromAddress: carrier.Address{
				Name:          "Xưởng Thời Trang Phương Nam",
				Phone:         "0901234567",
				ProvinceCode:  "79",
				WardCode:      "26740",
				StreetAddress: "123 Đường Đồng Khởi",
			},
			ToAddress: carrier.Address{
				Name:          "Nguyễn Thị Hồng Nhung",
				Phone:         "0987654321",
				ProvinceCode:  "01",
				WardCode:      "00070",
				StreetAddress: "45 Phố Hàng Trống",
			},
		})
		if err != nil {
			t.Fatalf("CreateShipmentWithQueue(enqueue_only): %v", err)
		}
		if !out.Queued || out.JobID == "" || out.Shipment != nil {
			t.Fatalf("expected queued=true job_id!=empty, got %+v", out)
		}
		if createCalls.Load() != 0 {
			t.Fatalf("enqueue_only must not call carrier synchronously, got %d", createCalls.Load())
		}

		// Job must be immediately due (no initial retry delay) on next worker cycle at same clock.
		n, err := svc.ProcessDueShipmentCreateJobs(context.Background(), 10)
		if err != nil || n != 1 {
			t.Fatalf("ProcessDueShipmentCreateJobs: n=%d err=%v", n, err)
		}
		if createCalls.Load() != 1 {
			t.Fatalf("expected exactly 1 async carrier call, got %d", createCalls.Load())
		}

		sh, err := repo.GetActiveShipmentByOrderID(context.Background(), "ord-g4-worker-ok", store.DirectionForward)
		if err != nil {
			t.Fatalf("GetActiveShipmentByOrderID: %v", err)
		}
		if sh.FromName != "Xưởng Thời Trang Phương Nam" || sh.FromPhone != "0901234567" || sh.FromStreetAddress != "123 Đường Đồng Khởi" ||
			sh.ToName != "Nguyễn Thị Hồng Nhung" || sh.ToPhone != "0987654321" || sh.ToStreetAddress != "45 Phố Hàng Trống" {
			t.Fatalf("worker shipment missing persisted address fields: %+v", sh)
		}
		if sh.CODAmountVND != 275000 || sh.WeightGrams != 850 || sh.ServiceCode != "FAST" {
			t.Fatalf("unexpected shipment parcel fields: %+v", sh)
		}

		repo.mu.Lock()
		outbox := append([]store.OutboxEvent(nil), repo.outbox...)
		repo.mu.Unlock()
		if len(outbox) != 1 || outbox[0].Subject != events.SubjectShipmentUpdated {
			t.Fatalf("expected 1 shipment.updated outbox event, got %+v", outbox)
		}
		var pl events.ShipmentUpdatedPayload
		if err := json.Unmarshal(outbox[0].Payload, &pl); err != nil {
			t.Fatalf("unmarshal shipment.updated: %v", err)
		}
		if pl.Status != carrier.StatusCreated || pl.TrackingCode != sh.TrackingCode || pl.OrderID != "ord-g4-worker-ok" {
			t.Fatalf("unexpected shipment.updated payload: %+v", pl)
		}
		if wake.count.Load() < 1 {
			t.Fatalf("expected outboxWake.Wake() to be called")
		}
	})

	t.Run("Worker4xxRejectionEmitsCreateFailedOutbox", func(t *testing.T) {
		repo := newMemRepo()
		rec := &reviewRecordingCarrier{
			Carrier: carrier.NewMockExpressAdapter(),
			createFn: func(_ context.Context, _ carrier.ShipmentRequest) (*carrier.ShipmentResult, error) {
				return nil, &carrier.NonRetryableError{Err: errors.New("400 invalid phone")}
			},
		}
		wake := &countingOutboxWake{}
		svc := NewShippingService(repo, carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), rec), nil, wake, slog.New(slog.NewTextHandler(io.Discard, nil)))
		now := time.Date(2026, 11, 25, 4, 15, 0, 0, time.UTC)
		svc.nowFn = func() time.Time { return now }

		out, err := svc.CreateShipmentWithQueue(context.Background(), CreateShipmentInput{
			OrderID:     "ord-g4-worker-fail",
			ShopID:      "shop-1",
			CarrierCode: carrier.CodeMockExpress,
			WeightGrams: 500,
			EnqueueOnly: true,
		})
		if err != nil || !out.Queued {
			t.Fatalf("enqueue_only: out=%+v err=%v", out, err)
		}

		if n, err := svc.ProcessDueShipmentCreateJobs(context.Background(), 10); err != nil || n != 1 {
			t.Fatalf("ProcessDueShipmentCreateJobs: n=%d err=%v", n, err)
		}
		job, err := repo.GetShipmentCreateJobByID(context.Background(), out.JobID)
		if err != nil || job.Status != store.JobStatusFailed {
			t.Fatalf("expected job status=failed, got job=%+v err=%v", job, err)
		}
		repo.mu.Lock()
		outbox := append([]store.OutboxEvent(nil), repo.outbox...)
		repo.mu.Unlock()
		if len(outbox) != 1 || outbox[0].Subject != events.SubjectShipmentCreateFailed {
			t.Fatalf("expected 1 shipment.create_failed outbox event, got %+v", outbox)
		}
	})
}

func TestW3B3_G4_Race_EnqueueOnlyConcurrentWithCancelOrderAndCancelShipment(t *testing.T) {
	t.Run("EnqueueOnlyThenMidFlightOrderCancelled_CancelsJobWithNoOrphan", func(t *testing.T) {
		repo := newMemRepo()
		mockExp := carrier.NewMockExpressAdapter()
		enteredCarrier := make(chan struct{})
		releaseCarrier := make(chan struct{})
		var cancelCount atomic.Int32

		rec := &reviewRecordingCarrier{
			Carrier: mockExp,
			createFn: func(ctx context.Context, req carrier.ShipmentRequest) (*carrier.ShipmentResult, error) {
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
		now := time.Date(2026, 11, 25, 4, 30, 0, 0, time.UTC)
		svc.nowFn = func() time.Time { return now }

		out, err := svc.CreateShipmentWithQueue(context.Background(), CreateShipmentInput{
			OrderID:     "ord-g4-race-mid",
			ShopID:      "shop-1",
			CarrierCode: carrier.CodeMockExpress,
			WeightGrams: 500,
			EnqueueOnly: true,
		})
		if err != nil || !out.Queued {
			t.Fatalf("enqueue_only: out=%+v err=%v", out, err)
		}

		workerDone := make(chan error, 1)
		go func() {
			_, wErr := svc.ProcessDueShipmentCreateJobs(context.Background(), 10)
			workerDone <- wErr
		}()

		<-enteredCarrier
		if err := svc.HandleOrderCancelled(context.Background(), "evt-g4-race-mid", "ord-g4-race-mid", "buyer_cancelled"); err != nil {
			t.Fatalf("HandleOrderCancelled: %v", err)
		}
		close(releaseCarrier)

		if err := <-workerDone; err != nil {
			t.Fatalf("worker err: %v", err)
		}
		if cancelCount.Load() != 1 {
			t.Fatalf("expected 1 compensating carrier CancelShipment, got %d", cancelCount.Load())
		}
		job, err := repo.GetShipmentCreateJobByID(context.Background(), out.JobID)
		if err != nil || job.Status != store.JobStatusCancelled {
			t.Fatalf("expected job cancelled, got job=%+v err=%v", job, err)
		}
		if _, err := repo.GetActiveShipmentByOrderID(context.Background(), "ord-g4-race-mid", store.DirectionForward); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("expected 0 active shipments, got err=%v", err)
		}
	})

	t.Run("ConcurrentEnqueueOnly_CancelShipment_AndWorker_NeverLeaksOrphanShipment", func(t *testing.T) {
		for iter := 0; iter < 10; iter++ {
			repo := newMemRepo()
			mockExp := carrier.NewMockExpressAdapter()
			var carrierCreated atomic.Int32
			var carrierCancelled atomic.Int32
			rec := &reviewRecordingCarrier{
				Carrier: mockExp,
				createFn: func(ctx context.Context, req carrier.ShipmentRequest) (*carrier.ShipmentResult, error) {
					carrierCreated.Add(1)
					return mockExp.CreateShipment(ctx, req)
				},
				cancelFn: func(_ context.Context, _ string) error {
					carrierCancelled.Add(1)
					return nil
				},
			}
			svc := NewShippingService(repo, carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), rec), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
			orderID := fmt.Sprintf("ord-g4-race-iter-%d", iter)

			// First enqueue_only, then race worker vs CancelShipment / HandleOrderCancelled.
			out, err := svc.CreateShipmentWithQueue(context.Background(), CreateShipmentInput{
				OrderID:     orderID,
				ShopID:      "shop-1",
				CarrierCode: carrier.CodeMockExpress,
				WeightGrams: 500,
				EnqueueOnly: true,
			})
			if err != nil || !out.Queued {
				t.Fatalf("iter %d enqueue_only: out=%+v err=%v", iter, out, err)
			}

			start := make(chan struct{})
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				<-start
				_, _ = svc.ProcessDueShipmentCreateJobs(context.Background(), 10)
			}()
			go func() {
				defer wg.Done()
				<-start
				if iter%2 == 0 {
					_, _ = svc.CancelShipment(context.Background(), orderID, store.DirectionForward, "race_cancel_rpc")
				} else {
					_ = svc.HandleOrderCancelled(context.Background(), fmt.Sprintf("evt-race-%d", iter), orderID, "race_order_cancelled")
				}
			}()
			close(start)
			wg.Wait()

			// Run one more worker tick after cancel completed: must NEVER create a new shipment.
			_, _ = svc.ProcessDueShipmentCreateJobs(context.Background(), 10)

			// Invariant: NO active (non-cancelled) shipment exists, AND every created carrier shipment was cancelled!
			if sh, err := repo.GetActiveShipmentByOrderID(context.Background(), orderID, store.DirectionForward); err == nil && sh != nil {
				t.Fatalf("iter %d: orphan active shipment remained after cancel race: %+v", iter, sh)
			}
			if carrierCreated.Load() != carrierCancelled.Load() {
				t.Fatalf("iter %d: carrier orphan mismatch created=%d cancelled=%d", iter, carrierCreated.Load(), carrierCancelled.Load())
			}
			job, err := repo.GetShipmentCreateJobByID(context.Background(), out.JobID)
			if err != nil {
				t.Fatalf("iter %d: get job: %v", iter, err)
			}
			if job.Status != store.JobStatusCancelled && job.Status != store.JobStatusSucceeded {
				t.Fatalf("iter %d: unexpected job status %q", iter, job.Status)
			}
			if job.Status == store.JobStatusPending || job.Status == store.JobStatusProcessing {
				t.Fatalf("iter %d: active job survived cancel: %+v", iter, job)
			}
		}
	})

	t.Run("InFlightEnqueueOnlyInsert_SerializedWithCancel_MarksNewJobCancelled", func(t *testing.T) {
		baseRepo := newMemRepo()
		enteredEnqueue := make(chan struct{})
		releaseEnqueue := make(chan struct{})
		hookRepo := &enqueueHookRepo{
			Repository: baseRepo,
			onEnqueue: func() {
				close(enteredEnqueue)
				<-releaseEnqueue
			},
		}
		mockExp := carrier.NewMockExpressAdapter()
		svc := NewShippingService(hookRepo, carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), mockExp), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

		type enqRes struct {
			out *CreateShipmentOutcome
			err error
		}
		enqCh := make(chan enqRes, 1)
		go func() {
			out, err := svc.CreateShipmentWithQueue(context.Background(), CreateShipmentInput{
				OrderID:     "ord-g4-lock-overlap",
				ShopID:      "shop-1",
				CarrierCode: carrier.CodeMockExpress,
				WeightGrams: 500,
				EnqueueOnly: true,
			})
			enqCh <- enqRes{out: out, err: err}
		}()

		<-enteredEnqueue
		cancelDone := make(chan error, 1)
		go func() {
			cancelDone <- svc.HandleOrderCancelled(context.Background(), "evt-g4-lock-overlap", "ord-g4-lock-overlap", "buyer_cancelled")
		}()

		// Give HandleOrderCancelled a moment to run its first CancelActiveShipmentCreateJobs (0 rows)
		// and block on AcquireOrderDirectionLock until CreateShipment(enqueue_only=true) finishes inserting.
		time.Sleep(15 * time.Millisecond)
		close(releaseEnqueue)

		res := <-enqCh
		if res.err != nil || !res.out.Queued || res.out.JobID == "" {
			t.Fatalf("enqueue_only failed: out=%+v err=%v", res.out, res.err)
		}
		if err := <-cancelDone; err != nil {
			t.Fatalf("HandleOrderCancelled failed: %v", err)
		}

		// Re-check under lock must have immediately cancelled the newly inserted job!
		job, err := baseRepo.GetShipmentCreateJobByID(context.Background(), res.out.JobID)
		if err != nil || job.Status != store.JobStatusCancelled {
			t.Fatalf("expected newly inserted job to be cancelled by concurrent HandleOrderCancelled, got job=%+v err=%v", job, err)
		}
		if n, err := svc.ProcessDueShipmentCreateJobs(context.Background(), 10); err != nil || n != 0 {
			t.Fatalf("expected 0 due jobs after concurrent cancel, got n=%d err=%v", n, err)
		}
	})
}

type enqueueHookRepo struct {
	store.Repository
	onEnqueue func()
}

func (r *enqueueHookRepo) EnqueueShipmentCreateJob(ctx context.Context, job *store.ShipmentCreateJob) (*store.ShipmentCreateJob, bool, error) {
	if r.onEnqueue != nil {
		r.onEnqueue()
	}
	return r.Repository.EnqueueShipmentCreateJob(ctx, job)
}
