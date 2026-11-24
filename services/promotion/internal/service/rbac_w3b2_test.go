package service_test

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
	"google.golang.org/protobuf/types/known/timestamppb"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	"github.com/nexusmart/core/pkg/common/auth"
)

func allRoleContexts() []struct {
	name string
	ctx  context.Context
} {
	return []struct {
		name string
		ctx  context.Context
	}{
		{auth.RoleSuperAdmin, ctxWithRole("staff-super", auth.RoleSuperAdmin, "")},
		{auth.RoleAdmin, ctxWithRole("staff-admin", auth.RoleAdmin, "")},
		{auth.RoleModerator, ctxWithRole("staff-mod", auth.RoleModerator, "")},
		{auth.RoleCSKH, ctxWithRole("staff-cskh", auth.RoleCSKH, "")},
		{auth.RoleAccountant, ctxWithRole("staff-acc", auth.RoleAccountant, "")},
		{auth.RoleOps, ctxWithRole("staff-ops", auth.RoleOps, "")},
		{auth.RoleBuyer, ctxWithRole("buyer-rbac", auth.RoleBuyer, "")},
		{auth.RoleSeller, ctxWithRole("seller-rbac", auth.RoleSeller, "shop-1")},
		{"service_token", ctxWithService("order-service")},
		{"anonymous", context.Background()},
	}
}

func roleSet(roles ...string) map[string]bool {
	out := make(map[string]bool, len(roles))
	for _, r := range roles {
		out[r] = true
	}
	return out
}

func expectRBACCode(t *testing.T, op, caller string, allowed map[string]bool, err error) {
	t.Helper()
	if caller == "anonymous" {
		if status.Code(err) != codes.Unauthenticated {
			t.Fatalf("%s as %s: want Unauthenticated, got %v", op, caller, err)
		}
		return
	}
	if allowed[caller] {
		if err != nil {
			t.Fatalf("%s as %s: want OK, got %v", op, caller, err)
		}
		return
	}
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("%s as %s: want PermissionDenied, got %v", op, caller, err)
	}
}

func TestService_W3B2_AdminRBACMatrix(t *testing.T) {
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)

	t.Run("ListAvailableVouchers_AdminView", func(t *testing.T) {
		svc, _, _ := setupService(t)
		svc.SetClock(func() time.Time { return now })

		adminCtx := ctxWithRole("seed-admin", auth.RoleAdmin, "")
		seller1Ctx := ctxWithRole("seed-seller1", auth.RoleSeller, "shop-1")
		buyer1Ctx := ctxWithRole("buyer-1", auth.RoleBuyer, "")

		// Seed active + paused platform vouchers and a paused shop-1 voucher.
		vPlatActive, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
			Code: "W3B2_PLAT_ACT", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000,
			Quota: 10, PerUserLimit: 2, StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
		})
		if err != nil {
			t.Fatalf("seed active platform voucher: %v", err)
		}
		vPlatPaused, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
			Code: "W3B2_PLAT_PSD", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 15000,
			Quota: 10, PerUserLimit: 2, StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
		})
		if err != nil {
			t.Fatalf("seed paused platform voucher: %v", err)
		}
		if _, err := svc.SetVoucherStatus(adminCtx, &promotionv1.SetVoucherStatusRequest{
			VoucherId: vPlatPaused.Voucher.Id, Status: promotionv1.VoucherStatus_VOUCHER_STATUS_PAUSED,
		}); err != nil {
			t.Fatalf("pause platform voucher: %v", err)
		}
		vShopPaused, err := svc.CreateVoucher(seller1Ctx, &promotionv1.CreateVoucherRequest{
			Code: "W3B2_SHOP_PSD", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP, ShopId: "shop-1",
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 12000,
			Quota: 10, PerUserLimit: 2, StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
		})
		if err != nil {
			t.Fatalf("seed shop voucher: %v", err)
		}
		if _, err := svc.SetVoucherStatus(seller1Ctx, &promotionv1.SetVoucherStatusRequest{
			VoucherId: vShopPaused.Voucher.Id, Status: promotionv1.VoucherStatus_VOUCHER_STATUS_PAUSED,
		}); err != nil {
			t.Fatalf("pause shop voucher: %v", err)
		}
		if _, err := svc.SaveVoucher(buyer1Ctx, &promotionv1.SaveVoucherRequest{VoucherId: vPlatActive.Voucher.Id}); err != nil {
			t.Fatalf("buyer-1 SaveVoucher: %v", err)
		}

		allowed := roleSet(auth.RoleSuperAdmin, auth.RoleAdmin, auth.RoleCSKH, auth.RoleAccountant, auth.RoleOps)

		for _, tc := range allRoleContexts() {
			// (a) Admin mode via include_inactive=true across all vouchers
			resp, err := svc.ListAvailableVouchers(tc.ctx, &promotionv1.ListAvailableVouchersRequest{
				IncludeInactive: true,
			})
			expectRBACCode(t, "ListAvailableVouchers(include_inactive=true)", tc.name, allowed, err)
			if allowed[tc.name] {
				codesSeen := make(map[string]bool, len(resp.GetVouchers()))
				for _, v := range resp.GetVouchers() {
					codesSeen[v.GetCode()] = true
				}
				if !codesSeen["W3B2_PLAT_ACT"] || !codesSeen["W3B2_PLAT_PSD"] || !codesSeen["W3B2_SHOP_PSD"] {
					t.Fatalf("ListAvailableVouchers(include_inactive=true) as %s missing expected vouchers: got %v", tc.name, codesSeen)
				}
			}

			// (b) Admin mode via status=PAUSED filter
			respPaused, err := svc.ListAvailableVouchers(tc.ctx, &promotionv1.ListAvailableVouchersRequest{
				Status: promotionv1.VoucherStatus_VOUCHER_STATUS_PAUSED,
			})
			expectRBACCode(t, "ListAvailableVouchers(status=PAUSED)", tc.name, allowed, err)
			if allowed[tc.name] && len(respPaused.GetVouchers()) != 2 {
				t.Fatalf("ListAvailableVouchers(status=PAUSED) as %s: want 2 paused vouchers, got %d", tc.name, len(respPaused.GetVouchers()))
			}

			// (c) Querying another user's wallet state requires CapVouchersRead (or service token)
			if tc.name != "service_token" {
				respUser, err := svc.ListAvailableVouchers(tc.ctx, &promotionv1.ListAvailableVouchersRequest{
					UserId: "buyer-1",
					Scope:  promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
				})
				expectRBACCode(t, "ListAvailableVouchers(user_id=buyer-1)", tc.name, allowed, err)
				if allowed[tc.name] && (len(respUser.GetVouchers()) != 1 || !respUser.GetVouchers()[0].GetSaved()) {
					t.Fatalf("ListAvailableVouchers(user_id=buyer-1) as %s: expected saved active voucher, got %+v", tc.name, respUser.GetVouchers())
				}
			}
		}

		// Seller own-shop manage view with include_inactive=true & status=PAUSED must remain intact.
		sellerOwn, err := svc.ListAvailableVouchers(seller1Ctx, &promotionv1.ListAvailableVouchersRequest{
			ShopId:          "shop-1",
			Scope:           promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP,
			IncludeInactive: true,
			Status:          promotionv1.VoucherStatus_VOUCHER_STATUS_PAUSED,
		})
		if err != nil || len(sellerOwn.GetVouchers()) != 1 || sellerOwn.GetVouchers()[0].GetCode() != "W3B2_SHOP_PSD" {
			t.Fatalf("seller own-shop manage ListAvailableVouchers failed: resp=%+v err=%v", sellerOwn, err)
		}
		// Seller requesting another shop's inactive vouchers must be rejected.
		if _, err := svc.ListAvailableVouchers(seller1Ctx, &promotionv1.ListAvailableVouchersRequest{
			ShopId:          "shop-2",
			Scope:           promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP,
			IncludeInactive: true,
		}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("seller cross-shop include_inactive: want PermissionDenied, got %v", err)
		}
		// Public storefront query (include_inactive=false, status=UNSPECIFIED) still works for moderator/buyer/anon.
		for _, pubCtx := range []context.Context{
			ctxWithRole("mod-pub", auth.RoleModerator, ""),
			ctxWithRole("buyer-pub", auth.RoleBuyer, ""),
			context.Background(),
		} {
			pubResp, err := svc.ListAvailableVouchers(pubCtx, &promotionv1.ListAvailableVouchersRequest{
				Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
			})
			if err != nil || len(pubResp.GetVouchers()) != 1 || pubResp.GetVouchers()[0].GetCode() != "W3B2_PLAT_ACT" {
				t.Fatalf("public ListAvailableVouchers unexpected: resp=%+v err=%v", pubResp, err)
			}
		}
	})

	t.Run("CreateUpdatePauseDeleteVoucher_AdminAndSeller", func(t *testing.T) {
		svc, _, _ := setupService(t)
		svc.SetClock(func() time.Time { return now })

		allowed := roleSet(auth.RoleSuperAdmin, auth.RoleAdmin, auth.RoleOps)

		for idx, tc := range allRoleContexts() {
			// Create PLATFORM voucher
			_, err := svc.CreateVoucher(tc.ctx, &promotionv1.CreateVoucherRequest{
				Code:         fmt.Sprintf("W3B2_CV_P_%d", idx),
				Name:         "Plat Voucher",
				Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
				DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
				ValueVnd:     10000,
				Quota:        10,
				PerUserLimit: 1,
				StartsAt:     timestamppb.New(now.Add(-time.Hour)),
				EndsAt:       timestamppb.New(now.Add(2 * time.Hour)),
			})
			expectRBACCode(t, "CreateVoucher(PLATFORM)", tc.name, allowed, err)

			// Create FREESHIP voucher
			created, err := svc.CreateVoucher(tc.ctx, &promotionv1.CreateVoucherRequest{
				Code:         fmt.Sprintf("W3B2_CV_F_%d", idx),
				Name:         "Freeship Voucher",
				Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_FREESHIP,
				DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
				ValueVnd:     15000,
				Quota:        10,
				PerUserLimit: 1,
				StartsAt:     timestamppb.New(now.Add(-time.Hour)),
				EndsAt:       timestamppb.New(now.Add(2 * time.Hour)),
			})
			expectRBACCode(t, "CreateVoucher(FREESHIP)", tc.name, allowed, err)

			// Ops/Admin/SuperAdmin must NOT create SHOP vouchers
			if allowed[tc.name] {
				if _, sErr := svc.CreateVoucher(tc.ctx, &promotionv1.CreateVoucherRequest{
					Code:         fmt.Sprintf("W3B2_CV_S_%d", idx),
					Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP,
					ShopId:       "shop-1",
					DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
					ValueVnd:     10000,
					Quota:        5,
					StartsAt:     timestamppb.New(now.Add(-time.Hour)),
					EndsAt:       timestamppb.New(now.Add(2 * time.Hour)),
				}); status.Code(sErr) != codes.PermissionDenied {
					t.Fatalf("CreateVoucher(SHOP) as %s: want PermissionDenied, got %v", tc.name, sErr)
				}
			}
			_ = created
		}

		// Seed a platform voucher and a shop-1 voucher for Update/SetStatus/Delete matrix.
		adminCtx := ctxWithRole("seed-admin", auth.RoleAdmin, "")
		seller1Ctx := ctxWithRole("seller-1", auth.RoleSeller, "shop-1")

		seedPlat := func(suffix string) string {
			v, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
				Code: "W3B2_MUT_" + suffix, Name: "Mutable", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
				DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000, Quota: 10, PerUserLimit: 1,
				StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
			})
			if err != nil {
				t.Fatalf("seedPlat(%s): %v", suffix, err)
			}
			return v.Voucher.Id
		}

		shopV, err := svc.CreateVoucher(seller1Ctx, &promotionv1.CreateVoucherRequest{
			Code: "W3B2_SHOP_OWN", Name: "Shop Own", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP, ShopId: "shop-1",
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000, Quota: 10, PerUserLimit: 1,
			StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
		})
		if err != nil {
			t.Fatalf("seller CreateVoucher(SHOP): %v", err)
		}

		for idx, tc := range allRoleContexts() {
			vid := seedPlat(fmt.Sprintf("%d", idx))

			_, uErr := svc.UpdateVoucher(tc.ctx, &promotionv1.UpdateVoucherRequest{
				VoucherId: vid,
				Name:      strPtr("Updated by " + tc.name),
			})
			expectRBACCode(t, "UpdateVoucher(PLATFORM)", tc.name, allowed, uErr)

			_, sErr := svc.SetVoucherStatus(tc.ctx, &promotionv1.SetVoucherStatusRequest{
				VoucherId: vid,
				Status:    promotionv1.VoucherStatus_VOUCHER_STATUS_PAUSED,
			})
			expectRBACCode(t, "SetVoucherStatus(PLATFORM)", tc.name, allowed, sErr)

			_, dErr := svc.DeleteVoucher(tc.ctx, &promotionv1.DeleteVoucherRequest{
				VoucherId: vid,
			})
			expectRBACCode(t, "DeleteVoucher(PLATFORM)", tc.name, allowed, dErr)

			// Non-existent voucher ID: unauthorized non-seller roles must get PermissionDenied (no NotFound oracle).
			if !allowed[tc.name] && tc.name != auth.RoleSeller && tc.name != "anonymous" {
				if _, err := svc.UpdateVoucher(tc.ctx, &promotionv1.UpdateVoucherRequest{VoucherId: "nonexistent-id", Name: strPtr("x")}); status.Code(err) != codes.PermissionDenied {
					t.Fatalf("UpdateVoucher(nonexistent) as %s: want PermissionDenied, got %v", tc.name, err)
				}
				if _, err := svc.SetVoucherStatus(tc.ctx, &promotionv1.SetVoucherStatusRequest{VoucherId: "nonexistent-id", Status: promotionv1.VoucherStatus_VOUCHER_STATUS_PAUSED}); status.Code(err) != codes.PermissionDenied {
					t.Fatalf("SetVoucherStatus(nonexistent) as %s: want PermissionDenied, got %v", tc.name, err)
				}
				if _, err := svc.DeleteVoucher(tc.ctx, &promotionv1.DeleteVoucherRequest{VoucherId: "nonexistent-id"}); status.Code(err) != codes.PermissionDenied {
					t.Fatalf("DeleteVoucher(nonexistent) as %s: want PermissionDenied, got %v", tc.name, err)
				}
			}

			// Even ops/admin/super_admin cannot mutate a seller's SHOP voucher.
			if allowed[tc.name] {
				if _, err := svc.UpdateVoucher(tc.ctx, &promotionv1.UpdateVoucherRequest{VoucherId: shopV.Voucher.Id, Name: strPtr("hijack")}); status.Code(err) != codes.PermissionDenied {
					t.Fatalf("UpdateVoucher(SHOP) as %s: want PermissionDenied, got %v", tc.name, err)
				}
				if _, err := svc.SetVoucherStatus(tc.ctx, &promotionv1.SetVoucherStatusRequest{VoucherId: shopV.Voucher.Id, Status: promotionv1.VoucherStatus_VOUCHER_STATUS_PAUSED}); status.Code(err) != codes.PermissionDenied {
					t.Fatalf("SetVoucherStatus(SHOP) as %s: want PermissionDenied, got %v", tc.name, err)
				}
				if _, err := svc.DeleteVoucher(tc.ctx, &promotionv1.DeleteVoucherRequest{VoucherId: shopV.Voucher.Id}); status.Code(err) != codes.PermissionDenied {
					t.Fatalf("DeleteVoucher(SHOP) as %s: want PermissionDenied, got %v", tc.name, err)
				}
			}
		}

		// Seller retains full Update/SetStatus/Delete rights on own SHOP voucher.
		if _, err := svc.UpdateVoucher(seller1Ctx, &promotionv1.UpdateVoucherRequest{VoucherId: shopV.Voucher.Id, Name: strPtr("Renamed By Owner")}); err != nil {
			t.Fatalf("seller UpdateVoucher(own SHOP): %v", err)
		}
		if _, err := svc.SetVoucherStatus(seller1Ctx, &promotionv1.SetVoucherStatusRequest{VoucherId: shopV.Voucher.Id, Status: promotionv1.VoucherStatus_VOUCHER_STATUS_PAUSED}); err != nil {
			t.Fatalf("seller SetVoucherStatus(own SHOP): %v", err)
		}
		if _, err := svc.DeleteVoucher(seller1Ctx, &promotionv1.DeleteVoucherRequest{VoucherId: shopV.Voucher.Id}); err != nil {
			t.Fatalf("seller DeleteVoucher(own SHOP): %v", err)
		}
	})

	t.Run("FlashSales_ListCreateRegisterApprove", func(t *testing.T) {
		svc, _, _ := setupService(t)
		svc.SetClock(func() time.Time { return now })
		pc := newFakeProductClient()
		pc.setSKU(&productv1.SKU{Id: "sku-fs-s1", ShopId: "shop-1", CategoryId: "cat-1", PriceVnd: 200000, MinPrice_30DVnd: 200000, AvailableStock: 50, IsActive: true})
		pc.setSKU(&productv1.SKU{Id: "sku-fs-s2", ShopId: "shop-2", CategoryId: "cat-1", PriceVnd: 200000, MinPrice_30DVnd: 200000, AvailableStock: 50, IsActive: true})
		svc.SetProductClient(pc)

		adminCtx := ctxWithRole("seed-admin", auth.RoleAdmin, "")
		seller1Ctx := ctxWithRole("seller-1", auth.RoleSeller, "shop-1")
		seller2Ctx := ctxWithRole("seller-2", auth.RoleSeller, "shop-2")

		// Seed flash sale campaign + items for shop-1 and shop-2.
		seedFS, err := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
			Name: "Seed Flash", StartsAt: timestamppb.New(now.Add(time.Hour)), EndsAt: timestamppb.New(now.Add(3 * time.Hour)),
		})
		if err != nil {
			t.Fatalf("seed CreateFlashSale: %v", err)
		}
		if _, err := svc.RegisterFlashSaleItem(seller1Ctx, &promotionv1.RegisterFlashSaleItemRequest{
			FlashSaleId: seedFS.FlashSale.Id, ShopId: "shop-1", SkuId: "sku-fs-s1", FlashPriceVnd: 150000, TotalSlots: 10, PerUserLimit: 1,
		}); err != nil {
			t.Fatalf("seed shop-1 RegisterFlashSaleItem: %v", err)
		}
		if _, err := svc.RegisterFlashSaleItem(seller2Ctx, &promotionv1.RegisterFlashSaleItemRequest{
			FlashSaleId: seedFS.FlashSale.Id, ShopId: "shop-2", SkuId: "sku-fs-s2", FlashPriceVnd: 150000, TotalSlots: 10, PerUserLimit: 1,
		}); err != nil {
			t.Fatalf("seed shop-2 RegisterFlashSaleItem: %v", err)
		}

		// 1. CreateFlashSale: CapFlashSalesWrite (super_admin, admin, ops) ONLY — moderator MUST be denied.
		createFSAllowed := roleSet(auth.RoleSuperAdmin, auth.RoleAdmin, auth.RoleOps)
		for idx, tc := range allRoleContexts() {
			_, err := svc.CreateFlashSale(tc.ctx, &promotionv1.CreateFlashSaleRequest{
				Name:     fmt.Sprintf("FS-%d-%s", idx, tc.name),
				StartsAt: timestamppb.New(now.Add(time.Duration(idx+10) * time.Hour)),
				EndsAt:   timestamppb.New(now.Add(time.Duration(idx+11) * time.Hour)),
			})
			expectRBACCode(t, "CreateFlashSale", tc.name, createFSAllowed, err)
		}

		// 2. ListAllFlashSales: CapFlashSalesRead (super_admin, admin, moderator, ops) + seller.
		listAllFSAllowed := roleSet(auth.RoleSuperAdmin, auth.RoleAdmin, auth.RoleModerator, auth.RoleOps, auth.RoleSeller)
		for _, tc := range allRoleContexts() {
			resp, err := svc.ListAllFlashSales(tc.ctx, &promotionv1.ListAllFlashSalesRequest{})
			expectRBACCode(t, "ListAllFlashSales", tc.name, listAllFSAllowed, err)
			if listAllFSAllowed[tc.name] && resp.GetTotal() == 0 {
				t.Fatalf("ListAllFlashSales as %s: expected campaigns, got 0", tc.name)
			}
		}

		// 3. ListFlashSaleItems (admin cross-shop view for shop-2): CapFlashSalesRead ONLY (seller of shop-1 denied for shop-2!).
		listItemsAdminAllowed := roleSet(auth.RoleSuperAdmin, auth.RoleAdmin, auth.RoleModerator, auth.RoleOps)
		for _, tc := range allRoleContexts() {
			resp, err := svc.ListFlashSaleItems(tc.ctx, &promotionv1.ListFlashSaleItemsRequest{ShopId: "shop-2"})
			expectRBACCode(t, "ListFlashSaleItems(shop-2)", tc.name, listItemsAdminAllowed, err)
			if listItemsAdminAllowed[tc.name] && len(resp.GetItems()) != 1 {
				t.Fatalf("ListFlashSaleItems(shop-2) as %s: want 1 item, got %d", tc.name, len(resp.GetItems()))
			}
		}
		// Seller still views own shop's flash sale items (both with explicit shop_id and empty shop_id clamped to claims.ShopID).
		s1Items, err := svc.ListFlashSaleItems(seller1Ctx, &promotionv1.ListFlashSaleItemsRequest{})
		if err != nil || len(s1Items.GetItems()) != 1 || s1Items.GetItems()[0].GetShopId() != "shop-1" {
			t.Fatalf("seller ListFlashSaleItems(own): resp=%+v err=%v", s1Items, err)
		}

		// 4. RegisterFlashSaleItem: CapFlashSalesWrite (super_admin, admin, ops) + seller (own shop).
		regAllowed := roleSet(auth.RoleSuperAdmin, auth.RoleAdmin, auth.RoleOps, auth.RoleSeller)
		for idx, tc := range allRoleContexts() {
			fs, err := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
				Name:     fmt.Sprintf("RegFS-%d", idx),
				StartsAt: timestamppb.New(now.Add(time.Duration(30+idx*3) * time.Hour)),
				EndsAt:   timestamppb.New(now.Add(time.Duration(31+idx*3) * time.Hour)),
			})
			if err != nil {
				t.Fatalf("seed reg FS: %v", err)
			}
			_, err = svc.RegisterFlashSaleItem(tc.ctx, &promotionv1.RegisterFlashSaleItemRequest{
				FlashSaleId:   fs.FlashSale.Id,
				ShopId:        "shop-1",
				SkuId:         "sku-fs-s1",
				FlashPriceVnd: 150000,
				TotalSlots:    5,
				PerUserLimit:  1,
			})
			expectRBACCode(t, "RegisterFlashSaleItem", tc.name, regAllowed, err)
		}

		// 5. ApproveFlashSaleItem: CapFlashSalesItemApprove (super_admin, admin, moderator, ops).
		approveAllowed := roleSet(auth.RoleSuperAdmin, auth.RoleAdmin, auth.RoleModerator, auth.RoleOps)
		for idx, tc := range allRoleContexts() {
			fs, err := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
				Name:     fmt.Sprintf("AppFS-%d", idx),
				StartsAt: timestamppb.New(now.Add(time.Duration(80+idx*3) * time.Hour)),
				EndsAt:   timestamppb.New(now.Add(time.Duration(81+idx*3) * time.Hour)),
			})
			if err != nil {
				t.Fatalf("seed app FS: %v", err)
			}
			reg, err := svc.RegisterFlashSaleItem(seller1Ctx, &promotionv1.RegisterFlashSaleItemRequest{
				FlashSaleId:   fs.FlashSale.Id,
				ShopId:        "shop-1",
				SkuId:         "sku-fs-s1",
				FlashPriceVnd: 150000,
				TotalSlots:    5,
				PerUserLimit:  1,
			})
			if err != nil {
				t.Fatalf("seed app item: %v", err)
			}
			_, err = svc.ApproveFlashSaleItem(tc.ctx, &promotionv1.ApproveFlashSaleItemRequest{
				ItemId:  reg.Item.Id,
				Approve: true,
			})
			expectRBACCode(t, "ApproveFlashSaleItem", tc.name, approveAllowed, err)
		}
	})

	t.Run("CreditCoins_F9Bis_AndServiceToken", func(t *testing.T) {
		svc, mem, _ := setupService(t)
		svc.SetClock(func() time.Time { return now })

		// F9-bis: ONLY super_admin, admin, accountant (+ internal service token) may credit coins.
		// cskh, ops, moderator, buyer, seller MUST be rejected with PermissionDenied!
		allowed := roleSet(auth.RoleSuperAdmin, auth.RoleAdmin, auth.RoleAccountant, "service_token")

		var expectedTotal int64
		for idx, tc := range allRoleContexts() {
			_, err := svc.CreditCoins(tc.ctx, &promotionv1.CreditCoinsRequest{
				UserId:      "target-buyer",
				Amount:      1000,
				Reason:      "test_" + tc.name,
				ReferenceId: fmt.Sprintf("w3b2-coin-%d", idx),
			})
			expectRBACCode(t, "CreditCoins", tc.name, allowed, err)
			if allowed[tc.name] {
				expectedTotal += 1000
			}
		}
		if got := mem.AccountBalance("target-buyer"); got != expectedTotal {
			t.Fatalf("unauthorized CreditCoins mutated balance: got %d, want %d", got, expectedTotal)
		}
	})

	t.Run("ReserveAndRelease_InvariantsPreserved", func(t *testing.T) {
		svc, _, _ := setupService(t)
		svc.SetClock(func() time.Time { return now })

		// ReservePromotion (D3): strictly order-service token ONLY; all 6 staff roles + buyer + seller + other service denied.
		reserveAllowed := roleSet("service_token")
		for idx, tc := range allRoleContexts() {
			_, err := svc.ReservePromotion(tc.ctx, &promotionv1.ReservePromotionRequest{
				OrderId:   fmt.Sprintf("w3b2-res-%d", idx),
				UserId:    "buyer-1",
				ShopItems: []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
			})
			expectRBACCode(t, "ReservePromotion", tc.name, reserveAllowed, err)
		}
		if _, err := svc.ReservePromotion(ctxWithService("payment-service"), &promotionv1.ReservePromotionRequest{
			OrderId:   "w3b2-res-other-svc",
			UserId:    "buyer-1",
			ShopItems: []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
		}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("ReservePromotion as payment-service: want PermissionDenied, got %v", err)
		}

		// ReleasePromotion & ReleasePromotionShop: service token + admin/super_admin ONLY; specialist staff/buyer/seller denied.
		releaseAllowed := roleSet(auth.RoleSuperAdmin, auth.RoleAdmin, "service_token")
		for idx, tc := range allRoleContexts() {
			_, rErr := svc.ReleasePromotion(tc.ctx, &promotionv1.ReleasePromotionRequest{
				OrderId: fmt.Sprintf("w3b2-rel-%d", idx),
			})
			expectRBACCode(t, "ReleasePromotion", tc.name, releaseAllowed, rErr)

			_, rsErr := svc.ReleasePromotionShop(tc.ctx, &promotionv1.ReleasePromotionShopRequest{
				OrderId: fmt.Sprintf("w3b2-rels-%d", idx),
				ShopId:  "shop-1",
			})
			expectRBACCode(t, "ReleasePromotionShop", tc.name, releaseAllowed, rsErr)
		}
	})

	t.Run("ListShopDiscountPrograms_CapVouchersRead", func(t *testing.T) {
		svc, _, _ := setupService(t)
		svc.SetClock(func() time.Time { return now })
		pc := newFakeProductClient()
		pc.setSKU(&productv1.SKU{Id: "sku-sdp-s2", ShopId: "shop-2", CategoryId: "cat-1", PriceVnd: 100000, MinPrice_30DVnd: 100000, AvailableStock: 20, IsActive: true})
		pc.setSKU(&productv1.SKU{Id: "sku-sdp-s1", ShopId: "shop-1", CategoryId: "cat-1", PriceVnd: 100000, MinPrice_30DVnd: 100000, AvailableStock: 20, IsActive: true})
		svc.SetProductClient(pc)

		seller1Ctx := ctxWithRole("seller-1", auth.RoleSeller, "shop-1")
		seller2Ctx := ctxWithRole("seller-2", auth.RoleSeller, "shop-2")
		if _, err := svc.CreateShopDiscountProgram(seller1Ctx, &promotionv1.CreateShopDiscountProgramRequest{
			Name:     "S1 Promo",
			StartsAt: timestamppb.New(now.Add(-time.Hour)),
			EndsAt:   timestamppb.New(now.Add(2 * time.Hour)),
			Items:    []*promotionv1.ShopDiscountProgramItemInput{{SkuId: "sku-sdp-s1", DiscountedPriceVnd: 80000}},
		}); err != nil {
			t.Fatalf("seed shop-1 discount program: %v", err)
		}
		if _, err := svc.CreateShopDiscountProgram(seller2Ctx, &promotionv1.CreateShopDiscountProgramRequest{
			Name:     "S2 Promo",
			StartsAt: timestamppb.New(now.Add(-time.Hour)),
			EndsAt:   timestamppb.New(now.Add(2 * time.Hour)),
			Items:    []*promotionv1.ShopDiscountProgramItemInput{{SkuId: "sku-sdp-s2", DiscountedPriceVnd: 80000}},
		}); err != nil {
			t.Fatalf("seed shop-2 discount program: %v", err)
		}

		// Cross-shop admin view (shop-2) requires CapVouchersRead (super_admin, admin, cskh, accountant, ops).
		// moderator, buyer, seller (of shop-1), service_token must get PermissionDenied.
		allowed := roleSet(auth.RoleSuperAdmin, auth.RoleAdmin, auth.RoleCSKH, auth.RoleAccountant, auth.RoleOps)
		for _, tc := range allRoleContexts() {
			resp, err := svc.ListShopDiscountPrograms(tc.ctx, &promotionv1.ListShopDiscountProgramsRequest{ShopId: "shop-2"})
			expectRBACCode(t, "ListShopDiscountPrograms(shop-2)", tc.name, allowed, err)
			if allowed[tc.name] && (len(resp.GetPrograms()) != 1 || resp.GetPrograms()[0].GetShopId() != "shop-2") {
				t.Fatalf("ListShopDiscountPrograms(shop-2) as %s: unexpected resp=%+v", tc.name, resp)
			}
		}

		// Seller retains access to own shop's discount programs.
		ownResp, err := svc.ListShopDiscountPrograms(seller1Ctx, &promotionv1.ListShopDiscountProgramsRequest{})
		if err != nil || len(ownResp.GetPrograms()) != 1 || ownResp.GetPrograms()[0].GetShopId() != "shop-1" {
			t.Fatalf("seller own ListShopDiscountPrograms failed: resp=%+v err=%v", ownResp, err)
		}
	})
}

func TestService_W3B2_GRPCInterceptor_ServiceTokensAndSignedJWTs(t *testing.T) {
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	svc, _, _ := setupService(t)
	svc.SetClock(func() time.Time { return now })
	pc := newFakeProductClient()
	pc.setSKU(&productv1.SKU{Id: "sku-grpc-1", ShopId: "shop-1", CategoryId: "cat-1", PriceVnd: 200000, MinPrice_30DVnd: 200000, AvailableStock: 50, IsActive: true})
	svc.SetProductClient(pc)

	jwtMgr, err := auth.NewManager("test-user-jwt-secret-w3b2-32bytes", auth.DefaultAccessTTL)
	if err != nil {
		t.Fatalf("NewManager(user): %v", err)
	}
	svcMgr, err := auth.NewManager("test-svc-jwt-secret-w3b2-32bytes!", auth.ServiceTokenTTL)
	if err != nil {
		t.Fatalf("NewManager(svc): %v", err)
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	grpcServer := grpc.NewServer(
		grpc.UnaryInterceptor(auth.GRPCUnaryServerInterceptor(jwtMgr, svcMgr)),
	)
	promotionv1.RegisterPromotionServiceServer(grpcServer, svc)
	go func() { _ = grpcServer.Serve(lis) }()
	t.Cleanup(func() {
		grpcServer.GracefulStop()
		_ = lis.Close()
	})

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := promotionv1.NewPromotionServiceClient(conn)

	userCtx := func(userID, role, shopID string) context.Context {
		tok, _, _, sErr := jwtMgr.SignAccessToken(userID, role, shopID)
		if sErr != nil {
			t.Fatalf("SignAccessToken(%s): %v", role, sErr)
		}
		return auth.InjectOutgoingGRPC(context.Background(), tok)
	}
	svcCtx := func(serviceName string) context.Context {
		tok, sErr := svcMgr.SignServiceToken(serviceName)
		if sErr != nil {
			t.Fatalf("SignServiceToken(%s): %v", serviceName, sErr)
		}
		return auth.InjectOutgoingGRPC(context.Background(), tok)
	}

	orderSvcCtx := svcCtx("order-service")
	otherSvcCtx := svcCtx("payment-service")

	// 1. Service token succeeds on internalRPCs=true: CreditCoins, ReservePromotion (order-service only), ReleasePromotion, ReleasePromotionShop.
	if _, err := client.CreditCoins(orderSvcCtx, &promotionv1.CreditCoinsRequest{
		UserId: "buyer-1", Amount: 500, Reason: "order_reward", ReferenceId: "grpc-svc-coin-1",
	}); err != nil {
		t.Fatalf("service token CreditCoins failed: %v", err)
	}
	if _, err := client.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
		OrderId:   "grpc-svc-ord-1",
		UserId:    "buyer-1",
		ShopItems: []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
	}); err != nil {
		t.Fatalf("order-service ReservePromotion failed: %v", err)
	}
	if _, err := client.ReservePromotion(otherSvcCtx, &promotionv1.ReservePromotionRequest{
		OrderId:   "grpc-svc-ord-2",
		UserId:    "buyer-1",
		ShopItems: []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("other-service ReservePromotion: want PermissionDenied, got %v", err)
	}
	if _, err := client.ReleasePromotionShop(orderSvcCtx, &promotionv1.ReleasePromotionShopRequest{
		OrderId: "grpc-svc-ord-1", ShopId: "shop-1",
	}); err != nil {
		t.Fatalf("service token ReleasePromotionShop failed: %v", err)
	}
	if _, err := client.ReleasePromotion(orderSvcCtx, &promotionv1.ReleasePromotionRequest{
		OrderId: "grpc-svc-ord-1",
	}); err != nil {
		t.Fatalf("service token ReleasePromotion failed: %v", err)
	}

	// 2. Service token MUST be rejected with PermissionDenied on EVERY internalRPCs=false RPC and public RPC.
	serviceDeniedCalls := []struct {
		name string
		call func(context.Context) error
	}{
		{"CreateVoucher", func(c context.Context) error {
			_, e := client.CreateVoucher(c, &promotionv1.CreateVoucherRequest{
				Code: "SVCBAD", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
				DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000, Quota: 5,
				StartsAt: timestamppb.New(now), EndsAt: timestamppb.New(now.Add(time.Hour)),
			})
			return e
		}},
		{"UpdateVoucher", func(c context.Context) error {
			_, e := client.UpdateVoucher(c, &promotionv1.UpdateVoucherRequest{VoucherId: "v-1", Name: strPtr("x")})
			return e
		}},
		{"SetVoucherStatus", func(c context.Context) error {
			_, e := client.SetVoucherStatus(c, &promotionv1.SetVoucherStatusRequest{VoucherId: "v-1", Status: promotionv1.VoucherStatus_VOUCHER_STATUS_PAUSED})
			return e
		}},
		{"DeleteVoucher", func(c context.Context) error {
			_, e := client.DeleteVoucher(c, &promotionv1.DeleteVoucherRequest{VoucherId: "v-1"})
			return e
		}},
		{"SaveVoucher", func(c context.Context) error {
			_, e := client.SaveVoucher(c, &promotionv1.SaveVoucherRequest{VoucherId: "v-1"})
			return e
		}},
		{"CreateFlashSale", func(c context.Context) error {
			_, e := client.CreateFlashSale(c, &promotionv1.CreateFlashSaleRequest{Name: "x", StartsAt: timestamppb.New(now), EndsAt: timestamppb.New(now.Add(time.Hour))})
			return e
		}},
		{"RegisterFlashSaleItem", func(c context.Context) error {
			_, e := client.RegisterFlashSaleItem(c, &promotionv1.RegisterFlashSaleItemRequest{FlashSaleId: "f-1", ShopId: "shop-1", SkuId: "sku-grpc-1", FlashPriceVnd: 100000, TotalSlots: 1})
			return e
		}},
		{"ApproveFlashSaleItem", func(c context.Context) error {
			_, e := client.ApproveFlashSaleItem(c, &promotionv1.ApproveFlashSaleItemRequest{ItemId: "fi-1", Approve: true})
			return e
		}},
		{"ListFlashSaleItems", func(c context.Context) error {
			_, e := client.ListFlashSaleItems(c, &promotionv1.ListFlashSaleItemsRequest{})
			return e
		}},
		{"ListAllFlashSales", func(c context.Context) error {
			_, e := client.ListAllFlashSales(c, &promotionv1.ListAllFlashSalesRequest{})
			return e
		}},
		{"CreateShopDiscountProgram", func(c context.Context) error {
			_, e := client.CreateShopDiscountProgram(c, &promotionv1.CreateShopDiscountProgramRequest{Name: "x"})
			return e
		}},
		{"UpdateShopDiscountProgram", func(c context.Context) error {
			_, e := client.UpdateShopDiscountProgram(c, &promotionv1.UpdateShopDiscountProgramRequest{ProgramId: "p-1"})
			return e
		}},
		{"EndShopDiscountProgram", func(c context.Context) error {
			_, e := client.EndShopDiscountProgram(c, &promotionv1.EndShopDiscountProgramRequest{ProgramId: "p-1"})
			return e
		}},
		{"ListShopDiscountPrograms", func(c context.Context) error {
			_, e := client.ListShopDiscountPrograms(c, &promotionv1.ListShopDiscountProgramsRequest{})
			return e
		}},
		{"GetCoinBalance", func(c context.Context) error {
			_, e := client.GetCoinBalance(c, &promotionv1.GetCoinBalanceRequest{UserId: "buyer-1"})
			return e
		}},
		{"PreviewDiscounts", func(c context.Context) error {
			_, e := client.PreviewDiscounts(c, &promotionv1.PreviewDiscountsRequest{UserId: "buyer-1"})
			return e
		}},
		{"ListAvailableVouchers(public)", func(c context.Context) error {
			_, e := client.ListAvailableVouchers(c, &promotionv1.ListAvailableVouchersRequest{IncludeInactive: true})
			return e
		}},
		{"ListActiveFlashSales(public)", func(c context.Context) error {
			_, e := client.ListActiveFlashSales(c, &promotionv1.ListActiveFlashSalesRequest{})
			return e
		}},
		{"ListActiveShopDiscounts(public)", func(c context.Context) error {
			_, e := client.ListActiveShopDiscounts(c, &promotionv1.ListActiveShopDiscountsRequest{ShopId: "shop-1"})
			return e
		}},
	}
	for _, sc := range serviceDeniedCalls {
		if err := sc.call(orderSvcCtx); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("service token on %s: want PermissionDenied, got %v", sc.name, err)
		}
	}

	// 3. End-to-end signed staff JWT enforcement & forged plain metadata attack check.
	forgedCtx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(
		"x-user-id", "fake-super",
		"x-user-role", auth.RoleSuperAdmin,
	))
	if _, err := client.CreditCoins(forgedCtx, &promotionv1.CreditCoinsRequest{
		UserId: "buyer-1", Amount: 999999, ReferenceId: "forged-1",
	}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("forged metadata CreditCoins: want Unauthenticated, got %v", err)
	}

	// Signed ops JWT can create flash sale & vouchers, CANNOT credit coins (F9-bis).
	opsCtx := userCtx("ops-1", auth.RoleOps, "")
	fsResp, err := client.CreateFlashSale(opsCtx, &promotionv1.CreateFlashSaleRequest{
		Name: "Ops Flash", StartsAt: timestamppb.New(now.Add(time.Hour)), EndsAt: timestamppb.New(now.Add(3 * time.Hour)),
	})
	if err != nil {
		t.Fatalf("signed ops CreateFlashSale failed: %v", err)
	}
	if _, err := client.CreditCoins(opsCtx, &promotionv1.CreditCoinsRequest{
		UserId: "buyer-1", Amount: 1000, ReferenceId: "ops-coin-denied",
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("signed ops CreditCoins (F9-bis): want PermissionDenied, got %v", err)
	}

	// Signed cskh JWT can list admin vouchers, CANNOT credit coins (F9-bis) or approve flash items.
	cskhCtx := userCtx("cskh-1", auth.RoleCSKH, "")
	if _, err := client.ListAvailableVouchers(cskhCtx, &promotionv1.ListAvailableVouchersRequest{IncludeInactive: true}); err != nil {
		t.Fatalf("signed cskh ListAvailableVouchers(admin) failed: %v", err)
	}
	if _, err := client.CreditCoins(cskhCtx, &promotionv1.CreditCoinsRequest{
		UserId: "buyer-1", Amount: 1000, ReferenceId: "cskh-coin-denied",
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("signed cskh CreditCoins (F9-bis): want PermissionDenied, got %v", err)
	}

	// Signed accountant JWT CAN credit coins and list admin vouchers, CANNOT create vouchers.
	accCtx := userCtx("acc-1", auth.RoleAccountant, "")
	if _, err := client.CreditCoins(accCtx, &promotionv1.CreditCoinsRequest{
		UserId: "buyer-1", Amount: 2000, ReferenceId: "acc-coin-ok",
	}); err != nil {
		t.Fatalf("signed accountant CreditCoins failed: %v", err)
	}
	if _, err := client.CreateVoucher(accCtx, &promotionv1.CreateVoucherRequest{
		Code: "ACCDENIED", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000, Quota: 5,
		StartsAt: timestamppb.New(now), EndsAt: timestamppb.New(now.Add(time.Hour)),
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("signed accountant CreateVoucher: want PermissionDenied, got %v", err)
	}

	// Signed moderator JWT CAN approve flash sale items & list flash sales, CANNOT create flash sale.
	sellerCtx := userCtx("seller-1", auth.RoleSeller, "shop-1")
	regResp, err := client.RegisterFlashSaleItem(sellerCtx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId: fsResp.FlashSale.Id, ShopId: "shop-1", SkuId: "sku-grpc-1", FlashPriceVnd: 150000, TotalSlots: 5, PerUserLimit: 1,
	})
	if err != nil {
		t.Fatalf("signed seller RegisterFlashSaleItem failed: %v", err)
	}
	modCtx := userCtx("mod-1", auth.RoleModerator, "")
	if _, err := client.ApproveFlashSaleItem(modCtx, &promotionv1.ApproveFlashSaleItemRequest{
		ItemId: regResp.Item.Id, Approve: true,
	}); err != nil {
		t.Fatalf("signed moderator ApproveFlashSaleItem failed: %v", err)
	}
	if _, err := client.CreateFlashSale(modCtx, &promotionv1.CreateFlashSaleRequest{
		Name: "Mod Denied", StartsAt: timestamppb.New(now.Add(5 * time.Hour)), EndsAt: timestamppb.New(now.Add(6 * time.Hour)),
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("signed moderator CreateFlashSale: want PermissionDenied, got %v", err)
	}
}
