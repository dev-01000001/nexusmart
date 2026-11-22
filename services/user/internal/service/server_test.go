package service

import (
	"context"
	"crypto/subtle"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/user/internal/db"
	"github.com/nexusmart/core/services/user/internal/events"
	"github.com/nexusmart/core/services/user/internal/password"
	"github.com/nexusmart/core/services/user/internal/security"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// --- In-memory test doubles ---

type memStore struct {
	mu                   sync.Mutex
	users                map[string]db.User
	refreshTokens        map[string]db.RefreshToken
	shops                map[string]db.Shop
	addresses            map[string]db.Address
	auditLogs            []db.AuditLog
	forceReplaceZeroRows bool
}

func newMemStore() *memStore {
	return &memStore{
		users:         make(map[string]db.User),
		refreshTokens: make(map[string]db.RefreshToken),
		shops:         make(map[string]db.Shop),
		addresses:     make(map[string]db.Address),
	}
}

func (m *memStore) Ping(_ context.Context) error { return nil }

func (m *memStore) WithTx(_ context.Context, fn func(q db.Querier) error) error {
	// Snapshot refreshTokens so any error returned by fn genuinely rolls back TX mutations.
	m.mu.Lock()
	snapshot := make(map[string]db.RefreshToken, len(m.refreshTokens))
	for k, v := range m.refreshTokens {
		snapshot[k] = v
	}
	m.mu.Unlock()

	if err := fn(m); err != nil {
		m.mu.Lock()
		m.refreshTokens = snapshot
		m.mu.Unlock()
		return err
	}
	return nil
}

func (m *memStore) CreateUser(_ context.Context, arg db.CreateUserParams) (db.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	u := db.User{
		ID:            arg.ID,
		FullName:      arg.FullName,
		Email:         arg.Email,
		Phone:         arg.Phone,
		PasswordHash:  arg.PasswordHash,
		Role:          arg.Role,
		Status:        arg.Status,
		PhoneVerified: arg.PhoneVerified,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	m.users[u.ID] = u
	return u, nil
}

func (m *memStore) GetUserByID(_ context.Context, id string) (db.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return db.User{}, pgx.ErrNoRows
	}
	return u, nil
}

func (m *memStore) GetUserByEmail(_ context.Context, email string) (db.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if strings.EqualFold(u.Email, email) {
			return u, nil
		}
	}
	return db.User{}, pgx.ErrNoRows
}

func (m *memStore) GetUserByPhone(_ context.Context, phone string) (db.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if phone != "" && u.Phone == phone {
			return u, nil
		}
	}
	return db.User{}, pgx.ErrNoRows
}

func (m *memStore) ActivateUser(_ context.Context, arg db.ActivateUserParams) (db.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[arg.ID]
	if !ok {
		return db.User{}, pgx.ErrNoRows
	}
	u.Status = StatusActive
	u.PhoneVerified = arg.PhoneVerified
	u.UpdatedAt = time.Now().UTC()
	m.users[u.ID] = u
	return u, nil
}

func (m *memStore) UpdateUserProfile(_ context.Context, arg db.UpdateUserProfileParams) (db.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[arg.ID]
	if !ok {
		return db.User{}, pgx.ErrNoRows
	}
	u.FullName = arg.FullName
	u.Phone = arg.Phone
	u.AvatarURL = arg.AvatarURL
	u.UpdatedAt = time.Now().UTC()
	m.users[u.ID] = u
	return u, nil
}

func (m *memStore) UpdateUserStatus(_ context.Context, arg db.UpdateUserStatusParams) (db.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[arg.ID]
	if !ok {
		return db.User{}, pgx.ErrNoRows
	}
	u.Status = arg.Status
	u.UpdatedAt = time.Now().UTC()
	m.users[u.ID] = u
	return u, nil
}

func (m *memStore) UpdateUserRole(_ context.Context, arg db.UpdateUserRoleParams) (db.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[arg.ID]
	if !ok {
		return db.User{}, pgx.ErrNoRows
	}
	u.Role = arg.Role
	u.UpdatedAt = time.Now().UTC()
	m.users[u.ID] = u
	return u, nil
}

func (m *memStore) GrantUserSellerShop(_ context.Context, arg db.GrantUserSellerShopParams) (db.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[arg.ID]
	if !ok {
		return db.User{}, pgx.ErrNoRows
	}
	if u.Role == RoleBuyer {
		u.Role = RoleSeller
	}
	u.ShopID = arg.ShopID
	u.UpdatedAt = time.Now().UTC()
	m.users[u.ID] = u
	return u, nil
}

func (m *memStore) CountActiveAdmins(_ context.Context) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for _, u := range m.users {
		if isAdminRole(u.Role) && u.Status == StatusActive {
			n++
		}
	}
	return n, nil
}

func (m *memStore) ListUsers(_ context.Context, arg db.ListUsersParams) ([]db.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var res []db.User
	for _, u := range m.users {
		if arg.Role != "" && u.Role != arg.Role {
			continue
		}
		if arg.Status != "" && u.Status != arg.Status {
			continue
		}
		if arg.Query != "" {
			q := strings.ToLower(arg.Query)
			if !strings.Contains(strings.ToLower(u.FullName), q) &&
				!strings.Contains(strings.ToLower(u.Email), q) &&
				!strings.Contains(strings.ToLower(u.Phone), q) {
				continue
			}
		}
		res = append(res, u)
	}
	return res, nil
}

func (m *memStore) CountUsers(ctx context.Context, arg db.CountUsersParams) (int64, error) {
	list, err := m.ListUsers(ctx, db.ListUsersParams{Query: arg.Query, Role: arg.Role, Status: arg.Status})
	return int64(len(list)), err
}

func (m *memStore) CreateRefreshToken(_ context.Context, arg db.CreateRefreshTokenParams) (db.RefreshToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rt := db.RefreshToken{
		ID:        arg.ID,
		UserID:    arg.UserID,
		FamilyID:  arg.FamilyID,
		TokenHash: arg.TokenHash,
		ExpiresAt: arg.ExpiresAt,
		CreatedAt: time.Now().UTC(),
	}
	m.refreshTokens[rt.ID] = rt
	return rt, nil
}

func (m *memStore) GetRefreshTokenByHash(_ context.Context, tokenHash string) (db.RefreshToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, rt := range m.refreshTokens {
		if rt.TokenHash == tokenHash {
			return rt, nil
		}
	}
	return db.RefreshToken{}, pgx.ErrNoRows
}

func (m *memStore) RevokeRefreshTokenWithReplacement(_ context.Context, arg db.RevokeRefreshTokenWithReplacementParams) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.forceReplaceZeroRows {
		return 0, nil
	}
	rt, ok := m.refreshTokens[arg.ID]
	if !ok || rt.RevokedAt != nil {
		return 0, nil
	}
	now := time.Now().UTC()
	rt.RevokedAt = &now
	rt.ReplacedByID = arg.ReplacedByID
	m.refreshTokens[rt.ID] = rt
	return 1, nil
}

func (m *memStore) RevokeRefreshTokenByHash(_ context.Context, tokenHash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	for id, rt := range m.refreshTokens {
		if rt.TokenHash == tokenHash && rt.RevokedAt == nil {
			rt.RevokedAt = &now
			m.refreshTokens[id] = rt
		}
	}
	return nil
}

func (m *memStore) RevokeRefreshTokenFamily(_ context.Context, familyID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	for id, rt := range m.refreshTokens {
		if rt.FamilyID == familyID && rt.RevokedAt == nil {
			rt.RevokedAt = &now
			m.refreshTokens[id] = rt
		}
	}
	return nil
}

func (m *memStore) RevokeAllUserRefreshTokens(_ context.Context, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	for id, rt := range m.refreshTokens {
		if rt.UserID == userID && rt.RevokedAt == nil {
			rt.RevokedAt = &now
			m.refreshTokens[id] = rt
		}
	}
	return nil
}

func (m *memStore) CreateShop(_ context.Context, arg db.CreateShopParams) (db.Shop, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	sh := db.Shop{
		ID:                 arg.ID,
		OwnerUserID:        arg.OwnerUserID,
		Name:               arg.Name,
		Slug:               arg.Slug,
		Description:        arg.Description,
		PickupAddressID:    arg.PickupAddressID,
		ContactPhone:       arg.ContactPhone,
		BusinessLicenseDoc: arg.BusinessLicenseDoc,
		Status:             arg.Status,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	m.shops[sh.ID] = sh
	return sh, nil
}

func (m *memStore) GetShopByID(_ context.Context, id string) (db.Shop, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sh, ok := m.shops[id]
	if !ok {
		return db.Shop{}, pgx.ErrNoRows
	}
	return sh, nil
}

func (m *memStore) GetShopBySlug(_ context.Context, slug string) (db.Shop, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, sh := range m.shops {
		if sh.Slug != "" && sh.Slug == slug {
			return sh, nil
		}
	}
	return db.Shop{}, pgx.ErrNoRows
}

func (m *memStore) GetShopByOwnerID(_ context.Context, ownerID string) (db.Shop, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, sh := range m.shops {
		if sh.OwnerUserID == ownerID {
			return sh, nil
		}
	}
	return db.Shop{}, pgx.ErrNoRows
}

func (m *memStore) GetShopByName(_ context.Context, name string) (db.Shop, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, sh := range m.shops {
		if strings.EqualFold(sh.Name, name) {
			return sh, nil
		}
	}
	return db.Shop{}, pgx.ErrNoRows
}

func (m *memStore) ListShops(_ context.Context, arg db.ListShopsParams) ([]db.Shop, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var res []db.Shop
	for _, sh := range m.shops {
		if arg.Status != "" && sh.Status != arg.Status {
			continue
		}
		res = append(res, sh)
	}
	return res, nil
}

func (m *memStore) CountShops(ctx context.Context, st string) (int64, error) {
	list, err := m.ListShops(ctx, db.ListShopsParams{Status: st})
	return int64(len(list)), err
}

func (m *memStore) UpdateShopReview(_ context.Context, arg db.UpdateShopReviewParams) (db.Shop, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sh, ok := m.shops[arg.ID]
	if !ok {
		return db.Shop{}, pgx.ErrNoRows
	}
	sh.Status = arg.Status
	sh.RejectionReason = arg.RejectionReason
	sh.UpdatedAt = time.Now().UTC()
	m.shops[sh.ID] = sh
	return sh, nil
}

func (m *memStore) CreateAddress(_ context.Context, arg db.CreateAddressParams) (db.Address, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	a := db.Address{
		ID:                 arg.ID,
		UserID:             arg.UserID,
		RecipientName:      arg.RecipientName,
		RecipientPhone:     arg.RecipientPhone,
		ProvinceCode:       arg.ProvinceCode,
		ProvinceName:       arg.ProvinceName,
		WardCode:           arg.WardCode,
		WardName:           arg.WardName,
		StreetAddress:      arg.StreetAddress,
		LegacyProvinceCode: arg.LegacyProvinceCode,
		LegacyDistrictCode: arg.LegacyDistrictCode,
		LegacyWardCode:     arg.LegacyWardCode,
		IsDefault:          arg.IsDefault,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	m.addresses[a.ID] = a
	return a, nil
}

func (m *memStore) ClearUserDefaultAddresses(_ context.Context, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, a := range m.addresses {
		if a.UserID == userID && a.IsDefault {
			a.IsDefault = false
			m.addresses[id] = a
		}
	}
	return nil
}

func (m *memStore) ListAddressesByUserID(_ context.Context, userID string) ([]db.Address, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var res []db.Address
	for _, a := range m.addresses {
		if a.UserID == userID {
			res = append(res, a)
		}
	}
	return res, nil
}

func (m *memStore) GetAddressByID(_ context.Context, id string) (db.Address, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.addresses[id]
	if !ok {
		return db.Address{}, pgx.ErrNoRows
	}
	return a, nil
}

func (m *memStore) UpdateAddress(_ context.Context, arg db.UpdateAddressParams) (db.Address, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.addresses[arg.ID]
	if !ok {
		return db.Address{}, pgx.ErrNoRows
	}
	a.RecipientName = arg.RecipientName
	a.RecipientPhone = arg.RecipientPhone
	a.ProvinceCode = arg.ProvinceCode
	a.ProvinceName = arg.ProvinceName
	a.WardCode = arg.WardCode
	a.WardName = arg.WardName
	a.StreetAddress = arg.StreetAddress
	a.IsDefault = arg.IsDefault
	a.UpdatedAt = time.Now().UTC()
	m.addresses[a.ID] = a
	return a, nil
}

func (m *memStore) DeleteAddress(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.addresses, id)
	return nil
}

func (m *memStore) InsertAuditLog(_ context.Context, arg db.InsertAuditLogParams) (db.AuditLog, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := db.AuditLog{
		ID:         arg.ID,
		ActorID:    arg.ActorID,
		ActorRole:  arg.ActorRole,
		Action:     arg.Action,
		TargetType: arg.TargetType,
		TargetID:   arg.TargetID,
		BeforeJSON: arg.BeforeJSON,
		AfterJSON:  arg.AfterJSON,
		Reason:     arg.Reason,
		IPAddress:  arg.IPAddress,
		CreatedAt:  time.Now().UTC(),
	}
	m.auditLogs = append(m.auditLogs, l)
	return l, nil
}

func (m *memStore) ListAuditLogs(_ context.Context, arg db.ListAuditLogsParams) ([]db.AuditLog, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var res []db.AuditLog
	for _, l := range m.auditLogs {
		if arg.ActorID != "" && l.ActorID != arg.ActorID {
			continue
		}
		if arg.TargetType != "" && l.TargetType != arg.TargetType {
			continue
		}
		if arg.TargetID != "" && l.TargetID != arg.TargetID {
			continue
		}
		res = append(res, l)
	}
	return res, nil
}

func (m *memStore) CountAuditLogs(ctx context.Context, arg db.CountAuditLogsParams) (int64, error) {
	list, err := m.ListAuditLogs(ctx, db.ListAuditLogsParams{
		ActorID:    arg.ActorID,
		TargetType: arg.TargetType,
		TargetID:   arg.TargetID,
	})
	return int64(len(list)), err
}

type memOTP struct {
	code      string
	attempts  int
	expiresAt time.Time
}

type memSecurity struct {
	mu          sync.Mutex
	otps        map[string]memOTP
	resends     map[string]int
	loginFails  map[string]int
	loginLocks  map[string]time.Time
	revokedJTIs map[string]bool
}

func newMemSecurity() *memSecurity {
	return &memSecurity{
		otps:        make(map[string]memOTP),
		resends:     make(map[string]int),
		loginFails:  make(map[string]int),
		loginLocks:  make(map[string]time.Time),
		revokedJTIs: make(map[string]bool),
	}
}

func (s *memSecurity) SaveOTP(_ context.Context, email, code string, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.otps[normalizeEmail(email)] = memOTP{code: code, expiresAt: time.Now().UTC().Add(ttl)}
	return nil
}

func (s *memSecurity) VerifyOTP(_ context.Context, email, code string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := normalizeEmail(email)
	rec, ok := s.otps[k]
	if !ok || time.Now().UTC().After(rec.expiresAt) {
		delete(s.otps, k)
		return security.ErrOTPNotFound
	}
	if subtle.ConstantTimeCompare([]byte(rec.code), []byte(code)) == 1 {
		delete(s.otps, k)
		delete(s.resends, k)
		return nil
	}
	rec.attempts++
	if rec.attempts >= 5 {
		delete(s.otps, k)
		return security.ErrOTPTooManyAttempts
	}
	s.otps[k] = rec
	return security.ErrOTPInvalid
}

func (s *memSecurity) IncrementOTPResend(_ context.Context, email string, maxResends int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := normalizeEmail(email)
	s.resends[k]++
	if s.resends[k] > maxResends {
		return security.ErrOTPResendLimitReached
	}
	return nil
}

func (s *memSecurity) IsLoginLocked(_ context.Context, email string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.loginLocks[normalizeEmail(email)]
	return ok && time.Now().UTC().Before(exp), nil
}

func (s *memSecurity) RecordLoginFailure(_ context.Context, email string, maxAttempts int, lockout time.Duration) (bool, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := normalizeEmail(email)
	s.loginFails[k]++
	if s.loginFails[k] >= maxAttempts {
		s.loginLocks[k] = time.Now().UTC().Add(lockout)
		delete(s.loginFails, k)
		return true, maxAttempts, nil
	}
	return false, s.loginFails[k], nil
}

func (s *memSecurity) ClearLoginFailures(_ context.Context, email string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := normalizeEmail(email)
	delete(s.loginFails, k)
	delete(s.loginLocks, k)
	return nil
}

func (s *memSecurity) RevokeJTI(_ context.Context, jti string, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revokedJTIs[jti] = true
	return nil
}

func (s *memSecurity) IsJTIRevoked(_ context.Context, jti string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.revokedJTIs[jti], nil
}

func (s *memSecurity) Ping(_ context.Context) error { return nil }

type memPublisher struct {
	mu             sync.Mutex
	userRegistered []events.UserRegisteredPayload
	shopReviewed   []events.ShopReviewedPayload
}

func (p *memPublisher) PublishUserRegistered(_ context.Context, payload events.UserRegisteredPayload) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.userRegistered = append(p.userRegistered, payload)
	return nil
}

func (p *memPublisher) PublishShopReviewed(_ context.Context, payload events.ShopReviewedPayload) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.shopReviewed = append(p.shopReviewed, payload)
	return nil
}

func newTestHarness(t *testing.T) (*Server, *memStore, *memSecurity, *memPublisher, *auth.Manager) {
	t.Helper()
	store := newMemStore()
	sec := newMemSecurity()
	pub := &memPublisher{}
	hasher := password.NewDefaultHasher()
	authMgr, err := auth.NewManager("test-secret-key-hs256", 15*time.Minute)
	if err != nil {
		t.Fatalf("new auth manager: %v", err)
	}
	log := logger.New("user-test", logger.WithWriter(io.Discard))
	srv := NewServer(store, sec, hasher, authMgr, pub, log, Options{})
	return srv, store, sec, pub, authMgr
}

func ctxWithUser(userID, role, shopID string) context.Context {
	claims := &auth.Claims{Role: role, ShopID: shopID}
	claims.Subject = userID
	claims.ID = uuid.NewString()
	return auth.WithClaims(context.Background(), claims)
}

// --- Tests ---

func TestBootstrapAdminIdempotent(t *testing.T) {
	srv, store, _, _, _ := newTestHarness(t)
	hasher := password.NewDefaultHasher()
	log := logger.New("user-test", logger.WithWriter(io.Discard))

	if err := BootstrapAdmin(context.Background(), store, hasher, log, "admin@nexusmart.local", "AdminPass123!"); err != nil {
		t.Fatalf("first bootstrap failed: %v", err)
	}
	if err := BootstrapAdmin(context.Background(), store, hasher, log, "admin@nexusmart.local", "AdminPass123!"); err != nil {
		t.Fatalf("second bootstrap failed: %v", err)
	}

	resp, err := srv.Login(context.Background(), &userv1.LoginRequest{
		Email:    "admin@nexusmart.local",
		Password: "AdminPass123!",
	})
	if err != nil {
		t.Fatalf("bootstrap admin login failed: %v", err)
	}
	if resp.GetUser().GetRole() != userv1.UserRole_USER_ROLE_SUPER_ADMIN {
		t.Fatalf("expected super_admin role, got %v", resp.GetUser().GetRole())
	}
}

func TestRegisterVerifyResendOTPAndLogin(t *testing.T) {
	srv, _, _, pub, authMgr := newTestHarness(t)
	ctx := context.Background()

	regResp, err := srv.Register(ctx, &userv1.RegisterRequest{
		FullName: "Nguyen Van A",
		Email:    "buyer@example.com",
		Phone:    "0901234567",
		Password: "Password123!",
	})
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	if regResp.GetStatus() != StatusPendingVerification {
		t.Fatalf("expected PENDING_VERIFICATION, got %s", regResp.GetStatus())
	}
	if len(pub.userRegistered) != 1 || len(pub.userRegistered[0].OTP) != 6 {
		t.Fatalf("expected 6-digit OTP published, got %+v", pub.userRegistered)
	}

	// Duplicate email rejected.
	if _, err := srv.Register(ctx, &userv1.RegisterRequest{
		FullName: "Other",
		Email:    "BUYER@example.com",
		Phone:    "0909999999",
		Password: "Password123!",
	}); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("expected AlreadyExists on duplicate email, got %v", err)
	}

	// Login before OTP verification fails.
	if _, err := srv.Login(ctx, &userv1.LoginRequest{
		Email:    "buyer@example.com",
		Password: "Password123!",
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition before verification, got %v", err)
	}

	// Resend OTP allowed up to 3 times; 4th hits limit.
	for i := 0; i < 3; i++ {
		if _, err := srv.ResendOTP(ctx, &userv1.ResendOTPRequest{Email: "buyer@example.com"}); err != nil {
			t.Fatalf("ResendOTP #%d failed: %v", i+1, err)
		}
	}
	if _, err := srv.ResendOTP(ctx, &userv1.ResendOTPRequest{Email: "buyer@example.com"}); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("expected ResourceExhausted on 4th resend, got %v", err)
	}

	latestOTP := pub.userRegistered[len(pub.userRegistered)-1].OTP
	verifyResp, err := srv.VerifyOTP(ctx, &userv1.VerifyOTPRequest{
		Email: "buyer@example.com",
		Otp:   latestOTP,
	})
	if err != nil || !verifyResp.GetVerified() {
		t.Fatalf("VerifyOTP failed: resp=%+v err=%v", verifyResp, err)
	}

	loginResp, err := srv.Login(ctx, &userv1.LoginRequest{
		Email:    "buyer@example.com",
		Password: "Password123!",
	})
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}
	if loginResp.GetExpiresInSeconds() != 900 {
		t.Fatalf("expected 900s access TTL, got %d", loginResp.GetExpiresInSeconds())
	}
	claims, err := authMgr.VerifyAccessToken(loginResp.GetAccessToken())
	if err != nil {
		t.Fatalf("VerifyAccessToken failed: %v", err)
	}
	if claims.UserID() != regResp.GetUserId() || claims.Role != RoleBuyer || claims.JTI() == "" {
		t.Fatalf("unexpected JWT claims: %+v", claims)
	}
}

func TestRefreshTokenRotationAndReuseRevokesFamily(t *testing.T) {
	srv, _, _, pub, _ := newTestHarness(t)
	ctx := context.Background()

	_, _ = srv.Register(ctx, &userv1.RegisterRequest{
		FullName: "Buyer Rotate",
		Email:    "rotate@example.com",
		Phone:    "0911111111",
		Password: "Password123!",
	})
	_, _ = srv.VerifyOTP(ctx, &userv1.VerifyOTPRequest{
		Email: "rotate@example.com",
		Otp:   pub.userRegistered[0].OTP,
	})
	loginResp, err := srv.Login(ctx, &userv1.LoginRequest{
		Email:    "rotate@example.com",
		Password: "Password123!",
	})
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}
	rt1 := loginResp.GetRefreshToken()

	// Normal rotation: rt1 -> rt2
	refResp, err := srv.RefreshToken(ctx, &userv1.RefreshTokenRequest{RefreshToken: rt1})
	if err != nil {
		t.Fatalf("first RefreshToken failed: %v", err)
	}
	rt2 := refResp.GetRefreshToken()
	if rt2 == "" || rt2 == rt1 {
		t.Fatalf("expected rotated distinct refresh token")
	}

	// Reuse old token rt1 -> MUST fail AND revoke entire family (including rt2!)
	if _, err := srv.RefreshToken(ctx, &userv1.RefreshTokenRequest{RefreshToken: rt1}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated on refresh reuse, got %v", err)
	}

	// rt2 must now also be revoked!
	if _, err := srv.RefreshToken(ctx, &userv1.RefreshTokenRequest{RefreshToken: rt2}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated on rt2 after family revocation, got %v", err)
	}
}

func TestLoginBruteForceLockout(t *testing.T) {
	srv, _, _, pub, _ := newTestHarness(t)
	ctx := context.Background()

	_, _ = srv.Register(ctx, &userv1.RegisterRequest{
		FullName: "Lockout Buyer",
		Email:    "lockout@example.com",
		Phone:    "0922222222",
		Password: "Password123!",
	})
	_, _ = srv.VerifyOTP(ctx, &userv1.VerifyOTPRequest{
		Email: "lockout@example.com",
		Otp:   pub.userRegistered[0].OTP,
	})

	for i := 1; i <= 4; i++ {
		_, err := srv.Login(ctx, &userv1.LoginRequest{
			Email:    "lockout@example.com",
			Password: "WrongPassword!",
		})
		if status.Code(err) != codes.Unauthenticated {
			t.Fatalf("attempt %d: expected Unauthenticated, got %v", i, err)
		}
	}

	// 5th failure locks account for 15m.
	_, err := srv.Login(ctx, &userv1.LoginRequest{
		Email:    "lockout@example.com",
		Password: "WrongPassword!",
	})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("5th attempt: expected ResourceExhausted lockout, got %v", err)
	}

	// Even with correct password, account stays locked.
	_, err = srv.Login(ctx, &userv1.LoginRequest{
		Email:    "lockout@example.com",
		Password: "Password123!",
	})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("expected locked account to reject correct password with ResourceExhausted, got %v", err)
	}
}

func TestAdminSelfLockAndLastAdminGuard(t *testing.T) {
	srv, store, _, pub, _ := newTestHarness(t)
	hasher := password.NewDefaultHasher()
	log := logger.New("user-test", logger.WithWriter(io.Discard))

	_ = BootstrapAdmin(context.Background(), store, hasher, log, "admin@nexusmart.local", "AdminPass123!")
	adminUser, _ := store.GetUserByEmail(context.Background(), "admin@nexusmart.local")
	adminCtx := ctxWithUser(adminUser.ID, RoleSuperAdmin, "")

	// Cannot lock self.
	if _, err := srv.AdminUpdateUserStatus(adminCtx, &userv1.AdminUpdateUserStatusRequest{
		TargetUserId: adminUser.ID,
		Status:       userv1.UserStatus_USER_STATUS_LOCKED,
		Reason:       "self test",
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition on self-lock, got %v", err)
	}

	// Cannot demote self / last active admin.
	if _, err := srv.AdminUpdateUserRole(adminCtx, &userv1.AdminUpdateUserRoleRequest{
		TargetUserId: adminUser.ID,
		Role:         userv1.UserRole_USER_ROLE_BUYER,
		Reason:       "demote self",
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition on self-demote, got %v", err)
	}

	// Register + verify + login a buyer, then admin locks buyer.
	regResp, _ := srv.Register(context.Background(), &userv1.RegisterRequest{
		FullName: "Bad Buyer",
		Email:    "bad@example.com",
		Phone:    "0933333333",
		Password: "Password123!",
	})
	_, _ = srv.VerifyOTP(context.Background(), &userv1.VerifyOTPRequest{
		Email: "bad@example.com",
		Otp:   pub.userRegistered[0].OTP,
	})
	buyerLogin, _ := srv.Login(context.Background(), &userv1.LoginRequest{
		Email:    "bad@example.com",
		Password: "Password123!",
	})

	// Non-admin cannot call AdminUpdateUserStatus.
	buyerCtx := ctxWithUser(regResp.GetUserId(), RoleBuyer, "")
	if _, err := srv.AdminUpdateUserStatus(buyerCtx, &userv1.AdminUpdateUserStatusRequest{
		TargetUserId: regResp.GetUserId(),
		Status:       userv1.UserStatus_USER_STATUS_LOCKED,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for buyer calling admin RPC, got %v", err)
	}

	// Admin locks buyer -> revokes refresh tokens + writes audit log.
	lockResp, err := srv.AdminUpdateUserStatus(adminCtx, &userv1.AdminUpdateUserStatusRequest{
		TargetUserId: regResp.GetUserId(),
		Status:       userv1.UserStatus_USER_STATUS_LOCKED,
		Reason:       "fraud",
	})
	if err != nil || lockResp.GetUser().GetStatus() != userv1.UserStatus_USER_STATUS_LOCKED {
		t.Fatalf("expected buyer locked, got resp=%+v err=%v", lockResp, err)
	}

	// Buyer's refresh token must be revoked.
	if _, err := srv.RefreshToken(context.Background(), &userv1.RefreshTokenRequest{
		RefreshToken: buyerLogin.GetRefreshToken(),
	}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected buyer refresh token revoked after lock, got %v", err)
	}

	// Audit log recorded.
	auditResp, err := srv.ListAuditLogs(adminCtx, &userv1.ListAuditLogsRequest{TargetId: regResp.GetUserId()})
	if err != nil || len(auditResp.GetLogs()) != 1 {
		t.Fatalf("expected 1 audit log entry, got %d err=%v", len(auditResp.GetLogs()), err)
	}
}

func TestShopRegisterReviewAndAddressOwnership(t *testing.T) {
	srv, store, _, pub, authMgr := newTestHarness(t)
	hasher := password.NewDefaultHasher()
	log := logger.New("user-test", logger.WithWriter(io.Discard))

	_ = BootstrapAdmin(context.Background(), store, hasher, log, "admin@nexusmart.local", "AdminPass123!")
	adminUser, _ := store.GetUserByEmail(context.Background(), "admin@nexusmart.local")
	adminCtx := ctxWithUser(adminUser.ID, RoleSuperAdmin, "")

	reg1, _ := srv.Register(context.Background(), &userv1.RegisterRequest{
		FullName: "Seller One",
		Email:    "seller1@example.com",
		Phone:    "0944444444",
		Password: "Password123!",
	})
	_, _ = srv.VerifyOTP(context.Background(), &userv1.VerifyOTPRequest{
		Email: "seller1@example.com",
		Otp:   pub.userRegistered[0].OTP,
	})
	loginResp, _ := srv.Login(context.Background(), &userv1.LoginRequest{
		Email:    "seller1@example.com",
		Password: "Password123!",
	})

	sellerCtx := ctxWithUser(reg1.GetUserId(), RoleBuyer, "")
	otherCtx := ctxWithUser(uuid.NewString(), RoleBuyer, "")

	// Create 2-level address.
	addrResp, err := srv.CreateAddress(sellerCtx, &userv1.CreateAddressRequest{
		RecipientName:  "Seller One",
		RecipientPhone: "0944444444",
		ProvinceCode:   "79",
		ProvinceName:   "Ho Chi Minh",
		WardCode:       "26734",
		WardName:       "Ben Nghe",
		StreetAddress:  "123 Le Loi",
		IsDefault:      true,
	})
	if err != nil {
		t.Fatalf("CreateAddress failed: %v", err)
	}

	// Another user cannot read/update/delete seller's address.
	if _, err := srv.GetAddress(otherCtx, &userv1.GetAddressRequest{AddressId: addrResp.GetAddress().GetId()}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied on other user's GetAddress, got %v", err)
	}
	if _, err := srv.DeleteAddress(otherCtx, &userv1.DeleteAddressRequest{AddressId: addrResp.GetAddress().GetId()}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied on other user's DeleteAddress, got %v", err)
	}

	// Register shop -> pending.
	shopResp, err := srv.RegisterShop(sellerCtx, &userv1.RegisterShopRequest{
		Name:            "Tech Store VN",
		Description:     "Electronics",
		PickupAddressId: addrResp.GetAddress().GetId(),
		ContactPhone:    "0944444444",
	})
	if err != nil || shopResp.GetShop().GetStatus() != userv1.ShopStatus_SHOP_STATUS_PENDING {
		t.Fatalf("RegisterShop failed: resp=%+v err=%v", shopResp, err)
	}

	// Admin approves shop -> grants seller role + shop_id + emits user.shop_reviewed.
	revResp, err := srv.AdminReviewShop(adminCtx, &userv1.AdminReviewShopRequest{
		ShopId:   shopResp.GetShop().GetId(),
		Decision: userv1.ReviewDecision_REVIEW_DECISION_APPROVE,
	})
	if err != nil || revResp.GetShop().GetStatus() != userv1.ShopStatus_SHOP_STATUS_ACTIVE {
		t.Fatalf("AdminReviewShop approve failed: resp=%+v err=%v", revResp, err)
	}
	if len(pub.shopReviewed) != 1 || pub.shopReviewed[0].Decision != "APPROVE" {
		t.Fatalf("expected user.shop_reviewed event, got %+v", pub.shopReviewed)
	}

	// Refreshing token issues JWT with new seller role and shop_id.
	refResp, err := srv.RefreshToken(context.Background(), &userv1.RefreshTokenRequest{
		RefreshToken: loginResp.GetRefreshToken(),
	})
	if err != nil {
		t.Fatalf("RefreshToken after shop approval failed: %v", err)
	}
	claims, err := authMgr.VerifyAccessToken(refResp.GetAccessToken())
	if err != nil {
		t.Fatalf("VerifyAccessToken failed: %v", err)
	}
	if claims.Role != RoleSeller || claims.ShopID != shopResp.GetShop().GetId() {
		t.Fatalf("expected seller role and shop_id %s, got role=%s shop_id=%s", shopResp.GetShop().GetId(), claims.Role, claims.ShopID)
	}
}

func TestLogoutRevokesAccessJTIAndVerifyOTPRejectsNonPending(t *testing.T) {
	srv, _, _, pub, _ := newTestHarness(t)
	ctx := context.Background()

	reg, err := srv.Register(ctx, &userv1.RegisterRequest{
		FullName: "Revoke Tester",
		Email:    "revoke@example.com",
		Phone:    "0955555555",
		Password: "Password123!",
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	otp := pub.userRegistered[0].OTP
	if _, err := srv.VerifyOTP(ctx, &userv1.VerifyOTPRequest{Email: "revoke@example.com", Otp: otp}); err != nil {
		t.Fatalf("VerifyOTP: %v", err)
	}
	// Re-verifying OTP on already active user must fail with FailedPrecondition.
	if _, err := srv.VerifyOTP(ctx, &userv1.VerifyOTPRequest{Email: "revoke@example.com", Otp: otp}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition on VerifyOTP for active user, got %v", err)
	}

	loginResp, err := srv.Login(ctx, &userv1.LoginRequest{Email: "revoke@example.com", Password: "Password123!"})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	userCtx := ctxWithUser(reg.GetUserId(), RoleBuyer, "")
	if _, err := srv.Logout(userCtx, &userv1.LogoutRequest{RefreshToken: loginResp.GetRefreshToken()}); err != nil {
		t.Fatalf("Logout: %v", err)
	}

	// Reusing the same access token JTI after Logout must be rejected.
	if _, err := srv.GetProfile(userCtx, &userv1.GetProfileRequest{UserId: reg.GetUserId()}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated for revoked JTI, got %v", err)
	}
}

func TestRefreshTokenConcurrentReplayRevokesFamilyOutsideRolledBackTx(t *testing.T) {
	srv, store, _, pub, _ := newTestHarness(t)
	ctx := context.Background()

	_, _ = srv.Register(ctx, &userv1.RegisterRequest{
		FullName: "Race Buyer",
		Email:    "race@example.com",
		Phone:    "0966666666",
		Password: "Password123!",
	})
	_, _ = srv.VerifyOTP(ctx, &userv1.VerifyOTPRequest{
		Email: "race@example.com",
		Otp:   pub.userRegistered[0].OTP,
	})
	loginResp, err := srv.Login(ctx, &userv1.LoginRequest{
		Email:    "race@example.com",
		Password: "Password123!",
	})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	// Rotate once so the family has two tokens (rt1 revoked, rt2 active).
	refResp, err := srv.RefreshToken(ctx, &userv1.RefreshTokenRequest{RefreshToken: loginResp.GetRefreshToken()})
	if err != nil {
		t.Fatalf("first RefreshToken: %v", err)
	}

	// Simulate concurrent replay where GetRefreshTokenByHash saw active rt2,
	// then UPDATE ... WHERE revoked_at IS NULL returned 0 rows inside WithTx.
	store.mu.Lock()
	store.forceReplaceZeroRows = true
	store.mu.Unlock()

	if _, err := srv.RefreshToken(ctx, &userv1.RefreshTokenRequest{RefreshToken: refResp.GetRefreshToken()}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated on concurrent replay, got %v", err)
	}

	// Family revocation MUST persist outside the rolled-back tx!
	store.mu.Lock()
	store.forceReplaceZeroRows = false
	for id, rt := range store.refreshTokens {
		if rt.RevokedAt == nil {
			store.mu.Unlock()
			t.Fatalf("expected refresh token %s in family %s to be revoked outside rolled-back tx", id, rt.FamilyID)
		}
	}
	store.mu.Unlock()
}

func TestShopSlugAutoGenerationLookupAndPublicList(t *testing.T) {
	srv, store, _, pub, _ := newTestHarness(t)
	hasher := password.NewDefaultHasher()
	log := logger.New("user-test", logger.WithWriter(io.Discard))
	ctx := context.Background()

	_ = BootstrapAdmin(ctx, store, hasher, log, "admin@nexusmart.local", "AdminPass123!")
	adminUser, _ := store.GetUserByEmail(ctx, "admin@nexusmart.local")
	adminCtx := ctxWithUser(adminUser.ID, RoleSuperAdmin, "")

	regUser := func(email, phone string) string {
		r, err := srv.Register(ctx, &userv1.RegisterRequest{
			FullName: "Shop Owner",
			Email:    email,
			Phone:    phone,
			Password: "Password123!",
		})
		if err != nil {
			t.Fatalf("Register %s: %v", email, err)
		}
		otp := pub.userRegistered[len(pub.userRegistered)-1].OTP
		if _, err := srv.VerifyOTP(ctx, &userv1.VerifyOTPRequest{Email: email, Otp: otp}); err != nil {
			t.Fatalf("VerifyOTP %s: %v", email, err)
		}
		return r.GetUserId()
	}

	owner1 := regUser("owner1@example.com", "0971111111")
	owner2 := regUser("owner2@example.com", "0972222222")
	owner3 := regUser("owner3@example.com", "0973333333")

	// 1. Auto-generated Vietnamese slug from shop name.
	sh1Resp, err := srv.RegisterShop(ctxWithUser(owner1, RoleBuyer, ""), &userv1.RegisterShopRequest{
		Name:               "Xưởng Dệt Bạch Đằng",
		Description:        "Cotton compact 220gsm",
		ContactPhone:       "0971111111",
		BusinessLicenseDoc: "GPKD-SECRET-001.pdf",
	})
	if err != nil {
		t.Fatalf("RegisterShop 1: %v", err)
	}
	if sh1Resp.GetShop().GetSlug() != "xuong-det-bach-dang" {
		t.Fatalf("expected slug xuong-det-bach-dang, got %q", sh1Resp.GetShop().GetSlug())
	}

	// 2. Explicit slug succeeds; duplicate explicit slug rejected; invalid slug rejected.
	sh2Resp, err := srv.RegisterShop(ctxWithUser(owner2, RoleBuyer, ""), &userv1.RegisterShopRequest{
		Name:               "Xưởng Dệt Sông Hồng",
		Slug:               "xuong-det-song-hong",
		Description:        "Pending shop",
		ContactPhone:       "0972222222",
		BusinessLicenseDoc: "GPKD-SECRET-002.pdf",
	})
	if err != nil {
		t.Fatalf("RegisterShop 2: %v", err)
	}
	if sh2Resp.GetShop().GetSlug() != "xuong-det-song-hong" {
		t.Fatalf("expected explicit slug xuong-det-song-hong, got %q", sh2Resp.GetShop().GetSlug())
	}
	if _, err := srv.RegisterShop(ctxWithUser(owner3, RoleBuyer, ""), &userv1.RegisterShopRequest{
		Name: "Xưởng Dệt Mới",
		Slug: "xuong-det-song-hong",
	}); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("expected AlreadyExists on duplicate shop slug, got %v", err)
	}
	if _, err := srv.RegisterShop(ctxWithUser(owner3, RoleBuyer, ""), &userv1.RegisterShopRequest{
		Name: "Xưởng Dệt Lỗi",
		Slug: "!!!",
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument on invalid shop slug, got %v", err)
	}

	// 3. Before any approval, ListPublicShops returns 0 shops.
	pubBefore, err := srv.ListPublicShops(ctx, &userv1.ListPublicShopsRequest{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("ListPublicShops before approve: %v", err)
	}
	if len(pubBefore.GetShops()) != 0 || pubBefore.GetTotal() != 0 {
		t.Fatalf("expected 0 public shops while all pending, got %d (total=%d)", len(pubBefore.GetShops()), pubBefore.GetTotal())
	}

	// 4. Approve shop 1 only; ListPublicShops returns only shop 1 with private fields stripped.
	if _, err := srv.AdminReviewShop(adminCtx, &userv1.AdminReviewShopRequest{
		ShopId:   sh1Resp.GetShop().GetId(),
		Decision: userv1.ReviewDecision_REVIEW_DECISION_APPROVE,
	}); err != nil {
		t.Fatalf("AdminReviewShop: %v", err)
	}

	pubAfter, err := srv.ListPublicShops(ctx, &userv1.ListPublicShopsRequest{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("ListPublicShops after approve: %v", err)
	}
	if len(pubAfter.GetShops()) != 1 || pubAfter.GetTotal() != 1 {
		t.Fatalf("expected 1 public shop, got %d (total=%d)", len(pubAfter.GetShops()), pubAfter.GetTotal())
	}
	pubShop := pubAfter.GetShops()[0]
	if pubShop.GetId() != sh1Resp.GetShop().GetId() || pubShop.GetSlug() != "xuong-det-bach-dang" {
		t.Fatalf("unexpected public shop identity: %+v", pubShop)
	}
	if pubShop.GetOwnerUserId() != "" || pubShop.GetContactPhone() != "" || pubShop.GetBusinessLicenseDoc() != "" || pubShop.GetPickupAddressId() != "" || pubShop.GetRejectionReason() != "" {
		t.Fatalf("public shop leaked private fields: %+v", pubShop)
	}

	// 5. GetShop lookup by slug works and returns NotFound for unknown slug.
	bySlug, err := srv.GetShop(ctx, &userv1.GetShopRequest{Slug: "xuong-det-bach-dang"})
	if err != nil || bySlug.GetShop().GetId() != sh1Resp.GetShop().GetId() {
		t.Fatalf("GetShop by slug failed: resp=%+v err=%v", bySlug, err)
	}
	if _, err := srv.GetShop(ctx, &userv1.GetShopRequest{Slug: "khong-ton-tai"}); status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound for unknown shop slug, got %v", err)
	}
}
