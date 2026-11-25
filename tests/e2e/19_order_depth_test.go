package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// --- Order Depth DTOs (Shipping Legacy, ZaloPay, Replacement Returns & Reconciliation Runs) ---

type e2eMapLegacyAddressResponse struct {
	Found              bool   `json:"found"`
	ProvinceCode       string `json:"province_code"`
	ProvinceName       string `json:"province_name"`
	WardCode           string `json:"ward_code"`
	WardName           string `json:"ward_name"`
	LegacyProvinceCode string `json:"legacy_province_code"`
	LegacyDistrictCode string `json:"legacy_district_code"`
	LegacyWardCode     string `json:"legacy_ward_code"`
}

type e2eWardsListResponse struct {
	Wards []struct {
		Code         string `json:"code"`
		ProvinceCode string `json:"province_code"`
		Name         string `json:"name"`
		CODSupported bool   `json:"cod_supported"`
	} `json:"wards"`
}

type e2ePaymentDetailResponse struct {
	Payment struct {
		PaymentID    string `json:"payment_id"`
		OrderGroupID string `json:"order_group_id"`
		OrderID      string `json:"order_id"`
		AmountVND    int64  `json:"amount_vnd"`
		Method       string `json:"method"`
		Status       string `json:"status"`
		QRURL        string `json:"qr_url"`
		QRCode       string `json:"qr_code"`
		PayURL       string `json:"pay_url"`
		Provider     string `json:"provider"`
		ProviderRef  string `json:"provider_ref"`
	} `json:"payment"`
}

type e2eZaloPayWebhookResponse struct {
	ReturnCode    int    `json:"return_code"`
	ReturnMessage string `json:"return_message"`
}

type e2eReturnRequestDTO struct {
	ID                 string `json:"id"`
	OrderID            string `json:"order_id"`
	BuyerID            string `json:"buyer_id"`
	ShopID             string `json:"shop_id"`
	Status             string `json:"status"`
	Reason             string `json:"reason"`
	SellerResponse     string `json:"seller_response"`
	DisputeReason      string `json:"dispute_reason"`
	AdminNote          string `json:"admin_note"`
	RefundAmountVND    int64  `json:"refund_amount_vnd"`
	ReturnTrackingCode string `json:"return_tracking_code"`
	ReturnLabelURL     string `json:"return_label_url"`
	GoodsReceived      bool   `json:"goods_received"`
}

type e2eCreateReturnResponse struct {
	Order           OrderDTO `json:"order"`
	ReturnRequestID string   `json:"return_request_id"`
}

type e2eSingleReturnResponse struct {
	ReturnRequest e2eReturnRequestDTO `json:"return_request"`
}

type e2eReturnsListResponse struct {
	Items    []e2eReturnRequestDTO `json:"items"`
	Page     int32                 `json:"page"`
	PageSize int32                 `json:"page_size"`
	Total    int64                 `json:"total"`
}

type e2eReconciliationRunSummaryDTO struct {
	RunID                string `json:"run_id"`
	Provider             string `json:"provider"`
	RunDate              string `json:"run_date"`
	SalesChannel         string `json:"sales_channel"`
	MatchedCount         int32  `json:"matched_count"`
	MissingLocalCount    int32  `json:"missing_local_count"`
	MissingProviderCount int32  `json:"missing_provider_count"`
	AmountMismatchCount  int32  `json:"amount_mismatch_count"`
	AutoHealedCount      int32  `json:"auto_healed_count"`
	Status               string `json:"status"`
}

type e2eReconciliationRunsResponse struct {
	Items    []e2eReconciliationRunSummaryDTO `json:"items"`
	Page     int32                            `json:"page"`
	PageSize int32                            `json:"page_size"`
	Total    int64                            `json:"total"`
}

type e2eShipmentDetailResponse struct {
	Shipment struct {
		ShipmentID      string `json:"shipment_id"`
		OrderID         string `json:"order_id"`
		CarrierCode     string `json:"carrier_code"`
		TrackingCode    string `json:"tracking_code"`
		Status          string `json:"status"`
		Direction       string `json:"direction"`
		ReturnRequestID string `json:"return_request_id"`
	} `json:"shipment"`
}

func buildSignedZaloPayCallback(
	appID int,
	key2 string,
	paymentID, orderGroupID, orderID, appTransID string,
	zpTransID int64,
	amountVND, serverTimeMs int64,
	overrideMAC string,
) []byte {
	embedBytes, _ := json.Marshal(map[string]string{
		"payment_id":     paymentID,
		"order_group_id": orderGroupID,
		"order_id":       orderID,
	})
	dataBytes, _ := json.Marshal(map[string]any{
		"app_id":           appID,
		"app_trans_id":     appTransID,
		"app_time":         serverTimeMs,
		"app_user":         "nexusmart_buyer",
		"amount":           amountVND,
		"embed_data":       string(embedBytes),
		"item":             "[]",
		"zp_trans_id":      zpTransID,
		"server_time":      serverTimeMs,
		"channel":          38,
		"merchant_user_id": "e2e_mock_user",
		"user_fee_amount":  0,
		"discount_amount":  0,
	})
	dataStr := string(dataBytes)
	mac := overrideMAC
	if mac == "" && key2 != "" {
		mac = ComputeHMACSHA256(key2, []byte(dataStr))
	}
	envelope := map[string]any{
		"data": dataStr,
		"type": 1,
	}
	if mac != "" {
		envelope["mac"] = mac
	}
	raw, _ := json.Marshal(envelope)
	return raw
}

func extractAppTransIDFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(u.Query().Get("app_trans_id"))
}

// deliverVietQROrder places a STOREFRONT online-paid VietQR order, settles payment, ships via seller,
// and delivers via signed shipping webhook so the order is in DELIVERED state ready for return flows.
func deliverVietQROrder(
	t *testing.T,
	env *testEnv,
	buyer *userFixture,
	seller *sellerFixture,
	sku SKUDTO,
) (OrderDTO, string) {
	t.Helper()
	ctx := env.ctx(t)

	quote := env.calculateShippingQuote(t, buyer.AccessToken, seller.ShopID, sku.WeightGrams, 0)
	coResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
		"Idempotency-Key": "ret-co-" + uuid.NewString(),
	}, CheckoutRequest{
		AddressID:        buyer.AddressID,
		FromCart:         false,
		SalesChannel:     "STOREFRONT",
		StorefrontShopID: seller.ShopID,
		Items:            []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 1}},
		PaymentMethod:    "VIETQR",
		ShippingChoices: []ShopShippingChoiceDTO{
			{ShopID: seller.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
		},
	}, http.StatusCreated, http.StatusOK)

	var co CheckoutResponse
	if err := DecodeJSON(coResp, &co); err != nil {
		t.Fatalf("decode VietQR checkout for return flow: %v", err)
	}
	if len(co.Orders) != 1 || co.PaymentID == "" {
		t.Fatalf("unexpected checkout response: %s", string(coResp.Body))
	}
	order := co.Orders[0]

	// Settle VietQR payment via HMAC-signed webhook.
	whResp, err := env.client.PostSignedWebhook(ctx, "/webhooks/payment", PaymentWebhookPayload{
		Provider:     "vietqr_mock",
		ProviderTxID: "TXN-RET-" + uuid.NewString(),
		PaymentID:    co.PaymentID,
		OrderGroupID: co.CheckoutGroupID,
		AmountVND:    co.TotalPayableVND,
		Status:       "SUCCEEDED",
		Reference:    "NEXUSMART " + co.CheckoutGroupID,
		OccurredAt:   time.Now().UTC().Format(time.RFC3339),
	}, env.cfg.PaymentWebhookSecret, time.Now().UTC(), "")
	if err != nil {
		t.Fatalf("POST /webhooks/payment for return order: %v", err)
	}
	if whResp.StatusCode != http.StatusOK {
		t.Fatalf("POST /webhooks/payment returned HTTP %d: %s", whResp.StatusCode, string(whResp.Body))
	}

	env.pollUntil(t, env.cfg.PollTimeout, 250*time.Millisecond, "order CONFIRMED for return test", func() (bool, string) {
		o := env.getOrder(t, buyer.AccessToken, order.ID)
		return StatusEquals(o.Status, "CONFIRMED"), fmt.Sprintf("status=%s", o.Status)
	})

	// Seller confirms -> PREPARING, then ships -> tracking_code assigned.
	env.mustDoJSON(t, http.MethodPost,
		"/api/v1/seller/orders/"+url.PathEscape(order.ID)+"/confirm",
		seller.AccessToken, nil, nil, http.StatusOK)

	carrierCode := strings.ToLower(quote.CarrierCode)
	if carrierCode == "" {
		carrierCode = "ghn"
	}
	serviceCode := quote.ServiceCode
	if serviceCode == "" {
		serviceCode = "standard"
	}
	shipResp := env.mustDoJSON(t, http.MethodPost,
		"/api/v1/seller/orders/"+url.PathEscape(order.ID)+"/ship",
		seller.AccessToken, nil, SellerShipRequest{
			CarrierCode: carrierCode,
			ServiceCode: serviceCode,
		}, http.StatusOK)
	var shipOut SellerShipResponse
	if err := DecodeJSON(shipResp, &shipOut); err != nil {
		t.Fatalf("decode seller ship: %v", err)
	}
	if shipOut.TrackingCode == "" {
		t.Fatalf("seller ship returned empty tracking_code")
	}

	// Deliver via signed shipping webhooks (delivering -> delivered).
	whPath := "/webhooks/shipping/" + url.PathEscape(carrierCode)
	now := time.Now().UTC()
	for _, step := range []struct {
		status string
		desc   string
		at     time.Time
	}{
		{"delivering", "Out for delivery", now.Add(-2 * time.Second)},
		{"delivered", "Delivered to buyer", now},
	} {
		sResp, sErr := env.client.PostSignedWebhook(ctx, whPath, ShippingWebhookPayload{
			EventID:      "e2e-ret-ship-" + uuid.NewString(),
			TrackingCode: shipOut.TrackingCode,
			OrderID:      order.ID,
			RawStatus:    step.status,
			Description:  step.desc,
			OccurredAt:   step.at.Format(time.RFC3339),
		}, env.cfg.ShippingWebhookSecret, time.Now().UTC(), "")
		if sErr != nil {
			t.Fatalf("POST %s (%s): %v", whPath, step.status, sErr)
		}
		if sResp.StatusCode != http.StatusOK {
			t.Fatalf("POST %s (%s) returned HTTP %d: %s", whPath, step.status, sResp.StatusCode, string(sResp.Body))
		}
	}

	var delivered OrderDTO
	env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "order DELIVERED for return test", func() (bool, string) {
		delivered = env.getOrder(t, buyer.AccessToken, order.ID)
		return StatusEquals(delivered.Status, "DELIVERED"), fmt.Sprintf("status=%s", delivered.Status)
	})
	return delivered, carrierCode
}

func TestE2E_OrderDepth_ShippingZaloPayReturnsComplaintsAndReconciliation(t *testing.T) {
	env := getEnv()
	registerRateLimitResetCleanup(t, env)

	adminTok := env.adminLogin(t)
	cat, seller1, seller2 := env.ensureBaseMarketplace(t)

	buyer1 := env.registerVerifyAndLogin(t, "buyer-depth-1")
	_ = env.addAddress(t, buyer1)
	buyer2 := env.registerVerifyAndLogin(t, "buyer-depth-2")

	// (a) Shipping: QĐ 19/2025 legacy 3-level VN address mapping + multi-carrier fee calculation (ghn + ghtk).
	t.Run("Shipping_MapLegacyAndMultiCarrierFee", func(t *testing.T) {
		// Standard 3-level HCMC address (QĐ 19/2025 official ward code 26740).
		map1Resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/shipping/addresses/map-legacy", "", nil, map[string]string{
			"legacy_province": "Thành phố Hồ Chí Minh",
			"legacy_district": "Quận 1",
			"legacy_ward":     "Phường Bến Nghé",
		}, http.StatusOK)
		var map1 e2eMapLegacyAddressResponse
		if err := DecodeJSON(map1Resp, &map1); err != nil {
			t.Fatalf("decode map-legacy HCMC: %v", err)
		}
		if !map1.Found || map1.ProvinceCode != "79" || map1.WardCode != "26740" {
			t.Fatalf("unexpected HCMC legacy mapping (want province=79 ward=26740): %+v", map1)
		}

		// Merged province (Bình Dương -> TP.HCM code 79, QĐ 19/2025 official ward code 25747).
		map2Resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/shipping/addresses/map-legacy", "", nil, map[string]string{
			"legacy_province": "Tỉnh Bình Dương",
			"legacy_district": "Thành phố Thủ Dầu Một",
			"legacy_ward":     "Phường Phú Cường",
		}, http.StatusOK)
		var map2 e2eMapLegacyAddressResponse
		if err := DecodeJSON(map2Resp, &map2); err != nil {
			t.Fatalf("decode map-legacy Binh Duong: %v", err)
		}
		if !map2.Found || map2.ProvinceCode != "79" || map2.WardCode != "25747" {
			t.Fatalf("unexpected Binh Duong legacy mapping (want province=79 ward=25747): %+v", map2)
		}

		// Fetch an active Hanoi ward code (province 01) for inter-region fee calculation.
		wardsResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/shipping/provinces/01/wards", "", nil, nil, http.StatusOK)
		var wardsOut e2eWardsListResponse
		if err := DecodeJSON(wardsResp, &wardsOut); err != nil {
			t.Fatalf("decode Hanoi wards: %v", err)
		}
		if len(wardsOut.Wards) == 0 || wardsOut.Wards[0].Code == "" {
			t.Fatalf("expected >= 1 active ward in province 01, got %+v", wardsOut)
		}
		hanoiWard := wardsOut.Wards[0].Code

		// Calculate shipping fee across inter-region route (Hanoi -> HCMC) and verify multiple carriers including ghn & ghtk.
		feeResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/shipping/calculate-fee", buyer1.AccessToken, nil, CalculateFeeRequest{
			ShopID:           seller1.ShopID,
			FromProvinceCode: "01",
			FromWardCode:     hanoiWard,
			ToProvinceCode:   "79",
			ToWardCode:       map1.WardCode,
			WeightGrams:      1000,
			CODAmountVND:     250000,
		}, http.StatusOK)
		var feeOut CalculateFeeResponse
		if err := DecodeJSON(feeResp, &feeOut); err != nil {
			t.Fatalf("decode calculate-fee: %v", err)
		}
		carriers := make(map[string]int)
		for _, q := range feeOut.Quotes {
			if q.FeeVND <= 0 || q.EstimatedDays < 1 {
				t.Fatalf("invalid quote returned: %+v", q)
			}
			carriers[strings.ToLower(q.CarrierCode)]++
		}
		if len(carriers) < 2 {
			t.Fatalf("expected >= 2 distinct carriers in calculate-fee, got %v (quotes=%+v)", carriers, feeOut.Quotes)
		}
		if carriers["ghn"] == 0 || carriers["ghtk"] == 0 {
			t.Fatalf("expected both ghn and ghtk mock carriers in calculate-fee, got carriers=%v", carriers)
		}
	})

	// (b) STOREFRONT ZaloPay checkout, HMAC/stale-time fallback security, 3x idempotent replay, balanced ledger.
	// See also 44_zalopay_payment_w3b2_test.go for extended ZaloPay refund/retry/timeout coverage.
	t.Run("ZaloPay_CheckoutHMACReplayAndBalancedLedger", func(t *testing.T) {
		ctx := env.ctx(t)
		_, skuZP := env.createApprovedProduct(t, seller1, cat.ID, "ZaloPay SmartWatch E2E "+env.runID, 350000, 20, 600)
		quote := env.calculateShippingQuote(t, buyer1.AccessToken, seller1.ShopID, 600, 0)

		coResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer1.AccessToken, map[string]string{
			"Idempotency-Key": "zp-e2e-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID:        buyer1.AddressID,
			FromCart:         false,
			SalesChannel:     "STOREFRONT",
			StorefrontShopID: seller1.ShopID,
			Items:            []CheckoutItemInputDTO{{SKUID: skuZP.ID, Quantity: 1}},
			PaymentMethod:    "ZALOPAY",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
			},
		}, http.StatusCreated, http.StatusOK)

		var co CheckoutResponse
		if err := DecodeJSON(coResp, &co); err != nil {
			t.Fatalf("decode ZaloPay checkout: %v", err)
		}
		if len(co.Orders) != 1 || co.PaymentID == "" || !StatusEquals(co.PaymentMethod, "ZALOPAY") {
			t.Fatalf("unexpected ZaloPay checkout response: %s", string(coResp.Body))
		}
		orderZP := co.Orders[0]
		if !StatusEquals(orderZP.Status, "PENDING") {
			t.Fatalf("expected ZaloPay order PENDING before callback, got %q", orderZP.Status)
		}

		// Read app_trans_id from GET /api/v1/payments/{id} (merged payment sets provider_ref & pay_url).
		payResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/payments/"+url.PathEscape(co.PaymentID), buyer1.AccessToken, nil, nil, http.StatusOK)
		var payDetail e2ePaymentDetailResponse
		if err := DecodeJSON(payResp, &payDetail); err != nil {
			t.Fatalf("decode ZaloPay payment detail: %v", err)
		}
		appTransID := strings.TrimSpace(payDetail.Payment.ProviderRef)
		if appTransID == "" {
			appTransID = extractAppTransIDFromURL(payDetail.Payment.PayURL)
		}
		if appTransID == "" {
			appTransID = extractAppTransIDFromURL(payDetail.Payment.QRURL)
		}
		if appTransID == "" {
			t.Fatalf("could not extract ZaloPay app_trans_id from payment %+v", payDetail.Payment)
		}

		appID := env.cfg.ZaloPayAppID
		key2 := env.cfg.ZaloPayKey2
		ledgerBefore := w3b44AssertLedgerBalanced(t, env)
		gcBefore := w3b44FindAccount(ledgerBefore, "gateway_clearing")

		assertUnchangedPending := func(label string) {
			t.Helper()
			if o := env.getOrder(t, buyer1.AccessToken, orderZP.ID); !StatusEquals(o.Status, "PENDING") {
				t.Fatalf("%s mutated order status to %q (want PENDING)", label, o.Status)
			}
			if p := env.getPayment(t, buyer1.AccessToken, co.PaymentID); !StatusEquals(p.Status, "PENDING") {
				t.Fatalf("%s mutated payment status to %q (want PENDING)", label, p.Status)
			}
			ledgerNow := w3b44AssertLedgerBalanced(t, env)
			if ledgerNow.TotalDebitsVND != ledgerBefore.TotalDebitsVND || ledgerNow.TotalCreditsVND != ledgerBefore.TotalCreditsVND {
				t.Fatalf("%s mutated ledger: before=(%d,%d) after=(%d,%d)",
					label, ledgerBefore.TotalDebitsVND, ledgerBefore.TotalCreditsVND,
					ledgerNow.TotalDebitsVND, ledgerNow.TotalCreditsVND)
			}
		}

		// 1a. Invalid HMAC rejected with HTTP 401 + return_code=-1, order stays PENDING, 0 ledger entries.
		badSigBody := buildSignedZaloPayCallback(
			appID, "wrong-zalopay-key2", co.PaymentID, co.CheckoutGroupID, orderZP.ID, appTransID,
			w3b44NextZPTransID(env), co.TotalPayableVND, time.Now().UTC().UnixMilli(), "",
		)
		badSigResp, err := env.client.DoJSON(ctx, http.MethodPost, "/webhooks/zalopay", "", nil, badSigBody)
		if err != nil {
			t.Fatalf("POST /webhooks/zalopay invalid mac: %v", err)
		}
		if badSigResp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected HTTP 401 for invalid ZaloPay MAC, got %d: %s", badSigResp.StatusCode, string(badSigResp.Body))
		}
		var badSigOut e2eZaloPayWebhookResponse
		if err := DecodeJSON(badSigResp, &badSigOut); err != nil {
			t.Fatalf("decode invalid MAC response: %v", err)
		}
		if badSigOut.ReturnCode != -1 {
			t.Fatalf("expected return_code=-1 for invalid ZaloPay MAC, got %+v", badSigOut)
		}
		assertUnchangedPending("invalid ZaloPay MAC callback")

		// 1b. Missing mac field in JSON envelope is a malformed payload per services/payment/src/http.rs
		//     -> HTTP 400 + return_code=-1, order stays PENDING, 0 ledger entries.
		missingSigBody := buildSignedZaloPayCallback(
			appID, "", co.PaymentID, co.CheckoutGroupID, orderZP.ID, appTransID,
			w3b44NextZPTransID(env), co.TotalPayableVND, time.Now().UTC().UnixMilli(), "",
		)
		missingSigResp, err := env.client.DoJSON(ctx, http.MethodPost, "/webhooks/zalopay", "", nil, missingSigBody)
		if err != nil {
			t.Fatalf("POST /webhooks/zalopay missing mac: %v", err)
		}
		if missingSigResp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected HTTP 400 for missing ZaloPay MAC field, got %d: %s", missingSigResp.StatusCode, string(missingSigResp.Body))
		}
		var missingSigOut e2eZaloPayWebhookResponse
		if err := DecodeJSON(missingSigResp, &missingSigOut); err != nil {
			t.Fatalf("decode missing MAC response: %v", err)
		}
		if missingSigOut.ReturnCode != -1 {
			t.Fatalf("expected return_code=-1 for missing ZaloPay MAC field, got %+v", missingSigOut)
		}
		assertUnchangedPending("missing ZaloPay MAC callback")

		// 2. Stale server_time (> 5 minutes old) with valid HMAC falls back to /v2/query on merged payment
		//    and returns HTTP 200 with return_code=0 while leaving unpaid mock order PENDING and ledger unchanged.
		staleTimeMs := time.Now().UTC().Add(-10 * time.Minute).UnixMilli()
		staleBody := buildSignedZaloPayCallback(
			appID, key2, co.PaymentID, co.CheckoutGroupID, orderZP.ID, appTransID,
			w3b44NextZPTransID(env), co.TotalPayableVND, staleTimeMs, "",
		)
		staleResp, err := env.client.DoJSON(ctx, http.MethodPost, "/webhooks/zalopay", "", nil, staleBody)
		if err != nil {
			t.Fatalf("POST /webhooks/zalopay stale timestamp: %v", err)
		}
		if staleResp.StatusCode != http.StatusOK {
			t.Fatalf("expected HTTP 200 (query fallback return_code=0) for stale ZaloPay server_time, got %d: %s", staleResp.StatusCode, string(staleResp.Body))
		}
		var staleOut e2eZaloPayWebhookResponse
		if err := DecodeJSON(staleResp, &staleOut); err != nil {
			t.Fatalf("decode stale ZaloPay response: %v", err)
		}
		if staleOut.ReturnCode != 0 {
			t.Fatalf("expected return_code=0 for stale unpaid ZaloPay callback, got %+v", staleOut)
		}
		assertUnchangedPending("stale ZaloPay server_time callback")

		// 3. Valid HMAC callback replayed 3x -> strictly idempotent (return_code=1, single ledger posting).
		zpTxID := w3b44NextZPTransID(env)
		validBody := buildSignedZaloPayCallback(
			appID, key2, co.PaymentID, co.CheckoutGroupID, orderZP.ID, appTransID,
			zpTxID, co.TotalPayableVND, time.Now().UTC().UnixMilli(), "",
		)
		for attempt := 1; attempt <= 3; attempt++ {
			resp, reqErr := env.client.DoJSON(ctx, http.MethodPost, "/webhooks/zalopay", "", nil, validBody)
			if reqErr != nil {
				t.Fatalf("POST /webhooks/zalopay attempt #%d failed: %v", attempt, reqErr)
			}
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("POST /webhooks/zalopay attempt #%d returned HTTP %d: %s", attempt, resp.StatusCode, string(resp.Body))
			}
			var out e2eZaloPayWebhookResponse
			if err := DecodeJSON(resp, &out); err != nil {
				t.Fatalf("decode ZaloPay webhook #%d: %v", attempt, err)
			}
			if out.ReturnCode != 1 {
				t.Fatalf("expected return_code=1 on ZaloPay webhook #%d, got %+v", attempt, out)
			}
		}

		// Poll until order CONFIRMED and payment SUCCEEDED.
		env.pollUntil(t, env.cfg.PollTimeout, 250*time.Millisecond, "ZaloPay order CONFIRMED", func() (bool, string) {
			o := env.getOrder(t, buyer1.AccessToken, orderZP.ID)
			return StatusEquals(o.Status, "CONFIRMED"), fmt.Sprintf("status=%s", o.Status)
		})
		payAfter := env.getPayment(t, buyer1.AccessToken, co.PaymentID)
		if !StatusEquals(payAfter.Status, "SUCCEEDED") {
			t.Fatalf("expected ZaloPay payment SUCCEEDED, got %q", payAfter.Status)
		}

		// Double-entry ledger balanced & debits posted strictly once (not 3x).
		ledgerAfter := w3b44AssertLedgerBalanced(t, env)
		gcAfter := w3b44FindAccount(ledgerAfter, "gateway_clearing")
		if gcDelta := gcAfter.DebitTotalVND - gcBefore.DebitTotalVND; gcDelta != co.TotalPayableVND {
			t.Fatalf("gateway_clearing debit delta = %d, want exact %d after 3x ZaloPay replay", gcDelta, co.TotalPayableVND)
		}
		delta := ledgerAfter.TotalDebitsVND - ledgerBefore.TotalDebitsVND
		if delta <= 0 || delta >= 2*co.TotalPayableVND {
			t.Fatalf("expected single ZaloPay payment ledger delta (~%d VND), got delta=%d", co.TotalPayableVND, delta)
		}
	})

	// (c.0) Removed phase-0b handlers_d.go routes MUST return HTTP 404 (per DECISIONS.md).
	// Replacement flows are verified in (c.1), (c.2), (c.3) below and in 19_order_returns_test.go & 20_payment_ops_test.go.
	t.Run("RemovedLegacyRoutes_Return404", func(t *testing.T) {
		fakeID := uuid.NewString()
		removedRoutes := []struct {
			name        string
			method      string
			path        string
			token       string
			body        any
			replacement string
		}{
			{
				name:        "POST /orders/{id}/returns (removed)",
				method:      http.MethodPost,
				path:        "/api/v1/orders/" + fakeID + "/returns",
				token:       buyer1.AccessToken,
				body:        map[string]any{"reason": "legacy plural returns"},
				replacement: "POST /api/v1/orders/{id}/return",
			},
			{
				name:        "GET /orders/{id}/returns (removed)",
				method:      http.MethodGet,
				path:        "/api/v1/orders/" + fakeID + "/returns",
				token:       buyer1.AccessToken,
				replacement: "GET /api/v1/orders/{id}/return",
			},
			{
				name:        "POST /seller/returns/{id}/approve (removed)",
				method:      http.MethodPost,
				path:        "/api/v1/seller/returns/" + fakeID + "/approve",
				token:       seller1.AccessToken,
				body:        map[string]any{"note": "legacy approve"},
				replacement: "POST /api/v1/seller/returns/{id}/respond (decision=ACCEPT_RETURN|ACCEPT_REFUND)",
			},
			{
				name:        "POST /seller/returns/{id}/reject (removed)",
				method:      http.MethodPost,
				path:        "/api/v1/seller/returns/" + fakeID + "/reject",
				token:       seller1.AccessToken,
				body:        map[string]any{"reason": "legacy reject"},
				replacement: "POST /api/v1/seller/returns/{id}/respond (decision=REJECT)",
			},
			{
				name:        "POST /orders/{id}/complaints (removed)",
				method:      http.MethodPost,
				path:        "/api/v1/orders/" + fakeID + "/complaints",
				token:       buyer1.AccessToken,
				body:        map[string]any{"reason": "legacy complaint"},
				replacement: "POST /api/v1/returns/{id}/escalate",
			},
			{
				name:        "GET /admin/complaints (removed)",
				method:      http.MethodGet,
				path:        "/api/v1/admin/complaints",
				token:       adminTok,
				replacement: "GET /api/v1/admin/returns",
			},
			{
				name:        "POST /admin/complaints/{id}/resolve (removed)",
				method:      http.MethodPost,
				path:        "/api/v1/admin/complaints/" + fakeID + "/resolve",
				token:       adminTok,
				body:        map[string]any{"resolution": "REFUND"},
				replacement: "POST /api/v1/admin/returns/{id}/resolve",
			},
			{
				name:        "GET /admin/payments/reconciliation-reports (removed)",
				method:      http.MethodGet,
				path:        "/api/v1/admin/payments/reconciliation-reports?limit=10",
				token:       adminTok,
				replacement: "GET /api/v1/admin/reconciliation/runs",
			},
		}

		for _, tc := range removedRoutes {
			resp := env.mustDoJSON(t, tc.method, tc.path, tc.token, nil, tc.body, http.StatusNotFound)
			t.Logf("Removed route %s -> HTTP %d (replacement: %s)", tc.name, resp.StatusCode, tc.replacement)
		}
	})

	// (c.1) Replacement return flow (main) on STOREFRONT order:
	// POST /orders/{id}/return -> seller ACCEPT_RETURN -> return shipment delivered -> confirm-received -> REFUNDED + Ledger + RBAC.
	// Full MARKETPLACE return matrix is also covered in 19_order_returns_test.go.
	t.Run("Returns_ApproveReverseShipmentConfirmReceivedAndRBAC", func(t *testing.T) {
		_, skuRet1 := env.createApprovedProduct(t, seller1, cat.ID, "Return Approve Jacket E2E "+env.runID, 280000, 20, 500)
		order1, carrier := deliverVietQROrder(t, env, buyer1, seller1, skuRet1)

		balBeforeResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/balance", seller1.AccessToken, nil, nil, http.StatusOK)
		var balBefore SellerBalanceResponse
		if err := DecodeJSON(balBeforeResp, &balBefore); err != nil {
			t.Fatalf("decode seller1 balance before return: %v", err)
		}

		// Buyer creates return request via replacement route POST /api/v1/orders/{id}/return.
		crResp := env.mustDoJSON(t, http.MethodPost,
			"/api/v1/orders/"+url.PathEscape(order1.ID)+"/return",
			buyer1.AccessToken, nil, map[string]any{
				"reason":        "Sản phẩm có vết rách ở đường may cổ áo",
				"evidence_urls": []string{"https://cdn.example.com/evidence/return-photo-1.jpg"},
			}, http.StatusOK, http.StatusCreated)
		var crOut e2eCreateReturnResponse
		if err := DecodeJSON(crResp, &crOut); err != nil {
			t.Fatalf("decode create return: %v", err)
		}
		rrID := crOut.ReturnRequestID
		if rrID == "" || !StatusEquals(crOut.Order.Status, "RETURN_REQUESTED") {
			t.Fatalf("unexpected create return response: %+v", crOut)
		}

		// Buyer reads own order return via replacement route GET /api/v1/orders/{id}/return.
		bRetResp := env.mustDoJSON(t, http.MethodGet,
			"/api/v1/orders/"+url.PathEscape(order1.ID)+"/return",
			buyer1.AccessToken, nil, nil, http.StatusOK)
		var bRetOut e2eSingleReturnResponse
		if err := DecodeJSON(bRetResp, &bRetOut); err != nil {
			t.Fatalf("decode buyer get order return: %v", err)
		}
		if bRetOut.ReturnRequest.ID != rrID {
			t.Fatalf("buyer GET /orders/%s/return id=%q, want %q", order1.ID, bRetOut.ReturnRequest.ID, rrID)
		}

		// RBAC checks: buyer2 cannot view buyer1's return; buyer1 & seller2 cannot respond to seller1's return.
		env.mustDoJSON(t, http.MethodGet,
			"/api/v1/orders/"+url.PathEscape(order1.ID)+"/return",
			buyer2.AccessToken, nil, nil, http.StatusForbidden)
		env.mustDoJSON(t, http.MethodPost,
			"/api/v1/seller/returns/"+url.PathEscape(rrID)+"/respond",
			buyer1.AccessToken, nil, map[string]any{"decision": "ACCEPT_RETURN", "note": "buyer self-approve"}, http.StatusForbidden)
		env.mustDoJSON(t, http.MethodPost,
			"/api/v1/seller/returns/"+url.PathEscape(rrID)+"/respond",
			seller2.AccessToken, nil, map[string]any{"decision": "ACCEPT_RETURN", "note": "wrong shop approve"}, http.StatusForbidden)

		// Seller1 lists shop returns.
		sRetResp := env.mustDoJSON(t, http.MethodGet,
			"/api/v1/seller/returns?page=1&page_size=50",
			seller1.AccessToken, nil, nil, http.StatusOK)
		var sRetOut e2eReturnsListResponse
		if err := DecodeJSON(sRetResp, &sRetOut); err != nil {
			t.Fatalf("decode seller list returns: %v", err)
		}
		foundInSeller := false
		for _, item := range sRetOut.Items {
			if item.ID == rrID {
				foundInSeller = true
				break
			}
		}
		if !foundInSeller {
			t.Fatalf("seller1 GET /seller/returns missing %s: %+v", rrID, sRetOut)
		}

		// Seller1 responds ACCEPT_RETURN via replacement route POST /api/v1/seller/returns/{id}/respond.
		env.mustDoJSON(t, http.MethodPost,
			"/api/v1/seller/returns/"+url.PathEscape(rrID)+"/respond",
			seller1.AccessToken, nil, map[string]any{
				"decision": "ACCEPT_RETURN",
				"note":     "Đồng ý nhận lại hàng hoàn",
			}, http.StatusOK)

		var retTrack string
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "return shipment tracking assigned", func() (bool, string) {
			resp := env.mustDoJSON(t, http.MethodGet, "/api/v1/returns/"+url.PathEscape(rrID), buyer1.AccessToken, nil, nil, http.StatusOK)
			var out e2eSingleReturnResponse
			if err := DecodeJSON(resp, &out); err != nil {
				return false, err.Error()
			}
			retTrack = out.ReturnRequest.ReturnTrackingCode
			return StatusEquals(out.ReturnRequest.Status, "AWAITING_SHIPMENT") && retTrack != "",
				fmt.Sprintf("status=%s tracking=%q", out.ReturnRequest.Status, retTrack)
		})

		// Verify reverse shipment exists on shipping service.
		revShipResp := env.mustDoJSON(t, http.MethodGet,
			"/api/v1/shipping/shipments/"+url.PathEscape(order1.ID)+"?direction=return",
			buyer1.AccessToken, nil, nil, http.StatusOK)
		var revShip e2eShipmentDetailResponse
		if err := DecodeJSON(revShipResp, &revShip); err != nil {
			t.Fatalf("decode reverse shipment: %v", err)
		}
		if revShip.Shipment.TrackingCode != retTrack {
			t.Fatalf("reverse shipment tracking mismatch: %q vs %q", revShip.Shipment.TrackingCode, retTrack)
		}

		// Deliver reverse shipment via signed shipping webhook -> RECEIVED, then seller confirms received -> REFUNDED/RETURNED.
		ordW2SendShippingWebhook(t, env, carrier, order1.ID, retTrack, "delivered", "Đã hoàn hàng về kho shop")
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "return RECEIVED after reverse delivery", func() (bool, string) {
			resp := env.mustDoJSON(t, http.MethodGet, "/api/v1/returns/"+url.PathEscape(rrID), seller1.AccessToken, nil, nil, http.StatusOK)
			var out e2eSingleReturnResponse
			if err := DecodeJSON(resp, &out); err != nil {
				return false, err.Error()
			}
			return StatusEquals(out.ReturnRequest.Status, "RECEIVED") && out.ReturnRequest.GoodsReceived,
				fmt.Sprintf("status=%s goods_received=%v", out.ReturnRequest.Status, out.ReturnRequest.GoodsReceived)
		})

		env.mustDoJSON(t, http.MethodPost,
			"/api/v1/seller/returns/"+url.PathEscape(rrID)+"/confirm-received",
			seller1.AccessToken, nil, nil, http.StatusOK)

		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "STOREFRONT return REFUNDED and order RETURNED", func() (bool, string) {
			retResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/returns/"+url.PathEscape(rrID), buyer1.AccessToken, nil, nil, http.StatusOK)
			var retOut e2eSingleReturnResponse
			if err := DecodeJSON(retResp, &retOut); err != nil {
				return false, err.Error()
			}
			ord := env.getOrder(t, buyer1.AccessToken, order1.ID)
			return StatusEquals(retOut.ReturnRequest.Status, "REFUNDED") && StatusEquals(ord.Status, "RETURNED"),
				fmt.Sprintf("ret=%s ord=%s", retOut.ReturnRequest.Status, ord.Status)
		})

		// Verify pending refund created for exact order total.
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "pending refund for STOREFRONT return", func() (bool, string) {
			refunds := ordW2GetOrderRefunds(t, env, buyer1.AccessToken, order1.ID)
			for _, r := range refunds {
				if r.AmountVND == order1.TotalPayableVND && StatusEquals(r.Status, "PENDING") {
					return true, ""
				}
			}
			return false, fmt.Sprintf("refunds=%+v want amount=%d status=PENDING", refunds, order1.TotalPayableVND)
		})

		// Verify double-entry ledger stays balanced & seller pending escrow did not grow.
		ledgerAfter := env.getLedgerSummary(t)
		if !ledgerAfter.IsBalanced || ledgerAfter.TotalDebitsVND != ledgerAfter.TotalCreditsVND {
			t.Fatalf("ledger imbalanced after return refund: debits=%d credits=%d balanced=%v",
				ledgerAfter.TotalDebitsVND, ledgerAfter.TotalCreditsVND, ledgerAfter.IsBalanced)
		}

		balAfterResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/balance", seller1.AccessToken, nil, nil, http.StatusOK)
		var balAfter SellerBalanceResponse
		if err := DecodeJSON(balAfterResp, &balAfter); err != nil {
			t.Fatalf("decode seller1 balance after refund: %v", err)
		}
		if balAfter.ShopID != seller1.ShopID || balAfter.PendingVND > balBefore.PendingVND {
			t.Fatalf("unexpected seller1 balance after return refund: before=%+v after=%+v", balBefore, balAfter)
		}
	})

	// (c.2) Replacement dispute/escalation flow (main) on STOREFRONT order:
	// POST /orders/{id}/return -> seller REJECT -> buyer POST /returns/{id}/escalate ->
	// admin GET /admin/returns + POST /admin/returns/{id}/resolve (REFUND) + RBAC.
	t.Run("Returns_RejectEscalateComplaintAndAdminResolve", func(t *testing.T) {
		_, skuRet2 := env.createApprovedProduct(t, seller1, cat.ID, "Return Reject Shoes E2E "+env.runID, 310000, 20, 700)
		order2, _ := deliverVietQROrder(t, env, buyer1, seller1, skuRet2)

		crResp := env.mustDoJSON(t, http.MethodPost,
			"/api/v1/orders/"+url.PathEscape(order2.ID)+"/return",
			buyer1.AccessToken, nil, map[string]any{
				"reason":        "Giao sai kích cỡ so với đơn đặt",
				"evidence_urls": []string{"https://cdn.example.com/evidence/wrong-size.jpg"},
			}, http.StatusOK, http.StatusCreated)
		var crOut e2eCreateReturnResponse
		if err := DecodeJSON(crResp, &crOut); err != nil {
			t.Fatalf("decode create return2: %v", err)
		}
		rr2ID := crOut.ReturnRequestID
		if rr2ID == "" {
			t.Fatalf("empty return_request_id: %+v", crOut)
		}

		// Seller1 rejects return via POST /api/v1/seller/returns/{id}/respond.
		env.mustDoJSON(t, http.MethodPost,
			"/api/v1/seller/returns/"+url.PathEscape(rr2ID)+"/respond",
			seller1.AccessToken, nil, map[string]any{
				"decision": "REJECT",
				"note":     "Shop kiểm tra đóng gói đúng size đã đặt",
			}, http.StatusOK)

		// Buyer escalates rejected return via replacement route POST /api/v1/returns/{id}/escalate -> ESCALATED.
		escResp := env.mustDoJSON(t, http.MethodPost,
			"/api/v1/returns/"+url.PathEscape(rr2ID)+"/escalate",
			buyer1.AccessToken, nil, map[string]any{
				"reason":        "Bổ sung hình ảnh tem size thực tế không khớp đơn hàng",
				"evidence_urls": []string{"https://cdn.example.com/evidence/size-tag.jpg"},
			}, http.StatusOK)
		var escOut e2eSingleReturnResponse
		if err := DecodeJSON(escResp, &escOut); err != nil {
			t.Fatalf("decode escalate return: %v", err)
		}
		if !StatusEquals(escOut.ReturnRequest.Status, "ESCALATED") {
			t.Fatalf("expected ESCALATED after escalate, got %+v", escOut.ReturnRequest)
		}

		// RBAC: buyer and seller cannot access admin returns endpoints.
		env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/returns", buyer1.AccessToken, nil, nil, http.StatusForbidden)
		env.mustDoJSON(t, http.MethodPost,
			"/api/v1/admin/returns/"+url.PathEscape(rr2ID)+"/resolve",
			buyer1.AccessToken, nil, map[string]any{"decision": "REFUND", "note": "buyer hijack"}, http.StatusForbidden)
		env.mustDoJSON(t, http.MethodPost,
			"/api/v1/admin/returns/"+url.PathEscape(rr2ID)+"/resolve",
			seller1.AccessToken, nil, map[string]any{"decision": "REJECT", "note": "seller hijack"}, http.StatusForbidden)

		// Admin lists escalated returns via replacement route GET /api/v1/admin/returns?status=ESCALATED.
		listResp := env.mustDoJSON(t, http.MethodGet,
			"/api/v1/admin/returns?status=ESCALATED&page=1&page_size=50",
			adminTok, nil, nil, http.StatusOK)
		var listOut e2eReturnsListResponse
		if err := DecodeJSON(listResp, &listOut); err != nil {
			t.Fatalf("decode admin list returns: %v", err)
		}
		foundRet := false
		for _, item := range listOut.Items {
			if !StatusEquals(item.Status, "ESCALATED") {
				t.Fatalf("GET /admin/returns?status=ESCALATED returned non-ESCALATED item: %+v", item)
			}
			if item.ID == rr2ID {
				foundRet = true
			}
		}
		if !foundRet {
			t.Fatalf("admin GET /admin/returns?status=ESCALATED missing return %s: %+v", rr2ID, listOut)
		}

		// Admin resolves return with immediate REFUND via replacement route POST /api/v1/admin/returns/{id}/resolve.
		resResp := env.mustDoJSON(t, http.MethodPost,
			"/api/v1/admin/returns/"+url.PathEscape(rr2ID)+"/resolve",
			adminTok, nil, map[string]any{
				"decision": "REFUND",
				"note":     "Xác minh bằng chứng giao sai size, hoàn tiền ngay cho người mua",
			}, http.StatusOK)
		var resOut e2eSingleReturnResponse
		if err := DecodeJSON(resResp, &resOut); err != nil {
			t.Fatalf("decode admin resolve return: %v", err)
		}
		if resOut.ReturnRequest.ID != rr2ID ||
			(!StatusEquals(resOut.ReturnRequest.Status, "REFUNDING") && !StatusEquals(resOut.ReturnRequest.Status, "REFUNDED")) {
			t.Fatalf("unexpected admin resolve REFUND response: %+v", resOut.ReturnRequest)
		}

		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "escalated return REFUNDED and order RETURNED", func() (bool, string) {
			retResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/returns/"+url.PathEscape(rr2ID), buyer1.AccessToken, nil, nil, http.StatusOK)
			var retOut e2eSingleReturnResponse
			if err := DecodeJSON(retResp, &retOut); err != nil {
				return false, err.Error()
			}
			ord := env.getOrder(t, buyer1.AccessToken, order2.ID)
			return StatusEquals(retOut.ReturnRequest.Status, "REFUNDED") && StatusEquals(ord.Status, "RETURNED"),
				fmt.Sprintf("ret=%s ord=%s", retOut.ReturnRequest.Status, ord.Status)
		})

		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "pending refund for admin-resolved escalated return", func() (bool, string) {
			refunds := ordW2GetOrderRefunds(t, env, buyer1.AccessToken, order2.ID)
			for _, r := range refunds {
				if r.AmountVND == order2.TotalPayableVND && StatusEquals(r.Status, "PENDING") {
					return true, ""
				}
			}
			return false, fmt.Sprintf("refunds=%+v want amount=%d", refunds, order2.TotalPayableVND)
		})

		// Ledger stays balanced after admin-resolved return refund.
		_ = w3b44AssertLedgerBalanced(t, env)
	})

	// (c.3) Replacement admin reconciliation runs endpoint (GET /api/v1/admin/reconciliation/runs) + RBAC.
	// See also 20_payment_ops_test.go for queue resolution and anomaly tests.
	t.Run("AdminReconciliationReportsAndRBAC", func(t *testing.T) {
		vnToday := time.Now().In(time.FixedZone("Asia/Ho_Chi_Minh", 7*3600)).Format("2006-01-02")
		env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/payments/reconcile", adminTok, nil, map[string]any{
			"date":          vnToday,
			"sales_channel": "STOREFRONT",
		}, http.StatusOK)

		repResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/reconciliation/runs?page=1&page_size=10", adminTok, nil, nil, http.StatusOK)
		var repOut e2eReconciliationRunsResponse
		if err := DecodeJSON(repResp, &repOut); err != nil {
			t.Fatalf("decode reconciliation runs: %v", err)
		}
		if len(repOut.Items) == 0 || repOut.Items[0].RunID == "" {
			t.Fatalf("expected >= 1 reconciliation run with run_id, got %+v", repOut)
		}

		// Channel-filtered reconciliation runs listing works cleanly.
		sfRepResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/reconciliation/runs?sales_channel=STOREFRONT&page=1&page_size=10", adminTok, nil, nil, http.StatusOK)
		var sfRepOut e2eReconciliationRunsResponse
		if err := DecodeJSON(sfRepResp, &sfRepOut); err != nil {
			t.Fatalf("decode STOREFRONT reconciliation runs: %v", err)
		}
		if len(sfRepOut.Items) == 0 {
			t.Fatalf("expected >= 1 STOREFRONT reconciliation run, got %+v", sfRepOut)
		}

		// Buyer and seller MUST get 403 on admin reconciliation runs.
		env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/reconciliation/runs", buyer1.AccessToken, nil, nil, http.StatusForbidden)
		env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/reconciliation/runs", seller1.AccessToken, nil, nil, http.StatusForbidden)
	})
}
