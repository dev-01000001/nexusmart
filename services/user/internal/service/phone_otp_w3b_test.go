package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/user/internal/events"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestW3B_E19_PhoneOTP_HappyPathMaskingAuditAndNoSecretLeak(t *testing.T) {
	clk := newW2BClock(time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC))
	srv, store, sec, pub, logBuf := newW2BHarness(t, Options{Now: clk.Now})
	sec.nowFn = clk.Now

	userID := registerActiveUser(t, srv, pub, "buyer-phone-ok@example.com", "0901234789")
	userCtx := ctxWithUser(userID, RoleBuyer, "")

	// Default phone from profile when req.Phone is omitted.
	reqResp, err := srv.RequestPhoneOTP(userCtx, &userv1.RequestPhoneOTPRequest{})
	if err != nil {
		t.Fatalf("RequestPhoneOTP: %v", err)
	}
	if !reqResp.GetSent() || reqResp.GetExpiresInSeconds() != 300 || reqResp.GetRetryAfterSeconds() != 60 || reqResp.GetPhoneMasked() != "090****789" {
		t.Fatalf("unexpected RequestPhoneOTPResponse: %+v", reqResp)
	}

	if len(pub.phoneOTPRequested) != 1 {
		t.Fatalf("expected 1 user.phone_otp_requested event, got %d", len(pub.phoneOTPRequested))
	}
	ev := pub.phoneOTPRequested[0]
	if ev.UserID != userID || ev.Email != "buyer-phone-ok@example.com" || ev.PhoneMasked != "090****789" || len(ev.OTP) != 6 {
		t.Fatalf("unexpected phone otp event payload: user=%s email=%s masked=%s otpLen=%d", ev.UserID, ev.Email, ev.PhoneMasked, len(ev.OTP))
	}
	wantExp := clk.Now().Add(5 * time.Minute).Format(time.RFC3339)
	if ev.ExpiresAt != wantExp {
		t.Fatalf("event expires_at = %q; want %q", ev.ExpiresAt, wantExp)
	}

	// Verify with explicit +84 equivalent of the same phone number.
	verResp, err := srv.VerifyPhoneOTP(userCtx, &userv1.VerifyPhoneOTPRequest{
		Phone: "+84 901-234-789",
		Otp:   ev.OTP,
	})
	if err != nil {
		t.Fatalf("VerifyPhoneOTP: %v", err)
	}
	if !verResp.GetVerified() || !verResp.GetUser().GetPhoneVerified() || verResp.GetUser().GetPhone() != "0901234789" {
		t.Fatalf("unexpected VerifyPhoneOTPResponse: %+v", verResp)
	}

	// ValidateUser reflects phone_verified=true.
	valResp, err := srv.ValidateUser(userCtx, &userv1.ValidateUserRequest{UserId: userID})
	if err != nil || !valResp.GetPhoneVerified() {
		t.Fatalf("ValidateUser after phone verify: err=%v phone_verified=%v", err, valResp.GetPhoneVerified())
	}

	// Replay same OTP must fail (single-use).
	if _, err := srv.VerifyPhoneOTP(userCtx, &userv1.VerifyPhoneOTPRequest{
		Phone: "0901234789",
		Otp:   ev.OTP,
	}); status.Code(err) != codes.InvalidArgument || status.Convert(err).Message() != errMsgInvalidOrExpiredOTP {
		t.Fatalf("replay OTP: want InvalidArgument %q, got %v", errMsgInvalidOrExpiredOTP, err)
	}

	// OTP and raw phone must NEVER appear in logs or audit_logs.
	assertNoSensitiveLeak(t, "phone otp logs", logBuf.String(), ev.OTP, "0901234789")
	store.mu.Lock()
	var verifyAudits int
	for _, a := range store.auditLogs {
		if a.Action == "user.phone.verify_otp" && a.TargetID == userID {
			verifyAudits++
			raw, _ := json.Marshal(a)
			assertNoSensitiveLeak(t, "phone otp audit log", string(raw), ev.OTP, "0901234789")
		}
	}
	store.mu.Unlock()
	if verifyAudits != 1 {
		t.Fatalf("expected 1 user.phone.verify_otp audit log, got %d", verifyAudits)
	}

	// User registered with empty phone can request and verify a new phone (sets users.phone + phone_verified=true).
	noPhoneID := registerActiveUser(t, srv, pub, "buyer-nophone@example.com", "")
	noPhoneCtx := ctxWithUser(noPhoneID, RoleBuyer, "")
	if _, err := srv.RequestPhoneOTP(noPhoneCtx, &userv1.RequestPhoneOTPRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty profile & request phone: want InvalidArgument, got %v", err)
	}
	req2, err := srv.RequestPhoneOTP(noPhoneCtx, &userv1.RequestPhoneOTPRequest{Phone: "0912345678"})
	if err != nil || req2.GetPhoneMasked() != "091****678" {
		t.Fatalf("RequestPhoneOTP for empty-phone user: err=%v resp=%+v", err, req2)
	}
	otp2 := pub.phoneOTPRequested[len(pub.phoneOTPRequested)-1].OTP
	ver2, err := srv.VerifyPhoneOTP(noPhoneCtx, &userv1.VerifyPhoneOTPRequest{Phone: "0912345678", Otp: otp2})
	if err != nil || !ver2.GetVerified() || !ver2.GetUser().GetPhoneVerified() || ver2.GetUser().GetPhone() != "0912345678" {
		t.Fatalf("VerifyPhoneOTP empty-phone user: err=%v resp=%+v", err, ver2)
	}
}

func TestW3B_E19_PhoneOTP_AuthAndInputGuards(t *testing.T) {
	clk := newW2BClock(time.Date(2026, 11, 24, 10, 30, 0, 0, time.UTC))
	srv, store, sec, pub, _ := newW2BHarness(t, Options{Now: clk.Now})
	sec.nowFn = clk.Now

	buyerID := registerActiveUser(t, srv, pub, "buyer-auth-guard@example.com", "0903111222")
	buyerCtx := ctxWithUser(buyerID, RoleBuyer, "")

	// Anonymous -> Unauthenticated.
	if _, err := srv.RequestPhoneOTP(context.Background(), &userv1.RequestPhoneOTPRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anonymous RequestPhoneOTP: want Unauthenticated, got %v", err)
	}
	if _, err := srv.VerifyPhoneOTP(context.Background(), &userv1.VerifyPhoneOTPRequest{Phone: "0903111222", Otp: "123456"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anonymous VerifyPhoneOTP: want Unauthenticated, got %v", err)
	}

	// Service token -> PermissionDenied.
	svcCtx := ctxWithService("order-service")
	if _, err := srv.RequestPhoneOTP(svcCtx, &userv1.RequestPhoneOTPRequest{Phone: "0903111222"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service RequestPhoneOTP: want PermissionDenied, got %v", err)
	}
	if _, err := srv.VerifyPhoneOTP(svcCtx, &userv1.VerifyPhoneOTPRequest{Phone: "0903111222", Otp: "123456"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service VerifyPhoneOTP: want PermissionDenied, got %v", err)
	}

	// Invalid phone & empty OTP -> InvalidArgument.
	if _, err := srv.RequestPhoneOTP(buyerCtx, &userv1.RequestPhoneOTPRequest{Phone: "not-a-phone"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid phone RequestPhoneOTP: want InvalidArgument, got %v", err)
	}
	if _, err := srv.VerifyPhoneOTP(buyerCtx, &userv1.VerifyPhoneOTPRequest{Phone: "0903111222", Otp: "   "}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty otp VerifyPhoneOTP: want InvalidArgument, got %v", err)
	}
	if _, err := srv.VerifyPhoneOTP(buyerCtx, &userv1.VerifyPhoneOTPRequest{Phone: "not-a-phone", Otp: "123456"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid phone VerifyPhoneOTP: want InvalidArgument, got %v", err)
	}

	// Locked user -> FailedPrecondition.
	store.mu.Lock()
	u := store.users[buyerID]
	u.Status = StatusLocked
	store.users[buyerID] = u
	store.mu.Unlock()
	if _, err := srv.RequestPhoneOTP(buyerCtx, &userv1.RequestPhoneOTPRequest{}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("locked user RequestPhoneOTP: want FailedPrecondition, got %v", err)
	}
	if _, err := srv.VerifyPhoneOTP(buyerCtx, &userv1.VerifyPhoneOTPRequest{Phone: "0903111222", Otp: "123456"}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("locked user VerifyPhoneOTP: want FailedPrecondition, got %v", err)
	}

	// Restore active, then revoke token -> Unauthenticated.
	store.mu.Lock()
	u.Status = StatusActive
	store.users[buyerID] = u
	store.mu.Unlock()
	claims, _ := auth.ClaimsFromContext(buyerCtx)
	_ = sec.RevokeJTI(context.Background(), claims.JTI(), 15*time.Minute)
	if _, err := srv.RequestPhoneOTP(buyerCtx, &userv1.RequestPhoneOTPRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("revoked token RequestPhoneOTP: want Unauthenticated, got %v", err)
	}
}

func TestW3B_E19_PhoneOTP_CooldownHourlyLimitAndExpiry(t *testing.T) {
	clk := newW2BClock(time.Date(2026, 11, 24, 11, 0, 0, 0, time.UTC))
	srv, _, sec, pub, _ := newW2BHarness(t, Options{Now: clk.Now})
	sec.nowFn = clk.Now

	buyerID := registerActiveUser(t, srv, pub, "buyer-rate@example.com", "0904111222")
	buyerCtx := ctxWithUser(buyerID, RoleBuyer, "")

	// 1st send succeeds.
	if _, err := srv.RequestPhoneOTP(buyerCtx, &userv1.RequestPhoneOTPRequest{}); err != nil {
		t.Fatalf("1st RequestPhoneOTP: %v", err)
	}

	// Retry inside 60s cooldown -> ResourceExhausted (429), must NOT consume hourly quota.
	clk.Advance(30 * time.Second)
	for i := 0; i < 3; i++ {
		if _, err := srv.RequestPhoneOTP(buyerCtx, &userv1.RequestPhoneOTPRequest{}); status.Code(err) != codes.ResourceExhausted {
			t.Fatalf("cooldown retry %d: want ResourceExhausted, got %v", i, err)
		}
	}

	// Sends 2..5 spaced 61s apart all succeed (proving cooldown rejections did not consume hourly quota).
	for i := 2; i <= 5; i++ {
		clk.Advance(61 * time.Second)
		if _, err := srv.RequestPhoneOTP(buyerCtx, &userv1.RequestPhoneOTPRequest{}); err != nil {
			t.Fatalf("send %d/5 after cooldown: %v", i, err)
		}
	}

	// 6th send after cooldown (still within 1 hour) -> ResourceExhausted (429).
	clk.Advance(61 * time.Second)
	if _, err := srv.RequestPhoneOTP(buyerCtx, &userv1.RequestPhoneOTPRequest{}); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("6th send in 1 hour: want ResourceExhausted, got %v", err)
	}

	// OTP expires after 5 minutes.
	lastOTP := pub.phoneOTPRequested[len(pub.phoneOTPRequested)-1].OTP
	clk.Advance(5 * time.Minute)
	if _, err := srv.VerifyPhoneOTP(buyerCtx, &userv1.VerifyPhoneOTPRequest{
		Phone: "0904111222",
		Otp:   lastOTP,
	}); status.Code(err) != codes.InvalidArgument || status.Convert(err).Message() != errMsgInvalidOrExpiredOTP {
		t.Fatalf("expired OTP verify: want InvalidArgument %q, got %v", errMsgInvalidOrExpiredOTP, err)
	}

	// After the 1-hour window rolls over, user can request again and verify.
	clk.Advance(time.Hour)
	if _, err := srv.RequestPhoneOTP(buyerCtx, &userv1.RequestPhoneOTPRequest{}); err != nil {
		t.Fatalf("send after 1-hour window reset: %v", err)
	}
	freshOTP := pub.phoneOTPRequested[len(pub.phoneOTPRequested)-1].OTP
	if resp, err := srv.VerifyPhoneOTP(buyerCtx, &userv1.VerifyPhoneOTPRequest{
		Phone: "0904111222",
		Otp:   freshOTP,
	}); err != nil || !resp.GetVerified() {
		t.Fatalf("verify fresh OTP after window reset: err=%v resp=%+v", err, resp)
	}
}

func TestW3B_E19_PhoneOTP_BruteForce5AttemptsDeletesWithUniformError(t *testing.T) {
	clk := newW2BClock(time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC))
	srv, _, sec, pub, logBuf := newW2BHarness(t, Options{Now: clk.Now})
	sec.nowFn = clk.Now

	buyerID := registerActiveUser(t, srv, pub, "buyer-bf@example.com", "0905111222")
	buyerCtx := ctxWithUser(buyerID, RoleBuyer, "")

	if _, err := srv.RequestPhoneOTP(buyerCtx, &userv1.RequestPhoneOTPRequest{}); err != nil {
		t.Fatalf("RequestPhoneOTP: %v", err)
	}
	realOTP := pub.phoneOTPRequested[len(pub.phoneOTPRequested)-1].OTP
	wrongOTP := "000000"
	if wrongOTP == realOTP {
		wrongOTP = "999999"
	}

	// 5 wrong guesses must all return the EXACT same code and message; the 5th deletes the OTP.
	for attempt := 1; attempt <= 5; attempt++ {
		_, err := srv.VerifyPhoneOTP(buyerCtx, &userv1.VerifyPhoneOTPRequest{
			Phone: "0905111222",
			Otp:   wrongOTP,
		})
		if status.Code(err) != codes.InvalidArgument || status.Convert(err).Message() != errMsgInvalidOrExpiredOTP {
			t.Fatalf("wrong attempt %d: want InvalidArgument %q, got %v", attempt, errMsgInvalidOrExpiredOTP, err)
		}
		assertNoSensitiveLeak(t, "wrong attempt error", err.Error(), realOTP, wrongOTP)
	}

	// 6th attempt with the REAL OTP must fail with the exact same uniform error because attempt 5 deleted the OTP.
	_, err := srv.VerifyPhoneOTP(buyerCtx, &userv1.VerifyPhoneOTPRequest{
		Phone: "0905111222",
		Otp:   realOTP,
	})
	if status.Code(err) != codes.InvalidArgument || status.Convert(err).Message() != errMsgInvalidOrExpiredOTP {
		t.Fatalf("6th attempt with real OTP after 5 wrong guesses: want InvalidArgument %q, got %v", errMsgInvalidOrExpiredOTP, err)
	}
	assertNoSensitiveLeak(t, "brute-force logs", logBuf.String(), realOTP, wrongOTP)
}

func TestW3B_E19_PhoneOTP_PhoneChangedBetweenRequestAndVerifyRejected(t *testing.T) {
	clk := newW2BClock(time.Date(2026, 11, 24, 13, 0, 0, 0, time.UTC))
	srv, store, sec, pub, _ := newW2BHarness(t, Options{Now: clk.Now})
	sec.nowFn = clk.Now

	// Scenario A: Request OTP for 0906111111, submit VerifyPhoneOTP with 0906222222 -> rejected.
	userA := registerActiveUser(t, srv, pub, "buyer-change-a@example.com", "0906111111")
	ctxA := ctxWithUser(userA, RoleBuyer, "")
	if _, err := srv.RequestPhoneOTP(ctxA, &userv1.RequestPhoneOTPRequest{Phone: "0906111111"}); err != nil {
		t.Fatalf("RequestPhoneOTP A: %v", err)
	}
	otpA := pub.phoneOTPRequested[len(pub.phoneOTPRequested)-1].OTP
	if _, err := srv.VerifyPhoneOTP(ctxA, &userv1.VerifyPhoneOTPRequest{Phone: "0906222222", Otp: otpA}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("verify with different phone in request: want InvalidArgument, got %v", err)
	}

	// Scenario B: Request OTP for 0906333333, then UpdateProfile changes phone to 0906444444 ->
	// verifying with old phone, new phone, or empty phone MUST ALL be rejected; changing back to 0906333333 also stays rejected!
	userB := registerActiveUser(t, srv, pub, "buyer-change-b@example.com", "0906333333")
	ctxB := ctxWithUser(userB, RoleBuyer, "")
	if _, err := srv.RequestPhoneOTP(ctxB, &userv1.RequestPhoneOTPRequest{}); err != nil {
		t.Fatalf("RequestPhoneOTP B: %v", err)
	}
	otpB := pub.phoneOTPRequested[len(pub.phoneOTPRequested)-1].OTP

	if _, err := srv.UpdateProfile(ctxB, &userv1.UpdateProfileRequest{Phone: "0906444444"}); err != nil {
		t.Fatalf("UpdateProfile B to new phone: %v", err)
	}
	for _, attemptPhone := range []string{"0906333333", "0906444444", ""} {
		if _, err := srv.VerifyPhoneOTP(ctxB, &userv1.VerifyPhoneOTPRequest{Phone: attemptPhone, Otp: otpB}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("VerifyPhoneOTP after UpdateProfile (phone=%q): want InvalidArgument, got %v", attemptPhone, err)
		}
	}
	// Changing profile back to original phone must NOT resurrect invalidated OTP.
	if _, err := srv.UpdateProfile(ctxB, &userv1.UpdateProfileRequest{Phone: "0906333333"}); err != nil {
		t.Fatalf("UpdateProfile B back to old phone: %v", err)
	}
	if _, err := srv.VerifyPhoneOTP(ctxB, &userv1.VerifyPhoneOTPRequest{Phone: "0906333333", Otp: otpB}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("VerifyPhoneOTP after reverting phone: want InvalidArgument, got %v", err)
	}

	// Scenario C: Defense in depth — even if DeletePhoneOTP fails during UpdateProfile, VerifyPhoneOTP STILL rejects via profilePhone digest binding!
	userC := registerActiveUser(t, srv, pub, "buyer-change-c@example.com", "0906555555")
	ctxC := ctxWithUser(userC, RoleBuyer, "")
	if _, err := srv.RequestPhoneOTP(ctxC, &userv1.RequestPhoneOTPRequest{}); err != nil {
		t.Fatalf("RequestPhoneOTP C: %v", err)
	}
	otpC := pub.phoneOTPRequested[len(pub.phoneOTPRequested)-1].OTP
	sec.mu.Lock()
	sec.deletePhoneOTPErr = errors.New("simulated transient redis delete error")
	sec.mu.Unlock()
	if _, err := srv.UpdateProfile(ctxC, &userv1.UpdateProfileRequest{Phone: "0906666666"}); err != nil {
		t.Fatalf("UpdateProfile C: %v", err)
	}
	sec.mu.Lock()
	sec.deletePhoneOTPErr = nil
	sec.mu.Unlock()
	for _, attemptPhone := range []string{"0906555555", "0906666666"} {
		if _, err := srv.VerifyPhoneOTP(ctxC, &userv1.VerifyPhoneOTPRequest{Phone: attemptPhone, Otp: otpC}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("defense-in-depth VerifyPhoneOTP (phone=%q): want InvalidArgument, got %v", attemptPhone, err)
		}
	}
	store.mu.Lock()
	if store.users[userC].PhoneVerified {
		store.mu.Unlock()
		t.Fatalf("userC phone_verified became true despite phone change")
	}
	store.mu.Unlock()

	// Scenario D: After verification, UpdateProfile changing phone resets phone_verified back to false; changing full_name keeps it true.
	clk.Advance(61 * time.Second)
	if _, err := srv.RequestPhoneOTP(ctxC, &userv1.RequestPhoneOTPRequest{}); err != nil {
		t.Fatalf("RequestPhoneOTP C fresh: %v", err)
	}
	otpC2 := pub.phoneOTPRequested[len(pub.phoneOTPRequested)-1].OTP
	if ver, err := srv.VerifyPhoneOTP(ctxC, &userv1.VerifyPhoneOTPRequest{Phone: "0906666666", Otp: otpC2}); err != nil || !ver.GetUser().GetPhoneVerified() {
		t.Fatalf("VerifyPhoneOTP C fresh: err=%v resp=%+v", err, ver)
	}
	if updName, err := srv.UpdateProfile(ctxC, &userv1.UpdateProfileRequest{FullName: "Renamed Verified"}); err != nil || !updName.GetUser().GetPhoneVerified() {
		t.Fatalf("UpdateProfile name only should keep phone_verified=true: err=%v resp=%+v", err, updName)
	}
	if updPhone, err := srv.UpdateProfile(ctxC, &userv1.UpdateProfileRequest{Phone: "0906777777"}); err != nil || updPhone.GetUser().GetPhoneVerified() {
		t.Fatalf("UpdateProfile phone change must reset phone_verified=false: err=%v resp=%+v", err, updPhone)
	}
}

func TestW3B_E19_PhoneOTP_DuplicatePhoneOwnedByAnotherAccountRejected(t *testing.T) {
	clk := newW2BClock(time.Date(2026, 11, 24, 14, 0, 0, 0, time.UTC))
	srv, _, sec, pub, _ := newW2BHarness(t, Options{Now: clk.Now})
	sec.nowFn = clk.Now

	ownerID := registerActiveUser(t, srv, pub, "owner-phone@example.com", "0907111222")
	ownerCtx := ctxWithUser(ownerID, RoleBuyer, "")
	if _, err := srv.RequestPhoneOTP(ownerCtx, &userv1.RequestPhoneOTPRequest{}); err != nil {
		t.Fatalf("owner RequestPhoneOTP: %v", err)
	}
	ownerOTP := pub.phoneOTPRequested[len(pub.phoneOTPRequested)-1].OTP
	if _, err := srv.VerifyPhoneOTP(ownerCtx, &userv1.VerifyPhoneOTPRequest{Phone: "0907111222", Otp: ownerOTP}); err != nil {
		t.Fatalf("owner VerifyPhoneOTP: %v", err)
	}

	// Another account requesting OTP for the exact same phone OR +84/84 equivalent -> AlreadyExists.
	otherID := registerActiveUser(t, srv, pub, "other-phone@example.com", "")
	otherCtx := ctxWithUser(otherID, RoleBuyer, "")
	for _, dupPhone := range []string{"0907111222", "+84907111222", "84907111222"} {
		if _, err := srv.RequestPhoneOTP(otherCtx, &userv1.RequestPhoneOTPRequest{Phone: dupPhone}); status.Code(err) != codes.AlreadyExists {
			t.Fatalf("other RequestPhoneOTP(%q): want AlreadyExists, got %v", dupPhone, err)
		}
	}

	// Unverified phone does NOT occupy the number (E19-bis): User 3 updating profile to 0907333444
	// without verifying MUST NOT block User 2 from verifying 0907333444.
	user3ID := registerActiveUser(t, srv, pub, "user3-phone@example.com", "")
	user3Ctx := ctxWithUser(user3ID, RoleBuyer, "")
	if _, err := srv.RequestPhoneOTP(otherCtx, &userv1.RequestPhoneOTPRequest{Phone: "0907333444"}); err != nil {
		t.Fatalf("other RequestPhoneOTP free phone: %v", err)
	}
	otherOTP := pub.phoneOTPRequested[len(pub.phoneOTPRequested)-1].OTP
	if _, err := srv.UpdateProfile(user3Ctx, &userv1.UpdateProfileRequest{Phone: "+84 907-333-444"}); err != nil {
		t.Fatalf("user3 UpdateProfile unverified phone: %v", err)
	}
	if ver2, err := srv.VerifyPhoneOTP(otherCtx, &userv1.VerifyPhoneOTPRequest{Phone: "0907333444", Otp: otherOTP}); err != nil || !ver2.GetUser().GetPhoneVerified() {
		t.Fatalf("other VerifyPhoneOTP must succeed despite user3 unverified phone: err=%v resp=%+v", err, ver2)
	}

	// Late verified takeover race: User 4 requests OTP for 0907555666, User 3 requests & VERIFIES 0907555666 first -> User 4 gets AlreadyExists.
	user4ID := registerActiveUser(t, srv, pub, "user4-phone@example.com", "")
	user4Ctx := ctxWithUser(user4ID, RoleBuyer, "")
	if _, err := srv.RequestPhoneOTP(user4Ctx, &userv1.RequestPhoneOTPRequest{Phone: "0907555666"}); err != nil {
		t.Fatalf("user4 RequestPhoneOTP: %v", err)
	}
	otp4 := pub.phoneOTPRequested[len(pub.phoneOTPRequested)-1].OTP
	if _, err := srv.RequestPhoneOTP(user3Ctx, &userv1.RequestPhoneOTPRequest{Phone: "+84907555666"}); err != nil {
		t.Fatalf("user3 RequestPhoneOTP: %v", err)
	}
	otp3 := pub.phoneOTPRequested[len(pub.phoneOTPRequested)-1].OTP
	if _, err := srv.VerifyPhoneOTP(user3Ctx, &userv1.VerifyPhoneOTPRequest{Phone: "+84907555666", Otp: otp3}); err != nil {
		t.Fatalf("user3 VerifyPhoneOTP: %v", err)
	}
	if _, err := srv.VerifyPhoneOTP(user4Ctx, &userv1.VerifyPhoneOTPRequest{Phone: "0907555666", Otp: otp4}); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("user4 VerifyPhoneOTP after user3 verified takeover: want AlreadyExists, got %v", err)
	}

	// UpdateProfile with same phone in a different format keeps phone_verified=true and canonical storage.
	updSame, err := srv.UpdateProfile(user3Ctx, &userv1.UpdateProfileRequest{Phone: "090.755.5666"})
	if err != nil || !updSame.GetUser().GetPhoneVerified() || updSame.GetUser().GetPhone() != "0907555666" {
		t.Fatalf("UpdateProfile same phone formatted must keep phone_verified=true and canonical phone: err=%v resp=%+v", err, updSame)
	}
}

func TestW3B_E19_PhoneOTP_PublishFailureRollbacksAndRedisFailClosed(t *testing.T) {
	clk := newW2BClock(time.Date(2026, 11, 24, 15, 0, 0, 0, time.UTC))
	srv, _, sec, pub, logBuf := newW2BHarness(t, Options{Now: clk.Now})
	sec.nowFn = clk.Now

	buyerID := registerActiveUser(t, srv, pub, "buyer-pubfail@example.com", "0908111222")
	buyerCtx := ctxWithUser(buyerID, RoleBuyer, "")

	// Inject publish failure with cancelled context inside publish hook to prove rollback runs with context.WithoutCancel.
	var capturedOTP string
	reqCtx2, cancel2 := context.WithCancel(buyerCtx)
	pub.onPublishPhoneOTP = func(_ context.Context, payload events.PhoneOTPRequestedPayload) error {
		capturedOTP = payload.OTP
		cancel2()
		return errors.New("nats timeout")
	}
	if _, err := srv.RequestPhoneOTP(reqCtx2, &userv1.RequestPhoneOTPRequest{}); status.Code(err) != codes.Internal {
		t.Fatalf("publish failure RequestPhoneOTP: want Internal, got %v", err)
	}
	assertNoSensitiveLeak(t, "publish failure logs", logBuf.String(), capturedOTP, "0908111222")

	// Undelivered OTP must NOT be verifiable (rolled back!).
	if _, err := srv.VerifyPhoneOTP(buyerCtx, &userv1.VerifyPhoneOTPRequest{Phone: "0908111222", Otp: capturedOTP}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("undelivered OTP should have been rolled back, got %v", err)
	}

	// Immediate retry (0s later!) MUST succeed because cooldown & hourly quota were rolled back!
	pub.onPublishPhoneOTP = nil
	if _, err := srv.RequestPhoneOTP(buyerCtx, &userv1.RequestPhoneOTPRequest{}); err != nil {
		t.Fatalf("retry right after publish failure should not hit cooldown: %v", err)
	}

	// Redis failure on SavePhoneOTP / VerifyPhoneOTP fails closed with Unavailable.
	sec.mu.Lock()
	sec.savePhoneOTPErr = errors.New("redis down")
	sec.verifyPhoneOTPErr = errors.New("redis down")
	sec.mu.Unlock()
	if _, err := srv.RequestPhoneOTP(buyerCtx, &userv1.RequestPhoneOTPRequest{}); status.Code(err) != codes.Unavailable {
		t.Fatalf("Redis down RequestPhoneOTP: want Unavailable, got %v", err)
	}
	if _, err := srv.VerifyPhoneOTP(buyerCtx, &userv1.VerifyPhoneOTPRequest{Phone: "0908111222", Otp: "123456"}); status.Code(err) != codes.Unavailable {
		t.Fatalf("Redis down VerifyPhoneOTP: want Unavailable, got %v", err)
	}
}

func TestW3B_E19_PhoneOTP_ConcurrentVerifySingleUse(t *testing.T) {
	clk := newW2BClock(time.Date(2026, 11, 24, 16, 0, 0, 0, time.UTC))
	srv, _, sec, pub, _ := newW2BHarness(t, Options{Now: clk.Now})
	sec.nowFn = clk.Now

	buyerID := registerActiveUser(t, srv, pub, "buyer-race@example.com", "0909111222")
	buyerCtx := ctxWithUser(buyerID, RoleBuyer, "")

	if _, err := srv.RequestPhoneOTP(buyerCtx, &userv1.RequestPhoneOTPRequest{}); err != nil {
		t.Fatalf("RequestPhoneOTP: %v", err)
	}
	realOTP := pub.phoneOTPRequested[len(pub.phoneOTPRequested)-1].OTP

	const workers = 10
	start := make(chan struct{})
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			_, errs[idx] = srv.VerifyPhoneOTP(buyerCtx, &userv1.VerifyPhoneOTPRequest{
				Phone: "0909111222",
				Otp:   realOTP,
			})
		}(i)
	}
	close(start)
	wg.Wait()

	var okCount, invalidCount int
	for _, err := range errs {
		switch {
		case err == nil:
			okCount++
		case status.Code(err) == codes.InvalidArgument:
			invalidCount++
		default:
			t.Fatalf("unexpected concurrent verify error: %v", err)
		}
	}
	if okCount != 1 || invalidCount != workers-1 {
		t.Fatalf("concurrent VerifyPhoneOTP: want 1 ok + %d InvalidArgument, got ok=%d invalid=%d", workers-1, okCount, invalidCount)
	}
}
