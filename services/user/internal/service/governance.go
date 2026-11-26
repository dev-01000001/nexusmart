package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/user/internal/db"
	"github.com/nexusmart/core/services/user/internal/events"
	"github.com/nexusmart/core/services/user/internal/password"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	// ActivityLogRetention is the 180-day retention window enforced on user_activity_logs (F13).
	ActivityLogRetention = 180 * 24 * time.Hour

	defaultRetentionBatchSize int32 = 500
	defaultRetentionInterval        = 24 * time.Hour

	ActivityCategoryAuth    = "auth"
	ActivityCategoryProfile = "profile"
	ActivityCategoryStatus  = "status"
	ActivityCategoryRole    = "role"
	ActivityCategoryPhone   = "phone"

	ActivityEventLoginSucceeded         = "login_succeeded"
	ActivityEventLoginFailed            = "login_failed"
	ActivityEventLogout                 = "logout"
	ActivityEventPasswordResetRequested = "password_reset_requested"
	ActivityEventPasswordResetFailed    = "password_reset_failed"
	ActivityEventPasswordResetCompleted = "password_reset_completed"
	ActivityEventPasswordChanged        = "password_changed"
	ActivityEventProfileUpdated         = "profile_updated"
	ActivityEventStatusChanged          = "status_changed"
	ActivityEventRoleChanged            = "role_changed"
	ActivityEventStaffCreated           = "staff_created"
	ActivityEventPhoneOTPRequested      = "phone_otp_requested"
	ActivityEventPhoneOTPVerified       = "phone_otp_verified"
	ActivityEventPhoneOTPFailed         = "phone_otp_failed"
	ActivityEventShopSuspended          = "shop_suspended"
	ActivityEventShopUnsuspended        = "shop_unsuspended"
)

// CreateStaffUserInput defines the input for super_admin staff creation (F10, F14).
// Intentionally accepts no password field: a random unknown password is generated and hashed,
// and the staff member sets their password via the existing password-reset email flow.
type CreateStaffUserInput struct {
	FullName string
	Email    string
	Phone    string
	Role     string
}

// CreateStaffUserResult is returned by CreateStaffUser. Never exposes password or reset token.
type CreateStaffUserResult struct {
	User              db.User
	PasswordResetSent bool
}

// UpdateUserRoleInput defines the input for role changes across all 8 roles (F10).
type UpdateUserRoleInput struct {
	TargetUserID string
	Role         string
	Reason       string
}

// ChangePasswordInput defines the input for an authenticated user changing their own password (F13).
type ChangePasswordInput struct {
	CurrentPassword string
	NewPassword     string
}

// RequestShopSuspensionInput defines the input for requesting 2-person shop suspension (F11).
type RequestShopSuspensionInput struct {
	ShopID string
	Reason string
}

// RequestShopSuspensionResult returns the created approval and shop display name.
type RequestShopSuspensionResult struct {
	Approval db.ShopSuspensionApproval
	ShopName string
}

// ListShopApprovalsInput defines filters for listing shop suspension approval requests (F11).
type ListShopApprovalsInput struct {
	Status   string
	ShopID   string
	Page     int32
	PageSize int32
}

// ListShopApprovalsResult holds paginated shop suspension approval rows (with shop_name) and total count.
type ListShopApprovalsResult struct {
	Approvals []db.ListShopSuspensionApprovalsRow
	Total     int64
}

// DecideShopSuspensionInput defines the input for approving/rejecting a pending shop suspension (F11, F12).
type DecideShopSuspensionInput struct {
	ApprovalID string
	Approve    bool
	Note       string
}

// DecideShopSuspensionResult returns the decided approval and current shop state.
type DecideShopSuspensionResult struct {
	Approval db.ShopSuspensionApproval
	Shop     db.Shop
}

// UnsuspendShopInput defines the input for reactivating a SUSPENDED shop (F12).
type UnsuspendShopInput struct {
	ShopID string
	Reason string
}

// ListUserActivitiesInput defines filters for listing a user's activity timeline (F13).
type ListUserActivitiesInput struct {
	UserID   string
	FromTime *time.Time
	ToTime   *time.Time
	Category string
	Page     int32
	PageSize int32
}

// ListUserActivitiesResult holds paginated activity logs and the user's COD rejection count.
type ListUserActivitiesResult struct {
	Entries          []db.UserActivityLog
	Total            int64
	CODRejectedCount int32
}

func generateUnknownStaffPassword() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate random staff password: %w", err)
	}
	// 256 bits of CSPRNG entropy plus complexity prefix; hashed immediately and discarded.
	return "Aa1!" + base64.RawURLEncoding.EncodeToString(buf), nil
}

func outboxTraceFromContext(ctx context.Context) (traceID, traceparent string) {
	traceID = telemetry.TraceIDFromContext(ctx)
	carrier := make(map[string]string)
	telemetry.InjectTraceHeaders(ctx, carrier)
	traceparent = carrier["traceparent"]
	return traceID, traceparent
}

func (s *Server) notifyOutbox() {
	if s.outboxTrigger == nil {
		return
	}
	select {
	case s.outboxTrigger <- struct{}{}:
	default:
	}
}

func (s *Server) recordActivityTx(
	ctx context.Context,
	q db.Querier,
	userID, eventType, category, rawIP string,
	metadata map[string]any,
) error {
	if !isValidUUID(userID) {
		return nil
	}
	_, err := q.InsertUserActivityLog(ctx, db.InsertUserActivityLogParams{
		ID:           uuid.NewString(),
		UserID:       userID,
		EventType:    eventType,
		Category:     category,
		IPAddress:    maskIP(rawIP),
		MetadataJSON: marshalAuditJSON(metadata),
		CreatedAt:    s.now(),
	})
	return err
}

func (s *Server) recordActivityBestEffort(
	ctx context.Context,
	userID, eventType, category, rawIP string,
	metadata map[string]any,
) {
	if !isValidUUID(userID) {
		return
	}
	logCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if _, err := s.store.InsertUserActivityLog(logCtx, db.InsertUserActivityLogParams{
		ID:           uuid.NewString(),
		UserID:       userID,
		EventType:    eventType,
		Category:     category,
		IPAddress:    maskIP(rawIP),
		MetadataJSON: marshalAuditJSON(metadata),
		CreatedAt:    s.now(),
	}); err != nil {
		s.log.WarnContext(ctx, "failed to write user activity log",
			slog.String("user_id", userID),
			slog.String("event_type", eventType),
			slog.String("error", err.Error()),
		)
	}
}

func auditApprovalSnapshot(a db.ShopSuspensionApproval) map[string]any {
	out := map[string]any{
		"id":             a.ID,
		"shop_id":        a.ShopID,
		"status":         a.Status,
		"reason":         a.Reason,
		"requested_by":   a.RequestedBy,
		"requested_role": a.RequestedRole,
		"decided_role":   a.DecidedRole,
		"decided_note":   a.DecidedNote,
	}
	if a.DecidedBy != nil {
		out["decided_by"] = *a.DecidedBy
	}
	if a.DecidedAt != nil {
		out["decided_at"] = a.DecidedAt.UTC().Format(time.RFC3339)
	}
	return out
}

// --- gRPC handlers wired to pure governance service methods ---

func (s *Server) AdminCreateStaffUser(ctx context.Context, req *userv1.AdminCreateStaffUserRequest) (*userv1.AdminCreateStaffUserResponse, error) {
	res, err := s.CreateStaffUser(ctx, CreateStaffUserInput{
		FullName: req.GetFullName(),
		Email:    req.GetEmail(),
		Phone:    req.GetPhone(),
		Role:     roleFromProto(req.GetRole()),
	})
	if err != nil {
		return nil, err
	}
	return &userv1.AdminCreateStaffUserResponse{
		User:              toProtoUser(res.User),
		PasswordResetSent: res.PasswordResetSent,
	}, nil
}

func (s *Server) AdminListUserActivities(ctx context.Context, req *userv1.AdminListUserActivitiesRequest) (*userv1.AdminListUserActivitiesResponse, error) {
	var fromTime, toTime *time.Time
	if req.GetFromTime() != nil {
		t := req.GetFromTime().AsTime()
		fromTime = &t
	}
	if req.GetToTime() != nil {
		t := req.GetToTime().AsTime()
		toTime = &t
	}
	res, err := s.ListUserActivities(ctx, ListUserActivitiesInput{
		UserID:   req.GetUserId(),
		FromTime: fromTime,
		ToTime:   toTime,
		Category: req.GetCategory(),
		Page:     req.GetPage(),
		PageSize: req.GetPageSize(),
	})
	if err != nil {
		return nil, err
	}
	items := make([]*userv1.UserActivityEntry, 0, len(res.Entries))
	for _, entry := range res.Entries {
		items = append(items, toProtoUserActivityEntry(entry))
	}
	return &userv1.AdminListUserActivitiesResponse{
		Items:            items,
		Total:            res.Total,
		CodRejectedCount: res.CODRejectedCount,
	}, nil
}

func (s *Server) AdminRequestShopSuspension(ctx context.Context, req *userv1.AdminRequestShopSuspensionRequest) (*userv1.AdminRequestShopSuspensionResponse, error) {
	res, err := s.RequestShopSuspension(ctx, RequestShopSuspensionInput{
		ShopID: req.GetShopId(),
		Reason: req.GetReason(),
	})
	if err != nil {
		return nil, err
	}
	return &userv1.AdminRequestShopSuspensionResponse{
		Approval: toProtoShopSuspensionApproval(res.Approval, res.ShopName),
	}, nil
}

func (s *Server) AdminListShopApprovals(ctx context.Context, req *userv1.AdminListShopApprovalsRequest) (*userv1.AdminListShopApprovalsResponse, error) {
	res, err := s.ListShopSuspensionApprovals(ctx, ListShopApprovalsInput{
		Status:   shopApprovalStatusFromProto(req.GetStatus()),
		ShopID:   req.GetShopId(),
		Page:     req.GetPage(),
		PageSize: req.GetPageSize(),
	})
	if err != nil {
		return nil, err
	}
	items := make([]*userv1.ShopSuspensionApproval, 0, len(res.Approvals))
	for _, row := range res.Approvals {
		items = append(items, toProtoShopSuspensionApprovalFromRow(row))
	}
	return &userv1.AdminListShopApprovalsResponse{
		Items: items,
		Total: res.Total,
	}, nil
}

func (s *Server) AdminDecideShopSuspension(ctx context.Context, req *userv1.AdminDecideShopSuspensionRequest) (*userv1.AdminDecideShopSuspensionResponse, error) {
	var approve bool
	switch req.GetDecision() {
	case userv1.ReviewDecision_REVIEW_DECISION_APPROVE:
		approve = true
	case userv1.ReviewDecision_REVIEW_DECISION_REJECT:
		approve = false
	default:
		return nil, status.Error(codes.InvalidArgument, "decision must be APPROVE or REJECT")
	}

	res, err := s.DecideShopSuspension(ctx, DecideShopSuspensionInput{
		ApprovalID: req.GetApprovalId(),
		Approve:    approve,
		Note:       req.GetNote(),
	})
	if err != nil {
		return nil, err
	}
	return &userv1.AdminDecideShopSuspensionResponse{
		Approval: toProtoShopSuspensionApproval(res.Approval, res.Shop.Name),
		Shop:     toProtoShop(res.Shop),
	}, nil
}

func (s *Server) AdminUnsuspendShop(ctx context.Context, req *userv1.AdminUnsuspendShopRequest) (*userv1.AdminUnsuspendShopResponse, error) {
	shop, err := s.UnsuspendShop(ctx, UnsuspendShopInput{
		ShopID: req.GetShopId(),
		Reason: req.GetReason(),
	})
	if err != nil {
		return nil, err
	}
	return &userv1.AdminUnsuspendShopResponse{
		Shop: toProtoShop(*shop),
	}, nil
}

// --- Pure governance business logic ---

// CreateStaffUser creates an ACTIVE internal staff account (staff.create / super_admin only, F10/F14).
// No caller-supplied password is accepted; a random unknown password is hashed and the existing
// password-reset flow emails a reset link to the new staff member.
func (s *Server) CreateStaffUser(ctx context.Context, input CreateStaffUserInput) (*CreateStaffUserResult, error) {
	claims, err := s.requireCapability(ctx, auth.CapStaffCreate)
	if err != nil {
		return nil, err
	}

	fullName := strings.TrimSpace(input.FullName)
	email := normalizeEmail(input.Email)
	role := normalizeRole(input.Role)
	rawPhone := strings.TrimSpace(input.Phone)

	if fullName == "" {
		return nil, status.Error(codes.InvalidArgument, "full_name is required")
	}
	if !isValidEmail(email) {
		return nil, status.Error(codes.InvalidArgument, "valid email is required")
	}
	if !isValidStaffCreatableRole(role) {
		return nil, status.Error(codes.InvalidArgument, "role must be one of admin, moderator, cskh, accountant, ops")
	}

	var phone string
	if rawPhone != "" {
		canon, ok := canonicalVNPhone(rawPhone)
		if !ok {
			return nil, status.Error(codes.InvalidArgument, "valid Vietnamese phone is required")
		}
		phone = canon
	}

	unknownPassword, err := generateUnknownStaffPassword()
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to generate credentials")
	}
	passwordHash, err := s.hasher.Hash(unknownPassword)
	unknownPassword = "" //nolint:ineffassign // explicitly drop plaintext secret immediately after hashing
	_ = unknownPassword
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to hash credentials")
	}

	resetPlain, resetHash, err := generateOpaqueToken()
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to generate password reset token")
	}

	now := s.now()
	expiresAt := now.Add(s.opts.PasswordResetTTL)
	userID := uuid.NewString()
	resetID := uuid.NewString()
	rawIP := auditIPFromContext(ctx, claims, "")

	var created db.User
	err = s.store.WithTx(ctx, func(q db.Querier) error {
		if _, txErr := q.GetUserByEmail(ctx, email); txErr == nil {
			return status.Error(codes.AlreadyExists, "email is already registered")
		} else if !isNotFoundErr(txErr) {
			return txErr
		}

		if phone != "" {
			if uErr := checkVerifiedPhoneAvailableForUser(ctx, q, userID, phone); uErr != nil {
				return uErr
			}
		}

		var txErr error
		created, txErr = q.CreateUser(ctx, db.CreateUserParams{
			ID:            userID,
			FullName:      fullName,
			Email:         email,
			Phone:         phone,
			PasswordHash:  passwordHash,
			Role:          role,
			Status:        StatusActive,
			PhoneVerified: false,
		})
		if txErr != nil {
			if isUniqueViolation(txErr) {
				return status.Error(codes.AlreadyExists, "email or phone already registered")
			}
			return txErr
		}

		if _, txErr = q.CreatePasswordReset(ctx, db.CreatePasswordResetParams{
			ID:        resetID,
			UserID:    created.ID,
			TokenHash: resetHash,
			ExpiresAt: expiresAt,
		}); txErr != nil {
			return txErr
		}

		if _, txErr = q.InsertAuditLog(ctx, db.InsertAuditLogParams{
			ID:         uuid.NewString(),
			ActorID:    claims.UserID(),
			ActorRole:  claims.Role,
			Action:     "admin.staff.create",
			TargetType: "user",
			TargetID:   created.ID,
			BeforeJSON: "{}",
			AfterJSON:  marshalAuditJSON(auditUserSnapshot(created)),
			Reason:     "staff onboarding",
			IPAddress:  rawIP,
		}); txErr != nil {
			return txErr
		}

		return s.recordActivityTx(ctx, q, created.ID, ActivityEventStaffCreated, ActivityCategoryRole, rawIP, map[string]any{
			"role":       created.Role,
			"actor_id":   claims.UserID(),
			"actor_role": claims.Role,
		})
	})
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		return nil, status.Error(codes.Internal, "failed to create staff user")
	}

	resetSent := false
	invalidateReset := func(invCtx context.Context) {
		invAt := s.now()
		if _, invErr := s.store.MarkPasswordResetUsed(invCtx, db.MarkPasswordResetUsedParams{
			ID:     resetID,
			UsedAt: &invAt,
		}); invErr != nil {
			s.log.WarnContext(invCtx, "failed to invalidate undelivered staff reset token",
				slog.String("user_id", created.ID),
				slog.String("error", invErr.Error()),
			)
		}
	}
	if s.pub != nil {
		if pubErr := s.pub.PublishPasswordResetRequested(ctx, events.PasswordResetRequestedPayload{
			UserID:     created.ID,
			Email:      created.Email,
			FullName:   created.FullName,
			ResetToken: resetPlain,
			ExpiresAt:  expiresAt.Format(time.RFC3339),
		}); pubErr != nil {
			s.log.ErrorContext(ctx, "failed to publish staff onboarding password reset email",
				slog.String("user_id", created.ID),
				slog.String("error", pubErr.Error()),
			)
			invCtx, invCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer invCancel()
			invalidateReset(invCtx)
			s.recordActivityBestEffort(invCtx, created.ID, ActivityEventPasswordResetFailed, ActivityCategoryAuth, rawIP, map[string]any{
				"source":   "staff_onboarding",
				"actor_id": claims.UserID(),
				"reason":   "publish_failed",
			})
		} else {
			resetSent = true
			actCtx, actCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer actCancel()
			s.recordActivityBestEffort(actCtx, created.ID, ActivityEventPasswordResetRequested, ActivityCategoryAuth, rawIP, map[string]any{
				"source":   "staff_onboarding",
				"actor_id": claims.UserID(),
			})
		}
	} else {
		invCtx, invCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer invCancel()
		invalidateReset(invCtx)
		s.recordActivityBestEffort(invCtx, created.ID, ActivityEventPasswordResetFailed, ActivityCategoryAuth, rawIP, map[string]any{
			"source":   "staff_onboarding",
			"actor_id": claims.UserID(),
			"reason":   "publisher_unavailable",
		})
	}

	s.log.InfoContext(ctx, "staff user created",
		slog.String("user_id", created.ID),
		slog.String("role", created.Role),
		slog.Bool("password_reset_sent", resetSent),
	)
	return &CreateStaffUserResult{
		User:              created,
		PasswordResetSent: resetSent,
	}, nil
}

// UpdateUserRole updates a user's role among the 7 assignable roles
// (buyer, seller, admin, moderator, cskh, accountant, ops; users.role.write / super_admin only, F10).
// super_admin cannot be assigned via API (contracts.md §4.1), self-modification is rejected with
// FailedPrecondition, and CanActOnTargetRole unconditionally protects every super_admin target
// (so the last active super_admin can never be demoted via API).
func (s *Server) UpdateUserRole(ctx context.Context, input UpdateUserRoleInput) (db.User, error) {
	claims, err := s.requireCapability(ctx, auth.CapUsersRoleWrite)
	if err != nil {
		return db.User{}, err
	}

	targetID := strings.TrimSpace(input.TargetUserID)
	newRole := normalizeRole(input.Role)
	if targetID == "" || !isValidRole(newRole) {
		return db.User{}, status.Error(codes.InvalidArgument, "target_user_id and valid role are required")
	}
	if newRole == RoleSuperAdmin || !auth.CanActOnTargetRole(claims.Role, newRole) {
		return db.User{}, status.Error(codes.PermissionDenied, "cannot assign role equal to or higher than caller role")
	}
	if !isValidUUID(targetID) {
		return db.User{}, status.Error(codes.NotFound, "target user not found")
	}

	actorID := claims.UserID()
	if actorID == targetID {
		return db.User{}, status.Error(codes.FailedPrecondition, "admin cannot modify own role")
	}

	reason := strings.TrimSpace(input.Reason)
	rawIP := auditIPFromContext(ctx, claims, "")

	var updated db.User
	err = s.store.WithTx(ctx, func(q db.Querier) error {
		target, txErr := q.GetUserByIDForUpdate(ctx, targetID)
		if txErr != nil {
			if isNotFoundErr(txErr) {
				return status.Error(codes.NotFound, "target user not found")
			}
			return txErr
		}
		// F10: strict RoleRank(actor) > RoleRank(target) blocks modifying any equal/higher role,
		// including any super_admin account (guaranteeing the last active super_admin cannot be demoted).
		if !auth.CanActOnTargetRole(claims.Role, target.Role) {
			return status.Error(codes.PermissionDenied, "cannot modify an account with equal or higher role")
		}

		updated, txErr = q.UpdateUserRole(ctx, db.UpdateUserRoleParams{
			ID:   target.ID,
			Role: newRole,
		})
		if txErr != nil {
			return txErr
		}
		if txErr = q.RevokeAllUserRefreshTokens(ctx, target.ID); txErr != nil {
			return txErr
		}
		if _, txErr = q.InsertAuditLog(ctx, db.InsertAuditLogParams{
			ID:         uuid.NewString(),
			ActorID:    actorID,
			ActorRole:  claims.Role,
			Action:     "admin.user.update_role",
			TargetType: "user",
			TargetID:   target.ID,
			BeforeJSON: marshalAuditJSON(auditUserSnapshot(target)),
			AfterJSON:  marshalAuditJSON(auditUserSnapshot(updated)),
			Reason:     reason,
			IPAddress:  rawIP,
		}); txErr != nil {
			return txErr
		}
		if txErr = s.recordActivityTx(ctx, q, target.ID, ActivityEventRoleChanged, ActivityCategoryRole, rawIP, map[string]any{
			"from_role":  target.Role,
			"to_role":    updated.Role,
			"actor_id":   actorID,
			"actor_role": claims.Role,
			"reason":     reason,
		}); txErr != nil {
			return txErr
		}
		if s.sec != nil {
			if rErr := s.sec.SetRevokedBefore(ctx, target.ID, s.now().Unix(), s.revokedBeforeTTL()); rErr != nil {
				s.log.ErrorContext(ctx, "failed to set revoked_before on role change",
					slog.String("user_id", target.ID),
					slog.String("error", rErr.Error()),
				)
				return status.Error(codes.Unavailable, "failed to revoke active sessions")
			}
		}
		return nil
	})
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return db.User{}, err
		}
		return db.User{}, status.Error(codes.Internal, "failed to update user role")
	}

	return updated, nil
}

// ChangePassword allows an authenticated user to change their own password, revoking active sessions
// and recording a password_changed activity log without sensitive fields (F13).
func (s *Server) ChangePassword(ctx context.Context, input ChangePasswordInput) error {
	claims, err := s.requireAuthClaims(ctx)
	if err != nil {
		return err
	}
	if claims.Service != "" {
		return status.Error(codes.PermissionDenied, "service identity not allowed for password change")
	}
	userID := claims.UserID()
	if !isValidUUID(userID) {
		return status.Error(codes.NotFound, "user not found")
	}
	if input.CurrentPassword == "" {
		return status.Error(codes.InvalidArgument, "current_password is required")
	}
	if err := password.ValidateStrength(input.NewPassword); err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}

	existing, err := s.store.GetUserByID(ctx, userID)
	if err != nil {
		if isNotFoundErr(err) {
			return status.Error(codes.NotFound, "user not found")
		}
		return status.Error(codes.Internal, "failed to load user")
	}
	if existing.Status == StatusLocked {
		return status.Error(codes.PermissionDenied, lockedAccountMessage(existing.LockReason))
	}
	if existing.Status != StatusActive {
		return status.Error(codes.FailedPrecondition, "account is not active")
	}

	ok, err := s.hasher.Verify(input.CurrentPassword, existing.PasswordHash)
	if err != nil || !ok {
		return status.Error(codes.Unauthenticated, "current password is incorrect")
	}

	newHash, err := s.hasher.Hash(input.NewPassword)
	if err != nil {
		return status.Error(codes.Internal, "failed to hash password")
	}

	rawIP := auditIPFromContext(ctx, claims, "")
	err = s.store.WithTx(ctx, func(q db.Querier) error {
		u, txErr := q.GetUserByIDForUpdate(ctx, userID)
		if txErr != nil {
			if isNotFoundErr(txErr) {
				return status.Error(codes.NotFound, "user not found")
			}
			return txErr
		}
		if u.Status == StatusLocked {
			return status.Error(codes.PermissionDenied, lockedAccountMessage(u.LockReason))
		}
		if u.Status != StatusActive {
			return status.Error(codes.FailedPrecondition, "account is not active")
		}
		if u.PasswordHash != existing.PasswordHash {
			return status.Error(codes.Unauthenticated, "current password is incorrect")
		}

		if _, txErr = q.UpdateUserPassword(ctx, db.UpdateUserPasswordParams{
			ID:           u.ID,
			PasswordHash: newHash,
		}); txErr != nil {
			return txErr
		}

		revokedAt := s.now()
		if txErr = q.InvalidateUserPasswordResets(ctx, db.InvalidateUserPasswordResetsParams{
			UserID: u.ID,
			UsedAt: &revokedAt,
		}); txErr != nil {
			return txErr
		}
		if txErr = q.RevokeAllUserRefreshTokens(ctx, u.ID); txErr != nil {
			return txErr
		}
		if txErr = s.recordActivityTx(ctx, q, u.ID, ActivityEventPasswordChanged, ActivityCategoryAuth, rawIP, map[string]any{
			"source": "self",
		}); txErr != nil {
			return txErr
		}
		if s.sec != nil {
			if rErr := s.sec.SetRevokedBefore(ctx, u.ID, revokedAt.Unix(), s.revokedBeforeTTL()); rErr != nil {
				s.log.ErrorContext(ctx, "failed to set revoked_before on password change",
					slog.String("user_id", u.ID),
					slog.String("error", rErr.Error()),
				)
				return status.Error(codes.Unavailable, "failed to revoke active sessions")
			}
		}
		return nil
	})
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return err
		}
		return status.Error(codes.Internal, "failed to change password")
	}

	s.log.InfoContext(ctx, "user password changed", slog.String("user_id", userID))
	return nil
}

// RequestShopSuspension creates a PENDING two-person approval request to suspend an ACTIVE shop (F11).
func (s *Server) RequestShopSuspension(ctx context.Context, input RequestShopSuspensionInput) (*RequestShopSuspensionResult, error) {
	claims, err := s.requireCapability(ctx, auth.CapShopsSuspendRequest)
	if err != nil {
		return nil, err
	}
	if !isValidUUID(claims.UserID()) {
		return nil, status.Error(codes.Unauthenticated, "invalid actor identity")
	}

	shopID := strings.TrimSpace(input.ShopID)
	reason := strings.TrimSpace(input.Reason)
	if shopID == "" {
		return nil, status.Error(codes.InvalidArgument, "shop_id is required")
	}
	if reason == "" {
		return nil, status.Error(codes.InvalidArgument, "reason is required")
	}
	if !isValidUUID(shopID) {
		return nil, status.Error(codes.NotFound, "shop not found")
	}

	rawIP := auditIPFromContext(ctx, claims, "")
	var (
		created  db.ShopSuspensionApproval
		shopName string
	)
	err = s.store.WithTx(ctx, func(q db.Querier) error {
		shop, txErr := q.GetShopByIDForUpdate(ctx, shopID)
		if txErr != nil {
			if isNotFoundErr(txErr) {
				return status.Error(codes.NotFound, "shop not found")
			}
			return txErr
		}
		if shop.Status != ShopStatusActive {
			return status.Error(codes.FailedPrecondition, "only ACTIVE shops can be requested for suspension")
		}
		shopName = shop.Name

		if _, pErr := q.GetPendingShopSuspensionApprovalByShopID(ctx, shop.ID); pErr == nil {
			return status.Error(codes.AlreadyExists, "a pending suspension approval already exists for this shop")
		} else if !isNotFoundErr(pErr) {
			return pErr
		}

		created, txErr = q.CreateShopSuspensionApproval(ctx, db.CreateShopSuspensionApprovalParams{
			ID:            uuid.NewString(),
			ShopID:        shop.ID,
			Reason:        reason,
			RequestedBy:   claims.UserID(),
			RequestedRole: normalizeRole(claims.Role),
			CreatedAt:     s.now(),
		})
		if txErr != nil {
			if isUniqueViolation(txErr) {
				return status.Error(codes.AlreadyExists, "a pending suspension approval already exists for this shop")
			}
			return txErr
		}

		_, txErr = q.InsertAuditLog(ctx, db.InsertAuditLogParams{
			ID:         uuid.NewString(),
			ActorID:    claims.UserID(),
			ActorRole:  claims.Role,
			Action:     "admin.shop.suspend_request",
			TargetType: "shop",
			TargetID:   shop.ID,
			BeforeJSON: marshalAuditJSON(auditShopSnapshot(shop)),
			AfterJSON:  marshalAuditJSON(auditApprovalSnapshot(created)),
			Reason:     reason,
			IPAddress:  rawIP,
		})
		return txErr
	})
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		return nil, status.Error(codes.Internal, "failed to create shop suspension request")
	}

	return &RequestShopSuspensionResult{
		Approval: created,
		ShopName: shopName,
	}, nil
}

// ListShopSuspensionApprovals lists shop suspension approval requests with optional status/shop filters.
func (s *Server) ListShopSuspensionApprovals(ctx context.Context, input ListShopApprovalsInput) (*ListShopApprovalsResult, error) {
	if _, err := s.requireCapability(ctx, auth.CapShopsApprovalsRead); err != nil {
		return nil, err
	}

	st := strings.ToUpper(strings.TrimSpace(input.Status))
	if st != "" && st != ApprovalStatusPending && st != ApprovalStatusApproved && st != ApprovalStatusRejected {
		return nil, status.Error(codes.InvalidArgument, "status must be PENDING, APPROVED, or REJECTED")
	}

	var shopIDPtr *string
	if sid := strings.TrimSpace(input.ShopID); sid != "" {
		if !isValidUUID(sid) {
			return nil, status.Error(codes.InvalidArgument, "invalid shop_id")
		}
		shopIDPtr = &sid
	}

	page, pageSize, err := normalizePagination(input.Page, input.PageSize)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.ListShopSuspensionApprovals(ctx, db.ListShopSuspensionApprovalsParams{
		Status:     st,
		ShopID:     shopIDPtr,
		PageLimit:  pageSize,
		PageOffset: (page - 1) * pageSize,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list shop suspension approvals")
	}
	total, err := s.store.CountShopSuspensionApprovals(ctx, db.CountShopSuspensionApprovalsParams{
		Status: st,
		ShopID: shopIDPtr,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to count shop suspension approvals")
	}

	return &ListShopApprovalsResult{
		Approvals: rows,
		Total:     total,
	}, nil
}

// DecideShopSuspension approves or rejects a pending shop suspension request (F11, F12).
// Enforces: checker capability from live claims AND re-verified current DB role/status locked
// FOR SHARE at decision time, FOR UPDATE serialization on shop/approval, maker != checker
// (app MAKER_CHECKER_CONFLICT + DB CHECK), and atomic shop status + version + outbox
// (version + occurred_at) + refresh token / revoked_before.
func (s *Server) DecideShopSuspension(ctx context.Context, input DecideShopSuspensionInput) (*DecideShopSuspensionResult, error) {
	claims, err := s.requireCapability(ctx, auth.CapShopsSuspendDecide)
	if err != nil {
		return nil, err
	}
	checkerID := claims.UserID()
	if !isValidUUID(checkerID) {
		return nil, status.Error(codes.Unauthenticated, "invalid actor identity")
	}

	approvalID := strings.TrimSpace(input.ApprovalID)
	if approvalID == "" {
		return nil, status.Error(codes.InvalidArgument, "approval_id is required")
	}
	if !isValidUUID(approvalID) {
		return nil, status.Error(codes.NotFound, "shop suspension approval not found")
	}

	note := strings.TrimSpace(input.Note)
	rawIP := auditIPFromContext(ctx, claims, "")
	traceID, traceparent := outboxTraceFromContext(ctx)

	var (
		decidedApproval db.ShopSuspensionApproval
		resultShop      db.Shop
		emittedOutbox   bool
	)
	err = s.store.WithTx(ctx, func(q db.Querier) error {
		// Non-locking peek to learn shop_id first so locks are always acquired in canonical order:
		//   1. shops (FOR UPDATE)
		//   2. shop_suspension_approvals (FOR UPDATE)
		//   3. users: checker (FOR SHARE, or FOR UPDATE when approving own shop to avoid lock upgrade)
		//   4. users: shop owner (FOR UPDATE on approve, when distinct from checker)
		//   5. refresh_tokens
		// Deadlock-free with UpdateUserRole / AdminUpdateUserStatus because they never lock shops or
		// approvals and only lock a single target users row (FOR UPDATE) -> refresh_tokens.
		peek, txErr := q.GetShopSuspensionApprovalByID(ctx, approvalID)
		if txErr != nil {
			if isNotFoundErr(txErr) {
				return status.Error(codes.NotFound, "shop suspension approval not found")
			}
			return txErr
		}

		shop, txErr := q.GetShopByIDForUpdate(ctx, peek.ShopID)
		if txErr != nil {
			if isNotFoundErr(txErr) {
				return status.Error(codes.NotFound, "shop not found")
			}
			return txErr
		}

		approval, txErr := q.GetShopSuspensionApprovalByIDForUpdate(ctx, approvalID)
		if txErr != nil {
			if isNotFoundErr(txErr) {
				return status.Error(codes.NotFound, "shop suspension approval not found")
			}
			return txErr
		}

		// Re-verify checker's live role and ACTIVE status in DB under FOR SHARE (F11):
		// serializes against concurrent UpdateUserRole / AdminUpdateUserStatus so a demotion
		// or lock that commits before this transaction commits is always observed and rejected,
		// and a concurrent demotion cannot commit ahead of this decision once FOR SHARE is held.
		var checker db.User
		if input.Approve && checkerID == shop.OwnerUserID {
			checker, txErr = q.GetUserByIDForUpdate(ctx, checkerID)
		} else {
			checker, txErr = q.GetUserByIDForShare(ctx, checkerID)
		}
		if txErr != nil {
			if isNotFoundErr(txErr) {
				return status.Error(codes.PermissionDenied, "checker account not found")
			}
			return txErr
		}
		if checker.Status != StatusActive || !auth.RoleHasCapability(checker.Role, auth.CapShopsSuspendDecide) {
			return status.Error(codes.PermissionDenied, "checker no longer holds approval authority")
		}

		if approval.Status != ApprovalStatusPending {
			return status.Error(codes.FailedPrecondition, "shop suspension approval has already been decided")
		}
		if approval.RequestedBy == checkerID {
			return status.Error(codes.PermissionDenied, "MAKER_CHECKER_CONFLICT: maker cannot decide own shop suspension request")
		}

		now := s.now()
		checkerRole := normalizeRole(checker.Role)

		if !input.Approve {
			decidedApproval, txErr = q.DecideShopSuspensionApproval(ctx, db.DecideShopSuspensionApprovalParams{
				ID:          approval.ID,
				Status:      ApprovalStatusRejected,
				DecidedBy:   &checkerID,
				DecidedRole: checkerRole,
				DecidedNote: note,
				DecidedAt:   &now,
			})
			if txErr != nil {
				if isCheckViolation(txErr) {
					return status.Error(codes.PermissionDenied, "MAKER_CHECKER_CONFLICT: maker cannot decide own shop suspension request")
				}
				if isNotFoundErr(txErr) {
					return status.Error(codes.FailedPrecondition, "shop suspension approval has already been decided")
				}
				return txErr
			}
			resultShop = shop
			_, txErr = q.InsertAuditLog(ctx, db.InsertAuditLogParams{
				ID:         uuid.NewString(),
				ActorID:    checkerID,
				ActorRole:  checkerRole,
				Action:     "admin.shop.suspend_reject",
				TargetType: "shop",
				TargetID:   shop.ID,
				BeforeJSON: marshalAuditJSON(auditApprovalSnapshot(approval)),
				AfterJSON:  marshalAuditJSON(auditApprovalSnapshot(decidedApproval)),
				Reason:     note,
				IPAddress:  rawIP,
			})
			return txErr
		}

		if shop.Status != ShopStatusActive {
			return status.Error(codes.FailedPrecondition, "only ACTIVE shops can be suspended")
		}

		decidedApproval, txErr = q.DecideShopSuspensionApproval(ctx, db.DecideShopSuspensionApprovalParams{
			ID:          approval.ID,
			Status:      ApprovalStatusApproved,
			DecidedBy:   &checkerID,
			DecidedRole: checkerRole,
			DecidedNote: note,
			DecidedAt:   &now,
		})
		if txErr != nil {
			if isCheckViolation(txErr) {
				return status.Error(codes.PermissionDenied, "MAKER_CHECKER_CONFLICT: maker cannot decide own shop suspension request")
			}
			if isNotFoundErr(txErr) {
				return status.Error(codes.FailedPrecondition, "shop suspension approval has already been decided")
			}
			return txErr
		}

		updatedShop, txErr := q.SuspendShop(ctx, db.SuspendShopParams{
			ID:              shop.ID,
			SuspendedAt:     &now,
			SuspendedReason: approval.Reason,
			UpdatedAt:       now,
		})
		if txErr != nil {
			if isNotFoundErr(txErr) {
				return status.Error(codes.FailedPrecondition, "only ACTIVE shops can be suspended")
			}
			return txErr
		}

		// Lock shop owner row so refresh token revocation + revoked_before serialize with Login/RefreshToken FOR SHARE.
		// Skip if checker == owner (already locked FOR UPDATE above to avoid an in-tx lock upgrade).
		if updatedShop.OwnerUserID != checkerID {
			if _, txErr = q.GetUserByIDForUpdate(ctx, updatedShop.OwnerUserID); txErr != nil && !isNotFoundErr(txErr) {
				return txErr
			}
		}
		if txErr = q.RevokeAllUserRefreshTokens(ctx, updatedShop.OwnerUserID); txErr != nil {
			return txErr
		}

		payloadBytes, mErr := json.Marshal(events.ShopStatusChangedPayload{
			ShopID:      updatedShop.ID,
			OwnerUserID: updatedShop.OwnerUserID,
			ShopName:    updatedShop.Name,
			FromStatus:  shop.Status,
			ToStatus:    updatedShop.Status,
			Reason:      approval.Reason,
			ActorID:     approval.RequestedBy,
			ApproverID:  checkerID,
			Version:     updatedShop.StatusVersion,
			OccurredAt:  now.UTC().Format(time.RFC3339),
		})
		if mErr != nil {
			return fmt.Errorf("marshal shop status changed payload: %w", mErr)
		}
		if txErr = q.InsertOutboxEvent(ctx, db.InsertOutboxEventParams{
			ID:          uuid.NewString(),
			AggregateID: updatedShop.ID,
			EventType:   events.SubjectShopStatusChanged,
			TraceID:     traceID,
			Traceparent: traceparent,
			PayloadJSON: payloadBytes,
		}); txErr != nil {
			return txErr
		}

		if _, txErr = q.InsertAuditLog(ctx, db.InsertAuditLogParams{
			ID:         uuid.NewString(),
			ActorID:    checkerID,
			ActorRole:  checkerRole,
			Action:     "admin.shop.suspend_approve",
			TargetType: "shop",
			TargetID:   updatedShop.ID,
			BeforeJSON: marshalAuditJSON(auditShopSnapshot(shop)),
			AfterJSON:  marshalAuditJSON(auditShopSnapshot(updatedShop)),
			Reason:     approval.Reason,
			IPAddress:  rawIP,
		}); txErr != nil {
			return txErr
		}

		if txErr = s.recordActivityTx(ctx, q, updatedShop.OwnerUserID, ActivityEventShopSuspended, ActivityCategoryStatus, rawIP, map[string]any{
			"shop_id":        updatedShop.ID,
			"from_status":    shop.Status,
			"to_status":      updatedShop.Status,
			"status_version": updatedShop.StatusVersion,
			"approval_id":    decidedApproval.ID,
			"actor_id":       approval.RequestedBy,
			"approver_id":    checkerID,
			"reason":         approval.Reason,
		}); txErr != nil {
			return txErr
		}

		if s.sec != nil {
			if rErr := s.sec.SetRevokedBefore(ctx, updatedShop.OwnerUserID, s.now().Unix(), s.revokedBeforeTTL()); rErr != nil {
				s.log.ErrorContext(ctx, "failed to set revoked_before on shop suspension",
					slog.String("shop_id", updatedShop.ID),
					slog.String("user_id", updatedShop.OwnerUserID),
					slog.String("error", rErr.Error()),
				)
				return status.Error(codes.Unavailable, "failed to revoke active sessions")
			}
		}

		resultShop = updatedShop
		emittedOutbox = true
		return nil
	})
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		return nil, status.Error(codes.Internal, "failed to decide shop suspension approval")
	}

	if emittedOutbox {
		s.notifyOutbox()
	}

	return &DecideShopSuspensionResult{
		Approval: decidedApproval,
		Shop:     resultShop,
	}, nil
}

// UnsuspendShop transitions a SUSPENDED shop back to ACTIVE, increments status_version,
// and enqueues a user.shop_status_changed outbox event atomically (F12).
func (s *Server) UnsuspendShop(ctx context.Context, input UnsuspendShopInput) (*db.Shop, error) {
	claims, err := s.requireCapability(ctx, auth.CapShopsUnsuspend)
	if err != nil {
		return nil, err
	}

	shopID := strings.TrimSpace(input.ShopID)
	if shopID == "" {
		return nil, status.Error(codes.InvalidArgument, "shop_id is required")
	}
	if !isValidUUID(shopID) {
		return nil, status.Error(codes.NotFound, "shop not found")
	}

	reason := strings.TrimSpace(input.Reason)
	rawIP := auditIPFromContext(ctx, claims, "")
	traceID, traceparent := outboxTraceFromContext(ctx)

	var updatedShop db.Shop
	err = s.store.WithTx(ctx, func(q db.Querier) error {
		shop, txErr := q.GetShopByIDForUpdate(ctx, shopID)
		if txErr != nil {
			if isNotFoundErr(txErr) {
				return status.Error(codes.NotFound, "shop not found")
			}
			return txErr
		}
		if shop.Status != ShopStatusSuspended {
			return status.Error(codes.FailedPrecondition, "only SUSPENDED shops can be unsuspended")
		}

		now := s.now()
		updatedShop, txErr = q.UnsuspendShop(ctx, db.UnsuspendShopParams{
			ID:        shop.ID,
			UpdatedAt: now,
		})
		if txErr != nil {
			if isNotFoundErr(txErr) {
				return status.Error(codes.FailedPrecondition, "only SUSPENDED shops can be unsuspended")
			}
			return txErr
		}

		payloadBytes, mErr := json.Marshal(events.ShopStatusChangedPayload{
			ShopID:      updatedShop.ID,
			OwnerUserID: updatedShop.OwnerUserID,
			ShopName:    updatedShop.Name,
			FromStatus:  shop.Status,
			ToStatus:    updatedShop.Status,
			Reason:      reason,
			ActorID:     claims.UserID(),
			ApproverID:  "",
			Version:     updatedShop.StatusVersion,
			OccurredAt:  now.UTC().Format(time.RFC3339),
		})
		if mErr != nil {
			return fmt.Errorf("marshal shop status changed payload: %w", mErr)
		}
		if txErr = q.InsertOutboxEvent(ctx, db.InsertOutboxEventParams{
			ID:          uuid.NewString(),
			AggregateID: updatedShop.ID,
			EventType:   events.SubjectShopStatusChanged,
			TraceID:     traceID,
			Traceparent: traceparent,
			PayloadJSON: payloadBytes,
		}); txErr != nil {
			return txErr
		}

		if _, txErr = q.InsertAuditLog(ctx, db.InsertAuditLogParams{
			ID:         uuid.NewString(),
			ActorID:    claims.UserID(),
			ActorRole:  claims.Role,
			Action:     "admin.shop.unsuspend",
			TargetType: "shop",
			TargetID:   updatedShop.ID,
			BeforeJSON: marshalAuditJSON(auditShopSnapshot(shop)),
			AfterJSON:  marshalAuditJSON(auditShopSnapshot(updatedShop)),
			Reason:     reason,
			IPAddress:  rawIP,
		}); txErr != nil {
			return txErr
		}

		return s.recordActivityTx(ctx, q, updatedShop.OwnerUserID, ActivityEventShopUnsuspended, ActivityCategoryStatus, rawIP, map[string]any{
			"shop_id":        updatedShop.ID,
			"from_status":    shop.Status,
			"to_status":      updatedShop.Status,
			"status_version": updatedShop.StatusVersion,
			"actor_id":       claims.UserID(),
			"actor_role":     claims.Role,
			"reason":         reason,
		})
	})
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		return nil, status.Error(codes.Internal, "failed to unsuspend shop")
	}

	s.notifyOutbox()
	return &updatedShop, nil
}

// ListUserActivities returns paginated user_activity_logs for a target user along with cod_rejected_count (F13).
func (s *Server) ListUserActivities(ctx context.Context, input ListUserActivitiesInput) (*ListUserActivitiesResult, error) {
	if _, err := s.requireCapability(ctx, auth.CapUsersRead); err != nil {
		return nil, err
	}

	userID := strings.TrimSpace(input.UserID)
	if userID == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	if !isValidUUID(userID) {
		return nil, status.Error(codes.NotFound, "user not found")
	}

	user, err := s.store.GetUserByID(ctx, userID)
	if err != nil {
		if isNotFoundErr(err) {
			return nil, status.Error(codes.NotFound, "user not found")
		}
		return nil, status.Error(codes.Internal, "failed to load user")
	}

	page, pageSize, err := normalizePagination(input.Page, input.PageSize)
	if err != nil {
		return nil, err
	}
	category := strings.ToLower(strings.TrimSpace(input.Category))

	entries, err := s.store.ListUserActivityLogs(ctx, db.ListUserActivityLogsParams{
		UserID:     user.ID,
		Category:   category,
		FromTime:   input.FromTime,
		ToTime:     input.ToTime,
		PageLimit:  pageSize,
		PageOffset: (page - 1) * pageSize,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list user activities")
	}
	total, err := s.store.CountUserActivityLogs(ctx, db.CountUserActivityLogsParams{
		UserID:   user.ID,
		Category: category,
		FromTime: input.FromTime,
		ToTime:   input.ToTime,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to count user activities")
	}

	return &ListUserActivitiesResult{
		Entries:          entries,
		Total:            total,
		CODRejectedCount: user.CODRejectedCount,
	}, nil
}

// PurgeExpiredUserActivities deletes user_activity_logs strictly older than 180 days in bounded batches (F13).
func (s *Server) PurgeExpiredUserActivities(ctx context.Context, batchSize int32) (int64, error) {
	if batchSize <= 0 {
		batchSize = defaultRetentionBatchSize
	}
	cutoff := s.now().Add(-ActivityLogRetention)
	var totalDeleted int64
	for {
		if ctx.Err() != nil {
			return totalDeleted, ctx.Err()
		}
		deleted, err := s.store.DeleteExpiredUserActivityLogs(ctx, db.DeleteExpiredUserActivityLogsParams{
			Cutoff:     cutoff,
			BatchLimit: batchSize,
		})
		if err != nil {
			return totalDeleted, fmt.Errorf("delete expired user activity logs: %w", err)
		}
		totalDeleted += deleted
		if deleted < int64(batchSize) {
			return totalDeleted, nil
		}
	}
}

// RunActivityRetentionLoop periodically purges user_activity_logs older than 180 days until ctx is cancelled.
func (s *Server) RunActivityRetentionLoop(ctx context.Context, interval time.Duration, batchSize int32) {
	if interval <= 0 {
		interval = defaultRetentionInterval
	}
	runOnce := func() {
		purgeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		n, err := s.PurgeExpiredUserActivities(purgeCtx, batchSize)
		if err != nil {
			if ctx.Err() == nil {
				s.log.WarnContext(ctx, "user activity retention cleanup failed", slog.String("error", err.Error()))
			}
			return
		}
		if n > 0 {
			s.log.InfoContext(ctx, "purged expired user activity logs", slog.Int64("deleted", n))
		}
	}

	runOnce()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runOnce()
		}
	}
}
