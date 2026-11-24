package e2e

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func prmW2bDialPromotion(t *testing.T, env *testEnv) promotionv1.PromotionServiceClient {
	t.Helper()
	conn, err := grpc.NewClient(env.cfg.PromotionGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial PromotionService gRPC %s: %v", env.cfg.PromotionGRPCAddr, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return promotionv1.NewPromotionServiceClient(conn)
}

func prmW2bBearerCtx(token string) (context.Context, context.CancelFunc) {
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))
	return context.WithTimeout(ctx, 20*time.Second)
}

func prmW2bCreateCategory(t *testing.T, env *testEnv, adminTok, name, parentID string) CategoryDTO {
	t.Helper()
	slug := strings.ToLower(env.uniqueCode("prm2b-cat-"))
	resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/categories", adminTok, nil, CreateCategoryRequest{
		Name:              name,
		Slug:              slug,
		ParentID:          parentID,
		CommissionRateBps: 500,
		SortOrder:         1,
	}, http.StatusCreated, http.StatusOK)
	var out CategoryResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode category: %v", err)
	}
	if out.Category.ID == "" {
		t.Fatalf("empty category id: %s", string(resp.Body))
	}
	return out.Category
}

func prmW2bStrPtr(s string) *string { return &s }
func prmW2bI32Ptr(v int32) *int32   { return &v }
func prmW2bI64Ptr(v int64) *int64   { return &v }

// TestE2E_PromotionW2B_VoucherLifecycle verifies:
// create -> save -> use -> restricted update -> pause -> rejected (Save/Preview/Reserve) & unusable in wallet
// -> resume -> works again -> soft-delete -> rejected everywhere & RBAC enforced.
func TestE2E_PromotionW2B_VoucherLifecycle(t *testing.T) {
	env := getEnv()
	cat, seller1, seller2 := env.ensureBaseMarketplace(t)
	buyer1 := env.registerVerifyAndLogin(t, "buyer-prm2b-vlife1")
	buyer2 := env.registerVerifyAndLogin(t, "buyer-prm2b-vlife2")
	client := prmW2bDialPromotion(t, env)

	// Approved real SKU priced at 150,000 VND so PreviewDiscounts resolves authoritative price (D4)
	// and preserves exact 150,000 subtotal / voucher expectations.
	_, skuLife := env.createApprovedProduct(t, seller1, cat.ID, "W2B Voucher Lifecycle SKU", 150000, 20, 300)

	s1Ctx, s1Cancel := prmW2bBearerCtx(seller1.AccessToken)
	defer s1Cancel()
	s2Ctx, s2Cancel := prmW2bBearerCtx(seller2.AccessToken)
	defer s2Cancel()
	b1Ctx, b1Cancel := prmW2bBearerCtx(buyer1.AccessToken)
	defer b1Cancel()
	b2Ctx, b2Cancel := prmW2bBearerCtx(buyer2.AccessToken)
	defer b2Cancel()
	ordCtx, ordCancel := prmW2bBearerCtx(promoOrderServiceToken())
	defer ordCancel()

	now := time.Now().UTC()
	code := env.uniqueCode("W2BLIFE")

	// 1. Create SHOP voucher
	created, err := client.CreateVoucher(s1Ctx, &promotionv1.CreateVoucherRequest{
		Code:         code,
		Name:         "W2B Lifecycle Voucher",
		Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP,
		ShopId:       seller1.ShopID,
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
		ValueVnd:     20000,
		MinOrderVnd:  100000,
		Quota:        5,
		PerUserLimit: 3,
		BudgetVnd:    100000,
		StartsAt:     timestamppb.New(now.Add(-5 * time.Minute)),
		EndsAt:       timestamppb.New(now.Add(24 * time.Hour)),
	})
	if err != nil {
		t.Fatalf("CreateVoucher: %v", err)
	}
	vid := created.GetVoucher().GetId()
	if created.GetVoucher().GetStatus() != promotionv1.VoucherStatus_VOUCHER_STATUS_ACTIVE {
		t.Fatalf("expected ACTIVE status, got %v", created.GetVoucher().GetStatus())
	}

	// Buyer-1 saves voucher into wallet and uses it once
	if _, err := client.SaveVoucher(b1Ctx, &promotionv1.SaveVoucherRequest{VoucherId: vid}); err != nil {
		t.Fatalf("SaveVoucher: %v", err)
	}
	use1OrderID := "e2e-w2b-vlife-use1-" + uuid.NewString()
	use1, err := client.ReservePromotion(ordCtx, &promotionv1.ReservePromotionRequest{
		OrderId:        use1OrderID,
		UserId:         buyer1.UserID,
		ShopVoucherIds: map[string]string{seller1.ShopID: vid},
		ShopItems: []*promotionv1.ShopDiscountInput{{
			ShopId:         seller1.ShopID,
			SubtotalVnd:    150000,
			ShippingFeeVnd: 20000,
			Items:          []*promotionv1.DiscountLineItem{{SkuId: skuLife.ID, Quantity: 1, UnitPriceVnd: 150000}},
		}},
	})
	if err != nil || !use1.GetReserved() || use1.GetBreakdown().GetTotalShopVoucherDiscountVnd() != 20000 {
		t.Fatalf("ReservePromotion (use 1) unexpected: resp=%+v err=%v", use1, err)
	}
	// Cross-user ReservePromotion replay (same order_id as buyer2) and direct buyer ReservePromotion -> PermissionDenied.
	if _, err := client.ReservePromotion(ordCtx, &promotionv1.ReservePromotionRequest{
		OrderId:        use1OrderID,
		UserId:         buyer2.UserID,
		ShopVoucherIds: map[string]string{seller1.ShopID: vid},
		ShopItems: []*promotionv1.ShopDiscountInput{{
			ShopId:         seller1.ShopID,
			SubtotalVnd:    150000,
			ShippingFeeVnd: 20000,
			Items:          []*promotionv1.DiscountLineItem{{SkuId: skuLife.ID, Quantity: 1, UnitPriceVnd: 150000}},
		}},
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cross-user ReservePromotion replay: want PermissionDenied, got %v", err)
	}
	if _, err := client.ReservePromotion(b1Ctx, &promotionv1.ReservePromotionRequest{
		OrderId:        "e2e-w2b-vlife-xuser-" + uuid.NewString(),
		UserId:         buyer2.UserID,
		ShopVoucherIds: map[string]string{seller1.ShopID: vid},
		ShopItems:      []*promotionv1.ShopDiscountInput{{ShopId: seller1.ShopID, SubtotalVnd: 150000, ShippingFeeVnd: 20000}},
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cross-user ReservePromotion spoofed user_id: want PermissionDenied, got %v", err)
	}

	// After use: lowering quota or changing discount value must fail; raising quota + extending ends_at must succeed
	if _, err := client.UpdateVoucher(s1Ctx, &promotionv1.UpdateVoucherRequest{
		VoucherId: vid, ValueVnd: prmW2bI64Ptr(30000),
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("UpdateVoucher value after use: want FailedPrecondition, got %v", err)
	}
	upd, err := client.UpdateVoucher(s1Ctx, &promotionv1.UpdateVoucherRequest{
		VoucherId: vid,
		Name:      prmW2bStrPtr("W2B Lifecycle Renamed"),
		Quota:     prmW2bI32Ptr(8),
		BudgetVnd: prmW2bI64Ptr(160000),
		EndsAt:    timestamppb.New(now.Add(48 * time.Hour)),
	})
	if err != nil || upd.GetVoucher().GetQuota() != 8 || upd.GetVoucher().GetRemainingQuota() != 7 {
		t.Fatalf("UpdateVoucher (allowed after use) unexpected: resp=%+v err=%v", upd, err)
	}

	// Cross-shop seller cannot pause/delete
	if _, err := client.SetVoucherStatus(s2Ctx, &promotionv1.SetVoucherStatusRequest{
		VoucherId: vid, Status: promotionv1.VoucherStatus_VOUCHER_STATUS_PAUSED,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("seller2 pause seller1 voucher: want PermissionDenied, got %v", err)
	}

	// 2. Pause voucher -> rejected immediately in Save/Preview/Reserve; wallet shows saved=true eligible=false
	paused, err := client.SetVoucherStatus(s1Ctx, &promotionv1.SetVoucherStatusRequest{
		VoucherId: vid, Status: promotionv1.VoucherStatus_VOUCHER_STATUS_PAUSED,
	})
	if err != nil || paused.GetVoucher().GetStatus() != promotionv1.VoucherStatus_VOUCHER_STATUS_PAUSED {
		t.Fatalf("SetVoucherStatus(PAUSED): resp=%+v err=%v", paused, err)
	}

	if _, err := client.SaveVoucher(b2Ctx, &promotionv1.SaveVoucherRequest{VoucherId: vid}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("SaveVoucher while PAUSED: want FailedPrecondition, got %v", err)
	}
	prevPaused, err := client.PreviewDiscounts(b1Ctx, &promotionv1.PreviewDiscountsRequest{
		ShopVoucherIds: map[string]string{seller1.ShopID: vid},
		ShopItems: []*promotionv1.ShopDiscountInput{{
			ShopId:         seller1.ShopID,
			ShippingFeeVnd: 20000,
			Items:          []*promotionv1.DiscountLineItem{{SkuId: skuLife.ID, Quantity: 1}},
		}},
	})
	if err != nil || prevPaused.GetBreakdown().GetTotalSubtotalVnd() != 150000 || prevPaused.GetBreakdown().GetTotalShopVoucherDiscountVnd() != 0 {
		t.Fatalf("PreviewDiscounts while PAUSED should give subtotal=150000 and 0 discount, got %+v err=%v", prevPaused, err)
	}
	if _, err := client.ReservePromotion(ordCtx, &promotionv1.ReservePromotionRequest{
		OrderId:        "e2e-w2b-vlife-paused-" + uuid.NewString(),
		UserId:         buyer1.UserID,
		ShopVoucherIds: map[string]string{seller1.ShopID: vid},
		ShopItems: []*promotionv1.ShopDiscountInput{{
			ShopId:         seller1.ShopID,
			SubtotalVnd:    150000,
			ShippingFeeVnd: 20000,
			Items:          []*promotionv1.DiscountLineItem{{SkuId: skuLife.ID, Quantity: 1, UnitPriceVnd: 150000}},
		}},
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("ReservePromotion while PAUSED: want FailedPrecondition, got %v", err)
	}

	walletResp, err := client.ListAvailableVouchers(b1Ctx, &promotionv1.ListAvailableVouchersRequest{ShopId: seller1.ShopID})
	if err != nil {
		t.Fatalf("ListAvailableVouchers(buyer1 wallet): %v", err)
	}
	foundPausedInWallet := false
	for _, v := range walletResp.GetVouchers() {
		if v.GetId() == vid {
			foundPausedInWallet = true
			if !v.GetSaved() || v.GetEligible() || v.GetStatus() != promotionv1.VoucherStatus_VOUCHER_STATUS_PAUSED {
				t.Fatalf("expected paused wallet voucher saved=true eligible=false status=PAUSED, got %+v", v)
			}
		}
	}
	if !foundPausedInWallet {
		t.Fatalf("expected saved paused voucher %s to remain visible in buyer1 wallet as unusable", vid)
	}

	// 3. Re-activate voucher -> ReservePromotion succeeds again!
	resumed, err := client.SetVoucherStatus(s1Ctx, &promotionv1.SetVoucherStatusRequest{
		VoucherId: vid, Status: promotionv1.VoucherStatus_VOUCHER_STATUS_ACTIVE,
	})
	if err != nil || resumed.GetVoucher().GetStatus() != promotionv1.VoucherStatus_VOUCHER_STATUS_ACTIVE {
		t.Fatalf("SetVoucherStatus(ACTIVE): resp=%+v err=%v", resumed, err)
	}
	use2, err := client.ReservePromotion(ordCtx, &promotionv1.ReservePromotionRequest{
		OrderId:        "e2e-w2b-vlife-use2-" + uuid.NewString(),
		UserId:         buyer2.UserID,
		ShopVoucherIds: map[string]string{seller1.ShopID: vid},
		ShopItems: []*promotionv1.ShopDiscountInput{{
			ShopId:         seller1.ShopID,
			SubtotalVnd:    150000,
			ShippingFeeVnd: 20000,
			Items:          []*promotionv1.DiscountLineItem{{SkuId: skuLife.ID, Quantity: 1, UnitPriceVnd: 150000}},
		}},
	})
	if err != nil || !use2.GetReserved() {
		t.Fatalf("ReservePromotion after resume: resp=%+v err=%v", use2, err)
	}

	// 4. Soft-delete voucher -> blocked everywhere & cannot reactivate
	delResp, err := client.DeleteVoucher(s1Ctx, &promotionv1.DeleteVoucherRequest{VoucherId: vid})
	if err != nil || !delResp.GetOk() {
		t.Fatalf("DeleteVoucher: resp=%+v err=%v", delResp, err)
	}
	if _, err := client.SetVoucherStatus(s1Ctx, &promotionv1.SetVoucherStatusRequest{
		VoucherId: vid, Status: promotionv1.VoucherStatus_VOUCHER_STATUS_ACTIVE,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("reactivating DELETED voucher: want FailedPrecondition, got %v", err)
	}
	if _, err := client.ReservePromotion(ordCtx, &promotionv1.ReservePromotionRequest{
		OrderId:        "e2e-w2b-vlife-del-" + uuid.NewString(),
		UserId:         buyer2.UserID,
		ShopVoucherIds: map[string]string{seller1.ShopID: vid},
		ShopItems: []*promotionv1.ShopDiscountInput{{
			ShopId:         seller1.ShopID,
			SubtotalVnd:    150000,
			ShippingFeeVnd: 20000,
			Items:          []*promotionv1.DiscountLineItem{{SkuId: skuLife.ID, Quantity: 1, UnitPriceVnd: 150000}},
		}},
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("ReservePromotion on DELETED voucher: want FailedPrecondition, got %v", err)
	}
}

// TestE2E_PromotionW2B_CategoryScopedPlatformVoucher verifies platform voucher restricted to a
// parent category applies ONLY to items in that category or its descendants (via ProductService.GetCategoryDescendants).
func TestE2E_PromotionW2B_CategoryScopedPlatformVoucher(t *testing.T) {
	env := getEnv()
	adminTok := env.adminLogin(t)
	_, seller1, seller2 := env.ensureBaseMarketplace(t)
	buyer := env.registerVerifyAndLogin(t, "buyer-prm2b-cat")
	client := prmW2bDialPromotion(t, env)

	parentCat := prmW2bCreateCategory(t, env, adminTok, "W2B Fashion Root", "")
	childCat := prmW2bCreateCategory(t, env, adminTok, "W2B Fashion Tees", parentCat.ID)
	otherCat := prmW2bCreateCategory(t, env, adminTok, "W2B Home Goods", "")

	_, skuChild := env.createApprovedProduct(t, seller1, childCat.ID, "W2B Cat Child Tee", 200000, 20, 300)
	_, skuOther := env.createApprovedProduct(t, seller2, otherCat.ID, "W2B Cat Other Lamp", 500000, 20, 500)

	aCtx, aCancel := prmW2bBearerCtx(adminTok)
	defer aCancel()
	bCtx, bCancel := prmW2bBearerCtx(buyer.AccessToken)
	defer bCancel()

	now := time.Now().UTC()
	vResp, err := client.CreateVoucher(aCtx, &promotionv1.CreateVoucherRequest{
		Code:            env.uniqueCode("W2BCAT"),
		Name:            "Fashion Only 10%",
		Scope:           promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
		DiscountType:    promotionv1.DiscountType_DISCOUNT_TYPE_PERCENT,
		DiscountPercent: 10,
		MaxDiscountVnd:  100000,
		MinOrderVnd:     100000,
		Quota:           10,
		PerUserLimit:    2,
		StartsAt:        timestamppb.New(now.Add(-5 * time.Minute)),
		EndsAt:          timestamppb.New(now.Add(24 * time.Hour)),
		CategoryIds:     []string{parentCat.ID},
	})
	if err != nil {
		t.Fatalf("CreateVoucher category-scoped: %v", err)
	}
	vid := vResp.GetVoucher().GetId()

	// Preview with 1 child-category item (200k) + 1 unrelated item (500k) -> 10% of 200k = 20,000 VND ONLY!
	prev, err := client.PreviewDiscounts(bCtx, &promotionv1.PreviewDiscountsRequest{
		PlatformVoucherId: vid,
		ShopItems: []*promotionv1.ShopDiscountInput{
			{
				ShopId: seller1.ShopID, ShippingFeeVnd: 20000,
				Items: []*promotionv1.DiscountLineItem{{SkuId: skuChild.ID, Quantity: 1, UnitPriceVnd: 200000}},
			},
			{
				ShopId: seller2.ShopID, ShippingFeeVnd: 20000,
				Items: []*promotionv1.DiscountLineItem{{SkuId: skuOther.ID, Quantity: 1, UnitPriceVnd: 500000}},
			},
		},
	})
	if err != nil {
		t.Fatalf("PreviewDiscounts category-scoped: %v", err)
	}
	if prev.GetBreakdown().GetTotalPlatformVoucherDiscountVnd() != 20000 {
		t.Fatalf("expected 20000 platform discount (10%% of 200k child-cat item only), got %d (warnings=%v)",
			prev.GetBreakdown().GetTotalPlatformVoucherDiscountVnd(), prev.GetBreakdown().GetWarnings())
	}
	for _, sb := range prev.GetBreakdown().GetShops() {
		if sb.GetShopId() == seller1.ShopID && sb.GetPlatformVoucherDiscountVnd() != 20000 {
			t.Fatalf("expected seller1 platform alloc 20000, got %d", sb.GetPlatformVoucherDiscountVnd())
		}
		if sb.GetShopId() == seller2.ShopID && sb.GetPlatformVoucherDiscountVnd() != 0 {
			t.Fatalf("expected seller2 platform alloc 0, got %d", sb.GetPlatformVoucherDiscountVnd())
		}
	}

	// Reserve with ONLY unrelated-category item -> rejected!
	ordCtx, ordCancel := prmW2bBearerCtx(promoOrderServiceToken())
	defer ordCancel()
	if _, err := client.ReservePromotion(ordCtx, &promotionv1.ReservePromotionRequest{
		OrderId:           "e2e-w2b-cat-reject-" + uuid.NewString(),
		UserId:            buyer.UserID,
		PlatformVoucherId: vid,
		ShopItems: []*promotionv1.ShopDiscountInput{
			{
				ShopId: seller2.ShopID, ShippingFeeVnd: 20000,
				Items: []*promotionv1.DiscountLineItem{{SkuId: skuOther.ID, Quantity: 1, UnitPriceVnd: 500000}},
			},
		},
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("ReservePromotion with only ineligible category: want FailedPrecondition, got %v", err)
	}
}

// TestE2E_PromotionW2B_FlashSaleValidationRejections verifies flash registration rejections for
// price (>= price/min_30d or < min_discount_percent), stock (total_slots > available_stock),
// cross-shop SKU ownership, overlapping window, plus ListFlashSaleItems & ListAllFlashSales & upcoming reserve rejection.
func TestE2E_PromotionW2B_FlashSaleValidationRejections(t *testing.T) {
	env := getEnv()
	adminTok := env.adminLogin(t)
	cat, seller1, seller2 := env.ensureBaseMarketplace(t)
	buyer := env.registerVerifyAndLogin(t, "buyer-prm2b-flash")
	client := prmW2bDialPromotion(t, env)

	_, sku1 := env.createApprovedProduct(t, seller1, cat.ID, "W2B Flash Validated SKU", 200000, 5, 300)

	aCtx, aCancel := prmW2bBearerCtx(adminTok)
	defer aCancel()
	s1Ctx, s1Cancel := prmW2bBearerCtx(seller1.AccessToken)
	defer s1Cancel()
	s2Ctx, s2Cancel := prmW2bBearerCtx(seller2.AccessToken)
	defer s2Cancel()
	ordCtx, ordCancel := prmW2bBearerCtx(promoOrderServiceToken())
	defer ordCancel()

	now := time.Now().UTC()
	fs1, err := client.CreateFlashSale(aCtx, &promotionv1.CreateFlashSaleRequest{
		Name:               "W2B Flash 20% Min " + uuid.NewString()[:8],
		StartsAt:           timestamppb.New(now.Add(1 * time.Hour)),
		EndsAt:             timestamppb.New(now.Add(3 * time.Hour)),
		MinDiscountPercent: 20,
	})
	if err != nil {
		t.Fatalf("CreateFlashSale 1: %v", err)
	}
	fsOverlap, err := client.CreateFlashSale(aCtx, &promotionv1.CreateFlashSaleRequest{
		Name:     "W2B Flash Overlap " + uuid.NewString()[:8],
		StartsAt: timestamppb.New(now.Add(2 * time.Hour)),
		EndsAt:   timestamppb.New(now.Add(4 * time.Hour)),
	})
	if err != nil {
		t.Fatalf("CreateFlashSale overlap: %v", err)
	}

	// 1. Price >= original price (200k) rejected
	if _, err := client.RegisterFlashSaleItem(s1Ctx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId: fs1.GetFlashSale().GetId(), SkuId: sku1.ID, FlashPriceVnd: 200000, TotalSlots: 3, PerUserLimit: 1,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("flash price >= original price: want FailedPrecondition, got %v", err)
	}
	// 2. Price below min_discount_percent (170k = 15% < 20%) rejected
	if _, err := client.RegisterFlashSaleItem(s1Ctx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId: fs1.GetFlashSale().GetId(), SkuId: sku1.ID, FlashPriceVnd: 170000, TotalSlots: 3, PerUserLimit: 1,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("flash price below min_discount_percent: want FailedPrecondition, got %v", err)
	}
	// 3. total_slots > available_stock (10 > 5) rejected
	if _, err := client.RegisterFlashSaleItem(s1Ctx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId: fs1.GetFlashSale().GetId(), SkuId: sku1.ID, FlashPriceVnd: 150000, TotalSlots: 10, PerUserLimit: 1,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("flash total_slots > available_stock: want FailedPrecondition, got %v", err)
	}
	// 4. Cross-shop SKU rejected at service layer!
	if _, err := client.RegisterFlashSaleItem(s2Ctx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId: fs1.GetFlashSale().GetId(), SkuId: sku1.ID, FlashPriceVnd: 150000, TotalSlots: 3, PerUserLimit: 1,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cross-shop SKU flash registration: want PermissionDenied, got %v", err)
	}

	// 5. Valid registration succeeds (150k = 25% off >= 20%, slots=3 <= 5)
	reg, err := client.RegisterFlashSaleItem(s1Ctx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId: fs1.GetFlashSale().GetId(), SkuId: sku1.ID, FlashPriceVnd: 150000, TotalSlots: 3, PerUserLimit: 1,
	})
	if err != nil || reg.GetItem().GetStatus() != promotionv1.FlashSaleItemStatus_FLASH_SALE_ITEM_STATUS_PENDING {
		t.Fatalf("valid RegisterFlashSaleItem unexpected: resp=%+v err=%v", reg, err)
	}

	// 6. Overlapping flash window for same SKU rejected!
	if _, err := client.RegisterFlashSaleItem(s1Ctx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId: fsOverlap.GetFlashSale().GetId(), SkuId: sku1.ID, FlashPriceVnd: 140000, TotalSlots: 2, PerUserLimit: 1,
	}); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("overlapping flash window: want AlreadyExists, got %v", err)
	}

	// 7. Admin & seller1 see PENDING item in ListFlashSaleItems; ListAllFlashSales lists UPCOMING campaign
	adminPending, err := client.ListFlashSaleItems(aCtx, &promotionv1.ListFlashSaleItemsRequest{
		Status:      promotionv1.FlashSaleItemStatus_FLASH_SALE_ITEM_STATUS_PENDING,
		FlashSaleId: fs1.GetFlashSale().GetId(),
	})
	if err != nil || adminPending.GetTotal() < 1 {
		t.Fatalf("admin ListFlashSaleItems(PENDING) unexpected: resp=%+v err=%v", adminPending, err)
	}
	upcList, err := client.ListAllFlashSales(s1Ctx, &promotionv1.ListAllFlashSalesRequest{Status: "UPCOMING"})
	if err != nil || upcList.GetTotal() < 1 {
		t.Fatalf("seller ListAllFlashSales(UPCOMING) unexpected: resp=%+v err=%v", upcList, err)
	}

	// 8. Approve upcoming item -> ReservePromotion MUST reject upcoming slot!
	if _, err := client.ApproveFlashSaleItem(aCtx, &promotionv1.ApproveFlashSaleItemRequest{
		ItemId: reg.GetItem().GetId(), Approve: true,
	}); err != nil {
		t.Fatalf("ApproveFlashSaleItem: %v", err)
	}
	if _, err := client.ReservePromotion(ordCtx, &promotionv1.ReservePromotionRequest{
		OrderId: "e2e-w2b-upc-flash-" + uuid.NewString(),
		UserId:  buyer.UserID,
		FlashSaleItems: []*promotionv1.FlashSaleReservationItem{
			{FlashSaleItemId: reg.GetItem().GetId(), SkuId: sku1.ID, Quantity: 1, ShopId: seller1.ShopID},
		},
		ShopItems: []*promotionv1.ShopDiscountInput{
			{
				ShopId:      seller1.ShopID,
				SubtotalVnd: 200000,
				Items: []*promotionv1.DiscountLineItem{
					{SkuId: sku1.ID, Quantity: 1, UnitPriceVnd: 200000, FlashSaleItemId: reg.GetItem().GetId()},
				},
			},
		},
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("ReservePromotion upcoming flash slot: want FailedPrecondition, got %v", err)
	}
}

// TestE2E_PromotionW2B_ShopDiscountProgram_PreviewAndFlashWins verifies:
// shop discount program CRUD + End, public ListActiveShopDiscounts, preview applies shop program,
// flash sale takes precedence over shop discount program (no stacking), shop freeship + platform freeship,
// and partial ReleasePromotionShop releases shop program per-user limit idempotently.
func TestE2E_PromotionW2B_ShopDiscountProgram_PreviewAndFlashWins(t *testing.T) {
	env := getEnv()
	adminTok := env.adminLogin(t)
	cat, seller1, _ := env.ensureBaseMarketplace(t)
	buyer := env.registerVerifyAndLogin(t, "buyer-prm2b-prog")
	client := prmW2bDialPromotion(t, env)

	_, skuA := env.createApprovedProduct(t, seller1, cat.ID, "W2B ShopProg SKU A", 200000, 20, 300)
	_, skuB := env.createApprovedProduct(t, seller1, cat.ID, "W2B ShopProg SKU B", 100000, 20, 300)

	aCtx, aCancel := prmW2bBearerCtx(adminTok)
	defer aCancel()
	s1Ctx, s1Cancel := prmW2bBearerCtx(seller1.AccessToken)
	defer s1Cancel()
	bCtx, bCancel := prmW2bBearerCtx(buyer.AccessToken)
	defer bCancel()

	now := time.Now().UTC()

	// 1. Create active shop discount program: skuA -> 160k (-40k), skuB -> 20% off = 80k (-20k)
	progResp, err := client.CreateShopDiscountProgram(s1Ctx, &promotionv1.CreateShopDiscountProgramRequest{
		Name:     "W2B Shop Sale " + uuid.NewString()[:6],
		StartsAt: timestamppb.New(now.Add(-5 * time.Minute)),
		EndsAt:   timestamppb.New(now.Add(3 * time.Hour)),
		Items: []*promotionv1.ShopDiscountProgramItemInput{
			{SkuId: skuA.ID, DiscountedPriceVnd: 160000, PerUserLimit: 2},
			{SkuId: skuB.ID, DiscountPercent: 20, PerUserLimit: 2},
		},
	})
	if err != nil {
		t.Fatalf("CreateShopDiscountProgram: %v", err)
	}
	var itemA, itemB *promotionv1.ShopDiscountProgramItem
	for _, it := range progResp.GetProgram().GetItems() {
		if it.GetSkuId() == skuA.ID {
			itemA = it
		}
		if it.GetSkuId() == skuB.ID {
			itemB = it
		}
	}
	if itemA == nil || itemB == nil || itemB.GetDiscountedPriceVnd() != 80000 {
		t.Fatalf("unexpected created program items: %+v", progResp.GetProgram().GetItems())
	}

	// Overlapping shop program for skuB rejected
	if _, err := client.CreateShopDiscountProgram(s1Ctx, &promotionv1.CreateShopDiscountProgramRequest{
		Name:     "Overlap Should Fail",
		StartsAt: timestamppb.New(now.Add(1 * time.Hour)),
		EndsAt:   timestamppb.New(now.Add(4 * time.Hour)),
		Items:    []*promotionv1.ShopDiscountProgramItemInput{{SkuId: skuB.ID, DiscountPercent: 10}},
	}); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("overlapping shop discount program: want AlreadyExists, got %v", err)
	}

	// Public ListActiveShopDiscounts (anonymous!): rejects empty query & >200 sku_ids; returns both SKUs when filtered
	pubCtx, pubCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer pubCancel()
	if _, err := client.ListActiveShopDiscounts(pubCtx, &promotionv1.ListActiveShopDiscountsRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("ListActiveShopDiscounts empty filter: want InvalidArgument, got %v", err)
	}
	tooManySKUs := make([]string, 201)
	for i := range tooManySKUs {
		tooManySKUs[i] = fmt.Sprintf("sku-cap-%03d", i)
	}
	if _, err := client.ListActiveShopDiscounts(pubCtx, &promotionv1.ListActiveShopDiscountsRequest{SkuIds: tooManySKUs}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("ListActiveShopDiscounts 201 sku_ids: want InvalidArgument, got %v", err)
	}
	actResp, err := client.ListActiveShopDiscounts(pubCtx, &promotionv1.ListActiveShopDiscountsRequest{
		SkuIds: []string{skuA.ID, skuB.ID},
	})
	if err != nil || len(actResp.GetDiscounts()) != 2 {
		t.Fatalf("ListActiveShopDiscounts unexpected: resp=%+v err=%v", actResp, err)
	}

	// 2. PreviewDiscounts applies shop discount program (even without passing flash)
	prevProg, err := client.PreviewDiscounts(bCtx, &promotionv1.PreviewDiscountsRequest{
		ShopItems: []*promotionv1.ShopDiscountInput{
			{
				ShopId:              seller1.ShopID,
				ShippingFeeVnd:      25000,
				FreeShipEnabled:     true,
				FreeShipMinOrderVnd: 150000,
				FreeShipMaxVnd:      15000,
				Items: []*promotionv1.DiscountLineItem{
					{SkuId: skuA.ID, Quantity: 1, UnitPriceVnd: 200000, ShopDiscountProgramItemId: itemA.GetId()},
					{SkuId: skuB.ID, Quantity: 1, UnitPriceVnd: 100000, ShopDiscountProgramItemId: itemB.GetId()},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("PreviewDiscounts shop program: %v", err)
	}
	bd1 := prevProg.GetBreakdown()
	// Gross 300k, shop program -60k (40k+20k) -> 240k >= 150k -> shop freeship -15k (final ship 10k) -> payable 250k
	if bd1.GetTotalShopProgramDiscountVnd() != 60000 || bd1.GetTotalShopFreeshipDiscountVnd() != 15000 || bd1.GetFinalPayableVnd() != 250000 {
		t.Fatalf("unexpected shop program preview breakdown: %+v", bd1)
	}

	// 3. Activate live Flash Sale on skuA (120k, -80k) -> Flash MUST win over shop program on skuA!
	fsLive, err := client.CreateFlashSale(aCtx, &promotionv1.CreateFlashSaleRequest{
		Name:     "W2B Live Flash Wins " + uuid.NewString()[:6],
		StartsAt: timestamppb.New(now.Add(-5 * time.Minute)),
		EndsAt:   timestamppb.New(now.Add(2 * time.Hour)),
	})
	if err != nil {
		t.Fatalf("CreateFlashSale live: %v", err)
	}
	regA, err := client.RegisterFlashSaleItem(s1Ctx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId: fsLive.GetFlashSale().GetId(), SkuId: skuA.ID, FlashPriceVnd: 120000, TotalSlots: 5, PerUserLimit: 2,
	})
	if err != nil {
		t.Fatalf("RegisterFlashSaleItem skuA: %v", err)
	}
	if _, err := client.ApproveFlashSaleItem(aCtx, &promotionv1.ApproveFlashSaleItemRequest{
		ItemId: regA.GetItem().GetId(), Approve: true,
	}); err != nil {
		t.Fatalf("ApproveFlashSaleItem skuA: %v", err)
	}

	prevFlashWins, err := client.PreviewDiscounts(bCtx, &promotionv1.PreviewDiscountsRequest{
		ShopItems: []*promotionv1.ShopDiscountInput{
			{
				ShopId:         seller1.ShopID,
				ShippingFeeVnd: 20000,
				Items: []*promotionv1.DiscountLineItem{
					{SkuId: skuA.ID, Quantity: 1, UnitPriceVnd: 200000, FlashSaleItemId: regA.GetItem().GetId(), ShopDiscountProgramItemId: itemA.GetId()},
					{SkuId: skuB.ID, Quantity: 1, UnitPriceVnd: 100000, ShopDiscountProgramItemId: itemB.GetId()},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("PreviewDiscounts flash wins: %v", err)
	}
	bd2 := prevFlashWins.GetBreakdown()
	if bd2.GetTotalFlashSaleDiscountVnd() != 80000 || bd2.GetTotalShopProgramDiscountVnd() != 20000 {
		t.Fatalf("expected flash=80000 and shop_program=20000 (skuB only, flash wins skuA), got flash=%d prog=%d warnings=%v",
			bd2.GetTotalFlashSaleDiscountVnd(), bd2.GetTotalShopProgramDiscountVnd(), bd2.GetWarnings())
	}

	// 4. Reserve & ReleasePromotionShop -> releases flash=1 and shop_program=1 cleanly
	ordCtx, ordCancel := prmW2bBearerCtx(promoOrderServiceToken())
	defer ordCancel()
	groupID := "e2e-w2b-prog-res-" + uuid.NewString()
	res, err := client.ReservePromotion(ordCtx, &promotionv1.ReservePromotionRequest{
		OrderId: groupID,
		UserId:  buyer.UserID,
		ShopItems: []*promotionv1.ShopDiscountInput{
			{
				ShopId:         seller1.ShopID,
				ShippingFeeVnd: 20000,
				Items: []*promotionv1.DiscountLineItem{
					{SkuId: skuA.ID, Quantity: 1, UnitPriceVnd: 200000, FlashSaleItemId: regA.GetItem().GetId(), ShopDiscountProgramItemId: itemA.GetId()},
					{SkuId: skuB.ID, Quantity: 1, UnitPriceVnd: 100000, ShopDiscountProgramItemId: itemB.GetId()},
				},
			},
		},
		FlashSaleItems: []*promotionv1.FlashSaleReservationItem{
			{FlashSaleItemId: regA.GetItem().GetId(), SkuId: skuA.ID, Quantity: 1, ShopId: seller1.ShopID},
		},
		ShopDiscountProgramItems: []*promotionv1.ShopDiscountProgramReservationItem{
			{ShopDiscountProgramItemId: itemA.GetId(), SkuId: skuA.ID, Quantity: 1, ShopId: seller1.ShopID},
			{ShopDiscountProgramItemId: itemB.GetId(), SkuId: skuB.ID, Quantity: 1, ShopId: seller1.ShopID},
		},
	})
	if err != nil || !res.GetReserved() {
		t.Fatalf("ReservePromotion flash+prog: resp=%+v err=%v", res, err)
	}

	rel, err := client.ReleasePromotionShop(aCtx, &promotionv1.ReleasePromotionShopRequest{
		OrderId: groupID, ShopId: seller1.ShopID, Reason: "e2e_w2b_release",
	})
	if err != nil || !rel.GetReleased() || rel.GetFlashSaleQuantityReleased() != 1 || rel.GetShopProgramQuantityReleased() != 1 {
		t.Fatalf("ReleasePromotionShop unexpected: resp=%+v err=%v", rel, err)
	}

	// 4b. Exhaust per_user_limit=2 on skuB, then buy again -> BOTH PreviewDiscounts and ReservePromotion
	// fall back to regular price with warning and empty applied_shop_program_item_ids; idempotent replay preserves old list.
	exhaustOrderID := "e2e-w2b-prog-exhaust-" + uuid.NewString()
	exhaustShopItems := []*promotionv1.ShopDiscountInput{
		{
			ShopId:         seller1.ShopID,
			ShippingFeeVnd: 20000,
			Items: []*promotionv1.DiscountLineItem{
				{SkuId: skuB.ID, Quantity: 2, UnitPriceVnd: 100000, ShopDiscountProgramItemId: itemB.GetId()},
			},
		},
	}
	resExhaust, err := client.ReservePromotion(ordCtx, &promotionv1.ReservePromotionRequest{
		OrderId:   exhaustOrderID,
		UserId:    buyer.UserID,
		ShopItems: exhaustShopItems,
		ShopDiscountProgramItems: []*promotionv1.ShopDiscountProgramReservationItem{
			{ShopDiscountProgramItemId: itemB.GetId(), SkuId: skuB.ID, Quantity: 2, ShopId: seller1.ShopID},
		},
	})
	if err != nil || !resExhaust.GetReserved() || resExhaust.GetBreakdown().GetTotalShopProgramDiscountVnd() != 40000 {
		t.Fatalf("ReservePromotion exhaust limit unexpected: resp=%+v err=%v", resExhaust, err)
	}
	if ids := resExhaust.GetBreakdown().GetShops()[0].GetAppliedShopProgramItemIds(); len(ids) != 1 || ids[0] != itemB.GetId() {
		t.Fatalf("expected applied_shop_program_item_ids=[%s], got %v", itemB.GetId(), ids)
	}

	// Next purchase after exhausting limit: Preview and Reserve MUST match at regular price (100k + 20k ship = 120k)
	overLimitShopItems := []*promotionv1.ShopDiscountInput{
		{
			ShopId:         seller1.ShopID,
			ShippingFeeVnd: 20000,
			Items: []*promotionv1.DiscountLineItem{
				{SkuId: skuB.ID, Quantity: 1, UnitPriceVnd: 100000, ShopDiscountProgramItemId: itemB.GetId()},
			},
		},
	}
	prevOver, err := client.PreviewDiscounts(bCtx, &promotionv1.PreviewDiscountsRequest{
		ShopItems: overLimitShopItems,
	})
	if err != nil {
		t.Fatalf("PreviewDiscounts over per_user_limit: %v", err)
	}
	if prevOver.GetBreakdown().GetTotalShopProgramDiscountVnd() != 0 ||
		prevOver.GetBreakdown().GetFinalPayableVnd() != 120000 ||
		len(prevOver.GetBreakdown().GetWarnings()) == 0 ||
		len(prevOver.GetBreakdown().GetShops()[0].GetAppliedShopProgramItemIds()) != 0 {
		t.Fatalf("PreviewDiscounts over limit unexpected: %+v", prevOver.GetBreakdown())
	}
	overOrderID := "e2e-w2b-prog-over-" + uuid.NewString()
	resOver, err := client.ReservePromotion(ordCtx, &promotionv1.ReservePromotionRequest{
		OrderId:   overOrderID,
		UserId:    buyer.UserID,
		ShopItems: overLimitShopItems,
		ShopDiscountProgramItems: []*promotionv1.ShopDiscountProgramReservationItem{
			{ShopDiscountProgramItemId: itemB.GetId(), SkuId: skuB.ID, Quantity: 1, ShopId: seller1.ShopID},
		},
	})
	if err != nil || !resOver.GetReserved() {
		t.Fatalf("ReservePromotion over per_user_limit should succeed at regular price, got resp=%+v err=%v", resOver, err)
	}
	if resOver.GetBreakdown().GetTotalShopProgramDiscountVnd() != 0 ||
		resOver.GetBreakdown().GetFinalPayableVnd() != prevOver.GetBreakdown().GetFinalPayableVnd() ||
		len(resOver.GetBreakdown().GetWarnings()) == 0 ||
		len(resOver.GetBreakdown().GetShops()[0].GetAppliedShopProgramItemIds()) != 0 {
		t.Fatalf("ReservePromotion over limit mismatch vs preview: prev=%+v res=%+v", prevOver.GetBreakdown(), resOver.GetBreakdown())
	}

	// 5. EndShopDiscountProgram -> immediately disappears from ListActiveShopDiscounts;
	// idempotent replay of exhaustOrderID STILL returns saved applied_shop_program_item_ids!
	ended, err := client.EndShopDiscountProgram(s1Ctx, &promotionv1.EndShopDiscountProgramRequest{
		ProgramId: progResp.GetProgram().GetId(),
	})
	if err != nil || ended.GetProgram().GetStatus() != promotionv1.ShopDiscountProgramStatus_SHOP_DISCOUNT_PROGRAM_STATUS_ENDED {
		t.Fatalf("EndShopDiscountProgram unexpected: resp=%+v err=%v", ended, err)
	}
	replayExhaust, err := client.ReservePromotion(ordCtx, &promotionv1.ReservePromotionRequest{
		OrderId:   exhaustOrderID,
		UserId:    buyer.UserID,
		ShopItems: exhaustShopItems,
	})
	if err != nil || !replayExhaust.GetAlreadyReserved() ||
		replayExhaust.GetBreakdown().GetTotalShopProgramDiscountVnd() != 40000 {
		t.Fatalf("idempotent replay of exhaustOrderID unexpected: resp=%+v err=%v", replayExhaust, err)
	}
	if ids := replayExhaust.GetBreakdown().GetShops()[0].GetAppliedShopProgramItemIds(); len(ids) != 1 || ids[0] != itemB.GetId() {
		t.Fatalf("idempotent replay lost applied_shop_program_item_ids: got %v want [%s]", ids, itemB.GetId())
	}
	actAfter, err := client.ListActiveShopDiscounts(pubCtx, &promotionv1.ListActiveShopDiscountsRequest{
		SkuIds: []string{skuA.ID, skuB.ID},
	})
	if err != nil || len(actAfter.GetDiscounts()) != 0 {
		t.Fatalf("expected 0 active discounts after EndShopDiscountProgram, got %+v err=%v", actAfter, err)
	}
	t.Logf("E2E Promotion W2B ShopDiscountProgram + FlashWins verified cleanly (%s)", fmt.Sprintf("prog=%s", progResp.GetProgram().GetId()))
}
