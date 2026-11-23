package server

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/product/internal/events"
	"github.com/nexusmart/core/services/product/internal/store"
)

func TestW2B_AdminReviewProduct_TransactionalOutbox(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)
	fixedNow := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	srv.SetClock(func() time.Time { return fixedNow })

	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-outbox-1")

	cat, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name:              "Outbox Cat",
		Slug:              "outbox-cat",
		CommissionRateBps: 500,
	})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	prod, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: cat.GetCategory().GetId(),
		Name:       "Áo Khoác Outbox",
		Skus:       []*productv1.SKUInput{{SkuCode: "OB-1", Name: "L", PriceVnd: 250000, Stock: 10}},
	})
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	prodID := prod.GetProduct().GetId()

	// Reviewing a non-existent product must NOT write any outbox event.
	if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: "missing-prod-id",
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	}); status.Code(err) != codes.NotFound {
		t.Fatalf("AdminReviewProduct missing product: want NotFound, got %v", err)
	}
	if len(st.OutboxEvents()) != 0 {
		t.Fatalf("expected 0 outbox events after failed review, got %d", len(st.OutboxEvents()))
	}

	// 1. REJECT writes product.reviewed outbox event atomically and triggers relay channel.
	if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: prodID,
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_REJECT,
		Reason:    "Thiếu ảnh chụp thật",
	}); err != nil {
		t.Fatalf("AdminReviewProduct REJECT: %v", err)
	}
	select {
	case <-srv.OutboxTrigger():
	default:
		t.Fatalf("expected outbox trigger signal after REJECT")
	}

	// 2. APPROVE writes second product.reviewed outbox event.
	fixedNow = fixedNow.Add(5 * time.Minute)
	if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: prodID,
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	}); err != nil {
		t.Fatalf("AdminReviewProduct APPROVE: %v", err)
	}

	evs := st.OutboxEvents()
	if len(evs) != 2 {
		t.Fatalf("expected 2 outbox events, got %d", len(evs))
	}

	var p0, p1 events.ProductReviewedPayload
	if err := json.Unmarshal(evs[0].PayloadJSON, &p0); err != nil {
		t.Fatalf("unmarshal ev[0]: %v", err)
	}
	if evs[0].EventType != events.SubjectProductReviewed || p0.ProductID != prodID || p0.ProductName != "Áo Khoác Outbox" ||
		p0.ShopID != "shop-outbox-1" || p0.Decision != "REJECT" || p0.Reason != "Thiếu ảnh chụp thật" || p0.OccurredAt == "" {
		t.Fatalf("unexpected REJECT outbox event: ev=%+v payload=%+v", evs[0], p0)
	}

	if err := json.Unmarshal(evs[1].PayloadJSON, &p1); err != nil {
		t.Fatalf("unmarshal ev[1]: %v", err)
	}
	if evs[1].EventType != events.SubjectProductReviewed || p1.ProductID != prodID ||
		p1.ShopID != "shop-outbox-1" || p1.Decision != "APPROVE" || p1.Reason != "" || p1.OccurredAt != fixedNow.Format(time.RFC3339) {
		t.Fatalf("unexpected APPROVE outbox event: ev=%+v payload=%+v", evs[1], p1)
	}
}

func TestW2B_DeleteCategory_CATB8_SoftDeletedProducts(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)
	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")

	catResp, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name:              "Soft Delete Guard Cat",
		Slug:              "soft-delete-guard-cat",
		CommissionRateBps: 500,
	})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	catID := catResp.GetCategory().GetId()

	prodResp, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: catID,
		Name:       "Prod To Delete",
		Skus:       []*productv1.SKUInput{{SkuCode: "CATB8-1", Name: "Default", PriceVnd: 100000, Stock: 5}},
	})
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}

	if _, err := srv.DeleteProduct(sellerCtx, &productv1.DeleteProductRequest{Id: prodResp.GetProduct().GetId()}); err != nil {
		t.Fatalf("DeleteProduct: %v", err)
	}

	// CAT-B8: Category still referenced by a soft-deleted product MUST return FailedPrecondition (HTTP 409), not succeed or 500.
	_, err = srv.DeleteCategory(adminCtx, &productv1.DeleteCategoryRequest{Id: catID})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("DeleteCategory with soft-deleted product: want FailedPrecondition (409), got %v", err)
	}

	// Truly empty category (never had products or children) CAN be deleted.
	emptyCat, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name:              "Empty Cat",
		Slug:              "empty-cat",
		CommissionRateBps: 500,
	})
	if err != nil {
		t.Fatalf("CreateCategory empty: %v", err)
	}
	delResp, err := srv.DeleteCategory(adminCtx, &productv1.DeleteCategoryRequest{Id: emptyCat.GetCategory().GetId()})
	if err != nil || !delResp.GetOk() {
		t.Fatalf("DeleteCategory empty: got resp=%+v err=%v", delResp, err)
	}
}

func TestW2B_CategoryTree_CATB9_CycleRecursiveSlugPresenceFlags(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)
	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")

	// 1. Slug normalization & uniqueness on CreateCategory / UpdateCategory.
	badSlugResp, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name:              "Thời Trang Nam",
		Slug:              "AUDIT-cat / Có Dấu!",
		CommissionRateBps: 600,
		SortOrder:         10,
	})
	if err != nil {
		t.Fatalf("CreateCategory with raw slug: %v", err)
	}
	if got := badSlugResp.GetCategory().GetSlug(); got != "audit-cat-co-dau" {
		t.Fatalf("CreateCategory slug normalization: got %q, want %q", got, "audit-cat-co-dau")
	}
	if _, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name: "Dup After Norm",
		Slug: "AUDIT-CAT / CÓ DẤU!",
	}); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("CreateCategory duplicate normalized slug: want AlreadyExists, got %v", err)
	}
	if _, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name: "Invalid Punctuation Slug",
		Slug: "!!! --- ///",
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("CreateCategory invalid slug: want InvalidArgument, got %v", err)
	}

	// Auto-generated slug suffixes cleanly on duplicate category name when slug omitted.
	auto1, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{Name: "Phụ Kiện Mới", CommissionRateBps: 500})
	if err != nil {
		t.Fatalf("CreateCategory auto1: %v", err)
	}
	auto2, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{Name: "Phụ Kiện Mới", CommissionRateBps: 500})
	if err != nil {
		t.Fatalf("CreateCategory auto2: %v", err)
	}
	if auto1.GetCategory().GetSlug() != "phu-kien-moi" || auto2.GetCategory().GetSlug() == auto1.GetCategory().GetSlug() {
		t.Fatalf("expected unique auto slugs, got %q and %q", auto1.GetCategory().GetSlug(), auto2.GetCategory().GetSlug())
	}

	// 2. Build 4-level tree: Cha -> Con -> Chau -> Chat.
	chaResp, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name:              "Cha",
		Slug:              "cha",
		CommissionRateBps: 500,
		SortOrder:         1,
	})
	if err != nil {
		t.Fatalf("CreateCategory Cha: %v", err)
	}
	chaID := chaResp.GetCategory().GetId()

	conResp, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name:              "Con",
		Slug:              "con",
		ParentId:          chaID,
		CommissionRateBps: 600,
		SortOrder:         2,
	})
	if err != nil {
		t.Fatalf("CreateCategory Con: %v", err)
	}
	conID := conResp.GetCategory().GetId()

	chauResp, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name:              "Cháu",
		Slug:              "chau",
		ParentId:          conID,
		CommissionRateBps: 700,
		SortOrder:         3,
	})
	if err != nil {
		t.Fatalf("CreateCategory Chau: %v", err)
	}
	chauID := chauResp.GetCategory().GetId()

	chatResp, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name:              "Chắt",
		Slug:              "chat",
		ParentId:          chauID,
		CommissionRateBps: 750,
		SortOrder:         4,
	})
	if err != nil {
		t.Fatalf("CreateCategory Chat: %v", err)
	}
	chatID := chatResp.GetCategory().GetId()

	// 3. Cycle prevention: Cha.parent = Con (2-node), Cha.parent = Chau (3-node), Cha.parent = Chat (4-node), self-parent.
	for label, target := range map[string]string{
		"self":   chaID,
		"2-node": conID,
		"3-node": chauID,
		"4-node": chatID,
	} {
		if _, err := srv.UpdateCategory(adminCtx, &productv1.UpdateCategoryRequest{
			Id:          chaID,
			ParentId:    target,
			HasParentId: true,
		}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("UpdateCategory %s cycle: want InvalidArgument, got %v", label, err)
		}
	}

	// 3b. Concurrent cycle prevention: two roots racing to reparent to each other cannot both succeed.
	// Note: This runs on MemoryStore where LockCategoryTree is a no-op and commit serialization relies
	// on MemoryStore's commit mutex; real Postgres pg_advisory_xact_lock (LockCategoryTree) is verified
	// in pg_integration_test.go (TestPGStore_W2BIntegration/CategoryTreeLock_RecursiveCTE_And_DeleteGuard).
	raceA, _ := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{Name: "Race A", Slug: "race-a", CommissionRateBps: 500})
	raceB, _ := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{Name: "Race B", Slug: "race-b", CommissionRateBps: 500})
	var okCount atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		if _, rErr := srv.UpdateCategory(adminCtx, &productv1.UpdateCategoryRequest{
			Id: raceA.GetCategory().GetId(), ParentId: raceB.GetCategory().GetId(), HasParentId: true,
		}); rErr == nil {
			okCount.Add(1)
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		if _, rErr := srv.UpdateCategory(adminCtx, &productv1.UpdateCategoryRequest{
			Id: raceB.GetCategory().GetId(), ParentId: raceA.GetCategory().GetId(), HasParentId: true,
		}); rErr == nil {
			okCount.Add(1)
		}
	}()
	close(start)
	wg.Wait()
	if okCount.Load() != 1 {
		t.Fatalf("concurrent mutual reparent: expected exactly 1 success (cycle blocked), got %d", okCount.Load())
	}

	// 4. Presence flags: partial UpdateCategory omitting commission_rate_bps, sort_order, parent_id preserves existing values.
	partialUpd, err := srv.UpdateCategory(adminCtx, &productv1.UpdateCategoryRequest{
		Id:   chauID,
		Name: "Cháu Đổi Tên",
	})
	if err != nil {
		t.Fatalf("UpdateCategory partial: %v", err)
	}
	gotChau := partialUpd.GetCategory()
	if gotChau.GetCommissionRateBps() != 700 || gotChau.GetSortOrder() != 3 || gotChau.GetParentId() != conID {
		t.Fatalf("UpdateCategory partial reset fields: bps=%d sort=%d parent=%q, want 700/3/%q",
			gotChau.GetCommissionRateBps(), gotChau.GetSortOrder(), gotChau.GetParentId(), conID)
	}

	// Explicit presence flags CAN set commission_rate_bps=0, sort_order=0, parent_id="".
	explicitUpd, err := srv.UpdateCategory(adminCtx, &productv1.UpdateCategoryRequest{
		Id:                   badSlugResp.GetCategory().GetId(),
		CommissionRateBps:    0,
		HasCommissionRateBps: true,
		SortOrder:            0,
		HasSortOrder:         true,
		Slug:                 "New / Slug!",
	})
	if err != nil {
		t.Fatalf("UpdateCategory explicit zero: %v", err)
	}
	if explicitUpd.GetCategory().GetCommissionRateBps() != 0 || explicitUpd.GetCategory().GetSortOrder() != 0 || explicitUpd.GetCategory().GetSlug() != "new-slug" {
		t.Fatalf("UpdateCategory explicit zero unexpected: %+v", explicitUpd.GetCategory())
	}

	// Explicit HasParentId=true with ParentId="" detaches child back to root.
	detachChild, _ := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{Name: "Detachable", Slug: "detachable", ParentId: chaID, CommissionRateBps: 500})
	detached, err := srv.UpdateCategory(adminCtx, &productv1.UpdateCategoryRequest{
		Id:          detachChild.GetCategory().GetId(),
		ParentId:    "",
		HasParentId: true,
	})
	if err != nil || detached.GetCategory().GetParentId() != "" {
		t.Fatalf("expected detached category parent_id=\"\", got %+v err=%v", detached, err)
	}

	// 5. Recursive category filtering: products in Chau (level 3) and Chat (level 4) appear when filtering by Cha.
	pChau, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: chauID,
		Name:       "Sản phẩm cấp Cháu",
		Skus:       []*productv1.SKUInput{{SkuCode: "CHAU-1", Name: "Std", PriceVnd: 180000, Stock: 10}},
	})
	if err != nil {
		t.Fatalf("CreateProduct in Chau: %v", err)
	}
	pChat, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: chatID,
		Name:       "Sản phẩm cấp Chắt",
		Skus:       []*productv1.SKUInput{{SkuCode: "CHAT-1", Name: "Std", PriceVnd: 190000, Stock: 10}},
	})
	if err != nil {
		t.Fatalf("CreateProduct in Chat: %v", err)
	}
	for _, pid := range []string{pChau.GetProduct().GetId(), pChat.GetProduct().GetId()} {
		if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
			ProductId: pid,
			Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
		}); err != nil {
			t.Fatalf("AdminReviewProduct %s: %v", pid, err)
		}
	}

	byCha, err := srv.ListProducts(context.Background(), &productv1.ListProductsRequest{
		CategoryId: chaID,
		Page:       1,
		PageSize:   20,
	})
	if err != nil {
		t.Fatalf("ListProducts by Cha: %v", err)
	}
	if byCha.GetTotal() != 2 || len(byCha.GetProducts()) != 2 {
		t.Fatalf("ListProducts by Cha (recursive 4-level): want 2 products, got total=%d products=%+v",
			byCha.GetTotal(), byCha.GetProducts())
	}
}

func TestW2B_SKUPriceHistory_MinPrice30d_FakeClock(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	srv.SetClock(func() time.Time { return now })

	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")

	cat, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name:              "Price Hist Cat",
		Slug:              "price-hist-cat",
		CommissionRateBps: 500,
	})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}

	// Day 0: Create SKU at 100,000 VND.
	prod, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: cat.GetCategory().GetId(),
		Name:       "Áo Khoác Test Giá",
		Skus: []*productv1.SKUInput{
			{SkuCode: "PH-1", Name: "M", PriceVnd: 100000, Stock: 10},
		},
	})
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	prodID := prod.GetProduct().GetId()
	skuID := prod.GetProduct().GetSkus()[0].GetId()
	if got := prod.GetProduct().GetSkus()[0].GetMinPrice_30DVnd(); got != 100000 {
		t.Fatalf("Day 0 CreateProduct min_price_30d_vnd = %d, want 100000", got)
	}

	assertMinPrice := func(label string, want int64) {
		t.Helper()
		gp, err := srv.GetProduct(sellerCtx, &productv1.GetProductRequest{Id: prodID})
		if err != nil {
			t.Fatalf("%s GetProduct: %v", label, err)
		}
		if got := gp.GetProduct().GetSkus()[0].GetMinPrice_30DVnd(); got != want {
			t.Fatalf("%s GetProduct min_price_30d_vnd = %d, want %d", label, got, want)
		}
		gb, err := srv.GetSKUsBatch(context.Background(), &productv1.GetSKUsBatchRequest{SkuIds: []string{skuID}})
		if err != nil {
			t.Fatalf("%s GetSKUsBatch: %v", label, err)
		}
		if len(gb.GetSkus()) != 1 || gb.GetSkus()[0].GetMinPrice_30DVnd() != want {
			t.Fatalf("%s GetSKUsBatch min_price_30d_vnd = %+v, want %d", label, gb.GetSkus(), want)
		}
	}

	// Day 40: Raise price to 200,000 VND.
	// Price 100,000 was active at Day 10..40 (in effect BEFORE the 30-day cutoff at Day 10!),
	// so min_price_30d_vnd MUST still be 100,000!
	now = now.Add(40 * 24 * time.Hour)
	if _, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id: prodID,
		Skus: []*productv1.SKUInput{
			{Id: skuID, SkuCode: "PH-1", Name: "M", PriceVnd: 200000, Stock: 10},
		},
	}); err != nil {
		t.Fatalf("Day 40 UpdateProduct: %v", err)
	}
	assertMinPrice("Day 40 (price in effect before 30d cutoff)", 100000)

	// Day 69: 100,000 was STILL in effect until Day 40 (29 days ago -> within 30d window).
	now = now.Add(29 * 24 * time.Hour)
	assertMinPrice("Day 69 (29d after raise)", 100000)

	// Day 71: 31 days after raise to 200,000. At cutoff (Day 41), the price in effect was 200,000,
	// so 100,000 has aged out of the 30-day window -> min_price_30d_vnd = 200,000!
	now = now.Add(2 * 24 * time.Hour)
	assertMinPrice("Day 71 (31d after raise)", 200000)

	// Day 72: Lower price to 120,000, then Day 73 raise to 180,000 -> min_price_30d_vnd = 120,000.
	now = now.Add(24 * time.Hour)
	if _, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id:   prodID,
		Skus: []*productv1.SKUInput{{Id: skuID, SkuCode: "PH-1", Name: "M", PriceVnd: 120000, Stock: 10}},
	}); err != nil {
		t.Fatalf("Day 72 drop: %v", err)
	}
	now = now.Add(24 * time.Hour)
	if _, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id:   prodID,
		Skus: []*productv1.SKUInput{{Id: skuID, SkuCode: "PH-1", Name: "M", PriceVnd: 180000, Stock: 10}},
	}); err != nil {
		t.Fatalf("Day 73 raise: %v", err)
	}
	assertMinPrice("Day 73 (dip to 120k inside window)", 120000)

	// Metadata-only update with unchanged price does NOT add redundant price_history row.
	histBefore := len(st.PriceHistory())
	if _, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id:   prodID,
		Skus: []*productv1.SKUInput{{Id: skuID, SkuCode: "PH-1", Name: "M (v2)", PriceVnd: 180000, Stock: 10}},
	}); err != nil {
		t.Fatalf("metadata-only update: %v", err)
	}
	if len(st.PriceHistory()) != histBefore {
		t.Fatalf("metadata-only update wrote unexpected price_history row: before=%d after=%d", histBefore, len(st.PriceHistory()))
	}
}

func TestW2B_GetCategoryDescendants_And_FeeSchedules_FakeClock(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	srv.SetClock(func() time.Time { return now })

	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")
	svcCtx := auth.WithClaims(context.Background(), &auth.Claims{
		Role:    auth.ServiceRole,
		Service: "promotion-service",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: "promotion-service",
		},
	})

	root, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{Name: "Root", Slug: "root-d", CommissionRateBps: 500})
	if err != nil {
		t.Fatalf("CreateCategory root: %v", err)
	}
	child, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{Name: "Child", Slug: "child-d", ParentId: root.GetCategory().GetId(), CommissionRateBps: 500})
	if err != nil {
		t.Fatalf("CreateCategory child: %v", err)
	}
	grand, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{Name: "Grand", Slug: "grand-d", ParentId: child.GetCategory().GetId(), CommissionRateBps: 500})
	if err != nil {
		t.Fatalf("CreateCategory grand: %v", err)
	}
	other, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{Name: "Other", Slug: "other-d", CommissionRateBps: 400})
	if err != nil {
		t.Fatalf("CreateCategory other: %v", err)
	}

	// 1. GetCategoryDescendants: anonymous rejected, service token & admin allowed, overlapping inputs deduplicated.
	if _, err := srv.GetCategoryDescendants(context.Background(), &productv1.GetCategoryDescendantsRequest{
		CategoryIds: []string{root.GetCategory().GetId()},
	}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("GetCategoryDescendants anonymous: want Unauthenticated, got %v", err)
	}
	descResp, err := srv.GetCategoryDescendants(svcCtx, &productv1.GetCategoryDescendantsRequest{
		CategoryIds: []string{root.GetCategory().GetId(), child.GetCategory().GetId()},
	})
	if err != nil {
		t.Fatalf("GetCategoryDescendants service token: %v", err)
	}
	if len(descResp.GetCategoryIds()) != 3 {
		t.Fatalf("GetCategoryDescendants want 3 deduplicated ids, got %v", descResp.GetCategoryIds())
	}
	for _, id := range descResp.GetCategoryIds() {
		if id == other.GetCategory().GetId() {
			t.Fatalf("unexpected sibling category %s in descendants", id)
		}
	}

	// 2. SetCategoryFeeSchedule validation: past effective_from, non-admin, invalid bps, unknown category.
	if _, err := srv.SetCategoryFeeSchedule(adminCtx, &productv1.SetCategoryFeeScheduleRequest{
		CategoryId:        grand.GetCategory().GetId(),
		CommissionRateBps: 800,
		EffectiveFrom:     timestamppb.New(now.Add(-time.Second)),
		Note:              "past invalid",
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("SetCategoryFeeSchedule past effective_from: want InvalidArgument, got %v", err)
	}
	if _, err := srv.SetCategoryFeeSchedule(sellerCtx, &productv1.SetCategoryFeeScheduleRequest{
		CategoryId:        grand.GetCategory().GetId(),
		CommissionRateBps: 800,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("SetCategoryFeeSchedule seller: want PermissionDenied, got %v", err)
	}
	if _, err := srv.SetCategoryFeeSchedule(adminCtx, &productv1.SetCategoryFeeScheduleRequest{
		CategoryId:        grand.GetCategory().GetId(),
		CommissionRateBps: 10001,
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("SetCategoryFeeSchedule bps>10000: want InvalidArgument, got %v", err)
	}
	if _, err := srv.SetCategoryFeeSchedule(adminCtx, &productv1.SetCategoryFeeScheduleRequest{
		CategoryId:        "non-existent-cat",
		CommissionRateBps: 600,
	}); status.Code(err) != codes.NotFound {
		t.Fatalf("SetCategoryFeeSchedule missing category: want NotFound, got %v", err)
	}

	prod, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: grand.GetCategory().GetId(),
		Name:       "Fee Test Prod",
		Skus:       []*productv1.SKUInput{{SkuCode: "FEE-1", Name: "Std", PriceVnd: 100000, Stock: 5}},
	})
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	skuID := prod.GetProduct().GetSkus()[0].GetId()

	// 3. Schedule a future commission change (+24h -> 850 bps).
	futureEff := now.Add(24 * time.Hour)
	schedResp, err := srv.SetCategoryFeeSchedule(adminCtx, &productv1.SetCategoryFeeScheduleRequest{
		CategoryId:        grand.GetCategory().GetId(),
		CommissionRateBps: 850,
		EffectiveFrom:     timestamppb.New(futureEff),
		Note:              "June promo commission",
	})
	if err != nil {
		t.Fatalf("SetCategoryFeeSchedule future: %v", err)
	}
	if schedResp.GetSchedule().GetCommissionRateBps() != 850 || schedResp.GetSchedule().GetCreatedBy() != "admin-1" {
		t.Fatalf("unexpected schedule response: %+v", schedResp.GetSchedule())
	}

	// Before effective_from: SKU.commission_rate_bps must STILL be 500.
	beforeBatch, err := srv.GetSKUsBatch(context.Background(), &productv1.GetSKUsBatchRequest{SkuIds: []string{skuID}})
	if err != nil || len(beforeBatch.GetSkus()) != 1 || beforeBatch.GetSkus()[0].GetCommissionRateBps() != 500 {
		t.Fatalf("before effective_from: want 500 bps, got %+v err=%v", beforeBatch.GetSkus(), err)
	}

	// Partial UpdateCategory (renaming only) must NOT cancel or overwrite the future fee schedule.
	if _, err := srv.UpdateCategory(adminCtx, &productv1.UpdateCategoryRequest{
		Id:   grand.GetCategory().GetId(),
		Name: "Grand Renamed",
	}); err != nil {
		t.Fatalf("UpdateCategory rename: %v", err)
	}

	// Advance fake clock to effective_from (+24h): SKU.commission_rate_bps switches to 850!
	now = futureEff
	afterBatch, err := srv.GetSKUsBatch(context.Background(), &productv1.GetSKUsBatchRequest{SkuIds: []string{skuID}})
	if err != nil || len(afterBatch.GetSkus()) != 1 || afterBatch.GetSkus()[0].GetCommissionRateBps() != 850 {
		t.Fatalf("at effective_from: want 850 bps, got %+v err=%v", afterBatch.GetSkus(), err)
	}

	// Immediate schedule (nil effective_from) overrides immediately to 900 bps.
	now = now.Add(time.Minute)
	if _, err := srv.SetCategoryFeeSchedule(adminCtx, &productv1.SetCategoryFeeScheduleRequest{
		CategoryId:        grand.GetCategory().GetId(),
		CommissionRateBps: 900,
		Note:              "immediate adjustment",
	}); err != nil {
		t.Fatalf("SetCategoryFeeSchedule immediate: %v", err)
	}
	immBatch, err := srv.GetSKUsBatch(context.Background(), &productv1.GetSKUsBatchRequest{SkuIds: []string{skuID}})
	if err != nil || len(immBatch.GetSkus()) != 1 || immBatch.GetSkus()[0].GetCommissionRateBps() != 900 {
		t.Fatalf("after immediate schedule: want 900 bps, got %+v err=%v", immBatch.GetSkus(), err)
	}

	// 4. ListCategoryFeeSchedules returns complete history (initial 500, future 850, immediate 900) newest-first.
	listResp, err := srv.ListCategoryFeeSchedules(adminCtx, &productv1.ListCategoryFeeSchedulesRequest{
		CategoryId: grand.GetCategory().GetId(),
		Page:       1,
		PageSize:   10,
	})
	if err != nil {
		t.Fatalf("ListCategoryFeeSchedules: %v", err)
	}
	if listResp.GetTotal() != 3 || len(listResp.GetSchedules()) != 3 {
		t.Fatalf("expected 3 fee schedules in history, got total=%d len=%d", listResp.GetTotal(), len(listResp.GetSchedules()))
	}
	if listResp.GetSchedules()[0].GetCommissionRateBps() != 900 ||
		listResp.GetSchedules()[1].GetCommissionRateBps() != 850 ||
		listResp.GetSchedules()[2].GetCommissionRateBps() != 500 {
		t.Fatalf("expected newest-first order [900,850,500], got %+v", listResp.GetSchedules())
	}

	// Listing with empty category_id returns schedules across all categories.
	allList, err := srv.ListCategoryFeeSchedules(adminCtx, &productv1.ListCategoryFeeSchedulesRequest{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("ListCategoryFeeSchedules all: %v", err)
	}
	if allList.GetTotal() < 6 {
		t.Fatalf("expected at least 6 total schedules across 4 categories, got %d", allList.GetTotal())
	}
}
