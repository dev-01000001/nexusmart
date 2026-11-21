package events_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/types/known/timestamppb"

	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/promotion/internal/domain"
	"github.com/nexusmart/core/services/promotion/internal/events"
	"github.com/nexusmart/core/services/promotion/internal/lua"
	"github.com/nexusmart/core/services/promotion/internal/service"
	"github.com/nexusmart/core/services/promotion/internal/store"
)

type recordingDLQ struct {
	subjects []string
	reasons  []string
	headers  []map[string]string
	err      error
}

func (r *recordingDLQ) PublishDLQ(_ context.Context, subject string, _ []byte, headers map[string]string) error {
	r.subjects = append(r.subjects, subject)
	r.reasons = append(r.reasons, headers["x-dlq-reason"])
	cp := make(map[string]string, len(headers))
	for k, v := range headers {
		cp[k] = v
	}
	r.headers = append(r.headers, cp)
	return r.err
}

type failingEventStore struct {
	*store.MemoryStore
	completedErr error
	refundedErr  error
	checkErr     error
}

func (f *failingEventStore) IsEventProcessed(ctx context.Context, eventID string) (bool, error) {
	if f.checkErr != nil {
		return false, f.checkErr
	}
	return f.MemoryStore.IsEventProcessed(ctx, eventID)
}

func (f *failingEventStore) ProcessOrderCompletedEvent(ctx context.Context, eventID, subject, orderID, buyerID string, coinsReward int64) (bool, error) {
	if f.completedErr != nil {
		return false, f.completedErr
	}
	return f.MemoryStore.ProcessOrderCompletedEvent(ctx, eventID, subject, orderID, buyerID, coinsReward)
}

func (f *failingEventStore) ProcessOrderRefundedEvent(ctx context.Context, in store.OrderRefundedInput) (bool, error) {
	if f.refundedErr != nil {
		return false, f.refundedErr
	}
	return f.MemoryStore.ProcessOrderRefundedEvent(ctx, in)
}

type fakeJetStream struct {
	nats.JetStreamContext
	streams       map[string]*nats.StreamInfo
	streamInfoErr map[string]error
	addStreamErr  map[string]error
	subErr        map[string]error
	subscribed    []string
	publishedMsgs []*nats.Msg
	publishErr    error
}

func newFakeJetStream() *fakeJetStream {
	return &fakeJetStream{
		streams:       make(map[string]*nats.StreamInfo),
		streamInfoErr: make(map[string]error),
		addStreamErr:  make(map[string]error),
		subErr:        make(map[string]error),
	}
}

func (f *fakeJetStream) StreamInfo(stream string, _ ...nats.JSOpt) (*nats.StreamInfo, error) {
	if err := f.streamInfoErr[stream]; err != nil {
		return nil, err
	}
	if info, ok := f.streams[stream]; ok {
		return info, nil
	}
	return nil, nats.ErrStreamNotFound
}

func (f *fakeJetStream) AddStream(cfg *nats.StreamConfig, _ ...nats.JSOpt) (*nats.StreamInfo, error) {
	if err := f.addStreamErr[cfg.Name]; err != nil {
		return nil, err
	}
	info := &nats.StreamInfo{Config: *cfg}
	f.streams[cfg.Name] = info
	return info, nil
}

func (f *fakeJetStream) Subscribe(subj string, _ nats.MsgHandler, _ ...nats.SubOpt) (*nats.Subscription, error) {
	if err := f.subErr[subj]; err != nil {
		return nil, err
	}
	f.subscribed = append(f.subscribed, subj)
	return nil, nil
}

func (f *fakeJetStream) PublishMsg(m *nats.Msg, _ ...nats.PubOpt) (*nats.PubAck, error) {
	if f.publishErr != nil {
		return nil, f.publishErr
	}
	f.publishedMsgs = append(f.publishedMsgs, m)
	return &nats.PubAck{Stream: events.DefaultDLQStream, Sequence: uint64(len(f.publishedMsgs))}, nil
}

func makeCompletedEvent(eventID, orderID, buyerID string, coins int64) []byte {
	payload, _ := json.Marshal(events.OrderCompletedPayload{
		OrderID:         orderID,
		OrderCode:       "NM-001",
		BuyerID:         buyerID,
		ShopID:          "shop-1",
		SubtotalVND:     200000,
		CommissionVND:   10000,
		TotalPayableVND: 200000,
		CoinsReward:     coins,
	})
	raw, _ := json.Marshal(events.Envelope{
		EventID:    eventID,
		EventType:  events.SubjectOrderCompleted,
		TraceID:    "4bf92f3577b34da6a3ce929d0e0e4736",
		OccurredAt: "2026-11-20T02:35:00Z",
		Payload:    payload,
	})
	return raw
}

func makeRefundedEvent(eventID string, p events.OrderRefundedPayload) []byte {
	payload, _ := json.Marshal(p)
	raw, _ := json.Marshal(events.Envelope{
		EventID:    eventID,
		EventType:  events.SubjectOrderRefunded,
		TraceID:    "4bf92f3577b34da6a3ce929d0e0e4736",
		OccurredAt: "2026-11-20T03:00:00Z",
		Payload:    payload,
	})
	return raw
}

func TestOrderCompletedConsumer_IdempotentCoinCredit(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemoryStore()
	log := logger.New("promotion-events-test", logger.WithWriter(io.Discard))
	consumer := events.NewOrderCompletedConsumer(mem, nil, "NEXUSMART", log)
	dlq := &recordingDLQ{}
	consumer.SetDLQPublisher(dlq)

	raw := makeCompletedEvent("evt-001", "ord-001", "buyer-1", 1500)

	// Deliver exact same event 5 times -> credited ONCE
	for i := 0; i < 5; i++ {
		ack, err := consumer.ProcessRawMessage(ctx, raw, nil, 1)
		if err != nil || !ack {
			t.Fatalf("attempt %d unexpected: ack=%v err=%v", i, ack, err)
		}
	}

	// Deliver a DIFFERENT event_id for the SAME order_id -> still credited ONCE (reference_id = order_id)
	dupOrderRaw := makeCompletedEvent("evt-002", "ord-001", "buyer-1", 1500)
	ack, err := consumer.ProcessRawMessage(ctx, dupOrderRaw, nil, 1)
	if err != nil || !ack {
		t.Fatalf("dup order event unexpected: ack=%v err=%v", ack, err)
	}

	bal, txns, err := mem.GetCoinBalance(ctx, "buyer-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if bal != 1500 || len(txns) != 1 || mem.AccountBalance("buyer-1") != 1500 || mem.LedgerSum("buyer-1") != 1500 {
		t.Fatalf("expected exactly one 1500 coin credit, got bal=%d txns=%d acct=%d sum=%d",
			bal, len(txns), mem.AccountBalance("buyer-1"), mem.LedgerSum("buyer-1"))
	}
	if len(dlq.subjects) != 0 {
		t.Fatalf("expected no DLQ messages, got %v", dlq.subjects)
	}

	// Zero coins_reward should process cleanly without creating a zero-delta coin tx
	zeroRaw := makeCompletedEvent("evt-003", "ord-002", "buyer-1", 0)
	ack, err = consumer.ProcessRawMessage(ctx, zeroRaw, nil, 1)
	if err != nil || !ack {
		t.Fatalf("zero reward event unexpected: ack=%v err=%v", ack, err)
	}
	if _, txns, _ = mem.GetCoinBalance(ctx, "buyer-1", 10); len(txns) != 1 {
		t.Fatalf("expected still 1 coin tx after 0-reward order, got %d", len(txns))
	}
}

func TestOrderCompletedConsumer_PoisonMessageSentToDLQ(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemoryStore()
	log := logger.New("promotion-events-test", logger.WithWriter(io.Discard))
	consumer := events.NewOrderCompletedConsumer(mem, nil, "NEXUSMART", log)
	dlq := &recordingDLQ{}
	consumer.SetDLQPublisher(dlq)

	// Invalid JSON -> sent to DLQ and acked
	ack, err := consumer.ProcessRawMessage(ctx, []byte("{not-json"), nil, 1)
	if err != nil || !ack {
		t.Fatalf("expected poison message acked after DLQ, got ack=%v err=%v", ack, err)
	}
	// Missing event_id -> sent to DLQ and acked
	ack, err = consumer.ProcessRawMessage(ctx, []byte(`{"event_type":"order.completed","payload":{}}`), nil, 1)
	if err != nil || !ack {
		t.Fatalf("expected missing event_id acked after DLQ, got ack=%v err=%v", ack, err)
	}
	// Invalid payload JSON -> sent to DLQ and acked
	ack, err = consumer.ProcessRawMessage(ctx, []byte(`{"event_id":"e-bad-p","event_type":"order.completed","payload":"not-an-object"}`), nil, 1)
	if err != nil || !ack {
		t.Fatalf("expected invalid payload acked after DLQ, got ack=%v err=%v", ack, err)
	}

	if len(dlq.subjects) != 3 || dlq.subjects[0] != "dlq.order.completed" || dlq.subjects[1] != "dlq.order.completed" || dlq.subjects[2] != "dlq.order.completed" {
		t.Fatalf("expected 3 DLQ publishes to dlq.order.completed, got %v", dlq.subjects)
	}
}

func TestOrderRefunded_RewardClawback_AndCappedAtBalance(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemoryStore()
	log := logger.New("promotion-events-test", logger.WithWriter(io.Discard))
	consumer := events.NewOrderCompletedConsumer(mem, nil, "NEXUSMART", log)
	dlq := &recordingDLQ{}
	consumer.SetDLQPublisher(dlq)

	// 1. Full clawback: order completed (+1500 reward), then refunded via ProcessRawMessage dispatch.
	if ack, err := consumer.ProcessRawMessage(ctx, makeCompletedEvent("evt-comp-1", "ord-full", "buyer-1", 1500), nil, 1); err != nil || !ack {
		t.Fatalf("order.completed failed: ack=%v err=%v", ack, err)
	}
	if mem.AccountBalance("buyer-1") != 1500 {
		t.Fatalf("expected 1500 coins, got %d", mem.AccountBalance("buyer-1"))
	}

	refundFull := makeRefundedEvent("evt-ref-1", events.OrderRefundedPayload{
		OrderID:              "ord-full",
		OrderCode:            "NM-FULL",
		CheckoutGroupID:      "grp-full",
		BuyerID:              "buyer-1",
		ShopID:               "shop-1",
		ReturnRequestID:      "ret-1",
		RefundID:             "ref-1",
		RefundAmountVND:      200000,
		CoinsDiscountVND:     0,
		AllGroupOrdersClosed: false,
		Reason:               "damaged",
	})
	// Deliver twice via ProcessRawMessage dispatch -> clawed back ONCE
	for i := 0; i < 2; i++ {
		if ack, err := consumer.ProcessRawMessage(ctx, refundFull, nil, 1); err != nil || !ack {
			t.Fatalf("order.refunded attempt %d failed: ack=%v err=%v", i, ack, err)
		}
	}
	// Deliver again with NEW event_id for same order_id -> still clawed back ONCE via reference_id
	refundFullDup := makeRefundedEvent("evt-ref-1-dup", events.OrderRefundedPayload{
		OrderID: "ord-full",
		BuyerID: "buyer-1",
	})
	if ack, err := consumer.ProcessOrderRefundedMessage(ctx, refundFullDup, nil, 1); err != nil || !ack {
		t.Fatalf("order.refunded dup order failed: ack=%v err=%v", ack, err)
	}

	bal, txns, err := mem.GetCoinBalance(ctx, "buyer-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if bal != 0 || mem.AccountBalance("buyer-1") != 0 || mem.LedgerSum("buyer-1") != 0 || len(txns) != 2 {
		t.Fatalf("expected full clawback to 0 with 2 txns, got bal=%d acct=%d sum=%d txns=%+v",
			bal, mem.AccountBalance("buyer-1"), mem.LedgerSum("buyer-1"), txns)
	}
	if txns[0].ReferenceID != "order_refund_reward_clawback:ord-full" || txns[0].Delta != -1500 {
		t.Fatalf("unexpected clawback tx: %+v", txns[0])
	}

	// 2. Clawback capped at current user balance (balance NEVER goes negative)
	if ack, err := consumer.ProcessRawMessage(ctx, makeCompletedEvent("evt-comp-2", "ord-cap", "buyer-2", 2000), nil, 1); err != nil || !ack {
		t.Fatalf("order.completed buyer-2 failed: ack=%v err=%v", ack, err)
	}
	// Buyer spends 1300 coins on another order -> balance becomes 700
	if _, _, err := mem.CommitReservation(ctx, domain.PromotionReservation{
		OrderID:   "ord-other-spend",
		UserID:    "buyer-2",
		CoinsUsed: 1300,
	}); err != nil {
		t.Fatal(err)
	}
	refundCap := makeRefundedEvent("evt-ref-cap", events.OrderRefundedPayload{
		OrderID:          "ord-cap",
		BuyerID:          "buyer-2",
		RefundAmountVND:  200000,
		CoinsDiscountVND: 0,
	})
	if ack, err := consumer.ProcessOrderRefundedMessage(ctx, refundCap, nil, 1); err != nil || !ack {
		t.Fatalf("order.refunded capped failed: ack=%v err=%v", ack, err)
	}
	bal2, txns2, err := mem.GetCoinBalance(ctx, "buyer-2", 10)
	if err != nil {
		t.Fatal(err)
	}
	if bal2 != 0 || mem.AccountBalance("buyer-2") != 0 || mem.LedgerSum("buyer-2") != 0 {
		t.Fatalf("capped clawback broke invariant: bal=%d acct=%d sum=%d", bal2, mem.AccountBalance("buyer-2"), mem.LedgerSum("buyer-2"))
	}
	if txns2[0].Delta != -700 || txns2[0].ReferenceID != "order_refund_reward_clawback:ord-cap" {
		t.Fatalf("expected capped clawback delta=-700, got %+v", txns2[0])
	}
	if len(dlq.subjects) != 0 {
		t.Fatalf("expected no DLQ messages, got %v", dlq.subjects)
	}
}

func TestOrderRefunded_ProportionalCoinRestore_PartialGroup(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemoryStore()
	log := logger.New("promotion-events-test", logger.WithWriter(io.Discard))
	consumer := events.NewOrderCompletedConsumer(mem, nil, "NEXUSMART", log)

	releaseCalls := 0
	consumer.SetReleaseReservationFn(func(_ context.Context, _ string) error {
		releaseCalls++
		return nil
	})

	// Multi-shop checkout: sub-order 1 refunded while sub-order 2 stays open (AllGroupOrdersClosed=false).
	raw := makeRefundedEvent("evt-part-1", events.OrderRefundedPayload{
		OrderID:              "ord-sub-1",
		OrderCode:            "NM-SUB-1",
		CheckoutGroupID:      "grp-multi-1",
		BuyerID:              "buyer-multi",
		ShopID:               "shop-a",
		ReturnRequestID:      "ret-sub-1",
		RefundID:             "ref-sub-1",
		RefundAmountVND:      150000,
		CoinsDiscountVND:     3500,
		AllGroupOrdersClosed: false,
		Reason:               "wrong size",
	})

	// Deliver 3 times -> restored ONCE, release callback NOT called
	for i := 0; i < 3; i++ {
		if ack, err := consumer.ProcessOrderRefundedMessage(ctx, raw, nil, 1); err != nil || !ack {
			t.Fatalf("attempt %d failed: ack=%v err=%v", i, ack, err)
		}
	}
	if releaseCalls != 0 {
		t.Fatalf("expected release callback NOT called for partial group refund, got %d", releaseCalls)
	}

	bal, txns, err := mem.GetCoinBalance(ctx, "buyer-multi", 10)
	if err != nil {
		t.Fatal(err)
	}
	if bal != 3500 || mem.AccountBalance("buyer-multi") != 3500 || mem.LedgerSum("buyer-multi") != 3500 {
		t.Fatalf("expected 3500 restored coins, got bal=%d acct=%d sum=%d",
			bal, mem.AccountBalance("buyer-multi"), mem.LedgerSum("buyer-multi"))
	}
	if len(txns) != 1 || txns[0].ReferenceID != "order_refund_coins:ord-sub-1" || txns[0].Reason != "order_refund_coins_restore" || txns[0].Delta != 3500 {
		t.Fatalf("unexpected proportional restore tx: %+v", txns)
	}
}

func TestOrderRefunded_AllGroupOrdersClosed_InvokesPromotionServiceReleaseCallback(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	mem := store.NewMemoryStore()
	eng := lua.NewEngine(rdb)
	log := logger.New("promotion-events-integ", logger.WithWriter(io.Discard))
	promoSvc := service.NewPromotionService(mem, eng, log)

	now := time.Now().UTC()
	adminCtx := auth.WithClaims(ctx, &auth.Claims{Role: "admin", RegisteredClaims: jwt.RegisteredClaims{Subject: "admin-1"}})
	sellerCtx := auth.WithClaims(ctx, &auth.Claims{Role: "seller", ShopID: "shop-1", RegisteredClaims: jwt.RegisteredClaims{Subject: "seller-1"}})
	buyerCtx := auth.WithClaims(ctx, &auth.Claims{Role: "buyer", RegisteredClaims: jwt.RegisteredClaims{Subject: "buyer-grp"}})

	// Seed voucher, flash sale, and 10,000 coins
	vResp, err := promoSvc.CreateVoucher(sellerCtx, &promotionv1.CreateVoucherRequest{
		Code: "GRP20K", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP, ShopId: "shop-1",
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 20000,
		Quota: 5, PerUserLimit: 1, BudgetVnd: 100000,
		StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}
	fsResp, err := promoSvc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
		Name: "Group Flash", StartsAt: timestamppb.New(now.Add(-time.Minute)), EndsAt: timestamppb.New(now.Add(time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}
	fiResp, err := promoSvc.RegisterFlashSaleItem(sellerCtx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId: fsResp.FlashSale.Id, ShopId: "shop-1", SkuId: "sku-grp", FlashPriceVnd: 80000, TotalSlots: 10, PerUserLimit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := promoSvc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: fiResp.Item.Id, Approve: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := promoSvc.CreditCoins(adminCtx, &promotionv1.CreditCoinsRequest{
		UserId: "buyer-grp", Amount: 10000, Reason: "seed", ReferenceId: "grp-seed",
	}); err != nil {
		t.Fatal(err)
	}

	// Reserve promotion for checkout group "grp-1" (uses voucher + 2 flash slots + 4000 coins -> balance 6000)
	if _, err := promoSvc.ReservePromotion(buyerCtx, &promotionv1.ReservePromotionRequest{
		OrderId:        "grp-1",
		UserId:         "buyer-grp",
		ShopVoucherIds: map[string]string{"shop-1": vResp.Voucher.Id},
		CoinsToUse:     4000,
		ShopItems: []*promotionv1.ShopDiscountInput{
			{
				ShopId: "shop-1", SubtotalVnd: 200000, ShippingFeeVnd: 15000,
				Items: []*promotionv1.DiscountLineItem{
					{SkuId: "sku-grp", Quantity: 2, UnitPriceVnd: 100000, FlashSaleItemId: fiResp.Item.Id},
				},
			},
		},
	}); err != nil {
		t.Fatalf("ReservePromotion: %v", err)
	}

	// Wire consumer with PromotionService.ReleaseReservationByOrderID
	consumer := events.NewOrderCompletedConsumer(mem, nil, "NEXUSMART", log)
	callbackCount := 0
	consumer.SetReleaseReservationFn(func(rctx context.Context, orderID string) error {
		callbackCount++
		_, rErr := promoSvc.ReleaseReservationByOrderID(rctx, orderID)
		return rErr
	})

	// Simulate order completed (+500 reward -> balance 6500), then full checkout group refunded!
	if ack, err := consumer.ProcessRawMessage(ctx, makeCompletedEvent("evt-grp-comp", "ord-grp-1", "buyer-grp", 500), nil, 1); err != nil || !ack {
		t.Fatalf("order.completed failed: ack=%v err=%v", ack, err)
	}
	if mem.AccountBalance("buyer-grp") != 6500 {
		t.Fatalf("expected 6500 balance before refund, got %d", mem.AccountBalance("buyer-grp"))
	}

	refundRaw := makeRefundedEvent("evt-grp-ref", events.OrderRefundedPayload{
		OrderID:              "ord-grp-1",
		CheckoutGroupID:      "grp-1",
		BuyerID:              "buyer-grp",
		CoinsDiscountVND:     4000,
		AllGroupOrdersClosed: true,
		Reason:               "full_return",
	})
	// Deliver twice -> callback invoked ONCE, vouchers & flash slots & reserved coins restored, reward clawed back!
	for i := 0; i < 2; i++ {
		if ack, err := consumer.ProcessOrderRefundedMessage(ctx, refundRaw, nil, 1); err != nil || !ack {
			t.Fatalf("order.refunded attempt %d failed: ack=%v err=%v", i, ack, err)
		}
	}

	if callbackCount != 1 {
		t.Fatalf("expected release callback invoked once, got %d", callbackCount)
	}
	// Balance: 6500 + 4000 (release reservation) - 500 (reward clawback) = 10000 (NO double restore of CoinsDiscountVND!)
	if mem.AccountBalance("buyer-grp") != 10000 || mem.LedgerSum("buyer-grp") != 10000 {
		t.Fatalf("expected exact balance 10000 after full group release + clawback, got acct=%d sum=%d",
			mem.AccountBalance("buyer-grp"), mem.LedgerSum("buyer-grp"))
	}
	vRem, _, _ := eng.GetInt64(ctx, lua.VoucherRemainingKey(vResp.Voucher.Id))
	fRem, _, _ := eng.GetInt64(ctx, lua.FlashRemainingKey(fiResp.Item.Id))
	if vRem != 5 || fRem != 10 {
		t.Fatalf("expected voucher quota=5 and flash slots=10 restored in Redis, got vRem=%d fRem=%d", vRem, fRem)
	}
}

func TestOrderRefunded_PoisonAndTransientRetryToDLQ(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemoryStore()
	fStore := &failingEventStore{MemoryStore: mem}
	log := logger.New("promotion-events-test", logger.WithWriter(io.Discard))
	consumer := events.NewOrderCompletedConsumer(fStore, nil, "NEXUSMART", log)
	dlq := &recordingDLQ{}
	consumer.SetDLQPublisher(dlq)

	// 1. Poison messages on ProcessOrderRefundedMessage -> dlq.order.refunded
	for _, bad := range [][]byte{
		[]byte("{bad-json"),
		[]byte(`{"event_type":"order.refunded","payload":{}}`),
		[]byte(`{"event_id":"e-bad","event_type":"order.refunded","payload":"not-obj"}`),
	} {
		ack, err := consumer.ProcessOrderRefundedMessage(ctx, bad, map[string]string{"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}, 1)
		if err != nil || !ack {
			t.Fatalf("expected poison order.refunded acked, got ack=%v err=%v", ack, err)
		}
	}
	if len(dlq.subjects) != 3 {
		t.Fatalf("expected 3 DLQ publishes, got %v", dlq.subjects)
	}
	for i, subj := range dlq.subjects {
		if subj != "dlq.order.refunded" || dlq.headers[i]["x-original-subject"] != events.SubjectOrderRefunded {
			t.Fatalf("unexpected DLQ publish %d: subj=%q headers=%+v", i, subj, dlq.headers[i])
		}
	}

	// 2. Transient DB error on order.completed: attempts 1..4 NAK, attempt 5 -> dlq.order.completed
	fStore.completedErr = errors.New("db timeout")
	compRaw := makeCompletedEvent("evt-retry-c", "ord-r-c", "buyer-r", 100)
	for attempt := uint64(1); attempt < events.MaxDeliveryAttempts; attempt++ {
		ack, err := consumer.ProcessRawMessage(ctx, compRaw, nil, attempt)
		if ack || err == nil {
			t.Fatalf("attempt %d: expected NAK + err, got ack=%v err=%v", attempt, ack, err)
		}
	}
	ack, err := consumer.ProcessRawMessage(ctx, compRaw, nil, events.MaxDeliveryAttempts)
	if !ack || err != nil {
		t.Fatalf("attempt 5 order.completed: expected ack after DLQ, got ack=%v err=%v", ack, err)
	}
	if dlq.subjects[len(dlq.subjects)-1] != "dlq.order.completed" || !strings.Contains(dlq.reasons[len(dlq.reasons)-1], "max retries exceeded") {
		t.Fatalf("expected dlq.order.completed max retries, got subj=%q reason=%q", dlq.subjects[len(dlq.subjects)-1], dlq.reasons[len(dlq.reasons)-1])
	}
	fStore.completedErr = nil

	// 3. Transient error on IsEventProcessed -> NAK on 1, DLQ on 5
	fStore.checkErr = errors.New("pg conn reset")
	refRaw := makeRefundedEvent("evt-retry-r", events.OrderRefundedPayload{
		OrderID: "ord-r-r", CheckoutGroupID: "grp-r", BuyerID: "buyer-r", AllGroupOrdersClosed: true,
	})
	if ack, err := consumer.ProcessOrderRefundedMessage(ctx, refRaw, nil, 1); ack || err == nil {
		t.Fatalf("IsEventProcessed error attempt 1: want NAK, got ack=%v err=%v", ack, err)
	}
	if ack, err := consumer.ProcessOrderRefundedMessage(ctx, refRaw, nil, events.MaxDeliveryAttempts); !ack || err != nil {
		t.Fatalf("IsEventProcessed error attempt 5: want ack+DLQ, got ack=%v err=%v", ack, err)
	}
	fStore.checkErr = nil

	// 4. Transient error on ReleaseReservationFn -> NAK on 1..4, DLQ on 5; recovery on retry succeeds!
	failRelease := true
	consumer.SetReleaseReservationFn(func(_ context.Context, _ string) error {
		if failRelease {
			return errors.New("redis unavailable")
		}
		return nil
	})
	for attempt := uint64(1); attempt < events.MaxDeliveryAttempts; attempt++ {
		ack, err := consumer.ProcessOrderRefundedMessage(ctx, refRaw, nil, attempt)
		if ack || err == nil {
			t.Fatalf("release error attempt %d: expected NAK, got ack=%v err=%v", attempt, ack, err)
		}
	}
	// Recover on attempt 5 -> succeeds without DLQ!
	dlqBefore := len(dlq.subjects)
	failRelease = false
	if ack, err := consumer.ProcessOrderRefundedMessage(ctx, refRaw, nil, 4); !ack || err != nil {
		t.Fatalf("recovered attempt expected ack, got ack=%v err=%v", ack, err)
	}
	if len(dlq.subjects) != dlqBefore {
		t.Fatalf("unexpected DLQ on recovered retry")
	}

	// Permanent failure of ReleaseReservationFn at attempt 5 -> dlq.order.refunded
	failRelease = true
	refRaw2 := makeRefundedEvent("evt-retry-r2", events.OrderRefundedPayload{
		OrderID: "ord-r-r2", CheckoutGroupID: "grp-r2", BuyerID: "buyer-r", AllGroupOrdersClosed: true,
	})
	if ack, err := consumer.ProcessOrderRefundedMessage(ctx, refRaw2, nil, events.MaxDeliveryAttempts); !ack || err != nil {
		t.Fatalf("release error attempt 5: expected ack+DLQ, got ack=%v err=%v", ack, err)
	}
	if dlq.subjects[len(dlq.subjects)-1] != "dlq.order.refunded" {
		t.Fatalf("expected dlq.order.refunded, got %q", dlq.subjects[len(dlq.subjects)-1])
	}
	failRelease = false

	// 5. Transient error on ProcessOrderRefundedEvent -> NAK on 1, DLQ on 5
	fStore.refundedErr = errors.New("tx serialization failure")
	refRaw3 := makeRefundedEvent("evt-retry-r3", events.OrderRefundedPayload{
		OrderID: "ord-r-r3", BuyerID: "buyer-r", CoinsDiscountVND: 1000,
	})
	if ack, err := consumer.ProcessOrderRefundedMessage(ctx, refRaw3, nil, 1); ack || err == nil {
		t.Fatalf("refunded store error attempt 1: expected NAK, got ack=%v err=%v", ack, err)
	}
	if ack, err := consumer.ProcessOrderRefundedMessage(ctx, refRaw3, nil, events.MaxDeliveryAttempts); !ack || err != nil {
		t.Fatalf("refunded store error attempt 5: expected ack+DLQ, got ack=%v err=%v", ack, err)
	}
	if dlq.subjects[len(dlq.subjects)-1] != "dlq.order.refunded" {
		t.Fatalf("expected dlq.order.refunded, got %q", dlq.subjects[len(dlq.subjects)-1])
	}

	// DLQ publish error & nil DLQ publisher do not panic
	dlq.err = errors.New("dlq broker down")
	if ack, err := consumer.ProcessRawMessage(ctx, []byte("{bad"), nil, 1); !ack || err != nil {
		t.Fatalf("DLQ publish failure should still ack poison message: ack=%v err=%v", ack, err)
	}
	consumer.SetDLQPublisher(nil)
	if ack, err := consumer.ProcessRawMessage(ctx, []byte("{bad"), nil, 1); !ack || err != nil {
		t.Fatalf("nil DLQ publisher should still ack poison message: ack=%v err=%v", ack, err)
	}
}

func TestOrderCompletedConsumer_JetStreamLifecycleAndDLQ(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemoryStore()

	// Nil js & nil logger defaults cleanly
	nilConsumer := events.NewOrderCompletedConsumer(mem, nil, "", nil)
	if err := nilConsumer.Start(ctx); err != nil {
		t.Fatalf("Start with nil js: %v", err)
	}
	if err := nilConsumer.Stop(); err != nil {
		t.Fatalf("Stop with nil subs: %v", err)
	}

	// Fake JetStream creates both streams and subscribes to both order.completed & order.refunded
	fjs := newFakeJetStream()
	c := events.NewOrderCompletedConsumer(mem, fjs, "NEXUSMART", nil)
	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start with fake js: %v", err)
	}
	if _, ok := fjs.streams["NEXUSMART"]; !ok {
		t.Fatal("expected NEXUSMART stream created")
	}
	if _, ok := fjs.streams[events.DefaultDLQStream]; !ok {
		t.Fatal("expected NEXUSMART_DLQ stream created")
	}
	if len(fjs.subscribed) != 2 || fjs.subscribed[0] != events.SubjectOrderCompleted || fjs.subscribed[1] != events.SubjectOrderRefunded {
		t.Fatalf("expected subscriptions to order.completed and order.refunded, got %v", fjs.subscribed)
	}
	if err := c.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Poison message exercises real jetStreamDLQ.PublishDLQ
	if ack, err := c.ProcessOrderRefundedMessage(ctx, []byte("{corrupt"), map[string]string{"x-test": "1"}, 1); !ack || err != nil {
		t.Fatalf("poison via jetStreamDLQ: ack=%v err=%v", ack, err)
	}
	if len(fjs.publishedMsgs) != 1 || fjs.publishedMsgs[0].Subject != "dlq.order.refunded" {
		t.Fatalf("expected 1 DLQ msg to dlq.order.refunded, got %+v", fjs.publishedMsgs)
	}

	// Error branches in EnsureStreams and Start
	fjsErr1 := newFakeJetStream()
	fjsErr1.streamInfoErr["NEXUSMART"] = errors.New("nats permission denied")
	if err := events.NewOrderCompletedConsumer(mem, fjsErr1, "NEXUSMART", nil).Start(ctx); err == nil {
		t.Fatal("expected StreamInfo error")
	}

	fjsErr2 := newFakeJetStream()
	fjsErr2.addStreamErr["NEXUSMART"] = errors.New("disk full")
	if err := events.NewOrderCompletedConsumer(mem, fjsErr2, "NEXUSMART", nil).EnsureStreams(); err == nil {
		t.Fatal("expected AddStream primary error")
	}

	fjsErr3 := newFakeJetStream()
	fjsErr3.streamInfoErr[events.DefaultDLQStream] = errors.New("dlq inspect failed")
	if err := events.NewOrderCompletedConsumer(mem, fjsErr3, "NEXUSMART", nil).EnsureStreams(); err == nil {
		t.Fatal("expected StreamInfo DLQ error")
	}

	fjsErr4 := newFakeJetStream()
	fjsErr4.addStreamErr[events.DefaultDLQStream] = errors.New("dlq add failed")
	if err := events.NewOrderCompletedConsumer(mem, fjsErr4, "NEXUSMART", nil).EnsureStreams(); err == nil {
		t.Fatal("expected AddStream DLQ error")
	}

	fjsErr5 := newFakeJetStream()
	fjsErr5.subErr[events.SubjectOrderCompleted] = errors.New("sub completed failed")
	if err := events.NewOrderCompletedConsumer(mem, fjsErr5, "NEXUSMART", nil).Start(ctx); err == nil {
		t.Fatal("expected Subscribe order.completed error")
	}

	fjsErr6 := newFakeJetStream()
	fjsErr6.subErr[events.SubjectOrderRefunded] = errors.New("sub refunded failed")
	if err := events.NewOrderCompletedConsumer(mem, fjsErr6, "NEXUSMART", nil).Start(ctx); err == nil {
		t.Fatal("expected Subscribe order.refunded error")
	}
}
