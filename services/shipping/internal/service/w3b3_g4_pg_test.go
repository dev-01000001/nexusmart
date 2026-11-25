package service

import (
	"context"
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

// TestService_W3B3_G3_G4_RealPG_AddressFieldsEnqueueOnlyAndCancelRace verifies G3/G4 on real Postgres:
//   - Migration 000008 is idempotent across repeated MigrateAndSeed runs.
//   - Sync CreateShipment and async enqueue_only worker both persist full from/to address fields on shipments.
//   - GetShipmentsByTrackingCodes fetches multiple shipments in one query with all address fields intact.
//   - Racing CreateShipment(enqueue_only=true) + worker vs CancelShipment/HandleOrderCancelled leaves 0 orphan shipments.
func TestService_W3B3_G3_G4_RealPG_AddressFieldsEnqueueOnlyAndCancelRace(t *testing.T) {
	pool, pgStore := openIsolatedServiceTestPG(t)
	ctx := context.Background()

	// Re-run MigrateAndSeed to verify 000008_w3b3_shipment_label_fields.sql idempotency on an already-migrated DB.
	if err := pgStore.MigrateAndSeed(ctx); err != nil {
		t.Fatalf("second MigrateAndSeed (000008 idempotency): %v", err)
	}

	t.Run("SyncAndEnqueueOnlyPersistFullAddressesAndBatchLookup", func(t *testing.T) {
		mockExp := carrier.NewMockExpressAdapter()
		svc := NewShippingService(pgStore, carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), mockExp), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		now := time.Date(2026, 11, 25, 5, 0, 0, 0, time.UTC)
		svc.nowFn = func() time.Time { return now }

		// 1. Sync CreateShipment persists full address fields.
		syncOut, err := svc.CreateShipmentWithQueue(ctx, CreateShipmentInput{
			OrderID:      "ord-pg-w3b3-sync",
			ShopID:       "shop-a",
			BuyerID:      "buyer-1",
			CarrierCode:  carrier.CodeMockExpress,
			ServiceCode:  "STANDARD",
			CODAmountVND: 320000,
			WeightGrams:  700,
			FromAddress: carrier.Address{
				Name: "Kho Gửi Miền Nam", Phone: "0901112222", ProvinceCode: "79", WardCode: "26740", StreetAddress: "100 Nguyễn Thị Minh Khai",
			},
			ToAddress: carrier.Address{
				Name: "Lê Hoàng Anh", Phone: "0913334444", ProvinceCode: "01", WardCode: "00070", StreetAddress: "20 Tràng Tiền",
			},
		})
		if err != nil || syncOut.Shipment == nil {
			t.Fatalf("sync CreateShipmentWithQueue: out=%+v err=%v", syncOut, err)
		}

		// 2. enqueue_only CreateShipment persists job, worker creates shipment with full addresses + outbox.
		enqOut, err := svc.CreateShipmentWithQueue(ctx, CreateShipmentInput{
			OrderID:      "ord-pg-w3b3-enq",
			ShopID:       "shop-a",
			BuyerID:      "buyer-2",
			CarrierCode:  carrier.CodeMockExpress,
			ServiceCode:  "FAST",
			CODAmountVND: 450000,
			WeightGrams:  1200,
			EnqueueOnly:  true,
			FromAddress: carrier.Address{
				Name: "Kho Gửi Đà Nẵng", Phone: "0905556666", ProvinceCode: "48", WardCode: "20242", StreetAddress: "50 Bạch Đằng",
			},
			ToAddress: carrier.Address{
				Name: "Phạm Thị Thu Thảo", Phone: "0988777666", ProvinceCode: "79", WardCode: "26743", StreetAddress: "88 Lê Thánh Tôn",
			},
		})
		if err != nil || !enqOut.Queued || enqOut.JobID == "" {
			t.Fatalf("enqueue_only CreateShipmentWithQueue: out=%+v err=%v", enqOut, err)
		}

		n, err := svc.ProcessDueShipmentCreateJobs(ctx, 10)
		if err != nil || n != 1 {
			t.Fatalf("ProcessDueShipmentCreateJobs: n=%d err=%v", n, err)
		}
		enqSh, err := pgStore.GetActiveShipmentByOrderID(ctx, "ord-pg-w3b3-enq", store.DirectionForward)
		if err != nil {
			t.Fatalf("GetActiveShipmentByOrderID(ord-pg-w3b3-enq): %v", err)
		}

		var outboxCount int
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM outbox_events WHERE subject = $1 AND payload->>'order_id' = 'ord-pg-w3b3-enq'`, events.SubjectShipmentUpdated).Scan(&outboxCount); err != nil || outboxCount != 1 {
			t.Fatalf("expected 1 shipment.updated outbox event for enqueue_only job, got count=%d err=%v", outboxCount, err)
		}

		// 3. Batch tracking code lookup returns both rows with full Vietnamese address fields intact.
		batch, err := pgStore.GetShipmentsByTrackingCodes(ctx, []string{syncOut.Shipment.TrackingCode, enqSh.TrackingCode})
		if err != nil || len(batch) != 2 {
			t.Fatalf("GetShipmentsByTrackingCodes: len=%d err=%v", len(batch), err)
		}
		byCode := map[string]store.Shipment{}
		for _, sh := range batch {
			byCode[sh.TrackingCode] = sh
		}
		gotSync := byCode[syncOut.Shipment.TrackingCode]
		if gotSync.FromName != "Kho Gửi Miền Nam" || gotSync.ToName != "Lê Hoàng Anh" || gotSync.ToStreetAddress != "20 Tràng Tiền" {
			t.Fatalf("sync shipment address mismatch from PG: %+v", gotSync)
		}
		gotEnq := byCode[enqSh.TrackingCode]
		if gotEnq.FromName != "Kho Gửi Đà Nẵng" || gotEnq.FromPhone != "0905556666" || gotEnq.FromStreetAddress != "50 Bạch Đằng" ||
			gotEnq.ToName != "Phạm Thị Thu Thảo" || gotEnq.ToPhone != "0988777666" || gotEnq.ToStreetAddress != "88 Lê Thánh Tôn" {
			t.Fatalf("enqueue_only shipment address mismatch from PG: %+v", gotEnq)
		}
	})

	t.Run("RealPG_EnqueueOnlyRaceWithOrderCancelledAndCancelShipment", func(t *testing.T) {
		for iter := 0; iter < 6; iter++ {
			mockExp := carrier.NewMockExpressAdapter()
			var created atomic.Int32
			var cancelled atomic.Int32
			rec := &reviewRecordingCarrier{
				Carrier: mockExp,
				createFn: func(c context.Context, req carrier.ShipmentRequest) (*carrier.ShipmentResult, error) {
					created.Add(1)
					return mockExp.CreateShipment(c, req)
				},
				cancelFn: func(_ context.Context, _ string) error {
					cancelled.Add(1)
					return nil
				},
			}
			svc := NewShippingService(pgStore, carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), rec), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
			orderID := fmt.Sprintf("ord-pg-g4-race-%d", iter)

			out, err := svc.CreateShipmentWithQueue(ctx, CreateShipmentInput{
				OrderID:     orderID,
				ShopID:      "shop-a",
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
				_, _ = svc.ProcessDueShipmentCreateJobs(ctx, 10)
			}()
			go func() {
				defer wg.Done()
				<-start
				if iter%2 == 0 {
					_, _ = svc.CancelShipment(ctx, orderID, store.DirectionForward, "pg_race_cancel_rpc")
				} else {
					_ = svc.HandleOrderCancelled(ctx, fmt.Sprintf("evt-pg-g4-race-%d", iter), orderID, "pg_race_order_cancelled")
				}
			}()
			close(start)
			wg.Wait()

			_, _ = svc.ProcessDueShipmentCreateJobs(ctx, 10)

			if _, err := pgStore.GetActiveShipmentByOrderID(ctx, orderID, store.DirectionForward); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("iter %d: real PG orphan active shipment survived cancel race (err=%v)", iter, err)
			}
			if created.Load() != cancelled.Load() {
				t.Fatalf("iter %d: real PG carrier orphan mismatch created=%d cancelled=%d", iter, created.Load(), cancelled.Load())
			}
		}
	})
}
