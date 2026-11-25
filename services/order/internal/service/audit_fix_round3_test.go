package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/order/internal/clients"
	"github.com/nexusmart/core/services/order/internal/config"
	"github.com/nexusmart/core/services/order/internal/events"
	"github.com/nexusmart/core/services/order/internal/store"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// TestReview2O_P1_1_PaymentResultRedelivery_EventIDEmptyAndShippedOrder reproduces review2-O P1-1.
func TestReview2O_P1_1_PaymentResultRedelivery_EventIDEmptyAndShippedOrder(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
	sysCtx := ctxWithUser("payment-service", "system", "")

	t.Run("WatchdogExpired_TransientReleaseErr_ThenPaymentFailedEventRescues", func(t *testing.T) {
		h := newTestHarness()
		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "idem-r3-p11-wd",
			BuyerId:           "buyer-1",
			AddressId:         "addr-buyer-1",
			Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PlatformVoucherId: "v-plat",
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		h.store.SetOrderPaymentExpiresAt(resp.Orders[0].Id, time.Now().UTC().Add(-2*time.Minute))
		h.product.releaseErr = status.Error(codes.Unavailable, "product down during watchdog")

		// Watchdog cancels order (payment_status = EXPIRED) then fails ReleaseStock.
		if _, err := h.svc.ScanExpiredPendingOrders(context.Background()); err != nil {
			t.Fatalf("ScanExpiredPendingOrders: %v", err)
		}
		if len(h.promo.releaseCalls) != 0 {
			t.Fatalf("expected ReleasePromotion skipped while ReleaseStock failing, got %v", h.promo.releaseCalls)
		}

		// Product recovers; payment.failed event (emitted by CancelPayment) arrives and MUST rescue stock + promo!
		h.product.releaseErr = nil
		h.product.releaseCalls = nil
		if err := h.svc.HandlePaymentFailed(context.Background(), "evt-r3-p11-rescue", events.PaymentFailedPayload{
			PaymentID:    resp.PaymentId,
			OrderGroupID: resp.CheckoutGroupId,
			Reason:       "Payment expired",
		}); err != nil {
			t.Fatalf("HandlePaymentFailed rescue: %v", err)
		}
		if len(h.product.releaseCalls) != 1 || len(h.promo.releaseCalls) != 1 {
			t.Fatalf("P1-1(a) violated: payment.failed after watchdog EXPIRED did not rescue downstream: stock=%v promo=%v",
				h.product.releaseCalls, h.promo.releaseCalls)
		}
	})

	t.Run("MarkPaymentResult_TransientReleaseErr_RetryCompletesDownstream", func(t *testing.T) {
		h := newTestHarness()
		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "idem-r3-p11-mpr",
			BuyerId:           "buyer-1",
			AddressId:         "addr-buyer-1",
			Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PlatformVoucherId: "v-plat",
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		h.product.releaseErr = status.Error(codes.Unavailable, "product down")
		req := &orderv1.MarkPaymentResultRequest{
			CheckoutGroupId: resp.CheckoutGroupId,
			PaymentId:       resp.PaymentId,
			Status:          orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_FAILED,
			Reason:          "bank failed",
		}
		if _, err := h.svc.MarkPaymentResult(sysCtx, req); err == nil {
			t.Fatalf("expected error on first MarkPaymentResult when ReleaseStock fails")
		}

		// Retry MarkPaymentResult (EventID == "") after product recovers -> MUST complete ReleaseStock & ReleasePromotion!
		h.product.releaseErr = nil
		h.product.releaseCalls = nil
		if _, err := h.svc.MarkPaymentResult(sysCtx, req); err != nil {
			t.Fatalf("retry MarkPaymentResult: %v", err)
		}
		if len(h.product.releaseCalls) != 1 || len(h.promo.releaseCalls) != 1 {
			t.Fatalf("P1-1(a) violated: retry MarkPaymentResult skipped downstream: stock=%v promo=%v",
				h.product.releaseCalls, h.promo.releaseCalls)
		}
	})

	t.Run("PaymentSucceeded_RedeliveryAfterShipped_StillConfirmsStock", func(t *testing.T) {
		h := newTestHarness()
		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "idem-r3-p11-ship",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		payload := events.PaymentSucceededPayload{
			PaymentID:    resp.PaymentId,
			OrderGroupID: resp.CheckoutGroupId,
			ProviderTxID: "tx-p11",
		}

		// Delivery #1 commits PENDING -> CONFIRMED then fails ConfirmStock.
		h.product.confirmErr = status.Error(codes.Unavailable, "product blip")
		if err := h.svc.HandlePaymentSucceeded(context.Background(), "evt-r3-p11-ok", payload); err == nil {
			t.Fatalf("expected error on transient ConfirmStock failure")
		}
		// Before NATS redelivery, seller ships order (CONFIRMED -> SHIPPING).
		if _, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{
			OrderId: resp.Orders[0].Id,
			ShopId:  "shop-1",
		}); err != nil {
			t.Fatalf("SellerShipOrder: %v", err)
		}

		// Delivery #2 after product recovers MUST still invoke ConfirmStock!
		h.product.confirmErr = nil
		h.product.confirmCalls = nil
		if err := h.svc.HandlePaymentSucceeded(context.Background(), "evt-r3-p11-ok", payload); err != nil {
			t.Fatalf("redelivered HandlePaymentSucceeded: %v", err)
		}
		if len(h.product.confirmCalls) != 1 {
			t.Fatalf("P1-1(b) violated: ConfirmStock not called on redelivery after order moved to SHIPPING, calls=%v", h.product.confirmCalls)
		}
	})
}

// TestReview2O_P1_2_RefundedBeforeSucceededOrExpiry_NeverDowngradesPaymentStatus reproduces review2-O P1-2.
func TestReview2O_P1_2_RefundedBeforeSucceededOrExpiry_NeverDowngradesPaymentStatus(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

	t.Run("COD_RefundedBeforeSucceeded_KeepsRefunded", func(t *testing.T) {
		h := newTestHarness()
		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "idem-r3-p12-cod",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil {
			t.Fatalf("CreateCheckout COD: %v", err)
		}
		ord := resp.Orders[0]
		_, _ = h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord.Id, ShopId: "shop-1"})
		_ = h.svc.HandleShipmentUpdated(context.Background(), "evt-r3-p12-deliv", events.ShipmentUpdatedPayload{
			OrderID: ord.Id, Status: "delivered",
		})

		// Out-of-order: payment.refunded (100%) arrives BEFORE payment.succeeded!
		if err := h.svc.HandlePaymentRefunded(context.Background(), "evt-r3-p12-ref", events.PaymentRefundedPayload{
			RefundID: "ref-cod-1", PaymentID: resp.PaymentId, OrderID: ord.Id, AmountVND: ord.TotalPayableVnd, Reason: "full refund",
		}); err != nil {
			t.Fatalf("HandlePaymentRefunded: %v", err)
		}
		if err := h.svc.HandlePaymentSucceeded(context.Background(), "evt-r3-p12-ok", events.PaymentSucceededPayload{
			PaymentID: resp.PaymentId, OrderGroupID: resp.CheckoutGroupId, OrderID: ord.Id,
		}); err != nil {
			t.Fatalf("HandlePaymentSucceeded: %v", err)
		}

		got, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ord.Id})
		if got.Order.PaymentStatus != "REFUNDED" {
			t.Fatalf("P1-2(a) violated: COD order payment_status downgraded to %q, want REFUNDED", got.Order.PaymentStatus)
		}
		grp, _ := h.store.GetCheckoutGroupByID(context.Background(), resp.CheckoutGroupId)
		if grp.PaymentStatus != "REFUNDED" {
			t.Fatalf("P1-2(a) violated: COD group payment_status downgraded to %q, want REFUNDED", grp.PaymentStatus)
		}
	})

	t.Run("VietQR_RefundedBeforeSucceeded_KeepsRefunded", func(t *testing.T) {
		h := newTestHarness()
		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "idem-r3-p12-vq",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout VietQR: %v", err)
		}
		ord := resp.Orders[0]
		_ = h.svc.HandlePaymentRefunded(context.Background(), "evt-r3-p12-vq-ref", events.PaymentRefundedPayload{
			RefundID: "ref-vq-1", PaymentID: resp.PaymentId, OrderID: ord.Id, AmountVND: ord.TotalPayableVnd, Reason: "full refund",
		})
		if err := h.svc.HandlePaymentSucceeded(context.Background(), "evt-r3-p12-vq-ok", events.PaymentSucceededPayload{
			PaymentID: resp.PaymentId, OrderGroupID: resp.CheckoutGroupId,
		}); err != nil {
			t.Fatalf("HandlePaymentSucceeded: %v", err)
		}

		got, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ord.Id})
		if got.Order.Status != orderv1.OrderStatus_ORDER_STATUS_CONFIRMED {
			t.Fatalf("order status = %v, want CONFIRMED", got.Order.Status)
		}
		if got.Order.PaymentStatus != "REFUNDED" {
			t.Fatalf("P1-2(b) violated: VietQR order payment_status downgraded to %q, want REFUNDED", got.Order.PaymentStatus)
		}
		grp, _ := h.store.GetCheckoutGroupByID(context.Background(), resp.CheckoutGroupId)
		if grp.PaymentStatus != "REFUNDED" {
			t.Fatalf("P1-2(b) violated: VietQR group payment_status downgraded to %q, want REFUNDED", grp.PaymentStatus)
		}
	})

	t.Run("VietQR_ExpiredWatchdog_WhenPaymentAlreadyRefunded_KeepsRefunded", func(t *testing.T) {
		h := newTestHarness()
		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "idem-r3-p12-exp",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout VietQR: %v", err)
		}
		h.store.SetOrderPaymentExpiresAt(resp.Orders[0].Id, time.Now().UTC().Add(-2*time.Minute))
		h.payment.getPaymentStatus = paymentv1.PaymentStatus_PAYMENT_STATUS_REFUNDED

		if _, err := h.svc.ScanExpiredPendingOrders(context.Background()); err != nil {
			t.Fatalf("ScanExpiredPendingOrders: %v", err)
		}

		got, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: resp.Orders[0].Id})
		if got.Order.PaymentStatus != "REFUNDED" {
			t.Fatalf("P1-2(b) violated: watchdog overwrote REFUNDED payment_status to %q", got.Order.PaymentStatus)
		}
		grp, _ := h.store.GetCheckoutGroupByID(context.Background(), resp.CheckoutGroupId)
		if grp.PaymentStatus != "REFUNDED" {
			t.Fatalf("P1-2(b) violated: watchdog overwrote group payment_status to %q, want REFUNDED", grp.PaymentStatus)
		}
	})
}

// supersedingPaymentClient simulates payment service's supersede_previous=true behavior
// and allows deterministic overlap between two pods in P1-3 test.
type supersedingPaymentClient struct {
	paymentv1.PaymentServiceClient
	mu             sync.Mutex
	seq            int
	payments       map[string]*paymentv1.Payment
	byIdemKey      map[string]*paymentv1.Payment
	createCalls    int
	cancelCalls    []string
	firstRetryHit  chan struct{}
	secondRetryHit chan struct{}
	releaseFirst   chan struct{}
}

func newSupersedingPaymentClient() *supersedingPaymentClient {
	return &supersedingPaymentClient{
		payments:       make(map[string]*paymentv1.Payment),
		byIdemKey:      make(map[string]*paymentv1.Payment),
		firstRetryHit:  make(chan struct{}),
		secondRetryHit: make(chan struct{}),
		releaseFirst:   make(chan struct{}),
	}
}

func (c *supersedingPaymentClient) CreatePayment(_ context.Context, req *paymentv1.CreatePaymentRequest, _ ...grpc.CallOption) (*paymentv1.CreatePaymentResponse, error) {
	isRetry := req.GetSupersedePrevious()
	var retryNum int
	c.mu.Lock()
	if existing, ok := c.byIdemKey[req.GetIdempotencyKey()]; ok && req.GetIdempotencyKey() != "" {
		cp, _ := proto.Clone(existing).(*paymentv1.Payment)
		c.mu.Unlock()
		return &paymentv1.CreatePaymentResponse{Payment: cp}, nil
	}
	c.seq++
	c.createCalls++
	if isRetry {
		retryNum = c.createCalls - 1
	}
	if isRetry {
		for _, p := range c.payments {
			if p.GetOrderGroupId() == req.GetOrderGroupId() && p.GetStatus() == paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING {
				p.Status = paymentv1.PaymentStatus_PAYMENT_STATUS_CANCELLED // represents superseded
			}
		}
	}
	payID := fmt.Sprintf("pay-%d", c.seq)
	p := &paymentv1.Payment{
		PaymentId:       payID,
		OrderGroupId:    req.GetOrderGroupId(),
		OrderId:         req.GetOrderId(),
		UserId:          req.GetUserId(),
		AmountVnd:       req.GetAmountVnd(),
		Method:          req.GetMethod(),
		Status:          paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING,
		QrUrl:           "https://qr/" + payID,
		QrCode:          "qr-" + payID,
		ShopAllocations: req.GetShopAllocations(),
		ExpiresAt:       req.GetExpiresAt(),
	}
	c.payments[payID] = p
	if req.GetIdempotencyKey() != "" {
		c.byIdemKey[req.GetIdempotencyKey()] = p
	}
	cp, _ := proto.Clone(p).(*paymentv1.Payment)
	c.mu.Unlock()

	if isRetry && retryNum == 1 {
		close(c.firstRetryHit)
		// Wait briefly so an unserialized Pod 2 overlaps here before Pod 1 writes order_db.
		select {
		case <-c.secondRetryHit:
		case <-time.After(60 * time.Millisecond):
		}
	} else if isRetry && retryNum == 2 {
		close(c.secondRetryHit)
	}
	return &paymentv1.CreatePaymentResponse{Payment: cp}, nil
}

func (c *supersedingPaymentClient) GetPayment(_ context.Context, req *paymentv1.GetPaymentRequest, _ ...grpc.CallOption) (*paymentv1.GetPaymentResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.payments[req.GetPaymentId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "not found")
	}
	cp, _ := proto.Clone(p).(*paymentv1.Payment)
	return &paymentv1.GetPaymentResponse{Payment: cp}, nil
}

func (c *supersedingPaymentClient) CancelPayment(_ context.Context, req *paymentv1.CancelPaymentRequest, _ ...grpc.CallOption) (*paymentv1.CancelPaymentResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cancelCalls = append(c.cancelCalls, req.GetPaymentId())
	if p, ok := c.payments[req.GetPaymentId()]; ok {
		p.Status = paymentv1.PaymentStatus_PAYMENT_STATUS_CANCELLED
	}
	return &paymentv1.CancelPaymentResponse{
		Payment: &paymentv1.Payment{PaymentId: req.GetPaymentId(), Status: paymentv1.PaymentStatus_PAYMENT_STATUS_CANCELLED},
	}, nil
}

func (c *supersedingPaymentClient) GetFeeConfig(_ context.Context, req *paymentv1.GetFeeConfigRequest, _ ...grpc.CallOption) (*paymentv1.GetFeeConfigResponse, error) {
	return &paymentv1.GetFeeConfigResponse{
		ActiveConfigs: []*paymentv1.PaymentFeeConfigItem{
			{Id: "fee-default", PaymentMethod: req.GetPaymentMethod(), FeeBps: 0},
		},
	}, nil
}

func (c *supersedingPaymentClient) GetPaymentMethodConfigs(_ context.Context, _ *paymentv1.GetPaymentMethodConfigsRequest, _ ...grpc.CallOption) (*paymentv1.GetPaymentMethodConfigsResponse, error) {
	return &paymentv1.GetPaymentMethodConfigsResponse{
		Configs: []*paymentv1.PaymentMethodConfig{
			{Method: paymentv1.PaymentMethod_PAYMENT_METHOD_COD, Enabled: true, MaxOrderVnd: 5000000},
			{Method: paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR, Enabled: true, ExpiryMinutes: 15},
			{Method: paymentv1.PaymentMethod_PAYMENT_METHOD_ZALOPAY, Enabled: true, ExpiryMinutes: 15},
		},
	}, nil
}

func (c *supersedingPaymentClient) AdjustCODShippingFee(_ context.Context, req *paymentv1.AdjustCODShippingFeeRequest, _ ...grpc.CallOption) (*paymentv1.AdjustCODShippingFeeResponse, error) {
	return &paymentv1.AdjustCODShippingFeeResponse{
		NewShippingFeeVnd: req.GetNewShippingFeeVnd(),
	}, nil
}

// TestReview2O_P1_3_MultiReplicaRetryPayment_DifferentKeysSerialized reproduces review2-O P1-3.
func TestReview2O_P1_3_MultiReplicaRetryPayment_DifferentKeysSerialized(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sharedStore := store.NewMemoryStore()
	payClient := newSupersedingPaymentClient()
	hBase := newTestHarness()
	cfg := config.Config{
		ServiceName:         "order",
		CODMaxRejectedCount: 3,
		RewardCoinsBPS:      100,
		GRPCCallTimeout:     5 * time.Second,
	}
	bundle := &clients.Bundle{
		User:          hBase.user,
		Product:       hBase.product,
		Cart:          hBase.cart,
		Promotion:     hBase.promo,
		Payment:       payClient,
		Shipping:      hBase.shipping,
		MethodConfigs: hBase.methodConfigs,
	}
	// Two separate pods (independent in-process idemLocks!) sharing the same DB store.
	pod1 := NewOrderService(cfg, sharedStore, bundle, logger.New("pod1", logger.WithWriter(io.Discard)))
	pod2 := NewOrderService(cfg, sharedStore, bundle, logger.New("pod2", logger.WithWriter(io.Discard)))

	checkoutResp, err := pod1.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "idem-r3-p13",
		BuyerId:        "buyer-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
	})
	if err != nil {
		t.Fatalf("CreateCheckout: %v", err)
	}
	oldPayID := checkoutResp.PaymentId

	var wg sync.WaitGroup
	wg.Add(2)
	var resp1, resp2 *orderv1.RetryPaymentResponse
	var err1, err2 error

	go func() {
		defer wg.Done()
		resp1, err1 = pod1.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
			PaymentId:      oldPayID,
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
			IdempotencyKey: "K1",
		})
	}()
	go func() {
		defer wg.Done()
		// Wait until pod1 starts CreatePayment so unserialized pods reliably race.
		<-payClient.firstRetryHit
		resp2, err2 = pod2.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
			PaymentId:      oldPayID,
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
			IdempotencyKey: "K2",
		})
	}()
	wg.Wait()

	if err1 != nil || resp1 == nil {
		t.Fatalf("pod1 RetryPayment failed: %v", err1)
	}
	if status.Code(err2) != codes.FailedPrecondition {
		t.Fatalf("P1-3 violated: pod2 with different key K2 got resp=%+v err=%v, want FailedPrecondition", resp2, err2)
	}

	payClient.mu.Lock()
	livePay := payClient.payments[resp1.PaymentId]
	createCalls := payClient.createCalls
	cancelCalls := append([]string(nil), payClient.cancelCalls...)
	payClient.mu.Unlock()

	if createCalls != 2 { // 1 initial checkout + 1 retry
		t.Fatalf("P1-3 violated: CreatePayment called %d times, want 2 (1 checkout + 1 serialized retry)", createCalls)
	}
	if len(cancelCalls) != 0 {
		t.Fatalf("P1-3 violated: CancelPayment called %v, want 0", cancelCalls)
	}
	if livePay == nil || livePay.GetStatus() != paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING {
		t.Fatalf("P1-3 violated: payment %q stored in order_db is NOT live PENDING, status=%v", resp1.PaymentId, livePay.GetStatus())
	}
}

// TestReview2O_P1_4a_CancelOrder_ReleaseStockTransientErr_DoesNotRestockPhantomStock reproduces review2-O P1-4a.
func TestReview2O_P1_4a_CancelOrder_ReleaseStockTransientErr_DoesNotRestockPhantomStock(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	h := newTestHarness()
	resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "idem-r3-p14a",
		BuyerId:        "buyer-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 2}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	if err != nil {
		t.Fatalf("CreateCheckout: %v", err)
	}

	// Inject transient error on ReleaseStock -> must retry AND must NOT fall back to RestockOrderItems!
	h.product.releaseErr = status.Error(codes.Unavailable, "product temporarily unavailable")
	if _, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{
		OrderId: resp.Orders[0].Id,
		Reason:  "buyer cancel",
	}); err != nil {
		t.Fatalf("CancelOrder: %v", err)
	}
	if len(h.product.releaseCalls) < 3 {
		t.Fatalf("P1-4a violated: ReleaseStock called %d times, want retried (>=3)", len(h.product.releaseCalls))
	}
	if len(h.product.restockCalls) != 0 {
		t.Fatalf("P1-4a violated: transient ReleaseStock error fell back to RestockOrderItems (%d calls), creating phantom stock", len(h.product.restockCalls))
	}
}

// TestReview2O_ManagerDecision_CancelUnpaidVietQRSubOrder_CancelsWholeGroup verifies Manager's Round 3 decision.
func TestReview2O_ManagerDecision_CancelUnpaidVietQRSubOrder_CancelsWholeGroup(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")

	t.Run("UnpaidVietQR_CancelOneSubOrder_CancelsAllPendingSiblings", func(t *testing.T) {
		h := newTestHarness()
		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "idem-r3-mgr-unpaid",
			BuyerId:           "buyer-1",
			AddressId:         "addr-buyer-1",
			FromCart:          true,
			PlatformVoucherId: "v-plat",
			CoinsToUse:        5000,
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil || len(resp.Orders) != 2 {
			t.Fatalf("CreateCheckout 2-shop VietQR: err=%v len=%d", err, len(resp.Orders))
		}

		if _, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{
			OrderId: resp.Orders[0].Id,
			Reason:  "buyer cancelled unpaid VietQR",
		}); err != nil {
			t.Fatalf("CancelOrder: %v", err)
		}

		// Both sub-orders MUST be CANCELLED with payment_status = CANCELLED.
		for _, o := range resp.Orders {
			got, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: o.Id})
			if got.Order.Status != orderv1.OrderStatus_ORDER_STATUS_CANCELLED || got.Order.PaymentStatus != "CANCELLED" {
				t.Fatalf("sub-order %s = (%v, %q), want (CANCELLED, CANCELLED)", o.Id, got.Order.Status, got.Order.PaymentStatus)
			}
		}
		grp, _ := h.store.GetCheckoutGroupByID(context.Background(), resp.CheckoutGroupId)
		if grp.PaymentStatus != "CANCELLED" {
			t.Fatalf("group payment_status = %q, want CANCELLED", grp.PaymentStatus)
		}

		// Exactly 1 ReleaseStock, 1 ReleasePromotion, 1 CancelPayment, 0 RestockOrderItems, 0 ReleasePromotionShop.
		if len(h.product.releaseCalls) != 1 || len(h.promo.releaseCalls) != 1 || len(h.payment.cancelCalls) != 1 {
			t.Fatalf("group compensation counts: releaseStock=%d releasePromo=%d cancelPay=%d, want 1/1/1",
				len(h.product.releaseCalls), len(h.promo.releaseCalls), len(h.payment.cancelCalls))
		}
		if len(h.product.restockCalls) != 0 || len(h.promo.releaseShopCalls) != 0 {
			t.Fatalf("unpaid VietQR group cancel must not call per-shop restock=%d or promoShop=%d",
				len(h.product.restockCalls), len(h.promo.releaseShopCalls))
		}

		// order.cancelled outbox emitted once for EACH sub-order.
		cancelledIDs := make(map[string]bool)
		for _, ev := range h.store.OutboxEvents() {
			if ev.EventType == events.SubjectOrderCancelled {
				var p events.OrderCancelledPayload
				_ = json.Unmarshal(ev.PayloadJSON, &p)
				cancelledIDs[p.OrderID] = true
			}
		}
		if len(cancelledIDs) != 2 || !cancelledIDs[resp.Orders[0].Id] || !cancelledIDs[resp.Orders[1].Id] {
			t.Fatalf("expected order.cancelled emitted for both sub-orders, got %v", cancelledIDs)
		}
	})

	t.Run("PaidVietQR_CancelOneSubOrder_KeepsSiblingConfirmed", func(t *testing.T) {
		h := newTestHarness()
		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "idem-r3-mgr-paid",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			FromCart:       true,
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil || len(resp.Orders) != 2 {
			t.Fatalf("CreateCheckout: %v", err)
		}
		_ = h.svc.HandlePaymentSucceeded(context.Background(), "evt-r3-mgr-ok", events.PaymentSucceededPayload{
			PaymentID: resp.PaymentId, OrderGroupID: resp.CheckoutGroupId,
		})
		if _, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{
			OrderId: resp.Orders[0].Id,
			Reason:  "cancel paid sub-order 1",
		}); err != nil {
			t.Fatalf("CancelOrder paid sub-order: %v", err)
		}
		sib, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: resp.Orders[1].Id})
		if sib.Order.Status != orderv1.OrderStatus_ORDER_STATUS_CONFIRMED {
			t.Fatalf("paid sibling status = %v, want CONFIRMED", sib.Order.Status)
		}
	})
}

// TestReview2O_P2_5_RecordPaymentRefunded_MultiShopAndGroupPartialEdgeCases reproduces review2-O P2-5.
func TestReview2O_P2_5_RecordPaymentRefunded_MultiShopAndGroupPartialEdgeCases(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	seller1Ctx := ctxWithUser("seller-1", "seller", "shop-1")

	t.Run("A_TwoShopCOD_OneCancelledOneRefunded_BothOrderingsSyncGroupRefunded", func(t *testing.T) {
		// Ordering 1: cancel ord2 first, then refund ord1.
		h1 := newTestHarness()
		r1, err := h1.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "idem-r3-p25-a1",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			FromCart:       true,
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil || len(r1.Orders) != 2 {
			t.Fatalf("CreateCheckout: %v", err)
		}
		ord1, ord2 := r1.Orders[0], r1.Orders[1]
		_, _ = h1.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: ord2.Id, Reason: "cancel shop2"})
		_, _ = h1.svc.SellerShipOrder(seller1Ctx, &orderv1.SellerShipOrderRequest{OrderId: ord1.Id, ShopId: ord1.ShopId})
		_ = h1.svc.HandleShipmentUpdated(context.Background(), "evt-p25-a1-deliv", events.ShipmentUpdatedPayload{
			OrderID: ord1.Id, Status: "delivered",
		})
		_ = h1.svc.HandlePaymentSucceeded(context.Background(), "evt-p25-a1-paid", events.PaymentSucceededPayload{
			PaymentID: r1.PaymentId, OrderGroupID: r1.CheckoutGroupId, OrderID: ord1.Id,
		})
		_ = h1.svc.HandlePaymentRefunded(context.Background(), "evt-p25-a1-ref", events.PaymentRefundedPayload{
			RefundID: "ref-a1", PaymentID: r1.PaymentId, OrderID: ord1.Id, AmountVND: ord1.TotalPayableVnd, Reason: "full",
		})
		grp1, _ := h1.store.GetCheckoutGroupByID(context.Background(), r1.CheckoutGroupId)
		if grp1.PaymentStatus != "REFUNDED" {
			t.Fatalf("P2-5(a) ordering 1 violated: group payment_status = %q, want REFUNDED", grp1.PaymentStatus)
		}

		// Ordering 2: refund ord1 first, then cancel ord2.
		h2 := newTestHarness()
		r2, err := h2.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "idem-r3-p25-a2",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			FromCart:       true,
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil || len(r2.Orders) != 2 {
			t.Fatalf("CreateCheckout: %v", err)
		}
		ord1, ord2 = r2.Orders[0], r2.Orders[1]
		_, _ = h2.svc.SellerShipOrder(seller1Ctx, &orderv1.SellerShipOrderRequest{OrderId: ord1.Id, ShopId: ord1.ShopId})
		_ = h2.svc.HandleShipmentUpdated(context.Background(), "evt-p25-a2-deliv", events.ShipmentUpdatedPayload{
			OrderID: ord1.Id, Status: "delivered",
		})
		_ = h2.svc.HandlePaymentSucceeded(context.Background(), "evt-p25-a2-paid", events.PaymentSucceededPayload{
			PaymentID: r2.PaymentId, OrderGroupID: r2.CheckoutGroupId, OrderID: ord1.Id,
		})
		_ = h2.svc.HandlePaymentRefunded(context.Background(), "evt-p25-a2-ref", events.PaymentRefundedPayload{
			RefundID: "ref-a2", PaymentID: r2.PaymentId, OrderID: ord1.Id, AmountVND: ord1.TotalPayableVnd, Reason: "full",
		})
		_, _ = h2.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: ord2.Id, Reason: "cancel shop2"})
		grp2, _ := h2.store.GetCheckoutGroupByID(context.Background(), r2.CheckoutGroupId)
		if grp2.PaymentStatus != "REFUNDED" {
			t.Fatalf("P2-5(a) ordering 2 violated: group payment_status = %q, want REFUNDED", grp2.PaymentStatus)
		}
	})

	t.Run("B_TwoShopVietQR_ZeroPayableSiblingActive_DoesNotMarkGroupRefundedEarly", func(t *testing.T) {
		h := newTestHarness()
		// shop-2 completely discounted to 0 VND; shop-1 stays positive.
		h.promo.breakdown = &promotionv1.DiscountBreakdown{
			Shops: []*promotionv1.ShopDiscountBreakdown{
				{ShopId: "shop-1", SubtotalVnd: 1000000, ShippingFeeVnd: 30000},
				{ShopId: "shop-2", SubtotalVnd: 1000000, PlatformVoucherDiscountVnd: 1000000, FreeshipDiscountVnd: 30000, ShippingFeeVnd: 30000},
			},
		}
		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "idem-r3-p25-b",
			BuyerId:           "buyer-1",
			AddressId:         "addr-buyer-1",
			FromCart:          true,
			PlatformVoucherId: "v-plat",
			FreeshipVoucherId: "v-free",
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil || len(resp.Orders) != 2 {
			t.Fatalf("CreateCheckout: %v", err)
		}
		_ = h.svc.HandlePaymentSucceeded(context.Background(), "evt-p25-b-ok", events.PaymentSucceededPayload{
			PaymentID: resp.PaymentId, OrderGroupID: resp.CheckoutGroupId,
		})
		var paidOrd, zeroOrd *orderv1.Order
		for _, o := range resp.Orders {
			if o.TotalPayableVnd > 0 {
				paidOrd = o
			} else {
				zeroOrd = o
			}
		}
		if paidOrd == nil || zeroOrd == nil {
			t.Fatalf("expected 1 paid + 1 zero-payable sub-order, got %+v", resp.Orders)
		}

		// Refund paidOrd 100% while zeroOrd is still CONFIRMED -> group MUST stay SUCCEEDED!
		_ = h.svc.HandlePaymentRefunded(context.Background(), "evt-p25-b-ref", events.PaymentRefundedPayload{
			RefundID: "ref-b", PaymentID: resp.PaymentId, OrderID: paidOrd.Id, AmountVND: paidOrd.TotalPayableVnd, Reason: "full shop1",
		})
		grp, _ := h.store.GetCheckoutGroupByID(context.Background(), resp.CheckoutGroupId)
		if grp.PaymentStatus == "REFUNDED" {
			t.Fatalf("P2-5(b) violated: group marked REFUNDED while 0-VND sibling %s is still CONFIRMED", zeroOrd.Id)
		}
		// Now cancel zeroOrd -> group becomes REFUNDED!
		_, _ = h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: zeroOrd.Id, Reason: "cancel zero sibling"})
		grp, _ = h.store.GetCheckoutGroupByID(context.Background(), resp.CheckoutGroupId)
		if grp.PaymentStatus != "REFUNDED" {
			t.Fatalf("P2-5(b): after cancelling 0-VND sibling, group payment_status = %q, want REFUNDED", grp.PaymentStatus)
		}
	})

	t.Run("C_GroupPartialRefundReaches100PercentOfFirstOrder_MarksOrderRefunded", func(t *testing.T) {
		h := newTestHarness()
		// Make shop-1 = 430,000 and shop-2 = 1,030,000 so shop-1 (sorted first) reaches 100% on a 500,000 group refund.
		h.product.skus["sku-1"].PriceVnd = 400000
		h.product.skus["sku-2"].PriceVnd = 1000000
		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "idem-r3-p25-c",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			FromCart:       true,
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil || len(resp.Orders) != 2 {
			t.Fatalf("CreateCheckout: %v", err)
		}
		_ = h.svc.HandlePaymentSucceeded(context.Background(), "evt-p25-c-ok", events.PaymentSucceededPayload{
			PaymentID: resp.PaymentId, OrderGroupID: resp.CheckoutGroupId,
		})
		ord1, ord2 := resp.Orders[0], resp.Orders[1] // shop-1 (430k), shop-2 (1,030k)

		// Group-level partial refund of 500,000 covers 100% of ord1 (430k) + 70k of ord2!
		if err := h.svc.HandlePaymentRefunded(context.Background(), "evt-p25-c-g1", events.PaymentRefundedPayload{
			RefundID: "ref-c-g1", PaymentID: resp.PaymentId, OrderID: resp.CheckoutGroupId, AmountVND: 500000, Reason: "partial group",
		}); err != nil {
			t.Fatalf("group partial refund: %v", err)
		}
		got1, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ord1.Id})
		if got1.Order.PaymentStatus != "REFUNDED" {
			t.Fatalf("P2-5(c) violated: ord1 covered 100%% by group partial refund has payment_status=%q, want REFUNDED", got1.Order.PaymentStatus)
		}
		// Remaining sub-order refund on ord2 (1,030,000 - 70,000 = 960,000) -> BOTH orders and group become REFUNDED!
		if err := h.svc.HandlePaymentRefunded(context.Background(), "evt-p25-c-s2", events.PaymentRefundedPayload{
			RefundID: "ref-c-s2", PaymentID: resp.PaymentId, OrderID: ord2.Id, AmountVND: ord2.TotalPayableVnd - 70000, Reason: "rest of ord2",
		}); err != nil {
			t.Fatalf("sub-order refund ord2: %v", err)
		}
		grp, _ := h.store.GetCheckoutGroupByID(context.Background(), resp.CheckoutGroupId)
		if grp.PaymentStatus != "REFUNDED" {
			t.Fatalf("P2-5(c) violated: after ord2 finishes refund, group payment_status=%q, want REFUNDED", grp.PaymentStatus)
		}
	})
}

// TestReview2O_P2_7_RetryPayment_NonPendingReturnedPaymentRejected reproduces review2-O P2-7.
func TestReview2O_P2_7_RetryPayment_NonPendingReturnedPaymentRejected(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	h, hs := newHookedHarness()
	resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "idem-r3-p27",
		BuyerId:        "buyer-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
	})
	if err != nil {
		t.Fatalf("CreateCheckout: %v", err)
	}

	// Attempt #1: CreatePayment creates pay-2, then DB fails transiently before committing order_db.
	var createdPay2 string
	hs.beforeUpdateGroupPaymentRetry = func(_ context.Context, params store.RetryPaymentUpdateParams) error {
		createdPay2 = params.NewPaymentID
		return errors.New("transient db timeout")
	}
	if _, err := h.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
		PaymentId:      resp.PaymentId,
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		IdempotencyKey: "k-p27",
	}); err == nil {
		t.Fatalf("expected transient DB error on RetryPayment #1")
	}
	hs.beforeUpdateGroupPaymentRetry = nil

	// Before client retries, pay-2 becomes CANCELLED in payment_db.
	h.payment.mu.Lock()
	h.payment.paymentsByID[createdPay2].Status = paymentv1.PaymentStatus_PAYMENT_STATUS_CANCELLED
	h.payment.mu.Unlock()

	// Attempt #2 with same key receives terminal pay-2 (CANCELLED) -> MUST return FailedPrecondition and keep order on pay-1!
	if _, err := h.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
		PaymentId:      resp.PaymentId,
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		IdempotencyKey: "k-p27",
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("P2-7 violated: RetryPayment returned %v for non-PENDING payment %s, want FailedPrecondition", err, createdPay2)
	}
	got, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: resp.Orders[0].Id})
	if got.Order.PaymentStatus == "CANCELLED" {
		t.Fatalf("P2-7 violated: PENDING order had payment_status overwritten to CANCELLED")
	}
}

// TestReview2O_P3_8_ShippingReturnedZeroPayableOrder_SetsPaymentStatusCancelled reproduces review2-O P3-8.
func TestReview2O_P3_8_ShippingReturnedZeroPayableOrder_SetsPaymentStatusCancelled(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
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

	resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey:    "idem-r3-p38-zero",
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
	ordID := resp.Orders[0].Id
	shipResp, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID, ShopId: "shop-1"})
	if err != nil {
		t.Fatalf("SellerShipOrder: %v", err)
	}
	if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-r3-p38-ret", events.ShipmentUpdatedPayload{
		OrderID: ordID, TrackingCode: shipResp.TrackingCode, Status: "returned", Description: "delivery failed on 0 VND order",
	}); err != nil {
		t.Fatalf("HandleShipmentUpdated(returned): %v", err)
	}
	got, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ordID})
	if got.Order.PaymentStatus != "CANCELLED" {
		t.Fatalf("P3-8 violated: 0 VND order payment_status = %q after SHIPPING->RETURNED, want CANCELLED", got.Order.PaymentStatus)
	}
}
