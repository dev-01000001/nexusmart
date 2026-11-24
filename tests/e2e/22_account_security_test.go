package e2e

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type usrW2UpdateStatusRequest struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

type usrW2UpdateRoleRequest struct {
	Role   string `json:"role"`
	Reason string `json:"reason"`
}

type usrW2EmailRequest struct {
	Email string `json:"email"`
}

type usrW2ResetPasswordRequest struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

type usrW2SentResponse struct {
	Sent bool `json:"sent"`
}

type usrW2OKResponse struct {
	OK bool `json:"ok"`
}

type usrW2UserEnvelope struct {
	User struct {
		ID         string `json:"id"`
		Email      string `json:"email"`
		Role       string `json:"role"`
		Status     string `json:"status"`
		LockReason string `json:"lock_reason"`
	} `json:"user"`
}

type usrW2ErrorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

var usrW2ResetTokenRe = regexp.MustCompile(`dat-lai-mat-khau\?token=([A-Za-z0-9_-]+)`)

func usrW2ExtractResetToken(parts ...string) string {
	combined := strings.Join(parts, "\n")
	if m := usrW2ResetTokenRe.FindStringSubmatch(combined); len(m) == 2 {
		return m[1]
	}
	return ""
}

func usrW2DecodeError(t *testing.T, resp *RawResponse) (string, string) {
	t.Helper()
	var env usrW2ErrorEnvelope
	if err := json.Unmarshal(resp.Body, &env); err != nil {
		t.Fatalf("decode error envelope (HTTP %d): %v; body=%s", resp.StatusCode, err, string(resp.Body))
	}
	return env.Error.Code, env.Error.Message
}

type usrW2JWTClaims struct {
	Role   string `json:"role"`
	ShopID string `json:"shop_id"`
}

func usrW2DecodeJWTClaims(t *testing.T, jwtToken string) usrW2JWTClaims {
	t.Helper()
	parts := strings.Split(jwtToken, ".")
	if len(parts) != 3 {
		t.Fatalf("unexpected JWT format (parts=%d)", len(parts))
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode JWT payload: %v", err)
	}
	var claims usrW2JWTClaims
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatalf("unmarshal JWT claims: %v", err)
	}
	return claims
}

func TestUserW2AAccountSecurity(t *testing.T) {
	env := getEnv()
	adminTok := env.adminLogin(t)

	t.Run("LockRevokesTokenAndLoginShowsReason", func(t *testing.T) {
		u := env.registerVerifyAndLogin(t, "usrw2lock")

		// Token works before lock.
		env.mustDoJSON(t, http.MethodGet, "/api/v1/users/me", u.AccessToken, nil, nil, http.StatusOK)

		lockReason := "USR-W2A vi pham dieu khoan bao mat"
		lockPath := fmt.Sprintf("/api/v1/admin/users/%s/status", url.PathEscape(u.UserID))
		lockResp := env.mustDoJSON(t, http.MethodPatch, lockPath, adminTok, nil, usrW2UpdateStatusRequest{
			Status: "LOCKED",
			Reason: lockReason,
		}, http.StatusOK)
		var lockedOut usrW2UserEnvelope
		if err := DecodeJSON(lockResp, &lockedOut); err != nil {
			t.Fatalf("decode lock user: %v", err)
		}
		if !StatusEquals(lockedOut.User.Status, "LOCKED") {
			t.Fatalf("expected status LOCKED, got %q", lockedOut.User.Status)
		}

		// Old access token must be rejected immediately with 401 on user service.
		env.mustDoJSON(t, http.MethodGet, "/api/v1/users/me", u.AccessToken, nil, nil, http.StatusUnauthorized)

		// Old refresh token must be rejected with 401.
		env.mustDoJSON(t, http.MethodPost, "/api/v1/auth/refresh", "", nil, RefreshRequest{
			RefreshToken: u.RefreshToken,
		}, http.StatusUnauthorized)

		// LOCKED + wrong password -> 401 without leaking lock reason.
		wrongResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/auth/login", "", nil, LoginRequest{
			Email:    u.Email,
			Password: "WrongPassword123!",
		}, http.StatusUnauthorized)
		_, wrongMsg := usrW2DecodeError(t, wrongResp)
		if strings.Contains(wrongMsg, lockReason) {
			t.Fatalf("LOCKED + wrong password leaked lock reason: %q", wrongMsg)
		}

		// LOCKED + right password -> 403 with lock reason and support contact.
		rightResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/auth/login", "", nil, LoginRequest{
			Email:    u.Email,
			Password: u.Password,
		}, http.StatusForbidden)
		_, rightMsg := usrW2DecodeError(t, rightResp)
		if !strings.Contains(rightMsg, lockReason) || !strings.Contains(strings.ToLower(rightMsg), "support@nexusmart.vn") {
			t.Fatalf("LOCKED + right password missing reason or support contact: %q", rightMsg)
		}

		// Cross-service immediate revocation check (requires gateway MGET user:revoked_before:<sub_id>).
		env.mustDoJSON(t, http.MethodGet, "/api/v1/cart", u.AccessToken, nil, nil, http.StatusUnauthorized)
	})

	t.Run("DemoteAdminRevokesToken", func(t *testing.T) {
		u := env.registerVerifyAndLogin(t, "usrw2role")
		rolePath := fmt.Sprintf("/api/v1/admin/users/%s/role", url.PathEscape(u.UserID))

		// Promote buyer -> admin: old buyer access + refresh tokens revoked immediately.
		env.mustDoJSON(t, http.MethodPatch, rolePath, adminTok, nil, usrW2UpdateRoleRequest{
			Role:   "admin",
			Reason: "USR-W2A promote admin",
		}, http.StatusOK)
		demoted := false
		t.Cleanup(func() {
			if demoted {
				return
			}
			cleanCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			resp, err := env.client.DoJSON(cleanCtx, http.MethodPatch, rolePath, adminTok, nil, usrW2UpdateRoleRequest{
				Role:   "buyer",
				Reason: "USR-W2A cleanup demote buyer",
			})
			if err != nil {
				t.Errorf("cleanup demote %s back to buyer failed: %v", u.UserID, err)
				return
			}
			if resp.StatusCode != http.StatusOK {
				t.Errorf("cleanup demote %s back to buyer returned HTTP %d: %s", u.UserID, resp.StatusCode, string(resp.Body))
			}
		})
		env.mustDoJSON(t, http.MethodGet, "/api/v1/users/me", u.AccessToken, nil, nil, http.StatusUnauthorized)
		env.mustDoJSON(t, http.MethodPost, "/api/v1/auth/refresh", "", nil, RefreshRequest{
			RefreshToken: u.RefreshToken,
		}, http.StatusUnauthorized)

		// Login again as admin; verify admin endpoint works.
		reLoginResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/auth/login", "", nil, LoginRequest{
			Email:    u.Email,
			Password: u.Password,
		}, http.StatusOK)
		var reLoginOut LoginResponse
		if err := DecodeJSON(reLoginResp, &reLoginOut); err != nil {
			t.Fatalf("decode admin relogin: %v", err)
		}
		env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/users?page_size=1", reLoginOut.AccessToken, nil, nil, http.StatusOK)

		// UC-A03 + F10: Admin cannot lock self -> 409; regular admin cannot update roles at all -> 403 (super_admin only).
		selfStatusPath := fmt.Sprintf("/api/v1/admin/users/%s/status", url.PathEscape(u.UserID))
		env.mustDoJSON(t, http.MethodPatch, selfStatusPath, reLoginOut.AccessToken, nil, usrW2UpdateStatusRequest{
			Status: "LOCKED",
			Reason: "self lock attempt",
		}, http.StatusConflict)
		env.mustDoJSON(t, http.MethodPatch, rolePath, reLoginOut.AccessToken, nil, usrW2UpdateRoleRequest{
			Role:   "buyer",
			Reason: "admin role change forbidden under F10",
		}, http.StatusForbidden)

		// Super admin attempting to self-demote -> 409 Conflict (cannot demote self / last active super_admin).
		meResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/users/me", adminTok, nil, nil, http.StatusOK)
		var superMe usrW2UserEnvelope
		if err := DecodeJSON(meResp, &superMe); err != nil || superMe.User.ID == "" {
			t.Fatalf("decode super_admin /users/me: err=%v body=%s", err, string(meResp.Body))
		}
		superRolePath := fmt.Sprintf("/api/v1/admin/users/%s/role", url.PathEscape(superMe.User.ID))
		env.mustDoJSON(t, http.MethodPatch, superRolePath, adminTok, nil, usrW2UpdateRoleRequest{
			Role:   "buyer",
			Reason: "super_admin self demote attempt",
		}, http.StatusConflict)

		// Super admin demotes u back to buyer -> old admin access + refresh tokens immediately 401.
		env.mustDoJSON(t, http.MethodPatch, rolePath, adminTok, nil, usrW2UpdateRoleRequest{
			Role:   "buyer",
			Reason: "USR-W2A demote buyer",
		}, http.StatusOK)
		demoted = true
		env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/users?page_size=1", reLoginOut.AccessToken, nil, nil, http.StatusUnauthorized)
		env.mustDoJSON(t, http.MethodGet, "/api/v1/users/me", reLoginOut.AccessToken, nil, nil, http.StatusUnauthorized)
		env.mustDoJSON(t, http.MethodPost, "/api/v1/auth/refresh", "", nil, RefreshRequest{
			RefreshToken: reLoginOut.RefreshToken,
		}, http.StatusUnauthorized)
	})

	t.Run("AntiEnumeration", func(t *testing.T) {
		activeUser := env.registerVerifyAndLogin(t, "usrw2enumact")
		pendingEmail := env.uniqueEmail("usrw2enumpend")
		env.mustDoJSON(t, http.MethodPost, "/api/v1/auth/register", "", nil, RegisterRequest{
			FullName: "Pending Enum",
			Email:    pendingEmail,
			Phone:    env.uniquePhone(),
			Password: "NexusPass123!",
		}, http.StatusCreated, http.StatusOK)
		unknownEmail := env.uniqueEmail("usrw2enumunk")

		// resend-otp: unknown email and active email return identical 200 {"sent":true}.
		rUnk := env.mustDoJSON(t, http.MethodPost, "/api/v1/auth/resend-otp", "", nil, usrW2EmailRequest{Email: unknownEmail}, http.StatusOK)
		rAct := env.mustDoJSON(t, http.MethodPost, "/api/v1/auth/resend-otp", "", nil, usrW2EmailRequest{Email: activeUser.Email}, http.StatusOK)
		var sUnk, sAct usrW2SentResponse
		if err := DecodeJSON(rUnk, &sUnk); err != nil || !sUnk.Sent {
			t.Fatalf("resend-otp unknown: err=%v sent=%v", err, sUnk.Sent)
		}
		if err := DecodeJSON(rAct, &sAct); err != nil || !sAct.Sent {
			t.Fatalf("resend-otp active: err=%v sent=%v", err, sAct.Sent)
		}
		if strings.TrimSpace(string(rUnk.Body)) != strings.TrimSpace(string(rAct.Body)) {
			t.Fatalf("resend-otp response bodies differ: unknown=%s active=%s", string(rUnk.Body), string(rAct.Body))
		}

		// verify-otp: unknown email, active email, and pending email across 5 wrong attempts MUST return identical 400.
		realPendingOTP, err := env.mailpit.WaitForOTP(env.ctx(t), pendingEmail, env.cfg.PollTimeout)
		if err != nil {
			t.Fatalf("wait OTP for pending enum email: %v", err)
		}
		wrongOTP := "000000"
		if realPendingOTP == wrongOTP {
			wrongOTP = "999999"
		}
		for attempt := 1; attempt <= 5; attempt++ {
			vUnk := env.mustDoJSON(t, http.MethodPost, "/api/v1/auth/verify-otp", "", nil, VerifyOTPRequest{Email: unknownEmail, OTP: wrongOTP}, http.StatusBadRequest)
			vAct := env.mustDoJSON(t, http.MethodPost, "/api/v1/auth/verify-otp", "", nil, VerifyOTPRequest{Email: activeUser.Email, OTP: wrongOTP}, http.StatusBadRequest)
			vWrong := env.mustDoJSON(t, http.MethodPost, "/api/v1/auth/verify-otp", "", nil, VerifyOTPRequest{Email: pendingEmail, OTP: wrongOTP}, http.StatusBadRequest)

			cUnk, mUnk := usrW2DecodeError(t, vUnk)
			cAct, mAct := usrW2DecodeError(t, vAct)
			cWrong, mWrong := usrW2DecodeError(t, vWrong)
			if cUnk != cAct || cUnk != cWrong || mUnk != mAct || mUnk != mWrong {
				t.Fatalf("verify-otp attempt %d errors differ: unknown=(%s,%q) active=(%s,%q) wrong=(%s,%q)",
					attempt, cUnk, mUnk, cAct, mAct, cWrong, mWrong)
			}
		}
		// Brute-force protection preserved: after 5 wrong attempts, even the real OTP is rejected with 400.
		env.mustDoJSON(t, http.MethodPost, "/api/v1/auth/verify-otp", "", nil, VerifyOTPRequest{Email: pendingEmail, OTP: realPendingOTP}, http.StatusBadRequest)

		// forgot-password: unknown email and active email return identical 200 {"sent":true}.
		fUnk := env.mustDoJSON(t, http.MethodPost, "/api/v1/auth/forgot-password", "", nil, usrW2EmailRequest{Email: unknownEmail}, http.StatusOK)
		fAct := env.mustDoJSON(t, http.MethodPost, "/api/v1/auth/forgot-password", "", nil, usrW2EmailRequest{Email: activeUser.Email}, http.StatusOK)
		if strings.TrimSpace(string(fUnk.Body)) != strings.TrimSpace(string(fAct.Body)) {
			t.Fatalf("forgot-password bodies differ: unknown=%s active=%s", string(fUnk.Body), string(fAct.Body))
		}
	})

	t.Run("ForgotAndResetPasswordViaMailpit", func(t *testing.T) {
		u := env.registerVerifyAndLogin(t, "usrw2reset")
		oldAT := u.AccessToken
		oldRT := u.RefreshToken

		fResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/auth/forgot-password", "", nil, usrW2EmailRequest{
			Email: u.Email,
		}, http.StatusOK)
		var fOut usrW2SentResponse
		if err := DecodeJSON(fResp, &fOut); err != nil || !fOut.Sent {
			t.Fatalf("forgot-password: err=%v sent=%v", err, fOut.Sent)
		}

		mailMsg, err := env.mailpit.WaitForEmail(env.ctx(t), u.Email, "dat-lai-mat-khau?token=", env.cfg.PollTimeout)
		if err != nil {
			t.Fatalf("wait password reset email in Mailpit: %v", err)
		}
		resetToken := usrW2ExtractResetToken(mailMsg.Text, mailMsg.HTML)
		if resetToken == "" {
			t.Fatalf("could not extract reset token from password reset email subject=%q", mailMsg.Subject)
		}

		newPassword := "NexusResetPass456!"
		resetResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/auth/reset-password", "", nil, usrW2ResetPasswordRequest{
			Token:       resetToken,
			NewPassword: newPassword,
		}, http.StatusOK)
		var resetOut usrW2OKResponse
		if err := DecodeJSON(resetResp, &resetOut); err != nil || !resetOut.OK {
			t.Fatalf("reset-password: err=%v ok=%v", err, resetOut.OK)
		}

		// Reusing the same reset link a second time must fail with 400.
		env.mustDoJSON(t, http.MethodPost, "/api/v1/auth/reset-password", "", nil, usrW2ResetPasswordRequest{
			Token:       resetToken,
			NewPassword: "AnotherResetPass789!",
		}, http.StatusBadRequest)

		// Old access token and old refresh token must be immediately invalidated.
		env.mustDoJSON(t, http.MethodGet, "/api/v1/users/me", oldAT, nil, nil, http.StatusUnauthorized)
		env.mustDoJSON(t, http.MethodGet, "/api/v1/cart", oldAT, nil, nil, http.StatusUnauthorized)
		env.mustDoJSON(t, http.MethodPost, "/api/v1/auth/refresh", "", nil, RefreshRequest{
			RefreshToken: oldRT,
		}, http.StatusUnauthorized)

		// Old password rejected; new password succeeds and new token works.
		env.mustDoJSON(t, http.MethodPost, "/api/v1/auth/login", "", nil, LoginRequest{
			Email:    u.Email,
			Password: u.Password,
		}, http.StatusUnauthorized)
		newLoginResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/auth/login", "", nil, LoginRequest{
			Email:    u.Email,
			Password: newPassword,
		}, http.StatusOK)
		var newLoginOut LoginResponse
		if err := DecodeJSON(newLoginResp, &newLoginOut); err != nil || newLoginOut.AccessToken == "" {
			t.Fatalf("login with new password: err=%v", err)
		}
		env.mustDoJSON(t, http.MethodGet, "/api/v1/users/me", newLoginOut.AccessToken, nil, nil, http.StatusOK)
	})

	t.Run("ConcurrentLoginRefreshVsLockAndReset", func(t *testing.T) {
		ctx := env.ctx(t)

		// Part A: Continuous Login + Refresh racing against Admin Lock on real PostgreSQL.
		uLock := env.registerVerifyAndLogin(t, "usrw2conclock")
		var (
			muA   sync.Mutex
			atsA  = []string{uLock.AccessToken}
			rtsA  = []string{uLock.RefreshToken}
			stopA atomic.Bool
			wgA   sync.WaitGroup
		)
		recA := func(at, rt string) {
			muA.Lock()
			if at != "" {
				atsA = append(atsA, at)
			}
			if rt != "" {
				rtsA = append(rtsA, rt)
			}
			muA.Unlock()
		}

		for g := 0; g < 3; g++ {
			wgA.Add(1)
			go func() {
				defer wgA.Done()
				for i := 0; i < 6 && !stopA.Load(); i++ {
					lResp, err := env.client.DoJSON(ctx, http.MethodPost, "/api/v1/auth/login", "", nil, LoginRequest{
						Email:    uLock.Email,
						Password: uLock.Password,
					})
					if err != nil || lResp.StatusCode != http.StatusOK {
						return
					}
					var lOut LoginResponse
					if err := DecodeJSON(lResp, &lOut); err != nil {
						return
					}
					recA(lOut.AccessToken, lOut.RefreshToken)

					rResp, err := env.client.DoJSON(ctx, http.MethodPost, "/api/v1/auth/refresh", "", nil, RefreshRequest{
						RefreshToken: lOut.RefreshToken,
					})
					if err != nil || rResp.StatusCode != http.StatusOK {
						return
					}
					var rOut RefreshResponse
					if err := DecodeJSON(rResp, &rOut); err != nil {
						return
					}
					recA(rOut.AccessToken, rOut.RefreshToken)
				}
			}()
		}

		time.Sleep(120 * time.Millisecond)
		lockPath := fmt.Sprintf("/api/v1/admin/users/%s/status", url.PathEscape(uLock.UserID))
		env.mustDoJSON(t, http.MethodPatch, lockPath, adminTok, nil, usrW2UpdateStatusRequest{
			Status: "LOCKED",
			Reason: "USR-W2A concurrent lock race check",
		}, http.StatusOK)
		stopA.Store(true)
		wgA.Wait()

		for i, at := range atsA {
			env.mustDoJSON(t, http.MethodGet, "/api/v1/users/me", at, nil, nil, http.StatusUnauthorized)
			env.mustDoJSON(t, http.MethodGet, "/api/v1/cart", at, nil, nil, http.StatusUnauthorized)
			_ = i
		}
		for _, rt := range rtsA {
			env.mustDoJSON(t, http.MethodPost, "/api/v1/auth/refresh", "", nil, RefreshRequest{
				RefreshToken: rt,
			}, http.StatusUnauthorized)
		}

		// Part B: Continuous Login (old password) + Refresh racing against ResetPassword on real PostgreSQL.
		uReset := env.registerVerifyAndLogin(t, "usrw2concreset")
		env.mustDoJSON(t, http.MethodPost, "/api/v1/auth/forgot-password", "", nil, usrW2EmailRequest{
			Email: uReset.Email,
		}, http.StatusOK)
		mailMsg, err := env.mailpit.WaitForEmail(ctx, uReset.Email, "dat-lai-mat-khau?token=", env.cfg.PollTimeout)
		if err != nil {
			t.Fatalf("wait password reset email: %v", err)
		}
		resetTok := usrW2ExtractResetToken(mailMsg.Text, mailMsg.HTML)
		if resetTok == "" {
			t.Fatalf("failed to extract reset token from Mailpit message")
		}

		var (
			muB   sync.Mutex
			atsB  = []string{uReset.AccessToken}
			rtsB  = []string{uReset.RefreshToken}
			stopB atomic.Bool
			wgB   sync.WaitGroup
		)
		recB := func(at, rt string) {
			muB.Lock()
			if at != "" {
				atsB = append(atsB, at)
			}
			if rt != "" {
				rtsB = append(rtsB, rt)
			}
			muB.Unlock()
		}

		for g := 0; g < 3; g++ {
			wgB.Add(1)
			go func() {
				defer wgB.Done()
				for i := 0; i < 6 && !stopB.Load(); i++ {
					lResp, err := env.client.DoJSON(ctx, http.MethodPost, "/api/v1/auth/login", "", nil, LoginRequest{
						Email:    uReset.Email,
						Password: uReset.Password,
					})
					if err != nil || lResp.StatusCode != http.StatusOK {
						return
					}
					var lOut LoginResponse
					if err := DecodeJSON(lResp, &lOut); err != nil {
						return
					}
					recB(lOut.AccessToken, lOut.RefreshToken)

					rResp, err := env.client.DoJSON(ctx, http.MethodPost, "/api/v1/auth/refresh", "", nil, RefreshRequest{
						RefreshToken: lOut.RefreshToken,
					})
					if err != nil || rResp.StatusCode != http.StatusOK {
						return
					}
					var rOut RefreshResponse
					if err := DecodeJSON(rResp, &rOut); err != nil {
						return
					}
					recB(rOut.AccessToken, rOut.RefreshToken)
				}
			}()
		}

		time.Sleep(120 * time.Millisecond)
		newPass := "ConcResetPass789!"
		env.mustDoJSON(t, http.MethodPost, "/api/v1/auth/reset-password", "", nil, usrW2ResetPasswordRequest{
			Token:       resetTok,
			NewPassword: newPass,
		}, http.StatusOK)
		stopB.Store(true)
		wgB.Wait()

		for _, at := range atsB {
			env.mustDoJSON(t, http.MethodGet, "/api/v1/users/me", at, nil, nil, http.StatusUnauthorized)
			env.mustDoJSON(t, http.MethodGet, "/api/v1/cart", at, nil, nil, http.StatusUnauthorized)
		}
		for _, rt := range rtsB {
			env.mustDoJSON(t, http.MethodPost, "/api/v1/auth/refresh", "", nil, RefreshRequest{
				RefreshToken: rt,
			}, http.StatusUnauthorized)
		}

		// Confirm new password works cleanly after concurrent reset.
		newLoginResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/auth/login", "", nil, LoginRequest{
			Email:    uReset.Email,
			Password: newPass,
		}, http.StatusOK)
		var newLoginOut LoginResponse
		if err := DecodeJSON(newLoginResp, &newLoginOut); err != nil || newLoginOut.AccessToken == "" {
			t.Fatalf("login with new password after concurrent reset: err=%v", err)
		}
		env.mustDoJSON(t, http.MethodGet, "/api/v1/users/me", newLoginOut.AccessToken, nil, nil, http.StatusOK)
	})

	t.Run("SellerDemoteClearsShopIDInJWTAndRestoreRecoversIt", func(t *testing.T) {
		seller := env.createApprovedSeller(t, "usrw2seldem")
		rolePath := fmt.Sprintf("/api/v1/admin/users/%s/role", url.PathEscape(seller.UserID))

		// Active seller JWT carries shop_id.
		initialClaims := usrW2DecodeJWTClaims(t, seller.AccessToken)
		if initialClaims.Role != "seller" || initialClaims.ShopID != seller.ShopID {
			t.Fatalf("initial seller JWT: want role=seller shop_id=%q, got %+v", seller.ShopID, initialClaims)
		}

		// Demote seller -> buyer: fresh login and refresh JWTs must have empty shop_id.
		env.mustDoJSON(t, http.MethodPatch, rolePath, adminTok, nil, usrW2UpdateRoleRequest{
			Role:   "buyer",
			Reason: "USR-W2A demote seller",
		}, http.StatusOK)

		demLoginResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/auth/login", "", nil, LoginRequest{
			Email:    seller.Email,
			Password: seller.Password,
		}, http.StatusOK)
		var demLogin LoginResponse
		if err := DecodeJSON(demLoginResp, &demLogin); err != nil {
			t.Fatalf("decode demoted login: %v", err)
		}
		if c := usrW2DecodeJWTClaims(t, demLogin.AccessToken); c.Role != "buyer" || c.ShopID != "" {
			t.Fatalf("demoted buyer login JWT leaked shop_id: %+v", c)
		}
		demRefResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/auth/refresh", "", nil, RefreshRequest{
			RefreshToken: demLogin.RefreshToken,
		}, http.StatusOK)
		var demRef RefreshResponse
		if err := DecodeJSON(demRefResp, &demRef); err != nil {
			t.Fatalf("decode demoted refresh: %v", err)
		}
		if c := usrW2DecodeJWTClaims(t, demRef.AccessToken); c.Role != "buyer" || c.ShopID != "" {
			t.Fatalf("demoted buyer refresh JWT leaked shop_id: %+v", c)
		}

		// Promote buyer -> seller again: shop_id returns in login and refresh JWTs and /seller/shop works.
		env.mustDoJSON(t, http.MethodPatch, rolePath, adminTok, nil, usrW2UpdateRoleRequest{
			Role:   "seller",
			Reason: "USR-W2A restore seller",
		}, http.StatusOK)

		restLoginResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/auth/login", "", nil, LoginRequest{
			Email:    seller.Email,
			Password: seller.Password,
		}, http.StatusOK)
		var restLogin LoginResponse
		if err := DecodeJSON(restLoginResp, &restLogin); err != nil {
			t.Fatalf("decode restored login: %v", err)
		}
		if c := usrW2DecodeJWTClaims(t, restLogin.AccessToken); c.Role != "seller" || c.ShopID != seller.ShopID {
			t.Fatalf("restored seller login JWT missing shop_id: want %q, got %+v", seller.ShopID, c)
		}
		env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/shop", restLogin.AccessToken, nil, nil, http.StatusOK)
	})
}
