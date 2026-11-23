package service

import (
	"context"
	"strings"
	"testing"
	"time"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/order/internal/clients"
	"github.com/nexusmart/core/services/order/internal/db"
	"github.com/nexusmart/core/services/order/internal/events"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TestW2B_Checkout_AllocationTable exercises the Wave 2b money model across all discount types
// and boundary conditions: shop discount program, shop freeship, negative-R clamp, C_total <= R,
// zero-payable order, multi-shop + default_weight_grams, COD + payment_fee_bps, flash-over-program
// precedence, GetFeeConfig failure, server-side pricing trust, and legacy 0-default rows.
func TestW2B_Checkout_AllocationTable(t *testing.T) {
	baseNow := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)

	t.Run("FullDiscountStack_ShopProgram_ShopFreeship_PaymentFee_CategoryBPS", func(t *testing.T) {
		h := newTestHarness()
		h.svc.nowFn = func() time.Time { return baseNow }
		h.product.skus["sku-1"].CategoryId = "cat-elec"
		h.product.skus["sku-1"].CommissionRateBps = 500 // 5%
		h.shipping.shopSettings = map[string]*shippingv1.ShopShippingSettings{
			"shop-1": {
				ShopId:              "shop-1",
				DefaultWeightGrams:  700,
				FreeShipEnabled:     true,
				FreeShipMinOrderVnd: 500000,
				FreeShipMaxVnd:      20000,
			},
		}
		h.payment.feeBPSByMethod = map[paymentv1.PaymentMethod]int32{
			paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR: 150, // 1.50%
		}
		h.promo.activeShopDiscounts = []*promotionv1.ActiveShopDiscount{
			{
				ItemId:             "sp-item-1",
				ProgramId:          "sp-1",
				ShopId:             "shop-1",
				SkuId:              "sku-1",
				OriginalPriceVnd:   1000000,
				DiscountedPriceVnd: 850000, // P_shop = 150,000
				DiscountPercent:    15,
				PerUserLimit:       2,
				StartsAt:           timestamppb.New(baseNow.Add(-time.Hour)),
				EndsAt:             timestamppb.New(baseNow.Add(time.Hour)),
			},
		}
		// G=1,000,000; P_shop=150,000; V_shop=50,000 -> merchAfterShop=800,000 >= 500,000
		// Ship=30,000; S_shop=min(30k,20k)=20,000 -> R = 780,000
		// Remaining ship=10,000; S_free=min(15k,10k)=10,000; V_plat=40,000; X=10,000 -> D=60,000
		// P = 780,000 + 30,000 - 60,000 = 750,000
		// C_cat = 850,000 * 500 / 10000 = 42,500
		// C_pay = 750,000 * 150 / 10000 = 11,250
		// C_total = 53,750 <= R (780,000)
		h.promo.breakdown = &promotionv1.DiscountBreakdown{
			Shops: []*promotionv1.ShopDiscountBreakdown{
				{
					ShopId:                     "shop-1",
					SubtotalVnd:                1000000,
					ShopProgramDiscountVnd:     150000,
					ShopVoucherDiscountVnd:     50000,
					ShopFreeshipDiscountVnd:    20000,
					PlatformVoucherDiscountVnd: 40000,
					FreeshipDiscountVnd:        15000, // deliberately > remaining ship 10k to verify order clamps to 10k
					CoinsDiscountVnd:           10000,
					ShippingFeeVnd:             30000,
					AppliedShopProgramItemIds:  []string{"sp-item-1"},
				},
			},
		}

		buyerCtx := auth.WithRawToken(ctxWithUser("buyer-1", "buyer", ""), "Bearer buyer-jwt")
		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "w2b-full-stack",
			AddressId:         "addr-buyer-1",
			Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			ShopVoucherIds:    map[string]string{"shop-1": "v-s1"},
			PlatformVoucherId: "v-p1",
			FreeshipVoucherId: "v-f1",
			CoinsToUse:        10000,
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout failed: %v", err)
		}

		// Verify GetShopShippingSettings was called with service token (not buyer JWT).
		if len(h.shipping.getShopSettingsCalls) != 1 || h.shipping.getShopSettingsAuth[0].service != clients.InternalServiceName || h.shipping.getShopSettingsAuth[0].rawToken != "" {
			t.Fatalf("GetShopShippingSettings must use service token, got calls=%d auth=%+v", len(h.shipping.getShopSettingsCalls), h.shipping.getShopSettingsAuth)
		}
		// Verify ReservePromotion received category_id, free_ship_* and shop_discount_program_items.
		if len(h.promo.reserveReqs) != 1 {
			t.Fatalf("expected 1 ReservePromotion call, got %d", len(h.promo.reserveReqs))
		}
		pr := h.promo.reserveReqs[0]
		if len(pr.ShopDiscountProgramItems) != 1 || pr.ShopDiscountProgramItems[0].ShopDiscountProgramItemId != "sp-item-1" || pr.ShopDiscountProgramItems[0].ShopId != "shop-1" {
			t.Fatalf("unexpected shop_discount_program_items: %+v", pr.ShopDiscountProgramItems)
		}
		if len(pr.ShopItems) != 1 || !pr.ShopItems[0].FreeShipEnabled || pr.ShopItems[0].FreeShipMinOrderVnd != 500000 || pr.ShopItems[0].FreeShipMaxVnd != 20000 {
			t.Fatalf("unexpected shop freeship inputs to ReservePromotion: %+v", pr.ShopItems)
		}
		if len(pr.ShopItems[0].Items) != 1 || pr.ShopItems[0].Items[0].CategoryId != "cat-elec" || pr.ShopItems[0].Items[0].ShopDiscountProgramItemId != "sp-item-1" {
			t.Fatalf("unexpected DiscountLineItem fields: %+v", pr.ShopItems[0].Items)
		}

		ord := resp.Orders[0]
		if ord.ShopProgramDiscountVnd != 150000 {
			t.Fatalf("shop_program_discount_vnd = %d, want 150000", ord.ShopProgramDiscountVnd)
		}
		if ord.ShopFreeshipDiscountVnd != 20000 {
			t.Fatalf("shop_freeship_discount_vnd = %d, want 20000", ord.ShopFreeshipDiscountVnd)
		}
		if ord.FreeshipDiscountVnd != 10000 {
			t.Fatalf("freeship_discount_vnd = %d, want 10000 (clamped to Ship - S_shop)", ord.FreeshipDiscountVnd)
		}
		if ord.TotalPayableVnd != 750000 {
			t.Fatalf("total_payable_vnd = %d, want 750000", ord.TotalPayableVnd)
		}
		if ord.CommissionVnd != 0 || ord.PaymentFeeVnd != 0 || ord.PaymentFeeBps != 0 || ord.Items[0].CommissionBps != 0 {
			t.Fatalf("buyer checkout view must mask fees to 0: C_cat=%d C_pay=%d bps=%d itemBps=%d", ord.CommissionVnd, ord.PaymentFeeVnd, ord.PaymentFeeBps, ord.Items[0].CommissionBps)
		}
		adminOrd, err := h.svc.GetOrder(ctxWithUser("admin-1", "admin", ""), &orderv1.GetOrderRequest{OrderId: ord.Id})
		if err != nil {
			t.Fatalf("admin GetOrder: %v", err)
		}
		if adminOrd.Order.CommissionVnd != 42500 {
			t.Fatalf("commission_vnd = %d, want 42500", adminOrd.Order.CommissionVnd)
		}
		if adminOrd.Order.PaymentFeeVnd != 11250 || adminOrd.Order.PaymentFeeBps != 150 {
			t.Fatalf("payment_fee = (%d, %d bps), want (11250, 150 bps)", adminOrd.Order.PaymentFeeVnd, adminOrd.Order.PaymentFeeBps)
		}
		if adminOrd.Order.Items[0].EffectiveUnitPriceVnd != 850000 || adminOrd.Order.Items[0].CommissionBps != 500 || adminOrd.Order.Items[0].ShopProgramItemId != "sp-item-1" {
			t.Fatalf("unexpected order item fields: %+v", adminOrd.Order.Items[0])
		}

		payReq := h.payment.createCalls[0]
		al := payReq.ShopAllocations[0]
		if al.SubtotalVnd != 780000 || al.CommissionVnd != 53750 || al.ShippingFeeVnd != 30000 || al.PlatformDiscountVnd != 60000 {
			t.Fatalf("allocation = %+v, want R=780000 C_total=53750 Ship=30000 D=60000", al)
		}
		if got := al.SubtotalVnd + al.ShippingFeeVnd - al.PlatformDiscountVnd; got != payReq.AmountVnd || got != 750000 {
			t.Fatalf("R+Ship-D = %d, amount = %d, want 750000", got, payReq.AmountVnd)
		}
	})

	t.Run("SingleItemPriceRule_FlashWinsOverShopDiscountProgram", func(t *testing.T) {
		h := newTestHarness()
		h.svc.nowFn = func() time.Time { return baseNow }
		h.promo.flashSales = []*promotionv1.FlashSale{
			{
				Id:       "fs-live",
				StartsAt: timestamppb.New(baseNow.Add(-10 * time.Minute)),
				EndsAt:   timestamppb.New(baseNow.Add(30 * time.Minute)),
				Items: []*promotionv1.FlashSaleItem{
					{Id: "fsi-live", SkuId: "sku-1", ShopId: "shop-1", FlashPriceVnd: 700000, Status: promotionv1.FlashSaleItemStatus_FLASH_SALE_ITEM_STATUS_APPROVED},
				},
			},
		}
		h.promo.activeShopDiscounts = []*promotionv1.ActiveShopDiscount{
			{
				ItemId:             "sp-conflict",
				ProgramId:          "sp-1",
				ShopId:             "shop-1",
				SkuId:              "sku-1",
				OriginalPriceVnd:   1000000,
				DiscountedPriceVnd: 850000,
				StartsAt:           timestamppb.New(baseNow.Add(-time.Hour)),
				EndsAt:             timestamppb.New(baseNow.Add(time.Hour)),
			},
		}
		h.promo.breakdown = &promotionv1.DiscountBreakdown{
			Shops: []*promotionv1.ShopDiscountBreakdown{
				{
					ShopId:               "shop-1",
					SubtotalVnd:          1000000,
					FlashSaleDiscountVnd: 300000,
					ShippingFeeVnd:       30000,
				},
			},
		}

		resp, err := h.svc.CreateCheckout(ctxWithUser("buyer-1", "buyer", ""), &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w2b-flash-wins",
			AddressId:      "addr-buyer-1",
			Items: []*orderv1.CheckoutItemInput{
				{SkuId: "sku-1", Quantity: 1, FlashSaleItemId: "fsi-live", ShopProgramItemId: "sp-conflict"},
			},
			PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout failed: %v", err)
		}
		ord := resp.Orders[0]
		if ord.FlashSaleDiscountVnd != 300000 || ord.ShopProgramDiscountVnd != 0 {
			t.Fatalf("flash must win over shop program without stacking: F=%d P_shop=%d", ord.FlashSaleDiscountVnd, ord.ShopProgramDiscountVnd)
		}
		if ord.Items[0].ShopProgramItemId != "" || ord.Items[0].FlashSaleItemId != "fsi-live" || ord.Items[0].EffectiveUnitPriceVnd != 700000 {
			t.Fatalf("unexpected line item when flash wins: %+v", ord.Items[0])
		}
		if len(h.promo.reserveReqs[0].ShopDiscountProgramItems) != 0 {
			t.Fatalf("must not reserve shop program for flash SKU, got %+v", h.promo.reserveReqs[0].ShopDiscountProgramItems)
		}
	})

	t.Run("Boundary_NegativeR_TrimsShopFreeship_AndCapsFeesAtZero", func(t *testing.T) {
		h := newTestHarness()
		h.svc.nowFn = func() time.Time { return baseNow }
		h.product.skus["sku-small"] = &productv1.SKU{
			Id: "sku-small", ProductId: "prod-small", ShopId: "shop-1",
			ProductName: "Cable", Name: "1m", PriceVnd: 100000,
			Stock: 50, AvailableStock: 50, WeightGrams: 100,
			CommissionRateBps: 500, IsActive: true,
		}
		h.shipping.shopSettings = map[string]*shippingv1.ShopShippingSettings{
			"shop-1": {
				ShopId:              "shop-1",
				DefaultWeightGrams:  500,
				FreeShipEnabled:     true,
				FreeShipMinOrderVnd: 0,
				FreeShipMaxVnd:      30000, // Naive R = 100k - 90k - 30k = -20k -> must trim S_shop to 10k so R = 0!
			},
		}
		h.payment.feeBPSByMethod = map[paymentv1.PaymentMethod]int32{
			paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR: 200,
		}
		h.promo.breakdown = &promotionv1.DiscountBreakdown{
			Shops: []*promotionv1.ShopDiscountBreakdown{
				{
					ShopId:                  "shop-1",
					SubtotalVnd:             100000,
					ShopVoucherDiscountVnd:  90000,
					ShopFreeshipDiscountVnd: 30000, // intentionally untrimmed to test order guard
					FreeshipDiscountVnd:     25000, // must clamp to remaining ship 30k - 10k = 20k
					ShippingFeeVnd:          30000,
				},
			},
		}

		resp, err := h.svc.CreateCheckout(ctxWithUser("buyer-1", "buyer", ""), &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "w2b-neg-r",
			AddressId:         "addr-buyer-1",
			Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-small", Quantity: 1}},
			ShopVoucherIds:    map[string]string{"shop-1": "v-90k"},
			FreeshipVoucherId: "v-free-25k",
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout failed: %v", err)
		}
		ord := resp.Orders[0]
		if ord.ShopFreeshipDiscountVnd != 10000 {
			t.Fatalf("S_shop = %d, want 10000 (trimmed to keep R >= 0)", ord.ShopFreeshipDiscountVnd)
		}
		if ord.FreeshipDiscountVnd != 20000 {
			t.Fatalf("S_free = %d, want 20000 (remaining Ship - S_shop)", ord.FreeshipDiscountVnd)
		}
		if ord.TotalPayableVnd != 10000 { // R=0 + Ship=30k - D=20k = 10k
			t.Fatalf("P = %d, want 10000", ord.TotalPayableVnd)
		}
		adminOrd, _ := h.svc.GetOrder(ctxWithUser("admin-1", "admin", ""), &orderv1.GetOrderRequest{OrderId: ord.Id})
		if adminOrd.Order.CommissionVnd != 0 || adminOrd.Order.PaymentFeeVnd != 0 || adminOrd.Order.PaymentFeeBps != 200 {
			t.Fatalf("fees must cap at R=0: C_cat=%d C_pay=%d bps=%d", adminOrd.Order.CommissionVnd, adminOrd.Order.PaymentFeeVnd, adminOrd.Order.PaymentFeeBps)
		}
		al := h.payment.createCalls[0].ShopAllocations[0]
		if al.SubtotalVnd != 0 || al.CommissionVnd != 0 || al.ShippingFeeVnd != 30000 || al.PlatformDiscountVnd != 20000 {
			t.Fatalf("unexpected allocation for R=0 boundary: %+v", al)
		}
		if al.SubtotalVnd+al.ShippingFeeVnd-al.PlatformDiscountVnd != h.payment.createCalls[0].AmountVnd {
			t.Fatalf("allocation invariant violated: %+v vs amount=%d", al, h.payment.createCalls[0].AmountVnd)
		}
	})

	t.Run("Boundary_FeeCap_CTotalCappedAtR", func(t *testing.T) {
		h := newTestHarness()
		h.svc.nowFn = func() time.Time { return baseNow }
		h.product.skus["sku-cap"] = &productv1.SKU{
			Id: "sku-cap", ProductId: "prod-cap", ShopId: "shop-1",
			ProductName: "Item", Name: "Std", PriceVnd: 100000,
			Stock: 10, AvailableStock: 10, WeightGrams: 200,
			CommissionRateBps: 800, // raw C_cat = 8,000
			IsActive:          true,
		}
		h.payment.feeBPSByMethod = map[paymentv1.PaymentMethod]int32{
			paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR: 1000, // 10% of P=40,000 -> raw C_pay = 4,000
		}
		// G=100k, V_shop=90k -> R=10,000; Ship=30,000 -> P=40,000.
		// C_cat=8,000 <= R=10,000; remaining for C_pay = 2,000 -> C_pay capped at 2,000 so C_total=10,000==R.
		h.promo.breakdown = &promotionv1.DiscountBreakdown{
			Shops: []*promotionv1.ShopDiscountBreakdown{
				{ShopId: "shop-1", SubtotalVnd: 100000, ShopVoucherDiscountVnd: 90000, ShippingFeeVnd: 30000},
			},
		}
		resp, err := h.svc.CreateCheckout(ctxWithUser("buyer-1", "buyer", ""), &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w2b-fee-cap",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-cap", Quantity: 1}},
			ShopVoucherIds: map[string]string{"shop-1": "v-90k"},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout failed: %v", err)
		}
		ord := resp.Orders[0]
		adminOrd, _ := h.svc.GetOrder(ctxWithUser("admin-1", "admin", ""), &orderv1.GetOrderRequest{OrderId: ord.Id})
		if adminOrd.Order.CommissionVnd != 8000 || adminOrd.Order.PaymentFeeVnd != 2000 {
			t.Fatalf("expected C_cat=8000 C_pay=2000 (capped at R=10000), got C_cat=%d C_pay=%d", adminOrd.Order.CommissionVnd, adminOrd.Order.PaymentFeeVnd)
		}
		al := h.payment.createCalls[0].ShopAllocations[0]
		if al.CommissionVnd != 10000 || al.SubtotalVnd != 10000 {
			t.Fatalf("allocation C_total=%d R=%d, want 10000/10000", al.CommissionVnd, al.SubtotalVnd)
		}
	})

	t.Run("Boundary_ZeroPayableOrder_WithShopProgramAndShopFreeship", func(t *testing.T) {
		h := newTestHarness()
		h.svc.nowFn = func() time.Time { return baseNow }
		h.shipping.shopSettings = map[string]*shippingv1.ShopShippingSettings{
			"shop-1": {ShopId: "shop-1", DefaultWeightGrams: 500, FreeShipEnabled: true, FreeShipMinOrderVnd: 500000, FreeShipMaxVnd: 30000},
		}
		h.payment.feeBPSByMethod = map[paymentv1.PaymentMethod]int32{
			paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR: 150,
		}
		h.promo.activeShopDiscounts = []*promotionv1.ActiveShopDiscount{
			{
				ItemId: "sp-0", ProgramId: "p-0", ShopId: "shop-1", SkuId: "sku-1",
				OriginalPriceVnd: 1000000, DiscountedPriceVnd: 800000,
				StartsAt: timestamppb.New(baseNow.Add(-time.Hour)), EndsAt: timestamppb.New(baseNow.Add(time.Hour)),
			},
		}
		// G=1,000,000; P_shop=200,000; S_shop=30,000 -> R=770,000; Ship=30,000; V_plat=500,000, X=300,000 -> D=800,000, P=0
		h.promo.breakdown = &promotionv1.DiscountBreakdown{
			Shops: []*promotionv1.ShopDiscountBreakdown{
				{
					ShopId:                     "shop-1",
					SubtotalVnd:                1000000,
					ShopProgramDiscountVnd:     200000,
					ShopFreeshipDiscountVnd:    30000,
					PlatformVoucherDiscountVnd: 500000,
					CoinsDiscountVnd:           300000,
					ShippingFeeVnd:             30000,
					AppliedShopProgramItemIds:  []string{"sp-0"},
				},
			},
		}
		resp, err := h.svc.CreateCheckout(ctxWithUser("buyer-1", "buyer", ""), &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "w2b-zero-vnd",
			AddressId:         "addr-buyer-1",
			Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PlatformVoucherId: "v-p500",
			CoinsToUse:        300000,
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout 0-VND failed: %v", err)
		}
		ord := resp.Orders[0]
		if ord.TotalPayableVnd != 0 || ord.Status != orderv1.OrderStatus_ORDER_STATUS_CONFIRMED || ord.PaymentStatus != "SUCCEEDED" {
			t.Fatalf("expected CONFIRMED/SUCCEEDED 0-VND order, got P=%d status=%v payStatus=%q", ord.TotalPayableVnd, ord.Status, ord.PaymentStatus)
		}
		adminOrd, _ := h.svc.GetOrder(ctxWithUser("admin-1", "admin", ""), &orderv1.GetOrderRequest{OrderId: ord.Id})
		if adminOrd.Order.PaymentFeeVnd != 0 || adminOrd.Order.PaymentFeeBps != 150 {
			t.Fatalf("0-VND order must snapshot bps=150 with payment_fee_vnd=0, got (%d, %d)", adminOrd.Order.PaymentFeeVnd, adminOrd.Order.PaymentFeeBps)
		}
		if len(h.payment.createCalls) != 1 || h.payment.createCalls[0].AmountVnd != 0 {
			t.Fatalf("0-VND order must still call CreatePayment(amount=0)")
		}
		al := h.payment.createCalls[0].ShopAllocations[0]
		if al.SubtotalVnd != 770000 || al.ShippingFeeVnd != 30000 || al.PlatformDiscountVnd != 800000 || al.CommissionVnd != 40000 {
			t.Fatalf("unexpected 0-VND allocation: %+v", al)
		}
	})

	t.Run("MultiShop_COD_DefaultWeight_ShopFreeshipWithoutOtherPromos", func(t *testing.T) {
		h := newTestHarness()
		h.svc.nowFn = func() time.Time { return baseNow }
		// sku-noweight has WeightGrams=0 -> must use shop-1's default_weight_grams=1200!
		h.product.skus["sku-noweight"] = &productv1.SKU{
			Id: "sku-noweight", ProductId: "prod-nw", ShopId: "shop-1",
			ProductName: "Bulk Box", Name: "L", PriceVnd: 600000,
			Stock: 20, AvailableStock: 20, WeightGrams: 0,
			CommissionRateBps: 500, IsActive: true,
		}
		h.shipping.shopSettings = map[string]*shippingv1.ShopShippingSettings{
			"shop-1": {
				ShopId:              "shop-1",
				DefaultWeightGrams:  1200,
				FreeShipEnabled:     true,
				FreeShipMinOrderVnd: 500000,
				FreeShipMaxVnd:      25000,
			},
			"shop-2": {
				ShopId:             "shop-2",
				DefaultWeightGrams: 400,
				FreeShipEnabled:    false,
			},
		}
		h.payment.feeBPSByMethod = map[paymentv1.PaymentMethod]int32{
			paymentv1.PaymentMethod_PAYMENT_METHOD_COD: 100, // 1.00% COD fee
		}

		resp, err := h.svc.CreateCheckout(ctxWithUser("buyer-1", "buyer", ""), &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w2b-multi-cod-weight",
			AddressId:      "addr-buyer-1",
			Items: []*orderv1.CheckoutItemInput{
				{SkuId: "sku-noweight", Quantity: 2}, // shop-1: G=1,200,000, weight=2*1200=2400g, Ship=30k, S_shop=25k -> R=1,175,000, P=1,205,000
				{SkuId: "sku-2", Quantity: 1},        // shop-2: G=500,000, weight=300g, Ship=30k, S_shop=0 -> R=500,000, P=530,000
			},
			PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil {
			t.Fatalf("CreateCheckout COD multi-shop failed: %v", err)
		}
		// Verify shop-1 CalculateFee used default_weight_grams*2 = 2400g.
		var sawShop1Weight int32
		for _, r := range h.shipping.calculateFeeReqs {
			if r.GetShopId() == "shop-1" {
				sawShop1Weight = r.GetWeightGrams()
			}
		}
		if sawShop1Weight != 2400 {
			t.Fatalf("shop-1 CalculateFee weight_grams = %d, want 2400 (default_weight_grams 1200 * qty 2)", sawShop1Weight)
		}
		// Shop-1 order must have S_shop=25,000 even though ReservePromotion wasn't needed.
		o1 := resp.Orders[0]
		o2 := resp.Orders[1]
		if o1.ShopFreeshipDiscountVnd != 25000 || o1.TotalPayableVnd != 1205000 {
			t.Fatalf("shop-1: S_shop=%d P=%d, want 25000 / 1205000", o1.ShopFreeshipDiscountVnd, o1.TotalPayableVnd)
		}
		if o1.Items[0].WeightGrams != 1200 {
			t.Fatalf("shop-1 item weight_grams = %d, want 1200", o1.Items[0].WeightGrams)
		}
		// Shop-1 fees (via admin view; buyer view masks fees to 0): C_cat = 60,000; C_pay = 12,050
		adminCtx := ctxWithUser("admin-1", "admin", "")
		a1, _ := h.svc.GetOrder(adminCtx, &orderv1.GetOrderRequest{OrderId: o1.Id})
		a2, _ := h.svc.GetOrder(adminCtx, &orderv1.GetOrderRequest{OrderId: o2.Id})
		if a1.Order.CommissionVnd != 60000 || a1.Order.PaymentFeeVnd != 12050 || a1.Order.PaymentFeeBps != 100 {
			t.Fatalf("shop-1 fees: C_cat=%d C_pay=%d bps=%d, want 60000/12050/100", a1.Order.CommissionVnd, a1.Order.PaymentFeeVnd, a1.Order.PaymentFeeBps)
		}
		// Shop-2 fees: C_cat = 500,000 * 800 / 10000 = 40,000; C_pay = 530,000 * 100 / 10000 = 5,300
		if o2.ShopFreeshipDiscountVnd != 0 || o2.TotalPayableVnd != 530000 || a2.Order.PaymentFeeVnd != 5300 {
			t.Fatalf("shop-2: S_shop=%d P=%d C_pay=%d, want 0/530000/5300", o2.ShopFreeshipDiscountVnd, o2.TotalPayableVnd, a2.Order.PaymentFeeVnd)
		}
		payReq := h.payment.createCalls[0]
		var sum int64
		for _, al := range payReq.ShopAllocations {
			sum += al.SubtotalVnd + al.ShippingFeeVnd - al.PlatformDiscountVnd
		}
		if sum != payReq.AmountVnd || sum != 1735000 {
			t.Fatalf("multi-shop sum(R+Ship-D)=%d != amount=%d (want 1735000)", sum, payReq.AmountVnd)
		}
	})

	t.Run("GetFeeConfigError_FailsCheckoutClearlyWithoutGuessing", func(t *testing.T) {
		h := newTestHarness()
		h.payment.getFeeConfigErr = status.Error(codes.Unavailable, "payment fee config db down")
		_, err := h.svc.CreateCheckout(ctxWithUser("buyer-1", "buyer", ""), &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w2b-fee-err",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err == nil {
			t.Fatalf("expected checkout to fail when Payment.GetFeeConfig errors")
		}
		if len(h.product.reserveCalls) != 0 {
			t.Fatalf("must not reserve stock when GetFeeConfig fails early, got %d calls", len(h.product.reserveCalls))
		}
	})

	t.Run("ServerSideTrust_IgnoresBogusClientShopProgramID", func(t *testing.T) {
		h := newTestHarness()
		h.svc.nowFn = func() time.Time { return baseNow }
		// No active shop discounts on server, even though client claims shop_program_item_id.
		resp, err := h.svc.CreateCheckout(ctxWithUser("buyer-1", "buyer", ""), &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w2b-bogus-sp",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1, ShopProgramItemId: "forged-item-id"}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout failed: %v", err)
		}
		ord := resp.Orders[0]
		if ord.ShopProgramDiscountVnd != 0 || ord.Items[0].ShopProgramItemId != "" || ord.Items[0].EffectiveUnitPriceVnd != 1000000 {
			t.Fatalf("forged client shop_program_item_id must be ignored: disc=%d item=%+v", ord.ShopProgramDiscountVnd, ord.Items[0])
		}
	})

	t.Run("LegacyOrderRow_DefaultsZeroCleanly", func(t *testing.T) {
		legacy := db.Order{
			ID:                     "ord-legacy",
			ShopID:                 "shop-1",
			SubtotalVND:            1000000,
			ShopVoucherDiscountVND: 50000,
			ShippingFeeVND:         30000,
			CommissionVND:          50000,
			TotalPayableVND:        980000,
		}
		al := buildShopAllocation(legacy)
		if al.SubtotalVnd != 950000 || al.CommissionVnd != 50000 || al.ShippingFeeVnd != 30000 || al.PlatformDiscountVnd != 0 {
			t.Fatalf("unexpected legacy allocation: %+v", al)
		}
		if coins := ComputeOrderRewardCoins(legacy, 100); coins != 9500 {
			t.Fatalf("legacy reward coins = %d, want 9500", coins)
		}
	})
}

// TestW2B_FlashSale_CheckoutAndRetry verifies Wave 2b item 2:
// active-only flash slots, short ORDER_FLASH_PAYMENT_EXPIRY_MINUTES, rejecting COD on flash,
// and RetryPayment keeping the short deadline without extending past the original window.
func TestW2B_FlashSale_CheckoutAndRetry(t *testing.T) {
	t.Run("RejectsCOD_WhenFlashSaleItemPresent", func(t *testing.T) {
		h := newTestHarness()
		now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
		h.svc.nowFn = func() time.Time { return now }
		h.promo.flashSales = []*promotionv1.FlashSale{
			{
				Id:       "fs-1",
				StartsAt: timestamppb.New(now.Add(-5 * time.Minute)),
				EndsAt:   timestamppb.New(now.Add(1 * time.Hour)),
				Items: []*promotionv1.FlashSaleItem{
					{Id: "fsi-1", SkuId: "sku-1", ShopId: "shop-1", FlashPriceVnd: 750000, Status: promotionv1.FlashSaleItemStatus_FLASH_SALE_ITEM_STATUS_APPROVED},
				},
			},
		}
		_, err := h.svc.CreateCheckout(ctxWithUser("buyer-1", "buyer", ""), &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w2b-flash-cod",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1, FlashSaleItemId: "fsi-1"}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "COD") {
			t.Fatalf("expected FailedPrecondition mentioning COD for flash sale, got %v", err)
		}
		if len(h.product.reserveCalls) != 0 || len(h.promo.reserveCalls) != 0 {
			t.Fatalf("rejected COD flash order must not reserve stock or promotions")
		}
	})

	t.Run("RejectsUpcomingOrInactiveFlashSaleSlot", func(t *testing.T) {
		h := newTestHarness()
		now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
		h.svc.nowFn = func() time.Time { return now }
		// Flash sale starts 30 minutes in the future (upcoming).
		h.promo.flashSales = []*promotionv1.FlashSale{
			{
				Id:       "fs-upcoming",
				StartsAt: timestamppb.New(now.Add(30 * time.Minute)),
				EndsAt:   timestamppb.New(now.Add(2 * time.Hour)),
				Items: []*promotionv1.FlashSaleItem{
					{Id: "fsi-upcoming", SkuId: "sku-1", ShopId: "shop-1", FlashPriceVnd: 750000, Status: promotionv1.FlashSaleItemStatus_FLASH_SALE_ITEM_STATUS_APPROVED},
				},
			},
		}
		_, err := h.svc.CreateCheckout(ctxWithUser("buyer-1", "buyer", ""), &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w2b-flash-upcoming",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1, FlashSaleItemId: "fsi-upcoming"}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("expected FailedPrecondition for upcoming flash slot, got %v", err)
		}
	})

	t.Run("ShortFlashExpiry_AndRetryNeverExtendsWindow", func(t *testing.T) {
		h := newTestHarness()
		curNow := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
		h.svc.nowFn = func() time.Time { return curNow }
		h.promo.flashSales = []*promotionv1.FlashSale{
			{
				Id:       "fs-live",
				StartsAt: timestamppb.New(curNow.Add(-5 * time.Minute)),
				EndsAt:   timestamppb.New(curNow.Add(1 * time.Hour)),
				Items: []*promotionv1.FlashSaleItem{
					{Id: "fsi-live", SkuId: "sku-1", ShopId: "shop-1", FlashPriceVnd: 800000, Status: promotionv1.FlashSaleItemStatus_FLASH_SALE_ITEM_STATUS_APPROVED},
				},
			},
		}
		h.promo.breakdown = &promotionv1.DiscountBreakdown{
			Shops: []*promotionv1.ShopDiscountBreakdown{
				{ShopId: "shop-1", SubtotalVnd: 1000000, FlashSaleDiscountVnd: 200000, ShippingFeeVnd: 30000},
			},
		}

		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w2b-flash-short-exp",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1, FlashSaleItemId: "fsi-live"}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout flash failed: %v", err)
		}
		wantInitialExp := curNow.Add(5 * time.Minute)
		if gotExp := resp.GetPaymentExpiresAt().AsTime().UTC(); !gotExp.Equal(wantInitialExp) {
			t.Fatalf("flash payment_expires_at = %s, want %s (5m short expiry)", gotExp, wantInitialExp)
		}

		// Advance clock by 3 minutes (2 minutes remaining) and retry payment.
		curNow = curNow.Add(3 * time.Minute)
		retry1, err := h.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
			PaymentId:      resp.PaymentId,
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
			IdempotencyKey: "retry-flash-1",
		})
		if err != nil {
			t.Fatalf("RetryPayment(1) failed: %v", err)
		}
		if gotExp := retry1.GetPaymentExpiresAt().AsTime().UTC(); !gotExp.Equal(wantInitialExp) {
			t.Fatalf("RetryPayment must NOT extend flash window: got %s, want original %s", gotExp, wantInitialExp)
		}
		if reqExp := h.payment.createCalls[1].GetExpiresAt().AsTime().UTC(); !reqExp.Equal(wantInitialExp) {
			t.Fatalf("Payment.CreatePayment retry expires_at = %s, want %s", reqExp, wantInitialExp)
		}

		// Advance clock past the original 5-minute window -> RetryPayment MUST fail!
		curNow = wantInitialExp.Add(time.Second)
		if _, err := h.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
			PaymentId:      retry1.PaymentId,
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
			IdempotencyKey: "retry-flash-2",
		}); status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("expected FailedPrecondition after flash 5m window expired, got %v", err)
		}
	})

	t.Run("FlashExpiryCappedByFlashSaleEndsAt", func(t *testing.T) {
		h := newTestHarness()
		curNow := time.Date(2026, 11, 23, 10, 58, 0, 0, time.UTC)
		flashEnds := time.Date(2026, 11, 23, 11, 0, 0, 0, time.UTC) // only 2 minutes left!
		h.svc.nowFn = func() time.Time { return curNow }
		h.promo.flashSales = []*promotionv1.FlashSale{
			{
				Id:       "fs-ending",
				StartsAt: timestamppb.New(curNow.Add(-58 * time.Minute)),
				EndsAt:   timestamppb.New(flashEnds),
				Items: []*promotionv1.FlashSaleItem{
					{Id: "fsi-ending", SkuId: "sku-1", ShopId: "shop-1", FlashPriceVnd: 800000, Status: promotionv1.FlashSaleItemStatus_FLASH_SALE_ITEM_STATUS_APPROVED},
				},
			},
		}
		h.promo.breakdown = &promotionv1.DiscountBreakdown{
			Shops: []*promotionv1.ShopDiscountBreakdown{
				{ShopId: "shop-1", SubtotalVnd: 1000000, FlashSaleDiscountVnd: 200000, ShippingFeeVnd: 30000},
			},
		}
		resp, err := h.svc.CreateCheckout(ctxWithUser("buyer-1", "buyer", ""), &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w2b-flash-ends-cap",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1, FlashSaleItemId: "fsi-ending"}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout failed: %v", err)
		}
		if gotExp := resp.GetPaymentExpiresAt().AsTime().UTC(); !gotExp.Equal(flashEnds) {
			t.Fatalf("expected flash expiry capped at ends_at %s, got %s", flashEnds, gotExp)
		}
	})
}

// TestW2B_Reports_SellerAndPlatform verifies Wave 2b item 3:
// Asia/Ho_Chi_Minh UTC+7 date boundaries, <= 366d span, GMV / net revenue / fees / subsidy /
// cancel & return rates / top products, and strict shop RBAC.
func TestW2B_Reports_SellerAndPlatform(t *testing.T) {
	h := newTestHarness()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	seller1Ctx := ctxWithUser("seller-1", "seller", "shop-1")
	adminCtx := ctxWithUser("admin-1", "admin", "")

	h.payment.feeBPSByMethod = map[paymentv1.PaymentMethod]int32{
		paymentv1.PaymentMethod_PAYMENT_METHOD_COD:    100, // 1%
		paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR: 100,
	}
	h.shipping.shopSettings = map[string]*shippingv1.ShopShippingSettings{
		"shop-1": {ShopId: "shop-1", DefaultWeightGrams: 500, FreeShipEnabled: true, FreeShipMinOrderVnd: 500000, FreeShipMaxVnd: 10000},
		"shop-2": {ShopId: "shop-2", DefaultWeightGrams: 500},
	}

	// Order 1 (shop-1): created at 2026-11-23T16:59:59Z = 2026-11-23 23:59:59 VN!
	// Completed on 2026-11-24T17:30:00Z = 2026-11-25 00:30:00 VN!
	// G=1,000,000, S_shop=10,000 -> R=990,000, Ship=30,000, P=1,020,000, C_cat=50,000, C_pay=10,200.
	// Also record a 20,000 partial refund -> Net = 990,000 - 20,000 - (50,000 + 10,200) = 909,800.
	tOrder1Created := time.Date(2026, 11, 23, 16, 59, 59, 0, time.UTC)
	tOrder1Completed := time.Date(2026, 11, 24, 17, 30, 0, 0, time.UTC)
	h.svc.nowFn = func() time.Time { return tOrder1Created }
	r1, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "rep-ord-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	if err != nil {
		t.Fatalf("CreateCheckout(1): %v", err)
	}
	ord1 := r1.Orders[0]
	if err := h.svc.HandleShipmentUpdated(context.Background(), "rep-del-1", events.ShipmentUpdatedPayload{
		OrderID: ord1.Id, Status: "delivered", Description: "delivered",
	}); err != nil {
		t.Fatalf("deliver(1): %v", err)
	}
	h.svc.nowFn = func() time.Time { return tOrder1Completed }
	if _, err := h.svc.BuyerConfirmReceived(buyerCtx, &orderv1.BuyerConfirmReceivedRequest{OrderId: ord1.Id}); err != nil {
		t.Fatalf("complete(1): %v", err)
	}
	if err := h.svc.HandlePaymentRefunded(context.Background(), "rep-partial-ref-1", events.PaymentRefundedPayload{
		RefundID: "ref-part-1", PaymentID: r1.PaymentId, OrderID: ord1.Id, AmountVND: 20000, Reason: "partial",
	}); err != nil {
		t.Fatalf("partial refund(1): %v", err)
	}

	// Order 2 (shop-1): created at 2026-11-23T17:00:00Z = 2026-11-24 00:00:00 VN -> CANCELLED (excluded from GMV!).
	tOrder2Created := time.Date(2026, 11, 23, 17, 0, 0, 0, time.UTC)
	h.svc.nowFn = func() time.Time { return tOrder2Created }
	r2, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "rep-ord-2",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	if err != nil {
		t.Fatalf("CreateCheckout(2): %v", err)
	}
	if _, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: r2.Orders[0].Id, Reason: "changed mind"}); err != nil {
		t.Fatalf("cancel(2): %v", err)
	}

	// Order 3 (shop-2): created & completed on 2026-11-24 VN with 50k platform voucher (D=50k) -> must NOT leak into shop-1 report, MUST appear in platform report!
	h.promo.breakdown = &promotionv1.DiscountBreakdown{
		Shops: []*promotionv1.ShopDiscountBreakdown{
			{ShopId: "shop-2", SubtotalVnd: 500000, PlatformVoucherDiscountVnd: 50000, ShippingFeeVnd: 30000},
		},
	}
	tOrder3 := time.Date(2026, 11, 24, 5, 0, 0, 0, time.UTC) // 12:00 VN on 2026-11-24
	h.svc.nowFn = func() time.Time { return tOrder3 }
	r3, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey:    "rep-ord-3",
		AddressId:         "addr-buyer-1",
		Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-2", Quantity: 1}},
		PlatformVoucherId: "v-p50",
		PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	if err != nil {
		t.Fatalf("CreateCheckout(3): %v", err)
	}
	ord3 := r3.Orders[0]
	_ = h.svc.HandleShipmentUpdated(context.Background(), "rep-del-3", events.ShipmentUpdatedPayload{
		OrderID: ord3.Id, Status: "delivered",
	})
	if _, err := h.svc.BuyerConfirmReceived(buyerCtx, &orderv1.BuyerConfirmReceivedRequest{OrderId: ord3.Id}); err != nil {
		t.Fatalf("complete(3): %v", err)
	}

	// 1. RBAC checks.
	if _, err := h.svc.GetSellerReport(buyerCtx, &orderv1.GetSellerReportRequest{FromDate: "2026-11-23", ToDate: "2026-11-25"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("buyer GetSellerReport must be PermissionDenied, got %v", err)
	}
	if _, err := h.svc.GetSellerReport(seller1Ctx, &orderv1.GetSellerReportRequest{ShopId: "shop-2", FromDate: "2026-11-23", ToDate: "2026-11-25"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("seller requesting another shop_id must be PermissionDenied, got %v", err)
	}
	if _, err := h.svc.GetPlatformReport(seller1Ctx, &orderv1.GetPlatformReportRequest{FromDate: "2026-11-23", ToDate: "2026-11-25"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("seller GetPlatformReport must be PermissionDenied, got %v", err)
	}

	// 2. Date range validation (366 OK, 367 rejected, inverted rejected).
	if _, err := h.svc.GetSellerReport(seller1Ctx, &orderv1.GetSellerReportRequest{FromDate: "2024-01-01", ToDate: "2024-12-31"}); err != nil {
		t.Fatalf("366-day leap year range should succeed, got %v", err)
	}
	if _, err := h.svc.GetSellerReport(seller1Ctx, &orderv1.GetSellerReportRequest{FromDate: "2024-01-01", ToDate: "2025-01-01"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("367-day range must return InvalidArgument, got %v", err)
	}
	if _, err := h.svc.GetSellerReport(seller1Ctx, &orderv1.GetSellerReportRequest{FromDate: "2026-11-25", ToDate: "2026-11-23"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("inverted date range must return InvalidArgument, got %v", err)
	}

	// 3. Seller 1 report for 2026-11-23..2026-11-25 (VN time).
	sRep, err := h.svc.GetSellerReport(seller1Ctx, &orderv1.GetSellerReportRequest{
		FromDate: "2026-11-23",
		ToDate:   "2026-11-25",
		TopLimit: 5,
	})
	if err != nil {
		t.Fatalf("GetSellerReport(shop-1) failed: %v", err)
	}
	if sRep.ShopId != "shop-1" {
		t.Fatalf("shop_id = %q, want shop-1", sRep.ShopId)
	}
	if sRep.TotalOrders != 2 || sRep.CompletedOrders != 1 || sRep.CancelledOrders != 1 || sRep.ReturnedOrders != 0 {
		t.Fatalf("unexpected shop-1 counts: total=%d comp=%d canc=%d ret=%d", sRep.TotalOrders, sRep.CompletedOrders, sRep.CancelledOrders, sRep.ReturnedOrders)
	}
	if sRep.CancelRateBps != 5000 || sRep.ReturnRateBps != 0 {
		t.Fatalf("unexpected rates: cancel=%d return=%d, want 5000/0", sRep.CancelRateBps, sRep.ReturnRateBps)
	}
	if sRep.GmvVnd != 1020000 {
		t.Fatalf("shop-1 GMV = %d, want 1020000 (excludes cancelled order 2 and shop-2 order 3)", sRep.GmvVnd)
	}
	if sRep.NetRevenueVnd != 909800 || sRep.CommissionVnd != 50000 || sRep.PaymentFeeVnd != 10200 {
		t.Fatalf("shop-1 financials: net=%d comm=%d payFee=%d, want 909800/50000/10200", sRep.NetRevenueVnd, sRep.CommissionVnd, sRep.PaymentFeeVnd)
	}
	if len(sRep.Daily) != 3 {
		t.Fatalf("expected 3 daily points (23, 24, 25), got %d", len(sRep.Daily))
	}
	// 2026-11-23: Order 1 created (16:59:59Z = 23:59:59 VN) -> order_count=1, gmv=1020000, completed=0
	if sRep.Daily[0].Date != "2026-11-23" || sRep.Daily[0].OrderCount != 1 || sRep.Daily[0].GmvVnd != 1020000 || sRep.Daily[0].CompletedCount != 0 {
		t.Fatalf("unexpected 2026-11-23 point: %+v", sRep.Daily[0])
	}
	// 2026-11-24: Order 2 created (17:00:00Z = 00:00:00 VN next day!) and cancelled -> order_count=1, cancelled=1, gmv=0
	if sRep.Daily[1].Date != "2026-11-24" || sRep.Daily[1].OrderCount != 1 || sRep.Daily[1].CancelledCount != 1 || sRep.Daily[1].GmvVnd != 0 {
		t.Fatalf("unexpected 2026-11-24 point: %+v", sRep.Daily[1])
	}
	// 2026-11-25: Order 1 completed (2026-11-24T17:30:00Z = 2026-11-25 00:30:00 VN!) -> completed=1, net=909800
	if sRep.Daily[2].Date != "2026-11-25" || sRep.Daily[2].CompletedCount != 1 || sRep.Daily[2].NetRevenueVnd != 909800 {
		t.Fatalf("unexpected 2026-11-25 point: %+v", sRep.Daily[2])
	}
	if len(sRep.TopProducts) != 1 || sRep.TopProducts[0].ProductId != "prod-1" || sRep.TopProducts[0].UnitsSold != 1 || sRep.TopProducts[0].RevenueVnd != 1000000 {
		t.Fatalf("unexpected top_products: %+v", sRep.TopProducts)
	}

	// 4. Platform report aggregates both shops + platform_subsidy_vnd = 50,000.
	pRep, err := h.svc.GetPlatformReport(adminCtx, &orderv1.GetPlatformReportRequest{
		FromDate: "2026-11-23",
		ToDate:   "2026-11-25",
	})
	if err != nil {
		t.Fatalf("GetPlatformReport failed: %v", err)
	}
	// Order 3 (shop-2): G=500,000, V_plat=50,000, Ship=30,000 -> R=500,000, D=50,000, P=480,000, P+D=530,000, C_cat=40,000, C_pay=4,800 -> Net=455,200.
	// Round 4 E: gmv_vnd = SUM(P + D) = 1,020,000 + (480,000 + 50,000) = 1,550,000.
	if pRep.TotalOrders != 3 || pRep.CompletedOrders != 2 || pRep.GmvVnd != 1020000+530000 {
		t.Fatalf("unexpected platform totals: total=%d comp=%d gmv=%d", pRep.TotalOrders, pRep.CompletedOrders, pRep.GmvVnd)
	}
	if pRep.PlatformSubsidyVnd != 50000 || pRep.NetRevenueVnd != 909800+455200 {
		t.Fatalf("unexpected platform subsidy/net: subsidy=%d net=%d, want 50000/%d", pRep.PlatformSubsidyVnd, pRep.NetRevenueVnd, 909800+455200)
	}
}

// TestW2B_AdminManualReviewQueue_AndVisibility verifies Wave 2b item 4:
// ListOrdersNeedingReview, ResolveOrderReview (admin-only), and needs_manual_review hidden from buyer/seller.
func TestW2B_AdminManualReviewQueue_AndVisibility(t *testing.T) {
	h := newTestHarness()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
	adminCtx := ctxWithUser("admin-1", "admin", "")

	resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "w2b-review-ord",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	if err != nil {
		t.Fatalf("CreateCheckout: %v", err)
	}
	ordID := resp.Orders[0].Id
	if err := h.store.MarkOrderOrphanShipmentManualReview(context.Background(), ordID, "orphan shipment already picked up"); err != nil {
		t.Fatalf("MarkOrderOrphanShipmentManualReview: %v", err)
	}

	// Visibility: buyer and seller MUST see needs_manual_review = false; admin MUST see true!
	buyerGet, err := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ordID})
	if err != nil || buyerGet.Order.NeedsManualReview {
		t.Fatalf("buyer must not see needs_manual_review=true, got err=%v val=%v", err, buyerGet.GetOrder().GetNeedsManualReview())
	}
	sellerGet, err := h.svc.GetOrder(sellerCtx, &orderv1.GetOrderRequest{OrderId: ordID})
	if err != nil || sellerGet.Order.NeedsManualReview {
		t.Fatalf("seller must not see needs_manual_review=true, got err=%v val=%v", err, sellerGet.GetOrder().GetNeedsManualReview())
	}
	sellerList, err := h.svc.ListOrders(sellerCtx, &orderv1.ListOrdersRequest{RoleScope: "seller"})
	if err != nil || len(sellerList.Orders) != 1 || sellerList.Orders[0].NeedsManualReview {
		t.Fatalf("seller ListOrders must not expose needs_manual_review=true")
	}
	adminGet, err := h.svc.GetOrder(adminCtx, &orderv1.GetOrderRequest{OrderId: ordID})
	if err != nil || !adminGet.Order.NeedsManualReview {
		t.Fatalf("admin must see needs_manual_review=true, got err=%v val=%v", err, adminGet.GetOrder().GetNeedsManualReview())
	}

	// RBAC: non-admins cannot list or resolve manual review queue.
	if _, err := h.svc.ListOrdersNeedingReview(buyerCtx, &orderv1.ListOrdersNeedingReviewRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("buyer ListOrdersNeedingReview must fail PermissionDenied, got %v", err)
	}
	if _, err := h.svc.ListOrdersNeedingReview(sellerCtx, &orderv1.ListOrdersNeedingReviewRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("seller ListOrdersNeedingReview must fail PermissionDenied, got %v", err)
	}
	if _, err := h.svc.ResolveOrderReview(sellerCtx, &orderv1.ResolveOrderReviewRequest{OrderId: ordID, Note: "nope"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("seller ResolveOrderReview must fail PermissionDenied, got %v", err)
	}

	// Admin lists queue -> sees 1 order with needs_manual_review=true.
	listResp, err := h.svc.ListOrdersNeedingReview(adminCtx, &orderv1.ListOrdersNeedingReviewRequest{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("ListOrdersNeedingReview failed: %v", err)
	}
	if listResp.Total != 1 || len(listResp.Orders) != 1 || listResp.Orders[0].Id != ordID || !listResp.Orders[0].NeedsManualReview {
		t.Fatalf("unexpected review queue: total=%d orders=%+v", listResp.Total, listResp.Orders)
	}

	// Admin resolves review -> clears flag, appends timeline event.
	resResp, err := h.svc.ResolveOrderReview(adminCtx, &orderv1.ResolveOrderReviewRequest{
		OrderId: ordID,
		Note:    "Contacted GHN to recall parcel",
	})
	if err != nil {
		t.Fatalf("ResolveOrderReview failed: %v", err)
	}
	if resResp.Order.NeedsManualReview {
		t.Fatalf("expected needs_manual_review=false after resolve")
	}
	foundNote := false
	for _, ev := range resResp.Order.Timeline {
		if strings.Contains(ev.Note, "Contacted GHN to recall parcel") {
			foundNote = true
		}
	}
	if !foundNote {
		t.Fatalf("expected resolve note appended to order timeline, got %+v", resResp.Order.Timeline)
	}

	// Second resolve must fail with FailedPrecondition, and queue is now empty.
	if _, err := h.svc.ResolveOrderReview(adminCtx, &orderv1.ResolveOrderReviewRequest{OrderId: ordID, Note: "dup"}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("second ResolveOrderReview must return FailedPrecondition, got %v", err)
	}
	listAfter, err := h.svc.ListOrdersNeedingReview(adminCtx, &orderv1.ListOrdersNeedingReviewRequest{})
	if err != nil || listAfter.Total != 0 || len(listAfter.Orders) != 0 {
		t.Fatalf("expected empty review queue after resolve, got total=%d len=%d err=%v", listAfter.Total, len(listAfter.Orders), err)
	}
}

// TestW2B_CancelReturnPartialRefund_WithShopProgramAndFreeship verifies Wave 2b item 5:
// refunds never exceed P (after P_shop and S_shop) across partial refunds, cancel, and return,
// and promotions (including shop discount program limits) are released cleanly.
func TestW2B_CancelReturnPartialRefund_WithShopProgramAndFreeship(t *testing.T) {
	h := newTestHarness()
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	h.svc.nowFn = func() time.Time { return now }
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	seller2Ctx := ctxWithUser("seller-2", "seller", "shop-2")

	h.shipping.shopSettings = map[string]*shippingv1.ShopShippingSettings{
		"shop-1": {ShopId: "shop-1", DefaultWeightGrams: 500, FreeShipEnabled: true, FreeShipMinOrderVnd: 500000, FreeShipMaxVnd: 20000},
		"shop-2": {ShopId: "shop-2", DefaultWeightGrams: 500, FreeShipEnabled: true, FreeShipMinOrderVnd: 300000, FreeShipMaxVnd: 30000},
	}
	h.promo.activeShopDiscounts = []*promotionv1.ActiveShopDiscount{
		{
			ItemId: "sp-s1", ProgramId: "p-s1", ShopId: "shop-1", SkuId: "sku-1",
			OriginalPriceVnd: 1000000, DiscountedPriceVnd: 800000,
			StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(time.Hour)),
		},
		{
			ItemId: "sp-s2", ProgramId: "p-s2", ShopId: "shop-2", SkuId: "sku-2",
			OriginalPriceVnd: 500000, DiscountedPriceVnd: 400000,
			StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(time.Hour)),
		},
	}
	// shop-1: G=1,000,000, P_shop=200,000, Ship=30,000, S_shop=20,000 -> R=780,000, P1=810,000
	// shop-2: G=500,000,   P_shop=100,000, Ship=30,000, S_shop=30,000 -> R=370,000, P2=400,000
	h.promo.breakdown = &promotionv1.DiscountBreakdown{
		Shops: []*promotionv1.ShopDiscountBreakdown{
			{ShopId: "shop-1", SubtotalVnd: 1000000, ShopProgramDiscountVnd: 200000, ShopFreeshipDiscountVnd: 20000, ShippingFeeVnd: 30000, AppliedShopProgramItemIds: []string{"sp-s1"}},
			{ShopId: "shop-2", SubtotalVnd: 500000, ShopProgramDiscountVnd: 100000, ShopFreeshipDiscountVnd: 30000, ShippingFeeVnd: 30000, AppliedShopProgramItemIds: []string{"sp-s2"}},
		},
	}

	resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "w2b-cancel-return-sp",
		AddressId:      "addr-buyer-1",
		Items: []*orderv1.CheckoutItemInput{
			{SkuId: "sku-1", Quantity: 1},
			{SkuId: "sku-2", Quantity: 1},
		},
		PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
	})
	if err != nil {
		t.Fatalf("CreateCheckout: %v", err)
	}
	if err := h.svc.HandlePaymentSucceeded(context.Background(), "w2b-pay-ok", events.PaymentSucceededPayload{
		PaymentID: resp.PaymentId, OrderGroupID: resp.CheckoutGroupId, AmountVND: resp.TotalPayableVnd,
	}); err != nil {
		t.Fatalf("HandlePaymentSucceeded: %v", err)
	}

	o1, o2 := resp.Orders[0], resp.Orders[1]
	if o1.TotalPayableVnd != 810000 || o2.TotalPayableVnd != 400000 {
		t.Fatalf("unexpected sub-order payables: P1=%d P2=%d, want 810000/400000", o1.TotalPayableVnd, o2.TotalPayableVnd)
	}

	// 1. Partial refund of 150,000 on o1, then buyer cancels o1 while o2 is active.
	if err := h.svc.HandlePaymentRefunded(context.Background(), "w2b-pref-1", events.PaymentRefundedPayload{
		RefundID: "ref-part-o1", PaymentID: resp.PaymentId, OrderID: o1.Id, AmountVND: 150000, Reason: "partial",
	}); err != nil {
		t.Fatalf("HandlePaymentRefunded: %v", err)
	}
	if _, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: o1.Id, Reason: "cancel o1"}); err != nil {
		t.Fatalf("CancelOrder(o1): %v", err)
	}
	if len(h.payment.refundCalls) != 1 || h.payment.refundCalls[0].AmountVnd != 660000 {
		t.Fatalf("expected cancel refund of remaining P1-150k = 660000, got %+v", h.payment.refundCalls)
	}
	if len(h.promo.releaseShopCalls) != 1 || h.promo.releaseShopCalls[0].ShopId != "shop-1" {
		t.Fatalf("expected ReleasePromotionShop(shop-1), got %+v", h.promo.releaseShopCalls)
	}

	// 2. Deliver o2, partial refund 50,000, then return + seller ACCEPT_REFUND -> refunds exact remaining 350,000 (total 400,000 == P2) & releases shop-2 promos!
	if err := h.svc.HandleShipmentUpdated(context.Background(), "w2b-del-o2", events.ShipmentUpdatedPayload{
		OrderID: o2.Id, Status: "delivered",
	}); err != nil {
		t.Fatalf("deliver o2: %v", err)
	}
	if err := h.svc.HandlePaymentRefunded(context.Background(), "w2b-pref-2", events.PaymentRefundedPayload{
		RefundID: "ref-part-o2", PaymentID: resp.PaymentId, OrderID: o2.Id, AmountVND: 50000, Reason: "partial o2",
	}); err != nil {
		t.Fatalf("partial refund o2: %v", err)
	}
	retResp, err := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{
		OrderId: o2.Id, Reason: "defective",
	})
	if err != nil {
		t.Fatalf("RequestReturn(o2): %v", err)
	}
	respRet, err := h.svc.RespondReturn(seller2Ctx, &orderv1.RespondReturnRequest{
		ReturnRequestId: retResp.ReturnRequestId,
		Decision:        orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_REFUND,
		Note:            "approved",
	})
	if err != nil {
		t.Fatalf("RespondReturn(ACCEPT_REFUND): %v", err)
	}
	if respRet.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_REFUNDED {
		t.Fatalf("expected return REFUNDED, got %v", respRet.ReturnRequest.Status)
	}
	if len(h.payment.refundCalls) != 2 || h.payment.refundCalls[1].AmountVnd != 350000 {
		t.Fatalf("expected return refund of remaining P2-50k = 350000, got %+v", h.payment.refundCalls)
	}
	if len(h.promo.releaseShopCalls) != 2 || h.promo.releaseShopCalls[1].ShopId != "shop-2" {
		t.Fatalf("expected ReleasePromotionShop(shop-2) on return, got %+v", h.promo.releaseShopCalls)
	}
}

// TestW2B_ReviewFixes verifies manager review fixes P1 (0 bps category fee + 0..10000 validation),
// P2 (authoritative ReservePromotion breakdown for P_shop/S_shop + compensation on mismatch),
// and P3 (missing Payment.GetFeeConfig active entry fails checkout clearly).
func TestW2B_ReviewFixes(t *testing.T) {
	baseNow := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)

	t.Run("P1_ZeroCategoryCommissionBps_NoFallback", func(t *testing.T) {
		h := newTestHarness()
		h.svc.nowFn = func() time.Time { return baseNow }
		h.product.skus["sku-1"].PriceVnd = 500000
		h.product.skus["sku-1"].CommissionRateBps = 0 // admin configured 0% category fee
		h.payment.feeBPSByMethod = map[paymentv1.PaymentMethod]int32{
			paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR: 100, // 1% payment fee
		}

		resp, err := h.svc.CreateCheckout(ctxWithUser("buyer-1", "buyer", ""), &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w2b-rev-zero-bps",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout failed for 0 bps category: %v", err)
		}
		ord := resp.Orders[0]
		if ord.CommissionVnd != 0 {
			t.Fatalf("commission_vnd = %d, want 0 (must not fall back to 5%%)", ord.CommissionVnd)
		}
		if ord.Items[0].CommissionBps != 0 {
			t.Fatalf("item.commission_bps = %d, want 0", ord.Items[0].CommissionBps)
		}
		// Persisted DB item must also store 0 bps.
		agg, err := h.store.GetOrderWithDetails(context.Background(), ord.Id)
		if err != nil {
			t.Fatalf("GetOrderWithDetails: %v", err)
		}
		if agg.Items[0].CommissionRateBPS != 0 {
			t.Fatalf("persisted order_items.commission_rate_bps = %d, want 0", agg.Items[0].CommissionRateBPS)
		}
		// Payment fee still computed: P = 500,000 + 30,000 = 530,000 -> C_pay = 5,300; ShopAllocation.CommissionVnd = 0 + 5,300.
		if agg.Order.CommissionVND != 0 || agg.Order.PaymentFeeVND != 5300 {
			t.Fatalf("persisted fees = (%d, %d), want (0, 5300)", agg.Order.CommissionVND, agg.Order.PaymentFeeVND)
		}
		if al := h.payment.createCalls[0].ShopAllocations[0]; al.CommissionVnd != 5300 {
			t.Fatalf("allocation commission_vnd = %d, want 5300 (C_cat=0 + C_pay=5300)", al.CommissionVnd)
		}
	})

	t.Run("P1_OutOfRangeCategoryCommissionBps_FailsCheckout", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			bps  int32
		}{
			{name: "negative", bps: -1},
			{name: "over_10000", bps: 10001},
		} {
			t.Run(tc.name, func(t *testing.T) {
				h := newTestHarness()
				h.svc.nowFn = func() time.Time { return baseNow }
				h.product.skus["sku-1"].CommissionRateBps = tc.bps

				_, err := h.svc.CreateCheckout(ctxWithUser("buyer-1", "buyer", ""), &orderv1.CreateCheckoutRequest{
					IdempotencyKey: "w2b-rev-bad-bps-" + tc.name,
					AddressId:      "addr-buyer-1",
					Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
					PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
				})
				if err == nil {
					t.Fatalf("expected checkout to fail for commission_rate_bps=%d", tc.bps)
				}
				if len(h.product.reserveCalls) != 0 {
					t.Fatalf("must not reserve stock for invalid commission_rate_bps=%d, got %d calls", tc.bps, len(h.product.reserveCalls))
				}
			})
		}
	})

	t.Run("P2_ShopProgramBreakdownZeroOrMismatch_FailsAndCompensatesBoth", func(t *testing.T) {
		for _, tc := range []struct {
			name             string
			appliedIDs       []string
			breakdownProgVND int64
		}{
			{name: "applied_item_but_breakdown_returns_zero", appliedIDs: []string{"spi-1"}, breakdownProgVND: 0},
			{name: "applied_item_but_price_changed", appliedIDs: []string{"spi-1"}, breakdownProgVND: 100000},
			{name: "empty_applied_but_nonzero_discount", appliedIDs: nil, breakdownProgVND: 100000},
		} {
			t.Run(tc.name, func(t *testing.T) {
				h := newTestHarness()
				h.svc.nowFn = func() time.Time { return baseNow }
				h.product.skus["sku-1"].PriceVnd = 1000000
				h.promo.activeShopDiscounts = []*promotionv1.ActiveShopDiscount{
					{
						ItemId:             "spi-1",
						ProgramId:          "sp-1",
						ShopId:             "shop-1",
						SkuId:              "sku-1",
						OriginalPriceVnd:   1000000,
						DiscountedPriceVnd: 850000, // expected P_shop = 150,000 when applied
						StartsAt:           timestamppb.New(baseNow.Add(-time.Hour)),
						EndsAt:             timestamppb.New(baseNow.Add(time.Hour)),
					},
				}
				h.promo.breakdown = &promotionv1.DiscountBreakdown{
					Shops: []*promotionv1.ShopDiscountBreakdown{
						{
							ShopId:                    "shop-1",
							SubtotalVnd:               1000000,
							ShopProgramDiscountVnd:    tc.breakdownProgVND,
							ShippingFeeVnd:            30000,
							AppliedShopProgramItemIds: tc.appliedIDs,
						},
					},
				}

				_, err := h.svc.CreateCheckout(ctxWithUser("buyer-1", "buyer", ""), &orderv1.CreateCheckoutRequest{
					IdempotencyKey: "w2b-rev-p2-" + tc.name,
					AddressId:      "addr-buyer-1",
					Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
					PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
				})
				if err == nil {
					t.Fatalf("expected checkout to fail when breakdown P_shop=%d != expected 150000", tc.breakdownProgVND)
				}
				if status.Code(err) != codes.FailedPrecondition {
					t.Fatalf("expected FailedPrecondition, got %v", err)
				}
				if len(h.product.releaseCalls) != 1 {
					t.Fatalf("expected ReleaseStock compensation, got %d calls", len(h.product.releaseCalls))
				}
				if len(h.promo.releaseCalls) != 1 {
					t.Fatalf("expected ReleasePromotion compensation, got %d calls", len(h.promo.releaseCalls))
				}
				if len(h.payment.createCalls) != 0 {
					t.Fatalf("must not create payment on breakdown mismatch, got %d calls", len(h.payment.createCalls))
				}
			})
		}
	})

	t.Run("P2_ShopFreeshipBreakdownZero_AuthoritativeNoLocalOverride", func(t *testing.T) {
		h := newTestHarness()
		h.svc.nowFn = func() time.Time { return baseNow }
		h.product.skus["sku-1"].PriceVnd = 500000
		h.shipping.shopSettings = map[string]*shippingv1.ShopShippingSettings{
			"shop-1": {
				ShopId:              "shop-1",
				DefaultWeightGrams:  500,
				FreeShipEnabled:     true,
				FreeShipMinOrderVnd: 100000,
				FreeShipMaxVnd:      20000,
			},
		}
		// ReservePromotion IS called (shop voucher present), and promotion returns S_shop=0.
		h.promo.breakdown = &promotionv1.DiscountBreakdown{
			Shops: []*promotionv1.ShopDiscountBreakdown{
				{
					ShopId:                  "shop-1",
					SubtotalVnd:             500000,
					ShopVoucherDiscountVnd:  50000,
					ShopFreeshipDiscountVnd: 0, // authoritative 0 from promotion
					ShippingFeeVnd:          30000,
				},
			},
		}

		resp, err := h.svc.CreateCheckout(ctxWithUser("buyer-1", "buyer", ""), &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w2b-rev-p2-sshop-zero",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			ShopVoucherIds: map[string]string{"shop-1": "v-shop-1"},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout failed: %v", err)
		}
		ord := resp.Orders[0]
		if ord.ShopFreeshipDiscountVnd != 0 {
			t.Fatalf("shop_freeship_discount_vnd = %d, want 0 (must trust ReservePromotion breakdown)", ord.ShopFreeshipDiscountVnd)
		}
		// R = 500,000 - 50,000 = 450,000; Ship = 30,000; P = 480,000.
		if ord.TotalPayableVnd != 480000 {
			t.Fatalf("total_payable_vnd = %d, want 480000", ord.TotalPayableVnd)
		}
	})

	t.Run("P3_MissingActivePaymentFeeConfig_FailsCheckoutClearly", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			resp *paymentv1.GetFeeConfigResponse
		}{
			{
				name: "empty_active_configs",
				resp: &paymentv1.GetFeeConfigResponse{ActiveConfigs: []*paymentv1.PaymentFeeConfigItem{}},
			},
			{
				name: "only_other_method_configured",
				resp: &paymentv1.GetFeeConfigResponse{
					ActiveConfigs: []*paymentv1.PaymentFeeConfigItem{
						{Id: "fee-cod", PaymentMethod: paymentv1.PaymentMethod_PAYMENT_METHOD_COD, FeeBps: 0},
					},
				},
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				h := newTestHarness()
				h.svc.nowFn = func() time.Time { return baseNow }
				h.payment.feeConfigResp = tc.resp

				_, err := h.svc.CreateCheckout(ctxWithUser("buyer-1", "buyer", ""), &orderv1.CreateCheckoutRequest{
					IdempotencyKey: "w2b-rev-p3-" + tc.name,
					AddressId:      "addr-buyer-1",
					Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
					PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
				})
				if err == nil {
					t.Fatalf("expected checkout to fail when active payment fee config is missing (%s)", tc.name)
				}
				if status.Code(err) != codes.FailedPrecondition {
					t.Fatalf("expected FailedPrecondition, got %v", err)
				}
				if len(h.product.reserveCalls) != 0 {
					t.Fatalf("must not reserve stock when payment fee config is missing, got %d calls", len(h.product.reserveCalls))
				}
			})
		}
	})
}

// TestW2B_ReviewRound3 verifies round-3 manager review requirements:
//  1. applied_shop_program_item_ids controls which lines get program price & order_items.shop_program_item_id;
//     excluded lines (e.g. per_user_limit exceeded) are billed at regular price with shop_program_item_id="".
//  2. Unknown applied_shop_program_item_ids or P_shop mismatch fails checkout and compensates stock + promotion.
//  3. Flash sale discount F comes strictly from ReservePromotion breakdown; mismatch fails with FLASH_SALE_PRICE_CHANGED + compensates.
//  4. Sub-order cancel releases shop promotions cleanly with order_items containing only applied program IDs.
func TestW2B_ReviewRound3(t *testing.T) {
	baseNow := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)

	t.Run("OneShopTwoLines_OnlyOneApplied_PricesCommissionRPDAndSubOrderCancel", func(t *testing.T) {
		h := newTestHarness()
		h.svc.nowFn = func() time.Time { return baseNow }
		h.product.skus["sku-a"] = &productv1.SKU{
			Id: "sku-a", ProductId: "prod-a", ShopId: "shop-1", ProductName: "SKU A", Name: "Default",
			PriceVnd: 500000, Stock: 20, AvailableStock: 20, WeightGrams: 300,
			CategoryId: "cat-a", CommissionRateBps: 600, IsActive: true,
		}
		h.product.skus["sku-b"] = &productv1.SKU{
			Id: "sku-b", ProductId: "prod-b", ShopId: "shop-1", ProductName: "SKU B", Name: "Default",
			PriceVnd: 300000, Stock: 20, AvailableStock: 20, WeightGrams: 200,
			CategoryId: "cat-b", CommissionRateBps: 500, IsActive: true,
		}
		h.product.skus["sku-2"].PriceVnd = 200000
		h.product.skus["sku-2"].CommissionRateBps = 500

		h.payment.feeBPSByMethod = map[paymentv1.PaymentMethod]int32{
			paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR: 100, // 1%
		}
		h.promo.activeShopDiscounts = []*promotionv1.ActiveShopDiscount{
			{
				ItemId: "sp-a", ProgramId: "prog-1", ShopId: "shop-1", SkuId: "sku-a",
				OriginalPriceVnd: 500000, DiscountedPriceVnd: 400000, PerUserLimit: 1,
				StartsAt: timestamppb.New(baseNow.Add(-time.Hour)), EndsAt: timestamppb.New(baseNow.Add(time.Hour)),
			},
			{
				ItemId: "sp-b", ProgramId: "prog-1", ShopId: "shop-1", SkuId: "sku-b",
				OriginalPriceVnd: 300000, DiscountedPriceVnd: 250000, PerUserLimit: 5,
				StartsAt: timestamppb.New(baseNow.Add(-time.Hour)), EndsAt: timestamppb.New(baseNow.Add(time.Hour)),
			},
		}
		// Only sp-b is applied (sp-a exceeded per_user_limit and is billed at regular 500,000).
		// Shop-1: G = 500k + 300k*2 = 1,100,000; P_shop = (300k-250k)*2 = 100,000; V_shop = 20,000; S_shop = 10,000
		// -> R = 970,000; Ship = 30,000; D = 15,000 -> P = 985,000
		// C_cat = 500k*6% (30,000) + 500k*5% (25,000) = 55,000; C_pay = 985,000*1% = 9,850
		h.promo.breakdown = &promotionv1.DiscountBreakdown{
			Shops: []*promotionv1.ShopDiscountBreakdown{
				{
					ShopId:                     "shop-1",
					SubtotalVnd:                1100000,
					ShopProgramDiscountVnd:     100000,
					ShopVoucherDiscountVnd:     20000,
					ShopFreeshipDiscountVnd:    10000,
					PlatformVoucherDiscountVnd: 15000,
					ShippingFeeVnd:             30000,
					AppliedShopProgramItemIds:  []string{"sp-b"},
				},
				{
					ShopId:         "shop-2",
					SubtotalVnd:    200000,
					ShippingFeeVnd: 30000,
				},
			},
		}

		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "w2b-r3-two-lines",
			AddressId:         "addr-buyer-1",
			Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-a", Quantity: 1}, {SkuId: "sku-b", Quantity: 2}, {SkuId: "sku-2", Quantity: 1}},
			ShopVoucherIds:    map[string]string{"shop-1": "v-s1"},
			PlatformVoucherId: "v-plat",
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout failed: %v", err)
		}
		if len(resp.Orders) != 2 {
			t.Fatalf("expected 2 sub-orders, got %d", len(resp.Orders))
		}
		o1 := resp.Orders[0]
		if o1.ShopId != "shop-1" {
			t.Fatalf("expected shop-1 first, got %s", o1.ShopId)
		}
		if o1.ShopProgramDiscountVnd != 100000 || o1.TotalPayableVnd != 985000 {
			t.Fatalf("shop-1 P_shop=%d P=%d, want 100000 / 985000", o1.ShopProgramDiscountVnd, o1.TotalPayableVnd)
		}
		a1, _ := h.svc.GetOrder(ctxWithUser("admin-1", "admin", ""), &orderv1.GetOrderRequest{OrderId: o1.Id})
		if a1.Order.CommissionVnd != 55000 || a1.Order.PaymentFeeVnd != 9850 {
			t.Fatalf("shop-1 C_cat=%d C_pay=%d, want 55000 / 9850", a1.Order.CommissionVnd, a1.Order.PaymentFeeVnd)
		}
		itemsBySKU := make(map[string]*orderv1.OrderItem)
		for _, it := range o1.Items {
			itemsBySKU[it.SkuId] = it
		}
		itA, itB := itemsBySKU["sku-a"], itemsBySKU["sku-b"]
		if itA.EffectiveUnitPriceVnd != 500000 || itA.LineTotalVnd != 500000 || itA.ShopProgramItemId != "" {
			t.Fatalf("unapplied sku-a must be billed at regular price with empty shop_program_item_id: %+v", itA)
		}
		if itB.EffectiveUnitPriceVnd != 250000 || itB.LineTotalVnd != 500000 || itB.ShopProgramItemId != "sp-b" {
			t.Fatalf("applied sku-b must have discounted price and shop_program_item_id=sp-b: %+v", itB)
		}

		// Verify persisted order_items in store match exactly (only sp-b stored).
		agg1, err := h.store.GetOrderWithDetails(context.Background(), o1.Id)
		if err != nil {
			t.Fatalf("GetOrderWithDetails: %v", err)
		}
		for _, dbIt := range agg1.Items {
			if dbIt.SKUID == "sku-a" && dbIt.ShopProgramItemID != "" {
				t.Fatalf("persisted sku-a shop_program_item_id = %q, want empty", dbIt.ShopProgramItemID)
			}
			if dbIt.SKUID == "sku-b" && dbIt.ShopProgramItemID != "sp-b" {
				t.Fatalf("persisted sku-b shop_program_item_id = %q, want sp-b", dbIt.ShopProgramItemID)
			}
		}

		// Verify Payment ShopAllocation matches R=970k, Ship=30k, D=15k, C_tot=64,850.
		al1 := h.payment.createCalls[0].ShopAllocations[0]
		if al1.SubtotalVnd != 970000 || al1.ShippingFeeVnd != 30000 || al1.PlatformDiscountVnd != 15000 || al1.CommissionVnd != 64850 {
			t.Fatalf("shop-1 allocation = %+v, want R=970000 Ship=30000 D=15000 C=64850", al1)
		}

		// Confirm payment and cancel only shop-1 sub-order -> ReleasePromotionShop(shop-1).
		if err := h.svc.HandlePaymentSucceeded(context.Background(), "r3-pay-ok", events.PaymentSucceededPayload{
			PaymentID: resp.PaymentId, OrderGroupID: resp.CheckoutGroupId, AmountVND: resp.TotalPayableVnd,
		}); err != nil {
			t.Fatalf("HandlePaymentSucceeded: %v", err)
		}
		if _, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: o1.Id, Reason: "cancel sub-order"}); err != nil {
			t.Fatalf("CancelOrder(o1): %v", err)
		}
		if len(h.promo.releaseShopCalls) != 1 || h.promo.releaseShopCalls[0].ShopId != "shop-1" {
			t.Fatalf("expected ReleasePromotionShop(shop-1), got %+v", h.promo.releaseShopCalls)
		}
	})

	t.Run("EmptyAppliedList_BillsAtRegularPriceAndClearsItemID", func(t *testing.T) {
		h := newTestHarness()
		h.svc.nowFn = func() time.Time { return baseNow }
		h.product.skus["sku-1"].PriceVnd = 500000
		h.product.skus["sku-1"].CommissionRateBps = 500
		h.promo.activeShopDiscounts = []*promotionv1.ActiveShopDiscount{
			{
				ItemId: "sp-exhausted", ProgramId: "prog-1", ShopId: "shop-1", SkuId: "sku-1",
				OriginalPriceVnd: 500000, DiscountedPriceVnd: 400000, PerUserLimit: 1,
				StartsAt: timestamppb.New(baseNow.Add(-time.Hour)), EndsAt: timestamppb.New(baseNow.Add(time.Hour)),
			},
		}
		h.promo.breakdown = &promotionv1.DiscountBreakdown{
			Shops: []*promotionv1.ShopDiscountBreakdown{
				{
					ShopId:                    "shop-1",
					SubtotalVnd:               500000,
					ShopProgramDiscountVnd:    0,
					ShippingFeeVnd:            30000,
					AppliedShopProgramItemIds: nil, // per_user_limit reached
				},
			},
		}

		resp, err := h.svc.CreateCheckout(ctxWithUser("buyer-1", "buyer", ""), &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w2b-r3-empty-applied",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout should succeed at regular price when applied list is empty: %v", err)
		}
		ord := resp.Orders[0]
		aOrd, _ := h.svc.GetOrder(ctxWithUser("admin-1", "admin", ""), &orderv1.GetOrderRequest{OrderId: ord.Id})
		if ord.ShopProgramDiscountVnd != 0 || ord.TotalPayableVnd != 530000 || aOrd.Order.CommissionVnd != 25000 {
			t.Fatalf("unexpected regular-price totals: P_shop=%d P=%d C_cat=%d, want 0/530000/25000",
				ord.ShopProgramDiscountVnd, ord.TotalPayableVnd, aOrd.Order.CommissionVnd)
		}
		if ord.Items[0].EffectiveUnitPriceVnd != 500000 || ord.Items[0].ShopProgramItemId != "" {
			t.Fatalf("item must use regular price & empty shop_program_item_id: %+v", ord.Items[0])
		}
	})

	t.Run("UnknownAppliedShopProgramItemID_FailsAndCompensatesBoth", func(t *testing.T) {
		h := newTestHarness()
		h.svc.nowFn = func() time.Time { return baseNow }
		h.product.skus["sku-1"].PriceVnd = 500000
		h.promo.activeShopDiscounts = []*promotionv1.ActiveShopDiscount{
			{
				ItemId: "sp-known", ProgramId: "prog-1", ShopId: "shop-1", SkuId: "sku-1",
				OriginalPriceVnd: 500000, DiscountedPriceVnd: 400000,
				StartsAt: timestamppb.New(baseNow.Add(-time.Hour)), EndsAt: timestamppb.New(baseNow.Add(time.Hour)),
			},
		}
		h.promo.breakdown = &promotionv1.DiscountBreakdown{
			Shops: []*promotionv1.ShopDiscountBreakdown{
				{
					ShopId:                    "shop-1",
					SubtotalVnd:               500000,
					ShopProgramDiscountVnd:    100000,
					ShippingFeeVnd:            30000,
					AppliedShopProgramItemIds: []string{"sp-stranger"},
				},
			},
		}

		_, err := h.svc.CreateCheckout(ctxWithUser("buyer-1", "buyer", ""), &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w2b-r3-unknown-id",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err == nil || status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "SHOP_PROGRAM_ITEM_UNKNOWN") {
			t.Fatalf("expected FailedPrecondition SHOP_PROGRAM_ITEM_UNKNOWN, got %v", err)
		}
		if len(h.product.releaseCalls) != 1 || len(h.promo.releaseCalls) != 1 || len(h.payment.createCalls) != 0 {
			t.Fatalf("expected full compensation (stock=1 promo=1 pay=0), got stock=%d promo=%d pay=%d",
				len(h.product.releaseCalls), len(h.promo.releaseCalls), len(h.payment.createCalls))
		}
	})

	t.Run("FlashSaleBreakdownMismatch_FailsAndCompensatesBoth", func(t *testing.T) {
		for _, tc := range []struct {
			name      string
			breakdown *promotionv1.DiscountBreakdown
		}{
			{
				name: "breakdown_returns_zero",
				breakdown: &promotionv1.DiscountBreakdown{
					Shops: []*promotionv1.ShopDiscountBreakdown{
						{ShopId: "shop-1", SubtotalVnd: 1000000, FlashSaleDiscountVnd: 0, ShippingFeeVnd: 30000},
					},
				},
			},
			{
				name: "breakdown_price_changed",
				breakdown: &promotionv1.DiscountBreakdown{
					Shops: []*promotionv1.ShopDiscountBreakdown{
						{ShopId: "shop-1", SubtotalVnd: 1000000, FlashSaleDiscountVnd: 150000, ShippingFeeVnd: 30000},
					},
				},
			},
			{
				name:      "breakdown_nil",
				breakdown: nil,
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				h := newTestHarness()
				h.svc.nowFn = func() time.Time { return baseNow }
				h.product.skus["sku-1"].PriceVnd = 1000000
				h.promo.flashSales = []*promotionv1.FlashSale{
					{
						Id:       "fs-r3",
						StartsAt: timestamppb.New(baseNow.Add(-5 * time.Minute)),
						EndsAt:   timestamppb.New(baseNow.Add(30 * time.Minute)),
						Items: []*promotionv1.FlashSaleItem{
							{Id: "fsi-r3", SkuId: "sku-1", ShopId: "shop-1", FlashPriceVnd: 800000, Status: promotionv1.FlashSaleItemStatus_FLASH_SALE_ITEM_STATUS_APPROVED},
						},
					},
				}
				h.promo.breakdown = tc.breakdown

				_, err := h.svc.CreateCheckout(ctxWithUser("buyer-1", "buyer", ""), &orderv1.CreateCheckoutRequest{
					IdempotencyKey: "w2b-r3-flash-" + tc.name,
					AddressId:      "addr-buyer-1",
					Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1, FlashSaleItemId: "fsi-r3"}},
					PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
				})
				if err == nil || status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "FLASH_SALE_PRICE_CHANGED") {
					t.Fatalf("expected FailedPrecondition FLASH_SALE_PRICE_CHANGED for %s, got %v", tc.name, err)
				}
				if len(h.product.releaseCalls) != 1 || len(h.promo.releaseCalls) != 1 || len(h.payment.createCalls) != 0 {
					t.Fatalf("expected full compensation for %s (stock=1 promo=1 pay=0), got stock=%d promo=%d pay=%d",
						tc.name, len(h.product.releaseCalls), len(h.promo.releaseCalls), len(h.payment.createCalls))
				}
			})
		}
	})
}
