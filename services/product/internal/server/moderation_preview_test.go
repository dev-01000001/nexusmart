package server

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	"github.com/nexusmart/core/services/product/internal/store"
)

type cancelAfterBatchStore struct {
	*indexedBenchStore
	cancelAfter int32
	cancelFn    context.CancelFunc
	scanCalls   atomic.Int32
}

func (s *cancelAfterBatchStore) ListProductsForModerationScan(ctx context.Context, cursorID string, limit int32) ([]store.Product, error) {
	callNum := s.scanCalls.Add(1)
	rows, err := s.indexedBenchStore.ListProductsForModerationScan(ctx, cursorID, limit)
	if callNum == s.cancelAfter && s.cancelFn != nil {
		s.cancelFn()
	}
	return rows, err
}

func TestPreviewBannedKeyword_StopsOnContextCancel(t *testing.T) {
	// Create 1,500 products (3 full batches of 500) so cancelling after batch 1 must stop before batch 2.
	products := make([]store.Product, 1500)
	skusByPID := make(map[string][]store.SKU, 1500)
	for i := range products {
		pid := fmt.Sprintf("prod-%06d", i)
		products[i] = store.Product{
			ID:          pid,
			ShopID:      "shop-1",
			CategoryID:  "cat-1",
			Name:        fmt.Sprintf("Áo thun nam cơ bản #%04d", i),
			Slug:        fmt.Sprintf("ao-thun-nam-co-ban-%04d", i),
			Description: "Mô tả sản phẩm sạch hoàn toàn",
			Status:      store.ProductStatusApproved,
		}
		skusByPID[pid] = []store.SKU{{
			ID:        fmt.Sprintf("sku-%06d", i),
			ProductID: pid,
			Name:      "Đen - M",
			Status:    store.SKUStatusActive,
		}}
	}

	base := &indexedBenchStore{
		MemoryStore: store.NewMemoryStore(),
		products:    products,
		skusByPID:   skusByPID,
	}

	t.Run("CancelAfterFirstBatch", func(t *testing.T) {
		ctx, cancel := context.WithCancel(ctxWithRole("admin-1", "admin", ""))
		defer cancel()

		st := &cancelAfterBatchStore{
			indexedBenchStore: base,
			cancelAfter:       1,
			cancelFn:          cancel,
		}
		srv := New(st, nil)

		_, err := srv.PreviewBannedKeyword(ctx, &productv1.PreviewBannedKeywordRequest{Term: "hàng giả"})
		if status.Code(err) != codes.Canceled {
			t.Fatalf("PreviewBannedKeyword canceled ctx: code=%v err=%v, want Canceled", status.Code(err), err)
		}
		if gotCalls := st.scanCalls.Load(); gotCalls != 1 {
			t.Fatalf("expected scan to stop after batch 1 when ctx canceled, got %d batch queries", gotCalls)
		}
	})

	t.Run("AlreadyCanceledBeforeFirstBatch", func(t *testing.T) {
		ctx, cancel := context.WithCancel(ctxWithRole("admin-1", "admin", ""))
		cancel()

		st := &cancelAfterBatchStore{
			indexedBenchStore: base,
		}
		srv := New(st, nil)

		_, err := srv.PreviewBannedKeyword(ctx, &productv1.PreviewBannedKeywordRequest{Term: "hàng giả"})
		if status.Code(err) != codes.Canceled {
			t.Fatalf("PreviewBannedKeyword pre-canceled ctx: code=%v err=%v, want Canceled", status.Code(err), err)
		}
		if gotCalls := st.scanCalls.Load(); gotCalls != 0 {
			t.Fatalf("expected 0 batch queries on already-canceled ctx, got %d", gotCalls)
		}
	})

	t.Run("DeadlineExceeded", func(t *testing.T) {
		ctx, cancel := context.WithDeadline(ctxWithRole("admin-1", "admin", ""), store.AsOfFromContext(context.Background()).Add(-1))
		defer cancel()

		st := &cancelAfterBatchStore{
			indexedBenchStore: base,
		}
		srv := New(st, nil)

		_, err := srv.PreviewBannedKeyword(ctx, &productv1.PreviewBannedKeywordRequest{Term: "hàng giả"})
		if status.Code(err) != codes.DeadlineExceeded {
			t.Fatalf("PreviewBannedKeyword expired ctx: code=%v err=%v, want DeadlineExceeded", status.Code(err), err)
		}
		if gotCalls := st.scanCalls.Load(); gotCalls != 0 {
			t.Fatalf("expected 0 batch queries on expired ctx, got %d", gotCalls)
		}
	})
}

type moderationQueryAuditStore struct {
	store.Store
	heavySKUCalls atomic.Int32
	lightSKUCalls atomic.Int32
}

type moderationQueryAuditTx struct {
	store.Queries
	parent *moderationQueryAuditStore
}

func (s *moderationQueryAuditStore) ListSKUsByProductIDs(ctx context.Context, productIDs []string) ([]store.SKU, error) {
	s.heavySKUCalls.Add(1)
	return s.Store.ListSKUsByProductIDs(ctx, productIDs)
}

func (s *moderationQueryAuditStore) ListModerationSKUsByProductIDs(ctx context.Context, productIDs []string) ([]store.ModerationSKU, error) {
	s.lightSKUCalls.Add(1)
	return s.Store.ListModerationSKUsByProductIDs(ctx, productIDs)
}

func (s *moderationQueryAuditStore) WithTx(ctx context.Context, fn func(q store.Queries) error) error {
	return s.Store.WithTx(ctx, func(q store.Queries) error {
		return fn(&moderationQueryAuditTx{Queries: q, parent: s})
	})
}

func (q *moderationQueryAuditTx) ListSKUsByProductIDs(ctx context.Context, productIDs []string) ([]store.SKU, error) {
	q.parent.heavySKUCalls.Add(1)
	return q.Queries.ListSKUsByProductIDs(ctx, productIDs)
}

func (q *moderationQueryAuditTx) ListModerationSKUsByProductIDs(ctx context.Context, productIDs []string) ([]store.ModerationSKU, error) {
	q.parent.lightSKUCalls.Add(1)
	return q.Queries.ListModerationSKUsByProductIDs(ctx, productIDs)
}

func TestModerationScan_UsesLightweightSKUQueryAndExcludesInactive(t *testing.T) {
	mem := store.NewMemoryStore()
	setupSrv := New(mem, nil)
	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")

	cat, err := setupSrv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name: "Thời trang", Slug: "thoi-trang-mod-light", CommissionRateBps: 500,
	})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	catID := cat.GetCategory().GetId()

	// Create 1 product with a clean SKU and 1 product whose matching SKU is later deactivated.
	pMatch, err := setupSrv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: catID,
		Name:       "Áo khoác gió nhẹ",
		Skus: []*productv1.SKUInput{{
			SkuCode:    "ML-1",
			Name:       "Màu đặc biệt",
			Attributes: map[string]string{"note": "xuất xứ nhập lậu"},
			PriceVnd:   150000,
			Stock:      10,
		}},
	})
	if err != nil {
		t.Fatalf("CreateProduct match: %v", err)
	}
	pInactiveSKU, err := setupSrv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: catID,
		Name:       "Áo sơ mi cotton",
		Skus: []*productv1.SKUInput{
			{SkuCode: "ML-2A", Name: "Trắng M", PriceVnd: 150000, Stock: 10},
			{SkuCode: "ML-2B", Name: "Phiên bản nhập lậu", PriceVnd: 150000, Stock: 10},
		},
	})
	if err != nil {
		t.Fatalf("CreateProduct inactive SKU: %v", err)
	}
	// Deactivate the matching SKU so only active SKUs should be scanned.
	for _, sk := range pInactiveSKU.GetProduct().GetSkus() {
		if sk.GetSkuCode() == "ML-2B" {
			if err := mem.DeactivateSKU(adminCtx, sk.GetId()); err != nil {
				t.Fatalf("DeactivateSKU: %v", err)
			}
		}
	}

	auditStore := &moderationQueryAuditStore{Store: mem}
	srv := New(auditStore, nil)

	prev, err := srv.PreviewBannedKeyword(adminCtx, &productv1.PreviewBannedKeywordRequest{Term: "nhập lậu"})
	if err != nil {
		t.Fatalf("PreviewBannedKeyword: %v", err)
	}
	if prev.GetAffectedCount() != 1 || len(prev.GetSamples()) != 1 || prev.GetSamples()[0].GetProductId() != pMatch.GetProduct().GetId() {
		t.Fatalf("PreviewBannedKeyword got affected=%d samples=%+v, want 1 (%s)", prev.GetAffectedCount(), prev.GetSamples(), pMatch.GetProduct().GetId())
	}
	if auditStore.heavySKUCalls.Load() != 0 {
		t.Fatalf("PreviewBannedKeyword called heavy ListSKUsByProductIDs %d times, want 0", auditStore.heavySKUCalls.Load())
	}
	if auditStore.lightSKUCalls.Load() == 0 {
		t.Fatalf("PreviewBannedKeyword did not call lightweight ListModerationSKUsByProductIDs")
	}

	// Add keyword and verify RescanProductsForBannedContent also uses ONLY ListModerationSKUsByProductIDs.
	if _, err := setupSrv.CreateBannedKeyword(adminCtx, &productv1.CreateBannedKeywordRequest{
		Term:   "nhập lậu",
		Action: productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_BLOCK,
	}); err != nil {
		t.Fatalf("CreateBannedKeyword: %v", err)
	}
	auditStore.heavySKUCalls.Store(0)
	auditStore.lightSKUCalls.Store(0)

	rescan, err := srv.RescanProductsForBannedContent(adminCtx, &productv1.RescanProductsForBannedContentRequest{})
	if err != nil {
		t.Fatalf("RescanProductsForBannedContent: %v", err)
	}
	if rescan.GetBlockedCount() != 1 {
		t.Fatalf("Rescan blocked=%d, want 1", rescan.GetBlockedCount())
	}
	if auditStore.heavySKUCalls.Load() != 0 {
		t.Fatalf("RescanProductsForBannedContent called heavy ListSKUsByProductIDs %d times, want 0", auditStore.heavySKUCalls.Load())
	}
	if auditStore.lightSKUCalls.Load() < 2 {
		t.Fatalf("expected Rescan to call ListModerationSKUsByProductIDs for batch + locked row, got %d", auditStore.lightSKUCalls.Load())
	}
}

func TestPreviewBannedKeyword_AutoSlugAndCustomSlug(t *testing.T) {
	products := []store.Product{
		{
			ID:          "prod-000001",
			ShopID:      "shop-1",
			CategoryID:  "cat-1",
			Name:        "Sản phẩm hãng giá rẻ bất ngờ",
			Slug:        slugify("Sản phẩm hãng giá rẻ bất ngờ", "prod-prod-000"),
			Description: "Mô tả chuẩn",
			Status:      store.ProductStatusApproved,
		},
		{
			ID:          "prod-000002",
			ShopID:      "shop-1",
			CategoryID:  "cat-1",
			Name:        "Áo thun nam cơ bản",
			Slug:        "custom-hang-gia-00002",
			Description: "Mô tả chuẩn",
			Status:      store.ProductStatusApproved,
		},
	}
	st := &indexedBenchStore{
		MemoryStore: store.NewMemoryStore(),
		products:    products,
		skusByPID:   map[string][]store.SKU{},
	}
	srv := New(st, nil)
	adminCtx := ctxWithRole("admin-1", "admin", "")

	resp, err := srv.PreviewBannedKeyword(adminCtx, &productv1.PreviewBannedKeywordRequest{Term: "hàng giả"})
	if err != nil {
		t.Fatalf("PreviewBannedKeyword: %v", err)
	}
	if resp.GetAffectedCount() != 1 || len(resp.GetSamples()) != 1 || resp.GetSamples()[0].GetProductId() != "prod-000002" {
		t.Fatalf("AffectedCount=%d samples=%+v, want 1 (prod-000002)", resp.GetAffectedCount(), resp.GetSamples())
	}
}
