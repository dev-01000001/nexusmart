package service_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	"github.com/nexusmart/core/services/promotion/internal/lua"
)

func TestService_SalesChannel_StorefrontRules(t *testing.T) {
	svc, mem, eng := setupService(t)
	now := time.Now().UTC()
	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerACtx := ctxWithRole("seller-a", "seller", "shop-a")
	sellerBCtx := ctxWithRole("seller-b", "seller", "shop-b")
	buyerCtx := ctxWithRole("buyer-1", "buyer", "")

	// Seed vouchers: shop-a SHOP, shop-b SHOP, PLATFORM, FREESHIP.
	shopAV, err := svc.CreateVoucher(sellerACtx, &promotionv1.CreateVoucherRequest{
		Code:         "SHOPA20K",
		Name:         "Shop A 20k",
		Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP,
		ShopId:       "shop-a",
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
		ValueVnd:     20000,
		MinOrderVnd:  100000,
		Quota:        10,
		PerUserLimit: 5,
		StartsAt:     timestamppb.New(now.Add(-time.Hour)),
		EndsAt:       timestamppb.New(now.Add(time.Hour)),
	})
	if err != nil {
		t.Fatalf("create shop-a voucher: %v", err)
	}
	shopBV, err := svc.CreateVoucher(sellerBCtx, &promotionv1.CreateVoucherRequest{
		Code:         "SHOPB20K",
		Name:         "Shop B 20k",
		Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP,
		ShopId:       "shop-b",
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
		ValueVnd:     20000,
		MinOrderVnd:  100000,
		Quota:        10,
		PerUserLimit: 5,
		StartsAt:     timestamppb.New(now.Add(-time.Hour)),
		EndsAt:       timestamppb.New(now.Add(time.Hour)),
	})
	if err != nil {
		t.Fatalf("create shop-b voucher: %v", err)
	}
	platV, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
		Code:         "PLAT15K",
		Name:         "Platform 15k",
		Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
		ValueVnd:     15000,
		MinOrderVnd:  100000,
		Quota:        10,
		PerUserLimit: 5,
		StartsAt:     timestamppb.New(now.Add(-time.Hour)),
		EndsAt:       timestamppb.New(now.Add(time.Hour)),
	})
	if err != nil {
		t.Fatalf("create platform voucher: %v", err)
	}
	shipV, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
		Code:         "FREE15K",
		Name:         "Freeship 15k",
		Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_FREESHIP,
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
		ValueVnd:     15000,
		MinOrderVnd:  100000,
		Quota:        10,
		PerUserLimit: 5,
		StartsAt:     timestamppb.New(now.Add(-time.Hour)),
		EndsAt:       timestamppb.New(now.Add(time.Hour)),
	})
	if err != nil {
		t.Fatalf("create freeship voucher: %v", err)
	}

	// Seed active flash sales for shop-a and shop-b.
	fsResp, err := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
		Name:     "Channel Flash Sale",
		StartsAt: timestamppb.New(now.Add(-5 * time.Minute)),
		EndsAt:   timestamppb.New(now.Add(2 * time.Hour)),
	})
	if err != nil {
		t.Fatalf("create flash sale: %v", err)
	}
	fsItemA, err := svc.RegisterFlashSaleItem(sellerACtx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId:   fsResp.FlashSale.Id,
		ShopId:        "shop-a",
		SkuId:         "sku-a1",
		FlashPriceVnd: 150000,
		TotalSlots:    10,
		PerUserLimit:  5,
	})
	if err != nil {
		t.Fatalf("register flash sale item A: %v", err)
	}
	if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: fsItemA.Item.Id, Approve: true}); err != nil {
		t.Fatalf("approve flash sale item A: %v", err)
	}
	fsItemB, err := svc.RegisterFlashSaleItem(sellerBCtx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId:   fsResp.FlashSale.Id,
		ShopId:        "shop-b",
		SkuId:         "sku-b1",
		FlashPriceVnd: 140000,
		TotalSlots:    10,
		PerUserLimit:  5,
	})
	if err != nil {
		t.Fatalf("register flash sale item B: %v", err)
	}
	if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: fsItemB.Item.Id, Approve: true}); err != nil {
		t.Fatalf("approve flash sale item B: %v", err)
	}

	// Credit coins to buyer-1.
	if _, err := svc.CreditCoins(adminCtx, &promotionv1.CreditCoinsRequest{
		UserId:      "buyer-1",
		Amount:      25000,
		Reason:      "seed",
		ReferenceId: "sf-coin-seed",
	}); err != nil {
		t.Fatalf("credit coins: %v", err)
	}

	shopAItems := []*promotionv1.ShopDiscountInput{
		{
			ShopId:         "shop-a",
			SubtotalVnd:    200000,
			ShippingFeeVnd: 25000,
			Items: []*promotionv1.DiscountLineItem{
				{SkuId: "sku-a1", Quantity: 1, UnitPriceVnd: 200000, FlashSaleItemId: fsItemA.Item.Id},
			},
		},
	}

	// 1. Channel validation errors on ListAvailableVouchers, PreviewDiscounts, and ReservePromotion.
	if _, err := svc.ListAvailableVouchers(buyerCtx, &promotionv1.ListAvailableVouchersRequest{
		SalesChannel: "UNKNOWN_CHANNEL",
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("ListAvailableVouchers unknown channel: want InvalidArgument, got %v", err)
	}
	if _, err := svc.ListAvailableVouchers(buyerCtx, &promotionv1.ListAvailableVouchersRequest{
		SalesChannel: "STOREFRONT",
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("ListAvailableVouchers STOREFRONT without storefront_shop_id: want InvalidArgument, got %v", err)
	}
	if _, err := svc.PreviewDiscounts(buyerCtx, &promotionv1.PreviewDiscountsRequest{
		SalesChannel:     "MARKETPLACE",
		StorefrontShopId: "shop-a",
		ShopItems:        shopAItems,
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("PreviewDiscounts MARKETPLACE with storefront_shop_id: want InvalidArgument, got %v", err)
	}
	if _, err := svc.PreviewDiscounts(buyerCtx, &promotionv1.PreviewDiscountsRequest{
		SalesChannel: "STOREFRONT",
		ShopItems:    shopAItems,
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("PreviewDiscounts STOREFRONT without storefront_shop_id: want InvalidArgument, got %v", err)
	}
	if _, err := svc.ReservePromotion(buyerCtx, &promotionv1.ReservePromotionRequest{
		OrderId:      "sf-missing-shop",
		SalesChannel: "STOREFRONT",
		ShopItems:    shopAItems,
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("ReservePromotion STOREFRONT without storefront_shop_id: want InvalidArgument, got %v", err)
	}

	// 2. ListAvailableVouchers on STOREFRONT returns ONLY that shop's SHOP vouchers (never PLATFORM, FREESHIP, or other shops).
	sfList, err := svc.ListAvailableVouchers(buyerCtx, &promotionv1.ListAvailableVouchersRequest{
		SalesChannel:     "STOREFRONT",
		StorefrontShopId: "shop-a",
	})
	if err != nil {
		t.Fatalf("ListAvailableVouchers STOREFRONT: %v", err)
	}
	if len(sfList.GetVouchers()) != 1 || sfList.GetVouchers()[0].GetCode() != "SHOPA20K" {
		t.Fatalf("expected only SHOPA20K on shop-a storefront, got %+v", sfList.GetVouchers())
	}
	sfPlatList, err := svc.ListAvailableVouchers(buyerCtx, &promotionv1.ListAvailableVouchersRequest{
		SalesChannel:     "STOREFRONT",
		StorefrontShopId: "shop-a",
		Scope:            promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
	})
	if err != nil || len(sfPlatList.GetVouchers()) != 0 {
		t.Fatalf("expected 0 platform vouchers on STOREFRONT, got %+v err=%v", sfPlatList, err)
	}

	// 3. Rejections on STOREFRONT in BOTH PreviewDiscounts and ReservePromotion.
	rejectCases := []struct {
		name    string
		wantSub string
		preview *promotionv1.PreviewDiscountsRequest
		reserve *promotionv1.ReservePromotionRequest
	}{
		{
			name:    "platform_voucher_field",
			wantSub: "STOREFRONT_VOUCHER_NOT_ALLOWED",
			preview: &promotionv1.PreviewDiscountsRequest{
				SalesChannel:      "STOREFRONT",
				StorefrontShopId:  "shop-a",
				ShopItems:         shopAItems,
				PlatformVoucherId: platV.Voucher.Id,
			},
			reserve: &promotionv1.ReservePromotionRequest{
				OrderId:           "sf-rej-plat",
				SalesChannel:      "STOREFRONT",
				StorefrontShopId:  "shop-a",
				ShopItems:         shopAItems,
				PlatformVoucherId: platV.Voucher.Id,
			},
		},
		{
			name:    "freeship_voucher_field",
			wantSub: "STOREFRONT_VOUCHER_NOT_ALLOWED",
			preview: &promotionv1.PreviewDiscountsRequest{
				SalesChannel:      "STOREFRONT",
				StorefrontShopId:  "shop-a",
				ShopItems:         shopAItems,
				FreeshipVoucherId: shipV.Voucher.Id,
			},
			reserve: &promotionv1.ReservePromotionRequest{
				OrderId:           "sf-rej-ship",
				SalesChannel:      "STOREFRONT",
				StorefrontShopId:  "shop-a",
				ShopItems:         shopAItems,
				FreeshipVoucherId: shipV.Voucher.Id,
			},
		},
		{
			name:    "smuggled_platform_voucher_in_shop_map",
			wantSub: "STOREFRONT_VOUCHER_NOT_ALLOWED",
			preview: &promotionv1.PreviewDiscountsRequest{
				SalesChannel:     "STOREFRONT",
				StorefrontShopId: "shop-a",
				ShopItems:        shopAItems,
				ShopVoucherIds:   map[string]string{"shop-a": platV.Voucher.Id},
			},
			reserve: &promotionv1.ReservePromotionRequest{
				OrderId:          "sf-rej-smuggle-plat",
				SalesChannel:     "STOREFRONT",
				StorefrontShopId: "shop-a",
				ShopItems:        shopAItems,
				ShopVoucherIds:   map[string]string{"shop-a": platV.Voucher.Id},
			},
		},
		{
			name:    "smuggled_freeship_voucher_in_shop_map",
			wantSub: "STOREFRONT_VOUCHER_NOT_ALLOWED",
			preview: &promotionv1.PreviewDiscountsRequest{
				SalesChannel:     "STOREFRONT",
				StorefrontShopId: "shop-a",
				ShopItems:        shopAItems,
				ShopVoucherIds:   map[string]string{"shop-a": shipV.Voucher.Code},
			},
			reserve: &promotionv1.ReservePromotionRequest{
				OrderId:          "sf-rej-smuggle-ship",
				SalesChannel:     "STOREFRONT",
				StorefrontShopId: "shop-a",
				ShopItems:        shopAItems,
				ShopVoucherIds:   map[string]string{"shop-a": shipV.Voucher.Code},
			},
		},
		{
			name:    "other_shop_voucher_id_under_own_key",
			wantSub: "STOREFRONT_VOUCHER_SHOP_MISMATCH",
			preview: &promotionv1.PreviewDiscountsRequest{
				SalesChannel:     "STOREFRONT",
				StorefrontShopId: "shop-a",
				ShopItems:        shopAItems,
				ShopVoucherIds:   map[string]string{"shop-a": shopBV.Voucher.Id},
			},
			reserve: &promotionv1.ReservePromotionRequest{
				OrderId:          "sf-rej-shopb-vid",
				SalesChannel:     "STOREFRONT",
				StorefrontShopId: "shop-a",
				ShopItems:        shopAItems,
				ShopVoucherIds:   map[string]string{"shop-a": shopBV.Voucher.Id},
			},
		},
		{
			name:    "other_shop_voucher_map_key",
			wantSub: "STOREFRONT_VOUCHER_SHOP_MISMATCH",
			preview: &promotionv1.PreviewDiscountsRequest{
				SalesChannel:     "STOREFRONT",
				StorefrontShopId: "shop-a",
				ShopItems:        shopAItems,
				ShopVoucherIds:   map[string]string{"shop-b": shopBV.Voucher.Id},
			},
			reserve: &promotionv1.ReservePromotionRequest{
				OrderId:          "sf-rej-shopb-key",
				SalesChannel:     "STOREFRONT",
				StorefrontShopId: "shop-a",
				ShopItems:        shopAItems,
				ShopVoucherIds:   map[string]string{"shop-b": shopBV.Voucher.Id},
			},
		},
		{
			name:    "platform_coins",
			wantSub: "STOREFRONT_COINS_NOT_ALLOWED",
			preview: &promotionv1.PreviewDiscountsRequest{
				SalesChannel:     "STOREFRONT",
				StorefrontShopId: "shop-a",
				ShopItems:        shopAItems,
				CoinsToUse:       5000,
			},
			reserve: &promotionv1.ReservePromotionRequest{
				OrderId:          "sf-rej-coins",
				SalesChannel:     "STOREFRONT",
				StorefrontShopId: "shop-a",
				ShopItems:        shopAItems,
				CoinsToUse:       5000,
			},
		},
		{
			name:    "other_shop_item",
			wantSub: "STOREFRONT_SHOP_MISMATCH",
			preview: &promotionv1.PreviewDiscountsRequest{
				SalesChannel:     "STOREFRONT",
				StorefrontShopId: "shop-a",
				ShopItems: []*promotionv1.ShopDiscountInput{
					{ShopId: "shop-b", SubtotalVnd: 200000, ShippingFeeVnd: 20000},
				},
			},
			reserve: &promotionv1.ReservePromotionRequest{
				OrderId:          "sf-rej-item-shop",
				SalesChannel:     "STOREFRONT",
				StorefrontShopId: "shop-a",
				ShopItems: []*promotionv1.ShopDiscountInput{
					{ShopId: "shop-b", SubtotalVnd: 200000, ShippingFeeVnd: 20000},
				},
			},
		},
		{
			name:    "other_shop_flash_sale_item",
			wantSub: "STOREFRONT_FLASH_SALE_SHOP_MISMATCH",
			preview: &promotionv1.PreviewDiscountsRequest{
				SalesChannel:     "STOREFRONT",
				StorefrontShopId: "shop-a",
				ShopItems: []*promotionv1.ShopDiscountInput{
					{
						ShopId:         "shop-a",
						SubtotalVnd:    200000,
						ShippingFeeVnd: 20000,
						Items: []*promotionv1.DiscountLineItem{
							{SkuId: "sku-b1", Quantity: 1, UnitPriceVnd: 200000, FlashSaleItemId: fsItemB.Item.Id},
						},
					},
				},
			},
			reserve: &promotionv1.ReservePromotionRequest{
				OrderId:          "sf-rej-flash-shop",
				SalesChannel:     "STOREFRONT",
				StorefrontShopId: "shop-a",
				FlashSaleItems: []*promotionv1.FlashSaleReservationItem{
					{FlashSaleItemId: fsItemB.Item.Id, SkuId: "sku-b1", Quantity: 1},
				},
			},
		},
	}

	for _, tc := range rejectCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.PreviewDiscounts(buyerCtx, tc.preview); status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("PreviewDiscounts(%s): want InvalidArgument containing %q, got %v", tc.name, tc.wantSub, err)
			}
			if _, err := svc.ReservePromotion(buyerCtx, tc.reserve); status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("ReservePromotion(%s): want InvalidArgument containing %q, got %v", tc.name, tc.wantSub, err)
			}
		})
	}

	// Verify NO quotas, slots, or coins were mutated by any rejected STOREFRONT calls.
	if mem.AccountBalance("buyer-1") != 25000 {
		t.Fatalf("buyer-1 coins must stay 25000, got %d", mem.AccountBalance("buyer-1"))
	}
	if rem, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(platV.Voucher.Id)); rem != 10 {
		t.Fatalf("platform voucher quota must stay 10, got %d", rem)
	}
	if rem, _, _ := eng.GetInt64(context.Background(), lua.FlashRemainingKey(fsItemB.Item.Id)); rem != 10 {
		t.Fatalf("shop-b flash sale slots must stay 10, got %d", rem)
	}

	// 4. Valid STOREFRONT checkout: shop-a SHOP voucher + shop-a flash sale item SUCCEEDS!
	// Subtotal 200k -> Flash sale (-50k, to 150k) -> Shop A voucher (-20k, to 130k) + Shipping 25k = 155k.
	prevResp, err := svc.PreviewDiscounts(buyerCtx, &promotionv1.PreviewDiscountsRequest{
		SalesChannel:     "STOREFRONT",
		StorefrontShopId: "shop-a",
		ShopItems:        shopAItems,
		ShopVoucherIds:   map[string]string{"shop-a": shopAV.Voucher.Id},
	})
	if err != nil {
		t.Fatalf("valid STOREFRONT PreviewDiscounts failed: %v", err)
	}
	if bd := prevResp.GetBreakdown(); bd.GetTotalFlashSaleDiscountVnd() != 50000 ||
		bd.GetTotalShopVoucherDiscountVnd() != 20000 ||
		bd.GetPlatformCostVnd() != 0 ||
		bd.GetShopCostVnd() != 70000 ||
		bd.GetFinalPayableVnd() != 155000 {
		t.Fatalf("unexpected STOREFRONT preview breakdown: %+v", bd)
	}

	resResp, err := svc.ReservePromotion(buyerCtx, &promotionv1.ReservePromotionRequest{
		OrderId:          "sf-valid-ord-1",
		SalesChannel:     "STOREFRONT",
		StorefrontShopId: "shop-a",
		ShopItems:        shopAItems,
		ShopVoucherIds:   map[string]string{"shop-a": shopAV.Voucher.Id},
	})
	if err != nil || !resResp.GetReserved() {
		t.Fatalf("valid STOREFRONT ReservePromotion failed: resp=%+v err=%v", resResp, err)
	}
	if bd := resResp.GetBreakdown(); bd.GetFinalPayableVnd() != 155000 || bd.GetPlatformCostVnd() != 0 || bd.GetShopCostVnd() != 70000 {
		t.Fatalf("unexpected STOREFRONT reserve breakdown: %+v", bd)
	}
	if rem, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(shopAV.Voucher.Id)); rem != 9 {
		t.Fatalf("expected shop-a voucher quota decremented to 9, got %d", rem)
	}
	if rem, _, _ := eng.GetInt64(context.Background(), lua.FlashRemainingKey(fsItemA.Item.Id)); rem != 9 {
		t.Fatalf("expected shop-a flash sale slots decremented to 9, got %d", rem)
	}

	// 5. MARKETPLACE (default empty sales_channel) still allows platform + freeship + coins + shop voucher!
	mpResp, err := svc.ReservePromotion(buyerCtx, &promotionv1.ReservePromotionRequest{
		OrderId:           "mp-valid-ord-1",
		ShopItems:         shopAItems,
		ShopVoucherIds:    map[string]string{"shop-a": shopAV.Voucher.Id},
		PlatformVoucherId: platV.Voucher.Id,
		FreeshipVoucherId: shipV.Voucher.Id,
		CoinsToUse:        5000,
	})
	if err != nil || !mpResp.GetReserved() {
		t.Fatalf("default MARKETPLACE ReservePromotion failed: resp=%+v err=%v", mpResp, err)
	}
	if bd := mpResp.GetBreakdown(); bd.GetTotalPlatformVoucherDiscountVnd() != 15000 ||
		bd.GetTotalFreeshipDiscountVnd() != 15000 ||
		bd.GetTotalCoinsDiscountVnd() != 5000 ||
		bd.GetPlatformCostVnd() != 35000 {
		t.Fatalf("unexpected MARKETPLACE reserve breakdown: %+v", bd)
	}
}
