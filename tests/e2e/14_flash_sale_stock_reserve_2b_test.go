package e2e

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// TestE2E_FlashSaleStockReserve_Phase2b verifies the merged flash-sale stock & concurrency contract
// (per DECISIONS.md: flash-sale stock pre-locking is removed so flash_sale_reserved_stock stays 0,
// stock is reserved atomically at checkout time, COD is forbidden on flash sales, and EndFlashSaleWindow
// ends a window idempotently):
// (a) registering or approving N slots when SKU available_stock < N is rejected cleanly with flash_reserved=0;
// (b) approving N=100 slots on stock=105 keeps flash_reserved=0 while regular (MARKETPLACE & STOREFRONT)
//
//	checkouts share stock;
//
// (c) 1,000 concurrent real VIETQR flash sale checkout requests against N=100 slots produce EXACTLY
//
//	100 successes & 900 failures (zero oversell);
//
// (d) COD flash checkout is rejected (FLASH_SALE_COD_NOT_ALLOWED), VIETQR flash checkout succeeds,
//
//	ending a window via POST /api/v1/admin/flash-sales/{id}/end is idempotent, blocks new flash checkouts,
//	and leaves remaining units immediately buyable via regular checkout;
//
// (e) double-entry ledger remains 100% balanced across all channels.
func TestE2E_FlashSaleStockReserve_Phase2b(t *testing.T) {
	env := getEnv()
	ctx := env.ctx(t)

	resetRL := func() {
		if env.cfg.RedisAddr == "" {
			return
		}
		rCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = ResetRateLimitKeys(rCtx, env.cfg.RedisAddr, env.cfg.RedisPassword)
	}
	resetRL()
	t.Cleanup(resetRL)

	adminTok := env.adminLogin(t)
	cat, seller1, _ := env.ensureBaseMarketplace(t)

	buyer := env.registerVerifyAndLogin(t, "buyer-fs2b")
	env.verifyBuyerPhone(t, buyer)
	addr := env.addAddress(t, buyer)
	quote := env.calculateShippingQuote(t, buyer.AccessToken, seller1.ShopID, 300, 250000)

	createActiveWindow := func(t *testing.T, label string) string {
		t.Helper()
		now := time.Now().UTC()
		fsResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/flash-sales", adminTok, nil, CreateFlashSaleRequest{
			Name:     fmt.Sprintf("E2E Phase2b %s %s", label, env.runID),
			StartsAt: now.Add(-5 * time.Minute).Format(time.RFC3339),
			EndsAt:   now.Add(2 * time.Hour).Format(time.RFC3339),
		}, http.StatusCreated, http.StatusOK)
		var fsOut FlashSaleResponse
		if err := DecodeJSON(fsResp, &fsOut); err != nil {
			t.Fatalf("decode create flash sale (%s): %v", label, err)
		}
		if fsOut.FlashSale.ID == "" {
			t.Fatalf("create flash sale (%s) returned empty id: %s", label, string(fsResp.Body))
		}
		return fsOut.FlashSale.ID
	}

	getFlashRemainingSlots := func(t *testing.T, itemID string) (int32, bool) {
		t.Helper()
		listResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/flash-sales", "", nil, nil, http.StatusOK)
		var listOut FlashSalesListResponse
		if err := DecodeJSON(listResp, &listOut); err != nil {
			t.Fatalf("decode list flash sales: %v", err)
		}
		for _, fs := range listOut.FlashSales {
			for _, it := range fs.Items {
				if it.ID == itemID {
					return it.RemainingSlots, true
				}
			}
		}
		return 0, false
	}

	window1ID := createActiveWindow(t, "Window1")

	// (a) Register or approve N flash sale slots when SKU available_stock < N -> rejected cleanly (4xx), stock untouched.
	t.Run("A_ApproveRejected_WhenAvailableStockInsufficient", func(t *testing.T) {
		prod, sku := env.createApprovedProduct(t, seller1, cat.ID, "Phase2b Insufficient Stock SKU", 200000, 20, 300)

		regPath := fmt.Sprintf("/api/v1/seller/flash-sales/%s/items", url.PathEscape(window1ID))
		// 1. Registering total_slots=50 > stock=20 is already rejected cleanly at registration.
		rejReg := env.mustDoJSON(t, http.MethodPost, regPath, seller1.AccessToken, nil, RegisterFlashSaleItemRequest{
			SKUID:         sku.ID,
			FlashPriceVND: 99000,
			TotalSlots:    50, // > 20 available stock!
			PerUserLimit:  2,
		}, http.StatusConflict, http.StatusBadRequest)
		t.Logf("Insufficient-stock flash registration rejected cleanly: HTTP %d body=%s", rejReg.StatusCode, string(rejReg.Body))

		afterRegRej := env.getProductSKU(t, prod.ID, sku.ID)
		if afterRegRej.Stock != 20 || afterRegRej.ReservedStock != 0 || afterRegRej.FlashSaleReservedStock != 0 || afterRegRej.AvailableStock != 20 {
			t.Fatalf("expected untouched SKU stock after rejected registration (stock=20 reserved=0 flash_reserved=0 avail=20), got %+v", afterRegRej)
		}

		// 2. Register 15 <= 20, reduce SKU stock to 10 before approval -> approval MUST reject cleanly.
		regResp := env.mustDoJSON(t, http.MethodPost, regPath, seller1.AccessToken, nil, RegisterFlashSaleItemRequest{
			SKUID:         sku.ID,
			FlashPriceVND: 99000,
			TotalSlots:    15,
			PerUserLimit:  2,
		}, http.StatusCreated, http.StatusOK)
		var regOut FlashSaleItemResponse
		if err := DecodeJSON(regResp, &regOut); err != nil {
			t.Fatalf("decode register flash sale item: %v", err)
		}

		stockPath := fmt.Sprintf("/api/v1/seller/skus/%s/stock", url.PathEscape(sku.ID))
		env.mustDoJSON(t, http.MethodPatch, stockPath, seller1.AccessToken, nil, map[string]any{
			"new_stock":        10,
			"expected_version": afterRegRej.Version,
		}, http.StatusOK)

		approvePath := fmt.Sprintf("/api/v1/admin/flash-sale-items/%s/approve", url.PathEscape(regOut.Item.ID))
		rejResp := env.mustDoJSON(t, http.MethodPost, approvePath, adminTok, nil, ApproveFlashSaleItemRequest{
			Approve: true,
			Reason:  "E2E insufficient stock test",
		}, http.StatusConflict, http.StatusBadRequest)
		t.Logf("Insufficient-stock flash approval rejected cleanly: HTTP %d body=%s", rejResp.StatusCode, string(rejResp.Body))

		after := env.getProductSKU(t, prod.ID, sku.ID)
		if after.Stock != 10 || after.ReservedStock != 0 || after.FlashSaleReservedStock != 0 || after.AvailableStock != 10 {
			t.Fatalf("expected untouched SKU stock after rejected approval (stock=10 reserved=0 flash_reserved=0 avail=10), got %+v", after)
		}
	})

	// (b) & (c) Approve N=100 on stock=105 (no pre-lock: flash_reserved stays 0; regular MARKETPLACE + STOREFRONT
	// orders consume 5 units leaving 100 units), then fire 1,000 concurrent real VIETQR flash sale checkouts
	// -> EXACTLY 100 succeed & 900 fail (zero oversell)!
	t.Run("BC_RegularBothChannels_And_1000ConcurrentFlashCheckouts", func(t *testing.T) {
		const (
			regularStock = int32(5)
			flashSlots   = int32(100)
			initialStock = regularStock + flashSlots // 105
			normalPrice  = int64(250000)
			flashPrice   = int64(99000)
		)
		prod, sku := env.createApprovedProduct(t, seller1, cat.ID, "Phase2b Concurrency Flash SKU", normalPrice, initialStock, 300)

		regPath := fmt.Sprintf("/api/v1/seller/flash-sales/%s/items", url.PathEscape(window1ID))
		regResp := env.mustDoJSON(t, http.MethodPost, regPath, seller1.AccessToken, nil, RegisterFlashSaleItemRequest{
			SKUID:         sku.ID,
			FlashPriceVND: flashPrice,
			TotalSlots:    flashSlots,
			PerUserLimit:  flashSlots,
		}, http.StatusCreated, http.StatusOK)
		var regOut FlashSaleItemResponse
		if err := DecodeJSON(regResp, &regOut); err != nil {
			t.Fatalf("decode register flash item: %v", err)
		}
		itemID := regOut.Item.ID

		// Approve flash item & verify idempotent re-approve leaves flash_sale_reserved_stock=0 (checkout-time reserve).
		approvePath := fmt.Sprintf("/api/v1/admin/flash-sale-items/%s/approve", url.PathEscape(itemID))
		env.mustDoJSON(t, http.MethodPost, approvePath, adminTok, nil, ApproveFlashSaleItemRequest{
			Approve: true,
			Reason:  "E2E Phase 2b approve 100 slots",
		}, http.StatusOK)
		env.mustDoJSON(t, http.MethodPost, approvePath, adminTok, nil, ApproveFlashSaleItemRequest{
			Approve: true,
			Reason:  "E2E Phase 2b idempotent re-approve",
		}, http.StatusOK)

		skuAfterApprove := env.getProductSKU(t, prod.ID, sku.ID)
		if skuAfterApprove.Stock != initialStock || skuAfterApprove.AvailableStock != initialStock || skuAfterApprove.FlashSaleReservedStock != 0 {
			t.Fatalf("expected stock=%d available=%d flash_reserved=0 after approve (no pre-lock), got %+v",
				initialStock, initialStock, skuAfterApprove)
		}

		// Regular MARKETPLACE order buys 3 units -> available drops 105 -> 102, flash_reserved stays 0.
		env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "p2b-reg-mkt-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID:     addr.ID,
			FromCart:      false,
			Items:         []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 3}},
			PaymentMethod: "COD",
			SalesChannel:  "MARKETPLACE",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
			},
		}, http.StatusCreated, http.StatusOK)

		skuMid := env.getProductSKU(t, prod.ID, sku.ID)
		if skuMid.AvailableStock != initialStock-3 || skuMid.FlashSaleReservedStock != 0 {
			t.Fatalf("after 3 regular MARKETPLACE units expected available=%d flash_reserved=0, got %+v", initialStock-3, skuMid)
		}

		// Regular STOREFRONT order buys 2 units -> available drops 102 -> 100, flash_reserved stays 0.
		env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "p2b-reg-sf-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID:        addr.ID,
			FromCart:         false,
			Items:            []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 2}},
			PaymentMethod:    "COD",
			SalesChannel:     "STOREFRONT",
			StorefrontShopID: seller1.ShopID,
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
			},
		}, http.StatusCreated, http.StatusOK)

		skuBeforeBurst := env.getProductSKU(t, prod.ID, sku.ID)
		if skuBeforeBurst.AvailableStock != flashSlots || skuBeforeBurst.FlashSaleReservedStock != 0 {
			t.Fatalf("after %d regular units expected available=%d flash_reserved=0, got %+v",
				regularStock, flashSlots, skuBeforeBurst)
		}

		remBeforeBurst, found := getFlashRemainingSlots(t, itemID)
		if !found || remBeforeBurst != flashSlots {
			t.Fatalf("expected flash sale item %s remaining_slots=%d intact before burst, found=%v got=%d",
				itemID, flashSlots, found, remBeforeBurst)
		}

		// (c) Fire 1,000 concurrent real VIETQR flash sale checkout requests (1 unit each) directly against
		// OrderService gRPC (bypassing Gateway IP rate limit) with bounded in-flight concurrency so shared
		// compose Postgres max_connections is never saturated while hammering Redis Lua + ReserveStock + Order.
		conn, err := grpc.NewClient(env.cfg.OrderGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Fatalf("dial OrderService gRPC %s: %v", env.cfg.OrderGRPCAddr, err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		orderClient := orderv1.NewOrderServiceClient(conn)

		const (
			totalRequests = 1000
			maxInFlight   = 24
		)
		var (
			succeeded    atomic.Int64
			failed       atomic.Int64
			errMu        sync.Mutex
			errCodeCount = make(map[codes.Code]int)
			start        = make(chan struct{})
			sem          = make(chan struct{}, maxInFlight)
			wg           sync.WaitGroup
		)
		wg.Add(totalRequests)
		runPrefix := uuid.NewString()[:8]

		for i := 0; i < totalRequests; i++ {
			go func(idx int) {
				defer wg.Done()
				<-start
				sem <- struct{}{}
				defer func() { <-sem }()

				callCtx, cancel := context.WithTimeout(
					metadata.NewOutgoingContext(context.Background(), metadata.Pairs(
						"authorization", "Bearer "+buyer.AccessToken,
					)),
					30*time.Second,
				)
				defer cancel()

				resp, rpcErr := orderClient.CreateCheckout(callCtx, &orderv1.CreateCheckoutRequest{
					IdempotencyKey:   fmt.Sprintf("p2b-burst-%s-%04d", runPrefix, idx),
					BuyerId:          buyer.UserID,
					AddressId:        addr.ID,
					FromCart:         false,
					SalesChannel:     "STOREFRONT",
					StorefrontShopId: seller1.ShopID,
					Items: []*orderv1.CheckoutItemInput{
						{
							SkuId:           sku.ID,
							Quantity:        1,
							FlashSaleItemId: itemID,
						},
					},
					PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
					ShippingChoices: []*orderv1.ShopShippingChoice{
						{
							ShopId:      seller1.ShopID,
							CarrierCode: quote.CarrierCode,
							ServiceCode: quote.ServiceCode,
						},
					},
				})
				if rpcErr == nil && len(resp.GetOrders()) == 1 {
					succeeded.Add(1)
					return
				}
				failed.Add(1)
				errMu.Lock()
				errCodeCount[status.Code(rpcErr)]++
				errMu.Unlock()
			}(i)
		}

		t0 := time.Now()
		close(start)
		wg.Wait()
		elapsed := time.Since(t0)

		gotSucc := succeeded.Load()
		gotFail := failed.Load()
		t.Logf("Phase 2b 1000 concurrent flash checkouts completed in %s: succeeded=%d failed=%d error_codes=%v",
			elapsed, gotSucc, gotFail, errCodeCount)

		if gotSucc != int64(flashSlots) || gotFail != int64(totalRequests)-int64(flashSlots) {
			t.Fatalf("OVERSELL/UNDERSELL VIOLATION: want succeeded=%d failed=%d, got succeeded=%d failed=%d (codes=%v)",
				flashSlots, totalRequests-int(flashSlots), gotSucc, gotFail, errCodeCount)
		}

		remAfterBurst, found := getFlashRemainingSlots(t, itemID)
		if !found || remAfterBurst != 0 {
			t.Fatalf("expected remaining_slots=0 after 1000 concurrent burst, found=%v got=%d", found, remAfterBurst)
		}

		skuAfterBurst := env.getProductSKU(t, prod.ID, sku.ID)
		if skuAfterBurst.FlashSaleReservedStock != 0 || skuAfterBurst.AvailableStock != 0 || (skuAfterBurst.Stock-skuAfterBurst.ReservedStock) != 0 {
			t.Fatalf("expected 0 remaining stock/flash_reserved/available after all 5 regular + 100 flash sold, got %+v", skuAfterBurst)
		}
	})

	// (d) COD flash checkout rejected + VietQR flash checkout + idempotent window end + post-end rejection + regular buy.
	t.Run("D_WindowEnd_Idempotent_BlocksFlash_And_RegularBuysRemaining", func(t *testing.T) {
		window2ID := createActiveWindow(t, "Window2Release")

		const (
			initialStock = int32(50)
			flashSlots   = int32(30)
			normalPrice  = int64(100000)
			flashPrice   = int64(60000)
			vqFlashQty   = int32(10)
		)
		prod, sku := env.createApprovedProduct(t, seller1, cat.ID, "Phase2b Window End SKU", normalPrice, initialStock, 250)

		regPath := fmt.Sprintf("/api/v1/seller/flash-sales/%s/items", url.PathEscape(window2ID))
		regResp := env.mustDoJSON(t, http.MethodPost, regPath, seller1.AccessToken, nil, RegisterFlashSaleItemRequest{
			SKUID:         sku.ID,
			FlashPriceVND: flashPrice,
			TotalSlots:    flashSlots,
			PerUserLimit:  20,
		}, http.StatusCreated, http.StatusOK)
		var regOut FlashSaleItemResponse
		if err := DecodeJSON(regResp, &regOut); err != nil {
			t.Fatalf("decode register release item: %v", err)
		}
		itemID := regOut.Item.ID

		approvePath := fmt.Sprintf("/api/v1/admin/flash-sale-items/%s/approve", url.PathEscape(itemID))
		env.mustDoJSON(t, http.MethodPost, approvePath, adminTok, nil, ApproveFlashSaleItemRequest{
			Approve: true,
			Reason:  "E2E Phase 2b window end test",
		}, http.StatusOK)

		skuApproved := env.getProductSKU(t, prod.ID, sku.ID)
		if skuApproved.AvailableStock != initialStock || skuApproved.FlashSaleReservedStock != 0 {
			t.Fatalf("expected available=%d flash_reserved=0 after approve, got %+v", initialStock, skuApproved)
		}

		// Flash sale forbids COD (main contract: FLASH_SALE_COD_NOT_ALLOWED -> 409).
		env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "p2b-rel-cod-rej-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID:        addr.ID,
			FromCart:         false,
			SalesChannel:     "STOREFRONT",
			StorefrontShopID: seller1.ShopID,
			Items: []CheckoutItemInputDTO{
				{SKUID: sku.ID, Quantity: 1, FlashSaleItemID: itemID},
			},
			PaymentMethod: "COD",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
			},
		}, http.StatusConflict, http.StatusBadRequest)

		// Buy 10 units via VietQR STOREFRONT flash sale checkout + settle HMAC webhook (also exercises double-entry ledger!).
		vqResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "p2b-rel-vq-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID:        addr.ID,
			FromCart:         false,
			SalesChannel:     "STOREFRONT",
			StorefrontShopID: seller1.ShopID,
			Items: []CheckoutItemInputDTO{
				{SKUID: sku.ID, Quantity: vqFlashQty, FlashSaleItemID: itemID},
			},
			PaymentMethod: "VIETQR",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
			},
		}, http.StatusCreated, http.StatusOK)
		var vqOut CheckoutResponse
		if err := DecodeJSON(vqResp, &vqOut); err != nil {
			t.Fatalf("decode flash VietQR checkout: %v", err)
		}
		if len(vqOut.Orders) != 1 || vqOut.PaymentID == "" {
			t.Fatalf("unexpected flash VietQR checkout response: %s", string(vqResp.Body))
		}
		whResp, err := env.client.PostSignedWebhook(ctx, "/webhooks/payment", PaymentWebhookPayload{
			Provider:     "vietqr_mock",
			ProviderTxID: "TXN-P2B-FS-" + uuid.NewString(),
			PaymentID:    vqOut.PaymentID,
			OrderGroupID: vqOut.CheckoutGroupID,
			AmountVND:    vqOut.TotalPayableVND,
			Status:       "SUCCEEDED",
			Reference:    "NEXUSMART " + vqOut.CheckoutGroupID,
			OccurredAt:   time.Now().UTC().Format(time.RFC3339),
		}, env.cfg.PaymentWebhookSecret, time.Now().UTC(), "")
		if err != nil || whResp.StatusCode != http.StatusOK {
			t.Fatalf("POST /webhooks/payment for flash VietQR failed: err=%v resp=%v", err, whResp)
		}
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "flash VietQR order CONFIRMED", func() (bool, string) {
			ord := env.getOrder(t, buyer.AccessToken, vqOut.Orders[0].ID)
			return StatusEquals(ord.Status, "CONFIRMED"), fmt.Sprintf("status=%s", ord.Status)
		})

		// Verify 10 consumed -> 40 available remaining, flash_reserved stays 0.
		skuBeforeEnd := env.getProductSKU(t, prod.ID, sku.ID)
		if skuBeforeEnd.FlashSaleReservedStock != 0 || skuBeforeEnd.AvailableStock != initialStock-vqFlashQty {
			t.Fatalf("expected flash_reserved=0 available=%d before ending window, got %+v", initialStock-vqFlashQty, skuBeforeEnd)
		}

		// End window via Admin endpoint -> marks ENDED & clears Redis slot keys.
		endPath := fmt.Sprintf("/api/v1/admin/flash-sales/%s/end", url.PathEscape(window2ID))
		endResp := env.mustDoJSON(t, http.MethodPost, endPath, adminTok, nil, nil, http.StatusOK)
		var endOut EndFlashSaleWindowResponse
		if err := DecodeJSON(endResp, &endOut); err != nil {
			t.Fatalf("decode end flash sale window: %v", err)
		}
		if endOut.ReleasedItems < 1 {
			t.Fatalf("expected released_items >= 1 when ending window, got %+v", endOut)
		}
		if !StatusEquals(endOut.FlashSale.Status, "ENDED") {
			t.Fatalf("expected ended flash sale status ENDED, got %q", endOut.FlashSale.Status)
		}

		skuAfterEnd := env.getProductSKU(t, prod.ID, sku.ID)
		if skuAfterEnd.FlashSaleReservedStock != 0 || skuAfterEnd.AvailableStock != 40 {
			t.Fatalf("expected flash_reserved=0 available=40 after ending window, got %+v", skuAfterEnd)
		}

		// Idempotent re-end: calling /end again returns 200 with released_items=0 and keeps available=40!
		endResp2 := env.mustDoJSON(t, http.MethodPost, endPath, adminTok, nil, nil, http.StatusOK)
		var endOut2 EndFlashSaleWindowResponse
		if err := DecodeJSON(endResp2, &endOut2); err != nil {
			t.Fatalf("decode idempotent second end window: %v", err)
		}
		if endOut2.ReleasedItems != 0 {
			t.Fatalf("expected released_items=0 on idempotent second end call, got %d", endOut2.ReleasedItems)
		}
		skuAfterEnd2 := env.getProductSKU(t, prod.ID, sku.ID)
		if skuAfterEnd2.FlashSaleReservedStock != 0 || skuAfterEnd2.AvailableStock != 40 {
			t.Fatalf("idempotent second end mutated stock: got %+v", skuAfterEnd2)
		}

		// VIETQR flash checkout after window ended must be rejected cleanly.
		env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "p2b-ended-fs-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID:        addr.ID,
			FromCart:         false,
			SalesChannel:     "STOREFRONT",
			StorefrontShopID: seller1.ShopID,
			Items: []CheckoutItemInputDTO{
				{SKUID: sku.ID, Quantity: 1, FlashSaleItemID: itemID},
			},
			PaymentMethod: "VIETQR",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
			},
		}, http.StatusConflict, http.StatusBadRequest)

		// Regular STOREFRONT buyer immediately buys 25 remaining units cleanly.
		env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "p2b-buy-released-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID:        addr.ID,
			FromCart:         false,
			SalesChannel:     "STOREFRONT",
			StorefrontShopID: seller1.ShopID,
			Items:            []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 25}},
			PaymentMethod:    "COD",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
			},
		}, http.StatusCreated, http.StatusOK)

		skuFinal := env.getProductSKU(t, prod.ID, sku.ID)
		if skuFinal.AvailableStock != 15 || skuFinal.FlashSaleReservedStock != 0 {
			t.Fatalf("expected available=15 flash_reserved=0 after buying 25 regular units, got %+v", skuFinal)
		}
	})

	// (e) Double-entry ledger summary remains 100% balanced after all Phase 2b flash sale & regular checkouts.
	t.Run("E_DoubleEntryLedger_Balanced", func(t *testing.T) {
		summary := env.getLedgerSummary(t)
		t.Logf("Phase 2b Ledger Summary: debits=%d credits=%d is_balanced=%v accounts=%d",
			summary.TotalDebitsVND, summary.TotalCreditsVND, summary.IsBalanced, len(summary.Accounts))
		if !summary.IsBalanced || summary.TotalDebitsVND != summary.TotalCreditsVND {
			t.Fatalf("LEDGER IMBALANCE after Phase 2b: is_balanced=%v debits=%d credits=%d",
				summary.IsBalanced, summary.TotalDebitsVND, summary.TotalCreditsVND)
		}
		if summary.TotalDebitsVND <= 0 {
			t.Fatalf("expected total_debits_vnd > 0, got %d", summary.TotalDebitsVND)
		}
	})
}
