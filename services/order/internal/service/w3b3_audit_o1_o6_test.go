package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	"github.com/nexusmart/core/services/order/internal/clients"
	"github.com/nexusmart/core/services/order/internal/db"
	"github.com/nexusmart/core/services/order/internal/events"
	"github.com/nexusmart/core/services/order/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type slowRenewStore struct {
	store.Store
	delay time.Duration
}

func (s *slowRenewStore) RenewOrderAddressChangeLease(ctx context.Context, id, claimToken string, now time.Time, lease time.Duration) (*db.OrderAddressChange, error) {
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	return s.Store.RenewOrderAddressChangeLease(ctx, id, claimToken, now, lease)
}

// --- O1 (1.1): Lease fence before every side-effect RPC, deadline < claimed_until, Step 5 reload, terminal orphan guard, single-claim watchdog ---

// Scenario 3 (report 1.1): R1 stalls at Step 2 past lease expiry while watchdog finishes ch1 and creates TRK-NEW.
// With a real lease fence + ctx deadline strictly before claimed_until, R1 MUST NEVER cancel TRK-NEW.
func TestW3B3_O1_WatchdogLeaseSteal_Step2MustNotCancelNewShipment(t *testing.T) {
	h := newTestHarness()
	// Make GRPCCallTimeout large so only the lease-bounded ctx deadline (claimed_until - safety) can cut off a stalled RPC.
	h.svc.cfg.GRPCCallTimeout = 60 * time.Second

	var clockMu sync.Mutex
	now := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	h.svc.nowFn = func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return now
	}

	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
	ord := createTestCODOrder(t, h, "buyer-1", "idem-o1-step2")
	addrB := registerBuyerAddr(h, "addr-o1-b", "buyer-1", "Buyer B", "0912345678", "79", "TP. Ho Chi Minh", "26735", "Tan Dinh", "20 B St")

	var shipMu sync.Mutex
	activeTracking := ""
	cancelledTrackings := map[string]bool{}
	var shipSeq atomic.Int32
	h.shipping.onCreateShipmentCtx = func(ctx context.Context, req *shippingv1.CreateShipmentRequest) (*shippingv1.CreateShipmentResponse, error) {
		if err := ctx.Err(); err != nil {
			return nil, status.FromContextError(err).Err()
		}
		n := shipSeq.Add(1)
		trk := "TRK-OLD"
		if n > 1 {
			trk = "TRK-NEW"
		}
		shipMu.Lock()
		activeTracking = trk
		shipMu.Unlock()
		return &shippingv1.CreateShipmentResponse{
			Shipment: &shippingv1.Shipment{
				OrderId:      req.GetOrderId(),
				CarrierCode:  "ghn",
				ServiceCode:  "standard",
				TrackingCode: trk,
				LabelUrl:     "https://labels.sandbox/" + trk,
				CodAmountVnd: req.GetCodAmountVnd(),
			},
		}, nil
	}

	if _, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord.GetId(), ShopId: "shop-1"}); err != nil {
		t.Fatalf("SellerShipOrder: %v", err)
	}

	// Subcase A: R1 stalls BEFORE reaching Step 2 CancelShipment (right after Step 1) while watchdog steals lease & completes ch1.
	r1PausedAtStep1 := make(chan struct{})
	releaseR1 := make(chan struct{})
	h.svc.afterAddressChangeStep = func(step int, _ string) error {
		if step == 1 {
			close(r1PausedAtStep1)
			<-releaseR1
		}
		return nil
	}
	h.shipping.onCancelShipmentCtx = func(ctx context.Context, req *shippingv1.CancelShipmentRequest) error {
		if err := ctx.Err(); err != nil {
			return status.FromContextError(err).Err()
		}
		shipMu.Lock()
		if activeTracking != "" {
			cancelledTrackings[activeTracking] = true
			activeTracking = ""
		}
		shipMu.Unlock()
		return nil
	}

	r1RespCh := make(chan *orderv1.UpdateOrderAddressResponse, 1)
	r1ErrCh := make(chan error, 1)
	go func() {
		resp, err := h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
			OrderId:        ord.GetId(),
			AddressId:      addrB,
			IdempotencyKey: "k-o1-step2",
		})
		r1RespCh <- resp
		r1ErrCh <- err
	}()
	<-r1PausedAtStep1

	// Advance clock past lease -> watchdog steals ch1, cancels TRK-OLD, applies B, creates TRK-NEW, closes COMPLETED.
	h.svc.afterAddressChangeStep = nil
	clockMu.Lock()
	now = now.Add(31 * time.Second)
	clockMu.Unlock()

	handled, err := h.svc.ScanPendingAddressChanges(context.Background())
	if err != nil || handled != 1 {
		t.Fatalf("watchdog handled=%d err=%v", handled, err)
	}
	afterW, _ := h.store.GetOrderWithDetails(context.Background(), ord.GetId())
	if afterW.Order.TrackingCode != "TRK-NEW" {
		t.Fatalf("watchdog tracking = %q, want TRK-NEW", afterW.Order.TrackingCode)
	}

	// Wake stale R1: lease fence MUST stop R1 before calling CancelShipment so TRK-NEW stays alive!
	close(releaseR1)
	r1Resp := <-r1RespCh
	r1Err := <-r1ErrCh

	shipMu.Lock()
	newCancelled := cancelledTrackings["TRK-NEW"]
	shipMu.Unlock()
	if newCancelled {
		t.Fatalf("O1 BUG: stale R1 cancelled newly created shipment TRK-NEW")
	}
	// Also satisfies O5: buyer gets final COMPLETED result instead of 500!
	if r1Err != nil || r1Resp.GetOrder().GetTrackingCode() != "TRK-NEW" {
		t.Fatalf("O5: expected final COMPLETED response with TRK-NEW, got resp=%+v err=%v", r1Resp, r1Err)
	}

	// Subcase B (in-flight stall inside CancelShipment): ctx-aware fake blocks until ctx.Done() -> hits DeadlineExceeded
	// strictly BEFORE short lease (120ms) expires, never cancelling TRK-NEW!
	t.Run("inflight_cancel_hits_ctx_deadline_before_lease_expiry", func(t *testing.T) {
		h2 := newTestHarness()
		h2.svc.cfg.GRPCCallTimeout = 10 * time.Second
		h2.svc.addressChangeLease = 120 * time.Millisecond
		h2.svc.addressChangeLeaseSafety = 50 * time.Millisecond
		h2.svc.nowFn = func() time.Time { return time.Now().UTC() }

		ord2 := createTestCODOrder(t, h2, "buyer-1", "idem-o1-inflight")
		addr2 := registerBuyerAddr(h2, "addr-o1-inf", "buyer-1", "Buyer B", "0912345678", "79", "TP. Ho Chi Minh", "26735", "Tan Dinh", "25 Inf St")
		var active2 string
		cancelled2 := map[string]bool{}
		var mu2 sync.Mutex
		var seq2 atomic.Int32
		h2.shipping.onCreateShipmentCtx = func(ctx context.Context, req *shippingv1.CreateShipmentRequest) (*shippingv1.CreateShipmentResponse, error) {
			if err := ctx.Err(); err != nil {
				return nil, status.FromContextError(err).Err()
			}
			trk := "TRK-INF-OLD"
			if seq2.Add(1) > 1 {
				trk = "TRK-INF-NEW"
			}
			mu2.Lock()
			active2 = trk
			mu2.Unlock()
			return &shippingv1.CreateShipmentResponse{
				Shipment: &shippingv1.Shipment{OrderId: req.GetOrderId(), CarrierCode: "ghn", ServiceCode: "standard", TrackingCode: trk},
			}, nil
		}
		if _, err := h2.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord2.GetId(), ShopId: "shop-1"}); err != nil {
			t.Fatalf("SellerShipOrder: %v", err)
		}

		var cancelCall atomic.Int32
		r1HitDeadline := make(chan error, 1)
		h2.shipping.onCancelShipmentCtx = func(ctx context.Context, _ *shippingv1.CancelShipmentRequest) error {
			if cancelCall.Add(1) == 1 {
				// Simulate hung transport: real gRPC aborts when ctx.Done() fires (~70ms < 120ms lease).
				<-ctx.Done()
				err := status.FromContextError(ctx.Err()).Err()
				r1HitDeadline <- err
				return err
			}
			mu2.Lock()
			if active2 != "" {
				cancelled2[active2] = true
				active2 = ""
			}
			mu2.Unlock()
			return nil
		}

		_, err := h2.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
			OrderId: ord2.GetId(), AddressId: addr2, IdempotencyKey: "k-o1-inf",
		})
		if status.Code(err) != codes.DeadlineExceeded {
			t.Fatalf("expected DeadlineExceeded from hung CancelShipment before lease expiry, got %v", err)
		}
		select {
		case dErr := <-r1HitDeadline:
			if status.Code(dErr) != codes.DeadlineExceeded {
				t.Fatalf("expected fake to see DeadlineExceeded, got %v", dErr)
			}
		default:
			t.Fatalf("expected CancelShipment to observe ctx.Done()")
		}
		// Advance past retry backoff -> watchdog finishes cleanly and TRK-INF-NEW stays live.
		h2.svc.nowFn = func() time.Time { return time.Now().UTC().Add(10 * time.Second) }
		if handled, wErr := h2.svc.ScanPendingAddressChanges(context.Background()); wErr != nil || handled != 1 {
			t.Fatalf("watchdog after deadline: handled=%d err=%v", handled, wErr)
		}
		mu2.Lock()
		newDead := cancelled2["TRK-INF-NEW"]
		mu2.Unlock()
		if newDead {
			t.Fatalf("O1 BUG: TRK-INF-NEW was cancelled")
		}
	})
}

// Scenario 1 (report 1.1): R1 stalls at resolveShopPickupAddress in Step 5 of ch1 (B, 150k), watchdog finishes ch1,
// buyer applies ch2 (C, 180k). When R1 wakes up, it MUST NOT call CreateShipment with stale address B / 150k.
func TestW3B3_O1_WatchdogLeaseSteal_Step5MustNotCreateStaleAddressShipment(t *testing.T) {
	h := newTestHarness()
	var clockMu sync.Mutex
	now := time.Date(2026, 11, 25, 10, 10, 0, 0, time.UTC)
	h.svc.nowFn = func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return now
	}

	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
	h.product.skus["sku-1"].PriceVnd = 120000
	ord := createTestCODOrder(t, h, "buyer-1", "idem-o1-step5-stale") // subtotal 120k + ship 30k = 150k
	addrB := registerBuyerAddr(h, "addr-o1-s5-b", "buyer-1", "Buyer B", "0912345678", "79", "TP. Ho Chi Minh", "26735", "Tan Dinh", "20 Addr B")
	addrC := registerBuyerAddr(h, "addr-o1-s5-c", "buyer-1", "Buyer C", "0912345678", "01", "Ha Noi", "00001", "Hoan Kiem", "30 Addr C")

	var shipMu sync.Mutex
	var createdReqs []*shippingv1.CreateShipmentRequest
	var shipSeq atomic.Int32
	h.shipping.onCreateShipmentCtx = func(ctx context.Context, req *shippingv1.CreateShipmentRequest) (*shippingv1.CreateShipmentResponse, error) {
		if err := ctx.Err(); err != nil {
			return nil, status.FromContextError(err).Err()
		}
		shipMu.Lock()
		createdReqs = append(createdReqs, req)
		shipMu.Unlock()
		n := shipSeq.Add(1)
		trk := "TRK-S5-" + string(rune('0'+n))
		return &shippingv1.CreateShipmentResponse{
			Shipment: &shippingv1.Shipment{
				OrderId:      req.GetOrderId(),
				CarrierCode:  req.GetCarrierCode(),
				ServiceCode:  req.GetServiceCode(),
				TrackingCode: trk,
				LabelUrl:     "https://labels.sandbox/" + trk,
				CodAmountVnd: req.GetCodAmountVnd(),
			},
		}, nil
	}

	if _, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord.GetId(), ShopId: "shop-1"}); err != nil {
		t.Fatalf("SellerShipOrder: %v", err)
	}

	// Pause R1 right after Step 4 of ch1 (before Step 5 CreateShipment).
	r1PausedAtStep4 := make(chan struct{})
	releaseR1 := make(chan struct{})
	h.svc.afterAddressChangeStep = func(step int, _ string) error {
		if step == 4 {
			close(r1PausedAtStep4)
			<-releaseR1
		}
		return nil
	}

	r1Done := make(chan error, 1)
	go func() {
		_, err := h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
			OrderId:        ord.GetId(),
			AddressId:      addrB,
			IdempotencyKey: "k-o1-ch1",
		})
		r1Done <- err
	}()
	<-r1PausedAtStep4

	// Clear hook; advance past lease -> watchdog finishes ch1 (creates TRK for B).
	h.svc.afterAddressChangeStep = nil
	clockMu.Lock()
	now = now.Add(31 * time.Second)
	clockMu.Unlock()

	if handled, err := h.svc.ScanPendingAddressChanges(context.Background()); err != nil || handled != 1 {
		t.Fatalf("watchdog ch1: handled=%d err=%v", handled, err)
	}

	// Buyer now sends ch2 (B -> C, fee 60k -> total 180k) and pauses right after ch2's Step 4 (so no live shipment at shipping yet).
	h.shipping.quotes = []*shippingv1.CarrierQuote{{CarrierCode: "ghn", ServiceCode: "standard", FeeVnd: 60000, CodSupported: true}}
	ch2PausedAtStep4 := make(chan struct{})
	releaseCh2 := make(chan struct{})
	h.svc.afterAddressChangeStep = func(step int, _ string) error {
		if step == 4 {
			close(ch2PausedAtStep4)
			<-releaseCh2
		}
		return nil
	}
	ch2Done := make(chan error, 1)
	go func() {
		_, err := h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
			OrderId:                   ord.GetId(),
			AddressId:                 addrC,
			AcceptFeeIncrease:         true,
			ExpectedNewShippingFeeVnd: 60000,
			IdempotencyKey:            "k-o1-ch2",
		})
		ch2Done <- err
	}()
	<-ch2PausedAtStep4

	// Record how many CreateShipment calls happened so far (1 initial + 1 watchdog ch1 = 2).
	shipMu.Lock()
	callsBeforeR1Wake := len(createdReqs)
	shipMu.Unlock()

	// Wake stale R1! It MUST hit the lease fence and NEVER call CreateShipment(Addr B, 150k).
	close(releaseR1)
	<-r1Done

	shipMu.Lock()
	callsAfterR1Wake := len(createdReqs)
	shipMu.Unlock()
	if callsAfterR1Wake != callsBeforeR1Wake {
		t.Fatalf("O1 BUG: stale R1 called CreateShipment after losing lease (before=%d after=%d, addr=%q cod=%d)",
			callsBeforeR1Wake, callsAfterR1Wake,
			createdReqs[callsAfterR1Wake-1].GetToAddress().GetStreetAddress(),
			createdReqs[callsAfterR1Wake-1].GetCodAmountVnd())
	}

	// Finish ch2 cleanly and assert final shipment is Addr C / 180k.
	h.svc.afterAddressChangeStep = nil
	close(releaseCh2)
	if err := <-ch2Done; err != nil {
		t.Fatalf("ch2 failed: %v", err)
	}
	shipMu.Lock()
	last := createdReqs[len(createdReqs)-1]
	shipMu.Unlock()
	if last.GetToAddress().GetStreetAddress() != "30 Addr C" || last.GetCodAmountVnd() != 180000 {
		t.Fatalf("final shipment mismatch: street=%q cod=%d", last.GetToAddress().GetStreetAddress(), last.GetCodAmountVnd())
	}
}

// Scenario 2 (report 1.1): FinalizeAddressChangeShipment on an already-terminal intent when order is CANCELLED
// and params carry a non-empty TrackingCode MUST set pending_shipment_cancel=true and NeedsOrphanShipmentCancel=true.
func TestW3B3_O1_WatchdogLeaseSteal_Step5OrphanOnCancelledOrder(t *testing.T) {
	h := newTestHarness()
	var clockMu sync.Mutex
	now := time.Date(2026, 11, 25, 10, 20, 0, 0, time.UTC)
	h.svc.nowFn = func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return now
	}

	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
	ord := createTestCODOrder(t, h, "buyer-1", "idem-o1-orphan")
	addrB := registerBuyerAddr(h, "addr-o1-orph", "buyer-1", "Buyer B", "0912345678", "79", "TP. Ho Chi Minh", "26735", "Tan Dinh", "20 Orphan St")

	if _, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord.GetId(), ShopId: "shop-1"}); err != nil {
		t.Fatalf("SellerShipOrder: %v", err)
	}

	// Part 1: R1 pauses at Step 4, watchdog completes ch1, buyer cancels order; waking R1 MUST NOT create an orphan shipment.
	r1Paused := make(chan struct{})
	releaseR1 := make(chan struct{})
	h.svc.afterAddressChangeStep = func(step int, _ string) error {
		if step == 4 {
			close(r1Paused)
			<-releaseR1
		}
		return nil
	}
	r1Done := make(chan error, 1)
	go func() {
		_, err := h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
			OrderId: ord.GetId(), AddressId: addrB, IdempotencyKey: "k-o1-orph",
		})
		r1Done <- err
	}()
	<-r1Paused
	h.svc.afterAddressChangeStep = nil

	clockMu.Lock()
	now = now.Add(31 * time.Second)
	clockMu.Unlock()
	if handled, err := h.svc.ScanPendingAddressChanges(context.Background()); err != nil || handled != 1 {
		t.Fatalf("watchdog: handled=%d err=%v", handled, err)
	}
	if _, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: ord.GetId(), Reason: "cancel before R1 wakes"}); err != nil {
		t.Fatalf("CancelOrder: %v", err)
	}

	callsBeforeWake := len(h.shipping.createShipCalls)
	close(releaseR1)
	<-r1Done
	if len(h.shipping.createShipCalls) != callsBeforeWake {
		t.Fatalf("O1 BUG: stale R1 created orphan shipment on cancelled order (calls before=%d after=%d)",
			callsBeforeWake, len(h.shipping.createShipCalls))
	}

	// Part 2: Direct store contract — FinalizeAddressChangeShipment on terminal intent + CANCELLED order + TrackingCode != ""
	// MUST enable pending_shipment_cancel and return NeedsOrphanShipmentCancel=true!
	ch, err := h.store.GetOrderAddressChangeByOrderAndIdemKey(context.Background(), ord.GetId(), "k-o1-orph")
	if err != nil {
		t.Fatalf("GetOrderAddressChangeByOrderAndIdemKey: %v", err)
	}
	finRes, err := h.store.FinalizeAddressChangeShipment(context.Background(), store.FinalizeAddressChangeShipmentParams{
		ChangeID:     ch.ID,
		OrderID:      ord.GetId(),
		ClaimToken:   "stale-tok",
		TrackingCode: "TRK-ORPHAN-999",
		Now:          now,
	})
	if err != nil {
		t.Fatalf("FinalizeAddressChangeShipment: %v", err)
	}
	if !finRes.NeedsOrphanShipmentCancel || !finRes.Order.Order.PendingShipmentCancel {
		t.Fatalf("O1 BUG: terminal FinalizeAddressChangeShipment on CANCELLED order swallowed orphan shipment: needsCancel=%v pendingFlag=%v",
			finRes.NeedsOrphanShipmentCancel, finRes.Order.Order.PendingShipmentCancel)
	}
}

// O1: Every side-effect RPC receives a ctx whose deadline is strictly before claimed_until (min(GRPCCallTimeout, claimed_until - 5s)),
// a stalled ctx-aware fake hits DeadlineExceeded before lease expiry, and ScanPendingAddressChanges claims 1-by-1 with ctx.Err() guard.
func TestW3B3_O1_RPCContextDeadlineBeforeClaimedUntil_AndSingleClaimWatchdog(t *testing.T) {
	t.Run("rpc_ctx_deadline_bounded_before_claimed_until", func(t *testing.T) {
		h := newTestHarness()
		// Make GRPCCallTimeout larger than (lease - safety) so the lease-derived deadline MUST win!
		h.svc.cfg.GRPCCallTimeout = 60 * time.Second
		// Real-time clock so time.Until(deadline) matches claimed_until directly.
		h.svc.nowFn = func() time.Time { return time.Now().UTC() }

		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
		ord := createTestCODOrder(t, h, "buyer-1", "idem-o1-deadline")
		addrB := registerBuyerAddr(h, "addr-o1-dl", "buyer-1", "Buyer B", "0912345678", "01", "Ha Noi", "00001", "Hoan Kiem", "55 DL St")

		if _, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord.GetId(), ShopId: "shop-1"}); err != nil {
			t.Fatalf("SellerShipOrder: %v", err)
		}

		checkDeadline := func(rpcName string, ctx context.Context) {
			t.Helper()
			dl, ok := ctx.Deadline()
			if !ok {
				t.Errorf("%s received context with no deadline", rpcName)
				return
			}
			ch, err := h.store.GetOrderAddressChangeByOrderAndIdemKey(context.Background(), ord.GetId(), "k-o1-dl")
			if err != nil || ch.ClaimedUntil == nil {
				t.Errorf("%s could not read claimed_until: err=%v", rpcName, err)
				return
			}
			// Deadline must be at least ~4s before claimed_until (safety = 5s; allow 1s scheduling slack).
			if !dl.Before(ch.ClaimedUntil.Add(-4 * time.Second)) {
				t.Errorf("O1 BUG: %s ctx deadline %v is NOT safely before claimed_until %v (remaining diff=%v)",
					rpcName, dl, *ch.ClaimedUntil, ch.ClaimedUntil.Sub(dl))
			}
		}

		h.shipping.quotes = []*shippingv1.CarrierQuote{{CarrierCode: "ghn", ServiceCode: "standard", FeeVnd: 50000, CodSupported: true}}
		h.shipping.onCancelShipmentCtx = func(ctx context.Context, _ *shippingv1.CancelShipmentRequest) error {
			checkDeadline("CancelShipment", ctx)
			return nil
		}
		h.payment.onAdjustCOD = func(ctx context.Context, req *paymentv1.AdjustCODShippingFeeRequest) (*paymentv1.AdjustCODShippingFeeResponse, error) {
			checkDeadline("AdjustCODShippingFee", ctx)
			return &paymentv1.AdjustCODShippingFeeResponse{NewShippingFeeVnd: req.GetNewShippingFeeVnd()}, nil
		}
		h.shipping.onCreateShipmentCtx = func(ctx context.Context, req *shippingv1.CreateShipmentRequest) (*shippingv1.CreateShipmentResponse, error) {
			checkDeadline("CreateShipment", ctx)
			return &shippingv1.CreateShipmentResponse{
				Shipment: &shippingv1.Shipment{
					OrderId: req.GetOrderId(), CarrierCode: "ghn", ServiceCode: "standard", TrackingCode: "TRK-DL-OK",
				},
			}, nil
		}

		if _, err := h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
			OrderId: ord.GetId(), AddressId: addrB, AcceptFeeIncrease: true, ExpectedNewShippingFeeVnd: 50000, IdempotencyKey: "k-o1-dl",
		}); err != nil {
			t.Fatalf("UpdateOrderAddress: %v", err)
		}
	})

	t.Run("renew_slower_than_lease_budget_aborts_before_rpc", func(t *testing.T) {
		h := newTestHarness()
		h.svc.cfg.GRPCCallTimeout = 5 * time.Second
		h.svc.addressChangeLease = 80 * time.Millisecond
		h.svc.addressChangeLeaseSafety = 30 * time.Millisecond // lease budget = 50ms
		h.svc.nowFn = func() time.Time { return time.Now().UTC() }

		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
		ord := createTestCODOrder(t, h, "buyer-1", "idem-o1-slow-renew")
		addrB := registerBuyerAddr(h, "addr-o1-slow", "buyer-1", "Buyer B", "0912345678", "79", "TP. Ho Chi Minh", "26735", "Tan Dinh", "60 Slow St")

		if _, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord.GetId(), ShopId: "shop-1"}); err != nil {
			t.Fatalf("SellerShipOrder: %v", err)
		}

		// Store stalls during RenewOrderAddressChangeLease longer than the 50ms lease budget.
		h.svc.store = &slowRenewStore{Store: h.store, delay: 75 * time.Millisecond}

		var cancelCalls atomic.Int32
		h.shipping.onCancelShipmentCtx = func(_ context.Context, _ *shippingv1.CancelShipmentRequest) error {
			cancelCalls.Add(1)
			return nil
		}

		_, err := h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
			OrderId: ord.GetId(), AddressId: addrB, IdempotencyKey: "k-o1-slow-renew",
		})
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "ADDRESS_UPDATE_SHIPMENT_IN_PROGRESS") {
			t.Fatalf("O1 BUG: slow Renew expected 409 ADDRESS_UPDATE_SHIPMENT_IN_PROGRESS (from ErrAddressChangeLostLease), got %v", err)
		}
		if cancelCalls.Load() != 0 {
			t.Fatalf("O1 BUG: side-effect RPC CancelShipment was invoked %d times despite Renew exceeding lease budget", cancelCalls.Load())
		}

		// Also verify prepareAddressChangeRPCContext itself returns store.ErrAddressChangeLostLease directly.
		ch, gErr := h.store.GetOrderAddressChangeByOrderAndIdemKey(context.Background(), ord.GetId(), "k-o1-slow-renew")
		if gErr != nil {
			t.Fatalf("GetOrderAddressChangeByOrderAndIdemKey: %v", gErr)
		}
		if _, _, pErr := h.svc.prepareAddressChangeRPCContext(context.Background(), ch); !errors.Is(pErr, store.ErrAddressChangeLostLease) {
			t.Fatalf("O1 BUG: prepareAddressChangeRPCContext with slow Renew = %v, want ErrAddressChangeLostLease", pErr)
		}
	})

	t.Run("watchdog_claims_one_by_one_respects_ctx_and_does_not_reclaim_errored_intent", func(t *testing.T) {
		h := newTestHarness()
		var clockMu sync.Mutex
		now := time.Date(2026, 11, 25, 10, 30, 0, 0, time.UTC)
		h.svc.nowFn = func() time.Time {
			clockMu.Lock()
			defer clockMu.Unlock()
			return now
		}

		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		addrB := registerBuyerAddr(h, "addr-o1-wd", "buyer-1", "Buyer B", "0912345678", "79", "TP. Ho Chi Minh", "26735", "Tan Dinh", "88 WD St")

		ord1 := createTestCODOrder(t, h, "buyer-1", "idem-wd-1")
		ord2 := createTestCODOrder(t, h, "buyer-1", "idem-wd-2")

		// Pause both orders after Step 1 so 2 due intents exist.
		h.svc.afterAddressChangeStep = func(step int, _ string) error {
			if step == 1 {
				return errors.New("crash step 1")
			}
			return nil
		}
		_, _ = h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{OrderId: ord1.GetId(), AddressId: addrB, IdempotencyKey: "k-wd-1"})
		_, _ = h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{OrderId: ord2.GetId(), AddressId: addrB, IdempotencyKey: "k-wd-2"})
		h.svc.afterAddressChangeStep = nil

		clockMu.Lock()
		now = now.Add(35 * time.Second)
		clockMu.Unlock()

		// Cancel sweep ctx during the 1st intent's Step 4 -> 2nd intent MUST NOT have been claimed yet!
		sweepCtx, cancelSweep := context.WithCancel(context.Background())
		h.svc.afterAddressChangeStep = func(step int, _ string) error {
			if step == 4 {
				cancelSweep()
			}
			return nil
		}
		handled, err := h.svc.ScanPendingAddressChanges(sweepCtx)
		if err != nil {
			t.Fatalf("ScanPendingAddressChanges: %v", err)
		}
		if handled != 1 {
			t.Fatalf("O1 BUG: expected single-claim watchdog to stop after 1st intent on cancelled ctx, handled=%d", handled)
		}

		// The 2nd intent must remain unclaimed (ClaimedUntil still expired <= now), immediately claimable by another pod!
		ch1, _ := h.store.GetOrderAddressChangeByOrderAndIdemKey(context.Background(), ord1.GetId(), "k-wd-1")
		ch2, _ := h.store.GetOrderAddressChangeByOrderAndIdemKey(context.Background(), ord2.GetId(), "k-wd-2")
		unhandled := ch2
		if ch1.State != store.AddressChangeStateCompleted {
			unhandled = ch1
		}
		if unhandled.ClaimedUntil != nil && unhandled.ClaimedUntil.After(now) {
			t.Fatalf("O1 BUG: watchdog eagerly leased 2nd intent %s until %v before processing it", unhandled.ID, *unhandled.ClaimedUntil)
		}

		// Errored intent MUST NOT be re-claimed within the same sweep tick (even with a failing RPC).
		h.svc.afterAddressChangeStep = nil
		ordFail := createTestCODOrder(t, h, "buyer-1", "idem-wd-err")
		h.svc.afterAddressChangeStep = func(step int, _ string) error {
			if step == 1 {
				return errors.New("pause step 1")
			}
			return nil
		}
		h.shipping.quotes = []*shippingv1.CarrierQuote{{CarrierCode: "ghn", ServiceCode: "standard", FeeVnd: 50000, CodSupported: true}}
		_, _ = h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
			OrderId: ordFail.GetId(), AddressId: addrB, AcceptFeeIncrease: true, ExpectedNewShippingFeeVnd: 50000, IdempotencyKey: "k-wd-err",
		})
		h.svc.afterAddressChangeStep = nil
		var payCalls atomic.Int32
		h.payment.onAdjustCOD = func(_ context.Context, _ *paymentv1.AdjustCODShippingFeeRequest) (*paymentv1.AdjustCODShippingFeeResponse, error) {
			payCalls.Add(1)
			return nil, status.Error(codes.Unavailable, "transient")
		}
		// Advance past lease for both unhandled (no COD adjust -> succeeds) and ordFail (COD adjust -> fails transiently).
		clockMu.Lock()
		now = now.Add(35 * time.Second)
		clockMu.Unlock()
		handled2, err := h.svc.ScanPendingAddressChanges(context.Background())
		if err != nil {
			t.Fatalf("ScanPendingAddressChanges 2nd sweep: %v", err)
		}
		if handled2 != 1 {
			t.Fatalf("expected 1 completed (the 2nd order) while failing order deferred, got handled=%d", handled2)
		}
		// RetryIdempotent runs 3 attempts for a single claim; if watchdog re-claimed ordFail in the same tick it would be >= 6!
		if gotCalls := payCalls.Load(); gotCalls != 3 {
			t.Fatalf("O1 BUG: errored intent was re-claimed in the same sweep tick (AdjustCOD calls=%d, want 3)", gotCalls)
		}
	})
}

// --- O2 (1.2): RESTORING_SHIPMENT clears dead OldTrackingCode atomically so restored created webhook is never dropped ---

func TestW3B3_O2_RestoringShipment_ClearsOldTrackingAndPreservesQueuedCreatedWebhook(t *testing.T) {
	h := newTestHarness()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
	ord := createTestCODOrder(t, h, "buyer-1", "idem-o2")
	addrB := registerBuyerAddr(h, "addr-o2-b", "buyer-1", "Buyer B", "0912345678", "01", "Ha Noi", "00001", "Hoan Kiem", "15 O2 St")

	var shipSeq atomic.Int32
	h.shipping.onCreateShipmentCtx = func(ctx context.Context, req *shippingv1.CreateShipmentRequest) (*shippingv1.CreateShipmentResponse, error) {
		if shipSeq.Add(1) == 1 {
			return &shippingv1.CreateShipmentResponse{
				Shipment: &shippingv1.Shipment{
					OrderId: req.GetOrderId(), CarrierCode: "ghn", ServiceCode: "standard",
					TrackingCode: "TRK-OLD-DEAD", LabelUrl: "https://labels.sandbox/old-dead",
				},
			}, nil
		}
		// Verify dead tracking & label were ALREADY cleared before CreateShipment is called!
		mid, _ := h.store.GetOrderWithDetails(context.Background(), ord.GetId())
		if mid.Order.TrackingCode != "" || mid.Order.LabelURL != "" {
			t.Errorf("O2 BUG: dead tracking/label still exposed during RESTORING_SHIPMENT: trk=%q label=%q",
				mid.Order.TrackingCode, mid.Order.LabelURL)
		}
		// Shipping worker enqueues AND delivers shipment.updated(created, TRK-RESTORED) before CreateShipment returns!
		if err := h.svc.HandleShipmentUpdated(context.Background(), "ev-o2-restored-created", events.ShipmentUpdatedPayload{
			OrderID: ord.GetId(), Direction: "FORWARD", Status: "created",
			CarrierCode: "ghn", ServiceCode: "standard", TrackingCode: "TRK-RESTORED", LabelURL: "https://labels.sandbox/restored",
		}); err != nil {
			t.Errorf("HandleShipmentUpdated: %v", err)
		}
		return &shippingv1.CreateShipmentResponse{Queued: true, JobId: "job-restore-o2"}, nil
	}

	if _, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord.GetId(), ShopId: "shop-1"}); err != nil {
		t.Fatalf("SellerShipOrder: %v", err)
	}

	h.shipping.quotes = []*shippingv1.CarrierQuote{{CarrierCode: "ghn", ServiceCode: "standard", FeeVnd: 50000, CodSupported: true}}
	h.payment.adjustCODErr = status.Error(codes.FailedPrecondition, "COD_ALREADY_COLLECTED: settled")

	_, err := h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
		OrderId: ord.GetId(), AddressId: addrB, AcceptFeeIncrease: true, ExpectedNewShippingFeeVnd: 50000,
	})
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "COD_ALREADY_COLLECTED") {
		t.Fatalf("expected COD_ALREADY_COLLECTED refusal, got %v", err)
	}

	got, _ := h.store.GetOrderWithDetails(context.Background(), ord.GetId())
	if got.Order.TrackingCode != "TRK-RESTORED" || got.Order.LabelURL != "https://labels.sandbox/restored" || got.Order.ShipmentJobStatus != "" {
		t.Fatalf("O2 BUG: restored created webhook lost or overwritten by QUEUED: trk=%q label=%q job=%q",
			got.Order.TrackingCode, got.Order.LabelURL, got.Order.ShipmentJobStatus)
	}
}

// --- O3 (1.3): Concurrent sibling COD ceiling check inside OpenOrderAddressChange ---

func TestW3B3_O3_ConcurrentSiblingCODCeiling_OnlyOneWins(t *testing.T) {
	h := newTestHarness()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	// Two shops in same COD checkout group: each subtotal 2,460,000 + ship 30,000 = 2,490,000 -> group total 4,980,000 <= 5,000,000.
	h.product.skus["sku-1"].PriceVnd = 2460000
	h.product.skus["sku-2"].PriceVnd = 2460000
	h.methodConfigs.SetConfig(clients.PaymentMethodConfig{Method: "COD", Enabled: true, MaxOrderVND: 5000000})

	chk, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "idem-o3-group",
		BuyerId:        "buyer-1",
		AddressId:      "addr-buyer-1",
		Items: []*orderv1.CheckoutItemInput{
			{SkuId: "sku-1", Quantity: 1},
			{SkuId: "sku-2", Quantity: 1},
		},
		PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	if err != nil {
		t.Fatalf("CreateCheckout 2-shop COD: %v", err)
	}
	if len(chk.GetOrders()) != 2 {
		t.Fatalf("expected 2 sibling orders, got %d", len(chk.GetOrders()))
	}
	o1, o2 := chk.GetOrders()[0], chk.GetOrders()[1]
	addrB := registerBuyerAddr(h, "addr-o3-b", "buyer-1", "Buyer B", "0912345678", "01", "Ha Noi", "00001", "Hoan Kiem", "10 O3 St")

	// Synchronize both goroutines right after CalculateFee (+15k each -> 4,995k individually, 5,010k together!)
	// so both pass the pre-lock check and race into OpenOrderAddressChange.
	var arrived atomic.Int32
	bothQuoted := make(chan struct{})
	h.shipping.onCalculateFee = func(_ *shippingv1.CalculateFeeRequest) (*shippingv1.CalculateFeeResponse, error) {
		if arrived.Add(1) == 2 {
			close(bothQuoted)
		} else {
			select {
			case <-bothQuoted:
			case <-time.After(2 * time.Second):
			}
		}
		return &shippingv1.CalculateFeeResponse{
			Quotes: []*shippingv1.CarrierQuote{{CarrierCode: "ghn", ServiceCode: "standard", FeeVnd: 45000, CodSupported: true}},
		}, nil
	}

	errs := make([]error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	for i, oid := range []string{o1.GetId(), o2.GetId()} {
		i, oid := i, oid
		go func() {
			defer wg.Done()
			_, errs[i] = h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
				OrderId:                   oid,
				AddressId:                 addrB,
				AcceptFeeIncrease:         true,
				ExpectedNewShippingFeeVnd: 45000,
			})
		}()
	}
	wg.Wait()

	var okCount, limitCount int
	for _, e := range errs {
		if e == nil {
			okCount++
		} else if status.Code(e) == codes.FailedPrecondition && strings.Contains(e.Error(), "COD_LIMIT_EXCEEDED") {
			limitCount++
		} else {
			t.Fatalf("unexpected error in O3 sibling COD race: %v", e)
		}
	}
	if okCount != 1 || limitCount != 1 {
		t.Fatalf("O3 BUG: expected exactly 1 success + 1 COD_LIMIT_EXCEEDED, got ok=%d limit=%d errs=%v", okCount, limitCount, errs)
	}
}

// --- O4 (1.4): Zero-VND COD order (payment_status = SUCCEEDED) treated as prepaid ---

func TestW3B3_O4_ZeroVNDCODSucceeded_TreatedAsPrepaid(t *testing.T) {
	h := newTestHarness()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

	// Seed 0-VND COD order (100% covered by vouchers + freeship -> payment_status = SUCCEEDED, total_payable_vnd = 0).
	h.product.skus["sku-1"].PriceVnd = 100000
	h.promo.breakdown = &promotionv1.DiscountBreakdown{
		Shops: []*promotionv1.ShopDiscountBreakdown{
			{
				ShopId:                 "shop-1",
				SubtotalVnd:            100000,
				ShopVoucherDiscountVnd: 100000,
				FreeshipDiscountVnd:    30000,
				ShippingFeeVnd:         30000,
			},
		},
	}
	chkResp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey:    "idem-o4-zero-cod",
		BuyerId:           "buyer-1",
		AddressId:         "addr-buyer-1",
		Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		ShopVoucherIds:    map[string]string{"shop-1": "sv-100"},
		FreeshipVoucherId: "fs-30",
		PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	if err != nil {
		t.Fatalf("CreateCheckout 0-VND COD: %v", err)
	}
	ord := chkResp.GetOrders()[0]
	if ord.GetTotalPayableVnd() != 0 || !strings.EqualFold(ord.GetPaymentStatus(), "SUCCEEDED") {
		t.Fatalf("seed 0-VND COD failed: total=%d payStatus=%q", ord.GetTotalPayableVnd(), ord.GetPaymentStatus())
	}
	if _, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord.GetId(), ShopId: "shop-1"}); err != nil {
		t.Fatalf("SellerShipOrder: %v", err)
	}

	addrFar := registerBuyerAddr(h, "addr-o4-far", "buyer-1", "Buyer B", "0912345678", "01", "Ha Noi", "00001", "Hoan Kiem", "1 Far St")
	addrIsland := registerBuyerAddr(h, "addr-o4-isl", "buyer-1", "Buyer B", "0912345678", "48", "Da Nang", "20200", "Hoang Sa", "2 Island St")

	// 1. Fee increase (+20k): preview MUST set blocked_prepaid_increase=true (NOT requires_fee_confirmation=true).
	h.shipping.quotes = []*shippingv1.CarrierQuote{{CarrierCode: "ghn", ServiceCode: "standard", FeeVnd: 50000, CodSupported: true}}
	prev, err := h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
		OrderId: ord.GetId(), AddressId: addrFar, PreviewOnly: true,
	})
	if err != nil {
		t.Fatalf("O4 preview: %v", err)
	}
	if !prev.GetBlockedPrepaidIncrease() || prev.GetRequiresFeeConfirmation() || prev.GetNewTotalPayableVnd() != 0 {
		t.Fatalf("O4 BUG: 0-VND COD preview = blocked:%v confirm:%v newTotal:%d, want true/false/0",
			prev.GetBlockedPrepaidIncrease(), prev.GetRequiresFeeConfirmation(), prev.GetNewTotalPayableVnd())
	}

	// 2. Fee increase apply: MUST fail with ADDRESS_FEE_INCREASE_PREPAID and MUST NOT cancel existing shipment!
	_, err = h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
		OrderId: ord.GetId(), AddressId: addrFar, AcceptFeeIncrease: true, ExpectedNewShippingFeeVnd: 50000,
	})
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "ADDRESS_FEE_INCREASE_PREPAID") {
		t.Fatalf("O4 BUG: 0-VND COD apply increase err=%v, want ADDRESS_FEE_INCREASE_PREPAID", err)
	}
	if len(h.shipping.cancelShipCalls) != 0 {
		t.Fatalf("O4 BUG: cancelled live shipment before rejecting 0-VND COD fee increase (%d calls)", len(h.shipping.cancelShipCalls))
	}

	// 3. Fee equal/decrease to non-COD area (cod_supported=false): succeeds because nothing is collected on delivery,
	// keeps totals unchanged (0 VND, shipping_fee 30k), 0 AdjustCODShippingFee calls, recreated shipment has cod_amount_vnd=0.
	h.shipping.quotes = []*shippingv1.CarrierQuote{{CarrierCode: "ghn", ServiceCode: "standard", FeeVnd: 20000, CodSupported: false}}
	resp, err := h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
		OrderId: ord.GetId(), AddressId: addrIsland,
	})
	if err != nil {
		t.Fatalf("O4 BUG: 0-VND COD equal/decrease on non-COD area failed: %v", err)
	}
	if resp.GetFeeAdjusted() || resp.GetOrder().GetTotalPayableVnd() != 0 || resp.GetOrder().GetShippingFeeVnd() != 30000 {
		t.Fatalf("O4 BUG: 0-VND COD totals mutated: feeAdj=%v total=%d shipFee=%d",
			resp.GetFeeAdjusted(), resp.GetOrder().GetTotalPayableVnd(), resp.GetOrder().GetShippingFeeVnd())
	}
	if len(h.payment.adjustCODCalls) != 0 {
		t.Fatalf("O4 BUG: called AdjustCODShippingFee on 0-VND COD order")
	}
	lastShip := h.shipping.createShipCalls[len(h.shipping.createShipCalls)-1]
	if lastShip.GetCodAmountVnd() != 0 {
		t.Fatalf("O4 BUG: recreated shipment cod_amount_vnd = %d, want 0", lastShip.GetCodAmountVnd())
	}
}

// --- O5 (1.5): ErrAddressChangeLostLease maps to 409 ADDRESS_UPDATE_SHIPMENT_IN_PROGRESS & buyer reads terminal intent ---

func TestW3B3_O5_LostLeaseMapsTo409AndBuyerReadsFinalOutcome(t *testing.T) {
	// 1. Direct mapStoreError check.
	mapped := mapStoreError(store.ErrAddressChangeLostLease)
	if status.Code(mapped) != codes.FailedPrecondition || !strings.Contains(mapped.Error(), "ADDRESS_UPDATE_SHIPMENT_IN_PROGRESS") {
		t.Fatalf("O5 BUG: mapStoreError(ErrAddressChangeLostLease) = %v, want FailedPrecondition ADDRESS_UPDATE_SHIPMENT_IN_PROGRESS", mapped)
	}

	// 2. Buyer loses lease while watchdog still holds active lease -> 409 ADDRESS_UPDATE_SHIPMENT_IN_PROGRESS (NOT 500!).
	t.Run("lost_lease_while_watchdog_still_running_returns_409", func(t *testing.T) {
		h := newTestHarness()
		var clockMu sync.Mutex
		now := time.Date(2026, 11, 25, 11, 0, 0, 0, time.UTC)
		h.svc.nowFn = func() time.Time {
			clockMu.Lock()
			defer clockMu.Unlock()
			return now
		}

		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		ord := createTestCODOrder(t, h, "buyer-1", "idem-o5-active")
		addrB := registerBuyerAddr(h, "addr-o5-b", "buyer-1", "Buyer B", "0912345678", "79", "TP. Ho Chi Minh", "26735", "Tan Dinh", "11 O5 St")

		paused := make(chan string, 1)
		release := make(chan struct{})
		h.svc.afterAddressChangeStep = func(step int, changeID string) error {
			if step == 1 {
				paused <- changeID
				<-release
			}
			return nil
		}
		errCh := make(chan error, 1)
		go func() {
			_, err := h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{OrderId: ord.GetId(), AddressId: addrB})
			errCh <- err
		}()
		chID := <-paused

		// Watchdog claims the intent with a new token and keeps it open (still in flight).
		clockMu.Lock()
		now = now.Add(35 * time.Second)
		clockMu.Unlock()
		if _, err := h.store.ClaimOrderAddressChangeByID(context.Background(), chID, "watchdog:other", now, 30*time.Second); err != nil {
			t.Fatalf("ClaimOrderAddressChangeByID: %v", err)
		}

		close(release)
		err := <-errCh
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "ADDRESS_UPDATE_SHIPMENT_IN_PROGRESS") {
			t.Fatalf("O5 BUG: buyer lost lease while intent open got %v, want FailedPrecondition ADDRESS_UPDATE_SHIPMENT_IN_PROGRESS", err)
		}
	})

	// 3. Buyer loses lease after watchdog ABORTED the intent -> returns ABORTED error (NOT 500!).
	t.Run("lost_lease_after_watchdog_aborted_returns_abort_error", func(t *testing.T) {
		h := newTestHarness()
		var clockMu sync.Mutex
		now := time.Date(2026, 11, 25, 11, 10, 0, 0, time.UTC)
		h.svc.nowFn = func() time.Time {
			clockMu.Lock()
			defer clockMu.Unlock()
			return now
		}

		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
		ord := createTestCODOrder(t, h, "buyer-1", "idem-o5-aborted")
		addrB := registerBuyerAddr(h, "addr-o5-ab", "buyer-1", "Buyer B", "0912345678", "79", "TP. Ho Chi Minh", "26735", "Tan Dinh", "12 O5 St")
		if _, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord.GetId(), ShopId: "shop-1"}); err != nil {
			t.Fatalf("SellerShipOrder: %v", err)
		}

		h.shipping.cancelShipErr = status.Error(codes.FailedPrecondition, "carrier already picked up")
		paused := make(chan struct{})
		release := make(chan struct{})
		h.svc.afterAddressChangeStep = func(step int, _ string) error {
			if step == 1 {
				close(paused)
				<-release
			}
			return nil
		}
		errCh := make(chan error, 1)
		go func() {
			_, err := h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{OrderId: ord.GetId(), AddressId: addrB})
			errCh <- err
		}()
		<-paused
		h.svc.afterAddressChangeStep = nil

		clockMu.Lock()
		now = now.Add(35 * time.Second)
		clockMu.Unlock()
		if _, err := h.svc.ScanPendingAddressChanges(context.Background()); err != nil {
			t.Fatalf("ScanPendingAddressChanges: %v", err)
		}

		close(release)
		err := <-errCh
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "ADDRESS_UPDATE_CARRIER_PICKED_UP") {
			t.Fatalf("O5 BUG: buyer lost lease after ABORTED got %v, want ADDRESS_UPDATE_CARRIER_PICKED_UP", err)
		}
	})
}

// --- O6 (1.6): Open-intent blocks same-address & preview; ListOrders offset int64 overflow & unknown status/payment_method ---

func TestW3B3_O6_OpenIntentBlocksSameAddressAndPreview_AndListOrdersValidation(t *testing.T) {
	t.Run("open_intent_blocks_same_address_and_preview", func(t *testing.T) {
		h := newTestHarness()
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		ord := createTestCODOrder(t, h, "buyer-1", "idem-o6a")
		addrB := registerBuyerAddr(h, "addr-o6-b", "buyer-1", "Buyer B", "0912345678", "79", "TP. Ho Chi Minh", "26735", "Tan Dinh", "99 O6 St")

		// Pause intent A -> B after Step 1 so orders.shipping_address_json is still addr-buyer-1 (A).
		h.svc.afterAddressChangeStep = func(step int, _ string) error {
			if step == 1 {
				return errors.New("pause step 1")
			}
			return nil
		}
		_, _ = h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
			OrderId: ord.GetId(), AddressId: addrB, IdempotencyKey: "k-o6-open",
		})
		h.svc.afterAddressChangeStep = nil

		// Selecting current address A while intent A -> B is open MUST return 409 ADDRESS_UPDATE_SHIPMENT_IN_PROGRESS!
		if _, err := h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
			OrderId: ord.GetId(), AddressId: "addr-buyer-1",
		}); status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "ADDRESS_UPDATE_SHIPMENT_IN_PROGRESS") {
			t.Fatalf("O6(a) BUG: same-address while intent open got %v, want ADDRESS_UPDATE_SHIPMENT_IN_PROGRESS", err)
		}

		// Preview while intent is open MUST ALSO return 409 ADDRESS_UPDATE_SHIPMENT_IN_PROGRESS!
		if _, err := h.svc.UpdateOrderAddress(buyerCtx, &orderv1.UpdateOrderAddressRequest{
			OrderId: ord.GetId(), AddressId: addrB, PreviewOnly: true,
		}); status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "ADDRESS_UPDATE_SHIPMENT_IN_PROGRESS") {
			t.Fatalf("O6(a) BUG: preview while intent open got %v, want ADDRESS_UPDATE_SHIPMENT_IN_PROGRESS", err)
		}
	})

	t.Run("list_orders_offset_overflow_and_unknown_enums", func(t *testing.T) {
		h := newTestHarness()
		sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
		_ = createTestCODOrder(t, h, "buyer-1", "idem-o6bc")

		// O6(b): page = 25000000, page_size = 100 overflows int32 -> must return InvalidArgument (400), NOT 500 or wrapped offset!
		if _, err := h.svc.ListOrders(sellerCtx, &orderv1.ListOrdersRequest{
			RoleScope: "seller",
			Page:      25000000,
			PageSize:  100,
		}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("O6(b) BUG: page overflow got err=%v, want InvalidArgument", err)
		}

		// O6(c): unknown non-zero status enum -> InvalidArgument.
		if _, err := h.svc.ListOrders(sellerCtx, &orderv1.ListOrdersRequest{
			RoleScope: "seller",
			Status:    orderv1.OrderStatus(999),
		}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("O6(c) BUG: unknown status 999 got err=%v, want InvalidArgument", err)
		}

		// O6(c): unknown non-zero payment_method enum -> InvalidArgument.
		if _, err := h.svc.ListOrders(sellerCtx, &orderv1.ListOrdersRequest{
			RoleScope:     "seller",
			PaymentMethod: orderv1.OrderPaymentMethod(999),
		}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("O6(c) BUG: unknown payment_method 999 got err=%v, want InvalidArgument", err)
		}
	})
}
