package service

import (
	"context"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	cartv1 "github.com/nexusmart/core/gen/go/nexusmart/cart/v1"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/order/internal/clients"
	"github.com/nexusmart/core/services/order/internal/db"
	"github.com/nexusmart/core/services/order/internal/events"
	"github.com/nexusmart/core/services/order/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func boolPtr(v bool) *bool { return &v }

// TestW3A_1_D3_ReservePromotionUsesOrderServiceToken verifies D3:
//   - CreateCheckout calls Promotion.ReservePromotion using order-service's internal service token
//     (clients.InternalServiceName == "order-service", role == auth.ServiceRole, rawToken == ""),
//     NOT forwarding the buyer JWT.
//   - Request carries verified buyer user_id.
//   - Other user-scoped calls (CreatePayment, GetAddress for buyer) keep the buyer JWT.
func TestW3A_1_D3_ReservePromotionUsesOrderServiceToken(t *testing.T) {
	h := newTestHarness()
	h.promo.breakdown = &promotionv1.DiscountBreakdown{
		Shops: []*promotionv1.ShopDiscountBreakdown{
			{ShopId: "shop-1", SubtotalVnd: 1000000, PlatformVoucherDiscountVnd: 50000, ShippingFeeVnd: 30000},
		},
	}

	buyerCtx := auth.WithRawToken(ctxWithUser("buyer-1", "buyer", ""), "Bearer buyer-secret-jwt")
	resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey:    "w3a-d3-promo-svc-tok",
		AddressId:         "addr-buyer-1",
		Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PlatformVoucherId: "v-plat-1",
		CoinsToUse:        1000,
		PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
	})
	if err != nil {
		t.Fatalf("CreateCheckout: %v", err)
	}
	if resp.CheckoutGroupId == "" || len(resp.Orders) != 1 {
		t.Fatalf("unexpected checkout response: %+v", resp)
	}

	if len(h.promo.reserveAuth) != 1 || len(h.promo.reserveReqs) != 1 {
		t.Fatalf("expected 1 ReservePromotion call, got auth=%d reqs=%d", len(h.promo.reserveAuth), len(h.promo.reserveReqs))
	}
	gotAuth := h.promo.reserveAuth[0]
	if gotAuth.service != clients.InternalServiceName || gotAuth.role != auth.ServiceRole || gotAuth.rawToken != "" {
		t.Fatalf("D3 violation: ReservePromotion must use order-service token with empty raw buyer JWT, got %+v", gotAuth)
	}
	if h.promo.reserveReqs[0].GetUserId() != "buyer-1" {
		t.Fatalf("D3 violation: ReservePromotion user_id = %q, want buyer-1", h.promo.reserveReqs[0].GetUserId())
	}

	// Verify buyer JWT is preserved on CreatePayment and buyer GetAddress.
	if len(h.payment.createAuth) != 1 || h.payment.createAuth[0].service != "" || h.payment.createAuth[0].rawToken != "Bearer buyer-secret-jwt" {
		t.Fatalf("CreatePayment must keep buyer JWT, got %+v", h.payment.createAuth)
	}
	buyerAddrAuth := h.user.getAddressAuth["addr-buyer-1"]
	if buyerAddrAuth.role != "buyer" || buyerAddrAuth.rawToken != "Bearer buyer-secret-jwt" {
		t.Fatalf("buyer GetAddress must keep buyer JWT, got %+v", buyerAddrAuth)
	}
}

// TestW3A_3_D10_PaymentRefundedIsPostSettlementAndReports verifies D10 / N11:
//   - Pre-settlement refund (is_post_settlement=false) arriving BEFORE order.completed.
//   - Pre-settlement refund (is_post_settlement=false) arriving AFTER order.completed (late NATS delivery).
//   - Mixed pre-before + late pre-after + post-settlement (is_post_settlement=true) after completed.
//   - Duplicate event_id is idempotent (no double count on refunded_vnd or settlement_refunded_vnd).
//   - Legacy event missing is_post_settlement (nil) preserves old behavior.
//   - Both GetSellerReport and GetPlatformReport match payment's compute_settlement_split waterfall.
func TestW3A_3_D10_PaymentRefundedIsPostSettlementAndReports(t *testing.T) {
	// Fixture numbers (P3-10 valid refunded <= P, exact VND integers):
	//   Subtotal = 1,000,000, V_plat = 0, Ship = 30,000
	//   R = 1,000,000; D = 0; P = 1,030,000
	//   C_cat (500 bps) = 50,000; C_pay (200 bps on P=1,030,000) = 20,600
	//   raw_net = 1,000,000 - 50,000 - 20,600 = 929,400
	// With pre-settlement refund = 940,000 (<= P = 1,030,000; exceeds raw_net by 10,600):
	//   seller_net = max(0, 929,400 - 940,000) = 0
	//   rem_after_seller = 10,600 -> deducted from payment_fee first:
	//   eff_payment_fee = 20,600 - 10,600 = 10,000
	//   eff_commission = 50,000
	//   platform_subsidy = max(0, R + Ship - P) = 0
	setupDeliveredOrder := func(t *testing.T, idemKey string) (*testHarness, string, string, time.Time) {
		t.Helper()
		h := newTestHarness()
		h.payment.feeBPSByMethod = map[paymentv1.PaymentMethod]int32{
			paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR: 200,
		}
		fixedNow := time.Date(2026, 11, 24, 5, 0, 0, 0, time.UTC) // 2026-11-24 12:00 VN
		h.svc.nowFn = func() time.Time { return fixedNow }

		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

		co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: idemKey,
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		ordID := co.Orders[0].Id
		payID := co.PaymentId

		if err := h.svc.HandlePaymentSucceeded(context.Background(), "evt-pay-ok-"+idemKey, events.PaymentSucceededPayload{
			PaymentID: payID, OrderGroupID: co.CheckoutGroupId, AmountVND: 1030000,
		}); err != nil {
			t.Fatalf("HandlePaymentSucceeded: %v", err)
		}
		shipResp, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID, ShopId: "shop-1"})
		if err != nil {
			t.Fatalf("SellerShipOrder: %v", err)
		}
		if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-deliv-"+idemKey, events.ShipmentUpdatedPayload{
			OrderID: ordID, TrackingCode: shipResp.TrackingCode, Status: "delivered",
		}); err != nil {
			t.Fatalf("HandleShipmentUpdated(delivered): %v", err)
		}
		return h, ordID, payID, fixedNow
	}

	assertReports := func(t *testing.T, h *testHarness, wantNet, wantComm, wantPayFee, wantSubsidy int64) {
		t.Helper()
		sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
		adminCtx := ctxWithUser("admin-1", "admin", "")
		sRep, err := h.svc.GetSellerReport(sellerCtx, &orderv1.GetSellerReportRequest{
			ShopId: "shop-1", FromDate: "2026-11-24", ToDate: "2026-11-24",
		})
		if err != nil {
			t.Fatalf("GetSellerReport: %v", err)
		}
		pRep, err := h.svc.GetPlatformReport(adminCtx, &orderv1.GetPlatformReportRequest{
			FromDate: "2026-11-24", ToDate: "2026-11-24",
		})
		if err != nil {
			t.Fatalf("GetPlatformReport: %v", err)
		}
		if sRep.GetNetRevenueVnd() != wantNet || sRep.GetCommissionVnd() != wantComm || sRep.GetPaymentFeeVnd() != wantPayFee {
			t.Fatalf("seller report mismatch: got net=%d comm=%d payFee=%d; want %d/%d/%d",
				sRep.GetNetRevenueVnd(), sRep.GetCommissionVnd(), sRep.GetPaymentFeeVnd(), wantNet, wantComm, wantPayFee)
		}
		if pRep.GetNetRevenueVnd() != wantNet || pRep.GetCommissionVnd() != wantComm || pRep.GetPaymentFeeVnd() != wantPayFee || pRep.GetPlatformSubsidyVnd() != wantSubsidy {
			t.Fatalf("platform report mismatch: got net=%d comm=%d payFee=%d subsidy=%d; want %d/%d/%d/%d",
				pRep.GetNetRevenueVnd(), pRep.GetCommissionVnd(), pRep.GetPaymentFeeVnd(), pRep.GetPlatformSubsidyVnd(),
				wantNet, wantComm, wantPayFee, wantSubsidy)
		}
	}

	t.Run("PreSettlementBeforeCompleted", func(t *testing.T) {
		h, ordID, payID, now := setupDeliveredOrder(t, "w3a-d10-pre-before")
		if err := h.svc.HandlePaymentRefunded(context.Background(), "evt-rf-before", events.PaymentRefundedPayload{
			RefundID: "rf-1", PaymentID: payID, OrderID: ordID, AmountVND: 940000,
			Reason: "pre-settlement partial", IsPostSettlement: boolPtr(false),
		}); err != nil {
			t.Fatalf("HandlePaymentRefunded: %v", err)
		}
		if _, err := h.svc.BuyerConfirmReceived(ctxWithUser("buyer-1", "buyer", ""), &orderv1.BuyerConfirmReceivedRequest{OrderId: ordID}); err != nil {
			t.Fatalf("BuyerConfirmReceived: %v", err)
		}
		h.store.SetOrderTimestamps(ordID, now, &now)
		agg, _ := h.store.GetOrderWithDetails(context.Background(), ordID)
		if agg.Order.RefundedVND != 940000 || agg.Order.SettlementRefundedVND != 940000 {
			t.Fatalf("refunded=%d settlement_refunded=%d, want 940000/940000", agg.Order.RefundedVND, agg.Order.SettlementRefundedVND)
		}
		assertReports(t, h, 0, 50000, 10000, 0)
	})

	t.Run("PreSettlementAfterCompleted_LateDelivery", func(t *testing.T) {
		h, ordID, payID, now := setupDeliveredOrder(t, "w3a-d10-pre-after")
		if _, err := h.svc.BuyerConfirmReceived(ctxWithUser("buyer-1", "buyer", ""), &orderv1.BuyerConfirmReceivedRequest{OrderId: ordID}); err != nil {
			t.Fatalf("BuyerConfirmReceived: %v", err)
		}
		h.store.SetOrderTimestamps(ordID, now, &now)

		// Late NATS delivery of pre-settlement refund (is_post_settlement=false) AFTER order.completed!
		if err := h.svc.HandlePaymentRefunded(context.Background(), "evt-rf-late-pre", events.PaymentRefundedPayload{
			RefundID: "rf-late", PaymentID: payID, OrderID: ordID, AmountVND: 940000,
			Reason: "late pre-settlement", IsPostSettlement: boolPtr(false),
		}); err != nil {
			t.Fatalf("HandlePaymentRefunded(late pre): %v", err)
		}

		agg, _ := h.store.GetOrderWithDetails(context.Background(), ordID)
		if agg.Order.RefundedVND != 940000 || agg.Order.SettlementRefundedVND != 940000 {
			t.Fatalf("D10 violation: late pre-settlement refund must increment settlement_refunded_vnd! got refunded=%d settlement_refunded=%d",
				agg.Order.RefundedVND, agg.Order.SettlementRefundedVND)
		}
		assertReports(t, h, 0, 50000, 10000, 0)
	})

	t.Run("MixedPreBefore_LatePreAfter_PostAfter_AndDuplicateEvent", func(t *testing.T) {
		h, ordID, payID, now := setupDeliveredOrder(t, "w3a-d10-mixed")
		// 1. Pre-settlement 100k before completed
		_ = h.svc.HandlePaymentRefunded(context.Background(), "evt-rf-m1", events.PaymentRefundedPayload{
			RefundID: "rf-m1", PaymentID: payID, OrderID: ordID, AmountVND: 100000,
			Reason: "pre1", IsPostSettlement: boolPtr(false),
		})
		// 2. Order completes (settlement_refunded_vnd snapshots 100k)
		if _, err := h.svc.BuyerConfirmReceived(ctxWithUser("buyer-1", "buyer", ""), &orderv1.BuyerConfirmReceivedRequest{OrderId: ordID}); err != nil {
			t.Fatalf("BuyerConfirmReceived: %v", err)
		}
		h.store.SetOrderTimestamps(ordID, now, &now)
		// 3. Late pre-settlement 840k arrives after completed (total pre = 940k <= P = 1,030,000)
		_ = h.svc.HandlePaymentRefunded(context.Background(), "evt-rf-m2", events.PaymentRefundedPayload{
			RefundID: "rf-m2", PaymentID: payID, OrderID: ordID, AmountVND: 840000,
			Reason: "late pre2", IsPostSettlement: boolPtr(false),
		})
		// 4. Duplicate delivery of evt-rf-m2 MUST be ignored (no double-count)
		_ = h.svc.HandlePaymentRefunded(context.Background(), "evt-rf-m2", events.PaymentRefundedPayload{
			RefundID: "rf-m2", PaymentID: payID, OrderID: ordID, AmountVND: 840000,
			Reason: "late pre2 dup", IsPostSettlement: boolPtr(false),
		})
		// 5. Post-settlement 10k arrives after completed (total refunded = 950k <= P = 1,030,000)
		_ = h.svc.HandlePaymentRefunded(context.Background(), "evt-rf-m3", events.PaymentRefundedPayload{
			RefundID: "rf-m3", PaymentID: payID, OrderID: ordID, AmountVND: 10000,
			Reason: "post-settlement", IsPostSettlement: boolPtr(true),
		})

		agg, _ := h.store.GetOrderWithDetails(context.Background(), ordID)
		if agg.Order.RefundedVND != 950000 || agg.Order.SettlementRefundedVND != 940000 {
			t.Fatalf("got refunded=%d settlement_refunded=%d, want 950000/940000", agg.Order.RefundedVND, agg.Order.SettlementRefundedVND)
		}
		// pre=940k -> seller_net_at_settle=0, rem=10600 -> effPayFee=10000, effComm=50000; post=10k -> net = 0 - 10000 = -10000
		assertReports(t, h, -10000, 50000, 10000, 0)
	})

	t.Run("LegacyEventMissingField_PreservesOldBehavior", func(t *testing.T) {
		h, ordID, payID, now := setupDeliveredOrder(t, "w3a-d10-legacy")
		if _, err := h.svc.BuyerConfirmReceived(ctxWithUser("buyer-1", "buyer", ""), &orderv1.BuyerConfirmReceivedRequest{OrderId: ordID}); err != nil {
			t.Fatalf("BuyerConfirmReceived: %v", err)
		}
		h.store.SetOrderTimestamps(ordID, now, &now)

		// Unmarshal legacy JSON without is_post_settlement -> pointer MUST be nil!
		rawLegacy := `{"refund_id":"rf-legacy","payment_id":"` + payID + `","order_id":"` + ordID + `","user_id":"buyer-1","amount_vnd":940000,"reason":"legacy"}`
		var payload events.PaymentRefundedPayload
		if err := json.Unmarshal([]byte(rawLegacy), &payload); err != nil {
			t.Fatalf("unmarshal legacy payload: %v", err)
		}
		if payload.IsPostSettlement != nil {
			t.Fatalf("expected nil IsPostSettlement for legacy JSON without field, got %v", *payload.IsPostSettlement)
		}
		if err := h.svc.HandlePaymentRefunded(context.Background(), "evt-rf-legacy", payload); err != nil {
			t.Fatalf("HandlePaymentRefunded(legacy): %v", err)
		}
		agg, _ := h.store.GetOrderWithDetails(context.Background(), ordID)
		if agg.Order.RefundedVND != 940000 || agg.Order.SettlementRefundedVND != 0 {
			t.Fatalf("legacy event missing is_post_settlement must keep settlement_refunded_vnd=0, got refunded=%d settlement_refunded=%d",
				agg.Order.RefundedVND, agg.Order.SettlementRefundedVND)
		}
		// Old post-settlement behavior: raw_net (929,400) - post (940,000) = -10,600; fees untouched (50,000 / 20,600).
		assertReports(t, h, -10600, 50000, 20600, 0)
	})
}

// TestW3A_4_AllCancelledGroupPreservesExpired verifies item 4:
// Closing an all-cancelled group (whether checkout_groups.payment_status or any sub-order payment_status
// is EXPIRED) MUST preserve EXPIRED and NEVER overwrite it to CANCELLED.
func TestW3A_4_AllCancelledGroupPreservesExpired(t *testing.T) {
	t.Run("ReconcileIncompleteGroup_GroupAlreadyExpired_NotOverwrittenToCancelled", func(t *testing.T) {
		h := newTestHarness()
		h.svc.cfg.StaleCheckoutAfter = 1 * time.Minute
		gid := "grp-w3a-exp-1"
		seedR6GroupAndOrders(t, h, gid, "IN_PROGRESS", "VIETQR", []store.NewOrderRecord{
			makeR6OrderRecord(gid, "ord-exp-1", "shop-1", "PENDING", "VIETQR", "PENDING", 500000),
		})
		// Simulate ScanExpiredPendingOrders / payment.expired cancelling the order and setting EXPIRED on group + order.
		if err := h.svc.HandlePaymentExpired(context.Background(), "evt-exp-w3a-1", events.PaymentFailedPayload{
			OrderGroupID: gid, Reason: "flash window expired",
		}); err != nil {
			t.Fatalf("HandlePaymentExpired: %v", err)
		}
		h.store.SetCheckoutGroupUpdatedAt(gid, time.Now().UTC().Add(-10*time.Minute))
		h.payment.onGetPayment = func(_ *paymentv1.GetPaymentRequest) (*paymentv1.GetPaymentResponse, error) {
			return nil, status.Error(codes.NotFound, "not found")
		}

		handled, err := h.svc.ScanIncompleteCheckoutGroups(context.Background())
		if err != nil || handled != 1 {
			t.Fatalf("ScanIncompleteCheckoutGroups = %d, %v; want 1, nil", handled, err)
		}
		grp, _ := h.store.GetCheckoutGroupByID(context.Background(), gid)
		if grp.Status != "FAILED" || grp.PaymentStatus != "EXPIRED" {
			t.Fatalf("item 4 violation: expired all-cancelled group became status=%q payment_status=%q, want FAILED/EXPIRED",
				grp.Status, grp.PaymentStatus)
		}
		ord, _ := h.store.GetOrderWithDetails(context.Background(), "ord-exp-1")
		if ord.Order.PaymentStatus != "EXPIRED" {
			t.Fatalf("item 4 violation: order payment_status=%q, want EXPIRED", ord.Order.PaymentStatus)
		}
	})

	t.Run("ReconcileIncompleteGroup_OrderExpiredWhileGroupPending_ClosesAsExpired", func(t *testing.T) {
		h := newTestHarness()
		h.svc.cfg.StaleCheckoutAfter = 1 * time.Minute
		gid := "grp-w3a-exp-2"
		seedR6GroupAndOrders(t, h, gid, "IN_PROGRESS", "VIETQR", []store.NewOrderRecord{
			makeR6OrderRecord(gid, "ord-exp-2a", "shop-1", "CANCELLED", "VIETQR", "CANCELLED", 300000),
			makeR6OrderRecord(gid, "ord-exp-2b", "shop-2", "CANCELLED", "VIETQR", "EXPIRED", 400000),
		})
		h.payment.onGetPayment = func(_ *paymentv1.GetPaymentRequest) (*paymentv1.GetPaymentResponse, error) {
			return nil, status.Error(codes.NotFound, "not found")
		}

		handled, err := h.svc.ScanIncompleteCheckoutGroups(context.Background())
		if err != nil || handled != 1 {
			t.Fatalf("ScanIncompleteCheckoutGroups = %d, %v; want 1, nil", handled, err)
		}
		grp, _ := h.store.GetCheckoutGroupByID(context.Background(), gid)
		if grp.Status != "FAILED" || grp.PaymentStatus != "EXPIRED" {
			t.Fatalf("item 4 violation: mixed CANCELLED+EXPIRED group closed with status=%q payment_status=%q, want FAILED/EXPIRED",
				grp.Status, grp.PaymentStatus)
		}
	})

	t.Run("FailCheckoutGroup_DoesNotOverwriteExistingExpired", func(t *testing.T) {
		h := newTestHarness()
		ctx := context.Background()
		gid := "grp-w3a-exp-3"
		seedR6GroupAndOrders(t, h, gid, "IN_PROGRESS", "VIETQR", []store.NewOrderRecord{
			makeR6OrderRecord(gid, "ord-exp-3", "shop-1", "PENDING", "VIETQR", "PENDING", 500000),
		})
		_ = h.svc.HandlePaymentExpired(ctx, "evt-exp-w3a-3", events.PaymentFailedPayload{OrderGroupID: gid, Reason: "expired"})
		// Direct FailCheckoutGroup(..., "CANCELLED") MUST NOT clobber EXPIRED!
		if err := h.store.FailCheckoutGroup(ctx, gid, "CANCELLED"); err != nil {
			t.Fatalf("FailCheckoutGroup: %v", err)
		}
		grp, _ := h.store.GetCheckoutGroupByID(ctx, gid)
		if grp.Status != "FAILED" || grp.PaymentStatus != "EXPIRED" {
			t.Fatalf("FailCheckoutGroup overwrote EXPIRED: status=%q payment_status=%q", grp.Status, grp.PaymentStatus)
		}
	})
}

// TestW3A_5_FailedCheckoutReturnsErrorWithReasonNot200 verifies D11:
//   - (a) Concurrent transition to FAILED before CompleteCheckoutWithPayment returns FailedPrecondition
//     with CHECKOUT_FAILED + reason code (NOT 200 OK), cancels payment P1, and skips Cart.ClearItems.
//   - (i) Pre-persist FAILED (0 orders, COD > limit) -> retry same Idempotency-Key with VIETQR -> 201 one set of orders.
//   - (ii) Pre-persist FAILED (0 orders, insufficient stock) -> retry same Idempotency-Key with fixed qty -> 201 one set of orders.
//   - (iii) Post-persist FAILED (orders already persisted, then all-cancelled/expired or payment failed) ->
//     retry same Idempotency-Key returns FailedPrecondition CHECKOUT_FAILED (<reason>), DB order count unchanged.
//   - (iv) 10 concurrent goroutines with same Idempotency-Key after 0-order FAILED (including independent
//     OrderService instances bypassing in-process idemLocks) -> strictly ONE set of orders created.
func TestW3A_5_FailedCheckoutReturnsErrorWithReasonNot200(t *testing.T) {
	t.Run("ConcurrentFailedBeforeCompleteCheckoutWithPayment_ReturnsErrorNot200AndSkipsClearCart", func(t *testing.T) {
		h := newTestHarness()
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		// Right after CreatePayment returns P1, simulate concurrent payment.expired arriving BEFORE
		// CompleteCheckoutWithPayment runs — with NO fake FailCheckoutGroup step!
		// Production CompleteCheckoutWithPayment itself MUST detect all orders CANCELLED and close FAILED (EXPIRED).
		h.payment.afterCreatePay = func(pay *paymentv1.Payment) {
			gid := pay.GetOrderGroupId()
			_ = h.svc.HandlePaymentExpired(context.Background(), "evt-race-exp", events.PaymentFailedPayload{
				OrderGroupID: gid, PaymentID: pay.GetPaymentId(), Reason: "expired mid-checkout",
			})
		}

		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w3a-failed-mid-checkout",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err == nil || resp != nil {
			t.Fatalf("P1-1 violation: CreateCheckout returned 200 OK (%+v) when all orders expired mid-checkout!", resp)
		}
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "CHECKOUT_FAILED") || !strings.Contains(err.Error(), "EXPIRED") {
			t.Fatalf("expected FailedPrecondition CHECKOUT_FAILED (EXPIRED), got %v", err)
		}
		if len(h.cart.clearCalls) != 0 {
			t.Fatalf("Cart.ClearItems must NOT run when checkout failed, got %d calls", len(h.cart.clearCalls))
		}
		// HandlePaymentExpired already attached P1 to the cancelled order (payment itself expired P1),
		// so CompleteCheckoutWithPayment closes FAILED without duplicating order.cancelled.
		cancelOutbox := map[string]struct{}{}
		for _, ev := range h.store.OutboxEvents() {
			if ev.EventType == events.SubjectOrderCancelled {
				cancelOutbox[ev.ID] = struct{}{}
			}
		}
		if len(cancelOutbox) != 1 {
			t.Fatalf("expected 1 order.cancelled outbox event from HandlePaymentExpired, got %d", len(cancelOutbox))
		}
		lookup, lErr := h.store.GetCheckoutGroupForIdempotency(context.Background(), "buyer-1", "w3a-failed-mid-checkout")
		if lErr != nil || !lookup.HasOrders || lookup.Group.Status != "FAILED" || lookup.Group.PaymentStatus != "EXPIRED" || len(lookup.Group.ResponseSnapshotJSON) != 0 || lookup.Group.PendingStockConfirm {
			t.Fatalf("P1-1 group state mismatch: lookup=%+v err=%v", lookup, lErr)
		}
		// Retry with same key MUST also return 409 CHECKOUT_FAILED (EXPIRED), never 200.
		if retryResp, retryErr := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w3a-failed-mid-checkout",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		}); retryErr == nil || retryResp != nil || status.Code(retryErr) != codes.FailedPrecondition || !strings.Contains(retryErr.Error(), "EXPIRED") {
			t.Fatalf("P1-1 retry violation: want FailedPrecondition CHECKOUT_FAILED (EXPIRED), got resp=%+v err=%v", retryResp, retryErr)
		}
	})

	t.Run("D11_i_CODLimitExceededThenRetryVIETQRSameKey_SucceedsOneOrderSet", func(t *testing.T) {
		h := newTestHarness()
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		idemKey := "w3a-d11-cod-to-vqr"

		// 25 x 1,000,000 = 25,000,000 VND > 20,000,000 COD limit -> pre-persist FAILED (0 orders).
		_, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: idemKey,
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 25}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "COD_LIMIT_EXCEEDED") {
			t.Fatalf("expected COD_LIMIT_EXCEEDED on first attempt, got %v", err)
		}

		// Switch to VIETQR using the EXACT SAME Idempotency-Key -> succeeds with ONE set of orders!
		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: idemKey,
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 25}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("D11(i) violation: retry with VIETQR on 0-order FAILED key failed: %v", err)
		}
		if resp.IdempotentReplay || len(resp.Orders) != 1 || resp.PaymentId == "" {
			t.Fatalf("unexpected fresh VIETQR checkout response: %+v", resp)
		}
		allOrders, total, _ := h.store.ListOrdersFiltered(context.Background(), "buyer-1", "", "", 20, 0)
		if total != 1 || len(allOrders) != 1 {
			t.Fatalf("expected strictly 1 persisted order, got total=%d", total)
		}
	})

	t.Run("D11_ii_StockInsufficientThenFixQtySameKey_SucceedsOneOrderSet", func(t *testing.T) {
		h := newTestHarness()
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		idemKey := "w3a-d11-stock-fix-qty"
		h.product.reserveErr = status.Error(codes.FailedPrecondition, "INSUFFICIENT_STOCK")

		_, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: idemKey,
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 10}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err == nil {
			t.Fatalf("expected stock failure on first attempt")
		}

		// Buyer lowers quantity and stock succeeds -> retry SAME Idempotency-Key creates ONE order set!
		h.product.reserveErr = nil
		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: idemKey,
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil {
			t.Fatalf("D11(ii) violation: retry with fixed quantity on 0-order FAILED key failed: %v", err)
		}
		if resp.IdempotentReplay || len(resp.Orders) != 1 {
			t.Fatalf("unexpected fresh checkout after stock failure: %+v", resp)
		}
		_, total, _ := h.store.ListOrdersFiltered(context.Background(), "buyer-1", "", "", 20, 0)
		if total != 1 {
			t.Fatalf("expected strictly 1 persisted order, got total=%d", total)
		}
	})

	t.Run("D11_iii_PersistedOrdersThenFailedOrExpired_RetrySameKeyReturns409AndOrderCountUnchanged", func(t *testing.T) {
		// Case 1: CreatePayment fails AFTER PersistCheckoutBatch -> group FAILED with cancelled order.
		h := newTestHarness()
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		h.payment.createErr = status.Error(codes.InvalidArgument, "payment rejected")

		req := &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w3a-d11-post-pay-fail",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		}
		if _, err := h.svc.CreateCheckout(buyerCtx, req); err == nil {
			t.Fatalf("expected first checkout to fail on CreatePayment")
		}
		_, countBefore, _ := h.store.ListOrdersFiltered(context.Background(), "buyer-1", "", "", 20, 0)
		if countBefore != 1 {
			t.Fatalf("expected 1 persisted (cancelled) order, got %d", countBefore)
		}

		// Retry with SAME Idempotency-Key (even after payment recovers & even with different payload) -> 409 CHECKOUT_FAILED!
		h.payment.createErr = nil
		stockCallsBefore := len(h.product.reserveCalls)
		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w3a-d11-post-pay-fail",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 2}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err == nil || resp != nil {
			t.Fatalf("D11(iii) violation: retry after post-persist FAILED returned 200 OK (%+v)!", resp)
		}
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "CHECKOUT_FAILED") || !strings.Contains(err.Error(), "FAILED") {
			t.Fatalf("expected FailedPrecondition CHECKOUT_FAILED (FAILED), got %v", err)
		}
		_, countAfter, _ := h.store.ListOrdersFiltered(context.Background(), "buyer-1", "", "", 20, 0)
		if countAfter != countBefore || len(h.product.reserveCalls) != stockCallsBefore {
			t.Fatalf("D11(iii) violation: DB order count changed (%d -> %d) or stock re-reserved", countBefore, countAfter)
		}

		// Case 2: Group IN_PROGRESS with persisted order -> all-cancelled/EXPIRED -> closed FAILED (EXPIRED) -> retry same key -> 409 CHECKOUT_FAILED (EXPIRED)!
		gidExp := "grp-d11-exp-persisted"
		seedR6GroupAndOrders(t, h, gidExp, "IN_PROGRESS", "VIETQR", []store.NewOrderRecord{
			makeR6OrderRecord(gidExp, "ord-d11-exp-1", "shop-1", "CANCELLED", "VIETQR", "EXPIRED", 500000),
		})
		if err := h.store.FailCheckoutGroup(context.Background(), gidExp, "EXPIRED"); err != nil {
			t.Fatalf("FailCheckoutGroup: %v", err)
		}
		_, countExpBefore, _ := h.store.ListOrdersFiltered(context.Background(), "buyer-1", "", "", 20, 0)
		_, err = h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "idem-" + gidExp,
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "CHECKOUT_FAILED") || !strings.Contains(err.Error(), "EXPIRED") {
			t.Fatalf("D11(iii) expired group retry: expected CHECKOUT_FAILED (EXPIRED), got %v", err)
		}
		_, countExpAfter, _ := h.store.ListOrdersFiltered(context.Background(), "buyer-1", "", "", 20, 0)
		if countExpAfter != countExpBefore {
			t.Fatalf("D11(iii) violation: expired FAILED group retry created extra orders (%d -> %d)", countExpBefore, countExpAfter)
		}
	})

	t.Run("D11_iv_10ConcurrentGoroutinesAfterZeroOrderFailed_StrictlyOneOrderSet", func(t *testing.T) {
		h := newTestHarness()
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		idemKey := "w3a-d11-race-10"

		// Step 1: First attempt fails pre-persist (COD > 20M, 0 orders).
		_, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: idemKey,
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 25}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("expected COD_LIMIT_EXCEEDED, got %v", err)
		}

		// Step 2: Launch 10 concurrent goroutines across 10 DISTINCT OrderService instances sharing the same store
		// (bypassing in-process idemLocks to prove store-level serialization under lock!).
		const workers = 10
		svcs := make([]*OrderService, workers)
		for i := 0; i < workers; i++ {
			svcs[i] = NewOrderService(h.svc.cfg, h.store, h.svc.clients, logger.New("order-race-test", logger.WithWriter(io.Discard)))
		}
		retryReq := &orderv1.CreateCheckoutRequest{
			IdempotencyKey: idemKey,
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		}

		start := make(chan struct{})
		type res struct {
			resp *orderv1.CreateCheckoutResponse
			err  error
		}
		results := make([]res, workers)
		var wg sync.WaitGroup
		wg.Add(workers)
		for i := 0; i < workers; i++ {
			go func(idx int) {
				defer wg.Done()
				<-start
				r, e := svcs[idx].CreateCheckout(buyerCtx, retryReq)
				results[idx] = res{resp: r, err: e}
			}(i)
		}
		close(start)
		wg.Wait()

		var canonGroupID, canonOrderID string
		for i, r := range results {
			if r.err != nil {
				t.Fatalf("goroutine[%d] failed: %v", i, r.err)
			}
			if r.resp == nil || len(r.resp.Orders) != 1 || r.resp.CheckoutGroupId == "" {
				t.Fatalf("goroutine[%d] invalid response: %+v", i, r.resp)
			}
			if canonGroupID == "" {
				canonGroupID = r.resp.CheckoutGroupId
				canonOrderID = r.resp.Orders[0].Id
			} else if r.resp.CheckoutGroupId != canonGroupID || r.resp.Orders[0].Id != canonOrderID {
				t.Fatalf("D11(iv) race violation: goroutine[%d] got (%s, %s), want (%s, %s)",
					i, r.resp.CheckoutGroupId, r.resp.Orders[0].Id, canonGroupID, canonOrderID)
			}
		}
		_, totalOrders, _ := h.store.ListOrdersFiltered(context.Background(), "buyer-1", "", "", 20, 0)
		if totalOrders != 1 {
			t.Fatalf("D11(iv) violation: expected strictly 1 persisted order across 10 concurrent retries, got %d", totalOrders)
		}
		if len(h.product.reserveCalls) != 1 {
			t.Fatalf("D11(iv) violation: expected ReserveStock called strictly once, got %d", len(h.product.reserveCalls))
		}
	})

	t.Run("P1_1_10b_SelfRecoveryExpiredPayment_Returns409Not200", func(t *testing.T) {
		h := newTestHarness()
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		req := &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w3a-unit-10b-rec-exp",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		}
		gid := "grp-unit-10b"
		_, _ = h.store.CreateCheckoutGroup(context.Background(), db.CreateCheckoutGroupParams{
			ID: gid, BuyerID: "buyer-1", IdempotencyKey: req.IdempotencyKey,
			RequestHash: computeCheckoutRequestHash("buyer-1", req),
			Status:      "IN_PROGRESS", PaymentMethod: "VIETQR", TotalPayableVND: 1030000,
		})
		_, _ = h.store.PersistCheckoutBatch(context.Background(), store.CheckoutBatch{
			CheckoutGroupID: gid,
			Orders:          []store.NewOrderRecord{makeR6OrderRecord(gid, "ord-unit-10b", "shop-1", "PENDING", "VIETQR", "PENDING", 1030000)},
		})
		h.store.SetCheckoutGroupUpdatedAt(gid, time.Now().UTC().Add(-10*time.Second))
		h.payment.onGetPayment = func(gReq *paymentv1.GetPaymentRequest) (*paymentv1.GetPaymentResponse, error) {
			if gReq.GetOrderGroupId() == gid {
				return &paymentv1.GetPaymentResponse{Payment: &paymentv1.Payment{
					PaymentId: "pay-unit-10b", OrderGroupId: gid, UserId: "buyer-1",
					AmountVnd: 1030000, Method: paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
					Status: paymentv1.PaymentStatus_PAYMENT_STATUS_EXPIRED,
				}}, nil
			}
			return nil, status.Error(codes.NotFound, "not found")
		}
		resp, err := h.svc.CreateCheckout(buyerCtx, req)
		if err == nil || resp != nil || status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "CHECKOUT_FAILED") || !strings.Contains(err.Error(), "EXPIRED") {
			t.Fatalf("P1-1 unit 10b violation: want FailedPrecondition CHECKOUT_FAILED (EXPIRED), got resp=%+v err=%v", resp, err)
		}
		grp, _ := h.store.GetCheckoutGroupByID(context.Background(), gid)
		if grp.Status != "FAILED" || grp.PaymentStatus != "EXPIRED" || len(grp.ResponseSnapshotJSON) != 0 {
			t.Fatalf("P1-1 unit 10b group state mismatch: %+v", grp)
		}
	})

	t.Run("P1_2_10c_NeedsCleanupWithDifferentPayload_CleansOrReturnsUnavailableNotConflict", func(t *testing.T) {
		h := newTestHarness()
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		idem := "w3a-unit-10c-nc-diff"
		h.promo.breakdown = &promotionv1.DiscountBreakdown{
			Shops: []*promotionv1.ShopDiscountBreakdown{
				{ShopId: "shop-1", SubtotalVnd: 25000000, PlatformVoucherDiscountVnd: 100000, ShippingFeeVnd: 30000},
			},
		}
		h.product.releaseErr = status.Error(codes.Unavailable, "product down")
		_, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: idem, AddressId: "addr-buyer-1",
			Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 25}},
			PlatformVoucherId: "v-plat", PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("expected COD_LIMIT_EXCEEDED, got %v", err)
		}
		// Retry with DIFFERENT payload while cleanup still fails -> MUST return Unavailable (NOT 409 IDEMPOTENCY_KEY_CONFLICT).
		diffReq := &orderv1.CreateCheckoutRequest{
			IdempotencyKey: idem, AddressId: "addr-buyer-1",
			Items:         []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		}
		if _, err := h.svc.CreateCheckout(buyerCtx, diffReq); status.Code(err) != codes.Unavailable {
			t.Fatalf("P1-2 unit violation: expected Unavailable while cleanup fails on different payload, got %v", err)
		}
		// Product recovers -> cleans up and creates fresh order!
		h.product.releaseErr = nil
		h.promo.breakdown = nil
		resp, err := h.svc.CreateCheckout(buyerCtx, diffReq)
		if err != nil || resp.IdempotentReplay || len(resp.Orders) != 1 {
			t.Fatalf("P1-2 unit violation: expected fresh checkout after cleanup recovery, got resp=%+v err=%v", resp, err)
		}
	})
}

// TestW3A_6_AcquireCheckoutGroupBackoffAndCtxCancellation verifies item 6:
//   - Progressive backoff drastically reduces GetPayment polling load compared to fixed 50ms.
//   - Default total wait remains 15s (checkoutAcquireMaxWait == 15s) and does not overshoot deadline.
//   - Context cancellation exits promptly without waiting out the current backoff sleep.
func TestW3A_6_AcquireCheckoutGroupBackoffAndCtxCancellation(t *testing.T) {
	if checkoutAcquireMaxWait != 15*time.Second {
		t.Fatalf("checkoutAcquireMaxWait = %v, want 15s", checkoutAcquireMaxWait)
	}

	t.Run("ProgressiveBackoffReducesGetPaymentCallsWithinMaxWait", func(t *testing.T) {
		h := newTestHarness()
		h.svc.acquireMaxWait = 700 * time.Millisecond
		h.svc.acquireInitialBackoff = 50 * time.Millisecond
		h.svc.acquireMaxBackoff = 1 * time.Second

		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		req := &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w3a-backoff-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		}
		reqHash := computeCheckoutRequestHash("buyer-1", req)

		gid := "grp-w3a-backoff-1"
		_, _ = h.store.CreateCheckoutGroup(context.Background(), db.CreateCheckoutGroupParams{
			ID: gid, BuyerID: "buyer-1", IdempotencyKey: req.IdempotencyKey, RequestHash: reqHash,
			Status: "IN_PROGRESS", PaymentMethod: "VIETQR", TotalPayableVND: 1030000,
		})
		_, _ = h.store.PersistCheckoutBatch(context.Background(), store.CheckoutBatch{
			CheckoutGroupID: gid,
			Orders:          []store.NewOrderRecord{makeR6OrderRecord(gid, "ord-w3a-bo-1", "shop-1", "PENDING", "VIETQR", "PENDING", 1030000)},
		})
		// Past 2s recovery grace so acquireCheckoutGroup probes GetPayment on each iteration.
		h.store.SetCheckoutGroupUpdatedAt(gid, time.Now().UTC().Add(-10*time.Second))

		var getPayCalls atomic.Int32
		h.payment.onGetPayment = func(_ *paymentv1.GetPaymentRequest) (*paymentv1.GetPaymentResponse, error) {
			getPayCalls.Add(1)
			return nil, status.Error(codes.NotFound, "still pending")
		}

		start := time.Now()
		_, err := h.svc.CreateCheckout(buyerCtx, req)
		elapsed := time.Since(start)

		if status.Code(err) != codes.Aborted {
			t.Fatalf("expected codes.Aborted after maxWait, got %v", err)
		}
		// With 50ms -> 100ms -> 200ms -> 350ms (clamped to remaining) = 5 calls total over 700ms.
		// Fixed 50ms polling would have made ~15 calls!
		calls := getPayCalls.Load()
		if calls < 3 || calls > 6 {
			t.Fatalf("item 6 violation: expected 3..6 progressive backoff GetPayment calls over 700ms (fixed 50ms would be ~15), got %d (elapsed=%v)", calls, elapsed)
		}
		if elapsed < 650*time.Millisecond || elapsed > 5*time.Second {
			t.Fatalf("expected wait >=650ms (maxWait=700ms) and <=5s, got %v", elapsed)
		}
	})

	t.Run("ContextCancelExitsImmediatelyDuringLongBackoff", func(t *testing.T) {
		h := newTestHarness()
		h.svc.acquireMaxWait = 15 * time.Second
		h.svc.acquireInitialBackoff = 500 * time.Millisecond
		h.svc.acquireMaxBackoff = 1 * time.Second

		baseCtx := ctxWithUser("buyer-1", "buyer", "")
		req := &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w3a-backoff-ctx",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		}
		reqHash := computeCheckoutRequestHash("buyer-1", req)
		gid := "grp-w3a-backoff-ctx"
		_, _ = h.store.CreateCheckoutGroup(context.Background(), db.CreateCheckoutGroupParams{
			ID: gid, BuyerID: "buyer-1", IdempotencyKey: req.IdempotencyKey, RequestHash: reqHash,
			Status: "IN_PROGRESS", PaymentMethod: "VIETQR", TotalPayableVND: 1030000,
		})
		_, _ = h.store.PersistCheckoutBatch(context.Background(), store.CheckoutBatch{
			CheckoutGroupID: gid,
			Orders:          []store.NewOrderRecord{makeR6OrderRecord(gid, "ord-w3a-bo-ctx", "shop-1", "PENDING", "VIETQR", "PENDING", 1030000)},
		})
		h.store.SetCheckoutGroupUpdatedAt(gid, time.Now().UTC().Add(-10*time.Second))
		h.payment.onGetPayment = func(_ *paymentv1.GetPaymentRequest) (*paymentv1.GetPaymentResponse, error) {
			return nil, status.Error(codes.NotFound, "not found")
		}

		ctx, cancel := context.WithTimeout(baseCtx, 80*time.Millisecond)
		defer cancel()
		start := time.Now()
		_, err := h.svc.CreateCheckout(ctx, req)
		elapsed := time.Since(start)

		if status.Code(err) != codes.DeadlineExceeded {
			t.Fatalf("expected codes.DeadlineExceeded on ctx timeout, got %v", err)
		}
		if elapsed > 5*time.Second {
			t.Fatalf("item 6 violation: did not stop promptly on ctx.Done(), elapsed=%v (maxWait was 15s)", elapsed)
		}
	})
}

// TestW3A_7_ResolveShopPickupAddressNeverFallsBackToBuyer verifies item 7:
//   - resolveShopPickupAddress NEVER substitutes the buyer's shipping address.
//   - Checkout fee calculation rejects when shop has no pickup address (aligned with services/shipping).
//   - SellerShipOrder on an existing order fails clearly without mutating/corrupting the order and succeeds once configured.
//   - ensureReturnShipment fails clearly without passing buyer's address as ToAddress and succeeds once configured.
func TestW3A_7_ResolveShopPickupAddressNeverFallsBackToBuyer(t *testing.T) {
	t.Run("CheckoutRejectsShopMissingPickupAddress_NeverPassesBuyerProvince", func(t *testing.T) {
		cases := []struct {
			name   string
			shopID string
			skuID  string
			setup  func(h *testHarness)
		}{
			{
				name:   "EmptyPickupAddressID",
				shopID: "shop-nopickup",
				skuID:  "sku-nopickup",
				setup: func(h *testHarness) {
					h.user.shops["shop-nopickup"] = &userv1.Shop{
						Id: "shop-nopickup", OwnerUserId: "seller-np", Name: "No Pickup Shop", ContactPhone: "0933333333", Status: userv1.ShopStatus_SHOP_STATUS_ACTIVE,
					}
					h.product.skus["sku-nopickup"] = &productv1.SKU{
						Id: "sku-nopickup", ProductId: "p-np", ShopId: "shop-nopickup", ProductName: "Item NP",
						Name: "Std", PriceVnd: 200000, Stock: 10, AvailableStock: 10, WeightGrams: 500, CommissionRateBps: 500, IsActive: true,
					}
				},
			},
			{
				name:   "PickupAddressNotFound",
				shopID: "shop-addr-gone",
				skuID:  "sku-addr-gone",
				setup: func(h *testHarness) {
					h.user.shops["shop-addr-gone"] = &userv1.Shop{
						Id: "shop-addr-gone", OwnerUserId: "seller-ag", Name: "Gone Addr Shop", PickupAddressId: "addr-deleted", Status: userv1.ShopStatus_SHOP_STATUS_ACTIVE,
					}
					h.product.skus["sku-addr-gone"] = &productv1.SKU{
						Id: "sku-addr-gone", ProductId: "p-ag", ShopId: "shop-addr-gone", ProductName: "Item AG",
						Name: "Std", PriceVnd: 200000, Stock: 10, AvailableStock: 10, WeightGrams: 500, CommissionRateBps: 500, IsActive: true,
					}
				},
			},
			{
				name:   "PickupAddressMissingProvinceCode",
				shopID: "shop-noprov",
				skuID:  "sku-noprov",
				setup: func(h *testHarness) {
					h.user.shops["shop-noprov"] = &userv1.Shop{
						Id: "shop-noprov", OwnerUserId: "seller-pr", Name: "No Prov Shop", PickupAddressId: "addr-noprov", Status: userv1.ShopStatus_SHOP_STATUS_ACTIVE,
					}
					h.user.addresses["addr-noprov"] = &userv1.Address{
						Id: "addr-noprov", UserId: "seller-pr", RecipientName: "WH", RecipientPhone: "0944444444",
						ProvinceCode: "", WardCode: "", StreetAddress: "Incomplete",
					}
					h.product.skus["sku-noprov"] = &productv1.SKU{
						Id: "sku-noprov", ProductId: "p-pr", ShopId: "shop-noprov", ProductName: "Item PR",
						Name: "Std", PriceVnd: 200000, Stock: 10, AvailableStock: 10, WeightGrams: 500, CommissionRateBps: 500, IsActive: true,
					}
				},
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				h := newTestHarness()
				tc.setup(h)
				_, err := h.svc.CreateCheckout(ctxWithUser("buyer-1", "buyer", ""), &orderv1.CreateCheckoutRequest{
					IdempotencyKey: "w3a-nopickup-" + tc.name,
					AddressId:      "addr-buyer-1",
					Items:          []*orderv1.CheckoutItemInput{{SkuId: tc.skuID, Quantity: 1}},
					PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
				})
				if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "SHOP_PICKUP_ADDRESS_REQUIRED") {
					t.Fatalf("expected FailedPrecondition SHOP_PICKUP_ADDRESS_REQUIRED, got %v", err)
				}
				if h.shipping.calculateFeeCall != 0 {
					t.Fatalf("item 7 violation: Shipping.CalculateFee called %d times with buyer fallback!", h.shipping.calculateFeeCall)
				}
			})
		}
	})

	t.Run("SellerShipOrderOnExistingOrder_MissingShopPickupFailsWithoutCorruptingOrder", func(t *testing.T) {
		h := newTestHarness()
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

		co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w3a-ship-nopickup",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		ordID := co.Orders[0].Id

		// Simulate shop losing its pickup address AFTER order was already placed.
		h.user.shops["shop-1"].PickupAddressId = ""

		_, err = h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{
			OrderId: ordID, ShopId: "shop-1",
		})
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "SHOP_PICKUP_ADDRESS_REQUIRED") {
			t.Fatalf("expected FailedPrecondition SHOP_PICKUP_ADDRESS_REQUIRED, got %v", err)
		}
		if len(h.shipping.createShipCalls) != 0 {
			t.Fatalf("item 7 violation: CreateShipment MUST NOT be called with buyer address! got %+v", h.shipping.createShipCalls)
		}

		// Existing placed order MUST NOT be corrupted or left with pending_shipment_cancel=true!
		agg, _ := h.store.GetOrderWithDetails(context.Background(), ordID)
		if agg.Order.Status != "CONFIRMED" || agg.Order.PendingShipmentCancel || agg.Order.TrackingCode != "" {
			t.Fatalf("existing order corrupted after pickup error: status=%q pendingShipCancel=%v tracking=%q",
				agg.Order.Status, agg.Order.PendingShipmentCancel, agg.Order.TrackingCode)
		}

		// Once seller restores pickup address, SellerShipOrder succeeds with shop's real address (province "01", NOT buyer's "79").
		h.user.shops["shop-1"].PickupAddressId = "addr-shop-1"
		shipResp, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{
			OrderId: ordID, ShopId: "shop-1",
		})
		if err != nil || shipResp.TrackingCode == "" {
			t.Fatalf("SellerShipOrder after restoring pickup address failed: resp=%+v err=%v", shipResp, err)
		}
		if len(h.shipping.createShipCalls) != 1 || h.shipping.createShipCalls[0].GetFromAddress().GetProvinceCode() != "01" {
			t.Fatalf("expected FromAddress.ProvinceCode=01 (shop Hanoi), got %+v", h.shipping.createShipCalls[0].GetFromAddress())
		}
	})

	t.Run("ReturnShipmentOnExistingOrder_MissingShopPickupFailsWithoutUsingBuyerAddressAsDestination", func(t *testing.T) {
		h := newTestHarness()
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

		co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w3a-ret-nopickup",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		ordID := co.Orders[0].Id
		shipResp, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID, ShopId: "shop-1"})
		if err != nil {
			t.Fatalf("SellerShipOrder: %v", err)
		}
		if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-w3a-ret-deliv", events.ShipmentUpdatedPayload{
			OrderID: ordID, TrackingCode: shipResp.TrackingCode, Status: "delivered",
		}); err != nil {
			t.Fatalf("HandleShipmentUpdated(delivered): %v", err)
		}
		retResp, err := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ordID, Reason: "damaged"})
		if err != nil {
			t.Fatalf("RequestReturn: %v", err)
		}

		// Clear shop pickup address before accepting return -> CreateReturnShipment MUST NOT receive buyer address as ToAddress!
		h.user.shops["shop-1"].PickupAddressId = ""
		accResp, err := h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: retResp.ReturnRequestId,
			Decision:        orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_RETURN,
		})
		if err != nil {
			t.Fatalf("RespondReturn(ACCEPT_RETURN): %v", err)
		}
		if accResp.GetReturnRequest().GetStatus() != orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT ||
			accResp.GetReturnRequest().GetReturnTrackingCode() != "" {
			t.Fatalf("expected AWAITING_SHIPMENT with empty tracking while pickup missing, got %+v", accResp.GetReturnRequest())
		}
		if len(h.shipping.createReturnShipCalls) != 0 {
			t.Fatalf("item 7 violation: CreateReturnShipment MUST NOT be called when shop pickup missing! got %+v", h.shipping.createReturnShipCalls)
		}

		// Restore shop pickup address -> watchdog ScanReturnPendingShipments succeeds with ToAddress = shop ("01")!
		h.user.shops["shop-1"].PickupAddressId = "addr-shop-1"
		n, err := h.svc.ScanReturnPendingShipments(context.Background())
		if err != nil || n != 1 {
			t.Fatalf("ScanReturnPendingShipments = %d, %v; want 1, nil", n, err)
		}
		if len(h.shipping.createReturnShipCalls) != 1 {
			t.Fatalf("expected 1 CreateReturnShipment call after restoring pickup, got %d", len(h.shipping.createReturnShipCalls))
		}
		call := h.shipping.createReturnShipCalls[0]
		if call.GetToAddress().GetProvinceCode() != "01" || call.GetFromAddress().GetProvinceCode() != "79" {
			t.Fatalf("expected return From=79(buyer) To=01(shop), got From=%+v To=%+v", call.GetFromAddress(), call.GetToAddress())
		}
	})
}

func openTempPostgresStoreForService(t *testing.T) (*store.PostgresStore, *pgxpool.Pool) {
	t.Helper()
	adminDSN := strings.TrimSpace(os.Getenv("TEST_ORDER_DB_URL"))
	if adminDSN == "" {
		t.Skip("TEST_ORDER_DB_URL not set; skipping real Postgres OrderService tests")
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
	tempDB := "order_svc_d11_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	if _, err := adminConn.Exec(ctx, "CREATE DATABASE "+tempDB); err != nil {
		_ = adminConn.Close(ctx)
		t.Fatalf("create temp db %s: %v", tempDB, err)
	}
	_ = adminConn.Close(ctx)

	testURL := *parsed
	testURL.Path = "/" + tempDB
	pool, err := pgxpool.New(ctx, testURL.String())
	if err != nil {
		dropTempServiceDB(adminDSN, tempDB)
		t.Fatalf("open pool %s: %v", tempDB, err)
	}
	t.Cleanup(func() {
		pool.Close()
		dropTempServiceDB(adminDSN, tempDB)
	})
	if err := db.ApplyMigrations(ctx, pool); err != nil {
		t.Fatalf("ApplyMigrations on %s: %v", tempDB, err)
	}
	return store.NewPostgresStore(pool), pool
}

func dropTempServiceDB(adminDSN, dbName string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close(ctx) }()
	_, _ = conn.Exec(ctx, "DROP DATABASE IF EXISTS "+dbName+" WITH (FORCE)")
}

// TestW3A_D11_PostgresCheckoutRetryAndConcurrency runs all 4 D11 checkout scenarios end-to-end
// through OrderService backed by real PostgreSQL (including 10 concurrent goroutines across
// 10 independent OrderService instances hitting pg_advisory_xact_lock in Postgres).
func TestW3A_D11_PostgresCheckoutRetryAndConcurrency(t *testing.T) {
	pgStore, pool := openTempPostgresStoreForService(t)

	newPGHarness := func() (*testHarness, *OrderService) {
		h := newTestHarness()
		svc := NewOrderService(h.svc.cfg, pgStore, h.svc.clients, logger.New("order-pg-d11", logger.WithWriter(io.Discard)))
		return h, svc
	}
	countOrders := func(t *testing.T) int {
		t.Helper()
		var c int
		if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM orders`).Scan(&c); err != nil {
			t.Fatalf("count orders: %v", err)
		}
		return c
	}

	buyerCtx := ctxWithUser("buyer-1", "buyer", "")

	// (i) COD exceeds 20M limit -> switch to VIETQR on same Idempotency-Key -> succeeds with 1 order.
	t.Run("i_CODLimitExceededThenVIETQRSameKey", func(t *testing.T) {
		_, svc := newPGHarness()
		idem := "pg-d11-cod-vqr"
		before := countOrders(t)
		_, err := svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: idem, AddressId: "addr-buyer-1",
			Items:         []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 25}},
			PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "COD_LIMIT_EXCEEDED") {
			t.Fatalf("expected COD_LIMIT_EXCEEDED, got %v", err)
		}
		resp, err := svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: idem, AddressId: "addr-buyer-1",
			Items:         []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 25}},
			PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil || resp.IdempotentReplay || len(resp.Orders) != 1 {
			t.Fatalf("D11(i) Postgres retry failed: resp=%+v err=%v", resp, err)
		}
		if after := countOrders(t); after != before+1 {
			t.Fatalf("expected strictly +1 order in Postgres, before=%d after=%d", before, after)
		}
	})

	// (ii) Stock reserve failure -> fix qty on same Idempotency-Key -> succeeds with 1 order.
	t.Run("ii_StockInsufficientThenFixQtySameKey", func(t *testing.T) {
		h, svc := newPGHarness()
		idem := "pg-d11-stock"
		before := countOrders(t)
		h.product.reserveErr = status.Error(codes.FailedPrecondition, "INSUFFICIENT_STOCK")
		if _, err := svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: idem, AddressId: "addr-buyer-1",
			Items:         []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 10}},
			PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		}); err == nil {
			t.Fatalf("expected insufficient stock failure")
		}
		h.product.reserveErr = nil
		resp, err := svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: idem, AddressId: "addr-buyer-1",
			Items:         []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil || resp.IdempotentReplay || len(resp.Orders) != 1 {
			t.Fatalf("D11(ii) Postgres retry failed: resp=%+v err=%v", resp, err)
		}
		if after := countOrders(t); after != before+1 {
			t.Fatalf("expected strictly +1 order in Postgres, before=%d after=%d", before, after)
		}
	})

	// (iii) Persisted orders then payment failure -> retry same Idempotency-Key -> 409 CHECKOUT_FAILED, DB count unchanged.
	t.Run("iii_PersistedOrdersThenFailed_RetrySameKeyReturns409AndCountUnchanged", func(t *testing.T) {
		h, svc := newPGHarness()
		idem := "pg-d11-persisted-fail"
		h.payment.createErr = status.Error(codes.InvalidArgument, "payment gateway rejected")
		if _, err := svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: idem, AddressId: "addr-buyer-1",
			Items:         []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		}); err == nil {
			t.Fatalf("expected CreatePayment error")
		}
		countBefore := countOrders(t)

		h.payment.createErr = nil
		resp, err := svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: idem, AddressId: "addr-buyer-1",
			Items:         []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err == nil || resp != nil {
			t.Fatalf("D11(iii) Postgres violation: retry returned OK %+v", resp)
		}
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "CHECKOUT_FAILED") {
			t.Fatalf("expected FailedPrecondition CHECKOUT_FAILED, got %v", err)
		}
		if countAfter := countOrders(t); countAfter != countBefore {
			t.Fatalf("D11(iii) Postgres violation: order count changed %d -> %d", countBefore, countAfter)
		}
	})

	// (iv) 10 concurrent goroutines across 10 independent OrderService instances hitting real Postgres
	//      right after a 0-order FAILED group -> strictly ONE checkout group and ONE persisted order!
	t.Run("iv_10ConcurrentGoroutinesRealPostgresAfterZeroOrderFailed", func(t *testing.T) {
		h, initSvc := newPGHarness()
		idem := "pg-d11-race-10"
		before := countOrders(t)
		_, err := initSvc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: idem, AddressId: "addr-buyer-1",
			Items:         []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 25}},
			PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("expected initial COD_LIMIT_EXCEEDED, got %v", err)
		}

		const workers = 10
		svcs := make([]*OrderService, workers)
		for i := 0; i < workers; i++ {
			svcs[i] = NewOrderService(h.svc.cfg, pgStore, h.svc.clients, logger.New("order-pg-race", logger.WithWriter(io.Discard)))
		}
		retryReq := &orderv1.CreateCheckoutRequest{
			IdempotencyKey: idem, AddressId: "addr-buyer-1",
			Items:         []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		}
		type out struct {
			resp *orderv1.CreateCheckoutResponse
			err  error
		}
		outs := make([]out, workers)
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(workers)
		for i := 0; i < workers; i++ {
			go func(idx int) {
				defer wg.Done()
				<-start
				r, e := svcs[idx].CreateCheckout(buyerCtx, retryReq)
				outs[idx] = out{resp: r, err: e}
			}(i)
		}
		close(start)
		wg.Wait()

		var canonGroup, canonOrder string
		for i, o := range outs {
			if o.err != nil {
				t.Fatalf("Postgres D11(iv) worker[%d] failed: %v", i, o.err)
			}
			if o.resp == nil || len(o.resp.Orders) != 1 || o.resp.CheckoutGroupId == "" {
				t.Fatalf("Postgres D11(iv) worker[%d] invalid resp: %+v", i, o.resp)
			}
			if canonGroup == "" {
				canonGroup = o.resp.CheckoutGroupId
				canonOrder = o.resp.Orders[0].Id
			} else if o.resp.CheckoutGroupId != canonGroup || o.resp.Orders[0].Id != canonOrder {
				t.Fatalf("Postgres D11(iv) race diverged[%d]: got (%s,%s) want (%s,%s)",
					i, o.resp.CheckoutGroupId, o.resp.Orders[0].Id, canonGroup, canonOrder)
			}
		}
		if after := countOrders(t); after != before+1 {
			t.Fatalf("Postgres D11(iv) violation: expected strictly +1 order, before=%d after=%d", before, after)
		}
		if len(h.product.reserveCalls) != 1 {
			t.Fatalf("Postgres D11(iv) violation: ReserveStock called %d times, want 1", len(h.product.reserveCalls))
		}
	})

	// 10(a) P1-1 Real Postgres: Racing payment.expired BEFORE CompleteCheckoutWithPayment with NO fake
	// FailCheckoutGroup step -> closes FAILED (EXPIRED), no snapshot, pending_stock_confirm=false,
	// returns 409 CHECKOUT_FAILED (EXPIRED), skips Cart.ClearItems, cancels P1, retry returns 409,
	// NO watchdog double-releases stock/promo, late payment.succeeded leaves CANCELLED untouched,
	// late payment.refunded records REFUNDED, and post-COMPLETED cancel still replays 200 OK.
	t.Run("10a_ConcurrentExpiredBeforeComplete_RealPostgresNoFakeStepAndWatchdogEvidence", func(t *testing.T) {
		h, svc := newPGHarness()
		idem := "pg-10a-race-exp"
		before := countOrders(t)
		var capturedGID, capturedPayID string

		h.payment.afterCreatePay = func(pay *paymentv1.Payment) {
			capturedGID = pay.GetOrderGroupId()
			capturedPayID = pay.GetPaymentId()
			// Production path ONLY: HandlePaymentExpired (NO fake FailCheckoutGroup call!).
			if err := svc.HandlePaymentExpired(context.Background(), "evt-pg-10a-exp", events.PaymentFailedPayload{
				OrderGroupID: capturedGID, PaymentID: capturedPayID, Reason: "expired mid-checkout",
			}); err != nil {
				t.Errorf("HandlePaymentExpired: %v", err)
			}
		}

		resp, err := svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: idem, AddressId: "addr-buyer-1",
			Items:         []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err == nil || resp != nil {
			t.Fatalf("P1-1 Postgres violation: CreateCheckout returned OK (%+v) when order expired mid-checkout", resp)
		}
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "CHECKOUT_FAILED") || !strings.Contains(err.Error(), "EXPIRED") {
			t.Fatalf("expected FailedPrecondition CHECKOUT_FAILED (EXPIRED), got %v", err)
		}
		if len(h.cart.clearCalls) != 0 {
			t.Fatalf("Cart.ClearItems must NOT run on FAILED checkout, got %d calls", len(h.cart.clearCalls))
		}
		// HandlePaymentExpired came from payment with PaymentID=P1 (already attached to the cancelled order),
		// so 1 order.cancelled event exists and no redundant re-emit is generated.
		var cancelOutboxCnt int
		if err := pool.QueryRow(context.Background(),
			`SELECT COUNT(*) FROM outbox_events WHERE event_type = $1 AND payload_json->>'order_id' IN (SELECT id FROM orders WHERE checkout_group_id = $2)`,
			events.SubjectOrderCancelled, capturedGID).Scan(&cancelOutboxCnt); err != nil {
			t.Fatalf("count order.cancelled outbox events: %v", err)
		}
		if cancelOutboxCnt != 1 {
			t.Fatalf("expected 1 order.cancelled outbox event from HandlePaymentExpired, got %d", cancelOutboxCnt)
		}

		// Verify DB state directly in Postgres: status=FAILED, payment_status=EXPIRED, snapshot IS NULL, pending_stock_confirm=false.
		var dbStatus, dbPayStatus, dbPayID string
		var dbPendStock bool
		var dbSnap []byte
		if err := pool.QueryRow(context.Background(),
			`SELECT status, payment_status, payment_id, pending_stock_confirm, response_snapshot_json FROM checkout_groups WHERE id = $1`,
			capturedGID).Scan(&dbStatus, &dbPayStatus, &dbPayID, &dbPendStock, &dbSnap); err != nil {
			t.Fatalf("query checkout_group: %v", err)
		}
		if dbStatus != "FAILED" || dbPayStatus != "EXPIRED" || dbPayID != capturedPayID || dbPendStock || len(dbSnap) != 0 {
			t.Fatalf("Postgres checkout_group mismatch: status=%q payStatus=%q payID=%q pendStock=%v snap=%q",
				dbStatus, dbPayStatus, dbPayID, dbPendStock, string(dbSnap))
		}

		// Retry with same key -> 409 CHECKOUT_FAILED (EXPIRED), order count unchanged.
		if rResp, rErr := svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: idem, AddressId: "addr-buyer-1",
			Items:         []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		}); rErr == nil || rResp != nil || status.Code(rErr) != codes.FailedPrecondition || !strings.Contains(rErr.Error(), "EXPIRED") {
			t.Fatalf("retry on 10a FAILED group: want CHECKOUT_FAILED (EXPIRED), got resp=%+v err=%v", rResp, rErr)
		}
		if after := countOrders(t); after != before+1 {
			t.Fatalf("expected strictly +1 order (no 2nd set on retry), before=%d after=%d", before, after)
		}

		// Watchdog evidence: NO watchdog touches this FAILED-with-orders group or releases stock/promo a second time!
		relStockBefore := len(h.product.releaseCalls)
		relPromoBefore := len(h.promo.releaseCalls)
		confStockBefore := len(h.product.confirmCalls)
		_, _ = pool.Exec(context.Background(), `UPDATE checkout_groups SET updated_at = NOW() - INTERVAL '1 hour' WHERE id = $1`, capturedGID)
		if n, wErr := svc.ScanStaleCheckoutGroups(context.Background()); wErr != nil || n != 0 {
			t.Fatalf("ScanStaleCheckoutGroups must ignore FAILED group with orders: n=%d err=%v", n, wErr)
		}
		if n, wErr := svc.ScanIncompleteCheckoutGroups(context.Background()); wErr != nil || n != 0 {
			t.Fatalf("ScanIncompleteCheckoutGroups must ignore FAILED group: n=%d err=%v", n, wErr)
		}
		if n, wErr := svc.ScanPendingStockConfirms(context.Background()); wErr != nil || n != 0 {
			t.Fatalf("ScanPendingStockConfirms must ignore FAILED group: n=%d err=%v", n, wErr)
		}
		if n, wErr := svc.ScanExpiredPendingOrders(context.Background()); wErr != nil || n != 0 {
			t.Fatalf("ScanExpiredPendingOrders must ignore already-cancelled orders: n=%d err=%v", n, wErr)
		}
		if len(h.product.releaseCalls) != relStockBefore || len(h.promo.releaseCalls) != relPromoBefore || len(h.product.confirmCalls) != confStockBefore {
			t.Fatalf("watchdog double-released or confirmed stock/promo on FAILED group with orders!")
		}

		// Late payment.succeeded for this FAILED group MUST NOT mutate CANCELLED orders or confirm/release stock.
		// Payment refund for P1 is driven automatically by the fresh order.cancelled outbox event emitted
		// inside CompleteCheckoutWithPayment (verified above), NOT by injecting a fake HandlePaymentRefunded!
		if err := svc.HandlePaymentSucceeded(context.Background(), "evt-pg-10a-late-succ", events.PaymentSucceededPayload{
			PaymentID: capturedPayID, OrderGroupID: capturedGID, AmountVND: 1030000,
		}); err != nil {
			t.Fatalf("late HandlePaymentSucceeded: %v", err)
		}
		if len(h.product.confirmCalls) != confStockBefore || len(h.product.releaseCalls) != relStockBefore {
			t.Fatalf("late payment.succeeded on FAILED group must not confirm/release stock")
		}
		aggs, _ := pgStore.ListOrdersByCheckoutGroup(context.Background(), capturedGID)
		if len(aggs) != 1 || aggs[0].Order.Status != "CANCELLED" {
			t.Fatalf("late payment.succeeded mutated cancelled order: %+v", aggs)
		}

		// Contrast check (D11): group that already reached COMPLETED and is cancelled AFTERWARD still replays 200 OK!
		idemDone := "pg-10a-completed-then-cancel"
		_, svcDone := newPGHarness()
		coDone, err := svcDone.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: idemDone, AddressId: "addr-buyer-1",
			Items:         []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil || len(coDone.Orders) != 1 {
			t.Fatalf("CreateCheckout(completed): %+v err=%v", coDone, err)
		}
		if _, err := svcDone.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: coDone.Orders[0].Id, Reason: "buyer changed mind"}); err != nil {
			t.Fatalf("CancelOrder after COMPLETED: %v", err)
		}
		replayDone, err := svcDone.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: idemDone, AddressId: "addr-buyer-1",
			Items:         []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil || !replayDone.IdempotentReplay || replayDone.CheckoutGroupId != coDone.CheckoutGroupId {
			t.Fatalf("post-COMPLETED cancel must still replay 200 OK, got resp=%+v err=%v", replayDone, err)
		}
		if len(replayDone.Orders) != 1 || replayDone.Orders[0].Status != orderv1.OrderStatus_ORDER_STATUS_CANCELLED || replayDone.PaymentStatus != "CANCELLED" {
			t.Fatalf("R7 replay must reflect live CANCELLED order & group payment_status, got status=%v payStatus=%q",
				replayDone.Orders[0].GetStatus(), replayDone.PaymentStatus)
		}
	})

	// 10(b) P1-1 Self-recovery path on Real Postgres: Pod 1 crashed after CreatePayment (IN_PROGRESS + persisted order),
	// payment EXPIRED at PaymentService -> buyer retry after >2s triggers tryFinalizeInProgressGroupFromPayment ->
	// finalizeRecoveredCheckoutGroup -> closes FAILED (EXPIRED) and returns 409 CHECKOUT_FAILED (EXPIRED) (NOT 200!).
	t.Run("10b_SelfRecoveryInProgressPaymentExpired_RealPostgres", func(t *testing.T) {
		h, svc := newPGHarness()
		idem := "pg-10b-recover-exp"
		gid := "grp-pg-10b-" + uuid.NewString()[:8]
		ordID := "ord-pg-10b-" + uuid.NewString()[:8]
		req := &orderv1.CreateCheckoutRequest{
			IdempotencyKey: idem, AddressId: "addr-buyer-1",
			Items:         []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		}
		reqHash := computeCheckoutRequestHash("buyer-1", req)

		_, err := pgStore.CreateCheckoutGroup(context.Background(), db.CreateCheckoutGroupParams{
			ID: gid, BuyerID: "buyer-1", IdempotencyKey: idem, RequestHash: reqHash,
			Status: "IN_PROGRESS", PaymentMethod: "VIETQR", TotalPayableVND: 1030000,
		})
		if err != nil {
			t.Fatalf("CreateCheckoutGroup: %v", err)
		}
		if _, err := pgStore.PersistCheckoutBatch(context.Background(), store.CheckoutBatch{
			CheckoutGroupID: gid,
			Orders: []store.NewOrderRecord{{
				Order: db.InsertOrderParams{
					ID: ordID, OrderCode: "NM-PG-10B-" + ordID[len(ordID)-4:], CheckoutGroupID: gid,
					BuyerID: "buyer-1", ShopID: "shop-1", Status: "PENDING", PaymentMethod: "VIETQR",
					PaymentStatus: "PENDING", SubtotalVND: 1000000, ShippingFeeVND: 30000, TotalPayableVND: 1030000,
					ShippingAddressJSON: []byte(`{"province_code":"79"}`),
				},
				Items: []db.InsertOrderItemParams{{
					ID: uuid.NewString(), OrderID: ordID, SKUID: "sku-1", ProductID: "prod-1",
					ProductName: "KB", SKUName: "Std", UnitPriceVND: 1000000, EffectiveUnitPriceVND: 1000000,
					Quantity: 1, LineTotalVND: 1000000,
				}},
			}},
		}); err != nil {
			t.Fatalf("PersistCheckoutBatch: %v", err)
		}
		// Age group past 2s inProgressRecoveryGrace in Postgres.
		_, _ = pool.Exec(context.Background(), `UPDATE checkout_groups SET updated_at = NOW() - INTERVAL '10 seconds' WHERE id = $1`, gid)

		// PaymentService reports the payment for gid already EXPIRED.
		h.payment.onGetPayment = func(gReq *paymentv1.GetPaymentRequest) (*paymentv1.GetPaymentResponse, error) {
			if gReq.GetOrderGroupId() == gid {
				return &paymentv1.GetPaymentResponse{
					Payment: &paymentv1.Payment{
						PaymentId: "pay-pg-10b-exp", OrderGroupId: gid, UserId: "buyer-1",
						AmountVnd: 1030000, Method: paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
						Status: paymentv1.PaymentStatus_PAYMENT_STATUS_EXPIRED,
					},
				}, nil
			}
			return nil, status.Error(codes.NotFound, "not found")
		}

		before := countOrders(t)
		resp, err := svc.CreateCheckout(buyerCtx, req)
		if err == nil || resp != nil {
			t.Fatalf("P1-1 (10b) Postgres violation: self-recovery of expired group returned 200 OK (%+v)!", resp)
		}
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "CHECKOUT_FAILED") || !strings.Contains(err.Error(), "EXPIRED") {
			t.Fatalf("P1-1 (10b) want FailedPrecondition CHECKOUT_FAILED (EXPIRED), got %v", err)
		}
		grpAfter, _ := pgStore.GetCheckoutGroupByID(context.Background(), gid)
		if grpAfter.Status != "FAILED" || grpAfter.PaymentStatus != "EXPIRED" || len(grpAfter.ResponseSnapshotJSON) != 0 || grpAfter.PendingStockConfirm {
			t.Fatalf("10b group state mismatch: %+v", grpAfter)
		}
		if after := countOrders(t); after != before {
			t.Fatalf("10b recovery must not create new orders: before=%d after=%d", before, after)
		}
	})

	// 10(c) P1-2 Real Postgres: Pre-persist failure leaves status=IN_PROGRESS + needs_cleanup=TRUE + 0 orders.
	// Retrying the SAME Idempotency-Key with a DIFFERENT payload (COD -> VIETQR) MUST NOT return
	// 409 IDEMPOTENCY_KEY_CONFLICT: returns Unavailable while cleanup fails, then cleans & succeeds 201!
	t.Run("10c_NeedsCleanupDifferentPayload_RealPostgres", func(t *testing.T) {
		h, svc := newPGHarness()
		idem := "pg-10c-nc-diff-payload"
		before := countOrders(t)

		// Attempt 1: COD with platform voucher where post-discount total STILL > 20M (25M - 100k > 20M)
		// -> ReserveStock + ReservePromotion run, then COD_LIMIT_EXCEEDED triggers compensateBeforePersistAndFail.
		// Inject ReleaseStock failure -> MarkCheckoutGroupNeedsCleanup leaves status='IN_PROGRESS', needs_cleanup=TRUE, 0 orders!
		h.promo.breakdown = &promotionv1.DiscountBreakdown{
			Shops: []*promotionv1.ShopDiscountBreakdown{
				{ShopId: "shop-1", SubtotalVnd: 25000000, PlatformVoucherDiscountVnd: 100000, ShippingFeeVnd: 30000},
			},
		}
		h.product.releaseErr = status.Error(codes.Unavailable, "product db blip")
		_, err := svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    idem,
			AddressId:         "addr-buyer-1",
			Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 25}},
			PlatformVoucherId: "v-plat",
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "COD_LIMIT_EXCEEDED") {
			t.Fatalf("expected COD_LIMIT_EXCEEDED on attempt 1, got %v", err)
		}

		// Confirm DB has status='IN_PROGRESS', needs_cleanup=TRUE, 0 orders.
		lookup, lErr := pgStore.GetCheckoutGroupForIdempotency(context.Background(), "buyer-1", idem)
		if lErr != nil || lookup.Group.Status != "IN_PROGRESS" || !lookup.Group.NeedsCleanup || lookup.HasOrders {
			t.Fatalf("expected IN_PROGRESS + needs_cleanup=true + 0 orders, got lookup=%+v err=%v", lookup, lErr)
		}

		// Attempt 2: DIFFERENT payload (switch COD -> VIETQR, drop voucher, qty=1) while ReleaseStock STILL fails
		// -> MUST return codes.Unavailable (NOT 409 IDEMPOTENCY_KEY_CONFLICT)!
		diffPayloadReq := &orderv1.CreateCheckoutRequest{
			IdempotencyKey: idem,
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		}
		_, err2 := svc.CreateCheckout(buyerCtx, diffPayloadReq)
		if status.Code(err2) != codes.Unavailable {
			t.Fatalf("P1-2 violation: retry with different payload while needs_cleanup=true & cleanup failing returned %v, want Unavailable (never IDEMPOTENCY_KEY_CONFLICT)", err2)
		}

		// Attempt 3: ReleaseStock recovers -> same different payload cleans up old group and succeeds with 1 order!
		h.product.releaseErr = nil
		h.promo.breakdown = nil
		resp3, err3 := svc.CreateCheckout(buyerCtx, diffPayloadReq)
		if err3 != nil || resp3.IdempotentReplay || len(resp3.Orders) != 1 || resp3.PaymentId == "" {
			t.Fatalf("P1-2 violation: retry with different payload after cleanup recovery failed: resp=%+v err=%v", resp3, err3)
		}
		if after := countOrders(t); after != before+1 {
			t.Fatalf("expected strictly +1 order in Postgres after cleanup+retry, before=%d after=%d", before, after)
		}
	})

	// Round 7 on Real Postgres: COMPLETED replay reloads live orders + group.PaymentStatus for ALL callers
	// (including buyer) while keeping payment/QR/warnings from snapshot and masking private fields by role.
	t.Run("R7_ReplayReloadsLiveOrdersAndMasksByRole_RealPostgres", func(t *testing.T) {
		h, svc := newPGHarness()
		verifyR7ReplayBehavior(t, svc, pgStore, h, "pg-r7-replay")
	})
}

func verifyR7ReplayBehavior(t *testing.T, svc *OrderService, st store.Store, h *testHarness, idemKey string) {
	t.Helper()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")

	// Inject a checkout warning so we also verify snapshot warnings survive live-order reload.
	h.promo.breakdown = &promotionv1.DiscountBreakdown{
		Warnings: []string{"voucher-warning-r7"},
		Shops:    []*promotionv1.ShopDiscountBreakdown{{ShopId: "shop-1", SubtotalVnd: 1000000, ShippingFeeVnd: 30000}},
	}
	req := &orderv1.CreateCheckoutRequest{
		IdempotencyKey:    idemKey,
		AddressId:         "addr-buyer-1",
		Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PlatformVoucherId: "v-r7",
		PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
	}

	// 1. Initial checkout -> COMPLETED with snapshot containing QR/PaymentId/Warnings.
	co, err := svc.CreateCheckout(buyerCtx, req)
	if err != nil || len(co.Orders) != 1 || co.Orders[0].Status != orderv1.OrderStatus_ORDER_STATUS_PENDING {
		t.Fatalf("initial CreateCheckout: resp=%+v err=%v", co, err)
	}
	origOrderID := co.Orders[0].Id

	// Flag manual review on DB order to verify role-based masking on replay.
	if err := st.MarkOrderOrphanShipmentManualReview(context.Background(), origOrderID, "r7-mask-check"); err != nil {
		t.Fatalf("MarkOrderOrphanShipmentManualReview: %v", err)
	}

	// 2. Before cancel -> buyer replay returns 200, idempotent_replay=true, PENDING order, snapshot QR/warnings intact,
	//    and private fields (commission/payment fees/needs_manual_review) masked to 0/false.
	repLive, err := svc.CreateCheckout(buyerCtx, req)
	if err != nil || !repLive.IdempotentReplay || len(repLive.Orders) != 1 {
		t.Fatalf("live buyer replay failed: resp=%+v err=%v", repLive, err)
	}
	if repLive.Orders[0].Status != orderv1.OrderStatus_ORDER_STATUS_PENDING || repLive.PaymentStatus != "PENDING" {
		t.Fatalf("live replay expected PENDING/PENDING, got status=%v payStatus=%q", repLive.Orders[0].Status, repLive.PaymentStatus)
	}
	if repLive.PaymentId != co.PaymentId || repLive.QrUrl != co.QrUrl || repLive.QrCode != co.QrCode ||
		len(repLive.Warnings) != 1 || repLive.Warnings[0] != "voucher-warning-r7" {
		t.Fatalf("live replay lost snapshot payment/QR/warnings: got pay=%q qr=%q/%q warn=%v",
			repLive.PaymentId, repLive.QrUrl, repLive.QrCode, repLive.Warnings)
	}
	if repLive.Orders[0].CommissionVnd != 0 || repLive.Orders[0].PaymentFeeVnd != 0 ||
		repLive.Orders[0].PaymentFeeBps != 0 || repLive.Orders[0].Items[0].CommissionBps != 0 ||
		repLive.Orders[0].NeedsManualReview {
		t.Fatalf("live buyer replay leaked private fields: %+v", repLive.Orders[0])
	}

	// 3. Cancel order after COMPLETED -> buyer replay MUST still return 200 + idempotent_replay=true,
	//    with live ORDER_STATUS_CANCELLED and group PaymentStatus="CANCELLED", while keeping snapshot QR/warnings!
	if _, err := svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{
		OrderId: origOrderID, Reason: "buyer cancelled post-completed",
	}); err != nil {
		t.Fatalf("CancelOrder: %v", err)
	}
	repCancelled, err := svc.CreateCheckout(buyerCtx, req)
	if err != nil || !repCancelled.IdempotentReplay || len(repCancelled.Orders) != 1 {
		t.Fatalf("cancelled buyer replay failed: resp=%+v err=%v", repCancelled, err)
	}
	if repCancelled.Orders[0].Status != orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
		t.Fatalf("R7 violation: buyer replay returned stale snapshot status %v, want ORDER_STATUS_CANCELLED", repCancelled.Orders[0].Status)
	}
	if repCancelled.PaymentStatus != "CANCELLED" {
		t.Fatalf("R7 violation: buyer replay payment_status=%q, want CANCELLED from group", repCancelled.PaymentStatus)
	}
	if repCancelled.PaymentId != co.PaymentId || repCancelled.QrUrl != co.QrUrl || repCancelled.QrCode != co.QrCode ||
		len(repCancelled.Warnings) != 1 || repCancelled.Warnings[0] != "voucher-warning-r7" {
		t.Fatalf("cancelled replay lost snapshot payment/QR/warnings: got pay=%q qr=%q/%q warn=%v",
			repCancelled.PaymentId, repCancelled.QrUrl, repCancelled.QrCode, repCancelled.Warnings)
	}
	if repCancelled.Orders[0].CommissionVnd != 0 || repCancelled.Orders[0].PaymentFeeVnd != 0 ||
		repCancelled.Orders[0].Items[0].CommissionBps != 0 || repCancelled.Orders[0].NeedsManualReview {
		t.Fatalf("cancelled buyer replay leaked private fields: %+v", repCancelled.Orders[0])
	}

	// 4. Role-based visibility check on the same replayed group (admin vs owning seller vs other seller).
	grp, gErr := st.GetCheckoutGroupByID(context.Background(), co.CheckoutGroupId)
	if gErr != nil {
		t.Fatalf("GetCheckoutGroupByID: %v", gErr)
	}
	adminRep, err := svc.buildReplayResponse(context.Background(), grp, callerIdentity{UserID: "admin-1", Role: "admin", Claims: &auth.Claims{Role: "admin"}})
	if err != nil || len(adminRep.Orders) != 1 || adminRep.Orders[0].Status != orderv1.OrderStatus_ORDER_STATUS_CANCELLED ||
		adminRep.Orders[0].CommissionVnd == 0 || !adminRep.Orders[0].NeedsManualReview {
		t.Fatalf("admin replay must see CANCELLED + fees + NeedsManualReview, got %+v err=%v", adminRep, err)
	}
	ownSellerRep, err := svc.buildReplayResponse(context.Background(), grp, callerIdentity{UserID: "seller-1", Role: "seller", ShopID: "shop-1"})
	if err != nil || len(ownSellerRep.Orders) != 1 || ownSellerRep.Orders[0].CommissionVnd == 0 || ownSellerRep.Orders[0].NeedsManualReview {
		t.Fatalf("owning seller replay must see fees but NOT NeedsManualReview, got %+v err=%v", ownSellerRep, err)
	}
	otherSellerRep, err := svc.buildReplayResponse(context.Background(), grp, callerIdentity{UserID: "seller-2", Role: "seller", ShopID: "shop-2"})
	if err != nil || len(otherSellerRep.Orders) != 1 || otherSellerRep.Orders[0].CommissionVnd != 0 || otherSellerRep.Orders[0].NeedsManualReview {
		t.Fatalf("other seller replay must have fees & NeedsManualReview masked, got %+v err=%v", otherSellerRep, err)
	}
}

func TestW3A_R7_ReplayReloadsLiveOrdersAndMasksByRole_Memory(t *testing.T) {
	h := newTestHarness()
	verifyR7ReplayBehavior(t, h.svc, h.store, h, "mem-r7-replay")
}

// TestW3A_R4_BuyNowDoesNotClearCart verifies that Buy Now checkout (from_cart=false) NEVER calls
// Cart.ClearItems even when the ordered SKU is also in the buyer's cart, whereas from_cart=true
// calls Cart.ClearItems once (and idempotent replay never calls Cart.ClearItems again).
func TestW3A_R4_BuyNowDoesNotClearCart(t *testing.T) {
	h := newTestHarness()
	h.cart.cart = &cartv1.Cart{
		UserId: "buyer-1",
		ShopGroups: []*cartv1.CartShopGroup{
			{
				ShopId: "shop-1",
				Items: []*cartv1.CartItem{
					{SkuId: "sku-1", Quantity: 2, Selected: true},
				},
			},
			{
				ShopId: "shop-2",
				Items: []*cartv1.CartItem{
					{SkuId: "sku-2", Quantity: 1, Selected: true},
				},
			},
		},
	}
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")

	// 1. Buy Now (from_cart=false) for sku-1 qty=1 while cart holds sku-1 qty=2 -> MUST NOT clear cart!
	buyNowReq := &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "r4-buynow-keep-cart",
		AddressId:      "addr-buyer-1",
		FromCart:       false,
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	}
	respBuyNow, err := h.svc.CreateCheckout(buyerCtx, buyNowReq)
	if err != nil || len(respBuyNow.Orders) != 1 {
		t.Fatalf("Buy Now CreateCheckout failed: resp=%+v err=%v", respBuyNow, err)
	}
	if len(h.cart.clearCalls) != 0 {
		t.Fatalf("Buy Now (from_cart=false) MUST NOT call Cart.ClearItems, got %v", h.cart.clearCalls)
	}

	// Idempotent replay of Buy Now also must not touch cart.
	replayBuyNow, err := h.svc.CreateCheckout(buyerCtx, buyNowReq)
	if err != nil || !replayBuyNow.IdempotentReplay {
		t.Fatalf("Buy Now replay failed: resp=%+v err=%v", replayBuyNow, err)
	}
	if len(h.cart.clearCalls) != 0 {
		t.Fatalf("Buy Now replay MUST NOT call Cart.ClearItems, got %v", h.cart.clearCalls)
	}

	// 2. Cart checkout (from_cart=true) -> MUST call Cart.ClearItems once with ordered SKUs.
	cartReq := &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "r4-fromcart-clears-cart",
		AddressId:      "addr-buyer-1",
		FromCart:       true,
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	}
	respCart, err := h.svc.CreateCheckout(buyerCtx, cartReq)
	if err != nil || len(respCart.Orders) != 2 {
		t.Fatalf("FromCart CreateCheckout failed: resp=%+v err=%v", respCart, err)
	}
	if len(h.cart.clearCalls) != 1 || len(h.cart.clearCalls[0]) != 2 || h.cart.clearCalls[0][0] != "sku-1" || h.cart.clearCalls[0][1] != "sku-2" {
		t.Fatalf("FromCart=true expected single Cart.ClearItems([sku-1 sku-2]), got %v", h.cart.clearCalls)
	}

	// P1-3: Idempotent COMPLETED replay of cart checkout invokes idempotent Cart.ClearItems with persisted order SKUs.
	replayCart, err := h.svc.CreateCheckout(buyerCtx, cartReq)
	if err != nil || !replayCart.IdempotentReplay {
		t.Fatalf("FromCart replay failed: resp=%+v err=%v", replayCart, err)
	}
	if len(h.cart.clearCalls) != 2 || len(h.cart.clearCalls[1]) != 2 || h.cart.clearCalls[1][0] != "sku-1" || h.cart.clearCalls[1][1] != "sku-2" {
		t.Fatalf("P1-3: FromCart COMPLETED replay expected idempotent Cart.ClearItems([sku-1 sku-2]), got %v", h.cart.clearCalls)
	}
}

// TestW3A_R6_P1_1_CompleteCheckoutFailureReturnsUnavailableAndResetsState verifies P1-1:
//   - When completeCheckoutGroupWithRetry fails all 3 attempts (including when attempt 1 invokes BuildSnapshot
//     then fails commit, and attempts 2-3 fail before BuildSnapshot), CreateCheckout returns codes.Unavailable
//     (NEVER 200 OK from persistedAggs) and NEVER calls Cart.ClearItems, even if the group concurrently FAILED.
func TestW3A_R6_P1_1_CompleteCheckoutFailureReturnsUnavailableAndResetsState(t *testing.T) {
	h := newTestHarness()
	h.cart.cart = &cartv1.Cart{
		UserId: "buyer-1",
		ShopGroups: []*cartv1.CartShopGroup{{
			ShopId: "shop-1",
			Items:  []*cartv1.CartItem{{SkuId: "sku-1", Quantity: 1, Selected: true}},
		}},
	}
	// Wrap store: attempt 1 invokes BuildSnapshot (simulating tx.Commit failure after snapshot build),
	// attempts 2 & 3 fail immediately before BuildSnapshot.
	var attempts atomic.Int32
	wrapped := &r6CommitFailStore{
		Store: h.store,
		onComplete: func(arg store.UpdateCheckoutPaymentParams) error {
			n := attempts.Add(1)
			if n == 1 && arg.BuildSnapshot != nil {
				// Simulate stale snapshot from a rolled-back attempt 1!
				_, _ = arg.BuildSnapshot(db.CheckoutGroup{ID: arg.CheckoutGroupID, Status: "COMPLETED", PaymentID: arg.PaymentID}, nil)
			}
			return status.Error(codes.Unavailable, "db commit timeout")
		},
	}
	h.svc.store = wrapped

	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	// Also simulate concurrent order expiry right after CreatePayment so DB group would be FAILED!
	h.payment.afterCreatePay = func(pay *paymentv1.Payment) {
		_ = h.store.FailCheckoutGroup(context.Background(), pay.GetOrderGroupId(), "EXPIRED")
	}

	resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "r6-p1-1-commit-fail",
		AddressId:      "addr-buyer-1",
		FromCart:       true,
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
	})
	if err == nil || resp != nil || status.Code(err) != codes.Unavailable {
		t.Fatalf("P1-1 violation: expected Unavailable (never 200 OK fallback), got resp=%+v err=%v", resp, err)
	}
	if got := attempts.Load(); got != 3 {
		t.Fatalf("expected 3 retry attempts on completeCheckoutGroupWithRetry, got %d", got)
	}
	if len(h.cart.clearCalls) != 0 {
		t.Fatalf("P1-1 violation: Cart.ClearItems must NOT run when finalization failed, got %v", h.cart.clearCalls)
	}
}

type r6CommitFailStore struct {
	store.Store
	onComplete func(arg store.UpdateCheckoutPaymentParams) error
}

func (s *r6CommitFailStore) CompleteCheckoutWithPayment(ctx context.Context, arg store.UpdateCheckoutPaymentParams) error {
	if s.onComplete != nil {
		return s.onComplete(arg)
	}
	return s.Store.CompleteCheckoutWithPayment(ctx, arg)
}

// TestW3A_R6_P1_3_ClearOrderedCartRetryAndRecoveryReplay verifies P1-3:
//   - Transient Cart.ClearItems errors are retried via RetryIdempotent(3) on the main path.
//   - Self-recovery replay (from_cart=true after initial CompleteCheckoutWithPayment failure) calls
//     Cart.ClearItems with persisted order SKUs once COMPLETED (200 OK).
//   - FAILED checkout and from_cart=false never call Cart.ClearItems.
func TestW3A_R6_P1_3_ClearOrderedCartRetryAndRecoveryReplay(t *testing.T) {
	t.Run("TransientClearItemsError_RetriesUpTo3AndSucceeds", func(t *testing.T) {
		h := newTestHarness()
		h.cart.cart = &cartv1.Cart{
			UserId: "buyer-1",
			ShopGroups: []*cartv1.CartShopGroup{{
				ShopId: "shop-1",
				Items:  []*cartv1.CartItem{{SkuId: "sku-1", Quantity: 1, Selected: true}},
			}},
		}
		var clearAttempts atomic.Int32
		h.cart.onClearItems = func(_ context.Context) error {
			if clearAttempts.Add(1) < 3 {
				return status.Error(codes.Unavailable, "cart redis blip")
			}
			return nil
		}

		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "r6-p1-3-retry-cart",
			AddressId:      "addr-buyer-1",
			FromCart:       true,
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil || len(resp.Orders) != 1 {
			t.Fatalf("CreateCheckout failed: resp=%+v err=%v", resp, err)
		}
		if got := clearAttempts.Load(); got != 3 {
			t.Fatalf("P1-3 violation: expected ClearItems retried 3 times via RetryIdempotent, got %d", got)
		}
	})

	t.Run("SelfRecoveryReplayFromCartTrue_ClearsPersistedOrderSKUs", func(t *testing.T) {
		h := newTestHarness()
		h.cart.cart = &cartv1.Cart{
			UserId: "buyer-1",
			ShopGroups: []*cartv1.CartShopGroup{{
				ShopId: "shop-1",
				Items:  []*cartv1.CartItem{{SkuId: "sku-1", Quantity: 2, Selected: true}},
			}},
		}
		wrapped := &failingCompleteStore{Store: h.store, failRemaining: 5, failErr: status.Error(codes.Unavailable, "db blip")}
		h.svc.store = wrapped

		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		req := &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "r6-p1-3-recover-cart",
			AddressId:      "addr-buyer-1",
			FromCart:       true,
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		}
		// Initial attempt fails finalization -> Unavailable, 0 ClearItems calls.
		_, err := h.svc.CreateCheckout(buyerCtx, req)
		if status.Code(err) != codes.Unavailable || len(h.cart.clearCalls) != 0 {
			t.Fatalf("attempt 1: want Unavailable & 0 clearCalls, got err=%v clearCalls=%v", err, h.cart.clearCalls)
		}

		// Recovery replay completes the group -> MUST call Cart.ClearItems with persisted SKU ["sku-1"]!
		lookup, _ := h.store.GetCheckoutGroupForIdempotency(context.Background(), "buyer-1", req.IdempotencyKey)
		h.store.SetCheckoutGroupUpdatedAt(lookup.Group.ID, time.Now().UTC().Add(-5*time.Second))
		wrapped.mu.Lock()
		wrapped.failRemaining = 0
		wrapped.mu.Unlock()

		resp2, err2 := h.svc.CreateCheckout(buyerCtx, req)
		if err2 != nil || !resp2.IdempotentReplay {
			t.Fatalf("recovery replay failed: resp=%+v err=%v", resp2, err2)
		}
		if len(h.cart.clearCalls) != 1 || len(h.cart.clearCalls[0]) != 1 || h.cart.clearCalls[0][0] != "sku-1" {
			t.Fatalf("P1-3 violation: recovery COMPLETED replay (from_cart=true) must clear [sku-1], got %v", h.cart.clearCalls)
		}
	})
}
