package store

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nexusmart/core/services/order/internal/db"
	"github.com/nexusmart/core/services/order/migrations"
)

// TestPostgres_Migration0008_Phase0bUpgradeAllCases executes an end-to-end PostgreSQL schema upgrade
// from a seeded phase-0b database (migrations 0001..0004) through 0005..0021, covering:
//   - Active phase-0b return states: PENDING, APPROVED, RETURNING (with reverse_tracking_code), RETURNED (un-refunded)
//   - REFUNDED full and partial (refund_amount_vnd < total_payable_vnd)
//   - F2: REJECTED + OPEN complaint -> ESCALATED (deadline_at NULL); REJECTED without OPEN complaint -> REJECTED + 3d deadline
//   - F1: Terminal orders (COMPLETED, CANCELLED) and non-return-flow orders (DELIVERED) with non-REFUNDED returns -> CLOSED (deadline_at NULL)
//   - F3: Duplicate return_requests on the same order (one REFUNDED, one REJECTED linked to complaints) -> keeps REFUNDED,
//     preserves ALL complaints rows (no ON DELETE CASCADE loss), creates UNIQUE(order_id) index
//   - Watchdog safety: FindExpiredReturnRequests(+30 days) returns ZERO rows for COMPLETED/CANCELLED/non-return-flow/ESCALATED/REFUNDED orders.
func TestPostgres_Migration0008_Phase0bUpgradeAllCases(t *testing.T) {
	adminDSN := strings.TrimSpace(os.Getenv("TEST_ORDER_DB_URL"))
	if adminDSN == "" {
		t.Skip("TEST_ORDER_DB_URL not set; skipping real Postgres migration 0008 upgrade test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	parsed, err := url.Parse(adminDSN)
	if err != nil {
		t.Fatalf("parse TEST_ORDER_DB_URL: %v", err)
	}

	adminConn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatalf("connect admin postgres: %v", err)
	}

	tempDBName := "order_m0008_test_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	if _, err := adminConn.Exec(ctx, "CREATE DATABASE "+tempDBName); err != nil {
		_ = adminConn.Close(ctx)
		t.Fatalf("create temp database %s: %v", tempDBName, err)
	}
	_ = adminConn.Close(ctx)

	testURL := *parsed
	testURL.Path = "/" + tempDBName
	pool, err := pgxpool.New(ctx, testURL.String())
	if err != nil {
		dropTempDatabase(adminDSN, tempDBName)
		t.Fatalf("open temp db pool %s: %v", tempDBName, err)
	}
	t.Cleanup(func() {
		pool.Close()
		dropTempDatabase(adminDSN, tempDBName)
	})

	// Step 1: Apply ONLY phase-0b migrations 0001..0004.
	applyPhase0bMigrations(t, pool)

	// Step 2: Seed phase-0b checkout_groups, orders, return_requests, and complaints.
	now := time.Now().UTC().Truncate(time.Second)
	seedOrder := func(orderID, orderCode, orderStatus string, totalPayableVND int64) {
		t.Helper()
		grpID := "grp-" + orderID
		if _, err := pool.Exec(ctx, `
			INSERT INTO checkout_groups (
				id, buyer_id, idempotency_key, status, payment_method, total_payable_vnd, created_at, updated_at
			) VALUES ($1, 'buyer-1', $2, 'COMPLETED', 'VIETQR', $3, $4, $4)
		`, grpID, "idem-"+orderID, totalPayableVND, now.Add(-48*time.Hour)); err != nil {
			t.Fatalf("seed checkout_group %s: %v", grpID, err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO orders (
				id, order_code, checkout_group_id, buyer_id, shop_id, status, payment_method, payment_status,
				subtotal_vnd, total_payable_vnd, shipping_address_json, created_at, updated_at
			) VALUES ($1, $2, $3, 'buyer-1', 'shop-1', $4, 'VIETQR', 'SUCCEEDED', $5, $5, '{"recipient_name":"Buyer"}'::jsonb, $6, $6)
		`, orderID, orderCode, grpID, orderStatus, totalPayableVND, now.Add(-48*time.Hour)); err != nil {
			t.Fatalf("seed order %s (%s): %v", orderID, orderStatus, err)
		}
	}

	type seedRRParams struct {
		id              string
		orderID         string
		status          string
		reason          string
		sellerNote      string
		reverseTracking string
		reverseLabel    string
		refundAmountVND int64
		refundID        string
		updatedAt       time.Time
	}
	seedReturn := func(p seedRRParams) {
		t.Helper()
		if p.updatedAt.IsZero() {
			p.updatedAt = now.Add(-24 * time.Hour)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO return_requests (
				id, order_id, buyer_id, shop_id, reason, status, seller_note,
				reverse_tracking_code, reverse_label_url, refund_amount_vnd, refund_id,
				created_at, updated_at
			) VALUES ($1, $2, 'buyer-1', 'shop-1', $3, $4, $5, $6, $7, $8, $9, $10, $11)
		`, p.id, p.orderID, p.reason, p.status, p.sellerNote,
			p.reverseTracking, p.reverseLabel, p.refundAmountVND, p.refundID,
			now.Add(-36*time.Hour), p.updatedAt); err != nil {
			t.Fatalf("seed return_request %s: %v", p.id, err)
		}
	}

	type seedComplaintParams struct {
		id              string
		returnRequestID string
		orderID         string
		reason          string
		status          string
		resolution      string
		adminNote       string
		resolvedBy      string
	}
	seedComplaint := func(p seedComplaintParams) {
		t.Helper()
		if _, err := pool.Exec(ctx, `
			INSERT INTO complaints (
				id, return_request_id, order_id, buyer_id, shop_id, reason, status, resolution, admin_note, resolved_by, created_at
			) VALUES ($1, $2, $3, 'buyer-1', 'shop-1', $4, $5, $6, $7, $8, $9)
		`, p.id, p.returnRequestID, p.orderID, p.reason, p.status, p.resolution, p.adminNote, p.resolvedBy, now.Add(-12*time.Hour)); err != nil {
			t.Fatalf("seed complaint %s: %v", p.id, err)
		}
	}

	// 1. PENDING on RETURN_REQUESTED
	seedOrder("ord-pend", "NM-M8-PEND", "RETURN_REQUESTED", 300000)
	seedReturn(seedRRParams{id: "rr-pend", orderID: "ord-pend", status: "PENDING", reason: "wrong size"})

	// 2. APPROVED on RETURN_APPROVED
	seedOrder("ord-appr", "NM-M8-APPR", "RETURN_APPROVED", 250000)
	seedReturn(seedRRParams{id: "rr-appr", orderID: "ord-appr", status: "APPROVED", reason: "defective", sellerNote: "approved return"})

	// 3. RETURNING with reverse_tracking_code & reverse_label_url on RETURNING
	seedOrder("ord-retg", "NM-M8-RETG", "RETURNING", 350000)
	seedReturn(seedRRParams{
		id: "rr-retg", orderID: "ord-retg", status: "RETURNING", reason: "scratched",
		sellerNote: "pack securely", reverseTracking: "RET-TRK-001", reverseLabel: "https://labels.example/ret-001.pdf",
	})

	// 4. RETURNED un-refunded on RETURNED
	seedOrder("ord-retd", "NM-M8-RETD", "RETURNED", 280000)
	seedReturn(seedRRParams{
		id: "rr-retd", orderID: "ord-retd", status: "RETURNED", reason: "broken button",
		reverseTracking: "RET-TRK-002",
	})

	// 5. REFUNDED full refund on REFUNDED
	seedOrder("ord-ref-full", "NM-M8-REFFULL", "REFUNDED", 500000)
	seedReturn(seedRRParams{
		id: "rr-ref-full", orderID: "ord-ref-full", status: "REFUNDED", reason: "full refund case",
		refundAmountVND: 500000, refundID: "ref-full-1",
	})

	// 6. REFUNDED partial refund (refund_amount_vnd < total_payable_vnd) on REFUNDED
	seedOrder("ord-ref-part", "NM-M8-REFPART", "REFUNDED", 500000)
	seedReturn(seedRRParams{
		id: "rr-ref-part", orderID: "ord-ref-part", status: "REFUNDED", reason: "partial refund case",
		refundAmountVND: 150000, refundID: "ref-part-1",
	})

	// 7. F2 positive: REJECTED + complaint OPEN on RETURN_REJECTED -> ESCALATED, deadline_at NULL
	seedOrder("ord-rej-open", "NM-M8-REJOPEN", "RETURN_REJECTED", 420000)
	seedReturn(seedRRParams{
		id: "rr-rej-open", orderID: "ord-rej-open", status: "REJECTED", reason: "damaged item",
		sellerNote: "packaging looked fine",
	})
	seedComplaint(seedComplaintParams{
		id: "cp-rej-open", returnRequestID: "rr-rej-open", orderID: "ord-rej-open",
		reason: "item arrived cracked", status: "OPEN",
	})

	// 8. F2 negative: REJECTED without OPEN complaint on RETURN_REJECTED -> REJECTED + 3d deadline
	seedOrder("ord-rej-noopen", "NM-M8-REJNOOPEN", "RETURN_REJECTED", 210000)
	seedReturn(seedRRParams{
		id: "rr-rej-noopen", orderID: "ord-rej-noopen", status: "REJECTED", reason: "changed mind",
		sellerNote: "missing original tag",
	})

	// 9. F1: REJECTED + complaint RESOLVED + order COMPLETED -> CLOSED, deadline_at NULL
	seedOrder("ord-rej-comp", "NM-M8-REJCOMP", "COMPLETED", 310000)
	seedReturn(seedRRParams{
		id: "rr-rej-comp", orderID: "ord-rej-comp", status: "REJECTED", reason: "color mismatch",
		sellerNote: "matches listing",
	})
	seedComplaint(seedComplaintParams{
		id: "cp-rej-comp", returnRequestID: "rr-rej-comp", orderID: "ord-rej-comp",
		reason: "buyer disputed color", status: "RESOLVED", resolution: "DISMISS",
		adminNote: "photos match listing", resolvedBy: "admin-1",
	})

	// 10. F1: PENDING + order COMPLETED -> CLOSED, deadline_at NULL
	seedOrder("ord-pend-comp", "NM-M8-PENDCOMP", "COMPLETED", 190000)
	seedReturn(seedRRParams{id: "rr-pend-comp", orderID: "ord-pend-comp", status: "PENDING", reason: "stale pending on completed"})

	// 11. F1 defensive: PENDING on CANCELLED and anomalous RETURNED on DELIVERED -> CLOSED, deadline_at NULL, goods_received FALSE
	seedOrder("ord-canc-pend", "NM-M8-CANCPEND", "CANCELLED", 180000)
	seedReturn(seedRRParams{id: "rr-canc-pend", orderID: "ord-canc-pend", status: "PENDING", reason: "anomalous pending on cancelled"})

	seedOrder("ord-deliv-anom", "NM-M8-DELIVANOM", "DELIVERED", 220000)
	seedReturn(seedRRParams{id: "rr-deliv-anom", orderID: "ord-deliv-anom", status: "RETURNED", reason: "anomalous returned on delivered"})

	// 12. F3: 2 returns on the same order (older REFUNDED + newer REJECTED linked to complaint).
	// Dedup must keep REFUNDED, copy complaint dispute/admin_note, and NOT cascade-delete complaints.
	seedOrder("ord-dup", "NM-M8-DUP", "REFUNDED", 400000)
	seedReturn(seedRRParams{
		id: "rr-dup-ref", orderID: "ord-dup", status: "REFUNDED", reason: "first return refunded",
		refundAmountVND: 400000, refundID: "ref-dup-1", updatedAt: now.Add(-48 * time.Hour),
	})
	seedReturn(seedRRParams{
		id: "rr-dup-rej", orderID: "ord-dup", status: "REJECTED", reason: "duplicate rejected row",
		sellerNote: "dup seller note", updatedAt: now.Add(-1 * time.Hour),
	})
	seedComplaint(seedComplaintParams{
		id: "cp-dup", returnRequestID: "rr-dup-rej", orderID: "ord-dup",
		reason: "buyer escalated duplicate", status: "RESOLVED", resolution: "REFUND",
		adminNote: "approved full refund", resolvedBy: "admin-1",
	})

	// Step 3: Run remaining migrations 0005..0021 via db.ApplyMigrations, then verify idempotency.
	if err := db.ApplyMigrations(ctx, pool); err != nil {
		t.Fatalf("ApplyMigrations (0005..0021): %v", err)
	}
	if err := db.ApplyMigrations(ctx, pool); err != nil {
		t.Fatalf("second ApplyMigrations idempotency check: %v", err)
	}

	// Step 4: Verify every seeded case's migrated return_requests + orders columns.
	type rrRow struct {
		status             string
		deadlineAt         *time.Time
		refundDone         bool
		promoReleased      bool
		restockDone        bool
		goodsReceived      bool
		refundAmountVND    int64
		returnTrackingCode string
		returnLabelURL     string
		sellerResponse     string
		disputeReason      string
		adminNote          string
		orderStatus        string
		orderRefundedVND   int64
	}
	loadCase := func(rrID string) rrRow {
		t.Helper()
		var out rrRow
		if err := pool.QueryRow(ctx, `
			SELECT r.status, r.deadline_at, r.refund_done, r.promo_released, r.restock_done,
			       r.goods_received, r.refund_amount_vnd, r.return_tracking_code, r.return_label_url,
			       r.seller_response, r.dispute_reason, r.admin_note,
			       o.status, o.refunded_vnd
			FROM return_requests r
			JOIN orders o ON o.id = r.order_id
			WHERE r.id = $1
		`, rrID).Scan(
			&out.status, &out.deadlineAt, &out.refundDone, &out.promoReleased, &out.restockDone,
			&out.goodsReceived, &out.refundAmountVND, &out.returnTrackingCode, &out.returnLabelURL,
			&out.sellerResponse, &out.disputeReason, &out.adminNote,
			&out.orderStatus, &out.orderRefundedVND,
		); err != nil {
			t.Fatalf("load migrated case %s: %v", rrID, err)
		}
		return out
	}

	type expectation struct {
		rrID            string
		wantStatus      string
		wantHasDeadline bool
		wantRefundDone  bool
		wantGoodsRecv   bool
		wantRefundVND   int64
		wantOrderStatus string
		wantOrderRefVND int64
		wantTracking    string
		wantLabel       string
		wantSellerResp  string
		wantDispute     string
		wantAdminNote   string
	}
	cases := []expectation{
		{
			rrID: "rr-pend", wantStatus: "PENDING", wantHasDeadline: true,
			wantRefundDone: false, wantGoodsRecv: false, wantRefundVND: 300000,
			wantOrderStatus: "RETURN_REQUESTED", wantOrderRefVND: 0,
		},
		{
			rrID: "rr-appr", wantStatus: "AWAITING_SHIPMENT", wantHasDeadline: true,
			wantRefundDone: false, wantGoodsRecv: false, wantRefundVND: 250000,
			wantOrderStatus: "RETURN_REQUESTED", wantOrderRefVND: 0,
			wantSellerResp: "approved return",
		},
		{
			rrID: "rr-retg", wantStatus: "IN_TRANSIT", wantHasDeadline: true,
			wantRefundDone: false, wantGoodsRecv: false, wantRefundVND: 350000,
			wantOrderStatus: "RETURN_REQUESTED", wantOrderRefVND: 0,
			wantTracking: "RET-TRK-001", wantLabel: "https://labels.example/ret-001.pdf",
			wantSellerResp: "pack securely",
		},
		{
			rrID: "rr-retd", wantStatus: "RECEIVED", wantHasDeadline: true,
			wantRefundDone: false, wantGoodsRecv: true, wantRefundVND: 280000,
			wantOrderStatus: "RETURN_REQUESTED", wantOrderRefVND: 0,
			wantTracking: "RET-TRK-002",
		},
		{
			rrID: "rr-ref-full", wantStatus: "REFUNDED", wantHasDeadline: false,
			wantRefundDone: true, wantGoodsRecv: true, wantRefundVND: 500000,
			wantOrderStatus: "RETURNED", wantOrderRefVND: 500000,
		},
		{
			rrID: "rr-ref-part", wantStatus: "REFUNDED", wantHasDeadline: false,
			wantRefundDone: true, wantGoodsRecv: true, wantRefundVND: 150000,
			wantOrderStatus: "RETURNED", wantOrderRefVND: 150000,
		},
		{
			rrID: "rr-rej-open", wantStatus: "ESCALATED", wantHasDeadline: false,
			wantRefundDone: false, wantGoodsRecv: false, wantRefundVND: 420000,
			wantOrderStatus: "RETURN_REQUESTED", wantOrderRefVND: 0,
			wantSellerResp: "packaging looked fine", wantDispute: "item arrived cracked",
		},
		{
			rrID: "rr-rej-noopen", wantStatus: "REJECTED", wantHasDeadline: true,
			wantRefundDone: false, wantGoodsRecv: false, wantRefundVND: 210000,
			wantOrderStatus: "RETURN_REQUESTED", wantOrderRefVND: 0,
			wantSellerResp: "missing original tag",
		},
		{
			rrID: "rr-rej-comp", wantStatus: "CLOSED", wantHasDeadline: false,
			wantRefundDone: false, wantGoodsRecv: false, wantRefundVND: 310000,
			wantOrderStatus: "COMPLETED", wantOrderRefVND: 0,
			wantSellerResp: "matches listing", wantDispute: "buyer disputed color",
			wantAdminNote: "photos match listing",
		},
		{
			rrID: "rr-pend-comp", wantStatus: "CLOSED", wantHasDeadline: false,
			wantRefundDone: false, wantGoodsRecv: false, wantRefundVND: 190000,
			wantOrderStatus: "COMPLETED", wantOrderRefVND: 0,
		},
		{
			rrID: "rr-canc-pend", wantStatus: "CLOSED", wantHasDeadline: false,
			wantRefundDone: false, wantGoodsRecv: false, wantRefundVND: 180000,
			wantOrderStatus: "CANCELLED", wantOrderRefVND: 0,
		},
		{
			rrID: "rr-deliv-anom", wantStatus: "CLOSED", wantHasDeadline: false,
			wantRefundDone: false, wantGoodsRecv: false, wantRefundVND: 220000,
			wantOrderStatus: "DELIVERED", wantOrderRefVND: 0,
		},
		{
			rrID: "rr-dup-ref", wantStatus: "REFUNDED", wantHasDeadline: false,
			wantRefundDone: true, wantGoodsRecv: true, wantRefundVND: 400000,
			wantOrderStatus: "RETURNED", wantOrderRefVND: 400000,
			wantDispute: "buyer escalated duplicate", wantAdminNote: "approved full refund",
		},
	}

	for _, tc := range cases {
		got := loadCase(tc.rrID)
		if got.status != tc.wantStatus {
			t.Errorf("%s: status = %q, want %q", tc.rrID, got.status, tc.wantStatus)
		}
		if (got.deadlineAt != nil) != tc.wantHasDeadline {
			t.Errorf("%s: deadline_at = %v, wantHasDeadline=%v", tc.rrID, got.deadlineAt, tc.wantHasDeadline)
		}
		if got.refundDone != tc.wantRefundDone || got.promoReleased != tc.wantRefundDone || got.restockDone != tc.wantRefundDone {
			t.Errorf("%s: refund_done=%v promo_released=%v restock_done=%v, want all %v",
				tc.rrID, got.refundDone, got.promoReleased, got.restockDone, tc.wantRefundDone)
		}
		if got.goodsReceived != tc.wantGoodsRecv {
			t.Errorf("%s: goods_received = %v, want %v", tc.rrID, got.goodsReceived, tc.wantGoodsRecv)
		}
		if got.refundAmountVND != tc.wantRefundVND {
			t.Errorf("%s: refund_amount_vnd = %d, want %d", tc.rrID, got.refundAmountVND, tc.wantRefundVND)
		}
		if got.orderStatus != tc.wantOrderStatus {
			t.Errorf("%s: orders.status = %q, want %q", tc.rrID, got.orderStatus, tc.wantOrderStatus)
		}
		if got.orderRefundedVND != tc.wantOrderRefVND {
			t.Errorf("%s: orders.refunded_vnd = %d, want %d", tc.rrID, got.orderRefundedVND, tc.wantOrderRefVND)
		}
		if tc.wantTracking != "" && got.returnTrackingCode != tc.wantTracking {
			t.Errorf("%s: return_tracking_code = %q, want %q", tc.rrID, got.returnTrackingCode, tc.wantTracking)
		}
		if tc.wantLabel != "" && got.returnLabelURL != tc.wantLabel {
			t.Errorf("%s: return_label_url = %q, want %q", tc.rrID, got.returnLabelURL, tc.wantLabel)
		}
		if tc.wantSellerResp != "" && got.sellerResponse != tc.wantSellerResp {
			t.Errorf("%s: seller_response = %q, want %q", tc.rrID, got.sellerResponse, tc.wantSellerResp)
		}
		if tc.wantDispute != "" && got.disputeReason != tc.wantDispute {
			t.Errorf("%s: dispute_reason = %q, want %q", tc.rrID, got.disputeReason, tc.wantDispute)
		}
		if tc.wantAdminNote != "" && got.adminNote != tc.wantAdminNote {
			t.Errorf("%s: admin_note = %q, want %q", tc.rrID, got.adminNote, tc.wantAdminNote)
		}
	}

	// Step 5: Verify dedup removed rr-dup-rej while preserving ALL 3 rows in complaints (F3).
	var dupRejExists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM return_requests WHERE id = 'rr-dup-rej')`).Scan(&dupRejExists); err != nil {
		t.Fatalf("check rr-dup-rej deleted: %v", err)
	}
	if dupRejExists {
		t.Fatalf("expected duplicate return_request rr-dup-rej to be deleted by dedup")
	}

	var complaintCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM complaints`).Scan(&complaintCount); err != nil {
		t.Fatalf("count complaints: %v", err)
	}
	if complaintCount != 3 {
		t.Fatalf("complaints count = %d, want 3 (dedup MUST NOT cascade-delete complaints)", complaintCount)
	}

	var cpDupExists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM complaints WHERE id = 'cp-dup' AND return_request_id = 'rr-dup-rej')`).Scan(&cpDupExists); err != nil {
		t.Fatalf("check cp-dup preserved: %v", err)
	}
	if !cpDupExists {
		t.Fatalf("expected complaint cp-dup linked to deduplicated rr-dup-rej to remain intact in complaints")
	}

	// Step 6: Verify UNIQUE(order_id) index exists and rejects duplicate inserts.
	var indexDef string
	if err := pool.QueryRow(ctx, `
		SELECT indexdef FROM pg_indexes
		WHERE tablename = 'return_requests' AND indexname = 'idx_return_requests_order_id_unique'
	`).Scan(&indexDef); err != nil {
		t.Fatalf("lookup idx_return_requests_order_id_unique: %v", err)
	}
	if !strings.Contains(strings.ToUpper(indexDef), "UNIQUE INDEX") {
		t.Fatalf("idx_return_requests_order_id_unique is not UNIQUE: %s", indexDef)
	}
	_, dupInsertErr := pool.Exec(ctx, `
		INSERT INTO return_requests (id, order_id, buyer_id, reason, status)
		VALUES ('rr-dup-insert-check', 'ord-pend', 'buyer-1', 'must fail unique', 'PENDING')
	`)
	var pgErr *pgconn.PgError
	if dupInsertErr == nil || !errors.As(dupInsertErr, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("expected SQLSTATE 23505 unique violation on duplicate return_requests(order_id), got: %v", dupInsertErr)
	}

	// Step 7: Verify FindExpiredReturnRequests(+30 days) NEVER returns terminal/non-return-flow/ESCALATED/REFUNDED rows,
	// while still returning active timed returns.
	q := db.New(pool)
	expiredIDs, err := q.FindExpiredReturnRequests(ctx, db.FindExpiredReturnRequestsParams{
		Now:        time.Now().UTC().Add(30 * 24 * time.Hour),
		BatchLimit: 100,
	})
	if err != nil {
		t.Fatalf("FindExpiredReturnRequests(+30d): %v", err)
	}
	expiredSet := make(map[string]bool, len(expiredIDs))
	for _, id := range expiredIDs {
		expiredSet[id] = true
	}

	for _, forbidden := range []string{
		"rr-rej-comp",   // COMPLETED order
		"rr-pend-comp",  // COMPLETED order
		"rr-canc-pend",  // CANCELLED order
		"rr-deliv-anom", // non-return-flow DELIVERED order
		"rr-rej-open",   // ESCALATED (OPEN complaint)
		"rr-ref-full",   // REFUNDED
		"rr-ref-part",   // REFUNDED partial
		"rr-dup-ref",    // REFUNDED surviving duplicate
	} {
		if expiredSet[forbidden] {
			t.Errorf("FindExpiredReturnRequests(+30d) MUST NOT return %s (expired=%v)", forbidden, expiredIDs)
		}
	}
	for _, wantActive := range []string{
		"rr-pend",
		"rr-appr",
		"rr-retg",
		"rr-retd",
		"rr-rej-noopen",
	} {
		if !expiredSet[wantActive] {
			t.Errorf("FindExpiredReturnRequests(+30d) expected active timed return %s to be eligible (expired=%v)", wantActive, expiredIDs)
		}
	}
}

func applyPhase0bMigrations(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			filename TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`); err != nil {
		t.Fatalf("ensure schema_migrations: %v", err)
	}
	phase0bFiles := []string{
		"0001_init.sql",
		"0002_sales_channel.sql",
		"0003_orders_storefront_index.sql",
		"0004_returns_and_complaints.sql",
	}
	for _, name := range phase0bFiles {
		sqlBytes, err := migrations.Files.ReadFile(name)
		if err != nil {
			t.Fatalf("read phase-0b migration %s: %v", name, err)
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin tx for %s: %v", name, err)
		}
		if _, err := tx.Exec(ctx, string(sqlBytes)); err != nil {
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
}
