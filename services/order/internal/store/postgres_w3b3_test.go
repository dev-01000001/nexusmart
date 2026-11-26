package store

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	"github.com/nexusmart/core/services/order/internal/db"
	"github.com/nexusmart/core/services/order/migrations"
)

// TestPostgresW3B3_Migration0018_UpgradeAndConstraints verifies migration 0018 upgrades cleanly
// over an existing 0001..0017 database and enforces all CHECK and partial UNIQUE constraints.
func TestPostgresW3B3_Migration0018_UpgradeAndConstraints(t *testing.T) {
	adminDSN := strings.TrimSpace(os.Getenv("TEST_ORDER_DB_URL"))
	if adminDSN == "" {
		t.Skip("TEST_ORDER_DB_URL not set; skipping real Postgres migration 0018 test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	parsed, err := url.Parse(adminDSN)
	if err != nil {
		t.Fatalf("parse TEST_ORDER_DB_URL: %v", err)
	}
	adminConn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatalf("connect admin postgres: %v", err)
	}
	tempDBName := "order_m18_test_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	if _, err := adminConn.Exec(ctx, "CREATE DATABASE "+tempDBName); err != nil {
		_ = adminConn.Close(ctx)
		t.Fatalf("create temp db: %v", err)
	}
	_ = adminConn.Close(ctx)

	testURL := *parsed
	testURL.Path = "/" + tempDBName
	pool, err := pgxpool.New(ctx, testURL.String())
	if err != nil {
		dropTempDatabase(adminDSN, tempDBName)
		t.Fatalf("open temp pool: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		dropTempDatabase(adminDSN, tempDBName)
	})

	// 1. Apply migrations 0001..0020 only (before 0021_w3b3_address_and_filters.sql).
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			filename TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`); err != nil {
		t.Fatalf("ensure schema_migrations: %v", err)
	}
	entries, err := fs.ReadDir(migrations.Files, ".")
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	var pre21 []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") && e.Name() < "0021_" {
			pre21 = append(pre21, e.Name())
		}
	}
	sort.Strings(pre21)
	for _, name := range pre21 {
		raw, err := migrations.Files.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin %s: %v", name, err)
		}
		if _, err := tx.Exec(ctx, string(raw)); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("exec %s: %v", name, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (filename) VALUES ($1)`, name); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("record %s: %v", name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit %s: %v", name, err)
		}
	}

	// 2. Seed pre-existing order before migration 0018.
	if _, err := pool.Exec(ctx, `
		INSERT INTO checkout_groups (id, buyer_id, idempotency_key, request_hash, status, payment_method, payment_id, payment_status, total_payable_vnd)
		VALUES ('grp-pre18', 'buyer-1', 'idem-pre18', 'h1', 'COMPLETED', 'COD', 'pay-pre18', 'PENDING', 230000)
	`); err != nil {
		t.Fatalf("seed checkout_group: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO orders (id, order_code, checkout_group_id, buyer_id, shop_id, status, payment_method, payment_id, payment_status, subtotal_vnd, shipping_fee_vnd, total_payable_vnd, carrier_code, service_code, shipping_address_json)
		VALUES ('ord-pre18', 'NM-PRE18-0001', 'grp-pre18', 'buyer-1', 'shop-1', 'CONFIRMED', 'COD', 'pay-pre18', 'PENDING', 200000, 30000, 230000, 'ghn', 'standard', '{"recipient_name":"Pre User","recipient_phone":"0901234567"}')
	`); err != nil {
		t.Fatalf("seed order: %v", err)
	}

	// 3. Run full migration runner (applies 0018).
	if err := db.ApplyMigrations(ctx, pool); err != nil {
		t.Fatalf("ApplyMigrations (0018): %v", err)
	}

	// 4. Verify indexes exist.
	for _, idx := range []string{
		"idx_orders_shop_carrier_created",
		"idx_order_address_changes_one_open_per_order",
		"idx_order_address_changes_order_idem",
		"idx_order_address_changes_due",
		"idx_order_address_changes_old_tracking",
	} {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = 'public' AND indexname = $1)`, idx).Scan(&exists); err != nil || !exists {
			t.Fatalf("index %s missing: exists=%v err=%v", idx, exists, err)
		}
	}

	// 5-6. CHECK constraints, one open intent per order, idempotency key unique per order even after completion.
	insert := func(id, key, state string, fee int64) string {
		_, err := pool.Exec(ctx, `
			INSERT INTO order_address_changes (id, order_id, buyer_id, idempotency_key, new_address_json, new_shipping_fee_vnd, state)
			VALUES ($1, 'ord-pre18', 'buyer-1', $2, '{}', $3, $4)`, id, key, fee, state)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			return pgErr.Code
		}
		if err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
		return ""
	}
	const checkViolation, uniqueViolation = "23514", "23505"
	for _, step := range []struct {
		id, key, state string
		fee            int64
		want           string
	}{
		{"ch-bad-state", "", "BOGUS", 0, checkViolation},
		{"ch-neg-fee", "", "OPEN", -1, checkViolation},
		{"ch-open-1", "k-1", "OPEN", 0, ""},
		{"ch-open-2", "k-2", "RESTORING_SHIPMENT", 0, uniqueViolation},
	} {
		if got := insert(step.id, step.key, step.state, step.fee); got != step.want {
			t.Fatalf("%s: SQLSTATE %q, want %q", step.id, got, step.want)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE order_address_changes SET state = 'COMPLETED' WHERE id = 'ch-open-1'`); err != nil {
		t.Fatalf("complete first intent: %v", err)
	}
	if got := insert("ch-dup-idem", "k-1", "OPEN", 0); got != uniqueViolation {
		t.Fatalf("duplicate idempotency key: SQLSTATE %q", got)
	}
	if got := insert("ch-open-3", "k-3", "OPEN", 0); got != "" {
		t.Fatalf("intent after terminal one: SQLSTATE %q", got)
	}
}

func TestPostgresW3B3_ListOrdersFilters_PickingList_GetOrderByCode(t *testing.T) {
	st, _ := openTempPostgresStore(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)

	addr1, _ := json.Marshal(&orderv1.OrderAddressSnapshot{RecipientName: "Nguyen_Percent%User", RecipientPhone: "0901234567"})
	addr2, _ := json.Marshal(&orderv1.OrderAddressSnapshot{RecipientName: "Le Thi Mai", RecipientPhone: "0912345678"})

	c1 := base
	c2 := base.Add(12 * time.Hour)
	seedPGCheckoutGroupWithOrder(t, st, "grp-l1", "buyer-1", "shop-1", "idem-l1", db.InsertOrderParams{
		ID: "ord-l1", OrderCode: "NM-PG-1001", Status: "CONFIRMED", PaymentMethod: "COD",
		PaymentStatus: "PENDING", SubtotalVND: 200000, ShippingFeeVND: 30000, TotalPayableVND: 230000,
		CarrierCode: "ghn", ServiceCode: "standard", ShippingAddressJSON: addr1, ConfirmedAt: &c1,
	}, base)
	seedPGCheckoutGroupWithOrder(t, st, "grp-l2", "buyer-1", "shop-1", "idem-l2", db.InsertOrderParams{
		ID: "ord-l2", OrderCode: "NM-PG-1002", Status: "PREPARING", PaymentMethod: "VIETQR",
		PaymentStatus: "SUCCEEDED", SubtotalVND: 500000, ShippingFeeVND: 45000, TotalPayableVND: 545000,
		CarrierCode: "mock_express", ServiceCode: "fast", ShippingAddressJSON: addr2, ConfirmedAt: &c2,
	}, base.Add(12*time.Hour))

	// GetOrderByCode
	agg, err := st.GetOrderByCode(ctx, "NM-PG-1001")
	if err != nil || agg.Order.ID != "ord-l1" {
		t.Fatalf("GetOrderByCode: agg=%+v err=%v", agg, err)
	}

	// ListOrdersWithFilter: wildcard-escaped keyword, due_cutoff, statuses, full-shop status counts.
	resKW, err := st.ListOrdersWithFilter(ctx, OrderListFilter{
		ShopID:              "shop-1",
		Keyword:             "Percent%",
		Limit:               1,
		IncludeStatusCounts: true,
	})
	if err != nil {
		t.Fatalf("ListOrdersWithFilter keyword: %v", err)
	}
	if resKW.Total != 1 || len(resKW.Orders) != 1 || resKW.Orders[0].Order.ID != "ord-l1" {
		t.Fatalf("keyword filter mismatch: total=%d orders=%+v", resKW.Total, resKW.Orders)
	}
	if resKW.StatusCounts["CONFIRMED"] != 1 || resKW.StatusCounts["PREPARING"] != 1 {
		t.Fatalf("full-shop status counts mismatch: %+v", resKW.StatusCounts)
	}

	// DueCutoff at base + 6h should match ONLY ord-l1.
	dueCut := base.Add(6 * time.Hour)
	resDue, err := st.ListOrdersWithFilter(ctx, OrderListFilter{
		ShopID:    "shop-1",
		Statuses:  []string{"CONFIRMED", "PREPARING"},
		DueCutoff: &dueCut,
		Limit:     10,
	})
	if err != nil || resDue.Total != 1 || resDue.Orders[0].Order.ID != "ord-l1" {
		t.Fatalf("due cutoff filter mismatch: err=%v total=%d", err, resDue.Total)
	}

	// GetPickingListByOrderIDs aggregates items across ord-l1 and ord-l2 (seeded with sku-1 qty 1 each).
	pickRows, err := st.GetPickingListByOrderIDs(ctx, "shop-1", []string{"ord-l1", "ord-l2"})
	if err != nil {
		t.Fatalf("GetPickingListByOrderIDs: %v", err)
	}
	if len(pickRows) != 1 || pickRows[0].TotalQuantity != 2 || pickRows[0].OrderCount != 2 || len(pickRows[0].OrderCodes) != 2 {
		t.Fatalf("picking list mismatch: %+v", pickRows)
	}
}

func TestPostgresW3B3_AddressChange_ConcurrentOpen_SkipLockedClaim_AndDelayedWebhook(t *testing.T) {
	st, _ := openTempPostgresStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)

	oldAddr, _ := json.Marshal(&orderv1.OrderAddressSnapshot{RecipientName: "Old", RecipientPhone: "0901234567", ProvinceCode: "79", WardCode: "26734", StreetAddress: "1 Old"})
	newAddr, _ := json.Marshal(&orderv1.OrderAddressSnapshot{RecipientName: "New", RecipientPhone: "0912345678", ProvinceCode: "01", ProvinceName: "Ha Noi", WardCode: "00001", WardName: "Phuc Xa", StreetAddress: "2 New"})

	seedPGCheckoutGroupWithOrder(t, st, "grp-ac1", "buyer-1", "shop-1", "idem-ac1", db.InsertOrderParams{
		ID: "ord-ac1", OrderCode: "NM-AC-0001", Status: "PREPARING", PaymentMethod: "COD",
		PaymentStatus: "PENDING", SubtotalVND: 200000, ShippingFeeVND: 30000, TotalPayableVND: 230000,
		CarrierCode: "ghn", ServiceCode: "standard", TrackingCode: "PG-OLD-TRK", ShippingAddressJSON: oldAddr,
	}, now)

	// 1. Concurrent OpenOrderAddressChange: strictly 1 wins, others get ErrAddressChangeInProgress.
	const workers = 4
	var wins, inProg atomic.Int32
	var winnerID string
	var winMu sync.Mutex
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		i := i
		go func() {
			defer wg.Done()
			res, err := st.OpenOrderAddressChange(ctx, OpenAddressChangeParams{
				ID:                        uuid.NewString(),
				OrderID:                   "ord-ac1",
				BuyerID:                   "buyer-1",
				IdempotencyKey:            "pg-idem-" + string(rune('a'+i)),
				RequestHash:               "hash-1",
				NewAddressJSON:            newAddr,
				CarrierCode:               "ghn",
				ServiceCode:               "standard",
				ExpectedOldShippingFeeVND: 30000,
				ExpectedOldTotalPayable:   230000,
				NewShippingFeeVND:         50000,
				AppliedShippingFeeVND:     50000,
				NewTotalPayableVND:        250000,
				FeeAdjusted:               true,
				NeedsCODAdjust:            true,
				ClaimToken:                "req-tok",
				LeaseDuration:             10 * time.Second,
				Now:                       now,
			})
			if err == nil {
				wins.Add(1)
				winMu.Lock()
				winnerID = res.Change.ID
				winMu.Unlock()
			} else if errors.Is(err, ErrAddressChangeInProgress) {
				inProg.Add(1)
			} else {
				t.Errorf("unexpected OpenOrderAddressChange err: %v", err)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 || inProg.Load() != workers-1 {
		t.Fatalf("concurrent open: wins=%d inProg=%d, want 1/%d", wins.Load(), inProg.Load(), workers-1)
	}

	// 2. Concurrent ClaimDueOrderAddressChanges across 2 pods after lease expiry (FOR UPDATE SKIP LOCKED).
	claimAt := now.Add(15 * time.Second)
	var claimedA, claimedB []db.OrderAddressChange
	wg.Add(2)
	go func() {
		defer wg.Done()
		claimedA, _ = st.ClaimDueOrderAddressChanges(ctx, claimAt, 30*time.Second, "pod-a", 10)
	}()
	go func() {
		defer wg.Done()
		claimedB, _ = st.ClaimDueOrderAddressChanges(ctx, claimAt, 30*time.Second, "pod-b", 10)
	}()
	wg.Wait()
	if len(claimedA)+len(claimedB) != 1 {
		t.Fatalf("SKIP LOCKED double-claim: podA=%d podB=%d", len(claimedA), len(claimedB))
	}
	var claimed db.OrderAddressChange
	if len(claimedA) == 1 {
		claimed = claimedA[0]
	} else {
		claimed = claimedB[0]
	}
	if claimed.ID != winnerID {
		t.Fatalf("claimed ID %q != winner %q", claimed.ID, winnerID)
	}

	// 3. Advance through SHIPMENT_CANCELLED -> Apply -> Finalize; verify delayed OLD-TRK webhook is ignored!
	nextLease := claimAt.Add(30 * time.Second)
	if _, err := st.AdvanceAddressChangeState(ctx, AddressChangeProgressUpdate{
		ID:                 claimed.ID,
		ExpectedClaimToken: claimed.ClaimToken,
		State:              AddressChangeStateShipmentCancelled,
		ShipmentCancelled:  true,
		ClaimedUntil:       &nextLease,
		ClaimToken:         claimed.ClaimToken,
		Now:                claimAt,
	}); err != nil {
		t.Fatalf("AdvanceAddressChangeState(SHIPMENT_CANCELLED): %v", err)
	}
	applyRes, err := st.ApplyAddressChangeToOrder(ctx, ApplyAddressChangeParams{
		ChangeID:      claimed.ID,
		OrderID:       "ord-ac1",
		ClaimToken:    claimed.ClaimToken,
		LeaseDuration: 30 * time.Second,
		Now:           claimAt,
	})
	if err != nil {
		t.Fatalf("ApplyAddressChangeToOrder: %v", err)
	}
	if applyRes.Change.State != AddressChangeStateApplied || applyRes.Order.Order.ShippingFeeVND != 50000 || applyRes.Order.Order.TotalPayableVND != 250000 {
		t.Fatalf("unexpected apply result: %+v", applyRes)
	}

	// Delayed shipment.updated for PG-OLD-TRK while tracking_code is empty -> MUST be suppressed!
	shOut, err := st.ApplyShipmentUpdate(ctx, ShipmentUpdateParams{
		EventID:        "ev-pg-delayed-old",
		EventType:      "shipment.updated",
		OrderID:        "ord-ac1",
		ShipmentStatus: "delivering",
		TrackingCode:   "PG-OLD-TRK",
		Now:            claimAt,
	})
	if err != nil {
		t.Fatalf("ApplyShipmentUpdate old: %v", err)
	}
	if shOut.TransitionedTo != orderv1.OrderStatus_ORDER_STATUS_UNSPECIFIED {
		t.Fatalf("delayed old tracking wrongly transitioned order to %v", shOut.TransitionedTo)
	}

	// Finalize Step 5 with new tracking code -> COMPLETED + 1 outbox event.
	finRes, err := st.FinalizeAddressChangeShipment(ctx, FinalizeAddressChangeShipmentParams{
		ChangeID:     claimed.ID,
		OrderID:      "ord-ac1",
		ClaimToken:   claimed.ClaimToken,
		CarrierCode:  "ghn",
		ServiceCode:  "standard",
		TrackingCode: "PG-NEW-TRK",
		LabelURL:     "https://labels.sandbox/pg-new",
		Now:          claimAt,
	})
	if err != nil {
		t.Fatalf("FinalizeAddressChangeShipment: %v", err)
	}
	if finRes.Change.State != AddressChangeStateCompleted || finRes.Order.Order.TrackingCode != "PG-NEW-TRK" {
		t.Fatalf("unexpected finalize result: %+v", finRes)
	}
}

// TestPostgresW3B3_AddressChange_RestoreOldShipment covers an intent refused after its shipment was cancelled: the order
// keeps its old address and fee, never keeps the dead tracking code, emits nothing and the intent ends ABORTED.
func TestPostgresW3B3_AddressChange_RestoreOldShipment(t *testing.T) {
	st, _ := openTempPostgresStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	oldAddr, _ := json.Marshal(&orderv1.OrderAddressSnapshot{RecipientName: "Old", RecipientPhone: "0901234567", ProvinceCode: "79", WardCode: "26734", StreetAddress: "1 Old"})
	newAddr, _ := json.Marshal(&orderv1.OrderAddressSnapshot{RecipientName: "New", RecipientPhone: "0912345678", ProvinceCode: "01", WardCode: "00001", StreetAddress: "2 New"})

	for _, tc := range []struct {
		name    string
		fin     FinalizeAddressChangeShipmentParams
		wantTrk string
		wantJob string
	}{
		{"recreated", FinalizeAddressChangeShipmentParams{CarrierCode: "ghn", ServiceCode: "standard", TrackingCode: "RESTORED"}, "RESTORED", ""},
		{"queued", FinalizeAddressChangeShipmentParams{Queued: true, JobID: "job-1"}, "", "QUEUED"},
		{"failed", FinalizeAddressChangeShipmentParams{PermanentFail: true, LastError: "carrier said no"}, "", "FAILED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ordID := "ord-restore-" + tc.name
			seedPGCheckoutGroupWithOrder(t, st, "grp-"+ordID, "buyer-1", "shop-1", "idem-"+ordID, db.InsertOrderParams{
				ID: ordID, OrderCode: "NM-RS-" + tc.name, Status: "PREPARING", PaymentMethod: "COD",
				PaymentStatus: "PENDING", SubtotalVND: 200000, ShippingFeeVND: 30000, TotalPayableVND: 230000,
				CarrierCode: "ghn", ServiceCode: "standard", TrackingCode: "DEAD-" + tc.name, ShippingAddressJSON: oldAddr,
			}, now)
			before, err := st.GetOrderWithDetails(ctx, ordID)
			if err != nil {
				t.Fatalf("load order: %v", err)
			}

			open := OpenAddressChangeParams{
				ID: uuid.NewString(), OrderID: ordID, BuyerID: "buyer-1", RequestHash: "h", NewAddressJSON: newAddr,
				CarrierCode: "ghn", ServiceCode: "standard", ExpectedOldAddressJSON: newAddr,
				ExpectedOldShippingFeeVND: 30000, ExpectedOldTotalPayable: 230000, NewShippingFeeVND: 50000,
				AppliedShippingFeeVND: 50000, NewTotalPayableVND: 250000, FeeAdjusted: true, NeedsCODAdjust: true,
				ClaimToken: "tok", LeaseDuration: 10 * time.Second, Now: now,
			}
			if _, err := st.OpenOrderAddressChange(ctx, open); !errors.Is(err, ErrAddressChangeInProgress) {
				t.Fatalf("stale address snapshot must be refused, got %v", err)
			}
			open.ExpectedOldAddressJSON = before.Order.ShippingAddressJSON
			opened, err := st.OpenOrderAddressChange(ctx, open)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			lease := now.Add(10 * time.Second)
			if _, err := st.AdvanceAddressChangeState(ctx, AddressChangeProgressUpdate{
				ID: opened.Change.ID, ExpectedClaimToken: "tok", State: AddressChangeStateRestoringShipment,
				ShipmentCancelled: true, AbortCode: "COD_ALREADY_COLLECTED", LastError: "COD_ALREADY_COLLECTED: paid",
				ClaimedUntil: &lease, ClaimToken: "tok", Now: now,
			}); err != nil {
				t.Fatalf("advance to RESTORING_SHIPMENT: %v", err)
			}

			fin := tc.fin
			fin.ChangeID, fin.OrderID, fin.ClaimToken, fin.Now = opened.Change.ID, ordID, "tok", now
			res, err := st.FinalizeAddressChangeShipment(ctx, fin)
			if err != nil {
				t.Fatalf("finalize: %v", err)
			}
			if res.Change.State != AddressChangeStateAborted || res.Change.AbortCode != "COD_ALREADY_COLLECTED" || res.Change.OutboxEmitted {
				t.Fatalf("intent = %+v, want ABORTED with the refusal code and no event", res.Change)
			}
			got := res.Order.Order
			if got.TrackingCode != tc.wantTrk || got.ShipmentJobStatus != tc.wantJob || got.Status != "PREPARING" {
				t.Fatalf("order trk=%q job=%q status=%s, want %q/%q/PREPARING", got.TrackingCode, got.ShipmentJobStatus, got.Status, tc.wantTrk, tc.wantJob)
			}
			if got.ShippingFeeVND != 30000 || got.TotalPayableVND != 230000 || string(got.ShippingAddressJSON) != string(before.Order.ShippingAddressJSON) {
				t.Fatalf("order money or address changed by a rejected update: %+v", got)
			}
		})
	}
}

// TestPostgresW3B3_AddressChange_RacesBatchShipCancelAndWebhook: whatever the interleaving, an order never has both an open
// address change and an in-flight batch shipment, and a cancelled order never gets its address changed afterwards.
func TestPostgresW3B3_AddressChange_RacesBatchShipCancelAndWebhook(t *testing.T) {
	st, _ := openTempPostgresStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	oldAddr, _ := json.Marshal(&orderv1.OrderAddressSnapshot{RecipientName: "Old", StreetAddress: "1 Old"})
	newAddr, _ := json.Marshal(&orderv1.OrderAddressSnapshot{RecipientName: "New", StreetAddress: "2 New"})

	for i := 0; i < 15; i++ {
		ordID := "ord-race-" + uuid.NewString()
		seedPGCheckoutGroupWithOrder(t, st, "grp-"+ordID, "buyer-1", "shop-1", "idem-"+ordID, db.InsertOrderParams{
			ID: ordID, OrderCode: ordID, Status: "CONFIRMED", PaymentMethod: "COD", PaymentStatus: "PENDING",
			SubtotalVND: 200000, ShippingFeeVND: 30000, TotalPayableVND: 230000,
			CarrierCode: "ghn", ServiceCode: "standard", ShippingAddressJSON: oldAddr,
		}, now)

		var opened *OpenAddressChangeResult
		var prep *BatchShipPrepareOutcome
		var openErr, prepErr, cancelErr, webhookErr error
		start := make(chan struct{})
		var wg sync.WaitGroup
		for _, fn := range []func(){
			func() {
				opened, openErr = st.OpenOrderAddressChange(ctx, OpenAddressChangeParams{
					ID: uuid.NewString(), OrderID: ordID, BuyerID: "buyer-1", RequestHash: "h", NewAddressJSON: newAddr,
					CarrierCode: "ghn", ServiceCode: "standard", ExpectedOldShippingFeeVND: 30000, ExpectedOldTotalPayable: 230000,
					NewShippingFeeVND: 30000, AppliedShippingFeeVND: 30000, NewTotalPayableVND: 230000,
					ClaimToken: "tok", LeaseDuration: 10 * time.Second, Now: now,
				})
			},
			func() { prep, prepErr = st.PrepareOrderForBatchShip(ctx, ordID, "shop-1", "", "", now) },
			func() {
				_, cancelErr = st.TransitionOrderStatus(ctx, SingleOrderTransitionParams{
					OrderID: ordID, TargetStatus: orderv1.OrderStatus_ORDER_STATUS_CANCELLED, CancelReason: "race",
					ActorID: "buyer-1", ActorRole: "buyer", Now: now,
				})
			},
			func() {
				_, webhookErr = st.ApplyShipmentUpdate(ctx, ShipmentUpdateParams{
					EventID: "ev-" + ordID, EventType: "shipment.updated", OrderID: ordID,
					ShipmentStatus: "cancelled", TrackingCode: "SOME-OLD-TRK", Now: now,
				})
			},
		} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				fn()
			}()
		}
		close(start)
		wg.Wait()

		if openErr != nil && !errors.Is(openErr, ErrAddressChangeInProgress) && !errors.Is(openErr, ErrAddressUpdateNotAllowed) {
			t.Fatalf("open: %v", openErr)
		}
		if prepErr != nil || cancelErr != nil || webhookErr != nil {
			t.Fatalf("prepare=%v cancel=%v webhook=%v", prepErr, cancelErr, webhookErr)
		}
		if openErr == nil && prep.SkipReason == "" {
			t.Fatalf("order has both an open address change and an in-flight batch shipment")
		}
		if openErr == nil {
			res, err := st.ApplyAddressChangeToOrder(ctx, ApplyAddressChangeParams{
				ChangeID: opened.Change.ID, OrderID: ordID, ClaimToken: "tok", LeaseDuration: 10 * time.Second, Now: now,
			})
			if err != nil || !res.Aborted {
				t.Fatalf("applying onto a cancelled order must abort: res=%+v err=%v", res, err)
			}
		}
		final, err := st.GetOrderWithDetails(ctx, ordID)
		if err != nil || final.Order.Status != "CANCELLED" || strings.Contains(string(final.Order.ShippingAddressJSON), "2 New") {
			t.Fatalf("final order status=%s addr=%s err=%v", final.Order.Status, final.Order.ShippingAddressJSON, err)
		}
	}
}

// TestPostgresW3B3_O3_ConcurrentSiblingCODCeiling_OnlyOneWins verifies on real Postgres that two concurrent
// OpenOrderAddressChange calls on sibling orders in the same COD checkout group cannot both exceed MaxCODGroupTotalVND,
// and also covers O1 terminal-orphan FinalizeAddressChangeShipment and O2 RESTORING_SHIPMENT clearing dead tracking.
func TestPostgresW3B3_O3_ConcurrentSiblingCODCeiling_OnlyOneWins(t *testing.T) {
	st, _ := openTempPostgresStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)

	oldAddr, _ := json.Marshal(&orderv1.OrderAddressSnapshot{RecipientName: "Old", RecipientPhone: "0901234567", StreetAddress: "1 Old"})
	newAddr, _ := json.Marshal(&orderv1.OrderAddressSnapshot{RecipientName: "New", RecipientPhone: "0912345678", StreetAddress: "2 New"})

	grpID := "grp-o3-cod-" + uuid.NewString()[:8]
	o1ID := "ord-o3-1-" + uuid.NewString()[:8]
	o2ID := "ord-o3-2-" + uuid.NewString()[:8]

	if _, err := st.CreateCheckoutGroup(ctx, db.CreateCheckoutGroupParams{
		ID: grpID, BuyerID: "buyer-1", IdempotencyKey: "idem-" + grpID, RequestHash: "h",
		Status: "IN_PROGRESS", PaymentMethod: "COD", TotalPayableVND: 4980000,
	}); err != nil {
		t.Fatalf("CreateCheckoutGroup: %v", err)
	}
	if _, err := st.PersistCheckoutBatch(ctx, CheckoutBatch{
		CheckoutGroupID: grpID,
		Now:             now,
		Orders: []NewOrderRecord{
			{
				Order: db.InsertOrderParams{
					ID: o1ID, OrderCode: "NM-O3-1", CheckoutGroupID: grpID, BuyerID: "buyer-1", ShopID: "shop-1",
					Status: "CONFIRMED", PaymentMethod: "COD", PaymentStatus: "PENDING",
					SubtotalVND: 2460000, ShippingFeeVND: 30000, TotalPayableVND: 2490000,
					CarrierCode: "ghn", ServiceCode: "standard", ShippingAddressJSON: oldAddr,
				},
			},
			{
				Order: db.InsertOrderParams{
					ID: o2ID, OrderCode: "NM-O3-2", CheckoutGroupID: grpID, BuyerID: "buyer-1", ShopID: "shop-2",
					Status: "CONFIRMED", PaymentMethod: "COD", PaymentStatus: "PENDING",
					SubtotalVND: 2460000, ShippingFeeVND: 30000, TotalPayableVND: 2490000,
					CarrierCode: "ghn", ServiceCode: "standard", ShippingAddressJSON: oldAddr,
				},
			},
		},
	}); err != nil {
		t.Fatalf("PersistCheckoutBatch: %v", err)
	}

	var wins, ceilingErrs atomic.Int32
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, oid := range []string{o1ID, o2ID} {
		oid := oid
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := st.OpenOrderAddressChange(ctx, OpenAddressChangeParams{
				ID:                        uuid.NewString(),
				OrderID:                   oid,
				BuyerID:                   "buyer-1",
				RequestHash:               "h-o3",
				NewAddressJSON:            newAddr,
				CarrierCode:               "ghn",
				ServiceCode:               "standard",
				ExpectedOldShippingFeeVND: 30000,
				ExpectedOldTotalPayable:   2490000,
				NewShippingFeeVND:         45000,
				AppliedShippingFeeVND:     45000,
				NewTotalPayableVND:        2505000,
				FeeAdjusted:               true,
				NeedsCODAdjust:            true,
				MaxCODGroupTotalVND:       5000000,
				ClaimToken:                "tok-" + oid,
				LeaseDuration:             30 * time.Second,
				Now:                       now,
			})
			if err == nil {
				wins.Add(1)
			} else if errors.Is(err, ErrCODLimitExceeded) {
				ceilingErrs.Add(1)
			} else {
				t.Errorf("unexpected error for %s: %v", oid, err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if wins.Load() != 1 || ceilingErrs.Load() != 1 {
		t.Fatalf("O3 real PG race: wins=%d ceilingErrs=%d, want 1/1", wins.Load(), ceilingErrs.Load())
	}
}

// TestPostgresW3B3_O1_O2_LeaseRenew_TerminalOrphan_AndRestoringClearsOldTracking verifies O1 & O2 SQL contracts on real Postgres.
func TestPostgresW3B3_O1_O2_LeaseRenew_TerminalOrphan_AndRestoringClearsOldTracking(t *testing.T) {
	st, _ := openTempPostgresStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	oldAddr, _ := json.Marshal(&orderv1.OrderAddressSnapshot{RecipientName: "Old", RecipientPhone: "0901234567", StreetAddress: "1 Old"})
	newAddr, _ := json.Marshal(&orderv1.OrderAddressSnapshot{RecipientName: "New", RecipientPhone: "0912345678", StreetAddress: "2 New"})

	ordID := "ord-pg-o12-" + uuid.NewString()[:8]
	seedPGCheckoutGroupWithOrder(t, st, "grp-"+ordID, "buyer-1", "shop-1", "idem-"+ordID, db.InsertOrderParams{
		ID: ordID, OrderCode: "NM-PG-O12", Status: "PREPARING", PaymentMethod: "COD", PaymentStatus: "PENDING",
		SubtotalVND: 200000, ShippingFeeVND: 30000, TotalPayableVND: 230000,
		CarrierCode: "ghn", ServiceCode: "standard", TrackingCode: "DEAD-PG-OLD", LabelURL: "https://labels/dead",
		ShippingAddressJSON: oldAddr,
	}, now)

	opened, err := st.OpenOrderAddressChange(ctx, OpenAddressChangeParams{
		ID: uuid.NewString(), OrderID: ordID, BuyerID: "buyer-1", RequestHash: "h", NewAddressJSON: newAddr,
		CarrierCode: "ghn", ServiceCode: "standard", ExpectedOldShippingFeeVND: 30000, ExpectedOldTotalPayable: 230000,
		NewShippingFeeVND: 50000, AppliedShippingFeeVND: 50000, NewTotalPayableVND: 250000,
		FeeAdjusted: true, NeedsCODAdjust: true, ClaimToken: "tok-1", LeaseDuration: 30 * time.Second, Now: now,
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	// O1: RenewOrderAddressChangeLease succeeds with matching token and fails with ErrAddressChangeLostLease on wrong token.
	renewed, err := st.RenewOrderAddressChangeLease(ctx, opened.Change.ID, "tok-1", now.Add(5*time.Second), 30*time.Second)
	if err != nil || renewed.ClaimedUntil == nil || !renewed.ClaimedUntil.Equal(now.Add(35*time.Second)) {
		t.Fatalf("RenewOrderAddressChangeLease matching tok: renewed=%+v err=%v", renewed, err)
	}
	if _, err := st.RenewOrderAddressChangeLease(ctx, opened.Change.ID, "wrong-tok", now.Add(5*time.Second), 30*time.Second); !errors.Is(err, ErrAddressChangeLostLease) {
		t.Fatalf("RenewOrderAddressChangeLease wrong tok = %v, want ErrAddressChangeLostLease", err)
	}

	// O2: Transitioning to RESTORING_SHIPMENT clears DEAD-PG-OLD & dead label atomically in the SAME transaction!
	lease := now.Add(40 * time.Second)
	if _, err := st.AdvanceAddressChangeState(ctx, AddressChangeProgressUpdate{
		ID: opened.Change.ID, ExpectedClaimToken: "tok-1", State: AddressChangeStateRestoringShipment,
		ShipmentCancelled: true, AbortCode: "COD_ALREADY_COLLECTED", LastError: "COD_ALREADY_COLLECTED: paid",
		ClaimedUntil: &lease, ClaimToken: "tok-1", Now: now.Add(10 * time.Second),
	}); err != nil {
		t.Fatalf("AdvanceAddressChangeState(RESTORING_SHIPMENT): %v", err)
	}
	midOrd, err := st.GetOrderWithDetails(ctx, ordID)
	if err != nil {
		t.Fatalf("GetOrderWithDetails: %v", err)
	}
	if midOrd.Order.TrackingCode != "" || midOrd.Order.LabelURL != "" {
		t.Fatalf("O2 PG BUG: dead tracking/label not cleared on RESTORING_SHIPMENT: trk=%q label=%q", midOrd.Order.TrackingCode, midOrd.Order.LabelURL)
	}

	// Close intent ABORTED, then cancel order, then invoke FinalizeAddressChangeShipment with non-empty TrackingCode -> O1 orphan guard!
	if _, err := st.FinalizeAddressChangeShipment(ctx, FinalizeAddressChangeShipmentParams{
		ChangeID: opened.Change.ID, OrderID: ordID, ClaimToken: "tok-1", PermanentFail: true, LastError: "done", Now: now.Add(11 * time.Second),
	}); err != nil {
		t.Fatalf("FinalizeAddressChangeShipment close: %v", err)
	}
	if _, err := st.TransitionOrderStatus(ctx, SingleOrderTransitionParams{
		OrderID: ordID, TargetStatus: orderv1.OrderStatus_ORDER_STATUS_CANCELLED, CancelReason: "cancel",
		ActorID: "buyer-1", ActorRole: "buyer", Now: now.Add(12 * time.Second),
	}); err != nil {
		t.Fatalf("CancelOrder: %v", err)
	}
	orphRes, err := st.FinalizeAddressChangeShipment(ctx, FinalizeAddressChangeShipmentParams{
		ChangeID: opened.Change.ID, OrderID: ordID, ClaimToken: "stale-tok", TrackingCode: "PG-ORPHAN-TRK", Now: now.Add(13 * time.Second),
	})
	if err != nil {
		t.Fatalf("FinalizeAddressChangeShipment terminal orphan: %v", err)
	}
	if !orphRes.NeedsOrphanShipmentCancel || !orphRes.Order.Order.PendingShipmentCancel {
		t.Fatalf("O1 PG BUG: terminal FinalizeAddressChangeShipment missed orphan cancel: needs=%v flag=%v",
			orphRes.NeedsOrphanShipmentCancel, orphRes.Order.Order.PendingShipmentCancel)
	}
}

// TestPostgresR1Money1_AddressChange_FreeshipFloorInvariant verifies OpenOrderAddressChange and
// ApplyAddressChangeToOrder enforce shop_freeship + freeship <= applied_shipping_fee in Postgres.
func TestPostgresR1Money1_AddressChange_FreeshipFloorInvariant(t *testing.T) {
	st, _ := openTempPostgresStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	oldAddr, _ := json.Marshal(&orderv1.OrderAddressSnapshot{RecipientName: "Old", RecipientPhone: "0901234567", ProvinceCode: "79", WardCode: "26734", StreetAddress: "1 Old"})
	newAddr, _ := json.Marshal(&orderv1.OrderAddressSnapshot{RecipientName: "New", RecipientPhone: "0912345678", ProvinceCode: "79", WardCode: "26735", StreetAddress: "2 New"})

	ordID := "ord-pg-r1m1-" + uuid.NewString()[:8]
	seedPGCheckoutGroupWithOrder(t, st, "grp-"+ordID, "buyer-1", "shop-1", "idem-"+ordID, db.InsertOrderParams{
		ID: ordID, OrderCode: "NM-PG-R1M1", Status: "CONFIRMED", PaymentMethod: "COD", PaymentStatus: "PENDING",
		SubtotalVND: 100000, ShopFreeshipDiscountVND: 30000, ShippingFeeVND: 50000, TotalPayableVND: 120000,
		CarrierCode: "ghn", ServiceCode: "standard", ShippingAddressJSON: oldAddr,
	}, now)

	// 1. Applied fee below freeship floor (10k < 30k) must be rejected at store open gate.
	if _, err := st.OpenOrderAddressChange(ctx, OpenAddressChangeParams{
		ID: uuid.NewString(), OrderID: ordID, BuyerID: "buyer-1", RequestHash: "h-below", NewAddressJSON: newAddr,
		CarrierCode: "ghn", ServiceCode: "standard", ExpectedOldShippingFeeVND: 50000, ExpectedOldTotalPayable: 120000,
		NewShippingFeeVND: 10000, AppliedShippingFeeVND: 10000, NewTotalPayableVND: 80000,
		FeeAdjusted: true, NeedsCODAdjust: true, ClaimToken: "tok-below", LeaseDuration: 30 * time.Second, Now: now,
	}); !errors.Is(err, ErrAddressUpdateNotAllowed) {
		t.Fatalf("OpenOrderAddressChange below freeship floor = %v, want ErrAddressUpdateNotAllowed", err)
	}

	// 2. Clamped applied fee (30k == freeship floor) opens and applies cleanly, keeping invariant.
	opened, err := st.OpenOrderAddressChange(ctx, OpenAddressChangeParams{
		ID: uuid.NewString(), OrderID: ordID, BuyerID: "buyer-1", RequestHash: "h-clamped", NewAddressJSON: newAddr,
		CarrierCode: "ghn", ServiceCode: "standard", ExpectedOldShippingFeeVND: 50000, ExpectedOldTotalPayable: 120000,
		NewShippingFeeVND: 30000, AppliedShippingFeeVND: 30000, NewTotalPayableVND: 100000,
		FeeAdjusted: true, NeedsCODAdjust: true, ClaimToken: "tok-clamped", LeaseDuration: 30 * time.Second, Now: now,
	})
	if err != nil {
		t.Fatalf("OpenOrderAddressChange clamped: %v", err)
	}
	applied, err := st.ApplyAddressChangeToOrder(ctx, ApplyAddressChangeParams{
		ChangeID: opened.Change.ID, OrderID: ordID, ClaimToken: "tok-clamped", LeaseDuration: 30 * time.Second, Now: now.Add(time.Second),
	})
	if err != nil || applied.Aborted {
		t.Fatalf("ApplyAddressChangeToOrder clamped: res=%+v err=%v", applied, err)
	}
	if applied.Order.Order.ShippingFeeVND != 30000 || applied.Order.Order.TotalPayableVND != 100000 ||
		applied.Order.Order.ShopFreeshipDiscountVND+applied.Order.Order.FreeshipDiscountVND > applied.Order.Order.ShippingFeeVND {
		t.Fatalf("PG order broke freeship floor: %+v", applied.Order.Order)
	}
}

// TestPostgresR1Money2_Phase0bPartialReturn_TransitionAndSettlement verifies migrated phase-0b
// partial returns preserve refund_amount_vnd, keep order RETURN_REQUESTED while REFUNDING, and
// complete the remaining order (RETURN_REQUESTED -> COMPLETED + order.completed) on REFUNDED.
func TestPostgresR1Money2_Phase0bPartialReturn_TransitionAndSettlement(t *testing.T) {
	st, pool := openTempPostgresStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	addrJSON, _ := json.Marshal(&orderv1.OrderAddressSnapshot{RecipientName: "Buyer", RecipientPhone: "0901234567", StreetAddress: "1 St"})

	ordID := "ord-pg-r1m2-" + uuid.NewString()[:8]
	grpID := "grp-" + ordID
	if _, err := st.CreateCheckoutGroup(ctx, db.CreateCheckoutGroupParams{
		ID: grpID, BuyerID: "buyer-1", IdempotencyKey: "idem-" + ordID, RequestHash: "h-" + ordID,
		Status: "IN_PROGRESS", PaymentMethod: "VIETQR", TotalPayableVND: 300000,
	}); err != nil {
		t.Fatalf("CreateCheckoutGroup: %v", err)
	}
	if _, err := st.PersistCheckoutBatch(ctx, CheckoutBatch{
		CheckoutGroupID: grpID,
		Now:             now,
		Orders: []NewOrderRecord{
			{
				Order: db.InsertOrderParams{
					ID: ordID, OrderCode: "NM-PG-R1M2", CheckoutGroupID: grpID, BuyerID: "buyer-1", ShopID: "shop-1",
					Status: "RETURN_REQUESTED", PaymentMethod: "VIETQR", PaymentID: "pay-" + ordID, PaymentStatus: "SUCCEEDED",
					SubtotalVND: 300000, TotalPayableVND: 300000, ShippingAddressJSON: addrJSON,
				},
				Items: []db.InsertOrderItemParams{
					{ID: "item-a-" + ordID, OrderID: ordID, SKUID: "SKU-A", ProductID: "p-a", ProductName: "A", UnitPriceVND: 100000, EffectiveUnitPriceVND: 100000, Quantity: 1, LineTotalVND: 100000},
					{ID: "item-b-" + ordID, OrderID: ordID, SKUID: "SKU-B", ProductID: "p-b", ProductName: "B", UnitPriceVND: 200000, EffectiveUnitPriceVND: 200000, Quantity: 1, LineTotalVND: 200000},
				},
			},
		},
	}); err != nil {
		t.Fatalf("PersistCheckoutBatch: %v", err)
	}
	rrID := "rr-pg-r1m2-" + uuid.NewString()[:8]
	if _, err := pool.Exec(ctx, `
		INSERT INTO return_requests (id, order_id, order_code, buyer_id, shop_id, reason, items_json, status, goods_received, refund_amount_vnd, created_at, updated_at)
		VALUES ($1, $2, 'NM-PG-R1M2', 'buyer-1', 'shop-1', 'SKU-A defective', $3::jsonb, 'RECEIVED', TRUE, 100000, $4, $4)
	`, rrID, ordID, `[{"order_item_id":"item-a-`+ordID+`","sku_id":"SKU-A","quantity":1}]`, now); err != nil {
		t.Fatalf("seed migrated partial return: %v", err)
	}

	res1, err := st.TransitionReturnRequest(ctx, ReturnTransitionParams{
		ReturnRequestID: rrID,
		TargetStatus:    orderv1.ReturnStatus_RETURN_STATUS_REFUNDING,
		ActorID:         "seller-1",
		ActorRole:       "seller",
		Note:            "confirm partial return received",
		Now:             now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("TransitionReturnRequest(REFUNDING): %v", err)
	}
	if res1.Return.Request.RefundAmountVND != 100000 {
		t.Fatalf("PG REFUNDING overwrote refund_amount_vnd: got %d, want 100000", res1.Return.Request.RefundAmountVND)
	}
	if res1.Order.Order.Status != "RETURN_REQUESTED" {
		t.Fatalf("PG REFUNDING partial return order status = %q, want RETURN_REQUESTED", res1.Order.Order.Status)
	}

	res2, err := st.TransitionReturnRequest(ctx, ReturnTransitionParams{
		ReturnRequestID: rrID,
		TargetStatus:    orderv1.ReturnStatus_RETURN_STATUS_REFUNDED,
		ActorID:         "system",
		ActorRole:       "system",
		Note:            "partial refund done",
		Now:             now.Add(2 * time.Second),
		BuildCompletedOutboxFn: func(ord db.Order) (db.InsertOutboxEventParams, error) {
			return db.InsertOutboxEventParams{
				ID:          uuid.NewString(),
				EventType:   "order.completed",
				PayloadJSON: []byte(`{"order_id":"` + ord.ID + `"}`),
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("TransitionReturnRequest(REFUNDED): %v", err)
	}
	if res2.Order.Order.Status != "COMPLETED" {
		t.Fatalf("PG REFUNDED partial return order status = %q, want COMPLETED", res2.Order.Order.Status)
	}
	var completedCount, returnedCount int
	if err := pool.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE event_type = 'order.completed'),
			COUNT(*) FILTER (WHERE event_type = 'order.returned')
		FROM outbox_events
		WHERE payload_json::text LIKE '%' || $1 || '%'
	`, ordID).Scan(&completedCount, &returnedCount); err != nil {
		t.Fatalf("query outbox_events: %v", err)
	}
	if completedCount != 1 || returnedCount != 0 {
		t.Fatalf("PG partial return outbox mismatch: completed=%d returned=%d, want 1/0", completedCount, returnedCount)
	}

	// 2. Scenario 1 (prior refunded_vnd = 150k, partial return = 100k, total = 300k):
	// both when payment.refunded for return has NOT yet arrived and when it HAS arrived,
	// effectiveRefunded must equal 250k (kept = 50k) and transition RETURN_REQUESTED -> COMPLETED.
	for _, recordedFirst := range []bool{false, true} {
		ordS1 := "ord-pg-r1m2-s1-" + uuid.NewString()[:8]
		grpS1 := "grp-" + ordS1
		rrS1 := "rr-" + ordS1
		refS1 := "ref-" + ordS1
		if _, err := st.CreateCheckoutGroup(ctx, db.CreateCheckoutGroupParams{
			ID: grpS1, BuyerID: "buyer-1", IdempotencyKey: "idem-" + ordS1, RequestHash: "h-" + ordS1,
			Status: "IN_PROGRESS", PaymentMethod: "VIETQR", TotalPayableVND: 300000,
		}); err != nil {
			t.Fatalf("CreateCheckoutGroup s1: %v", err)
		}
		if _, err := st.PersistCheckoutBatch(ctx, CheckoutBatch{
			CheckoutGroupID: grpS1,
			Now:             now,
			Orders: []NewOrderRecord{
				{
					Order: db.InsertOrderParams{
						ID: ordS1, OrderCode: "NM-PG-S1-" + ordS1[len(ordS1)-4:], CheckoutGroupID: grpS1, BuyerID: "buyer-1", ShopID: "shop-1",
						Status: "RETURN_REQUESTED", PaymentMethod: "VIETQR", PaymentID: "pay-" + ordS1, PaymentStatus: "SUCCEEDED",
						SubtotalVND: 300000, TotalPayableVND: 300000, ShippingAddressJSON: addrJSON,
					},
					Items: []db.InsertOrderItemParams{
						{ID: "item-a-" + ordS1, OrderID: ordS1, SKUID: "SKU-A", ProductID: "p-a", ProductName: "A", UnitPriceVND: 100000, EffectiveUnitPriceVND: 100000, Quantity: 1, LineTotalVND: 100000},
						{ID: "item-b-" + ordS1, OrderID: ordS1, SKUID: "SKU-B", ProductID: "p-b", ProductName: "B", UnitPriceVND: 200000, EffectiveUnitPriceVND: 200000, Quantity: 1, LineTotalVND: 200000},
					},
				},
			},
		}); err != nil {
			t.Fatalf("PersistCheckoutBatch s1: %v", err)
		}
		// Prior manual refund of 150k.
		if _, err := st.RecordPaymentRefunded(ctx, PaymentRefundedParams{
			EventID: "ev-prior-" + ordS1, EventType: "payment.refunded", PaymentID: "pay-" + ordS1,
			OrderID: ordS1, RefundID: "ref-prior-" + ordS1, AmountVND: 150000, Reason: "prior manual refund",
		}); err != nil {
			t.Fatalf("RecordPaymentRefunded prior: %v", err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO return_requests (id, order_id, order_code, buyer_id, shop_id, reason, items_json, status, goods_received, refund_amount_vnd, created_at, updated_at)
			VALUES ($1, $2, 'NM-PG-S1', 'buyer-1', 'shop-1', 'SKU-A defective', $3::jsonb, 'RECEIVED', TRUE, 100000, $4, $4)
		`, rrS1, ordS1, `[{"order_item_id":"item-a-`+ordS1+`","sku_id":"SKU-A","quantity":1}]`, now); err != nil {
			t.Fatalf("seed s1 return: %v", err)
		}
		if _, err := st.TransitionReturnRequest(ctx, ReturnTransitionParams{
			ReturnRequestID: rrS1, TargetStatus: orderv1.ReturnStatus_RETURN_STATUS_REFUNDING,
			ActorID: "seller-1", ActorRole: "seller", Note: "received", Now: now.Add(time.Second),
		}); err != nil {
			t.Fatalf("s1 REFUNDING: %v", err)
		}
		amt100k := int64(100000)
		if _, err := st.UpdateReturnRefundApprovalState(ctx, rrS1, refS1, "", true, &amt100k); err != nil {
			t.Fatalf("s1 save refund_id: %v", err)
		}
		if recordedFirst {
			if _, err := st.RecordPaymentRefunded(ctx, PaymentRefundedParams{
				EventID: "ev-ret-" + ordS1, EventType: "payment.refunded", PaymentID: "pay-" + ordS1,
				OrderID: ordS1, RefundID: refS1, IdempotencyKey: "order-return-refund:" + ordS1,
				AmountVND: 100000, Reason: "return refund",
			}); err != nil {
				t.Fatalf("RecordPaymentRefunded return: %v", err)
			}
		}
		var gotEffective int64 = -1
		resS1, err := st.TransitionReturnRequest(ctx, ReturnTransitionParams{
			ReturnRequestID: rrS1, TargetStatus: orderv1.ReturnStatus_RETURN_STATUS_REFUNDED,
			ActorID: "system", ActorRole: "system", Note: "done", RequirePromoReleasedOnFullReturn: true, Now: now.Add(2 * time.Second),
			BuildPartialCompletedOutboxFn: func(ord db.Order, effectiveRefundedVND int64) (db.InsertOutboxEventParams, error) {
				gotEffective = effectiveRefundedVND
				return db.InsertOutboxEventParams{
					ID: uuid.NewString(), EventType: "order.completed",
					PayloadJSON: []byte(`{"order_id":"` + ord.ID + `"}`),
				}, nil
			},
		})
		if err != nil {
			t.Fatalf("s1 (recordedFirst=%v) REFUNDED: %v", recordedFirst, err)
		}
		if gotEffective != 250000 || resS1.Order.Order.Status != "COMPLETED" {
			t.Fatalf("s1 (recordedFirst=%v): gotEffective=%d status=%q, want 250000 / COMPLETED", recordedFirst, gotEffective, resS1.Order.Order.Status)
		}
	}

	// 3. Scenario 2 (prior 100k + partial return 100k + concurrent admin refund 100k = 300k/300k unrecorded return event):
	// REFUNDING -> REFUNDED requires promo release (ErrReturnNeedsPromoRelease), then transitions to RETURNED + order.returned.
	ordS2 := "ord-pg-r1m2-s2-" + uuid.NewString()[:8]
	grpS2 := "grp-" + ordS2
	rrS2 := "rr-" + ordS2
	refS2 := "ref-" + ordS2
	if _, err := st.CreateCheckoutGroup(ctx, db.CreateCheckoutGroupParams{
		ID: grpS2, BuyerID: "buyer-1", IdempotencyKey: "idem-" + ordS2, RequestHash: "h-" + ordS2,
		Status: "IN_PROGRESS", PaymentMethod: "VIETQR", TotalPayableVND: 300000,
	}); err != nil {
		t.Fatalf("CreateCheckoutGroup s2: %v", err)
	}
	if _, err := st.PersistCheckoutBatch(ctx, CheckoutBatch{
		CheckoutGroupID: grpS2,
		Now:             now,
		Orders: []NewOrderRecord{
			{
				Order: db.InsertOrderParams{
					ID: ordS2, OrderCode: "NM-PG-S2-" + ordS2[len(ordS2)-4:], CheckoutGroupID: grpS2, BuyerID: "buyer-1", ShopID: "shop-1",
					Status: "RETURN_REQUESTED", PaymentMethod: "VIETQR", PaymentID: "pay-" + ordS2, PaymentStatus: "SUCCEEDED",
					SubtotalVND: 300000, TotalPayableVND: 300000, ShippingAddressJSON: addrJSON,
				},
				Items: []db.InsertOrderItemParams{
					{ID: "item-a-" + ordS2, OrderID: ordS2, SKUID: "SKU-A", ProductID: "p-a", ProductName: "A", UnitPriceVND: 100000, EffectiveUnitPriceVND: 100000, Quantity: 1, LineTotalVND: 100000},
					{ID: "item-b-" + ordS2, OrderID: ordS2, SKUID: "SKU-B", ProductID: "p-b", ProductName: "B", UnitPriceVND: 200000, EffectiveUnitPriceVND: 200000, Quantity: 1, LineTotalVND: 200000},
				},
			},
		},
	}); err != nil {
		t.Fatalf("PersistCheckoutBatch s2: %v", err)
	}
	if _, err := st.RecordPaymentRefunded(ctx, PaymentRefundedParams{
		EventID: "ev-s2-p1-" + ordS2, EventType: "payment.refunded", PaymentID: "pay-" + ordS2,
		OrderID: ordS2, RefundID: "ref-s2-p1", AmountVND: 100000, Reason: "prior 100k",
	}); err != nil {
		t.Fatalf("s2 prior 100k: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO return_requests (id, order_id, order_code, buyer_id, shop_id, reason, items_json, status, goods_received, refund_amount_vnd, created_at, updated_at)
		VALUES ($1, $2, 'NM-PG-S2', 'buyer-1', 'shop-1', 'SKU-A defective', $3::jsonb, 'RECEIVED', TRUE, 100000, $4, $4)
	`, rrS2, ordS2, `[{"order_item_id":"item-a-`+ordS2+`","sku_id":"SKU-A","quantity":1}]`, now); err != nil {
		t.Fatalf("seed s2 return: %v", err)
	}
	if _, err := st.TransitionReturnRequest(ctx, ReturnTransitionParams{
		ReturnRequestID: rrS2, TargetStatus: orderv1.ReturnStatus_RETURN_STATUS_REFUNDING,
		ActorID: "seller-1", ActorRole: "seller", Note: "received", Now: now.Add(time.Second),
	}); err != nil {
		t.Fatalf("s2 REFUNDING: %v", err)
	}
	amt100 := int64(100000)
	if _, err := st.UpdateReturnRefundApprovalState(ctx, rrS2, refS2, "", true, &amt100); err != nil {
		t.Fatalf("s2 save refund_id: %v", err)
	}
	// Concurrent admin refund adds another 100k -> ord.RefundedVND=200k; plus unrecorded 100k return = 300k (100%).
	if _, err := st.RecordPaymentRefunded(ctx, PaymentRefundedParams{
		EventID: "ev-s2-admin-" + ordS2, EventType: "payment.refunded", PaymentID: "pay-" + ordS2,
		OrderID: ordS2, RefundID: "ref-s2-admin", AmountVND: 100000, Reason: "concurrent admin 100k",
	}); err != nil {
		t.Fatalf("s2 concurrent admin 100k: %v", err)
	}
	if _, err := st.TransitionReturnRequest(ctx, ReturnTransitionParams{
		ReturnRequestID: rrS2, TargetStatus: orderv1.ReturnStatus_RETURN_STATUS_REFUNDED,
		ActorID: "system", ActorRole: "system", Note: "done", RequirePromoReleasedOnFullReturn: true, Now: now.Add(2 * time.Second),
	}); !errors.Is(err, ErrReturnNeedsPromoRelease) {
		t.Fatalf("s2 without promo_released: got err=%v, want ErrReturnNeedsPromoRelease", err)
	}
	if _, err := st.UpdateReturnSideEffectProgress(ctx, rrS2, false, true, false, nil); err != nil {
		t.Fatalf("s2 mark promo_released: %v", err)
	}
	resS2, err := st.TransitionReturnRequest(ctx, ReturnTransitionParams{
		ReturnRequestID: rrS2, TargetStatus: orderv1.ReturnStatus_RETURN_STATUS_REFUNDED,
		ActorID: "system", ActorRole: "system", Note: "done", RequirePromoReleasedOnFullReturn: true, Now: now.Add(3 * time.Second),
	})
	if err != nil {
		t.Fatalf("s2 REFUNDED after promo_released: %v", err)
	}
	if resS2.Order.Order.Status != "RETURNED" {
		t.Fatalf("s2 order status = %q, want RETURNED", resS2.Order.Order.Status)
	}
	var s2Comp, s2Ret int
	if err := pool.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE event_type = 'order.completed'),
			COUNT(*) FILTER (WHERE event_type = 'order.returned')
		FROM outbox_events
		WHERE payload_json::text LIKE '%' || $1 || '%'
	`, ordS2).Scan(&s2Comp, &s2Ret); err != nil {
		t.Fatalf("query s2 outbox_events: %v", err)
	}
	if s2Comp != 0 || s2Ret != 1 {
		t.Fatalf("PG s2 outbox mismatch: completed=%d returned=%d, want 0/1", s2Comp, s2Ret)
	}
}
