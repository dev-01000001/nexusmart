package service

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	"github.com/nexusmart/core/services/order/internal/clients"
	"github.com/nexusmart/core/services/order/internal/events"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func createDeliveredOrderHelper(
	t *testing.T,
	h *testHarness,
	idemKey string,
	method orderv1.OrderPaymentMethod,
) *orderv1.Order {
	t.Helper()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

	resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: idemKey,
		BuyerId:        "buyer-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  method,
	})
	if err != nil {
		t.Fatalf("CreateCheckout(%s): %v", idemKey, err)
	}
	ord := resp.Orders[0]
	if method == orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR {
		if err := h.svc.HandlePaymentSucceeded(context.Background(), "evt-pay-"+idemKey, events.PaymentSucceededPayload{
			PaymentID:    resp.PaymentId,
			OrderGroupID: resp.CheckoutGroupId,
		}); err != nil {
			t.Fatalf("HandlePaymentSucceeded: %v", err)
		}
	}
	shipResp, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{
		OrderId: ord.Id,
		ShopId:  "shop-1",
	})
	if err != nil {
		t.Fatalf("SellerShipOrder: %v", err)
	}
	if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-deliv-"+idemKey, events.ShipmentUpdatedPayload{
		OrderID:      ord.Id,
		Status:       "delivered",
		TrackingCode: shipResp.TrackingCode,
		Description:  "Delivered",
	}); err != nil {
		t.Fatalf("HandleShipmentUpdated(delivered): %v", err)
	}
	got, err := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ord.Id})
	if err != nil {
		t.Fatalf("GetOrder: %v", err)
	}
	return got.Order
}

func TestW2A_ReturnWorkflow_EvidenceValidationAndDuplicateGuard(t *testing.T) {
	h := newTestHarness()
	baseNow := time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC)
	h.svc.nowFn = func() time.Time { return baseNow }

	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	ord := createDeliveredOrderHelper(t, h, "w2a-ev-1", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR)

	// Invalid scheme -> InvalidArgument
	if _, err := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{
		OrderId:      ord.Id,
		Reason:       "broken",
		EvidenceUrls: []string{"javascript:alert(1)"},
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("non-http(s) evidence URL = %v, want InvalidArgument", err)
	}

	// Too many URLs (>5) -> InvalidArgument
	if _, err := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{
		OrderId: ord.Id,
		Reason:  "broken",
		EvidenceUrls: []string{
			"https://cdn.example.com/1.jpg",
			"https://cdn.example.com/2.jpg",
			"https://cdn.example.com/3.jpg",
			"https://cdn.example.com/4.jpg",
			"https://cdn.example.com/5.jpg",
			"https://cdn.example.com/6.jpg",
		},
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf(">5 evidence URLs = %v, want InvalidArgument", err)
	}

	// URL > 500 chars -> InvalidArgument
	tooLong := "https://cdn.example.com/" + strings.Repeat("a", 490)
	if _, err := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{
		OrderId:      ord.Id,
		Reason:       "broken",
		EvidenceUrls: []string{tooLong},
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf(">500 char evidence URL = %v, want InvalidArgument", err)
	}

	// Past 7-day window -> FailedPrecondition
	h.svc.nowFn = func() time.Time { return baseNow.Add(7*24*time.Hour + time.Minute) }
	if _, err := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{
		OrderId: ord.Id,
		Reason:  "too late",
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("return past 7d window = %v, want FailedPrecondition", err)
	}

	// Within window with valid URLs -> succeeds, sets 3d deadline and emits order.return_updated
	h.svc.nowFn = func() time.Time { return baseNow.Add(24 * time.Hour) }
	retResp, err := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{
		OrderId:      ord.Id,
		Reason:       "damaged box",
		EvidenceUrls: []string{"https://cdn.example.com/img1.jpg"},
	})
	if err != nil {
		t.Fatalf("RequestReturn valid: %v", err)
	}
	if retResp.Order.ReturnRequestId != retResp.ReturnRequestId || retResp.Order.ReturnStatus != orderv1.ReturnStatus_RETURN_STATUS_PENDING {
		t.Fatalf("order return metadata mismatch: %+v", retResp.Order)
	}

	// Second RequestReturn on same order -> FailedPrecondition
	if _, err := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{
		OrderId: ord.Id,
		Reason:  "duplicate",
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("duplicate RequestReturn = %v, want FailedPrecondition", err)
	}
}

func TestW2A_ReturnWorkflow_AllBranchesAndSideEffects(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
	adminCtx := ctxWithUser("admin-1", "admin", "")

	t.Run("AcceptReturn_ShipmentInTransitReceived_Confirm_RestocksAndRefunds", func(t *testing.T) {
		h := newTestHarness()
		baseNow := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
		h.svc.nowFn = func() time.Time { return baseNow }

		ord := createDeliveredOrderHelper(t, h, "w2a-ret-branch-1", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR)
		retResp, err := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{
			OrderId: ord.Id, Reason: "wrong color",
		})
		if err != nil {
			t.Fatalf("RequestReturn: %v", err)
		}
		retID := retResp.ReturnRequestId

		// Seller ACCEPT_RETURN -> AWAITING_SHIPMENT + CreateReturnShipment via service token + 5d deadline
		resp, err := h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: retID,
			Decision:        orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_RETURN,
			Note:            "Please send back",
		})
		if err != nil {
			t.Fatalf("RespondReturn(ACCEPT_RETURN): %v", err)
		}
		rr := resp.GetReturnRequest()
		if rr.Status != orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT ||
			rr.ReturnTrackingCode == "" || rr.ReturnFeeBearer != "SELLER" || rr.DeadlineAt == nil {
			t.Fatalf("unexpected AWAITING_SHIPMENT return: %+v", rr)
		}
		if len(h.shipping.createReturnShipCalls) != 1 || h.shipping.createReturnShipCalls[0].GetBuyerId() != "buyer-1" {
			t.Fatalf("CreateReturnShipment must set buyer_id=buyer-1, got %+v", h.shipping.createReturnShipCalls)
		}
		if gotAuth := h.shipping.createReturnShipAuth[0]; gotAuth.service != clients.InternalServiceName || gotAuth.rawToken != "" {
			t.Fatalf("CreateReturnShipment must use service token, got %+v", gotAuth)
		}

		// RETURN shipment delivering -> IN_TRANSIT (order stays RETURN_REQUESTED)
		if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-ret-transit", events.ShipmentUpdatedPayload{
			OrderID: ord.Id, Direction: "RETURN", ReturnRequestID: retID, Status: "delivering", TrackingCode: rr.ReturnTrackingCode,
		}); err != nil {
			t.Fatalf("HandleShipmentUpdated(RETURN delivering): %v", err)
		}
		gotRet, _ := h.svc.GetReturnRequest(buyerCtx, &orderv1.GetReturnRequestRequest{ReturnRequestId: retID})
		if gotRet.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_IN_TRANSIT {
			t.Fatalf("status after RETURN delivering = %v, want IN_TRANSIT", gotRet.ReturnRequest.Status)
		}
		gotOrd, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ord.Id})
		if gotOrd.Order.Status != orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED {
			t.Fatalf("order status mutated on RETURN delivering: %v", gotOrd.Order.Status)
		}

		// Duplicate event_id is idempotent
		if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-ret-transit", events.ShipmentUpdatedPayload{
			OrderID: ord.Id, Direction: "RETURN", ReturnRequestID: retID, Status: "delivering",
		}); err != nil {
			t.Fatalf("duplicate RETURN event: %v", err)
		}

		// RETURN shipment delivered -> RECEIVED (goods_received=true, 3d deadline)
		h.svc.nowFn = func() time.Time { return baseNow.Add(24 * time.Hour) }
		if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-ret-recv", events.ShipmentUpdatedPayload{
			OrderID: ord.Id, Direction: "RETURN", ReturnRequestID: retID, Status: "delivered", TrackingCode: rr.ReturnTrackingCode,
		}); err != nil {
			t.Fatalf("HandleShipmentUpdated(RETURN delivered): %v", err)
		}
		gotRet, _ = h.svc.GetReturnRequest(sellerCtx, &orderv1.GetReturnRequestRequest{OrderId: ord.Id})
		if gotRet.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_RECEIVED || !gotRet.ReturnRequest.GoodsReceived || gotRet.ReturnRequest.DeadlineAt == nil {
			t.Fatalf("unexpected RECEIVED return: %+v", gotRet.ReturnRequest)
		}

		// Seller confirms received -> REFUNDING -> REFUNDED; order RETURNED; refund + promo + restock!
		confResp, err := h.svc.ConfirmReturnReceived(sellerCtx, &orderv1.ConfirmReturnReceivedRequest{ReturnRequestId: retID})
		if err != nil {
			t.Fatalf("ConfirmReturnReceived: %v", err)
		}
		if confResp.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_REFUNDED {
			t.Fatalf("status after ConfirmReturnReceived = %v, want REFUNDED", confResp.ReturnRequest.Status)
		}
		gotOrd, _ = h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ord.Id})
		if gotOrd.Order.Status != orderv1.OrderStatus_ORDER_STATUS_RETURNED {
			t.Fatalf("order status = %v, want RETURNED", gotOrd.Order.Status)
		}
		if len(h.payment.refundCalls) != 1 || h.payment.refundCalls[0].GetIdempotencyKey() != "order-return-refund:"+ord.Id ||
			h.payment.refundCalls[0].GetAmountVnd() != ord.TotalPayableVnd {
			t.Fatalf("unexpected CreateRefund calls: %+v", h.payment.refundCalls)
		}
		if len(h.promo.releaseShopCalls) != 1 {
			t.Fatalf("expected 1 ReleasePromotionShop call, got %d", len(h.promo.releaseShopCalls))
		}
		if len(h.product.restockCalls) != 1 {
			t.Fatalf("expected 1 RestockOrderItems call when goods received & confirmed, got %d", len(h.product.restockCalls))
		}

		// Late RETURN webhook after terminal REFUNDED is ignored.
		if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-ret-late", events.ShipmentUpdatedPayload{
			OrderID: ord.Id, Direction: "RETURN", ReturnRequestID: retID, Status: "delivered",
		}); err != nil {
			t.Fatalf("late RETURN event on terminal return failed: %v", err)
		}
		gotRet, _ = h.svc.GetReturnRequest(buyerCtx, &orderv1.GetReturnRequestRequest{ReturnRequestId: retID})
		if gotRet.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_REFUNDED {
			t.Fatalf("terminal return status changed: %v", gotRet.ReturnRequest.Status)
		}
	})

	t.Run("AcceptRefund_COD_RefundsWithoutRestock", func(t *testing.T) {
		h := newTestHarness()
		ord := createDeliveredOrderHelper(t, h, "w2a-ret-cod", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD)
		retResp, _ := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord.Id, Reason: "missing part"})

		resp, err := h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: retResp.ReturnRequestId,
			Decision:        orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_REFUND,
			Note:            "Keep item, we refund",
		})
		if err != nil {
			t.Fatalf("RespondReturn(ACCEPT_REFUND): %v", err)
		}
		if resp.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_REFUNDED {
			t.Fatalf("status = %v, want REFUNDED", resp.ReturnRequest.Status)
		}
		if len(h.payment.refundCalls) != 1 || h.payment.refundCalls[0].GetAmountVnd() != ord.TotalPayableVnd {
			t.Fatalf("COD delivered return MUST call CreateRefund for P=%d, got %+v", ord.TotalPayableVnd, h.payment.refundCalls)
		}
		if len(h.product.restockCalls) != 0 {
			t.Fatalf("ACCEPT_REFUND MUST NOT restock items, got %d restock calls", len(h.product.restockCalls))
		}
	})

	t.Run("Reject_Escalate_RequestEvidence_AddEvidence_AdminReject_CompletesOrder", func(t *testing.T) {
		h := newTestHarness()
		ord := createDeliveredOrderHelper(t, h, "w2a-ret-esc-rej", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR)
		retResp, _ := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord.Id, Reason: "don't like it"})
		retID := retResp.ReturnRequestId

		// REJECT without note -> InvalidArgument
		if _, err := h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: retID,
			Decision:        orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_REJECT,
		}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("REJECT without note = %v, want InvalidArgument", err)
		}

		// REJECT with note -> REJECTED
		rejResp, err := h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: retID,
			Decision:        orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_REJECT,
			Note:            "Item matches listing",
		})
		if err != nil || rejResp.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_REJECTED {
			t.Fatalf("REJECT failed: %v %+v", err, rejResp)
		}

		// Buyer escalates -> ESCALATED
		escResp, err := h.svc.EscalateReturn(buyerCtx, &orderv1.EscalateReturnRequest{
			ReturnRequestId: retID,
			Reason:          "Size chart is wrong",
			EvidenceUrls:    []string{"https://cdn.example.com/esc1.jpg"},
		})
		if err != nil || escResp.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_ESCALATED {
			t.Fatalf("EscalateReturn failed: %v %+v", err, escResp)
		}

		// Admin requests evidence -> EVIDENCE_REQUESTED
		evReqResp, err := h.svc.AdminResolveReturn(adminCtx, &orderv1.AdminResolveReturnRequest{
			ReturnRequestId: retID,
			Decision:        orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_REQUEST_EVIDENCE,
			Note:            "Need video unboxing",
		})
		if err != nil || evReqResp.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_EVIDENCE_REQUESTED {
			t.Fatalf("AdminResolveReturn(REQUEST_EVIDENCE): %v %+v", err, evReqResp)
		}

		// Buyer submits evidence -> ESCALATED
		addEvResp, err := h.svc.AddReturnEvidence(buyerCtx, &orderv1.AddReturnEvidenceRequest{
			ReturnRequestId: retID,
			Note:            "Attached photo",
			EvidenceUrls:    []string{"https://cdn.example.com/ev2.jpg"},
		})
		if err != nil || addEvResp.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_ESCALATED {
			t.Fatalf("AddReturnEvidence: %v %+v", err, addEvResp)
		}

		// Admin rejects -> CLOSED, order becomes COMPLETED + order.completed outbox emitted with reward coins!
		resResp, err := h.svc.AdminResolveReturn(adminCtx, &orderv1.AdminResolveReturnRequest{
			ReturnRequestId: retID,
			Decision:        orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_REJECT,
			Note:            "Insufficient proof",
		})
		if err != nil || resResp.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_CLOSED {
			t.Fatalf("AdminResolveReturn(REJECT): %v %+v", err, resResp)
		}
		gotOrd, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ord.Id})
		if gotOrd.Order.Status != orderv1.OrderStatus_ORDER_STATUS_COMPLETED {
			t.Fatalf("order status after CLOSED = %v, want COMPLETED", gotOrd.Order.Status)
		}
		var completedPayload events.OrderCompletedPayload
		foundCompleted := false
		for _, ev := range h.store.OutboxEvents() {
			if ev.EventType == events.SubjectOrderCompleted {
				_ = json.Unmarshal(ev.PayloadJSON, &completedPayload)
				foundCompleted = true
			}
		}
		if !foundCompleted || completedPayload.OrderID != ord.Id || completedPayload.CoinsReward <= 0 {
			t.Fatalf("expected order.completed outbox with coins reward on CLOSED, found=%v payload=%+v", foundCompleted, completedPayload)
		}
		if len(h.payment.refundCalls) != 0 || len(h.product.restockCalls) != 0 {
			t.Fatalf("CLOSED must not refund or restock: refunds=%d restocks=%d", len(h.payment.refundCalls), len(h.product.restockCalls))
		}
	})

	t.Run("Received_DisputeWrongGoods_AdminCannotReturnAndRefund_AdminRefundDoesNotRestock", func(t *testing.T) {
		h := newTestHarness()
		ord := createDeliveredOrderHelper(t, h, "w2a-ret-dispute", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR)
		retResp, _ := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord.Id, Reason: "defective"})
		retID := retResp.ReturnRequestId
		accResp, _ := h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: retID, Decision: orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_RETURN,
		})
		_ = h.svc.HandleShipmentUpdated(context.Background(), "evt-disp-recv", events.ShipmentUpdatedPayload{
			OrderID: ord.Id, Direction: "RETURN", ReturnRequestID: retID, TrackingCode: accResp.ReturnRequest.ReturnTrackingCode, Status: "delivered",
		})

		// Dispute without reason -> InvalidArgument
		if _, err := h.svc.DisputeReturnGoods(sellerCtx, &orderv1.DisputeReturnGoodsRequest{
			ReturnRequestId: retID,
		}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("DisputeReturnGoods without reason = %v, want InvalidArgument", err)
		}

		// Seller disputes wrong goods -> ESCALATED (goods_received stays true)
		dispResp, err := h.svc.DisputeReturnGoods(sellerCtx, &orderv1.DisputeReturnGoodsRequest{
			ReturnRequestId: retID,
			Reason:          "Buyer returned a brick instead of keyboard",
			EvidenceUrls:    []string{"https://cdn.example.com/brick.jpg"},
		})
		if err != nil || dispResp.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_ESCALATED || !dispResp.ReturnRequest.GoodsReceived {
			t.Fatalf("DisputeReturnGoods failed: %v %+v", err, dispResp)
		}

		// Admin RETURN_AND_REFUND forbidden because goods_received == true!
		if _, err := h.svc.AdminResolveReturn(adminCtx, &orderv1.AdminResolveReturnRequest{
			ReturnRequestId: retID,
			Decision:        orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_RETURN_AND_REFUND,
		}); status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("Admin RETURN_AND_REFUND when goods already received = %v, want FailedPrecondition", err)
		}

		// Admin REFUND -> REFUNDED, MUST NOT restock (disputed wrong-goods branch)!
		resResp, err := h.svc.AdminResolveReturn(adminCtx, &orderv1.AdminResolveReturnRequest{
			ReturnRequestId: retID,
			Decision:        orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_REFUND,
			Note:            "Refund buyer after review",
			ReturnFeeBearer: "PLATFORM",
		})
		if err != nil || resResp.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_REFUNDED {
			t.Fatalf("AdminResolveReturn(REFUND): %v %+v", err, resResp)
		}
		if resResp.ReturnRequest.ReturnFeeBearer != "PLATFORM" {
			t.Fatalf("return_fee_bearer = %q, want PLATFORM", resResp.ReturnRequest.ReturnFeeBearer)
		}
		if len(h.payment.refundCalls) != 1 {
			t.Fatalf("expected 1 CreateRefund, got %d", len(h.payment.refundCalls))
		}
		if len(h.product.restockCalls) != 0 {
			t.Fatalf("disputed wrong-goods branch MUST NOT restock, got %d calls", len(h.product.restockCalls))
		}
	})

	t.Run("EscalatedFromRejected_AdminReturnAndRefund_CreatesShipmentWithBearer", func(t *testing.T) {
		h := newTestHarness()
		ord := createDeliveredOrderHelper(t, h, "w2a-ret-admin-rnr", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR)
		retResp, _ := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord.Id, Reason: "scratched"})
		retID := retResp.ReturnRequestId
		_, _ = h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: retID, Decision: orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_REJECT, Note: "minor",
		})
		_, _ = h.svc.EscalateReturn(buyerCtx, &orderv1.EscalateReturnRequest{
			ReturnRequestId: retID, Reason: "clearly damaged",
		})

		resResp, err := h.svc.AdminResolveReturn(adminCtx, &orderv1.AdminResolveReturnRequest{
			ReturnRequestId: retID,
			Decision:        orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_RETURN_AND_REFUND,
			Note:            "Buyer returns item, buyer bears fee",
			ReturnFeeBearer: "BUYER",
		})
		if err != nil {
			t.Fatalf("AdminResolveReturn(RETURN_AND_REFUND): %v", err)
		}
		rr := resResp.ReturnRequest
		if rr.Status != orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT || rr.ReturnFeeBearer != "BUYER" || rr.ReturnTrackingCode == "" {
			t.Fatalf("unexpected RETURN_AND_REFUND result: %+v", rr)
		}
	})
}

func TestW2A_ReturnWorkflow_RBACAndForbiddenTransitions(t *testing.T) {
	h := newTestHarness()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	otherBuyerCtx := ctxWithUser("buyer-2", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
	otherSellerCtx := ctxWithUser("seller-2", "seller", "shop-2")
	adminCtx := ctxWithUser("admin-1", "admin", "")

	ord := createDeliveredOrderHelper(t, h, "w2a-ret-rbac", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR)
	retResp, _ := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord.Id, Reason: "defect"})
	retID := retResp.ReturnRequestId

	// Other buyer & other seller cannot view
	if _, err := h.svc.GetReturnRequest(otherBuyerCtx, &orderv1.GetReturnRequestRequest{ReturnRequestId: retID}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("other buyer GetReturnRequest = %v, want PermissionDenied", err)
	}
	if _, err := h.svc.GetReturnRequest(otherSellerCtx, &orderv1.GetReturnRequestRequest{ReturnRequestId: retID}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("other seller GetReturnRequest = %v, want PermissionDenied", err)
	}
	// Admin can view
	if _, err := h.svc.GetReturnRequest(adminCtx, &orderv1.GetReturnRequestRequest{ReturnRequestId: retID}); err != nil {
		t.Fatalf("admin GetReturnRequest failed: %v", err)
	}
	// Non-admin cannot list admin scope
	if _, err := h.svc.ListReturnRequests(buyerCtx, &orderv1.ListReturnRequestsRequest{RoleScope: "admin"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("buyer ListReturnRequests(admin) = %v, want PermissionDenied", err)
	}
	// Other seller / buyer / admin cannot RespondReturn
	for name, ctx := range map[string]context.Context{"otherSeller": otherSellerCtx, "buyer": buyerCtx, "admin": adminCtx} {
		if _, err := h.svc.RespondReturn(ctx, &orderv1.RespondReturnRequest{
			ReturnRequestId: retID, Decision: orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_REFUND,
		}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("%s RespondReturn = %v, want PermissionDenied", name, err)
		}
	}
	// Buyer / seller cannot AdminResolveReturn
	for name, ctx := range map[string]context.Context{"buyer": buyerCtx, "seller": sellerCtx} {
		if _, err := h.svc.AdminResolveReturn(ctx, &orderv1.AdminResolveReturnRequest{
			ReturnRequestId: retID, Decision: orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_REFUND,
		}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("%s AdminResolveReturn = %v, want PermissionDenied", name, err)
		}
	}
	// Forbidden transition: EscalateReturn while PENDING -> FailedPrecondition
	if _, err := h.svc.EscalateReturn(buyerCtx, &orderv1.EscalateReturnRequest{
		ReturnRequestId: retID, Reason: "premature",
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("EscalateReturn from PENDING = %v, want FailedPrecondition", err)
	}
	// Forbidden transition: ConfirmReturnReceived while PENDING -> FailedPrecondition
	if _, err := h.svc.ConfirmReturnReceived(sellerCtx, &orderv1.ConfirmReturnReceivedRequest{
		ReturnRequestId: retID,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("ConfirmReturnReceived from PENDING = %v, want FailedPrecondition", err)
	}
	// Move to REJECTED then test other buyer cannot EscalateReturn
	_, _ = h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
		ReturnRequestId: retID, Decision: orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_REJECT, Note: "no",
	})
	if _, err := h.svc.EscalateReturn(otherBuyerCtx, &orderv1.EscalateReturnRequest{
		ReturnRequestId: retID, Reason: "hijack",
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("other buyer EscalateReturn = %v, want PermissionDenied", err)
	}
	// List scopes return accurate counts
	bList, _ := h.svc.ListReturnRequests(buyerCtx, &orderv1.ListReturnRequestsRequest{RoleScope: "buyer"})
	sList, _ := h.svc.ListReturnRequests(sellerCtx, &orderv1.ListReturnRequestsRequest{RoleScope: "seller"})
	s2List, _ := h.svc.ListReturnRequests(otherSellerCtx, &orderv1.ListReturnRequestsRequest{RoleScope: "seller"})
	aList, _ := h.svc.ListReturnRequests(adminCtx, &orderv1.ListReturnRequestsRequest{RoleScope: "admin"})
	if bList.Total != 1 || sList.Total != 1 || s2List.Total != 0 || aList.Total != 1 {
		t.Fatalf("list totals mismatch: buyer=%d seller1=%d seller2=%d admin=%d", bList.Total, sList.Total, s2List.Total, aList.Total)
	}
}

func TestW2A_ReturnWorkflow_ConcurrentRaceOnlyOneWins(t *testing.T) {
	h := newTestHarness()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

	ord := createDeliveredOrderHelper(t, h, "w2a-ret-race", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR)
	retResp, _ := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord.Id, Reason: "defect"})
	retID := retResp.ReturnRequestId

	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, errs[0] = h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: retID,
			Decision:        orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_REFUND,
			Note:            "refund",
		})
	}()
	go func() {
		defer wg.Done()
		_, errs[1] = h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: retID,
			Decision:        orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_REJECT,
			Note:            "reject",
		})
	}()
	wg.Wait()

	successes := 0
	precondFails := 0
	for _, e := range errs {
		if e == nil {
			successes++
		} else if status.Code(e) == codes.FailedPrecondition {
			precondFails++
		} else {
			t.Fatalf("unexpected concurrent error: %v", e)
		}
	}
	if successes != 1 || precondFails != 1 {
		t.Fatalf("expected exactly 1 success and 1 FailedPrecondition in race, got successes=%d precondFails=%d errs=%v", successes, precondFails, errs)
	}

	// Also verify concurrent ConfirmReturnReceived vs DisputeReturnGoods from RECEIVED.
	ord2 := createDeliveredOrderHelper(t, h, "w2a-ret-race-2", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR)
	ret2, _ := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord2.Id, Reason: "defect"})
	acc2, _ := h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
		ReturnRequestId: ret2.ReturnRequestId, Decision: orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_RETURN,
	})
	_ = h.svc.HandleShipmentUpdated(context.Background(), "evt-race-recv", events.ShipmentUpdatedPayload{
		OrderID: ord2.Id, Direction: "RETURN", ReturnRequestID: ret2.ReturnRequestId, TrackingCode: acc2.ReturnRequest.ReturnTrackingCode, Status: "delivered",
	})
	errs2 := make([]error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, errs2[0] = h.svc.ConfirmReturnReceived(sellerCtx, &orderv1.ConfirmReturnReceivedRequest{ReturnRequestId: ret2.ReturnRequestId})
	}()
	go func() {
		defer wg.Done()
		_, errs2[1] = h.svc.DisputeReturnGoods(sellerCtx, &orderv1.DisputeReturnGoodsRequest{
			ReturnRequestId: ret2.ReturnRequestId, Reason: "wrong item",
		})
	}()
	wg.Wait()
	s2, p2 := 0, 0
	for _, e := range errs2 {
		if e == nil {
			s2++
		} else if status.Code(e) == codes.FailedPrecondition {
			p2++
		} else {
			t.Fatalf("unexpected concurrent RECEIVED error: %v", e)
		}
	}
	if s2 != 1 || p2 != 1 {
		t.Fatalf("expected 1 success + 1 FailedPrecondition on RECEIVED race, got s=%d p=%d errs=%v", s2, p2, errs2)
	}
}

func TestW2A_RefundingAndPendingShipmentWatchdogs_RetryWithoutDuplicateRefund(t *testing.T) {
	h := newTestHarness()
	baseNow := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	h.svc.nowFn = func() time.Time { return baseNow }

	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

	ord := createDeliveredOrderHelper(t, h, "w2a-ret-wd", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR)
	retResp, _ := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord.Id, Reason: "broken"})
	retID := retResp.ReturnRequestId

	// 1. Fail CreateReturnShipment on attempt 1 -> AWAITING_SHIPMENT has empty tracking and immediate 5d deadline!
	h.shipping.createReturnShipErr = status.Error(codes.Unavailable, "shipping down")
	resp, err := h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
		ReturnRequestId: retID,
		Decision:        orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_RETURN,
	})
	if err != nil {
		t.Fatalf("RespondReturn should commit AWAITING_SHIPMENT even if post-commit CreateReturnShipment fails: %v", err)
	}
	wantInitDeadline := baseNow.Add(5 * 24 * time.Hour)
	if resp.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT ||
		resp.ReturnRequest.ReturnTrackingCode != "" ||
		resp.ReturnRequest.DeadlineAt == nil || !resp.ReturnRequest.DeadlineAt.AsTime().Equal(wantInitDeadline) {
		t.Fatalf("expected empty tracking & immediate 5d deadline %v before shipment created, got %+v", wantInitDeadline, resp.ReturnRequest)
	}

	// Recover shipping -> ScanReturnPendingShipments attaches tracking and starts 5d deadline from NOW!
	shipCreatedAt := baseNow.Add(2 * time.Hour)
	h.svc.nowFn = func() time.Time { return shipCreatedAt }
	h.shipping.createReturnShipErr = nil
	if n, err := h.svc.ScanReturnPendingShipments(context.Background()); err != nil || n != 1 {
		t.Fatalf("ScanReturnPendingShipments = %d, %v; want 1, nil", n, err)
	}
	gotRet, _ := h.svc.GetReturnRequest(buyerCtx, &orderv1.GetReturnRequestRequest{ReturnRequestId: retID})
	wantDeadline := shipCreatedAt.Add(5 * 24 * time.Hour)
	if gotRet.ReturnRequest.ReturnTrackingCode == "" || gotRet.ReturnRequest.DeadlineAt == nil ||
		!gotRet.ReturnRequest.DeadlineAt.AsTime().Equal(wantDeadline) {
		t.Fatalf("expected tracking + 5d deadline %v, got %+v", wantDeadline, gotRet.ReturnRequest)
	}

	// 2. Deliver return goods -> RECEIVED; then fail CreateRefund on attempt 1 -> stays REFUNDING!
	_ = h.svc.HandleShipmentUpdated(context.Background(), "evt-wd-recv", events.ShipmentUpdatedPayload{
		OrderID: ord.Id, Direction: "RETURN", ReturnRequestID: retID, TrackingCode: gotRet.ReturnRequest.ReturnTrackingCode, Status: "delivered",
	})
	h.payment.refundErr = status.Error(codes.Unavailable, "payment blip")
	confResp, err := h.svc.ConfirmReturnReceived(sellerCtx, &orderv1.ConfirmReturnReceivedRequest{ReturnRequestId: retID})
	if err != nil {
		t.Fatalf("ConfirmReturnReceived should commit REFUNDING even if CreateRefund fails: %v", err)
	}
	if confResp.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_REFUNDING {
		t.Fatalf("status = %v, want REFUNDING while CreateRefund failing", confResp.ReturnRequest.Status)
	}
	// Order is already RETURNED in the same TX!
	gotOrd, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ord.Id})
	if gotOrd.Order.Status != orderv1.OrderStatus_ORDER_STATUS_RETURNED {
		t.Fatalf("order status = %v, want RETURNED immediately on entering REFUNDING", gotOrd.Order.Status)
	}

	// Recover payment -> ScanRefundingReturns completes refund, promo, restock -> REFUNDED!
	h.payment.refundErr = nil
	h.payment.refundCalls = nil
	if n, err := h.svc.ScanRefundingReturns(context.Background()); err != nil || n != 1 {
		t.Fatalf("ScanRefundingReturns = %d, %v; want 1, nil", n, err)
	}
	gotRet, _ = h.svc.GetReturnRequest(buyerCtx, &orderv1.GetReturnRequestRequest{ReturnRequestId: retID})
	if gotRet.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_REFUNDED {
		t.Fatalf("status after watchdog = %v, want REFUNDED", gotRet.ReturnRequest.Status)
	}
	if len(h.payment.refundCalls) != 1 || len(h.promo.releaseShopCalls) != 1 || len(h.product.restockCalls) != 1 {
		t.Fatalf("expected 1 refund, 1 promo release, 1 restock; got %d, %d, %d",
			len(h.payment.refundCalls), len(h.promo.releaseShopCalls), len(h.product.restockCalls))
	}

	// Running watchdog again does NOT refund a second time!
	if n, err := h.svc.ScanRefundingReturns(context.Background()); err != nil || n != 0 {
		t.Fatalf("second ScanRefundingReturns = %d, %v; want 0, nil", n, err)
	}
	if len(h.payment.refundCalls) != 1 {
		t.Fatalf("CreateRefund called more than once after REFUNDED: %d", len(h.payment.refundCalls))
	}
}

func TestW2A_ReturnDeadlinesWatchdog_FakeClock(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
	adminCtx := ctxWithUser("admin-1", "admin", "")

	t.Run("PendingTimeout3d_AutoAcceptsReturn_ThenShipTimeout5d_ClosesAndCancelsReturnShipment", func(t *testing.T) {
		h := newTestHarness()
		now := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
		h.svc.nowFn = func() time.Time { return now }

		ord := createDeliveredOrderHelper(t, h, "w2a-dl-1", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR)
		retResp, _ := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord.Id, Reason: "defect"})
		retID := retResp.ReturnRequestId

		// Before 3d: no expiry
		now = now.Add(3*24*time.Hour - time.Minute)
		if n, _ := h.svc.ScanReturnDeadlines(context.Background()); n != 0 {
			t.Fatalf("expected 0 before 3d, got %d", n)
		}

		// After 3d: auto-accept -> AWAITING_SHIPMENT + RETURN shipment created!
		now = now.Add(2 * time.Minute)
		if n, err := h.svc.ScanReturnDeadlines(context.Background()); err != nil || n != 1 {
			t.Fatalf("ScanReturnDeadlines(PENDING 3d) = %d, %v; want 1, nil", n, err)
		}
		gotRet, _ := h.svc.GetReturnRequest(buyerCtx, &orderv1.GetReturnRequestRequest{ReturnRequestId: retID})
		if gotRet.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT || gotRet.ReturnRequest.ReturnTrackingCode == "" {
			t.Fatalf("expected auto-accepted AWAITING_SHIPMENT with tracking, got %+v", gotRet.ReturnRequest)
		}

		// After 5d from tracking: CLOSED -> order COMPLETED + best-effort CancelShipment(RETURN)
		now = now.Add(5*24*time.Hour + time.Minute)
		if n, err := h.svc.ScanReturnDeadlines(context.Background()); err != nil || n != 1 {
			t.Fatalf("ScanReturnDeadlines(AWAITING_SHIPMENT 5d) = %d, %v; want 1, nil", n, err)
		}
		gotRet, _ = h.svc.GetReturnRequest(buyerCtx, &orderv1.GetReturnRequestRequest{ReturnRequestId: retID})
		gotOrd, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ord.Id})
		if gotRet.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_CLOSED || gotOrd.Order.Status != orderv1.OrderStatus_ORDER_STATUS_COMPLETED {
			t.Fatalf("expected CLOSED & COMPLETED, got ret=%v ord=%v", gotRet.ReturnRequest.Status, gotOrd.Order.Status)
		}
		if len(h.shipping.cancelShipCalls) != 1 || h.shipping.cancelShipCalls[0].GetDirection() != shippingv1.ShipmentDirection_SHIPMENT_DIRECTION_RETURN {
			t.Fatalf("expected best-effort CancelShipment(RETURN), got %+v", h.shipping.cancelShipCalls)
		}
	})

	t.Run("RejectedTimeout3d_Closes", func(t *testing.T) {
		h := newTestHarness()
		now := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
		h.svc.nowFn = func() time.Time { return now }

		ord := createDeliveredOrderHelper(t, h, "w2a-dl-2", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR)
		retResp, _ := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord.Id, Reason: "defect"})
		_, _ = h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: retResp.ReturnRequestId, Decision: orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_REJECT, Note: "not defective",
		})

		now = now.Add(3*24*time.Hour + time.Minute)
		if n, err := h.svc.ScanReturnDeadlines(context.Background()); err != nil || n != 1 {
			t.Fatalf("ScanReturnDeadlines(REJECTED 3d) = %d, %v", n, err)
		}
		gotRet, _ := h.svc.GetReturnRequest(buyerCtx, &orderv1.GetReturnRequestRequest{ReturnRequestId: retResp.ReturnRequestId})
		gotOrd, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ord.Id})
		if gotRet.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_CLOSED || gotOrd.Order.Status != orderv1.OrderStatus_ORDER_STATUS_COMPLETED {
			t.Fatalf("expected CLOSED & COMPLETED, got ret=%v ord=%v", gotRet.ReturnRequest.Status, gotOrd.Order.Status)
		}
	})

	t.Run("ReceivedTimeout3d_AutoConfirmsAndRestocks", func(t *testing.T) {
		h := newTestHarness()
		now := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
		h.svc.nowFn = func() time.Time { return now }

		ord := createDeliveredOrderHelper(t, h, "w2a-dl-3", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR)
		retResp, _ := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord.Id, Reason: "defect"})
		accDl3, _ := h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: retResp.ReturnRequestId, Decision: orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_RETURN,
		})
		_ = h.svc.HandleShipmentUpdated(context.Background(), "evt-dl-recv", events.ShipmentUpdatedPayload{
			OrderID: ord.Id, Direction: "RETURN", ReturnRequestID: retResp.ReturnRequestId, TrackingCode: accDl3.ReturnRequest.ReturnTrackingCode, Status: "delivered",
		})

		now = now.Add(3*24*time.Hour + time.Minute)
		if n, err := h.svc.ScanReturnDeadlines(context.Background()); err != nil || n != 1 {
			t.Fatalf("ScanReturnDeadlines(RECEIVED 3d) = %d, %v", n, err)
		}
		gotRet, _ := h.svc.GetReturnRequest(buyerCtx, &orderv1.GetReturnRequestRequest{ReturnRequestId: retResp.ReturnRequestId})
		gotOrd, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ord.Id})
		if gotRet.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_REFUNDED || gotOrd.Order.Status != orderv1.OrderStatus_ORDER_STATUS_RETURNED {
			t.Fatalf("expected REFUNDED & RETURNED, got ret=%v ord=%v", gotRet.ReturnRequest.Status, gotOrd.Order.Status)
		}
		if len(h.payment.refundCalls) != 1 || len(h.product.restockCalls) != 1 {
			t.Fatalf("expected 1 refund & 1 restock on auto-confirmed RECEIVED, got %d & %d", len(h.payment.refundCalls), len(h.product.restockCalls))
		}
	})

	t.Run("EvidenceRequestedTimeout3d_Closes", func(t *testing.T) {
		h := newTestHarness()
		now := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
		h.svc.nowFn = func() time.Time { return now }

		ord := createDeliveredOrderHelper(t, h, "w2a-dl-4", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR)
		retResp, _ := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord.Id, Reason: "defect"})
		_, _ = h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: retResp.ReturnRequestId, Decision: orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_REJECT, Note: "no",
		})
		_, _ = h.svc.EscalateReturn(buyerCtx, &orderv1.EscalateReturnRequest{
			ReturnRequestId: retResp.ReturnRequestId, Reason: "yes",
		})
		_, _ = h.svc.AdminResolveReturn(adminCtx, &orderv1.AdminResolveReturnRequest{
			ReturnRequestId: retResp.ReturnRequestId, Decision: orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_REQUEST_EVIDENCE, Note: "send photos",
		})

		now = now.Add(3*24*time.Hour + time.Minute)
		if n, err := h.svc.ScanReturnDeadlines(context.Background()); err != nil || n != 1 {
			t.Fatalf("ScanReturnDeadlines(EVIDENCE_REQUESTED 3d) = %d, %v", n, err)
		}
		gotRet, _ := h.svc.GetReturnRequest(buyerCtx, &orderv1.GetReturnRequestRequest{ReturnRequestId: retResp.ReturnRequestId})
		if gotRet.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_CLOSED {
			t.Fatalf("status = %v, want CLOSED", gotRet.ReturnRequest.Status)
		}
	})
}

func TestW2A_OrderJobs_AutoCancelAndAutoComplete_FakeClock(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

	t.Run("AutoCancelConfirmedAfter48h_UsesStandardCancelPathAndContinuesOnError", func(t *testing.T) {
		h := newTestHarness()
		now := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
		h.svc.nowFn = func() time.Time { return now }

		// Order 1: VIETQR paid at t=0 -> CONFIRMED
		r1, _ := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w2a-ac-1",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		_ = h.svc.HandlePaymentSucceeded(context.Background(), "evt-ac-pay", events.PaymentSucceededPayload{
			PaymentID: r1.PaymentId, OrderGroupID: r1.CheckoutGroupId,
		})
		ord1 := r1.Orders[0]

		// Order 2: COD confirmed at t=0, legacy row with confirmed_at=nil -> falls back to updated_at!
		r2, _ := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w2a-ac-2",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		ord2 := r2.Orders[0]
		h.store.SetOrderConfirmedAt(ord2.Id, nil, now)

		// Order 3: COD confirmed by seller -> PREPARING, must NOT be auto-cancelled!
		r3, _ := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w2a-ac-3",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		ord3 := r3.Orders[0]
		_, _ = h.svc.SellerConfirmOrder(sellerCtx, &orderv1.SellerConfirmOrderRequest{OrderId: ord3.Id, ShopId: "shop-1"})

		// At 47h59m: none expired
		now = now.Add(48*time.Hour - time.Minute)
		if n, err := h.svc.ScanAutoCancelUnconfirmedOrders(context.Background()); err != nil || n != 0 {
			t.Fatalf("before 48h expected 0 auto-cancelled, got %d (%v)", n, err)
		}

		// At 48h01m: ord1 and ord2 auto-cancelled with cancel_reason = "seller_confirm_timeout", ord3 untouched!
		now = now.Add(2 * time.Minute)
		if n, err := h.svc.ScanAutoCancelUnconfirmedOrders(context.Background()); err != nil || n != 2 {
			t.Fatalf("after 48h expected 2 auto-cancelled, got %d (%v)", n, err)
		}
		g1, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ord1.Id})
		g2, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ord2.Id})
		g3, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ord3.Id})
		if g1.Order.Status != orderv1.OrderStatus_ORDER_STATUS_CANCELLED || g1.Order.CancelReason != "seller_confirm_timeout" {
			t.Fatalf("ord1 unexpected: status=%v reason=%q", g1.Order.Status, g1.Order.CancelReason)
		}
		if g2.Order.Status != orderv1.OrderStatus_ORDER_STATUS_CANCELLED || g2.Order.CancelReason != "seller_confirm_timeout" {
			t.Fatalf("ord2 (legacy updated_at) unexpected: status=%v reason=%q", g2.Order.Status, g2.Order.CancelReason)
		}
		if g3.Order.Status != orderv1.OrderStatus_ORDER_STATUS_PREPARING {
			t.Fatalf("ord3 should stay PREPARING, got %v", g3.Order.Status)
		}
		if len(h.payment.refundCalls) != 1 || h.payment.refundCalls[0].GetOrderId() != ord1.Id {
			t.Fatalf("auto-cancelled paid VietQR order must trigger refund, got %+v", h.payment.refundCalls)
		}
	})

	t.Run("AutoCancel_OneOrderErrorDoesNotBlockBatch", func(t *testing.T) {
		h := newTestHarness()
		now := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
		h.svc.nowFn = func() time.Time { return now }

		// Create 2 COD orders; inject a tracking code on ordA and make CancelShipment fail with Unavailable.
		rA, _ := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w2a-batch-err-a",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		rB, _ := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w2a-batch-err-b",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		// Give ordA a tracking code while remaining CONFIRMED via shipment update picking (non-transition).
		_ = h.svc.HandleShipmentUpdated(context.Background(), "evt-batch-trk", events.ShipmentUpdatedPayload{
			OrderID: rA.Orders[0].Id, Status: "picking", TrackingCode: "TRK-FAIL-A",
		})
		h.shipping.cancelShipErr = status.Error(codes.Unavailable, "shipping down")

		now = now.Add(49 * time.Hour)
		n, err := h.svc.ScanAutoCancelUnconfirmedOrders(context.Background())
		if err != nil {
			t.Fatalf("ScanAutoCancelUnconfirmedOrders error: %v", err)
		}
		if n != 1 {
			t.Fatalf("expected 1 order (ordB) cancelled despite ordA error, got %d", n)
		}
		gA, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: rA.Orders[0].Id})
		gB, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: rB.Orders[0].Id})
		if gA.Order.Status != orderv1.OrderStatus_ORDER_STATUS_CONFIRMED || gB.Order.Status != orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
			t.Fatalf("expected ordA=CONFIRMED and ordB=CANCELLED, got A=%v B=%v", gA.Order.Status, gB.Order.Status)
		}
	})

	t.Run("AutoCompleteDeliveredAfter7d_SkipsReturnRequestedAndEmitsCoins", func(t *testing.T) {
		h := newTestHarness()
		now := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
		h.svc.nowFn = func() time.Time { return now }

		ord1 := createDeliveredOrderHelper(t, h, "w2a-done-1", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD)
		ord2 := createDeliveredOrderHelper(t, h, "w2a-done-2", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD)
		// ord2 has a return request -> must NOT be auto-completed!
		_, _ = h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord2.Id, Reason: "defect"})

		// Before 7d: 0 auto-completed
		now = now.Add(7*24*time.Hour - time.Minute)
		if n, err := h.svc.ScanAutoCompleteDeliveredOrders(context.Background()); err != nil || n != 0 {
			t.Fatalf("before 7d expected 0 auto-completed, got %d (%v)", n, err)
		}

		// After 7d: only ord1 auto-completed, emits order.completed with coins_reward!
		now = now.Add(2 * time.Minute)
		if n, err := h.svc.ScanAutoCompleteDeliveredOrders(context.Background()); err != nil || n != 1 {
			t.Fatalf("after 7d expected 1 auto-completed, got %d (%v)", n, err)
		}
		g1, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ord1.Id})
		g2, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ord2.Id})
		if g1.Order.Status != orderv1.OrderStatus_ORDER_STATUS_COMPLETED {
			t.Fatalf("ord1 status = %v, want COMPLETED", g1.Order.Status)
		}
		if g2.Order.Status != orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED {
			t.Fatalf("ord2 status = %v, want RETURN_REQUESTED", g2.Order.Status)
		}

		// Concurrent/repeat sweep is idempotent (0 handled).
		if n, err := h.svc.ScanAutoCompleteDeliveredOrders(context.Background()); err != nil || n != 0 {
			t.Fatalf("repeat auto-complete = %d, %v; want 0, nil", n, err)
		}

		completedEvents := 0
		var completedPayload events.OrderCompletedPayload
		for _, ev := range h.store.OutboxEvents() {
			if ev.EventType == events.SubjectOrderCompleted {
				completedEvents++
				_ = json.Unmarshal(ev.PayloadJSON, &completedPayload)
			}
		}
		if completedEvents != 1 || completedPayload.OrderID != ord1.Id || completedPayload.CoinsReward != 10000 {
			t.Fatalf("expected 1 order.completed for ord1 with 10000 coins, got count=%d payload=%+v", completedEvents, completedPayload)
		}
	})
}
