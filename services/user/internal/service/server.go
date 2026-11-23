package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/user/internal/db"
	"github.com/nexusmart/core/services/user/internal/events"
	"github.com/nexusmart/core/services/user/internal/password"
	"github.com/nexusmart/core/services/user/internal/security"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var errRefreshTokenConcurrentReplay = errors.New("refresh token concurrent replay")

// AddressCatalog validates Vietnamese 2-level province/ward codes (backed by ShippingService).
type AddressCatalog interface {
	ListProvinces(ctx context.Context, in *shippingv1.ListProvincesRequest, opts ...grpc.CallOption) (*shippingv1.ListProvincesResponse, error)
	ListWards(ctx context.Context, in *shippingv1.ListWardsRequest, opts ...grpc.CallOption) (*shippingv1.ListWardsResponse, error)
}

// Options configures auth/security timings and optional collaborators for Server.
type Options struct {
	AccessTTL              time.Duration
	RefreshTTL             time.Duration
	OTPTTL                 time.Duration
	OTPMaxResends          int
	LoginMaxFailedAttempts int
	LoginLockoutDuration   time.Duration
	AddressCatalog         AddressCatalog
}

func (o Options) withDefaults() Options {
	if o.AccessTTL <= 0 {
		o.AccessTTL = auth.DefaultAccessTTL
	}
	if o.RefreshTTL <= 0 {
		o.RefreshTTL = 168 * time.Hour
	}
	if o.OTPTTL <= 0 {
		o.OTPTTL = 10 * time.Minute
	}
	if o.OTPMaxResends <= 0 {
		o.OTPMaxResends = 3
	}
	if o.LoginMaxFailedAttempts <= 0 {
		o.LoginMaxFailedAttempts = 5
	}
	if o.LoginLockoutDuration <= 0 {
		o.LoginLockoutDuration = 15 * time.Minute
	}
	return o
}

// Server implements nexusmart.user.v1.UserServiceServer.
type Server struct {
	userv1.UnimplementedUserServiceServer

	store    db.Store
	sec      security.Store
	hasher   *password.Hasher
	authMgr  *auth.Manager
	pub      events.Publisher
	shipping AddressCatalog
	log      *slog.Logger
	opts     Options
}

// NewServer constructs a UserService gRPC server.
func NewServer(
	store db.Store,
	sec security.Store,
	hasher *password.Hasher,
	authMgr *auth.Manager,
	pub events.Publisher,
	log *slog.Logger,
	opts Options,
) *Server {
	return &Server{
		store:    store,
		sec:      sec,
		hasher:   hasher,
		authMgr:  authMgr,
		pub:      pub,
		shipping: opts.AddressCatalog,
		log:      log,
		opts:     opts.withDefaults(),
	}
}

func (s *Server) requireAuthClaims(ctx context.Context) (*auth.Claims, error) {
	claims, err := requireAuthClaims(ctx)
	if err != nil {
		return nil, err
	}
	if s.sec != nil && claims.Service == "" && claims.JTI() != "" {
		revoked, rErr := s.sec.IsJTIRevoked(ctx, claims.JTI())
		if rErr != nil {
			s.log.WarnContext(ctx, "failed to check revoked jti", slog.String("error", rErr.Error()))
		} else if revoked {
			return nil, status.Error(codes.Unauthenticated, "access token has been revoked")
		}
	}
	return claims, nil
}

func (s *Server) requireAdminClaims(ctx context.Context) (*auth.Claims, error) {
	claims, err := s.requireAuthClaims(ctx)
	if err != nil {
		return nil, err
	}
	if !auth.HasRole(claims, RoleAdmin, RoleSuperAdmin) {
		return nil, status.Error(codes.PermissionDenied, "admin role required")
	}
	return claims, nil
}

func (s *Server) resolveOwnerUserID(ctx context.Context, requestedUserID string) (string, error) {
	claims, err := s.requireAuthClaims(ctx)
	if err != nil {
		return "", err
	}
	target := strings.TrimSpace(requestedUserID)
	if target == "" {
		return claims.UserID(), nil
	}
	if target != claims.UserID() && !isAdminRole(claims.Role) {
		return "", status.Error(codes.PermissionDenied, "cannot access another user's resource")
	}
	return target, nil
}

func (s *Server) Register(ctx context.Context, req *userv1.RegisterRequest) (*userv1.RegisterResponse, error) {
	fullName := strings.TrimSpace(req.GetFullName())
	email := normalizeEmail(req.GetEmail())
	phone := strings.TrimSpace(req.GetPhone())
	plainPassword := req.GetPassword()

	if fullName == "" {
		return nil, status.Error(codes.InvalidArgument, "full_name is required")
	}
	if !isValidEmail(email) {
		return nil, status.Error(codes.InvalidArgument, "valid email is required")
	}
	if err := password.ValidateStrength(plainPassword); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	if _, err := s.store.GetUserByEmail(ctx, email); err == nil {
		return nil, status.Error(codes.AlreadyExists, "email is already registered")
	} else if !isNotFoundErr(err) {
		return nil, status.Error(codes.Internal, "failed to check email uniqueness")
	}

	if phone != "" {
		if _, err := s.store.GetUserByPhone(ctx, phone); err == nil {
			return nil, status.Error(codes.AlreadyExists, "phone is already registered")
		} else if !isNotFoundErr(err) {
			return nil, status.Error(codes.Internal, "failed to check phone uniqueness")
		}
	}

	hash, err := s.hasher.Hash(plainPassword)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to hash password")
	}

	userID := uuid.NewString()
	created, err := s.store.CreateUser(ctx, db.CreateUserParams{
		ID:            userID,
		FullName:      fullName,
		Email:         email,
		Phone:         phone,
		PasswordHash:  hash,
		Role:          RoleBuyer,
		Status:        StatusPendingVerification,
		PhoneVerified: false,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return nil, status.Error(codes.AlreadyExists, "email or phone already registered")
		}
		return nil, status.Error(codes.Internal, "failed to create user")
	}

	otpCode, err := generateOTP()
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to generate otp")
	}
	if err := s.sec.SaveOTP(ctx, email, otpCode, s.opts.OTPTTL); err != nil {
		return nil, status.Error(codes.Internal, "failed to persist otp")
	}

	if s.pub != nil {
		expiresAt := time.Now().UTC().Add(s.opts.OTPTTL).Format(time.RFC3339)
		if err := s.pub.PublishUserRegistered(ctx, events.UserRegisteredPayload{
			UserID:    created.ID,
			Email:     created.Email,
			FullName:  created.FullName,
			OTP:       otpCode,
			ExpiresAt: expiresAt,
		}); err != nil {
			s.log.ErrorContext(ctx, "failed to publish user.registered", slog.String("user_id", created.ID), slog.String("error", err.Error()))
			return nil, status.Error(codes.Internal, "failed to emit registration event")
		}
	}

	s.log.InfoContext(ctx, "user registered", slog.String("user_id", created.ID), slog.String("email", created.Email))
	return &userv1.RegisterResponse{
		UserId: created.ID,
		Email:  created.Email,
		Status: created.Status,
	}, nil
}

func (s *Server) VerifyOTP(ctx context.Context, req *userv1.VerifyOTPRequest) (*userv1.VerifyOTPResponse, error) {
	email := normalizeEmail(req.GetEmail())
	otp := strings.TrimSpace(req.GetOtp())
	if !isValidEmail(email) || otp == "" {
		return nil, status.Error(codes.InvalidArgument, "email and otp are required")
	}

	user, err := s.store.GetUserByEmail(ctx, email)
	if err != nil {
		if isNotFoundErr(err) {
			return nil, status.Error(codes.NotFound, "user not found")
		}
		return nil, status.Error(codes.Internal, "failed to lookup user")
	}
	if user.Status != StatusPendingVerification {
		return nil, status.Error(codes.FailedPrecondition, "account is not pending verification")
	}

	if err := s.sec.VerifyOTP(ctx, email, otp); err != nil {
		switch {
		case errors.Is(err, security.ErrOTPNotFound), errors.Is(err, security.ErrOTPInvalid):
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case errors.Is(err, security.ErrOTPTooManyAttempts):
			return nil, status.Error(codes.ResourceExhausted, err.Error())
		default:
			return nil, status.Error(codes.Internal, "failed to verify otp")
		}
	}

	updated, err := s.store.ActivateUser(ctx, db.ActivateUserParams{
		ID:            user.ID,
		PhoneVerified: user.PhoneVerified,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to activate user")
	}

	s.log.InfoContext(ctx, "user verified otp", slog.String("user_id", updated.ID))
	return &userv1.VerifyOTPResponse{
		Verified: true,
		User:     toProtoUser(updated),
	}, nil
}

func (s *Server) ResendOTP(ctx context.Context, req *userv1.ResendOTPRequest) (*userv1.ResendOTPResponse, error) {
	email := normalizeEmail(req.GetEmail())
	if !isValidEmail(email) {
		return nil, status.Error(codes.InvalidArgument, "valid email is required")
	}

	user, err := s.store.GetUserByEmail(ctx, email)
	if err != nil {
		if isNotFoundErr(err) {
			return nil, status.Error(codes.NotFound, "user not found")
		}
		return nil, status.Error(codes.Internal, "failed to lookup user")
	}
	if user.Status != StatusPendingVerification {
		return nil, status.Error(codes.FailedPrecondition, "account is not pending verification")
	}

	if err := s.sec.IncrementOTPResend(ctx, email, s.opts.OTPMaxResends); err != nil {
		if errors.Is(err, security.ErrOTPResendLimitReached) {
			return nil, status.Error(codes.ResourceExhausted, err.Error())
		}
		return nil, status.Error(codes.Internal, "failed to track otp resend")
	}

	otpCode, err := generateOTP()
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to generate otp")
	}
	if err := s.sec.SaveOTP(ctx, email, otpCode, s.opts.OTPTTL); err != nil {
		return nil, status.Error(codes.Internal, "failed to save otp")
	}

	if s.pub != nil {
		expiresAt := time.Now().UTC().Add(s.opts.OTPTTL).Format(time.RFC3339)
		if err := s.pub.PublishUserRegistered(ctx, events.UserRegisteredPayload{
			UserID:    user.ID,
			Email:     user.Email,
			FullName:  user.FullName,
			OTP:       otpCode,
			ExpiresAt: expiresAt,
		}); err != nil {
			s.log.ErrorContext(ctx, "failed to publish resent otp event", slog.String("user_id", user.ID), slog.String("error", err.Error()))
			return nil, status.Error(codes.Internal, "failed to emit otp event")
		}
	}

	return &userv1.ResendOTPResponse{Sent: true}, nil
}

func (s *Server) Login(ctx context.Context, req *userv1.LoginRequest) (*userv1.LoginResponse, error) {
	email := normalizeEmail(req.GetEmail())
	plainPassword := req.GetPassword()
	if !isValidEmail(email) || plainPassword == "" {
		return nil, status.Error(codes.InvalidArgument, "email and password are required")
	}

	locked, err := s.sec.IsLoginLocked(ctx, email)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to check login lock status")
	}
	if locked {
		return nil, status.Error(codes.ResourceExhausted, "account temporarily locked due to too many failed login attempts")
	}

	user, err := s.store.GetUserByEmail(ctx, email)
	if err != nil {
		if isNotFoundErr(err) {
			nowLocked, _, _ := s.sec.RecordLoginFailure(ctx, email, s.opts.LoginMaxFailedAttempts, s.opts.LoginLockoutDuration)
			if nowLocked {
				return nil, status.Error(codes.ResourceExhausted, "account temporarily locked due to too many failed login attempts")
			}
			return nil, status.Error(codes.Unauthenticated, "invalid email or password")
		}
		return nil, status.Error(codes.Internal, "failed to lookup user")
	}

	ok, err := s.hasher.Verify(plainPassword, user.PasswordHash)
	if err != nil || !ok {
		nowLocked, _, recErr := s.sec.RecordLoginFailure(ctx, email, s.opts.LoginMaxFailedAttempts, s.opts.LoginLockoutDuration)
		if recErr != nil {
			s.log.WarnContext(ctx, "failed to record login failure", slog.String("error", recErr.Error()))
		}
		if nowLocked {
			return nil, status.Error(codes.ResourceExhausted, "account temporarily locked due to too many failed login attempts")
		}
		return nil, status.Error(codes.Unauthenticated, "invalid email or password")
	}

	switch user.Status {
	case StatusPendingVerification:
		return nil, status.Error(codes.FailedPrecondition, "account pending verification")
	case StatusLocked:
		return nil, status.Error(codes.PermissionDenied, "account is locked")
	case StatusActive:
	default:
		return nil, status.Error(codes.PermissionDenied, "account is not active")
	}

	_ = s.sec.ClearLoginFailures(ctx, email)

	var shopID string
	if user.ShopID != nil {
		shopID = *user.ShopID
	}
	accessToken, _, _, err := s.authMgr.SignAccessToken(user.ID, user.Role, shopID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to sign access token")
	}

	refreshPlain, refreshHash, err := generateOpaqueToken()
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to generate refresh token")
	}
	familyID := uuid.NewString()
	_, err = s.store.CreateRefreshToken(ctx, db.CreateRefreshTokenParams{
		ID:        uuid.NewString(),
		UserID:    user.ID,
		FamilyID:  familyID,
		TokenHash: refreshHash,
		ExpiresAt: time.Now().UTC().Add(s.opts.RefreshTTL),
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to persist refresh token")
	}

	s.log.InfoContext(ctx, "user logged in", slog.String("user_id", user.ID), slog.String("role", user.Role))
	return &userv1.LoginResponse{
		AccessToken:      accessToken,
		RefreshToken:     refreshPlain,
		ExpiresInSeconds: int64(s.opts.AccessTTL.Seconds()),
		User:             toProtoUser(user),
	}, nil
}

func (s *Server) RefreshToken(ctx context.Context, req *userv1.RefreshTokenRequest) (*userv1.RefreshTokenResponse, error) {
	rawToken := strings.TrimSpace(req.GetRefreshToken())
	if rawToken == "" {
		return nil, status.Error(codes.InvalidArgument, "refresh_token is required")
	}

	tokenHash := hashToken(rawToken)
	existing, err := s.store.GetRefreshTokenByHash(ctx, tokenHash)
	if err != nil {
		if isNotFoundErr(err) {
			return nil, status.Error(codes.Unauthenticated, "invalid refresh token")
		}
		return nil, status.Error(codes.Internal, "failed to lookup refresh token")
	}

	// Strict reuse detection: if an already-revoked token is replayed, revoke the entire token family.
	if existing.RevokedAt != nil {
		if err := s.store.RevokeRefreshTokenFamily(ctx, existing.FamilyID); err != nil {
			s.log.ErrorContext(ctx, "failed to revoke refresh token family on reuse", slog.String("family_id", existing.FamilyID), slog.String("error", err.Error()))
		}
		s.log.WarnContext(ctx, "refresh token reuse detected; family revoked", slog.String("user_id", existing.UserID), slog.String("family_id", existing.FamilyID))
		return nil, status.Error(codes.Unauthenticated, "refresh token reuse detected; session revoked")
	}

	if time.Now().UTC().After(existing.ExpiresAt) {
		_ = s.store.RevokeRefreshTokenByHash(ctx, tokenHash)
		return nil, status.Error(codes.Unauthenticated, "refresh token expired")
	}

	user, err := s.store.GetUserByID(ctx, existing.UserID)
	if err != nil {
		if isNotFoundErr(err) {
			return nil, status.Error(codes.Unauthenticated, "user not found")
		}
		return nil, status.Error(codes.Internal, "failed to lookup user")
	}
	if user.Status != StatusActive {
		_ = s.store.RevokeRefreshTokenFamily(ctx, existing.FamilyID)
		return nil, status.Error(codes.PermissionDenied, "account is not active")
	}

	newPlain, newHash, err := generateOpaqueToken()
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to generate refresh token")
	}
	newID := uuid.NewString()
	newExpires := time.Now().UTC().Add(s.opts.RefreshTTL)

	err = s.store.WithTx(ctx, func(q db.Querier) error {
		rows, txErr := q.RevokeRefreshTokenWithReplacement(ctx, db.RevokeRefreshTokenWithReplacementParams{
			ID:           existing.ID,
			ReplacedByID: &newID,
		})
		if txErr != nil {
			return txErr
		}
		if rows == 0 {
			// Return sentinel so WithTx rolls back cleanly; family revocation runs outside the rolled-back tx.
			return errRefreshTokenConcurrentReplay
		}
		_, txErr = q.CreateRefreshToken(ctx, db.CreateRefreshTokenParams{
			ID:        newID,
			UserID:    user.ID,
			FamilyID:  existing.FamilyID,
			TokenHash: newHash,
			ExpiresAt: newExpires,
		})
		return txErr
	})
	if err != nil {
		if errors.Is(err, errRefreshTokenConcurrentReplay) {
			if revErr := s.store.RevokeRefreshTokenFamily(ctx, existing.FamilyID); revErr != nil {
				s.log.ErrorContext(ctx, "failed to revoke refresh token family on concurrent replay", slog.String("family_id", existing.FamilyID), slog.String("error", revErr.Error()))
			}
			s.log.WarnContext(ctx, "concurrent refresh token replay detected; family revoked", slog.String("user_id", existing.UserID), slog.String("family_id", existing.FamilyID))
			return nil, status.Error(codes.Unauthenticated, "refresh token reuse detected; session revoked")
		}
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		return nil, status.Error(codes.Internal, "failed to rotate refresh token")
	}

	var shopID string
	if user.ShopID != nil {
		shopID = *user.ShopID
	}
	accessToken, _, _, err := s.authMgr.SignAccessToken(user.ID, user.Role, shopID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to sign access token")
	}

	return &userv1.RefreshTokenResponse{
		AccessToken:      accessToken,
		RefreshToken:     newPlain,
		ExpiresInSeconds: int64(s.opts.AccessTTL.Seconds()),
	}, nil
}

func (s *Server) Logout(ctx context.Context, req *userv1.LogoutRequest) (*userv1.LogoutResponse, error) {
	claims, hasClaims := auth.ClaimsFromContext(ctx)
	if rawToken := strings.TrimSpace(req.GetRefreshToken()); rawToken != "" {
		tokenHash := hashToken(rawToken)
		existing, err := s.store.GetRefreshTokenByHash(ctx, tokenHash)
		if err == nil {
			if hasClaims && !isAdminRole(claims.Role) && existing.UserID != claims.UserID() {
				return nil, status.Error(codes.PermissionDenied, "cannot revoke another user's refresh token")
			}
			_ = s.store.RevokeRefreshTokenFamily(ctx, existing.FamilyID)
		}
	}

	if hasClaims && claims.JTI() != "" {
		ttl := s.opts.AccessTTL
		if claims.ExpiresAt != nil {
			if rem := time.Until(claims.ExpiresAt.Time); rem > 0 {
				ttl = rem
			}
		}
		_ = s.sec.RevokeJTI(ctx, claims.JTI(), ttl)
	}

	return &userv1.LogoutResponse{Ok: true}, nil
}

func (s *Server) GetProfile(ctx context.Context, req *userv1.GetProfileRequest) (*userv1.GetProfileResponse, error) {
	targetID := strings.TrimSpace(req.GetUserId())
	if _, ok := auth.ClaimsFromContext(ctx); ok {
		claims, err := s.requireAuthClaims(ctx)
		if err != nil {
			return nil, err
		}
		if targetID == "" {
			targetID = claims.UserID()
		}
		if targetID != claims.UserID() && !isAdminRole(claims.Role) {
			return nil, status.Error(codes.PermissionDenied, "cannot read another user's profile")
		}
	}
	if targetID == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	if !isValidUUID(targetID) {
		return nil, status.Error(codes.NotFound, "user not found")
	}

	user, err := s.store.GetUserByID(ctx, targetID)
	if err != nil {
		if isNotFoundErr(err) {
			return nil, status.Error(codes.NotFound, "user not found")
		}
		return nil, status.Error(codes.Internal, "failed to load profile")
	}
	return &userv1.GetProfileResponse{User: toProtoUser(user)}, nil
}

func (s *Server) UpdateProfile(ctx context.Context, req *userv1.UpdateProfileRequest) (*userv1.UpdateProfileResponse, error) {
	targetID, err := s.resolveOwnerUserID(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	if !isValidUUID(targetID) {
		return nil, status.Error(codes.NotFound, "user not found")
	}

	existing, err := s.store.GetUserByID(ctx, targetID)
	if err != nil {
		if isNotFoundErr(err) {
			return nil, status.Error(codes.NotFound, "user not found")
		}
		return nil, status.Error(codes.Internal, "failed to load user")
	}

	fullName := existing.FullName
	if v := strings.TrimSpace(req.GetFullName()); v != "" {
		fullName = v
	}
	phone := existing.Phone
	phoneVerified := existing.PhoneVerified
	if v := strings.TrimSpace(req.GetPhone()); v != "" && v != existing.Phone {
		if other, pErr := s.store.GetUserByPhone(ctx, v); pErr == nil && other.ID != existing.ID {
			return nil, status.Error(codes.AlreadyExists, "phone is already in use")
		} else if pErr != nil && !isNotFoundErr(pErr) {
			return nil, status.Error(codes.Internal, "failed to check phone uniqueness")
		}
		phone = v
		phoneVerified = false
	}
	avatarURL := existing.AvatarURL
	if v := strings.TrimSpace(req.GetAvatarUrl()); v != "" {
		avatarURL = v
	}

	updated, err := s.store.UpdateUserProfile(ctx, db.UpdateUserProfileParams{
		ID:            existing.ID,
		FullName:      fullName,
		Phone:         phone,
		AvatarURL:     avatarURL,
		PhoneVerified: phoneVerified,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return nil, status.Error(codes.AlreadyExists, "phone is already in use")
		}
		return nil, status.Error(codes.Internal, "failed to update profile")
	}

	return &userv1.UpdateProfileResponse{User: toProtoUser(updated)}, nil
}

func (s *Server) ValidateUser(ctx context.Context, req *userv1.ValidateUserRequest) (*userv1.ValidateUserResponse, error) {
	claims, err := s.requireAuthClaims(ctx)
	if err != nil {
		return nil, err
	}
	if claims.Service != "" {
		return nil, status.Error(codes.PermissionDenied, "service identity not allowed for ValidateUser")
	}

	targetID := strings.TrimSpace(req.GetUserId())
	if targetID == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	if targetID != claims.UserID() && !isAdminRole(claims.Role) {
		return nil, status.Error(codes.PermissionDenied, "cannot validate another user")
	}
	if !isValidUUID(targetID) {
		return &userv1.ValidateUserResponse{Valid: false}, nil
	}

	user, err := s.store.GetUserByID(ctx, targetID)
	if err != nil {
		if isNotFoundErr(err) {
			return &userv1.ValidateUserResponse{Valid: false}, nil
		}
		return nil, status.Error(codes.Internal, "failed to validate user")
	}

	var shopID string
	if user.ShopID != nil {
		shopID = *user.ShopID
	}
	return &userv1.ValidateUserResponse{
		Valid:            user.Status == StatusActive,
		Role:             roleToProto(user.Role),
		Status:           statusToProto(user.Status),
		ShopId:           shopID,
		PhoneVerified:    user.PhoneVerified,
		CodRejectedCount: user.CODRejectedCount,
	}, nil
}

func (s *Server) AdminListUsers(ctx context.Context, req *userv1.AdminListUsersRequest) (*userv1.AdminListUsersResponse, error) {
	if _, err := s.requireAdminClaims(ctx); err != nil {
		return nil, err
	}

	page, pageSize := normalizePagination(req.GetPage(), req.GetPageSize())
	query := strings.TrimSpace(req.GetQuery())
	role := roleFromProto(req.GetRole())
	st := statusFromProto(req.GetStatus())

	users, err := s.store.ListUsers(ctx, db.ListUsersParams{
		Query:      query,
		Role:       role,
		Status:     st,
		PageLimit:  pageSize,
		PageOffset: (page - 1) * pageSize,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list users")
	}
	total, err := s.store.CountUsers(ctx, db.CountUsersParams{
		Query:  query,
		Role:   role,
		Status: st,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to count users")
	}

	out := make([]*userv1.User, 0, len(users))
	for _, u := range users {
		out = append(out, toProtoUser(u))
	}
	return &userv1.AdminListUsersResponse{
		Users: out,
		Total: total,
	}, nil
}

func (s *Server) AdminUpdateUserStatus(ctx context.Context, req *userv1.AdminUpdateUserStatusRequest) (*userv1.AdminUpdateUserStatusResponse, error) {
	claims, err := s.requireAdminClaims(ctx)
	if err != nil {
		return nil, err
	}

	targetID := strings.TrimSpace(req.GetTargetUserId())
	newStatus := statusFromProto(req.GetStatus())
	if targetID == "" || (newStatus != StatusActive && newStatus != StatusLocked) {
		return nil, status.Error(codes.InvalidArgument, "target_user_id and valid status (ACTIVE or LOCKED) are required")
	}
	if !isValidUUID(targetID) {
		return nil, status.Error(codes.NotFound, "target user not found")
	}

	actorID := claims.UserID()
	if actorID == targetID && newStatus == StatusLocked {
		return nil, status.Error(codes.FailedPrecondition, "admin cannot lock own account")
	}

	target, err := s.store.GetUserByID(ctx, targetID)
	if err != nil {
		if isNotFoundErr(err) {
			return nil, status.Error(codes.NotFound, "target user not found")
		}
		return nil, status.Error(codes.Internal, "failed to load target user")
	}

	if target.Role == RoleSuperAdmin && !strings.EqualFold(claims.Role, RoleSuperAdmin) {
		return nil, status.Error(codes.PermissionDenied, "only super_admin can modify a super_admin account")
	}

	var updated db.User
	err = s.store.WithTx(ctx, func(q db.Querier) error {
		if isAdminRole(target.Role) && target.Status == StatusActive && newStatus == StatusLocked {
			activeAdmins, txErr := q.CountActiveAdmins(ctx)
			if txErr != nil {
				return txErr
			}
			if activeAdmins <= 1 {
				return status.Error(codes.FailedPrecondition, "cannot lock the last active admin")
			}
		}
		var txErr error
		updated, txErr = q.UpdateUserStatus(ctx, db.UpdateUserStatusParams{
			ID:     target.ID,
			Status: newStatus,
		})
		if txErr != nil {
			return txErr
		}
		if newStatus == StatusLocked {
			if txErr = q.RevokeAllUserRefreshTokens(ctx, target.ID); txErr != nil {
				return txErr
			}
		}
		_, txErr = q.InsertAuditLog(ctx, db.InsertAuditLogParams{
			ID:         uuid.NewString(),
			ActorID:    actorID,
			ActorRole:  claims.Role,
			Action:     "admin.user.update_status",
			TargetType: "user",
			TargetID:   target.ID,
			BeforeJSON: marshalAuditJSON(auditUserSnapshot(target)),
			AfterJSON:  marshalAuditJSON(auditUserSnapshot(updated)),
			Reason:     strings.TrimSpace(req.GetReason()),
			IPAddress:  auditIPFromContext(ctx, claims, ""),
		})
		return txErr
	})
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		return nil, status.Error(codes.Internal, "failed to update user status")
	}

	return &userv1.AdminUpdateUserStatusResponse{User: toProtoUser(updated)}, nil
}

func (s *Server) AdminUpdateUserRole(ctx context.Context, req *userv1.AdminUpdateUserRoleRequest) (*userv1.AdminUpdateUserRoleResponse, error) {
	claims, err := s.requireAdminClaims(ctx)
	if err != nil {
		return nil, err
	}

	targetID := strings.TrimSpace(req.GetTargetUserId())
	newRole := roleFromProto(req.GetRole())
	if targetID == "" || newRole == "" {
		return nil, status.Error(codes.InvalidArgument, "target_user_id and valid role are required")
	}
	if !isValidUUID(targetID) {
		return nil, status.Error(codes.NotFound, "target user not found")
	}

	if newRole == RoleSuperAdmin && !strings.EqualFold(claims.Role, RoleSuperAdmin) {
		return nil, status.Error(codes.PermissionDenied, "only super_admin can assign super_admin role")
	}

	actorID := claims.UserID()
	target, err := s.store.GetUserByID(ctx, targetID)
	if err != nil {
		if isNotFoundErr(err) {
			return nil, status.Error(codes.NotFound, "target user not found")
		}
		return nil, status.Error(codes.Internal, "failed to load target user")
	}

	if target.Role == RoleSuperAdmin && !strings.EqualFold(claims.Role, RoleSuperAdmin) {
		return nil, status.Error(codes.PermissionDenied, "only super_admin can modify a super_admin account")
	}

	if actorID == target.ID && isAdminRole(target.Role) && !isAdminRole(newRole) {
		return nil, status.Error(codes.FailedPrecondition, "admin cannot demote own admin role")
	}

	var updated db.User
	err = s.store.WithTx(ctx, func(q db.Querier) error {
		if isAdminRole(target.Role) && !isAdminRole(newRole) && target.Status == StatusActive {
			activeAdmins, txErr := q.CountActiveAdmins(ctx)
			if txErr != nil {
				return txErr
			}
			if activeAdmins <= 1 {
				return status.Error(codes.FailedPrecondition, "cannot demote the last active admin")
			}
		}
		var txErr error
		updated, txErr = q.UpdateUserRole(ctx, db.UpdateUserRoleParams{
			ID:   target.ID,
			Role: newRole,
		})
		if txErr != nil {
			return txErr
		}
		_, txErr = q.InsertAuditLog(ctx, db.InsertAuditLogParams{
			ID:         uuid.NewString(),
			ActorID:    actorID,
			ActorRole:  claims.Role,
			Action:     "admin.user.update_role",
			TargetType: "user",
			TargetID:   target.ID,
			BeforeJSON: marshalAuditJSON(auditUserSnapshot(target)),
			AfterJSON:  marshalAuditJSON(auditUserSnapshot(updated)),
			Reason:     strings.TrimSpace(req.GetReason()),
			IPAddress:  auditIPFromContext(ctx, claims, ""),
		})
		return txErr
	})
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		return nil, status.Error(codes.Internal, "failed to update user role")
	}

	return &userv1.AdminUpdateUserRoleResponse{User: toProtoUser(updated)}, nil
}

func (s *Server) RegisterShop(ctx context.Context, req *userv1.RegisterShopRequest) (*userv1.RegisterShopResponse, error) {
	ownerID, err := s.resolveOwnerUserID(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	if !isValidUUID(ownerID) {
		return nil, status.Error(codes.NotFound, "user not found")
	}

	shopName := strings.TrimSpace(req.GetName())
	if shopName == "" {
		return nil, status.Error(codes.InvalidArgument, "shop name is required")
	}

	pickupAddressID := strings.TrimSpace(req.GetPickupAddressId())
	if pickupAddressID == "" {
		return nil, status.Error(codes.InvalidArgument, "pickup_address_id is required")
	}
	if !isValidUUID(pickupAddressID) {
		return nil, status.Error(codes.InvalidArgument, "invalid pickup_address_id")
	}
	addr, err := s.store.GetAddressByID(ctx, pickupAddressID)
	if err != nil {
		if isNotFoundErr(err) {
			return nil, status.Error(codes.InvalidArgument, "pickup address not found")
		}
		return nil, status.Error(codes.Internal, "failed to validate pickup address")
	}
	if addr.UserID != ownerID {
		return nil, status.Error(codes.PermissionDenied, "pickup address does not belong to user")
	}

	contactPhone := normalizeVNPhone(req.GetContactPhone())
	if !isValidVNPhone(contactPhone) {
		return nil, status.Error(codes.InvalidArgument, "valid Vietnamese contact_phone is required")
	}

	businessLicenseDoc := strings.TrimSpace(req.GetBusinessLicenseDoc())
	if businessLicenseDoc == "" {
		return nil, status.Error(codes.InvalidArgument, "business_license_doc is required")
	}

	var existingShop *db.Shop
	if existing, err := s.store.GetShopByOwnerID(ctx, ownerID); err == nil {
		isResubmittable := existing.Status == ShopStatusRejected ||
			(existing.Status == ShopStatusPending && strings.TrimSpace(existing.RejectionReason) != "")
		if !isResubmittable {
			if existing.Status == ShopStatusSuspended {
				return nil, status.Error(codes.FailedPrecondition, "shop is suspended")
			}
			return nil, status.Error(codes.AlreadyExists, "user already has a pending or active shop")
		}
		existingCopy := existing
		existingShop = &existingCopy
	} else if !isNotFoundErr(err) {
		return nil, status.Error(codes.Internal, "failed to check existing shop")
	}

	if other, err := s.store.GetShopByName(ctx, shopName); err == nil {
		if existingShop == nil || other.ID != existingShop.ID {
			return nil, status.Error(codes.AlreadyExists, "shop name is already taken")
		}
	} else if !isNotFoundErr(err) {
		return nil, status.Error(codes.Internal, "failed to check shop name uniqueness")
	}

	shopID := uuid.NewString()
	if existingShop != nil {
		shopID = existingShop.ID
	}
	rawSlug := strings.TrimSpace(req.GetSlug())
	var shopSlug string
	if rawSlug != "" {
		shopSlug = normalizeShopSlug(rawSlug)
		if shopSlug == "" {
			return nil, status.Error(codes.InvalidArgument, "shop slug must contain letters or digits")
		}
		if other, err := s.store.GetShopBySlug(ctx, shopSlug); err == nil {
			if existingShop == nil || other.ID != existingShop.ID {
				return nil, status.Error(codes.AlreadyExists, "shop slug is already taken")
			}
		} else if !isNotFoundErr(err) {
			return nil, status.Error(codes.Internal, "failed to check shop slug uniqueness")
		}
	} else {
		base := slugifyShop(shopName, "shop-"+shopID[:8])
		shopSlug = base
		if other, err := s.store.GetShopBySlug(ctx, shopSlug); err == nil {
			if existingShop == nil || other.ID != existingShop.ID {
				shopSlug = base + "-" + shopID[:8]
			}
		} else if !isNotFoundErr(err) {
			return nil, status.Error(codes.Internal, "failed to check shop slug uniqueness")
		}
	}

	var saved db.Shop
	if existingShop != nil {
		saved, err = s.store.ResubmitShop(ctx, db.ResubmitShopParams{
			ID:                 existingShop.ID,
			Name:               shopName,
			Slug:               shopSlug,
			Description:        strings.TrimSpace(req.GetDescription()),
			PickupAddressID:    pickupAddressID,
			ContactPhone:       contactPhone,
			BusinessLicenseDoc: businessLicenseDoc,
		})
		if isNotFoundErr(err) {
			return nil, status.Error(codes.FailedPrecondition, "shop is no longer eligible for resubmission")
		}
	} else {
		saved, err = s.store.CreateShop(ctx, db.CreateShopParams{
			ID:                 shopID,
			OwnerUserID:        ownerID,
			Name:               shopName,
			Slug:               shopSlug,
			Description:        strings.TrimSpace(req.GetDescription()),
			PickupAddressID:    pickupAddressID,
			ContactPhone:       contactPhone,
			BusinessLicenseDoc: businessLicenseDoc,
			Status:             ShopStatusPending,
		})
	}
	if err != nil {
		if isUniqueViolation(err) {
			return nil, status.Error(codes.AlreadyExists, "shop name or slug is already taken")
		}
		return nil, status.Error(codes.Internal, "failed to register shop")
	}

	return &userv1.RegisterShopResponse{Shop: toProtoShop(saved)}, nil
}

func (s *Server) GetShop(ctx context.Context, req *userv1.GetShopRequest) (*userv1.GetShopResponse, error) {
	shopID := strings.TrimSpace(req.GetShopId())
	slug := strings.ToLower(strings.TrimSpace(req.GetSlug()))
	ownerID := strings.TrimSpace(req.GetOwnerUserId())
	if shopID == "" && slug == "" && ownerID == "" {
		return nil, status.Error(codes.InvalidArgument, "shop_id, slug, or owner_user_id is required")
	}

	var (
		shop db.Shop
		err  error
	)
	switch {
	case shopID != "":
		if !isValidUUID(shopID) {
			return nil, status.Error(codes.NotFound, "shop not found")
		}
		shop, err = s.store.GetShopByID(ctx, shopID)
	case slug != "":
		shop, err = s.store.GetShopBySlug(ctx, slug)
	default:
		if !isValidUUID(ownerID) {
			return nil, status.Error(codes.NotFound, "shop not found")
		}
		shop, err = s.store.GetShopByOwnerID(ctx, ownerID)
	}
	if err != nil {
		if isNotFoundErr(err) {
			return nil, status.Error(codes.NotFound, "shop not found")
		}
		return nil, status.Error(codes.Internal, "failed to load shop")
	}

	return &userv1.GetShopResponse{Shop: toProtoShop(shop)}, nil
}

func (s *Server) ListPublicShops(ctx context.Context, req *userv1.ListPublicShopsRequest) (*userv1.ListPublicShopsResponse, error) {
	page, pageSize := normalizePagination(req.GetPage(), req.GetPageSize())

	shops, err := s.store.ListShops(ctx, db.ListShopsParams{
		Status:     ShopStatusActive,
		PageLimit:  pageSize,
		PageOffset: (page - 1) * pageSize,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list public shops")
	}
	total, err := s.store.CountShops(ctx, ShopStatusActive)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to count public shops")
	}

	out := make([]*userv1.Shop, 0, len(shops))
	for _, sh := range shops {
		out = append(out, toPublicProtoShop(sh))
	}
	return &userv1.ListPublicShopsResponse{
		Shops: out,
		Total: total,
	}, nil
}

func (s *Server) AdminListShops(ctx context.Context, req *userv1.AdminListShopsRequest) (*userv1.AdminListShopsResponse, error) {
	if _, err := s.requireAdminClaims(ctx); err != nil {
		return nil, err
	}

	page, pageSize := normalizePagination(req.GetPage(), req.GetPageSize())
	st := shopStatusFromProto(req.GetStatus())

	shops, err := s.store.ListShops(ctx, db.ListShopsParams{
		Status:     st,
		PageLimit:  pageSize,
		PageOffset: (page - 1) * pageSize,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list shops")
	}
	total, err := s.store.CountShops(ctx, st)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to count shops")
	}

	out := make([]*userv1.Shop, 0, len(shops))
	for _, sh := range shops {
		out = append(out, toProtoShop(sh))
	}
	return &userv1.AdminListShopsResponse{
		Shops: out,
		Total: total,
	}, nil
}

func (s *Server) AdminReviewShop(ctx context.Context, req *userv1.AdminReviewShopRequest) (*userv1.AdminReviewShopResponse, error) {
	claims, err := s.requireAdminClaims(ctx)
	if err != nil {
		return nil, err
	}

	shopID := strings.TrimSpace(req.GetShopId())
	if shopID == "" {
		return nil, status.Error(codes.InvalidArgument, "shop_id is required")
	}
	if !isValidUUID(shopID) {
		return nil, status.Error(codes.NotFound, "shop not found")
	}

	var (
		newStatus    string
		decisionName string
		reason       = strings.TrimSpace(req.GetReason())
	)
	switch req.GetDecision() {
	case userv1.ReviewDecision_REVIEW_DECISION_APPROVE:
		newStatus = ShopStatusActive
		decisionName = "APPROVE"
		reason = ""
	case userv1.ReviewDecision_REVIEW_DECISION_REJECT:
		if reason == "" {
			return nil, status.Error(codes.InvalidArgument, "reason is required when rejecting a shop")
		}
		newStatus = ShopStatusRejected
		decisionName = "REJECT"
	case userv1.ReviewDecision_REVIEW_DECISION_REQUEST_INFO:
		if reason == "" {
			return nil, status.Error(codes.InvalidArgument, "reason is required when requesting additional info")
		}
		newStatus = ShopStatusPending
		decisionName = "REQUEST_INFO"
	default:
		return nil, status.Error(codes.InvalidArgument, "valid review decision is required")
	}

	existing, err := s.store.GetShopByID(ctx, shopID)
	if err != nil {
		if isNotFoundErr(err) {
			return nil, status.Error(codes.NotFound, "shop not found")
		}
		return nil, status.Error(codes.Internal, "failed to load shop")
	}
	if existing.Status != ShopStatusPending {
		return nil, status.Error(codes.FailedPrecondition, "only PENDING shops can be reviewed")
	}

	var updated db.Shop
	err = s.store.WithTx(ctx, func(q db.Querier) error {
		var txErr error
		updated, txErr = q.UpdateShopReview(ctx, db.UpdateShopReviewParams{
			ID:              existing.ID,
			Status:          newStatus,
			RejectionReason: reason,
		})
		if txErr != nil {
			if isNotFoundErr(txErr) {
				return status.Error(codes.FailedPrecondition, "only PENDING shops can be reviewed")
			}
			return txErr
		}
		if req.GetDecision() == userv1.ReviewDecision_REVIEW_DECISION_APPROVE {
			sid := updated.ID
			if _, txErr = q.GrantUserSellerShop(ctx, db.GrantUserSellerShopParams{
				ID:     updated.OwnerUserID,
				ShopID: &sid,
			}); txErr != nil {
				return txErr
			}
		}
		_, txErr = q.InsertAuditLog(ctx, db.InsertAuditLogParams{
			ID:         uuid.NewString(),
			ActorID:    claims.UserID(),
			ActorRole:  claims.Role,
			Action:     "admin.shop.review",
			TargetType: "shop",
			TargetID:   updated.ID,
			BeforeJSON: marshalAuditJSON(auditShopSnapshot(existing)),
			AfterJSON:  marshalAuditJSON(auditShopSnapshot(updated)),
			Reason:     strings.TrimSpace(req.GetReason()),
			IPAddress:  auditIPFromContext(ctx, claims, ""),
		})
		return txErr
	})
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		return nil, status.Error(codes.Internal, "failed to review shop")
	}

	if s.pub != nil {
		if pubErr := s.pub.PublishShopReviewed(ctx, events.ShopReviewedPayload{
			ShopID:      updated.ID,
			OwnerUserID: updated.OwnerUserID,
			ShopName:    updated.Name,
			Decision:    decisionName,
			Reason:      strings.TrimSpace(req.GetReason()),
		}); pubErr != nil {
			s.log.ErrorContext(ctx, "failed to publish user.shop_reviewed", slog.String("shop_id", updated.ID), slog.String("error", pubErr.Error()))
		}
	}

	return &userv1.AdminReviewShopResponse{Shop: toProtoShop(updated)}, nil
}

func (s *Server) validateProvinceWard(ctx context.Context, provinceCode, wardCode, provinceName, wardName string) (string, string, error) {
	if s.shipping == nil {
		return provinceName, wardName, nil
	}

	callListProvinces := func() (*shippingv1.ListProvincesResponse, error) {
		var lastErr error
		backoff := 50 * time.Millisecond
		for attempt := 0; attempt < 2; attempt++ {
			callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			resp, err := s.shipping.ListProvinces(callCtx, &shippingv1.ListProvincesRequest{Query: provinceCode})
			cancel()
			if err == nil {
				return resp, nil
			}
			lastErr = err
			code := status.Code(err)
			if code != codes.Unavailable && code != codes.DeadlineExceeded {
				return nil, err
			}
			if attempt == 0 {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(backoff):
				}
			}
		}
		return nil, lastErr
	}

	provResp, err := callListProvinces()
	if err != nil {
		s.log.WarnContext(ctx, "shipping ListProvinces failed", slog.String("error", err.Error()))
		return "", "", status.Error(codes.Unavailable, "failed to validate province_code with shipping service")
	}
	var matchedProv *shippingv1.Province
	for _, p := range provResp.GetProvinces() {
		if p.GetCode() == provinceCode {
			matchedProv = p
			break
		}
	}
	if matchedProv == nil {
		return "", "", status.Error(codes.InvalidArgument, "invalid province_code")
	}

	callListWards := func() (*shippingv1.ListWardsResponse, error) {
		var lastErr error
		backoff := 50 * time.Millisecond
		for attempt := 0; attempt < 2; attempt++ {
			callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			resp, err := s.shipping.ListWards(callCtx, &shippingv1.ListWardsRequest{
				ProvinceCode: provinceCode,
				Query:        wardCode,
			})
			cancel()
			if err == nil {
				return resp, nil
			}
			lastErr = err
			code := status.Code(err)
			if code != codes.Unavailable && code != codes.DeadlineExceeded {
				return nil, err
			}
			if attempt == 0 {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(backoff):
				}
			}
		}
		return nil, lastErr
	}

	wardResp, err := callListWards()
	if err != nil {
		s.log.WarnContext(ctx, "shipping ListWards failed", slog.String("error", err.Error()))
		return "", "", status.Error(codes.Unavailable, "failed to validate ward_code with shipping service")
	}
	var matchedWard *shippingv1.Ward
	for _, w := range wardResp.GetWards() {
		if w.GetCode() == wardCode && w.GetProvinceCode() == provinceCode {
			matchedWard = w
			break
		}
	}
	if matchedWard == nil {
		return "", "", status.Error(codes.InvalidArgument, "invalid ward_code for province")
	}

	if provinceName == "" {
		provinceName = matchedProv.GetName()
	}
	if wardName == "" {
		wardName = matchedWard.GetName()
	}
	return provinceName, wardName, nil
}

func (s *Server) CreateAddress(ctx context.Context, req *userv1.CreateAddressRequest) (*userv1.CreateAddressResponse, error) {
	ownerID, err := s.resolveOwnerUserID(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	if !isValidUUID(ownerID) {
		return nil, status.Error(codes.NotFound, "user not found")
	}

	recipientName := strings.TrimSpace(req.GetRecipientName())
	recipientPhone := normalizeVNPhone(req.GetRecipientPhone())
	provinceCode := strings.TrimSpace(req.GetProvinceCode())
	wardCode := strings.TrimSpace(req.GetWardCode())
	streetAddress := strings.TrimSpace(req.GetStreetAddress())

	if recipientName == "" || recipientPhone == "" || provinceCode == "" || wardCode == "" || streetAddress == "" {
		return nil, status.Error(codes.InvalidArgument, "recipient_name, recipient_phone, province_code, ward_code, and street_address are required")
	}
	if !isValidVNPhone(recipientPhone) {
		return nil, status.Error(codes.InvalidArgument, "valid Vietnamese recipient_phone is required")
	}

	provinceName, wardName, err := s.validateProvinceWard(
		ctx,
		provinceCode,
		wardCode,
		strings.TrimSpace(req.GetProvinceName()),
		strings.TrimSpace(req.GetWardName()),
	)
	if err != nil {
		return nil, err
	}

	var created db.Address
	err = s.store.WithTx(ctx, func(q db.Querier) error {
		if req.GetIsDefault() {
			if txErr := q.ClearUserDefaultAddresses(ctx, ownerID); txErr != nil {
				return txErr
			}
		}
		var txErr error
		created, txErr = q.CreateAddress(ctx, db.CreateAddressParams{
			ID:                 uuid.NewString(),
			UserID:             ownerID,
			RecipientName:      recipientName,
			RecipientPhone:     recipientPhone,
			ProvinceCode:       provinceCode,
			ProvinceName:       provinceName,
			WardCode:           wardCode,
			WardName:           wardName,
			StreetAddress:      streetAddress,
			LegacyProvinceCode: strings.TrimSpace(req.GetLegacyProvince()),
			LegacyDistrictCode: strings.TrimSpace(req.GetLegacyDistrict()),
			LegacyWardCode:     strings.TrimSpace(req.GetLegacyWard()),
			IsDefault:          req.GetIsDefault(),
		})
		return txErr
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to create address")
	}

	return &userv1.CreateAddressResponse{Address: toProtoAddress(created)}, nil
}

func (s *Server) ListAddresses(ctx context.Context, req *userv1.ListAddressesRequest) (*userv1.ListAddressesResponse, error) {
	ownerID, err := s.resolveOwnerUserID(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	if !isValidUUID(ownerID) {
		return &userv1.ListAddressesResponse{Addresses: []*userv1.Address{}}, nil
	}

	addrs, err := s.store.ListAddressesByUserID(ctx, ownerID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list addresses")
	}
	out := make([]*userv1.Address, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, toProtoAddress(a))
	}
	return &userv1.ListAddressesResponse{Addresses: out}, nil
}

func (s *Server) GetAddress(ctx context.Context, req *userv1.GetAddressRequest) (*userv1.GetAddressResponse, error) {
	addressID := strings.TrimSpace(req.GetAddressId())
	if addressID == "" {
		return nil, status.Error(codes.InvalidArgument, "address_id is required")
	}
	if !isValidUUID(addressID) {
		return nil, status.Error(codes.NotFound, "address not found")
	}

	addr, err := s.store.GetAddressByID(ctx, addressID)
	if err != nil {
		if isNotFoundErr(err) {
			return nil, status.Error(codes.NotFound, "address not found")
		}
		return nil, status.Error(codes.Internal, "failed to load address")
	}

	if claims, ok := auth.ClaimsFromContext(ctx); ok && claims.UserID() != "" {
		if !isAdminRole(claims.Role) && addr.UserID != claims.UserID() {
			return nil, status.Error(codes.PermissionDenied, "cannot access another user's address")
		}
	} else if reqUserID := strings.TrimSpace(req.GetUserId()); reqUserID != "" && addr.UserID != reqUserID {
		return nil, status.Error(codes.PermissionDenied, "address does not belong to user")
	}

	return &userv1.GetAddressResponse{Address: toProtoAddress(addr)}, nil
}

func (s *Server) UpdateAddress(ctx context.Context, req *userv1.UpdateAddressRequest) (*userv1.UpdateAddressResponse, error) {
	ownerID, err := s.resolveOwnerUserID(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}

	addressID := strings.TrimSpace(req.GetAddressId())
	if addressID == "" {
		return nil, status.Error(codes.InvalidArgument, "address_id is required")
	}
	if !isValidUUID(addressID) {
		return nil, status.Error(codes.NotFound, "address not found")
	}

	existing, err := s.store.GetAddressByID(ctx, addressID)
	if err != nil {
		if isNotFoundErr(err) {
			return nil, status.Error(codes.NotFound, "address not found")
		}
		return nil, status.Error(codes.Internal, "failed to load address")
	}

	claims, _ := auth.ClaimsFromContext(ctx)
	if existing.UserID != ownerID && !isAdminRole(claims.Role) {
		return nil, status.Error(codes.PermissionDenied, "cannot modify another user's address")
	}

	recipientName := existing.RecipientName
	if v := strings.TrimSpace(req.GetRecipientName()); v != "" {
		recipientName = v
	}
	recipientPhone := existing.RecipientPhone
	if v := strings.TrimSpace(req.GetRecipientPhone()); v != "" {
		norm := normalizeVNPhone(v)
		if !isValidVNPhone(norm) {
			return nil, status.Error(codes.InvalidArgument, "valid Vietnamese recipient_phone is required")
		}
		recipientPhone = norm
	}
	provinceCode := existing.ProvinceCode
	reqProvinceCode := strings.TrimSpace(req.GetProvinceCode())
	if reqProvinceCode != "" {
		provinceCode = reqProvinceCode
	}
	provinceName := existing.ProvinceName
	if v := strings.TrimSpace(req.GetProvinceName()); v != "" {
		provinceName = v
	} else if reqProvinceCode != "" && reqProvinceCode != existing.ProvinceCode {
		provinceName = ""
	}
	wardCode := existing.WardCode
	reqWardCode := strings.TrimSpace(req.GetWardCode())
	if reqWardCode != "" {
		wardCode = reqWardCode
	}
	wardName := existing.WardName
	if v := strings.TrimSpace(req.GetWardName()); v != "" {
		wardName = v
	} else if reqWardCode != "" && reqWardCode != existing.WardCode {
		wardName = ""
	}
	if reqProvinceCode != "" || reqWardCode != "" {
		provinceName, wardName, err = s.validateProvinceWard(ctx, provinceCode, wardCode, provinceName, wardName)
		if err != nil {
			return nil, err
		}
	}
	streetAddress := existing.StreetAddress
	if v := strings.TrimSpace(req.GetStreetAddress()); v != "" {
		streetAddress = v
	}

	// ponytail: UpdateAddressRequest.is_default is a non-optional proto3 bool, so omitted vs explicit false
	// cannot be distinguished. Keep existing.IsDefault when false; promote when true (upgrade path: optional bool in proto).
	isDefault := existing.IsDefault
	if req.GetIsDefault() {
		isDefault = true
	}

	var updated db.Address
	err = s.store.WithTx(ctx, func(q db.Querier) error {
		if req.GetIsDefault() {
			if txErr := q.ClearUserDefaultAddresses(ctx, existing.UserID); txErr != nil {
				return txErr
			}
		}
		var txErr error
		updated, txErr = q.UpdateAddress(ctx, db.UpdateAddressParams{
			ID:             existing.ID,
			RecipientName:  recipientName,
			RecipientPhone: recipientPhone,
			ProvinceCode:   provinceCode,
			ProvinceName:   provinceName,
			WardCode:       wardCode,
			WardName:       wardName,
			StreetAddress:  streetAddress,
			IsDefault:      isDefault,
		})
		return txErr
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to update address")
	}

	return &userv1.UpdateAddressResponse{Address: toProtoAddress(updated)}, nil
}

func (s *Server) DeleteAddress(ctx context.Context, req *userv1.DeleteAddressRequest) (*userv1.DeleteAddressResponse, error) {
	ownerID, err := s.resolveOwnerUserID(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}

	addressID := strings.TrimSpace(req.GetAddressId())
	if addressID == "" {
		return nil, status.Error(codes.InvalidArgument, "address_id is required")
	}
	if !isValidUUID(addressID) {
		return nil, status.Error(codes.NotFound, "address not found")
	}

	existing, err := s.store.GetAddressByID(ctx, addressID)
	if err != nil {
		if isNotFoundErr(err) {
			return nil, status.Error(codes.NotFound, "address not found")
		}
		return nil, status.Error(codes.Internal, "failed to load address")
	}

	claims, _ := auth.ClaimsFromContext(ctx)
	if existing.UserID != ownerID && !isAdminRole(claims.Role) {
		return nil, status.Error(codes.PermissionDenied, "cannot delete another user's address")
	}

	err = s.store.WithTx(ctx, func(q db.Querier) error {
		inUse, txErr := q.ExistsActiveShopByPickupAddressID(ctx, existing.ID)
		if txErr != nil {
			return txErr
		}
		if inUse {
			return status.Error(codes.FailedPrecondition, "cannot delete pickup address of an active shop")
		}
		return q.DeleteAddress(ctx, existing.ID)
	})
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		return nil, status.Error(codes.Internal, "failed to delete address")
	}
	return &userv1.DeleteAddressResponse{Ok: true}, nil
}

func (s *Server) WriteAuditLog(ctx context.Context, req *userv1.WriteAuditLogRequest) (*userv1.WriteAuditLogResponse, error) {
	claims, err := s.requireAdminClaims(ctx)
	if err != nil {
		return nil, err
	}

	actorID := claims.UserID()
	actorRole := claims.Role
	if claims.Service != "" {
		if v := strings.TrimSpace(req.GetActorId()); v != "" {
			actorID = v
		}
		if v := strings.TrimSpace(req.GetActorRole()); v != "" {
			actorRole = v
		}
	}

	action := strings.TrimSpace(req.GetAction())
	targetType := strings.TrimSpace(req.GetTargetType())
	targetID := strings.TrimSpace(req.GetTargetId())
	if actorID == "" || action == "" || targetType == "" || targetID == "" {
		return nil, status.Error(codes.InvalidArgument, "actor_id, action, target_type, and target_id are required")
	}

	beforeJSON := strings.TrimSpace(req.GetBeforeJson())
	if beforeJSON == "" {
		beforeJSON = "{}"
	}
	afterJSON := strings.TrimSpace(req.GetAfterJson())
	if afterJSON == "" {
		afterJSON = "{}"
	}
	ipAddr := auditIPFromContext(ctx, claims, req.GetIpAddress())

	created, err := s.store.InsertAuditLog(ctx, db.InsertAuditLogParams{
		ID:         uuid.NewString(),
		ActorID:    actorID,
		ActorRole:  actorRole,
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		BeforeJSON: beforeJSON,
		AfterJSON:  afterJSON,
		Reason:     strings.TrimSpace(req.GetReason()),
		IPAddress:  ipAddr,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to insert audit log")
	}

	return &userv1.WriteAuditLogResponse{AuditLog: toProtoAuditLog(created)}, nil
}

func (s *Server) RecordCODRejection(ctx context.Context, req *userv1.RecordCODRejectionRequest) (*userv1.RecordCODRejectionResponse, error) {
	claims, err := requireAuthClaims(ctx)
	if err != nil {
		return nil, err
	}
	if claims.Service == "" {
		return nil, status.Error(codes.PermissionDenied, "service token required")
	}

	userID := strings.TrimSpace(req.GetUserId())
	orderID := strings.TrimSpace(req.GetOrderId())
	reason := strings.TrimSpace(req.GetReason())
	if userID == "" || orderID == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id and order_id are required")
	}
	if !isValidUUID(userID) {
		return nil, status.Error(codes.NotFound, "user not found")
	}

	var (
		rejectedCount   int32
		alreadyRecorded bool
	)
	err = s.store.WithTx(ctx, func(q db.Querier) error {
		user, txErr := q.GetUserByID(ctx, userID)
		if txErr != nil {
			if isNotFoundErr(txErr) {
				return status.Error(codes.NotFound, "user not found")
			}
			return txErr
		}
		rows, txErr := q.InsertCODRejection(ctx, db.InsertCODRejectionParams{
			OrderID: orderID,
			UserID:  user.ID,
			Reason:  reason,
		})
		if txErr != nil {
			return txErr
		}
		if rows == 0 {
			alreadyRecorded = true
			fresh, fErr := q.GetUserByID(ctx, user.ID)
			if fErr != nil {
				return fErr
			}
			rejectedCount = fresh.CODRejectedCount
			return nil
		}
		updated, txErr := q.IncrementUserCODRejectedCount(ctx, user.ID)
		if txErr != nil {
			return txErr
		}
		rejectedCount = updated.CODRejectedCount
		return nil
	})
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		return nil, status.Error(codes.Internal, "failed to record COD rejection")
	}

	s.log.InfoContext(ctx, "cod rejection processed",
		slog.String("user_id", userID),
		slog.String("order_id", orderID),
		slog.Bool("already_recorded", alreadyRecorded),
		slog.Int("cod_rejected_count", int(rejectedCount)),
	)
	return &userv1.RecordCODRejectionResponse{
		CodRejectedCount: rejectedCount,
		AlreadyRecorded:  alreadyRecorded,
	}, nil
}

func (s *Server) ListAuditLogs(ctx context.Context, req *userv1.ListAuditLogsRequest) (*userv1.ListAuditLogsResponse, error) {
	if _, err := s.requireAdminClaims(ctx); err != nil {
		return nil, err
	}

	page, pageSize := normalizePagination(req.GetPage(), req.GetPageSize())
	var fromTime, toTime *time.Time
	if req.GetFromTime() != nil {
		t := req.GetFromTime().AsTime()
		fromTime = &t
	}
	if req.GetToTime() != nil {
		t := req.GetToTime().AsTime()
		toTime = &t
	}

	actorID := strings.TrimSpace(req.GetActorId())
	targetType := strings.TrimSpace(req.GetTargetType())
	targetID := strings.TrimSpace(req.GetTargetId())

	logs, err := s.store.ListAuditLogs(ctx, db.ListAuditLogsParams{
		ActorID:    actorID,
		TargetType: targetType,
		TargetID:   targetID,
		FromTime:   fromTime,
		ToTime:     toTime,
		PageLimit:  pageSize,
		PageOffset: (page - 1) * pageSize,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list audit logs")
	}
	total, err := s.store.CountAuditLogs(ctx, db.CountAuditLogsParams{
		ActorID:    actorID,
		TargetType: targetType,
		TargetID:   targetID,
		FromTime:   fromTime,
		ToTime:     toTime,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to count audit logs")
	}

	out := make([]*userv1.AuditLog, 0, len(logs))
	for _, entry := range logs {
		out = append(out, toProtoAuditLog(entry))
	}
	return &userv1.ListAuditLogsResponse{
		Logs:  out,
		Total: total,
	}, nil
}
