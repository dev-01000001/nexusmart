package server

import (
	"context"
	"fmt"
	"strings"
	"testing"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/product/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestProductImageURLOwnershipAndIDOR(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)

	const (
		shopA = "11111111-2222-4333-8444-555555555555"
		shopB = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	)
	hash1 := strings.Repeat("a", 64)
	hash2 := strings.Repeat("b", 64)

	adminCtx := ctxWithRole("admin-1", auth.RoleAdmin, "")
	sellerACtx := ctxWithRole("seller-a", auth.RoleSeller, shopA)
	sellerBCtx := ctxWithRole("seller-b", auth.RoleSeller, shopB)

	catResp, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name:              "Gom su",
		Slug:              "gom-su",
		CommissionRateBps: 500,
	})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	catID := catResp.GetCategory().GetId()

	shopAMediaJPG := fmt.Sprintf("/media/shops/%s/%s.jpg", shopA, hash1)
	shopAMediaWebP := fmt.Sprintf("/media/shops/%s/%s.webp", shopA, hash2)
	shopBMediaJPG := fmt.Sprintf("/media/shops/%s/%s.jpg", shopB, hash1)

	// 1. Seller A creates product with own /media/shops/<shopA>/... and sample /products/... -> succeeds.
	created, err := srv.CreateProduct(sellerACtx, &productv1.CreateProductRequest{
		CategoryId: catID,
		Name:       "Binh gom Bat Trang",
		Images:     []string{shopAMediaJPG, "/products/ao-thun-cotton-trang.png"},
		Skus: []*productv1.SKUInput{
			{SkuCode: "GOM-1", Name: "Men ngoc", PriceVnd: 250000, Stock: 10, ImageUrl: shopAMediaWebP},
			{SkuCode: "GOM-2", Name: "Men trang", PriceVnd: 260000, Stock: 5, ImageUrl: ""},
		},
	})
	if err != nil {
		t.Fatalf("CreateProduct with own shop media + /products/ sample failed: %v", err)
	}
	prodID := created.GetProduct().GetId()
	skuID := created.GetProduct().GetSkus()[0].GetId()

	// 2. IDOR on CreateProduct: seller A attaches shop B's media URL in images[] -> 400 InvalidArgument.
	if _, err := srv.CreateProduct(sellerACtx, &productv1.CreateProductRequest{
		CategoryId: catID,
		Name:       "IDOR Product Images",
		Images:     []string{shopBMediaJPG},
		Skus:       []*productv1.SKUInput{{SkuCode: "IDOR-1", PriceVnd: 100000, Stock: 1}},
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("CreateProduct cross-shop images[]: want InvalidArgument, got %v", err)
	}

	// 3. IDOR on CreateProduct: seller A attaches shop B's media URL in SKU image_url -> 400 InvalidArgument.
	if _, err := srv.CreateProduct(sellerACtx, &productv1.CreateProductRequest{
		CategoryId: catID,
		Name:       "IDOR SKU Image",
		Images:     []string{shopAMediaJPG},
		Skus:       []*productv1.SKUInput{{SkuCode: "IDOR-2", PriceVnd: 100000, Stock: 1, ImageUrl: shopBMediaJPG}},
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("CreateProduct cross-shop sku.image_url: want InvalidArgument, got %v", err)
	}

	// 4. IDOR on UpdateProduct: seller A tries to swap product images[] or SKU image_url to shop B's media -> 400 InvalidArgument.
	if _, err := srv.UpdateProduct(sellerACtx, &productv1.UpdateProductRequest{
		Id:     prodID,
		Images: []string{shopBMediaJPG},
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("UpdateProduct cross-shop images[]: want InvalidArgument, got %v", err)
	}
	if _, err := srv.UpdateProduct(sellerACtx, &productv1.UpdateProductRequest{
		Id: prodID,
		Skus: []*productv1.SKUInput{
			{Id: skuID, SkuCode: "GOM-1", Name: "Men ngoc", PriceVnd: 250000, Stock: 10, ImageUrl: shopBMediaJPG},
		},
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("UpdateProduct cross-shop sku.image_url: want InvalidArgument, got %v", err)
	}

	// 5. Seller B cannot update Seller A's product at all -> PermissionDenied.
	if _, err := srv.UpdateProduct(sellerBCtx, &productv1.UpdateProductRequest{
		Id:     prodID,
		Images: []string{shopBMediaJPG},
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("Seller B UpdateProduct on Seller A product: want PermissionDenied, got %v", err)
	}

	// 6. Admin updating shop A's product: accepts shop A's media, rejects shop B's media.
	if _, err := srv.UpdateProduct(adminCtx, &productv1.UpdateProductRequest{
		Id:     prodID,
		Images: []string{shopAMediaWebP},
	}); err != nil {
		t.Fatalf("Admin UpdateProduct with owning shop A media failed: %v", err)
	}
	if _, err := srv.UpdateProduct(adminCtx, &productv1.UpdateProductRequest{
		Id:     prodID,
		Images: []string{shopBMediaJPG},
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("Admin UpdateProduct with foreign shop B media: want InvalidArgument, got %v", err)
	}

	// 7. Reject external URLs, schemes, SVG, traversal, and malformed media keys.
	badURLs := []string{
		"",
		"https://cdn.example.com/photo.jpg",
		"http://evil.local/photo.jpg",
		"javascript:alert(1)",
		"data:image/png;base64,AAAA",
		"/banners/vietnam-workshop.jpg",
		"/products/",
		"/products/../etc/passwd.png",
		"/products/xss.svg",
		"/products/photo.jpg?foo=1",
		"/media/shops/" + shopA + "/not-a-sha256.jpg",
		"/media/shops/" + shopA + "/" + hash1 + ".svg",
		"/media/shops/" + shopA + "/" + strings.Repeat("A", 64) + ".jpg",
		"/media/shops/" + shopA + "/../" + shopB + "/" + hash1 + ".jpg",
		"/media/other/" + hash1 + ".jpg",
	}
	for _, bad := range badURLs {
		if _, err := srv.CreateProduct(sellerACtx, &productv1.CreateProductRequest{
			CategoryId: catID,
			Name:       "Bad Image URL Test",
			Images:     []string{bad},
			Skus:       []*productv1.SKUInput{{SkuCode: "BAD-1", PriceVnd: 100000, Stock: 1}},
		}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("CreateProduct images=[%q]: want InvalidArgument, got %v", bad, err)
		}
		if bad != "" {
			if _, err := srv.CreateProduct(sellerACtx, &productv1.CreateProductRequest{
				CategoryId: catID,
				Name:       "Bad SKU Image URL Test",
				Skus:       []*productv1.SKUInput{{SkuCode: "BAD-2", PriceVnd: 100000, Stock: 1, ImageUrl: bad}},
			}); status.Code(err) != codes.InvalidArgument {
				t.Fatalf("CreateProduct sku.image_url=%q: want InvalidArgument, got %v", bad, err)
			}
		}
	}

	// 8. CSV import also blocks cross-shop media URLs and external URLs while allowing own-shop media + /products/.
	csvBody := "product_name,sku_code,category_id,price_vnd,stock,images,image_url\n" +
		fmt.Sprintf("SP Hop Le,CSV-OK,%s,150000,5,%s,%s\n", catID, shopAMediaJPG, shopAMediaWebP) +
		fmt.Sprintf("SP IDOR,CSV-IDOR,%s,150000,5,%s,\n", catID, shopBMediaJPG) +
		fmt.Sprintf("SP External,CSV-EXT,%s,150000,5,https://evil.example/a.jpg,\n", catID) +
		fmt.Sprintf("SP Sample,CSV-SMP,%s,150000,5,/products/sample.png,/products/sample.png\n", catID)
	jobResp, err := srv.CreateProductImportJob(sellerACtx, &productv1.CreateProductImportJobRequest{
		Filename:   "media-check.csv",
		CsvContent: []byte(csvBody),
	})
	if err != nil {
		t.Fatalf("CreateProductImportJob: %v", err)
	}
	if _, err := srv.ProcessNextImportJob(context.Background()); err != nil {
		t.Fatalf("ProcessNextImportJob: %v", err)
	}
	gotJob, err := srv.GetProductImportJob(sellerACtx, &productv1.GetProductImportJobRequest{JobId: jobResp.GetJob().GetId()})
	if err != nil {
		t.Fatalf("GetProductImportJob: %v", err)
	}
	if gotJob.GetJob().GetValidRows() != 2 || gotJob.GetJob().GetInvalidRows() != 2 {
		t.Fatalf("CSV import media check: want 2 valid / 2 invalid, got valid=%d invalid=%d",
			gotJob.GetJob().GetValidRows(), gotJob.GetJob().GetInvalidRows())
	}
}
