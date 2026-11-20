package service_test

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"

	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/promotion/internal/lua"
	"github.com/nexusmart/core/services/promotion/internal/service"
	"github.com/nexusmart/core/services/promotion/internal/store"
)

// TestGRPCTrust_PromotionService_B1 exercises the real gRPC stack (auth interceptor + PromotionService)
// the way cmd/server wires it, proving every non-public Promotion RPC rejects anonymous,
// forged-metadata, invalid-token, unauthorized-role, and cross-user callers.
func TestGRPCTrust_PromotionService_B1(t *testing.T) {
	users, err := auth.NewManager("promo-test-user-key", 15*time.Minute)
	if err != nil {
		t.Fatalf("new user manager: %v", err)
	}
	svcMgr, err := auth.NewManager("promo-test-service-key", auth.ServiceTokenTTL)
	if err != nil {
		t.Fatalf("new service manager: %v", err)
	}

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	mem := store.NewMemoryStore()
	eng := lua.NewEngine(rdb)
	log := logger.New("promotion-trust-test", logger.WithWriter(io.Discard))
	promoSvc := service.NewPromotionService(mem, eng, log)

	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer(grpc.ChainUnaryInterceptor(auth.GRPCUnaryServerInterceptor(users, svcMgr)))
	promotionv1.RegisterPromotionServiceServer(gs, promoSvc)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := promotionv1.NewPromotionServiceClient(conn)

	md := func(kv ...string) context.Context {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		t.Cleanup(cancel)
		return metadata.NewOutgoingContext(ctx, metadata.Pairs(kv...))
	}
	bearer := func(tok string, kv ...string) context.Context {
		return md(append(kv, "authorization", "Bearer "+tok)...)
	}

	adminTok, _, _, _ := users.SignAccessToken("admin-1", "admin", "")
	sellerATok, _, _, _ := users.SignAccessToken("seller-a", "seller", "shop-a")
	buyerATok, _, _, _ := users.SignAccessToken("buyer-a", "buyer", "")
	buyerBTok, _, _, _ := users.SignAccessToken("buyer-b", "buyer", "")
	svcTok, _ := svcMgr.SignServiceToken("order-service")

	wrongKeyMgr, _ := auth.NewManager("wrong-promo-key", 15*time.Minute)
	wrongKeyTok, _, _, _ := wrongKeyMgr.SignAccessToken("admin-1", "admin", "")
	wrongSvcMgr, _ := auth.NewManager("wrong-service-key", auth.ServiceTokenTTL)
	wrongSvcTok, _ := wrongSvcMgr.SignServiceToken("order-service")
	expiredMgr, _ := auth.NewManager("promo-test-user-key", time.Millisecond)
	expiredTok, _, _, _ := expiredMgr.SignAccessToken("buyer-a", "buyer", "")
	time.Sleep(10 * time.Millisecond)

	now := time.Now().UTC()

	// Seed a platform voucher, shop-a voucher, flash sale, and coins for buyer-a.
	platV, err := client.CreateVoucher(bearer(adminTok), &promotionv1.CreateVoucherRequest{
		Code:         "TRUSTPLAT",
		Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
		ValueVnd:     10000,
		Quota:        10,
		PerUserLimit: 2,
		StartsAt:     timestamppb.New(now.Add(-time.Hour)),
		EndsAt:       timestamppb.New(now.Add(time.Hour)),
	})
	if err != nil {
		t.Fatalf("seed platform voucher: %v", err)
	}
	if _, err := client.CreditCoins(bearer(adminTok), &promotionv1.CreditCoinsRequest{
		UserId:      "buyer-a",
		Amount:      20000,
		Reason:      "seed",
		ReferenceId: "trust-seed-1",
	}); err != nil {
		t.Fatalf("seed buyer-a coins: %v", err)
	}

	// 1. Anonymous, forged metadata, wrong-key, and expired tokens MUST fail with Unauthenticated on ALL 10 non-public RPCs.
	badAuth := map[string]context.Context{
		"anonymous":      md(),
		"forged_admin":   md("x-user-id", "admin-1", "x-user-role", "admin"),
		"forged_seller":  md("x-user-id", "seller-a", "x-user-role", "seller", "x-shop-id", "shop-a"),
		"wrong_user_key": bearer(wrongKeyTok),
		"wrong_svc_key":  bearer(wrongSvcTok),
		"expired_jwt":    bearer(expiredTok),
	}
	for name, ctx := range badAuth {
		if _, err := client.CreateVoucher(ctx, &promotionv1.CreateVoucherRequest{
			Code: "BAD1", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 5000, Quota: 1,
			StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(time.Hour)),
		}); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("CreateVoucher(%s): want Unauthenticated, got %v", name, err)
		}
		if _, err := client.SaveVoucher(ctx, &promotionv1.SaveVoucherRequest{UserId: "buyer-a", VoucherId: platV.Voucher.Id}); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("SaveVoucher(%s): want Unauthenticated, got %v", name, err)
		}
		if _, err := client.CreateFlashSale(ctx, &promotionv1.CreateFlashSaleRequest{
			Name: "Bad FS", StartsAt: timestamppb.New(now), EndsAt: timestamppb.New(now.Add(time.Hour)),
		}); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("CreateFlashSale(%s): want Unauthenticated, got %v", name, err)
		}
		if _, err := client.RegisterFlashSaleItem(ctx, &promotionv1.RegisterFlashSaleItemRequest{
			FlashSaleId: "fs-1", ShopId: "shop-a", SkuId: "sku-1", FlashPriceVnd: 1000, TotalSlots: 1,
		}); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("RegisterFlashSaleItem(%s): want Unauthenticated, got %v", name, err)
		}
		if _, err := client.ApproveFlashSaleItem(ctx, &promotionv1.ApproveFlashSaleItemRequest{
			ItemId: "item-1", Approve: true,
		}); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("ApproveFlashSaleItem(%s): want Unauthenticated, got %v", name, err)
		}
		if _, err := client.GetCoinBalance(ctx, &promotionv1.GetCoinBalanceRequest{UserId: "buyer-a"}); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("GetCoinBalance(%s): want Unauthenticated, got %v", name, err)
		}
		if _, err := client.CreditCoins(ctx, &promotionv1.CreditCoinsRequest{UserId: "buyer-a", Amount: 999999}); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("CreditCoins(%s): want Unauthenticated, got %v", name, err)
		}
		if _, err := client.PreviewDiscounts(ctx, &promotionv1.PreviewDiscountsRequest{UserId: "buyer-a", CoinsToUse: 1000}); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("PreviewDiscounts(%s): want Unauthenticated, got %v", name, err)
		}
		if _, err := client.ReservePromotion(ctx, &promotionv1.ReservePromotionRequest{
			OrderId: "anon-ord", UserId: "buyer-a", CoinsToUse: 1000,
		}); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("ReservePromotion(%s): want Unauthenticated, got %v", name, err)
		}
		if _, err := client.ReleasePromotion(ctx, &promotionv1.ReleasePromotionRequest{OrderId: "anon-ord"}); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("ReleasePromotion(%s): want Unauthenticated, got %v", name, err)
		}
		if _, err := client.EndFlashSaleWindow(ctx, &promotionv1.EndFlashSaleWindowRequest{WindowId: "fs-1"}); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("EndFlashSaleWindow(%s): want Unauthenticated, got %v", name, err)
		}
	}

	// Verify buyer-a's coins were NOT inflated by anonymous CreditCoins calls.
	if mem.AccountBalance("buyer-a") != 20000 {
		t.Fatalf("buyer-a coins mutated by unauthenticated calls: got %d, want 20000", mem.AccountBalance("buyer-a"))
	}

	// 2. Public read RPCs stay publicly callable WITHOUT user_id, while anonymous user_id probing is rejected.
	if _, err := client.ListActiveFlashSales(md(), &promotionv1.ListActiveFlashSalesRequest{}); err != nil {
		t.Fatalf("public ListActiveFlashSales failed: %v", err)
	}
	if resp, err := client.ListAvailableVouchers(md(), &promotionv1.ListAvailableVouchersRequest{}); err != nil || len(resp.GetVouchers()) != 1 {
		t.Fatalf("public ListAvailableVouchers failed: resp=%+v err=%v", resp, err)
	}
	if _, err := client.ListAvailableVouchers(md(), &promotionv1.ListAvailableVouchersRequest{UserId: "buyer-a"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anonymous ListAvailableVouchers with user_id: want Unauthenticated, got %v", err)
	}

	// 3. Cross-user calls (buyer-b targeting buyer-a) MUST fail with PermissionDenied.
	crossCtx := bearer(buyerBTok, "x-user-id", "buyer-a", "x-user-role", "admin")
	if _, err := client.SaveVoucher(crossCtx, &promotionv1.SaveVoucherRequest{UserId: "buyer-a", VoucherId: platV.Voucher.Id}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cross-user SaveVoucher: want PermissionDenied, got %v", err)
	}
	if _, err := client.GetCoinBalance(crossCtx, &promotionv1.GetCoinBalanceRequest{UserId: "buyer-a"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cross-user GetCoinBalance: want PermissionDenied, got %v", err)
	}
	if _, err := client.PreviewDiscounts(crossCtx, &promotionv1.PreviewDiscountsRequest{UserId: "buyer-a", CoinsToUse: 1000}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cross-user PreviewDiscounts: want PermissionDenied, got %v", err)
	}
	if _, err := client.ReservePromotion(crossCtx, &promotionv1.ReservePromotionRequest{
		OrderId: "cross-ord", UserId: "buyer-a", CoinsToUse: 1000,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cross-user ReservePromotion: want PermissionDenied, got %v", err)
	}
	if _, err := client.ListAvailableVouchers(crossCtx, &promotionv1.ListAvailableVouchersRequest{UserId: "buyer-a"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cross-user ListAvailableVouchers: want PermissionDenied, got %v", err)
	}

	// 4. Buyer JWT CANNOT call admin/internal RPCs including CreditCoins and ReleasePromotion.
	if _, err := client.CreditCoins(bearer(buyerATok), &promotionv1.CreditCoinsRequest{UserId: "buyer-a", Amount: 5000}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("buyer CreditCoins: want PermissionDenied, got %v", err)
	}
	if _, err := client.ReleasePromotion(bearer(buyerATok), &promotionv1.ReleasePromotionRequest{OrderId: "some-order"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("buyer ReleasePromotion: want PermissionDenied, got %v", err)
	}
	if _, err := client.EndFlashSaleWindow(bearer(buyerATok), &promotionv1.EndFlashSaleWindowRequest{WindowId: "some-window"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("buyer EndFlashSaleWindow: want PermissionDenied, got %v", err)
	}
	if _, err := client.EndFlashSaleWindow(bearer(sellerATok), &promotionv1.EndFlashSaleWindowRequest{WindowId: "some-window"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("seller EndFlashSaleWindow: want PermissionDenied, got %v", err)
	}

	// 5. Seller JWT + forged x-shop-id: shop-b creates voucher only for shop-a; targeting shop-b in body -> PermissionDenied.
	createdV, err := client.CreateVoucher(bearer(sellerATok, "x-shop-id", "shop-b", "x-user-role", "admin"), &promotionv1.CreateVoucherRequest{
		Code:         "SELLERAOK",
		Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP,
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
		ValueVnd:     5000,
		Quota:        5,
		StartsAt:     timestamppb.New(now.Add(-time.Hour)),
		EndsAt:       timestamppb.New(now.Add(time.Hour)),
	})
	if err != nil {
		t.Fatalf("seller-a CreateVoucher: %v", err)
	}
	if createdV.GetVoucher().GetShopId() != "shop-a" {
		t.Fatalf("expected shop-a (forged x-shop-id ignored), got %q", createdV.GetVoucher().GetShopId())
	}
	if _, err := client.CreateVoucher(bearer(sellerATok, "x-shop-id", "shop-b"), &promotionv1.CreateVoucherRequest{
		Code:         "SELLERBBAD",
		Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP,
		ShopId:       "shop-b",
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
		ValueVnd:     5000,
		Quota:        5,
		StartsAt:     timestamppb.New(now.Add(-time.Hour)),
		EndsAt:       timestamppb.New(now.Add(time.Hour)),
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("seller-a CreateVoucher for shop-b: want PermissionDenied, got %v", err)
	}

	// 6. Service token: rejected on non-allowed RPCs, ALLOWED on ReleasePromotion!
	for name, call := range map[string]func() error{
		"CreditCoins": func() error {
			_, e := client.CreditCoins(bearer(svcTok), &promotionv1.CreditCoinsRequest{UserId: "buyer-a", Amount: 100})
			return e
		},
		"ReservePromotion": func() error {
			_, e := client.ReservePromotion(bearer(svcTok), &promotionv1.ReservePromotionRequest{OrderId: "svc-ord", UserId: "buyer-a"})
			return e
		},
		"GetCoinBalance": func() error {
			_, e := client.GetCoinBalance(bearer(svcTok), &promotionv1.GetCoinBalanceRequest{UserId: "buyer-a"})
			return e
		},
		"SaveVoucher": func() error {
			_, e := client.SaveVoucher(bearer(svcTok), &promotionv1.SaveVoucherRequest{UserId: "buyer-a", VoucherId: platV.Voucher.Id})
			return e
		},
		"PreviewDiscounts": func() error {
			_, e := client.PreviewDiscounts(bearer(svcTok), &promotionv1.PreviewDiscountsRequest{UserId: "buyer-a"})
			return e
		},
		"CreateVoucher": func() error {
			_, e := client.CreateVoucher(bearer(svcTok), &promotionv1.CreateVoucherRequest{
				Code: "SVCBAD", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
				DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 1000, Quota: 1,
				StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(time.Hour)),
			})
			return e
		},
		"EndFlashSaleWindow": func() error {
			_, e := client.EndFlashSaleWindow(bearer(svcTok), &promotionv1.EndFlashSaleWindowRequest{WindowId: "fs-1"})
			return e
		},
	} {
		if err := call(); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("service token on %s: want PermissionDenied, got %v", name, err)
		}
	}

	// Reserve as buyer-a, then ReleasePromotion with valid service token succeeds and restores coins!
	if _, err := client.ReservePromotion(bearer(buyerATok), &promotionv1.ReservePromotionRequest{
		OrderId:    "svc-release-ord",
		UserId:     "buyer-a",
		CoinsToUse: 4000,
	}); err != nil {
		t.Fatalf("buyer-a ReservePromotion: %v", err)
	}
	if mem.AccountBalance("buyer-a") != 16000 {
		t.Fatalf("expected buyer-a balance 16000 after reserve, got %d", mem.AccountBalance("buyer-a"))
	}
	relResp, err := client.ReleasePromotion(bearer(svcTok), &promotionv1.ReleasePromotionRequest{OrderId: "svc-release-ord"})
	if err != nil || !relResp.GetReleased() {
		t.Fatalf("service token ReleasePromotion failed: resp=%+v err=%v", relResp, err)
	}
	if mem.AccountBalance("buyer-a") != 20000 {
		t.Fatalf("expected buyer-a balance restored to 20000 after service ReleasePromotion, got %d", mem.AccountBalance("buyer-a"))
	}

	// Admin JWT CAN call EndFlashSaleWindow.
	fs, err := client.CreateFlashSale(bearer(adminTok), &promotionv1.CreateFlashSaleRequest{
		Name:     "Trust Window",
		StartsAt: timestamppb.New(now.Add(-time.Minute)),
		EndsAt:   timestamppb.New(now.Add(time.Hour)),
	})
	if err != nil {
		t.Fatalf("admin CreateFlashSale: %v", err)
	}
	endResp, err := client.EndFlashSaleWindow(bearer(adminTok), &promotionv1.EndFlashSaleWindowRequest{
		WindowId: fs.GetFlashSale().GetId(),
	})
	if err != nil || endResp.GetWindow().GetStatus() != "ENDED" {
		t.Fatalf("admin EndFlashSaleWindow failed: resp=%+v err=%v", endResp, err)
	}
}
