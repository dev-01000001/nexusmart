package service

import (
	"context"
	"crypto/subtle"
	"io"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
	mu                      sync.Mutex
	userLocks               map[string]*sync.RWMutex
	shopLocks               map[string]*sync.Mutex
	users                   map[string]db.User
	refreshTokens           map[string]db.RefreshToken
	passwordResets          map[string]db.PasswordReset
	shops                   map[string]db.Shop
	shopBankAccounts        map[string]db.ShopBankAccount
	shopSuspensionApprovals map[string]db.ShopSuspensionApproval
	addresses               map[string]db.Address
	codRejections           map[string]string
	auditLogs               []db.AuditLog
	userActivityLogs        []db.UserActivityLog
	outboxEvents            []db.OutboxEvent
	outboxSeq               int64
	forceReplaceZeroRows    bool
	onUpdateUserPassword    func(userID string)
}

func newMemStore() *memStore {
	return &memStore{
		userLocks:               make(map[string]*sync.RWMutex),
		shopLocks:               make(map[string]*sync.Mutex),
		users:                   make(map[string]db.User),
		refreshTokens:           make(map[string]db.RefreshToken),
		passwordResets:          make(map[string]db.PasswordReset),
		shops:                   make(map[string]db.Shop),
		shopBankAccounts:        make(map[string]db.ShopBankAccount),
		shopSuspensionApprovals: make(map[string]db.ShopSuspensionApproval),
		addresses:               make(map[string]db.Address),
		codRejections:           make(map[string]string),
	}
}

func (m *memStore) shopLock(shopID string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.shopLocks[shopID]
	if !ok {
		l = &sync.Mutex{}
		m.shopLocks[shopID] = l
	}
	return l
}

func (m *memStore) userLock(userID string) *sync.RWMutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.userLocks[userID]
	if !ok {
		l = &sync.RWMutex{}
		m.userLocks[userID] = l
	}
	return l
}

func (m *memStore) Ping(_ context.Context) error { return nil }

// memTx models PostgreSQL per-user FOR SHARE vs UPDATE row locking and per-tx rollback.
type memTx struct {
	*memStore
	shared   map[string]*sync.RWMutex
	excl     map[string]*sync.RWMutex
	shopExcl map[string]*sync.Mutex
	undo     []func()
}

func (t *memTx) lockShop(shopID string) {
	if _, ok := t.shopExcl[shopID]; ok {
		return
	}
	l := t.shopLock(shopID)
	l.Lock()
	t.shopExcl[shopID] = l
}

func (t *memTx) lockShared(userID string) {
	if _, ok := t.shared[userID]; ok {
		return
	}
	if _, ok := t.excl[userID]; ok {
		return
	}
	l := t.userLock(userID)
	l.RLock()
	t.shared[userID] = l
}

func (t *memTx) lockExcl(userID string) {
	if _, ok := t.excl[userID]; ok {
		return
	}
	if l, ok := t.shared[userID]; ok {
		l.RUnlock()
		delete(t.shared, userID)
	}
	l := t.userLock(userID)
	l.Lock()
	t.excl[userID] = l
}

func (t *memTx) release() {
	for id, l := range t.excl {
		l.Unlock()
		delete(t.excl, id)
	}
	for id, l := range t.shared {
		l.RUnlock()
		delete(t.shared, id)
	}
	for id, l := range t.shopExcl {
		l.Unlock()
		delete(t.shopExcl, id)
	}
}

func (t *memTx) GetShopByIDForUpdate(ctx context.Context, id string) (db.Shop, error) {
	t.lockShop(id)
	return t.memStore.GetShopByID(ctx, id)
}

func (t *memTx) CreateUser(ctx context.Context, arg db.CreateUserParams) (db.User, error) {
	u, err := t.memStore.CreateUser(ctx, arg)
	if err == nil {
		id := u.ID
		t.undo = append(t.undo, func() {
			t.mu.Lock()
			delete(t.users, id)
			t.mu.Unlock()
		})
	}
	return u, err
}

func (t *memTx) SuspendShop(ctx context.Context, arg db.SuspendShopParams) (db.Shop, error) {
	t.lockShop(arg.ID)
	t.mu.Lock()
	prev, hadPrev := t.shops[arg.ID]
	t.mu.Unlock()
	sh, err := t.memStore.SuspendShop(ctx, arg)
	if err == nil && hadPrev {
		t.undo = append(t.undo, func() {
			t.mu.Lock()
			t.shops[arg.ID] = prev
			t.mu.Unlock()
		})
	}
	return sh, err
}

func (t *memTx) UnsuspendShop(ctx context.Context, arg db.UnsuspendShopParams) (db.Shop, error) {
	t.lockShop(arg.ID)
	t.mu.Lock()
	prev, hadPrev := t.shops[arg.ID]
	t.mu.Unlock()
	sh, err := t.memStore.UnsuspendShop(ctx, arg)
	if err == nil && hadPrev {
		t.undo = append(t.undo, func() {
			t.mu.Lock()
			t.shops[arg.ID] = prev
			t.mu.Unlock()
		})
	}
	return sh, err
}

func (t *memTx) CreateShopSuspensionApproval(ctx context.Context, arg db.CreateShopSuspensionApprovalParams) (db.ShopSuspensionApproval, error) {
	a, err := t.memStore.CreateShopSuspensionApproval(ctx, arg)
	if err == nil {
		id := a.ID
		t.undo = append(t.undo, func() {
			t.mu.Lock()
			delete(t.shopSuspensionApprovals, id)
			t.mu.Unlock()
		})
	}
	return a, err
}

func (t *memTx) DecideShopSuspensionApproval(ctx context.Context, arg db.DecideShopSuspensionApprovalParams) (db.ShopSuspensionApproval, error) {
	t.mu.Lock()
	prev, hadPrev := t.shopSuspensionApprovals[arg.ID]
	t.mu.Unlock()
	a, err := t.memStore.DecideShopSuspensionApproval(ctx, arg)
	if err == nil && hadPrev {
		t.undo = append(t.undo, func() {
			t.mu.Lock()
			t.shopSuspensionApprovals[arg.ID] = prev
			t.mu.Unlock()
		})
	}
	return a, err
}

func (t *memTx) InsertUserActivityLog(ctx context.Context, arg db.InsertUserActivityLogParams) (db.UserActivityLog, error) {
	l, err := t.memStore.InsertUserActivityLog(ctx, arg)
	if err == nil {
		id := l.ID
		t.undo = append(t.undo, func() {
			t.mu.Lock()
			for i := len(t.userActivityLogs) - 1; i >= 0; i-- {
				if t.userActivityLogs[i].ID == id {
					t.userActivityLogs = append(t.userActivityLogs[:i], t.userActivityLogs[i+1:]...)
					break
				}
			}
			t.mu.Unlock()
		})
	}
	return l, err
}

func (t *memTx) InsertOutboxEvent(ctx context.Context, arg db.InsertOutboxEventParams) error {
	err := t.memStore.InsertOutboxEvent(ctx, arg)
	if err == nil {
		id := arg.ID
		t.undo = append(t.undo, func() {
			t.mu.Lock()
			for i := len(t.outboxEvents) - 1; i >= 0; i-- {
				if t.outboxEvents[i].ID == id {
					t.outboxEvents = append(t.outboxEvents[:i], t.outboxEvents[i+1:]...)
					break
				}
			}
			t.mu.Unlock()
		})
	}
	return err
}

func (t *memTx) GetUserByIDForShare(ctx context.Context, id string) (db.User, error) {
	t.lockShared(id)
	return t.memStore.GetUserByID(ctx, id)
}

func (t *memTx) UpdateUserStatus(ctx context.Context, arg db.UpdateUserStatusParams) (db.User, error) {
	t.lockExcl(arg.ID)
	t.mu.Lock()
	prev, hadPrev := t.users[arg.ID]
	t.mu.Unlock()
	u, err := t.memStore.UpdateUserStatus(ctx, arg)
	if err == nil && hadPrev {
		t.undo = append(t.undo, func() {
			t.mu.Lock()
			t.users[arg.ID] = prev
			t.mu.Unlock()
		})
	}
	return u, err
}

func (t *memTx) UpdateUserRole(ctx context.Context, arg db.UpdateUserRoleParams) (db.User, error) {
	t.lockExcl(arg.ID)
	t.mu.Lock()
	prev, hadPrev := t.users[arg.ID]
	t.mu.Unlock()
	u, err := t.memStore.UpdateUserRole(ctx, arg)
	if err == nil && hadPrev {
		t.undo = append(t.undo, func() {
			t.mu.Lock()
			t.users[arg.ID] = prev
			t.mu.Unlock()
		})
	}
	return u, err
}

func (t *memTx) UpdateUserPassword(ctx context.Context, arg db.UpdateUserPasswordParams) (db.User, error) {
	t.lockExcl(arg.ID)
	t.mu.Lock()
	prev, hadPrev := t.users[arg.ID]
	t.mu.Unlock()
	u, err := t.memStore.UpdateUserPassword(ctx, arg)
	if err == nil && hadPrev {
		t.undo = append(t.undo, func() {
			t.mu.Lock()
			t.users[arg.ID] = prev
			t.mu.Unlock()
		})
	}
	return u, err
}

func (t *memTx) CreateRefreshToken(ctx context.Context, arg db.CreateRefreshTokenParams) (db.RefreshToken, error) {
	rt, err := t.memStore.CreateRefreshToken(ctx, arg)
	if err == nil {
		id := rt.ID
		t.undo = append(t.undo, func() {
			t.mu.Lock()
			delete(t.refreshTokens, id)
			t.mu.Unlock()
		})
	}
	return rt, err
}

func (t *memTx) RevokeRefreshTokenWithReplacement(ctx context.Context, arg db.RevokeRefreshTokenWithReplacementParams) (int64, error) {
	t.mu.Lock()
	prev, hadPrev := t.refreshTokens[arg.ID]
	t.mu.Unlock()
	rows, err := t.memStore.RevokeRefreshTokenWithReplacement(ctx, arg)
	if err == nil && rows > 0 && hadPrev {
		t.undo = append(t.undo, func() {
			t.mu.Lock()
			t.refreshTokens[arg.ID] = prev
			t.mu.Unlock()
		})
	}
	return rows, err
}

func (t *memTx) RevokeAllUserRefreshTokens(ctx context.Context, userID string) error {
	t.mu.Lock()
	var prevs []db.RefreshToken
	for _, rt := range t.refreshTokens {
		if rt.UserID == userID && rt.RevokedAt == nil {
			prevs = append(prevs, rt)
		}
	}
	t.mu.Unlock()
	err := t.memStore.RevokeAllUserRefreshTokens(ctx, userID)
	if err == nil && len(prevs) > 0 {
		t.undo = append(t.undo, func() {
			t.mu.Lock()
			for _, p := range prevs {
				t.refreshTokens[p.ID] = p
			}
			t.mu.Unlock()
		})
	}
	return err
}

func (t *memTx) CreatePasswordReset(ctx context.Context, arg db.CreatePasswordResetParams) (db.PasswordReset, error) {
	pr, err := t.memStore.CreatePasswordReset(ctx, arg)
	if err == nil {
		id := pr.ID
		t.undo = append(t.undo, func() {
			t.mu.Lock()
			delete(t.passwordResets, id)
			t.mu.Unlock()
		})
	}
	return pr, err
}

func (t *memTx) MarkPasswordResetUsed(ctx context.Context, arg db.MarkPasswordResetUsedParams) (int64, error) {
	t.mu.Lock()
	prev, hadPrev := t.passwordResets[arg.ID]
	t.mu.Unlock()
	rows, err := t.memStore.MarkPasswordResetUsed(ctx, arg)
	if err == nil && rows > 0 && hadPrev {
		t.undo = append(t.undo, func() {
			t.mu.Lock()
			t.passwordResets[arg.ID] = prev
			t.mu.Unlock()
		})
	}
	return rows, err
}

func (t *memTx) InvalidateUserPasswordResets(ctx context.Context, arg db.InvalidateUserPasswordResetsParams) error {
	t.mu.Lock()
	var prevs []db.PasswordReset
	for _, pr := range t.passwordResets {
		if pr.UserID == arg.UserID && pr.UsedAt == nil {
			prevs = append(prevs, pr)
		}
	}
	t.mu.Unlock()
	err := t.memStore.InvalidateUserPasswordResets(ctx, arg)
	if err == nil && len(prevs) > 0 {
		t.undo = append(t.undo, func() {
			t.mu.Lock()
			for _, p := range prevs {
				t.passwordResets[p.ID] = p
			}
			t.mu.Unlock()
		})
	}
	return err
}

func (t *memTx) InsertAuditLog(ctx context.Context, arg db.InsertAuditLogParams) (db.AuditLog, error) {
	l, err := t.memStore.InsertAuditLog(ctx, arg)
	if err == nil {
		id := l.ID
		t.undo = append(t.undo, func() {
			t.mu.Lock()
			for i := len(t.auditLogs) - 1; i >= 0; i-- {
				if t.auditLogs[i].ID == id {
					t.auditLogs = append(t.auditLogs[:i], t.auditLogs[i+1:]...)
					break
				}
			}
			t.mu.Unlock()
		})
	}
	return l, err
}

func (t *memTx) snapshotShopBankAccounts() map[string]db.ShopBankAccount {
	t.mu.Lock()
	defer t.mu.Unlock()
	cp := make(map[string]db.ShopBankAccount, len(t.shopBankAccounts))
	for k, v := range t.shopBankAccounts {
		cp[k] = v
	}
	return cp
}

func (t *memTx) pushBankAccountUndo(prev map[string]db.ShopBankAccount) {
	t.undo = append(t.undo, func() {
		t.mu.Lock()
		t.shopBankAccounts = prev
		t.mu.Unlock()
	})
}

func (t *memTx) GetUserByIDForUpdate(ctx context.Context, id string) (db.User, error) {
	t.lockExcl(id)
	return t.memStore.GetUserByID(ctx, id)
}

func (t *memTx) UpdateUserProfile(ctx context.Context, arg db.UpdateUserProfileParams) (db.User, error) {
	t.lockExcl(arg.ID)
	t.mu.Lock()
	prev, hadPrev := t.users[arg.ID]
	t.mu.Unlock()
	u, err := t.memStore.UpdateUserProfile(ctx, arg)
	if err == nil && hadPrev {
		t.undo = append(t.undo, func() {
			t.mu.Lock()
			t.users[arg.ID] = prev
			t.mu.Unlock()
		})
	}
	return u, err
}

func (t *memTx) MarkUserPhoneVerified(ctx context.Context, arg db.MarkUserPhoneVerifiedParams) (db.User, error) {
	t.lockExcl(arg.ID)
	t.mu.Lock()
	prev, hadPrev := t.users[arg.ID]
	t.mu.Unlock()
	u, err := t.memStore.MarkUserPhoneVerified(ctx, arg)
	if err == nil && hadPrev {
		t.undo = append(t.undo, func() {
			t.mu.Lock()
			t.users[arg.ID] = prev
			t.mu.Unlock()
		})
	}
	return u, err
}

func (t *memTx) SupersedePendingOTPShopBankAccounts(ctx context.Context, arg db.SupersedePendingOTPShopBankAccountsParams) error {
	prev := t.snapshotShopBankAccounts()
	err := t.memStore.SupersedePendingOTPShopBankAccounts(ctx, arg)
	if err == nil {
		t.pushBankAccountUndo(prev)
	}
	return err
}

func (t *memTx) SupersedePendingAdminReviewShopBankAccounts(ctx context.Context, arg db.SupersedePendingAdminReviewShopBankAccountsParams) error {
	prev := t.snapshotShopBankAccounts()
	err := t.memStore.SupersedePendingAdminReviewShopBankAccounts(ctx, arg)
	if err == nil {
		t.pushBankAccountUndo(prev)
	}
	return err
}

func (t *memTx) MarkShopBankAccountPublishFailed(ctx context.Context, arg db.MarkShopBankAccountPublishFailedParams) error {
	prev := t.snapshotShopBankAccounts()
	err := t.memStore.MarkShopBankAccountPublishFailed(ctx, arg)
	if err == nil {
		t.pushBankAccountUndo(prev)
	}
	return err
}

func (t *memTx) CreateShopBankAccount(ctx context.Context, arg db.CreateShopBankAccountParams) (db.ShopBankAccount, error) {
	prev := t.snapshotShopBankAccounts()
	acc, err := t.memStore.CreateShopBankAccount(ctx, arg)
	if err == nil {
		t.pushBankAccountUndo(prev)
	}
	return acc, err
}

func (t *memTx) RecordShopBankAccountOTPFailure(ctx context.Context, arg db.RecordShopBankAccountOTPFailureParams) (db.ShopBankAccount, error) {
	prev := t.snapshotShopBankAccounts()
	acc, err := t.memStore.RecordShopBankAccountOTPFailure(ctx, arg)
	if err == nil {
		t.pushBankAccountUndo(prev)
	}
	return acc, err
}

func (t *memTx) SupersedeShopBankAccount(ctx context.Context, arg db.SupersedeShopBankAccountParams) error {
	prev := t.snapshotShopBankAccounts()
	err := t.memStore.SupersedeShopBankAccount(ctx, arg)
	if err == nil {
		t.pushBankAccountUndo(prev)
	}
	return err
}

func (t *memTx) CompleteShopBankAccountOTPVerification(ctx context.Context, arg db.CompleteShopBankAccountOTPVerificationParams) (db.ShopBankAccount, error) {
	prev := t.snapshotShopBankAccounts()
	acc, err := t.memStore.CompleteShopBankAccountOTPVerification(ctx, arg)
	if err == nil {
		t.pushBankAccountUndo(prev)
	}
	return acc, err
}

func (t *memTx) UpdateShopBankAccountAdminReview(ctx context.Context, arg db.UpdateShopBankAccountAdminReviewParams) (db.ShopBankAccount, error) {
	prev := t.snapshotShopBankAccounts()
	acc, err := t.memStore.UpdateShopBankAccountAdminReview(ctx, arg)
	if err == nil {
		t.pushBankAccountUndo(prev)
	}
	return acc, err
}

func (m *memStore) WithTx(_ context.Context, fn func(q db.Querier) error) error {
	tx := &memTx{
		memStore: m,
		shared:   make(map[string]*sync.RWMutex),
		excl:     make(map[string]*sync.RWMutex),
		shopExcl: make(map[string]*sync.Mutex),
	}
	defer tx.release()
	if err := fn(tx); err != nil {
		for i := len(tx.undo) - 1; i >= 0; i-- {
			tx.undo[i]()
		}
		return err
	}
	return nil
}

func (m *memStore) GetUserByIDForShare(ctx context.Context, id string) (db.User, error) {
	return m.GetUserByID(ctx, id)
}

func (m *memStore) GetUserByIDForUpdate(ctx context.Context, id string) (db.User, error) {
	return m.GetUserByID(ctx, id)
}

func (m *memStore) CreateUser(_ context.Context, arg db.CreateUserParams) (db.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if arg.PhoneVerified && arg.Phone != "" {
		for otherID, other := range m.users {
			if otherID != arg.ID && other.PhoneVerified && other.Phone == arg.Phone {
				return db.User{}, &pgconn.PgError{Code: "23505", ConstraintName: "idx_users_phone_verified_unique"}
			}
		}
	}
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
		if phone != "" && u.PhoneVerified && u.Phone == phone {
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
	if arg.PhoneVerified && arg.Phone != "" {
		for otherID, other := range m.users {
			if otherID != arg.ID && other.PhoneVerified && other.Phone == arg.Phone {
				return db.User{}, &pgconn.PgError{Code: "23505", ConstraintName: "idx_users_phone_verified_unique"}
			}
		}
	}
	u.FullName = arg.FullName
	u.Phone = arg.Phone
	u.AvatarURL = arg.AvatarURL
	u.PhoneVerified = arg.PhoneVerified
	u.UpdatedAt = time.Now().UTC()
	m.users[u.ID] = u
	return u, nil
}

func (m *memStore) MarkUserPhoneVerified(_ context.Context, arg db.MarkUserPhoneVerifiedParams) (db.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[arg.ID]
	if !ok {
		return db.User{}, pgx.ErrNoRows
	}
	if arg.Phone != "" {
		for otherID, other := range m.users {
			if otherID != arg.ID && other.PhoneVerified && other.Phone == arg.Phone {
				return db.User{}, &pgconn.PgError{Code: "23505", ConstraintName: "idx_users_phone_verified_unique"}
			}
		}
	}
	u.Phone = arg.Phone
	u.PhoneVerified = true
	u.UpdatedAt = time.Now().UTC()
	m.users[u.ID] = u
	return u, nil
}

func (m *memStore) InsertCODRejection(_ context.Context, arg db.InsertCODRejectionParams) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.codRejections[arg.OrderID]; exists {
		return 0, nil
	}
	m.codRejections[arg.OrderID] = arg.UserID
	return 1, nil
}

func (m *memStore) IncrementUserCODRejectedCount(_ context.Context, id string) (db.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return db.User{}, pgx.ErrNoRows
	}
	u.CODRejectedCount++
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
	u.LockReason = arg.LockReason
	u.UpdatedAt = time.Now().UTC()
	m.users[u.ID] = u
	return u, nil
}

func (m *memStore) UpdateUserPassword(_ context.Context, arg db.UpdateUserPasswordParams) (db.User, error) {
	m.mu.Lock()
	hook := m.onUpdateUserPassword
	m.mu.Unlock()
	if hook != nil {
		hook(arg.ID)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[arg.ID]
	if !ok {
		return db.User{}, pgx.ErrNoRows
	}
	u.PasswordHash = arg.PasswordHash
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

func (m *memStore) ListUsers(_ context.Context, arg db.ListUsersParams) ([]db.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	queryIsUUID := len(arg.Query) == 36 && arg.Query[8] == '-' && isValidUUID(arg.Query)
	var res []db.User
	for _, u := range m.users {
		if arg.Role != "" && u.Role != arg.Role {
			continue
		}
		if arg.Status != "" && u.Status != arg.Status {
			continue
		}
		if arg.Query != "" {
			if queryIsUUID {
				if !strings.EqualFold(u.ID, arg.Query) {
					continue
				}
			} else {
				q := strings.ToLower(arg.Query)
				if !strings.Contains(strings.ToLower(u.FullName), q) &&
					!strings.Contains(strings.ToLower(u.Email), q) &&
					!strings.Contains(strings.ToLower(u.Phone), q) {
					continue
				}
			}
		}
		res = append(res, u)
	}
	sort.Slice(res, func(i, j int) bool {
		if !res[i].CreatedAt.Equal(res[j].CreatedAt) {
			return res[i].CreatedAt.After(res[j].CreatedAt)
		}
		return res[i].ID > res[j].ID
	})
	offset := int(arg.PageOffset)
	if offset < 0 {
		offset = 0
	}
	if offset >= len(res) {
		return []db.User{}, nil
	}
	end := len(res)
	if arg.PageLimit > 0 && offset+int(arg.PageLimit) < end {
		end = offset + int(arg.PageLimit)
	}
	return res[offset:end], nil
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

func (m *memStore) InvalidateUserPasswordResets(_ context.Context, arg db.InvalidateUserPasswordResetsParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, pr := range m.passwordResets {
		if pr.UserID == arg.UserID && pr.UsedAt == nil {
			pr.UsedAt = arg.UsedAt
			m.passwordResets[id] = pr
		}
	}
	return nil
}

func (m *memStore) CreatePasswordReset(_ context.Context, arg db.CreatePasswordResetParams) (db.PasswordReset, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	pr := db.PasswordReset{
		ID:        arg.ID,
		UserID:    arg.UserID,
		TokenHash: arg.TokenHash,
		ExpiresAt: arg.ExpiresAt,
		CreatedAt: time.Now().UTC(),
	}
	m.passwordResets[pr.ID] = pr
	return pr, nil
}

func (m *memStore) GetPasswordResetByHashForUpdate(_ context.Context, tokenHash string) (db.PasswordReset, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, pr := range m.passwordResets {
		if pr.TokenHash == tokenHash {
			return pr, nil
		}
	}
	return db.PasswordReset{}, pgx.ErrNoRows
}

func (m *memStore) MarkPasswordResetUsed(_ context.Context, arg db.MarkPasswordResetUsedParams) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	pr, ok := m.passwordResets[arg.ID]
	if !ok || pr.UsedAt != nil {
		return 0, nil
	}
	pr.UsedAt = arg.UsedAt
	m.passwordResets[pr.ID] = pr
	return 1, nil
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
		StatusVersion:      1,
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

func (m *memStore) hasPendingBankReviewLocked(shopID string) bool {
	for _, acc := range m.shopBankAccounts {
		if acc.ShopID == shopID && acc.Status == BankAccountStatusPendingAdminReview {
			return true
		}
	}
	return false
}

func (m *memStore) AdminListShops(_ context.Context, arg db.AdminListShopsParams) ([]db.AdminListShopsRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := make([]db.AdminListShopsRow, 0, len(m.shops))
	for _, sh := range m.shops {
		if arg.Status != "" && sh.Status != arg.Status {
			continue
		}
		pending := m.hasPendingBankReviewLocked(sh.ID)
		if arg.BankReviewPendingOnly && !pending {
			continue
		}
		rows = append(rows, db.AdminListShopsRow{
			ID:                 sh.ID,
			OwnerUserID:        sh.OwnerUserID,
			Name:               sh.Name,
			Description:        sh.Description,
			PickupAddressID:    sh.PickupAddressID,
			ContactPhone:       sh.ContactPhone,
			BusinessLicenseDoc: sh.BusinessLicenseDoc,
			Status:             sh.Status,
			RejectionReason:    sh.RejectionReason,
			CreatedAt:          sh.CreatedAt,
			UpdatedAt:          sh.UpdatedAt,
			Slug:               sh.Slug,
			SuspendedAt:        sh.SuspendedAt,
			SuspendedReason:    sh.SuspendedReason,
			StatusVersion:      sh.StatusVersion,
			BankReviewPending:  pending,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].CreatedAt.After(rows[j].CreatedAt)
		}
		return rows[i].ID > rows[j].ID
	})
	offset := int(arg.PageOffset)
	if offset < 0 {
		offset = 0
	}
	if offset >= len(rows) {
		return []db.AdminListShopsRow{}, nil
	}
	end := len(rows)
	if arg.PageLimit > 0 && offset+int(arg.PageLimit) < end {
		end = offset + int(arg.PageLimit)
	}
	return rows[offset:end], nil
}

func (m *memStore) AdminCountShops(_ context.Context, arg db.AdminCountShopsParams) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var total int64
	for _, sh := range m.shops {
		if arg.Status != "" && sh.Status != arg.Status {
			continue
		}
		if arg.BankReviewPendingOnly && !m.hasPendingBankReviewLocked(sh.ID) {
			continue
		}
		total++
	}
	return total, nil
}

func (m *memStore) ResubmitShop(_ context.Context, arg db.ResubmitShopParams) (db.Shop, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sh, ok := m.shops[arg.ID]
	isEligible := ok && (sh.Status == ShopStatusRejected ||
		(sh.Status == ShopStatusPending && strings.TrimSpace(sh.RejectionReason) != ""))
	if !isEligible {
		return db.Shop{}, pgx.ErrNoRows
	}
	sh.Name = arg.Name
	sh.Slug = arg.Slug
	sh.Description = arg.Description
	sh.PickupAddressID = arg.PickupAddressID
	sh.ContactPhone = arg.ContactPhone
	sh.BusinessLicenseDoc = arg.BusinessLicenseDoc
	if sh.Status != ShopStatusPending {
		sh.StatusVersion++
	}
	sh.Status = ShopStatusPending
	sh.RejectionReason = ""
	sh.VerifiedOwnerName = ""
	sh.UpdatedAt = time.Now().UTC()
	m.shops[sh.ID] = sh
	return sh, nil
}

func (m *memStore) UpdateShopReview(_ context.Context, arg db.UpdateShopReviewParams) (db.Shop, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sh, ok := m.shops[arg.ID]
	if !ok || sh.Status != ShopStatusPending {
		return db.Shop{}, pgx.ErrNoRows
	}
	if sh.Status != arg.Status {
		sh.StatusVersion++
	}
	sh.Status = arg.Status
	sh.RejectionReason = arg.RejectionReason
	if arg.Status == ShopStatusActive {
		sh.VerifiedOwnerName = arg.VerifiedOwnerName
	}
	sh.UpdatedAt = time.Now().UTC()
	m.shops[sh.ID] = sh
	return sh, nil
}

func (m *memStore) SuspendShop(_ context.Context, arg db.SuspendShopParams) (db.Shop, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sh, ok := m.shops[arg.ID]
	if !ok || sh.Status != ShopStatusActive {
		return db.Shop{}, pgx.ErrNoRows
	}
	sh.Status = ShopStatusSuspended
	sh.SuspendedAt = arg.SuspendedAt
	sh.SuspendedReason = arg.SuspendedReason
	if sh.StatusVersion < 1 {
		sh.StatusVersion = 1
	}
	sh.StatusVersion++
	sh.UpdatedAt = arg.UpdatedAt
	m.shops[sh.ID] = sh
	return sh, nil
}

func (m *memStore) UnsuspendShop(_ context.Context, arg db.UnsuspendShopParams) (db.Shop, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sh, ok := m.shops[arg.ID]
	if !ok || sh.Status != ShopStatusSuspended {
		return db.Shop{}, pgx.ErrNoRows
	}
	sh.Status = ShopStatusActive
	sh.SuspendedAt = nil
	sh.SuspendedReason = ""
	if sh.StatusVersion < 1 {
		sh.StatusVersion = 1
	}
	sh.StatusVersion++
	sh.UpdatedAt = arg.UpdatedAt
	m.shops[sh.ID] = sh
	return sh, nil
}

func (m *memStore) ExistsPendingBankReviewByShopID(_ context.Context, shopID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.hasPendingBankReviewLocked(shopID), nil
}

func (m *memStore) ExistsActiveShopByPickupAddressID(_ context.Context, pickupAddressID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, sh := range m.shops {
		if (sh.Status == ShopStatusActive || sh.Status == ShopStatusSuspended || sh.Status == ShopStatusPending) &&
			sh.PickupAddressID == pickupAddressID {
			return true, nil
		}
	}
	return false, nil
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

func (m *memStore) GetShopByIDForUpdate(ctx context.Context, id string) (db.Shop, error) {
	return m.GetShopByID(ctx, id)
}

func (m *memStore) SupersedePendingOTPShopBankAccounts(_ context.Context, arg db.SupersedePendingOTPShopBankAccountsParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, acc := range m.shopBankAccounts {
		if acc.ShopID == arg.ShopID && (acc.Status == "PENDING_OTP" || acc.Status == "REJECTED") {
			acc.Status = "SUPERSEDED"
			acc.OTPHash = ""
			acc.OTPExpiresAt = nil
			acc.UpdatedAt = arg.UpdatedAt
			m.shopBankAccounts[id] = acc
		}
	}
	return nil
}

func (m *memStore) SupersedePendingAdminReviewShopBankAccounts(_ context.Context, arg db.SupersedePendingAdminReviewShopBankAccountsParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, acc := range m.shopBankAccounts {
		if acc.ShopID == arg.ShopID && (acc.Status == "PENDING_ADMIN_REVIEW" || acc.Status == "REJECTED") {
			acc.Status = "SUPERSEDED"
			acc.OTPHash = ""
			acc.OTPExpiresAt = nil
			acc.UpdatedAt = arg.UpdatedAt
			m.shopBankAccounts[id] = acc
		}
	}
	return nil
}

func (m *memStore) CreateShopBankAccount(_ context.Context, arg db.CreateShopBankAccountParams) (db.ShopBankAccount, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	encCopy := append([]byte(nil), arg.AccountNumberEnc...)
	acc := db.ShopBankAccount{
		ID:                  arg.ID,
		ShopID:              arg.ShopID,
		BankCode:            arg.BankCode,
		BankName:            arg.BankName,
		AccountNumber:       "",
		AccountNumberEnc:    encCopy,
		AccountNumberMasked: arg.AccountNumberMasked,
		AccountHolderName:   arg.AccountHolderName,
		HolderNameMatched:   arg.HolderNameMatched,
		Status:              arg.Status,
		ReviewNote:          arg.ReviewNote,
		OTPHash:             arg.OTPHash,
		OTPExpiresAt:        arg.OTPExpiresAt,
		OTPAttempts:         arg.OTPAttempts,
		CreatedAt:           arg.CreatedAt,
		UpdatedAt:           arg.UpdatedAt,
	}
	m.shopBankAccounts[acc.ID] = acc
	return acc, nil
}

func (m *memStore) CountShopBankAccountsCreatedSince(_ context.Context, arg db.CountShopBankAccountsCreatedSinceParams) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for _, acc := range m.shopBankAccounts {
		if acc.ShopID == arg.ShopID && acc.CreatedAt.After(arg.CreatedAt) && !acc.OTPPublishFailed {
			n++
		}
	}
	return n, nil
}

func (m *memStore) MarkShopBankAccountPublishFailed(ctx context.Context, arg db.MarkShopBankAccountPublishFailedParams) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	acc, ok := m.shopBankAccounts[arg.ID]
	if !ok || acc.Status != "PENDING_OTP" {
		return nil
	}
	acc.Status = "SUPERSEDED"
	acc.OTPHash = ""
	acc.OTPExpiresAt = nil
	acc.OTPPublishFailed = true
	acc.UpdatedAt = arg.UpdatedAt
	m.shopBankAccounts[acc.ID] = acc
	return nil
}

func (m *memStore) GetPendingOTPShopBankAccountForUpdate(_ context.Context, shopID string) (db.ShopBankAccount, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var (
		found bool
		best  db.ShopBankAccount
	)
	for _, acc := range m.shopBankAccounts {
		if acc.ShopID == shopID && acc.Status == "PENDING_OTP" {
			if !found || acc.CreatedAt.After(best.CreatedAt) {
				best = acc
				found = true
			}
		}
	}
	if !found {
		return db.ShopBankAccount{}, pgx.ErrNoRows
	}
	return best, nil
}

func (m *memStore) RecordShopBankAccountOTPFailure(_ context.Context, arg db.RecordShopBankAccountOTPFailureParams) (db.ShopBankAccount, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	acc, ok := m.shopBankAccounts[arg.ID]
	if !ok {
		return db.ShopBankAccount{}, pgx.ErrNoRows
	}
	acc.Status = arg.Status
	acc.OTPAttempts = arg.OTPAttempts
	acc.OTPHash = arg.OTPHash
	acc.OTPExpiresAt = arg.OTPExpiresAt
	acc.UpdatedAt = arg.UpdatedAt
	m.shopBankAccounts[acc.ID] = acc
	return acc, nil
}

func (m *memStore) GetVerifiedShopBankAccountByShopID(_ context.Context, shopID string) (db.ShopBankAccount, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, acc := range m.shopBankAccounts {
		if acc.ShopID == shopID && acc.Status == "VERIFIED" {
			return acc, nil
		}
	}
	return db.ShopBankAccount{}, pgx.ErrNoRows
}

func (m *memStore) SupersedeShopBankAccount(_ context.Context, arg db.SupersedeShopBankAccountParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	acc, ok := m.shopBankAccounts[arg.ID]
	if !ok || acc.Status == "SUPERSEDED" {
		return nil
	}
	acc.Status = "SUPERSEDED"
	acc.OTPHash = ""
	acc.OTPExpiresAt = nil
	acc.UpdatedAt = arg.UpdatedAt
	m.shopBankAccounts[acc.ID] = acc
	return nil
}

func (m *memStore) CompleteShopBankAccountOTPVerification(_ context.Context, arg db.CompleteShopBankAccountOTPVerificationParams) (db.ShopBankAccount, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	acc, ok := m.shopBankAccounts[arg.ID]
	if !ok || acc.Status != "PENDING_OTP" {
		return db.ShopBankAccount{}, pgx.ErrNoRows
	}
	acc.Status = arg.Status
	acc.HolderNameMatched = arg.HolderNameMatched
	acc.OTPHash = ""
	acc.OTPExpiresAt = nil
	acc.PayoutLockedUntil = arg.PayoutLockedUntil
	acc.VerifiedAt = arg.VerifiedAt
	acc.UpdatedAt = arg.UpdatedAt
	m.shopBankAccounts[acc.ID] = acc
	return acc, nil
}

func memBankAccountStatusRank(st string) int {
	switch st {
	case BankAccountStatusPendingOTP:
		return 0
	case BankAccountStatusPendingAdminReview:
		return 1
	case BankAccountStatusRejected:
		return 2
	default:
		return 3
	}
}

func (m *memStore) GetLatestShopBankAccountByShopID(_ context.Context, arg db.GetLatestShopBankAccountByShopIDParams) (db.ShopBankAccount, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var (
		found bool
		best  db.ShopBankAccount
	)
	preferScore := func(st string) int {
		if arg.PreferAdminReview && st == BankAccountStatusPendingAdminReview {
			return 0
		}
		return 1
	}
	for _, acc := range m.shopBankAccounts {
		if acc.ShopID != arg.ShopID || acc.Status == BankAccountStatusSuperseded {
			continue
		}
		if acc.Status == BankAccountStatusPendingOTP && (acc.OTPExpiresAt == nil || !acc.OTPExpiresAt.After(arg.NowTime)) {
			continue
		}
		if !found {
			best = acc
			found = true
			continue
		}
		ap, bp := preferScore(acc.Status), preferScore(best.Status)
		if ap < bp ||
			(ap == bp && acc.CreatedAt.After(best.CreatedAt)) ||
			(ap == bp && acc.CreatedAt.Equal(best.CreatedAt) && memBankAccountStatusRank(acc.Status) < memBankAccountStatusRank(best.Status)) {
			best = acc
		}
	}
	if !found {
		return db.ShopBankAccount{}, pgx.ErrNoRows
	}
	return best, nil
}

func (m *memStore) GetShopBankAccountByIDForUpdate(_ context.Context, id string) (db.ShopBankAccount, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	acc, ok := m.shopBankAccounts[id]
	if !ok {
		return db.ShopBankAccount{}, pgx.ErrNoRows
	}
	return acc, nil
}

func (m *memStore) UpdateShopBankAccountAdminReview(_ context.Context, arg db.UpdateShopBankAccountAdminReviewParams) (db.ShopBankAccount, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	acc, ok := m.shopBankAccounts[arg.ID]
	if !ok || acc.Status != "PENDING_ADMIN_REVIEW" {
		return db.ShopBankAccount{}, pgx.ErrNoRows
	}
	acc.Status = arg.Status
	acc.ReviewNote = arg.ReviewNote
	acc.PayoutLockedUntil = arg.PayoutLockedUntil
	acc.VerifiedAt = arg.VerifiedAt
	acc.UpdatedAt = arg.UpdatedAt
	m.shopBankAccounts[acc.ID] = acc
	return acc, nil
}

func (m *memStore) GetUserPlatformCounts(_ context.Context) (db.GetUserPlatformCountsRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var row db.GetUserPlatformCountsRow
	for _, u := range m.users {
		row.TotalUsers++
		if u.Status == StatusActive {
			row.ActiveUsers++
		}
	}
	return row, nil
}

func (m *memStore) GetShopPlatformCounts(_ context.Context) (db.GetShopPlatformCountsRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var row db.GetShopPlatformCountsRow
	for _, sh := range m.shops {
		row.TotalShops++
		switch sh.Status {
		case ShopStatusActive:
			row.ActiveShops++
		case ShopStatusPending:
			row.PendingShops++
		}
	}
	return row, nil
}

var memVNZone = time.FixedZone("Asia/Ho_Chi_Minh", 7*3600)

func (m *memStore) ListDailyNewUsersVN(_ context.Context, arg db.ListDailyNewUsersVNParams) ([]db.ListDailyNewUsersVNRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	counts := make(map[string]int64)
	for _, u := range m.users {
		if !u.CreatedAt.Before(arg.StartTime) && u.CreatedAt.Before(arg.EndTime) {
			day := u.CreatedAt.In(memVNZone).Format("2006-01-02")
			counts[day]++
		}
	}
	rows := make([]db.ListDailyNewUsersVNRow, 0, len(counts))
	for day, cnt := range counts {
		rows = append(rows, db.ListDailyNewUsersVNRow{VnDate: day, Cnt: cnt})
	}
	return rows, nil
}

func (m *memStore) ListDailyNewShopsVN(_ context.Context, arg db.ListDailyNewShopsVNParams) ([]db.ListDailyNewShopsVNRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	counts := make(map[string]int64)
	for _, sh := range m.shops {
		if !sh.CreatedAt.Before(arg.StartTime) && sh.CreatedAt.Before(arg.EndTime) {
			day := sh.CreatedAt.In(memVNZone).Format("2006-01-02")
			counts[day]++
		}
	}
	rows := make([]db.ListDailyNewShopsVNRow, 0, len(counts))
	for day, cnt := range counts {
		rows = append(rows, db.ListDailyNewShopsVNRow{VnDate: day, Cnt: cnt})
	}
	return rows, nil
}

func (m *memStore) CreateShopSuspensionApproval(_ context.Context, arg db.CreateShopSuspensionApprovalParams) (db.ShopSuspensionApproval, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.shopSuspensionApprovals {
		if existing.ShopID == arg.ShopID && existing.Status == ApprovalStatusPending {
			return db.ShopSuspensionApproval{}, &pgconn.PgError{Code: "23505", ConstraintName: "idx_shop_suspension_approvals_one_pending_per_shop"}
		}
	}
	a := db.ShopSuspensionApproval{
		ID:            arg.ID,
		ShopID:        arg.ShopID,
		Status:        ApprovalStatusPending,
		Reason:        arg.Reason,
		RequestedBy:   arg.RequestedBy,
		RequestedRole: arg.RequestedRole,
		CreatedAt:     arg.CreatedAt,
	}
	m.shopSuspensionApprovals[a.ID] = a
	return a, nil
}

func (m *memStore) GetShopSuspensionApprovalByID(_ context.Context, id string) (db.ShopSuspensionApproval, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.shopSuspensionApprovals[id]
	if !ok {
		return db.ShopSuspensionApproval{}, pgx.ErrNoRows
	}
	return a, nil
}

func (m *memStore) GetShopSuspensionApprovalByIDForUpdate(ctx context.Context, id string) (db.ShopSuspensionApproval, error) {
	return m.GetShopSuspensionApprovalByID(ctx, id)
}

func (m *memStore) GetPendingShopSuspensionApprovalByShopID(_ context.Context, shopID string) (db.ShopSuspensionApproval, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.shopSuspensionApprovals {
		if a.ShopID == shopID && a.Status == ApprovalStatusPending {
			return a, nil
		}
	}
	return db.ShopSuspensionApproval{}, pgx.ErrNoRows
}

func (m *memStore) DecideShopSuspensionApproval(_ context.Context, arg db.DecideShopSuspensionApprovalParams) (db.ShopSuspensionApproval, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.shopSuspensionApprovals[arg.ID]
	if !ok || a.Status != ApprovalStatusPending {
		return db.ShopSuspensionApproval{}, pgx.ErrNoRows
	}
	if arg.DecidedBy != nil && *arg.DecidedBy == a.RequestedBy {
		return db.ShopSuspensionApproval{}, &pgconn.PgError{Code: "23514", ConstraintName: "chk_shop_suspension_maker_neq_checker"}
	}
	a.Status = arg.Status
	a.DecidedBy = arg.DecidedBy
	a.DecidedRole = arg.DecidedRole
	a.DecidedNote = arg.DecidedNote
	a.DecidedAt = arg.DecidedAt
	m.shopSuspensionApprovals[a.ID] = a
	return a, nil
}

func (m *memStore) ListShopSuspensionApprovals(_ context.Context, arg db.ListShopSuspensionApprovalsParams) ([]db.ListShopSuspensionApprovalsRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var rows []db.ListShopSuspensionApprovalsRow
	for _, a := range m.shopSuspensionApprovals {
		if arg.Status != "" && a.Status != arg.Status {
			continue
		}
		if arg.ShopID != nil && a.ShopID != *arg.ShopID {
			continue
		}
		sh, ok := m.shops[a.ShopID]
		if !ok {
			continue
		}
		rows = append(rows, db.ListShopSuspensionApprovalsRow{
			ID:            a.ID,
			ShopID:        a.ShopID,
			Status:        a.Status,
			Reason:        a.Reason,
			RequestedBy:   a.RequestedBy,
			RequestedRole: a.RequestedRole,
			DecidedBy:     a.DecidedBy,
			DecidedRole:   a.DecidedRole,
			DecidedNote:   a.DecidedNote,
			CreatedAt:     a.CreatedAt,
			DecidedAt:     a.DecidedAt,
			ShopName:      sh.Name,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].CreatedAt.After(rows[j].CreatedAt)
		}
		return rows[i].ID > rows[j].ID
	})
	offset := int(arg.PageOffset)
	if offset < 0 {
		offset = 0
	}
	if offset >= len(rows) {
		return []db.ListShopSuspensionApprovalsRow{}, nil
	}
	end := len(rows)
	if arg.PageLimit > 0 && offset+int(arg.PageLimit) < end {
		end = offset + int(arg.PageLimit)
	}
	return rows[offset:end], nil
}

func (m *memStore) CountShopSuspensionApprovals(_ context.Context, arg db.CountShopSuspensionApprovalsParams) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var total int64
	for _, a := range m.shopSuspensionApprovals {
		if arg.Status != "" && a.Status != arg.Status {
			continue
		}
		if arg.ShopID != nil && a.ShopID != *arg.ShopID {
			continue
		}
		total++
	}
	return total, nil
}

func (m *memStore) InsertUserActivityLog(_ context.Context, arg db.InsertUserActivityLogParams) (db.UserActivityLog, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := db.UserActivityLog(arg)
	m.userActivityLogs = append(m.userActivityLogs, l)
	return l, nil
}

func (m *memStore) ListUserActivityLogs(_ context.Context, arg db.ListUserActivityLogsParams) ([]db.UserActivityLog, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var rows []db.UserActivityLog
	for _, l := range m.userActivityLogs {
		if l.UserID != arg.UserID {
			continue
		}
		if arg.Category != "" && l.Category != arg.Category {
			continue
		}
		if arg.FromTime != nil && l.CreatedAt.Before(*arg.FromTime) {
			continue
		}
		if arg.ToTime != nil && l.CreatedAt.After(*arg.ToTime) {
			continue
		}
		rows = append(rows, l)
	}
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].CreatedAt.After(rows[j].CreatedAt)
		}
		return rows[i].ID > rows[j].ID
	})
	offset := int(arg.PageOffset)
	if offset < 0 {
		offset = 0
	}
	if offset >= len(rows) {
		return []db.UserActivityLog{}, nil
	}
	end := len(rows)
	if arg.PageLimit > 0 && offset+int(arg.PageLimit) < end {
		end = offset + int(arg.PageLimit)
	}
	return rows[offset:end], nil
}

func (m *memStore) CountUserActivityLogs(_ context.Context, arg db.CountUserActivityLogsParams) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var total int64
	for _, l := range m.userActivityLogs {
		if l.UserID != arg.UserID {
			continue
		}
		if arg.Category != "" && l.Category != arg.Category {
			continue
		}
		if arg.FromTime != nil && l.CreatedAt.Before(*arg.FromTime) {
			continue
		}
		if arg.ToTime != nil && l.CreatedAt.After(*arg.ToTime) {
			continue
		}
		total++
	}
	return total, nil
}

func (m *memStore) DeleteExpiredUserActivityLogs(_ context.Context, arg db.DeleteExpiredUserActivityLogsParams) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var expiredIdx []int
	for i, l := range m.userActivityLogs {
		if l.CreatedAt.Before(arg.Cutoff) {
			expiredIdx = append(expiredIdx, i)
		}
	}
	sort.Slice(expiredIdx, func(i, j int) bool {
		return m.userActivityLogs[expiredIdx[i]].CreatedAt.Before(m.userActivityLogs[expiredIdx[j]].CreatedAt)
	})
	limit := int(arg.BatchLimit)
	if limit > 0 && len(expiredIdx) > limit {
		expiredIdx = expiredIdx[:limit]
	}
	toDelete := make(map[int]struct{}, len(expiredIdx))
	for _, idx := range expiredIdx {
		toDelete[idx] = struct{}{}
	}
	kept := make([]db.UserActivityLog, 0, len(m.userActivityLogs)-len(toDelete))
	for i, l := range m.userActivityLogs {
		if _, drop := toDelete[i]; !drop {
			kept = append(kept, l)
		}
	}
	m.userActivityLogs = kept
	return int64(len(toDelete)), nil
}

func (m *memStore) InsertOutboxEvent(_ context.Context, arg db.InsertOutboxEventParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.outboxSeq++
	m.outboxEvents = append(m.outboxEvents, db.OutboxEvent{
		ID:          arg.ID,
		Seq:         m.outboxSeq,
		AggregateID: arg.AggregateID,
		EventType:   arg.EventType,
		TraceID:     arg.TraceID,
		Traceparent: arg.Traceparent,
		PayloadJSON: append([]byte(nil), arg.PayloadJSON...),
		CreatedAt:   time.Now().UTC(),
	})
	return nil
}

func (m *memStore) ClaimPendingOutboxEvents(_ context.Context, arg db.ClaimPendingOutboxEventsParams) ([]db.OutboxEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	until := now.Add(time.Duration(arg.LeaseMs) * time.Millisecond)
	var claimed []db.OutboxEvent
	for i := range m.outboxEvents {
		ev := &m.outboxEvents[i]
		if ev.PublishedAt != nil {
			continue
		}
		if ev.ClaimedUntil != nil && ev.ClaimedUntil.After(now) {
			continue
		}
		ev.ClaimedUntil = &until
		ev.ClaimToken = arg.ClaimToken
		claimed = append(claimed, *ev)
		if arg.BatchLimit > 0 && int32(len(claimed)) >= arg.BatchLimit {
			break
		}
	}
	return claimed, nil
}

func (m *memStore) MarkOutboxEventPublished(_ context.Context, arg db.MarkOutboxEventPublishedParams) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	for i := range m.outboxEvents {
		ev := &m.outboxEvents[i]
		if ev.ID == arg.ID && ev.PublishedAt == nil && ev.ClaimToken == arg.ClaimToken {
			ev.PublishedAt = &now
			ev.Attempts++
			ev.LastError = ""
			ev.ClaimedUntil = nil
			ev.ClaimToken = ""
			return 1, nil
		}
	}
	return 0, nil
}

func (m *memStore) MarkOutboxEventAttemptFailed(_ context.Context, arg db.MarkOutboxEventAttemptFailedParams) (int32, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.outboxEvents {
		ev := &m.outboxEvents[i]
		if ev.ID == arg.ID && ev.PublishedAt == nil && ev.ClaimToken == arg.ClaimToken {
			ev.Attempts++
			ev.LastError = arg.LastError
			ev.ClaimedUntil = nil
			ev.ClaimToken = ""
			return ev.Attempts, nil
		}
	}
	return 0, pgx.ErrNoRows
}

func (m *memStore) ReleaseOutboxEventClaims(_ context.Context, arg db.ReleaseOutboxEventClaimsParams) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	idSet := make(map[string]struct{}, len(arg.Ids))
	for _, id := range arg.Ids {
		idSet[id] = struct{}{}
	}
	var released int64
	for i := range m.outboxEvents {
		ev := &m.outboxEvents[i]
		if _, ok := idSet[ev.ID]; ok && ev.PublishedAt == nil && ev.ClaimToken == arg.ClaimToken {
			ev.ClaimedUntil = nil
			ev.ClaimToken = ""
			released++
		}
	}
	return released, nil
}

type memOTP struct {
	code      string
	attempts  int
	expiresAt time.Time
}

type memPwdResetRate struct {
	count     int
	expiresAt time.Time
}

type memPhoneOTP struct {
	phone        string
	profilePhone string
	code         string
	attempts     int
	expiresAt    time.Time
}

type memPhoneRate struct {
	count     int
	expiresAt time.Time
}

type memSecurity struct {
	mu                   sync.Mutex
	otps                 map[string]memOTP
	resends              map[string]int
	phoneOTPs            map[string]memPhoneOTP
	phoneCooldowns       map[string]time.Time
	phoneHourly          map[string]memPhoneRate
	pwdResetReqs         map[string]memPwdResetRate
	loginFails           map[string]int
	loginLocks           map[string]time.Time
	revokedJTIs          map[string]bool
	revokedBefore        map[string]int64
	revokedBeforeTTL     map[string]time.Duration
	nowFn                func() time.Time
	setRevokedBeforeErr  error
	isTokenRevokedErr    error
	passwordResetRateErr error
	savePhoneOTPErr      error
	verifyPhoneOTPErr    error
	deletePhoneOTPErr    error
	onGetRevokedBefore   func(userID string)
}

func newMemSecurity() *memSecurity {
	return &memSecurity{
		otps:             make(map[string]memOTP),
		resends:          make(map[string]int),
		phoneOTPs:        make(map[string]memPhoneOTP),
		phoneCooldowns:   make(map[string]time.Time),
		phoneHourly:      make(map[string]memPhoneRate),
		pwdResetReqs:     make(map[string]memPwdResetRate),
		loginFails:       make(map[string]int),
		loginLocks:       make(map[string]time.Time),
		revokedJTIs:      make(map[string]bool),
		revokedBefore:    make(map[string]int64),
		revokedBeforeTTL: make(map[string]time.Duration),
	}
}

func (s *memSecurity) now() time.Time {
	if s.nowFn != nil {
		return s.nowFn().UTC()
	}
	return time.Now().UTC()
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

func (s *memSecurity) SavePhoneOTP(
	ctx context.Context,
	userID, phone, profilePhone, code string,
	ttl, cooldown time.Duration,
	maxPerHour int,
	window time.Duration,
) (time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.savePhoneOTPErr != nil {
		return 0, s.savePhoneOTPErr
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	if maxPerHour <= 0 {
		maxPerHour = 5
	}
	if window <= 0 {
		window = time.Hour
	}
	k := strings.TrimSpace(userID)
	now := s.now()
	if exp, ok := s.phoneCooldowns[k]; ok && now.Before(exp) {
		return exp.Sub(now), security.ErrPhoneOTPCooldown
	}
	rate, ok := s.phoneHourly[k]
	if !ok || !now.Before(rate.expiresAt) {
		rate = memPhoneRate{count: 0, expiresAt: now.Add(window)}
	}
	if rate.count >= maxPerHour {
		return rate.expiresAt.Sub(now), security.ErrPhoneOTPRateLimitReached
	}
	rate.count++
	s.phoneHourly[k] = rate
	s.phoneOTPs[k] = memPhoneOTP{
		phone:        strings.TrimSpace(phone),
		profilePhone: strings.TrimSpace(profilePhone),
		code:         strings.TrimSpace(code),
		attempts:     0,
		expiresAt:    now.Add(ttl),
	}
	if cooldown > 0 {
		s.phoneCooldowns[k] = now.Add(cooldown)
	}
	return 0, nil
}

func (s *memSecurity) VerifyPhoneOTP(ctx context.Context, userID, phone, profilePhone, code string, maxAttempts int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.verifyPhoneOTPErr != nil {
		return s.verifyPhoneOTPErr
	}
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	k := strings.TrimSpace(userID)
	now := s.now()
	rec, ok := s.phoneOTPs[k]
	if !ok || !now.Before(rec.expiresAt) {
		delete(s.phoneOTPs, k)
		return security.ErrOTPNotFound
	}
	want := rec.phone + "\x00" + rec.profilePhone + "\x00" + rec.code
	got := strings.TrimSpace(phone) + "\x00" + strings.TrimSpace(profilePhone) + "\x00" + strings.TrimSpace(code)
	if subtle.ConstantTimeCompare([]byte(want), []byte(got)) == 1 {
		delete(s.phoneOTPs, k)
		return nil
	}
	rec.attempts++
	if rec.attempts >= maxAttempts {
		delete(s.phoneOTPs, k)
		return security.ErrOTPTooManyAttempts
	}
	s.phoneOTPs[k] = rec
	return security.ErrOTPInvalid
}

func (s *memSecurity) RollbackPhoneOTP(ctx context.Context, userID, phone, profilePhone, code string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := strings.TrimSpace(userID)
	rec, ok := s.phoneOTPs[k]
	if !ok {
		return nil
	}
	want := rec.phone + "\x00" + rec.profilePhone + "\x00" + rec.code
	got := strings.TrimSpace(phone) + "\x00" + strings.TrimSpace(profilePhone) + "\x00" + strings.TrimSpace(code)
	if subtle.ConstantTimeCompare([]byte(want), []byte(got)) != 1 {
		return nil
	}
	delete(s.phoneOTPs, k)
	delete(s.phoneCooldowns, k)
	if rate, exists := s.phoneHourly[k]; exists {
		rate.count--
		if rate.count <= 0 {
			delete(s.phoneHourly, k)
		} else {
			s.phoneHourly[k] = rate
		}
	}
	return nil
}

func (s *memSecurity) DeletePhoneOTP(ctx context.Context, userID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deletePhoneOTPErr != nil {
		return s.deletePhoneOTPErr
	}
	delete(s.phoneOTPs, strings.TrimSpace(userID))
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

func (s *memSecurity) IncrementPasswordResetRequest(_ context.Context, email string, maxRequests int, window time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.passwordResetRateErr != nil {
		return s.passwordResetRateErr
	}
	if maxRequests <= 0 {
		maxRequests = 3
	}
	if window <= 0 {
		window = time.Hour
	}
	k := normalizeEmail(email)
	now := s.now()
	rec, ok := s.pwdResetReqs[k]
	if !ok || !now.Before(rec.expiresAt) {
		rec = memPwdResetRate{count: 0, expiresAt: now.Add(window)}
	}
	rec.count++
	s.pwdResetReqs[k] = rec
	if rec.count > maxRequests {
		return security.ErrPasswordResetLimitReached
	}
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

func (s *memSecurity) SetRevokedBefore(_ context.Context, userID string, unixSec int64, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.setRevokedBeforeErr != nil {
		return s.setRevokedBeforeErr
	}
	s.revokedBefore[userID] = unixSec
	s.revokedBeforeTTL[userID] = ttl
	return nil
}

func (s *memSecurity) GetRevokedBefore(_ context.Context, userID string) (int64, error) {
	s.mu.Lock()
	hook := s.onGetRevokedBefore
	s.mu.Unlock()
	if hook != nil {
		hook(userID)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.revokedBefore[userID], nil
}

func (s *memSecurity) IsTokenRevoked(_ context.Context, userID, jti string, issuedAt *time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.isTokenRevokedErr != nil {
		return false, s.isTokenRevokedErr
	}
	if jti != "" && s.revokedJTIs[jti] {
		return true, nil
	}
	if ts, ok := s.revokedBefore[userID]; ok {
		if issuedAt == nil {
			return true, nil
		}
		return issuedAt.Unix() <= ts, nil
	}
	return false, nil
}

func (s *memSecurity) Ping(_ context.Context) error { return nil }

type memPublisher struct {
	mu                      sync.Mutex
	userRegistered          []events.UserRegisteredPayload
	passwordResetRequested  []events.PasswordResetRequestedPayload
	shopReviewed            []events.ShopReviewedPayload
	shopBankAccountOTP      []events.ShopBankAccountOTPPayload
	phoneOTPRequested       []events.PhoneOTPRequestedPayload
	publishPasswordResetErr error
	publishBankOTPErr       error
	publishPhoneOTPErr      error
	onPublishBankOTP        func(ctx context.Context) error
	onPublishPhoneOTP       func(ctx context.Context, payload events.PhoneOTPRequestedPayload) error
}

func (p *memPublisher) PublishUserRegistered(_ context.Context, payload events.UserRegisteredPayload) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.userRegistered = append(p.userRegistered, payload)
	return nil
}

func (p *memPublisher) PublishPasswordResetRequested(_ context.Context, payload events.PasswordResetRequestedPayload) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.publishPasswordResetErr != nil {
		return p.publishPasswordResetErr
	}
	p.passwordResetRequested = append(p.passwordResetRequested, payload)
	return nil
}

func (p *memPublisher) PublishShopReviewed(_ context.Context, payload events.ShopReviewedPayload) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.shopReviewed = append(p.shopReviewed, payload)
	return nil
}

func (p *memPublisher) PublishShopBankAccountOTP(ctx context.Context, payload events.ShopBankAccountOTPPayload) error {
	p.mu.Lock()
	hook := p.onPublishBankOTP
	pubErr := p.publishBankOTPErr
	p.mu.Unlock()
	if hook != nil {
		if err := hook(ctx); err != nil {
			return err
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if pubErr != nil {
		return pubErr
	}
	p.shopBankAccountOTP = append(p.shopBankAccountOTP, payload)
	return nil
}

func (p *memPublisher) PublishPhoneOTPRequested(ctx context.Context, payload events.PhoneOTPRequestedPayload) error {
	p.mu.Lock()
	hook := p.onPublishPhoneOTP
	pubErr := p.publishPhoneOTPErr
	p.mu.Unlock()
	if hook != nil {
		if err := hook(ctx, payload); err != nil {
			return err
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if pubErr != nil {
		return pubErr
	}
	p.phoneOTPRequested = append(p.phoneOTPRequested, payload)
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
		Name:               "Tech Store VN",
		Description:        "Electronics",
		PickupAddressId:    addrResp.GetAddress().GetId(),
		ContactPhone:       "0944444444",
		BusinessLicenseDoc: "GPKD-TECH-001.pdf",
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
	// Re-verifying OTP on already active user must return generic InvalidArgument (USR-B8 anti-enumeration).
	if _, err := srv.VerifyOTP(ctx, &userv1.VerifyOTPRequest{Email: "revoke@example.com", Otp: otp}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument on VerifyOTP for active user, got %v", err)
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

	addPickup := func(ownerID, phone string) string {
		resp, err := srv.CreateAddress(ctxWithUser(ownerID, RoleBuyer, ""), &userv1.CreateAddressRequest{
			RecipientName:  "Kho Hang",
			RecipientPhone: phone,
			ProvinceCode:   "79",
			WardCode:       "79001",
			StreetAddress:  "100 Le Loi",
			IsDefault:      true,
		})
		if err != nil {
			t.Fatalf("CreateAddress for %s: %v", ownerID, err)
		}
		return resp.GetAddress().GetId()
	}
	pickup1 := addPickup(owner1, "0971111111")
	pickup2 := addPickup(owner2, "0972222222")
	pickup3 := addPickup(owner3, "0973333333")

	// 1. Auto-generated Vietnamese slug from shop name.
	sh1Resp, err := srv.RegisterShop(ctxWithUser(owner1, RoleBuyer, ""), &userv1.RegisterShopRequest{
		Name:               "Xưởng Dệt Bạch Đằng",
		Description:        "Cotton compact 220gsm",
		PickupAddressId:    pickup1,
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
		PickupAddressId:    pickup2,
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
		Name:               "Xưởng Dệt Mới",
		Slug:               "xuong-det-song-hong",
		PickupAddressId:    pickup3,
		ContactPhone:       "0973333333",
		BusinessLicenseDoc: "GPKD-SECRET-003.pdf",
	}); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("expected AlreadyExists on duplicate shop slug, got %v", err)
	}
	if _, err := srv.RegisterShop(ctxWithUser(owner3, RoleBuyer, ""), &userv1.RegisterShopRequest{
		Name:               "Xưởng Dệt Lỗi",
		Slug:               "!!!",
		PickupAddressId:    pickup3,
		ContactPhone:       "0973333333",
		BusinessLicenseDoc: "GPKD-SECRET-003.pdf",
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
