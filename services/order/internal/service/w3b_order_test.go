package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	cartv1 "github.com/nexusmart/core/gen/go/nexusmart/cart/v1"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/order/internal/config"
	"github.com/nexusmart/core/services/order/internal/db"
	"github.com/nexusmart/core/services/order/internal/events"
	"github.com/nexusmart/core/services/order/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestW3B_E4_FromCartUnavailableItem verifies E4:
//   - Selected unavailable item (mixed with available item OR alone) rejects with FailedPrecondition SKU_INACTIVE
//     BEFORE reserving stock or promotions (never silently drops the item to place the rest).
//   - Unselected unavailable item does NOT block checkout of remaining selected available items.
//   - Cart with only unselected unavailable items fails with the existing empty-cart error ("shopping cart is empty").
func TestW3B_E4_FromCartUnavailableItem(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")

	t.Run("MixedSelectedAvailableAndUnavailable_RejectsSKUInactiveBeforeReserve", func(t *testing.T) {
		h := newTestHarness()
		h.cart.cart = &cartv1.Cart{
			UserId: "buyer-1",
			ShopGroups: []*cartv1.CartShopGroup{
				{
					ShopId: "shop-1",
					Items: []*cartv1.CartItem{
						{SkuId: "sku-1", ShopId: "shop-1", Quantity: 1, PriceVnd: 1000000, Selected: true, Unavailable: false},
						{SkuId: "sku-hidden", ShopId: "shop-1", Quantity: 1, PriceVnd: 0, Selected: true, Unavailable: true, UnavailableReason: "SKU_INACTIVE"},
					},
				},
			},
		}

		_, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "w3b-e4-mixed-1",
			AddressId:         "addr-buyer-1",
			FromCart:          true,
			PlatformVoucherId: "v-plat-1",
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "SKU_INACTIVE") {
			t.Fatalf("E4 violation: got err=%v, want FailedPrecondition SKU_INACTIVE", err)
		}
		if len(h.product.reserveCalls) != 0 || len(h.promo.reserveCalls) != 0 {
			t.Fatalf("E4 violation: must reject BEFORE stock/promo reserve, got reserveStock=%d reservePromo=%d",
				len(h.product.reserveCalls), len(h.promo.reserveCalls))
		}
		if len(h.cart.clearCalls) != 0 {
			t.Fatalf("E4 violation: must not clear cart on failed checkout, got %v", h.cart.clearCalls)
		}
	})

	t.Run("OnlySelectedUnavailableSKUNotFound_RejectsSKUInactiveBeforeReserve", func(t *testing.T) {
		h := newTestHarness()
		h.cart.cart = &cartv1.Cart{
			UserId: "buyer-1",
			ShopGroups: []*cartv1.CartShopGroup{
				{
					ShopId: "shop-1",
					Items: []*cartv1.CartItem{
						{SkuId: "sku-deleted", ShopId: "shop-1", Quantity: 2, PriceVnd: 0, Selected: true, Unavailable: true, UnavailableReason: "SKU_NOT_FOUND"},
					},
				},
			},
		}

		_, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w3b-e4-notfound-1",
			AddressId:      "addr-buyer-1",
			FromCart:       true,
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "SKU_INACTIVE") {
			t.Fatalf("E4 violation: SKU_NOT_FOUND selected item must reject with FailedPrecondition SKU_INACTIVE, got %v", err)
		}
		if len(h.product.reserveCalls) != 0 || len(h.promo.reserveCalls) != 0 {
			t.Fatalf("E4 violation: must reject before reserve, got stock=%d promo=%d",
				len(h.product.reserveCalls), len(h.promo.reserveCalls))
		}
	})

	t.Run("UnselectedUnavailableWithSelectedAvailable_SucceedsForSelectedOnly", func(t *testing.T) {
		h := newTestHarness()
		h.cart.cart = &cartv1.Cart{
			UserId: "buyer-1",
			ShopGroups: []*cartv1.CartShopGroup{
				{
					ShopId: "shop-1",
					Items: []*cartv1.CartItem{
						{SkuId: "sku-1", ShopId: "shop-1", Quantity: 1, PriceVnd: 1000000, Selected: true, Unavailable: false},
						{SkuId: "sku-hidden", ShopId: "shop-1", Quantity: 1, PriceVnd: 0, Selected: false, Unavailable: true, UnavailableReason: "SKU_INACTIVE"},
					},
				},
			},
		}

		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w3b-e4-unselected-ok",
			AddressId:      "addr-buyer-1",
			FromCart:       true,
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("expected checkout to succeed for selected available SKU, got %v", err)
		}
		if len(resp.Orders) != 1 || len(resp.Orders[0].Items) != 1 || resp.Orders[0].Items[0].SkuId != "sku-1" {
			t.Fatalf("unexpected orders: %+v", resp.Orders)
		}
	})

	t.Run("OnlyUnselectedUnavailable_ReturnsExistingEmptyCartError", func(t *testing.T) {
		h := newTestHarness()
		h.cart.cart = &cartv1.Cart{
			UserId: "buyer-1",
			ShopGroups: []*cartv1.CartShopGroup{
				{
					ShopId: "shop-1",
					Items: []*cartv1.CartItem{
						{SkuId: "sku-hidden", ShopId: "shop-1", Quantity: 1, PriceVnd: 0, Selected: false, Unavailable: true, UnavailableReason: "SKU_INACTIVE"},
					},
				},
			},
		}

		_, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w3b-e4-empty-1",
			AddressId:      "addr-buyer-1",
			FromCart:       true,
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "shopping cart is empty") {
			t.Fatalf("E4 violation: expected existing empty-cart FailedPrecondition, got %v", err)
		}
	})
}

// seedCODHistoryOrder inserts a historical COD order directly into MemoryStore for E19 rate tests.
func seedCODHistoryOrder(t *testing.T, st *store.MemoryStore, buyerID, orderID, statusStr string) {
	t.Helper()
	gid := "grp-hist-" + orderID
	_, err := st.CreateCheckoutGroup(context.Background(), db.CreateCheckoutGroupParams{
		ID:              gid,
		BuyerID:         buyerID,
		IdempotencyKey:  "idem-hist-" + orderID,
		RequestHash:     "hash-" + orderID,
		Status:          "IN_PROGRESS",
		PaymentMethod:   "COD",
		TotalPayableVND: 100000,
	})
	if err != nil {
		t.Fatalf("seed CreateCheckoutGroup(%s): %v", orderID, err)
	}
	now := time.Now().UTC().Add(-24 * time.Hour)
	_, err = st.PersistCheckoutBatch(context.Background(), store.CheckoutBatch{
		CheckoutGroupID: gid,
		Now:             now,
		Orders: []store.NewOrderRecord{
			{
				Order: db.InsertOrderParams{
					ID:                  orderID,
					OrderCode:           "NM-HIST-" + orderID,
					CheckoutGroupID:     gid,
					BuyerID:             buyerID,
					ShopID:              "shop-1",
					Status:              "CONFIRMED",
					PaymentMethod:       "COD",
					PaymentStatus:       "PENDING",
					SubtotalVND:         100000,
					TotalPayableVND:     100000,
					ShippingAddressJSON: []byte(`{"province_code":"79","ward_code":"26734"}`),
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("seed PersistCheckoutBatch(%s): %v", orderID, err)
	}
	if err := st.CompleteCheckoutWithPayment(context.Background(), store.UpdateCheckoutPaymentParams{
		CheckoutGroupID: gid,
		PaymentID:       "pay-hist-" + orderID,
		PaymentStatus:   "PENDING",
	}); err != nil {
		t.Fatalf("seed CompleteCheckoutWithPayment(%s): %v", orderID, err)
	}
	switch statusStr {
	case "COMPLETED":
		if _, err := st.ApplyShipmentUpdate(context.Background(), store.ShipmentUpdateParams{
			OrderID:        orderID,
			ShipmentStatus: "delivered",
			TrackingCode:   "TRK-" + orderID,
			Now:            now,
		}); err != nil {
			t.Fatalf("seed delivered(%s): %v", orderID, err)
		}
		if _, err := st.TransitionOrderCompleted(context.Background(), store.CompleteOrderParams{
			OrderID:   orderID,
			ActorID:   buyerID,
			ActorRole: "buyer",
			Note:      "completed",
			Now:       now,
		}); err != nil {
			t.Fatalf("seed completed(%s): %v", orderID, err)
		}
	case "RETURNED_REFUSED":
		if _, err := st.ApplyShipmentUpdate(context.Background(), store.ShipmentUpdateParams{
			OrderID:        orderID,
			ShipmentStatus: "returned",
			TrackingCode:   "TRK-" + orderID,
			Now:            now,
		}); err != nil {
			t.Fatalf("seed returned(%s): %v", orderID, err)
		}
	case "RETURNED_AFTER_DELIVERY":
		// Buyer accepted delivery (delivered_at set), then legitimately returned defective goods.
		if _, err := st.ApplyShipmentUpdate(context.Background(), store.ShipmentUpdateParams{
			OrderID:        orderID,
			ShipmentStatus: "delivered",
			TrackingCode:   "TRK-" + orderID,
			Now:            now,
		}); err != nil {
			t.Fatalf("seed delivered(%s): %v", orderID, err)
		}
		rrID := "rr-" + orderID
		if _, _, err := st.TransitionOrderReturnRequested(context.Background(), store.ReturnRequestParams{
			OrderID:         orderID,
			BuyerID:         buyerID,
			ShopID:          "shop-1",
			ReturnRequestID: rrID,
			Reason:          "defective item",
			RefundAmountVND: 100000,
			ActorID:         buyerID,
			ActorRole:       "buyer",
			Note:            "return requested",
			Now:             now,
		}); err != nil {
			t.Fatalf("seed return requested(%s): %v", orderID, err)
		}
		if _, err := st.TransitionReturnRequest(context.Background(), store.ReturnTransitionParams{
			ReturnRequestID: rrID,
			ExpectedStatus:  orderv1.ReturnStatus_RETURN_STATUS_PENDING,
			TargetStatus:    orderv1.ReturnStatus_RETURN_STATUS_REFUNDING,
			ActorID:         "seller-1",
			ActorRole:       "seller",
			Note:            "accept refund",
			Now:             now,
		}); err != nil {
			t.Fatalf("seed return refunding(%s): %v", orderID, err)
		}
	case "CANCELLED":
		if _, err := st.TransitionOrderStatus(context.Background(), store.SingleOrderTransitionParams{
			OrderID:      orderID,
			TargetStatus: orderv1.OrderStatus_ORDER_STATUS_CANCELLED,
			CancelReason: "buyer cancelled",
			ActorID:      buyerID,
			ActorRole:    "buyer",
			Note:         "cancel",
			Now:          now,
		}); err != nil {
			t.Fatalf("seed cancelled(%s): %v", orderID, err)
		}
	case "CONFIRMED":
		// Already CONFIRMED (non-terminal).
	}
}

// TestW3B_E19_CODRiskChecks verifies E19 across CreateCheckout, D11 pre-persist retry switching to COD,
// RetryPayment COD rejection, and fail-closed behavior on transient user/shipping errors.
func TestW3B_E19_CODRiskChecks(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")

	t.Run("PhoneNotVerified_RejectsCODBeforeReserve_AllowsVietQR", func(t *testing.T) {
		h := newTestHarness()
		h.user.phoneVerified = false

		_, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "w3b-e19-phone-cod",
			AddressId:         "addr-buyer-1",
			Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PlatformVoucherId: "v-plat",
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "COD_PHONE_NOT_VERIFIED") {
			t.Fatalf("E19 violation: got err=%v, want FailedPrecondition COD_PHONE_NOT_VERIFIED", err)
		}
		if len(h.product.reserveCalls) != 0 || len(h.promo.reserveCalls) != 0 {
			t.Fatalf("E19 violation: must NOT reserve stock/promo on COD_PHONE_NOT_VERIFIED, got stock=%d promo=%d",
				len(h.product.reserveCalls), len(h.promo.reserveCalls))
		}

		// Same buyer with unverified phone CAN still place VietQR.
		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w3b-e19-phone-vietqr",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil || len(resp.Orders) != 1 {
			t.Fatalf("unverified phone must still allow VIETQR checkout, got resp=%+v err=%v", resp, err)
		}

		// After verifying phone, buyer can retry the SAME idempotency key (D11 pre-persist FAILED) with COD!
		h.user.phoneVerified = true
		resp2, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w3b-e19-phone-cod",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil || len(resp2.Orders) != 1 {
			t.Fatalf("D11+E19: retry same key after verifying phone should succeed, got resp=%+v err=%v", resp2, err)
		}
	})

	t.Run("AreaNotSupported_RejectsCODBeforeReserve_AllowsVietQR", func(t *testing.T) {
		h := newTestHarness()
		h.shipping.quotes = []*shippingv1.CarrierQuote{
			{CarrierCode: "ghn", CarrierName: "GHN", ServiceCode: "standard", FeeVnd: 45000, EstimatedDays: 5, CodSupported: false},
		}

		_, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "w3b-e19-area-cod",
			AddressId:         "addr-buyer-1",
			Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PlatformVoucherId: "v-plat",
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "COD_AREA_NOT_SUPPORTED") {
			t.Fatalf("E19 violation: got err=%v, want FailedPrecondition COD_AREA_NOT_SUPPORTED", err)
		}
		if len(h.product.reserveCalls) != 0 || len(h.promo.reserveCalls) != 0 {
			t.Fatalf("E19 violation: must NOT reserve stock/promo on COD_AREA_NOT_SUPPORTED, got stock=%d promo=%d",
				len(h.product.reserveCalls), len(h.promo.reserveCalls))
		}

		// VietQR to the same island ward MUST succeed.
		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w3b-e19-area-vietqr",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil || len(resp.Orders) != 1 {
			t.Fatalf("VIETQR to non-COD area should succeed, got resp=%+v err=%v", resp, err)
		}
	})

	t.Run("ExplicitCarrierWithoutCOD_Rejected_AutoPickPrefersCODSupported", func(t *testing.T) {
		h := newTestHarness()
		h.shipping.quotes = []*shippingv1.CarrierQuote{
			{CarrierCode: "cheap_nocod", ServiceCode: "std", FeeVnd: 20000, CodSupported: false},
			{CarrierCode: "ghn", ServiceCode: "standard", FeeVnd: 30000, CodSupported: true},
		}

		// Explicit choice of carrier without COD -> rejected with COD_AREA_NOT_SUPPORTED.
		_, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey:  "w3b-e19-explicit-nocod",
			AddressId:       "addr-buyer-1",
			Items:           []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:   orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
			ShippingChoices: []*orderv1.ShopShippingChoice{{ShopId: "shop-1", CarrierCode: "cheap_nocod", ServiceCode: "std"}},
		})
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "COD_AREA_NOT_SUPPORTED") {
			t.Fatalf("expected COD_AREA_NOT_SUPPORTED for explicit non-COD carrier, got %v", err)
		}

		// No explicit choice -> picks cheapest COD-supported quote (ghn) and succeeds!
		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w3b-e19-autopick-cod",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil || len(resp.Orders) != 1 || resp.Orders[0].CarrierCode != "ghn" {
			t.Fatalf("expected auto-pick ghn COD-supported quote, got resp=%+v err=%v", resp, err)
		}
	})

	t.Run("RejectRate_BoundaryAndNonTerminalExclusion", func(t *testing.T) {
		// 1 refused + 1 completed = 2 terminal (< 3) + 2 CONFIRMED non-terminal -> ALLOWED!
		h := newTestHarness()
		h.user.codRejectedCount = 1
		seedCODHistoryOrder(t, h.store, "buyer-1", "h1", "RETURNED_REFUSED")
		seedCODHistoryOrder(t, h.store, "buyer-1", "h2", "COMPLETED")
		seedCODHistoryOrder(t, h.store, "buyer-1", "h3", "CONFIRMED")
		seedCODHistoryOrder(t, h.store, "buyer-1", "h4", "CONFIRMED")

		if _, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w3b-e19-rate-below-min",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		}); err != nil {
			t.Fatalf("total_terminal=2 (<3) must be allowed even with non-terminal orders, got %v", err)
		}

		// Add 1 more COMPLETED -> now 2 COMPLETED + 1 RETURNED_REFUSED = 3 terminal, 1/3 = 33.33% >= 30% -> BLOCKED!
		// Even adding 5 CANCELLED orders MUST NOT dilute the rate!
		seedCODHistoryOrder(t, h.store, "buyer-1", "h5", "COMPLETED")
		for i := 0; i < 5; i++ {
			seedCODHistoryOrder(t, h.store, "buyer-1", "h-cancel-"+string(rune('a'+i)), "CANCELLED")
		}
		beforeReserve := len(h.product.reserveCalls)
		_, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "w3b-e19-rate-blocked-33pct",
			AddressId:         "addr-buyer-1",
			Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PlatformVoucherId: "v-plat",
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "COD_REJECTED_LIMIT_EXCEEDED") {
			t.Fatalf("E19 violation: 1/3 (33.3%%) must reject with COD_REJECTED_LIMIT_EXCEEDED, got %v", err)
		}
		if len(h.product.reserveCalls) != beforeReserve {
			t.Fatalf("E19 violation: must reject BEFORE ReserveStock, reserve calls grew from %d to %d", beforeReserve, len(h.product.reserveCalls))
		}

		// Add 1 more COMPLETED -> now 3 COMPLETED + 1 RETURNED_REFUSED = 4 terminal, 1/4 = 25% < 30% -> ALLOWED!
		seedCODHistoryOrder(t, h.store, "buyer-1", "h6", "COMPLETED")
		if _, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w3b-e19-rate-ok-25pct",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		}); err != nil {
			t.Fatalf("1/4 (25%%) must be allowed, got %v", err)
		}

		// Post-delivery legitimate return (RETURN_REQUESTED -> RETURNED with delivered_at set) MUST NOT count as bom hang.
		h2 := newTestHarness()
		seedCODHistoryOrder(t, h2.store, "buyer-1", "d1", "COMPLETED")
		seedCODHistoryOrder(t, h2.store, "buyer-1", "d2", "COMPLETED")
		seedCODHistoryOrder(t, h2.store, "buyer-1", "d3", "RETURNED_AFTER_DELIVERY")
		if _, err := h2.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w3b-e19-post-delivery-return-ok",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		}); err != nil {
			t.Fatalf("post-delivery defect return should NOT count as COD rejection, got %v", err)
		}
	})

	t.Run("TransientUserOrShippingError_FailsClosedRetryable_NeverBypassesCODCheck", func(t *testing.T) {
		h := newTestHarness()
		h.user.validateUserErr = status.Error(codes.Unavailable, "user service down")
		_, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w3b-e19-user-unavail",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if status.Code(err) != codes.Unavailable {
			t.Fatalf("expected Unavailable when user service fails transiently, got %v", err)
		}
		if len(h.product.reserveCalls) != 0 {
			t.Fatalf("must not reserve stock on user error")
		}

		h2 := newTestHarness()
		h2.shipping.calculateFeeErr = errors.New("shipping transport timeout")
		_, err = h2.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w3b-e19-ship-unavail",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if status.Code(err) != codes.Unavailable && status.Code(err) != codes.DeadlineExceeded {
			t.Fatalf("expected retryable Unavailable/DeadlineExceeded when shipping fails transiently, got code=%v err=%v", status.Code(err), err)
		}
		if len(h2.product.reserveCalls) != 0 {
			t.Fatalf("must not reserve stock on shipping error")
		}
	})

	t.Run("E17_EmptyWardOrEstimatedQuote_RejectedAtCheckout", func(t *testing.T) {
		h := newTestHarness()
		h.user.addresses["addr-no-ward"] = &userv1.Address{
			Id:             "addr-no-ward",
			UserId:         "buyer-1",
			RecipientName:  "Nguyen Van A",
			RecipientPhone: "0901234567",
			ProvinceCode:   "48",
			WardCode:       "",
			StreetAddress:  "Da Nang",
		}
		_, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w3b-e17-noward",
			AddressId:      "addr-no-ward",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if status.Code(err) != codes.InvalidArgument && status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("E17: checkout without ward_code must be rejected, got %v", err)
		}
		if len(h.product.reserveCalls) != 0 {
			t.Fatalf("must not reserve stock when address lacks ward_code")
		}

		h2 := newTestHarness()
		h2.shipping.calculateFeeIsEstimated = true
		_, err = h2.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w3b-e17-estimated",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("E17: estimated representative-ward quote must be rejected at checkout, got %v", err)
		}
	})

	t.Run("AllPathsSwitchingToCOD_D11RetryAndRetryPayment", func(t *testing.T) {
		h := newTestHarness()
		h.user.phoneVerified = false
		h.product.reserveErr = status.Error(codes.FailedPrecondition, "INSUFFICIENT_STOCK")

		// Attempt 1: VIETQR fails pre-persist (0 orders, group FAILED).
		_, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w3b-e19-switch-key",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err == nil {
			t.Fatalf("expected initial VIETQR attempt to fail pre-persist")
		}

		// Attempt 2: D11 retry with SAME key switching to COD -> MUST run COD eligibility check!
		h.product.reserveErr = nil
		beforeReserve := len(h.product.reserveCalls)
		_, err = h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w3b-e19-switch-key",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "COD_PHONE_NOT_VERIFIED") {
			t.Fatalf("D11 retry switching to COD must enforce COD_PHONE_NOT_VERIFIED, got %v", err)
		}
		if len(h.product.reserveCalls) != beforeReserve {
			t.Fatalf("D11 retry switching to COD must reject before ReserveStock")
		}

		// RetryPayment attempting to switch a live PENDING VietQR order to COD MUST remain rejected.
		vietqrResp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w3b-e19-vietqr-for-retry",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("VIETQR checkout failed: %v", err)
		}
		_, err = h.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
			PaymentId:     vietqrResp.PaymentId,
			PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("RetryPayment switching to COD must be rejected with FailedPrecondition, got %v", err)
		}
	})
}

// TestW3B_E18_AsyncShipmentQueueAndEvents verifies E18:
//   - SellerShipOrder receiving queued=true persists shipment_job_status="QUEUED" without advancing order status.
//   - shipment.updated status="created" idempotently attaches tracking/label/carrier/service, clears shipment_job_status, moves CONFIRMED->PREPARING.
//   - Buyer can cancel while shipment_job_status="QUEUED"; late shipment.updated status="created" on CANCELLED order triggers compensating CancelShipment.
//   - shipment.create_failed sets shipment_job_status="FAILED" (only when tracking_code==""), visible on GetOrder, and seller can retry SellerShipOrder.
func TestW3B_E18_AsyncShipmentQueueAndEvents(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

	t.Run("QueuedThenCreatedEvent_IdempotentAttachAndClearJobStatus", func(t *testing.T) {
		h := newTestHarness()
		co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w3b-e18-q1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		ordID := co.Orders[0].Id

		h.shipping.onCreateShipment = func(_ *shippingv1.CreateShipmentRequest) (*shippingv1.CreateShipmentResponse, error) {
			return &shippingv1.CreateShipmentResponse{
				Queued: true,
				JobId:  "job-101",
			}, nil
		}

		shipResp, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{
			OrderId: ordID,
			ShopId:  "shop-1",
		})
		if err != nil {
			t.Fatalf("SellerShipOrder queued: %v", err)
		}
		if !shipResp.GetQueued() || shipResp.GetShipmentJobId() != "job-101" {
			t.Fatalf("E18: expected queued=true job_id=job-101, got queued=%v job_id=%q", shipResp.GetQueued(), shipResp.GetShipmentJobId())
		}
		if shipResp.GetOrder().GetStatus() != orderv1.OrderStatus_ORDER_STATUS_CONFIRMED {
			t.Fatalf("E18: order status MUST NOT advance on queued, got %v", shipResp.GetOrder().GetStatus())
		}
		if shipResp.GetOrder().GetShipmentJobStatus() != "QUEUED" {
			t.Fatalf("E18: shipment_job_status = %q, want QUEUED", shipResp.GetOrder().GetShipmentJobStatus())
		}
		if shipResp.GetTrackingCode() != "" || shipResp.GetOrder().GetTrackingCode() != "" {
			t.Fatalf("E18: tracking_code must stay empty while queued")
		}
		for _, tl := range shipResp.GetOrder().GetTimeline() {
			if strings.Contains(tl.GetNote(), "job-101") {
				t.Fatalf("E18: timeline note must not leak internal job_id, got %q", tl.GetNote())
			}
		}

		// Deliver shipment.updated status="created" via NATS consumer pipeline.
		c := events.NewConsumer(nil, nil, h.store, h.svc, logger.New("order-test", logger.WithWriter(io.Discard)), "NEXUSMART")
		createdEnv, _ := json.Marshal(events.Envelope{
			EventID:   "evt-ship-created-1",
			EventType: events.SubjectShipmentUpdated,
			Payload: json.RawMessage(`{
				"shipment_id":"shp-async-1",
				"order_id":"` + ordID + `",
				"shop_id":"shop-1",
				"carrier_code":"ghn",
				"service_code":"standard",
				"tracking_code":"GHN-ASYNC-001",
				"label_url":"https://labels.sandbox/async-1",
				"status":"created",
				"direction":"FORWARD"
			}`),
		})
		act, err := c.ProcessRawMessage(context.Background(), events.SubjectShipmentUpdated, createdEnv, nil, 1)
		if err != nil || act != events.ActionAck {
			t.Fatalf("ProcessRawMessage(shipment.updated created) = %v, %v", act, err)
		}

		got, err := h.svc.GetOrder(sellerCtx, &orderv1.GetOrderRequest{OrderId: ordID})
		if err != nil {
			t.Fatalf("GetOrder: %v", err)
		}
		if got.Order.Status != orderv1.OrderStatus_ORDER_STATUS_PREPARING ||
			got.Order.TrackingCode != "GHN-ASYNC-001" ||
			got.Order.LabelUrl != "https://labels.sandbox/async-1" ||
			got.Order.ShipmentJobStatus != "" {
			t.Fatalf("E18: unexpected order after created event: status=%v trk=%q label=%q job=%q",
				got.Order.Status, got.Order.TrackingCode, got.Order.LabelUrl, got.Order.ShipmentJobStatus)
		}
		tlCountAfterFirst := len(got.Order.Timeline)

		// Replay with a DIFFERENT event_id must remain idempotent (no duplicate timeline, no status regression).
		createdEnv2, _ := json.Marshal(events.Envelope{
			EventID:   "evt-ship-created-2",
			EventType: events.SubjectShipmentUpdated,
			Payload: json.RawMessage(`{
				"shipment_id":"shp-async-1",
				"order_id":"` + ordID + `",
				"shop_id":"shop-1",
				"carrier_code":"ghn",
				"service_code":"standard",
				"tracking_code":"GHN-ASYNC-001",
				"label_url":"https://labels.sandbox/async-1",
				"status":"created",
				"direction":"FORWARD"
			}`),
		})
		if act2, err2 := c.ProcessRawMessage(context.Background(), events.SubjectShipmentUpdated, createdEnv2, nil, 1); err2 != nil || act2 != events.ActionAck {
			t.Fatalf("duplicate created event failed: %v, %v", act2, err2)
		}
		got2, _ := h.svc.GetOrder(sellerCtx, &orderv1.GetOrderRequest{OrderId: ordID})
		if len(got2.Order.Timeline) != tlCountAfterFirst {
			t.Fatalf("E18: idempotent created replay added duplicate timeline events (%d -> %d)", tlCountAfterFirst, len(got2.Order.Timeline))
		}
	})

	t.Run("CancelWhileQueued_LateCreatedEventTriggersCompensatingCancelShipment", func(t *testing.T) {
		h := newTestHarness()
		co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w3b-e18-cancel-q",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		ordID := co.Orders[0].Id

		h.shipping.onCreateShipment = func(_ *shippingv1.CreateShipmentRequest) (*shippingv1.CreateShipmentResponse, error) {
			return &shippingv1.CreateShipmentResponse{Queued: true, JobId: "job-cancel-1"}, nil
		}
		if _, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID, ShopId: "shop-1"}); err != nil {
			t.Fatalf("SellerShipOrder queued: %v", err)
		}

		// Buyer cancels while queued -> MUST succeed immediately (not blocked by pending_shipment_cancel!).
		cancResp, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: ordID, Reason: "changed mind"})
		if err != nil {
			t.Fatalf("E18: CancelOrder while queued must succeed, got %v", err)
		}
		if cancResp.Order.Status != orderv1.OrderStatus_ORDER_STATUS_CANCELLED || cancResp.Order.ShipmentJobStatus != "" {
			t.Fatalf("unexpected cancelled order: status=%v job=%q", cancResp.Order.Status, cancResp.Order.ShipmentJobStatus)
		}

		// Async worker finishes an in-flight carrier call and emits shipment.updated(status="created") AFTER cancel!
		beforeLateCancelCalls := len(h.shipping.cancelShipCalls)
		c := events.NewConsumer(nil, nil, h.store, h.svc, logger.New("order-test", logger.WithWriter(io.Discard)), "NEXUSMART")
		lateCreated, _ := json.Marshal(events.Envelope{
			EventID:   "evt-ship-late-created",
			EventType: events.SubjectShipmentUpdated,
			Payload: json.RawMessage(`{
				"shipment_id":"shp-orphan-1",
				"order_id":"` + ordID + `",
				"shop_id":"shop-1",
				"carrier_code":"ghn",
				"service_code":"standard",
				"tracking_code":"GHN-ORPHAN-1",
				"label_url":"https://labels.sandbox/orphan-1",
				"status":"created",
				"direction":"FORWARD"
			}`),
		})
		act, err := c.ProcessRawMessage(context.Background(), events.SubjectShipmentUpdated, lateCreated, nil, 1)
		if err != nil || act != events.ActionAck {
			t.Fatalf("late created event handling failed: %v, %v", act, err)
		}
		if len(h.shipping.cancelShipCalls) <= beforeLateCancelCalls {
			t.Fatalf("E18 violation: late shipment.updated(created) on CANCELLED order MUST call compensating Shipping.CancelShipment")
		}
		got, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ordID})
		if got.Order.Status != orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
			t.Fatalf("order must stay CANCELLED, got %v", got.Order.Status)
		}
	})

	t.Run("ShipmentCreateFailedEvent_SetsFailedAndAllowsSellerRetry", func(t *testing.T) {
		h := newTestHarness()
		co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w3b-e18-fail-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		ordID := co.Orders[0].Id

		h.shipping.onCreateShipment = func(_ *shippingv1.CreateShipmentRequest) (*shippingv1.CreateShipmentResponse, error) {
			return &shippingv1.CreateShipmentResponse{Queued: true, JobId: "job-fail-1"}, nil
		}
		if _, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID, ShopId: "shop-1"}); err != nil {
			t.Fatalf("SellerShipOrder queued: %v", err)
		}

		// Deliver shipment.create_failed via Consumer.
		c := events.NewConsumer(nil, nil, h.store, h.svc, logger.New("order-test", logger.WithWriter(io.Discard)), "NEXUSMART")
		failedEnv, _ := json.Marshal(events.Envelope{
			EventID:   "evt-ship-create-failed-1",
			EventType: "shipment.create_failed",
			Payload: json.RawMessage(`{
				"job_id":"job-fail-1",
				"order_id":"` + ordID + `",
				"shop_id":"shop-1",
				"carrier_code":"ghn",
				"service_code":"standard",
				"attempts":8,
				"error_message":"HTTP 503 https://carrier/internal",
				"occurred_at":"2026-11-24T10:00:00Z"
			}`),
		})
		act, err := c.ProcessRawMessage(context.Background(), "shipment.create_failed", failedEnv, nil, 1)
		if err != nil || act != events.ActionAck {
			t.Fatalf("ProcessRawMessage(shipment.create_failed) = %v, %v", act, err)
		}

		got, err := h.svc.GetOrder(sellerCtx, &orderv1.GetOrderRequest{OrderId: ordID})
		if err != nil {
			t.Fatalf("GetOrder: %v", err)
		}
		if got.Order.ShipmentJobStatus != "FAILED" || got.Order.Status != orderv1.OrderStatus_ORDER_STATUS_CONFIRMED {
			t.Fatalf("E18: expected shipment_job_status=FAILED and status=CONFIRMED, got job=%q status=%v",
				got.Order.ShipmentJobStatus, got.Order.Status)
		}
		foundFailedNote := false
		for _, tl := range got.Order.Timeline {
			note := tl.GetNote()
			if strings.Contains(note, "503") || strings.Contains(note, "carrier/") || strings.Contains(note, "job-fail-1") {
				t.Fatalf("E18: timeline note must not expose raw carrier error or job_id, got %q", note)
			}
			if strings.Contains(note, "after 8 attempts") {
				foundFailedNote = true
			}
		}
		if !foundFailedNote {
			t.Fatalf("E18: expected generic failure note with attempt count in timeline, got %+v", got.Order.Timeline)
		}
		gotBuyer, err := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ordID})
		if err != nil {
			t.Fatalf("GetOrder(buyer): %v", err)
		}
		for _, tl := range gotBuyer.Order.Timeline {
			note := tl.GetNote()
			if strings.Contains(note, "503") || strings.Contains(note, "carrier/") || strings.Contains(note, "job-fail-1") {
				t.Fatalf("E18: buyer timeline note must not expose raw carrier error or job_id, got %q", note)
			}
		}

		// Seller retries SellerShipOrder after carrier recovers -> succeeds and clears shipment_job_status!
		h.shipping.onCreateShipment = nil
		retryResp, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID, ShopId: "shop-1"})
		if err != nil {
			t.Fatalf("E18: seller retry after FAILED must succeed, got %v", err)
		}
		if retryResp.GetQueued() || retryResp.GetTrackingCode() == "" ||
			retryResp.GetOrder().GetStatus() != orderv1.OrderStatus_ORDER_STATUS_PREPARING ||
			retryResp.GetOrder().GetShipmentJobStatus() != "" {
			t.Fatalf("E18: unexpected retry response: queued=%v trk=%q status=%v job=%q",
				retryResp.GetQueued(), retryResp.GetTrackingCode(), retryResp.GetOrder().GetStatus(), retryResp.GetOrder().GetShipmentJobStatus())
		}

		// Stale shipment.create_failed arriving AFTER tracking_code is already attached MUST NOT overwrite shipment_job_status to FAILED!
		staleFailedEnv, _ := json.Marshal(events.Envelope{
			EventID:   "evt-ship-create-failed-stale",
			EventType: "shipment.create_failed",
			Payload: json.RawMessage(`{
				"job_id":"job-old",
				"order_id":"` + ordID + `",
				"shop_id":"shop-1",
				"carrier_code":"ghn",
				"service_code":"standard",
				"attempts":8,
				"error_message":"stale failure"
			}`),
		})
		if _, err := c.ProcessRawMessage(context.Background(), "shipment.create_failed", staleFailedEnv, nil, 1); err != nil {
			t.Fatalf("stale shipment.create_failed: %v", err)
		}
		gotAfter, _ := h.svc.GetOrder(sellerCtx, &orderv1.GetOrderRequest{OrderId: ordID})
		if gotAfter.Order.ShipmentJobStatus != "" {
			t.Fatalf("E18: stale shipment.create_failed must not set FAILED when tracking_code already attached, got %q", gotAfter.Order.ShipmentJobStatus)
		}
	})
}

// TestW3B_E20_OrderDeliveredIncludesCarrierAndTracking verifies E20:
// order.delivered outbox event payload carries both carrier_code and tracking_code.
func TestW3B_E20_OrderDeliveredIncludesCarrierAndTracking(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

	h := newTestHarness()
	co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "w3b-e20-deliv-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	if err != nil {
		t.Fatalf("CreateCheckout: %v", err)
	}
	ordID := co.Orders[0].Id

	shipResp, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{
		OrderId: ordID,
		ShopId:  "shop-1",
	})
	if err != nil {
		t.Fatalf("SellerShipOrder: %v", err)
	}

	if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-e20-delivered", events.ShipmentUpdatedPayload{
		OrderID:      ordID,
		CarrierCode:  "ghn",
		TrackingCode: shipResp.TrackingCode,
		Status:       "delivered",
		Description:  "Delivered COD",
	}); err != nil {
		t.Fatalf("HandleShipmentUpdated(delivered): %v", err)
	}

	var found bool
	for _, ev := range h.store.OutboxEvents() {
		if ev.EventType != events.SubjectOrderDelivered {
			continue
		}
		found = true
		var payload map[string]any
		if err := json.Unmarshal(ev.PayloadJSON, &payload); err != nil {
			t.Fatalf("unmarshal order.delivered payload: %v", err)
		}
		if payload["carrier_code"] != "ghn" {
			t.Fatalf("E20 violation: order.delivered missing/wrong carrier_code: %v (raw=%s)", payload["carrier_code"], string(ev.PayloadJSON))
		}
		if payload["tracking_code"] != shipResp.TrackingCode {
			t.Fatalf("E20 violation: order.delivered tracking_code=%v, want %q", payload["tracking_code"], shipResp.TrackingCode)
		}
	}
	if !found {
		t.Fatalf("E20 violation: order.delivered outbox event not found")
	}
}

// TestW3B_ConfigDefaults_CODEnv verifies E19 default configuration values from config.Load().
func TestW3B_ConfigDefaults_CODEnv(t *testing.T) {
	t.Setenv("ORDER_COD_REQUIRE_PHONE_VERIFIED", "")
	t.Setenv("ORDER_COD_MAX_REJECTED_COUNT", "")
	t.Setenv("ORDER_COD_MIN_ORDERS_FOR_RATE", "")
	t.Setenv("ORDER_COD_MAX_REJECT_RATE_BPS", "")

	cfg := config.Load()
	if !cfg.CODRequirePhoneVerified {
		t.Fatalf("E19 violation: ORDER_COD_REQUIRE_PHONE_VERIFIED must default to true")
	}
	if cfg.CODMaxRejectedCount != 3 {
		t.Fatalf("ORDER_COD_MAX_REJECTED_COUNT default = %d, want 3", cfg.CODMaxRejectedCount)
	}
	if cfg.CODMinOrdersForRate != 3 {
		t.Fatalf("ORDER_COD_MIN_ORDERS_FOR_RATE default = %d, want 3", cfg.CODMinOrdersForRate)
	}
	if cfg.CODMaxRejectRateBPS != 3000 {
		t.Fatalf("ORDER_COD_MAX_REJECT_RATE_BPS default = %d, want 3000", cfg.CODMaxRejectRateBPS)
	}
}

type failingCODStatsStore struct {
	store.Store
	err error
}

func (f *failingCODStatsStore) GetBuyerTerminalCODStats(_ context.Context, _ string) (store.BuyerCODStats, error) {
	return store.BuyerCODStats{}, f.err
}

// TestW3B_E19_EdgeAndAttackCases verifies remaining E19 edge cases and failure modes:
//   - ORDER_COD_REQUIRE_PHONE_VERIFIED=false skips phone check while keeping area/rate checks active.
//   - cod_rejected_count >= 3 still blocks COD even when terminal rate is < 30%.
//   - Multi-shop checkout rejects COD before any reservation if ANY shop lacks COD support.
//   - Store error during GetBuyerTerminalCODStats fails closed with Unavailable before any reservation.
func TestW3B_E19_EdgeAndAttackCases(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")

	t.Run("PhoneVerifiedToggleFalse_SkipsPhoneKeepsAreaAndRate", func(t *testing.T) {
		h := newTestHarness()
		h.svc.cfg.CODRequirePhoneVerified = false
		h.user.phoneVerified = false

		// Allowed when area and rate are OK.
		if _, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w3b-e19-toggle-ok",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		}); err != nil {
			t.Fatalf("expected COD allowed when CODRequirePhoneVerified=false, got %v", err)
		}

		// Area check STILL blocks even when phone check is disabled.
		h.shipping.quotes = []*shippingv1.CarrierQuote{
			{CarrierCode: "ghn", ServiceCode: "standard", FeeVnd: 30000, CodSupported: false},
		}
		_, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w3b-e19-toggle-area-block",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "COD_AREA_NOT_SUPPORTED") {
			t.Fatalf("expected COD_AREA_NOT_SUPPORTED even when phone check disabled, got %v", err)
		}
	})

	t.Run("RejectedCountThresholdStillBlocksEvenWhenRateBelow30Pct", func(t *testing.T) {
		h := newTestHarness()
		h.user.codRejectedCount = 3
		// Even with 10 completed + 0 refused in order DB (0% rate), cod_rejected_count >= 3 MUST still block!
		for i := 0; i < 10; i++ {
			seedCODHistoryOrder(t, h.store, "buyer-1", "cnt-ok-"+string(rune('a'+i)), "COMPLETED")
		}
		_, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w3b-e19-cnt-block",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "COD_REJECTED_LIMIT_EXCEEDED") {
			t.Fatalf("expected COD_REJECTED_LIMIT_EXCEEDED when cod_rejected_count=3, got %v", err)
		}
		if len(h.product.reserveCalls) != 0 {
			t.Fatalf("must reject before ReserveStock")
		}
	})

	t.Run("MultiShop_OneShopNoCOD_RejectsBeforeAnyReserve", func(t *testing.T) {
		h := newTestHarness()
		h.shipping.onCalculateFee = func(req *shippingv1.CalculateFeeRequest) (*shippingv1.CalculateFeeResponse, error) {
			codOK := req.GetShopId() == "shop-1"
			return &shippingv1.CalculateFeeResponse{
				Quotes: []*shippingv1.CarrierQuote{
					{CarrierCode: "ghn", ServiceCode: "standard", FeeVnd: 30000, CodSupported: codOK},
				},
			}, nil
		}
		_, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w3b-e19-multishop-nocod",
			AddressId:      "addr-buyer-1",
			Items: []*orderv1.CheckoutItemInput{
				{SkuId: "sku-1", Quantity: 1},
				{SkuId: "sku-2", Quantity: 1},
			},
			PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "COD_AREA_NOT_SUPPORTED") {
			t.Fatalf("multi-shop COD must reject with COD_AREA_NOT_SUPPORTED when shop-2 lacks COD, got %v", err)
		}
		if len(h.product.reserveCalls) != 0 || len(h.promo.reserveCalls) != 0 {
			t.Fatalf("must NOT reserve stock/promo for shop-1 when shop-2 lacks COD")
		}
	})

	t.Run("StoreCODStatsError_FailsClosedUnavailableBeforeReserve", func(t *testing.T) {
		h := newTestHarness()
		h.svc.store = &failingCODStatsStore{Store: h.store, err: errors.New("db connection reset")}
		_, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w3b-e19-store-err",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if status.Code(err) != codes.Unavailable {
			t.Fatalf("expected Unavailable on COD stats DB failure, got %v", err)
		}
		if len(h.product.reserveCalls) != 0 {
			t.Fatalf("must fail closed before ReserveStock on COD stats DB error")
		}
	})
}

// TestW3B_E18_RaceAndEdgeCases verifies remaining E18/E20 races and edge cases:
//   - shipment.updated(created) beating CreateShipment's queued=true return never downgrades back to QUEUED.
//   - Out-of-order shipment.updated(delivering) before created preserves SHIPPING and backfills label_url.
//   - Carrier 4xx error on SellerShipOrder clears pending_shipment_cancel and leaves shipment_job_status="".
//   - Late created on CANCELLED order where carrier already picked up (FailedPrecondition) flags manual review.
//   - E20 order.delivered with fallback carrier resolves real carrier_code from shipment.updated.
func TestW3B_E18_RaceAndEdgeCases(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

	t.Run("CreatedEventBeatsQueuedReturn_NeverDowngradesToQueued", func(t *testing.T) {
		h := newTestHarness()
		co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w3b-e18-race-beat",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		ordID := co.Orders[0].Id

		// Simulate async worker delivering shipment.updated(created) BEFORE CreateShipment RPC returns queued=true.
		h.shipping.onCreateShipment = func(_ *shippingv1.CreateShipmentRequest) (*shippingv1.CreateShipmentResponse, error) {
			if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-fast-beat", events.ShipmentUpdatedPayload{
				OrderID:      ordID,
				CarrierCode:  "ghn",
				ServiceCode:  "standard",
				TrackingCode: "GHN-BEAT-1",
				LabelURL:     "https://labels/beat-1",
				Status:       "created",
				Direction:    "FORWARD",
			}); err != nil {
				t.Errorf("mid-RPC HandleShipmentUpdated(created): %v", err)
			}
			return &shippingv1.CreateShipmentResponse{Queued: true, JobId: "job-beat"}, nil
		}

		resp, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID, ShopId: "shop-1"})
		if err != nil {
			t.Fatalf("SellerShipOrder: %v", err)
		}
		if resp.GetQueued() || resp.GetTrackingCode() != "GHN-BEAT-1" ||
			resp.GetOrder().GetStatus() != orderv1.OrderStatus_ORDER_STATUS_PREPARING ||
			resp.GetOrder().GetShipmentJobStatus() != "" {
			t.Fatalf("race guard failed: got queued=%v trk=%q status=%v job=%q",
				resp.GetQueued(), resp.GetTrackingCode(), resp.GetOrder().GetStatus(), resp.GetOrder().GetShipmentJobStatus())
		}
	})

	t.Run("OutOfOrderDeliveringBeforeCreated_KeepsShippingAndFillsLabel", func(t *testing.T) {
		h := newTestHarness()
		co, _ := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w3b-e18-ooo",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		ordID := co.Orders[0].Id

		if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-ooo-1", events.ShipmentUpdatedPayload{
			OrderID:      ordID,
			CarrierCode:  "ghn",
			TrackingCode: "GHN-OOO-1",
			Status:       "delivering",
		}); err != nil {
			t.Fatalf("delivering: %v", err)
		}
		if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-ooo-2", events.ShipmentUpdatedPayload{
			OrderID:      ordID,
			CarrierCode:  "ghn",
			ServiceCode:  "standard",
			TrackingCode: "GHN-OOO-1",
			LabelURL:     "https://labels/ooo-1",
			Status:       "created",
		}); err != nil {
			t.Fatalf("late created: %v", err)
		}
		got, _ := h.svc.GetOrder(sellerCtx, &orderv1.GetOrderRequest{OrderId: ordID})
		if got.Order.Status != orderv1.OrderStatus_ORDER_STATUS_SHIPPING || got.Order.LabelUrl != "https://labels/ooo-1" {
			t.Fatalf("out-of-order created downgraded status or dropped label: status=%v label=%q", got.Order.Status, got.Order.LabelUrl)
		}
	})

	t.Run("Carrier4xxError_ClearsPendingCancelAndLeavesJobStatusEmpty", func(t *testing.T) {
		h := newTestHarness()
		co, _ := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w3b-e18-4xx",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		ordID := co.Orders[0].Id

		h.shipping.onCreateShipment = func(_ *shippingv1.CreateShipmentRequest) (*shippingv1.CreateShipmentResponse, error) {
			return nil, status.Error(codes.InvalidArgument, "invalid destination address")
		}
		_, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID, ShopId: "shop-1"})
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("expected InvalidArgument from 4xx carrier error, got %v", err)
		}
		agg, _ := h.store.GetOrderWithDetails(context.Background(), ordID)
		if agg.Order.PendingShipmentCancel || agg.Order.ShipmentJobStatus != "" || agg.Order.Status != "CONFIRMED" {
			t.Fatalf("4xx error must clear pending_shipment_cancel and not set QUEUED/FAILED: pending=%v job=%q status=%s",
				agg.Order.PendingShipmentCancel, agg.Order.ShipmentJobStatus, agg.Order.Status)
		}
	})

	t.Run("LateCreatedOnCancelled_AlreadyPickedUpFlagsManualReview", func(t *testing.T) {
		h := newTestHarness()
		co, _ := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w3b-e18-manual-rev",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		ordID := co.Orders[0].Id

		h.shipping.onCreateShipment = func(_ *shippingv1.CreateShipmentRequest) (*shippingv1.CreateShipmentResponse, error) {
			return &shippingv1.CreateShipmentResponse{Queued: true, JobId: "job-mr-1"}, nil
		}
		if _, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID, ShopId: "shop-1"}); err != nil {
			t.Fatalf("SellerShipOrder queued: %v", err)
		}
		if _, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: ordID, Reason: "cancel"}); err != nil {
			t.Fatalf("CancelOrder: %v", err)
		}

		// Carrier already picked up the parcel -> CancelShipment fails with FailedPrecondition.
		h.shipping.cancelShipErr = status.Error(codes.FailedPrecondition, "shipment already picked up")
		if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-mr-created", events.ShipmentUpdatedPayload{
			OrderID:      ordID,
			CarrierCode:  "ghn",
			TrackingCode: "GHN-PICKED-1",
			Status:       "created",
		}); err != nil {
			t.Fatalf("HandleShipmentUpdated(late created): %v", err)
		}
		agg, _ := h.store.GetOrderWithDetails(context.Background(), ordID)
		if !agg.Order.OrphanShipmentNeedsManualReview || agg.Order.PendingShipmentCancel {
			t.Fatalf("expected OrphanShipmentNeedsManualReview=true and PendingShipmentCancel=false, got review=%v pending=%v",
				agg.Order.OrphanShipmentNeedsManualReview, agg.Order.PendingShipmentCancel)
		}
	})

	t.Run("E20_FallbackCarrierResolvedInOrderDeliveredPayload", func(t *testing.T) {
		h := newTestHarness()
		h.shipping.quotes = []*shippingv1.CarrierQuote{
			{CarrierCode: "fallback", CarrierName: "Fallback", ServiceCode: "standard", FeeVnd: 25000, CodSupported: true},
		}
		co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w3b-e20-fallback",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil {
			t.Fatalf("CreateCheckout fallback: %v", err)
		}
		ordID := co.Orders[0].Id

		if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-fb-deliv", events.ShipmentUpdatedPayload{
			OrderID:      ordID,
			CarrierCode:  "ghtk",
			TrackingCode: "GHTK-FB-999",
			Status:       "delivered",
		}); err != nil {
			t.Fatalf("HandleShipmentUpdated(delivered): %v", err)
		}
		var found bool
		for _, ev := range h.store.OutboxEvents() {
			if ev.EventType != events.SubjectOrderDelivered {
				continue
			}
			var payload map[string]any
			if err := json.Unmarshal(ev.PayloadJSON, &payload); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if payload["order_id"] == ordID {
				found = true
				if payload["carrier_code"] != "ghtk" || payload["tracking_code"] != "GHTK-FB-999" {
					t.Fatalf("E20 fallback carrier_code resolution failed: got carrier=%v trk=%v", payload["carrier_code"], payload["tracking_code"])
				}
			}
		}
		if !found {
			t.Fatalf("order.delivered outbox event not found for fallback order")
		}
	})
}

type hookTransitionStore struct {
	store.Store
	beforeTransition func(params store.SingleOrderTransitionParams)
}

func (s *hookTransitionStore) TransitionOrderStatus(ctx context.Context, params store.SingleOrderTransitionParams) (*store.OrderTransitionResult, error) {
	if s.beforeTransition != nil {
		s.beforeTransition(params)
	}
	return s.Store.TransitionOrderStatus(ctx, params)
}

// TestW3B_ReviewP1_CancelQueuedShipmentAndCancelledOrphanEvents verifies independent-review P1 fixes:
//  1. Cancelling a QUEUED order calls Shipping.CancelShipment(FORWARD) BEFORE committing CANCELLED.
//  2. If carrier already picked up (CancelShipment returns FailedPrecondition), cancel is rejected with
//     no refund and no stock/promo release.
//  3. If async worker just created a cancellable shipment before order received shipment.updated(created),
//     CancelShipment cancels it first and then the order commits CANCELLED.
//  4. If CancelShipment cancels the queued job but TransitionOrderStatus fails (order stays alive),
//     shipment_job_status transitions to FAILED so the order is not stuck QUEUED and seller can retry.
//  5. Racing SellerShipOrder(queued=true) committing during CancelOrder is caught by checkCancelShipmentGuard
//     under FOR UPDATE and forces CancelShipment before CANCELLED commit.
//  6. A CANCELLED order with no tracking receiving picking/picked/delivering triggers compensating CancelShipment
//     and sets orphan_shipment_needs_manual_review=true on FailedPrecondition; "cancelled" is ignored;
//     duplicate events do not loop infinitely.
func TestW3B_ReviewP1_CancelQueuedShipmentAndCancelledOrphanEvents(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

	createPaidConfirmedVietQROrder := func(t *testing.T, h *testHarness, idemKey string) string {
		t.Helper()
		co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: idemKey,
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout(%s): %v", idemKey, err)
		}
		if err := h.svc.HandlePaymentSucceeded(context.Background(), "evt-pay-"+idemKey, events.PaymentSucceededPayload{
			PaymentID:    co.PaymentId,
			OrderGroupID: co.CheckoutGroupId,
			ProviderTxID: "tx-" + idemKey,
		}); err != nil {
			t.Fatalf("HandlePaymentSucceeded(%s): %v", idemKey, err)
		}
		return co.Orders[0].Id
	}

	t.Run("CancelWhileQueued_CallsCancelShipmentBeforeCommit", func(t *testing.T) {
		h := newTestHarness()
		ordID := createPaidConfirmedVietQROrder(t, h, "p1-q-before-commit")

		h.shipping.onCreateShipment = func(_ *shippingv1.CreateShipmentRequest) (*shippingv1.CreateShipmentResponse, error) {
			return &shippingv1.CreateShipmentResponse{Queued: true, JobId: "job-p1-1"}, nil
		}
		if _, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID, ShopId: "shop-1"}); err != nil {
			t.Fatalf("SellerShipOrder queued: %v", err)
		}

		var statusDuringCancelShip string
		h.shipping.onCancelShipment = func(req *shippingv1.CancelShipmentRequest) error {
			agg, gErr := h.store.GetOrderWithDetails(context.Background(), req.GetOrderId())
			if gErr == nil {
				statusDuringCancelShip = agg.Order.Status
			}
			// Pending job cancelled in shipping -> no shipment row exists -> NotFound.
			return status.Error(codes.NotFound, "no shipment yet; queued job cancelled")
		}

		cancResp, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: ordID, Reason: "buyer cancel queued"})
		if err != nil {
			t.Fatalf("CancelOrder while QUEUED failed: %v", err)
		}
		if len(h.shipping.cancelShipCalls) != 1 {
			t.Fatalf("P1 violation: expected exactly 1 CancelShipment call before commit, got %d", len(h.shipping.cancelShipCalls))
		}
		if statusDuringCancelShip != "CONFIRMED" {
			t.Fatalf("P1 violation: CancelShipment must run BEFORE CANCELLED commit, saw order status=%q", statusDuringCancelShip)
		}
		if cancResp.Order.Status != orderv1.OrderStatus_ORDER_STATUS_CANCELLED || cancResp.Order.ShipmentJobStatus != "" {
			t.Fatalf("unexpected cancelled order: status=%v job=%q", cancResp.Order.Status, cancResp.Order.ShipmentJobStatus)
		}
	})

	t.Run("CancelWhileQueued_CarrierAlreadyPickedUp_RejectsNoRefundNoRestock", func(t *testing.T) {
		h := newTestHarness()
		ordID := createPaidConfirmedVietQROrder(t, h, "p1-q-picked")

		h.shipping.onCreateShipment = func(_ *shippingv1.CreateShipmentRequest) (*shippingv1.CreateShipmentResponse, error) {
			return &shippingv1.CreateShipmentResponse{Queued: true, JobId: "job-p1-picked"}, nil
		}
		if _, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID, ShopId: "shop-1"}); err != nil {
			t.Fatalf("SellerShipOrder queued: %v", err)
		}

		h.shipping.cancelShipErr = status.Error(codes.FailedPrecondition, "carrier already picked up parcel")

		_, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: ordID, Reason: "too late"})
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("P1 violation: CancelOrder must be rejected with FailedPrecondition when carrier already picked up, got %v", err)
		}
		if len(h.shipping.cancelShipCalls) != 1 {
			t.Fatalf("expected 1 CancelShipment probe, got %d", len(h.shipping.cancelShipCalls))
		}
		if len(h.payment.refundCalls) != 0 {
			t.Fatalf("P1 violation: must NOT refund when CancelShipment fails, got %d refund calls", len(h.payment.refundCalls))
		}
		if len(h.product.restockCalls) != 0 || len(h.product.releaseCalls) != 0 {
			t.Fatalf("P1 violation: must NOT restock/release stock when CancelShipment fails, got restock=%d release=%d",
				len(h.product.restockCalls), len(h.product.releaseCalls))
		}
		agg, _ := h.store.GetOrderWithDetails(context.Background(), ordID)
		if agg.Order.Status != "CONFIRMED" || agg.Order.ShipmentJobStatus != "QUEUED" {
			t.Fatalf("order must remain CONFIRMED/QUEUED after rejected cancel, got status=%s job=%q",
				agg.Order.Status, agg.Order.ShipmentJobStatus)
		}
	})

	t.Run("CancelWhileQueued_WorkerJustCreatedShipment_CancelsShipmentThenOrder", func(t *testing.T) {
		h := newTestHarness()
		ordID := createPaidConfirmedVietQROrder(t, h, "p1-q-just-created")

		h.shipping.onCreateShipment = func(_ *shippingv1.CreateShipmentRequest) (*shippingv1.CreateShipmentResponse, error) {
			return &shippingv1.CreateShipmentResponse{Queued: true, JobId: "job-p1-just"}, nil
		}
		if _, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID, ShopId: "shop-1"}); err != nil {
			t.Fatalf("SellerShipOrder queued: %v", err)
		}

		// Worker already created shipment TRK-WORKER-1 in shipping before shipment.updated(created) reached order.
		h.shipping.forwardTrackings[ordID] = "TRK-WORKER-1"
		var statusAtCancelShip string
		h.shipping.onCancelShipment = func(req *shippingv1.CancelShipmentRequest) error {
			agg, _ := h.store.GetOrderWithDetails(context.Background(), req.GetOrderId())
			statusAtCancelShip = agg.Order.Status
			return nil
		}

		cancResp, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: ordID, Reason: "cancel before pickup"})
		if err != nil {
			t.Fatalf("CancelOrder failed: %v", err)
		}
		if len(h.shipping.cancelShipCalls) != 1 || statusAtCancelShip != "CONFIRMED" {
			t.Fatalf("expected CancelShipment called once while CONFIRMED, got calls=%d status=%q",
				len(h.shipping.cancelShipCalls), statusAtCancelShip)
		}
		if cancResp.Order.Status != orderv1.OrderStatus_ORDER_STATUS_CANCELLED || cancResp.Order.ShipmentJobStatus != "" {
			t.Fatalf("expected CANCELLED with cleared shipment_job_status, got status=%v job=%q",
				cancResp.Order.Status, cancResp.Order.ShipmentJobStatus)
		}
	})

	t.Run("CancelWhileQueued_TransitionFailsOrderStaysAlive_MarksJobFailedForSellerRetry", func(t *testing.T) {
		h := newTestHarness()
		ordID := createPaidConfirmedVietQROrder(t, h, "p1-q-abort-alive")

		h.shipping.onCreateShipment = func(_ *shippingv1.CreateShipmentRequest) (*shippingv1.CreateShipmentResponse, error) {
			return &shippingv1.CreateShipmentResponse{Queued: true, JobId: "job-p1-abort"}, nil
		}
		if _, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID, ShopId: "shop-1"}); err != nil {
			t.Fatalf("SellerShipOrder queued: %v", err)
		}

		// Watchdog auto-cancel sees CONFIRMED+QUEUED, calls CancelShipment (which cancels the job in shipping!),
		// and concurrently seller moves CONFIRMED -> PREPARING before TransitionOrderStatus(expectedStatus=CONFIRMED).
		h.shipping.onCancelShipment = func(_ *shippingv1.CancelShipmentRequest) error {
			if _, cErr := h.svc.SellerConfirmOrder(sellerCtx, &orderv1.SellerConfirmOrderRequest{OrderId: ordID}); cErr != nil {
				t.Errorf("concurrent SellerConfirmOrder: %v", cErr)
			}
			return status.Error(codes.NotFound, "queued job cancelled")
		}
		// Advance clock past seller confirm timeout (48h) so ScanAutoCancelUnconfirmedOrders picks up this order.
		h.svc.nowFn = func() time.Time { return time.Now().UTC().Add(72 * time.Hour) }

		handled, err := h.svc.ScanAutoCancelUnconfirmedOrders(context.Background())
		if err != nil {
			t.Fatalf("ScanAutoCancelUnconfirmedOrders: %v", err)
		}
		if handled != 0 {
			t.Fatalf("expected 0 auto-cancelled orders because seller confirmed concurrently, got %d", handled)
		}
		if len(h.shipping.cancelShipCalls) != 1 {
			t.Fatalf("expected watchdog auto-cancel to call CancelShipment for QUEUED order, got %d", len(h.shipping.cancelShipCalls))
		}
		agg, _ := h.store.GetOrderWithDetails(context.Background(), ordID)
		if agg.Order.Status != "PREPARING" {
			t.Fatalf("expected order to stay alive in PREPARING, got %s", agg.Order.Status)
		}
		if agg.Order.ShipmentJobStatus != "FAILED" {
			t.Fatalf("P1 violation: order stayed alive after job was cancelled; must NOT stay stuck QUEUED, got shipment_job_status=%q (want FAILED)",
				agg.Order.ShipmentJobStatus)
		}

		// Seller can immediately click Ship again and succeed!
		h.shipping.onCreateShipment = nil
		h.shipping.onCancelShipment = nil
		retryResp, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID, ShopId: "shop-1"})
		if err != nil || retryResp.GetTrackingCode() == "" || retryResp.GetOrder().GetShipmentJobStatus() != "" {
			t.Fatalf("seller retry after aborted-cancel FAILED must succeed, got resp=%+v err=%v", retryResp, err)
		}
	})

	t.Run("Race_SellerShipOrderQueuedCommitsDuringCancelOrder", func(t *testing.T) {
		h := newTestHarness()
		ordID := createPaidConfirmedVietQROrder(t, h, "p1-q-race-commit")

		h.shipping.onCreateShipment = func(_ *shippingv1.CreateShipmentRequest) (*shippingv1.CreateShipmentResponse, error) {
			return &shippingv1.CreateShipmentResponse{Queued: true, JobId: "job-p1-race"}, nil
		}

		// Inject a race: CancelOrder loads CONFIRMED (shipment_job_status=""), skips initial CancelShipment,
		// and right before TransitionOrderStatus acquires FOR UPDATE, SellerShipOrder commits QUEUED!
		raceFired := false
		h.svc.store = &hookTransitionStore{
			Store: h.store,
			beforeTransition: func(params store.SingleOrderTransitionParams) {
				if !raceFired && params.TargetStatus == orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
					raceFired = true
					if _, sErr := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID, ShopId: "shop-1"}); sErr != nil {
						t.Errorf("racing SellerShipOrder(queued): %v", sErr)
					}
				}
			},
		}

		// Variant A: Carrier already picked up by the time retry calls CancelShipment -> cancel MUST be rejected!
		h.shipping.cancelShipErr = status.Error(codes.FailedPrecondition, "already picked up")
		_, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: ordID, Reason: "racing cancel"})
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("P1 race violation: expected FailedPrecondition when queued job raced and carrier picked up, got %v", err)
		}
		if len(h.shipping.cancelShipCalls) != 1 || len(h.payment.refundCalls) != 0 || len(h.product.restockCalls) != 0 {
			t.Fatalf("P1 race violation: expected 1 CancelShipment and 0 refund/restock, got cancel=%d refund=%d restock=%d",
				len(h.shipping.cancelShipCalls), len(h.payment.refundCalls), len(h.product.restockCalls))
		}
		agg, _ := h.store.GetOrderWithDetails(context.Background(), ordID)
		if agg.Order.Status != "CONFIRMED" || agg.Order.ShipmentJobStatus != "QUEUED" {
			t.Fatalf("expected order to remain CONFIRMED/QUEUED after blocked race, got %s/%q", agg.Order.Status, agg.Order.ShipmentJobStatus)
		}

		// Variant B: Same race on a fresh order where CancelShipment cancels the queued job (NotFound) -> succeeds AFTER calling CancelShipment!
		h2 := newTestHarness()
		ordID2 := createPaidConfirmedVietQROrder(t, h2, "p1-q-race-commit-ok")
		h2.shipping.onCreateShipment = func(_ *shippingv1.CreateShipmentRequest) (*shippingv1.CreateShipmentResponse, error) {
			return &shippingv1.CreateShipmentResponse{Queued: true, JobId: "job-p1-race-2"}, nil
		}
		raceFired2 := false
		h2.svc.store = &hookTransitionStore{
			Store: h2.store,
			beforeTransition: func(params store.SingleOrderTransitionParams) {
				if !raceFired2 && params.TargetStatus == orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
					raceFired2 = true
					if _, sErr := h2.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID2, ShopId: "shop-1"}); sErr != nil {
						t.Errorf("racing SellerShipOrder(queued) 2: %v", sErr)
					}
				}
			},
		}
		var statusDuringCancel2 string
		h2.shipping.onCancelShipment = func(req *shippingv1.CancelShipmentRequest) error {
			a, _ := h2.store.GetOrderWithDetails(context.Background(), req.GetOrderId())
			statusDuringCancel2 = a.Order.Status
			return status.Error(codes.NotFound, "queued job cancelled")
		}
		resp2, err := h2.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: ordID2, Reason: "racing cancel ok"})
		if err != nil {
			t.Fatalf("racing CancelOrder (cancellable queued job) failed: %v", err)
		}
		if len(h2.shipping.cancelShipCalls) != 1 || statusDuringCancel2 != "CONFIRMED" {
			t.Fatalf("P1 race violation: expected CancelShipment before commit, got calls=%d status=%q",
				len(h2.shipping.cancelShipCalls), statusDuringCancel2)
		}
		if resp2.Order.Status != orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
			t.Fatalf("expected CANCELLED, got %v", resp2.Order.Status)
		}
	})

	t.Run("CancelledOrderNoTracking_DeliveringOrPicking_FlagsManualReviewAndNoLoop", func(t *testing.T) {
		h := newTestHarness()
		co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "p1-can-deliv",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		ordID := co.Orders[0].Id
		if _, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: ordID, Reason: "cancel"}); err != nil {
			t.Fatalf("CancelOrder: %v", err)
		}

		// 1. status="cancelled" on CANCELLED order must be ignored (0 CancelShipment calls).
		if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-p1-cancelled-noop", events.ShipmentUpdatedPayload{
			OrderID:      ordID,
			CarrierCode:  "ghn",
			TrackingCode: "TRK-ORPHAN-DELIV",
			Status:       "cancelled",
		}); err != nil {
			t.Fatalf("HandleShipmentUpdated(cancelled): %v", err)
		}
		if len(h.shipping.cancelShipCalls) != 0 {
			t.Fatalf("status=cancelled on CANCELLED order must NOT call CancelShipment, got %d", len(h.shipping.cancelShipCalls))
		}

		// 2. status="delivering" on CANCELLED order with no tracking -> calls CancelShipment;
		//    carrier rejects FailedPrecondition (already picked up) -> orphan_shipment_needs_manual_review=true!
		h.shipping.cancelShipErr = status.Error(codes.FailedPrecondition, "already picked up / in transit")
		if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-p1-deliv-1", events.ShipmentUpdatedPayload{
			OrderID:      ordID,
			CarrierCode:  "ghn",
			TrackingCode: "TRK-ORPHAN-DELIV",
			Status:       "delivering",
		}); err != nil {
			t.Fatalf("HandleShipmentUpdated(delivering): %v", err)
		}
		if len(h.shipping.cancelShipCalls) != 1 {
			t.Fatalf("P1 violation: CANCELLED order receiving delivering must call CancelShipment, got %d calls", len(h.shipping.cancelShipCalls))
		}
		agg, _ := h.store.GetOrderWithDetails(context.Background(), ordID)
		if !agg.Order.OrphanShipmentNeedsManualReview || agg.Order.PendingShipmentCancel || agg.Order.Status != "CANCELLED" {
			t.Fatalf("P1 violation: expected OrphanShipmentNeedsManualReview=true PendingShipmentCancel=false Status=CANCELLED, got review=%v pending=%v status=%s",
				agg.Order.OrphanShipmentNeedsManualReview, agg.Order.PendingShipmentCancel, agg.Order.Status)
		}
		tlAfterManualReview := len(agg.Timeline)

		// 3. Duplicate event_id AND subsequent carrier event (e.g. "delivered") MUST NOT loop or spam CancelShipment/timeline!
		if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-p1-deliv-1", events.ShipmentUpdatedPayload{
			OrderID:      ordID,
			CarrierCode:  "ghn",
			TrackingCode: "TRK-ORPHAN-DELIV",
			Status:       "delivering",
		}); err != nil {
			t.Fatalf("duplicate delivering event: %v", err)
		}
		if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-p1-deliv-2", events.ShipmentUpdatedPayload{
			OrderID:      ordID,
			CarrierCode:  "ghn",
			TrackingCode: "TRK-ORPHAN-DELIV",
			Status:       "delivered",
		}); err != nil {
			t.Fatalf("subsequent delivered event after manual review: %v", err)
		}
		if len(h.shipping.cancelShipCalls) != 1 {
			t.Fatalf("must not keep calling CancelShipment after manual review already flagged, got %d", len(h.shipping.cancelShipCalls))
		}
		agg2, _ := h.store.GetOrderWithDetails(context.Background(), ordID)
		if len(agg2.Timeline) != tlAfterManualReview {
			t.Fatalf("duplicate/subsequent orphan events added extra timeline entries: %d -> %d", tlAfterManualReview, len(agg2.Timeline))
		}
	})
}
