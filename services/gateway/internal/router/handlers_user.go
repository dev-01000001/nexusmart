package router

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/gateway/internal/httpx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type userDTO struct {
	ID               string `json:"id"`
	FullName         string `json:"full_name"`
	Email            string `json:"email"`
	Phone            string `json:"phone"`
	Role             string `json:"role"`
	Status           string `json:"status"`
	ShopID           string `json:"shop_id"`
	PhoneVerified    bool   `json:"phone_verified"`
	CODRejectedCount int32  `json:"cod_rejected_count"`
	AvatarURL        string `json:"avatar_url"`
	LockReason       string `json:"lock_reason"`
	CreatedAt        string `json:"created_at,omitempty"`
	UpdatedAt        string `json:"updated_at,omitempty"`
}

type shopDTO struct {
	ID                 string `json:"id"`
	OwnerUserID        string `json:"owner_user_id"`
	Name               string `json:"name"`
	Slug               string `json:"slug"`
	Description        string `json:"description"`
	PickupAddressID    string `json:"pickup_address_id"`
	ContactPhone       string `json:"contact_phone"`
	BusinessLicenseDoc string `json:"business_license_doc"`
	Status             string `json:"status"`
	RejectionReason    string `json:"rejection_reason"`
	SuspendedReason    string `json:"suspended_reason"`
	SuspendedAt        string `json:"suspended_at,omitempty"`
	StatusVersion      int64  `json:"status_version"`
	CreatedAt          string `json:"created_at,omitempty"`
	UpdatedAt          string `json:"updated_at,omitempty"`
}

type userActivityEntryDTO struct {
	ID           string `json:"id"`
	UserID       string `json:"user_id"`
	EventType    string `json:"event_type"`
	IPAddress    string `json:"ip_address"`
	MetadataJSON string `json:"metadata_json"`
	CreatedAt    string `json:"created_at,omitempty"`
}

type shopSuspensionApprovalDTO struct {
	ID            string `json:"id"`
	ShopID        string `json:"shop_id"`
	ShopName      string `json:"shop_name"`
	Status        string `json:"status"`
	Reason        string `json:"reason"`
	RequestedBy   string `json:"requested_by"`
	RequestedRole string `json:"requested_role"`
	DecidedBy     string `json:"decided_by,omitempty"`
	DecidedNote   string `json:"decided_note,omitempty"`
	CreatedAt     string `json:"created_at,omitempty"`
	DecidedAt     string `json:"decided_at,omitempty"`
}

// adminShopDTO extends shopDTO with admin-only operational flags.
type adminShopDTO struct {
	shopDTO
	BankReviewPending bool `json:"bank_review_pending"`
}

// publicShopDTO exposes only non-sensitive fields for unauthenticated storefront reads.
type publicShopDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at,omitempty"`
	UpdatedAt   string `json:"updated_at,omitempty"`
}

type addressDTO struct {
	ID                 string `json:"id"`
	UserID             string `json:"user_id"`
	RecipientName      string `json:"recipient_name"`
	RecipientPhone     string `json:"recipient_phone"`
	ProvinceCode       string `json:"province_code"`
	ProvinceName       string `json:"province_name"`
	WardCode           string `json:"ward_code"`
	WardName           string `json:"ward_name"`
	StreetAddress      string `json:"street_address"`
	LegacyProvinceCode string `json:"legacy_province_code"`
	LegacyDistrictCode string `json:"legacy_district_code"`
	LegacyWardCode     string `json:"legacy_ward_code"`
	IsDefault          bool   `json:"is_default"`
	CreatedAt          string `json:"created_at,omitempty"`
	UpdatedAt          string `json:"updated_at,omitempty"`
}

type auditLogDTO struct {
	ID         string `json:"id"`
	ActorID    string `json:"actor_id"`
	ActorRole  string `json:"actor_role"`
	Action     string `json:"action"`
	TargetType string `json:"target_type"`
	TargetID   string `json:"target_id"`
	BeforeJSON string `json:"before_json"`
	AfterJSON  string `json:"after_json"`
	Reason     string `json:"reason"`
	IPAddress  string `json:"ip_address"`
	CreatedAt  string `json:"created_at,omitempty"`
}

type shopBankAccountDTO struct {
	ID                  string `json:"id"`
	ShopID              string `json:"shop_id"`
	BankCode            string `json:"bank_code"`
	BankName            string `json:"bank_name"`
	AccountNumberMasked string `json:"account_number_masked"`
	AccountHolderName   string `json:"account_holder_name"`
	HolderNameMatched   bool   `json:"holder_name_matched"`
	Status              string `json:"status"`
	ReviewNote          string `json:"review_note"`
	PayoutLockedUntil   string `json:"payout_locked_until,omitempty"`
	VerifiedAt          string `json:"verified_at,omitempty"`
	CreatedAt           string `json:"created_at,omitempty"`
	UpdatedAt           string `json:"updated_at,omitempty"`
}

type dailyUserShopStatDTO struct {
	Date     string `json:"date"`
	NewUsers int64  `json:"new_users"`
	NewShops int64  `json:"new_shops"`
}

type platformUserStatsDTO struct {
	TotalUsers   int64                   `json:"total_users"`
	ActiveUsers  int64                   `json:"active_users"`
	NewUsers     int64                   `json:"new_users"`
	TotalShops   int64                   `json:"total_shops"`
	ActiveShops  int64                   `json:"active_shops"`
	PendingShops int64                   `json:"pending_shops"`
	NewShops     int64                   `json:"new_shops"`
	Daily        []*dailyUserShopStatDTO `json:"daily"`
}

func shopBankAccountStatusString(st userv1.ShopBankAccountStatus) string {
	switch st {
	case userv1.ShopBankAccountStatus_SHOP_BANK_ACCOUNT_STATUS_PENDING_OTP:
		return "PENDING_OTP"
	case userv1.ShopBankAccountStatus_SHOP_BANK_ACCOUNT_STATUS_PENDING_ADMIN_REVIEW:
		return "PENDING_ADMIN_REVIEW"
	case userv1.ShopBankAccountStatus_SHOP_BANK_ACCOUNT_STATUS_VERIFIED:
		return "VERIFIED"
	case userv1.ShopBankAccountStatus_SHOP_BANK_ACCOUNT_STATUS_REJECTED:
		return "REJECTED"
	default:
		return ""
	}
}

func mapShopBankAccount(ba *userv1.ShopBankAccount) *shopBankAccountDTO {
	if ba == nil {
		return nil
	}
	return &shopBankAccountDTO{
		ID:                  ba.GetId(),
		ShopID:              ba.GetShopId(),
		BankCode:            ba.GetBankCode(),
		BankName:            ba.GetBankName(),
		AccountNumberMasked: ba.GetAccountNumberMasked(),
		AccountHolderName:   ba.GetAccountHolderName(),
		HolderNameMatched:   ba.GetHolderNameMatched(),
		Status:              shopBankAccountStatusString(ba.GetStatus()),
		ReviewNote:          ba.GetReviewNote(),
		PayoutLockedUntil:   httpx.FormatTimestamp(ba.GetPayoutLockedUntil()),
		VerifiedAt:          httpx.FormatTimestamp(ba.GetVerifiedAt()),
		CreatedAt:           httpx.FormatTimestamp(ba.GetCreatedAt()),
		UpdatedAt:           httpx.FormatTimestamp(ba.GetUpdatedAt()),
	}
}

func mapPlatformUserStats(s *userv1.GetPlatformUserStatsResponse) *platformUserStatsDTO {
	if s == nil {
		return nil
	}
	daily := make([]*dailyUserShopStatDTO, 0, len(s.GetDaily()))
	for _, d := range s.GetDaily() {
		daily = append(daily, &dailyUserShopStatDTO{
			Date:     d.GetDate(),
			NewUsers: d.GetNewUsers(),
			NewShops: d.GetNewShops(),
		})
	}
	return &platformUserStatsDTO{
		TotalUsers:   s.GetTotalUsers(),
		ActiveUsers:  s.GetActiveUsers(),
		NewUsers:     s.GetNewUsers(),
		TotalShops:   s.GetTotalShops(),
		ActiveShops:  s.GetActiveShops(),
		PendingShops: s.GetPendingShops(),
		NewShops:     s.GetNewShops(),
		Daily:        daily,
	}
}

func mapUser(u *userv1.User) *userDTO {
	if u == nil {
		return nil
	}
	return &userDTO{
		ID:               u.GetId(),
		FullName:         u.GetFullName(),
		Email:            u.GetEmail(),
		Phone:            u.GetPhone(),
		Role:             userRoleString(u.GetRole()),
		Status:           userStatusString(u.GetStatus()),
		ShopID:           u.GetShopId(),
		PhoneVerified:    u.GetPhoneVerified(),
		CODRejectedCount: u.GetCodRejectedCount(),
		AvatarURL:        u.GetAvatarUrl(),
		LockReason:       u.GetLockReason(),
		CreatedAt:        httpx.FormatTimestamp(u.GetCreatedAt()),
		UpdatedAt:        httpx.FormatTimestamp(u.GetUpdatedAt()),
	}
}

func mapShop(s *userv1.Shop) *shopDTO {
	if s == nil {
		return nil
	}
	return &shopDTO{
		ID:                 s.GetId(),
		OwnerUserID:        s.GetOwnerUserId(),
		Name:               s.GetName(),
		Slug:               s.GetSlug(),
		Description:        s.GetDescription(),
		PickupAddressID:    s.GetPickupAddressId(),
		ContactPhone:       s.GetContactPhone(),
		BusinessLicenseDoc: s.GetBusinessLicenseDoc(),
		Status:             shopStatusString(s.GetStatus()),
		RejectionReason:    s.GetRejectionReason(),
		SuspendedReason:    s.GetSuspendedReason(),
		SuspendedAt:        httpx.FormatTimestamp(s.GetSuspendedAt()),
		StatusVersion:      s.GetStatusVersion(),
		CreatedAt:          httpx.FormatTimestamp(s.GetCreatedAt()),
		UpdatedAt:          httpx.FormatTimestamp(s.GetUpdatedAt()),
	}
}

func mapUserActivityEntry(e *userv1.UserActivityEntry) *userActivityEntryDTO {
	if e == nil {
		return nil
	}
	return &userActivityEntryDTO{
		ID:           e.GetId(),
		UserID:       e.GetUserId(),
		EventType:    e.GetEventType(),
		IPAddress:    e.GetIpAddress(),
		MetadataJSON: e.GetMetadataJson(),
		CreatedAt:    httpx.FormatTimestamp(e.GetCreatedAt()),
	}
}

func shopApprovalStatusString(st userv1.ShopApprovalStatus) string {
	switch st {
	case userv1.ShopApprovalStatus_SHOP_APPROVAL_STATUS_PENDING:
		return "PENDING"
	case userv1.ShopApprovalStatus_SHOP_APPROVAL_STATUS_APPROVED:
		return "APPROVED"
	case userv1.ShopApprovalStatus_SHOP_APPROVAL_STATUS_REJECTED:
		return "REJECTED"
	default:
		return ""
	}
}

func mapShopSuspensionApproval(a *userv1.ShopSuspensionApproval) *shopSuspensionApprovalDTO {
	if a == nil {
		return nil
	}
	return &shopSuspensionApprovalDTO{
		ID:            a.GetId(),
		ShopID:        a.GetShopId(),
		ShopName:      a.GetShopName(),
		Status:        shopApprovalStatusString(a.GetStatus()),
		Reason:        a.GetReason(),
		RequestedBy:   a.GetRequestedBy(),
		RequestedRole: a.GetRequestedRole(),
		DecidedBy:     a.GetDecidedBy(),
		DecidedNote:   a.GetDecidedNote(),
		CreatedAt:     httpx.FormatTimestamp(a.GetCreatedAt()),
		DecidedAt:     httpx.FormatTimestamp(a.GetDecidedAt()),
	}
}

func mapAdminShop(s *userv1.Shop) *adminShopDTO {
	if s == nil {
		return nil
	}
	return &adminShopDTO{
		shopDTO:           *mapShop(s),
		BankReviewPending: s.GetBankReviewPending(),
	}
}

func mapPublicShop(s *userv1.Shop) *publicShopDTO {
	if s == nil {
		return nil
	}
	return &publicShopDTO{
		ID:          s.GetId(),
		Name:        s.GetName(),
		Slug:        s.GetSlug(),
		Description: s.GetDescription(),
		Status:      shopStatusString(s.GetStatus()),
		CreatedAt:   httpx.FormatTimestamp(s.GetCreatedAt()),
		UpdatedAt:   httpx.FormatTimestamp(s.GetUpdatedAt()),
	}
}

func mapAddress(a *userv1.Address) *addressDTO {
	if a == nil {
		return nil
	}
	return &addressDTO{
		ID:                 a.GetId(),
		UserID:             a.GetUserId(),
		RecipientName:      a.GetRecipientName(),
		RecipientPhone:     a.GetRecipientPhone(),
		ProvinceCode:       a.GetProvinceCode(),
		ProvinceName:       a.GetProvinceName(),
		WardCode:           a.GetWardCode(),
		WardName:           a.GetWardName(),
		StreetAddress:      a.GetStreetAddress(),
		LegacyProvinceCode: a.GetLegacyProvinceCode(),
		LegacyDistrictCode: a.GetLegacyDistrictCode(),
		LegacyWardCode:     a.GetLegacyWardCode(),
		IsDefault:          a.GetIsDefault(),
		CreatedAt:          httpx.FormatTimestamp(a.GetCreatedAt()),
		UpdatedAt:          httpx.FormatTimestamp(a.GetUpdatedAt()),
	}
}

func mapAuditLog(l *userv1.AuditLog) *auditLogDTO {
	if l == nil {
		return nil
	}
	return &auditLogDTO{
		ID:         l.GetId(),
		ActorID:    l.GetActorId(),
		ActorRole:  l.GetActorRole(),
		Action:     l.GetAction(),
		TargetType: l.GetTargetType(),
		TargetID:   l.GetTargetId(),
		BeforeJSON: l.GetBeforeJson(),
		AfterJSON:  l.GetAfterJson(),
		Reason:     l.GetReason(),
		IPAddress:  l.GetIpAddress(),
		CreatedAt:  httpx.FormatTimestamp(l.GetCreatedAt()),
	}
}

func userRoleString(r userv1.UserRole) string {
	switch r {
	case userv1.UserRole_USER_ROLE_BUYER:
		return "buyer"
	case userv1.UserRole_USER_ROLE_SELLER:
		return "seller"
	case userv1.UserRole_USER_ROLE_ADMIN:
		return "admin"
	case userv1.UserRole_USER_ROLE_SUPER_ADMIN:
		return "super_admin"
	case userv1.UserRole_USER_ROLE_MODERATOR:
		return "moderator"
	case userv1.UserRole_USER_ROLE_CSKH:
		return "cskh"
	case userv1.UserRole_USER_ROLE_ACCOUNTANT:
		return "accountant"
	case userv1.UserRole_USER_ROLE_OPS:
		return "ops"
	default:
		return ""
	}
}

func parseUserRole(s string) userv1.UserRole {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "buyer", "user_role_buyer":
		return userv1.UserRole_USER_ROLE_BUYER
	case "seller", "user_role_seller":
		return userv1.UserRole_USER_ROLE_SELLER
	case "admin", "user_role_admin":
		return userv1.UserRole_USER_ROLE_ADMIN
	case "super_admin", "user_role_super_admin":
		return userv1.UserRole_USER_ROLE_SUPER_ADMIN
	case "moderator", "user_role_moderator":
		return userv1.UserRole_USER_ROLE_MODERATOR
	case "cskh", "user_role_cskh":
		return userv1.UserRole_USER_ROLE_CSKH
	case "accountant", "user_role_accountant":
		return userv1.UserRole_USER_ROLE_ACCOUNTANT
	case "ops", "user_role_ops":
		return userv1.UserRole_USER_ROLE_OPS
	default:
		return userv1.UserRole_USER_ROLE_UNSPECIFIED
	}
}

func userStatusString(st userv1.UserStatus) string {
	switch st {
	case userv1.UserStatus_USER_STATUS_PENDING_VERIFICATION:
		return "PENDING_VERIFICATION"
	case userv1.UserStatus_USER_STATUS_ACTIVE:
		return "ACTIVE"
	case userv1.UserStatus_USER_STATUS_LOCKED:
		return "LOCKED"
	default:
		return ""
	}
}

func parseUserStatus(s string) userv1.UserStatus {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "PENDING_VERIFICATION", "USER_STATUS_PENDING_VERIFICATION":
		return userv1.UserStatus_USER_STATUS_PENDING_VERIFICATION
	case "ACTIVE", "USER_STATUS_ACTIVE":
		return userv1.UserStatus_USER_STATUS_ACTIVE
	case "LOCKED", "USER_STATUS_LOCKED":
		return userv1.UserStatus_USER_STATUS_LOCKED
	default:
		return userv1.UserStatus_USER_STATUS_UNSPECIFIED
	}
}

func shopStatusString(st userv1.ShopStatus) string {
	switch st {
	case userv1.ShopStatus_SHOP_STATUS_PENDING:
		return "PENDING"
	case userv1.ShopStatus_SHOP_STATUS_ACTIVE:
		return "ACTIVE"
	case userv1.ShopStatus_SHOP_STATUS_REJECTED:
		return "REJECTED"
	case userv1.ShopStatus_SHOP_STATUS_SUSPENDED:
		return "SUSPENDED"
	default:
		return ""
	}
}

func parseShopStatus(s string) userv1.ShopStatus {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "PENDING", "SHOP_STATUS_PENDING":
		return userv1.ShopStatus_SHOP_STATUS_PENDING
	case "ACTIVE", "SHOP_STATUS_ACTIVE":
		return userv1.ShopStatus_SHOP_STATUS_ACTIVE
	case "REJECTED", "SHOP_STATUS_REJECTED":
		return userv1.ShopStatus_SHOP_STATUS_REJECTED
	case "SUSPENDED", "SHOP_STATUS_SUSPENDED":
		return userv1.ShopStatus_SHOP_STATUS_SUSPENDED
	default:
		return userv1.ShopStatus_SHOP_STATUS_UNSPECIFIED
	}
}

func parseReviewDecision(s string) userv1.ReviewDecision {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "APPROVE", "REVIEW_DECISION_APPROVE":
		return userv1.ReviewDecision_REVIEW_DECISION_APPROVE
	case "REJECT", "REVIEW_DECISION_REJECT":
		return userv1.ReviewDecision_REVIEW_DECISION_REJECT
	case "REQUEST_INFO", "REVIEW_DECISION_REQUEST_INFO":
		return userv1.ReviewDecision_REVIEW_DECISION_REQUEST_INFO
	default:
		return userv1.ReviewDecision_REVIEW_DECISION_UNSPECIFIED
	}
}

func (h *handler) handleRegister(w http.ResponseWriter, r *http.Request) {
	var body struct {
		FullName string `json:"full_name"`
		Email    string `json:"email"`
		Phone    string `json:"phone"`
		Password string `json:"password"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.Register(ctx, &userv1.RegisterRequest{
		FullName: body.FullName,
		Email:    body.Email,
		Phone:    body.Phone,
		Password: body.Password,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"user_id": resp.GetUserId(),
		"email":   resp.GetEmail(),
		"status":  resp.GetStatus(),
	})
}

func (h *handler) handleVerifyOTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
		OTP   string `json:"otp"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.VerifyOTP(ctx, &userv1.VerifyOTPRequest{
		Email: body.Email,
		Otp:   body.OTP,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"verified": resp.GetVerified(),
		"user":     mapUser(resp.GetUser()),
	})
}

func (h *handler) handleResendOTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.ResendOTP(ctx, &userv1.ResendOTPRequest{
		Email: body.Email,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"sent": resp.GetSent(),
	})
}

func (h *handler) handleForgotPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.ForgotPassword(ctx, &userv1.ForgotPasswordRequest{
		Email: body.Email,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"sent": resp.GetSent(),
	})
}

func (h *handler) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token       string `json:"token"`
		NewPassword string `json:"new_password"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.ResetPassword(ctx, &userv1.ResetPasswordRequest{
		Token:       body.Token,
		NewPassword: body.NewPassword,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"ok": resp.GetOk(),
	})
}

func (h *handler) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.Login(ctx, &userv1.LoginRequest{
		Email:    body.Email,
		Password: body.Password,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"access_token":       resp.GetAccessToken(),
		"refresh_token":      resp.GetRefreshToken(),
		"expires_in_seconds": resp.GetExpiresInSeconds(),
		"user":               mapUser(resp.GetUser()),
	})
}

func (h *handler) handleRefreshToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.RefreshToken(ctx, &userv1.RefreshTokenRequest{
		RefreshToken: body.RefreshToken,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"access_token":       resp.GetAccessToken(),
		"refresh_token":      resp.GetRefreshToken(),
		"expires_in_seconds": resp.GetExpiresInSeconds(),
	})
}

func (h *handler) handleLogout(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := httpx.DecodeOptionalJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.Logout(ctx, &userv1.LogoutRequest{
		RefreshToken: body.RefreshToken,
		UserId:       auth.UserIDFromContext(r.Context()),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": resp.GetOk()})
}

func (h *handler) handleGetMe(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.GetProfile(ctx, &userv1.GetProfileRequest{
		UserId: auth.UserIDFromContext(r.Context()),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"user": mapUser(resp.GetUser())})
}

func (h *handler) handleUpdateMe(w http.ResponseWriter, r *http.Request) {
	var body struct {
		FullName  string `json:"full_name"`
		Phone     string `json:"phone"`
		AvatarURL string `json:"avatar_url"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.UpdateProfile(ctx, &userv1.UpdateProfileRequest{
		UserId:    auth.UserIDFromContext(r.Context()),
		FullName:  body.FullName,
		Phone:     body.Phone,
		AvatarUrl: body.AvatarURL,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"user": mapUser(resp.GetUser())})
}

func (h *handler) handleRequestPhoneOTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Phone string `json:"phone"`
	}
	if err := httpx.DecodeOptionalJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.RequestPhoneOTP(ctx, &userv1.RequestPhoneOTPRequest{
		Phone: strings.TrimSpace(body.Phone),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"sent":                resp.GetSent(),
		"expires_in_seconds":  resp.GetExpiresInSeconds(),
		"retry_after_seconds": resp.GetRetryAfterSeconds(),
		"phone_masked":        resp.GetPhoneMasked(),
	})
}

func (h *handler) handleVerifyPhoneOTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Phone string `json:"phone"`
		OTP   string `json:"otp"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	otp := strings.TrimSpace(body.OTP)
	if otp == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "otp is required")
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.VerifyPhoneOTP(ctx, &userv1.VerifyPhoneOTPRequest{
		Phone: strings.TrimSpace(body.Phone),
		Otp:   otp,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"verified": resp.GetVerified(),
		"user":     mapUser(resp.GetUser()),
	})
}

func (h *handler) handleListAddresses(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.ListAddresses(ctx, &userv1.ListAddressesRequest{
		UserId: auth.UserIDFromContext(r.Context()),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	addrs := make([]*addressDTO, 0, len(resp.GetAddresses()))
	for _, a := range resp.GetAddresses() {
		addrs = append(addrs, mapAddress(a))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"addresses": addrs})
}

func (h *handler) handleCreateAddress(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RecipientName      string `json:"recipient_name"`
		RecipientPhone     string `json:"recipient_phone"`
		ProvinceCode       string `json:"province_code"`
		ProvinceName       string `json:"province_name"`
		WardCode           string `json:"ward_code"`
		WardName           string `json:"ward_name"`
		StreetAddress      string `json:"street_address"`
		IsDefault          bool   `json:"is_default"`
		LegacyProvince     string `json:"legacy_province"`
		LegacyDistrict     string `json:"legacy_district"`
		LegacyWard         string `json:"legacy_ward"`
		LegacyProvinceCode string `json:"legacy_province_code"`
		LegacyDistrictCode string `json:"legacy_district_code"`
		LegacyWardCode     string `json:"legacy_ward_code"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	if body.LegacyProvince == "" {
		body.LegacyProvince = body.LegacyProvinceCode
	}
	if body.LegacyDistrict == "" {
		body.LegacyDistrict = body.LegacyDistrictCode
	}
	if body.LegacyWard == "" {
		body.LegacyWard = body.LegacyWardCode
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.CreateAddress(ctx, &userv1.CreateAddressRequest{
		UserId:         auth.UserIDFromContext(r.Context()),
		RecipientName:  body.RecipientName,
		RecipientPhone: body.RecipientPhone,
		ProvinceCode:   body.ProvinceCode,
		ProvinceName:   body.ProvinceName,
		WardCode:       body.WardCode,
		WardName:       body.WardName,
		StreetAddress:  body.StreetAddress,
		LegacyProvince: body.LegacyProvince,
		LegacyDistrict: body.LegacyDistrict,
		LegacyWard:     body.LegacyWard,
		IsDefault:      body.IsDefault,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"address": mapAddress(resp.GetAddress())})
}

func (h *handler) handleUpdateAddress(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RecipientName  string `json:"recipient_name"`
		RecipientPhone string `json:"recipient_phone"`
		ProvinceCode   string `json:"province_code"`
		ProvinceName   string `json:"province_name"`
		WardCode       string `json:"ward_code"`
		WardName       string `json:"ward_name"`
		StreetAddress  string `json:"street_address"`
		IsDefault      bool   `json:"is_default"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.UpdateAddress(ctx, &userv1.UpdateAddressRequest{
		UserId:         auth.UserIDFromContext(r.Context()),
		AddressId:      chi.URLParam(r, "id"),
		RecipientName:  body.RecipientName,
		RecipientPhone: body.RecipientPhone,
		ProvinceCode:   body.ProvinceCode,
		ProvinceName:   body.ProvinceName,
		WardCode:       body.WardCode,
		WardName:       body.WardName,
		StreetAddress:  body.StreetAddress,
		IsDefault:      body.IsDefault,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"address": mapAddress(resp.GetAddress())})
}

func (h *handler) handleDeleteAddress(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.DeleteAddress(ctx, &userv1.DeleteAddressRequest{
		UserId:    auth.UserIDFromContext(r.Context()),
		AddressId: chi.URLParam(r, "id"),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": resp.GetOk()})
}

func (h *handler) handleRegisterShop(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name               string `json:"name"`
		Slug               string `json:"slug"`
		Description        string `json:"description"`
		PickupAddressID    string `json:"pickup_address_id"`
		ContactPhone       string `json:"contact_phone"`
		BusinessLicenseDoc string `json:"business_license_doc"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.RegisterShop(ctx, &userv1.RegisterShopRequest{
		UserId:             auth.UserIDFromContext(r.Context()),
		Name:               body.Name,
		Slug:               body.Slug,
		Description:        body.Description,
		PickupAddressId:    body.PickupAddressID,
		ContactPhone:       body.ContactPhone,
		BusinessLicenseDoc: body.BusinessLicenseDoc,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"shop": mapShop(resp.GetShop())})
}

func (h *handler) handleListPublicShops(w http.ResponseWriter, r *http.Request) {
	page, pageSize, err := httpx.ParsePagination(r)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.ListPublicShops(ctx, &userv1.ListPublicShopsRequest{
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	items := make([]*publicShopDTO, 0, len(resp.GetShops()))
	for _, s := range resp.GetShops() {
		if s.GetStatus() != userv1.ShopStatus_SHOP_STATUS_ACTIVE {
			continue
		}
		items = append(items, mapPublicShop(s))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items":     items,
		"page":      page,
		"page_size": pageSize,
		"total":     resp.GetTotal(),
	})
}

// handleGetShop serves the public storefront view. Owners and staff with shops.read get the full
// record; everyone else only sees ACTIVE shops and only public fields.
func (h *handler) handleGetShop(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.GetShop(ctx, &userv1.GetShopRequest{
		ShopId: chi.URLParam(r, "id"),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	shop := resp.GetShop()
	claims, _ := auth.ClaimsFromContext(r.Context())
	privileged := claims != nil && shop != nil && (auth.HasCapability(claims, auth.CapShopsRead) ||
		(claims.UserID() != "" && claims.UserID() == shop.GetOwnerUserId()) ||
		(claims.ShopID != "" && claims.ShopID == shop.GetId()))
	if privileged {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"shop": mapShop(shop)})
		return
	}
	if shop == nil || shop.GetStatus() != userv1.ShopStatus_SHOP_STATUS_ACTIVE {
		httpx.WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "shop not found")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"shop": mapPublicShop(shop)})
}

func (h *handler) handleGetShopBySlug(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.GetShop(ctx, &userv1.GetShopRequest{
		Slug: chi.URLParam(r, "slug"),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	shop := resp.GetShop()
	if shop == nil || shop.GetStatus() != userv1.ShopStatus_SHOP_STATUS_ACTIVE {
		httpx.WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "shop not found")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"shop": mapPublicShop(shop)})
}

func (h *handler) handleGetMyShop(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.GetShop(ctx, &userv1.GetShopRequest{
		OwnerUserId: auth.UserIDFromContext(r.Context()),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"shop": mapShop(resp.GetShop())})
}

// handleSellerGetShop returns the caller's own shop; the shop id comes from the JWT.
func (h *handler) handleSellerGetShop(w http.ResponseWriter, r *http.Request) {
	shopID, ok := requireSellerShopID(w, r)
	if !ok {
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.GetShop(ctx, &userv1.GetShopRequest{
		ShopId: shopID,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"shop": mapShop(resp.GetShop())})
}

func (h *handler) handleAdminListUsers(w http.ResponseWriter, r *http.Request) {
	page, pageSize, err := httpx.ParsePagination(r)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	q := r.URL.Query()
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.AdminListUsers(ctx, &userv1.AdminListUsersRequest{
		Query:    q.Get("query"),
		Role:     parseUserRole(q.Get("role")),
		Status:   parseUserStatus(q.Get("status")),
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	items := make([]*userDTO, 0, len(resp.GetUsers()))
	for _, u := range resp.GetUsers() {
		items = append(items, mapUser(u))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items":     items,
		"page":      page,
		"page_size": pageSize,
		"total":     resp.GetTotal(),
	})
}

func (h *handler) handleAdminUpdateUserStatus(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.AdminUpdateUserStatus(ctx, &userv1.AdminUpdateUserStatusRequest{
		AdminId:      auth.UserIDFromContext(r.Context()),
		TargetUserId: chi.URLParam(r, "id"),
		Status:       parseUserStatus(body.Status),
		Reason:       body.Reason,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"user": mapUser(resp.GetUser())})
}

func isAssignableUserRole(r userv1.UserRole) bool {
	switch r {
	case userv1.UserRole_USER_ROLE_BUYER,
		userv1.UserRole_USER_ROLE_SELLER,
		userv1.UserRole_USER_ROLE_ADMIN,
		userv1.UserRole_USER_ROLE_MODERATOR,
		userv1.UserRole_USER_ROLE_CSKH,
		userv1.UserRole_USER_ROLE_ACCOUNTANT,
		userv1.UserRole_USER_ROLE_OPS:
		return true
	default:
		return false
	}
}

func (h *handler) handleAdminUpdateUserRole(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Role   string `json:"role"`
		Reason string `json:"reason"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	role := parseUserRole(body.Role)
	if !isAssignableUserRole(role) {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "role must be buyer, seller, admin, moderator, cskh, accountant, or ops")
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.AdminUpdateUserRole(ctx, &userv1.AdminUpdateUserRoleRequest{
		AdminId:      auth.UserIDFromContext(r.Context()),
		TargetUserId: chi.URLParam(r, "id"),
		Role:         role,
		Reason:       body.Reason,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"user": mapUser(resp.GetUser())})
}

func (h *handler) handleAdminListShops(w http.ResponseWriter, r *http.Request) {
	page, pageSize, err := httpx.ParsePagination(r)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.AdminListShops(ctx, &userv1.AdminListShopsRequest{
		Status:                parseShopStatus(r.URL.Query().Get("status")),
		BankReviewPendingOnly: httpx.ParseBoolQuery(r, "bank_review_pending"),
		Page:                  page,
		PageSize:              pageSize,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	items := make([]*adminShopDTO, 0, len(resp.GetShops()))
	for _, s := range resp.GetShops() {
		items = append(items, mapAdminShop(s))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items":     items,
		"page":      page,
		"page_size": pageSize,
		"total":     resp.GetTotal(),
	})
}

func (h *handler) handleAdminReviewShop(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Decision          string `json:"decision"`
		Reason            string `json:"reason"`
		ExpectedOwnerName string `json:"expected_owner_name"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.AdminReviewShop(ctx, &userv1.AdminReviewShopRequest{
		AdminId:           auth.UserIDFromContext(r.Context()),
		ShopId:            chi.URLParam(r, "id"),
		Decision:          parseReviewDecision(body.Decision),
		Reason:            body.Reason,
		ExpectedOwnerName: body.ExpectedOwnerName,
	})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.FailedPrecondition && strings.Contains(st.Message(), "OWNER_NAME_CHANGED") {
			httpx.WriteError(w, r, http.StatusConflict, "OWNER_NAME_CHANGED", st.Message())
			return
		}
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"shop": mapAdminShop(resp.GetShop())})
}

func (h *handler) handleAdminListAuditLogs(w http.ResponseWriter, r *http.Request) {
	page, pageSize, err := httpx.ParsePagination(r)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	q := r.URL.Query()

	fromTS, err := httpx.ParseTimestamp(q.Get("from_time"))
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid from_time format")
		return
	}
	toTS, err := httpx.ParseTimestamp(q.Get("to_time"))
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid to_time format")
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.ListAuditLogs(ctx, &userv1.ListAuditLogsRequest{
		ActorId:    q.Get("actor_id"),
		TargetType: q.Get("target_type"),
		TargetId:   q.Get("target_id"),
		FromTime:   fromTS,
		ToTime:     toTS,
		Page:       page,
		PageSize:   pageSize,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	items := make([]*auditLogDTO, 0, len(resp.GetLogs()))
	for _, l := range resp.GetLogs() {
		items = append(items, mapAuditLog(l))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items":     items,
		"page":      page,
		"page_size": pageSize,
		"total":     resp.GetTotal(),
	})
}

func (h *handler) handleSellerGetBankAccount(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.GetShopBankAccount(ctx, &userv1.GetShopBankAccountRequest{
		ShopId: auth.ShopIDFromContext(r.Context()),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"bank_account": mapShopBankAccount(resp.GetBankAccount()),
	})
}

func (h *handler) handleSellerAddBankAccount(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BankCode          string `json:"bank_code"`
		BankName          string `json:"bank_name"`
		AccountNumber     string `json:"account_number"`
		AccountHolderName string `json:"account_holder_name"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.AddShopBankAccount(ctx, &userv1.AddShopBankAccountRequest{
		ShopId:            auth.ShopIDFromContext(r.Context()),
		BankCode:          body.BankCode,
		BankName:          body.BankName,
		AccountNumber:     body.AccountNumber,
		AccountHolderName: body.AccountHolderName,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"bank_account": mapShopBankAccount(resp.GetBankAccount()),
		"otp_sent":     resp.GetOtpSent(),
	})
}

func (h *handler) handleSellerVerifyBankAccountOTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OTP string `json:"otp"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.VerifyShopBankAccountOTP(ctx, &userv1.VerifyShopBankAccountOTPRequest{
		ShopId: auth.ShopIDFromContext(r.Context()),
		Otp:    body.OTP,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"bank_account": mapShopBankAccount(resp.GetBankAccount()),
	})
}

func (h *handler) handleAdminGetShopBankAccount(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.GetShopBankAccount(ctx, &userv1.GetShopBankAccountRequest{
		ShopId: chi.URLParam(r, "id"),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"bank_account": mapShopBankAccount(resp.GetBankAccount()),
	})
}

func (h *handler) handleAdminReviewShopBankAccount(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BankAccountID string `json:"bank_account_id"`
		Decision      string `json:"decision"`
		Reason        string `json:"reason"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	bankAccountID := strings.TrimSpace(body.BankAccountID)
	if bankAccountID == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "bank_account_id is required")
		return
	}
	decision := parseReviewDecision(body.Decision)
	if decision != userv1.ReviewDecision_REVIEW_DECISION_APPROVE && decision != userv1.ReviewDecision_REVIEW_DECISION_REJECT {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "decision must be APPROVE or REJECT")
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.AdminReviewShopBankAccount(ctx, &userv1.AdminReviewShopBankAccountRequest{
		ShopId:        chi.URLParam(r, "id"),
		BankAccountId: bankAccountID,
		Decision:      decision,
		Reason:        body.Reason,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"bank_account": mapShopBankAccount(resp.GetBankAccount()),
	})
}

// --- Wave 3b-2 (A7): Staff Creation, User Activities, Shop Suspension & Unified Approvals ---

func isCreatableStaffRole(r userv1.UserRole) bool {
	switch r {
	case userv1.UserRole_USER_ROLE_ADMIN,
		userv1.UserRole_USER_ROLE_MODERATOR,
		userv1.UserRole_USER_ROLE_CSKH,
		userv1.UserRole_USER_ROLE_ACCOUNTANT,
		userv1.UserRole_USER_ROLE_OPS:
		return true
	default:
		return false
	}
}

func (h *handler) handleAdminCreateStaffUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		FullName          string           `json:"full_name"`
		Email             string           `json:"email"`
		Phone             string           `json:"phone"`
		Role              string           `json:"role"`
		Password          *json.RawMessage `json:"password"`
		TemporaryPassword *json.RawMessage `json:"temporary_password"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	// F14: staff creation must never accept an initial password; server sends a password-reset email instead.
	if body.Password != nil || body.TemporaryPassword != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "password field is not accepted; staff accounts receive a password reset email")
		return
	}
	role := parseUserRole(body.Role)
	if !isCreatableStaffRole(role) {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "role must be admin, moderator, cskh, accountant, or ops")
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.AdminCreateStaffUser(ctx, &userv1.AdminCreateStaffUserRequest{
		FullName: body.FullName,
		Email:    body.Email,
		Phone:    body.Phone,
		Role:     role,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"user":                mapUser(resp.GetUser()),
		"password_reset_sent": resp.GetPasswordResetSent(),
	})
}

func (h *handler) handleAdminListUserActivities(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(chi.URLParam(r, "id"))
	if userID == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "user id is required")
		return
	}
	page, pageSize, err := httpx.ParsePagination(r)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	q := r.URL.Query()

	fromTS, err := httpx.ParseTimestamp(q.Get("from_time"))
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid from_time format")
		return
	}
	toTS, err := httpx.ParseTimestamp(q.Get("to_time"))
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid to_time format")
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.AdminListUserActivities(ctx, &userv1.AdminListUserActivitiesRequest{
		UserId:   userID,
		FromTime: fromTS,
		ToTime:   toTS,
		Category: strings.TrimSpace(q.Get("category")),
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	items := make([]*userActivityEntryDTO, 0, len(resp.GetItems()))
	for _, entry := range resp.GetItems() {
		items = append(items, mapUserActivityEntry(entry))
	}

	// F13: call OrderService.ListOrders using viewer's JWT ONLY when viewer has orders.read;
	// otherwise return an empty slice without calling order (prevents 403 for moderator).
	recentOrders := []*orderDTO{}
	claims, _ := auth.ClaimsFromContext(r.Context())
	if auth.HasCapability(claims, auth.CapOrdersRead) && h.clients.Order != nil {
		ordResp, ordErr := h.clients.Order.ListOrders(ctx, &orderv1.ListOrdersRequest{
			BuyerId:   userID,
			RoleScope: "admin",
			Page:      1,
			PageSize:  10,
		})
		if ordErr != nil {
			httpx.WriteGRPCError(w, r, ordErr)
			return
		}
		recentOrders = make([]*orderDTO, 0, len(ordResp.GetOrders()))
		for _, o := range ordResp.GetOrders() {
			recentOrders = append(recentOrders, mapOrderForAdmin(o))
		}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items":              items,
		"cod_rejected_count": resp.GetCodRejectedCount(),
		"recent_orders":      recentOrders,
		"page":               page,
		"page_size":          pageSize,
		"total":              resp.GetTotal(),
	})
}

func (h *handler) handleAdminSuspendShop(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reason string `json:"reason"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	reason := strings.TrimSpace(body.Reason)
	if reason == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "reason is required")
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.AdminRequestShopSuspension(ctx, &userv1.AdminRequestShopSuspensionRequest{
		ShopId: chi.URLParam(r, "id"),
		Reason: reason,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"approval": mapShopSuspensionApproval(resp.GetApproval()),
	})
}

func (h *handler) handleAdminUnsuspendShop(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reason string `json:"reason"`
	}
	if err := httpx.DecodeOptionalJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.AdminUnsuspendShop(ctx, &userv1.AdminUnsuspendShopRequest{
		ShopId: chi.URLParam(r, "id"),
		Reason: strings.TrimSpace(body.Reason),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"shop": mapAdminShop(resp.GetShop()),
	})
}

func (h *handler) handleAdminDecideShopSuspension(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Decision string `json:"decision"`
		Note     string `json:"note"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	decision := parseReviewDecision(body.Decision)
	if decision != userv1.ReviewDecision_REVIEW_DECISION_APPROVE && decision != userv1.ReviewDecision_REVIEW_DECISION_REJECT {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "decision must be APPROVE or REJECT")
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.AdminDecideShopSuspension(ctx, &userv1.AdminDecideShopSuspensionRequest{
		ApprovalId: chi.URLParam(r, "id"),
		Decision:   decision,
		Note:       body.Note,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"approval": mapShopSuspensionApproval(resp.GetApproval()),
		"shop":     mapAdminShop(resp.GetShop()),
	})
}

func (h *handler) handleAdminListApprovals(w http.ResponseWriter, r *http.Request) {
	page, pageSize, err := httpx.ParsePagination(r)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	q := r.URL.Query()

	approvalType := strings.ToLower(strings.TrimSpace(q.Get("type")))
	if approvalType != "" && approvalType != "shop" && approvalType != "refund" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "type must be shop or refund")
		return
	}

	rawStatus := strings.ToUpper(strings.TrimSpace(q.Get("status")))
	var shopStatus userv1.ShopApprovalStatus
	var refundStatus paymentv1.RefundStatus
	queryRefundsForStatus := true

	switch rawStatus {
	case "", "PENDING", "PENDING_APPROVAL":
		shopStatus = userv1.ShopApprovalStatus_SHOP_APPROVAL_STATUS_PENDING
		refundStatus = paymentv1.RefundStatus_REFUND_STATUS_PENDING_APPROVAL
	case "APPROVED":
		shopStatus = userv1.ShopApprovalStatus_SHOP_APPROVAL_STATUS_APPROVED
		// Approved refunds transition into standard PENDING/DONE lifecycle, so omit unrelated refunds here.
		queryRefundsForStatus = false
	case "REJECTED":
		shopStatus = userv1.ShopApprovalStatus_SHOP_APPROVAL_STATUS_REJECTED
		refundStatus = paymentv1.RefundStatus_REFUND_STATUS_REJECTED
	case "ALL":
		shopStatus = userv1.ShopApprovalStatus_SHOP_APPROVAL_STATUS_UNSPECIFIED
		refundStatus = paymentv1.RefundStatus_REFUND_STATUS_PENDING_APPROVAL
	default:
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "status must be PENDING, PENDING_APPROVAL, APPROVED, REJECTED, or ALL")
		return
	}

	claims, _ := auth.ClaimsFromContext(r.Context())
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	shopApprovals := []*shopSuspensionApprovalDTO{}
	var shopTotal int64
	if (approvalType == "" || approvalType == "shop") &&
		auth.HasCapability(claims, auth.CapShopsApprovalsRead) &&
		h.clients.User != nil {
		sResp, err := h.clients.User.AdminListShopApprovals(ctx, &userv1.AdminListShopApprovalsRequest{
			Status:   shopStatus,
			ShopId:   strings.TrimSpace(q.Get("shop_id")),
			Page:     page,
			PageSize: pageSize,
		})
		if err != nil {
			httpx.WriteGRPCError(w, r, err)
			return
		}
		shopApprovals = make([]*shopSuspensionApprovalDTO, 0, len(sResp.GetItems()))
		for _, item := range sResp.GetItems() {
			shopApprovals = append(shopApprovals, mapShopSuspensionApproval(item))
		}
		shopTotal = sResp.GetTotal()
	}

	refundApprovals := []*refundDTO{}
	var refundTotal int64
	if (approvalType == "" || approvalType == "refund") &&
		queryRefundsForStatus &&
		auth.HasCapability(claims, auth.CapRefundsRead) &&
		h.clients.Payment != nil {
		rResp, err := h.clients.Payment.ListRefunds(ctx, &paymentv1.ListRefundsRequest{
			Status:   refundStatus,
			OrderId:  strings.TrimSpace(q.Get("order_id")),
			Page:     page,
			PageSize: pageSize,
		})
		if err != nil {
			httpx.WriteGRPCError(w, r, err)
			return
		}
		refundApprovals = make([]*refundDTO, 0, len(rResp.GetRefunds()))
		for _, rf := range rResp.GetRefunds() {
			refundApprovals = append(refundApprovals, mapRefund(rf, true))
		}
		refundTotal = rResp.GetTotal()
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"shop_approvals":   shopApprovals,
		"refund_approvals": refundApprovals,
		"shop_total":       shopTotal,
		"refund_total":     refundTotal,
	})
}
