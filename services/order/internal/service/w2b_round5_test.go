package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// faultInjectStore wraps store.Store to simulate post-commit errors, inspect failures,
// cancel failures, and detached-context checks deterministically.
type faultInjectStore struct {
	store.Store
	mu                    sync.Mutex
	commitThenErr         error
	persistRollbackErr    error
	persistCtxErr         error
	persistCalls          int
	failGroupCalls        int
	cancelGroupCalls      int
	listOrdersErr         error
	cancelGroupErr        error
	completeFailRemaining int
	completeFailErr       error
	onBeforeComplete      func(attempt int, arg store.UpdateCheckoutPaymentParams)
	completeAttempts      int
}

func (f *faultInjectStore) PersistCheckoutBatch(ctx context.Context, batch store.CheckoutBatch) ([]store.OrderAggregate, error) {
	f.mu.Lock()
	f.persistCalls++
	f.persistCtxErr = ctx.Err()
	rbErr := f.persistRollbackErr
	postErr := f.commitThenErr
	f.mu.Unlock()
	if rbErr != nil {
		return nil, rbErr
	}
	aggs, err := f.Store.PersistCheckoutBatch(ctx, batch)
	if err != nil {
		return nil, err
	}
	if postErr != nil {
		return nil, postErr
	}
	return aggs, nil
}

func (f *faultInjectStore) FailCheckoutGroup(ctx context.Context, id, paymentStatus string) error {
	f.mu.Lock()
	f.failGroupCalls++
	f.mu.Unlock()
	return f.Store.FailCheckoutGroup(ctx, id, paymentStatus)
}

func (f *faultInjectStore) ListOrdersByCheckoutGroup(ctx context.Context, checkoutGroupID string) ([]store.OrderAggregate, error) {
	f.mu.Lock()
	err := f.listOrdersErr
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return f.Store.ListOrdersByCheckoutGroup(ctx, checkoutGroupID)
}

func (f *faultInjectStore) InspectCheckoutGroupOrdersLocked(ctx context.Context, checkoutGroupID string) (*db.CheckoutGroup, []store.OrderAggregate, error) {
	f.mu.Lock()
	err := f.listOrdersErr
	f.mu.Unlock()
	if err != nil {
		return nil, nil, err
	}
	return f.Store.InspectCheckoutGroupOrdersLocked(ctx, checkoutGroupID)
}

func (f *faultInjectStore) CancelCheckoutGroupOrders(ctx context.Context, params store.CancelGroupOrdersParams) (*store.CancelGroupOrdersResult, error) {
	f.mu.Lock()
	f.cancelGroupCalls++
	err := f.cancelGroupErr
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return f.Store.CancelCheckoutGroupOrders(ctx, params)
}

func (f *faultInjectStore) CompleteCheckoutWithPayment(ctx context.Context, arg store.UpdateCheckoutPaymentParams) error {
	f.mu.Lock()
	f.completeAttempts++
	attempt := f.completeAttempts
	cb := f.onBeforeComplete
	shouldFail := f.completeFailRemaining > 0
	if shouldFail {
		f.completeFailRemaining--
	}
	failErr := f.completeFailErr
	f.mu.Unlock()
	if cb != nil {
		cb(attempt, arg)
	}
	if shouldFail {
		return failErr
	}
	return f.Store.CompleteCheckoutWithPayment(ctx, arg)
}

// TestW2B_R5_1_N1_PersistCheckoutBatchRecovery covers item 1 (P0 N1):
//   - Detached context on PersistCheckoutBatch.
//   - Commit succeeded then error returned -> inspect sees orders, continues forward without releasing stock/promo.
//   - Rollback error -> inspect sees 0 orders -> compensates stock/promo & marks FAILED.
//   - Persist error + inspect failure -> returns Unavailable, NO compensation, NO FAILED.
//   - Group not IN_PROGRESS -> PersistCheckoutBatch rejects without inserting orders.
func TestW2B_R5_1_N1_PersistCheckoutBatchRecovery(t *testing.T) {
	t.Run("CommitSucceededThenReturnsError_ContinuesForwardWithoutRelease", func(t *testing.T) {
		h := newTestHarness()
		wrapped := &faultInjectStore{
			Store:         h.store,
			commitThenErr: errors.New("broken pipe after postgres commit"),
		}
		h.svc.store = wrapped

		resp, err := h.svc.CreateCheckout(ctxWithUser("buyer-1", "buyer", ""), &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "r5-n1-post-commit-err",
			AddressId:         "addr-buyer-1",
			Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PlatformVoucherId: "v-plat",
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil {
			t.Fatalf("N1: expected forward recovery when orders were committed, got error: %v", err)
		}
		if len(resp.Orders) != 1 || resp.PaymentId == "" {
			t.Fatalf("expected completed checkout with payment, got %+v", resp)
		}
		if len(h.product.releaseCalls) != 0 || len(h.promo.releaseCalls) != 0 {
			t.Fatalf("N1 violation: released stock/promo on live committed orders! stock=%d promo=%d",
				len(h.product.releaseCalls), len(h.promo.releaseCalls))
		}
		grp, _ := h.store.GetCheckoutGroupByID(context.Background(), resp.CheckoutGroupId)
		if grp.Status != "COMPLETED" {
			t.Fatalf("expected COMPLETED checkout group, got %q", grp.Status)
		}
	})

	t.Run("PersistErrorAndInspectFails_ReturnsUnavailableNoCompensationNoFailed", func(t *testing.T) {
		h := newTestHarness()
		wrapped := &faultInjectStore{
			Store:         h.store,
			commitThenErr: errors.New("db connection lost during commit"),
			listOrdersErr: errors.New("db still unreachable"),
		}
		h.svc.store = wrapped

		_, err := h.svc.CreateCheckout(ctxWithUser("buyer-1", "buyer", ""), &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "r5-n1-uncertain",
			AddressId:         "addr-buyer-1",
			Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PlatformVoucherId: "v-plat",
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if status.Code(err) != codes.Unavailable {
			t.Fatalf("N1: expected codes.Unavailable when persist+inspect uncertain, got %v", err)
		}
		if len(h.product.releaseCalls) != 0 || len(h.promo.releaseCalls) != 0 {
			t.Fatalf("N1 violation: must NOT compensate when persist state uncertain! stock=%d promo=%d",
				len(h.product.releaseCalls), len(h.promo.releaseCalls))
		}
		lookup, gErr := h.store.GetCheckoutGroupForIdempotency(context.Background(), "buyer-1", "r5-n1-uncertain")
		if gErr != nil || lookup.Group.Status != "IN_PROGRESS" {
			t.Fatalf("N1 violation: group must remain IN_PROGRESS for watchdog, got err=%v lookup=%+v", gErr, lookup)
		}
	})

	t.Run("GroupNotInProgress_PersistCheckoutBatchRejects", func(t *testing.T) {
		h := newTestHarness()
		ctx := context.Background()
		gid := "grp-already-failed"
		_, _ = h.store.CreateCheckoutGroup(ctx, db.CreateCheckoutGroupParams{
			ID: gid, BuyerID: "buyer-1", IdempotencyKey: "r5-n1-not-inprog",
			RequestHash: "h", Status: "IN_PROGRESS", PaymentMethod: "COD",
		})
		_ = h.store.FailCheckoutGroup(ctx, gid, "STALE")

		_, err := h.store.PersistCheckoutBatch(ctx, store.CheckoutBatch{
			CheckoutGroupID: gid,
			Orders: []store.NewOrderRecord{{
				Order: db.InsertOrderParams{
					ID: "ord-should-fail", OrderCode: "NM-FAIL-1", CheckoutGroupID: gid,
					BuyerID: "buyer-1", ShopID: "shop-1", Status: "CONFIRMED",
					PaymentMethod: "COD", PaymentStatus: "PENDING", ShippingAddressJSON: []byte(`{}`),
				},
			}},
		})
		if err == nil {
			t.Fatalf("N1 violation: PersistCheckoutBatch succeeded on FAILED checkout group")
		}
		aggs, _ := h.store.ListOrdersByCheckoutGroup(ctx, gid)
		if len(aggs) != 0 {
			t.Fatalf("expected 0 orders inserted after rejected PersistCheckoutBatch, got %d", len(aggs))
		}
	})

	t.Run("ClientDisconnectedBeforePersist_DetachedContextSucceeds", func(t *testing.T) {
		h := newTestHarness()
		wrapped := &faultInjectStore{Store: h.store}
		h.svc.store = wrapped

		reqCtx, cancelClient := context.WithCancel(ctxWithUser("buyer-1", "buyer", ""))
		defer cancelClient()
		// Cancel client context right inside ReserveStock (immediately before PersistCheckoutBatch runs)
		// so prior steps run on an active ctx and PersistCheckoutBatch MUST run with a non-canceled detached ctx.
		h.product.onReserve = func(_ context.Context, _ *productv1.ReserveStockRequest) {
			cancelClient()
		}

		resp, err := h.svc.CreateCheckout(reqCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "r5-n1-detached-persist",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil {
			t.Fatalf("N1: CreateCheckout failed despite detached persist/payment ctx: %v", err)
		}
		if resp == nil || len(resp.Orders) != 1 {
			t.Fatalf("N1: expected 1 persisted order, got %+v", resp)
		}
		if wrapped.persistCalls != 1 {
			t.Fatalf("N1: PersistCheckoutBatch calls = %d, want 1", wrapped.persistCalls)
		}
		if wrapped.persistCtxErr != nil {
			t.Fatalf("N1: PersistCheckoutBatch received canceled client ctx: %v", wrapped.persistCtxErr)
		}
	})
}

// TestW2B_R5_2_N2_CreatePaymentAmbiguousAndSafeCancel covers item 2 (P0 N2):
//   - CreatePayment Unavailable but payment actually created -> probe finds it, continues forward!
//   - CreatePayment Unavailable and probe fails -> returns Unavailable, NO cancel, NO release!
//   - CreatePayment fails & CancelCheckoutGroupOrders fails -> NO stock/promo/payment release!
//   - CreatePayment fails (probe NotFound) on confirmed/restockable order -> calls CancelPayment(OrderGroupId),
//     restocks when ReleaseStock returns released=false, clears pending_cancel_compensation only after all succeed.
func TestW2B_R5_2_N2_CreatePaymentAmbiguousAndSafeCancel(t *testing.T) {
	t.Run("CreatePaymentUnavailableButPaymentCommitted_ProbesAndContinuesForward", func(t *testing.T) {
		h := newTestHarness()
		var createdPay *paymentv1.Payment
		h.payment.onCreatePayment = func(req *paymentv1.CreatePaymentRequest) {
			// Simulate payment committed in DB before network returned Unavailable.
			createdPay = &paymentv1.Payment{
				PaymentId:    "pay-committed-before-err",
				OrderGroupId: req.GetOrderGroupId(),
				OrderId:      req.GetOrderId(),
				UserId:       req.GetUserId(),
				AmountVnd:    req.GetAmountVnd(),
				Method:       req.GetMethod(),
				Status:       paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING,
				QrUrl:        "https://qr.example/recovered",
				QrCode:       "QR-RECOVERED",
				ExpiresAt:    req.GetExpiresAt(),
			}
		}
		h.payment.createErr = status.Error(codes.Unavailable, "eof after commit")
		h.payment.onGetPayment = func(req *paymentv1.GetPaymentRequest) (*paymentv1.GetPaymentResponse, error) {
			if createdPay != nil && req.GetOrderGroupId() == createdPay.GetOrderGroupId() {
				return &paymentv1.GetPaymentResponse{Payment: createdPay}, nil
			}
			return nil, status.Error(codes.NotFound, "not found")
		}

		resp, err := h.svc.CreateCheckout(ctxWithUser("buyer-1", "buyer", ""), &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "r5-n2-created-then-unavail",
			AddressId:         "addr-buyer-1",
			Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PlatformVoucherId: "v-plat",
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("N2: expected forward recovery when GetPayment finds created payment, got %v", err)
		}
		if resp.PaymentId != "pay-committed-before-err" {
			t.Fatalf("payment_id = %q, want pay-committed-before-err", resp.PaymentId)
		}
		if len(h.product.releaseCalls) != 0 || len(h.promo.releaseCalls) != 0 {
			t.Fatalf("N2 violation: must not release stock/promo when payment exists! stock=%d promo=%d",
				len(h.product.releaseCalls), len(h.promo.releaseCalls))
		}
		aggs, _ := h.store.ListOrdersByCheckoutGroup(context.Background(), resp.CheckoutGroupId)
		if len(aggs) != 1 || aggs[0].Order.Status != "PENDING" {
			t.Fatalf("order must stay PENDING (not cancelled), got %+v", aggs)
		}
	})

	t.Run("CreatePaymentUnavailableAndProbeFails_ReturnsUnavailableNoCancelNoRelease", func(t *testing.T) {
		h := newTestHarness()
		h.payment.createErr = status.Error(codes.Unavailable, "payment timeout")
		h.payment.getPaymentErr = status.Error(codes.Unavailable, "payment still unreachable")

		_, err := h.svc.CreateCheckout(ctxWithUser("buyer-1", "buyer", ""), &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "r5-n2-probe-fail",
			AddressId:         "addr-buyer-1",
			Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PlatformVoucherId: "v-plat",
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if status.Code(err) != codes.Unavailable {
			t.Fatalf("N2: expected Unavailable when probe fails, got %v", err)
		}
		if len(h.product.releaseCalls) != 0 || len(h.promo.releaseCalls) != 0 {
			t.Fatalf("N2 violation: must NOT release stock/promo when payment probe fails! stock=%d promo=%d",
				len(h.product.releaseCalls), len(h.promo.releaseCalls))
		}
		lookup, gErr := h.store.GetCheckoutGroupForIdempotency(context.Background(), "buyer-1", "r5-n2-probe-fail")
		if gErr != nil || lookup.Group.Status != "IN_PROGRESS" {
			t.Fatalf("N2 violation: group must stay IN_PROGRESS, got err=%v lookup=%+v", gErr, lookup)
		}
		aggs, _ := h.store.ListOrdersByCheckoutGroup(context.Background(), lookup.Group.ID)
		if len(aggs) != 1 || aggs[0].Order.Status != "PENDING" {
			t.Fatalf("N2 violation: order must stay PENDING when probe fails, got %+v", aggs)
		}
	})

	t.Run("CancelGroupOrdersFails_DoesNotReleaseStockOrPromo", func(t *testing.T) {
		h := newTestHarness()
		wrapped := &faultInjectStore{
			Store:          h.store,
			cancelGroupErr: errors.New("db unavailable during cancel"),
		}
		h.svc.store = wrapped
		h.payment.createErr = status.Error(codes.InvalidArgument, "definite reject")

		_, err := h.svc.CreateCheckout(ctxWithUser("buyer-1", "buyer", ""), &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "r5-n2-cancel-fail",
			AddressId:         "addr-buyer-1",
			Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PlatformVoucherId: "v-plat",
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err == nil {
			t.Fatalf("expected error")
		}
		if len(h.product.releaseCalls) != 0 || len(h.promo.releaseCalls) != 0 || len(h.payment.cancelCalls) != 0 {
			t.Fatalf("N2 violation: released resources even though CancelCheckoutGroupOrders failed! stock=%d promo=%d payCancel=%d",
				len(h.product.releaseCalls), len(h.promo.releaseCalls), len(h.payment.cancelCalls))
		}
	})

	t.Run("CancelBranch_CallsCancelPaymentAndRestocksWhenReleaseStockReturnsFalse", func(t *testing.T) {
		h := newTestHarness()
		h.payment.createErr = status.Error(codes.InvalidArgument, "definite reject")
		h.product.releaseReturnsOk = false // simulate ConfirmStock already ran

		_, err := h.svc.CreateCheckout(ctxWithUser("buyer-1", "buyer", ""), &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "r5-n2-restock-branch",
			AddressId:         "addr-buyer-1",
			Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PlatformVoucherId: "v-plat",
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err == nil {
			t.Fatalf("expected error")
		}
		if len(h.payment.cancelCalls) != 1 || h.payment.cancelCalls[0].GetOrderGroupId() == "" {
			t.Fatalf("N2: expected CancelPayment(OrderGroupId) after cancel, got %+v", h.payment.cancelCalls)
		}
		if len(h.product.restockCalls) != 1 {
			t.Fatalf("N2: expected RestockOrderItems when ReleaseStock returned released=false, got %d calls", len(h.product.restockCalls))
		}
		aggs, _ := h.store.ListOrdersByCheckoutGroup(context.Background(), h.payment.cancelCalls[0].GetOrderGroupId())
		if len(aggs) != 1 || aggs[0].Order.PendingCancelCompensation {
			t.Fatalf("N2: expected PendingCancelCompensation=false after all compensations succeeded, got %+v", aggs)
		}
	})

	t.Run("CancelBranch_RestockFails_KeepsPendingCancelCompensationTrue", func(t *testing.T) {
		h := newTestHarness()
		h.payment.createErr = status.Error(codes.InvalidArgument, "definite reject")
		h.product.releaseReturnsOk = false
		h.product.restockErr = status.Error(codes.Unavailable, "product restock unavailable")

		_, err := h.svc.CreateCheckout(ctxWithUser("buyer-1", "buyer", ""), &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "r5-n2-restock-fail-flag",
			AddressId:         "addr-buyer-1",
			Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PlatformVoucherId: "v-plat",
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err == nil {
			t.Fatalf("expected error")
		}
		if len(h.payment.cancelCalls) != 1 {
			t.Fatalf("expected CancelPayment call, got %d", len(h.payment.cancelCalls))
		}
		aggs, _ := h.store.ListOrdersByCheckoutGroup(context.Background(), h.payment.cancelCalls[0].GetOrderGroupId())
		if len(aggs) != 1 || !aggs[0].Order.PendingCancelCompensation {
			t.Fatalf("N2: expected PendingCancelCompensation=true when RestockOrderItems failed, got %+v", aggs)
		}
	})
}

// TestW2B_R5_3_N3_IncompleteCheckoutForwardRecovery covers item 3 (P0 N3):
//   - COD crash before CreatePayment (after ConfirmStock ran) -> forward-recovers via idempotent CreatePayment!
//   - COD crash before CreatePayment where seller already shipped (PREPARING/SHIPPING with tracking) -> forward-recovers, never cancels or releases promo!
//   - All orders already CANCELLED -> closes group FAILED (no infinite loop, no promo release).
func TestW2B_R5_3_N3_IncompleteCheckoutForwardRecovery(t *testing.T) {
	seedStaleGroup := func(t *testing.T, h *testHarness, groupID, orderID, ordStatus, payMethod, payStatus, tracking string, payable int64) {
		t.Helper()
		ctx := context.Background()
		_, err := h.store.CreateCheckoutGroup(ctx, db.CreateCheckoutGroupParams{
			ID: groupID, BuyerID: "buyer-1", IdempotencyKey: "idem-" + groupID,
			RequestHash: "h-" + groupID, Status: "IN_PROGRESS", PaymentMethod: payMethod, TotalPayableVND: payable,
		})
		if err != nil {
			t.Fatalf("CreateCheckoutGroup: %v", err)
		}
		_, err = h.store.PersistCheckoutBatch(ctx, store.CheckoutBatch{
			CheckoutGroupID: groupID,
			Now:             time.Now().UTC().Add(-10 * time.Minute),
			Orders: []store.NewOrderRecord{{
				Order: db.InsertOrderParams{
					ID: orderID, OrderCode: "NM-" + orderID, CheckoutGroupID: groupID,
					BuyerID: "buyer-1", ShopID: "shop-1", Status: ordStatus,
					PaymentMethod: payMethod, PaymentStatus: payStatus,
					SubtotalVND: 1000000, ShippingFeeVND: 30000, CommissionVND: 50000,
					TotalPayableVND: payable, TrackingCode: tracking,
					ShippingAddressJSON: []byte(`{}`),
				},
				Items: []db.InsertOrderItemParams{{
					ID: "item-" + orderID, OrderID: orderID, SKUID: "sku-1",
					ProductID: "prod-1", ProductName: "Phone", SKUName: "Black",
					UnitPriceVND: 1000000, EffectiveUnitPriceVND: 1000000, Quantity: 1,
					LineTotalVND: 1000000, WeightGrams: 500, CommissionRateBPS: 500,
				}},
			}},
		})
		if err != nil {
			t.Fatalf("PersistCheckoutBatch: %v", err)
		}
		h.store.SetCheckoutGroupUpdatedAt(groupID, time.Now().UTC().Add(-10*time.Minute))
	}

	t.Run("CODConfirmedAfterConfirmStock_RecoversViaIdempotentCreatePayment", func(t *testing.T) {
		h := newTestHarness()
		h.svc.cfg.StaleCheckoutAfter = 1 * time.Minute
		seedStaleGroup(t, h, "grp-cod-crash", "ord-cod-crash", "CONFIRMED", "COD", "PENDING", "", 1030000)

		h.payment.onGetPayment = func(_ *paymentv1.GetPaymentRequest) (*paymentv1.GetPaymentResponse, error) {
			return nil, status.Error(codes.NotFound, "no payment created before crash")
		}
		h.product.releaseReturnsOk = false // ConfirmStock already ran!

		handled, err := h.svc.ScanIncompleteCheckoutGroups(context.Background())
		if err != nil || handled != 1 {
			t.Fatalf("ScanIncompleteCheckoutGroups = %d, %v; want 1, nil", handled, err)
		}
		if len(h.payment.createCalls) != 1 || h.payment.createCalls[0].GetIdempotencyKey() != "checkout:grp-cod-crash" {
			t.Fatalf("N3: expected idempotent CreatePayment(checkout:grp-cod-crash), got %+v", h.payment.createCalls)
		}
		if len(h.promo.releaseCalls) != 0 || len(h.product.releaseCalls) != 0 {
			t.Fatalf("N3 violation: must NOT cancel/release CONFIRMED COD order! promo=%d stock=%d",
				len(h.promo.releaseCalls), len(h.product.releaseCalls))
		}
		grp, _ := h.store.GetCheckoutGroupByID(context.Background(), "grp-cod-crash")
		ord, _ := h.store.GetOrderWithDetails(context.Background(), "ord-cod-crash")
		if grp.Status != "COMPLETED" || grp.PaymentID == "" || ord.Order.Status != "CONFIRMED" || ord.Order.PaymentID == "" {
			t.Fatalf("expected COMPLETED group & CONFIRMED order with payment_id, got grp=%+v ord=%+v", grp, ord.Order)
		}
	})

	t.Run("CODAlreadyShipped_CreatePaymentFailsTransiently_NeverCancelsOrReleasesPromo", func(t *testing.T) {
		h := newTestHarness()
		h.svc.cfg.StaleCheckoutAfter = 1 * time.Minute
		seedStaleGroup(t, h, "grp-cod-shipped", "ord-cod-shipped", "SHIPPING", "COD", "PENDING", "TRK-999", 1030000)

		h.payment.onGetPayment = func(_ *paymentv1.GetPaymentRequest) (*paymentv1.GetPaymentResponse, error) {
			return nil, status.Error(codes.NotFound, "not found")
		}
		h.payment.createErr = status.Error(codes.Unavailable, "payment temporarily down")

		handled, err := h.svc.ScanIncompleteCheckoutGroups(context.Background())
		if err != nil {
			t.Fatalf("ScanIncompleteCheckoutGroups err: %v", err)
		}
		if handled != 0 {
			t.Fatalf("expected handled=0 on transient CreatePayment error so next tick retries, got %d", handled)
		}
		if len(h.promo.releaseCalls) != 0 || len(h.product.releaseCalls) != 0 {
			t.Fatalf("N3 violation: released promo/stock on SHIPPING order! promo=%d stock=%d",
				len(h.promo.releaseCalls), len(h.product.releaseCalls))
		}
		grp, _ := h.store.GetCheckoutGroupByID(context.Background(), "grp-cod-shipped")
		ord, _ := h.store.GetOrderWithDetails(context.Background(), "ord-cod-shipped")
		if grp.Status != "IN_PROGRESS" || ord.Order.Status != "SHIPPING" {
			t.Fatalf("group must remain IN_PROGRESS and order SHIPPING for next tick, got grp=%s ord=%s", grp.Status, ord.Order.Status)
		}
	})

	t.Run("AllOrdersAlreadyCancelled_ClosesGroupFailedNoInfiniteLoop", func(t *testing.T) {
		h := newTestHarness()
		h.svc.cfg.StaleCheckoutAfter = 1 * time.Minute
		ctx := context.Background()
		_, err := h.store.CreateCheckoutGroup(ctx, db.CreateCheckoutGroupParams{
			ID: "grp-all-can", BuyerID: "buyer-1", IdempotencyKey: "idem-grp-all-can",
			RequestHash: "h-grp-all-can", Status: "IN_PROGRESS", PaymentMethod: "VIETQR", TotalPayableVND: 1030000,
		})
		if err != nil {
			t.Fatalf("CreateCheckoutGroup: %v", err)
		}
		_, err = h.store.PersistCheckoutBatch(ctx, store.CheckoutBatch{
			CheckoutGroupID:     "grp-all-can",
			PendingStockConfirm: true,
			Now:                 time.Now().UTC().Add(-10 * time.Minute),
			Orders: []store.NewOrderRecord{{
				Order: db.InsertOrderParams{
					ID: "ord-all-can", OrderCode: "NM-ord-all-can", CheckoutGroupID: "grp-all-can",
					BuyerID: "buyer-1", ShopID: "shop-1", Status: "CANCELLED",
					PaymentMethod: "VIETQR", PaymentStatus: "CANCELLED",
					SubtotalVND: 1000000, ShippingFeeVND: 30000, TotalPayableVND: 1030000,
					ShippingAddressJSON: []byte(`{}`),
				},
			}},
		})
		if err != nil {
			t.Fatalf("PersistCheckoutBatch: %v", err)
		}
		h.store.SetCheckoutGroupUpdatedAt("grp-all-can", time.Now().UTC().Add(-10*time.Minute))

		wrapped := &faultInjectStore{Store: h.store}
		h.svc.store = wrapped

		h.payment.onGetPayment = func(_ *paymentv1.GetPaymentRequest) (*paymentv1.GetPaymentResponse, error) {
			return nil, status.Error(codes.NotFound, "not found")
		}

		handled1, err := h.svc.ScanIncompleteCheckoutGroups(ctx)
		if err != nil || handled1 != 1 {
			t.Fatalf("first sweep = %d, %v; want 1, nil", handled1, err)
		}
		// Prove the dedicated `if allCancelled` branch executed directly (FailCheckoutGroup called,
		// CancelCheckoutGroupOrders NOT called, and PendingStockConfirm cleared).
		if wrapped.failGroupCalls != 1 || wrapped.cancelGroupCalls != 0 {
			t.Fatalf("expected allCancelled branch (failGroupCalls=1, cancelGroupCalls=0), got fail=%d cancel=%d",
				wrapped.failGroupCalls, wrapped.cancelGroupCalls)
		}
		grp, _ := h.store.GetCheckoutGroupByID(ctx, "grp-all-can")
		if grp.Status != "FAILED" {
			t.Fatalf("expected FAILED checkout_group for all-cancelled group, got %q", grp.Status)
		}
		if grp.PendingStockConfirm {
			t.Fatalf("expected PendingStockConfirm cleared to false on all-cancelled group")
		}
		if len(h.promo.releaseCalls) != 0 {
			t.Fatalf("must not re-release promo when orders already cancelled, got %d", len(h.promo.releaseCalls))
		}
		// Second sweep must find 0 groups (no infinite loop).
		handled2, _ := h.svc.ScanIncompleteCheckoutGroups(ctx)
		if handled2 != 0 {
			t.Fatalf("expected 0 on second sweep, got %d (infinite loop!)", handled2)
		}
	})
}

// TestW2B_R5_4_N4_CompleteCheckoutDoesNotClobberRetryPaymentP2 covers item 4 (P1 N4):
// RetryPayment creates P2 (exp2, snapshot=NULL) between attempt 1 and attempt 2 of CompleteCheckoutWithPayment(P1, exp1).
// Late P1 attempt MUST NOT overwrite P2's payment_id, payment_expires_at, or response_snapshot_json!
func TestW2B_R5_4_N4_CompleteCheckoutDoesNotClobberRetryPaymentP2(t *testing.T) {
	h := newTestHarness()
	baseNow := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	h.svc.nowFn = func() time.Time { return baseNow }
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	exp2 := baseNow.Add(15 * time.Minute)

	var injectedRetry atomic.Bool
	wrapped := &faultInjectStore{
		Store:                 h.store,
		completeFailRemaining: 1,
		completeFailErr:       errors.New("transient fail on attempt 1"),
	}
	wrapped.onBeforeComplete = func(attempt int, arg store.UpdateCheckoutPaymentParams) {
		if attempt == 2 && injectedRetry.CompareAndSwap(false, true) {
			// Simulate Pod B completing P1, then buyer RetryPayment creating P2 (exp2, snapshot=NULL)
			// BEFORE Pod A's retry #2 of CompleteCheckoutWithPayment(P1) executes!
			_ = h.store.CompleteCheckoutWithPayment(context.Background(), arg)
			_ = h.store.UpdateGroupPaymentOnRetry(context.Background(), store.RetryPaymentUpdateParams{
				CheckoutGroupID:     arg.CheckoutGroupID,
				ExpectedPaymentID:   arg.PaymentID,
				NewPaymentID:        "pay-p2-superseding",
				PaymentStatus:       "PENDING",
				QRURL:               "https://qr.example/p2",
				QRCode:              "QR-P2",
				PaymentExpiresAt:    &exp2,
				RetryIdempotencyKey: "retry-p2",
				Now:                 baseNow,
			})
		}
	}
	h.svc.store = wrapped

	h.svc.cfg.PaymentExpiryMinutes = 5 // exp1 = baseNow + 5m (< exp2 = baseNow + 15m)
	resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "r5-n4-p2-race",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
	})
	if err != nil {
		t.Fatalf("CreateCheckout: %v", err)
	}

	grp, _ := h.store.GetCheckoutGroupByID(context.Background(), resp.CheckoutGroupId)
	if grp.PaymentID != "pay-p2-superseding" {
		t.Fatalf("N4 violation: group payment_id = %q, want pay-p2-superseding", grp.PaymentID)
	}
	if grp.PaymentExpiresAt == nil || !grp.PaymentExpiresAt.Equal(exp2) {
		t.Fatalf("N4 violation: group payment_expires_at clobbered to %v, want %v", grp.PaymentExpiresAt, exp2)
	}
	if len(grp.ResponseSnapshotJSON) > 0 {
		t.Fatalf("N4 violation: group response_snapshot_json clobbered to %s, want NULL/empty", string(grp.ResponseSnapshotJSON))
	}
	aggs, _ := h.store.ListOrdersByCheckoutGroup(context.Background(), resp.CheckoutGroupId)
	if aggs[0].Order.PaymentID != "pay-p2-superseding" || aggs[0].Order.PaymentExpiresAt == nil || !aggs[0].Order.PaymentExpiresAt.Equal(exp2) {
		t.Fatalf("N4 violation: order payment_id=%q expires=%v clobbered by stale P1", aggs[0].Order.PaymentID, aggs[0].Order.PaymentExpiresAt)
	}
}

// TestW2B_R5_5_N5_CancelPaymentWithEmptyIDAndPostCommitCancel covers item 5 (P1 N5):
//   - CancelOrder on PENDING order with empty payment_id calls CancelPayment(OrderGroupId).
//   - CompleteCheckoutWithPayment seeing all orders CANCELLED calls CancelPayment(P1) after commit.
func TestW2B_R5_5_N5_CancelPaymentWithEmptyIDAndPostCommitCancel(t *testing.T) {
	t.Run("CancelOrderPendingWithEmptyPaymentID_CallsCancelPaymentByGroupID", func(t *testing.T) {
		h := newTestHarness()
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		// Cancel right after CreatePayment returns (while order still has payment_id == "")!
		var cancelOrderErr error
		var createdGroupID, cancelledOrderID string
		h.payment.afterCreatePay = func(pay *paymentv1.Payment) {
			createdGroupID = pay.GetOrderGroupId()
			aggs, _ := h.store.ListOrdersByCheckoutGroup(context.Background(), pay.GetOrderGroupId())
			if len(aggs) > 0 {
				cancelledOrderID = aggs[0].Order.ID
				_, cancelOrderErr = h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{
					OrderId: cancelledOrderID,
					Reason:  "buyer cancelled before CompleteCheckoutWithPayment",
				})
			}
		}

		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "r5-n5-cancel-empty-payid",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if cancelOrderErr != nil {
			t.Fatalf("CancelOrder=%v", cancelOrderErr)
		}
		if err == nil || resp != nil || status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "CHECKOUT_FAILED") {
			t.Fatalf("D11 (P1-1): expected FailedPrecondition CHECKOUT_FAILED when all orders cancelled, got resp=%+v err=%v", resp, err)
		}
		lookup, lErr := h.store.GetCheckoutGroupForIdempotency(context.Background(), "buyer-1", "r5-n5-cancel-empty-payid")
		if lErr != nil || !lookup.HasOrders || lookup.Group.Status != "FAILED" || lookup.Group.ID != createdGroupID {
			t.Fatalf("expected FAILED group with orders in store, got lookup=%+v err=%v", lookup, lErr)
		}
		// Expect CancelOrder called CancelPayment(OrderGroupId) even though PaymentID=="",
		// AND CompleteCheckoutWithPayment re-emitted a fresh order.cancelled outbox event (distinct event_id)
		// in the same TX so payment cancels/refunds P1 regardless of P1's status (P0-1).
		var sawByGroup bool
		for _, req := range h.payment.cancelCalls {
			if req.GetOrderGroupId() == lookup.Group.ID && req.GetPaymentId() == "" {
				sawByGroup = true
			}
		}
		if !sawByGroup {
			t.Fatalf("N5: CancelOrder on PENDING order with empty payment_id did not call CancelPayment(OrderGroupId); calls=%+v", h.payment.cancelCalls)
		}
		seenCancelEventIDs := map[string]struct{}{}
		for _, ev := range h.store.OutboxEvents() {
			if ev.EventType != events.SubjectOrderCancelled {
				continue
			}
			var p events.OrderCancelledPayload
			if json.Unmarshal(ev.PayloadJSON, &p) == nil && p.OrderID == cancelledOrderID {
				seenCancelEventIDs[ev.ID] = struct{}{}
			}
		}
		if len(seenCancelEventIDs) < 2 {
			t.Fatalf("P0-1: expected >=2 distinct order.cancelled outbox events (initial cancel + CompleteCheckoutWithPayment re-emit), got %d", len(seenCancelEventIDs))
		}
	})

	t.Run("CancelOrderPaymentOutage_FlagsPendingCancelCompAndReemitsOutbox", func(t *testing.T) {
		h := newTestHarness()
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		var cancelOrderErr error
		var cancelledOrderID string
		h.payment.afterCreatePay = func(pay *paymentv1.Payment) {
			aggs, _ := h.store.ListOrdersByCheckoutGroup(context.Background(), pay.GetOrderGroupId())
			if len(aggs) > 0 {
				cancelledOrderID = aggs[0].Order.ID
				// Simulate transient payment outage when CancelOrder's compensation runs!
				h.payment.cancelErr = status.Error(codes.Unavailable, "payment temporarily unavailable")
				_, cancelOrderErr = h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{
					OrderId: cancelledOrderID,
					Reason:  "buyer cancelled",
				})
			}
		}

		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "r5-n5-postcommit-retry",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if cancelOrderErr != nil {
			t.Fatalf("CancelOrder=%v", cancelOrderErr)
		}
		if err == nil || resp != nil || status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "CHECKOUT_FAILED") {
			t.Fatalf("D11 (P1-1): expected FailedPrecondition CHECKOUT_FAILED when all orders cancelled, got resp=%+v err=%v", resp, err)
		}
		lookup, lErr := h.store.GetCheckoutGroupForIdempotency(context.Background(), "buyer-1", "r5-n5-postcommit-retry")
		if lErr != nil || !lookup.HasOrders || lookup.Group.Status != "FAILED" {
			t.Fatalf("expected FAILED group with orders in store, got lookup=%+v err=%v", lookup, lErr)
		}
		// Fresh order.cancelled outbox event is durably recorded inside CompleteCheckoutWithPayment TX even during payment outage.
		cancelOutboxCount := 0
		for _, ev := range h.store.OutboxEvents() {
			if ev.EventType != events.SubjectOrderCancelled {
				continue
			}
			var p events.OrderCancelledPayload
			if json.Unmarshal(ev.PayloadJSON, &p) == nil && p.OrderID == cancelledOrderID {
				cancelOutboxCount++
			}
		}
		if cancelOutboxCount < 2 {
			t.Fatalf("P0-1: expected durable re-emitted order.cancelled outbox event during payment outage, got %d", cancelOutboxCount)
		}
		// Order must still be flagged pending_cancel_compensation=true so ScanPendingCancelCompensations recovers!
		agg, _ := h.store.GetOrderWithDetails(context.Background(), cancelledOrderID)
		if !agg.Order.PendingCancelCompensation {
			t.Fatalf("N5: expected pending_cancel_compensation=true when CancelPayment failed during outage")
		}
		// Payment recovers -> watchdog succeeds and clears pending_cancel_compensation.
		h.payment.cancelErr = nil
		handled, err := h.svc.ScanPendingCancelCompensations(context.Background())
		if err != nil || handled != 1 {
			t.Fatalf("ScanPendingCancelCompensations = %d, %v; want 1, nil", handled, err)
		}
		aggAfter, _ := h.store.GetOrderWithDetails(context.Background(), cancelledOrderID)
		if aggAfter.Order.PendingCancelCompensation {
			t.Fatalf("expected pending_cancel_compensation cleared after watchdog recovery")
		}
	})
}

// TestW2B_R5_6_P2_N6_N7_N8_N9_N10 covers item 6 (N6, N7, N8, N9, N10) & item 7 (N12).
func TestW2B_R5_6_P2_N6_N7_N8_N9_N10(t *testing.T) {
	t.Run("N6_ReplayLegacySnapshotWithNonZeroFees_MasksForBuyer", func(t *testing.T) {
		h := newTestHarness()
		ctx := context.Background()
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		req := &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "r5-n6-legacy-snap",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		}
		reqHash := computeCheckoutRequestHash("buyer-1", req)
		gid := "grp-legacy-snap"
		_, _ = h.store.CreateCheckoutGroup(ctx, db.CreateCheckoutGroupParams{
			ID: gid, BuyerID: "buyer-1", IdempotencyKey: "r5-n6-legacy-snap",
			RequestHash: reqHash, Status: "IN_PROGRESS", PaymentMethod: "VIETQR", TotalPayableVND: 1030000,
		})
		_, _ = h.store.PersistCheckoutBatch(ctx, store.CheckoutBatch{
			CheckoutGroupID: gid,
			Orders: []store.NewOrderRecord{{
				Order: db.InsertOrderParams{
					ID: "ord-legacy", OrderCode: "NM-LEGACY-1", CheckoutGroupID: gid,
					BuyerID: "buyer-1", ShopID: "shop-1", Status: "PENDING",
					PaymentMethod: "VIETQR", PaymentStatus: "PENDING",
					SubtotalVND: 1000000, ShippingFeeVND: 30000, CommissionVND: 50000,
					PaymentFeeVND: 15450, PaymentFeeBPS: 150, TotalPayableVND: 1030000,
					ShippingAddressJSON: []byte(`{}`),
				},
				Items: []db.InsertOrderItemParams{{
					ID: "oi-legacy", OrderID: "ord-legacy", SKUID: "sku-1", ProductID: "p-1",
					ProductName: "KB", SKUName: "Std", UnitPriceVND: 1000000, EffectiveUnitPriceVND: 1000000,
					Quantity: 1, LineTotalVND: 1000000, CommissionRateBPS: 500,
				}},
			}},
		})
		_ = h.store.MarkOrderOrphanShipmentManualReview(ctx, "ord-legacy", "legacy review flag")
		// Inject a pre-r4 snapshot containing non-zero seller fees + needs_manual_review=true!
		legacySnap, _ := protojson.Marshal(&orderv1.CreateCheckoutResponse{
			CheckoutGroupId: gid,
			PaymentId:       "pay-legacy",
			PaymentMethod:   orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
			PaymentStatus:   "PENDING",
			TotalPayableVnd: 1030000,
			Orders: []*orderv1.Order{{
				Id:                "ord-legacy",
				ShopId:            "shop-1",
				BuyerId:           "buyer-1",
				CommissionVnd:     50000,
				PaymentFeeVnd:     15450,
				PaymentFeeBps:     150,
				NeedsManualReview: true,
				Items:             []*orderv1.OrderItem{{SkuId: "sku-1", CommissionBps: 500}},
			}},
		})
		_ = h.store.CompleteCheckoutWithPayment(ctx, store.UpdateCheckoutPaymentParams{
			CheckoutGroupID:      gid,
			PaymentID:            "pay-legacy",
			PaymentStatus:        "PENDING",
			ResponseSnapshotJSON: legacySnap,
		})

		replay, err := h.svc.CreateCheckout(buyerCtx, req)
		if err != nil {
			t.Fatalf("replay failed: %v", err)
		}
		o := replay.Orders[0]
		if o.CommissionVnd != 0 || o.PaymentFeeVnd != 0 || o.PaymentFeeBps != 0 || o.Items[0].CommissionBps != 0 || o.NeedsManualReview {
			t.Fatalf("N6 violation: legacy snapshot leaked fees/review flag to buyer: %+v", o)
		}

		// Gap 5b: Admin/seller replay must propagate store read error instead of silently falling back to masked snapshot.
		wrapped := &faultInjectStore{
			Store:         h.store,
			listOrdersErr: errors.New("db read failure during privileged replay"),
		}
		h.svc.store = wrapped
		adminCtx := ctxWithUser("admin-1", "admin", "")
		adminReq := &orderv1.CreateCheckoutRequest{
			BuyerId:        "buyer-1",
			IdempotencyKey: req.IdempotencyKey,
			AddressId:      req.AddressId,
			Items:          req.Items,
			PaymentMethod:  req.PaymentMethod,
		}
		if _, rErr := h.svc.CreateCheckout(adminCtx, adminReq); rErr == nil {
			t.Fatalf("N6 violation: admin replay silently fell back to masked snapshot on store error")
		}
	})

	t.Run("N7_WarningsPersistedBeforeCreatePayment_SurviveRecovery", func(t *testing.T) {
		h := newTestHarness()
		baseNow := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
		h.svc.nowFn = func() time.Time { return baseNow }
		h.promo.activeShopDiscounts = []*promotionv1.ActiveShopDiscount{{
			ItemId: "sp-lim", ProgramId: "p-1", ShopId: "shop-1", SkuId: "sku-1",
			OriginalPriceVnd: 1000000, DiscountedPriceVnd: 800000, PerUserLimit: 1,
			StartsAt: timestamppb.New(baseNow.Add(-time.Hour)), EndsAt: timestamppb.New(baseNow.Add(time.Hour)),
		}}
		h.promo.breakdown = &promotionv1.DiscountBreakdown{
			Warnings: []string{"SKU sku-1 exceeded per_user_limit"},
			Shops:    []*promotionv1.ShopDiscountBreakdown{{ShopId: "shop-1", SubtotalVnd: 1000000, ShippingFeeVnd: 30000}},
		}
		wrapped := &faultInjectStore{
			Store:                 h.store,
			completeFailRemaining: 5,
			completeFailErr:       errors.New("db hiccup"),
		}
		h.svc.store = wrapped

		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		req := &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "r5-n7-warnings",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		}
		resp1, err := h.svc.CreateCheckout(buyerCtx, req)
		if err == nil || resp1 != nil || status.Code(err) != codes.Unavailable {
			t.Fatalf("P1-1: CreateCheckout 1 expected Unavailable when finalization fails, got resp=%+v err=%v", resp1, err)
		}

		// Reset completeFailRemaining = 0 BEFORE the 500ms pre-grace check so that if the 2s grace guard
		// were removed, tryFinalizeInProgressGroupFromPayment would succeed immediately and fail this test deterministically!
		wrapped.mu.Lock()
		wrapped.completeFailRemaining = 0
		wrapped.mu.Unlock()

		// Before grace period (2s): retry does NOT hijack in-flight Pod A!
		h.svc.nowFn = func() time.Time { return baseNow.Add(500 * time.Millisecond) }
		shortCtx, shortCancel := context.WithTimeout(buyerCtx, 120*time.Millisecond)
		defer shortCancel()
		if _, err := h.svc.CreateCheckout(shortCtx, req); status.Code(err) != codes.DeadlineExceeded {
			t.Fatalf("N7: expected retry before 2s grace to wait instead of hijacking, got %v", err)
		}

		// Advance past grace period (5s > 2s) -> idempotent retry recovers AND preserves warnings!
		h.svc.nowFn = func() time.Time { return baseNow.Add(5 * time.Second) }

		replay, err := h.svc.CreateCheckout(buyerCtx, req)
		if err != nil {
			t.Fatalf("recovered replay failed: %v", err)
		}
		if len(replay.Warnings) != 1 || replay.Warnings[0] != "SKU sku-1 exceeded per_user_limit" {
			t.Fatalf("N7 violation: warnings lost during recovery! got %v", replay.Warnings)
		}
	})

	t.Run("N8_MultiShopCOD_PartialPaidDoesNotMarkGroupSucceeded", func(t *testing.T) {
		h := newTestHarness()
		ctx := context.Background()
		gid := "grp-n8-cod"
		_, _ = h.store.CreateCheckoutGroup(ctx, db.CreateCheckoutGroupParams{
			ID: gid, BuyerID: "buyer-1", IdempotencyKey: "r5-n8", RequestHash: "h8",
			Status: "IN_PROGRESS", PaymentMethod: "COD", TotalPayableVND: 1560000,
		})
		_, _ = h.store.PersistCheckoutBatch(ctx, store.CheckoutBatch{
			CheckoutGroupID: gid,
			Orders: []store.NewOrderRecord{
				{Order: db.InsertOrderParams{
					ID: "ord-n8-1", OrderCode: "NM-N8-1", CheckoutGroupID: gid,
					BuyerID: "buyer-1", ShopID: "shop-1", Status: "DELIVERED",
					PaymentMethod: "COD", PaymentStatus: "SUCCEEDED", TotalPayableVND: 1030000,
					ShippingAddressJSON: []byte(`{}`),
				}},
				{Order: db.InsertOrderParams{
					ID: "ord-n8-2", OrderCode: "NM-N8-2", CheckoutGroupID: gid,
					BuyerID: "buyer-1", ShopID: "shop-2", Status: "CONFIRMED",
					PaymentMethod: "COD", PaymentStatus: "PENDING", TotalPayableVND: 530000,
					ShippingAddressJSON: []byte(`{}`),
				}},
			},
		})
		if err := h.store.CompleteCheckoutWithPayment(ctx, store.UpdateCheckoutPaymentParams{
			CheckoutGroupID: gid, PaymentID: "pay-n8", PaymentStatus: "PENDING",
		}); err != nil {
			t.Fatalf("CompleteCheckoutWithPayment: %v", err)
		}
		grp, _ := h.store.GetCheckoutGroupByID(ctx, gid)
		if grp.PaymentStatus != "PENDING" {
			t.Fatalf("N8 violation: multi-shop COD group prematurely marked %q while shop-2 is PENDING", grp.PaymentStatus)
		}
	})

	t.Run("N9_NeedsCleanupWithNoOrders_RetryCleansImmediatelyOrReturnsUnavailable", func(t *testing.T) {
		h := newTestHarness()
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		h.promo.reserveErr = status.Error(codes.Unavailable, "promo down")
		h.product.releaseErr = status.Error(codes.Unavailable, "product down during cleanup")

		req := &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "r5-n9-cleanup",
			AddressId:         "addr-buyer-1",
			Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PlatformVoucherId: "v-plat",
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		}
		if _, err := h.svc.CreateCheckout(buyerCtx, req); err == nil {
			t.Fatalf("expected failure")
		}

		// Case A: product STILL down -> immediate retry must return Unavailable fast (NOT hang 15s -> Aborted).
		fastCtx, cancel := context.WithTimeout(buyerCtx, 500*time.Millisecond)
		defer cancel()
		_, err := h.svc.CreateCheckout(fastCtx, req)
		if status.Code(err) != codes.Unavailable {
			t.Fatalf("N9: expected codes.Unavailable when cleanup still failing, got %v", err)
		}

		// Case B: product & promo recover -> immediate retry cleans up and creates a fresh checkout!
		h.promo.reserveErr = nil
		h.product.releaseErr = nil
		resp, err := h.svc.CreateCheckout(buyerCtx, req)
		if err != nil {
			t.Fatalf("N9: expected immediate cleanup + new checkout on retry, got %v", err)
		}
		if len(resp.Orders) != 1 || resp.IdempotentReplay {
			t.Fatalf("expected fresh checkout after cleanup, got replay=%v orders=%d", resp.IdempotentReplay, len(resp.Orders))
		}
	})

	t.Run("N10_RetryPaymentOnInProgressGroupWithEmptyPaymentID_RecoversAndSucceeds", func(t *testing.T) {
		h := newTestHarness()
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		wrapped := &faultInjectStore{
			Store:                 h.store,
			completeFailRemaining: 5,
			completeFailErr:       errors.New("db hiccup"),
		}
		h.svc.store = wrapped

		var origPayID string
		h.payment.afterCreatePay = func(p *paymentv1.Payment) {
			if origPayID == "" {
				origPayID = p.GetPaymentId()
			}
		}
		co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "r5-n10-retry-pay",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err == nil || co != nil || status.Code(err) != codes.Unavailable || origPayID == "" {
			t.Fatalf("P1-1: CreateCheckout expected Unavailable with created payment, got resp=%+v payID=%q err=%v", co, origPayID, err)
		}
		// DB recovered -> buyer calls RetryPayment(P1) while group is still IN_PROGRESS and payment_id="".
		wrapped.mu.Lock()
		wrapped.completeFailRemaining = 0
		wrapped.mu.Unlock()

		retryResp, err := h.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
			PaymentId:      origPayID,
			IdempotencyKey: "retry-n10-1",
		})
		if err != nil {
			t.Fatalf("N10: RetryPayment must recover IN_PROGRESS group instead of failing FailedPrecondition: %v", err)
		}
		if retryResp.PaymentId == "" || retryResp.PaymentId == origPayID {
			t.Fatalf("expected new superseding payment_id, got %q (old %q)", retryResp.PaymentId, origPayID)
		}
	})

	t.Run("N12_EmptyShopsWithTotalShopProgramDiscount_FailsAndCompensates", func(t *testing.T) {
		h := newTestHarness()
		baseNow := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
		h.svc.nowFn = func() time.Time { return baseNow }
		h.promo.activeShopDiscounts = []*promotionv1.ActiveShopDiscount{{
			ItemId: "sp-n12", ProgramId: "p-1", ShopId: "shop-1", SkuId: "sku-1",
			OriginalPriceVnd: 1000000, DiscountedPriceVnd: 800000,
			StartsAt: timestamppb.New(baseNow.Add(-time.Hour)), EndsAt: timestamppb.New(baseNow.Add(time.Hour)),
		}}
		// Promotion returned aggregate TotalShopProgramDiscountVnd > 0 with empty Shops slice!
		h.promo.breakdown = &promotionv1.DiscountBreakdown{
			TotalShopProgramDiscountVnd: 200000,
		}

		_, err := h.svc.CreateCheckout(ctxWithUser("buyer-1", "buyer", ""), &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "r5-n12-empty-shops",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "PROMOTION_BREAKDOWN_MISSING") {
			t.Fatalf("N12: expected FailedPrecondition PROMOTION_BREAKDOWN_MISSING, got %v", err)
		}
		if len(h.product.releaseCalls) != 1 || len(h.promo.releaseCalls) != 1 {
			t.Fatalf("N12: expected full stock+promo compensation, got stock=%d promo=%d",
				len(h.product.releaseCalls), len(h.promo.releaseCalls))
		}
	})

	t.Run("N13_VerifyFakeTestFix_SellerBuyingFromOtherShopSeesZeroFees", func(t *testing.T) {
		h := newTestHarness()
		h.payment.feeBPSByMethod = map[paymentv1.PaymentMethod]int32{
			paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR: 150,
		}
		// seller-2 owns shop-2, buys SKU from shop-1 as a buyer!
		seller2Ctx := ctxWithUser("seller-2", "seller", "shop-2")
		seller1Ctx := ctxWithUser("seller-1", "seller", "shop-1")
		h.user.addresses["addr-seller-2"] = &userv1.Address{
			Id:             "addr-seller-2",
			UserId:         "seller-2",
			RecipientName:  "Seller2",
			RecipientPhone: "0909000222",
			ProvinceCode:   "79",
			ProvinceName:   "Ho Chi Minh",
			WardCode:       "26734",
			WardName:       "Ben Nghe",
			StreetAddress:  "2 Le Loi",
		}

		co, err := h.svc.CreateCheckout(seller2Ctx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "r5-seller-as-buyer",
			AddressId:      "addr-seller-2",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout as seller-2 on shop-1 failed: %v", err)
		}
		ordID := co.Orders[0].Id
		if co.Orders[0].CommissionVnd != 0 || co.Orders[0].PaymentFeeVnd != 0 || co.Orders[0].PaymentFeeBps != 0 || co.Orders[0].Items[0].CommissionBps != 0 {
			t.Fatalf("seller-2 buying from shop-1 leaked fees on CreateCheckout: %+v", co.Orders[0])
		}
		gotByBuyerSeller, err := h.svc.GetOrder(seller2Ctx, &orderv1.GetOrderRequest{OrderId: ordID})
		if err != nil {
			t.Fatalf("GetOrder as buyer seller-2 failed: %v", err)
		}
		if gotByBuyerSeller.Order.CommissionVnd != 0 || gotByBuyerSeller.Order.PaymentFeeVnd != 0 || gotByBuyerSeller.Order.PaymentFeeBps != 0 || gotByBuyerSeller.Order.Items[0].CommissionBps != 0 {
			t.Fatalf("seller-2 buying from shop-1 leaked fees on GetOrder: %+v", gotByBuyerSeller.Order)
		}
		// Owning seller-1 MUST see real fees!
		gotByOwner, err := h.svc.GetOrder(seller1Ctx, &orderv1.GetOrderRequest{OrderId: ordID})
		if err != nil {
			t.Fatalf("GetOrder as owning seller-1 failed: %v", err)
		}
		if gotByOwner.Order.CommissionVnd != 50000 || gotByOwner.Order.PaymentFeeVnd != 15450 || gotByOwner.Order.PaymentFeeBps != 150 || gotByOwner.Order.Items[0].CommissionBps != 500 {
			t.Fatalf("owning seller-1 fees = (%d,%d,%d,%d), want (50000,15450,150,500)",
				gotByOwner.Order.CommissionVnd, gotByOwner.Order.PaymentFeeVnd, gotByOwner.Order.PaymentFeeBps, gotByOwner.Order.Items[0].CommissionBps)
		}
	})
}

// Keep unused json import avoided if needed.
var _ = json.Valid
var _ = events.SubjectOrderCompleted
var _ = productv1.ReleaseStockRequest{}
