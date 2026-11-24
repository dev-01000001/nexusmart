package service

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/services/user/internal/db"
	"github.com/nexusmart/core/services/user/migrations"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestPG_W3B_E19_PhoneOTPVerifyUniquenessAndConcurrency(t *testing.T) {
	if strings.TrimSpace(os.Getenv("TEST_USER_DB_URL")) == "" {
		t.Skip("set TEST_USER_DB_URL to run real Postgres integration tests")
	}

	box := newTestBankAccountBox(t)

	t.Run("VerifyResetAndDuplicatePhoneConstraint", func(t *testing.T) {
		ctx := context.Background()
		pool, _ := openTempUserPGPool(t)
		if err := db.RunMigrations(ctx, pool); err != nil {
			t.Fatalf("RunMigrations: %v", err)
		}
		clk := newW2BClock(time.Date(2026, 11, 24, 17, 0, 0, 0, time.UTC))
		srv, pub, logBuf := newPGServerWithLog(t, pool, box, clk)
		if memSec, ok := srv.sec.(*memSecurity); ok {
			memSec.nowFn = clk.Now
		}

		// Seed buyer A with unverified phone and buyer B with empty phone.
		buyerA := uuid.NewString()
		buyerB := uuid.NewString()
		if _, err := pool.Exec(ctx, `
			INSERT INTO users (id, full_name, email, phone, password_hash, role, status, phone_verified)
			VALUES
				($1, 'Buyer PG A', $2, '0908000111', 'hash', 'buyer', 'ACTIVE', FALSE),
				($3, 'Buyer PG B', $4, '', 'hash', 'buyer', 'ACTIVE', FALSE)
		`, buyerA, "pg-phone-a-"+buyerA[:8]+"@example.com", buyerB, "pg-phone-b-"+buyerB[:8]+"@example.com"); err != nil {
			t.Fatalf("seed buyers: %v", err)
		}

		ctxA := ctxWithUser(buyerA, RoleBuyer, "")
		ctxB := ctxWithUser(buyerB, RoleBuyer, "")

		// Buyer A requests & verifies OTP -> DB phone_verified=true, ValidateUser phone_verified=true, audit_logs recorded without OTP.
		reqResp, err := srv.RequestPhoneOTP(ctxA, &userv1.RequestPhoneOTPRequest{})
		if err != nil || !reqResp.GetSent() || reqResp.GetPhoneMasked() != "090****111" {
			t.Fatalf("PG RequestPhoneOTP A: err=%v resp=%+v", err, reqResp)
		}
		otpA := pub.phoneOTPRequested[len(pub.phoneOTPRequested)-1].OTP
		verResp, err := srv.VerifyPhoneOTP(ctxA, &userv1.VerifyPhoneOTPRequest{
			Phone: "0908000111",
			Otp:   otpA,
		})
		if err != nil || !verResp.GetVerified() || !verResp.GetUser().GetPhoneVerified() {
			t.Fatalf("PG VerifyPhoneOTP A: err=%v resp=%+v", err, verResp)
		}

		var dbPhone string
		var dbVerified bool
		if err := pool.QueryRow(ctx, `SELECT phone, phone_verified FROM users WHERE id = $1`, buyerA).Scan(&dbPhone, &dbVerified); err != nil {
			t.Fatalf("query buyer A: %v", err)
		}
		if dbPhone != "0908000111" || !dbVerified {
			t.Fatalf("PG user A after verify: phone=%q verified=%v; want 0908000111/true", dbPhone, dbVerified)
		}

		// Audit log recorded in Postgres without OTP or raw phone.
		var beforeJSON, afterJSON string
		if err := pool.QueryRow(ctx, `
			SELECT before_json, after_json FROM audit_logs
			WHERE action = 'user.phone.verify_otp' AND target_id = $1
			ORDER BY created_at DESC LIMIT 1
		`, buyerA).Scan(&beforeJSON, &afterJSON); err != nil {
			t.Fatalf("query verify audit_log: %v", err)
		}
		rawAudit, _ := json.Marshal(map[string]string{"before": beforeJSON, "after": afterJSON})
		assertNoSensitiveLeak(t, "PG audit_logs", string(rawAudit), otpA, "0908000111")
		assertNoSensitiveLeak(t, "PG server logs", logBuf.String(), otpA, "0908000111")

		// Buyer B cannot request OTP for Buyer A's verified phone (exact or +84 form).
		for _, dup := range []string{"0908000111", "+84908000111", "84908000111"} {
			if _, err := srv.RequestPhoneOTP(ctxB, &userv1.RequestPhoneOTPRequest{Phone: dup}); status.Code(err) != codes.AlreadyExists {
				t.Fatalf("PG Buyer B RequestPhoneOTP(%q): want AlreadyExists, got %v", dup, err)
			}
		}

		// Buyer A updates phone via UpdateProfile -> Postgres phone_verified resets to FALSE.
		if _, err := srv.UpdateProfile(ctxA, &userv1.UpdateProfileRequest{Phone: "0908000222"}); err != nil {
			t.Fatalf("PG UpdateProfile A new phone: %v", err)
		}
		if err := pool.QueryRow(ctx, `SELECT phone, phone_verified FROM users WHERE id = $1`, buyerA).Scan(&dbPhone, &dbVerified); err != nil {
			t.Fatalf("query buyer A after UpdateProfile: %v", err)
		}
		if dbPhone != "0908000222" || dbVerified {
			t.Fatalf("PG user A after phone change: phone=%q verified=%v; want 0908000222/false", dbPhone, dbVerified)
		}
	})

	t.Run("ConcurrentTwoUsersClaimSameNewPhone", func(t *testing.T) {
		ctx := context.Background()
		pool, _ := openTempUserPGPool(t)
		if err := db.RunMigrations(ctx, pool); err != nil {
			t.Fatalf("RunMigrations: %v", err)
		}
		clk := newW2BClock(time.Date(2026, 11, 24, 17, 10, 0, 0, time.UTC))
		srv, pub, _ := newPGServerWithLog(t, pool, box, clk)
		if memSec, ok := srv.sec.(*memSecurity); ok {
			memSec.nowFn = clk.Now
		}

		u1, u2 := uuid.NewString(), uuid.NewString()
		if _, err := pool.Exec(ctx, `
			INSERT INTO users (id, full_name, email, phone, password_hash, role, status, phone_verified)
			VALUES
				($1, 'Race Phone 1', $2, '', 'hash', 'buyer', 'ACTIVE', FALSE),
				($3, 'Race Phone 2', $4, '', 'hash', 'buyer', 'ACTIVE', FALSE)
		`, u1, "pg-r1-"+u1[:8]+"@example.com", u2, "pg-r2-"+u2[:8]+"@example.com"); err != nil {
			t.Fatalf("seed race users: %v", err)
		}

		ctx1 := ctxWithUser(u1, RoleBuyer, "")
		ctx2 := ctxWithUser(u2, RoleBuyer, "")
		const contestedPhone = "0908999888"

		if _, err := srv.RequestPhoneOTP(ctx1, &userv1.RequestPhoneOTPRequest{Phone: contestedPhone}); err != nil {
			t.Fatalf("RequestPhoneOTP u1: %v", err)
		}
		otp1 := pub.phoneOTPRequested[len(pub.phoneOTPRequested)-1].OTP
		if _, err := srv.RequestPhoneOTP(ctx2, &userv1.RequestPhoneOTPRequest{Phone: contestedPhone}); err != nil {
			t.Fatalf("RequestPhoneOTP u2: %v", err)
		}
		otp2 := pub.phoneOTPRequested[len(pub.phoneOTPRequested)-1].OTP

		start := make(chan struct{})
		errs := make([]error, 2)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, errs[0] = srv.VerifyPhoneOTP(ctx1, &userv1.VerifyPhoneOTPRequest{Phone: contestedPhone, Otp: otp1})
		}()
		go func() {
			defer wg.Done()
			<-start
			_, errs[1] = srv.VerifyPhoneOTP(ctx2, &userv1.VerifyPhoneOTPRequest{Phone: contestedPhone, Otp: otp2})
		}()
		close(start)
		wg.Wait()

		var okCnt, dupCnt int
		for _, err := range errs {
			switch {
			case err == nil:
				okCnt++
			case status.Code(err) == codes.AlreadyExists:
				dupCnt++
			default:
				t.Fatalf("unexpected error in concurrent phone claim: %v", err)
			}
		}
		if okCnt != 1 || dupCnt != 1 {
			t.Fatalf("concurrent phone claim: want 1 ok + 1 AlreadyExists, got ok=%d dup=%d", okCnt, dupCnt)
		}

		var ownerCount int64
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE phone = $1 AND phone_verified = TRUE`, contestedPhone).Scan(&ownerCount); err != nil {
			t.Fatalf("count verified owners: %v", err)
		}
		if ownerCount != 1 {
			t.Fatalf("expected strictly 1 verified owner for %s, got %d", contestedPhone, ownerCount)
		}
	})

	t.Run("ConcurrentUpdateProfileVsVerifyPhoneOTP", func(t *testing.T) {
		ctx := context.Background()
		pool, _ := openTempUserPGPool(t)
		if err := db.RunMigrations(ctx, pool); err != nil {
			t.Fatalf("RunMigrations: %v", err)
		}
		clk := newW2BClock(time.Date(2026, 11, 24, 17, 20, 0, 0, time.UTC))
		srv, pub, _ := newPGServerWithLog(t, pool, box, clk)
		if memSec, ok := srv.sec.(*memSecurity); ok {
			memSec.nowFn = clk.Now
		}

		for trial := 0; trial < 5; trial++ {
			uid := uuid.NewString()
			origPhone := "09070010" + string(rune('0'+trial)) + "1"
			newPhone := "09070020" + string(rune('0'+trial)) + "2"
			if _, err := pool.Exec(ctx, `
				INSERT INTO users (id, full_name, email, phone, password_hash, role, status, phone_verified)
				VALUES ($1, 'Race Profile User', $2, $3, 'hash', 'buyer', 'ACTIVE', FALSE)
			`, uid, "pg-race-upd-"+uid[:8]+"@example.com", origPhone); err != nil {
				t.Fatalf("trial %d seed user: %v", trial, err)
			}
			uCtx := ctxWithUser(uid, RoleBuyer, "")
			if _, err := srv.RequestPhoneOTP(uCtx, &userv1.RequestPhoneOTPRequest{Phone: origPhone}); err != nil {
				t.Fatalf("trial %d RequestPhoneOTP: %v", trial, err)
			}
			otp := pub.phoneOTPRequested[len(pub.phoneOTPRequested)-1].OTP

			start := make(chan struct{})
			var (
				updErr error
				verErr error
				wg     sync.WaitGroup
			)
			wg.Add(2)
			go func() {
				defer wg.Done()
				<-start
				_, updErr = srv.UpdateProfile(uCtx, &userv1.UpdateProfileRequest{Phone: newPhone})
			}()
			go func() {
				defer wg.Done()
				<-start
				_, verErr = srv.VerifyPhoneOTP(uCtx, &userv1.VerifyPhoneOTPRequest{Phone: origPhone, Otp: otp})
			}()
			close(start)
			wg.Wait()

			if updErr != nil {
				t.Fatalf("trial %d UpdateProfile failed: %v", trial, updErr)
			}
			if verErr != nil && status.Code(verErr) != codes.InvalidArgument {
				t.Fatalf("trial %d unexpected VerifyPhoneOTP error: %v", trial, verErr)
			}

			var finalPhone string
			var finalVerified bool
			if err := pool.QueryRow(ctx, `SELECT phone, phone_verified FROM users WHERE id = $1`, uid).Scan(&finalPhone, &finalVerified); err != nil {
				t.Fatalf("trial %d select user: %v", trial, err)
			}
			// In BOTH lock orderings (Verify->UpdateProfile or UpdateProfile->Verify),
			// final phone MUST be newPhone and phone_verified MUST be FALSE!
			if finalPhone != newPhone || finalVerified {
				t.Fatalf("trial %d invariant violated: phone=%q verified=%v (want %q/false)", trial, finalPhone, finalVerified, newPhone)
			}
		}
	})
}

func applyMigrationsUpTo0007(ctx context.Context, t *testing.T, pool *pgxpool.Pool) {
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
		"0007_shop_bank_account_encryption.sql",
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

func TestPG_W3B_E19Bis_CanonicalPhoneAndVerifiedOnlyUniqueness(t *testing.T) {
	if strings.TrimSpace(os.Getenv("TEST_USER_DB_URL")) == "" {
		t.Skip("set TEST_USER_DB_URL to run real Postgres integration tests")
	}

	box := newTestBankAccountBox(t)

	// 1. Migration 0008 on pre-populated DB (0001..0007) with mixed phone formats, duplicate verified accounts,
	//    unverified duplicates, empty phones, and non-VN phones; plus clean empty DB.
	t.Run("Migration0008OnPopulatedAndEmptyDB", func(t *testing.T) {
		ctx := context.Background()

		// 1a. Empty DB runs all migrations cleanly including 0008.
		emptyPool, _ := openTempUserPGPool(t)
		if err := db.RunMigrations(ctx, emptyPool); err != nil {
			t.Fatalf("RunMigrations on empty DB: %v", err)
		}
		var hasVerifiedIdx, hasOldIdx, hasLookupIdx bool
		if err := emptyPool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_indexes WHERE tablename='users' AND indexname='idx_users_phone_verified_unique')`).Scan(&hasVerifiedIdx); err != nil {
			t.Fatalf("check idx_users_phone_verified_unique: %v", err)
		}
		if err := emptyPool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_indexes WHERE tablename='users' AND indexname='idx_users_phone_nonempty')`).Scan(&hasOldIdx); err != nil {
			t.Fatalf("check idx_users_phone_nonempty: %v", err)
		}
		if err := emptyPool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_indexes WHERE tablename='users' AND indexname='idx_users_phone')`).Scan(&hasLookupIdx); err != nil {
			t.Fatalf("check idx_users_phone: %v", err)
		}
		if !hasVerifiedIdx || hasOldIdx || !hasLookupIdx {
			t.Fatalf("unexpected indexes after 0008: verified=%v old=%v lookup=%v", hasVerifiedIdx, hasOldIdx, hasLookupIdx)
		}

		// 1b. Legacy DB populated up to 0007 with raw formatted phones and duplicate verified canonical phones.
		pool, _ := openTempUserPGPool(t)
		applyMigrationsUpTo0007(ctx, t, pool)

		tEarly := time.Date(2026, 1, 10, 8, 0, 0, 0, time.UTC)
		tLate := time.Date(2026, 2, 10, 8, 0, 0, 0, time.UTC)
		uWinner := uuid.NewString()
		uLoser := uuid.NewString()
		uUnver := uuid.NewString()
		uLandline := uuid.NewString()
		uNonVN := uuid.NewString()
		uEmpty := uuid.NewString()

		seedRows := []struct {
			id       string
			email    string
			phone    string
			verified bool
			created  time.Time
			updated  time.Time
		}{
			{uWinner, "mig-winner@example.com", "090-123-4567", true, tEarly, tEarly},
			{uLoser, "mig-loser@example.com", "+84 901.234.567", true, tLate, tLate},
			{uUnver, "mig-unver@example.com", "84901234567", false, tEarly, tEarly},
			{uLandline, "mig-landline@example.com", "+84 28-3822.1234", true, tEarly, tEarly},
			{uNonVN, "mig-nonvn@example.com", "INTL-99999", false, tEarly, tEarly},
			{uEmpty, "mig-empty@example.com", "", false, tEarly, tEarly},
		}
		for _, r := range seedRows {
			if _, err := pool.Exec(ctx, `
				INSERT INTO users (id, full_name, email, phone, password_hash, role, status, phone_verified, created_at, updated_at)
				VALUES ($1, 'Mig User', $2, $3, 'hash', 'buyer', 'ACTIVE', $4, $5, $6)
			`, r.id, r.email, r.phone, r.verified, r.created, r.updated); err != nil {
				t.Fatalf("seed legacy user %s: %v", r.email, err)
			}
		}

		if err := db.RunMigrations(ctx, pool); err != nil {
			t.Fatalf("RunMigrations (0008 on populated DB): %v", err)
		}

		checkUser := func(id, wantPhone string, wantVerified bool) {
			t.Helper()
			var gotPhone string
			var gotVer bool
			if err := pool.QueryRow(ctx, `SELECT phone, phone_verified FROM users WHERE id = $1`, id).Scan(&gotPhone, &gotVer); err != nil {
				t.Fatalf("query user %s: %v", id, err)
			}
			if gotPhone != wantPhone || gotVer != wantVerified {
				t.Fatalf("user %s after 0008: got phone=%q verified=%v, want %q/%v", id, gotPhone, gotVer, wantPhone, wantVerified)
			}
		}
		// Earliest verified keeps phone_verified=true; later duplicate keeps canonical phone with phone_verified=false!
		checkUser(uWinner, "0901234567", true)
		checkUser(uLoser, "0901234567", false)
		checkUser(uUnver, "0901234567", false)
		checkUser(uLandline, "02838221234", true)
		checkUser(uNonVN, "INTL-99999", false)
		checkUser(uEmpty, "", false)
	})

	// 2. Formatted phone canonicalization on write, verified-only uniqueness, Register DoS prevention,
	//    same-phone format-only UpdateProfile preserving phone_verified, and concurrent +84 vs 0 race.
	t.Run("CanonicalWriteVerifiedUniquenessAndPlus84Vs0Race", func(t *testing.T) {
		ctx := context.Background()
		pool, _ := openTempUserPGPool(t)
		if err := db.RunMigrations(ctx, pool); err != nil {
			t.Fatalf("RunMigrations: %v", err)
		}
		clk := newW2BClock(time.Date(2026, 11, 24, 19, 0, 0, 0, time.UTC))
		srv, pub, _ := newPGServerWithLog(t, pool, box, clk)
		if memSec, ok := srv.sec.(*memSecurity); ok {
			memSec.nowFn = clk.Now
		}

		// 2a. User A registers with formatted "090-123-4567" -> DB stores canonical "0901234567".
		userA := registerActiveUser(t, srv, pub, "e19bis-a@example.com", "090-123-4567")
		ctxA := ctxWithUser(userA, RoleBuyer, "")
		var dbPhoneA string
		if err := pool.QueryRow(ctx, `SELECT phone FROM users WHERE id = $1`, userA).Scan(&dbPhoneA); err != nil {
			t.Fatalf("query A phone: %v", err)
		}
		if dbPhoneA != "0901234567" {
			t.Fatalf("Register formatted phone stored %q; want canonical 0901234567", dbPhoneA)
		}

		// 2b. Attacker calls Register with A's UNVERIFIED phone (both "+84901234567" and "0901234567") ->
		//     MUST succeed (no unverified squatting) AND MUST NOT block A from verifying!
		if _, err := srv.Register(ctx, &userv1.RegisterRequest{
			FullName: "Squatter Unverified",
			Email:    "e19bis-squat@example.com",
			Phone:    "+84 901-234-567",
			Password: "Password123!",
		}); err != nil {
			t.Fatalf("Register with another user's unverified phone should succeed, got %v", err)
		}

		// Even if A has raw formatted phone sitting in DB (e.g. legacy row), VerifyPhoneOTP MUST canonicalize DB to "0901234567".
		if _, err := pool.Exec(ctx, `UPDATE users SET phone = '090-123-4567' WHERE id = $1`, userA); err != nil {
			t.Fatalf("set raw phone on A: %v", err)
		}
		if _, err := srv.RequestPhoneOTP(ctxA, &userv1.RequestPhoneOTPRequest{}); err != nil {
			t.Fatalf("A RequestPhoneOTP must not be blocked by unverified squatter: %v", err)
		}
		otpA := pub.phoneOTPRequested[len(pub.phoneOTPRequested)-1].OTP
		verA, err := srv.VerifyPhoneOTP(ctxA, &userv1.VerifyPhoneOTPRequest{
			Phone: "090-123-4567",
			Otp:   otpA,
		})
		if err != nil || !verA.GetVerified() || verA.GetUser().GetPhone() != "0901234567" || !verA.GetUser().GetPhoneVerified() {
			t.Fatalf("A VerifyPhoneOTP: err=%v resp=%+v", err, verA)
		}
		if err := pool.QueryRow(ctx, `SELECT phone FROM users WHERE id = $1`, userA).Scan(&dbPhoneA); err != nil {
			t.Fatalf("query A canonical phone after verify: %v", err)
		}
		if dbPhoneA != "0901234567" {
			t.Fatalf("DB phone for A after verify = %q; want 0901234567", dbPhoneA)
		}

		// 2c. UpdateProfile with SAME phone in another format ("+84 901 234 567") MUST keep phone_verified=true and canonical DB phone!
		updSame, err := srv.UpdateProfile(ctxA, &userv1.UpdateProfileRequest{Phone: "+84 901 234 567"})
		if err != nil {
			t.Fatalf("UpdateProfile same phone +84 format: %v", err)
		}
		if !updSame.GetUser().GetPhoneVerified() || updSame.GetUser().GetPhone() != "0901234567" {
			t.Fatalf("UpdateProfile same phone format lost phone_verified or mutated phone: %+v", updSame.GetUser())
		}

		// 2d. Register with A's VERIFIED phone (0 or +84 form) -> AlreadyExists ("phone is already registered").
		for _, dupPhone := range []string{"0901234567", "+84901234567", "090.123.4567"} {
			_, regErr := srv.Register(ctx, &userv1.RegisterRequest{
				FullName: "Late Dup",
				Email:    "e19bis-dup-" + uuid.NewString()[:6] + "@example.com",
				Phone:    dupPhone,
				Password: "Password123!",
			})
			if status.Code(regErr) != codes.AlreadyExists || !strings.Contains(status.Convert(regErr).Message(), "phone is already registered") {
				t.Fatalf("Register with verified phone %q: want AlreadyExists 'phone is already registered', got %v", dupPhone, regErr)
			}
		}

		// 2e. User B requesting/verifying A's verified phone (0901234567 or +84901234567) -> AlreadyExists.
		userB := registerActiveUser(t, srv, pub, "e19bis-b@example.com", "")
		ctxB := ctxWithUser(userB, RoleBuyer, "")
		for _, dupPhone := range []string{"0901234567", "+84901234567"} {
			if _, err := srv.RequestPhoneOTP(ctxB, &userv1.RequestPhoneOTPRequest{Phone: dupPhone}); status.Code(err) != codes.AlreadyExists {
				t.Fatalf("B RequestPhoneOTP(%q): want AlreadyExists, got %v", dupPhone, err)
			}
			if _, err := srv.VerifyPhoneOTP(ctxB, &userv1.VerifyPhoneOTPRequest{Phone: dupPhone, Otp: "123456"}); status.Code(err) != codes.AlreadyExists {
				t.Fatalf("B VerifyPhoneOTP(%q): want AlreadyExists, got %v", dupPhone, err)
			}
		}

		// 2f. Concurrent race: 2 users verifying the SAME phone simultaneously — one with "+84909777666", one with "0909777666" ->
		//     strictly 1 succeeds (stored as "0909777666", phone_verified=true) and 1 gets AlreadyExists!
		r1 := registerActiveUser(t, srv, pub, "e19bis-race1@example.com", "+84909777666")
		r2 := registerActiveUser(t, srv, pub, "e19bis-race2@example.com", "090-977-7666")
		ctxR1 := ctxWithUser(r1, RoleBuyer, "")
		ctxR2 := ctxWithUser(r2, RoleBuyer, "")
		if _, err := srv.RequestPhoneOTP(ctxR1, &userv1.RequestPhoneOTPRequest{Phone: "+84909777666"}); err != nil {
			t.Fatalf("r1 RequestPhoneOTP: %v", err)
		}
		otpR1 := pub.phoneOTPRequested[len(pub.phoneOTPRequested)-1].OTP
		if _, err := srv.RequestPhoneOTP(ctxR2, &userv1.RequestPhoneOTPRequest{Phone: "0909777666"}); err != nil {
			t.Fatalf("r2 RequestPhoneOTP: %v", err)
		}
		otpR2 := pub.phoneOTPRequested[len(pub.phoneOTPRequested)-1].OTP

		start := make(chan struct{})
		errs := make([]error, 2)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, errs[0] = srv.VerifyPhoneOTP(ctxR1, &userv1.VerifyPhoneOTPRequest{Phone: "+84909777666", Otp: otpR1})
		}()
		go func() {
			defer wg.Done()
			<-start
			_, errs[1] = srv.VerifyPhoneOTP(ctxR2, &userv1.VerifyPhoneOTPRequest{Phone: "0909777666", Otp: otpR2})
		}()
		close(start)
		wg.Wait()

		var okCnt, dupCnt int
		for _, e := range errs {
			switch {
			case e == nil:
				okCnt++
			case status.Code(e) == codes.AlreadyExists:
				dupCnt++
			default:
				t.Fatalf("unexpected +84 vs 0 race error: %v", e)
			}
		}
		if okCnt != 1 || dupCnt != 1 {
			t.Fatalf("+84 vs 0 race: want 1 ok + 1 AlreadyExists, got ok=%d dup=%d", okCnt, dupCnt)
		}

		var verifiedOwners int64
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE phone = '0909777666' AND phone_verified = TRUE`).Scan(&verifiedOwners); err != nil {
			t.Fatalf("count verified owners for 0909777666: %v", err)
		}
		if verifiedOwners != 1 {
			t.Fatalf("expected strictly 1 verified owner for 0909777666, got %d", verifiedOwners)
		}
	})
}
