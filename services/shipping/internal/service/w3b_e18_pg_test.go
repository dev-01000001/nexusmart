package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nexusmart/core/services/shipping/internal/carrier"
	"github.com/nexusmart/core/services/shipping/internal/events"
	"github.com/nexusmart/core/services/shipping/internal/store"
)

func openIsolatedServiceTestPG(t *testing.T) (*pgxpool.Pool, *store.PGStore) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_SHIPPING_DB_URL"))
	if dsn == "" {
		t.Skip("set TEST_SHIPPING_DB_URL to run real Postgres E18 shipping service tests")
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse TEST_SHIPPING_DB_URL: %v", err)
	}
	adminURL := *u
	adminURL.Path = "/postgres"

	ctx := context.Background()
	adminConn, err := pgx.Connect(ctx, adminURL.String())
	if err != nil {
		t.Fatalf("connect admin postgres: %v", err)
	}
	dbName := "ship_svc_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if _, err := adminConn.Exec(ctx, "CREATE DATABASE "+dbName); err != nil {
		_ = adminConn.Close(ctx)
		t.Fatalf("create isolated test db %s: %v", dbName, err)
	}
	_ = adminConn.Close(ctx)

	testURL := *u
	testURL.Path = "/" + dbName
	pool, err := pgxpool.New(ctx, testURL.String())
	if err != nil {
		dropIsolatedServiceTestDB(adminURL.String(), dbName)
		t.Fatalf("pgxpool.New(%s): %v", dbName, err)
	}
	lockPool, err := store.NewLockPool(ctx, testURL.String())
	if err != nil {
		pool.Close()
		dropIsolatedServiceTestDB(adminURL.String(), dbName)
		t.Fatalf("NewLockPool(%s): %v", dbName, err)
	}
	t.Cleanup(func() {
		lockPool.Close()
		pool.Close()
		dropIsolatedServiceTestDB(adminURL.String(), dbName)
	})

	pgStore := store.NewPGStoreWithLockPool(pool, lockPool)
	if err := pgStore.MigrateAndSeed(ctx); err != nil {
		t.Fatalf("MigrateAndSeed: %v", err)
	}
	return pool, pgStore
}

func dropIsolatedServiceTestDB(adminDSN, dbName string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close(ctx) }()
	_, _ = conn.Exec(ctx, "DROP DATABASE IF EXISTS "+dbName+" WITH (FORCE)")
}

// TestService_W3B_E18_RealPG_TwoWorkers503AndMidFlightCancel verifies E18 end-to-end on real Postgres:
//  1. Two concurrent workers + injected 503 three times -> exactly ONE row in shipments & ONE shipment.updated outbox event.
//  2. Mid-flight order.cancelled while worker is inside carrier.CreateShipment -> compensating CancelShipment & 0 shipments.
func TestService_W3B_E18_RealPG_TwoWorkers503AndMidFlightCancel(t *testing.T) {
	pool, pgStore := openIsolatedServiceTestPG(t)
	ctx := context.Background()

	t.Run("TwoWorkers_503ThreeTimes_CreatesExactlyOneShipment", func(t *testing.T) {
		mockExp := carrier.NewMockExpressAdapter()
		mockExp.SetTestFaultInjectionEnabled(true)
		if err := mockExp.InjectCreateFault("ord-realpg-503", http.StatusServiceUnavailable, 3); err != nil {
			t.Fatalf("InjectCreateFault: %v", err)
		}
		rec := &reviewRecordingCarrier{Carrier: mockExp}
		agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), rec)

		var clockMu sync.Mutex
		now := time.Date(2026, 11, 24, 15, 0, 0, 0, time.UTC)
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

		svc1 := NewShippingService(pgStore, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		svc2 := NewShippingService(pgStore, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		svc1.nowFn = nowFn
		svc2.nowFn = nowFn

		out, err := svc1.CreateShipmentWithQueue(ctx, CreateShipmentInput{
			OrderID:     "ord-realpg-503",
			ShopID:      "shop-1",
			BuyerID:     "buyer-1",
			CarrierCode: carrier.CodeMockExpress,
			ServiceCode: "STANDARD",
			WeightGrams: 500,
			FromAddress: carrier.Address{ProvinceCode: "79", WardCode: "26740"},
			ToAddress:   carrier.Address{ProvinceCode: "01", WardCode: "00070"},
		})
		if err != nil || !out.Queued || out.JobID == "" {
			t.Fatalf("expected queued job on 503 #1, got out=%+v err=%v", out, err)
		}

		// Three backoff steps (2s -> 503 #2, 4s -> 503 #3, 8s -> 200) raced by 2 workers concurrently on real Postgres.
		for _, step := range []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second} {
			advance(step)
			var wg sync.WaitGroup
			wg.Add(2)
			go func() { defer wg.Done(); _, _ = svc1.ProcessDueShipmentCreateJobs(ctx, 10) }()
			go func() { defer wg.Done(); _, _ = svc2.ProcessDueShipmentCreateJobs(ctx, 10) }()
			wg.Wait()
		}

		var shipCount, outboxCount int
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM shipments WHERE order_id = 'ord-realpg-503'`).Scan(&shipCount); err != nil {
			t.Fatalf("count shipments: %v", err)
		}
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM outbox_events WHERE subject = $1 AND payload->>'order_id' = 'ord-realpg-503'`, events.SubjectShipmentUpdated).Scan(&outboxCount); err != nil {
			t.Fatalf("count outbox: %v", err)
		}
		if shipCount != 1 || outboxCount != 1 {
			t.Fatalf("E18 real PG: want exactly 1 shipment and 1 outbox event, got shipments=%d outbox=%d", shipCount, outboxCount)
		}
		var rawPayload []byte
		if err := pool.QueryRow(ctx, `SELECT payload FROM outbox_events WHERE subject = $1 AND payload->>'order_id' = 'ord-realpg-503'`, events.SubjectShipmentUpdated).Scan(&rawPayload); err != nil {
			t.Fatalf("read outbox payload: %v", err)
		}
		var pl events.ShipmentUpdatedPayload
		if err := json.Unmarshal(rawPayload, &pl); err != nil || pl.Status != carrier.StatusCreated || pl.TrackingCode == "" || pl.LabelURL == "" {
			t.Fatalf("unexpected shipment.updated created payload: %+v err=%v", pl, err)
		}
	})

	t.Run("MidFlightOrderCancelled_CompensatesCancelOnRealPG", func(t *testing.T) {
		mockExp := carrier.NewMockExpressAdapter()
		var firstFail atomic.Bool
		firstFail.Store(true)
		enteredCarrier := make(chan struct{})
		releaseCarrier := make(chan struct{})
		var cancelCalls atomic.Int32

		rec := &reviewRecordingCarrier{
			Carrier: mockExp,
			createFn: func(c context.Context, req carrier.ShipmentRequest) (*carrier.ShipmentResult, error) {
				if firstFail.Swap(false) {
					return nil, errors.New("503 initial")
				}
				close(enteredCarrier)
				<-releaseCarrier
				return mockExp.CreateShipment(c, req)
			},
			cancelFn: func(_ context.Context, _ string) error {
				cancelCalls.Add(1)
				return nil
			},
		}
		svc := NewShippingService(pgStore, carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), rec), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		now := time.Date(2026, 11, 24, 16, 0, 0, 0, time.UTC)
		svc.nowFn = func() time.Time { return now }

		out, err := svc.CreateShipmentWithQueue(ctx, CreateShipmentInput{
			OrderID:     "ord-realpg-cancel",
			ShopID:      "shop-1",
			CarrierCode: carrier.CodeMockExpress,
			WeightGrams: 500,
			FromAddress: carrier.Address{ProvinceCode: "79", WardCode: "26740"},
			ToAddress:   carrier.Address{ProvinceCode: "01", WardCode: "00070"},
		})
		if err != nil || !out.Queued {
			t.Fatalf("expected queued job, got out=%+v err=%v", out, err)
		}

		now = now.Add(5 * time.Second)
		done := make(chan error, 1)
		go func() {
			_, wErr := svc.ProcessDueShipmentCreateJobs(ctx, 10)
			done <- wErr
		}()

		<-enteredCarrier
		if err := svc.HandleOrderCancelled(ctx, "evt-realpg-cancel", "ord-realpg-cancel", "buyer_cancelled"); err != nil {
			t.Fatalf("HandleOrderCancelled: %v", err)
		}
		close(releaseCarrier)
		if err := <-done; err != nil {
			t.Fatalf("worker err: %v", err)
		}

		if cancelCalls.Load() != 1 {
			t.Fatalf("E18 real PG: expected 1 compensating CancelShipment call, got %d", cancelCalls.Load())
		}
		var shipCount int
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM shipments WHERE order_id = 'ord-realpg-cancel'`).Scan(&shipCount); err != nil || shipCount != 0 {
			t.Fatalf("E18 real PG: expected 0 shipments after mid-flight cancel compensation, got %d err=%v", shipCount, err)
		}
		job, err := pgStore.GetShipmentCreateJobByID(ctx, out.JobID)
		if err != nil || job.Status != store.JobStatusCancelled {
			t.Fatalf("expected job status=cancelled, got %+v err=%v", job, err)
		}
	})
}
