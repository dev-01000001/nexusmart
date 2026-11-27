package server

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	"github.com/nexusmart/core/services/product/internal/store"
)

func TestSearchAccentInsensitiveAndWildcardEscape(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)

	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")

	catTops, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name:              "Áo",
		Slug:              "ao",
		CommissionRateBps: 500,
	})
	if err != nil {
		t.Fatalf("CreateCategory tops: %v", err)
	}
	catDress, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name:              "Đầm",
		Slug:              "dam",
		CommissionRateBps: 500,
	})
	if err != nil {
		t.Fatalf("CreateCategory dress: %v", err)
	}

	createAndApprove := func(catID, name, desc, code string, price int64) *productv1.Product {
		t.Helper()
		created, cErr := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
			CategoryId:  catID,
			Name:        name,
			Description: desc,
			Skus: []*productv1.SKUInput{
				{SkuCode: code, Name: "M", PriceVnd: price, Stock: 10, WeightGrams: 200},
			},
		})
		if cErr != nil {
			t.Fatalf("CreateProduct %q: %v", name, cErr)
		}
		rev, rErr := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
			ProductId: created.GetProduct().GetId(),
			Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
		})
		if rErr != nil {
			t.Fatalf("Approve %q: %v", name, rErr)
		}
		return rev.GetProduct()
	}

	tee := createAndApprove(catTops.GetCategory().GetId(), "Áo thun Cotton Compact 220gsm", "Vải cotton thoáng mát", "AT-1", 179000)
	dress := createAndApprove(catDress.GetCategory().GetId(), "Đầm Linen Dáng Suông", "Ưu đãi 50% chất liệu_linen tự nhiên", "DM-1", 450000)
	_ = createAndApprove(catTops.GetCategory().GetId(), "Quần Short Chạy bộ", "Co giãn nhẹ", "QS-1", 220000)

	cases := []struct {
		name    string
		req     *productv1.ListProductsRequest
		wantIDs []string
	}{
		{
			name:    "unaccented_ao_thun_matches_accented_name",
			req:     &productv1.ListProductsRequest{Keyword: "ao thun"},
			wantIDs: []string{tee.GetId()},
		},
		{
			name:    "accented_ao_thun_matches",
			req:     &productv1.ListProductsRequest{Keyword: "Áo thun"},
			wantIDs: []string{tee.GetId()},
		},
		{
			name:    "unaccented_dam_matches_d_stroke",
			req:     &productv1.ListProductsRequest{Keyword: "dam"},
			wantIDs: []string{dress.GetId()},
		},
		{
			name:    "uppercase_DAM_matches_d_stroke",
			req:     &productv1.ListProductsRequest{Keyword: "ĐẦM"},
			wantIDs: []string{dress.GetId()},
		},
		{
			name:    "unaccented_description_matches",
			req:     &productv1.ListProductsRequest{Keyword: "thoang mat"},
			wantIDs: []string{tee.GetId()},
		},
		{
			name:    "literal_percent_does_not_act_as_wildcard",
			req:     &productv1.ListProductsRequest{Keyword: "%"},
			wantIDs: []string{dress.GetId()},
		},
		{
			name:    "literal_underscore_does_not_act_as_wildcard",
			req:     &productv1.ListProductsRequest{Keyword: "_"},
			wantIDs: []string{dress.GetId()},
		},
		{
			name: "filters_and_pagination_preserved_with_unaccented_search",
			req: &productv1.ListProductsRequest{
				CategoryId:  catTops.GetCategory().GetId(),
				Keyword:     "ao thun",
				MinPriceVnd: 150000,
				MaxPriceVnd: 200000,
				Page:        1,
				PageSize:    10,
			},
			wantIDs: []string{tee.GetId()},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, lErr := srv.ListProducts(context.Background(), tc.req)
			if lErr != nil {
				t.Fatalf("ListProducts: %v", lErr)
			}
			if int(resp.GetTotal()) != len(tc.wantIDs) || len(resp.GetProducts()) != len(tc.wantIDs) {
				t.Fatalf("got total=%d len=%d, want %d (products=%+v)", resp.GetTotal(), len(resp.GetProducts()), len(tc.wantIDs), resp.GetProducts())
			}
			for i, wantID := range tc.wantIDs {
				if resp.GetProducts()[i].GetId() != wantID {
					t.Fatalf("product[%d] = %s, want %s", i, resp.GetProducts()[i].GetId(), wantID)
				}
			}
		})
	}

	// Updating product name refreshes search_text so searches find the new unaccented name.
	if _, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id:   tee.GetId(),
		Name: "Áo khoác Gió Đen",
	}); err != nil {
		t.Fatalf("UpdateProduct name: %v", err)
	}
	afterOld, err := srv.ListProducts(context.Background(), &productv1.ListProductsRequest{Keyword: "ao thun"})
	if err != nil || afterOld.GetTotal() != 0 {
		t.Fatalf("expected 0 matches for old name after update, got total=%d err=%v", afterOld.GetTotal(), err)
	}
	afterNew, err := srv.ListProducts(context.Background(), &productv1.ListProductsRequest{Keyword: "ao khoac gio den"})
	if err != nil || afterNew.GetTotal() != 1 || afterNew.GetProducts()[0].GetId() != tee.GetId() {
		t.Fatalf("expected updated product to match unaccented new name, got %+v err=%v", afterNew, err)
	}
}

func findSKUByCode(t *testing.T, skus []*productv1.SKU, code string) *productv1.SKU {
	t.Helper()
	for _, s := range skus {
		if s.GetSkuCode() == code {
			return s
		}
	}
	t.Fatalf("sku with code %q not found in %+v", code, skus)
	return nil
}

func TestUpdateProductStockUsesOptimisticPathHistoryAndStatusSync(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)

	_, prod := setupApprovedProduct(t, srv, "shop-1", []*productv1.SKUInput{
		{SkuCode: "CATB4-1", Name: "Đen / M", PriceVnd: 150000, Stock: 5, WeightGrams: 220},
		{SkuCode: "CATB4-2", Name: "Trắng / L", PriceVnd: 250000, Stock: 3, WeightGrams: 240},
	})
	sellerCtx := ctxWithRole("seller-shop-1", "seller", "shop-1")
	sku1 := findSKUByCode(t, prod.GetSkus(), "CATB4-1")
	sku2 := findSKUByCode(t, prod.GetSkus(), "CATB4-2")

	// 1. PUT sets all SKUs to stock=0 (even when form still passes status=APPROVED):
	//    must write stock_history for both SKUs and sync product status to OUT_OF_STOCK.
	updZero, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id:     prod.GetId(),
		Status: productv1.ProductStatus_PRODUCT_STATUS_APPROVED,
		Skus: []*productv1.SKUInput{
			{Id: sku1.GetId(), SkuCode: "CATB4-1", Name: "Đen / M", PriceVnd: 150000, Stock: 0, WeightGrams: 220},
			{Id: sku2.GetId(), SkuCode: "CATB4-2", Name: "Trắng / L", PriceVnd: 250000, Stock: 0, WeightGrams: 240},
		},
	})
	if err != nil {
		t.Fatalf("UpdateProduct to zero stock: %v", err)
	}
	if updZero.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_OUT_OF_STOCK {
		t.Fatalf("expected product status OUT_OF_STOCK after setting all SKUs to 0, got %v", updZero.GetProduct().GetStatus())
	}
	hist := st.StockHistory()
	if len(hist) != 2 {
		t.Fatalf("expected 2 stock_history rows after PUT changing 2 SKUs, got %d (%+v)", len(hist), hist)
	}
	if hist[0].ChangeType != "MANUAL_UPDATE" || hist[0].StockBefore != 5 || hist[0].StockAfter != 0 || hist[0].Delta != -5 || hist[0].ActorID != "seller-shop-1" {
		t.Fatalf("unexpected stock_history[0]: %+v", hist[0])
	}
	if hist[1].ChangeType != "MANUAL_UPDATE" || hist[1].StockBefore != 3 || hist[1].StockAfter != 0 || hist[1].Delta != -3 || hist[1].ActorID != "seller-shop-1" {
		t.Fatalf("unexpected stock_history[1]: %+v", hist[1])
	}

	// 2. Seller cannot force OUT_OF_STOCK -> APPROVED while all SKUs still have 0 stock.
	if _, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id:     prod.GetId(),
		Status: productv1.ProductStatus_PRODUCT_STATUS_APPROVED,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition for OUT_OF_STOCK->APPROVED with 0 stock, got %v", err)
	}

	// 3. PUT restocks sku1 to 6 while sku2 stays 0 (form still passes status=OUT_OF_STOCK):
	//    must write 1 stock_history row (only sku1 changed) and sync product status back to APPROVED.
	updRestock, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id:     prod.GetId(),
		Status: productv1.ProductStatus_PRODUCT_STATUS_OUT_OF_STOCK,
		Skus: []*productv1.SKUInput{
			{Id: sku1.GetId(), SkuCode: "CATB4-1", Name: "Đen / M", PriceVnd: 155000, Stock: 6, WeightGrams: 220},
			{Id: sku2.GetId(), SkuCode: "CATB4-2", Name: "Trắng / L", PriceVnd: 250000, Stock: 0, WeightGrams: 240},
		},
	})
	if err != nil {
		t.Fatalf("UpdateProduct restock: %v", err)
	}
	if updRestock.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_APPROVED {
		t.Fatalf("expected product status APPROVED after restocking sku1, got %v", updRestock.GetProduct().GetStatus())
	}
	restockedSKU1 := findSKUByCode(t, updRestock.GetProduct().GetSkus(), "CATB4-1")
	if restockedSKU1.GetPriceVnd() != 155000 || restockedSKU1.GetStock() != 6 {
		t.Fatalf("expected sku1 price=155000 stock=6, got %+v", restockedSKU1)
	}
	histAfterRestock := st.StockHistory()
	if len(histAfterRestock) != 3 {
		t.Fatalf("expected 3 total stock_history rows (only sku1 changed), got %d", len(histAfterRestock))
	}
	last := histAfterRestock[2]
	if last.SKUID != sku1.GetId() || last.StockBefore != 0 || last.StockAfter != 6 || last.Delta != 6 {
		t.Fatalf("unexpected restock stock_history: %+v", last)
	}

	// 4. Metadata-only PUT with unchanged stock must NOT create extra stock_history entries.
	if _, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id:          prod.GetId(),
		Description: "Updated description only",
		Skus: []*productv1.SKUInput{
			{Id: sku1.GetId(), SkuCode: "CATB4-1", Name: "Đen / M (v2)", PriceVnd: 155000, Stock: 6, WeightGrams: 220},
			{Id: sku2.GetId(), SkuCode: "CATB4-2", Name: "Trắng / L", PriceVnd: 250000, Stock: 0, WeightGrams: 240},
		},
	}); err != nil {
		t.Fatalf("UpdateProduct metadata only: %v", err)
	}
	if len(st.StockHistory()) != 3 {
		t.Fatalf("expected stock_history count unchanged at 3, got %d", len(st.StockHistory()))
	}

	// 5. Stock cannot be reduced below reserved_stock via PUT.
	buyerCtx := ctxWithRole("buyer-catb4", "buyer", "")
	if _, err := srv.ReserveStock(buyerCtx, &productv1.ReserveStockRequest{
		OrderId: "ord-catb4-res",
		Items:   []*productv1.StockReservationItem{{SkuId: sku1.GetId(), Quantity: 4}},
	}); err != nil {
		t.Fatalf("ReserveStock: %v", err)
	}
	if _, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id: prod.GetId(),
		Skus: []*productv1.SKUInput{
			{Id: sku1.GetId(), SkuCode: "CATB4-1", Name: "Đen / M", PriceVnd: 155000, Stock: 2, WeightGrams: 220},
			{Id: sku2.GetId(), SkuCode: "CATB4-2", Name: "Trắng / L", PriceVnd: 250000, Stock: 0, WeightGrams: 240},
		},
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition when PUT sets stock < reserved_stock, got %v", err)
	}

	// 6. Concurrent modification during PUT stock change triggers optimistic version conflict (Aborted / 409).
	histBeforeConflict := len(st.StockHistory())
	triggered := false
	st.BeforeUpdateStockWrite = func(targetSKU string) {
		if targetSKU == sku1.GetId() && !triggered {
			triggered = true
			// Simulate concurrent order reserving 1 more unit and bumping version.
			_, _ = srv.ReserveStock(buyerCtx, &productv1.ReserveStockRequest{
				OrderId: "ord-catb4-concurrent",
				Items:   []*productv1.StockReservationItem{{SkuId: sku1.GetId(), Quantity: 1}},
			})
		}
	}
	_, err = srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id: prod.GetId(),
		Skus: []*productv1.SKUInput{
			{Id: sku1.GetId(), SkuCode: "CATB4-1", Name: "Đen / M", PriceVnd: 155000, Stock: 10, WeightGrams: 220},
			{Id: sku2.GetId(), SkuCode: "CATB4-2", Name: "Trắng / L", PriceVnd: 250000, Stock: 0, WeightGrams: 240},
		},
	})
	st.BeforeUpdateStockWrite = nil
	if !triggered {
		t.Fatalf("expected BeforeUpdateStockWrite hook to trigger during UpdateProduct stock change")
	}
	if status.Code(err) != codes.Aborted {
		t.Fatalf("expected Aborted (409) on concurrent stock modification during PUT, got %v", err)
	}
	// Only the concurrent RESERVE history entry was added; PUT's MANUAL_UPDATE rolled back.
	if len(st.StockHistory()) != histBeforeConflict+1 || st.StockHistory()[len(st.StockHistory())-1].ChangeType != "RESERVE" {
		t.Fatalf("expected PUT rollback without MANUAL_UPDATE history, got %+v", st.StockHistory()[histBeforeConflict:])
	}

	// 7. applySKUStockChange checks explicit expectedVersion when > 0 and rejects stale version.
	curSKU, err := st.GetSKUWithContext(context.Background(), sku1.GetId())
	if err != nil {
		t.Fatalf("GetSKUWithContext: %v", err)
	}
	err = st.WithTx(context.Background(), func(q store.Queries) error {
		_, applyErr := applySKUStockChange(context.Background(), q, curSKU, 12, curSKU.Version-1, "seller-shop-1", "explicit stale version")
		return applyErr
	})
	if !errors.Is(err, store.ErrVersionConflict) {
		t.Fatalf("expected ErrVersionConflict for explicit stale expectedVersion, got %v", err)
	}
}

func TestUpdateProductReactivatesInactiveSKUCode(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)

	_, prod := setupApprovedProduct(t, srv, "shop-1", []*productv1.SKUInput{
		{SkuCode: "CATB6-BLK-M", Name: "Đen / M", PriceVnd: 150000, Stock: 5, WeightGrams: 220},
		{SkuCode: "CATB6-WHT-L", Name: "Trắng / L", PriceVnd: 250000, Stock: 10, WeightGrams: 240},
	})
	sellerCtx := ctxWithRole("seller-shop-1", "seller", "shop-1")
	sku1 := findSKUByCode(t, prod.GetSkus(), "CATB6-BLK-M")
	sku2 := findSKUByCode(t, prod.GetSkus(), "CATB6-WHT-L")

	// 1. Deactivate sku2 by omitting it in PUT.
	afterDrop, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id: prod.GetId(),
		Skus: []*productv1.SKUInput{
			{Id: sku1.GetId(), SkuCode: "CATB6-BLK-M", Name: "Đen / M", PriceVnd: 150000, Stock: 5, WeightGrams: 220},
		},
	})
	if err != nil {
		t.Fatalf("UpdateProduct drop sku2: %v", err)
	}
	if len(afterDrop.GetProduct().GetSkus()) != 1 {
		t.Fatalf("expected 1 active SKU after drop, got %d", len(afterDrop.GetProduct().GetSkus()))
	}

	// 2. Re-add variant with same sku_code CATB6-WHT-L (without id): must reactivate existing row, not 409.
	readded, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id: prod.GetId(),
		Skus: []*productv1.SKUInput{
			{Id: sku1.GetId(), SkuCode: "CATB6-BLK-M", Name: "Đen / M", PriceVnd: 150000, Stock: 5, WeightGrams: 220},
			{
				SkuCode:     "CATB6-WHT-L",
				Name:        "Trắng / L mới",
				Attributes:  map[string]string{"color": "Trắng", "size": "L"},
				PriceVnd:    260000,
				Stock:       3,
				WeightGrams: 245,
				ImageUrl:    "/products/wht-l.jpg",
			},
		},
	})
	if err != nil {
		t.Fatalf("UpdateProduct re-add inactive sku_code failed: %v", err)
	}
	if len(readded.GetProduct().GetSkus()) != 2 {
		t.Fatalf("expected 2 active SKUs after re-add, got %d", len(readded.GetProduct().GetSkus()))
	}

	var reactivated *productv1.SKU
	for _, sk := range readded.GetProduct().GetSkus() {
		if sk.GetSkuCode() == "CATB6-WHT-L" {
			reactivated = sk
			break
		}
	}
	if reactivated == nil {
		t.Fatalf("reactivated SKU CATB6-WHT-L not returned: %+v", readded.GetProduct().GetSkus())
	}
	if reactivated.GetId() != sku2.GetId() {
		t.Fatalf("expected reactivated SKU to reuse original id %s, got %s", sku2.GetId(), reactivated.GetId())
	}
	if reactivated.GetName() != "Trắng / L mới" || reactivated.GetPriceVnd() != 260000 || reactivated.GetStock() != 3 || reactivated.GetWeightGrams() != 245 || !reactivated.GetIsActive() {
		t.Fatalf("unexpected reactivated SKU fields: %+v", reactivated)
	}
	if readded.GetProduct().GetMinPriceVnd() != 150000 || readded.GetProduct().GetMaxPriceVnd() != 260000 {
		t.Fatalf("unexpected price range after reactivate: %d..%d", readded.GetProduct().GetMinPriceVnd(), readded.GetProduct().GetMaxPriceVnd())
	}

	// Stock changed from 10 (inactive) -> 3 (reactivated) must be recorded in stock_history.
	hist := st.StockHistory()
	if len(hist) != 1 || hist[0].SKUID != sku2.GetId() || hist[0].StockBefore != 10 || hist[0].StockAfter != 3 || hist[0].Delta != -7 {
		t.Fatalf("expected stock_history entry for reactivated SKU (10->3), got %+v", hist)
	}
}

func TestUpdateStockRejectsInactiveSKU(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)

	_, prod := setupApprovedProduct(t, srv, "shop-1", []*productv1.SKUInput{
		{SkuCode: "CATB11-1", Name: "M", PriceVnd: 150000, Stock: 5, WeightGrams: 200},
		{SkuCode: "CATB11-2", Name: "L", PriceVnd: 160000, Stock: 4, WeightGrams: 210},
	})
	sellerCtx := ctxWithRole("seller-shop-1", "seller", "shop-1")
	sku1 := findSKUByCode(t, prod.GetSkus(), "CATB11-1")
	sku2 := findSKUByCode(t, prod.GetSkus(), "CATB11-2")

	// Deactivate sku2 via PUT.
	if _, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id: prod.GetId(),
		Skus: []*productv1.SKUInput{
			{Id: sku1.GetId(), SkuCode: "CATB11-1", Name: "M", PriceVnd: 150000, Stock: 5, WeightGrams: 200},
		},
	}); err != nil {
		t.Fatalf("UpdateProduct deactivate sku2: %v", err)
	}

	// PATCH stock on INACTIVE sku2 must fail with FailedPrecondition (HTTP 409) and write no history.
	histBefore := len(st.StockHistory())
	_, err := srv.UpdateStock(sellerCtx, &productv1.UpdateStockRequest{
		SkuId:           sku2.GetId(),
		NewStock:        9,
		ExpectedVersion: sku2.GetVersion(),
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition (409) for UpdateStock on INACTIVE SKU, got %v", err)
	}
	if len(st.StockHistory()) != histBefore {
		t.Fatalf("expected no stock_history added for rejected INACTIVE UpdateStock")
	}
}

func TestAdminReviewProductRejectRequiresReason(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)

	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")

	cat, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name:              "Moderation",
		Slug:              "moderation",
		CommissionRateBps: 500,
	})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	created, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: cat.GetCategory().GetId(),
		Name:       "Sản phẩm chờ duyệt",
		Skus:       []*productv1.SKUInput{{SkuCode: "MOD-1", Name: "Std", PriceVnd: 120000, Stock: 5}},
	})
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	prodID := created.GetProduct().GetId()

	// Empty and whitespace-only reasons must fail with InvalidArgument (HTTP 400).
	for _, reason := range []string{"", "   ", "\t\n "} {
		_, rErr := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
			ProductId: prodID,
			Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_REJECT,
			Reason:    reason,
		})
		if status.Code(rErr) != codes.InvalidArgument {
			t.Fatalf("reject with reason %q: expected InvalidArgument (400), got %v", reason, rErr)
		}
	}

	// Non-empty reason succeeds.
	okResp, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: prodID,
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_REJECT,
		Reason:    "  Thiếu mô tả chất liệu  ",
	})
	if err != nil {
		t.Fatalf("reject with valid reason failed: %v", err)
	}
	if okResp.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_REJECTED || okResp.GetProduct().GetRejectionReason() != "Thiếu mô tả chất liệu" {
		t.Fatalf("unexpected rejected product: %+v", okResp.GetProduct())
	}
}
