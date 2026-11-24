package service

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/services/user/internal/db"
	"github.com/nexusmart/core/services/user/internal/events"
	"github.com/nexusmart/core/services/user/internal/security"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// canonicalVNPhone normalizes a Vietnamese phone number to local 0-prefixed digits (e.g. "0901234567").
func canonicalVNPhone(raw string) (string, bool) {
	norm := normalizeVNPhone(raw)
	if !isValidVNPhone(norm) {
		return "", false
	}
	switch {
	case strings.HasPrefix(norm, "+84"):
		return "0" + norm[3:], true
	case strings.HasPrefix(norm, "84"):
		return "0" + norm[2:], true
	default:
		return norm, true
	}
}

// canonicalizeAccountPhone normalizes valid Vietnamese phone numbers to local 0-prefixed digits
// (e.g. "+84 901-234-567" -> "0901234567"). Non-VN inputs are only trimmed to preserve per-RPC validation behavior.
func canonicalizeAccountPhone(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if canon, ok := canonicalVNPhone(trimmed); ok {
		return canon
	}
	return trimmed
}

// maskVNPhone masks the middle digits of a phone number while keeping the first 3 and last 3 digits
// (e.g. "0901234789" -> "090****789").
func maskVNPhone(phone string) string {
	trimmed := strings.TrimSpace(phone)
	if trimmed == "" {
		return ""
	}
	if canon, ok := canonicalVNPhone(trimmed); ok {
		trimmed = canon
	}
	runes := []rune(trimmed)
	if len(runes) <= 6 {
		return "****"
	}
	return string(runes[:3]) + "****" + string(runes[len(runes)-3:])
}

// checkVerifiedPhoneAvailableForUser rejects AlreadyExists only when another account holds
// the canonical phone with phone_verified = TRUE (E19-bis).
func checkVerifiedPhoneAvailableForUser(ctx context.Context, q db.Querier, userID, rawPhone string) error {
	canon := canonicalizeAccountPhone(rawPhone)
	if canon == "" {
		return nil
	}
	other, err := q.GetUserByPhone(ctx, canon)
	if err == nil && other.ID != userID {
		return status.Error(codes.AlreadyExists, "phone is already in use")
	}
	if err != nil && !isNotFoundErr(err) {
		return status.Error(codes.Internal, "failed to check phone uniqueness")
	}
	return nil
}

func auditPhoneVerificationSnapshot(userID, phone string, verified bool) map[string]any {
	return map[string]any{
		"id":             userID,
		"phone_masked":   maskVNPhone(phone),
		"phone_verified": verified,
	}
}

func (s *Server) RequestPhoneOTP(ctx context.Context, req *userv1.RequestPhoneOTPRequest) (*userv1.RequestPhoneOTPResponse, error) {
	claims, err := s.requireAuthClaims(ctx)
	if err != nil {
		return nil, err
	}
	if claims.Service != "" {
		return nil, status.Error(codes.PermissionDenied, "service identity not allowed for phone otp endpoint")
	}
	userID := claims.UserID()
	if !isValidUUID(userID) {
		return nil, status.Error(codes.NotFound, "user not found")
	}
	if s.sec == nil {
		return nil, status.Error(codes.Unavailable, "phone verification service unavailable")
	}

	user, err := s.store.GetUserByID(ctx, userID)
	if err != nil {
		if isNotFoundErr(err) {
			return nil, status.Error(codes.NotFound, "user not found")
		}
		return nil, status.Error(codes.Internal, "failed to load user")
	}
	if user.Status != StatusActive {
		return nil, status.Error(codes.FailedPrecondition, "user account is not active")
	}
	if strings.TrimSpace(user.Email) == "" {
		return nil, status.Error(codes.FailedPrecondition, "user email is required for phone otp delivery")
	}

	rawPhone := strings.TrimSpace(req.GetPhone())
	if rawPhone == "" {
		rawPhone = strings.TrimSpace(user.Phone)
	}
	targetPhone, ok := canonicalVNPhone(rawPhone)
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "valid Vietnamese phone is required")
	}

	if err := checkVerifiedPhoneAvailableForUser(ctx, s.store, user.ID, targetPhone); err != nil {
		return nil, err
	}

	otpCode, err := generateOTP()
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to generate otp")
	}

	profilePhone := canonicalizeAccountPhone(user.Phone)
	retryAfter, err := s.sec.SavePhoneOTP(
		ctx,
		user.ID,
		targetPhone,
		profilePhone,
		otpCode,
		s.opts.PhoneOTPTTL,
		s.opts.PhoneOTPCooldown,
		s.opts.PhoneOTPMaxPerHour,
		s.opts.PhoneOTPRateWindow,
	)
	if err != nil {
		switch {
		case errors.Is(err, security.ErrPhoneOTPCooldown):
			waitSec := int(math.Ceil(retryAfter.Seconds()))
			if waitSec < 1 {
				waitSec = 1
			}
			return nil, status.Errorf(codes.ResourceExhausted, "please wait %d seconds before requesting another phone otp", waitSec)
		case errors.Is(err, security.ErrPhoneOTPRateLimitReached):
			return nil, status.Error(codes.ResourceExhausted, "too many phone otp requests; please try again later")
		default:
			s.log.ErrorContext(ctx, "failed to save phone otp in redis",
				slog.String("user_id", user.ID),
				slog.String("error", err.Error()),
			)
			return nil, status.Error(codes.Unavailable, "phone verification service unavailable")
		}
	}

	now := s.now()
	expiresAt := now.Add(s.opts.PhoneOTPTTL)
	masked := maskVNPhone(targetPhone)

	if s.pub != nil {
		if pubErr := s.pub.PublishPhoneOTPRequested(ctx, events.PhoneOTPRequestedPayload{
			UserID:      user.ID,
			Email:       user.Email,
			FullName:    user.FullName,
			PhoneMasked: masked,
			OTP:         otpCode,
			ExpiresAt:   expiresAt.Format(time.RFC3339),
		}); pubErr != nil {
			s.log.ErrorContext(ctx, "failed to publish user.phone_otp_requested",
				slog.String("user_id", user.ID),
				slog.String("phone_masked", masked),
				slog.String("error", pubErr.Error()),
			)
			cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cleanupCancel()
			if rbErr := s.sec.RollbackPhoneOTP(cleanupCtx, user.ID, targetPhone, profilePhone, otpCode); rbErr != nil {
				s.log.WarnContext(cleanupCtx, "failed to rollback undelivered phone otp",
					slog.String("user_id", user.ID),
					slog.String("phone_masked", masked),
					slog.String("error", rbErr.Error()),
				)
			}
			return nil, status.Error(codes.Internal, "failed to emit phone otp event")
		}
	}

	s.recordActivityBestEffort(ctx, user.ID, ActivityEventPhoneOTPRequested, ActivityCategoryPhone, auditIPFromContext(ctx, claims, ""), map[string]any{
		"phone_masked": masked,
	})

	s.log.InfoContext(ctx, "phone otp requested",
		slog.String("user_id", user.ID),
		slog.String("phone_masked", masked),
	)

	return &userv1.RequestPhoneOTPResponse{
		Sent:              true,
		ExpiresInSeconds:  int32(s.opts.PhoneOTPTTL / time.Second),
		RetryAfterSeconds: int32(s.opts.PhoneOTPCooldown / time.Second),
		PhoneMasked:       masked,
	}, nil
}

func (s *Server) VerifyPhoneOTP(ctx context.Context, req *userv1.VerifyPhoneOTPRequest) (*userv1.VerifyPhoneOTPResponse, error) {
	claims, err := s.requireAuthClaims(ctx)
	if err != nil {
		return nil, err
	}
	if claims.Service != "" {
		return nil, status.Error(codes.PermissionDenied, "service identity not allowed for phone otp endpoint")
	}
	userID := claims.UserID()
	if !isValidUUID(userID) {
		return nil, status.Error(codes.NotFound, "user not found")
	}
	if s.sec == nil {
		return nil, status.Error(codes.Unavailable, "phone verification service unavailable")
	}

	otp := strings.TrimSpace(req.GetOtp())
	if otp == "" {
		return nil, status.Error(codes.InvalidArgument, "otp is required")
	}

	var explicitTargetPhone string
	if rawPhone := strings.TrimSpace(req.GetPhone()); rawPhone != "" {
		canon, ok := canonicalVNPhone(rawPhone)
		if !ok {
			return nil, status.Error(codes.InvalidArgument, "valid Vietnamese phone is required")
		}
		explicitTargetPhone = canon
	}

	rawIP := auditIPFromContext(ctx, claims, "")
	var (
		updated     db.User
		targetPhone string
		otpFailed   bool
	)
	err = s.store.WithTx(ctx, func(q db.Querier) error {
		user, txErr := q.GetUserByIDForUpdate(ctx, userID)
		if txErr != nil {
			if isNotFoundErr(txErr) {
				return status.Error(codes.NotFound, "user not found")
			}
			return txErr
		}
		if user.Status != StatusActive {
			return status.Error(codes.FailedPrecondition, "user account is not active")
		}

		profilePhone := canonicalizeAccountPhone(user.Phone)
		if explicitTargetPhone != "" {
			targetPhone = explicitTargetPhone
		} else {
			canon, ok := canonicalVNPhone(user.Phone)
			if !ok {
				return status.Error(codes.InvalidArgument, "valid Vietnamese phone is required")
			}
			targetPhone = canon
		}

		if uErr := checkVerifiedPhoneAvailableForUser(ctx, q, user.ID, targetPhone); uErr != nil {
			return uErr
		}

		if vErr := s.sec.VerifyPhoneOTP(ctx, user.ID, targetPhone, profilePhone, otp, s.opts.PhoneOTPMaxAttempts); vErr != nil {
			switch {
			case errors.Is(vErr, security.ErrOTPNotFound),
				errors.Is(vErr, security.ErrOTPInvalid),
				errors.Is(vErr, security.ErrOTPTooManyAttempts):
				otpFailed = true
				return status.Error(codes.InvalidArgument, errMsgInvalidOrExpiredOTP)
			default:
				s.log.ErrorContext(ctx, "failed to verify phone otp in redis",
					slog.String("user_id", user.ID),
					slog.String("error", vErr.Error()),
				)
				return status.Error(codes.Unavailable, "phone verification service unavailable")
			}
		}

		updated, txErr = q.MarkUserPhoneVerified(ctx, db.MarkUserPhoneVerifiedParams{
			ID:    user.ID,
			Phone: targetPhone,
		})
		if txErr != nil {
			if isUniqueViolation(txErr) {
				return status.Error(codes.AlreadyExists, "phone is already in use")
			}
			return txErr
		}

		if _, txErr = q.InsertAuditLog(ctx, db.InsertAuditLogParams{
			ID:         uuid.NewString(),
			ActorID:    claims.UserID(),
			ActorRole:  claims.Role,
			Action:     "user.phone.verify_otp",
			TargetType: "user",
			TargetID:   updated.ID,
			BeforeJSON: marshalAuditJSON(auditPhoneVerificationSnapshot(user.ID, user.Phone, user.PhoneVerified)),
			AfterJSON:  marshalAuditJSON(auditPhoneVerificationSnapshot(updated.ID, updated.Phone, updated.PhoneVerified)),
			Reason:     "",
			IPAddress:  rawIP,
		}); txErr != nil {
			return txErr
		}
		return s.recordActivityTx(ctx, q, updated.ID, ActivityEventPhoneOTPVerified, ActivityCategoryPhone, rawIP, map[string]any{
			"phone_masked": maskVNPhone(targetPhone),
		})
	})
	if err != nil {
		if otpFailed {
			s.recordActivityBestEffort(ctx, userID, ActivityEventPhoneOTPFailed, ActivityCategoryPhone, rawIP, map[string]any{
				"phone_masked": maskVNPhone(targetPhone),
			})
		}
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		return nil, status.Error(codes.Internal, "failed to verify phone otp")
	}

	s.log.InfoContext(ctx, "user phone otp verified",
		slog.String("user_id", updated.ID),
		slog.String("phone_masked", maskVNPhone(targetPhone)),
	)

	return &userv1.VerifyPhoneOTPResponse{
		Verified: true,
		User:     toProtoUser(updated),
	}, nil
}
