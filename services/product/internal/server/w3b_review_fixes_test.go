package server

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	"github.com/nexusmart/core/services/product/internal/events"
	"github.com/nexusmart/core/services/product/internal/store"
)

// TestW3B_ReviewFix_2_AttributesAndSlugModeration verifies SKU.attributes (keys & values)
// and custom slug are scanned across Create/Update/Approve/Preview/Rescan while sku_code and image_url are not.
func TestW3B_ReviewFix_2_AttributesAndSlugModeration(t *testing.T) {
	mem := store.NewMemoryStore()
	srv := New(mem, nil)
	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")

	cat, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name: "Phụ kiện", Slug: "phu-kien", CommissionRateBps: 500,
	})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	catID := cat.GetCategory().GetId()

	if _, err := srv.CreateBannedKeyword(adminCtx, &productv1.CreateBannedKeywordRequest{
		Term: "ma túy", Action: productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_BLOCK,
	}); err != nil {
		t.Fatalf("CreateBannedKeyword BLOCK: %v", err)
	}
	if _, err := srv.CreateBannedKeyword(adminCtx, &productv1.CreateBannedKeywordRequest{
		Term: "cam kết 100%", Action: productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_REVIEW,
	}); err != nil {
		t.Fatalf("CreateBannedKeyword REVIEW: %v", err)
	}

	// 1. BLOCK keyword in SKU attribute VALUE -> 400 BANNED_CONTENT.
	if _, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: catID,
		Name:       "Hộp đựng đồ",
		Skus: []*productv1.SKUInput{{
			SkuCode:    "HD-1",
			Name:       "M",
			Attributes: map[string]string{"chất liệu": "chứa ma túy tổng hợp"},
			PriceVnd:   100000,
			Stock:      5,
		}},
	}); status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "BANNED_CONTENT") {
		t.Fatalf("BLOCK in SKU attribute value: want InvalidArgument BANNED_CONTENT, got %v", err)
	}

	// 2. BLOCK keyword in SKU attribute KEY -> 400 BANNED_CONTENT.
	if _, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: catID,
		Name:       "Hộp đựng đồ 2",
		Skus: []*productv1.SKUInput{{
			SkuCode:    "HD-2",
			Name:       "M",
			Attributes: map[string]string{"ma túy": "không"},
			PriceVnd:   100000,
			Stock:      5,
		}},
	}); status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "BANNED_CONTENT") {
		t.Fatalf("BLOCK in SKU attribute key: want InvalidArgument BANNED_CONTENT, got %v", err)
	}

	// 3. BLOCK keyword in custom slug -> 400 BANNED_CONTENT.
	if _, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: catID,
		Name:       "Áo thun trơn",
		Slug:       "ao-ma-tuy-gia-re",
		Skus:       []*productv1.SKUInput{{SkuCode: "AT-1", Name: "M", PriceVnd: 100000, Stock: 5}},
	}); status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "BANNED_CONTENT") {
		t.Fatalf("BLOCK in custom slug: want InvalidArgument BANNED_CONTENT, got %v", err)
	}

	// 4. sku_code and image_url must NOT be scanned.
	cleanProd, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: catID,
		Name:       "Áo thun trơn sạch",
		Slug:       "ao-thun-tron-sach",
		Images:     []string{"https://cdn.example.com/ma-tuy-not-scanned.jpg"},
		Skus: []*productv1.SKUInput{{
			SkuCode:  "MA-TUY-CODE-1",
			Name:     "M",
			ImageUrl: "https://cdn.example.com/ma-tuy-sku.jpg",
			PriceVnd: 100000,
			Stock:    5,
		}},
	})
	if err != nil {
		t.Fatalf("sku_code/image_url must not trigger moderation: %v", err)
	}
	if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: cleanProd.GetProduct().GetId(),
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	}); err != nil {
		t.Fatalf("Approve clean product: %v", err)
	}

	// 5. UpdateProduct adding BLOCK keyword to slug -> 400 BANNED_CONTENT.
	if _, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id:   cleanProd.GetProduct().GetId(),
		Slug: "ao-ma-tuy-edit",
	}); status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "BANNED_CONTENT") {
		t.Fatalf("UpdateProduct BLOCK in slug: want InvalidArgument BANNED_CONTENT, got %v", err)
	}

	// 6. UpdateProduct adding REVIEW keyword to SKU attributes -> demoted to PENDING with flagged_keywords.
	skuID := cleanProd.GetProduct().GetSkus()[0].GetId()
	updRev, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id: cleanProd.GetProduct().GetId(),
		Skus: []*productv1.SKUInput{{
			Id:         skuID,
			SkuCode:    "MA-TUY-CODE-1",
			Name:       "M",
			Attributes: map[string]string{"ghi chú": "cam kết 100% cotton"},
			PriceVnd:   100000,
			Stock:      5,
		}},
	})
	if err != nil {
		t.Fatalf("UpdateProduct REVIEW in attributes: %v", err)
	}
	if updRev.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_PENDING ||
		!reflect.DeepEqual(updRev.GetProduct().GetFlaggedKeywords(), []string{"cam kết 100%"}) {
		t.Fatalf("UpdateProduct REVIEW in attributes: status=%v flagged=%v", updRev.GetProduct().GetStatus(), updRev.GetProduct().GetFlaggedKeywords())
	}

	// 7. PreviewBannedKeyword and Rescan detect keyword inside existing SKU attribute value and slug.
	attrProd, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: catID,
		Name:       "Sản phẩm bình thường",
		Slug:       "sp-doc-quyen-slug",
		Skus: []*productv1.SKUInput{{
			SkuCode:    "SP-ATTR-1",
			Name:       "L",
			Attributes: map[string]string{"nguồn gốc": "nhập lậu bí mật"},
			PriceVnd:   120000,
			Stock:      3,
		}},
	})
	if err != nil {
		t.Fatalf("CreateProduct attrProd: %v", err)
	}
	if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: attrProd.GetProduct().GetId(),
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	}); err != nil {
		t.Fatalf("Approve attrProd: %v", err)
	}
	prevAttr, err := srv.PreviewBannedKeyword(adminCtx, &productv1.PreviewBannedKeywordRequest{Term: "nhập lậu"})
	if err != nil || prevAttr.GetAffectedCount() != 1 {
		t.Fatalf("PreviewBannedKeyword in SKU attribute: affected=%d err=%v, want 1", prevAttr.GetAffectedCount(), err)
	}
	prevSlug, err := srv.PreviewBannedKeyword(adminCtx, &productv1.PreviewBannedKeywordRequest{Term: "độc quyền"})
	if err != nil || prevSlug.GetAffectedCount() != 1 {
		t.Fatalf("PreviewBannedKeyword in slug: affected=%d err=%v, want 1", prevSlug.GetAffectedCount(), err)
	}

	// Add BLOCK keyword matching attribute -> AdminReviewProduct(APPROVE) on new pending & Rescan on approved both catch it.
	if _, err := srv.CreateBannedKeyword(adminCtx, &productv1.CreateBannedKeywordRequest{
		Term: "nhập lậu", Action: productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_BLOCK,
	}); err != nil {
		t.Fatalf("CreateBannedKeyword nhập lậu: %v", err)
	}
	rescanResp, err := srv.RescanProductsForBannedContent(adminCtx, &productv1.RescanProductsForBannedContentRequest{})
	if err != nil || rescanResp.GetBlockedCount() != 1 {
		t.Fatalf("Rescan catching SKU attribute BLOCK: resp=%+v err=%v", rescanResp, err)
	}
	afterAttr, _ := srv.GetProduct(adminCtx, &productv1.GetProductRequest{Id: attrProd.GetProduct().GetId()})
	if afterAttr.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_REJECTED {
		t.Fatalf("attrProd status = %v, want REJECTED", afterAttr.GetProduct().GetStatus())
	}
	// Item 9: Vietnamese rejection_reason for seller email/UI.
	if !strings.Contains(afterAttr.GetProduct().GetRejectionReason(), "Nội dung chứa từ khoá cấm: nhập lậu") {
		t.Fatalf("rejection_reason = %q, want Vietnamese 'Nội dung chứa từ khoá cấm: nhập lậu'", afterAttr.GetProduct().GetRejectionReason())
	}
}

// TestW3B_ReviewFix_3_AdminApprovedReviewNotFlipped verifies that an admin-approved product
// with existing REVIEW flagged_keywords is NOT flipped back to PENDING by rescan or price/stock edits
// unless a NEW REVIEW keyword (not in existing flagged_keywords) matches.
func TestW3B_ReviewFix_3_AdminApprovedReviewNotFlipped(t *testing.T) {
	mem := store.NewMemoryStore()
	srv := New(mem, nil)
	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")

	cat, _ := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{Name: "Trà", Slug: "tra", CommissionRateBps: 500})
	kw1, _ := srv.CreateBannedKeyword(adminCtx, &productv1.CreateBannedKeywordRequest{
		Term: "cam kết 100%", Action: productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_REVIEW,
	})
	if _, err := srv.CreateBannedKeyword(adminCtx, &productv1.CreateBannedKeywordRequest{
		Term: "chữa bách bệnh", Action: productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_REVIEW,
	}); err != nil {
		t.Fatalf("CreateBannedKeyword kw2: %v", err)
	}

	prod, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId:  cat.GetCategory().GetId(),
		Name:        "Trà xanh",
		Description: "Cam kết 100% lá trà tươi",
		Skus:        []*productv1.SKUInput{{SkuCode: "TX-1", Name: "Hộp", PriceVnd: 100000, Stock: 10}},
	})
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	prodID := prod.GetProduct().GetId()
	skuID := prod.GetProduct().GetSkus()[0].GetId()

	// Admin explicitly approves the REVIEW-flagged product.
	approved, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: prodID,
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	})
	if err != nil || approved.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_APPROVED ||
		!reflect.DeepEqual(approved.GetProduct().GetFlaggedKeywords(), []string{"cam kết 100%"}) {
		t.Fatalf("Admin approve REVIEW product: %+v err=%v", approved, err)
	}

	// 1. Seller edits ONLY price/stock -> MUST stay APPROVED!
	priceEdit, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id: prodID,
		Skus: []*productv1.SKUInput{{
			Id: skuID, SkuCode: "TX-1", Name: "Hộp", PriceVnd: 120000, Stock: 25,
		}},
	})
	if err != nil {
		t.Fatalf("UpdateProduct price/stock: %v", err)
	}
	if priceEdit.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_APPROVED {
		t.Fatalf("price/stock edit flipped admin-approved REVIEW product to %v (want APPROVED)", priceEdit.GetProduct().GetStatus())
	}

	// 2. Rescan runs -> MUST stay APPROVED and flagged_count == 0!
	rescan1, err := srv.RescanProductsForBannedContent(adminCtx, &productv1.RescanProductsForBannedContentRequest{})
	if err != nil {
		t.Fatalf("Rescan 1: %v", err)
	}
	afterRescan1, _ := srv.GetProduct(adminCtx, &productv1.GetProductRequest{Id: prodID})
	if afterRescan1.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_APPROVED || rescan1.GetFlaggedCount() != 0 {
		t.Fatalf("Rescan flipped admin-approved REVIEW product: status=%v flagged_count=%d", afterRescan1.GetProduct().GetStatus(), rescan1.GetFlaggedCount())
	}

	// 3. Seller adds a NEW REVIEW keyword ("chữa bách bệnh") not in approved flagged_keywords -> MUST demote to PENDING!
	newFlagEdit, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id:          prodID,
		Description: "Cam kết 100% lá trà tươi, chữa bách bệnh",
	})
	if err != nil {
		t.Fatalf("UpdateProduct adding new REVIEW kw: %v", err)
	}
	if newFlagEdit.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_PENDING ||
		!reflect.DeepEqual(newFlagEdit.GetProduct().GetFlaggedKeywords(), []string{"cam kết 100%", "chữa bách bệnh"}) {
		t.Fatalf("adding new REVIEW kw: status=%v flagged=%v, want PENDING with both keywords",
			newFlagEdit.GetProduct().GetStatus(), newFlagEdit.GetProduct().GetFlaggedKeywords())
	}

	// 4. Admin approves again with both flags; then kw1 is deactivated -> Rescan keeps APPROVED and shrinks flagged_keywords to subset!
	if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: prodID,
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	}); err != nil {
		t.Fatalf("Re-approve with both flags: %v", err)
	}
	if _, err := srv.UpdateBannedKeyword(adminCtx, &productv1.UpdateBannedKeywordRequest{
		Id: kw1.GetKeyword().GetId(), IsActive: false, HasIsActive: true,
	}); err != nil {
		t.Fatalf("Deactivate kw1: %v", err)
	}
	rescanSubset, err := srv.RescanProductsForBannedContent(adminCtx, &productv1.RescanProductsForBannedContentRequest{})
	if err != nil {
		t.Fatalf("Rescan subset: %v", err)
	}
	afterSubset, _ := srv.GetProduct(adminCtx, &productv1.GetProductRequest{Id: prodID})
	if afterSubset.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_APPROVED ||
		!reflect.DeepEqual(afterSubset.GetProduct().GetFlaggedKeywords(), []string{"chữa bách bệnh"}) ||
		rescanSubset.GetFlaggedCount() != 0 {
		t.Fatalf("subset rescan: status=%v flagged=%v flagged_count=%d",
			afterSubset.GetProduct().GetStatus(), afterSubset.GetProduct().GetFlaggedKeywords(), rescanSubset.GetFlaggedCount())
	}
}

// TestW3B_ReviewFix_4_HiddenProductReviewRescanPreserved verifies HIDDEN + REVIEW is untouched by rescan,
// caught when seller unhides via UpdateProduct, while HIDDEN + BLOCK still becomes REJECTED.
func TestW3B_ReviewFix_4_HiddenProductReviewRescanPreserved(t *testing.T) {
	mem := store.NewMemoryStore()
	srv := New(mem, nil)
	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")

	cat, _ := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{Name: "Đồ chơi", Slug: "do-choi", CommissionRateBps: 500})

	pRev, _ := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: cat.GetCategory().GetId(), Name: "Gấu bông bảo hành trọn đời",
		Skus: []*productv1.SKUInput{{SkuCode: "GB-1", PriceVnd: 100000, Stock: 5}},
	})
	pBlk, _ := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: cat.GetCategory().GetId(), Name: "Súng nước nhựa độc hại cấm",
		Skus: []*productv1.SKUInput{{SkuCode: "SN-1", PriceVnd: 100000, Stock: 5}},
	})
	for _, pid := range []string{pRev.GetProduct().GetId(), pBlk.GetProduct().GetId()} {
		_, _ = srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
			ProductId: pid, Decision: productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
		})
		_, _ = srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
			Id: pid, Status: productv1.ProductStatus_PRODUCT_STATUS_HIDDEN,
		})
	}

	_, _ = srv.CreateBannedKeyword(adminCtx, &productv1.CreateBannedKeywordRequest{
		Term: "bảo hành trọn đời", Action: productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_REVIEW,
	})
	_, _ = srv.CreateBannedKeyword(adminCtx, &productv1.CreateBannedKeywordRequest{
		Term: "độc hại cấm", Action: productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_BLOCK,
	})

	rescan, err := srv.RescanProductsForBannedContent(adminCtx, &productv1.RescanProductsForBannedContentRequest{})
	if err != nil {
		t.Fatalf("Rescan: %v", err)
	}
	if rescan.GetBlockedCount() != 1 || rescan.GetFlaggedCount() != 0 {
		t.Fatalf("Rescan HIDDEN products: blocked=%d flagged=%d, want 1 and 0", rescan.GetBlockedCount(), rescan.GetFlaggedCount())
	}

	revAfter, _ := srv.GetProduct(adminCtx, &productv1.GetProductRequest{Id: pRev.GetProduct().GetId()})
	if revAfter.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_HIDDEN || len(revAfter.GetProduct().GetFlaggedKeywords()) != 0 {
		t.Fatalf("HIDDEN + REVIEW product must stay HIDDEN with unchanged flagged_keywords, got status=%v flagged=%v",
			revAfter.GetProduct().GetStatus(), revAfter.GetProduct().GetFlaggedKeywords())
	}

	blkAfter, _ := srv.GetProduct(adminCtx, &productv1.GetProductRequest{Id: pBlk.GetProduct().GetId()})
	if blkAfter.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_REJECTED {
		t.Fatalf("HIDDEN + BLOCK product must become REJECTED, got %v", blkAfter.GetProduct().GetStatus())
	}

	// When seller tries to unhide pRev -> UpdateProduct catches REVIEW keyword and moves to PENDING with flagged_keywords!
	unhide, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id: pRev.GetProduct().GetId(), Status: productv1.ProductStatus_PRODUCT_STATUS_APPROVED,
	})
	if err != nil {
		t.Fatalf("Seller unhide pRev: %v", err)
	}
	if unhide.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_PENDING ||
		!reflect.DeepEqual(unhide.GetProduct().GetFlaggedKeywords(), []string{"bảo hành trọn đời"}) {
		t.Fatalf("unhide HIDDEN+REVIEW product: status=%v flagged=%v, want PENDING + [bảo hành trọn đời]",
			unhide.GetProduct().GetStatus(), unhide.GetProduct().GetFlaggedKeywords())
	}
}

// TestW3B_ReviewFix_5_RescanKeywordIDsComputesFullFlaggedKeywords verifies keyword_ids selects candidates
// while flagged_keywords is always computed against ALL active keywords.
func TestW3B_ReviewFix_5_RescanKeywordIDsComputesFullFlaggedKeywords(t *testing.T) {
	mem := store.NewMemoryStore()
	srv := New(mem, nil)
	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")

	cat, _ := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{Name: "Thực phẩm", Slug: "thuc-pham", CommissionRateBps: 500})

	pBoth, _ := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId:  cat.GetCategory().GetId(),
		Name:        "Mật ong rừng",
		Description: "Cam kết 100% nguyên chất, chữa bách bệnh",
		Skus:        []*productv1.SKUInput{{SkuCode: "MO-1", PriceVnd: 200000, Stock: 10}},
	})
	pOnly2, _ := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId:  cat.GetCategory().GetId(),
		Name:        "Trà gừng",
		Description: "Giúp chữa bách bệnh mùa đông",
		Skus:        []*productv1.SKUInput{{SkuCode: "TG-1", PriceVnd: 90000, Stock: 10}},
	})
	for _, pid := range []string{pBoth.GetProduct().GetId(), pOnly2.GetProduct().GetId()} {
		_, _ = srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
			ProductId: pid, Decision: productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
		})
	}

	kw1, _ := srv.CreateBannedKeyword(adminCtx, &productv1.CreateBannedKeywordRequest{
		Term: "cam kết 100%", Action: productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_REVIEW,
	})
	_, _ = srv.CreateBannedKeyword(adminCtx, &productv1.CreateBannedKeywordRequest{
		Term: "chữa bách bệnh", Action: productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_REVIEW,
	})

	// Rescan targeting ONLY kw1 -> pBoth is a candidate, pOnly2 is NOT.
	res, err := srv.RescanProductsForBannedContent(adminCtx, &productv1.RescanProductsForBannedContentRequest{
		KeywordIds: []string{kw1.GetKeyword().GetId()},
	})
	if err != nil {
		t.Fatalf("Rescan with keyword_ids: %v", err)
	}
	if res.GetFlaggedCount() != 1 {
		t.Fatalf("flagged_count = %d, want 1", res.GetFlaggedCount())
	}

	bothAfter, _ := srv.GetProduct(adminCtx, &productv1.GetProductRequest{Id: pBoth.GetProduct().GetId()})
	if bothAfter.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_PENDING {
		t.Fatalf("pBoth status = %v, want PENDING", bothAfter.GetProduct().GetStatus())
	}
	wantFullFlags := []string{"cam kết 100%", "chữa bách bệnh"}
	if !reflect.DeepEqual(bothAfter.GetProduct().GetFlaggedKeywords(), wantFullFlags) {
		t.Fatalf("pBoth flagged_keywords = %v, want full active set %v", bothAfter.GetProduct().GetFlaggedKeywords(), wantFullFlags)
	}

	only2After, _ := srv.GetProduct(adminCtx, &productv1.GetProductRequest{Id: pOnly2.GetProduct().GetId()})
	if only2After.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_APPROVED {
		t.Fatalf("pOnly2 (not a candidate of kw1) must stay APPROVED, got %v", only2After.GetProduct().GetStatus())
	}
}

// hookBeforeForUpdateStore wraps a store.Store to invoke a callback once before GetProductByIDForUpdate inside WithTx,
// deterministically simulating a concurrent seller edit between the scan batch read and the tx row lock.
type hookBeforeForUpdateStore struct {
	store.Store
	targetID string
	onBefore func()
	fired    bool
}

type hookTxQueries struct {
	store.Queries
	parent *hookBeforeForUpdateStore
}

func (h *hookBeforeForUpdateStore) WithTx(ctx context.Context, fn func(q store.Queries) error) error {
	return h.Store.WithTx(ctx, func(q store.Queries) error {
		return fn(&hookTxQueries{Queries: q, parent: h})
	})
}

func (q *hookTxQueries) GetProductByIDForUpdate(ctx context.Context, id string) (store.Product, error) {
	if id == q.parent.targetID && !q.parent.fired && q.parent.onBefore != nil {
		q.parent.fired = true
		q.parent.onBefore()
	}
	return q.Queries.GetProductByIDForUpdate(ctx, id)
}

// TestW3B_ReviewFix_6_RescanReevaluatesLockedCurInTx verifies rescan re-evaluates moderation against locked cur
// inside the transaction rather than deciding based on stale pre-tx scan row p.
func TestW3B_ReviewFix_6_RescanReevaluatesLockedCurInTx(t *testing.T) {
	mem := store.NewMemoryStore()
	hooked := &hookBeforeForUpdateStore{Store: mem}
	srv := New(hooked, nil)
	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")

	cat, _ := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{Name: "Gia dụng", Slug: "gia-dung-6", CommissionRateBps: 500})
	prod, _ := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: cat.GetCategory().GetId(),
		Name:       "Sản phẩm tạm vi phạm xyz",
		Slug:       "sp-tam-vi-pham",
		Skus:       []*productv1.SKUInput{{SkuCode: "TVP-1", PriceVnd: 100000, Stock: 5}},
	})
	prodID := prod.GetProduct().GetId()
	_, _ = srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: prodID, Decision: productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	})
	_, _ = srv.CreateBannedKeyword(adminCtx, &productv1.CreateBannedKeywordRequest{
		Term: "tạm vi phạm xyz", Action: productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_BLOCK,
	})

	// Simulate seller cleaning the product concurrently AFTER ListProductsForModerationScan read stale p,
	// right BEFORE Rescan's tx acquires GetProductByIDForUpdate(prodID).
	hooked.targetID = prodID
	hooked.onBefore = func() {
		_, updErr := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
			Id:   prodID,
			Name: "Sản phẩm đã làm sạch",
			Slug: "sp-da-lam-sach",
		})
		if updErr != nil {
			t.Errorf("concurrent seller cleanup failed: %v", updErr)
		}
	}

	outboxBefore := len(mem.OutboxEvents())
	res, err := srv.RescanProductsForBannedContent(adminCtx, &productv1.RescanProductsForBannedContentRequest{})
	if err != nil {
		t.Fatalf("Rescan: %v", err)
	}
	if !hooked.fired {
		t.Fatal("test hook did not fire")
	}
	if res.GetBlockedCount() != 0 || res.GetFlaggedCount() != 0 {
		t.Fatalf("Rescan acted on stale p after cur was cleaned: blocked=%d flagged=%d, want 0/0", res.GetBlockedCount(), res.GetFlaggedCount())
	}
	after, _ := srv.GetProduct(adminCtx, &productv1.GetProductRequest{Id: prodID})
	if after.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_APPROVED {
		t.Fatalf("cleaned product status = %v, want APPROVED", after.GetProduct().GetStatus())
	}
	if len(mem.OutboxEvents()) != outboxBefore {
		t.Fatalf("expected 0 new outbox events for skipped cleaned product, got %d", len(mem.OutboxEvents())-outboxBefore)
	}
}

// TestW3B_ReviewFix_7_SellerCanHideViolatingProductOnly verifies a seller can hide (status -> HIDDEN)
// an existing violating product without changing any other fields, while any other edit or unhide is blocked.
func TestW3B_ReviewFix_7_SellerCanHideViolatingProductOnly(t *testing.T) {
	mem := store.NewMemoryStore()
	srv := New(mem, nil)
	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")

	catNorm, _ := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{Name: "Cat Norm", Slug: "cat-norm-7", CommissionRateBps: 500})
	catLaterProhib, _ := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{Name: "Cat Prohib", Slug: "cat-prohib-7", CommissionRateBps: 500})

	pBlk, _ := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: catNorm.GetCategory().GetId(), Name: "Sản phẩm chứa thuốc lắc giả", Slug: "sp-thuoc-lac-7",
		Skus: []*productv1.SKUInput{{SkuCode: "TL-7", PriceVnd: 100000, Stock: 5}},
	})
	pCat, _ := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: catLaterProhib.GetCategory().GetId(), Name: "Sản phẩm danh mục cấm", Slug: "sp-dm-cam-7",
		Skus: []*productv1.SKUInput{{SkuCode: "DM-7", PriceVnd: 100000, Stock: 5}},
	})
	for _, pid := range []string{pBlk.GetProduct().GetId(), pCat.GetProduct().GetId()} {
		_, _ = srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
			ProductId: pid, Decision: productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
		})
	}

	// Now add BLOCK keyword and mark catLaterProhib prohibited.
	_, _ = srv.CreateBannedKeyword(adminCtx, &productv1.CreateBannedKeywordRequest{
		Term: "thuốc lắc", Action: productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_BLOCK,
	})
	_, _ = srv.UpdateCategory(adminCtx, &productv1.UpdateCategoryRequest{
		Id: catLaterProhib.GetCategory().GetId(), IsProhibited: true, HasIsProhibited: true,
	})

	// 1. Changing name while hiding -> STILL blocked!
	if _, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id: pBlk.GetProduct().GetId(), Status: productv1.ProductStatus_PRODUCT_STATUS_HIDDEN, Name: "Đổi tên kèm thuốc lắc",
	}); status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "BANNED_CONTENT") {
		t.Fatalf("hide + name edit on BLOCK product: want BANNED_CONTENT, got %v", err)
	}
	// 2. Changing SKU attribute while hiding -> STILL blocked!
	if _, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id: pCat.GetProduct().GetId(), Status: productv1.ProductStatus_PRODUCT_STATUS_HIDDEN,
		Skus: []*productv1.SKUInput{{
			Id: pCat.GetProduct().GetSkus()[0].GetId(), SkuCode: "DM-7", Name: "Default",
			Attributes: map[string]string{"màu": "đỏ"}, PriceVnd: 99000, Stock: 5,
		}},
	}); status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "PROHIBITED_CATEGORY") {
		t.Fatalf("hide + SKU attribute edit on prohibited-category product: want PROHIBITED_CATEGORY, got %v", err)
	}

	// 3. Pure hide-only (no content/category/SKU changes) MUST succeed for BOTH BLOCK and prohibited-category products!
	hidBlk, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id: pBlk.GetProduct().GetId(), Status: productv1.ProductStatus_PRODUCT_STATUS_HIDDEN,
	})
	if err != nil || hidBlk.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_HIDDEN {
		t.Fatalf("pure hide on BLOCK product failed: resp=%+v err=%v", hidBlk, err)
	}
	hidCat, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id: pCat.GetProduct().GetId(), Status: productv1.ProductStatus_PRODUCT_STATUS_HIDDEN,
	})
	if err != nil || hidCat.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_HIDDEN {
		t.Fatalf("pure hide on prohibited-category product failed: resp=%+v err=%v", hidCat, err)
	}

	// 4. Attempting to unhide either violating product MUST be blocked!
	if _, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id: pBlk.GetProduct().GetId(), Status: productv1.ProductStatus_PRODUCT_STATUS_APPROVED,
	}); status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "BANNED_CONTENT") {
		t.Fatalf("unhide BLOCK product: want BANNED_CONTENT, got %v", err)
	}
	if _, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id: pCat.GetProduct().GetId(), Status: productv1.ProductStatus_PRODUCT_STATUS_APPROVED,
	}); status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "PROHIBITED_CATEGORY") {
		t.Fatalf("unhide prohibited-category product: want PROHIBITED_CATEGORY, got %v", err)
	}

	// 5. Rescan on prohibited category emits Vietnamese rejection_reason in both product and product.reviewed outbox (Item 9).
	rescan, err := srv.RescanProductsForBannedContent(adminCtx, &productv1.RescanProductsForBannedContentRequest{})
	if err != nil || rescan.GetBlockedCount() != 2 {
		t.Fatalf("Rescan violating hidden products: resp=%+v err=%v", rescan, err)
	}
	pCatRej, _ := srv.GetProduct(adminCtx, &productv1.GetProductRequest{Id: pCat.GetProduct().GetId()})
	if pCatRej.GetProduct().GetRejectionReason() != "Danh mục thuộc diện cấm kinh doanh" {
		t.Fatalf("prohibited category rejection_reason = %q, want Vietnamese reason", pCatRej.GetProduct().GetRejectionReason())
	}
	var sawVietReason bool
	for _, ev := range mem.OutboxEvents() {
		if ev.EventType != events.SubjectProductReviewed {
			continue
		}
		var pl events.ProductReviewedPayload
		if json.Unmarshal(ev.PayloadJSON, &pl) == nil && pl.ProductID == pCat.GetProduct().GetId() && pl.Decision == "REJECT" {
			if pl.Reason == "Danh mục thuộc diện cấm kinh doanh" {
				sawVietReason = true
			}
		}
	}
	if !sawVietReason {
		t.Fatal("expected Vietnamese rejection_reason in product.reviewed outbox payload")
	}
}

// TestW3B_ReviewFix_8_CreateReviewDoesNotChangeProductUpdatedAt verifies IncrementProductRating
// leaves products.updated_at unchanged.
func TestW3B_ReviewFix_8_CreateReviewDoesNotChangeProductUpdatedAt(t *testing.T) {
	mem := store.NewMemoryStore()
	srv := New(mem, nil)
	t0 := time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)
	srv.SetClock(func() time.Time { return t0 })

	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")
	buyerCtx := ctxWithRoleAndToken("buyer-8", "buyer", "", "Bearer tok-8")

	cat, _ := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{Name: "C8", Slug: "c8", CommissionRateBps: 500})
	prod, _ := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: cat.GetCategory().GetId(), Name: "Prod 8",
		Skus: []*productv1.SKUInput{{SkuCode: "P8-1", PriceVnd: 100000, Stock: 5}},
	})
	prodID := prod.GetProduct().GetId()
	skuID := prod.GetProduct().GetSkus()[0].GetId()
	approved, _ := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: prodID, Decision: productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	})
	updatedAtBefore := approved.GetProduct().GetUpdatedAt().AsTime()

	srv.SetOrderClient(&fakeOrderClient{
		fn: func(_ context.Context, _ *orderv1.GetOrderRequest) (*orderv1.GetOrderResponse, error) {
			return &orderv1.GetOrderResponse{Order: &orderv1.Order{
				Id: "ord-8", BuyerId: "buyer-8", ShopId: "shop-1",
				Status: orderv1.OrderStatus_ORDER_STATUS_COMPLETED,
				Items:  []*orderv1.OrderItem{{Id: "oi-8", OrderId: "ord-8", ProductId: prodID, SkuId: skuID, SkuName: "Default"}},
			}}, nil
		},
	})

	// Advance clock by 2 hours before creating review.
	srv.SetClock(func() time.Time { return t0.Add(2 * time.Hour) })
	if _, err := srv.CreateReview(buyerCtx, &productv1.CreateReviewRequest{
		OrderId: "ord-8", OrderItemId: "oi-8", Rating: 5, Comment: "Tốt",
	}); err != nil {
		t.Fatalf("CreateReview: %v", err)
	}

	after, _ := srv.GetProduct(context.Background(), &productv1.GetProductRequest{Id: prodID})
	if !after.GetProduct().GetUpdatedAt().AsTime().Equal(updatedAtBefore) {
		t.Fatalf("CreateReview mutated products.updated_at: before=%v after=%v", updatedAtBefore, after.GetProduct().GetUpdatedAt().AsTime())
	}
}

// TestW3B_ReviewFix_A_CannotBypassReviewViaHidden verifies sellers cannot smuggle unapproved REVIEW keywords
// into flagged_keywords via HIDDEN (either in one hide+edit call or by editing while already HIDDEN) and then unhide to APPROVED.
func TestW3B_ReviewFix_A_CannotBypassReviewViaHidden(t *testing.T) {
	mem := store.NewMemoryStore()
	srv := New(mem, nil)
	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")

	cat, _ := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{Name: "Cat A", Slug: "cat-a", CommissionRateBps: 500})
	_, _ = srv.CreateBannedKeyword(adminCtx, &productv1.CreateBannedKeywordRequest{
		Term: "cam kết 100%", Action: productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_REVIEW,
	})
	_, _ = srv.CreateBannedKeyword(adminCtx, &productv1.CreateBannedKeywordRequest{
		Term: "chữa bách bệnh", Action: productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_REVIEW,
	})

	// Path 1: APPROVED clean product -> hide + inject REVIEW keyword in one call -> unhide MUST demote to PENDING!
	p1, _ := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: cat.GetCategory().GetId(), Name: "Trà sạch 1", Slug: "tra-sach-1",
		Skus: []*productv1.SKUInput{{SkuCode: "A1-1", PriceVnd: 100000, Stock: 10}},
	})
	_, _ = srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: p1.GetProduct().GetId(), Decision: productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	})
	hid1, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id:          p1.GetProduct().GetId(),
		Status:      productv1.ProductStatus_PRODUCT_STATUS_HIDDEN,
		Description: "Cam kết 100% nguyên chất",
	})
	if err != nil {
		t.Fatalf("Path 1 hide+edit: %v", err)
	}
	if len(hid1.GetProduct().GetFlaggedKeywords()) != 0 {
		t.Fatalf("Path 1 hide+edit must not record unapproved REVIEW keyword in flagged_keywords, got %v", hid1.GetProduct().GetFlaggedKeywords())
	}
	unhid1, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id:     p1.GetProduct().GetId(),
		Status: productv1.ProductStatus_PRODUCT_STATUS_APPROVED,
	})
	if err != nil {
		t.Fatalf("Path 1 unhide: %v", err)
	}
	if unhid1.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_PENDING ||
		!reflect.DeepEqual(unhid1.GetProduct().GetFlaggedKeywords(), []string{"cam kết 100%"}) {
		t.Fatalf("Path 1 unhide bypassed REVIEW moderation: status=%v flagged=%v, want PENDING + [cam kết 100%%]",
			unhid1.GetProduct().GetStatus(), unhid1.GetProduct().GetFlaggedKeywords())
	}

	// Path 2: APPROVED clean product -> hide -> edit while HIDDEN adding REVIEW keyword -> unhide MUST demote to PENDING!
	p2, _ := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: cat.GetCategory().GetId(), Name: "Trà sạch 2", Slug: "tra-sach-2",
		Skus: []*productv1.SKUInput{{SkuCode: "A2-1", PriceVnd: 100000, Stock: 10}},
	})
	_, _ = srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: p2.GetProduct().GetId(), Decision: productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	})
	_, _ = srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id: p2.GetProduct().GetId(), Status: productv1.ProductStatus_PRODUCT_STATUS_HIDDEN,
	})
	editWhileHid, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id:          p2.GetProduct().GetId(),
		Description: "Uống vào chữa bách bệnh",
	})
	if err != nil {
		t.Fatalf("Path 2 edit while HIDDEN: %v", err)
	}
	if len(editWhileHid.GetProduct().GetFlaggedKeywords()) != 0 {
		t.Fatalf("Path 2 edit while HIDDEN recorded unapproved keyword: %v", editWhileHid.GetProduct().GetFlaggedKeywords())
	}
	unhid2, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id:     p2.GetProduct().GetId(),
		Status: productv1.ProductStatus_PRODUCT_STATUS_APPROVED,
	})
	if err != nil {
		t.Fatalf("Path 2 unhide: %v", err)
	}
	if unhid2.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_PENDING ||
		!reflect.DeepEqual(unhid2.GetProduct().GetFlaggedKeywords(), []string{"chữa bách bệnh"}) {
		t.Fatalf("Path 2 unhide bypassed REVIEW moderation: status=%v flagged=%v, want PENDING + [chữa bách bệnh]",
			unhid2.GetProduct().GetStatus(), unhid2.GetProduct().GetFlaggedKeywords())
	}

	// Path 3: Product with admin-approved REVIEW keyword ("cam kết 100%") -> hide & unhide stays APPROVED;
	// hiding + adding a SECOND unapproved REVIEW keyword preserves only the approved one and demotes on unhide!
	p3, _ := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId:  cat.GetCategory().GetId(),
		Name:        "Trà đã duyệt",
		Description: "Cam kết 100% lá tươi",
		Slug:        "tra-da-duyet",
		Skus:        []*productv1.SKUInput{{SkuCode: "A3-1", PriceVnd: 100000, Stock: 10}},
	})
	_, _ = srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: p3.GetProduct().GetId(), Decision: productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	})
	_, _ = srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id: p3.GetProduct().GetId(), Status: productv1.ProductStatus_PRODUCT_STATUS_HIDDEN,
	})
	unhid3, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id: p3.GetProduct().GetId(), Status: productv1.ProductStatus_PRODUCT_STATUS_APPROVED,
	})
	if err != nil || unhid3.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_APPROVED {
		t.Fatalf("Path 3 hide/unhide of admin-approved REVIEW product: resp=%+v err=%v, want APPROVED", unhid3, err)
	}
	hid3WithNew, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id:          p3.GetProduct().GetId(),
		Status:      productv1.ProductStatus_PRODUCT_STATUS_HIDDEN,
		Description: "Cam kết 100% lá tươi, chữa bách bệnh",
	})
	if err != nil {
		t.Fatalf("Path 3 hide+add second keyword: %v", err)
	}
	if !reflect.DeepEqual(hid3WithNew.GetProduct().GetFlaggedKeywords(), []string{"cam kết 100%"}) {
		t.Fatalf("Path 3 intersection on HIDDEN = %v, want only pre-approved [cam kết 100%%]", hid3WithNew.GetProduct().GetFlaggedKeywords())
	}
	unhid3New, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id: p3.GetProduct().GetId(), Status: productv1.ProductStatus_PRODUCT_STATUS_APPROVED,
	})
	if err != nil || unhid3New.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_PENDING ||
		!reflect.DeepEqual(unhid3New.GetProduct().GetFlaggedKeywords(), []string{"cam kết 100%", "chữa bách bệnh"}) {
		t.Fatalf("Path 3 unhide after adding second keyword: status=%v flagged=%v err=%v",
			unhid3New.GetProduct().GetStatus(), unhid3New.GetProduct().GetFlaggedKeywords(), err)
	}
}

// TestW3B_ReviewFix_B_HideFromFullSellerFormWithSKUs verifies hiding works when the seller form
// submits full name/description/images/skus (even changing price/stock/weight/images or SKU order),
// while still blocking if any moderated field (name, description, slug, category, SKU name, SKU attribute) changes.
func TestW3B_ReviewFix_B_HideFromFullSellerFormWithSKUs(t *testing.T) {
	mem := store.NewMemoryStore()
	srv := New(mem, nil)
	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")

	cat, _ := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{Name: "Cat B", Slug: "cat-b", CommissionRateBps: 500})
	catID := cat.GetCategory().GetId()

	created, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId:  catID,
		Name:        "Hộp thuốc cấm vi phạm",
		Description: "Mô tả cũ",
		Slug:        "hop-thuoc-cam-b",
		Images:      []string{"https://cdn.example.com/old.jpg"},
		Skus: []*productv1.SKUInput{
			{SkuCode: "B-1", Name: "Nhỏ", Attributes: map[string]string{"màu": "đỏ", "cỡ": "S"}, PriceVnd: 100000, Stock: 10, WeightGrams: 200},
			{SkuCode: "B-2", Name: "Lớn", Attributes: map[string]string{"màu": "xanh", "cỡ": "L"}, PriceVnd: 150000, Stock: 5, WeightGrams: 400},
		},
	})
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	prodID := created.GetProduct().GetId()
	sku1 := findSKUByCode(t, created.GetProduct().GetSkus(), "B-1")
	sku2 := findSKUByCode(t, created.GetProduct().GetSkus(), "B-2")

	_, _ = srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: prodID, Decision: productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	})
	// Now ban a keyword matching the live product.
	_, _ = srv.CreateBannedKeyword(adminCtx, &productv1.CreateBannedKeywordRequest{
		Term: "thuốc cấm", Action: productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_BLOCK,
	})

	// 1. Changing ONE SKU attribute + HIDDEN -> MUST STILL be blocked!
	if _, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id:          prodID,
		CategoryId:  catID,
		Name:        "Hộp thuốc cấm vi phạm",
		Description: "Mô tả cũ",
		Slug:        "hop-thuoc-cam-b",
		Status:      productv1.ProductStatus_PRODUCT_STATUS_HIDDEN,
		Skus: []*productv1.SKUInput{
			{Id: sku1.GetId(), SkuCode: "B-1", Name: "Nhỏ", Attributes: map[string]string{"màu": "vàng", "cỡ": "S"}, PriceVnd: 100000, Stock: 10, WeightGrams: 200},
			{Id: sku2.GetId(), SkuCode: "B-2", Name: "Lớn", Attributes: map[string]string{"màu": "xanh", "cỡ": "L"}, PriceVnd: 150000, Stock: 5, WeightGrams: 400},
		},
	}); status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "BANNED_CONTENT") {
		t.Fatalf("changing one SKU attribute + HIDDEN: want BANNED_CONTENT, got %v", err)
	}

	// 2. Full form payload with IDENTICAL moderated fields (SKUs sent in reversed order, price/stock/weight/images updated) + HIDDEN -> MUST SUCCEED!
	hid, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id:          prodID,
		CategoryId:  catID,
		Name:        "Hộp thuốc cấm vi phạm",
		Description: "Mô tả cũ",
		Slug:        "hop-thuoc-cam-b",
		Images:      []string{"https://cdn.example.com/new.jpg"},
		Status:      productv1.ProductStatus_PRODUCT_STATUS_HIDDEN,
		Skus: []*productv1.SKUInput{
			{Id: sku2.GetId(), SkuCode: "B-2", Name: "Lớn", Attributes: map[string]string{"cỡ": "L", "màu": "xanh"}, PriceVnd: 160000, Stock: 8, WeightGrams: 450},
			{Id: sku1.GetId(), SkuCode: "B-1", Name: "Nhỏ", Attributes: map[string]string{"cỡ": "S", "màu": "đỏ"}, PriceVnd: 95000, Stock: 12, WeightGrams: 250},
		},
	})
	if err != nil || hid.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_HIDDEN {
		t.Fatalf("full seller form hide with identical moderated fields failed: resp=%+v err=%v", hid, err)
	}
}
