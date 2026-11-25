package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	cartv1 "github.com/nexusmart/core/gen/go/nexusmart/cart/v1"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	"github.com/nexusmart/core/services/order/internal/db"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestCreateCheckout_ValidationAndEdgeBranches(t *testing.T) {
	h := newTestHarness()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	adminCtx := ctxWithUser("admin-1", "admin", "")

	// Nil request, buyer impersonation, missing address, unsupported payment method, negative coins.
	if _, err := h.svc.CreateCheckout(buyerCtx, nil); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("nil req: want InvalidArgument, got %v", err)
	}
	if _, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "k-imp", BuyerId: "buyer-2", AddressId: "addr-buyer-1",
		PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("impersonate buyer: want PermissionDenied, got %v", err)
	}
	if _, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "k-noaddr", PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("missing address_id: want InvalidArgument, got %v", err)
	}
	if _, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "k-badpm", AddressId: "addr-buyer-1",
		PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_UNSPECIFIED,
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid payment method: want InvalidArgument, got %v", err)
	}
	if _, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "k-negcoin", AddressId: "addr-buyer-1",
		PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD, CoinsToUse: -10,
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("negative coins: want InvalidArgument, got %v", err)
	}

	// Empty items & invalid item qty & duplicate SKU merge.
	if _, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "k-noitems", AddressId: "addr-buyer-1",
		PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("no items: want InvalidArgument, got %v", err)
	}
	if _, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "k-zeroqty", AddressId: "addr-buyer-1",
		Items:         []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 0}},
		PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("zero qty: want InvalidArgument, got %v", err)
	}

	// Inactive buyer account rejected.
	h.user.userValid = false
	if _, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "k-inactive-user", AddressId: "addr-buyer-1",
		Items:         []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("inactive buyer: want PermissionDenied, got %v", err)
	}
	h.user.userValid = true

	// Unknown address & missing SKU & bad SKU price & insufficient stock.
	if _, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "k-badaddr", AddressId: "addr-missing",
		Items:         []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	}); status.Code(err) != codes.NotFound {
		t.Fatalf("missing addr: want NotFound, got %v", err)
	}
	if _, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "k-badsku", AddressId: "addr-buyer-1",
		Items:         []*orderv1.CheckoutItemInput{{SkuId: "sku-missing", Quantity: 1}},
		PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	}); status.Code(err) != codes.NotFound {
		t.Fatalf("missing sku: want NotFound, got %v", err)
	}

	h.product.skus["sku-bad-price"] = &productv1.SKU{Id: "sku-bad-price", ShopId: "shop-1", IsActive: true, PriceVnd: 0, AvailableStock: 5}
	h.product.skus["sku-no-shop"] = &productv1.SKU{Id: "sku-no-shop", ShopId: "", IsActive: true, PriceVnd: 1000, AvailableStock: 5}
	for _, tc := range []struct {
		key, sku string
		qty      int32
	}{
		{"k-badprice", "sku-bad-price", 1},
		{"k-noshop", "sku-no-shop", 1},
		{"k-overstock", "sku-1", 999},
	} {
		if _, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: tc.key, AddressId: "addr-buyer-1",
			Items:         []*orderv1.CheckoutItemInput{{SkuId: tc.sku, Quantity: tc.qty}},
			PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		}); status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("%s: want FailedPrecondition, got %v", tc.key, err)
		}
	}

	// No shipping quotes available.
	origQuotes := h.shipping.quotes
	h.shipping.quotes = nil
	if _, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "k-noquotes", AddressId: "addr-buyer-1",
		Items:         []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("no shipping quotes: want FailedPrecondition, got %v", err)
	}
	h.shipping.quotes = origQuotes

	// Admin on behalf of buyer-1 with duplicate SKUs merged and zero total payable (100% discounted).
	h.promo.breakdown = &promotionv1.DiscountBreakdown{
		Shops: []*promotionv1.ShopDiscountBreakdown{
			{
				ShopId:                     "shop-1",
				SubtotalVnd:                2000000,
				PlatformVoucherDiscountVnd: 2000000,
				FreeshipDiscountVnd:        50000, // exceeds shipping fee -> clamped
			},
		},
	}
	zeroResp, err := h.svc.CreateCheckout(adminCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey:    "k-zero-payable",
		BuyerId:           "buyer-1",
		AddressId:         "addr-buyer-1",
		PlatformVoucherId: "v-100pct",
		FreeshipVoucherId: "v-free",
		Items: []*orderv1.CheckoutItemInput{
			{SkuId: "sku-1", Quantity: 1},
			{SkuId: "sku-1", Quantity: 1},
		},
		PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
	})
	if err != nil {
		t.Fatalf("zero payable checkout failed: %v", err)
	}
	if zeroResp.GetTotalPayableVnd() != 0 || zeroResp.GetPaymentStatus() != "SUCCEEDED" ||
		zeroResp.GetOrders()[0].GetStatus() != orderv1.OrderStatus_ORDER_STATUS_CONFIRMED {
		t.Fatalf("unexpected zero payable checkout resp: %+v", zeroResp)
	}

	// Empty cart error + cart with selected items filtering.
	h.cart.cart = &cartv1.Cart{UserId: "buyer-1"}
	if _, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "k-emptycart", AddressId: "addr-buyer-1", FromCart: true,
		PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("empty cart: want FailedPrecondition, got %v", err)
	}

	h.promo.breakdown = nil
	h.cart.cart = &cartv1.Cart{
		UserId: "buyer-1",
		ShopGroups: []*cartv1.CartShopGroup{
			{
				ShopId: "shop-1",
				Items: []*cartv1.CartItem{
					{SkuId: "sku-1", ShopId: "shop-1", Quantity: 1, Selected: true},
					{SkuId: "sku-2", ShopId: "shop-2", Quantity: 1, Selected: false},
				},
			},
		},
	}
	selResp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "k-selected-cart", AddressId: "addr-buyer-1", FromCart: true,
		PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	if err != nil || len(selResp.GetOrders()) != 1 || selResp.GetOrders()[0].GetShopId() != "shop-1" {
		t.Fatalf("selected cart checkout: resp=%+v err=%v", selResp, err)
	}

	// Test paymentStatusToString all enum branches and pickShippingQuote carrier fallback.
	for ps, want := range map[paymentv1.PaymentStatus]string{
		paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING:     "PENDING",
		paymentv1.PaymentStatus_PAYMENT_STATUS_SUCCEEDED:   "SUCCEEDED",
		paymentv1.PaymentStatus_PAYMENT_STATUS_FAILED:      "FAILED",
		paymentv1.PaymentStatus_PAYMENT_STATUS_EXPIRED:     "EXPIRED",
		paymentv1.PaymentStatus_PAYMENT_STATUS_CANCELLED:   "CANCELLED",
		paymentv1.PaymentStatus_PAYMENT_STATUS_REFUNDED:    "REFUNDED",
		paymentv1.PaymentStatus_PAYMENT_STATUS_UNSPECIFIED: "FB",
	} {
		if got := paymentStatusToString(ps, "FB"); got != want {
			t.Fatalf("paymentStatusToString(%v) = %q, want %q", ps, got, want)
		}
	}
	q := pickShippingQuote([]*shippingv1.CarrierQuote{
		{CarrierCode: "ghn", ServiceCode: "std", FeeVnd: 30000, CodSupported: true},
		{CarrierCode: "ghtk", ServiceCode: "road", FeeVnd: 25000, CodSupported: true},
	}, &orderv1.ShopShippingChoice{CarrierCode: "ghn", ServiceCode: "missing-svc"}, "COD")
	if q.GetCarrierCode() != "ghn" {
		t.Fatalf("expected carrier fallback ghn, got %+v", q)
	}

	// Replay response fallback when snapshot is empty.
	now := time.Now().UTC()
	h.store.SeedLegacyOrder(
		db.CheckoutGroup{ID: "grp-nosnap", BuyerID: "buyer-1", IdempotencyKey: "idem-nosnap", Status: "COMPLETED", PaymentMethod: "ZALOPAY", PaymentExpiresAt: &now},
		db.Order{ID: "ord-nosnap", OrderCode: "NM-NS", CheckoutGroupID: "grp-nosnap", BuyerID: "buyer-1", ShopID: "shop-1", Status: "PENDING", PaymentMethod: "ZALOPAY"},
		nil,
	)
	replayResp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "idem-nosnap", AddressId: "addr-buyer-1",
		Items:         []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY,
	})
	if err != nil || !replayResp.GetIdempotentReplay() || replayResp.GetCheckoutGroupId() != "grp-nosnap" {
		t.Fatalf("replay fallback without snapshot: resp=%+v err=%v", replayResp, err)
	}
}

type retryOnceRestockProductClient struct {
	*mockProductClient
	failedOnce bool
}

func (c *retryOnceRestockProductClient) RestockOrderItems(ctx context.Context, req *productv1.RestockOrderItemsRequest, opts ...grpc.CallOption) (*productv1.RestockOrderItemsResponse, error) {
	if !c.failedOnce {
		c.failedOnce = true
		return nil, status.Error(codes.Unavailable, "transient product error")
	}
	return c.mockProductClient.RestockOrderItems(ctx, req, opts...)
}

func TestLifecycleHandlersAndReturns_EdgeCasesAndWatchdog(t *testing.T) {
	h := newTestHarness()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerNoShopCtx := ctxWithUser("seller-0", "seller", "")
	adminCtx := ctxWithUser("admin-1", "admin", "")

	// OutboxTrigger channel accessor + ComputeOrderRewardCoins edge cases.
	if h.svc.OutboxTrigger() == nil {
		t.Fatalf("OutboxTrigger returned nil")
	}
	if ComputeOrderRewardCoins(db.Order{SubtotalVND: 0, TotalPayableVND: 0}, 100) != 0 {
		t.Fatalf("ComputeOrderRewardCoins zero order failed")
	}
	if got := ComputeOrderRewardCoins(db.Order{SubtotalVND: 100000, TotalPayableVND: 100000}, 100); got != 1000 {
		t.Fatalf("ComputeOrderRewardCoins(100000, 100bps) = %d, want 1000", got)
	}

	// Empty order_id validation across lifecycle RPCs.
	if _, err := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("GetOrder empty: %v", err)
	}
	if _, err := h.svc.GetOrderTimeline(buyerCtx, &orderv1.GetOrderTimelineRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("GetOrderTimeline empty: %v", err)
	}
	if _, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("CancelOrder empty: %v", err)
	}
	if _, err := h.svc.SellerConfirmOrder(adminCtx, &orderv1.SellerConfirmOrderRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("SellerConfirmOrder empty: %v", err)
	}
	if _, err := h.svc.SellerShipOrder(adminCtx, &orderv1.SellerShipOrderRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("SellerShipOrder empty: %v", err)
	}
	if _, err := h.svc.BuyerConfirmReceived(buyerCtx, &orderv1.BuyerConfirmReceivedRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("BuyerConfirmReceived empty: %v", err)
	}
	if _, err := h.svc.MarkPaymentResult(adminCtx, &orderv1.MarkPaymentResultRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("MarkPaymentResult empty status: %v", err)
	}
	if _, err := h.svc.MarkPaymentResult(adminCtx, &orderv1.MarkPaymentResultRequest{
		Status: orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_SUCCEEDED,
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("MarkPaymentResult empty ids: %v", err)
	}

	// ListOrders seller without shopID & buyer impersonation & admin default scope & pageSize clamp.
	if _, err := h.svc.ListOrders(sellerNoShopCtx, &orderv1.ListOrdersRequest{RoleScope: "seller"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("seller without shopID: %v", err)
	}
	if _, err := h.svc.ListOrders(buyerCtx, &orderv1.ListOrdersRequest{BuyerId: "buyer-2"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("buyer list other buyer: %v", err)
	}
	if _, err := h.svc.ListOrders(adminCtx, &orderv1.ListOrdersRequest{Status: orderv1.OrderStatus_ORDER_STATUS_CONFIRMED, Page: 0, PageSize: 500}); err != nil {
		t.Fatalf("admin ListOrders default scope: %v", err)
	}
	if _, err := h.svc.ListOrders(adminCtx, &orderv1.ListOrdersRequest{ShopId: "shop-1"}); err != nil {
		t.Fatalf("admin ListOrders shop filter: %v", err)
	}
	if _, err := h.svc.ListOrders(adminCtx, &orderv1.ListOrdersRequest{BuyerId: "buyer-1"}); err != nil {
		t.Fatalf("admin ListOrders buyer filter: %v", err)
	}

	// CancelOrder with active sibling sub-order + transient Unavailable retry in RestockOrderItems!
	retryProd := &retryOnceRestockProductClient{mockProductClient: h.product}
	h.svc.clients.Product = retryProd
	multiResp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "k-cancel-sibling",
		AddressId:      "addr-buyer-1",
		FromCart:       true,
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	if err != nil || len(multiResp.GetOrders()) != 2 {
		t.Fatalf("multi-shop COD checkout: %+v err=%v", multiResp, err)
	}
	if _, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{
		OrderId: multiResp.GetOrders()[0].GetId(),
	}); err != nil {
		t.Fatalf("CancelOrder with active sibling: %v", err)
	}
	if len(h.product.restockCalls) != 1 {
		t.Fatalf("expected restock to succeed after Aborted retry, got %d calls", len(h.product.restockCalls))
	}

	// Unified return RPC validation edge cases.
	if _, err := h.svc.ListReturnRequests(sellerNoShopCtx, &orderv1.ListReturnRequestsRequest{RoleScope: "seller"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("seller without shopID ListReturnRequests: %v", err)
	}
	if _, err := h.svc.RespondReturn(adminCtx, &orderv1.RespondReturnRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("RespondReturn empty: %v", err)
	}
	if _, err := h.svc.ConfirmReturnReceived(adminCtx, &orderv1.ConfirmReturnReceivedRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("ConfirmReturnReceived empty: %v", err)
	}

	// RunExpiryWatchdog ticks and shuts down cleanly on cancel.
	h.svc.cfg.ExpiryWatchdogPeriod = 15 * time.Millisecond
	h.svc.cfg.ExpiryWatchdogTimeout = 200 * time.Millisecond
	wCtx, wCancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.svc.RunExpiryWatchdog(wCtx)
	}()
	time.Sleep(45 * time.Millisecond)
	wCancel()
	<-done

	// mapStoreError fallback for unknown error.
	if err := mapStoreError(errors.New("boom")); status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("mapStoreError unknown: %v", err)
	}

	// Verify resolveShopPickupAddress NEVER falls back and returns SHOP_PICKUP_ADDRESS_REQUIRED when shop not found.
	if _, err := h.svc.resolveShopPickupAddress(context.Background(), "shop-unknown"); status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "SHOP_PICKUP_ADDRESS_REQUIRED") {
		t.Fatalf("resolveShopPickupAddress unknown shop: want FailedPrecondition(SHOP_PICKUP_ADDRESS_REQUIRED), got %v", err)
	}

	// HandlePaymentResult lookup via OrderID (instead of CheckoutGroupID).
	if err := h.svc.HandlePaymentResult(context.Background(), PaymentResultInput{
		OrderID: multiResp.GetOrders()[1].GetId(),
		Status:  orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_SUCCEEDED,
	}); err != nil {
		t.Fatalf("HandlePaymentResult by OrderID: %v", err)
	}
}
