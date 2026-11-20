package router

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/gateway/internal/httpx"
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
	CreatedAt        string `json:"created_at,omitempty"`
	UpdatedAt        string `json:"updated_at,omitempty"`
}

type shopDTO struct {
	ID                 string `json:"id"`
	OwnerUserID        string `json:"owner_user_id"`
	Name               string `json:"name"`
	Description        string `json:"description"`
	PickupAddressID    string `json:"pickup_address_id"`
	ContactPhone       string `json:"contact_phone"`
	BusinessLicenseDoc string `json:"business_license_doc"`
	Status             string `json:"status"`
	RejectionReason    string `json:"rejection_reason"`
	CreatedAt          string `json:"created_at,omitempty"`
	UpdatedAt          string `json:"updated_at,omitempty"`
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
		Description:        s.GetDescription(),
		PickupAddressID:    s.GetPickupAddressId(),
		ContactPhone:       s.GetContactPhone(),
		BusinessLicenseDoc: s.GetBusinessLicenseDoc(),
		Status:             shopStatusString(s.GetStatus()),
		RejectionReason:    s.GetRejectionReason(),
		CreatedAt:          httpx.FormatTimestamp(s.GetCreatedAt()),
		UpdatedAt:          httpx.FormatTimestamp(s.GetUpdatedAt()),
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
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
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
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
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
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
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

func (h *handler) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
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
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
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
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
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
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
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
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
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
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
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
		Description        string `json:"description"`
		PickupAddressID    string `json:"pickup_address_id"`
		ContactPhone       string `json:"contact_phone"`
		BusinessLicenseDoc string `json:"business_license_doc"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.RegisterShop(ctx, &userv1.RegisterShopRequest{
		UserId:             auth.UserIDFromContext(r.Context()),
		Name:               body.Name,
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
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"shop": mapShop(resp.GetShop())})
}

func (h *handler) handleAdminListUsers(w http.ResponseWriter, r *http.Request) {
	page, pageSize := httpx.ParsePagination(r)
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
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
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

func (h *handler) handleAdminUpdateUserRole(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Role   string `json:"role"`
		Reason string `json:"reason"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.AdminUpdateUserRole(ctx, &userv1.AdminUpdateUserRoleRequest{
		AdminId:      auth.UserIDFromContext(r.Context()),
		TargetUserId: chi.URLParam(r, "id"),
		Role:         parseUserRole(body.Role),
		Reason:       body.Reason,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"user": mapUser(resp.GetUser())})
}

func (h *handler) handleAdminListShops(w http.ResponseWriter, r *http.Request) {
	page, pageSize := httpx.ParsePagination(r)
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.AdminListShops(ctx, &userv1.AdminListShopsRequest{
		Status:   parseShopStatus(r.URL.Query().Get("status")),
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	items := make([]*shopDTO, 0, len(resp.GetShops()))
	for _, s := range resp.GetShops() {
		items = append(items, mapShop(s))
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
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.User.AdminReviewShop(ctx, &userv1.AdminReviewShopRequest{
		AdminId:  auth.UserIDFromContext(r.Context()),
		ShopId:   chi.URLParam(r, "id"),
		Decision: parseReviewDecision(body.Decision),
		Reason:   body.Reason,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"shop": mapShop(resp.GetShop())})
}

func (h *handler) handleAdminListAuditLogs(w http.ResponseWriter, r *http.Request) {
	page, pageSize := httpx.ParsePagination(r)
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
