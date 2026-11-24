package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/secretbox"
	"github.com/nexusmart/core/services/user/internal/db"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func assertNoSensitiveLeak(t *testing.T, where, payload string, secrets ...string) {
	t.Helper()
	for _, s := range secrets {
		if s != "" && strings.Contains(payload, s) {
			t.Fatalf("%s leaked sensitive value (len=%d)", where, len(s))
		}
	}
}

func TestW3A_D6_AdminReviewShopBankAccount_RequiresIDAndRaceStale409(t *testing.T) {
	clk := newW2BClock(time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC))
	srv, store, _, pub, _ := newW2BHarness(t, Options{
		Now:                    clk.Now,
		OTPTTL:                 10 * time.Minute,
		BankPayoutLockDuration: 24 * time.Hour,
	})

	admin, _ := seedUserAndShop(store, "Admin D6", "admin-d6-bank@example.com", RoleAdmin, StatusActive, "", clk.Now())
	owner, shop := seedUserAndShop(store, "Nguyễn Văn Chủ Shop", "seller-d6-bank@example.com", RoleSeller, StatusActive, ShopStatusActive, clk.Now())
	adminCtx := ctxWithUser(admin.ID, RoleAdmin, "")
	sellerCtx := ctxWithUser(owner.ID, RoleSeller, shop.ID)

	// Seller submits bank account A (mismatched holder name) -> verifies OTP -> PENDING_ADMIN_REVIEW.
	addA, err := srv.AddShopBankAccount(sellerCtx, &userv1.AddShopBankAccountRequest{
		BankCode:          "VCB",
		BankName:          "Vietcombank",
		AccountNumber:     "19034567891111",
		AccountHolderName: "CONG TY TNHH A",
	})
	if err != nil {
		t.Fatalf("AddShopBankAccount A: %v", err)
	}
	if _, err := srv.VerifyShopBankAccountOTP(sellerCtx, &userv1.VerifyShopBankAccountOTPRequest{
		Otp: pub.shopBankAccountOTP[len(pub.shopBankAccountOTP)-1].OTP,
	}); err != nil {
		t.Fatalf("VerifyShopBankAccountOTP A: %v", err)
	}
	accAID := addA.GetBankAccount().GetId()

	// 1. Missing or blank bank_account_id MUST be rejected with InvalidArgument (HTTP 400) — never fall back!
	for _, blank := range []string{"", "   "} {
		if _, err := srv.AdminReviewShopBankAccount(adminCtx, &userv1.AdminReviewShopBankAccountRequest{
			ShopId:        shop.ID,
			BankAccountId: blank,
			Decision:      userv1.ReviewDecision_REVIEW_DECISION_APPROVE,
			Reason:        "missing id",
		}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("AdminReviewShopBankAccount bank_account_id=%q: want InvalidArgument, got %v", blank, err)
		}
	}

	// Admin views A while A is PENDING_ADMIN_REVIEW.
	viewedA, err := srv.GetShopBankAccount(adminCtx, &userv1.GetShopBankAccountRequest{ShopId: shop.ID})
	if err != nil || viewedA.GetBankAccount().GetId() != accAID {
		t.Fatalf("admin GetShopBankAccount before race: want A (%s), got err=%v resp=%+v", accAID, err, viewedA)
	}

	// 2. Race: while admin is reviewing A, seller submits B (mismatched) and verifies OTP -> A becomes SUPERSEDED, B becomes PENDING_ADMIN_REVIEW.
	clk.Advance(time.Minute)
	addB, err := srv.AddShopBankAccount(sellerCtx, &userv1.AddShopBankAccountRequest{
		BankCode:          "TCB",
		BankName:          "Techcombank",
		AccountNumber:     "19034567892222",
		AccountHolderName: "CONG TY TNHH B",
	})
	if err != nil {
		t.Fatalf("AddShopBankAccount B: %v", err)
	}
	if _, err := srv.VerifyShopBankAccountOTP(sellerCtx, &userv1.VerifyShopBankAccountOTPRequest{
		Otp: pub.shopBankAccountOTP[len(pub.shopBankAccountOTP)-1].OTP,
	}); err != nil {
		t.Fatalf("VerifyShopBankAccountOTP B: %v", err)
	}
	accBID := addB.GetBankAccount().GetId()

	// Admin now attempts to approve OR reject stale A -> MUST return FailedPrecondition (409) and ZERO rows VERIFIED!
	if _, err := srv.AdminReviewShopBankAccount(adminCtx, &userv1.AdminReviewShopBankAccountRequest{
		ShopId:        shop.ID,
		BankAccountId: accAID,
		Decision:      userv1.ReviewDecision_REVIEW_DECISION_APPROVE,
		Reason:        "approving stale A",
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("AdminReviewShopBankAccount approve stale A: want FailedPrecondition, got %v", err)
	}
	if _, err := srv.AdminReviewShopBankAccount(adminCtx, &userv1.AdminReviewShopBankAccountRequest{
		ShopId:        shop.ID,
		BankAccountId: accAID,
		Decision:      userv1.ReviewDecision_REVIEW_DECISION_REJECT,
		Reason:        "rejecting stale A",
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("AdminReviewShopBankAccount reject stale A: want FailedPrecondition, got %v", err)
	}

	store.mu.Lock()
	rowA := store.shopBankAccounts[accAID]
	rowB := store.shopBankAccounts[accBID]
	verifiedCount := 0
	for _, acc := range store.shopBankAccounts {
		if acc.ShopID == shop.ID && acc.Status == BankAccountStatusVerified {
			verifiedCount++
		}
	}
	store.mu.Unlock()
	if verifiedCount != 0 {
		t.Fatalf("stale A review left %d VERIFIED row(s); want 0", verifiedCount)
	}
	if rowA.Status != BankAccountStatusSuperseded || rowB.Status != BankAccountStatusPendingAdminReview {
		t.Fatalf("unexpected statuses after stale review: A=%q B=%q", rowA.Status, rowB.Status)
	}

	// Admin approving current B succeeds; re-approving B returns FailedPrecondition (409).
	approvedB, err := srv.AdminReviewShopBankAccount(adminCtx, &userv1.AdminReviewShopBankAccountRequest{
		ShopId:        shop.ID,
		BankAccountId: accBID,
		Decision:      userv1.ReviewDecision_REVIEW_DECISION_APPROVE,
		Reason:        "verified B",
	})
	if err != nil || approvedB.GetBankAccount().GetStatus() != userv1.ShopBankAccountStatus_SHOP_BANK_ACCOUNT_STATUS_VERIFIED {
		t.Fatalf("approve current B: err=%v resp=%+v", err, approvedB)
	}
	if _, err := srv.AdminReviewShopBankAccount(adminCtx, &userv1.AdminReviewShopBankAccountRequest{
		ShopId:        shop.ID,
		BankAccountId: accBID,
		Decision:      userv1.ReviewDecision_REVIEW_DECISION_APPROVE,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("re-approve already VERIFIED B: want FailedPrecondition, got %v", err)
	}

	// 3. Race variant: seller adds C (mismatched -> PENDING_ADMIN_REVIEW), admin views C,
	// seller switches to D (matching owner name -> auto-VERIFIED, supersedes C), admin approves stale C -> 409 and D remains the sole VERIFIED row.
	clk.Advance(time.Minute)
	addC, err := srv.AddShopBankAccount(sellerCtx, &userv1.AddShopBankAccountRequest{
		BankCode:          "MB",
		BankName:          "MB Bank",
		AccountNumber:     "19034567893333",
		AccountHolderName: "CONG TY TNHH C",
	})
	if err != nil {
		t.Fatalf("AddShopBankAccount C: %v", err)
	}
	if _, err := srv.VerifyShopBankAccountOTP(sellerCtx, &userv1.VerifyShopBankAccountOTPRequest{
		Otp: pub.shopBankAccountOTP[len(pub.shopBankAccountOTP)-1].OTP,
	}); err != nil {
		t.Fatalf("VerifyShopBankAccountOTP C: %v", err)
	}
	clk.Advance(time.Minute)
	addD, err := srv.AddShopBankAccount(sellerCtx, &userv1.AddShopBankAccountRequest{
		BankCode:          "ACB",
		BankName:          "ACB",
		AccountNumber:     "19034567894444",
		AccountHolderName: "NGUYEN VAN CHU SHOP",
	})
	if err != nil {
		t.Fatalf("AddShopBankAccount D: %v", err)
	}
	verD, err := srv.VerifyShopBankAccountOTP(sellerCtx, &userv1.VerifyShopBankAccountOTPRequest{
		Otp: pub.shopBankAccountOTP[len(pub.shopBankAccountOTP)-1].OTP,
	})
	if err != nil || verD.GetBankAccount().GetStatus() != userv1.ShopBankAccountStatus_SHOP_BANK_ACCOUNT_STATUS_VERIFIED {
		t.Fatalf("VerifyShopBankAccountOTP D: err=%v status=%v", err, verD.GetBankAccount().GetStatus())
	}
	if _, err := srv.AdminReviewShopBankAccount(adminCtx, &userv1.AdminReviewShopBankAccountRequest{
		ShopId:        shop.ID,
		BankAccountId: addC.GetBankAccount().GetId(),
		Decision:      userv1.ReviewDecision_REVIEW_DECISION_APPROVE,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("approve stale C after matching D verified: want FailedPrecondition, got %v", err)
	}
	payoutD, err := srv.GetShopBankAccountForPayout(ctxWithService("payment"), &userv1.GetShopBankAccountForPayoutRequest{ShopId: shop.ID})
	if err != nil || payoutD.GetBankAccount().GetId() != addD.GetBankAccount().GetId() || payoutD.GetBankAccount().GetAccountNumber() != "19034567894444" {
		t.Fatalf("expected D to remain sole verified payout account, got err=%v resp=%+v", err, payoutD)
	}
}

func TestW3A_D6_AdminReviewShop_ExpectedOwnerNameCheckAndRace(t *testing.T) {
	clk := newW2BClock(time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC))
	srv, store, _, pub, _ := newW2BHarness(t, Options{Now: clk.Now})

	admin, _ := seedUserAndShop(store, "Admin Shop D6", "admin-d6-shop@example.com", RoleAdmin, StatusActive, "", clk.Now())
	adminCtx := ctxWithUser(admin.ID, RoleAdmin, "")

	ownerID := registerActiveUser(t, srv, pub, "owner-d6-shop@example.com", "0908111222")
	ownerCtx := ctxWithUser(ownerID, RoleBuyer, "")
	if _, err := srv.UpdateProfile(ownerCtx, &userv1.UpdateProfileRequest{FullName: "Nguyễn Văn Gốc"}); err != nil {
		t.Fatalf("UpdateProfile initial: %v", err)
	}
	addr, err := srv.CreateAddress(ownerCtx, &userv1.CreateAddressRequest{
		RecipientName: "Nguyễn Văn Gốc", RecipientPhone: "0908111222",
		ProvinceCode: "01", WardCode: "00001", StreetAddress: "123 Phố Huế", IsDefault: true,
	})
	if err != nil {
		t.Fatalf("CreateAddress: %v", err)
	}
	reg, err := srv.RegisterShop(ownerCtx, &userv1.RegisterShopRequest{
		Name: "Shop D6 Owner Name Race", PickupAddressId: addr.GetAddress().GetId(),
		ContactPhone: "0908111222", BusinessLicenseDoc: "https://example.com/gpkd.pdf",
	})
	if err != nil {
		t.Fatalf("RegisterShop: %v", err)
	}
	shopID := reg.GetShop().GetId()

	// Race: owner changes full_name between admin opening the review screen and clicking APPROVE.
	if _, err := srv.UpdateProfile(ownerCtx, &userv1.UpdateProfileRequest{FullName: "Trần Kẻ Đổi Tên"}); err != nil {
		t.Fatalf("UpdateProfile race: %v", err)
	}

	eventsBefore := len(pub.shopReviewed)
	store.mu.Lock()
	auditsBefore := len(store.auditLogs)
	store.mu.Unlock()

	_, err = srv.AdminReviewShop(adminCtx, &userv1.AdminReviewShopRequest{
		ShopId:            shopID,
		Decision:          userv1.ReviewDecision_REVIEW_DECISION_APPROVE,
		ExpectedOwnerName: "Nguyễn Văn Gốc",
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("AdminReviewShop with stale expected_owner_name: want FailedPrecondition, got %v", err)
	}
	if !strings.Contains(status.Convert(err).Message(), "OWNER_NAME_CHANGED") {
		t.Fatalf("AdminReviewShop mismatch error must contain OWNER_NAME_CHANGED, got %q", status.Convert(err).Message())
	}

	// Verify shop was NOT approved, user was NOT promoted, no audit log, no event!
	store.mu.Lock()
	shAfter := store.shops[shopID]
	ownerAfter := store.users[ownerID]
	auditsAfter := len(store.auditLogs)
	store.mu.Unlock()
	if shAfter.Status != ShopStatusPending || shAfter.VerifiedOwnerName != "" {
		t.Fatalf("shop mutated on OWNER_NAME_CHANGED: status=%q verified_owner_name=%q", shAfter.Status, shAfter.VerifiedOwnerName)
	}
	if ownerAfter.Role != RoleBuyer || ownerAfter.ShopID != nil {
		t.Fatalf("owner promoted on OWNER_NAME_CHANGED: role=%q shop_id=%v", ownerAfter.Role, ownerAfter.ShopID)
	}
	if auditsAfter != auditsBefore || len(pub.shopReviewed) != eventsBefore {
		t.Fatalf("side effects occurred on OWNER_NAME_CHANGED: audits=%d->%d events=%d->%d",
			auditsBefore, auditsAfter, eventsBefore, len(pub.shopReviewed))
	}

	// Tone-mark-only change must ALSO be caught (exact trimmed comparison, not diacritic-folded).
	if _, err := srv.UpdateProfile(ownerCtx, &userv1.UpdateProfileRequest{FullName: "Nguyễn Văn À"}); err != nil {
		t.Fatalf("UpdateProfile tone change: %v", err)
	}
	if _, err := srv.AdminReviewShop(adminCtx, &userv1.AdminReviewShopRequest{
		ShopId:            shopID,
		Decision:          userv1.ReviewDecision_REVIEW_DECISION_APPROVE,
		ExpectedOwnerName: "Nguyễn Văn Á",
	}); status.Code(err) != codes.FailedPrecondition || !strings.Contains(status.Convert(err).Message(), "OWNER_NAME_CHANGED") {
		t.Fatalf("tone-mark mismatch must return OWNER_NAME_CHANGED, got %v", err)
	}

	// REQUEST_INFO and REJECT still succeed even when expected_owner_name differs (guard applies to APPROVE only).
	if _, err := srv.AdminReviewShop(adminCtx, &userv1.AdminReviewShopRequest{
		ShopId:            shopID,
		Decision:          userv1.ReviewDecision_REVIEW_DECISION_REQUEST_INFO,
		Reason:            "Xác nhận lại tên chủ hộ",
		ExpectedOwnerName: "Tên Cũ Không Khớp",
	}); err != nil {
		t.Fatalf("REQUEST_INFO with mismatched expected_owner_name should succeed, got %v", err)
	}

	// APPROVE with matching current full_name (ignoring surrounding whitespace) succeeds and snapshots verified_owner_name.
	okResp, err := srv.AdminReviewShop(adminCtx, &userv1.AdminReviewShopRequest{
		ShopId:            shopID,
		Decision:          userv1.ReviewDecision_REVIEW_DECISION_APPROVE,
		ExpectedOwnerName: "  Nguyễn Văn À  ",
	})
	if err != nil || okResp.GetShop().GetStatus() != userv1.ShopStatus_SHOP_STATUS_ACTIVE {
		t.Fatalf("APPROVE with matching expected_owner_name failed: err=%v resp=%+v", err, okResp)
	}
	store.mu.Lock()
	verifiedSnap := store.shops[shopID].VerifiedOwnerName
	store.mu.Unlock()
	if verifiedSnap != "Nguyễn Văn À" {
		t.Fatalf("verified_owner_name snapshot: want %q, got %q", "Nguyễn Văn À", verifiedSnap)
	}

	// Second shop: REJECT works even with mismatched expected_owner_name; empty expected_owner_name on APPROVE remains optional.
	owner2ID := registerActiveUser(t, srv, pub, "owner2-d6-shop@example.com", "0908111333")
	owner2Ctx := ctxWithUser(owner2ID, RoleBuyer, "")
	addr2, err := srv.CreateAddress(owner2Ctx, &userv1.CreateAddressRequest{
		RecipientName: "Owner Two", RecipientPhone: "0908111333",
		ProvinceCode: "01", WardCode: "00001", StreetAddress: "456 Ba Trieu", IsDefault: true,
	})
	if err != nil {
		t.Fatalf("CreateAddress 2: %v", err)
	}
	reg2, err := srv.RegisterShop(owner2Ctx, &userv1.RegisterShopRequest{
		Name: "Shop D6 Reject And Optional", PickupAddressId: addr2.GetAddress().GetId(),
		ContactPhone: "0908111333", BusinessLicenseDoc: "https://example.com/gpkd2.pdf",
	})
	if err != nil {
		t.Fatalf("RegisterShop 2: %v", err)
	}
	if _, err := srv.AdminReviewShop(adminCtx, &userv1.AdminReviewShopRequest{
		ShopId:            reg2.GetShop().GetId(),
		Decision:          userv1.ReviewDecision_REVIEW_DECISION_REJECT,
		Reason:            "Từ chối hồ sơ",
		ExpectedOwnerName: "Tên Khác Hoàn Toàn",
	}); err != nil {
		t.Fatalf("REJECT with mismatched expected_owner_name should succeed, got %v", err)
	}
	if _, err := srv.RegisterShop(owner2Ctx, &userv1.RegisterShopRequest{
		Name: "Shop D6 Reject And Optional", PickupAddressId: addr2.GetAddress().GetId(),
		ContactPhone: "0908111333", BusinessLicenseDoc: "https://example.com/gpkd2b.pdf",
	}); err != nil {
		t.Fatalf("RegisterShop 2 resubmit: %v", err)
	}
	if _, err := srv.AdminReviewShop(adminCtx, &userv1.AdminReviewShopRequest{
		ShopId:   reg2.GetShop().GetId(),
		Decision: userv1.ReviewDecision_REVIEW_DECISION_APPROVE,
	}); err != nil {
		t.Fatalf("APPROVE with empty optional expected_owner_name should succeed, got %v", err)
	}
}

func TestW3A_D5_NewBankAccountNotStoredPlaintext(t *testing.T) {
	clk := newW2BClock(time.Date(2026, 11, 24, 11, 0, 0, 0, time.UTC))
	srv, store, _, pub, _ := newW2BHarness(t, Options{Now: clk.Now, OTPTTL: 10 * time.Minute})
	owner, shop := seedUserAndShop(store, "Nguyễn Văn Mã Hóa", "seller-d5-plain@example.com", RoleSeller, StatusActive, ShopStatusActive, clk.Now())
	sellerCtx := ctxWithUser(owner.ID, RoleSeller, shop.ID)

	rawAcc := "19034567891234"
	addResp, err := srv.AddShopBankAccount(sellerCtx, &userv1.AddShopBankAccountRequest{
		BankCode:          "VCB",
		BankName:          "Vietcombank",
		AccountNumber:     rawAcc,
		AccountHolderName: "NGUYEN VAN MA HOA",
	})
	if err != nil {
		t.Fatalf("AddShopBankAccount: %v", err)
	}

	store.mu.Lock()
	rec := store.shopBankAccounts[addResp.GetBankAccount().GetId()]
	store.mu.Unlock()
	if rec.AccountNumber != "" {
		t.Fatalf("D5 violation: new row stored plaintext account_number (len=%d); must be empty", len(rec.AccountNumber))
	}
	if len(rec.AccountNumberEnc) < 28 || bytes.Contains(rec.AccountNumberEnc, []byte(rawAcc)) {
		t.Fatalf("D5 violation: expected AES-GCM ciphertext without plaintext, got len=%d", len(rec.AccountNumberEnc))
	}

	otp := pub.shopBankAccountOTP[len(pub.shopBankAccountOTP)-1].OTP
	if _, err := srv.VerifyShopBankAccountOTP(sellerCtx, &userv1.VerifyShopBankAccountOTPRequest{Otp: otp}); err != nil {
		t.Fatalf("VerifyShopBankAccountOTP: %v", err)
	}
	payoutResp, err := srv.GetShopBankAccountForPayout(ctxWithService("payment"), &userv1.GetShopBankAccountForPayoutRequest{ShopId: shop.ID})
	if err != nil || payoutResp.GetBankAccount().GetAccountNumber() != rawAcc {
		t.Fatalf("GetShopBankAccountForPayout: err=%v got len=%d want len=%d", err, len(payoutResp.GetBankAccount().GetAccountNumber()), len(rawAcc))
	}
}

func TestW3A_D5_OnlyPayoutDecryptsAndDecryptFailureGenericInternal(t *testing.T) {
	clk := newW2BClock(time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC))
	box1 := newTestBankAccountBox(t)
	wrongKey := bytes.Repeat([]byte{0x5a}, 32)
	wrongBox, err := secretbox.New(wrongKey, "BANK_ACCOUNT_KEY")
	if err != nil {
		t.Fatalf("secretbox.New wrongBox: %v", err)
	}

	srv, store, _, pub, logBuf := newW2BHarness(t, Options{
		Now:            clk.Now,
		OTPTTL:         10 * time.Minute,
		BankAccountBox: box1,
	})
	admin, _ := seedUserAndShop(store, "Admin Dec", "admin-dec@example.com", RoleAdmin, StatusActive, "", clk.Now())
	owner, shop := seedUserAndShop(store, "Trần Thị Giải Mã", "seller-dec@example.com", RoleSeller, StatusActive, ShopStatusActive, clk.Now())
	adminCtx := ctxWithUser(admin.ID, RoleAdmin, "")
	sellerCtx := ctxWithUser(owner.ID, RoleSeller, shop.ID)
	svcCtx := ctxWithService("payment")

	rawAcc := "88112233445566"
	addResp, err := srv.AddShopBankAccount(sellerCtx, &userv1.AddShopBankAccountRequest{
		BankCode:          "TCB",
		BankName:          "Techcombank",
		AccountNumber:     rawAcc,
		AccountHolderName: "TRAN THI GIAI MA",
	})
	if err != nil {
		t.Fatalf("AddShopBankAccount: %v", err)
	}
	otp := pub.shopBankAccountOTP[len(pub.shopBankAccountOTP)-1].OTP
	if _, err := srv.VerifyShopBankAccountOTP(sellerCtx, &userv1.VerifyShopBankAccountOTPRequest{Otp: otp}); err != nil {
		t.Fatalf("VerifyShopBankAccountOTP: %v", err)
	}

	store.mu.Lock()
	encBytes := append([]byte(nil), store.shopBankAccounts[addResp.GetBankAccount().GetId()].AccountNumberEnc...)
	store.mu.Unlock()

	// Swap server cipher to wrongBox:
	// 1. GetShopBankAccount (seller & admin) MUST still succeed and return masked number, proving they NEVER decrypt!
	srv.bankBox = wrongBox
	if resp, err := srv.GetShopBankAccount(sellerCtx, &userv1.GetShopBankAccountRequest{}); err != nil ||
		resp.GetBankAccount().GetAccountNumber() != "" || resp.GetBankAccount().GetAccountNumberMasked() != "****5566" {
		t.Fatalf("seller GetShopBankAccount with wrong key should not decrypt: err=%v resp=%+v", err, resp)
	}
	if resp, err := srv.GetShopBankAccount(adminCtx, &userv1.GetShopBankAccountRequest{ShopId: shop.ID}); err != nil ||
		resp.GetBankAccount().GetAccountNumber() != "" || resp.GetBankAccount().GetAccountNumberMasked() != "****5566" {
		t.Fatalf("admin GetShopBankAccount with wrong key should not decrypt: err=%v resp=%+v", err, resp)
	}

	// 2. GetShopBankAccountForPayout MUST fail with generic Internal error without leaking plaintext, ciphertext, or key!
	_, decErr := srv.GetShopBankAccountForPayout(svcCtx, &userv1.GetShopBankAccountForPayoutRequest{ShopId: shop.ID})
	if status.Code(decErr) != codes.Internal {
		t.Fatalf("GetShopBankAccountForPayout wrong key: want Internal, got %v", decErr)
	}
	if status.Convert(decErr).Message() != "failed to load verified shop bank account" {
		t.Fatalf("GetShopBankAccountForPayout error must be generic, got %q", status.Convert(decErr).Message())
	}

	// 3. Corrupted/truncated ciphertext and empty ciphertext also return generic Internal.
	store.mu.Lock()
	acc := store.shopBankAccounts[addResp.GetBankAccount().GetId()]
	acc.AccountNumberEnc = []byte("short")
	store.shopBankAccounts[acc.ID] = acc
	store.mu.Unlock()
	srv.bankBox = box1
	if _, err := srv.GetShopBankAccountForPayout(svcCtx, &userv1.GetShopBankAccountForPayoutRequest{ShopId: shop.ID}); status.Code(err) != codes.Internal ||
		status.Convert(err).Message() != "failed to load verified shop bank account" {
		t.Fatalf("truncated ciphertext payout: want generic Internal, got %v", err)
	}

	store.mu.Lock()
	acc.AccountNumberEnc = nil
	store.shopBankAccounts[acc.ID] = acc
	store.mu.Unlock()
	if _, err := srv.GetShopBankAccountForPayout(svcCtx, &userv1.GetShopBankAccountForPayoutRequest{ShopId: shop.ID}); status.Code(err) != codes.Internal ||
		status.Convert(err).Message() != "failed to load verified shop bank account" {
		t.Fatalf("nil ciphertext payout: want generic Internal, got %v", err)
	}

	// 4. Nil bankBox fails closed on AddShopBankAccount (never stores plaintext).
	srv.bankBox = nil
	if _, err := srv.AddShopBankAccount(sellerCtx, &userv1.AddShopBankAccountRequest{
		BankCode:          "VCB",
		BankName:          "Vietcombank",
		AccountNumber:     "99887766554433",
		AccountHolderName: "TRAN THI GIAI MA",
	}); status.Code(err) != codes.Internal {
		t.Fatalf("AddShopBankAccount with nil bankBox: want Internal, got %v", err)
	}

	forbidden := []string{
		rawAcc,
		"99887766554433",
		string(encBytes),
		base64.StdEncoding.EncodeToString(encBytes),
		base64.RawStdEncoding.EncodeToString(encBytes),
		hex.EncodeToString(encBytes),
		testBankAccountKeyB64,
		base64.StdEncoding.EncodeToString(wrongKey),
		otp,
	}
	assertNoSensitiveLeak(t, "decrypt error message", decErr.Error(), forbidden...)
	assertNoSensitiveLeak(t, "logs", logBuf.String(), forbidden...)
}

func TestW3A_D5_NoPlaintextOrCiphertextInLogsAuditEventsErrors(t *testing.T) {
	clk := newW2BClock(time.Date(2026, 11, 24, 13, 0, 0, 0, time.UTC))
	box := newTestBankAccountBox(t)
	srv, store, _, pub, logBuf := newW2BHarness(t, Options{
		Now:            clk.Now,
		OTPTTL:         10 * time.Minute,
		BankAccountBox: box,
	})

	admin, _ := seedUserAndShop(store, "Admin Audit Leak", "admin-leak@example.com", RoleAdmin, StatusActive, "", clk.Now())
	owner, shop := seedUserAndShop(store, "Phạm Văn An Toàn", "seller-leak@example.com", RoleSeller, StatusActive, ShopStatusActive, clk.Now())
	adminCtx := ctxWithUser(admin.ID, RoleAdmin, "")
	sellerCtx := ctxWithUser(owner.ID, RoleSeller, shop.ID)
	svcCtx := ctxWithService("payment")

	rawAcc := "77665544332211"

	// Full lifecycle: add (mismatched) -> verify OTP -> get (seller + admin) -> admin approve -> payout -> publish error path.
	addResp, err := srv.AddShopBankAccount(sellerCtx, &userv1.AddShopBankAccountRequest{
		BankCode:          "MB",
		BankName:          "MB Bank",
		AccountNumber:     rawAcc,
		AccountHolderName: "CONG TY TNHH AN TOAN",
	})
	if err != nil {
		t.Fatalf("AddShopBankAccount: %v", err)
	}
	otp := pub.shopBankAccountOTP[len(pub.shopBankAccountOTP)-1].OTP
	verResp, err := srv.VerifyShopBankAccountOTP(sellerCtx, &userv1.VerifyShopBankAccountOTPRequest{Otp: otp})
	if err != nil {
		t.Fatalf("VerifyShopBankAccountOTP: %v", err)
	}
	getSel, err := srv.GetShopBankAccount(sellerCtx, &userv1.GetShopBankAccountRequest{})
	if err != nil {
		t.Fatalf("GetShopBankAccount seller: %v", err)
	}
	getAdm, err := srv.GetShopBankAccount(adminCtx, &userv1.GetShopBankAccountRequest{ShopId: shop.ID})
	if err != nil {
		t.Fatalf("GetShopBankAccount admin: %v", err)
	}
	revResp, err := srv.AdminReviewShopBankAccount(adminCtx, &userv1.AdminReviewShopBankAccountRequest{
		ShopId:        shop.ID,
		BankAccountId: addResp.GetBankAccount().GetId(),
		Decision:      userv1.ReviewDecision_REVIEW_DECISION_APPROVE,
		Reason:        "Đã kiểm tra hồ sơ",
	})
	if err != nil {
		t.Fatalf("AdminReviewShopBankAccount: %v", err)
	}
	if _, err := srv.GetShopBankAccountForPayout(svcCtx, &userv1.GetShopBankAccountForPayoutRequest{ShopId: shop.ID}); err != nil {
		t.Fatalf("GetShopBankAccountForPayout: %v", err)
	}

	// Trigger publish failure path too.
	pub.publishBankOTPErr = errors.New("nats down")
	rawAccFail := "66554433221100"
	_, pubErr := srv.AddShopBankAccount(sellerCtx, &userv1.AddShopBankAccountRequest{
		BankCode:          "VCB",
		BankName:          "Vietcombank",
		AccountNumber:     rawAccFail,
		AccountHolderName: "PHAM VAN AN TOAN",
	})
	pub.publishBankOTPErr = nil
	if status.Code(pubErr) != codes.Internal {
		t.Fatalf("publish failure path: want Internal, got %v", pubErr)
	}

	// Gather every stored ciphertext (raw, std base64, raw base64, hex) and plaintext.
	secrets := []string{rawAcc, rawAccFail, testBankAccountKeyB64}
	store.mu.Lock()
	for _, acc := range store.shopBankAccounts {
		if len(acc.AccountNumberEnc) > 0 {
			secrets = append(secrets,
				string(acc.AccountNumberEnc),
				base64.StdEncoding.EncodeToString(acc.AccountNumberEnc),
				base64.RawStdEncoding.EncodeToString(acc.AccountNumberEnc),
				hex.EncodeToString(acc.AccountNumberEnc),
			)
		}
	}
	audits := append([]db.AuditLog(nil), store.auditLogs...)
	store.mu.Unlock()

	secretsWithOTP := append(append([]string(nil), secrets...), otp)

	// 1. Logs: zero plaintext, ciphertext, key, or OTP.
	assertNoSensitiveLeak(t, "slog output", logBuf.String(), secretsWithOTP...)

	// 2. Audit logs: zero plaintext, ciphertext, key, or OTP.
	if len(audits) < 3 {
		t.Fatalf("expected at least 3 audit logs (add, verify, review), got %d", len(audits))
	}
	for _, a := range audits {
		b, _ := json.Marshal(a)
		assertNoSensitiveLeak(t, "audit log "+a.Action, string(b), secretsWithOTP...)
	}

	// 3. Events: zero plaintext account number, ciphertext, or key.
	for i, evt := range pub.shopBankAccountOTP {
		b, _ := json.Marshal(evt)
		assertNoSensitiveLeak(t, "event shop_bank_account_otp", string(b), secrets...)
		if i == 0 && evt.AccountNumberMasked != "****2211" {
			t.Fatalf("event account_number_masked = %q, want ****2211", evt.AccountNumberMasked)
		}
	}

	// 4. Non-payout gRPC responses and errors: zero plaintext account number, ciphertext, key, or OTP.
	for name, msg := range map[string]any{
		"AddShopBankAccount":         addResp,
		"VerifyShopBankAccountOTP":   verResp,
		"GetShopBankAccount(seller)": getSel,
		"GetShopBankAccount(admin)":  getAdm,
		"AdminReviewShopBankAccount": revResp,
	} {
		b, _ := json.Marshal(msg)
		assertNoSensitiveLeak(t, "response "+name, string(b), secretsWithOTP...)
	}
	assertNoSensitiveLeak(t, "publish failure error", pubErr.Error(), secretsWithOTP...)
}

func TestW3A_AdminListUsers_QueryByExactIDAndExistingFilters(t *testing.T) {
	clk := newW2BClock(time.Date(2026, 11, 24, 16, 0, 0, 0, time.UTC))
	srv, store, _, _, _ := newW2BHarness(t, Options{Now: clk.Now})

	admin, _ := seedUserAndShop(store, "Admin Lookup", "admin-lookup@example.com", RoleAdmin, StatusActive, "", clk.Now())
	superAdmin, _ := seedUserAndShop(store, "Super Lookup", "super-lookup@example.com", RoleSuperAdmin, StatusActive, "", clk.Now())
	u1, _ := seedUserAndShop(store, "Nguyễn Minh Khôi", "minhkhoi@example.com", RoleBuyer, StatusActive, "", clk.Now())
	u2, _ := seedUserAndShop(store, "Trần Thị Lan Anh", "lananh@example.com", RoleSeller, StatusActive, ShopStatusActive, clk.Now())
	// Seed a newer user carrying u1.ID in full_name and email to ensure UUID query matches id ONLY.
	clk.Advance(time.Hour)
	shadow, _ := seedUserAndShop(store, "Shadow "+u1.ID, u1.ID+"@evil.example.com", RoleBuyer, StatusActive, "", clk.Now())

	// Pin deterministic phones without UUID overlap.
	store.mu.Lock()
	u1.Phone = "0908111222"
	store.users[u1.ID] = u1
	u2.Phone = "0909333444"
	store.users[u2.ID] = u2
	shadow.Phone = "0907555666"
	store.users[shadow.ID] = shadow
	store.mu.Unlock()

	adminCtx := ctxWithUser(admin.ID, RoleAdmin, "")
	superCtx := ctxWithUser(superAdmin.ID, RoleSuperAdmin, "")
	buyerCtx := ctxWithUser(u1.ID, RoleBuyer, "")
	sellerCtx := ctxWithUser(u2.ID, RoleSeller, *u2.ShopID)

	// Permissions unchanged: buyer & seller -> PermissionDenied; admin & super_admin allowed.
	if _, err := srv.AdminListUsers(buyerCtx, &userv1.AdminListUsersRequest{Query: u1.ID}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("buyer AdminListUsers: want PermissionDenied, got %v", err)
	}
	if _, err := srv.AdminListUsers(sellerCtx, &userv1.AdminListUsersRequest{Query: u1.ID}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("seller AdminListUsers: want PermissionDenied, got %v", err)
	}

	// Exact UUID match with page_size=1 (canonical + uppercase + trimmed) -> returns strictly u1, total=1 (never shadowed by newer account!).
	for name, tc := range map[string]struct {
		ctx   context.Context
		query string
	}{
		"exact_id_admin":       {adminCtx, u1.ID},
		"exact_id_uppercase":   {superCtx, strings.ToUpper(u1.ID)},
		"exact_id_with_spaces": {adminCtx, "  " + u1.ID + "  "},
	} {
		resp, err := srv.AdminListUsers(tc.ctx, &userv1.AdminListUsersRequest{Query: tc.query, Page: 1, PageSize: 1})
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", name, err)
		}
		if resp.GetTotal() != 1 || len(resp.GetUsers()) != 1 || resp.GetUsers()[0].GetId() != u1.ID {
			gotID := ""
			if len(resp.GetUsers()) > 0 {
				gotID = resp.GetUsers()[0].GetId()
			}
			t.Fatalf("%s: want 1 user (%s) total=1, got len=%d total=%d first_id=%s",
				name, u1.ID, len(resp.GetUsers()), resp.GetTotal(), gotID)
		}
	}

	// Unknown UUID -> 0 users, total=0.
	missResp, err := srv.AdminListUsers(adminCtx, &userv1.AdminListUsersRequest{Query: "00000000-0000-4000-8000-000000000001"})
	if err != nil || missResp.GetTotal() != 0 || len(missResp.GetUsers()) != 0 {
		t.Fatalf("unknown UUID: want 0/0, got err=%v len=%d total=%d", err, len(missResp.GetUsers()), missResp.GetTotal())
	}

	// Partial UUID prefix MUST NOT match id (exact match only, no ILIKE on id).
	partialResp, err := srv.AdminListUsers(adminCtx, &userv1.AdminListUsersRequest{Query: u2.ID[:13]})
	if err != nil || partialResp.GetTotal() != 0 || len(partialResp.GetUsers()) != 0 {
		t.Fatalf("partial UUID prefix: want 0/0, got err=%v len=%d total=%d", err, len(partialResp.GetUsers()), partialResp.GetTotal())
	}

	// Role filter combines cleanly with exact ID query.
	wrongRoleResp, err := srv.AdminListUsers(adminCtx, &userv1.AdminListUsersRequest{
		Query: u1.ID,
		Role:  userv1.UserRole_USER_ROLE_SELLER,
	})
	if err != nil || wrongRoleResp.GetTotal() != 0 || len(wrongRoleResp.GetUsers()) != 0 {
		t.Fatalf("exact ID + mismatched role: want 0/0, got err=%v len=%d total=%d", err, len(wrongRoleResp.GetUsers()), wrongRoleResp.GetTotal())
	}

	// Existing email, full_name, phone substring searches still match as before.
	for label, want := range map[string]struct {
		query string
		id    string
	}{
		"email": {"LANANH@EXAMPLE", u2.ID},
		"name":  {"minh khôi", u1.ID},
		"phone": {"0909333", u2.ID},
	} {
		resp, err := srv.AdminListUsers(adminCtx, &userv1.AdminListUsersRequest{Query: want.query})
		if err != nil || resp.GetTotal() != 1 || len(resp.GetUsers()) != 1 || resp.GetUsers()[0].GetId() != want.id {
			t.Fatalf("%s search (%q): want user %s total=1, got err=%v len=%d total=%d",
				label, want.query, want.id, err, len(resp.GetUsers()), resp.GetTotal())
		}
	}
}
