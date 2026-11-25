package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	"github.com/nexusmart/core/services/order/internal/db"
	"github.com/nexusmart/core/services/order/internal/events"
	"github.com/nexusmart/core/services/order/internal/statemachine"
	"github.com/nexusmart/core/services/order/migrations"
)

// TestPostgresW3B2_Migration0016_UpgradeWithExistingData proves migration 0016 upgrades cleanly
// on a real PostgreSQL database that already contains COD and VIETQR data from 0001..0015,
// enforces payment_method CHECK constraints, adds checkout_groups.pay_url, and rebuilds
// idx_orders_pending_expiry to cover ('VIETQR', 'ZALOPAY').
func TestPostgresW3B2_Migration0016_UpgradeWithExistingData(t *testing.T) {
	adminDSN := strings.TrimSpace(os.Getenv("TEST_ORDER_DB_URL"))
	if adminDSN == "" {
		t.Skip("TEST_ORDER_DB_URL not set; skipping real Postgres migration 0016 test")
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
	tempDBName := "order_m16_test_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
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

	// 1. Apply migrations 0001..0018 ONLY (simulating pre-3b-2 database before 0019_online_methods.sql).
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
	var pre19 []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") && e.Name() < "0019_" {
			pre19 = append(pre19, e.Name())
		}
	}
	sort.Strings(pre19)
	for _, name := range pre19 {
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

	// 2. Seed existing COD and VIETQR rows before migration 0016 runs.
	past := time.Now().UTC().Add(-5 * time.Minute)
	if _, err := pool.Exec(ctx, `
		INSERT INTO checkout_groups (id, buyer_id, idempotency_key, request_hash, status, payment_method, payment_id, payment_status, payment_expires_at, total_payable_vnd)
		VALUES
		    ('grp-pre-cod', 'buyer-1', 'idem-pre-cod', 'h1', 'COMPLETED', 'COD', 'pay-pre-cod', 'PENDING', NULL, 200000),
		    ('grp-pre-vqr', 'buyer-1', 'idem-pre-vqr', 'h2', 'COMPLETED', 'VIETQR', 'pay-pre-vqr', 'PENDING', $1, 300000)
	`, past); err != nil {
		t.Fatalf("seed pre-0016 checkout_groups: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO orders (id, order_code, checkout_group_id, buyer_id, shop_id, status, payment_method, payment_id, payment_status, subtotal_vnd, total_payable_vnd, shipping_address_json, payment_expires_at)
		VALUES
		    ('ord-pre-cod', 'NM-PRE-COD', 'grp-pre-cod', 'buyer-1', 'shop-1', 'CONFIRMED', 'COD', 'pay-pre-cod', 'PENDING', 200000, 200000, '{}'::jsonb, NULL),
		    ('ord-pre-vqr', 'NM-PRE-VQR', 'grp-pre-vqr', 'buyer-1', 'shop-1', 'PENDING', 'VIETQR', 'pay-pre-vqr', 'PENDING', 300000, 300000, '{}'::jsonb, $1)
	`, past); err != nil {
		t.Fatalf("seed pre-0016 orders: %v", err)
	}

	// 3. Apply remaining migrations (0016_online_methods.sql) on top of live data!
	if err := db.ApplyMigrations(ctx, pool); err != nil {
		t.Fatalf("ApplyMigrations(0016 on DB with data): %v", err)
	}

	// 4. Verify pay_url column exists with NOT NULL DEFAULT '' and existing rows received ''.
	var colDefault, isNullable string
	if err := pool.QueryRow(ctx, `
		SELECT column_default, is_nullable
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'checkout_groups' AND column_name = 'pay_url'
	`).Scan(&colDefault, &isNullable); err != nil {
		t.Fatalf("checkout_groups.pay_url metadata missing: %v", err)
	}
	if isNullable != "NO" || !strings.Contains(colDefault, "''") {
		t.Fatalf("unexpected pay_url column metadata: nullable=%q default=%q", isNullable, colDefault)
	}

	st := NewPostgresStore(pool)
	grpVQR, err := st.GetCheckoutGroupByID(ctx, "grp-pre-vqr")
	if err != nil || grpVQR.PayURL != "" {
		t.Fatalf("existing checkout_group pay_url = %q err=%v, want ''", grpVQR.PayURL, err)
	}

	// 5. ZALOPAY inserts succeed; invalid method ('CRYPTO') fails CHECK on both checkout_groups and orders!
	if _, err := st.CreateCheckoutGroup(ctx, db.CreateCheckoutGroupParams{
		ID: "grp-zlp-1", BuyerID: "buyer-1", IdempotencyKey: "idem-zlp-1", RequestHash: "hz1",
		Status: "IN_PROGRESS", PaymentMethod: statemachine.PaymentMethodZALOPAY, TotalPayableVND: 400000,
	}); err != nil {
		t.Fatalf("insert ZALOPAY checkout_group failed: %v", err)
	}
	if _, err := st.PersistCheckoutBatch(ctx, CheckoutBatch{
		CheckoutGroupID: "grp-zlp-1",
		Now:             time.Now().UTC(),
		Orders: []NewOrderRecord{{
			Order: db.InsertOrderParams{
				ID: "ord-zlp-1", OrderCode: "NM-ZLP-1", CheckoutGroupID: "grp-zlp-1", BuyerID: "buyer-1", ShopID: "shop-1",
				Status: "PENDING", PaymentMethod: statemachine.PaymentMethodZALOPAY, PaymentStatus: "PENDING",
				SubtotalVND: 400000, TotalPayableVND: 400000, ShippingAddressJSON: []byte(`{}`), PaymentExpiresAt: &past,
			},
		}},
	}); err != nil {
		t.Fatalf("insert ZALOPAY order failed: %v", err)
	}
	if err := st.CompleteCheckoutWithPayment(ctx, UpdateCheckoutPaymentParams{
		CheckoutGroupID: "grp-zlp-1", PaymentID: "pay-zlp-1", PaymentStatus: "PENDING",
		PayURL: "https://sb-openapi.zalopay.vn/v2/pay/1", PaymentExpiresAt: &past,
	}); err != nil {
		t.Fatalf("CompleteCheckoutWithPayment(ZALOPAY pay_url): %v", err)
	}
	grpZLP, err := st.GetCheckoutGroupByID(ctx, "grp-zlp-1")
	if err != nil || grpZLP.PayURL != "https://sb-openapi.zalopay.vn/v2/pay/1" {
		t.Fatalf("ZALOPAY pay_url not persisted: grp=%+v err=%v", grpZLP, err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO checkout_groups (id, buyer_id, idempotency_key, request_hash, status, payment_method, total_payable_vnd)
		VALUES ('grp-bad-method', 'buyer-1', 'idem-bad', 'hb', 'IN_PROGRESS', 'CRYPTO', 100000)
	`); err == nil {
		t.Fatalf("CHECK constraint chk_checkout_groups_payment_method failed to reject 'CRYPTO'")
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO orders (id, order_code, checkout_group_id, buyer_id, shop_id, status, payment_method, subtotal_vnd, total_payable_vnd, shipping_address_json)
		VALUES ('ord-bad-method', 'NM-BAD-1', 'grp-zlp-1', 'buyer-1', 'shop-1', 'PENDING', 'CRYPTO', 100000, 100000, '{}'::jsonb)
	`); err == nil {
		t.Fatalf("CHECK constraint chk_orders_payment_method failed to reject 'CRYPTO'")
	}

	// 6. Verify idx_orders_pending_expiry covers ('VIETQR','ZALOPAY'), planner uses it, and FindExpiredPendingGroups finds BOTH!
	var idxDef string
	if err := pool.QueryRow(ctx, `
		SELECT indexdef FROM pg_indexes
		WHERE schemaname = 'public' AND tablename = 'orders' AND indexname = 'idx_orders_pending_expiry'
	`).Scan(&idxDef); err != nil {
		t.Fatalf("idx_orders_pending_expiry missing: %v", err)
	}
	if !strings.Contains(idxDef, "VIETQR") || !strings.Contains(idxDef, "ZALOPAY") {
		t.Fatalf("idx_orders_pending_expiry missing VIETQR/ZALOPAY: %s", idxDef)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin explain tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL enable_seqscan = off`); err != nil {
		t.Fatalf("disable seqscan: %v", err)
	}
	rows, err := tx.Query(ctx, `
		EXPLAIN (FORMAT TEXT)
		SELECT id, checkout_group_id, payment_id
		FROM orders
		WHERE status = 'PENDING'
		  AND payment_method IN ('VIETQR', 'ZALOPAY')
		  AND payment_expires_at IS NOT NULL
		  AND payment_expires_at <= NOW()
		LIMIT 20
	`)
	if err != nil {
		t.Fatalf("EXPLAIN FindExpiredPendingOrders: %v", err)
	}
	var planLines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("scan plan line: %v", err)
		}
		planLines = append(planLines, line)
	}
	rows.Close()
	if !strings.Contains(strings.Join(planLines, "\n"), "idx_orders_pending_expiry") {
		t.Fatalf("expected planner to use idx_orders_pending_expiry, got:\n%s", strings.Join(planLines, "\n"))
	}

	expired, err := st.FindExpiredPendingGroups(ctx, 20)
	if err != nil {
		t.Fatalf("FindExpiredPendingGroups: %v", err)
	}
	found := map[string]bool{}
	for _, c := range expired {
		found[c.CheckoutGroupID] = true
	}
	if !found["grp-pre-vqr"] || !found["grp-zlp-1"] || found["grp-pre-cod"] {
		t.Fatalf("FindExpiredPendingGroups = %+v, want grp-pre-vqr and grp-zlp-1 (not COD)", expired)
	}
}

// TestPostgresW3B2_UpdateGroupPaymentOnRetry_OnlineAndCODSwitchAndRace tests F5 store transactions on real Postgres:
//  1. VIETQR -> ZALOPAY updates payment_method, pay_url, fees, keeps total_payable_vnd & original expiry.
//  2. ZALOPAY -> COD atomically confirms sub-orders, clears expiry, sets pending_stock_confirm=TRUE, emits order.confirmed.
//  3. Concurrent UpdateGroupPaymentOnRetry(COD) vs TransitionOrdersPaymentResult(EXPIRED) yields strictly one winner.
func TestPostgresW3B2_UpdateGroupPaymentOnRetry_OnlineAndCODSwitchAndRace(t *testing.T) {
	st, pool := openTempPostgresStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	origExp := now.Add(15 * time.Minute)

	// 1. VIETQR -> ZALOPAY -> COD sequence on real Postgres.
	grpID := "grp-f5-seq-" + uuid.NewString()[:8]
	ordID := "ord-f5-seq-" + uuid.NewString()[:8]
	seedPGCheckoutGroupWithOrder(t, st, grpID, "buyer-f5", "shop-1", "idem-f5-seq", db.InsertOrderParams{
		ID: ordID, OrderCode: "NM-F5-SEQ", Status: "PENDING", PaymentMethod: "VIETQR",
		PaymentStatus: "PENDING", SubtotalVND: 1000000, ShippingFeeVND: 30000,
		CommissionVND: 50000, PaymentFeeVND: 10300, PaymentFeeBPS: 100, TotalPayableVND: 1030000,
		PaymentExpiresAt: &origExp,
	}, now)
	if err := st.CompleteCheckoutWithPayment(ctx, UpdateCheckoutPaymentParams{
		CheckoutGroupID: grpID, PaymentID: "pay-1", PaymentStatus: "PENDING",
		QRURL: "https://qr/1", QRCode: "QR1", PaymentExpiresAt: &origExp,
	}); err != nil {
		t.Fatalf("CompleteCheckoutWithPayment: %v", err)
	}

	// Switch VIETQR -> ZALOPAY (recalculating fee to 200 bps = 20,600 VND; keeping origExp & total 1,030,000).
	if err := st.UpdateGroupPaymentOnRetry(ctx, RetryPaymentUpdateParams{
		CheckoutGroupID:     grpID,
		ExpectedPaymentID:   "pay-1",
		TargetMethod:        "ZALOPAY",
		NewPaymentID:        "pay-2-zlp",
		PaymentStatus:       "PENDING",
		PayURL:              "https://sb-openapi.zalopay.vn/v2/pay/2",
		PaymentExpiresAt:    &origExp,
		OrderFees:           []RetryOrderFeeSpec{{OrderID: ordID, PaymentFeeVND: 20600, PaymentFeeBPS: 200}},
		ActorID:             "buyer-f5",
		ActorRole:           "buyer",
		RetryIdempotencyKey: "rk-zlp",
		Now:                 now.Add(2 * time.Minute),
	}); err != nil {
		t.Fatalf("UpdateGroupPaymentOnRetry(VIETQR->ZALOPAY): %v", err)
	}

	grp1, _ := st.GetCheckoutGroupByID(ctx, grpID)
	agg1, _ := st.GetOrderWithDetails(ctx, ordID)
	if grp1.PaymentMethod != "ZALOPAY" || grp1.PayURL != "https://sb-openapi.zalopay.vn/v2/pay/2" || grp1.PaymentID != "pay-2-zlp" {
		t.Fatalf("group after VIETQR->ZALOPAY mismatch: %+v", grp1)
	}
	if agg1.Order.PaymentMethod != "ZALOPAY" || agg1.Order.PaymentFeeBPS != 200 || agg1.Order.PaymentFeeVND != 20600 ||
		agg1.Order.TotalPayableVND != 1030000 || agg1.Order.PaymentExpiresAt == nil || !agg1.Order.PaymentExpiresAt.Equal(origExp) {
		t.Fatalf("order after VIETQR->ZALOPAY mismatch: %+v", agg1.Order)
	}

	// Switch ZALOPAY -> COD (recalculating fee to 50 bps = 5,150 VND; confirming order, clearing expiry, setting pending_stock_confirm).
	if err := st.UpdateGroupPaymentOnRetry(ctx, RetryPaymentUpdateParams{
		CheckoutGroupID:     grpID,
		ExpectedPaymentID:   "pay-2-zlp",
		TargetMethod:        "COD",
		NewPaymentID:        "pay-3-cod",
		PaymentStatus:       "PENDING",
		SwitchToCOD:         true,
		OrderFees:           []RetryOrderFeeSpec{{OrderID: ordID, PaymentFeeVND: 5150, PaymentFeeBPS: 50}},
		ActorID:             "buyer-f5",
		ActorRole:           "buyer",
		RetryIdempotencyKey: "rk-cod",
		Now:                 now.Add(4 * time.Minute),
	}); err != nil {
		t.Fatalf("UpdateGroupPaymentOnRetry(ZALOPAY->COD): %v", err)
	}

	// Idempotent replay of the exact same COD switch succeeds cleanly!
	if err := st.UpdateGroupPaymentOnRetry(ctx, RetryPaymentUpdateParams{
		CheckoutGroupID:     grpID,
		ExpectedPaymentID:   "pay-2-zlp",
		TargetMethod:        "COD",
		NewPaymentID:        "pay-3-cod",
		PaymentStatus:       "PENDING",
		SwitchToCOD:         true,
		OrderFees:           []RetryOrderFeeSpec{{OrderID: ordID, PaymentFeeVND: 5150, PaymentFeeBPS: 50}},
		RetryIdempotencyKey: "rk-cod",
		Now:                 now.Add(4 * time.Minute),
	}); err != nil {
		t.Fatalf("idempotent replay of UpdateGroupPaymentOnRetry(COD) failed: %v", err)
	}

	grp2, _ := st.GetCheckoutGroupByID(ctx, grpID)
	agg2, _ := st.GetOrderWithDetails(ctx, ordID)
	if grp2.PaymentMethod != "COD" || grp2.PayURL != "" || grp2.PaymentExpiresAt != nil || !grp2.PendingStockConfirm {
		t.Fatalf("group after ZALOPAY->COD mismatch: %+v", grp2)
	}
	if agg2.Order.Status != "CONFIRMED" || agg2.Order.PaymentMethod != "COD" || agg2.Order.PaymentExpiresAt != nil ||
		agg2.Order.PaymentFeeBPS != 50 || agg2.Order.PaymentFeeVND != 5150 || agg2.Order.TotalPayableVND != 1030000 {
		t.Fatalf("order after ZALOPAY->COD mismatch: %+v", agg2.Order)
	}
	var payloadRaw []byte
	if err := pool.QueryRow(ctx, `
		SELECT payload_json FROM outbox_events
		WHERE event_type = $1 AND payload_json->>'order_id' = $2
		ORDER BY seq DESC LIMIT 1
	`, events.SubjectOrderConfirmed, ordID).Scan(&payloadRaw); err != nil {
		t.Fatalf("expected order.confirmed outbox event after COD switch: %v", err)
	}
	var confPayload events.OrderConfirmedPayload
	if err := json.Unmarshal(payloadRaw, &confPayload); err != nil || confPayload.PaymentMethod != "COD" || confPayload.TotalPayableVND != 1030000 {
		t.Fatalf("unexpected order.confirmed outbox payload: %s", string(payloadRaw))
	}

	// 2. Concurrent store race: UpdateGroupPaymentOnRetry(COD) vs TransitionOrdersPaymentResult(EXPIRED) under FOR UPDATE.
	for i := 0; i < 6; i++ {
		rGrpID := fmt.Sprintf("grp-f5-pg-race-%d-%s", i, uuid.NewString()[:6])
		rOrdID := fmt.Sprintf("ord-f5-pg-race-%d-%s", i, uuid.NewString()[:6])
		rExp := now.Add(10 * time.Minute)
		seedPGCheckoutGroupWithOrder(t, st, rGrpID, "buyer-race", "shop-1", "idem-"+rGrpID, db.InsertOrderParams{
			ID: rOrdID, OrderCode: fmt.Sprintf("NM-RACE-%d-%s", i, uuid.NewString()[:4]), Status: "PENDING",
			PaymentMethod: "ZALOPAY", PaymentStatus: "PENDING", SubtotalVND: 500000, TotalPayableVND: 500000,
			PaymentExpiresAt: &rExp,
		}, now)
		if err := st.CompleteCheckoutWithPayment(ctx, UpdateCheckoutPaymentParams{
			CheckoutGroupID: rGrpID, PaymentID: "pay-orig-" + rGrpID, PaymentStatus: "PENDING", PaymentExpiresAt: &rExp,
		}); err != nil {
			t.Fatalf("iter %d CompleteCheckoutWithPayment: %v", i, err)
		}

		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		var switchErr error
		var expOut *PaymentResultTransitionOutcome
		var expErr error
		go func() {
			defer wg.Done()
			<-start
			switchErr = st.UpdateGroupPaymentOnRetry(ctx, RetryPaymentUpdateParams{
				CheckoutGroupID:     rGrpID,
				ExpectedPaymentID:   "pay-orig-" + rGrpID,
				TargetMethod:        "COD",
				NewPaymentID:        "pay-cod-" + rGrpID,
				PaymentStatus:       "PENDING",
				SwitchToCOD:         true,
				OrderFees:           []RetryOrderFeeSpec{{OrderID: rOrdID, PaymentFeeVND: 2500, PaymentFeeBPS: 50}},
				RetryIdempotencyKey: "rk-race-" + rGrpID,
				Now:                 now.Add(time.Minute),
			})
		}()
		go func() {
			defer wg.Done()
			<-start
			expOut, expErr = st.TransitionOrdersPaymentResult(ctx, PaymentResultTransitionParams{
				CheckoutGroupID: rGrpID,
				PaymentID:       "pay-orig-" + rGrpID,
				ResultStatus:    orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_EXPIRED,
				Reason:          "Payment expired",
				ActorID:         "watchdog",
				ActorRole:       "system",
				Now:             now.Add(time.Minute),
			})
		}()
		close(start)
		wg.Wait()

		if expErr != nil {
			t.Fatalf("iter %d TransitionOrdersPaymentResult err: %v", i, expErr)
		}
		finalAgg, err := st.GetOrderWithDetails(ctx, rOrdID)
		if err != nil {
			t.Fatalf("iter %d GetOrderWithDetails: %v", i, err)
		}
		switch finalAgg.Order.Status {
		case "CONFIRMED":
			if switchErr != nil || finalAgg.Order.PaymentMethod != "COD" || len(expOut.Transitioned) != 0 {
				t.Fatalf("iter %d CONFIRMED inconsistent: switchErr=%v expOut=%+v ord=%+v", i, switchErr, expOut, finalAgg.Order)
			}
		case "CANCELLED":
			if !errors.Is(switchErr, ErrRetryPrecondition) || len(expOut.Transitioned) != 1 {
				t.Fatalf("iter %d CANCELLED inconsistent: switchErr=%v expOut=%+v ord=%+v", i, switchErr, expOut, finalAgg.Order)
			}
		default:
			t.Fatalf("iter %d unexpected final status %q", i, finalAgg.Order.Status)
		}
	}
}

// TestPostgresW3B2P2_Migration0017_ReturnRefundApprovalAndWatchdogSkip verifies migration 0017
// and all F11 return refund approval store transitions on real PostgreSQL:
//  1. CHECK constraint rejects invalid refund_approval_status values.
//  2. AdminResolveReturn(REFUND) persists require_approval_if_large=true + refund_requested_by.
//  3. PENDING_APPROVAL excludes the return from FindRefundingReturnRequests (watchdog skip).
//  4. REFUNDING -> ESCALATED reverts orders.status RETURNED -> RETURN_REQUESTED, increments refund_attempt,
//     and refuses re-escalation once refund_done=true.
//  5. APPROVED includes the return in FindRefundingReturnRequests so watchdog settles REFUNDING -> REFUNDED.
func TestPostgresW3B2P2_Migration0017_ReturnRefundApprovalAndWatchdogSkip(t *testing.T) {
	st, pool := openTempPostgresStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)

	grpID := "grp-m17-" + uuid.NewString()[:8]
	ordID := "ord-m17-" + uuid.NewString()[:8]
	retID := "ret-m17-" + uuid.NewString()[:8]
	seedPGCheckoutGroupWithOrder(t, st, grpID, "buyer-m17", "shop-1", "idem-m17", db.InsertOrderParams{
		ID: ordID, OrderCode: "NM-M17-1", Status: "DELIVERED", PaymentMethod: "VIETQR",
		PaymentStatus: "SUCCEEDED", SubtotalVND: 2000000, ShippingFeeVND: 30000, TotalPayableVND: 2030000,
	}, now)
	if err := st.CompleteCheckoutWithPayment(ctx, UpdateCheckoutPaymentParams{
		CheckoutGroupID: grpID, PaymentID: "pay-m17-1", PaymentStatus: "SUCCEEDED",
	}); err != nil {
		t.Fatalf("CompleteCheckoutWithPayment: %v", err)
	}

	dl := now.Add(72 * time.Hour)
	if _, _, err := st.TransitionOrderReturnRequested(ctx, ReturnRequestParams{
		OrderID: ordID, OrderCode: "NM-M17-1", BuyerID: "buyer-m17", ShopID: "shop-1",
		ReturnRequestID: retID, Reason: "damaged", EvidenceURLsJSON: []byte("[]"),
		RefundAmountVND: 2030000, DeadlineAt: &dl, ActorID: "buyer-m17", ActorRole: "buyer",
		Note: "return requested", Now: now,
		OutboxEvent: db.InsertOutboxEventParams{
			ID: uuid.NewString(), EventType: events.SubjectOrderReturnUpdated, PayloadJSON: []byte(`{"order_id":"` + ordID + `"}`),
		},
	}); err != nil {
		t.Fatalf("TransitionOrderReturnRequested: %v", err)
	}
	// PENDING -> REJECTED -> ESCALATED.
	if _, err := st.TransitionReturnRequest(ctx, ReturnTransitionParams{
		ReturnRequestID: retID, ExpectedStatus: orderv1.ReturnStatus_RETURN_STATUS_PENDING,
		TargetStatus: orderv1.ReturnStatus_RETURN_STATUS_REJECTED, ActorID: "seller-1", ActorRole: "seller",
		Note: "reject", UpdateDeadline: true, DeadlineAt: &dl, Now: now,
	}); err != nil {
		t.Fatalf("PENDING->REJECTED: %v", err)
	}
	if _, err := st.TransitionReturnRequest(ctx, ReturnTransitionParams{
		ReturnRequestID: retID, ExpectedStatus: orderv1.ReturnStatus_RETURN_STATUS_REJECTED,
		TargetStatus: orderv1.ReturnStatus_RETURN_STATUS_ESCALATED, ActorID: "buyer-m17", ActorRole: "buyer",
		Note: "dispute", UpdateDeadline: true, Now: now,
	}); err != nil {
		t.Fatalf("REJECTED->ESCALATED: %v", err)
	}

	// Verify CHECK constraint rejects bogus refund_approval_status.
	if _, err := pool.Exec(ctx, `UPDATE return_requests SET refund_approval_status = 'BOGUS' WHERE id = $1`, retID); err == nil {
		t.Fatalf("CHECK constraint chk_return_requests_refund_approval_status failed to reject 'BOGUS'")
	}

	// 1. AdminResolveReturn(REFUND) -> ESCALATED -> REFUNDING with maker persisted.
	reqApproval := true
	maker := "cskh-maker-pg"
	empty := ""
	res1, err := st.TransitionReturnRequest(ctx, ReturnTransitionParams{
		ReturnRequestID: retID, ExpectedStatus: orderv1.ReturnStatus_RETURN_STATUS_ESCALATED,
		TargetStatus: orderv1.ReturnStatus_RETURN_STATUS_REFUNDING, ActorID: maker, ActorRole: "cskh",
		Note: "admin refund", RequireApprovalIfLarge: &reqApproval, RefundRequestedBy: &maker,
		RefundID: &empty, RefundApprovalStatus: &empty, UpdateDeadline: true, Now: now,
	})
	if err != nil {
		t.Fatalf("ESCALATED->REFUNDING: %v", err)
	}
	if !res1.Return.Request.RequireApprovalIfLarge || res1.Return.Request.RefundRequestedBy != maker || res1.Order.Order.Status != "RETURNED" {
		t.Fatalf("REFUNDING persistence mismatch: ret=%+v ord=%+v", res1.Return.Request, res1.Order.Order)
	}

	// Before PENDING_APPROVAL is set, FindRefundingReturnRequests INCLUDES this return (so transient CreateRefund error retries).
	idsBefore, err := st.FindRefundingReturnRequests(ctx, 20)
	if err != nil || len(idsBefore) != 1 || idsBefore[0] != retID {
		t.Fatalf("FindRefundingReturnRequests(before PENDING_APPROVAL) = %v err=%v, want [%s]", idsBefore, err, retID)
	}

	// 2. Record PENDING_APPROVAL -> FindRefundingReturnRequests MUST EXCLUDE it!
	amt := int64(2030000)
	updPending, err := st.UpdateReturnRefundApprovalState(ctx, retID, "ref-pg-1", "PENDING_APPROVAL", false, &amt)
	if err != nil || updPending.RefundApprovalStatus != "PENDING_APPROVAL" || updPending.RefundID != "ref-pg-1" || updPending.RefundDone {
		t.Fatalf("UpdateReturnRefundApprovalState(PENDING_APPROVAL): row=%+v err=%v", updPending, err)
	}
	idsPending, err := st.FindRefundingReturnRequests(ctx, 20)
	if err != nil || len(idsPending) != 0 {
		t.Fatalf("FindRefundingReturnRequests MUST skip PENDING_APPROVAL, got %v err=%v", idsPending, err)
	}

	// 3. Rejection: REFUNDING -> ESCALATED reverts order RETURNED -> RETURN_REQUESTED and increments refund_attempt!
	rejected := "REJECTED"
	refID := "ref-pg-1"
	resEsc, err := st.TransitionReturnRequest(ctx, ReturnTransitionParams{
		ReturnRequestID: retID, ExpectedStatus: orderv1.ReturnStatus_RETURN_STATUS_REFUNDING,
		TargetStatus: orderv1.ReturnStatus_RETURN_STATUS_ESCALATED, ActorID: "checker-1", ActorRole: "system",
		Note: "checker rejected", RefundID: &refID, RefundApprovalStatus: &rejected,
		IncrementRefundAttempt: true, UpdateDeadline: true, Now: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("REFUNDING->ESCALATED: %v", err)
	}
	if resEsc.Return.Request.Status != "ESCALATED" || resEsc.Return.Request.RefundAttempt != 1 ||
		resEsc.Return.Request.RefundApprovalStatus != "REJECTED" || resEsc.Order.Order.Status != "RETURN_REQUESTED" {
		t.Fatalf("after REFUNDING->ESCALATED mismatch: ret=%+v ord=%+v", resEsc.Return.Request, resEsc.Order.Order)
	}
	// Stale PENDING_APPROVAL write after out-of-order REFUNDING->ESCALATED MUST be rejected (never clobber REJECTED).
	if _, err := st.UpdateReturnRefundApprovalState(ctx, retID, "ref-pg-1", "PENDING_APPROVAL", false, &amt); !errors.Is(err, statemachine.ErrInvalidReturnTransition) {
		t.Fatalf("stale UpdateReturnRefundApprovalState on ESCALATED return must fail with ErrInvalidReturnTransition, got %v", err)
	}

	// 4. Second REFUND attempt -> APPROVED -> FindRefundingReturnRequests INCLUDES it, and REFUNDING->ESCALATED is blocked once refund_done=true!
	maker2 := "admin-maker-pg"
	if _, err := st.TransitionReturnRequest(ctx, ReturnTransitionParams{
		ReturnRequestID: retID, ExpectedStatus: orderv1.ReturnStatus_RETURN_STATUS_ESCALATED,
		TargetStatus: orderv1.ReturnStatus_RETURN_STATUS_REFUNDING, ActorID: maker2, ActorRole: "admin",
		Note: "retry admin refund", RequireApprovalIfLarge: &reqApproval, RefundRequestedBy: &maker2,
		RefundID: &empty, RefundApprovalStatus: &empty, UpdateDeadline: true, Now: now.Add(2 * time.Minute),
	}); err != nil {
		t.Fatalf("second ESCALATED->REFUNDING: %v", err)
	}
	updApproved, err := st.UpdateReturnRefundApprovalState(ctx, retID, "ref-pg-2", "APPROVED", true, &amt)
	if err != nil || !updApproved.RefundDone || updApproved.RefundApprovalStatus != "APPROVED" {
		t.Fatalf("UpdateReturnRefundApprovalState(APPROVED): row=%+v err=%v", updApproved, err)
	}
	idsApproved, err := st.FindRefundingReturnRequests(ctx, 20)
	if err != nil || len(idsApproved) != 1 || idsApproved[0] != retID {
		t.Fatalf("FindRefundingReturnRequests(APPROVED) = %v err=%v, want [%s]", idsApproved, err, retID)
	}
	if _, err := st.TransitionReturnRequest(ctx, ReturnTransitionParams{
		ReturnRequestID: retID, ExpectedStatus: orderv1.ReturnStatus_RETURN_STATUS_REFUNDING,
		TargetStatus: orderv1.ReturnStatus_RETURN_STATUS_ESCALATED, ActorID: "checker-1", ActorRole: "system",
		Note: "must fail after refund_done", Now: now.Add(3 * time.Minute),
	}); !errors.Is(err, statemachine.ErrInvalidReturnTransition) {
		t.Fatalf("REFUNDING->ESCALATED after refund_done=true must fail, got %v", err)
	}
}

// TestPostgresW3B2P2_F5_SupersededPaidOrdersRace verifies on real PostgreSQL that racing
// UpdateGroupPaymentOnRetry(COD) vs TransitionOrdersPaymentResult(oldPay, SUCCEEDED) NEVER leaves
// an order CONFIRMED COD without reporting SupersededPaidOrders for automatic refund.
func TestPostgresW3B2P2_F5_SupersededPaidOrdersRace(t *testing.T) {
	st, _ := openTempPostgresStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)

	for i := 0; i < 8; i++ {
		grpID := fmt.Sprintf("grp-sup-race-%d-%s", i, uuid.NewString()[:6])
		ordID := fmt.Sprintf("ord-sup-race-%d-%s", i, uuid.NewString()[:6])
		oldPay := "pay-old-" + grpID
		exp := now.Add(15 * time.Minute)
		seedPGCheckoutGroupWithOrder(t, st, grpID, "buyer-sup", "shop-1", "idem-"+grpID, db.InsertOrderParams{
			ID: ordID, OrderCode: fmt.Sprintf("NM-SUP-%d-%s", i, uuid.NewString()[:4]),
			Status: "PENDING", PaymentMethod: "VIETQR", PaymentStatus: "PENDING",
			SubtotalVND: 500000, TotalPayableVND: 500000, PaymentExpiresAt: &exp,
		}, now)
		if err := st.CompleteCheckoutWithPayment(ctx, UpdateCheckoutPaymentParams{
			CheckoutGroupID: grpID, PaymentID: oldPay, PaymentStatus: "PENDING", PaymentExpiresAt: &exp,
		}); err != nil {
			t.Fatalf("iter %d CompleteCheckoutWithPayment: %v", i, err)
		}

		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		var switchErr error
		var payOut *PaymentResultTransitionOutcome
		var payErr error
		go func() {
			defer wg.Done()
			<-start
			switchErr = st.UpdateGroupPaymentOnRetry(ctx, RetryPaymentUpdateParams{
				CheckoutGroupID:     grpID,
				ExpectedPaymentID:   oldPay,
				TargetMethod:        "COD",
				NewPaymentID:        "pay-cod-" + grpID,
				PaymentStatus:       "PENDING",
				SwitchToCOD:         true,
				OrderFees:           []RetryOrderFeeSpec{{OrderID: ordID, PaymentFeeVND: 2500, PaymentFeeBPS: 50}},
				RetryIdempotencyKey: "rk-sup-" + grpID,
				Now:                 now.Add(time.Minute),
			})
		}()
		go func() {
			defer wg.Done()
			<-start
			payOut, payErr = st.TransitionOrdersPaymentResult(ctx, PaymentResultTransitionParams{
				CheckoutGroupID: grpID,
				PaymentID:       oldPay,
				ResultStatus:    orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_SUCCEEDED,
				Reason:          "late IPN",
				ActorID:         "payment-service",
				ActorRole:       "system",
				Now:             now.Add(time.Minute),
			})
		}()
		close(start)
		wg.Wait()

		if payErr != nil {
			t.Fatalf("iter %d TransitionOrdersPaymentResult err: %v", i, payErr)
		}
		finalAgg, err := st.GetOrderWithDetails(ctx, ordID)
		if err != nil {
			t.Fatalf("iter %d GetOrderWithDetails: %v", i, err)
		}
		if finalAgg.Order.Status != "CONFIRMED" {
			t.Fatalf("iter %d expected CONFIRMED, got %s", i, finalAgg.Order.Status)
		}
		if finalAgg.Order.PaymentMethod == "COD" {
			if switchErr != nil || len(payOut.SupersededPaidOrders) != 1 || payOut.SupersededPaidOrders[0].ID != ordID {
				t.Fatalf("iter %d COD won but SupersededPaidOrders missing: switchErr=%v payOut=%+v", i, switchErr, payOut)
			}
		} else {
			if finalAgg.Order.PaymentMethod != "VIETQR" || finalAgg.Order.PaymentStatus != "SUCCEEDED" || !errors.Is(switchErr, ErrRetryPrecondition) {
				t.Fatalf("iter %d VIETQR IPN won but state inconsistent: switchErr=%v ord=%+v", i, switchErr, finalAgg.Order)
			}
		}
	}
}

// TestPostgresW3B2P4_D_Migration0017_CheckConstraintsAndLegacy3b1Data verifies Item D (#3):
//   - Existing 3b-1 return_requests rows (default refund_approval_status=”, including REFUNDED + refund_done=TRUE)
//     upgrade cleanly through migration 0017.
//   - CHECK constraints reject refund_done=TRUE or status='REFUNDED' when refund_approval_status IN ('PENDING_APPROVAL','REJECTED').
//   - Valid APPROVED -> refund_done=TRUE -> status='REFUNDED' succeeds.
func TestPostgresW3B2P4_D_Migration0017_CheckConstraintsAndLegacy3b1Data(t *testing.T) {
	adminDSN := strings.TrimSpace(os.Getenv("TEST_ORDER_DB_URL"))
	if adminDSN == "" {
		t.Skip("TEST_ORDER_DB_URL not set; skipping real Postgres migration 0017 CHECK test")
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
	tempDBName := "order_m17_test_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
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
	var pre20 []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") && e.Name() < "0020_" {
			pre20 = append(pre20, e.Name())
		}
	}
	sort.Strings(pre20)
	for _, name := range pre20 {
		sqlBytes, err := migrations.Files.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if _, err := pool.Exec(ctx, string(sqlBytes)); err != nil {
			t.Fatalf("exec %s: %v", name, err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO schema_migrations (filename) VALUES ($1)`, name); err != nil {
			t.Fatalf("record %s: %v", name, err)
		}
	}

	// Seed pre-0017 (3b-1) orders + return_requests including legacy REFUNDED/refund_done=TRUE row.
	grpID := "grp-m17-legacy"
	ordLegacyDone := "ord-m17-legacy-done"
	ordNewTest := "ord-m17-new"
	if _, err := pool.Exec(ctx, `
		INSERT INTO checkout_groups (id, buyer_id, idempotency_key, request_hash, status, payment_method, payment_status, total_payable_vnd)
		VALUES ($1, 'buyer-1', 'idem-m17', 'hm17', 'COMPLETED', 'VIETQR', 'SUCCEEDED', 500000)
	`, grpID); err != nil {
		t.Fatalf("seed checkout_group: %v", err)
	}
	for i, oid := range []string{ordLegacyDone, ordNewTest} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO orders (
				id, order_code, checkout_group_id, buyer_id, shop_id, status,
				payment_method, payment_status, subtotal_vnd, total_payable_vnd, shipping_address_json
			) VALUES ($1, $2, $3, 'buyer-1', 'shop-1', 'RETURNED', 'VIETQR', 'SUCCEEDED', 250000, 250000, '{}'::jsonb)
		`, oid, fmt.Sprintf("NM-M17-%d", i), grpID); err != nil {
			t.Fatalf("seed order %s: %v", oid, err)
		}
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO return_requests (id, order_id, order_code, buyer_id, shop_id, reason, status, refund_amount_vnd, refund_done, promo_released)
		VALUES ('ret-m17-legacy', $1, 'NM-M17-0', 'buyer-1', 'shop-1', 'broken', 'REFUNDED', 250000, TRUE, TRUE)
	`, ordLegacyDone); err != nil {
		t.Fatalf("seed legacy return_request: %v", err)
	}

	// Apply migration 0017 (and re-run idempotently via db.ApplyMigrations).
	if err := db.ApplyMigrations(ctx, pool); err != nil {
		t.Fatalf("db.ApplyMigrations (including 0017) failed on existing 3b-1 data: %v", err)
	}

	// Verify legacy row survived with default refund_approval_status=''.
	var legacyStatus, legacyApproval string
	var legacyDone bool
	if err := pool.QueryRow(ctx, `SELECT status, refund_done, refund_approval_status FROM return_requests WHERE id='ret-m17-legacy'`).
		Scan(&legacyStatus, &legacyDone, &legacyApproval); err != nil {
		t.Fatalf("select legacy return: %v", err)
	}
	if legacyStatus != "REFUNDED" || !legacyDone || legacyApproval != "" {
		t.Fatalf("legacy 3b-1 row unexpected: status=%q done=%v approval=%q", legacyStatus, legacyDone, legacyApproval)
	}

	// Insert a PENDING_APPROVAL REFUNDING return and test every forbidden CHECK combination.
	if _, err := pool.Exec(ctx, `
		INSERT INTO return_requests (id, order_id, order_code, buyer_id, shop_id, reason, status, refund_amount_vnd, require_approval_if_large, refund_approval_status)
		VALUES ('ret-m17-new', $1, 'NM-M17-1', 'buyer-1', 'shop-1', 'high value', 'REFUNDING', 250000, TRUE, 'PENDING_APPROVAL')
	`, ordNewTest); err != nil {
		t.Fatalf("insert PENDING_APPROVAL return: %v", err)
	}

	forbiddenQueries := []struct {
		name string
		sql  string
	}{
		{"refund_done_with_PENDING_APPROVAL", `UPDATE return_requests SET refund_done = TRUE WHERE id = 'ret-m17-new'`},
		{"status_REFUNDED_with_PENDING_APPROVAL", `UPDATE return_requests SET status = 'REFUNDED' WHERE id = 'ret-m17-new'`},
		{"refund_done_with_REJECTED", `UPDATE return_requests SET refund_approval_status = 'REJECTED', refund_done = TRUE WHERE id = 'ret-m17-new'`},
		{"status_REFUNDED_with_REJECTED", `UPDATE return_requests SET refund_approval_status = 'REJECTED', status = 'REFUNDED' WHERE id = 'ret-m17-new'`},
	}
	for _, tc := range forbiddenQueries {
		if _, err := pool.Exec(ctx, tc.sql); err == nil {
			t.Fatalf("Item D (#3): expected CHECK constraint violation for %s, got nil", tc.name)
		}
	}

	// Valid APPROVED transition MUST succeed.
	if _, err := pool.Exec(ctx, `
		UPDATE return_requests
		SET refund_approval_status = 'APPROVED', refund_done = TRUE, status = 'REFUNDED'
		WHERE id = 'ret-m17-new'
	`); err != nil {
		t.Fatalf("Item D (#3): valid APPROVED + refund_done + REFUNDED update must succeed, got %v", err)
	}
}

// TestPostgresW3B2P4_C_RecordPaymentRefunded_AtomicReturnApproval verifies Item C (#4c) on real PostgreSQL:
// RecordPaymentRefunded atomically updates return_requests to refund_approval_status='APPROVED' + refund_done=TRUE
// in the same DB transaction (both by refund_id and out-of-order by idempotency_key), and FindRefundingReturnRequests
// returns APPROVED rows waiting for promo/restock/REFUNDED completion.
func TestPostgresW3B2P4_C_RecordPaymentRefunded_AtomicReturnApproval(t *testing.T) {
	st, _ := openTempPostgresStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	for _, tc := range []struct {
		name          string
		savedRefundID string
		eventRefundID string
		eventIdemKey  string
	}{
		{name: "MatchBySavedRefundID", savedRefundID: "ref-pg-4c-1", eventRefundID: "ref-pg-4c-1", eventIdemKey: ""},
		{name: "MatchOutOfOrderByIdempotencyKey", savedRefundID: "", eventRefundID: "ref-pg-4c-2", eventIdemKey: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			grpID := "grp-pg-4c-" + uuid.NewString()[:8]
			ordID := "ord-pg-4c-" + uuid.NewString()[:8]
			retID := "ret-pg-4c-" + uuid.NewString()[:8]
			payID := "pay-pg-4c-" + uuid.NewString()[:8]
			if tc.savedRefundID == "" {
				tc.eventIdemKey = "order-return-refund:" + ordID
			}

			exp := now.Add(15 * time.Minute)
			seedPGCheckoutGroupWithOrder(t, st, grpID, "buyer-1", "shop-1", "idem-"+grpID, db.InsertOrderParams{
				ID: ordID, OrderCode: "NM-4C-" + ordID[len(ordID)-6:],
				Status: "PENDING", PaymentMethod: "VIETQR", PaymentStatus: "PENDING",
				SubtotalVND: 2000000, TotalPayableVND: 2000000, PaymentExpiresAt: &exp,
			}, now)
			if err := st.CompleteCheckoutWithPayment(ctx, UpdateCheckoutPaymentParams{
				CheckoutGroupID: grpID, PaymentID: payID, PaymentStatus: "PENDING", PaymentExpiresAt: &exp,
			}); err != nil {
				t.Fatalf("CompleteCheckoutWithPayment: %v", err)
			}
			if _, err := st.TransitionOrdersPaymentResult(ctx, PaymentResultTransitionParams{
				CheckoutGroupID: grpID, PaymentID: payID, ResultStatus: orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_SUCCEEDED, Now: now,
			}); err != nil {
				t.Fatalf("TransitionOrdersPaymentResult: %v", err)
			}
			if _, err := st.TransitionOrderStatus(ctx, SingleOrderTransitionParams{
				OrderID: ordID, TargetStatus: orderv1.OrderStatus_ORDER_STATUS_SHIPPING, ActorID: "seller-1", ActorRole: "seller", Now: now,
			}); err != nil {
				t.Fatalf("SHIPPING: %v", err)
			}
			if _, err := st.TransitionOrderStatus(ctx, SingleOrderTransitionParams{
				OrderID: ordID, TargetStatus: orderv1.OrderStatus_ORDER_STATUS_DELIVERED, ActorID: "ship", ActorRole: "system", Now: now,
			}); err != nil {
				t.Fatalf("DELIVERED: %v", err)
			}
			dl := now.Add(48 * time.Hour)
			if _, _, err := st.TransitionOrderReturnRequested(ctx, ReturnRequestParams{
				OrderID: ordID, BuyerID: "buyer-1", ShopID: "shop-1", ReturnRequestID: retID,
				Reason: "defect", EvidenceURLsJSON: []byte(`[]`), RefundAmountVND: 2000000, DeadlineAt: &dl,
				ActorID: "buyer-1", ActorRole: "buyer", Now: now,
			}); err != nil {
				t.Fatalf("TransitionOrderReturnRequested: %v", err)
			}
			reqApp := true
			maker := "cskh-1"
			if _, err := st.TransitionReturnRequest(ctx, ReturnTransitionParams{
				ReturnRequestID: retID, TargetStatus: orderv1.ReturnStatus_RETURN_STATUS_REFUNDING,
				RequireApprovalIfLarge: &reqApp, RefundRequestedBy: &maker,
				ActorID: "cskh-1", ActorRole: "cskh", Note: "approve", Now: now,
			}); err != nil {
				t.Fatalf("REFUNDING: %v", err)
			}
			if tc.savedRefundID != "" {
				if _, err := st.UpdateReturnRefundApprovalState(ctx, retID, tc.savedRefundID, "PENDING_APPROVAL", false, nil); err != nil {
					t.Fatalf("UpdateReturnRefundApprovalState: %v", err)
				}
			}

			dup, err := st.RecordPaymentRefunded(ctx, PaymentRefundedParams{
				EventID:        "ev-pg-4c-" + uuid.NewString()[:8],
				EventType:      events.SubjectPaymentRefunded,
				PaymentID:      payID,
				OrderID:        ordID,
				RefundID:       tc.eventRefundID,
				IdempotencyKey: tc.eventIdemKey,
				AmountVND:      2000000,
				Reason:         "checker approved",
			})
			if err != nil || dup {
				t.Fatalf("RecordPaymentRefunded: dup=%v err=%v", dup, err)
			}

			rr, err := st.GetReturnRequest(ctx, retID, "")
			if err != nil {
				t.Fatalf("GetReturnRequest: %v", err)
			}
			if rr.Request.RefundApprovalStatus != "APPROVED" || !rr.Request.RefundDone || rr.Request.RefundID != tc.eventRefundID {
				t.Fatalf("Item C (#4c PG): expected APPROVED + refund_done=true + refund_id=%q, got %+v", tc.eventRefundID, rr.Request)
			}

			ids, err := st.FindRefundingReturnRequests(ctx, 50)
			if err != nil {
				t.Fatalf("FindRefundingReturnRequests: %v", err)
			}
			found := false
			for _, id := range ids {
				if id == retID {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("Item C (#4c PG): FindRefundingReturnRequests did not include APPROVED return %s (got %v)", retID, ids)
			}
		})
	}
}
