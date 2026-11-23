package service

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/order/internal/clients"
	"github.com/nexusmart/core/services/order/internal/config"
	"github.com/nexusmart/core/services/order/internal/db"
	"github.com/nexusmart/core/services/order/internal/events"
	"github.com/nexusmart/core/services/order/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// hookStore wraps *store.MemoryStore to inject deterministic concurrency & failure scenarios in unit tests.
type hookStore struct {
	*store.MemoryStore
	beforeListOrdersByGroup       func(ctx context.Context, groupID string) error
	beforeTransitionPaymentResult func(ctx context.Context, params store.PaymentResultTransitionParams)
	beforeTransitionOrderStatus   func(ctx context.Context, params store.SingleOrderTransitionParams)
	afterTransitionOrderStatus    func(ctx context.Context, params store.SingleOrderTransitionParams)
	beforeUpdateGroupPaymentRetry func(ctx context.Context, params store.RetryPaymentUpdateParams) error
}

func (hs *hookStore) ListOrdersByCheckoutGroup(ctx context.Context, checkoutGroupID string) ([]store.OrderAggregate, error) {
	if hs.beforeListOrdersByGroup != nil {
		if err := hs.beforeListOrdersByGroup(ctx, checkoutGroupID); err != nil {
			return nil, err
		}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return hs.MemoryStore.ListOrdersByCheckoutGroup(ctx, checkoutGroupID)
}

func (hs *hookStore) TransitionOrdersPaymentResult(ctx context.Context, params store.PaymentResultTransitionParams) (*store.PaymentResultTransitionOutcome, error) {
	if hs.beforeTransitionPaymentResult != nil {
		hs.beforeTransitionPaymentResult(ctx, params)
	}
	return hs.MemoryStore.TransitionOrdersPaymentResult(ctx, params)
}

func (hs *hookStore) TransitionOrderStatus(ctx context.Context, params store.SingleOrderTransitionParams) (*store.OrderTransitionResult, error) {
	if hs.beforeTransitionOrderStatus != nil {
		hs.beforeTransitionOrderStatus(ctx, params)
	}
	res, err := hs.MemoryStore.TransitionOrderStatus(ctx, params)
	if hs.afterTransitionOrderStatus != nil {
		hs.afterTransitionOrderStatus(ctx, params)
	}
	return res, err
}

func (hs *hookStore) UpdateGroupPaymentOnRetry(ctx context.Context, params store.RetryPaymentUpdateParams) error {
	if hs.beforeUpdateGroupPaymentRetry != nil {
		if err := hs.beforeUpdateGroupPaymentRetry(ctx, params); err != nil {
			return err
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return hs.MemoryStore.UpdateGroupPaymentOnRetry(ctx, params)
}

func newHookedHarness() (*testHarness, *hookStore) {
	h := newTestHarness()
	hs := &hookStore{MemoryStore: h.store}
	cfg := config.Config{
		ServiceName:          "order",
		CODMaxVND:            5000000,
		CODMaxRejectedCount:  3,
		PaymentExpiryMinutes: 15,
		RewardCoinsBPS:       100,
		GRPCCallTimeout:      5 * time.Second,
	}
	bundle := &clients.Bundle{
		User:      h.user,
		Product:   h.product,
		Cart:      h.cart,
		Promotion: h.promo,
		Payment:   h.payment,
		Shipping:  h.shipping,
	}
	h.svc = NewOrderService(cfg, hs, bundle, logger.New("order-test", logger.WithWriter(io.Discard)))
	return h, hs
}

// TestReviewA_P0_1_PartialRefundThenReturnOrCancel_RefundsOnlyRemaining reproduces review-A P0-1.
func TestReviewA_P0_1_PartialRefundThenReturnOrCancel_RefundsOnlyRemaining(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

	t.Run("ReturnedAfterPartialRefund_RefundsRemainingOnly", func(t *testing.T) {
		h := newTestHarness()
		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "idem-p01-return",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		ord := resp.Orders[0] // total_payable_vnd = 1,030,000
		if err := h.svc.HandlePaymentSucceeded(context.Background(), "evt-p01-ok", events.PaymentSucceededPayload{
			PaymentID: resp.PaymentId, OrderGroupID: resp.CheckoutGroupId,
		}); err != nil {
			t.Fatalf("HandlePaymentSucceeded: %v", err)
		}
		// Partial admin refund of 300,000 VND.
		if err := h.svc.HandlePaymentRefunded(context.Background(), "evt-p01-part", events.PaymentRefundedPayload{
			RefundID: "ref-part-1", PaymentID: resp.PaymentId, OrderID: ord.Id, AmountVND: 300000, Reason: "partial",
		}); err != nil {
			t.Fatalf("HandlePaymentRefunded: %v", err)
		}
		shipResp, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord.Id, ShopId: "shop-1"})
		if err != nil {
			t.Fatalf("SellerShipOrder: %v", err)
		}
		if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-p01-ret", events.ShipmentUpdatedPayload{
			OrderID: ord.Id, TrackingCode: shipResp.TrackingCode, Status: "returned", Description: "returned to seller",
		}); err != nil {
			t.Fatalf("HandleShipmentUpdated(returned): %v", err)
		}
		wantRemaining := ord.TotalPayableVnd - 300000
		if len(h.payment.refundCalls) != 1 || h.payment.refundCalls[0].GetAmountVnd() != wantRemaining {
			t.Fatalf("CreateRefund amount = %+v, want single call with remaining %d VND", h.payment.refundCalls, wantRemaining)
		}
	})

	t.Run("CancelAfterPartialRefund_RefundsRemainingOnly", func(t *testing.T) {
		h := newTestHarness()
		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "idem-p01-cancel",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		ord := resp.Orders[0]
		_ = h.svc.HandlePaymentSucceeded(context.Background(), "evt-p01-c-ok", events.PaymentSucceededPayload{
			PaymentID: resp.PaymentId, OrderGroupID: resp.CheckoutGroupId,
		})
		_ = h.svc.HandlePaymentRefunded(context.Background(), "evt-p01-c-part", events.PaymentRefundedPayload{
			RefundID: "ref-c-part", PaymentID: resp.PaymentId, OrderID: ord.Id, AmountVND: 300000, Reason: "partial",
		})
		if _, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: ord.Id, Reason: "cancel"}); err != nil {
			t.Fatalf("CancelOrder: %v", err)
		}
		wantRemaining := ord.TotalPayableVnd - 300000
		if len(h.payment.refundCalls) != 1 || h.payment.refundCalls[0].GetAmountVnd() != wantRemaining {
			t.Fatalf("CreateRefund amount = %+v, want single call with remaining %d VND", h.payment.refundCalls, wantRemaining)
		}
	})
}

// TestReviewA_P0_2_PaymentFailedRaceWithRetry_DoesNotReleaseStockOrPromo reproduces review-A P0-2.
func TestReviewA_P0_2_PaymentFailedRaceWithRetry_DoesNotReleaseStockOrPromo(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")

	t.Run("RetryCommitsBeforePaymentFailedLocks_NoStockOrPromoRelease", func(t *testing.T) {
		h, _ := newHookedHarness()
		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "idem-p02-race",
			BuyerId:           "buyer-1",
			AddressId:         "addr-buyer-1",
			Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PlatformVoucherId: "v-plat",
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		oldPayID := resp.PaymentId

		var retriedPayID string
		racedStore := &p02RaceStore{
			MemoryStore: h.store,
			onAfterPreLockRead: func() {
				retryResp, rErr := h.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
					PaymentId:     oldPayID,
					PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
				})
				if rErr != nil {
					t.Errorf("concurrent RetryPayment failed: %v", rErr)
					return
				}
				retriedPayID = retryResp.PaymentId
			},
		}
		h.svc.store = racedStore

		if err := h.svc.HandlePaymentFailed(context.Background(), "evt-p02-fail-old", events.PaymentFailedPayload{
			PaymentID:    oldPayID,
			OrderGroupID: resp.CheckoutGroupId,
			Reason:       "old pay-1 failed",
		}); err != nil {
			t.Fatalf("HandlePaymentFailed: %v", err)
		}

		if retriedPayID == "" {
			t.Fatalf("expected concurrent RetryPayment race hook to fire")
		}

		if len(h.product.releaseCalls) != 0 {
			t.Fatalf("P0-2 race violated: ReleaseStock called %v despite RetryPayment moving group to %q", h.product.releaseCalls, retriedPayID)
		}
		if len(h.promo.releaseCalls) != 0 {
			t.Fatalf("P0-2 race violated: ReleasePromotion called %v despite RetryPayment moving group to %q", h.promo.releaseCalls, retriedPayID)
		}
		got, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: resp.Orders[0].Id})
		if got.Order.Status != orderv1.OrderStatus_ORDER_STATUS_PENDING || got.Order.PaymentId != retriedPayID {
			t.Fatalf("order status=%v payment_id=%q, want PENDING / %q", got.Order.Status, got.Order.PaymentId, retriedPayID)
		}
	})

	t.Run("TransientReleaseError_LeavesEventUnprocessedForRedelivery", func(t *testing.T) {
		h := newTestHarness()
		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "idem-p02-redeliver",
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
		payload := events.PaymentFailedPayload{
			PaymentID:    resp.PaymentId,
			OrderGroupID: resp.CheckoutGroupId,
			Reason:       "bank failed",
		}
		if err := h.svc.HandlePaymentFailed(context.Background(), "evt-p02-retry-1", payload); err == nil {
			t.Fatalf("expected error on transient ReleaseStock failure")
		}
		processed, _ := h.store.IsEventProcessed(context.Background(), "evt-p02-retry-1")
		if processed {
			t.Fatalf("event must NOT be marked processed when ReleaseStock failed")
		}

		// Redelivery after product recovers succeeds and completes both ReleaseStock & ReleasePromotion.
		h.product.releaseErr = nil
		if err := h.svc.HandlePaymentFailed(context.Background(), "evt-p02-retry-1", payload); err != nil {
			t.Fatalf("redelivered HandlePaymentFailed failed: %v", err)
		}
		processed, _ = h.store.IsEventProcessed(context.Background(), "evt-p02-retry-1")
		if !processed {
			t.Fatalf("event MUST be marked processed after redelivery succeeded")
		}
		if len(h.promo.releaseCalls) != 1 {
			t.Fatalf("expected ReleasePromotion called once on redelivery, got %v", h.promo.releaseCalls)
		}
	})
}

type p02RaceStore struct {
	*store.MemoryStore
	fired              bool
	onAfterPreLockRead func()
}

func (s *p02RaceStore) ListOrdersByCheckoutGroup(ctx context.Context, checkoutGroupID string) ([]store.OrderAggregate, error) {
	aggs, err := s.MemoryStore.ListOrdersByCheckoutGroup(ctx, checkoutGroupID)
	if err == nil && !s.fired && s.onAfterPreLockRead != nil {
		s.fired = true
		s.onAfterPreLockRead()
	}
	return aggs, err
}

func (s *p02RaceStore) TransitionOrdersPaymentResult(ctx context.Context, params store.PaymentResultTransitionParams) (*store.PaymentResultTransitionOutcome, error) {
	if !s.fired && s.onAfterPreLockRead != nil {
		s.fired = true
		s.onAfterPreLockRead()
	}
	return s.MemoryStore.TransitionOrdersPaymentResult(ctx, params)
}

// TestReviewA_P0_3_And_ReviewB_P0_2_RetryPaymentResilienceAndScopedIdemKey covers review-A P0-3 + review-B P0-2.
func TestReviewA_P0_3_And_ReviewB_P0_2_RetryPaymentResilienceAndScopedIdemKey(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")

	t.Run("ScopedIdemKeyPrefixAndDeterministicFallback", func(t *testing.T) {
		h := newTestHarness()
		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "idem-p03-key",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		r1, err := h.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
			PaymentId:      resp.PaymentId,
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
			IdempotencyKey: "client-k1",
		})
		if err != nil {
			t.Fatalf("RetryPayment with client key: %v", err)
		}
		wantKey1 := "retry:" + resp.CheckoutGroupId + ":client-k1"
		if got := h.payment.createCalls[len(h.payment.createCalls)-1].GetIdempotencyKey(); got != wantKey1 {
			t.Fatalf("CreatePayment idempotency_key = %q, want %q", got, wantKey1)
		}

		// Retry with empty client key -> deterministic "retry:<groupID>:<currentPaymentID>".
		if _, err := h.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
			PaymentId:     r1.PaymentId,
			PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		}); err != nil {
			t.Fatalf("RetryPayment with empty key: %v", err)
		}
		wantKey2 := "retry:" + resp.CheckoutGroupId + ":" + r1.PaymentId
		if got := h.payment.createCalls[len(h.payment.createCalls)-1].GetIdempotencyKey(); got != wantKey2 {
			t.Fatalf("CreatePayment fallback idempotency_key = %q, want deterministic %q", got, wantKey2)
		}
	})

	t.Run("SequentialIdempotentReplay_ReturnsSameNewPayment", func(t *testing.T) {
		h := newTestHarness()
		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "idem-p03-seq",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		r1, err := h.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
			PaymentId:      resp.PaymentId,
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
			IdempotencyKey: "same-retry-key",
		})
		if err != nil {
			t.Fatalf("RetryPayment #1: %v", err)
		}
		callsAfter1 := len(h.payment.createCalls)

		// Sequential replay with exact same (payment_id, idempotency_key) -> returns pay-2 idempotently!
		r2, err := h.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
			PaymentId:      resp.PaymentId,
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
			IdempotencyKey: "same-retry-key",
		})
		if err != nil {
			t.Fatalf("RetryPayment #2 sequential replay failed: %v", err)
		}
		if r2.PaymentId != r1.PaymentId || r2.CheckoutGroupId != r1.CheckoutGroupId {
			t.Fatalf("replay = %+v, want %+v", r2, r1)
		}
		if len(h.payment.createCalls) != callsAfter1 {
			t.Fatalf("replay should not create another payment, calls=%d want %d", len(h.payment.createCalls), callsAfter1)
		}
		// Different key on stale pay-1 must STILL fail with 409!
		if _, err := h.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
			PaymentId:      resp.PaymentId,
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
			IdempotencyKey: "different-key",
		}); status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("stale pay-1 with different key = %v, want FailedPrecondition", err)
		}
	})

	t.Run("MultiReplicaSameNewPayment_DoesNotCancelLivePayment", func(t *testing.T) {
		h, hs := newHookedHarness()
		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "idem-p03-replica",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		// Simulate Pod 1 committing pay-1 -> pay-2 right before Pod 2 executes UpdateGroupPaymentOnRetry with the SAME pay-2.
		pod1Committed := false
		hs.beforeUpdateGroupPaymentRetry = func(ctx context.Context, params store.RetryPaymentUpdateParams) error {
			if !pod1Committed {
				pod1Committed = true
				return hs.MemoryStore.UpdateGroupPaymentOnRetry(ctx, params)
			}
			return nil
		}
		r, err := h.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
			PaymentId:      resp.PaymentId,
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
			IdempotencyKey: "replica-key",
		})
		if err != nil {
			t.Fatalf("Pod 2 RetryPayment failed on idempotent same-payment update: %v", err)
		}
		if len(h.payment.cancelCalls) != 0 {
			t.Fatalf("Pod 2 MUST NOT CancelPayment(%s), got cancelCalls=%+v", r.PaymentId, h.payment.cancelCalls)
		}
	})

	t.Run("ClientDisconnectAfterCreatePayment_StillCommitsToDB", func(t *testing.T) {
		h, _ := newHookedHarness()
		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "idem-p03-disconnect",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}

		reqCtx, cancelReq := context.WithCancel(buyerCtx)
		defer cancelReq()
		h.payment.onCreatePayment = func(req *paymentv1.CreatePaymentRequest) {
			if req.GetSupersedePrevious() {
				cancelReq() // Simulate client disconnecting right as CreatePayment succeeds!
			}
		}

		r, err := h.svc.RetryPayment(reqCtx, &orderv1.RetryPaymentRequest{
			PaymentId:      resp.PaymentId,
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
			IdempotencyKey: "disconnect-key",
		})
		if err != nil {
			t.Fatalf("RetryPayment should persist pay-2 despite client ctx cancel: %v", err)
		}
		if len(h.payment.cancelCalls) != 0 {
			t.Fatalf("must NOT cancel newly created pay-2 on client disconnect, got %+v", h.payment.cancelCalls)
		}
		got, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: resp.Orders[0].Id})
		if got.Order.PaymentId != r.PaymentId {
			t.Fatalf("order_db payment_id = %q, want persisted %q", got.Order.PaymentId, r.PaymentId)
		}
	})
}

// TestReviewA_P0_4_CancelOrder_CanceledCtxAndTOCTOURaceWithPaymentSucceeded reproduces review-A P0-4.
func TestReviewA_P0_4_CancelOrder_CanceledCtxAndTOCTOURaceWithPaymentSucceeded(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")

	t.Run("CanceledCtxAfterCommit_StillReleasesShopOnlyNotGroup", func(t *testing.T) {
		h, hs := newHookedHarness()
		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "idem-p04-ctx",
			BuyerId:           "buyer-1",
			AddressId:         "addr-buyer-1",
			FromCart:          true,
			PlatformVoucherId: "v-plat",
			CoinsToUse:        5000,
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil || len(resp.Orders) != 2 {
			t.Fatalf("CreateCheckout 2-shop COD: %v", err)
		}

		reqCtx, cancelReq := context.WithCancel(buyerCtx)
		defer cancelReq()
		hs.afterTransitionOrderStatus = func(_ context.Context, _ store.SingleOrderTransitionParams) {
			cancelReq() // Client disconnects right after CancelOrder commits!
		}

		if _, err := h.svc.CancelOrder(reqCtx, &orderv1.CancelOrderRequest{
			OrderId: resp.Orders[0].Id,
			Reason:  "cancel shop 1",
		}); err != nil {
			t.Fatalf("CancelOrder: %v", err)
		}
		if len(h.promo.releaseCalls) != 0 {
			t.Fatalf("P0-4(a) violated: group ReleasePromotion called while sibling active: %v", h.promo.releaseCalls)
		}
		if len(h.payment.cancelCalls) != 0 {
			t.Fatalf("P0-4(a) violated: group CancelPayment called while sibling active: %+v", h.payment.cancelCalls)
		}
		if len(h.promo.releaseShopCalls) != 1 || h.promo.releaseShopCalls[0].GetShopId() != resp.Orders[0].ShopId {
			t.Fatalf("expected ReleasePromotionShop(%s), got %+v", resp.Orders[0].ShopId, h.promo.releaseShopCalls)
		}
	})

	t.Run("TOCTOURace_PendingBecomesConfirmedBeforeLock_RefundsAndPreservesSibling", func(t *testing.T) {
		h, hs := newHookedHarness()
		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "idem-p04-toctou",
			BuyerId:           "buyer-1",
			AddressId:         "addr-buyer-1",
			FromCart:          true,
			PlatformVoucherId: "v-plat",
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil || len(resp.Orders) != 2 {
			t.Fatalf("CreateCheckout 2-shop VIETQR: %v", err)
		}

		// Right before CancelOrder locks FOR UPDATE, payment.succeeded transitions BOTH orders to CONFIRMED/SUCCEEDED!
		injected := false
		hs.beforeTransitionOrderStatus = func(_ context.Context, _ store.SingleOrderTransitionParams) {
			if !injected {
				injected = true
				if pErr := h.svc.HandlePaymentSucceeded(context.Background(), "evt-p04-paid", events.PaymentSucceededPayload{
					PaymentID: resp.PaymentId, OrderGroupID: resp.CheckoutGroupId,
				}); pErr != nil {
					t.Errorf("HandlePaymentSucceeded failed: %v", pErr)
				}
			}
		}

		cancelResp, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{
			OrderId: resp.Orders[0].Id,
			Reason:  "cancel during payment race",
		})
		if err != nil {
			t.Fatalf("CancelOrder: %v", err)
		}
		if cancelResp.Order.PaymentStatus != "SUCCEEDED" {
			t.Fatalf("P0-4(b) violated: paid order payment_status overwritten to %q, want SUCCEEDED", cancelResp.Order.PaymentStatus)
		}
		// Sibling order must still be CONFIRMED!
		sib, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: resp.Orders[1].Id})
		if sib.Order.Status != orderv1.OrderStatus_ORDER_STATUS_CONFIRMED {
			t.Fatalf("sibling order status = %v, want CONFIRMED", sib.Order.Status)
		}
		// Must NOT release group promo/stock; must call ReleasePromotionShop + RestockOrderItems + CreateRefund!
		if len(h.promo.releaseCalls) != 0 || len(h.product.releaseCalls) != 0 {
			t.Fatalf("P0-4(b) violated: group release called promo=%v stock=%v", h.promo.releaseCalls, h.product.releaseCalls)
		}
		if len(h.promo.releaseShopCalls) != 1 || len(h.product.restockCalls) != 1 || len(h.payment.refundCalls) != 1 {
			t.Fatalf("expected shop promo release (1), restock (1), refund (1); got promoShop=%d restock=%d refund=%d",
				len(h.promo.releaseShopCalls), len(h.product.restockCalls), len(h.payment.refundCalls))
		}
		// Verify outbox order.cancelled has was_paid=true.
		foundPaidCancel := false
		for _, ev := range h.store.OutboxEvents() {
			if ev.EventType == events.SubjectOrderCancelled {
				var p events.OrderCancelledPayload
				_ = json.Unmarshal(ev.PayloadJSON, &p)
				if p.OrderID == resp.Orders[0].Id && p.WasPaid {
					foundPaidCancel = true
				}
			}
		}
		if !foundPaidCancel {
			t.Fatalf("expected order.cancelled outbox event with was_paid=true")
		}
	})
}

// TestReviewA_P1_7_HandleShipmentReturned_DownstreamErrorTriggersRedelivery reproduces review-A P1-7.
func TestReviewA_P1_7_HandleShipmentReturned_DownstreamErrorTriggersRedelivery(t *testing.T) {
	h := newTestHarness()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

	resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey:    "idem-p17-ret",
		BuyerId:           "buyer-1",
		AddressId:         "addr-buyer-1",
		Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PlatformVoucherId: "v-plat",
		PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	if err != nil {
		t.Fatalf("CreateCheckout: %v", err)
	}
	ordID := resp.Orders[0].Id
	shipResp, _ := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID, ShopId: "shop-1"})

	// Inject transient failure in User.RecordCODRejection.
	h.user.codRejectionErr = status.Error(codes.Unavailable, "user service unavailable")
	payload := events.ShipmentUpdatedPayload{
		OrderID:      ordID,
		TrackingCode: shipResp.TrackingCode,
		Status:       "returned",
		Description:  "Buyer refused COD",
	}

	if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-p17-ret-1", payload); err == nil {
		t.Fatalf("P1-7 violated: HandleShipmentUpdated swallowed RecordCODRejection error and returned nil")
	}
	processed, _ := h.store.IsEventProcessed(context.Background(), "evt-p17-ret-1")
	if processed {
		t.Fatalf("P1-7 violated: event marked processed despite failed compensation")
	}

	// Recover user service -> NATS redelivery completes RecordCODRejection without duplicating order.returned outbox!
	h.user.codRejectionErr = nil
	if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-p17-ret-1", payload); err != nil {
		t.Fatalf("redelivered HandleShipmentUpdated failed: %v", err)
	}
	processed, _ = h.store.IsEventProcessed(context.Background(), "evt-p17-ret-1")
	if !processed {
		t.Fatalf("event must be marked processed after redelivery succeeded")
	}
	if h.user.codRejectedCount != 1 {
		t.Fatalf("codRejectedCount = %d, want 1 after redelivery", h.user.codRejectedCount)
	}
	returnedOutboxCount := 0
	for _, ev := range h.store.OutboxEvents() {
		if ev.EventType == events.SubjectOrderReturned {
			returnedOutboxCount++
		}
	}
	if returnedOutboxCount != 1 {
		t.Fatalf("expected exactly 1 order.returned outbox event across redelivery, got %d", returnedOutboxCount)
	}
}

// TestReviewA_P2_8_RefundAccumulation_GroupPartialAndSubOrderSyncsGroupStatus reproduces review-A P2-8.
func TestReviewA_P2_8_RefundAccumulation_GroupPartialAndSubOrderSyncsGroupStatus(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")

	t.Run("GroupLevelMultiplePartialRefunds_AccumulateToRefunded", func(t *testing.T) {
		h := newTestHarness()
		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "idem-p28-grp",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			FromCart:       true,
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil || len(resp.Orders) != 2 {
			t.Fatalf("CreateCheckout: %v", err)
		}
		_ = h.svc.HandlePaymentSucceeded(context.Background(), "evt-p28-ok", events.PaymentSucceededPayload{
			PaymentID: resp.PaymentId, OrderGroupID: resp.CheckoutGroupId,
		})

		part1 := int64(500000)
		part2 := resp.TotalPayableVnd - part1
		if err := h.svc.HandlePaymentRefunded(context.Background(), "evt-p28-g1", events.PaymentRefundedPayload{
			RefundID: "ref-g1", PaymentID: resp.PaymentId, OrderID: resp.CheckoutGroupId, AmountVND: part1, Reason: "part1",
		}); err != nil {
			t.Fatalf("part1 refund: %v", err)
		}
		grp, _ := h.store.GetCheckoutGroupByID(context.Background(), resp.CheckoutGroupId)
		if grp.PaymentStatus == "REFUNDED" {
			t.Fatalf("partial group refund must not mark group REFUNDED yet")
		}

		if err := h.svc.HandlePaymentRefunded(context.Background(), "evt-p28-g2", events.PaymentRefundedPayload{
			RefundID: "ref-g2", PaymentID: resp.PaymentId, OrderID: resp.CheckoutGroupId, AmountVND: part2, Reason: "part2",
		}); err != nil {
			t.Fatalf("part2 refund: %v", err)
		}
		grp, _ = h.store.GetCheckoutGroupByID(context.Background(), resp.CheckoutGroupId)
		if grp.PaymentStatus != "REFUNDED" {
			t.Fatalf("P2-8(a) violated: group payment_status = %q, want REFUNDED after cumulative group refunds", grp.PaymentStatus)
		}
		for _, o := range resp.Orders {
			got, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: o.Id})
			if got.Order.PaymentStatus != "REFUNDED" {
				t.Fatalf("sub-order %s payment_status = %q, want REFUNDED", o.Id, got.Order.PaymentStatus)
			}
		}
	})

	t.Run("SubOrderRefunds_SyncCheckoutGroupPaymentStatus", func(t *testing.T) {
		h := newTestHarness()
		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "idem-p28-sub",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			FromCart:       true,
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil || len(resp.Orders) != 2 {
			t.Fatalf("CreateCheckout: %v", err)
		}
		_ = h.svc.HandlePaymentSucceeded(context.Background(), "evt-p28-sub-ok", events.PaymentSucceededPayload{
			PaymentID: resp.PaymentId, OrderGroupID: resp.CheckoutGroupId,
		})

		// Refund sub-order 1 fully -> group stays SUCCEEDED because sub-order 2 is not refunded yet.
		_ = h.svc.HandlePaymentRefunded(context.Background(), "evt-p28-s1", events.PaymentRefundedPayload{
			RefundID: "ref-s1", PaymentID: resp.PaymentId, OrderID: resp.Orders[0].Id, AmountVND: resp.Orders[0].TotalPayableVnd, Reason: "s1",
		})
		grp, _ := h.store.GetCheckoutGroupByID(context.Background(), resp.CheckoutGroupId)
		if grp.PaymentStatus != "SUCCEEDED" {
			t.Fatalf("after 1 of 2 sub-orders refunded, group payment_status = %q, want SUCCEEDED", grp.PaymentStatus)
		}

		// Refund sub-order 2 fully -> ALL sub-orders refunded -> group becomes REFUNDED!
		_ = h.svc.HandlePaymentRefunded(context.Background(), "evt-p28-s2", events.PaymentRefundedPayload{
			RefundID: "ref-s2", PaymentID: resp.PaymentId, OrderID: resp.Orders[1].Id, AmountVND: resp.Orders[1].TotalPayableVnd, Reason: "s2",
		})
		grp, _ = h.store.GetCheckoutGroupByID(context.Background(), resp.CheckoutGroupId)
		if grp.PaymentStatus != "REFUNDED" {
			t.Fatalf("P2-8(b) violated: after all sub-orders refunded, group payment_status = %q, want REFUNDED", grp.PaymentStatus)
		}
	})
}

// TestReviewA_P2_9_CheckoutReplayAfterRetryReturnsNewPayment reproduces review-A P2-9.
func TestReviewA_P2_9_CheckoutReplayAfterRetryReturnsNewPayment(t *testing.T) {
	h := newTestHarness()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")

	req := &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "idem-p29-replay",
		BuyerId:        "buyer-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
	}
	resp1, err := h.svc.CreateCheckout(buyerCtx, req)
	if err != nil {
		t.Fatalf("CreateCheckout: %v", err)
	}
	retryResp, err := h.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
		PaymentId:     resp1.PaymentId,
		PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
	})
	if err != nil {
		t.Fatalf("RetryPayment: %v", err)
	}

	replay, err := h.svc.CreateCheckout(buyerCtx, req)
	if err != nil {
		t.Fatalf("CreateCheckout replay: %v", err)
	}
	if !replay.IdempotentReplay {
		t.Fatalf("expected idempotent_replay=true")
	}
	if replay.PaymentId != retryResp.PaymentId {
		t.Fatalf("P2-9 violated: checkout replay returned stale payment_id=%q, want retried %q", replay.PaymentId, retryResp.PaymentId)
	}
	if len(replay.Orders) != 1 || replay.Orders[0].PaymentId != retryResp.PaymentId {
		t.Fatalf("P2-9 violated: checkout replay order payment_id=%q, want %q", replay.Orders[0].PaymentId, retryResp.PaymentId)
	}
}

// TestReviewA_P2_10_LegacyCODPending_CancelledOnCancelAndShippingReturned reproduces review-A P2-10.
func TestReviewA_P2_10_LegacyCODPending_CancelledOnCancelAndShippingReturned(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

	t.Run("CancelLegacyCODPendingOrder", func(t *testing.T) {
		h := newTestHarness()
		_, _ = h.store.CreateCheckoutGroup(context.Background(), db.CreateCheckoutGroupParams{
			ID: "grp-legacy-cod-1", BuyerID: "buyer-1", IdempotencyKey: "idem-leg-1",
			Status: "IN_PROGRESS", PaymentMethod: "COD", TotalPayableVND: 200000,
		})
		_, err := h.store.PersistCheckoutBatch(context.Background(), store.CheckoutBatch{
			CheckoutGroupID: "grp-legacy-cod-1",
			Orders: []store.NewOrderRecord{
				{
					Order: db.InsertOrderParams{
						ID:                  "ord-legacy-cod-1",
						OrderCode:           "NM-20261120-LEG00001",
						CheckoutGroupID:     "grp-legacy-cod-1",
						BuyerID:             "buyer-1",
						ShopID:              "shop-1",
						Status:              "CONFIRMED",
						PaymentMethod:       "COD",
						PaymentID:           "pay-legacy-1",
						PaymentStatus:       "COD_PENDING",
						TotalPayableVND:     200000,
						ShippingAddressJSON: []byte("{}"),
					},
				},
			},
		})
		if err != nil {
			t.Fatalf("PersistCheckoutBatch: %v", err)
		}
		cancelResp, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{
			OrderId: "ord-legacy-cod-1", Reason: "cancel legacy COD",
		})
		if err != nil {
			t.Fatalf("CancelOrder: %v", err)
		}
		if cancelResp.Order.PaymentStatus != "CANCELLED" {
			t.Fatalf("P2-10 violated: legacy COD_PENDING order payment_status = %q after cancel, want CANCELLED", cancelResp.Order.PaymentStatus)
		}
	})

	t.Run("ShippingReturnedLegacyCODPendingOrder", func(t *testing.T) {
		h := newTestHarness()
		_, _ = h.store.CreateCheckoutGroup(context.Background(), db.CreateCheckoutGroupParams{
			ID: "grp-legacy-cod-2", BuyerID: "buyer-1", IdempotencyKey: "idem-leg-2",
			Status: "IN_PROGRESS", PaymentMethod: "COD", TotalPayableVND: 200000,
		})
		_, err := h.store.PersistCheckoutBatch(context.Background(), store.CheckoutBatch{
			CheckoutGroupID: "grp-legacy-cod-2",
			Orders: []store.NewOrderRecord{
				{
					Order: db.InsertOrderParams{
						ID:                  "ord-legacy-cod-2",
						OrderCode:           "NM-20261120-LEG00002",
						CheckoutGroupID:     "grp-legacy-cod-2",
						BuyerID:             "buyer-1",
						ShopID:              "shop-1",
						Status:              "CONFIRMED",
						PaymentMethod:       "COD",
						PaymentID:           "pay-legacy-2",
						PaymentStatus:       "COD_PENDING",
						TotalPayableVND:     200000,
						CarrierCode:         "ghn",
						ServiceCode:         "standard",
						ShippingAddressJSON: []byte("{}"),
					},
				},
			},
		})
		if err != nil {
			t.Fatalf("PersistCheckoutBatch: %v", err)
		}
		if err := h.store.CompleteCheckoutWithPayment(context.Background(), store.UpdateCheckoutPaymentParams{
			CheckoutGroupID: "grp-legacy-cod-2",
			PaymentID:       "pay-legacy-2",
			PaymentStatus:   "COD_PENDING",
			TotalPayableVND: 200000,
		}); err != nil {
			t.Fatalf("CompleteCheckoutWithPayment: %v", err)
		}
		shipResp, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{
			OrderId: "ord-legacy-cod-2", ShopId: "shop-1",
		})
		if err != nil {
			t.Fatalf("SellerShipOrder: %v", err)
		}
		if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-leg-ret", events.ShipmentUpdatedPayload{
			OrderID: "ord-legacy-cod-2", TrackingCode: shipResp.TrackingCode, Status: "returned", Description: "refused",
		}); err != nil {
			t.Fatalf("HandleShipmentUpdated(returned): %v", err)
		}
		got, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: "ord-legacy-cod-2"})
		if got.Order.PaymentStatus != "CANCELLED" {
			t.Fatalf("P2-10 violated: legacy COD_PENDING order payment_status = %q after SHIPPING->RETURNED, want CANCELLED", got.Order.PaymentStatus)
		}
	})
}

// TestReviewA_P3_12_CancelZeroPayableOrder_SetsPaymentStatusCancelled reproduces review-A P3-12.
func TestReviewA_P3_12_CancelZeroPayableOrder_SetsPaymentStatusCancelled(t *testing.T) {
	h := newTestHarness()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
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
		IdempotencyKey:    "idem-p312-zero",
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
	cancelResp, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{
		OrderId: resp.Orders[0].Id, Reason: "cancel 0 VND order",
	})
	if err != nil {
		t.Fatalf("CancelOrder 0 VND: %v", err)
	}
	if cancelResp.Order.PaymentStatus != "CANCELLED" {
		t.Fatalf("P3-12 violated: 0 VND cancelled order payment_status = %q, want CANCELLED", cancelResp.Order.PaymentStatus)
	}
	if len(h.payment.refundCalls) != 0 {
		t.Fatalf("0 VND order cancel must not call CreateRefund, got %+v", h.payment.refundCalls)
	}
	if len(h.payment.cancelCalls) != 0 {
		t.Fatalf("0 VND order cancel must not call CancelPayment (payment is already SUCCEEDED; handled via order.cancelled), got %d", len(h.payment.cancelCalls))
	}
}
