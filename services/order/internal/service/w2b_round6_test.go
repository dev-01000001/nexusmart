package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/order/internal/db"
	"github.com/nexusmart/core/services/order/internal/events"
	"github.com/nexusmart/core/services/order/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func seedR6GroupAndOrders(t *testing.T, h *testHarness, groupID, groupStatus, payMethod string, orders []store.NewOrderRecord) {
	t.Helper()
	ctx := context.Background()
	var total int64
	for _, r := range orders {
		total += r.Order.TotalPayableVND
	}
	_, err := h.store.CreateCheckoutGroup(ctx, db.CreateCheckoutGroupParams{
		ID: groupID, BuyerID: "buyer-1", IdempotencyKey: "idem-" + groupID,
		RequestHash: "h-" + groupID, Status: "IN_PROGRESS", PaymentMethod: payMethod, TotalPayableVND: total,
	})
	if err != nil {
		t.Fatalf("CreateCheckoutGroup(%s): %v", groupID, err)
	}
	_, err = h.store.PersistCheckoutBatch(ctx, store.CheckoutBatch{
		CheckoutGroupID: groupID,
		Now:             time.Now().UTC().Add(-10 * time.Minute),
		Orders:          orders,
	})
	if err != nil {
		t.Fatalf("PersistCheckoutBatch(%s): %v", groupID, err)
	}
	if groupStatus == "COMPLETED" {
		_ = h.store.CompleteCheckoutWithPayment(ctx, store.UpdateCheckoutPaymentParams{
			CheckoutGroupID: groupID, PaymentID: "pay-" + groupID, PaymentStatus: "PENDING",
		})
	} else if groupStatus == "FAILED" {
		_ = h.store.FailCheckoutGroup(ctx, groupID, "FAILED")
	}
	h.store.SetCheckoutGroupUpdatedAt(groupID, time.Now().UTC().Add(-10*time.Minute))
}

func makeR6OrderRecord(groupID, orderID, shopID, ordStatus, payMethod, payStatus string, payable int64) store.NewOrderRecord {
	return store.NewOrderRecord{
		Order: db.InsertOrderParams{
			ID: orderID, OrderCode: "NM-" + orderID, CheckoutGroupID: groupID,
			BuyerID: "buyer-1", ShopID: shopID, Status: ordStatus,
			PaymentMethod: payMethod, PaymentStatus: payStatus,
			SubtotalVND: payable, ShippingFeeVND: 0, CommissionVND: (payable * 500) / 10000,
			TotalPayableVND: payable, CarrierCode: "ghn", ServiceCode: "standard",
			ShippingAddressJSON: []byte(`{"recipient_name":"Buyer","province_code":"79","ward_code":"26734"}`),
		},
		Items: []db.InsertOrderItemParams{{
			ID: "it-" + orderID, OrderID: orderID, SKUID: "sku-1",
			ProductID: "prod-1", ProductName: "Phone", SKUName: "Std",
			UnitPriceVND: payable, EffectiveUnitPriceVND: payable, Quantity: 1,
			LineTotalVND: payable, WeightGrams: 500, CommissionRateBPS: 500,
		}},
	}
}

// TestW2B_R6_1_M1_BlockShippingWhileGroupInProgressAndLegacyGuard covers item 1 (P0 M1):
//   - SellerShipOrder (and admin SellerShipOrder) rejected with FailedPrecondition PAYMENT_NOT_INITIALIZED
//     BEFORE CreateShipment when checkout_groups.status == 'IN_PROGRESS'.
//   - SellerConfirmOrder (CONFIRMED -> PREPARING) is NOT blocked.
//   - HandleShipmentUpdated (FORWARD), BuyerConfirmReceived, RequestReturn blocked while group IN_PROGRESS.
//   - reconcileIncompleteCheckoutGroup seeing legacy DELIVERED/COMPLETED/RETURNED does NOT call CreatePayment,
//     returns error (manual intervention), and does NOT close group.
func TestW2B_R6_1_M1_BlockShippingWhileGroupInProgressAndLegacyGuard(t *testing.T) {
	t.Run("SellerShipOrderBlockedBeforeCreateShipment_SellerConfirmAllowed", func(t *testing.T) {
		h := newTestHarness()
		sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
		adminCtx := ctxWithUser("admin-1", "admin", "")
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")

		seedR6GroupAndOrders(t, h, "grp-m1-inprog", "IN_PROGRESS", "COD", []store.NewOrderRecord{
			makeR6OrderRecord("grp-m1-inprog", "ord-m1-1", "shop-1", "CONFIRMED", "COD", "PENDING", 500000),
		})

		// SellerConfirmOrder (CONFIRMED -> PREPARING) MUST still succeed!
		if _, err := h.svc.SellerConfirmOrder(sellerCtx, &orderv1.SellerConfirmOrderRequest{
			OrderId: "ord-m1-1", ShopId: "shop-1",
		}); err != nil {
			t.Fatalf("M1: SellerConfirmOrder must NOT be blocked while group IN_PROGRESS, got %v", err)
		}

		// SellerShipOrder MUST be blocked with FailedPrecondition PAYMENT_NOT_INITIALIZED BEFORE CreateShipment!
		for name, ctx := range map[string]context.Context{"seller": sellerCtx, "admin": adminCtx} {
			_, err := h.svc.SellerShipOrder(ctx, &orderv1.SellerShipOrderRequest{
				OrderId: "ord-m1-1", ShopId: "shop-1",
			})
			if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "PAYMENT_NOT_INITIALIZED") {
				t.Fatalf("M1 (%s): expected FailedPrecondition PAYMENT_NOT_INITIALIZED, got %v", name, err)
			}
		}
		if len(h.shipping.createShipCalls) != 0 {
			t.Fatalf("M1: CreateShipment must NOT be called when group IN_PROGRESS, got %d calls", len(h.shipping.createShipCalls))
		}

		// HandleShipmentUpdated (FORWARD delivering) MUST also fail with PAYMENT_NOT_INITIALIZED while IN_PROGRESS!
		if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-m1-ship", events.ShipmentUpdatedPayload{
			OrderID: "ord-m1-1", Status: "delivering", TrackingCode: "TRK-1",
		}); status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "PAYMENT_NOT_INITIALIZED") {
			t.Fatalf("M1: HandleShipmentUpdated(FORWARD) must fail with PAYMENT_NOT_INITIALIZED, got %v", err)
		}

		// Seed a legacy DELIVERED order in IN_PROGRESS group -> BuyerConfirmReceived & RequestReturn also blocked!
		seedR6GroupAndOrders(t, h, "grp-m1-del", "IN_PROGRESS", "COD", []store.NewOrderRecord{
			makeR6OrderRecord("grp-m1-del", "ord-m1-del", "shop-1", "DELIVERED", "COD", "PENDING", 500000),
		})
		if _, err := h.svc.BuyerConfirmReceived(buyerCtx, &orderv1.BuyerConfirmReceivedRequest{
			OrderId: "ord-m1-del",
		}); status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "PAYMENT_NOT_INITIALIZED") {
			t.Fatalf("M1: BuyerConfirmReceived must fail with PAYMENT_NOT_INITIALIZED, got %v", err)
		}
		if _, err := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{
			OrderId: "ord-m1-del", Reason: "damaged",
		}); status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "PAYMENT_NOT_INITIALIZED") {
			t.Fatalf("M1: RequestReturn must fail with PAYMENT_NOT_INITIALIZED, got %v", err)
		}
	})

	t.Run("ReconcileIncompleteGroup_LegacyDeliveredCompletedReturned_NoCreatePaymentNoClose", func(t *testing.T) {
		for _, legacyStatus := range []string{"DELIVERED", "COMPLETED", "RETURNED"} {
			t.Run(legacyStatus, func(t *testing.T) {
				h := newTestHarness()
				h.svc.cfg.StaleCheckoutAfter = 1 * time.Minute
				gid := "grp-m1-legacy-" + legacyStatus
				seedR6GroupAndOrders(t, h, gid, "IN_PROGRESS", "COD", []store.NewOrderRecord{
					makeR6OrderRecord(gid, "ord-"+legacyStatus, "shop-1", legacyStatus, "COD", "PENDING", 500000),
				})
				h.payment.onGetPayment = func(_ *paymentv1.GetPaymentRequest) (*paymentv1.GetPaymentResponse, error) {
					return nil, status.Error(codes.NotFound, "not found")
				}

				handled, err := h.svc.ScanIncompleteCheckoutGroups(context.Background())
				if err != nil {
					t.Fatalf("ScanIncompleteCheckoutGroups err: %v", err)
				}
				if handled != 0 {
					t.Fatalf("M1: legacy %s group must return error (handled=0, manual intervention), got handled=%d", legacyStatus, handled)
				}
				if len(h.payment.createCalls) != 0 {
					t.Fatalf("M1: must NOT call CreatePayment for legacy %s order, got %d calls", legacyStatus, len(h.payment.createCalls))
				}
				grp, _ := h.store.GetCheckoutGroupByID(context.Background(), gid)
				if grp.Status != "IN_PROGRESS" {
					t.Fatalf("M1: must NOT close legacy %s group, got status=%q", legacyStatus, grp.Status)
				}
			})
		}
	})
}

// TestW2B_R6_2_M2_ForwardRecoveryExcludesCancelledAndHandlesMismatch covers item 2 (P1 M2):
//   - Mixed CONFIRMED + CANCELLED group only sends live (non-CANCELLED) orders & live total to CreatePayment.
//   - If an earlier slow CreatePayment committed with original full amount -> CreatePayment returns mismatch error
//     -> next tick's GetPayment finds the committed payment and finalizes cleanly!
func TestW2B_R6_2_M2_ForwardRecoveryExcludesCancelledAndHandlesMismatch(t *testing.T) {
	t.Run("MixedConfirmedAndCancelled_CreatesPaymentOnlyForLiveOrders", func(t *testing.T) {
		h := newTestHarness()
		h.svc.cfg.StaleCheckoutAfter = 1 * time.Minute
		seedR6GroupAndOrders(t, h, "grp-m2-mix", "IN_PROGRESS", "COD", []store.NewOrderRecord{
			makeR6OrderRecord("grp-m2-mix", "ord-m2-live", "shop-1", "CONFIRMED", "COD", "PENDING", 1000000),
			makeR6OrderRecord("grp-m2-mix", "ord-m2-canc", "shop-2", "CANCELLED", "COD", "CANCELLED", 500000),
		})
		h.payment.onGetPayment = func(_ *paymentv1.GetPaymentRequest) (*paymentv1.GetPaymentResponse, error) {
			return nil, status.Error(codes.NotFound, "not found")
		}

		handled, err := h.svc.ScanIncompleteCheckoutGroups(context.Background())
		if err != nil || handled != 1 {
			t.Fatalf("ScanIncompleteCheckoutGroups = %d, %v; want 1, nil", handled, err)
		}
		if len(h.payment.createCalls) != 1 {
			t.Fatalf("expected 1 CreatePayment call, got %d", len(h.payment.createCalls))
		}
		req := h.payment.createCalls[0]
		if req.GetAmountVnd() != 1000000 {
			t.Fatalf("M2: CreatePayment amount_vnd = %d, want 1000000 (excluding CANCELLED 500k)", req.GetAmountVnd())
		}
		if len(req.GetShopAllocations()) != 1 || req.GetShopAllocations()[0].GetOrderId() != "ord-m2-live" {
			t.Fatalf("M2: ShopAllocations must contain only live ord-m2-live, got %+v", req.GetShopAllocations())
		}
		grp, _ := h.store.GetCheckoutGroupByID(context.Background(), "grp-m2-mix")
		if grp.Status != "COMPLETED" || grp.TotalPayableVND != 1000000 {
			t.Fatalf("M2: group status=%s total=%d, want COMPLETED / 1000000", grp.Status, grp.TotalPayableVND)
		}
	})

	t.Run("AmountMismatchWithLateFirstCreatePayment_NextTickGetPaymentFinalizes", func(t *testing.T) {
		h := newTestHarness()
		h.svc.cfg.StaleCheckoutAfter = 1 * time.Minute
		seedR6GroupAndOrders(t, h, "grp-m2-mismatch", "IN_PROGRESS", "COD", []store.NewOrderRecord{
			makeR6OrderRecord("grp-m2-mismatch", "ord-m2-m1", "shop-1", "CONFIRMED", "COD", "PENDING", 1000000),
			makeR6OrderRecord("grp-m2-mismatch", "ord-m2-m2", "shop-2", "CANCELLED", "COD", "CANCELLED", 500000),
		})
		tick := 0
		h.payment.onGetPayment = func(req *paymentv1.GetPaymentRequest) (*paymentv1.GetPaymentResponse, error) {
			tick++
			if tick == 1 {
				return nil, status.Error(codes.NotFound, "first create_payment still committing")
			}
			// Tick 2: earlier CreatePayment(1,500,000) is now visible!
			return &paymentv1.GetPaymentResponse{
				Payment: &paymentv1.Payment{
					PaymentId:    "pay-late-first",
					OrderGroupId: req.GetOrderGroupId(),
					AmountVnd:    1500000,
					Status:       paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING,
				},
			}, nil
		}
		h.payment.createErr = status.Error(codes.AlreadyExists, "idempotency key reused with mismatched amount")

		// Tick 1: CreatePayment fails with mismatch -> skipped cleanly, group stays IN_PROGRESS.
		h1, _ := h.svc.ScanIncompleteCheckoutGroups(context.Background())
		if h1 != 0 {
			t.Fatalf("tick 1 should skip on mismatch error, got handled=%d", h1)
		}
		// Tick 2: GetPayment finds late-committed payment -> completes group!
		h2, err := h.svc.ScanIncompleteCheckoutGroups(context.Background())
		if err != nil || h2 != 1 {
			t.Fatalf("tick 2 should recover via GetPayment, got handled=%d err=%v", h2, err)
		}
		grp, _ := h.store.GetCheckoutGroupByID(context.Background(), "grp-m2-mismatch")
		if grp.Status != "COMPLETED" || grp.PaymentID != "pay-late-first" {
			t.Fatalf("expected COMPLETED/pay-late-first on tick 2, got %+v", grp)
		}
	})
}

// TestW2B_R6_3_M3_ClaimFirstStaleCleanupAndRetry covers item 3 (P1 M3):
//   - cleanupStaleCheckoutGroup claims FAILED + needs_cleanup=TRUE BEFORE ReleasePromotion/ReleaseStock.
//   - If ReleaseStock fails -> group stays FAILED + needs_cleanup=TRUE, and next ScanStaleCheckoutGroups picks it up and clears needs_cleanup=FALSE.
//   - If group already has orders -> cleanupStaleCheckoutGroup returns nil and does NOT release promo/stock or flip FAILED.
func TestW2B_R6_3_M3_ClaimFirstStaleCleanupAndRetry(t *testing.T) {
	t.Run("ClaimsFailedNeedsCleanupBeforeRelease_RetriesUntilDone", func(t *testing.T) {
		h := newTestHarness()
		ctx := context.Background()
		gid := "grp-m3-claim"
		_, _ = h.store.CreateCheckoutGroup(ctx, db.CreateCheckoutGroupParams{
			ID: gid, BuyerID: "buyer-1", IdempotencyKey: "idem-m3", RequestHash: "h",
			Status: "IN_PROGRESS", PaymentMethod: "VIETQR",
		})
		h.store.SetCheckoutGroupUpdatedAt(gid, time.Now().UTC().Add(-10*time.Minute))

		// Verify group is ALREADY marked FAILED + needs_cleanup=TRUE AT THE MOMENT ReleasePromotion runs!
		var grpStatusDuringRelease string
		var needsCleanupDuringRelease bool
		h.promo.onRelease = func(_ string) {
			g, _ := h.store.GetCheckoutGroupByID(ctx, gid)
			grpStatusDuringRelease = g.Status
			needsCleanupDuringRelease = g.NeedsCleanup
		}
		// Inject ReleaseStock failure on sweep 1.
		h.product.releaseErr = status.Error(codes.Unavailable, "product down")

		h1, err := h.svc.ScanStaleCheckoutGroups(ctx)
		if err != nil || h1 != 0 {
			t.Fatalf("sweep 1 = %d, %v; want 0, nil", h1, err)
		}
		if grpStatusDuringRelease != "FAILED" || !needsCleanupDuringRelease {
			t.Fatalf("M3 violation: group must be claimed (FAILED + needs_cleanup=TRUE) BEFORE ReleasePromotion! got status=%q needs_cleanup=%v",
				grpStatusDuringRelease, needsCleanupDuringRelease)
		}
		gMid, _ := h.store.GetCheckoutGroupByID(ctx, gid)
		if gMid.Status != "FAILED" || !gMid.NeedsCleanup {
			t.Fatalf("M3: after ReleaseStock error, group must stay FAILED + needs_cleanup=TRUE, got %+v", gMid)
		}

		// Sweep 2 after product recovers: FindStaleInProgressCheckoutGroups MUST pick up FAILED + needs_cleanup=TRUE and clear needs_cleanup!
		h.product.releaseErr = nil
		h2, err := h.svc.ScanStaleCheckoutGroups(ctx)
		if err != nil || h2 != 1 {
			t.Fatalf("M3: sweep 2 must pick up FAILED+needs_cleanup=TRUE group, got handled=%d err=%v", h2, err)
		}
		gAfter, _ := h.store.GetCheckoutGroupByID(ctx, gid)
		if gAfter.Status != "FAILED" || gAfter.NeedsCleanup {
			t.Fatalf("M3: after sweep 2 succeeds, group must be FAILED + needs_cleanup=FALSE, got %+v", gAfter)
		}
	})

	t.Run("GroupWithOrders_CleanupDoesNothing", func(t *testing.T) {
		h := newTestHarness()
		ctx := context.Background()
		seedR6GroupAndOrders(t, h, "grp-m3-has-ord", "IN_PROGRESS", "COD", []store.NewOrderRecord{
			makeR6OrderRecord("grp-m3-has-ord", "ord-m3-1", "shop-1", "CONFIRMED", "COD", "PENDING", 500000),
		})
		if err := h.svc.cleanupStaleCheckoutGroup(ctx, "grp-m3-has-ord"); err != nil {
			t.Fatalf("expected nil when group has orders, got %v", err)
		}
		if len(h.promo.releaseCalls) != 0 || len(h.product.releaseCalls) != 0 {
			t.Fatalf("M3 violation: released resources on group with orders! promo=%d stock=%d",
				len(h.promo.releaseCalls), len(h.product.releaseCalls))
		}
		g, _ := h.store.GetCheckoutGroupByID(ctx, "grp-m3-has-ord")
		if g.Status != "IN_PROGRESS" {
			t.Fatalf("M3 violation: flipped group with orders to %q", g.Status)
		}
	})
}

// TestW2B_R6_4_M4_CancelGroupOrdersRefusesCompletedGroup covers item 4 (P1 M4):
// CancelCheckoutGroupOrders on a COMPLETED group cancels 0 orders and returns HasLiveOrders=true.
func TestW2B_R6_4_M4_CancelGroupOrdersRefusesCompletedGroup(t *testing.T) {
	h := newTestHarness()
	ctx := context.Background()
	seedR6GroupAndOrders(t, h, "grp-m4-done", "COMPLETED", "VIETQR", []store.NewOrderRecord{
		makeR6OrderRecord("grp-m4-done", "ord-m4-1", "shop-1", "PENDING", "VIETQR", "PENDING", 500000),
	})

	res, err := h.store.CancelCheckoutGroupOrders(ctx, store.CancelGroupOrdersParams{
		CheckoutGroupID: "grp-m4-done",
		Reason:          "late watchdog cancel",
		ActorID:         "watchdog",
		ActorRole:       "system",
		PaymentStatus:   "FAILED",
		OnlyPending:     true,
	})
	if err != nil {
		t.Fatalf("CancelCheckoutGroupOrders: %v", err)
	}
	if !res.HasLiveOrders || len(res.Cancelled) != 0 {
		t.Fatalf("M4 violation: COMPLETED group must cancel 0 orders and return HasLiveOrders=true, got hasLive=%v cancelled=%d",
			res.HasLiveOrders, len(res.Cancelled))
	}
	agg, _ := h.store.GetOrderWithDetails(ctx, "ord-m4-1")
	if agg.Order.Status != "PENDING" {
		t.Fatalf("M4 violation: PENDING order in COMPLETED group was mutated to %q", agg.Order.Status)
	}

	// Also test OnlyPending=false on COMPLETED COD group (directly isolates grp.Status != IN_PROGRESS guard).
	seedR6GroupAndOrders(t, h, "grp-m4-cod", "COMPLETED", "COD", []store.NewOrderRecord{
		makeR6OrderRecord("grp-m4-cod", "ord-m4-cod", "shop-1", "CONFIRMED", "COD", "PENDING", 500000),
	})
	resCOD, err := h.store.CancelCheckoutGroupOrders(ctx, store.CancelGroupOrdersParams{
		CheckoutGroupID: "grp-m4-cod",
		Reason:          "late cancel on completed COD",
		ActorID:         "sys",
		ActorRole:       "system",
		PaymentStatus:   "FAILED",
		OnlyPending:     false,
	})
	if err != nil {
		t.Fatalf("CancelCheckoutGroupOrders(COD): %v", err)
	}
	if !resCOD.HasLiveOrders || len(resCOD.Cancelled) != 0 {
		t.Fatalf("M4 violation (OnlyPending=false): COMPLETED COD group cancelled %d orders (hasLive=%v)", len(resCOD.Cancelled), resCOD.HasLiveOrders)
	}
	codAgg, _ := h.store.GetOrderWithDetails(ctx, "ord-m4-cod")
	if codAgg.Order.Status != "CONFIRMED" {
		t.Fatalf("M4 violation: COMPLETED COD order mutated to %q", codAgg.Order.Status)
	}
}

// TestW2B_R6_5_M5_ReconcileObservedErrorReturnsUnavailable covers item 5 (P2 M5):
// After CreatePayment ambiguous error + GetPayment probe returns SUCCEEDED, if reconcileObservedPaymentStatus fails,
// CreateCheckout MUST return Unavailable immediately and MUST NOT call CompleteCheckoutWithPayment!
func TestW2B_R6_5_M5_ReconcileObservedErrorReturnsUnavailable(t *testing.T) {
	h := newTestHarness()
	// Make ConfirmStock (inside HandlePaymentResult -> reconcileObservedPaymentStatus) fail!
	h.product.confirmErr = status.Error(codes.Unavailable, "product confirm down")
	h.payment.createErr = status.Error(codes.Unavailable, "eof after create")
	h.payment.onGetPayment = func(req *paymentv1.GetPaymentRequest) (*paymentv1.GetPaymentResponse, error) {
		return &paymentv1.GetPaymentResponse{
			Payment: &paymentv1.Payment{
				PaymentId:    "pay-m5-succ",
				OrderGroupId: req.GetOrderGroupId(),
				Status:       paymentv1.PaymentStatus_PAYMENT_STATUS_SUCCEEDED,
				AmountVnd:    1030000,
			},
		}, nil
	}

	_, err := h.svc.CreateCheckout(ctxWithUser("buyer-1", "buyer", ""), &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "r6-m5-reconcile-err",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
	})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("M5: expected codes.Unavailable when reconcileObservedPaymentStatus fails, got %v", err)
	}
	lookup, gErr := h.store.GetCheckoutGroupForIdempotency(context.Background(), "buyer-1", "r6-m5-reconcile-err")
	if gErr != nil || lookup.Group.Status != "IN_PROGRESS" {
		t.Fatalf("M5 violation: group must stay IN_PROGRESS (NOT COMPLETED) when reconcile fails, got err=%v lookup=%+v", gErr, lookup)
	}
}

// TestW2B_R6_6_M6_DetachedCreatePaymentAndCODAmbiguousDefer covers item 6 (P2 M6):
//   - CreatePayment runs with detachedCallerContext (survives client cancel while keeping buyer JWT).
//   - COD / 0-VND with ambiguous CreatePayment + probe NotFound returns Unavailable without cancelling!
//   - Definite CreatePayment error on COD still cancels as before.
func TestW2B_R6_6_M6_DetachedCreatePaymentAndCODAmbiguousDefer(t *testing.T) {
	t.Run("CODAmbiguousAndProbeNotFound_ReturnsUnavailableWithoutCancelling", func(t *testing.T) {
		h := newTestHarness()
		h.payment.createErr = status.Error(codes.Unavailable, "payment timeout")
		h.payment.onGetPayment = func(_ *paymentv1.GetPaymentRequest) (*paymentv1.GetPaymentResponse, error) {
			return nil, status.Error(codes.NotFound, "not found yet")
		}

		_, err := h.svc.CreateCheckout(ctxWithUser("buyer-1", "buyer", ""), &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "r6-m6-cod-ambig",
			AddressId:         "addr-buyer-1",
			Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PlatformVoucherId: "v-plat",
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if status.Code(err) != codes.Unavailable {
			t.Fatalf("M6: expected codes.Unavailable for COD ambiguous+NotFound, got %v", err)
		}
		if len(h.product.releaseCalls) != 0 || len(h.promo.releaseCalls) != 0 {
			t.Fatalf("M6 violation: must NOT cancel/release COD order on ambiguous+NotFound! stock=%d promo=%d",
				len(h.product.releaseCalls), len(h.promo.releaseCalls))
		}
		lookup, gErr := h.store.GetCheckoutGroupForIdempotency(context.Background(), "buyer-1", "r6-m6-cod-ambig")
		if gErr != nil || lookup.Group.Status != "IN_PROGRESS" {
			t.Fatalf("M6: group must remain IN_PROGRESS for watchdog forward recovery, got err=%v lookup=%+v", gErr, lookup)
		}
		aggs, _ := h.store.ListOrdersByCheckoutGroup(context.Background(), lookup.Group.ID)
		if len(aggs) != 1 || aggs[0].Order.Status != "CONFIRMED" {
			t.Fatalf("M6: COD order must remain CONFIRMED, got %+v", aggs)
		}
	})

	t.Run("CreatePaymentUsesDetachedCallerContext_KeepsBuyerJWTAndSurvivesCancel", func(t *testing.T) {
		h := newTestHarness()
		baseCtx := auth.WithRawToken(ctxWithUser("buyer-1", "buyer", ""), "buyer-jwt-secret-token")
		reqCtx, cancelClient := context.WithCancel(baseCtx)
		defer cancelClient()
		// Cancel client context right inside ReserveStock (before PersistCheckoutBatch & CreatePayment).
		h.product.onReserve = func(_ context.Context, _ *productv1.ReserveStockRequest) {
			cancelClient()
		}
		h.payment.onCreatePayCtx = func(ctx context.Context, _ *paymentv1.CreatePaymentRequest) {
			if ctx.Err() != nil {
				t.Errorf("M6: CreatePayment received canceled context: %v", ctx.Err())
			}
		}

		resp, err := h.svc.CreateCheckout(reqCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "r6-m6-detached-pay",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil {
			t.Fatalf("M6: CreateCheckout should succeed via detachedCallerContext even after client cancel, got %v", err)
		}
		if resp.PaymentId == "" || len(h.payment.createCalls) != 1 {
			t.Fatalf("M6: expected 1 CreatePayment call with payment_id, got calls=%d pay=%q", len(h.payment.createCalls), resp.PaymentId)
		}
		if len(h.payment.createCtxErrs) != 1 || h.payment.createCtxErrs[0] != nil {
			t.Fatalf("M6: CreatePayment ctx.Err = %v, want nil", h.payment.createCtxErrs)
		}
		if len(h.payment.createAuth) != 1 || h.payment.createAuth[0].role != "buyer" || h.payment.createAuth[0].service != "" || h.payment.createAuth[0].rawToken != "buyer-jwt-secret-token" {
			t.Fatalf("M6: detachedCallerContext must preserve buyer JWT (not service token), got %+v", h.payment.createAuth)
		}
	})

	t.Run("DefiniteCreatePaymentErrorOnCOD_StillCancels", func(t *testing.T) {
		h := newTestHarness()
		h.payment.createErr = status.Error(codes.InvalidArgument, "invalid payment payload")

		_, err := h.svc.CreateCheckout(ctxWithUser("buyer-1", "buyer", ""), &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "r6-m6-cod-definite",
			AddressId:         "addr-buyer-1",
			Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PlatformVoucherId: "v-plat",
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("M6: expected InvalidArgument for definite CreatePayment error, got %v", err)
		}
		if len(h.product.releaseCalls) != 1 || len(h.promo.releaseCalls) != 1 {
			t.Fatalf("M6: definite error on COD must still cancel and release stock/promo! stock=%d promo=%d",
				len(h.product.releaseCalls), len(h.promo.releaseCalls))
		}
	})

	t.Run("VietQRAmbiguousAndProbeNotFound_StillCancels", func(t *testing.T) {
		h := newTestHarness()
		h.payment.createErr = status.Error(codes.Unavailable, "payment timeout")
		h.payment.onGetPayment = func(_ *paymentv1.GetPaymentRequest) (*paymentv1.GetPaymentResponse, error) {
			return nil, status.Error(codes.NotFound, "not found")
		}

		_, err := h.svc.CreateCheckout(ctxWithUser("buyer-1", "buyer", ""), &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "r6-m6-vietqr-ambig",
			AddressId:         "addr-buyer-1",
			Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PlatformVoucherId: "v-plat",
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if status.Code(err) != codes.Unavailable {
			t.Fatalf("M6: expected Unavailable for VietQR ambiguous+NotFound, got %v", err)
		}
		if len(h.product.releaseCalls) != 1 || len(h.promo.releaseCalls) != 1 {
			t.Fatalf("M6: VietQR PENDING ambiguous+NotFound must still cancel and release stock/promo! stock=%d promo=%d",
				len(h.product.releaseCalls), len(h.promo.releaseCalls))
		}
	})
}

// TestW2B_R6_7_M7_ZeroSubOrderInMultiShopVietQRAndP3 covers item 7 (P2 M7) & item 8 (P3 a, b, c).
func TestW2B_R6_7_M7_ZeroSubOrderInMultiShopVietQRAndP3(t *testing.T) {
	t.Run("M7_CancelPendingZeroSubOrderInMultiShopVietQR_CallsCancelPayment", func(t *testing.T) {
		h := newTestHarness()
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		// Multi-shop VietQR group: shop-1 has TotalPayableVND=0, shop-2 has TotalPayableVND=100,000; both PENDING!
		seedR6GroupAndOrders(t, h, "grp-m7-multi", "IN_PROGRESS", "VIETQR", []store.NewOrderRecord{
			makeR6OrderRecord("grp-m7-multi", "ord-m7-zero", "shop-1", "PENDING", "VIETQR", "PENDING", 0),
			makeR6OrderRecord("grp-m7-multi", "ord-m7-pos", "shop-2", "PENDING", "VIETQR", "PENDING", 100000),
		})

		if _, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{
			OrderId: "ord-m7-zero", Reason: "cancel from 0-vnd sub-order",
		}); err != nil {
			t.Fatalf("CancelOrder(ord-m7-zero): %v", err)
		}
		if len(h.payment.cancelCalls) != 1 || h.payment.cancelCalls[0].GetOrderGroupId() != "grp-m7-multi" {
			t.Fatalf("M7 violation: cancelling PENDING 0-VND sub-order skipped CancelPayment! calls=%+v", h.payment.cancelCalls)
		}

		// CompleteCheckoutWithPayment with payment_id emits fresh order.cancelled outbox events for both cancelled sub-orders (P0-1).
		beforeOutbox := len(h.store.OutboxEvents())
		if err := h.store.CompleteCheckoutWithPayment(context.Background(), store.UpdateCheckoutPaymentParams{
			CheckoutGroupID: "grp-m7-multi", PaymentID: "pay-m7", PaymentStatus: "PENDING",
		}); err != nil {
			t.Fatalf("CompleteCheckoutWithPayment: %v", err)
		}
		afterEvents := h.store.OutboxEvents()[beforeOutbox:]
		if len(afterEvents) != 2 {
			t.Fatalf("P0-1: expected 2 re-emitted order.cancelled events for grp-m7-multi, got %d", len(afterEvents))
		}
	})

	t.Run("P3a_CancelCheckoutGroupOrders_ZeroVNDOrderSetsPaymentStatusCancelled", func(t *testing.T) {
		h := newTestHarness()
		ctx := context.Background()
		seedR6GroupAndOrders(t, h, "grp-p3a-zero", "IN_PROGRESS", "VIETQR", []store.NewOrderRecord{
			makeR6OrderRecord("grp-p3a-zero", "ord-p3a-zero", "shop-1", "CONFIRMED", "VIETQR", "SUCCEEDED", 0),
		})
		res, err := h.store.CancelCheckoutGroupOrders(ctx, store.CancelGroupOrdersParams{
			CheckoutGroupID: "grp-p3a-zero", Reason: "definite createpayment fail",
			ActorID: "sys", ActorRole: "system", PaymentStatus: "FAILED",
		})
		if err != nil || res.HasLiveOrders || len(res.Cancelled) != 1 {
			t.Fatalf("CancelCheckoutGroupOrders: err=%v res=%+v", err, res)
		}
		agg, _ := h.store.GetOrderWithDetails(ctx, "ord-p3a-zero")
		if agg.Order.PaymentStatus != "CANCELLED" {
			t.Fatalf("P3(a): cancelled 0-VND order payment_status = %q, want CANCELLED (matching TransitionOrderStatus)", agg.Order.PaymentStatus)
		}
	})

	t.Run("P3b_RetryPayment_RecoveryErrorReturnsUnavailable", func(t *testing.T) {
		h := newTestHarness()
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		seedR6GroupAndOrders(t, h, "grp-p3b", "IN_PROGRESS", "VIETQR", []store.NewOrderRecord{
			makeR6OrderRecord("grp-p3b", "ord-p3b", "shop-1", "PENDING", "VIETQR", "PENDING", 500000),
		})
		call := 0
		h.payment.onGetPayment = func(req *paymentv1.GetPaymentRequest) (*paymentv1.GetPaymentResponse, error) {
			call++
			if call == 1 {
				// Initial lookup by PaymentId succeeds so RetryPayment locates grp-p3b.
				return &paymentv1.GetPaymentResponse{
					Payment: &paymentv1.Payment{
						PaymentId: "pay-p3b", OrderGroupId: "grp-p3b", UserId: "buyer-1",
						Status: paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING,
					},
				}, nil
			}
			// Recovery lookup by OrderGroupId fails transiently!
			return nil, status.Error(codes.Unavailable, "payment temporarily unavailable")
		}
		_, err := h.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
			PaymentId: "pay-p3b", IdempotencyKey: "rk-p3b",
		})
		if status.Code(err) != codes.Unavailable {
			t.Fatalf("P3(b): expected codes.Unavailable when RetryPayment recovery fails, got %v", err)
		}
	})

	t.Run("P3c_CompensateCancelledCheckoutGroup_GetOrderWithDetailsErrKeepsFlag", func(t *testing.T) {
		h := newTestHarness()
		ctx := context.Background()
		seedR6GroupAndOrders(t, h, "grp-p3c", "IN_PROGRESS", "COD", []store.NewOrderRecord{
			makeR6OrderRecord("grp-p3c", "ord-p3c", "shop-1", "CONFIRMED", "COD", "PENDING", 500000),
		})
		res, _ := h.store.CancelCheckoutGroupOrders(ctx, store.CancelGroupOrdersParams{
			CheckoutGroupID: "grp-p3c", Reason: "fail", ActorID: "sys", ActorRole: "system", PaymentStatus: "FAILED",
		})
		wrapped := &r6FailGetOrderStore{Store: h.store, getOrderErr: errors.New("db read error")}
		h.svc.store = wrapped
		h.product.releaseReturnsOk = false

		// Pass empty allAggs so compensateCancelledCheckoutGroup falls back to GetOrderWithDetails (which fails!).
		h.svc.compensateCancelledCheckoutGroup(ctx, "grp-p3c", res.Cancelled, nil, false)

		agg, _ := h.store.GetOrderWithDetails(ctx, "ord-p3c")
		if !agg.Order.PendingCancelCompensation {
			t.Fatalf("P3(c): PendingCancelCompensation was cleared despite GetOrderWithDetails error!")
		}
	})
}

type r6FailGetOrderStore struct {
	store.Store
	getOrderErr error
}

func (s *r6FailGetOrderStore) GetOrderWithDetails(ctx context.Context, orderID string) (*store.OrderAggregate, error) {
	if s.getOrderErr != nil {
		return nil, s.getOrderErr
	}
	return s.Store.GetOrderWithDetails(ctx, orderID)
}

var _ = productv1.ReleaseStockRequest{}
var _ = shippingv1.CreateShipmentRequest{}
