package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/user/internal/db"
	"github.com/nexusmart/core/services/user/internal/events"
	"github.com/nexusmart/core/services/user/internal/password"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

func ctxWithClientIP(base context.Context, ip string) context.Context {
	md := metadata.Pairs(mdClientIP, ip)
	ctx := metadata.NewIncomingContext(base, md)
	return peer.NewContext(ctx, &peer.Peer{
		Addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 40000},
	})
}

func seedUserWithRole(t *testing.T, srv *Server, store *memStore, pub *memPublisher, email, phone, role string) string {
	t.Helper()
	uid := registerActiveUser(t, srv, pub, email, phone)
	store.mu.Lock()
	u := store.users[uid]
	u.Role = role
	store.users[uid] = u
	store.mu.Unlock()
	return uid
}

func seedActiveShopMem(t *testing.T, srv *Server, store *memStore, pub *memPublisher, ownerEmail, phone, shopName string) (ownerID, shopID string) {
	t.Helper()
	ownerID = registerActiveUser(t, srv, pub, ownerEmail, phone)
	ownerCtx := ctxWithUser(ownerID, RoleBuyer, "")
	addr, err := srv.CreateAddress(ownerCtx, &userv1.CreateAddressRequest{
		RecipientName:  "Chu Shop",
		RecipientPhone: phone,
		ProvinceCode:   "79",
		ProvinceName:   "TP Ho Chi Minh",
		WardCode:       "79001",
		WardName:       "Phuong 1",
		StreetAddress:  "123 Le Loi",
		IsDefault:      true,
	})
	if err != nil {
		t.Fatalf("CreateAddress: %v", err)
	}
	regShop, err := srv.RegisterShop(ownerCtx, &userv1.RegisterShopRequest{
		Name:               shopName,
		PickupAddressId:    addr.GetAddress().GetId(),
		ContactPhone:       phone,
		BusinessLicenseDoc: "GPKD-W3B2.pdf",
	})
	if err != nil {
		t.Fatalf("RegisterShop: %v", err)
	}
	adminID := uuid.NewString()
	adminCtx := ctxWithUser(adminID, RoleAdmin, "")
	if _, err := srv.AdminReviewShop(adminCtx, &userv1.AdminReviewShopRequest{
		ShopId:   regShop.GetShop().GetId(),
		Decision: userv1.ReviewDecision_REVIEW_DECISION_APPROVE,
	}); err != nil {
		t.Fatalf("AdminReviewShop approve: %v", err)
	}
	_ = store
	return ownerID, regShop.GetShop().GetId()
}

func TestW3B2_MaskIP(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"   ", ""},
		{"203.0.113.45", "203.0.113.xxx"},
		{"203.0.113.0", "203.0.113.xxx"},
		{"203.0.113.45:54321", "203.0.113.xxx"},
		{"::ffff:198.51.100.7", "198.51.100.xxx"},
		{"2001:db8:85a3::8a2e:370:7334", "2001:db8:85a3::xxxx"},
		{"[2001:db8:85a3::1]:443", "2001:db8:85a3::xxxx"},
		{"::1", "0:0:0::xxxx"},
		{"not-an-ip", "masked"},
	}
	for _, tc := range cases {
		if got := maskIP(tc.in); got != tc.want {
			t.Fatalf("maskIP(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

func TestW3B2_F10_RoleHierarchySuperAdminOnlyAndLastSuperAdmin(t *testing.T) {
	srv, store, _, pub, _ := newTestHarness(t)
	ctx := context.Background()

	super1ID := seedUserWithRole(t, srv, store, pub, "f10-super1@example.com", "0901100001", RoleSuperAdmin)
	super2ID := seedUserWithRole(t, srv, store, pub, "f10-super2@example.com", "0901100002", RoleSuperAdmin)
	admin1ID := seedUserWithRole(t, srv, store, pub, "f10-admin1@example.com", "0901100003", RoleAdmin)
	admin2ID := seedUserWithRole(t, srv, store, pub, "f10-admin2@example.com", "0901100004", RoleAdmin)
	opsID := seedUserWithRole(t, srv, store, pub, "f10-ops@example.com", "0901100005", RoleOps)
	cskhID := seedUserWithRole(t, srv, store, pub, "f10-cskh@example.com", "0901100006", RoleCSKH)
	modID := seedUserWithRole(t, srv, store, pub, "f10-mod@example.com", "0901100007", RoleModerator)
	accID := seedUserWithRole(t, srv, store, pub, "f10-acc@example.com", "0901100008", RoleAccountant)
	buyerID := seedUserWithRole(t, srv, store, pub, "f10-buyer@example.com", "0901100009", RoleBuyer)
	sellerID := seedUserWithRole(t, srv, store, pub, "f10-seller@example.com", "0901100010", RoleSeller)

	super1Ctx := ctxWithUser(super1ID, RoleSuperAdmin, "")
	admin1Ctx := ctxWithUser(admin1ID, RoleAdmin, "")
	opsCtx := ctxWithUser(opsID, RoleOps, "")
	cskhCtx := ctxWithUser(cskhID, RoleCSKH, "")
	modCtx := ctxWithUser(modID, RoleModerator, "")
	accCtx := ctxWithUser(accID, RoleAccountant, "")
	buyerCtx := ctxWithUser(buyerID, RoleBuyer, "")

	// 1. Only super_admin may call CreateStaffUser or UpdateUserRole.
	for _, tc := range []struct {
		name string
		ctx  context.Context
	}{
		{"admin", admin1Ctx},
		{"ops", opsCtx},
		{"cskh", cskhCtx},
		{"moderator", modCtx},
		{"accountant", accCtx},
		{"buyer", buyerCtx},
	} {
		if _, err := srv.CreateStaffUser(tc.ctx, CreateStaffUserInput{
			FullName: "Blocked Staff",
			Email:    "blocked-" + tc.name + "@example.com",
			Role:     RoleModerator,
		}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("%s CreateStaffUser: want PermissionDenied, got %v", tc.name, err)
		}
		if _, err := srv.UpdateUserRole(tc.ctx, UpdateUserRoleInput{
			TargetUserID: buyerID,
			Role:         RoleModerator,
			Reason:       "unauthorized role change",
		}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("%s UpdateUserRole: want PermissionDenied, got %v", tc.name, err)
		}
	}

	// 2. cskh and ops can lock/unlock buyer & seller, MUST NOT touch any staff/admin/super_admin.
	for _, actor := range []struct {
		name string
		ctx  context.Context
	}{
		{"cskh", cskhCtx},
		{"ops", opsCtx},
	} {
		for _, targetID := range []string{buyerID, sellerID} {
			if _, err := srv.AdminUpdateUserStatus(actor.ctx, &userv1.AdminUpdateUserStatusRequest{
				TargetUserId: targetID,
				Status:       userv1.UserStatus_USER_STATUS_LOCKED,
				Reason:       "policy check",
			}); err != nil {
				t.Fatalf("%s lock end-user %s: unexpected err %v", actor.name, targetID, err)
			}
			if _, err := srv.AdminUpdateUserStatus(actor.ctx, &userv1.AdminUpdateUserStatusRequest{
				TargetUserId: targetID,
				Status:       userv1.UserStatus_USER_STATUS_ACTIVE,
			}); err != nil {
				t.Fatalf("%s unlock end-user %s: unexpected err %v", actor.name, targetID, err)
			}
		}
		for _, forbiddenTarget := range []struct {
			label string
			id    string
		}{
			{"moderator", modID},
			{"accountant", accID},
			{"ops", opsID},
			{"cskh", cskhID},
			{"admin", admin1ID},
			{"super_admin", super1ID},
		} {
			// Skip self-lock (self-lock returns FailedPrecondition).
			claims, _ := auth.ClaimsFromContext(actor.ctx)
			if claims.UserID() == forbiddenTarget.id {
				continue
			}
			if _, err := srv.AdminUpdateUserStatus(actor.ctx, &userv1.AdminUpdateUserStatusRequest{
				TargetUserId: forbiddenTarget.id,
				Status:       userv1.UserStatus_USER_STATUS_LOCKED,
				Reason:       "attempt lock peer/higher",
			}); status.Code(err) != codes.PermissionDenied {
				t.Fatalf("%s lock %s: want PermissionDenied, got %v", actor.name, forbiddenTarget.label, err)
			}
		}
	}

	// 3. moderator & accountant cannot update status of anyone.
	for _, tc := range []struct {
		name string
		ctx  context.Context
	}{
		{"moderator", modCtx},
		{"accountant", accCtx},
	} {
		if _, err := srv.AdminUpdateUserStatus(tc.ctx, &userv1.AdminUpdateUserStatusRequest{
			TargetUserId: buyerID,
			Status:       userv1.UserStatus_USER_STATUS_LOCKED,
		}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("%s AdminUpdateUserStatus: want PermissionDenied, got %v", tc.name, err)
		}
	}

	// 4. admin can lock specialist staff (moderator/cskh/accountant/ops), CANNOT lock peer admin or super_admin.
	if _, err := srv.AdminUpdateUserStatus(admin1Ctx, &userv1.AdminUpdateUserStatusRequest{
		TargetUserId: modID,
		Status:       userv1.UserStatus_USER_STATUS_LOCKED,
		Reason:       "suspend moderator",
	}); err != nil {
		t.Fatalf("admin lock moderator: unexpected err %v", err)
	}
	if _, err := srv.AdminUpdateUserStatus(admin1Ctx, &userv1.AdminUpdateUserStatusRequest{
		TargetUserId: admin2ID,
		Status:       userv1.UserStatus_USER_STATUS_LOCKED,
		Reason:       "attempt lock peer admin",
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("admin lock peer admin: want PermissionDenied, got %v", err)
	}
	if _, err := srv.AdminUpdateUserStatus(admin1Ctx, &userv1.AdminUpdateUserStatusRequest{
		TargetUserId: super1ID,
		Status:       userv1.UserStatus_USER_STATUS_LOCKED,
		Reason:       "attempt lock super_admin",
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("admin lock super_admin: want PermissionDenied, got %v", err)
	}

	// 5. F10 strict CanActOnTargetRole: even super_admin cannot lock or demote a peer super_admin!
	if _, err := srv.AdminUpdateUserStatus(super1Ctx, &userv1.AdminUpdateUserStatusRequest{
		TargetUserId: super2ID,
		Status:       userv1.UserStatus_USER_STATUS_LOCKED,
		Reason:       "attempt lock peer super_admin",
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("super1 lock peer super2: want PermissionDenied (CanActOnTargetRole), got %v", err)
	}
	if _, err := srv.UpdateUserRole(super1Ctx, UpdateUserRoleInput{
		TargetUserID: super2ID,
		Role:         RoleAdmin,
		Reason:       "attempt demote peer super_admin",
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("super1 demote peer super2: want PermissionDenied (CanActOnTargetRole), got %v", err)
	}

	// super_admin CAN lock & change role of lower-ranked admin.
	if _, err := srv.AdminUpdateUserStatus(super1Ctx, &userv1.AdminUpdateUserStatusRequest{
		TargetUserId: admin2ID,
		Status:       userv1.UserStatus_USER_STATUS_LOCKED,
		Reason:       "lock lower admin",
	}); err != nil {
		t.Fatalf("super1 lock admin2: unexpected err %v", err)
	}

	// Simulate only 1 active super_admin remaining (mark super2 LOCKED) while admin1 is still ACTIVE.
	store.mu.Lock()
	s2 := store.users[super2ID]
	s2.Status = StatusLocked
	store.users[super2ID] = s2
	store.mu.Unlock()

	// Last super_admin protection: self-lock/self-demote returns FailedPrecondition and any peer super_admin
	// returns PermissionDenied (F10 CanActOnTargetRole), so the last active super_admin can never be locked or demoted.
	if _, err := srv.AdminUpdateUserStatus(super1Ctx, &userv1.AdminUpdateUserStatusRequest{
		TargetUserId: super1ID,
		Status:       userv1.UserStatus_USER_STATUS_LOCKED,
		Reason:       "self lock last super_admin",
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("self lock last super_admin: want FailedPrecondition, got %v", err)
	}
	if _, err := srv.UpdateUserRole(super1Ctx, UpdateUserRoleInput{
		TargetUserID: super1ID,
		Role:         RoleAdmin,
		Reason:       "self demote last super_admin",
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("self demote last super_admin: want FailedPrecondition, got %v", err)
	}
	otherSuperCtx := ctxWithUser(uuid.NewString(), RoleSuperAdmin, "")
	if _, err := srv.AdminUpdateUserStatus(otherSuperCtx, &userv1.AdminUpdateUserStatusRequest{
		TargetUserId: super1ID,
		Status:       userv1.UserStatus_USER_STATUS_LOCKED,
		Reason:       "lock last super_admin",
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("peer lock last super_admin: want PermissionDenied, got %v", err)
	}
	if _, err := srv.UpdateUserRole(otherSuperCtx, UpdateUserRoleInput{
		TargetUserID: super1ID,
		Role:         RoleAdmin,
		Reason:       "demote last super_admin to admin",
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("peer demote last super_admin to admin: want PermissionDenied, got %v", err)
	}
	// Verify super1 is still active super_admin in store.
	s1, err := store.GetUserByID(ctx, super1ID)
	if err != nil || s1.Role != RoleSuperAdmin || s1.Status != StatusActive {
		t.Fatalf("last super_admin mutated unexpectedly: %+v err=%v", s1, err)
	}
}

func TestW3B2_F14_CreateStaffUserUnknownPasswordAndResetFlow(t *testing.T) {
	store := newMemStore()
	sec := newMemSecurity()
	pub := &memPublisher{}
	hasher := password.NewDefaultHasher()
	authMgr, err := auth.NewManager("test-secret-w3b2-f14", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	var logBuf bytes.Buffer
	log := logger.New("user-f14-test", logger.WithWriter(&logBuf))
	srv := NewServer(store, sec, hasher, authMgr, pub, log, Options{
		OTPTTL:           10 * time.Minute,
		PasswordResetTTL: 15 * time.Minute,
	})

	ctx := context.Background()
	superID := seedUserWithRole(t, srv, store, pub, "f14-super@example.com", "0901200001", RoleSuperAdmin)
	superCtx := ctxWithClientIP(ctxWithUser(superID, RoleSuperAdmin, ""), "203.0.113.88")

	// Reject non-staff / super_admin roles in CreateStaffUser.
	for _, badRole := range []string{RoleBuyer, RoleSeller, RoleSuperAdmin, "root", ""} {
		if _, err := srv.CreateStaffUser(superCtx, CreateStaffUserInput{
			FullName: "Bad Role",
			Email:    "badrole-" + badRole + "@example.com",
			Role:     badRole,
		}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("CreateStaffUser role=%q: want InvalidArgument, got %v", badRole, err)
		}
	}

	// Create valid staff user (accountant) without supplying any password.
	res, err := srv.CreateStaffUser(superCtx, CreateStaffUserInput{
		FullName: "Nguyen Ke Toan",
		Email:    "staff-acc@example.com",
		Phone:    "+84 901-200-099",
		Role:     RoleAccountant,
	})
	if err != nil {
		t.Fatalf("CreateStaffUser: %v", err)
	}
	if !res.PasswordResetSent || res.User.Status != StatusActive || res.User.Role != RoleAccountant || res.User.Phone != "0901200099" {
		t.Fatalf("unexpected CreateStaffUser result: %+v", res)
	}
	if res.User.PasswordHash == "" || !strings.HasPrefix(res.User.PasswordHash, "$argon2id$") {
		t.Fatalf("expected argon2id hash for unknown random password, got %q", res.User.PasswordHash)
	}
	if len(pub.passwordResetRequested) != 1 {
		t.Fatalf("expected 1 password_reset_requested event, got %d", len(pub.passwordResetRequested))
	}
	resetEvt := pub.passwordResetRequested[0]
	if resetEvt.UserID != res.User.ID || resetEvt.Email != "staff-acc@example.com" || resetEvt.ResetToken == "" {
		t.Fatalf("unexpected reset event: %+v", resetEvt)
	}

	// Verify logs, audit_logs, and user_activity_logs never leak the reset token or password hash.
	if strings.Contains(logBuf.String(), resetEvt.ResetToken) || strings.Contains(logBuf.String(), res.User.PasswordHash) {
		t.Fatal("server logs leaked reset token or password hash")
	}
	for _, l := range store.userActivityLogs {
		if strings.Contains(l.MetadataJSON, resetEvt.ResetToken) || strings.Contains(l.MetadataJSON, res.User.PasswordHash) {
			t.Fatalf("activity log leaked secret: %+v", l)
		}
		if l.UserID == res.User.ID && l.IPAddress != "203.0.113.xxx" {
			t.Fatalf("expected masked IP 203.0.113.xxx in activity log, got %q", l.IPAddress)
		}
	}

	// Nobody knows initial password -> login with guessed password fails.
	if _, err := srv.Login(ctx, &userv1.LoginRequest{
		Email:    "staff-acc@example.com",
		Password: "Password123!",
	}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("login with guessed password: want Unauthenticated, got %v", err)
	}

	// Staff uses emailed reset token to set their own password, then logs in successfully!
	if _, err := srv.ResetPassword(ctx, &userv1.ResetPasswordRequest{
		Token:       resetEvt.ResetToken,
		NewPassword: "StaffSecret#2026!",
	}); err != nil {
		t.Fatalf("ResetPassword for new staff: %v", err)
	}
	loginResp, err := srv.Login(ctx, &userv1.LoginRequest{
		Email:    "staff-acc@example.com",
		Password: "StaffSecret#2026!",
	})
	if err != nil {
		t.Fatalf("Login after ResetPassword: %v", err)
	}
	claims, err := authMgr.VerifyAccessToken(loginResp.GetAccessToken())
	if err != nil || claims.Role != RoleAccountant {
		t.Fatalf("staff JWT claims: err=%v role=%q; want %q", err, claims.Role, RoleAccountant)
	}

	// If publisher fails on CreateStaffUser, undelivered reset token is invalidated & PasswordResetSent=false.
	pub.mu.Lock()
	pub.publishPasswordResetErr = errors.New("nats down")
	pub.mu.Unlock()
	resFail, err := srv.CreateStaffUser(superCtx, CreateStaffUserInput{
		FullName: "Undelivered Staff",
		Email:    "staff-failpub@example.com",
		Role:     RoleCSKH,
	})
	pub.mu.Lock()
	pub.publishPasswordResetErr = nil
	pub.mu.Unlock()
	if err != nil {
		t.Fatalf("CreateStaffUser when pub fails: unexpected err %v", err)
	}
	if resFail.PasswordResetSent {
		t.Fatal("expected PasswordResetSent=false when publish fails")
	}
	for _, pr := range store.passwordResets {
		if pr.UserID == resFail.User.ID && pr.UsedAt == nil {
			t.Fatal("expected undelivered reset token to be marked used/invalidated")
		}
	}
}

func TestW3B2_F11_F12_ShopSuspensionApprovalsLiveClaimsAndOutbox(t *testing.T) {
	store := newMemStore()
	sec := newMemSecurity()
	pub := &memPublisher{}
	hasher := password.NewDefaultHasher()
	authMgr, err := auth.NewManager("test-secret-w3b2-f11", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	outboxCh := make(chan struct{}, 4)
	log := logger.New("user-f11-test", logger.WithWriter(&bytes.Buffer{}))
	srv := NewServer(store, sec, hasher, authMgr, pub, log, Options{
		OutboxTrigger: outboxCh,
	})
	ctx := context.Background()

	superID := seedUserWithRole(t, srv, store, pub, "f11-super@example.com", "0901300001", RoleSuperAdmin)
	ops1ID := seedUserWithRole(t, srv, store, pub, "f11-ops1@example.com", "0901300002", RoleOps)
	ops2ID := seedUserWithRole(t, srv, store, pub, "f11-ops2@example.com", "0901300003", RoleOps)
	modID := seedUserWithRole(t, srv, store, pub, "f11-mod@example.com", "0901300004", RoleModerator)
	cskhID := seedUserWithRole(t, srv, store, pub, "f11-cskh@example.com", "0901300005", RoleCSKH)

	ownerID, shopID := seedActiveShopMem(t, srv, store, pub, "f11-seller@example.com", "0901300010", "Shop F11 Test")
	sellerLogin, err := srv.Login(ctx, &userv1.LoginRequest{
		Email:    "f11-seller@example.com",
		Password: "Password123!",
	})
	if err != nil {
		t.Fatalf("Login seller: %v", err)
	}
	sellerTokCtx := ctxWithToken(t, authMgr, sellerLogin.GetAccessToken())

	superCtx := ctxWithUser(superID, RoleSuperAdmin, "")
	ops1Ctx := ctxWithUser(ops1ID, RoleOps, "")
	ops2Ctx := ctxWithUser(ops2ID, RoleOps, "")
	modCtx := ctxWithUser(modID, RoleModerator, "")
	cskhCtx := ctxWithUser(cskhID, RoleCSKH, "")

	// cskh cannot request suspension.
	if _, err := srv.RequestShopSuspension(cskhCtx, RequestShopSuspensionInput{
		ShopID: shopID,
		Reason: "unauthorized",
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cskh RequestShopSuspension: want PermissionDenied, got %v", err)
	}

	// moderator CAN request suspension.
	req1Res, err := srv.RequestShopSuspension(modCtx, RequestShopSuspensionInput{
		ShopID: shopID,
		Reason: "ban hang gia",
	})
	if err != nil || req1Res.Approval.Status != ApprovalStatusPending || req1Res.ShopName != "Shop F11 Test" {
		t.Fatalf("moderator RequestShopSuspension: err=%v req=%+v", err, req1Res)
	}
	req1 := req1Res.Approval

	// Duplicate PENDING request for the same shop -> AlreadyExists.
	if _, err := srv.RequestShopSuspension(ops1Ctx, RequestShopSuspensionInput{
		ShopID: shopID,
		Reason: "trung lap",
	}); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("duplicate pending RequestShopSuspension: want AlreadyExists, got %v", err)
	}

	// moderator cannot decide (maker-only role).
	if _, err := srv.DecideShopSuspension(modCtx, DecideShopSuspensionInput{
		ApprovalID: req1.ID,
		Approve:    true,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("moderator DecideShopSuspension: want PermissionDenied, got %v", err)
	}

	// Reject req1 by ops1 -> shop stays ACTIVE, status_version unchanged, 0 outbox events.
	rejRes, err := srv.DecideShopSuspension(ops1Ctx, DecideShopSuspensionInput{
		ApprovalID: req1.ID,
		Approve:    false,
		Note:       "chua du bang chung",
	})
	if err != nil || rejRes.Approval.Status != ApprovalStatusRejected || rejRes.Shop.Status != ShopStatusActive {
		t.Fatalf("reject approval: err=%v res=%+v", err, rejRes)
	}
	if len(store.outboxEvents) != 0 {
		t.Fatalf("expected 0 outbox events on reject, got %d", len(store.outboxEvents))
	}

	// Create second request by ops1 -> ops1 self-approval MUST fail with PermissionDenied + MAKER_CHECKER_CONFLICT.
	req2Res, err := srv.RequestShopSuspension(ops1Ctx, RequestShopSuspensionInput{
		ShopID: shopID,
		Reason: "da xac minh hang gia",
	})
	if err != nil {
		t.Fatalf("ops1 RequestShopSuspension: %v", err)
	}
	req2 := req2Res.Approval
	if _, err := srv.DecideShopSuspension(ops1Ctx, DecideShopSuspensionInput{
		ApprovalID: req2.ID,
		Approve:    true,
	}); status.Code(err) != codes.PermissionDenied || !strings.Contains(err.Error(), "MAKER_CHECKER_CONFLICT") {
		t.Fatalf("self-approval by ops1: want PermissionDenied MAKER_CHECKER_CONFLICT, got %v", err)
	}

	// Live claims check (F11): ops2 logs in with ops JWT, then gets demoted to moderator ->
	// old ops JWT fails Unauthenticated (revoked_before), new moderator JWT fails PermissionDenied!
	ops2Login, err := srv.Login(ctx, &userv1.LoginRequest{
		Email:    "f11-ops2@example.com",
		Password: "Password123!",
	})
	if err != nil {
		t.Fatalf("Login ops2: %v", err)
	}
	ops2OldTokCtx := ctxWithToken(t, authMgr, ops2Login.GetAccessToken())
	if _, err := srv.UpdateUserRole(superCtx, UpdateUserRoleInput{
		TargetUserID: ops2ID,
		Role:         RoleModerator,
		Reason:       "demote ops2",
	}); err != nil {
		t.Fatalf("demote ops2: %v", err)
	}
	if _, err := srv.DecideShopSuspension(ops2OldTokCtx, DecideShopSuspensionInput{
		ApprovalID: req2.ID,
		Approve:    true,
	}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("demoted checker old JWT: want Unauthenticated, got %v", err)
	}
	ops2DemotedLogin, err := srv.Login(ctx, &userv1.LoginRequest{
		Email:    "f11-ops2@example.com",
		Password: "Password123!",
	})
	if err != nil {
		t.Fatalf("Login demoted ops2: %v", err)
	}
	if _, err := srv.DecideShopSuspension(ctxWithToken(t, authMgr, ops2DemotedLogin.GetAccessToken()), DecideShopSuspensionInput{
		ApprovalID: req2.ID,
		Approve:    true,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("demoted checker new moderator JWT: want PermissionDenied, got %v", err)
	}

	// Redis failure during SetRevokedBefore must roll back the entire approval TX!
	sec.mu.Lock()
	sec.setRevokedBeforeErr = errors.New("redis down")
	sec.mu.Unlock()
	if _, err := srv.DecideShopSuspension(superCtx, DecideShopSuspensionInput{
		ApprovalID: req2.ID,
		Approve:    true,
	}); status.Code(err) != codes.Unavailable {
		t.Fatalf("approve during Redis outage: want Unavailable, got %v", err)
	}
	sec.mu.Lock()
	sec.setRevokedBeforeErr = nil
	sec.mu.Unlock()
	if sh, _ := store.GetShopByID(ctx, shopID); sh.Status != ShopStatusActive {
		t.Fatalf("shop mutated despite rolled-back Redis failure: %+v", sh)
	}
	if len(store.outboxEvents) != 0 {
		t.Fatalf("outbox event leaked despite rollback: %d", len(store.outboxEvents))
	}

	// Restore ops2 -> ops, re-login for fresh ops JWT, and approve req2!
	if _, err := srv.UpdateUserRole(superCtx, UpdateUserRoleInput{
		TargetUserID: ops2ID,
		Role:         RoleOps,
		Reason:       "restore ops2",
	}); err != nil {
		t.Fatalf("restore ops2: %v", err)
	}
	ops2RestoredLogin, err := srv.Login(ctx, &userv1.LoginRequest{
		Email:    "f11-ops2@example.com",
		Password: "Password123!",
	})
	if err != nil {
		t.Fatalf("Login restored ops2: %v", err)
	}
	ops2FreshCtx := ctxWithToken(t, authMgr, ops2RestoredLogin.GetAccessToken())
	_ = ops2Ctx
	approvedRes, err := srv.DecideShopSuspension(ops2FreshCtx, DecideShopSuspensionInput{
		ApprovalID: req2.ID,
		Approve:    true,
		Note:       "dong y khoa",
	})
	if err != nil {
		t.Fatalf("ops2 DecideShopSuspension approve: %v", err)
	}
	if approvedRes.Approval.Status != ApprovalStatusApproved ||
		approvedRes.Shop.Status != ShopStatusSuspended ||
		approvedRes.Shop.SuspendedAt == nil ||
		approvedRes.Shop.SuspendedReason != "da xac minh hang gia" {
		t.Fatalf("unexpected approved result: %+v", approvedRes)
	}
	vSuspended := approvedRes.Shop.StatusVersion
	if vSuspended < 2 {
		t.Fatalf("expected status_version >= 2 after suspend, got %d", vSuspended)
	}

	// Owner's active JWT & refresh token MUST be revoked immediately!
	if _, err := srv.GetProfile(sellerTokCtx, &userv1.GetProfileRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("suspended shop owner's old access token: want Unauthenticated, got %v", err)
	}
	if _, err := srv.RefreshToken(ctx, &userv1.RefreshTokenRequest{RefreshToken: sellerLogin.GetRefreshToken()}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("suspended shop owner's old refresh token: want Unauthenticated, got %v", err)
	}

	// Outbox trigger fired and exactly 1 outbox event created for user.shop_status_changed.
	select {
	case <-outboxCh:
	default:
		t.Fatal("expected outbox trigger signal after suspend commit")
	}
	if len(store.outboxEvents) != 1 || store.outboxEvents[0].EventType != events.SubjectShopStatusChanged {
		t.Fatalf("expected 1 outbox user.shop_status_changed event, got %+v", store.outboxEvents)
	}
	var payload1 events.ShopStatusChangedPayload
	if err := json.Unmarshal(store.outboxEvents[0].PayloadJSON, &payload1); err != nil {
		t.Fatalf("unmarshal outbox payload1: %v", err)
	}
	if payload1.ShopID != shopID || payload1.OwnerUserID != ownerID || payload1.FromStatus != ShopStatusActive ||
		payload1.ToStatus != ShopStatusSuspended || payload1.Version != vSuspended ||
		payload1.ActorID != ops1ID || payload1.ApproverID != ops2ID || payload1.OccurredAt == "" {
		t.Fatalf("unexpected suspend outbox payload: %+v", payload1)
	}

	// UnsuspendShop: moderator rejected, ops2 succeeds and increments status_version!
	if _, err := srv.UnsuspendShop(modCtx, UnsuspendShopInput{ShopID: shopID}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("moderator UnsuspendShop: want PermissionDenied, got %v", err)
	}
	unsuspended, err := srv.UnsuspendShop(ops2FreshCtx, UnsuspendShopInput{
		ShopID: shopID,
		Reason: "da khac phuc",
	})
	if err != nil {
		t.Fatalf("UnsuspendShop: %v", err)
	}
	if unsuspended.Status != ShopStatusActive || unsuspended.SuspendedAt != nil || unsuspended.SuspendedReason != "" {
		t.Fatalf("unexpected unsuspended shop: %+v", unsuspended)
	}
	if unsuspended.StatusVersion != vSuspended+1 {
		t.Fatalf("status_version after unsuspend = %d; want %d", unsuspended.StatusVersion, vSuspended+1)
	}
	if len(store.outboxEvents) != 2 {
		t.Fatalf("expected 2 outbox events after suspend+unsuspend, got %d", len(store.outboxEvents))
	}
	var payload2 events.ShopStatusChangedPayload
	if err := json.Unmarshal(store.outboxEvents[1].PayloadJSON, &payload2); err != nil {
		t.Fatalf("unmarshal outbox payload2: %v", err)
	}
	if payload2.FromStatus != ShopStatusSuspended || payload2.ToStatus != ShopStatusActive || payload2.Version != vSuspended+1 || payload2.OccurredAt == "" {
		t.Fatalf("unexpected unsuspend outbox payload: %+v", payload2)
	}

	// ListShopSuspensionApprovals returns both approvals with filter support.
	allList, err := srv.ListShopSuspensionApprovals(modCtx, ListShopApprovalsInput{ShopID: shopID})
	if err != nil || allList.Total != 2 || len(allList.Approvals) != 2 {
		t.Fatalf("ListShopSuspensionApprovals all: err=%v res=%+v", err, allList)
	}
	appList, err := srv.ListShopSuspensionApprovals(ops1Ctx, ListShopApprovalsInput{Status: ApprovalStatusApproved, ShopID: shopID})
	if err != nil || appList.Total != 1 || appList.Approvals[0].ID != req2.ID {
		t.Fatalf("ListShopSuspensionApprovals approved: err=%v res=%+v", err, appList)
	}
}

func TestW3B2_F13_ActivityLogsCoverageNoSecretsAndRetention(t *testing.T) {
	store := newMemStore()
	sec := newMemSecurity()
	pub := &memPublisher{}
	hasher := password.NewDefaultHasher()
	authMgr, err := auth.NewManager("test-secret-w3b2-f13", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	log := logger.New("user-f13-test", logger.WithWriter(&bytes.Buffer{}))
	srv := NewServer(store, sec, hasher, authMgr, pub, log, Options{})

	const rawClientIP = "203.0.113.45"
	ipCtx := ctxWithClientIP(context.Background(), rawClientIP)

	superID := seedUserWithRole(t, srv, store, pub, "f13-super@example.com", "0901400001", RoleSuperAdmin)
	cskhID := seedUserWithRole(t, srv, store, pub, "f13-cskh@example.com", "0901400002", RoleCSKH)
	buyerID := registerActiveUser(t, srv, pub, "f13-buyer@example.com", "0901400010")

	superCtx := ctxWithClientIP(ctxWithUser(superID, RoleSuperAdmin, ""), rawClientIP)
	cskhCtx := ctxWithClientIP(ctxWithUser(cskhID, RoleCSKH, ""), rawClientIP)
	buyerCtx := ctxWithClientIP(ctxWithUser(buyerID, RoleBuyer, ""), rawClientIP)

	// 1. Failed login
	const wrongPass = "WrongSecret#999!"
	if _, err := srv.Login(ipCtx, &userv1.LoginRequest{
		Email:    "f13-buyer@example.com",
		Password: wrongPass,
	}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("failed login: want Unauthenticated, got %v", err)
	}

	// 2. Successful login
	loginResp, err := srv.Login(ipCtx, &userv1.LoginRequest{
		Email:    "f13-buyer@example.com",
		Password: "Password123!",
	})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	// 3. ForgotPassword + ResetPassword
	if _, err := srv.ForgotPassword(ipCtx, &userv1.ForgotPasswordRequest{Email: "f13-buyer@example.com"}); err != nil {
		t.Fatalf("ForgotPassword: %v", err)
	}
	resetToken := pub.passwordResetRequested[len(pub.passwordResetRequested)-1].ResetToken
	const resetNewPass = "ResetPass#2026!"
	if _, err := srv.ResetPassword(ipCtx, &userv1.ResetPasswordRequest{
		Token:       resetToken,
		NewPassword: resetNewPass,
	}); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}

	// 4. ChangePassword (after fresh login; waitPastRevokedBefore ensures fresh iat > revoked_before)
	loginAfterReset, err := srv.Login(ipCtx, &userv1.LoginRequest{
		Email:    "f13-buyer@example.com",
		Password: resetNewPass,
	})
	if err != nil {
		t.Fatalf("Login after reset: %v", err)
	}
	oldTokCtx := ctxWithClientIP(ctxWithToken(t, authMgr, loginAfterReset.GetAccessToken()), rawClientIP)
	const changedPass = "ChangedPass#2026!"
	if err := srv.ChangePassword(oldTokCtx, ChangePasswordInput{
		CurrentPassword: resetNewPass,
		NewPassword:     changedPass,
	}); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	// Old token must be immediately revoked by ChangePassword.
	if _, err := srv.GetProfile(oldTokCtx, &userv1.GetProfileRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("old token after ChangePassword: want Unauthenticated, got %v", err)
	}

	// Re-login with changed password so subsequent buyer actions carry a fresh valid token.
	loginAfterChange, err := srv.Login(ipCtx, &userv1.LoginRequest{
		Email:    "f13-buyer@example.com",
		Password: changedPass,
	})
	if err != nil {
		t.Fatalf("Login after ChangePassword: %v", err)
	}
	freshBuyerCtx := ctxWithClientIP(ctxWithToken(t, authMgr, loginAfterChange.GetAccessToken()), rawClientIP)
	_ = buyerCtx

	// 5. UpdateProfile
	if _, err := srv.UpdateProfile(freshBuyerCtx, &userv1.UpdateProfileRequest{
		FullName: "Buyer Updated Name",
		Phone:    "0901400099",
	}); err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}

	// 6. Phone OTP (request + wrong OTP + valid OTP)
	if _, err := srv.RequestPhoneOTP(freshBuyerCtx, &userv1.RequestPhoneOTPRequest{Phone: "0901400099"}); err != nil {
		t.Fatalf("RequestPhoneOTP: %v", err)
	}
	phoneOTP := pub.phoneOTPRequested[len(pub.phoneOTPRequested)-1].OTP
	wrongOTP := "000000"
	if wrongOTP == phoneOTP {
		wrongOTP = "111111"
	}
	if _, err := srv.VerifyPhoneOTP(freshBuyerCtx, &userv1.VerifyPhoneOTPRequest{Phone: "0901400099", Otp: wrongOTP}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("wrong phone OTP: want InvalidArgument, got %v", err)
	}
	if _, err := srv.VerifyPhoneOTP(freshBuyerCtx, &userv1.VerifyPhoneOTPRequest{Phone: "0901400099", Otp: phoneOTP}); err != nil {
		t.Fatalf("valid phone OTP: %v", err)
	}

	// 7. Status change & role change
	if _, err := srv.AdminUpdateUserStatus(cskhCtx, &userv1.AdminUpdateUserStatusRequest{
		TargetUserId: buyerID,
		Status:       userv1.UserStatus_USER_STATUS_LOCKED,
		Reason:       "tam khoa",
	}); err != nil {
		t.Fatalf("lock buyer: %v", err)
	}
	if _, err := srv.AdminUpdateUserStatus(cskhCtx, &userv1.AdminUpdateUserStatusRequest{
		TargetUserId: buyerID,
		Status:       userv1.UserStatus_USER_STATUS_ACTIVE,
	}); err != nil {
		t.Fatalf("unlock buyer: %v", err)
	}
	if _, err := srv.UpdateUserRole(superCtx, UpdateUserRoleInput{
		TargetUserID: buyerID,
		Role:         RoleSeller,
		Reason:       "promote to seller",
	}); err != nil {
		t.Fatalf("UpdateUserRole: %v", err)
	}

	// ListUserActivities: buyer denied, cskh (staff) allowed.
	loginAsSeller, err := srv.Login(ipCtx, &userv1.LoginRequest{
		Email:    "f13-buyer@example.com",
		Password: changedPass,
	})
	if err != nil {
		t.Fatalf("Login after role update: %v", err)
	}
	if _, err := srv.ListUserActivities(ctxWithToken(t, authMgr, loginAsSeller.GetAccessToken()), ListUserActivitiesInput{UserID: buyerID}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("seller ListUserActivities: want PermissionDenied, got %v", err)
	}
	listRes, err := srv.ListUserActivities(cskhCtx, ListUserActivitiesInput{
		UserID:   buyerID,
		Page:     1,
		PageSize: 100,
	})
	if err != nil {
		t.Fatalf("staff ListUserActivities: %v", err)
	}

	seenEvents := make(map[string]int)
	secrets := []string{
		rawClientIP,
		"Password123!",
		wrongPass,
		resetNewPass,
		changedPass,
		resetToken,
		phoneOTP,
		wrongOTP,
		loginResp.GetAccessToken(),
		loginResp.GetRefreshToken(),
		"0901400099",
	}
	for _, entry := range listRes.Entries {
		seenEvents[entry.EventType]++
		if entry.IPAddress != "203.0.113.xxx" {
			t.Fatalf("entry %s IPAddress = %q; want 203.0.113.xxx", entry.EventType, entry.IPAddress)
		}
		for _, secret := range secrets {
			if secret != "" && (strings.Contains(entry.MetadataJSON, secret) || strings.Contains(entry.IPAddress, secret)) {
				t.Fatalf("activity log %s leaked secret %q: %+v", entry.EventType, secret, entry)
			}
		}
	}

	wantEvents := []string{
		ActivityEventLoginFailed,
		ActivityEventLoginSucceeded,
		ActivityEventPasswordResetRequested,
		ActivityEventPasswordResetCompleted,
		ActivityEventPasswordChanged,
		ActivityEventProfileUpdated,
		ActivityEventPhoneOTPRequested,
		ActivityEventPhoneOTPFailed,
		ActivityEventPhoneOTPVerified,
		ActivityEventStatusChanged,
		ActivityEventRoleChanged,
	}
	for _, ev := range wantEvents {
		if seenEvents[ev] == 0 {
			t.Fatalf("missing expected activity event %q; seen=%v", ev, seenEvents)
		}
	}

	// Category filter works.
	phoneOnly, err := srv.ListUserActivities(cskhCtx, ListUserActivitiesInput{
		UserID:   buyerID,
		Category: ActivityCategoryPhone,
	})
	if err != nil || phoneOnly.Total != 3 {
		t.Fatalf("phone category filter: want total=3, got err=%v total=%d", err, phoneOnly.Total)
	}

	// Retention job with fake clock: insert 3 old (>180d) and 1 boundary (exactly 180d) rows.
	retClk := newW2BClock(time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC))
	retSrv := NewServer(store, sec, hasher, authMgr, pub, log, Options{Now: retClk.Now})
	now := retClk.Now()
	for i := 0; i < 3; i++ {
		_, _ = store.InsertUserActivityLog(context.Background(), db.InsertUserActivityLogParams{
			ID:           uuid.NewString(),
			UserID:       buyerID,
			EventType:    ActivityEventLoginSucceeded,
			Category:     ActivityCategoryAuth,
			IPAddress:    "203.0.113.xxx",
			MetadataJSON: "{}",
			CreatedAt:    now.Add(-ActivityLogRetention - time.Duration(i+1)*time.Hour),
		})
	}
	boundaryID := uuid.NewString()
	_, _ = store.InsertUserActivityLog(context.Background(), db.InsertUserActivityLogParams{
		ID:           boundaryID,
		UserID:       buyerID,
		EventType:    ActivityEventLoginSucceeded,
		Category:     ActivityCategoryAuth,
		IPAddress:    "203.0.113.xxx",
		MetadataJSON: "{}",
		CreatedAt:    now.Add(-ActivityLogRetention),
	})

	deleted, err := retSrv.PurgeExpiredUserActivities(context.Background(), 2)
	if err != nil {
		t.Fatalf("PurgeExpiredUserActivities: %v", err)
	}
	if deleted != 3 {
		t.Fatalf("PurgeExpiredUserActivities deleted = %d; want 3", deleted)
	}
	// Boundary row (exactly 180 days) must still exist.
	foundBoundary := false
	for _, entry := range store.userActivityLogs {
		if entry.ID == boundaryID {
			foundBoundary = true
			break
		}
	}
	if !foundBoundary {
		t.Fatal("retention job deleted row at exact 180-day boundary; only strictly older rows should be deleted")
	}
}

func TestW3B2_ReviewFix_NoSuperAdminAssignAndStaffResetActivity(t *testing.T) {
	t.Run("UpdateUserRole_RejectsSuperAdminAssignment", func(t *testing.T) {
		srv, store, _, pub, _ := newTestHarness(t)
		superID := seedUserWithRole(t, srv, store, pub, "rev-super@example.com", "0908100001", RoleSuperAdmin)
		adminID := seedUserWithRole(t, srv, store, pub, "rev-admin@example.com", "0908100002", RoleAdmin)
		opsID := seedUserWithRole(t, srv, store, pub, "rev-ops@example.com", "0908100003", RoleOps)
		buyerID := seedUserWithRole(t, srv, store, pub, "rev-buyer@example.com", "0908100004", RoleBuyer)
		superCtx := ctxWithUser(superID, RoleSuperAdmin, "")

		for _, tc := range []struct {
			name string
			id   string
			role string
		}{
			{"buyer", buyerID, RoleBuyer},
			{"ops", opsID, RoleOps},
			{"admin", adminID, RoleAdmin},
		} {
			if _, err := srv.UpdateUserRole(superCtx, UpdateUserRoleInput{
				TargetUserID: tc.id,
				Role:         RoleSuperAdmin,
				Reason:       "attempt grant super_admin",
			}); status.Code(err) != codes.PermissionDenied {
				t.Errorf("UpdateUserRole(%s -> super_admin): want PermissionDenied, got %v", tc.name, err)
			}
			if _, err := srv.AdminUpdateUserRole(superCtx, &userv1.AdminUpdateUserRoleRequest{
				TargetUserId: tc.id,
				Role:         userv1.UserRole_USER_ROLE_SUPER_ADMIN,
				Reason:       "attempt grant super_admin via rpc",
			}); status.Code(err) != codes.PermissionDenied {
				t.Errorf("AdminUpdateUserRole(%s -> USER_ROLE_SUPER_ADMIN): want PermissionDenied, got %v", tc.name, err)
			}
			u, err := store.GetUserByID(context.Background(), tc.id)
			if err != nil || u.Role != tc.role {
				t.Errorf("target %s role mutated after rejected super_admin grant: got %q (want %q), err=%v", tc.name, u.Role, tc.role, err)
			}
		}
	})

	t.Run("CreateStaffUser_ResetActivityMatchesPublishOutcome", func(t *testing.T) {
		srv, store, _, pub, _ := newTestHarness(t)
		superID := seedUserWithRole(t, srv, store, pub, "rev-staff-super@example.com", "0908100010", RoleSuperAdmin)
		superCtx := ctxWithClientIP(ctxWithUser(superID, RoleSuperAdmin, ""), "198.51.100.42")

		// Case A: PublishPasswordResetRequested fails -> MUST record password_reset_failed and MUST NOT record password_reset_requested.
		pub.mu.Lock()
		pub.publishPasswordResetErr = errors.New("nats unavailable")
		pub.mu.Unlock()

		failRes, err := srv.CreateStaffUser(superCtx, CreateStaffUserInput{
			FullName: "Fail Mail Staff",
			Email:    "rev-staff-fail@example.com",
			Role:     RoleModerator,
		})
		if err != nil {
			t.Fatalf("CreateStaffUser (publish failure): unexpected err %v", err)
		}
		if failRes.PasswordResetSent {
			t.Fatalf("CreateStaffUser (publish failure): want PasswordResetSent=false, got true")
		}

		failEvents := map[string]int{}
		store.mu.Lock()
		for _, entry := range store.userActivityLogs {
			if entry.UserID == failRes.User.ID {
				failEvents[entry.EventType]++
				if entry.IPAddress != "198.51.100.xxx" {
					t.Errorf("expected masked IP 198.51.100.xxx, got %q", entry.IPAddress)
				}
			}
		}
		var failResetUsed bool
		for _, pr := range store.passwordResets {
			if pr.UserID == failRes.User.ID && pr.UsedAt != nil {
				failResetUsed = true
			}
		}
		store.mu.Unlock()

		if failEvents[ActivityEventStaffCreated] != 1 {
			t.Errorf("publish failure: want 1 staff_created, got %d", failEvents[ActivityEventStaffCreated])
		}
		if failEvents[ActivityEventPasswordResetRequested] != 0 {
			t.Errorf("publish failure: must NOT record password_reset_requested, got %d", failEvents[ActivityEventPasswordResetRequested])
		}
		if failEvents["password_reset_failed"] != 1 {
			t.Errorf("publish failure: want 1 password_reset_failed, got %d", failEvents["password_reset_failed"])
		}
		if !failResetUsed {
			t.Errorf("publish failure: expected undelivered password reset token to be invalidated")
		}

		// Case B: PublishPasswordResetRequested succeeds -> MUST record password_reset_requested and MUST NOT record password_reset_failed.
		pub.mu.Lock()
		pub.publishPasswordResetErr = nil
		pub.mu.Unlock()

		okRes, err := srv.CreateStaffUser(superCtx, CreateStaffUserInput{
			FullName: "Ok Mail Staff",
			Email:    "rev-staff-ok@example.com",
			Role:     RoleOps,
		})
		if err != nil {
			t.Fatalf("CreateStaffUser (publish ok): %v", err)
		}
		if !okRes.PasswordResetSent {
			t.Fatalf("CreateStaffUser (publish ok): want PasswordResetSent=true, got false")
		}

		okEvents := map[string]int{}
		store.mu.Lock()
		for _, entry := range store.userActivityLogs {
			if entry.UserID == okRes.User.ID {
				okEvents[entry.EventType]++
			}
		}
		store.mu.Unlock()

		if okEvents[ActivityEventStaffCreated] != 1 {
			t.Errorf("publish ok: want 1 staff_created, got %d", okEvents[ActivityEventStaffCreated])
		}
		if okEvents[ActivityEventPasswordResetRequested] != 1 {
			t.Errorf("publish ok: want 1 password_reset_requested, got %d", okEvents[ActivityEventPasswordResetRequested])
		}
		if okEvents["password_reset_failed"] != 0 {
			t.Errorf("publish ok: must NOT record password_reset_failed, got %d", okEvents["password_reset_failed"])
		}
	})

	t.Run("DeleteAddress_BlocksActiveSuspendedPendingAllowsRejected", func(t *testing.T) {
		srv, store, _, pub, _ := newTestHarness(t)
		now := time.Now().UTC()

		for i, tc := range []struct {
			status      string
			wantBlocked bool
		}{
			{ShopStatusActive, true},
			{ShopStatusSuspended, true},
			{ShopStatusPending, true},
			{ShopStatusRejected, false},
		} {
			uid := registerActiveUser(t, srv, pub, "rev-pickup-"+strings.ToLower(tc.status)+"@example.com", "")
			uCtx := ctxWithUser(uid, RoleSeller, "")
			addrResp, err := srv.CreateAddress(uCtx, &userv1.CreateAddressRequest{
				RecipientName:  "Kho " + tc.status,
				RecipientPhone: "090810002" + string(rune('1'+i)),
				ProvinceCode:   "79",
				ProvinceName:   "TP. Ho Chi Minh",
				WardCode:       "26734",
				WardName:       "Phuong Ben Nghe",
				StreetAddress:  "123 Duong Kho",
				IsDefault:      true,
			})
			if err != nil {
				t.Fatalf("CreateAddress(%s): %v", tc.status, err)
			}
			addrID := addrResp.GetAddress().GetId()
			shopID := uuid.NewString()
			store.mu.Lock()
			store.shops[shopID] = db.Shop{
				ID:              shopID,
				OwnerUserID:     uid,
				Name:            "Shop " + tc.status,
				Slug:            "shop-rev-" + strings.ToLower(tc.status),
				PickupAddressID: addrID,
				Status:          tc.status,
				StatusVersion:   1,
				CreatedAt:       now,
				UpdatedAt:       now,
			}
			store.mu.Unlock()

			_, delErr := srv.DeleteAddress(uCtx, &userv1.DeleteAddressRequest{AddressId: addrID})
			if tc.wantBlocked {
				if status.Code(delErr) != codes.FailedPrecondition {
					t.Errorf("DeleteAddress for %s shop pickup: want FailedPrecondition, got %v", tc.status, delErr)
				}
			} else if delErr != nil {
				t.Errorf("DeleteAddress for %s shop pickup: want nil, got %v", tc.status, delErr)
			}
		}
	})
}
