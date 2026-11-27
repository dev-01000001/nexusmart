package e2e

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type w3b45ErrorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		TraceID string `json:"trace_id"`
	} `json:"error"`
}

type w3b45StaffFixture struct {
	UserID       string
	Email        string
	Role         string
	Password     string
	AccessToken  string
	RefreshToken string
}

type w3b45CreateStaffRequest struct {
	FullName string `json:"full_name"`
	Email    string `json:"email"`
	Phone    string `json:"phone"`
	Role     string `json:"role"`
}

type w3b45CreateStaffResponse struct {
	User              UserDTO `json:"user"`
	PasswordResetSent bool    `json:"password_reset_sent"`
}

type w3b45ShopApprovalDTO struct {
	ID            string `json:"id"`
	ShopID        string `json:"shop_id"`
	ShopName      string `json:"shop_name"`
	Status        string `json:"status"`
	Reason        string `json:"reason"`
	RequestedBy   string `json:"requested_by"`
	RequestedRole string `json:"requested_role"`
	DecidedBy     string `json:"decided_by"`
	DecidedNote   string `json:"decided_note"`
	CreatedAt     string `json:"created_at"`
	DecidedAt     string `json:"decided_at"`
}

type w3b45SuspendShopResponse struct {
	Approval w3b45ShopApprovalDTO `json:"approval"`
}

type w3b45DecideShopApprovalResponse struct {
	Approval w3b45ShopApprovalDTO `json:"approval"`
	Shop     ShopDTO              `json:"shop"`
}

type w3b45ApprovalsListResponse struct {
	ShopApprovals   []w3b45ShopApprovalDTO `json:"shop_approvals"`
	RefundApprovals []RefundDTO            `json:"refund_approvals"`
	ShopTotal       int64                  `json:"shop_total"`
	RefundTotal     int64                  `json:"refund_total"`
}

type w3b45ProductsListResponse struct {
	Items    []ProductDTO `json:"items"`
	Page     int32        `json:"page"`
	PageSize int32        `json:"page_size"`
	Total    int64        `json:"total"`
}

type w3b45UserActivityDTO struct {
	ID           string `json:"id"`
	UserID       string `json:"user_id"`
	EventType    string `json:"event_type"`
	IPAddress    string `json:"ip_address"`
	MetadataJSON string `json:"metadata_json"`
	CreatedAt    string `json:"created_at"`
}

type w3b45UserActivitiesResponse struct {
	Items            []w3b45UserActivityDTO `json:"items"`
	CODRejectedCount int32                  `json:"cod_rejected_count"`
	RecentOrders     []OrderDTO             `json:"recent_orders"`
	Page             int32                  `json:"page"`
	PageSize         int32                  `json:"page_size"`
	Total            int64                  `json:"total"`
}

type w3b45AuditLogDTO struct {
	ID         string `json:"id"`
	ActorID    string `json:"actor_id"`
	ActorRole  string `json:"actor_role"`
	Action     string `json:"action"`
	TargetType string `json:"target_type"`
	TargetID   string `json:"target_id"`
	Reason     string `json:"reason"`
	CreatedAt  string `json:"created_at"`
}

type w3b45AuditLogsResponse struct {
	Items    []w3b45AuditLogDTO `json:"items"`
	Page     int32              `json:"page"`
	PageSize int32              `json:"page_size"`
	Total    int64              `json:"total"`
}

type w3b45SettleRefundResponse struct {
	Refund         RefundDTO `json:"refund"`
	AlreadySettled bool      `json:"already_settled"`
}

type w3b45RefundListResponse struct {
	Items    []RefundDTO `json:"items"`
	Page     int32       `json:"page"`
	PageSize int32       `json:"page_size"`
	Total    int64       `json:"total"`
}

type w3b45ReturnRequestWrap struct {
	Order           OrderDTO `json:"order"`
	ReturnRequestID string   `json:"return_request_id"`
}

type w3b45ReturnDetailResponse struct {
	ReturnRequest struct {
		ID              string `json:"id"`
		OrderID         string `json:"order_id"`
		Status          string `json:"status"`
		RefundAmountVND int64  `json:"refund_amount_vnd"`
	} `json:"return_request"`
}

var w3b45ResetTokenRe = regexp.MustCompile(`dat-lai-mat-khau\?token=([A-Za-z0-9_-]+)`)

func w3b45ExtractResetToken(parts ...string) string {
	combined := strings.Join(parts, "\n")
	if m := w3b45ResetTokenRe.FindStringSubmatch(combined); len(m) == 2 {
		return m[1]
	}
	return ""
}

func w3b45AssertErrorCode(t *testing.T, label string, resp *RawResponse, wantCode string) {
	t.Helper()
	var envErr w3b45ErrorEnvelope
	if err := DecodeJSON(resp, &envErr); err != nil {
		t.Fatalf("%s: decode error envelope (HTTP %d): %v; body=%s", label, resp.StatusCode, err, string(resp.Body))
	}
	if envErr.Error.Code != wantCode {
		t.Fatalf("%s: error.code = %q, want %q (HTTP %d body=%s)", label, envErr.Error.Code, wantCode, resp.StatusCode, string(resp.Body))
	}
}

func w3b45FindAccount(summary LedgerSummaryResponse, accountCode string) AccountBalanceDTO {
	for _, acc := range summary.Accounts {
		if acc.AccountCode == accountCode {
			return acc
		}
	}
	return AccountBalanceDTO{}
}

func w3b45AssertLedgerBalanced(t *testing.T, env *testEnv) LedgerSummaryResponse {
	t.Helper()
	summary := env.getLedgerSummary(t)
	if !summary.IsBalanced || summary.TotalDebitsVND != summary.TotalCreditsVND {
		t.Fatalf("ledger unbalanced: is_balanced=%v debits=%d credits=%d",
			summary.IsBalanced, summary.TotalDebitsVND, summary.TotalCreditsVND)
	}
	if rp := w3b45FindAccount(summary, "refund_payable"); rp.BalanceVND < 0 {
		t.Fatalf("ledger invariant violation: refund_payable=%d < 0", rp.BalanceVND)
	}
	return summary
}

func w3b45DialPaymentGRPC(t *testing.T, env *testEnv) paymentv1.PaymentServiceClient {
	t.Helper()
	conn, err := grpc.NewClient(env.cfg.PaymentGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial PaymentService gRPC %s: %v", env.cfg.PaymentGRPCAddr, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return paymentv1.NewPaymentServiceClient(conn)
}

func w3b45AuthCtx(ctx context.Context, token string) context.Context {
	return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+token))
}

func w3b45HasShopVoucher(t *testing.T, env *testEnv, shopID, voucherID string) bool {
	t.Helper()
	path := fmt.Sprintf("/api/v1/vouchers?scope=SHOP&shop_id=%s&sales_channel=MARKETPLACE", url.QueryEscape(shopID))
	resp := env.mustDoJSON(t, http.MethodGet, path, "", nil, nil, http.StatusOK)
	var out VouchersListResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode GET %s: %v", path, err)
	}
	for _, v := range out.Vouchers {
		if v.ID == voucherID {
			return true
		}
	}
	return false
}

func w3b45HasFlashSaleItem(t *testing.T, env *testEnv, flashSaleItemID string) bool {
	t.Helper()
	resp := env.mustDoJSON(t, http.MethodGet, "/api/v1/flash-sales", "", nil, nil, http.StatusOK)
	var out FlashSalesListResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode GET /api/v1/flash-sales: %v", err)
	}
	for _, fs := range out.FlashSales {
		for _, it := range fs.Items {
			if it.ID == flashSaleItemID {
				return true
			}
		}
	}
	return false
}

// w3b45ProvisionStaff creates a passwordless internal staff account via POST /api/v1/admin/staff (F14),
// reads the reset-password link from Mailpit, calls POST /api/v1/auth/reset-password, logs in, and
// registers cleanup on parentT so no active ephemeral staff account remains after the test suite completes.
func w3b45ProvisionStaff(t, parentT *testing.T, env *testEnv, superTok, label, role string) *w3b45StaffFixture {
	t.Helper()
	email := env.uniqueEmail("w3b45-" + label)
	phone := env.uniquePhone()
	password := "StaffPass123!"

	createResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/staff", superTok, nil, w3b45CreateStaffRequest{
		FullName: "E2E Staff " + label,
		Email:    email,
		Phone:    phone,
		Role:     role,
	}, http.StatusCreated)
	var created w3b45CreateStaffResponse
	if err := DecodeJSON(createResp, &created); err != nil {
		t.Fatalf("decode create staff %s (%s): %v", label, role, err)
	}
	if created.User.ID == "" || !created.PasswordResetSent {
		t.Fatalf("create staff %s (%s) unexpected response: %s", label, role, string(createResp.Body))
	}
	if !StatusEquals(created.User.Role, role) || !StatusEquals(created.User.Status, "ACTIVE") {
		t.Fatalf("create staff %s: role=%q status=%q, want %q/ACTIVE", label, created.User.Role, created.User.Status, role)
	}

	// Always demote ephemeral staff back to buyer and lock on parentT exit so no active staff remains for later suites.
	staffID := created.User.ID
	parentT.Cleanup(func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cleanTok, tokErr := env.adminTokenCtx(cleanCtx)
		if tokErr != nil {
			parentT.Errorf("cleanup admin login for staff %s (%s) failed: %v", label, staffID, tokErr)
			return
		}
		rolePath := fmt.Sprintf("/api/v1/admin/users/%s/role", url.PathEscape(staffID))
		if resp, err := env.client.DoJSON(cleanCtx, http.MethodPatch, rolePath, cleanTok, nil, map[string]string{
			"role":   "buyer",
			"reason": "w3b45 cleanup demote ephemeral staff",
		}); err != nil || resp.StatusCode != http.StatusOK {
			parentT.Errorf("cleanup demote staff %s (%s) failed: err=%v resp=%+v", label, staffID, err, resp)
		}
		statusPath := fmt.Sprintf("/api/v1/admin/users/%s/status", url.PathEscape(staffID))
		if resp, err := env.client.DoJSON(cleanCtx, http.MethodPatch, statusPath, cleanTok, nil, map[string]string{
			"status": "LOCKED",
			"reason": "w3b45 cleanup lock ephemeral staff",
		}); err != nil || resp.StatusCode != http.StatusOK {
			parentT.Errorf("cleanup lock staff %s (%s) failed: err=%v resp=%+v", label, staffID, err, resp)
		}
	})

	mailMsg, err := env.mailpit.WaitForEmail(env.ctx(t), email, "dat-lai-mat-khau?token=", env.cfg.PollTimeout)
	if err != nil {
		t.Fatalf("wait password-reset email for staff %s (%s): %v", label, email, err)
	}
	resetToken := w3b45ExtractResetToken(mailMsg.Text, mailMsg.HTML)
	if resetToken == "" {
		t.Fatalf("failed to extract reset token from staff %s email subject=%q", label, mailMsg.Subject)
	}

	env.mustDoJSON(t, http.MethodPost, "/api/v1/auth/reset-password", "", nil, map[string]string{
		"token":        resetToken,
		"new_password": password,
	}, http.StatusOK)

	loginResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/auth/login", "", nil, LoginRequest{
		Email:    email,
		Password: password,
	}, http.StatusOK)
	var loginOut LoginResponse
	if err := DecodeJSON(loginResp, &loginOut); err != nil {
		t.Fatalf("decode staff login %s: %v", label, err)
	}
	if loginOut.AccessToken == "" || !StatusEquals(loginOut.User.Role, role) {
		t.Fatalf("staff login %s returned token_len=%d role=%q (want %q)", label, len(loginOut.AccessToken), loginOut.User.Role, role)
	}

	return &w3b45StaffFixture{
		UserID:       staffID,
		Email:        email,
		Role:         role,
		Password:     password,
		AccessToken:  loginOut.AccessToken,
		RefreshToken: loginOut.RefreshToken,
	}
}

func w3b45CreatePendingProduct(
	t *testing.T,
	env *testEnv,
	sellerTok string,
	categoryID string,
	name string,
	priceVND int64,
	stock int32,
) ProductDTO {
	t.Helper()
	resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/products", sellerTok, nil, CreateProductRequest{
		CategoryID:  categoryID,
		Name:        name,
		Description: "W3B2 A7 test product " + name,
		Images:      []string{"/products/ao-thun-cotton-trang.png"},
		SKUs: []SKUInputDTO{
			{
				SKUCode:     env.uniqueCode("A7SKU"),
				Name:        "Standard",
				Attributes:  map[string]string{"tier": "standard"},
				PriceVND:    priceVND,
				Stock:       stock,
				WeightGrams: 500,
				ImageURL:    "/products/ao-thun-cotton-xam-da.png",
			},
		},
	}, http.StatusCreated)
	var out ProductResponse
	if err := DecodeJSON(resp, &out); err != nil || out.Product.ID == "" || len(out.Product.SKUs) == 0 {
		t.Fatalf("create pending product %s: err=%v body=%s", name, err, string(resp.Body))
	}
	return out.Product
}

func w3b45HideProduct(t *testing.T, env *testEnv, sellerTok string, prod ProductDTO) {
	t.Helper()
	skus := make([]map[string]any, 0, len(prod.SKUs))
	for _, sk := range prod.SKUs {
		skus = append(skus, map[string]any{
			"id":           sk.ID,
			"sku_code":     sk.SKUCode,
			"name":         sk.Name,
			"attributes":   sk.Attributes,
			"price_vnd":    sk.PriceVND,
			"stock":        sk.Stock,
			"weight_grams": sk.WeightGrams,
		})
	}
	updatePath := fmt.Sprintf("/api/v1/seller/products/%s", url.PathEscape(prod.ID))
	resp := env.mustDoJSON(t, http.MethodPut, updatePath, sellerTok, nil, map[string]any{
		"category_id": prod.CategoryID,
		"name":        prod.Name,
		"description": prod.Description,
		"images":      []string{"/products/ao-thun-cotton-trang.png"},
		"status":      "HIDDEN",
		"skus":        skus,
	}, http.StatusOK)
	var out ProductResponse
	if err := DecodeJSON(resp, &out); err != nil || !StatusEquals(out.Product.Status, "HIDDEN") {
		t.Fatalf("hide product %s failed: err=%v status=%q", prod.ID, err, out.Product.Status)
	}
}

func w3b45PlaceAndConfirmVietQR(
	t *testing.T,
	env *testEnv,
	buyer *userFixture,
	seller *sellerFixture,
	sku SKUDTO,
	qty int32,
	quote CarrierQuoteDTO,
) CheckoutResponse {
	t.Helper()
	coResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
		"Idempotency-Key": "w3b45-vqr-" + uuid.NewString(),
	}, CheckoutRequest{
		SalesChannel:  "MARKETPLACE",
		AddressID:     buyer.AddressID,
		FromCart:      false,
		Items:         []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: qty}},
		PaymentMethod: "VIETQR",
		ShippingChoices: []ShopShippingChoiceDTO{
			{ShopID: seller.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
		},
	}, http.StatusCreated)
	var co CheckoutResponse
	if err := DecodeJSON(coResp, &co); err != nil || len(co.Orders) != 1 {
		t.Fatalf("decode VietQR checkout: err=%v body=%s", err, string(coResp.Body))
	}

	whResp, err := env.client.PostSignedWebhook(env.ctx(t), "/webhooks/payment", PaymentWebhookPayload{
		Provider:     "vietqr_mock",
		ProviderTxID: "W3B45-TX-" + uuid.NewString(),
		PaymentID:    co.PaymentID,
		OrderGroupID: co.CheckoutGroupID,
		AmountVND:    co.TotalPayableVND,
		Status:       "SUCCEEDED",
		Reference:    "NEXUSMART " + co.CheckoutGroupID,
		OccurredAt:   time.Now().UTC().Format(time.RFC3339),
	}, env.cfg.PaymentWebhookSecret, time.Now().UTC(), "")
	if err != nil || whResp.StatusCode != http.StatusOK {
		t.Fatalf("confirm VietQR webhook failed: err=%v resp=%+v", err, whResp)
	}

	ordID := co.Orders[0].ID
	env.pollUntil(t, env.cfg.PollTimeout, 250*time.Millisecond, "VietQR order CONFIRMED", func() (bool, string) {
		o := env.getOrder(t, buyer.AccessToken, ordID)
		return StatusEquals(o.Status, "CONFIRMED") && StatusEquals(o.PaymentStatus, "SUCCEEDED"),
			fmt.Sprintf("status=%s payment_status=%s", o.Status, o.PaymentStatus)
	})
	return co
}

func w3b45ShipAndDeliverOrder(
	t *testing.T,
	env *testEnv,
	seller *sellerFixture,
	buyer *userFixture,
	orderID string,
	quote CarrierQuoteDTO,
) {
	t.Helper()
	shipPath := fmt.Sprintf("/api/v1/seller/orders/%s/ship", url.PathEscape(orderID))
	shipResp := env.mustDoJSON(t, http.MethodPost, shipPath, seller.AccessToken, nil, SellerShipRequest{
		CarrierCode: quote.CarrierCode,
		ServiceCode: quote.ServiceCode,
	}, http.StatusOK)
	var shipOut SellerShipResponse
	if err := DecodeJSON(shipResp, &shipOut); err != nil || shipOut.TrackingCode == "" {
		t.Fatalf("ship order %s: err=%v body=%s", orderID, err, string(shipResp.Body))
	}

	whShipResp, err := env.client.PostSignedWebhook(env.ctx(t), "/webhooks/shipping/"+url.PathEscape(quote.CarrierCode), ShippingWebhookPayload{
		EventID:      "w3b45-deliv-" + uuid.NewString(),
		TrackingCode: shipOut.TrackingCode,
		OrderID:      orderID,
		RawStatus:    "delivered",
		Description:  "Delivered for w3b45 return test",
		OccurredAt:   time.Now().UTC().Format(time.RFC3339),
	}, env.cfg.ShippingWebhookSecret, time.Now().UTC(), "")
	if err != nil || whShipResp.StatusCode != http.StatusOK {
		t.Fatalf("deliver webhook for %s failed: err=%v resp=%+v", orderID, err, whShipResp)
	}

	env.pollUntil(t, env.cfg.PollTimeout, 250*time.Millisecond, "order "+orderID+" DELIVERED", func() (bool, string) {
		o := env.getOrder(t, buyer.AccessToken, orderID)
		return StatusEquals(o.Status, "DELIVERED"), o.Status
	})
}

func TestE2E_W3B2_A7_AdminRBACApprovalsAndAudit(t *testing.T) {
	parentT := t
	env := getEnv()
	superTok := env.adminLogin(t)
	cat, seller1, _ := env.ensureBaseMarketplace(t)
	verifiedBuyer := env.ensureVerifiedBuyer(t)
	testStart := time.Now().UTC().Add(-2 * time.Second)

	quoteSeller1 := env.calculateShippingQuote(t, verifiedBuyer.AccessToken, seller1.ShopID, 500, 0)

	// Shared staff accounts provisioned passwordlessly via Mailpit reset-password (F10, F14).
	var (
		regularAdmin *w3b45StaffFixture
		moderator    *w3b45StaffFixture
		cskh         *w3b45StaffFixture
		accountant   *w3b45StaffFixture
		ops1         *w3b45StaffFixture
		ops2         *w3b45StaffFixture

		activityBuyer        *userFixture
		activityOrderID      string
		suspendSeller        *sellerFixture
		suspendProdAppr      ProductDTO
		largeRefundPaymentID string
		largeRefundID        string
		largeRefundSKU       SKUDTO
		returnProductSKU     SKUDTO
	)

	thresholdVND := env.cfg.LargeRefundApprovalVND
	if thresholdVND <= 0 {
		thresholdVND = 5000000
	}
	largeAmtVND := int64(6000000)
	if thresholdVND >= largeAmtVND {
		largeAmtVND = thresholdVND + 1000000
	}
	splitAmtVND := int64(3000000)
	if thresholdVND != 5000000 {
		splitAmtVND = (thresholdVND / 2) + 500000
		if splitAmtVND >= thresholdVND {
			splitAmtVND = thresholdVND - 100000
		}
	}
	largeSKUPrice := int64(6500000)
	if need := largeAmtVND + 500000; need > largeSKUPrice {
		largeSKUPrice = need
	}
	if need := splitAmtVND*2 + 500000; need > largeSKUPrice {
		largeSKUPrice = need
	}

	_, returnProductSKU = env.createApprovedProduct(t, seller1, cat.ID, "W3B2 A7 Return Item", 120000, 30, 400)
	_, largeRefundSKU = env.createApprovedProduct(t, seller1, cat.ID, "W3B2 A7 Large Refund Item", largeSKUPrice, 20, 500)

	// 1. Passwordless staff creation & 6-role RBAC positive/negative matrix (F9, F9-bis, F10, F14).
	t.Run("1_StaffPasswordlessProvisioning_And_RBACMatrix", func(t *testing.T) {
		regularAdmin = w3b45ProvisionStaff(t, parentT, env, superTok, "admin", "admin")
		moderator = w3b45ProvisionStaff(t, parentT, env, superTok, "mod", "moderator")
		cskh = w3b45ProvisionStaff(t, parentT, env, superTok, "cskh", "cskh")
		accountant = w3b45ProvisionStaff(t, parentT, env, superTok, "acct", "accountant")
		ops1 = w3b45ProvisionStaff(t, parentT, env, superTok, "ops1", "ops")
		ops2 = w3b45ProvisionStaff(t, parentT, env, superTok, "ops2", "ops")

		activityBuyer = env.registerVerifyAndLogin(t, "w3b45-actbuyer")
		env.addAddress(t, activityBuyer)

		// Dedicated test-owned shop (never touch seed shops or env.seller1/seller2): register as PENDING,
		// then approve via moderator (verifies moderator shops.review=200 and feeds Scenario 2).
		suspendOwner := env.registerVerifyAndLogin(t, "w3b45-susowner")
		suspendAddr := env.addAddress(t, suspendOwner)
		shopName := fmt.Sprintf("W3B45-SuspendShop-%s-%d", env.runID, env.nextSeq())
		shopRegResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/shops/register", suspendOwner.AccessToken, nil, RegisterShopRequest{
			Name:               shopName,
			Description:        "Dedicated E2E shop for 2-person suspension test",
			PickupAddressID:    suspendAddr.ID,
			ContactPhone:       suspendOwner.Phone,
			BusinessLicenseDoc: "https://sandbox.nexusmart.local/docs/w3b45-license.pdf",
		}, http.StatusCreated)
		var shopRegOut ShopResponse
		if err := DecodeJSON(shopRegResp, &shopRegOut); err != nil || shopRegOut.Shop.ID == "" {
			t.Fatalf("register dedicated shop: err=%v body=%s", err, string(shopRegResp.Body))
		}

		// (a) regularAdmin (admin) cannot create staff or change user roles -> 403 (F10).
		env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/staff", regularAdmin.AccessToken, nil, w3b45CreateStaffRequest{
			FullName: "Unauthorized Staff",
			Email:    env.uniqueEmail("w3b45-denied-staff"),
			Phone:    env.uniquePhone(),
			Role:     "ops",
		}, http.StatusForbidden)
		env.mustDoJSON(t, http.MethodPatch, fmt.Sprintf("/api/v1/admin/users/%s/role", url.PathEscape(activityBuyer.UserID)),
			regularAdmin.AccessToken, nil, map[string]string{
				"role":   "ops",
				"reason": "admin must not change roles",
			}, http.StatusForbidden)
		// super_admin cannot be assigned via PATCH /admin/users/{id}/role -> strictly 400 INVALID_ARGUMENT (gateway r1 + user p4).
		assignSuperResp := env.mustDoJSON(t, http.MethodPatch, fmt.Sprintf("/api/v1/admin/users/%s/role", url.PathEscape(activityBuyer.UserID)),
			superTok, nil, map[string]string{
				"role":   "super_admin",
				"reason": "super_admin assignment must be rejected with 400",
			}, http.StatusBadRequest)
		w3b45AssertErrorCode(t, "PATCH role super_admin", assignSuperResp, "INVALID_ARGUMENT")

		// Overflowing page query parameter -> strictly 400 INVALID_ARGUMENT (gateway r1).
		for _, overflowPath := range []string{
			"/api/v1/admin/users?page=2147483648&page_size=20",
			"/api/v1/admin/users?page=200000000&page_size=20",
		} {
			ovResp := env.mustDoJSON(t, http.MethodGet, overflowPath, superTok, nil, nil, http.StatusBadRequest)
			w3b45AssertErrorCode(t, "overflow pagination "+overflowPath, ovResp, "INVALID_ARGUMENT")
		}
		env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/users?page_size=1", regularAdmin.AccessToken, nil, nil, http.StatusOK)

		// (b) moderator: approve shop (200) & approve product (200); denied settle/coins/carrier/orders (403).
		shopReviewPath := fmt.Sprintf("/api/v1/admin/shops/%s/review", url.PathEscape(shopRegOut.Shop.ID))
		modShopResp := env.mustDoJSON(t, http.MethodPost, shopReviewPath, moderator.AccessToken, nil, map[string]string{
			"decision":            "APPROVE",
			"reason":              "moderator approves test shop",
			"expected_owner_name": suspendOwner.User.FullName,
		}, http.StatusOK)
		var modShopOut ShopResponse
		if err := DecodeJSON(modShopResp, &modShopOut); err != nil || !StatusEquals(modShopOut.Shop.Status, "ACTIVE") {
			t.Fatalf("moderator approve shop: err=%v body=%s", err, string(modShopResp.Body))
		}

		// Re-login suspendOwner so JWT carries role=seller + shop_id.
		reLoginResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/auth/login", "", nil, LoginRequest{
			Email:    suspendOwner.Email,
			Password: suspendOwner.Password,
		}, http.StatusOK)
		var reLoginOut LoginResponse
		if err := DecodeJSON(reLoginResp, &reLoginOut); err != nil || reLoginOut.AccessToken == "" {
			t.Fatalf("relogin dedicated seller: err=%v body=%s", err, string(reLoginResp.Body))
		}
		suspendOwner.AccessToken = reLoginOut.AccessToken
		suspendOwner.RefreshToken = reLoginOut.RefreshToken
		suspendOwner.User = reLoginOut.User
		suspendSeller = &sellerFixture{
			userFixture: *suspendOwner,
			ShopID:      shopRegOut.Shop.ID,
			ShopName:    shopName,
		}

		pendingProd := w3b45CreatePendingProduct(t, env, suspendSeller.AccessToken, cat.ID, "W3B2 A7 Approved Item", 150000, 30)
		prodReviewPath := fmt.Sprintf("/api/v1/admin/products/%s/review", url.PathEscape(pendingProd.ID))
		modProdResp := env.mustDoJSON(t, http.MethodPost, prodReviewPath, moderator.AccessToken, nil, ReviewRequest{
			Decision: "APPROVE",
			Reason:   "moderator approves product",
		}, http.StatusOK)
		var modProdOut ProductResponse
		if err := DecodeJSON(modProdResp, &modProdOut); err != nil || !StatusEquals(modProdOut.Product.Status, "APPROVED") {
			t.Fatalf("moderator approve product: err=%v body=%s", err, string(modProdResp.Body))
		}
		suspendProdAppr = modProdOut.Product

		const dummyUUID = "00000000-0000-0000-0000-000000000001"
		env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/refunds/"+dummyUUID+"/settle", moderator.AccessToken, map[string]string{
			"Idempotency-Key": "w3b45-mod-settle-" + uuid.NewString(),
		}, map[string]string{"provider_refund_id": "MOD-DENY", "note": "deny"}, http.StatusForbidden)
		env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/coins/credit", moderator.AccessToken, nil, CreditCoinsRequest{
			UserID:      activityBuyer.UserID,
			Amount:      1000,
			Reason:      "mod deny",
			ReferenceID: "w3b45-mod-coin-" + uuid.NewString(),
		}, http.StatusForbidden)
		env.mustDoJSON(t, http.MethodPut, "/api/v1/admin/carriers/mock_express", moderator.AccessToken, nil, map[string]any{
			"enabled": true,
		}, http.StatusForbidden)
		env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/orders?page_size=1", moderator.AccessToken, nil, nil, http.StatusForbidden)

		// (c) cskh: cannot lock staff/admin (403); CAN lock/unlock buyer (200); CAN resolve return (200);
		//     denied fee config & credit coins (403, F9-bis).
		env.mustDoJSON(t, http.MethodPatch, fmt.Sprintf("/api/v1/admin/users/%s/status", url.PathEscape(moderator.UserID)),
			cskh.AccessToken, nil, map[string]string{"status": "LOCKED", "reason": "cskh must not lock staff"}, http.StatusForbidden)
		env.mustDoJSON(t, http.MethodPatch, fmt.Sprintf("/api/v1/admin/users/%s/status", url.PathEscape(regularAdmin.UserID)),
			cskh.AccessToken, nil, map[string]string{"status": "LOCKED", "reason": "cskh must not lock admin"}, http.StatusForbidden)

		buyerStatusPath := fmt.Sprintf("/api/v1/admin/users/%s/status", url.PathEscape(activityBuyer.UserID))
		env.mustDoJSON(t, http.MethodPatch, buyerStatusPath, cskh.AccessToken, nil, map[string]string{
			"status": "LOCKED",
			"reason": "w3b45 cskh temporary lock",
		}, http.StatusOK)
		env.mustDoJSON(t, http.MethodPatch, buyerStatusPath, cskh.AccessToken, nil, map[string]string{
			"status": "ACTIVE",
			"reason": "w3b45 cskh unlock buyer",
		}, http.StatusOK)
		// Re-login activityBuyer after lock/unlock (also generates fresh login_succeeded activity for Scenario 4).
		buyerReLogin := env.mustDoJSON(t, http.MethodPost, "/api/v1/auth/login", "", nil, LoginRequest{
			Email:    activityBuyer.Email,
			Password: activityBuyer.Password,
		}, http.StatusOK)
		var buyerReLoginOut LoginResponse
		if err := DecodeJSON(buyerReLogin, &buyerReLoginOut); err != nil || buyerReLoginOut.AccessToken == "" {
			t.Fatalf("relogin activityBuyer after unlock: err=%v", err)
		}
		activityBuyer.AccessToken = buyerReLoginOut.AccessToken
		activityBuyer.RefreshToken = buyerReLoginOut.RefreshToken

		// Real escalated return resolved by cskh (200); order also populates recent_orders in Scenario 4.
		retCo := w3b45PlaceAndConfirmVietQR(t, env, activityBuyer, seller1, returnProductSKU, 1, quoteSeller1)
		activityOrderID = retCo.Orders[0].ID
		w3b45ShipAndDeliverOrder(t, env, seller1, activityBuyer, activityOrderID, quoteSeller1)

		reqRetResp := env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/orders/%s/return", url.PathEscape(activityOrderID)),
			activityBuyer.AccessToken, nil, map[string]any{"reason": "w3b45 buyer return test"}, http.StatusOK)
		var reqRetOut w3b45ReturnRequestWrap
		if err := DecodeJSON(reqRetResp, &reqRetOut); err != nil || reqRetOut.ReturnRequestID == "" {
			t.Fatalf("request return: err=%v body=%s", err, string(reqRetResp.Body))
		}
		retID := reqRetOut.ReturnRequestID
		env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/seller/returns/%s/respond", url.PathEscape(retID)),
			seller1.AccessToken, nil, map[string]any{"decision": "REJECT", "note": "w3b45 seller reject"}, http.StatusOK)
		env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/returns/%s/escalate", url.PathEscape(retID)),
			activityBuyer.AccessToken, nil, map[string]any{"reason": "w3b45 escalate to cskh"}, http.StatusOK)
		env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/admin/returns/%s/resolve", url.PathEscape(retID)),
			cskh.AccessToken, nil, map[string]any{"decision": "REJECT", "note": "w3b45 cskh resolved"}, http.StatusOK)

		env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/fees/payment", cskh.AccessToken, nil, map[string]any{
			"payment_method": "VIETQR",
			"fee_bps":        50,
			"note":           "cskh deny",
		}, http.StatusForbidden)
		env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/admin/fees/categories/%s", url.PathEscape(cat.ID)), cskh.AccessToken, nil, map[string]any{
			"commission_rate_bps": 500,
			"note":                "cskh deny",
		}, http.StatusForbidden)
		env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/coins/credit", cskh.AccessToken, nil, CreditCoinsRequest{
			UserID:      activityBuyer.UserID,
			Amount:      1000,
			Reason:      "cskh deny F9-bis",
			ReferenceID: "w3b45-cskh-coin-" + uuid.NewString(),
		}, http.StatusForbidden)

		// (d) accountant: reports/ledger/reconciliation/credit coins -> 200; product review -> 403.
		vnLoc := time.FixedZone("ICT", 7*3600)
		today := time.Now().In(vnLoc).Format("2006-01-02")
		env.mustDoJSON(t, http.MethodGet, fmt.Sprintf("/api/v1/admin/reports?from_date=%s&to_date=%s", today, today),
			accountant.AccessToken, nil, nil, http.StatusOK)
		env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/ledger/summary", accountant.AccessToken, nil, nil, http.StatusOK)
		env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/reconciliation/queue?page=1&page_size=5", accountant.AccessToken, nil, nil, http.StatusOK)
		env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/payments/reconcile", accountant.AccessToken, nil, map[string]string{
			"date": today,
		}, http.StatusOK)
		env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/coins/credit", accountant.AccessToken, nil, CreditCoinsRequest{
			UserID:      activityBuyer.UserID,
			Amount:      1000,
			Reason:      "w3b45 accountant credit F9-bis",
			ReferenceID: "w3b45-acct-coin-" + uuid.NewString(),
		}, http.StatusOK)
		env.mustDoJSON(t, http.MethodPost, prodReviewPath, accountant.AccessToken, nil, ReviewRequest{
			Decision: "APPROVE",
			Reason:   "accountant deny product review",
		}, http.StatusForbidden)

		// (e) ops: credit coins -> 403 (F9-bis); carriers/payment-methods -> 200; product review -> 403.
		//     (No gateway REST route exists for MarkPaymentResult or admin place-order-on-behalf.)
		env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/coins/credit", ops1.AccessToken, nil, CreditCoinsRequest{
			UserID:      activityBuyer.UserID,
			Amount:      1000,
			Reason:      "ops deny F9-bis",
			ReferenceID: "w3b45-ops-coin-" + uuid.NewString(),
		}, http.StatusForbidden)
		env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/carriers", ops1.AccessToken, nil, nil, http.StatusOK)
		env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/payment-methods", ops1.AccessToken, nil, nil, http.StatusOK)
		env.mustDoJSON(t, http.MethodPost, prodReviewPath, ops1.AccessToken, nil, ReviewRequest{
			Decision: "APPROVE",
			Reason:   "ops deny product review",
		}, http.StatusForbidden)

		// (f) ops / cskh / accountant cannot call CancelPayment on behalf of another buyer (payment p4 Item L).
		//     Gateway exposes no REST route for CancelPayment, so verify directly at PaymentService gRPC.
		cancelTargetResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", activityBuyer.AccessToken, map[string]string{
			"Idempotency-Key": "w3b45-cancel-rbac-" + uuid.NewString(),
		}, CheckoutRequest{
			SalesChannel:  "MARKETPLACE",
			AddressID:     activityBuyer.AddressID,
			FromCart:      false,
			Items:         []CheckoutItemInputDTO{{SKUID: returnProductSKU.ID, Quantity: 1}},
			PaymentMethod: "VIETQR",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: quoteSeller1.CarrierCode, ServiceCode: quoteSeller1.ServiceCode},
			},
		}, http.StatusCreated)
		var cancelTargetCo CheckoutResponse
		if err := DecodeJSON(cancelTargetResp, &cancelTargetCo); err != nil || cancelTargetCo.PaymentID == "" || len(cancelTargetCo.Orders) != 1 {
			t.Fatalf("create pending payment for CancelPayment RBAC check: err=%v body=%s", err, string(cancelTargetResp.Body))
		}
		defer func() {
			cleanCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			_, _ = env.client.DoJSON(cleanCtx, http.MethodPost,
				fmt.Sprintf("/api/v1/orders/%s/cancel", url.PathEscape(cancelTargetCo.Orders[0].ID)),
				activityBuyer.AccessToken, nil, map[string]string{"reason": "w3b45 cleanup cancel RBAC order"})
		}()

		payGRPC := w3b45DialPaymentGRPC(t, env)
		for _, staff := range []*w3b45StaffFixture{ops1, cskh, accountant} {
			_, cancelErr := payGRPC.CancelPayment(w3b45AuthCtx(env.ctx(t), staff.AccessToken), &paymentv1.CancelPaymentRequest{
				PaymentId:    cancelTargetCo.PaymentID,
				OrderGroupId: cancelTargetCo.CheckoutGroupID,
				Reason:       "staff cancel on behalf must fail",
			})
			if status.Code(cancelErr) != codes.PermissionDenied {
				t.Fatalf("%s CancelPayment on behalf of buyer: got %v, want PermissionDenied", staff.Role, cancelErr)
			}
		}
	})

	// 2. Two-person shop suspension & unsuspend (F11-F12) on dedicated test shop ONLY.
	t.Run("2_TwoPersonShopSuspensionAndUnsuspend", func(t *testing.T) {
		if suspendSeller == nil || ops1 == nil || ops2 == nil || moderator == nil {
			t.Fatalf("prerequisite subtest 1 fixtures not initialized")
		}
		shopID := suspendSeller.ShopID
		shopIsSuspended := false

		// Safety cleanup: unsuspend dedicated test shop and clear verifiedBuyer cart even if subtest fails.
		t.Cleanup(func() {
			cleanCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			_, _ = env.client.DoJSON(cleanCtx, http.MethodDelete, "/api/v1/cart", verifiedBuyer.AccessToken, nil, nil)
			if !shopIsSuspended {
				return
			}
			cleanTok, tokErr := env.adminTokenCtx(cleanCtx)
			if tokErr != nil {
				t.Errorf("cleanup admin login for unsuspend shop %s failed: %v", shopID, tokErr)
				return
			}
			if resp, err := env.client.DoJSON(cleanCtx, http.MethodPost, fmt.Sprintf("/api/v1/admin/shops/%s/unsuspend", url.PathEscape(shopID)),
				cleanTok, nil, map[string]string{"reason": "w3b45 cleanup unsuspend"}); err != nil || resp.StatusCode != http.StatusOK {
				t.Errorf("cleanup unsuspend dedicated shop %s failed: err=%v resp=%+v", shopID, err, resp)
			}
		})

		// Create a second product that the seller explicitly hides BEFORE suspension -> must stay HIDDEN after unsuspend.
		pendingHidden := w3b45CreatePendingProduct(t, env, suspendSeller.AccessToken, cat.ID, "W3B2 A7 Seller Hidden Item", 160000, 15)
		env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/admin/products/%s/review", url.PathEscape(pendingHidden.ID)),
			moderator.AccessToken, nil, ReviewRequest{Decision: "APPROVE", Reason: "approve before seller hide"}, http.StatusOK)
		w3b45HideProduct(t, env, suspendSeller.AccessToken, pendingHidden)
		env.mustDoJSON(t, http.MethodGet, fmt.Sprintf("/api/v1/products/%s", url.PathEscape(pendingHidden.ID)), "", nil, nil, http.StatusNotFound)
		env.mustDoJSON(t, http.MethodGet, fmt.Sprintf("/api/v1/products/%s", url.PathEscape(suspendProdAppr.ID)), "", nil, nil, http.StatusOK)

		// Create shop voucher & active flash sale item BEFORE suspension (promotion r1 visibility + SaveVoucher checks).
		suspendVoucher := env.createShopVoucher(t, suspendSeller, 15000, 100000, 10)
		now := time.Now().UTC()
		fsResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/flash-sales", superTok, nil, CreateFlashSaleRequest{
			Name:     "W3B45 Suspend Shop Flash " + env.runID,
			StartsAt: now.Add(-5 * time.Minute).Format(time.RFC3339),
			EndsAt:   now.Add(45 * time.Minute).Format(time.RFC3339),
		}, http.StatusCreated)
		var fsOut FlashSaleResponse
		if err := DecodeJSON(fsResp, &fsOut); err != nil || fsOut.FlashSale.ID == "" {
			t.Fatalf("create flash sale for suspended shop test: err=%v body=%s", err, string(fsResp.Body))
		}
		regFSResp := env.mustDoJSON(t, http.MethodPost,
			fmt.Sprintf("/api/v1/seller/flash-sales/%s/items", url.PathEscape(fsOut.FlashSale.ID)),
			suspendSeller.AccessToken, nil, RegisterFlashSaleItemRequest{
				SKUID:         suspendProdAppr.SKUs[0].ID,
				FlashPriceVND: 120000,
				TotalSlots:    10,
				PerUserLimit:  3,
			}, http.StatusCreated)
		var regFSOut FlashSaleItemResponse
		if err := DecodeJSON(regFSResp, &regFSOut); err != nil || regFSOut.Item.ID == "" {
			t.Fatalf("register flash sale item for suspended shop: err=%v body=%s", err, string(regFSResp.Body))
		}
		suspendFlashItemID := regFSOut.Item.ID
		env.mustDoJSON(t, http.MethodPost,
			fmt.Sprintf("/api/v1/admin/flash-sale-items/%s/approve", url.PathEscape(suspendFlashItemID)),
			superTok, nil, ApproveFlashSaleItemRequest{
				Approve: true,
				Reason:  "w3b45 approve flash sale before suspension",
			}, http.StatusOK)

		if !w3b45HasShopVoucher(t, env, shopID, suspendVoucher.ID) {
			t.Fatalf("shop voucher %s must be visible in GET /api/v1/vouchers before suspension", suspendVoucher.ID)
		}
		if !w3b45HasFlashSaleItem(t, env, suspendFlashItemID) {
			t.Fatalf("flash sale item %s must be visible in GET /api/v1/flash-sales before suspension", suspendFlashItemID)
		}

		// Add suspended shop's SKU to verifiedBuyer's cart BEFORE suspension so we can test from_cart=true after product hides SKU.
		env.mustDoJSON(t, http.MethodDelete, "/api/v1/cart", verifiedBuyer.AccessToken, nil, nil, http.StatusOK)
		env.mustDoJSON(t, http.MethodPost, "/api/v1/cart/items", verifiedBuyer.AccessToken, nil, AddCartItemRequest{
			SKUID:    suspendProdAppr.SKUs[0].ID,
			Quantity: 1,
		}, http.StatusOK)

		// Place an in-flight COD order against the shop BEFORE suspension -> must remain shippable while SUSPENDED.
		susQuote := env.calculateShippingQuote(t, verifiedBuyer.AccessToken, shopID, 500, 0)
		inFlightResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", verifiedBuyer.AccessToken, map[string]string{
			"Idempotency-Key": "w3b45-inflight-" + uuid.NewString(),
		}, CheckoutRequest{
			SalesChannel:  "MARKETPLACE",
			AddressID:     verifiedBuyer.AddressID,
			Items:         []CheckoutItemInputDTO{{SKUID: suspendProdAppr.SKUs[0].ID, Quantity: 1}},
			PaymentMethod: "COD",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: shopID, CarrierCode: susQuote.CarrierCode, ServiceCode: susQuote.ServiceCode},
			},
		}, http.StatusCreated)
		var inFlightCo CheckoutResponse
		if err := DecodeJSON(inFlightResp, &inFlightCo); err != nil || len(inFlightCo.Orders) != 1 {
			t.Fatalf("create in-flight order before suspension: err=%v body=%s", err, string(inFlightResp.Body))
		}
		inFlightOrderID := inFlightCo.Orders[0].ID

		oldSellerAccess := suspendSeller.AccessToken
		oldSellerRefresh := suspendSeller.RefreshToken

		// Step 1: ops1 requests suspension -> 201 PENDING, shop remains ACTIVE.
		suspendPath := fmt.Sprintf("/api/v1/admin/shops/%s/suspend", url.PathEscape(shopID))
		reqSusResp := env.mustDoJSON(t, http.MethodPost, suspendPath, ops1.AccessToken, nil, map[string]string{
			"reason": "w3b45 policy violation investigation",
		}, http.StatusCreated)
		var reqSusOut w3b45SuspendShopResponse
		if err := DecodeJSON(reqSusResp, &reqSusOut); err != nil || reqSusOut.Approval.ID == "" {
			t.Fatalf("decode suspend shop request: err=%v body=%s", err, string(reqSusResp.Body))
		}
		if !StatusEquals(reqSusOut.Approval.Status, "PENDING") || reqSusOut.Approval.RequestedBy != ops1.UserID {
			t.Fatalf("unexpected shop suspension approval: %+v", reqSusOut.Approval)
		}
		approvalID := reqSusOut.Approval.ID

		// Shop MUST still be ACTIVE before checker approves.
		pubShopBefore := env.mustDoJSON(t, http.MethodGet, fmt.Sprintf("/api/v1/shops/%s", url.PathEscape(shopID)), "", nil, nil, http.StatusOK)
		var shopBeforeOut ShopResponse
		if err := DecodeJSON(pubShopBefore, &shopBeforeOut); err != nil || !StatusEquals(shopBeforeOut.Shop.Status, "ACTIVE") {
			t.Fatalf("shop must remain ACTIVE before 2nd approval: err=%v status=%q", err, shopBeforeOut.Shop.Status)
		}

		// Unified approvals queue shows the pending shop approval to ops2.
		appListResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/approvals?type=shop&status=PENDING&page=1&page_size=50",
			ops2.AccessToken, nil, nil, http.StatusOK)
		var appList w3b45ApprovalsListResponse
		if err := DecodeJSON(appListResp, &appList); err != nil {
			t.Fatalf("decode admin approvals list: %v", err)
		}
		foundApproval := false
		for _, a := range appList.ShopApprovals {
			if a.ID == approvalID && a.ShopID == shopID {
				foundApproval = true
				break
			}
		}
		if !foundApproval {
			t.Fatalf("pending shop approval %s not found in GET /api/v1/admin/approvals: %+v", approvalID, appList.ShopApprovals)
		}

		// Step 2: ops1 self-approve -> 403 MAKER_CHECKER_CONFLICT; moderator lacks decide capability -> 403.
		decidePath := fmt.Sprintf("/api/v1/admin/approvals/shop/%s/decide", url.PathEscape(approvalID))
		selfDecideResp := env.mustDoJSON(t, http.MethodPost, decidePath, ops1.AccessToken, nil, map[string]string{
			"decision": "APPROVE",
			"note":     "ops1 self-approve must fail",
		}, http.StatusForbidden)
		w3b45AssertErrorCode(t, "ops1 self-approve shop suspension", selfDecideResp, "MAKER_CHECKER_CONFLICT")

		env.mustDoJSON(t, http.MethodPost, decidePath, moderator.AccessToken, nil, map[string]string{
			"decision": "APPROVE",
			"note":     "moderator cannot decide suspension",
		}, http.StatusForbidden)

		// Step 3: ops2 approves -> shop becomes SUSPENDED.
		decideResp := env.mustDoJSON(t, http.MethodPost, decidePath, ops2.AccessToken, nil, map[string]string{
			"decision": "APPROVE",
			"note":     "ops2 verified violation",
		}, http.StatusOK)
		var decideOut w3b45DecideShopApprovalResponse
		if err := DecodeJSON(decideResp, &decideOut); err != nil {
			t.Fatalf("decode ops2 decide shop suspension: %v", err)
		}
		if !StatusEquals(decideOut.Approval.Status, "APPROVED") || decideOut.Approval.DecidedBy != ops2.UserID {
			t.Fatalf("unexpected approved record: %+v", decideOut.Approval)
		}
		if !StatusEquals(decideOut.Shop.Status, "SUSPENDED") {
			t.Fatalf("expected shop status SUSPENDED, got %q", decideOut.Shop.Status)
		}
		shopIsSuspended = true

		// Step 4: Seller's old access & refresh tokens are immediately revoked (F12).
		env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/shop", oldSellerAccess, nil, nil, http.StatusUnauthorized)
		env.mustDoJSON(t, http.MethodPost, "/api/v1/auth/refresh", "", nil, RefreshRequest{
			RefreshToken: oldSellerRefresh,
		}, http.StatusUnauthorized)

		// Step 5a: Products hidden asynchronously from public GET /api/v1/products (via user.shop_status_changed).
		env.pollUntil(t, env.cfg.PollTimeout, 250*time.Millisecond, "suspended shop products hidden from public catalog", func() (bool, string) {
			listResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/products?shop_id="+url.QueryEscape(shopID), "", nil, nil, http.StatusOK)
			var listOut w3b45ProductsListResponse
			if err := DecodeJSON(listResp, &listOut); err != nil {
				return false, err.Error()
			}
			for _, p := range listOut.Items {
				if p.ID == suspendProdAppr.ID {
					return false, fmt.Sprintf("product %s still visible in public list (total=%d)", p.ID, listOut.Total)
				}
			}
			return listOut.Total == 0, fmt.Sprintf("public list total=%d", listOut.Total)
		})
		env.mustDoJSON(t, http.MethodGet, fmt.Sprintf("/api/v1/products/%s", url.PathEscape(suspendProdAppr.ID)), "", nil, nil, http.StatusNotFound)

		// Step 5b: Shop vouchers & flash sales hidden asynchronously via promotion consumer (promotion r1).
		env.pollUntil(t, env.cfg.PollTimeout, 250*time.Millisecond, "suspended shop voucher & flash sale hidden from public APIs", func() (bool, string) {
			hasV := w3b45HasShopVoucher(t, env, shopID, suspendVoucher.ID)
			hasFS := w3b45HasFlashSaleItem(t, env, suspendFlashItemID)
			return !hasV && !hasFS, fmt.Sprintf("voucher_visible=%v flash_sale_visible=%v", hasV, hasFS)
		})

		// Step 5c: SaveVoucher for suspended shop -> strictly 409 SHOP_SUSPENDED (promotion r1).
		saveSusVResp := env.mustDoJSON(t, http.MethodPost,
			fmt.Sprintf("/api/v1/vouchers/%s/save", url.PathEscape(suspendVoucher.ID)),
			verifiedBuyer.AccessToken, nil, SaveVoucherRequest{}, http.StatusConflict)
		w3b45AssertErrorCode(t, "SaveVoucher on suspended shop", saveSusVResp, "SHOP_SUSPENDED")

		// Step 6a: After product has consumed user.shop_status_changed (SKU inactive), checkout via items[] MUST still return 409 SHOP_SUSPENDED (NOT SKU_INACTIVE; order p4 Item G).
		susCoResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", verifiedBuyer.AccessToken, map[string]string{
			"Idempotency-Key": "w3b45-blocked-sus-" + uuid.NewString(),
		}, CheckoutRequest{
			SalesChannel:  "MARKETPLACE",
			AddressID:     verifiedBuyer.AddressID,
			Items:         []CheckoutItemInputDTO{{SKUID: suspendProdAppr.SKUs[0].ID, Quantity: 1}},
			PaymentMethod: "COD",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: shopID, CarrierCode: susQuote.CarrierCode, ServiceCode: susQuote.ServiceCode},
			},
		}, http.StatusConflict)
		w3b45AssertErrorCode(t, "checkout items[] on suspended shop", susCoResp, "SHOP_SUSPENDED")

		// Step 6b: Checkout via from_cart=true after product deactivated SKU MUST ALSO return 409 SHOP_SUSPENDED (NOT SKU_INACTIVE; order p4 Item G).
		susCartCoResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", verifiedBuyer.AccessToken, map[string]string{
			"Idempotency-Key": "w3b45-blocked-sus-cart-" + uuid.NewString(),
		}, CheckoutRequest{
			SalesChannel:  "MARKETPLACE",
			AddressID:     verifiedBuyer.AddressID,
			FromCart:      true,
			PaymentMethod: "COD",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: shopID, CarrierCode: susQuote.CarrierCode, ServiceCode: susQuote.ServiceCode},
			},
		}, http.StatusConflict)
		w3b45AssertErrorCode(t, "checkout from_cart on suspended shop", susCartCoResp, "SHOP_SUSPENDED")
		env.mustDoJSON(t, http.MethodDelete, "/api/v1/cart", verifiedBuyer.AccessToken, nil, nil, http.StatusOK)

		// Step 7: Re-login seller: catalog creation blocked (409 SHOP_SUSPENDED), but in-flight order still ships (200).
		freshLoginResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/auth/login", "", nil, LoginRequest{
			Email:    suspendSeller.Email,
			Password: suspendSeller.Password,
		}, http.StatusOK)
		var freshLogin LoginResponse
		if err := DecodeJSON(freshLoginResp, &freshLogin); err != nil || freshLogin.AccessToken == "" {
			t.Fatalf("seller relogin while suspended: err=%v body=%s", err, string(freshLoginResp.Body))
		}
		freshSellerTok := freshLogin.AccessToken

		blockedCreateResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/products", freshSellerTok, nil, CreateProductRequest{
			CategoryID:  cat.ID,
			Name:        "W3B2 Blocked While Suspended",
			Description: "Should be blocked while shop is suspended",
			Images:      []string{"/products/ao-thun-cotton-trang.png"},
			SKUs: []SKUInputDTO{{
				SKUCode: env.uniqueCode("SUSBLK"), Name: "Default", PriceVND: 100000, Stock: 5, WeightGrams: 300,
			}},
		}, http.StatusConflict)
		w3b45AssertErrorCode(t, "seller create product while suspended", blockedCreateResp, "SHOP_SUSPENDED")

		inFlightShipResp := env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/seller/orders/%s/ship", url.PathEscape(inFlightOrderID)),
			freshSellerTok, nil, SellerShipRequest{
				CarrierCode: susQuote.CarrierCode,
				ServiceCode: susQuote.ServiceCode,
			}, http.StatusOK)
		var inFlightShipOut SellerShipResponse
		if err := DecodeJSON(inFlightShipResp, &inFlightShipOut); err != nil || inFlightShipOut.TrackingCode == "" {
			t.Fatalf("in-flight order ship while suspended failed: err=%v body=%s", err, string(inFlightShipResp.Body))
		}

		// Step 8: ops2 unsuspends shop -> APPROVED product reappears, seller-HIDDEN product stays HIDDEN.
		unsusResp := env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/admin/shops/%s/unsuspend", url.PathEscape(shopID)),
			ops2.AccessToken, nil, map[string]string{
				"reason": "w3b45 issue resolved, reopen shop",
			}, http.StatusOK)
		var unsusOut ShopResponse
		if err := DecodeJSON(unsusResp, &unsusOut); err != nil || !StatusEquals(unsusOut.Shop.Status, "ACTIVE") {
			t.Fatalf("ops2 unsuspend shop: err=%v body=%s", err, string(unsusResp.Body))
		}
		shopIsSuspended = false

		env.pollUntil(t, env.cfg.PollTimeout, 250*time.Millisecond, "approved product visible again after unsuspend", func() (bool, string) {
			listResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/products?shop_id="+url.QueryEscape(shopID), "", nil, nil, http.StatusOK)
			var listOut w3b45ProductsListResponse
			if err := DecodeJSON(listResp, &listOut); err != nil {
				return false, err.Error()
			}
			for _, p := range listOut.Items {
				if p.ID == suspendProdAppr.ID {
					return true, ""
				}
			}
			return false, fmt.Sprintf("approved product %s not yet restored in public list (total=%d)", suspendProdAppr.ID, listOut.Total)
		})

		// Approved product is public again; seller-hidden product remains HIDDEN and absent from public API.
		env.mustDoJSON(t, http.MethodGet, fmt.Sprintf("/api/v1/products/%s", url.PathEscape(suspendProdAppr.ID)), "", nil, nil, http.StatusOK)
		env.mustDoJSON(t, http.MethodGet, fmt.Sprintf("/api/v1/products/%s", url.PathEscape(pendingHidden.ID)), "", nil, nil, http.StatusNotFound)

		sellerHiddenResp := env.mustDoJSON(t, http.MethodGet, fmt.Sprintf("/api/v1/seller/products/%s", url.PathEscape(pendingHidden.ID)),
			freshSellerTok, nil, nil, http.StatusOK)
		var sellerHiddenOut ProductResponse
		if err := DecodeJSON(sellerHiddenResp, &sellerHiddenOut); err != nil {
			t.Fatalf("decode seller hidden product after unsuspend: %v", err)
		}
		if !StatusEquals(sellerHiddenOut.Product.Status, "HIDDEN") || sellerHiddenOut.Product.ShopSuspended {
			t.Fatalf("seller-hidden product mutated after unsuspend: status=%q shop_suspended=%v (want HIDDEN, false)",
				sellerHiddenOut.Product.Status, sellerHiddenOut.Product.ShopSuspended)
		}

		// After unsuspend, shop voucher & flash sale reappear publicly and SaveVoucher succeeds (promotion r1).
		env.pollUntil(t, env.cfg.PollTimeout, 250*time.Millisecond, "unsuspended shop voucher & flash sale visible again", func() (bool, string) {
			hasV := w3b45HasShopVoucher(t, env, shopID, suspendVoucher.ID)
			hasFS := w3b45HasFlashSaleItem(t, env, suspendFlashItemID)
			return hasV && hasFS, fmt.Sprintf("voucher_visible=%v flash_sale_visible=%v", hasV, hasFS)
		})
		saveUnsusResp := env.mustDoJSON(t, http.MethodPost,
			fmt.Sprintf("/api/v1/vouchers/%s/save", url.PathEscape(suspendVoucher.ID)),
			verifiedBuyer.AccessToken, nil, SaveVoucherRequest{}, http.StatusOK)
		var saveUnsusOut SaveVoucherResponse
		if err := DecodeJSON(saveUnsusResp, &saveUnsusOut); err != nil || !saveUnsusOut.Saved {
			t.Fatalf("SaveVoucher after unsuspend failed: err=%v body=%s", err, string(saveUnsusResp.Body))
		}
	})

	// 3. Two-person large refund approval, 24h anti-splitting & return refund rejection (F11):
	//    - 3A: cskh requests 6M manual refund -> PENDING_APPROVAL (0 ledger) -> cskh decide 403 ->
	//      accountant approves -> PENDING (ledger updated & balanced) -> accountant settles (200 DONE);
	//    - 3B: split 2 x 3M on same payment within 24h -> 2nd PENDING_APPROVAL; accountant self-approve -> 403 MAKER_CHECKER_CONFLICT;
	//    - 3C: large return refund resolved by cskh -> PENDING_APPROVAL -> accountant rejects ->
	//      payment.refund_rejected returns request back to ESCALATED (and order to RETURN_REQUESTED).
	t.Run("3_TwoPersonLargeRefundApproval_AntiSplitting_And_ReturnRejection", func(t *testing.T) {
		if cskh == nil || accountant == nil || regularAdmin == nil {
			t.Fatalf("prerequisite subtest 1 fixtures not initialized")
		}

		// 3A: Single 6,000,000 VND refund (above 5,000,000 threshold).
		co1 := w3b45PlaceAndConfirmVietQR(t, env, verifiedBuyer, seller1, largeRefundSKU, 1, quoteSeller1)
		ord1ID := co1.Orders[0].ID

		ledgerBefore := w3b45AssertLedgerBalanced(t, env)
		rpBefore := w3b45FindAccount(ledgerBefore, "refund_payable")

		largeAmt := largeAmtVND
		ref1Path := fmt.Sprintf("/api/v1/admin/payments/%s/refund", url.PathEscape(co1.PaymentID))
		ref1Resp := env.mustDoJSON(t, http.MethodPost, ref1Path, cskh.AccessToken, map[string]string{
			"Idempotency-Key": "w3b45-large-rf-" + uuid.NewString(),
		}, RefundRequest{
			OrderID:   ord1ID,
			AmountVND: largeAmt,
			Reason:    "w3b45 cskh large refund 6M",
		}, http.StatusOK)
		var ref1Out RefundResponse
		if err := DecodeJSON(ref1Resp, &ref1Out); err != nil {
			t.Fatalf("decode large refund create: %v", err)
		}
		if !StatusEquals(ref1Out.Refund.Status, "PENDING_APPROVAL") {
			t.Fatalf("large refund (%d VND) status = %q, want PENDING_APPROVAL", largeAmt, ref1Out.Refund.Status)
		}
		if ref1Out.Refund.RequestedBy != cskh.UserID {
			t.Fatalf("large refund requested_by = %q, want cskh %q", ref1Out.Refund.RequestedBy, cskh.UserID)
		}
		rf1ID := ref1Out.Refund.RefundID
		largeRefundPaymentID = co1.PaymentID
		largeRefundID = rf1ID

		// Ledger must NOT change while refund is PENDING_APPROVAL.
		ledgerAfterReq := w3b45AssertLedgerBalanced(t, env)
		rpAfterReq := w3b45FindAccount(ledgerAfterReq, "refund_payable")
		if ledgerAfterReq.TotalDebitsVND != ledgerBefore.TotalDebitsVND ||
			ledgerAfterReq.TotalCreditsVND != ledgerBefore.TotalCreditsVND ||
			rpAfterReq.BalanceVND != rpBefore.BalanceVND {
			t.Fatalf("PENDING_APPROVAL refund mutated ledger: before=(%d,%d,rp=%d) after=(%d,%d,rp=%d)",
				ledgerBefore.TotalDebitsVND, ledgerBefore.TotalCreditsVND, rpBefore.BalanceVND,
				ledgerAfterReq.TotalDebitsVND, ledgerAfterReq.TotalCreditsVND, rpAfterReq.BalanceVND)
		}

		// Unified approvals endpoint lists the pending refund approval for accountant.
		rfAppListResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/approvals?type=refund&page=1&page_size=50",
			accountant.AccessToken, nil, nil, http.StatusOK)
		var rfAppList w3b45ApprovalsListResponse
		if err := DecodeJSON(rfAppListResp, &rfAppList); err != nil {
			t.Fatalf("decode refund approvals list: %v", err)
		}
		foundRF := false
		for _, rf := range rfAppList.RefundApprovals {
			if rf.RefundID == rf1ID {
				foundRF = true
				break
			}
		}
		if !foundRF {
			t.Fatalf("pending refund %s not listed in GET /api/v1/admin/approvals?type=refund", rf1ID)
		}

		// cskh cannot approve -> strictly 403.
		decideRF1Path := fmt.Sprintf("/api/v1/admin/approvals/refund/%s/decide", url.PathEscape(rf1ID))
		env.mustDoJSON(t, http.MethodPost, decideRF1Path, cskh.AccessToken, nil, map[string]any{
			"approve": true,
			"note":    "cskh self-approve must fail",
		}, http.StatusForbidden)

		// accountant approves -> PENDING, ledger posts DR escrow / CR refund_payable and stays balanced.
		decRF1Resp := env.mustDoJSON(t, http.MethodPost, decideRF1Path, accountant.AccessToken, nil, map[string]any{
			"approve": true,
			"note":    "accountant approved 6M refund",
		}, http.StatusOK)
		var decRF1Out RefundResponse
		if err := DecodeJSON(decRF1Resp, &decRF1Out); err != nil {
			t.Fatalf("decode accountant approve refund: %v", err)
		}
		if !StatusEquals(decRF1Out.Refund.Status, "PENDING") {
			t.Fatalf("approved large refund status = %q, want PENDING", decRF1Out.Refund.Status)
		}
		if decRF1Out.Refund.ApprovedBy != accountant.UserID {
			t.Fatalf("approved large refund approved_by = %q, want accountant %q", decRF1Out.Refund.ApprovedBy, accountant.UserID)
		}

		ledgerAfterApprove := w3b45AssertLedgerBalanced(t, env)
		rpAfterApprove := w3b45FindAccount(ledgerAfterApprove, "refund_payable")
		if delta := rpAfterApprove.BalanceVND - rpBefore.BalanceVND; delta != largeAmt {
			t.Fatalf("refund_payable delta after approval = %d, want %d", delta, largeAmt)
		}

		// Buyer submits refund bank account and accountant settles refund (verifies accountant refunds.settle=200).
		env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/refunds/%s/bank-account", url.PathEscape(rf1ID)),
			verifiedBuyer.AccessToken, nil, map[string]string{
				"bank_code":           "VCB",
				"bank_account_number": "19034567891234",
				"bank_account_name":   "NGUYEN VAN BUYER",
			}, http.StatusOK)
		settleResp := env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/admin/refunds/%s/settle", url.PathEscape(rf1ID)),
			accountant.AccessToken, map[string]string{
				"Idempotency-Key": "w3b45-acct-settle-" + uuid.NewString(),
			}, map[string]string{
				"provider_refund_id": "FT-W3B45-" + uuid.NewString()[:8],
				"note":               "w3b45 accountant settled approved refund",
			}, http.StatusOK)
		var settleOut w3b45SettleRefundResponse
		if err := DecodeJSON(settleResp, &settleOut); err != nil || !StatusEquals(settleOut.Refund.Status, "DONE") {
			t.Fatalf("accountant settle refund: err=%v body=%s", err, string(settleResp.Body))
		}
		_ = w3b45AssertLedgerBalanced(t, env)

		// 3B: Anti-splitting within 24h: 2 x 3,000,000 VND on the same payment -> 1st PENDING, 2nd PENDING_APPROVAL.
		co2 := w3b45PlaceAndConfirmVietQR(t, env, verifiedBuyer, seller1, largeRefundSKU, 1, quoteSeller1)
		ord2ID := co2.Orders[0].ID
		ref2Path := fmt.Sprintf("/api/v1/admin/payments/%s/refund", url.PathEscape(co2.PaymentID))

		split1Resp := env.mustDoJSON(t, http.MethodPost, ref2Path, cskh.AccessToken, map[string]string{
			"Idempotency-Key": "w3b45-split1-" + uuid.NewString(),
		}, RefundRequest{
			OrderID:   ord2ID,
			AmountVND: splitAmtVND,
			Reason:    "w3b45 split refund #1 (3M)",
		}, http.StatusOK)
		var split1Out RefundResponse
		if err := DecodeJSON(split1Resp, &split1Out); err != nil {
			t.Fatalf("decode split refund #1: %v", err)
		}
		if !StatusEquals(split1Out.Refund.Status, "PENDING") {
			t.Fatalf("split refund #1 (%d < %d) status = %q, want PENDING", splitAmtVND, thresholdVND, split1Out.Refund.Status)
		}

		ledgerAfterSplit1 := w3b45AssertLedgerBalanced(t, env)

		split2Resp := env.mustDoJSON(t, http.MethodPost, ref2Path, cskh.AccessToken, map[string]string{
			"Idempotency-Key": "w3b45-split2-" + uuid.NewString(),
		}, RefundRequest{
			OrderID:   ord2ID,
			AmountVND: splitAmtVND,
			Reason:    "w3b45 split refund #2 (3M, 24h sum=6M >= 5M)",
		}, http.StatusOK)
		var split2Out RefundResponse
		if err := DecodeJSON(split2Resp, &split2Out); err != nil {
			t.Fatalf("decode split refund #2: %v", err)
		}
		if !StatusEquals(split2Out.Refund.Status, "PENDING_APPROVAL") {
			t.Fatalf("split refund #2 (24h rolling sum %d >= %d) status = %q, want PENDING_APPROVAL",
				splitAmtVND*2, thresholdVND, split2Out.Refund.Status)
		}
		ledgerAfterSplit2 := w3b45AssertLedgerBalanced(t, env)
		if ledgerAfterSplit2.TotalDebitsVND != ledgerAfterSplit1.TotalDebitsVND {
			t.Fatalf("2nd split PENDING_APPROVAL unexpectedly posted to ledger: before=%d after=%d",
				ledgerAfterSplit1.TotalDebitsVND, ledgerAfterSplit2.TotalDebitsVND)
		}

		// accountant rejects 2nd split -> REJECTED with 0 ledger impact.
		rejResp := env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/admin/approvals/refund/%s/decide", url.PathEscape(split2Out.Refund.RefundID)),
			accountant.AccessToken, nil, map[string]any{
				"approve": false,
				"note":    "w3b45 reject split refund",
			}, http.StatusOK)
		var rejOut RefundResponse
		if err := DecodeJSON(rejResp, &rejOut); err != nil || !StatusEquals(rejOut.Refund.Status, "REJECTED") {
			t.Fatalf("accountant reject split refund #2: err=%v body=%s", err, string(rejResp.Body))
		}

		// Same-role maker-checker conflict on refund approval: accountant (who HAS refunds.approve) creates a 3M split
		// (24h rolling sum with split #1 >= threshold -> PENDING_APPROVAL), then tries to self-approve -> 403 MAKER_CHECKER_CONFLICT.
		acctMakerResp := env.mustDoJSON(t, http.MethodPost, ref2Path, accountant.AccessToken, map[string]string{
			"Idempotency-Key": "w3b45-split3-acct-" + uuid.NewString(),
		}, RefundRequest{
			OrderID:   ord2ID,
			AmountVND: splitAmtVND,
			Reason:    "w3b45 accountant maker self-approve guard",
		}, http.StatusOK)
		var acctMakerOut RefundResponse
		if err := DecodeJSON(acctMakerResp, &acctMakerOut); err != nil || !StatusEquals(acctMakerOut.Refund.Status, "PENDING_APPROVAL") {
			t.Fatalf("accountant split refund #3 want PENDING_APPROVAL, got err=%v body=%s", err, string(acctMakerResp.Body))
		}
		acctSelfDecide := env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/admin/approvals/refund/%s/decide", url.PathEscape(acctMakerOut.Refund.RefundID)),
			accountant.AccessToken, nil, map[string]any{
				"approve": true,
				"note":    "accountant self-approve must conflict",
			}, http.StatusForbidden)
		w3b45AssertErrorCode(t, "accountant self-approve refund", acctSelfDecide, "MAKER_CHECKER_CONFLICT")

		// regularAdmin cleans up by rejecting accountant's pending request.
		env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/admin/approvals/refund/%s/decide", url.PathEscape(acctMakerOut.Refund.RefundID)),
			regularAdmin.AccessToken, nil, map[string]any{
				"approve": false,
				"note":    "w3b45 admin rejects test request",
			}, http.StatusOK)
		_ = w3b45AssertLedgerBalanced(t, env)

		// 3C: Large return refund rejected by checker -> payment.refund_rejected moves return request back to ESCALATED.
		coRetLarge := w3b45PlaceAndConfirmVietQR(t, env, verifiedBuyer, seller1, largeRefundSKU, 1, quoteSeller1)
		ordRetID := coRetLarge.Orders[0].ID
		w3b45ShipAndDeliverOrder(t, env, seller1, verifiedBuyer, ordRetID, quoteSeller1)

		retLargeResp := env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/orders/%s/return", url.PathEscape(ordRetID)),
			verifiedBuyer.AccessToken, nil, map[string]any{"reason": "w3b45 large return refund test"}, http.StatusOK)
		var retLargeOut w3b45ReturnRequestWrap
		if err := DecodeJSON(retLargeResp, &retLargeOut); err != nil || retLargeOut.ReturnRequestID == "" {
			t.Fatalf("request large return: err=%v body=%s", err, string(retLargeResp.Body))
		}
		retLargeID := retLargeOut.ReturnRequestID

		env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/seller/returns/%s/respond", url.PathEscape(retLargeID)),
			seller1.AccessToken, nil, map[string]any{"decision": "REJECT", "note": "w3b45 seller reject large return"}, http.StatusOK)
		env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/returns/%s/escalate", url.PathEscape(retLargeID)),
			verifiedBuyer.AccessToken, nil, map[string]any{"reason": "w3b45 escalate large return"}, http.StatusOK)

		ledgerBeforeRet := w3b45AssertLedgerBalanced(t, env)
		rpBeforeRet := w3b45FindAccount(ledgerBeforeRet, "refund_payable")

		// cskh resolves escalated large return with REFUND -> order calls CreateRefund(require_approval_if_large=true, requested_by=cskh).
		env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/admin/returns/%s/resolve", url.PathEscape(retLargeID)),
			cskh.AccessToken, nil, map[string]any{"decision": "REFUND", "note": "w3b45 cskh large return refund"}, http.StatusOK)

		var pendingRetRefund RefundDTO
		env.pollUntil(t, env.cfg.PollTimeout, 250*time.Millisecond, "large return refund created as PENDING_APPROVAL", func() (bool, string) {
			qPath := fmt.Sprintf("/api/v1/admin/refunds?order_id=%s&status=PENDING_APPROVAL&page=1&page_size=20", url.QueryEscape(ordRetID))
			resp := env.mustDoJSON(t, http.MethodGet, qPath, accountant.AccessToken, nil, nil, http.StatusOK)
			var list w3b45RefundListResponse
			if err := DecodeJSON(resp, &list); err != nil {
				return false, err.Error()
			}
			for _, rf := range list.Items {
				if rf.OrderID == ordRetID && StatusEquals(rf.Status, "PENDING_APPROVAL") {
					pendingRetRefund = rf
					return true, ""
				}
			}
			return false, fmt.Sprintf("order=%s pending_approval refunds=%d", ordRetID, len(list.Items))
		})
		if pendingRetRefund.RequestedBy != cskh.UserID {
			t.Fatalf("large return refund requested_by = %q, want cskh %q", pendingRetRefund.RequestedBy, cskh.UserID)
		}

		// accountant rejects the large return refund -> payment emits payment.refund_rejected -> return transitions to ESCALATED.
		retRejResp := env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/admin/approvals/refund/%s/decide", url.PathEscape(pendingRetRefund.RefundID)),
			accountant.AccessToken, nil, map[string]any{
				"approve": false,
				"note":    "w3b45 accountant rejects large return refund",
			}, http.StatusOK)
		var retRejOut RefundResponse
		if err := DecodeJSON(retRejResp, &retRejOut); err != nil || !StatusEquals(retRejOut.Refund.Status, "REJECTED") {
			t.Fatalf("reject large return refund: err=%v body=%s", err, string(retRejResp.Body))
		}

		var ordRetCode string
		env.pollUntil(t, env.cfg.PollTimeout, 250*time.Millisecond, "return request re-escalated via payment.refund_rejected", func() (bool, string) {
			rResp := env.mustDoJSON(t, http.MethodGet, fmt.Sprintf("/api/v1/admin/returns/%s", url.PathEscape(retLargeID)),
				cskh.AccessToken, nil, nil, http.StatusOK)
			var rOut w3b45ReturnDetailResponse
			if err := DecodeJSON(rResp, &rOut); err != nil {
				return false, err.Error()
			}
			o := env.getOrder(t, verifiedBuyer.AccessToken, ordRetID)
			ordRetCode = o.OrderCode
			return StatusEquals(rOut.ReturnRequest.Status, "ESCALATED") && StatusEquals(o.Status, "RETURN_REQUESTED"),
				fmt.Sprintf("return_status=%s order_status=%s", rOut.ReturnRequest.Status, o.Status)
		})

		// Verify buyer's in-app notification for order.return_updated (reason="refund_rejected") uses the
		// dedicated re-review wording and NEVER contains the generic dispute text "chuyển tới quản trị viên" (notification r2).
		if ordRetCode == "" {
			ordRetCode = ordRetID
		}
		var refundRejNotif NotificationDTO
		env.pollUntil(t, env.cfg.PollTimeout, 250*time.Millisecond, "buyer in-app refund_rejected notification", func() (bool, string) {
			nResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/notifications?category=ORDER&page=1&page_size=30",
				verifiedBuyer.AccessToken, nil, nil, http.StatusOK)
			var nList NotificationsListResponse
			if err := DecodeJSON(nResp, &nList); err != nil {
				return false, err.Error()
			}
			for _, it := range nList.Items {
				if strings.Contains(it.Title, ordRetCode) && strings.Contains(it.Title, "cần được xem xét lại") {
					refundRejNotif = it
					return true, ""
				}
			}
			return false, fmt.Sprintf("checked %d ORDER notifications for order_code=%s refund_rejected title", len(nList.Items), ordRetCode)
		})
		if !strings.Contains(refundRejNotif.Body, "hiện chưa được hoàn tiền") {
			t.Fatalf("refund_rejected notification body missing expected copy 'hiện chưa được hoàn tiền': title=%q body=%q",
				refundRejNotif.Title, refundRejNotif.Body)
		}
		if strings.Contains(refundRejNotif.Body, "chuyển tới quản trị viên") {
			t.Fatalf("refund_rejected notification body must NOT contain generic dispute phrase 'chuyển tới quản trị viên': %q",
				refundRejNotif.Body)
		}

		ledgerAfterRetRej := w3b45AssertLedgerBalanced(t, env)
		rpAfterRetRej := w3b45FindAccount(ledgerAfterRetRej, "refund_payable")
		if rpAfterRetRej.BalanceVND != rpBeforeRet.BalanceVND {
			t.Fatalf("rejected large return refund mutated refund_payable: before=%d after=%d",
				rpBeforeRet.BalanceVND, rpAfterRetRej.BalanceVND)
		}
	})

	// 4. User activities timeline & audit logs with time range (F13):
	//    - GET /admin/users/{id}/activities: cskh gets timeline (login_succeeded + status_changed) + recent_orders;
	//      moderator gets 200 + timeline + recent_orders: [] (lacks orders.read, never 403);
	//    - GET /admin/audit-logs?from_time=...&to_time=... includes exact maker/checker pairs.
	t.Run("4_UserActivities_And_AuditLogsMakerCheckerPairs", func(t *testing.T) {
		if activityBuyer == nil || cskh == nil || moderator == nil || ops1 == nil || ops2 == nil || accountant == nil {
			t.Fatalf("prerequisite fixtures not initialized")
		}

		actPath := fmt.Sprintf("/api/v1/admin/users/%s/activities?page=1&page_size=50", url.PathEscape(activityBuyer.UserID))

		// (a) cskh (has users.read + orders.read) sees login_succeeded + status_changed AND recent_orders.
		cskhResp := env.mustDoJSON(t, http.MethodGet, actPath, cskh.AccessToken, nil, nil, http.StatusOK)
		if strings.Contains(string(cskhResp.Body), activityBuyer.Password) || strings.Contains(string(cskhResp.Body), activityBuyer.AccessToken) {
			t.Fatalf("user activities response leaked sensitive credential/token!")
		}
		var cskhAct w3b45UserActivitiesResponse
		if err := DecodeJSON(cskhResp, &cskhAct); err != nil {
			t.Fatalf("decode cskh user activities: %v", err)
		}
		hasLogin, hasStatusChange := false, false
		for _, it := range cskhAct.Items {
			switch it.EventType {
			case "login_succeeded":
				hasLogin = true
			case "status_changed":
				hasStatusChange = true
			}
		}
		if !hasLogin || !hasStatusChange {
			t.Fatalf("user activities missing expected exact events (login_succeeded=%v status_changed=%v); items=%+v",
				hasLogin, hasStatusChange, cskhAct.Items)
		}
		foundOrder := false
		for _, o := range cskhAct.RecentOrders {
			if o.ID == activityOrderID {
				foundOrder = true
				break
			}
		}
		if !foundOrder {
			t.Fatalf("cskh user activities recent_orders missing order %s; got %d orders", activityOrderID, len(cskhAct.RecentOrders))
		}

		// (b) moderator (has users.read, lacks orders.read) gets 200 with timeline and recent_orders: [] (F13).
		modResp := env.mustDoJSON(t, http.MethodGet, actPath, moderator.AccessToken, nil, nil, http.StatusOK)
		var modAct w3b45UserActivitiesResponse
		if err := DecodeJSON(modResp, &modAct); err != nil {
			t.Fatalf("decode moderator user activities: %v", err)
		}
		if len(modAct.Items) == 0 {
			t.Fatalf("moderator user activities returned 0 timeline entries")
		}
		if len(modAct.RecentOrders) != 0 {
			t.Fatalf("moderator must receive empty recent_orders (lacks orders.read), got %d orders", len(modAct.RecentOrders))
		}

		// (c) Audit logs filtered by from_time & to_time contain exact maker/checker pairs for shop suspension & refund approval.
		fromParam := url.QueryEscape(testStart.Format(time.RFC3339))
		toParam := url.QueryEscape(time.Now().UTC().Add(5 * time.Second).Format(time.RFC3339))

		allAuditResp := env.mustDoJSON(t, http.MethodGet,
			fmt.Sprintf("/api/v1/admin/audit-logs?from_time=%s&to_time=%s&page=1&page_size=200", fromParam, toParam),
			superTok, nil, nil, http.StatusOK)
		var allAudit w3b45AuditLogsResponse
		if err := DecodeJSON(allAuditResp, &allAudit); err != nil {
			t.Fatalf("decode audit-logs range response: %v", err)
		}
		if allAudit.Total < 4 {
			t.Fatalf("expected at least 4 audit log entries in test window, got total=%d", allAudit.Total)
		}

		actorChecks := []struct {
			label      string
			actorID    string
			wantTarget string
			wantAction string
		}{
			{
				label:      "shop-suspend-maker(ops1)",
				actorID:    ops1.UserID,
				wantTarget: suspendSeller.ShopID,
				wantAction: "admin.shop.suspend_request",
			},
			{
				label:      "shop-suspend-checker(ops2)",
				actorID:    ops2.UserID,
				wantTarget: suspendSeller.ShopID,
				wantAction: "admin.shop.suspend_approve",
			},
			{
				label:      "refund-maker(cskh)",
				actorID:    cskh.UserID,
				wantTarget: largeRefundPaymentID,
				wantAction: "POST /api/v1/admin/payments/{id}/refund",
			},
			{
				label:      "refund-checker(accountant)",
				actorID:    accountant.UserID,
				wantTarget: largeRefundID,
				wantAction: "POST /api/v1/admin/approvals/refund/{id}/decide",
			},
		}
		for _, ac := range actorChecks {
			env.pollUntil(t, env.cfg.PollTimeout, 250*time.Millisecond, "audit log for "+ac.label, func() (bool, string) {
				qPath := fmt.Sprintf("/api/v1/admin/audit-logs?from_time=%s&to_time=%s&actor_id=%s&page=1&page_size=50",
					fromParam, toParam, url.QueryEscape(ac.actorID))
				resp := env.mustDoJSON(t, http.MethodGet, qPath, superTok, nil, nil, http.StatusOK)
				var out w3b45AuditLogsResponse
				if err := DecodeJSON(resp, &out); err != nil {
					return false, err.Error()
				}
				for _, item := range out.Items {
					if item.ActorID == ac.actorID && item.TargetID == ac.wantTarget && item.Action == ac.wantAction {
						return true, ""
					}
				}
				return false, fmt.Sprintf("actor=%s returned %d audit items, want target=%q action=%q",
					ac.actorID, len(out.Items), ac.wantTarget, ac.wantAction)
			})
		}

		// Invalid timestamp format -> strictly 400 INVALID_ARGUMENT.
		badAuditResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/audit-logs?from_time=not-a-timestamp",
			superTok, nil, nil, http.StatusBadRequest)
		w3b45AssertErrorCode(t, "audit-logs invalid from_time", badAuditResp, "INVALID_ARGUMENT")
	})
}
