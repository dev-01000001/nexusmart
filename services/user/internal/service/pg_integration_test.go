package service

import (
	"context"
	"io"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/user/internal/db"
	"github.com/nexusmart/core/services/user/internal/password"
	"github.com/nexusmart/core/services/user/migrations"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func applyPre0005Migrations(ctx context.Context, t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
	version TEXT PRIMARY KEY,
	applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	preVersions := []string{
		"0001_init.sql",
		"0002_add_shop_slug.sql",
		"0003_cod_rejections.sql",
		"0004_password_resets_and_lock_reason.sql",
	}
	for _, v := range preVersions {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = $1)`, v).Scan(&exists); err != nil {
			t.Fatalf("check migration %s: %v", v, err)
		}
		if exists {
			continue
		}
		sqlBytes, err := fs.ReadFile(migrations.Files, v)
		if err != nil {
			t.Fatalf("read migration %s: %v", v, err)
		}
		if _, err := pool.Exec(ctx, string(sqlBytes)); err != nil {
			t.Fatalf("exec migration %s: %v", v, err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, v); err != nil {
			t.Fatalf("record migration %s: %v", v, err)
		}
	}
}

func newPGTestServer(t *testing.T, pool *pgxpool.Pool, opts Options) (*Server, *db.SQLStore, *memPublisher) {
	t.Helper()
	pgStore := db.NewSQLStore(pool)
	sec := newMemSecurity()
	pub := &memPublisher{}
	hasher := password.NewDefaultHasher()
	authMgr, err := auth.NewManager("test-secret-key-hs256", 15*time.Minute)
	if err != nil {
		t.Fatalf("new auth manager: %v", err)
	}
	if opts.BankAccountBox == nil {
		opts.BankAccountBox = newTestBankAccountBox(t)
	}
	log := logger.New("user-pg-test", logger.WithWriter(io.Discard))
	srv := NewServer(pgStore, sec, hasher, authMgr, pub, log, opts)
	return srv, pgStore, pub
}

// TestPGStore_ShopBankAccountSQLAndMigrations runs real PostgreSQL tests when TEST_USER_DB_URL is set.
// It validates migrations 0005 & 0006 on a pre-populated DB, real SQL for GetLatestShopBankAccountByShopID
// (admin vs seller + expired PENDING_OTP filtering), MarkShopBankAccountPublishFailed with cancelled ctx,
// SupersedePendingAdminReviewShopBankAccounts, and partial unique indexes.
func TestPGStore_ShopBankAccountSQLAndMigrations(t *testing.T) {
	if strings.TrimSpace(os.Getenv("TEST_USER_DB_URL")) == "" {
		t.Skip("set TEST_USER_DB_URL to run real Postgres integration tests")
	}

	ctx := context.Background()
	pool, _ := openTempUserPGPool(t)

	// 1. Seed pre-0005 data (after 0001..0004), then run 0005 and 0006 via db.RunMigrations.
	t.Run("Migrations0005And0006OnPopulatedDB", func(t *testing.T) {
		applyPre0005Migrations(ctx, t, pool)

		seedPre0005UserAndShop := func(fullName, email, role, shopStatus string) (string, string) {
			uid := uuid.NewString()
			sid := uuid.NewString()
			if _, err := pool.Exec(ctx, `
				INSERT INTO users (id, full_name, email, phone, password_hash, role, status, shop_id)
				VALUES ($1, $2, $3, $4, 'hash', $5, 'ACTIVE', $6)
			`, uid, fullName, email, "090"+strings.ReplaceAll(uid[:7], "-", "2"), role, sid); err != nil {
				t.Fatalf("insert pre-0005 user %s: %v", email, err)
			}
			if _, err := pool.Exec(ctx, `
				INSERT INTO shops (id, owner_user_id, name, slug, status)
				VALUES ($1, $2, $3, $4, $5)
			`, sid, uid, "PreShop "+uid[:8], "preshop-"+uid[:8], shopStatus); err != nil {
				t.Fatalf("insert pre-0005 shop %s: %v", shopStatus, err)
			}
			return uid, sid
		}

		_, activeShopID := seedPre0005UserAndShop("Nguyễn Văn Hoạt Động", "pg-pre-act-"+uuid.NewString()[:6]+"@example.com", RoleSeller, ShopStatusActive)
		_, suspendedShopID := seedPre0005UserAndShop("Trần Thị Tạm Ngưng", "pg-pre-sus-"+uuid.NewString()[:6]+"@example.com", RoleSeller, ShopStatusSuspended)
		_, pendingShopID := seedPre0005UserAndShop("Lê Văn Chờ", "pg-pre-pen-"+uuid.NewString()[:6]+"@example.com", RoleBuyer, ShopStatusPending)
		_, rejectedShopID := seedPre0005UserAndShop("Phạm Văn Từ Chối", "pg-pre-rej-"+uuid.NewString()[:6]+"@example.com", RoleBuyer, ShopStatusRejected)

		// Apply remaining migrations (0005 and 0006) on populated DB, then re-run for idempotency.
		if err := db.RunMigrations(ctx, pool); err != nil {
			t.Fatalf("db.RunMigrations (first): %v", err)
		}
		if err := db.RunMigrations(ctx, pool); err != nil {
			t.Fatalf("db.RunMigrations (idempotent second): %v", err)
		}

		checkVerifiedOwner := func(shopID, want string) {
			t.Helper()
			var got string
			if err := pool.QueryRow(ctx, `SELECT verified_owner_name FROM shops WHERE id = $1`, shopID).Scan(&got); err != nil {
				t.Fatalf("query verified_owner_name for %s: %v", shopID, err)
			}
			if got != want {
				t.Fatalf("shop %s verified_owner_name: want %q, got %q", shopID, want, got)
			}
		}
		checkVerifiedOwner(activeShopID, "Nguyễn Văn Hoạt Động")
		checkVerifiedOwner(suspendedShopID, "Trần Thị Tạm Ngưng")
		checkVerifiedOwner(pendingShopID, "")
		checkVerifiedOwner(rejectedShopID, "")

		// Verify real Postgres partial unique index idx_shop_bank_accounts_one_verified rejects a second VERIFIED row.
		if _, err := pool.Exec(ctx, `
			INSERT INTO shop_bank_accounts (id, shop_id, bank_code, bank_name, account_number, account_number_masked, account_holder_name, status)
			VALUES ($1, $2, 'VCB', 'Vietcombank', '19000000001111', '****1111', 'NGUYEN VAN HOAT DONG', 'VERIFIED')
		`, uuid.NewString(), activeShopID); err != nil {
			t.Fatalf("insert first VERIFIED row: %v", err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO shop_bank_accounts (id, shop_id, bank_code, bank_name, account_number, account_number_masked, account_holder_name, status)
			VALUES ($1, $2, 'TCB', 'Techcombank', '19000000002222', '****2222', 'NGUYEN VAN HOAT DONG', 'VERIFIED')
		`, uuid.NewString(), activeShopID); !isUniqueViolation(err) {
			t.Fatalf("expected unique violation on second VERIFIED bank account, got %v", err)
		}
	})

	// Ensure migrations are applied even if subtest 1 is skipped via -run filter.
	if err := db.RunMigrations(ctx, pool); err != nil {
		t.Fatalf("ensure migrations: %v", err)
	}

	insertPGUserAndActiveShop := func(t *testing.T, fullName string, createdAt time.Time) (string, string) {
		t.Helper()
		uid := uuid.NewString()
		sid := uuid.NewString()
		if _, err := pool.Exec(ctx, `
			INSERT INTO users (id, full_name, email, phone, password_hash, role, status, shop_id, created_at, updated_at)
			VALUES ($1, $2, $3, $4, 'hash', 'seller', 'ACTIVE', $5, $6, $6)
		`, uid, fullName, "pg-"+uid[:8]+"@example.com", "091"+strings.ReplaceAll(uid[:7], "-", "3"), sid, createdAt); err != nil {
			t.Fatalf("insert pg user: %v", err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO shops (id, owner_user_id, name, slug, status, verified_owner_name, created_at, updated_at)
			VALUES ($1, $2, $3, $4, 'ACTIVE', $5, $6, $6)
		`, sid, uid, "PGShop "+uid[:8], "pgshop-"+uid[:8], fullName, createdAt); err != nil {
			t.Fatalf("insert pg shop: %v", err)
		}
		return uid, sid
	}

	adminID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, full_name, email, phone, password_hash, role, status)
		VALUES ($1, 'PG Admin', $2, '', 'hash', 'admin', 'ACTIVE')
	`, adminID, "pg-admin-"+adminID[:8]+"@example.com"); err != nil {
		t.Fatalf("insert pg admin: %v", err)
	}
	adminCtx := ctxWithUser(adminID, RoleAdmin, "")

	// 2. Real SQL for GetLatestShopBankAccountByShopID (admin vs seller + expired PENDING_OTP filtering).
	t.Run("RealPG_GetLatestShopBankAccountByShopID_AdminVsSellerAndExpiry", func(t *testing.T) {
		clk := newW2BClock(time.Date(2026, 11, 23, 19, 0, 0, 0, time.UTC))
		srv, _, pub := newPGTestServer(t, pool, Options{Now: clk.Now, OTPTTL: 10 * time.Minute})

		owner1, shop1 := insertPGUserAndActiveShop(t, "Nguyễn Văn Postgres", clk.Now())
		owner2, shop2 := insertPGUserAndActiveShop(t, "Trần Thị Hết Hạn PG", clk.Now())
		seller1Ctx := ctxWithUser(owner1, RoleSeller, shop1)
		seller2Ctx := ctxWithUser(owner2, RoleSeller, shop2)

		// A1: mismatched -> PENDING_ADMIN_REVIEW.
		addA1, err := srv.AddShopBankAccount(seller1Ctx, &userv1.AddShopBankAccountRequest{
			BankCode: "VCB", BankName: "Vietcombank", AccountNumber: "19034567898101", AccountHolderName: "CONG TY PG A1",
		})
		if err != nil {
			t.Fatalf("Add A1: %v", err)
		}
		if _, err := srv.VerifyShopBankAccountOTP(seller1Ctx, &userv1.VerifyShopBankAccountOTPRequest{
			Otp: pub.shopBankAccountOTP[len(pub.shopBankAccountOTP)-1].OTP,
		}); err != nil {
			t.Fatalf("Verify A1: %v", err)
		}

		// A2: new unexpired PENDING_OTP while A1 is PENDING_ADMIN_REVIEW.
		clk.Advance(time.Minute)
		addA2, err := srv.AddShopBankAccount(seller1Ctx, &userv1.AddShopBankAccountRequest{
			BankCode: "TCB", BankName: "Techcombank", AccountNumber: "19034567898102", AccountHolderName: "NGUYEN VAN POSTGRES",
		})
		if err != nil {
			t.Fatalf("Add A2: %v", err)
		}

		// Admin sees A1 (PENDING_ADMIN_REVIEW), Seller sees A2 (unexpired PENDING_OTP).
		gotAdm, err := srv.GetShopBankAccount(adminCtx, &userv1.GetShopBankAccountRequest{ShopId: shop1})
		if err != nil || gotAdm.GetBankAccount().GetId() != addA1.GetBankAccount().GetId() ||
			gotAdm.GetBankAccount().GetStatus() != userv1.ShopBankAccountStatus_SHOP_BANK_ACCOUNT_STATUS_PENDING_ADMIN_REVIEW {
			t.Fatalf("real PG admin GetShopBankAccount: want A1 PENDING_ADMIN_REVIEW, got err=%v acc=%+v", err, gotAdm)
		}
		gotSel, err := srv.GetShopBankAccount(seller1Ctx, &userv1.GetShopBankAccountRequest{})
		if err != nil || gotSel.GetBankAccount().GetId() != addA2.GetBankAccount().GetId() ||
			gotSel.GetBankAccount().GetStatus() != userv1.ShopBankAccountStatus_SHOP_BANK_ACCOUNT_STATUS_PENDING_OTP {
			t.Fatalf("real PG seller GetShopBankAccount: want A2 PENDING_OTP, got err=%v acc=%+v", err, gotSel)
		}

		// Advance fake clock to exact expiry boundary (otp_expires_at == s.now()):
		// real Postgres WHERE excludes expired A2 (using passed-in s.now(), NOT Postgres NOW()).
		clk.Advance(10 * time.Minute)
		gotSelExp, err := srv.GetShopBankAccount(seller1Ctx, &userv1.GetShopBankAccountRequest{})
		if err != nil || gotSelExp.GetBankAccount().GetId() != addA1.GetBankAccount().GetId() {
			t.Fatalf("real PG seller GetShopBankAccount after expiry: want fallback A1, got err=%v acc=%+v", err, gotSelExp)
		}
		gotAdmExp, err := srv.GetShopBankAccount(adminCtx, &userv1.GetShopBankAccountRequest{ShopId: shop1})
		if err != nil || gotAdmExp.GetBankAccount().GetId() != addA1.GetBankAccount().GetId() {
			t.Fatalf("real PG admin GetShopBankAccount after expiry: want A1, got err=%v acc=%+v", err, gotAdmExp)
		}

		// Shop2: PENDING_OTP with NULL otp_expires_at must ALSO be excluded by real SQL.
		if _, err := pool.Exec(ctx, `
			INSERT INTO shop_bank_accounts (id, shop_id, bank_code, bank_name, account_number, account_number_masked, account_holder_name, status, otp_expires_at, created_at, updated_at)
			VALUES ($1, $2, 'MB', 'MB Bank', '19034567898103', '****8103', 'TRAN THI HET HAN PG', 'PENDING_OTP', NULL, $3, $3)
		`, uuid.NewString(), shop2, clk.Now()); err != nil {
			t.Fatalf("insert NULL-expiry PENDING_OTP row: %v", err)
		}
		if _, err := srv.GetShopBankAccount(seller2Ctx, &userv1.GetShopBankAccountRequest{}); status.Code(err) != codes.NotFound {
			t.Fatalf("real PG seller with NULL otp_expires_at PENDING_OTP: want NotFound, got %v", err)
		}
		if _, err := srv.GetShopBankAccount(adminCtx, &userv1.GetShopBankAccountRequest{ShopId: shop2}); status.Code(err) != codes.NotFound {
			t.Fatalf("real PG admin with NULL otp_expires_at PENDING_OTP: want NotFound, got %v", err)
		}
	})

	// 3. Real SQL for MarkShopBankAccountPublishFailed (with cancelled ctx), CountShopBankAccountsCreatedSince,
	// SupersedePendingAdminReviewShopBankAccounts, and AdminListShops(bank_review_pending_only).
	t.Run("RealPG_PublishFailCancelledCtxAndSupersedePendingAdminReview", func(t *testing.T) {
		clk := newW2BClock(time.Date(2026, 11, 23, 20, 0, 0, 0, time.UTC))
		srv, pgStore, pub := newPGTestServer(t, pool, Options{Now: clk.Now, OTPTTL: 10 * time.Minute})

		owner, shopID := insertPGUserAndActiveShop(t, "Lê Minh Postgres", clk.Now())
		sellerCtx := ctxWithUser(owner, RoleSeller, shopID)

		// A1: mismatched -> PENDING_ADMIN_REVIEW (1 successful email).
		addA1, err := srv.AddShopBankAccount(sellerCtx, &userv1.AddShopBankAccountRequest{
			BankCode: "VCB", BankName: "Vietcombank", AccountNumber: "19034567898201", AccountHolderName: "CONG TY PG B1",
		})
		if err != nil {
			t.Fatalf("Add A1: %v", err)
		}
		if _, err := srv.VerifyShopBankAccountOTP(sellerCtx, &userv1.VerifyShopBankAccountOTPRequest{
			Otp: pub.shopBankAccountOTP[len(pub.shopBankAccountOTP)-1].OTP,
		}); err != nil {
			t.Fatalf("Verify A1: %v", err)
		}

		// Publish failure with cancelled caller ctx -> cleanup still succeeds via context.WithoutCancel!
		cancelCtx, cancel := context.WithCancel(sellerCtx)
		pub.onPublishBankOTP = func(_ context.Context) error {
			cancel()
			return context.Canceled
		}
		clk.Advance(time.Minute)
		if _, err := srv.AddShopBankAccount(cancelCtx, &userv1.AddShopBankAccountRequest{
			BankCode: "TCB", BankName: "Techcombank", AccountNumber: "19034567898202", AccountHolderName: "LE MINH POSTGRES",
		}); status.Code(err) != codes.Internal {
			t.Fatalf("Add with cancelled publish ctx: want Internal, got %v", err)
		}
		pub.onPublishBankOTP = nil

		// Verify directly in Postgres: failed row is SUPERSEDED with otp_publish_failed=true,
		// A1 is still PENDING_ADMIN_REVIEW, and CountShopBankAccountsCreatedSince is still 1.
		var failedStatus string
		var publishFailed bool
		if err := pool.QueryRow(ctx, `
			SELECT status, otp_publish_failed FROM shop_bank_accounts
			WHERE shop_id = $1 AND id <> $2
			ORDER BY created_at DESC LIMIT 1
		`, shopID, addA1.GetBankAccount().GetId()).Scan(&failedStatus, &publishFailed); err != nil {
			t.Fatalf("query failed publish row: %v", err)
		}
		if failedStatus != BankAccountStatusSuperseded || !publishFailed {
			t.Fatalf("failed publish row: want SUPERSEDED + otp_publish_failed=true, got %q / %v", failedStatus, publishFailed)
		}

		cnt, err := pgStore.CountShopBankAccountsCreatedSince(ctx, db.CountShopBankAccountsCreatedSinceParams{
			ShopID:    shopID,
			CreatedAt: clk.Now().Add(-time.Hour),
		})
		if err != nil || cnt != 1 {
			t.Fatalf("real PG CountShopBankAccountsCreatedSince: want 1, got cnt=%d err=%v", cnt, err)
		}

		// Now submit A3 (matching verified_owner_name) and verify OTP -> real SQL SupersedePendingAdminReviewShopBankAccounts
		// must transition A1 to SUPERSEDED and A3 to VERIFIED!
		clk.Advance(time.Minute)
		addA3, err := srv.AddShopBankAccount(sellerCtx, &userv1.AddShopBankAccountRequest{
			BankCode: "MB", BankName: "MB Bank", AccountNumber: "19034567898203", AccountHolderName: "LE MINH POSTGRES",
		})
		if err != nil {
			t.Fatalf("Add A3: %v", err)
		}
		verA3, err := srv.VerifyShopBankAccountOTP(sellerCtx, &userv1.VerifyShopBankAccountOTPRequest{
			Otp: pub.shopBankAccountOTP[len(pub.shopBankAccountOTP)-1].OTP,
		})
		if err != nil || verA3.GetBankAccount().GetStatus() != userv1.ShopBankAccountStatus_SHOP_BANK_ACCOUNT_STATUS_VERIFIED {
			t.Fatalf("Verify A3: want VERIFIED, got err=%v acc=%+v", err, verA3)
		}

		var a1Status, a3Status string
		if err := pool.QueryRow(ctx, `SELECT status FROM shop_bank_accounts WHERE id = $1`, addA1.GetBankAccount().GetId()).Scan(&a1Status); err != nil {
			t.Fatalf("query A1 status: %v", err)
		}
		if err := pool.QueryRow(ctx, `SELECT status FROM shop_bank_accounts WHERE id = $1`, addA3.GetBankAccount().GetId()).Scan(&a3Status); err != nil {
			t.Fatalf("query A3 status: %v", err)
		}
		if a1Status != BankAccountStatusSuperseded || a3Status != BankAccountStatusVerified {
			t.Fatalf("real PG SupersedePendingAdminReview mismatch: A1=%q (want SUPERSEDED) A3=%q (want VERIFIED)", a1Status, a3Status)
		}

		// Verify real SQL AdminListShops(bank_review_pending_only=true) no longer includes shopID.
		admList, err := srv.AdminListShops(adminCtx, &userv1.AdminListShopsRequest{
			BankReviewPendingOnly: true,
			Page:                  1,
			PageSize:              100,
		})
		if err != nil {
			t.Fatalf("real PG AdminListShops: %v", err)
		}
		for _, sh := range admList.GetShops() {
			if sh.GetId() == shopID {
				t.Fatalf("shop %s still appeared in real PG AdminListShops(bank_review_pending_only=true) after A1 superseded", shopID)
			}
		}

		// Verify real SQL GetPlatformUserStats runs cleanly without unused params/columns.
		statsResp, err := srv.GetPlatformUserStats(adminCtx, &userv1.GetPlatformUserStatsRequest{
			FromDate: "2026-11-23",
			ToDate:   "2026-11-23",
		})
		if err != nil || statsResp.GetTotalUsers() < 1 || statsResp.GetTotalShops() < 1 {
			t.Fatalf("real PG GetPlatformUserStats: err=%v resp=%+v", err, statsResp)
		}
	})
}
