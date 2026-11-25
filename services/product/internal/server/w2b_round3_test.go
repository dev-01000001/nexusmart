package server

import (
	"context"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/product/internal/store"
)

func ctxWithService(serviceName string) context.Context {
	return auth.WithClaims(context.Background(), &auth.Claims{
		Role:    auth.ServiceRole,
		Service: serviceName,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: serviceName,
		},
	})
}

func TestW2B_Round3_ReleaseStockTombstoneSemantics(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)
	svcCtx := ctxWithService("order-service")

	_, prod := setupApprovedProduct(t, srv, "shop-r3-tomb", []*productv1.SKUInput{
		{SkuCode: "R3-TOMB-1", Name: "M", PriceVnd: 100000, Stock: 10, WeightGrams: 200},
	})
	skuID := prod.GetSkus()[0].GetId()
	orderID := "ord-r3-tombstone-cycle"

	// 1. First ReleaseStock on unseen order inserts tombstone: Released=false, AlreadyReleased=false.
	rel1, err := srv.ReleaseStock(svcCtx, &productv1.ReleaseStockRequest{OrderId: orderID})
	if err != nil {
		t.Fatalf("first ReleaseStock: %v", err)
	}
	if rel1.GetReleased() || rel1.GetAlreadyReleased() {
		t.Fatalf("first tombstone ReleaseStock: want Released=false AlreadyReleased=false, got %+v", rel1)
	}

	// 2. ConfirmStock on tombstoned order returns FailedPrecondition.
	if _, err := srv.ConfirmStock(svcCtx, &productv1.ConfirmStockRequest{OrderId: orderID}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("ConfirmStock after tombstone ReleaseStock: want FailedPrecondition, got %v", err)
	}

	// 3. Second ReleaseStock hitting existing tombstone returns Released=true, AlreadyReleased=true
	//    (consistent with allReleased branch so order recognizes the reservation as already released).
	rel2, err := srv.ReleaseStock(svcCtx, &productv1.ReleaseStockRequest{OrderId: orderID})
	if err != nil {
		t.Fatalf("second ReleaseStock: %v", err)
	}
	if !rel2.GetReleased() || !rel2.GetAlreadyReleased() {
		t.Fatalf("second tombstone ReleaseStock: want Released=true AlreadyReleased=true, got %+v", rel2)
	}

	// 4. Late ReserveStock after tombstone is still rejected and leaves reserved_stock == 0.
	if _, err := srv.ReserveStock(ctxWithRole("buyer-r3-tomb", "buyer", ""), &productv1.ReserveStockRequest{
		OrderId: orderID,
		Items:   []*productv1.StockReservationItem{{SkuId: skuID, Quantity: 2}},
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("late ReserveStock after tombstone: want FailedPrecondition, got %v", err)
	}
	sk, err := st.GetSKUWithContext(context.Background(), skuID)
	if err != nil || sk.ReservedStock != 0 {
		t.Fatalf("late ReserveStock leaked reserved_stock=%d err=%v", sk.ReservedStock, err)
	}
}

func TestW2B_Round3_ReleaseAndConfirmStock_RequireServiceOrAdmin(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)

	buyerCtx := ctxWithRole("buyer-r3", "buyer", "")
	sellerCtx := ctxWithRole("seller-r3", "seller", "shop-r3-auth")
	adminCtx := ctxWithRole("admin-r3", "admin", "")
	svcCtx := ctxWithService("order-service")

	_, prod := setupApprovedProduct(t, srv, "shop-r3-auth", []*productv1.SKUInput{
		{SkuCode: "R3-AUTH-1", Name: "M", PriceVnd: 100000, Stock: 20, WeightGrams: 200},
	})
	skuID := prod.GetSkus()[0].GetId()

	if _, err := srv.ReserveStock(buyerCtx, &productv1.ReserveStockRequest{
		OrderId: "ord-r3-auth-rel",
		Items:   []*productv1.StockReservationItem{{SkuId: skuID, Quantity: 2}},
	}); err != nil {
		t.Fatalf("ReserveStock rel: %v", err)
	}
	if _, err := srv.ReserveStock(buyerCtx, &productv1.ReserveStockRequest{
		OrderId: "ord-r3-auth-conf",
		Items:   []*productv1.StockReservationItem{{SkuId: skuID, Quantity: 2}},
	}); err != nil {
		t.Fatalf("ReserveStock conf: %v", err)
	}
	if _, err := srv.ReserveStock(buyerCtx, &productv1.ReserveStockRequest{
		OrderId: "ord-r3-auth-admin-rel",
		Items:   []*productv1.StockReservationItem{{SkuId: skuID, Quantity: 1}},
	}); err != nil {
		t.Fatalf("ReserveStock admin rel: %v", err)
	}
	if _, err := srv.ReserveStock(buyerCtx, &productv1.ReserveStockRequest{
		OrderId: "ord-r3-auth-admin-conf",
		Items:   []*productv1.StockReservationItem{{SkuId: skuID, Quantity: 1}},
	}); err != nil {
		t.Fatalf("ReserveStock admin conf: %v", err)
	}

	// Anonymous -> Unauthenticated; buyer/seller -> PermissionDenied.
	for name, tc := range map[string]struct {
		ctx  context.Context
		want codes.Code
	}{
		"anonymous": {ctx: context.Background(), want: codes.Unauthenticated},
		"buyer":     {ctx: buyerCtx, want: codes.PermissionDenied},
		"seller":    {ctx: sellerCtx, want: codes.PermissionDenied},
	} {
		if _, err := srv.ReleaseStock(tc.ctx, &productv1.ReleaseStockRequest{OrderId: "ord-r3-auth-rel"}); status.Code(err) != tc.want {
			t.Fatalf("ReleaseStock(%s): want %v, got %v", name, tc.want, err)
		}
		if _, err := srv.ConfirmStock(tc.ctx, &productv1.ConfirmStockRequest{OrderId: "ord-r3-auth-conf"}); status.Code(err) != tc.want {
			t.Fatalf("ConfirmStock(%s): want %v, got %v", name, tc.want, err)
		}
	}

	// Service token -> allowed for both ReleaseStock and ConfirmStock.
	relSvc, err := srv.ReleaseStock(svcCtx, &productv1.ReleaseStockRequest{OrderId: "ord-r3-auth-rel"})
	if err != nil || !relSvc.GetReleased() {
		t.Fatalf("ReleaseStock(service): resp=%+v err=%v", relSvc, err)
	}
	confSvc, err := srv.ConfirmStock(svcCtx, &productv1.ConfirmStockRequest{OrderId: "ord-r3-auth-conf"})
	if err != nil || !confSvc.GetConfirmed() {
		t.Fatalf("ConfirmStock(service): resp=%+v err=%v", confSvc, err)
	}

	// Admin JWT -> allowed for both ReleaseStock and ConfirmStock.
	relAdm, err := srv.ReleaseStock(adminCtx, &productv1.ReleaseStockRequest{OrderId: "ord-r3-auth-admin-rel"})
	if err != nil || !relAdm.GetReleased() {
		t.Fatalf("ReleaseStock(admin): resp=%+v err=%v", relAdm, err)
	}
	confAdm, err := srv.ConfirmStock(adminCtx, &productv1.ConfirmStockRequest{OrderId: "ord-r3-auth-admin-conf"})
	if err != nil || !confAdm.GetConfirmed() {
		t.Fatalf("ConfirmStock(admin): resp=%+v err=%v", confAdm, err)
	}
}

func TestW2B_Round3_CategoryFeeSchedule_TieBreakBySeqNotCreatedAt(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)

	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	now := base
	srv.SetClock(func() time.Time { return now })

	adminCtx := ctxWithRole("admin-r3", "admin", "")
	sellerCtx := ctxWithRole("seller-r3", "seller", "shop-r3-fee")

	catResp, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name:              "Fee TieBreak Cat",
		Slug:              "fee-tiebreak-cat",
		CommissionRateBps: 500,
	})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	catID := catResp.GetCategory().GetId()

	prodResp, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
		CategoryId: catID,
		Name:       "Fee TieBreak Product",
		Skus:       []*productv1.SKUInput{{SkuCode: "R3-FEE-1", Name: "M", PriceVnd: 150000, Stock: 5}},
	})
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	prodID := prodResp.GetProduct().GetId()
	skuID := prodResp.GetProduct().GetSkus()[0].GetId()
	if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
		ProductId: prodID,
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
	}); err != nil {
		t.Fatalf("AdminReviewProduct approve: %v", err)
	}

	effAt := base.Add(48 * time.Hour)

	// Schedule 1 (lower seq, 700 bps) written at base + 10m (LATER created_at).
	now = base.Add(10 * time.Minute)
	if _, err := srv.SetCategoryFeeSchedule(adminCtx, &productv1.SetCategoryFeeScheduleRequest{
		CategoryId:        catID,
		CommissionRateBps: 700,
		EffectiveFrom:     timestamppb.New(effAt),
		Note:              "first write with later created_at",
	}); err != nil {
		t.Fatalf("SetCategoryFeeSchedule #1: %v", err)
	}

	// Schedule 2 (higher seq, 900 bps) written with clock skewed backward to base + 5m (EARLIER created_at).
	// Tie-breaker MUST be (effective_from DESC, seq DESC) ignoring created_at, so 900 bps wins!
	now = base.Add(5 * time.Minute)
	if _, err := srv.SetCategoryFeeSchedule(adminCtx, &productv1.SetCategoryFeeScheduleRequest{
		CategoryId:        catID,
		CommissionRateBps: 900,
		EffectiveFrom:     timestamppb.New(effAt),
		Note:              "second write with earlier created_at but higher seq",
	}); err != nil {
		t.Fatalf("SetCategoryFeeSchedule #2: %v", err)
	}

	// 1. ListCategoryFeeSchedules orders by effective_from DESC, seq DESC -> 900 before 700.
	listResp, err := srv.ListCategoryFeeSchedules(adminCtx, &productv1.ListCategoryFeeSchedulesRequest{
		CategoryId: catID,
		Page:       1,
		PageSize:   10,
	})
	if err != nil {
		t.Fatalf("ListCategoryFeeSchedules: %v", err)
	}
	if len(listResp.GetSchedules()) < 2 {
		t.Fatalf("expected >= 2 schedules, got %d", len(listResp.GetSchedules()))
	}
	if listResp.GetSchedules()[0].GetCommissionRateBps() != 900 || listResp.GetSchedules()[1].GetCommissionRateBps() != 700 {
		t.Fatalf("ListCategoryFeeSchedules order = [%d, %d], want [900, 700] (seq DESC ignores created_at)",
			listResp.GetSchedules()[0].GetCommissionRateBps(), listResp.GetSchedules()[1].GetCommissionRateBps())
	}

	// 2. At effAt, effective commission across ListCategories, GetProduct, and GetSKUsBatch MUST be 900 bps.
	now = effAt
	cats, err := srv.ListCategories(context.Background(), &productv1.ListCategoriesRequest{})
	if err != nil || len(cats.GetCategories()) != 1 || cats.GetCategories()[0].GetCommissionRateBps() != 900 {
		t.Fatalf("ListCategories at effAt: want 900 bps, got %+v err=%v", cats.GetCategories(), err)
	}
	gp, err := srv.GetProduct(sellerCtx, &productv1.GetProductRequest{Id: prodID})
	if err != nil || gp.GetProduct().GetSkus()[0].GetCommissionRateBps() != 900 {
		t.Fatalf("GetProduct at effAt: want 900 bps, got %+v err=%v", gp.GetProduct().GetSkus(), err)
	}
	gb, err := srv.GetSKUsBatch(context.Background(), &productv1.GetSKUsBatchRequest{SkuIds: []string{skuID}})
	if err != nil || gb.GetSkus()[0].GetCommissionRateBps() != 900 {
		t.Fatalf("GetSKUsBatch at effAt: want 900 bps, got %+v err=%v", gb.GetSkus(), err)
	}
}
