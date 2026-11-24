package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	cartv1 "github.com/nexusmart/core/gen/go/nexusmart/cart/v1"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/order/internal/events"
	"github.com/nexusmart/core/services/order/internal/statemachine"
	"github.com/nexusmart/core/services/order/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type failingRetryUpdateStore struct {
	store.Store
	failAttempts int32
	failErr      error
	calls        atomic.Int32
}

func (f *failingRetryUpdateStore) UpdateGroupPaymentOnRetry(ctx context.Context, params store.RetryPaymentUpdateParams) error {
	n := f.calls.Add(1)
	if n <= f.failAttempts {
		return f.failErr
	}
	return f.Store.UpdateGroupPaymentOnRetry(ctx, params)
}

// TestW3B2P4_A_SupersededRefund_AlreadyExistsReturnsError verifies Item A (#1):
// Rust payment maps PaymentError::Conflict -> codes.AlreadyExists (true idempotent replay returns OK + already_existed=true).
// refundSupersededPaidOrders MUST NOT treat codes.AlreadyExists as success: it must log ERROR with payment_id/order_id
// and return an error so the event is not acked and routes to retry/DLQ.
func TestW3B2P4_A_SupersededRefund_AlreadyExistsReturnsError(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	h := newTestHarness()
	var logBuf bytes.Buffer
	h.svc.logger = slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "p4-a-co-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
	})
	if err != nil {
		t.Fatalf("CreateCheckout: %v", err)
	}
	if _, err := h.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
		PaymentId:      co.PaymentId,
		IdempotencyKey: "p4-a-rk-1",
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	}); err != nil {
		t.Fatalf("RetryPayment(COD): %v", err)
	}

	h.payment.refundErr = status.Error(codes.AlreadyExists, "payment conflict")
	err = h.svc.HandlePaymentSucceeded(context.Background(), "ev-p4-a-1", events.PaymentSucceededPayload{
		PaymentID:    co.PaymentId,
		OrderGroupID: co.CheckoutGroupId,
		OrderID:      co.Orders[0].Id,
		AmountVND:    co.Orders[0].TotalPayableVnd,
		Method:       "VIETQR",
	})
	if err == nil {
		t.Fatalf("Item A: codes.AlreadyExists MUST NOT be treated as success")
	}
	if processed, _ := h.store.IsEventProcessed(context.Background(), "ev-p4-a-1"); processed {
		t.Fatalf("Item A: event must NOT be marked processed when CreateRefund returns AlreadyExists")
	}
	logged := logBuf.String()
	if !strings.Contains(logged, `"level":"ERROR"`) || !strings.Contains(logged, co.PaymentId) || !strings.Contains(logged, co.Orders[0].Id) {
		t.Fatalf("Item A: expected ERROR log with payment_id and order_id, got: %s", logged)
	}
}

// TestW3B2P4_B_NonCurrentPaymentSucceeded_MultiHopAndOrphanRefund verifies Item B (#2 + #6.2):
// Any payment.succeeded for a payment_id OTHER than the checkout group's current payment_id
// (both >=2-hop superseded P1->P2->P3 and orphaned unrecorded P2) triggers automatic refund,
// idempotently with key superseded-late-refund:<payment>:<order>, without ever refunding the current confirmed payment.
func TestW3B2P4_B_NonCurrentPaymentSucceeded_MultiHopAndOrphanRefund(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")

	t.Run("MultiHop_P1_P2_COD_LateP1Succeeded_AutoRefundsP1", func(t *testing.T) {
		h := newTestHarness()
		co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "p4-b-multihop",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		p1 := co.PaymentId
		ordID := co.Orders[0].Id

		// Hop 1: VIETQR (P1) -> ZALOPAY (P2)
		r1, err := h.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
			PaymentId:      p1,
			IdempotencyKey: "p4-b-hop1",
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY,
		})
		if err != nil {
			t.Fatalf("RetryPayment(ZALOPAY): %v", err)
		}
		p2 := r1.PaymentId

		// Hop 2: ZALOPAY (P2) -> COD (P3). Now grp.RetryFromPaymentID == P2, NOT P1!
		if _, err := h.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
			PaymentId:      p2,
			IdempotencyKey: "p4-b-hop2",
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		}); err != nil {
			t.Fatalf("RetryPayment(COD): %v", err)
		}

		// Late payment.succeeded arrives for P1 (2 hops back).
		if err := h.svc.HandlePaymentSucceeded(context.Background(), "ev-p4-b-p1-late", events.PaymentSucceededPayload{
			PaymentID:    p1,
			OrderGroupID: co.CheckoutGroupId,
			OrderID:      ordID,
			AmountVND:    co.Orders[0].TotalPayableVnd,
			Method:       "VIETQR",
		}); err != nil {
			t.Fatalf("HandlePaymentSucceeded(P1): %v", err)
		}

		h.payment.mu.Lock()
		refundCalls := append([]*paymentv1.CreateRefundRequest(nil), h.payment.refundCalls...)
		h.payment.mu.Unlock()
		if len(refundCalls) != 1 {
			t.Fatalf("Item B: expected 1 automatic CreateRefund for 2-hop superseded P1, got %d", len(refundCalls))
		}
		wantKey := "superseded-late-refund:" + p1 + ":" + ordID
		if refundCalls[0].GetPaymentId() != p1 || refundCalls[0].GetIdempotencyKey() != wantKey || refundCalls[0].GetRequireApprovalIfLarge() {
			t.Fatalf("Item B: unexpected refund req: %+v", refundCalls[0])
		}

		// Duplicate event_id -> 0 additional refund calls.
		if err := h.svc.HandlePaymentSucceeded(context.Background(), "ev-p4-b-p1-late", events.PaymentSucceededPayload{
			PaymentID: p1, OrderGroupID: co.CheckoutGroupId, OrderID: ordID,
		}); err != nil {
			t.Fatalf("duplicate HandlePaymentSucceeded(P1): %v", err)
		}
		h.payment.mu.Lock()
		dupCount := len(h.payment.refundCalls)
		h.payment.mu.Unlock()
		if dupCount != 1 {
			t.Fatalf("duplicate event_id must not re-call CreateRefund, got %d", dupCount)
		}
	})

	t.Run("OrphanPayment_NotRecordedOnOrderGroup_AutoRefundsOrphan", func(t *testing.T) {
		h := newTestHarness()
		co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "p4-b-orphan",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		ordID := co.Orders[0].Id
		orphanPayID := "pay-orphan-unrecorded-99"

		// Orphan payment P2 (never saved on checkout_group) succeeds at payment-service.
		if err := h.svc.HandlePaymentSucceeded(context.Background(), "ev-p4-b-orphan-1", events.PaymentSucceededPayload{
			PaymentID:    orphanPayID,
			OrderGroupID: co.CheckoutGroupId,
			OrderID:      ordID,
			AmountVND:    co.Orders[0].TotalPayableVnd,
			Method:       "ZALOPAY",
		}); err != nil {
			t.Fatalf("HandlePaymentSucceeded(orphan): %v", err)
		}

		h.payment.mu.Lock()
		refundCalls := append([]*paymentv1.CreateRefundRequest(nil), h.payment.refundCalls...)
		h.payment.mu.Unlock()
		if len(refundCalls) != 1 || refundCalls[0].GetPaymentId() != orphanPayID {
			t.Fatalf("Item B (#6.2): expected 1 automatic CreateRefund for orphan payment %s, got %+v", orphanPayID, refundCalls)
		}
		// Order stays PENDING on its current payment P1 (not mutated by orphan P2).
		ord, _ := h.store.GetOrderWithDetails(context.Background(), ordID)
		if ord.Order.Status != "PENDING" || ord.Order.PaymentID != co.PaymentId {
			t.Fatalf("orphan payment must not mutate order: status=%s payment_id=%s", ord.Order.Status, ord.Order.PaymentID)
		}
	})

	t.Run("CurrentConfirmedPayment_ReplayNeverRefunded", func(t *testing.T) {
		h := newTestHarness()
		co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "p4-b-current-ok",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		for i, evID := range []string{"ev-p4-b-cur-1", "ev-p4-b-cur-2"} {
			if err := h.svc.HandlePaymentSucceeded(context.Background(), evID, events.PaymentSucceededPayload{
				PaymentID: co.PaymentId, OrderGroupID: co.CheckoutGroupId, OrderID: co.Orders[0].Id,
			}); err != nil {
				t.Fatalf("iter %d HandlePaymentSucceeded: %v", i, err)
			}
		}
		h.payment.mu.Lock()
		nRef := len(h.payment.refundCalls)
		h.payment.mu.Unlock()
		if nRef != 0 {
			t.Fatalf("current confirmed payment must NEVER be refunded on replay, got %d refund calls", nRef)
		}
	})
}

// TestW3B2P4_C_ReturnRefundMatching_AndAtomicRecordPaymentRefunded verifies Item C (#4a, #4b, #4c):
//   - Out-of-order matching (rr.RefundID=="") uses idempotency_key == returnRefundIdempotencyKey(order, attempt),
//     NOT amount_vnd / payment_id guessing.
//   - Manual admin refund with same order_id & amount_vnd but different idempotency_key is ignored.
//   - Legacy event missing both correlation keys does NOT match and logs WARN.
//   - RecordPaymentRefunded atomically sets refund_approval_status='APPROVED' + refund_done=TRUE in the same TX
//     so ScanRefundingReturns recovers promo release + REFUNDED even when post-TX side effects fail transiently.
func TestW3B2P4_C_ReturnRefundMatching_AndAtomicRecordPaymentRefunded(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
	cskhCtx := ctxWithUser("cskh-maker-p4", auth.RoleCSKH, "")

	seedEscalatedReturn := func(t *testing.T, h *testHarness, suffix string) (string, string, string) {
		t.Helper()
		co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "p4-c-co-" + suffix,
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 2}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		ordID := co.Orders[0].Id
		if err := h.svc.HandlePaymentSucceeded(context.Background(), "ev-p4-c-pay-"+suffix, events.PaymentSucceededPayload{
			PaymentID: co.PaymentId, OrderGroupID: co.CheckoutGroupId, OrderID: ordID,
		}); err != nil {
			t.Fatalf("HandlePaymentSucceeded: %v", err)
		}
		shipRes, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID})
		if err != nil {
			t.Fatalf("SellerShipOrder: %v", err)
		}
		_ = h.svc.HandleShipmentUpdated(context.Background(), "ev-p4-c-sh1-"+suffix, events.ShipmentUpdatedPayload{
			OrderID: ordID, TrackingCode: shipRes.TrackingCode, Status: "delivering",
		})
		_ = h.svc.HandleShipmentUpdated(context.Background(), "ev-p4-c-sh2-"+suffix, events.ShipmentUpdatedPayload{
			OrderID: ordID, TrackingCode: shipRes.TrackingCode, Status: "delivered",
		})
		reqRet, err := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ordID, Reason: "damaged"})
		if err != nil {
			t.Fatalf("RequestReturn: %v", err)
		}
		if _, err := h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: reqRet.ReturnRequestId, Decision: orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_REJECT, Note: "no",
		}); err != nil {
			t.Fatalf("RespondReturn: %v", err)
		}
		if _, err := h.svc.EscalateReturn(buyerCtx, &orderv1.EscalateReturnRequest{ReturnRequestId: reqRet.ReturnRequestId, Reason: "dispute"}); err != nil {
			t.Fatalf("EscalateReturn: %v", err)
		}
		return ordID, co.PaymentId, reqRet.ReturnRequestId
	}

	t.Run("4a_OutOfOrder_MatchByIdempotencyKey_EvenWhenAmountDiffers", func(t *testing.T) {
		h := newTestHarness()
		ordID, payID, retID := seedEscalatedReturn(t, h, "4a")
		podB := NewOrderService(h.svc.cfg, h.store, h.svc.clients, h.svc.logger)

		h.payment.onCreateRefund = func(_ context.Context, req *paymentv1.CreateRefundRequest) (*paymentv1.CreateRefundResponse, error) {
			// Out-of-order rejection arrives before return_requests persists refund_id.
			// Amount intentionally differs from rr.RefundAmountVND (e.g. after earlier partial refund),
			// while idempotency_key matches returnRefundIdempotencyKey(ordID, 0).
			if err := podB.HandlePaymentRefundRejected(context.Background(), "ev-p4-c-4a-rej", events.PaymentRefundRejectedPayload{
				RefundID:       "ref-4a-1",
				PaymentID:      payID,
				OrderID:        ordID,
				AmountVND:      req.GetAmountVnd() - 500000, // different amount!
				IdempotencyKey: req.GetIdempotencyKey(),
				Reason:         "checker rejected",
				RejectedBy:     "accountant-1",
			}); err != nil {
				t.Errorf("podB HandlePaymentRefundRejected: %v", err)
			}
			return &paymentv1.CreateRefundResponse{
				Refund: &paymentv1.Refund{
					RefundId: "ref-4a-1", PaymentId: payID, OrderId: ordID,
					AmountVnd: req.GetAmountVnd(), Status: paymentv1.RefundStatus_REFUND_STATUS_PENDING_APPROVAL,
				},
			}, nil
		}

		res, err := h.svc.AdminResolveReturn(cskhCtx, &orderv1.AdminResolveReturnRequest{
			ReturnRequestId: retID, Decision: orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_REFUND,
		})
		if err != nil {
			t.Fatalf("AdminResolveReturn: %v", err)
		}
		if res.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_ESCALATED {
			t.Fatalf("Item C (#4a): idempotency_key match must transition REFUNDING -> ESCALATED even if amount differs, got %v", res.ReturnRequest.Status)
		}
	})

	t.Run("4b_ManualAdminRefund_SameAmountDifferentIdemKey_DoesNotMatch", func(t *testing.T) {
		h := newTestHarness()
		ordID, payID, retID := seedEscalatedReturn(t, h, "4b")
		podB := NewOrderService(h.svc.cfg, h.store, h.svc.clients, h.svc.logger)

		h.payment.onCreateRefund = func(_ context.Context, req *paymentv1.CreateRefundRequest) (*paymentv1.CreateRefundResponse, error) {
			// Unrelated manual admin refund on the same order with identical amount & payment_id,
			// but DIFFERENT idempotency_key -> MUST NOT match the pending return!
			if err := podB.HandlePaymentRefundRejected(context.Background(), "ev-p4-c-4b-manual", events.PaymentRefundRejectedPayload{
				RefundID:       "ref-manual-other-4b",
				PaymentID:      payID,
				OrderID:        ordID,
				AmountVND:      req.GetAmountVnd(),
				IdempotencyKey: "manual-admin-refund:other-key",
				Reason:         "manual refund rejected",
				RejectedBy:     "accountant-1",
			}); err != nil {
				t.Errorf("podB HandlePaymentRefundRejected: %v", err)
			}
			return &paymentv1.CreateRefundResponse{
				Refund: &paymentv1.Refund{
					RefundId: "ref-return-real-4b", PaymentId: payID, OrderId: ordID,
					AmountVnd: req.GetAmountVnd(), Status: paymentv1.RefundStatus_REFUND_STATUS_PENDING_APPROVAL,
				},
			}, nil
		}

		res, err := h.svc.AdminResolveReturn(cskhCtx, &orderv1.AdminResolveReturnRequest{
			ReturnRequestId: retID, Decision: orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_REFUND,
		})
		if err != nil {
			t.Fatalf("AdminResolveReturn: %v", err)
		}
		if res.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_REFUNDING {
			t.Fatalf("Item C (#4b): manual admin refund with different idempotency_key MUST NOT corrupt return, got %v", res.ReturnRequest.Status)
		}
		rr, _ := h.store.GetReturnRequest(context.Background(), retID, "")
		if rr.Request.RefundApprovalStatus != "PENDING_APPROVAL" || rr.Request.RefundID != "ref-return-real-4b" {
			t.Fatalf("Item C (#4b): expected return in PENDING_APPROVAL with ref-return-real-4b, got %+v", rr.Request)
		}
	})

	t.Run("LegacyEvent_MissingCorrelationKeys_IgnoredAndLogsWarn", func(t *testing.T) {
		h := newTestHarness()
		var logBuf bytes.Buffer
		h.svc.logger = slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelWarn}))
		ordID, payID, retID := seedEscalatedReturn(t, h, "legacy-warn")
		podB := NewOrderService(h.svc.cfg, h.store, h.svc.clients, h.svc.logger)

		h.payment.onCreateRefund = func(_ context.Context, req *paymentv1.CreateRefundRequest) (*paymentv1.CreateRefundResponse, error) {
			// Legacy event without idempotency_key while rr.RefundID == "" -> MUST NOT match and MUST log WARN.
			if err := podB.HandlePaymentRefundRejected(context.Background(), "ev-p4-c-legacy-rej", events.PaymentRefundRejectedPayload{
				RefundID:   "ref-legacy-1",
				PaymentID:  payID,
				OrderID:    ordID,
				AmountVND:  req.GetAmountVnd(),
				Reason:     "legacy rejected",
				RejectedBy: "accountant-1",
			}); err != nil {
				t.Errorf("podB HandlePaymentRefundRejected: %v", err)
			}
			// Also legacy payment.refunded missing both refund_id and idempotency_key -> MUST NOT match and MUST log WARN.
			if err := podB.HandlePaymentRefunded(context.Background(), "ev-p4-c-legacy-ok", events.PaymentRefundedPayload{
				PaymentID: payID,
				OrderID:   ordID,
				AmountVND: req.GetAmountVnd(),
				Reason:    "legacy refunded",
			}); err != nil {
				t.Errorf("podB HandlePaymentRefunded: %v", err)
			}
			return &paymentv1.CreateRefundResponse{
				Refund: &paymentv1.Refund{
					RefundId: "ref-real-legacy", PaymentId: payID, OrderId: ordID,
					AmountVnd: req.GetAmountVnd(), Status: paymentv1.RefundStatus_REFUND_STATUS_PENDING_APPROVAL,
				},
			}, nil
		}

		res, err := h.svc.AdminResolveReturn(cskhCtx, &orderv1.AdminResolveReturnRequest{
			ReturnRequestId: retID, Decision: orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_REFUND,
		})
		if err != nil {
			t.Fatalf("AdminResolveReturn: %v", err)
		}
		if res.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_REFUNDING {
			t.Fatalf("Item C: legacy event missing idempotency_key MUST NOT match before refund_id is persisted, got %v", res.ReturnRequest.Status)
		}
		if !strings.Contains(logBuf.String(), `"level":"WARN"`) {
			t.Fatalf("Item C: expected WARN log when legacy event lacks correlation keys, got: %s", logBuf.String())
		}
	})

	t.Run("4c_RecordPaymentRefunded_AtomicApprovedInSameTX_WatchdogRecovers", func(t *testing.T) {
		h := newTestHarness()
		ordID, payID, retID := seedEscalatedReturn(t, h, "4c")

		h.payment.onCreateRefund = func(_ context.Context, req *paymentv1.CreateRefundRequest) (*paymentv1.CreateRefundResponse, error) {
			return &paymentv1.CreateRefundResponse{
				Refund: &paymentv1.Refund{
					RefundId: "ref-4c-big", PaymentId: payID, OrderId: ordID,
					AmountVnd: req.GetAmountVnd(), Status: paymentv1.RefundStatus_REFUND_STATUS_PENDING_APPROVAL,
				},
			}, nil
		}
		if _, err := h.svc.AdminResolveReturn(cskhCtx, &orderv1.AdminResolveReturnRequest{
			ReturnRequestId: retID, Decision: orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_REFUND,
		}); err != nil {
			t.Fatalf("AdminResolveReturn: %v", err)
		}

		// RecordPaymentRefunded directly (simulating crash immediately after TX before onPaymentRefundApprovedForReturn runs).
		dup, err := h.store.RecordPaymentRefunded(context.Background(), store.PaymentRefundedParams{
			EventID:        "ev-p4-c-4c-ref",
			EventType:      events.SubjectPaymentRefunded,
			PaymentID:      payID,
			OrderID:        ordID,
			RefundID:       "ref-4c-big",
			IdempotencyKey: "order-return-refund:" + ordID,
			AmountVND:      2030000,
			Reason:         "checker approved",
		})
		if err != nil || dup {
			t.Fatalf("RecordPaymentRefunded: dup=%v err=%v", dup, err)
		}

		// IMMEDIATELY after RecordPaymentRefunded's TX (without calling onPaymentRefundApprovedForReturn),
		// return_requests MUST already have refund_approval_status='APPROVED' and refund_done=TRUE!
		rrAfterTX, err := h.store.GetReturnRequest(context.Background(), retID, "")
		if err != nil {
			t.Fatalf("GetReturnRequest: %v", err)
		}
		if rrAfterTX.Request.RefundApprovalStatus != "APPROVED" || !rrAfterTX.Request.RefundDone {
			t.Fatalf("Item C (#4c): RecordPaymentRefunded TX must atomically set APPROVED + refund_done=true, got status=%q done=%v",
				rrAfterTX.Request.RefundApprovalStatus, rrAfterTX.Request.RefundDone)
		}

		// FindRefundingReturnRequests MUST include the APPROVED unfinished return, and ScanRefundingReturns completes it!
		ids, err := h.store.FindRefundingReturnRequests(context.Background(), 20)
		if err != nil || len(ids) != 1 || ids[0] != retID {
			t.Fatalf("Item C (#4c): FindRefundingReturnRequests must return APPROVED return %s, got %v err=%v", retID, ids, err)
		}
		h.payment.mu.Lock()
		beforeRefundCalls := len(h.payment.refundCalls)
		h.payment.mu.Unlock()
		n, err := h.svc.ScanRefundingReturns(context.Background())
		if err != nil || n != 1 {
			t.Fatalf("ScanRefundingReturns = %d, %v; want 1, nil", n, err)
		}
		rrDone, _ := h.store.GetReturnRequest(context.Background(), retID, "")
		if rrDone.Request.Status != "REFUNDED" {
			t.Fatalf("Item C (#4c): ScanRefundingReturns must finish REFUNDED, got %s", rrDone.Request.Status)
		}
		h.payment.mu.Lock()
		afterRefundCalls := len(h.payment.refundCalls)
		h.payment.mu.Unlock()
		if afterRefundCalls != beforeRefundCalls {
			t.Fatalf("ScanRefundingReturns must not re-invoke CreateRefund after refund_done=true: before=%d after=%d", beforeRefundCalls, afterRefundCalls)
		}
	})
}

// TestW3B2P4_E_OrderReturnStateMachineAndOutboxMetadata verifies Item E (#5 minimal):
//   - statemachine declares RETURNED -> RETURN_REQUESTED and ValidateTransition permits it.
//   - Buyer RequestReturn MUST still reject orders in RETURNED status (path restricted to refund-rejected rollback).
//   - order.return_updated carries refund_approval_status and uses reason="refund_rejected" on REFUNDING->ESCALATED.
func TestW3B2P4_E_OrderReturnStateMachineAndOutboxMetadata(t *testing.T) {
	if err := statemachine.ValidateTransition(
		orderv1.OrderStatus_ORDER_STATUS_RETURNED,
		orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED,
	); err != nil {
		t.Fatalf("Item E: statemachine.ValidateTransition(RETURNED -> RETURN_REQUESTED) must succeed, got %v", err)
	}

	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
	cskhCtx := ctxWithUser("cskh-maker-e", auth.RoleCSKH, "")

	h := newTestHarness()
	co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "p4-e-co-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 2}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
	})
	if err != nil {
		t.Fatalf("CreateCheckout: %v", err)
	}
	ordID := co.Orders[0].Id
	_ = h.svc.HandlePaymentSucceeded(context.Background(), "ev-p4-e-pay", events.PaymentSucceededPayload{
		PaymentID: co.PaymentId, OrderGroupID: co.CheckoutGroupId, OrderID: ordID,
	})
	shipRes, _ := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID})
	_ = h.svc.HandleShipmentUpdated(context.Background(), "ev-p4-e-sh1", events.ShipmentUpdatedPayload{
		OrderID: ordID, TrackingCode: shipRes.TrackingCode, Status: "delivering",
	})
	_ = h.svc.HandleShipmentUpdated(context.Background(), "ev-p4-e-sh2", events.ShipmentUpdatedPayload{
		OrderID: ordID, TrackingCode: shipRes.TrackingCode, Status: "delivered",
	})

	// Create return -> seller reject -> escalate -> admin refund (PENDING_APPROVAL) -> order is now RETURNED.
	reqRet, _ := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ordID, Reason: "defective"})
	_, _ = h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
		ReturnRequestId: reqRet.ReturnRequestId, Decision: orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_REJECT, Note: "no",
	})
	_, _ = h.svc.EscalateReturn(buyerCtx, &orderv1.EscalateReturnRequest{ReturnRequestId: reqRet.ReturnRequestId, Reason: "dispute"})
	h.payment.onCreateRefund = func(_ context.Context, req *paymentv1.CreateRefundRequest) (*paymentv1.CreateRefundResponse, error) {
		return &paymentv1.CreateRefundResponse{
			Refund: &paymentv1.Refund{
				RefundId: "ref-e-1", PaymentId: req.GetPaymentId(), OrderId: req.GetOrderId(),
				AmountVnd: req.GetAmountVnd(), Status: paymentv1.RefundStatus_REFUND_STATUS_PENDING_APPROVAL,
			},
		}, nil
	}
	if _, err := h.svc.AdminResolveReturn(cskhCtx, &orderv1.AdminResolveReturnRequest{
		ReturnRequestId: reqRet.ReturnRequestId, Decision: orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_REFUND,
	}); err != nil {
		t.Fatalf("AdminResolveReturn: %v", err)
	}

	// Buyer RequestReturn on an order already in RETURNED status MUST STILL be rejected!
	if _, err := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ordID, Reason: "try again"}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("Item E: RequestReturn on RETURNED order must fail with FailedPrecondition, got %v", err)
	}

	// Reject the large refund -> REFUNDING -> ESCALATED with order.return_updated having
	// reason="refund_rejected" and refund_approval_status="REJECTED".
	if err := h.svc.HandlePaymentRefundRejected(context.Background(), "ev-p4-e-rej", events.PaymentRefundRejectedPayload{
		RefundID: "ref-e-1", PaymentID: co.PaymentId, OrderID: ordID,
		IdempotencyKey: "order-return-refund:" + ordID, Reason: "suspicious invoice", RejectedBy: "accountant-1",
	}); err != nil {
		t.Fatalf("HandlePaymentRefundRejected: %v", err)
	}

	outbox := h.store.OutboxEvents()
	foundRejEvent := false
	for _, ob := range outbox {
		if ob.EventType != events.SubjectOrderReturnUpdated {
			continue
		}
		var p events.OrderReturnUpdatedPayload
		if err := json.Unmarshal(ob.PayloadJSON, &p); err != nil {
			t.Fatalf("unmarshal order.return_updated: %v", err)
		}
		if p.PreviousStatus == "REFUNDING" && p.Status == "ESCALATED" {
			foundRejEvent = true
			if p.Reason != "refund_rejected" {
				t.Fatalf("Item E: expected order.return_updated reason=%q on refund rejection, got %q", "refund_rejected", p.Reason)
			}
			if p.RefundApprovalStatus != "REJECTED" {
				t.Fatalf("Item E: expected order.return_updated refund_approval_status=%q, got %q", "REJECTED", p.RefundApprovalStatus)
			}
		}
	}
	if !foundRejEvent {
		t.Fatalf("Item E: did not find REFUNDING->ESCALATED order.return_updated outbox event")
	}

	// Re-resolve with immediate approval -> REFUNDING -> REFUNDED emits refund_approval_status="APPROVED".
	h.payment.onCreateRefund = func(_ context.Context, req *paymentv1.CreateRefundRequest) (*paymentv1.CreateRefundResponse, error) {
		return &paymentv1.CreateRefundResponse{
			Refund: &paymentv1.Refund{
				RefundId: "ref-e-2", PaymentId: req.GetPaymentId(), OrderId: req.GetOrderId(),
				AmountVnd: req.GetAmountVnd(), Status: paymentv1.RefundStatus_REFUND_STATUS_DONE,
			},
		}, nil
	}
	if _, err := h.svc.AdminResolveReturn(cskhCtx, &orderv1.AdminResolveReturnRequest{
		ReturnRequestId: reqRet.ReturnRequestId, Decision: orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_REFUND,
	}); err != nil {
		t.Fatalf("second AdminResolveReturn: %v", err)
	}
	outbox2 := h.store.OutboxEvents()
	foundApproved := false
	for _, ob := range outbox2 {
		if ob.EventType != events.SubjectOrderReturnUpdated {
			continue
		}
		var p events.OrderReturnUpdatedPayload
		_ = json.Unmarshal(ob.PayloadJSON, &p)
		if p.Status == "REFUNDED" {
			foundApproved = true
			if p.RefundApprovalStatus != "APPROVED" {
				t.Fatalf("Item E: expected REFUNDED order.return_updated refund_approval_status=%q, got %q", "APPROVED", p.RefundApprovalStatus)
			}
		}
	}
	if !foundApproved {
		t.Fatalf("Item E: did not find REFUNDED order.return_updated outbox event")
	}
}

// TestW3B2P4_F_RetryPaymentResilience_AndExpiredWatchdogActiveLookup verifies Item F (#6):
//  1. RetryPayment runs CreatePayment with detachedCallerContext (client cancel does not abort CreatePayment).
//  2. Ambiguous CreatePayment error probes GetPayment(OrderGroupId) and recovers newly created PENDING payment.
//  3. Transient UpdateGroupPaymentOnRetry errors retry up to 3 attempts with backoff (non-transient errors do not retry).
//  4. processExpiredGroup queries active payment by OrderGroupId (not pinning old PaymentID) and cancels orphan P2 + expires order.
func TestW3B2P4_F_RetryPaymentResilience_AndExpiredWatchdogActiveLookup(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")

	t.Run("RetryPayment_CreatePaymentUsesDetachedContext", func(t *testing.T) {
		h := newTestHarness()
		co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "p4-f-detach",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}

		reqCtx, cancelReq := context.WithCancel(buyerCtx)
		var sawCtxErr error
		h.payment.onCreatePayCtx = func(callCtx context.Context, _ *paymentv1.CreatePaymentRequest) {
			cancelReq() // client disconnects mid-CreatePayment
			sawCtxErr = callCtx.Err()
		}

		res, err := h.svc.RetryPayment(reqCtx, &orderv1.RetryPaymentRequest{
			PaymentId:      co.PaymentId,
			IdempotencyKey: "p4-f-detach-rk",
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY,
		})
		if err != nil {
			t.Fatalf("Item F: RetryPayment must survive client disconnect during CreatePayment, got %v", err)
		}
		if sawCtxErr != nil {
			t.Fatalf("Item F: CreatePayment context was cancelled by client disconnect: %v", sawCtxErr)
		}
		if res.PaymentId == "" || res.PaymentId == co.PaymentId {
			t.Fatalf("Item F: expected new payment_id, got %q", res.PaymentId)
		}
	})

	t.Run("RetryPayment_AmbiguousCreatePaymentError_ProbesGetPaymentAndRecovers", func(t *testing.T) {
		h := newTestHarness()
		co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "p4-f-ambig",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}

		// Simulate payment-service actually creating P2 before returning an ambiguous DeadlineExceeded.
		newPayID := "pay-ambig-recovered-2"
		h.payment.createErr = status.Error(codes.DeadlineExceeded, "context deadline exceeded after commit")
		var probeReq *paymentv1.GetPaymentRequest
		h.payment.onGetPayment = func(req *paymentv1.GetPaymentRequest) (*paymentv1.GetPaymentResponse, error) {
			probeReq = req
			return &paymentv1.GetPaymentResponse{
				Payment: &paymentv1.Payment{
					PaymentId:    newPayID,
					OrderGroupId: co.CheckoutGroupId,
					Method:       paymentv1.PaymentMethod_PAYMENT_METHOD_ZALOPAY,
					Status:       paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING,
					PayUrl:       "https://sb-openapi.zalopay.vn/pay/" + newPayID,
				},
			}, nil
		}

		res, err := h.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
			PaymentId:      co.PaymentId,
			IdempotencyKey: "p4-f-ambig-rk",
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY,
		})
		if err != nil {
			t.Fatalf("Item F: RetryPayment must probe GetPayment(OrderGroupId) and recover %s on ambiguous error, got %v", newPayID, err)
		}
		if probeReq == nil || probeReq.GetOrderGroupId() != co.CheckoutGroupId || probeReq.GetPaymentId() != "" {
			t.Fatalf("Item F: probe GetPayment must query by OrderGroupId with empty PaymentId, got %+v", probeReq)
		}
		if res.PaymentId != newPayID || res.PayUrl == "" {
			t.Fatalf("Item F: expected recovered payment %s with pay_url, got %+v", newPayID, res)
		}
	})

	t.Run("RetryPayment_TransientUpdateGroupPaymentError_RetriesUpTo3Times", func(t *testing.T) {
		h := newTestHarness()
		co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "p4-f-retry-db",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}

		wrapped := &failingRetryUpdateStore{
			Store:        h.store,
			failAttempts: 2, // fail 2 times, succeed on 3rd attempt
			failErr:      errors.New("transient pg connection reset"),
		}
		h.svc.store = wrapped

		res, err := h.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
			PaymentId:      co.PaymentId,
			IdempotencyKey: "p4-f-retry-db-rk",
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY,
		})
		if err != nil {
			t.Fatalf("Item F: expected RetryPayment to succeed on 3rd UpdateGroupPaymentOnRetry attempt, got %v", err)
		}
		if gotCalls := wrapped.calls.Load(); gotCalls != 3 {
			t.Fatalf("Item F: expected 3 UpdateGroupPaymentOnRetry attempts, got %d", gotCalls)
		}
		if res.PaymentId == "" || res.PaymentId == co.PaymentId {
			t.Fatalf("Item F: expected new payment_id, got %q", res.PaymentId)
		}
	})

	t.Run("ProcessExpiredGroup_QueriesByOrderGroupID_CancelsOrphanP2AndExpiresOrder", func(t *testing.T) {
		h := newTestHarness()
		co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "p4-f-orphan-exp",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		p1 := co.PaymentId
		ordID := co.Orders[0].Id
		orphanP2 := "pay-orphan-active-2"

		// Expire the checkout group while order_db still has P1, and payment-service has P1 CANCELLED + orphan P2 PENDING.
		h.store.SetOrderPaymentExpiresAt(ordID, time.Now().UTC().Add(-time.Minute))
		var observedGetReq *paymentv1.GetPaymentRequest
		h.payment.onGetPayment = func(req *paymentv1.GetPaymentRequest) (*paymentv1.GetPaymentResponse, error) {
			observedGetReq = req
			if req.GetPaymentId() == p1 {
				// Mirror real payment-service: pinning old P1 returns P1 (CANCELLED/superseded) and misses orphan P2!
				return &paymentv1.GetPaymentResponse{
					Payment: &paymentv1.Payment{
						PaymentId: p1, OrderGroupId: co.CheckoutGroupId, Status: paymentv1.PaymentStatus_PAYMENT_STATUS_CANCELLED,
					},
				}, nil
			}
			// Querying by order_group_id alone returns the active orphan P2!
			return &paymentv1.GetPaymentResponse{
				Payment: &paymentv1.Payment{
					PaymentId: orphanP2, OrderGroupId: co.CheckoutGroupId, Status: paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING,
				},
			}, nil
		}

		handled, err := h.svc.ScanExpiredPendingOrders(context.Background())
		if err != nil || handled != 1 {
			t.Fatalf("ScanExpiredPendingOrders = %d, %v; want 1, nil", handled, err)
		}
		if observedGetReq == nil || observedGetReq.GetPaymentId() != "" || observedGetReq.GetOrderGroupId() != co.CheckoutGroupId {
			t.Fatalf("Item F: processExpiredGroup MUST call GetPayment with OrderGroupId only (PaymentId==\"\"), got %+v", observedGetReq)
		}
		h.payment.mu.Lock()
		cancels := append([]*paymentv1.CancelPaymentRequest(nil), h.payment.cancelCalls...)
		h.payment.mu.Unlock()
		if len(cancels) != 1 || cancels[0].GetPaymentId() != orphanP2 {
			t.Fatalf("Item F: processExpiredGroup MUST cancel active orphan payment %s, got cancels=%+v", orphanP2, cancels)
		}
		ord, _ := h.store.GetOrderWithDetails(context.Background(), ordID)
		if ord.Order.Status != "CANCELLED" {
			t.Fatalf("Item F: expired PENDING order must be CANCELLED after cancelling orphan P2, got %s", ord.Order.Status)
		}
	})
}

// TestW3B2P4_G_SuspendedShopReturnsShopSuspendedBeforeSKUInactive verifies Item G (#8):
// verifyShopsActive runs BEFORE checking sku.IsActive (and cart item Unavailable) in CreateCheckout,
// so a suspended shop ALWAYS returns FailedPrecondition SHOP_SUSPENDED (never SKU_INACTIVE).
func TestW3B2P4_G_SuspendedShopReturnsShopSuspendedBeforeSKUInactive(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")

	t.Run("ExplicitItems_RedactedInactiveSKU_ReturnsShopSuspended", func(t *testing.T) {
		h := newTestHarness()
		h.user.shops["shop-1"].Status = userv1.ShopStatus_SHOP_STATUS_SUSPENDED
		// Product already processed user.shop_status_changed and returns redacted SKU (IsActive=false, ShopId preserved).
		h.product.skus["sku-1"].IsActive = false
		h.product.skus["sku-1"].PriceVnd = 0

		_, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "p4-g-explicit",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "SHOP_SUSPENDED") {
			t.Fatalf("Item G: expected FailedPrecondition SHOP_SUSPENDED before SKU_INACTIVE, got %v", err)
		}
		if len(h.product.reserveCalls) != 0 || len(h.promo.reserveCalls) != 0 {
			t.Fatalf("must not reserve stock/promo when shop is suspended")
		}
	})

	t.Run("FromCart_UnavailableSelectedItem_SuspendedShop_ReturnsShopSuspended", func(t *testing.T) {
		h := newTestHarness()
		h.user.shops["shop-1"].Status = userv1.ShopStatus_SHOP_STATUS_SUSPENDED
		h.product.skus["sku-1"].IsActive = false
		h.cart.cart = &cartv1.Cart{
			UserId: "buyer-1",
			ShopGroups: []*cartv1.CartShopGroup{
				{
					ShopId: "shop-1",
					Items: []*cartv1.CartItem{
						{
							SkuId:             "sku-1",
							ShopId:            "shop-1",
							Quantity:          1,
							Selected:          true,
							Unavailable:       true,
							UnavailableReason: "SKU_INACTIVE",
						},
					},
				},
			},
		}

		_, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "p4-g-cart-susp",
			AddressId:      "addr-buyer-1",
			FromCart:       true,
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "SHOP_SUSPENDED") {
			t.Fatalf("Item G (from_cart): expected FailedPrecondition SHOP_SUSPENDED before SKU_INACTIVE, got %v", err)
		}
	})

	t.Run("FromCart_UnavailableSelectedItem_ActiveShop_StillReturnsSKUInactive", func(t *testing.T) {
		h := newTestHarness()
		h.cart.cart = &cartv1.Cart{
			UserId: "buyer-1",
			ShopGroups: []*cartv1.CartShopGroup{
				{
					ShopId: "shop-1",
					Items: []*cartv1.CartItem{
						{
							SkuId:             "sku-1",
							ShopId:            "shop-1",
							Quantity:          1,
							Selected:          true,
							Unavailable:       true,
							UnavailableReason: "SKU_INACTIVE",
						},
					},
				},
			},
		}

		_, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "p4-g-cart-active",
			AddressId:      "addr-buyer-1",
			FromCart:       true,
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "SKU_INACTIVE") {
			t.Fatalf("Item G (active shop): expected SKU_INACTIVE, got %v", err)
		}
	})
}

// TestW3B2P4_H_CODStrictCarrier_CODReplayCancelled_LookupFeeWrap verifies Item H (#11, #13):
//  1. Switching to COD on an order with fixed CarrierCode (!= "fallback") rejects when that exact
//     carrier/service is missing or does not support COD (never falls back to another carrier).
//  2. Replaying RetryPayment(COD, same idemKey) AFTER order is CANCELLED returns codes.FailedPrecondition (409).
//  3. lookupPaymentFeeBPS wraps downstream errors with wrapDownstreamError (preserving gRPC code).
func TestW3B2P4_H_CODStrictCarrier_CODReplayCancelled_LookupFeeWrap(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")

	t.Run("CODSwitch_FixedCarrierNotInQuotes_RejectsWithoutFallback", func(t *testing.T) {
		h := newTestHarness()
		h.shipping.quotes = []*shippingv1.CarrierQuote{
			{CarrierCode: "ghn", ServiceCode: "standard", FeeVnd: 30000, CodSupported: true},
		}
		co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "p4-h-carrier",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
			ShippingChoices: []*orderv1.ShopShippingChoice{
				{ShopId: "shop-1", CarrierCode: "ghn", ServiceCode: "standard"},
			},
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}

		// Now shipping service only returns viettel_post (COD supported) and does NOT return ghn/standard.
		h.shipping.quotes = []*shippingv1.CarrierQuote{
			{CarrierCode: "viettel_post", ServiceCode: "vtp_std", FeeVnd: 25000, CodSupported: true},
		}
		_, err = h.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
			PaymentId:      co.PaymentId,
			IdempotencyKey: "p4-h-carrier-rk",
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "COD_AREA_NOT_SUPPORTED") {
			t.Fatalf("Item H (#11): fixed carrier ghn/standard missing must return COD_AREA_NOT_SUPPORTED (no fallback to viettel_post), got %v", err)
		}
	})

	t.Run("CODReplay_AfterOrderCancelled_ReturnsFailedPrecondition409", func(t *testing.T) {
		h := newTestHarness()
		co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "p4-h-cod-replay",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		if _, err := h.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
			PaymentId:      co.PaymentId,
			IdempotencyKey: "p4-h-cod-replay-rk",
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		}); err != nil {
			t.Fatalf("RetryPayment(COD): %v", err)
		}
		if _, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{
			OrderId: co.Orders[0].Id, Reason: "changed mind",
		}); err != nil {
			t.Fatalf("CancelOrder: %v", err)
		}

		// Replaying the same (oldPaymentID, idemKey, COD) after order is CANCELLED must return 409 FailedPrecondition!
		_, err = h.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
			PaymentId:      co.PaymentId,
			IdempotencyKey: "p4-h-cod-replay-rk",
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("Item H (#11): COD replay on CANCELLED order must return codes.FailedPrecondition (409), got %v", err)
		}
	})

	t.Run("LookupPaymentFeeBPS_WrapsDownstreamErrorWithGRPCCode", func(t *testing.T) {
		h := newTestHarness()
		h.payment.getFeeConfigErr = status.Error(codes.Unavailable, "payment fee service down")

		_, err := h.svc.lookupPaymentFeeBPS(context.Background(), statemachine.PaymentMethodVIETQR, time.Now().UTC())
		if _, direct := err.(interface{ GRPCStatus() *status.Status }); !direct {
			t.Fatalf("Item H (#13): expected direct gRPC status error from wrapDownstreamError, got %T (%v)", err, err)
		}
		st, ok := status.FromError(err)
		if !ok || st.Code() != codes.Unavailable || !strings.Contains(st.Message(), "PAYMENT_FEE_CONFIG_FAILED") {
			t.Fatalf("Item H (#13): lookupPaymentFeeBPS must preserve codes.Unavailable AND message prefix PAYMENT_FEE_CONFIG_FAILED, got ok=%v code=%v msg=%q err=%v", ok, st.Code(), st.Message(), err)
		}

		// Plain non-gRPC transport error must also normalize to codes.Unavailable (never Unknown).
		h.payment.getFeeConfigErr = errors.New("connection reset by peer")
		_, err2 := h.svc.lookupPaymentFeeBPS(context.Background(), statemachine.PaymentMethodVIETQR, time.Now().UTC())
		st2, ok2 := status.FromError(err2)
		if !ok2 || st2.Code() != codes.Unavailable || !strings.Contains(st2.Message(), "PAYMENT_FEE_CONFIG_FAILED") {
			t.Fatalf("Item H (#13): plain error must wrap to codes.Unavailable with prefix, got ok=%v code=%v msg=%q", ok2, st2.Code(), st2.Message())
		}
	})
}
