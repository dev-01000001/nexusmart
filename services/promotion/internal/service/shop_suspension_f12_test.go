package service_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/promotion/internal/events"
	"github.com/nexusmart/core/services/promotion/internal/lua"
	"github.com/nexusmart/core/services/promotion/internal/service"
	"github.com/nexusmart/core/services/promotion/internal/store"
)

func makeShopStatusEventRaw(t *testing.T, eventID, shopID, fromStatus, toStatus string, version int64) []byte {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"shop_id":       shopID,
		"owner_user_id": "owner-" + shopID,
		"shop_name":     "Shop " + shopID,
		"from_status":   fromStatus,
		"to_status":     toStatus,
		"reason":        "policy",
		"actor_id":      "ops-1",
		"approver_id":   "ops-2",
		"version":       version,
		"occurred_at":   "2026-11-24T12:00:00Z",
	})
	if err != nil {
		t.Fatalf("marshal shop status payload: %v", err)
	}
	raw, err := json.Marshal(events.Envelope{
		EventID:    eventID,
		EventType:  "user.shop_status_changed",
		TraceID:    "4bf92f3577b34da6a3ce929d0e0e4736",
		OccurredAt: "2026-11-24T12:00:00Z",
		Payload:    payload,
	})
	if err != nil {
		t.Fatalf("marshal shop status envelope: %v", err)
	}
	return raw
}

func assertShopSuspendedErr(t *testing.T, label string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected FailedPrecondition SHOP_SUSPENDED, got nil", label)
	}
	st := status.Convert(err)
	if st.Code() != codes.FailedPrecondition || !strings.HasPrefix(st.Message(), "SHOP_SUSPENDED") {
		t.Fatalf("%s: want FailedPrecondition SHOP_SUSPENDED, got code=%v msg=%q", label, st.Code(), st.Message())
	}
}

func TestService_W3B2_F12_ShopSuspensionLifecycle(t *testing.T) {
	runSuite := func(t *testing.T, st store.Store, suffix string) {
		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })

		now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
		log := logger.New("promo-f12-test", logger.WithWriter(io.Discard))
		eng := lua.NewEngine(rdb)
		svc := service.NewPromotionService(st, eng, log)
		svc.SetClock(func() time.Time { return now })

		shop1 := "shop1-" + suffix
		shop2 := "shop2-" + suffix
		sku1 := "sku1-" + suffix
		sku1b := "sku1b-" + suffix
		sku2 := "sku2-" + suffix
		buyer1 := "buyer1-" + suffix
		buyer2 := "buyer2-" + suffix

		pc := newFakeProductClient()
		pc.setSKU(&productv1.SKU{Id: sku1, ShopId: shop1, CategoryId: "cat-1", PriceVnd: 200000, MinPrice_30DVnd: 200000, AvailableStock: 50, IsActive: true})
		pc.setSKU(&productv1.SKU{Id: sku1b, ShopId: shop1, CategoryId: "cat-1", PriceVnd: 180000, MinPrice_30DVnd: 180000, AvailableStock: 50, IsActive: true})
		pc.setSKU(&productv1.SKU{Id: sku2, ShopId: shop2, CategoryId: "cat-1", PriceVnd: 200000, MinPrice_30DVnd: 200000, AvailableStock: 50, IsActive: true})
		svc.SetProductClient(pc)

		consumer := events.NewOrderCompletedConsumer(st, nil, "NEXUSMART", log)

		adminCtx := ctxWithRole("admin-"+suffix, auth.RoleAdmin, "")
		opsCtx := ctxWithRole("ops-"+suffix, auth.RoleOps, "")
		seller1Ctx := ctxWithRole("seller1-"+suffix, auth.RoleSeller, shop1)
		seller2Ctx := ctxWithRole("seller2-"+suffix, auth.RoleSeller, shop2)
		buyer1Ctx := ctxWithRole(buyer1, auth.RoleBuyer, "")
		buyer2Ctx := ctxWithRole(buyer2, auth.RoleBuyer, "")
		orderSvcCtx := ctxWithService("order-service")

		// Seed vouchers: shop1, shop2, platform
		vShop1, err := svc.CreateVoucher(seller1Ctx, &promotionv1.CreateVoucherRequest{
			Code: "S1V_" + suffix, Name: "Shop1 20k", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP, ShopId: shop1,
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 20000, MinOrderVnd: 100000,
			Quota: 20, PerUserLimit: 5, StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(4 * time.Hour)),
		})
		if err != nil {
			t.Fatalf("seed shop1 voucher: %v", err)
		}
		vShop2, err := svc.CreateVoucher(seller2Ctx, &promotionv1.CreateVoucherRequest{
			Code: "S2V_" + suffix, Name: "Shop2 20k", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP, ShopId: shop2,
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 20000, MinOrderVnd: 100000,
			Quota: 20, PerUserLimit: 5, StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(4 * time.Hour)),
		})
		if err != nil {
			t.Fatalf("seed shop2 voucher: %v", err)
		}
		vPlat, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
			Code: "PLV_" + suffix, Name: "Plat 15k", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 15000, MinOrderVnd: 50000,
			Quota: 20, PerUserLimit: 5, StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(4 * time.Hour)),
		})
		if err != nil {
			t.Fatalf("seed plat voucher: %v", err)
		}

		// buyer1 saves shop1 voucher BEFORE suspension
		if _, err := svc.SaveVoucher(buyer1Ctx, &promotionv1.SaveVoucherRequest{VoucherId: vShop1.Voucher.Id}); err != nil {
			t.Fatalf("buyer1 SaveVoucher before suspend: %v", err)
		}

		// Seed active flash sale + approved items for shop1 and shop2
		fsActive, err := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
			Name: "Live Flash " + suffix, StartsAt: timestamppb.New(now.Add(-30 * time.Minute)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
		})
		if err != nil {
			t.Fatalf("seed active flash sale: %v", err)
		}
		fsFuture, err := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
			Name: "Future Flash " + suffix, StartsAt: timestamppb.New(now.Add(5 * time.Hour)), EndsAt: timestamppb.New(now.Add(7 * time.Hour)),
		})
		if err != nil {
			t.Fatalf("seed future flash sale: %v", err)
		}
		fiShop1, err := svc.RegisterFlashSaleItem(seller1Ctx, &promotionv1.RegisterFlashSaleItemRequest{
			FlashSaleId: fsActive.FlashSale.Id, ShopId: shop1, SkuId: sku1, FlashPriceVnd: 140000, TotalSlots: 10, PerUserLimit: 2,
		})
		if err != nil {
			t.Fatalf("seed shop1 flash item: %v", err)
		}
		if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: fiShop1.Item.Id, Approve: true}); err != nil {
			t.Fatalf("approve shop1 flash item: %v", err)
		}
		fiShop2, err := svc.RegisterFlashSaleItem(seller2Ctx, &promotionv1.RegisterFlashSaleItemRequest{
			FlashSaleId: fsActive.FlashSale.Id, ShopId: shop2, SkuId: sku2, FlashPriceVnd: 140000, TotalSlots: 10, PerUserLimit: 2,
		})
		if err != nil {
			t.Fatalf("seed shop2 flash item: %v", err)
		}
		if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: fiShop2.Item.Id, Approve: true}); err != nil {
			t.Fatalf("approve shop2 flash item: %v", err)
		}

		// Seed active shop discount programs for shop1 (sku1b) and shop2 (sku2)
		progShop1, err := svc.CreateShopDiscountProgram(seller1Ctx, &promotionv1.CreateShopDiscountProgramRequest{
			Name: "Shop1 Discount " + suffix, StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(3 * time.Hour)),
			Items: []*promotionv1.ShopDiscountProgramItemInput{{SkuId: sku1b, DiscountedPriceVnd: 150000, PerUserLimit: 2}},
		})
		if err != nil {
			t.Fatalf("seed shop1 discount program: %v", err)
		}
		if _, err := svc.CreateShopDiscountProgram(seller2Ctx, &promotionv1.CreateShopDiscountProgramRequest{
			Name: "Shop2 Discount " + suffix, StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(3 * time.Hour)),
			Items: []*promotionv1.ShopDiscountProgramItemInput{{SkuId: sku2, DiscountedPriceVnd: 160000, PerUserLimit: 2}},
		}); err != nil {
			t.Fatalf("seed shop2 discount program: %v", err)
		}

		// Seed an in-flight order reservation for shop1 BEFORE suspension (must stay releasable/replayable!).
		inFlightOrderID := "inflight-ord-" + suffix
		if _, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId:        inFlightOrderID,
			UserId:         buyer1,
			ShopVoucherIds: map[string]string{shop1: vShop1.Voucher.Id},
			ShopItems: []*promotionv1.ShopDiscountInput{
				{
					ShopId: shop1, SubtotalVnd: 200000, ShippingFeeVnd: 20000,
					Items: []*promotionv1.DiscountLineItem{
						{SkuId: sku1, Quantity: 1, UnitPriceVnd: 200000, FlashSaleItemId: fiShop1.Item.Id},
					},
				},
			},
		}); err != nil {
			t.Fatalf("seed in-flight ReservePromotion: %v", err)
		}

		// === 1. Deliver user.shop_status_changed (SUSPENDED, version=2) ===
		suspEvtID := "evt-susp-v2-" + suffix
		ack, err := consumer.ProcessRawMessage(context.Background(), makeShopStatusEventRaw(t, suspEvtID, shop1, "ACTIVE", "SUSPENDED", 2), nil, 1)
		if err != nil || !ack {
			t.Fatalf("process SUSPENDED event: ack=%v err=%v", ack, err)
		}

		// === 2. Buyer visibility filtering ===
		// (a) ListActiveFlashSales hides shop1 item, keeps shop2 item.
		fsResp, err := svc.ListActiveFlashSales(context.Background(), &promotionv1.ListActiveFlashSalesRequest{})
		if err != nil {
			t.Fatalf("ListActiveFlashSales: %v", err)
		}
		var sawFI1, sawFI2 bool
		for _, fs := range fsResp.GetFlashSales() {
			for _, it := range fs.GetItems() {
				if it.GetId() == fiShop1.Item.Id {
					sawFI1 = true
				}
				if it.GetId() == fiShop2.Item.Id {
					sawFI2 = true
				}
			}
		}
		if sawFI1 || !sawFI2 {
			t.Fatalf("ListActiveFlashSales while shop1 suspended: sawFI1=%v (want false) sawFI2=%v (want true)", sawFI1, sawFI2)
		}

		// (b) ListAvailableVouchers (buyer1 who saved it + buyer2 + anon) hides shop1 voucher, keeps shop2 & platform.
		for label, bCtx := range map[string]context.Context{"buyer1-saved": buyer1Ctx, "buyer2": buyer2Ctx, "anon": context.Background()} {
			vResp, err := svc.ListAvailableVouchers(bCtx, &promotionv1.ListAvailableVouchersRequest{})
			if err != nil {
				t.Fatalf("ListAvailableVouchers(%s): %v", label, err)
			}
			var sawS1, sawS2, sawP bool
			for _, v := range vResp.GetVouchers() {
				switch v.GetId() {
				case vShop1.Voucher.Id:
					sawS1 = true
				case vShop2.Voucher.Id:
					sawS2 = true
				case vPlat.Voucher.Id:
					sawP = true
				}
			}
			if sawS1 || !sawS2 || !sawP {
				t.Fatalf("ListAvailableVouchers(%s) while suspended: sawS1=%v (want false) sawS2=%v sawP=%v", label, sawS1, sawS2, sawP)
			}
		}
		// Admin management view still sees shop1 voucher.
		adminVResp, err := svc.ListAvailableVouchers(adminCtx, &promotionv1.ListAvailableVouchersRequest{IncludeInactive: true})
		if err != nil {
			t.Fatalf("admin ListAvailableVouchers: %v", err)
		}
		adminSawS1 := false
		for _, v := range adminVResp.GetVouchers() {
			if v.GetId() == vShop1.Voucher.Id {
				adminSawS1 = true
			}
		}
		if !adminSawS1 {
			t.Fatal("admin ListAvailableVouchers(include_inactive=true) must still see suspended shop voucher")
		}

		// (c) ListActiveShopDiscounts hides shop1 discount, keeps shop2 discount.
		sdResp, err := svc.ListActiveShopDiscounts(context.Background(), &promotionv1.ListActiveShopDiscountsRequest{
			SkuIds: []string{sku1b, sku2},
		})
		if err != nil {
			t.Fatalf("ListActiveShopDiscounts: %v", err)
		}
		var sawSD1, sawSD2 bool
		for _, d := range sdResp.GetDiscounts() {
			if d.GetShopId() == shop1 {
				sawSD1 = true
			}
			if d.GetShopId() == shop2 {
				sawSD2 = true
			}
		}
		if sawSD1 || !sawSD2 {
			t.Fatalf("ListActiveShopDiscounts while suspended: sawSD1=%v (want false) sawSD2=%v (want true)", sawSD1, sawSD2)
		}

		// (d) SaveVoucher rejects shop1 voucher with FailedPrecondition SHOP_SUSPENDED; shop2/platform still saveable.
		_, err = svc.SaveVoucher(buyer2Ctx, &promotionv1.SaveVoucherRequest{VoucherId: vShop1.Voucher.Id})
		assertShopSuspendedErr(t, "SaveVoucher(shop1)", err)
		if _, err := svc.SaveVoucher(buyer2Ctx, &promotionv1.SaveVoucherRequest{VoucherId: vShop2.Voucher.Id}); err != nil {
			t.Fatalf("SaveVoucher(shop2) should succeed: %v", err)
		}

		// (e) PreviewDiscounts MUST NOT apply suspended shop's SHOP voucher, flash item, or shop discount program.
		prevResp, err := svc.PreviewDiscounts(buyer2Ctx, &promotionv1.PreviewDiscountsRequest{
			ShopVoucherIds: map[string]string{shop1: vShop1.Voucher.Id},
			ShopItems: []*promotionv1.ShopDiscountInput{
				{
					ShopId: shop1, ShippingFeeVnd: 20000,
					Items: []*promotionv1.DiscountLineItem{
						{SkuId: sku1, Quantity: 1, FlashSaleItemId: fiShop1.Item.Id},
						{SkuId: sku1b, Quantity: 1, ShopDiscountProgramItemId: progShop1.Program.Items[0].Id},
					},
				},
			},
		})
		if err != nil {
			t.Fatalf("PreviewDiscounts while shop1 suspended: %v", err)
		}
		bd := prevResp.GetBreakdown()
		if bd.GetTotalShopVoucherDiscountVnd() != 0 || bd.GetTotalFlashSaleDiscountVnd() != 0 || bd.GetTotalShopProgramDiscountVnd() != 0 {
			t.Fatalf("PreviewDiscounts applied discounts for suspended shop: voucher=%d flash=%d prog=%d",
				bd.GetTotalShopVoucherDiscountVnd(), bd.GetTotalFlashSaleDiscountVnd(), bd.GetTotalShopProgramDiscountVnd())
		}
		if !strings.Contains(strings.Join(bd.GetWarnings(), "; "), "SHOP_SUSPENDED") {
			t.Fatalf("PreviewDiscounts warnings missing SHOP_SUSPENDED: %v", bd.GetWarnings())
		}

		// (f) ReservePromotion (strict) rejects new reservation using suspended shop's SHOP voucher with SHOP_SUSPENDED.
		_, err = svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId:        "new-ord-susp-" + suffix,
			UserId:         buyer2,
			ShopVoucherIds: map[string]string{shop1: vShop1.Voucher.Id},
			ShopItems:      []*promotionv1.ShopDiscountInput{{ShopId: shop1, SubtotalVnd: 200000, ShippingFeeVnd: 20000}},
		})
		assertShopSuspendedErr(t, "ReservePromotion(suspended shop voucher)", err)

		// === 3. Seller mutations of suspended shop1 blocked with SHOP_SUSPENDED; admin/ops still allowed ===
		_, err = svc.CreateVoucher(seller1Ctx, &promotionv1.CreateVoucherRequest{
			Code: "S1NEW_" + suffix, Name: "Blocked", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP, ShopId: shop1,
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000, Quota: 5, PerUserLimit: 1,
			StartsAt: timestamppb.New(now), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
		})
		assertShopSuspendedErr(t, "seller CreateVoucher", err)

		_, err = svc.UpdateVoucher(seller1Ctx, &promotionv1.UpdateVoucherRequest{
			VoucherId: vShop1.Voucher.Id, Name: strPtr("Extended While Suspended"),
			EndsAt: timestamppb.New(now.Add(8 * time.Hour)),
		})
		assertShopSuspendedErr(t, "seller UpdateVoucher", err)

		_, err = svc.SetVoucherStatus(seller1Ctx, &promotionv1.SetVoucherStatusRequest{
			VoucherId: vShop1.Voucher.Id, Status: promotionv1.VoucherStatus_VOUCHER_STATUS_PAUSED,
		})
		assertShopSuspendedErr(t, "seller SetVoucherStatus(PAUSED)", err)
		_, err = svc.SetVoucherStatus(seller1Ctx, &promotionv1.SetVoucherStatusRequest{
			VoucherId: vShop1.Voucher.Id, Status: promotionv1.VoucherStatus_VOUCHER_STATUS_ACTIVE,
		})
		assertShopSuspendedErr(t, "seller SetVoucherStatus(ACTIVE)", err)

		_, err = svc.CreateShopDiscountProgram(seller1Ctx, &promotionv1.CreateShopDiscountProgramRequest{
			Name: "Blocked Prog", StartsAt: timestamppb.New(now.Add(4 * time.Hour)), EndsAt: timestamppb.New(now.Add(6 * time.Hour)),
			Items: []*promotionv1.ShopDiscountProgramItemInput{{SkuId: sku1b, DiscountedPriceVnd: 150000}},
		})
		assertShopSuspendedErr(t, "seller CreateShopDiscountProgram", err)

		_, err = svc.UpdateShopDiscountProgram(seller1Ctx, &promotionv1.UpdateShopDiscountProgramRequest{
			ProgramId: progShop1.Program.Id, Name: strPtr("Mutated While Suspended"),
		})
		assertShopSuspendedErr(t, "seller UpdateShopDiscountProgram", err)

		_, err = svc.RegisterFlashSaleItem(seller1Ctx, &promotionv1.RegisterFlashSaleItemRequest{
			FlashSaleId: fsFuture.FlashSale.Id, ShopId: shop1, SkuId: sku1, FlashPriceVnd: 140000, TotalSlots: 5, PerUserLimit: 1,
		})
		assertShopSuspendedErr(t, "seller RegisterFlashSaleItem", err)

		// Admin/Ops actions still succeed while shop1 is suspended.
		if _, err := svc.UpdateVoucher(opsCtx, &promotionv1.UpdateVoucherRequest{
			VoucherId: vPlat.Voucher.Id, Name: strPtr("Ops Updated Plat"),
		}); err != nil {
			t.Fatalf("ops UpdateVoucher(PLATFORM) should succeed: %v", err)
		}
		if _, err := svc.RegisterFlashSaleItem(opsCtx, &promotionv1.RegisterFlashSaleItemRequest{
			FlashSaleId: fsFuture.FlashSale.Id, ShopId: shop1, SkuId: sku1, FlashPriceVnd: 140000, TotalSlots: 5, PerUserLimit: 1,
		}); err != nil {
			t.Fatalf("ops RegisterFlashSaleItem should still succeed: %v", err)
		}

		// === 4. In-flight order unaffected (replay, release, order.completed reward) ===
		replayResp, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId:        inFlightOrderID,
			UserId:         buyer1,
			ShopVoucherIds: map[string]string{shop1: vShop1.Voucher.Id},
			ShopItems:      []*promotionv1.ShopDiscountInput{{ShopId: shop1, SubtotalVnd: 200000}},
		})
		if err != nil || !replayResp.GetAlreadyReserved() {
			t.Fatalf("in-flight ReservePromotion replay should succeed: resp=%+v err=%v", replayResp, err)
		}
		relResp, err := svc.ReleasePromotion(orderSvcCtx, &promotionv1.ReleasePromotionRequest{OrderId: inFlightOrderID})
		if err != nil || !relResp.GetReleased() {
			t.Fatalf("in-flight ReleasePromotion should succeed while shop suspended: resp=%+v err=%v", relResp, err)
		}
		ocPayload, _ := json.Marshal(events.OrderCompletedPayload{
			OrderID: "done-ord-" + suffix, BuyerID: buyer1, ShopID: shop1, CoinsReward: 750,
		})
		ocRaw, _ := json.Marshal(events.Envelope{
			EventID: "evt-oc-" + suffix, EventType: events.SubjectOrderCompleted, Payload: ocPayload,
		})
		if ack, err := consumer.ProcessRawMessage(context.Background(), ocRaw, nil, 1); err != nil || !ack {
			t.Fatalf("order.completed for suspended shop must still succeed: ack=%v err=%v", ack, err)
		}
		bal, _, err := st.GetCoinBalance(context.Background(), buyer1, 10)
		if err != nil || bal != 750 {
			t.Fatalf("expected buyer1 reward balance 750 for in-flight order, got bal=%d err=%v", bal, err)
		}

		// === 5. Stale out-of-order events (version <= 2) & duplicate event_id MUST be ignored ===
		// Stale version=1 ACTIVE
		if ack, err := consumer.ProcessRawMessage(context.Background(), makeShopStatusEventRaw(t, "evt-stale-v1-"+suffix, shop1, "SUSPENDED", "ACTIVE", 1), nil, 1); err != nil || !ack {
			t.Fatalf("stale v1 event: ack=%v err=%v", ack, err)
		}
		// Stale equal version=2 ACTIVE
		if ack, err := consumer.ProcessRawMessage(context.Background(), makeShopStatusEventRaw(t, "evt-stale-v2-"+suffix, shop1, "SUSPENDED", "ACTIVE", 2), nil, 1); err != nil || !ack {
			t.Fatalf("stale v2 event: ack=%v err=%v", ack, err)
		}
		// Duplicate event_id of original suspend event (even if payload says ACTIVE v99!)
		if ack, err := consumer.ProcessRawMessage(context.Background(), makeShopStatusEventRaw(t, suspEvtID, shop1, "SUSPENDED", "ACTIVE", 99), nil, 1); err != nil || !ack {
			t.Fatalf("duplicate event_id: ack=%v err=%v", ack, err)
		}
		// Shop1 must STILL be suspended!
		_, err = svc.SaveVoucher(buyer2Ctx, &promotionv1.SaveVoucherRequest{VoucherId: vShop1.Voucher.Id})
		assertShopSuspendedErr(t, "SaveVoucher after stale/duplicate events", err)

		// === 6. Unsuspend (ACTIVE, version=3) restores visibility and seller mutations immediately ===
		if ack, err := consumer.ProcessRawMessage(context.Background(), makeShopStatusEventRaw(t, "evt-unsusp-v3-"+suffix, shop1, "SUSPENDED", "ACTIVE", 3), nil, 1); err != nil || !ack {
			t.Fatalf("unsuspend v3 event: ack=%v err=%v", ack, err)
		}

		// Visible again in ListActiveFlashSales, ListAvailableVouchers, ListActiveShopDiscounts
		fsResp2, err := svc.ListActiveFlashSales(context.Background(), &promotionv1.ListActiveFlashSalesRequest{})
		if err != nil {
			t.Fatalf("ListActiveFlashSales after unsuspend: %v", err)
		}
		sawFI1After := false
		for _, fs := range fsResp2.GetFlashSales() {
			for _, it := range fs.GetItems() {
				if it.GetId() == fiShop1.Item.Id {
					sawFI1After = true
				}
			}
		}
		if !sawFI1After {
			t.Fatal("ListActiveFlashSales must immediately show shop1 flash item after unsuspend")
		}

		vResp2, err := svc.ListAvailableVouchers(buyer2Ctx, &promotionv1.ListAvailableVouchersRequest{})
		if err != nil {
			t.Fatalf("ListAvailableVouchers after unsuspend: %v", err)
		}
		sawV1After := false
		for _, v := range vResp2.GetVouchers() {
			if v.GetId() == vShop1.Voucher.Id {
				sawV1After = true
			}
		}
		if !sawV1After {
			t.Fatal("ListAvailableVouchers must immediately show shop1 voucher after unsuspend")
		}

		sdResp2, err := svc.ListActiveShopDiscounts(context.Background(), &promotionv1.ListActiveShopDiscountsRequest{SkuIds: []string{sku1b}})
		if err != nil || len(sdResp2.GetDiscounts()) != 1 || sdResp2.GetDiscounts()[0].GetShopId() != shop1 {
			t.Fatalf("ListActiveShopDiscounts after unsuspend: resp=%+v err=%v", sdResp2, err)
		}

		// SaveVoucher & PreviewDiscounts work again
		if _, err := svc.SaveVoucher(buyer2Ctx, &promotionv1.SaveVoucherRequest{VoucherId: vShop1.Voucher.Id}); err != nil {
			t.Fatalf("SaveVoucher after unsuspend: %v", err)
		}
		prevAfter, err := svc.PreviewDiscounts(buyer2Ctx, &promotionv1.PreviewDiscountsRequest{
			ShopVoucherIds: map[string]string{shop1: vShop1.Voucher.Id},
			ShopItems: []*promotionv1.ShopDiscountInput{
				{ShopId: shop1, ShippingFeeVnd: 20000, Items: []*promotionv1.DiscountLineItem{{SkuId: sku1b, Quantity: 1}}},
			},
		})
		if err != nil || prevAfter.GetBreakdown().GetTotalShopVoucherDiscountVnd() != 20000 || prevAfter.GetBreakdown().GetTotalShopProgramDiscountVnd() != 30000 {
			t.Fatalf("PreviewDiscounts after unsuspend unexpected: resp=%+v err=%v", prevAfter, err)
		}

		// Seller mutations work again
		if _, err := svc.UpdateVoucher(seller1Ctx, &promotionv1.UpdateVoucherRequest{
			VoucherId: vShop1.Voucher.Id, Name: strPtr("Renamed After Unsuspend"),
		}); err != nil {
			t.Fatalf("seller UpdateVoucher after unsuspend: %v", err)
		}
		if _, err := svc.SetVoucherStatus(seller1Ctx, &promotionv1.SetVoucherStatusRequest{
			VoucherId: vShop1.Voucher.Id, Status: promotionv1.VoucherStatus_VOUCHER_STATUS_PAUSED,
		}); err != nil {
			t.Fatalf("seller SetVoucherStatus after unsuspend: %v", err)
		}
		if _, err := svc.UpdateShopDiscountProgram(seller1Ctx, &promotionv1.UpdateShopDiscountProgramRequest{
			ProgramId: progShop1.Program.Id, Name: strPtr("Updated After Unsuspend"),
		}); err != nil {
			t.Fatalf("seller UpdateShopDiscountProgram after unsuspend: %v", err)
		}
	}

	t.Run("MemoryStore", func(t *testing.T) {
		runSuite(t, store.NewMemoryStore(), "mem-"+uuid.NewString()[:8])
	})

	t.Run("PostgresStore", func(t *testing.T) {
		dsn := strings.TrimSpace(os.Getenv("TEST_PROMOTION_DB_URL"))
		if dsn == "" {
			t.Skip("TEST_PROMOTION_DB_URL not set; run via scripts/test-db.sh")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			t.Fatalf("pgxpool.New: %v", err)
		}
		t.Cleanup(pool.Close)

		pgStore := store.NewPostgresStore(pool)
		if err := pgStore.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		runSuite(t, pgStore, "pg-"+uuid.NewString()[:8])
	})
}
