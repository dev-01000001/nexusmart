package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/user/internal/db"
	"github.com/nexusmart/core/services/user/internal/password"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func ctxWithToken(t *testing.T, authMgr *auth.Manager, accessToken string) context.Context {
	t.Helper()
	claims, err := authMgr.VerifyAccessToken(accessToken)
	if err != nil {
		t.Fatalf("VerifyAccessToken: %v", err)
	}
	return auth.WithClaims(context.Background(), claims)
}

func TestW2A_USRB1_ImmediateRevocationOnLockAndRoleChange(t *testing.T) {
	srv, store, sec, pub, authMgr := newTestHarness(t)
	ctx := context.Background()

	// Seed super_admin and a buyer.
	adminID := registerActiveUser(t, srv, pub, "w2a-super@example.com", "0901000001")
	store.mu.Lock()
	adm := store.users[adminID]
	adm.Role = RoleSuperAdmin
	store.users[adminID] = adm
	store.mu.Unlock()
	adminCtx := ctxWithUser(adminID, RoleSuperAdmin, "")

	buyerID := registerActiveUser(t, srv, pub, "w2a-buyer@example.com", "0901000002")
	login1, err := srv.Login(ctx, &userv1.LoginRequest{
		Email:    "w2a-buyer@example.com",
		Password: "Password123!",
	})
	if err != nil {
		t.Fatalf("Login buyer: %v", err)
	}
	buyerTokCtx := ctxWithToken(t, authMgr, login1.GetAccessToken())

	// Sanity: token works before lock.
	if _, err := srv.GetProfile(buyerTokCtx, &userv1.GetProfileRequest{}); err != nil {
		t.Fatalf("GetProfile before lock: %v", err)
	}

	// Lock buyer with reason -> existing access token must be rejected immediately, TTL = 16m.
	lockResp, err := srv.AdminUpdateUserStatus(adminCtx, &userv1.AdminUpdateUserStatusRequest{
		TargetUserId: buyerID,
		Status:       userv1.UserStatus_USER_STATUS_LOCKED,
		Reason:       "Vi phạm chính sách hoàn tiền",
	})
	if err != nil {
		t.Fatalf("AdminUpdateUserStatus LOCKED: %v", err)
	}
	if lockResp.GetUser().GetLockReason() != "Vi phạm chính sách hoàn tiền" {
		t.Fatalf("expected lock_reason persisted, got %q", lockResp.GetUser().GetLockReason())
	}
	sec.mu.Lock()
	gotTTL := sec.revokedBeforeTTL[buyerID]
	sec.mu.Unlock()
	if gotTTL != 16*time.Minute {
		t.Fatalf("expected revoked_before TTL 16m, got %s", gotTTL)
	}
	if _, err := srv.GetProfile(buyerTokCtx, &userv1.GetProfileRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("locked user's old access token: want Unauthenticated, got %v", err)
	}

	// Unlock buyer -> lock_reason cleared; promote to admin -> old access + refresh revoked.
	unlockResp, err := srv.AdminUpdateUserStatus(adminCtx, &userv1.AdminUpdateUserStatusRequest{
		TargetUserId: buyerID,
		Status:       userv1.UserStatus_USER_STATUS_ACTIVE,
	})
	if err != nil {
		t.Fatalf("AdminUpdateUserStatus ACTIVE: %v", err)
	}
	if unlockResp.GetUser().GetLockReason() != "" {
		t.Fatalf("expected lock_reason cleared on unlock, got %q", unlockResp.GetUser().GetLockReason())
	}

	login2, err := srv.Login(ctx, &userv1.LoginRequest{
		Email:    "w2a-buyer@example.com",
		Password: "Password123!",
	})
	if err != nil {
		t.Fatalf("Login after unlock: %v", err)
	}
	tok2Ctx := ctxWithToken(t, authMgr, login2.GetAccessToken())
	// Fresh token issued after unlock must work even while revoked_before is still cached.
	if _, err := srv.GetProfile(tok2Ctx, &userv1.GetProfileRequest{}); err != nil {
		t.Fatalf("fresh login token after revoke: want OK, got %v", err)
	}

	// Promote buyer -> admin (upward role change must revoke access + refresh tokens).
	if _, err := srv.AdminUpdateUserRole(adminCtx, &userv1.AdminUpdateUserRoleRequest{
		TargetUserId: buyerID,
		Role:         userv1.UserRole_USER_ROLE_ADMIN,
		Reason:       "promote to admin",
	}); err != nil {
		t.Fatalf("AdminUpdateUserRole promote: %v", err)
	}
	if _, err := srv.GetProfile(tok2Ctx, &userv1.GetProfileRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("promoted user's old access token: want Unauthenticated, got %v", err)
	}
	if _, err := srv.RefreshToken(ctx, &userv1.RefreshTokenRequest{RefreshToken: login2.GetRefreshToken()}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("promoted user's old refresh token: want Unauthenticated, got %v", err)
	}

	// Login as admin, then demote to buyer -> old admin access + refresh tokens revoked.
	loginAdmin, err := srv.Login(ctx, &userv1.LoginRequest{
		Email:    "w2a-buyer@example.com",
		Password: "Password123!",
	})
	if err != nil {
		t.Fatalf("Login promoted admin: %v", err)
	}
	demotedAdminCtx := ctxWithToken(t, authMgr, loginAdmin.GetAccessToken())

	if _, err := srv.AdminUpdateUserRole(adminCtx, &userv1.AdminUpdateUserRoleRequest{
		TargetUserId: buyerID,
		Role:         userv1.UserRole_USER_ROLE_BUYER,
		Reason:       "demote to buyer",
	}); err != nil {
		t.Fatalf("AdminUpdateUserRole demote: %v", err)
	}
	if _, err := srv.AdminListUsers(demotedAdminCtx, &userv1.AdminListUsersRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("demoted admin's old access token: want Unauthenticated, got %v", err)
	}
	if _, err := srv.RefreshToken(ctx, &userv1.RefreshTokenRequest{RefreshToken: loginAdmin.GetRefreshToken()}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("demoted admin's old refresh token: want Unauthenticated, got %v", err)
	}
}

func TestW2A_USRB1_RedisFailureRollsBackAndFailsClosed(t *testing.T) {
	srv, store, sec, pub, authMgr := newTestHarness(t)
	ctx := context.Background()

	adminID := registerActiveUser(t, srv, pub, "w2a-rb-super@example.com", "0901000003")
	store.mu.Lock()
	adm := store.users[adminID]
	adm.Role = RoleSuperAdmin
	store.users[adminID] = adm
	store.mu.Unlock()
	adminCtx := ctxWithUser(adminID, RoleSuperAdmin, "")

	buyerID := registerActiveUser(t, srv, pub, "w2a-rb-buyer@example.com", "0901000004")
	loginResp, err := srv.Login(ctx, &userv1.LoginRequest{
		Email:    "w2a-rb-buyer@example.com",
		Password: "Password123!",
	})
	if err != nil {
		t.Fatalf("Login buyer: %v", err)
	}
	buyerCtx := ctxWithToken(t, authMgr, loginResp.GetAccessToken())

	// Simulate Redis outage during SetRevokedBefore -> lock must fail AND roll back DB!
	sec.mu.Lock()
	sec.setRevokedBeforeErr = errors.New("redis connection refused")
	sec.mu.Unlock()

	if _, err := srv.AdminUpdateUserStatus(adminCtx, &userv1.AdminUpdateUserStatusRequest{
		TargetUserId: buyerID,
		Status:       userv1.UserStatus_USER_STATUS_LOCKED,
		Reason:       "redis down test",
	}); status.Code(err) != codes.Unavailable {
		t.Fatalf("lock during Redis outage: want Unavailable, got %v", err)
	}
	uAfterLockFail, _ := store.GetUserByID(ctx, buyerID)
	if uAfterLockFail.Status != StatusActive || uAfterLockFail.LockReason != "" {
		t.Fatalf("user was half-locked after Redis failure: status=%q lock_reason=%q", uAfterLockFail.Status, uAfterLockFail.LockReason)
	}

	// Simulate Redis outage during AdminUpdateUserRole -> role change must fail AND roll back DB!
	if _, err := srv.AdminUpdateUserRole(adminCtx, &userv1.AdminUpdateUserRoleRequest{
		TargetUserId: buyerID,
		Role:         userv1.UserRole_USER_ROLE_ADMIN,
		Reason:       "redis down role test",
	}); status.Code(err) != codes.Unavailable {
		t.Fatalf("role update during Redis outage: want Unavailable, got %v", err)
	}
	uAfterRoleFail, _ := store.GetUserByID(ctx, buyerID)
	if uAfterRoleFail.Role != RoleBuyer {
		t.Fatalf("user role mutated after Redis failure: got %q, want %q", uAfterRoleFail.Role, RoleBuyer)
	}

	// Refresh token must still be usable because the DB tx rolled back cleanly.
	if _, err := srv.RefreshToken(ctx, &userv1.RefreshTokenRequest{RefreshToken: loginResp.GetRefreshToken()}); err != nil {
		t.Fatalf("refresh token was revoked despite rolled-back tx: %v", err)
	}

	// Clear write error; inject read error on IsTokenRevoked -> requireAuthClaims must fail closed (Unavailable).
	sec.mu.Lock()
	sec.setRevokedBeforeErr = nil
	sec.isTokenRevokedErr = errors.New("redis timeout")
	sec.mu.Unlock()

	if _, err := srv.GetProfile(buyerCtx, &userv1.GetProfileRequest{}); status.Code(err) != codes.Unavailable {
		t.Fatalf("GetProfile during Redis read outage: want Unavailable (fail-closed), got %v", err)
	}
}

func TestW2A_USRB8_AntiEnumerationAndLockedLoginReason(t *testing.T) {
	srv, store, _, pub, _ := newTestHarness(t)
	ctx := context.Background()

	activeID := registerActiveUser(t, srv, pub, "w2a-active@example.com", "0901000010")
	if _, err := srv.Register(ctx, &userv1.RegisterRequest{
		FullName: "Pending User",
		Email:    "w2a-pending@example.com",
		Phone:    "0901000011",
		Password: "Password123!",
	}); err != nil {
		t.Fatalf("Register pending: %v", err)
	}

	// Register duplicate email retains 409 AlreadyExists; duplicate phone returns 409 AlreadyExists when phone_verified=true (E19-bis).
	store.mu.Lock()
	actUser := store.users[activeID]
	actUser.PhoneVerified = true
	store.users[activeID] = actUser
	store.mu.Unlock()

	if _, err := srv.Register(ctx, &userv1.RegisterRequest{
		FullName: "Dup Email",
		Email:    "w2a-active@example.com",
		Phone:    "0901000099",
		Password: "Password123!",
	}); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("duplicate email Register: want AlreadyExists, got %v", err)
	}
	if _, err := srv.Register(ctx, &userv1.RegisterRequest{
		FullName: "Dup Phone",
		Email:    "w2a-dupphone@example.com",
		Phone:    "0901000010",
		Password: "Password123!",
	}); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("duplicate verified phone Register: want AlreadyExists, got %v", err)
	}

	// ResendOTP: unknown email and active email must both return Sent=true without emitting events.
	eventsBefore := len(pub.userRegistered)
	for _, em := range []string{"does-not-exist@example.com", "w2a-active@example.com"} {
		resp, err := srv.ResendOTP(ctx, &userv1.ResendOTPRequest{Email: em})
		if err != nil || !resp.GetSent() {
			t.Fatalf("ResendOTP(%s): want Sent=true err=nil, got resp=%+v err=%v", em, resp, err)
		}
	}
	if len(pub.userRegistered) != eventsBefore {
		t.Fatalf("ResendOTP on unknown/active email unexpectedly emitted %d event(s)", len(pub.userRegistered)-eventsBefore)
	}

	// VerifyOTP: unknown email, active user, wrong OTP, and missing/expired OTP MUST return identical 400 error.
	_, errUnknown := srv.VerifyOTP(ctx, &userv1.VerifyOTPRequest{Email: "does-not-exist@example.com", Otp: "000000"})
	_, errActive := srv.VerifyOTP(ctx, &userv1.VerifyOTPRequest{Email: "w2a-active@example.com", Otp: "000000"})
	_, errWrong := srv.VerifyOTP(ctx, &userv1.VerifyOTPRequest{Email: "w2a-pending@example.com", Otp: "000000"})
	for name, err := range map[string]error{"unknown": errUnknown, "active": errActive, "wrong": errWrong} {
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("VerifyOTP(%s): want InvalidArgument (400), got %v", name, err)
		}
	}
	msgUnknown := status.Convert(errUnknown).Message()
	if msgUnknown != status.Convert(errActive).Message() || msgUnknown != status.Convert(errWrong).Message() {
		t.Fatalf("VerifyOTP error messages differ: unknown=%q active=%q wrong=%q",
			msgUnknown, status.Convert(errActive).Message(), status.Convert(errWrong).Message())
	}

	// Lock active user with reason.
	adminID := registerActiveUser(t, srv, pub, "w2a-adm8@example.com", "0901000012")
	store.mu.Lock()
	adm := store.users[adminID]
	adm.Role = RoleSuperAdmin
	store.users[adminID] = adm
	store.mu.Unlock()
	if _, err := srv.AdminUpdateUserStatus(ctxWithUser(adminID, RoleSuperAdmin, ""), &userv1.AdminUpdateUserStatusRequest{
		TargetUserId: activeID,
		Status:       userv1.UserStatus_USER_STATUS_LOCKED,
		Reason:       "Phát hiện giao dịch bất thường",
	}); err != nil {
		t.Fatalf("lock user: %v", err)
	}

	// LOCKED + wrong password -> 401 Unauthenticated (no lock reason leaked).
	_, errLockedWrong := srv.Login(ctx, &userv1.LoginRequest{
		Email:    "w2a-active@example.com",
		Password: "WrongPassword123!",
	})
	if status.Code(errLockedWrong) != codes.Unauthenticated {
		t.Fatalf("LOCKED + wrong password: want Unauthenticated, got %v", errLockedWrong)
	}
	if strings.Contains(errLockedWrong.Error(), "bất thường") {
		t.Fatalf("LOCKED + wrong password leaked lock reason: %v", errLockedWrong)
	}

	// LOCKED + right password -> 403 PermissionDenied with lock reason and support contact.
	_, errLockedRight := srv.Login(ctx, &userv1.LoginRequest{
		Email:    "w2a-active@example.com",
		Password: "Password123!",
	})
	if status.Code(errLockedRight) != codes.PermissionDenied {
		t.Fatalf("LOCKED + right password: want PermissionDenied, got %v", errLockedRight)
	}
	msg := status.Convert(errLockedRight).Message()
	if !strings.Contains(msg, "Phát hiện giao dịch bất thường") || !strings.Contains(strings.ToLower(msg), "support@nexusmart.vn") {
		t.Fatalf("LOCKED + right password message missing reason or support contact: %q", msg)
	}

	// Unknown email login runs dummy Argon2id (non-trivial CPU time > 5ms, same error as wrong password).
	start := time.Now()
	_, errNoUser := srv.Login(ctx, &userv1.LoginRequest{
		Email:    "nonexistent-timing@example.com",
		Password: "WrongPassword123!",
	})
	elapsed := time.Since(start)
	if status.Code(errNoUser) != codes.Unauthenticated {
		t.Fatalf("unknown email login: want Unauthenticated, got %v", errNoUser)
	}
	if elapsed < 5*time.Millisecond {
		t.Fatalf("unknown email login returned too fast (%s); dummy Argon2id hash likely skipped", elapsed)
	}
}

func TestW2A_PRMB14_ResendOTPSetsIsResendFlag(t *testing.T) {
	srv, _, _, pub, _ := newTestHarness(t)
	ctx := context.Background()

	if _, err := srv.Register(ctx, &userv1.RegisterRequest{
		FullName: "Resend Flag User",
		Email:    "w2a-resendflag@example.com",
		Phone:    "0901000019",
		Password: "Password123!",
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if len(pub.userRegistered) != 1 || pub.userRegistered[0].IsResend {
		t.Fatalf("Register event should have IsResend=false, got %+v", pub.userRegistered)
	}

	if _, err := srv.ResendOTP(ctx, &userv1.ResendOTPRequest{
		Email: "w2a-resendflag@example.com",
	}); err != nil {
		t.Fatalf("ResendOTP: %v", err)
	}
	if len(pub.userRegistered) != 2 || !pub.userRegistered[1].IsResend {
		t.Fatalf("ResendOTP event should have IsResend=true, got %+v", pub.userRegistered)
	}
}

func TestW2A_UCB11_PasswordResetLifecycleAndFakeClock(t *testing.T) {
	var mu sync.Mutex
	curTime := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	nowFn := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return curTime
	}
	advance := func(d time.Duration) {
		mu.Lock()
		curTime = curTime.Add(d)
		mu.Unlock()
	}

	store := newMemStore()
	sec := newMemSecurity()
	sec.nowFn = nowFn
	pub := &memPublisher{}
	hasher := password.NewDefaultHasher()
	authMgr, err := auth.NewManager("test-secret-key-hs256", 15*time.Minute)
	if err != nil {
		t.Fatalf("new auth manager: %v", err)
	}
	log := logger.New("user-test", logger.WithWriter(io.Discard))
	srv := NewServer(store, sec, hasher, authMgr, pub, log, Options{Now: nowFn})
	ctx := context.Background()

	userID := registerActiveUser(t, srv, pub, "w2a-reset@example.com", "0901000020")
	// Also test pending user gets silent Sent=true without emitting event.
	_, _ = srv.Register(ctx, &userv1.RegisterRequest{
		FullName: "Pending Reset",
		Email:    "w2a-reset-pending@example.com",
		Phone:    "0901000021",
		Password: "Password123!",
	})

	// Unknown and pending emails return Sent=true without emitting events; 4th request hits 3/hour rate limit identically.
	for i := 0; i < 3; i++ {
		resp, err := srv.ForgotPassword(ctx, &userv1.ForgotPasswordRequest{Email: "nobody@example.com"})
		if err != nil || !resp.GetSent() {
			t.Fatalf("ForgotPassword unknown #%d: want Sent=true, got resp=%+v err=%v", i+1, resp, err)
		}
	}
	if _, err := srv.ForgotPassword(ctx, &userv1.ForgotPasswordRequest{Email: "nobody@example.com"}); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("ForgotPassword unknown 4th in 1h: want ResourceExhausted, got %v", err)
	}
	if resp, err := srv.ForgotPassword(ctx, &userv1.ForgotPasswordRequest{Email: "w2a-reset-pending@example.com"}); err != nil || !resp.GetSent() {
		t.Fatalf("ForgotPassword pending: want Sent=true, got resp=%+v err=%v", resp, err)
	}
	if len(pub.passwordResetRequested) != 0 {
		t.Fatalf("unknown/pending ForgotPassword must not emit event, got %d", len(pub.passwordResetRequested))
	}

	// Active user logs in before requesting reset.
	loginBefore, err := srv.Login(ctx, &userv1.LoginRequest{
		Email:    "w2a-reset@example.com",
		Password: "Password123!",
	})
	if err != nil {
		t.Fatalf("Login before reset: %v", err)
	}
	oldTokCtx := ctxWithToken(t, authMgr, loginBefore.GetAccessToken())

	// Request #1 -> emits user.password_reset_requested with 32-byte token (base64url len 43), DB stores hash only.
	if resp, err := srv.ForgotPassword(ctx, &userv1.ForgotPasswordRequest{Email: "w2a-reset@example.com"}); err != nil || !resp.GetSent() {
		t.Fatalf("ForgotPassword #1: resp=%+v err=%v", resp, err)
	}
	if len(pub.passwordResetRequested) != 1 {
		t.Fatalf("expected 1 password_reset_requested event, got %d", len(pub.passwordResetRequested))
	}
	evt1 := pub.passwordResetRequested[0]
	if evt1.UserID != userID || evt1.Email != "w2a-reset@example.com" || len(evt1.ResetToken) < 40 {
		t.Fatalf("unexpected reset event payload: %+v", evt1)
	}
	wantExp := nowFn().Add(15 * time.Minute).Format(time.RFC3339)
	if evt1.ExpiresAt != wantExp {
		t.Fatalf("reset ExpiresAt: want %q, got %q", wantExp, evt1.ExpiresAt)
	}
	store.mu.Lock()
	for _, pr := range store.passwordResets {
		if pr.TokenHash == evt1.ResetToken {
			store.mu.Unlock()
			t.Fatalf("password_resets stored plaintext token instead of hash")
		}
		if pr.TokenHash != hashToken(evt1.ResetToken) {
			store.mu.Unlock()
			t.Fatalf("password_resets token_hash mismatch")
		}
	}
	store.mu.Unlock()

	// Request #2 invalidates token #1!
	if _, err := srv.ForgotPassword(ctx, &userv1.ForgotPasswordRequest{Email: "w2a-reset@example.com"}); err != nil {
		t.Fatalf("ForgotPassword #2: %v", err)
	}
	token2 := pub.passwordResetRequested[1].ResetToken
	if _, err := srv.ResetPassword(ctx, &userv1.ResetPasswordRequest{
		Token:       evt1.ResetToken,
		NewPassword: "NewStrongPass123!",
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("token #1 after request #2: want InvalidArgument, got %v", err)
	}

	// Request #3 succeeds; request #4 within 1 hour fails with ResourceExhausted.
	if _, err := srv.ForgotPassword(ctx, &userv1.ForgotPasswordRequest{Email: "w2a-reset@example.com"}); err != nil {
		t.Fatalf("ForgotPassword #3: %v", err)
	}
	token3 := pub.passwordResetRequested[2].ResetToken
	if _, err := srv.ForgotPassword(ctx, &userv1.ForgotPasswordRequest{Email: "w2a-reset@example.com"}); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("ForgotPassword #4 within 1h: want ResourceExhausted, got %v", err)
	}
	_ = token2

	// Weak password rejected without consuming token #3.
	if _, err := srv.ResetPassword(ctx, &userv1.ResetPasswordRequest{
		Token:       token3,
		NewPassword: "short",
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("ResetPassword weak password: want InvalidArgument, got %v", err)
	}

	// Advance fake clock by 15m + 1s -> token #3 is now expired!
	advance(15*time.Minute + time.Second)
	if _, err := srv.ResetPassword(ctx, &userv1.ResetPasswordRequest{
		Token:       token3,
		NewPassword: "NewStrongPass123!",
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("ResetPassword expired token: want InvalidArgument, got %v", err)
	}

	// Advance past 1-hour rate limit window -> fresh ForgotPassword succeeds.
	advance(50 * time.Minute)
	if _, err := srv.ForgotPassword(ctx, &userv1.ForgotPasswordRequest{Email: "w2a-reset@example.com"}); err != nil {
		t.Fatalf("ForgotPassword after 1h window: %v", err)
	}
	freshToken := pub.passwordResetRequested[len(pub.passwordResetRequested)-1].ResetToken

	// Redis failure during ResetPassword rolls back DB cleanly (password unchanged, token unused).
	sec.mu.Lock()
	sec.setRevokedBeforeErr = errors.New("redis down")
	sec.mu.Unlock()
	if _, err := srv.ResetPassword(ctx, &userv1.ResetPasswordRequest{
		Token:       freshToken,
		NewPassword: "NewStrongPass123!",
	}); status.Code(err) != codes.Unavailable {
		t.Fatalf("ResetPassword during Redis outage: want Unavailable, got %v", err)
	}
	sec.mu.Lock()
	sec.setRevokedBeforeErr = nil
	sec.mu.Unlock()

	// Now advance fake clock to real wall-clock time so revoked_before >= old token's iat.
	mu.Lock()
	curTime = time.Now().UTC()
	mu.Unlock()
	// Make sure freshToken isn't expired relative to curTime (refresh if needed).
	store.mu.Lock()
	for id, pr := range store.passwordResets {
		if pr.TokenHash == hashToken(freshToken) {
			pr.ExpiresAt = curTime.Add(15 * time.Minute)
			store.passwordResets[id] = pr
		}
	}
	store.mu.Unlock()

	// Complete ResetPassword -> succeeds.
	resetResp, err := srv.ResetPassword(ctx, &userv1.ResetPasswordRequest{
		Token:       freshToken,
		NewPassword: "NewStrongPass123!",
	})
	if err != nil || !resetResp.GetOk() {
		t.Fatalf("ResetPassword valid: resp=%+v err=%v", resetResp, err)
	}

	// Reusing freshToken a second time fails (single-use).
	if _, err := srv.ResetPassword(ctx, &userv1.ResetPasswordRequest{
		Token:       freshToken,
		NewPassword: "AnotherPassword123!",
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("ResetPassword replay: want InvalidArgument, got %v", err)
	}

	// Old access token and old refresh token are immediately revoked!
	if _, err := srv.GetProfile(oldTokCtx, &userv1.GetProfileRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("old access token after password reset: want Unauthenticated, got %v", err)
	}
	if _, err := srv.RefreshToken(ctx, &userv1.RefreshTokenRequest{RefreshToken: loginBefore.GetRefreshToken()}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("old refresh token after password reset: want Unauthenticated, got %v", err)
	}

	// Old password fails; new password logs in and its fresh access token works!
	if _, err := srv.Login(ctx, &userv1.LoginRequest{
		Email:    "w2a-reset@example.com",
		Password: "Password123!",
	}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("login with old password after reset: want Unauthenticated, got %v", err)
	}
	loginAfter, err := srv.Login(ctx, &userv1.LoginRequest{
		Email:    "w2a-reset@example.com",
		Password: "NewStrongPass123!",
	})
	if err != nil {
		t.Fatalf("login with new password after reset: %v", err)
	}
	newTokCtx := ctxWithToken(t, authMgr, loginAfter.GetAccessToken())
	if _, err := srv.GetProfile(newTokCtx, &userv1.GetProfileRequest{}); err != nil {
		t.Fatalf("GetProfile with post-reset token: %v", err)
	}
}

func TestW2A_UCA03_LastActiveAdminGuards(t *testing.T) {
	srv, store, _, pub, _ := newTestHarness(t)

	// Create super_admin (active), admin2 (active), and admin3 (locked).
	superID := registerActiveUser(t, srv, pub, "w2a-a03-super@example.com", "0901000030")
	adm2ID := registerActiveUser(t, srv, pub, "w2a-a03-adm2@example.com", "0901000031")
	adm3ID := registerActiveUser(t, srv, pub, "w2a-a03-adm3@example.com", "0901000032")

	store.mu.Lock()
	u1 := store.users[superID]
	u1.Role = RoleSuperAdmin
	store.users[superID] = u1
	u2 := store.users[adm2ID]
	u2.Role = RoleAdmin
	store.users[adm2ID] = u2
	u3 := store.users[adm3ID]
	u3.Role = RoleAdmin
	u3.Status = StatusLocked
	store.users[adm3ID] = u3
	store.mu.Unlock()

	superCtx := ctxWithUser(superID, RoleSuperAdmin, "")
	adm2Ctx := ctxWithUser(adm2ID, RoleAdmin, "")

	// Admin cannot lock self.
	if _, err := srv.AdminUpdateUserStatus(adm2Ctx, &userv1.AdminUpdateUserStatusRequest{
		TargetUserId: adm2ID,
		Status:       userv1.UserStatus_USER_STATUS_LOCKED,
		Reason:       "self lock",
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("self lock: want FailedPrecondition, got %v", err)
	}

	// Admin cannot demote self.
	if _, err := srv.AdminUpdateUserRole(adm2Ctx, &userv1.AdminUpdateUserRoleRequest{
		TargetUserId: adm2ID,
		Role:         userv1.UserRole_USER_ROLE_BUYER,
		Reason:       "self demote",
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("self demote: want FailedPrecondition, got %v", err)
	}

	// With 2 active admins (super + adm2), super_admin CAN lock adm2 (leaving 1 active admin).
	if _, err := srv.AdminUpdateUserStatus(superCtx, &userv1.AdminUpdateUserStatusRequest{
		TargetUserId: adm2ID,
		Status:       userv1.UserStatus_USER_STATUS_LOCKED,
		Reason:       "lock second admin",
	}); err != nil {
		t.Fatalf("lock second admin: %v", err)
	}

	// Now only 1 active admin remains (superID); adm2 & adm3 are LOCKED.
	// Even another actor context (simulating a second super_admin session) cannot lock or demote the last active admin!
	otherSuperCtx := ctxWithUser("00000000-0000-0000-0000-000000000099", RoleSuperAdmin, "")
	if _, err := srv.AdminUpdateUserStatus(otherSuperCtx, &userv1.AdminUpdateUserStatusRequest{
		TargetUserId: superID,
		Status:       userv1.UserStatus_USER_STATUS_LOCKED,
		Reason:       "lock last active admin",
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("lock last active admin: want FailedPrecondition, got %v", err)
	}
	if _, err := srv.AdminUpdateUserRole(otherSuperCtx, &userv1.AdminUpdateUserRoleRequest{
		TargetUserId: superID,
		Role:         userv1.UserRole_USER_ROLE_BUYER,
		Reason:       "demote last active admin",
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("demote last active admin: want FailedPrecondition, got %v", err)
	}
}

func TestW2A_Race_LoginVsLockResetAndDemote(t *testing.T) {
	srv, store, sec, pub, authMgr := newTestHarness(t)
	ctx := context.Background()

	superID := registerActiveUser(t, srv, pub, "w2a-race-super@example.com", "0901000040")
	store.mu.Lock()
	sup := store.users[superID]
	sup.Role = RoleSuperAdmin
	store.users[superID] = sup
	store.mu.Unlock()
	superCtx := ctxWithUser(superID, RoleSuperAdmin, "")

	// Case 1: Admin locks user while Login is in waitPastRevokedBefore (after Verify succeeded).
	buyer1ID := registerActiveUser(t, srv, pub, "w2a-race-lock@example.com", "0901000041")
	sec.mu.Lock()
	sec.onGetRevokedBefore = func(uid string) {
		if uid != buyer1ID {
			return
		}
		sec.mu.Lock()
		sec.onGetRevokedBefore = nil
		sec.mu.Unlock()
		if _, err := srv.AdminUpdateUserStatus(superCtx, &userv1.AdminUpdateUserStatusRequest{
			TargetUserId: buyer1ID,
			Status:       userv1.UserStatus_USER_STATUS_LOCKED,
			Reason:       "khoa giua luc login",
		}); err != nil {
			t.Errorf("hook lock failed: %v", err)
		}
	}
	sec.mu.Unlock()

	_, err := srv.Login(ctx, &userv1.LoginRequest{
		Email:    "w2a-race-lock@example.com",
		Password: "Password123!",
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("Login racing with AdminUpdateUserStatus(LOCKED): want PermissionDenied (403), got %v", err)
	}
	if !strings.Contains(status.Convert(err).Message(), "khoa giua luc login") {
		t.Fatalf("Login racing with lock missing lock_reason in 403 message: %q", status.Convert(err).Message())
	}

	// Case 2: User resets password while Login is in waitPastRevokedBefore (after old password Verify succeeded).
	buyer2ID := registerActiveUser(t, srv, pub, "w2a-race-reset@example.com", "0901000042")
	if _, err := srv.ForgotPassword(ctx, &userv1.ForgotPasswordRequest{Email: "w2a-race-reset@example.com"}); err != nil {
		t.Fatalf("ForgotPassword: %v", err)
	}
	resetTok := pub.passwordResetRequested[len(pub.passwordResetRequested)-1].ResetToken

	sec.mu.Lock()
	sec.onGetRevokedBefore = func(uid string) {
		if uid != buyer2ID {
			return
		}
		sec.mu.Lock()
		sec.onGetRevokedBefore = nil
		sec.mu.Unlock()
		if _, err := srv.ResetPassword(ctx, &userv1.ResetPasswordRequest{
			Token:       resetTok,
			NewPassword: "ChangedDuringLogin123!",
		}); err != nil {
			t.Errorf("hook ResetPassword failed: %v", err)
		}
	}
	sec.mu.Unlock()

	if _, err := srv.Login(ctx, &userv1.LoginRequest{
		Email:    "w2a-race-reset@example.com",
		Password: "Password123!",
	}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("Login racing with ResetPassword: want Unauthenticated (401), got %v", err)
	}

	// Case 3: Admin demotes admin -> buyer while Login is in waitPastRevokedBefore.
	// Login must NOT mint an access token carrying the stale admin role!
	admID := registerActiveUser(t, srv, pub, "w2a-race-demote@example.com", "0901000043")
	store.mu.Lock()
	a := store.users[admID]
	a.Role = RoleAdmin
	store.users[admID] = a
	store.mu.Unlock()

	sec.mu.Lock()
	sec.onGetRevokedBefore = func(uid string) {
		if uid != admID {
			return
		}
		sec.mu.Lock()
		sec.onGetRevokedBefore = nil
		sec.mu.Unlock()
		if _, err := srv.AdminUpdateUserRole(superCtx, &userv1.AdminUpdateUserRoleRequest{
			TargetUserId: admID,
			Role:         userv1.UserRole_USER_ROLE_BUYER,
			Reason:       "ha quyen giua luc login",
		}); err != nil {
			t.Errorf("hook demote failed: %v", err)
		}
	}
	sec.mu.Unlock()

	loginDemoted, err := srv.Login(ctx, &userv1.LoginRequest{
		Email:    "w2a-race-demote@example.com",
		Password: "Password123!",
	})
	if err != nil {
		t.Fatalf("Login racing with demote: %v", err)
	}
	claims, err := authMgr.VerifyAccessToken(loginDemoted.GetAccessToken())
	if err != nil {
		t.Fatalf("VerifyAccessToken: %v", err)
	}
	if claims.Role != RoleBuyer {
		t.Fatalf("Login racing with demote minted stale role %q; want %q", claims.Role, RoleBuyer)
	}
}

func TestW2A_Race_RefreshTokenVsLockAndReset(t *testing.T) {
	srv, store, sec, pub, _ := newTestHarness(t)
	ctx := context.Background()

	superID := registerActiveUser(t, srv, pub, "w2a-rf-super@example.com", "0901000044")
	store.mu.Lock()
	sup := store.users[superID]
	sup.Role = RoleSuperAdmin
	store.users[superID] = sup
	store.mu.Unlock()
	superCtx := ctxWithUser(superID, RoleSuperAdmin, "")

	buyerID := registerActiveUser(t, srv, pub, "w2a-rf-buyer@example.com", "0901000045")
	loginResp, err := srv.Login(ctx, &userv1.LoginRequest{
		Email:    "w2a-rf-buyer@example.com",
		Password: "Password123!",
	})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	// Hook triggers admin lock AFTER RefreshToken pre-check read active token, BEFORE RefreshToken opens tx.
	sec.mu.Lock()
	sec.onGetRevokedBefore = func(uid string) {
		if uid != buyerID {
			return
		}
		sec.mu.Lock()
		sec.onGetRevokedBefore = nil
		sec.mu.Unlock()
		if _, err := srv.AdminUpdateUserStatus(superCtx, &userv1.AdminUpdateUserStatusRequest{
			TargetUserId: buyerID,
			Status:       userv1.UserStatus_USER_STATUS_LOCKED,
			Reason:       "lock during refresh",
		}); err != nil {
			t.Errorf("hook lock failed: %v", err)
		}
	}
	sec.mu.Unlock()

	if _, err := srv.RefreshToken(ctx, &userv1.RefreshTokenRequest{
		RefreshToken: loginResp.GetRefreshToken(),
	}); err == nil {
		t.Fatalf("RefreshToken racing with Lock succeeded; want Unauthenticated or PermissionDenied")
	}

	// Verify ZERO unrevoked refresh tokens remain for buyerID.
	store.mu.Lock()
	for id, rt := range store.refreshTokens {
		if rt.UserID == buyerID && rt.RevokedAt == nil {
			store.mu.Unlock()
			t.Fatalf("unrevoked refresh token %s survived lock race", id)
		}
	}
	store.mu.Unlock()
}

func TestW2A_ResetPassword_RevokedBeforeTimestampTakenAfterUserUpdate(t *testing.T) {
	var mu sync.Mutex
	curTime := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	nowFn := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return curTime
	}

	store := newMemStore()
	sec := newMemSecurity()
	sec.nowFn = nowFn
	pub := &memPublisher{}
	hasher := password.NewDefaultHasher()
	authMgr, err := auth.NewManager("test-secret-key-hs256", 15*time.Minute)
	if err != nil {
		t.Fatalf("new auth manager: %v", err)
	}
	log := logger.New("user-test", logger.WithWriter(io.Discard))
	srv := NewServer(store, sec, hasher, authMgr, pub, log, Options{Now: nowFn})
	ctx := context.Background()

	userID := registerActiveUser(t, srv, pub, "w2a-ts-reset@example.com", "0901000046")
	if _, err := srv.ForgotPassword(ctx, &userv1.ForgotPasswordRequest{Email: "w2a-ts-reset@example.com"}); err != nil {
		t.Fatalf("ForgotPassword: %v", err)
	}
	resetTok := pub.passwordResetRequested[len(pub.passwordResetRequested)-1].ResetToken

	// Simulate Argon2id + waiting on users row lock advancing clock by +5s right at UpdateUserPassword.
	initialUnix := curTime.Unix()
	store.mu.Lock()
	store.onUpdateUserPassword = func(uid string) {
		if uid == userID {
			mu.Lock()
			curTime = curTime.Add(5 * time.Second)
			mu.Unlock()
		}
	}
	store.mu.Unlock()

	if _, err := srv.ResetPassword(ctx, &userv1.ResetPasswordRequest{
		Token:       resetTok,
		NewPassword: "PostLockPass123!",
	}); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}

	sec.mu.Lock()
	gotRevokedBefore := sec.revokedBefore[userID]
	sec.mu.Unlock()
	wantUnix := initialUnix + 5
	if gotRevokedBefore != wantUnix {
		t.Fatalf("SetRevokedBefore timestamp taken before UpdateUserPassword: got %d, want %d (initial=%d)",
			gotRevokedBefore, wantUnix, initialUnix)
	}
}

func TestW2A_ForgotPassword_PublishErrorReturns200AndInvalidatesRecord(t *testing.T) {
	store := newMemStore()
	sec := newMemSecurity()
	pub := &memPublisher{}
	hasher := password.NewDefaultHasher()
	authMgr, err := auth.NewManager("test-secret-key-hs256", 15*time.Minute)
	if err != nil {
		t.Fatalf("new auth manager: %v", err)
	}
	var logBuf bytes.Buffer
	log := logger.New("user-test", logger.WithWriter(&logBuf))
	srv := NewServer(store, sec, hasher, authMgr, pub, log, Options{})
	ctx := context.Background()

	userID := registerActiveUser(t, srv, pub, "w2a-puberr@example.com", "0901000047")

	// Inject NATS publish error: ForgotPassword MUST still return Sent=true (200) to avoid email enumeration,
	// invalidate the persisted password_resets record, and log the error without leaking the reset token.
	pub.mu.Lock()
	pub.publishPasswordResetErr = errors.New("nats stream unavailable")
	pub.mu.Unlock()

	resp, err := srv.ForgotPassword(ctx, &userv1.ForgotPasswordRequest{Email: "w2a-puberr@example.com"})
	if err != nil || !resp.GetSent() {
		t.Fatalf("ForgotPassword on NATS failure: want Sent=true err=nil (anti-enumeration), got resp=%+v err=%v", resp, err)
	}

	// Every password_resets row created for this user must be invalidated (UsedAt != nil).
	store.mu.Lock()
	foundRow := false
	for _, pr := range store.passwordResets {
		if pr.UserID == userID {
			foundRow = true
			if pr.UsedAt == nil {
				store.mu.Unlock()
				t.Fatalf("password_resets row %s left active after publish failure", pr.ID)
			}
		}
	}
	store.mu.Unlock()
	if !foundRow {
		t.Fatalf("expected password_resets row to have been created and invalidated")
	}

	// Error logged, no raw token logged.
	logged := logBuf.String()
	if !strings.Contains(logged, "nats stream unavailable") {
		t.Fatalf("expected publish error logged, got logs: %s", logged)
	}
}

func TestW2A_Race_ConcurrentLoginRefreshVsLockAndReset(t *testing.T) {
	srv, store, _, pub, authMgr := newTestHarness(t)
	ctx := context.Background()

	superID := registerActiveUser(t, srv, pub, "w2a-conc-super@example.com", "0901000050")
	store.mu.Lock()
	sup := store.users[superID]
	sup.Role = RoleSuperAdmin
	store.users[superID] = sup
	store.mu.Unlock()
	superCtx := ctxWithUser(superID, RoleSuperAdmin, "")

	// Part A: Hammer Login + RefreshToken concurrently with AdminUpdateUserStatus(LOCKED).
	lockTargetID := registerActiveUser(t, srv, pub, "w2a-conc-lock@example.com", "0901000051")
	initLogin, err := srv.Login(ctx, &userv1.LoginRequest{
		Email:    "w2a-conc-lock@example.com",
		Password: "Password123!",
	})
	if err != nil {
		t.Fatalf("initial Login (lock): %v", err)
	}

	var (
		tokMu  sync.Mutex
		allATs = []string{initLogin.GetAccessToken()}
		allRTs = []string{initLogin.GetRefreshToken()}
		wg     sync.WaitGroup
		doneCh = make(chan struct{})
	)
	recordToks := func(at, rt string) {
		tokMu.Lock()
		if at != "" {
			allATs = append(allATs, at)
		}
		if rt != "" {
			allRTs = append(allRTs, rt)
		}
		tokMu.Unlock()
	}

	for g := 0; g < 3; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-doneCh:
					return
				default:
				}
				resp, lErr := srv.Login(ctx, &userv1.LoginRequest{
					Email:    "w2a-conc-lock@example.com",
					Password: "Password123!",
				})
				if lErr != nil {
					return
				}
				recordToks(resp.GetAccessToken(), resp.GetRefreshToken())
				if rfResp, rErr := srv.RefreshToken(ctx, &userv1.RefreshTokenRequest{
					RefreshToken: resp.GetRefreshToken(),
				}); rErr == nil {
					recordToks(rfResp.GetAccessToken(), rfResp.GetRefreshToken())
				}
			}
		}()
	}

	time.Sleep(150 * time.Millisecond)
	if _, err := srv.AdminUpdateUserStatus(superCtx, &userv1.AdminUpdateUserStatusRequest{
		TargetUserId: lockTargetID,
		Status:       userv1.UserStatus_USER_STATUS_LOCKED,
		Reason:       "concurrent lock race test",
	}); err != nil {
		t.Fatalf("AdminUpdateUserStatus LOCKED: %v", err)
	}
	close(doneCh)
	wg.Wait()

	for i, at := range allATs {
		if _, err := srv.GetProfile(ctxWithToken(t, authMgr, at), &userv1.GetProfileRequest{}); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("access token #%d survived concurrent lock: err=%v", i, err)
		}
	}
	for i, rt := range allRTs {
		if _, err := srv.RefreshToken(ctx, &userv1.RefreshTokenRequest{RefreshToken: rt}); err == nil {
			t.Fatalf("refresh token #%d survived concurrent lock", i)
		}
	}

	// Part B: Hammer Login (old password) + RefreshToken concurrently with ResetPassword.
	resetEmail := "w2a-conc-reset@example.com"
	_ = registerActiveUser(t, srv, pub, resetEmail, "0901000052")
	if _, err := srv.ForgotPassword(ctx, &userv1.ForgotPasswordRequest{Email: resetEmail}); err != nil {
		t.Fatalf("ForgotPassword: %v", err)
	}
	resetToken := pub.passwordResetRequested[len(pub.passwordResetRequested)-1].ResetToken

	initResetLogin, err := srv.Login(ctx, &userv1.LoginRequest{Email: resetEmail, Password: "Password123!"})
	if err != nil {
		t.Fatalf("initial Login (reset): %v", err)
	}
	allATs = []string{initResetLogin.GetAccessToken()}
	allRTs = []string{initResetLogin.GetRefreshToken()}
	doneCh = make(chan struct{})

	for g := 0; g < 3; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-doneCh:
					return
				default:
				}
				resp, lErr := srv.Login(ctx, &userv1.LoginRequest{
					Email:    resetEmail,
					Password: "Password123!",
				})
				if lErr != nil {
					return
				}
				recordToks(resp.GetAccessToken(), resp.GetRefreshToken())
				if rfResp, rErr := srv.RefreshToken(ctx, &userv1.RefreshTokenRequest{
					RefreshToken: resp.GetRefreshToken(),
				}); rErr == nil {
					recordToks(rfResp.GetAccessToken(), rfResp.GetRefreshToken())
				}
			}
		}()
	}

	time.Sleep(150 * time.Millisecond)
	if _, err := srv.ResetPassword(ctx, &userv1.ResetPasswordRequest{
		Token:       resetToken,
		NewPassword: "BrandNewPassword123!",
	}); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}
	close(doneCh)
	wg.Wait()

	for i, at := range allATs {
		if _, err := srv.GetProfile(ctxWithToken(t, authMgr, at), &userv1.GetProfileRequest{}); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("old-password access token #%d survived concurrent ResetPassword: err=%v", i, err)
		}
	}
	for i, rt := range allRTs {
		if _, err := srv.RefreshToken(ctx, &userv1.RefreshTokenRequest{RefreshToken: rt}); err == nil {
			t.Fatalf("old-password refresh token #%d survived concurrent ResetPassword", i)
		}
	}
}

func TestW2A_Review1_14_ShopIDOnlyForSellerInTokensAndValidateUser(t *testing.T) {
	srv, store, _, pub, authMgr := newTestHarness(t)
	ctx := context.Background()

	superID := registerActiveUser(t, srv, pub, "w2a-rev1-super@example.com", "0901000060")
	store.mu.Lock()
	sup := store.users[superID]
	sup.Role = RoleSuperAdmin
	store.users[superID] = sup
	store.mu.Unlock()
	superCtx := ctxWithUser(superID, RoleSuperAdmin, "")

	// Register buyer, add pickup address, register & approve shop -> role=seller, shop_id=S.
	sellerID := registerActiveUser(t, srv, pub, "w2a-rev1-seller@example.com", "0901000061")
	sellerCtx := ctxWithUser(sellerID, RoleBuyer, "")
	addrResp, err := srv.CreateAddress(sellerCtx, &userv1.CreateAddressRequest{
		RecipientName:  "Kho Review 1",
		RecipientPhone: "0901000061",
		ProvinceCode:   "79",
		WardCode:       "79001",
		StreetAddress:  "123 Le Loi",
		IsDefault:      true,
	})
	if err != nil {
		t.Fatalf("CreateAddress: %v", err)
	}
	shopResp, err := srv.RegisterShop(sellerCtx, &userv1.RegisterShopRequest{
		Name:               "Shop Review 1",
		PickupAddressId:    addrResp.GetAddress().GetId(),
		ContactPhone:       "0901000061",
		BusinessLicenseDoc: "GPKD-REV1.pdf",
	})
	if err != nil {
		t.Fatalf("RegisterShop: %v", err)
	}
	shopID := shopResp.GetShop().GetId()
	if _, err := srv.AdminReviewShop(superCtx, &userv1.AdminReviewShopRequest{
		ShopId:   shopID,
		Decision: userv1.ReviewDecision_REVIEW_DECISION_APPROVE,
	}); err != nil {
		t.Fatalf("AdminReviewShop: %v", err)
	}

	assertEffectiveShopID := func(stage, wantRole, wantShopID string) {
		t.Helper()
		loginResp, lErr := srv.Login(ctx, &userv1.LoginRequest{
			Email:    "w2a-rev1-seller@example.com",
			Password: "Password123!",
		})
		if lErr != nil {
			t.Fatalf("%s Login: %v", stage, lErr)
		}
		loginClaims, cErr := authMgr.VerifyAccessToken(loginResp.GetAccessToken())
		if cErr != nil {
			t.Fatalf("%s VerifyAccessToken(login): %v", stage, cErr)
		}
		if loginClaims.Role != wantRole || loginClaims.ShopID != wantShopID {
			t.Fatalf("%s Login JWT: want role=%q shop_id=%q, got role=%q shop_id=%q",
				stage, wantRole, wantShopID, loginClaims.Role, loginClaims.ShopID)
		}

		rfResp, rErr := srv.RefreshToken(ctx, &userv1.RefreshTokenRequest{
			RefreshToken: loginResp.GetRefreshToken(),
		})
		if rErr != nil {
			t.Fatalf("%s RefreshToken: %v", stage, rErr)
		}
		rfClaims, rcErr := authMgr.VerifyAccessToken(rfResp.GetAccessToken())
		if rcErr != nil {
			t.Fatalf("%s VerifyAccessToken(refresh): %v", stage, rcErr)
		}
		if rfClaims.Role != wantRole || rfClaims.ShopID != wantShopID {
			t.Fatalf("%s RefreshToken JWT: want role=%q shop_id=%q, got role=%q shop_id=%q",
				stage, wantRole, wantShopID, rfClaims.Role, rfClaims.ShopID)
		}

		valResp, vErr := srv.ValidateUser(ctxWithToken(t, authMgr, rfResp.GetAccessToken()), &userv1.ValidateUserRequest{
			UserId: sellerID,
		})
		if vErr != nil {
			t.Fatalf("%s ValidateUser: %v", stage, vErr)
		}
		if valResp.GetShopId() != wantShopID {
			t.Fatalf("%s ValidateUser shop_id: want %q, got %q", stage, wantShopID, valResp.GetShopId())
		}
	}

	// 1. Active seller carries shop_id.
	assertEffectiveShopID("initial seller", RoleSeller, shopID)

	// 2. Demote seller -> buyer: Login, RefreshToken, ValidateUser MUST emit empty shop_id.
	if _, err := srv.AdminUpdateUserRole(superCtx, &userv1.AdminUpdateUserRoleRequest{
		TargetUserId: sellerID,
		Role:         userv1.UserRole_USER_ROLE_BUYER,
		Reason:       "demote seller to buyer",
	}); err != nil {
		t.Fatalf("demote to buyer: %v", err)
	}
	assertEffectiveShopID("demoted buyer", RoleBuyer, "")

	// 3. Promote buyer -> admin: still not seller -> empty shop_id.
	if _, err := srv.AdminUpdateUserRole(superCtx, &userv1.AdminUpdateUserRoleRequest{
		TargetUserId: sellerID,
		Role:         userv1.UserRole_USER_ROLE_ADMIN,
		Reason:       "promote to admin",
	}); err != nil {
		t.Fatalf("promote to admin: %v", err)
	}
	assertEffectiveShopID("promoted admin", RoleAdmin, "")

	// 4. Restore role -> seller: shop_id returns in Login, RefreshToken, ValidateUser!
	if _, err := srv.AdminUpdateUserRole(superCtx, &userv1.AdminUpdateUserRoleRequest{
		TargetUserId: sellerID,
		Role:         userv1.UserRole_USER_ROLE_SELLER,
		Reason:       "restore seller role",
	}); err != nil {
		t.Fatalf("restore seller: %v", err)
	}
	assertEffectiveShopID("restored seller", RoleSeller, shopID)
}

func TestW2A_Review4_ResetPasswordInvalidatesAllOpenTokens(t *testing.T) {
	srv, store, _, pub, _ := newTestHarness(t)
	ctx := context.Background()

	userID := registerActiveUser(t, srv, pub, "w2a-rev4-reset@example.com", "0901000062")

	// Simulate two concurrent ForgotPassword calls inserting two active reset tokens A and B.
	tokenA, hashA, err := generateOpaqueToken()
	if err != nil {
		t.Fatalf("generateOpaqueToken A: %v", err)
	}
	tokenB, hashB, err := generateOpaqueToken()
	if err != nil {
		t.Fatalf("generateOpaqueToken B: %v", err)
	}
	exp := time.Now().UTC().Add(15 * time.Minute)
	if _, err := store.CreatePasswordReset(ctx, db.CreatePasswordResetParams{
		ID:        uuid.NewString(),
		UserID:    userID,
		TokenHash: hashA,
		ExpiresAt: exp,
	}); err != nil {
		t.Fatalf("CreatePasswordReset A: %v", err)
	}
	if _, err := store.CreatePasswordReset(ctx, db.CreatePasswordResetParams{
		ID:        uuid.NewString(),
		UserID:    userID,
		TokenHash: hashB,
		ExpiresAt: exp,
	}); err != nil {
		t.Fatalf("CreatePasswordReset B: %v", err)
	}

	// Using token A succeeds and MUST invalidate sibling token B in the same transaction.
	if resp, err := srv.ResetPassword(ctx, &userv1.ResetPasswordRequest{
		Token:       tokenA,
		NewPassword: "FirstResetPass123!",
	}); err != nil || !resp.GetOk() {
		t.Fatalf("ResetPassword(tokenA): resp=%+v err=%v", resp, err)
	}

	if _, err := srv.ResetPassword(ctx, &userv1.ResetPasswordRequest{
		Token:       tokenB,
		NewPassword: "SecondResetPass123!",
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("sibling tokenB after ResetPassword(tokenA): want InvalidArgument, got %v", err)
	}
}

func TestW2A_Review5_VerifyOTPBruteForceIdenticalResponse(t *testing.T) {
	srv, _, _, pub, _ := newTestHarness(t)
	ctx := context.Background()

	_ = registerActiveUser(t, srv, pub, "w2a-rev5-active@example.com", "0901000063")
	if _, err := srv.Register(ctx, &userv1.RegisterRequest{
		FullName: "Pending Brute",
		Email:    "w2a-rev5-pending@example.com",
		Phone:    "0901000064",
		Password: "Password123!",
	}); err != nil {
		t.Fatalf("Register pending: %v", err)
	}
	realOTP := pub.userRegistered[len(pub.userRegistered)-1].OTP
	wrongOTP := "000000"
	if realOTP == wrongOTP {
		wrongOTP = "999999"
	}

	// 5 wrong attempts on unknown, ACTIVE, and PENDING emails MUST all return the exact same 400 error.
	for attempt := 1; attempt <= 5; attempt++ {
		_, errUnk := srv.VerifyOTP(ctx, &userv1.VerifyOTPRequest{Email: "w2a-rev5-unknown@example.com", Otp: wrongOTP})
		_, errAct := srv.VerifyOTP(ctx, &userv1.VerifyOTPRequest{Email: "w2a-rev5-active@example.com", Otp: wrongOTP})
		_, errPend := srv.VerifyOTP(ctx, &userv1.VerifyOTPRequest{Email: "w2a-rev5-pending@example.com", Otp: wrongOTP})

		for label, err := range map[string]error{"unknown": errUnk, "active": errAct, "pending": errPend} {
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("attempt %d (%s): want InvalidArgument (400), got %v", attempt, label, err)
			}
		}
		mUnk := status.Convert(errUnk).Message()
		mAct := status.Convert(errAct).Message()
		mPend := status.Convert(errPend).Message()
		if mUnk != mAct || mUnk != mPend {
			t.Fatalf("attempt %d messages differ: unknown=%q active=%q pending=%q", attempt, mUnk, mAct, mPend)
		}
	}

	// Brute-force protection preserved: after 5 wrong attempts, the OTP was deleted so even realOTP MUST fail!
	if _, err := srv.VerifyOTP(ctx, &userv1.VerifyOTPRequest{
		Email: "w2a-rev5-pending@example.com",
		Otp:   realOTP,
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("real OTP after 5 failed attempts: want InvalidArgument (OTP destroyed), got %v", err)
	}
}
