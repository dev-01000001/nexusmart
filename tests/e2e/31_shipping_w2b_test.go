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
	"testing"
	"time"

	"github.com/google/uuid"
	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type shpW2BShopSettingsDTO struct {
	ShopID              string   `json:"shop_id"`
	EnabledCarrierCodes []string `json:"enabled_carrier_codes"`
	DefaultWeightGrams  int32    `json:"default_weight_grams"`
	DefaultLengthCM     int32    `json:"default_length_cm"`
	DefaultWidthCM      int32    `json:"default_width_cm"`
	DefaultHeightCM     int32    `json:"default_height_cm"`
	FreeShipEnabled     bool     `json:"free_ship_enabled"`
	FreeShipMinOrderVND int64    `json:"free_ship_min_order_vnd"`
	FreeShipMaxVND      int64    `json:"free_ship_max_vnd"`
}

type shpW2BShopSettingsResponse struct {
	Settings shpW2BShopSettingsDTO `json:"settings"`
}

func shpW2BShippingGRPCAddr() string {
	if v := strings.TrimSpace(os.Getenv("SHIPPING_GRPC_ADDR")); v != "" {
		return v
	}
	return "localhost:50057"
}

func shpW2BServiceJWTSecret() string {
	if v := strings.TrimSpace(os.Getenv("SERVICE_JWT_SECRET")); v != "" {
		return v
	}
	return "dev-only-nexusmart-service-hs256-secret-change-me"
}

func shpW2BMintServiceToken(serviceName string) string {
	enc := base64.RawURLEncoding
	now := time.Now().Unix()
	claims, _ := json.Marshal(map[string]any{
		"sub":     serviceName,
		"role":    "admin",
		"service": serviceName,
		"iss":     "nexusmart",
		"jti":     uuid.NewString(),
		"iat":     now,
		"exp":     now + 60,
	})
	unsigned := enc.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." + enc.EncodeToString(claims)
	mac := hmac.New(sha256.New, []byte(shpW2BServiceJWTSecret()))
	_, _ = mac.Write([]byte(unsigned))
	return unsigned + "." + enc.EncodeToString(mac.Sum(nil))
}

func shpW2BAuthCtx(ctx context.Context, token string) context.Context {
	return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+token))
}

func shpW2BDialShipping(t *testing.T) shippingv1.ShippingServiceClient {
	t.Helper()
	conn, err := grpc.NewClient(shpW2BShippingGRPCAddr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial shipping gRPC (%s): %v", shpW2BShippingGRPCAddr(), err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return shippingv1.NewShippingServiceClient(conn)
}

func shpW2BAddAddress(t *testing.T, env *testEnv, u *userFixture, provCode, provName, wardCode, wardName string) AddressDTO {
	t.Helper()
	resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/users/me/addresses", u.AccessToken, nil, CreateAddressRequest{
		RecipientName:  "Nguyen Van ShipW2B",
		RecipientPhone: u.Phone,
		ProvinceCode:   provCode,
		ProvinceName:   provName,
		WardCode:       wardCode,
		WardName:       wardName,
		StreetAddress:  fmt.Sprintf("88 Le Loi #%d", env.nextSeq()),
		IsDefault:      true,
	}, http.StatusCreated, http.StatusOK)

	var out CreateAddressResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode create address: %v", err)
	}
	u.AddressID = out.Address.ID
	return out.Address
}

func shpW2BCreateSeller(t *testing.T, env *testEnv, label string) *sellerFixture {
	t.Helper()
	u := env.registerVerifyAndLogin(t, label)
	addr := shpW2BAddAddress(t, env, u, "79", "Thành phố Hồ Chí Minh", "79001", "Phường Sài Gòn")

	shopName := fmt.Sprintf("ShipW2BShop-%s-%s-%d", label, env.runID, env.nextSeq())
	shopResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/shops/register", u.AccessToken, nil, RegisterShopRequest{
		Name:               shopName,
		Description:        "E2E wave 2b shipping freeship shop",
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
		Reason:   "E2E w2b shipping approval",
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

func shpW2BQuoteMap(quotes []CarrierQuoteDTO) map[string]int64 {
	out := make(map[string]int64, len(quotes))
	for _, q := range quotes {
		key := strings.ToLower(q.CarrierCode) + ":" + strings.ToUpper(q.ServiceCode)
		out[key] = q.FeeVND
	}
	return out
}

func TestE2E_ShippingW2BFreeshipSettings(t *testing.T) {
	env := getEnv()
	shipClient := shpW2BDialShipping(t)
	buyer := env.registerVerifyAndLogin(t, "shpw2b-buyer")
	orderSvcTok := shpW2BMintServiceToken("order")

	t.Run("GRPCShopFreeshipValidationPersistenceAndOrderAccess", func(t *testing.T) {
		seller := shpW2BCreateSeller(t, env, "shpw2b-grpc")
		otherSeller := shpW2BCreateSeller(t, env, "shpw2b-other")
		ctx := env.ctx(t)
		sellerCtx := shpW2BAuthCtx(ctx, seller.AccessToken)
		otherSellerCtx := shpW2BAuthCtx(ctx, otherSeller.AccessToken)
		buyerCtx := shpW2BAuthCtx(ctx, buyer.AccessToken)
		orderCtx := shpW2BAuthCtx(ctx, orderSvcTok)

		// 1. Unconfigured shop returns default freeship disabled (false, 0, 0) and default weight 500g to order service.
		defResp, err := shipClient.GetShopShippingSettings(orderCtx, &shippingv1.GetShopShippingSettingsRequest{
			ShopId: seller.ShopID,
		})
		if err != nil {
			t.Fatalf("order GetShopShippingSettings default failed: %v", err)
		}
		defSt := defResp.GetSettings()
		if defSt.GetFreeShipEnabled() || defSt.GetFreeShipMinOrderVnd() != 0 || defSt.GetFreeShipMaxVnd() != 0 || defSt.GetDefaultWeightGrams() != 500 {
			t.Fatalf("unexpected default shop shipping settings: %+v", defSt)
		}

		// 2. Invalid freeship values must be rejected with InvalidArgument.
		for _, bad := range []*shippingv1.UpdateShopShippingSettingsRequest{
			{
				EnabledCarrierCodes: []string{"ghn"},
				DefaultWeightGrams:  500,
				FreeShipEnabled:     true,
				FreeShipMinOrderVnd: 200000,
				FreeShipMaxVnd:      0,
			},
			{
				EnabledCarrierCodes: []string{"ghn"},
				DefaultWeightGrams:  500,
				FreeShipEnabled:     true,
				FreeShipMinOrderVnd: -100,
				FreeShipMaxVnd:      25000,
			},
			{
				EnabledCarrierCodes: []string{"ghn"},
				DefaultWeightGrams:  500,
				FreeShipEnabled:     false,
				FreeShipMinOrderVnd: 0,
				FreeShipMaxVnd:      -1,
			},
		} {
			if _, err := shipClient.UpdateShopShippingSettings(sellerCtx, bad); status.Code(err) != codes.InvalidArgument {
				t.Fatalf("expected InvalidArgument for %+v, got %v", bad, err)
			}
		}

		// 3. RBAC: anonymous blocked (Unauthenticated); buyer empty shop_id -> InvalidArgument;
		// buyer, other seller, and service token cannot mutate settings (PermissionDenied).
		if _, err := shipClient.GetShopShippingSettings(ctx, &shippingv1.GetShopShippingSettingsRequest{ShopId: seller.ShopID}); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("expected Unauthenticated for anonymous GetShopShippingSettings, got %v", err)
		}
		if _, err := shipClient.GetShopShippingSettings(buyerCtx, &shippingv1.GetShopShippingSettingsRequest{ShopId: ""}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("expected InvalidArgument for buyer GetShopShippingSettings with empty shop_id, got %v", err)
		}
		if _, err := shipClient.UpdateShopShippingSettings(buyerCtx, &shippingv1.UpdateShopShippingSettingsRequest{
			ShopId: seller.ShopID, EnabledCarrierCodes: []string{"ghn"}, DefaultWeightGrams: 500,
			FreeShipEnabled: true, FreeShipMinOrderVnd: 200000, FreeShipMaxVnd: 30000,
		}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("expected PermissionDenied for buyer UpdateShopShippingSettings, got %v", err)
		}
		if _, err := shipClient.UpdateShopShippingSettings(otherSellerCtx, &shippingv1.UpdateShopShippingSettingsRequest{
			ShopId: seller.ShopID, EnabledCarrierCodes: []string{"ghn"}, DefaultWeightGrams: 500,
			FreeShipEnabled: true, FreeShipMinOrderVnd: 200000, FreeShipMaxVnd: 30000,
		}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("expected PermissionDenied for other seller UpdateShopShippingSettings, got %v", err)
		}
		if _, err := shipClient.UpdateShopShippingSettings(orderCtx, &shippingv1.UpdateShopShippingSettingsRequest{
			ShopId: seller.ShopID, EnabledCarrierCodes: []string{"ghn"}, DefaultWeightGrams: 500,
			FreeShipEnabled: true, FreeShipMinOrderVnd: 200000, FreeShipMaxVnd: 30000,
		}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("expected PermissionDenied for service token UpdateShopShippingSettings, got %v", err)
		}

		// 4. Seller enables shop freeship -> persisted in Postgres:
		// order service token and owner seller receive full fields; buyer and another seller receive ONLY shop_id + free_ship_*.
		updResp, err := shipClient.UpdateShopShippingSettings(sellerCtx, &shippingv1.UpdateShopShippingSettingsRequest{
			EnabledCarrierCodes: []string{"ghn", "mock_express"},
			DefaultWeightGrams:  1200,
			DefaultLengthCm:     25,
			DefaultWidthCm:      20,
			DefaultHeightCm:     12,
			FreeShipEnabled:     true,
			FreeShipMinOrderVnd: 200000,
			FreeShipMaxVnd:      30000,
		})
		if err != nil {
			t.Fatalf("seller UpdateShopShippingSettings failed: %v", err)
		}
		st := updResp.GetSettings()
		if st.GetShopId() != seller.ShopID || !st.GetFreeShipEnabled() || st.GetFreeShipMinOrderVnd() != 200000 || st.GetFreeShipMaxVnd() != 30000 || st.GetDefaultWeightGrams() != 1200 {
			t.Fatalf("unexpected updated settings: %+v", st)
		}

		for name, callCtx := range map[string]context.Context{
			"order-service": orderCtx,
			"seller-owner":  sellerCtx,
		} {
			fullRead, err := shipClient.GetShopShippingSettings(callCtx, &shippingv1.GetShopShippingSettingsRequest{
				ShopId: seller.ShopID,
			})
			if err != nil {
				t.Fatalf("%s GetShopShippingSettings after update failed: %v", name, err)
			}
			fs := fullRead.GetSettings()
			if !fs.GetFreeShipEnabled() || fs.GetFreeShipMinOrderVnd() != 200000 || fs.GetFreeShipMaxVnd() != 30000 ||
				len(fs.GetEnabledCarrierCodes()) != 2 || fs.GetDefaultWeightGrams() != 1200 ||
				fs.GetDefaultLengthCm() != 25 || fs.GetDefaultWidthCm() != 20 || fs.GetDefaultHeightCm() != 12 ||
				fs.GetUpdatedAt() == nil {
				t.Fatalf("%s expected full shipping settings, got: %+v", name, fs)
			}
		}

		for name, callCtx := range map[string]context.Context{
			"buyer":        buyerCtx,
			"other-seller": otherSellerCtx,
		} {
			pubRead, err := shipClient.GetShopShippingSettings(callCtx, &shippingv1.GetShopShippingSettingsRequest{
				ShopId: seller.ShopID,
			})
			if err != nil {
				t.Fatalf("%s GetShopShippingSettings failed: %v", name, err)
			}
			ps := pubRead.GetSettings()
			// Shipping r4 (docs/contracts.md): non-owner JWT receives shop_id, default_weight_grams, and free_ship_* only.
			if ps.GetShopId() != seller.ShopID || !ps.GetFreeShipEnabled() || ps.GetFreeShipMinOrderVnd() != 200000 || ps.GetFreeShipMaxVnd() != 30000 || ps.GetDefaultWeightGrams() != 1200 {
				t.Fatalf("%s expected public fields (freeship=true/200000/30000, default_weight_grams=1200), got: %+v", name, ps)
			}
			if len(ps.GetEnabledCarrierCodes()) != 0 ||
				ps.GetDefaultLengthCm() != 0 || ps.GetDefaultWidthCm() != 0 || ps.GetDefaultHeightCm() != 0 ||
				ps.GetUpdatedAt() != nil {
				t.Fatalf("%s MUST NOT see carriers/dimensions/updated_at, got: %+v", name, ps)
			}
		}

		// 5. Edge case: min_order=0 with positive max_vnd is valid, then disabling works cleanly.
		if _, err := shipClient.UpdateShopShippingSettings(sellerCtx, &shippingv1.UpdateShopShippingSettingsRequest{
			EnabledCarrierCodes: []string{"ghn"},
			DefaultWeightGrams:  1200,
			FreeShipEnabled:     true,
			FreeShipMinOrderVnd: 0,
			FreeShipMaxVnd:      25000,
		}); err != nil {
			t.Fatalf("enable freeship with min_order=0 failed: %v", err)
		}
		if _, err := shipClient.UpdateShopShippingSettings(sellerCtx, &shippingv1.UpdateShopShippingSettingsRequest{
			EnabledCarrierCodes: []string{"ghn"},
			DefaultWeightGrams:  1200,
			FreeShipEnabled:     false,
			FreeShipMinOrderVnd: 0,
			FreeShipMaxVnd:      0,
		}); err != nil {
			t.Fatalf("disable freeship failed: %v", err)
		}
		afterDisable, err := shipClient.GetShopShippingSettings(orderCtx, &shippingv1.GetShopShippingSettingsRequest{
			ShopId: seller.ShopID,
		})
		if err != nil || afterDisable.GetSettings().GetFreeShipEnabled() {
			t.Fatalf("expected freeship disabled after toggle off, got resp=%+v err=%v", afterDisable, err)
		}
	})

	t.Run("CalculateFeeDoesNotDeductShopFreeship", func(t *testing.T) {
		seller := shpW2BCreateSeller(t, env, "shpw2b-calc")
		ctx := env.ctx(t)
		sellerCtx := shpW2BAuthCtx(ctx, seller.AccessToken)

		// Configure baseline settings with default_weight_grams=1600 and freeship OFF.
		if _, err := shipClient.UpdateShopShippingSettings(sellerCtx, &shippingv1.UpdateShopShippingSettingsRequest{
			EnabledCarrierCodes: []string{"ghn", "mock_express"},
			DefaultWeightGrams:  1600,
			DefaultLengthCm:     25,
			DefaultWidthCm:      20,
			DefaultHeightCm:     12,
			FreeShipEnabled:     false,
			FreeShipMinOrderVnd: 0,
			FreeShipMaxVnd:      0,
		}); err != nil {
			t.Fatalf("set baseline shop settings: %v", err)
		}

		// Baseline CalculateFee via gateway (explicit 1000g and weight=0 -> shop default 1600g).
		respBase1 := env.mustDoJSON(t, http.MethodPost, "/api/v1/shipping/calculate-fee", buyer.AccessToken, nil, CalculateFeeRequest{
			ShopID:         seller.ShopID,
			ToProvinceCode: "01",
			ToWardCode:     "01001",
			WeightGrams:    1000,
		}, http.StatusOK)
		var base1Out CalculateFeeResponse
		if err := DecodeJSON(respBase1, &base1Out); err != nil || len(base1Out.Quotes) == 0 {
			t.Fatalf("decode baseline explicit calculate-fee: out=%+v err=%v", base1Out, err)
		}

		respBaseDef := env.mustDoJSON(t, http.MethodPost, "/api/v1/shipping/calculate-fee", buyer.AccessToken, nil, CalculateFeeRequest{
			ShopID:         seller.ShopID,
			ToProvinceCode: "79",
			ToWardCode:     "79001",
			WeightGrams:    0,
		}, http.StatusOK)
		var baseDefOut CalculateFeeResponse
		if err := DecodeJSON(respBaseDef, &baseDefOut); err != nil || len(baseDefOut.Quotes) == 0 {
			t.Fatalf("decode baseline default-weight calculate-fee: out=%+v err=%v", baseDefOut, err)
		}

		// Enable generous shop freeship (min_order=0, max=100,000 VND > every carrier fee).
		if _, err := shipClient.UpdateShopShippingSettings(sellerCtx, &shippingv1.UpdateShopShippingSettingsRequest{
			EnabledCarrierCodes: []string{"ghn", "mock_express"},
			DefaultWeightGrams:  1600,
			DefaultLengthCm:     25,
			DefaultWidthCm:      20,
			DefaultHeightCm:     12,
			FreeShipEnabled:     true,
			FreeShipMinOrderVnd: 0,
			FreeShipMaxVnd:      100000,
		}); err != nil {
			t.Fatalf("enable generous shop freeship: %v", err)
		}

		// Re-quote identical routes plus a fresh uncached weight (1300g): every fee MUST stay > 0 and unchanged.
		respAfter1 := env.mustDoJSON(t, http.MethodPost, "/api/v1/shipping/calculate-fee", buyer.AccessToken, nil, CalculateFeeRequest{
			ShopID:         seller.ShopID,
			ToProvinceCode: "01",
			ToWardCode:     "01001",
			WeightGrams:    1000,
		}, http.StatusOK)
		var after1Out CalculateFeeResponse
		if err := DecodeJSON(respAfter1, &after1Out); err != nil {
			t.Fatalf("decode after explicit calculate-fee: %v", err)
		}
		baseMap1 := shpW2BQuoteMap(base1Out.Quotes)
		afterMap1 := shpW2BQuoteMap(after1Out.Quotes)
		if len(afterMap1) != len(baseMap1) {
			t.Fatalf("quote count changed after enabling freeship: before=%v after=%v", baseMap1, afterMap1)
		}
		for k, beforeFee := range baseMap1 {
			if afterFee := afterMap1[k]; afterFee <= 0 || afterFee != beforeFee {
				t.Fatalf("CalculateFee must NOT deduct shop freeship for %s: before=%d after=%d", k, beforeFee, afterFee)
			}
		}

		respAfterDef := env.mustDoJSON(t, http.MethodPost, "/api/v1/shipping/calculate-fee", buyer.AccessToken, nil, CalculateFeeRequest{
			ShopID:         seller.ShopID,
			ToProvinceCode: "79",
			ToWardCode:     "79001",
			WeightGrams:    0,
		}, http.StatusOK)
		var afterDefOut CalculateFeeResponse
		if err := DecodeJSON(respAfterDef, &afterDefOut); err != nil {
			t.Fatalf("decode after default-weight calculate-fee: %v", err)
		}
		baseMapDef := shpW2BQuoteMap(baseDefOut.Quotes)
		afterMapDef := shpW2BQuoteMap(afterDefOut.Quotes)
		for k, beforeFee := range baseMapDef {
			if afterFee := afterMapDef[k]; afterFee <= 0 || afterFee != beforeFee {
				t.Fatalf("CalculateFee(weight=0) must NOT deduct shop freeship for %s: before=%d after=%d", k, beforeFee, afterFee)
			}
		}

		respUncached := env.mustDoJSON(t, http.MethodPost, "/api/v1/shipping/calculate-fee", buyer.AccessToken, nil, CalculateFeeRequest{
			ShopID:         seller.ShopID,
			ToProvinceCode: "48",
			ToWardCode:     "48001",
			WeightGrams:    1350,
		}, http.StatusOK)
		var uncachedOut CalculateFeeResponse
		if err := DecodeJSON(respUncached, &uncachedOut); err != nil || len(uncachedOut.Quotes) == 0 {
			t.Fatalf("decode uncached calculate-fee: out=%+v err=%v", uncachedOut, err)
		}
		for _, q := range uncachedOut.Quotes {
			if q.FeeVND <= 0 {
				t.Fatalf("CalculateFee uncached route returned non-positive fee with shop freeship enabled: %+v", q)
			}
		}
	})

	t.Run("GatewayRESTSellerFreeshipSettings", func(t *testing.T) {
		seller := shpW2BCreateSeller(t, env, "shpw2b-rest")

		// Invalid freeship (enabled with max=0) must return 400.
		env.mustDoJSON(t, http.MethodPut, "/api/v1/seller/shipping-settings", seller.AccessToken, nil, map[string]any{
			"enabled_carrier_codes":   []string{"ghn"},
			"default_weight_grams":    800,
			"default_length_cm":       20,
			"default_width_cm":        15,
			"default_height_cm":       10,
			"free_ship_enabled":       true,
			"free_ship_min_order_vnd": 250000,
			"free_ship_max_vnd":       0,
		}, http.StatusBadRequest)

		// Valid freeship round-trip via PUT + GET /api/v1/seller/shipping-settings.
		putResp := env.mustDoJSON(t, http.MethodPut, "/api/v1/seller/shipping-settings", seller.AccessToken, nil, map[string]any{
			"enabled_carrier_codes":   []string{"ghn", "mock_express"},
			"default_weight_grams":    950,
			"default_length_cm":       25,
			"default_width_cm":        20,
			"default_height_cm":       12,
			"free_ship_enabled":       true,
			"free_ship_min_order_vnd": 250000,
			"free_ship_max_vnd":       35000,
		}, http.StatusOK)
		var putOut shpW2BShopSettingsResponse
		if err := DecodeJSON(putResp, &putOut); err != nil {
			t.Fatalf("decode PUT shipping-settings: %v", err)
		}
		if !putOut.Settings.FreeShipEnabled || putOut.Settings.FreeShipMinOrderVND != 250000 || putOut.Settings.FreeShipMaxVND != 35000 {
			t.Fatalf("PUT /api/v1/seller/shipping-settings did not return freeship fields: %+v", putOut.Settings)
		}

		getResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/shipping-settings", seller.AccessToken, nil, nil, http.StatusOK)
		var getOut shpW2BShopSettingsResponse
		if err := DecodeJSON(getResp, &getOut); err != nil {
			t.Fatalf("decode GET shipping-settings: %v", err)
		}
		if !getOut.Settings.FreeShipEnabled || getOut.Settings.FreeShipMinOrderVND != 250000 || getOut.Settings.FreeShipMaxVND != 35000 || getOut.Settings.DefaultWeightGrams != 950 {
			t.Fatalf("GET /api/v1/seller/shipping-settings mismatch: %+v", getOut.Settings)
		}
	})
}
