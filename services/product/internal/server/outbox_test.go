package server

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/product/internal/events"
	"github.com/nexusmart/core/services/product/internal/store"
)

type testPublisher struct {
	mu   sync.Mutex
	msgs []*nats.Msg
}

func (p *testPublisher) PublishMsg(m *nats.Msg, _ ...nats.PubOpt) (*nats.PubAck, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cp := *m
	cp.Data = append([]byte(nil), m.Data...)
	cp.Header = nats.Header{}
	for k, vals := range m.Header {
		cp.Header[k] = append([]string(nil), vals...)
	}
	p.msgs = append(p.msgs, &cp)
	return &nats.PubAck{Stream: events.DefaultStreamName, Sequence: uint64(len(p.msgs))}, nil
}

func decodeProductPayload(t *testing.T, raw []byte) events.ProductEventPayload {
	t.Helper()
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("unmarshal raw payload map: %v", err)
	}
	for _, key := range []string{"product_id", "shop_id", "version", "occurred_at"} {
		if _, ok := fields[key]; !ok {
			t.Fatalf("payload missing required field %q: %s", key, string(raw))
		}
	}
	if len(fields) != 4 {
		t.Fatalf("expected exactly 4 canonical fields in payload, got %d: %s", len(fields), string(raw))
	}

	var p events.ProductEventPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("unmarshal ProductEventPayload: %v", err)
	}
	if _, err := time.Parse(time.RFC3339Nano, p.OccurredAt); err != nil {
		t.Fatalf("invalid RFC3339 occurred_at %q: %v", p.OccurredAt, err)
	}
	return p
}

func TestOutboxEvents_AllSubjectsMonotonicVersionAndTracePropagation(t *testing.T) {
	shutdown, err := telemetry.Init(context.Background(), telemetry.Config{ServiceName: "product-outbox-test"})
	if err != nil {
		t.Fatalf("telemetry.Init: %v", err)
	}
	t.Cleanup(func() { _ = shutdown(context.Background()) })

	st := store.NewMemoryStore()
	srv := New(st, nil)

	now := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	srv.SetClock(func() time.Time { return now })

	var notifyCount atomic.Int32
	srv.SetOutboxNotifier(func() { notifyCount.Add(1) })

	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")
	buyerCtx := ctxWithRole("buyer-1", "buyer", "")
	promoCtx := ctxWithService("promotion-service")

	catResp, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name:              "Apparel",
		Slug:              "apparel",
		CommissionRateBps: 500,
	})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	// Category CRUD should not emit product.* events.
	if got := len(st.OutboxEvents()); got != 0 {
		t.Fatalf("expected 0 outbox events after CreateCategory, got %d", got)
	}

	// 1. CreateProduct with active OTel span -> product.created + trace_id + traceparent.
	tracedSellerCtx, span := otel.Tracer("product-test").Start(sellerCtx, "create-product")
	wantTraceID := telemetry.TraceIDFromContext(tracedSellerCtx)
	prodResp, err := srv.CreateProduct(tracedSellerCtx, &productv1.CreateProductRequest{
		CategoryId:  catResp.Category.Id,
		Name:        "Áo thun thể thao",
		Description: "Thoáng khí, co giãn",
		Skus: []*productv1.SKUInput{
			{SkuCode: "AT-M", Name: "Size M", PriceVnd: 199000, Stock: 20},
		},
	})
	span.End()
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	prodID := prodResp.Product.Id
	skuID := prodResp.Product.Skus[0].Id

	// 2. AdminReviewProduct (REJECT then APPROVE) -> 2x product.updated.
	if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: prodID,
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_REJECT,
		Reason:    "need sharper image",
	}); err != nil {
		t.Fatalf("AdminReviewProduct reject: %v", err)
	}
	if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: prodID,
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	}); err != nil {
		t.Fatalf("AdminReviewProduct approve: %v", err)
	}

	// 3. UpdateProduct -> product.updated.
	if _, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id:          prodID,
		Name:        "Áo thun thể thao Pro",
		Description: "Bản nâng cấp",
	}); err != nil {
		t.Fatalf("UpdateProduct: %v", err)
	}

	// 4. UpdateStock -> product.stock_changed.
	updStockResp, err := srv.UpdateStock(sellerCtx, &productv1.UpdateStockRequest{
		SkuId:           skuID,
		NewStock:        25,
		ExpectedVersion: 1,
	})
	if err != nil {
		t.Fatalf("UpdateStock: %v", err)
	}
	if updStockResp.Sku.Stock != 25 {
		t.Fatalf("expected stock=25, got %d", updStockResp.Sku.Stock)
	}

	// 5. ReserveStock -> product.stock_changed; idempotent retry -> NO duplicate event.
	res1, err := srv.ReserveStock(buyerCtx, &productv1.ReserveStockRequest{
		OrderId: "ord-rel-1",
		Items:   []*productv1.StockReservationItem{{SkuId: skuID, Quantity: 3}},
	})
	if err != nil || res1.AlreadyReserved {
		t.Fatalf("ReserveStock 1st: resp=%+v err=%v", res1, err)
	}
	res1Dup, err := srv.ReserveStock(buyerCtx, &productv1.ReserveStockRequest{
		OrderId: "ord-rel-1",
		Items:   []*productv1.StockReservationItem{{SkuId: skuID, Quantity: 3}},
	})
	if err != nil || !res1Dup.AlreadyReserved {
		t.Fatalf("ReserveStock idempotent retry: resp=%+v err=%v", res1Dup, err)
	}

	// 6. ReleaseStock -> product.stock_changed; idempotent retry & unknown order -> NO event.
	rel1, err := srv.ReleaseStock(adminCtx, &productv1.ReleaseStockRequest{OrderId: "ord-rel-1"})
	if err != nil || !rel1.Released || rel1.AlreadyReleased {
		t.Fatalf("ReleaseStock 1st: resp=%+v err=%v", rel1, err)
	}
	rel1Dup, err := srv.ReleaseStock(adminCtx, &productv1.ReleaseStockRequest{OrderId: "ord-rel-1"})
	if err != nil || !rel1Dup.AlreadyReleased {
		t.Fatalf("ReleaseStock idempotent retry: resp=%+v err=%v", rel1Dup, err)
	}
	relUnknown, err := srv.ReleaseStock(adminCtx, &productv1.ReleaseStockRequest{OrderId: "ord-does-not-exist"})
	if err != nil || relUnknown.Released {
		t.Fatalf("ReleaseStock unknown order: resp=%+v err=%v", relUnknown, err)
	}

	// 7. ReserveStock + ConfirmStock -> product.stock_changed each; idempotent ConfirmStock -> NO event.
	if _, err := srv.ReserveStock(buyerCtx, &productv1.ReserveStockRequest{
		OrderId: "ord-conf-1",
		Items:   []*productv1.StockReservationItem{{SkuId: skuID, Quantity: 2}},
	}); err != nil {
		t.Fatalf("ReserveStock for confirm: %v", err)
	}
	conf1, err := srv.ConfirmStock(adminCtx, &productv1.ConfirmStockRequest{OrderId: "ord-conf-1"})
	if err != nil || !conf1.Confirmed || conf1.AlreadyConfirmed {
		t.Fatalf("ConfirmStock 1st: resp=%+v err=%v", conf1, err)
	}
	conf1Dup, err := srv.ConfirmStock(adminCtx, &productv1.ConfirmStockRequest{OrderId: "ord-conf-1"})
	if err != nil || !conf1Dup.AlreadyConfirmed {
		t.Fatalf("ConfirmStock idempotent retry: resp=%+v err=%v", conf1Dup, err)
	}

	// 8. Flash sale reserve + idempotent retry + release + idempotent retry.
	endsAt := now.Add(time.Hour)
	if _, err := srv.ReserveFlashSaleStock(promoCtx, &productv1.ReserveFlashSaleStockRequest{
		FlashSaleItemId: "fs-outbox-1",
		SkuId:           skuID,
		Quantity:        5,
		EndsAt:          timestamppb.New(endsAt),
	}); err != nil {
		t.Fatalf("ReserveFlashSaleStock 1st: %v", err)
	}
	if r, err := srv.ReserveFlashSaleStock(promoCtx, &productv1.ReserveFlashSaleStockRequest{
		FlashSaleItemId: "fs-outbox-1",
		SkuId:           skuID,
		Quantity:        5,
		EndsAt:          timestamppb.New(endsAt),
	}); err != nil || !r.AlreadyReserved {
		t.Fatalf("ReserveFlashSaleStock idempotent: resp=%+v err=%v", r, err)
	}
	if r, err := srv.ReleaseFlashSaleStock(promoCtx, &productv1.ReleaseFlashSaleStockRequest{
		FlashSaleItemId: "fs-outbox-1",
	}); err != nil || r.AlreadyReleased || r.ReleasedQty != 5 {
		t.Fatalf("ReleaseFlashSaleStock 1st: resp=%+v err=%v", r, err)
	}
	if r, err := srv.ReleaseFlashSaleStock(promoCtx, &productv1.ReleaseFlashSaleStockRequest{
		FlashSaleItemId: "fs-outbox-1",
	}); err != nil || !r.AlreadyReleased {
		t.Fatalf("ReleaseFlashSaleStock idempotent: resp=%+v err=%v", r, err)
	}

	// 9. Flash sale fully consumed -> release has remaining=0 -> NO extra stock_changed.
	if _, err := srv.ReserveFlashSaleStock(promoCtx, &productv1.ReserveFlashSaleStockRequest{
		FlashSaleItemId: "fs-outbox-full",
		SkuId:           skuID,
		Quantity:        2,
		EndsAt:          timestamppb.New(endsAt),
	}); err != nil {
		t.Fatalf("ReserveFlashSaleStock full: %v", err)
	}
	if _, err := srv.ReserveStock(buyerCtx, &productv1.ReserveStockRequest{
		OrderId: "ord-fs-full",
		Items: []*productv1.StockReservationItem{
			{SkuId: skuID, Quantity: 2, FlashSaleItemId: "fs-outbox-full"},
		},
	}); err != nil {
		t.Fatalf("ReserveStock consuming full flash slot: %v", err)
	}
	if r, err := srv.ReleaseFlashSaleStock(promoCtx, &productv1.ReleaseFlashSaleStockRequest{
		FlashSaleItemId: "fs-outbox-full",
	}); err != nil || r.ReleasedQty != 0 {
		t.Fatalf("ReleaseFlashSaleStock fully consumed: resp=%+v err=%v", r, err)
	}

	// 10. Flash sale expired sweeper -> emits stock_changed when releasing remaining stock.
	if _, err := srv.ReserveFlashSaleStock(promoCtx, &productv1.ReserveFlashSaleStockRequest{
		FlashSaleItemId: "fs-outbox-sweep",
		SkuId:           skuID,
		Quantity:        3,
		EndsAt:          timestamppb.New(now.Add(10 * time.Minute)),
	}); err != nil {
		t.Fatalf("ReserveFlashSaleStock for sweep: %v", err)
	}
	now = now.Add(15 * time.Minute)
	swept, err := srv.SweepExpiredFlashSaleReservations(context.Background())
	if err != nil || swept != 1 {
		t.Fatalf("SweepExpiredFlashSaleReservations: swept=%d err=%v", swept, err)
	}

	// 11. DeleteProduct -> product.deleted.
	if _, err := srv.DeleteProduct(sellerCtx, &productv1.DeleteProductRequest{Id: prodID}); err != nil {
		t.Fatalf("DeleteProduct: %v", err)
	}

	wantTypes := []string{
		events.SubjectProductCreated,      // CreateProduct
		events.SubjectProductUpdated,      // AdminReviewProduct REJECT
		events.SubjectProductUpdated,      // AdminReviewProduct APPROVE
		events.SubjectProductUpdated,      // UpdateProduct
		events.SubjectProductStockChanged, // UpdateStock
		events.SubjectProductStockChanged, // ReserveStock (ord-rel-1)
		events.SubjectProductStockChanged, // ReleaseStock (ord-rel-1)
		events.SubjectProductStockChanged, // ReserveStock (ord-conf-1)
		events.SubjectProductStockChanged, // ConfirmStock (ord-conf-1)
		events.SubjectProductStockChanged, // ReserveFlashSaleStock (fs-outbox-1)
		events.SubjectProductStockChanged, // ReleaseFlashSaleStock (fs-outbox-1)
		events.SubjectProductStockChanged, // ReserveFlashSaleStock (fs-outbox-full)
		events.SubjectProductStockChanged, // ReserveStock (ord-fs-full)
		events.SubjectProductStockChanged, // ReserveFlashSaleStock (fs-outbox-sweep)
		events.SubjectProductStockChanged, // SweepExpiredFlashSaleReservations (fs-outbox-sweep)
		events.SubjectProductDeleted,      // DeleteProduct
	}

	rows := st.OutboxEvents()
	if len(rows) != len(wantTypes) {
		gotTypes := make([]string, len(rows))
		for i, r := range rows {
			gotTypes[i] = r.EventType
		}
		t.Fatalf("outbox count = %d (%v), want %d (%v)", len(rows), gotTypes, len(wantTypes), wantTypes)
	}
	if int(notifyCount.Load()) != len(wantTypes) {
		t.Fatalf("outbox notifier invoked %d times, want %d", notifyCount.Load(), len(wantTypes))
	}

	// Verify trace propagation on first event (traced CreateProduct).
	if rows[0].TraceID != wantTraceID || wantTraceID == "" {
		t.Fatalf("first event trace_id = %q, want %q", rows[0].TraceID, wantTraceID)
	}
	if rows[0].Traceparent == "" {
		t.Fatalf("expected non-empty traceparent on traced CreateProduct event")
	}

	var prevVersion int64
	for i, row := range rows {
		if row.EventType != wantTypes[i] {
			t.Fatalf("event[%d] type = %q, want %q", i, row.EventType, wantTypes[i])
		}
		if row.ID == "" {
			t.Fatalf("event[%d] has empty ID", i)
		}
		payload := decodeProductPayload(t, row.PayloadJSON)
		if payload.ProductID != prodID || payload.ShopID != "shop-1" {
			t.Fatalf("event[%d] payload mismatch: %+v", i, payload)
		}
		if payload.Version <= prevVersion {
			t.Fatalf("event[%d] version %d is not strictly greater than previous %d", i, payload.Version, prevVersion)
		}
		prevVersion = payload.Version
	}

	// Relay all events through OutboxRelay and verify NATS publication end-to-end.
	pub := &testPublisher{}
	relay := events.NewOutboxRelay(st, pub, logger.New("test", logger.WithWriter(io.Discard)), 0, nil)
	if published := relay.FlushOnce(context.Background()); published != len(wantTypes) {
		t.Fatalf("relay published %d, want %d", published, len(wantTypes))
	}
	if len(pub.msgs) != len(wantTypes) {
		t.Fatalf("publisher received %d msgs, want %d", len(pub.msgs), len(wantTypes))
	}
	if got := pub.msgs[0].Header.Get("traceparent"); got != rows[0].Traceparent {
		t.Fatalf("published NATS traceparent = %q, want %q", got, rows[0].Traceparent)
	}
	if got := pub.msgs[0].Header.Get(nats.MsgIdHdr); got != rows[0].ID {
		t.Fatalf("published Nats-Msg-Id = %q, want %q", got, rows[0].ID)
	}
}

func TestOutboxEvents_DeduplicatesPerProductInBatchStockOps(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)
	adminCtx := ctxWithRole("admin-1", "admin", "")
	buyerCtx := ctxWithRole("buyer-1", "buyer", "")

	_, prodA := setupApprovedProduct(t, srv, "shop-a", []*productv1.SKUInput{
		{SkuCode: "A-1", Name: "Red", PriceVnd: 100000, Stock: 10},
		{SkuCode: "A-2", Name: "Blue", PriceVnd: 120000, Stock: 10},
	})
	_, prodB := setupApprovedProduct(t, srv, "shop-b", []*productv1.SKUInput{
		{SkuCode: "B-1", Name: "Default", PriceVnd: 200000, Stock: 10},
	})

	baseCount := len(st.OutboxEvents()) // 2 created + 2 approved = 4

	// Reserve 2 SKUs from prodA + 1 SKU from prodB -> must emit exactly 2 stock_changed events (deduped per product_id).
	if _, err := srv.ReserveStock(buyerCtx, &productv1.ReserveStockRequest{
		OrderId: "ord-multi-1",
		Items: []*productv1.StockReservationItem{
			{SkuId: prodA.Skus[0].Id, Quantity: 1},
			{SkuId: prodA.Skus[1].Id, Quantity: 2},
			{SkuId: prodB.Skus[0].Id, Quantity: 1},
		},
	}); err != nil {
		t.Fatalf("ReserveStock multi: %v", err)
	}

	afterReserve := st.OutboxEvents()[baseCount:]
	if len(afterReserve) != 2 {
		t.Fatalf("expected 2 deduped stock_changed events on ReserveStock, got %d", len(afterReserve))
	}
	seenProds := map[string]string{}
	for _, ev := range afterReserve {
		if ev.EventType != events.SubjectProductStockChanged {
			t.Fatalf("unexpected event type %q", ev.EventType)
		}
		p := decodeProductPayload(t, ev.PayloadJSON)
		seenProds[p.ProductID] = p.ShopID
	}
	if seenProds[prodA.Id] != "shop-a" || seenProds[prodB.Id] != "shop-b" {
		t.Fatalf("unexpected affected products on reserve: %+v", seenProds)
	}

	// ReleaseStock on the same multi-SKU order -> exactly 2 deduped stock_changed events.
	beforeRelease := len(st.OutboxEvents())
	if _, err := srv.ReleaseStock(adminCtx, &productv1.ReleaseStockRequest{OrderId: "ord-multi-1"}); err != nil {
		t.Fatalf("ReleaseStock multi: %v", err)
	}
	afterRelease := st.OutboxEvents()[beforeRelease:]
	if len(afterRelease) != 2 {
		t.Fatalf("expected 2 deduped stock_changed events on ReleaseStock, got %d", len(afterRelease))
	}

	// Reserve + Confirm on multi-SKU order -> ConfirmStock emits exactly 2 deduped stock_changed events.
	if _, err := srv.ReserveStock(buyerCtx, &productv1.ReserveStockRequest{
		OrderId: "ord-multi-2",
		Items: []*productv1.StockReservationItem{
			{SkuId: prodA.Skus[0].Id, Quantity: 1},
			{SkuId: prodA.Skus[1].Id, Quantity: 1},
			{SkuId: prodB.Skus[0].Id, Quantity: 1},
		},
	}); err != nil {
		t.Fatalf("ReserveStock multi-2: %v", err)
	}
	beforeConfirm := len(st.OutboxEvents())
	if _, err := srv.ConfirmStock(adminCtx, &productv1.ConfirmStockRequest{OrderId: "ord-multi-2"}); err != nil {
		t.Fatalf("ConfirmStock multi-2: %v", err)
	}
	afterConfirm := st.OutboxEvents()[beforeConfirm:]
	if len(afterConfirm) != 2 {
		t.Fatalf("expected 2 deduped stock_changed events on ConfirmStock, got %d", len(afterConfirm))
	}
}

func TestOutboxEvents_RollbackLeavesZeroOutboxRows(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)

	var notifyCount atomic.Int32
	srv.SetOutboxNotifier(func() { notifyCount.Add(1) })

	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")
	otherSellerCtx := ctxWithRole("seller-2", "seller", "shop-2")
	buyerCtx := ctxWithRole("buyer-1", "buyer", "")

	cat, prod := setupApprovedProduct(t, srv, "shop-1", []*productv1.SKUInput{
		{SkuCode: "RB-1", Name: "SKU 1", PriceVnd: 100000, Stock: 5},
		{SkuCode: "RB-2", Name: "SKU 2", PriceVnd: 100000, Stock: 1},
	})
	baselineEvents := len(st.OutboxEvents())
	baselineNotifies := notifyCount.Load()

	// 1. CreateProduct failing validation (duplicate sku_code) -> 0 new events.
	if _, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: cat.Id,
		Name:       "Bad Product",
		Skus: []*productv1.SKUInput{
			{SkuCode: "DUP", PriceVnd: 10000, Stock: 1},
			{SkuCode: "DUP", PriceVnd: 10000, Stock: 1},
		},
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument on duplicate sku_code, got %v", err)
	}

	// 2. AdminReviewProduct on unknown product -> 0 new events.
	if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: "missing-product-id",
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	}); status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound on unknown product review, got %v", err)
	}

	// 3. DeleteProduct by wrong seller -> 0 new events.
	if _, err := srv.DeleteProduct(otherSellerCtx, &productv1.DeleteProductRequest{
		Id: prod.Id,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied on cross-shop delete, got %v", err)
	}

	// 4. UpdateStock with wrong version -> 0 new events.
	if _, err := srv.UpdateStock(sellerCtx, &productv1.UpdateStockRequest{
		SkuId:           prod.Skus[0].Id,
		NewStock:        99,
		ExpectedVersion: 999,
	}); status.Code(err) != codes.Aborted {
		t.Fatalf("expected Aborted on wrong stock version, got %v", err)
	}

	// 5. ReserveStock failing on second SKU (insufficient stock) -> entire tx rolls back, 0 new events.
	if _, err := srv.ReserveStock(buyerCtx, &productv1.ReserveStockRequest{
		OrderId: "ord-rollback-1",
		Items: []*productv1.StockReservationItem{
			{SkuId: prod.Skus[0].Id, Quantity: 2},
			{SkuId: prod.Skus[1].Id, Quantity: 10}, // exceeds stock=1
		},
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition on insufficient stock, got %v", err)
	}

	if got := len(st.OutboxEvents()); got != baselineEvents {
		t.Fatalf("expected 0 new outbox events after rolled-back mutations, got +%d", got-baselineEvents)
	}
	if got := notifyCount.Load(); got != baselineNotifies {
		t.Fatalf("expected 0 new outbox notifications after rolled-back mutations, got +%d", got-baselineNotifies)
	}

	// 6. Optimistic conflict during ReserveStock retry -> rolled-back attempt leaves NO orphan outbox row!
	var conflictInjected atomic.Bool
	st.BeforeReserveWrite = func(skuID string) {
		if skuID == prod.Skus[0].Id && conflictInjected.CompareAndSwap(false, true) {
			// Concurrent writer bumps SKU version right before commit.
			_, _ = st.UpdateSKUStockOptimistic(context.Background(), skuID, 6, 1)
		}
	}
	defer func() { st.BeforeReserveWrite = nil }()

	if _, err := srv.ReserveStock(buyerCtx, &productv1.ReserveStockRequest{
		OrderId: "ord-retry-clean",
		Items:   []*productv1.StockReservationItem{{SkuId: prod.Skus[0].Id, Quantity: 1}},
	}); err != nil {
		t.Fatalf("ReserveStock with 1 optimistic conflict retry failed: %v", err)
	}
	if got := len(st.OutboxEvents()) - baselineEvents; got != 1 {
		t.Fatalf("expected exactly 1 outbox event from committed retry (no orphan from rolled-back attempt), got %d", got)
	}
}
