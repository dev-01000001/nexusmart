package e2e

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func prmFixGetVoucherUsedCount(t *testing.T, env *testEnv, token, scope, voucherID string) int32 {
	t.Helper()
	path := "/api/v1/vouchers"
	if scope != "" {
		path += "?scope=" + url.QueryEscape(scope)
	}
	resp := env.mustDoJSON(t, http.MethodGet, path, token, nil, nil, http.StatusOK)
	var out VouchersListResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode vouchers list: %v", err)
	}
	for _, v := range out.Vouchers {
		if v.ID == voucherID {
			return v.UsedCount
		}
	}
	t.Fatalf("voucher %s not found in GET %s", voucherID, path)
	return -1
}

func prmFixDialPromotion(t *testing.T, env *testEnv) promotionv1.PromotionServiceClient {
	t.Helper()
	conn, err := grpc.NewClient(env.cfg.PromotionGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial PromotionService gRPC %s: %v", env.cfg.PromotionGRPCAddr, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return promotionv1.NewPromotionServiceClient(conn)
}

func prmFixBearerCtx(parent context.Context, token string) (context.Context, context.CancelFunc) {
	ctx := metadata.NewOutgoingContext(parent, metadata.Pairs("authorization", "Bearer "+token))
	return context.WithTimeout(ctx, 15*time.Second)
}

// TestE2E_PromotionPartialRelease_SubOrderCancel verifies R5 (PRM-B3, ORD-B3) across Gateway + Order + Promotion:
// 2-shop checkout with shop voucher + platform voucher + coins -> cancelling one sub-order restores only that shop's
// coins & shop voucher while holding the platform voucher; cancelling the remaining sub-order restores the platform voucher.
func TestE2E_PromotionPartialRelease_SubOrderCancel(t *testing.T) {
	env := getEnv()
	cat, seller1, seller2 := env.ensureBaseMarketplace(t)

	_, sku1 := env.createApprovedProduct(t, seller1, cat.ID, "Promo Partial Shop1 Tee", 200000, 30, 300)
	_, sku2 := env.createApprovedProduct(t, seller2, cat.ID, "Promo Partial Shop2 Jeans", 300000, 30, 500)

	shop1Voucher := env.createShopVoucher(t, seller1, 20000, 100000, 10)
	shop2Voucher := env.createShopVoucher(t, seller2, 15000, 100000, 10)
	platformVoucher := env.createAdminVoucher(t, "PLATFORM", 30000, 200000, 10)

	buyer := env.registerVerifyAndLogin(t, "buyer-prm-r5")
	env.verifyBuyerPhone(t, buyer)
	addr := env.addAddress(t, buyer)

	const (
		initialCoins = int64(20000)
		coinsToUse   = int64(10000)
	)
	env.creditCoins(t, buyer.UserID, initialCoins, "e2e_prm_r5_init")
	env.saveVoucher(t, buyer.AccessToken, shop1Voucher)
	env.saveVoucher(t, buyer.AccessToken, shop2Voucher)
	env.saveVoucher(t, buyer.AccessToken, platformVoucher)

	quote1 := env.calculateShippingQuote(t, buyer.AccessToken, seller1.ShopID, 300, 200000)
	quote2 := env.calculateShippingQuote(t, buyer.AccessToken, seller2.ShopID, 500, 300000)

	checkoutResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
		"Idempotency-Key": "prm-r5-" + uuid.NewString(),
	}, CheckoutRequest{
		SalesChannel: "MARKETPLACE",
		AddressID:    addr.ID,
		FromCart:     false,
		Items: []CheckoutItemInputDTO{
			{SKUID: sku1.ID, Quantity: 1},
			{SKUID: sku2.ID, Quantity: 1},
		},
		ShopVoucherIDs: map[string]string{
			seller1.ShopID: shop1Voucher.ID,
			seller2.ShopID: shop2Voucher.ID,
		},
		PlatformVoucherID: platformVoucher.ID,
		CoinsToUse:        coinsToUse,
		PaymentMethod:     "COD",
		ShippingChoices: []ShopShippingChoiceDTO{
			{ShopID: seller1.ShopID, CarrierCode: quote1.CarrierCode, ServiceCode: quote1.ServiceCode},
			{ShopID: seller2.ShopID, CarrierCode: quote2.CarrierCode, ServiceCode: quote2.ServiceCode},
		},
	}, http.StatusCreated, http.StatusOK)

	var checkout CheckoutResponse
	if err := DecodeJSON(checkoutResp, &checkout); err != nil {
		t.Fatalf("decode checkout: %v", err)
	}
	if len(checkout.Orders) != 2 {
		t.Fatalf("expected 2 sub-orders, got %d", len(checkout.Orders))
	}

	var shop1Order, shop2Order OrderDTO
	for _, o := range checkout.Orders {
		switch o.ShopID {
		case seller1.ShopID:
			shop1Order = o
		case seller2.ShopID:
			shop2Order = o
		}
	}
	if shop1Order.ID == "" || shop2Order.ID == "" {
		t.Fatalf("missing sub-order for shop1 or shop2: %+v", checkout.Orders)
	}
	if shop1Order.CoinsDiscountVND <= 0 || shop2Order.CoinsDiscountVND <= 0 ||
		shop1Order.CoinsDiscountVND+shop2Order.CoinsDiscountVND != coinsToUse {
		t.Fatalf("unexpected coin split across sub-orders: shop1=%d shop2=%d total=%d",
			shop1Order.CoinsDiscountVND, shop2Order.CoinsDiscountVND, coinsToUse)
	}

	// Verify reserved state before cancelling
	if bal := env.getCoinBalance(t, buyer.AccessToken); bal != initialCoins-coinsToUse {
		t.Fatalf("expected coin balance %d after checkout, got %d", initialCoins-coinsToUse, bal)
	}
	if used := prmFixGetVoucherUsedCount(t, env, buyer.AccessToken, "SHOP", shop1Voucher.ID); used != 1 {
		t.Fatalf("expected shop1 voucher used_count=1 after checkout, got %d", used)
	}
	if used := prmFixGetVoucherUsedCount(t, env, buyer.AccessToken, "PLATFORM", platformVoucher.ID); used != 1 {
		t.Fatalf("expected platform voucher used_count=1 after checkout, got %d", used)
	}

	// 1. Cancel ONLY shop1 sub-order
	cancel1Path := fmt.Sprintf("/api/v1/orders/%s/cancel", url.PathEscape(shop1Order.ID))
	env.mustDoJSON(t, http.MethodPost, cancel1Path, buyer.AccessToken, nil, map[string]string{
		"reason": "e2e partial cancel shop1",
	}, http.StatusOK)

	wantCoinsAfter1 := initialCoins - coinsToUse + shop1Order.CoinsDiscountVND
	env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "shop1 coins restored after partial cancel", func() (bool, string) {
		bal := env.getCoinBalance(t, buyer.AccessToken)
		return bal == wantCoinsAfter1, fmt.Sprintf("balance=%d want=%d", bal, wantCoinsAfter1)
	})
	env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "shop1 voucher used_count decremented to 0", func() (bool, string) {
		used := prmFixGetVoucherUsedCount(t, env, buyer.AccessToken, "SHOP", shop1Voucher.ID)
		return used == 0, fmt.Sprintf("used_count=%d want=0", used)
	})
	if used := prmFixGetVoucherUsedCount(t, env, buyer.AccessToken, "PLATFORM", platformVoucher.ID); used != 1 {
		t.Fatalf("platform voucher must NOT be released while shop2 is active, got used_count=%d", used)
	}

	// 2. Cancel remaining shop2 sub-order -> all remaining coins, shop2 voucher, AND platform voucher must be restored
	cancel2Path := fmt.Sprintf("/api/v1/orders/%s/cancel", url.PathEscape(shop2Order.ID))
	env.mustDoJSON(t, http.MethodPost, cancel2Path, buyer.AccessToken, nil, map[string]string{
		"reason": "e2e cancel remaining shop2",
	}, http.StatusOK)

	env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "all coins restored after final sub-order cancel", func() (bool, string) {
		bal := env.getCoinBalance(t, buyer.AccessToken)
		return bal == initialCoins, fmt.Sprintf("balance=%d want=%d", bal, initialCoins)
	})
	env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "shop2 voucher used_count decremented to 0", func() (bool, string) {
		used := prmFixGetVoucherUsedCount(t, env, buyer.AccessToken, "SHOP", shop2Voucher.ID)
		return used == 0, fmt.Sprintf("used_count=%d want=0", used)
	})
	env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "platform voucher used_count decremented to 0 after last cancel", func() (bool, string) {
		used := prmFixGetVoucherUsedCount(t, env, buyer.AccessToken, "PLATFORM", platformVoucher.ID)
		return used == 0, fmt.Sprintf("used_count=%d want=0", used)
	})
}

// TestE2E_PromotionService_DirectGRPC_PartialRelease_Auth_And_ListFilter exercises the live PromotionService
// (real Postgres migration 002_ + real Redis Lua + real gRPC interceptor) directly for R5, PRM-B4, and PRM-B8.
func TestE2E_PromotionService_DirectGRPC_PartialRelease_Auth_And_ListFilter(t *testing.T) {
	env := getEnv()
	adminTok := env.adminLogin(t)
	cat, seller1, seller2 := env.ensureBaseMarketplace(t)
	promoClient := prmFixDialPromotion(t, env)

	t.Run("R5_ReleasePromotionShop_RealPostgresAndRedis", func(t *testing.T) {
		_, sku1 := env.createApprovedProduct(t, seller1, cat.ID, "Direct R5 SKU1", 200000, 20, 300)
		_, sku2 := env.createApprovedProduct(t, seller2, cat.ID, "Direct R5 SKU2", 300000, 20, 300)

		shop1V := env.createShopVoucher(t, seller1, 20000, 100000, 10)
		shop2V := env.createShopVoucher(t, seller2, 15000, 100000, 10)
		platV := env.createAdminVoucher(t, "PLATFORM", 30000, 200000, 10)

		// Active flash sale with 1 item per shop
		now := time.Now().UTC()
		fsResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/flash-sales", adminTok, nil, CreateFlashSaleRequest{
			Name:     "E2E R5 Flash " + uuid.NewString()[:8],
			StartsAt: now.Add(-5 * time.Minute).Format(time.RFC3339),
			EndsAt:   now.Add(2 * time.Hour).Format(time.RFC3339),
		}, http.StatusCreated, http.StatusOK)
		var fsOut FlashSaleResponse
		if err := DecodeJSON(fsResp, &fsOut); err != nil {
			t.Fatalf("decode flash sale: %v", err)
		}

		regItem := func(seller *sellerFixture, skuID string, price int64) string {
			path := fmt.Sprintf("/api/v1/seller/flash-sales/%s/items", url.PathEscape(fsOut.FlashSale.ID))
			r := env.mustDoJSON(t, http.MethodPost, path, seller.AccessToken, nil, RegisterFlashSaleItemRequest{
				SKUID: skuID, FlashPriceVND: price, TotalSlots: 10, PerUserLimit: 5,
			}, http.StatusCreated, http.StatusOK)
			var out FlashSaleItemResponse
			if err := DecodeJSON(r, &out); err != nil {
				t.Fatalf("decode reg flash item: %v", err)
			}
			appPath := fmt.Sprintf("/api/v1/admin/flash-sale-items/%s/approve", url.PathEscape(out.Item.ID))
			env.mustDoJSON(t, http.MethodPost, appPath, adminTok, nil, ApproveFlashSaleItemRequest{Approve: true}, http.StatusOK)
			return out.Item.ID
		}
		fi1ID := regItem(seller1, sku1.ID, 150000)
		fi2ID := regItem(seller2, sku2.ID, 250000)

		buyer := env.registerVerifyAndLogin(t, "buyer-prm-direct")
		env.creditCoins(t, buyer.UserID, 20000, "e2e_direct_r5_init")

		groupID := "e2e-direct-r5-" + uuid.NewString()
		ordCtx, ordCancel := prmFixBearerCtx(context.Background(), promoOrderServiceToken())
		defer ordCancel()

		resResp, err := promoClient.ReservePromotion(ordCtx, &promotionv1.ReservePromotionRequest{
			OrderId: groupID,
			UserId:  buyer.UserID,
			ShopVoucherIds: map[string]string{
				seller1.ShopID: shop1V.ID,
				seller2.ShopID: shop2V.ID,
			},
			PlatformVoucherId: platV.ID,
			CoinsToUse:        10000,
			FlashSaleItems: []*promotionv1.FlashSaleReservationItem{
				{FlashSaleItemId: fi1ID, SkuId: sku1.ID, Quantity: 2, ShopId: seller1.ShopID},
				{FlashSaleItemId: fi2ID, SkuId: sku2.ID, Quantity: 1, ShopId: seller2.ShopID},
			},
			ShopItems: []*promotionv1.ShopDiscountInput{
				{
					ShopId: seller1.ShopID, SubtotalVnd: 400000, ShippingFeeVnd: 20000,
					Items: []*promotionv1.DiscountLineItem{{SkuId: sku1.ID, Quantity: 2, UnitPriceVnd: 200000, FlashSaleItemId: fi1ID}},
				},
				{
					ShopId: seller2.ShopID, SubtotalVnd: 300000, ShippingFeeVnd: 20000,
					Items: []*promotionv1.DiscountLineItem{{SkuId: sku2.ID, Quantity: 1, UnitPriceVnd: 300000, FlashSaleItemId: fi2ID}},
				},
			},
		})
		if err != nil {
			t.Fatalf("ReservePromotion: %v", err)
		}

		var s1Coins, s2Coins int64
		for _, sb := range resResp.GetBreakdown().GetShops() {
			if sb.GetShopId() == seller1.ShopID {
				s1Coins = sb.GetCoinsDiscountVnd()
			}
			if sb.GetShopId() == seller2.ShopID {
				s2Coins = sb.GetCoinsDiscountVnd()
			}
		}

		// Release shop1 twice (idempotency check on live Postgres + Redis)
		aCtx, aCancel := prmFixBearerCtx(context.Background(), adminTok)
		defer aCancel()
		rel1, err := promoClient.ReleasePromotionShop(aCtx, &promotionv1.ReleasePromotionShopRequest{
			OrderId: groupID, ShopId: seller1.ShopID, Reason: "e2e_partial",
		})
		if err != nil {
			t.Fatalf("ReleasePromotionShop(shop1): %v", err)
		}
		if !rel1.Released || rel1.AlreadyReleased || rel1.CoinsReturned != s1Coins ||
			!rel1.ShopVoucherReleased || rel1.FlashSaleQuantityReleased != 2 || rel1.PlatformVoucherReleased {
			t.Fatalf("unexpected ReleasePromotionShop(shop1) response: %+v", rel1)
		}

		rel1Dup, err := promoClient.ReleasePromotionShop(aCtx, &promotionv1.ReleasePromotionShopRequest{
			OrderId: groupID, ShopId: seller1.ShopID, Reason: "e2e_partial_dup",
		})
		if err != nil || !rel1Dup.AlreadyReleased || rel1Dup.CoinsReturned != 0 {
			t.Fatalf("expected idempotent 2nd ReleasePromotionShop(shop1), got %+v err=%v", rel1Dup, err)
		}

		if bal := env.getCoinBalance(t, buyer.AccessToken); bal != 10000+s1Coins {
			t.Fatalf("expected coin balance %d after shop1 release, got %d", 10000+s1Coins, bal)
		}
		if used := prmFixGetVoucherUsedCount(t, env, buyer.AccessToken, "SHOP", shop1V.ID); used != 0 {
			t.Fatalf("expected shop1 voucher used_count=0, got %d", used)
		}
		if used := prmFixGetVoucherUsedCount(t, env, buyer.AccessToken, "PLATFORM", platV.ID); used != 1 {
			t.Fatalf("expected platform voucher used_count still 1, got %d", used)
		}

		// Release shop2 (last shop) -> platform voucher released too
		aCtx2, aCancel2 := prmFixBearerCtx(context.Background(), adminTok)
		defer aCancel2()
		rel2, err := promoClient.ReleasePromotionShop(aCtx2, &promotionv1.ReleasePromotionShopRequest{
			OrderId: groupID, ShopId: seller2.ShopID, Reason: "e2e_partial_last",
		})
		if err != nil {
			t.Fatalf("ReleasePromotionShop(shop2): %v", err)
		}
		if !rel2.Released || rel2.AlreadyReleased || rel2.CoinsReturned != s2Coins ||
			!rel2.ShopVoucherReleased || rel2.FlashSaleQuantityReleased != 1 || !rel2.PlatformVoucherReleased {
			t.Fatalf("unexpected ReleasePromotionShop(shop2) response: %+v", rel2)
		}
		if bal := env.getCoinBalance(t, buyer.AccessToken); bal != 20000 {
			t.Fatalf("expected coin balance fully restored to 20000, got %d", bal)
		}
		if used := prmFixGetVoucherUsedCount(t, env, buyer.AccessToken, "PLATFORM", platV.ID); used != 0 {
			t.Fatalf("expected platform voucher used_count=0 after last shop release, got %d", used)
		}
	})

	t.Run("PRM_B4_DirectGRPC_AuthRequired", func(t *testing.T) {
		anonCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if _, err := promoClient.CreditCoins(anonCtx, &promotionv1.CreditCoinsRequest{UserId: "u1", Amount: 100}); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("anon CreditCoins: want Unauthenticated, got %v", err)
		}
		if _, err := promoClient.SaveVoucher(anonCtx, &promotionv1.SaveVoucherRequest{UserId: "u1", VoucherId: "v1"}); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("anon SaveVoucher: want Unauthenticated, got %v", err)
		}
		if _, err := promoClient.GetCoinBalance(anonCtx, &promotionv1.GetCoinBalanceRequest{UserId: "u1"}); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("anon GetCoinBalance: want Unauthenticated, got %v", err)
		}
		if _, err := promoClient.PreviewDiscounts(anonCtx, &promotionv1.PreviewDiscountsRequest{UserId: "u1"}); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("anon PreviewDiscounts: want Unauthenticated, got %v", err)
		}
		if _, err := promoClient.ReleasePromotionShop(anonCtx, &promotionv1.ReleasePromotionShopRequest{OrderId: "o1", ShopId: "s1"}); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("anon ReleasePromotionShop: want Unauthenticated, got %v", err)
		}

		buyerTok := MintAccessToken(env.cfg.JWTSecret, "e2e-auth-buyer-"+uuid.NewString()[:8], "buyer")
		bCtx, bCancel := prmFixBearerCtx(context.Background(), buyerTok)
		defer bCancel()
		if _, err := promoClient.CreditCoins(bCtx, &promotionv1.CreditCoinsRequest{UserId: "u1", Amount: 100}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("buyer CreditCoins: want PermissionDenied, got %v", err)
		}
		if _, err := promoClient.ReleasePromotionShop(bCtx, &promotionv1.ReleasePromotionShopRequest{OrderId: "o1", ShopId: "s1"}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("buyer ReleasePromotionShop: want PermissionDenied, got %v", err)
		}
	})

	t.Run("PRM_B8_ListAvailableVouchers_ExcludesUnavailable", func(t *testing.T) {
		now := time.Now().UTC()
		createAdminV := func(code string, start, end time.Time, discType string, val int64, pct int32, maxDisc, budget int64) VoucherDTO {
			r := env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/vouchers", adminTok, nil, CreateVoucherRequest{
				Code:            code,
				Name:            "E2E B8 " + code,
				Scope:           "PLATFORM",
				DiscountType:    discType,
				ValueVND:        val,
				DiscountPercent: pct,
				MaxDiscountVND:  maxDisc,
				Quota:           5,
				PerUserLimit:    5,
				BudgetVND:       budget,
				StartsAt:        start.Format(time.RFC3339),
				EndsAt:          end.Format(time.RFC3339),
			}, http.StatusCreated, http.StatusOK)
			var out VoucherResponse
			if err := DecodeJSON(r, &out); err != nil {
				t.Fatalf("decode voucher %s: %v", code, err)
			}
			return out.Voucher
		}

		expiredV := createAdminV(env.uniqueCode("B8EXP"), now.Add(-2*time.Hour), now.Add(-1*time.Hour), "FIXED", 10000, 0, 10000, 50000)
		upcomingV := createAdminV(env.uniqueCode("B8UPC"), now.Add(1*time.Hour), now.Add(2*time.Hour), "FIXED", 10000, 0, 10000, 50000)
		lowBudgetV := createAdminV(env.uniqueCode("B8BUD"), now.Add(-1*time.Hour), now.Add(2*time.Hour), "PERCENT", 0, 10, 15000, 20000)

		// Consume 15k of lowBudgetV's 20k budget -> remaining budget 5k < 15k needed for a 200k order
		buyer1 := env.registerVerifyAndLogin(t, "buyer-b8-1")
		ordCtx, ordCancel := prmFixBearerCtx(context.Background(), promoOrderServiceToken())
		defer ordCancel()
		if _, err := promoClient.ReservePromotion(ordCtx, &promotionv1.ReservePromotionRequest{
			OrderId:           "e2e-b8-bud-" + uuid.NewString(),
			UserId:            buyer1.UserID,
			PlatformVoucherId: lowBudgetV.ID,
			ShopItems:         []*promotionv1.ShopDiscountInput{{ShopId: seller1.ShopID, SubtotalVnd: 200000, ShippingFeeVnd: 20000}},
		}); err != nil {
			t.Fatalf("consume lowBudgetV: %v", err)
		}

		buyer2 := env.registerVerifyAndLogin(t, "buyer-b8-2")
		resp := env.mustDoJSON(t, http.MethodGet, "/api/v1/vouchers?scope=PLATFORM&order_amount_vnd=200000", buyer2.AccessToken, nil, nil, http.StatusOK)
		var list VouchersListResponse
		if err := DecodeJSON(resp, &list); err != nil {
			t.Fatalf("decode list vouchers: %v", err)
		}
		for _, v := range list.Vouchers {
			if v.ID == expiredV.ID || v.ID == upcomingV.ID || v.ID == lowBudgetV.ID {
				t.Fatalf("unavailable voucher %s (%s) must not appear in GET /api/v1/vouchers?order_amount_vnd=200000", v.Code, v.ID)
			}
		}
	})

	t.Run("ReviewA_P1_5_P1_6_P2_11_SellerView_IDOR_And_FreeshipBudget", func(t *testing.T) {
		now := time.Now().UTC()

		// P1-5: Seller creates an expired & upcoming shop voucher -> GET /api/v1/seller/vouchers MUST return them,
		// while GET /api/v1/vouchers?shop_id=... MUST exclude them.
		createSellerV := func(code string, start, end time.Time) VoucherDTO {
			r := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/vouchers", seller1.AccessToken, nil, CreateVoucherRequest{
				Code:           code,
				Name:           "E2E P15 " + code,
				DiscountType:   "FIXED",
				ValueVND:       10000,
				MaxDiscountVND: 10000,
				Quota:          5,
				PerUserLimit:   5,
				BudgetVND:      50000,
				StartsAt:       start.Format(time.RFC3339),
				EndsAt:         end.Format(time.RFC3339),
			}, http.StatusCreated, http.StatusOK)
			var out VoucherResponse
			if err := DecodeJSON(r, &out); err != nil {
				t.Fatalf("decode seller voucher %s: %v", code, err)
			}
			return out.Voucher
		}
		sExp := createSellerV(env.uniqueCode("P15E"), now.Add(-2*time.Hour), now.Add(-1*time.Hour))
		sUpc := createSellerV(env.uniqueCode("P15U"), now.Add(1*time.Hour), now.Add(2*time.Hour))

		sellerListResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/vouchers", seller1.AccessToken, nil, nil, http.StatusOK)
		var sellerList VouchersListResponse
		if err := DecodeJSON(sellerListResp, &sellerList); err != nil {
			t.Fatalf("decode seller vouchers: %v", err)
		}
		foundExp, foundUpc := false, false
		for _, v := range sellerList.Vouchers {
			if v.ID == sExp.ID {
				foundExp = true
			}
			if v.ID == sUpc.ID {
				foundUpc = true
			}
		}
		if !foundExp || !foundUpc {
			t.Fatalf("GET /api/v1/seller/vouchers must include expired & upcoming vouchers (foundExp=%v foundUpc=%v)", foundExp, foundUpc)
		}

		// Public buyer list still excludes them
		pubShopResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/vouchers?scope=SHOP&shop_id="+url.QueryEscape(seller1.ShopID), "", nil, nil, http.StatusOK)
		var pubShopList VouchersListResponse
		if err := DecodeJSON(pubShopResp, &pubShopList); err != nil {
			t.Fatalf("decode public shop vouchers: %v", err)
		}
		for _, v := range pubShopList.Vouchers {
			if v.ID == sExp.ID || v.ID == sUpc.ID {
				t.Fatalf("public GET /api/v1/vouchers must exclude expired/upcoming shop voucher %s", v.Code)
			}
		}

		// P1-6: Direct gRPC ListAvailableVouchers must reject spoofed req.UserId from anon or another buyer
		anonCtx, anonCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer anonCancel()
		if _, err := promoClient.ListAvailableVouchers(anonCtx, &promotionv1.ListAvailableVouchersRequest{
			UserId: "victim-user-id",
			Scope:  promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
		}); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("anon ListAvailableVouchers with user_id: want Unauthenticated, got %v", err)
		}
		buyerTok := MintAccessToken(env.cfg.JWTSecret, "e2e-p16-attacker-"+uuid.NewString()[:8], "buyer")
		bCtx, bCancel := prmFixBearerCtx(context.Background(), buyerTok)
		defer bCancel()
		if _, err := promoClient.ListAvailableVouchers(bCtx, &promotionv1.ListAvailableVouchersRequest{
			UserId: "victim-user-id",
			Scope:  promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
		}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("cross-user ListAvailableVouchers: want PermissionDenied, got %v", err)
		}

		// P2-11: FREESHIP PERCENT 100%, max 30k, budget 50k -> after 30k used (rem=20k > 0), MUST stay listed for 200k order
		fsCreate := env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/vouchers", adminTok, nil, CreateVoucherRequest{
			Code:            env.uniqueCode("P211FS"),
			Name:            "E2E P211 Freeship",
			Scope:           "FREESHIP",
			DiscountType:    "PERCENT",
			DiscountPercent: 100,
			MaxDiscountVND:  30000,
			Quota:           5,
			PerUserLimit:    5,
			BudgetVND:       50000,
			StartsAt:        now.Add(-1 * time.Hour).Format(time.RFC3339),
			EndsAt:          now.Add(2 * time.Hour).Format(time.RFC3339),
		}, http.StatusCreated, http.StatusOK)
		var fsV VoucherResponse
		if err := DecodeJSON(fsCreate, &fsV); err != nil {
			t.Fatalf("decode freeship voucher: %v", err)
		}
		b1 := env.registerVerifyAndLogin(t, "buyer-p211-1")
		ordCtx, ordCancel := prmFixBearerCtx(context.Background(), promoOrderServiceToken())
		defer ordCancel()
		if _, err := promoClient.ReservePromotion(ordCtx, &promotionv1.ReservePromotionRequest{
			OrderId:           "e2e-p211-" + uuid.NewString(),
			UserId:            b1.UserID,
			FreeshipVoucherId: fsV.Voucher.ID,
			ShopItems:         []*promotionv1.ShopDiscountInput{{ShopId: seller1.ShopID, SubtotalVnd: 200000, ShippingFeeVnd: 30000}},
		}); err != nil {
			t.Fatalf("consume freeship budget: %v", err)
		}
		b2 := env.registerVerifyAndLogin(t, "buyer-p211-2")
		fsListResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/vouchers?scope=FREESHIP&order_amount_vnd=200000", b2.AccessToken, nil, nil, http.StatusOK)
		var fsList VouchersListResponse
		if err := DecodeJSON(fsListResp, &fsList); err != nil {
			t.Fatalf("decode freeship list: %v", err)
		}
		foundFS := false
		for _, v := range fsList.Vouchers {
			if v.ID == fsV.Voucher.ID && v.Eligible {
				foundFS = true
			}
		}
		if !foundFS {
			t.Fatalf("FREESHIP voucher %s with remBudget=20k > 0 must remain listed & eligible at order_amount_vnd=200000", fsV.Voucher.Code)
		}
	})
}
