package server

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	"github.com/nexusmart/core/services/product/internal/events"
	"github.com/nexusmart/core/services/product/internal/store"
)

func TestW2B_Round2_AdminReviewProduct_IdempotentZeroStockHiddenDeletedAndTakedown(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)
	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-r2")

	catResp, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name:              "Round2 Review Cat",
		Slug:              "round2-review-cat",
		CommissionRateBps: 500,
	})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	catID := catResp.GetCategory().GetId()

	// 1. Approving PENDING product with stock > 0 -> APPROVED + 1 outbox event.
	//    Re-approving already APPROVED product -> returns current APPROVED, 0 extra outbox events.
	p1Resp, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: catID,
		Name:       "In-Stock Product",
		Skus:       []*productv1.SKUInput{{SkuCode: "R2-IN-1", Name: "M", PriceVnd: 150000, Stock: 5}},
	})
	if err != nil {
		t.Fatalf("CreateProduct p1: %v", err)
	}
	p1ID := p1Resp.GetProduct().GetId()

	reviewedEvents := func() []store.OutboxEvent {
		var out []store.OutboxEvent
		for _, ev := range st.OutboxEvents() {
			if ev.EventType == events.SubjectProductReviewed {
				out = append(out, ev)
			}
		}
		return out
	}

	rev1, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: p1ID,
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	})
	if err != nil || rev1.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_APPROVED {
		t.Fatalf("first APPROVE: resp=%+v err=%v", rev1, err)
	}
	if len(reviewedEvents()) != 1 {
		t.Fatalf("after first APPROVE: expected 1 product.reviewed outbox event, got %d", len(reviewedEvents()))
	}
	outboxAfterFirstApprove := len(st.OutboxEvents())

	rev1Dup, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: p1ID,
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	})
	if err != nil || rev1Dup.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_APPROVED {
		t.Fatalf("duplicate APPROVE: resp=%+v err=%v", rev1Dup, err)
	}
	if len(st.OutboxEvents()) != outboxAfterFirstApprove {
		t.Fatalf("duplicate APPROVE MUST NOT emit extra outbox event, got %d (was %d)", len(st.OutboxEvents()), outboxAfterFirstApprove)
	}

	// 2. Admin takedown APPROVED -> REJECTED is allowed and emits 1 product.reviewed event.
	//    Re-rejecting with same reason is idempotent (0 extra events).
	//    Re-rejecting with a new reason updates rejection_reason and emits 1 product.reviewed event.
	rej1, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: p1ID,
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_REJECT,
		Reason:    "Vi phạm nhãn hiệu",
	})
	if err != nil || rej1.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_REJECTED {
		t.Fatalf("takedown APPROVED->REJECTED: resp=%+v err=%v", rej1, err)
	}
	if len(reviewedEvents()) != 2 {
		t.Fatalf("after takedown REJECT: expected 2 product.reviewed outbox events, got %d", len(reviewedEvents()))
	}
	outboxAfterRej1 := len(st.OutboxEvents())

	rej1Same, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: p1ID,
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_REJECT,
		Reason:    "  Vi phạm nhãn hiệu  ",
	})
	if err != nil || rej1Same.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_REJECTED {
		t.Fatalf("duplicate REJECT same reason: resp=%+v err=%v", rej1Same, err)
	}
	if len(st.OutboxEvents()) != outboxAfterRej1 {
		t.Fatalf("duplicate REJECT with same reason MUST NOT emit extra event, got %d (was %d)", len(st.OutboxEvents()), outboxAfterRej1)
	}

	rej1Diff, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: p1ID,
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_REJECT,
		Reason:    "Thiếu chứng từ xuất xứ",
	})
	if err != nil || rej1Diff.GetProduct().GetRejectionReason() != "Thiếu chứng từ xuất xứ" {
		t.Fatalf("REJECT with updated reason: resp=%+v err=%v", rej1Diff, err)
	}
	if len(reviewedEvents()) != 3 {
		t.Fatalf("REJECT with different reason should emit product.reviewed event, got %d", len(reviewedEvents()))
	}

	// 3. Approving PENDING product whose SKUs all have stock == 0 -> OUT_OF_STOCK (using syncProductStockStatus).
	//    Re-approving OUT_OF_STOCK -> returns OUT_OF_STOCK with 0 extra outbox events.
	pZeroResp, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: catID,
		Name:       "Zero-Stock Product",
		Skus:       []*productv1.SKUInput{{SkuCode: "R2-ZERO-1", Name: "M", PriceVnd: 150000, Stock: 0}},
	})
	if err != nil {
		t.Fatalf("CreateProduct zero stock: %v", err)
	}
	pZeroID := pZeroResp.GetProduct().GetId()

	revZero, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: pZeroID,
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	})
	if err != nil {
		t.Fatalf("APPROVE zero-stock product: %v", err)
	}
	if revZero.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_OUT_OF_STOCK {
		t.Fatalf("approving 0-stock product: want OUT_OF_STOCK, got %v", revZero.GetProduct().GetStatus())
	}
	revEvs := reviewedEvents()
	if len(revEvs) != 4 {
		t.Fatalf("after approving 0-stock product: expected 4 product.reviewed outbox events, got %d", len(revEvs))
	}
	var lastPayload events.ProductReviewedPayload
	if err := json.Unmarshal(revEvs[3].PayloadJSON, &lastPayload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if lastPayload.ProductID != pZeroID || lastPayload.Decision != "APPROVE" {
		t.Fatalf("unexpected outbox payload for 0-stock approval: %+v", lastPayload)
	}
	outboxAfterZeroApprove := len(st.OutboxEvents())

	revZeroDup, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: pZeroID,
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	})
	if err != nil || revZeroDup.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_OUT_OF_STOCK {
		t.Fatalf("duplicate APPROVE on OUT_OF_STOCK: resp=%+v err=%v", revZeroDup, err)
	}
	if len(st.OutboxEvents()) != outboxAfterZeroApprove {
		t.Fatalf("duplicate APPROVE on OUT_OF_STOCK MUST NOT emit extra event, got %d (was %d)", len(st.OutboxEvents()), outboxAfterZeroApprove)
	}

	// 4. Seller-HIDDEN product: APPROVE MUST NOT re-open listing (returns FailedPrecondition, stays HIDDEN, 0 events).
	pHidResp, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: catID,
		Name:       "Seller Hidden Product",
		Skus:       []*productv1.SKUInput{{SkuCode: "R2-HID-1", Name: "M", PriceVnd: 150000, Stock: 10}},
	})
	if err != nil {
		t.Fatalf("CreateProduct hidden test: %v", err)
	}
	pHidID := pHidResp.GetProduct().GetId()
	if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: pHidID,
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	}); err != nil {
		t.Fatalf("approve before hide: %v", err)
	}
	if _, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id:     pHidID,
		Status: productv1.ProductStatus_PRODUCT_STATUS_HIDDEN,
	}); err != nil {
		t.Fatalf("seller hide product: %v", err)
	}
	eventsBeforeHiddenApprove := len(st.OutboxEvents())

	if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: pHidID,
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("APPROVE on seller-HIDDEN product: want FailedPrecondition, got %v", err)
	}
	if len(st.OutboxEvents()) != eventsBeforeHiddenApprove {
		t.Fatalf("APPROVE on seller-HIDDEN product must not emit event")
	}
	gotHid, err := srv.GetProduct(sellerCtx, &productv1.GetProductRequest{Id: pHidID})
	if err != nil || gotHid.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_HIDDEN {
		t.Fatalf("seller-HIDDEN product status must remain HIDDEN, got %+v err=%v", gotHid, err)
	}

	// Admin REJECT on seller-HIDDEN product is still allowed (moderation takedown).
	if rejHid, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: pHidID,
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_REJECT,
		Reason:    "Hàng cấm kinh doanh",
	}); err != nil || rejHid.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_REJECTED {
		t.Fatalf("REJECT on seller-HIDDEN product: want REJECTED, got resp=%+v err=%v", rejHid, err)
	}

	// 5. Soft-deleted product: APPROVE returns NotFound, does NOT resurrect or emit event.
	if _, err := srv.DeleteProduct(sellerCtx, &productv1.DeleteProductRequest{Id: pHidID}); err != nil {
		t.Fatalf("DeleteProduct: %v", err)
	}
	eventsBeforeDeletedApprove := len(st.OutboxEvents())
	if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: pHidID,
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	}); status.Code(err) != codes.NotFound {
		t.Fatalf("APPROVE on soft-deleted product: want NotFound, got %v", err)
	}
	if len(st.OutboxEvents()) != eventsBeforeDeletedApprove {
		t.Fatalf("APPROVE on soft-deleted product must not emit event")
	}
}

func TestW2B_Round2_ReleaseBeforeReserve_TombstonePreventsLeakedStock(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)

	_, prod := setupApprovedProduct(t, srv, "shop-tomb", []*productv1.SKUInput{
		{SkuCode: "TOMB-1", Name: "M", PriceVnd: 100000, Stock: 10, WeightGrams: 200},
	})
	skuID := prod.GetSkus()[0].GetId()

	svcCtx := ctxWithService("order-service")

	// 1. Sequential: ReleaseStock arrives BEFORE ReserveStock for the same order_id.
	orderID := "ord-release-before-reserve"
	relResp, err := srv.ReleaseStock(svcCtx, &productv1.ReleaseStockRequest{OrderId: orderID})
	if err != nil {
		t.Fatalf("early ReleaseStock: %v", err)
	}
	if relResp.GetReleased() || relResp.GetAlreadyReleased() {
		t.Fatalf("early ReleaseStock without reservation should report Released=false AlreadyReleased=false, got %+v", relResp)
	}

	// Delayed ReserveStock arriving after ReleaseStock MUST be rejected and leave reserved_stock == 0!
	if _, err := srv.ReserveStock(context.Background(), &productv1.ReserveStockRequest{
		OrderId: orderID,
		Items:   []*productv1.StockReservationItem{{SkuId: skuID, Quantity: 3}},
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("delayed ReserveStock after ReleaseStock: want FailedPrecondition, got %v", err)
	}

	skAfter, err := st.GetSKUWithContext(context.Background(), skuID)
	if err != nil {
		t.Fatalf("GetSKUWithContext: %v", err)
	}
	if skAfter.ReservedStock != 0 {
		t.Fatalf("delayed ReserveStock leaked reserved_stock=%d (want 0)", skAfter.ReservedStock)
	}

	// ConfirmStock on a tombstoned order returns FailedPrecondition (already released).
	if _, err := srv.ConfirmStock(svcCtx, &productv1.ConfirmStockRequest{OrderId: orderID}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("ConfirmStock on tombstoned order: want FailedPrecondition, got %v", err)
	}

	// 2. Concurrent ReserveStock vs ReleaseStock on the same order_id must NEVER leak reserved_stock.
	// Note: This runs on MemoryStore where LockStockOrder is a no-op and commit serialization relies
	// on MemoryStore's optimistic staging mutex; real Postgres pg_advisory_xact_lock (LockStockOrder)
	// is verified in pg_integration_test.go (TestPGStore_W2BIntegration/ReserveStock_ReleaseStock_TombstoneAndConcurrency_RealPG).
	for i := 0; i < 10; i++ {
		raceOrderID := "ord-race-tomb-" + string(rune('a'+i))
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, _ = srv.ReserveStock(context.Background(), &productv1.ReserveStockRequest{
				OrderId: raceOrderID,
				Items:   []*productv1.StockReservationItem{{SkuId: skuID, Quantity: 2}},
			})
		}()
		go func() {
			defer wg.Done()
			<-start
			_, _ = srv.ReleaseStock(svcCtx, &productv1.ReleaseStockRequest{OrderId: raceOrderID})
		}()
		close(start)
		wg.Wait()
	}

	skFinal, err := st.GetSKUWithContext(context.Background(), skuID)
	if err != nil {
		t.Fatalf("GetSKUWithContext final: %v", err)
	}
	if skFinal.ReservedStock != 0 {
		t.Fatalf("concurrent ReserveStock/ReleaseStock race leaked reserved_stock=%d (want 0)", skFinal.ReservedStock)
	}
}

type fkInjectingStore struct {
	store.Store
	failCreateProduct bool
	failFeeSchedule   bool
}

type fkInjectingQueries struct {
	store.Queries
	failCreateProduct bool
	failFeeSchedule   bool
}

func (f *fkInjectingStore) WithTx(ctx context.Context, fn func(q store.Queries) error) error {
	return f.Store.WithTx(ctx, func(q store.Queries) error {
		return fn(&fkInjectingQueries{
			Queries:           q,
			failCreateProduct: f.failCreateProduct,
			failFeeSchedule:   f.failFeeSchedule,
		})
	})
}

func (f *fkInjectingQueries) CreateProduct(ctx context.Context, p store.Product) (store.Product, error) {
	if f.failCreateProduct {
		return store.Product{}, store.ErrForeignKeyViolation
	}
	return f.Queries.CreateProduct(ctx, p)
}

func (f *fkInjectingQueries) InsertCategoryFeeSchedule(ctx context.Context, sched store.CategoryFeeSchedule) (store.CategoryFeeSchedule, error) {
	if f.failFeeSchedule {
		return store.CategoryFeeSchedule{}, store.ErrForeignKeyViolation
	}
	return f.Queries.InsertCategoryFeeSchedule(ctx, sched)
}

func TestW2B_Round2_FKViolationMappedToNotFound_NeverInternal(t *testing.T) {
	base := store.NewMemoryStore()
	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-fk")

	baseSrv := New(base, nil)
	catResp, err := baseSrv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name:              "FK Cat",
		Slug:              "fk-cat",
		CommissionRateBps: 500,
	})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	catID := catResp.GetCategory().GetId()

	// 1. SetCategoryFeeSchedule receiving ErrForeignKeyViolation -> NotFound (not Internal).
	feeSrv := New(&fkInjectingStore{Store: base, failFeeSchedule: true}, nil)
	_, err = feeSrv.SetCategoryFeeSchedule(adminCtx, &productv1.SetCategoryFeeScheduleRequest{
		CategoryId:        catID,
		CommissionRateBps: 700,
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("SetCategoryFeeSchedule FK violation: want NotFound, got %v", err)
	}

	// 2. CreateProduct receiving ErrForeignKeyViolation -> NotFound (not Internal).
	prodSrv := New(&fkInjectingStore{Store: base, failCreateProduct: true}, nil)
	_, err = prodSrv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: catID,
		Name:       "FK Product",
		Skus:       []*productv1.SKUInput{{SkuCode: "FK-1", Name: "M", PriceVnd: 100000, Stock: 5}},
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("CreateProduct FK violation: want NotFound, got %v", err)
	}
}
