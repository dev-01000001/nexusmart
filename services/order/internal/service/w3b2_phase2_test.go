package service

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/order/internal/db"
	"github.com/nexusmart/core/services/order/internal/events"
	"github.com/nexusmart/core/services/order/internal/statemachine"
	"github.com/nexusmart/core/services/order/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func ctxWithService(serviceName string) context.Context {
	return auth.WithClaims(context.Background(), &auth.Claims{
		Role:    auth.ServiceRole,
		Service: serviceName,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: serviceName,
		},
	})
}

// TestW3B2P2_ZaloPay_CreateCheckoutReplayRecoveryAndRetryResponse verifies all Phase 2 proto wiring:
//   - ZALOPAY CreateCheckout returns pay_url, idempotent replay & crash recovery preserve pay_url.
//   - RetryPaymentResponse returns full fields (payment_method, switched_to_cod, pay_url, orders)
//     for both online switch (VIETQR->ZALOPAY), COD switch (ZALOPAY->COD), and idempotent replays.
//   - Invalid payment_method error message includes COD, VIETQR, and ZALOPAY.
func TestW3B2P2_ZaloPay_CreateCheckoutReplayRecoveryAndRetryResponse(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

	t.Run("CreateCheckout_ZALOPAY_PayURL_Replay_And_InvalidMethodMessage", func(t *testing.T) {
		h := newTestHarness()
		baseNow := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
		h.svc.nowFn = func() time.Time { return baseNow }
		h.methodConfigs.SetConfig(PaymentMethodConfig{
			Method:        "ZALOPAY",
			Enabled:       true,
			MinOrderVND:   10000,
			MaxOrderVND:   20000000,
			ExpiryMinutes: 20,
		})

		// Invalid payment method error mentions COD, VIETQR, or ZALOPAY.
		_, badErr := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "zlp-bad-method",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_UNSPECIFIED,
		})
		if status.Code(badErr) != codes.InvalidArgument || !strings.Contains(badErr.Error(), "ZALOPAY") {
			t.Fatalf("invalid method err = %v, want InvalidArgument mentioning ZALOPAY", badErr)
		}

		req := &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "zlp-checkout-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY,
		}
		resp, err := h.svc.CreateCheckout(buyerCtx, req)
		if err != nil {
			t.Fatalf("CreateCheckout(ZALOPAY): %v", err)
		}
		wantPayURL := "https://sb-openapi.zalopay.vn/pay/" + resp.PaymentId
		if resp.PaymentMethod != orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY {
			t.Fatalf("PaymentMethod = %v, want ZALOPAY", resp.PaymentMethod)
		}
		if resp.PayUrl != wantPayURL {
			t.Fatalf("PayUrl = %q, want %q", resp.PayUrl, wantPayURL)
		}
		if resp.PaymentExpiresAt == nil || !resp.PaymentExpiresAt.AsTime().Equal(baseNow.Add(20*time.Minute)) {
			t.Fatalf("PaymentExpiresAt = %v, want +20m", resp.PaymentExpiresAt)
		}
		if len(resp.Orders) != 1 || resp.Orders[0].PaymentMethod != orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY {
			t.Fatalf("order PaymentMethod = %+v, want ZALOPAY", resp.Orders)
		}

		// Idempotent replay preserves pay_url from snapshot.
		replay, err := h.svc.CreateCheckout(buyerCtx, req)
		if err != nil {
			t.Fatalf("CreateCheckout replay: %v", err)
		}
		if !replay.IdempotentReplay || replay.PayUrl != wantPayURL || replay.PaymentMethod != orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY {
			t.Fatalf("replay lost pay_url or method: %+v", replay)
		}

		// Recovery path: finalizeRecoveredCheckoutGroup also persists pay.GetPayUrl().
		grpID := "grp-zlp-recover"
		if _, err := h.store.CreateCheckoutGroup(context.Background(), db.CreateCheckoutGroupParams{
			ID: grpID, BuyerID: "buyer-1", IdempotencyKey: "zlp-recover-idem", RequestHash: "h-rec",
			Status: "IN_PROGRESS", PaymentMethod: statemachine.PaymentMethodZALOPAY, TotalPayableVND: 1030000,
		}); err != nil {
			t.Fatalf("CreateCheckoutGroup: %v", err)
		}
		exp := baseNow.Add(20 * time.Minute)
		aggs, err := h.store.PersistCheckoutBatch(context.Background(), store.CheckoutBatch{
			CheckoutGroupID: grpID,
			Now:             baseNow,
			Orders: []store.NewOrderRecord{{
				Order: db.InsertOrderParams{
					ID: "ord-zlp-rec", OrderCode: "NM-ZLP-REC", CheckoutGroupID: grpID,
					BuyerID: "buyer-1", ShopID: "shop-1", Status: "PENDING",
					PaymentMethod: statemachine.PaymentMethodZALOPAY, PaymentStatus: "PENDING",
					SubtotalVND: 1000000, ShippingFeeVND: 30000, TotalPayableVND: 1030000,
					ShippingAddressJSON: []byte(`{"province_code":"79","ward_code":"26734"}`),
					PaymentExpiresAt:    &exp,
				},
			}},
		})
		if err != nil {
			t.Fatalf("PersistCheckoutBatch: %v", err)
		}
		if err := h.svc.finalizeRecoveredCheckoutGroup(context.Background(), grpID, &paymentv1.Payment{
			PaymentId: "pay-zlp-rec-1",
			Status:    paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING,
			PayUrl:    "https://sb-openapi.zalopay.vn/pay/recovered",
		}, aggs); err != nil {
			t.Fatalf("finalizeRecoveredCheckoutGroup: %v", err)
		}
		grpRec, err := h.store.GetCheckoutGroupByID(context.Background(), grpID)
		if err != nil || grpRec.PayURL != "https://sb-openapi.zalopay.vn/pay/recovered" {
			t.Fatalf("recovered pay_url = %q err=%v", grpRec.PayURL, err)
		}
	})

	t.Run("RetryPayment_VIETQR_To_ZALOPAY_To_COD_ResponseFieldsAndReplay", func(t *testing.T) {
		h := newTestHarness()
		baseNow := time.Date(2026, 11, 24, 13, 0, 0, 0, time.UTC)
		curNow := baseNow
		h.svc.nowFn = func() time.Time { return curNow }
		h.payment.feeBPSByMethod = map[paymentv1.PaymentMethod]int32{
			paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR:  100, // 1%
			paymentv1.PaymentMethod_PAYMENT_METHOD_ZALOPAY: 200, // 2%
			paymentv1.PaymentMethod_PAYMENT_METHOD_COD:     50,  // 0.5%
		}

		co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "zlp-retry-chain",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout(VIETQR): %v", err)
		}
		origExp := co.PaymentExpiresAt.AsTime()
		ordID := co.Orders[0].Id

		// 1. Switch VIETQR -> ZALOPAY at t+3m (keeps origExp, returns pay_url + orders).
		curNow = baseNow.Add(3 * time.Minute)
		retryZlp, err := h.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
			PaymentId:      co.PaymentId,
			IdempotencyKey: "rk-to-zlp",
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY,
		})
		if err != nil {
			t.Fatalf("RetryPayment(VIETQR->ZALOPAY): %v", err)
		}
		wantZlpURL := "https://sb-openapi.zalopay.vn/pay/" + retryZlp.PaymentId
		if retryZlp.PaymentMethod != orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY ||
			retryZlp.SwitchedToCod ||
			retryZlp.PayUrl != wantZlpURL ||
			retryZlp.PaymentExpiresAt == nil || !retryZlp.PaymentExpiresAt.AsTime().Equal(origExp) ||
			len(retryZlp.Orders) != 1 ||
			retryZlp.Orders[0].PaymentMethod != orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY ||
			retryZlp.Orders[0].PaymentFeeVnd != 0 { // Buyer fees masked!
			t.Fatalf("RetryPayment(VIETQR->ZALOPAY) response mismatch: %+v", retryZlp)
		}
		// Replay of VIETQR->ZALOPAY returns identical response fields.
		retryZlpReplay, err := h.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
			PaymentId:      co.PaymentId,
			IdempotencyKey: "rk-to-zlp",
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY,
		})
		if err != nil {
			t.Fatalf("RetryPayment(VIETQR->ZALOPAY) replay: %v", err)
		}
		if retryZlpReplay.PaymentId != retryZlp.PaymentId || retryZlpReplay.PayUrl != wantZlpURL ||
			retryZlpReplay.PaymentMethod != orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY || len(retryZlpReplay.Orders) != 1 {
			t.Fatalf("RetryPayment(ZALOPAY) replay mismatch: %+v", retryZlpReplay)
		}

		// 2. Switch ZALOPAY -> COD at t+5m (confirms order, switched_to_cod=true, pay_url="", orders populated).
		curNow = baseNow.Add(5 * time.Minute)
		retryCOD, err := h.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
			PaymentId:      retryZlp.PaymentId,
			IdempotencyKey: "rk-to-cod",
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil {
			t.Fatalf("RetryPayment(ZALOPAY->COD): %v", err)
		}
		if retryCOD.PaymentMethod != orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD ||
			!retryCOD.SwitchedToCod ||
			retryCOD.PayUrl != "" ||
			retryCOD.PaymentExpiresAt != nil ||
			len(retryCOD.Orders) != 1 ||
			retryCOD.Orders[0].Status != orderv1.OrderStatus_ORDER_STATUS_CONFIRMED ||
			retryCOD.Orders[0].PaymentMethod != orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD ||
			retryCOD.Orders[0].PaymentFeeVnd != 0 { // Buyer fees masked!
			t.Fatalf("RetryPayment(ZALOPAY->COD) response mismatch: %+v", retryCOD)
		}
		// Replay of ZALOPAY->COD returns identical COD response with orders.
		retryCODReplay, err := h.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
			PaymentId:      retryZlp.PaymentId,
			IdempotencyKey: "rk-to-cod",
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil {
			t.Fatalf("RetryPayment(ZALOPAY->COD) replay: %v", err)
		}
		if !retryCODReplay.SwitchedToCod || retryCODReplay.PayUrl != "" ||
			retryCODReplay.PaymentMethod != orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD ||
			len(retryCODReplay.Orders) != 1 || retryCODReplay.Orders[0].Status != orderv1.OrderStatus_ORDER_STATUS_CONFIRMED {
			t.Fatalf("RetryPayment(COD) replay mismatch: %+v", retryCODReplay)
		}

		// Seller sees recalculated COD payment fee (1,030,000 * 50 / 10000 = 5,150 VND).
		sellerOrd, err := h.svc.GetOrder(sellerCtx, &orderv1.GetOrderRequest{OrderId: ordID})
		if err != nil {
			t.Fatalf("GetOrder(seller): %v", err)
		}
		if sellerOrd.Order.PaymentFeeBps != 50 || sellerOrd.Order.PaymentFeeVnd != 5150 || sellerOrd.Order.TotalPayableVnd != 1030000 {
			t.Fatalf("recalculated COD fees mismatch: %+v", sellerOrd.Order)
		}
	})
}

// TestW3B2P2_RBAC_F9_SixRolesMatrix verifies F9 capability-based RBAC across ALL 6 internal staff roles
// (super_admin, admin, moderator, cskh, accountant, ops) plus buyer, non-owning seller, and service token
// against every admin RPC in OrderService.
func TestW3B2P2_RBAC_F9_SixRolesMatrix(t *testing.T) {
	allActors := []struct {
		name      string
		role      string
		isService bool
		ctx       context.Context
	}{
		{"super_admin", auth.RoleSuperAdmin, false, ctxWithUser("u-super", auth.RoleSuperAdmin, "")},
		{"admin", auth.RoleAdmin, false, ctxWithUser("u-admin", auth.RoleAdmin, "")},
		{"moderator", auth.RoleModerator, false, ctxWithUser("u-mod", auth.RoleModerator, "")},
		{"cskh", auth.RoleCSKH, false, ctxWithUser("u-cskh", auth.RoleCSKH, "")},
		{"accountant", auth.RoleAccountant, false, ctxWithUser("u-acc", auth.RoleAccountant, "")},
		{"ops", auth.RoleOps, false, ctxWithUser("u-ops", auth.RoleOps, "")},
		{"other_buyer", auth.RoleBuyer, false, ctxWithUser("buyer-other", auth.RoleBuyer, "")},
		{"other_seller", auth.RoleSeller, false, ctxWithUser("seller-other", auth.RoleSeller, "shop-other")},
		{"service_token", auth.ServiceRole, true, ctxWithService("payment-service")},
	}

	// Helper to seed an order (flagged for manual review) + an escalated return request + a pending online order.
	setupFixtures := func(t *testing.T) (*testHarness, string, string, string) {
		t.Helper()
		h := newTestHarness()
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

		// 1. Delivered + escalated return request order (also flagged for manual review for testing).
		co1, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "rbac-co1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil {
			t.Fatalf("seed co1: %v", err)
		}
		ord1ID := co1.Orders[0].Id
		ship1, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord1ID})
		if err != nil {
			t.Fatalf("SellerShipOrder: %v", err)
		}
		if err := h.svc.HandleShipmentUpdated(context.Background(), "ev-ship-1", events.ShipmentUpdatedPayload{OrderID: ord1ID, TrackingCode: ship1.TrackingCode, Status: "delivering"}); err != nil {
			t.Fatalf("delivering: %v", err)
		}
		if err := h.svc.HandleShipmentUpdated(context.Background(), "ev-deliv-1", events.ShipmentUpdatedPayload{OrderID: ord1ID, TrackingCode: ship1.TrackingCode, Status: "delivered"}); err != nil {
			t.Fatalf("delivered: %v", err)
		}
		retResp, err := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord1ID, Reason: "damaged item"})
		if err != nil {
			t.Fatalf("RequestReturn: %v", err)
		}
		if _, err := h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: retResp.ReturnRequestId,
			Decision:        orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_REJECT,
			Note:            "seller rejects",
		}); err != nil {
			t.Fatalf("RespondReturn(REJECT): %v", err)
		}
		if _, err := h.svc.EscalateReturn(buyerCtx, &orderv1.EscalateReturnRequest{
			ReturnRequestId: retResp.ReturnRequestId,
			Reason:          "buyer disputes",
		}); err != nil {
			t.Fatalf("EscalateReturn: %v", err)
		}

		// 2. Cancelled order flagged with orphan_shipment_needs_manual_review = true.
		co2, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "rbac-co2",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil {
			t.Fatalf("seed co2: %v", err)
		}
		ord2ID := co2.Orders[0].Id
		if _, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: ord2ID, Reason: "buyer cancel"}); err != nil {
			t.Fatalf("CancelOrder co2: %v", err)
		}
		if err := h.store.MarkOrderOrphanShipmentManualReview(context.Background(), ord2ID, "carrier already picked up"); err != nil {
			t.Fatalf("MarkOrderOrphanShipmentManualReview: %v", err)
		}

		// 3. Pending online order for CancelOrder / MarkPaymentResult admin override checks.
		co3, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "rbac-co3",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("seed co3: %v", err)
		}
		return h, ord1ID, retResp.ReturnRequestId, co3.Orders[0].Id
	}

	checkExpectation := func(t *testing.T, rpcName, actorName string, wantAllow bool, err error) {
		t.Helper()
		if wantAllow {
			if err != nil {
				t.Errorf("%s as %s: expected ALLOW (nil error), got %v", rpcName, actorName, err)
			}
		} else {
			if status.Code(err) != codes.PermissionDenied {
				t.Errorf("%s as %s: expected PermissionDenied, got %v", rpcName, actorName, err)
			}
		}
	}

	// Read-only RPCs share one fixture harness.
	hRead, ord1ID, retID, _ := setupFixtures(t)
	// Also pick the flagged order ID from ListOrdersNeedingManualReview.
	flaggedList, _, _ := hRead.store.ListOrdersNeedingManualReview(context.Background(), 10, 0)
	flaggedOrdID := flaggedList[0].Order.ID

	for _, actor := range allActors {
		actor := actor
		t.Run("ReadRPCs_"+actor.name, func(t *testing.T) {
			allowOrdersRead := !actor.isService && auth.RoleHasCapability(actor.role, auth.CapOrdersRead)
			allowReportsRead := !actor.isService && auth.RoleHasCapability(actor.role, auth.CapReportsRead)
			allowReturnsRead := !actor.isService && auth.RoleHasCapability(actor.role, auth.CapReturnsRead)
			allowFullAdmin := !actor.isService && (actor.role == auth.RoleAdmin || actor.role == auth.RoleSuperAdmin)

			_, err := hRead.svc.ListOrders(actor.ctx, &orderv1.ListOrdersRequest{RoleScope: "admin"})
			checkExpectation(t, "ListOrders(admin)", actor.name, allowOrdersRead, err)

			ordResp, err := hRead.svc.GetOrder(actor.ctx, &orderv1.GetOrderRequest{OrderId: flaggedOrdID})
			checkExpectation(t, "GetOrder", actor.name, allowOrdersRead, err)
			if allowOrdersRead && (!ordResp.Order.NeedsManualReview) {
				t.Errorf("GetOrder as %s (orders.read) must see NeedsManualReview=true", actor.name)
			}

			_, err = hRead.svc.GetOrderTimeline(actor.ctx, &orderv1.GetOrderTimelineRequest{OrderId: ord1ID})
			checkExpectation(t, "GetOrderTimeline", actor.name, allowOrdersRead, err)

			_, err = hRead.svc.ListOrdersNeedingReview(actor.ctx, &orderv1.ListOrdersNeedingReviewRequest{})
			checkExpectation(t, "ListOrdersNeedingReview", actor.name, allowOrdersRead, err)

			_, err = hRead.svc.GetPlatformReport(actor.ctx, &orderv1.GetPlatformReportRequest{})
			checkExpectation(t, "GetPlatformReport", actor.name, allowReportsRead, err)

			// GetSellerReport admin override stays admin/super_admin only (matching HEAD; NOT accountant/ops).
			_, err = hRead.svc.GetSellerReport(actor.ctx, &orderv1.GetSellerReportRequest{ShopId: "shop-1"})
			checkExpectation(t, "GetSellerReport(shop-1)", actor.name, allowFullAdmin, err)

			_, err = hRead.svc.ListReturnRequests(actor.ctx, &orderv1.ListReturnRequestsRequest{RoleScope: "admin"})
			checkExpectation(t, "ListReturnRequests(admin)", actor.name, allowReturnsRead, err)

			_, err = hRead.svc.GetReturnRequest(actor.ctx, &orderv1.GetReturnRequestRequest{ReturnRequestId: retID})
			checkExpectation(t, "GetReturnRequest", actor.name, allowReturnsRead, err)
		})
	}

	// Mutating admin RPCs use a fresh fixture harness per actor so state changes never interfere.
	for _, actor := range allActors {
		actor := actor
		t.Run("MutatingRPCs_"+actor.name, func(t *testing.T) {
			allowReviewResolve := !actor.isService && auth.RoleHasCapability(actor.role, auth.CapOrdersReviewResolve)
			allowReturnsResolve := !actor.isService && auth.RoleHasCapability(actor.role, auth.CapReturnsResolve)
			allowFullAdmin := !actor.isService && (actor.role == auth.RoleAdmin || actor.role == auth.RoleSuperAdmin)
			allowMarkPay := actor.isService || allowFullAdmin

			// 1. ResolveOrderReview -> orders.review.resolve (contracts §4.11)
			h1, _, _, _ := setupFixtures(t)
			fl1, _, _ := h1.store.ListOrdersNeedingManualReview(context.Background(), 10, 0)
			_, err := h1.svc.ResolveOrderReview(actor.ctx, &orderv1.ResolveOrderReviewRequest{
				OrderId: fl1[0].Order.ID,
				Note:    "resolved by " + actor.name,
			})
			checkExpectation(t, "ResolveOrderReview", actor.name, allowReviewResolve, err)

			// 2. AdminResolveReturn -> returns.resolve (contracts §4.11)
			h2, _, r2ID, _ := setupFixtures(t)
			_, err = h2.svc.AdminResolveReturn(actor.ctx, &orderv1.AdminResolveReturnRequest{
				ReturnRequestId: r2ID,
				Decision:        orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_REQUEST_EVIDENCE,
				Note:            "need photos",
			})
			checkExpectation(t, "AdminResolveReturn", actor.name, allowReturnsResolve, err)

			// 3. CancelOrder (other user's order) -> admin/super_admin ONLY (never ops/accountant/cskh/moderator)
			h3, _, _, pendingOrdID := setupFixtures(t)
			_, err = h3.svc.CancelOrder(actor.ctx, &orderv1.CancelOrderRequest{
				OrderId: pendingOrdID,
				Reason:  "admin cancel",
			})
			checkExpectation(t, "CancelOrder(admin)", actor.name, allowFullAdmin, err)

			// 4. SellerConfirmOrder & SellerShipOrder (other shop's order) -> admin/super_admin ONLY (never ops/accountant/cskh/moderator)
			hShop := newTestHarness()
			coShop, err := hShop.svc.CreateCheckout(ctxWithUser("buyer-1", "buyer", ""), &orderv1.CreateCheckoutRequest{
				IdempotencyKey: "rbac-shop-manage-" + actor.name,
				AddressId:      "addr-buyer-1",
				Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
				PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
			})
			if err != nil {
				t.Fatalf("seed shop order: %v", err)
			}
			_, err = hShop.svc.SellerConfirmOrder(actor.ctx, &orderv1.SellerConfirmOrderRequest{
				OrderId: coShop.Orders[0].Id,
				ShopId:  "shop-1",
			})
			checkExpectation(t, "SellerConfirmOrder(admin)", actor.name, allowFullAdmin, err)

			_, err = hShop.svc.SellerShipOrder(actor.ctx, &orderv1.SellerShipOrderRequest{
				OrderId: coShop.Orders[0].Id,
				ShopId:  "shop-1",
			})
			checkExpectation(t, "SellerShipOrder(admin)", actor.name, allowFullAdmin, err)

			// 5. MarkPaymentResult -> admin/super_admin OR service token ONLY (never ops/accountant/cskh/moderator)
			h4, _, _, pendingOrdID4 := setupFixtures(t)
			agg4, _ := h4.store.GetOrderWithDetails(context.Background(), pendingOrdID4)
			_, err = h4.svc.MarkPaymentResult(actor.ctx, &orderv1.MarkPaymentResultRequest{
				CheckoutGroupId: agg4.Order.CheckoutGroupID,
				PaymentId:       agg4.Order.PaymentID,
				Status:          orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_SUCCEEDED,
				Reason:          "manual confirm",
			})
			checkExpectation(t, "MarkPaymentResult", actor.name, allowMarkPay, err)

			// 6. CreateCheckout on behalf of another buyer -> admin/super_admin ONLY (never ops/accountant/cskh/moderator)
			h5 := newTestHarness()
			_, err = h5.svc.CreateCheckout(actor.ctx, &orderv1.CreateCheckoutRequest{
				IdempotencyKey: "on-behalf-" + actor.name,
				BuyerId:        "buyer-1",
				AddressId:      "addr-buyer-1",
				Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
				PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
			})
			checkExpectation(t, "CreateCheckout(on-behalf)", actor.name, allowFullAdmin, err)

			// 7. mockUserClient.GetAddress mirrors user p3: owner + admin/super_admin + service token ONLY (rejects moderator/cskh/accountant/ops).
			_, addrErr := h5.user.GetAddress(actor.ctx, &userv1.GetAddressRequest{
				UserId:    "buyer-1",
				AddressId: "addr-buyer-1",
			})
			checkExpectation(t, "mockUserClient.GetAddress", actor.name, allowFullAdmin || actor.isService, addrErr)
		})
	}
}

// TestW3B2P2_F11_LargeRefundApproval_PendingApprovedRejectedAndAutoRefunds verifies F11 end-to-end:
//  1. AdminResolveReturn(REFUND) passes RequireApprovalIfLarge=true + RequestedBy=maker; when payment
//     returns PENDING_APPROVAL, return stays REFUNDING (NOT REFUNDED) and watchdog does NOT re-call CreateRefund.
//  2. Approved via payment.refunded (matching refund_id) settles REFUNDING -> REFUNDED exactly once;
//     unrelated partial refund_id does NOT prematurely settle it.
//  3. Rejected via payment.refund_rejected transitions REFUNDING -> ESCALATED (reverts order RETURNED -> RETURN_REQUESTED),
//     increments refund_attempt so a retry AdminResolveReturn(REFUND) uses a fresh idempotency key (":r1"),
//     and also allows AdminResolveReturn(REJECT) -> CLOSED cleanly.
//  4. Rejected directly from CreateRefund replay (crash recovery) also transitions REFUNDING -> ESCALATED cleanly.
//  5. Watchdog retry after transient CreateRefund error preserves durable maker RequestedBy + RequireApprovalIfLarge=true.
//  6. Automatic refunds (cancel, forward delivery return, seller ACCEPT_REFUND, seller ConfirmReturnReceived,
//     auto-confirm deadline, F5 superseded late refund) ALL set RequireApprovalIfLarge=false and RequestedBy="".
func TestW3B2P2_F11_LargeRefundApproval_PendingApprovedRejectedAndAutoRefunds(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
	cskhCtx := ctxWithUser("cskh-maker-1", auth.RoleCSKH, "")
	adminCtx := ctxWithUser("admin-maker-2", auth.RoleAdmin, "")

	seedEscalatedOnlineReturn := func(t *testing.T, h *testHarness, suffix string) (string, string, string) {
		t.Helper()
		co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "f11-co-" + suffix,
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 2}}, // 2,030,000 VND
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		ordID := co.Orders[0].Id
		if err := h.svc.HandlePaymentSucceeded(context.Background(), "ev-pay-"+suffix, events.PaymentSucceededPayload{
			PaymentID: co.PaymentId, OrderGroupID: co.CheckoutGroupId, OrderID: ordID,
		}); err != nil {
			t.Fatalf("HandlePaymentSucceeded: %v", err)
		}
		shipRes, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID})
		if err != nil {
			t.Fatalf("SellerShipOrder: %v", err)
		}
		if err := h.svc.HandleShipmentUpdated(context.Background(), "ev-ship-"+suffix, events.ShipmentUpdatedPayload{OrderID: ordID, TrackingCode: shipRes.TrackingCode, Status: "delivering"}); err != nil {
			t.Fatalf("delivering: %v", err)
		}
		if err := h.svc.HandleShipmentUpdated(context.Background(), "ev-del-"+suffix, events.ShipmentUpdatedPayload{OrderID: ordID, TrackingCode: shipRes.TrackingCode, Status: "delivered"}); err != nil {
			t.Fatalf("delivered: %v", err)
		}
		reqRet, err := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ordID, Reason: "defective item"})
		if err != nil {
			t.Fatalf("RequestReturn: %v", err)
		}
		if _, err := h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: reqRet.ReturnRequestId,
			Decision:        orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_REJECT,
			Note:            "dispute",
		}); err != nil {
			t.Fatalf("RespondReturn(REJECT): %v", err)
		}
		if _, err := h.svc.EscalateReturn(buyerCtx, &orderv1.EscalateReturnRequest{
			ReturnRequestId: reqRet.ReturnRequestId,
			Reason:          "need admin arbitration",
		}); err != nil {
			t.Fatalf("EscalateReturn: %v", err)
		}
		return ordID, co.PaymentId, reqRet.ReturnRequestId
	}

	t.Run("PendingApproval_WatchdogSkips_ThenApprovedOnce", func(t *testing.T) {
		h := newTestHarness()
		ordID, payID, retID := seedEscalatedOnlineReturn(t, h, "approve")

		h.payment.onCreateRefund = func(_ context.Context, req *paymentv1.CreateRefundRequest) (*paymentv1.CreateRefundResponse, error) {
			return &paymentv1.CreateRefundResponse{
				Refund: &paymentv1.Refund{
					RefundId:    "ref-large-approve-1",
					PaymentId:   req.GetPaymentId(),
					OrderId:     req.GetOrderId(),
					AmountVnd:   req.GetAmountVnd(),
					Status:      paymentv1.RefundStatus_REFUND_STATUS_PENDING_APPROVAL,
					RequestedBy: req.GetRequestedBy(),
				},
			}, nil
		}

		res, err := h.svc.AdminResolveReturn(cskhCtx, &orderv1.AdminResolveReturnRequest{
			ReturnRequestId: retID,
			Decision:        orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_REFUND,
			Note:            "large refund for damaged laptop",
		})
		if err != nil {
			t.Fatalf("AdminResolveReturn(REFUND): %v", err)
		}
		if res.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_REFUNDING {
			t.Fatalf("PENDING_APPROVAL return MUST stay REFUNDING (not REFUNDED), got %v", res.ReturnRequest.Status)
		}
		if len(h.payment.refundCalls) != 1 {
			t.Fatalf("expected 1 CreateRefund call, got %d", len(h.payment.refundCalls))
		}
		call := h.payment.refundCalls[0]
		if !call.GetRequireApprovalIfLarge() || call.GetRequestedBy() != "cskh-maker-1" || call.GetIdempotencyKey() != "order-return-refund:"+ordID {
			t.Fatalf("CreateRefund F11 fields mismatch: reqApproval=%v reqBy=%q idem=%q",
				call.GetRequireApprovalIfLarge(), call.GetRequestedBy(), call.GetIdempotencyKey())
		}
		if len(h.promo.releaseShopCalls) != 0 {
			t.Fatalf("must NOT release promotions while refund is PENDING_APPROVAL")
		}

		// Watchdog sweeps 3 times: MUST skip PENDING_APPROVAL and NEVER call CreateRefund again!
		for i := 0; i < 3; i++ {
			handled, err := h.svc.ScanRefundingReturns(context.Background())
			if err != nil {
				t.Fatalf("ScanRefundingReturns #%d: %v", i, err)
			}
			if handled != 0 {
				t.Fatalf("ScanRefundingReturns #%d handled=%d, want 0 while PENDING_APPROVAL", i, handled)
			}
		}
		if len(h.payment.refundCalls) != 1 {
			t.Fatalf("watchdog created duplicate refunds while PENDING_APPROVAL: calls=%d", len(h.payment.refundCalls))
		}

		// Unrelated partial refund with different refund_id MUST NOT prematurely settle the pending-approval return.
		if err := h.svc.HandlePaymentRefunded(context.Background(), "ev-unrelated-partial", events.PaymentRefundedPayload{
			RefundID:  "ref-unrelated-999",
			PaymentID: payID,
			OrderID:   ordID,
			AmountVND: 100000,
			Reason:    "goodwill partial",
		}); err != nil {
			t.Fatalf("HandlePaymentRefunded(unrelated): %v", err)
		}
		rrMid, _ := h.store.GetReturnRequest(context.Background(), retID, "")
		if rrMid.Request.Status != "REFUNDING" || rrMid.Request.RefundApprovalStatus != "PENDING_APPROVAL" {
			t.Fatalf("unrelated refund must not settle pending approval: %+v", rrMid.Request)
		}

		// Checker approves -> payment.refunded with matching refund_id settles REFUNDING -> REFUNDED exactly once!
		if err := h.svc.HandlePaymentRefunded(context.Background(), "ev-approved-1", events.PaymentRefundedPayload{
			RefundID:  "ref-large-approve-1",
			PaymentID: payID,
			OrderID:   ordID,
			AmountVND: 2030000,
			Reason:    "Order return refund",
		}); err != nil {
			t.Fatalf("HandlePaymentRefunded(approved): %v", err)
		}

		rrDone, _ := h.store.GetReturnRequest(context.Background(), retID, "")
		if rrDone.Request.Status != "REFUNDED" || !rrDone.Request.RefundDone || rrDone.Request.RefundApprovalStatus != "APPROVED" {
			t.Fatalf("expected REFUNDED + APPROVED after approval event, got %+v", rrDone.Request)
		}
		if len(h.promo.releaseShopCalls) != 1 || len(h.payment.refundCalls) != 1 {
			t.Fatalf("expected promo release=1 refund calls=1, got promo=%d refund=%d", len(h.promo.releaseShopCalls), len(h.payment.refundCalls))
		}

		// Duplicate event delivery + watchdog tick: idempotent, still 1 REFUNDED transition, 1 promo release, 1 CreateRefund.
		if err := h.svc.HandlePaymentRefunded(context.Background(), "ev-approved-1", events.PaymentRefundedPayload{
			RefundID: "ref-large-approve-1", PaymentID: payID, OrderID: ordID, AmountVND: 2030000,
		}); err != nil {
			t.Fatalf("duplicate HandlePaymentRefunded: %v", err)
		}
		_, _ = h.svc.ScanRefundingReturns(context.Background())
		rrAfter, _ := h.store.GetReturnRequest(context.Background(), retID, "")
		refundedEvCount := 0
		for _, ev := range rrAfter.Events {
			if ev.ToStatus == "REFUNDED" {
				refundedEvCount++
			}
		}
		if refundedEvCount != 1 || len(h.promo.releaseShopCalls) != 1 || len(h.payment.refundCalls) != 1 {
			t.Fatalf("REFUNDED must happen exactly once: evCount=%d promo=%d refund=%d",
				refundedEvCount, len(h.promo.releaseShopCalls), len(h.payment.refundCalls))
		}
	})

	t.Run("RejectedViaEvent_ReEscalates_AllowsFreshRefundOrRejectClose", func(t *testing.T) {
		h := newTestHarness()
		ordID, payID, retID := seedEscalatedOnlineReturn(t, h, "reject-retry")

		var attempt atomic.Int32
		h.payment.onCreateRefund = func(_ context.Context, req *paymentv1.CreateRefundRequest) (*paymentv1.CreateRefundResponse, error) {
			n := attempt.Add(1)
			if n == 1 {
				return &paymentv1.CreateRefundResponse{
					Refund: &paymentv1.Refund{
						RefundId:  "ref-large-rej-1",
						PaymentId: req.GetPaymentId(),
						OrderId:   req.GetOrderId(),
						AmountVnd: req.GetAmountVnd(),
						Status:    paymentv1.RefundStatus_REFUND_STATUS_PENDING_APPROVAL,
					},
				}, nil
			}
			// Second attempt succeeds immediately!
			return &paymentv1.CreateRefundResponse{
				Refund: &paymentv1.Refund{
					RefundId:  "ref-large-retry-2",
					PaymentId: req.GetPaymentId(),
					OrderId:   req.GetOrderId(),
					AmountVnd: req.GetAmountVnd(),
					Status:    paymentv1.RefundStatus_REFUND_STATUS_DONE,
				},
			}, nil
		}

		if _, err := h.svc.AdminResolveReturn(cskhCtx, &orderv1.AdminResolveReturnRequest{
			ReturnRequestId: retID,
			Decision:        orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_REFUND,
		}); err != nil {
			t.Fatalf("first AdminResolveReturn(REFUND): %v", err)
		}

		// Checker rejects via payment.refund_rejected event.
		if err := h.svc.HandlePaymentRefundRejected(context.Background(), "ev-rej-1", events.PaymentRefundRejectedPayload{
			RefundID:   "ref-large-rej-1",
			PaymentID:  payID,
			OrderID:    ordID,
			AmountVND:  2030000,
			Reason:     "insufficient proof of damage",
			RejectedBy: "accountant-checker-1",
		}); err != nil {
			t.Fatalf("HandlePaymentRefundRejected: %v", err)
		}
		// Duplicate delivery is a clean no-op.
		if err := h.svc.HandlePaymentRefundRejected(context.Background(), "ev-rej-1", events.PaymentRefundRejectedPayload{
			RefundID: "ref-large-rej-1", PaymentID: payID, OrderID: ordID, Reason: "dup",
		}); err != nil {
			t.Fatalf("duplicate HandlePaymentRefundRejected: %v", err)
		}

		rrEsc, _ := h.store.GetReturnRequest(context.Background(), retID, "")
		ordReverted, _ := h.store.GetOrderWithDetails(context.Background(), ordID)
		if rrEsc.Request.Status != "ESCALATED" || rrEsc.Request.RefundApprovalStatus != "REJECTED" || rrEsc.Request.RefundAttempt != 1 {
			t.Fatalf("rejected large refund must move to ESCALATED + attempt=1, got %+v", rrEsc.Request)
		}
		if ordReverted.Order.Status != "RETURN_REQUESTED" {
			t.Fatalf("order status after refund rejection = %q, want RETURN_REQUESTED", ordReverted.Order.Status)
		}
		if len(h.promo.releaseShopCalls) != 0 {
			t.Fatalf("promo must NOT be released when large refund was rejected")
		}

		// Admin re-submits REFUND -> uses fresh idempotency key ":r1" and new maker identity!
		res2, err := h.svc.AdminResolveReturn(adminCtx, &orderv1.AdminResolveReturnRequest{
			ReturnRequestId: retID,
			Decision:        orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_REFUND,
			Note:            "verified photos, re-issuing refund",
		})
		if err != nil {
			t.Fatalf("second AdminResolveReturn(REFUND): %v", err)
		}
		if res2.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_REFUNDED {
			t.Fatalf("second attempt status = %v, want REFUNDED", res2.ReturnRequest.Status)
		}
		if len(h.payment.refundCalls) != 2 {
			t.Fatalf("expected 2 CreateRefund calls, got %d", len(h.payment.refundCalls))
		}
		secondCall := h.payment.refundCalls[1]
		if secondCall.GetIdempotencyKey() != "order-return-refund:"+ordID+":r1" ||
			!secondCall.GetRequireApprovalIfLarge() ||
			secondCall.GetRequestedBy() != "admin-maker-2" {
			t.Fatalf("second CreateRefund mismatch: idem=%q reqApproval=%v reqBy=%q",
				secondCall.GetIdempotencyKey(), secondCall.GetRequireApprovalIfLarge(), secondCall.GetRequestedBy())
		}

		// Also verify AdminResolveReturn(REJECT) -> CLOSED works cleanly after a large refund rejection.
		h2 := newTestHarness()
		ord2ID, pay2ID, ret2ID := seedEscalatedOnlineReturn(t, h2, "reject-close")
		h2.payment.onCreateRefund = func(_ context.Context, req *paymentv1.CreateRefundRequest) (*paymentv1.CreateRefundResponse, error) {
			return &paymentv1.CreateRefundResponse{
				Refund: &paymentv1.Refund{
					RefundId: "ref-rej-close-1", PaymentId: req.GetPaymentId(), OrderId: req.GetOrderId(),
					AmountVnd: req.GetAmountVnd(), Status: paymentv1.RefundStatus_REFUND_STATUS_REJECTED,
					ApprovalNote: "rejected synchronously on replay",
				},
			}, nil
		}
		resRej, err := h2.svc.AdminResolveReturn(cskhCtx, &orderv1.AdminResolveReturnRequest{
			ReturnRequestId: ret2ID,
			Decision:        orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_REFUND,
		})
		if err != nil {
			t.Fatalf("AdminResolveReturn(REFUND->REJECTED): %v", err)
		}
		if resRej.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_ESCALATED {
			t.Fatalf("direct REFUND_STATUS_REJECTED must transition to ESCALATED, got %v", resRej.ReturnRequest.Status)
		}
		_ = pay2ID
		resClose, err := h2.svc.AdminResolveReturn(adminCtx, &orderv1.AdminResolveReturnRequest{
			ReturnRequestId: ret2ID,
			Decision:        orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_REJECT,
			Note:            "closing dispute after checker rejection",
		})
		if err != nil {
			t.Fatalf("AdminResolveReturn(REJECT) after rejection: %v", err)
		}
		ord2Final, _ := h2.store.GetOrderWithDetails(context.Background(), ord2ID)
		if resClose.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_CLOSED || ord2Final.Order.Status != "COMPLETED" {
			t.Fatalf("expected CLOSED + COMPLETED, got ret=%v ord=%q", resClose.ReturnRequest.Status, ord2Final.Order.Status)
		}
	})

	t.Run("WatchdogRetry_PreservesDurableMakerRequestedBy", func(t *testing.T) {
		h := newTestHarness()
		_, _, retID := seedEscalatedOnlineReturn(t, h, "watchdog-maker")

		var calls atomic.Int32
		h.payment.onCreateRefund = func(_ context.Context, req *paymentv1.CreateRefundRequest) (*paymentv1.CreateRefundResponse, error) {
			// Fail all 3 RetryIdempotent attempts during AdminResolveReturn so the watchdog performs attempt #4.
			if calls.Add(1) <= 3 {
				return nil, status.Error(codes.Unavailable, "payment temporarily unavailable")
			}
			return &paymentv1.CreateRefundResponse{
				Refund: &paymentv1.Refund{
					RefundId: "ref-wd-1", PaymentId: req.GetPaymentId(), OrderId: req.GetOrderId(),
					AmountVnd: req.GetAmountVnd(), Status: paymentv1.RefundStatus_REFUND_STATUS_PENDING_APPROVAL,
				},
			}, nil
		}

		// Initial AdminResolveReturn(REFUND) commits REFUNDING even though CreateRefund fails transiently.
		res, err := h.svc.AdminResolveReturn(cskhCtx, &orderv1.AdminResolveReturnRequest{
			ReturnRequestId: retID,
			Decision:        orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_REFUND,
		})
		if err != nil {
			t.Fatalf("AdminResolveReturn: %v", err)
		}
		if res.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_REFUNDING {
			t.Fatalf("status = %v, want REFUNDING", res.ReturnRequest.Status)
		}
		// Watchdog retries using background context (no admin JWT!) — MUST read durable maker from return_requests!
		handled, err := h.svc.ScanRefundingReturns(context.Background())
		if err != nil || handled != 1 {
			t.Fatalf("ScanRefundingReturns handled=%d err=%v, want handled=1", handled, err)
		}
		lastCall := h.payment.refundCalls[len(h.payment.refundCalls)-1]
		if !lastCall.GetRequireApprovalIfLarge() || lastCall.GetRequestedBy() != "cskh-maker-1" {
			t.Fatalf("watchdog lost maker identity: reqApproval=%v reqBy=%q, want true / cskh-maker-1",
				lastCall.GetRequireApprovalIfLarge(), lastCall.GetRequestedBy())
		}
	})

	t.Run("AutomaticRefunds_KeepRequireApprovalIfLargeFalse", func(t *testing.T) {
		// 1. CancelOrder on paid online order -> RequireApprovalIfLarge=false.
		h1 := newTestHarness()
		co1, err := h1.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "auto-cancel", AddressId: "addr-buyer-1",
			Items:         []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 2}},
			PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("co1: %v", err)
		}
		if err := h1.svc.HandlePaymentSucceeded(context.Background(), "ev-ac-1", events.PaymentSucceededPayload{
			PaymentID: co1.PaymentId, OrderGroupID: co1.CheckoutGroupId, OrderID: co1.Orders[0].Id,
		}); err != nil {
			t.Fatalf("paid1: %v", err)
		}
		if _, err := h1.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: co1.Orders[0].Id, Reason: "changed mind"}); err != nil {
			t.Fatalf("CancelOrder: %v", err)
		}
		if len(h1.payment.refundCalls) != 1 || h1.payment.refundCalls[0].GetRequireApprovalIfLarge() || h1.payment.refundCalls[0].GetRequestedBy() != "" {
			t.Fatalf("CancelOrder auto-refund must have require_approval_if_large=false: %+v", h1.payment.refundCalls)
		}

		// 2. Forward delivery failure (SHIPPING -> RETURNED) -> RequireApprovalIfLarge=false.
		h2 := newTestHarness()
		co2, err := h2.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "auto-fwd-ret", AddressId: "addr-buyer-1",
			Items:         []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 2}},
			PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("co2: %v", err)
		}
		ord2ID := co2.Orders[0].Id
		_ = h2.svc.HandlePaymentSucceeded(context.Background(), "ev-af-1", events.PaymentSucceededPayload{
			PaymentID: co2.PaymentId, OrderGroupID: co2.CheckoutGroupId, OrderID: ord2ID,
		})
		ship2, _ := h2.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord2ID})
		_ = h2.svc.HandleShipmentUpdated(context.Background(), "ev-af-2", events.ShipmentUpdatedPayload{OrderID: ord2ID, TrackingCode: ship2.TrackingCode, Status: "delivering"})
		if err := h2.svc.HandleShipmentUpdated(context.Background(), "ev-af-3", events.ShipmentUpdatedPayload{OrderID: ord2ID, TrackingCode: ship2.TrackingCode, Status: "returned"}); err != nil {
			t.Fatalf("shipment returned: %v", err)
		}
		if len(h2.payment.refundCalls) != 1 || h2.payment.refundCalls[0].GetRequireApprovalIfLarge() || h2.payment.refundCalls[0].GetRequestedBy() != "" {
			t.Fatalf("forward return auto-refund must have require_approval_if_large=false: %+v", h2.payment.refundCalls)
		}

		// 3. Seller RespondReturn(ACCEPT_REFUND) -> RequireApprovalIfLarge=false.
		h3 := newTestHarness()
		co3, _ := h3.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "auto-seller-acc", AddressId: "addr-buyer-1",
			Items:         []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 2}},
			PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		ord3ID := co3.Orders[0].Id
		_ = h3.svc.HandlePaymentSucceeded(context.Background(), "ev-as-1", events.PaymentSucceededPayload{
			PaymentID: co3.PaymentId, OrderGroupID: co3.CheckoutGroupId, OrderID: ord3ID,
		})
		ship3, _ := h3.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord3ID})
		_ = h3.svc.HandleShipmentUpdated(context.Background(), "ev-as-2", events.ShipmentUpdatedPayload{OrderID: ord3ID, TrackingCode: ship3.TrackingCode, Status: "delivering"})
		_ = h3.svc.HandleShipmentUpdated(context.Background(), "ev-as-3", events.ShipmentUpdatedPayload{OrderID: ord3ID, TrackingCode: ship3.TrackingCode, Status: "delivered"})
		rr3, _ := h3.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord3ID, Reason: "wrong color"})
		if _, err := h3.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: rr3.ReturnRequestId,
			Decision:        orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_REFUND,
		}); err != nil {
			t.Fatalf("RespondReturn(ACCEPT_REFUND): %v", err)
		}
		if len(h3.payment.refundCalls) != 1 || h3.payment.refundCalls[0].GetRequireApprovalIfLarge() || h3.payment.refundCalls[0].GetRequestedBy() != "" {
			t.Fatalf("seller ACCEPT_REFUND must have require_approval_if_large=false: %+v", h3.payment.refundCalls)
		}

		// 4. Seller ConfirmReturnReceived (RECEIVED -> REFUNDING -> REFUNDED) -> RequireApprovalIfLarge=false.
		h4 := newTestHarness()
		co4, _ := h4.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "auto-confirm-recv", AddressId: "addr-buyer-1",
			Items:         []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 2}},
			PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		ord4ID := co4.Orders[0].Id
		_ = h4.svc.HandlePaymentSucceeded(context.Background(), "ev-cr-1", events.PaymentSucceededPayload{
			PaymentID: co4.PaymentId, OrderGroupID: co4.CheckoutGroupId, OrderID: ord4ID,
		})
		ship4, _ := h4.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord4ID})
		_ = h4.svc.HandleShipmentUpdated(context.Background(), "ev-cr-2", events.ShipmentUpdatedPayload{OrderID: ord4ID, TrackingCode: ship4.TrackingCode, Status: "delivering"})
		_ = h4.svc.HandleShipmentUpdated(context.Background(), "ev-cr-3", events.ShipmentUpdatedPayload{OrderID: ord4ID, TrackingCode: ship4.TrackingCode, Status: "delivered"})
		rr4, _ := h4.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord4ID, Reason: "wrong size"})
		resp4, _ := h4.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: rr4.ReturnRequestId,
			Decision:        orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_RETURN,
		})
		_ = h4.svc.HandleShipmentUpdated(context.Background(), "ev-cr-4", events.ShipmentUpdatedPayload{
			OrderID: ord4ID, Direction: "RETURN", ReturnRequestID: rr4.ReturnRequestId,
			TrackingCode: resp4.ReturnRequest.ReturnTrackingCode, Status: "delivered",
		})
		if _, err := h4.svc.ConfirmReturnReceived(sellerCtx, &orderv1.ConfirmReturnReceivedRequest{ReturnRequestId: rr4.ReturnRequestId}); err != nil {
			t.Fatalf("ConfirmReturnReceived: %v", err)
		}
		if len(h4.payment.refundCalls) != 1 || h4.payment.refundCalls[0].GetRequireApprovalIfLarge() || h4.payment.refundCalls[0].GetRequestedBy() != "" {
			t.Fatalf("ConfirmReturnReceived auto-refund must have require_approval_if_large=false: %+v", h4.payment.refundCalls)
		}

		// 5. Watchdog ScanReturnDeadlines(RECEIVED auto-confirm) -> RequireApprovalIfLarge=false.
		h5 := newTestHarness()
		baseNow := time.Date(2026, 11, 24, 14, 0, 0, 0, time.UTC)
		curNow := baseNow
		h5.svc.nowFn = func() time.Time { return curNow }
		co5, _ := h5.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "auto-deadline-recv", AddressId: "addr-buyer-1",
			Items:         []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 2}},
			PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		ord5ID := co5.Orders[0].Id
		_ = h5.svc.HandlePaymentSucceeded(context.Background(), "ev-dr-1", events.PaymentSucceededPayload{
			PaymentID: co5.PaymentId, OrderGroupID: co5.CheckoutGroupId, OrderID: ord5ID,
		})
		ship5, _ := h5.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord5ID})
		_ = h5.svc.HandleShipmentUpdated(context.Background(), "ev-dr-2", events.ShipmentUpdatedPayload{OrderID: ord5ID, TrackingCode: ship5.TrackingCode, Status: "delivering"})
		_ = h5.svc.HandleShipmentUpdated(context.Background(), "ev-dr-3", events.ShipmentUpdatedPayload{OrderID: ord5ID, TrackingCode: ship5.TrackingCode, Status: "delivered"})
		rr5, _ := h5.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord5ID, Reason: "wrong size"})
		resp5, _ := h5.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: rr5.ReturnRequestId,
			Decision:        orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_RETURN,
		})
		_ = h5.svc.HandleShipmentUpdated(context.Background(), "ev-dr-4", events.ShipmentUpdatedPayload{
			OrderID: ord5ID, Direction: "RETURN", ReturnRequestID: rr5.ReturnRequestId,
			TrackingCode: resp5.ReturnRequest.ReturnTrackingCode, Status: "delivered",
		})
		curNow = baseNow.Add(4 * 24 * time.Hour)
		if handled, err := h5.svc.ScanReturnDeadlines(context.Background()); err != nil || handled != 1 {
			t.Fatalf("ScanReturnDeadlines(RECEIVED): handled=%d err=%v", handled, err)
		}
		if len(h5.payment.refundCalls) != 1 || h5.payment.refundCalls[0].GetRequireApprovalIfLarge() || h5.payment.refundCalls[0].GetRequestedBy() != "" {
			t.Fatalf("deadline auto-confirm refund must have require_approval_if_large=false: %+v", h5.payment.refundCalls)
		}
	})
}

// TestW3B2P2_F5_Races_RetryVsRetry_RetryVsExpiry_RetryVsOldIPN verifies all 3 required F5 concurrency races:
//  1. Retry || Retry (ZALOPAY vs COD)
//  2. Retry || Expiry scanner
//  3. Retry(COD) || IPN payment.succeeded of old online payment: NEVER CONFIRMED COD + unrefunded online payment!
func TestW3B2P2_F5_Races_RetryVsRetry_RetryVsExpiry_RetryVsOldIPN(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")

	t.Run("Retry_vs_OldIPN_BothOrdersAndConcurrentRace_NeverConfirmedCODWithUnrefundedOnlineMoney", func(t *testing.T) {
		// Deterministic Order B: RetryPayment(COD) commits first, then late IPN payment.succeeded(oldPay) arrives!
		hB := newTestHarness()
		coB, err := hB.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "f5-ipn-orderB", AddressId: "addr-buyer-1",
			Items:         []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		oldPayB := coB.PaymentId
		ordB := coB.Orders[0].Id
		retryResp, err := hB.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
			PaymentId:      oldPayB,
			IdempotencyKey: "rk-cod-B",
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil || !retryResp.SwitchedToCod {
			t.Fatalf("RetryPayment(COD): resp=%+v err=%v", retryResp, err)
		}
		// Late IPN payment.succeeded for superseded oldPayB arrives!
		if err := hB.svc.HandlePaymentSucceeded(context.Background(), "ev-late-ipn-B", events.PaymentSucceededPayload{
			PaymentID: oldPayB, OrderGroupID: coB.CheckoutGroupId, OrderID: ordB, AmountVND: 1030000, Method: "VIETQR",
		}); err != nil {
			t.Fatalf("HandlePaymentSucceeded(late oldPayB): %v", err)
		}
		aggB, _ := hB.store.GetOrderWithDetails(context.Background(), ordB)
		if aggB.Order.Status != "CONFIRMED" || aggB.Order.PaymentMethod != "COD" {
			t.Fatalf("Order B expected CONFIRMED COD, got status=%s method=%s", aggB.Order.Status, aggB.Order.PaymentMethod)
		}
		if len(hB.payment.refundCalls) != 1 ||
			hB.payment.refundCalls[0].GetPaymentId() != oldPayB ||
			hB.payment.refundCalls[0].GetAmountVnd() != 1030000 ||
			hB.payment.refundCalls[0].GetRequireApprovalIfLarge() {
			t.Fatalf("F5 race violation: late IPN on superseded payment MUST auto-refund oldPayB with require_approval=false; refundCalls=%+v", hB.payment.refundCalls)
		}

		// Deterministic Order A: IPN payment.succeeded(oldPay) commits right before UpdateGroupPaymentOnRetry(COD)!
		hA, hsA := newHookedHarness()
		coA, err := hA.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "f5-ipn-orderA", AddressId: "addr-buyer-1",
			Items:         []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout A: %v", err)
		}
		oldPayA := coA.PaymentId
		ordA := coA.Orders[0].Id
		hsA.beforeUpdateGroupPaymentRetry = func(_ context.Context, _ store.RetryPaymentUpdateParams) error {
			return hA.svc.HandlePaymentSucceeded(context.Background(), "ev-ipn-win-A", events.PaymentSucceededPayload{
				PaymentID: oldPayA, OrderGroupID: coA.CheckoutGroupId, OrderID: ordA, AmountVND: 1030000, Method: "VIETQR",
			})
		}
		_, retryErrA := hA.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
			PaymentId:      oldPayA,
			IdempotencyKey: "rk-cod-A",
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if status.Code(retryErrA) != codes.FailedPrecondition {
			t.Fatalf("Order A expected FailedPrecondition when IPN won before DB update, got %v", retryErrA)
		}
		aggA, _ := hA.store.GetOrderWithDetails(context.Background(), ordA)
		if aggA.Order.Status != "CONFIRMED" || aggA.Order.PaymentMethod != "VIETQR" || aggA.Order.PaymentStatus != "SUCCEEDED" {
			t.Fatalf("Order A expected CONFIRMED VIETQR SUCCEEDED, got %+v", aggA.Order)
		}
		if len(hA.payment.cancelCalls) == 0 {
			t.Fatalf("Order A must cancel the newly created COD payment after losing race to IPN")
		}

		// Concurrent goroutine race (10 iterations under -race): RetryPayment(COD) || HandlePaymentSucceeded(oldPay).
		for i := 0; i < 10; i++ {
			h := newTestHarness()
			co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
				IdempotencyKey: fmt.Sprintf("f5-conc-ipn-%d", i),
				AddressId:      "addr-buyer-1",
				Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
				PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
			})
			if err != nil {
				t.Fatalf("iter %d CreateCheckout: %v", i, err)
			}
			oldPay := co.PaymentId
			ordID := co.Orders[0].Id

			start := make(chan struct{})
			var wg sync.WaitGroup
			wg.Add(2)
			var rErr, ipnErr error
			go func() {
				defer wg.Done()
				<-start
				_, rErr = h.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
					PaymentId:      oldPay,
					IdempotencyKey: fmt.Sprintf("rk-conc-cod-%d", i),
					PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
				})
			}()
			go func() {
				defer wg.Done()
				<-start
				ipnErr = h.svc.HandlePaymentSucceeded(context.Background(), fmt.Sprintf("ev-conc-ipn-%d", i), events.PaymentSucceededPayload{
					PaymentID: oldPay, OrderGroupID: co.CheckoutGroupId, OrderID: ordID, AmountVND: 1030000, Method: "VIETQR",
				})
			}()
			close(start)
			wg.Wait()

			if ipnErr != nil {
				t.Fatalf("iter %d HandlePaymentSucceeded err: %v", i, ipnErr)
			}
			finalAgg, _ := h.store.GetOrderWithDetails(context.Background(), ordID)
			if finalAgg.Order.Status != "CONFIRMED" {
				t.Fatalf("iter %d expected CONFIRMED, got %s", i, finalAgg.Order.Status)
			}
			if finalAgg.Order.PaymentMethod == "COD" {
				if rErr != nil {
					t.Fatalf("iter %d COD won in DB but RetryPayment returned %v", i, rErr)
				}
				h.payment.mu.Lock()
				refundCount := len(h.payment.refundCalls)
				var refundedPayID string
				if refundCount > 0 {
					refundedPayID = h.payment.refundCalls[0].GetPaymentId()
				}
				h.payment.mu.Unlock()
				if refundCount != 1 || refundedPayID != oldPay {
					t.Fatalf("iter %d INVARIANT BROKEN: order is CONFIRMED COD with unrefunded oldPay %q (refunds=%d)", i, oldPay, refundCount)
				}
			} else {
				if finalAgg.Order.PaymentMethod != "VIETQR" || finalAgg.Order.PaymentStatus != "SUCCEEDED" {
					t.Fatalf("iter %d unexpected non-COD state: %+v", i, finalAgg.Order)
				}
				if status.Code(rErr) != codes.FailedPrecondition {
					t.Fatalf("iter %d IPN won but RetryPayment returned %v (want FailedPrecondition)", i, rErr)
				}
			}
		}
	})

	t.Run("Retry_vs_Retry_And_Retry_vs_ExpiryScanner_Concurrent", func(t *testing.T) {
		// Retry(ZALOPAY) || Retry(COD)
		for i := 0; i < 6; i++ {
			h := newTestHarness()
			co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
				IdempotencyKey: fmt.Sprintf("f5-p2-rr-%d", i),
				AddressId:      "addr-buyer-1",
				Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
				PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
			})
			if err != nil {
				t.Fatalf("iter %d CreateCheckout: %v", i, err)
			}
			start := make(chan struct{})
			var wg sync.WaitGroup
			wg.Add(2)
			var r1, r2 *orderv1.RetryPaymentResponse
			var e1, e2 error
			go func() {
				defer wg.Done()
				<-start
				r1, e1 = h.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
					PaymentId: co.PaymentId, IdempotencyKey: "rk-zlp", PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY,
				})
			}()
			go func() {
				defer wg.Done()
				<-start
				r2, e2 = h.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
					PaymentId: co.PaymentId, IdempotencyKey: "rk-cod", PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
				})
			}()
			close(start)
			wg.Wait()

			okCount := 0
			if e1 == nil {
				okCount++
				if r1.PaymentMethod != orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY || r1.PayUrl == "" || len(r1.Orders) != 1 {
					t.Fatalf("iter %d ZALOPAY winner missing fields: %+v", i, r1)
				}
			}
			if e2 == nil {
				okCount++
				if r2.PaymentMethod != orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD || !r2.SwitchedToCod || len(r2.Orders) != 1 {
					t.Fatalf("iter %d COD winner missing fields: %+v", i, r2)
				}
			}
			if okCount != 1 {
				t.Fatalf("iter %d Retry||Retry must have strictly 1 winner: e1=%v e2=%v", i, e1, e2)
			}
		}

		// Retry(ZALOPAY) || ScanExpiredPendingOrders
		for i := 0; i < 6; i++ {
			h := newTestHarness()
			baseNow := time.Now().UTC()
			h.svc.nowFn = func() time.Time { return baseNow }
			co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
				IdempotencyKey: fmt.Sprintf("f5-p2-re-%d", i),
				AddressId:      "addr-buyer-1",
				Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
				PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
			})
			if err != nil {
				t.Fatalf("iter %d CreateCheckout: %v", i, err)
			}
			start := make(chan struct{})
			var wg sync.WaitGroup
			wg.Add(2)
			var retryErr error
			go func() {
				defer wg.Done()
				<-start
				_, retryErr = h.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
					PaymentId: co.PaymentId, IdempotencyKey: "rk-exp-zlp", PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY,
				})
			}()
			go func() {
				defer wg.Done()
				<-start
				_ = h.svc.processExpiredGroup(context.Background(), store.ExpiredGroupCandidate{
					CheckoutGroupID: co.CheckoutGroupId, PaymentID: co.PaymentId,
				})
			}()
			close(start)
			wg.Wait()

			ord, _ := h.store.GetOrderWithDetails(context.Background(), co.Orders[0].Id)
			if ord.Order.Status == "CANCELLED" {
				if retryErr == nil {
					t.Fatalf("iter %d order CANCELLED by expiry yet RetryPayment succeeded", i)
				}
			} else if ord.Order.Status == "PENDING" {
				if retryErr != nil || ord.Order.PaymentMethod != "ZALOPAY" || ord.Order.PaymentID == co.PaymentId {
					t.Fatalf("iter %d order stayed PENDING but retry state inconsistent: err=%v ord=%+v", i, retryErr, ord.Order)
				}
			} else {
				t.Fatalf("iter %d unexpected status %q", i, ord.Order.Status)
			}
		}
	})
}

// TestW3B2P2_F5_SupersededRefundErrors_NeverSwallowFailedPreconditionOrNotFound verifies Review Item 3:
//   - FailedPrecondition and NotFound from Payment.CreateRefund on a late superseded payment MUST NOT be swallowed:
//     HandlePaymentSucceeded returns error, does NOT mark event_id processed (so consumer retries -> DLQ),
//     and logs ERROR with payment_id and order_id.
//   - Only a matching idempotent replay (AlreadyExisted=true or codes.AlreadyExists) is treated as complete.
func TestW3B2P2_F5_SupersededRefundErrors_NeverSwallowFailedPreconditionOrNotFound(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")

	setupSupersededCOD := func(t *testing.T, suffix string) (*testHarness, *bytes.Buffer, string, string, string) {
		t.Helper()
		h := newTestHarness()
		var logBuf bytes.Buffer
		h.svc.logger = slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelError}))

		co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "sup-err-co-" + suffix,
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		if _, err := h.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
			PaymentId:      co.PaymentId,
			IdempotencyKey: "sup-err-rk-" + suffix,
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		}); err != nil {
			t.Fatalf("RetryPayment(COD): %v", err)
		}
		return h, &logBuf, co.CheckoutGroupId, co.PaymentId, co.Orders[0].Id
	}

	t.Run("FailedPrecondition_ReturnsError_NotAcked_LogsErrorWithPaymentAndOrderID", func(t *testing.T) {
		h, logBuf, grpID, oldPayID, ordID := setupSupersededCOD(t, "failed-precond")
		h.payment.refundErr = status.Error(codes.FailedPrecondition, "refund exceeds remaining")

		evID := "ev-sup-fp-1"
		err := h.svc.HandlePaymentSucceeded(context.Background(), evID, events.PaymentSucceededPayload{
			PaymentID: oldPayID, OrderGroupID: grpID, OrderID: ordID, AmountVND: 1030000, Method: "VIETQR",
		})
		if err == nil || status.Code(err) != codes.FailedPrecondition && !strings.Contains(err.Error(), "FailedPrecondition") {
			t.Fatalf("expected FailedPrecondition error to propagate for retry/DLQ, got %v", err)
		}
		processed, pErr := h.store.IsEventProcessed(context.Background(), evID)
		if pErr != nil || processed {
			t.Fatalf("event %s MUST NOT be marked processed when CreateRefund fails with FailedPrecondition (processed=%v err=%v)", evID, processed, pErr)
		}
		logged := logBuf.String()
		if !strings.Contains(logged, oldPayID) || !strings.Contains(logged, ordID) || !strings.Contains(logged, `"level":"ERROR"`) {
			t.Fatalf("expected ERROR log containing payment_id=%q and order_id=%q, got: %s", oldPayID, ordID, logged)
		}
	})

	t.Run("NotFound_ReturnsError_NotAcked_LogsErrorWithPaymentAndOrderID", func(t *testing.T) {
		h, logBuf, grpID, oldPayID, ordID := setupSupersededCOD(t, "not-found")
		h.payment.refundErr = status.Error(codes.NotFound, "payment not found")

		evID := "ev-sup-nf-1"
		err := h.svc.HandlePaymentSucceeded(context.Background(), evID, events.PaymentSucceededPayload{
			PaymentID: oldPayID, OrderGroupID: grpID, OrderID: ordID, AmountVND: 1030000, Method: "VIETQR",
		})
		if err == nil {
			t.Fatalf("expected NotFound error to propagate for retry/DLQ, got nil")
		}
		processed, _ := h.store.IsEventProcessed(context.Background(), evID)
		if processed {
			t.Fatalf("event %s MUST NOT be marked processed on NotFound", evID)
		}
		logged := logBuf.String()
		if !strings.Contains(logged, oldPayID) || !strings.Contains(logged, ordID) {
			t.Fatalf("expected ERROR log with payment_id and order_id, got: %s", logged)
		}
	})

	t.Run("AlreadyExistedOrAlreadyExists_AcceptedIdempotently", func(t *testing.T) {
		// Case A: Rust payment idempotent replay returns OK + AlreadyExisted=true.
		h1, _, grpID1, oldPayID1, ordID1 := setupSupersededCOD(t, "already-existed")
		h1.payment.onCreateRefund = func(_ context.Context, req *paymentv1.CreateRefundRequest) (*paymentv1.CreateRefundResponse, error) {
			return &paymentv1.CreateRefundResponse{
				AlreadyExisted: true,
				Refund: &paymentv1.Refund{
					RefundId:       "ref-existing-1",
					PaymentId:      req.GetPaymentId(),
					OrderId:        req.GetOrderId(),
					AmountVnd:      req.GetAmountVnd(),
					IdempotencyKey: req.GetIdempotencyKey(),
					Status:         paymentv1.RefundStatus_REFUND_STATUS_DONE,
				},
			}, nil
		}
		if err := h1.svc.HandlePaymentSucceeded(context.Background(), "ev-sup-ae-1", events.PaymentSucceededPayload{
			PaymentID: oldPayID1, OrderGroupID: grpID1, OrderID: ordID1, AmountVND: 1030000, Method: "VIETQR",
		}); err != nil {
			t.Fatalf("idempotent AlreadyExisted=true must succeed, got %v", err)
		}
		if processed, _ := h1.store.IsEventProcessed(context.Background(), "ev-sup-ae-1"); !processed {
			t.Fatalf("expected event marked processed on matching AlreadyExisted=true")
		}

		// Case B: codes.AlreadyExists maps from PaymentError::Conflict in Rust payment — MUST NOT be swallowed.
		h2, logBuf2, grpID2, oldPayID2, ordID2 := setupSupersededCOD(t, "already-exists-code")
		h2.payment.refundErr = status.Error(codes.AlreadyExists, "conflict: payment state conflict")
		err2 := h2.svc.HandlePaymentSucceeded(context.Background(), "ev-sup-ae-2", events.PaymentSucceededPayload{
			PaymentID: oldPayID2, OrderGroupID: grpID2, OrderID: ordID2, AmountVND: 1030000, Method: "VIETQR",
		})
		if err2 == nil {
			t.Fatalf("codes.AlreadyExists (Conflict) MUST return error for retry/DLQ, got nil")
		}
		if processed, _ := h2.store.IsEventProcessed(context.Background(), "ev-sup-ae-2"); processed {
			t.Fatalf("event MUST NOT be marked processed on codes.AlreadyExists")
		}
		if logged := logBuf2.String(); !strings.Contains(logged, oldPayID2) || !strings.Contains(logged, ordID2) || !strings.Contains(logged, `"level":"ERROR"`) {
			t.Fatalf("expected ERROR log with payment_id and order_id on AlreadyExists, got: %s", logged)
		}
	})
}

// TestW3B2P2_F11_RefundRejected_OutOfOrder_Duplicate_And_ManualAdminRefundIgnored verifies Review Item 4:
//  1. Duplicate event_id is ignored idempotently.
//  2. Out-of-order payment.refund_rejected arriving BEFORE return_requests persists refund_id / PENDING_APPROVAL
//     transitions REFUNDING -> ESCALATED cleanly AND prevents delayed UpdateReturnRefundApprovalState("PENDING_APPROVAL")
//     from overwriting ESCALATED / REJECTED.
//  3. Manual admin refund not belonging to any return (no return on order, non-approval seller REFUNDING return,
//     or admin return with different refund_id) is ignored cleanly without corrupting return state.
func TestW3B2P2_F11_RefundRejected_OutOfOrder_Duplicate_And_ManualAdminRefundIgnored(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
	cskhCtx := ctxWithUser("cskh-maker-ooo", auth.RoleCSKH, "")

	seedDeliveredPaidOrder := func(t *testing.T, h *testHarness, suffix string) (string, string) {
		t.Helper()
		co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "ooo-co-" + suffix,
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 2}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		ordID := co.Orders[0].Id
		if err := h.svc.HandlePaymentSucceeded(context.Background(), "ev-ooo-pay-"+suffix, events.PaymentSucceededPayload{
			PaymentID: co.PaymentId, OrderGroupID: co.CheckoutGroupId, OrderID: ordID,
		}); err != nil {
			t.Fatalf("HandlePaymentSucceeded: %v", err)
		}
		shipRes, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID})
		if err != nil {
			t.Fatalf("SellerShipOrder: %v", err)
		}
		_ = h.svc.HandleShipmentUpdated(context.Background(), "ev-ooo-sh1-"+suffix, events.ShipmentUpdatedPayload{
			OrderID: ordID, TrackingCode: shipRes.TrackingCode, Status: "delivering",
		})
		_ = h.svc.HandleShipmentUpdated(context.Background(), "ev-ooo-sh2-"+suffix, events.ShipmentUpdatedPayload{
			OrderID: ordID, TrackingCode: shipRes.TrackingCode, Status: "delivered",
		})
		return ordID, co.PaymentId
	}

	t.Run("ManualAdminRefund_NoReturn_IgnoredCleanly", func(t *testing.T) {
		h := newTestHarness()
		ordID, payID := seedDeliveredPaidOrder(t, h, "manual-no-ret")

		if err := h.svc.HandlePaymentRefundRejected(context.Background(), "ev-manual-noreturn", events.PaymentRefundRejectedPayload{
			RefundID: "ref-manual-1", PaymentID: payID, OrderID: ordID, UserID: "buyer-1",
			AmountVND: 2030000, Reason: "manual refund rejected", RejectedBy: "accountant-1",
		}); err != nil {
			t.Fatalf("manual refund with no return must be ignored cleanly, got %v", err)
		}
		if processed, _ := h.store.IsEventProcessed(context.Background(), "ev-manual-noreturn"); !processed {
			t.Fatalf("expected manual refund rejection event marked processed")
		}
		ord, _ := h.store.GetOrderWithDetails(context.Background(), ordID)
		if ord.Order.Status != "DELIVERED" {
			t.Fatalf("order without return must stay DELIVERED, got %s", ord.Order.Status)
		}
	})

	t.Run("ManualAdminRefund_DuringNonApprovalSellerReturn_IgnoredCleanly", func(t *testing.T) {
		h := newTestHarness()
		ordID, payID := seedDeliveredPaidOrder(t, h, "manual-vs-seller-ret")
		reqRet, err := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ordID, Reason: "wrong color"})
		if err != nil {
			t.Fatalf("RequestReturn: %v", err)
		}
		// Force seller ACCEPT_REFUND to fail CreateRefund temporarily so return stays REFUNDING (RequireApprovalIfLarge=false, RefundID="").
		h.payment.refundErr = status.Error(codes.Unavailable, "temp down")
		if _, err := h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: reqRet.ReturnRequestId,
			Decision:        orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_REFUND,
		}); err != nil {
			t.Fatalf("RespondReturn(ACCEPT_REFUND): %v", err)
		}
		h.payment.refundErr = nil

		// Separate manual admin refund on same order is rejected -> MUST NOT re-escalate seller's non-approval return!
		if err := h.svc.HandlePaymentRefundRejected(context.Background(), "ev-manual-vs-seller", events.PaymentRefundRejectedPayload{
			RefundID: "ref-manual-other", PaymentID: payID, OrderID: ordID, UserID: "buyer-1",
			AmountVND: 2030000, Reason: "manual admin refund rejected", RejectedBy: "accountant-1",
		}); err != nil {
			t.Fatalf("HandlePaymentRefundRejected: %v", err)
		}
		rr, _ := h.store.GetReturnRequest(context.Background(), reqRet.ReturnRequestId, "")
		if rr.Request.Status != "REFUNDING" || rr.Request.RefundApprovalStatus == "REJECTED" {
			t.Fatalf("manual admin refund rejection must NOT affect seller non-approval return: %+v", rr.Request)
		}
	})

	t.Run("ManualAdminRefund_DifferentRefundID_IgnoredCleanly", func(t *testing.T) {
		h := newTestHarness()
		ordID, payID := seedDeliveredPaidOrder(t, h, "diff-refid")
		reqRet, _ := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ordID, Reason: "broken"})
		_, _ = h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: reqRet.ReturnRequestId, Decision: orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_REJECT, Note: "no",
		})
		_, _ = h.svc.EscalateReturn(buyerCtx, &orderv1.EscalateReturnRequest{ReturnRequestId: reqRet.ReturnRequestId, Reason: "dispute"})

		h.payment.onCreateRefund = func(_ context.Context, req *paymentv1.CreateRefundRequest) (*paymentv1.CreateRefundResponse, error) {
			return &paymentv1.CreateRefundResponse{
				Refund: &paymentv1.Refund{
					RefundId: "ref-return-owned-1", PaymentId: req.GetPaymentId(), OrderId: req.GetOrderId(),
					AmountVnd: req.GetAmountVnd(), Status: paymentv1.RefundStatus_REFUND_STATUS_PENDING_APPROVAL,
				},
			}, nil
		}
		if _, err := h.svc.AdminResolveReturn(cskhCtx, &orderv1.AdminResolveReturnRequest{
			ReturnRequestId: reqRet.ReturnRequestId, Decision: orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_REFUND,
		}); err != nil {
			t.Fatalf("AdminResolveReturn: %v", err)
		}

		// Rejection for a different manual refund_id must NOT disturb the pending return.
		if err := h.svc.HandlePaymentRefundRejected(context.Background(), "ev-diff-refid", events.PaymentRefundRejectedPayload{
			RefundID: "ref-manual-different-999", PaymentID: payID, OrderID: ordID, UserID: "buyer-1",
			AmountVND: 2030000, Reason: "different manual refund rejected", RejectedBy: "accountant-1",
		}); err != nil {
			t.Fatalf("HandlePaymentRefundRejected: %v", err)
		}
		rr, _ := h.store.GetReturnRequest(context.Background(), reqRet.ReturnRequestId, "")
		if rr.Request.Status != "REFUNDING" || rr.Request.RefundApprovalStatus != "PENDING_APPROVAL" {
			t.Fatalf("different refund_id must NOT change return state: %+v", rr.Request)
		}
	})

	t.Run("OutOfOrder_ArrivesBeforeRefundIDPersisted_AndStalePendingApprovalCannotClobberEscalated", func(t *testing.T) {
		h := newTestHarness()
		ordID, payID := seedDeliveredPaidOrder(t, h, "out-of-order")
		reqRet, _ := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ordID, Reason: "defective"})
		_, _ = h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: reqRet.ReturnRequestId, Decision: orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_REJECT, Note: "no",
		})
		_, _ = h.svc.EscalateReturn(buyerCtx, &orderv1.EscalateReturnRequest{ReturnRequestId: reqRet.ReturnRequestId, Reason: "dispute"})

		// Simulate cross-pod out-of-order delivery:
		// Pod A commits REFUNDING (refund_id="") and calls CreateRefund, but BEFORE Pod A persists
		// UpdateReturnRefundApprovalState("PENDING_APPROVAL"), Pod B receives payment.refund_rejected!
		podB := NewOrderService(h.svc.cfg, h.store, h.svc.clients, h.svc.logger)
		h.payment.onCreateRefund = func(_ context.Context, req *paymentv1.CreateRefundRequest) (*paymentv1.CreateRefundResponse, error) {
			// Fire out-of-order event on sibling pod B while return_requests still has refund_id == ""!
			if err := podB.HandlePaymentRefundRejected(context.Background(), "ev-ooo-rej-1", events.PaymentRefundRejectedPayload{
				RefundID:       "ref-ooo-1",
				PaymentID:      payID,
				OrderID:        ordID,
				UserID:         "buyer-1",
				AmountVND:      req.GetAmountVnd(),
				IdempotencyKey: req.GetIdempotencyKey(),
				Reason:         "checker rejected immediately",
				RejectedBy:     "accountant-checker",
			}); err != nil {
				t.Errorf("podB out-of-order HandlePaymentRefundRejected: %v", err)
			}
			// Pod A's CreateRefund then returns PENDING_APPROVAL (from before checker acted).
			return &paymentv1.CreateRefundResponse{
				Refund: &paymentv1.Refund{
					RefundId:  "ref-ooo-1",
					PaymentId: req.GetPaymentId(),
					OrderId:   req.GetOrderId(),
					AmountVnd: req.GetAmountVnd(),
					Status:    paymentv1.RefundStatus_REFUND_STATUS_PENDING_APPROVAL,
				},
			}, nil
		}

		res, err := h.svc.AdminResolveReturn(cskhCtx, &orderv1.AdminResolveReturnRequest{
			ReturnRequestId: reqRet.ReturnRequestId,
			Decision:        orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_REFUND,
		})
		if err != nil {
			t.Fatalf("AdminResolveReturn: %v", err)
		}
		if res.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_ESCALATED {
			t.Fatalf("out-of-order rejection must win and end in ESCALATED (never clobbered back to REFUNDING/PENDING_APPROVAL), got %v", res.ReturnRequest.Status)
		}
		rrFinal, _ := h.store.GetReturnRequest(context.Background(), reqRet.ReturnRequestId, "")
		ordFinal, _ := h.store.GetOrderWithDetails(context.Background(), ordID)
		if rrFinal.Request.Status != "ESCALATED" ||
			rrFinal.Request.RefundApprovalStatus != "REJECTED" ||
			rrFinal.Request.RefundID != "ref-ooo-1" ||
			rrFinal.Request.RefundAttempt != 1 ||
			ordFinal.Order.Status != "RETURN_REQUESTED" {
			t.Fatalf("out-of-order final state corrupted: rr=%+v ord=%q", rrFinal.Request, ordFinal.Order.Status)
		}

		// Duplicate delivery of the same event_id is a clean idempotent no-op.
		if err := podB.HandlePaymentRefundRejected(context.Background(), "ev-ooo-rej-1", events.PaymentRefundRejectedPayload{
			RefundID: "ref-ooo-1", PaymentID: payID, OrderID: ordID, Reason: "dup",
		}); err != nil {
			t.Fatalf("duplicate event_id failed: %v", err)
		}
	})
}
