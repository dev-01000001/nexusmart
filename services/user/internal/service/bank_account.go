package service

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"log/slog"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/user/internal/db"
	"github.com/nexusmart/core/services/user/internal/events"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	BankAccountStatusPendingOTP         = "PENDING_OTP"
	BankAccountStatusPendingAdminReview = "PENDING_ADMIN_REVIEW"
	BankAccountStatusVerified           = "VERIFIED"
	BankAccountStatusRejected           = "REJECTED"
	BankAccountStatusSuperseded         = "SUPERSEDED"
)

func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}

func isValidBankCode(code string) bool {
	if len(code) < 2 || len(code) > 32 {
		return false
	}
	for i := 0; i < len(code); i++ {
		c := code[i]
		if !((c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func cleanBankAccountNumber(raw string) (string, bool) {
	cleaned := strings.NewReplacer(" ", "", "-", "").Replace(strings.TrimSpace(raw))
	if len(cleaned) < 6 || len(cleaned) > 24 {
		return "", false
	}
	for i := 0; i < len(cleaned); i++ {
		if cleaned[i] < '0' || cleaned[i] > '9' {
			return "", false
		}
	}
	return cleaned, true
}

// maskBankAccountNumber preserves only the last 4 characters prefixed with "****",
// matching the masking format used by the payment service.
func maskBankAccountNumber(account string) string {
	runes := []rune(strings.TrimSpace(account))
	if len(runes) <= 4 {
		return "****"
	}
	return "****" + string(runes[len(runes)-4:])
}

// normalizeVietnameseName folds Vietnamese diacritics (both NFC precomposed and NFD combining
// marks, including Đ/đ), lowercases, strips non-alphanumeric separators, and collapses whitespace.
func normalizeVietnameseName(input string) string {
	var b strings.Builder
	prevSpace := true
	for _, r := range strings.ToLower(strings.TrimSpace(input)) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		r = foldVietnameseRune(r)
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevSpace = false
		} else if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			prevSpace = false
		} else if !prevSpace {
			b.WriteByte(' ')
			prevSpace = true
		}
	}
	return strings.TrimSpace(b.String())
}

func namesMatchVietnamese(a, b string) bool {
	na := normalizeVietnameseName(a)
	nb := normalizeVietnameseName(b)
	return na != "" && na == nb
}

func hashShopBankOTP(shopID, bankAccountID, otp string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(shopID) + ":" + strings.TrimSpace(bankAccountID) + ":" + strings.TrimSpace(otp)))
	return hex.EncodeToString(sum[:])
}

func shopBankAccountStatusToProto(st string) userv1.ShopBankAccountStatus {
	switch strings.ToUpper(strings.TrimSpace(st)) {
	case BankAccountStatusPendingOTP:
		return userv1.ShopBankAccountStatus_SHOP_BANK_ACCOUNT_STATUS_PENDING_OTP
	case BankAccountStatusPendingAdminReview:
		return userv1.ShopBankAccountStatus_SHOP_BANK_ACCOUNT_STATUS_PENDING_ADMIN_REVIEW
	case BankAccountStatusVerified:
		return userv1.ShopBankAccountStatus_SHOP_BANK_ACCOUNT_STATUS_VERIFIED
	case BankAccountStatusRejected:
		return userv1.ShopBankAccountStatus_SHOP_BANK_ACCOUNT_STATUS_REJECTED
	default:
		return userv1.ShopBankAccountStatus_SHOP_BANK_ACCOUNT_STATUS_UNSPECIFIED
	}
}

func toProtoShopBankAccount(acc db.ShopBankAccount, includeRawNumber bool) *userv1.ShopBankAccount {
	out := &userv1.ShopBankAccount{
		Id:                  acc.ID,
		ShopId:              acc.ShopID,
		BankCode:            acc.BankCode,
		BankName:            acc.BankName,
		AccountNumberMasked: acc.AccountNumberMasked,
		AccountHolderName:   acc.AccountHolderName,
		HolderNameMatched:   acc.HolderNameMatched,
		Status:              shopBankAccountStatusToProto(acc.Status),
		ReviewNote:          acc.ReviewNote,
		CreatedAt:           timestamppb.New(acc.CreatedAt),
		UpdatedAt:           timestamppb.New(acc.UpdatedAt),
	}
	if includeRawNumber {
		out.AccountNumber = acc.AccountNumber
	}
	if acc.PayoutLockedUntil != nil {
		out.PayoutLockedUntil = timestamppb.New(*acc.PayoutLockedUntil)
	}
	if acc.VerifiedAt != nil {
		out.VerifiedAt = timestamppb.New(*acc.VerifiedAt)
	}
	return out
}

// auditShopBankAccountSnapshot omits raw account_number, otp, and otp_hash so sensitive fields
// never enter audit_logs.
func auditShopBankAccountSnapshot(acc db.ShopBankAccount) map[string]any {
	snap := map[string]any{
		"id":                    acc.ID,
		"shop_id":               acc.ShopID,
		"bank_code":             acc.BankCode,
		"bank_name":             acc.BankName,
		"account_number_masked": acc.AccountNumberMasked,
		"account_holder_name":   acc.AccountHolderName,
		"holder_name_matched":   acc.HolderNameMatched,
		"status":                acc.Status,
		"review_note":           acc.ReviewNote,
	}
	if acc.PayoutLockedUntil != nil {
		snap["payout_locked_until"] = acc.PayoutLockedUntil.UTC().Format(time.RFC3339)
	}
	if acc.VerifiedAt != nil {
		snap["verified_at"] = acc.VerifiedAt.UTC().Format(time.RFC3339)
	}
	return snap
}

func (s *Server) requireSellerShopID(ctx context.Context, requestedShopID string) (*auth.Claims, string, error) {
	claims, err := s.requireAuthClaims(ctx)
	if err != nil {
		return nil, "", err
	}
	if claims.Service != "" {
		return nil, "", status.Error(codes.PermissionDenied, "service identity not allowed for seller bank account endpoint")
	}
	if !auth.HasRole(claims, RoleSeller) || strings.TrimSpace(claims.ShopID) == "" {
		return nil, "", status.Error(codes.PermissionDenied, "seller role with active shop required")
	}
	reqShopID := strings.TrimSpace(requestedShopID)
	if reqShopID != "" && reqShopID != claims.ShopID {
		return nil, "", status.Error(codes.PermissionDenied, "cannot access another shop's bank account")
	}
	if !isValidUUID(claims.ShopID) {
		return nil, "", status.Error(codes.NotFound, "shop not found")
	}
	return claims, claims.ShopID, nil
}

func (s *Server) AddShopBankAccount(ctx context.Context, req *userv1.AddShopBankAccountRequest) (*userv1.AddShopBankAccountResponse, error) {
	claims, shopID, err := s.requireSellerShopID(ctx, req.GetShopId())
	if err != nil {
		return nil, err
	}

	bankCode := strings.ToUpper(strings.TrimSpace(req.GetBankCode()))
	bankName := collapseSpaces(req.GetBankName())
	accountNumber, ok := cleanBankAccountNumber(req.GetAccountNumber())
	accountHolderName := collapseSpaces(req.GetAccountHolderName())

	if !isValidBankCode(bankCode) {
		return nil, status.Error(codes.InvalidArgument, "valid bank_code is required")
	}
	if len(bankName) < 2 || len(bankName) > 120 {
		return nil, status.Error(codes.InvalidArgument, "valid bank_name is required")
	}
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "account_number must contain 6 to 24 digits")
	}
	if len(accountHolderName) < 2 || len(accountHolderName) > 120 {
		return nil, status.Error(codes.InvalidArgument, "valid account_holder_name is required")
	}

	masked := maskBankAccountNumber(accountNumber)
	otpCode, err := generateOTP()
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to generate otp")
	}

	now := s.now()
	expiresAt := now.Add(s.opts.OTPTTL)
	accountID := uuid.NewString()
	otpHash := hashShopBankOTP(shopID, accountID, otpCode)

	var (
		created db.ShopBankAccount
		owner   db.User
	)
	err = s.store.WithTx(ctx, func(q db.Querier) error {
		shop, txErr := q.GetShopByIDForUpdate(ctx, shopID)
		if txErr != nil {
			if isNotFoundErr(txErr) {
				return status.Error(codes.NotFound, "shop not found")
			}
			return txErr
		}
		if shop.OwnerUserID != claims.UserID() {
			return status.Error(codes.PermissionDenied, "caller is not the owner of this shop")
		}
		if shop.Status != ShopStatusActive {
			return status.Error(codes.FailedPrecondition, "only ACTIVE shops can register a bank account")
		}

		owner, txErr = q.GetUserByID(ctx, shop.OwnerUserID)
		if txErr != nil {
			if isNotFoundErr(txErr) {
				return status.Error(codes.NotFound, "shop owner not found")
			}
			return txErr
		}
		if owner.Status != StatusActive {
			return status.Error(codes.FailedPrecondition, "shop owner account is not active")
		}

		recentCount, txErr := q.CountShopBankAccountsCreatedSince(ctx, db.CountShopBankAccountsCreatedSinceParams{
			ShopID:    shop.ID,
			CreatedAt: now.Add(-s.opts.BankAddRateWindow),
		})
		if txErr != nil {
			return txErr
		}
		if recentCount >= int64(s.opts.BankAddMaxPerHour) {
			return status.Error(codes.ResourceExhausted, "too many bank account submissions; please try again later")
		}

		if txErr = q.SupersedePendingOTPShopBankAccounts(ctx, db.SupersedePendingOTPShopBankAccountsParams{
			ShopID:    shop.ID,
			UpdatedAt: now,
		}); txErr != nil {
			return txErr
		}

		holderMatched := namesMatchVietnamese(accountHolderName, shop.VerifiedOwnerName)
		// ponytail: account_number is stored plaintext in user_db because no KMS/envelope key
		// is configured for user yet; upgrade path: envelope encryption with KMS-managed DEK.
		created, txErr = q.CreateShopBankAccount(ctx, db.CreateShopBankAccountParams{
			ID:                  accountID,
			ShopID:              shop.ID,
			BankCode:            bankCode,
			BankName:            bankName,
			AccountNumber:       accountNumber,
			AccountNumberMasked: masked,
			AccountHolderName:   accountHolderName,
			HolderNameMatched:   holderMatched,
			Status:              BankAccountStatusPendingOTP,
			ReviewNote:          "",
			OTPHash:             otpHash,
			OTPExpiresAt:        &expiresAt,
			OTPAttempts:         0,
			CreatedAt:           now,
			UpdatedAt:           now,
		})
		if txErr != nil {
			return txErr
		}

		_, txErr = q.InsertAuditLog(ctx, db.InsertAuditLogParams{
			ID:         uuid.NewString(),
			ActorID:    claims.UserID(),
			ActorRole:  claims.Role,
			Action:     "seller.shop_bank_account.add",
			TargetType: "shop_bank_account",
			TargetID:   created.ID,
			BeforeJSON: "{}",
			AfterJSON:  marshalAuditJSON(auditShopBankAccountSnapshot(created)),
			Reason:     "",
			IPAddress:  auditIPFromContext(ctx, claims, ""),
		})
		return txErr
	})
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		return nil, status.Error(codes.Internal, "failed to create shop bank account")
	}

	if s.pub != nil {
		if pubErr := s.pub.PublishShopBankAccountOTP(ctx, events.ShopBankAccountOTPPayload{
			UserID:              owner.ID,
			ShopID:              created.ShopID,
			Email:               owner.Email,
			FullName:            owner.FullName,
			OTP:                 otpCode,
			ExpiresAt:           expiresAt.Format(time.RFC3339),
			BankCode:            created.BankCode,
			AccountNumberMasked: created.AccountNumberMasked,
		}); pubErr != nil {
			s.log.ErrorContext(ctx, "failed to publish user.shop_bank_account_otp",
				slog.String("shop_id", created.ShopID),
				slog.String("bank_account_id", created.ID),
				slog.String("error", pubErr.Error()),
			)
			cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cleanupCancel()
			if supErr := s.store.MarkShopBankAccountPublishFailed(cleanupCtx, db.MarkShopBankAccountPublishFailedParams{
				ID:        created.ID,
				UpdatedAt: s.now(),
			}); supErr != nil {
				s.log.WarnContext(cleanupCtx, "failed to mark undelivered bank account otp publish failed",
					slog.String("shop_id", created.ShopID),
					slog.String("bank_account_id", created.ID),
					slog.String("error", supErr.Error()),
				)
			}
			return nil, status.Error(codes.Internal, "failed to emit bank account otp event")
		}
	}

	s.log.InfoContext(ctx, "shop bank account otp initiated",
		slog.String("shop_id", created.ShopID),
		slog.String("bank_account_id", created.ID),
		slog.String("bank_code", created.BankCode),
		slog.String("account_number_masked", created.AccountNumberMasked),
		slog.Bool("holder_name_matched", created.HolderNameMatched),
	)

	return &userv1.AddShopBankAccountResponse{
		BankAccount: toProtoShopBankAccount(created, false),
		OtpSent:     true,
	}, nil
}

func (s *Server) VerifyShopBankAccountOTP(ctx context.Context, req *userv1.VerifyShopBankAccountOTPRequest) (*userv1.VerifyShopBankAccountOTPResponse, error) {
	claims, shopID, err := s.requireSellerShopID(ctx, req.GetShopId())
	if err != nil {
		return nil, err
	}

	otp := strings.TrimSpace(req.GetOtp())
	if otp == "" {
		return nil, status.Error(codes.InvalidArgument, "otp is required")
	}

	now := s.now()
	var (
		updated   db.ShopBankAccount
		verifyErr error
	)
	err = s.store.WithTx(ctx, func(q db.Querier) error {
		shop, txErr := q.GetShopByIDForUpdate(ctx, shopID)
		if txErr != nil {
			if isNotFoundErr(txErr) {
				return status.Error(codes.NotFound, "shop not found")
			}
			return txErr
		}
		if shop.OwnerUserID != claims.UserID() {
			return status.Error(codes.PermissionDenied, "caller is not the owner of this shop")
		}
		if shop.Status != ShopStatusActive {
			return status.Error(codes.FailedPrecondition, "only ACTIVE shops can verify a bank account")
		}

		owner, txErr := q.GetUserByID(ctx, shop.OwnerUserID)
		if txErr != nil {
			if isNotFoundErr(txErr) {
				return status.Error(codes.NotFound, "shop owner not found")
			}
			return txErr
		}
		if owner.Status != StatusActive {
			return status.Error(codes.FailedPrecondition, "shop owner account is not active")
		}

		acc, txErr := q.GetPendingOTPShopBankAccountForUpdate(ctx, shop.ID)
		if txErr != nil {
			if isNotFoundErr(txErr) {
				return status.Error(codes.FailedPrecondition, "no bank account pending otp verification; please submit bank account again")
			}
			return txErr
		}

		maxAttempts := int32(s.opts.BankOTPMaxAttempts)
		if acc.OTPAttempts >= maxAttempts {
			if _, txErr = q.RecordShopBankAccountOTPFailure(ctx, db.RecordShopBankAccountOTPFailureParams{
				ID:           acc.ID,
				Status:       BankAccountStatusSuperseded,
				OTPAttempts:  acc.OTPAttempts,
				OTPHash:      "",
				OTPExpiresAt: nil,
				UpdatedAt:    now,
			}); txErr != nil {
				return txErr
			}
			verifyErr = status.Error(codes.FailedPrecondition, "too many invalid otp attempts; please submit bank account again")
			return nil
		}

		if acc.OTPHash == "" || acc.OTPExpiresAt == nil || !now.Before(*acc.OTPExpiresAt) {
			if _, txErr = q.RecordShopBankAccountOTPFailure(ctx, db.RecordShopBankAccountOTPFailureParams{
				ID:           acc.ID,
				Status:       BankAccountStatusSuperseded,
				OTPAttempts:  acc.OTPAttempts,
				OTPHash:      "",
				OTPExpiresAt: nil,
				UpdatedAt:    now,
			}); txErr != nil {
				return txErr
			}
			verifyErr = status.Error(codes.FailedPrecondition, "otp expired; please submit bank account again")
			return nil
		}

		expectedHash := hashShopBankOTP(shop.ID, acc.ID, otp)
		if subtle.ConstantTimeCompare([]byte(acc.OTPHash), []byte(expectedHash)) != 1 {
			newAttempts := acc.OTPAttempts + 1
			nextStatus := BankAccountStatusPendingOTP
			nextHash := acc.OTPHash
			nextExp := acc.OTPExpiresAt
			if newAttempts >= maxAttempts {
				nextStatus = BankAccountStatusSuperseded
				nextHash = ""
				nextExp = nil
				verifyErr = status.Error(codes.FailedPrecondition, "too many invalid otp attempts; please submit bank account again")
			} else {
				verifyErr = status.Error(codes.InvalidArgument, "invalid otp")
			}
			if _, txErr = q.RecordShopBankAccountOTPFailure(ctx, db.RecordShopBankAccountOTPFailureParams{
				ID:           acc.ID,
				Status:       nextStatus,
				OTPAttempts:  newAttempts,
				OTPHash:      nextHash,
				OTPExpiresAt: nextExp,
				UpdatedAt:    now,
			}); txErr != nil {
				return txErr
			}
			return nil
		}

		if txErr = q.SupersedePendingAdminReviewShopBankAccounts(ctx, db.SupersedePendingAdminReviewShopBankAccountsParams{
			ShopID:    shop.ID,
			UpdatedAt: now,
		}); txErr != nil {
			return txErr
		}

		holderMatched := namesMatchVietnamese(acc.AccountHolderName, shop.VerifiedOwnerName)
		newStatus := BankAccountStatusPendingAdminReview
		var (
			verifiedAt        *time.Time
			payoutLockedUntil *time.Time
		)
		if holderMatched {
			newStatus = BankAccountStatusVerified
			vAt := now
			verifiedAt = &vAt

			prev, prevErr := q.GetVerifiedShopBankAccountByShopID(ctx, shop.ID)
			if prevErr == nil {
				if txErr = q.SupersedeShopBankAccount(ctx, db.SupersedeShopBankAccountParams{
					ID:        prev.ID,
					UpdatedAt: now,
				}); txErr != nil {
					return txErr
				}
				lockUntil := now.Add(s.opts.BankPayoutLockDuration)
				payoutLockedUntil = &lockUntil
			} else if !isNotFoundErr(prevErr) {
				return prevErr
			}
		}

		updated, txErr = q.CompleteShopBankAccountOTPVerification(ctx, db.CompleteShopBankAccountOTPVerificationParams{
			ID:                acc.ID,
			Status:            newStatus,
			HolderNameMatched: holderMatched,
			PayoutLockedUntil: payoutLockedUntil,
			VerifiedAt:        verifiedAt,
			UpdatedAt:         now,
		})
		if txErr != nil {
			return txErr
		}

		_, txErr = q.InsertAuditLog(ctx, db.InsertAuditLogParams{
			ID:         uuid.NewString(),
			ActorID:    claims.UserID(),
			ActorRole:  claims.Role,
			Action:     "seller.shop_bank_account.verify_otp",
			TargetType: "shop_bank_account",
			TargetID:   updated.ID,
			BeforeJSON: marshalAuditJSON(auditShopBankAccountSnapshot(acc)),
			AfterJSON:  marshalAuditJSON(auditShopBankAccountSnapshot(updated)),
			Reason:     "",
			IPAddress:  auditIPFromContext(ctx, claims, ""),
		})
		return txErr
	})
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		return nil, status.Error(codes.Internal, "failed to verify bank account otp")
	}
	if verifyErr != nil {
		return nil, verifyErr
	}

	s.log.InfoContext(ctx, "shop bank account otp verified",
		slog.String("shop_id", updated.ShopID),
		slog.String("bank_account_id", updated.ID),
		slog.String("status", updated.Status),
		slog.Bool("holder_name_matched", updated.HolderNameMatched),
		slog.Bool("payout_locked", updated.PayoutLockedUntil != nil),
	)

	return &userv1.VerifyShopBankAccountOTPResponse{
		BankAccount: toProtoShopBankAccount(updated, false),
	}, nil
}

func (s *Server) GetShopBankAccount(ctx context.Context, req *userv1.GetShopBankAccountRequest) (*userv1.GetShopBankAccountResponse, error) {
	claims, err := s.requireAuthClaims(ctx)
	if err != nil {
		return nil, err
	}
	if claims.Service != "" {
		return nil, status.Error(codes.PermissionDenied, "service identity not allowed for GetShopBankAccount")
	}

	var shopID string
	if isAdminRole(claims.Role) {
		shopID = strings.TrimSpace(req.GetShopId())
		if shopID == "" {
			return nil, status.Error(codes.InvalidArgument, "shop_id is required")
		}
		if !isValidUUID(shopID) {
			return nil, status.Error(codes.NotFound, "shop bank account not found")
		}
	} else if auth.HasRole(claims, RoleSeller) && strings.TrimSpace(claims.ShopID) != "" {
		reqShopID := strings.TrimSpace(req.GetShopId())
		if reqShopID != "" && reqShopID != claims.ShopID {
			return nil, status.Error(codes.PermissionDenied, "cannot access another shop's bank account")
		}
		shopID = claims.ShopID
		if !isValidUUID(shopID) {
			return nil, status.Error(codes.NotFound, "shop bank account not found")
		}
		shop, sErr := s.store.GetShopByID(ctx, shopID)
		if sErr != nil {
			if isNotFoundErr(sErr) {
				return nil, status.Error(codes.NotFound, "shop not found")
			}
			return nil, status.Error(codes.Internal, "failed to load shop")
		}
		if shop.OwnerUserID != claims.UserID() {
			return nil, status.Error(codes.PermissionDenied, "caller is not the owner of this shop")
		}
	} else {
		return nil, status.Error(codes.PermissionDenied, "seller or admin role required")
	}

	acc, err := s.store.GetLatestShopBankAccountByShopID(ctx, db.GetLatestShopBankAccountByShopIDParams{
		ShopID:            shopID,
		NowTime:           s.now(),
		PreferAdminReview: isAdminRole(claims.Role),
	})
	if err != nil {
		if isNotFoundErr(err) {
			return nil, status.Error(codes.NotFound, "shop bank account not found")
		}
		return nil, status.Error(codes.Internal, "failed to load shop bank account")
	}

	return &userv1.GetShopBankAccountResponse{
		BankAccount: toProtoShopBankAccount(acc, false),
	}, nil
}

func (s *Server) AdminReviewShopBankAccount(ctx context.Context, req *userv1.AdminReviewShopBankAccountRequest) (*userv1.AdminReviewShopBankAccountResponse, error) {
	claims, err := s.requireAdminClaims(ctx)
	if err != nil {
		return nil, err
	}
	if claims.Service != "" {
		return nil, status.Error(codes.PermissionDenied, "service identity not allowed for AdminReviewShopBankAccount")
	}

	shopID := strings.TrimSpace(req.GetShopId())
	if shopID == "" {
		return nil, status.Error(codes.InvalidArgument, "shop_id is required")
	}
	if !isValidUUID(shopID) {
		return nil, status.Error(codes.NotFound, "shop not found")
	}

	bankAccountID := strings.TrimSpace(req.GetBankAccountId())
	if bankAccountID != "" && !isValidUUID(bankAccountID) {
		return nil, status.Error(codes.NotFound, "shop bank account not found")
	}

	reason := strings.TrimSpace(req.GetReason())
	var approve bool
	switch req.GetDecision() {
	case userv1.ReviewDecision_REVIEW_DECISION_APPROVE:
		approve = true
	case userv1.ReviewDecision_REVIEW_DECISION_REJECT:
		if reason == "" {
			return nil, status.Error(codes.InvalidArgument, "reason is required when rejecting a bank account")
		}
		approve = false
	default:
		return nil, status.Error(codes.InvalidArgument, "decision must be APPROVE or REJECT")
	}

	now := s.now()
	var updated db.ShopBankAccount
	err = s.store.WithTx(ctx, func(q db.Querier) error {
		shop, txErr := q.GetShopByIDForUpdate(ctx, shopID)
		if txErr != nil {
			if isNotFoundErr(txErr) {
				return status.Error(codes.NotFound, "shop not found")
			}
			return txErr
		}

		var acc db.ShopBankAccount
		if bankAccountID != "" {
			acc, txErr = q.GetShopBankAccountByIDForUpdate(ctx, bankAccountID)
		} else {
			acc, txErr = q.GetPendingAdminReviewShopBankAccountByShopIDForUpdate(ctx, shop.ID)
		}
		if txErr != nil {
			if isNotFoundErr(txErr) {
				return status.Error(codes.NotFound, "shop bank account not found")
			}
			return txErr
		}
		if acc.ShopID != shop.ID {
			return status.Error(codes.NotFound, "shop bank account not found for shop")
		}
		if acc.Status != BankAccountStatusPendingAdminReview {
			return status.Error(codes.FailedPrecondition, "only PENDING_ADMIN_REVIEW bank accounts can be reviewed")
		}

		newStatus := BankAccountStatusRejected
		var (
			verifiedAt        *time.Time
			payoutLockedUntil *time.Time
		)
		if approve {
			if shop.Status != ShopStatusActive {
				return status.Error(codes.FailedPrecondition, "only ACTIVE shops can have a verified bank account")
			}
			newStatus = BankAccountStatusVerified
			vAt := now
			verifiedAt = &vAt

			prev, prevErr := q.GetVerifiedShopBankAccountByShopID(ctx, shop.ID)
			if prevErr == nil && prev.ID != acc.ID {
				if txErr = q.SupersedeShopBankAccount(ctx, db.SupersedeShopBankAccountParams{
					ID:        prev.ID,
					UpdatedAt: now,
				}); txErr != nil {
					return txErr
				}
				lockUntil := now.Add(s.opts.BankPayoutLockDuration)
				payoutLockedUntil = &lockUntil
			} else if prevErr != nil && !isNotFoundErr(prevErr) {
				return prevErr
			}
		}

		updated, txErr = q.UpdateShopBankAccountAdminReview(ctx, db.UpdateShopBankAccountAdminReviewParams{
			ID:                acc.ID,
			Status:            newStatus,
			ReviewNote:        reason,
			PayoutLockedUntil: payoutLockedUntil,
			VerifiedAt:        verifiedAt,
			UpdatedAt:         now,
		})
		if txErr != nil {
			if isNotFoundErr(txErr) {
				return status.Error(codes.FailedPrecondition, "only PENDING_ADMIN_REVIEW bank accounts can be reviewed")
			}
			return txErr
		}

		_, txErr = q.InsertAuditLog(ctx, db.InsertAuditLogParams{
			ID:         uuid.NewString(),
			ActorID:    claims.UserID(),
			ActorRole:  claims.Role,
			Action:     "admin.shop_bank_account.review",
			TargetType: "shop_bank_account",
			TargetID:   updated.ID,
			BeforeJSON: marshalAuditJSON(auditShopBankAccountSnapshot(acc)),
			AfterJSON:  marshalAuditJSON(auditShopBankAccountSnapshot(updated)),
			Reason:     reason,
			IPAddress:  auditIPFromContext(ctx, claims, ""),
		})
		return txErr
	})
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		return nil, status.Error(codes.Internal, "failed to review shop bank account")
	}

	s.log.InfoContext(ctx, "admin reviewed shop bank account",
		slog.String("shop_id", updated.ShopID),
		slog.String("bank_account_id", updated.ID),
		slog.String("status", updated.Status),
		slog.String("actor_id", claims.UserID()),
	)

	return &userv1.AdminReviewShopBankAccountResponse{
		BankAccount: toProtoShopBankAccount(updated, false),
	}, nil
}

func (s *Server) GetShopBankAccountForPayout(ctx context.Context, req *userv1.GetShopBankAccountForPayoutRequest) (*userv1.GetShopBankAccountForPayoutResponse, error) {
	claims, err := requireAuthClaims(ctx)
	if err != nil {
		return nil, err
	}
	if claims.Service == "" {
		return nil, status.Error(codes.PermissionDenied, "service token required")
	}

	shopID := strings.TrimSpace(req.GetShopId())
	if shopID == "" {
		return nil, status.Error(codes.InvalidArgument, "shop_id is required")
	}
	if !isValidUUID(shopID) {
		return nil, status.Error(codes.NotFound, "verified shop bank account not found")
	}

	acc, err := s.store.GetVerifiedShopBankAccountByShopID(ctx, shopID)
	if err != nil {
		if isNotFoundErr(err) {
			return nil, status.Error(codes.NotFound, "verified shop bank account not found")
		}
		return nil, status.Error(codes.Internal, "failed to load verified shop bank account")
	}

	s.log.InfoContext(ctx, "shop bank account fetched for payout",
		slog.String("shop_id", acc.ShopID),
		slog.String("bank_account_id", acc.ID),
		slog.String("caller_service", claims.Service),
		slog.String("account_number_masked", acc.AccountNumberMasked),
	)

	return &userv1.GetShopBankAccountForPayoutResponse{
		BankAccount: toProtoShopBankAccount(acc, true),
	}, nil
}
