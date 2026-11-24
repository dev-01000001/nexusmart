package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	"github.com/nexusmart/core/services/order/internal/db"
	"github.com/nexusmart/core/services/order/internal/events"
	"github.com/nexusmart/core/services/order/internal/statemachine"
)

func seedPostgresOrder(
	t *testing.T,
	st *PostgresStore,
	buyerID, shopID, orderID, paymentMethod, initialStatus string,
) {
	t.Helper()
	ctx := context.Background()
	gid := "grp-w3b-" + orderID
	if _, err := st.CreateCheckoutGroup(ctx, db.CreateCheckoutGroupParams{
		ID:              gid,
		BuyerID:         buyerID,
		IdempotencyKey:  "idem-w3b-" + orderID,
		RequestHash:     "hash-" + orderID,
		Status:          "IN_PROGRESS",
		PaymentMethod:   paymentMethod,
		TotalPayableVND: 150000,
	}); err != nil {
		t.Fatalf("CreateCheckoutGroup(%s): %v", orderID, err)
	}
	now := time.Now().UTC().Add(-2 * time.Hour)
	if _, err := st.PersistCheckoutBatch(ctx, CheckoutBatch{
		CheckoutGroupID: gid,
		Now:             now,
		Orders: []NewOrderRecord{
			{
				Order: db.InsertOrderParams{
					ID:                  orderID,
					OrderCode:           "NM-W3B-" + orderID,
					CheckoutGroupID:     gid,
					BuyerID:             buyerID,
					ShopID:              shopID,
					Status:              initialStatus,
					PaymentMethod:       paymentMethod,
					PaymentStatus:       "PENDING",
					SubtotalVND:         120000,
					ShippingFeeVND:      30000,
					TotalPayableVND:     150000,
					CarrierCode:         "ghn",
					ServiceCode:         "standard",
					ShippingAddressJSON: []byte(`{"province_code":"79","ward_code":"26734"}`),
				},
			},
		},
	}); err != nil {
		t.Fatalf("PersistCheckoutBatch(%s): %v", orderID, err)
	}
	if err := st.CompleteCheckoutWithPayment(ctx, UpdateCheckoutPaymentParams{
		CheckoutGroupID: gid,
		PaymentID:       "pay-w3b-" + orderID,
		PaymentStatus:   "PENDING",
	}); err != nil {
		t.Fatalf("CompleteCheckoutWithPayment(%s): %v", orderID, err)
	}
}

// TestPostgresW3B_Migration0015_AndCODRateIndex verifies migration 0015 creates
// orders.shipment_job_status and idx_orders_buyer_cod_status, and that PostgreSQL
// can serve GetBuyerTerminalCODStats via an index-only/index scan on idx_orders_buyer_cod_status.
func TestPostgresW3B_Migration0015_AndCODRateIndex(t *testing.T) {
	st, pool := openTempPostgresStore(t)
	ctx := context.Background()

	// 1. Column shipment_job_status exists with NOT NULL DEFAULT ''.
	var colDefault, isNullable string
	if err := pool.QueryRow(ctx, `
		SELECT column_default, is_nullable
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'orders' AND column_name = 'shipment_job_status'
	`).Scan(&colDefault, &isNullable); err != nil {
		t.Fatalf("migration 0015 column orders.shipment_job_status missing: %v", err)
	}
	if isNullable != "NO" || !strings.Contains(colDefault, "''") {
		t.Fatalf("unexpected shipment_job_status metadata: nullable=%q default=%q", isNullable, colDefault)
	}

	// 2. Partial index idx_orders_buyer_cod_status exists.
	var indexDef string
	if err := pool.QueryRow(ctx, `
		SELECT indexdef FROM pg_indexes
		WHERE schemaname = 'public' AND tablename = 'orders' AND indexname = 'idx_orders_buyer_cod_status'
	`).Scan(&indexDef); err != nil {
		t.Fatalf("migration 0015 index idx_orders_buyer_cod_status missing: %v", err)
	}
	if !strings.Contains(indexDef, "buyer_id") || !strings.Contains(indexDef, "COD") {
		t.Fatalf("unexpected index definition: %s", indexDef)
	}

	// 3. Verify planner can use idx_orders_buyer_cod_status for GetBuyerTerminalCODStats.
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
		SELECT
		    COUNT(*) FILTER (WHERE status IN ('COMPLETED', 'RETURNED'))::BIGINT AS total_terminal,
		    COUNT(*) FILTER (WHERE status = 'RETURNED' AND delivered_at IS NULL)::BIGINT AS rejected_count
		FROM orders
		WHERE buyer_id = $1
		  AND payment_method = 'COD'
		  AND status IN ('COMPLETED', 'RETURNED')
	`, "buyer-explain")
	if err != nil {
		t.Fatalf("EXPLAIN GetBuyerTerminalCODStats: %v", err)
	}
	defer rows.Close()
	var planLines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("scan explain line: %v", err)
		}
		planLines = append(planLines, line)
	}
	planText := strings.Join(planLines, "\n")
	if !strings.Contains(planText, "idx_orders_buyer_cod_status") {
		t.Fatalf("E19 index violation: expected query plan to use idx_orders_buyer_cod_status, got:\n%s", planText)
	}
	_ = st
}

// TestPostgresW3B_E19_GetBuyerTerminalCODStats verifies the single indexed terminal COD query on real Postgres.
func TestPostgresW3B_E19_GetBuyerTerminalCODStats(t *testing.T) {
	st, pool := openTempPostgresStore(t)
	ctx := context.Background()

	// Seed mixed orders for buyer-rate:
	//  - 2 COD COMPLETED (terminal, not rejected)
	//  - 1 COD RETURNED with delivered_at IS NULL (terminal, rejected = bom hang)
	//  - 1 COD RETURNED with delivered_at IS NOT NULL (terminal, NOT rejected = post-delivery defective return)
	//  - 2 COD CANCELLED (non-terminal / excluded)
	//  - 1 COD CONFIRMED, 1 COD SHIPPING, 1 COD DELIVERED (non-terminal / excluded)
	//  - 2 VIETQR RETURNED with delivered_at IS NULL (non-COD / excluded)
	//  - 1 other-buyer COD RETURNED with delivered_at IS NULL (other buyer / excluded)
	seedPostgresOrder(t, st, "buyer-rate", "shop-1", "o-cod-comp-1", "COD", "CONFIRMED")
	seedPostgresOrder(t, st, "buyer-rate", "shop-1", "o-cod-comp-2", "COD", "CONFIRMED")
	seedPostgresOrder(t, st, "buyer-rate", "shop-1", "o-cod-refused", "COD", "CONFIRMED")
	seedPostgresOrder(t, st, "buyer-rate", "shop-1", "o-cod-postdeliv-ret", "COD", "CONFIRMED")
	seedPostgresOrder(t, st, "buyer-rate", "shop-1", "o-cod-cancel-1", "COD", "CONFIRMED")
	seedPostgresOrder(t, st, "buyer-rate", "shop-1", "o-cod-cancel-2", "COD", "CONFIRMED")
	seedPostgresOrder(t, st, "buyer-rate", "shop-1", "o-cod-confirmed", "COD", "CONFIRMED")
	seedPostgresOrder(t, st, "buyer-rate", "shop-1", "o-cod-shipping", "COD", "CONFIRMED")
	seedPostgresOrder(t, st, "buyer-rate", "shop-1", "o-cod-delivered", "COD", "CONFIRMED")
	seedPostgresOrder(t, st, "buyer-rate", "shop-1", "o-vqr-ret-1", "VIETQR", "CONFIRMED")
	seedPostgresOrder(t, st, "buyer-rate", "shop-1", "o-vqr-ret-2", "VIETQR", "CONFIRMED")
	seedPostgresOrder(t, st, "buyer-other", "shop-1", "o-other-refused", "COD", "CONFIRMED")

	now := time.Now().UTC()
	mustExec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("exec %q: %v", sql, err)
		}
	}
	mustExec(`UPDATE orders SET status = 'COMPLETED', delivered_at = $1, completed_at = $1 WHERE id IN ('o-cod-comp-1', 'o-cod-comp-2')`, now)
	mustExec(`UPDATE orders SET status = 'RETURNED', delivered_at = NULL WHERE id IN ('o-cod-refused', 'o-vqr-ret-1', 'o-vqr-ret-2', 'o-other-refused')`)
	mustExec(`UPDATE orders SET status = 'RETURNED', delivered_at = $1 WHERE id = 'o-cod-postdeliv-ret'`, now)
	mustExec(`UPDATE orders SET status = 'CANCELLED' WHERE id IN ('o-cod-cancel-1', 'o-cod-cancel-2')`)
	mustExec(`UPDATE orders SET status = 'SHIPPING' WHERE id = 'o-cod-shipping'`)
	mustExec(`UPDATE orders SET status = 'DELIVERED', delivered_at = $1 WHERE id = 'o-cod-delivered'`, now)

	stats, err := st.GetBuyerTerminalCODStats(ctx, "buyer-rate")
	if err != nil {
		t.Fatalf("GetBuyerTerminalCODStats: %v", err)
	}
	// TotalTerminal = 2 COMPLETED + 1 refused RETURNED + 1 post-delivery RETURNED = 4.
	// RejectedCount = 1 (ONLY the refused RETURNED where delivered_at IS NULL).
	if stats.TotalTerminal != 4 || stats.RejectedCount != 1 {
		t.Fatalf("GetBuyerTerminalCODStats = %+v, want {TotalTerminal:4 RejectedCount:1}", stats)
	}

	emptyStats, err := st.GetBuyerTerminalCODStats(ctx, "buyer-brand-new")
	if err != nil {
		t.Fatalf("GetBuyerTerminalCODStats(empty): %v", err)
	}
	if emptyStats.TotalTerminal != 0 || emptyStats.RejectedCount != 0 {
		t.Fatalf("empty buyer stats = %+v, want {0, 0}", emptyStats)
	}
}

// TestPostgresW3B_E18_MarkOrderShipmentQueued_AndRaces tests MarkOrderShipmentQueued on real Postgres.
func TestPostgresW3B_E18_MarkOrderShipmentQueued_AndRaces(t *testing.T) {
	st, _ := openTempPostgresStore(t)
	ctx := context.Background()

	// 1. Happy path: CONFIRMED -> MarkOrderShipmentCreating -> MarkOrderShipmentQueued.
	seedPostgresOrder(t, st, "buyer-1", "shop-1", "ord-q-1", "COD", "CONFIRMED")
	tok, err := st.MarkOrderShipmentCreating(ctx, "ord-q-1", time.Now().UTC())
	if err != nil {
		t.Fatalf("MarkOrderShipmentCreating: %v", err)
	}
	qOut, err := st.MarkOrderShipmentQueued(ctx, QueueShipmentParams{
		OrderID:               "ord-q-1",
		JobID:                 "job-pg-1",
		ExpectedCreatingToken: tok,
		ActorID:               "seller-1",
		ActorRole:             "seller",
		Now:                   time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("MarkOrderShipmentQueued: %v", err)
	}
	if qOut.AlreadyAttached || qOut.Order.Order.Status != "CONFIRMED" ||
		qOut.Order.Order.ShipmentJobStatus != "QUEUED" || qOut.Order.Order.PendingShipmentCancel {
		t.Fatalf("unexpected queued state: attached=%v status=%s job=%q pending=%v",
			qOut.AlreadyAttached, qOut.Order.Order.Status, qOut.Order.Order.ShipmentJobStatus, qOut.Order.Order.PendingShipmentCancel)
	}
	for _, tl := range qOut.Order.Timeline {
		if strings.Contains(tl.Note, "job-pg-1") {
			t.Fatalf("timeline note must not leak internal job_id, got %q", tl.Note)
		}
	}
	tlAfterFirst := len(qOut.Order.Timeline)

	// Re-queue idempotently does NOT duplicate timeline entry.
	tok2, err := st.MarkOrderShipmentCreating(ctx, "ord-q-1", time.Now().UTC())
	if err != nil {
		t.Fatalf("second MarkOrderShipmentCreating: %v", err)
	}
	qOut2, err := st.MarkOrderShipmentQueued(ctx, QueueShipmentParams{
		OrderID:               "ord-q-1",
		JobID:                 "job-pg-1",
		ExpectedCreatingToken: tok2,
		ActorID:               "seller-1",
		ActorRole:             "seller",
		Now:                   time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("second MarkOrderShipmentQueued: %v", err)
	}
	if len(qOut2.Order.Timeline) != tlAfterFirst {
		t.Fatalf("duplicate timeline added on re-queue: %d -> %d", tlAfterFirst, len(qOut2.Order.Timeline))
	}

	// 2. Race: shipment.updated(created) already attached tracking before CreateShipment returns queued=true.
	seedPostgresOrder(t, st, "buyer-1", "shop-1", "ord-q-race-attach", "COD", "CONFIRMED")
	tokA, err := st.MarkOrderShipmentCreating(ctx, "ord-q-race-attach", time.Now().UTC())
	if err != nil {
		t.Fatalf("MarkOrderShipmentCreating(attach): %v", err)
	}
	if _, err := st.ApplyShipmentUpdate(ctx, ShipmentUpdateParams{
		EventID:        "evt-race-attach",
		EventType:      events.SubjectShipmentUpdated,
		OrderID:        "ord-q-race-attach",
		CarrierCode:    "ghn",
		ServiceCode:    "standard",
		TrackingCode:   "TRK-FAST-1",
		LabelURL:       "https://labels/fast-1",
		ShipmentStatus: "created",
		Now:            time.Now().UTC(),
	}); err != nil {
		t.Fatalf("ApplyShipmentUpdate(created): %v", err)
	}
	qOutA, err := st.MarkOrderShipmentQueued(ctx, QueueShipmentParams{
		OrderID:               "ord-q-race-attach",
		JobID:                 "job-fast-1",
		ExpectedCreatingToken: tokA,
		ActorID:               "seller-1",
		ActorRole:             "seller",
		Now:                   time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("MarkOrderShipmentQueued after attach: %v", err)
	}
	if !qOutA.AlreadyAttached || qOutA.Order.Order.Status != "PREPARING" ||
		qOutA.Order.Order.TrackingCode != "TRK-FAST-1" || qOutA.Order.Order.ShipmentJobStatus != "" {
		t.Fatalf("expected AlreadyAttached=true PREPARING TRK-FAST-1 job='', got attached=%v status=%s trk=%q job=%q",
			qOutA.AlreadyAttached, qOutA.Order.Order.Status, qOutA.Order.Order.TrackingCode, qOutA.Order.Order.ShipmentJobStatus)
	}

	// 3. Race: order CANCELLED before MarkOrderShipmentQueued commits -> returns ErrOrphanShipmentNeedsCancel.
	seedPostgresOrder(t, st, "buyer-1", "shop-1", "ord-q-race-cancel", "COD", "CONFIRMED")
	if _, err := st.TransitionOrderStatus(ctx, SingleOrderTransitionParams{
		OrderID:      "ord-q-race-cancel",
		TargetStatus: orderv1.OrderStatus_ORDER_STATUS_CANCELLED,
		CancelReason: "buyer cancelled",
		ActorID:      "buyer-1",
		ActorRole:    "buyer",
		Note:         "cancel",
		Now:          time.Now().UTC(),
	}); err != nil {
		t.Fatalf("cancel ord-q-race-cancel: %v", err)
	}
	_, err = st.MarkOrderShipmentQueued(ctx, QueueShipmentParams{
		OrderID:   "ord-q-race-cancel",
		JobID:     "job-orphan",
		ActorID:   "seller-1",
		ActorRole: "seller",
		Now:       time.Now().UTC(),
	})
	if !errors.Is(err, ErrOrphanShipmentNeedsCancel) {
		t.Fatalf("expected ErrOrphanShipmentNeedsCancel, got %v", err)
	}
	aggC, err := st.GetOrderWithDetails(ctx, "ord-q-race-cancel")
	if err != nil {
		t.Fatalf("GetOrderWithDetails: %v", err)
	}
	if !aggC.Order.PendingShipmentCancel || aggC.Order.ShipmentJobStatus != "" {
		t.Fatalf("expected pending_shipment_cancel=true and shipment_job_status='', got pending=%v job=%q",
			aggC.Order.PendingShipmentCancel, aggC.Order.ShipmentJobStatus)
	}
}

// TestPostgresW3B_E18_ApplyShipmentUpdate_AndE20Delivered verifies:
//   - shipment.updated(created): attaches tracking/label/carrier/service, clears shipment_job_status, CONFIRMED->PREPARING, idempotent replay.
//   - out-of-order delivering then created: preserves SHIPPING and backfills label_url.
//   - late created on CANCELLED order: sets durable pending_shipment_cancel=true and NeedsOrphanShipmentCancel=true.
//   - delivered emits order.delivered outbox JSON containing both carrier_code and tracking_code (E20).
func TestPostgresW3B_E18_ApplyShipmentUpdate_AndE20Delivered(t *testing.T) {
	st, pool := openTempPostgresStore(t)
	ctx := context.Background()

	// A. Queued -> created -> replay -> delivered (E18 + E20).
	seedPostgresOrder(t, st, "buyer-1", "shop-1", "ord-e18-e20", "COD", "CONFIRMED")
	if _, err := pool.Exec(ctx, `UPDATE orders SET carrier_code = 'fallback', service_code = 'standard', shipment_job_status = 'QUEUED' WHERE id = 'ord-e18-e20'`); err != nil {
		t.Fatalf("prepare fallback queued order: %v", err)
	}

	out1, err := st.ApplyShipmentUpdate(ctx, ShipmentUpdateParams{
		EventID:        "evt-pg-created-1",
		EventType:      events.SubjectShipmentUpdated,
		OrderID:        "ord-e18-e20",
		CarrierCode:    "ghtk",
		ServiceCode:    "express",
		TrackingCode:   "GHTK-PG-001",
		LabelURL:       "https://labels/ghtk-001",
		ShipmentStatus: "created",
		Now:            time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("ApplyShipmentUpdate(created): %v", err)
	}
	if out1.Order.Order.Status != "PREPARING" || out1.Order.Order.CarrierCode != "ghtk" ||
		out1.Order.Order.ServiceCode != "express" || out1.Order.Order.TrackingCode != "GHTK-PG-001" ||
		out1.Order.Order.LabelURL != "https://labels/ghtk-001" || out1.Order.Order.ShipmentJobStatus != "" {
		t.Fatalf("unexpected order after created: %+v", out1.Order.Order)
	}
	tlAfterCreated := len(out1.Order.Timeline)

	// Replay with a NEW event_id must be idempotent (no duplicate timeline).
	outReplay, err := st.ApplyShipmentUpdate(ctx, ShipmentUpdateParams{
		EventID:        "evt-pg-created-2",
		EventType:      events.SubjectShipmentUpdated,
		OrderID:        "ord-e18-e20",
		CarrierCode:    "ghtk",
		ServiceCode:    "express",
		TrackingCode:   "GHTK-PG-001",
		LabelURL:       "https://labels/ghtk-001",
		ShipmentStatus: "created",
		Now:            time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("ApplyShipmentUpdate(created replay): %v", err)
	}
	if len(outReplay.Order.Timeline) != tlAfterCreated {
		t.Fatalf("idempotent replay added timeline row: %d -> %d", tlAfterCreated, len(outReplay.Order.Timeline))
	}

	// Delivered -> verify E20 outbox payload carries BOTH carrier_code and tracking_code.
	if _, err := st.ApplyShipmentUpdate(ctx, ShipmentUpdateParams{
		EventID:        "evt-pg-delivered-1",
		EventType:      events.SubjectShipmentUpdated,
		OrderID:        "ord-e18-e20",
		CarrierCode:    "ghtk",
		TrackingCode:   "GHTK-PG-001",
		ShipmentStatus: "delivered",
		Now:            time.Now().UTC(),
	}); err != nil {
		t.Fatalf("ApplyShipmentUpdate(delivered): %v", err)
	}
	var payloadRaw []byte
	if err := pool.QueryRow(ctx, `
		SELECT payload_json FROM outbox_events
		WHERE event_type = $1 AND payload_json->>'order_id' = $2
		ORDER BY seq DESC LIMIT 1
	`, events.SubjectOrderDelivered, "ord-e18-e20").Scan(&payloadRaw); err != nil {
		t.Fatalf("query order.delivered outbox: %v", err)
	}
	var deliv map[string]any
	if err := json.Unmarshal(payloadRaw, &deliv); err != nil {
		t.Fatalf("unmarshal order.delivered payload: %v", err)
	}
	if deliv["carrier_code"] != "ghtk" || deliv["tracking_code"] != "GHTK-PG-001" {
		t.Fatalf("E20 violation on Postgres outbox: got carrier_code=%v tracking_code=%v raw=%s",
			deliv["carrier_code"], deliv["tracking_code"], string(payloadRaw))
	}

	// B. Out-of-order: delivering arrives BEFORE created -> stays SHIPPING and backfills label_url.
	seedPostgresOrder(t, st, "buyer-1", "shop-1", "ord-ooo", "COD", "CONFIRMED")
	if _, err := st.ApplyShipmentUpdate(ctx, ShipmentUpdateParams{
		EventID:        "evt-ooo-deliv",
		EventType:      events.SubjectShipmentUpdated,
		OrderID:        "ord-ooo",
		CarrierCode:    "ghn",
		TrackingCode:   "TRK-OOO-1",
		ShipmentStatus: "delivering",
		Now:            time.Now().UTC(),
	}); err != nil {
		t.Fatalf("ApplyShipmentUpdate(delivering): %v", err)
	}
	outOOO, err := st.ApplyShipmentUpdate(ctx, ShipmentUpdateParams{
		EventID:        "evt-ooo-created",
		EventType:      events.SubjectShipmentUpdated,
		OrderID:        "ord-ooo",
		CarrierCode:    "ghn",
		ServiceCode:    "standard",
		TrackingCode:   "TRK-OOO-1",
		LabelURL:       "https://labels/ooo-1",
		ShipmentStatus: "created",
		Now:            time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("ApplyShipmentUpdate(out-of-order created): %v", err)
	}
	if outOOO.Order.Order.Status != "SHIPPING" || outOOO.Order.Order.LabelURL != "https://labels/ooo-1" {
		t.Fatalf("out-of-order created must keep SHIPPING and backfill label, got status=%s label=%q",
			outOOO.Order.Order.Status, outOOO.Order.Order.LabelURL)
	}

	// C. Late created on CANCELLED order -> durable pending_shipment_cancel=true + NeedsOrphanShipmentCancel=true.
	seedPostgresOrder(t, st, "buyer-1", "shop-1", "ord-late-cancel", "COD", "CONFIRMED")
	if _, err := st.TransitionOrderStatus(ctx, SingleOrderTransitionParams{
		OrderID:      "ord-late-cancel",
		TargetStatus: orderv1.OrderStatus_ORDER_STATUS_CANCELLED,
		CancelReason: "cancelled",
		ActorID:      "buyer-1",
		ActorRole:    "buyer",
		Now:          time.Now().UTC(),
	}); err != nil {
		t.Fatalf("cancel ord-late-cancel: %v", err)
	}
	outLate, err := st.ApplyShipmentUpdate(ctx, ShipmentUpdateParams{
		EventID:        "evt-late-created",
		EventType:      events.SubjectShipmentUpdated,
		OrderID:        "ord-late-cancel",
		CarrierCode:    "ghn",
		TrackingCode:   "TRK-LATE-1",
		ShipmentStatus: "created",
		Now:            time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("ApplyShipmentUpdate(late created): %v", err)
	}
	if !outLate.NeedsOrphanShipmentCancel || outLate.Order.Order.Status != "CANCELLED" || !outLate.Order.Order.PendingShipmentCancel {
		t.Fatalf("expected NeedsOrphanShipmentCancel=true on CANCELLED order with durable pending_shipment_cancel=true, got %+v", outLate)
	}
}

// TestPostgresW3B_E18_ApplyShipmentCreateFailed verifies shipment.create_failed persistence and guards.
func TestPostgresW3B_E18_ApplyShipmentCreateFailed(t *testing.T) {
	st, _ := openTempPostgresStore(t)
	ctx := context.Background()

	// 1. Queued order -> FAILED, idempotent by event_id.
	seedPostgresOrder(t, st, "buyer-1", "shop-1", "ord-scf-1", "COD", "CONFIRMED")
	tok, _ := st.MarkOrderShipmentCreating(ctx, "ord-scf-1", time.Now().UTC())
	if _, err := st.MarkOrderShipmentQueued(ctx, QueueShipmentParams{
		OrderID:               "ord-scf-1",
		JobID:                 "job-scf-1",
		ExpectedCreatingToken: tok,
		ActorID:               "seller-1",
		ActorRole:             "seller",
		Now:                   time.Now().UTC(),
	}); err != nil {
		t.Fatalf("MarkOrderShipmentQueued: %v", err)
	}

	out, err := st.ApplyShipmentCreateFailed(ctx, ShipmentCreateFailedParams{
		EventID:      "evt-pg-scf-1",
		EventType:    events.SubjectShipmentCreateFailed,
		OrderID:      "ord-scf-1",
		JobID:        "job-scf-1",
		Attempts:     8,
		ErrorMessage: "HTTP 503 https://carrier/internal",
		Now:          time.Now().UTC(),
	})
	if err != nil || !out.Updated || out.Order.Order.ShipmentJobStatus != "FAILED" || out.Order.Order.Status != "CONFIRMED" {
		t.Fatalf("ApplyShipmentCreateFailed = %+v, err=%v; want Updated=true job=FAILED status=CONFIRMED", out, err)
	}
	foundFailedNote := false
	for _, tl := range out.Order.Timeline {
		if strings.Contains(tl.Note, "503") || strings.Contains(tl.Note, "carrier/") || strings.Contains(tl.Note, "job-scf-1") {
			t.Fatalf("timeline note must not leak raw carrier error or job_id, got %q", tl.Note)
		}
		if strings.Contains(tl.Note, "after 8 attempts") {
			foundFailedNote = true
		}
	}
	if !foundFailedNote {
		t.Fatalf("expected generic failure note with attempt count in timeline, got %+v", out.Order.Timeline)
	}

	// Duplicate event_id -> AlreadyProcessed.
	dup, err := st.ApplyShipmentCreateFailed(ctx, ShipmentCreateFailedParams{
		EventID:   "evt-pg-scf-1",
		EventType: events.SubjectShipmentCreateFailed,
		OrderID:   "ord-scf-1",
		JobID:     "job-scf-1",
		Attempts:  8,
	})
	if err != nil || !dup.AlreadyProcessed {
		t.Fatalf("duplicate event_id should return AlreadyProcessed, got %+v err=%v", dup, err)
	}

	// 2. Ignored when a new synchronous SellerShipOrder attempt is actively in flight (pending_shipment_cancel=true).
	seedPostgresOrder(t, st, "buyer-1", "shop-1", "ord-scf-inflight", "COD", "CONFIRMED")
	if _, err := st.MarkOrderShipmentCreating(ctx, "ord-scf-inflight", time.Now().UTC()); err != nil {
		t.Fatalf("MarkOrderShipmentCreating: %v", err)
	}
	outInflight, err := st.ApplyShipmentCreateFailed(ctx, ShipmentCreateFailedParams{
		EventID:      "evt-pg-scf-inflight",
		EventType:    events.SubjectShipmentCreateFailed,
		OrderID:      "ord-scf-inflight",
		JobID:        "job-old",
		Attempts:     8,
		ErrorMessage: "stale error",
	})
	if err != nil || outInflight.Updated || outInflight.Order.Order.ShipmentJobStatus != "" {
		t.Fatalf("stale failed event during in-flight ship attempt must not set FAILED, got %+v err=%v", outInflight, err)
	}

	// 3. Ignored when tracking_code is already attached.
	seedPostgresOrder(t, st, "buyer-1", "shop-1", "ord-scf-tracked", "COD", "CONFIRMED")
	if _, err := st.ApplyShipmentUpdate(ctx, ShipmentUpdateParams{
		EventID:        "evt-scf-tracked-created",
		EventType:      events.SubjectShipmentUpdated,
		OrderID:        "ord-scf-tracked",
		CarrierCode:    "ghn",
		TrackingCode:   "TRK-EXISTS",
		ShipmentStatus: "created",
		Now:            time.Now().UTC(),
	}); err != nil {
		t.Fatalf("attach tracking: %v", err)
	}
	outTracked, err := st.ApplyShipmentCreateFailed(ctx, ShipmentCreateFailedParams{
		EventID:      "evt-pg-scf-tracked",
		EventType:    events.SubjectShipmentCreateFailed,
		OrderID:      "ord-scf-tracked",
		JobID:        "job-old",
		Attempts:     8,
		ErrorMessage: "stale error",
	})
	if err != nil || outTracked.Updated || outTracked.Order.Order.ShipmentJobStatus != "" {
		t.Fatalf("stale failed event after tracking attached must not set FAILED, got %+v err=%v", outTracked, err)
	}
}

// TestPostgresW3B_E18_ConcurrentCancelVsShipmentCreated races buyer cancellation
// against async shipment.updated(status="created") on real Postgres across multiple iterations
// and asserts that no orphan shipment is ever silently lost.
func TestPostgresW3B_E18_ConcurrentCancelVsShipmentCreated(t *testing.T) {
	st, _ := openTempPostgresStore(t)
	ctx := context.Background()

	for i := 0; i < 6; i++ {
		ordID := fmt.Sprintf("ord-race-%d", i)
		seedPostgresOrder(t, st, "buyer-1", "shop-1", ordID, "COD", "CONFIRMED")
		tok, err := st.MarkOrderShipmentCreating(ctx, ordID, time.Now().UTC())
		if err != nil {
			t.Fatalf("iter %d MarkOrderShipmentCreating: %v", i, err)
		}
		qOut, err := st.MarkOrderShipmentQueued(ctx, QueueShipmentParams{
			OrderID:               ordID,
			JobID:                 fmt.Sprintf("job-race-%d", i),
			ExpectedCreatingToken: tok,
			ActorID:               "seller-1",
			ActorRole:             "seller",
			Now:                   time.Now().UTC(),
		})
		if err != nil {
			t.Fatalf("iter %d MarkOrderShipmentQueued: %v", i, err)
		}
		queuedAt := qOut.Order.Order.UpdatedAt

		start := make(chan struct{})
		var wg sync.WaitGroup
		var cancelErr error
		var shipOut *ShipmentUpdateOutcome
		var shipErr error

		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, cancelErr = st.TransitionOrderStatus(ctx, SingleOrderTransitionParams{
				OrderID:              ordID,
				TargetStatus:         orderv1.OrderStatus_ORDER_STATUS_CANCELLED,
				CancelReason:         "concurrent cancel",
				ActorID:              "buyer-1",
				ActorRole:            "buyer",
				RequireNoTracking:    false,
				VerifyShipmentCancel: true,
				KnownShipmentQueued:  true,
				KnownQueuedUpdatedAt: &queuedAt,
				LastCancelNotFound:   true,
				Now:                  time.Now().UTC(),
			})
		}()
		go func() {
			defer wg.Done()
			<-start
			shipOut, shipErr = st.ApplyShipmentUpdate(ctx, ShipmentUpdateParams{
				EventID:        fmt.Sprintf("evt-race-%d", i),
				EventType:      events.SubjectShipmentUpdated,
				OrderID:        ordID,
				CarrierCode:    "ghn",
				ServiceCode:    "standard",
				TrackingCode:   fmt.Sprintf("TRK-RACE-%d", i),
				LabelURL:       "https://labels/race",
				ShipmentStatus: "created",
				Now:            time.Now().UTC(),
			})
		}()
		close(start)
		wg.Wait()

		if shipErr != nil {
			t.Fatalf("iter %d ApplyShipmentUpdate error: %v", i, shipErr)
		}
		agg, err := st.GetOrderWithDetails(ctx, ordID)
		if err != nil {
			t.Fatalf("iter %d GetOrderWithDetails: %v", i, err)
		}

		switch statemachine.StringToStatus(agg.Order.Status) {
		case orderv1.OrderStatus_ORDER_STATUS_CANCELLED:
			// Cancel won before created -> created MUST have flagged durable orphan cancel!
			if cancelErr != nil {
				t.Fatalf("iter %d: order is CANCELLED yet cancelErr=%v", i, cancelErr)
			}
			if !shipOut.NeedsOrphanShipmentCancel || !agg.Order.PendingShipmentCancel {
				t.Fatalf("iter %d E18 race violation: CANCELLED order missed orphan shipment cancel flag (needs=%v pending=%v)",
					i, shipOut.NeedsOrphanShipmentCancel, agg.Order.PendingShipmentCancel)
			}
		case orderv1.OrderStatus_ORDER_STATUS_PREPARING:
			// Created won first -> tracking attached, shipment_job_status cleared, and unverified cancel blocked with ErrOrderHasTracking.
			if !errors.Is(cancelErr, ErrOrderHasTracking) {
				t.Fatalf("iter %d: expected ErrOrderHasTracking when created won first, got cancelErr=%v", i, cancelErr)
			}
			if agg.Order.TrackingCode == "" || agg.Order.ShipmentJobStatus != "" {
				t.Fatalf("iter %d: PREPARING order invalid state: trk=%q job=%q", i, agg.Order.TrackingCode, agg.Order.ShipmentJobStatus)
			}
		default:
			t.Fatalf("iter %d: unexpected final order status %s", i, agg.Order.Status)
		}
	}
}

// TestPostgresW3B_ReviewP1_QueuedCancelGuardAndCancelledDeliveringOrphan verifies review P1 fixes on real PostgreSQL:
//   - checkCancelShipmentGuard rejects unverified cancel (RequireNoTracking=true or stale KnownQueuedUpdatedAt) on QUEUED orders.
//   - MarkOrderShipmentJobFailedIfQueued safely flips zombie QUEUED -> FAILED without touching active/tracked/cancelled orders.
//   - ApplyShipmentUpdate on CANCELLED order with no tracking triggers NeedsOrphanShipmentCancel for picking/picked/delivering,
//     ignores "cancelled", deduplicates by event_id, and stops looping once orphan_shipment_needs_manual_review=true.
//   - Concurrent race between MarkOrderShipmentQueued and unverified CancelOrder never bypasses ErrOrderHasTracking.
func TestPostgresW3B_ReviewP1_QueuedCancelGuardAndCancelledDeliveringOrphan(t *testing.T) {
	st, pool := openTempPostgresStore(t)
	ctx := context.Background()

	// 1. Guard rejects unverified cancel on QUEUED order, detects refreshed QUEUED timestamp, allows verified cancel.
	seedPostgresOrder(t, st, "buyer-1", "shop-1", "ord-p1-guard", "COD", "CONFIRMED")
	tok1, err := st.MarkOrderShipmentCreating(ctx, "ord-p1-guard", time.Now().UTC())
	if err != nil {
		t.Fatalf("MarkOrderShipmentCreating: %v", err)
	}
	q1, err := st.MarkOrderShipmentQueued(ctx, QueueShipmentParams{
		OrderID:               "ord-p1-guard",
		JobID:                 "job-guard-1",
		ExpectedCreatingToken: tok1,
		ActorID:               "seller-1",
		ActorRole:             "seller",
		Now:                   time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("MarkOrderShipmentQueued: %v", err)
	}
	t1 := q1.Order.Order.UpdatedAt

	// Unverified cancel (RequireNoTracking=true) MUST fail with ErrOrderHasTracking.
	if _, err := st.TransitionOrderStatus(ctx, SingleOrderTransitionParams{
		OrderID:              "ord-p1-guard",
		TargetStatus:         orderv1.OrderStatus_ORDER_STATUS_CANCELLED,
		CancelReason:         "unverified",
		ActorID:              "buyer-1",
		ActorRole:            "buyer",
		RequireNoTracking:    true,
		VerifyShipmentCancel: true,
		Now:                  time.Now().UTC(),
	}); !errors.Is(err, ErrOrderHasTracking) {
		t.Fatalf("P1 violation: QUEUED order must reject unverified cancel with ErrOrderHasTracking, got %v", err)
	}

	// Simulate a re-queue advancing updated_at after an earlier CancelShipment observed t1 -> stale t1 MUST fail with ErrOrderHasTracking.
	if _, err := pool.Exec(ctx, `UPDATE orders SET updated_at = updated_at + INTERVAL '2 seconds' WHERE id = 'ord-p1-guard'`); err != nil {
		t.Fatalf("bump updated_at: %v", err)
	}
	if _, err := st.TransitionOrderStatus(ctx, SingleOrderTransitionParams{
		OrderID:              "ord-p1-guard",
		TargetStatus:         orderv1.OrderStatus_ORDER_STATUS_CANCELLED,
		CancelReason:         "stale queued timestamp",
		ActorID:              "buyer-1",
		ActorRole:            "buyer",
		RequireNoTracking:    false,
		VerifyShipmentCancel: true,
		KnownShipmentQueued:  true,
		KnownQueuedUpdatedAt: &t1,
		LastCancelNotFound:   true,
		Now:                  time.Now().UTC(),
	}); !errors.Is(err, ErrOrderHasTracking) {
		t.Fatalf("P1 violation: refreshed QUEUED timestamp must fail with ErrOrderHasTracking, got %v", err)
	}

	// Refresh timestamp and commit verified cancel -> succeeds!
	aggLatest, err := st.GetOrderWithDetails(ctx, "ord-p1-guard")
	if err != nil {
		t.Fatalf("GetOrderWithDetails: %v", err)
	}
	t2 := aggLatest.Order.UpdatedAt
	resOK, err := st.TransitionOrderStatus(ctx, SingleOrderTransitionParams{
		OrderID:              "ord-p1-guard",
		TargetStatus:         orderv1.OrderStatus_ORDER_STATUS_CANCELLED,
		CancelReason:         "verified cancel",
		ActorID:              "buyer-1",
		ActorRole:            "buyer",
		RequireNoTracking:    false,
		VerifyShipmentCancel: true,
		KnownShipmentQueued:  true,
		KnownQueuedUpdatedAt: &t2,
		LastCancelNotFound:   true,
		Now:                  time.Now().UTC(),
	})
	if err != nil || resOK.Order.Order.Status != "CANCELLED" || resOK.Order.Order.ShipmentJobStatus != "" {
		t.Fatalf("verified cancel on QUEUED failed: res=%+v err=%v", resOK, err)
	}

	// 2. MarkOrderShipmentJobFailedIfQueued flips alive QUEUED -> FAILED, ignores pending/tracked/cancelled.
	seedPostgresOrder(t, st, "buyer-1", "shop-1", "ord-p1-zombie", "COD", "CONFIRMED")
	tokZ, _ := st.MarkOrderShipmentCreating(ctx, "ord-p1-zombie", time.Now().UTC())
	if _, err := st.MarkOrderShipmentQueued(ctx, QueueShipmentParams{
		OrderID:               "ord-p1-zombie",
		JobID:                 "job-z",
		ExpectedCreatingToken: tokZ,
		ActorID:               "seller-1",
		ActorRole:             "seller",
		Now:                   time.Now().UTC(),
	}); err != nil {
		t.Fatalf("queue ord-p1-zombie: %v", err)
	}
	flipped, err := st.MarkOrderShipmentJobFailedIfQueued(ctx, "ord-p1-zombie")
	if err != nil || !flipped {
		t.Fatalf("MarkOrderShipmentJobFailedIfQueued = %v, %v; want true, nil", flipped, err)
	}
	aggZ, _ := st.GetOrderWithDetails(ctx, "ord-p1-zombie")
	if aggZ.Order.ShipmentJobStatus != "FAILED" {
		t.Fatalf("expected shipment_job_status=FAILED, got %q", aggZ.Order.ShipmentJobStatus)
	}
	// Second call is a no-op (already FAILED).
	if flipped2, err := st.MarkOrderShipmentJobFailedIfQueued(ctx, "ord-p1-zombie"); err != nil || flipped2 {
		t.Fatalf("second MarkOrderShipmentJobFailedIfQueued = %v, %v; want false, nil", flipped2, err)
	}

	// 3. CANCELLED order with no tracking receiving picking/picked/delivering triggers NeedsOrphanShipmentCancel;
	//    "cancelled" is ignored; duplicate event_id & post-manual-review events do not loop.
	seedPostgresOrder(t, st, "buyer-1", "shop-1", "ord-p1-can-deliv", "COD", "CONFIRMED")
	if _, err := st.TransitionOrderStatus(ctx, SingleOrderTransitionParams{
		OrderID:      "ord-p1-can-deliv",
		TargetStatus: orderv1.OrderStatus_ORDER_STATUS_CANCELLED,
		CancelReason: "cancelled",
		ActorID:      "buyer-1",
		ActorRole:    "buyer",
		Now:          time.Now().UTC(),
	}); err != nil {
		t.Fatalf("cancel ord-p1-can-deliv: %v", err)
	}
	beforeCan, _ := st.GetOrderWithDetails(ctx, "ord-p1-can-deliv")
	tlBefore := len(beforeCan.Timeline)

	outCanIgnored, err := st.ApplyShipmentUpdate(ctx, ShipmentUpdateParams{
		EventID:        "evt-pg-can-ignored",
		EventType:      events.SubjectShipmentUpdated,
		OrderID:        "ord-p1-can-deliv",
		TrackingCode:   "TRK-ORPHAN",
		ShipmentStatus: "cancelled",
		Now:            time.Now().UTC(),
	})
	if err != nil || outCanIgnored.NeedsOrphanShipmentCancel {
		t.Fatalf("status=cancelled must not trigger orphan cancel: out=%+v err=%v", outCanIgnored, err)
	}

	for _, stName := range []string{"picking", "delivering"} {
		outOrph, err := st.ApplyShipmentUpdate(ctx, ShipmentUpdateParams{
			EventID:        "evt-pg-orph-" + stName,
			EventType:      events.SubjectShipmentUpdated,
			OrderID:        "ord-p1-can-deliv",
			CarrierCode:    "ghn",
			TrackingCode:   "TRK-ORPHAN",
			ShipmentStatus: stName,
			Now:            time.Now().UTC(),
		})
		if err != nil || !outOrph.NeedsOrphanShipmentCancel || !outOrph.Order.Order.PendingShipmentCancel {
			t.Fatalf("P1 violation: status=%s on CANCELLED order must set NeedsOrphanShipmentCancel & pending_shipment_cancel, got out=%+v err=%v",
				stName, outOrph, err)
		}
		if len(outOrph.Order.Timeline) != tlBefore {
			t.Fatalf("ApplyShipmentUpdate must not add fake timeline rows on CANCELLED order before CancelShipment result (%d -> %d)",
				tlBefore, len(outOrph.Order.Timeline))
		}
	}

	// Duplicate event_id returns AlreadyProcessed.
	dupOrph, err := st.ApplyShipmentUpdate(ctx, ShipmentUpdateParams{
		EventID:        "evt-pg-orph-delivering",
		EventType:      events.SubjectShipmentUpdated,
		OrderID:        "ord-p1-can-deliv",
		TrackingCode:   "TRK-ORPHAN",
		ShipmentStatus: "delivering",
		Now:            time.Now().UTC(),
	})
	if err != nil || !dupOrph.AlreadyProcessed || dupOrph.NeedsOrphanShipmentCancel {
		t.Fatalf("duplicate orphan event should return AlreadyProcessed without re-triggering cancel, got %+v err=%v", dupOrph, err)
	}

	// Once flagged for manual review, subsequent events do not re-trigger NeedsOrphanShipmentCancel.
	if err := st.MarkOrderOrphanShipmentManualReview(ctx, "ord-p1-can-deliv", "MANUAL_REVIEW_REQUIRED: already picked up"); err != nil {
		t.Fatalf("MarkOrderOrphanShipmentManualReview: %v", err)
	}
	afterMR, err := st.ApplyShipmentUpdate(ctx, ShipmentUpdateParams{
		EventID:        "evt-pg-orph-after-mr",
		EventType:      events.SubjectShipmentUpdated,
		OrderID:        "ord-p1-can-deliv",
		TrackingCode:   "TRK-ORPHAN",
		ShipmentStatus: "delivered",
		Now:            time.Now().UTC(),
	})
	if err != nil || afterMR.NeedsOrphanShipmentCancel || afterMR.Order.Order.PendingShipmentCancel {
		t.Fatalf("after manual review flagged, must not re-trigger orphan cancel loop, got %+v err=%v", afterMR, err)
	}

	// 4. Concurrent real-Postgres race: MarkOrderShipmentQueued vs unverified CancelOrder (RequireNoTracking=true).
	for i := 0; i < 6; i++ {
		ordID := fmt.Sprintf("ord-p1-qrace-%d", i)
		seedPostgresOrder(t, st, "buyer-1", "shop-1", ordID, "COD", "CONFIRMED")
		tok, err := st.MarkOrderShipmentCreating(ctx, ordID, time.Now().UTC())
		if err != nil {
			t.Fatalf("iter %d MarkOrderShipmentCreating: %v", i, err)
		}

		start := make(chan struct{})
		var wg sync.WaitGroup
		var qErr, cErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, qErr = st.MarkOrderShipmentQueued(ctx, QueueShipmentParams{
				OrderID:               ordID,
				JobID:                 fmt.Sprintf("job-qrace-%d", i),
				ExpectedCreatingToken: tok,
				ActorID:               "seller-1",
				ActorRole:             "seller",
				Now:                   time.Now().UTC(),
			})
		}()
		go func() {
			defer wg.Done()
			<-start
			_, cErr = st.TransitionOrderStatus(ctx, SingleOrderTransitionParams{
				OrderID:              ordID,
				TargetStatus:         orderv1.OrderStatus_ORDER_STATUS_CANCELLED,
				CancelReason:         "unverified race cancel",
				ActorID:              "buyer-1",
				ActorRole:            "buyer",
				RequireNoTracking:    true,
				VerifyShipmentCancel: true,
				Now:                  time.Now().UTC(),
			})
		}()
		close(start)
		wg.Wait()

		// Because PendingShipmentCancel was set initially and transitions to QUEUED, unverified cancel
		// MUST ALWAYS be rejected with ErrOrderHasTracking — never silently cancelling!
		if !errors.Is(cErr, ErrOrderHasTracking) {
			t.Fatalf("iter %d P1 race violation: unverified cancel must return ErrOrderHasTracking, got cErr=%v qErr=%v", i, cErr, qErr)
		}
		if qErr != nil {
			t.Fatalf("iter %d MarkOrderShipmentQueued unexpected error: %v", i, qErr)
		}
		aggR, _ := st.GetOrderWithDetails(ctx, ordID)
		if aggR.Order.Status != "CONFIRMED" || aggR.Order.ShipmentJobStatus != "QUEUED" {
			t.Fatalf("iter %d expected CONFIRMED/QUEUED, got %s/%q", i, aggR.Order.Status, aggR.Order.ShipmentJobStatus)
		}
	}
}
