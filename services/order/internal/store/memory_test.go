package store

import (
	"context"
	"errors"
	"testing"
	"time"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	"github.com/nexusmart/core/services/order/internal/db"
	"github.com/nexusmart/core/services/order/internal/events"
	"github.com/nexusmart/core/services/order/internal/statemachine"
)

func TestMemoryStore_CheckoutLifecycleReturnsComplaintsAndOutbox(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()

	if err := st.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	rawJSON, err := MarshalJSONBytes(map[string]string{"k": "v"})
	if err != nil || string(rawJSON) != `{"k":"v"}` {
		t.Fatalf("MarshalJSONBytes: %s err=%v", string(rawJSON), err)
	}

	// CheckoutGroup creation, duplicate idempotency key, lookup, fail.
	grp, err := st.CreateCheckoutGroup(ctx, db.CreateCheckoutGroupParams{
		ID:             "grp-1",
		BuyerID:        "buyer-1",
		IdempotencyKey: "idem-1",
		Status:         "IN_PROGRESS",
		PaymentMethod:  "ZALOPAY",
	})
	if err != nil || grp.SalesChannel != "MARKETPLACE" {
		t.Fatalf("CreateCheckoutGroup: %+v err=%v", grp, err)
	}
	if _, err := st.CreateCheckoutGroup(ctx, db.CreateCheckoutGroupParams{
		ID: "grp-dup", BuyerID: "buyer-1", IdempotencyKey: "idem-1", Status: "IN_PROGRESS",
	}); !errors.Is(err, ErrDuplicateIdempotencyKey) {
		t.Fatalf("expected ErrDuplicateIdempotencyKey, got %v", err)
	}
	if got, err := st.GetActiveCheckoutGroup(ctx, "buyer-1", "idem-1"); err != nil || got.ID != "grp-1" {
		t.Fatalf("GetActiveCheckoutGroup: %+v err=%v", got, err)
	}
	if got, err := st.GetCheckoutGroupByID(ctx, "grp-1"); err != nil || got.ID != "grp-1" {
		t.Fatalf("GetCheckoutGroupByID: %+v err=%v", got, err)
	}
	if _, err := st.GetCheckoutGroupByID(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	// Persist batch + CompleteCheckoutWithPayment.
	exp := time.Now().UTC().Add(-time.Minute)
	aggs, err := st.PersistCheckoutBatch(ctx, CheckoutBatch{
		CheckoutGroupID: "grp-1",
		Orders: []NewOrderRecord{
			{
				Order: db.InsertOrderParams{
					ID:                  "ord-1",
					OrderCode:           "NM-1",
					CheckoutGroupID:     "grp-1",
					BuyerID:             "buyer-1",
					ShopID:              "shop-1",
					Status:              "PENDING",
					PaymentMethod:       "ZALOPAY",
					TotalPayableVND:     500000,
					ShippingAddressJSON: []byte(`{}`),
					PaymentExpiresAt:    &exp,
				},
				Items: []db.InsertOrderItemParams{
					{ID: "item-1", OrderID: "ord-1", SKUID: "sku-1", Quantity: 1, UnitPriceVND: 500000, EffectiveUnitPriceVND: 500000, LineTotalVND: 500000},
				},
				Timeline: []db.InsertTimelineEventParams{
					{ID: "tl-1", OrderID: "ord-1", FromStatus: "UNSPECIFIED", ToStatus: "PENDING"},
				},
				Outbox: []db.InsertOutboxEventParams{
					{ID: "ob-1", EventType: events.SubjectOrderCreated, PayloadJSON: []byte(`{}`)},
				},
			},
		},
	})
	if err != nil || len(aggs) != 1 {
		t.Fatalf("PersistCheckoutBatch: %+v err=%v", aggs, err)
	}
	if err := st.CompleteCheckoutWithPayment(ctx, UpdateCheckoutPaymentParams{
		CheckoutGroupID:  "grp-1",
		PaymentID:        "pay-1",
		PaymentStatus:    "PENDING",
		PaymentExpiresAt: &exp,
		TotalPayableVND:  500000,
	}); err != nil {
		t.Fatalf("CompleteCheckoutWithPayment: %v", err)
	}

	// FindExpiredPendingGroups finds expired ZALOPAY order.
	expired, err := st.FindExpiredPendingGroups(ctx, 10)
	if err != nil || len(expired) != 1 || expired[0].CheckoutGroupID != "grp-1" {
		t.Fatalf("FindExpiredPendingGroups: %+v err=%v", expired, err)
	}

	// TransitionOrdersPaymentResult (SUCCEEDED + duplicate event_id).
	payOut, err := st.TransitionOrdersPaymentResult(ctx, PaymentResultTransitionParams{
		EventID:         "ev-pay-1",
		EventType:       events.SubjectPaymentSucceeded,
		CheckoutGroupID: "grp-1",
		PaymentID:       "pay-1",
		ResultStatus:    orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_SUCCEEDED,
	})
	if err != nil || len(payOut.Transitioned) != 1 || payOut.AlreadyProcessed {
		t.Fatalf("TransitionOrdersPaymentResult: %+v err=%v", payOut, err)
	}
	dupOut, err := st.TransitionOrdersPaymentResult(ctx, PaymentResultTransitionParams{
		EventID: "ev-pay-1", CheckoutGroupID: "grp-1", ResultStatus: orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_SUCCEEDED,
	})
	if err != nil || !dupOut.AlreadyProcessed {
		t.Fatalf("expected AlreadyProcessed=true, got %+v err=%v", dupOut, err)
	}
	if ok, _ := st.IsEventProcessed(ctx, "ev-pay-1"); !ok {
		t.Fatalf("expected IsEventProcessed=true")
	}

	// Ship -> Delivered -> ReturnRequested -> RejectReturn -> UpsertOpenComplaint -> Resolve (CompleteReturnRefund).
	if _, err := st.TransitionOrderShipped(ctx, ShipOrderParams{
		OrderID: "ord-1", CarrierCode: "ghtk", TrackingCode: "GHTK-1",
		OutboxEvent: db.InsertOutboxEventParams{ID: "ob-ship", EventType: events.SubjectOrderShipped, PayloadJSON: []byte(`{}`)},
	}); err != nil {
		t.Fatalf("TransitionOrderShipped: %v", err)
	}
	if _, err := st.ApplyShipmentUpdate(ctx, ShipmentUpdateParams{
		EventID: "ev-deliv", EventType: events.SubjectShipmentUpdated, OrderID: "ord-1", ShipmentStatus: "delivered",
	}); err != nil {
		t.Fatalf("ApplyShipmentUpdate delivered: %v", err)
	}
	if _, rr, err := st.TransitionOrderReturnRequested(ctx, ReturnRequestParams{
		OrderID: "ord-1", BuyerID: "buyer-1", ShopID: "shop-1", ReturnRequestID: "rr-1",
		Reason: "defect", RefundAmountVND: 500000,
		OutboxEvent: db.InsertOutboxEventParams{ID: "ob-rr", EventType: events.SubjectOrderReturnRequested, PayloadJSON: []byte(`{}`)},
	}); err != nil || rr.ID != "rr-1" {
		t.Fatalf("TransitionOrderReturnRequested: %+v err=%v", rr, err)
	}
	if gotRR, err := st.GetReturnRequestByID(ctx, "rr-1"); err != nil || gotRR.RefundAmountVND != 500000 {
		t.Fatalf("GetReturnRequestByID: %+v err=%v", gotRR, err)
	}
	if latestRR, err := st.GetLatestReturnRequestByOrderID(ctx, "ord-1"); err != nil || latestRR.ID != "rr-1" {
		t.Fatalf("GetLatestReturnRequestByOrderID: %+v err=%v", latestRR, err)
	}
	if listRR, totalRR, err := st.ListReturnRequestsFiltered(ctx, "ord-1", "buyer-1", "shop-1", "PENDING", 10, 0); err != nil || totalRR != 1 || len(listRR) != 1 {
		t.Fatalf("ListReturnRequestsFiltered: len=%d total=%d err=%v", len(listRR), totalRR, err)
	}

	_, _, cp, err := st.RejectReturnAndCreateComplaint(ctx, RejectReturnParams{
		ReturnRequestID: "rr-1", OrderID: "ord-1", Reason: "no defect", ComplaintID: "cp-1", ComplaintReason: "rejected",
	})
	if err != nil || cp.ID != "cp-1" || cp.Status != "OPEN" {
		t.Fatalf("RejectReturnAndCreateComplaint: %+v err=%v", cp, err)
	}
	if gotCP, err := st.GetComplaintByID(ctx, "cp-1"); err != nil || gotCP.ID != "cp-1" {
		t.Fatalf("GetComplaintByID: %+v err=%v", gotCP, err)
	}
	if gotCP, err := st.GetComplaintByReturnRequestID(ctx, "rr-1"); err != nil || gotCP.ID != "cp-1" {
		t.Fatalf("GetComplaintByReturnRequestID: %+v err=%v", gotCP, err)
	}
	upCP, err := st.UpsertOpenComplaint(ctx, UpsertComplaintParams{
		ComplaintID: "cp-new", ReturnRequestID: "rr-1", OrderID: "ord-1", BuyerID: "buyer-1", ShopID: "shop-1",
		Reason: "updated reason", EvidenceURLsJSON: []byte(`["https://a"]`), UpdateEvidence: true,
	})
	if err != nil || upCP.ID != "cp-1" || upCP.Reason != "updated reason" {
		t.Fatalf("UpsertOpenComplaint update: %+v err=%v", upCP, err)
	}
	if listCP, totalCP, err := st.ListComplaintsFiltered(ctx, "OPEN", "ord-1", "shop-1", 10, 0); err != nil || totalCP != 1 || len(listCP) != 1 {
		t.Fatalf("ListComplaintsFiltered: len=%d total=%d err=%v", len(listCP), totalCP, err)
	}

	// CompleteReturnRefund resolving complaint + transitioning RETURN_REJECTED -> RETURN_APPROVED -> REFUNDED.
	refOut, err := st.CompleteReturnRefund(ctx, CompleteReturnRefundParams{
		ReturnRequestID:     "rr-1",
		OrderID:             "ord-1",
		RefundID:            "ref-1",
		RefundAmountVND:     500000,
		PaymentStatus:       "REFUNDED",
		IntermediateStatus:  orderv1.OrderStatus_ORDER_STATUS_RETURN_APPROVED,
		ActorID:             "admin-1",
		ActorRole:           "admin",
		Note:                "refunded",
		ComplaintID:         "cp-1",
		ComplaintStatus:     "RESOLVED",
		ComplaintResolution: "REFUND",
		AdminNote:           "approved",
		ResolvedBy:          "admin-1",
		OutboxEvents:        []db.InsertOutboxEventParams{{ID: "ob-ref", EventType: events.SubjectOrderRefunded, PayloadJSON: []byte(`{}`)}},
	})
	if err != nil || refOut.AlreadyRefunded || refOut.Order.Order.Status != "REFUNDED" || refOut.Complaint.Status != "RESOLVED" {
		t.Fatalf("CompleteReturnRefund: %+v err=%v", refOut, err)
	}
	// UpsertOpenComplaint on resolved complaint -> ErrComplaintNotOpen.
	if _, err := st.UpsertOpenComplaint(ctx, UpsertComplaintParams{
		ReturnRequestID: "rr-1", Reason: "too late",
	}); !errors.Is(err, ErrComplaintNotOpen) {
		t.Fatalf("expected ErrComplaintNotOpen, got %v", err)
	}

	// Outbox processing (with 1 simulated failure then success) & dead letters & timeline.
	tl, err := st.GetOrderTimeline(ctx, "ord-1")
	if err != nil || len(tl) < 5 {
		t.Fatalf("GetOrderTimeline: len=%d err=%v", len(tl), err)
	}
	failedOnce := false
	nPub, err := st.ProcessPendingOutbox(ctx, 10, func(_ context.Context, _ db.OutboxEvent) error {
		if !failedOnce {
			failedOnce = true
			return errors.New("transient publish err")
		}
		return nil
	})
	if err != nil || nPub < 1 {
		t.Fatalf("ProcessPendingOutbox: nPub=%d err=%v", nPub, err)
	}
	if err := st.InsertDeadLetterEvent(ctx, db.InsertDeadLetterEventParams{
		ID: "dlq-1", Subject: "payment.failed", ErrorMessage: "bad json",
	}); err != nil || len(st.DeadLetters()) != 1 {
		t.Fatalf("InsertDeadLetterEvent failed: %v", err)
	}

	// CancelCheckoutGroupOrders & FailCheckoutGroup & DismissComplaintAndCompleteOrder & ApproveReturnReturning.
	if err := st.FailCheckoutGroup(ctx, "grp-1", "FAILED"); err != nil {
		t.Fatalf("FailCheckoutGroup: %v", err)
	}
	st.SeedLegacyOrder(
		db.CheckoutGroup{ID: "grp-2", BuyerID: "buyer-1", Status: "COMPLETED"},
		db.Order{ID: "ord-2", OrderCode: "NM-2", CheckoutGroupID: "grp-2", BuyerID: "buyer-1", ShopID: "shop-1", Status: "PENDING", PaymentMethod: "VIETQR"},
		nil,
	)
	if cancelled, err := st.CancelCheckoutGroupOrders(ctx, CancelGroupOrdersParams{
		CheckoutGroupID: "grp-2", Reason: "payment fail", PaymentStatus: "FAILED",
	}); err != nil || len(cancelled) != 1 {
		t.Fatalf("CancelCheckoutGroupOrders: %+v err=%v", cancelled, err)
	}
	// Verify invalid transition error propagates cleanly.
	if _, _, err := st.TransitionOrderStatus(ctx, SingleOrderTransitionParams{
		OrderID: "ord-2", TargetStatus: orderv1.OrderStatus_ORDER_STATUS_SHIPPING,
	}); !errors.Is(err, statemachine.ErrInvalidTransition) {
		t.Fatalf("expected ErrInvalidTransition, got %v", err)
	}

	// DismissComplaintAndCompleteOrder + ApproveReturnReturning + RecordPaymentRefunded + ListOrdersFiltered pagination.
	st.SeedLegacyOrder(
		db.CheckoutGroup{ID: "grp-3", BuyerID: "buyer-1", Status: "COMPLETED"},
		db.Order{ID: "ord-3", OrderCode: "NM-3", CheckoutGroupID: "grp-3", BuyerID: "buyer-1", ShopID: "shop-1", Status: "DELIVERED", PaymentMethod: "COD"},
		nil,
	)
	_, _, _ = st.TransitionOrderReturnRequested(ctx, ReturnRequestParams{OrderID: "ord-3", BuyerID: "buyer-1", ShopID: "shop-1", ReturnRequestID: "rr-3", Reason: "r"})
	_, _, _, _ = st.RejectReturnAndCreateComplaint(ctx, RejectReturnParams{ReturnRequestID: "rr-3", OrderID: "ord-3", Reason: "nope", ComplaintID: "cp-3", ComplaintReason: "r"})
	if agg3, _, cp3, err := st.DismissComplaintAndCompleteOrder(ctx, DismissComplaintParams{
		ComplaintID: "cp-3", OrderID: "ord-3", AdminNote: "dismissed", ResolvedBy: "admin-1", ActorRole: "admin", Note: "done",
		OutboxEvent: db.InsertOutboxEventParams{ID: "ob-dis", EventType: events.SubjectOrderCompleted, PayloadJSON: []byte(`{}`)},
	}); err != nil || agg3.Order.Status != "COMPLETED" || cp3.Status != "DISMISSED" {
		t.Fatalf("DismissComplaintAndCompleteOrder: agg=%+v cp=%+v err=%v", agg3, cp3, err)
	}

	st.SeedLegacyOrder(
		db.CheckoutGroup{ID: "grp-4", BuyerID: "buyer-1", Status: "COMPLETED"},
		db.Order{ID: "ord-4", OrderCode: "NM-4", CheckoutGroupID: "grp-4", BuyerID: "buyer-1", ShopID: "shop-1", Status: "DELIVERED", PaymentMethod: "COD"},
		nil,
	)
	_, _, _ = st.TransitionOrderReturnRequested(ctx, ReturnRequestParams{OrderID: "ord-4", BuyerID: "buyer-1", ShopID: "shop-1", ReturnRequestID: "rr-4", Reason: "r"})
	if agg4, rr4, _, err := st.ApproveReturnReturning(ctx, ApproveReturnReturningParams{
		ReturnRequestID: "rr-4", OrderID: "ord-4", ReverseTrackingCode: "RET-4", ReverseLabelURL: "https://l/4",
		ActorID: "seller-1", ActorRole: "seller", ApproveNote: "ok", ReturningNote: "ship",
		OutboxEvents: []db.InsertOutboxEventParams{{ID: "ob-app4", EventType: events.SubjectOrderReturnApproved, PayloadJSON: []byte(`{}`)}},
	}); err != nil || agg4.Order.Status != "RETURNING" || rr4.ReverseTrackingCode != "RET-4" {
		t.Fatalf("ApproveReturnReturning: agg=%+v rr=%+v err=%v", agg4, rr4, err)
	}

	if dup, err := st.RecordPaymentRefunded(ctx, PaymentRefundedParams{
		EventID: "ev-ref-4", EventType: events.SubjectPaymentRefunded, OrderID: "ord-4", RefundID: "r4", AmountVND: 1000,
	}); err != nil || dup {
		t.Fatalf("RecordPaymentRefunded: dup=%v err=%v", dup, err)
	}
	if _, total, err := st.ListOrdersFiltered(ctx, "buyer-1", "", "", "MARKETPLACE", "", 2, 10); err != nil || total < 4 {
		t.Fatalf("ListOrdersFiltered beyond end: total=%d err=%v", total, err)
	}

	// Additional error & edge paths on MemoryStore for high branch coverage.
	if _, err := st.GetActiveCheckoutGroup(ctx, "nobody", "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if _, err := st.GetOrderWithDetails(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if _, err := st.GetReturnRequestByID(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if _, err := st.GetLatestReturnRequestByOrderID(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if _, err := st.GetComplaintByID(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if _, err := st.GetComplaintByReturnRequestID(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if _, err := st.TransitionOrderShipped(ctx, ShipOrderParams{OrderID: "missing"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if _, err := st.TransitionOrderCompleted(ctx, CompleteOrderParams{OrderID: "missing"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if _, _, err := st.TransitionOrderReturnRequested(ctx, ReturnRequestParams{OrderID: "missing"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if _, _, _, err := st.ApproveReturnReturning(ctx, ApproveReturnReturningParams{OrderID: "missing"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if _, err := st.CompleteReturnRefund(ctx, CompleteReturnRefundParams{OrderID: "missing"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if _, _, _, err := st.RejectReturnAndCreateComplaint(ctx, RejectReturnParams{OrderID: "missing"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if _, _, _, err := st.DismissComplaintAndCompleteOrder(ctx, DismissComplaintParams{ComplaintID: "missing"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}

	// UpsertOpenComplaint insert path (when no complaint existed yet).
	newCP, err := st.UpsertOpenComplaint(ctx, UpsertComplaintParams{
		ComplaintID: "cp-fresh", ReturnRequestID: "rr-fresh", OrderID: "ord-4", BuyerID: "buyer-1", ShopID: "shop-1",
		Reason: "new dispute", EvidenceURLsJSON: []byte(`["https://e"]`), UpdateEvidence: true,
	})
	if err != nil || newCP.ID != "cp-fresh" {
		t.Fatalf("UpsertOpenComplaint create: %+v err=%v", newCP, err)
	}

	// ApplyShipmentUpdate: NotFound, AlreadyProcessed, return direction picking/returned.
	if out, err := st.ApplyShipmentUpdate(ctx, ShipmentUpdateParams{OrderID: "missing", ShipmentStatus: "delivered"}); err != nil || !out.NotFound {
		t.Fatalf("ApplyShipmentUpdate missing: %+v err=%v", out, err)
	}
	if out, err := st.ApplyShipmentUpdate(ctx, ShipmentUpdateParams{EventID: "ev-deliv", OrderID: "ord-1", ShipmentStatus: "delivered"}); err != nil || !out.AlreadyProcessed {
		t.Fatalf("ApplyShipmentUpdate dup: %+v err=%v", out, err)
	}
	if _, err := st.ApplyShipmentUpdate(ctx, ShipmentUpdateParams{EventID: "ev-ret-pick", OrderID: "ord-4", ShipmentStatus: "picking", Direction: "return"}); err != nil {
		t.Fatalf("ApplyShipmentUpdate return picking: %v", err)
	}
	if out, err := st.ApplyShipmentUpdate(ctx, ShipmentUpdateParams{EventID: "ev-ret-4", OrderID: "ord-4", ShipmentStatus: "returned", Direction: "return"}); err != nil || out.TransitionedTo != orderv1.OrderStatus_ORDER_STATUS_RETURNED {
		t.Fatalf("ApplyShipmentUpdate return returned: %+v err=%v", out, err)
	}

	// CompleteReturnRefund idempotent AlreadyRefunded + AlreadyProcessed + RecordPaymentRefunded duplicate & group fallback.
	if out, err := st.CompleteReturnRefund(ctx, CompleteReturnRefundParams{EventID: "ev-ref-idem", OrderID: "ord-1", ReturnRequestID: "rr-1"}); err != nil || !out.AlreadyRefunded {
		t.Fatalf("CompleteReturnRefund already refunded: %+v err=%v", out, err)
	}
	if out, err := st.CompleteReturnRefund(ctx, CompleteReturnRefundParams{EventID: "ev-ref-idem", OrderID: "ord-1"}); err != nil || !out.AlreadyProcessed {
		t.Fatalf("CompleteReturnRefund dup event: %+v err=%v", out, err)
	}
	if dup, err := st.RecordPaymentRefunded(ctx, PaymentRefundedParams{EventID: "ev-ref-4", OrderID: "ord-4"}); err != nil || !dup {
		t.Fatalf("RecordPaymentRefunded dup: dup=%v err=%v", dup, err)
	}
	if dup, err := st.RecordPaymentRefunded(ctx, PaymentRefundedParams{EventID: "ev-ref-grp", OrderID: "grp-4", RefundID: "rg4", AmountVND: 500}); err != nil || dup {
		t.Fatalf("RecordPaymentRefunded group: dup=%v err=%v", dup, err)
	}

	// TransitionOrdersPaymentResult with EXPIRED / FAILED / empty group / OrderID lookup.
	st.SeedLegacyOrder(
		db.CheckoutGroup{ID: "grp-5", BuyerID: "buyer-1", Status: "COMPLETED"},
		db.Order{ID: "ord-5", OrderCode: "NM-5", CheckoutGroupID: "grp-5", BuyerID: "buyer-1", ShopID: "shop-1", Status: "PENDING", PaymentMethod: "ZALOPAY"},
		nil,
	)
	if out, err := st.TransitionOrdersPaymentResult(ctx, PaymentResultTransitionParams{
		OrderID: "ord-5", ResultStatus: orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_EXPIRED,
	}); err != nil || len(out.Transitioned) != 1 || out.Transitioned[0].PaymentStatus != "EXPIRED" {
		t.Fatalf("TransitionOrdersPaymentResult EXPIRED: %+v err=%v", out, err)
	}
	if out, err := st.TransitionOrdersPaymentResult(ctx, PaymentResultTransitionParams{}); err != nil || len(out.Transitioned) != 0 {
		t.Fatalf("TransitionOrdersPaymentResult empty: %+v err=%v", out, err)
	}

	// OutboxEvents snapshot + pagination bounds on returns & complaints.
	if len(st.OutboxEvents()) == 0 {
		t.Fatalf("expected non-empty outbox events")
	}
	if list, _, err := st.ListReturnRequestsFiltered(ctx, "", "", "", "", 10, 999); err != nil || len(list) != 0 {
		t.Fatalf("ListReturnRequestsFiltered beyond end: %+v err=%v", list, err)
	}
	if list, _, err := st.ListComplaintsFiltered(ctx, "", "", "", 10, 999); err != nil || len(list) != 0 {
		t.Fatalf("ListComplaintsFiltered beyond end: %+v err=%v", list, err)
	}
}
