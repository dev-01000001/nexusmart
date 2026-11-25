package db

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/nexusmart/core/services/order/migrations"
)

type fakeRow struct {
	scanErr error
}

func (r fakeRow) Scan(_ ...any) error { return r.scanErr }

type fakeRows struct {
	remaining int
	scanErr   error
	rowsErr   error
}

func (r *fakeRows) Close()                                       {}
func (r *fakeRows) Err() error                                   { return r.rowsErr }
func (r *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.NewCommandTag("SELECT 1") }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) Next() bool {
	if r.remaining > 0 {
		r.remaining--
		return true
	}
	return false
}
func (r *fakeRows) Scan(_ ...any) error    { return r.scanErr }
func (r *fakeRows) Values() ([]any, error) { return nil, nil }
func (r *fakeRows) RawValues() [][]byte    { return nil }
func (r *fakeRows) Conn() *pgx.Conn        { return nil }

type mockDBTX struct {
	lastSQL  string
	lastArgs []any
	execErr  error
	queryErr error
	scanErr  error
	rowsErr  error
	numRows  int
}

func (m *mockDBTX) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	m.lastSQL = sql
	m.lastArgs = args
	return pgconn.NewCommandTag("INSERT 0 1"), m.execErr
}

func (m *mockDBTX) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	m.lastSQL = sql
	m.lastArgs = args
	if m.queryErr != nil {
		return nil, m.queryErr
	}
	return &fakeRows{remaining: m.numRows, scanErr: m.scanErr, rowsErr: m.rowsErr}, nil
}

func (m *mockDBTX) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	m.lastSQL = sql
	m.lastArgs = args
	return fakeRow{scanErr: m.scanErr}
}

func TestMigration0004_SchemaContract(t *testing.T) {
	raw, err := migrations.Files.ReadFile("0004_returns_and_complaints.sql")
	if err != nil {
		t.Fatalf("read 0004_returns_and_complaints.sql: %v", err)
	}
	sqlText := string(raw)
	for _, want := range []string{
		"ALTER TABLE return_requests",
		"shop_id TEXT NOT NULL DEFAULT ''",
		"items_json JSONB NOT NULL DEFAULT '[]'::jsonb",
		"seller_note TEXT NOT NULL DEFAULT ''",
		"reverse_tracking_code TEXT NOT NULL DEFAULT ''",
		"reverse_label_url TEXT NOT NULL DEFAULT ''",
		"refund_amount_vnd BIGINT NOT NULL DEFAULT 0",
		"refund_id TEXT NOT NULL DEFAULT ''",
		"CREATE TABLE IF NOT EXISTS complaints",
		"return_request_id TEXT NOT NULL UNIQUE REFERENCES return_requests(id) ON DELETE CASCADE",
		"idx_orders_pending_online_expiry",
		"payment_method IN ('VIETQR', 'ZALOPAY')",
	} {
		if !strings.Contains(sqlText, want) {
			t.Fatalf("0004_returns_and_complaints.sql missing %q", want)
		}
	}
}

// TestMigrations_NumberingAndPhase0bPreserved verifies sequential 0001..0021 numbering with
// zero prefix collisions and the exact 4 phase-0b migrations leading the sequence.
func TestMigrations_NumberingAndPhase0bPreserved(t *testing.T) {
	entries, err := fs.ReadDir(migrations.Files, ".")
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	var sqlFiles []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			sqlFiles = append(sqlFiles, e.Name())
		}
	}
	sort.Strings(sqlFiles)

	wantFiles := []string{
		"0001_init.sql",
		"0002_sales_channel.sql",
		"0003_orders_storefront_index.sql",
		"0004_returns_and_complaints.sql",
		"0005_checkout_groups_in_progress_idx.sql",
		"0006_checkout_request_hash_and_refunded_vnd.sql",
		"0007_retry_idempotency.sql",
		"0008_returns_and_lifecycle.sql",
		"0009_w2a_review_fixes.sql",
		"0010_w2a_indep_review_fixes.sql",
		"0011_w2a_review2_fixes.sql",
		"0012_pending_shipment_cancel_ttl.sql",
		"0013_w2b_order.sql",
		"0014_w2b_order_round4.sql",
		"0015_w2b_order_round5.sql",
		"0016_w2b_order_round6.sql",
		"0017_outbox_lease.sql",
		"0018_cod_rate_index_and_ship_job.sql",
		"0019_online_methods.sql",
		"0020_return_refund_approval.sql",
		"0021_w3b3_address_and_filters.sql",
	}
	if len(sqlFiles) != len(wantFiles) {
		t.Fatalf("migration count = %d, want %d; files=%v", len(sqlFiles), len(wantFiles), sqlFiles)
	}
	for i, want := range wantFiles {
		prefix := fmt.Sprintf("%04d_", i+1)
		if !strings.HasPrefix(sqlFiles[i], prefix) || sqlFiles[i] != want {
			t.Fatalf("migration[%d] = %q, want %q", i, sqlFiles[i], want)
		}
	}
}

// TestMigration0008_ReturnsAndLifecycle_StatusMappingAndAntiDoubleRefund statically verifies
// every unification invariant required when migrating a live phase-0b DB (0001..0004) into main:
//   - All 7 phase-0b return_requests statuses mapped explicitly, strictly NO ELSE 'CLOSED'.
//   - Partial refund_amount_vnd > 0 preserved rather than overwritten.
//   - seller_note / reverse_tracking_code / reverse_label_url copied into main columns.
//   - Anti-double-refund: REFUNDED rows get refund_done/promo_released/restock_done/goods_received=TRUE,
//     orders.refunded_vnd is synced via GREATEST, and dedup keeps REFUNDED rows first.
//   - complaints history copied into return_requests BEFORE dedup and UNIQUE(order_id) index, table never dropped.
//   - All 4 phase-0b orders.status values (plus RETURNED+RECEIVED) normalized BEFORE confirmed_at backfill.
//   - CHECK constraints enforced via DO $$ (not silently skipped by CREATE TABLE IF NOT EXISTS).
func TestMigration0008_ReturnsAndLifecycle_StatusMappingAndAntiDoubleRefund(t *testing.T) {
	raw, err := migrations.Files.ReadFile("0008_returns_and_lifecycle.sql")
	if err != nil {
		t.Fatalf("read 0008_returns_and_lifecycle.sql: %v", err)
	}
	sqlText := string(raw)
	upper := strings.ToUpper(sqlText)

	// 1. Strictly NO ELSE 'CLOSED' catch-all (would silently close valid phase-0b returns).
	if strings.Contains(upper, "ELSE 'CLOSED'") || strings.Contains(upper, "ELSE'CLOSED'") {
		t.Fatalf("0008_returns_and_lifecycle.sql MUST NOT contain ELSE 'CLOSED'")
	}

	// 2. Never drop legacy complaints history table.
	if strings.Contains(upper, "DROP TABLE") && strings.Contains(upper, "COMPLAINTS") {
		t.Fatalf("0008_returns_and_lifecycle.sql MUST NOT drop legacy complaints table")
	}

	// 3. Explicit mapping for all 7 phase-0b return_requests statuses + F1/F2 guards + preservation of main statuses.
	for _, want := range []string{
		"WHEN r.status = 'REFUNDED' OR o.status = 'REFUNDED' THEN 'REFUNDED'",
		"WHEN o.status IN ('COMPLETED', 'CANCELLED') THEN 'CLOSED'",
		"WHEN o.status NOT IN ('RETURN_REQUESTED', 'RETURN_APPROVED', 'RETURN_REJECTED', 'RETURNING', 'RETURNED', 'REFUNDED') THEN 'CLOSED'",
		"WHEN (r.status = 'REJECTED' OR (r.status = 'PENDING' AND o.status = 'RETURN_REJECTED'))",
		"AND c.status = 'OPEN'",
		") THEN 'ESCALATED'",
		"WHEN r.status = 'APPROVED' THEN 'AWAITING_SHIPMENT'",
		"WHEN r.status = 'RETURNING' THEN 'IN_TRANSIT'",
		"WHEN r.status = 'RETURNED' THEN 'RECEIVED'",
		"WHEN r.status = 'REJECTED' THEN 'REJECTED'",
		"WHEN r.status = 'ESCALATED' THEN 'ESCALATED'",
		"WHEN r.status = 'PENDING' THEN 'PENDING'",
		"ELSE r.status",
	} {
		if !strings.Contains(sqlText, want) {
			t.Fatalf("0008_returns_and_lifecycle.sql missing return status mapping clause %q", want)
		}
	}

	// 3b. F1/F2 branch ordering inside status = CASE and deadline_at = CASE:
	//     REFUNDED < COMPLETED/CANCELLED < non-return-flow < OPEN-complaint ESCALATED < active branches (APPROVED/REJECTED/PENDING).
	statusCaseStart := strings.Index(sqlText, "status = CASE")
	goodsCaseStart := strings.Index(sqlText, "goods_received = CASE")
	deadlineCaseStart := strings.Index(sqlText, "deadline_at = CASE")
	deadlineCaseEnd := strings.Index(sqlText, "updated_at = NOW()\nFROM orders o")
	if statusCaseStart < 0 || goodsCaseStart < 0 || deadlineCaseStart < 0 || deadlineCaseEnd < 0 ||
		!(statusCaseStart < goodsCaseStart && goodsCaseStart < deadlineCaseStart && deadlineCaseStart < deadlineCaseEnd) {
		t.Fatalf("0008 unexpected CASE block layout: status=%d goods=%d deadline=%d..%d",
			statusCaseStart, goodsCaseStart, deadlineCaseStart, deadlineCaseEnd)
	}
	statusCase := sqlText[statusCaseStart:goodsCaseStart]
	goodsCase := sqlText[goodsCaseStart:deadlineCaseStart]
	deadlineCase := sqlText[deadlineCaseStart:deadlineCaseEnd]

	assertOrderedClauses := func(label, block string, clauses []string) {
		t.Helper()
		offset := 0
		for _, clause := range clauses {
			rel := strings.Index(block[offset:], clause)
			if rel < 0 {
				t.Fatalf("0008 %s missing clause %q after offset %d", label, clause, offset)
			}
			offset += rel + len(clause)
		}
	}
	assertOrderedClauses("status CASE", statusCase, []string{
		"WHEN r.status = 'REFUNDED' OR o.status = 'REFUNDED' THEN 'REFUNDED'",
		"WHEN o.status IN ('COMPLETED', 'CANCELLED') THEN 'CLOSED'",
		"WHEN o.status NOT IN ('RETURN_REQUESTED', 'RETURN_APPROVED', 'RETURN_REJECTED', 'RETURNING', 'RETURNED', 'REFUNDED') THEN 'CLOSED'",
		"AND c.status = 'OPEN'",
		") THEN 'ESCALATED'",
		"WHEN r.status = 'APPROVED' THEN 'AWAITING_SHIPMENT'",
		"WHEN r.status = 'RETURNING' THEN 'IN_TRANSIT'",
		"WHEN r.status = 'RETURNED' THEN 'RECEIVED'",
		"WHEN r.status = 'REJECTED' THEN 'REJECTED'",
		"WHEN r.status = 'ESCALATED' THEN 'ESCALATED'",
		"WHEN r.status = 'PENDING' THEN 'PENDING'",
	})
	assertOrderedClauses("goods_received CASE", goodsCase, []string{
		"WHEN r.status = 'REFUNDED' OR o.status = 'REFUNDED' THEN TRUE",
		"WHEN o.status IN ('COMPLETED', 'CANCELLED') THEN r.goods_received",
		"WHEN o.status NOT IN ('RETURN_REQUESTED', 'RETURN_APPROVED', 'RETURN_REJECTED', 'RETURNING', 'RETURNED', 'REFUNDED') THEN r.goods_received",
		"WHEN r.status = 'RETURNED' OR o.status = 'RETURNED' THEN TRUE",
	})
	assertOrderedClauses("deadline_at CASE", deadlineCase, []string{
		"WHEN r.status = 'REFUNDED' OR o.status = 'REFUNDED' OR (r.status = 'PENDING' AND o.status = 'RETURNED') THEN NULL",
		"WHEN o.status IN ('COMPLETED', 'CANCELLED') THEN NULL",
		"WHEN o.status NOT IN ('RETURN_REQUESTED', 'RETURN_APPROVED', 'RETURN_REJECTED', 'RETURNING', 'RETURNED', 'REFUNDED') THEN NULL",
		"AND c.status = 'OPEN'",
		") THEN NULL",
		"WHEN r.status = 'APPROVED' OR (r.status = 'PENDING' AND o.status = 'RETURN_APPROVED') THEN COALESCE(r.deadline_at, NOW() + INTERVAL '5 days')",
		"WHEN r.status = 'REJECTED' OR (r.status = 'PENDING' AND o.status = 'RETURN_REJECTED') THEN COALESCE(r.deadline_at, NOW() + INTERVAL '3 days')",
		"WHEN r.status = 'PENDING' THEN COALESCE(r.deadline_at, NOW() + INTERVAL '3 days')",
	})

	// 4. Preserve partial refund_amount_vnd > 0 + copy seller_note/reverse_tracking_code/reverse_label_url.
	for _, want := range []string{
		"WHEN r.refund_amount_vnd > 0 THEN r.refund_amount_vnd",
		"seller_response = CASE WHEN r.seller_response = '' THEN r.seller_note ELSE r.seller_response END",
		"return_tracking_code = CASE WHEN r.return_tracking_code = '' THEN r.reverse_tracking_code ELSE r.return_tracking_code END",
		"return_label_url = CASE WHEN r.return_label_url = '' THEN r.reverse_label_url ELSE r.return_label_url END",
	} {
		if !strings.Contains(sqlText, want) {
			t.Fatalf("0008_returns_and_lifecycle.sql missing data preservation clause %q", want)
		}
	}

	// 5. Anti-double-refund flags + orders.refunded_vnd sync + REFUNDED priority during dedup.
	for _, want := range []string{
		"WHEN r.status = 'REFUNDED' OR o.status = 'REFUNDED' THEN TRUE",
		"WHEN r.status = 'RETURNED' OR o.status = 'RETURNED' THEN TRUE",
		"refund_done = CASE",
		"promo_released = CASE",
		"restock_done = CASE",
		"WHEN r.status = 'REFUNDED' OR o.status = 'REFUNDED' OR (r.status = 'PENDING' AND o.status = 'RETURNED') THEN TRUE",
		"refunded_vnd = GREATEST(o.refunded_vnd, r.refund_amount_vnd)",
		"refunded_vnd = GREATEST(o.refunded_vnd, o.total_payable_vnd)",
		"(status = 'REFUNDED') DESC",
	} {
		if !strings.Contains(sqlText, want) {
			t.Fatalf("0008_returns_and_lifecycle.sql missing anti-double-refund clause %q", want)
		}
	}

	// 6. Normalize all 4 phase-0b orders.status values + RETURNED with un-refunded RECEIVED return.
	for _, want := range []string{
		"WHEN o.status IN ('RETURN_APPROVED', 'RETURN_REJECTED', 'RETURNING') THEN 'RETURN_REQUESTED'",
		"WHEN o.status = 'REFUNDED' THEN 'RETURNED'",
		"WHEN o.status = 'RETURNED' AND EXISTS (",
		"WHERE r.order_id = o.id AND r.status = 'RECEIVED'",
		") THEN 'RETURN_REQUESTED'",
	} {
		if !strings.Contains(sqlText, want) {
			t.Fatalf("0008_returns_and_lifecycle.sql missing orders.status normalization clause %q", want)
		}
	}

	// 7. CHECK constraints enforced via DO $$ / pg_constraint.
	for _, want := range []string{
		"chk_return_requests_refund_amount_nonneg",
		"CHECK (refund_amount_vnd >= 0)",
		"chk_return_requests_return_fee_nonneg",
		"CHECK (return_fee_vnd >= 0)",
		"FROM pg_constraint",
	} {
		if !strings.Contains(sqlText, want) {
			t.Fatalf("0008_returns_and_lifecycle.sql missing CHECK constraint clause %q", want)
		}
	}

	// 8. Strict execution ordering invariants:
	//    (a) copy complaints -> F3 DROP CONSTRAINT complaints_return_request_id_fkey -> dedup DELETE -> CREATE UNIQUE INDEX idx_return_requests_order_id_unique
	//    (b) normalize orders.status -> backfill orders.confirmed_at
	complaintsIdx := strings.Index(sqlText, "FROM complaints c")
	dropFKIdx := strings.Index(sqlText, "ALTER TABLE complaints DROP CONSTRAINT IF EXISTS complaints_return_request_id_fkey;")
	dedupIdx := strings.Index(sqlText, "DELETE FROM return_requests")
	uniqueIdx := strings.Index(sqlText, "idx_return_requests_order_id_unique")
	if complaintsIdx < 0 || dropFKIdx < 0 || dedupIdx < 0 || uniqueIdx < 0 {
		t.Fatalf("0008 missing expected statements: complaints=%d dropFK=%d dedup=%d unique=%d",
			complaintsIdx, dropFKIdx, dedupIdx, uniqueIdx)
	}
	if !(complaintsIdx < dropFKIdx && dropFKIdx < dedupIdx && dedupIdx < uniqueIdx) {
		t.Fatalf("0008 ordering violated: must copy complaints (%d) BEFORE drop complaints FK (%d) BEFORE dedup (%d) BEFORE unique index (%d)",
			complaintsIdx, dropFKIdx, dedupIdx, uniqueIdx)
	}

	normOrdersIdx := strings.Index(sqlText, "WHEN o.status IN ('RETURN_APPROVED', 'RETURN_REJECTED', 'RETURNING')")
	confirmedAtIdx := strings.Index(sqlText, "SET confirmed_at = COALESCE")
	if normOrdersIdx < 0 || confirmedAtIdx < 0 {
		t.Fatalf("0008 missing expected statements: normOrders=%d confirmedAt=%d", normOrdersIdx, confirmedAtIdx)
	}
	if normOrdersIdx >= confirmedAtIdx {
		t.Fatalf("0008 ordering violated: must normalize orders.status (%d) BEFORE confirmed_at backfill (%d)",
			normOrdersIdx, confirmedAtIdx)
	}
}

// TestMigration0010And0019_Fixes verifies the fixes noted in a-order.md for migrations 0010 and 0019:
//   - 0010 delivered_at backfill covers all return statuses defensively.
//   - 0019 drops duplicate phase-0b idx_orders_pending_online_expiry and creates idx_orders_pending_expiry with IF NOT EXISTS.
func TestMigration0010And0019_Fixes(t *testing.T) {
	m0010, err := migrations.Files.ReadFile("0010_w2a_indep_review_fixes.sql")
	if err != nil {
		t.Fatalf("read 0010_w2a_indep_review_fixes.sql: %v", err)
	}
	s0010 := string(m0010)
	for _, want := range []string{
		"'DELIVERED'", "'COMPLETED'", "'RETURN_REQUESTED'", "'RETURNED'",
		"'RETURN_APPROVED'", "'RETURN_REJECTED'", "'RETURNING'", "'REFUNDED'",
	} {
		if !strings.Contains(s0010, want) {
			t.Fatalf("0010_w2a_indep_review_fixes.sql missing %s in delivered_at backfill", want)
		}
	}

	m0019, err := migrations.Files.ReadFile("0019_online_methods.sql")
	if err != nil {
		t.Fatalf("read 0019_online_methods.sql: %v", err)
	}
	s0019 := string(m0019)
	for _, want := range []string{
		"DROP INDEX IF EXISTS idx_orders_pending_online_expiry;",
		"DROP INDEX IF EXISTS idx_orders_pending_expiry;",
		"CREATE INDEX IF NOT EXISTS idx_orders_pending_expiry",
		"payment_method IN ('VIETQR', 'ZALOPAY')",
	} {
		if !strings.Contains(s0019, want) {
			t.Fatalf("0019_online_methods.sql missing %q", want)
		}
	}
}

func TestQueries_SQLContractsAndRowScanning(t *testing.T) {
	ctx := context.Background()
	m := &mockDBTX{numRows: 1}
	q := New(m)
	_ = q.WithTx(nil)

	// Exec queries.
	if err := q.FailCheckoutGroup(ctx, FailCheckoutGroupParams{ID: "g1"}); err != nil {
		t.Fatalf("FailCheckoutGroup: %v", err)
	}
	if err := q.InsertDeadLetterEvent(ctx, InsertDeadLetterEventParams{ID: "d1"}); err != nil {
		t.Fatalf("InsertDeadLetterEvent: %v", err)
	}
	if err := q.InsertOutboxEvent(ctx, InsertOutboxEventParams{ID: "o1"}); err != nil {
		t.Fatalf("InsertOutboxEvent: %v", err)
	}
	if _, err := q.MarkOutboxEventAttemptFailed(ctx, MarkOutboxEventAttemptFailedParams{ID: "o1"}); err != nil {
		t.Fatalf("MarkOutboxEventAttemptFailed: %v", err)
	}
	if _, err := q.MarkOutboxEventPublished(ctx, MarkOutboxEventPublishedParams{ID: "o1"}); err != nil {
		t.Fatalf("MarkOutboxEventPublished: %v", err)
	}
	if n, err := q.TryMarkEventProcessed(ctx, TryMarkEventProcessedParams{EventID: "e1"}); err != nil || n != 1 {
		t.Fatalf("TryMarkEventProcessed: n=%d err=%v", n, err)
	}

	// Single-row queries including sales_channel / storefront_shop_id contracts.
	if _, err := q.CreateCheckoutGroup(ctx, CreateCheckoutGroupParams{ID: "g1", SalesChannel: "STOREFRONT", StorefrontShopID: "s1"}); err != nil ||
		!strings.Contains(m.lastSQL, "sales_channel") || !strings.Contains(m.lastSQL, "storefront_shop_id") {
		t.Fatalf("CreateCheckoutGroup: err=%v sql=%s", err, m.lastSQL)
	}
	if _, err := q.InsertOrder(ctx, InsertOrderParams{ID: "o1", SalesChannel: "STOREFRONT", StorefrontShopID: "s1"}); err != nil ||
		!strings.Contains(m.lastSQL, "sales_channel") || !strings.Contains(m.lastSQL, "storefront_shop_id") {
		t.Fatalf("InsertOrder: err=%v sql=%s", err, m.lastSQL)
	}
	if _, err := q.CompleteCheckoutGroup(ctx, CompleteCheckoutGroupParams{ID: "g1"}); err != nil {
		t.Fatalf("CompleteCheckoutGroup: %v", err)
	}
	if _, err := q.GetCheckoutGroupForIdempotency(ctx, GetCheckoutGroupForIdempotencyParams{}); err != nil {
		t.Fatalf("GetCheckoutGroupForIdempotency: %v", err)
	}
	if _, err := q.GetCheckoutGroupByID(ctx, "g1"); err != nil {
		t.Fatalf("GetCheckoutGroupByID: %v", err)
	}
	if _, err := q.GetOrderByID(ctx, "o1"); err != nil {
		t.Fatalf("GetOrderByID: %v", err)
	}
	if _, err := q.GetOrderByIDForUpdate(ctx, "o1"); err != nil {
		t.Fatalf("GetOrderByIDForUpdate: %v", err)
	}
	if _, err := q.GetReturnRequestByID(ctx, "rr1"); err != nil {
		t.Fatalf("GetReturnRequestByID: %v", err)
	}
	if _, err := q.GetReturnRequestByOrderID(ctx, "o1"); err != nil {
		t.Fatalf("GetReturnRequestByOrderID: %v", err)
	}
	if _, err := q.InsertOrderItem(ctx, InsertOrderItemParams{ID: "oi1"}); err != nil {
		t.Fatalf("InsertOrderItem: %v", err)
	}
	if _, err := q.InsertReturnRequest(ctx, InsertReturnRequestParams{ID: "rr1", ShopID: "s1", RefundAmountVND: 1000}); err != nil {
		t.Fatalf("InsertReturnRequest: %v", err)
	}
	if _, err := q.InsertTimelineEvent(ctx, InsertTimelineEventParams{ID: "tl1"}); err != nil {
		t.Fatalf("InsertTimelineEvent: %v", err)
	}
	if _, err := q.IsEventProcessed(ctx, "e1"); err != nil {
		t.Fatalf("IsEventProcessed: %v", err)
	}
	if _, err := q.CountOrdersFiltered(ctx, CountOrdersFilteredParams{SalesChannel: "STOREFRONT", StorefrontShopID: "s1"}); err != nil ||
		!strings.Contains(m.lastSQL, "sales_channel") || !strings.Contains(m.lastSQL, "storefront_shop_id") {
		t.Fatalf("CountOrdersFiltered: err=%v sql=%s", err, m.lastSQL)
	}
	if _, err := q.CountReturnRequestsFiltered(ctx, CountReturnRequestsFilteredParams{}); err != nil {
		t.Fatalf("CountReturnRequestsFiltered: %v", err)
	}

	// Multi-row queries + verify FindExpiredPendingOrders supports both VIETQR and ZALOPAY.
	if rows, err := q.ListOrderItemsByOrderID(ctx, "o1"); err != nil || len(rows) != 1 {
		t.Fatalf("ListOrderItemsByOrderID: len=%d err=%v", len(rows), err)
	}
	if rows, err := q.ListOrderTimelineByOrderID(ctx, "o1"); err != nil || len(rows) != 1 {
		t.Fatalf("ListOrderTimelineByOrderID: len=%d err=%v", len(rows), err)
	}
	if rows, err := q.ListOrdersByCheckoutGroup(ctx, "g1"); err != nil || len(rows) != 1 {
		t.Fatalf("ListOrdersByCheckoutGroup: len=%d err=%v", len(rows), err)
	}
	if rows, err := q.ListOrdersByCheckoutGroupForUpdate(ctx, "g1"); err != nil || len(rows) != 1 {
		t.Fatalf("ListOrdersByCheckoutGroupForUpdate: len=%d err=%v", len(rows), err)
	}
	if rows, err := q.ListOrdersFiltered(ctx, ListOrdersFilteredParams{SalesChannel: "STOREFRONT", StorefrontShopID: "s1"}); err != nil || len(rows) != 1 ||
		!strings.Contains(m.lastSQL, "sales_channel") || !strings.Contains(m.lastSQL, "storefront_shop_id") {
		t.Fatalf("ListOrdersFiltered: len=%d err=%v sql=%s", len(rows), err, m.lastSQL)
	}
	if rows, err := q.CountOrdersByStatus(ctx, CountOrdersByStatusParams{SalesChannel: "STOREFRONT", StorefrontShopID: "s1"}); err != nil || len(rows) != 1 ||
		!strings.Contains(m.lastSQL, "sales_channel") || !strings.Contains(m.lastSQL, "storefront_shop_id") {
		t.Fatalf("CountOrdersByStatus: len=%d err=%v sql=%s", len(rows), err, m.lastSQL)
	}
	if rows, err := q.ListReturnRequestsFiltered(ctx, ListReturnRequestsFilteredParams{}); err != nil || len(rows) != 1 {
		t.Fatalf("ListReturnRequestsFiltered: len=%d err=%v", len(rows), err)
	}
	if rows, err := q.ClaimPendingOutboxEvents(ctx, ClaimPendingOutboxEventsParams{BatchLimit: 10}); err != nil || len(rows) != 1 {
		t.Fatalf("ClaimPendingOutboxEvents: len=%d err=%v", len(rows), err)
	}
	if rows, err := q.FindExpiredPendingOrders(ctx, 10); err != nil || len(rows) != 1 || !strings.Contains(m.lastSQL, "IN ('VIETQR', 'ZALOPAY')") {
		t.Fatalf("FindExpiredPendingOrders: len=%d err=%v sql=%s", len(rows), err, m.lastSQL)
	}

	// Verify Query, Scan, and Rows.Err error propagation on multi-row queries.
	errBoom := errors.New("db failure")
	for _, tc := range []struct {
		name     string
		queryErr error
		scanErr  error
		rowsErr  error
	}{
		{"queryErr", errBoom, nil, nil},
		{"scanErr", nil, errBoom, nil},
		{"rowsErr", nil, nil, errBoom},
	} {
		em := &mockDBTX{numRows: 1, queryErr: tc.queryErr, scanErr: tc.scanErr, rowsErr: tc.rowsErr}
		eq := New(em)
		if _, err := eq.ListOrderItemsByOrderID(ctx, "o1"); !errors.Is(err, errBoom) {
			t.Fatalf("%s ListOrderItemsByOrderID: got %v", tc.name, err)
		}
		if _, err := eq.ListOrderTimelineByOrderID(ctx, "o1"); !errors.Is(err, errBoom) {
			t.Fatalf("%s ListOrderTimelineByOrderID: got %v", tc.name, err)
		}
		if _, err := eq.ListOrdersByCheckoutGroup(ctx, "g1"); !errors.Is(err, errBoom) {
			t.Fatalf("%s ListOrdersByCheckoutGroup: got %v", tc.name, err)
		}
		if _, err := eq.ListOrdersByCheckoutGroupForUpdate(ctx, "g1"); !errors.Is(err, errBoom) {
			t.Fatalf("%s ListOrdersByCheckoutGroupForUpdate: got %v", tc.name, err)
		}
		if _, err := eq.ListOrdersFiltered(ctx, ListOrdersFilteredParams{}); !errors.Is(err, errBoom) {
			t.Fatalf("%s ListOrdersFiltered: got %v", tc.name, err)
		}
		if _, err := eq.ListReturnRequestsFiltered(ctx, ListReturnRequestsFilteredParams{}); !errors.Is(err, errBoom) {
			t.Fatalf("%s ListReturnRequestsFiltered: got %v", tc.name, err)
		}
		if _, err := eq.FindExpiredPendingOrders(ctx, 5); !errors.Is(err, errBoom) {
			t.Fatalf("%s FindExpiredPendingOrders: got %v", tc.name, err)
		}
		if _, err := eq.ClaimPendingOutboxEvents(ctx, ClaimPendingOutboxEventsParams{BatchLimit: 5}); !errors.Is(err, errBoom) {
			t.Fatalf("%s ClaimPendingOutboxEvents: got %v", tc.name, err)
		}
	}
}
