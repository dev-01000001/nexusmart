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

type usrW2BAddBankAccountReq struct {
	BankCode          string `json:"bank_code"`
	BankName          string `json:"bank_name"`
	AccountNumber     string `json:"account_number"`
	AccountHolderName string `json:"account_holder_name"`
}

type usrW2BVerifyBankOTPReq struct {
	OTP string `json:"otp"`
}

type usrW2BAdminReviewBankReq struct {
	BankAccountID string `json:"bank_account_id"`
	Decision      string `json:"decision"`
	Reason        string `json:"reason"`
}

type usrW2BBankAccountDTO struct {
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
	PayoutLockedUntil   string `json:"payout_locked_until"`
	VerifiedAt          string `json:"verified_at"`
}

type usrW2BAddBankAccountResp struct {
	BankAccount usrW2BBankAccountDTO `json:"bank_account"`
	OTPSent     bool                 `json:"otp_sent"`
}

type usrW2BBankAccountResp struct {
	BankAccount usrW2BBankAccountDTO `json:"bank_account"`
}

type usrW2BAuditLogsResp struct {
	Items []struct {
		ID         string `json:"id"`
		Action     string `json:"action"`
		TargetType string `json:"target_type"`
		TargetID   string `json:"target_id"`
		BeforeJSON string `json:"before_json"`
		AfterJSON  string `json:"after_json"`
		Reason     string `json:"reason"`
	} `json:"items"`
	Total int64 `json:"total"`
}

func usrW2BUserGRPCAddr() string {
	if v := strings.TrimSpace(os.Getenv("USER_GRPC_ADDR")); v != "" {
		return v
	}
	return "localhost:50051"
}

func usrW2BServiceJWTSecret() string {
	if v := strings.TrimSpace(os.Getenv("SERVICE_JWT_SECRET")); v != "" {
		return v
	}
	return "dev-only-nexusmart-service-hs256-secret-change-me"
}

func usrW2BMintServiceToken(secret, serviceName string) string {
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

func usrW2BDialUserGRPC(t *testing.T) userv1.UserServiceClient {
	t.Helper()
	conn, err := grpc.NewClient(usrW2BUserGRPCAddr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial user grpc %s: %v", usrW2BUserGRPCAddr(), err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return userv1.NewUserServiceClient(conn)
}

func usrW2BWithBearer(ctx context.Context, token string) context.Context {
	if strings.TrimSpace(token) == "" {
		return ctx
	}
	return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+token))
}

func usrW2BSnapshotMailpitIDs(t *testing.T, env *testEnv) map[string]struct{} {
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

func usrW2BWaitNewOTPEmail(t *testing.T, env *testEnv, toEmail string, seen map[string]struct{}) (*MailpitMessage, string) {
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
					return full, otp
				}
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for new bank-account OTP email in Mailpit for %s", toEmail)
	return nil, ""
}

func usrW2BAssertNoRawLeak(t *testing.T, contextLabel string, body []byte, forbidden ...string) {
	t.Helper()
	s := string(body)
	for _, f := range forbidden {
		if f != "" && strings.Contains(s, f) {
			t.Fatalf("%s leaked sensitive value (len=%d)", contextLabel, len(f))
		}
	}
}

// usrW2BCreateApprovedSellerWithName sets the user's full_name BEFORE admin approves the shop so
// shops.verified_owner_name snapshots the expected owner name at approval time (contracts §4.1).
func usrW2BCreateApprovedSellerWithName(t *testing.T, env *testEnv, label, fullName string) *sellerFixture {
	t.Helper()
	u := env.registerVerifyAndLogin(t, label)
	env.mustDoJSON(t, http.MethodPatch, "/api/v1/users/me", u.AccessToken, nil, map[string]string{
		"full_name": fullName,
	}, http.StatusOK)
	u.User.FullName = fullName
	addr := env.addAddress(t, u)

	shopName := fmt.Sprintf("NexusShop-%s-%s-%d", label, env.runID, env.nextSeq())
	shopResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/shops/register", u.AccessToken, nil, RegisterShopRequest{
		Name:               shopName,
		Description:        "E2E verified merchant store",
		PickupAddressID:    addr.ID,
		ContactPhone:       u.Phone,
		BusinessLicenseDoc: "https://sandbox.nexusmart.local/docs/license.pdf",
	}, http.StatusCreated, http.StatusOK)
	var shopOut ShopResponse
	if err := DecodeJSON(shopResp, &shopOut); err != nil || shopOut.Shop.ID == "" {
		t.Fatalf("decode register shop (%s): err=%v body=%s", label, err, string(shopResp.Body))
	}

	adminTok := env.adminLogin(t)
	reviewPath := fmt.Sprintf("/api/v1/admin/shops/%s/review", url.PathEscape(shopOut.Shop.ID))
	reviewResp := env.mustDoJSON(t, http.MethodPost, reviewPath, adminTok, nil, ReviewRequest{
		Decision: "APPROVE",
		Reason:   "E2E automated shop approval with verified_owner_name",
	}, http.StatusOK)
	var reviewOut ShopResponse
	if err := DecodeJSON(reviewResp, &reviewOut); err != nil || !StatusEquals(reviewOut.Shop.Status, "ACTIVE") {
		t.Fatalf("approve shop (%s) mismatch: err=%v body=%s", label, err, string(reviewResp.Body))
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

// TestUserW2BDirectGRPC verifies user service gRPC RPCs directly against live Postgres/Redis/NATS:
// cross-shop 403 isolation, service-token-only GetShopBankAccountForPayout, and VN-date GetPlatformUserStats.
func TestUserW2BDirectGRPC(t *testing.T) {
	env := getEnv()
	adminTok := env.adminLogin(t)
	userClient := usrW2BDialUserGRPC(t)
	svcTok := usrW2BMintServiceToken(usrW2BServiceJWTSecret(), "payment")

	// Set seller1 full_name BEFORE shop approval so shops.verified_owner_name captures the accented name.
	seller1 := usrW2BCreateApprovedSellerWithName(t, env, "usrw2bgrpc1", "Nguyễn Văn Ánh Đạt")
	seller2 := env.createApprovedSeller(t, "usrw2bgrpc2")
	buyer := env.registerVerifyAndLogin(t, "usrw2bgrpcb")

	t.Run("CrossShop403AndPayoutServiceTokenOnly", func(t *testing.T) {
		ctx := env.ctx(t)

		// Buyer cannot add or read shop bank account -> PermissionDenied.
		if _, err := userClient.AddShopBankAccount(usrW2BWithBearer(ctx, buyer.AccessToken), &userv1.AddShopBankAccountRequest{
			ShopId:            seller1.ShopID,
			BankCode:          "VCB",
			BankName:          "Vietcombank",
			AccountNumber:     "19034567890001",
			AccountHolderName: "E2E BUYER",
		}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("buyer AddShopBankAccount: want PermissionDenied, got %v", err)
		}

		// Seller1 spoofing Seller2's shop_id -> PermissionDenied (403).
		if _, err := userClient.AddShopBankAccount(usrW2BWithBearer(ctx, seller1.AccessToken), &userv1.AddShopBankAccountRequest{
			ShopId:            seller2.ShopID,
			BankCode:          "VCB",
			BankName:          "Vietcombank",
			AccountNumber:     "19034567890002",
			AccountHolderName: "E2E SELLER",
		}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("cross-shop AddShopBankAccount: want PermissionDenied, got %v", err)
		}
		if _, err := userClient.GetShopBankAccount(usrW2BWithBearer(ctx, seller1.AccessToken), &userv1.GetShopBankAccountRequest{
			ShopId: seller2.ShopID,
		}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("cross-shop GetShopBankAccount: want PermissionDenied, got %v", err)
		}
		if _, err := userClient.VerifyShopBankAccountOTP(usrW2BWithBearer(ctx, seller1.AccessToken), &userv1.VerifyShopBankAccountOTPRequest{
			ShopId: seller2.ShopID,
			Otp:    "123456",
		}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("cross-shop VerifyShopBankAccountOTP: want PermissionDenied, got %v", err)
		}

		// GetShopBankAccountForPayout: anonymous -> Unauthenticated; buyer/seller/admin JWT -> PermissionDenied.
		if _, err := userClient.GetShopBankAccountForPayout(ctx, &userv1.GetShopBankAccountForPayoutRequest{
			ShopId: seller1.ShopID,
		}); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("anonymous GetShopBankAccountForPayout: want Unauthenticated, got %v", err)
		}
		for role, tok := range map[string]string{
			"buyer":  buyer.AccessToken,
			"seller": seller1.AccessToken,
			"admin":  adminTok,
		} {
			if _, err := userClient.GetShopBankAccountForPayout(usrW2BWithBearer(ctx, tok), &userv1.GetShopBankAccountForPayoutRequest{
				ShopId: seller1.ShopID,
			}); status.Code(err) != codes.PermissionDenied {
				t.Fatalf("%s JWT GetShopBankAccountForPayout: want PermissionDenied, got %v", role, err)
			}
		}

		// Service token against shop with no verified account -> NotFound (proving service token auth succeeded).
		if _, err := userClient.GetShopBankAccountForPayout(usrW2BWithBearer(ctx, svcTok), &userv1.GetShopBankAccountForPayoutRequest{
			ShopId: seller1.ShopID,
		}); status.Code(err) != codes.NotFound {
			t.Fatalf("service token GetShopBankAccountForPayout before verify: want NotFound, got %v", err)
		}
	})

	t.Run("FullLifecycleDirectGRPCWithMailpit", func(t *testing.T) {
		ctx := env.ctx(t)
		seenMail := usrW2BSnapshotMailpitIDs(t, env)

		// 1. Add bank account -> real NATS user.shop_bank_account_otp -> notification -> Mailpit -> 5 wrong OTPs lockout.
		rawAccWrong := "19034567895001"
		addWrong, err := userClient.AddShopBankAccount(usrW2BWithBearer(ctx, seller1.AccessToken), &userv1.AddShopBankAccountRequest{
			BankCode:          "VCB",
			BankName:          "Vietcombank",
			AccountNumber:     rawAccWrong,
			AccountHolderName: "NGUYEN VAN ANH DAT",
		})
		if err != nil {
			t.Fatalf("AddShopBankAccount (wrong OTP test): %v", err)
		}
		if !addWrong.GetOtpSent() || addWrong.GetBankAccount().GetAccountNumber() != "" ||
			addWrong.GetBankAccount().GetAccountNumberMasked() != "****5001" {
			t.Fatalf("unexpected AddShopBankAccount response: %+v", addWrong)
		}
		mailWrong, realOTPWrong := usrW2BWaitNewOTPEmail(t, env, seller1.Email, seenMail)
		if strings.Contains(mailWrong.Subject, realOTPWrong) {
			t.Fatalf("OTP must NOT appear in email Subject: %q", mailWrong.Subject)
		}
		usrW2BAssertNoRawLeak(t, "Mailpit OTP email (gRPC)", []byte(mailWrong.Subject+"\n"+mailWrong.Text+"\n"+mailWrong.HTML), rawAccWrong)

		badOTP := "000000"
		if realOTPWrong == badOTP {
			badOTP = "999999"
		}
		for i := 1; i <= 4; i++ {
			if _, err := userClient.VerifyShopBankAccountOTP(usrW2BWithBearer(ctx, seller1.AccessToken), &userv1.VerifyShopBankAccountOTPRequest{
				Otp: badOTP,
			}); status.Code(err) != codes.InvalidArgument {
				t.Fatalf("wrong OTP #%d: want InvalidArgument, got %v", i, err)
			}
		}
		if _, err := userClient.VerifyShopBankAccountOTP(usrW2BWithBearer(ctx, seller1.AccessToken), &userv1.VerifyShopBankAccountOTPRequest{
			Otp: badOTP,
		}); status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("5th wrong OTP: want FailedPrecondition, got %v", err)
		}
		if _, err := userClient.VerifyShopBankAccountOTP(usrW2BWithBearer(ctx, seller1.AccessToken), &userv1.VerifyShopBankAccountOTPRequest{
			Otp: realOTPWrong,
		}); status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("real OTP after 5 wrong attempts: want FailedPrecondition, got %v", err)
		}
		// Locked/superseded PENDING_OTP must not be returned by GetShopBankAccount; seller can cleanly re-submit below.
		if _, err := userClient.GetShopBankAccount(usrW2BWithBearer(ctx, seller1.AccessToken), &userv1.GetShopBankAccountRequest{}); status.Code(err) != codes.NotFound {
			t.Fatalf("GetShopBankAccount after 5 wrong OTPs: want NotFound, got %v", err)
		}

		// 2. Re-submit matching name -> OTP verified -> VERIFIED (no payout lock on first verified account).
		rawAcc1 := "19034567895002"
		if _, err := userClient.AddShopBankAccount(usrW2BWithBearer(ctx, seller1.AccessToken), &userv1.AddShopBankAccountRequest{
			BankCode:          "VCB",
			BankName:          "Vietcombank",
			AccountNumber:     rawAcc1,
			AccountHolderName: "  NGUYEN   VAN ANH DAT ",
		}); err != nil {
			t.Fatalf("AddShopBankAccount #1: %v", err)
		}
		_, otp1 := usrW2BWaitNewOTPEmail(t, env, seller1.Email, seenMail)
		ver1, err := userClient.VerifyShopBankAccountOTP(usrW2BWithBearer(ctx, seller1.AccessToken), &userv1.VerifyShopBankAccountOTPRequest{
			Otp: otp1,
		})
		if err != nil {
			t.Fatalf("VerifyShopBankAccountOTP #1: %v", err)
		}
		if ver1.GetBankAccount().GetStatus() != userv1.ShopBankAccountStatus_SHOP_BANK_ACCOUNT_STATUS_VERIFIED ||
			!ver1.GetBankAccount().GetHolderNameMatched() ||
			ver1.GetBankAccount().GetPayoutLockedUntil() != nil ||
			ver1.GetBankAccount().GetAccountNumber() != "" {
			t.Fatalf("unexpected verified bank account #1: %+v", ver1.GetBankAccount())
		}

		// GetShopBankAccount masks for both seller & admin; GetShopBankAccountForPayout returns raw only for service token.
		getSel, err := userClient.GetShopBankAccount(usrW2BWithBearer(ctx, seller1.AccessToken), &userv1.GetShopBankAccountRequest{})
		if err != nil || getSel.GetBankAccount().GetAccountNumber() != "" || getSel.GetBankAccount().GetAccountNumberMasked() != "****5002" {
			t.Fatalf("seller GetShopBankAccount mismatch: err=%v acc=%+v", err, getSel)
		}
		getAdm, err := userClient.GetShopBankAccount(usrW2BWithBearer(ctx, adminTok), &userv1.GetShopBankAccountRequest{ShopId: seller1.ShopID})
		if err != nil || getAdm.GetBankAccount().GetAccountNumber() != "" || getAdm.GetBankAccount().GetAccountNumberMasked() != "****5002" {
			t.Fatalf("admin GetShopBankAccount mismatch: err=%v acc=%+v", err, getAdm)
		}
		payout1, err := userClient.GetShopBankAccountForPayout(usrW2BWithBearer(ctx, svcTok), &userv1.GetShopBankAccountForPayoutRequest{ShopId: seller1.ShopID})
		if err != nil || payout1.GetBankAccount().GetAccountNumber() != rawAcc1 {
			t.Fatalf("GetShopBankAccountForPayout #1 mismatch: err=%v acc=%+v", err, payout1)
		}

		// 3. Mismatched holder name change -> PENDING_ADMIN_REVIEW while payout keeps rawAcc1; admin APPROVE -> VERIFIED + 24h payout lock.
		rawAcc2 := "19034567895003"
		add2, err := userClient.AddShopBankAccount(usrW2BWithBearer(ctx, seller1.AccessToken), &userv1.AddShopBankAccountRequest{
			BankCode:          "TCB",
			BankName:          "Techcombank",
			AccountNumber:     rawAcc2,
			AccountHolderName: "CONG TY TNHH KHAC TEN",
		})
		if err != nil {
			t.Fatalf("AddShopBankAccount #2 mismatch: %v", err)
		}
		_, otp2 := usrW2BWaitNewOTPEmail(t, env, seller1.Email, seenMail)
		ver2, err := userClient.VerifyShopBankAccountOTP(usrW2BWithBearer(ctx, seller1.AccessToken), &userv1.VerifyShopBankAccountOTPRequest{
			Otp: otp2,
		})
		if err != nil {
			t.Fatalf("VerifyShopBankAccountOTP #2 mismatch: %v", err)
		}
		if ver2.GetBankAccount().GetStatus() != userv1.ShopBankAccountStatus_SHOP_BANK_ACCOUNT_STATUS_PENDING_ADMIN_REVIEW ||
			ver2.GetBankAccount().GetHolderNameMatched() {
			t.Fatalf("expected PENDING_ADMIN_REVIEW for mismatch, got %+v", ver2.GetBankAccount())
		}
		// While pending admin review, payout still returns rawAcc1.
		payoutStill1, err := userClient.GetShopBankAccountForPayout(usrW2BWithBearer(ctx, svcTok), &userv1.GetShopBankAccountForPayoutRequest{ShopId: seller1.ShopID})
		if err != nil || payoutStill1.GetBankAccount().GetAccountNumber() != rawAcc1 {
			t.Fatalf("expected prior verified account while change pending review: err=%v acc=%+v", err, payoutStill1)
		}

		// AdminListShops(bank_review_pending_only=true) surfaces seller1 with bank_review_pending=true,
		// combines with status filter, while non-admin callers / GetShop / ListPublicShops never see the flag.
		if _, err := userClient.AdminListShops(usrW2BWithBearer(ctx, seller1.AccessToken), &userv1.AdminListShopsRequest{
			BankReviewPendingOnly: true,
		}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("seller AdminListShops: want PermissionDenied, got %v", err)
		}
		if _, err := userClient.AdminListShops(usrW2BWithBearer(ctx, buyer.AccessToken), &userv1.AdminListShopsRequest{
			BankReviewPendingOnly: true,
		}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("buyer AdminListShops: want PermissionDenied, got %v", err)
		}

		pendingList, err := userClient.AdminListShops(usrW2BWithBearer(ctx, adminTok), &userv1.AdminListShopsRequest{
			Status:                userv1.ShopStatus_SHOP_STATUS_ACTIVE,
			BankReviewPendingOnly: true,
			Page:                  1,
			PageSize:              100,
		})
		if err != nil {
			t.Fatalf("AdminListShops(ACTIVE, bank_review_pending_only=true): %v", err)
		}
		foundPending := false
		for _, sh := range pendingList.GetShops() {
			if !sh.GetBankReviewPending() {
				t.Fatalf("filtered shop %s missing bank_review_pending=true", sh.GetId())
			}
			if sh.GetId() == seller1.ShopID {
				foundPending = true
			}
		}
		if !foundPending || pendingList.GetTotal() < 1 {
			t.Fatalf("expected seller1 shop %s in bank_review_pending_only list (total=%d)", seller1.ShopID, pendingList.GetTotal())
		}

		pendingStatusMismatch, err := userClient.AdminListShops(usrW2BWithBearer(ctx, adminTok), &userv1.AdminListShopsRequest{
			Status:                userv1.ShopStatus_SHOP_STATUS_PENDING,
			BankReviewPendingOnly: true,
			Page:                  1,
			PageSize:              100,
		})
		if err != nil {
			t.Fatalf("AdminListShops(PENDING, bank_review_pending_only=true): %v", err)
		}
		for _, sh := range pendingStatusMismatch.GetShops() {
			if sh.GetId() == seller1.ShopID {
				t.Fatalf("ACTIVE shop %s unexpectedly returned under status=PENDING filter", seller1.ShopID)
			}
		}

		pubGetShop, err := userClient.GetShop(ctx, &userv1.GetShopRequest{ShopId: seller1.ShopID})
		if err != nil || pubGetShop.GetShop().GetBankReviewPending() {
			t.Fatalf("GetShop must keep bank_review_pending=false: err=%v flag=%v", err, pubGetShop.GetShop().GetBankReviewPending())
		}
		pubListShops, err := userClient.ListPublicShops(ctx, &userv1.ListPublicShopsRequest{Page: 1, PageSize: 100})
		if err != nil {
			t.Fatalf("ListPublicShops: %v", err)
		}
		for _, sh := range pubListShops.GetShops() {
			if sh.GetBankReviewPending() {
				t.Fatalf("ListPublicShops leaked bank_review_pending=true on shop %s", sh.GetId())
			}
		}

		// Admin approves -> supersedes old VERIFIED, activates rawAcc2, sets payout_locked_until (~24h).
		revResp, err := userClient.AdminReviewShopBankAccount(usrW2BWithBearer(ctx, adminTok), &userv1.AdminReviewShopBankAccountRequest{
			ShopId:        seller1.ShopID,
			BankAccountId: add2.GetBankAccount().GetId(),
			Decision:      userv1.ReviewDecision_REVIEW_DECISION_APPROVE,
			Reason:        "Live E2E admin approval",
		})
		if err != nil {
			t.Fatalf("AdminReviewShopBankAccount approve: %v", err)
		}
		if revResp.GetBankAccount().GetStatus() != userv1.ShopBankAccountStatus_SHOP_BANK_ACCOUNT_STATUS_VERIFIED ||
			revResp.GetBankAccount().GetAccountNumber() != "" ||
			revResp.GetBankAccount().GetPayoutLockedUntil() == nil {
			t.Fatalf("unexpected admin approved bank account: %+v", revResp.GetBankAccount())
		}
		if rem := time.Until(revResp.GetBankAccount().GetPayoutLockedUntil().AsTime()); rem < 23*time.Hour || rem > 25*time.Hour {
			t.Fatalf("expected ~24h payout lock on bank account change, got remaining=%s", rem)
		}
		payout2, err := userClient.GetShopBankAccountForPayout(usrW2BWithBearer(ctx, svcTok), &userv1.GetShopBankAccountForPayoutRequest{ShopId: seller1.ShopID})
		if err != nil || payout2.GetBankAccount().GetAccountNumber() != rawAcc2 {
			t.Fatalf("GetShopBankAccountForPayout #2 mismatch: err=%v acc=%+v", err, payout2)
		}

		// After admin approval, seller1 disappears from bank_review_pending_only list and has bank_review_pending=false.
		afterApprovePending, err := userClient.AdminListShops(usrW2BWithBearer(ctx, adminTok), &userv1.AdminListShopsRequest{
			BankReviewPendingOnly: true,
			Page:                  1,
			PageSize:              100,
		})
		if err != nil {
			t.Fatalf("AdminListShops(bank_review_pending_only=true) after approve: %v", err)
		}
		for _, sh := range afterApprovePending.GetShops() {
			if sh.GetId() == seller1.ShopID {
				t.Fatalf("shop %s still appeared in bank_review_pending_only list after approval", seller1.ShopID)
			}
		}
		afterApproveAll, err := userClient.AdminListShops(usrW2BWithBearer(ctx, adminTok), &userv1.AdminListShopsRequest{
			Status:   userv1.ShopStatus_SHOP_STATUS_ACTIVE,
			Page:     1,
			PageSize: 100,
		})
		if err != nil {
			t.Fatalf("AdminListShops(ACTIVE) after approve: %v", err)
		}
		for _, sh := range afterApproveAll.GetShops() {
			if sh.GetId() == seller1.ShopID && sh.GetBankReviewPending() {
				t.Fatalf("shop %s still has bank_review_pending=true after approval", seller1.ShopID)
			}
		}

		// 4. Audit logs recorded in Postgres without leaking raw account numbers or OTPs.
		logsResp, err := userClient.ListAuditLogs(usrW2BWithBearer(ctx, adminTok), &userv1.ListAuditLogsRequest{
			TargetType: "shop_bank_account",
			Page:       1,
			PageSize:   50,
		})
		if err != nil {
			t.Fatalf("ListAuditLogs: %v", err)
		}
		if logsResp.GetTotal() == 0 {
			t.Fatalf("expected shop_bank_account audit log entries")
		}
		for _, entry := range logsResp.GetLogs() {
			combined := entry.GetBeforeJson() + " " + entry.GetAfterJson() + " " + entry.GetReason()
			usrW2BAssertNoRawLeak(t, "audit_log "+entry.GetAction(), []byte(combined),
				rawAccWrong, rawAcc1, rawAcc2, realOTPWrong, otp1, otp2)
		}
	})

	t.Run("AddRateLimitDirectGRPC", func(t *testing.T) {
		ctx := env.ctx(t)
		// Dedicated shops so rate-limit test never interferes with seller1/seller2.
		rateSeller := env.createApprovedSeller(t, "usrw2brate1")
		otherRateSeller := env.createApprovedSeller(t, "usrw2brate2")

		for i := 1; i <= 5; i++ {
			if _, err := userClient.AddShopBankAccount(usrW2BWithBearer(ctx, rateSeller.AccessToken), &userv1.AddShopBankAccountRequest{
				BankCode:          "VCB",
				BankName:          "Vietcombank",
				AccountNumber:     fmt.Sprintf("1903456789800%d", i),
				AccountHolderName: "E2E RATE LIMIT",
			}); err != nil {
				t.Fatalf("AddShopBankAccount #%d failed: %v", i, err)
			}
		}

		// 6th call within 1 hour -> ResourceExhausted.
		if _, err := userClient.AddShopBankAccount(usrW2BWithBearer(ctx, rateSeller.AccessToken), &userv1.AddShopBankAccountRequest{
			BankCode:          "VCB",
			BankName:          "Vietcombank",
			AccountNumber:     "19034567898006",
			AccountHolderName: "E2E RATE LIMIT",
		}); status.Code(err) != codes.ResourceExhausted {
			t.Fatalf("6th AddShopBankAccount within 1h: want ResourceExhausted, got %v", err)
		}

		// Separate shop is unaffected.
		if _, err := userClient.AddShopBankAccount(usrW2BWithBearer(ctx, otherRateSeller.AccessToken), &userv1.AddShopBankAccountRequest{
			BankCode:          "TCB",
			BankName:          "Techcombank",
			AccountNumber:     "19034567899001",
			AccountHolderName: "E2E OTHER SHOP",
		}); err != nil {
			t.Fatalf("other shop AddShopBankAccount should succeed: %v", err)
		}
	})

	t.Run("PlatformUserStatsVNDateRange", func(t *testing.T) {
		ctx := env.ctx(t)
		vnZone := time.FixedZone("Asia/Ho_Chi_Minh", 7*3600)
		todayVN := time.Now().In(vnZone).Format("2006-01-02")

		// Non-admin and service token rejected.
		if _, err := userClient.GetPlatformUserStats(usrW2BWithBearer(ctx, buyer.AccessToken), &userv1.GetPlatformUserStatsRequest{
			FromDate: todayVN, ToDate: todayVN,
		}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("buyer GetPlatformUserStats: want PermissionDenied, got %v", err)
		}
		if _, err := userClient.GetPlatformUserStats(usrW2BWithBearer(ctx, seller1.AccessToken), &userv1.GetPlatformUserStatsRequest{
			FromDate: todayVN, ToDate: todayVN,
		}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("seller GetPlatformUserStats: want PermissionDenied, got %v", err)
		}
		if _, err := userClient.GetPlatformUserStats(usrW2BWithBearer(ctx, svcTok), &userv1.GetPlatformUserStatsRequest{
			FromDate: todayVN, ToDate: todayVN,
		}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("service token GetPlatformUserStats: want PermissionDenied, got %v", err)
		}

		// Span > 366 days rejected.
		if _, err := userClient.GetPlatformUserStats(usrW2BWithBearer(ctx, adminTok), &userv1.GetPlatformUserStatsRequest{
			FromDate: "2025-01-01", ToDate: "2026-01-02",
		}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("367-day span GetPlatformUserStats: want InvalidArgument, got %v", err)
		}

		before, err := userClient.GetPlatformUserStats(usrW2BWithBearer(ctx, adminTok), &userv1.GetPlatformUserStatsRequest{
			FromDate: todayVN, ToDate: todayVN,
		})
		if err != nil {
			t.Fatalf("GetPlatformUserStats before: %v", err)
		}

		// Create 1 new approved seller (1 new active user + 1 new active shop) today in VN time.
		_ = env.createApprovedSeller(t, "usrw2bstat")

		after, err := userClient.GetPlatformUserStats(usrW2BWithBearer(ctx, adminTok), &userv1.GetPlatformUserStatsRequest{
			FromDate: todayVN, ToDate: todayVN,
		})
		if err != nil {
			t.Fatalf("GetPlatformUserStats after: %v", err)
		}
		if after.GetTotalUsers() < before.GetTotalUsers()+1 ||
			after.GetActiveUsers() < before.GetActiveUsers()+1 ||
			after.GetNewUsers() < before.GetNewUsers()+1 {
			t.Fatalf("expected user stats to increment: before=%+v after=%+v", before, after)
		}
		if after.GetTotalShops() < before.GetTotalShops()+1 ||
			after.GetActiveShops() < before.GetActiveShops()+1 ||
			after.GetNewShops() < before.GetNewShops()+1 {
			t.Fatalf("expected shop stats to increment: before=%+v after=%+v", before, after)
		}
		if len(after.GetDaily()) != 1 || after.GetDaily()[0].GetDate() != todayVN ||
			after.GetDaily()[0].GetNewUsers() != after.GetNewUsers() ||
			after.GetDaily()[0].GetNewShops() != after.GetNewShops() {
			t.Fatalf("unexpected daily stats: %+v", after.GetDaily())
		}
	})
}

// TestUserW2BBankAccountGatewayAndMailpit exercises the complete UC-S17 end-to-end flow via
// Gateway REST + user service + NATS + notification service + Mailpit:
// OTP email, wrong OTP lockout, diacritic-insensitive matching, mismatch -> admin review,
// changing verified account -> 24h payout lock, cross-shop 403, and no account-number leakage.
func TestUserW2BBankAccountGatewayAndMailpit(t *testing.T) {
	env := getEnv()
	adminTok := env.adminLogin(t)
	userClient := usrW2BDialUserGRPC(t)
	svcTok := usrW2BMintServiceToken(usrW2BServiceJWTSecret(), "payment")

	// Set seller1 full_name BEFORE shop approval so shops.verified_owner_name captures the Vietnamese accented name.
	seller1 := usrW2BCreateApprovedSellerWithName(t, env, "usrw2bbank1", "Nguyễn Văn Ánh Đạt")
	seller2 := env.createApprovedSeller(t, "usrw2bbank2")
	buyer := env.registerVerifyAndLogin(t, "usrw2bbankb")

	seenMail := usrW2BSnapshotMailpitIDs(t, env)

	t.Run("WrongOTPLockoutViaMailpit", func(t *testing.T) {
		rawAcc := "19034567890011"
		addResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/bank-account", seller1.AccessToken, nil, usrW2BAddBankAccountReq{
			BankCode:          "VCB",
			BankName:          "Vietcombank",
			AccountNumber:     rawAcc,
			AccountHolderName: "NGUYEN VAN ANH DAT",
		}, http.StatusCreated, http.StatusOK)
		usrW2BAssertNoRawLeak(t, "POST /seller/bank-account", addResp.Body, rawAcc)

		var addOut usrW2BAddBankAccountResp
		if err := DecodeJSON(addResp, &addOut); err != nil {
			t.Fatalf("decode add bank account: %v", err)
		}
		if !addOut.OTPSent || !StatusEquals(addOut.BankAccount.Status, "PENDING_OTP") || addOut.BankAccount.AccountNumber != "" {
			t.Fatalf("unexpected add bank account response: %+v", addOut)
		}

		msg, realOTP := usrW2BWaitNewOTPEmail(t, env, seller1.Email, seenMail)
		if strings.Contains(msg.Subject, realOTP) {
			t.Fatalf("OTP must NOT appear in email Subject: %q", msg.Subject)
		}
		usrW2BAssertNoRawLeak(t, "Mailpit OTP email", []byte(msg.Subject+"\n"+msg.Text+"\n"+msg.HTML), rawAcc)

		wrongOTP := "000000"
		if realOTP == wrongOTP {
			wrongOTP = "999999"
		}
		for i := 1; i <= 4; i++ {
			env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/bank-account/verify-otp", seller1.AccessToken, nil, usrW2BVerifyBankOTPReq{
				OTP: wrongOTP,
			}, http.StatusBadRequest)
		}
		// 5th wrong attempt locks out OTP (409), and even real OTP is rejected afterwards!
		env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/bank-account/verify-otp", seller1.AccessToken, nil, usrW2BVerifyBankOTPReq{
			OTP: wrongOTP,
		}, http.StatusConflict)
		env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/bank-account/verify-otp", seller1.AccessToken, nil, usrW2BVerifyBankOTPReq{
			OTP: realOTP,
		}, http.StatusConflict)
		// Superseded/locked PENDING_OTP is omitted on GET (404), and seller can immediately re-submit in the next step.
		env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/bank-account", seller1.AccessToken, nil, nil, http.StatusNotFound)
	})

	t.Run("MatchNameVerifyThenChangeLocksPayout24h", func(t *testing.T) {
		rawAcc1 := "19034567891122"
		add1 := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/bank-account", seller1.AccessToken, nil, usrW2BAddBankAccountReq{
			BankCode:          "VCB",
			BankName:          "Vietcombank",
			AccountNumber:     rawAcc1,
			AccountHolderName: "NGUYEN VAN ANH DAT",
		}, http.StatusCreated, http.StatusOK)
		usrW2BAssertNoRawLeak(t, "POST /seller/bank-account #1", add1.Body, rawAcc1)

		_, otp1 := usrW2BWaitNewOTPEmail(t, env, seller1.Email, seenMail)
		ver1 := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/bank-account/verify-otp", seller1.AccessToken, nil, usrW2BVerifyBankOTPReq{
			OTP: otp1,
		}, http.StatusOK)
		usrW2BAssertNoRawLeak(t, "verify-otp #1", ver1.Body, rawAcc1, otp1)

		var ver1Out usrW2BBankAccountResp
		if err := DecodeJSON(ver1, &ver1Out); err != nil {
			t.Fatalf("decode verify #1: %v", err)
		}
		if !StatusEquals(ver1Out.BankAccount.Status, "VERIFIED") || !ver1Out.BankAccount.HolderNameMatched ||
			ver1Out.BankAccount.AccountNumberMasked != "****1122" || ver1Out.BankAccount.PayoutLockedUntil != "" {
			t.Fatalf("unexpected verified bank account #1: %+v", ver1Out.BankAccount)
		}

		// GET seller & admin bank account responses must mask raw account number.
		getSeller := env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/bank-account", seller1.AccessToken, nil, nil, http.StatusOK)
		usrW2BAssertNoRawLeak(t, "GET /seller/bank-account", getSeller.Body, rawAcc1)
		adminGetPath := fmt.Sprintf("/api/v1/admin/shops/%s/bank-account", url.PathEscape(seller1.ShopID))
		getAdmin := env.mustDoJSON(t, http.MethodGet, adminGetPath, adminTok, nil, nil, http.StatusOK)
		usrW2BAssertNoRawLeak(t, "GET /admin/shops/{id}/bank-account", getAdmin.Body, rawAcc1)

		// Service token payout RPC returns raw account number.
		payout1, err := userClient.GetShopBankAccountForPayout(usrW2BWithBearer(env.ctx(t), svcTok), &userv1.GetShopBankAccountForPayoutRequest{
			ShopId: seller1.ShopID,
		})
		if err != nil || payout1.GetBankAccount().GetAccountNumber() != rawAcc1 {
			t.Fatalf("GetShopBankAccountForPayout #1 failed: err=%v acc=%+v", err, payout1)
		}

		// Change verified bank account -> sets payout_locked_until (~24h).
		rawAcc2 := "19034567893344"
		add2 := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/bank-account", seller1.AccessToken, nil, usrW2BAddBankAccountReq{
			BankCode:          "TCB",
			BankName:          "Techcombank",
			AccountNumber:     rawAcc2,
			AccountHolderName: "NGUYEN VAN ANH DAT",
		}, http.StatusCreated, http.StatusOK)
		usrW2BAssertNoRawLeak(t, "POST /seller/bank-account #2", add2.Body, rawAcc2)

		_, otp2 := usrW2BWaitNewOTPEmail(t, env, seller1.Email, seenMail)
		ver2 := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/bank-account/verify-otp", seller1.AccessToken, nil, usrW2BVerifyBankOTPReq{
			OTP: otp2,
		}, http.StatusOK)
		usrW2BAssertNoRawLeak(t, "verify-otp #2", ver2.Body, rawAcc2, otp2)

		var ver2Out usrW2BBankAccountResp
		if err := DecodeJSON(ver2, &ver2Out); err != nil {
			t.Fatalf("decode verify #2: %v", err)
		}
		if !StatusEquals(ver2Out.BankAccount.Status, "VERIFIED") || ver2Out.BankAccount.PayoutLockedUntil == "" {
			t.Fatalf("changing verified account must set payout_locked_until: %+v", ver2Out.BankAccount)
		}
		lockTime, err := time.Parse(time.RFC3339, ver2Out.BankAccount.PayoutLockedUntil)
		if err != nil {
			t.Fatalf("parse payout_locked_until %q: %v", ver2Out.BankAccount.PayoutLockedUntil, err)
		}
		if remaining := time.Until(lockTime); remaining < 23*time.Hour || remaining > 25*time.Hour {
			t.Fatalf("expected ~24h payout lock, got remaining=%s", remaining)
		}

		payout2, err := userClient.GetShopBankAccountForPayout(usrW2BWithBearer(env.ctx(t), svcTok), &userv1.GetShopBankAccountForPayoutRequest{
			ShopId: seller1.ShopID,
		})
		if err != nil || payout2.GetBankAccount().GetAccountNumber() != rawAcc2 {
			t.Fatalf("GetShopBankAccountForPayout #2 must reflect new verified account: err=%v acc=%+v", err, payout2)
		}
	})

	t.Run("MismatchNamePendingReviewAdminApproveAndAuditRedaction", func(t *testing.T) {
		rawAcc := "21510008885566"
		addResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/bank-account", seller2.AccessToken, nil, usrW2BAddBankAccountReq{
			BankCode:          "BIDV",
			BankName:          "BIDV",
			AccountNumber:     rawAcc,
			AccountHolderName: "CONG TY TNHH KHONG KHOP TEN",
		}, http.StatusCreated, http.StatusOK)
		usrW2BAssertNoRawLeak(t, "POST /seller/bank-account mismatch", addResp.Body, rawAcc)

		var addOut usrW2BAddBankAccountResp
		if err := DecodeJSON(addResp, &addOut); err != nil {
			t.Fatalf("decode mismatch add: %v", err)
		}

		_, otp := usrW2BWaitNewOTPEmail(t, env, seller2.Email, seenMail)
		verResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/bank-account/verify-otp", seller2.AccessToken, nil, usrW2BVerifyBankOTPReq{
			OTP: otp,
		}, http.StatusOK)
		usrW2BAssertNoRawLeak(t, "verify-otp mismatch", verResp.Body, rawAcc, otp)

		var verOut usrW2BBankAccountResp
		if err := DecodeJSON(verResp, &verOut); err != nil {
			t.Fatalf("decode mismatch verify: %v", err)
		}
		if !StatusEquals(verOut.BankAccount.Status, "PENDING_ADMIN_REVIEW") || verOut.BankAccount.HolderNameMatched {
			t.Fatalf("mismatched holder name must enter PENDING_ADMIN_REVIEW: %+v", verOut.BankAccount)
		}

		// Cross-shop / RBAC 403 checks via gateway.
		adminShopBankPath := fmt.Sprintf("/api/v1/admin/shops/%s/bank-account", url.PathEscape(seller2.ShopID))
		adminReviewPath := fmt.Sprintf("/api/v1/admin/shops/%s/bank-account/review", url.PathEscape(seller2.ShopID))
		env.mustDoJSON(t, http.MethodGet, adminShopBankPath, seller1.AccessToken, nil, nil, http.StatusForbidden)
		env.mustDoJSON(t, http.MethodPost, adminReviewPath, seller1.AccessToken, nil, usrW2BAdminReviewBankReq{
			BankAccountID: addOut.BankAccount.ID, Decision: "APPROVE",
		}, http.StatusForbidden)
		env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/bank-account", buyer.AccessToken, nil, nil, http.StatusForbidden)

		// Admin approves -> VERIFIED (masked in response).
		revResp := env.mustDoJSON(t, http.MethodPost, adminReviewPath, adminTok, nil, usrW2BAdminReviewBankReq{
			BankAccountID: addOut.BankAccount.ID,
			Decision:      "APPROVE",
			Reason:        "E2E verified company license",
		}, http.StatusOK)
		usrW2BAssertNoRawLeak(t, "admin review bank account", revResp.Body, rawAcc)
		var revOut usrW2BBankAccountResp
		if err := DecodeJSON(revResp, &revOut); err != nil {
			t.Fatalf("decode admin review: %v", err)
		}
		if !StatusEquals(revOut.BankAccount.Status, "VERIFIED") {
			t.Fatalf("expected VERIFIED after admin approve, got %+v", revOut.BankAccount)
		}

		// Audit logs must record actions without leaking raw account numbers or OTPs.
		auditResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/audit-logs?target_type=shop_bank_account&page_size=20", adminTok, nil, nil, http.StatusOK)
		usrW2BAssertNoRawLeak(t, "GET /admin/audit-logs", auditResp.Body, rawAcc, otp)
		var auditOut usrW2BAuditLogsResp
		if err := DecodeJSON(auditResp, &auditOut); err != nil {
			t.Fatalf("decode audit logs: %v", err)
		}
		if auditOut.Total == 0 || len(auditOut.Items) == 0 {
			t.Fatalf("expected audit_logs entries for shop_bank_account")
		}
	})

	// Changing user full_name AFTER admin approved the shop must NOT bypass verified_owner_name matching:
	// submitting a bank account under the newly mutated profile name must enter PENDING_ADMIN_REVIEW.
	t.Run("FullNameChangedAfterApproval_EntersPendingAdminReview", func(t *testing.T) {
		seller3 := usrW2BCreateApprovedSellerWithName(t, env, "usrw2bpostname", "Trần Minh Khôi")
		env.mustDoJSON(t, http.MethodPatch, "/api/v1/users/me", seller3.AccessToken, nil, map[string]string{
			"full_name": "Lê Hoàng Bảo",
		}, http.StatusOK)

		// Snapshot Mailpit AFTER creating seller3 so we ignore seller3's registration OTP email.
		seenMail3 := usrW2BSnapshotMailpitIDs(t, env)

		rawAcc := "19034567896677"
		env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/bank-account", seller3.AccessToken, nil, usrW2BAddBankAccountReq{
			BankCode:          "ACB",
			BankName:          "ACB",
			AccountNumber:     rawAcc,
			AccountHolderName: "LE HOANG BAO",
		}, http.StatusCreated, http.StatusOK)

		_, otp := usrW2BWaitNewOTPEmail(t, env, seller3.Email, seenMail3)
		verResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/bank-account/verify-otp", seller3.AccessToken, nil, usrW2BVerifyBankOTPReq{
			OTP: otp,
		}, http.StatusOK)
		var verOut usrW2BBankAccountResp
		if err := DecodeJSON(verResp, &verOut); err != nil {
			t.Fatalf("decode post-approval name change verify: %v", err)
		}
		if !StatusEquals(verOut.BankAccount.Status, "PENDING_ADMIN_REVIEW") || verOut.BankAccount.HolderNameMatched {
			t.Fatalf("changing full_name after shop approval must NOT auto-verify; want PENDING_ADMIN_REVIEW & holder_name_matched=false, got %+v", verOut.BankAccount)
		}
	})
}
