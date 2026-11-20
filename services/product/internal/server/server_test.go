package server

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/product/internal/store"
)

func ctxWithRole(userID, role, shopID string) context.Context {
	return auth.WithClaims(context.Background(), &auth.Claims{
		Role:   role,
		ShopID: shopID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: userID,
		},
	})
}

func setupApprovedProduct(t *testing.T, srv *Server, shopID string, skus []*productv1.SKUInput) (*productv1.Category, *productv1.Product) {
	t.Helper()
	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-"+shopID, "seller", shopID)

	catResp, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name:              "Electronics " + shopID,
		CommissionRateBps: 500,
		SortOrder:         1,
	})
	if err != nil {
		t.Fatalf("CreateCategory failed: %v", err)
	}

	prodResp, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId:  catResp.Category.Id,
		Name:        "Smartphone Pro",
		Description: "Flagship OLED smartphone",
		Images:      []string{"https://cdn.local/phone.jpg"},
		Skus:        skus,
	})
	if err != nil {
		t.Fatalf("CreateProduct failed: %v", err)
	}
	if prodResp.Product.Status != productv1.ProductStatus_PRODUCT_STATUS_PENDING {
		t.Fatalf("expected initial status PENDING, got %v", prodResp.Product.Status)
	}

	revResp, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		AdminId:   "admin-1",
		ProductId: prodResp.Product.Id,
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	})
	if err != nil {
		t.Fatalf("AdminReviewProduct failed: %v", err)
	}
	if revResp.Product.Status != productv1.ProductStatus_PRODUCT_STATUS_APPROVED {
		t.Fatalf("expected APPROVED, got %v", revResp.Product.Status)
	}

	return catResp.Category, revResp.Product
}

func TestCategoryCRUDAndDeleteGuard(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)

	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")

	// Non-admin cannot create category
	_, err := srv.CreateCategory(sellerCtx, &productv1.CreateCategoryRequest{Name: "Forbidden"})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for seller CreateCategory, got %v", err)
	}

	// Create root and child category
	rootResp, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name:              "Fashion",
		Slug:              "fashion",
		CommissionRateBps: 600,
		SortOrder:         1,
	})
	if err != nil {
		t.Fatalf("create root category: %v", err)
	}

	childResp, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name:              "T-Shirts",
		Slug:              "t-shirts",
		ParentId:          rootResp.Category.Id,
		CommissionRateBps: 650,
		SortOrder:         2,
	})
	if err != nil {
		t.Fatalf("create child category: %v", err)
	}

	// Duplicate slug rejected
	_, err = srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name: "Other Fashion",
		Slug: "fashion",
	})
	if status.Code(err) != codes.AlreadyExists {
		t.Fatalf("expected AlreadyExists for duplicate slug, got %v", err)
	}

	// List all vs by parent_id
	allCats, err := srv.ListCategories(context.Background(), &productv1.ListCategoriesRequest{})
	if err != nil || len(allCats.Categories) != 2 {
		t.Fatalf("expected 2 categories, got %d, err=%v", len(allCats.Categories), err)
	}
	childCats, err := srv.ListCategories(context.Background(), &productv1.ListCategoriesRequest{ParentId: rootResp.Category.Id})
	if err != nil || len(childCats.Categories) != 1 || childCats.Categories[0].Id != childResp.Category.Id {
		t.Fatalf("expected 1 child category, got %v, err=%v", childCats, err)
	}

	// Cannot delete root while child exists
	_, err = srv.DeleteCategory(adminCtx, &productv1.DeleteCategoryRequest{Id: rootResp.Category.Id})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition deleting category with child, got %v", err)
	}

	// Create product in child category
	prodResp, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: childResp.Category.Id,
		Name:       "Cotton Tee",
		Skus: []*productv1.SKUInput{
			{SkuCode: "TEE-M", Name: "Size M", PriceVnd: 150000, Stock: 20, WeightGrams: 200},
		},
	})
	if err != nil {
		t.Fatalf("create product: %v", err)
	}

	// Category delete guard: MUST fail with FailedPrecondition while product exists
	_, err = srv.DeleteCategory(adminCtx, &productv1.DeleteCategoryRequest{Id: childResp.Category.Id})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition deleting category with product, got %v", err)
	}

	// Soft-delete product -> now category CAN be deleted
	if _, err := srv.DeleteProduct(sellerCtx, &productv1.DeleteProductRequest{Id: prodResp.Product.Id}); err != nil {
		t.Fatalf("delete product: %v", err)
	}
	delResp, err := srv.DeleteCategory(adminCtx, &productv1.DeleteCategoryRequest{Id: childResp.Category.Id})
	if err != nil || !delResp.Ok {
		t.Fatalf("expected child category deletion to succeed after product deleted, got %v, err=%v", delResp, err)
	}
}

func TestSellerOwnershipAndRBAC(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)

	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerACtx := ctxWithRole("seller-a", "seller", "shop-a")
	sellerBCtx := ctxWithRole("seller-b", "seller", "shop-b")
	buyerCtx := ctxWithRole("buyer-1", "buyer", "")

	catResp, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name:              "Books",
		CommissionRateBps: 400,
	})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}

	// Unauthenticated cannot create product
	_, err = srv.CreateProduct(context.Background(), &productv1.CreateProductRequest{
		ShopId:     "shop-a",
		CategoryId: catResp.Category.Id,
		Name:       "Unauth Book",
		Skus:       []*productv1.SKUInput{{SkuCode: "B1", PriceVnd: 50000, Stock: 5}},
	})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got %v", err)
	}

	// Buyer cannot create product
	_, err = srv.CreateProduct(buyerCtx, &productv1.CreateProductRequest{
		ShopId:     "shop-a",
		CategoryId: catResp.Category.Id,
		Name:       "Buyer Book",
		Skus:       []*productv1.SKUInput{{SkuCode: "B1", PriceVnd: 50000, Stock: 5}},
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for buyer, got %v", err)
	}

	// Seller A cannot create product specifying Seller B's shop_id
	_, err = srv.CreateProduct(sellerACtx, &productv1.CreateProductRequest{
		ShopId:     "shop-b",
		CategoryId: catResp.Category.Id,
		Name:       "Cross-shop Book",
		Skus:       []*productv1.SKUInput{{SkuCode: "B1", PriceVnd: 50000, Stock: 5}},
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for cross-shop create, got %v", err)
	}

	// Seller A creates product in own shop
	created, err := srv.CreateProduct(sellerACtx, &productv1.CreateProductRequest{
		CategoryId: catResp.Category.Id,
		Name:       "Go Systems Programming",
		Skus:       []*productv1.SKUInput{{SkuCode: "GO-1", Name: "Paperback", PriceVnd: 200000, Stock: 10}},
	})
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	prodID := created.Product.Id
	skuID := created.Product.Skus[0].Id

	// Seller A cannot self-approve own PENDING product
	_, err = srv.UpdateProduct(sellerACtx, &productv1.UpdateProductRequest{
		Id:     prodID,
		Status: productv1.ProductStatus_PRODUCT_STATUS_APPROVED,
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied on seller self-approve, got %v", err)
	}

	// Seller A cannot call AdminReviewProduct
	_, err = srv.AdminReviewProduct(sellerACtx, &productv1.AdminReviewProductRequest{
		ProductId: prodID,
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied on seller AdminReviewProduct, got %v", err)
	}

	// Seller B cannot update, delete, or update stock of Seller A's product
	_, err = srv.UpdateProduct(sellerBCtx, &productv1.UpdateProductRequest{
		Id:   prodID,
		Name: "Hijacked",
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for Seller B UpdateProduct, got %v", err)
	}

	_, err = srv.DeleteProduct(sellerBCtx, &productv1.DeleteProductRequest{Id: prodID})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for Seller B DeleteProduct, got %v", err)
	}

	_, err = srv.UpdateStock(sellerBCtx, &productv1.UpdateStockRequest{
		SkuId:           skuID,
		NewStock:        99,
		ExpectedVersion: 1,
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for Seller B UpdateStock, got %v", err)
	}
}

func TestUpdateStockOptimisticLockingAndHistory(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)

	_, prod := setupApprovedProduct(t, srv, "shop-1", []*productv1.SKUInput{
		{SkuCode: "P1-BLK", Name: "Black", PriceVnd: 1000000, Stock: 10, WeightGrams: 300},
	})
	skuID := prod.Skus[0].Id
	sellerCtx := ctxWithRole("seller-shop-1", "seller", "shop-1")

	// Reserve 4 units first so reserved_stock == 4
	if _, err := srv.ReserveStock(context.Background(), &productv1.ReserveStockRequest{
		OrderId: "order-1",
		Items:   []*productv1.StockReservationItem{{SkuId: skuID, Quantity: 4}},
	}); err != nil {
		t.Fatalf("ReserveStock: %v", err)
	}
	// Version is now 2 (initial 1 + reserve bump 1)

	// Stale version (1) must fail with Aborted (409)
	_, err := srv.UpdateStock(sellerCtx, &productv1.UpdateStockRequest{
		SkuId:           skuID,
		NewStock:        20,
		ExpectedVersion: 1,
	})
	if status.Code(err) != codes.Aborted {
		t.Fatalf("expected Aborted (409) on stale version, got %v", err)
	}

	// Setting new_stock < reserved_stock (3 < 4) must fail with FailedPrecondition
	_, err = srv.UpdateStock(sellerCtx, &productv1.UpdateStockRequest{
		SkuId:           skuID,
		NewStock:        3,
		ExpectedVersion: 2,
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition when new_stock < reserved_stock, got %v", err)
	}

	// Matching version (2) with valid new_stock (15 >= 4) succeeds and increments version -> 3
	updResp, err := srv.UpdateStock(sellerCtx, &productv1.UpdateStockRequest{
		SkuId:           skuID,
		NewStock:        15,
		ExpectedVersion: 2,
	})
	if err != nil {
		t.Fatalf("UpdateStock failed: %v", err)
	}
	if updResp.Sku.Stock != 15 || updResp.Sku.ReservedStock != 4 || updResp.Sku.AvailableStock != 11 || updResp.Sku.Version != 3 {
		t.Fatalf("unexpected updated SKU state: %+v", updResp.Sku)
	}

	// Verify stock_history captured both RESERVE and MANUAL_UPDATE
	hist := st.StockHistory()
	if len(hist) < 2 {
		t.Fatalf("expected at least 2 stock_history entries, got %d", len(hist))
	}
	last := hist[len(hist)-1]
	if last.ChangeType != "MANUAL_UPDATE" || last.StockBefore != 10 || last.StockAfter != 15 || last.Delta != 5 {
		t.Fatalf("unexpected stock_history entry: %+v", last)
	}
}

func TestReserveReleaseConfirmIdempotencyAndAtomicRollback(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)

	_, prod := setupApprovedProduct(t, srv, "shop-1", []*productv1.SKUInput{
		{SkuCode: "SKU-A", Name: "A", PriceVnd: 100000, Stock: 10, WeightGrams: 100},
		{SkuCode: "SKU-B", Name: "B", PriceVnd: 200000, Stock: 2, WeightGrams: 100},
	})
	skuA := prod.Skus[0].Id
	skuB := prod.Skus[1].Id

	// 1. Atomic rollback: SKU-A has enough (5 <= 10) but SKU-B lacks stock (5 > 2) -> ENTIRE TX must roll back!
	_, err := srv.ReserveStock(context.Background(), &productv1.ReserveStockRequest{
		OrderId: "order-fail",
		Items: []*productv1.StockReservationItem{
			{SkuId: skuA, Quantity: 5},
			{SkuId: skuB, Quantity: 5},
		},
	})
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "INSUFFICIENT_STOCK") {
		t.Fatalf("expected FailedPrecondition INSUFFICIENT_STOCK, got %v", err)
	}
	batch, err := srv.GetSKUsBatch(context.Background(), &productv1.GetSKUsBatchRequest{SkuIds: []string{skuA, skuB}})
	if err != nil {
		t.Fatalf("GetSKUsBatch: %v", err)
	}
	for _, sk := range batch.Skus {
		if sk.ReservedStock != 0 {
			t.Fatalf("expected atomic rollback leaving reserved_stock=0 for %s, got %d", sk.Id, sk.ReservedStock)
		}
	}

	// 2. Idempotent ReserveStock: calling twice with same order_id reserves ONCE
	res1, err := srv.ReserveStock(context.Background(), &productv1.ReserveStockRequest{
		OrderId: "order-idem-1",
		Items: []*productv1.StockReservationItem{
			{SkuId: skuB, Quantity: 1},
			{SkuId: skuA, Quantity: 3},
		},
	})
	if err != nil || !res1.Reserved || res1.AlreadyReserved {
		t.Fatalf("first ReserveStock failed: resp=%+v err=%v", res1, err)
	}

	res2, err := srv.ReserveStock(context.Background(), &productv1.ReserveStockRequest{
		OrderId: "order-idem-1",
		Items: []*productv1.StockReservationItem{
			{SkuId: skuB, Quantity: 1},
			{SkuId: skuA, Quantity: 3},
		},
	})
	if err != nil || !res2.Reserved || !res2.AlreadyReserved || res2.ReservationId != res1.ReservationId {
		t.Fatalf("second ReserveStock should be idempotent replay: resp=%+v err=%v", res2, err)
	}

	skAState, _ := st.GetSKUWithContext(context.Background(), skuA)
	skBState, _ := st.GetSKUWithContext(context.Background(), skuB)
	if skAState.ReservedStock != 3 || skBState.ReservedStock != 1 {
		t.Fatalf("expected reserved A=3 B=1 after idempotent replay, got A=%d B=%d", skAState.ReservedStock, skBState.ReservedStock)
	}

	// 3. Idempotent ReleaseStock: calling twice only decrements reserved_stock ONCE
	rel1, err := srv.ReleaseStock(context.Background(), &productv1.ReleaseStockRequest{OrderId: "order-idem-1"})
	if err != nil || !rel1.Released || rel1.AlreadyReleased {
		t.Fatalf("first ReleaseStock: resp=%+v err=%v", rel1, err)
	}
	rel2, err := srv.ReleaseStock(context.Background(), &productv1.ReleaseStockRequest{OrderId: "order-idem-1"})
	if err != nil || !rel2.Released || !rel2.AlreadyReleased {
		t.Fatalf("second ReleaseStock should be idempotent no-op: resp=%+v err=%v", rel2, err)
	}
	skAState, _ = st.GetSKUWithContext(context.Background(), skuA)
	skBState, _ = st.GetSKUWithContext(context.Background(), skuB)
	if skAState.ReservedStock != 0 || skBState.ReservedStock != 0 {
		t.Fatalf("expected reserved A=0 B=0 after release, got A=%d B=%d", skAState.ReservedStock, skBState.ReservedStock)
	}

	// Unknown order_id release is graceful no-op
	relUnknown, err := srv.ReleaseStock(context.Background(), &productv1.ReleaseStockRequest{OrderId: "does-not-exist"})
	if err != nil || relUnknown.Released {
		t.Fatalf("expected graceful no-op for unknown order_id release, got %+v err=%v", relUnknown, err)
	}

	// 4. ConfirmStock idempotency & out-of-stock transition
	if _, err := srv.ReserveStock(context.Background(), &productv1.ReserveStockRequest{
		OrderId: "order-confirm-1",
		Items:   []*productv1.StockReservationItem{{SkuId: skuB, Quantity: 2}},
	}); err != nil {
		t.Fatalf("ReserveStock for confirm: %v", err)
	}

	conf1, err := srv.ConfirmStock(context.Background(), &productv1.ConfirmStockRequest{OrderId: "order-confirm-1"})
	if err != nil || !conf1.Confirmed || conf1.AlreadyConfirmed {
		t.Fatalf("first ConfirmStock: resp=%+v err=%v", conf1, err)
	}
	conf2, err := srv.ConfirmStock(context.Background(), &productv1.ConfirmStockRequest{OrderId: "order-confirm-1"})
	if err != nil || !conf2.Confirmed || !conf2.AlreadyConfirmed {
		t.Fatalf("second ConfirmStock should be idempotent: resp=%+v err=%v", conf2, err)
	}

	skBState, _ = st.GetSKUWithContext(context.Background(), skuB)
	if skBState.Stock != 0 || skBState.ReservedStock != 0 || skBState.Status != store.SKUStatusOutOfStock {
		t.Fatalf("expected SKU-B stock=0 reserved=0 status=OUT_OF_STOCK, got %+v", skBState)
	}

	// Confirming an already-released order must fail with FailedPrecondition
	_, err = srv.ConfirmStock(context.Background(), &productv1.ConfirmStockRequest{OrderId: "order-idem-1"})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition confirming released order, got %v", err)
	}
}

func TestReserveStockConcurrentNoOversellAndRetry(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)

	_, prod := setupApprovedProduct(t, srv, "shop-concurrent", []*productv1.SKUInput{
		{SkuCode: "HOT-1", Name: "Limited Edition", PriceVnd: 500000, Stock: 10, WeightGrams: 500},
	})
	skuID := prod.Skus[0].Id

	// Test 1: Deterministic optimistic-lock conflict retry using BeforeReserveWrite hook
	var hookTriggered atomic.Int32
	st.BeforeReserveWrite = func(targetSKU string) {
		if targetSKU == skuID && hookTriggered.CompareAndSwap(0, 1) {
			// Simulate another transaction bumping version on this SKU right before our optimistic write
			_, _ = st.UpdateSKUStockOptimistic(context.Background(), skuID, 10, 1)
		}
	}
	res, err := srv.ReserveStock(context.Background(), &productv1.ReserveStockRequest{
		OrderId: "order-retry-check",
		Items:   []*productv1.StockReservationItem{{SkuId: skuID, Quantity: 2}},
	})
	st.BeforeReserveWrite = nil
	if err != nil || !res.Reserved {
		t.Fatalf("expected ReserveStock to retry and succeed after single version conflict, got resp=%+v err=%v", res, err)
	}
	if hookTriggered.Load() != 1 {
		t.Fatalf("expected conflict hook to trigger once")
	}

	// Release that test reservation so stock available returns to 10
	if _, err := srv.ReleaseStock(context.Background(), &productv1.ReleaseStockRequest{OrderId: "order-retry-check"}); err != nil {
		t.Fatalf("ReleaseStock: %v", err)
	}

	// Test 2: 50 concurrent orders competing for 10 units -> EXACTLY 10 succeed, zero oversell!
	const (
		totalWorkers = 50
		stockLimit   = 10
	)
	var (
		wg        sync.WaitGroup
		succeeded atomic.Int32
		failed    atomic.Int32
	)
	startCh := make(chan struct{})
	for i := 0; i < totalWorkers; i++ {
		wg.Add(1)
		orderID := fmt.Sprintf("concurrent-order-%d", i)
		go func(oid string) {
			defer wg.Done()
			<-startCh
			_, callErr := srv.ReserveStock(context.Background(), &productv1.ReserveStockRequest{
				OrderId: oid,
				Items:   []*productv1.StockReservationItem{{SkuId: skuID, Quantity: 1}},
			})
			if callErr == nil {
				succeeded.Add(1)
			} else {
				failed.Add(1)
			}
		}(orderID)
	}
	close(startCh)
	wg.Wait()

	if succeeded.Load() != stockLimit {
		t.Fatalf("expected exactly %d reservations to succeed (no oversell), got %d (failed=%d)", stockLimit, succeeded.Load(), failed.Load())
	}
	skuState, err := st.GetSKUWithContext(context.Background(), skuID)
	if err != nil {
		t.Fatalf("GetSKUWithContext: %v", err)
	}
	if skuState.ReservedStock != stockLimit || skuState.AvailableStock() != 0 {
		t.Fatalf("expected reserved=%d available=0, got reserved=%d available=%d", stockLimit, skuState.ReservedStock, skuState.AvailableStock())
	}

	// Test 3: 10 concurrent ReserveStock calls with the SAME order_id reserve only once
	_, prod2 := setupApprovedProduct(t, srv, "shop-idem-conc", []*productv1.SKUInput{
		{SkuCode: "SAME-ORD", Name: "Item", PriceVnd: 100000, Stock: 20, WeightGrams: 100},
	})
	sku2 := prod2.Skus[0].Id
	var wg2 sync.WaitGroup
	start2 := make(chan struct{})
	for i := 0; i < 10; i++ {
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			<-start2
			_, callErr := srv.ReserveStock(context.Background(), &productv1.ReserveStockRequest{
				OrderId: "same-order-race",
				Items:   []*productv1.StockReservationItem{{SkuId: sku2, Quantity: 3}},
			})
			if callErr != nil {
				t.Errorf("concurrent same-order ReserveStock unexpected error: %v", callErr)
			}
		}()
	}
	close(start2)
	wg2.Wait()

	sku2State, _ := st.GetSKUWithContext(context.Background(), sku2)
	if sku2State.ReservedStock != 3 {
		t.Fatalf("expected concurrent same-order reserve to reserve exactly 3, got %d", sku2State.ReservedStock)
	}
}

func TestListProductsFilteringSortingAndPriceRange(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)

	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")

	catA, _ := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{Name: "Audio", Slug: "audio", CommissionRateBps: 500})
	catB, _ := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{Name: "Laptops", Slug: "laptops", CommissionRateBps: 300})

	p1, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId:  catA.Category.Id,
		Name:        "Wireless Earbuds",
		Description: "Noise cancelling Bluetooth earbuds",
		Skus: []*productv1.SKUInput{
			{SkuCode: "EB-1", Name: "White", PriceVnd: 500000, Stock: 10},
			{SkuCode: "EB-2", Name: "Black", PriceVnd: 600000, Stock: 5},
		},
	})
	if err != nil {
		t.Fatalf("create p1: %v", err)
	}
	if p1.Product.MinPriceVnd != 500000 || p1.Product.MaxPriceVnd != 600000 {
		t.Fatalf("expected price range 500000..600000, got %d..%d", p1.Product.MinPriceVnd, p1.Product.MaxPriceVnd)
	}

	p2, _ := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId:  catB.Category.Id,
		Name:        "Developer Laptop",
		Description: "High performance coding machine",
		Skus:        []*productv1.SKUInput{{SkuCode: "LP-1", Name: "16GB", PriceVnd: 25000000, Stock: 3}},
	})
	// p3 stays PENDING (must not show in public listing!)
	_, _ = srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: catA.Category.Id,
		Name:       "Pending Speaker",
		Skus:       []*productv1.SKUInput{{SkuCode: "SP-1", Name: "Std", PriceVnd: 300000, Stock: 2}},
	})

	_, _ = srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{ProductId: p1.Product.Id, Decision: productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE})
	_, _ = srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{ProductId: p2.Product.Id, Decision: productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE})

	// Public list only returns 2 approved products, sorted by price_desc
	listResp, err := srv.ListProducts(context.Background(), &productv1.ListProductsRequest{
		SortBy: "price_desc",
	})
	if err != nil || listResp.Total != 2 || len(listResp.Products) != 2 {
		t.Fatalf("expected 2 public products, got total=%d len=%d err=%v", listResp.Total, len(listResp.Products), err)
	}
	if listResp.Products[0].Id != p2.Product.Id {
		t.Fatalf("expected Laptop first under price_desc, got %s", listResp.Products[0].Name)
	}

	// Case-insensitive keyword search on description
	kwResp, err := srv.ListProducts(context.Background(), &productv1.ListProductsRequest{
		Keyword: "BLUETOOTH",
	})
	if err != nil || kwResp.Total != 1 || kwResp.Products[0].Id != p1.Product.Id {
		t.Fatalf("expected keyword match for Earbuds, got %+v err=%v", kwResp, err)
	}

	// Price range filter
	priceResp, err := srv.ListProducts(context.Background(), &productv1.ListProductsRequest{
		MinPriceVnd: 1000000,
		MaxPriceVnd: 30000000,
	})
	if err != nil || priceResp.Total != 1 || priceResp.Products[0].Id != p2.Product.Id {
		t.Fatalf("expected Laptop in price range, got %+v err=%v", priceResp, err)
	}

	// Admin can see all 3 products including PENDING
	adminList, err := srv.ListProducts(adminCtx, &productv1.ListProductsRequest{})
	if err != nil || adminList.Total != 3 {
		t.Fatalf("expected admin to see all 3 products, got %d err=%v", adminList.Total, err)
	}
}

func TestPublicVisibilityRBACForListAndGetProduct(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)

	adminCtx := ctxWithRole("admin-1", "admin", "")
	seller1Ctx := ctxWithRole("seller-1", "seller", "shop-1")
	seller2Ctx := ctxWithRole("seller-2", "seller", "shop-2")
	buyerCtx := ctxWithRole("buyer-1", "buyer", "")

	cat, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name:              "Gadgets",
		Slug:              "gadgets",
		CommissionRateBps: 500,
	})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}

	pendingProd, err := srv.CreateProduct(seller1Ctx, &productv1.CreateProductRequest{
		CategoryId: cat.Category.Id,
		Name:       "Secret Unapproved Gadget",
		Skus:       []*productv1.SKUInput{{SkuCode: "SEC-1", PriceVnd: 100000, Stock: 5}},
	})
	if err != nil {
		t.Fatalf("CreateProduct pending: %v", err)
	}

	hiddenProd, err := srv.CreateProduct(seller1Ctx, &productv1.CreateProductRequest{
		CategoryId: cat.Category.Id,
		Name:       "Hidden Gadget",
		Skus:       []*productv1.SKUInput{{SkuCode: "HID-1", PriceVnd: 150000, Stock: 5}},
	})
	if err != nil {
		t.Fatalf("CreateProduct hidden: %v", err)
	}
	if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: hiddenProd.Product.Id,
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	}); err != nil {
		t.Fatalf("approve hiddenProd: %v", err)
	}
	if _, err := srv.UpdateProduct(seller1Ctx, &productv1.UpdateProductRequest{
		Id:     hiddenProd.Product.Id,
		Status: productv1.ProductStatus_PRODUCT_STATUS_HIDDEN,
	}); err != nil {
		t.Fatalf("hide product: %v", err)
	}

	// Anonymous, buyer, and competing seller MUST NOT bypass public_only by passing status=PENDING or status=HIDDEN
	for _, tc := range []struct {
		name   string
		ctx    context.Context
		status productv1.ProductStatus
	}{
		{"anon_pending", context.Background(), productv1.ProductStatus_PRODUCT_STATUS_PENDING},
		{"anon_hidden", context.Background(), productv1.ProductStatus_PRODUCT_STATUS_HIDDEN},
		{"buyer_pending", buyerCtx, productv1.ProductStatus_PRODUCT_STATUS_PENDING},
		{"other_seller_pending", seller2Ctx, productv1.ProductStatus_PRODUCT_STATUS_PENDING},
	} {
		resp, err := srv.ListProducts(tc.ctx, &productv1.ListProductsRequest{
			ShopId: "shop-1",
			Status: tc.status,
		})
		if err != nil {
			t.Fatalf("%s: unexpected err: %v", tc.name, err)
		}
		if resp.Total != 0 || len(resp.Products) != 0 {
			t.Fatalf("%s: expected 0 leaked products, got total=%d len=%d", tc.name, resp.Total, len(resp.Products))
		}
	}

	// Owning seller and admin CAN list own PENDING products
	ownPending, err := srv.ListProducts(seller1Ctx, &productv1.ListProductsRequest{
		ShopId: "shop-1",
		Status: productv1.ProductStatus_PRODUCT_STATUS_PENDING,
	})
	if err != nil || ownPending.Total != 1 || ownPending.Products[0].Id != pendingProd.Product.Id {
		t.Fatalf("expected owning seller to see own pending product, got %+v err=%v", ownPending, err)
	}
	adminPending, err := srv.ListProducts(adminCtx, &productv1.ListProductsRequest{
		Status: productv1.ProductStatus_PRODUCT_STATUS_PENDING,
	})
	if err != nil || adminPending.Total != 1 {
		t.Fatalf("expected admin to see pending product, got %+v err=%v", adminPending, err)
	}

	// GetProduct denies anonymous, buyer, and other seller on PENDING & HIDDEN products
	for _, prodID := range []string{pendingProd.Product.Id, hiddenProd.Product.Id} {
		for _, ctx := range []context.Context{context.Background(), buyerCtx, seller2Ctx} {
			if _, err := srv.GetProduct(ctx, &productv1.GetProductRequest{Id: prodID}); status.Code(err) != codes.NotFound {
				t.Fatalf("expected NotFound for non-public product %s, got %v", prodID, err)
			}
		}
		// Owning seller and admin CAN view them
		if got, err := srv.GetProduct(seller1Ctx, &productv1.GetProductRequest{Id: prodID}); err != nil || got.Product.Id != prodID {
			t.Fatalf("expected owning seller to view %s, got %+v err=%v", prodID, got, err)
		}
		if got, err := srv.GetProduct(adminCtx, &productv1.GetProductRequest{Id: prodID}); err != nil || got.Product.Id != prodID {
			t.Fatalf("expected admin to view %s, got %+v err=%v", prodID, got, err)
		}
	}
}

func TestProductUpdateRejectAndOutOfStockTransitions(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)

	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")

	cat, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{Name: "Home", Slug: "home", CommissionRateBps: 500})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}

	created, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: cat.Category.Id,
		Name:       "Desk Lamp",
		Skus: []*productv1.SKUInput{
			{SkuCode: "LAMP-1", Name: "Warm", PriceVnd: 200000, Stock: 3, WeightGrams: 400},
		},
	})
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	prodID := created.Product.Id
	skuID := created.Product.Skus[0].Id

	// Admin rejects product with reason
	rej, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: prodID,
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_REJECT,
		Reason:    "Missing dimensions",
	})
	if err != nil || rej.Product.Status != productv1.ProductStatus_PRODUCT_STATUS_REJECTED || rej.Product.RejectionReason != "Missing dimensions" {
		t.Fatalf("expected REJECTED with reason, got %+v err=%v", rej, err)
	}

	// Seller updates product + adds second variant and resubmits to PENDING
	upd, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id:          prodID,
		Name:        "Desk Lamp LED",
		Description: "30cm height",
		Status:      productv1.ProductStatus_PRODUCT_STATUS_PENDING,
		Skus: []*productv1.SKUInput{
			{Id: skuID, SkuCode: "LAMP-1", Name: "Warm", PriceVnd: 220000, Stock: 2, WeightGrams: 400},
			{SkuCode: "LAMP-2", Name: "Cool", PriceVnd: 250000, Stock: 1, WeightGrams: 400},
		},
	})
	if err != nil {
		t.Fatalf("UpdateProduct: %v", err)
	}
	if upd.Product.Status != productv1.ProductStatus_PRODUCT_STATUS_PENDING || len(upd.Product.Skus) != 2 {
		t.Fatalf("unexpected updated product: %+v", upd.Product)
	}
	if upd.Product.MinPriceVnd != 220000 || upd.Product.MaxPriceVnd != 250000 {
		t.Fatalf("unexpected updated price range: %d..%d", upd.Product.MinPriceVnd, upd.Product.MaxPriceVnd)
	}

	// Admin approves
	approved, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: prodID,
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	})
	if err != nil || approved.Product.Status != productv1.ProductStatus_PRODUCT_STATUS_APPROVED {
		t.Fatalf("approve failed: %+v err=%v", approved, err)
	}

	// Reserve & Confirm all stock of BOTH SKUs -> product transitions to OUT_OF_STOCK
	sku2ID := approved.Product.Skus[1].Id
	if _, err := srv.ReserveStock(context.Background(), &productv1.ReserveStockRequest{
		OrderId: "ord-drain-all",
		Items: []*productv1.StockReservationItem{
			{SkuId: skuID, Quantity: 2},
			{SkuId: sku2ID, Quantity: 1},
		},
	}); err != nil {
		t.Fatalf("ReserveStock drain: %v", err)
	}
	if _, err := srv.ConfirmStock(context.Background(), &productv1.ConfirmStockRequest{OrderId: "ord-drain-all"}); err != nil {
		t.Fatalf("ConfirmStock drain: %v", err)
	}

	got, err := srv.GetProduct(context.Background(), &productv1.GetProductRequest{Id: prodID})
	if err != nil || got.Product.Status != productv1.ProductStatus_PRODUCT_STATUS_OUT_OF_STOCK {
		t.Fatalf("expected product OUT_OF_STOCK when all SKUs drained, got %+v err=%v", got, err)
	}

	// Restock one SKU via UpdateStock -> product automatically transitions back to APPROVED
	sku1Ver := got.Product.Skus[0].Version
	if _, err := srv.UpdateStock(sellerCtx, &productv1.UpdateStockRequest{
		SkuId:           got.Product.Skus[0].Id,
		NewStock:        5,
		ExpectedVersion: sku1Ver,
	}); err != nil {
		t.Fatalf("UpdateStock restock: %v", err)
	}
	got2, err := srv.GetProduct(context.Background(), &productv1.GetProductRequest{Id: prodID})
	if err != nil || got2.Product.Status != productv1.ProductStatus_PRODUCT_STATUS_APPROVED {
		t.Fatalf("expected product restored to APPROVED after restock, got %+v err=%v", got2, err)
	}
}
