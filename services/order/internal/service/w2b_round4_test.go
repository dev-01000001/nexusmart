package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	cartv1 "github.com/nexusmart/core/gen/go/nexusmart/cart/v1"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/services/order/internal/db"
	"github.com/nexusmart/core/services/order/internal/events"
	"github.com/nexusmart/core/services/order/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// failingCompleteStore wraps a store.Store to inject deterministic failures into CompleteCheckoutWithPayment.
type failingCompleteStore struct {
	store.Store
	mu             sync.Mutex
	failRemaining  int
	failErr        error
	completeCalls  int
	lastCtxErr     error
	captureCtxErrs []error
}

func (f *failingCompleteStore) CompleteCheckoutWithPayment(ctx context.Context, arg store.UpdateCheckoutPaymentParams) error {
	f.mu.Lock()
	f.completeCalls++
	f.lastCtxErr = ctx.Err()
	f.captureCtxErrs = append(f.captureCtxErrs, ctx.Err())
	shouldFail := f.failRemaining > 0
	if shouldFail {
		f.failRemaining--
	}
	err := f.failErr
	f.mu.Unlock()
	if shouldFail {
		return err
	}
	return f.Store.CompleteCheckoutWithPayment(ctx, arg)
}

// referenceComputeSettlementSplit mirrors services/payment/src/domain.rs:compute_settlement_split
// for direct cross-verification of order report metrics against payment ledger accounting.
func referenceComputeSettlementSplit(r, cTotal, ship, alreadyRefunded int64) (sellerNet, effComm, effShip int64) {
	rawNet := r - cTotal
	sellerDeduct := minInt64(alreadyRefunded, rawNet)
	sellerNet = rawNet - sellerDeduct
	remAfterSeller := alreadyRefunded - sellerDeduct

	commDeduct := minInt64(remAfterSeller, cTotal)
	effComm = cTotal - commDeduct
	remAfterComm := remAfterSeller - commDeduct

	shipDeduct := minInt64(remAfterComm, ship)
	effShip = ship - shipDeduct
	return sellerNet, effComm, effShip
}

// TestW2B_R4_A_CompleteCheckoutRaceAndRecovery covers item A (P0-1):
//   - Race with payment.succeeded between CreatePayment and CompleteCheckoutWithPayment never downgrades payment_status
//     and response_snapshot_json reflects SUCCEEDED/CONFIRMED.
//   - Race with CancelOrder never touches cancelled orders or downgrades payment_status.
//   - Client context cancellation after CreatePayment does not break CompleteCheckoutWithPayment / ConfirmStock / ClearCart.
//   - Retry of CompleteCheckoutWithPayment + watchdog recovery + idempotent retry returning result instead of hanging 15s -> Aborted.
func TestW2B_R4_A_CompleteCheckoutRaceAndRecovery(t *testing.T) {
	t.Run("RaceWithPaymentSucceeded_NoDowngradeAndFreshSnapshot", func(t *testing.T) {
		h := newTestHarness()
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		adminCtx := ctxWithUser("admin-1", "admin", "")

		// Inject payment.succeeded right after Payment.CreatePayment returns, BEFORE CompleteCheckoutWithPayment commits.
		h.payment.afterCreatePay = func(pay *paymentv1.Payment) {
			if err := h.svc.HandlePaymentSucceeded(context.Background(), "evt-race-succ", events.PaymentSucceededPayload{
				PaymentID:    pay.GetPaymentId(),
				OrderGroupID: pay.GetOrderGroupId(),
				AmountVND:    pay.GetAmountVnd(),
			}); err != nil {
				t.Errorf("racing HandlePaymentSucceeded failed: %v", err)
			}
		}

		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "r4-p01-race-succ",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout failed: %v", err)
		}

		ordID := resp.Orders[0].Id
		got, err := h.svc.GetOrder(adminCtx, &orderv1.GetOrderRequest{OrderId: ordID})
		if err != nil {
			t.Fatalf("GetOrder: %v", err)
		}
		if got.Order.Status != orderv1.OrderStatus_ORDER_STATUS_CONFIRMED {
			t.Fatalf("order status = %v, want CONFIRMED", got.Order.Status)
		}
		if got.Order.PaymentStatus != "SUCCEEDED" {
			t.Fatalf("P0-1 regression: order payment_status downgraded to %q, want SUCCEEDED", got.Order.PaymentStatus)
		}
		grp, err := h.store.GetCheckoutGroupByID(context.Background(), resp.CheckoutGroupId)
		if err != nil {
			t.Fatalf("GetCheckoutGroupByID: %v", err)
		}
		if grp.PaymentStatus != "SUCCEEDED" || grp.Status != "COMPLETED" {
			t.Fatalf("P0-1 regression: checkout_group status=%q payment_status=%q, want COMPLETED/SUCCEEDED", grp.Status, grp.PaymentStatus)
		}

		// Idempotent replay MUST reflect SUCCEEDED/CONFIRMED from response_snapshot_json, NOT stale PENDING.
		replay, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "r4-p01-race-succ",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("idempotent replay failed: %v", err)
		}
		if !replay.IdempotentReplay {
			t.Fatalf("expected idempotent_replay=true")
		}
		if replay.PaymentStatus != "SUCCEEDED" || replay.Orders[0].PaymentStatus != "SUCCEEDED" || replay.Orders[0].Status != orderv1.OrderStatus_ORDER_STATUS_CONFIRMED {
			t.Fatalf("snapshot must reflect SUCCEEDED/CONFIRMED, got group=%q ordStatus=%v ordPay=%q",
				replay.PaymentStatus, replay.Orders[0].Status, replay.Orders[0].PaymentStatus)
		}
	})

	t.Run("RaceWithCancelOrder_CancelledOrderUntouchedAndFreshSnapshot", func(t *testing.T) {
		// Case 1 (D11 extended / P1-1): Single-order checkout cancelled mid-flight -> CompleteCheckoutWithPayment
		// closes group FAILED (no COMPLETED snapshot), CreateCheckout & retry return CHECKOUT_FAILED (CANCELLED),
		// and the cancelled order stays untouched (CANCELLED/CANCELLED).
		h := newTestHarness()
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		adminCtx := ctxWithUser("admin-1", "admin", "")
		var cancelledOrderID string

		h.payment.afterCreatePay = func(pay *paymentv1.Payment) {
			aggs, err := h.store.ListOrdersByCheckoutGroup(context.Background(), pay.GetOrderGroupId())
			if err != nil || len(aggs) == 0 {
				t.Errorf("list orders in cancel race: err=%v len=%d", err, len(aggs))
				return
			}
			cancelledOrderID = aggs[0].Order.ID
			if _, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{
				OrderId: aggs[0].Order.ID,
				Reason:  "buyer cancelled during checkout",
			}); err != nil {
				t.Errorf("racing CancelOrder failed: %v", err)
			}
		}

		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "r4-p01-race-cancel",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err == nil || resp != nil || status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "CHECKOUT_FAILED") || !strings.Contains(err.Error(), "CANCELLED") {
			t.Fatalf("expected FailedPrecondition CHECKOUT_FAILED (CANCELLED) when all orders cancelled, got resp=%+v err=%v", resp, err)
		}

		got, err := h.svc.GetOrder(adminCtx, &orderv1.GetOrderRequest{OrderId: cancelledOrderID})
		if err != nil {
			t.Fatalf("GetOrder: %v", err)
		}
		if got.Order.Status != orderv1.OrderStatus_ORDER_STATUS_CANCELLED || got.Order.PaymentStatus != "CANCELLED" {
			t.Fatalf("cancelled order was mutated: status=%v payment_status=%q, want CANCELLED/CANCELLED", got.Order.Status, got.Order.PaymentStatus)
		}

		if replay, rErr := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "r4-p01-race-cancel",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		}); rErr == nil || replay != nil || status.Code(rErr) != codes.FailedPrecondition || !strings.Contains(rErr.Error(), "CHECKOUT_FAILED") {
			t.Fatalf("retry on all-cancelled FAILED group must return CHECKOUT_FAILED, got replay=%+v err=%v", replay, rErr)
		}

		// Case 2 (partial cancel in 2-shop COD checkout): cancelling only 1 of 2 CONFIRMED orders still closes COMPLETED
		// with a fresh snapshot reflecting CANCELLED on shop-1 and CONFIRMED on shop-2.
		h2 := newTestHarness()
		var cancelledShop1ID string
		h2.payment.afterCreatePay = func(pay *paymentv1.Payment) {
			aggs, err := h2.store.ListOrdersByCheckoutGroup(context.Background(), pay.GetOrderGroupId())
			if err != nil || len(aggs) != 2 {
				t.Errorf("list 2-shop orders in partial cancel race: err=%v len=%d", err, len(aggs))
				return
			}
			for _, a := range aggs {
				if a.Order.ShopID == "shop-1" {
					cancelledShop1ID = a.Order.ID
					if _, err := h2.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{
						OrderId: a.Order.ID,
						Reason:  "cancel only shop-1 during checkout",
					}); err != nil {
						t.Errorf("racing partial CancelOrder failed: %v", err)
					}
				}
			}
		}
		partialResp, err := h2.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "r4-p01-race-partial-cancel",
			AddressId:      "addr-buyer-1",
			Items: []*orderv1.CheckoutItemInput{
				{SkuId: "sku-1", Quantity: 1},
				{SkuId: "sku-2", Quantity: 1},
			},
			PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil || len(partialResp.Orders) != 2 {
			t.Fatalf("partial cancel CreateCheckout failed: resp=%+v err=%v", partialResp, err)
		}
		partialReplay, err := h2.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "r4-p01-race-partial-cancel",
			AddressId:      "addr-buyer-1",
			Items: []*orderv1.CheckoutItemInput{
				{SkuId: "sku-1", Quantity: 1},
				{SkuId: "sku-2", Quantity: 1},
			},
			PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil || !partialReplay.IdempotentReplay || len(partialReplay.Orders) != 2 {
			t.Fatalf("partial cancel replay failed: resp=%+v err=%v", partialReplay, err)
		}
		var sawCancelled, sawConfirmed bool
		for _, o := range partialReplay.Orders {
			if o.Id == cancelledShop1ID {
				if o.Status != orderv1.OrderStatus_ORDER_STATUS_CANCELLED || o.PaymentStatus != "CANCELLED" {
					t.Fatalf("partial replay cancelled order mismatch: status=%v pay=%q", o.Status, o.PaymentStatus)
				}
				sawCancelled = true
			} else if o.Status == orderv1.OrderStatus_ORDER_STATUS_CONFIRMED {
				sawConfirmed = true
			}
		}
		if !sawCancelled || !sawConfirmed {
			t.Fatalf("partial replay expected 1 CANCELLED + 1 CONFIRMED, got %+v", partialReplay.Orders)
		}
	})

	t.Run("ClientDisconnectAfterCreatePayment_DetachedFinalizeAndClearCart", func(t *testing.T) {
		h := newTestHarness()
		wrapped := &failingCompleteStore{Store: h.store}
		h.svc.store = wrapped

		reqCtx, cancelClient := context.WithCancel(ctxWithUser("buyer-1", "buyer", ""))
		defer cancelClient()

		// Cancel client context immediately after CreatePayment succeeds.
		h.payment.afterCreatePay = func(_ *paymentv1.Payment) {
			cancelClient()
		}
		h.cart.cart = &cartv1.Cart{
			UserId: "buyer-1",
			ShopGroups: []*cartv1.CartShopGroup{{
				ShopId: "shop-1",
				Items:  []*cartv1.CartItem{{SkuId: "sku-1", Quantity: 1, Selected: true}},
			}},
		}
		var cartCtxErr error
		h.cart.onClearItems = func(ctx context.Context) error {
			cartCtxErr = ctx.Err()
			return ctx.Err()
		}

		resp, err := h.svc.CreateCheckout(reqCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "r4-p01-disconnect",
			AddressId:      "addr-buyer-1",
			FromCart:       true,
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil {
			t.Fatalf("CreateCheckout failed: %v", err)
		}
		if wrapped.lastCtxErr != nil {
			t.Fatalf("CompleteCheckoutWithPayment received canceled client ctx: %v", wrapped.lastCtxErr)
		}
		if len(h.cart.clearCalls) != 1 {
			t.Fatalf("expected Cart.ClearItems to be called once for FromCart=true checkout, got %d calls", len(h.cart.clearCalls))
		}
		if cartCtxErr != nil {
			t.Fatalf("Cart.ClearItems received canceled client ctx: %v", cartCtxErr)
		}
		if len(h.product.confirmCalls) != 1 {
			t.Fatalf("expected ConfirmStock to run with detached ctx, got %d calls", len(h.product.confirmCalls))
		}
		grp, err := h.store.GetCheckoutGroupByID(context.Background(), resp.CheckoutGroupId)
		if err != nil || grp.Status != "COMPLETED" || grp.PaymentID == "" {
			t.Fatalf("expected COMPLETED group with payment_id, got err=%v grp=%+v", err, grp)
		}
	})

	t.Run("CompleteCheckoutFailsAllRetries_IdempotentRetryRecoversImmediatelyWithoutHanging", func(t *testing.T) {
		h := newTestHarness()
		var createdPay1 *paymentv1.Payment
		h.payment.afterCreatePay = func(p *paymentv1.Payment) { createdPay1 = p }
		wrapped := &failingCompleteStore{
			Store:         h.store,
			failRemaining: 5, // fail initial call + all retries
			failErr:       errors.New("transient db timeout"),
		}
		h.svc.store = wrapped
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")

		resp1, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "r4-p01-retry-recover",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err == nil || resp1 != nil || status.Code(err) != codes.Unavailable {
			t.Fatalf("P1-1: expected Unavailable (not 200 OK) when CompleteCheckoutWithPayment fails all retries, got resp=%+v err=%v", resp1, err)
		}
		if len(h.cart.clearCalls) != 0 {
			t.Fatalf("P1-1: Cart.ClearItems must not run when finalization failed, got %d calls", len(h.cart.clearCalls))
		}
		if wrapped.completeCalls < 2 {
			t.Fatalf("expected CompleteCheckoutWithPayment to retry on transient error, got %d calls", wrapped.completeCalls)
		}

		lookup1, lErr := h.store.GetCheckoutGroupForIdempotency(context.Background(), "buyer-1", "r4-p01-retry-recover")
		if lErr != nil || !lookup1.HasOrders {
			t.Fatalf("expected persisted IN_PROGRESS group in store, got lookup=%+v err=%v", lookup1, lErr)
		}
		groupID1 := lookup1.Group.ID
		payID1 := createdPay1.GetPaymentId()

		// Group is still IN_PROGRESS with persisted orders and payment_id='' in DB, while PaymentService has the payment.
		// Backdate past grace period (N7) so retry triggers immediate forward recovery instead of waiting 15s -> Aborted.
		h.store.SetCheckoutGroupUpdatedAt(groupID1, time.Now().UTC().Add(-5*time.Second))
		wrapped.mu.Lock()
		wrapped.failRemaining = 0
		wrapped.mu.Unlock()

		// Retry with same Idempotency-Key MUST recover from Payment.GetPayment(OrderGroupId) fast (NOT hang 15s -> Aborted!).
		retryCtx, cancel := context.WithTimeout(buyerCtx, 2*time.Second)
		defer cancel()
		resp2, err := h.svc.CreateCheckout(retryCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "r4-p01-retry-recover",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("idempotent retry should self-recover from PaymentService instead of hanging/Aborting: %v", err)
		}
		if resp2.CheckoutGroupId != groupID1 || resp2.PaymentId != payID1 || !resp2.IdempotentReplay {
			t.Fatalf("unexpected recovered replay: got group=%s pay=%s replay=%v want %s/%s/true",
				resp2.CheckoutGroupId, resp2.PaymentId, resp2.IdempotentReplay, groupID1, payID1)
		}
	})

	t.Run("WatchdogScanIncompleteCheckoutGroups_RecoversWithPaymentAndCancelsOrphanWithoutPayment", func(t *testing.T) {
		h := newTestHarness()
		h.svc.cfg.StaleCheckoutAfter = 1 * time.Millisecond
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		adminCtx := ctxWithUser("admin-1", "admin", "")

		// Case 1: Payment exists in PaymentService (and already SUCCEEDED), but CompleteCheckoutWithPayment failed.
		var wdPay *paymentv1.Payment
		h.payment.afterCreatePay = func(p *paymentv1.Payment) { wdPay = p }
		wrapped := &failingCompleteStore{Store: h.store, failRemaining: 10, failErr: errors.New("db down")}
		h.svc.store = wrapped

		resp1, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "r4-p01-watchdog-found",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err == nil || resp1 != nil || status.Code(err) != codes.Unavailable {
			t.Fatalf("P1-1: CreateCheckout 1 expected Unavailable when finalization fails, got resp=%+v err=%v", resp1, err)
		}
		lookupWD, _ := h.store.GetCheckoutGroupForIdempotency(context.Background(), "buyer-1", "r4-p01-watchdog-found")
		wdGroupID := lookupWD.Group.ID
		wdAggs, _ := h.store.ListOrdersByCheckoutGroup(context.Background(), wdGroupID)
		wdOrderID := wdAggs[0].Order.ID
		h.payment.onGetPayment = func(req *paymentv1.GetPaymentRequest) (*paymentv1.GetPaymentResponse, error) {
			if req.GetOrderGroupId() == wdGroupID {
				return &paymentv1.GetPaymentResponse{
					Payment: &paymentv1.Payment{
						PaymentId:    wdPay.GetPaymentId(),
						OrderGroupId: wdGroupID,
						Status:       paymentv1.PaymentStatus_PAYMENT_STATUS_SUCCEEDED,
						AmountVnd:    wdPay.GetAmountVnd(),
					},
				}, nil
			}
			return nil, status.Error(codes.NotFound, "payment not found")
		}

		// Case 2: Orphan IN_PROGRESS group with persisted order where PaymentService returns NotFound (crashed before CreatePayment).
		orphanGroupID := "grp-orphan-nopay"
		orphanOrderID := "ord-orphan-nopay"
		_, _ = h.store.CreateCheckoutGroup(context.Background(), db.CreateCheckoutGroupParams{
			ID: orphanGroupID, BuyerID: "buyer-1", IdempotencyKey: "r4-p01-watchdog-orphan",
			RequestHash: "h-orphan", Status: "IN_PROGRESS", PaymentMethod: "VIETQR", TotalPayableVND: 1030000,
		})
		_, _ = h.store.PersistCheckoutBatch(context.Background(), store.CheckoutBatch{
			CheckoutGroupID: orphanGroupID,
			Now:             time.Now().UTC().Add(-10 * time.Minute),
			Orders: []store.NewOrderRecord{{
				Order: db.InsertOrderParams{
					ID: orphanOrderID, OrderCode: "NM-ORPHAN-1", CheckoutGroupID: orphanGroupID,
					BuyerID: "buyer-1", ShopID: "shop-1", Status: "PENDING",
					PaymentMethod: "VIETQR", PaymentStatus: "PENDING",
					SubtotalVND: 1000000, ShippingFeeVND: 30000, TotalPayableVND: 1030000,
					ShippingAddressJSON: []byte(`{}`),
				},
			}},
		})

		// Restore DB and let stale window pass.
		wrapped.mu.Lock()
		wrapped.failRemaining = 0
		wrapped.mu.Unlock()
		time.Sleep(5 * time.Millisecond)

		handled, err := h.svc.ScanIncompleteCheckoutGroups(context.Background())
		if err != nil || handled != 2 {
			t.Fatalf("ScanIncompleteCheckoutGroups = %d, %v; want 2, nil", handled, err)
		}

		// Case 1 recovered: group COMPLETED/SUCCEEDED and order CONFIRMED/SUCCEEDED with payment_id attached.
		g1, _ := h.store.GetCheckoutGroupByID(context.Background(), wdGroupID)
		o1, _ := h.svc.GetOrder(adminCtx, &orderv1.GetOrderRequest{OrderId: wdOrderID})
		if g1.Status != "COMPLETED" || g1.PaymentStatus != "SUCCEEDED" || o1.Order.Status != orderv1.OrderStatus_ORDER_STATUS_CONFIRMED || o1.Order.PaymentId != wdPay.GetPaymentId() {
			t.Fatalf("case 1 recovery mismatch: grp=%s/%s ord=%v payID=%q", g1.Status, g1.PaymentStatus, o1.Order.Status, o1.Order.PaymentId)
		}

		// Case 2 orphan cancelled + group FAILED + stock & promotion released.
		g2, _ := h.store.GetCheckoutGroupByID(context.Background(), orphanGroupID)
		o2, _ := h.svc.GetOrder(adminCtx, &orderv1.GetOrderRequest{OrderId: orphanOrderID})
		if g2.Status != "FAILED" || o2.Order.Status != orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
			t.Fatalf("case 2 orphan recovery mismatch: grp=%s ord=%v", g2.Status, o2.Order.Status)
		}
		if len(h.product.releaseCalls) != 1 || len(h.promo.releaseCalls) != 1 {
			t.Fatalf("case 2 must release stock & promo for orphan group, got stock=%d promo=%d",
				len(h.product.releaseCalls), len(h.promo.releaseCalls))
		}
	})
}

// TestW2B_R4_B_ProcessExpiredGroupCancelPaymentSafety covers item B (P0-2):
//   - Webhook beats watchdog: GetPayment#1=PENDING -> CancelPayment=AlreadyExists -> GetPayment#2=SUCCEEDED -> order CONFIRMED (not EXPIRED).
//   - Transient CancelPayment error -> retry next tick (order stays PENDING, no premature release).
//   - Empty cand.PaymentID -> uses pay.GetPaymentId() from GetPayment lookup.
func TestW2B_R4_B_ProcessExpiredGroupCancelPaymentSafety(t *testing.T) {
	t.Run("WebhookWinsOverWatchdog_OrderConfirmedNotCancelled", func(t *testing.T) {
		h := newTestHarness()
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		adminCtx := ctxWithUser("admin-1", "admin", "")

		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "r4-p02-webhook-wins",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		ordID := resp.Orders[0].Id
		h.store.SetOrderPaymentExpiresAt(ordID, time.Now().UTC().Add(-time.Minute))

		var getCalls atomic.Int32
		h.payment.onGetPayment = func(req *paymentv1.GetPaymentRequest) (*paymentv1.GetPaymentResponse, error) {
			n := getCalls.Add(1)
			st := paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING
			if n >= 2 {
				st = paymentv1.PaymentStatus_PAYMENT_STATUS_SUCCEEDED
			}
			return &paymentv1.GetPaymentResponse{
				Payment: &paymentv1.Payment{
					PaymentId:    resp.PaymentId,
					OrderGroupId: resp.CheckoutGroupId,
					Status:       st,
				},
			}, nil
		}
		h.payment.onCancelPayment = func(_ *paymentv1.CancelPaymentRequest) (*paymentv1.CancelPaymentResponse, error) {
			return nil, status.Error(codes.AlreadyExists, "cannot cancel payment in status 'succeeded'")
		}

		handled, err := h.svc.ScanExpiredPendingOrders(context.Background())
		if err != nil || handled != 1 {
			t.Fatalf("ScanExpiredPendingOrders = %d, %v; want 1, nil", handled, err)
		}
		got, err := h.svc.GetOrder(adminCtx, &orderv1.GetOrderRequest{OrderId: ordID})
		if err != nil {
			t.Fatalf("GetOrder: %v", err)
		}
		if got.Order.Status != orderv1.OrderStatus_ORDER_STATUS_CONFIRMED || got.Order.PaymentStatus != "SUCCEEDED" {
			t.Fatalf("P0-2 regression: paid order was expired! status=%v payment_status=%q, want CONFIRMED/SUCCEEDED",
				got.Order.Status, got.Order.PaymentStatus)
		}
		if len(h.product.releaseCalls) != 0 {
			t.Fatalf("must NOT release stock when payment succeeded, got %d release calls", len(h.product.releaseCalls))
		}
		if len(h.product.confirmCalls) != 1 {
			t.Fatalf("expected ConfirmStock called once, got %d", len(h.product.confirmCalls))
		}
	})

	t.Run("TransientCancelPaymentError_RetriesNextTickWithoutExpiring", func(t *testing.T) {
		h := newTestHarness()
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		adminCtx := ctxWithUser("admin-1", "admin", "")

		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "r4-p02-transient",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		ordID := resp.Orders[0].Id
		h.store.SetOrderPaymentExpiresAt(ordID, time.Now().UTC().Add(-time.Minute))
		h.payment.cancelErr = status.Error(codes.Unavailable, "payment db temporarily down")

		handled, err := h.svc.ScanExpiredPendingOrders(context.Background())
		if err != nil {
			t.Fatalf("ScanExpiredPendingOrders error: %v", err)
		}
		if handled != 0 {
			t.Fatalf("expected handled=0 when CancelPayment fails transiently, got %d", handled)
		}
		got, _ := h.svc.GetOrder(adminCtx, &orderv1.GetOrderRequest{OrderId: ordID})
		if got.Order.Status != orderv1.OrderStatus_ORDER_STATUS_PENDING {
			t.Fatalf("order must remain PENDING on transient CancelPayment error, got %v", got.Order.Status)
		}
		if len(h.product.releaseCalls) != 0 {
			t.Fatalf("must NOT release stock on transient CancelPayment error, got %d calls", len(h.product.releaseCalls))
		}
	})

	t.Run("EmptyCandidatePaymentID_UsesRecoveredPaymentID", func(t *testing.T) {
		h := newTestHarness()
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		adminCtx := ctxWithUser("admin-1", "admin", "")

		// Simulate crash before CompleteCheckoutWithPayment: payment_id is '' on order, but PaymentService has pay-recovered.
		wrapped := &failingCompleteStore{Store: h.store, failRemaining: 10, failErr: errors.New("db down")}
		h.svc.store = wrapped

		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "r4-p02-empty-payid",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err == nil || resp != nil || status.Code(err) != codes.Unavailable {
			t.Fatalf("P1-1: CreateCheckout expected Unavailable when finalization fails, got resp=%+v err=%v", resp, err)
		}
		lookup, _ := h.store.GetCheckoutGroupForIdempotency(context.Background(), "buyer-1", "r4-p02-empty-payid")
		aggs, _ := h.store.ListOrdersByCheckoutGroup(context.Background(), lookup.Group.ID)
		ordID := aggs[0].Order.ID
		h.store.SetOrderPaymentExpiresAt(ordID, time.Now().UTC().Add(-time.Minute))

		h.payment.onGetPayment = func(req *paymentv1.GetPaymentRequest) (*paymentv1.GetPaymentResponse, error) {
			return &paymentv1.GetPaymentResponse{
				Payment: &paymentv1.Payment{
					PaymentId:    "pay-recovered-99",
					OrderGroupId: req.GetOrderGroupId(),
					Status:       paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING,
				},
			}, nil
		}

		handled, err := h.svc.ScanExpiredPendingOrders(context.Background())
		if err != nil || handled != 1 {
			t.Fatalf("ScanExpiredPendingOrders = %d, %v; want 1, nil", handled, err)
		}
		got, _ := h.svc.GetOrder(adminCtx, &orderv1.GetOrderRequest{OrderId: ordID})
		if got.Order.PaymentId != "pay-recovered-99" {
			t.Fatalf("expected recovered payment_id 'pay-recovered-99' on expired order, got %q", got.Order.PaymentId)
		}
	})
}

// TestW2B_R4_C_AmbiguousReserveErrorsAndWatchdogCleanup covers item C (P1 manager):
//   - Ambiguous ReserveStock error triggers compensation (ReleaseStock + ReleasePromotion).
//   - Ambiguous ReservePromotion error triggers compensation (ReleaseStock + ReleasePromotion).
//   - If compensation fails, checkout_group stays IN_PROGRESS (NOT FAILED) so ScanStaleCheckoutGroups retries cleanup and marks FAILED once done.
func TestW2B_R4_C_AmbiguousReserveErrorsAndWatchdogCleanup(t *testing.T) {
	t.Run("AmbiguousReserveStockError_CompensatesStockOnly", func(t *testing.T) {
		h := newTestHarness()
		h.product.reserveErr = status.Error(codes.DeadlineExceeded, "context deadline exceeded")

		_, err := h.svc.CreateCheckout(ctxWithUser("buyer-1", "buyer", ""), &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "r4-c-stock-timeout",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err == nil {
			t.Fatalf("expected checkout failure on ReserveStock timeout")
		}
		if len(h.product.releaseCalls) != 1 {
			t.Fatalf("expected ReleaseStock compensation on ambiguous ReserveStock timeout, got %d calls", len(h.product.releaseCalls))
		}
		if len(h.promo.releaseCalls) != 0 {
			t.Fatalf("N12: must NOT call ReleasePromotion when ReserveStock failed before ReservePromotion was ever called, got %d calls", len(h.promo.releaseCalls))
		}
	})

	t.Run("AmbiguousReservePromotionError_CompensatesBothStockAndPromo", func(t *testing.T) {
		h := newTestHarness()
		h.promo.reserveErr = status.Error(codes.DeadlineExceeded, "promotion rpc timed out")

		_, err := h.svc.CreateCheckout(ctxWithUser("buyer-1", "buyer", ""), &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "r4-c-promo-timeout",
			AddressId:         "addr-buyer-1",
			Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PlatformVoucherId: "v-plat",
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err == nil {
			t.Fatalf("expected checkout failure on ReservePromotion timeout")
		}
		if len(h.product.releaseCalls) != 1 {
			t.Fatalf("expected ReleaseStock called once, got %d", len(h.product.releaseCalls))
		}
		if len(h.promo.releaseCalls) != 1 {
			t.Fatalf("expected ReleasePromotion called once on ambiguous ReservePromotion error, got %d", len(h.promo.releaseCalls))
		}
	})

	t.Run("FailedCompensation_LeavesGroupForWatchdogInsteadOfMarkingFailed", func(t *testing.T) {
		h := newTestHarness()
		h.promo.reserveErr = status.Error(codes.Unavailable, "promotion unavailable")
		h.product.releaseErr = status.Error(codes.Unavailable, "product still unavailable during compensate")

		_, err := h.svc.CreateCheckout(ctxWithUser("buyer-1", "buyer", ""), &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "r4-c-comp-fails",
			AddressId:         "addr-buyer-1",
			Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PlatformVoucherId: "v-plat",
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err == nil {
			t.Fatalf("expected checkout failure")
		}

		// Group MUST still be active (IN_PROGRESS, not FAILED) so ScanStaleCheckoutGroups can clean it up!
		lookup, gErr := h.store.GetCheckoutGroupForIdempotency(context.Background(), "buyer-1", "r4-c-comp-fails")
		if gErr != nil {
			t.Fatalf("group must remain IN_PROGRESS when compensation failed so watchdog can recover it, got err=%v", gErr)
		}
		grp := &lookup.Group
		if grp.Status != "IN_PROGRESS" {
			t.Fatalf("group status = %q, want IN_PROGRESS", grp.Status)
		}

		// Product recovers -> watchdog ScanStaleCheckoutGroups immediately picks it up and marks FAILED.
		h.product.releaseErr = nil
		h.product.releaseCalls = nil
		h.promo.releaseCalls = nil

		cleaned, wErr := h.svc.ScanStaleCheckoutGroups(context.Background())
		if wErr != nil || cleaned != 1 {
			t.Fatalf("ScanStaleCheckoutGroups = %d, %v; want 1, nil", cleaned, wErr)
		}
		if len(h.product.releaseCalls) != 1 || len(h.promo.releaseCalls) != 1 {
			t.Fatalf("watchdog must release both stock and promo, got stock=%d promo=%d",
				len(h.product.releaseCalls), len(h.promo.releaseCalls))
		}
		afterGrp, _ := h.store.GetCheckoutGroupByID(context.Background(), grp.ID)
		if afterGrp.Status != "FAILED" {
			t.Fatalf("group status after watchdog cleanup = %q, want FAILED", afterGrp.Status)
		}
	})
}

// TestW2B_R4_D_ReportSettlementWaterfallMatchesLedger covers item D (P1-1):
// Compares report seller net, commission, and payment_fee directly against payment's compute_settlement_split
// across all 4 refund waterfall regimes.
func TestW2B_R4_D_ReportSettlementWaterfallMatchesLedger(t *testing.T) {
	// Base order: G=R=100,000, Ship=30,000, D=0, P=130,000, C_cat=5,000 (5%), C_pay=2,600 (2% of 130k)
	// => C_total = 7,600, raw_net = 92,400.
	cases := []struct {
		name             string
		preRefundVND     int64
		postRefundVND    int64
		wantNetVND       int64
		wantCommVND      int64
		wantPayFeeVND    int64
		wantFeeTotalSame bool
	}{
		{
			name:             "pre_refund_below_net",
			preRefundVND:     40000,
			postRefundVND:    0,
			wantNetVND:       52400, // 92,400 - 40,000
			wantCommVND:      5000,
			wantPayFeeVND:    2600,
			wantFeeTotalSame: true,
		},
		{
			name:             "pre_refund_eats_into_fees",
			preRefundVND:     96000, // excess over raw_net = 3,600 -> payment_fee 2,600 wiped, commission reduced by 1,000 to 4,000
			postRefundVND:    0,
			wantNetVND:       0,
			wantCommVND:      4000,
			wantPayFeeVND:    0,
			wantFeeTotalSame: true,
		},
		{
			name:             "pre_refund_touches_shipping",
			preRefundVND:     110000, // exceeds raw_net + C_total (100,000) -> seller_net=0, fees=0 (remaining 10k hits shipping)
			postRefundVND:    0,
			wantNetVND:       0,
			wantCommVND:      0,
			wantPayFeeVND:    0,
			wantFeeTotalSame: true,
		},
		{
			name:             "post_settlement_refund_deducted_from_seller_only",
			preRefundVND:     40000,
			postRefundVND:    30000, // total refunded = 70,000; fees stay 5,000 + 2,600
			wantNetVND:       22400, // max(0, 92,400 - 40,000) - 30,000
			wantCommVND:      5000,
			wantPayFeeVND:    2600,
			wantFeeTotalSame: true,
		},
		{
			name:             "post_settlement_full_refund_negative_seller_net_keeps_fees",
			preRefundVND:     0,
			postRefundVND:    100000,
			wantNetVND:       -7600, // 92,400 - 100,000
			wantCommVND:      5000,
			wantPayFeeVND:    2600,
			wantFeeTotalSame: true,
		},
	}

	day := time.Date(2026, 11, 23, 5, 0, 0, 0, time.UTC) // 12:00 VN
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHarness()
			h.svc.nowFn = func() time.Time { return day }
			h.product.skus["sku-1"].PriceVnd = 100000
			h.product.skus["sku-1"].CommissionRateBps = 500 // 5,000
			h.payment.feeBPSByMethod = map[paymentv1.PaymentMethod]int32{
				paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR: 200, // 2% of 130,000 = 2,600
			}

			buyerCtx := ctxWithUser("buyer-1", "buyer", "")
			sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
			adminCtx := ctxWithUser("admin-1", "admin", "")

			co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
				IdempotencyKey: "r4-d-" + tc.name,
				AddressId:      "addr-buyer-1",
				Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
				PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
			})
			if err != nil {
				t.Fatalf("CreateCheckout: %v", err)
			}
			ordID := co.Orders[0].Id
			if err := h.svc.HandlePaymentSucceeded(context.Background(), "pay-"+tc.name, events.PaymentSucceededPayload{
				PaymentID: co.PaymentId, OrderGroupID: co.CheckoutGroupId, AmountVND: co.TotalPayableVnd,
			}); err != nil {
				t.Fatalf("HandlePaymentSucceeded: %v", err)
			}
			if err := h.svc.HandleShipmentUpdated(context.Background(), "del-"+tc.name, events.ShipmentUpdatedPayload{
				OrderID: ordID, Status: "delivered",
			}); err != nil {
				t.Fatalf("deliver: %v", err)
			}

			// Pre-settlement partial refund BEFORE BuyerConfirmReceived (COMPLETED).
			if tc.preRefundVND > 0 {
				if err := h.svc.HandlePaymentRefunded(context.Background(), "pre-ref-"+tc.name, events.PaymentRefundedPayload{
					RefundID: "ref-pre-" + tc.name, PaymentID: co.PaymentId, OrderID: ordID, AmountVND: tc.preRefundVND,
				}); err != nil {
					t.Fatalf("pre-settlement HandlePaymentRefunded: %v", err)
				}
			}

			// Complete order -> snapshots settlement_refunded_vnd = preRefundVND.
			if _, err := h.svc.BuyerConfirmReceived(buyerCtx, &orderv1.BuyerConfirmReceivedRequest{OrderId: ordID}); err != nil {
				t.Fatalf("BuyerConfirmReceived: %v", err)
			}

			// Post-settlement partial refund AFTER COMPLETED.
			if tc.postRefundVND > 0 {
				if err := h.svc.HandlePaymentRefunded(context.Background(), "post-ref-"+tc.name, events.PaymentRefundedPayload{
					RefundID: "ref-post-" + tc.name, PaymentID: co.PaymentId, OrderID: ordID, AmountVND: tc.postRefundVND,
				}); err != nil {
					t.Fatalf("post-settlement HandlePaymentRefunded: %v", err)
				}
			}

			// Cross-check directly with Rust compute_settlement_split reference formula.
			refSellerAtSettle, refEffComm, _ := referenceComputeSettlementSplit(100000, 7600, 30000, tc.preRefundVND)
			if wantRefNet := refSellerAtSettle - tc.postRefundVND; wantRefNet != tc.wantNetVND {
				t.Fatalf("test table mismatch against compute_settlement_split: ref=%d table=%d", wantRefNet, tc.wantNetVND)
			}
			if tc.wantCommVND+tc.wantPayFeeVND != refEffComm {
				t.Fatalf("test table fee split %d+%d != compute_settlement_split effComm %d",
					tc.wantCommVND, tc.wantPayFeeVND, refEffComm)
			}

			sRep, err := h.svc.GetSellerReport(sellerCtx, &orderv1.GetSellerReportRequest{
				FromDate: "2026-11-23", ToDate: "2026-11-23",
			})
			if err != nil {
				t.Fatalf("GetSellerReport: %v", err)
			}
			if sRep.NetRevenueVnd != tc.wantNetVND || sRep.CommissionVnd != tc.wantCommVND || sRep.PaymentFeeVnd != tc.wantPayFeeVND {
				t.Fatalf("seller report mismatch: got net=%d comm=%d payFee=%d, want %d / %d / %d",
					sRep.NetRevenueVnd, sRep.CommissionVnd, sRep.PaymentFeeVnd, tc.wantNetVND, tc.wantCommVND, tc.wantPayFeeVND)
			}
			pRep, err := h.svc.GetPlatformReport(adminCtx, &orderv1.GetPlatformReportRequest{
				FromDate: "2026-11-23", ToDate: "2026-11-23",
			})
			if err != nil {
				t.Fatalf("GetPlatformReport: %v", err)
			}
			if pRep.NetRevenueVnd != tc.wantNetVND || pRep.CommissionVnd != tc.wantCommVND || pRep.PaymentFeeVnd != tc.wantPayFeeVND {
				t.Fatalf("platform report mismatch: got net=%d comm=%d payFee=%d, want %d / %d / %d",
					pRep.NetRevenueVnd, pRep.CommissionVnd, pRep.PaymentFeeVnd, tc.wantNetVND, tc.wantCommVND, tc.wantPayFeeVND)
			}
		})
	}
}

// TestW2B_R4_E_GMVDefinitionAndDailySubsidy covers item E (P2-3) & H (DailyReportPoint.platform_subsidy_vnd):
//   - gmv_vnd = SUM(P + D) for orders NOT in (PENDING, CANCELLED, RETURNED).
//   - 0-VND fully subsidized COMPLETED order contributes D (= 230,000) to GMV!
//   - PENDING, CANCELLED, and RETURNED orders contribute 0 to GMV!
//   - DailyReportPoint.platform_subsidy_vnd populated on platform report, always 0 on seller report.
func TestW2B_R4_E_GMVDefinitionAndDailySubsidy(t *testing.T) {
	h := newTestHarness()
	day := time.Date(2026, 11, 23, 5, 0, 0, 0, time.UTC) // 12:00 VN
	h.svc.nowFn = func() time.Time { return day }
	h.product.skus["sku-1"].PriceVnd = 200000

	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
	adminCtx := ctxWithUser("admin-1", "admin", "")

	// Order 1: PENDING VietQR (200k + 30k = 230k) -> excluded from GMV!
	_, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "r4-e-pending",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
	})
	if err != nil {
		t.Fatalf("order 1: %v", err)
	}

	// Order 2: 0-VND fully subsidized COMPLETED (G=R=200k, Ship=30k, V_plat=150k, S_free=30k, X=50k -> D=230k, P=0) -> GMV += 230,000!
	h.promo.breakdown = &promotionv1.DiscountBreakdown{
		Shops: []*promotionv1.ShopDiscountBreakdown{
			{ShopId: "shop-1", SubtotalVnd: 200000, PlatformVoucherDiscountVnd: 150000, FreeshipDiscountVnd: 30000, CoinsDiscountVnd: 50000, ShippingFeeVnd: 30000},
		},
	}
	co2, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey:    "r4-e-zero-vnd",
		AddressId:         "addr-buyer-1",
		Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PlatformVoucherId: "v-p",
		FreeshipVoucherId: "v-f",
		CoinsToUse:        50000,
		PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
	})
	if err != nil {
		t.Fatalf("order 2: %v", err)
	}
	_ = h.svc.HandleShipmentUpdated(context.Background(), "del-e2", events.ShipmentUpdatedPayload{OrderID: co2.Orders[0].Id, Status: "delivered"})
	_, _ = h.svc.BuyerConfirmReceived(buyerCtx, &orderv1.BuyerConfirmReceivedRequest{OrderId: co2.Orders[0].Id})

	// Order 3: RETURNED COD (200k + 30k = 230k) -> excluded from GMV!
	h.promo.breakdown = nil
	co3, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "r4-e-returned",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	if err != nil {
		t.Fatalf("order 3: %v", err)
	}
	_ = h.svc.HandleShipmentUpdated(context.Background(), "ret-e3", events.ShipmentUpdatedPayload{OrderID: co3.Orders[0].Id, Status: "returned"})

	// Order 4: CANCELLED COD -> excluded from GMV!
	co4, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "r4-e-cancelled",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	if err != nil {
		t.Fatalf("order 4: %v", err)
	}
	_, _ = h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: co4.Orders[0].Id, Reason: "cancel"})

	sRep, err := h.svc.GetSellerReport(sellerCtx, &orderv1.GetSellerReportRequest{FromDate: "2026-11-23", ToDate: "2026-11-23"})
	if err != nil {
		t.Fatalf("GetSellerReport: %v", err)
	}
	if sRep.GmvVnd != 230000 {
		t.Fatalf("seller gmv_vnd = %d, want 230000 (only Order 2 P+D=230k; PENDING/CANCELLED/RETURNED excluded)", sRep.GmvVnd)
	}
	if sRep.Daily[0].PlatformSubsidyVnd != 0 {
		t.Fatalf("seller daily platform_subsidy_vnd = %d, want 0", sRep.Daily[0].PlatformSubsidyVnd)
	}

	pRep, err := h.svc.GetPlatformReport(adminCtx, &orderv1.GetPlatformReportRequest{FromDate: "2026-11-23", ToDate: "2026-11-23"})
	if err != nil {
		t.Fatalf("GetPlatformReport: %v", err)
	}
	if pRep.GmvVnd != 230000 {
		t.Fatalf("platform gmv_vnd = %d, want 230000", pRep.GmvVnd)
	}
	if pRep.PlatformSubsidyVnd != 230000 || pRep.Daily[0].PlatformSubsidyVnd != 230000 {
		t.Fatalf("platform subsidy summary=%d daily=%d, want 230000/230000", pRep.PlatformSubsidyVnd, pRep.Daily[0].PlatformSubsidyVnd)
	}
}

// TestW2B_R4_FGH_WarningsBatchingInputsAndP3 covers items F, G, H:
//   - F: CreateCheckoutResponse.Warnings forwarded from breakdown.Warnings and preserved on idempotent replay.
//   - G (P2-2): ListActiveShopDiscounts splits >200 SKUs into batches of 200.
//   - G (P2-4): Duplicate sku_id with conflicting flash_sale_item_id -> InvalidArgument; TrimSpace on flashOverrides.
//   - H: order.completed includes payment_fee_vnd; ShopProgramItemID ignored in request hash;
//     dead proportional fallback removed; ComputeOrderRewardCoins excludes S_shop penalty;
//     aggregateToProtoForCaller masks commission/payment_fee for buyers while showing real values to admin, owning seller, and service tokens.
func TestW2B_R4_FGH_WarningsBatchingInputsAndP3(t *testing.T) {
	baseNow := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)

	t.Run("F_WarningsForwardedAndReplayed", func(t *testing.T) {
		h := newTestHarness()
		h.svc.nowFn = func() time.Time { return baseNow }
		h.promo.activeShopDiscounts = []*promotionv1.ActiveShopDiscount{
			{
				ItemId: "sp-lim", ProgramId: "p-1", ShopId: "shop-1", SkuId: "sku-1",
				OriginalPriceVnd: 1000000, DiscountedPriceVnd: 800000, PerUserLimit: 1,
				StartsAt: timestamppb.New(baseNow.Add(-time.Hour)), EndsAt: timestamppb.New(baseNow.Add(time.Hour)),
			},
		}
		h.promo.breakdown = &promotionv1.DiscountBreakdown{
			Warnings: []string{"SKU sku-1 exceeded per_user_limit for shop discount program; billed at regular price"},
			Shops: []*promotionv1.ShopDiscountBreakdown{
				{ShopId: "shop-1", SubtotalVnd: 1000000, ShopProgramDiscountVnd: 0, ShippingFeeVnd: 30000},
			},
		}

		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		req := &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "r4-f-warnings",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		}
		resp, err := h.svc.CreateCheckout(buyerCtx, req)
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		if len(resp.GetWarnings()) != 1 || !strings.Contains(resp.GetWarnings()[0], "per_user_limit") {
			t.Fatalf("expected warnings forwarded on first call, got %v", resp.GetWarnings())
		}
		replay, err := h.svc.CreateCheckout(buyerCtx, req)
		if err != nil || !replay.IdempotentReplay {
			t.Fatalf("replay failed: err=%v replay=%v", err, replay.GetIdempotentReplay())
		}
		if len(replay.GetWarnings()) != 1 || replay.GetWarnings()[0] != resp.GetWarnings()[0] {
			t.Fatalf("expected warnings preserved in idempotent replay snapshot, got %v", replay.GetWarnings())
		}
	})

	t.Run("G_ListActiveShopDiscountsBatchesBy200", func(t *testing.T) {
		h := newTestHarness()
		h.svc.nowFn = func() time.Time { return baseNow }
		items := make([]*orderv1.CheckoutItemInput, 0, 205)
		for i := 0; i < 205; i++ {
			id := fmt.Sprintf("sku-batch-%03d", i)
			h.product.skus[id] = &productv1.SKU{
				Id: id, ProductId: "p-b", ShopId: "shop-1", ProductName: "BatchItem", Name: "Default",
				PriceVnd: 10000, Stock: 10, AvailableStock: 10, WeightGrams: 50, CommissionRateBps: 500, IsActive: true,
			}
			items = append(items, &orderv1.CheckoutItemInput{SkuId: id, Quantity: 1})
		}
		if _, err := h.svc.CreateCheckout(ctxWithUser("buyer-1", "buyer", ""), &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "r4-g-batch200",
			AddressId:      "addr-buyer-1",
			Items:          items,
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		}); err != nil {
			t.Fatalf("CreateCheckout 205 SKUs: %v", err)
		}
		if len(h.promo.listShopDiscountsReqs) != 2 {
			t.Fatalf("expected 2 batched ListActiveShopDiscounts calls (200 + 5), got %d", len(h.promo.listShopDiscountsReqs))
		}
		if len(h.promo.listShopDiscountsReqs[0].GetSkuIds()) != 200 || len(h.promo.listShopDiscountsReqs[1].GetSkuIds()) != 5 {
			t.Fatalf("unexpected batch sizes: %d, %d",
				len(h.promo.listShopDiscountsReqs[0].GetSkuIds()), len(h.promo.listShopDiscountsReqs[1].GetSkuIds()))
		}
	})

	t.Run("G_DuplicateSKUConflictingFlashIDRejected_AndTrimSpaceOverrides", func(t *testing.T) {
		h := newTestHarness()
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		_, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "r4-g-dup-conflict",
			AddressId:      "addr-buyer-1",
			Items: []*orderv1.CheckoutItemInput{
				{SkuId: "sku-1", Quantity: 1, FlashSaleItemId: "fsi-1"},
				{SkuId: "sku-1", Quantity: 2, FlashSaleItemId: ""},
			},
			PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("expected InvalidArgument on conflicting duplicate sku flash_sale_item_id, got %v", err)
		}

		// TrimSpace on flashOverrides when from_cart=true:
		h.cart.cart = &cartv1.Cart{
			UserId: "buyer-1",
			ShopGroups: []*cartv1.CartShopGroup{
				{ShopId: "shop-1", Items: []*cartv1.CartItem{{SkuId: "sku-1", ShopId: "shop-1", Quantity: 1, PriceVnd: 1000000}}},
			},
		}
		resolved, _, err := h.svc.resolveCheckoutInputs(buyerCtx, "buyer-1", &orderv1.CreateCheckoutRequest{
			FromCart: true,
			Items:    []*orderv1.CheckoutItemInput{{SkuId: " sku-1 ", FlashSaleItemId: " fsi-trim "}},
		}, SalesChannelMarketplace, "")
		if err != nil {
			t.Fatalf("resolveCheckoutInputs: %v", err)
		}
		if len(resolved) != 1 || resolved[0].GetFlashSaleItemId() != "fsi-trim" {
			t.Fatalf("expected trimmed flash override 'fsi-trim', got %+v", resolved)
		}
	})

	t.Run("H_P3_CompletedPaymentFee_HashIgnoresShopProgram_RewardCoins_AndFeeMasking", func(t *testing.T) {
		// 1. computeCheckoutRequestHash ignores client ShopProgramItemId.
		r1 := &orderv1.CreateCheckoutRequest{
			AddressId:     "addr-1",
			Items:         []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1, ShopProgramItemId: "client-a"}},
			PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		}
		r2 := &orderv1.CreateCheckoutRequest{
			AddressId:     "addr-1",
			Items:         []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1, ShopProgramItemId: ""}},
			PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		}
		if computeCheckoutRequestHash("buyer-1", r1) != computeCheckoutRequestHash("buyer-1", r2) {
			t.Fatalf("computeCheckoutRequestHash must ignore client ShopProgramItemId")
		}

		// 2. ComputeOrderRewardCoins does not subtract S_shop from buyer merchandise paid.
		ordWithSShop := db.Order{
			SubtotalVND:             100000,
			ShippingFeeVND:          30000,
			ShopFreeshipDiscountVND: 30000, // shop covers shipping; buyer still paid 100,000 for goods
			TotalPayableVND:         100000,
		}
		if coins := ComputeOrderRewardCoins(ordWithSShop, 100); coins != 1000 {
			t.Fatalf("ComputeOrderRewardCoins(S_shop=30k, P=100k) = %d, want 1000", coins)
		}
		// With V_plat=70k, buyer paid 30k for goods (P=30k) -> 300 coins (capped by P too).
		ordWithSShopAndVPlat := db.Order{
			SubtotalVND:                100000,
			ShippingFeeVND:             30000,
			ShopFreeshipDiscountVND:    30000,
			PlatformVoucherDiscountVND: 70000,
			TotalPayableVND:            30000,
		}
		if coins := ComputeOrderRewardCoins(ordWithSShopAndVPlat, 100); coins != 300 {
			t.Fatalf("ComputeOrderRewardCoins(S_shop=30k, V_plat=70k, P=30k) = %d, want 300", coins)
		}

		// 3. order.completed outbox includes payment_fee_vnd + aggregateToProtoForCaller masks fees for buyers.
		h := newTestHarness()
		h.payment.feeBPSByMethod = map[paymentv1.PaymentMethod]int32{
			paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR: 150, // 1.5%
		}
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		seller1Ctx := ctxWithUser("seller-1", "seller", "shop-1")
		seller2Ctx := ctxWithUser("seller-2", "seller", "shop-2")
		adminCtx := ctxWithUser("admin-1", "admin", "")

		co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "r4-h-fees",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		ordID := co.Orders[0].Id
		// Buyer response on CreateCheckout MUST mask commission & payment fee to 0!
		if co.Orders[0].CommissionVnd != 0 || co.Orders[0].PaymentFeeVnd != 0 || co.Orders[0].PaymentFeeBps != 0 || co.Orders[0].Items[0].CommissionBps != 0 {
			t.Fatalf("CreateCheckout buyer response leaked seller fees: comm=%d payFee=%d payBps=%d itemBps=%d",
				co.Orders[0].CommissionVnd, co.Orders[0].PaymentFeeVnd, co.Orders[0].PaymentFeeBps, co.Orders[0].Items[0].CommissionBps)
		}
		// Buyer GetOrder -> masked 0.
		bGet, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ordID})
		if bGet.Order.CommissionVnd != 0 || bGet.Order.PaymentFeeVnd != 0 || bGet.Order.PaymentFeeBps != 0 || bGet.Order.Items[0].CommissionBps != 0 {
			t.Fatalf("buyer GetOrder leaked seller fees: %+v", bGet.Order)
		}
		// Owning seller and admin -> see real fees (C_cat=50,000, C_pay=15,450, bps=150).
		for name, cCtx := range map[string]context.Context{"seller1": seller1Ctx, "admin": adminCtx} {
			g, gErr := h.svc.GetOrder(cCtx, &orderv1.GetOrderRequest{OrderId: ordID})
			if gErr != nil {
				t.Fatalf("%s GetOrder failed: %v", name, gErr)
			}
			if g.Order.CommissionVnd != 50000 || g.Order.PaymentFeeVnd != 15450 || g.Order.PaymentFeeBps != 150 || g.Order.Items[0].CommissionBps != 500 {
				t.Fatalf("%s GetOrder fees = (%d,%d,%d,%d), want (50000,15450,150,500)",
					name, g.Order.CommissionVnd, g.Order.PaymentFeeVnd, g.Order.PaymentFeeBps, g.Order.Items[0].CommissionBps)
			}
		}
		// Non-owning seller (shop-2) placing an order as BUYER on shop-1: GetOrder succeeds (is buyer)
		// and MUST mask shop-1 seller fees to 0 (Item 9 fake-test fix).
		h.user.addresses["addr-s2-r4"] = &userv1.Address{
			Id: "addr-s2-r4", UserId: "seller-2", RecipientName: "Seller2", RecipientPhone: "0909000999",
			ProvinceCode: "79", ProvinceName: "Ho Chi Minh", WardCode: "26734", WardName: "Ben Nghe", StreetAddress: "2 Le Loi",
		}
		s2Co, err := h.svc.CreateCheckout(seller2Ctx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "r4-h-seller2-as-buyer",
			AddressId:      "addr-s2-r4",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("seller2 CreateCheckout on shop-1 failed: %v", err)
		}
		s2Get, err := h.svc.GetOrder(seller2Ctx, &orderv1.GetOrderRequest{OrderId: s2Co.Orders[0].Id})
		if err != nil {
			t.Fatalf("seller2 GetOrder on own purchase failed: %v", err)
		}
		if s2Get.Order.CommissionVnd != 0 || s2Get.Order.PaymentFeeVnd != 0 || s2Get.Order.PaymentFeeBps != 0 || s2Get.Order.Items[0].CommissionBps != 0 {
			t.Fatalf("non-owning seller buying from shop-1 leaked fees: %+v", s2Get.Order)
		}

		// Complete order and verify order.completed outbox payload carries payment_fee_vnd.
		_ = h.svc.HandlePaymentSucceeded(context.Background(), "pay-h", events.PaymentSucceededPayload{
			PaymentID: co.PaymentId, OrderGroupID: co.CheckoutGroupId, AmountVND: co.TotalPayableVnd,
		})
		_ = h.svc.HandleShipmentUpdated(context.Background(), "del-h", events.ShipmentUpdatedPayload{
			OrderID: ordID, Status: "delivered",
		})
		if _, err := h.svc.BuyerConfirmReceived(buyerCtx, &orderv1.BuyerConfirmReceivedRequest{OrderId: ordID}); err != nil {
			t.Fatalf("BuyerConfirmReceived: %v", err)
		}
		var foundCompleted bool
		for _, ev := range h.store.OutboxEvents() {
			if ev.EventType == events.SubjectOrderCompleted {
				var raw map[string]any
				_ = json.Unmarshal(ev.PayloadJSON, &raw)
				pf, ok := raw["payment_fee_vnd"].(float64)
				if !ok || int64(pf) != 15450 {
					t.Fatalf("order.completed payload missing payment_fee_vnd=15450: %s", string(ev.PayloadJSON))
				}
				foundCompleted = true
			}
		}
		if !foundCompleted {
			t.Fatalf("order.completed outbox event not found")
		}
	})
}
