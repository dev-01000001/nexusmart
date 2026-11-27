package e2e

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

type userFixture struct {
	UserID        string
	Email         string
	Phone         string
	Password      string
	AccessToken   string
	RefreshToken  string
	AddressID     string
	PhoneVerified bool
	User          UserDTO
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

	adminToken    string
	province      ProvinceDTO
	ward          WardDTO
	category      CategoryDTO
	seller1       *sellerFixture
	seller2       *sellerFixture
	verifiedBuyer *userFixture
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
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
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

const fixtureTokenRefreshThreshold = 5 * time.Minute

// jwtNeedsRefresh reports whether token is empty, malformed, lacks a valid numeric exp claim,
// or has less than threshold remaining before expiry relative to now.
func jwtNeedsRefresh(token string, now time.Time, threshold time.Duration) bool {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 || parts[1] == "" {
		return true
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return true
	}
	var claims struct {
		Exp *float64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp == nil || *claims.Exp <= 0 {
		return true
	}
	return time.Unix(int64(*claims.Exp), 0).Sub(now) < threshold
}

// ensureFreshToken returns the cached token if it has at least fixtureTokenRefreshThreshold
// remaining; otherwise it re-authenticates via HTTP without holding e.mu and updates the cache
// in place via apply under e.mu.
func (e *testEnv) ensureFreshToken(
	ctx context.Context,
	readToken func() string,
	email, password string,
	apply func(LoginResponse),
) (string, error) {
	e.mu.Lock()
	cur := readToken()
	e.mu.Unlock()
	if !jwtNeedsRefresh(cur, time.Now(), fixtureTokenRefreshThreshold) {
		return cur, nil
	}

	resp, err := e.client.DoJSON(ctx, http.MethodPost, "/api/v1/auth/login", "", nil, LoginRequest{
		Email:    email,
		Password: password,
	})
	if err != nil {
		return "", fmt.Errorf("login %s: %w", email, err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("login %s returned HTTP %d: %s", email, resp.StatusCode, string(resp.Body))
	}
	var out LoginResponse
	if err := DecodeJSON(resp, &out); err != nil {
		return "", fmt.Errorf("decode login %s: %w", email, err)
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("login %s returned empty access_token: %s", email, string(resp.Body))
	}

	e.mu.Lock()
	if latest := readToken(); !jwtNeedsRefresh(latest, time.Now(), fixtureTokenRefreshThreshold) {
		e.mu.Unlock()
		return latest, nil
	}
	apply(out)
	e.mu.Unlock()
	return out.AccessToken, nil
}

// adminTokenCtx returns a fresh cached admin access token without calling t.Fatalf (safe for t.Cleanup).
func (e *testEnv) adminTokenCtx(ctx context.Context) (string, error) {
	return e.ensureFreshToken(
		ctx,
		func() string { return e.adminToken },
		e.cfg.AdminEmail,
		e.cfg.AdminPassword,
		func(out LoginResponse) { e.adminToken = out.AccessToken },
	)
}

func (e *testEnv) refreshUserFixture(t *testing.T, u *userFixture) {
	t.Helper()
	if _, err := e.ensureFreshToken(
		e.ctx(t),
		func() string { return u.AccessToken },
		u.Email,
		u.Password,
		func(out LoginResponse) {
			u.AccessToken = out.AccessToken
			if out.RefreshToken != "" {
				u.RefreshToken = out.RefreshToken
			}
			if out.User.ID != "" {
				u.User = out.User
			}
		},
	); err != nil {
		t.Fatalf("refresh fixture token (%s): %v", u.Email, err)
	}
}

func (e *testEnv) adminLogin(t *testing.T) string {
	t.Helper()
	tok, err := e.adminTokenCtx(e.ctx(t))
	if err != nil {
		t.Fatalf("admin login: %v", err)
	}
	return tok
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
	for _, w := range wardOut.Wards {
		if w.CODSupported {
			chosenWard = w
			break
		}
	}
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
		UserID:        userID,
		Email:         email,
		Phone:         phone,
		Password:      password,
		AccessToken:   loginOut.AccessToken,
		RefreshToken:  loginOut.RefreshToken,
		PhoneVerified: loginOut.User.PhoneVerified,
		User:          loginOut.User,
	}
}

// verifyBuyerPhone completes real phone OTP verification via Mailpit once per account (E19).
// Subsequent calls for the same userFixture are no-ops so rate limits and Mailpit traffic stay minimal.
func (e *testEnv) verifyBuyerPhone(t *testing.T, u *userFixture) {
	t.Helper()
	if u == nil {
		t.Fatalf("verifyBuyerPhone called with nil userFixture")
	}
	if u.PhoneVerified {
		return
	}

	seen, err := e.mailpit.SnapshotRecipientMessageIDs(e.ctx(t), u.Email)
	if err != nil {
		t.Fatalf("snapshot Mailpit messages for %s before phone OTP: %v", u.Email, err)
	}

	otpReqResp := e.mustDoJSON(t, http.MethodPost, "/api/v1/users/me/phone/otp", u.AccessToken, nil, map[string]any{}, http.StatusOK)
	var otpReqOut struct {
		Sent              bool   `json:"sent"`
		ExpiresInSeconds  int32  `json:"expires_in_seconds"`
		RetryAfterSeconds int32  `json:"retry_after_seconds"`
		PhoneMasked       string `json:"phone_masked"`
	}
	if err := DecodeJSON(otpReqResp, &otpReqOut); err != nil {
		t.Fatalf("decode request phone otp (%s): %v", u.Email, err)
	}
	if !otpReqOut.Sent {
		t.Fatalf("request phone otp (%s) returned sent=false: %s", u.Email, string(otpReqResp.Body))
	}

	_, otp, err := e.mailpit.WaitForNewOTPMessage(e.ctx(t), u.Email, seen, e.cfg.PollTimeout)
	if err != nil {
		t.Fatalf("wait phone OTP from Mailpit for %s: %v", u.Email, err)
	}

	verifyResp := e.mustDoJSON(t, http.MethodPost, "/api/v1/users/me/phone/verify", u.AccessToken, nil, map[string]any{
		"otp": otp,
	}, http.StatusOK)
	var verifyOut struct {
		Verified bool    `json:"verified"`
		User     UserDTO `json:"user"`
	}
	if err := DecodeJSON(verifyResp, &verifyOut); err != nil {
		t.Fatalf("decode verify phone otp (%s): %v", u.Email, err)
	}
	if !verifyOut.Verified || !verifyOut.User.PhoneVerified {
		t.Fatalf("verify phone otp (%s) expected verified=true and user.phone_verified=true, got body=%s", u.Email, string(verifyResp.Body))
	}
	u.PhoneVerified = true
	u.User = verifyOut.User
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
		if e.seller1 == nil {
			e.seller1 = s1
		}
		s1 = e.seller1
		e.mu.Unlock()
	} else {
		e.refreshUserFixture(t, &s1.userFixture)
	}
	if s2 == nil {
		s2 = e.createApprovedSeller(t, "seller2")
		e.mu.Lock()
		if e.seller2 == nil {
			e.seller2 = s2
		}
		s2 = e.seller2
		e.mu.Unlock()
	} else {
		e.refreshUserFixture(t, &s2.userFixture)
	}
	return cat, s1, s2
}

// ensureVerifiedBuyer lazily creates and caches a shared buyer whose email OTP and phone OTP
// are verified once via Mailpit with a default COD-supported address so multiple tests can
// reuse it without adding repeated registration/OTP overhead to the E2E runtime budget.
func (e *testEnv) ensureVerifiedBuyer(t *testing.T) *userFixture {
	t.Helper()
	e.mu.Lock()
	b := e.verifiedBuyer
	e.mu.Unlock()
	if b != nil {
		e.refreshUserFixture(t, b)
		return b
	}

	b = e.registerVerifyAndLogin(t, "shared-verified-buyer")
	e.verifyBuyerPhone(t, b)
	e.addAddress(t, b)

	e.mu.Lock()
	if e.verifiedBuyer == nil {
		e.verifiedBuyer = b
	}
	res := e.verifiedBuyer
	e.mu.Unlock()
	e.refreshUserFixture(t, res)
	return res
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
		Images:      []string{"/products/ao-thun-cotton-trang.png"},
		SKUs: []SKUInputDTO{
			{
				SKUCode:     skuCode,
				Name:        "Default Variant",
				Attributes:  map[string]string{"edition": "standard"},
				PriceVND:    priceVND,
				Stock:       stock,
				WeightGrams: weightGrams,
				ImageURL:    "/products/ao-thun-cotton-xam-da.png",
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
	path := "/api/v1/vouchers?sales_channel=MARKETPLACE"
	if scope != "" {
		path += "&scope=" + url.QueryEscape(scope)
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

func TestJWTNeedsRefresh(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	makeJWT := func(payloadJSON string) string {
		hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
		pay := base64.RawURLEncoding.EncodeToString([]byte(payloadJSON))
		return hdr + "." + pay + ".sig"
	}

	cases := []struct {
		name string
		tok  string
		want bool
	}{
		{
			name: "6m remaining keeps cached token",
			tok:  makeJWT(fmt.Sprintf(`{"sub":"u1","exp":%d}`, now.Add(6*time.Minute).Unix())),
			want: false,
		},
		{
			name: "4m remaining refreshes token",
			tok:  makeJWT(fmt.Sprintf(`{"sub":"u1","exp":%d}`, now.Add(4*time.Minute).Unix())),
			want: true,
		},
		{
			name: "expired token refreshes",
			tok:  makeJWT(fmt.Sprintf(`{"sub":"u1","exp":%d}`, now.Add(-1*time.Minute).Unix())),
			want: true,
		},
		{
			name: "empty token refreshes",
			tok:  "",
			want: true,
		},
		{
			name: "malformed segments refresh",
			tok:  "not-a-jwt",
			want: true,
		},
		{
			name: "invalid base64 payload refreshes",
			tok:  "aaa.!!!invalid!!!.ccc",
			want: true,
		},
		{
			name: "missing exp claim refreshes",
			tok:  makeJWT(`{"sub":"u1"}`),
			want: true,
		},
		{
			name: "non-numeric exp claim refreshes",
			tok:  makeJWT(`{"sub":"u1","exp":"soon"}`),
			want: true,
		},
		{
			name: "zero exp claim refreshes",
			tok:  makeJWT(`{"sub":"u1","exp":0}`),
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := jwtNeedsRefresh(tc.tok, now, fixtureTokenRefreshThreshold); got != tc.want {
				t.Fatalf("jwtNeedsRefresh(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestClient429WithoutRetryAfterReturnsImmediately(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":"RESOURCE_EXHAUSTED","message":"FLASH_SOLD_OUT"}}`))
	}))
	defer srv.Close()

	c := NewClient(Config{GatewayURL: srv.URL, HTTPTimeout: 5 * time.Second})
	start := time.Now()
	resp, err := c.DoJSON(context.Background(), http.MethodPost, "/api/v1/checkout", "", nil, map[string]any{})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("DoJSON returned unexpected error: %v", err)
	}
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", resp.StatusCode)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("hits = %d, want 1 (must not retry 429 without Retry-After)", got)
	}
	if elapsed > time.Second {
		t.Fatalf("429 without Retry-After slept unexpectedly (%s)", elapsed)
	}
}

func TestW3B42RawHTTP429RetryAndNoRetry(t *testing.T) {
	origSleep := w3b42RawHTTPSleep
	t.Cleanup(func() { w3b42RawHTTPSleep = origSleep })

	t.Run("RetriesWithRetryAfterThenSucceeds", func(t *testing.T) {
		var hits atomic.Int32
		var sleeps []time.Duration
		w3b42RawHTTPSleep = func(_ context.Context, d time.Duration) error {
			sleeps = append(sleeps, d)
			return nil
		}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if hits.Add(1) <= 2 {
				w.Header().Set("Retry-After", "2")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error":{"code":"RATE_LIMITED"}}`))
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		}))
		defer srv.Close()

		code, _, body := w3b42RawHTTP(context.Background(), t, http.MethodGet, srv.URL+"/test", "", "", nil)
		if code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", code, string(body))
		}
		if got := hits.Load(); got != 3 {
			t.Fatalf("hits = %d, want 3 (2 retries + 1 success)", got)
		}
		if len(sleeps) != 2 || sleeps[0] != 2250*time.Millisecond || sleeps[1] != 2250*time.Millisecond {
			t.Fatalf("sleeps = %v, want two 2.25s waits", sleeps)
		}
	})

	t.Run("CapsAtFiveRetriesOnPersistent429", func(t *testing.T) {
		var hits atomic.Int32
		var sleepCount int
		w3b42RawHTTPSleep = func(_ context.Context, _ time.Duration) error {
			sleepCount++
			return nil
		}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hits.Add(1)
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"code":"RATE_LIMITED"}}`))
		}))
		defer srv.Close()

		code, _, _ := w3b42RawHTTP(context.Background(), t, http.MethodGet, srv.URL+"/test", "", "", nil)
		if code != http.StatusTooManyRequests {
			t.Fatalf("status = %d, want 429", code)
		}
		if got := hits.Load(); got != w3b42RawHTTPMax429Retries+1 {
			t.Fatalf("hits = %d, want %d (1 initial + %d retries)", got, w3b42RawHTTPMax429Retries+1, w3b42RawHTTPMax429Retries)
		}
		if sleepCount != w3b42RawHTTPMax429Retries {
			t.Fatalf("sleepCount = %d, want %d", sleepCount, w3b42RawHTTPMax429Retries)
		}
	})

	t.Run("NoRetryDoesNotRetryRateLimitTests", func(t *testing.T) {
		var hits atomic.Int32
		w3b42RawHTTPSleep = func(_ context.Context, _ time.Duration) error {
			t.Fatalf("w3b42RawHTTPNoRetry must not sleep")
			return nil
		}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hits.Add(1)
			w.Header().Set("Retry-After", "3")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"code":"RATE_LIMITED"}}`))
		}))
		defer srv.Close()

		code, _, _ := w3b42RawHTTPNoRetry(context.Background(), t, http.MethodGet, srv.URL+"/test", "", "", nil)
		if code != http.StatusTooManyRequests || hits.Load() != 1 {
			t.Fatalf("code=%d hits=%d, want 429 and 1 hit", code, hits.Load())
		}
	})
}
