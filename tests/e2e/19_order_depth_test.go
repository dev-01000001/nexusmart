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

// --- Order Depth DTOs (Shipping Legacy, ZaloPay, Returns, Complaints, Reconciliation Reports) ---

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
		Provider     string `json:"provider"`
		ProviderRef  string `json:"provider_ref"`
	} `json:"payment"`
}

type e2eZaloPayWebhookResponse struct {
	ReturnCode    int    `json:"return_code"`
	ReturnMessage string `json:"return_message"`
	Status        string `json:"status"`
	ProviderTxID  string `json:"provider_tx_id"`
	PaymentID     string `json:"payment_id"`
	Duplicate     bool   `json:"duplicate"`
}

type e2eReturnItemDTO struct {
	OrderItemID string `json:"order_item_id,omitempty"`
	SKUID       string `json:"sku_id,omitempty"`
	Quantity    int32  `json:"quantity"`
}

type e2eReturnRequestDTO struct {
	ID                  string             `json:"id"`
	OrderID             string             `json:"order_id"`
	BuyerID             string             `json:"buyer_id"`
	ShopID              string             `json:"shop_id"`
	Items               []e2eReturnItemDTO `json:"items"`
	Reason              string             `json:"reason"`
	Photos              []string           `json:"photos"`
	Status              string             `json:"status"`
	SellerNote          string             `json:"seller_note"`
	ReverseTrackingCode string             `json:"reverse_tracking_code"`
	ReverseLabelURL     string             `json:"reverse_label_url"`
	RefundAmountVND     int64              `json:"refund_amount_vnd"`
	RefundID            string             `json:"refund_id"`
}

type e2eCreateReturnResponse struct {
	Order           OrderDTO            `json:"order"`
	ReturnRequestID string              `json:"return_request_id"`
	ReturnRequest   e2eReturnRequestDTO `json:"return_request"`
}

type e2eReturnsListResponse struct {
	Items    []e2eReturnRequestDTO `json:"items"`
	Page     int32                 `json:"page"`
	PageSize int32                 `json:"page_size"`
	Total    int64                 `json:"total"`
}

type e2eComplaintDTO struct {
	ID              string   `json:"id"`
	ReturnRequestID string   `json:"return_request_id"`
	OrderID         string   `json:"order_id"`
	BuyerID         string   `json:"buyer_id"`
	ShopID          string   `json:"shop_id"`
	Reason          string   `json:"reason"`
	EvidenceURLs    []string `json:"evidence_urls"`
	Status          string   `json:"status"`
	Resolution      string   `json:"resolution"`
	AdminNote       string   `json:"admin_note"`
	ResolvedBy      string   `json:"resolved_by"`
}

type e2eReturnActionResponse struct {
	Order         OrderDTO            `json:"order"`
	ReturnRequest e2eReturnRequestDTO `json:"return_request"`
	Complaint     *e2eComplaintDTO    `json:"complaint"`
}

type e2eCreateComplaintResponse struct {
	Complaint e2eComplaintDTO `json:"complaint"`
}

type e2eComplaintsListResponse struct {
	Items    []e2eComplaintDTO `json:"items"`
	Page     int32             `json:"page"`
	PageSize int32             `json:"page_size"`
	Total    int64             `json:"total"`
}

type e2eResolveComplaintResponse struct {
	Complaint     e2eComplaintDTO     `json:"complaint"`
	Order         OrderDTO            `json:"order"`
	ReturnRequest e2eReturnRequestDTO `json:"return_request"`
}

type e2eReconciliationReportSummaryDTO struct {
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

type e2eReconciliationReportsResponse struct {
	Reports []e2eReconciliationReportSummaryDTO `json:"reports"`
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
	key2 string,
	paymentID, orderGroupID, orderID, appTransID, zpTransID string,
	amountVND, serverTimeMs int64,
	overrideMAC string,
) []byte {
	embedBytes, _ := json.Marshal(map[string]string{
		"payment_id":     paymentID,
		"order_group_id": orderGroupID,
		"order_id":       orderID,
	})
	dataBytes, _ := json.Marshal(map[string]any{
		"app_id":           2554,
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

func extractAppTransIDFromQRURL(qrURL string) string {
	u, err := url.Parse(qrURL)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(u.Query().Get("app_trans_id"))
}

// deliverVietQROrder places an online-paid VietQR order, settles payment, ships via seller,
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
		AddressID:     buyer.AddressID,
		FromCart:      false,
		Items:         []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 1}},
		PaymentMethod: "VIETQR",
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

	// Seller confirms -> PREPARING, then ships -> SHIPPING.
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
	return delivered, co.PaymentID
}

func TestE2E_OrderDepth_ShippingZaloPayReturnsComplaintsAndReconciliation(t *testing.T) {
	env := getEnv()
	registerRateLimitResetCleanup(t, env)

	adminTok := env.adminLogin(t)
	cat, seller1, seller2 := env.ensureBaseMarketplace(t)

	buyer1 := env.registerVerifyAndLogin(t, "buyer-depth-1")
	_ = env.addAddress(t, buyer1)
	buyer2 := env.registerVerifyAndLogin(t, "buyer-depth-2")

	// (a) Shipping: legacy 3-level VN address mapping + multi-carrier fee calculation (ghn + ghtk).
	t.Run("Shipping_MapLegacyAndMultiCarrierFee", func(t *testing.T) {
		// Standard 3-level HCMC address.
		map1Resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/shipping/addresses/map-legacy", "", nil, map[string]string{
			"legacy_province": "Thành phố Hồ Chí Minh",
			"legacy_district": "Quận 1",
			"legacy_ward":     "Phường Bến Nghé",
		}, http.StatusOK)
		var map1 e2eMapLegacyAddressResponse
		if err := DecodeJSON(map1Resp, &map1); err != nil {
			t.Fatalf("decode map-legacy HCMC: %v", err)
		}
		if !map1.Found || map1.ProvinceCode != "79" || map1.WardCode != "79001" {
			t.Fatalf("unexpected HCMC legacy mapping: %+v", map1)
		}

		// Merged province (Bình Dương -> TP.HCM code 79).
		map2Resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/shipping/addresses/map-legacy", "", nil, map[string]string{
			"legacy_province": "Tỉnh Bình Dương",
			"legacy_district": "Thành phố Thủ Dầu Một",
			"legacy_ward":     "Phường Phú Cường",
		}, http.StatusOK)
		var map2 e2eMapLegacyAddressResponse
		if err := DecodeJSON(map2Resp, &map2); err != nil {
			t.Fatalf("decode map-legacy Binh Duong: %v", err)
		}
		if !map2.Found || map2.ProvinceCode != "79" || map2.WardCode != "79006" {
			t.Fatalf("unexpected Binh Duong legacy mapping: %+v", map2)
		}

		// Calculate shipping fee across inter-region route (Hanoi -> HCMC) and verify multiple carriers including ghn & ghtk.
		feeResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/shipping/calculate-fee", buyer1.AccessToken, nil, CalculateFeeRequest{
			ShopID:           seller1.ShopID,
			FromProvinceCode: "01",
			FromWardCode:     "01001",
			ToProvinceCode:   "79",
			ToWardCode:       "79001",
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

	// (b) ZaloPay checkout, HMAC/timestamp security, 3x idempotent replay, double-entry ledger balance.
	t.Run("ZaloPay_CheckoutHMACReplayAndBalancedLedger", func(t *testing.T) {
		ctx := env.ctx(t)
		_, skuZP := env.createApprovedProduct(t, seller1, cat.ID, "ZaloPay SmartWatch E2E "+env.runID, 350000, 20, 600)
		quote := env.calculateShippingQuote(t, buyer1.AccessToken, seller1.ShopID, 600, 0)

		coResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer1.AccessToken, map[string]string{
			"Idempotency-Key": "zp-e2e-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID:     buyer1.AddressID,
			FromCart:      false,
			Items:         []CheckoutItemInputDTO{{SKUID: skuZP.ID, Quantity: 1}},
			PaymentMethod: "ZALOPAY",
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

		// Read app_trans_id from GET /api/v1/payments/{id}.
		payResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/payments/"+url.PathEscape(co.PaymentID), buyer1.AccessToken, nil, nil, http.StatusOK)
		var payDetail e2ePaymentDetailResponse
		if err := DecodeJSON(payResp, &payDetail); err != nil {
			t.Fatalf("decode ZaloPay payment detail: %v", err)
		}
		appTransID := strings.TrimSpace(payDetail.Payment.ProviderRef)
		if appTransID == "" {
			appTransID = extractAppTransIDFromQRURL(payDetail.Payment.QRURL)
		}
		if appTransID == "" {
			t.Fatalf("could not extract ZaloPay app_trans_id from payment %+v", payDetail.Payment)
		}

		key2 := envOr("ZALOPAY_KEY2", "zalopay-mock-key2-dev")
		ledgerBefore := env.getLedgerSummary(t)

		// 1. Invalid HMAC & missing HMAC rejected (401).
		badSigBody := buildSignedZaloPayCallback(
			"wrong-zalopay-key2", co.PaymentID, co.CheckoutGroupID, orderZP.ID, appTransID,
			"ZP-BADSIG-"+uuid.NewString(), co.TotalPayableVND, time.Now().UTC().UnixMilli(), "",
		)
		badSigResp, err := env.client.DoJSON(ctx, http.MethodPost, "/webhooks/zalopay", "", nil, badSigBody)
		if err != nil {
			t.Fatalf("POST /webhooks/zalopay invalid mac: %v", err)
		}
		if badSigResp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected HTTP 401 for invalid ZaloPay MAC, got %d: %s", badSigResp.StatusCode, string(badSigResp.Body))
		}

		missingSigBody := buildSignedZaloPayCallback(
			"", co.PaymentID, co.CheckoutGroupID, orderZP.ID, appTransID,
			"ZP-NOSIG-"+uuid.NewString(), co.TotalPayableVND, time.Now().UTC().UnixMilli(), "",
		)
		missingSigResp, err := env.client.DoJSON(ctx, http.MethodPost, "/webhooks/zalopay", "", nil, missingSigBody)
		if err != nil {
			t.Fatalf("POST /webhooks/zalopay missing mac: %v", err)
		}
		if missingSigResp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected HTTP 401 for missing ZaloPay MAC, got %d: %s", missingSigResp.StatusCode, string(missingSigResp.Body))
		}

		// 2. Stale server_time (> 5 minutes old) with valid HMAC rejected (401).
		staleTimeMs := time.Now().UTC().Add(-10 * time.Minute).UnixMilli()
		staleBody := buildSignedZaloPayCallback(
			key2, co.PaymentID, co.CheckoutGroupID, orderZP.ID, appTransID,
			"ZP-STALE-"+uuid.NewString(), co.TotalPayableVND, staleTimeMs, "",
		)
		staleResp, err := env.client.DoJSON(ctx, http.MethodPost, "/webhooks/zalopay", "", nil, staleBody)
		if err != nil {
			t.Fatalf("POST /webhooks/zalopay stale timestamp: %v", err)
		}
		if staleResp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected HTTP 401 for stale ZaloPay server_time, got %d: %s", staleResp.StatusCode, string(staleResp.Body))
		}

		// Payment MUST still be PENDING after all rejected callbacks.
		payStillPending := env.getPayment(t, buyer1.AccessToken, co.PaymentID)
		if !StatusEquals(payStillPending.Status, "PENDING") {
			t.Fatalf("expected ZaloPay payment PENDING after rejected callbacks, got %q", payStillPending.Status)
		}

		// 3. Valid HMAC callback replayed 3x -> 1 fresh + 2 idempotent duplicates.
		zpTxID := "ZP-TX-" + uuid.NewString()
		validBody := buildSignedZaloPayCallback(
			key2, co.PaymentID, co.CheckoutGroupID, orderZP.ID, appTransID,
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
			if attempt == 1 && out.Duplicate {
				t.Fatalf("expected duplicate=false on first valid ZaloPay callback, got %+v", out)
			}
			if attempt > 1 && !out.Duplicate {
				t.Fatalf("expected duplicate=true on ZaloPay replay #%d, got %+v", attempt, out)
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

		// Double-entry ledger balanced & debits posted once (not 3x).
		ledgerAfter := env.getLedgerSummary(t)
		if !ledgerAfter.IsBalanced || ledgerAfter.TotalDebitsVND != ledgerAfter.TotalCreditsVND {
			t.Fatalf("ledger imbalanced after ZaloPay: debits=%d credits=%d balanced=%v",
				ledgerAfter.TotalDebitsVND, ledgerAfter.TotalCreditsVND, ledgerAfter.IsBalanced)
		}
		delta := ledgerAfter.TotalDebitsVND - ledgerBefore.TotalDebitsVND
		if delta <= 0 || delta >= 2*co.TotalPayableVND {
			t.Fatalf("expected single ZaloPay payment ledger delta (~%d VND), got delta=%d", co.TotalPayableVND, delta)
		}
	})

	// (c.1) Return Approved -> Reverse Shipment -> Seller Confirm Received -> REFUNDED + Ledger + Seller Balance + RBAC.
	t.Run("Returns_ApproveReverseShipmentConfirmReceivedAndRBAC", func(t *testing.T) {
		_, skuRet1 := env.createApprovedProduct(t, seller1, cat.ID, "Return Approve Jacket E2E "+env.runID, 280000, 20, 500)
		order1, _ := deliverVietQROrder(t, env, buyer1, seller1, skuRet1)

		ledgerBefore := env.getLedgerSummary(t)
		balBeforeResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/balance", seller1.AccessToken, nil, nil, http.StatusOK)
		var balBefore SellerBalanceResponse
		if err := DecodeJSON(balBeforeResp, &balBefore); err != nil {
			t.Fatalf("decode seller1 balance before return: %v", err)
		}

		var itemInput map[string]any
		if len(order1.Items) > 0 {
			itemInput = map[string]any{
				"order_item_id": order1.Items[0].ID,
				"sku_id":        order1.Items[0].SKUID,
				"quantity":      order1.Items[0].Quantity,
			}
		} else {
			itemInput = map[string]any{"sku_id": skuRet1.ID, "quantity": 1}
		}

		// Buyer creates return request with items, reason, and photos.
		crResp := env.mustDoJSON(t, http.MethodPost,
			"/api/v1/orders/"+url.PathEscape(order1.ID)+"/returns",
			buyer1.AccessToken, nil, map[string]any{
				"items":  []map[string]any{itemInput},
				"reason": "Sản phẩm có vết rách ở đường may cổ áo",
				"photos": []string{"https://cdn.example.com/evidence/return-photo-1.jpg"},
			}, http.StatusCreated, http.StatusOK)
		var crOut e2eCreateReturnResponse
		if err := DecodeJSON(crResp, &crOut); err != nil {
			t.Fatalf("decode create return: %v", err)
		}
		rrID := crOut.ReturnRequest.ID
		if rrID == "" {
			rrID = crOut.ReturnRequestID
		}
		if rrID == "" || !StatusEquals(crOut.Order.Status, "RETURN_REQUESTED") {
			t.Fatalf("unexpected create return response: %+v", crOut)
		}

		// Buyer lists own order returns.
		bRetResp := env.mustDoJSON(t, http.MethodGet,
			"/api/v1/orders/"+url.PathEscape(order1.ID)+"/returns",
			buyer1.AccessToken, nil, nil, http.StatusOK)
		var bRetOut e2eReturnsListResponse
		if err := DecodeJSON(bRetResp, &bRetOut); err != nil {
			t.Fatalf("decode buyer list returns: %v", err)
		}
		if len(bRetOut.Items) == 0 || bRetOut.Items[0].ID != rrID {
			t.Fatalf("buyer GET /orders/%s/returns missing %s: %+v", order1.ID, rrID, bRetOut)
		}

		// RBAC checks: buyer2 cannot view buyer1's returns; buyer1 cannot approve; seller2 cannot approve seller1's return.
		env.mustDoJSON(t, http.MethodGet,
			"/api/v1/orders/"+url.PathEscape(order1.ID)+"/returns",
			buyer2.AccessToken, nil, nil, http.StatusForbidden)
		env.mustDoJSON(t, http.MethodPost,
			"/api/v1/seller/returns/"+url.PathEscape(rrID)+"/approve",
			buyer1.AccessToken, nil, map[string]any{"note": "buyer self-approve"}, http.StatusForbidden)
		env.mustDoJSON(t, http.MethodPost,
			"/api/v1/seller/returns/"+url.PathEscape(rrID)+"/approve",
			seller2.AccessToken, nil, map[string]any{"note": "wrong shop approve"}, http.StatusForbidden)

		// Seller1 lists shop returns.
		sRetResp := env.mustDoJSON(t, http.MethodGet,
			"/api/v1/seller/returns?order_id="+url.QueryEscape(order1.ID),
			seller1.AccessToken, nil, nil, http.StatusOK)
		var sRetOut e2eReturnsListResponse
		if err := DecodeJSON(sRetResp, &sRetOut); err != nil {
			t.Fatalf("decode seller list returns: %v", err)
		}
		if len(sRetOut.Items) == 0 || sRetOut.Items[0].ID != rrID {
			t.Fatalf("seller1 GET /seller/returns missing %s: %+v", rrID, sRetOut)
		}

		// Seller1 approves return with reverse shipment (immediate_refund=false) -> RETURNING + reverse tracking code.
		appResp := env.mustDoJSON(t, http.MethodPost,
			"/api/v1/seller/returns/"+url.PathEscape(rrID)+"/approve",
			seller1.AccessToken, nil, map[string]any{
				"note":             "Đồng ý nhận lại hàng hoàn",
				"immediate_refund": false,
			}, http.StatusOK)
		var appOut e2eReturnActionResponse
		if err := DecodeJSON(appResp, &appOut); err != nil {
			t.Fatalf("decode seller approve return: %v", err)
		}
		if !StatusEquals(appOut.Order.Status, "RETURNING") || appOut.ReturnRequest.ReverseTrackingCode == "" {
			t.Fatalf("expected RETURNING + reverse_tracking_code after approve, got %+v", appOut)
		}

		// Verify reverse shipment exists on shipping service.
		revShipResp := env.mustDoJSON(t, http.MethodGet,
			"/api/v1/shipping/shipments/"+url.PathEscape(order1.ID)+"?direction=return",
			buyer1.AccessToken, nil, nil, http.StatusOK)
		var revShip e2eShipmentDetailResponse
		if err := DecodeJSON(revShipResp, &revShip); err != nil {
			t.Fatalf("decode reverse shipment: %v", err)
		}
		if revShip.Shipment.TrackingCode != appOut.ReturnRequest.ReverseTrackingCode {
			t.Fatalf("reverse shipment tracking mismatch: %q vs %q",
				revShip.Shipment.TrackingCode, appOut.ReturnRequest.ReverseTrackingCode)
		}

		// Seller1 confirms returned goods received -> REFUNDED + refund ID + balanced ledger + updated seller balance.
		confResp := env.mustDoJSON(t, http.MethodPost,
			"/api/v1/seller/returns/"+url.PathEscape(rrID)+"/confirm-received",
			seller1.AccessToken, nil, map[string]any{
				"note": "Đã nhận hàng hoàn nguyên vẹn, hoàn tiền cho khách",
			}, http.StatusOK)
		var confOut e2eReturnActionResponse
		if err := DecodeJSON(confResp, &confOut); err != nil {
			t.Fatalf("decode confirm return received: %v", err)
		}
		if !StatusEquals(confOut.Order.Status, "REFUNDED") ||
			!StatusEquals(confOut.ReturnRequest.Status, "REFUNDED") ||
			confOut.ReturnRequest.RefundID == "" ||
			confOut.ReturnRequest.RefundAmountVND <= 0 {
			t.Fatalf("expected REFUNDED order & return with refund_id after confirm-received, got %+v", confOut)
		}

		// Verify double-entry ledger stays balanced & posted refund reversal.
		ledgerAfter := env.getLedgerSummary(t)
		if !ledgerAfter.IsBalanced || ledgerAfter.TotalDebitsVND != ledgerAfter.TotalCreditsVND {
			t.Fatalf("ledger imbalanced after return refund: debits=%d credits=%d balanced=%v",
				ledgerAfter.TotalDebitsVND, ledgerAfter.TotalCreditsVND, ledgerAfter.IsBalanced)
		}
		if ledgerAfter.TotalDebitsVND <= ledgerBefore.TotalDebitsVND {
			t.Fatalf("expected ledger debits to grow after refund posting: before=%d after=%d",
				ledgerBefore.TotalDebitsVND, ledgerAfter.TotalDebitsVND)
		}

		// Verify seller1 balance endpoint succeeds and pending escrow did not grow.
		balAfterResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/balance", seller1.AccessToken, nil, nil, http.StatusOK)
		var balAfter SellerBalanceResponse
		if err := DecodeJSON(balAfterResp, &balAfter); err != nil {
			t.Fatalf("decode seller1 balance after refund: %v", err)
		}
		if balAfter.ShopID != seller1.ShopID || balAfter.PendingVND > balBefore.PendingVND {
			t.Fatalf("unexpected seller1 balance after return refund: before=%+v after=%+v", balBefore, balAfter)
		}
	})

	// (c.2) Return Rejected -> Complaint Escalated -> Buyer Complaint Evidence -> Admin Resolve Complaint + RBAC.
	t.Run("Returns_RejectEscalateComplaintAndAdminResolve", func(t *testing.T) {
		_, skuRet2 := env.createApprovedProduct(t, seller1, cat.ID, "Return Reject Shoes E2E "+env.runID, 310000, 20, 700)
		order2, _ := deliverVietQROrder(t, env, buyer1, seller1, skuRet2)

		crResp := env.mustDoJSON(t, http.MethodPost,
			"/api/v1/orders/"+url.PathEscape(order2.ID)+"/returns",
			buyer1.AccessToken, nil, map[string]any{
				"reason": "Giao sai kích cỡ so với đơn đặt",
				"photos": []string{"https://cdn.example.com/evidence/wrong-size.jpg"},
			}, http.StatusCreated, http.StatusOK)
		var crOut e2eCreateReturnResponse
		if err := DecodeJSON(crResp, &crOut); err != nil {
			t.Fatalf("decode create return2: %v", err)
		}
		rr2ID := crOut.ReturnRequest.ID
		if rr2ID == "" {
			rr2ID = crOut.ReturnRequestID
		}

		// Seller1 rejects return -> automatically escalates OPEN complaint.
		rejResp := env.mustDoJSON(t, http.MethodPost,
			"/api/v1/seller/returns/"+url.PathEscape(rr2ID)+"/reject",
			seller1.AccessToken, nil, map[string]any{
				"reason": "Shop kiểm tra đóng gói đúng size đã đặt",
			}, http.StatusOK)
		var rejOut e2eReturnActionResponse
		if err := DecodeJSON(rejResp, &rejOut); err != nil {
			t.Fatalf("decode seller reject return: %v", err)
		}
		if !StatusEquals(rejOut.Order.Status, "RETURN_REJECTED") ||
			!StatusEquals(rejOut.ReturnRequest.Status, "REJECTED") {
			t.Fatalf("unexpected reject return response: %+v", rejOut)
		}
		if rejOut.Complaint == nil || rejOut.Complaint.ID == "" || !StatusEquals(rejOut.Complaint.Status, "OPEN") {
			t.Fatalf("expected auto-escalated OPEN complaint on return reject, got %+v", rejOut.Complaint)
		}

		// Buyer submits/updates complaint via POST /api/v1/orders/{id}/complaints.
		cmpResp := env.mustDoJSON(t, http.MethodPost,
			"/api/v1/orders/"+url.PathEscape(order2.ID)+"/complaints",
			buyer1.AccessToken, nil, map[string]any{
				"return_request_id": rr2ID,
				"reason":            "Bổ sung hình ảnh tem size thực tế không khớp đơn hàng",
				"evidence_urls":     []string{"https://cdn.example.com/evidence/size-tag.jpg"},
			}, http.StatusCreated, http.StatusOK)
		var cmpOut e2eCreateComplaintResponse
		if err := DecodeJSON(cmpResp, &cmpOut); err != nil {
			t.Fatalf("decode create complaint: %v", err)
		}
		complaintID := cmpOut.Complaint.ID
		if complaintID == "" || !StatusEquals(cmpOut.Complaint.Status, "OPEN") {
			t.Fatalf("unexpected complaint response: %+v", cmpOut)
		}

		// RBAC: buyer and seller cannot access admin complaints endpoints.
		env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/complaints", buyer1.AccessToken, nil, nil, http.StatusForbidden)
		env.mustDoJSON(t, http.MethodPost,
			"/api/v1/admin/complaints/"+url.PathEscape(complaintID)+"/resolve",
			buyer1.AccessToken, nil, map[string]any{"resolution": "REFUND", "admin_note": "buyer hijack"}, http.StatusForbidden)
		env.mustDoJSON(t, http.MethodPost,
			"/api/v1/admin/complaints/"+url.PathEscape(complaintID)+"/resolve",
			seller1.AccessToken, nil, map[string]any{"resolution": "DISMISS", "admin_note": "seller hijack"}, http.StatusForbidden)

		// Admin lists open complaints and sees our complaint.
		listCmpResp := env.mustDoJSON(t, http.MethodGet,
			"/api/v1/admin/complaints?status=OPEN&order_id="+url.QueryEscape(order2.ID),
			adminTok, nil, nil, http.StatusOK)
		var listCmpOut e2eComplaintsListResponse
		if err := DecodeJSON(listCmpResp, &listCmpOut); err != nil {
			t.Fatalf("decode admin list complaints: %v", err)
		}
		foundCmp := false
		for _, c := range listCmpOut.Items {
			if c.ID == complaintID {
				foundCmp = true
				break
			}
		}
		if !foundCmp {
			t.Fatalf("admin GET /admin/complaints missing complaint %s: %+v", complaintID, listCmpOut)
		}

		// Admin resolves complaint with REFUND (immediate refund, no return shipping).
		resResp := env.mustDoJSON(t, http.MethodPost,
			"/api/v1/admin/complaints/"+url.PathEscape(complaintID)+"/resolve",
			adminTok, nil, map[string]any{
				"resolution":              "REFUND",
				"admin_note":              "Xác minh bằng chứng giao sai size, hoàn tiền ngay cho người mua",
				"require_return_shipping": false,
			}, http.StatusOK)
		var resOut e2eResolveComplaintResponse
		if err := DecodeJSON(resResp, &resOut); err != nil {
			t.Fatalf("decode admin resolve complaint: %v", err)
		}
		if !StatusEquals(resOut.Complaint.Status, "RESOLVED") ||
			!StatusEquals(resOut.Complaint.Resolution, "REFUND") ||
			!StatusEquals(resOut.Order.Status, "REFUNDED") {
			t.Fatalf("unexpected resolve complaint response: %+v", resOut)
		}

		// Ledger stays balanced after complaint-driven refund.
		ledgerAfter := env.getLedgerSummary(t)
		if !ledgerAfter.IsBalanced || ledgerAfter.TotalDebitsVND != ledgerAfter.TotalCreditsVND {
			t.Fatalf("ledger imbalanced after admin complaint refund: debits=%d credits=%d balanced=%v",
				ledgerAfter.TotalDebitsVND, ledgerAfter.TotalCreditsVND, ledgerAfter.IsBalanced)
		}
	})

	// (c.3) Admin reconciliation reports endpoint + RBAC.
	t.Run("AdminReconciliationReportsAndRBAC", func(t *testing.T) {
		// Trigger a reconciliation run so at least one persisted report exists.
		env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/payments/reconcile", adminTok, nil, map[string]any{}, http.StatusOK)

		repResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/payments/reconciliation-reports?limit=10", adminTok, nil, nil, http.StatusOK)
		var repOut e2eReconciliationReportsResponse
		if err := DecodeJSON(repResp, &repOut); err != nil {
			t.Fatalf("decode reconciliation reports: %v", err)
		}
		if len(repOut.Reports) == 0 || repOut.Reports[0].RunID == "" {
			t.Fatalf("expected >= 1 reconciliation report with run_id, got %+v", repOut)
		}

		// Buyer and seller MUST get 403 on admin reconciliation reports.
		env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/payments/reconciliation-reports", buyer1.AccessToken, nil, nil, http.StatusForbidden)
		env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/payments/reconciliation-reports", seller1.AccessToken, nil, nil, http.StatusForbidden)
	})
}
