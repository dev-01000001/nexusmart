package e2e

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// promoMintServiceToken mints a short-lived internal service token matching pkg/common/auth.SignServiceToken
// (ServiceRole = "admin", service = serviceName) for direct internal gRPC calls.
func promoMintServiceToken(serviceName string) string {
	secret := strings.TrimSpace(os.Getenv("SERVICE_JWT_SECRET"))
	if secret == "" {
		secret = "dev-only-nexusmart-service-hs256-secret-change-me"
	}
	enc := base64.RawURLEncoding
	now := time.Now().Unix()
	claims, _ := json.Marshal(map[string]any{
		"sub":     serviceName,
		"role":    "admin",
		"service": serviceName,
		"iss":     "nexusmart",
		"jti":     uuid.NewString(),
		"iat":     now,
		"exp":     now + 300,
	})
	unsigned := enc.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." + enc.EncodeToString(claims)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(unsigned))
	return unsigned + "." + enc.EncodeToString(mac.Sum(nil))
}

// promoOrderServiceToken mints a short-lived internal service token identifying order-service
// for direct gRPC PromotionService.ReservePromotion calls (D3: restricted to order-service).
func promoOrderServiceToken() string {
	return promoMintServiceToken("order-service")
}

func TestE2E_FlashSale_1000Concurrent_100Slots(t *testing.T) {
	env := getEnv()
	adminTok := env.adminLogin(t)
	cat, seller1, _ := env.ensureBaseMarketplace(t)

	// 1. Admin creates an active flash sale window.
	now := time.Now().UTC()
	fsResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/flash-sales", adminTok, nil, CreateFlashSaleRequest{
		Name:     "E2E Mega Flash Sale " + env.runID,
		StartsAt: now.Add(-5 * time.Minute).Format(time.RFC3339),
		EndsAt:   now.Add(2 * time.Hour).Format(time.RFC3339),
	}, http.StatusCreated, http.StatusOK)

	var fsOut FlashSaleResponse
	if err := DecodeJSON(fsResp, &fsOut); err != nil {
		t.Fatalf("decode create flash sale: %v", err)
	}
	if fsOut.FlashSale.ID == "" {
		t.Fatalf("create flash sale returned empty id: %s", string(fsResp.Body))
	}
	flashSaleID := fsOut.FlashSale.ID

	// 2. Part A: 1,000 concurrent ReservePromotion requests competing for EXACTLY 100 slots.
	t.Run("1000Concurrent_100Slots_ZeroOversell", func(t *testing.T) {
		_, sku := env.createApprovedProduct(t, seller1, cat.ID, "Flash Sale Smartphone E2E", 500000, 500, 400)

		regPath := fmt.Sprintf("/api/v1/seller/flash-sales/%s/items", url.PathEscape(flashSaleID))
		regResp := env.mustDoJSON(t, http.MethodPost, regPath, seller1.AccessToken, nil, RegisterFlashSaleItemRequest{
			SKUID:         sku.ID,
			FlashPriceVND: 199000,
			TotalSlots:    100,
			PerUserLimit:  1,
		}, http.StatusCreated, http.StatusOK)
		var regOut FlashSaleItemResponse
		if err := DecodeJSON(regResp, &regOut); err != nil {
			t.Fatalf("decode register flash sale item: %v", err)
		}
		itemID := regOut.Item.ID
		if itemID == "" {
			t.Fatalf("register flash sale item returned empty id: %s", string(regResp.Body))
		}

		approvePath := fmt.Sprintf("/api/v1/admin/flash-sale-items/%s/approve", url.PathEscape(itemID))
		approveResp := env.mustDoJSON(t, http.MethodPost, approvePath, adminTok, nil, ApproveFlashSaleItemRequest{
			Approve: true,
			Reason:  "E2E flash sale concurrency test",
		}, http.StatusOK)
		var approveOut FlashSaleItemResponse
		if err := DecodeJSON(approveResp, &approveOut); err != nil {
			t.Fatalf("decode approve flash sale item: %v", err)
		}
		if !StatusEquals(approveOut.Item.Status, "APPROVED") {
			t.Fatalf("expected flash sale item APPROVED, got %q", approveOut.Item.Status)
		}

		// Connect directly to PromotionService gRPC (reachable only inside the compose network, see `make e2e`)
		// so Gateway IP rate-limit does not mask concurrency.
		conn, err := grpc.NewClient(env.cfg.PromotionGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Fatalf("dial PromotionService gRPC %s: %v", env.cfg.PromotionGRPCAddr, err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		promoClient := promotionv1.NewPromotionServiceClient(conn)

		const (
			totalRequests = 1000
			expectedSlots = 100
		)
		var succeeded atomic.Int64
		var failed atomic.Int64
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(totalRequests)

		runPrefix := uuid.NewString()[:8]
		orderSvcTok := promoOrderServiceToken()
		for i := 0; i < totalRequests; i++ {
			go func(idx int) {
				defer wg.Done()
				<-start

				userID := fmt.Sprintf("e2e-fs-user-%s-%04d", runPrefix, idx)
				orderID := fmt.Sprintf("e2e-fs-order-%s-%04d", runPrefix, idx)
				// ReservePromotion is restricted to internal order-service service tokens (D3).
				callCtx, cancel := context.WithTimeout(
					metadata.NewOutgoingContext(context.Background(), metadata.Pairs(
						"authorization", "Bearer "+orderSvcTok)),
					20*time.Second,
				)
				defer cancel()

				resp, rpcErr := promoClient.ReservePromotion(callCtx, &promotionv1.ReservePromotionRequest{
					OrderId: orderID,
					UserId:  userID,
					FlashSaleItems: []*promotionv1.FlashSaleReservationItem{
						{
							FlashSaleItemId: itemID,
							SkuId:           sku.ID,
							Quantity:        1,
						},
					},
					ShopItems: []*promotionv1.ShopDiscountInput{
						{
							ShopId:      seller1.ShopID,
							SubtotalVnd: 500000,
							Items: []*promotionv1.DiscountLineItem{
								{
									SkuId:           sku.ID,
									Quantity:        1,
									UnitPriceVnd:    500000,
									FlashSaleItemId: itemID,
								},
							},
						},
					},
				})
				if rpcErr == nil && resp.GetReserved() {
					succeeded.Add(1)
				} else {
					failed.Add(1)
				}
			}(i)
		}

		t0 := time.Now()
		close(start)
		wg.Wait()
		elapsed := time.Since(t0)

		gotSucc := succeeded.Load()
		gotFail := failed.Load()
		t.Logf("FlashSale 1000 concurrent requests completed in %s: succeeded=%d failed=%d", elapsed, gotSucc, gotFail)

		if gotSucc != expectedSlots {
			t.Fatalf("OVERSELL/UNDERSELL VIOLATION: expected EXACTLY %d succeeded reservations out of %d, got succeeded=%d failed=%d",
				expectedSlots, totalRequests, gotSucc, gotFail)
		}
		if gotFail != totalRequests-expectedSlots {
			t.Fatalf("expected EXACTLY %d failed reservations, got %d", totalRequests-expectedSlots, gotFail)
		}

		// Verify remaining_slots via Gateway is exactly 0 (never negative).
		listResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/flash-sales", "", nil, nil, http.StatusOK)
		var listOut FlashSalesListResponse
		if err := DecodeJSON(listResp, &listOut); err != nil {
			t.Fatalf("decode list flash sales: %v", err)
		}
		found := false
		for _, fs := range listOut.FlashSales {
			for _, it := range fs.Items {
				if it.ID == itemID {
					found = true
					if it.RemainingSlots != 0 {
						t.Fatalf("expected remaining_slots=0 after 100 slots sold, got %d", it.RemainingSlots)
					}
				}
			}
		}
		if !found {
			t.Fatalf("flash sale item %s not found in GET /api/v1/flash-sales", itemID)
		}
	})

	// 3. Part B: Real Gateway checkout purchasing a flash-sale item at flash price.
	t.Run("RealCheckout_AtFlashPrice", func(t *testing.T) {
		const (
			normalPrice = int64(400000)
			flashPrice  = int64(150000)
		)
		_, sku := env.createApprovedProduct(t, seller1, cat.ID, "Flash Sale Earbuds E2E", normalPrice, 20, 250)

		regPath := fmt.Sprintf("/api/v1/seller/flash-sales/%s/items", url.PathEscape(flashSaleID))
		regResp := env.mustDoJSON(t, http.MethodPost, regPath, seller1.AccessToken, nil, RegisterFlashSaleItemRequest{
			SKUID:         sku.ID,
			FlashPriceVND: flashPrice,
			TotalSlots:    5,
			PerUserLimit:  2,
		}, http.StatusCreated, http.StatusOK)
		var regOut FlashSaleItemResponse
		if err := DecodeJSON(regResp, &regOut); err != nil {
			t.Fatalf("decode register flash checkout item: %v", err)
		}
		itemID := regOut.Item.ID

		approvePath := fmt.Sprintf("/api/v1/admin/flash-sale-items/%s/approve", url.PathEscape(itemID))
		env.mustDoJSON(t, http.MethodPost, approvePath, adminTok, nil, ApproveFlashSaleItemRequest{
			Approve: true,
			Reason:  "E2E flash checkout test",
		}, http.StatusOK)

		buyer := env.registerVerifyAndLogin(t, "buyer-flash")
		addr := env.addAddress(t, buyer)
		quote := env.calculateShippingQuote(t, buyer.AccessToken, seller1.ShopID, 250, 0)

		// Wave 2b (w2b-plan §2): orders containing flash sale items must reject COD.
		codResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "fs-checkout-cod-" + uuid.NewString(),
		}, CheckoutRequest{
			SalesChannel: "MARKETPLACE",
			AddressID:    addr.ID,
			FromCart:     false,
			Items: []CheckoutItemInputDTO{
				{
					SKUID:           sku.ID,
					Quantity:        1,
					FlashSaleItemID: itemID,
				},
			},
			PaymentMethod: "COD",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
			},
		}, http.StatusConflict)
		if !strings.Contains(string(codResp.Body), "FLASH_SALE_COD_NOT_ALLOWED") {
			t.Fatalf("expected FLASH_SALE_COD_NOT_ALLOWED when checking out flash item with COD, got body=%s", string(codResp.Body))
		}

		// Real flash-sale checkout via VIETQR: awaiting payment with short expiry <= 5 minutes.
		beforeCheckout := time.Now().UTC()
		checkoutResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "fs-checkout-" + uuid.NewString(),
		}, CheckoutRequest{
			SalesChannel: "MARKETPLACE",
			AddressID:    addr.ID,
			FromCart:     false,
			Items: []CheckoutItemInputDTO{
				{
					SKUID:           sku.ID,
					Quantity:        1,
					FlashSaleItemID: itemID,
				},
			},
			PaymentMethod: "VIETQR",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
			},
		}, http.StatusCreated, http.StatusOK)
		afterCheckout := time.Now().UTC()

		var checkout CheckoutResponse
		if err := DecodeJSON(checkoutResp, &checkout); err != nil {
			t.Fatalf("decode flash checkout: %v", err)
		}
		if len(checkout.Orders) != 1 {
			t.Fatalf("expected 1 order for flash checkout, got %d", len(checkout.Orders))
		}
		if checkout.PaymentID == "" {
			t.Fatalf("expected non-empty payment_id for VIETQR flash checkout: %s", string(checkoutResp.Body))
		}
		ord := checkout.Orders[0]
		if !StatusEquals(ord.Status, "PENDING") {
			t.Fatalf("expected VIETQR flash order status PENDING (awaiting payment), got %q", ord.Status)
		}
		if !StatusEquals(ord.PaymentStatus, "PENDING") || !StatusEquals(checkout.PaymentStatus, "PENDING") {
			t.Fatalf("expected VIETQR flash payment status PENDING, got order=%q checkout=%q", ord.PaymentStatus, checkout.PaymentStatus)
		}
		if checkout.PaymentExpiresAt == "" {
			t.Fatalf("expected non-empty payment_expires_at for VIETQR flash checkout")
		}
		expiresAt, err := time.Parse(time.RFC3339, checkout.PaymentExpiresAt)
		if err != nil {
			t.Fatalf("parse payment_expires_at %q: %v", checkout.PaymentExpiresAt, err)
		}
		if !expiresAt.After(beforeCheckout) || expiresAt.After(afterCheckout.Add(5*time.Minute)) {
			t.Fatalf("expected flash payment_expires_at within (0, 5m] from checkout, got expires_at=%s (checkout window [%s, %s])",
				expiresAt.Format(time.RFC3339), beforeCheckout.Format(time.RFC3339), afterCheckout.Format(time.RFC3339))
		}

		expectedDiscount := normalPrice - flashPrice
		expectedPayable := flashPrice + ord.ShippingFeeVND
		if ord.FlashSaleDiscountVND != expectedDiscount && ord.TotalPayableVND != expectedPayable {
			t.Fatalf("flash price not applied: normal=%d flash=%d shipping=%d got flash_discount=%d total_payable=%d (order=%+v)",
				normalPrice, flashPrice, ord.ShippingFeeVND, ord.FlashSaleDiscountVND, ord.TotalPayableVND, ord)
		}
		if ord.TotalPayableVND != expectedPayable {
			t.Fatalf("expected flash order total_payable_vnd=%d (flash=%d + shipping=%d), got %d",
				expectedPayable, flashPrice, ord.ShippingFeeVND, ord.TotalPayableVND)
		}

		// Verify remaining_slots decremented from 5 to 4.
		listResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/flash-sales", "", nil, nil, http.StatusOK)
		var listOut FlashSalesListResponse
		if err := DecodeJSON(listResp, &listOut); err != nil {
			t.Fatalf("decode list flash sales after checkout: %v", err)
		}
		for _, fs := range listOut.FlashSales {
			for _, it := range fs.Items {
				if it.ID == itemID {
					if it.RemainingSlots != 4 {
						t.Fatalf("expected remaining_slots=4 after 1 flash checkout, got %d", it.RemainingSlots)
					}
				}
			}
		}
	})
}
