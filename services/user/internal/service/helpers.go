package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/user/internal/db"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	RoleBuyer      = "buyer"
	RoleSeller     = "seller"
	RoleAdmin      = "admin"
	RoleSuperAdmin = "super_admin"

	StatusPendingVerification = "PENDING_VERIFICATION"
	StatusActive              = "ACTIVE"
	StatusLocked              = "LOCKED"

	ShopStatusPending   = "PENDING"
	ShopStatusActive    = "ACTIVE"
	ShopStatusRejected  = "REJECTED"
	ShopStatusSuspended = "SUSPENDED"
)

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func isValidEmail(email string) bool {
	at := strings.IndexByte(email, '@')
	if at <= 0 || at >= len(email)-1 {
		return false
	}
	domain := email[at+1:]
	return strings.Contains(domain, ".") && !strings.HasPrefix(domain, ".") && !strings.HasSuffix(domain, ".")
}

func isValidUUID(s string) bool {
	_, err := uuid.Parse(s)
	return err == nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}

func isNotFoundErr(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}

func generateOTP() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", fmt.Errorf("generate otp: %w", err)
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

func generateOpaqueToken() (plain string, hash string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("generate refresh token: %w", err)
	}
	plain = base64.RawURLEncoding.EncodeToString(buf)
	hash = hashToken(plain)
	return plain, hash, nil
}

func hashToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

func isAdminRole(role string) bool {
	return strings.EqualFold(role, RoleAdmin) || strings.EqualFold(role, RoleSuperAdmin)
}

func requireAuthClaims(ctx context.Context) (*auth.Claims, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok || claims.UserID() == "" {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}
	return claims, nil
}

func normalizePagination(page, pageSize int32) (int32, int32) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return page, pageSize
}

func peerIPFromContext(ctx context.Context) string {
	if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
		return p.Addr.String()
	}
	return ""
}

func marshalAuditJSON(v any) string {
	if v == nil {
		return "{}"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func roleToProto(role string) userv1.UserRole {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case RoleBuyer:
		return userv1.UserRole_USER_ROLE_BUYER
	case RoleSeller:
		return userv1.UserRole_USER_ROLE_SELLER
	case RoleAdmin:
		return userv1.UserRole_USER_ROLE_ADMIN
	case RoleSuperAdmin:
		return userv1.UserRole_USER_ROLE_SUPER_ADMIN
	default:
		return userv1.UserRole_USER_ROLE_UNSPECIFIED
	}
}

func roleFromProto(role userv1.UserRole) string {
	switch role {
	case userv1.UserRole_USER_ROLE_BUYER:
		return RoleBuyer
	case userv1.UserRole_USER_ROLE_SELLER:
		return RoleSeller
	case userv1.UserRole_USER_ROLE_ADMIN:
		return RoleAdmin
	case userv1.UserRole_USER_ROLE_SUPER_ADMIN:
		return RoleSuperAdmin
	default:
		return ""
	}
}

func statusToProto(st string) userv1.UserStatus {
	switch strings.ToUpper(strings.TrimSpace(st)) {
	case StatusPendingVerification:
		return userv1.UserStatus_USER_STATUS_PENDING_VERIFICATION
	case StatusActive:
		return userv1.UserStatus_USER_STATUS_ACTIVE
	case StatusLocked:
		return userv1.UserStatus_USER_STATUS_LOCKED
	default:
		return userv1.UserStatus_USER_STATUS_UNSPECIFIED
	}
}

func statusFromProto(st userv1.UserStatus) string {
	switch st {
	case userv1.UserStatus_USER_STATUS_PENDING_VERIFICATION:
		return StatusPendingVerification
	case userv1.UserStatus_USER_STATUS_ACTIVE:
		return StatusActive
	case userv1.UserStatus_USER_STATUS_LOCKED:
		return StatusLocked
	default:
		return ""
	}
}

func shopStatusToProto(st string) userv1.ShopStatus {
	switch strings.ToUpper(strings.TrimSpace(st)) {
	case ShopStatusPending:
		return userv1.ShopStatus_SHOP_STATUS_PENDING
	case ShopStatusActive:
		return userv1.ShopStatus_SHOP_STATUS_ACTIVE
	case ShopStatusRejected:
		return userv1.ShopStatus_SHOP_STATUS_REJECTED
	case ShopStatusSuspended:
		return userv1.ShopStatus_SHOP_STATUS_SUSPENDED
	default:
		return userv1.ShopStatus_SHOP_STATUS_UNSPECIFIED
	}
}

func shopStatusFromProto(st userv1.ShopStatus) string {
	switch st {
	case userv1.ShopStatus_SHOP_STATUS_PENDING:
		return ShopStatusPending
	case userv1.ShopStatus_SHOP_STATUS_ACTIVE:
		return ShopStatusActive
	case userv1.ShopStatus_SHOP_STATUS_REJECTED:
		return ShopStatusRejected
	case userv1.ShopStatus_SHOP_STATUS_SUSPENDED:
		return ShopStatusSuspended
	default:
		return ""
	}
}

func toProtoUser(u db.User) *userv1.User {
	var shopID string
	if u.ShopID != nil {
		shopID = *u.ShopID
	}
	return &userv1.User{
		Id:               u.ID,
		FullName:         u.FullName,
		Email:            u.Email,
		Phone:            u.Phone,
		Role:             roleToProto(u.Role),
		Status:           statusToProto(u.Status),
		ShopId:           shopID,
		PhoneVerified:    u.PhoneVerified,
		CodRejectedCount: u.CODRejectedCount,
		AvatarUrl:        u.AvatarURL,
		CreatedAt:        timestamppb.New(u.CreatedAt),
		UpdatedAt:        timestamppb.New(u.UpdatedAt),
	}
}

func toProtoShop(s db.Shop) *userv1.Shop {
	return &userv1.Shop{
		Id:                 s.ID,
		OwnerUserId:        s.OwnerUserID,
		Name:               s.Name,
		Description:        s.Description,
		PickupAddressId:    s.PickupAddressID,
		ContactPhone:       s.ContactPhone,
		BusinessLicenseDoc: s.BusinessLicenseDoc,
		Status:             shopStatusToProto(s.Status),
		RejectionReason:    s.RejectionReason,
		CreatedAt:          timestamppb.New(s.CreatedAt),
		UpdatedAt:          timestamppb.New(s.UpdatedAt),
	}
}

func toProtoAddress(a db.Address) *userv1.Address {
	return &userv1.Address{
		Id:                 a.ID,
		UserId:             a.UserID,
		RecipientName:      a.RecipientName,
		RecipientPhone:     a.RecipientPhone,
		ProvinceCode:       a.ProvinceCode,
		ProvinceName:       a.ProvinceName,
		WardCode:           a.WardCode,
		WardName:           a.WardName,
		StreetAddress:      a.StreetAddress,
		LegacyProvinceCode: a.LegacyProvinceCode,
		LegacyDistrictCode: a.LegacyDistrictCode,
		LegacyWardCode:     a.LegacyWardCode,
		IsDefault:          a.IsDefault,
		CreatedAt:          timestamppb.New(a.CreatedAt),
		UpdatedAt:          timestamppb.New(a.UpdatedAt),
	}
}

func toProtoAuditLog(a db.AuditLog) *userv1.AuditLog {
	return &userv1.AuditLog{
		Id:         a.ID,
		ActorId:    a.ActorID,
		ActorRole:  a.ActorRole,
		Action:     a.Action,
		TargetType: a.TargetType,
		TargetId:   a.TargetID,
		BeforeJson: a.BeforeJSON,
		AfterJson:  a.AfterJSON,
		Reason:     a.Reason,
		IpAddress:  a.IPAddress,
		CreatedAt:  timestamppb.New(a.CreatedAt),
	}
}

func auditUserSnapshot(u db.User) map[string]any {
	var shopID string
	if u.ShopID != nil {
		shopID = *u.ShopID
	}
	return map[string]any{
		"id":      u.ID,
		"email":   u.Email,
		"role":    u.Role,
		"status":  u.Status,
		"shop_id": shopID,
	}
}

func auditShopSnapshot(s db.Shop) map[string]any {
	return map[string]any{
		"id":               s.ID,
		"owner_user_id":    s.OwnerUserID,
		"name":             s.Name,
		"status":           s.Status,
		"rejection_reason": s.RejectionReason,
	}
}
