package server

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
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

func TestProductImageURLModelsAndLegacyUpdate(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)

	const (
		shopA = "11111111-2222-4333-8444-555555555555"
		shopB = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	)
	hash1 := strings.Repeat("c", 64)
	shopAMedia := fmt.Sprintf("/media/shops/%s/%s.jpg", shopA, hash1)
	shopBMedia := fmt.Sprintf("/media/shops/%s/%s.jpg", shopB, hash1)

	adminCtx := ctxWithRole("admin-1", auth.RoleAdmin, "")
	sellerACtx := ctxWithRole("seller-a", auth.RoleSeller, shopA)

	catResp, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name:              "Thoi trang",
		Slug:              "thoi-trang-m3",
		CommissionRateBps: 500,
	})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	catID := catResp.GetCategory().GetId()

	// 1. /models/<file>.<ext> is accepted just like /products/<file>.<ext> on CreateProduct and CSV import.
	if _, err := srv.CreateProduct(sellerACtx, &productv1.CreateProductRequest{
		CategoryId: catID,
		Name:       "Ao thun mau",
		Images:     []string{"/models/x.jpg", "/models/model-ao-thun-oversize.webp"},
		Skus: []*productv1.SKUInput{
			{SkuCode: "MOD-1", Name: "M", PriceVnd: 199000, Stock: 10, ImageUrl: "/models/model-ao-thun-oversize.jpg"},
		},
	}); err != nil {
		t.Fatalf("CreateProduct with /models/ images failed: %v", err)
	}

	// 2. Reject traversal, nested paths, invalid extensions, and query/fragment under /models/ and /products/.
	badStatic := []string{
		"/models/",
		"/models/../x.jpg",
		"/models/a/b.jpg",
		"/models//x.jpg",
		"/models/x.jpg?w=100",
		"/models/x.jpg#hash",
		"/models/x.svg",
		"/models/.jpg",
		`/models\x.jpg`,
		"/products/a/b.jpg",
		"/products/.jpg",
	}
	for _, bad := range badStatic {
		if _, err := srv.CreateProduct(sellerACtx, &productv1.CreateProductRequest{
			CategoryId: catID,
			Name:       "Bad Static Image",
			Images:     []string{bad},
			Skus:       []*productv1.SKUInput{{SkuCode: "BAD-ST-1", PriceVnd: 100000, Stock: 1}},
		}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("CreateProduct images=[%q]: want InvalidArgument, got %v", bad, err)
		}
		if _, err := srv.CreateProduct(sellerACtx, &productv1.CreateProductRequest{
			CategoryId: catID,
			Name:       "Bad Static SKU Image",
			Skus:       []*productv1.SKUInput{{SkuCode: "BAD-ST-2", PriceVnd: 100000, Stock: 1, ImageUrl: bad}},
		}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("CreateProduct sku.image_url=%q: want InvalidArgument, got %v", bad, err)
		}
	}

	// 3. Seed a legacy product directly in store with legacy https:// URLs on product and SKUs.
	legacyProdImg := "https://legacy.nexusmart.local/catalog/old-product.jpg?w=800"
	legacySKUImg := "https://legacy.nexusmart.local/catalog/old-sku.jpg"
	legacyInactiveSKUImg := "https://legacy.nexusmart.local/catalog/old-inactive-sku.jpg"
	legacyProdID := uuid.NewString()
	legacySKUID := uuid.NewString()

	if err := st.WithTx(context.Background(), func(q store.Queries) error {
		if _, err := q.CreateProduct(context.Background(), store.Product{
			ID:          legacyProdID,
			ShopID:      shopA,
			CategoryID:  catID,
			Name:        "San pham cu",
			Slug:        "san-pham-cu-m3",
			Description: "Du lieu seed cu",
			Images:      []string{legacyProdImg, "/models/model-ao-thun-oversize.jpg"},
			Status:      store.ProductStatusApproved,
			MinPriceVND: 200000,
			MaxPriceVND: 200000,
		}); err != nil {
			return err
		}
		if _, err := q.CreateSKU(context.Background(), store.SKU{
			ID:                legacySKUID,
			ProductID:         legacyProdID,
			ShopID:            shopA,
			ProductName:       "San pham cu",
			SKUCode:           "LEG-1",
			Name:              "Mac dinh",
			PriceVND:          200000,
			MinPrice30dVND:    200000,
			Stock:             10,
			ImageURL:          legacySKUImg,
			CategoryID:        catID,
			CommissionRateBps: 500,
			Status:            store.SKUStatusActive,
			ProductStatus:     store.ProductStatusApproved,
		}); err != nil {
			return err
		}
		if _, err := q.CreateSKU(context.Background(), store.SKU{
			ID:                uuid.NewString(),
			ProductID:         legacyProdID,
			ShopID:            shopA,
			ProductName:       "San pham cu",
			SKUCode:           "LEG-INACT",
			Name:              "Tam an",
			PriceVND:          210000,
			MinPrice30dVND:    210000,
			Stock:             0,
			ImageURL:          legacyInactiveSKUImg,
			CategoryID:        catID,
			CommissionRateBps: 500,
			Status:            store.SKUStatusInactive,
			ProductStatus:     store.ProductStatusApproved,
		}); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("seed legacy product: %v", err)
	}

	// 4. UpdateProduct keeping existing legacy product & SKU URLs (and adding valid own media / /models/) -> OK.
	updResp, err := srv.UpdateProduct(sellerACtx, &productv1.UpdateProductRequest{
		Id:     legacyProdID,
		Images: []string{legacyProdImg, "/models/model-ao-thun-oversize.jpg", shopAMedia},
		Skus: []*productv1.SKUInput{
			{Id: legacySKUID, SkuCode: "LEG-1", Name: "Mac dinh", PriceVnd: 220000, Stock: 15, ImageUrl: legacySKUImg},
			{SkuCode: "LEG-INACT", Name: "Kich hoat lai", PriceVnd: 230000, Stock: 3, ImageUrl: legacyInactiveSKUImg},
		},
	})
	if err != nil {
		t.Fatalf("UpdateProduct keeping legacy product & SKU images failed: %v", err)
	}
	if len(updResp.GetProduct().GetImages()) != 3 || updResp.GetProduct().GetImages()[0] != legacyProdImg {
		t.Fatalf("unexpected updated images: %v", updResp.GetProduct().GetImages())
	}

	// 5. UpdateProduct adding a NEW external https:// URL on product images[] or SKU image_url -> 400 InvalidArgument.
	if _, err := srv.UpdateProduct(sellerACtx, &productv1.UpdateProductRequest{
		Id:     legacyProdID,
		Images: []string{legacyProdImg, "https://evil.example/new.jpg"},
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("UpdateProduct adding new external images[]: want InvalidArgument, got %v", err)
	}
	if _, err := srv.UpdateProduct(sellerACtx, &productv1.UpdateProductRequest{
		Id: legacyProdID,
		Skus: []*productv1.SKUInput{
			{Id: legacySKUID, SkuCode: "LEG-1", Name: "Mac dinh", PriceVnd: 220000, Stock: 15, ImageUrl: "https://evil.example/new-sku.jpg"},
		},
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("UpdateProduct setting new external sku.image_url: want InvalidArgument, got %v", err)
	}
	if _, err := srv.UpdateProduct(sellerACtx, &productv1.UpdateProductRequest{
		Id:     legacyProdID,
		Images: []string{legacyProdImg, shopBMedia},
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("UpdateProduct adding cross-shop media to legacy product: want InvalidArgument, got %v", err)
	}
	if _, err := srv.UpdateProduct(sellerACtx, &productv1.UpdateProductRequest{
		Id:     legacyProdID,
		Images: []string{""},
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("UpdateProduct empty product image on legacy product: want InvalidArgument, got %v", err)
	}

	// 6. Legacy URLs are NOT allowed on CreateProduct or CSV import; /models/ IS allowed on CSV import.
	if _, err := srv.CreateProduct(sellerACtx, &productv1.CreateProductRequest{
		CategoryId: catID,
		Name:       "Moi dung anh cu",
		Images:     []string{legacyProdImg},
		Skus:       []*productv1.SKUInput{{SkuCode: "NEW-LEG", PriceVnd: 100000, Stock: 1}},
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("CreateProduct with legacy https URL: want InvalidArgument, got %v", err)
	}

	csvBody := "product_name,sku_code,category_id,price_vnd,stock,images,image_url\n" +
		fmt.Sprintf("SP Model OK,CSV-MOD-OK,%s,150000,5,/models/x.jpg,/models/x.jpg\n", catID) +
		fmt.Sprintf("SP Model Nested,CSV-MOD-BAD,%s,150000,5,/models/a/b.jpg,\n", catID) +
		fmt.Sprintf("SP Legacy,CSV-LEG-BAD,%s,150000,5,%s,\n", catID, legacyProdImg)
	jobResp, err := srv.CreateProductImportJob(sellerACtx, &productv1.CreateProductImportJobRequest{
		Filename:   "models-and-legacy.csv",
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
	if gotJob.GetJob().GetValidRows() != 1 || gotJob.GetJob().GetInvalidRows() != 2 {
		t.Fatalf("CSV import /models/ + legacy check: want 1 valid / 2 invalid, got valid=%d invalid=%d",
			gotJob.GetJob().GetValidRows(), gotJob.GetJob().GetInvalidRows())
	}

	// 7. Control chars and overlong URLs remain rejected even if already stored on the legacy product.
	badCtrlURL := "https://legacy.nexusmart.local/ctrl\x07.jpg"
	overlongURL := "https://legacy.nexusmart.local/" + strings.Repeat("a", maxProductImageURLRunes) + ".jpg"
	badLegacyProdID := uuid.NewString()
	if err := st.WithTx(context.Background(), func(q store.Queries) error {
		_, err := q.CreateProduct(context.Background(), store.Product{
			ID:          badLegacyProdID,
			ShopID:      shopA,
			CategoryID:  catID,
			Name:        "San pham loi ky tu",
			Slug:        "san-pham-loi-ky-tu-m3",
			Images:      []string{badCtrlURL, overlongURL},
			Status:      store.ProductStatusApproved,
			MinPriceVND: 100000,
			MaxPriceVND: 100000,
		})
		return err
	}); err != nil {
		t.Fatalf("seed bad legacy product: %v", err)
	}
	if _, err := srv.UpdateProduct(sellerACtx, &productv1.UpdateProductRequest{
		Id:     badLegacyProdID,
		Images: []string{badCtrlURL},
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("UpdateProduct echoing legacy URL with control char: want InvalidArgument, got %v", err)
	}
	if _, err := srv.UpdateProduct(sellerACtx, &productv1.UpdateProductRequest{
		Id:     badLegacyProdID,
		Images: []string{overlongURL},
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("UpdateProduct echoing overlong legacy URL: want InvalidArgument, got %v", err)
	}

	// 8. Once removed from the product, a legacy URL cannot be re-added on a subsequent UpdateProduct.
	if _, err := srv.UpdateProduct(sellerACtx, &productv1.UpdateProductRequest{
		Id:     legacyProdID,
		Images: []string{"/models/x.jpg"},
	}); err != nil {
		t.Fatalf("UpdateProduct replacing legacy image with /models/x.jpg: %v", err)
	}
	if _, err := srv.UpdateProduct(sellerACtx, &productv1.UpdateProductRequest{
		Id:     legacyProdID,
		Images: []string{legacyProdImg},
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("UpdateProduct re-adding removed legacy URL: want InvalidArgument, got %v", err)
	}
}
