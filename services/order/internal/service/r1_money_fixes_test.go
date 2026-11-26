package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/services/order/internal/clients"
	"github.com/nexusmart/core/services/order/internal/db"
	"github.com/nexusmart/core/services/order/internal/events"
	"github.com/nexusmart/core/services/order/internal/resilience"
	"github.com/nexusmart/core/services/order/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestR1Money1_CODAddressUpdate_FreeshipFloor(t *testing.T) {
	t.Run("StorefrontShopFreeship_30kTo10k_KeepsPayable100k", func(t *testing.T) {
		h := newTestHarness()
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

		h.product.skus["sku-1"].PriceVnd = 100000
		h.shipping.shopSettings = map[string]*shippingv1.ShopShippingSettings{
			"shop-1": {
				ShopId:              "shop-1",
				FreeShipEnabled:     true,
				FreeShipMinOrderVnd: 50000,
				FreeShipMaxVnd:      30000,
			},
		}
		h.shipping.quotes = []*shippingv1.CarrierQuote{
			{CarrierCode: "ghn", ServiceCode: "standard", FeeVnd: 30000, CodSupported: true},
		}

		co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey:   "r1-m1-sf-shop-freeship",
			AddressId:        "addr-buyer-1",
			Items:            []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:    orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
			SalesChannel:     SalesChannelStorefront,
			StorefrontShopId: "shop-1",
		})
		if err != nil {
			t.Fatalf("CreateCheckout storefront: %v", err)
		}
		ord := co.GetOrders()[0]
		if ord.GetShippingFeeVnd() != 30000 || ord.GetShopFreeshipDiscountVnd() != 30000 || ord.GetTotalPayableVnd() != 100000 {
			t.Fatalf("unexpected initial order totals: ship=%d shopFreeship=%d payable=%d",
				ord.GetShippingFeeVnd(), ord.GetShopFreeshipDiscountVnd(), ord.GetTotalPayableVnd())
		}

		if _, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord.GetId(), ShopId: "shop-1"}); err != nil {
			t.Fatalf("SellerShipOrder: %v", err)
		}

		addrInner := registerBuyerAddr(h, "addr-inner-1", "buyer-1", "Tran Thi Binh", "0912345678", "79", "TP. Ho Chi Minh", "26735", "Tan Dinh", "12 Inner St")
		h.shipping.quotes = []*shippingv1.CarrierQuote{
			{CarrierCode: "ghn", ServiceCode: "standard", FeeVnd: 10000, CodSupported: true},
		}

		// Preview must clamp applied shipping fee to 30k -> delta 0, payable 100k, fee_adjusted=false.
		prev, err := h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
			OrderId:     ord.GetId(),
			AddressId:   addrInner,
			PreviewOnly: true,
		})
		if err != nil {
			t.Fatalf("preview: %v", err)
		}
		if prev.GetNewShippingFeeVnd() != 30000 || prev.GetFeeDeltaVnd() != 0 ||
			prev.GetNewTotalPayableVnd() != 100000 || prev.GetFeeAdjusted() || prev.GetRequiresFeeConfirmation() {
			t.Fatalf("preview broke freeship floor: %+v", prev)
		}

		// Apply must keep payable=100k, shipping_fee=30k, skip AdjustCODShippingFee, and recreate COD shipment for 100k.
		applied, err := h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
			OrderId:        ord.GetId(),
			AddressId:      addrInner,
			IdempotencyKey: "r1-m1-sf-apply",
		})
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if applied.GetFeeAdjusted() || applied.GetFeeDeltaVnd() != 0 ||
			applied.GetNewShippingFeeVnd() != 30000 || applied.GetNewTotalPayableVnd() != 100000 {
			t.Fatalf("apply response broke freeship floor: %+v", applied)
		}
		gotOrd := applied.GetOrder()
		if gotOrd.GetShippingFeeVnd() != 30000 || gotOrd.GetTotalPayableVnd() != 100000 {
			t.Fatalf("order totals mutated below freeship floor: ship=%d payable=%d", gotOrd.GetShippingFeeVnd(), gotOrd.GetTotalPayableVnd())
		}
		if gotOrd.GetShopFreeshipDiscountVnd()+gotOrd.GetFreeshipDiscountVnd() > gotOrd.GetShippingFeeVnd() {
			t.Fatalf("invariant violated: shop_freeship(%d) + freeship(%d) > shipping_fee(%d)",
				gotOrd.GetShopFreeshipDiscountVnd(), gotOrd.GetFreeshipDiscountVnd(), gotOrd.GetShippingFeeVnd())
		}
		if len(h.payment.adjustCODCalls) != 0 {
			t.Fatalf("expected 0 AdjustCODShippingFee calls when applied fee unchanged, got %d (%+v)", len(h.payment.adjustCODCalls), h.payment.adjustCODCalls)
		}
		lastShip := h.shipping.createShipCalls[len(h.shipping.createShipCalls)-1]
		if lastShip.GetCodAmountVnd() != 100000 {
			t.Fatalf("recreated COD shipment amount = %d, want 100000", lastShip.GetCodAmountVnd())
		}
	})

	t.Run("MarketplacePlatformFreeship_30kTo10k_KeepsPayable100k", func(t *testing.T) {
		h := newTestHarness()
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		h.product.skus["sku-1"].PriceVnd = 100000
		h.promo.breakdown = &promotionv1.DiscountBreakdown{
			Shops: []*promotionv1.ShopDiscountBreakdown{
				{
					ShopId:              "shop-1",
					SubtotalVnd:         100000,
					FreeshipDiscountVnd: 30000,
					ShippingFeeVnd:      30000,
				},
			},
		}
		co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "r1-m1-mp-plat-freeship",
			AddressId:         "addr-buyer-1",
			Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			FreeshipVoucherId: "v-free-30k",
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil {
			t.Fatalf("CreateCheckout marketplace: %v", err)
		}
		ord := co.GetOrders()[0]
		if ord.GetShippingFeeVnd() != 30000 || ord.GetFreeshipDiscountVnd() != 30000 || ord.GetTotalPayableVnd() != 100000 {
			t.Fatalf("unexpected marketplace totals: ship=%d freeship=%d payable=%d",
				ord.GetShippingFeeVnd(), ord.GetFreeshipDiscountVnd(), ord.GetTotalPayableVnd())
		}

		addrInner := registerBuyerAddr(h, "addr-mp-inner", "buyer-1", "Tran Thi Binh", "0912345678", "79", "TP. Ho Chi Minh", "26735", "Tan Dinh", "12 Inner St")
		h.shipping.quotes = []*shippingv1.CarrierQuote{
			{CarrierCode: "ghn", ServiceCode: "standard", FeeVnd: 10000, CodSupported: true},
		}

		resp, err := h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
			OrderId:        ord.GetId(),
			AddressId:      addrInner,
			IdempotencyKey: "r1-m1-mp-apply",
		})
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if resp.GetFeeAdjusted() || resp.GetFeeDeltaVnd() != 0 ||
			resp.GetOrder().GetShippingFeeVnd() != 30000 || resp.GetOrder().GetTotalPayableVnd() != 100000 {
			t.Fatalf("marketplace freeship floor broken: %+v", resp)
		}
		if len(h.payment.adjustCODCalls) != 0 {
			t.Fatalf("expected 0 AdjustCODShippingFee calls, got %d", len(h.payment.adjustCODCalls))
		}
	})

	t.Run("PartialReduction_50kFreeship30k_NewQuote10k_Applies30k", func(t *testing.T) {
		h := newTestHarness()
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		h.product.skus["sku-1"].PriceVnd = 100000
		h.shipping.shopSettings = map[string]*shippingv1.ShopShippingSettings{
			"shop-1": {
				ShopId:              "shop-1",
				FreeShipEnabled:     true,
				FreeShipMinOrderVnd: 50000,
				FreeShipMaxVnd:      30000,
			},
		}
		h.shipping.quotes = []*shippingv1.CarrierQuote{
			{CarrierCode: "ghn", ServiceCode: "standard", FeeVnd: 50000, CodSupported: true},
		}

		co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey:   "r1-m1-partial-50k",
			AddressId:        "addr-buyer-1",
			Items:            []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:    orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
			SalesChannel:     SalesChannelStorefront,
			StorefrontShopId: "shop-1",
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		ord := co.GetOrders()[0]
		if ord.GetShippingFeeVnd() != 50000 || ord.GetShopFreeshipDiscountVnd() != 30000 || ord.GetTotalPayableVnd() != 120000 {
			t.Fatalf("unexpected initial 50k totals: ship=%d sf=%d payable=%d",
				ord.GetShippingFeeVnd(), ord.GetShopFreeshipDiscountVnd(), ord.GetTotalPayableVnd())
		}

		addrNear := registerBuyerAddr(h, "addr-near-10k", "buyer-1", "Tran Thi Binh", "0912345678", "79", "TP. Ho Chi Minh", "26735", "Tan Dinh", "12 Near St")
		h.shipping.quotes = []*shippingv1.CarrierQuote{
			{CarrierCode: "ghn", ServiceCode: "standard", FeeVnd: 10000, CodSupported: true},
		}

		prev, err := h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
			OrderId:     ord.GetId(),
			AddressId:   addrNear,
			PreviewOnly: true,
		})
		if err != nil {
			t.Fatalf("preview partial reduction: %v", err)
		}
		if prev.GetNewShippingFeeVnd() != 30000 || prev.GetFeeDeltaVnd() != -20000 ||
			prev.GetNewTotalPayableVnd() != 100000 || !prev.GetFeeAdjusted() || prev.GetRequiresFeeConfirmation() {
			t.Fatalf("preview partial reduction mismatch: %+v", prev)
		}

		applied, err := h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
			OrderId:        ord.GetId(),
			AddressId:      addrNear,
			IdempotencyKey: "r1-m1-partial-apply",
		})
		if err != nil {
			t.Fatalf("apply partial reduction: %v", err)
		}
		if !applied.GetFeeAdjusted() || applied.GetNewShippingFeeVnd() != 30000 ||
			applied.GetFeeDeltaVnd() != -20000 || applied.GetNewTotalPayableVnd() != 100000 ||
			applied.GetOrder().GetShippingFeeVnd() != 30000 || applied.GetOrder().GetTotalPayableVnd() != 100000 {
			t.Fatalf("apply partial reduction mismatch: %+v", applied)
		}
		if len(h.payment.adjustCODCalls) != 1 || h.payment.adjustCODCalls[0].GetNewShippingFeeVnd() != 30000 {
			t.Fatalf("expected 1 AdjustCODShippingFee(30000), got %+v", h.payment.adjustCODCalls)
		}

		// Store-layer guard: OpenOrderAddressChange must reject AppliedShippingFeeVND < freeshipFloor directly.
		addrOther := registerBuyerAddr(h, "addr-other-store", "buyer-1", "Tran Thi Binh", "0912345678", "01", "Ha Noi", "00002", "Trang Tien", "1 Other")
		_ = addrOther
		newAddrBytes, _ := json.Marshal(&orderv1.OrderAddressSnapshot{RecipientName: "Test", RecipientPhone: "0912345678", ProvinceCode: "01", WardCode: "00002", StreetAddress: "1 Other"})
		_, openErr := h.store.OpenOrderAddressChange(context.Background(), store.OpenAddressChangeParams{
			ID:                        uuid.NewString(),
			OrderID:                   ord.GetId(),
			BuyerID:                   "buyer-1",
			RequestHash:               "h-direct-low",
			NewAddressJSON:            newAddrBytes,
			CarrierCode:               "ghn",
			ServiceCode:               "standard",
			ExpectedOldShippingFeeVND: 30000,
			ExpectedOldTotalPayable:   100000,
			NewShippingFeeVND:         10000,
			AppliedShippingFeeVND:     10000,
			NewTotalPayableVND:        80000,
			FeeAdjusted:               true,
			NeedsCODAdjust:            true,
			ClaimToken:                "tok-low",
			LeaseDuration:             30 * time.Second,
			Now:                       time.Now().UTC(),
		})
		if !errors.Is(openErr, store.ErrAddressUpdateNotAllowed) {
			t.Fatalf("store OpenOrderAddressChange below freeship floor: got %v, want ErrAddressUpdateNotAllowed", openErr)
		}
	})

	t.Run("FeeIncreaseConfirmWithFreeship_UsesAppliedFee", func(t *testing.T) {
		h := newTestHarness()
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		h.product.skus["sku-1"].PriceVnd = 100000
		h.shipping.shopSettings = map[string]*shippingv1.ShopShippingSettings{
			"shop-1": {ShopId: "shop-1", FreeShipEnabled: true, FreeShipMinOrderVnd: 50000, FreeShipMaxVnd: 30000},
		}
		h.shipping.quotes = []*shippingv1.CarrierQuote{
			{CarrierCode: "ghn", ServiceCode: "standard", FeeVnd: 30000, CodSupported: true},
		}
		co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey:   "r1-m1-inc-freeship",
			AddressId:        "addr-buyer-1",
			Items:            []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:    orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
			SalesChannel:     SalesChannelStorefront,
			StorefrontShopId: "shop-1",
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		ord := co.GetOrders()[0]
		addrFar := registerBuyerAddr(h, "addr-far-45k", "buyer-1", "Tran Thi Binh", "0912345678", "01", "Ha Noi", "00002", "Trang Tien", "99 Far")
		h.shipping.quotes = []*shippingv1.CarrierQuote{
			{CarrierCode: "ghn", ServiceCode: "standard", FeeVnd: 45000, CodSupported: true},
		}
		_, err = h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
			OrderId:                   ord.GetId(),
			AddressId:                 addrFar,
			AcceptFeeIncrease:         true,
			ExpectedNewShippingFeeVnd: 40000,
		})
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "ADDRESS_FEE_CHANGED") {
			t.Fatalf("stale confirm expected ADDRESS_FEE_CHANGED, got %v", err)
		}
		applied, err := h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
			OrderId:                   ord.GetId(),
			AddressId:                 addrFar,
			AcceptFeeIncrease:         true,
			ExpectedNewShippingFeeVnd: 45000,
			IdempotencyKey:            "r1-m1-inc-ok",
		})
		if err != nil {
			t.Fatalf("apply fee increase: %v", err)
		}
		if applied.GetOrder().GetShippingFeeVnd() != 45000 || applied.GetOrder().GetTotalPayableVnd() != 115000 {
			t.Fatalf("fee increase totals mismatch: ship=%d payable=%d", applied.GetOrder().GetShippingFeeVnd(), applied.GetOrder().GetTotalPayableVnd())
		}
	})
}

func TestR1Money2_Phase0bPartialReturn_RefundAmountRestockAndEscrowSettlement(t *testing.T) {
	t.Run("MigratedPartialReturn_RefundsPartial_RestocksItemsJSON_AndCompletesRemainingOrder", func(t *testing.T) {
		h := newTestHarness()
		sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
		now := time.Now().UTC()

		ordID := "ord-p0b-partial-1"
		grpID := "grp-p0b-partial-1"
		h.store.SeedLegacyOrder(
			db.CheckoutGroup{
				ID: grpID, BuyerID: "buyer-1", Status: "COMPLETED", PaymentMethod: "VIETQR",
				PaymentID: "pay-p0b-1", PaymentStatus: "SUCCEEDED", TotalPayableVND: 300000,
				SalesChannel: SalesChannelStorefront, StorefrontShopID: "shop-1", CreatedAt: now, UpdatedAt: now,
			},
			db.Order{
				ID: ordID, OrderCode: "NM-P0B-PART-1", CheckoutGroupID: grpID, BuyerID: "buyer-1", ShopID: "shop-1",
				Status: "RETURN_REQUESTED", PaymentMethod: "VIETQR", PaymentID: "pay-p0b-1", PaymentStatus: "SUCCEEDED",
				SubtotalVND: 300000, TotalPayableVND: 300000, RefundedVND: 0,
				SalesChannel: SalesChannelStorefront, StorefrontShopID: "shop-1", CreatedAt: now, UpdatedAt: now,
			},
			[]db.OrderItem{
				{ID: "item-a", OrderID: ordID, SKUID: "SKU-A", ProductID: "p-a", ProductName: "Shirt A", UnitPriceVND: 100000, EffectiveUnitPriceVND: 100000, Quantity: 1, LineTotalVND: 100000},
				{ID: "item-b", OrderID: ordID, SKUID: "SKU-B", ProductID: "p-b", ProductName: "Jacket B", UnitPriceVND: 200000, EffectiveUnitPriceVND: 200000, Quantity: 1, LineTotalVND: 200000},
			},
		)
		h.store.SeedReturnRequestForTest(db.ReturnRequest{
			ID:              "rr-p0b-partial-1",
			OrderID:         ordID,
			OrderCode:       "NM-P0B-PART-1",
			BuyerID:         "buyer-1",
			ShopID:          "shop-1",
			Reason:          "SKU-A defective",
			ItemsJSON:       []byte(`[{"order_item_id":"item-a","sku_id":"SKU-A","quantity":1}]`),
			Status:          "RECEIVED",
			GoodsReceived:   true,
			RefundAmountVND: 100000,
			CreatedAt:       now,
			UpdatedAt:       now,
		})

		resp, err := h.svc.ConfirmReturnReceived(sellerCtx, &orderv1.ConfirmReturnReceivedRequest{
			ReturnRequestId: "rr-p0b-partial-1",
		})
		if err != nil {
			t.Fatalf("ConfirmReturnReceived: %v", err)
		}
		if resp.GetReturnRequest().GetStatus() != orderv1.ReturnStatus_RETURN_STATUS_REFUNDED {
			t.Fatalf("return status = %v, want REFUNDED", resp.GetReturnRequest().GetStatus())
		}
		if resp.GetReturnRequest().GetRefundAmountVnd() != 100000 {
			t.Fatalf("return refund_amount_vnd = %d, want 100000 (must NOT overwrite with 300000)", resp.GetReturnRequest().GetRefundAmountVnd())
		}
		if len(h.payment.refundCalls) != 1 || h.payment.refundCalls[0].GetAmountVnd() != 100000 {
			t.Fatalf("CreateRefund calls = %+v, want 1 call with AmountVnd=100000", h.payment.refundCalls)
		}
		if len(h.product.restockCalls) != 1 {
			t.Fatalf("expected 1 RestockOrderItems call, got %d", len(h.product.restockCalls))
		}
		restocked := h.product.restockCalls[0].GetItems()
		if len(restocked) != 1 || restocked[0].GetSkuId() != "SKU-A" || restocked[0].GetQuantity() != 1 {
			t.Fatalf("RestockOrderItems must ONLY restock SKU-A x1, got %+v", restocked)
		}
		if len(h.promo.releaseShopCalls) != 0 {
			t.Fatalf("partial return with kept items must NOT release whole shop promotions, got %+v", h.promo.releaseShopCalls)
		}

		// Order must transition RETURN_REQUESTED -> COMPLETED and emit order.completed (not order.returned)
		// so payment settles the remaining 200k escrow to seller_pending instead of stranding it in 'held'.
		ordAgg, err := h.store.GetOrderWithDetails(context.Background(), ordID)
		if err != nil {
			t.Fatalf("GetOrderWithDetails: %v", err)
		}
		if ordAgg.Order.Status != "COMPLETED" {
			t.Fatalf("order status = %q, want COMPLETED so remaining 200k escrow settles to seller", ordAgg.Order.Status)
		}
		var sawCompleted, sawReturned bool
		for _, ev := range h.store.OutboxEvents() {
			switch ev.EventType {
			case events.SubjectOrderCompleted:
				var p events.OrderCompletedPayload
				if err := json.Unmarshal(ev.PayloadJSON, &p); err != nil {
					t.Fatalf("unmarshal order.completed: %v", err)
				}
				if p.OrderID == ordID {
					sawCompleted = true
					// Reward coins (default 100 bps = 1%) must be capped at kept 200k (<= 2000), not full 300k (3000).
					if p.CoinsReward > 2000 {
						t.Fatalf("order.completed CoinsReward = %d, must not reward refunded 100k (want <= 2000)", p.CoinsReward)
					}
				}
			case events.SubjectOrderReturned:
				sawReturned = true
			}
		}
		if !sawCompleted || sawReturned {
			t.Fatalf("outbox events for partial return: sawCompleted=%v sawReturned=%v, want true/false", sawCompleted, sawReturned)
		}
	})

	t.Run("MigratedPartialReturn_ClampedByPriorRefund_CompletesAsReturnedWhenZeroRemainder", func(t *testing.T) {
		h := newTestHarness()
		sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
		now := time.Now().UTC()

		ordID := "ord-p0b-partial-clamp"
		grpID := "grp-p0b-partial-clamp"
		h.store.SeedLegacyOrder(
			db.CheckoutGroup{
				ID: grpID, BuyerID: "buyer-1", Status: "COMPLETED", PaymentMethod: "VIETQR",
				PaymentID: "pay-p0b-2", PaymentStatus: "SUCCEEDED", TotalPayableVND: 300000, CreatedAt: now, UpdatedAt: now,
			},
			db.Order{
				ID: ordID, OrderCode: "NM-P0B-PART-2", CheckoutGroupID: grpID, BuyerID: "buyer-1", ShopID: "shop-1",
				Status: "RETURN_REQUESTED", PaymentMethod: "VIETQR", PaymentID: "pay-p0b-2", PaymentStatus: "SUCCEEDED",
				SubtotalVND: 300000, TotalPayableVND: 300000, RefundedVND: 250000, CreatedAt: now, UpdatedAt: now,
			},
			[]db.OrderItem{
				{ID: "item-a", OrderID: ordID, SKUID: "SKU-A", Quantity: 1, UnitPriceVND: 100000, EffectiveUnitPriceVND: 100000, LineTotalVND: 100000},
				{ID: "item-b", OrderID: ordID, SKUID: "SKU-B", Quantity: 1, UnitPriceVND: 200000, EffectiveUnitPriceVND: 200000, LineTotalVND: 200000},
			},
		)
		h.store.SeedReturnRequestForTest(db.ReturnRequest{
			ID:              "rr-p0b-clamp",
			OrderID:         ordID,
			OrderCode:       "NM-P0B-PART-2",
			BuyerID:         "buyer-1",
			ShopID:          "shop-1",
			Reason:          "SKU-A defective",
			ItemsJSON:       []byte(`[{"order_item_id":"item-a","sku_id":"SKU-A","quantity":1}]`),
			Status:          "RECEIVED",
			GoodsReceived:   true,
			RefundAmountVND: 100000,
			CreatedAt:       now,
			UpdatedAt:       now,
		})

		resp, err := h.svc.ConfirmReturnReceived(sellerCtx, &orderv1.ConfirmReturnReceivedRequest{
			ReturnRequestId: "rr-p0b-clamp",
		})
		if err != nil {
			t.Fatalf("ConfirmReturnReceived: %v", err)
		}
		if resp.GetReturnRequest().GetRefundAmountVnd() != 50000 {
			t.Fatalf("clamped refund_amount_vnd = %d, want 50000", resp.GetReturnRequest().GetRefundAmountVnd())
		}
		if len(h.payment.refundCalls) != 1 || h.payment.refundCalls[0].GetAmountVnd() != 50000 {
			t.Fatalf("CreateRefund = %+v, want 50000", h.payment.refundCalls)
		}
		if len(h.product.restockCalls) != 1 || len(h.product.restockCalls[0].GetItems()) != 1 || h.product.restockCalls[0].GetItems()[0].GetSkuId() != "SKU-A" {
			t.Fatalf("must restock only SKU-A, got %+v", h.product.restockCalls)
		}
		ordAgg, _ := h.store.GetOrderWithDetails(context.Background(), ordID)
		if ordAgg.Order.Status != "RETURNED" {
			t.Fatalf("when 0 remainder left, order status = %q, want RETURNED", ordAgg.Order.Status)
		}
	})

	t.Run("MainFullReturn_EmptyItemsJSON_BehaviorUnchanged", func(t *testing.T) {
		h := newTestHarness()
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
		now := time.Now().UTC()

		ordID := "ord-main-full-ret"
		grpID := "grp-main-full-ret"
		deliveredAt := now.Add(-1 * time.Hour)
		h.store.SeedLegacyOrder(
			db.CheckoutGroup{
				ID: grpID, BuyerID: "buyer-1", Status: "COMPLETED", PaymentMethod: "VIETQR",
				PaymentID: "pay-main-full", PaymentStatus: "SUCCEEDED", TotalPayableVND: 300000, CreatedAt: now, UpdatedAt: now,
			},
			db.Order{
				ID: ordID, OrderCode: "NM-MAIN-FULL-1", CheckoutGroupID: grpID, BuyerID: "buyer-1", ShopID: "shop-1",
				Status: "DELIVERED", PaymentMethod: "VIETQR", PaymentID: "pay-main-full", PaymentStatus: "SUCCEEDED",
				SubtotalVND: 300000, TotalPayableVND: 300000, RefundedVND: 0, DeliveredAt: &deliveredAt, CreatedAt: now, UpdatedAt: now,
			},
			[]db.OrderItem{
				{ID: "item-a", OrderID: ordID, SKUID: "SKU-A", Quantity: 1, UnitPriceVND: 100000, EffectiveUnitPriceVND: 100000, LineTotalVND: 100000},
				{ID: "item-b", OrderID: ordID, SKUID: "SKU-B", Quantity: 1, UnitPriceVND: 200000, EffectiveUnitPriceVND: 200000, LineTotalVND: 200000},
			},
		)

		retReq, err := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{
			OrderId:      ordID,
			Reason:       "full return",
			EvidenceUrls: []string{"https://cdn.example/ev1.jpg"},
		})
		if err != nil {
			t.Fatalf("RequestReturn: %v", err)
		}
		rrID := retReq.GetReturnRequestId()
		if _, err := h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: rrID,
			Decision:        orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_REFUND,
			Note:            "approve full refund",
		}); err != nil {
			t.Fatalf("RespondReturn ACCEPT_REFUND: %v", err)
		}
		if len(h.payment.refundCalls) != 1 || h.payment.refundCalls[0].GetAmountVnd() != 300000 {
			t.Fatalf("main full return CreateRefund = %+v, want 300000", h.payment.refundCalls)
		}
		if len(h.promo.releaseShopCalls) != 1 || h.promo.releaseShopCalls[0].GetSubOrderId() != ordID || h.promo.releaseShopCalls[0].GetClawbackOnly() {
			t.Fatalf("main full return ReleasePromotionShop = %+v, want sub_order_id=%s clawback_only=false", h.promo.releaseShopCalls, ordID)
		}
		ordAgg, _ := h.store.GetOrderWithDetails(context.Background(), ordID)
		if ordAgg.Order.Status != "RETURNED" {
			t.Fatalf("main full return order status = %q, want RETURNED", ordAgg.Order.Status)
		}
	})

	t.Run("Scenario1_PriorRefund150k_PartialReturn100k_RewardsOnlyKept50k_BothOrderings", func(t *testing.T) {
		for _, eventArrivesBeforeRefunded := range []bool{false, true} {
			name := "payment_refunded_not_yet_arrived"
			if eventArrivesBeforeRefunded {
				name = "payment_refunded_already_recorded"
			}
			t.Run(name, func(t *testing.T) {
				h := newTestHarness()
				sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
				now := time.Now().UTC()

				ordID := "ord-p0b-s1-" + name
				grpID := "grp-p0b-s1-" + name
				rrID := "rr-p0b-s1-" + name
				retRefundID := "ref-ret-s1-" + name

				h.store.SeedLegacyOrder(
					db.CheckoutGroup{
						ID: grpID, BuyerID: "buyer-1", Status: "COMPLETED", PaymentMethod: "VIETQR",
						PaymentID: "pay-s1", PaymentStatus: "SUCCEEDED", TotalPayableVND: 300000, CreatedAt: now, UpdatedAt: now,
					},
					db.Order{
						ID: ordID, OrderCode: "NM-P0B-S1", CheckoutGroupID: grpID, BuyerID: "buyer-1", ShopID: "shop-1",
						Status: "RETURN_REQUESTED", PaymentMethod: "VIETQR", PaymentID: "pay-s1", PaymentStatus: "SUCCEEDED",
						SubtotalVND: 300000, TotalPayableVND: 300000, RefundedVND: 0, CreatedAt: now, UpdatedAt: now,
					},
					[]db.OrderItem{
						{ID: "item-a", OrderID: ordID, SKUID: "SKU-A", Quantity: 1, UnitPriceVND: 100000, EffectiveUnitPriceVND: 100000, LineTotalVND: 100000},
						{ID: "item-b", OrderID: ordID, SKUID: "SKU-B", Quantity: 1, UnitPriceVND: 200000, EffectiveUnitPriceVND: 200000, LineTotalVND: 200000},
					},
				)
				// Prior manual refund of 150k -> ord.RefundedVND = 150k.
				if err := h.svc.HandlePaymentRefunded(context.Background(), "ev-prior-150k-"+name, events.PaymentRefundedPayload{
					RefundID: "ref-prior-150k", PaymentID: "pay-s1", OrderID: ordID, AmountVND: 150000, Reason: "prior manual refund",
				}); err != nil {
					t.Fatalf("prior refund: %v", err)
				}
				h.store.SeedReturnRequestForTest(db.ReturnRequest{
					ID: rrID, OrderID: ordID, OrderCode: "NM-P0B-S1", BuyerID: "buyer-1", ShopID: "shop-1",
					Reason: "SKU-A defective", ItemsJSON: []byte(`[{"order_item_id":"item-a","sku_id":"SKU-A","quantity":1}]`),
					Status: "RECEIVED", GoodsReceived: true, RefundAmountVND: 100000, CreatedAt: now, UpdatedAt: now,
				})
				h.payment.onCreateRefund = func(_ context.Context, req *paymentv1.CreateRefundRequest) (*paymentv1.CreateRefundResponse, error) {
					if eventArrivesBeforeRefunded {
						// Simulate payment.refunded DB commit arriving on a concurrent consumer before TransitionReturnRequest(REFUNDED).
						if _, err := h.store.RecordPaymentRefunded(context.Background(), store.PaymentRefundedParams{
							EventID: "ev-ret-100k-" + name, EventType: events.SubjectPaymentRefunded,
							RefundID: retRefundID, PaymentID: "pay-s1", OrderID: ordID,
							IdempotencyKey: req.GetIdempotencyKey(), AmountVND: req.GetAmountVnd(), Reason: "return refund",
						}); err != nil {
							t.Errorf("in-flight RecordPaymentRefunded: %v", err)
						}
					}
					return &paymentv1.CreateRefundResponse{
						Refund: &paymentv1.Refund{
							RefundId: retRefundID, PaymentId: req.GetPaymentId(), OrderId: req.GetOrderId(),
							AmountVnd: req.GetAmountVnd(), Status: paymentv1.RefundStatus_REFUND_STATUS_PENDING,
						},
					}, nil
				}

				resp, err := h.svc.ConfirmReturnReceived(sellerCtx, &orderv1.ConfirmReturnReceivedRequest{ReturnRequestId: rrID})
				if err != nil {
					t.Fatalf("ConfirmReturnReceived: %v", err)
				}
				if resp.GetReturnRequest().GetStatus() != orderv1.ReturnStatus_RETURN_STATUS_REFUNDED {
					t.Fatalf("return status = %v, want REFUNDED", resp.GetReturnRequest().GetStatus())
				}
				ordAgg, _ := h.store.GetOrderWithDetails(context.Background(), ordID)
				if ordAgg.Order.Status != "COMPLETED" {
					t.Fatalf("order status = %q, want COMPLETED", ordAgg.Order.Status)
				}
				if len(h.promo.releaseShopCalls) != 0 {
					t.Fatalf("kept 50k remainder must NOT release shop promotions, got %+v", h.promo.releaseShopCalls)
				}
				var gotCoins int64 = -1
				var sawReturned bool
				for _, ev := range h.store.OutboxEvents() {
					switch ev.EventType {
					case events.SubjectOrderCompleted:
						var p events.OrderCompletedPayload
						if err := json.Unmarshal(ev.PayloadJSON, &p); err != nil {
							t.Fatalf("unmarshal order.completed: %v", err)
						}
						if p.OrderID == ordID {
							gotCoins = p.CoinsReward
						}
					case events.SubjectOrderReturned:
						sawReturned = true
					}
				}
				// Default 100 bps (1%) on kept 50,000 VND = exactly 500 coins (NOT 1500!).
				if gotCoins != 500 || sawReturned {
					t.Fatalf("order.completed CoinsReward = %d (sawReturned=%v), want 500 / false", gotCoins, sawReturned)
				}
			})
		}
	})

	t.Run("Scenario2_ConcurrentAdminRefundReaches100Percent_MarksReturnedAndReleasesPromo", func(t *testing.T) {
		for _, raceTiming := range []string{"before_step2_promo_check", "between_step2_and_step4_refunded"} {
			t.Run(raceTiming, func(t *testing.T) {
				h := newTestHarness()
				sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
				now := time.Now().UTC()

				ordID := "ord-p0b-s2-" + raceTiming
				grpID := "grp-p0b-s2-" + raceTiming
				rrID := "rr-p0b-s2-" + raceTiming
				retRefundID := "ref-ret-s2-" + raceTiming

				h.store.SeedLegacyOrder(
					db.CheckoutGroup{
						ID: grpID, BuyerID: "buyer-1", Status: "COMPLETED", PaymentMethod: "VIETQR",
						PaymentID: "pay-s2", PaymentStatus: "SUCCEEDED", TotalPayableVND: 300000, CreatedAt: now, UpdatedAt: now,
					},
					db.Order{
						ID: ordID, OrderCode: "NM-P0B-S2", CheckoutGroupID: grpID, BuyerID: "buyer-1", ShopID: "shop-1",
						Status: "RETURN_REQUESTED", PaymentMethod: "VIETQR", PaymentID: "pay-s2", PaymentStatus: "SUCCEEDED",
						SubtotalVND: 300000, TotalPayableVND: 300000, RefundedVND: 0, CreatedAt: now, UpdatedAt: now,
					},
					[]db.OrderItem{
						{ID: "item-a", OrderID: ordID, SKUID: "SKU-A", Quantity: 1, UnitPriceVND: 100000, EffectiveUnitPriceVND: 100000, LineTotalVND: 100000},
						{ID: "item-b", OrderID: ordID, SKUID: "SKU-B", Quantity: 1, UnitPriceVND: 200000, EffectiveUnitPriceVND: 200000, LineTotalVND: 200000},
					},
				)
				// Prior manual refund of 100k -> ord.RefundedVND = 100k.
				if err := h.svc.HandlePaymentRefunded(context.Background(), "ev-s2-prior-"+raceTiming, events.PaymentRefundedPayload{
					RefundID: "ref-s2-prior", PaymentID: "pay-s2", OrderID: ordID, AmountVND: 100000, Reason: "prior 100k",
				}); err != nil {
					t.Fatalf("prior refund: %v", err)
				}
				h.store.SeedReturnRequestForTest(db.ReturnRequest{
					ID: rrID, OrderID: ordID, OrderCode: "NM-P0B-S2", BuyerID: "buyer-1", ShopID: "shop-1",
					Reason: "SKU-A defective", ItemsJSON: []byte(`[{"order_item_id":"item-a","sku_id":"SKU-A","quantity":1}]`),
					Status: "RECEIVED", GoodsReceived: true, RefundAmountVND: 100000, CreatedAt: now, UpdatedAt: now,
				})

				triggerAdminRefund := func() {
					// Admin concurrently refunds another 100k -> ord.RefundedVND = 200k;
					// combined with return's committed 100k (payment.refunded not yet arrived), total refunded = 300k (100%).
					if _, err := h.store.RecordPaymentRefunded(context.Background(), store.PaymentRefundedParams{
						EventID: "ev-s2-admin-" + raceTiming, EventType: events.SubjectPaymentRefunded,
						RefundID: "ref-s2-admin", PaymentID: "pay-s2", OrderID: ordID, AmountVND: 100000, Reason: "concurrent admin 100k",
					}); err != nil {
						t.Errorf("concurrent admin RecordPaymentRefunded: %v", err)
					}
				}

				h.payment.onCreateRefund = func(_ context.Context, req *paymentv1.CreateRefundRequest) (*paymentv1.CreateRefundResponse, error) {
					if raceTiming == "before_step2_promo_check" {
						triggerAdminRefund()
					}
					return &paymentv1.CreateRefundResponse{
						Refund: &paymentv1.Refund{
							RefundId: retRefundID, PaymentId: req.GetPaymentId(), OrderId: req.GetOrderId(),
							AmountVnd: req.GetAmountVnd(), Status: paymentv1.RefundStatus_REFUND_STATUS_PENDING,
						},
					}, nil
				}
				if raceTiming == "between_step2_and_step4_refunded" {
					h.product.onRestock = func(_ context.Context, _ *productv1.RestockOrderItemsRequest) {
						triggerAdminRefund()
					}
				}

				resp, err := h.svc.ConfirmReturnReceived(sellerCtx, &orderv1.ConfirmReturnReceivedRequest{ReturnRequestId: rrID})
				if err != nil {
					t.Fatalf("ConfirmReturnReceived: %v", err)
				}
				if resp.GetReturnRequest().GetStatus() != orderv1.ReturnStatus_RETURN_STATUS_REFUNDED {
					t.Fatalf("return status = %v, want REFUNDED", resp.GetReturnRequest().GetStatus())
				}
				ordAgg, _ := h.store.GetOrderWithDetails(context.Background(), ordID)
				if ordAgg.Order.Status != "RETURNED" {
					t.Fatalf("order status = %q, want RETURNED when effectiveRefunded reaches 100%%", ordAgg.Order.Status)
				}
				if len(h.promo.releaseShopCalls) != 1 ||
					h.promo.releaseShopCalls[0].GetSubOrderId() != ordID ||
					h.promo.releaseShopCalls[0].GetClawbackOnly() {
					t.Fatalf("expected 1 ReleasePromotionShop(sub_order_id=%s, clawback_only=false), got %+v", ordID, h.promo.releaseShopCalls)
				}
				var sawCompleted, sawReturned bool
				for _, ev := range h.store.OutboxEvents() {
					switch ev.EventType {
					case events.SubjectOrderCompleted:
						sawCompleted = true
					case events.SubjectOrderReturned:
						sawReturned = true
					}
				}
				if sawCompleted || !sawReturned {
					t.Fatalf("100%% refunded partial return outbox: sawCompleted=%v sawReturned=%v, want false/true", sawCompleted, sawReturned)
				}
			})
		}
	})
}

func TestR1Money4_StorefrontVerifyShopsActive_BreakerRetryNoDuplicate(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")

	t.Run("ActiveStorefrontCallsGetShopOnce", func(t *testing.T) {
		h := newTestHarness()
		_, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey:   "r1-m4-single-getshop",
			AddressId:        "addr-buyer-1",
			Items:            []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:    orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
			SalesChannel:     SalesChannelStorefront,
			StorefrontShopId: "shop-1",
		})
		if err != nil {
			t.Fatalf("CreateCheckout storefront: %v", err)
		}
		if len(h.user.getShopCalls) != 1 {
			t.Fatalf("STOREFRONT checkout called GetShop %d times (%v), want 1 (no duplicate)", len(h.user.getShopCalls), h.user.getShopCalls)
		}
	})

	t.Run("SuspendedStorefrontReturns409ShopSuspendedEvenWithRedactedSKU", func(t *testing.T) {
		h := newTestHarness()
		h.user.shops["shop-1"].Status = userv1.ShopStatus_SHOP_STATUS_SUSPENDED
		h.product.skus["sku-1"].IsActive = false

		_, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey:   "r1-m4-susp-sf",
			AddressId:        "addr-buyer-1",
			Items:            []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:    orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
			SalesChannel:     SalesChannelStorefront,
			StorefrontShopId: "shop-1",
		})
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "SHOP_SUSPENDED") {
			t.Fatalf("suspended STOREFRONT shop: got %v, want FailedPrecondition SHOP_SUSPENDED", err)
		}
		if len(h.product.reserveCalls) != 0 {
			t.Fatalf("must reserve 0 stock for suspended storefront shop")
		}
	})

	t.Run("StorefrontUsesCircuitBreakerAndRetry", func(t *testing.T) {
		h := newTestHarness()
		var attempts atomic.Int32
		h.user.onGetShop = func(_ context.Context, req *userv1.GetShopInternalRequest) (*userv1.GetShopInternalResponse, error) {
			if attempts.Add(1) == 1 {
				return nil, status.Error(codes.Unavailable, "transient user outage")
			}
			return &userv1.GetShopInternalResponse{Shop: h.user.shops[req.GetShopId()]}, nil
		}
		if _, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey:   "r1-m4-retry-sf",
			AddressId:        "addr-buyer-1",
			Items:            []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:    orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
			SalesChannel:     SalesChannelStorefront,
			StorefrontShopId: "shop-1",
		}); err != nil {
			t.Fatalf("STOREFRONT checkout should retry transient GetShop Unavailable and succeed, got %v", err)
		}
		if attempts.Load() != 2 {
			t.Fatalf("expected 2 GetShop attempts (1 transient + 1 retry), got %d", attempts.Load())
		}

		// Open circuit breaker must fail closed with Unavailable SHOP_STATUS_CHECK_UNAVAILABLE without calling GetShop.
		cb := resilience.NewCircuitBreaker(1, time.Minute)
		cb.RecordFailure()
		h.svc.SetUserShopBreaker(cb)
		attempts.Store(0)
		_, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey:   "r1-m4-cb-open",
			AddressId:        "addr-buyer-1",
			Items:            []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:    orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
			SalesChannel:     SalesChannelStorefront,
			StorefrontShopId: "shop-1",
		})
		if status.Code(err) != codes.Unavailable || !strings.Contains(err.Error(), "SHOP_STATUS_CHECK_UNAVAILABLE") {
			t.Fatalf("open breaker on STOREFRONT: got %v, want Unavailable SHOP_STATUS_CHECK_UNAVAILABLE", err)
		}
		if attempts.Load() != 0 || len(h.product.reserveCalls) != 1 {
			t.Fatalf("open breaker must not call GetShop or reserve stock: attempts=%d reserves=%d", attempts.Load(), len(h.product.reserveCalls))
		}
	})
}

func TestR1Money5_CompletedOrderFullRefund_ClawsBackRewardCoins(t *testing.T) {
	h := newTestHarness()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

	co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey:   "r1-m5-completed-refund",
		AddressId:        "addr-buyer-1",
		Items:            []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:    orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		SalesChannel:     SalesChannelStorefront,
		StorefrontShopId: "shop-1",
	})
	if err != nil {
		t.Fatalf("CreateCheckout: %v", err)
	}
	ord := co.GetOrders()[0]
	if ord.GetId() == co.GetCheckoutGroupId() {
		t.Fatalf("expected distinct sub_order_id and checkout_group_id")
	}

	shipResp, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord.GetId(), ShopId: "shop-1"})
	if err != nil {
		t.Fatalf("SellerShipOrder: %v", err)
	}
	if err := h.svc.HandleShipmentUpdated(context.Background(), "ev-m5-deliv", events.ShipmentUpdatedPayload{
		OrderID: ord.GetId(), Status: "delivered", TrackingCode: shipResp.GetTrackingCode(),
	}); err != nil {
		t.Fatalf("HandleShipmentUpdated delivered: %v", err)
	}
	if _, err := h.svc.BuyerConfirmReceived(buyerCtx, &orderv1.BuyerConfirmReceivedRequest{OrderId: ord.GetId()}); err != nil {
		t.Fatalf("BuyerConfirmReceived: %v", err)
	}

	// 1. Partial post-settlement refund (< total_payable) must NOT claw back reward coins yet.
	part1 := ord.GetTotalPayableVnd() / 2
	isPost := true
	if err := h.svc.HandlePaymentRefunded(context.Background(), "ev-m5-part1", events.PaymentRefundedPayload{
		RefundID: "ref-m5-1", PaymentID: co.GetPaymentId(), OrderID: ord.GetId(),
		AmountVND: part1, Reason: "partial post-settlement", IsPostSettlement: &isPost,
	}); err != nil {
		t.Fatalf("HandlePaymentRefunded part1: %v", err)
	}
	if len(h.promo.releaseShopCalls) != 0 {
		t.Fatalf("partial refund must not trigger clawback, got %+v", h.promo.releaseShopCalls)
	}

	// 2. Final refund reaches total_payable; simulate transient Promotion failure on 1st delivery -> must return error for consumer retry!
	rem := ord.GetTotalPayableVnd() - part1
	h.promo.releaseShopErr = status.Error(codes.Unavailable, "promotion temporarily down")
	err = h.svc.HandlePaymentRefunded(context.Background(), "ev-m5-part2", events.PaymentRefundedPayload{
		RefundID: "ref-m5-2", PaymentID: co.GetPaymentId(), OrderID: ord.GetId(),
		AmountVND: rem, Reason: "final post-settlement", IsPostSettlement: &isPost,
	})
	if err == nil {
		t.Fatalf("expected error on transient Promotion failure so NATS consumer retries")
	}

	// 3. Consumer redelivers SAME event_id "ev-m5-part2" once Promotion recovers -> must NOT double-add RefundedVND AND must invoke clawback_only with sub_order_id = ord.ID!
	h.promo.releaseShopErr = nil
	h.promo.releaseShopCalls = nil
	h.promo.releaseShopAuth = nil
	if err := h.svc.HandlePaymentRefunded(context.Background(), "ev-m5-part2", events.PaymentRefundedPayload{
		RefundID: "ref-m5-2", PaymentID: co.GetPaymentId(), OrderID: ord.GetId(),
		AmountVND: rem, Reason: "final post-settlement", IsPostSettlement: &isPost,
	}); err != nil {
		t.Fatalf("HandlePaymentRefunded redelivery: %v", err)
	}
	if len(h.promo.releaseShopCalls) != 1 {
		t.Fatalf("expected 1 ReleasePromotionShop call on completed order full refund, got %d", len(h.promo.releaseShopCalls))
	}
	call := h.promo.releaseShopCalls[0]
	if call.GetOrderId() != co.GetCheckoutGroupId() || call.GetShopId() != "shop-1" ||
		call.GetSubOrderId() != ord.GetId() || !call.GetClawbackOnly() {
		t.Fatalf("unexpected ReleasePromotionShopRequest: %+v (want group=%s shop=shop-1 sub=%s clawback_only=true)",
			call, co.GetCheckoutGroupId(), ord.GetId())
	}
	if len(h.promo.releaseShopAuth) != 1 || h.promo.releaseShopAuth[0].service != clients.InternalServiceName {
		t.Fatalf("ReleasePromotionShop must use order-service token, got %+v", h.promo.releaseShopAuth)
	}

	// 4. Verify non-completed return/cancel paths ALSO pass sub_order_id = ord.ID (and clawback_only = false).
	co2, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "r1-m5-multi-cancel",
		AddressId:      "addr-buyer-1",
		Items: []*orderv1.CheckoutItemInput{
			{SkuId: "sku-1", Quantity: 1},
			{SkuId: "sku-2", Quantity: 1},
		},
		PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	if err != nil {
		t.Fatalf("CreateCheckout multi: %v", err)
	}
	h.promo.releaseShopCalls = nil
	if _, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{
		OrderId: co2.GetOrders()[0].GetId(), Reason: "cancel sub1",
	}); err != nil {
		t.Fatalf("CancelOrder sub1: %v", err)
	}
	if len(h.promo.releaseShopCalls) != 1 ||
		h.promo.releaseShopCalls[0].GetSubOrderId() != co2.GetOrders()[0].GetId() ||
		h.promo.releaseShopCalls[0].GetClawbackOnly() {
		t.Fatalf("CancelOrder sub-order ReleasePromotionShop mismatch: %+v", h.promo.releaseShopCalls)
	}
}
