package service

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/pkg/common/secretbox"
	"github.com/nexusmart/core/services/user/internal/db"
	"github.com/nexusmart/core/services/user/internal/password"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type w2bClock struct {
	mu  sync.Mutex
	cur time.Time
}

func newW2BClock(t time.Time) *w2bClock {
	return &w2bClock{cur: t.UTC()}
}

func (c *w2bClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cur
}

func (c *w2bClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cur = c.cur.Add(d)
}

const testBankAccountKeyB64 = "ZGV2LW9ubHktYmFuay1hY2NvdW50LWtleS0zMmJ5dGU="

func newTestBankAccountBox(t *testing.T) *secretbox.Box {
	t.Helper()
	box, err := secretbox.NewFromBase64(testBankAccountKeyB64, "BANK_ACCOUNT_KEY")
	if err != nil {
		t.Fatalf("new test secretbox: %v", err)
	}
	return box
}

func newW2BHarness(t *testing.T, opts Options) (*Server, *memStore, *memSecurity, *memPublisher, *bytes.Buffer) {
	t.Helper()
	store := newMemStore()
	sec := newMemSecurity()
	pub := &memPublisher{}
	hasher := password.NewDefaultHasher()
	authMgr, err := auth.NewManager("test-secret-key-hs256", 15*time.Minute)
	if err != nil {
		t.Fatalf("new auth manager: %v", err)
	}
	if opts.BankAccountBox == nil {
		opts.BankAccountBox = newTestBankAccountBox(t)
	}
	var logBuf bytes.Buffer
	log := logger.New("user-w2b-test", logger.WithWriter(&logBuf))
	srv := NewServer(store, sec, hasher, authMgr, pub, log, opts)
	return srv, store, sec, pub, &logBuf
}

func seedUserAndShop(store *memStore, fullName, email, role, userStatus, shopStatus string, createdAt time.Time) (db.User, db.Shop) {
	userID := uuid.NewString()
	shopID := uuid.NewString()
	var shopPtr *string
	if shopStatus != "" {
		s := shopID
		shopPtr = &s
	}
	u := db.User{
		ID:        userID,
		FullName:  fullName,
		Email:     email,
		Phone:     "090" + strings.ReplaceAll(uuid.NewString()[:7], "-", "1"),
		Role:      role,
		Status:    userStatus,
		ShopID:    shopPtr,
		CreatedAt: createdAt.UTC(),
		UpdatedAt: createdAt.UTC(),
	}
	store.mu.Lock()
	store.users[u.ID] = u
	var sh db.Shop
	if shopStatus != "" {
		verifiedOwner := ""
		if shopStatus == ShopStatusActive || shopStatus == ShopStatusSuspended {
			verifiedOwner = fullName
		}
		sh = db.Shop{
			ID:                shopID,
			OwnerUserID:       userID,
			Name:              "Shop " + fullName + " " + shopID[:6],
			Slug:              "shop-" + shopID[:8],
			Status:            shopStatus,
			VerifiedOwnerName: verifiedOwner,
			CreatedAt:         createdAt.UTC(),
			UpdatedAt:         createdAt.UTC(),
		}
		store.shops[sh.ID] = sh
	}
	store.mu.Unlock()
	return u, sh
}

func TestW2B_ShopBankAccount_AddVerifyMatchingNameAndMasking(t *testing.T) {
	clk := newW2BClock(time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC))
	srv, store, _, pub, logBuf := newW2BHarness(t, Options{
		Now:                    clk.Now,
		OTPTTL:                 10 * time.Minute,
		BankPayoutLockDuration: 24 * time.Hour,
	})

	owner, shop := seedUserAndShop(store, "Nguyễn Văn Ánh Đạt", "seller-match@example.com", RoleSeller, StatusActive, ShopStatusActive, clk.Now())
	otherSeller, otherShop := seedUserAndShop(store, "Trần Thị Bình", "seller-other@example.com", RoleSeller, StatusActive, ShopStatusActive, clk.Now())
	pendingOwner, pendingShop := seedUserAndShop(store, "Lê Chờ Duyệt", "seller-pending@example.com", RoleSeller, StatusActive, ShopStatusPending, clk.Now())
	buyer, _ := seedUserAndShop(store, "Người Mua", "buyer@example.com", RoleBuyer, StatusActive, "", clk.Now())
	admin, _ := seedUserAndShop(store, "Admin One", "admin@example.com", RoleAdmin, StatusActive, "", clk.Now())

	sellerCtx := ctxWithUser(owner.ID, RoleSeller, shop.ID)
	otherSellerCtx := ctxWithUser(otherSeller.ID, RoleSeller, otherShop.ID)
	pendingSellerCtx := ctxWithUser(pendingOwner.ID, RoleSeller, pendingShop.ID)
	buyerCtx := ctxWithUser(buyer.ID, RoleBuyer, "")
	adminCtx := ctxWithUser(admin.ID, RoleAdmin, "")
	svcCtx := ctxWithService("payment")

	rawAccNum := "19034567897890"

	// Authorization guards: buyer, service token, non-owner, cross-shop shop_id -> PermissionDenied.
	if _, err := srv.AddShopBankAccount(buyerCtx, &userv1.AddShopBankAccountRequest{
		BankCode: "VCB", BankName: "Vietcombank", AccountNumber: rawAccNum, AccountHolderName: "NGUYEN VAN ANH DAT",
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("buyer AddShopBankAccount: want PermissionDenied, got %v", err)
	}
	if _, err := srv.AddShopBankAccount(svcCtx, &userv1.AddShopBankAccountRequest{
		ShopId: shop.ID, BankCode: "VCB", BankName: "Vietcombank", AccountNumber: rawAccNum, AccountHolderName: "NGUYEN VAN ANH DAT",
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service token AddShopBankAccount: want PermissionDenied, got %v", err)
	}
	if _, err := srv.AddShopBankAccount(sellerCtx, &userv1.AddShopBankAccountRequest{
		ShopId: otherShop.ID, BankCode: "VCB", BankName: "Vietcombank", AccountNumber: rawAccNum, AccountHolderName: "NGUYEN VAN ANH DAT",
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cross-shop AddShopBankAccount: want PermissionDenied, got %v", err)
	}
	if _, err := srv.AddShopBankAccount(ctxWithUser(otherSeller.ID, RoleSeller, shop.ID), &userv1.AddShopBankAccountRequest{
		BankCode: "VCB", BankName: "Vietcombank", AccountNumber: rawAccNum, AccountHolderName: "NGUYEN VAN ANH DAT",
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("non-owner AddShopBankAccount: want PermissionDenied, got %v", err)
	}
	// Non-ACTIVE shop -> FailedPrecondition.
	if _, err := srv.AddShopBankAccount(pendingSellerCtx, &userv1.AddShopBankAccountRequest{
		BankCode: "VCB", BankName: "Vietcombank", AccountNumber: rawAccNum, AccountHolderName: "LE CHO DUYET",
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("PENDING shop AddShopBankAccount: want FailedPrecondition, got %v", err)
	}
	// Input validation: non-digit / too short account number -> InvalidArgument.
	if _, err := srv.AddShopBankAccount(sellerCtx, &userv1.AddShopBankAccountRequest{
		BankCode: "VCB", BankName: "Vietcombank", AccountNumber: "1234", AccountHolderName: "NGUYEN VAN ANH DAT",
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("short account_number: want InvalidArgument, got %v", err)
	}
	if _, err := srv.AddShopBankAccount(sellerCtx, &userv1.AddShopBankAccountRequest{
		BankCode: "VCB", BankName: "Vietcombank", AccountNumber: "19034567ABCD", AccountHolderName: "NGUYEN VAN ANH DAT",
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("non-digit account_number: want InvalidArgument, got %v", err)
	}

	// Happy path: add with uppercase unaccented Vietnamese name (and extra spaces) matching owner.
	addResp, err := srv.AddShopBankAccount(sellerCtx, &userv1.AddShopBankAccountRequest{
		BankCode:          "vcb",
		BankName:          "Vietcombank",
		AccountNumber:     "1903 4567 8978 90",
		AccountHolderName: "  NGUYEN   VAN ANH   DAT ",
	})
	if err != nil {
		t.Fatalf("AddShopBankAccount failed: %v", err)
	}
	if !addResp.GetOtpSent() {
		t.Fatalf("expected otp_sent=true")
	}
	ba := addResp.GetBankAccount()
	if ba.GetStatus() != userv1.ShopBankAccountStatus_SHOP_BANK_ACCOUNT_STATUS_PENDING_OTP {
		t.Fatalf("want PENDING_OTP, got %v", ba.GetStatus())
	}
	if ba.GetBankCode() != "VCB" || ba.GetAccountNumberMasked() != "****7890" {
		t.Fatalf("unexpected bank_code/masked: code=%q masked=%q", ba.GetBankCode(), ba.GetAccountNumberMasked())
	}
	if ba.GetAccountNumber() != "" {
		t.Fatalf("raw account_number leaked in AddShopBankAccount response: %q", ba.GetAccountNumber())
	}
	if !ba.GetHolderNameMatched() {
		t.Fatalf("expected holder_name_matched=true for Vietnamese diacritic-insensitive match")
	}

	// Verify event published with 6-digit OTP & masked account number.
	if len(pub.shopBankAccountOTP) != 1 {
		t.Fatalf("want 1 user.shop_bank_account_otp event, got %d", len(pub.shopBankAccountOTP))
	}
	otpEvt := pub.shopBankAccountOTP[0]
	if otpEvt.ShopID != shop.ID || otpEvt.UserID != owner.ID || otpEvt.Email != owner.Email ||
		otpEvt.BankCode != "VCB" || otpEvt.AccountNumberMasked != "****7890" || len(otpEvt.OTP) != 6 {
		t.Fatalf("unexpected otp event payload: %+v", otpEvt)
	}

	// Verify OTP is stored hashed (not plaintext) in DB.
	store.mu.Lock()
	storedRec := store.shopBankAccounts[ba.GetId()]
	store.mu.Unlock()
	if storedRec.OTPHash == "" || storedRec.OTPHash == otpEvt.OTP || strings.Contains(storedRec.OTPHash, otpEvt.OTP) {
		t.Fatalf("OTP must be stored hashed, not plaintext")
	}

	// Cross-shop VerifyShopBankAccountOTP -> PermissionDenied.
	if _, err := srv.VerifyShopBankAccountOTP(otherSellerCtx, &userv1.VerifyShopBankAccountOTPRequest{
		ShopId: shop.ID,
		Otp:    otpEvt.OTP,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cross-shop VerifyShopBankAccountOTP: want PermissionDenied, got %v", err)
	}

	// Verify OTP with owner seller -> VERIFIED, no payout lock on first verified account!
	verifyResp, err := srv.VerifyShopBankAccountOTP(sellerCtx, &userv1.VerifyShopBankAccountOTPRequest{
		Otp: otpEvt.OTP,
	})
	if err != nil {
		t.Fatalf("VerifyShopBankAccountOTP failed: %v", err)
	}
	vba := verifyResp.GetBankAccount()
	if vba.GetStatus() != userv1.ShopBankAccountStatus_SHOP_BANK_ACCOUNT_STATUS_VERIFIED {
		t.Fatalf("want VERIFIED after matching name OTP, got %v", vba.GetStatus())
	}
	if !vba.GetHolderNameMatched() || vba.GetVerifiedAt() == nil {
		t.Fatalf("expected holder_name_matched=true and verified_at set")
	}
	if vba.GetPayoutLockedUntil() != nil {
		t.Fatalf("first verified bank account must NOT set payout_locked_until, got %v", vba.GetPayoutLockedUntil().AsTime())
	}
	if vba.GetAccountNumber() != "" {
		t.Fatalf("raw account_number leaked in VerifyShopBankAccountOTP response")
	}

	// GetShopBankAccount: owner seller & admin both see masked only; other seller & buyer get PermissionDenied.
	for name, ctx := range map[string]context.Context{"seller": sellerCtx, "admin": adminCtx} {
		getResp, err := srv.GetShopBankAccount(ctx, &userv1.GetShopBankAccountRequest{ShopId: shop.ID})
		if err != nil {
			t.Fatalf("GetShopBankAccount(%s) failed: %v", name, err)
		}
		if getResp.GetBankAccount().GetAccountNumber() != "" || getResp.GetBankAccount().GetAccountNumberMasked() != "****7890" {
			t.Fatalf("GetShopBankAccount(%s) must mask account number, got raw=%q masked=%q",
				name, getResp.GetBankAccount().GetAccountNumber(), getResp.GetBankAccount().GetAccountNumberMasked())
		}
	}
	if _, err := srv.GetShopBankAccount(otherSellerCtx, &userv1.GetShopBankAccountRequest{ShopId: shop.ID}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("other seller GetShopBankAccount: want PermissionDenied, got %v", err)
	}
	if _, err := srv.GetShopBankAccount(buyerCtx, &userv1.GetShopBankAccountRequest{ShopId: shop.ID}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("buyer GetShopBankAccount: want PermissionDenied, got %v", err)
	}

	// Audit logs must record add + verify WITHOUT raw account number or OTP.
	store.mu.Lock()
	audits := append([]db.AuditLog(nil), store.auditLogs...)
	store.mu.Unlock()
	if len(audits) < 2 {
		t.Fatalf("expected at least 2 audit log entries for add+verify, got %d", len(audits))
	}
	for _, a := range audits {
		combined := a.BeforeJSON + " " + a.AfterJSON + " " + a.Reason
		if strings.Contains(combined, rawAccNum) || strings.Contains(combined, otpEvt.OTP) {
			t.Fatalf("audit log %s leaked sensitive data: %s", a.Action, combined)
		}
	}

	// Logs must NEVER contain raw account number or OTP.
	logOut := logBuf.String()
	if strings.Contains(logOut, rawAccNum) {
		t.Fatalf("raw account number leaked into logs: %s", logOut)
	}
	if strings.Contains(logOut, otpEvt.OTP) {
		t.Fatalf("OTP leaked into logs: %s", logOut)
	}
}

func TestW2B_ShopBankAccount_OTPExpiryAndMaxAttempts(t *testing.T) {
	clk := newW2BClock(time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC))
	srv, store, _, pub, logBuf := newW2BHarness(t, Options{
		Now:                clk.Now,
		OTPTTL:             10 * time.Minute,
		BankOTPMaxAttempts: 5,
	})

	owner, shop := seedUserAndShop(store, "Phạm Minh Tuấn", "seller-otp@example.com", RoleSeller, StatusActive, ShopStatusActive, clk.Now())
	sellerCtx := ctxWithUser(owner.ID, RoleSeller, shop.ID)

	// 1. Expired OTP via fake clock.
	if _, err := srv.AddShopBankAccount(sellerCtx, &userv1.AddShopBankAccountRequest{
		BankCode: "TCB", BankName: "Techcombank", AccountNumber: "19001122334455", AccountHolderName: "PHAM MINH TUAN",
	}); err != nil {
		t.Fatalf("AddShopBankAccount (expiry): %v", err)
	}
	expiredOTP := pub.shopBankAccountOTP[len(pub.shopBankAccountOTP)-1].OTP
	clk.Advance(10*time.Minute + time.Second)

	if _, err := srv.VerifyShopBankAccountOTP(sellerCtx, &userv1.VerifyShopBankAccountOTPRequest{
		Otp: expiredOTP,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expired OTP: want FailedPrecondition, got %v", err)
	}

	// 2. Max wrong OTP attempts (5): attempts 1..4 -> InvalidArgument, 5th -> FailedPrecondition,
	// and subsequent attempt with the REAL OTP must still fail with FailedPrecondition!
	if _, err := srv.AddShopBankAccount(sellerCtx, &userv1.AddShopBankAccountRequest{
		BankCode: "TCB", BankName: "Techcombank", AccountNumber: "19001122334455", AccountHolderName: "PHAM MINH TUAN",
	}); err != nil {
		t.Fatalf("AddShopBankAccount (attempts): %v", err)
	}
	realOTP := pub.shopBankAccountOTP[len(pub.shopBankAccountOTP)-1].OTP
	wrongOTP := "000000"
	if realOTP == wrongOTP {
		wrongOTP = "999999"
	}

	for i := 1; i <= 4; i++ {
		if _, err := srv.VerifyShopBankAccountOTP(sellerCtx, &userv1.VerifyShopBankAccountOTPRequest{
			Otp: wrongOTP,
		}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("wrong OTP attempt %d: want InvalidArgument, got %v", i, err)
		}
	}
	if _, err := srv.VerifyShopBankAccountOTP(sellerCtx, &userv1.VerifyShopBankAccountOTPRequest{
		Otp: wrongOTP,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("5th wrong OTP attempt: want FailedPrecondition, got %v", err)
	}
	if _, err := srv.VerifyShopBankAccountOTP(sellerCtx, &userv1.VerifyShopBankAccountOTPRequest{
		Otp: realOTP,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("real OTP after lockout: want FailedPrecondition, got %v", err)
	}

	// 3. Publish failure supersedes pending row and returns Internal without leaking OTP.
	pub.publishBankOTPErr = errors.New("nats unavailable")
	if _, err := srv.AddShopBankAccount(sellerCtx, &userv1.AddShopBankAccountRequest{
		BankCode: "ACB", BankName: "ACB Bank", AccountNumber: "998877665544", AccountHolderName: "PHAM MINH TUAN",
	}); status.Code(err) != codes.Internal {
		t.Fatalf("AddShopBankAccount publish failure: want Internal, got %v", err)
	}
	pub.publishBankOTPErr = nil

	if strings.Contains(logBuf.String(), realOTP) || strings.Contains(logBuf.String(), "19001122334455") {
		t.Fatalf("sensitive data leaked in logs: %s", logBuf.String())
	}
}

func TestW2B_ShopBankAccount_MismatchAdminReviewAndChangeLock(t *testing.T) {
	clk := newW2BClock(time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC))
	lockDur := 36 * time.Hour // Custom USER_BANK_PAYOUT_LOCK_HOURS to verify config is honored.
	srv, store, _, pub, logBuf := newW2BHarness(t, Options{
		Now:                    clk.Now,
		OTPTTL:                 10 * time.Minute,
		BankPayoutLockDuration: lockDur,
	})

	// Use NFD accented owner name to also verify NFD vs NFC/ASCII matching.
	owner, shop := seedUserAndShop(store, "Đặng Thị Hồng Điệp", "seller-review@example.com", RoleSeller, StatusActive, ShopStatusActive, clk.Now())
	admin, _ := seedUserAndShop(store, "Admin Reviewer", "admin-rev@example.com", RoleAdmin, StatusActive, "", clk.Now())

	sellerCtx := ctxWithUser(owner.ID, RoleSeller, shop.ID)
	adminCtx := ctxWithUser(admin.ID, RoleAdmin, "")
	svcCtx := ctxWithService("payment")

	// 1. Mismatched holder name -> OTP verified -> PENDING_ADMIN_REVIEW.
	add1, err := srv.AddShopBankAccount(sellerCtx, &userv1.AddShopBankAccountRequest{
		BankCode: "BIDV", BankName: "BIDV", AccountNumber: "21510001234567", AccountHolderName: "CONG TY TNHH KHAC TEN",
	})
	if err != nil {
		t.Fatalf("AddShopBankAccount mismatch: %v", err)
	}
	otp1 := pub.shopBankAccountOTP[len(pub.shopBankAccountOTP)-1].OTP
	ver1, err := srv.VerifyShopBankAccountOTP(sellerCtx, &userv1.VerifyShopBankAccountOTPRequest{Otp: otp1})
	if err != nil {
		t.Fatalf("VerifyShopBankAccountOTP mismatch: %v", err)
	}
	if ver1.GetBankAccount().GetStatus() != userv1.ShopBankAccountStatus_SHOP_BANK_ACCOUNT_STATUS_PENDING_ADMIN_REVIEW {
		t.Fatalf("want PENDING_ADMIN_REVIEW, got %v", ver1.GetBankAccount().GetStatus())
	}
	if ver1.GetBankAccount().GetHolderNameMatched() {
		t.Fatalf("expected holder_name_matched=false")
	}

	// Seller cannot review own bank account -> PermissionDenied.
	if _, err := srv.AdminReviewShopBankAccount(sellerCtx, &userv1.AdminReviewShopBankAccountRequest{
		ShopId: shop.ID, BankAccountId: add1.GetBankAccount().GetId(), Decision: userv1.ReviewDecision_REVIEW_DECISION_APPROVE,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("seller AdminReviewShopBankAccount: want PermissionDenied, got %v", err)
	}
	// Admin REJECT without reason -> InvalidArgument.
	if _, err := srv.AdminReviewShopBankAccount(adminCtx, &userv1.AdminReviewShopBankAccountRequest{
		ShopId: shop.ID, BankAccountId: add1.GetBankAccount().GetId(), Decision: userv1.ReviewDecision_REVIEW_DECISION_REJECT,
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("reject without reason: want InvalidArgument, got %v", err)
	}
	// Admin REJECT with reason -> REJECTED.
	rej1, err := srv.AdminReviewShopBankAccount(adminCtx, &userv1.AdminReviewShopBankAccountRequest{
		ShopId: shop.ID, BankAccountId: add1.GetBankAccount().GetId(), Decision: userv1.ReviewDecision_REVIEW_DECISION_REJECT, Reason: "Thiếu giấy uỷ quyền",
	})
	if err != nil {
		t.Fatalf("admin reject failed: %v", err)
	}
	if rej1.GetBankAccount().GetStatus() != userv1.ShopBankAccountStatus_SHOP_BANK_ACCOUNT_STATUS_REJECTED ||
		rej1.GetBankAccount().GetReviewNote() != "Thiếu giấy uỷ quyền" ||
		rej1.GetBankAccount().GetAccountNumber() != "" {
		t.Fatalf("unexpected rejected bank account: %+v", rej1.GetBankAccount())
	}

	// 2. Add matching account (first VERIFIED account) -> VERIFIED with NO payout lock.
	clk.Advance(time.Minute)
	if _, err := srv.AddShopBankAccount(sellerCtx, &userv1.AddShopBankAccountRequest{
		BankCode: "VCB", BankName: "Vietcombank", AccountNumber: "00112233441111", AccountHolderName: "Dang\u0323 Thi\u0323 Hong Diep",
	}); err != nil {
		t.Fatalf("AddShopBankAccount matching #1: %v", err)
	}
	otp2 := pub.shopBankAccountOTP[len(pub.shopBankAccountOTP)-1].OTP
	ver2, err := srv.VerifyShopBankAccountOTP(sellerCtx, &userv1.VerifyShopBankAccountOTPRequest{Otp: otp2})
	if err != nil {
		t.Fatalf("VerifyShopBankAccountOTP matching #1: %v", err)
	}
	if ver2.GetBankAccount().GetStatus() != userv1.ShopBankAccountStatus_SHOP_BANK_ACCOUNT_STATUS_VERIFIED {
		t.Fatalf("want VERIFIED for matching NFD name, got %v", ver2.GetBankAccount().GetStatus())
	}
	if ver2.GetBankAccount().GetPayoutLockedUntil() != nil {
		t.Fatalf("first verified account after prior rejection should NOT lock payout")
	}

	// 3. Change to mismatched account -> while PENDING_ADMIN_REVIEW, payout still uses prior VERIFIED account!
	clk.Advance(time.Hour)
	add3, err := srv.AddShopBankAccount(sellerCtx, &userv1.AddShopBankAccountRequest{
		BankCode: "MB", BankName: "MB Bank", AccountNumber: "88889999002222", AccountHolderName: "CONG TY TNHH DIEP",
	})
	if err != nil {
		t.Fatalf("AddShopBankAccount change mismatch: %v", err)
	}
	otp3 := pub.shopBankAccountOTP[len(pub.shopBankAccountOTP)-1].OTP
	if _, err := srv.VerifyShopBankAccountOTP(sellerCtx, &userv1.VerifyShopBankAccountOTPRequest{Otp: otp3}); err != nil {
		t.Fatalf("VerifyShopBankAccountOTP change mismatch: %v", err)
	}
	payoutWhilePending, err := srv.GetShopBankAccountForPayout(svcCtx, &userv1.GetShopBankAccountForPayoutRequest{ShopId: shop.ID})
	if err != nil {
		t.Fatalf("GetShopBankAccountForPayout while change pending: %v", err)
	}
	if payoutWhilePending.GetBankAccount().GetAccountNumber() != "00112233441111" {
		t.Fatalf("expected old verified account while change pending, got %q", payoutWhilePending.GetBankAccount().GetAccountNumber())
	}

	// Admin APPROVES the mismatched change -> becomes VERIFIED, supersedes old verified, sets payout_locked_until = now + 36h!
	clk.Advance(30 * time.Minute)
	expectedLock3 := clk.Now().Add(lockDur)
	app3, err := srv.AdminReviewShopBankAccount(adminCtx, &userv1.AdminReviewShopBankAccountRequest{
		ShopId: shop.ID, BankAccountId: add3.GetBankAccount().GetId(), Decision: userv1.ReviewDecision_REVIEW_DECISION_APPROVE, Reason: "Đã xác minh ĐKKD",
	})
	if err != nil {
		t.Fatalf("admin approve change failed: %v", err)
	}
	if app3.GetBankAccount().GetStatus() != userv1.ShopBankAccountStatus_SHOP_BANK_ACCOUNT_STATUS_VERIFIED {
		t.Fatalf("want VERIFIED after admin approve, got %v", app3.GetBankAccount().GetStatus())
	}
	if app3.GetBankAccount().GetAccountNumber() != "" {
		t.Fatalf("admin review response leaked raw account number")
	}
	if app3.GetBankAccount().GetPayoutLockedUntil() == nil || !app3.GetBankAccount().GetPayoutLockedUntil().AsTime().Equal(expectedLock3) {
		t.Fatalf("want payout_locked_until=%s, got %v", expectedLock3, app3.GetBankAccount().GetPayoutLockedUntil())
	}

	// 4. Direct matching-name change of verified account -> replaces active verified & sets payout_locked_until!
	clk.Advance(2 * time.Hour)
	expectedLock4 := clk.Now().Add(lockDur)
	if _, err := srv.AddShopBankAccount(sellerCtx, &userv1.AddShopBankAccountRequest{
		BankCode: "ACB", BankName: "ACB", AccountNumber: "55556666773333", AccountHolderName: "DANG THI HONG DIEP",
	}); err != nil {
		t.Fatalf("AddShopBankAccount change match: %v", err)
	}
	otp4 := pub.shopBankAccountOTP[len(pub.shopBankAccountOTP)-1].OTP
	ver4, err := srv.VerifyShopBankAccountOTP(sellerCtx, &userv1.VerifyShopBankAccountOTPRequest{Otp: otp4})
	if err != nil {
		t.Fatalf("VerifyShopBankAccountOTP change match: %v", err)
	}
	if ver4.GetBankAccount().GetStatus() != userv1.ShopBankAccountStatus_SHOP_BANK_ACCOUNT_STATUS_VERIFIED {
		t.Fatalf("want VERIFIED, got %v", ver4.GetBankAccount().GetStatus())
	}
	if ver4.GetBankAccount().GetPayoutLockedUntil() == nil || !ver4.GetBankAccount().GetPayoutLockedUntil().AsTime().Equal(expectedLock4) {
		t.Fatalf("want payout_locked_until=%s, got %v", expectedLock4, ver4.GetBankAccount().GetPayoutLockedUntil())
	}

	// Enforce invariant: strictly ONE VERIFIED bank account for this shop!
	store.mu.Lock()
	verifiedCount := 0
	for _, acc := range store.shopBankAccounts {
		if acc.ShopID == shop.ID && acc.Status == "VERIFIED" {
			verifiedCount++
		}
	}
	store.mu.Unlock()
	if verifiedCount != 1 {
		t.Fatalf("invariant violation: expected exactly 1 VERIFIED bank account for shop, got %d", verifiedCount)
	}

	// Payout RPC returns the latest verified account with raw number & payout lock.
	payoutFinal, err := srv.GetShopBankAccountForPayout(svcCtx, &userv1.GetShopBankAccountForPayoutRequest{ShopId: shop.ID})
	if err != nil {
		t.Fatalf("GetShopBankAccountForPayout final: %v", err)
	}
	if payoutFinal.GetBankAccount().GetAccountNumber() != "55556666773333" ||
		payoutFinal.GetBankAccount().GetAccountNumberMasked() != "****3333" {
		t.Fatalf("unexpected final payout bank account: %+v", payoutFinal.GetBankAccount())
	}

	for _, raw := range []string{"21510001234567", "00112233441111", "88889999002222", "55556666773333"} {
		if strings.Contains(logBuf.String(), raw) {
			t.Fatalf("raw account number %s leaked into logs", raw)
		}
	}
}

func TestW2B_ShopBankAccount_ForPayoutAuthAndMasking(t *testing.T) {
	clk := newW2BClock(time.Date(2026, 11, 23, 11, 0, 0, 0, time.UTC))
	srv, store, _, pub, logBuf := newW2BHarness(t, Options{Now: clk.Now})

	owner, shop := seedUserAndShop(store, "Nguyễn Bảo Long", "seller-payout@example.com", RoleSeller, StatusActive, ShopStatusActive, clk.Now())
	buyer, _ := seedUserAndShop(store, "Buyer P", "buyer-payout@example.com", RoleBuyer, StatusActive, "", clk.Now())
	admin, _ := seedUserAndShop(store, "Admin P", "admin-payout@example.com", RoleAdmin, StatusActive, "", clk.Now())
	superAdmin, _ := seedUserAndShop(store, "Super P", "super-payout@example.com", RoleSuperAdmin, StatusActive, "", clk.Now())

	sellerCtx := ctxWithUser(owner.ID, RoleSeller, shop.ID)
	svcCtx := ctxWithService("payment")

	// Before any verified account -> NotFound.
	if _, err := srv.GetShopBankAccountForPayout(svcCtx, &userv1.GetShopBankAccountForPayoutRequest{ShopId: shop.ID}); status.Code(err) != codes.NotFound {
		t.Fatalf("GetShopBankAccountForPayout with no verified account: want NotFound, got %v", err)
	}

	rawAcc := "77778888996543"
	if _, err := srv.AddShopBankAccount(sellerCtx, &userv1.AddShopBankAccountRequest{
		BankCode: "VPB", BankName: "VPBank", AccountNumber: rawAcc, AccountHolderName: "NGUYEN BAO LONG",
	}); err != nil {
		t.Fatalf("AddShopBankAccount: %v", err)
	}
	// Still PENDING_OTP -> NotFound for payout.
	if _, err := srv.GetShopBankAccountForPayout(svcCtx, &userv1.GetShopBankAccountForPayoutRequest{ShopId: shop.ID}); status.Code(err) != codes.NotFound {
		t.Fatalf("GetShopBankAccountForPayout while PENDING_OTP: want NotFound, got %v", err)
	}

	otp := pub.shopBankAccountOTP[len(pub.shopBankAccountOTP)-1].OTP
	if _, err := srv.VerifyShopBankAccountOTP(sellerCtx, &userv1.VerifyShopBankAccountOTPRequest{Otp: otp}); err != nil {
		t.Fatalf("VerifyShopBankAccountOTP: %v", err)
	}

	// Anonymous -> Unauthenticated.
	if _, err := srv.GetShopBankAccountForPayout(context.Background(), &userv1.GetShopBankAccountForPayoutRequest{ShopId: shop.ID}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anonymous GetShopBankAccountForPayout: want Unauthenticated, got %v", err)
	}
	// Every end-user JWT (buyer, owner seller, admin, super_admin) MUST be rejected with PermissionDenied!
	for roleName, ctx := range map[string]context.Context{
		"buyer":       ctxWithUser(buyer.ID, RoleBuyer, ""),
		"seller":      sellerCtx,
		"admin":       ctxWithUser(admin.ID, RoleAdmin, ""),
		"super_admin": ctxWithUser(superAdmin.ID, RoleSuperAdmin, ""),
	} {
		if _, err := srv.GetShopBankAccountForPayout(ctx, &userv1.GetShopBankAccountForPayoutRequest{ShopId: shop.ID}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("%s JWT GetShopBankAccountForPayout: want PermissionDenied, got %v", roleName, err)
		}
	}

	// Service token succeeds and returns unmasked account_number.
	resp, err := srv.GetShopBankAccountForPayout(svcCtx, &userv1.GetShopBankAccountForPayoutRequest{ShopId: shop.ID})
	if err != nil {
		t.Fatalf("service token GetShopBankAccountForPayout failed: %v", err)
	}
	if resp.GetBankAccount().GetAccountNumber() != rawAcc || resp.GetBankAccount().GetAccountNumberMasked() != "****6543" {
		t.Fatalf("unexpected payout account response: raw=%q masked=%q",
			resp.GetBankAccount().GetAccountNumber(), resp.GetBankAccount().GetAccountNumberMasked())
	}
	if strings.Contains(logBuf.String(), rawAcc) {
		t.Fatalf("raw account number leaked in logs on GetShopBankAccountForPayout")
	}
}

func TestW2B_GetPlatformUserStats_VNTimezoneAndValidation(t *testing.T) {
	// Now = 2026-11-23 15:00 VN = 2026-11-23 08:00 UTC.
	clk := newW2BClock(time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC))
	srv, store, _, _, _ := newW2BHarness(t, Options{Now: clk.Now})

	admin, _ := seedUserAndShop(store, "Admin Stat", "admin-stat@example.com", RoleAdmin, StatusActive, "", time.Date(2026, 11, 10, 3, 0, 0, 0, time.UTC))
	buyer, _ := seedUserAndShop(store, "Buyer Stat", "buyer-stat@example.com", RoleBuyer, StatusActive, "", time.Date(2026, 11, 22, 16, 59, 59, 0, time.UTC))                // 2026-11-22 23:59:59 VN -> 2026-11-22
	seller1, _ := seedUserAndShop(store, "Seller 1", "seller1-stat@example.com", RoleSeller, StatusActive, ShopStatusActive, time.Date(2026, 11, 22, 17, 0, 0, 0, time.UTC)) // 2026-11-23 00:00:00 VN -> 2026-11-23
	_, _ = seedUserAndShop(store, "Seller 2", "seller2-stat@example.com", RoleSeller, StatusLocked, ShopStatusPending, time.Date(2026, 11, 20, 5, 0, 0, 0, time.UTC))        // 2026-11-20 12:00:00 VN -> outside [22..23]

	adminCtx := ctxWithUser(admin.ID, RoleAdmin, "")
	buyerCtx := ctxWithUser(buyer.ID, RoleBuyer, "")
	sellerCtx := ctxWithUser(seller1.ID, RoleSeller, *seller1.ShopID)
	svcCtx := ctxWithService("order")

	// Auth checks: buyer, seller, service token -> PermissionDenied.
	for name, ctx := range map[string]context.Context{"buyer": buyerCtx, "seller": sellerCtx, "service": svcCtx} {
		if _, err := srv.GetPlatformUserStats(ctx, &userv1.GetPlatformUserStatsRequest{
			FromDate: "2026-11-22", ToDate: "2026-11-23",
		}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("%s GetPlatformUserStats: want PermissionDenied, got %v", name, err)
		}
	}

	// Validation: bad format, inverted range, > 366 days -> InvalidArgument.
	badCases := []struct {
		name string
		from string
		to   string
	}{
		{"bad from", "23-11-2026", "2026-11-23"},
		{"bad to", "2026-11-22", "2026/11/23"},
		{"from after to", "2026-11-24", "2026-11-23"},
		{"span 367 days", "2025-11-22", "2026-11-23"},
	}
	for _, tc := range badCases {
		if _, err := srv.GetPlatformUserStats(adminCtx, &userv1.GetPlatformUserStatsRequest{
			FromDate: tc.from, ToDate: tc.to,
		}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("%s: want InvalidArgument, got %v", tc.name, err)
		}
	}

	// Exact 366-day span inclusive MUST succeed!
	span366, err := srv.GetPlatformUserStats(adminCtx, &userv1.GetPlatformUserStatsRequest{
		FromDate: "2025-11-23", ToDate: "2026-11-23",
	})
	if err != nil {
		t.Fatalf("366-day inclusive span should succeed, got %v", err)
	}
	if len(span366.GetDaily()) != 366 {
		t.Fatalf("want 366 daily points, got %d", len(span366.GetDaily()))
	}

	// VN timezone boundary verification over [2026-11-21 .. 2026-11-23] (3 days, 2026-11-21 has 0).
	resp, err := srv.GetPlatformUserStats(adminCtx, &userv1.GetPlatformUserStatsRequest{
		FromDate: "2026-11-21", ToDate: "2026-11-23",
	})
	if err != nil {
		t.Fatalf("GetPlatformUserStats failed: %v", err)
	}
	if resp.GetFromDate() != "2026-11-21" || resp.GetToDate() != "2026-11-23" {
		t.Fatalf("unexpected dates: from=%q to=%q", resp.GetFromDate(), resp.GetToDate())
	}
	if resp.GetTotalUsers() != 4 || resp.GetActiveUsers() != 3 || resp.GetNewUsers() != 2 {
		t.Fatalf("unexpected user counts: total=%d active=%d new=%d", resp.GetTotalUsers(), resp.GetActiveUsers(), resp.GetNewUsers())
	}
	if resp.GetTotalShops() != 2 || resp.GetActiveShops() != 1 || resp.GetPendingShops() != 1 || resp.GetNewShops() != 1 {
		t.Fatalf("unexpected shop counts: total=%d active=%d pending=%d new=%d",
			resp.GetTotalShops(), resp.GetActiveShops(), resp.GetPendingShops(), resp.GetNewShops())
	}
	daily := resp.GetDaily()
	if len(daily) != 3 {
		t.Fatalf("want 3 dense daily points, got %d", len(daily))
	}
	wantDaily := []struct {
		date     string
		newUsers int64
		newShops int64
	}{
		{"2026-11-21", 0, 0},
		{"2026-11-22", 1, 0},
		{"2026-11-23", 1, 1},
	}
	for i, w := range wantDaily {
		if daily[i].GetDate() != w.date || daily[i].GetNewUsers() != w.newUsers || daily[i].GetNewShops() != w.newShops {
			t.Fatalf("daily[%d]: want %+v, got date=%q new_users=%d new_shops=%d",
				i, w, daily[i].GetDate(), daily[i].GetNewUsers(), daily[i].GetNewShops())
		}
	}

	// Default dates when empty (uses fake clock VN today = 2026-11-23).
	defResp, err := srv.GetPlatformUserStats(adminCtx, &userv1.GetPlatformUserStatsRequest{})
	if err != nil {
		t.Fatalf("GetPlatformUserStats default dates failed: %v", err)
	}
	if defResp.GetToDate() != "2026-11-23" || defResp.GetFromDate() == "" {
		t.Fatalf("unexpected default date range: from=%q to=%q", defResp.GetFromDate(), defResp.GetToDate())
	}
}

func TestW2B_VietnameseNameNormalizationAndMasking(t *testing.T) {
	matchCases := [][2]string{
		{"Nguyễn Văn Ánh", "NGUYEN VAN ANH"},
		{"Đặng Thị Hồng Điệp", "dang thi hong diep"},
		{"  Trần   Minh   Đức ", "TRAN MINH DUC"},
		{"Lê Quốc Bảo", "Le\u0302 Quo\u0302\u0301c Ba\u0309o"},
		{"Hoàng Yến Ước Ơn Ăn Ân", "hoang yen uoc on an an"},
	}
	for _, pair := range matchCases {
		if !namesMatchVietnamese(pair[0], pair[1]) {
			t.Fatalf("expected Vietnamese names to match: %q vs %q (norm: %q vs %q)",
				pair[0], pair[1], normalizeVietnameseName(pair[0]), normalizeVietnameseName(pair[1]))
		}
	}
	mismatchCases := [][2]string{
		{"Nguyễn Văn A", "NGUYEN VAN B"},
		{"Đặng Thị Hồng Điệp", "CONG TY TNHH DIEP"},
		{"", ""},
		{"   ", "   "},
	}
	for _, pair := range mismatchCases {
		if namesMatchVietnamese(pair[0], pair[1]) {
			t.Fatalf("expected Vietnamese names NOT to match: %q vs %q", pair[0], pair[1])
		}
	}

	if got := maskBankAccountNumber("19034567890123"); got != "****0123" {
		t.Fatalf("maskBankAccountNumber(14 digits): want ****0123, got %q", got)
	}
	if got := maskBankAccountNumber("1234"); got != "****" {
		t.Fatalf("maskBankAccountNumber(4 digits): want ****, got %q", got)
	}
}

func TestW2B_ShopBankAccount_AddRateLimitPerHour(t *testing.T) {
	clk := newW2BClock(time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC))
	srv, store, _, pub, _ := newW2BHarness(t, Options{
		Now:    clk.Now,
		OTPTTL: 10 * time.Minute,
	})

	owner1, shop1 := seedUserAndShop(store, "Nguyễn Văn Rate", "seller-rate1@example.com", RoleSeller, StatusActive, ShopStatusActive, clk.Now())
	owner2, shop2 := seedUserAndShop(store, "Trần Thị Khác", "seller-rate2@example.com", RoleSeller, StatusActive, ShopStatusActive, clk.Now())
	seller1Ctx := ctxWithUser(owner1.ID, RoleSeller, shop1.ID)
	seller2Ctx := ctxWithUser(owner2.ID, RoleSeller, shop2.ID)

	countRowsForShop := func(shopID string) int {
		store.mu.Lock()
		defer store.mu.Unlock()
		n := 0
		for _, acc := range store.shopBankAccounts {
			if acc.ShopID == shopID {
				n++
			}
		}
		return n
	}

	// 5 successful OTP email sends within 1 hour reach the per-shop limit.
	for i := 1; i <= 5; i++ {
		if _, err := srv.AddShopBankAccount(seller1Ctx, &userv1.AddShopBankAccountRequest{
			BankCode:          "VCB",
			BankName:          "Vietcombank",
			AccountNumber:     "19034567896001",
			AccountHolderName: "NGUYEN VAN RATE",
		}); err != nil {
			t.Fatalf("AddShopBankAccount attempt %d failed: %v", i, err)
		}
		if i < 5 {
			clk.Advance(time.Minute)
		}
	}

	eventsBefore6 := len(pub.shopBankAccountOTP)
	rowsBefore6 := countRowsForShop(shop1.ID)
	if eventsBefore6 != 5 || rowsBefore6 != 5 {
		t.Fatalf("before 6th attempt: want events=5 rows=5, got events=%d rows=%d", eventsBefore6, rowsBefore6)
	}

	// 6th attempt within 1 hour MUST be rejected with ResourceExhausted, creating NO row and emitting NO event.
	if _, err := srv.AddShopBankAccount(seller1Ctx, &userv1.AddShopBankAccountRequest{
		BankCode:          "VCB",
		BankName:          "Vietcombank",
		AccountNumber:     "19034567896001",
		AccountHolderName: "NGUYEN VAN RATE",
	}); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("6th AddShopBankAccount within 1h: want ResourceExhausted, got %v", err)
	}
	if len(pub.shopBankAccountOTP) != eventsBefore6 {
		t.Fatalf("rate-limited 6th attempt emitted an OTP event: before=%d after=%d", eventsBefore6, len(pub.shopBankAccountOTP))
	}
	if countRowsForShop(shop1.ID) != rowsBefore6 {
		t.Fatalf("rate-limited 6th attempt created a DB row: before=%d after=%d", rowsBefore6, countRowsForShop(shop1.ID))
	}

	// Another shop MUST NOT be affected by shop1's rate limit.
	if _, err := srv.AddShopBankAccount(seller2Ctx, &userv1.AddShopBankAccountRequest{
		BankCode:          "TCB",
		BankName:          "Techcombank",
		AccountNumber:     "19034567897001",
		AccountHolderName: "TRAN THI KHAC",
	}); err != nil {
		t.Fatalf("shop2 AddShopBankAccount should not be rate-limited by shop1: %v", err)
	}

	// At 59m59s after attempt 1 (4m already elapsed, advance 55m59s), shop1 still has 5 rows in the last hour -> ResourceExhausted.
	clk.Advance(55*time.Minute + 59*time.Second)
	if _, err := srv.AddShopBankAccount(seller1Ctx, &userv1.AddShopBankAccountRequest{
		BankCode:          "VCB",
		BankName:          "Vietcombank",
		AccountNumber:     "19034567896001",
		AccountHolderName: "NGUYEN VAN RATE",
	}); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("AddShopBankAccount at 59m59s: want ResourceExhausted, got %v", err)
	}

	// Cross the 1-hour mark since attempt 1 (+2s -> 1h00m01s since attempt 1) -> attempt 1 ages out, shop1 is allowed again!
	clk.Advance(2 * time.Second)
	if _, err := srv.AddShopBankAccount(seller1Ctx, &userv1.AddShopBankAccountRequest{
		BankCode:          "VCB",
		BankName:          "Vietcombank",
		AccountNumber:     "19034567896002",
		AccountHolderName: "NGUYEN VAN RATE",
	}); err != nil {
		t.Fatalf("AddShopBankAccount after 1h window elapsed should succeed, got %v", err)
	}
}

func TestW2B_AdminListShops_BankReviewPendingFilterAndVisibility(t *testing.T) {
	clk := newW2BClock(time.Date(2026, 11, 23, 14, 0, 0, 0, time.UTC))
	srv, store, _, pub, _ := newW2BHarness(t, Options{Now: clk.Now})

	admin, _ := seedUserAndShop(store, "Admin List", "admin-list@example.com", RoleAdmin, StatusActive, "", clk.Now())
	buyer, _ := seedUserAndShop(store, "Buyer List", "buyer-list@example.com", RoleBuyer, StatusActive, "", clk.Now())

	ownerA, shopA := seedUserAndShop(store, "Nguyễn Văn An", "seller-a@example.com", RoleSeller, StatusActive, ShopStatusActive, clk.Now())
	clk.Advance(time.Minute)
	ownerB, shopB := seedUserAndShop(store, "Trần Thị Bích", "seller-b@example.com", RoleSeller, StatusActive, ShopStatusActive, clk.Now())
	clk.Advance(time.Minute)
	ownerC, shopC := seedUserAndShop(store, "Lê Minh Châu", "seller-c@example.com", RoleSeller, StatusActive, ShopStatusActive, clk.Now())
	clk.Advance(time.Minute)
	_, shopPending := seedUserAndShop(store, "Phạm Chờ Duyệt", "seller-p@example.com", RoleBuyer, StatusActive, ShopStatusPending, clk.Now())

	adminCtx := ctxWithUser(admin.ID, RoleAdmin, "")
	buyerCtx := ctxWithUser(buyer.ID, RoleBuyer, "")
	sellerACtx := ctxWithUser(ownerA.ID, RoleSeller, shopA.ID)
	sellerBCtx := ctxWithUser(ownerB.ID, RoleSeller, shopB.ID)
	sellerCCtx := ctxWithUser(ownerC.ID, RoleSeller, shopC.ID)

	// shopA: matching name -> VERIFIED (bank_review_pending = false).
	if _, err := srv.AddShopBankAccount(sellerACtx, &userv1.AddShopBankAccountRequest{
		BankCode: "VCB", BankName: "Vietcombank", AccountNumber: "19034567891001", AccountHolderName: "NGUYEN VAN AN",
	}); err != nil {
		t.Fatalf("shopA AddShopBankAccount: %v", err)
	}
	if _, err := srv.VerifyShopBankAccountOTP(sellerACtx, &userv1.VerifyShopBankAccountOTPRequest{
		Otp: pub.shopBankAccountOTP[len(pub.shopBankAccountOTP)-1].OTP,
	}); err != nil {
		t.Fatalf("shopA VerifyShopBankAccountOTP: %v", err)
	}

	// shopB & shopC: mismatched holder names -> PENDING_ADMIN_REVIEW (bank_review_pending = true).
	addB, err := srv.AddShopBankAccount(sellerBCtx, &userv1.AddShopBankAccountRequest{
		BankCode: "TCB", BankName: "Techcombank", AccountNumber: "19034567891002", AccountHolderName: "CONG TY TNHH B",
	})
	if err != nil {
		t.Fatalf("shopB AddShopBankAccount: %v", err)
	}
	if _, err := srv.VerifyShopBankAccountOTP(sellerBCtx, &userv1.VerifyShopBankAccountOTPRequest{
		Otp: pub.shopBankAccountOTP[len(pub.shopBankAccountOTP)-1].OTP,
	}); err != nil {
		t.Fatalf("shopB VerifyShopBankAccountOTP: %v", err)
	}

	addC, err := srv.AddShopBankAccount(sellerCCtx, &userv1.AddShopBankAccountRequest{
		BankCode: "MB", BankName: "MB Bank", AccountNumber: "19034567891003", AccountHolderName: "CONG TY TNHH C",
	})
	if err != nil {
		t.Fatalf("shopC AddShopBankAccount: %v", err)
	}
	if _, err := srv.VerifyShopBankAccountOTP(sellerCCtx, &userv1.VerifyShopBankAccountOTPRequest{
		Otp: pub.shopBankAccountOTP[len(pub.shopBankAccountOTP)-1].OTP,
	}); err != nil {
		t.Fatalf("shopC VerifyShopBankAccountOTP: %v", err)
	}

	// 1. Non-admin callers cannot call AdminListShops.
	if _, err := srv.AdminListShops(buyerCtx, &userv1.AdminListShopsRequest{BankReviewPendingOnly: true}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("buyer AdminListShops: want PermissionDenied, got %v", err)
	}
	if _, err := srv.AdminListShops(sellerBCtx, &userv1.AdminListShopsRequest{BankReviewPendingOnly: true}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("seller AdminListShops: want PermissionDenied, got %v", err)
	}

	// 2. AdminListShops without filter returns all 4 shops with bank_review_pending accurately populated.
	allResp, err := srv.AdminListShops(adminCtx, &userv1.AdminListShopsRequest{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("AdminListShops(all): %v", err)
	}
	if allResp.GetTotal() != 4 || len(allResp.GetShops()) != 4 {
		t.Fatalf("AdminListShops(all): want total=4 len=4, got total=%d len=%d", allResp.GetTotal(), len(allResp.GetShops()))
	}
	pendingByShop := make(map[string]bool, len(allResp.GetShops()))
	for _, sh := range allResp.GetShops() {
		pendingByShop[sh.GetId()] = sh.GetBankReviewPending()
	}
	if pendingByShop[shopA.ID] || !pendingByShop[shopB.ID] || !pendingByShop[shopC.ID] || pendingByShop[shopPending.ID] {
		t.Fatalf("unexpected bank_review_pending flags: A=%v B=%v C=%v Pending=%v",
			pendingByShop[shopA.ID], pendingByShop[shopB.ID], pendingByShop[shopC.ID], pendingByShop[shopPending.ID])
	}

	// 3. bank_review_pending_only = true returns ONLY shopB & shopC with exact total & pagination.
	filtPage1, err := srv.AdminListShops(adminCtx, &userv1.AdminListShopsRequest{
		BankReviewPendingOnly: true,
		Page:                  1,
		PageSize:              1,
	})
	if err != nil {
		t.Fatalf("AdminListShops(pending page 1): %v", err)
	}
	if filtPage1.GetTotal() != 2 || len(filtPage1.GetShops()) != 1 || filtPage1.GetShops()[0].GetId() != shopC.ID || !filtPage1.GetShops()[0].GetBankReviewPending() {
		t.Fatalf("page 1 mismatch: total=%d shops=%+v", filtPage1.GetTotal(), filtPage1.GetShops())
	}
	filtPage2, err := srv.AdminListShops(adminCtx, &userv1.AdminListShopsRequest{
		BankReviewPendingOnly: true,
		Page:                  2,
		PageSize:              1,
	})
	if err != nil {
		t.Fatalf("AdminListShops(pending page 2): %v", err)
	}
	if filtPage2.GetTotal() != 2 || len(filtPage2.GetShops()) != 1 || filtPage2.GetShops()[0].GetId() != shopB.ID || !filtPage2.GetShops()[0].GetBankReviewPending() {
		t.Fatalf("page 2 mismatch: total=%d shops=%+v", filtPage2.GetTotal(), filtPage2.GetShops())
	}

	// 4. Combines cleanly with status filter.
	combActive, err := srv.AdminListShops(adminCtx, &userv1.AdminListShopsRequest{
		Status:                userv1.ShopStatus_SHOP_STATUS_ACTIVE,
		BankReviewPendingOnly: true,
	})
	if err != nil || combActive.GetTotal() != 2 || len(combActive.GetShops()) != 2 {
		t.Fatalf("status=ACTIVE + bank_review_pending_only=true: want total=2, got err=%v total=%d", err, combActive.GetTotal())
	}
	combPending, err := srv.AdminListShops(adminCtx, &userv1.AdminListShopsRequest{
		Status:                userv1.ShopStatus_SHOP_STATUS_PENDING,
		BankReviewPendingOnly: true,
	})
	if err != nil || combPending.GetTotal() != 0 || len(combPending.GetShops()) != 0 {
		t.Fatalf("status=PENDING + bank_review_pending_only=true: want total=0, got err=%v total=%d", err, combPending.GetTotal())
	}

	// 5. GetShop and ListPublicShops ALWAYS leave bank_review_pending = false (even for shops with pending review).
	getB, err := srv.GetShop(context.Background(), &userv1.GetShopRequest{ShopId: shopB.ID})
	if err != nil || getB.GetShop().GetBankReviewPending() {
		t.Fatalf("GetShop must never expose bank_review_pending=true: err=%v pending=%v", err, getB.GetShop().GetBankReviewPending())
	}
	getBSeller, err := srv.GetShop(sellerBCtx, &userv1.GetShopRequest{ShopId: shopB.ID})
	if err != nil || getBSeller.GetShop().GetBankReviewPending() {
		t.Fatalf("GetShop(seller) must never expose bank_review_pending=true: err=%v pending=%v", err, getBSeller.GetShop().GetBankReviewPending())
	}
	pubShops, err := srv.ListPublicShops(context.Background(), &userv1.ListPublicShopsRequest{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("ListPublicShops: %v", err)
	}
	for _, sh := range pubShops.GetShops() {
		if sh.GetBankReviewPending() {
			t.Fatalf("ListPublicShops leaked bank_review_pending=true on shop %s", sh.GetId())
		}
	}

	// 6. After admin approves shopB and rejects shopC, both disappear from bank_review_pending_only filter.
	if _, err := srv.AdminReviewShopBankAccount(adminCtx, &userv1.AdminReviewShopBankAccountRequest{
		ShopId: shopB.ID, BankAccountId: addB.GetBankAccount().GetId(), Decision: userv1.ReviewDecision_REVIEW_DECISION_APPROVE,
	}); err != nil {
		t.Fatalf("approve shopB bank: %v", err)
	}
	if _, err := srv.AdminReviewShopBankAccount(adminCtx, &userv1.AdminReviewShopBankAccountRequest{
		ShopId: shopC.ID, BankAccountId: addC.GetBankAccount().GetId(), Decision: userv1.ReviewDecision_REVIEW_DECISION_REJECT, Reason: "Thiếu hồ sơ",
	}); err != nil {
		t.Fatalf("reject shopC bank: %v", err)
	}

	afterFilt, err := srv.AdminListShops(adminCtx, &userv1.AdminListShopsRequest{BankReviewPendingOnly: true})
	if err != nil || afterFilt.GetTotal() != 0 || len(afterFilt.GetShops()) != 0 {
		t.Fatalf("after review, bank_review_pending_only=true: want 0, got err=%v total=%d", err, afterFilt.GetTotal())
	}
	afterAll, err := srv.AdminListShops(adminCtx, &userv1.AdminListShopsRequest{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("AdminListShops after review: %v", err)
	}
	for _, sh := range afterAll.GetShops() {
		if sh.GetBankReviewPending() {
			t.Fatalf("expected bank_review_pending=false for shop %s after review", sh.GetId())
		}
	}
}

func TestW2B_Turn4_VerifiedOwnerNamePreventsUpdateProfileBypass(t *testing.T) {
	clk := newW2BClock(time.Date(2026, 11, 23, 15, 0, 0, 0, time.UTC))
	srv, store, _, pub, _ := newW2BHarness(t, Options{Now: clk.Now, OTPTTL: 10 * time.Minute})

	admin, _ := seedUserAndShop(store, "Admin Snap", "admin-snap@example.com", RoleAdmin, StatusActive, "", clk.Now())
	adminCtx := ctxWithUser(admin.ID, RoleAdmin, "")

	// Register buyer -> create pickup address -> RegisterShop -> AdminReviewShop(APPROVE).
	ownerID := registerActiveUser(t, srv, pub, "owner-snap@example.com", "0901222333")
	ownerCtx := ctxWithUser(ownerID, RoleBuyer, "")
	if _, err := srv.UpdateProfile(ownerCtx, &userv1.UpdateProfileRequest{FullName: "Nguyễn Văn Chủ"}); err != nil {
		t.Fatalf("UpdateProfile initial name: %v", err)
	}
	addrResp, err := srv.CreateAddress(ownerCtx, &userv1.CreateAddressRequest{
		RecipientName: "Nguyễn Văn Chủ", RecipientPhone: "0901222333",
		ProvinceCode: "01", WardCode: "00001", StreetAddress: "123 Le Loi", IsDefault: true,
	})
	if err != nil {
		t.Fatalf("CreateAddress: %v", err)
	}
	regShop, err := srv.RegisterShop(ownerCtx, &userv1.RegisterShopRequest{
		Name: "Shop Snapshot Owner", PickupAddressId: addrResp.GetAddress().GetId(),
		ContactPhone: "0901222333", BusinessLicenseDoc: "https://example.com/doc.pdf",
	})
	if err != nil {
		t.Fatalf("RegisterShop: %v", err)
	}
	if _, err := srv.AdminReviewShop(adminCtx, &userv1.AdminReviewShopRequest{
		ShopId: regShop.GetShop().GetId(), Decision: userv1.ReviewDecision_REVIEW_DECISION_APPROVE,
	}); err != nil {
		t.Fatalf("AdminReviewShop APPROVE: %v", err)
	}

	shopID := regShop.GetShop().GetId()
	sellerCtx := ctxWithUser(ownerID, RoleSeller, shopID)

	// Case A: Change users.full_name BEFORE AddShopBankAccount -> must NOT match new self-edited name!
	if _, err := srv.UpdateProfile(sellerCtx, &userv1.UpdateProfileRequest{FullName: "Kẻ Chiếm Tài Khoản"}); err != nil {
		t.Fatalf("UpdateProfile attacker name: %v", err)
	}
	addA, err := srv.AddShopBankAccount(sellerCtx, &userv1.AddShopBankAccountRequest{
		BankCode: "VCB", BankName: "Vietcombank", AccountNumber: "19034567892001", AccountHolderName: "KE CHIEM TAI KHOAN",
	})
	if err != nil {
		t.Fatalf("AddShopBankAccount Case A: %v", err)
	}
	if addA.GetBankAccount().GetHolderNameMatched() {
		t.Fatalf("Case A: self-edited UpdateProfile name must NOT match verified_owner_name")
	}
	verA, err := srv.VerifyShopBankAccountOTP(sellerCtx, &userv1.VerifyShopBankAccountOTPRequest{
		Otp: pub.shopBankAccountOTP[len(pub.shopBankAccountOTP)-1].OTP,
	})
	if err != nil {
		t.Fatalf("VerifyShopBankAccountOTP Case A: %v", err)
	}
	if verA.GetBankAccount().GetStatus() != userv1.ShopBankAccountStatus_SHOP_BANK_ACCOUNT_STATUS_PENDING_ADMIN_REVIEW {
		t.Fatalf("Case A: want PENDING_ADMIN_REVIEW, got %v", verA.GetBankAccount().GetStatus())
	}

	// Case B: Change users.full_name BETWEEN AddShopBankAccount and VerifyShopBankAccountOTP -> STILL must go to PENDING_ADMIN_REVIEW!
	if _, err := srv.AddShopBankAccount(sellerCtx, &userv1.AddShopBankAccountRequest{
		BankCode: "TCB", BankName: "Techcombank", AccountNumber: "19034567892002", AccountHolderName: "TEN GIA MAO GIUA CHUNG",
	}); err != nil {
		t.Fatalf("AddShopBankAccount Case B: %v", err)
	}
	if _, err := srv.UpdateProfile(sellerCtx, &userv1.UpdateProfileRequest{FullName: "Tên Giả Mạo Giữa Chừng"}); err != nil {
		t.Fatalf("UpdateProfile mid-OTP: %v", err)
	}
	verB, err := srv.VerifyShopBankAccountOTP(sellerCtx, &userv1.VerifyShopBankAccountOTPRequest{
		Otp: pub.shopBankAccountOTP[len(pub.shopBankAccountOTP)-1].OTP,
	})
	if err != nil {
		t.Fatalf("VerifyShopBankAccountOTP Case B: %v", err)
	}
	if verB.GetBankAccount().GetStatus() != userv1.ShopBankAccountStatus_SHOP_BANK_ACCOUNT_STATUS_PENDING_ADMIN_REVIEW ||
		verB.GetBankAccount().GetHolderNameMatched() {
		t.Fatalf("Case B: changing full_name during PENDING_OTP bypassed admin review: status=%v matched=%v",
			verB.GetBankAccount().GetStatus(), verB.GetBankAccount().GetHolderNameMatched())
	}

	// Case C: Account matching snapshotted verified_owner_name ("Nguyễn Văn Chủ") still auto-verifies even after UpdateProfile!
	if _, err := srv.AddShopBankAccount(sellerCtx, &userv1.AddShopBankAccountRequest{
		BankCode: "MB", BankName: "MB Bank", AccountNumber: "19034567892003", AccountHolderName: "NGUYEN VAN CHU",
	}); err != nil {
		t.Fatalf("AddShopBankAccount Case C: %v", err)
	}
	verC, err := srv.VerifyShopBankAccountOTP(sellerCtx, &userv1.VerifyShopBankAccountOTPRequest{
		Otp: pub.shopBankAccountOTP[len(pub.shopBankAccountOTP)-1].OTP,
	})
	if err != nil {
		t.Fatalf("VerifyShopBankAccountOTP Case C: %v", err)
	}
	if verC.GetBankAccount().GetStatus() != userv1.ShopBankAccountStatus_SHOP_BANK_ACCOUNT_STATUS_VERIFIED ||
		!verC.GetBankAccount().GetHolderNameMatched() {
		t.Fatalf("Case C: want VERIFIED against snapshotted verified_owner_name, got status=%v matched=%v",
			verC.GetBankAccount().GetStatus(), verC.GetBankAccount().GetHolderNameMatched())
	}

	// Case D: Rejected shop resubmitted after UpdateProfile -> re-approval snapshots the NEW owner full_name!
	owner2ID := registerActiveUser(t, srv, pub, "owner-resub@example.com", "0901222444")
	owner2Ctx := ctxWithUser(owner2ID, RoleBuyer, "")
	if _, err := srv.UpdateProfile(owner2Ctx, &userv1.UpdateProfileRequest{FullName: "Tên Cũ Trước Khi Duyệt"}); err != nil {
		t.Fatalf("UpdateProfile owner2 initial: %v", err)
	}
	addr2, err := srv.CreateAddress(owner2Ctx, &userv1.CreateAddressRequest{
		RecipientName: "Tên Cũ", RecipientPhone: "0901222444",
		ProvinceCode: "01", WardCode: "00001", StreetAddress: "456 Nguyen Trai", IsDefault: true,
	})
	if err != nil {
		t.Fatalf("CreateAddress owner2: %v", err)
	}
	reg2, err := srv.RegisterShop(owner2Ctx, &userv1.RegisterShopRequest{
		Name: "Shop Resubmit Snap", PickupAddressId: addr2.GetAddress().GetId(),
		ContactPhone: "0901222444", BusinessLicenseDoc: "https://example.com/old.pdf",
	})
	if err != nil {
		t.Fatalf("RegisterShop owner2: %v", err)
	}
	if _, err := srv.AdminReviewShop(adminCtx, &userv1.AdminReviewShopRequest{
		ShopId: reg2.GetShop().GetId(), Decision: userv1.ReviewDecision_REVIEW_DECISION_REJECT, Reason: "Sai giấy tờ",
	}); err != nil {
		t.Fatalf("AdminReviewShop REJECT: %v", err)
	}
	if _, err := srv.UpdateProfile(owner2Ctx, &userv1.UpdateProfileRequest{FullName: "Đặng Minh Khôi"}); err != nil {
		t.Fatalf("UpdateProfile owner2 corrected: %v", err)
	}
	if _, err := srv.RegisterShop(owner2Ctx, &userv1.RegisterShopRequest{
		Name: "Shop Resubmit Snap", PickupAddressId: addr2.GetAddress().GetId(),
		ContactPhone: "0901222444", BusinessLicenseDoc: "https://example.com/new.pdf",
	}); err != nil {
		t.Fatalf("RegisterShop resubmit: %v", err)
	}
	if _, err := srv.AdminReviewShop(adminCtx, &userv1.AdminReviewShopRequest{
		ShopId: reg2.GetShop().GetId(), Decision: userv1.ReviewDecision_REVIEW_DECISION_APPROVE,
	}); err != nil {
		t.Fatalf("AdminReviewShop re-APPROVE: %v", err)
	}
	seller2Ctx := ctxWithUser(owner2ID, RoleSeller, reg2.GetShop().GetId())
	if _, err := srv.AddShopBankAccount(seller2Ctx, &userv1.AddShopBankAccountRequest{
		BankCode: "ACB", BankName: "ACB", AccountNumber: "19034567892004", AccountHolderName: "DANG MINH KHOI",
	}); err != nil {
		t.Fatalf("AddShopBankAccount owner2: %v", err)
	}
	ver2, err := srv.VerifyShopBankAccountOTP(seller2Ctx, &userv1.VerifyShopBankAccountOTPRequest{
		Otp: pub.shopBankAccountOTP[len(pub.shopBankAccountOTP)-1].OTP,
	})
	if err != nil || ver2.GetBankAccount().GetStatus() != userv1.ShopBankAccountStatus_SHOP_BANK_ACCOUNT_STATUS_VERIFIED {
		t.Fatalf("resubmitted+approved shop should match newly captured verified_owner_name: err=%v status=%v", err, ver2.GetBankAccount().GetStatus())
	}

	// Case E: Empty verified_owner_name on an ACTIVE shop must NEVER match -> always PENDING_ADMIN_REVIEW.
	ownerEmpty, shopEmpty := seedUserAndShop(store, "Nguyễn Văn Rỗng", "owner-empty@example.com", RoleSeller, StatusActive, ShopStatusActive, clk.Now())
	store.mu.Lock()
	shEmpty := store.shops[shopEmpty.ID]
	shEmpty.VerifiedOwnerName = ""
	store.shops[shopEmpty.ID] = shEmpty
	store.mu.Unlock()
	emptyCtx := ctxWithUser(ownerEmpty.ID, RoleSeller, shopEmpty.ID)
	addE, err := srv.AddShopBankAccount(emptyCtx, &userv1.AddShopBankAccountRequest{
		BankCode: "VCB", BankName: "Vietcombank", AccountNumber: "19034567892005", AccountHolderName: "NGUYEN VAN RONG",
	})
	if err != nil {
		t.Fatalf("AddShopBankAccount Case E: %v", err)
	}
	if addE.GetBankAccount().GetHolderNameMatched() {
		t.Fatalf("Case E: empty verified_owner_name must yield holder_name_matched=false")
	}
	verE, err := srv.VerifyShopBankAccountOTP(emptyCtx, &userv1.VerifyShopBankAccountOTPRequest{
		Otp: pub.shopBankAccountOTP[len(pub.shopBankAccountOTP)-1].OTP,
	})
	if err != nil || verE.GetBankAccount().GetStatus() != userv1.ShopBankAccountStatus_SHOP_BANK_ACCOUNT_STATUS_PENDING_ADMIN_REVIEW {
		t.Fatalf("Case E: empty verified_owner_name must go to PENDING_ADMIN_REVIEW: err=%v status=%v", err, verE.GetBankAccount().GetStatus())
	}
}

func TestW2B_Turn4_VerifyOTPChecksOwnerActiveAndShopActive(t *testing.T) {
	clk := newW2BClock(time.Date(2026, 11, 23, 15, 30, 0, 0, time.UTC))
	srv, store, _, pub, _ := newW2BHarness(t, Options{Now: clk.Now, OTPTTL: 10 * time.Minute})

	owner, shop := seedUserAndShop(store, "Nguyễn Văn Khóa", "seller-lockv@example.com", RoleSeller, StatusActive, ShopStatusActive, clk.Now())
	sellerCtx := ctxWithUser(owner.ID, RoleSeller, shop.ID)

	if _, err := srv.AddShopBankAccount(sellerCtx, &userv1.AddShopBankAccountRequest{
		BankCode: "VCB", BankName: "Vietcombank", AccountNumber: "19034567893001", AccountHolderName: "NGUYEN VAN KHOA",
	}); err != nil {
		t.Fatalf("AddShopBankAccount: %v", err)
	}
	otp := pub.shopBankAccountOTP[len(pub.shopBankAccountOTP)-1].OTP

	// Lock owner account -> VerifyShopBankAccountOTP must fail with FailedPrecondition!
	store.mu.Lock()
	u := store.users[owner.ID]
	u.Status = StatusLocked
	store.users[owner.ID] = u
	store.mu.Unlock()

	if _, err := srv.VerifyShopBankAccountOTP(sellerCtx, &userv1.VerifyShopBankAccountOTPRequest{Otp: otp}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("VerifyShopBankAccountOTP with LOCKED owner: want FailedPrecondition, got %v", err)
	}

	// Restore owner ACTIVE, suspend shop -> VerifyShopBankAccountOTP must fail with FailedPrecondition!
	store.mu.Lock()
	u = store.users[owner.ID]
	u.Status = StatusActive
	store.users[owner.ID] = u
	sh := store.shops[shop.ID]
	sh.Status = ShopStatusSuspended
	store.shops[shop.ID] = sh
	store.mu.Unlock()

	if _, err := srv.VerifyShopBankAccountOTP(sellerCtx, &userv1.VerifyShopBankAccountOTPRequest{Otp: otp}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("VerifyShopBankAccountOTP with SUSPENDED shop: want FailedPrecondition, got %v", err)
	}
}

func TestW2B_Turn4_ExpiredOrMaxAttemptsTransitionsToTerminalNotStuck(t *testing.T) {
	clk := newW2BClock(time.Date(2026, 11, 23, 16, 0, 0, 0, time.UTC))
	srv, store, _, pub, _ := newW2BHarness(t, Options{Now: clk.Now, OTPTTL: 10 * time.Minute, BankOTPMaxAttempts: 5})

	owner, shop := seedUserAndShop(store, "Lê Thị Hết Hạn", "seller-term@example.com", RoleSeller, StatusActive, ShopStatusActive, clk.Now())
	sellerCtx := ctxWithUser(owner.ID, RoleSeller, shop.ID)

	// 1. Expired PENDING_OTP -> transitions to SUPERSEDED, GetShopBankAccount returns NotFound (not stuck on PENDING_OTP).
	addExp, err := srv.AddShopBankAccount(sellerCtx, &userv1.AddShopBankAccountRequest{
		BankCode: "VCB", BankName: "Vietcombank", AccountNumber: "19034567894001", AccountHolderName: "LE THI HET HAN",
	})
	if err != nil {
		t.Fatalf("AddShopBankAccount (expired): %v", err)
	}
	otpExp := pub.shopBankAccountOTP[len(pub.shopBankAccountOTP)-1].OTP
	clk.Advance(10*time.Minute + time.Second)

	if _, err := srv.VerifyShopBankAccountOTP(sellerCtx, &userv1.VerifyShopBankAccountOTPRequest{Otp: otpExp}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expired VerifyShopBankAccountOTP: want FailedPrecondition, got %v", err)
	}
	store.mu.Lock()
	expRow := store.shopBankAccounts[addExp.GetBankAccount().GetId()]
	store.mu.Unlock()
	if expRow.Status != BankAccountStatusSuperseded {
		t.Fatalf("expired PENDING_OTP row must move to SUPERSEDED, got %q", expRow.Status)
	}
	if _, err := srv.GetShopBankAccount(sellerCtx, &userv1.GetShopBankAccountRequest{}); status.Code(err) != codes.NotFound {
		t.Fatalf("GetShopBankAccount after expired OTP (no prior account): want NotFound, got %v", err)
	}

	// 2. Max wrong OTP attempts (5) -> transitions to SUPERSEDED, GetShopBankAccount returns NotFound (not stuck).
	addMax, err := srv.AddShopBankAccount(sellerCtx, &userv1.AddShopBankAccountRequest{
		BankCode: "TCB", BankName: "Techcombank", AccountNumber: "19034567894002", AccountHolderName: "LE THI HET HAN",
	})
	if err != nil {
		t.Fatalf("AddShopBankAccount (max attempts): %v", err)
	}
	realOTP := pub.shopBankAccountOTP[len(pub.shopBankAccountOTP)-1].OTP
	wrongOTP := "000000"
	if realOTP == wrongOTP {
		wrongOTP = "999999"
	}
	for i := 1; i <= 4; i++ {
		if _, err := srv.VerifyShopBankAccountOTP(sellerCtx, &userv1.VerifyShopBankAccountOTPRequest{Otp: wrongOTP}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("wrong OTP %d: want InvalidArgument, got %v", i, err)
		}
	}
	if _, err := srv.VerifyShopBankAccountOTP(sellerCtx, &userv1.VerifyShopBankAccountOTPRequest{Otp: wrongOTP}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("5th wrong OTP: want FailedPrecondition, got %v", err)
	}
	store.mu.Lock()
	maxRow := store.shopBankAccounts[addMax.GetBankAccount().GetId()]
	store.mu.Unlock()
	if maxRow.Status != BankAccountStatusSuperseded {
		t.Fatalf("max-attempts PENDING_OTP row must move to SUPERSEDED, got %q", maxRow.Status)
	}
	if _, err := srv.GetShopBankAccount(sellerCtx, &userv1.GetShopBankAccountRequest{}); status.Code(err) != codes.NotFound {
		t.Fatalf("GetShopBankAccount after 5 wrong OTPs: want NotFound, got %v", err)
	}
}

func TestW2B_Turn4_PublishFailurePreservesPendingReviewAndDoesNotBurnRateLimit(t *testing.T) {
	clk := newW2BClock(time.Date(2026, 11, 23, 16, 30, 0, 0, time.UTC))
	srv, store, _, pub, _ := newW2BHarness(t, Options{Now: clk.Now, OTPTTL: 10 * time.Minute})

	admin, _ := seedUserAndShop(store, "Admin Pub", "admin-pub@example.com", RoleAdmin, StatusActive, "", clk.Now())
	owner, shop := seedUserAndShop(store, "Hoàng Văn Nam", "seller-pub@example.com", RoleSeller, StatusActive, ShopStatusActive, clk.Now())
	adminCtx := ctxWithUser(admin.ID, RoleAdmin, "")
	sellerCtx := ctxWithUser(owner.ID, RoleSeller, shop.ID)

	// 1. Create a PENDING_ADMIN_REVIEW bank account (counts as 1 successful email sent).
	addPending, err := srv.AddShopBankAccount(sellerCtx, &userv1.AddShopBankAccountRequest{
		BankCode: "VCB", BankName: "Vietcombank", AccountNumber: "19034567895100", AccountHolderName: "CONG TY KHAC TEN",
	})
	if err != nil {
		t.Fatalf("AddShopBankAccount #1: %v", err)
	}
	if _, err := srv.VerifyShopBankAccountOTP(sellerCtx, &userv1.VerifyShopBankAccountOTPRequest{
		Otp: pub.shopBankAccountOTP[len(pub.shopBankAccountOTP)-1].OTP,
	}); err != nil {
		t.Fatalf("VerifyShopBankAccountOTP #1: %v", err)
	}

	// 2. Simulate 3 consecutive NATS publish failures -> old PENDING_ADMIN_REVIEW MUST NOT be lost,
	// and failed publishes MUST NOT consume the 5/hour email limit!
	pub.publishBankOTPErr = errors.New("nats connection lost")
	for i := 1; i <= 3; i++ {
		clk.Advance(time.Minute)
		if _, err := srv.AddShopBankAccount(sellerCtx, &userv1.AddShopBankAccountRequest{
			BankCode: "TCB", BankName: "Techcombank", AccountNumber: "19034567895101", AccountHolderName: "HOANG VAN NAM",
		}); status.Code(err) != codes.Internal {
			t.Fatalf("publish-fail attempt %d: want Internal, got %v", i, err)
		}
	}
	pub.publishBankOTPErr = nil

	// Old PENDING_ADMIN_REVIEW must still be active & visible to seller and admin!
	gotAfterFail, err := srv.GetShopBankAccount(sellerCtx, &userv1.GetShopBankAccountRequest{})
	if err != nil || gotAfterFail.GetBankAccount().GetId() != addPending.GetBankAccount().GetId() ||
		gotAfterFail.GetBankAccount().GetStatus() != userv1.ShopBankAccountStatus_SHOP_BANK_ACCOUNT_STATUS_PENDING_ADMIN_REVIEW {
		t.Fatalf("old PENDING_ADMIN_REVIEW was lost after publish failure: err=%v acc=%+v", err, gotAfterFail)
	}
	adminPend, err := srv.AdminListShops(adminCtx, &userv1.AdminListShopsRequest{BankReviewPendingOnly: true})
	if err != nil || adminPend.GetTotal() != 1 || adminPend.GetShops()[0].GetId() != shop.ID {
		t.Fatalf("shop disappeared from bank_review_pending_only after publish failure: err=%v total=%d", err, adminPend.GetTotal())
	}

	// 3. Because the 3 failed publishes do NOT count toward the 5/hour limit, 4 more successful adds (total 5 sent emails) MUST succeed!
	for i := 2; i <= 5; i++ {
		clk.Advance(time.Minute)
		if _, err := srv.AddShopBankAccount(sellerCtx, &userv1.AddShopBankAccountRequest{
			BankCode: "MB", BankName: "MB Bank", AccountNumber: "19034567895102", AccountHolderName: "HOANG VAN NAM",
		}); err != nil {
			t.Fatalf("successful add #%d after publish failures should not be rate-limited: %v", i, err)
		}
	}

	// 4. 6th successful add within 1 hour MUST still be blocked with ResourceExhausted (spam protection preserved!).
	if _, err := srv.AddShopBankAccount(sellerCtx, &userv1.AddShopBankAccountRequest{
		BankCode: "MB", BankName: "MB Bank", AccountNumber: "19034567895103", AccountHolderName: "HOANG VAN NAM",
	}); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("6th sent email within 1h: want ResourceExhausted, got %v", err)
	}

	// 5. Verifying the 5th attempt's OTP NOW supersedes the old PENDING_ADMIN_REVIEW and marks the new account VERIFIED!
	ver5, err := srv.VerifyShopBankAccountOTP(sellerCtx, &userv1.VerifyShopBankAccountOTPRequest{
		Otp: pub.shopBankAccountOTP[len(pub.shopBankAccountOTP)-1].OTP,
	})
	if err != nil || ver5.GetBankAccount().GetStatus() != userv1.ShopBankAccountStatus_SHOP_BANK_ACCOUNT_STATUS_VERIFIED {
		t.Fatalf("verify 5th attempt OTP: err=%v status=%v", err, ver5.GetBankAccount().GetStatus())
	}
	adminPendAfter, err := srv.AdminListShops(adminCtx, &userv1.AdminListShopsRequest{BankReviewPendingOnly: true})
	if err != nil || adminPendAfter.GetTotal() != 0 {
		t.Fatalf("old PENDING_ADMIN_REVIEW must be superseded once new OTP is verified: err=%v total=%d", err, adminPendAfter.GetTotal())
	}
}

func TestW2B_Turn4_AdminReviewShopReturnsBankReviewPending(t *testing.T) {
	clk := newW2BClock(time.Date(2026, 11, 23, 17, 0, 0, 0, time.UTC))
	srv, store, _, _, _ := newW2BHarness(t, Options{Now: clk.Now})

	admin, _ := seedUserAndShop(store, "Admin RevP", "admin-revp@example.com", RoleAdmin, StatusActive, "", clk.Now())
	_, shop := seedUserAndShop(store, "Seller RevP", "seller-revp@example.com", RoleBuyer, StatusActive, ShopStatusPending, clk.Now())
	adminCtx := ctxWithUser(admin.ID, RoleAdmin, "")

	// Seed a PENDING_ADMIN_REVIEW bank account on the shop before AdminReviewShop runs.
	store.mu.Lock()
	store.shopBankAccounts["acc-revp"] = db.ShopBankAccount{
		ID:     "acc-revp",
		ShopID: shop.ID,
		Status: BankAccountStatusPendingAdminReview,
	}
	store.mu.Unlock()

	resp, err := srv.AdminReviewShop(adminCtx, &userv1.AdminReviewShopRequest{
		ShopId: shop.ID, Decision: userv1.ReviewDecision_REVIEW_DECISION_APPROVE,
	})
	if err != nil {
		t.Fatalf("AdminReviewShop: %v", err)
	}
	if !resp.GetShop().GetBankReviewPending() {
		t.Fatalf("AdminReviewShop must populate bank_review_pending=true via toAdminProtoShop")
	}
}

func TestW2B_Turn5_GetShopBankAccountAdminVsSellerAndExpiredOTP(t *testing.T) {
	clk := newW2BClock(time.Date(2026, 11, 23, 18, 0, 0, 0, time.UTC))
	srv, store, _, pub, _ := newW2BHarness(t, Options{Now: clk.Now, OTPTTL: 10 * time.Minute})

	admin, _ := seedUserAndShop(store, "Admin R5", "admin-r5@example.com", RoleAdmin, StatusActive, "", clk.Now())
	owner1, shop1 := seedUserAndShop(store, "Nguyễn Văn Năm", "seller-r5a@example.com", RoleSeller, StatusActive, ShopStatusActive, clk.Now())
	owner2, shop2 := seedUserAndShop(store, "Trần Thị Hết", "seller-r5b@example.com", RoleSeller, StatusActive, ShopStatusActive, clk.Now())
	adminCtx := ctxWithUser(admin.ID, RoleAdmin, "")
	seller1Ctx := ctxWithUser(owner1.ID, RoleSeller, shop1.ID)
	seller2Ctx := ctxWithUser(owner2.ID, RoleSeller, shop2.ID)

	// 1. Shop1 submits A1 (mismatched) and verifies OTP -> A1 is PENDING_ADMIN_REVIEW.
	addA1, err := srv.AddShopBankAccount(seller1Ctx, &userv1.AddShopBankAccountRequest{
		BankCode: "VCB", BankName: "Vietcombank", AccountNumber: "19034567897101", AccountHolderName: "CONG TY A1",
	})
	if err != nil {
		t.Fatalf("Add A1: %v", err)
	}
	if _, err := srv.VerifyShopBankAccountOTP(seller1Ctx, &userv1.VerifyShopBankAccountOTPRequest{
		Otp: pub.shopBankAccountOTP[len(pub.shopBankAccountOTP)-1].OTP,
	}); err != nil {
		t.Fatalf("Verify A1: %v", err)
	}

	// 2. Shop1 starts submitting A2 (PENDING_OTP, unexpired) while A1 is PENDING_ADMIN_REVIEW.
	clk.Advance(time.Minute)
	addA2, err := srv.AddShopBankAccount(seller1Ctx, &userv1.AddShopBankAccountRequest{
		BankCode: "TCB", BankName: "Techcombank", AccountNumber: "19034567897102", AccountHolderName: "NGUYEN VAN NAM",
	})
	if err != nil {
		t.Fatalf("Add A2: %v", err)
	}

	// While A2 is unexpired:
	// - Admin MUST see A1 (PENDING_ADMIN_REVIEW) so admin can review it!
	admWhileActive, err := srv.GetShopBankAccount(adminCtx, &userv1.GetShopBankAccountRequest{ShopId: shop1.ID})
	if err != nil {
		t.Fatalf("admin GetShopBankAccount (A1 pending + A2 active OTP): %v", err)
	}
	if admWhileActive.GetBankAccount().GetId() != addA1.GetBankAccount().GetId() ||
		admWhileActive.GetBankAccount().GetStatus() != userv1.ShopBankAccountStatus_SHOP_BANK_ACCOUNT_STATUS_PENDING_ADMIN_REVIEW {
		t.Fatalf("admin must see A1 PENDING_ADMIN_REVIEW (id=%s), got id=%s status=%v",
			addA1.GetBankAccount().GetId(), admWhileActive.GetBankAccount().GetId(), admWhileActive.GetBankAccount().GetStatus())
	}
	// - Seller MUST see A2 (unexpired PENDING_OTP) so seller can enter OTP!
	selWhileActive, err := srv.GetShopBankAccount(seller1Ctx, &userv1.GetShopBankAccountRequest{})
	if err != nil {
		t.Fatalf("seller GetShopBankAccount (A1 pending + A2 active OTP): %v", err)
	}
	if selWhileActive.GetBankAccount().GetId() != addA2.GetBankAccount().GetId() ||
		selWhileActive.GetBankAccount().GetStatus() != userv1.ShopBankAccountStatus_SHOP_BANK_ACCOUNT_STATUS_PENDING_OTP {
		t.Fatalf("seller must see A2 unexpired PENDING_OTP (id=%s), got id=%s status=%v",
			addA2.GetBankAccount().GetId(), selWhileActive.GetBankAccount().GetId(), selWhileActive.GetBankAccount().GetStatus())
	}

	// 3. Advance fake clock so A2 expires (exact boundary otp_expires_at == now) WITHOUT calling Verify!
	clk.Advance(10 * time.Minute)
	// - Seller must NO LONGER see expired A2; falls back to A1 (PENDING_ADMIN_REVIEW)!
	selAfterExpiry, err := srv.GetShopBankAccount(seller1Ctx, &userv1.GetShopBankAccountRequest{})
	if err != nil {
		t.Fatalf("seller GetShopBankAccount after A2 expired: %v", err)
	}
	if selAfterExpiry.GetBankAccount().GetId() != addA1.GetBankAccount().GetId() ||
		selAfterExpiry.GetBankAccount().GetStatus() != userv1.ShopBankAccountStatus_SHOP_BANK_ACCOUNT_STATUS_PENDING_ADMIN_REVIEW {
		t.Fatalf("seller must fall back to A1 after A2 expires, got id=%s status=%v",
			selAfterExpiry.GetBankAccount().GetId(), selAfterExpiry.GetBankAccount().GetStatus())
	}
	// - Admin still sees A1 (PENDING_ADMIN_REVIEW) and can approve it!
	admAfterExpiry, err := srv.GetShopBankAccount(adminCtx, &userv1.GetShopBankAccountRequest{ShopId: shop1.ID})
	if err != nil || admAfterExpiry.GetBankAccount().GetId() != addA1.GetBankAccount().GetId() {
		t.Fatalf("admin must see A1 after A2 expires: err=%v acc=%+v", err, admAfterExpiry)
	}

	// 4. Shop2 has ONLY a PENDING_OTP account; once it expires, BOTH seller and admin get NotFound (not stuck!).
	if _, err := srv.AddShopBankAccount(seller2Ctx, &userv1.AddShopBankAccountRequest{
		BankCode: "MB", BankName: "MB Bank", AccountNumber: "19034567897103", AccountHolderName: "TRAN THI HET",
	}); err != nil {
		t.Fatalf("Shop2 Add: %v", err)
	}
	// While unexpired, admin (having no PENDING_ADMIN_REVIEW) sees the active PENDING_OTP.
	admShop2Active, err := srv.GetShopBankAccount(adminCtx, &userv1.GetShopBankAccountRequest{ShopId: shop2.ID})
	if err != nil || admShop2Active.GetBankAccount().GetStatus() != userv1.ShopBankAccountStatus_SHOP_BANK_ACCOUNT_STATUS_PENDING_OTP {
		t.Fatalf("admin on shop2 with only unexpired PENDING_OTP: want PENDING_OTP, got err=%v acc=%+v", err, admShop2Active)
	}
	clk.Advance(10*time.Minute + time.Second)
	if _, err := srv.GetShopBankAccount(seller2Ctx, &userv1.GetShopBankAccountRequest{}); status.Code(err) != codes.NotFound {
		t.Fatalf("seller on shop2 with only expired PENDING_OTP: want NotFound, got %v", err)
	}
	if _, err := srv.GetShopBankAccount(adminCtx, &userv1.GetShopBankAccountRequest{ShopId: shop2.ID}); status.Code(err) != codes.NotFound {
		t.Fatalf("admin on shop2 with only expired PENDING_OTP: want NotFound, got %v", err)
	}
}

func TestW2B_Turn5_PublishFailureWithCancelledContextStillMarksPublishFailed(t *testing.T) {
	clk := newW2BClock(time.Date(2026, 11, 23, 18, 30, 0, 0, time.UTC))
	srv, store, _, pub, _ := newW2BHarness(t, Options{Now: clk.Now, OTPTTL: 10 * time.Minute})

	admin, _ := seedUserAndShop(store, "Admin Ctx", "admin-r5ctx@example.com", RoleAdmin, StatusActive, "", clk.Now())
	owner, shop := seedUserAndShop(store, "Phạm Văn Hủy", "seller-r5ctx@example.com", RoleSeller, StatusActive, ShopStatusActive, clk.Now())
	adminCtx := ctxWithUser(admin.ID, RoleAdmin, "")
	baseSellerCtx := ctxWithUser(owner.ID, RoleSeller, shop.ID)

	// Seed an existing PENDING_ADMIN_REVIEW account A1.
	addA1, err := srv.AddShopBankAccount(baseSellerCtx, &userv1.AddShopBankAccountRequest{
		BankCode: "VCB", BankName: "Vietcombank", AccountNumber: "19034567897201", AccountHolderName: "CONG TY C1",
	})
	if err != nil {
		t.Fatalf("Add A1: %v", err)
	}
	if _, err := srv.VerifyShopBankAccountOTP(baseSellerCtx, &userv1.VerifyShopBankAccountOTPRequest{
		Otp: pub.shopBankAccountOTP[len(pub.shopBankAccountOTP)-1].OTP,
	}); err != nil {
		t.Fatalf("Verify A1: %v", err)
	}

	// Simulate caller ctx getting canceled right as PublishShopBankAccountOTP runs.
	cancelCtx, cancel := context.WithCancel(baseSellerCtx)
	pub.onPublishBankOTP = func(_ context.Context) error {
		cancel()
		return context.Canceled
	}
	defer func() { pub.onPublishBankOTP = nil }()

	clk.Advance(time.Minute)
	if _, err := srv.AddShopBankAccount(cancelCtx, &userv1.AddShopBankAccountRequest{
		BankCode: "TCB", BankName: "Techcombank", AccountNumber: "19034567897202", AccountHolderName: "PHAM VAN HUY",
	}); status.Code(err) != codes.Internal {
		t.Fatalf("AddShopBankAccount with cancelled publish ctx: want Internal, got %v", err)
	}
	pub.onPublishBankOTP = nil

	// The failed row MUST be marked SUPERSEDED + otp_publish_failed=true (NOT stuck PENDING_OTP!).
	store.mu.Lock()
	var failedRow *db.ShopBankAccount
	for id, acc := range store.shopBankAccounts {
		if acc.ShopID == shop.ID && id != addA1.GetBankAccount().GetId() {
			cp := acc
			failedRow = &cp
		}
	}
	store.mu.Unlock()
	if failedRow == nil {
		t.Fatalf("expected failed publish row in store")
	}
	if failedRow.Status != BankAccountStatusSuperseded || !failedRow.OTPPublishFailed {
		t.Fatalf("cancelled ctx broke cleanup: want status=SUPERSEDED otp_publish_failed=true, got status=%q otp_publish_failed=%v",
			failedRow.Status, failedRow.OTPPublishFailed)
	}

	// Admin and seller must still see A1 (PENDING_ADMIN_REVIEW), and rate limit must still count only 1 sent email!
	gotSel, err := srv.GetShopBankAccount(baseSellerCtx, &userv1.GetShopBankAccountRequest{})
	if err != nil || gotSel.GetBankAccount().GetId() != addA1.GetBankAccount().GetId() {
		t.Fatalf("seller GetShopBankAccount after cancelled publish: want A1, got err=%v acc=%+v", err, gotSel)
	}
	gotAdm, err := srv.GetShopBankAccount(adminCtx, &userv1.GetShopBankAccountRequest{ShopId: shop.ID})
	if err != nil || gotAdm.GetBankAccount().GetId() != addA1.GetBankAccount().GetId() {
		t.Fatalf("admin GetShopBankAccount after cancelled publish: want A1, got err=%v acc=%+v", err, gotAdm)
	}
	cnt, err := store.CountShopBankAccountsCreatedSince(context.Background(), db.CountShopBankAccountsCreatedSinceParams{
		ShopID:    shop.ID,
		CreatedAt: clk.Now().Add(-time.Hour),
	})
	if err != nil || cnt != 1 {
		t.Fatalf("cancelled publish should not burn rate limit: want count=1, got cnt=%d err=%v", cnt, err)
	}
}
