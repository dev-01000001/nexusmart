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
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type usrW3AAddBankReq struct {
	BankCode          string `json:"bank_code"`
	BankName          string `json:"bank_name"`
	AccountNumber     string `json:"account_number"`
	AccountHolderName string `json:"account_holder_name"`
}

type usrW3AVerifyBankOTPReq struct {
	OTP string `json:"otp"`
}

type usrW3AAdminReviewBankReq struct {
	BankAccountID string `json:"bank_account_id,omitempty"`
	Decision      string `json:"decision"`
	Reason        string `json:"reason,omitempty"`
}

type usrW3AAdminReviewShopReq struct {
	Decision          string `json:"decision"`
	Reason            string `json:"reason,omitempty"`
	ExpectedOwnerName string `json:"expected_owner_name,omitempty"`
}

type usrW3ABankAccountDTO struct {
	ID                  string `json:"id"`
	ShopID              string `json:"shop_id"`
	BankCode            string `json:"bank_code"`
	BankName            string `json:"bank_name"`
	AccountNumberMasked string `json:"account_number_masked"`
	AccountNumber       string `json:"account_number"`
	AccountHolderName   string `json:"account_holder_name"`
	HolderNameMatched   bool   `json:"holder_name_matched"`
	Status              string `json:"status"`
	ReviewNote          string `json:"review_note"`
}

type usrW3ABankAccountResp struct {
	BankAccount usrW3ABankAccountDTO `json:"bank_account"`
	OTPSent     bool                 `json:"otp_sent"`
}

func usrW3AUserGRPCAddr() string {
	if v := strings.TrimSpace(os.Getenv("USER_GRPC_ADDR")); v != "" {
		return v
	}
	return "localhost:50051"
}

func usrW3AServiceJWTSecret() string {
	if v := strings.TrimSpace(os.Getenv("SERVICE_JWT_SECRET")); v != "" {
		return v
	}
	return "dev-only-nexusmart-service-hs256-secret-change-me"
}

func usrW3AMintServiceToken(secret, serviceName string) string {
	enc := base64.RawURLEncoding
	now := time.Now().Unix()
	claims, _ := json.Marshal(map[string]any{
		"sub":     serviceName,
		"role":    "admin",
		"service": serviceName,
		"iss":     "nexusmart",
		"jti":     uuid.NewString(),
		"iat":     now,
		"exp":     now + 300,
	})
	unsigned := enc.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." + enc.EncodeToString(claims)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(unsigned))
	return unsigned + "." + enc.EncodeToString(mac.Sum(nil))
}

func usrW3ADialUserGRPC(t *testing.T) userv1.UserServiceClient {
	t.Helper()
	conn, err := grpc.NewClient(usrW3AUserGRPCAddr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial user grpc %s: %v", usrW3AUserGRPCAddr(), err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return userv1.NewUserServiceClient(conn)
}

func usrW3AWithBearer(ctx context.Context, token string) context.Context {
	if strings.TrimSpace(token) == "" {
		return ctx
	}
	return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+token))
}

func usrW3ASnapshotMailpitIDs(t *testing.T, env *testEnv) map[string]struct{} {
	t.Helper()
	seen := make(map[string]struct{})
	list, err := env.mailpit.ListMessages(env.ctx(t), 100)
	if err != nil {
		t.Fatalf("mailpit list snapshot: %v", err)
	}
	for _, m := range list.Messages {
		seen[m.ID] = struct{}{}
	}
	return seen
}

func usrW3AWaitNewOTPEmail(t *testing.T, env *testEnv, toEmail string, seen map[string]struct{}) string {
	t.Helper()
	deadline := time.Now().Add(env.cfg.PollTimeout)
	target := strings.ToLower(strings.TrimSpace(toEmail))
	for time.Now().Before(deadline) {
		list, err := env.mailpit.ListMessages(env.ctx(t), 100)
		if err == nil {
			for _, summary := range list.Messages {
				if _, old := seen[summary.ID]; old {
					continue
				}
				matched := false
				for _, addr := range summary.To {
					if strings.ToLower(strings.TrimSpace(addr.Address)) == target {
						matched = true
						break
					}
				}
				if !matched {
					continue
				}
				full, getErr := env.mailpit.GetMessage(env.ctx(t), summary.ID)
				if getErr != nil {
					continue
				}
				if otp := ExtractOTP(full.Text, full.HTML, summary.Snippet); otp != "" {
					seen[summary.ID] = struct{}{}
					return otp
				}
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for bank OTP email in Mailpit for %s", toEmail)
	return ""
}

func usrW3ACreateApprovedSellerWithName(t *testing.T, env *testEnv, label, fullName string) *sellerFixture {
	t.Helper()
	u := env.registerVerifyAndLogin(t, label)
	env.mustDoJSON(t, http.MethodPatch, "/api/v1/users/me", u.AccessToken, nil, map[string]string{
		"full_name": fullName,
	}, http.StatusOK)
	u.User.FullName = fullName
	addr := env.addAddress(t, u)

	shopName := fmt.Sprintf("NexusW3AShop-%s-%s-%d", label, env.runID, env.nextSeq())
	shopResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/shops/register", u.AccessToken, nil, RegisterShopRequest{
		Name:               shopName,
		Description:        "E2E W3A user security shop",
		PickupAddressID:    addr.ID,
		ContactPhone:       u.Phone,
		BusinessLicenseDoc: "https://sandbox.nexusmart.local/docs/w3a-license.pdf",
	}, http.StatusCreated, http.StatusOK)
	var shopOut ShopResponse
	if err := DecodeJSON(shopResp, &shopOut); err != nil || shopOut.Shop.ID == "" {
		t.Fatalf("decode register shop (%s): err=%v body=%s", label, err, string(shopResp.Body))
	}

	adminTok := env.adminLogin(t)
	reviewPath := fmt.Sprintf("/api/v1/admin/shops/%s/review", url.PathEscape(shopOut.Shop.ID))
	reviewResp := env.mustDoJSON(t, http.MethodPost, reviewPath, adminTok, nil, usrW3AAdminReviewShopReq{
		Decision:          "APPROVE",
		Reason:            "E2E W3A approve with expected_owner_name",
		ExpectedOwnerName: fullName,
	}, http.StatusOK)
	var reviewOut ShopResponse
	if err := DecodeJSON(reviewResp, &reviewOut); err != nil || !StatusEquals(reviewOut.Shop.Status, "ACTIVE") {
		t.Fatalf("approve shop (%s): err=%v body=%s", label, err, string(reviewResp.Body))
	}

	reLoginResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/auth/login", "", nil, LoginRequest{
		Email:    u.Email,
		Password: u.Password,
	}, http.StatusOK)
	var reLoginOut LoginResponse
	if err := DecodeJSON(reLoginResp, &reLoginOut); err != nil || reLoginOut.AccessToken == "" {
		t.Fatalf("seller re-login (%s): err=%v body=%s", label, err, string(reLoginResp.Body))
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

// TestUserW3A_D5D6_BankAccountReviewAndOwnerNameGuard verifies D5 (encrypted bank account at rest + payout-only decrypt)
// and D6 (required bank_account_id -> 400 when missing, stale bank_account_id -> 409, and expected_owner_name mismatch -> 409).
func TestUserW3A_D5D6_BankAccountReviewAndOwnerNameGuard(t *testing.T) {
	env := getEnv()
	adminTok := env.adminLogin(t)
	userClient := usrW3ADialUserGRPC(t)
	svcTok := usrW3AMintServiceToken(usrW3AServiceJWTSecret(), "payment")

	t.Run("BankAccountReview_MissingID400_StaleVersion409_PayoutDecrypt", func(t *testing.T) {
		seller := usrW3ACreateApprovedSellerWithName(t, env, "usrw3abank", "Nguyễn Văn Ba A")
		seenMail := usrW3ASnapshotMailpitIDs(t, env)

		// 1. Seller adds bank account A (mismatched holder name) and verifies OTP -> PENDING_ADMIN_REVIEW.
		rawAccA := "19034567893801"
		addAResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/bank-account", seller.AccessToken, nil, usrW3AAddBankReq{
			BankCode:          "VCB",
			BankName:          "Vietcombank",
			AccountNumber:     rawAccA,
			AccountHolderName: "CONG TY W3A PHIEN BAN A",
		}, http.StatusCreated, http.StatusOK)
		if strings.Contains(string(addAResp.Body), rawAccA) {
			t.Fatalf("POST /seller/bank-account A leaked raw account number")
		}
		var addAOut usrW3ABankAccountResp
		if err := DecodeJSON(addAResp, &addAOut); err != nil || addAOut.BankAccount.ID == "" {
			t.Fatalf("decode add A: err=%v body=%s", err, string(addAResp.Body))
		}
		accAID := addAOut.BankAccount.ID

		otpA := usrW3AWaitNewOTPEmail(t, env, seller.Email, seenMail)
		verAResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/bank-account/verify-otp", seller.AccessToken, nil, usrW3AVerifyBankOTPReq{
			OTP: otpA,
		}, http.StatusOK)
		var verAOut usrW3ABankAccountResp
		if err := DecodeJSON(verAResp, &verAOut); err != nil || !StatusEquals(verAOut.BankAccount.Status, "PENDING_ADMIN_REVIEW") {
			t.Fatalf("verify A: want PENDING_ADMIN_REVIEW, got err=%v body=%s", err, string(verAResp.Body))
		}

		adminReviewPath := fmt.Sprintf("/api/v1/admin/shops/%s/bank-account/review", url.PathEscape(seller.ShopID))
		adminGetPath := fmt.Sprintf("/api/v1/admin/shops/%s/bank-account", url.PathEscape(seller.ShopID))

		// 2. Missing or blank bank_account_id -> 400 Bad Request via REST and InvalidArgument via direct gRPC.
		env.mustDoJSON(t, http.MethodPost, adminReviewPath, adminTok, nil, usrW3AAdminReviewBankReq{
			Decision: "APPROVE",
			Reason:   "missing bank_account_id",
		}, http.StatusBadRequest)
		env.mustDoJSON(t, http.MethodPost, adminReviewPath, adminTok, nil, usrW3AAdminReviewBankReq{
			BankAccountID: "   ",
			Decision:      "APPROVE",
			Reason:        "blank bank_account_id",
		}, http.StatusBadRequest)
		if _, err := userClient.AdminReviewShopBankAccount(usrW3AWithBearer(env.ctx(t), adminTok), &userv1.AdminReviewShopBankAccountRequest{
			ShopId:        seller.ShopID,
			BankAccountId: "",
			Decision:      userv1.ReviewDecision_REVIEW_DECISION_APPROVE,
		}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("direct gRPC AdminReviewShopBankAccount empty bank_account_id: want InvalidArgument, got %v", err)
		}

		// 3. Admin views A, then seller switches to B (mismatched holder name -> OTP verified -> A becomes SUPERSEDED, B is PENDING_ADMIN_REVIEW).
		adminViewAResp := env.mustDoJSON(t, http.MethodGet, adminGetPath, adminTok, nil, nil, http.StatusOK)
		var adminViewA usrW3ABankAccountResp
		if err := DecodeJSON(adminViewAResp, &adminViewA); err != nil || adminViewA.BankAccount.ID != accAID {
			t.Fatalf("admin view A mismatch: err=%v body=%s", err, string(adminViewAResp.Body))
		}

		rawAccB := "19034567893802"
		addBResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/bank-account", seller.AccessToken, nil, usrW3AAddBankReq{
			BankCode:          "TCB",
			BankName:          "Techcombank",
			AccountNumber:     rawAccB,
			AccountHolderName: "CONG TY W3A PHIEN BAN B",
		}, http.StatusCreated, http.StatusOK)
		if strings.Contains(string(addBResp.Body), rawAccB) {
			t.Fatalf("POST /seller/bank-account B leaked raw account number")
		}
		var addBOut usrW3ABankAccountResp
		if err := DecodeJSON(addBResp, &addBOut); err != nil || addBOut.BankAccount.ID == "" {
			t.Fatalf("decode add B: err=%v body=%s", err, string(addBResp.Body))
		}
		accBID := addBOut.BankAccount.ID

		otpB := usrW3AWaitNewOTPEmail(t, env, seller.Email, seenMail)
		verBResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/bank-account/verify-otp", seller.AccessToken, nil, usrW3AVerifyBankOTPReq{
			OTP: otpB,
		}, http.StatusOK)
		var verBOut usrW3ABankAccountResp
		if err := DecodeJSON(verBResp, &verBOut); err != nil || !StatusEquals(verBOut.BankAccount.Status, "PENDING_ADMIN_REVIEW") {
			t.Fatalf("verify B: want PENDING_ADMIN_REVIEW, got err=%v body=%s", err, string(verBResp.Body))
		}

		// 4. Admin tries to approve stale A -> 409 Conflict! Neither A nor B is VERIFIED!
		env.mustDoJSON(t, http.MethodPost, adminReviewPath, adminTok, nil, usrW3AAdminReviewBankReq{
			BankAccountID: accAID,
			Decision:      "APPROVE",
			Reason:        "stale approval attempt on A",
		}, http.StatusConflict)

		// Confirm B is still PENDING_ADMIN_REVIEW and NO verified account exists for payout.
		adminViewBResp := env.mustDoJSON(t, http.MethodGet, adminGetPath, adminTok, nil, nil, http.StatusOK)
		var adminViewB usrW3ABankAccountResp
		if err := DecodeJSON(adminViewBResp, &adminViewB); err != nil ||
			adminViewB.BankAccount.ID != accBID ||
			!StatusEquals(adminViewB.BankAccount.Status, "PENDING_ADMIN_REVIEW") {
			t.Fatalf("after stale A review, expected B still PENDING_ADMIN_REVIEW: err=%v body=%s", err, string(adminViewBResp.Body))
		}
		if _, err := userClient.GetShopBankAccountForPayout(usrW3AWithBearer(env.ctx(t), svcTok), &userv1.GetShopBankAccountForPayoutRequest{
			ShopId: seller.ShopID,
		}); status.Code(err) != codes.NotFound {
			t.Fatalf("payout before approving B must return NotFound (zero VERIFIED rows), got %v", err)
		}

		// 5. Admin approves current B -> 200 VERIFIED (masked in REST), payout RPC decrypts rawAccB, re-approving B -> 409.
		approveBResp := env.mustDoJSON(t, http.MethodPost, adminReviewPath, adminTok, nil, usrW3AAdminReviewBankReq{
			BankAccountID: accBID,
			Decision:      "APPROVE",
			Reason:        "verified current B",
		}, http.StatusOK)
		if strings.Contains(string(approveBResp.Body), rawAccB) {
			t.Fatalf("admin approve B response leaked raw account number")
		}
		var approveBOut usrW3ABankAccountResp
		if err := DecodeJSON(approveBResp, &approveBOut); err != nil || !StatusEquals(approveBOut.BankAccount.Status, "VERIFIED") {
			t.Fatalf("approve B: want VERIFIED, got err=%v body=%s", err, string(approveBResp.Body))
		}

		payoutResp, err := userClient.GetShopBankAccountForPayout(usrW3AWithBearer(env.ctx(t), svcTok), &userv1.GetShopBankAccountForPayoutRequest{
			ShopId: seller.ShopID,
		})
		if err != nil || payoutResp.GetBankAccount().GetAccountNumber() != rawAccB {
			t.Fatalf("GetShopBankAccountForPayout after approving B: err=%v got len=%d", err, len(payoutResp.GetBankAccount().GetAccountNumber()))
		}

		env.mustDoJSON(t, http.MethodPost, adminReviewPath, adminTok, nil, usrW3AAdminReviewBankReq{
			BankAccountID: accBID,
			Decision:      "APPROVE",
			Reason:        "duplicate approve on verified B",
		}, http.StatusConflict)
	})

	t.Run("ShopReview_ExpectedOwnerNameMismatch409", func(t *testing.T) {
		u := env.registerVerifyAndLogin(t, "usrw3aowner")
		const initialName = "Nguyễn Văn Gốc W3A"
		const changedName = "Trần Văn Đổi W3A"
		env.mustDoJSON(t, http.MethodPatch, "/api/v1/users/me", u.AccessToken, nil, map[string]string{
			"full_name": initialName,
		}, http.StatusOK)
		addr := env.addAddress(t, u)

		shopName := fmt.Sprintf("NexusW3AOwner-%s-%d", env.runID, env.nextSeq())
		regResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/shops/register", u.AccessToken, nil, RegisterShopRequest{
			Name:               shopName,
			Description:        "E2E W3A expected_owner_name test",
			PickupAddressID:    addr.ID,
			ContactPhone:       u.Phone,
			BusinessLicenseDoc: "https://sandbox.nexusmart.local/docs/w3a-owner.pdf",
		}, http.StatusCreated, http.StatusOK)
		var regOut ShopResponse
		if err := DecodeJSON(regResp, &regOut); err != nil || regOut.Shop.ID == "" {
			t.Fatalf("decode register shop: err=%v body=%s", err, string(regResp.Body))
		}
		shopID := regOut.Shop.ID

		// Owner changes full_name between admin opening review and clicking APPROVE.
		env.mustDoJSON(t, http.MethodPatch, "/api/v1/users/me", u.AccessToken, nil, map[string]string{
			"full_name": changedName,
		}, http.StatusOK)

		// Direct gRPC APPROVE with stale expected_owner_name -> FailedPrecondition (OWNER_NAME_CHANGED).
		if _, err := userClient.AdminReviewShop(usrW3AWithBearer(env.ctx(t), adminTok), &userv1.AdminReviewShopRequest{
			ShopId:            shopID,
			Decision:          userv1.ReviewDecision_REVIEW_DECISION_APPROVE,
			ExpectedOwnerName: initialName,
		}); status.Code(err) != codes.FailedPrecondition || !strings.Contains(status.Convert(err).Message(), "OWNER_NAME_CHANGED") {
			t.Fatalf("direct gRPC AdminReviewShop stale owner name: want FailedPrecondition OWNER_NAME_CHANGED, got %v", err)
		}

		// Gateway REST APPROVE with stale expected_owner_name -> 409 Conflict with OWNER_NAME_CHANGED.
		reviewPath := fmt.Sprintf("/api/v1/admin/shops/%s/review", url.PathEscape(shopID))
		conflictResp := env.mustDoJSON(t, http.MethodPost, reviewPath, adminTok, nil, usrW3AAdminReviewShopReq{
			Decision:          "APPROVE",
			ExpectedOwnerName: initialName,
		}, http.StatusConflict)
		if !strings.Contains(string(conflictResp.Body), "OWNER_NAME_CHANGED") {
			t.Fatalf("REST 409 body missing OWNER_NAME_CHANGED: %s", string(conflictResp.Body))
		}

		// Shop MUST still be PENDING.
		meShopResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/shops/me", u.AccessToken, nil, nil, http.StatusOK)
		var meShop ShopResponse
		if err := DecodeJSON(meShopResp, &meShop); err != nil || !StatusEquals(meShop.Shop.Status, "PENDING") {
			t.Fatalf("shop should remain PENDING after OWNER_NAME_CHANGED: err=%v body=%s", err, string(meShopResp.Body))
		}

		// Approving with the current full_name succeeds -> 200 ACTIVE.
		okResp := env.mustDoJSON(t, http.MethodPost, reviewPath, adminTok, nil, usrW3AAdminReviewShopReq{
			Decision:          "APPROVE",
			ExpectedOwnerName: changedName,
		}, http.StatusOK)
		var okOut ShopResponse
		if err := DecodeJSON(okResp, &okOut); err != nil || !StatusEquals(okOut.Shop.Status, "ACTIVE") {
			t.Fatalf("approve with current expected_owner_name: want ACTIVE, got err=%v body=%s", err, string(okResp.Body))
		}
	})
}
