package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/user/internal/db"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TestW3B2_F9_RoleCapabilityMatrixAllAdminRPCs tests all 6 internal staff roles plus buyer,
// seller, and internal service tokens against every admin RPC in UserService (positive + negative).
func TestW3B2_F9_RoleCapabilityMatrixAllAdminRPCs(t *testing.T) {
	srv, store, _, pub, _ := newTestHarness(t)

	allRoles := []string{
		RoleSuperAdmin,
		RoleAdmin,
		RoleModerator,
		RoleCSKH,
		RoleAccountant,
		RoleOps,
		RoleBuyer,
		RoleSeller,
	}

	actorIDs := make(map[string]string, len(allRoles))
	for i, role := range allRoles {
		phone := "09055000" + string(rune('1'+i)) + "0"
		actorIDs[role] = seedUserWithRole(t, srv, store, pub, "matrix-"+role+"@example.com", phone, role)
	}

	buyerTargetID := seedUserWithRole(t, srv, store, pub, "matrix-target-buyer@example.com", "0905500101", RoleBuyer)
	_, activeShopID := seedActiveShopMem(t, srv, store, pub, "matrix-shop-owner@example.com", "0905500102", "Matrix Active Shop")
	_, bankShopID := seedActiveShopMem(t, srv, store, pub, "matrix-bank-owner@example.com", "0905500103", "Matrix Bank Shop")

	// Seed a PENDING_ADMIN_REVIEW bank account on bankShopID.
	now := time.Now().UTC()
	encNum := []byte("enc-matrix-test")
	seedPendingBank := func() string {
		id := uuid.NewString()
		store.mu.Lock()
		store.shopBankAccounts[id] = db.ShopBankAccount{
			ID:                  id,
			ShopID:              bankShopID,
			BankCode:            "VCB",
			BankName:            "Vietcombank",
			AccountNumberEnc:    encNum,
			AccountNumberMasked: "****4567",
			AccountHolderName:   "CONG TY MATRIX",
			HolderNameMatched:   false,
			Status:              BankAccountStatusPendingAdminReview,
			CreatedAt:           now,
			UpdatedAt:           now,
		}
		store.mu.Unlock()
		return id
	}
	_ = seedPendingBank()

	// Helper to seed a fresh PENDING shop for AdminReviewShop positive checks.
	seedPendingShop := func(tag string) string {
		uid := registerActiveUser(t, srv, pub, "matrix-pend-"+tag+"@example.com", "")
		shID := uuid.NewString()
		store.mu.Lock()
		store.shops[shID] = db.Shop{
			ID:            shID,
			OwnerUserID:   uid,
			Name:          "Matrix Pending " + tag,
			Slug:          "matrix-pending-" + tag,
			Status:        ShopStatusPending,
			StatusVersion: 1,
			CreatedAt:     now,
			UpdatedAt:     now,
		}
		store.mu.Unlock()
		return shID
	}

	// Helper to seed a fresh ACTIVE or SUSPENDED shop and optionally a PENDING approval requested by another maker.
	seedShopWithStatus := func(tag, st string) string {
		uid := registerActiveUser(t, srv, pub, "matrix-st-"+tag+"@example.com", "")
		shID := uuid.NewString()
		var susAt *time.Time
		var susReason string
		if st == ShopStatusSuspended {
			t0 := now
			susAt = &t0
			susReason = "seeded suspended"
		}
		store.mu.Lock()
		store.shops[shID] = db.Shop{
			ID:              shID,
			OwnerUserID:     uid,
			Name:            "Matrix Shop " + tag,
			Slug:            "matrix-shop-" + tag,
			Status:          st,
			StatusVersion:   1,
			SuspendedAt:     susAt,
			SuspendedReason: susReason,
			CreatedAt:       now,
			UpdatedAt:       now,
		}
		store.mu.Unlock()
		return shID
	}

	type rpcCase struct {
		name       string
		capability string
		invoke     func(ctx context.Context, roleTag string) error
	}

	cases := []rpcCase{
		{
			name:       "AdminListUsers",
			capability: auth.CapUsersRead,
			invoke: func(ctx context.Context, _ string) error {
				_, err := srv.AdminListUsers(ctx, &userv1.AdminListUsersRequest{Page: 1, PageSize: 10})
				return err
			},
		},
		{
			name:       "AdminListUserActivities",
			capability: auth.CapUsersRead,
			invoke: func(ctx context.Context, _ string) error {
				_, err := srv.AdminListUserActivities(ctx, &userv1.AdminListUserActivitiesRequest{UserId: buyerTargetID, Page: 1, PageSize: 10})
				return err
			},
		},
		{
			name:       "AdminUpdateUserStatus",
			capability: auth.CapUsersStatusWrite,
			invoke: func(ctx context.Context, tag string) error {
				tid := seedUserWithRole(t, srv, store, pub, "matrix-stat-"+tag+"@example.com", "", RoleBuyer)
				_, err := srv.AdminUpdateUserStatus(ctx, &userv1.AdminUpdateUserStatusRequest{
					TargetUserId: tid,
					Status:       userv1.UserStatus_USER_STATUS_LOCKED,
					Reason:       "matrix test",
				})
				return err
			},
		},
		{
			name:       "AdminUpdateUserRole",
			capability: auth.CapUsersRoleWrite,
			invoke: func(ctx context.Context, tag string) error {
				tid := seedUserWithRole(t, srv, store, pub, "matrix-role-"+tag+"@example.com", "", RoleBuyer)
				_, err := srv.AdminUpdateUserRole(ctx, &userv1.AdminUpdateUserRoleRequest{
					TargetUserId: tid,
					Role:         userv1.UserRole_USER_ROLE_MODERATOR,
					Reason:       "matrix role test",
				})
				return err
			},
		},
		{
			name:       "AdminCreateStaffUser",
			capability: auth.CapStaffCreate,
			invoke: func(ctx context.Context, tag string) error {
				_, err := srv.AdminCreateStaffUser(ctx, &userv1.AdminCreateStaffUserRequest{
					FullName: "Staff " + tag,
					Email:    "matrix-newstaff-" + tag + "@example.com",
					Role:     userv1.UserRole_USER_ROLE_OPS,
				})
				return err
			},
		},
		{
			name:       "AdminListShops",
			capability: auth.CapShopsRead,
			invoke: func(ctx context.Context, _ string) error {
				_, err := srv.AdminListShops(ctx, &userv1.AdminListShopsRequest{Page: 1, PageSize: 10})
				return err
			},
		},
		{
			name:       "AdminReviewShop",
			capability: auth.CapShopsReview,
			invoke: func(ctx context.Context, tag string) error {
				shID := seedPendingShop(tag)
				_, err := srv.AdminReviewShop(ctx, &userv1.AdminReviewShopRequest{
					ShopId:   shID,
					Decision: userv1.ReviewDecision_REVIEW_DECISION_APPROVE,
				})
				return err
			},
		},
		{
			name:       "AdminRequestShopSuspension",
			capability: auth.CapShopsSuspendRequest,
			invoke: func(ctx context.Context, tag string) error {
				shID := seedShopWithStatus("req-"+tag, ShopStatusActive)
				_, err := srv.AdminRequestShopSuspension(ctx, &userv1.AdminRequestShopSuspensionRequest{
					ShopId: shID,
					Reason: "matrix suspend request",
				})
				return err
			},
		},
		{
			name:       "AdminListShopApprovals",
			capability: auth.CapShopsApprovalsRead,
			invoke: func(ctx context.Context, _ string) error {
				_, err := srv.AdminListShopApprovals(ctx, &userv1.AdminListShopApprovalsRequest{Page: 1, PageSize: 10})
				return err
			},
		},
		{
			name:       "AdminDecideShopSuspension",
			capability: auth.CapShopsSuspendDecide,
			invoke: func(ctx context.Context, tag string) error {
				shID := seedShopWithStatus("dec-"+tag, ShopStatusActive)
				// Seed pending approval created by a distinct maker ID so checker != maker.
				appID := uuid.NewString()
				store.mu.Lock()
				store.shopSuspensionApprovals[appID] = db.ShopSuspensionApproval{
					ID:            appID,
					ShopID:        shID,
					Status:        ApprovalStatusPending,
					Reason:        "matrix decide test",
					RequestedBy:   uuid.NewString(),
					RequestedRole: RoleModerator,
					CreatedAt:     now,
				}
				store.mu.Unlock()
				_, err := srv.AdminDecideShopSuspension(ctx, &userv1.AdminDecideShopSuspensionRequest{
					ApprovalId: appID,
					Decision:   userv1.ReviewDecision_REVIEW_DECISION_APPROVE,
					Note:       "ok",
				})
				return err
			},
		},
		{
			name:       "AdminUnsuspendShop",
			capability: auth.CapShopsUnsuspend,
			invoke: func(ctx context.Context, tag string) error {
				shID := seedShopWithStatus("uns-"+tag, ShopStatusSuspended)
				_, err := srv.AdminUnsuspendShop(ctx, &userv1.AdminUnsuspendShopRequest{
					ShopId: shID,
					Reason: "matrix unsuspend",
				})
				return err
			},
		},
		{
			name:       "GetShopBankAccount_Admin",
			capability: auth.CapShopsBankRead,
			invoke: func(ctx context.Context, _ string) error {
				_, err := srv.GetShopBankAccount(ctx, &userv1.GetShopBankAccountRequest{ShopId: bankShopID})
				return err
			},
		},
		{
			name:       "AdminReviewShopBankAccount",
			capability: auth.CapShopsBankReview,
			invoke: func(ctx context.Context, _ string) error {
				accID := seedPendingBank()
				_, err := srv.AdminReviewShopBankAccount(ctx, &userv1.AdminReviewShopBankAccountRequest{
					ShopId:        bankShopID,
					BankAccountId: accID,
					Decision:      userv1.ReviewDecision_REVIEW_DECISION_REJECT,
					Reason:        "matrix reject",
				})
				return err
			},
		},
		{
			name:       "ListAuditLogs",
			capability: auth.CapAuditLogsRead,
			invoke: func(ctx context.Context, _ string) error {
				_, err := srv.ListAuditLogs(ctx, &userv1.ListAuditLogsRequest{Page: 1, PageSize: 10})
				return err
			},
		},
		{
			name:       "GetPlatformUserStats",
			capability: auth.CapReportsRead,
			invoke: func(ctx context.Context, _ string) error {
				_, err := srv.GetPlatformUserStats(ctx, &userv1.GetPlatformUserStatsRequest{
					FromDate: "2026-11-24",
					ToDate:   "2026-11-24",
				})
				return err
			},
		},
	}

	_ = activeShopID

	for _, rc := range cases {
		for _, role := range allRoles {
			wantAllowed := auth.RoleHasCapability(role, rc.capability)
			tag := rc.name + "-" + role
			ctx := ctxWithUser(actorIDs[role], role, "")
			err := rc.invoke(ctx, tag)
			if wantAllowed {
				if err != nil {
					t.Errorf("%s as %s (cap=%s): want OK, got %v", rc.name, role, rc.capability, err)
				}
			} else {
				if status.Code(err) != codes.PermissionDenied {
					t.Errorf("%s as %s (cap=%s): want PermissionDenied, got %v", rc.name, role, rc.capability, err)
				}
			}
		}

		// Internal service tokens MUST satisfy zero admin capabilities (HasCapability rejects Service != "").
		svcErr := rc.invoke(ctxWithService("order-service"), rc.name+"-svc")
		if status.Code(svcErr) != codes.PermissionDenied {
			t.Errorf("%s with service token: want PermissionDenied, got %v", rc.name, svcErr)
		}
	}
}

// TestW3B2_ListAuditLogs_ScopedRoles enforces §2.1 † / contracts.md §4.11:
// moderator/cskh/accountant default to actor_id=me and get PermissionDenied if querying another actor.
func TestW3B2_ListAuditLogs_ScopedRoles(t *testing.T) {
	srv, store, _, pub, _ := newTestHarness(t)

	roles := []string{RoleSuperAdmin, RoleAdmin, RoleOps, RoleModerator, RoleCSKH, RoleAccountant}
	ids := make(map[string]string, len(roles))
	for i, r := range roles {
		ids[r] = seedUserWithRole(t, srv, store, pub, "audit-scope-"+r+"@example.com", "", r)
		_, _ = store.InsertAuditLog(context.Background(), db.InsertAuditLogParams{
			ID:         uuid.NewString(),
			ActorID:    ids[r],
			ActorRole:  r,
			Action:     "test.action." + r,
			TargetType: "user",
			TargetID:   uuid.NewString(),
			BeforeJSON: "{}",
			AfterJSON:  "{}",
			Reason:     "seed " + string(rune('a'+i)),
			IPAddress:  "127.0.0.1",
		})
	}

	// Unrestricted roles (super_admin, admin, ops) see all 6 logs and can query any actor_id.
	for _, r := range []string{RoleSuperAdmin, RoleAdmin, RoleOps} {
		ctx := ctxWithUser(ids[r], r, "")
		allResp, err := srv.ListAuditLogs(ctx, &userv1.ListAuditLogsRequest{Page: 1, PageSize: 50})
		if err != nil || allResp.GetTotal() != int64(len(roles)) {
			t.Fatalf("%s ListAuditLogs all: want total=%d, got err=%v total=%d", r, len(roles), err, allResp.GetTotal())
		}
		otherResp, err := srv.ListAuditLogs(ctx, &userv1.ListAuditLogsRequest{ActorId: ids[RoleCSKH]})
		if err != nil || otherResp.GetTotal() != 1 || otherResp.GetLogs()[0].GetActorId() != ids[RoleCSKH] {
			t.Fatalf("%s ListAuditLogs(cskh): err=%v resp=%+v", r, err, otherResp)
		}
	}

	// Scoped roles (moderator, cskh, accountant): empty actor_id returns ONLY own logs; another actor_id -> PermissionDenied!
	for _, r := range []string{RoleModerator, RoleCSKH, RoleAccountant} {
		ctx := ctxWithUser(ids[r], r, "")
		ownDefault, err := srv.ListAuditLogs(ctx, &userv1.ListAuditLogsRequest{Page: 1, PageSize: 50})
		if err != nil {
			t.Fatalf("%s ListAuditLogs default: %v", r, err)
		}
		if ownDefault.GetTotal() != 1 || len(ownDefault.GetLogs()) != 1 || ownDefault.GetLogs()[0].GetActorId() != ids[r] {
			t.Fatalf("%s ListAuditLogs default leaked other actors: total=%d logs=%+v", r, ownDefault.GetTotal(), ownDefault.GetLogs())
		}

		ownExplicit, err := srv.ListAuditLogs(ctx, &userv1.ListAuditLogsRequest{ActorId: ids[r]})
		if err != nil || ownExplicit.GetTotal() != 1 {
			t.Fatalf("%s ListAuditLogs(self): err=%v total=%d", r, err, ownExplicit.GetTotal())
		}

		if _, err := srv.ListAuditLogs(ctx, &userv1.ListAuditLogsRequest{ActorId: ids[RoleSuperAdmin]}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("%s ListAuditLogs(super_admin): want PermissionDenied, got %v", r, err)
		}
	}
}

// TestW3B2_GRPC_HandlersRoleMappingAndDBDemotionAttack verifies the 6 new gRPC handlers end-to-end,
// all 8 proto UserRole mappings, Shop suspension proto fields, Logout activity log, and DB-demotion attack.
func TestW3B2_GRPC_HandlersRoleMappingAndDBDemotionAttack(t *testing.T) {
	srv, store, _, pub, authMgr := newTestHarness(t)
	ctx := context.Background()

	superID := seedUserWithRole(t, srv, store, pub, "grpc-super@example.com", "0906600001", RoleSuperAdmin)
	superCtx := ctxWithClientIP(ctxWithUser(superID, RoleSuperAdmin, ""), "198.51.100.77")

	// 1. AdminCreateStaffUser creates all 4 new roles without password & returns exact proto UserRole.
	rolePairs := []struct {
		proto userv1.UserRole
		str   string
	}{
		{userv1.UserRole_USER_ROLE_MODERATOR, RoleModerator},
		{userv1.UserRole_USER_ROLE_CSKH, RoleCSKH},
		{userv1.UserRole_USER_ROLE_ACCOUNTANT, RoleAccountant},
		{userv1.UserRole_USER_ROLE_OPS, RoleOps},
	}
	createdIDs := make(map[string]string, len(rolePairs))
	for _, rp := range rolePairs {
		resp, err := srv.AdminCreateStaffUser(superCtx, &userv1.AdminCreateStaffUserRequest{
			FullName: "Staff " + rp.str,
			Email:    "grpc-staff-" + rp.str + "@example.com",
			Role:     rp.proto,
		})
		if err != nil {
			t.Fatalf("AdminCreateStaffUser(%s): %v", rp.str, err)
		}
		if !resp.GetPasswordResetSent() || resp.GetUser().GetRole() != rp.proto {
			t.Fatalf("AdminCreateStaffUser(%s) mismatch: resp=%+v", rp.str, resp)
		}
		createdIDs[rp.str] = resp.GetUser().GetId()
	}

	// AdminUpdateUserRole round-trips new proto roles (e.g. cskh -> accountant -> ops).
	updRole, err := srv.AdminUpdateUserRole(superCtx, &userv1.AdminUpdateUserRoleRequest{
		TargetUserId: createdIDs[RoleCSKH],
		Role:         userv1.UserRole_USER_ROLE_ACCOUNTANT,
		Reason:       "cross-train",
	})
	if err != nil || updRole.GetUser().GetRole() != userv1.UserRole_USER_ROLE_ACCOUNTANT {
		t.Fatalf("AdminUpdateUserRole to ACCOUNTANT: err=%v resp=%+v", err, updRole)
	}

	// 2. Shop suspension flow via gRPC handlers + proto fields + DB-demotion attack (F11).
	_, shopID := seedActiveShopMem(t, srv, store, pub, "grpc-seller@example.com", "0906600010", "GRPC Proto Shop")
	modCtx := ctxWithUser(createdIDs[RoleModerator], RoleModerator, "")
	opsCtx := ctxWithUser(createdIDs[RoleOps], RoleOps, "")

	reqResp, err := srv.AdminRequestShopSuspension(modCtx, &userv1.AdminRequestShopSuspensionRequest{
		ShopId: shopID,
		Reason: "vi pham ban hang",
	})
	if err != nil {
		t.Fatalf("AdminRequestShopSuspension: %v", err)
	}
	if reqResp.GetApproval().GetShopName() != "GRPC Proto Shop" ||
		reqResp.GetApproval().GetStatus() != userv1.ShopApprovalStatus_SHOP_APPROVAL_STATUS_PENDING {
		t.Fatalf("unexpected approval response: %+v", reqResp.GetApproval())
	}
	appID := reqResp.GetApproval().GetId()

	// Invalid decision enum rejected with InvalidArgument.
	if _, err := srv.AdminDecideShopSuspension(opsCtx, &userv1.AdminDecideShopSuspensionRequest{
		ApprovalId: appID,
		Decision:   userv1.ReviewDecision_REVIEW_DECISION_REQUEST_INFO,
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("REQUEST_INFO decision: want InvalidArgument, got %v", err)
	}

	// DB-demotion attack: ops user is downgraded in DB to moderator without revoking existing JWT claims -> PermissionDenied!
	store.mu.Lock()
	opsUser := store.users[createdIDs[RoleOps]]
	opsUser.Role = RoleModerator
	store.users[createdIDs[RoleOps]] = opsUser
	store.mu.Unlock()

	if _, err := srv.AdminDecideShopSuspension(opsCtx, &userv1.AdminDecideShopSuspensionRequest{
		ApprovalId: appID,
		Decision:   userv1.ReviewDecision_REVIEW_DECISION_APPROVE,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("DB-demoted ops approving with stale claims: want PermissionDenied, got %v", err)
	}

	// Restore ops in DB and approve -> GetShop & AdminListShops expose suspended_reason, suspended_at, status_version!
	store.mu.Lock()
	opsUser.Role = RoleOps
	store.users[createdIDs[RoleOps]] = opsUser
	store.mu.Unlock()

	decResp, err := srv.AdminDecideShopSuspension(opsCtx, &userv1.AdminDecideShopSuspensionRequest{
		ApprovalId: appID,
		Decision:   userv1.ReviewDecision_REVIEW_DECISION_APPROVE,
		Note:       "xac nhan khoa",
	})
	if err != nil {
		t.Fatalf("AdminDecideShopSuspension approve: %v", err)
	}
	if decResp.GetApproval().GetShopName() != "GRPC Proto Shop" ||
		decResp.GetApproval().GetStatus() != userv1.ShopApprovalStatus_SHOP_APPROVAL_STATUS_APPROVED ||
		decResp.GetApproval().GetDecidedBy() != createdIDs[RoleOps] ||
		decResp.GetShop().GetSuspendedReason() != "vi pham ban hang" ||
		decResp.GetShop().GetSuspendedAt() == nil ||
		decResp.GetShop().GetStatusVersion() < 2 {
		t.Fatalf("unexpected decide response: approval=%+v shop=%+v", decResp.GetApproval(), decResp.GetShop())
	}

	// Anonymous GetShop MUST hide suspended shop (NotFound); staff with shops.read and internal service callers see suspension fields.
	if _, err := srv.GetShop(ctx, &userv1.GetShopRequest{ShopId: shopID}); status.Code(err) != codes.NotFound {
		t.Fatalf("anonymous GetShop on suspended shop: want NotFound, got %v", err)
	}
	gotShop, err := srv.GetShop(opsCtx, &userv1.GetShopRequest{ShopId: shopID})
	if err != nil || gotShop.GetShop().GetSuspendedReason() != "vi pham ban hang" ||
		gotShop.GetShop().GetSuspendedAt() == nil ||
		gotShop.GetShop().GetStatusVersion() != decResp.GetShop().GetStatusVersion() {
		t.Fatalf("admin GetShop missing suspension fields: err=%v shop=%+v", err, gotShop.GetShop())
	}
	gotInternalShop, err := srv.GetShopInternal(ctxWithService("order-service"), &userv1.GetShopInternalRequest{ShopId: shopID})
	if err != nil || gotInternalShop.GetShop().GetSuspendedReason() != "vi pham ban hang" ||
		gotInternalShop.GetShop().GetSuspendedAt() == nil ||
		gotInternalShop.GetShop().GetStatusVersion() != decResp.GetShop().GetStatusVersion() {
		t.Fatalf("GetShopInternal missing suspension fields: err=%v shop=%+v", err, gotInternalShop.GetShop())
	}

	// AdminListShopApprovals populates shop_name.
	listApps, err := srv.AdminListShopApprovals(modCtx, &userv1.AdminListShopApprovalsRequest{
		Status: userv1.ShopApprovalStatus_SHOP_APPROVAL_STATUS_APPROVED,
		ShopId: shopID,
	})
	if err != nil || listApps.GetTotal() != 1 || listApps.GetItems()[0].GetShopName() != "GRPC Proto Shop" {
		t.Fatalf("AdminListShopApprovals mismatch: err=%v resp=%+v", err, listApps)
	}

	// Unsuspend via gRPC handler increments status_version and clears suspension fields.
	unsusp, err := srv.AdminUnsuspendShop(opsCtx, &userv1.AdminUnsuspendShopRequest{
		ShopId: shopID,
		Reason: "mo lai",
	})
	if err != nil || unsusp.GetShop().GetStatus() != userv1.ShopStatus_SHOP_STATUS_ACTIVE ||
		unsusp.GetShop().GetSuspendedReason() != "" ||
		unsusp.GetShop().GetSuspendedAt() != nil ||
		unsusp.GetShop().GetStatusVersion() != decResp.GetShop().GetStatusVersion()+1 {
		t.Fatalf("AdminUnsuspendShop mismatch: err=%v resp=%+v", err, unsusp)
	}

	// 3. Logout writes activity log (masked IP, no token) and AdminListUserActivities filters + paginates.
	buyerID := registerActiveUser(t, srv, pub, "grpc-act-buyer@example.com", "0906600099")
	loginResp, err := srv.Login(ctxWithClientIP(ctx, "203.0.113.55"), &userv1.LoginRequest{
		Email:    "grpc-act-buyer@example.com",
		Password: "Password123!",
	})
	if err != nil {
		t.Fatalf("Login buyer: %v", err)
	}
	logoutCtx := ctxWithClientIP(ctxWithToken(t, authMgr, loginResp.GetAccessToken()), "203.0.113.55")
	if _, err := srv.Logout(logoutCtx, &userv1.LogoutRequest{RefreshToken: loginResp.GetRefreshToken()}); err != nil {
		t.Fatalf("Logout buyer: %v", err)
	}

	fromTS := timestamppb.New(time.Now().UTC().Add(-time.Minute))
	toTS := timestamppb.New(time.Now().UTC().Add(time.Minute))
	actResp, err := srv.AdminListUserActivities(superCtx, &userv1.AdminListUserActivitiesRequest{
		UserId:   buyerID,
		Category: ActivityCategoryAuth,
		FromTime: fromTS,
		ToTime:   toTS,
		Page:     1,
		PageSize: 1,
	})
	if err != nil || actResp.GetTotal() < 2 || len(actResp.GetItems()) != 1 {
		t.Fatalf("AdminListUserActivities pagination: err=%v total=%d len=%d", err, actResp.GetTotal(), len(actResp.GetItems()))
	}
	allAct, err := srv.AdminListUserActivities(superCtx, &userv1.AdminListUserActivitiesRequest{
		UserId:   buyerID,
		Category: ActivityCategoryAuth,
		Page:     1,
		PageSize: 10,
	})
	if err != nil {
		t.Fatalf("AdminListUserActivities all: %v", err)
	}
	sawLogout := false
	for _, it := range allAct.GetItems() {
		if it.GetEventType() == ActivityEventLogout {
			sawLogout = true
		}
		if it.GetIpAddress() != "203.0.113.xxx" {
			t.Fatalf("expected masked IP 203.0.113.xxx, got %q", it.GetIpAddress())
		}
		if strings.Contains(it.GetMetadataJson(), loginResp.GetRefreshToken()) ||
			strings.Contains(it.GetMetadataJson(), loginResp.GetAccessToken()) {
			t.Fatalf("activity entry leaked token: %+v", it)
		}
	}
	if !sawLogout {
		t.Fatalf("expected logout activity event in AdminListUserActivities, got %+v", allAct.GetItems())
	}
}

// TestW3B2_NonAdminRPCsNoOverBroadStaffAccess verifies that non-admin user/address RPCs
// (GetAddress, GetProfile, ValidateUser, Logout, ListAddresses, UpdateAddress, DeleteAddress, UpdateProfile)
// do NOT grant cross-user access to specialist staff roles (moderator, cskh, accountant, ops) outside
// contracts.md §4.11, while preserving owner, admin/super_admin, and (where allowlisted) service token access,
// and confirming GetShopBankAccount allows shops.bank.read (super_admin, admin, accountant, ops).
func TestW3B2_NonAdminRPCsNoOverBroadStaffAccess(t *testing.T) {
	srv, store, _, pub, _ := newTestHarness(t)

	ownerID := seedUserWithRole(t, srv, store, pub, "pii-owner@example.com", "0907700001", RoleBuyer)
	ownerCtx := ctxWithUser(ownerID, RoleBuyer, "")

	// Seed an address with sensitive recipient phone belonging to ownerID.
	addrResp, err := srv.CreateAddress(ownerCtx, &userv1.CreateAddressRequest{
		RecipientName:  "Nguyen Van Chu",
		RecipientPhone: "0907700001",
		ProvinceCode:   "79",
		ProvinceName:   "TP. Ho Chi Minh",
		WardCode:       "26734",
		WardName:       "Phuong Ben Nghe",
		StreetAddress:  "123 Le Loi",
		IsDefault:      true,
	})
	if err != nil {
		t.Fatalf("CreateAddress: %v", err)
	}
	addrID := addrResp.GetAddress().GetId()

	// Seed an active login session for ownerID so Logout cross-user revocation can be tested.
	loginResp, err := srv.Login(context.Background(), &userv1.LoginRequest{
		Email:    "pii-owner@example.com",
		Password: "Password123!",
	})
	if err != nil {
		t.Fatalf("Login owner: %v", err)
	}
	ownerRefreshToken := loginResp.GetRefreshToken()

	staffIDs := make(map[string]string, 6)
	for i, r := range []string{RoleSuperAdmin, RoleAdmin, RoleModerator, RoleCSKH, RoleAccountant, RoleOps} {
		staffIDs[r] = seedUserWithRole(t, srv, store, pub, "nonadmin-chk-"+r+"@example.com", "090770001"+string(rune('1'+i)), r)
	}
	otherBuyerID := seedUserWithRole(t, srv, store, pub, "nonadmin-chk-buyer@example.com", "0907700021", RoleBuyer)
	otherSellerID := seedUserWithRole(t, srv, store, pub, "nonadmin-chk-seller@example.com", "0907700022", RoleSeller)

	deniedSpecialistRoles := []string{RoleModerator, RoleCSKH, RoleAccountant, RoleOps}
	svcCtx := ctxWithService("order-service")

	// 1. GetAddress: denied for moderator/cskh/accountant/ops/other buyer/other seller; allowed for owner, admin, super_admin, service token.
	for _, r := range deniedSpecialistRoles {
		if _, err := srv.GetAddress(ctxWithUser(staffIDs[r], r, ""), &userv1.GetAddressRequest{AddressId: addrID}); status.Code(err) != codes.PermissionDenied {
			t.Errorf("GetAddress as %s (non-owner): want PermissionDenied, got %v", r, err)
		}
	}
	for _, tc := range []struct {
		name string
		ctx  context.Context
	}{
		{"other_buyer", ctxWithUser(otherBuyerID, RoleBuyer, "")},
		{"other_seller", ctxWithUser(otherSellerID, RoleSeller, "")},
	} {
		if _, err := srv.GetAddress(tc.ctx, &userv1.GetAddressRequest{AddressId: addrID}); status.Code(err) != codes.PermissionDenied {
			t.Errorf("GetAddress as %s: want PermissionDenied, got %v", tc.name, err)
		}
	}
	for _, tc := range []struct {
		name string
		ctx  context.Context
	}{
		{"owner", ownerCtx},
		{"admin", ctxWithUser(staffIDs[RoleAdmin], RoleAdmin, "")},
		{"super_admin", ctxWithUser(staffIDs[RoleSuperAdmin], RoleSuperAdmin, "")},
		{"service_token", svcCtx},
	} {
		got, err := srv.GetAddress(tc.ctx, &userv1.GetAddressRequest{AddressId: addrID})
		if err != nil || got.GetAddress().GetId() != addrID {
			t.Errorf("GetAddress as %s: want OK with addr %s, got err=%v resp=%+v", tc.name, addrID, err, got)
		}
	}

	// 2. GetProfile: denied for moderator/cskh/accountant/ops/other buyer; allowed for owner, admin, super_admin, service token.
	for _, r := range deniedSpecialistRoles {
		if _, err := srv.GetProfile(ctxWithUser(staffIDs[r], r, ""), &userv1.GetProfileRequest{UserId: ownerID}); status.Code(err) != codes.PermissionDenied {
			t.Errorf("GetProfile as %s (non-owner): want PermissionDenied, got %v", r, err)
		}
	}
	if _, err := srv.GetProfile(ctxWithUser(otherBuyerID, RoleBuyer, ""), &userv1.GetProfileRequest{UserId: ownerID}); status.Code(err) != codes.PermissionDenied {
		t.Errorf("GetProfile as other buyer: want PermissionDenied, got %v", err)
	}
	for _, tc := range []struct {
		name string
		ctx  context.Context
	}{
		{"owner", ownerCtx},
		{"admin", ctxWithUser(staffIDs[RoleAdmin], RoleAdmin, "")},
		{"super_admin", ctxWithUser(staffIDs[RoleSuperAdmin], RoleSuperAdmin, "")},
		{"service_token", ctxWithService("notification-service")},
	} {
		got, err := srv.GetProfile(tc.ctx, &userv1.GetProfileRequest{UserId: ownerID})
		if err != nil || got.GetUser().GetId() != ownerID {
			t.Errorf("GetProfile as %s: want OK with user %s, got err=%v resp=%+v", tc.name, ownerID, err, got)
		}
	}

	// 3. ValidateUser: denied for moderator/cskh/accountant/ops/other buyer/service token; allowed for owner, admin, super_admin.
	for _, r := range deniedSpecialistRoles {
		if _, err := srv.ValidateUser(ctxWithUser(staffIDs[r], r, ""), &userv1.ValidateUserRequest{UserId: ownerID}); status.Code(err) != codes.PermissionDenied {
			t.Errorf("ValidateUser as %s (non-owner): want PermissionDenied, got %v", r, err)
		}
	}
	if _, err := srv.ValidateUser(svcCtx, &userv1.ValidateUserRequest{UserId: ownerID}); status.Code(err) != codes.PermissionDenied {
		t.Errorf("ValidateUser with service token: want PermissionDenied, got %v", err)
	}
	for _, tc := range []struct {
		name string
		ctx  context.Context
	}{
		{"owner", ownerCtx},
		{"admin", ctxWithUser(staffIDs[RoleAdmin], RoleAdmin, "")},
		{"super_admin", ctxWithUser(staffIDs[RoleSuperAdmin], RoleSuperAdmin, "")},
	} {
		got, err := srv.ValidateUser(tc.ctx, &userv1.ValidateUserRequest{UserId: ownerID})
		if err != nil || !got.GetValid() {
			t.Errorf("ValidateUser as %s: want Valid=true, got err=%v resp=%+v", tc.name, err, got)
		}
	}

	// 4. Logout (cross-user refresh token revocation): denied for moderator/cskh/accountant/ops/other buyer; allowed for admin/super_admin/owner.
	for _, r := range deniedSpecialistRoles {
		if _, err := srv.Logout(ctxWithUser(staffIDs[r], r, ""), &userv1.LogoutRequest{RefreshToken: ownerRefreshToken}); status.Code(err) != codes.PermissionDenied {
			t.Errorf("Logout(other's refresh token) as %s: want PermissionDenied, got %v", r, err)
		}
	}
	// Verify owner's refresh token is still active after denied specialist attempts, then allow admin to revoke it.
	rtRow, err := store.GetRefreshTokenByHash(context.Background(), hashToken(ownerRefreshToken))
	if err != nil || rtRow.RevokedAt != nil {
		t.Fatalf("owner refresh token should remain active after denied Logout attempts: err=%v row=%+v", err, rtRow)
	}
	if _, err := srv.Logout(ctxWithUser(staffIDs[RoleAdmin], RoleAdmin, ""), &userv1.LogoutRequest{RefreshToken: ownerRefreshToken}); err != nil {
		t.Errorf("Logout(other's refresh token) as admin: want OK, got %v", err)
	}

	// 5. ListAddresses / UpdateAddress / DeleteAddress / UpdateProfile cross-user: denied for moderator/cskh/accountant/ops; allowed for admin.
	for _, r := range deniedSpecialistRoles {
		c := ctxWithUser(staffIDs[r], r, "")
		if _, err := srv.ListAddresses(c, &userv1.ListAddressesRequest{UserId: ownerID}); status.Code(err) != codes.PermissionDenied {
			t.Errorf("ListAddresses(other) as %s: want PermissionDenied, got %v", r, err)
		}
		if _, err := srv.UpdateAddress(c, &userv1.UpdateAddressRequest{UserId: ownerID, AddressId: addrID, RecipientName: "Hijack"}); status.Code(err) != codes.PermissionDenied {
			t.Errorf("UpdateAddress(other) as %s: want PermissionDenied, got %v", r, err)
		}
		if _, err := srv.DeleteAddress(c, &userv1.DeleteAddressRequest{UserId: ownerID, AddressId: addrID}); status.Code(err) != codes.PermissionDenied {
			t.Errorf("DeleteAddress(other) as %s: want PermissionDenied, got %v", r, err)
		}
		if _, err := srv.UpdateProfile(c, &userv1.UpdateProfileRequest{UserId: ownerID, FullName: "Hijack"}); status.Code(err) != codes.PermissionDenied {
			t.Errorf("UpdateProfile(other) as %s: want PermissionDenied, got %v", r, err)
		}
	}
	if listResp, err := srv.ListAddresses(ctxWithUser(staffIDs[RoleAdmin], RoleAdmin, ""), &userv1.ListAddressesRequest{UserId: ownerID}); err != nil || len(listResp.GetAddresses()) != 1 {
		t.Errorf("ListAddresses(other) as admin: want 1 address, got err=%v resp=%+v", err, listResp)
	}

	// 6. GetShopBankAccount (contracts.md §4.11 shops.bank.read): allowed for owner seller, super_admin, admin, accountant, ops; denied for moderator, cskh, service token.
	shopOwnerID, shopID := seedActiveShopMem(t, srv, store, pub, "nonadmin-bank-owner@example.com", "0907700033", "Bank Scope Shop")
	now := time.Now().UTC()
	bankID := uuid.NewString()
	store.mu.Lock()
	store.shopBankAccounts[bankID] = db.ShopBankAccount{
		ID:                  bankID,
		ShopID:              shopID,
		BankCode:            "VCB",
		BankName:            "Vietcombank",
		AccountNumberEnc:    []byte("enc-scope-test"),
		AccountNumberMasked: "****8899",
		AccountHolderName:   "CONG TY SCOPE",
		Status:              BankAccountStatusVerified,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	store.mu.Unlock()
	for _, r := range []string{RoleModerator, RoleCSKH} {
		if _, err := srv.GetShopBankAccount(ctxWithUser(staffIDs[r], r, ""), &userv1.GetShopBankAccountRequest{ShopId: shopID}); status.Code(err) != codes.PermissionDenied {
			t.Errorf("GetShopBankAccount as %s: want PermissionDenied, got %v", r, err)
		}
	}
	if _, err := srv.GetShopBankAccount(svcCtx, &userv1.GetShopBankAccountRequest{ShopId: shopID}); status.Code(err) != codes.PermissionDenied {
		t.Errorf("GetShopBankAccount with service token: want PermissionDenied, got %v", err)
	}
	for _, tc := range []struct {
		name string
		ctx  context.Context
	}{
		{"owner_seller", ctxWithUser(shopOwnerID, RoleSeller, shopID)},
		{"super_admin", ctxWithUser(staffIDs[RoleSuperAdmin], RoleSuperAdmin, "")},
		{"admin", ctxWithUser(staffIDs[RoleAdmin], RoleAdmin, "")},
		{"accountant", ctxWithUser(staffIDs[RoleAccountant], RoleAccountant, "")},
		{"ops", ctxWithUser(staffIDs[RoleOps], RoleOps, "")},
	} {
		got, err := srv.GetShopBankAccount(tc.ctx, &userv1.GetShopBankAccountRequest{ShopId: shopID})
		if err != nil || got.GetBankAccount().GetAccountNumberMasked() != "****8899" || got.GetBankAccount().GetAccountNumber() != "" {
			t.Errorf("GetShopBankAccount as %s: want masked ****8899 and empty plaintext, got err=%v resp=%+v", tc.name, err, got)
		}
	}
}
