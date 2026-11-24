package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"io/fs"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/pkg/common/secretbox"
	"github.com/nexusmart/core/services/user/internal/db"
	"github.com/nexusmart/core/services/user/internal/password"
	"github.com/nexusmart/core/services/user/migrations"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func openTempUserPGPool(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	adminDSN := strings.TrimSpace(os.Getenv("TEST_USER_DB_URL"))
	if adminDSN == "" {
		t.Skip("set TEST_USER_DB_URL to run real Postgres integration tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	parsed, err := url.Parse(adminDSN)
	if err != nil {
		t.Fatalf("parse TEST_USER_DB_URL: %v", err)
	}

	adminConn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatalf("connect admin postgres: %v", err)
	}

	tempDBName := "user_w3a_test_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	if _, err := adminConn.Exec(ctx, "CREATE DATABASE "+tempDBName); err != nil {
		_ = adminConn.Close(ctx)
		t.Fatalf("create temp database %s: %v", tempDBName, err)
	}
	_ = adminConn.Close(ctx)

	testURL := *parsed
	testURL.Path = "/" + tempDBName
	dsn := testURL.String()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		dropTempUserDatabase(adminDSN, tempDBName)
		t.Fatalf("open temp db pool %s: %v", tempDBName, err)
	}

	t.Cleanup(func() {
		pool.Close()
		dropTempUserDatabase(adminDSN, tempDBName)
	})

	return pool, dsn
}

func dropTempUserDatabase(adminDSN, dbName string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close(ctx) }()
	_, _ = conn.Exec(ctx, `
		SELECT pg_terminate_backend(pid)
		FROM pg_stat_activity
		WHERE datname = $1 AND pid <> pg_backend_pid()
	`, dbName)
	_, _ = conn.Exec(ctx, "DROP DATABASE IF EXISTS "+dbName)
}

func applyMigrationsUpTo0006(ctx context.Context, t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
	version TEXT PRIMARY KEY,
	applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	versions := []string{
		"0001_init.sql",
		"0002_add_shop_slug.sql",
		"0003_cod_rejections.sql",
		"0004_password_resets_and_lock_reason.sql",
		"0005_shop_bank_accounts.sql",
		"0006_shop_verified_owner_name_and_otp_publish.sql",
	}
	for _, v := range versions {
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

func insertPGUserShop(ctx context.Context, t *testing.T, pool *pgxpool.Pool, fullName, role, shopStatus string) (string, string) {
	t.Helper()
	uid := uuid.NewString()
	sid := uuid.NewString()
	var shopPtr *string
	if shopStatus == ShopStatusActive {
		shopPtr = &sid
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, full_name, email, phone, password_hash, role, status, shop_id)
		VALUES ($1, $2, $3, $4, 'hash', $5, 'ACTIVE', $6)
	`, uid, fullName, "w3a-"+uid[:8]+"@example.com", "090"+strings.ReplaceAll(uid[:7], "-", "5"), role, shopPtr); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	verifiedOwner := ""
	if shopStatus == ShopStatusActive {
		verifiedOwner = fullName
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO shops (id, owner_user_id, name, slug, status, verified_owner_name)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, sid, uid, "W3AShop "+uid[:8], "w3ashop-"+uid[:8], shopStatus, verifiedOwner); err != nil {
		t.Fatalf("insert shop: %v", err)
	}
	return uid, sid
}

func newPGServerWithLog(t *testing.T, pool *pgxpool.Pool, box *secretbox.Box, clk *w2bClock) (*Server, *memPublisher, *bytes.Buffer) {
	t.Helper()
	pgStore := db.NewSQLStore(pool)
	sec := newMemSecurity()
	pub := &memPublisher{}
	hasher := password.NewDefaultHasher()
	authMgr, err := auth.NewManager("test-secret-key-hs256", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	var logBuf bytes.Buffer
	log := logger.New("user-pg-w3a", logger.WithWriter(&logBuf))
	opts := Options{
		OTPTTL:                 10 * time.Minute,
		BankPayoutLockDuration: 24 * time.Hour,
		BankAccountBox:         box,
	}
	if clk != nil {
		opts.Now = clk.Now
	}
	return NewServer(pgStore, sec, hasher, authMgr, pub, log, opts), pub, &logBuf
}

func TestPG_W3A_D5_BankAccountEncryptionAndBackfill(t *testing.T) {
	if strings.TrimSpace(os.Getenv("TEST_USER_DB_URL")) == "" {
		t.Skip("set TEST_USER_DB_URL to run real Postgres integration tests")
	}

	box1 := newTestBankAccountBox(t)
	wrongKeyBytes := bytes.Repeat([]byte{0x7c}, 32)
	box2, err := secretbox.New(wrongKeyBytes, "BANK_ACCOUNT_KEY")
	if err != nil {
		t.Fatalf("secretbox.New box2: %v", err)
	}

	// 1. Old DB with plaintext rows -> migration 0007 + multi-batch backfill -> 0 plaintext rows, payout returns exact number.
	t.Run("LegacyPlaintextDB_StartupBackfillAndPayoutDecrypt", func(t *testing.T) {
		ctx := context.Background()
		pool, _ := openTempUserPGPool(t)
		applyMigrationsUpTo0006(ctx, t, pool)

		type legacySeed struct {
			rowID   string
			shopID  string
			ownerID string
			rawNum  string
			status  string
		}
		statuses := []string{
			BankAccountStatusVerified,
			BankAccountStatusPendingAdminReview,
			BankAccountStatusRejected,
			BankAccountStatusSuperseded,
			BankAccountStatusPendingOTP,
			BankAccountStatusVerified,
			BankAccountStatusSuperseded,
		}
		seeds := make([]legacySeed, 0, len(statuses))
		for i, st := range statuses {
			ownerID, shopID := insertPGUserShop(ctx, t, pool, "Nguyễn Văn Cũ", RoleSeller, ShopStatusActive)
			rowID := uuid.NewString()
			rawNum := "1903456789000" + string(rune('1'+i))
			masked := maskBankAccountNumber(rawNum)
			if _, err := pool.Exec(ctx, `
				INSERT INTO shop_bank_accounts (id, shop_id, bank_code, bank_name, account_number, account_number_masked, account_holder_name, holder_name_matched, status)
				VALUES ($1, $2, 'VCB', 'Vietcombank', $3, $4, 'NGUYEN VAN CU', TRUE, $5)
			`, rowID, shopID, rawNum, masked, st); err != nil {
				t.Fatalf("seed legacy plaintext row %d: %v", i, err)
			}
			seeds = append(seeds, legacySeed{rowID: rowID, shopID: shopID, ownerID: ownerID, rawNum: rawNum, status: st})
		}

		// Simulate startup: RunMigrations (applies 0007) + BackfillShopBankAccountEncryption with small batch size (2) across 7 rows.
		if err := db.RunMigrations(ctx, pool); err != nil {
			t.Fatalf("RunMigrations (0007): %v", err)
		}
		backfilled, err := db.BackfillShopBankAccountEncryption(ctx, pool, box1, 2)
		if err != nil {
			t.Fatalf("BackfillShopBankAccountEncryption: %v", err)
		}
		if backfilled != len(seeds) {
			t.Fatalf("backfilled count = %d, want %d", backfilled, len(seeds))
		}

		// Zero rows may retain plaintext account_number.
		var remainingPlain int64
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM shop_bank_accounts WHERE account_number <> ''`).Scan(&remainingPlain); err != nil {
			t.Fatalf("count remaining plaintext: %v", err)
		}
		if remainingPlain != 0 {
			t.Fatalf("expected 0 plaintext rows after backfill, got %d", remainingPlain)
		}

		// Every row has valid ciphertext (without plaintext bytes) that decrypts to original number.
		encSnapshots := make(map[string][]byte, len(seeds))
		for _, s := range seeds {
			var (
				plainCol string
				encCol   []byte
			)
			if err := pool.QueryRow(ctx, `SELECT account_number, account_number_enc FROM shop_bank_accounts WHERE id = $1`, s.rowID).Scan(&plainCol, &encCol); err != nil {
				t.Fatalf("query backfilled row %s: %v", s.rowID, err)
			}
			if plainCol != "" {
				t.Fatalf("row %s still has non-empty account_number", s.rowID)
			}
			if len(encCol) < 28 || bytes.Contains(encCol, []byte(s.rawNum)) {
				t.Fatalf("row %s invalid ciphertext (len=%d)", s.rowID, len(encCol))
			}
			dec, err := box1.Decrypt(encCol)
			if err != nil || dec != s.rawNum {
				t.Fatalf("row %s decrypt mismatch: err=%v", s.rowID, err)
			}
			encSnapshots[s.rowID] = append([]byte(nil), encCol...)
		}

		// Second backfill run MUST be idempotent: 0 rows updated, ciphertexts byte-for-byte unchanged!
		secondCount, err := db.BackfillShopBankAccountEncryption(ctx, pool, box1, 2)
		if err != nil || secondCount != 0 {
			t.Fatalf("second backfill: count=%d err=%v, want 0/nil", secondCount, err)
		}
		for _, s := range seeds {
			var encAfter []byte
			if err := pool.QueryRow(ctx, `SELECT account_number_enc FROM shop_bank_accounts WHERE id = $1`, s.rowID).Scan(&encAfter); err != nil {
				t.Fatalf("query row %s after second backfill: %v", s.rowID, err)
			}
			if !bytes.Equal(encSnapshots[s.rowID], encAfter) {
				t.Fatalf("second backfill re-encrypted row %s; expected idempotent no-op", s.rowID)
			}
		}

		// GetShopBankAccountForPayout returns decrypted number; GetShopBankAccount returns masked only.
		srv, _, _ := newPGServerWithLog(t, pool, box1, nil)
		verifiedSeed := seeds[0]
		payoutResp, err := srv.GetShopBankAccountForPayout(ctxWithService("payment"), &userv1.GetShopBankAccountForPayoutRequest{
			ShopId: verifiedSeed.shopID,
		})
		if err != nil || payoutResp.GetBankAccount().GetAccountNumber() != verifiedSeed.rawNum {
			t.Fatalf("GetShopBankAccountForPayout after backfill: err=%v resp=%+v", err, payoutResp)
		}
		selResp, err := srv.GetShopBankAccount(ctxWithUser(verifiedSeed.ownerID, RoleSeller, verifiedSeed.shopID), &userv1.GetShopBankAccountRequest{})
		if err != nil || selResp.GetBankAccount().GetAccountNumber() != "" || selResp.GetBankAccount().GetAccountNumberMasked() != maskBankAccountNumber(verifiedSeed.rawNum) {
			t.Fatalf("GetShopBankAccount must mask only: err=%v resp=%+v", err, selResp)
		}
	})

	// 2. Two concurrent instances running backfill simultaneously under advisory lock.
	t.Run("ConcurrentTwoInstancesBackfillSafe", func(t *testing.T) {
		ctx := context.Background()
		pool1, dsn := openTempUserPGPool(t)
		pool2, err := pgxpool.New(ctx, dsn)
		if err != nil {
			t.Fatalf("pgxpool.New pool2: %v", err)
		}
		defer pool2.Close()

		applyMigrationsUpTo0006(ctx, t, pool1)
		if err := db.RunMigrations(ctx, pool1); err != nil {
			t.Fatalf("RunMigrations: %v", err)
		}

		const totalRows = 12
		expected := make(map[string]string, totalRows)
		for i := 0; i < totalRows; i++ {
			_, shopID := insertPGUserShop(ctx, t, pool1, "Chủ Shop Song Song", RoleSeller, ShopStatusActive)
			rowID := uuid.NewString()
			rawNum := "29034567890" + string(rune('1'+(i/10))) + string(rune('0'+(i%10))) + "9"
			if _, err := pool1.Exec(ctx, `
				INSERT INTO shop_bank_accounts (id, shop_id, bank_code, bank_name, account_number, account_number_masked, account_holder_name, status)
				VALUES ($1, $2, 'TCB', 'Techcombank', $3, $4, 'CHU SHOP SONG SONG', 'VERIFIED')
			`, rowID, shopID, rawNum, maskBankAccountNumber(rawNum)); err != nil {
				t.Fatalf("seed concurrent row %d: %v", i, err)
			}
			expected[rowID] = rawNum
		}

		type res struct {
			count int
			err   error
		}
		start := make(chan struct{})
		results := make(chan res, 2)
		var wg sync.WaitGroup
		for _, p := range []*pgxpool.Pool{pool1, pool2} {
			wg.Add(1)
			go func(pool *pgxpool.Pool) {
				defer wg.Done()
				<-start
				n, bErr := db.BackfillShopBankAccountEncryption(ctx, pool, box1, 2)
				results <- res{count: n, err: bErr}
			}(p)
		}
		close(start)
		wg.Wait()
		close(results)

		totalCount := 0
		for r := range results {
			if r.err != nil {
				t.Fatalf("concurrent backfill returned error: %v", r.err)
			}
			totalCount += r.count
		}
		if totalCount != totalRows {
			t.Fatalf("concurrent backfill sum = %d, want %d", totalCount, totalRows)
		}

		var remaining int64
		if err := pool1.QueryRow(ctx, `SELECT COUNT(*) FROM shop_bank_accounts WHERE account_number <> ''`).Scan(&remaining); err != nil {
			t.Fatalf("count remaining: %v", err)
		}
		if remaining != 0 {
			t.Fatalf("expected 0 plaintext rows after concurrent backfill, got %d", remaining)
		}
		for rowID, wantPlain := range expected {
			var enc []byte
			if err := pool1.QueryRow(ctx, `SELECT account_number_enc FROM shop_bank_accounts WHERE id = $1`, rowID).Scan(&enc); err != nil {
				t.Fatalf("select %s: %v", rowID, err)
			}
			got, decErr := box1.Decrypt(enc)
			if decErr != nil || got != wantPlain {
				t.Fatalf("row %s decrypt mismatch after concurrent backfill: err=%v", rowID, decErr)
			}
		}
	})

	// 3. Wrong key refuses startup AND does NOT overwrite already-encrypted OR pending plaintext rows!
	t.Run("WrongKeyRefusesStartupAndNoOverwrite", func(t *testing.T) {
		ctx := context.Background()
		pool, _ := openTempUserPGPool(t)
		if err := db.RunMigrations(ctx, pool); err != nil {
			t.Fatalf("RunMigrations: %v", err)
		}

		owner1, shop1 := insertPGUserShop(ctx, t, pool, "Shop Key One", RoleSeller, ShopStatusActive)
		_, shop2 := insertPGUserShop(ctx, t, pool, "Shop Key Two", RoleSeller, ShopStatusActive)

		// Row 1 encrypted with box1.
		row1ID := uuid.NewString()
		raw1 := "39034567891001"
		enc1, err := box1.Encrypt(raw1)
		if err != nil {
			t.Fatalf("Encrypt raw1: %v", err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO shop_bank_accounts (id, shop_id, bank_code, bank_name, account_number, account_number_enc, account_number_masked, account_holder_name, status)
			VALUES ($1, $2, 'VCB', 'Vietcombank', '', $3, '****1001', 'SHOP KEY ONE', 'VERIFIED')
		`, row1ID, shop1, enc1); err != nil {
			t.Fatalf("insert row1: %v", err)
		}

		// Row 2 still legacy plaintext.
		row2ID := uuid.NewString()
		raw2 := "39034567892002"
		if _, err := pool.Exec(ctx, `
			INSERT INTO shop_bank_accounts (id, shop_id, bank_code, bank_name, account_number, account_number_masked, account_holder_name, status)
			VALUES ($1, $2, 'TCB', 'Techcombank', $3, '****2002', 'SHOP KEY TWO', 'VERIFIED')
		`, row2ID, shop2, raw2); err != nil {
			t.Fatalf("insert row2: %v", err)
		}

		// Startup backfill with wrong key (box2) MUST fail without modifying row1 or row2!
		_, wrongErr := db.BackfillShopBankAccountEncryption(ctx, pool, box2, 10)
		if wrongErr == nil {
			t.Fatalf("expected backfill with wrong key to fail, got nil")
		}
		if !strings.Contains(wrongErr.Error(), "BANK_ACCOUNT_KEY") {
			t.Fatalf("wrong key backfill error should mention BANK_ACCOUNT_KEY, got %v", wrongErr)
		}
		assertNoSensitiveLeak(t, "wrong key backfill error", wrongErr.Error(),
			raw1, raw2,
			string(enc1),
			base64.StdEncoding.EncodeToString(enc1),
			hex.EncodeToString(enc1),
			base64.StdEncoding.EncodeToString(wrongKeyBytes),
			testBankAccountKeyB64,
		)

		// Verify DB untouched: row1 ciphertext unchanged, row2 still unencrypted plaintext!
		var (
			gotPlain1 string
			gotEnc1   []byte
			gotPlain2 string
			gotEnc2   []byte
		)
		if err := pool.QueryRow(ctx, `SELECT account_number, account_number_enc FROM shop_bank_accounts WHERE id = $1`, row1ID).Scan(&gotPlain1, &gotEnc1); err != nil {
			t.Fatalf("read row1: %v", err)
		}
		if err := pool.QueryRow(ctx, `SELECT account_number, account_number_enc FROM shop_bank_accounts WHERE id = $1`, row2ID).Scan(&gotPlain2, &gotEnc2); err != nil {
			t.Fatalf("read row2: %v", err)
		}
		if gotPlain1 != "" || !bytes.Equal(gotEnc1, enc1) {
			t.Fatalf("row1 was modified during failed wrong-key backfill")
		}
		if gotPlain2 != raw2 || gotEnc2 != nil {
			t.Fatalf("row2 was overwritten during failed wrong-key backfill: plainLen=%d encNil=%v", len(gotPlain2), gotEnc2 == nil)
		}

		// Also verify wrong-key server rejects payout with generic Internal while GetShopBankAccount still succeeds.
		wrongSrv, _, logBuf := newPGServerWithLog(t, pool, box2, nil)
		if _, err := wrongSrv.GetShopBankAccountForPayout(ctxWithService("payment"), &userv1.GetShopBankAccountForPayoutRequest{ShopId: shop1}); status.Code(err) != codes.Internal ||
			status.Convert(err).Message() != "failed to load verified shop bank account" {
			t.Fatalf("wrong-key payout: want generic Internal, got %v", err)
		}
		assertNoSensitiveLeak(t, "wrong-key payout log", logBuf.String(),
			raw1, string(enc1), base64.StdEncoding.EncodeToString(enc1), hex.EncodeToString(enc1),
		)
		if resp, err := wrongSrv.GetShopBankAccount(ctxWithUser(owner1, RoleSeller, shop1), &userv1.GetShopBankAccountRequest{}); err != nil ||
			resp.GetBankAccount().GetAccountNumber() != "" || resp.GetBankAccount().GetAccountNumberMasked() != "****1001" {
			t.Fatalf("wrong-key GetShopBankAccount should not decrypt: err=%v resp=%+v", err, resp)
		}

		// Corrupted/truncated ciphertext in DB must also fail backfill without modifying row2!
		_, shop3 := insertPGUserShop(ctx, t, pool, "Shop Corrupt", RoleSeller, ShopStatusActive)
		row3ID := uuid.NewString()
		if _, err := pool.Exec(ctx, `
			INSERT INTO shop_bank_accounts (id, shop_id, bank_code, bank_name, account_number, account_number_enc, account_number_masked, account_holder_name, status)
			VALUES ($1, $2, 'MB', 'MB Bank', '', $3, '****3003', 'SHOP CORRUPT', 'VERIFIED')
		`, row3ID, shop3, []byte("truncated")); err != nil {
			t.Fatalf("insert corrupt row3: %v", err)
		}
		if _, err := db.BackfillShopBankAccountEncryption(ctx, pool, box1, 10); err == nil {
			t.Fatalf("expected backfill to fail on truncated ciphertext row3")
		}
		var stillPlain2 string
		var stillEnc2 []byte
		if err := pool.QueryRow(ctx, `SELECT account_number, account_number_enc FROM shop_bank_accounts WHERE id = $1`, row2ID).Scan(&stillPlain2, &stillEnc2); err != nil {
			t.Fatalf("re-read row2 after corrupt row3 failure: %v", err)
		}
		if stillPlain2 != raw2 || stillEnc2 != nil {
			t.Fatalf("row2 was modified despite corrupt row3 failure")
		}
	})

	// 4. Newly inserted rows via AddShopBankAccount have empty account_number and encrypted account_number_enc in Postgres.
	t.Run("NewRowWritesOnlyCiphertextNoPlaintext", func(t *testing.T) {
		ctx := context.Background()
		pool, _ := openTempUserPGPool(t)
		if err := db.RunMigrations(ctx, pool); err != nil {
			t.Fatalf("RunMigrations: %v", err)
		}
		clk := newW2BClock(time.Date(2026, 11, 24, 14, 0, 0, 0, time.UTC))
		srv, pub, logBuf := newPGServerWithLog(t, pool, box1, clk)
		ownerID, shopID := insertPGUserShop(ctx, t, pool, "Lê Thị Mới", RoleSeller, ShopStatusActive)
		sellerCtx := ctxWithUser(ownerID, RoleSeller, shopID)

		rawAcc := "49034567896789"
		addResp, err := srv.AddShopBankAccount(sellerCtx, &userv1.AddShopBankAccountRequest{
			BankCode:          "MB",
			BankName:          "MB Bank",
			AccountNumber:     rawAcc,
			AccountHolderName: "LE THI MOI",
		})
		if err != nil {
			t.Fatalf("AddShopBankAccount: %v", err)
		}
		if _, err := srv.VerifyShopBankAccountOTP(sellerCtx, &userv1.VerifyShopBankAccountOTPRequest{
			Otp: pub.shopBankAccountOTP[len(pub.shopBankAccountOTP)-1].OTP,
		}); err != nil {
			t.Fatalf("VerifyShopBankAccountOTP: %v", err)
		}

		var (
			plainCol string
			encCol   []byte
		)
		if err := pool.QueryRow(ctx, `SELECT account_number, account_number_enc FROM shop_bank_accounts WHERE id = $1`, addResp.GetBankAccount().GetId()).Scan(&plainCol, &encCol); err != nil {
			t.Fatalf("query new row: %v", err)
		}
		if plainCol != "" {
			t.Fatalf("new row stored plaintext account_number (len=%d)", len(plainCol))
		}
		if len(encCol) < 28 || bytes.Contains(encCol, []byte(rawAcc)) {
			t.Fatalf("new row ciphertext invalid (len=%d)", len(encCol))
		}

		payoutResp, err := srv.GetShopBankAccountForPayout(ctxWithService("payment"), &userv1.GetShopBankAccountForPayoutRequest{ShopId: shopID})
		if err != nil || payoutResp.GetBankAccount().GetAccountNumber() != rawAcc {
			t.Fatalf("GetShopBankAccountForPayout new row: err=%v resp=%+v", err, payoutResp)
		}
		assertNoSensitiveLeak(t, "new row logs", logBuf.String(),
			rawAcc, string(encCol), base64.StdEncoding.EncodeToString(encCol), hex.EncodeToString(encCol),
		)
	})
}

func TestPG_W3A_D6_ConcurrencyRaces(t *testing.T) {
	if strings.TrimSpace(os.Getenv("TEST_USER_DB_URL")) == "" {
		t.Skip("set TEST_USER_DB_URL to run real Postgres integration tests")
	}

	box := newTestBankAccountBox(t)

	// 1. Admin views A, seller switches to B (OTP done), admin approves A -> 409 & zero wrong VERIFIED rows; plus real concurrent race.
	t.Run("BankAccountReviewRace_AdminVsSellerSwitch", func(t *testing.T) {
		ctx := context.Background()
		pool, _ := openTempUserPGPool(t)
		if err := db.RunMigrations(ctx, pool); err != nil {
			t.Fatalf("RunMigrations: %v", err)
		}
		clk := newW2BClock(time.Date(2026, 11, 24, 15, 0, 0, 0, time.UTC))
		srv, pub, _ := newPGServerWithLog(t, pool, box, clk)

		adminID := uuid.NewString()
		if _, err := pool.Exec(ctx, `
			INSERT INTO users (id, full_name, email, phone, password_hash, role, status)
			VALUES ($1, 'PG Admin D6', $2, '', 'hash', 'admin', 'ACTIVE')
		`, adminID, "pg-adm-d6-"+adminID[:8]+"@example.com"); err != nil {
			t.Fatalf("insert admin: %v", err)
		}
		adminCtx := ctxWithUser(adminID, RoleAdmin, "")

		// 1a. Deterministic race on real Postgres: Admin views A -> seller switches to B -> admin approves A -> 409, 0 VERIFIED.
		owner1, shop1 := insertPGUserShop(ctx, t, pool, "Nguyễn Văn Đua Một", RoleSeller, ShopStatusActive)
		seller1Ctx := ctxWithUser(owner1, RoleSeller, shop1)

		addA, err := srv.AddShopBankAccount(seller1Ctx, &userv1.AddShopBankAccountRequest{
			BankCode: "VCB", BankName: "Vietcombank", AccountNumber: "59034567890001", AccountHolderName: "CONG TY A PG",
		})
		if err != nil {
			t.Fatalf("Add A: %v", err)
		}
		if _, err := srv.VerifyShopBankAccountOTP(seller1Ctx, &userv1.VerifyShopBankAccountOTPRequest{
			Otp: pub.shopBankAccountOTP[len(pub.shopBankAccountOTP)-1].OTP,
		}); err != nil {
			t.Fatalf("Verify A: %v", err)
		}
		// Admin views A.
		gotA, err := srv.GetShopBankAccount(adminCtx, &userv1.GetShopBankAccountRequest{ShopId: shop1})
		if err != nil || gotA.GetBankAccount().GetId() != addA.GetBankAccount().GetId() {
			t.Fatalf("Admin view A: err=%v resp=%+v", err, gotA)
		}
		// Seller switches to B (OTP verified).
		clk.Advance(time.Minute)
		addB, err := srv.AddShopBankAccount(seller1Ctx, &userv1.AddShopBankAccountRequest{
			BankCode: "TCB", BankName: "Techcombank", AccountNumber: "59034567890002", AccountHolderName: "CONG TY B PG",
		})
		if err != nil {
			t.Fatalf("Add B: %v", err)
		}
		if _, err := srv.VerifyShopBankAccountOTP(seller1Ctx, &userv1.VerifyShopBankAccountOTPRequest{
			Otp: pub.shopBankAccountOTP[len(pub.shopBankAccountOTP)-1].OTP,
		}); err != nil {
			t.Fatalf("Verify B: %v", err)
		}
		// Admin approves stale A -> 409 FailedPrecondition!
		if _, err := srv.AdminReviewShopBankAccount(adminCtx, &userv1.AdminReviewShopBankAccountRequest{
			ShopId:        shop1,
			BankAccountId: addA.GetBankAccount().GetId(),
			Decision:      userv1.ReviewDecision_REVIEW_DECISION_APPROVE,
		}); status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("PG approve stale A: want FailedPrecondition, got %v", err)
		}
		var verifiedCnt int64
		var statusA, statusB string
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM shop_bank_accounts WHERE shop_id = $1 AND status = 'VERIFIED'`, shop1).Scan(&verifiedCnt); err != nil {
			t.Fatalf("count verified: %v", err)
		}
		if err := pool.QueryRow(ctx, `SELECT status FROM shop_bank_accounts WHERE id = $1`, addA.GetBankAccount().GetId()).Scan(&statusA); err != nil {
			t.Fatalf("status A: %v", err)
		}
		if err := pool.QueryRow(ctx, `SELECT status FROM shop_bank_accounts WHERE id = $1`, addB.GetBankAccount().GetId()).Scan(&statusB); err != nil {
			t.Fatalf("status B: %v", err)
		}
		if verifiedCnt != 0 || statusA != BankAccountStatusSuperseded || statusB != BankAccountStatusPendingAdminReview {
			t.Fatalf("unexpected PG state after stale A approve: verified=%d A=%q B=%q", verifiedCnt, statusA, statusB)
		}

		// 1b. Concurrent double approval on B -> strictly 1 OK and 1 FailedPrecondition!
		startDouble := make(chan struct{})
		errsDouble := make([]error, 2)
		var wgDouble sync.WaitGroup
		for i := 0; i < 2; i++ {
			wgDouble.Add(1)
			go func(idx int) {
				defer wgDouble.Done()
				<-startDouble
				_, errsDouble[idx] = srv.AdminReviewShopBankAccount(adminCtx, &userv1.AdminReviewShopBankAccountRequest{
					ShopId:        shop1,
					BankAccountId: addB.GetBankAccount().GetId(),
					Decision:      userv1.ReviewDecision_REVIEW_DECISION_APPROVE,
				})
			}(i)
		}
		close(startDouble)
		wgDouble.Wait()
		okCnt, conflictCnt := 0, 0
		for _, e := range errsDouble {
			switch {
			case e == nil:
				okCnt++
			case status.Code(e) == codes.FailedPrecondition:
				conflictCnt++
			default:
				t.Fatalf("unexpected concurrent double-approve error: %v", e)
			}
		}
		if okCnt != 1 || conflictCnt != 1 {
			t.Fatalf("concurrent double-approve: want 1 ok + 1 FailedPrecondition, got ok=%d conflict=%d", okCnt, conflictCnt)
		}

		// 1c. True concurrent goroutine race: VerifyOTP(B2) vs AdminReview(A2) behind barrier on real Postgres!
		owner2, shop2 := insertPGUserShop(ctx, t, pool, "Nguyễn Văn Đua Hai", RoleSeller, ShopStatusActive)
		seller2Ctx := ctxWithUser(owner2, RoleSeller, shop2)

		addA2, err := srv.AddShopBankAccount(seller2Ctx, &userv1.AddShopBankAccountRequest{
			BankCode: "VCB", BankName: "Vietcombank", AccountNumber: "59034567890011", AccountHolderName: "CONG TY A2 PG",
		})
		if err != nil {
			t.Fatalf("Add A2: %v", err)
		}
		if _, err := srv.VerifyShopBankAccountOTP(seller2Ctx, &userv1.VerifyShopBankAccountOTPRequest{
			Otp: pub.shopBankAccountOTP[len(pub.shopBankAccountOTP)-1].OTP,
		}); err != nil {
			t.Fatalf("Verify A2: %v", err)
		}
		clk.Advance(time.Minute)
		addB2, err := srv.AddShopBankAccount(seller2Ctx, &userv1.AddShopBankAccountRequest{
			BankCode: "TCB", BankName: "Techcombank", AccountNumber: "59034567890012", AccountHolderName: "CONG TY B2 PG",
		})
		if err != nil {
			t.Fatalf("Add B2: %v", err)
		}
		otpB2 := pub.shopBankAccountOTP[len(pub.shopBankAccountOTP)-1].OTP

		startRace := make(chan struct{})
		var (
			verErr error
			revErr error
			wgRace sync.WaitGroup
		)
		wgRace.Add(2)
		go func() {
			defer wgRace.Done()
			<-startRace
			_, verErr = srv.VerifyShopBankAccountOTP(seller2Ctx, &userv1.VerifyShopBankAccountOTPRequest{Otp: otpB2})
		}()
		go func() {
			defer wgRace.Done()
			<-startRace
			_, revErr = srv.AdminReviewShopBankAccount(adminCtx, &userv1.AdminReviewShopBankAccountRequest{
				ShopId:        shop2,
				BankAccountId: addA2.GetBankAccount().GetId(),
				Decision:      userv1.ReviewDecision_REVIEW_DECISION_APPROVE,
			})
		}()
		close(startRace)
		wgRace.Wait()

		if verErr != nil {
			t.Fatalf("concurrent VerifyOTP(B2) failed: %v", verErr)
		}
		var sA2, sB2 string
		if err := pool.QueryRow(ctx, `SELECT status FROM shop_bank_accounts WHERE id = $1`, addA2.GetBankAccount().GetId()).Scan(&sA2); err != nil {
			t.Fatalf("status A2: %v", err)
		}
		if err := pool.QueryRow(ctx, `SELECT status FROM shop_bank_accounts WHERE id = $1`, addB2.GetBankAccount().GetId()).Scan(&sB2); err != nil {
			t.Fatalf("status B2: %v", err)
		}
		if revErr == nil {
			// Admin won lock first -> A2 VERIFIED, then B2 entered PENDING_ADMIN_REVIEW.
			if sA2 != BankAccountStatusVerified || sB2 != BankAccountStatusPendingAdminReview {
				t.Fatalf("admin-first ordering violated invariant: A2=%q B2=%q", sA2, sB2)
			}
		} else if status.Code(revErr) == codes.FailedPrecondition {
			// Seller won lock first -> A2 SUPERSEDED, B2 PENDING_ADMIN_REVIEW, 0 rows VERIFIED!
			if sA2 != BankAccountStatusSuperseded || sB2 != BankAccountStatusPendingAdminReview {
				t.Fatalf("seller-first ordering violated invariant: A2=%q B2=%q", sA2, sB2)
			}
		} else {
			t.Fatalf("unexpected concurrent AdminReviewShopBankAccount error: %v", revErr)
		}
	})

	// 2. AdminReviewShop race: full_name changes between admin view and approve -> 409 OWNER_NAME_CHANGED.
	t.Run("ShopReviewRace_FullNameChangesBetweenViewAndApprove", func(t *testing.T) {
		ctx := context.Background()
		pool, _ := openTempUserPGPool(t)
		if err := db.RunMigrations(ctx, pool); err != nil {
			t.Fatalf("RunMigrations: %v", err)
		}
		srv, _, _ := newPGServerWithLog(t, pool, box, nil)

		adminID := uuid.NewString()
		if _, err := pool.Exec(ctx, `
			INSERT INTO users (id, full_name, email, phone, password_hash, role, status)
			VALUES ($1, 'PG Admin Shop', $2, '', 'hash', 'admin', 'ACTIVE')
		`, adminID, "pg-adm-sh-"+adminID[:8]+"@example.com"); err != nil {
			t.Fatalf("insert admin: %v", err)
		}
		adminCtx := ctxWithUser(adminID, RoleAdmin, "")

		// 2a. Deterministic: owner changes full_name before admin approves -> 409 OWNER_NAME_CHANGED, shop stays PENDING.
		ownerID, shopID := insertPGUserShop(ctx, t, pool, "Nguyễn Văn Ban Đầu", RoleBuyer, ShopStatusPending)
		if _, err := srv.UpdateProfile(ctxWithUser(ownerID, RoleBuyer, ""), &userv1.UpdateProfileRequest{
			FullName: "Nguyễn Văn Đã Đổi",
		}); err != nil {
			t.Fatalf("UpdateProfile: %v", err)
		}
		_, err := srv.AdminReviewShop(adminCtx, &userv1.AdminReviewShopRequest{
			ShopId:            shopID,
			Decision:          userv1.ReviewDecision_REVIEW_DECISION_APPROVE,
			ExpectedOwnerName: "Nguyễn Văn Ban Đầu",
		})
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(status.Convert(err).Message(), "OWNER_NAME_CHANGED") {
			t.Fatalf("PG AdminReviewShop stale name: want FailedPrecondition OWNER_NAME_CHANGED, got %v", err)
		}
		var st, verName string
		if err := pool.QueryRow(ctx, `SELECT status, verified_owner_name FROM shops WHERE id = $1`, shopID).Scan(&st, &verName); err != nil {
			t.Fatalf("query shop after stale approve: %v", err)
		}
		if st != ShopStatusPending || verName != "" {
			t.Fatalf("shop mutated despite OWNER_NAME_CHANGED: status=%q verified_owner_name=%q", st, verName)
		}

		// 2b. Concurrent goroutines on real Postgres across 5 trials: UpdateProfile racing AdminReviewShop.
		for trial := 0; trial < 5; trial++ {
			oID, sID := insertPGUserShop(ctx, t, pool, "Chủ Shop Đua Gốc", RoleBuyer, ShopStatusPending)
			start := make(chan struct{})
			var (
				updErr error
				revErr error
				wg     sync.WaitGroup
			)
			wg.Add(2)
			go func() {
				defer wg.Done()
				<-start
				_, updErr = srv.UpdateProfile(ctxWithUser(oID, RoleBuyer, ""), &userv1.UpdateProfileRequest{
					FullName: "Chủ Shop Đua Mới",
				})
			}()
			go func() {
				defer wg.Done()
				<-start
				_, revErr = srv.AdminReviewShop(adminCtx, &userv1.AdminReviewShopRequest{
					ShopId:            sID,
					Decision:          userv1.ReviewDecision_REVIEW_DECISION_APPROVE,
					ExpectedOwnerName: "Chủ Shop Đua Gốc",
				})
			}()
			close(start)
			wg.Wait()

			if updErr != nil {
				t.Fatalf("trial %d UpdateProfile failed: %v", trial, updErr)
			}
			var gotStatus, gotVerified string
			if err := pool.QueryRow(ctx, `SELECT status, verified_owner_name FROM shops WHERE id = $1`, sID).Scan(&gotStatus, &gotVerified); err != nil {
				t.Fatalf("trial %d query shop: %v", trial, err)
			}
			if revErr == nil {
				// Approve won the users row lock first -> MUST have snapshotted the expected original name, NEVER the new name!
				if gotStatus != ShopStatusActive || gotVerified != "Chủ Shop Đua Gốc" {
					t.Fatalf("trial %d approve-first violated invariant: status=%q verified_owner_name=%q", trial, gotStatus, gotVerified)
				}
			} else if status.Code(revErr) == codes.FailedPrecondition && strings.Contains(status.Convert(revErr).Message(), "OWNER_NAME_CHANGED") {
				// UpdateProfile won first -> shop MUST remain PENDING with empty verified_owner_name!
				if gotStatus != ShopStatusPending || gotVerified != "" {
					t.Fatalf("trial %d update-first violated invariant: status=%q verified_owner_name=%q", trial, gotStatus, gotVerified)
				}
			} else {
				t.Fatalf("trial %d unexpected AdminReviewShop error: %v", trial, revErr)
			}
		}
	})
}

func TestPG_W3A_AdminListUsers_QueryByExactID(t *testing.T) {
	if strings.TrimSpace(os.Getenv("TEST_USER_DB_URL")) == "" {
		t.Skip("set TEST_USER_DB_URL to run real Postgres integration tests")
	}

	ctx := context.Background()
	pool, _ := openTempUserPGPool(t)
	if err := db.RunMigrations(ctx, pool); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	srv, _, _ := newPGServerWithLog(t, pool, newTestBankAccountBox(t), nil)

	insertUser := func(fullName, email, phone, role, st string) string {
		t.Helper()
		id := uuid.NewString()
		if _, err := pool.Exec(ctx, `
			INSERT INTO users (id, full_name, email, phone, password_hash, role, status)
			VALUES ($1, $2, $3, $4, 'hash', $5, $6)
		`, id, fullName, email, phone, role, st); err != nil {
			t.Fatalf("insert user %s: %v", email, err)
		}
		return id
	}

	adminID := insertUser("Admin PG Search", "pg-adm-search@example.com", "0901000001", RoleAdmin, StatusActive)
	superID := insertUser("Super PG Search", "pg-sup-search@example.com", "0901000002", RoleSuperAdmin, StatusActive)
	u1ID := insertUser("Nguyễn Văn Chủ Gian Hàng", "chushop.mot@example.com", "0908555111", RoleBuyer, StatusActive)
	u2ID := insertUser("Lê Thị Thanh Hà", "thanhha.hai@example.com", "0909666222", RoleSeller, StatusActive)

	// Make u1 older, then insert newer accounts whose full_name / email contain u1ID (and a non-existent UUID)
	// to prove UUID queries ONLY match users.id and never let newer fuzzy matches shadow page_size=1.
	if _, err := pool.Exec(ctx, `UPDATE users SET created_at = NOW() - INTERVAL '1 hour' WHERE id = $1`, u1ID); err != nil {
		t.Fatalf("backdate u1 created_at: %v", err)
	}
	notFoundID := uuid.NewString()
	_ = insertUser("Kẻ Che Khuất "+u1ID, "shadow.name@example.com", "0907111222", RoleBuyer, StatusActive)
	_ = insertUser("Kẻ Che Email", u1ID+"@shadow.example.com", "0907111333", RoleBuyer, StatusActive)
	_ = insertUser("Chứa UUID Rỗng "+notFoundID, "shadow.miss@example.com", "0907111444", RoleBuyer, StatusActive)

	adminCtx := ctxWithUser(adminID, RoleAdmin, "")
	superCtx := ctxWithUser(superID, RoleSuperAdmin, "")
	buyerCtx := ctxWithUser(u1ID, RoleBuyer, "")

	// Auth unchanged: non-admin rejected; admin & super_admin allowed.
	if _, err := srv.AdminListUsers(buyerCtx, &userv1.AdminListUsersRequest{Query: u1ID}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("buyer AdminListUsers: want PermissionDenied, got %v", err)
	}

	// 1. Exact ID lookup with page_size=1 (lowercase, uppercase, trimmed) MUST return u1 with total=1,
	// even when newer users carry u1ID in full_name or email!
	for _, tc := range []struct {
		label string
		ctx   context.Context
		query string
	}{
		{"exact_lower", adminCtx, u1ID},
		{"exact_upper", superCtx, strings.ToUpper(u1ID)},
		{"exact_trimmed", adminCtx, "  " + u1ID + "  "},
	} {
		resp, err := srv.AdminListUsers(tc.ctx, &userv1.AdminListUsersRequest{
			Query:    tc.query,
			Page:     1,
			PageSize: 1,
		})
		if err != nil {
			t.Fatalf("%s: AdminListUsers failed: %v", tc.label, err)
		}
		if resp.GetTotal() != 1 || len(resp.GetUsers()) != 1 || resp.GetUsers()[0].GetId() != u1ID {
			gotID := ""
			if len(resp.GetUsers()) > 0 {
				gotID = resp.GetUsers()[0].GetId()
			}
			t.Fatalf("%s: want 1 user (%s) total=1, got len=%d total=%d first_id=%s",
				tc.label, u1ID, len(resp.GetUsers()), resp.GetTotal(), gotID)
		}
	}

	// 2. Non-existent UUID -> 0 users, total=0 (even though a user has notFoundID in full_name!).
	missResp, err := srv.AdminListUsers(adminCtx, &userv1.AdminListUsersRequest{
		Query:    notFoundID,
		Page:     1,
		PageSize: 1,
	})
	if err != nil || missResp.GetTotal() != 0 || len(missResp.GetUsers()) != 0 {
		t.Fatalf("non-existent UUID %s: want 0/0, got err=%v len=%d total=%d", notFoundID, err, len(missResp.GetUsers()), missResp.GetTotal())
	}

	// 3. Partial UUID prefix -> 0 users for u2ID (not present in any name/email/phone; exact match only, no ILIKE on id).
	partialResp, err := srv.AdminListUsers(adminCtx, &userv1.AdminListUsersRequest{Query: u2ID[:13]})
	if err != nil || partialResp.GetTotal() != 0 || len(partialResp.GetUsers()) != 0 {
		t.Fatalf("partial UUID prefix: want 0/0, got err=%v len=%d total=%d", err, len(partialResp.GetUsers()), partialResp.GetTotal())
	}

	// 4. Non-UUID / malformed strings (including quote/newline) must NOT trigger Postgres UUID cast error (22P02).
	for _, bad := range []string{"not-a-uuid", "550e8400-e29b-41d4-a716-446655440000-extra", "zz0e8400-e29b-41d4-a716-446655440000", "abc'def"} {
		resp, err := srv.AdminListUsers(adminCtx, &userv1.AdminListUsersRequest{Query: bad})
		if err != nil || resp.GetTotal() != 0 || len(resp.GetUsers()) != 0 {
			t.Fatalf("malformed query %q: want 0/0 without error, got err=%v len=%d total=%d", bad, err, len(resp.GetUsers()), resp.GetTotal())
		}
	}

	// 5. Email, full_name, and phone searches still match via ILIKE as before.
	for label, tc := range map[string]struct {
		query  string
		wantID string
	}{
		"email": {"CHUSHOP.MOT", u1ID},
		"name":  {"Thanh Hà", u2ID},
		"phone": {"0909666", u2ID},
	} {
		resp, err := srv.AdminListUsers(adminCtx, &userv1.AdminListUsersRequest{Query: tc.query})
		if err != nil || resp.GetTotal() != 1 || len(resp.GetUsers()) != 1 || resp.GetUsers()[0].GetId() != tc.wantID {
			t.Fatalf("%s query (%q): want user %s total=1, got err=%v len=%d total=%d",
				label, tc.query, tc.wantID, err, len(resp.GetUsers()), resp.GetTotal())
		}
	}

	// 6. Role + status filters still combine with exact ID lookup.
	wrongRole, err := srv.AdminListUsers(adminCtx, &userv1.AdminListUsersRequest{
		Query: u1ID,
		Role:  userv1.UserRole_USER_ROLE_SELLER,
	})
	if err != nil || wrongRole.GetTotal() != 0 || len(wrongRole.GetUsers()) != 0 {
		t.Fatalf("exact ID + mismatched role: want 0/0, got err=%v len=%d total=%d", err, len(wrongRole.GetUsers()), wrongRole.GetTotal())
	}
	rightRole, err := srv.AdminListUsers(adminCtx, &userv1.AdminListUsersRequest{
		Query:  u1ID,
		Role:   userv1.UserRole_USER_ROLE_BUYER,
		Status: userv1.UserStatus_USER_STATUS_ACTIVE,
	})
	if err != nil || rightRole.GetTotal() != 1 || len(rightRole.GetUsers()) != 1 || rightRole.GetUsers()[0].GetId() != u1ID {
		t.Fatalf("exact ID + matching role/status: want 1 user (%s), got err=%v len=%d total=%d", u1ID, err, len(rightRole.GetUsers()), rightRole.GetTotal())
	}

	// 7. Empty query still lists all users.
	allResp, err := srv.AdminListUsers(adminCtx, &userv1.AdminListUsersRequest{Page: 1, PageSize: 20})
	if err != nil || allResp.GetTotal() != 7 || len(allResp.GetUsers()) != 7 {
		t.Fatalf("empty query: want 7 users total=7, got err=%v len=%d total=%d", err, len(allResp.GetUsers()), allResp.GetTotal())
	}
}
