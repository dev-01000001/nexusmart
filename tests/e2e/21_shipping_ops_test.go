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
)

type shpW2ShipmentDTO struct {
	ShipmentID      string `json:"shipment_id"`
	OrderID         string `json:"order_id"`
	ShopID          string `json:"shop_id"`
	CarrierCode     string `json:"carrier_code"`
	ServiceCode     string `json:"service_code"`
	TrackingCode    string `json:"tracking_code"`
	LabelURL        string `json:"label_url"`
	Status          string `json:"status"`
	FeeVND          int64  `json:"fee_vnd"`
	CODAmountVND    int64  `json:"cod_amount_vnd"`
	Direction       string `json:"direction"`
	ReturnRequestID string `json:"return_request_id"`
	RedeliveryCount int32  `json:"redelivery_count"`
}

type shpW2ShipmentEventDTO struct {
	ID          string `json:"id"`
	ShipmentID  string `json:"shipment_id"`
	Status      string `json:"status"`
	RawStatus   string `json:"raw_status"`
	Description string `json:"description"`
	OccurredAt  string `json:"occurred_at"`
}

type shpW2GetShipmentResponse struct {
	Shipment shpW2ShipmentDTO        `json:"shipment"`
	Events   []shpW2ShipmentEventDTO `json:"events"`
}

type shpW2RedeliveryResponse struct {
	Shipment shpW2ShipmentDTO `json:"shipment"`
}

type shpW2ShopSettingsDTO struct {
	ShopID              string   `json:"shop_id"`
	EnabledCarrierCodes []string `json:"enabled_carrier_codes"`
	DefaultWeightGrams  int32    `json:"default_weight_grams"`
	DefaultLengthCM     int32    `json:"default_length_cm"`
	DefaultWidthCM      int32    `json:"default_width_cm"`
	DefaultHeightCM     int32    `json:"default_height_cm"`
}

type shpW2ShopSettingsResponse struct {
	Settings shpW2ShopSettingsDTO `json:"settings"`
}

type shpW2FallbackRateDTO struct {
	Region             string `json:"region"`
	BaseFeeVND         int64  `json:"base_fee_vnd"`
	BaseWeightGrams    int32  `json:"base_weight_grams"`
	ExtraFeeVNDPer500g int64  `json:"extra_fee_vnd_per_500g"`
}

type shpW2CarrierConfigDTO struct {
	CarrierCode     string                 `json:"carrier_code"`
	Name            string                 `json:"name"`
	Enabled         bool                   `json:"enabled"`
	TokenConfigured bool                   `json:"token_configured"`
	FallbackRates   []shpW2FallbackRateDTO `json:"fallback_rates"`
}

type shpW2ListCarriersResponse struct {
	Carriers []shpW2CarrierConfigDTO `json:"carriers"`
}

type shpW2CarrierResponse struct {
	Carrier shpW2CarrierConfigDTO `json:"carrier"`
}

type shpW2TestCarrierResponse struct {
	Ok      bool   `json:"ok"`
	Message string `json:"message"`
}

func shpW2AddAddress(t *testing.T, env *testEnv, u *userFixture, provCode, provName, wardCode, wardName string) AddressDTO {
	t.Helper()
	resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/users/me/addresses", u.AccessToken, nil, CreateAddressRequest{
		RecipientName:  "Nguyen Van ShipW2",
		RecipientPhone: u.Phone,
		ProvinceCode:   provCode,
		ProvinceName:   provName,
		WardCode:       wardCode,
		WardName:       wardName,
		StreetAddress:  fmt.Sprintf("42 Nguyen Hue #%d", env.nextSeq()),
		IsDefault:      true,
	}, http.StatusCreated, http.StatusOK)

	var out CreateAddressResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode create address: %v", err)
	}
	u.AddressID = out.Address.ID
	return out.Address
}

func shpW2CreateSeller(t *testing.T, env *testEnv, label string) *sellerFixture {
	t.Helper()
	u := env.registerVerifyAndLogin(t, label)
	addr := shpW2AddAddress(t, env, u, "79", "Thành phố Hồ Chí Minh", "26740", "Phường Sài Gòn")

	shopName := fmt.Sprintf("ShipW2Shop-%s-%s-%d", label, env.runID, env.nextSeq())
	shopResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/shops/register", u.AccessToken, nil, RegisterShopRequest{
		Name:               shopName,
		Description:        "E2E wave 2a shipping shop",
		PickupAddressID:    addr.ID,
		ContactPhone:       u.Phone,
		BusinessLicenseDoc: "https://example.com/docs/license.pdf",
	}, http.StatusCreated, http.StatusOK)
	var shopOut ShopResponse
	if err := DecodeJSON(shopResp, &shopOut); err != nil {
		t.Fatalf("decode register shop: %v", err)
	}

	adminTok := env.adminLogin(t)
	reviewPath := fmt.Sprintf("/api/v1/admin/shops/%s/review", url.PathEscape(shopOut.Shop.ID))
	env.mustDoJSON(t, http.MethodPost, reviewPath, adminTok, nil, ReviewRequest{
		Decision: "APPROVE",
		Reason:   "E2E w2a shipping approval",
	}, http.StatusOK)

	reLoginResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/auth/login", "", nil, LoginRequest{
		Email:    u.Email,
		Password: u.Password,
	}, http.StatusOK)
	var reLoginOut LoginResponse
	if err := DecodeJSON(reLoginResp, &reLoginOut); err != nil {
		t.Fatalf("decode seller re-login: %v", err)
	}
	u.AccessToken = reLoginOut.AccessToken
	u.RefreshToken = reLoginOut.RefreshToken
	u.User = reLoginOut.User

	return &sellerFixture{
		userFixture: *u,
		ShopID:      shopOut.Shop.ID,
		ShopName:    shopName,
	}
}

func shpW2CreateAndShipOrder(t *testing.T, env *testEnv, buyer *userFixture, buyerAddrID string, seller *sellerFixture, skuID string) (OrderDTO, SellerShipResponse) {
	t.Helper()
	checkoutResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
		"Idempotency-Key": "shpw2-ord-" + uuid.NewString(),
	}, CheckoutRequest{
		AddressID:     buyerAddrID,
		FromCart:      false,
		Items:         []CheckoutItemInputDTO{{SKUID: skuID, Quantity: 1}},
		PaymentMethod: "COD",
		ShippingChoices: []ShopShippingChoiceDTO{
			{ShopID: seller.ShopID, CarrierCode: "ghn", ServiceCode: "STANDARD"},
		},
	}, http.StatusCreated, http.StatusOK)
	var co CheckoutResponse
	if err := DecodeJSON(checkoutResp, &co); err != nil {
		t.Fatalf("decode checkout: %v", err)
	}
	if len(co.Orders) != 1 {
		t.Fatalf("expected 1 order, got %d", len(co.Orders))
	}
	ord := co.Orders[0]

	confirmPath := fmt.Sprintf("/api/v1/seller/orders/%s/confirm", url.PathEscape(ord.ID))
	env.mustDoJSON(t, http.MethodPost, confirmPath, seller.AccessToken, nil, nil, http.StatusOK)

	shipPath := fmt.Sprintf("/api/v1/seller/orders/%s/ship", url.PathEscape(ord.ID))
	shipResp := env.mustDoJSON(t, http.MethodPost, shipPath, seller.AccessToken, nil, SellerShipRequest{
		CarrierCode: "ghn",
		ServiceCode: "STANDARD",
	}, http.StatusOK)
	var shipOut SellerShipResponse
	if err := DecodeJSON(shipResp, &shipOut); err != nil {
		t.Fatalf("decode ship response: %v", err)
	}
	if shipOut.TrackingCode == "" {
		t.Fatalf("expected non-empty tracking_code")
	}
	if strings.Contains(shipOut.LabelURL, ".local") {
		t.Fatalf("SHP-B8: label_url must not use .local domain, got %q", shipOut.LabelURL)
	}
	return ord, shipOut
}

func shpW2GetShipment(t *testing.T, env *testEnv, token, orderID, direction string) shpW2GetShipmentResponse {
	t.Helper()
	path := fmt.Sprintf("/api/v1/shipping/shipments/%s", url.PathEscape(orderID))
	if direction != "" {
		path += "?direction=" + url.QueryEscape(direction)
	}
	resp := env.mustDoJSON(t, http.MethodGet, path, token, nil, nil, http.StatusOK)
	var out shpW2GetShipmentResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode shipment: %v", err)
	}
	return out
}

func shpW2PostWebhook(t *testing.T, env *testEnv, carrierCode, trackingCode, orderID, rawStatus, desc string, occurredAt time.Time) {
	t.Helper()
	ctx := env.ctx(t)
	resp, err := env.client.PostSignedWebhook(ctx, "/webhooks/shipping/"+url.PathEscape(carrierCode), ShippingWebhookPayload{
		EventID:      "shpw2-wh-" + uuid.NewString(),
		TrackingCode: trackingCode,
		OrderID:      orderID,
		RawStatus:    rawStatus,
		Description:  desc,
		OccurredAt:   occurredAt.UTC().Format(time.RFC3339),
	}, env.cfg.ShippingWebhookSecret, time.Now().UTC(), "")
	if err != nil {
		t.Fatalf("post shipping webhook (%s/%s): %v", carrierCode, rawStatus, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("post shipping webhook (%s/%s) returned HTTP %d body=%s", carrierCode, rawStatus, resp.StatusCode, string(resp.Body))
	}
}

func TestE2E_ShippingW2AOps(t *testing.T) {
	env := getEnv()
	cat := env.ensureCategory(t)
	adminTok := env.adminLogin(t)

	seller := shpW2CreateSeller(t, env, "shpw2-seller")
	_, sku := env.createApprovedProduct(t, seller, cat.ID, "ShipW2 Jacket", 250000, 50, 400)

	buyer := env.registerVerifyAndLogin(t, "shpw2-buyer1")
	env.verifyBuyerPhone(t, buyer)
	buyerAddr := shpW2AddAddress(t, env, buyer, "79", "Thành phố Hồ Chí Minh", "26740", "Phường Sài Gòn")
	otherBuyer := env.registerVerifyAndLogin(t, "shpw2-buyer2")

	t.Run("AccentInsensitiveSearch", func(t *testing.T) {
		provResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/shipping/provinces?query="+url.QueryEscape("Ha Noi"), "", nil, nil, http.StatusOK)
		var provOut ProvincesResponse
		if err := DecodeJSON(provResp, &provOut); err != nil {
			t.Fatalf("decode provinces: %v", err)
		}
		foundHN := false
		for _, p := range provOut.Provinces {
			if p.Code == "01" {
				foundHN = true
				break
			}
		}
		if !foundHN {
			t.Fatalf("SHP-B8: expected Ha Noi (01) in accent-insensitive search, got %+v", provOut.Provinces)
		}

		wardResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/shipping/provinces/79/wards?query="+url.QueryEscape("Ben Thanh"), "", nil, nil, http.StatusOK)
		var wardOut WardsResponse
		if err := DecodeJSON(wardResp, &wardOut); err != nil {
			t.Fatalf("decode wards: %v", err)
		}
		foundBT := false
		for _, w := range wardOut.Wards {
			if w.Code == "26743" {
				foundBT = true
				break
			}
		}
		if !foundBT {
			t.Fatalf("SHP-B8: expected Phuong Ben Thanh (26743) in accent-insensitive ward search, got %+v", wardOut.Wards)
		}
	})

	t.Run("CancelBeforeAndAfterPicked", func(t *testing.T) {
		// 1. Order A: create shipment (PREPARING), check printable label auth, cancel BEFORE pickup -> 200, shipment CANCELLED.
		ordA, shipA := shpW2CreateAndShipOrder(t, env, buyer, buyerAddr.ID, seller, sku.ID)

		// Verify openable label endpoint directly on shipping HTTP port with RBAC and security headers.
		labelURL := fmt.Sprintf("%s/labels/%s", env.cfg.ShippingHTTPURL, url.PathEscape(shipA.TrackingCode))
		lblOwnerResp, err := env.client.DoAbsolute(env.ctx(t), http.MethodGet, labelURL, seller.AccessToken, nil, nil)
		if err != nil {
			t.Fatalf("GET label as seller: %v", err)
		}
		labelHTML := string(lblOwnerResp.Body)
		if lblOwnerResp.StatusCode != http.StatusOK || !strings.Contains(labelHTML, shipA.TrackingCode) {
			t.Fatalf("expected 200 HTML label containing %s, got HTTP %d body=%s", shipA.TrackingCode, lblOwnerResp.StatusCode, labelHTML)
		}
		const wantLabelCSP = "sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data:; frame-ancestors 'none'"
		if gotCSP := lblOwnerResp.Headers.Get("Content-Security-Policy"); gotCSP != wantLabelCSP {
			t.Fatalf("label Content-Security-Policy = %q, want %q", gotCSP, wantLabelCSP)
		}
		if gotNosniff := lblOwnerResp.Headers.Get("X-Content-Type-Options"); gotNosniff != "nosniff" {
			t.Fatalf("label X-Content-Type-Options = %q, want nosniff", gotNosniff)
		}
		if strings.Contains(strings.ToLower(labelHTML), "onclick") || strings.Contains(strings.ToLower(labelHTML), "window.print") {
			t.Fatalf("label HTML must not contain inline onclick/window.print handlers")
		}
		lblOtherResp, err := env.client.DoAbsolute(env.ctx(t), http.MethodGet, labelURL, otherBuyer.AccessToken, nil, nil)
		if err != nil {
			t.Fatalf("GET label as other buyer: %v", err)
		}
		if lblOtherResp.StatusCode != http.StatusForbidden {
			t.Fatalf("expected 403 for other buyer accessing label, got HTTP %d", lblOtherResp.StatusCode)
		}
		if !strings.HasPrefix(lblOtherResp.Headers.Get("Content-Type"), "application/json") {
			t.Fatalf("label 403 error must have Content-Type application/json, got %q", lblOtherResp.Headers.Get("Content-Type"))
		}

		cancelAPath := fmt.Sprintf("/api/v1/orders/%s/cancel", url.PathEscape(ordA.ID))
		env.mustDoJSON(t, http.MethodPost, cancelAPath, buyer.AccessToken, nil, map[string]string{
			"reason": "huỷ trước khi hãng lấy hàng",
		}, http.StatusOK)

		shA := shpW2GetShipment(t, env, seller.AccessToken, ordA.ID, "FORWARD")
		if !StatusEquals(shA.Shipment.Status, "CANCELLED") {
			t.Fatalf("expected FORWARD shipment CANCELLED after pre-pickup cancel, got %q", shA.Shipment.Status)
		}

		// 2. Order B: create shipment, carrier sends "picked" (-> delivering), cancel MUST return 409!
		ordB, shipB := shpW2CreateAndShipOrder(t, env, buyer, buyerAddr.ID, seller, sku.ID)
		shpW2PostWebhook(t, env, "ghn", shipB.TrackingCode, ordB.ID, "picked", "", time.Now().UTC().Add(time.Second))

		shB := shpW2GetShipment(t, env, seller.AccessToken, ordB.ID, "FORWARD")
		if !StatusEquals(shB.Shipment.Status, "DELIVERING") {
			t.Fatalf("picked must map to DELIVERING, got %q", shB.Shipment.Status)
		}
		if len(shB.Events) == 0 || shB.Events[len(shB.Events)-1].Description != "Đang giao hàng" {
			t.Fatalf("expected Vietnamese description 'Đang giao hàng' for picked, got %+v", shB.Events)
		}

		cancelBPath := fmt.Sprintf("/api/v1/orders/%s/cancel", url.PathEscape(ordB.ID))
		env.mustDoJSON(t, http.MethodPost, cancelBPath, buyer.AccessToken, nil, map[string]string{
			"reason": "huỷ sau khi đã lấy hàng",
		}, http.StatusConflict)
	})

	t.Run("ReturnShipmentWebhookIsolatedFromForward", func(t *testing.T) {
		ordC, shipC := shpW2CreateAndShipOrder(t, env, buyer, buyerAddr.ID, seller, sku.ID)
		baseTime := time.Now().UTC()
		shpW2PostWebhook(t, env, "ghn", shipC.TrackingCode, ordC.ID, "picked", "", baseTime)
		shpW2PostWebhook(t, env, "ghn", shipC.TrackingCode, ordC.ID, "delivered", "", baseTime.Add(time.Minute))

		// Wait for order to reach DELIVERED via NATS shipment.updated.
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "order DELIVERED", func() (bool, string) {
			o := env.getOrder(t, buyer.AccessToken, ordC.ID)
			return StatusEquals(o.Status, "DELIVERED"), o.Status
		})

		// Buyer opens return request -> seller accepts return -> RETURN shipment created.
		retPath := fmt.Sprintf("/api/v1/orders/%s/return", url.PathEscape(ordC.ID))
		retResp := env.mustDoJSON(t, http.MethodPost, retPath, buyer.AccessToken, nil, map[string]any{
			"reason":        "Sai kích cỡ",
			"evidence_urls": []string{"https://example.com/evidence/1.jpg"},
		}, http.StatusOK, http.StatusCreated)
		var retOut struct {
			ReturnRequestID string `json:"return_request_id"`
		}
		if err := DecodeJSON(retResp, &retOut); err != nil || retOut.ReturnRequestID == "" {
			t.Fatalf("decode return_request_id: err=%v body=%s", err, string(retResp.Body))
		}

		respondPath := fmt.Sprintf("/api/v1/seller/returns/%s/respond", url.PathEscape(retOut.ReturnRequestID))
		env.mustDoJSON(t, http.MethodPost, respondPath, seller.AccessToken, nil, map[string]any{
			"decision": "ACCEPT_RETURN",
			"note":     "Đồng ý nhận trả hàng",
		}, http.StatusOK)

		var retShip shpW2GetShipmentResponse
		env.pollUntil(t, env.cfg.PollTimeout, 400*time.Millisecond, "RETURN shipment created", func() (bool, string) {
			p := fmt.Sprintf("/api/v1/shipping/shipments/%s?direction=RETURN", url.PathEscape(ordC.ID))
			r, err := env.client.DoJSON(env.ctx(t), http.MethodGet, p, seller.AccessToken, nil, nil)
			if err != nil {
				return false, err.Error()
			}
			if r.StatusCode != http.StatusOK {
				return false, fmt.Sprintf("HTTP %d: %s", r.StatusCode, string(r.Body))
			}
			if decErr := DecodeJSON(r, &retShip); decErr != nil {
				return false, decErr.Error()
			}
			return retShip.Shipment.TrackingCode != "", string(r.Body)
		})

		if !strings.EqualFold(retShip.Shipment.Direction, "RETURN") || retShip.Shipment.TrackingCode == shipC.TrackingCode {
			t.Fatalf("expected distinct RETURN shipment, got %+v (forward=%s)", retShip.Shipment, shipC.TrackingCode)
		}

		// Drive RETURN shipment through full webhook cycle: picked -> delivered.
		retCarrier := strings.ToLower(retShip.Shipment.CarrierCode)
		shpW2PostWebhook(t, env, retCarrier, retShip.Shipment.TrackingCode, ordC.ID, "picked", "", baseTime.Add(2*time.Minute))
		shpW2PostWebhook(t, env, retCarrier, retShip.Shipment.TrackingCode, ordC.ID, "delivered", "", baseTime.Add(3*time.Minute))

		afterRet := shpW2GetShipment(t, env, seller.AccessToken, ordC.ID, "RETURN")
		if !StatusEquals(afterRet.Shipment.Status, "DELIVERED") {
			t.Fatalf("expected RETURN shipment DELIVERED, got %q", afterRet.Shipment.Status)
		}
		afterFwd := shpW2GetShipment(t, env, seller.AccessToken, ordC.ID, "FORWARD")
		if afterFwd.Shipment.TrackingCode != shipC.TrackingCode || !StatusEquals(afterFwd.Shipment.Status, "DELIVERED") {
			t.Fatalf("FORWARD shipment must be untouched by RETURN webhooks, got %+v", afterFwd.Shipment)
		}
	})

	t.Run("RedeliveryThreeTimesThen409AndOtherBuyerRejected", func(t *testing.T) {
		ordD, shipD := shpW2CreateAndShipOrder(t, env, buyer, buyerAddr.ID, seller, sku.ID)
		baseTime := time.Now().UTC()
		shpW2PostWebhook(t, env, "ghn", shipD.TrackingCode, ordD.ID, "picked", "", baseTime)
		shpW2PostWebhook(t, env, "ghn", shipD.TrackingCode, ordD.ID, "delivery_fail", "", baseTime.Add(time.Minute))

		shD := shpW2GetShipment(t, env, buyer.AccessToken, ordD.ID, "FORWARD")
		if !StatusEquals(shD.Shipment.Status, "FAILED") {
			t.Fatalf("expected shipment status FAILED after delivery_fail, got %q", shD.Shipment.Status)
		}

		redeliveryPath := fmt.Sprintf("/api/v1/orders/%s/redelivery", url.PathEscape(ordD.ID))

		// Other buyer must be rejected with 403 or 404.
		env.mustDoJSON(t, http.MethodPost, redeliveryPath, otherBuyer.AccessToken, nil, map[string]string{
			"preferred_date": "2026-11-28",
			"note":           "unauthorized attempt",
		}, http.StatusForbidden, http.StatusNotFound)

		// Owner buyer succeeds 1x per failed delivery attempt up to SHIPPING_MAX_REDELIVERY=3;
		// duplicate request on the same failed attempt returns 409, and 4th attempt returns 409.
		for attempt := int32(1); attempt <= 3; attempt++ {
			if attempt > 1 {
				shpW2PostWebhook(t, env, "ghn", shipD.TrackingCode, ordD.ID, "delivery_fail", "", baseTime.Add(time.Duration(attempt)*time.Minute))
			}
			resp := env.mustDoJSON(t, http.MethodPost, redeliveryPath, buyer.AccessToken, nil, map[string]string{
				"preferred_date": "2026-11-28",
				"note":           fmt.Sprintf("Giao lại lần %d", attempt),
			}, http.StatusOK)
			var out shpW2RedeliveryResponse
			if err := DecodeJSON(resp, &out); err != nil {
				t.Fatalf("decode redelivery attempt %d: %v", attempt, err)
			}
			if out.Shipment.RedeliveryCount != attempt {
				t.Fatalf("attempt %d: redelivery_count=%d, want %d", attempt, out.Shipment.RedeliveryCount, attempt)
			}
			// Immediate duplicate redelivery for the same failed event must return 409.
			env.mustDoJSON(t, http.MethodPost, redeliveryPath, buyer.AccessToken, nil, map[string]string{
				"preferred_date": "2026-11-29",
				"note":           fmt.Sprintf("Trùng lần %d", attempt),
			}, http.StatusConflict)
		}

		// Even after a 4th delivery_fail, 4th redelivery must return 409 (max 3).
		shpW2PostWebhook(t, env, "ghn", shipD.TrackingCode, ordD.ID, "delivery_fail", "", baseTime.Add(4*time.Minute))
		env.mustDoJSON(t, http.MethodPost, redeliveryPath, buyer.AccessToken, nil, map[string]string{
			"preferred_date": "2026-11-29",
			"note":           "attempt 4 must fail",
		}, http.StatusConflict)
	})

	t.Run("ShopSettingsFilterAndRejectDisableAll", func(t *testing.T) {
		shopSeller := shpW2CreateSeller(t, env, "shpw2-shopcfg")

		// Disabling all carriers must fail with 400.
		env.mustDoJSON(t, http.MethodPut, "/api/v1/seller/shipping-settings", shopSeller.AccessToken, nil, map[string]any{
			"enabled_carrier_codes": []string{},
			"default_weight_grams":  500,
		}, http.StatusBadRequest)

		// Seller enables ONLY ghn (disables mock_express).
		updResp := env.mustDoJSON(t, http.MethodPut, "/api/v1/seller/shipping-settings", shopSeller.AccessToken, nil, map[string]any{
			"enabled_carrier_codes": []string{"ghn"},
			"default_weight_grams":  900,
			"default_length_cm":     25,
			"default_width_cm":      20,
			"default_height_cm":     12,
		}, http.StatusOK)
		var cfgOut shpW2ShopSettingsResponse
		if err := DecodeJSON(updResp, &cfgOut); err != nil {
			t.Fatalf("decode shop shipping settings: %v", err)
		}
		if cfgOut.Settings.ShopID != shopSeller.ShopID || len(cfgOut.Settings.EnabledCarrierCodes) != 1 || cfgOut.Settings.EnabledCarrierCodes[0] != "ghn" {
			t.Fatalf("unexpected shop settings: %+v", cfgOut.Settings)
		}

		// Quote for this shop must NOT include mock_express.
		qResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/shipping/calculate-fee", buyer.AccessToken, nil, CalculateFeeRequest{
			ShopID:         shopSeller.ShopID,
			ToProvinceCode: "79",
			ToWardCode:     "26740",
			WeightGrams:    500,
		}, http.StatusOK)
		var qOut CalculateFeeResponse
		if err := DecodeJSON(qResp, &qOut); err != nil {
			t.Fatalf("decode calculate-fee: %v", err)
		}
		if len(qOut.Quotes) == 0 {
			t.Fatalf("expected ghn quotes, got 0")
		}
		for _, q := range qOut.Quotes {
			if strings.EqualFold(q.CarrierCode, "mock_express") {
				t.Fatalf("shop disabled mock_express, but quote still returned it: %+v", qOut.Quotes)
			}
		}
	})

	t.Run("AdminCarrierDisableBadTokenAndRestore", func(t *testing.T) {
		freshSeller := shpW2CreateSeller(t, env, "shpw2-admincfg")

		listResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/carriers", adminTok, nil, nil, http.StatusOK)
		var listOut shpW2ListCarriersResponse
		if err := DecodeJSON(listResp, &listOut); err != nil {
			t.Fatalf("decode admin carriers: %v", err)
		}
		if len(listOut.Carriers) < 2 {
			t.Fatalf("expected at least 2 carriers, got %+v", listOut.Carriers)
		}

		// Always restore mock_express enabled state even if an assertion fails.
		t.Cleanup(func() {
			cleanCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			resp, err := env.client.DoJSON(cleanCtx, http.MethodPut, "/api/v1/admin/carriers/mock_express", adminTok, nil, map[string]any{
				"enabled":   true,
				"api_token": "mock-express-valid-token",
			})
			if err != nil {
				t.Errorf("cleanup restore mock_express carrier failed: %v", err)
				return
			}
			if resp.StatusCode != http.StatusOK {
				t.Errorf("cleanup restore mock_express carrier returned HTTP %d: %s", resp.StatusCode, string(resp.Body))
			}
		})

		// Disable mock_express platform-wide.
		env.mustDoJSON(t, http.MethodPut, "/api/v1/admin/carriers/mock_express", adminTok, nil, map[string]any{
			"enabled": false,
		}, http.StatusOK)

		// Quotes for a shop with default settings must omit mock_express now.
		qResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/shipping/calculate-fee", buyer.AccessToken, nil, CalculateFeeRequest{
			ShopID:         freshSeller.ShopID,
			ToProvinceCode: "79",
			ToWardCode:     "26740",
			WeightGrams:    500,
		}, http.StatusOK)
		var qOut CalculateFeeResponse
		if err := DecodeJSON(qResp, &qOut); err != nil {
			t.Fatalf("decode calculate-fee after admin disable: %v", err)
		}
		if len(qOut.Quotes) == 0 {
			t.Fatalf("expected ghn quotes while mock_express disabled, got 0")
		}
		for _, q := range qOut.Quotes {
			if strings.EqualFold(q.CarrierCode, "mock_express") {
				t.Fatalf("admin disabled mock_express, but quote still returned it: %+v", qOut.Quotes)
			}
		}

		// Attempting to re-enable with an invalid token must fail with 400.
		env.mustDoJSON(t, http.MethodPut, "/api/v1/admin/carriers/mock_express", adminTok, nil, map[string]any{
			"enabled":   true,
			"api_token": "invalid-token",
		}, http.StatusBadRequest)

		// Re-enable with valid token and test connection -> ok=true.
		updResp := env.mustDoJSON(t, http.MethodPut, "/api/v1/admin/carriers/mock_express", adminTok, nil, map[string]any{
			"enabled":   true,
			"api_token": "mock-express-valid-token",
		}, http.StatusOK)
		var updOut shpW2CarrierResponse
		if err := DecodeJSON(updResp, &updOut); err != nil {
			t.Fatalf("decode re-enable mock_express: %v", err)
		}
		if !updOut.Carrier.Enabled || !updOut.Carrier.TokenConfigured {
			t.Fatalf("expected enabled=true & token_configured=true, got %+v", updOut.Carrier)
		}

		testResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/carriers/mock_express/test", adminTok, nil, nil, http.StatusOK)
		var testOut shpW2TestCarrierResponse
		if err := DecodeJSON(testResp, &testOut); err != nil {
			t.Fatalf("decode test carrier: %v", err)
		}
		if !testOut.Ok {
			t.Fatalf("expected AdminTestCarrier ok=true, got %+v", testOut)
		}
	})
}
