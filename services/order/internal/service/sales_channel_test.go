package service

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/order/internal/db"
	"github.com/nexusmart/core/services/order/internal/events"
	"github.com/nexusmart/core/services/order/migrations"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
)

func TestSalesChannel_DefaultMarketplaceAndValidation(t *testing.T) {
	h := newTestHarness()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")

	// 1. Default empty sales_channel resolves to MARKETPLACE and passes MARKETPLACE to PaymentService.
	resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "ch-default-mp",
		AddressId:      "addr-buyer-1",
		Items: []*orderv1.CheckoutItemInput{
			{SkuId: "sku-1", Quantity: 1},
		},
		PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	if err != nil {
		t.Fatalf("default marketplace checkout: %v", err)
	}
	if resp.GetSalesChannel() != SalesChannelMarketplace || resp.GetStorefrontShopId() != "" {
		t.Fatalf("checkout response channel=(%q,%q), want (MARKETPLACE, \"\")", resp.GetSalesChannel(), resp.GetStorefrontShopId())
	}
	if len(resp.GetOrders()) != 1 || resp.GetOrders()[0].GetSalesChannel() != SalesChannelMarketplace || resp.GetOrders()[0].GetStorefrontShopId() != "" {
		t.Fatalf("order channel mismatch: %+v", resp.GetOrders())
	}
	if len(h.payment.createCalls) != 1 {
		t.Fatalf("expected 1 CreatePayment call, got %d", len(h.payment.createCalls))
	}
	payReq := h.payment.createCalls[0]
	if payReq.GetSalesChannel() != SalesChannelMarketplace || payReq.GetStorefrontShopId() != "" {
		t.Fatalf("payment channel=(%q,%q), want (MARKETPLACE, \"\")", payReq.GetSalesChannel(), payReq.GetStorefrontShopId())
	}
	for _, alloc := range payReq.GetShopAllocations() {
		if alloc.GetSalesChannel() != SalesChannelMarketplace || alloc.GetStorefrontShopId() != "" {
			t.Fatalf("allocation channel=(%q,%q), want (MARKETPLACE, \"\")", alloc.GetSalesChannel(), alloc.GetStorefrontShopId())
		}
	}

	// 2. Reject unknown sales_channel, MARKETPLACE with storefront_shop_id, and STOREFRONT without storefront_shop_id — 0 stock reserved.
	prevReserves := len(h.product.reserveCalls)
	invalidCases := []struct {
		name  string
		ch    string
		shop  string
		idem  string
		match string
	}{
		{"unknown", "SOCIAL_LIVE", "", "ch-bad-1", "INVALID_SALES_CHANNEL"},
		{"marketplace_with_shop", "MARKETPLACE", "shop-1", "ch-bad-2", "INVALID_SALES_CHANNEL"},
		{"storefront_missing_shop", "STOREFRONT", "", "ch-bad-3", "STOREFRONT_SHOP_REQUIRED"},
	}
	for _, tc := range invalidCases {
		_, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey:   tc.idem,
			AddressId:        "addr-buyer-1",
			Items:            []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:    orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
			SalesChannel:     tc.ch,
			StorefrontShopId: tc.shop,
		})
		if status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), tc.match) {
			t.Fatalf("%s: want InvalidArgument(%s), got %v", tc.name, tc.match, err)
		}
	}
	if len(h.product.reserveCalls) != prevReserves {
		t.Fatalf("invalid channel checks must not reserve stock: before=%d after=%d", prevReserves, len(h.product.reserveCalls))
	}
}

func TestSalesChannel_StorefrontHappyPathCODAndVietQR(t *testing.T) {
	h := newTestHarness()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
	adminCtx := ctxWithUser("admin-1", "admin", "")

	// Configure shop voucher discount for shop-1.
	h.promo.breakdown = &promotionv1.DiscountBreakdown{
		Shops: []*promotionv1.ShopDiscountBreakdown{
			{
				ShopId:                 "shop-1",
				SubtotalVnd:            1000000,
				ShopVoucherId:          "voucher-shop-1",
				ShopVoucherDiscountVnd: 100000,
			},
		},
	}

	// 1. STOREFRONT COD with matching SHOP voucher.
	codResp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey:   "sf-cod-1",
		AddressId:        "addr-buyer-1",
		Items:            []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		ShopVoucherIds:   map[string]string{"shop-1": "voucher-shop-1"},
		PaymentMethod:    orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		SalesChannel:     SalesChannelStorefront,
		StorefrontShopId: "shop-1",
	})
	if err != nil {
		t.Fatalf("storefront COD checkout failed: %v", err)
	}
	if codResp.GetSalesChannel() != SalesChannelStorefront || codResp.GetStorefrontShopId() != "shop-1" {
		t.Fatalf("cod response channel=(%q,%q)", codResp.GetSalesChannel(), codResp.GetStorefrontShopId())
	}
	if len(codResp.GetOrders()) != 1 {
		t.Fatalf("expected 1 order, got %d", len(codResp.GetOrders()))
	}
	codOrd := codResp.GetOrders()[0]
	if codOrd.GetSalesChannel() != SalesChannelStorefront || codOrd.GetStorefrontShopId() != "shop-1" {
		t.Fatalf("cod order channel=(%q,%q)", codOrd.GetSalesChannel(), codOrd.GetStorefrontShopId())
	}
	// Commission math unchanged: 1,000,000 * 500 bps = 50,000 VND.
	if codOrd.GetCommissionVnd() != 50000 {
		t.Fatalf("expected unchanged commission 50000 VND, got %d", codOrd.GetCommissionVnd())
	}

	// PromotionService received sales_channel and storefront_shop_id.
	if len(h.promo.reserveReqs) != 1 ||
		h.promo.reserveReqs[0].GetSalesChannel() != SalesChannelStorefront ||
		h.promo.reserveReqs[0].GetStorefrontShopId() != "shop-1" {
		t.Fatalf("promotion reserve did not receive storefront channel: %+v", h.promo.reserveReqs)
	}

	// PaymentService received sales_channel and storefront_shop_id on request and allocations.
	if len(h.payment.createCalls) != 1 {
		t.Fatalf("expected 1 payment call, got %d", len(h.payment.createCalls))
	}
	payCod := h.payment.createCalls[0]
	if payCod.GetSalesChannel() != SalesChannelStorefront || payCod.GetStorefrontShopId() != "shop-1" {
		t.Fatalf("payment req channel=(%q,%q)", payCod.GetSalesChannel(), payCod.GetStorefrontShopId())
	}
	if len(payCod.GetShopAllocations()) != 1 ||
		payCod.GetShopAllocations()[0].GetSalesChannel() != SalesChannelStorefront ||
		payCod.GetShopAllocations()[0].GetStorefrontShopId() != "shop-1" {
		t.Fatalf("payment allocation channel mismatch: %+v", payCod.GetShopAllocations())
	}

	// Idempotent replay preserves sales_channel and storefront_shop_id.
	replayResp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey:   "sf-cod-1",
		AddressId:        "addr-buyer-1",
		Items:            []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:    orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		SalesChannel:     SalesChannelStorefront,
		StorefrontShopId: "shop-1",
	})
	if err != nil || !replayResp.GetIdempotentReplay() || replayResp.GetSalesChannel() != SalesChannelStorefront || replayResp.GetStorefrontShopId() != "shop-1" {
		t.Fatalf("idempotent replay channel mismatch: resp=%+v err=%v", replayResp, err)
	}

	// 2. STOREFRONT VietQR succeeds too.
	h.promo.breakdown = nil
	qrResp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey:   "sf-qr-1",
		AddressId:        "addr-buyer-1",
		Items:            []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:    orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		SalesChannel:     SalesChannelStorefront,
		StorefrontShopId: "shop-1",
	})
	if err != nil {
		t.Fatalf("storefront VietQR checkout failed: %v", err)
	}
	if qrResp.GetSalesChannel() != SalesChannelStorefront || qrResp.GetStorefrontShopId() != "shop-1" || qrResp.GetQrUrl() == "" {
		t.Fatalf("storefront VietQR unexpected response: %+v", qrResp)
	}

	// 3. Buyer, Seller, and Admin read/list RPCs all return sales_channel & storefront_shop_id.
	for name, ctx := range map[string]context.Context{"buyer": buyerCtx, "seller": sellerCtx, "admin": adminCtx} {
		got, err := h.svc.GetOrder(ctx, &orderv1.GetOrderRequest{OrderId: codOrd.GetId()})
		if err != nil {
			t.Fatalf("GetOrder (%s): %v", name, err)
		}
		if got.GetOrder().GetSalesChannel() != SalesChannelStorefront || got.GetOrder().GetStorefrontShopId() != "shop-1" {
			t.Fatalf("GetOrder (%s) channel=(%q,%q)", name, got.GetOrder().GetSalesChannel(), got.GetOrder().GetStorefrontShopId())
		}
	}

	// ListOrders filtering by sales_channel (buyer, seller, admin) + invalid filter rejection.
	for _, tc := range []struct {
		name string
		ctx  context.Context
		req  *orderv1.ListOrdersRequest
		want int64
	}{
		{"buyer_storefront", buyerCtx, &orderv1.ListOrdersRequest{RoleScope: "buyer", SalesChannel: "STOREFRONT"}, 2},
		{"buyer_marketplace", buyerCtx, &orderv1.ListOrdersRequest{RoleScope: "buyer", SalesChannel: "MARKETPLACE"}, 0},
		{"seller_storefront", sellerCtx, &orderv1.ListOrdersRequest{RoleScope: "seller", SalesChannel: "STOREFRONT"}, 2},
		{"admin_storefront", adminCtx, &orderv1.ListOrdersRequest{RoleScope: "admin", SalesChannel: "STOREFRONT"}, 2},
	} {
		list, err := h.svc.ListOrders(tc.ctx, tc.req)
		if err != nil || list.GetTotal() != tc.want {
			t.Fatalf("%s: want total=%d, got total=%d err=%v", tc.name, tc.want, list.GetTotal(), err)
		}
		for _, o := range list.GetOrders() {
			if o.GetSalesChannel() != SalesChannelStorefront || o.GetStorefrontShopId() != "shop-1" {
				t.Fatalf("%s: order channel=(%q,%q)", tc.name, o.GetSalesChannel(), o.GetStorefrontShopId())
			}
		}
	}
	if _, err := h.svc.ListOrders(adminCtx, &orderv1.ListOrdersRequest{RoleScope: "admin", SalesChannel: "BAD"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("ListOrders invalid sales_channel filter: want InvalidArgument, got %v", err)
	}
}

func TestSalesChannel_StorefrontRejectsInactiveShopAndCrossShopItemsZeroStockChange(t *testing.T) {
	h := newTestHarness()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")

	// 1. Inactive or missing storefront shops rejected BEFORE stock reservation.
	for _, shopID := range []string{"shop-missing", "shop-pending", "shop-suspended"} {
		_, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey:   "sf-inactive-" + shopID,
			AddressId:        "addr-buyer-1",
			Items:            []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:    orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
			SalesChannel:     SalesChannelStorefront,
			StorefrontShopId: shopID,
		})
		if status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "STOREFRONT_SHOP_INACTIVE") {
			t.Fatalf("shop %s: want InvalidArgument(STOREFRONT_SHOP_INACTIVE), got %v", shopID, err)
		}
	}

	// 2. Cross-shop item (sku-2 belongs to shop-2 on shop-1 storefront) rejected BEFORE stock reservation.
	_, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "sf-cross-1",
		AddressId:      "addr-buyer-1",
		Items: []*orderv1.CheckoutItemInput{
			{SkuId: "sku-1", Quantity: 1},
			{SkuId: "sku-2", Quantity: 1},
		},
		PaymentMethod:    orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		SalesChannel:     SalesChannelStorefront,
		StorefrontShopId: "shop-1",
	})
	if status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "STOREFRONT_SHOP_MISMATCH") {
		t.Fatalf("mixed-shop storefront: want InvalidArgument(STOREFRONT_SHOP_MISMATCH), got %v", err)
	}

	// Single item from another shop only.
	_, err = h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey:   "sf-cross-2",
		AddressId:        "addr-buyer-1",
		Items:            []*orderv1.CheckoutItemInput{{SkuId: "sku-2", Quantity: 1}},
		PaymentMethod:    orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		SalesChannel:     SalesChannelStorefront,
		StorefrontShopId: "shop-1",
	})
	if status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "STOREFRONT_SHOP_MISMATCH") {
		t.Fatalf("wrong-shop storefront: want InvalidArgument(STOREFRONT_SHOP_MISMATCH), got %v", err)
	}

	// CRITICAL INVARIANT: 0 stock reservations happened across all rejected attempts!
	if len(h.product.reserveCalls) != 0 {
		t.Fatalf("expected 0 ReserveStock calls on rejected STOREFRONT orders, got %d", len(h.product.reserveCalls))
	}
}

func TestSalesChannel_StorefrontRejectsPlatformPromotions(t *testing.T) {
	h := newTestHarness()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")

	cases := []struct {
		name string
		req  *orderv1.CreateCheckoutRequest
		want string
	}{
		{
			name: "coins",
			req: &orderv1.CreateCheckoutRequest{
				IdempotencyKey:   "sf-promo-coins",
				AddressId:        "addr-buyer-1",
				Items:            []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
				CoinsToUse:       5000,
				PaymentMethod:    orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
				SalesChannel:     SalesChannelStorefront,
				StorefrontShopId: "shop-1",
			},
			want: "STOREFRONT_COINS_NOT_ALLOWED",
		},
		{
			name: "platform_voucher",
			req: &orderv1.CreateCheckoutRequest{
				IdempotencyKey:    "sf-promo-plat",
				AddressId:         "addr-buyer-1",
				Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
				PlatformVoucherId: "voucher-plat-1",
				PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
				SalesChannel:      SalesChannelStorefront,
				StorefrontShopId:  "shop-1",
			},
			want: "STOREFRONT_PLATFORM_VOUCHER_NOT_ALLOWED",
		},
		{
			name: "freeship_voucher",
			req: &orderv1.CreateCheckoutRequest{
				IdempotencyKey:    "sf-promo-free",
				AddressId:         "addr-buyer-1",
				Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
				FreeshipVoucherId: "voucher-free-1",
				PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
				SalesChannel:      SalesChannelStorefront,
				StorefrontShopId:  "shop-1",
			},
			want: "STOREFRONT_FREESHIP_VOUCHER_NOT_ALLOWED",
		},
		{
			name: "other_shop_voucher",
			req: &orderv1.CreateCheckoutRequest{
				IdempotencyKey:   "sf-promo-other-shop",
				AddressId:        "addr-buyer-1",
				Items:            []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
				ShopVoucherIds:   map[string]string{"shop-2": "voucher-shop-2"},
				PaymentMethod:    orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
				SalesChannel:     SalesChannelStorefront,
				StorefrontShopId: "shop-1",
			},
			want: "STOREFRONT_VOUCHER_SHOP_MISMATCH",
		},
	}

	for _, tc := range cases {
		_, err := h.svc.CreateCheckout(buyerCtx, tc.req)
		if status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: want InvalidArgument(%s), got %v", tc.name, tc.want, err)
		}
	}
	if len(h.product.reserveCalls) != 0 {
		t.Fatalf("expected 0 stock reservations for rejected platform promos, got %d", len(h.product.reserveCalls))
	}

	// Adversarial case: buyer stuffs a PLATFORM voucher ID into shop_voucher_ids["shop-1"].
	// PromotionService rejects it and OrderService compensates stock via ReleaseStock.
	h.promo.reserveErr = status.Error(codes.InvalidArgument, "STOREFRONT_VOUCHER_NOT_ALLOWED: platform voucher cannot be used")
	_, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey:   "sf-promo-smuggled",
		AddressId:        "addr-buyer-1",
		Items:            []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		ShopVoucherIds:   map[string]string{"shop-1": "plat-voucher-uuid"},
		PaymentMethod:    orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		SalesChannel:     SalesChannelStorefront,
		StorefrontShopId: "shop-1",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("smuggled voucher: want InvalidArgument, got %v", err)
	}
	if len(h.product.reserveCalls) != 1 || len(h.product.releaseCalls) != 1 {
		t.Fatalf("expected stock compensation (1 reserve, 1 release), got reserve=%d release=%d", len(h.product.reserveCalls), len(h.product.releaseCalls))
	}
}

func TestSalesChannel_StorefrontCartScopingLeavesOtherShopItemsIntact(t *testing.T) {
	h := newTestHarness()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")

	// Cart initially contains BOTH shop-1 (sku-1) and shop-2 (sku-2).
	resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey:   "sf-cart-scope-1",
		AddressId:        "addr-buyer-1",
		FromCart:         true,
		PaymentMethod:    orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		SalesChannel:     SalesChannelStorefront,
		StorefrontShopId: "shop-1",
	})
	if err != nil {
		t.Fatalf("storefront from_cart checkout failed: %v", err)
	}
	if len(resp.GetOrders()) != 1 || resp.GetOrders()[0].GetShopId() != "shop-1" {
		t.Fatalf("expected 1 order for shop-1 only, got %+v", resp.GetOrders())
	}
	if len(h.cart.getCartCalls) != 1 || h.cart.getCartCalls[0].GetShopId() != "shop-1" {
		t.Fatalf("expected GetCart called with shop_id=shop-1, got %+v", h.cart.getCartCalls)
	}
	if len(h.cart.clearReqCalls) != 1 {
		t.Fatalf("expected 1 ClearItems call, got %d", len(h.cart.clearReqCalls))
	}
	clr := h.cart.clearReqCalls[0]
	if clr.GetShopId() != "shop-1" || len(clr.GetSkuIds()) != 1 || clr.GetSkuIds()[0] != "sku-1" {
		t.Fatalf("expected ClearItems scoped to shop-1 / [sku-1], got %+v", clr)
	}

	// Verify shop-2 item remains intact in the single user cart!
	if len(h.cart.cart.GetShopGroups()) != 1 || h.cart.cart.GetShopGroups()[0].GetShopId() != "shop-2" {
		t.Fatalf("expected shop-2 items to remain untouched in cart, got %+v", h.cart.cart.GetShopGroups())
	}
}

func TestSalesChannel_ImmutabilityAndLegacyBackwardCompat(t *testing.T) {
	h := newTestHarness()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
	adminCtx := ctxWithUser("admin-1", "admin", "")

	// 1. Create STOREFRONT order and walk it through full lifecycle -> channel/shop MUST NEVER mutate.
	created, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey:   "sf-imm-1",
		AddressId:        "addr-buyer-1",
		Items:            []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:    orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		SalesChannel:     SalesChannelStorefront,
		StorefrontShopId: "shop-1",
	})
	if err != nil {
		t.Fatalf("create storefront order: %v", err)
	}
	ordID := created.GetOrders()[0].GetId()

	conf, err := h.svc.SellerConfirmOrder(sellerCtx, &orderv1.SellerConfirmOrderRequest{OrderId: ordID})
	if err != nil || conf.GetOrder().GetSalesChannel() != SalesChannelStorefront || conf.GetOrder().GetStorefrontShopId() != "shop-1" {
		t.Fatalf("after SellerConfirmOrder channel mutated: resp=%+v err=%v", conf, err)
	}
	shipped, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID, CarrierCode: "ghn"})
	if err != nil || shipped.GetOrder().GetSalesChannel() != SalesChannelStorefront || shipped.GetOrder().GetStorefrontShopId() != "shop-1" {
		t.Fatalf("after SellerShipOrder channel mutated: resp=%+v err=%v", shipped, err)
	}
	if err := h.svc.HandleShipmentUpdated(context.Background(), "ev-deliv-imm", events.ShipmentUpdatedPayload{
		OrderID:      ordID,
		Status:       "delivered",
		TrackingCode: shipped.GetTrackingCode(),
	}); err != nil {
		t.Fatalf("HandleShipmentUpdated delivered: %v", err)
	}
	completed, err := h.svc.BuyerConfirmReceived(buyerCtx, &orderv1.BuyerConfirmReceivedRequest{OrderId: ordID})
	if err != nil || completed.GetOrder().GetSalesChannel() != SalesChannelStorefront || completed.GetOrder().GetStorefrontShopId() != "shop-1" {
		t.Fatalf("after BuyerConfirmReceived channel mutated: resp=%+v err=%v", completed, err)
	}

	// 2. Proto contract immutability: NO update/lifecycle RPC request message contains sales_channel or storefront_shop_id.
	updateMsgs := []proto.Message{
		&orderv1.CancelOrderRequest{},
		&orderv1.SellerConfirmOrderRequest{},
		&orderv1.SellerShipOrderRequest{},
		&orderv1.BuyerConfirmReceivedRequest{},
		&orderv1.RequestReturnRequest{},
		&orderv1.MarkPaymentResultRequest{},
	}
	for _, msg := range updateMsgs {
		desc := msg.ProtoReflect().Descriptor()
		if desc.Fields().ByName("sales_channel") != nil || desc.Fields().ByName("storefront_shop_id") != nil {
			t.Fatalf("immutability violation: %s must not accept sales_channel or storefront_shop_id", desc.FullName())
		}
	}

	// 3. Migration SQL defines DEFAULT 'MARKETPLACE', CHECK constraints, and BEFORE UPDATE immutability triggers.
	sqlBytes, err := migrations.Files.ReadFile("0002_sales_channel.sql")
	if err != nil {
		t.Fatalf("read 0002_sales_channel.sql: %v", err)
	}
	sqlText := string(sqlBytes)
	for _, snippet := range []string{
		"DEFAULT 'MARKETPLACE'",
		"CHECK (sales_channel IN ('MARKETPLACE', 'STOREFRONT'))",
		"trg_checkout_groups_sales_channel_immutable",
		"trg_orders_sales_channel_immutable",
		"BEFORE UPDATE ON orders",
		"BEFORE UPDATE ON checkout_groups",
		"NEW.sales_channel IS DISTINCT FROM OLD.sales_channel",
	} {
		if !strings.Contains(sqlText, snippet) {
			t.Fatalf("0002_sales_channel.sql missing required clause %q", snippet)
		}
	}

	// 4. Backward compatibility: legacy row with empty SalesChannel reads back as MARKETPLACE across all read/list paths.
	now := time.Now().UTC()
	h.store.SeedLegacyOrder(
		db.CheckoutGroup{
			ID:            "legacy-grp-1",
			BuyerID:       "buyer-1",
			Status:        "COMPLETED",
			PaymentMethod: "COD",
			SalesChannel:  "", // legacy pre-migration state
			CreatedAt:     now,
			UpdatedAt:     now,
		},
		db.Order{
			ID:               "legacy-ord-1",
			OrderCode:        "NM-LEGACY-001",
			CheckoutGroupID:  "legacy-grp-1",
			BuyerID:          "buyer-1",
			ShopID:           "shop-1",
			Status:           "CONFIRMED",
			PaymentMethod:    "COD",
			SalesChannel:     "", // legacy pre-migration state
			StorefrontShopID: "",
			CreatedAt:        now,
			UpdatedAt:        now,
		},
		nil,
	)

	legacyGet, err := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: "legacy-ord-1"})
	if err != nil {
		t.Fatalf("GetOrder legacy: %v", err)
	}
	if legacyGet.GetOrder().GetSalesChannel() != SalesChannelMarketplace || legacyGet.GetOrder().GetStorefrontShopId() != "" {
		t.Fatalf("legacy order must read as MARKETPLACE, got (%q, %q)", legacyGet.GetOrder().GetSalesChannel(), legacyGet.GetOrder().GetStorefrontShopId())
	}

	legacyList, err := h.svc.ListOrders(adminCtx, &orderv1.ListOrdersRequest{RoleScope: "admin", SalesChannel: "MARKETPLACE"})
	if err != nil || legacyList.GetTotal() != 1 || legacyList.GetOrders()[0].GetId() != "legacy-ord-1" || legacyList.GetOrders()[0].GetSalesChannel() != SalesChannelMarketplace {
		t.Fatalf("legacy order must appear under MARKETPLACE filter: total=%d orders=%+v err=%v", legacyList.GetTotal(), legacyList.GetOrders(), err)
	}
}

func TestListOrders_StorefrontShopIDFilterAndOutboxStorefront(t *testing.T) {
	h := newTestHarness()
	buyer1Ctx := ctxWithUser("buyer-1", "buyer", "")
	buyer2Ctx := ctxWithUser("buyer-2", "buyer", "")

	shopA := uuid.NewString()
	shopB := uuid.NewString()
	now := time.Now().UTC()

	// Seed 2 orders on shopA, 1 order on shopB, and 1 legacy MARKETPLACE order for buyer-1.
	seedOrder := func(id, buyerID, shopID, ch, sfShop string, offset time.Duration) {
		ts := now.Add(offset)
		h.store.SeedLegacyOrder(
			db.CheckoutGroup{ID: "grp-" + id, BuyerID: buyerID, Status: "COMPLETED", SalesChannel: ch, StorefrontShopID: sfShop, CreatedAt: ts, UpdatedAt: ts},
			db.Order{ID: id, OrderCode: "NM-" + id, CheckoutGroupID: "grp-" + id, BuyerID: buyerID, ShopID: shopID, Status: "CONFIRMED", PaymentMethod: "COD", SalesChannel: ch, StorefrontShopID: sfShop, CreatedAt: ts, UpdatedAt: ts},
			nil,
		)
	}
	seedOrder("ord-a1", "buyer-1", shopA, SalesChannelStorefront, shopA, 1*time.Second)
	seedOrder("ord-a2", "buyer-1", shopA, SalesChannelStorefront, shopA, 2*time.Second)
	seedOrder("ord-b1", "buyer-1", shopB, SalesChannelStorefront, shopB, 3*time.Second)
	seedOrder("ord-mp", "buyer-1", shopA, SalesChannelMarketplace, "", 4*time.Second)

	// Filtering by shopA returns only the 2 STOREFRONT orders on shopA with exact pagination.
	p1, err := h.svc.ListOrders(buyer1Ctx, &orderv1.ListOrdersRequest{RoleScope: "buyer", StorefrontShopId: shopA, Page: 1, PageSize: 1})
	if err != nil || p1.GetTotal() != 2 || len(p1.GetOrders()) != 1 || p1.GetOrders()[0].GetId() != "ord-a2" {
		t.Fatalf("shopA page 1: total=%d orders=%+v err=%v", p1.GetTotal(), p1.GetOrders(), err)
	}
	p2, err := h.svc.ListOrders(buyer1Ctx, &orderv1.ListOrdersRequest{RoleScope: "buyer", StorefrontShopId: shopA, Page: 2, PageSize: 1})
	if err != nil || p2.GetTotal() != 2 || len(p2.GetOrders()) != 1 || p2.GetOrders()[0].GetId() != "ord-a1" {
		t.Fatalf("shopA page 2: total=%d orders=%+v err=%v", p2.GetTotal(), p2.GetOrders(), err)
	}
	// Filtering by shopB returns only shopB order.
	listB, err := h.svc.ListOrders(buyer1Ctx, &orderv1.ListOrdersRequest{RoleScope: "buyer", StorefrontShopId: shopB, Page: 1, PageSize: 20})
	if err != nil || listB.GetTotal() != 1 || len(listB.GetOrders()) != 1 || listB.GetOrders()[0].GetId() != "ord-b1" {
		t.Fatalf("shopB list: total=%d orders=%+v err=%v", listB.GetTotal(), listB.GetOrders(), err)
	}
	// Cross-buyer isolation: buyer-2 sees 0 orders even when filtering by shopA.
	otherBuyer, err := h.svc.ListOrders(buyer2Ctx, &orderv1.ListOrdersRequest{RoleScope: "buyer", StorefrontShopId: shopA, Page: 1, PageSize: 20})
	if err != nil || otherBuyer.GetTotal() != 0 || len(otherBuyer.GetOrders()) != 0 {
		t.Fatalf("buyer-2 must not see buyer-1 orders: total=%d err=%v", otherBuyer.GetTotal(), err)
	}
	// Invalid UUID rejected cleanly.
	if _, err := h.svc.ListOrders(buyer1Ctx, &orderv1.ListOrdersRequest{RoleScope: "buyer", StorefrontShopId: "not-a-uuid"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid storefront_shop_id must return InvalidArgument, got %v", err)
	}

	// Outbox events emitted on STOREFRONT checkout must include storefront_shop_id for site-scoped notifications.
	resp, err := h.svc.CreateCheckout(buyer1Ctx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey:   "sf-outbox-check",
		AddressId:        "addr-buyer-1",
		Items:            []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:    orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		SalesChannel:     SalesChannelStorefront,
		StorefrontShopId: "shop-1",
	})
	if err != nil || len(resp.GetOrders()) != 1 {
		t.Fatalf("storefront checkout for outbox test: resp=%+v err=%v", resp, err)
	}
	var sawCreated, sawConfirmed bool
	for _, ev := range h.store.OutboxEvents() {
		switch ev.EventType {
		case events.SubjectOrderCreated:
			var p events.OrderCreatedPayload
			if err := json.Unmarshal(ev.PayloadJSON, &p); err != nil || p.StorefrontShopID != "shop-1" {
				t.Fatalf("order.created payload missing storefront_shop_id: %s err=%v", string(ev.PayloadJSON), err)
			}
			sawCreated = true
		case events.SubjectOrderConfirmed:
			var p events.OrderConfirmedPayload
			if err := json.Unmarshal(ev.PayloadJSON, &p); err != nil || p.StorefrontShopID != "shop-1" {
				t.Fatalf("order.confirmed payload missing storefront_shop_id: %s err=%v", string(ev.PayloadJSON), err)
			}
			sawConfirmed = true
		}
	}
	if !sawCreated || !sawConfirmed {
		t.Fatalf("expected order.created and order.confirmed outbox events, saw created=%v confirmed=%v", sawCreated, sawConfirmed)
	}
}

func TestOrderGRPCTrust_BufconnB1Enforcement(t *testing.T) {
	h := newTestHarness()
	users, _ := auth.NewManager("order-test-user-key", 15*time.Minute)
	svcMgr, _ := auth.NewManager("order-test-service-key", auth.ServiceTokenTTL)

	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer(grpc.ChainUnaryInterceptor(auth.GRPCUnaryServerInterceptor(users, svcMgr)))
	orderv1.RegisterOrderServiceServer(gs, &grpcAdapter{svc: h.svc})
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := orderv1.NewOrderServiceClient(conn)

	md := func(kv ...string) context.Context {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		t.Cleanup(cancel)
		return metadata.NewOutgoingContext(ctx, metadata.Pairs(kv...))
	}
	bearer := func(tok string) context.Context {
		return md("authorization", "Bearer "+tok)
	}

	buyerTok, _, _, _ := users.SignAccessToken("buyer-1", "buyer", "")
	svcTok, _ := svcMgr.SignServiceToken("payment-service")
	spoof := []string{"x-user-id", "buyer-1", "x-user-role", "admin"}

	// Anonymous/spoofed rejected with Unauthenticated.
	if _, err := client.CreateCheckout(md(spoof...), &orderv1.CreateCheckoutRequest{IdempotencyKey: "k1"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anon CreateCheckout: want Unauthenticated, got %v", err)
	}
	if _, err := client.ListOrders(md(spoof...), &orderv1.ListOrdersRequest{RoleScope: "admin"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anon ListOrders: want Unauthenticated, got %v", err)
	}
	if _, err := client.MarkPaymentResult(md(spoof...), &orderv1.MarkPaymentResultRequest{
		CheckoutGroupId: "grp-1", Status: orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_SUCCEEDED,
	}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anon MarkPaymentResult: want Unauthenticated, got %v", err)
	}

	// Service token rejected on user RPCs (PermissionDenied), allowed on MarkPaymentResult; buyer rejected on MarkPaymentResult.
	if _, err := client.ListOrders(bearer(svcTok), &orderv1.ListOrdersRequest{RoleScope: "admin"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service token ListOrders: want PermissionDenied, got %v", err)
	}
	if _, err := client.MarkPaymentResult(bearer(buyerTok), &orderv1.MarkPaymentResultRequest{
		CheckoutGroupId: "grp-1", Status: orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_SUCCEEDED,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("buyer MarkPaymentResult: want PermissionDenied, got %v", err)
	}
	if _, err := client.MarkPaymentResult(bearer(svcTok), &orderv1.MarkPaymentResultRequest{
		CheckoutGroupId: "grp-1", Status: orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_SUCCEEDED,
	}); err != nil {
		t.Fatalf("service token MarkPaymentResult: want OK, got %v", err)
	}
}

type grpcAdapter struct {
	orderv1.UnimplementedOrderServiceServer
	svc *OrderService
}

func (a *grpcAdapter) CreateCheckout(ctx context.Context, req *orderv1.CreateCheckoutRequest) (*orderv1.CreateCheckoutResponse, error) {
	return a.svc.CreateCheckout(ctx, req)
}
func (a *grpcAdapter) GetOrder(ctx context.Context, req *orderv1.GetOrderRequest) (*orderv1.GetOrderResponse, error) {
	return a.svc.GetOrder(ctx, req)
}
func (a *grpcAdapter) ListOrders(ctx context.Context, req *orderv1.ListOrdersRequest) (*orderv1.ListOrdersResponse, error) {
	return a.svc.ListOrders(ctx, req)
}
func (a *grpcAdapter) MarkPaymentResult(ctx context.Context, req *orderv1.MarkPaymentResultRequest) (*orderv1.MarkPaymentResultResponse, error) {
	return a.svc.MarkPaymentResult(ctx, req)
}
