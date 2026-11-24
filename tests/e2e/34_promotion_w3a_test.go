package e2e

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
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
)

func prmW3aDialPromotion(t *testing.T, env *testEnv) promotionv1.PromotionServiceClient {
	t.Helper()
	conn, err := grpc.NewClient(env.cfg.PromotionGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial PromotionService gRPC %s: %v", env.cfg.PromotionGRPCAddr, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return promotionv1.NewPromotionServiceClient(conn)
}

func prmW3aBearerCtx(token string) (context.Context, context.CancelFunc) {
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))
	return context.WithTimeout(ctx, 20*time.Second)
}

// TestE2E_PromotionW3A_D3_ReserveOrderServiceOnly_AndCheckoutWithVoucher verifies:
//  1. Direct gRPC PromotionService.ReservePromotion with buyer JWT (+ spoofed price), admin JWT, or anonymous
//     is rejected (PermissionDenied / Unauthenticated) and leaves voucher quota untouched.
//  2. Direct gRPC PromotionService.PreviewDiscounts ignores spoofed client price/subtotal and resolves
//     authoritative SKU pricing from ProductService (D4).
//  3. Real end-to-end checkout via gateway POST /api/v1/checkout with a shop voucher succeeds via
//     order-service's internal service token and applies the exact voucher discount.
func TestE2E_PromotionW3A_D3_ReserveOrderServiceOnly_AndCheckoutWithVoucher(t *testing.T) {
	env := getEnv()
	adminTok := env.adminLogin(t)
	cat, seller1, _ := env.ensureBaseMarketplace(t)
	buyer := env.registerVerifyAndLogin(t, "buyer-prm3a")
	env.verifyBuyerPhone(t, buyer)
	addr := env.addAddress(t, buyer)

	const (
		skuPriceVND     int64 = 200000
		voucherValueVND int64 = 30000
		voucherQuota    int32 = 5
	)
	_, sku := env.createApprovedProduct(t, seller1, cat.ID, "W3A Promo Test Item", skuPriceVND, 20, 400)
	voucher := env.createShopVoucher(t, seller1, voucherValueVND, 100000, voucherQuota)
	env.saveVoucher(t, buyer.AccessToken, voucher)

	promoClient := prmW3aDialPromotion(t, env)
	buyerCtx, buyerCancel := prmW3aBearerCtx(buyer.AccessToken)
	defer buyerCancel()
	adminCtx, adminCancel := prmW3aBearerCtx(adminTok)
	defer adminCancel()
	anonCtx, anonCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer anonCancel()

	spoofReq := &promotionv1.ReservePromotionRequest{
		OrderId:        "e2e-w3a-spoof-" + uuid.NewString(),
		UserId:         buyer.UserID,
		ShopVoucherIds: map[string]string{seller1.ShopID: voucher.ID},
		ShopItems: []*promotionv1.ShopDiscountInput{
			{
				ShopId:         seller1.ShopID,
				SubtotalVnd:    9999999,
				ShippingFeeVnd: 20000,
				Items: []*promotionv1.DiscountLineItem{
					{
						SkuId:        sku.ID,
						Quantity:     1,
						UnitPriceVnd: 9999999,
					},
				},
			},
		},
	}

	// 1. Direct gRPC ReservePromotion with buyer JWT + spoofed price MUST fail PermissionDenied.
	if resp, err := promoClient.ReservePromotion(buyerCtx, spoofReq); status.Code(err) != codes.PermissionDenied || resp != nil {
		t.Fatalf("direct buyer gRPC ReservePromotion: want PermissionDenied & nil resp, got resp=%+v err=%v", resp, err)
	}
	// Admin JWT MUST also be rejected (D3: order-service only).
	if resp, err := promoClient.ReservePromotion(adminCtx, spoofReq); status.Code(err) != codes.PermissionDenied || resp != nil {
		t.Fatalf("direct admin gRPC ReservePromotion: want PermissionDenied & nil resp, got resp=%+v err=%v", resp, err)
	}
	// Other internal service token (payment-service != order-service) MUST be rejected with PermissionDenied (D3).
	paySvcCtx, paySvcCancel := prmW3aBearerCtx(promoMintServiceToken("payment-service"))
	defer paySvcCancel()
	if resp, err := promoClient.ReservePromotion(paySvcCtx, spoofReq); status.Code(err) != codes.PermissionDenied || resp != nil {
		t.Fatalf("payment-service gRPC ReservePromotion: want PermissionDenied & nil resp, got resp=%+v err=%v", resp, err)
	}
	// Anonymous caller MUST fail Unauthenticated.
	if resp, err := promoClient.ReservePromotion(anonCtx, spoofReq); status.Code(err) != codes.Unauthenticated || resp != nil {
		t.Fatalf("direct anon gRPC ReservePromotion: want Unauthenticated & nil resp, got resp=%+v err=%v", resp, err)
	}

	// Voucher remaining quota MUST remain completely untouched at initial quota.
	if rem := env.getVoucherRemainingQuota(t, buyer.AccessToken, "SHOP", voucher.ID); rem != voucherQuota {
		t.Fatalf("voucher quota mutated after rejected gRPC ReservePromotion: got %d want %d", rem, voucherQuota)
	}

	// 2. Direct gRPC PreviewDiscounts with spoofed price/subtotal MUST resolve real SKU price (200k).
	prevResp, err := promoClient.PreviewDiscounts(buyerCtx, &promotionv1.PreviewDiscountsRequest{
		UserId:         buyer.UserID,
		ShopVoucherIds: map[string]string{seller1.ShopID: voucher.ID},
		ShopItems: []*promotionv1.ShopDiscountInput{
			{
				ShopId:         seller1.ShopID,
				SubtotalVnd:    9999999,
				ShippingFeeVnd: 20000,
				Items: []*promotionv1.DiscountLineItem{
					{
						SkuId:        sku.ID,
						Quantity:     1,
						UnitPriceVnd: 9999999,
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("direct gRPC PreviewDiscounts: %v", err)
	}
	if prevResp.GetBreakdown().GetTotalSubtotalVnd() != skuPriceVND {
		t.Fatalf("PreviewDiscounts trusted spoofed price: got subtotal=%d want %d", prevResp.GetBreakdown().GetTotalSubtotalVnd(), skuPriceVND)
	}
	if prevResp.GetBreakdown().GetTotalShopVoucherDiscountVnd() != voucherValueVND {
		t.Fatalf("PreviewDiscounts shop voucher discount: got %d want %d", prevResp.GetBreakdown().GetTotalShopVoucherDiscountVnd(), voucherValueVND)
	}

	// 2b. Direct gRPC PreviewDiscounts with a HIDDEN product's SKU MUST fail FailedPrecondition (SKU_INACTIVE) per D2/D4.
	hiddenProd, hiddenSKU := env.createApprovedProduct(t, seller1, cat.ID, "W3A Promo Hidden Item", 180000, 10, 300)
	env.mustDoJSON(t, http.MethodPut, fmt.Sprintf("/api/v1/seller/products/%s", url.PathEscape(hiddenProd.ID)),
		seller1.AccessToken, nil, map[string]string{"status": "HIDDEN"}, http.StatusOK)
	if _, err := promoClient.PreviewDiscounts(buyerCtx, &promotionv1.PreviewDiscountsRequest{
		UserId: buyer.UserID,
		ShopItems: []*promotionv1.ShopDiscountInput{
			{
				ShopId:         seller1.ShopID,
				ShippingFeeVnd: 20000,
				Items:          []*promotionv1.DiscountLineItem{{SkuId: hiddenSKU.ID, Quantity: 1}},
			},
		},
	}); status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "SKU_INACTIVE") {
		t.Fatalf("PreviewDiscounts on HIDDEN SKU: want FailedPrecondition SKU_INACTIVE, got %v", err)
	}

	// 3. Real checkout through gateway POST /api/v1/checkout with the shop voucher succeeds end-to-end.
	quote := env.calculateShippingQuote(t, buyer.AccessToken, seller1.ShopID, 400, skuPriceVND)
	checkoutResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
		"Idempotency-Key": "e2e-w3a-checkout-" + uuid.NewString(),
	}, CheckoutRequest{
		AddressID:      addr.ID,
		FromCart:       false,
		Items:          []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 1}},
		ShopVoucherIDs: map[string]string{seller1.ShopID: voucher.ID},
		PaymentMethod:  "COD",
		ShippingChoices: []ShopShippingChoiceDTO{
			{
				ShopID:      seller1.ShopID,
				CarrierCode: quote.CarrierCode,
				ServiceCode: quote.ServiceCode,
			},
		},
	}, http.StatusCreated, http.StatusOK)

	type w3aCheckoutOrderDTO struct {
		SubtotalVND                int64 `json:"subtotal_vnd"`
		FlashSaleDiscountVND       int64 `json:"flash_sale_discount_vnd"`
		ShopProgramDiscountVND     int64 `json:"shop_program_discount_vnd"`
		ShopVoucherDiscountVND     int64 `json:"shop_voucher_discount_vnd"`
		ShopFreeshipDiscountVND    int64 `json:"shop_freeship_discount_vnd"`
		PlatformVoucherDiscountVND int64 `json:"platform_voucher_discount_vnd"`
		FreeshipDiscountVND        int64 `json:"freeship_discount_vnd"`
		CoinsDiscountVND           int64 `json:"coins_discount_vnd"`
		ShippingFeeVND             int64 `json:"shipping_fee_vnd"`
		TotalPayableVND            int64 `json:"total_payable_vnd"`
	}
	var out struct {
		CheckoutGroupID string                `json:"checkout_group_id"`
		Orders          []w3aCheckoutOrderDTO `json:"orders"`
	}
	if err := DecodeJSON(checkoutResp, &out); err != nil {
		t.Fatalf("decode checkout response: %v", err)
	}
	if out.CheckoutGroupID == "" || len(out.Orders) != 1 {
		t.Fatalf("unexpected checkout response: %+v", out)
	}
	ord := out.Orders[0]
	if ord.SubtotalVND != skuPriceVND {
		t.Fatalf("checkout order subtotal: got %d want %d", ord.SubtotalVND, skuPriceVND)
	}
	if ord.ShopVoucherDiscountVND != voucherValueVND {
		t.Fatalf("checkout order shop_voucher_discount_vnd: got %d want %d", ord.ShopVoucherDiscountVND, voucherValueVND)
	}
	wantPayable := ord.SubtotalVND -
		ord.FlashSaleDiscountVND -
		ord.ShopProgramDiscountVND -
		ord.ShopVoucherDiscountVND -
		ord.ShopFreeshipDiscountVND -
		ord.PlatformVoucherDiscountVND -
		ord.FreeshipDiscountVND -
		ord.CoinsDiscountVND +
		ord.ShippingFeeVND
	if ord.TotalPayableVND != wantPayable {
		t.Fatalf("checkout order total_payable_vnd: got %d want %d (order=%+v)", ord.TotalPayableVND, wantPayable, ord)
	}
	if rem := env.getVoucherRemainingQuota(t, buyer.AccessToken, "SHOP", voucher.ID); rem != voucherQuota-1 {
		t.Fatalf("voucher remaining quota after real checkout: got %d want %d", rem, voucherQuota-1)
	}
}
