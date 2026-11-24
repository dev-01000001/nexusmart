package server

import (
	"reflect"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	"github.com/nexusmart/core/services/product/internal/events"
	"github.com/nexusmart/core/services/product/internal/store"
)

// TestW3B_RevB_1_AutoSlugNotFalseBlocked_And_RescanSlugOnlyDemotesToPending verifies P1-1:
//   - Legitimate Vietnamese names whose accent-stripped auto-slug resembles multi-token BLOCK keywords
//     pass Create -> AdminReview(APPROVE) -> Update(price/stock with echoed slug) -> Rescan unchanged.
//   - Custom slugs containing banned keywords are still blocked on Create and Update.
//   - Renaming a product while keeping its old auto-slug passes Update; Rescan sees ONLY slug matches
//     and demotes to PENDING (never auto-REJECT).
func TestW3B_RevB_1_AutoSlugNotFalseBlocked_And_RescanSlugOnlyDemotesToPending(t *testing.T) {
	mem := store.NewMemoryStore()
	srv := New(mem, nil)
	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")

	cat, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name: "Tổng hợp", Slug: "tong-hop-revb", CommissionRateBps: 500,
	})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	catID := cat.GetCategory().GetId()

	for _, term := range []string{"hàng giả", "ma túy", "cần sa"} {
		if _, err := srv.CreateBannedKeyword(adminCtx, &productv1.CreateBannedKeywordRequest{
			Term: term, Action: productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_BLOCK,
		}); err != nil {
			t.Fatalf("CreateBannedKeyword %q: %v", term, err)
		}
	}

	legitNames := []string{
		"Sản phẩm hãng giá rẻ bất ngờ",
		"Sản phẩm hãng giá rẻ bất ngờ", // duplicate name -> exercises slugify(name)+"-"+id[:8] auto-slug
		"Giao diện ma tùy biến linh hoạt",
		"Căn sa bàn kiến trúc",
	}
	created := make([]*productv1.Product, 0, len(legitNames))
	for i, name := range legitNames {
		pResp, cErr := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
			CategoryId: catID,
			Name:       name,
			Skus:       []*productv1.SKUInput{{SkuCode: "REVB-LEGIT-" + string(rune('A'+i)), Name: "M", PriceVnd: 100000, Stock: 10}},
		})
		if cErr != nil {
			t.Fatalf("CreateProduct(%q) failed: %v", name, cErr)
		}
		p := pResp.GetProduct()
		if i == 1 && !strings.HasSuffix(p.GetSlug(), "-"+p.GetId()[:8]) {
			t.Fatalf("expected collision auto-slug with -%s suffix, got %q", p.GetId()[:8], p.GetSlug())
		}
		// Admin APPROVE must succeed (must NOT fail on auto-generated slug).
		appResp, aErr := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
			ProductId: p.GetId(),
			Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
		})
		if aErr != nil || appResp.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_APPROVED {
			t.Fatalf("AdminReviewProduct(APPROVE) on %q (slug=%q) failed: resp=%+v err=%v", name, p.GetSlug(), appResp, aErr)
		}
		// Seller edits price while web form echoes back the stored slug -> must succeed and stay APPROVED.
		sku := appResp.GetProduct().GetSkus()[0]
		updResp, uErr := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
			Id:         p.GetId(),
			CategoryId: catID,
			Name:       name,
			Slug:       p.GetSlug(),
			Skus: []*productv1.SKUInput{{
				Id: sku.GetId(), SkuCode: sku.GetSkuCode(), Name: sku.GetName(), PriceVnd: 120000, Stock: 15,
			}},
		})
		if uErr != nil || updResp.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_APPROVED {
			t.Fatalf("UpdateProduct(price) on %q (slug=%q) failed: resp=%+v err=%v", name, p.GetSlug(), updResp, uErr)
		}
		created = append(created, updResp.GetProduct())
	}

	// Rescan must leave all 3 legitimate products APPROVED (blocked=0, flagged=0).
	rescan1, err := srv.RescanProductsForBannedContent(adminCtx, &productv1.RescanProductsForBannedContentRequest{})
	if err != nil {
		t.Fatalf("Rescan on legitimate auto-slug products: %v", err)
	}
	if rescan1.GetBlockedCount() != 0 || rescan1.GetFlaggedCount() != 0 {
		t.Fatalf("Rescan wrongly touched auto-slug products: blocked=%d flagged=%d samples=%+v",
			rescan1.GetBlockedCount(), rescan1.GetFlaggedCount(), rescan1.GetSamples())
	}
	for _, p := range created {
		cur, _ := srv.GetProduct(adminCtx, &productv1.GetProductRequest{Id: p.GetId()})
		if cur.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_APPROVED {
			t.Fatalf("product %q status changed to %v after rescan", p.GetName(), cur.GetProduct().GetStatus())
		}
	}

	// Custom slug containing banned keyword is STILL blocked on CreateProduct & UpdateProduct.
	if _, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: catID,
		Name:       "Áo thun trơn",
		Slug:       "ao-ma-tuy-custom",
		Skus:       []*productv1.SKUInput{{SkuCode: "REVB-BAD-1", Name: "M", PriceVnd: 100000, Stock: 5}},
	}); status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "BANNED_CONTENT") {
		t.Fatalf("CreateProduct custom banned slug: want BANNED_CONTENT, got %v", err)
	}
	if _, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id:   created[0].GetId(),
		Slug: "custom-hang-gia-slug",
	}); status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "BANNED_CONTENT") {
		t.Fatalf("UpdateProduct custom banned slug: want BANNED_CONTENT, got %v", err)
	}

	// Rename product while keeping old slug -> UpdateProduct passes (does NOT scan unchanged old slug).
	// On subsequent Rescan, ONLY slug matches BLOCK keyword (name/desc/SKU clean) -> demotes to PENDING (NOT REJECTED, 0 REJECT outbox).
	target := created[0]
	renamed, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id:   target.GetId(),
		Name: "Sản phẩm mới siêu ưu đãi",
		Slug: target.GetSlug(),
	})
	if err != nil || renamed.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_APPROVED {
		t.Fatalf("UpdateProduct rename keeping old slug failed: resp=%+v err=%v", renamed, err)
	}

	outboxBefore := countRejectOutbox(mem.OutboxEvents(), target.GetId())
	rescan2, err := srv.RescanProductsForBannedContent(adminCtx, &productv1.RescanProductsForBannedContentRequest{})
	if err != nil {
		t.Fatalf("Rescan after rename: %v", err)
	}
	if rescan2.GetBlockedCount() != 0 || rescan2.GetFlaggedCount() != 1 {
		t.Fatalf("Rescan slug-only match: blocked=%d flagged=%d, want 0 and 1 (demote to PENDING, never auto-REJECT)",
			rescan2.GetBlockedCount(), rescan2.GetFlaggedCount())
	}
	afterRename, _ := srv.GetProduct(adminCtx, &productv1.GetProductRequest{Id: target.GetId()})
	if afterRename.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_PENDING ||
		!reflect.DeepEqual(afterRename.GetProduct().GetFlaggedKeywords(), []string{"hàng giả"}) {
		t.Fatalf("after slug-only rescan: status=%v flagged=%v, want PENDING + [hàng giả]",
			afterRename.GetProduct().GetStatus(), afterRename.GetProduct().GetFlaggedKeywords())
	}
	if got := countRejectOutbox(mem.OutboxEvents(), target.GetId()); got != outboxBefore {
		t.Fatalf("slug-only rescan emitted REJECT product.reviewed outbox event (%d vs %d)", got, outboxBefore)
	}

	// Admin MUST be able to APPROVE the product demoted by slug-only BLOCK (treated as REVIEW);
	// flagged_keywords retains ["hàng giả"] so a subsequent Rescan is a no-op with 0 new outbox events.
	reApproved, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: target.GetId(),
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	})
	if err != nil {
		t.Fatalf("AdminReviewProduct(APPROVE) on slug-only PENDING product failed: %v", err)
	}
	if reApproved.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_APPROVED ||
		!reflect.DeepEqual(reApproved.GetProduct().GetFlaggedKeywords(), []string{"hàng giả"}) {
		t.Fatalf("after slug-only APPROVE: status=%v flagged=%v, want APPROVED + [hàng giả]",
			reApproved.GetProduct().GetStatus(), reApproved.GetProduct().GetFlaggedKeywords())
	}

	totalOutboxBeforeRescan3 := len(mem.OutboxEvents())
	rescan3, err := srv.RescanProductsForBannedContent(adminCtx, &productv1.RescanProductsForBannedContentRequest{})
	if err != nil {
		t.Fatalf("Rescan 2 after slug-only APPROVE: %v", err)
	}
	if rescan3.GetBlockedCount() != 0 || rescan3.GetFlaggedCount() != 0 {
		t.Fatalf("Rescan 2 after slug-only APPROVE wrongly acted: blocked=%d flagged=%d, want 0/0",
			rescan3.GetBlockedCount(), rescan3.GetFlaggedCount())
	}
	afterRescan3, _ := srv.GetProduct(adminCtx, &productv1.GetProductRequest{Id: target.GetId()})
	if afterRescan3.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_APPROVED ||
		!reflect.DeepEqual(afterRescan3.GetProduct().GetFlaggedKeywords(), []string{"hàng giả"}) {
		t.Fatalf("after Rescan 2: status=%v flagged=%v, want APPROVED + [hàng giả]",
			afterRescan3.GetProduct().GetStatus(), afterRescan3.GetProduct().GetFlaggedKeywords())
	}
	if len(mem.OutboxEvents()) != totalOutboxBeforeRescan3 {
		t.Fatalf("Rescan 2 emitted %d unexpected outbox events", len(mem.OutboxEvents())-totalOutboxBeforeRescan3)
	}

	// Product whose DESCRIPTION matches a BLOCK keyword MUST still be blocked on AdminReviewProduct(APPROVE).
	descProd, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId:  catID,
		Name:        "Áo khoác gió mùa đông",
		Description: "Sản phẩm chứa cụm nội dung cấm tuyệt đối",
		Skus:        []*productv1.SKUInput{{SkuCode: "REVB-DESC-BLK", Name: "L", PriceVnd: 200000, Stock: 5}},
	})
	if err != nil {
		t.Fatalf("CreateProduct descProd: %v", err)
	}
	if _, err := srv.CreateBannedKeyword(adminCtx, &productv1.CreateBannedKeywordRequest{
		Term:   "nội dung cấm tuyệt đối",
		Action: productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_BLOCK,
	}); err != nil {
		t.Fatalf("CreateBannedKeyword desc block: %v", err)
	}
	if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: descProd.GetProduct().GetId(),
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	}); status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "BANNED_CONTENT") {
		t.Fatalf("AdminReviewProduct(APPROVE) with BLOCK in description: want 400 BANNED_CONTENT, got %v", err)
	}
}

func countRejectOutbox(evs []store.OutboxEvent, productID string) int {
	n := 0
	for _, ev := range evs {
		if ev.EventType == events.SubjectProductReviewed && ev.AggregateID == productID && strings.Contains(string(ev.PayloadJSON), `"REJECT"`) {
			n++
		}
	}
	return n
}

// TestW3B_R11_AdminReviewResponseSKUIsActive_MemoryStore verifies that AdminReviewProduct(APPROVE)
// reloads SKUs after updating product status so active SKUs report is_active=true with accurate
// available_stock on both the initial APPROVE and idempotent re-APPROVE, and is_active=false on REJECT.
func TestW3B_R11_AdminReviewResponseSKUIsActive_MemoryStore(t *testing.T) {
	mem := store.NewMemoryStore()
	srv := New(mem, nil)
	adminCtx := ctxWithRole("admin-r11", "admin", "")

	cat, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name: "R11 Cat", Slug: "r11-cat-mem", CommissionRateBps: 500,
	})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	assertAdminReviewSKUActiveLifecycle(t, srv, "shop-r11", cat.GetCategory().GetId(), "R11-MEM")
}

func assertAdminReviewSKUActiveLifecycle(
	t *testing.T,
	srv *Server,
	shopID string,
	catID string,
	prefix string,
) {
	t.Helper()
	adminCtx := ctxWithRole("admin-r11", "admin", "")
	sellerCtx := ctxWithRole("seller-r11", "seller", shopID)

	created, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: catID,
		Name:       "Sản phẩm kiểm duyệt r11 " + prefix,
		Slug:       strings.ToLower(prefix) + "-r11-active",
		Skus: []*productv1.SKUInput{
			{SkuCode: prefix + "-A1", Name: "Size M", PriceVnd: 150000, Stock: 15},
			{SkuCode: prefix + "-A2", Name: "Size L", PriceVnd: 180000, Stock: 7},
			{SkuCode: prefix + "-ZERO", Name: "Size S (hết)", PriceVnd: 150000, Stock: 0},
		},
	})
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	prodID := created.GetProduct().GetId()
	for _, sk := range created.GetProduct().GetSkus() {
		if sk.GetIsActive() {
			t.Fatalf("CreateProduct (PENDING): sku %s unexpectedly is_active=true", sk.GetSkuCode())
		}
	}

	checkSKUStates := func(stage string, prod *productv1.Product, wantApproved bool) {
		t.Helper()
		byCode := make(map[string]*productv1.SKU, len(prod.GetSkus()))
		for _, sk := range prod.GetSkus() {
			byCode[sk.GetSkuCode()] = sk
		}
		if len(byCode) != 3 {
			t.Fatalf("%s: got %d skus, want 3", stage, len(byCode))
		}
		for code, wantStock := range map[string]int32{prefix + "-A1": 15, prefix + "-A2": 7, prefix + "-ZERO": 0} {
			sk := byCode[code]
			if sk == nil {
				t.Fatalf("%s: missing sku %s", stage, code)
			}
			wantActive := wantApproved
			if sk.GetIsActive() != wantActive {
				t.Fatalf("%s: sku %s is_active=%v, want %v (product status=%v)", stage, code, sk.GetIsActive(), wantActive, prod.GetStatus())
			}
			if sk.GetAvailableStock() != wantStock {
				t.Fatalf("%s: sku %s available_stock=%d, want %d", stage, code, sk.GetAvailableStock(), wantStock)
			}
		}
	}

	// 1. Initial APPROVE: active in-stock SKUs MUST be is_active=true with exact available_stock.
	app1, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: prodID,
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	})
	if err != nil || app1.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_APPROVED {
		t.Fatalf("AdminReviewProduct(APPROVE #1): resp=%+v err=%v", app1, err)
	}
	checkSKUStates("APPROVE #1", app1.GetProduct(), true)

	// 2. Idempotent second APPROVE: same active SKUs + available_stock.
	app2, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: prodID,
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	})
	if err != nil || app2.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_APPROVED {
		t.Fatalf("AdminReviewProduct(APPROVE #2): resp=%+v err=%v", app2, err)
	}
	checkSKUStates("APPROVE #2 (idempotent)", app2.GetProduct(), true)

	// 3. Hide -> is_active=false; Unhide -> is_active=true again (without resending skus).
	hid, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id:     prodID,
		Status: productv1.ProductStatus_PRODUCT_STATUS_HIDDEN,
	})
	if err != nil || hid.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_HIDDEN {
		t.Fatalf("UpdateProduct(HIDDEN): resp=%+v err=%v", hid, err)
	}
	checkSKUStates("UpdateProduct(HIDDEN)", hid.GetProduct(), false)

	unhid, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id:     prodID,
		Status: productv1.ProductStatus_PRODUCT_STATUS_APPROVED,
	})
	if err != nil || unhid.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_APPROVED {
		t.Fatalf("UpdateProduct(UNHIDE): resp=%+v err=%v", unhid, err)
	}
	checkSKUStates("UpdateProduct(UNHIDE)", unhid.GetProduct(), true)

	// 4. REJECT -> all SKUs is_active=false.
	rej, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: prodID,
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_REJECT,
		Reason:    "takedown r11",
	})
	if err != nil || rej.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_REJECTED {
		t.Fatalf("AdminReviewProduct(REJECT): resp=%+v err=%v", rej, err)
	}
	checkSKUStates("REJECT", rej.GetProduct(), false)
}
