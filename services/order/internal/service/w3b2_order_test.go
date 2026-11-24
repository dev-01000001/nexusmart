package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/order/internal/clients"
	"github.com/nexusmart/core/services/order/internal/db"
	"github.com/nexusmart/core/services/order/internal/events"
	"github.com/nexusmart/core/services/order/internal/resilience"
	"github.com/nexusmart/core/services/order/internal/statemachine"
	"github.com/nexusmart/core/services/order/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TestW3B2_F6_PaymentMethodConfigSource verifies F6:
//   - PaymentMethodConfigSource is the sole source of truth for enabled, min/max VND, and expiry minutes
//     (no fallback to removed env vars, no caching).
//   - Missing source, RPC error, NotFound, disabled method, invalid config, below min, and above max
//     (pre-promo and post-promo with compensation) all reject with clear codes.
//   - ORDER_FLASH_PAYMENT_EXPIRY_MINUTES still governs flash sale expiry (clamped by method expiry).
func TestW3B2_F6_PaymentMethodConfigSource(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")

	t.Run("NilSourceOrError_FailsClosedUnavailable_NeverFallsBackToEnvConfig", func(t *testing.T) {
		h := newTestHarness()
		h.svc.SetPaymentMethodConfigSource(nil)
		// Nil source MUST fail closed with Unavailable (no hardcoded fallback).
		_, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "f6-nil-source",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if status.Code(err) != codes.Unavailable || !strings.Contains(err.Error(), "PAYMENT_METHOD_CONFIG_UNAVAILABLE") {
			t.Fatalf("F6 violation: nil source got %v, want Unavailable PAYMENT_METHOD_CONFIG_UNAVAILABLE", err)
		}
		if len(h.product.reserveCalls) != 0 {
			t.Fatalf("must not reserve stock on config failure")
		}

		h2 := newTestHarness()
		h2.methodConfigs.SetError(status.Error(codes.Unavailable, "payment db down"))
		_, err = h2.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "f6-rpc-err",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if status.Code(err) != codes.Unavailable || !strings.Contains(err.Error(), "PAYMENT_METHOD_CONFIG_UNAVAILABLE") {
			t.Fatalf("F6 violation: config error got %v, want Unavailable PAYMENT_METHOD_CONFIG_UNAVAILABLE", err)
		}
		if len(h2.product.reserveCalls) != 0 {
			t.Fatalf("must not reserve stock on config RPC error")
		}

		// NotFound config -> FailedPrecondition PAYMENT_METHOD_NOT_CONFIGURED.
		h3 := newTestHarness()
		h3.methodConfigs.SetMethodError("VIETQR", status.Error(codes.NotFound, "missing"))
		_, err = h3.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "f6-not-configured",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "PAYMENT_METHOD_NOT_CONFIGURED") {
			t.Fatalf("F6 violation: missing method config got %v, want FailedPrecondition PAYMENT_METHOD_NOT_CONFIGURED", err)
		}
	})

	t.Run("DisabledMethod_RejectedBeforeStockOrPromoReserve", func(t *testing.T) {
		h := newTestHarness()
		h.methodConfigs.SetConfig(PaymentMethodConfig{
			Method:        "VIETQR",
			Enabled:       false,
			ExpiryMinutes: 15,
		})
		_, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "f6-disabled-vqr",
			AddressId:         "addr-buyer-1",
			Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PlatformVoucherId: "v-plat",
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "PAYMENT_METHOD_DISABLED") {
			t.Fatalf("F6 violation: disabled method got %v, want FailedPrecondition PAYMENT_METHOD_DISABLED", err)
		}
		if len(h.product.reserveCalls) != 0 || len(h.promo.reserveCalls) != 0 {
			t.Fatalf("disabled method must reject before reserves: stock=%d promo=%d", len(h.product.reserveCalls), len(h.promo.reserveCalls))
		}
	})

	t.Run("MinAndMaxBounds_PrePromoAndPostPromoWithCompensation", func(t *testing.T) {
		h := newTestHarness()
		// Dynamic COD limit 500,000 VND and VietQR min 1,200,000 VND / max 3,000,000 VND.
		h.methodConfigs.SetConfig(PaymentMethodConfig{
			Method:      "COD",
			Enabled:     true,
			MinOrderVND: 0,
			MaxOrderVND: 500000,
		})
		h.methodConfigs.SetConfig(PaymentMethodConfig{
			Method:        "VIETQR",
			Enabled:       true,
			MinOrderVND:   1200000,
			MaxOrderVND:   3000000,
			ExpiryMinutes: 15,
		})

		// Pre-promo COD above dynamic 500k (sku-1 = 1,000,000 + 30,000 ship = 1,030,000) -> COD_LIMIT_EXCEEDED before reserve!
		_, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "f6-cod-max-pre",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "COD_LIMIT_EXCEEDED") {
			t.Fatalf("F6 violation: dynamic COD max got %v, want COD_LIMIT_EXCEEDED", err)
		}
		if len(h.product.reserveCalls) != 0 {
			t.Fatalf("expected 0 reserveCalls on pre-promo COD_LIMIT_EXCEEDED")
		}

		// Pre-promo VIETQR below min (1,030,000 < 1,200,000) -> ORDER_AMOUNT_OUT_OF_RANGE before reserve!
		_, err = h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "f6-vqr-min-pre",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "ORDER_AMOUNT_OUT_OF_RANGE") {
			t.Fatalf("F6 violation: below min got %v, want ORDER_AMOUNT_OUT_OF_RANGE", err)
		}
		if len(h.product.reserveCalls) != 0 {
			t.Fatalf("expected 0 reserveCalls on pre-promo below min")
		}

		// Pre-promo VIETQR above max (4 * 1,000,000 + 30,000 > 3,000,000) -> ORDER_AMOUNT_OUT_OF_RANGE!
		_, err = h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "f6-vqr-max-pre",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 4}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "ORDER_AMOUNT_OUT_OF_RANGE") {
			t.Fatalf("F6 violation: above online max got %v, want ORDER_AMOUNT_OUT_OF_RANGE", err)
		}

		// Post-promo: 2 * 1,000,000 + 30,000 = 2,030,000, voucher reduces by 1,000,000 -> 1,030,000 < 1,200,000 min!
		// Must reject ORDER_AMOUNT_OUT_OF_RANGE AND release stock + promotion!
		h.promo.breakdown = &promotionv1.DiscountBreakdown{
			TotalPlatformVoucherDiscountVnd: 1000000,
			Shops: []*promotionv1.ShopDiscountBreakdown{
				{ShopId: "shop-1", PlatformVoucherDiscountVnd: 1000000},
			},
		}
		_, err = h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "f6-vqr-min-post",
			AddressId:         "addr-buyer-1",
			Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 2}},
			PlatformVoucherId: "v-big",
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "ORDER_AMOUNT_OUT_OF_RANGE") {
			t.Fatalf("F6 violation: post-promo below min got %v, want ORDER_AMOUNT_OUT_OF_RANGE", err)
		}
		if len(h.product.releaseCalls) != 1 || len(h.promo.releaseCalls) != 1 {
			t.Fatalf("F6 post-promo rejection must compensate stock & promo: releaseStock=%d releasePromo=%d",
				len(h.product.releaseCalls), len(h.promo.releaseCalls))
		}
	})

	t.Run("ZeroVNDOrder_ExemptFromMinOrderVND_StillRejectsDisabledMethod", func(t *testing.T) {
		h := newTestHarness()
		// Method is enabled with high min_order_vnd = 100,000 VND.
		h.methodConfigs.SetConfig(PaymentMethodConfig{
			Method:        "VIETQR",
			Enabled:       true,
			MinOrderVND:   100000,
			MaxOrderVND:   5000000,
			ExpiryMinutes: 15,
		})
		// Voucher + freeship cover 100% of sku-1 (1,000,000) + shipping (30,000) -> grandTotalPayableVND == 0.
		h.promo.breakdown = &promotionv1.DiscountBreakdown{
			TotalPlatformVoucherDiscountVnd: 1000000,
			TotalFreeshipDiscountVnd:        30000,
			Shops: []*promotionv1.ShopDiscountBreakdown{
				{ShopId: "shop-1", PlatformVoucherDiscountVnd: 1000000, FreeshipDiscountVnd: 30000},
			},
		}
		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "f6-zero-vnd-ok",
			AddressId:         "addr-buyer-1",
			Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PlatformVoucherId: "v-100pct",
			FreeshipVoucherId: "fs-100pct",
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("0-VND order must be exempt from min_order_vnd, got error: %v", err)
		}
		if resp.TotalPayableVnd != 0 || resp.Orders[0].Status != orderv1.OrderStatus_ORDER_STATUS_CONFIRMED {
			t.Fatalf("0-VND checkout mismatch: total=%d status=%v", resp.TotalPayableVnd, resp.Orders[0].Status)
		}

		// Disabled method MUST still be rejected even when vouchers/coins would make total 0 VND.
		h.methodConfigs.SetConfig(PaymentMethodConfig{
			Method:        "VIETQR",
			Enabled:       false,
			MinOrderVND:   100000,
			MaxOrderVND:   5000000,
			ExpiryMinutes: 15,
		})
		_, err = h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "f6-zero-vnd-disabled",
			AddressId:         "addr-buyer-1",
			Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PlatformVoucherId: "v-100pct",
			FreeshipVoucherId: "fs-100pct",
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "PAYMENT_METHOD_DISABLED") {
			t.Fatalf("disabled method must still reject 0-VND order, got %v", err)
		}
	})

	t.Run("ExpiryMinutesFromConfigSource_NoCaching_AndFlashSaleCapKept", func(t *testing.T) {
		h := newTestHarness()
		baseNow := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
		h.svc.nowFn = func() time.Time { return baseNow }
		// PaymentMethodConfigSource says 22m -> MUST use 22m; FlashPaymentExpiryMinutes=5 caps flash sales.
		h.svc.cfg.FlashPaymentExpiryMinutes = 5
		h.methodConfigs.SetConfig(PaymentMethodConfig{
			Method:        "VIETQR",
			Enabled:       true,
			ExpiryMinutes: 22,
		})

		resp1, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "f6-exp-22m",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout(22m): %v", err)
		}
		if got, want := resp1.PaymentExpiresAt.AsTime(), baseNow.Add(22*time.Minute); !got.Equal(want) {
			t.Fatalf("expiry = %v, want %v (from PaymentMethodConfigSource)", got, want)
		}

		// Live update to 9m with NO cache -> immediate effect on next checkout!
		h.methodConfigs.SetConfig(PaymentMethodConfig{
			Method:        "VIETQR",
			Enabled:       true,
			ExpiryMinutes: 9,
		})
		resp2, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "f6-exp-9m",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout(9m): %v", err)
		}
		if got, want := resp2.PaymentExpiresAt.AsTime(), baseNow.Add(9*time.Minute); !got.Equal(want) {
			t.Fatalf("expiry after live config update = %v, want %v (no caching)", got, want)
		}
		if len(h.methodConfigs.Calls()) < 2 {
			t.Fatalf("expected direct uncached config reads per checkout, got calls=%v", h.methodConfigs.Calls())
		}

		// Flash sale STILL uses ORDER_FLASH_PAYMENT_EXPIRY_MINUTES (5m < 9m)!
		h.promo.flashSales = []*promotionv1.FlashSale{
			{
				Id:       "fs-1",
				StartsAt: timestamppb.New(baseNow.Add(-time.Minute)),
				EndsAt:   timestamppb.New(baseNow.Add(time.Hour)),
				Items: []*promotionv1.FlashSaleItem{
					{
						Id:            "fsi-1",
						SkuId:         "sku-1",
						ShopId:        "shop-1",
						FlashPriceVnd: 800000,
						Status:        promotionv1.FlashSaleItemStatus_FLASH_SALE_ITEM_STATUS_APPROVED,
					},
				},
			},
		}
		h.promo.breakdown = &promotionv1.DiscountBreakdown{
			TotalFlashSaleDiscountVnd: 200000,
			Shops:                     []*promotionv1.ShopDiscountBreakdown{{ShopId: "shop-1", FlashSaleDiscountVnd: 200000}},
		}
		respFlash, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "f6-exp-flash",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1, FlashSaleItemId: "fsi-1"}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout(flash): %v", err)
		}
		if got, want := respFlash.PaymentExpiresAt.AsTime(), baseNow.Add(5*time.Minute); !got.Equal(want) {
			t.Fatalf("flash sale expiry = %v, want %v (ORDER_FLASH_PAYMENT_EXPIRY_MINUTES)", got, want)
		}

		// Invalid online expiry_minutes <= 0 fails closed (never guesses 15m).
		h.methodConfigs.SetConfig(PaymentMethodConfig{
			Method:        "VIETQR",
			Enabled:       true,
			ExpiryMinutes: 0,
		})
		_, err = h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "f6-exp-zero",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "PAYMENT_METHOD_CONFIG_INVALID") {
			t.Fatalf("zero online expiry must fail closed, got %v", err)
		}
	})
}

// TestW3B2_F12_ShopStatusVerification verifies F12:
//   - Parallel User.GetShop per shop (errgroup + timeout + circuit breaker), accepting ONLY ACTIVE.
//   - Suspended shop -> FailedPrecondition SHOP_SUSPENDED BEFORE stock/promo reservation.
//   - User unavailable / timeout / open circuit breaker -> Unavailable (fail closed).
//   - Idempotent replay of completed checkout NOT blocked if shop is later suspended.
//   - Existing orders of suspended shop continue lifecycle normally.
//   - RetryPayment blocks if shop suspended mid-way before creating a replacement payment.
func TestW3B2_F12_ShopStatusVerification(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

	t.Run("ParallelGetShopAcrossShops_AndSuspendedShopRejectedBeforeReserves", func(t *testing.T) {
		h := newTestHarness()
		// Prove shop-1 and shop-2 are fetched in parallel via errgroup using a 2-goroutine barrier.
		var entered atomic.Int32
		bothEntered := make(chan struct{})
		h.user.onGetShop = func(ctx context.Context, req *userv1.GetShopRequest) (*userv1.GetShopResponse, error) {
			if entered.Add(1) == 2 {
				close(bothEntered)
			}
			select {
			case <-bothEntered:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			h.user.mu.Lock()
			s := h.user.shops[req.GetShopId()]
			h.user.mu.Unlock()
			return &userv1.GetShopResponse{Shop: s}, nil
		}

		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "f12-parallel-ok",
			AddressId:      "addr-buyer-1",
			Items: []*orderv1.CheckoutItemInput{
				{SkuId: "sku-1", Quantity: 1},
				{SkuId: "sku-2", Quantity: 1},
			},
			PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil || len(resp.Orders) != 2 {
			t.Fatalf("parallel multi-shop checkout failed: resp=%+v err=%v", resp, err)
		}
		// Must not call GetShop twice per shop (reused by calculateShippingFees).
		if got := len(h.user.getShopCalls); got != 2 {
			t.Fatalf("expected exactly 2 GetShop calls (1 per shop), got %d (%v)", got, h.user.getShopCalls)
		}

		// Suspend shop-2 -> multi-shop checkout fails with SHOP_SUSPENDED BEFORE reserving stock or promo!
		h.user.onGetShop = nil
		h.user.shops["shop-2"].Status = userv1.ShopStatus_SHOP_STATUS_SUSPENDED
		beforeStock := len(h.product.reserveCalls)
		beforePromo := len(h.promo.reserveCalls)
		_, err = h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "f12-shop2-suspended",
			AddressId:         "addr-buyer-1",
			PlatformVoucherId: "v-plat",
			Items: []*orderv1.CheckoutItemInput{
				{SkuId: "sku-1", Quantity: 1},
				{SkuId: "sku-2", Quantity: 1},
			},
			PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "SHOP_SUSPENDED") {
			t.Fatalf("F12 violation: suspended shop got %v, want FailedPrecondition SHOP_SUSPENDED", err)
		}
		if len(h.product.reserveCalls) != beforeStock || len(h.promo.reserveCalls) != beforePromo {
			t.Fatalf("F12 violation: must NOT reserve stock/promo when shop is suspended")
		}

		// Strictly ONLY ACTIVE is accepted: PENDING / REJECTED / UNSPECIFIED must all reject!
		for _, st := range []userv1.ShopStatus{
			userv1.ShopStatus_SHOP_STATUS_PENDING,
			userv1.ShopStatus_SHOP_STATUS_REJECTED,
			userv1.ShopStatus_SHOP_STATUS_UNSPECIFIED,
		} {
			h.user.shops["shop-1"].Status = st
			_, err = h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
				IdempotencyKey: "f12-non-active-" + st.String(),
				AddressId:      "addr-buyer-1",
				Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
				PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
			})
			if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "SHOP_NOT_ACTIVE") {
				t.Fatalf("F12 violation: shop status %s got %v, want FailedPrecondition SHOP_NOT_ACTIVE", st, err)
			}
		}
	})

	t.Run("UserUnavailableTimeoutAndCircuitBreaker_FailClosedUnavailable", func(t *testing.T) {
		h := newTestHarness()
		h.svc.cfg.GRPCCallTimeout = 40 * time.Millisecond
		h.svc.SetUserShopBreaker(resilience.NewCircuitBreaker(2, time.Minute))

		var shopCalls atomic.Int32
		h.user.onGetShop = func(ctx context.Context, _ *userv1.GetShopRequest) (*userv1.GetShopResponse, error) {
			shopCalls.Add(1)
			<-ctx.Done()
			return nil, ctx.Err()
		}

		// First checkout times out (2 attempts -> trips circuit breaker with maxFailures=2) and fails closed Unavailable.
		_, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "f12-timeout-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if status.Code(err) != codes.Unavailable {
			t.Fatalf("F12 violation: GetShop timeout got %v, want Unavailable", err)
		}
		if len(h.product.reserveCalls) != 0 {
			t.Fatalf("must not reserve stock when GetShop times out")
		}
		callsAfterTrip := shopCalls.Load()
		if callsAfterTrip != 2 {
			t.Fatalf("expected 2 retry attempts to trip breaker(2), got %d", callsAfterTrip)
		}

		// Second checkout hits OPEN circuit breaker -> immediate Unavailable without touching UserService!
		_, err = h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "f12-cb-open",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if status.Code(err) != codes.Unavailable {
			t.Fatalf("F12 violation: open breaker got %v, want Unavailable", err)
		}
		if shopCalls.Load() != callsAfterTrip {
			t.Fatalf("open breaker must short-circuit without calling User.GetShop (%d -> %d)", callsAfterTrip, shopCalls.Load())
		}
	})

	t.Run("IdempotentReplayAndExistingOrdersUnaffectedAfterSuspension_RetryPaymentBlocked", func(t *testing.T) {
		h := newTestHarness()
		// 1. Complete checkout while shop-1 is ACTIVE.
		coResp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "f12-replay-key",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil || len(coResp.Orders) != 1 {
			t.Fatalf("CreateCheckout: %v", err)
		}
		ordID := coResp.Orders[0].Id

		// 2. Suspend shop-1 afterwards!
		h.user.shops["shop-1"].Status = userv1.ShopStatus_SHOP_STATUS_SUSPENDED
		callsBeforeReplay := len(h.user.getShopCalls)

		// 3. Idempotent replay of the already-completed checkout MUST STILL SUCCEED!
		replay, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "f12-replay-key",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil || !replay.IdempotentReplay || replay.CheckoutGroupId != coResp.CheckoutGroupId {
			t.Fatalf("F12 violation: idempotent replay after suspension failed: resp=%+v err=%v", replay, err)
		}
		if len(h.user.getShopCalls) != callsBeforeReplay {
			t.Fatalf("idempotent replay must not call User.GetShop")
		}

		// 4. Attack: RetryPayment while shop-1 is suspended (online retry AND switch to COD) MUST be rejected!
		payCallsBefore := len(h.payment.createCalls)
		for _, pm := range []orderv1.OrderPaymentMethod{
			orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
			orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		} {
			_, rErr := h.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
				PaymentId:      coResp.PaymentId,
				PaymentMethod:  pm,
				IdempotencyKey: "retry-susp-" + pm.String(),
			})
			if status.Code(rErr) != codes.FailedPrecondition || !strings.Contains(rErr.Error(), "SHOP_SUSPENDED") {
				t.Fatalf("F12 attack violation: RetryPayment(%v) on suspended shop got %v, want FailedPrecondition SHOP_SUSPENDED", pm, rErr)
			}
		}
		if len(h.payment.createCalls) != payCallsBefore {
			t.Fatalf("RetryPayment on suspended shop must not call CreatePayment")
		}

		// 5. Existing payment succeeding via IPN & seller fulfilling existing order MUST still work normally!
		if err := h.svc.HandlePaymentSucceeded(context.Background(), "evt-susp-paid", events.PaymentSucceededPayload{
			PaymentID: coResp.PaymentId, OrderGroupID: coResp.CheckoutGroupId,
		}); err != nil {
			t.Fatalf("HandlePaymentSucceeded for existing order of suspended shop failed: %v", err)
		}
		if _, err := h.svc.SellerConfirmOrder(sellerCtx, &orderv1.SellerConfirmOrderRequest{OrderId: ordID, ShopId: "shop-1"}); err != nil {
			t.Fatalf("SellerConfirmOrder on existing order of suspended shop failed: %v", err)
		}
		if _, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID, ShopId: "shop-1"}); err != nil {
			t.Fatalf("SellerShipOrder on existing order of suspended shop failed: %v", err)
		}
	})
}

// TestW3B2_F5_PlanRetryPaymentSwitch_Pure exercises the pure F5 switch planner across all method pairs
// (including string-based ZALOPAY before Phase 2 proto wiring).
func TestW3B2_F5_PlanRetryPaymentSwitch_Pure(t *testing.T) {
	baseNow := time.Date(2026, 11, 24, 14, 0, 0, 0, time.UTC)
	origExp := baseNow.Add(10 * time.Minute)
	now := baseNow.Add(4 * time.Minute)

	makeAgg := func(id, shopID, method string, subtotal, commVND, ship, totalPayable int64, flashID string) store.OrderAggregate {
		exp := origExp
		return store.OrderAggregate{
			Order: db.Order{
				ID:               id,
				CheckoutGroupID:  "grp-pure-1",
				BuyerID:          "buyer-1",
				ShopID:           shopID,
				Status:           "PENDING",
				PaymentMethod:    method,
				PaymentID:        "pay-old",
				PaymentStatus:    "PENDING",
				SubtotalVND:      subtotal,
				ShippingFeeVND:   ship,
				CommissionVND:    commVND,
				PaymentFeeVND:    10000,
				PaymentFeeBPS:    100,
				TotalPayableVND:  totalPayable,
				PaymentExpiresAt: &exp,
			},
			Items: []db.OrderItem{
				{ID: "it-" + id, OrderID: id, SKUID: "sku-" + shopID, Quantity: 1, FlashSaleItemID: flashID},
			},
		}
	}

	grp := db.CheckoutGroup{
		ID:               "grp-pure-1",
		BuyerID:          "buyer-1",
		PaymentMethod:    "VIETQR",
		PaymentID:        "pay-old",
		PaymentExpiresAt: &origExp,
	}

	t.Run("VIETQR_To_ZALOPAY_KeepsOriginalExpiryRecalculatesFeeAndCapsAtSellerRevenue", func(t *testing.T) {
		aggs := []store.OrderAggregate{
			// Normal order: R=1,000,000, C_cat=50,000, Ship=30,000, P=1,030,000. At 250 bps -> raw C_pay = 25,750.
			makeAgg("ord-1", "shop-1", "VIETQR", 1000000, 50000, 30000, 1030000, ""),
			// Tight-margin order: R=100,000, C_cat=99,000, Ship=30,000, P=130,000. At 250 bps -> raw 3,250, CAPPED at R - C_cat = 1,000!
			makeAgg("ord-2", "shop-2", "VIETQR", 100000, 99000, 30000, 130000, ""),
		}
		plan, err := PlanRetryPaymentSwitch(RetrySwitchInput{
			Group:           grp,
			Aggs:            aggs,
			ExpectedPayID:   "pay-old",
			RequestedMethod: "ZALOPAY",
			MethodConfig:    &PaymentMethodConfig{Method: "ZALOPAY", Enabled: true, ExpiryMinutes: 30},
			PaymentFeeBPS:   250,
			Now:             now,
		})
		if err != nil {
			t.Fatalf("PlanRetryPaymentSwitch(VIETQR->ZALOPAY): %v", err)
		}
		if plan.TargetMethod != "ZALOPAY" || plan.SwitchedToCOD {
			t.Fatalf("unexpected target: method=%q cod=%v", plan.TargetMethod, plan.SwitchedToCOD)
		}
		// Original expiry strictly preserved (NOT now + 30m!).
		if plan.ExpiresAt == nil || !plan.ExpiresAt.Equal(origExp) {
			t.Fatalf("F5 expiry violation: got %v, want original %v", plan.ExpiresAt, origExp)
		}
		// Buyer grand total unchanged.
		if plan.TotalPayableVND != 1160000 {
			t.Fatalf("total payable changed: got %d, want 1160000", plan.TotalPayableVND)
		}
		if plan.OrderFees[0].PaymentFeeVND != 25750 || plan.OrderFees[0].PaymentFeeBPS != 250 || plan.OrderFees[0].TotalPayableVND != 1030000 {
			t.Fatalf("ord-1 fee mismatch: %+v", plan.OrderFees[0])
		}
		if plan.OrderFees[1].PaymentFeeVND != 1000 || plan.ShopAllocations[1].CommissionVnd != 100000 {
			t.Fatalf("ord-2 C_cat+C_pay <= R cap violated: fee=%+v alloc=%+v", plan.OrderFees[1], plan.ShopAllocations[1])
		}
		if plan.ShopAllocations[0].CommissionVnd != 50000+25750 {
			t.Fatalf("ord-1 allocation commission_vnd = %d, want %d", plan.ShopAllocations[0].CommissionVnd, 75750)
		}
	})

	t.Run("ZALOPAY_To_COD_AndFlashSaleBlocked", func(t *testing.T) {
		zaloGrp := grp
		zaloGrp.PaymentMethod = "ZALOPAY"

		// Regular ZALOPAY -> COD succeeds.
		plan, err := PlanRetryPaymentSwitch(RetrySwitchInput{
			Group:           zaloGrp,
			Aggs:            []store.OrderAggregate{makeAgg("ord-z1", "shop-1", "ZALOPAY", 1000000, 50000, 30000, 1030000, "")},
			ExpectedPayID:   "pay-old",
			RequestedMethod: "COD",
			MethodConfig:    &PaymentMethodConfig{Method: "COD", Enabled: true, MaxOrderVND: 5000000},
			PaymentFeeBPS:   50,
			Now:             now,
		})
		if err != nil {
			t.Fatalf("ZALOPAY->COD failed: %v", err)
		}
		if !plan.SwitchedToCOD || plan.TargetMethod != "COD" || plan.ExpiresAt != nil {
			t.Fatalf("unexpected COD plan: %+v", plan)
		}
		if plan.OrderFees[0].PaymentFeeVND != (1030000*50)/10000 || plan.OrderFees[0].TotalPayableVND != 1030000 {
			t.Fatalf("COD recalculated fee mismatch: %+v", plan.OrderFees[0])
		}

		// Flash sale ZALOPAY -> COD MUST be blocked!
		_, err = PlanRetryPaymentSwitch(RetrySwitchInput{
			Group:           zaloGrp,
			Aggs:            []store.OrderAggregate{makeAgg("ord-zflash", "shop-1", "ZALOPAY", 1000000, 50000, 30000, 1030000, "fsi-1")},
			ExpectedPayID:   "pay-old",
			RequestedMethod: "COD",
			MethodConfig:    &PaymentMethodConfig{Method: "COD", Enabled: true, MaxOrderVND: 5000000},
			PaymentFeeBPS:   50,
			Now:             now,
		})
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "FLASH_SALE_COD_NOT_ALLOWED") {
			t.Fatalf("flash ZALOPAY->COD got %v, want FLASH_SALE_COD_NOT_ALLOWED", err)
		}

		// Switching to disabled method rejected.
		_, err = PlanRetryPaymentSwitch(RetrySwitchInput{
			Group:           grp,
			Aggs:            []store.OrderAggregate{makeAgg("ord-dis", "shop-1", "VIETQR", 1000000, 50000, 30000, 1030000, "")},
			ExpectedPayID:   "pay-old",
			RequestedMethod: "ZALOPAY",
			MethodConfig:    &PaymentMethodConfig{Method: "ZALOPAY", Enabled: false, ExpiryMinutes: 15},
			PaymentFeeBPS:   100,
			Now:             now,
		})
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "PAYMENT_METHOD_DISABLED") {
			t.Fatalf("disabled target got %v, want PAYMENT_METHOD_DISABLED", err)
		}

		// Expired original window rejected even for ZALOPAY switch.
		_, err = PlanRetryPaymentSwitch(RetrySwitchInput{
			Group:           grp,
			Aggs:            []store.OrderAggregate{makeAgg("ord-exp", "shop-1", "VIETQR", 1000000, 50000, 30000, 1030000, "")},
			ExpectedPayID:   "pay-old",
			RequestedMethod: "ZALOPAY",
			MethodConfig:    &PaymentMethodConfig{Method: "ZALOPAY", Enabled: true, ExpiryMinutes: 15},
			PaymentFeeBPS:   100,
			Now:             origExp,
		})
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("expired window got %v, want FailedPrecondition", err)
		}
	})
}

// TestW3B2_F5_RetryPayment_SwitchAndAttacks verifies F5 + E19 + F6 + F7 through OrderService.RetryPayment
// plus string-based ZALOPAY online method parity across cancel/return/expiry handlers.
func TestW3B2_F5_RetryPayment_SwitchAndAttacks(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")

	t.Run("VIETQR_To_COD_MultiShop_ConfirmsOrdersRecalculatesFeesConfirmsStockEmitsOutbox", func(t *testing.T) {
		h := newTestHarness()
		baseNow := time.Date(2026, 11, 24, 15, 0, 0, 0, time.UTC)
		h.svc.nowFn = func() time.Time { return baseNow }
		h.payment.feeBPSByMethod = map[paymentv1.PaymentMethod]int32{
			paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR: 100, // 1.0%
			paymentv1.PaymentMethod_PAYMENT_METHOD_COD:    250, // 2.5%
		}

		coResp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "f5-vqr-to-cod-multi",
			AddressId:      "addr-buyer-1",
			FromCart:       true, // shop-1 (1,000,000) + shop-2 (2*500,000 = 1,000,000)
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil || len(coResp.Orders) != 2 {
			t.Fatalf("CreateCheckout multi-shop VIETQR: %v", err)
		}
		origPayID := coResp.PaymentId
		origTotal := coResp.TotalPayableVnd
		origOrderTotals := map[string]int64{}
		for _, o := range coResp.Orders {
			origOrderTotals[o.Id] = o.TotalPayableVnd
		}

		// Switch VIETQR -> COD 5 minutes later.
		h.svc.nowFn = func() time.Time { return baseNow.Add(5 * time.Minute) }
		retryResp, err := h.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
			PaymentId:      origPayID,
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
			IdempotencyKey: "switch-cod-k1",
		})
		if err != nil {
			t.Fatalf("RetryPayment(VIETQR->COD) failed: %v", err)
		}
		if retryResp.PaymentId == "" || retryResp.PaymentId == origPayID {
			t.Fatalf("expected new superseding COD payment_id, got %q (old %q)", retryResp.PaymentId, origPayID)
		}
		if retryResp.PaymentExpiresAt != nil {
			t.Fatalf("COD switch must clear PaymentExpiresAt, got %v", retryResp.PaymentExpiresAt)
		}

		// Verify Payment.CreatePayment received Method=COD, SupersedePrevious=true, and recalculated 250 bps fees.
		lastPayReq := h.payment.createCalls[len(h.payment.createCalls)-1]
		if lastPayReq.Method != paymentv1.PaymentMethod_PAYMENT_METHOD_COD || !lastPayReq.SupersedePrevious || lastPayReq.AmountVnd != origTotal {
			t.Fatalf("unexpected CreatePayment req on COD switch: %+v", lastPayReq)
		}
		if len(h.product.confirmCalls) != 1 || h.product.confirmCalls[0] != coResp.CheckoutGroupId {
			t.Fatalf("COD switch MUST call Product.ConfirmStock once for group %s, got %v", coResp.CheckoutGroupId, h.product.confirmCalls)
		}

		// Verify every sub-order is now CONFIRMED, COD, nil expiry, recalculated fee (250 bps), unchanged TotalPayableVND!
		seller1Ctx := ctxWithUser("seller-1", "seller", "shop-1")
		for _, o := range coResp.Orders {
			viewCtx := seller1Ctx
			if o.ShopId == "shop-2" {
				viewCtx = ctxWithUser("seller-2", "seller", "shop-2")
			}
			got, gErr := h.svc.GetOrder(viewCtx, &orderv1.GetOrderRequest{OrderId: o.Id})
			if gErr != nil {
				t.Fatalf("GetOrder(%s): %v", o.Id, gErr)
			}
			wantPayFee := (origOrderTotals[o.Id] * 250) / 10000
			if got.Order.Status != orderv1.OrderStatus_ORDER_STATUS_CONFIRMED ||
				got.Order.PaymentMethod != orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD ||
				got.Order.PaymentId != retryResp.PaymentId ||
				got.Order.PaymentExpiresAt != nil ||
				got.Order.TotalPayableVnd != origOrderTotals[o.Id] ||
				got.Order.PaymentFeeBps != 250 ||
				got.Order.PaymentFeeVnd != wantPayFee {
				t.Fatalf("sub-order %s after COD switch mismatch: status=%v method=%v payID=%q exp=%v total=%d feeBPS=%d feeVND=%d (want %d)",
					o.Id, got.Order.Status, got.Order.PaymentMethod, got.Order.PaymentId, got.Order.PaymentExpiresAt,
					got.Order.TotalPayableVnd, got.Order.PaymentFeeBps, got.Order.PaymentFeeVnd, wantPayFee)
			}
		}

		// Verify order.confirmed outbox events were emitted for both orders with payment_method=COD.
		confirmedEvents := 0
		for _, ev := range h.store.OutboxEvents() {
			if ev.EventType == events.SubjectOrderConfirmed {
				var p events.OrderConfirmedPayload
				if err := json.Unmarshal(ev.PayloadJSON, &p); err == nil && p.PaymentMethod == "COD" {
					confirmedEvents++
				}
			}
		}
		if confirmedEvents != 2 {
			t.Fatalf("expected 2 order.confirmed COD outbox events, got %d", confirmedEvents)
		}

		// Sequential idempotent replay of the COD switch succeeds without creating another payment.
		payCallsBeforeReplay := len(h.payment.createCalls)
		replayResp, err := h.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
			PaymentId:      origPayID,
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
			IdempotencyKey: "switch-cod-k1",
		})
		if err != nil || replayResp.PaymentId != retryResp.PaymentId {
			t.Fatalf("COD switch idempotent replay failed: resp=%+v err=%v", replayResp, err)
		}
		if len(h.payment.createCalls) != payCallsBeforeReplay {
			t.Fatalf("COD switch replay must not call CreatePayment again")
		}

		// Late payment.succeeded for the OLD superseded VietQR payment MUST be ignored (no double confirm).
		if err := h.svc.HandlePaymentSucceeded(context.Background(), "evt-late-old-vqr", events.PaymentSucceededPayload{
			PaymentID: origPayID, OrderGroupID: coResp.CheckoutGroupId,
		}); err != nil {
			t.Fatalf("HandlePaymentSucceeded(old pay): %v", err)
		}
		grpAfter, _ := h.store.GetCheckoutGroupByID(context.Background(), coResp.CheckoutGroupId)
		if grpAfter.PaymentID != retryResp.PaymentId || grpAfter.PaymentMethod != "COD" || grpAfter.PaymentStatus != "PENDING" {
			t.Fatalf("late old VietQR payment.succeeded mutated COD group: %+v", grpAfter)
		}
	})

	t.Run("Attack_FlashSaleOnlineThenSwitchToCOD_Rejected", func(t *testing.T) {
		h := newTestHarness()
		baseNow := time.Date(2026, 11, 24, 16, 0, 0, 0, time.UTC)
		h.svc.nowFn = func() time.Time { return baseNow }
		h.promo.flashSales = []*promotionv1.FlashSale{
			{
				Id:       "fs-atk",
				StartsAt: timestamppb.New(baseNow.Add(-time.Minute)),
				EndsAt:   timestamppb.New(baseNow.Add(time.Hour)),
				Items: []*promotionv1.FlashSaleItem{
					{Id: "fsi-atk", SkuId: "sku-1", ShopId: "shop-1", FlashPriceVnd: 750000, Status: promotionv1.FlashSaleItemStatus_FLASH_SALE_ITEM_STATUS_APPROVED},
				},
			},
		}
		h.promo.breakdown = &promotionv1.DiscountBreakdown{
			TotalFlashSaleDiscountVnd: 250000,
			Shops:                     []*promotionv1.ShopDiscountBreakdown{{ShopId: "shop-1", FlashSaleDiscountVnd: 250000}},
		}

		coResp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "f5-atk-flash-vqr",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1, FlashSaleItemId: "fsi-atk"}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout flash VIETQR: %v", err)
		}
		payCallsBefore := len(h.payment.createCalls)

		// Attacker tries to bypass Flash Sale no-COD rule via RetryPayment(COD)!
		_, err = h.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
			PaymentId:      coResp.PaymentId,
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
			IdempotencyKey: "atk-flash-cod",
		})
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "FLASH_SALE_COD_NOT_ALLOWED") {
			t.Fatalf("F5 attack violation: flash VIETQR->COD got %v, want FailedPrecondition FLASH_SALE_COD_NOT_ALLOWED", err)
		}
		if len(h.payment.createCalls) != payCallsBefore || len(h.product.confirmCalls) != 0 {
			t.Fatalf("flash->COD attack must not create payment or confirm stock")
		}
		got, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: coResp.Orders[0].Id})
		if got.Order.Status != orderv1.OrderStatus_ORDER_STATUS_PENDING || got.Order.PaymentMethod != orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR {
			t.Fatalf("flash order mutated after rejected switch: status=%v method=%v", got.Order.Status, got.Order.PaymentMethod)
		}
	})

	t.Run("Attack_SwitchToDisabledMethodRejected_ExistingOrderIPNStillSucceeds", func(t *testing.T) {
		h := newTestHarness()
		coResp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "f5-atk-disabled",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}

		// Admin disables COD AND VIETQR after checkout.
		h.methodConfigs.SetConfig(PaymentMethodConfig{Method: "COD", Enabled: false, MaxOrderVND: 5000000})
		h.methodConfigs.SetConfig(PaymentMethodConfig{Method: "VIETQR", Enabled: false, ExpiryMinutes: 15})

		for _, target := range []orderv1.OrderPaymentMethod{
			orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
			orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		} {
			_, rErr := h.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
				PaymentId:      coResp.PaymentId,
				PaymentMethod:  target,
				IdempotencyKey: "atk-dis-" + target.String(),
			})
			if status.Code(rErr) != codes.FailedPrecondition || !strings.Contains(rErr.Error(), "PAYMENT_METHOD_DISABLED") {
				t.Fatalf("F7 attack violation: RetryPayment(%v) disabled got %v, want PAYMENT_METHOD_DISABLED", target, rErr)
			}
		}

		// Existing VietQR payment STILL succeeds when IPN arrives (F7)!
		if err := h.svc.HandlePaymentSucceeded(context.Background(), "evt-f7-ok", events.PaymentSucceededPayload{
			PaymentID: coResp.PaymentId, OrderGroupID: coResp.CheckoutGroupId,
		}); err != nil {
			t.Fatalf("F7 violation: IPN for pre-existing payment failed after method disabled: %v", err)
		}
		got, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: coResp.Orders[0].Id})
		if got.Order.Status != orderv1.OrderStatus_ORDER_STATUS_CONFIRMED {
			t.Fatalf("expected CONFIRMED after IPN, got %v", got.Order.Status)
		}
	})

	t.Run("FullE19AssessCOD_OnRetrySwitchToCOD", func(t *testing.T) {
		runSwitch := func(t *testing.T, mutate func(h *testHarness)) error {
			t.Helper()
			h := newTestHarness()
			coResp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
				IdempotencyKey: "f5-e19-" + t.Name(),
				AddressId:      "addr-buyer-1",
				Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}}, // 1,030,000 VND
				PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
			})
			if err != nil {
				t.Fatalf("CreateCheckout: %v", err)
			}
			mutate(h)
			_, err = h.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
				PaymentId:      coResp.PaymentId,
				PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
				IdempotencyKey: "switch-e19-" + t.Name(),
			})
			return err
		}

		if err := runSwitch(t, func(h *testHarness) { h.user.phoneVerified = false }); status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "COD_PHONE_NOT_VERIFIED") {
			t.Fatalf("unverified phone -> got %v, want COD_PHONE_NOT_VERIFIED", err)
		}
		if err := runSwitch(t, func(h *testHarness) {
			h.shipping.quotes = []*shippingv1.CarrierQuote{{CarrierCode: "ghn", ServiceCode: "standard", FeeVnd: 30000, CodSupported: false}}
		}); status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "COD_AREA_NOT_SUPPORTED") {
			t.Fatalf("unsupported COD area -> got %v, want COD_AREA_NOT_SUPPORTED", err)
		}
		if err := runSwitch(t, func(h *testHarness) { h.user.codRejectedCount = 3 }); status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "COD_REJECTED_LIMIT_EXCEEDED") {
			t.Fatalf("rejected count >= 3 -> got %v, want COD_REJECTED_LIMIT_EXCEEDED", err)
		}
		if err := runSwitch(t, func(h *testHarness) {
			seedCODHistoryOrder(t, h.store, "buyer-1", "r-s1", "COMPLETED")
			seedCODHistoryOrder(t, h.store, "buyer-1", "r-s2", "COMPLETED")
			seedCODHistoryOrder(t, h.store, "buyer-1", "r-s3", "RETURNED_REFUSED") // 1/3 = 33.3% >= 30%
		}); status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "COD_REJECTED_LIMIT_EXCEEDED") {
			t.Fatalf("reject rate >= 30%% -> got %v, want COD_REJECTED_LIMIT_EXCEEDED", err)
		}
		if err := runSwitch(t, func(h *testHarness) {
			h.methodConfigs.SetConfig(PaymentMethodConfig{Method: "COD", Enabled: true, MaxOrderVND: 500000})
		}); status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "COD_LIMIT_EXCEEDED") {
			t.Fatalf("over COD max -> got %v, want COD_LIMIT_EXCEEDED", err)
		}
		if err := runSwitch(t, func(h *testHarness) {
			h.methodConfigs.SetConfig(PaymentMethodConfig{Method: "COD", Enabled: true, MinOrderVND: 2000000, MaxOrderVND: 5000000})
		}); status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "ORDER_AMOUNT_OUT_OF_RANGE") {
			t.Fatalf("under COD min -> got %v, want ORDER_AMOUNT_OUT_OF_RANGE", err)
		}
	})

	t.Run("ZALOPAY_OnlineMethodParity_CancelRefundReturnAndExpiry", func(t *testing.T) {
		h := newTestHarness()
		now := time.Now().UTC()
		expiredAt := now.Add(-2 * time.Minute)
		futureAt := now.Add(10 * time.Minute)

		seedOnlineOrder := func(gid, ordID, statusStr, payStatus string, exp *time.Time) {
			t.Helper()
			if _, err := h.store.CreateCheckoutGroup(context.Background(), db.CreateCheckoutGroupParams{
				ID: gid, BuyerID: "buyer-1", IdempotencyKey: "idem-" + gid, RequestHash: "h-" + gid,
				Status: "IN_PROGRESS", PaymentMethod: statemachine.PaymentMethodZALOPAY, TotalPayableVND: 500000,
			}); err != nil {
				t.Fatalf("CreateCheckoutGroup: %v", err)
			}
			if _, err := h.store.PersistCheckoutBatch(context.Background(), store.CheckoutBatch{
				CheckoutGroupID: gid, Now: now,
				Orders: []store.NewOrderRecord{{
					Order: db.InsertOrderParams{
						ID: ordID, OrderCode: "NM-ZLP-" + ordID, CheckoutGroupID: gid, BuyerID: "buyer-1", ShopID: "shop-1",
						Status: statusStr, PaymentMethod: statemachine.PaymentMethodZALOPAY, PaymentStatus: payStatus,
						SubtotalVND: 470000, ShippingFeeVND: 30000, TotalPayableVND: 500000,
						ShippingAddressJSON: []byte(`{"province_code":"79","ward_code":"26734"}`), PaymentExpiresAt: exp,
					},
					Items: []db.InsertOrderItemParams{{
						ID: "it-" + ordID, OrderID: ordID, SKUID: "sku-1", ProductID: "prod-1",
						ProductName: "KB", SKUName: "Std", UnitPriceVND: 470000, EffectiveUnitPriceVND: 470000, Quantity: 1, LineTotalVND: 470000,
					}},
				}},
			}); err != nil {
				t.Fatalf("PersistCheckoutBatch: %v", err)
			}
			if err := h.store.CompleteCheckoutWithPayment(context.Background(), store.UpdateCheckoutPaymentParams{
				CheckoutGroupID: gid, PaymentID: "pay-" + gid, PaymentStatus: payStatus, PaymentExpiresAt: exp, PayURL: "https://sb-openapi.zalopay.vn/pay/" + gid,
			}); err != nil {
				t.Fatalf("CompleteCheckoutWithPayment: %v", err)
			}
		}

		// 1. Expired PENDING ZALOPAY order is picked up and cancelled by ScanExpiredPendingOrders!
		seedOnlineOrder("grp-zlp-exp", "ord-zlp-exp", "PENDING", "PENDING", &expiredAt)
		handled, err := h.svc.ScanExpiredPendingOrders(context.Background())
		if err != nil || handled != 1 {
			t.Fatalf("ScanExpiredPendingOrders(ZALOPAY) = %d, %v; want 1, nil", handled, err)
		}
		aggExp, _ := h.store.GetOrderWithDetails(context.Background(), "ord-zlp-exp")
		if aggExp.Order.Status != "CANCELLED" || aggExp.Order.PaymentStatus != "EXPIRED" {
			t.Fatalf("expired ZALOPAY order = status=%s payStatus=%s, want CANCELLED/EXPIRED", aggExp.Order.Status, aggExp.Order.PaymentStatus)
		}

		// 2. Paid ZALOPAY CancelOrder sets was_paid=true and triggers CreateRefund!
		seedOnlineOrder("grp-zlp-paid-cancel", "ord-zlp-paid-cancel", "CONFIRMED", "SUCCEEDED", &futureAt)
		beforeRefunds := len(h.payment.refundCalls)
		if _, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: "ord-zlp-paid-cancel", Reason: "buyer cancel"}); err != nil {
			t.Fatalf("CancelOrder paid ZALOPAY: %v", err)
		}
		if len(h.payment.refundCalls) != beforeRefunds+1 || h.payment.refundCalls[beforeRefunds].OrderId != "ord-zlp-paid-cancel" {
			t.Fatalf("CancelOrder paid ZALOPAY must call CreateRefund, got %+v", h.payment.refundCalls)
		}

		// 3. Paid ZALOPAY SHIPPING -> RETURNED triggers CreateRefund and NEVER RecordCODRejection!
		seedOnlineOrder("grp-zlp-ret", "ord-zlp-ret", "SHIPPING", "SUCCEEDED", &futureAt)
		beforeRefunds = len(h.payment.refundCalls)
		beforeCODRej := len(h.user.codRejectionCalls)
		if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-zlp-ret", events.ShipmentUpdatedPayload{
			OrderID: "ord-zlp-ret", Status: "returned", Direction: "FORWARD", Description: "undeliverable",
		}); err != nil {
			t.Fatalf("HandleShipmentUpdated(returned ZALOPAY): %v", err)
		}
		if len(h.payment.refundCalls) != beforeRefunds+1 {
			t.Fatalf("returned paid ZALOPAY must call CreateRefund")
		}
		if len(h.user.codRejectionCalls) != beforeCODRej {
			t.Fatalf("returned ZALOPAY must NOT call RecordCODRejection")
		}
	})
}

// TestW3B2_F5_Postgres_Races exercises real-PostgreSQL concurrency races under -race:
//  1. Concurrent switch-vs-switch across two independent OrderService pods (pg_advisory_xact_lock).
//  2. Concurrent switch-to-COD vs payment expiry cancellation (FOR UPDATE).
func TestW3B2_F5_Postgres_Races(t *testing.T) {
	pgStore, _ := openTempPostgresStoreForService(t)
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")

	t.Run("SwitchVsSwitch_TwoPods_RealPostgres_SingleWinner", func(t *testing.T) {
		hBase := newTestHarness()
		payClient := newSupersedingPaymentClient()
		bundle := &clients.Bundle{
			User:          hBase.user,
			Product:       hBase.product,
			Cart:          hBase.cart,
			Promotion:     hBase.promo,
			Payment:       payClient,
			Shipping:      hBase.shipping,
			MethodConfigs: hBase.methodConfigs,
		}
		pod1 := NewOrderService(hBase.svc.cfg, pgStore, bundle, logger.New("pg-pod1", logger.WithWriter(io.Discard)))
		pod2 := NewOrderService(hBase.svc.cfg, pgStore, bundle, logger.New("pg-pod2", logger.WithWriter(io.Discard)))

		coResp, err := pod1.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "pg-f5-switch-vs-switch",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}

		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		var (
			r1, r2 *orderv1.RetryPaymentResponse
			e1, e2 error
		)
		go func() {
			defer wg.Done()
			<-start
			r1, e1 = pod1.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
				PaymentId:      coResp.PaymentId,
				PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
				IdempotencyKey: "pg-k1-cod",
			})
		}()
		go func() {
			defer wg.Done()
			<-start
			r2, e2 = pod2.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
				PaymentId:      coResp.PaymentId,
				PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
				IdempotencyKey: "pg-k2-vqr",
			})
		}()
		close(start)
		wg.Wait()

		okCount := 0
		if e1 == nil && r1 != nil {
			okCount++
		}
		if e2 == nil && r2 != nil {
			okCount++
		}
		if okCount != 1 {
			t.Fatalf("F5 race violation: expected strictly 1 winner, got okCount=%d (r1=%+v e1=%v | r2=%+v e2=%v)", okCount, r1, e1, r2, e2)
		}
		loserErr := e1
		if e1 == nil {
			loserErr = e2
		}
		if status.Code(loserErr) != codes.FailedPrecondition {
			t.Fatalf("loser must receive FailedPrecondition, got %v", loserErr)
		}

		grp, err := pgStore.GetCheckoutGroupByID(context.Background(), coResp.CheckoutGroupId)
		if err != nil {
			t.Fatalf("GetCheckoutGroupByID: %v", err)
		}
		aggs, err := pgStore.ListOrdersByCheckoutGroup(context.Background(), coResp.CheckoutGroupId)
		if err != nil || len(aggs) != 1 {
			t.Fatalf("ListOrdersByCheckoutGroup: %v", err)
		}
		if e1 == nil {
			// COD switch won.
			if grp.PaymentMethod != "COD" || aggs[0].Order.Status != "CONFIRMED" || aggs[0].Order.PaymentMethod != "COD" || aggs[0].Order.PaymentID != r1.PaymentId {
				t.Fatalf("inconsistent DB state after COD win: grp=%+v ord=%+v", grp, aggs[0].Order)
			}
		} else {
			// VIETQR retry won.
			if grp.PaymentMethod != "VIETQR" || aggs[0].Order.Status != "PENDING" || aggs[0].Order.PaymentMethod != "VIETQR" || aggs[0].Order.PaymentID != r2.PaymentId {
				t.Fatalf("inconsistent DB state after VIETQR win: grp=%+v ord=%+v", grp, aggs[0].Order)
			}
		}
	})

	t.Run("SwitchToCOD_Vs_ExpiryCancel_RealPostgres_AtomicSingleOutcome", func(t *testing.T) {
		for iter := 0; iter < 6; iter++ {
			h := newTestHarness()
			svc := NewOrderService(h.svc.cfg, pgStore, h.svc.clients, logger.New("pg-f5-exp", logger.WithWriter(io.Discard)))

			coResp, err := svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
				IdempotencyKey: fmt.Sprintf("pg-f5-exp-race-%d", iter),
				AddressId:      "addr-buyer-1",
				Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
				PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
			})
			if err != nil {
				t.Fatalf("iter %d CreateCheckout: %v", iter, err)
			}
			origPayID := coResp.PaymentId

			start := make(chan struct{})
			var wg sync.WaitGroup
			wg.Add(2)
			var (
				retryResp *orderv1.RetryPaymentResponse
				retryErr  error
				expErr    error
			)
			go func() {
				defer wg.Done()
				<-start
				retryResp, retryErr = svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
					PaymentId:      origPayID,
					PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
					IdempotencyKey: fmt.Sprintf("pg-exp-switch-%d", iter),
				})
			}()
			go func() {
				defer wg.Done()
				<-start
				expErr = svc.HandlePaymentResult(context.Background(), PaymentResultInput{
					CheckoutGroupID: coResp.CheckoutGroupId,
					PaymentID:       origPayID,
					Status:          orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_EXPIRED,
					Reason:          "Payment expired",
					ActorID:         "order-expiry-watchdog",
					ActorRole:       "system",
				})
			}()
			close(start)
			wg.Wait()

			if expErr != nil {
				t.Fatalf("iter %d HandlePaymentResult(EXPIRED): %v", iter, expErr)
			}
			aggs, err := pgStore.ListOrdersByCheckoutGroup(context.Background(), coResp.CheckoutGroupId)
			if err != nil || len(aggs) != 1 {
				t.Fatalf("iter %d ListOrdersByCheckoutGroup: %v", iter, err)
			}
			ord := aggs[0].Order
			switch ord.Status {
			case "CONFIRMED":
				// COD switch won -> retry succeeded, order is COD with new payment, stock confirmed, NOT released!
				if retryErr != nil || retryResp == nil || ord.PaymentMethod != "COD" || ord.PaymentID != retryResp.PaymentId {
					t.Fatalf("iter %d CONFIRMED inconsistent: retryResp=%+v retryErr=%v ord=%+v", iter, retryResp, retryErr, ord)
				}
				if len(h.product.releaseCalls) != 0 {
					t.Fatalf("iter %d F5 race violation: stock was released after COD switch won!", iter)
				}
			case "CANCELLED":
				// Expiry won -> retry MUST have failed FailedPrecondition, and if CreatePayment ran it MUST have been cancelled!
				if status.Code(retryErr) != codes.FailedPrecondition {
					t.Fatalf("iter %d CANCELLED yet retryErr=%v, want FailedPrecondition", iter, retryErr)
				}
				if len(h.payment.createCalls) > 1 && len(h.payment.cancelCalls) == 0 {
					t.Fatalf("iter %d F5 race violation: orphan COD payment created on CANCELLED group without CancelPayment!", iter)
				}
			default:
				t.Fatalf("iter %d unexpected final status %q", iter, ord.Status)
			}
		}
	})
}
