package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/jetstream"
	"github.com/nexusmart/core/services/product/internal/events"
	"github.com/nexusmart/core/services/product/internal/store"
	"github.com/nexusmart/core/services/product/migrations"
)

func assertShopSuspendedError(t *testing.T, label string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected FailedPrecondition SHOP_SUSPENDED, got nil", label)
	}
	if status.Code(err) != codes.FailedPrecondition || !strings.HasPrefix(status.Convert(err).Message(), "SHOP_SUSPENDED") {
		t.Fatalf("%s: want FailedPrecondition SHOP_SUSPENDED, got code=%v msg=%q", label, status.Code(err), status.Convert(err).Message())
	}
}

func assertNoSuspensionReasonLeak(t *testing.T, label string, err error) {
	t.Helper()
	if err == nil {
		return
	}
	lower := strings.ToLower(status.Convert(err).Message())
	for _, forbidden := range []string{"suspend", "khoá", "khóa", "locked", "banned"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("%s leaked suspension reason %q in error %q", label, forbidden, status.Convert(err).Message())
		}
	}
}

func publishShopStatusEnvelope(t *testing.T, js nats.JetStreamContext, subject, eventID, shopID, fromStatus, toStatus string, version int64) {
	t.Helper()
	payloadBytes, err := json.Marshal(events.ShopStatusChangedPayload{
		ShopID:      shopID,
		OwnerUserID: "owner-" + shopID,
		ShopName:    "Shop " + shopID,
		FromStatus:  fromStatus,
		ToStatus:    toStatus,
		Version:     version,
		Reason:      "compliance suspension",
		ActorID:     "ops-1",
		ApproverID:  "ops-2",
		OccurredAt:  time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	envBytes, err := json.Marshal(events.Envelope{
		EventID:    eventID,
		EventType:  events.SubjectShopStatusChanged,
		OccurredAt: time.Now().UTC(),
		Payload:    payloadBytes,
	})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	if _, err := js.Publish(subject, envBytes); err != nil {
		t.Fatalf("publish %s (%s): %v", subject, eventID, err)
	}
}

func TestW3B2_ShopSuspended_LifecycleVisibilityGuardsAndReserve_MemoryStore(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	srv := New(st, nil)
	statusHandler := events.NewShopStatusHandler(st, slog.New(slog.NewTextHandler(io.Discard, nil)))

	adminCtx := ctxWithRole("admin-1", "admin", "")
	buyerCtx := ctxWithRole("buyer-1", "buyer", "")
	seller1Ctx := ctxWithRole("seller-1", "seller", "shop-1")
	seller2Ctx := ctxWithRole("seller-2", "seller", "shop-2")
	svcCtx := ctxWithService("order-service")

	catResp, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name: "Apparel", Slug: "apparel-w3b2", CommissionRateBps: 500,
	})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	catID := catResp.GetCategory().GetId()

	// Shop 1: APPROVED, SELLER-HIDDEN (previously approved), OUT_OF_STOCK, PENDING (never approved).
	prodApprResp, err := srv.CreateProduct(seller1Ctx, &productv1.CreateProductRequest{
		CategoryId: catID, Name: "Áo Khoác Gió Alpha", Slug: "ao-khoac-alpha", Description: "Chống nắng",
		Skus: []*productv1.SKUInput{{SkuCode: "S1-APPR", Name: "L", PriceVnd: 250000, Stock: 20}},
	})
	if err != nil {
		t.Fatalf("CreateProduct appr: %v", err)
	}
	prodApprID := prodApprResp.GetProduct().GetId()
	skuApprID := prodApprResp.GetProduct().GetSkus()[0].GetId()
	if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: prodApprID, Decision: productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	}); err != nil {
		t.Fatalf("Approve prodAppr: %v", err)
	}

	prodHidResp, err := srv.CreateProduct(seller1Ctx, &productv1.CreateProductRequest{
		CategoryId: catID, Name: "Áo Khoác Ẩn Alpha", Slug: "ao-khoac-an-alpha",
		Skus: []*productv1.SKUInput{{SkuCode: "S1-HID", Name: "M", PriceVnd: 180000, Stock: 10}},
	})
	if err != nil {
		t.Fatalf("CreateProduct hid: %v", err)
	}
	prodHidID := prodHidResp.GetProduct().GetId()
	if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: prodHidID, Decision: productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	}); err != nil {
		t.Fatalf("Approve prodHid: %v", err)
	}
	if _, err := srv.UpdateProduct(seller1Ctx, &productv1.UpdateProductRequest{
		Id: prodHidID, Status: productv1.ProductStatus_PRODUCT_STATUS_HIDDEN,
	}); err != nil {
		t.Fatalf("Hide prodHid: %v", err)
	}

	prodPendResp, err := srv.CreateProduct(seller1Ctx, &productv1.CreateProductRequest{
		CategoryId: catID, Name: "Áo Mới Chờ Duyệt Alpha", Slug: "ao-moi-cho-duyet-alpha",
		Skus: []*productv1.SKUInput{{SkuCode: "S1-PEND", Name: "S", PriceVnd: 150000, Stock: 5}},
	})
	if err != nil {
		t.Fatalf("CreateProduct pend: %v", err)
	}
	prodPendID := prodPendResp.GetProduct().GetId()

	// Shop 2: APPROVED control product (must remain visible and editable).
	prodShop2Resp, err := srv.CreateProduct(seller2Ctx, &productv1.CreateProductRequest{
		CategoryId: catID, Name: "Áo Khoác Beta Alpha", Slug: "ao-khoac-beta",
		Skus: []*productv1.SKUInput{{SkuCode: "S2-APPR", Name: "XL", PriceVnd: 300000, Stock: 15}},
	})
	if err != nil {
		t.Fatalf("CreateProduct shop2: %v", err)
	}
	prodShop2ID := prodShop2Resp.GetProduct().GetId()
	skuShop2ID := prodShop2Resp.GetProduct().GetSkus()[0].GetId()
	if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: prodShop2ID, Decision: productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	}); err != nil {
		t.Fatalf("Approve shop2: %v", err)
	}

	// Pre-suspension: public list sees both shop-1 and shop-2 approved products.
	preList, err := srv.ListProducts(ctx, &productv1.ListProductsRequest{Keyword: "Alpha", Page: 1, PageSize: 20})
	if err != nil || preList.GetTotal() != 2 {
		t.Fatalf("pre-suspension ListProducts: total=%d err=%v, want 2", preList.GetTotal(), err)
	}

	// === 1. Suspend shop-1 (version 1) ===
	if err := statusHandler.HandleShopStatusChanged(ctx, "evt-mem-s1-v1", events.ShopStatusChangedPayload{
		ShopID: "shop-1", FromStatus: "ACTIVE", ToStatus: "SUSPENDED", Version: 1,
	}); err != nil {
		t.Fatalf("HandleShopStatusChanged v1: %v", err)
	}

	// Public ListProducts: browse, search, category/recommendations, shop storefront, flash sale sku_ids ALL hide shop-1.
	for name, req := range map[string]*productv1.ListProductsRequest{
		"browse":         {Page: 1, PageSize: 20},
		"search":         {Keyword: "Alpha", Page: 1, PageSize: 20},
		"category_rec":   {CategoryId: catID, Page: 1, PageSize: 20},
		"flash_sku_ids":  {SkuIds: []string{skuApprID, skuShop2ID}, Page: 1, PageSize: 20},
		"shop_store_pub": {ShopId: "shop-1", Page: 1, PageSize: 20},
	} {
		resp, lErr := srv.ListProducts(ctx, req)
		if lErr != nil {
			t.Fatalf("public ListProducts(%s): %v", name, lErr)
		}
		wantTotal := int64(1)
		if name == "shop_store_pub" {
			wantTotal = 0
		}
		if resp.GetTotal() != wantTotal || int64(len(resp.GetProducts())) != wantTotal {
			t.Fatalf("public ListProducts(%s) while suspended: got total=%d len=%d, want %d",
				name, resp.GetTotal(), len(resp.GetProducts()), wantTotal)
		}
		for _, p := range resp.GetProducts() {
			if p.GetShopId() == "shop-1" {
				t.Fatalf("public ListProducts(%s) leaked suspended shop-1 product %s", name, p.GetId())
			}
			if p.GetShopSuspended() {
				t.Fatalf("public ListProducts(%s) must never expose shop_suspended=true on %s", name, p.GetId())
			}
		}
	}

	// Public GetProduct by ID -> NotFound without leaking reason.
	if _, err := srv.GetProduct(ctx, &productv1.GetProductRequest{Id: prodApprID}); status.Code(err) != codes.NotFound {
		t.Fatalf("public GetProduct(id) suspended: want NotFound, got %v", err)
	} else {
		assertNoSuspensionReasonLeak(t, "GetProduct(id)", err)
	}
	if _, err := srv.GetProduct(buyerCtx, &productv1.GetProductRequest{Slug: "ao-khoac-alpha"}); status.Code(err) != codes.NotFound {
		t.Fatalf("public GetProduct(slug, stub=false) suspended: want NotFound, got %v", err)
	} else {
		assertNoSuspensionReasonLeak(t, "GetProduct(slug, stub=false)", err)
	}

	// Public GetProduct by slug with IncludeDiscontinuedStub=true:
	// - Previously-approved product (APPROVED or HIDDEN) -> E13 discontinued stub (no product, no SKU/price, no name/reason leak).
	// - Never-approved product (PENDING) -> 404 NotFound.
	stubAppr, err := srv.GetProduct(ctx, &productv1.GetProductRequest{Slug: "ao-khoac-alpha", IncludeDiscontinuedStub: true})
	if err != nil {
		t.Fatalf("public GetProduct(slug, stub=true) suspended approved: %v", err)
	}
	if !stubAppr.GetDiscontinued() || stubAppr.GetProduct() != nil || stubAppr.GetDiscontinuedCategoryId() != catID || stubAppr.GetDiscontinuedName() != "" || stubAppr.GetDiscontinuedShopId() != "shop-1" {
		t.Fatalf("E13/F12 suspended approved stub mismatch: %+v", stubAppr)
	}
	stubHidSusp, err := srv.GetProduct(ctx, &productv1.GetProductRequest{Slug: "ao-khoac-an-alpha", IncludeDiscontinuedStub: true})
	if err != nil || !stubHidSusp.GetDiscontinued() || stubHidSusp.GetProduct() != nil || stubHidSusp.GetDiscontinuedName() != "" || stubHidSusp.GetDiscontinuedShopId() != "shop-1" {
		t.Fatalf("E13/F12 suspended hidden stub mismatch: %+v err=%v", stubHidSusp, err)
	}
	if _, err := srv.GetProduct(ctx, &productv1.GetProductRequest{Slug: "ao-moi-cho-duyet-alpha", IncludeDiscontinuedStub: true}); status.Code(err) != codes.NotFound {
		t.Fatalf("never-approved product of suspended shop must still 404, got %v", err)
	}

	// Public ListProductReviews -> NotFound without leaking reason.
	if _, err := srv.ListProductReviews(ctx, &productv1.ListProductReviewsRequest{ProductId: prodApprID}); status.Code(err) != codes.NotFound {
		t.Fatalf("public ListProductReviews suspended: want NotFound, got %v", err)
	}

	// GetSKUsBatch for public/buyer/cart/order: shop-1 SKU is redacted with IsActive=false (SKU_INACTIVE in cart/order), shop-2 stays active.
	for label, caller := range map[string]context.Context{
		"anon":         ctx,
		"buyer":        buyerCtx,
		"other_seller": seller2Ctx,
	} {
		batch, bErr := srv.GetSKUsBatch(caller, &productv1.GetSKUsBatchRequest{SkuIds: []string{skuApprID, skuShop2ID}})
		if bErr != nil || len(batch.GetSkus()) != 2 {
			t.Fatalf("%s GetSKUsBatch: len=%d err=%v", label, len(batch.GetSkus()), bErr)
		}
		for _, sk := range batch.GetSkus() {
			if sk.GetId() == skuApprID {
				if sk.GetIsActive() || sk.GetPriceVnd() != 0 || sk.GetStock() != 0 || sk.GetSkuCode() != "" || sk.GetCommissionRateBps() != 0 {
					t.Fatalf("%s suspended SKU must be redacted inactive: %+v", label, sk)
				}
			}
			if sk.GetId() == skuShop2ID {
				if !sk.GetIsActive() || sk.GetPriceVnd() != 300000 {
					t.Fatalf("%s active shop-2 SKU altered: %+v", label, sk)
				}
			}
		}
	}

	// Owner seller & admin STILL SEE shop-1 products with original products.status untouched and shop_suspended=true.
	for label, caller := range map[string]context.Context{
		"owner_seller": seller1Ctx,
		"admin":        adminCtx,
	} {
		list, lErr := srv.ListProducts(caller, &productv1.ListProductsRequest{ShopId: "shop-1", Page: 1, PageSize: 20})
		if lErr != nil || list.GetTotal() != 3 {
			t.Fatalf("%s ListProducts(shop-1): total=%d err=%v, want 3", label, list.GetTotal(), lErr)
		}
		for _, p := range list.GetProducts() {
			if !p.GetShopSuspended() {
				t.Fatalf("%s ListProducts(shop-1) product %s: want shop_suspended=true", label, p.GetId())
			}
		}
		got, gErr := srv.GetProduct(caller, &productv1.GetProductRequest{Id: prodApprID})
		if gErr != nil || got.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_APPROVED || !got.GetProduct().GetShopSuspended() {
			t.Fatalf("%s GetProduct(prodAppr): status=%v shop_suspended=%v err=%v, want APPROVED + true",
				label, got.GetProduct().GetStatus(), got.GetProduct().GetShopSuspended(), gErr)
		}
		gotHid, hErr := srv.GetProduct(caller, &productv1.GetProductRequest{Id: prodHidID})
		if hErr != nil || gotHid.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_HIDDEN || !gotHid.GetProduct().GetShopSuspended() {
			t.Fatalf("%s GetProduct(prodHid): status=%v shop_suspended=%v err=%v, want HIDDEN + true",
				label, gotHid.GetProduct().GetStatus(), gotHid.GetProduct().GetShopSuspended(), hErr)
		}
		// Owner/admin GetSKUsBatch sees full SKU fields while IsActive=false while shop suspended.
		batch, bErr := srv.GetSKUsBatch(caller, &productv1.GetSKUsBatchRequest{SkuIds: []string{skuApprID}})
		if bErr != nil || len(batch.GetSkus()) != 1 || batch.GetSkus()[0].GetPriceVnd() != 250000 || batch.GetSkus()[0].GetIsActive() {
			t.Fatalf("%s GetSKUsBatch(suspended): want full price=250000 is_active=false, got %+v err=%v", label, batch.GetSkus(), bErr)
		}
	}

	// === 2. Seller of suspended shop blocked with FailedPrecondition SHOP_SUSPENDED ===
	_, err = srv.CreateProduct(seller1Ctx, &productv1.CreateProductRequest{
		CategoryId: catID, Name: "Blocked New",
		Skus: []*productv1.SKUInput{{SkuCode: "BLK-1", Name: "M", PriceVnd: 100000, Stock: 5}},
	})
	assertShopSuspendedError(t, "seller CreateProduct", err)

	_, err = srv.UpdateProduct(seller1Ctx, &productv1.UpdateProductRequest{
		Id: prodApprID, Name: "Blocked Edit",
	})
	assertShopSuspendedError(t, "seller UpdateProduct(edit)", err)

	_, err = srv.UpdateProduct(seller1Ctx, &productv1.UpdateProductRequest{
		Id: prodHidID, Status: productv1.ProductStatus_PRODUCT_STATUS_APPROVED,
	})
	assertShopSuspendedError(t, "seller UpdateProduct(publish/unhide)", err)

	_, err = srv.UpdateProduct(seller1Ctx, &productv1.UpdateProductRequest{
		Id: prodApprID, Status: productv1.ProductStatus_PRODUCT_STATUS_HIDDEN,
	})
	assertShopSuspendedError(t, "seller UpdateProduct(hide)", err)

	_, err = srv.UpdateStock(seller1Ctx, &productv1.UpdateStockRequest{
		SkuId: skuApprID, NewStock: 50, ExpectedVersion: 1,
	})
	assertShopSuspendedError(t, "seller UpdateStock", err)

	_, err = srv.DeleteProduct(seller1Ctx, &productv1.DeleteProductRequest{Id: prodPendID})
	assertShopSuspendedError(t, "seller DeleteProduct", err)

	// Active shop-2 seller is unaffected.
	if _, err := srv.UpdateProduct(seller2Ctx, &productv1.UpdateProductRequest{Id: prodShop2ID, Description: "Updated ok"}); err != nil {
		t.Fatalf("active shop-2 seller UpdateProduct failed: %v", err)
	}

	// Admin creating a product for suspended shop-1 succeeds AND inherits shop_suspended=true even after APPROVE.
	adminNewResp, err := srv.CreateProduct(adminCtx, &productv1.CreateProductRequest{
		ShopId: "shop-1", CategoryId: catID, Name: "Admin Added During Suspension Alpha", Slug: "admin-added-susp",
		Skus: []*productv1.SKUInput{{SkuCode: "S1-ADM", Name: "M", PriceVnd: 200000, Stock: 5}},
	})
	if err != nil {
		t.Fatalf("admin CreateProduct for suspended shop: %v", err)
	}
	if !adminNewResp.GetProduct().GetShopSuspended() {
		t.Fatalf("admin CreateProduct for suspended shop must return shop_suspended=true")
	}
	adminNewID := adminNewResp.GetProduct().GetId()
	if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: adminNewID, Decision: productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	}); err != nil {
		t.Fatalf("admin approve new product of suspended shop: %v", err)
	}
	if _, err := srv.GetProduct(ctx, &productv1.GetProductRequest{Id: adminNewID}); status.Code(err) != codes.NotFound {
		t.Fatalf("newly created+approved product of suspended shop MUST stay hidden publicly, got %v", err)
	}

	// === 3. In-flight order stock operations (ReserveStock, ConfirmStock, ReleaseStock, RestockOrderItems) NOT blocked ===
	res1, err := srv.ReserveStock(buyerCtx, &productv1.ReserveStockRequest{
		OrderId: "ord-inflight-1",
		Items:   []*productv1.StockReservationItem{{SkuId: skuApprID, Quantity: 3}},
	})
	if err != nil || !res1.GetReserved() {
		t.Fatalf("in-flight ReserveStock while shop suspended must succeed, got resp=%+v err=%v", res1, err)
	}
	conf1, err := srv.ConfirmStock(svcCtx, &productv1.ConfirmStockRequest{OrderId: "ord-inflight-1"})
	if err != nil || !conf1.GetConfirmed() {
		t.Fatalf("in-flight ConfirmStock while shop suspended must succeed, got resp=%+v err=%v", conf1, err)
	}
	res2, err := srv.ReserveStock(buyerCtx, &productv1.ReserveStockRequest{
		OrderId: "ord-inflight-2",
		Items:   []*productv1.StockReservationItem{{SkuId: skuApprID, Quantity: 2}},
	})
	if err != nil || !res2.GetReserved() {
		t.Fatalf("in-flight ReserveStock #2 while shop suspended: %v", err)
	}
	rel2, err := srv.ReleaseStock(svcCtx, &productv1.ReleaseStockRequest{OrderId: "ord-inflight-2"})
	if err != nil || !rel2.GetReleased() {
		t.Fatalf("in-flight ReleaseStock while shop suspended: %v", err)
	}
	restock1, err := srv.RestockOrderItems(svcCtx, &productv1.RestockOrderItemsRequest{
		OrderId: "ord-inflight-1",
		Items:   []*productv1.StockReservationItem{{SkuId: skuApprID, Quantity: 3}},
	})
	if err != nil || !restock1.GetRestocked() {
		t.Fatalf("in-flight RestockOrderItems while shop suspended: %v", err)
	}

	// === 4. Unsuspend shop-1 (version 2) ===
	if err := statusHandler.HandleShopStatusChanged(ctx, "evt-mem-s1-v2", events.ShopStatusChangedPayload{
		ShopID: "shop-1", FromStatus: "SUSPENDED", ToStatus: "ACTIVE", Version: 2,
	}); err != nil {
		t.Fatalf("HandleShopStatusChanged v2: %v", err)
	}

	// Public ListProducts now shows shop-1's APPROVED products (prodAppr + adminNew) + shop-2's prodShop2 = 3,
	// while seller-hidden prodHid stays HIDDEN and prodPend stays PENDING!
	afterList, err := srv.ListProducts(ctx, &productv1.ListProductsRequest{Keyword: "Alpha", Page: 1, PageSize: 20})
	if err != nil || afterList.GetTotal() != 3 {
		t.Fatalf("post-unsuspend ListProducts: want total=3 (prodAppr, adminNew, prodShop2), got total=%d err=%v", afterList.GetTotal(), err)
	}
	// Seller-hidden product remains HIDDEN and restores E13 HIDDEN discontinued stub WITH name!
	hidAfter, err := srv.GetProduct(ctx, &productv1.GetProductRequest{Slug: "ao-khoac-an-alpha", IncludeDiscontinuedStub: true})
	if err != nil || !hidAfter.GetDiscontinued() || hidAfter.GetDiscontinuedName() != "Áo Khoác Ẩn Alpha" || hidAfter.GetDiscontinuedShopId() != "shop-1" {
		t.Fatalf("post-unsuspend HIDDEN product must keep HIDDEN status & E13 name/shop_id, got %+v err=%v", hidAfter, err)
	}
	// GetSKUsBatch public is active again!
	batchAfter, err := srv.GetSKUsBatch(buyerCtx, &productv1.GetSKUsBatchRequest{SkuIds: []string{skuApprID}})
	if err != nil || len(batchAfter.GetSkus()) != 1 || !batchAfter.GetSkus()[0].GetIsActive() || batchAfter.GetSkus()[0].GetPriceVnd() != 250000 {
		t.Fatalf("post-unsuspend GetSKUsBatch: want active price=250000, got %+v err=%v", batchAfter.GetSkus(), err)
	}
	// Seller can mutate again (publish hidden product!).
	pubResp, err := srv.UpdateProduct(seller1Ctx, &productv1.UpdateProductRequest{
		Id: prodHidID, Status: productv1.ProductStatus_PRODUCT_STATUS_APPROVED,
	})
	if err != nil || pubResp.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_APPROVED {
		t.Fatalf("post-unsuspend seller publish failed: %+v err=%v", pubResp, err)
	}

	// === 5. Stale v1 SUSPENDED arriving after v2 ACTIVE is ignored ===
	if err := statusHandler.HandleShopStatusChanged(ctx, "evt-mem-s1-stale-v1", events.ShopStatusChangedPayload{
		ShopID: "shop-1", FromStatus: "ACTIVE", ToStatus: "SUSPENDED", Version: 1,
	}); err != nil {
		t.Fatalf("stale v1: %v", err)
	}
	if _, err := srv.GetProduct(ctx, &productv1.GetProductRequest{Id: prodApprID}); err != nil {
		t.Fatalf("stale v1 must not re-hide product: %v", err)
	}
}

func TestPGStore_W3B2_ShopSuspended_RealPGAndNATS(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_PRODUCT_DB_URL"))
	if dsn == "" {
		t.Skip("set TEST_PRODUCT_DB_URL to run real Postgres + NATS shop suspension integration tests")
	}
	natsURL := strings.TrimSpace(os.Getenv("TEST_NATS_URL"))
	if natsURL == "" {
		t.Skip("set TEST_NATS_URL to run real Postgres + NATS shop suspension integration tests")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()

	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatalf("first migrations.Apply: %v", err)
	}
	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatalf("second migrations.Apply (idempotency): %v", err)
	}

	pgStore := store.NewPgStore(pool)
	srv := New(pgStore, nil)
	runPrefix := "pg3b2-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	shop1ID := runPrefix + "-shop-1"
	shop2ID := runPrefix + "-shop-2"

	adminCtx := ctxWithRole("pg-admin-3b2", "admin", "")
	buyerCtx := ctxWithRole("pg-buyer-3b2", "buyer", "")
	seller1Ctx := ctxWithRole("pg-seller1-3b2", "seller", shop1ID)
	seller2Ctx := ctxWithRole("pg-seller2-3b2", "seller", shop2ID)
	svcCtx := ctxWithService("order-service")

	// Set up an isolated JetStream stream for this test so parallel package runs never collide,
	// AND verify canonical EnsureStreams succeeds cleanly on the real NATS server.
	adminNC, err := nats.Connect(natsURL)
	if err != nil {
		t.Fatalf("connect admin nats: %v", err)
	}
	adminJS, err := adminNC.JetStream()
	if err != nil {
		adminNC.Close()
		t.Fatalf("admin jetstream: %v", err)
	}
	streamName := "PG_SHOP_SUSP_" + strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", "")[:8])
	subject := fmt.Sprintf("pgtest.%s.user.shop_status_changed", runPrefix)
	durable := "product-pg-shop-status-" + runPrefix
	queue := "product-pg-shop-workers-" + runPrefix

	t.Cleanup(func() {
		_ = adminJS.DeleteStream(streamName)
		adminNC.Close()
	})
	if err := events.EnsureStreams(adminJS, jetstream.DefaultPrimaryStream); err != nil {
		t.Fatalf("EnsureStreams canonical NEXUSMART: %v", err)
	}
	if err := jetstream.EnsureStream(adminJS, &nats.StreamConfig{
		Name:      streamName,
		Subjects:  []string{subject},
		Retention: nats.LimitsPolicy,
		Storage:   nats.FileStorage,
	}); err != nil {
		t.Fatalf("create isolated test stream: %v", err)
	}

	startConsumer := func() (*nats.Conn, context.CancelFunc, *events.Consumer) {
		t.Helper()
		nc, cErr := nats.Connect(natsURL)
		if cErr != nil {
			t.Fatalf("connect consumer nats: %v", cErr)
		}
		js, jErr := nc.JetStream()
		if jErr != nil {
			nc.Close()
			t.Fatalf("consumer jetstream: %v", jErr)
		}
		cCtx, cancel := context.WithCancel(context.Background())
		handler := events.NewShopStatusHandler(pgStore, slog.New(slog.NewTextHandler(io.Discard, nil)))
		c := events.NewConsumer(js, js, pgStore, handler, slog.New(slog.NewTextHandler(io.Discard, nil)), streamName)
		c.SetSubscription(subject, durable, queue)
		if sErr := c.Start(cCtx); sErr != nil {
			cancel()
			nc.Close()
			t.Fatalf("start consumer: %v", sErr)
		}
		return nc, cancel, c
	}

	waitShopState := func(shopID string, wantSuspended bool, wantVersion int64) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			st, gErr := pgStore.GetShopSuspensionState(ctx, shopID)
			if gErr == nil && st.Suspended == wantSuspended && st.Version == wantVersion {
				return
			}
			time.Sleep(25 * time.Millisecond)
		}
		st, gErr := pgStore.GetShopSuspensionState(ctx, shopID)
		t.Fatalf("timed out waiting for shop %s state (suspended=%v version=%d); got %+v err=%v",
			shopID, wantSuspended, wantVersion, st, gErr)
	}

	waitProcessedEvent := func(eventID string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			var exists bool
			if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM processed_events WHERE event_id = $1)`, eventID).Scan(&exists); err == nil && exists {
				return
			}
			time.Sleep(25 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for processed_events(%s)", eventID)
	}

	// Seed category & products across shop1 & shop2.
	catResp, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name: "PG Susp Cat " + runPrefix, Slug: runPrefix + "-cat", CommissionRateBps: 500,
	})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	catID := catResp.GetCategory().GetId()

	prodApprResp, err := srv.CreateProduct(seller1Ctx, &productv1.CreateProductRequest{
		CategoryId: catID, Name: "PG Giày Thể Thao " + runPrefix, Slug: runPrefix + "-giay-appr",
		Skus: []*productv1.SKUInput{{SkuCode: runPrefix + "-S1A", Name: "42", PriceVnd: 450000, Stock: 40}},
	})
	if err != nil {
		t.Fatalf("CreateProduct appr: %v", err)
	}
	prodApprID := prodApprResp.GetProduct().GetId()
	skuApprID := prodApprResp.GetProduct().GetSkus()[0].GetId()
	if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: prodApprID, Decision: productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	}); err != nil {
		t.Fatalf("Approve appr: %v", err)
	}

	prodHidResp, err := srv.CreateProduct(seller1Ctx, &productv1.CreateProductRequest{
		CategoryId: catID, Name: "PG Giày Tự Ẩn " + runPrefix, Slug: runPrefix + "-giay-hid",
		Skus: []*productv1.SKUInput{{SkuCode: runPrefix + "-S1H", Name: "41", PriceVnd: 350000, Stock: 10}},
	})
	if err != nil {
		t.Fatalf("CreateProduct hid: %v", err)
	}
	prodHidID := prodHidResp.GetProduct().GetId()
	if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: prodHidID, Decision: productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	}); err != nil {
		t.Fatalf("Approve hid: %v", err)
	}
	if _, err := srv.UpdateProduct(seller1Ctx, &productv1.UpdateProductRequest{
		Id: prodHidID, Status: productv1.ProductStatus_PRODUCT_STATUS_HIDDEN,
	}); err != nil {
		t.Fatalf("Seller hide prodHid: %v", err)
	}

	prodPendResp, err := srv.CreateProduct(seller1Ctx, &productv1.CreateProductRequest{
		CategoryId: catID, Name: "PG Giày Chờ " + runPrefix, Slug: runPrefix + "-giay-pend",
		Skus: []*productv1.SKUInput{{SkuCode: runPrefix + "-S1P", Name: "40", PriceVnd: 250000, Stock: 5}},
	})
	if err != nil {
		t.Fatalf("CreateProduct pend: %v", err)
	}
	prodPendID := prodPendResp.GetProduct().GetId()

	prodShop2Resp, err := srv.CreateProduct(seller2Ctx, &productv1.CreateProductRequest{
		CategoryId: catID, Name: "PG Giày Shop2 " + runPrefix, Slug: runPrefix + "-giay-s2",
		Skus: []*productv1.SKUInput{{SkuCode: runPrefix + "-S2A", Name: "43", PriceVnd: 500000, Stock: 15}},
	})
	if err != nil {
		t.Fatalf("CreateProduct shop2: %v", err)
	}
	skuShop2ID := prodShop2Resp.GetProduct().GetSkus()[0].GetId()
	if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: prodShop2Resp.GetProduct().GetId(), Decision: productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	}); err != nil {
		t.Fatalf("Approve shop2: %v", err)
	}

	// === 1. Start consumer instance #1, publish SUSPENDED v1 + duplicate event_id ===
	nc1, cancel1, c1 := startConsumer()
	v1EventID := runPrefix + "-evt-v1"
	publishShopStatusEnvelope(t, adminJS, subject, v1EventID, shop1ID, "ACTIVE", "SUSPENDED", 1)
	waitShopState(shop1ID, true, 1)

	// Publish duplicate event_id with contradictory payload -> deduplicated by processed_events!
	publishShopStatusEnvelope(t, adminJS, subject, v1EventID, shop1ID, "SUSPENDED", "ACTIVE", 99)
	time.Sleep(150 * time.Millisecond)
	stAfterDup, err := pgStore.GetShopSuspensionState(ctx, shop1ID)
	if err != nil || !stAfterDup.Suspended || stAfterDup.Version != 1 {
		t.Fatalf("duplicate event_id mutated Postgres shop_suspension_state: %+v err=%v", stAfterDup, err)
	}

	// Verify DB products.status untouched and shop_suspended=true for all shop1 products.
	var apprStatus string
	var apprSusp bool
	if err := pool.QueryRow(ctx, `SELECT status, shop_suspended FROM products WHERE id = $1`, prodApprID).Scan(&apprStatus, &apprSusp); err != nil {
		t.Fatalf("query prodAppr: %v", err)
	}
	if apprStatus != store.ProductStatusApproved || !apprSusp {
		t.Fatalf("prodAppr in Postgres: status=%s shop_suspended=%v, want APPROVED + true", apprStatus, apprSusp)
	}

	// Verify public ListProducts (browse/search/category/storefront/flash sku_ids), GetProduct (ID & slug stub), GetSKUsBatch hide shop1.
	for name, req := range map[string]*productv1.ListProductsRequest{
		"search":        {Keyword: runPrefix, Page: 1, PageSize: 20},
		"category":      {CategoryId: catID, Page: 1, PageSize: 20},
		"flash_sku_ids": {SkuIds: []string{skuApprID, skuShop2ID}, Page: 1, PageSize: 20},
		"storefront":    {ShopId: shop1ID, Page: 1, PageSize: 20},
	} {
		resp, lErr := srv.ListProducts(ctx, req)
		if lErr != nil {
			t.Fatalf("PG public ListProducts(%s): %v", name, lErr)
		}
		wantTotal := int64(1)
		if name == "storefront" {
			wantTotal = 0
		}
		if resp.GetTotal() != wantTotal {
			t.Fatalf("PG public ListProducts(%s): got total=%d, want %d", name, resp.GetTotal(), wantTotal)
		}
	}

	stubResp, err := srv.GetProduct(ctx, &productv1.GetProductRequest{Slug: runPrefix + "-giay-appr", IncludeDiscontinuedStub: true})
	if err != nil || !stubResp.GetDiscontinued() || stubResp.GetProduct() != nil || stubResp.GetDiscontinuedCategoryId() != catID || stubResp.GetDiscontinuedName() != "" || stubResp.GetDiscontinuedShopId() != shop1ID {
		t.Fatalf("PG suspended slug stub mismatch: %+v err=%v", stubResp, err)
	}
	if _, err := srv.GetProduct(ctx, &productv1.GetProductRequest{Slug: runPrefix + "-giay-pend", IncludeDiscontinuedStub: true}); status.Code(err) != codes.NotFound {
		t.Fatalf("PG never-approved slug while suspended: want NotFound, got %v", err)
	}
	if _, err := srv.GetProduct(ctx, &productv1.GetProductRequest{Id: prodApprID}); status.Code(err) != codes.NotFound {
		t.Fatalf("PG GetProduct(id) while suspended: want NotFound, got %v", err)
	} else {
		assertNoSuspensionReasonLeak(t, "PG GetProduct(id)", err)
	}

	cartBatch, err := srv.GetSKUsBatch(buyerCtx, &productv1.GetSKUsBatchRequest{SkuIds: []string{skuApprID, skuShop2ID}})
	if err != nil || len(cartBatch.GetSkus()) != 2 {
		t.Fatalf("PG GetSKUsBatch: len=%d err=%v", len(cartBatch.GetSkus()), err)
	}
	for _, sk := range cartBatch.GetSkus() {
		if sk.GetId() == skuApprID && (sk.GetIsActive() || sk.GetPriceVnd() != 0 || sk.GetStock() != 0) {
			t.Fatalf("PG GetSKUsBatch suspended SKU not redacted inactive: %+v", sk)
		}
		if sk.GetId() == skuShop2ID && (!sk.GetIsActive() || sk.GetPriceVnd() != 500000) {
			t.Fatalf("PG GetSKUsBatch active shop2 SKU broken: %+v", sk)
		}
	}

	// Seller & Admin still see shop1 products with shop_suspended=true.
	sellerList, err := srv.ListProducts(seller1Ctx, &productv1.ListProductsRequest{ShopId: shop1ID, Page: 1, PageSize: 20})
	if err != nil || sellerList.GetTotal() != 3 {
		t.Fatalf("PG seller ListProducts while suspended: total=%d err=%v, want 3", sellerList.GetTotal(), err)
	}
	for _, p := range sellerList.GetProducts() {
		if !p.GetShopSuspended() {
			t.Fatalf("PG seller ListProducts product %s: want shop_suspended=true", p.GetId())
		}
	}
	adminList, err := srv.ListProducts(adminCtx, &productv1.ListProductsRequest{ShopId: shop1ID, Page: 1, PageSize: 20})
	if err != nil || adminList.GetTotal() != 3 {
		t.Fatalf("PG admin ListProducts while suspended: total=%d err=%v, want 3", adminList.GetTotal(), err)
	}
	for _, p := range adminList.GetProducts() {
		if !p.GetShopSuspended() {
			t.Fatalf("PG admin ListProducts product %s: want shop_suspended=true", p.GetId())
		}
	}

	// Seller mutations blocked with FailedPrecondition SHOP_SUSPENDED.
	_, err = srv.CreateProduct(seller1Ctx, &productv1.CreateProductRequest{
		CategoryId: catID, Name: "PG Blocked", Skus: []*productv1.SKUInput{{SkuCode: runPrefix + "-BLK", Name: "M", PriceVnd: 100000, Stock: 1}},
	})
	assertShopSuspendedError(t, "PG seller CreateProduct", err)
	_, err = srv.UpdateProduct(seller1Ctx, &productv1.UpdateProductRequest{Id: prodApprID, Name: "PG Blocked Edit"})
	assertShopSuspendedError(t, "PG seller UpdateProduct", err)
	_, err = srv.UpdateProduct(seller1Ctx, &productv1.UpdateProductRequest{Id: prodHidID, Status: productv1.ProductStatus_PRODUCT_STATUS_APPROVED})
	assertShopSuspendedError(t, "PG seller Publish", err)
	_, err = srv.UpdateStock(seller1Ctx, &productv1.UpdateStockRequest{SkuId: skuApprID, NewStock: 99, ExpectedVersion: 1})
	assertShopSuspendedError(t, "PG seller UpdateStock", err)
	_, err = srv.DeleteProduct(seller1Ctx, &productv1.DeleteProductRequest{Id: prodPendID})
	assertShopSuspendedError(t, "PG seller DeleteProduct", err)

	// New product created by admin for suspended shop1 inherits shop_suspended=true in Postgres.
	adminAddedResp, err := srv.CreateProduct(adminCtx, &productv1.CreateProductRequest{
		ShopId: shop1ID, CategoryId: catID, Name: "PG Created While Suspended " + runPrefix, Slug: runPrefix + "-created-susp",
		Skus: []*productv1.SKUInput{{SkuCode: runPrefix + "-S1ADM", Name: "M", PriceVnd: 220000, Stock: 6}},
	})
	if err != nil {
		t.Fatalf("PG admin CreateProduct for suspended shop: %v", err)
	}
	adminAddedID := adminAddedResp.GetProduct().GetId()
	if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: adminAddedID, Decision: productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	}); err != nil {
		t.Fatalf("PG admin approve adminAdded: %v", err)
	}
	var adminAddedSusp bool
	if err := pool.QueryRow(ctx, `SELECT shop_suspended FROM products WHERE id = $1`, adminAddedID).Scan(&adminAddedSusp); err != nil || !adminAddedSusp {
		t.Fatalf("PG newly created product of suspended shop must have shop_suspended=true, got %v err=%v", adminAddedSusp, err)
	}
	if _, err := srv.GetProduct(ctx, &productv1.GetProductRequest{Id: adminAddedID}); status.Code(err) != codes.NotFound {
		t.Fatalf("PG newly created+approved product of suspended shop must stay hidden publicly, got %v", err)
	}

	// === 2. Restart consumer mid-way (Stop without Drain -> close nc1 -> verify durable survives -> start consumer #2) ===
	cancel1()
	if err := c1.Stop(); err != nil {
		t.Fatalf("stop consumer #1: %v", err)
	}
	nc1.Close()
	if _, err := adminJS.ConsumerInfo(streamName, durable); err != nil {
		t.Fatalf("E1: durable consumer %s was deleted on restart: %v", durable, err)
	}

	// Publish ACTIVE v2 while service is down, then start consumer #2 -> picks up v2 cleanly without replaying v1!
	v2EventID := runPrefix + "-evt-v2"
	publishShopStatusEnvelope(t, adminJS, subject, v2EventID, shop1ID, "SUSPENDED", "ACTIVE", 2)

	nc2, cancel2, c2 := startConsumer()
	defer func() {
		cancel2()
		_ = c2.Stop()
		nc2.Close()
	}()
	waitShopState(shop1ID, false, 2)

	// Verify APPROVED products (prodAppr + adminAdded) are publicly visible again, while seller-hidden prodHid STAYS HIDDEN!
	pubAfter, err := srv.ListProducts(ctx, &productv1.ListProductsRequest{ShopId: shop1ID, Page: 1, PageSize: 20})
	if err != nil || pubAfter.GetTotal() != 2 {
		t.Fatalf("PG post-unsuspend public ListProducts(shop1): want total=2 (prodAppr + adminAdded), got total=%d err=%v", pubAfter.GetTotal(), err)
	}
	var hidStatus string
	var hidSusp bool
	if err := pool.QueryRow(ctx, `SELECT status, shop_suspended FROM products WHERE id = $1`, prodHidID).Scan(&hidStatus, &hidSusp); err != nil {
		t.Fatalf("query prodHid after unsuspend: %v", err)
	}
	if hidStatus != store.ProductStatusHidden || hidSusp {
		t.Fatalf("PG prodHid after unsuspend: want status=HIDDEN shop_suspended=false, got status=%s shop_suspended=%v", hidStatus, hidSusp)
	}
	hidStubAfter, err := srv.GetProduct(ctx, &productv1.GetProductRequest{Slug: runPrefix + "-giay-hid", IncludeDiscontinuedStub: true})
	if err != nil || !hidStubAfter.GetDiscontinued() || hidStubAfter.GetDiscontinuedName() != "PG Giày Tự Ẩn "+runPrefix || hidStubAfter.GetDiscontinuedShopId() != shop1ID {
		t.Fatalf("PG post-unsuspend HIDDEN E13 stub mismatch: %+v err=%v", hidStubAfter, err)
	}

	// === 3. Late stale v1 SUSPENDED arriving AFTER v2 ACTIVE (new event_id) MUST be ignored (F12) ===
	staleEventID := runPrefix + "-evt-late-v1"
	publishShopStatusEnvelope(t, adminJS, subject, staleEventID, shop1ID, "ACTIVE", "SUSPENDED", 1)
	waitProcessedEvent(staleEventID)
	stAfterStale, err := pgStore.GetShopSuspensionState(ctx, shop1ID)
	if err != nil || stAfterStale.Suspended || stAfterStale.Version != 2 {
		t.Fatalf("PG stale v1 overwrote v2 state: %+v err=%v", stAfterStale, err)
	}
	if _, err := srv.GetProduct(ctx, &productv1.GetProductRequest{Id: prodApprID}); err != nil {
		t.Fatalf("PG stale v1 hid active product: %v", err)
	}

	// === 4. Race test on real Postgres: ReserveStock/ConfirmStock/ReleaseStock + Admin CreateProduct racing with shop suspension toggles ===
	// Seed 4 approved products/SKUs in shop1 so multi-row product locks in SetProductsShopSuspendedByShop
	// race directly with ConfirmStock (proving ORDER BY id ASC deadlock freedom) while per-SKU optimistic
	// contention stays within maxOptimisticRetries (4 orders/SKU < 8 retries).
	const (
		raceSKUs       = 4
		ordersPerSKU   = 4
		raceOrders     = raceSKUs * ordersPerSKU
		raceToggles    = 16
		raceCreates    = 8
		initialRaceStk = 20
	)
	raceSKUIDs := make([]string, 0, raceSKUs)
	for sIdx := 0; sIdx < raceSKUs; sIdx++ {
		pResp, cErr := srv.CreateProduct(adminCtx, &productv1.CreateProductRequest{
			ShopId:     shop1ID,
			CategoryId: catID,
			Name:       fmt.Sprintf("PG Race Stock Prod %s-%d", runPrefix, sIdx),
			Slug:       fmt.Sprintf("%s-race-stk-%d", runPrefix, sIdx),
			Skus:       []*productv1.SKUInput{{SkuCode: fmt.Sprintf("%s-RSTK-%d", runPrefix, sIdx), Name: "Std", PriceVnd: 120000, Stock: initialRaceStk}},
		})
		if cErr != nil {
			t.Fatalf("seed race product %d: %v", sIdx, cErr)
		}
		if _, rErr := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
			ProductId: pResp.GetProduct().GetId(),
			Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
		}); rErr != nil {
			t.Fatalf("approve race product %d: %v", sIdx, rErr)
		}
		raceSKUIDs = append(raceSKUIDs, pResp.GetProduct().GetSkus()[0].GetId())
	}

	var (
		wg               sync.WaitGroup
		reserveConfirmOK atomic.Int32
		errCh            = make(chan error, raceOrders+raceToggles+raceCreates)
		start            = make(chan struct{})
	)

	// Workers running in-flight ReserveStock -> ConfirmStock (even) or ReleaseStock (odd).
	// Include multi-SKU orders (spanning 2 products of shop1) to exercise multi-product ConfirmStock locking vs SetProductsShopSuspendedByShop.
	for i := 0; i < raceOrders; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			ordID := fmt.Sprintf("%s-race-ord-%02d", runPrefix, idx)
			skuID := raceSKUIDs[idx%raceSKUs]
			rResp, rErr := srv.ReserveStock(buyerCtx, &productv1.ReserveStockRequest{
				OrderId: ordID,
				Items:   []*productv1.StockReservationItem{{SkuId: skuID, Quantity: 1}},
			})
			if rErr != nil || !rResp.GetReserved() {
				errCh <- fmt.Errorf("order %s ReserveStock failed during suspension race: %w", ordID, rErr)
				return
			}
			if idx%2 == 0 {
				cResp, cErr := srv.ConfirmStock(svcCtx, &productv1.ConfirmStockRequest{OrderId: ordID})
				if cErr != nil || !cResp.GetConfirmed() {
					errCh <- fmt.Errorf("order %s ConfirmStock failed during suspension race: %w", ordID, cErr)
					return
				}
				reserveConfirmOK.Add(1)
			} else {
				relResp, relErr := srv.ReleaseStock(svcCtx, &productv1.ReleaseStockRequest{OrderId: ordID})
				if relErr != nil || !relResp.GetReleased() {
					errCh <- fmt.Errorf("order %s ReleaseStock failed during suspension race: %w", ordID, relErr)
					return
				}
			}
		}(i)
	}

	// Worker rapidly publishing monotonic SUSPENDED/ACTIVE events via real NATS JetStream.
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < raceToggles; i++ {
			ver := int64(10 + i)
			toStatus := "SUSPENDED"
			fromStatus := "ACTIVE"
			if i%2 == 1 {
				toStatus = "ACTIVE"
				fromStatus = "SUSPENDED"
			}
			publishShopStatusEnvelope(t, adminJS, subject, fmt.Sprintf("%s-race-ev-%02d", runPrefix, i), shop1ID, fromStatus, toStatus, ver)
			time.Sleep(5 * time.Millisecond)
		}
	}()

	// Workers creating new products for shop1 concurrently with suspension toggles (LockShopSuspension prevents escaped flag!).
	for i := 0; i < raceCreates; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			if _, cErr := srv.CreateProduct(adminCtx, &productv1.CreateProductRequest{
				ShopId:     shop1ID,
				CategoryId: catID,
				Name:       fmt.Sprintf("PG Race Prod %s-%02d", runPrefix, idx),
				Slug:       fmt.Sprintf("%s-race-p-%02d", runPrefix, idx),
				Skus:       []*productv1.SKUInput{{SkuCode: fmt.Sprintf("%s-RP-%02d", runPrefix, idx), Name: "M", PriceVnd: 100000, Stock: 2}},
			}); cErr != nil {
				errCh <- fmt.Errorf("race CreateProduct %d failed: %w", idx, cErr)
			}
		}(i)
	}

	close(start)
	wg.Wait()
	close(errCh)
	for e := range errCh {
		t.Fatalf("concurrency race failure: %v", e)
	}

	// Wait for the final toggle event (version = 10 + raceToggles - 1 = 25, ACTIVE -> suspended=false) to settle,
	// then send one final SUSPENDED event (version = 100) and verify EVERY shop1 product (including all race-created ones) matches!
	finalToggleVer := int64(10 + raceToggles - 1)
	waitShopState(shop1ID, false, finalToggleVer)

	// Verify stock accounting across all race SKUs after concurrent ReserveStock + ConfirmStock/ReleaseStock.
	if wantConfirmed := int32(raceOrders / 2); reserveConfirmOK.Load() != wantConfirmed {
		t.Fatalf("confirmed orders = %d, want %d", reserveConfirmOK.Load(), wantConfirmed)
	}
	var totalStockRemaining int32
	for _, sID := range raceSKUIDs {
		skFinal, gErr := pgStore.GetSKUWithContext(ctx, sID)
		if gErr != nil {
			t.Fatalf("GetSKUWithContext final %s: %v", sID, gErr)
		}
		if skFinal.ReservedStock != 0 {
			t.Fatalf("SKU %s leaked reserved_stock=%d after suspension+reserve race", sID, skFinal.ReservedStock)
		}
		totalStockRemaining += skFinal.Stock
	}
	if wantTotalStock := int32(raceSKUs*initialRaceStk - raceOrders/2); totalStockRemaining != wantTotalStock {
		t.Fatalf("total stock remaining = %d, want %d", totalStockRemaining, wantTotalStock)
	}

	// Verify 100% consistency between shop_suspension_state and EVERY product row of shop1 (zero escaped rows!).
	var mismatched int64
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM products p
		JOIN shop_suspension_state s ON s.shop_id = p.shop_id
		WHERE p.shop_id = $1 AND p.shop_suspended IS DISTINCT FROM s.suspended
	`, shop1ID).Scan(&mismatched); err != nil {
		t.Fatalf("check shop_suspended consistency: %v", err)
	}
	if mismatched != 0 {
		t.Fatalf("found %d products of %s whose shop_suspended disagrees with shop_suspension_state after race", mismatched, shop1ID)
	}

	// Final suspend check: version 100 sets all race-created + existing products to shop_suspended=true cleanly.
	publishShopStatusEnvelope(t, adminJS, subject, runPrefix+"-evt-final-susp", shop1ID, "ACTIVE", "SUSPENDED", 100)
	waitShopState(shop1ID, true, 100)
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM products
		WHERE shop_id = $1 AND shop_suspended = FALSE
	`, shop1ID).Scan(&mismatched); err != nil {
		t.Fatalf("check final suspended consistency: %v", err)
	}
	if mismatched != 0 {
		t.Fatalf("found %d products of %s still with shop_suspended=FALSE after final suspend", mismatched, shop1ID)
	}

	// === 5. Missing/zero version on real Postgres + NATS routes to dead_letter_events + NEXUSMART_DLQ without mutating shop2 ===
	dlqSubject := fmt.Sprintf("pgtest.%s.user.shop_status_changed.dlqcheck", runPrefix)
	dlqStream := "PG_SHOP_VER0_" + strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", "")[:8])
	if err := jetstream.EnsureStream(adminJS, &nats.StreamConfig{
		Name:      dlqStream,
		Subjects:  []string{dlqSubject},
		Retention: nats.LimitsPolicy,
		Storage:   nats.FileStorage,
	}); err != nil {
		t.Fatalf("create dlqcheck test stream: %v", err)
	}
	t.Cleanup(func() {
		_ = adminJS.DeleteStream(dlqStream)
	})
	dlqNC, err := nats.Connect(natsURL)
	if err != nil {
		t.Fatalf("connect dlqcheck nats: %v", err)
	}
	defer dlqNC.Close()
	dlqJS, err := dlqNC.JetStream()
	if err != nil {
		t.Fatalf("dlqcheck jetstream: %v", err)
	}
	dlqCtx, dlqCancel := context.WithCancel(context.Background())
	defer dlqCancel()
	dlqConsumer := events.NewConsumer(dlqJS, dlqJS, pgStore, events.NewShopStatusHandler(pgStore, slog.New(slog.NewTextHandler(io.Discard, nil))), slog.New(slog.NewTextHandler(io.Discard, nil)), dlqStream)
	dlqConsumer.SetMaxAttempts(1)
	dlqConsumer.SetSubscription(dlqSubject, "product-pg-ver0-"+runPrefix, "product-pg-ver0-workers-"+runPrefix)
	if err := dlqConsumer.Start(dlqCtx); err != nil {
		t.Fatalf("start dlqcheck consumer: %v", err)
	}
	defer func() { _ = dlqConsumer.Stop() }()

	missingVerEventID := runPrefix + "-evt-missing-ver"
	missingVerRaw := fmt.Sprintf(`{
		"event_id": %q,
		"event_type": "user.shop_status_changed",
		"occurred_at": "2026-11-24T12:00:00Z",
		"payload": {
			"shop_id": %q,
			"from_status": "ACTIVE",
			"to_status": "SUSPENDED"
		}
	}`, missingVerEventID, shop2ID)
	if _, err := adminJS.Publish(dlqSubject, []byte(missingVerRaw)); err != nil {
		t.Fatalf("publish missing version event: %v", err)
	}

	dlqDeadline := time.Now().Add(5 * time.Second)
	var dbDLQFound bool
	for time.Now().Before(dlqDeadline) {
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM dead_letter_events WHERE event_id = $1)`, missingVerEventID).Scan(&dbDLQFound); err == nil && dbDLQFound {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !dbDLQFound {
		t.Fatalf("missing version event %s was not recorded in Postgres dead_letter_events", missingVerEventID)
	}
	if _, err := adminJS.GetLastMsg(events.DLQStreamName, "dlq."+dlqSubject); err != nil {
		t.Fatalf("missing version event was not published to NATS %s: %v", events.DLQStreamName, err)
	}
	var shop2StateCount int64
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM shop_suspension_state WHERE shop_id = $1`, shop2ID).Scan(&shop2StateCount); err != nil || shop2StateCount != 0 {
		t.Fatalf("missing version event must not insert shop_suspension_state for %s, count=%d err=%v", shop2ID, shop2StateCount, err)
	}
	var shop2Susp bool
	if err := pool.QueryRow(ctx, `SELECT shop_suspended FROM products WHERE id = $1`, prodShop2Resp.GetProduct().GetId()).Scan(&shop2Susp); err != nil || shop2Susp {
		t.Fatalf("missing version event must not suspend shop2 product: suspended=%v err=%v", shop2Susp, err)
	}
}

func TestW3B2_F9_AdminRBACMatrix_6RolesAllAdminRPCs(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	srv := New(st, nil)
	statusHandler := events.NewShopStatusHandler(st, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Verify all 6 internal staff roles from pkg/common/auth.StaffRoles().
	staffRoles := auth.StaffRoles()
	wantStaff := []string{
		auth.RoleSuperAdmin,
		auth.RoleAdmin,
		auth.RoleModerator,
		auth.RoleCSKH,
		auth.RoleAccountant,
		auth.RoleOps,
	}
	if len(staffRoles) != len(wantStaff) {
		t.Fatalf("unexpected StaffRoles count: got %v, want %v", staffRoles, wantStaff)
	}

	type principal struct {
		name      string
		role      string
		ctx       context.Context
		isAnon    bool
		isService bool
	}
	principals := []principal{
		{name: "super_admin", role: auth.RoleSuperAdmin, ctx: ctxWithRole("staff-super", auth.RoleSuperAdmin, "")},
		{name: "admin", role: auth.RoleAdmin, ctx: ctxWithRole("staff-admin", auth.RoleAdmin, "")},
		{name: "moderator", role: auth.RoleModerator, ctx: ctxWithRole("staff-mod", auth.RoleModerator, "")},
		{name: "cskh", role: auth.RoleCSKH, ctx: ctxWithRole("staff-cskh", auth.RoleCSKH, "")},
		{name: "accountant", role: auth.RoleAccountant, ctx: ctxWithRole("staff-acct", auth.RoleAccountant, "")},
		{name: "ops", role: auth.RoleOps, ctx: ctxWithRole("staff-ops", auth.RoleOps, "")},
		{name: "buyer", role: auth.RoleBuyer, ctx: ctxWithRole("buyer-rbac", auth.RoleBuyer, "")},
		{name: "other_seller", role: auth.RoleSeller, ctx: ctxWithRole("seller-other", auth.RoleSeller, "shop-other")},
		{name: "service_token", role: "", ctx: ctxWithService("order-service"), isService: true},
		{name: "anonymous", role: "", ctx: context.Background(), isAnon: true},
	}

	superCtx := principals[0].ctx
	ownerSellerCtx := ctxWithRole("seller-rbac-owner", auth.RoleSeller, "shop-rbac-1")

	// Seed baseline category, banned keyword (REVIEW), approved+flagged active product, and suspended shop product.
	baseCatResp, err := srv.CreateCategory(superCtx, &productv1.CreateCategoryRequest{
		Name: "RBAC Base Cat", Slug: "rbac-base-cat", CommissionRateBps: 500,
	})
	if err != nil {
		t.Fatalf("seed CreateCategory: %v", err)
	}
	baseCatID := baseCatResp.GetCategory().GetId()

	if _, err := srv.CreateBannedKeyword(superCtx, &productv1.CreateBannedKeywordRequest{
		Term:     "nhạy cảm rbac",
		Action:   productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_REVIEW,
		Category: "GENERAL",
	}); err != nil {
		t.Fatalf("seed CreateBannedKeyword: %v", err)
	}

	// Active shop product (APPROVED, with flagged_keywords populated).
	activeProdResp, err := srv.CreateProduct(ctxWithRole("seller-active", auth.RoleSeller, "shop-rbac-active"), &productv1.CreateProductRequest{
		CategoryId:  baseCatID,
		Name:        "Sản phẩm hoạt động nhạy cảm rbac",
		Slug:        "san-pham-hoat-dong-rbac",
		Description: "Mô tả hợp lệ",
		Skus:        []*productv1.SKUInput{{SkuCode: "RBAC-ACT-1", Name: "M", PriceVnd: 199000, Stock: 10}},
	})
	if err != nil {
		t.Fatalf("seed active product: %v", err)
	}
	activeProdID := activeProdResp.GetProduct().GetId()
	if _, err := srv.AdminReviewProduct(superCtx, &productv1.AdminReviewProductRequest{
		ProductId: activeProdID,
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	}); err != nil {
		t.Fatalf("approve active product: %v", err)
	}

	// Suspended shop product (APPROVED + shop_suspended=true + flagged_keywords populated).
	suspProdResp, err := srv.CreateProduct(ownerSellerCtx, &productv1.CreateProductRequest{
		CategoryId:  baseCatID,
		Name:        "Sản phẩm shop khóa nhạy cảm rbac",
		Slug:        "san-pham-shop-khoa-rbac",
		Description: "Mô tả hợp lệ",
		Skus:        []*productv1.SKUInput{{SkuCode: "RBAC-SUSP-1", Name: "L", PriceVnd: 299000, Stock: 12}},
	})
	if err != nil {
		t.Fatalf("seed suspended product: %v", err)
	}
	suspProdID := suspProdResp.GetProduct().GetId()
	suspSKUID := suspProdResp.GetProduct().GetSkus()[0].GetId()
	if _, err := srv.AdminReviewProduct(superCtx, &productv1.AdminReviewProductRequest{
		ProductId: suspProdID,
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	}); err != nil {
		t.Fatalf("approve suspended product: %v", err)
	}
	if err := statusHandler.HandleShopStatusChanged(ctx, "evt-rbac-susp-1", events.ShopStatusChangedPayload{
		ShopID: "shop-rbac-1", FromStatus: "ACTIVE", ToStatus: "SUSPENDED", Version: 1,
	}); err != nil {
		t.Fatalf("suspend shop-rbac-1: %v", err)
	}

	hasCap := func(p principal, capName string) bool {
		if p.isAnon || p.isService {
			return false
		}
		return auth.HasCapability(&auth.Claims{Role: p.role}, capName)
	}

	assertAccess := func(t *testing.T, rpcName string, p principal, capName string, err error) {
		t.Helper()
		allowed := hasCap(p, capName)
		if allowed {
			if err != nil {
				t.Errorf("%s [%s] (has %s): expected success, got %v", rpcName, p.name, capName, err)
			}
			return
		}
		wantCode := codes.PermissionDenied
		if p.isAnon {
			wantCode = codes.Unauthenticated
		}
		if status.Code(err) != wantCode {
			t.Errorf("%s [%s] (lacks %s): want %v, got %v", rpcName, p.name, capName, wantCode, err)
		}
	}

	// === 1. Matrix across all 12 capability-gated Admin RPCs ===
	t.Run("CategoryWriteRPCs", func(t *testing.T) {
		for i, p := range principals {
			created, cErr := srv.CreateCategory(p.ctx, &productv1.CreateCategoryRequest{
				Name: fmt.Sprintf("Cat %s %d", p.name, i), Slug: fmt.Sprintf("cat-%s-%d", p.name, i), CommissionRateBps: 400,
			})
			assertAccess(t, "CreateCategory", p, auth.CapCategoriesWrite, cErr)

			targetID := baseCatID
			if cErr == nil && created != nil {
				targetID = created.GetCategory().GetId()
			}
			_, uErr := srv.UpdateCategory(p.ctx, &productv1.UpdateCategoryRequest{
				Id: targetID, Name: fmt.Sprintf("Cat Updated %s %d", p.name, i),
			})
			assertAccess(t, "UpdateCategory", p, auth.CapCategoriesWrite, uErr)

			// Seed a fresh empty category for DeleteCategory so allowed callers succeed cleanly.
			delTarget, dSeedErr := srv.CreateCategory(superCtx, &productv1.CreateCategoryRequest{
				Name: fmt.Sprintf("DelCat %s %d", p.name, i), Slug: fmt.Sprintf("delcat-%s-%d", p.name, i), CommissionRateBps: 300,
			})
			if dSeedErr != nil {
				t.Fatalf("seed del cat: %v", dSeedErr)
			}
			_, dErr := srv.DeleteCategory(p.ctx, &productv1.DeleteCategoryRequest{Id: delTarget.GetCategory().GetId()})
			assertAccess(t, "DeleteCategory", p, auth.CapCategoriesWrite, dErr)
		}
	})

	t.Run("CategoryFeeRPCs", func(t *testing.T) {
		for i, p := range principals {
			_, lErr := srv.ListCategoryFeeSchedules(p.ctx, &productv1.ListCategoryFeeSchedulesRequest{CategoryId: baseCatID})
			assertAccess(t, "ListCategoryFeeSchedules", p, auth.CapFeesCategoryRead, lErr)

			effFrom := time.Now().UTC().Add(time.Duration(24+i) * time.Hour)
			_, sErr := srv.SetCategoryFeeSchedule(p.ctx, &productv1.SetCategoryFeeScheduleRequest{
				CategoryId:        baseCatID,
				CommissionRateBps: int32(450 + i),
				EffectiveFrom:     timestamppb.New(effFrom),
			})
			assertAccess(t, "SetCategoryFeeSchedule", p, auth.CapFeesCategoryWrite, sErr)
		}
	})

	t.Run("ProductReviewRPC", func(t *testing.T) {
		for i, p := range principals {
			// Fresh PENDING product per principal so every allowed reviewer succeeds cleanly.
			pendingResp, seedErr := srv.CreateProduct(ctxWithRole("seller-rev", auth.RoleSeller, "shop-rbac-active"), &productv1.CreateProductRequest{
				CategoryId: baseCatID,
				Name:       fmt.Sprintf("Review Target %s %d", p.name, i),
				Slug:       fmt.Sprintf("review-target-%s-%d", p.name, i),
				Skus:       []*productv1.SKUInput{{SkuCode: fmt.Sprintf("REV-%d", i), Name: "Std", PriceVnd: 120000, Stock: 3}},
			})
			if seedErr != nil {
				t.Fatalf("seed pending product: %v", seedErr)
			}
			_, rErr := srv.AdminReviewProduct(p.ctx, &productv1.AdminReviewProductRequest{
				ProductId: pendingResp.GetProduct().GetId(),
				Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
			})
			assertAccess(t, "AdminReviewProduct", p, auth.CapProductsReview, rErr)
		}
	})

	t.Run("BannedKeywordAndRescanRPCs", func(t *testing.T) {
		for i, p := range principals {
			_, lErr := srv.ListBannedKeywords(p.ctx, &productv1.ListBannedKeywordsRequest{})
			assertAccess(t, "ListBannedKeywords", p, auth.CapBannedKeywordsRead, lErr)

			_, prevErr := srv.PreviewBannedKeyword(p.ctx, &productv1.PreviewBannedKeywordRequest{
				Term: "rbac",
			})
			assertAccess(t, "PreviewBannedKeyword", p, auth.CapBannedKeywordsRead, prevErr)

			createdKW, cErr := srv.CreateBannedKeyword(p.ctx, &productv1.CreateBannedKeywordRequest{
				Term:     fmt.Sprintf("rbac-kw-%s-%d", p.name, i),
				Action:   productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_REVIEW,
				Category: "GENERAL",
			})
			assertAccess(t, "CreateBannedKeyword", p, auth.CapBannedKeywordsWrite, cErr)

			// Seed a fresh keyword for Update & Delete so every allowed principal exercises a real write.
			seedKW, sErr := srv.CreateBannedKeyword(superCtx, &productv1.CreateBannedKeywordRequest{
				Term:     fmt.Sprintf("rbac-mut-kw-%s-%d", p.name, i),
				Action:   productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_REVIEW,
				Category: "GENERAL",
			})
			if sErr != nil {
				t.Fatalf("seed mut kw: %v", sErr)
			}
			kwID := seedKW.GetKeyword().GetId()
			if cErr == nil && createdKW != nil {
				kwID = createdKW.GetKeyword().GetId()
			}
			_, uErr := srv.UpdateBannedKeyword(p.ctx, &productv1.UpdateBannedKeywordRequest{
				Id:          kwID,
				Term:        fmt.Sprintf("rbac-upd-kw-%s-%d", p.name, i),
				Action:      productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_REVIEW,
				IsActive:    true,
				HasIsActive: true,
			})
			assertAccess(t, "UpdateBannedKeyword", p, auth.CapBannedKeywordsWrite, uErr)

			_, dErr := srv.DeleteBannedKeyword(p.ctx, &productv1.DeleteBannedKeywordRequest{Id: seedKW.GetKeyword().GetId()})
			assertAccess(t, "DeleteBannedKeyword", p, auth.CapBannedKeywordsWrite, dErr)

			_, rescanErr := srv.RescanProductsForBannedContent(p.ctx, &productv1.RescanProductsForBannedContentRequest{Limit: 10})
			assertAccess(t, "RescanProductsForBannedContent", p, auth.CapBannedKeywordsWrite, rescanErr)
		}
	})

	// === 2. products.read & Product.shop_suspended / flagged_keywords gating across all 6 roles + owner seller + public ===
	t.Run("ProductsReadAndShopSuspendedVisibility", func(t *testing.T) {
		type readCase struct {
			name        string
			ctx         context.Context
			canReadPriv bool
		}
		cases := []readCase{
			{name: "owner_seller", ctx: ownerSellerCtx, canReadPriv: true},
		}
		for _, p := range principals {
			cases = append(cases, readCase{
				name:        p.name,
				ctx:         p.ctx,
				canReadPriv: hasCap(p, auth.CapProductsRead),
			})
		}

		for _, tc := range cases {
			// GetProduct on suspended shop-rbac-1 product:
			gotSusp, sErr := srv.GetProduct(tc.ctx, &productv1.GetProductRequest{Id: suspProdID})
			if tc.canReadPriv {
				if sErr != nil {
					t.Errorf("GetProduct(suspended) [%s]: expected success, got %v", tc.name, sErr)
				} else {
					if !gotSusp.GetProduct().GetShopSuspended() {
						t.Errorf("GetProduct(suspended) [%s]: want shop_suspended=true, got false", tc.name)
					}
					if len(gotSusp.GetProduct().GetFlaggedKeywords()) == 0 {
						t.Errorf("GetProduct(suspended) [%s]: want non-empty flagged_keywords", tc.name)
					}
				}
			} else if status.Code(sErr) != codes.NotFound {
				t.Errorf("GetProduct(suspended) [%s]: want NotFound, got %v", tc.name, sErr)
			}

			// ListProducts(shop-rbac-1):
			listSusp, lErr := srv.ListProducts(tc.ctx, &productv1.ListProductsRequest{ShopId: "shop-rbac-1", Page: 1, PageSize: 20})
			if lErr != nil {
				t.Fatalf("ListProducts(shop-rbac-1) [%s]: %v", tc.name, lErr)
			}
			if tc.canReadPriv {
				if listSusp.GetTotal() != 1 || !listSusp.GetProducts()[0].GetShopSuspended() {
					t.Errorf("ListProducts(shop-rbac-1) [%s]: want total=1 with shop_suspended=true, got total=%d prods=%+v",
						tc.name, listSusp.GetTotal(), listSusp.GetProducts())
				}
			} else if listSusp.GetTotal() != 0 {
				t.Errorf("ListProducts(shop-rbac-1) [%s]: want total=0 (hidden), got %d", tc.name, listSusp.GetTotal())
			}

			// GetProduct on active shop product: public/accountant/other_seller must NOT see flagged_keywords and always shop_suspended=false.
			gotAct, aErr := srv.GetProduct(tc.ctx, &productv1.GetProductRequest{Id: activeProdID})
			if aErr != nil {
				t.Fatalf("GetProduct(active) [%s]: %v", tc.name, aErr)
			}
			if gotAct.GetProduct().GetShopSuspended() {
				t.Errorf("GetProduct(active) [%s]: want shop_suspended=false", tc.name)
			}
			expectActInternal := tc.canReadPriv && tc.name != "owner_seller" // owner_seller owns shop-rbac-1, not shop-rbac-active
			if expectActInternal && len(gotAct.GetProduct().GetFlaggedKeywords()) == 0 {
				t.Errorf("GetProduct(active) [%s]: expected flagged_keywords for products.read role", tc.name)
			}
			if !expectActInternal && len(gotAct.GetProduct().GetFlaggedKeywords()) != 0 {
				t.Errorf("GetProduct(active) [%s]: leaked flagged_keywords=%v", tc.name, gotAct.GetProduct().GetFlaggedKeywords())
			}

			// GetSKUsBatch on suspended shop SKU: products.read & owner_seller see full unredacted price/code (with IsActive=false); others redacted.
			batch, bErr := srv.GetSKUsBatch(tc.ctx, &productv1.GetSKUsBatchRequest{SkuIds: []string{suspSKUID}})
			if bErr != nil || len(batch.GetSkus()) != 1 {
				t.Fatalf("GetSKUsBatch(suspended) [%s]: len=%d err=%v", tc.name, len(batch.GetSkus()), bErr)
			}
			sk := batch.GetSkus()[0]
			if sk.GetIsActive() {
				t.Errorf("GetSKUsBatch(suspended) [%s]: suspended SKU must always have IsActive=false", tc.name)
			}
			if tc.canReadPriv {
				if sk.GetPriceVnd() != 299000 || sk.GetSkuCode() != "RBAC-SUSP-1" {
					t.Errorf("GetSKUsBatch(suspended) [%s]: expected unredacted SKU, got %+v", tc.name, sk)
				}
			} else {
				if sk.GetPriceVnd() != 0 || sk.GetSkuCode() != "" || sk.GetStock() != 0 {
					t.Errorf("GetSKUsBatch(suspended) [%s]: expected redacted SKU, got %+v", tc.name, sk)
				}
			}
		}

		// Direct unit assertion on toProtoProduct gating of shop_suspended & flagged_keywords.
		rawProd := store.Product{
			ID: "p-gate", ShopID: "shop-rbac-1", Status: store.ProductStatusApproved,
			ShopSuspended: true, FlaggedKeywords: []string{"nhạy cảm"},
		}
		pubProto := toProtoProduct(rawProd, nil, false)
		if pubProto.GetShopSuspended() || len(pubProto.GetFlaggedKeywords()) != 0 {
			t.Fatalf("toProtoProduct(includeInternal=false) leaked internal fields: suspended=%v flagged=%v",
				pubProto.GetShopSuspended(), pubProto.GetFlaggedKeywords())
		}
		privProto := toProtoProduct(rawProd, nil, true)
		if !privProto.GetShopSuspended() || len(privProto.GetFlaggedKeywords()) != 1 {
			t.Fatalf("toProtoProduct(includeInternal=true) dropped internal fields: suspended=%v flagged=%v",
				privProto.GetShopSuspended(), privProto.GetFlaggedKeywords())
		}
	})

	// === 3. Specialist staff roles must NOT gain access to saga stock RPCs or seller catalog mutations ===
	t.Run("SpecialistStaffRejectedOnSagaAndSellerMutationRPCs", func(t *testing.T) {
		for _, role := range []string{auth.RoleModerator, auth.RoleCSKH, auth.RoleAccountant, auth.RoleOps} {
			rCtx := ctxWithRole("spec-"+role, role, "")
			if _, err := srv.ConfirmStock(rCtx, &productv1.ConfirmStockRequest{OrderId: "ord-none"}); status.Code(err) != codes.PermissionDenied {
				t.Errorf("ConfirmStock [%s]: want PermissionDenied, got %v", role, err)
			}
			if _, err := srv.ReleaseStock(rCtx, &productv1.ReleaseStockRequest{OrderId: "ord-none"}); status.Code(err) != codes.PermissionDenied {
				t.Errorf("ReleaseStock [%s]: want PermissionDenied, got %v", role, err)
			}
			if _, err := srv.RestockOrderItems(rCtx, &productv1.RestockOrderItemsRequest{OrderId: "ord-none"}); status.Code(err) != codes.PermissionDenied {
				t.Errorf("RestockOrderItems [%s]: want PermissionDenied, got %v", role, err)
			}
			if _, err := srv.CreateProduct(rCtx, &productv1.CreateProductRequest{
				ShopId: "shop-rbac-active", CategoryId: baseCatID, Name: "Nope",
				Skus: []*productv1.SKUInput{{SkuCode: "NOPE", Name: "M", PriceVnd: 10000, Stock: 1}},
			}); status.Code(err) != codes.PermissionDenied {
				t.Errorf("CreateProduct [%s]: want PermissionDenied, got %v", role, err)
			}
			if _, err := srv.UpdateProduct(rCtx, &productv1.UpdateProductRequest{Id: activeProdID, Name: "Nope"}); status.Code(err) != codes.PermissionDenied {
				t.Errorf("UpdateProduct [%s]: want PermissionDenied, got %v", role, err)
			}
			if _, err := srv.UpdateStock(rCtx, &productv1.UpdateStockRequest{SkuId: suspSKUID, NewStock: 5, ExpectedVersion: 1}); status.Code(err) != codes.PermissionDenied {
				t.Errorf("UpdateStock [%s]: want PermissionDenied, got %v", role, err)
			}
			if _, err := srv.DeleteProduct(rCtx, &productv1.DeleteProductRequest{Id: activeProdID}); status.Code(err) != codes.PermissionDenied {
				t.Errorf("DeleteProduct [%s]: want PermissionDenied, got %v", role, err)
			}
		}
	})
}
