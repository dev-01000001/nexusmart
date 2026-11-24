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
	"net"
	"net/netip"
	"regexp"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/user/internal/db"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	RoleBuyer      = auth.RoleBuyer
	RoleSeller     = auth.RoleSeller
	RoleModerator  = auth.RoleModerator
	RoleCSKH       = auth.RoleCSKH
	RoleAccountant = auth.RoleAccountant
	RoleOps        = auth.RoleOps
	RoleAdmin      = auth.RoleAdmin
	RoleSuperAdmin = auth.RoleSuperAdmin

	StatusPendingVerification = "PENDING_VERIFICATION"
	StatusActive              = "ACTIVE"
	StatusLocked              = "LOCKED"

	ShopStatusPending   = "PENDING"
	ShopStatusActive    = "ACTIVE"
	ShopStatusRejected  = "REJECTED"
	ShopStatusSuspended = "SUSPENDED"

	ApprovalStatusPending  = "PENDING"
	ApprovalStatusApproved = "APPROVED"
	ApprovalStatusRejected = "REJECTED"

	mdClientIP = "x-client-ip"
)

var vnPhoneRegexp = regexp.MustCompile(`^(?:0|\+?84)(?:[35789]\d{8}|2\d{9})$`)

func normalizeVNPhone(raw string) string {
	s := strings.TrimSpace(raw)
	return strings.NewReplacer(" ", "", "-", "", ".", "").Replace(s)
}

func isValidVNPhone(phone string) bool {
	return vnPhoneRegexp.MatchString(phone)
}

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

func isCheckViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23514"
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

func normalizeRole(role string) string {
	return strings.ToLower(strings.TrimSpace(role))
}

func isValidRole(role string) bool {
	switch normalizeRole(role) {
	case RoleBuyer, RoleSeller, RoleModerator, RoleCSKH, RoleAccountant, RoleOps, RoleAdmin, RoleSuperAdmin:
		return true
	default:
		return false
	}
}

func isValidStaffCreatableRole(role string) bool {
	switch normalizeRole(role) {
	case RoleModerator, RoleCSKH, RoleAccountant, RoleOps, RoleAdmin:
		return true
	default:
		return false
	}
}

// isScopedAuditViewerRole returns true for specialist roles whose ListAuditLogs access
// is restricted to their own actor_id (w3b-design-a7 §2.1 †, contracts.md §4.11).
func isScopedAuditViewerRole(role string) bool {
	switch normalizeRole(role) {
	case RoleModerator, RoleCSKH, RoleAccountant:
		return true
	default:
		return false
	}
}

// isFullAdminRank returns true for general admin tiers (admin, super_admin) allowed to
// override self-resource checks on non-admin user/address endpoints.
func isFullAdminRank(claims *auth.Claims) bool {
	if claims == nil || claims.Service != "" {
		return false
	}
	return auth.RoleRank(claims.Role) >= auth.RoleRank(auth.RoleAdmin)
}

// maskIP partially masks IPv4 (last octet -> .xxx) and IPv6 (keeps /48 prefix + ::xxxx)
// for privacy-preserving activity logging (F13).
func maskIP(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil {
		return "masked"
	}
	addr = addr.Unmap()
	if addr.Is4() {
		b := addr.As4()
		return fmt.Sprintf("%d.%d.%d.xxx", b[0], b[1], b[2])
	}
	b := addr.As16()
	g0 := uint16(b[0])<<8 | uint16(b[1])
	g1 := uint16(b[2])<<8 | uint16(b[3])
	g2 := uint16(b[4])<<8 | uint16(b[5])
	return fmt.Sprintf("%x:%x:%x::xxxx", g0, g1, g2)
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
	p, ok := peer.FromContext(ctx)
	if !ok || p.Addr == nil {
		return ""
	}
	raw := p.Addr.String()
	if host, _, err := net.SplitHostPort(raw); err == nil {
		return host
	}
	return raw
}

// isTrustedAuditIPCaller reports whether the caller is trusted to supply client IP metadata
// (either authenticated via internal service token or calling over the internal network/gateway).
func isTrustedAuditIPCaller(ctx context.Context, claims *auth.Claims) bool {
	if claims != nil && claims.Service != "" {
		return true
	}
	host := peerIPFromContext(ctx)
	if host == "" {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate())
}

func auditIPFromContext(ctx context.Context, claims *auth.Claims, explicitIP string) string {
	if isTrustedAuditIPCaller(ctx, claims) {
		if ip := strings.TrimSpace(explicitIP); ip != "" {
			return ip
		}
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			if vals := md.Get(mdClientIP); len(vals) > 0 {
				if ip := strings.TrimSpace(vals[0]); ip != "" {
					return ip
				}
			}
		}
	}
	return peerIPFromContext(ctx)
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
	case RoleModerator:
		return userv1.UserRole_USER_ROLE_MODERATOR
	case RoleCSKH:
		return userv1.UserRole_USER_ROLE_CSKH
	case RoleAccountant:
		return userv1.UserRole_USER_ROLE_ACCOUNTANT
	case RoleOps:
		return userv1.UserRole_USER_ROLE_OPS
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
	case userv1.UserRole_USER_ROLE_MODERATOR:
		return RoleModerator
	case userv1.UserRole_USER_ROLE_CSKH:
		return RoleCSKH
	case userv1.UserRole_USER_ROLE_ACCOUNTANT:
		return RoleAccountant
	case userv1.UserRole_USER_ROLE_OPS:
		return RoleOps
	default:
		return ""
	}
}

func shopApprovalStatusToProto(st string) userv1.ShopApprovalStatus {
	switch strings.ToUpper(strings.TrimSpace(st)) {
	case ApprovalStatusPending:
		return userv1.ShopApprovalStatus_SHOP_APPROVAL_STATUS_PENDING
	case ApprovalStatusApproved:
		return userv1.ShopApprovalStatus_SHOP_APPROVAL_STATUS_APPROVED
	case ApprovalStatusRejected:
		return userv1.ShopApprovalStatus_SHOP_APPROVAL_STATUS_REJECTED
	default:
		return userv1.ShopApprovalStatus_SHOP_APPROVAL_STATUS_UNSPECIFIED
	}
}

func shopApprovalStatusFromProto(st userv1.ShopApprovalStatus) string {
	switch st {
	case userv1.ShopApprovalStatus_SHOP_APPROVAL_STATUS_PENDING:
		return ApprovalStatusPending
	case userv1.ShopApprovalStatus_SHOP_APPROVAL_STATUS_APPROVED:
		return ApprovalStatusApproved
	case userv1.ShopApprovalStatus_SHOP_APPROVAL_STATUS_REJECTED:
		return ApprovalStatusRejected
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
		LockReason:       u.LockReason,
	}
}

func toProtoShop(s db.Shop) *userv1.Shop {
	out := &userv1.Shop{
		Id:                 s.ID,
		OwnerUserId:        s.OwnerUserID,
		Name:               s.Name,
		Slug:               s.Slug,
		Description:        s.Description,
		PickupAddressId:    s.PickupAddressID,
		ContactPhone:       s.ContactPhone,
		BusinessLicenseDoc: s.BusinessLicenseDoc,
		Status:             shopStatusToProto(s.Status),
		RejectionReason:    s.RejectionReason,
		CreatedAt:          timestamppb.New(s.CreatedAt),
		UpdatedAt:          timestamppb.New(s.UpdatedAt),
		SuspendedReason:    s.SuspendedReason,
		StatusVersion:      s.StatusVersion,
	}
	if s.SuspendedAt != nil {
		out.SuspendedAt = timestamppb.New(*s.SuspendedAt)
	}
	return out
}

func toProtoUserActivityEntry(e db.UserActivityLog) *userv1.UserActivityEntry {
	return &userv1.UserActivityEntry{
		Id:           e.ID,
		UserId:       e.UserID,
		EventType:    e.EventType,
		IpAddress:    e.IPAddress,
		MetadataJson: e.MetadataJSON,
		CreatedAt:    timestamppb.New(e.CreatedAt),
	}
}

func toProtoShopSuspensionApproval(a db.ShopSuspensionApproval, shopName string) *userv1.ShopSuspensionApproval {
	out := &userv1.ShopSuspensionApproval{
		Id:            a.ID,
		ShopId:        a.ShopID,
		ShopName:      shopName,
		Status:        shopApprovalStatusToProto(a.Status),
		Reason:        a.Reason,
		RequestedBy:   a.RequestedBy,
		RequestedRole: a.RequestedRole,
		DecidedNote:   a.DecidedNote,
		CreatedAt:     timestamppb.New(a.CreatedAt),
	}
	if a.DecidedBy != nil {
		out.DecidedBy = *a.DecidedBy
	}
	if a.DecidedAt != nil {
		out.DecidedAt = timestamppb.New(*a.DecidedAt)
	}
	return out
}

func toProtoShopSuspensionApprovalFromRow(r db.ListShopSuspensionApprovalsRow) *userv1.ShopSuspensionApproval {
	out := &userv1.ShopSuspensionApproval{
		Id:            r.ID,
		ShopId:        r.ShopID,
		ShopName:      r.ShopName,
		Status:        shopApprovalStatusToProto(r.Status),
		Reason:        r.Reason,
		RequestedBy:   r.RequestedBy,
		RequestedRole: r.RequestedRole,
		DecidedNote:   r.DecidedNote,
		CreatedAt:     timestamppb.New(r.CreatedAt),
	}
	if r.DecidedBy != nil {
		out.DecidedBy = *r.DecidedBy
	}
	if r.DecidedAt != nil {
		out.DecidedAt = timestamppb.New(*r.DecidedAt)
	}
	return out
}

func toAdminProtoShop(s db.Shop, bankReviewPending bool) *userv1.Shop {
	out := toProtoShop(s)
	out.BankReviewPending = bankReviewPending
	return out
}

func shopFromAdminListRow(r db.AdminListShopsRow) db.Shop {
	return db.Shop{
		ID:                 r.ID,
		OwnerUserID:        r.OwnerUserID,
		Name:               r.Name,
		Description:        r.Description,
		PickupAddressID:    r.PickupAddressID,
		ContactPhone:       r.ContactPhone,
		BusinessLicenseDoc: r.BusinessLicenseDoc,
		Status:             r.Status,
		RejectionReason:    r.RejectionReason,
		CreatedAt:          r.CreatedAt,
		UpdatedAt:          r.UpdatedAt,
		Slug:               r.Slug,
		SuspendedAt:        r.SuspendedAt,
		SuspendedReason:    r.SuspendedReason,
		StatusVersion:      r.StatusVersion,
	}
}

// toPublicProtoShop strips private owner/contact/license fields for unauthenticated storefront listings.
func toPublicProtoShop(s db.Shop) *userv1.Shop {
	return &userv1.Shop{
		Id:          s.ID,
		Name:        s.Name,
		Slug:        s.Slug,
		Description: s.Description,
		Status:      shopStatusToProto(s.Status),
		CreatedAt:   timestamppb.New(s.CreatedAt),
		UpdatedAt:   timestamppb.New(s.UpdatedAt),
	}
}

func slugifyShop(name, fallback string) string {
	s := normalizeShopSlug(name)
	if s == "" {
		return fallback
	}
	return s
}

func normalizeShopSlug(input string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(input)) {
		r = foldVietnameseRune(r)
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
		} else if unicode.IsSpace(r) || r == '-' || r == '_' || r == '/' || r == '.' {
			if b.Len() > 0 && !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func foldVietnameseRune(r rune) rune {
	switch r {
	case 'á', 'à', 'ả', 'ã', 'ạ', 'ă', 'ắ', 'ằ', 'ẳ', 'ẵ', 'ặ', 'â', 'ấ', 'ầ', 'ẩ', 'ẫ', 'ậ':
		return 'a'
	case 'đ':
		return 'd'
	case 'é', 'è', 'ẻ', 'ẽ', 'ẹ', 'ê', 'ế', 'ề', 'ể', 'ễ', 'ệ':
		return 'e'
	case 'í', 'ì', 'ỉ', 'ĩ', 'ị':
		return 'i'
	case 'ó', 'ò', 'ỏ', 'õ', 'ọ', 'ô', 'ố', 'ồ', 'ổ', 'ỗ', 'ộ', 'ơ', 'ớ', 'ờ', 'ở', 'ỡ', 'ợ':
		return 'o'
	case 'ú', 'ù', 'ủ', 'ũ', 'ụ', 'ư', 'ứ', 'ừ', 'ử', 'ữ', 'ự':
		return 'u'
	case 'ý', 'ỳ', 'ỷ', 'ỹ', 'ỵ':
		return 'y'
	default:
		return r
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
		"id":          u.ID,
		"email":       u.Email,
		"role":        u.Role,
		"status":      u.Status,
		"shop_id":     shopID,
		"lock_reason": u.LockReason,
	}
}

func effectiveSellerShopID(u db.User) string {
	if u.Role == RoleSeller && u.ShopID != nil {
		return *u.ShopID
	}
	return ""
}

func lockedAccountMessage(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason != "" {
		return fmt.Sprintf("Tài khoản đã bị khóa. Lý do: %s. Vui lòng liên hệ hỗ trợ support@nexusmart.vn để được giải quyết.", reason)
	}
	return "Tài khoản đã bị khóa. Vui lòng liên hệ hỗ trợ support@nexusmart.vn để được giải quyết."
}

func auditShopSnapshot(s db.Shop) map[string]any {
	out := map[string]any{
		"id":                  s.ID,
		"owner_user_id":       s.OwnerUserID,
		"name":                s.Name,
		"status":              s.Status,
		"status_version":      s.StatusVersion,
		"rejection_reason":    s.RejectionReason,
		"suspended_reason":    s.SuspendedReason,
		"verified_owner_name": s.VerifiedOwnerName,
	}
	if s.SuspendedAt != nil {
		out["suspended_at"] = s.SuspendedAt.UTC().Format("2006-01-02T15:04:05Z07:00")
	}
	return out
}
