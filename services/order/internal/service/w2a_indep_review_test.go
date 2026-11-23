package service

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	"github.com/nexusmart/core/services/order/internal/events"
	"github.com/nexusmart/core/services/order/internal/store"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// statefulProductClient simulates real ProductService reservation state (RESERVED -> CONFIRMED|RELEASED)
// and idempotent RestockOrderItems to prove P0-2 stock/reserved invariants across all orderings.
type statefulProductClient struct {
	mu             sync.Mutex
	skus           map[string]*productv1.SKU
	reservations   map[string][]simReservation // checkoutGroupID -> rows
	restockedOrder map[string]bool             // orderID -> idempotent dedup
	confirmErr     error
	releaseErr     error
	restockErr     error
}

type simReservation struct {
	skuID  string
	qty    int32
	status string // RESERVED, CONFIRMED, RELEASED
}

func newStatefulProductClient() *statefulProductClient {
	return &statefulProductClient{
		skus: map[string]*productv1.SKU{
			"sku-1": {Id: "sku-1", ProductId: "prod-1", ShopId: "shop-1", ProductName: "Item A", Name: "Default", PriceVnd: 100000, Stock: 10, AvailableStock: 10, WeightGrams: 500, CommissionRateBps: 500, IsActive: true},
			"sku-2": {Id: "sku-2", ProductId: "prod-2", ShopId: "shop-2", ProductName: "Item B", Name: "Default", PriceVnd: 100000, Stock: 10, AvailableStock: 10, WeightGrams: 500, CommissionRateBps: 500, IsActive: true},
		},
		reservations:   make(map[string][]simReservation),
		restockedOrder: make(map[string]bool),
	}
}

func (p *statefulProductClient) GetSKUsBatch(_ context.Context, req *productv1.GetSKUsBatchRequest, _ ...grpc.CallOption) (*productv1.GetSKUsBatchResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]*productv1.SKU, 0, len(req.GetSkuIds()))
	for _, id := range req.GetSkuIds() {
		if s, ok := p.skus[id]; ok {
			out = append(out, proto.Clone(s).(*productv1.SKU))
		}
	}
	return &productv1.GetSKUsBatchResponse{Skus: out}, nil
}

func (p *statefulProductClient) ReserveStock(_ context.Context, req *productv1.ReserveStockRequest, _ ...grpc.CallOption) (*productv1.ReserveStockResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.reservations[req.GetOrderId()]; exists {
		return &productv1.ReserveStockResponse{Reserved: true}, nil
	}
	rows := make([]simReservation, 0, len(req.GetItems()))
	for _, it := range req.GetItems() {
		sku := p.skus[it.GetSkuId()]
		sku.ReservedStock += it.GetQuantity()
		sku.AvailableStock = sku.Stock - sku.ReservedStock
		rows = append(rows, simReservation{skuID: it.GetSkuId(), qty: it.GetQuantity(), status: "RESERVED"})
	}
	p.reservations[req.GetOrderId()] = rows
	return &productv1.ReserveStockResponse{Reserved: true}, nil
}

func (p *statefulProductClient) ConfirmStock(_ context.Context, req *productv1.ConfirmStockRequest, _ ...grpc.CallOption) (*productv1.ConfirmStockResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.confirmErr != nil {
		return nil, p.confirmErr
	}
	rows := p.reservations[req.GetOrderId()]
	if len(rows) == 0 {
		return nil, status.Error(codes.NotFound, "reservation not found")
	}
	allConfirmed := true
	hasReleased := false
	for _, r := range rows {
		switch r.status {
		case "RESERVED":
			allConfirmed = false
		case "RELEASED":
			hasReleased = true
			allConfirmed = false
		}
	}
	if allConfirmed {
		return &productv1.ConfirmStockResponse{Confirmed: true, AlreadyConfirmed: true}, nil
	}
	if hasReleased {
		return nil, status.Error(codes.FailedPrecondition, "cannot confirm stock reservation that was already released")
	}
	for i := range rows {
		if rows[i].status == "RESERVED" {
			sku := p.skus[rows[i].skuID]
			sku.Stock -= rows[i].qty
			sku.ReservedStock -= rows[i].qty
			sku.AvailableStock = sku.Stock - sku.ReservedStock
			rows[i].status = "CONFIRMED"
		}
	}
	p.reservations[req.GetOrderId()] = rows
	return &productv1.ConfirmStockResponse{Confirmed: true}, nil
}

func (p *statefulProductClient) ReleaseStock(_ context.Context, req *productv1.ReleaseStockRequest, _ ...grpc.CallOption) (*productv1.ReleaseStockResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.releaseErr != nil {
		return nil, p.releaseErr
	}
	rows := p.reservations[req.GetOrderId()]
	if len(rows) == 0 {
		return &productv1.ReleaseStockResponse{Released: false, AlreadyReleased: false}, nil
	}
	allReleased := true
	hasReserved := false
	for _, r := range rows {
		if r.status == "RESERVED" {
			hasReserved = true
			allReleased = false
		} else if r.status != "RELEASED" {
			allReleased = false
		}
	}
	if !hasReserved {
		return &productv1.ReleaseStockResponse{Released: allReleased, AlreadyReleased: allReleased}, nil
	}
	for i := range rows {
		if rows[i].status == "RESERVED" {
			sku := p.skus[rows[i].skuID]
			sku.ReservedStock -= rows[i].qty
			sku.AvailableStock = sku.Stock - sku.ReservedStock
			rows[i].status = "RELEASED"
		}
	}
	p.reservations[req.GetOrderId()] = rows
	return &productv1.ReleaseStockResponse{Released: true}, nil
}

func (p *statefulProductClient) RestockOrderItems(_ context.Context, req *productv1.RestockOrderItemsRequest, _ ...grpc.CallOption) (*productv1.RestockOrderItemsResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.restockErr != nil {
		return nil, p.restockErr
	}
	if p.restockedOrder[req.GetOrderId()] {
		return &productv1.RestockOrderItemsResponse{Restocked: false, AlreadyRestocked: true}, nil
	}
	for _, it := range req.GetItems() {
		sku := p.skus[it.GetSkuId()]
		sku.Stock += it.GetQuantity()
		sku.AvailableStock = sku.Stock - sku.ReservedStock
	}
	p.restockedOrder[req.GetOrderId()] = true
	return &productv1.RestockOrderItemsResponse{Restocked: true}, nil
}

func (p *statefulProductClient) stockState(skuID string) (stock, reserved int32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.skus[skuID]
	return s.Stock, s.ReservedStock
}

// P0-1: Demoted seller (role=buyer with stale shop_id in JWT) MUST NOT view/list/cancel orders or view returns of old shop.
func TestIndepReview_P0_1_DemotedBuyerWithStaleShopIDCannotViewOrCancel(t *testing.T) {
	h := newTestHarness()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	demotedCtx := ctxWithUser("former-seller-1", "buyer", "shop-1")

	ord := createDeliveredOrderHelper(t, h, "p01-ret-ord", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD)
	retResp, err := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord.Id, Reason: "defect"})
	if err != nil {
		t.Fatalf("RequestReturn: %v", err)
	}

	// Fresh cancellable order in shop-1 owned by buyer-1.
	coResp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "p01-cancel-ord",
		BuyerId:        "buyer-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	if err != nil {
		t.Fatalf("CreateCheckout: %v", err)
	}
	freshOrdID := coResp.Orders[0].Id

	if _, err := h.svc.GetOrder(demotedCtx, &orderv1.GetOrderRequest{OrderId: freshOrdID}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("demoted buyer GetOrder = %v, want PermissionDenied", err)
	}
	if _, err := h.svc.GetOrderTimeline(demotedCtx, &orderv1.GetOrderTimelineRequest{OrderId: freshOrdID}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("demoted buyer GetOrderTimeline = %v, want PermissionDenied", err)
	}
	if _, err := h.svc.ListOrders(demotedCtx, &orderv1.ListOrdersRequest{RoleScope: "seller"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("demoted buyer ListOrders(seller) = %v, want PermissionDenied", err)
	}
	if _, err := h.svc.CancelOrder(demotedCtx, &orderv1.CancelOrderRequest{OrderId: freshOrdID, Reason: "malicious"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("demoted buyer CancelOrder = %v, want PermissionDenied", err)
	}
	if _, err := h.svc.GetReturnRequest(demotedCtx, &orderv1.GetReturnRequestRequest{ReturnRequestId: retResp.ReturnRequestId}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("demoted buyer GetReturnRequest = %v, want PermissionDenied", err)
	}
}

// P0-2: ConfirmStock must succeed before any RestockOrderItems; no phantom stock or leaked reserved_stock on COD/0-VND multi-shop checkout.
func TestIndepReview_P0_2_NoPhantomStockOrLeakedReservedAfterConfirmStockFailure(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	seller1Ctx := ctxWithUser("seller-1", "seller", "shop-1")

	t.Run("CancelO1WhileO2Active_ThenCancelO2_ExactStockZeroReserved", func(t *testing.T) {
		h := newTestHarness()
		prodSim := newStatefulProductClient()
		h.svc.clients.Product = prodSim

		// Checkout fails ConfirmStock transiently (COD 2-shop: sku-1 qty=2, sku-2 qty=1).
		prodSim.confirmErr = status.Error(codes.Unavailable, "product blip during checkout")
		coResp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "p02-case-a",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items: []*orderv1.CheckoutItemInput{
				{SkuId: "sku-1", Quantity: 2},
				{SkuId: "sku-2", Quantity: 1},
			},
			PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		var o1ID, o2ID string
		for _, o := range coResp.Orders {
			if o.ShopId == "shop-1" {
				o1ID = o.Id
			} else {
				o2ID = o.Id
			}
		}

		// Product recovers before O1 is cancelled.
		prodSim.confirmErr = nil
		if _, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: o1ID, Reason: "cancel O1"}); err != nil {
			t.Fatalf("CancelOrder(O1): %v", err)
		}
		s1, r1 := prodSim.stockState("sku-1")
		s2, r2 := prodSim.stockState("sku-2")
		if s1 != 10 || r1 != 0 || s2 != 9 || r2 != 0 {
			t.Fatalf("after cancelling O1 (O2 active): sku-1=(%d/%d) sku-2=(%d/%d), want (10/0) and (9/0)", s1, r1, s2, r2)
		}

		// Now cancel O2 -> sku-1 must STAY 10/0, sku-2 returns to 10/0 (no +2 phantom stock!).
		if _, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: o2ID, Reason: "cancel O2"}); err != nil {
			t.Fatalf("CancelOrder(O2): %v", err)
		}
		s1, r1 = prodSim.stockState("sku-1")
		s2, r2 = prodSim.stockState("sku-2")
		if s1 != 10 || r1 != 0 || s2 != 10 || r2 != 0 {
			t.Fatalf("after cancelling O1 then O2: sku-1=(%d/%d) sku-2=(%d/%d), want (10/0) and (10/0)", s1, r1, s2, r2)
		}
	})

	t.Run("ReturnO1WhileO2Active_ConfirmsGroupBeforeRestocking", func(t *testing.T) {
		h := newTestHarness()
		prodSim := newStatefulProductClient()
		h.svc.clients.Product = prodSim

		prodSim.confirmErr = status.Error(codes.Unavailable, "product blip during checkout")
		coResp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "p02-case-ret",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items: []*orderv1.CheckoutItemInput{
				{SkuId: "sku-1", Quantity: 2},
				{SkuId: "sku-2", Quantity: 1},
			},
			PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		var o1ID string
		for _, o := range coResp.Orders {
			if o.ShopId == "shop-1" {
				o1ID = o.Id
			}
		}

		shipResp, _ := h.svc.SellerShipOrder(seller1Ctx, &orderv1.SellerShipOrderRequest{OrderId: o1ID, ShopId: "shop-1"})
		prodSim.confirmErr = nil
		if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-p02-ret", events.ShipmentUpdatedPayload{
			OrderID: o1ID, Status: "returned", TrackingCode: shipResp.TrackingCode, Description: "refused",
		}); err != nil {
			t.Fatalf("HandleShipmentUpdated(returned): %v", err)
		}
		s1, r1 := prodSim.stockState("sku-1")
		s2, r2 := prodSim.stockState("sku-2")
		if s1 != 10 || r1 != 0 || s2 != 9 || r2 != 0 {
			t.Fatalf("after O1 returned (O2 active): sku-1=(%d/%d) sku-2=(%d/%d), want (10/0) and (9/0)", s1, r1, s2, r2)
		}
	})

	t.Run("CancelO1WhileProductDown_ThenCancelO2ReleasesAll_WatchdogO1NoDoubleRestock", func(t *testing.T) {
		h := newTestHarness()
		prodSim := newStatefulProductClient()
		h.svc.clients.Product = prodSim

		prodSim.confirmErr = status.Error(codes.Unavailable, "product down at checkout")
		coResp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "p02-case-b-outage",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items: []*orderv1.CheckoutItemInput{
				{SkuId: "sku-1", Quantity: 2},
				{SkuId: "sku-2", Quantity: 1},
			},
			PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		var o1ID, o2ID string
		for _, o := range coResp.Orders {
			if o.ShopId == "shop-1" {
				o1ID = o.Id
			} else {
				o2ID = o.Id
			}
		}

		// Product STILL down when buyer cancels O1 -> pending_cancel_compensation stays true, NO restock!
		prodSim.releaseErr = status.Error(codes.Unavailable, "product still down")
		prodSim.restockErr = status.Error(codes.Unavailable, "product still down")
		if _, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: o1ID, Reason: "cancel O1 during outage"}); err != nil {
			t.Fatalf("CancelOrder(O1): %v", err)
		}
		agg1, _ := h.store.GetOrderWithDetails(context.Background(), o1ID)
		if !agg1.Order.PendingCancelCompensation {
			t.Fatalf("expected O1.pending_cancel_compensation=true after failed Product compensation")
		}
		if s1, r1 := prodSim.stockState("sku-1"); s1 != 10 || r1 != 2 {
			t.Fatalf("during outage sku-1=(%d/%d), want (10/2)", s1, r1)
		}

		// Product recovers BEFORE watchdog runs; buyer cancels O2 (last active sub-order) -> ReleaseStock frees BOTH!
		prodSim.confirmErr = nil
		prodSim.releaseErr = nil
		prodSim.restockErr = nil
		if _, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: o2ID, Reason: "cancel O2"}); err != nil {
			t.Fatalf("CancelOrder(O2): %v", err)
		}
		if s1, r1 := prodSim.stockState("sku-1"); s1 != 10 || r1 != 0 {
			t.Fatalf("after O2 cancel sku-1=(%d/%d), want (10/0)", s1, r1)
		}
		if s2, r2 := prodSim.stockState("sku-2"); s2 != 10 || r2 != 0 {
			t.Fatalf("after O2 cancel sku-2=(%d/%d), want (10/0)", s2, r2)
		}

		// Watchdog retries O1 compensation -> sees AlreadyReleased, MUST NOT double-restock sku-1!
		if n, err := h.svc.ScanPendingCancelCompensations(context.Background()); err != nil || n != 1 {
			t.Fatalf("ScanPendingCancelCompensations = %d, %v; want 1, nil", n, err)
		}
		agg1, _ = h.store.GetOrderWithDetails(context.Background(), o1ID)
		if agg1.Order.PendingCancelCompensation {
			t.Fatalf("O1.pending_cancel_compensation must be cleared after watchdog retry")
		}
		if s1, r1 := prodSim.stockState("sku-1"); s1 != 10 || r1 != 0 {
			t.Fatalf("after O1 watchdog sku-1=(%d/%d), want (10/0) (no double restock!)", s1, r1)
		}

		// PendingStockConfirm was already cleared when ReleaseStock freed the group reservation on O2 cancel.
		grp, _ := h.store.GetCheckoutGroupByID(context.Background(), coResp.CheckoutGroupId)
		if grp.PendingStockConfirm {
			t.Fatalf("checkout_group.pending_stock_confirm must be cleared after ReleaseStock freed group")
		}
		if n, err := h.svc.ScanPendingStockConfirms(context.Background()); err != nil || n != 0 {
			t.Fatalf("ScanPendingStockConfirms = %d, %v; want 0, nil", n, err)
		}
	})

	t.Run("ScanPendingStockConfirms_RetriesCODConfirmStockUntilCleared", func(t *testing.T) {
		h := newTestHarness()
		prodSim := newStatefulProductClient()
		h.svc.clients.Product = prodSim

		prodSim.confirmErr = status.Error(codes.Unavailable, "product blip")
		coResp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "p02-watchdog-confirm",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 2}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		grp, _ := h.store.GetCheckoutGroupByID(context.Background(), coResp.CheckoutGroupId)
		if !grp.PendingStockConfirm {
			t.Fatalf("expected pending_stock_confirm=true after COD ConfirmStock failure")
		}
		if s1, r1 := prodSim.stockState("sku-1"); s1 != 10 || r1 != 2 {
			t.Fatalf("before retry sku-1=(%d/%d), want (10/2)", s1, r1)
		}

		prodSim.confirmErr = nil
		if n, err := h.svc.ScanPendingStockConfirms(context.Background()); err != nil || n != 1 {
			t.Fatalf("ScanPendingStockConfirms = %d, %v; want 1, nil", n, err)
		}
		if s1, r1 := prodSim.stockState("sku-1"); s1 != 8 || r1 != 0 {
			t.Fatalf("after ConfirmStock retry sku-1=(%d/%d), want (8/0)", s1, r1)
		}
		grp, _ = h.store.GetCheckoutGroupByID(context.Background(), coResp.CheckoutGroupId)
		if grp.PendingStockConfirm {
			t.Fatalf("pending_stock_confirm must be cleared after ConfirmStock succeeds")
		}
	})

	t.Run("P1_4_ScanPendingCancelCompensations_RetriesFailedPromotionRelease", func(t *testing.T) {
		h := newTestHarness()
		coResp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "p14-promo-retry",
			BuyerId:           "buyer-1",
			AddressId:         "addr-buyer-1",
			Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PlatformVoucherId: "v-plat",
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		ordID := coResp.Orders[0].Id

		h.promo.releaseErr = status.Error(codes.Unavailable, "promotion service down")
		if _, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: ordID, Reason: "cancel"}); err != nil {
			t.Fatalf("CancelOrder: %v", err)
		}
		agg, _ := h.store.GetOrderWithDetails(context.Background(), ordID)
		if !agg.Order.PendingCancelCompensation {
			t.Fatalf("expected pending_cancel_compensation=true after failed ReleasePromotion")
		}

		// Recovery -> watchdog retries and clears flag.
		h.promo.releaseErr = nil
		if n, err := h.svc.ScanPendingCancelCompensations(context.Background()); err != nil || n != 1 {
			t.Fatalf("ScanPendingCancelCompensations = %d, %v; want 1, nil", n, err)
		}
		agg, _ = h.store.GetOrderWithDetails(context.Background(), ordID)
		if agg.Order.PendingCancelCompensation {
			t.Fatalf("pending_cancel_compensation must be cleared after successful retry")
		}
		if len(h.promo.releaseCalls) == 0 || h.promo.releaseCalls[len(h.promo.releaseCalls)-1] != coResp.CheckoutGroupId {
			t.Fatalf("expected ReleasePromotion(%s), got %v", coResp.CheckoutGroupId, h.promo.releaseCalls)
		}
	})
}

// P0-3: CancelOrder MUST block under FOR UPDATE when pending_shipment_cancel=true; FailedPrecondition orphan records manual review trace.
func TestIndepReview_P0_3_CancelBlocksOnPendingShipmentCancelAndOrphanRecordsManualReview(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

	t.Run("CancelSeesPendingShipmentCancelUnderLock_CallsCancelShipmentAndRejects409", func(t *testing.T) {
		h := newTestHarness()
		wrapped := &interceptStore{Store: h.store}
		h.svc.store = wrapped

		coResp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "p03-race-lock",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		ordID := coResp.Orders[0].Id

		// Simulate SellerShipOrder calling MarkOrderShipmentCreating (setting pending_shipment_cancel=true)
		// and carrier picking up right AFTER CancelOrder read agg (with tracking="" & pending_shipment_cancel=false)
		// and BEFORE CancelOrder acquires FOR UPDATE in TransitionOrderStatus!
		injected := false
		wrapped.onTransitionOrderStatus = func(ctx context.Context, params store.SingleOrderTransitionParams) (*store.OrderTransitionResult, error) {
			if !injected && params.TargetStatus == orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
				injected = true
				_, _ = h.store.MarkOrderShipmentCreating(ctx, ordID, time.Now().UTC())
				h.shipping.cancelShipErr = status.Error(codes.FailedPrecondition, "carrier already picked up")
			}
			return h.store.TransitionOrderStatus(ctx, params)
		}

		_, err = h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: ordID, Reason: "cancel race"})
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("CancelOrder during pending shipment creation + pickup = %v, want FailedPrecondition", err)
		}
		got, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ordID})
		if got.Order.Status == orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
			t.Fatalf("order MUST NOT be cancelled when shipment was already picked up")
		}
	})

	t.Run("OrphanShipmentFailedPrecondition_RecordsDurableManualReviewAndDoesNotSpin", func(t *testing.T) {
		h := newTestHarness()
		coResp, _ := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "p03-orphan-fp",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		ordID := coResp.Orders[0].Id

		h.shipping.onCreateShipment = func(req *shippingv1.CreateShipmentRequest) (*shippingv1.CreateShipmentResponse, error) {
			// Concurrent buyer cancel commits CANCELLED while CreateShipment is in flight (before pickup),
			// then carrier picks up before compensateOrphanForwardShipment calls CancelShipment!
			if _, cErr := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: ordID, Reason: "cancel"}); cErr != nil {
				t.Errorf("CancelOrder: %v", cErr)
			}
			h.shipping.cancelShipErr = status.Error(codes.FailedPrecondition, "already picked up")
			return &shippingv1.CreateShipmentResponse{
				Shipment: &shippingv1.Shipment{
					ShipmentId:   "shp-p03",
					OrderId:      req.GetOrderId(),
					TrackingCode: "TRK-P03-PICKED",
				},
			}, nil
		}

		_, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID, ShopId: "shop-1"})
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("SellerShipOrder = %v, want FailedPrecondition", err)
		}

		// Timeline MUST contain a durable MANUAL_REVIEW_REQUIRED record for admin visibility!
		got, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ordID})
		foundReview := false
		for _, tl := range got.Order.Timeline {
			if strings.Contains(tl.Note, "MANUAL_REVIEW_REQUIRED") {
				foundReview = true
				break
			}
		}
		if !foundReview {
			t.Fatalf("expected durable MANUAL_REVIEW_REQUIRED timeline entry on orphan FailedPrecondition, got %+v", got.Order.Timeline)
		}
		// Watchdog must not spin infinitely on the same order.
		if n, _ := h.svc.ScanPendingOrphanShipmentCancels(context.Background()); n != 0 {
			t.Fatalf("watchdog should not spin after recording manual review, handled=%d", n)
		}
	})
}

// P1-1: Early RETURN event before SaveReturnShipmentInfo returns transient error (no processed_events); old tracking is ignored.
func TestIndepReview_P1_1_EarlyReturnEventRedeliversWhileOldTrackingIgnored(t *testing.T) {
	h := newTestHarness()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

	ord := createDeliveredOrderHelper(t, h, "p11-early", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR)
	retResp, _ := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord.Id, Reason: "defect"})
	retID := retResp.ReturnRequestId

	// Fail CreateReturnShipment initially so return is AWAITING_SHIPMENT with empty return_tracking_code.
	h.shipping.createReturnShipErr = status.Error(codes.Unavailable, "shipping slow")
	_, err := h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
		ReturnRequestId: retID,
		Decision:        orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_RETURN,
	})
	if err != nil {
		t.Fatalf("RespondReturn: %v", err)
	}

	// Webhook arrives early BEFORE SaveReturnShipmentInfo commits tracking "RET-EARLY-1" -> MUST return error & NOT mark processed!
	earlyEventID := "evt-p11-early-deliv"
	err = h.svc.HandleShipmentUpdated(context.Background(), earlyEventID, events.ShipmentUpdatedPayload{
		OrderID: ord.Id, Direction: "RETURN", ReturnRequestID: retID, TrackingCode: "RET-EARLY-1", Status: "delivered",
	})
	if err == nil {
		t.Fatalf("expected transient error for early RETURN event before SaveReturnShipmentInfo, got nil")
	}
	if processed, _ := h.store.IsEventProcessed(context.Background(), earlyEventID); processed {
		t.Fatalf("early RETURN event must NOT be recorded in processed_events")
	}

	// Now watchdog saves tracking "RET-EARLY-1".
	h.shipping.createReturnShipErr = nil
	h.shipping.onCreateReturnShipment = func(req *shippingv1.CreateReturnShipmentRequest) (*shippingv1.CreateReturnShipmentResponse, error) {
		return &shippingv1.CreateReturnShipmentResponse{
			Shipment: &shippingv1.Shipment{
				ShipmentId: "rshp-early", OrderId: req.GetOrderId(), TrackingCode: "RET-EARLY-1",
				LabelUrl: "https://labels.sandbox/early", FeeVnd: 25000, Direction: shippingv1.ShipmentDirection_SHIPMENT_DIRECTION_RETURN,
			},
		}, nil
	}
	if n, sErr := h.svc.ScanReturnPendingShipments(context.Background()); sErr != nil || n != 1 {
		t.Fatalf("ScanReturnPendingShipments = %d, %v; want 1, nil", n, sErr)
	}

	// Redelivery of the exact same earlyEventID now succeeds and transitions to RECEIVED!
	if err := h.svc.HandleShipmentUpdated(context.Background(), earlyEventID, events.ShipmentUpdatedPayload{
		OrderID: ord.Id, Direction: "RETURN", ReturnRequestID: retID, TrackingCode: "RET-EARLY-1", Status: "delivered",
	}); err != nil {
		t.Fatalf("redelivered RETURN event failed: %v", err)
	}
	got, _ := h.svc.GetReturnRequest(buyerCtx, &orderv1.GetReturnRequestRequest{ReturnRequestId: retID})
	if got.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_RECEIVED {
		t.Fatalf("status after redelivery = %v, want RECEIVED", got.ReturnRequest.Status)
	}
}

// P1-2: AWAITING_SHIPMENT expiry syncs via GetShipment(RETURN) first and caps grace extensions at 3 before escalating.
func TestIndepReview_P1_2_SyncGetShipmentAndCapDeadlineExtensions(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

	t.Run("SyncsDeliveredFromGetShipmentInsteadOfExtending", func(t *testing.T) {
		h := newTestHarness()
		now := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
		h.svc.nowFn = func() time.Time { return now }

		ord := createDeliveredOrderHelper(t, h, "p12-sync", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR)
		retResp, _ := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord.Id, Reason: "defect"})
		retID := retResp.ReturnRequestId
		acc, _ := h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: retID, Decision: orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_RETURN,
		})

		h.shipping.cancelShipErr = status.Error(codes.FailedPrecondition, "already delivered")
		h.shipping.getShipResp = &shippingv1.GetShipmentResponse{
			Shipment: &shippingv1.Shipment{
				OrderId: ord.Id, TrackingCode: acc.ReturnRequest.ReturnTrackingCode,
				Status: shippingv1.ShipmentStatus_SHIPMENT_STATUS_DELIVERED, Direction: shippingv1.ShipmentDirection_SHIPMENT_DIRECTION_RETURN,
			},
		}

		now = now.Add(5*24*time.Hour + time.Minute)
		if _, err := h.svc.ScanReturnDeadlines(context.Background()); err != nil {
			t.Fatalf("ScanReturnDeadlines: %v", err)
		}
		got, _ := h.svc.GetReturnRequest(buyerCtx, &orderv1.GetReturnRequestRequest{ReturnRequestId: retID})
		if got.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_RECEIVED {
			t.Fatalf("status after GetShipment sync = %v, want RECEIVED", got.ReturnRequest.Status)
		}
	})

	t.Run("CapsExtensionsAt3ThenEscalates", func(t *testing.T) {
		h := newTestHarness()
		now := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
		h.svc.nowFn = func() time.Time { return now }

		ord := createDeliveredOrderHelper(t, h, "p12-cap", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR)
		retResp, _ := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord.Id, Reason: "defect"})
		retID := retResp.ReturnRequestId
		_, _ = h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: retID, Decision: orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_RETURN,
		})

		h.shipping.cancelShipErr = status.Error(codes.FailedPrecondition, "cannot cancel")
		h.shipping.getShipErr = status.Error(codes.Unavailable, "get shipment temporarily unavailable")

		// 3 grace extensions keep AWAITING_SHIPMENT.
		now = now.Add(5*24*time.Hour + time.Minute)
		for ext := 1; ext <= 3; ext++ {
			if _, err := h.svc.ScanReturnDeadlines(context.Background()); err != nil {
				t.Fatalf("extension %d failed: %v", ext, err)
			}
			got, _ := h.svc.GetReturnRequest(buyerCtx, &orderv1.GetReturnRequestRequest{ReturnRequestId: retID})
			if got.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT {
				t.Fatalf("after extension %d status = %v, want AWAITING_SHIPMENT", ext, got.ReturnRequest.Status)
			}
			now = now.Add(24*time.Hour + time.Minute)
		}

		// 4th expiry exceeds max 3 extensions -> MUST escalate to ESCALATED!
		if _, err := h.svc.ScanReturnDeadlines(context.Background()); err != nil {
			t.Fatalf("4th expiry failed: %v", err)
		}
		got, _ := h.svc.GetReturnRequest(buyerCtx, &orderv1.GetReturnRequestRequest{ReturnRequestId: retID})
		if got.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_ESCALATED {
			t.Fatalf("after 3 extensions + expiry status = %v, want ESCALATED", got.ReturnRequest.Status)
		}
	})
}

// P1-3: AdminResolveReturn rejects RETURN_AND_REFUND on delivering/failed(lost) shipments with Vietnamese FailedPrecondition,
// and allows REJECT when escalated via 14d IN_TRANSIT timeout.
func TestIndepReview_P1_3_AdminResolveReturnDeliveringFailedAndTransitTimeout(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
	adminCtx := ctxWithUser("admin-1", "admin", "")

	t.Run("FailedLostShipment_RejectsReturnAndRefundWithVietnameseMessage", func(t *testing.T) {
		h := newTestHarness()
		ord := createDeliveredOrderHelper(t, h, "p13-lost", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR)
		retResp, _ := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord.Id, Reason: "defect"})
		retID := retResp.ReturnRequestId
		acc, _ := h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: retID, Decision: orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_RETURN,
		})
		_ = h.svc.HandleShipmentUpdated(context.Background(), "evt-p13-lost", events.ShipmentUpdatedPayload{
			OrderID: ord.Id, Direction: "RETURN", ReturnRequestID: retID,
			TrackingCode: acc.ReturnRequest.ReturnTrackingCode, Status: "failed", RawStatus: "lost", Description: "Parcel lost",
		})
		h.shipping.getShipResp = &shippingv1.GetShipmentResponse{
			Shipment: &shippingv1.Shipment{
				OrderId: ord.Id, TrackingCode: acc.ReturnRequest.ReturnTrackingCode,
				Status: shippingv1.ShipmentStatus_SHIPMENT_STATUS_FAILED, Direction: shippingv1.ShipmentDirection_SHIPMENT_DIRECTION_RETURN,
			},
		}

		_, err := h.svc.AdminResolveReturn(adminCtx, &orderv1.AdminResolveReturnRequest{
			ReturnRequestId: retID, Decision: orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_RETURN_AND_REFUND,
		})
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("AdminResolveReturn(RETURN_AND_REFUND) on lost shipment = %v, want FailedPrecondition", err)
		}
		if !strings.Contains(err.Error(), "REFUND") || !strings.Contains(err.Error(), "REJECT") {
			t.Fatalf("expected Vietnamese guidance mentioning REFUND/REJECT, got %q", err.Error())
		}
	})

	t.Run("InTransitTimeout_RejectsReturnAndRefund_AllowsReject", func(t *testing.T) {
		h := newTestHarness()
		now := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
		h.svc.nowFn = func() time.Time { return now }

		ord := createDeliveredOrderHelper(t, h, "p13-transit", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR)
		retResp, _ := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord.Id, Reason: "defect"})
		retID := retResp.ReturnRequestId
		acc, _ := h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: retID, Decision: orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_RETURN,
		})
		_ = h.svc.HandleShipmentUpdated(context.Background(), "evt-p13-go", events.ShipmentUpdatedPayload{
			OrderID: ord.Id, Direction: "RETURN", ReturnRequestID: retID,
			TrackingCode: acc.ReturnRequest.ReturnTrackingCode, Status: "delivering",
		})
		now = now.Add(14*24*time.Hour + time.Minute)
		_, _ = h.svc.ScanReturnDeadlines(context.Background())

		h.shipping.getShipResp = &shippingv1.GetShipmentResponse{
			Shipment: &shippingv1.Shipment{
				OrderId: ord.Id, TrackingCode: acc.ReturnRequest.ReturnTrackingCode,
				Status: shippingv1.ShipmentStatus_SHIPMENT_STATUS_DELIVERING, Direction: shippingv1.ShipmentDirection_SHIPMENT_DIRECTION_RETURN,
			},
		}
		if _, err := h.svc.AdminResolveReturn(adminCtx, &orderv1.AdminResolveReturnRequest{
			ReturnRequestId: retID, Decision: orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_RETURN_AND_REFUND,
		}); status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("AdminResolveReturn(RETURN_AND_REFUND) on delivering/timeout = %v, want FailedPrecondition", err)
		}

		// REJECT must SUCCEED even though CancelShipment would fail with FailedPrecondition!
		h.shipping.cancelShipErr = status.Error(codes.FailedPrecondition, "already picked up")
		res, err := h.svc.AdminResolveReturn(adminCtx, &orderv1.AdminResolveReturnRequest{
			ReturnRequestId: retID, Decision: orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_REJECT, Note: "Admin closes after transit timeout",
		})
		if err != nil {
			t.Fatalf("AdminResolveReturn(REJECT) after IN_TRANSIT timeout failed: %v", err)
		}
		if res.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_CLOSED {
			t.Fatalf("status = %v, want CLOSED", res.ReturnRequest.Status)
		}
	})
}

// P1-5: FORWARD shipment.updated ignores terminal/post-delivery states and mismatched/old tracking codes.
func TestIndepReview_P1_5_ForwardShipmentUpdateIgnoresTerminalAndStaleTracking(t *testing.T) {
	h := newTestHarness()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

	// 1. Cancelled order must ignore late FORWARD events (no tracking overwrite, no fake timeline rows).
	co1, _ := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "p15-cancelled",
		BuyerId:        "buyer-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	ord1ID := co1.Orders[0].Id
	_, _ = h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: ord1ID, Reason: "cancel"})
	before1, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ord1ID})

	_ = h.svc.HandleShipmentUpdated(context.Background(), "evt-p15-can-1", events.ShipmentUpdatedPayload{
		OrderID: ord1ID, Status: "delivering", TrackingCode: "TRK-FAKE", Description: "late webhook",
	})
	_ = h.svc.HandleShipmentUpdated(context.Background(), "evt-p15-can-2", events.ShipmentUpdatedPayload{
		OrderID: ord1ID, Status: "picking", TrackingCode: "TRK-FAKE", Description: "late picking",
	})
	after1, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ord1ID})
	if after1.Order.TrackingCode != "" || len(after1.Order.Timeline) != len(before1.Order.Timeline) {
		t.Fatalf("cancelled order was mutated by late FORWARD webhook: tracking=%q timeline=%d->%d",
			after1.Order.TrackingCode, len(before1.Order.Timeline), len(after1.Order.Timeline))
	}

	// 2. Replaced tracking code: stale webhook with OLD tracking MUST NOT transition order with NEW tracking.
	co2, _ := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "p15-stale-trk",
		BuyerId:        "buyer-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	ord2ID := co2.Orders[0].Id
	ship2, _ := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord2ID, ShopId: "shop-1"})

	_ = h.svc.HandleShipmentUpdated(context.Background(), "evt-p15-old-deliv", events.ShipmentUpdatedPayload{
		OrderID: ord2ID, Status: "delivered", TrackingCode: "TRK-OLD-SUPERSEDED", Description: "old shipment",
	})
	got2, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ord2ID})
	if got2.Order.Status != orderv1.OrderStatus_ORDER_STATUS_PREPARING || got2.Order.TrackingCode != ship2.TrackingCode {
		t.Fatalf("stale FORWARD tracking mutated order: status=%v tracking=%q", got2.Order.Status, got2.Order.TrackingCode)
	}
}

// P2-2, P2-3, P2-4, P2-5, P2-6, P3-1, P3-2, P3-3 combined regression tests.
func TestIndepReview_P2_And_P3_Fixes(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
	adminCtx := ctxWithUser("admin-1", "admin", "")

	t.Run("P2_2_FallbackCarrierAllowsRealCarrierOnSellerShipOrder", func(t *testing.T) {
		h := newTestHarness()
		h.shipping.quotes = []*shippingv1.CarrierQuote{
			{CarrierCode: "fallback", CarrierName: "Fallback", ServiceCode: "standard", FeeVnd: 30000, IsFallback: true},
		}
		co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "p22-fb",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		shipResp, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{
			OrderId: co.Orders[0].Id, ShopId: "shop-1", CarrierCode: "ghn", ServiceCode: "express",
		})
		if err != nil {
			t.Fatalf("SellerShipOrder with real carrier on fallback order failed: %v", err)
		}
		if shipResp.Order.CarrierCode != "ghn" {
			t.Fatalf("carrier_code = %q, want ghn", shipResp.Order.CarrierCode)
		}
	})

	t.Run("P2_3_SaveReturnShipmentInfoEmitsOrderReturnUpdatedWithTracking", func(t *testing.T) {
		h := newTestHarness()
		ord := createDeliveredOrderHelper(t, h, "p23-outbox", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR)
		retResp, _ := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord.Id, Reason: "defect"})
		acc, err := h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: retResp.ReturnRequestId, Decision: orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_RETURN,
		})
		if err != nil {
			t.Fatalf("RespondReturn: %v", err)
		}
		foundWithTracking := false
		for _, ev := range h.store.OutboxEvents() {
			if ev.EventType == events.SubjectOrderReturnUpdated {
				var p events.OrderReturnUpdatedPayload
				_ = json.Unmarshal(ev.PayloadJSON, &p)
				if p.ReturnTrackingCode == acc.ReturnRequest.ReturnTrackingCode && p.ReturnTrackingCode != "" {
					foundWithTracking = true
				}
			}
		}
		if !foundWithTracking {
			t.Fatalf("expected order.return_updated outbox event containing return_tracking_code=%q", acc.ReturnRequest.ReturnTrackingCode)
		}
	})

	t.Run("P2_4_AdminCannotCallBuyerConfirmReceivedOrRequestReturn", func(t *testing.T) {
		h := newTestHarness()
		ord := createDeliveredOrderHelper(t, h, "p24-rbac", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD)
		if _, err := h.svc.BuyerConfirmReceived(adminCtx, &orderv1.BuyerConfirmReceivedRequest{OrderId: ord.Id}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("admin BuyerConfirmReceived = %v, want PermissionDenied", err)
		}
		if _, err := h.svc.RequestReturn(adminCtx, &orderv1.RequestReturnRequest{OrderId: ord.Id, Reason: "admin"}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("admin RequestReturn = %v, want PermissionDenied", err)
		}
	})

	t.Run("P2_5_AutoCompleteIgnoresClosedReturnRequests", func(t *testing.T) {
		h := newTestHarness()
		now := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
		h.svc.nowFn = func() time.Time { return now }

		ord := createDeliveredOrderHelper(t, h, "p25-closed", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD)
		// Simulate legacy DB state from 0005: DELIVERED order with a CLOSED return_request row.
		h.store.SeedClosedReturnForDeliveredOrderForTest(ord.Id)

		now = now.Add(7*24*time.Hour + time.Minute)
		n, err := h.svc.ScanAutoCompleteDeliveredOrders(context.Background())
		if err != nil || n != 1 {
			t.Fatalf("ScanAutoCompleteDeliveredOrders with CLOSED return = %d, %v; want 1, nil", n, err)
		}
		got, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ord.Id})
		if got.Order.Status != orderv1.OrderStatus_ORDER_STATUS_COMPLETED {
			t.Fatalf("status = %v, want COMPLETED", got.Order.Status)
		}
	})

	t.Run("P2_6_RefundAmountVNDUpdatesOnRefundingRetryAfterPartialRefund", func(t *testing.T) {
		h := newTestHarness()
		ord := createDeliveredOrderHelper(t, h, "p26-amt", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR)
		retResp, _ := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord.Id, Reason: "defect"})
		retID := retResp.ReturnRequestId

		// Fail first CreateRefund so return stays in REFUNDING with initial refund_amount_vnd = TotalPayableVND.
		h.payment.refundErr = status.Error(codes.Unavailable, "payment blip")
		_, _ = h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: retID, Decision: orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_REFUND,
		})

		// Manual partial refund of 30,000 VND arrives via payment.refunded before retry!
		_ = h.svc.HandlePaymentRefunded(context.Background(), "evt-p26-part", events.PaymentRefundedPayload{
			RefundID: "ref-manual-1", PaymentID: ord.PaymentId, OrderID: ord.Id, AmountVND: 30000, Reason: "partial",
		})

		h.payment.refundErr = nil
		if n, err := h.svc.ScanRefundingReturns(context.Background()); err != nil || n != 1 {
			t.Fatalf("ScanRefundingReturns = %d, %v; want 1, nil", n, err)
		}
		gotRet, _ := h.svc.GetReturnRequest(buyerCtx, &orderv1.GetReturnRequestRequest{ReturnRequestId: retID})
		wantRem := ord.TotalPayableVnd - 30000
		if gotRet.ReturnRequest.RefundAmountVnd != wantRem {
			t.Fatalf("return.refund_amount_vnd = %d, want %d after partial refund", gotRet.ReturnRequest.RefundAmountVnd, wantRem)
		}
	})

	t.Run("P3_1_ZeroVNDOrderReturnedSyncsPaymentStatusCancelledOnOrderAndGroup", func(t *testing.T) {
		h := newTestHarness()
		h.promo.breakdown = &promotionv1.DiscountBreakdown{
			Shops: []*promotionv1.ShopDiscountBreakdown{
				{
					ShopId:                     "shop-1",
					SubtotalVnd:                1000000,
					PlatformVoucherDiscountVnd: 500000,
					FreeshipDiscountVnd:        30000,
					CoinsDiscountVnd:           500000,
					ShippingFeeVnd:             30000,
				},
			},
		}
		co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "p31-zero",
			BuyerId:           "buyer-1",
			AddressId:         "addr-buyer-1",
			Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PlatformVoucherId: "v-p",
			FreeshipVoucherId: "v-f",
			CoinsToUse:        500000,
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout 0 VND: %v", err)
		}
		ord := co.Orders[0]
		if ord.TotalPayableVnd != 0 {
			t.Fatalf("setup expected 0 VND order, got %d", ord.TotalPayableVnd)
		}
		shipResp, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord.Id, ShopId: "shop-1"})
		if err != nil {
			t.Fatalf("SellerShipOrder: %v", err)
		}
		if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-p31-deliv", events.ShipmentUpdatedPayload{
			OrderID: ord.Id, TrackingCode: shipResp.TrackingCode, Status: "delivered",
		}); err != nil {
			t.Fatalf("HandleShipmentUpdated(delivered): %v", err)
		}
		retResp, err := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord.Id, Reason: "defect"})
		if err != nil {
			t.Fatalf("RequestReturn: %v", err)
		}
		_, err = h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: retResp.ReturnRequestId, Decision: orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_REFUND,
		})
		if err != nil {
			t.Fatalf("RespondReturn: %v", err)
		}
		gotOrd, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ord.Id})
		if gotOrd.Order.PaymentStatus != "CANCELLED" {
			t.Fatalf("0-VND returned order payment_status = %q, want CANCELLED", gotOrd.Order.PaymentStatus)
		}
		grp, _ := h.store.GetCheckoutGroupByID(context.Background(), ord.CheckoutGroupId)
		if grp.PaymentStatus != "CANCELLED" {
			t.Fatalf("0-VND returned checkout_group payment_status = %q, want CANCELLED", grp.PaymentStatus)
		}
	})

	t.Run("P3_2_CreateShipmentValidationErrorClearsPendingShipmentCancel", func(t *testing.T) {
		h := newTestHarness()
		co, _ := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "p32-valerr",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		ordID := co.Orders[0].Id
		h.shipping.onCreateShipment = func(_ *shippingv1.CreateShipmentRequest) (*shippingv1.CreateShipmentResponse, error) {
			return nil, status.Error(codes.InvalidArgument, "bad address")
		}
		if _, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID, ShopId: "shop-1"}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("SellerShipOrder = %v, want InvalidArgument", err)
		}
		agg, _ := h.store.GetOrderWithDetails(context.Background(), ordID)
		if agg.Order.PendingShipmentCancel {
			t.Fatalf("pending_shipment_cancel must be cleared after InvalidArgument CreateShipment error")
		}
	})

	t.Run("P3_3_SellerShipOrderIdempotentWhenWebhookMovedToShippingWithSameTracking", func(t *testing.T) {
		h := newTestHarness()
		co, _ := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "p33-race-ship",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		ordID := co.Orders[0].Id
		h.shipping.onCreateShipment = func(req *shippingv1.CreateShipmentRequest) (*shippingv1.CreateShipmentResponse, error) {
			// Webhook delivering arrives BEFORE TransitionOrderPreparedForShipment runs!
			_ = h.svc.HandleShipmentUpdated(context.Background(), "evt-p33-go", events.ShipmentUpdatedPayload{
				OrderID: ordID, Status: "delivering", TrackingCode: "TRK-P33-FAST",
			})
			return &shippingv1.CreateShipmentResponse{
				Shipment: &shippingv1.Shipment{
					ShipmentId: "shp-p33", OrderId: req.GetOrderId(), CarrierCode: "ghn", ServiceCode: "standard",
					TrackingCode: "TRK-P33-FAST", LabelUrl: "https://labels.sandbox/p33-fast",
				},
			}, nil
		}
		resp, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID, ShopId: "shop-1"})
		if err != nil {
			t.Fatalf("SellerShipOrder racing with delivering webhook failed: %v", err)
		}
		if resp.Order.Status != orderv1.OrderStatus_ORDER_STATUS_SHIPPING || resp.Order.LabelUrl != "https://labels.sandbox/p33-fast" {
			t.Fatalf("expected SHIPPING with label_url preserved, got status=%v label=%q", resp.Order.Status, resp.Order.LabelUrl)
		}
		agg, _ := h.store.GetOrderWithDetails(context.Background(), ordID)
		if agg.Order.PendingShipmentCancel {
			t.Fatalf("pending_shipment_cancel must be cleared")
		}
	})
}
