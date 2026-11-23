package e2e

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func shpFixAddAddressInProvince(t *testing.T, env *testEnv, u *userFixture, provinceCode, provinceName, wardCode, wardName string) AddressDTO {
	t.Helper()
	resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/users/me/addresses", u.AccessToken, nil, CreateAddressRequest{
		RecipientName:  "Nguyen Van ShipFix",
		RecipientPhone: u.Phone,
		ProvinceCode:   provinceCode,
		ProvinceName:   provinceName,
		WardCode:       wardCode,
		WardName:       wardName,
		StreetAddress:  fmt.Sprintf("118 Nguyen Van Huong #%d", env.nextSeq()),
		IsDefault:      true,
	}, http.StatusCreated, http.StatusOK)

	var out CreateAddressResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode create address: %v", err)
	}
	if out.Address.ID == "" {
		t.Fatalf("create address returned empty id: %s", string(resp.Body))
	}
	u.AddressID = out.Address.ID
	return out.Address
}

func shpFixCreateApprovedSellerInProvince(t *testing.T, env *testEnv, label, provinceCode, provinceName, wardCode, wardName string) *sellerFixture {
	t.Helper()
	u := env.registerVerifyAndLogin(t, label)
	addr := shpFixAddAddressInProvince(t, env, u, provinceCode, provinceName, wardCode, wardName)

	shopName := fmt.Sprintf("ShipFixShop-%s-%s-%d", label, env.runID, env.nextSeq())
	shopResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/shops/register", u.AccessToken, nil, RegisterShopRequest{
		Name:               shopName,
		Description:        "E2E shipping fix verification shop",
		PickupAddressID:    addr.ID,
		ContactPhone:       u.Phone,
		BusinessLicenseDoc: "https://sandbox.nexusmart.local/docs/license.pdf",
	}, http.StatusCreated, http.StatusOK)

	var shopOut ShopResponse
	if err := DecodeJSON(shopResp, &shopOut); err != nil {
		t.Fatalf("decode register shop: %v", err)
	}

	adminTok := env.adminLogin(t)
	reviewPath := fmt.Sprintf("/api/v1/admin/shops/%s/review", url.PathEscape(shopOut.Shop.ID))
	env.mustDoJSON(t, http.MethodPost, reviewPath, adminTok, nil, ReviewRequest{
		Decision: "APPROVE",
		Reason:   "E2E shipping fix approval",
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

func TestE2E_ShippingFeeShopPickupValidationAndWebhookCarrier(t *testing.T) {
	env := getEnv()
	ctx := env.ctx(t)

	// Use TP.HCM (79 / 79001, South region) for both shop pickup and buyer address so that
	// omitting from_province_code would fall back to central region (+16,000 VND) if broken.
	cat := env.ensureCategory(t)
	seller := shpFixCreateApprovedSellerInProvince(t, env, "shpfix-seller", "79", "Thành phố Hồ Chí Minh", "79001", "Phường Sài Gòn")
	_, sku := env.createApprovedProduct(t, seller, cat.ID, "ShipFix Cotton Tee", 200000, 20, 250)

	buyer := env.registerVerifyAndLogin(t, "shpfix-buyer")
	buyerAddr := shpFixAddAddressInProvince(t, env, buyer, "79", "Thành phố Hồ Chí Minh", "79001", "Phường Sài Gòn")

	// 1. R7 (SHP-B1): Quote with shop_id only (no from_province_code / from_ward_code) must match checkout shipping_fee_vnd.
	quoteResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/shipping/calculate-fee", buyer.AccessToken, nil, CalculateFeeRequest{
		ShopID:           seller.ShopID,
		FromProvinceCode: "",
		FromWardCode:     "",
		ToProvinceCode:   "79",
		ToWardCode:       "79001",
		WeightGrams:      250,
		CODAmountVND:     200000,
	}, http.StatusOK)

	var quoteOut CalculateFeeResponse
	if err := DecodeJSON(quoteResp, &quoteOut); err != nil {
		t.Fatalf("decode calculate-fee: %v", err)
	}
	var chosen CarrierQuoteDTO
	for _, q := range quoteOut.Quotes {
		if strings.EqualFold(q.CarrierCode, "ghn") && strings.EqualFold(q.ServiceCode, "STANDARD") {
			chosen = q
			break
		}
	}
	if chosen.CarrierCode == "" {
		t.Fatalf("expected ghn/STANDARD quote, got %+v", quoteOut.Quotes)
	}

	// Checkout with the exact same shop, address, carrier, and service.
	checkoutResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
		"Idempotency-Key": "shpfix-fee-" + uuid.NewString(),
	}, CheckoutRequest{
		AddressID:     buyerAddr.ID,
		FromCart:      false,
		Items:         []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 1}},
		PaymentMethod: "COD",
		ShippingChoices: []ShopShippingChoiceDTO{
			{ShopID: seller.ShopID, CarrierCode: chosen.CarrierCode, ServiceCode: chosen.ServiceCode},
		},
	}, http.StatusCreated, http.StatusOK)

	var checkout CheckoutResponse
	if err := DecodeJSON(checkoutResp, &checkout); err != nil {
		t.Fatalf("decode checkout: %v", err)
	}
	if len(checkout.Orders) != 1 {
		t.Fatalf("expected 1 order, got %d", len(checkout.Orders))
	}
	order := checkout.Orders[0]
	if chosen.FeeVND != order.ShippingFeeVND {
		t.Fatalf("SHP-B1 mismatch: quote(shop_id only)=%d VND != checkout order.shipping_fee_vnd=%d VND",
			chosen.FeeVND, order.ShippingFeeVND)
	}
	t.Logf("SHP-B1 verified: quote=%d VND == order.shipping_fee_vnd=%d VND (%s/%s)",
		chosen.FeeVND, order.ShippingFeeVND, chosen.CarrierCode, chosen.ServiceCode)

	// Missing both shop_id and from_province_code -> 400.
	env.mustDoJSON(t, http.MethodPost, "/api/v1/shipping/calculate-fee", buyer.AccessToken, nil, CalculateFeeRequest{
		ToProvinceCode: "79",
		ToWardCode:     "79001",
		WeightGrams:    250,
	}, http.StatusBadRequest)

	// 2. SHP-B7: Reject weight_grams <= 0 and unknown/mismatched province/ward codes.
	for _, bad := range []struct {
		name string
		req  CalculateFeeRequest
	}{
		{
			name: "zero weight",
			req: CalculateFeeRequest{
				ShopID: seller.ShopID, FromProvinceCode: "79", FromWardCode: "79001",
				ToProvinceCode: "79", ToWardCode: "79001", WeightGrams: 0,
			},
		},
		{
			name: "negative weight",
			req: CalculateFeeRequest{
				ShopID: seller.ShopID, FromProvinceCode: "79", FromWardCode: "79001",
				ToProvinceCode: "79", ToWardCode: "79001", WeightGrams: -50,
			},
		},
		{
			name: "unknown province 99999",
			req: CalculateFeeRequest{
				ShopID: seller.ShopID, FromProvinceCode: "79", FromWardCode: "79001",
				ToProvinceCode: "99999", ToWardCode: "99999", WeightGrams: 250,
			},
		},
		{
			name: "ward from another province",
			req: CalculateFeeRequest{
				ShopID: seller.ShopID, FromProvinceCode: "79", FromWardCode: "79001",
				ToProvinceCode: "79", ToWardCode: "01001", WeightGrams: 250,
			},
		},
	} {
		t.Run("Reject_"+bad.name, func(t *testing.T) {
			env.mustDoJSON(t, http.MethodPost, "/api/v1/shipping/calculate-fee", buyer.AccessToken, nil, bad.req, http.StatusBadRequest)
		})
	}

	// 3. SHP-B6: Confirm & ship order via ghn, then verify webhook on wrong carrier path is rejected with 4xx.
	confirmPath := fmt.Sprintf("/api/v1/seller/orders/%s/confirm", url.PathEscape(order.ID))
	env.mustDoJSON(t, http.MethodPost, confirmPath, seller.AccessToken, nil, nil, http.StatusOK)

	shipPath := fmt.Sprintf("/api/v1/seller/orders/%s/ship", url.PathEscape(order.ID))
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

	now := time.Now().UTC()
	mismatchEventID := "e2e-shp-b6-" + uuid.NewString()
	wrongResp, err := env.client.PostSignedWebhook(ctx, "/webhooks/shipping/fake_carrier", ShippingWebhookPayload{
		EventID:      mismatchEventID,
		TrackingCode: shipOut.TrackingCode,
		OrderID:      order.ID,
		RawStatus:    "delivering",
		Description:  "Spoofed carrier test",
		OccurredAt:   now.Format(time.RFC3339),
	}, env.cfg.ShippingWebhookSecret, now, "")
	if err != nil {
		t.Fatalf("post mismatched webhook failed: %v", err)
	}
	if wrongResp.StatusCode < 400 || wrongResp.StatusCode >= 500 {
		t.Fatalf("SHP-B6: expected 4xx for mismatched carrier webhook, got HTTP %d body=%s",
			wrongResp.StatusCode, string(wrongResp.Body))
	}

	// Verify shipment status did NOT change after rejected webhook.
	getShipPath := fmt.Sprintf("/api/v1/shipping/shipments/%s", url.PathEscape(order.ID))
	shResp := env.mustDoJSON(t, http.MethodGet, getShipPath, seller.AccessToken, nil, nil, http.StatusOK)
	var shBody struct {
		Shipment struct {
			Status      string `json:"status"`
			CarrierCode string `json:"carrier_code"`
		} `json:"shipment"`
	}
	if err := DecodeJSON(shResp, &shBody); err != nil {
		t.Fatalf("decode shipment: %v", err)
	}
	if !StatusEquals(shBody.Shipment.Status, "CREATED") {
		t.Fatalf("SHP-B6: shipment status changed after mismatched webhook: got %q, want CREATED", shBody.Shipment.Status)
	}

	// Valid webhook on matching carrier (/webhooks/shipping/ghn) must succeed.
	okResp, err := env.client.PostSignedWebhook(ctx, "/webhooks/shipping/ghn", ShippingWebhookPayload{
		EventID:      mismatchEventID,
		TrackingCode: shipOut.TrackingCode,
		OrderID:      order.ID,
		RawStatus:    "delivering",
		Description:  "GHN đang giao hàng",
		OccurredAt:   now.Add(time.Second).Format(time.RFC3339),
	}, env.cfg.ShippingWebhookSecret, time.Now().UTC(), "")
	if err != nil {
		t.Fatalf("post valid webhook failed: %v", err)
	}
	if okResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on matching carrier webhook, got HTTP %d body=%s", okResp.StatusCode, string(okResp.Body))
	}
}
