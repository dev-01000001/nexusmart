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

func TestMemoryStore_CheckoutLifecycleReturnsSalesChannelAndOutbox(t *testing.T) {
	ctx := context.Background()
	st := NewMemoryStore()

	if err := st.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	rawJSON, err := MarshalJSONBytes(map[string]string{"k": "v"})
	if err != nil || string(rawJSON) != `{"k":"v"}` {
		t.Fatalf("MarshalJSONBytes: %s err=%v", string(rawJSON), err)
	}

	// CheckoutGroup creation defaults empty SalesChannel to MARKETPLACE; rejects duplicate active idempotency key.
	grp, err := st.CreateCheckoutGroup(ctx, db.CreateCheckoutGroupParams{
		ID:             "grp-1",
		BuyerID:        "buyer-1",
		IdempotencyKey: "idem-1",
		RequestHash:    "hash-1",
		Status:         "IN_PROGRESS",
		PaymentMethod:  "ZALOPAY",
	})
	if err != nil || grp.SalesChannel != "MARKETPLACE" || grp.StorefrontShopID != "" {
		t.Fatalf("CreateCheckoutGroup: %+v err=%v", grp, err)
	}
	if _, err := st.CreateCheckoutGroup(ctx, db.CreateCheckoutGroupParams{
		ID: "grp-dup", BuyerID: "buyer-1", IdempotencyKey: "idem-1", Status: "IN_PROGRESS",
	}); !errors.Is(err, ErrDuplicateIdempotencyKey) {
		t.Fatalf("expected ErrDuplicateIdempotencyKey, got %v", err)
	}
	if got, err := st.GetCheckoutGroupForIdempotency(ctx, "buyer-1", "idem-1"); err != nil || got.Group.ID != "grp-1" {
		t.Fatalf("GetCheckoutGroupForIdempotency: %+v err=%v", got, err)
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
	if err != nil || len(aggs) != 1 || aggs[0].Order.SalesChannel != "MARKETPLACE" {
		t.Fatalf("PersistCheckoutBatch: %+v err=%v", aggs, err)
	}
	if err := st.CompleteCheckoutWithPayment(ctx, UpdateCheckoutPaymentParams{
		CheckoutGroupID:  "grp-1",
		PaymentID:        "pay-1",
		PaymentStatus:    "PENDING",
		PaymentExpiresAt: &exp,
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

	// Prepare -> Delivered -> ReturnRequested -> Return transitions (unified main 10-state model).
	if _, err := st.TransitionOrderPreparedForShipment(ctx, ShipOrderParams{
		OrderID: "ord-1", CarrierCode: "ghtk", TrackingCode: "GHTK-1",
	}); err != nil {
		t.Fatalf("TransitionOrderPreparedForShipment: %v", err)
	}
	if _, err := st.ApplyShipmentUpdate(ctx, ShipmentUpdateParams{
		EventID: "ev-deliv", EventType: events.SubjectShipmentUpdated, OrderID: "ord-1", ShipmentStatus: "delivered", TrackingCode: "GHTK-1",
	}); err != nil {
		t.Fatalf("ApplyShipmentUpdate delivered: %v", err)
	}
	deadline := time.Now().UTC().Add(72 * time.Hour)
	_, retAgg, err := st.TransitionOrderReturnRequested(ctx, ReturnRequestParams{
		OrderID: "ord-1", OrderCode: "NM-1", BuyerID: "buyer-1", ShopID: "shop-1", ReturnRequestID: "rr-1",
		Reason: "defect", RefundAmountVND: 500000, DeadlineAt: &deadline,
		OutboxEvent: db.InsertOutboxEventParams{ID: "ob-rr", EventType: events.SubjectOrderReturnUpdated, PayloadJSON: []byte(`{}`)},
	})
	if err != nil || retAgg.Request.ID != "rr-1" {
		t.Fatalf("TransitionOrderReturnRequested: %+v err=%v", retAgg, err)
	}
	if gotRR, err := st.GetReturnRequest(ctx, "rr-1", ""); err != nil || gotRR.Request.RefundAmountVND != 500000 {
		t.Fatalf("GetReturnRequest: %+v err=%v", gotRR, err)
	}
	if listRR, totalRR, err := st.ListReturnRequestsFiltered(ctx, "buyer-1", "shop-1", "PENDING", 10, 0); err != nil || totalRR != 1 || len(listRR) != 1 {
		t.Fatalf("ListReturnRequestsFiltered: len=%d total=%d err=%v", len(listRR), totalRR, err)
	}

	// Outbox processing (with 1 simulated failure then success) & dead letters & timeline.
	tl, err := st.GetOrderTimeline(ctx, "ord-1")
	if err != nil || len(tl) < 4 {
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

	// CancelCheckoutGroupOrders on IN_PROGRESS group + invalid transition error.
	st.SeedLegacyOrder(
		db.CheckoutGroup{ID: "grp-2", BuyerID: "buyer-1", Status: "IN_PROGRESS"},
		db.Order{ID: "ord-2", OrderCode: "NM-2", CheckoutGroupID: "grp-2", BuyerID: "buyer-1", ShopID: "shop-1", Status: "PENDING", PaymentMethod: "VIETQR"},
		nil,
	)
	if cancelRes, err := st.CancelCheckoutGroupOrders(ctx, CancelGroupOrdersParams{
		CheckoutGroupID: "grp-2", Reason: "payment fail", PaymentStatus: "FAILED",
	}); err != nil || len(cancelRes.Cancelled) != 1 {
		t.Fatalf("CancelCheckoutGroupOrders: %+v err=%v", cancelRes, err)
	}
	if _, err := st.TransitionOrderStatus(ctx, SingleOrderTransitionParams{
		OrderID: "ord-2", TargetStatus: orderv1.OrderStatus_ORDER_STATUS_SHIPPING,
	}); !errors.Is(err, statemachine.ErrInvalidTransition) {
		t.Fatalf("expected ErrInvalidTransition, got %v", err)
	}

	// SalesChannel / StorefrontShopID filtering + SeedLegacyOrder empty channel -> MARKETPLACE.
	st.SeedLegacyOrder(
		db.CheckoutGroup{ID: "grp-sf", BuyerID: "buyer-1", Status: "COMPLETED", SalesChannel: "STOREFRONT", StorefrontShopID: "shop-sf"},
		db.Order{ID: "ord-sf", OrderCode: "NM-SF", CheckoutGroupID: "grp-sf", BuyerID: "buyer-1", ShopID: "shop-sf", Status: "CONFIRMED", PaymentMethod: "COD", SalesChannel: "STOREFRONT", StorefrontShopID: "shop-sf"},
		nil,
	)
	sfRes, err := st.ListOrdersWithFilter(ctx, OrderListFilter{
		BuyerID:             "buyer-1",
		SalesChannel:        "STOREFRONT",
		StorefrontShopID:    "shop-sf",
		Limit:               10,
		IncludeStatusCounts: true,
	})
	if err != nil || sfRes.Total != 1 || len(sfRes.Orders) != 1 || sfRes.Orders[0].Order.ID != "ord-sf" || sfRes.StatusCounts["CONFIRMED"] != 1 {
		t.Fatalf("ListOrdersWithFilter STOREFRONT: %+v err=%v", sfRes, err)
	}
	mpRes, err := st.ListOrdersWithFilter(ctx, OrderListFilter{
		BuyerID:      "buyer-1",
		SalesChannel: "MARKETPLACE",
		Limit:        10,
	})
	if err != nil || mpRes.Total != 2 {
		t.Fatalf("ListOrdersWithFilter MARKETPLACE (includes legacy empty): total=%d err=%v", mpRes.Total, err)
	}

	// RecordPaymentRefunded + duplicate detection + error paths.
	if dup, err := st.RecordPaymentRefunded(ctx, PaymentRefundedParams{
		EventID: "ev-ref-1", EventType: events.SubjectPaymentRefunded, OrderID: "ord-1", RefundID: "r1", AmountVND: 1000,
	}); err != nil || dup {
		t.Fatalf("RecordPaymentRefunded: dup=%v err=%v", dup, err)
	}
	if dup, err := st.RecordPaymentRefunded(ctx, PaymentRefundedParams{EventID: "ev-ref-1", OrderID: "ord-1"}); err != nil || !dup {
		t.Fatalf("RecordPaymentRefunded dup: dup=%v err=%v", dup, err)
	}
	if _, err := st.GetCheckoutGroupForIdempotency(ctx, "nobody", "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if _, err := st.GetOrderWithDetails(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if _, err := st.TransitionOrderPreparedForShipment(ctx, ShipOrderParams{OrderID: "missing"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if _, err := st.TransitionOrderCompleted(ctx, CompleteOrderParams{OrderID: "missing"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if _, _, err := st.TransitionOrderReturnRequested(ctx, ReturnRequestParams{OrderID: "missing"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if out, err := st.ApplyShipmentUpdate(ctx, ShipmentUpdateParams{OrderID: "missing", ShipmentStatus: "delivered"}); err != nil || !out.NotFound {
		t.Fatalf("ApplyShipmentUpdate missing: %+v err=%v", out, err)
	}
	if len(st.OutboxEvents()) == 0 {
		t.Fatalf("expected non-empty outbox events")
	}
}
