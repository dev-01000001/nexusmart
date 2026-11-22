package server

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/product/internal/store"
)

func withTestClaims(userID, role, shopID string) context.Context {
	claims := &auth.Claims{Role: role, ShopID: shopID}
	claims.Subject = userID
	return auth.WithClaims(context.Background(), claims)
}

func TestProductSlugGenerationLookupVisibilityAndUpdate(t *testing.T) {
	srv := New(store.NewMemoryStore(), nil)
	adminCtx := withTestClaims("admin-1", "admin", "")
	sellerACtx := withTestClaims("seller-a", "seller", "shop-a")
	sellerBCtx := withTestClaims("seller-b", "seller", "shop-b")
	buyerCtx := withTestClaims("buyer-1", "buyer", "")

	catResp, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name:              "Áo thun",
		Slug:              "ao-thun",
		CommissionRateBps: 500,
	})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	catID := catResp.GetCategory().GetId()

	// 1. Auto-generated Vietnamese slug from product name.
	p1Resp, err := srv.CreateProduct(sellerACtx, &productv1.CreateProductRequest{
		CategoryId: catID,
		Name:       "Áo thun Cotton Compact 220gsm",
		Skus:       []*productv1.SKUInput{{SkuCode: "AT-01", Name: "Trắng / M", PriceVnd: 199000, Stock: 25}},
	})
	if err != nil {
		t.Fatalf("CreateProduct p1: %v", err)
	}
	p1 := p1Resp.GetProduct()
	if p1.GetSlug() != "ao-thun-cotton-compact-220gsm" {
		t.Fatalf("expected Vietnamese slug ao-thun-cotton-compact-220gsm, got %q", p1.GetSlug())
	}

	// 2. Second product with identical name and omitted slug auto-suffixes cleanly without collision error.
	p2Resp, err := srv.CreateProduct(sellerACtx, &productv1.CreateProductRequest{
		CategoryId: catID,
		Name:       "Áo thun Cotton Compact 220gsm",
		Skus:       []*productv1.SKUInput{{SkuCode: "AT-02", Name: "Đen / L", PriceVnd: 199000, Stock: 10}},
	})
	if err != nil {
		t.Fatalf("CreateProduct p2 duplicate name: %v", err)
	}
	if !strings.HasPrefix(p2Resp.GetProduct().GetSlug(), "ao-thun-cotton-compact-220gsm-") {
		t.Fatalf("expected suffixed slug, got %q", p2Resp.GetProduct().GetSlug())
	}

	// 3. Explicit slug works; duplicate explicit slug rejected; invalid slug rejected.
	p3Resp, err := srv.CreateProduct(sellerACtx, &productv1.CreateProductRequest{
		CategoryId: catID,
		Name:       "Quần Jogger Nỉ",
		Slug:       "quan-jogger-ni-320gsm",
		Skus:       []*productv1.SKUInput{{SkuCode: "QJ-01", Name: "Xám / L", PriceVnd: 329000, Stock: 15}},
	})
	if err != nil {
		t.Fatalf("CreateProduct p3 explicit slug: %v", err)
	}
	if p3Resp.GetProduct().GetSlug() != "quan-jogger-ni-320gsm" {
		t.Fatalf("expected explicit slug quan-jogger-ni-320gsm, got %q", p3Resp.GetProduct().GetSlug())
	}
	if _, err := srv.CreateProduct(sellerACtx, &productv1.CreateProductRequest{
		CategoryId: catID,
		Name:       "Quần Jogger Khác",
		Slug:       "quan-jogger-ni-320gsm",
		Skus:       []*productv1.SKUInput{{SkuCode: "QJ-02", PriceVnd: 329000, Stock: 5}},
	}); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("expected AlreadyExists on duplicate explicit slug, got %v", err)
	}
	if _, err := srv.CreateProduct(sellerACtx, &productv1.CreateProductRequest{
		CategoryId: catID,
		Name:       "Bad Slug",
		Slug:       "---",
		Skus:       []*productv1.SKUInput{{SkuCode: "BS-01", PriceVnd: 100000, Stock: 1}},
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument on invalid slug, got %v", err)
	}

	// 4. Visibility on GetProduct by slug while PENDING: hidden from anon/buyer/other seller, visible to owner & admin.
	for name, ctx := range map[string]context.Context{
		"anonymous": context.Background(),
		"buyer":     buyerCtx,
		"seller-b":  sellerBCtx,
	} {
		if _, err := srv.GetProduct(ctx, &productv1.GetProductRequest{Slug: p1.GetSlug()}); status.Code(err) != codes.NotFound {
			t.Fatalf("%s GetProduct(slug) on PENDING: want NotFound, got %v", name, err)
		}
	}
	if got, err := srv.GetProduct(sellerACtx, &productv1.GetProductRequest{Slug: p1.GetSlug()}); err != nil || got.GetProduct().GetId() != p1.GetId() {
		t.Fatalf("owner seller GetProduct(slug) on PENDING failed: got=%+v err=%v", got, err)
	}
	if got, err := srv.GetProduct(adminCtx, &productv1.GetProductRequest{Slug: p1.GetSlug()}); err != nil || got.GetProduct().GetId() != p1.GetId() {
		t.Fatalf("admin GetProduct(slug) on PENDING failed: got=%+v err=%v", got, err)
	}

	// 5. After admin approval, anonymous GetProduct(slug) returns product + SKUs + slug.
	if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: p1.GetId(),
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	}); err != nil {
		t.Fatalf("AdminReviewProduct: %v", err)
	}
	pubGot, err := srv.GetProduct(context.Background(), &productv1.GetProductRequest{Slug: p1.GetSlug()})
	if err != nil {
		t.Fatalf("public GetProduct(slug) after approval: %v", err)
	}
	if pubGot.GetProduct().GetId() != p1.GetId() || pubGot.GetProduct().GetSlug() != p1.GetSlug() || len(pubGot.GetProduct().GetSkus()) != 1 {
		t.Fatalf("unexpected public product by slug: %+v", pubGot.GetProduct())
	}

	// 6. UpdateProduct can change slug and rejects colliding with another active product's slug.
	if _, err := srv.UpdateProduct(sellerACtx, &productv1.UpdateProductRequest{
		Id:   p1.GetId(),
		Slug: "quan-jogger-ni-320gsm",
	}); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("expected AlreadyExists on slug collision in UpdateProduct, got %v", err)
	}
	updResp, err := srv.UpdateProduct(sellerACtx, &productv1.UpdateProductRequest{
		Id:   p1.GetId(),
		Slug: "ao-thun-compact-v2",
	})
	if err != nil || updResp.GetProduct().GetSlug() != "ao-thun-compact-v2" {
		t.Fatalf("UpdateProduct slug failed: resp=%+v err=%v", updResp, err)
	}
}
