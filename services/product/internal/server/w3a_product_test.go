package server

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/product/internal/store"
)

func TestW3A_D2_GetSKUsBatch_RedactionMatrix(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)

	adminCtx := ctxWithRole("admin-1", "admin", "")
	superAdminCtx := ctxWithRole("super-1", "super_admin", "")
	ownerCtx := ctxWithRole("seller-owner", "seller", "shop-owner")
	otherSellerCtx := ctxWithRole("seller-other", "seller", "shop-other")
	buyerCtx := ctxWithRole("buyer-1", "buyer", "")
	anonCtx := context.Background()

	catResp, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name:              "D2 Matrix Cat",
		Slug:              "d2-matrix-cat",
		CommissionRateBps: 750,
	})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	catID := catResp.GetCategory().GetId()

	createProd := func(name, skuCode string, price int64, stock int32) (*productv1.Product, *productv1.SKU) {
		t.Helper()
		resp, cErr := srv.CreateProduct(ownerCtx, &productv1.CreateProductRequest{
			CategoryId: catID,
			Name:       name,
			Skus: []*productv1.SKUInput{{
				SkuCode:     skuCode,
				Name:        "Variant " + skuCode,
				Attributes:  map[string]string{"color": "black", "size": "M"},
				PriceVnd:    price,
				Stock:       stock,
				WeightGrams: 350,
				ImageUrl:    "https://cdn.local/" + skuCode + ".jpg",
			}},
		})
		if cErr != nil {
			t.Fatalf("CreateProduct %s: %v", name, cErr)
		}
		return resp.GetProduct(), resp.GetProduct().GetSkus()[0]
	}

	// 1. APPROVED product with one ACTIVE SKU and one deactivated (INACTIVE) SKU.
	activeAndInactiveResp, err := srv.CreateProduct(ownerCtx, &productv1.CreateProductRequest{
		CategoryId: catID,
		Name:       "Active And Inactive Prod",
		Skus: []*productv1.SKUInput{
			{
				SkuCode:     "D2-ACT",
				Name:        "Active Variant",
				Attributes:  map[string]string{"color": "blue"},
				PriceVnd:    250000,
				Stock:       12,
				WeightGrams: 300,
				ImageUrl:    "https://cdn.local/d2-act.jpg",
			},
			{
				SkuCode:     "D2-INACT",
				Name:        "Deactivated Variant",
				Attributes:  map[string]string{"color": "red"},
				PriceVnd:    280000,
				Stock:       8,
				WeightGrams: 320,
				ImageUrl:    "https://cdn.local/d2-inact.jpg",
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateProduct active+inactive: %v", err)
	}
	if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: activeAndInactiveResp.GetProduct().GetId(),
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	}); err != nil {
		t.Fatalf("Approve active+inactive: %v", err)
	}
	skuActive := findSKUByCode(t, activeAndInactiveResp.GetProduct().GetSkus(), "D2-ACT")
	skuDeactivated := findSKUByCode(t, activeAndInactiveResp.GetProduct().GetSkus(), "D2-INACT")

	// Reserve 2 units on active SKU so reserved_stock is non-zero in assertions.
	if _, err := srv.ReserveStock(buyerCtx, &productv1.ReserveStockRequest{
		OrderId: "ord-d2-res-act",
		Items:   []*productv1.StockReservationItem{{SkuId: skuActive.GetId(), Quantity: 2}},
	}); err != nil {
		t.Fatalf("ReserveStock active: %v", err)
	}

	// Deactivate D2-INACT by omitting it from UpdateProduct.
	if _, err := srv.UpdateProduct(ownerCtx, &productv1.UpdateProductRequest{
		Id: activeAndInactiveResp.GetProduct().GetId(),
		Skus: []*productv1.SKUInput{{
			Id:          skuActive.GetId(),
			SkuCode:     "D2-ACT",
			Name:        "Active Variant",
			Attributes:  map[string]string{"color": "blue"},
			PriceVnd:    250000,
			Stock:       12,
			WeightGrams: 300,
			ImageUrl:    "https://cdn.local/d2-act.jpg",
		}},
	}); err != nil {
		t.Fatalf("UpdateProduct deactivate D2-INACT: %v", err)
	}

	// 2. PENDING product.
	_, skuPending := createProd("Pending Prod", "D2-PEND", 310000, 7)

	// 3. REJECTED product.
	prodRej, skuRejected := createProd("Rejected Prod", "D2-REJ", 320000, 6)
	if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: prodRej.GetId(),
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_REJECT,
		Reason:    "Missing specs",
	}); err != nil {
		t.Fatalf("Reject prod: %v", err)
	}

	// 4. HIDDEN product.
	prodHid, skuHidden := createProd("Hidden Prod", "D2-HID", 330000, 9)
	if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: prodHid.GetId(),
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	}); err != nil {
		t.Fatalf("Approve hidden prod: %v", err)
	}
	if _, err := srv.UpdateProduct(ownerCtx, &productv1.UpdateProductRequest{
		Id:     prodHid.GetId(),
		Status: productv1.ProductStatus_PRODUCT_STATUS_HIDDEN,
	}); err != nil {
		t.Fatalf("Hide prod: %v", err)
	}

	// 5. Soft-deleted product.
	prodDel, skuDeleted := createProd("Deleted Prod", "D2-DEL", 340000, 5)
	if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: prodDel.GetId(),
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	}); err != nil {
		t.Fatalf("Approve deleted prod: %v", err)
	}
	if _, err := srv.DeleteProduct(ownerCtx, &productv1.DeleteProductRequest{Id: prodDel.GetId()}); err != nil {
		t.Fatalf("DeleteProduct: %v", err)
	}

	// 6. OUT_OF_STOCK public product (approved with 0 stock -> still public & is_active=true).
	prodOOS, skuOOS := createProd("OOS Public Prod", "D2-OOS", 350000, 0)
	if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: prodOOS.GetId(),
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	}); err != nil {
		t.Fatalf("Approve OOS prod: %v", err)
	}

	type skuCase struct {
		name           string
		skuID          string
		productID      string
		productName    string
		skuName        string
		imageURL       string
		wantActive     bool
		wantPrice      int64
		wantMinPrice   int64
		wantStock      int32
		wantReserved   int32
		wantAvail      int32
		wantWeight     int32
		wantSKUCode    string
		wantCommission int32
	}

	skuCases := []skuCase{
		{
			name: "deactivated_sku", skuID: skuDeactivated.GetId(), productID: activeAndInactiveResp.GetProduct().GetId(),
			productName: "Active And Inactive Prod", skuName: "Deactivated Variant", imageURL: "https://cdn.local/d2-inact.jpg",
			wantActive: false, wantPrice: 280000, wantMinPrice: 280000, wantStock: 8, wantReserved: 0, wantAvail: 8,
			wantWeight: 320, wantSKUCode: "D2-INACT", wantCommission: 750,
		},
		{
			name: "pending_product", skuID: skuPending.GetId(), productID: skuPending.GetProductId(),
			productName: "Pending Prod", skuName: "Variant D2-PEND", imageURL: "https://cdn.local/D2-PEND.jpg",
			wantActive: false, wantPrice: 310000, wantMinPrice: 310000, wantStock: 7, wantReserved: 0, wantAvail: 7,
			wantWeight: 350, wantSKUCode: "D2-PEND", wantCommission: 750,
		},
		{
			name: "rejected_product", skuID: skuRejected.GetId(), productID: prodRej.GetId(),
			productName: "Rejected Prod", skuName: "Variant D2-REJ", imageURL: "https://cdn.local/D2-REJ.jpg",
			wantActive: false, wantPrice: 320000, wantMinPrice: 320000, wantStock: 6, wantReserved: 0, wantAvail: 6,
			wantWeight: 350, wantSKUCode: "D2-REJ", wantCommission: 750,
		},
		{
			name: "hidden_product", skuID: skuHidden.GetId(), productID: prodHid.GetId(),
			productName: "Hidden Prod", skuName: "Variant D2-HID", imageURL: "https://cdn.local/D2-HID.jpg",
			wantActive: false, wantPrice: 330000, wantMinPrice: 330000, wantStock: 9, wantReserved: 0, wantAvail: 9,
			wantWeight: 350, wantSKUCode: "D2-HID", wantCommission: 750,
		},
		{
			name: "deleted_product", skuID: skuDeleted.GetId(), productID: prodDel.GetId(),
			productName: "Deleted Prod", skuName: "Variant D2-DEL", imageURL: "https://cdn.local/D2-DEL.jpg",
			wantActive: false, wantPrice: 340000, wantMinPrice: 340000, wantStock: 5, wantReserved: 0, wantAvail: 5,
			wantWeight: 350, wantSKUCode: "D2-DEL", wantCommission: 750,
		},
		{
			name: "active_approved", skuID: skuActive.GetId(), productID: activeAndInactiveResp.GetProduct().GetId(),
			productName: "Active And Inactive Prod", skuName: "Active Variant", imageURL: "https://cdn.local/d2-act.jpg",
			wantActive: true, wantPrice: 250000, wantMinPrice: 250000, wantStock: 12, wantReserved: 2, wantAvail: 10,
			wantWeight: 300, wantSKUCode: "D2-ACT", wantCommission: 750,
		},
		{
			name: "active_out_of_stock", skuID: skuOOS.GetId(), productID: prodOOS.GetId(),
			productName: "OOS Public Prod", skuName: "Variant D2-OOS", imageURL: "https://cdn.local/D2-OOS.jpg",
			wantActive: true, wantPrice: 350000, wantMinPrice: 350000, wantStock: 0, wantReserved: 0, wantAvail: 0,
			wantWeight: 350, wantSKUCode: "D2-OOS", wantCommission: 750,
		},
	}

	allSKUIDs := make([]string, 0, len(skuCases))
	for _, sc := range skuCases {
		allSKUIDs = append(allSKUIDs, sc.skuID)
	}

	callers := []struct {
		name           string
		ctx            context.Context
		canSeeInactive bool
	}{
		{"anonymous", anonCtx, false},
		{"buyer", buyerCtx, false},
		{"other_seller", otherSellerCtx, false},
		{"owner_seller", ownerCtx, true},
		{"admin", adminCtx, true},
		{"super_admin", superAdminCtx, true},
	}

	for _, caller := range callers {
		t.Run(caller.name, func(t *testing.T) {
			resp, bErr := srv.GetSKUsBatch(caller.ctx, &productv1.GetSKUsBatchRequest{SkuIds: allSKUIDs})
			if bErr != nil {
				t.Fatalf("GetSKUsBatch: %v", bErr)
			}
			if len(resp.GetSkus()) != len(skuCases) {
				t.Fatalf("expected all %d SKU rows preserved (never dropped), got %d", len(skuCases), len(resp.GetSkus()))
			}
			byID := make(map[string]*productv1.SKU, len(resp.GetSkus()))
			for _, sk := range resp.GetSkus() {
				byID[sk.GetId()] = sk
			}

			for _, sc := range skuCases {
				t.Run(sc.name, func(t *testing.T) {
					got, ok := byID[sc.skuID]
					if !ok {
						t.Fatalf("SKU %s missing from GetSKUsBatch response", sc.skuID)
					}
					// Kept fields MUST always be present for every caller & state.
					if got.GetId() != sc.skuID ||
						got.GetProductId() != sc.productID ||
						got.GetShopId() != "shop-owner" ||
						got.GetProductName() != sc.productName ||
						got.GetName() != sc.skuName ||
						got.GetImageUrl() != sc.imageURL ||
						got.GetIsActive() != sc.wantActive {
						t.Fatalf("kept fields mismatch: got=%+v", got)
					}

					if sc.wantActive || caller.canSeeInactive {
						// Full unredacted fields (including commission_rate_bps per D1).
						if got.GetSkuCode() != sc.wantSKUCode ||
							len(got.GetAttributes()) == 0 ||
							got.GetPriceVnd() != sc.wantPrice ||
							got.GetMinPrice_30DVnd() != sc.wantMinPrice ||
							got.GetStock() != sc.wantStock ||
							got.GetReservedStock() != sc.wantReserved ||
							got.GetAvailableStock() != sc.wantAvail ||
							got.GetVersion() <= 0 ||
							got.GetWeightGrams() != sc.wantWeight ||
							got.GetCategoryId() != catID ||
							got.GetCommissionRateBps() != sc.wantCommission {
							t.Fatalf("expected full unredacted SKU, got=%+v", got)
						}
					} else {
						// Redacted hidden SKU: ALL 11 non-kept fields MUST be zero/empty.
						if got.GetSkuCode() != "" ||
							len(got.GetAttributes()) != 0 ||
							got.GetPriceVnd() != 0 ||
							got.GetStock() != 0 ||
							got.GetReservedStock() != 0 ||
							got.GetAvailableStock() != 0 ||
							got.GetVersion() != 0 ||
							got.GetWeightGrams() != 0 ||
							got.GetCategoryId() != "" ||
							got.GetCommissionRateBps() != 0 ||
							got.GetMinPrice_30DVnd() != 0 {
							t.Fatalf("hidden SKU leaked sensitive fields to %s: got=%+v", caller.name, got)
						}
					}
				})
			}
		})
	}

	// End-to-end gRPC interceptor verification (bufconn): spoofed metadata cannot bypass redaction,
	// while signed owner/admin JWTs see full hidden SKU and signed buyer/other-seller JWTs see redacted SKU.
	t.Run("GRPCInterceptor_RedactionEnforcement", func(t *testing.T) {
		users, _ := auth.NewManager("w3a-d2-user-key", 15*time.Minute)
		svcMgr, _ := auth.NewManager("w3a-d2-svc-key", auth.ServiceTokenTTL)

		lis := bufconn.Listen(1 << 20)
		gs := grpc.NewServer(grpc.ChainUnaryInterceptor(auth.GRPCUnaryServerInterceptor(users, svcMgr)))
		productv1.RegisterProductServiceServer(gs, srv)
		go func() { _ = gs.Serve(lis) }()
		t.Cleanup(gs.Stop)

		conn, dErr := grpc.NewClient("passthrough:///bufnet",
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
			grpc.WithTransportCredentials(insecure.NewCredentials()))
		if dErr != nil {
			t.Fatalf("dial bufconn: %v", dErr)
		}
		t.Cleanup(func() { _ = conn.Close() })
		client := productv1.NewProductServiceClient(conn)

		callCtx := func(tok string, extraMD ...string) context.Context {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			t.Cleanup(cancel)
			pairs := append([]string{}, extraMD...)
			if tok != "" {
				pairs = append(pairs, "authorization", "Bearer "+tok)
			}
			return metadata.NewOutgoingContext(ctx, metadata.Pairs(pairs...))
		}

		// Forged plain metadata pretending to be admin/owner -> still redacted!
		forgedResp, fErr := client.GetSKUsBatch(
			callCtx("", "x-user-role", "admin", "x-shop-id", "shop-owner"),
			&productv1.GetSKUsBatchRequest{SkuIds: []string{skuPending.GetId(), skuActive.GetId()}},
		)
		if fErr != nil || len(forgedResp.GetSkus()) != 2 {
			t.Fatalf("forged metadata GetSKUsBatch: resp=%+v err=%v", forgedResp, fErr)
		}
		for _, sk := range forgedResp.GetSkus() {
			if sk.GetId() == skuPending.GetId() && sk.GetPriceVnd() != 0 {
				t.Fatalf("forged metadata bypassed D2 redaction: %+v", sk)
			}
			if sk.GetId() == skuActive.GetId() && sk.GetCommissionRateBps() != 750 {
				t.Fatalf("active SKU commission_rate_bps altered: %+v", sk)
			}
		}

		// Signed owner JWT sees full pending SKU; signed other-seller JWT sees redacted pending SKU.
		ownerTok, _, _, _ := users.SignAccessToken("seller-owner", "seller", "shop-owner")
		otherTok, _, _, _ := users.SignAccessToken("seller-other", "seller", "shop-other")
		ownerResp, oErr := client.GetSKUsBatch(callCtx(ownerTok), &productv1.GetSKUsBatchRequest{SkuIds: []string{skuPending.GetId()}})
		if oErr != nil || len(ownerResp.GetSkus()) != 1 || ownerResp.GetSkus()[0].GetPriceVnd() != 310000 {
			t.Fatalf("signed owner JWT should see unredacted pending SKU, got resp=%+v err=%v", ownerResp, oErr)
		}
		otherResp, otErr := client.GetSKUsBatch(callCtx(otherTok), &productv1.GetSKUsBatchRequest{SkuIds: []string{skuPending.GetId()}})
		if otErr != nil || len(otherResp.GetSkus()) != 1 || otherResp.GetSkus()[0].GetPriceVnd() != 0 {
			t.Fatalf("signed other seller JWT must see redacted pending SKU, got resp=%+v err=%v", otherResp, otErr)
		}
	})
}

func TestW3A_D8_ListProducts_BySKUIDs(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)

	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-d8")

	catResp, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name:              "D8 Cat",
		Slug:              "d8-cat",
		CommissionRateBps: 500,
	})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	catID := catResp.GetCategory().GetId()

	// Create 3 approved products (p1 has 2 active SKUs + 1 deactivated SKU; p2 approved; p3 OUT_OF_STOCK)
	// plus non-public products (PENDING, REJECTED, HIDDEN, soft-deleted).
	p1Resp, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: catID,
		Name:       "Alpha Tee",
		Skus: []*productv1.SKUInput{
			{SkuCode: "D8-P1-A", Name: "M", PriceVnd: 100000, Stock: 10},
			{SkuCode: "D8-P1-B", Name: "L", PriceVnd: 110000, Stock: 10},
			{SkuCode: "D8-P1-OFF", Name: "XL", PriceVnd: 120000, Stock: 10},
		},
	})
	if err != nil {
		t.Fatalf("CreateProduct p1: %v", err)
	}
	if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: p1Resp.GetProduct().GetId(),
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	}); err != nil {
		t.Fatalf("Approve p1: %v", err)
	}
	p1SkuA := findSKUByCode(t, p1Resp.GetProduct().GetSkus(), "D8-P1-A")
	p1SkuB := findSKUByCode(t, p1Resp.GetProduct().GetSkus(), "D8-P1-B")
	p1SkuOff := findSKUByCode(t, p1Resp.GetProduct().GetSkus(), "D8-P1-OFF")
	// Deactivate D8-P1-OFF.
	if _, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id: p1Resp.GetProduct().GetId(),
		Skus: []*productv1.SKUInput{
			{Id: p1SkuA.GetId(), SkuCode: "D8-P1-A", Name: "M", PriceVnd: 100000, Stock: 10},
			{Id: p1SkuB.GetId(), SkuCode: "D8-P1-B", Name: "L", PriceVnd: 110000, Stock: 10},
		},
	}); err != nil {
		t.Fatalf("deactivate D8-P1-OFF: %v", err)
	}

	p2Resp, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: catID,
		Name:       "Beta Jacket",
		Skus:       []*productv1.SKUInput{{SkuCode: "D8-P2", Name: "M", PriceVnd: 300000, Stock: 5}},
	})
	if err != nil {
		t.Fatalf("CreateProduct p2: %v", err)
	}
	if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: p2Resp.GetProduct().GetId(),
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	}); err != nil {
		t.Fatalf("Approve p2: %v", err)
	}
	p2Sku := p2Resp.GetProduct().GetSkus()[0]

	p3Resp, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: catID,
		Name:       "Gamma OOS Hat",
		Skus:       []*productv1.SKUInput{{SkuCode: "D8-P3-OOS", Name: "Std", PriceVnd: 200000, Stock: 0}},
	})
	if err != nil {
		t.Fatalf("CreateProduct p3: %v", err)
	}
	if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: p3Resp.GetProduct().GetId(),
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	}); err != nil {
		t.Fatalf("Approve p3: %v", err)
	}
	p3Sku := p3Resp.GetProduct().GetSkus()[0]

	// Non-public products: PENDING, REJECTED, HIDDEN, soft-deleted.
	pPending, _ := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: catID, Name: "Pending D8",
		Skus: []*productv1.SKUInput{{SkuCode: "D8-PEND", Name: "M", PriceVnd: 150000, Stock: 5}},
	})
	pRejected, _ := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: catID, Name: "Rejected D8",
		Skus: []*productv1.SKUInput{{SkuCode: "D8-REJ", Name: "M", PriceVnd: 150000, Stock: 5}},
	})
	_, _ = srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: pRejected.GetProduct().GetId(),
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_REJECT,
		Reason:    "bad",
	})
	pHidden, _ := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: catID, Name: "Hidden D8",
		Skus: []*productv1.SKUInput{{SkuCode: "D8-HID", Name: "M", PriceVnd: 150000, Stock: 5}},
	})
	_, _ = srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: pHidden.GetProduct().GetId(),
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	})
	_, _ = srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
		Id: pHidden.GetProduct().GetId(), Status: productv1.ProductStatus_PRODUCT_STATUS_HIDDEN,
	})
	pDeleted, _ := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: catID, Name: "Deleted D8",
		Skus: []*productv1.SKUInput{{SkuCode: "D8-DEL", Name: "M", PriceVnd: 150000, Stock: 5}},
	})
	_, _ = srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: pDeleted.GetProduct().GetId(),
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	})
	_, _ = srv.DeleteProduct(sellerCtx, &productv1.DeleteProductRequest{Id: pDeleted.GetProduct().GetId()})

	// 1. Validation: > 200 sku_ids -> InvalidArgument (even if duplicates).
	tooMany := make([]string, 201)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("sku-%03d", i)
	}
	if _, err := srv.ListProducts(context.Background(), &productv1.ListProductsRequest{SkuIds: tooMany}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("201 unique sku_ids: want InvalidArgument, got %v", err)
	}
	tooManyDups := make([]string, 201)
	for i := range tooManyDups {
		tooManyDups[i] = p1SkuA.GetId()
	}
	if _, err := srv.ListProducts(context.Background(), &productv1.ListProductsRequest{SkuIds: tooManyDups}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("201 duplicate sku_ids: want InvalidArgument, got %v", err)
	}

	// Blank/whitespace element -> InvalidArgument.
	for _, bad := range [][]string{{""}, {"   "}, {p1SkuA.GetId(), ""}} {
		if _, err := srv.ListProducts(context.Background(), &productv1.ListProductsRequest{SkuIds: bad}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("blank sku_ids %v: want InvalidArgument, got %v", bad, err)
		}
	}

	// Exactly 200 sku_ids succeeds.
	exact200 := make([]string, 200)
	exact200[0] = p1SkuA.GetId()
	for i := 1; i < 200; i++ {
		exact200[i] = fmt.Sprintf("missing-sku-%03d", i)
	}
	resp200, err := srv.ListProducts(context.Background(), &productv1.ListProductsRequest{SkuIds: exact200})
	if err != nil || resp200.GetTotal() != 1 || len(resp200.GetProducts()) != 1 || resp200.GetProducts()[0].GetId() != p1Resp.GetProduct().GetId() {
		t.Fatalf("exact 200 sku_ids: got total=%d len=%d err=%v", resp200.GetTotal(), len(resp200.GetProducts()), err)
	}

	// 2. Public-only enforcement (even for admin & owning seller!): non-public products and deactivated SKUs excluded.
	mixedIDs := []string{
		p1SkuA.GetId(),
		p1SkuB.GetId(), // same product as p1SkuA -> must deduplicate to 1 product!
		p2Sku.GetId(),
		p3Sku.GetId(), // OUT_OF_STOCK public product -> MUST be included!
		pPending.GetProduct().GetSkus()[0].GetId(),
		pRejected.GetProduct().GetSkus()[0].GetId(),
		pHidden.GetProduct().GetSkus()[0].GetId(),
		pDeleted.GetProduct().GetSkus()[0].GetId(),
	}
	for label, callerCtx := range map[string]context.Context{
		"anonymous": context.Background(),
		"admin":     adminCtx,
		"seller":    sellerCtx,
	} {
		resp, lErr := srv.ListProducts(callerCtx, &productv1.ListProductsRequest{
			ShopId: "shop-d8",
			SkuIds: mixedIDs,
			SortBy: "price_asc",
		})
		if lErr != nil {
			t.Fatalf("%s ListProducts(sku_ids): %v", label, lErr)
		}
		if resp.GetTotal() != 3 || len(resp.GetProducts()) != 3 {
			t.Fatalf("%s ListProducts(sku_ids): want 3 unique public products (p1, p3, p2), got total=%d len=%d",
				label, resp.GetTotal(), len(resp.GetProducts()))
		}
		if resp.GetProducts()[0].GetId() != p1Resp.GetProduct().GetId() ||
			resp.GetProducts()[1].GetId() != p3Resp.GetProduct().GetId() ||
			resp.GetProducts()[2].GetId() != p2Resp.GetProduct().GetId() {
			t.Fatalf("%s unexpected product order under price_asc: %v", label, resp.GetProducts())
		}
	}

	// Deactivated SKU alone must NOT match its parent product.
	offResp, err := srv.ListProducts(context.Background(), &productv1.ListProductsRequest{SkuIds: []string{p1SkuOff.GetId()}})
	if err != nil || offResp.GetTotal() != 0 || len(offResp.GetProducts()) != 0 {
		t.Fatalf("deactivated sku_id must not match product: got total=%d len=%d err=%v", offResp.GetTotal(), len(offResp.GetProducts()), err)
	}

	// Unknown sku_id returns empty list, NOT all products.
	unkResp, err := srv.ListProducts(context.Background(), &productv1.ListProductsRequest{SkuIds: []string{"no-such-sku"}})
	if err != nil || unkResp.GetTotal() != 0 || len(unkResp.GetProducts()) != 0 {
		t.Fatalf("unknown sku_id must return empty: got total=%d len=%d err=%v", unkResp.GetTotal(), len(unkResp.GetProducts()), err)
	}

	// 3. Pagination combined with sku_ids + sort_by.
	page1, err := srv.ListProducts(context.Background(), &productv1.ListProductsRequest{
		SkuIds:   []string{p1SkuA.GetId(), p2Sku.GetId(), p3Sku.GetId()},
		SortBy:   "price_asc",
		Page:     1,
		PageSize: 2,
	})
	if err != nil || page1.GetTotal() != 3 || len(page1.GetProducts()) != 2 ||
		page1.GetProducts()[0].GetId() != p1Resp.GetProduct().GetId() ||
		page1.GetProducts()[1].GetId() != p3Resp.GetProduct().GetId() {
		t.Fatalf("sku_ids page 1: want total=3 [p1,p3], got total=%d products=%+v err=%v", page1.GetTotal(), page1.GetProducts(), err)
	}
	page2, err := srv.ListProducts(context.Background(), &productv1.ListProductsRequest{
		SkuIds:   []string{p1SkuA.GetId(), p2Sku.GetId(), p3Sku.GetId()},
		SortBy:   "price_asc",
		Page:     2,
		PageSize: 2,
	})
	if err != nil || page2.GetTotal() != 3 || len(page2.GetProducts()) != 1 ||
		page2.GetProducts()[0].GetId() != p2Resp.GetProduct().GetId() {
		t.Fatalf("sku_ids page 2: want total=3 [p2], got total=%d products=%+v err=%v", page2.GetTotal(), page2.GetProducts(), err)
	}
}
