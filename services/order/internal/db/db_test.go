package db

import (
	"context"
	"errors"
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
func (r *fakeRows) Scan(_ ...any) error { return r.scanErr }
func (r *fakeRows) Values() ([]any, error) {
	return nil, nil
}
func (r *fakeRows) RawValues() [][]byte { return nil }
func (r *fakeRows) Conn() *pgx.Conn     { return nil }

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

func TestQueries_SQLContractsAndRowScanning(t *testing.T) {
	ctx := context.Background()
	m := &mockDBTX{numRows: 1}
	q := New(m)
	_ = q.WithTx(nil)

	// Exec queries.
	if err := q.CompleteCheckoutGroup(ctx, CompleteCheckoutGroupParams{ID: "g1"}); err != nil {
		t.Fatalf("CompleteCheckoutGroup: %v", err)
	}
	if err := q.FailCheckoutGroup(ctx, FailCheckoutGroupParams{ID: "g1"}); err != nil {
		t.Fatalf("FailCheckoutGroup: %v", err)
	}
	if err := q.InsertDeadLetterEvent(ctx, InsertDeadLetterEventParams{ID: "d1"}); err != nil {
		t.Fatalf("InsertDeadLetterEvent: %v", err)
	}
	if err := q.InsertOutboxEvent(ctx, InsertOutboxEventParams{ID: "o1"}); err != nil {
		t.Fatalf("InsertOutboxEvent: %v", err)
	}
	if err := q.MarkOutboxEventAttemptFailed(ctx, MarkOutboxEventAttemptFailedParams{ID: "o1"}); err != nil {
		t.Fatalf("MarkOutboxEventAttemptFailed: %v", err)
	}
	if err := q.MarkOutboxEventPublished(ctx, "o1"); err != nil {
		t.Fatalf("MarkOutboxEventPublished: %v", err)
	}
	if n, err := q.TryMarkEventProcessed(ctx, TryMarkEventProcessedParams{EventID: "e1"}); err != nil || n != 1 {
		t.Fatalf("TryMarkEventProcessed: n=%d err=%v", n, err)
	}
	if err := q.UpdateCheckoutGroupPaymentStatus(ctx, UpdateCheckoutGroupPaymentStatusParams{ID: "g1"}); err != nil {
		t.Fatalf("UpdateCheckoutGroupPaymentStatus: %v", err)
	}
	if err := q.UpdateOrderPaymentInfoByGroup(ctx, UpdateOrderPaymentInfoByGroupParams{CheckoutGroupID: "g1"}); err != nil {
		t.Fatalf("UpdateOrderPaymentInfoByGroup: %v", err)
	}
	if err := q.UpdateOrderPaymentStatus(ctx, UpdateOrderPaymentStatusParams{ID: "o1"}); err != nil {
		t.Fatalf("UpdateOrderPaymentStatus: %v", err)
	}
	if err := q.UpdateOrderTrackingCode(ctx, UpdateOrderTrackingCodeParams{ID: "o1"}); err != nil {
		t.Fatalf("UpdateOrderTrackingCode: %v", err)
	}

	// Single-row queries.
	if _, err := q.CountComplaintsFiltered(ctx, CountComplaintsFilteredParams{}); err != nil {
		t.Fatalf("CountComplaintsFiltered: %v", err)
	}
	if _, err := q.CountOrdersFiltered(ctx, CountOrdersFilteredParams{}); err != nil {
		t.Fatalf("CountOrdersFiltered: %v", err)
	}
	if _, err := q.CountReturnRequestsFiltered(ctx, CountReturnRequestsFilteredParams{}); err != nil {
		t.Fatalf("CountReturnRequestsFiltered: %v", err)
	}
	if _, err := q.CreateCheckoutGroup(ctx, CreateCheckoutGroupParams{ID: "g1"}); err != nil {
		t.Fatalf("CreateCheckoutGroup: %v", err)
	}
	if _, err := q.GetActiveCheckoutGroupByIdempotencyKey(ctx, GetActiveCheckoutGroupByIdempotencyKeyParams{}); err != nil {
		t.Fatalf("GetActiveCheckoutGroupByIdempotencyKey: %v", err)
	}
	if _, err := q.GetCheckoutGroupByID(ctx, "g1"); err != nil {
		t.Fatalf("GetCheckoutGroupByID: %v", err)
	}
	if _, err := q.GetComplaintByID(ctx, "c1"); err != nil {
		t.Fatalf("GetComplaintByID: %v", err)
	}
	if _, err := q.GetComplaintByIDForUpdate(ctx, "c1"); err != nil {
		t.Fatalf("GetComplaintByIDForUpdate: %v", err)
	}
	if _, err := q.GetComplaintByReturnRequestID(ctx, "rr1"); err != nil {
		t.Fatalf("GetComplaintByReturnRequestID: %v", err)
	}
	if _, err := q.GetLatestReturnRequestByOrderID(ctx, "o1"); err != nil {
		t.Fatalf("GetLatestReturnRequestByOrderID: %v", err)
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
	if _, err := q.GetReturnRequestByIDForUpdate(ctx, "rr1"); err != nil {
		t.Fatalf("GetReturnRequestByIDForUpdate: %v", err)
	}
	if _, err := q.InsertComplaint(ctx, InsertComplaintParams{ID: "c1", ReturnRequestID: "rr1"}); err != nil || len(m.lastArgs) != 8 {
		t.Fatalf("InsertComplaint: err=%v args=%d", err, len(m.lastArgs))
	}
	if _, err := q.InsertOrder(ctx, InsertOrderParams{ID: "o1"}); err != nil {
		t.Fatalf("InsertOrder: %v", err)
	}
	if _, err := q.InsertOrderItem(ctx, InsertOrderItemParams{ID: "oi1"}); err != nil {
		t.Fatalf("InsertOrderItem: %v", err)
	}
	if _, err := q.InsertReturnRequest(ctx, InsertReturnRequestParams{ID: "rr1", ShopID: "s1", RefundAmountVND: 1000}); err != nil || len(m.lastArgs) != 13 {
		t.Fatalf("InsertReturnRequest: err=%v args=%d", err, len(m.lastArgs))
	}
	if _, err := q.InsertTimelineEvent(ctx, InsertTimelineEventParams{ID: "tl1"}); err != nil {
		t.Fatalf("InsertTimelineEvent: %v", err)
	}
	if _, err := q.IsEventProcessed(ctx, "e1"); err != nil {
		t.Fatalf("IsEventProcessed: %v", err)
	}
	if _, err := q.ResolveComplaintRow(ctx, ResolveComplaintRowParams{ID: "c1", Status: "RESOLVED", Resolution: "REFUND"}); err != nil || !strings.Contains(m.lastSQL, "resolved_at = NOW()") {
		t.Fatalf("ResolveComplaintRow: err=%v sql=%s", err, m.lastSQL)
	}
	if _, err := q.UpdateComplaintDetails(ctx, UpdateComplaintDetailsParams{ID: "c1"}); err != nil {
		t.Fatalf("UpdateComplaintDetails: %v", err)
	}
	if _, err := q.UpdateOrderCompleted(ctx, "o1"); err != nil {
		t.Fatalf("UpdateOrderCompleted: %v", err)
	}
	if _, err := q.UpdateOrderDelivered(ctx, UpdateOrderDeliveredParams{ID: "o1"}); err != nil {
		t.Fatalf("UpdateOrderDelivered: %v", err)
	}
	if _, err := q.UpdateOrderShipped(ctx, UpdateOrderShippedParams{ID: "o1"}); err != nil {
		t.Fatalf("UpdateOrderShipped: %v", err)
	}
	if _, err := q.UpdateOrderStatus(ctx, UpdateOrderStatusParams{ID: "o1"}); err != nil {
		t.Fatalf("UpdateOrderStatus: %v", err)
	}
	if _, err := q.UpdateReturnRequest(ctx, UpdateReturnRequestParams{ID: "rr1", Status: "REFUNDED", RefundID: "ref-1"}); err != nil {
		t.Fatalf("UpdateReturnRequest: %v", err)
	}

	// Multi-row queries + verify LockExpiredPendingOrders supports both VIETQR and ZALOPAY.
	if rows, err := q.ListComplaintsFiltered(ctx, ListComplaintsFilteredParams{}); err != nil || len(rows) != 1 {
		t.Fatalf("ListComplaintsFiltered: len=%d err=%v", len(rows), err)
	}
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
	if rows, err := q.ListOrdersFiltered(ctx, ListOrdersFilteredParams{}); err != nil || len(rows) != 1 {
		t.Fatalf("ListOrdersFiltered: len=%d err=%v", len(rows), err)
	}
	if rows, err := q.ListReturnRequestsFiltered(ctx, ListReturnRequestsFilteredParams{}); err != nil || len(rows) != 1 {
		t.Fatalf("ListReturnRequestsFiltered: len=%d err=%v", len(rows), err)
	}
	if rows, err := q.LockPendingOutboxEvents(ctx, 10); err != nil || len(rows) != 1 {
		t.Fatalf("LockPendingOutboxEvents: len=%d err=%v", len(rows), err)
	}
	if rows, err := q.LockExpiredPendingOrders(ctx, 10); err != nil || len(rows) != 1 || !strings.Contains(m.lastSQL, "IN ('VIETQR', 'ZALOPAY')") {
		t.Fatalf("LockExpiredPendingOrders: len=%d err=%v sql=%s", len(rows), err, m.lastSQL)
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
		if _, err := eq.ListComplaintsFiltered(ctx, ListComplaintsFilteredParams{}); !errors.Is(err, errBoom) {
			t.Fatalf("%s ListComplaintsFiltered: got %v", tc.name, err)
		}
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
		if _, err := eq.LockExpiredPendingOrders(ctx, 5); !errors.Is(err, errBoom) {
			t.Fatalf("%s LockExpiredPendingOrders: got %v", tc.name, err)
		}
		if _, err := eq.LockPendingOutboxEvents(ctx, 5); !errors.Is(err, errBoom) {
			t.Fatalf("%s LockPendingOutboxEvents: got %v", tc.name, err)
		}
	}
}
