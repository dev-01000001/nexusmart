package e2e

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

type userFixture struct {
	UserID       string
	Email        string
	Phone        string
	Password     string
	AccessToken  string
	RefreshToken string
	AddressID    string
	User         UserDTO
}

type sellerFixture struct {
	userFixture
	ShopID   string
	ShopName string
}

type testEnv struct {
	mu      sync.Mutex
	cfg     Config
	client  *Client
	mailpit *MailpitClient

	runID     string
	phoneBase int64
	seq       atomic.Int64

	adminToken string
	province   ProvinceDTO
	ward       WardDTO
	category   CategoryDTO
	seller1    *sellerFixture
	seller2    *sellerFixture
}

var (
	globalEnvOnce sync.Once
	globalEnv     *testEnv
)

func getEnv() *testEnv {
	globalEnvOnce.Do(func() {
		cfg := LoadConfig()
		nowNano := time.Now().UnixNano()
		globalEnv = &testEnv{
			cfg:       cfg,
			client:    NewClient(cfg),
			mailpit:   NewMailpitClient(cfg.MailpitURL, cfg.HTTPTimeout),
			runID:     strings.ReplaceAll(uuid.NewString()[:8], "-", ""),
			phoneBase: (nowNano / 1000) % 80000000,
		}
	})
	return globalEnv
}

func (e *testEnv) nextSeq() int64 {
	return e.seq.Add(1)
}

func (e *testEnv) uniqueEmail(prefix string) string {
	return fmt.Sprintf("%s-%s-%d@nexusmart.test", prefix, e.runID, e.nextSeq())
}

func (e *testEnv) uniquePhone() string {
	n := (e.phoneBase + e.nextSeq()*7) % 90000000
	return fmt.Sprintf("09%08d", n+10000000)
}

func (e *testEnv) uniqueCode(prefix string) string {
	return strings.ToUpper(fmt.Sprintf("%s%s%d", prefix, e.runID, e.nextSeq()))
}

func (e *testEnv) ctx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func (e *testEnv) mustDoJSON(
	t *testing.T,
	method string,
	path string,
	token string,
	headers map[string]string,
	reqBody any,
	allowedStatuses ...int,
) *RawResponse {
	t.Helper()
	resp, err := e.client.DoJSON(e.ctx(t), method, path, token, headers, reqBody)
	if err != nil {
		t.Fatalf("%s %s failed: %v", method, path, err)
	}
	for _, code := range allowedStatuses {
		if resp.StatusCode == code {
			return resp
		}
	}
	traceID := resp.Headers.Get("X-Trace-Id")
	t.Fatalf(
		"%s %s returned HTTP %d (want %v) trace_id=%q body=%s",
		method, path, resp.StatusCode, allowedStatuses, traceID, string(resp.Body),
	)
	return nil
}

func (e *testEnv) adminLogin(t *testing.T) string {
	t.Helper()
	e.mu.Lock()
	if e.adminToken != "" {
		tok := e.adminToken
		e.mu.Unlock()
		return tok
	}
	e.mu.Unlock()

	resp := e.mustDoJSON(t, http.MethodPost, "/api/v1/auth/login", "", nil, LoginRequest{
		Email:    e.cfg.AdminEmail,
		Password: e.cfg.AdminPassword,
	}, http.StatusOK)

	var out LoginResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode admin login: %v", err)
	}
	if out.AccessToken == "" {
		t.Fatalf("admin login returned empty access_token: %s", string(resp.Body))
	}

	e.mu.Lock()
	e.adminToken = out.AccessToken
	e.mu.Unlock()
	return out.AccessToken
}

func (e *testEnv) ensureProvinceAndWard(t *testing.T) (ProvinceDTO, WardDTO) {
	t.Helper()
	e.mu.Lock()
	if e.province.Code != "" && e.ward.Code != "" {
		p, w := e.province, e.ward
		e.mu.Unlock()
		return p, w
	}
	e.mu.Unlock()

	provResp := e.mustDoJSON(t, http.MethodGet, "/api/v1/shipping/provinces", "", nil, nil, http.StatusOK)
	var provOut ProvincesResponse
	if err := DecodeJSON(provResp, &provOut); err != nil {
		t.Fatalf("decode provinces: %v", err)
	}
	if len(provOut.Provinces) < 34 {
		t.Fatalf("expected at least 34 provinces (2-level VN admin data), got %d", len(provOut.Provinces))
	}

	chosenProv := provOut.Provinces[0]
	wardsPath := fmt.Sprintf("/api/v1/shipping/provinces/%s/wards", url.PathEscape(chosenProv.Code))
	wardResp := e.mustDoJSON(t, http.MethodGet, wardsPath, "", nil, nil, http.StatusOK)
	var wardOut WardsResponse
	if err := DecodeJSON(wardResp, &wardOut); err != nil {
		t.Fatalf("decode wards: %v", err)
	}
	if len(wardOut.Wards) == 0 {
		t.Fatalf("expected at least 1 ward for province %s, got 0", chosenProv.Code)
	}

	chosenWard := wardOut.Wards[0]
	e.mu.Lock()
	e.province = chosenProv
	e.ward = chosenWard
	e.mu.Unlock()
	return chosenProv, chosenWard
}

func (e *testEnv) ensureCategory(t *testing.T) CategoryDTO {
	t.Helper()
	e.mu.Lock()
	if e.category.ID != "" {
		cat := e.category
		e.mu.Unlock()
		return cat
	}
	e.mu.Unlock()

	adminTok := e.adminLogin(t)
	slug := strings.ToLower(e.uniqueCode("cat-"))
	resp := e.mustDoJSON(t, http.MethodPost, "/api/v1/admin/categories", adminTok, nil, CreateCategoryRequest{
		Name:              "E2E Electronics " + e.runID,
		Slug:              slug,
		CommissionRateBps: 500,
		SortOrder:         1,
	}, http.StatusCreated, http.StatusOK)

	var out CategoryResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode category: %v", err)
	}
	if out.Category.ID == "" {
		t.Fatalf("create category returned empty id: %s", string(resp.Body))
	}

	e.mu.Lock()
	e.category = out.Category
	e.mu.Unlock()
	return out.Category
}

func (e *testEnv) registerVerifyAndLogin(t *testing.T, label string) *userFixture {
	t.Helper()
	email := e.uniqueEmail(label)
	phone := e.uniquePhone()
	password := "NexusPass123!"

	regResp := e.mustDoJSON(t, http.MethodPost, "/api/v1/auth/register", "", nil, RegisterRequest{
		FullName: "E2E " + label,
		Email:    email,
		Phone:    phone,
		Password: password,
	}, http.StatusCreated, http.StatusOK)

	var regOut RegisterResponse
	if err := DecodeJSON(regResp, &regOut); err != nil {
		t.Fatalf("decode register (%s): %v", label, err)
	}
	if regOut.UserID == "" {
		t.Fatalf("register (%s) returned empty user_id: %s", label, string(regResp.Body))
	}

	otp, err := e.mailpit.WaitForOTP(e.ctx(t), email, e.cfg.PollTimeout)
	if err != nil {
		t.Fatalf("wait OTP for %s (%s) from Mailpit: %v", label, email, err)
	}
	t.Logf("[%s] received OTP from Mailpit for %s", label, email)

	verifyResp := e.mustDoJSON(t, http.MethodPost, "/api/v1/auth/verify-otp", "", nil, VerifyOTPRequest{
		Email: email,
		OTP:   otp,
	}, http.StatusOK)
	var verifyOut VerifyOTPResponse
	if err := DecodeJSON(verifyResp, &verifyOut); err != nil {
		t.Fatalf("decode verify-otp (%s): %v", label, err)
	}
	if !verifyOut.Verified {
		t.Fatalf("verify-otp (%s) returned verified=false: %s", label, string(verifyResp.Body))
	}

	loginResp := e.mustDoJSON(t, http.MethodPost, "/api/v1/auth/login", "", nil, LoginRequest{
		Email:    email,
		Password: password,
	}, http.StatusOK)
	var loginOut LoginResponse
	if err := DecodeJSON(loginResp, &loginOut); err != nil {
		t.Fatalf("decode login (%s): %v", label, err)
	}
	if loginOut.AccessToken == "" || loginOut.RefreshToken == "" {
		t.Fatalf("login (%s) missing tokens: %s", label, string(loginResp.Body))
	}

	userID := loginOut.User.ID
	if userID == "" {
		userID = regOut.UserID
	}

	return &userFixture{
		UserID:       userID,
		Email:        email,
		Phone:        phone,
		Password:     password,
		AccessToken:  loginOut.AccessToken,
		RefreshToken: loginOut.RefreshToken,
		User:         loginOut.User,
	}
}

func (e *testEnv) addAddress(t *testing.T, u *userFixture) AddressDTO {
	t.Helper()
	prov, ward := e.ensureProvinceAndWard(t)
	resp := e.mustDoJSON(t, http.MethodPost, "/api/v1/users/me/addresses", u.AccessToken, nil, CreateAddressRequest{
		RecipientName:  "Nguyen Van E2E",
		RecipientPhone: u.Phone,
		ProvinceCode:   prov.Code,
		ProvinceName:   prov.Name,
		WardCode:       ward.Code,
		WardName:       ward.Name,
		StreetAddress:  fmt.Sprintf("123 Le Loi Street #%d", e.nextSeq()),
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

func (e *testEnv) createApprovedSeller(t *testing.T, label string) *sellerFixture {
	t.Helper()
	u := e.registerVerifyAndLogin(t, label)
	addr := e.addAddress(t, u)

	shopName := fmt.Sprintf("NexusShop-%s-%s-%d", label, e.runID, e.nextSeq())
	shopResp := e.mustDoJSON(t, http.MethodPost, "/api/v1/shops/register", u.AccessToken, nil, RegisterShopRequest{
		Name:               shopName,
		Description:        "E2E verified merchant store",
		PickupAddressID:    addr.ID,
		ContactPhone:       u.Phone,
		BusinessLicenseDoc: "https://sandbox.nexusmart.local/docs/license.pdf",
	}, http.StatusCreated, http.StatusOK)

	var shopOut ShopResponse
	if err := DecodeJSON(shopResp, &shopOut); err != nil {
		t.Fatalf("decode register shop (%s): %v", label, err)
	}
	if shopOut.Shop.ID == "" {
		t.Fatalf("register shop (%s) returned empty shop.id: %s", label, string(shopResp.Body))
	}

	adminTok := e.adminLogin(t)
	reviewPath := fmt.Sprintf("/api/v1/admin/shops/%s/review", url.PathEscape(shopOut.Shop.ID))
	reviewResp := e.mustDoJSON(t, http.MethodPost, reviewPath, adminTok, nil, ReviewRequest{
		Decision: "APPROVE",
		Reason:   "E2E automated shop approval",
	}, http.StatusOK)
	var reviewOut ShopResponse
	if err := DecodeJSON(reviewResp, &reviewOut); err != nil {
		t.Fatalf("decode review shop (%s): %v", label, err)
	}
	if !StatusEquals(reviewOut.Shop.Status, "ACTIVE") {
		t.Fatalf("expected approved shop status ACTIVE, got %q (body=%s)", reviewOut.Shop.Status, string(reviewResp.Body))
	}

	// Verify token refresh works, then re-login to obtain fresh seller JWT carrying role=seller + shop_id.
	refreshResp := e.mustDoJSON(t, http.MethodPost, "/api/v1/auth/refresh", "", nil, RefreshRequest{
		RefreshToken: u.RefreshToken,
	}, http.StatusOK)
	var refreshOut RefreshResponse
	if err := DecodeJSON(refreshResp, &refreshOut); err != nil {
		t.Fatalf("decode refresh token (%s): %v", label, err)
	}
	if refreshOut.AccessToken == "" || refreshOut.RefreshToken == "" {
		t.Fatalf("refresh token (%s) returned empty tokens: %s", label, string(refreshResp.Body))
	}

	reLoginResp := e.mustDoJSON(t, http.MethodPost, "/api/v1/auth/login", "", nil, LoginRequest{
		Email:    u.Email,
		Password: u.Password,
	}, http.StatusOK)
	var reLoginOut LoginResponse
	if err := DecodeJSON(reLoginResp, &reLoginOut); err != nil {
		t.Fatalf("decode seller re-login (%s): %v", label, err)
	}
	if reLoginOut.AccessToken == "" {
		t.Fatalf("seller re-login (%s) returned empty access_token", label)
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

func (e *testEnv) ensureBaseMarketplace(t *testing.T) (CategoryDTO, *sellerFixture, *sellerFixture) {
	t.Helper()
	cat := e.ensureCategory(t)

	e.mu.Lock()
	s1, s2 := e.seller1, e.seller2
	e.mu.Unlock()

	if s1 == nil {
		s1 = e.createApprovedSeller(t, "seller1")
		e.mu.Lock()
		e.seller1 = s1
		e.mu.Unlock()
	}
	if s2 == nil {
		s2 = e.createApprovedSeller(t, "seller2")
		e.mu.Lock()
		e.seller2 = s2
		e.mu.Unlock()
	}
	return cat, s1, s2
}

func (e *testEnv) createApprovedProduct(
	t *testing.T,
	seller *sellerFixture,
	categoryID string,
	name string,
	priceVND int64,
	stock int32,
	weightGrams int32,
) (ProductDTO, SKUDTO) {
	t.Helper()
	skuCode := e.uniqueCode("SKU")
	createResp := e.mustDoJSON(t, http.MethodPost, "/api/v1/seller/products", seller.AccessToken, nil, CreateProductRequest{
		CategoryID:  categoryID,
		Name:        name,
		Description: "E2E test product " + name,
		Images:      []string{"https://sandbox.nexusmart.local/img/p1.png"},
		SKUs: []SKUInputDTO{
			{
				SKUCode:     skuCode,
				Name:        "Default Variant",
				Attributes:  map[string]string{"edition": "standard"},
				PriceVND:    priceVND,
				Stock:       stock,
				WeightGrams: weightGrams,
				ImageURL:    "https://sandbox.nexusmart.local/img/sku1.png",
			},
		},
	}, http.StatusCreated, http.StatusOK)

	var createOut ProductResponse
	if err := DecodeJSON(createResp, &createOut); err != nil {
		t.Fatalf("decode create product: %v", err)
	}
	if createOut.Product.ID == "" || len(createOut.Product.SKUs) == 0 {
		t.Fatalf("create product missing id/skus: %s", string(createResp.Body))
	}

	adminTok := e.adminLogin(t)
	reviewPath := fmt.Sprintf("/api/v1/admin/products/%s/review", url.PathEscape(createOut.Product.ID))
	reviewResp := e.mustDoJSON(t, http.MethodPost, reviewPath, adminTok, nil, ReviewRequest{
		Decision: "APPROVE",
		Reason:   "E2E automated product approval",
	}, http.StatusOK)
	var reviewOut ProductResponse
	if err := DecodeJSON(reviewResp, &reviewOut); err != nil {
		t.Fatalf("decode review product: %v", err)
	}
	if !StatusEquals(reviewOut.Product.Status, "APPROVED") {
		t.Fatalf("expected approved product status APPROVED, got %q", reviewOut.Product.Status)
	}

	sku := createOut.Product.SKUs[0]
	if len(reviewOut.Product.SKUs) > 0 {
		sku = reviewOut.Product.SKUs[0]
	}
	return reviewOut.Product, sku
}

func (e *testEnv) getProductSKU(t *testing.T, productID, skuID string) SKUDTO {
	t.Helper()
	path := fmt.Sprintf("/api/v1/products/%s", url.PathEscape(productID))
	resp := e.mustDoJSON(t, http.MethodGet, path, "", nil, nil, http.StatusOK)
	var out ProductResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode get product %s: %v", productID, err)
	}
	for _, s := range out.Product.SKUs {
		if s.ID == skuID {
			return s
		}
	}
	t.Fatalf("sku %s not found in product %s (body=%s)", skuID, productID, string(resp.Body))
	return SKUDTO{}
}

func (e *testEnv) createShopVoucher(t *testing.T, seller *sellerFixture, valueVND, minOrderVND int64, quota int32) VoucherDTO {
	t.Helper()
	now := time.Now().UTC()
	resp := e.mustDoJSON(t, http.MethodPost, "/api/v1/seller/vouchers", seller.AccessToken, nil, CreateVoucherRequest{
		Code:           e.uniqueCode("SHPV"),
		Name:           "E2E Shop Voucher",
		DiscountType:   "FIXED",
		ValueVND:       valueVND,
		MaxDiscountVND: valueVND,
		MinOrderVND:    minOrderVND,
		Quota:          quota,
		PerUserLimit:   5,
		BudgetVND:      valueVND * int64(quota),
		StartsAt:       now.Add(-1 * time.Hour).Format(time.RFC3339),
		EndsAt:         now.Add(24 * time.Hour).Format(time.RFC3339),
	}, http.StatusCreated, http.StatusOK)

	var out VoucherResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode shop voucher: %v", err)
	}
	if out.Voucher.ID == "" {
		t.Fatalf("create shop voucher returned empty id: %s", string(resp.Body))
	}
	return out.Voucher
}

func (e *testEnv) createAdminVoucher(t *testing.T, scope string, valueVND, minOrderVND int64, quota int32) VoucherDTO {
	t.Helper()
	now := time.Now().UTC()
	adminTok := e.adminLogin(t)
	resp := e.mustDoJSON(t, http.MethodPost, "/api/v1/admin/vouchers", adminTok, nil, CreateVoucherRequest{
		Code:           e.uniqueCode(scope[:3]),
		Name:           "E2E " + scope + " Voucher",
		Scope:          scope,
		DiscountType:   "FIXED",
		ValueVND:       valueVND,
		MaxDiscountVND: valueVND,
		MinOrderVND:    minOrderVND,
		Quota:          quota,
		PerUserLimit:   5,
		BudgetVND:      valueVND * int64(quota),
		StartsAt:       now.Add(-1 * time.Hour).Format(time.RFC3339),
		EndsAt:         now.Add(24 * time.Hour).Format(time.RFC3339),
	}, http.StatusCreated, http.StatusOK)

	var out VoucherResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode admin %s voucher: %v", scope, err)
	}
	if out.Voucher.ID == "" {
		t.Fatalf("create admin %s voucher returned empty id: %s", scope, string(resp.Body))
	}
	return out.Voucher
}

func (e *testEnv) saveVoucher(t *testing.T, buyerToken string, v VoucherDTO) {
	t.Helper()
	path := fmt.Sprintf("/api/v1/vouchers/%s/save", url.PathEscape(v.ID))
	resp := e.mustDoJSON(t, http.MethodPost, path, buyerToken, nil, SaveVoucherRequest{
		Code: v.Code,
	}, http.StatusOK, http.StatusCreated)
	var out SaveVoucherResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode save voucher %s: %v", v.ID, err)
	}
	if !out.Saved {
		t.Fatalf("save voucher %s returned saved=false: %s", v.ID, string(resp.Body))
	}
}

func (e *testEnv) getVoucherRemainingQuota(t *testing.T, token, scope, voucherID string) int32 {
	t.Helper()
	path := "/api/v1/vouchers"
	if scope != "" {
		path += "?scope=" + url.QueryEscape(scope)
	}
	resp := e.mustDoJSON(t, http.MethodGet, path, token, nil, nil, http.StatusOK)
	var out VouchersListResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode list vouchers: %v", err)
	}
	for _, v := range out.Vouchers {
		if v.ID == voucherID {
			if v.RemainingQuota == 0 && v.Quota > 0 && v.UsedCount < v.Quota {
				return v.Quota - v.UsedCount
			}
			return v.RemainingQuota
		}
	}
	t.Fatalf("voucher %s not found in GET %s (body=%s)", voucherID, path, string(resp.Body))
	return -1
}

func (e *testEnv) creditCoins(t *testing.T, userID string, amount int64, reason string) int64 {
	t.Helper()
	adminTok := e.adminLogin(t)
	resp := e.mustDoJSON(t, http.MethodPost, "/api/v1/admin/coins/credit", adminTok, nil, CreditCoinsRequest{
		UserID:      userID,
		Amount:      amount,
		Reason:      reason,
		ReferenceID: "ref-" + uuid.NewString(),
	}, http.StatusOK, http.StatusCreated)
	var out CreditCoinsResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode credit coins: %v", err)
	}
	return out.Balance
}

func (e *testEnv) getCoinBalance(t *testing.T, userToken string) int64 {
	t.Helper()
	resp := e.mustDoJSON(t, http.MethodGet, "/api/v1/coins/balance", userToken, nil, nil, http.StatusOK)
	var out CoinBalanceResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode coin balance: %v", err)
	}
	return out.Balance
}

func (e *testEnv) calculateShippingQuote(
	t *testing.T,
	buyerToken string,
	shopID string,
	weightGrams int32,
	codVND int64,
) CarrierQuoteDTO {
	t.Helper()
	prov, ward := e.ensureProvinceAndWard(t)
	resp := e.mustDoJSON(t, http.MethodPost, "/api/v1/shipping/calculate-fee", buyerToken, nil, CalculateFeeRequest{
		ShopID:           shopID,
		FromProvinceCode: prov.Code,
		FromWardCode:     ward.Code,
		ToProvinceCode:   prov.Code,
		ToWardCode:       ward.Code,
		WeightGrams:      weightGrams,
		CODAmountVND:     codVND,
	}, http.StatusOK)
	var out CalculateFeeResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode calculate shipping fee: %v", err)
	}
	if len(out.Quotes) == 0 {
		t.Fatalf("calculate shipping fee returned 0 quotes: %s", string(resp.Body))
	}
	for _, q := range out.Quotes {
		if strings.EqualFold(q.CarrierCode, "ghn") {
			return q
		}
	}
	return out.Quotes[0]
}

func (e *testEnv) getOrder(t *testing.T, token, orderID string) OrderDTO {
	t.Helper()
	path := fmt.Sprintf("/api/v1/orders/%s", url.PathEscape(orderID))
	resp := e.mustDoJSON(t, http.MethodGet, path, token, nil, nil, http.StatusOK)
	var out OrderResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode get order %s: %v", orderID, err)
	}
	return out.Order
}

func (e *testEnv) getPayment(t *testing.T, token, paymentID string) PaymentDTO {
	t.Helper()
	path := fmt.Sprintf("/api/v1/payments/%s", url.PathEscape(paymentID))
	resp := e.mustDoJSON(t, http.MethodGet, path, token, nil, nil, http.StatusOK)
	var out PaymentResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode get payment %s: %v", paymentID, err)
	}
	return out.Payment
}

func (e *testEnv) getLedgerSummary(t *testing.T) LedgerSummaryResponse {
	t.Helper()
	adminTok := e.adminLogin(t)
	resp := e.mustDoJSON(t, http.MethodGet, "/api/v1/admin/ledger/summary", adminTok, nil, nil, http.StatusOK)
	var out LedgerSummaryResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode ledger summary: %v", err)
	}
	return out
}

func (e *testEnv) pollUntil(t *testing.T, timeout, interval time.Duration, desc string, check func() (bool, string)) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastMsg string
	for {
		ok, msg := check()
		if ok {
			return
		}
		lastMsg = msg
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s; last state: %s", timeout, desc, lastMsg)
		}
		time.Sleep(interval)
	}
}
