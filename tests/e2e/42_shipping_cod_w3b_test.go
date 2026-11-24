package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func w3b42RawHTTP(
	ctx context.Context,
	t *testing.T,
	method string,
	fullURL string,
	token string,
	contentType string,
	body []byte,
) (int, http.Header, []byte) {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, fullURL, bodyReader)
	if err != nil {
		t.Fatalf("build raw request %s %s: %v", method, fullURL, err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	hc := &http.Client{Timeout: 30 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatalf("execute raw request %s %s: %v", method, fullURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read raw response %s %s: %v", method, fullURL, err)
	}
	return resp.StatusCode, resp.Header.Clone(), raw
}

func w3b42RawPostJSON(t *testing.T, env *testEnv, path, token string, payload any) (int, http.Header, []byte) {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return w3b42RawHTTP(env.ctx(t), t, http.MethodPost, env.cfg.GatewayURL+path, token, "application/json", b)
}

func w3b42AssertRawErrorCode(t *testing.T, label string, body []byte, wantCode string) {
	t.Helper()
	var e w3b41ErrorEnvelope
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("%s: decode error envelope: %v (body=%s)", label, err, string(body))
	}
	if e.Error.Code != wantCode {
		t.Fatalf("%s: error.code = %q, want %q (body=%s)", label, e.Error.Code, wantCode, string(body))
	}
}

func w3b42InjectCarrierFault(t *testing.T, env *testEnv, carrierCode, orderID string, statusCode, count int) {
	t.Helper()
	if strings.TrimSpace(orderID) == "" {
		t.Fatalf("w3b42InjectCarrierFault requires a specific non-empty order_id (never global empty string)")
	}
	postFault := func(ctx context.Context, cnt int) (int, []byte, error) {
		payload, _ := json.Marshal(map[string]any{
			"carrier_code": carrierCode,
			"order_id":     orderID,
			"status_code":  statusCode,
			"count":        cnt,
		})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, env.cfg.ShippingHTTPURL+"/internal/test/carrier-fault", bytes.NewReader(payload))
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		hc := &http.Client{Timeout: 10 * time.Second}
		resp, err := hc.Do(req)
		if err != nil {
			return 0, nil, err
		}
		defer func() { _ = resp.Body.Close() }()
		b, err := io.ReadAll(resp.Body)
		return resp.StatusCode, b, err
	}

	code, body, err := postFault(env.ctx(t), count)
	if err != nil {
		t.Fatalf("POST /internal/test/carrier-fault failed: %v", err)
	}
	if code != http.StatusOK {
		t.Fatalf("POST /internal/test/carrier-fault returned HTTP %d (body=%s)", code, string(body))
	}
	if count > 0 {
		t.Cleanup(func() {
			cleanCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, _, _ = postFault(cleanCtx, 0)
		})
	}
}

func w3b42EstimatedRetryBackoff(cfg Config) time.Duration {
	maxAttempts := cfg.ShippingCreateRetryMax
	if maxAttempts <= 0 {
		maxAttempts = 8
	}
	base := time.Duration(cfg.ShippingCreateRetryBaseMS) * time.Millisecond
	if base <= 0 {
		base = 2 * time.Second
	}
	maxD := time.Duration(cfg.ShippingCreateRetryMaxBackoffMS) * time.Millisecond
	if maxD < base {
		maxD = base
	}
	interval := time.Duration(cfg.ShippingCreateWorkerIntervalMS) * time.Millisecond
	if interval <= 0 {
		interval = 2 * time.Second
	}
	var total time.Duration
	delay := base
	for i := 0; i < maxAttempts; i++ {
		total += delay + interval
		if delay >= maxD/2 {
			delay = maxD
		} else {
			delay *= 2
		}
	}
	return total
}

// w3b42RequireFaultInjectionTestConfig validates that when SHIPPING_TEST_FAULT_INJECTION=true,
// all carrier resilience and async retry tuning env vars are explicitly available and compatible
// with E18 fault-injection tests. Fails fast with t.Fatalf (never t.Skip) on missing/incompatible config.
// Note: per services/shipping/internal/carrier/resilience.go (`for attempt := 0; attempt < r.cfg.MaxAttempts; attempt++`),
// CARRIER_MAX_RETRIES=1 means 1 total attempt per CreateShipment call (0 inner retries).
func w3b42RequireFaultInjectionTestConfig(t *testing.T, cfg Config) (retryMax, carrierMaxRetries int, estBackoff time.Duration) {
	t.Helper()

	var missing []string
	for _, key := range []string{
		"SHIPPING_CREATE_RETRY_MAX",
		"SHIPPING_CREATE_RETRY_BASE_MS",
		"SHIPPING_CREATE_RETRY_MAX_BACKOFF_MS",
		"SHIPPING_CREATE_WORKER_INTERVAL_MS",
		"CARRIER_MAX_RETRIES",
		"CARRIER_CB_MAX_FAILURES",
		"CARRIER_CB_OPEN_TIMEOUT",
	} {
		raw := strings.TrimSpace(os.Getenv(key))
		if raw == "" {
			missing = append(missing, key)
			continue
		}
		if key == "CARRIER_CB_OPEN_TIMEOUT" {
			if d, err := time.ParseDuration(raw); err != nil || d <= 0 {
				t.Fatalf("E18 test config invalid: %s=%q must be a positive Go duration (e.g. 30s)", key, raw)
			}
		} else {
			if n, err := strconv.Atoi(raw); err != nil || n <= 0 {
				t.Fatalf("E18 test config invalid: %s=%q must be a positive integer", key, raw)
			}
		}
	}
	if len(missing) > 0 {
		t.Fatalf("E18 fault-injection test requires env vars %v in e2e container (set SHIPPING_CREATE_RETRY_MAX=4, SHIPPING_CREATE_RETRY_BASE_MS=300, SHIPPING_CREATE_RETRY_MAX_BACKOFF_MS=2000, SHIPPING_CREATE_WORKER_INTERVAL_MS=300, CARRIER_MAX_RETRIES=1, CARRIER_CB_MAX_FAILURES=100, CARRIER_CB_OPEN_TIMEOUT=30s)", missing)
	}

	retryMax = cfg.ShippingCreateRetryMax
	carrierMaxRetries = cfg.CarrierMaxRetries
	estBackoff = w3b42EstimatedRetryBackoff(cfg)

	if estBackoff > 60*time.Second {
		t.Fatalf("E18 fault-injection test requires fast retry settings (computed total backoff %s > 60s): set SHIPPING_CREATE_RETRY_MAX=4, SHIPPING_CREATE_RETRY_BASE_MS=300, SHIPPING_CREATE_RETRY_MAX_BACKOFF_MS=2000, SHIPPING_CREATE_WORKER_INTERVAL_MS=300", estBackoff)
	}

	// Worst-case consecutive 503 failures on mock_express across cancel-queued + exhaustion subtests without an intervening success.
	worstCaseConsecutiveFailures := (retryMax + 5) * carrierMaxRetries
	if cfg.CarrierCBMaxFailures <= worstCaseConsecutiveFailures && cfg.CarrierCBOpenTimeout >= estBackoff {
		t.Fatalf("E18 fault-injection test detected non-test carrier resilience config: CARRIER_MAX_RETRIES=%d CARRIER_CB_MAX_FAILURES=%d CARRIER_CB_OPEN_TIMEOUT=%s will trip the circuit breaker during retry window (%s); set CARRIER_MAX_RETRIES=1 and CARRIER_CB_MAX_FAILURES=100 in isolated test .env",
			cfg.CarrierMaxRetries, cfg.CarrierCBMaxFailures, cfg.CarrierCBOpenTimeout, estBackoff)
	}

	return retryMax, carrierMaxRetries, estBackoff
}

func w3b42FindAccountBalance(summary LedgerSummaryResponse, accountCode string) int64 {
	for _, a := range summary.Accounts {
		if a.AccountCode == accountCode {
			return a.BalanceVND
		}
	}
	return 0
}

func w3b42PostShippingWebhook(t *testing.T, env *testEnv, carrierCode, orderID, trackingCode, rawStatus, desc string) {
	t.Helper()
	resp, err := env.client.PostSignedWebhook(env.ctx(t), "/webhooks/shipping/"+url.PathEscape(carrierCode), ShippingWebhookPayload{
		EventID:      "w3b42-wh-" + uuid.NewString(),
		TrackingCode: trackingCode,
		OrderID:      orderID,
		RawStatus:    rawStatus,
		Description:  desc,
		OccurredAt:   time.Now().UTC().Format(time.RFC3339),
	}, env.cfg.ShippingWebhookSecret, time.Now().UTC(), "")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("post shipping webhook (%s/%s): err=%v resp=%+v", carrierCode, rawStatus, err, resp)
	}
}

func w3b42PlaceCODOrder(t *testing.T, env *testEnv, buyer *userFixture, addrID string, seller *sellerFixture, sku SKUDTO, carrierCode, serviceCode string) CheckoutResponse {
	t.Helper()
	coResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
		"Idempotency-Key": "w3b42-cod-" + uuid.NewString(),
	}, CheckoutRequest{
		AddressID:     addrID,
		FromCart:      false,
		Items:         []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 1}},
		PaymentMethod: "COD",
		ShippingChoices: []ShopShippingChoiceDTO{
			{ShopID: seller.ShopID, CarrierCode: carrierCode, ServiceCode: serviceCode},
		},
	}, http.StatusCreated, http.StatusOK)
	var co CheckoutResponse
	if err := DecodeJSON(coResp, &co); err != nil || len(co.Orders) != 1 {
		t.Fatalf("decode COD checkout: err=%v body=%s", err, string(coResp.Body))
	}
	return co
}

// TestE2E_W3B_VNAddressCatalogAndLegacyMapping verifies E15 and E16:
// - 34 provinces, 3,321 active wards across Vietnam (QD 19/2025).
// - cod_supported=false strictly for Hoang Sa (48/20333) and Truong Sa (56/22736), true for all other 3,319 wards.
// - Legacy mapping Binh Duong / Thu Dau Mot / Phu Cuong -> 79 / 25747 usable for buyer address creation.
func TestE2E_W3B_VNAddressCatalogAndLegacyMapping(t *testing.T) {
	env := getEnv()

	provResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/shipping/provinces", "", nil, nil, http.StatusOK)
	var provOut ProvincesResponse
	if err := DecodeJSON(provResp, &provOut); err != nil {
		t.Fatalf("decode provinces: %v", err)
	}
	if len(provOut.Provinces) != 34 {
		t.Fatalf("E15: expected exactly 34 provinces, got %d", len(provOut.Provinces))
	}

	totalWards := 0
	codDisabled := make(map[string]string)
	for _, p := range provOut.Provinces {
		wResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/shipping/provinces/"+url.PathEscape(p.Code)+"/wards", "", nil, nil, http.StatusOK)
		var wOut WardsResponse
		if err := DecodeJSON(wResp, &wOut); err != nil {
			t.Fatalf("decode wards for province %s: %v", p.Code, err)
		}
		totalWards += len(wOut.Wards)
		for _, w := range wOut.Wards {
			if !w.CODSupported {
				codDisabled[w.Code] = w.ProvinceCode
			}
		}
	}
	if totalWards != 3321 {
		t.Fatalf("E15: expected exactly 3,321 active wards across 34 provinces, got %d", totalWards)
	}
	if len(codDisabled) != 2 || codDisabled["20333"] != "48" || codDisabled["22736"] != "56" {
		t.Fatalf("E16: expected cod_supported=false exclusively for Hoang Sa (48/20333) and Truong Sa (56/22736), got %v", codDisabled)
	}

	// Legacy address mapping -> create buyer address.
	mapResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/shipping/addresses/map-legacy", "", nil, map[string]any{
		"legacy_province": "Bình Dương",
		"legacy_district": "Thủ Dầu Một",
		"legacy_ward":     "Phú Cường",
	}, http.StatusOK)
	var mapOut struct {
		Found        bool   `json:"found"`
		ProvinceCode string `json:"province_code"`
		ProvinceName string `json:"province_name"`
		WardCode     string `json:"ward_code"`
		WardName     string `json:"ward_name"`
	}
	if err := DecodeJSON(mapResp, &mapOut); err != nil {
		t.Fatalf("decode map-legacy: %v", err)
	}
	if !mapOut.Found || mapOut.ProvinceCode != "79" || mapOut.WardCode != "25747" {
		t.Fatalf("E15 map-legacy mismatch: got %+v, want found=true province=79 ward=25747", mapOut)
	}

	buyer := env.registerVerifyAndLogin(t, "w3b42-legacy-buyer")
	env.mustDoJSON(t, http.MethodPost, "/api/v1/users/me/addresses", buyer.AccessToken, nil, CreateAddressRequest{
		RecipientName:  "Nguyen Legacy",
		RecipientPhone: buyer.Phone,
		ProvinceCode:   mapOut.ProvinceCode,
		ProvinceName:   mapOut.ProvinceName,
		WardCode:       mapOut.WardCode,
		WardName:       mapOut.WardName,
		StreetAddress:  "12 Yersin",
		IsDefault:      true,
	}, http.StatusCreated, http.StatusOK)
}

// TestE2E_W3B_PublicCalculateFeeAndPerUserRateLimit verifies E17, E17-bis, and contract-r0 gRPC RBAC:
//   - Direct gRPC CalculateFee succeeds anonymously and rejects service tokens; CreateShipment rejects service tokens.
//   - Anonymous REST calculate-fee with omitted to_ward_code returns is_estimated=true, cod_supported, fallback_weight_item_qty,
//     no warehouse address leak, consistent cached quotes, and cod_supported=false for Hoang Sa.
//   - Authenticated buyers on the same IP have independent per-user 30/min calculate-fee buckets.
func TestE2E_W3B_PublicCalculateFeeAndPerUserRateLimit(t *testing.T) {
	env := getEnv()
	cat, seller1, _ := env.ensureBaseMarketplace(t)
	_, sku0g := env.createApprovedProduct(t, seller1, cat.ID, "W3B ZeroWeight PDP Item", 180000, 20, 0)

	// 1. Direct gRPC RBAC for ShippingService.CalculateFee & CreateShipment.
	shipClient := shpW2BDialShipping(t)
	anonCtx := env.ctx(t)
	svcTok := promoMintServiceToken("order-service")
	svcCtx := metadata.NewOutgoingContext(env.ctx(t), metadata.Pairs("authorization", "Bearer "+svcTok))

	gFeeResp, err := shipClient.CalculateFee(anonCtx, &shippingv1.CalculateFeeRequest{
		ShopId:                seller1.ShopID,
		ToProvinceCode:        "01",
		FallbackWeightItemQty: 1,
	})
	if err != nil || len(gFeeResp.GetQuotes()) == 0 || !gFeeResp.GetIsEstimated() {
		t.Fatalf("E17 anonymous gRPC CalculateFee failed: resp=%+v err=%v", gFeeResp, err)
	}
	if _, err := shipClient.CalculateFee(svcCtx, &shippingv1.CalculateFeeRequest{
		ShopId:         seller1.ShopID,
		ToProvinceCode: "01",
		WeightGrams:    500,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service-token gRPC CalculateFee want PermissionDenied, got %v", err)
	}
	if _, err := shipClient.CreateShipment(anonCtx, &shippingv1.CreateShipmentRequest{
		OrderId: uuid.NewString(), ShopId: seller1.ShopID,
	}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anonymous gRPC CreateShipment want Unauthenticated, got %v", err)
	}
	if _, err := shipClient.CreateShipment(svcCtx, &shippingv1.CreateShipmentRequest{
		OrderId: uuid.NewString(), ShopId: seller1.ShopID,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service-token gRPC CreateShipment want PermissionDenied, got %v", err)
	}

	// 2. Anonymous REST POST /api/v1/shipping/calculate-fee with empty to_ward_code + 0g SKU items[].
	anonReq := map[string]any{
		"shop_id":          seller1.ShopID,
		"to_province_code": "01",
		"to_ward_code":     "",
		"payment_method":   "VIETQR",
		"items":            []map[string]any{{"sku_id": sku0g.ID, "quantity": 2}},
	}
	r1 := env.mustDoJSON(t, http.MethodPost, "/api/v1/shipping/calculate-fee", "", nil, anonReq, http.StatusOK)
	var q1 CalculateFeeResponse
	if err := DecodeJSON(r1, &q1); err != nil || !q1.IsEstimated || len(q1.Quotes) == 0 {
		t.Fatalf("E17 anonymous estimated quote failed: err=%v body=%s", err, string(r1.Body))
	}
	if strings.Contains(string(r1.Body), "from_ward_code") || strings.Contains(string(r1.Body), "pickup_address") {
		t.Fatalf("E17 anonymous quote leaked warehouse info: %s", string(r1.Body))
	}
	for _, q := range q1.Quotes {
		if q.FeeVND <= 0 || !q.CODSupported {
			t.Fatalf("E17 mainland quote want fee>0 cod_supported=true, got %+v", q)
		}
	}
	// Cached repeat call matches fees.
	r2 := env.mustDoJSON(t, http.MethodPost, "/api/v1/shipping/calculate-fee", "", nil, anonReq, http.StatusOK)
	var q2 CalculateFeeResponse
	if err := DecodeJSON(r2, &q2); err != nil || len(q2.Quotes) != len(q1.Quotes) || q2.Quotes[0].FeeVND != q1.Quotes[0].FeeVND {
		t.Fatalf("E17 cached quote mismatch: q1=%+v q2=%+v err=%v", q1, q2, err)
	}

	// Island ward Hoang Sa (48/20333) -> is_estimated=false, cod_supported=false.
	rIsland := env.mustDoJSON(t, http.MethodPost, "/api/v1/shipping/calculate-fee", "", nil, map[string]any{
		"shop_id":          seller1.ShopID,
		"to_province_code": "48",
		"to_ward_code":     "20333",
		"weight_grams":     500,
	}, http.StatusOK)
	var qIsland CalculateFeeResponse
	if err := DecodeJSON(rIsland, &qIsland); err != nil || qIsland.IsEstimated || len(qIsland.Quotes) == 0 {
		t.Fatalf("E16/E17 Hoang Sa quote failed: err=%v body=%s", err, string(rIsland.Body))
	}
	for _, q := range qIsland.Quotes {
		if q.CODSupported {
			t.Fatalf("E16: Hoang Sa quote must return cod_supported=false, got %+v", q)
		}
	}

	// 3. E17-bis: Authenticated buyers sharing container IP have independent 30/min calculate-fee buckets.
	buyerRL1 := env.registerVerifyAndLogin(t, "w3b42-rl-buyer1")
	buyerRL2 := env.registerVerifyAndLogin(t, "w3b42-rl-buyer2")
	rlBody := map[string]any{
		"shop_id":          seller1.ShopID,
		"to_province_code": "79",
		"to_ward_code":     "26740",
		"weight_grams":     500,
	}
	hit429 := false
	for i := 1; i <= 45; i++ {
		code, hdr, raw := w3b42RawPostJSON(t, env, "/api/v1/shipping/calculate-fee", buyerRL1.AccessToken, rlBody)
		if code == http.StatusTooManyRequests {
			// Distinguish route-specific 30/min limit from general IP limit.
			if lim, _ := strconv.Atoi(hdr.Get("X-RateLimit-Limit")); lim > 60 {
				retrySec, _ := strconv.Atoi(hdr.Get("Retry-After"))
				if retrySec < 1 {
					retrySec = 1
				}
				time.Sleep(time.Duration(retrySec) * time.Second)
				continue
			}
			w3b42AssertRawErrorCode(t, "buyerRL1 calculate-fee 429", raw, "RATE_LIMITED")
			if hdr.Get("Retry-After") == "" {
				t.Fatalf("calculate-fee 429 missing Retry-After header")
			}
			hit429 = true
			break
		}
		if code != http.StatusOK {
			t.Fatalf("buyerRL1 calculate-fee call #%d returned HTTP %d: %s", i, code, string(raw))
		}
	}
	if !hit429 {
		t.Fatalf("E17-bis: buyerRL1 did not hit 429 within 45 calculate-fee calls")
	}
	// buyerRL2 on the exact same IP MUST still get 200.
	code2, _, raw2 := w3b42RawPostJSON(t, env, "/api/v1/shipping/calculate-fee", buyerRL2.AccessToken, rlBody)
	if code2 != http.StatusOK {
		t.Fatalf("E17-bis: buyerRL2 on same IP should NOT be blocked by buyerRL1 bucket, got HTTP %d: %s", code2, string(raw2))
	}
}

// TestE2E_W3B_PhoneOTPVerificationAndSecurity verifies E19 phone OTP flows, Mailpit SMS mock privacy,
// notification preferences bypass, duplicate phone normalization (+84), phone change invalidation,
// gateway 5/min rate limit, service 60s cooldown, and 5-wrong-attempt lockout.
func TestE2E_W3B_PhoneOTPVerificationAndSecurity(t *testing.T) {
	env := getEnv()
	cat, seller1, _ := env.ensureBaseMarketplace(t)
	_, sku := env.createApprovedProduct(t, seller1, cat.ID, "W3B PhoneOTP Item", 120000, 20, 300)

	// 1. Direct gRPC RBAC for RequestPhoneOTP & VerifyPhoneOTP + REST anonymous 401.
	userClient := usrW3ADialUserGRPC(t)
	anonCtx := env.ctx(t)
	svcTok := promoMintServiceToken("order-service")
	svcCtx := metadata.NewOutgoingContext(env.ctx(t), metadata.Pairs("authorization", "Bearer "+svcTok))

	if _, err := userClient.RequestPhoneOTP(anonCtx, &userv1.RequestPhoneOTPRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anonymous gRPC RequestPhoneOTP want Unauthenticated, got %v", err)
	}
	if _, err := userClient.RequestPhoneOTP(svcCtx, &userv1.RequestPhoneOTPRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service-token gRPC RequestPhoneOTP want PermissionDenied, got %v", err)
	}
	if _, err := userClient.VerifyPhoneOTP(anonCtx, &userv1.VerifyPhoneOTPRequest{Otp: "123456"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anonymous gRPC VerifyPhoneOTP want Unauthenticated, got %v", err)
	}
	if _, err := userClient.VerifyPhoneOTP(svcCtx, &userv1.VerifyPhoneOTPRequest{Otp: "123456"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service-token gRPC VerifyPhoneOTP want PermissionDenied, got %v", err)
	}
	env.mustDoJSON(t, http.MethodPost, "/api/v1/users/me/phone/otp", "", nil, map[string]any{}, http.StatusUnauthorized)
	env.mustDoJSON(t, http.MethodPost, "/api/v1/users/me/phone/verify", "", nil, map[string]any{"otp": "123456"}, http.StatusUnauthorized)

	// 2. Happy path + preferences bypass (system_email=false) + Mailpit subject has NO OTP + 0 in-app notifications.
	buyer1 := env.registerVerifyAndLogin(t, "w3b42-otp-b1")
	addr1 := env.addAddress(t, buyer1)

	env.mustDoJSON(t, http.MethodPut, "/api/v1/notifications/preferences", buyer1.AccessToken, nil, map[string]any{
		"order_email":  false,
		"promo_email":  false,
		"system_email": false,
	}, http.StatusOK)

	notifBeforeResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/notifications?page=1&page_size=20", buyer1.AccessToken, nil, nil, http.StatusOK)
	var notifBefore NotificationsListResponse
	if err := DecodeJSON(notifBeforeResp, &notifBefore); err != nil {
		t.Fatalf("decode notifications before phone OTP: %v", err)
	}
	seen1, err := env.mailpit.SnapshotRecipientMessageIDs(env.ctx(t), buyer1.Email)
	if err != nil {
		t.Fatalf("snapshot mailpit: %v", err)
	}

	reqResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/users/me/phone/otp", buyer1.AccessToken, nil, map[string]any{}, http.StatusOK)
	var reqOut struct {
		Sent              bool   `json:"sent"`
		ExpiresInSeconds  int32  `json:"expires_in_seconds"`
		RetryAfterSeconds int32  `json:"retry_after_seconds"`
		PhoneMasked       string `json:"phone_masked"`
	}
	if err := DecodeJSON(reqResp, &reqOut); err != nil {
		t.Fatalf("decode request phone otp: %v", err)
	}
	if !reqOut.Sent || reqOut.ExpiresInSeconds != 300 || reqOut.RetryAfterSeconds != 60 || !strings.Contains(reqOut.PhoneMasked, "*") {
		t.Fatalf("unexpected request phone otp response: %+v", reqOut)
	}

	msg1, otp1, err := env.mailpit.WaitForNewOTPMessage(env.ctx(t), buyer1.Email, seen1, env.cfg.PollTimeout)
	if err != nil {
		t.Fatalf("wait phone OTP email for buyer1: %v", err)
	}
	if notifW2BSixDigitsRe.MatchString(msg1.Subject) {
		t.Fatalf("E19: phone OTP email Subject MUST NOT contain 6-digit OTP, got %q", msg1.Subject)
	}
	if !strings.Contains(msg1.Text, otp1) || !strings.Contains(msg1.Text, reqOut.PhoneMasked) {
		t.Fatalf("E19: phone OTP email body missing OTP or phone_masked (%q): %q", reqOut.PhoneMasked, msg1.Text)
	}

	// Verify 0 new in-app notifications created for phone OTP.
	notifAfterResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/notifications?page=1&page_size=20", buyer1.AccessToken, nil, nil, http.StatusOK)
	var notifAfter NotificationsListResponse
	if err := DecodeJSON(notifAfterResp, &notifAfter); err != nil {
		t.Fatalf("decode notifications after phone OTP: %v", err)
	}
	if notifAfter.Total != notifBefore.Total {
		t.Fatalf("E19: phone OTP must NOT create in-app notification: before=%d after=%d", notifBefore.Total, notifAfter.Total)
	}

	verResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/users/me/phone/verify", buyer1.AccessToken, nil, map[string]any{
		"otp": otp1,
	}, http.StatusOK)
	var verOut VerifyOTPResponse
	if err := DecodeJSON(verResp, &verOut); err != nil || !verOut.Verified || !verOut.User.PhoneVerified {
		t.Fatalf("verify phone OTP failed: err=%v body=%s", err, string(verResp.Body))
	}
	// Replay same OTP -> 400.
	env.mustDoJSON(t, http.MethodPost, "/api/v1/users/me/phone/verify", buyer1.AccessToken, nil, map[string]any{
		"otp": otp1,
	}, http.StatusBadRequest)

	// 3. E19-bis: Unverified accounts do NOT occupy a phone number, VN phones normalize to 0xxxxxxxxx,
	//    only verified phones enforce uniqueness (+84 duplicate rejected with 409), and updating to an
	//    equivalent phone format (+84 vs 0) preserves phone_verified=true.
	registerWithPhone := func(label, rawPhone string) *userFixture {
		t.Helper()
		email := env.uniqueEmail(label)
		password := "NexusPass123!"
		env.mustDoJSON(t, http.MethodPost, "/api/v1/auth/register", "", nil, RegisterRequest{
			FullName: "E2E " + label,
			Email:    email,
			Phone:    rawPhone,
			Password: password,
		}, http.StatusCreated, http.StatusOK)

		emailOTP, otpErr := env.mailpit.WaitForOTP(env.ctx(t), email, env.cfg.PollTimeout)
		if otpErr != nil {
			t.Fatalf("wait email OTP for %s: %v", label, otpErr)
		}
		env.mustDoJSON(t, http.MethodPost, "/api/v1/auth/verify-otp", "", nil, VerifyOTPRequest{
			Email: email,
			OTP:   emailOTP,
		}, http.StatusOK)

		lResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/auth/login", "", nil, LoginRequest{
			Email:    email,
			Password: password,
		}, http.StatusOK)
		var lOut LoginResponse
		if decErr := DecodeJSON(lResp, &lOut); decErr != nil || lOut.AccessToken == "" {
			t.Fatalf("login %s failed: err=%v body=%s", label, decErr, string(lResp.Body))
		}
		return &userFixture{
			UserID:        lOut.User.ID,
			Email:         email,
			Phone:         lOut.User.Phone,
			Password:      password,
			AccessToken:   lOut.AccessToken,
			RefreshToken:  lOut.RefreshToken,
			PhoneVerified: lOut.User.PhoneVerified,
			User:          lOut.User,
		}
	}

	sharedLocal := env.uniquePhone() // 09xxxxxxxx
	sharedPlus84 := "+84" + strings.TrimPrefix(sharedLocal, "0")

	// Account A registers with +84 format -> stored canonically as 0xxxxxxxxx, unverified.
	userA := registerWithPhone("w3b42-e19bis-a", sharedPlus84)
	if userA.User.Phone != sharedLocal || userA.User.PhoneVerified {
		t.Fatalf("E19-bis: expected canonical phone %q with phone_verified=false, got phone=%q verified=%v",
			sharedLocal, userA.User.Phone, userA.User.PhoneVerified)
	}

	// Account B registers with the SAME unverified phone -> MUST succeed (unverified does not occupy number).
	userB := registerWithPhone("w3b42-e19bis-b", sharedLocal)
	if userB.User.Phone != sharedLocal || userB.User.PhoneVerified {
		t.Fatalf("E19-bis: second registration with unverified phone expected %q/false, got phone=%q verified=%v",
			sharedLocal, userB.User.Phone, userB.User.PhoneVerified)
	}

	// Both A and B request OTP while phone is still unverified.
	seenA, err := env.mailpit.SnapshotRecipientMessageIDs(env.ctx(t), userA.Email)
	if err != nil {
		t.Fatalf("snapshot mailpit userA: %v", err)
	}
	seenB, err := env.mailpit.SnapshotRecipientMessageIDs(env.ctx(t), userB.Email)
	if err != nil {
		t.Fatalf("snapshot mailpit userB: %v", err)
	}
	env.mustDoJSON(t, http.MethodPost, "/api/v1/users/me/phone/otp", userA.AccessToken, nil, map[string]any{}, http.StatusOK)
	env.mustDoJSON(t, http.MethodPost, "/api/v1/users/me/phone/otp", userB.AccessToken, nil, map[string]any{"phone": sharedPlus84}, http.StatusOK)

	_, otpA, err := env.mailpit.WaitForNewOTPMessage(env.ctx(t), userA.Email, seenA, env.cfg.PollTimeout)
	if err != nil {
		t.Fatalf("wait OTP userA: %v", err)
	}
	_, otpB, err := env.mailpit.WaitForNewOTPMessage(env.ctx(t), userB.Email, seenB, env.cfg.PollTimeout)
	if err != nil {
		t.Fatalf("wait OTP userB: %v", err)
	}

	// Account A verifies first -> 200 verified=true, stored canonically as 0xxxxxxxxx.
	verAResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/users/me/phone/verify", userA.AccessToken, nil, map[string]any{
		"otp": otpA,
	}, http.StatusOK)
	var verAOut VerifyOTPResponse
	if err := DecodeJSON(verAResp, &verAOut); err != nil || !verAOut.Verified || !verAOut.User.PhoneVerified || verAOut.User.Phone != sharedLocal {
		t.Fatalf("E19-bis: userA verify failed: err=%v body=%s", err, string(verAResp.Body))
	}

	// E19-bis: Account A updating profile phone to equivalent +84 format keeps phone=0xxxxxxxxx AND phone_verified=true.
	equivResp := env.mustDoJSON(t, http.MethodPatch, "/api/v1/users/me", userA.AccessToken, nil, map[string]any{
		"phone": sharedPlus84,
	}, http.StatusOK)
	var equivOut struct {
		User UserDTO `json:"user"`
	}
	if err := DecodeJSON(equivResp, &equivOut); err != nil || equivOut.User.Phone != sharedLocal || !equivOut.User.PhoneVerified {
		t.Fatalf("E19-bis: equivalent phone format update must keep phone=%q and phone_verified=true, got err=%v body=%s",
			sharedLocal, err, string(equivResp.Body))
	}

	// Account B verifying the same phone (sending +84 format) after A verified -> 409 ALREADY_EXISTS.
	dupVerifyResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/users/me/phone/verify", userB.AccessToken, nil, map[string]any{
		"phone": sharedPlus84,
		"otp":   otpB,
	}, http.StatusConflict)
	w3b41AssertErrorCode(t, "E19-bis B verify already-verified +84 phone", dupVerifyResp, "ALREADY_EXISTS")

	// Fresh Account C requesting OTP for A's already-verified phone (+84 format) -> 409 ALREADY_EXISTS.
	userC := env.registerVerifyAndLogin(t, "w3b42-e19bis-c")
	dupReqResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/users/me/phone/otp", userC.AccessToken, nil, map[string]any{
		"phone": sharedPlus84,
	}, http.StatusConflict)
	w3b41AssertErrorCode(t, "E19-bis C request OTP for verified +84 phone", dupReqResp, "ALREADY_EXISTS")

	// 4. Changing verified phone via PATCH /users/me resets phone_verified=false and blocks COD immediately.
	newPhoneB := env.uniquePhone()
	env.mustDoJSON(t, http.MethodPatch, "/api/v1/users/me", buyer1.AccessToken, nil, map[string]any{
		"phone": newPhoneB,
	}, http.StatusOK)
	meResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/users/me", buyer1.AccessToken, nil, nil, http.StatusOK)
	var meOut struct {
		User UserDTO `json:"user"`
	}
	if err := DecodeJSON(meResp, &meOut); err != nil || meOut.User.PhoneVerified {
		t.Fatalf("E19: phone change must reset phone_verified=false: err=%v body=%s", err, string(meResp.Body))
	}
	quote1 := env.calculateShippingQuote(t, buyer1.AccessToken, seller1.ShopID, 300, 120000)
	codAfterChange := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer1.AccessToken, map[string]string{
		"Idempotency-Key": "w3b42-cod-reset-" + uuid.NewString(),
	}, CheckoutRequest{
		AddressID:     addr1.ID,
		Items:         []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 1}},
		PaymentMethod: "COD",
		ShippingChoices: []ShopShippingChoiceDTO{
			{ShopID: seller1.ShopID, CarrierCode: quote1.CarrierCode, ServiceCode: quote1.ServiceCode},
		},
	}, http.StatusConflict)
	w3b41AssertErrorCode(t, "COD after phone change reset", codAfterChange, "COD_PHONE_NOT_VERIFIED")

	// 5. Phone change mid-flight invalidates pending OTP for old phone.
	buyer3 := env.registerVerifyAndLogin(t, "w3b42-otp-b3")
	seen3, err := env.mailpit.SnapshotRecipientMessageIDs(env.ctx(t), buyer3.Email)
	if err != nil {
		t.Fatalf("snapshot mailpit buyer3: %v", err)
	}
	env.mustDoJSON(t, http.MethodPost, "/api/v1/users/me/phone/otp", buyer3.AccessToken, nil, map[string]any{}, http.StatusOK)
	_, otp3, err := env.mailpit.WaitForNewOTPMessage(env.ctx(t), buyer3.Email, seen3, env.cfg.PollTimeout)
	if err != nil {
		t.Fatalf("wait OTP buyer3: %v", err)
	}
	phone3New := env.uniquePhone()
	env.mustDoJSON(t, http.MethodPatch, "/api/v1/users/me", buyer3.AccessToken, nil, map[string]any{
		"phone": phone3New,
	}, http.StatusOK)
	env.mustDoJSON(t, http.MethodPost, "/api/v1/users/me/phone/verify", buyer3.AccessToken, nil, map[string]any{
		"phone": buyer3.Phone, "otp": otp3,
	}, http.StatusBadRequest)
	env.mustDoJSON(t, http.MethodPost, "/api/v1/users/me/phone/verify", buyer3.AccessToken, nil, map[string]any{
		"phone": phone3New, "otp": otp3,
	}, http.StatusBadRequest)

	// 6. Gateway RATE_LIMIT_PHONE_OTP_PER_MIN (5/min) returns 429 RATE_LIMITED.
	buyer4 := env.registerVerifyAndLogin(t, "w3b42-otp-rl4")
	codeInit, _, rawInit := w3b42RawPostJSON(t, env, "/api/v1/users/me/phone/otp", buyer4.AccessToken, map[string]any{})
	if codeInit != http.StatusOK {
		t.Fatalf("buyer4 initial phone/otp want 200, got %d: %s", codeInit, string(rawInit))
	}
	hitOTP429 := false
	for i := 0; i < 6; i++ {
		code, hdr, raw := w3b42RawPostJSON(t, env, "/api/v1/users/me/phone/verify", buyer4.AccessToken, map[string]any{"otp": "000000"})
		if code == http.StatusTooManyRequests {
			w3b42AssertRawErrorCode(t, "phone OTP gateway 429", raw, "RATE_LIMITED")
			if hdr.Get("Retry-After") == "" {
				t.Fatalf("phone OTP 429 missing Retry-After header")
			}
			hitOTP429 = true
			break
		}
	}
	if !hitOTP429 {
		t.Fatalf("expected gateway 429 RATE_LIMITED on phone OTP endpoint after 5/min")
	}

	// 7. Direct gRPC: 60s cooldown (ResourceExhausted) + 5 wrong guesses invalidate OTP so 6th right OTP fails.
	buyer5 := env.registerVerifyAndLogin(t, "w3b42-otp-bf5")
	b5Ctx := metadata.NewOutgoingContext(env.ctx(t), metadata.Pairs("authorization", "Bearer "+buyer5.AccessToken))
	seen5, err := env.mailpit.SnapshotRecipientMessageIDs(env.ctx(t), buyer5.Email)
	if err != nil {
		t.Fatalf("snapshot mailpit buyer5: %v", err)
	}
	if _, err := userClient.RequestPhoneOTP(b5Ctx, &userv1.RequestPhoneOTPRequest{}); err != nil {
		t.Fatalf("buyer5 gRPC RequestPhoneOTP #1: %v", err)
	}
	if _, err := userClient.RequestPhoneOTP(b5Ctx, &userv1.RequestPhoneOTPRequest{}); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("buyer5 gRPC RequestPhoneOTP #2 within 60s cooldown want ResourceExhausted, got %v", err)
	}
	_, otp5, err := env.mailpit.WaitForNewOTPMessage(env.ctx(t), buyer5.Email, seen5, env.cfg.PollTimeout)
	if err != nil {
		t.Fatalf("wait OTP buyer5: %v", err)
	}
	wrongOTP := "000000"
	if otp5 == wrongOTP {
		wrongOTP = "999999"
	}
	var firstWrongMsg string
	for attempt := 1; attempt <= 5; attempt++ {
		_, err := userClient.VerifyPhoneOTP(b5Ctx, &userv1.VerifyPhoneOTPRequest{Otp: wrongOTP})
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("wrong OTP attempt %d want InvalidArgument, got %v", attempt, err)
		}
		if attempt == 1 {
			firstWrongMsg = status.Convert(err).Message()
		} else if status.Convert(err).Message() != firstWrongMsg {
			t.Fatalf("wrong OTP attempt %d error message changed (%q vs %q)", attempt, status.Convert(err).Message(), firstWrongMsg)
		}
	}
	// 6th attempt with the REAL OTP must fail because 5 wrong guesses invalidated it.
	if _, err := userClient.VerifyPhoneOTP(b5Ctx, &userv1.VerifyPhoneOTPRequest{Otp: otp5}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("6th attempt with real OTP after 5 failures want InvalidArgument, got %v", err)
	}
}

// TestE2E_W3B_CODEligibilityGatesAndRejectionRate verifies E19 COD checkout gates:
//  1. Unverified phone -> 409 COD_PHONE_NOT_VERIFIED (stock & voucher untouched), VietQR succeeds,
//     after verifying phone retrying SAME Idempotency-Key succeeds (D11 0-order retry).
//  2. Island ward Hoang Sa (cod_supported=false) -> 2-shop COD 409 COD_AREA_NOT_SUPPORTED (0 stock reserved), VietQR succeeds.
//  3. COD rejection rate (min 3 terminal orders, >= 30%): 1/2 allowed, CANCELLED excluded, 1/3 blocked
//     409 COD_REJECTED_LIMIT_EXCEEDED, 1/4 allowed, post-delivery return does NOT count toward rejection rate.
func TestE2E_W3B_CODEligibilityGatesAndRejectionRate(t *testing.T) {
	env := getEnv()
	cat, seller1, seller2 := env.ensureBaseMarketplace(t)

	t.Run("UnverifiedPhoneBlocksCOD_ThenSameIdemKeySucceedsAfterVerify", func(t *testing.T) {
		buyer := env.registerVerifyAndLogin(t, "w3b42-cod-unver")
		addr := env.addAddress(t, buyer)
		prod, sku := env.createApprovedProduct(t, seller1, cat.ID, "W3B COD Phone Gate", 150000, 20, 400)
		voucher := env.createShopVoucher(t, seller1, 10000, 100000, 5)
		env.saveVoucher(t, buyer.AccessToken, voucher)
		quote := env.calculateShippingQuote(t, buyer.AccessToken, seller1.ShopID, 400, 150000)

		stockBefore := env.getProductSKU(t, prod.ID, sku.ID).AvailableStock
		voucherBefore := env.getVoucherRemainingQuota(t, buyer.AccessToken, "SHOP", voucher.ID)

		idemKey := "w3b42-unver-cod-" + uuid.NewString()
		codReq := CheckoutRequest{
			AddressID:      addr.ID,
			Items:          []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 1}},
			ShopVoucherIDs: map[string]string{seller1.ShopID: voucher.ID},
			PaymentMethod:  "COD",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
			},
		}
		blockResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": idemKey,
		}, codReq, http.StatusConflict)
		w3b41AssertErrorCode(t, "unverified phone COD", blockResp, "COD_PHONE_NOT_VERIFIED")

		if got := env.getProductSKU(t, prod.ID, sku.ID).AvailableStock; got != stockBefore {
			t.Fatalf("blocked COD altered stock: got %d want %d", got, stockBefore)
		}
		if got := env.getVoucherRemainingQuota(t, buyer.AccessToken, "SHOP", voucher.ID); got != voucherBefore {
			t.Fatalf("blocked COD altered voucher quota: got %d want %d", got, voucherBefore)
		}

		// VietQR succeeds even without phone verification.
		vqrReq := codReq
		vqrReq.ShopVoucherIDs = nil
		vqrReq.PaymentMethod = "VIETQR"
		env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "w3b42-unver-vqr-" + uuid.NewString(),
		}, vqrReq, http.StatusCreated, http.StatusOK)

		// Verify phone -> retry SAME Idempotency-Key succeeds (D11 0-order retry).
		env.verifyBuyerPhone(t, buyer)
		retryResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": idemKey,
		}, codReq, http.StatusCreated, http.StatusOK)
		var co CheckoutResponse
		if err := DecodeJSON(retryResp, &co); err != nil || len(co.Orders) != 1 {
			t.Fatalf("COD retry after phone verification failed: err=%v body=%s", err, string(retryResp.Body))
		}
	})

	t.Run("IslandWardBlocksMultiShopCOD_AllowsVietQR", func(t *testing.T) {
		buyer := env.registerVerifyAndLogin(t, "w3b42-cod-island")
		env.verifyBuyerPhone(t, buyer)

		addrResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/users/me/addresses", buyer.AccessToken, nil, CreateAddressRequest{
			RecipientName:  "Nguyen Hoang Sa",
			RecipientPhone: buyer.Phone,
			ProvinceCode:   "48",
			ProvinceName:   "Thành phố Đà Nẵng",
			WardCode:       "20333",
			WardName:       "Đặc khu Hoàng Sa",
			StreetAddress:  "Đảo Hoàng Sa",
			IsDefault:      true,
		}, http.StatusCreated, http.StatusOK)
		var addrOut CreateAddressResponse
		if err := DecodeJSON(addrResp, &addrOut); err != nil {
			t.Fatalf("decode island address: %v", err)
		}

		prod1, sku1 := env.createApprovedProduct(t, seller1, cat.ID, "W3B Island S1", 130000, 15, 300)
		prod2, sku2 := env.createApprovedProduct(t, seller2, cat.ID, "W3B Island S2", 140000, 15, 300)
		s1Before := env.getProductSKU(t, prod1.ID, sku1.ID).AvailableStock
		s2Before := env.getProductSKU(t, prod2.ID, sku2.ID).AvailableStock

		codBlock := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "w3b42-island-cod-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID: addrOut.Address.ID,
			Items: []CheckoutItemInputDTO{
				{SKUID: sku1.ID, Quantity: 1},
				{SKUID: sku2.ID, Quantity: 1},
			},
			PaymentMethod: "COD",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: "ghn", ServiceCode: "STANDARD"},
				{ShopID: seller2.ShopID, CarrierCode: "ghn", ServiceCode: "STANDARD"},
			},
		}, http.StatusConflict)
		w3b41AssertErrorCode(t, "Hoang Sa multi-shop COD", codBlock, "COD_AREA_NOT_SUPPORTED")

		if env.getProductSKU(t, prod1.ID, sku1.ID).AvailableStock != s1Before || env.getProductSKU(t, prod2.ID, sku2.ID).AvailableStock != s2Before {
			t.Fatalf("E19: blocked island COD reserved stock on shop1 or shop2")
		}

		// VietQR to Hoang Sa succeeds.
		env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "w3b42-island-vqr-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID:     addrOut.Address.ID,
			Items:         []CheckoutItemInputDTO{{SKUID: sku1.ID, Quantity: 1}},
			PaymentMethod: "VIETQR",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: "ghn", ServiceCode: "STANDARD"},
			},
		}, http.StatusCreated, http.StatusOK)
	})

	t.Run("CODRejectionRateFormulaAndPostDeliveryReturnExclusion", func(t *testing.T) {
		buyer := env.registerVerifyAndLogin(t, "w3b42-cod-rate")
		env.verifyBuyerPhone(t, buyer)
		addr := env.addAddress(t, buyer)
		_, sku := env.createApprovedProduct(t, seller1, cat.ID, "W3B COD Rate Item", 110000, 40, 300)
		quote := env.calculateShippingQuote(t, buyer.AccessToken, seller1.ShopID, 300, 110000)

		shipOrder := func(ordID string) (string, string) {
			t.Helper()
			env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/seller/orders/%s/confirm", url.PathEscape(ordID)), seller1.AccessToken, nil, nil, http.StatusOK)
			sResp := env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/seller/orders/%s/ship", url.PathEscape(ordID)), seller1.AccessToken, nil, SellerShipRequest{
				CarrierCode: quote.CarrierCode,
				ServiceCode: quote.ServiceCode,
			}, http.StatusOK)
			var sOut SellerShipResponse
			if err := DecodeJSON(sResp, &sOut); err != nil || sOut.TrackingCode == "" {
				t.Fatalf("ship order %s: err=%v body=%s", ordID, err, string(sResp.Body))
			}
			cc := strings.ToLower(strings.TrimSpace(sOut.Order.CarrierCode))
			if cc == "" {
				cc = strings.ToLower(strings.TrimSpace(quote.CarrierCode))
			}
			return cc, sOut.TrackingCode
		}
		completeOrder := func(ordID string) {
			t.Helper()
			cc, trk := shipOrder(ordID)
			w3b42PostShippingWebhook(t, env, cc, ordID, trk, "delivered", "Giao thành công")
			env.pollUntil(t, env.cfg.PollTimeout, 250*time.Millisecond, "order DELIVERED", func() (bool, string) {
				o := env.getOrder(t, buyer.AccessToken, ordID)
				return StatusEquals(o.Status, "DELIVERED"), o.Status
			})
			env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/orders/%s/confirm-received", url.PathEscape(ordID)), buyer.AccessToken, nil, nil, http.StatusOK)
		}

		// Place 4 COD orders up front while buyer has 0 rejections.
		oRej := w3b42PlaceCODOrder(t, env, buyer, addr.ID, seller1, sku, quote.CarrierCode, quote.ServiceCode).Orders[0]
		oComp1 := w3b42PlaceCODOrder(t, env, buyer, addr.ID, seller1, sku, quote.CarrierCode, quote.ServiceCode).Orders[0]
		oComp2 := w3b42PlaceCODOrder(t, env, buyer, addr.ID, seller1, sku, quote.CarrierCode, quote.ServiceCode).Orders[0]
		oComp3 := w3b42PlaceCODOrder(t, env, buyer, addr.ID, seller1, sku, quote.CarrierCode, quote.ServiceCode).Orders[0]

		// Order 1: failed delivery -> RETURNED (rejected=1, terminal=1).
		ccRej, trkRej := shipOrder(oRej.ID)
		w3b42PostShippingWebhook(t, env, ccRej, oRej.ID, trkRej, "delivering", "Đang giao")
		env.pollUntil(t, env.cfg.PollTimeout, 250*time.Millisecond, "oRej SHIPPING", func() (bool, string) {
			o := env.getOrder(t, buyer.AccessToken, oRej.ID)
			return StatusEquals(o.Status, "SHIPPING"), o.Status
		})
		w3b42PostShippingWebhook(t, env, ccRej, oRej.ID, trkRej, "returned", "Khách từ chối nhận COD")
		env.pollUntil(t, env.cfg.PollTimeout, 250*time.Millisecond, "oRej RETURNED", func() (bool, string) {
			o := env.getOrder(t, buyer.AccessToken, oRej.ID)
			return StatusEquals(o.Status, "RETURNED"), o.Status
		})

		// Order 2: COMPLETED -> 1/2 (< min_orders 3).
		completeOrder(oComp1.ID)

		// At 1/2: COD MUST still be allowed; place an extra order and CANCEL it (CANCELLED must not enter denominator).
		oCanc := w3b42PlaceCODOrder(t, env, buyer, addr.ID, seller1, sku, quote.CarrierCode, quote.ServiceCode).Orders[0]
		env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/orders/%s/cancel", url.PathEscape(oCanc.ID)), buyer.AccessToken, nil, map[string]any{
			"reason": "Hủy để kiểm tra mẫu số",
		}, http.StatusOK)

		// Order 3: COMPLETED -> 1/3 = 33.33% >= 30% -> COD MUST BE BLOCKED (409 COD_REJECTED_LIMIT_EXCEEDED).
		completeOrder(oComp2.ID)
		blockedResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "w3b42-rate-block-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID:     addr.ID,
			Items:         []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 1}},
			PaymentMethod: "COD",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
			},
		}, http.StatusConflict)
		w3b41AssertErrorCode(t, "COD blocked at 1/3 rejection rate", blockedResp, "COD_REJECTED_LIMIT_EXCEEDED")

		// Order 4: COMPLETED -> 1/4 = 25% < 30% -> COD ALLOWED again!
		completeOrder(oComp3.ID)
		oPostRet := w3b42PlaceCODOrder(t, env, buyer, addr.ID, seller1, sku, quote.CarrierCode, quote.ServiceCode).Orders[0]

		// Deliver oPostRet and do a POST-DELIVERY buyer return (ACCEPT_REFUND -> RETURNED).
		// This MUST NOT count as a failed COD delivery!
		ccPR, trkPR := shipOrder(oPostRet.ID)
		w3b42PostShippingWebhook(t, env, ccPR, oPostRet.ID, trkPR, "delivered", "Giao thành công")
		env.pollUntil(t, env.cfg.PollTimeout, 250*time.Millisecond, "oPostRet DELIVERED", func() (bool, string) {
			o := env.getOrder(t, buyer.AccessToken, oPostRet.ID)
			return StatusEquals(o.Status, "DELIVERED"), o.Status
		})
		retResp := env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/orders/%s/return", url.PathEscape(oPostRet.ID)), buyer.AccessToken, nil, map[string]any{
			"reason": "Đổi trả sau khi đã nhận hàng",
		}, http.StatusOK)
		var retOut struct {
			ReturnRequestID string `json:"return_request_id"`
		}
		if err := DecodeJSON(retResp, &retOut); err != nil || retOut.ReturnRequestID == "" {
			t.Fatalf("decode return request: err=%v body=%s", err, string(retResp.Body))
		}
		env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/seller/returns/%s/respond", url.PathEscape(retOut.ReturnRequestID)), seller1.AccessToken, nil, map[string]any{
			"decision": "ACCEPT_REFUND",
			"note":     "Hoàn tiền",
		}, http.StatusOK)
		env.pollUntil(t, env.cfg.PollTimeout, 250*time.Millisecond, "oPostRet RETURNED", func() (bool, string) {
			o := env.getOrder(t, buyer.AccessToken, oPostRet.ID)
			return StatusEquals(o.Status, "RETURNED"), o.Status
		})

		// Rate is still 1/4 = 25% (or 1/5 = 20%), NOT 2/5 = 40% -> COD MUST STILL BE ALLOWED!
		_ = w3b42PlaceCODOrder(t, env, buyer, addr.ID, seller1, sku, quote.CarrierCode, quote.ServiceCode)
	})
}

// TestE2E_W3B_AsyncShipmentCreationFaultInjection verifies E18 via POST /internal/test/carrier-fault
// on mock_express (specific order_id only, cleaned up in t.Cleanup):
//   - Skips ONLY when SHIPPING_TEST_FAULT_INJECTION is false.
//   - When SHIPPING_TEST_FAULT_INJECTION=true, strictly validates test env vars (t.Fatal on missing/incompatible config).
//   - (1) 400 non-retryable fails immediately with 0 job queued.
//   - (2) 503 transient ((MAX-1)*CARRIER_MAX_RETRIES faults) queues job (QUEUED) and recovers to PREPARING with 1 shipment.
//   - (3) 503 queued + buyer/seller cancel -> CANCELLED, clear fault, wait > 1 worker interval -> 0 shipments created & stays CANCELLED.
//   - (4) 503 exhaustion ((MAX+1)*CARRIER_MAX_RETRIES+1 faults) -> shipment_job_status=FAILED + seller notification,
//     then clearing fault and retrying /ship succeeds.
func TestE2E_W3B_AsyncShipmentCreationFaultInjection(t *testing.T) {
	env := getEnv()
	if !env.cfg.ShippingTestFaultInjection {
		t.Skip("SHIPPING_TEST_FAULT_INJECTION is not enabled in this environment")
	}

	retryMax, carrierMaxRetries, estBackoff := w3b42RequireFaultInjectionTestConfig(t, env.cfg)

	cat := env.ensureCategory(t)
	seller := env.createApprovedSeller(t, "w3b42-e18-seller")
	// Ensure mock_express is enabled for this seller.
	env.mustDoJSON(t, http.MethodPut, "/api/v1/seller/shipping-settings", seller.AccessToken, nil, map[string]any{
		"enabled_carrier_codes": []string{"ghn", "mock_express"},
		"default_weight_grams":  500,
	}, http.StatusOK)
	_, sku := env.createApprovedProduct(t, seller, cat.ID, "W3B E18 Async Ship Item", 160000, 40, 400)

	buyer := env.registerVerifyAndLogin(t, "w3b42-e18-buyer")
	env.verifyBuyerPhone(t, buyer)
	addr := env.addAddress(t, buyer)

	// COD orders start in CONFIRMED immediately after checkout; do NOT call /confirm here
	// (which transitions CONFIRMED -> PREPARING), because SellerShipOrder accepts CONFIRMED directly
	// and E18 must verify CONFIRMED -> QUEUED (stays CONFIRMED) -> PREPARING.
	newConfirmedOrder := func() OrderDTO {
		t.Helper()
		co := w3b42PlaceCODOrder(t, env, buyer, addr.ID, seller, sku, "mock_express", "STANDARD")
		ord := co.Orders[0]
		if !StatusEquals(ord.Status, "CONFIRMED") {
			t.Fatalf("expected fresh COD order in CONFIRMED before /ship, got %q", ord.Status)
		}
		return ord
	}

	pollBudget := estBackoff + 15*time.Second
	if pollBudget < 20*time.Second {
		pollBudget = 20 * time.Second
	}
	exhaustFaultCount := (retryMax+1)*carrierMaxRetries + 1

	t.Run("1_NonRetryable400_FailsImmediatelyNoJob", func(t *testing.T) {
		ord := newConfirmedOrder()
		w3b42InjectCarrierFault(t, env, "mock_express", ord.ID, http.StatusBadRequest, 1)

		shipPath := fmt.Sprintf("/api/v1/seller/orders/%s/ship", url.PathEscape(ord.ID))
		resp, err := env.client.DoJSON(env.ctx(t), http.MethodPost, shipPath, seller.AccessToken, nil, SellerShipRequest{
			CarrierCode: "mock_express",
			ServiceCode: "STANDARD",
		})
		if err != nil {
			t.Fatalf("POST /ship 400 test error: %v", err)
		}
		if resp.StatusCode == http.StatusOK {
			t.Fatalf("E18: carrier 400 must NOT succeed or queue job, got HTTP 200 body=%s", string(resp.Body))
		}
		got := env.getOrder(t, seller.AccessToken, ord.ID)
		if !StatusEquals(got.Status, "CONFIRMED") || got.ShipmentJobStatus != "" || got.TrackingCode != "" {
			t.Fatalf("E18: carrier 400 must leave order CONFIRMED with empty shipment_job_status, got status=%s job=%q trk=%q",
				got.Status, got.ShipmentJobStatus, got.TrackingCode)
		}
	})

	t.Run("2_Transient503_QueuesAndRecoversToPreparing", func(t *testing.T) {
		ord := newConfirmedOrder()
		outerFails := retryMax - 1
		if outerFails < 1 {
			outerFails = 1
		}
		// Each outer CreateShipment call consumes CARRIER_MAX_RETRIES inner attempts (1 when CARRIER_MAX_RETRIES=1).
		failCount := outerFails * carrierMaxRetries
		w3b42InjectCarrierFault(t, env, "mock_express", ord.ID, http.StatusServiceUnavailable, failCount)

		shipPath := fmt.Sprintf("/api/v1/seller/orders/%s/ship", url.PathEscape(ord.ID))
		shipResp := env.mustDoJSON(t, http.MethodPost, shipPath, seller.AccessToken, nil, SellerShipRequest{
			CarrierCode: "mock_express",
			ServiceCode: "STANDARD",
		}, http.StatusOK)
		var shipOut SellerShipResponse
		if err := DecodeJSON(shipResp, &shipOut); err != nil {
			t.Fatalf("decode queued ship response: %v", err)
		}
		if !shipOut.Queued || shipOut.ShipmentJobID == "" || !StatusEquals(shipOut.Order.Status, "CONFIRMED") || shipOut.Order.ShipmentJobStatus != "QUEUED" {
			t.Fatalf("E18: expected queued=true job_id!=empty status=CONFIRMED shipment_job_status=QUEUED, got %+v", shipOut)
		}

		// Poll until async retry worker recovers and order transitions to PREPARING.
		var prepared OrderDTO
		env.pollUntil(t, pollBudget, 300*time.Millisecond, "queued shipment recovers to PREPARING", func() (bool, string) {
			prepared = env.getOrder(t, seller.AccessToken, ord.ID)
			return StatusEquals(prepared.Status, "PREPARING") && prepared.TrackingCode != "" && prepared.LabelURL != "" && prepared.ShipmentJobStatus == "",
				fmt.Sprintf("status=%s job=%q trk=%q", prepared.Status, prepared.ShipmentJobStatus, prepared.TrackingCode)
		})

		// Clicking /ship again must be idempotent (same tracking_code, queued=false).
		againResp := env.mustDoJSON(t, http.MethodPost, shipPath, seller.AccessToken, nil, SellerShipRequest{
			CarrierCode: "mock_express",
			ServiceCode: "STANDARD",
		}, http.StatusOK)
		var againOut SellerShipResponse
		if err := DecodeJSON(againResp, &againOut); err != nil || againOut.Queued || againOut.TrackingCode != prepared.TrackingCode {
			t.Fatalf("E18: repeat /ship must return existing tracking %q with queued=false, got err=%v out=%+v",
				prepared.TrackingCode, err, againOut)
		}
	})

	t.Run("3_Transient503_CancelWhileQueued_NoShipmentCreatedAndStaysCancelled", func(t *testing.T) {
		cancelCases := []struct {
			name  string
			token string
		}{
			{"BuyerCancelsQueuedOrder", buyer.AccessToken},
			{"SellerCancelsQueuedOrder", seller.AccessToken},
		}
		for _, tc := range cancelCases {
			t.Run(tc.name, func(t *testing.T) {
				ord := newConfirmedOrder()
				w3b42InjectCarrierFault(t, env, "mock_express", ord.ID, http.StatusServiceUnavailable, exhaustFaultCount)

				shipPath := fmt.Sprintf("/api/v1/seller/orders/%s/ship", url.PathEscape(ord.ID))
				shipResp := env.mustDoJSON(t, http.MethodPost, shipPath, seller.AccessToken, nil, SellerShipRequest{
					CarrierCode: "mock_express",
					ServiceCode: "STANDARD",
				}, http.StatusOK)
				var shipOut SellerShipResponse
				if err := DecodeJSON(shipResp, &shipOut); err != nil || !shipOut.Queued || shipOut.ShipmentJobID == "" || shipOut.Order.ShipmentJobStatus != "QUEUED" {
					t.Fatalf("expected queued=true with shipment_job_status=QUEUED, got err=%v out=%+v", err, shipOut)
				}

				cancelResp := env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/orders/%s/cancel", url.PathEscape(ord.ID)), tc.token, nil, map[string]any{
					"reason": "E18 hủy đơn QUEUED (" + tc.name + ")",
				}, http.StatusOK)
				var cancOut OrderResponse
				if err := DecodeJSON(cancelResp, &cancOut); err != nil || !StatusEquals(cancOut.Order.Status, "CANCELLED") {
					t.Fatalf("cancel QUEUED order (%s) expected CANCELLED, got err=%v body=%s", tc.name, err, string(cancelResp.Body))
				}

				// Clear injected fault and wait > 1 full worker interval + initial retry backoff so an uncancelled job would fire.
				w3b42InjectCarrierFault(t, env, "mock_express", ord.ID, http.StatusServiceUnavailable, 0)
				waitDur := time.Duration(env.cfg.ShippingCreateWorkerIntervalMS*2+env.cfg.ShippingCreateRetryBaseMS+400) * time.Millisecond
				if waitDur < 1200*time.Millisecond {
					waitDur = 1200 * time.Millisecond
				}
				if waitDur > 4*time.Second {
					waitDur = 4 * time.Second
				}
				time.Sleep(waitDur)

				got := env.getOrder(t, seller.AccessToken, ord.ID)
				if !StatusEquals(got.Status, "CANCELLED") || got.TrackingCode != "" {
					t.Fatalf("E18 (%s): cancelled QUEUED order must stay CANCELLED with empty tracking_code, got status=%s trk=%q job=%q",
						tc.name, got.Status, got.TrackingCode, got.ShipmentJobStatus)
				}
				shResp, err := env.client.DoJSON(env.ctx(t), http.MethodGet, "/api/v1/shipping/shipments/"+url.PathEscape(ord.ID), seller.AccessToken, nil, nil)
				if err != nil {
					t.Fatalf("GET shipment after cancel (%s): %v", tc.name, err)
				}
				if shResp.StatusCode != http.StatusNotFound {
					t.Fatalf("E18 (%s): cancelled QUEUED order must have 0 shipments created (want HTTP 404), got HTTP %d body=%s",
						tc.name, shResp.StatusCode, string(shResp.Body))
				}
			})
		}
	})

	t.Run("4_Transient503_Exhaustion_FailedNotifiesSellerAndRetrySucceeds", func(t *testing.T) {
		ord := newConfirmedOrder()
		w3b42InjectCarrierFault(t, env, "mock_express", ord.ID, http.StatusServiceUnavailable, exhaustFaultCount)

		sellerSeenMail, err := env.mailpit.SnapshotRecipientMessageIDs(env.ctx(t), seller.Email)
		if err != nil {
			t.Fatalf("snapshot seller mailpit: %v", err)
		}

		shipPath := fmt.Sprintf("/api/v1/seller/orders/%s/ship", url.PathEscape(ord.ID))
		shipResp := env.mustDoJSON(t, http.MethodPost, shipPath, seller.AccessToken, nil, SellerShipRequest{
			CarrierCode: "mock_express",
			ServiceCode: "STANDARD",
		}, http.StatusOK)
		var shipOut SellerShipResponse
		if err := DecodeJSON(shipResp, &shipOut); err != nil || !shipOut.Queued {
			t.Fatalf("expected queued=true, got err=%v body=%s", err, string(shipResp.Body))
		}

		// Poll until shipment_job_status becomes FAILED.
		env.pollUntil(t, pollBudget, 300*time.Millisecond, "order shipment_job_status FAILED", func() (bool, string) {
			o := env.getOrder(t, seller.AccessToken, ord.ID)
			return StatusEquals(o.Status, "CONFIRMED") && o.ShipmentJobStatus == "FAILED",
				fmt.Sprintf("status=%s job=%q", o.Status, o.ShipmentJobStatus)
		})

		// Verify seller receives Vietnamese in-app notification + email; buyer does NOT.
		wantLink := "/seller/don-hang/" + ord.ID
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "seller shipment.create_failed in-app notification", func() (bool, string) {
			nResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/notifications?page=1&page_size=30", seller.AccessToken, nil, nil, http.StatusOK)
			var nOut NotificationsListResponse
			if err := DecodeJSON(nResp, &nOut); err != nil {
				return false, err.Error()
			}
			for _, n := range nOut.Items {
				if n.Link == wantLink && strings.Contains(n.Title, "Tạo vận đơn thất bại") {
					if strings.Contains(n.Title+" "+n.Body, "FAILED") {
						t.Errorf("notification leaked raw FAILED enum: %+v", n)
					}
					return true, ""
				}
			}
			return false, fmt.Sprintf("total=%d", len(nOut.Items))
		})

		// Seller email arrived.
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "seller shipment.create_failed email", func() (bool, string) {
			list, lErr := env.mailpit.ListMessages(env.ctx(t), 100)
			if lErr != nil {
				return false, lErr.Error()
			}
			for _, s := range list.Messages {
				if !hasRecipient(s.To, strings.ToLower(seller.Email)) {
					continue
				}
				if _, old := sellerSeenMail[s.ID]; old {
					continue
				}
				if strings.Contains(s.Subject, "Tạo vận đơn thất bại") {
					return true, ""
				}
			}
			return false, "no matching seller failure email yet"
		})

		// Buyer must NOT receive shipment creation failure notification.
		buyerNotifsResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/notifications?page=1&page_size=30", buyer.AccessToken, nil, nil, http.StatusOK)
		var buyerNotifs NotificationsListResponse
		if err := DecodeJSON(buyerNotifsResp, &buyerNotifs); err == nil {
			for _, n := range buyerNotifs.Items {
				if strings.Contains(n.Title, "Tạo vận đơn thất bại") {
					t.Fatalf("E18: buyer must NOT receive shipment.create_failed notification: %+v", n)
				}
			}
		}

		// Clear fault -> seller retries /ship -> succeeds and clears shipment_job_status.
		w3b42InjectCarrierFault(t, env, "mock_express", ord.ID, http.StatusServiceUnavailable, 0)
		retryResp := env.mustDoJSON(t, http.MethodPost, shipPath, seller.AccessToken, nil, SellerShipRequest{
			CarrierCode: "mock_express",
			ServiceCode: "STANDARD",
		}, http.StatusOK)
		var retryOut SellerShipResponse
		if err := DecodeJSON(retryResp, &retryOut); err != nil {
			t.Fatalf("decode seller retry /ship: %v", err)
		}
		if retryOut.Queued || retryOut.TrackingCode == "" || !StatusEquals(retryOut.Order.Status, "PREPARING") || retryOut.Order.ShipmentJobStatus != "" {
			t.Fatalf("E18: seller retry after FAILED expected immediate PREPARING shipment, got %+v", retryOut)
		}
	})
}

// TestE2E_W3B_CODReconciliationCSV verifies E20 and E20-bis:
//   - Direct gRPC & REST RBAC (admin/super_admin only; buyer/seller/service-token denied).
//   - CSV validation (non-digit amount, formula injection, >5000 rows -> 400; >2 MiB -> 413).
//   - UTF-8 BOM text/csv upload with exact match, in-file duplicate (ALREADY_RECONCILED), 1-VND mismatch,
//     unknown tracking (MISSING_LOCAL), and undelivered order (NOT_DELIVERED).
//   - Ledger cod_receivable clears once and remains balanced; queue items tagged source_type=cod_file; audit log recorded.
//   - Multipart re-upload of identical CSV (same sha256) is idempotent; after delivering the undelivered order,
//     re-uploading the SAME CSV matches it and auto-closes its cod_not_delivered queue item.
//   - Paginated GET /api/v1/admin/reconciliation/runs?run_type=cod_file.
func TestE2E_W3B_CODReconciliationCSV(t *testing.T) {
	env := getEnv()
	adminTok := env.adminLogin(t)
	cat, seller1, _ := env.ensureBaseMarketplace(t)

	buyer := env.registerVerifyAndLogin(t, "w3b42-codrec-buyer")
	env.verifyBuyerPhone(t, buyer)
	addr := env.addAddress(t, buyer)

	// 1. Direct gRPC RBAC for ImportCodReconciliation & ListReconciliationRuns + REST RBAC.
	payClient := payW2bDialGRPC(t, env)
	svcTok := promoMintServiceToken("order-service")
	for label, tok := range map[string]string{
		"buyer":         buyer.AccessToken,
		"seller":        seller1.AccessToken,
		"service_token": svcTok,
	} {
		callCtx := metadata.NewOutgoingContext(env.ctx(t), metadata.Pairs("authorization", "Bearer "+tok))
		if _, err := payClient.ImportCodReconciliation(callCtx, &paymentv1.ImportCodReconciliationRequest{
			CarrierCode: "ghn", FileName: "a.csv", FileSha256: "00",
		}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("gRPC ImportCodReconciliation (%s) want PermissionDenied, got %v", label, err)
		}
		if _, err := payClient.ListReconciliationRuns(callCtx, &paymentv1.ListReconciliationRunsRequest{
			RunType: "cod_file",
		}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("gRPC ListReconciliationRuns (%s) want PermissionDenied, got %v", label, err)
		}
	}
	for _, tok := range []string{"", buyer.AccessToken, seller1.AccessToken} {
		want := http.StatusForbidden
		if tok == "" {
			want = http.StatusUnauthorized
		}
		env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/reconciliation/runs?run_type=cod_file", tok, nil, nil, want)
		code, _, _ := w3b42RawHTTP(env.ctx(t), t, http.MethodPost, env.cfg.GatewayURL+"/api/v1/admin/reconciliation/cod/import?carrier_code=ghn", tok, "text/csv", []byte("tracking_code,cod_amount_vnd,delivered_at\nT1,1000,2026-11-24T00:00:00Z\n"))
		if code != want {
			t.Fatalf("POST cod/import (want %d) got %d", want, code)
		}
	}

	// 2. Validation: non-digit amount, formula injection, >5000 rows -> 400; >2 MiB -> 413.
	importURL := env.cfg.GatewayURL + "/api/v1/admin/reconciliation/cod/import?carrier_code=ghn&file_name=bad.csv"
	for _, tc := range []struct {
		name string
		csv  string
	}{
		{"float_amount", "tracking_code,cod_amount_vnd,delivered_at\nTRK1,100.50,2026-11-24T00:00:00Z\n"},
		{"negative_amount", "tracking_code,cod_amount_vnd,delivered_at\nTRK1,-100,2026-11-24T00:00:00Z\n"},
		{"csv_formula", "tracking_code,cod_amount_vnd,delivered_at\n=CMD|' /C calc'!A0,100000,2026-11-24T00:00:00Z\n"},
	} {
		code, _, raw := w3b42RawHTTP(env.ctx(t), t, http.MethodPost, importURL, adminTok, "text/csv", []byte(tc.csv))
		if code != http.StatusBadRequest {
			t.Fatalf("validation %s want 400, got %d body=%s", tc.name, code, string(raw))
		}
	}
	// > 5,000 rows -> 400.
	var bigRows strings.Builder
	bigRows.WriteString("tracking_code,cod_amount_vnd,delivered_at\n")
	for i := 1; i <= 5001; i++ {
		fmt.Fprintf(&bigRows, "TRK%05d,1000,2026-11-24T00:00:00Z\n", i)
	}
	if code, _, raw := w3b42RawHTTP(env.ctx(t), t, http.MethodPost, importURL, adminTok, "text/csv", []byte(bigRows.String())); code != http.StatusBadRequest {
		t.Fatalf(">5000 rows want 400, got %d body=%s", code, string(raw))
	}
	// > 2 MiB -> 413.
	oversize := bytes.Repeat([]byte("a"), 2*1024*1024+1024)
	if code, _, raw := w3b42RawHTTP(env.ctx(t), t, http.MethodPost, importURL, adminTok, "text/csv", oversize); code != http.StatusRequestEntityTooLarge {
		t.Fatalf(">2 MiB CSV want 413, got %d body=%s", code, string(raw))
	}

	// 3. Create 4 COD orders:
	//    - A, B: shipped and delivered (allocations already carry tracking_code).
	//    - C: shipped, NOT delivered yet; CSV provides order_code = ordC.ID (the client_order_code sent to carriers per E18/E20) -> NOT_DELIVERED.
	//    - D: shipped, NOT delivered yet; CSV leaves order_code EMPTY -> MISSING_LOCAL until delivered.
	_, sku := env.createApprovedProduct(t, seller1, cat.ID, "W3B COD Recon Jacket", 210000, 40, 400)
	createAndShip := func() (CheckoutResponse, OrderDTO, string) {
		t.Helper()
		co := w3b42PlaceCODOrder(t, env, buyer, addr.ID, seller1, sku, "ghn", "STANDARD")
		ord := co.Orders[0]
		sResp := env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/seller/orders/%s/ship", url.PathEscape(ord.ID)), seller1.AccessToken, nil, SellerShipRequest{
			CarrierCode: "ghn",
			ServiceCode: "STANDARD",
		}, http.StatusOK)
		var sOut SellerShipResponse
		if err := DecodeJSON(sResp, &sOut); err != nil || sOut.TrackingCode == "" {
			t.Fatalf("ship COD recon order: err=%v body=%s", err, string(sResp.Body))
		}
		return co, ord, sOut.TrackingCode
	}
	deliverAndWaitPaid := func(co CheckoutResponse, ord OrderDTO, trk string) {
		t.Helper()
		w3b42PostShippingWebhook(t, env, "ghn", ord.ID, trk, "delivered", "Giao COD thành công")
		env.pollUntil(t, env.cfg.PollTimeout, 250*time.Millisecond, "COD payment SUCCEEDED for "+ord.ID, func() (bool, string) {
			pay := env.getPayment(t, buyer.AccessToken, co.PaymentID)
			return StatusEquals(pay.Status, "SUCCEEDED"), pay.Status
		})
	}

	coA, ordA, trkA := createAndShip()
	coB, ordB, trkB := createAndShip()
	coC, ordC, trkC := createAndShip()
	coD, ordD, trkD := createAndShip()
	deliverAndWaitPaid(coA, ordA, trkA)
	deliverAndWaitPaid(coB, ordB, trkB)

	codRecBefore := w3b42FindAccountBalance(env.getLedgerSummary(t), "cod_receivable")

	unknownTrk := "UNKNOWN-" + strings.ToUpper(env.runID)
	nowISO := time.Now().UTC().Format(time.RFC3339)
	// Row layout (6 rows):
	//   1. A exact match                                      -> MATCHED
	//   2. A duplicate in same file                           -> ALREADY_RECONCILED
	//   3. B off by 1 VND                                     -> AMOUNT_MISMATCH
	//   4. unknown tracking                                   -> MISSING_LOCAL
	//   5. C undelivered with order_code=ordC.ID (client ref) -> NOT_DELIVERED
	//   6. D undelivered with empty order_code                -> MISSING_LOCAL
	csvBytes := append([]byte{0xEF, 0xBB, 0xBF}, []byte(fmt.Sprintf(
		"tracking_code,cod_amount_vnd,delivered_at,order_code\n"+
			"%s,%d,%s,%s\n"+
			"%s,%d,%s,%s\n"+
			"%s,%d,%s,%s\n"+
			"%s,%d,%s,\n"+
			"%s,%d,%s,%s\n"+
			"%s,%d,%s,\n",
		trkA, ordA.TotalPayableVND, nowISO, ordA.ID,
		trkA, ordA.TotalPayableVND, nowISO, ordA.ID,
		trkB, ordB.TotalPayableVND+1, nowISO, ordB.ID,
		unknownTrk, 123000, nowISO,
		trkC, ordC.TotalPayableVND, nowISO, ordC.ID,
		trkD, ordD.TotalPayableVND, nowISO,
	))...)

	type importResp struct {
		Run struct {
			RunID                  string `json:"run_id"`
			RunType                string `json:"run_type"`
			TotalRows              int32  `json:"total_rows"`
			MatchedCount           int32  `json:"matched_count"`
			AlreadyReconciledCount int32  `json:"already_reconciled_count"`
			MissingLocalCount      int32  `json:"missing_local_count"`
			AmountMismatchCount    int32  `json:"amount_mismatch_count"`
			NotDeliveredCount      int32  `json:"not_delivered_count"`
			TotalMatchedVND        int64  `json:"total_matched_vnd"`
		} `json:"run"`
		Items []struct {
			Status       string `json:"status"`
			TrackingCode string `json:"tracking_code"`
		} `json:"items"`
	}

	// Upload #1 (raw text/csv with UTF-8 BOM).
	u1URL := env.cfg.GatewayURL + "/api/v1/admin/reconciliation/cod/import?carrier_code=ghn&file_name=cod-w3b-1.csv"
	c1, _, raw1 := w3b42RawHTTP(env.ctx(t), t, http.MethodPost, u1URL, adminTok, "text/csv; charset=utf-8", csvBytes)
	if c1 != http.StatusOK {
		t.Fatalf("COD import #1 want 200, got %d body=%s", c1, string(raw1))
	}
	var out1 importResp
	if err := json.Unmarshal(raw1, &out1); err != nil {
		t.Fatalf("decode COD import #1: %v", err)
	}
	if out1.Run.RunType != "cod_file" || out1.Run.TotalRows != 6 ||
		out1.Run.MatchedCount != 1 || out1.Run.AlreadyReconciledCount != 1 ||
		out1.Run.AmountMismatchCount != 1 || out1.Run.MissingLocalCount != 2 ||
		out1.Run.NotDeliveredCount != 1 || out1.Run.TotalMatchedVND != ordA.TotalPayableVND {
		t.Fatalf("E20 import #1 summary mismatch: %+v", out1.Run)
	}

	ledAfter1 := env.getLedgerSummary(t)
	if !ledAfter1.IsBalanced {
		t.Fatalf("ledger unbalanced after COD import #1: %+v", ledAfter1)
	}
	codRecAfter1 := w3b42FindAccountBalance(ledAfter1, "cod_receivable")
	if codRecBefore-codRecAfter1 != ordA.TotalPayableVND {
		t.Fatalf("E20: cod_receivable delta = %d, want %d (before=%d after=%d)",
			codRecBefore-codRecAfter1, ordA.TotalPayableVND, codRecBefore, codRecAfter1)
	}

	// Verify queue items carry source_type=cod_file and tracking_code.
	qResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/reconciliation/queue?status=pending&page=1&page_size=100", adminTok, nil, nil, http.StatusOK)
	var qOut struct {
		Items []struct {
			Reason           string `json:"reason"`
			ResolutionStatus string `json:"resolution_status"`
			SourceType       string `json:"source_type"`
			TrackingCode     string `json:"tracking_code"`
		} `json:"items"`
	}
	if err := DecodeJSON(qResp, &qOut); err != nil {
		t.Fatalf("decode reconciliation queue: %v", err)
	}
	seenQueue := map[string]string{}
	for _, it := range qOut.Items {
		if it.SourceType == "cod_file" && it.TrackingCode != "" {
			seenQueue[it.TrackingCode] = it.Reason
		}
	}
	if seenQueue[trkB] != "cod_amount_mismatch" || seenQueue[unknownTrk] != "cod_missing_local" ||
		seenQueue[trkC] != "cod_not_delivered" || seenQueue[trkD] != "cod_missing_local" {
		t.Fatalf("E20: expected cod_file queue items for B(mismatch)/unknown(missing)/C(not_delivered)/D(missing), got %v", seenQueue)
	}

	// Verify audit log recorded reconciliation_run.
	audResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/audit-logs?target_type=reconciliation_run&target_id="+url.QueryEscape(out1.Run.RunID), adminTok, nil, nil, http.StatusOK)
	if !strings.Contains(string(audResp.Body), out1.Run.RunID) {
		t.Fatalf("E20: audit log missing reconciliation_run %s: %s", out1.Run.RunID, string(audResp.Body))
	}

	// Upload #2: IDENTICAL CSV bytes via multipart/form-data -> idempotent (0 matched, 2 already_reconciled, 0 ledger change).
	uploadMultipart := func(fileName string) importResp {
		t.Helper()
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		_ = mw.WriteField("carrier_code", "ghn")
		fw, err := mw.CreateFormFile("file", fileName)
		if err != nil {
			t.Fatalf("create multipart form file: %v", err)
		}
		if _, err := fw.Write(csvBytes); err != nil {
			t.Fatalf("write multipart csv: %v", err)
		}
		if err := mw.Close(); err != nil {
			t.Fatalf("close multipart writer: %v", err)
		}
		c, _, raw := w3b42RawHTTP(env.ctx(t), t, http.MethodPost, env.cfg.GatewayURL+"/api/v1/admin/reconciliation/cod/import", adminTok, mw.FormDataContentType(), buf.Bytes())
		if c != http.StatusOK {
			t.Fatalf("multipart COD import (%s) want 200, got %d body=%s", fileName, c, string(raw))
		}
		var res importResp
		if err := json.Unmarshal(raw, &res); err != nil {
			t.Fatalf("decode multipart COD import (%s): %v", fileName, err)
		}
		return res
	}

	out2 := uploadMultipart("cod-w3b-2.csv")
	if out2.Run.MatchedCount != 0 || out2.Run.AlreadyReconciledCount != 2 ||
		out2.Run.AmountMismatchCount != 1 || out2.Run.MissingLocalCount != 2 ||
		out2.Run.NotDeliveredCount != 1 || out2.Run.TotalMatchedVND != 0 {
		t.Fatalf("E20 duplicate upload must post 0 new entries, got %+v", out2.Run)
	}
	if gotBal := w3b42FindAccountBalance(env.getLedgerSummary(t), "cod_receivable"); gotBal != codRecAfter1 {
		t.Fatalf("E20 duplicate upload changed cod_receivable: got %d want %d", gotBal, codRecAfter1)
	}

	// E20-bis: Deliver BOTH orders C and D -> re-upload EXACT SAME CSV (same sha256) ->
	// both C and D match, cod_receivable drops by P_C + P_D, and both old queue rows (cod_not_delivered & cod_missing_local) auto-close!
	deliverAndWaitPaid(coC, ordC, trkC)
	deliverAndWaitPaid(coD, ordD, trkD)
	codRecBefore3 := w3b42FindAccountBalance(env.getLedgerSummary(t), "cod_receivable")
	wantDelta3 := ordC.TotalPayableVND + ordD.TotalPayableVND

	out3 := uploadMultipart("cod-w3b-3.csv")
	if out3.Run.MatchedCount != 2 || out3.Run.AlreadyReconciledCount != 2 ||
		out3.Run.AmountMismatchCount != 1 || out3.Run.MissingLocalCount != 1 ||
		out3.Run.NotDeliveredCount != 0 || out3.Run.TotalMatchedVND != wantDelta3 {
		t.Fatalf("E20-bis re-upload after C+D delivered expected matched=2 already=2 mismatch=1 missing=1 not_delivered=0 total=%d, got %+v",
			wantDelta3, out3.Run)
	}
	ledAfter3 := env.getLedgerSummary(t)
	if !ledAfter3.IsBalanced {
		t.Fatalf("ledger unbalanced after COD import #3")
	}
	codRecAfter3 := w3b42FindAccountBalance(ledAfter3, "cod_receivable")
	if codRecBefore3-codRecAfter3 != wantDelta3 {
		t.Fatalf("E20-bis: cod_receivable delta for orders C+D = %d, want %d", codRecBefore3-codRecAfter3, wantDelta3)
	}

	// Verify BOTH C (cod_not_delivered) and D (cod_missing_local) queue items transitioned out of pending into assigned.
	qPendingAfter3 := env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/reconciliation/queue?status=pending&page=1&page_size=100", adminTok, nil, nil, http.StatusOK)
	if strings.Contains(string(qPendingAfter3.Body), trkC) || strings.Contains(string(qPendingAfter3.Body), trkD) {
		t.Fatalf("E20-bis: C (%s) and D (%s) must no longer remain pending after re-upload match: %s", trkC, trkD, string(qPendingAfter3.Body))
	}
	qAssignedResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/reconciliation/queue?status=assigned&page=1&page_size=100", adminTok, nil, nil, http.StatusOK)
	if !strings.Contains(string(qAssignedResp.Body), trkC) || !strings.Contains(string(qAssignedResp.Body), trkD) {
		t.Fatalf("E20-bis: expected auto-closed queue items for C (%s) and D (%s) in status=assigned, got %s", trkC, trkD, string(qAssignedResp.Body))
	}

	// 4. Paginated GET /api/v1/admin/reconciliation/runs?run_type=cod_file.
	runsResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/reconciliation/runs?run_type=cod_file&page=1&page_size=2", adminTok, nil, nil, http.StatusOK)
	var runsOut struct {
		Items []struct {
			RunType string `json:"run_type"`
		} `json:"items"`
		Page     int32 `json:"page"`
		PageSize int32 `json:"page_size"`
		Total    int64 `json:"total"`
	}
	if err := DecodeJSON(runsResp, &runsOut); err != nil {
		t.Fatalf("decode reconciliation runs: %v", err)
	}
	if len(runsOut.Items) != 2 || runsOut.Total < 3 || runsOut.Page != 1 || runsOut.PageSize != 2 {
		t.Fatalf("unexpected reconciliation runs pagination: %+v", runsOut)
	}
	for _, r := range runsOut.Items {
		if r.RunType != "cod_file" {
			t.Fatalf("run_type filter leaked %q", r.RunType)
		}
	}
}

// TestE2E_W3B_Z_AnonymousCalculateFeeRateLimit429 runs LAST in 42_ so exhausting the container IP's
// anonymous calculate-fee bucket (30/min) never interferes with earlier tests (and 43_ does not call calculate-fee).
func TestE2E_W3B_Z_AnonymousCalculateFeeRateLimit429(t *testing.T) {
	env := getEnv()
	_, seller1, _ := env.ensureBaseMarketplace(t)

	body := map[string]any{
		"shop_id":          seller1.ShopID,
		"to_province_code": "01",
		"to_ward_code":     "",
		"weight_grams":     500,
	}
	hit429 := false
	for i := 1; i <= 45; i++ {
		code, hdr, raw := w3b42RawPostJSON(t, env, "/api/v1/shipping/calculate-fee", "", body)
		if code == http.StatusTooManyRequests {
			if lim, _ := strconv.Atoi(hdr.Get("X-RateLimit-Limit")); lim > 60 {
				retrySec, _ := strconv.Atoi(hdr.Get("Retry-After"))
				if retrySec < 1 {
					retrySec = 1
				}
				time.Sleep(time.Duration(retrySec) * time.Second)
				continue
			}
			w3b42AssertRawErrorCode(t, "anonymous calculate-fee 429", raw, "RATE_LIMITED")
			retrySec, _ := strconv.Atoi(hdr.Get("Retry-After"))
			if retrySec < 1 {
				t.Fatalf("anonymous calculate-fee 429 expected Retry-After >= 1, got %q", hdr.Get("Retry-After"))
			}
			hit429 = true
			break
		}
		if code != http.StatusOK {
			t.Fatalf("anonymous calculate-fee #%d returned HTTP %d: %s", i, code, string(raw))
		}
	}
	if !hit429 {
		t.Fatalf("E17: anonymous POST /api/v1/shipping/calculate-fee did not return 429 within 45 calls")
	}
}
