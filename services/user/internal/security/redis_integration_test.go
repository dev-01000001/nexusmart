package security

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func openTestRedisClient(t *testing.T) *redis.Client {
	t.Helper()
	addr := strings.TrimSpace(os.Getenv("TEST_REDIS_ADDR"))
	if addr == "" {
		t.Skip("set TEST_REDIS_ADDR to run real Redis Lua integration tests")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = rdb.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Fatalf("ping redis %s: %v", addr, err)
	}
	if err := rdb.FlushDB(ctx).Err(); err != nil {
		t.Fatalf("flushdb redis: %v", err)
	}
	return rdb
}

func TestRedisStore_PhoneOTP_RealRedisLuaAtomicityAndLimits(t *testing.T) {
	ctx := context.Background()
	rdb := openTestRedisClient(t)
	store := NewRedisStore(rdb)

	// 1. SavePhoneOTP stores only SHA-256 digest (no plaintext OTP/phone), enforces cooldown, and respects 5/hour cap.
	uid := "user-redis-1"
	if retry, err := store.SavePhoneOTP(ctx, uid, "0901234567", "0901234567", "123456", 5*time.Minute, 500*time.Millisecond, 5, time.Hour); err != nil || retry != 0 {
		t.Fatalf("1st SavePhoneOTP: retry=%v err=%v", retry, err)
	}
	rawStored, err := rdb.Get(ctx, phoneOTPKey(uid)).Result()
	if err != nil {
		t.Fatalf("GET otp key: %v", err)
	}
	if len(rawStored) != 64 || strings.Contains(rawStored, "123456") || strings.Contains(rawStored, "0901234567") {
		t.Fatalf("redis otp value leaked raw data or wrong format: len=%d", len(rawStored))
	}

	// Retry inside cooldown -> ErrPhoneOTPCooldown and hourly count stays 1 (does not overwrite existing OTP).
	if retry, err := store.SavePhoneOTP(ctx, uid, "0901234567", "0901234567", "654321", 5*time.Minute, 500*time.Millisecond, 5, time.Hour); !errors.Is(err, ErrPhoneOTPCooldown) || retry <= 0 {
		t.Fatalf("cooldown SavePhoneOTP: want ErrPhoneOTPCooldown with retry>0, got retry=%v err=%v", retry, err)
	}
	if cnt, err := rdb.Get(ctx, phoneOTPHourlyKey(uid)).Int(); err != nil || cnt != 1 {
		t.Fatalf("hourly count after cooldown rejection = %d (err=%v); want 1", cnt, err)
	}

	// Correct OTP verifies cleanly and deletes both otp and attempts keys; replay fails.
	if err := store.VerifyPhoneOTP(ctx, uid, "0901234567", "0901234567", "123456", 5); err != nil {
		t.Fatalf("VerifyPhoneOTP valid: %v", err)
	}
	if exists, _ := rdb.Exists(ctx, phoneOTPKey(uid), phoneOTPAttemptsKey(uid)).Result(); exists != 0 {
		t.Fatalf("expected otp + attempts keys deleted on success, got %d remaining", exists)
	}
	if err := store.VerifyPhoneOTP(ctx, uid, "0901234567", "0901234567", "123456", 5); !errors.Is(err, ErrOTPNotFound) {
		t.Fatalf("replay after verify: want ErrOTPNotFound, got %v", err)
	}

	// 2. RollbackPhoneOTP with wrong digest is a no-op; with matching digest clears otp, cooldown, and decrements hourly count.
	uidRB := "user-redis-rb"
	if _, err := store.SavePhoneOTP(ctx, uidRB, "0901234567", "0901234567", "123456", 5*time.Minute, 500*time.Millisecond, 5, time.Hour); err != nil {
		t.Fatalf("SavePhoneOTP rb: %v", err)
	}
	if err := store.RollbackPhoneOTP(ctx, uidRB, "0901234567", "0901234567", "999999"); err != nil {
		t.Fatalf("RollbackPhoneOTP mismatch: %v", err)
	}
	if exists, _ := rdb.Exists(ctx, phoneOTPKey(uidRB)).Result(); exists != 1 {
		t.Fatalf("mismatched rollback deleted otp key")
	}
	if err := store.RollbackPhoneOTP(ctx, uidRB, "0901234567", "0901234567", "123456"); err != nil {
		t.Fatalf("RollbackPhoneOTP match: %v", err)
	}
	if exists, _ := rdb.Exists(ctx, phoneOTPKey(uidRB), phoneOTPCooldownKey(uidRB), phoneOTPHourlyKey(uidRB)).Result(); exists != 0 {
		t.Fatalf("rollback left %d keys behind", exists)
	}

	// 3. Five sends with 0 cooldown succeed; 6th hits ErrPhoneOTPRateLimitReached.
	for i := 1; i <= 5; i++ {
		if _, err := store.SavePhoneOTP(ctx, uidRB, "0901234567", "0901234567", "111111", 5*time.Minute, 0, 5, time.Hour); err != nil {
			t.Fatalf("send %d/5: %v", i, err)
		}
	}
	if retry, err := store.SavePhoneOTP(ctx, uidRB, "0901234567", "0901234567", "222222", 5*time.Minute, 0, 5, time.Hour); !errors.Is(err, ErrPhoneOTPRateLimitReached) || retry <= 0 {
		t.Fatalf("6th send: want ErrPhoneOTPRateLimitReached, got retry=%v err=%v", retry, err)
	}

	// 4. Binding checks (wrong user, wrong phone, wrong profilePhone) and brute-force deletion at maxAttempts=5.
	uidBF := "user-redis-bf"
	if _, err := store.SavePhoneOTP(ctx, uidBF, "0902222222", "0902222222", "777888", 5*time.Minute, 0, 5, time.Hour); err != nil {
		t.Fatalf("SavePhoneOTP bf: %v", err)
	}
	// Wrong user must not match and must not consume uidBF's attempts.
	if err := store.VerifyPhoneOTP(ctx, "user-redis-other", "0902222222", "0902222222", "777888", 5); !errors.Is(err, ErrOTPNotFound) {
		t.Fatalf("wrong user verify: want ErrOTPNotFound, got %v", err)
	}
	// Attempt 1: wrong phone
	if err := store.VerifyPhoneOTP(ctx, uidBF, "0903333333", "0902222222", "777888", 5); !errors.Is(err, ErrOTPInvalid) {
		t.Fatalf("wrong phone verify: want ErrOTPInvalid, got %v", err)
	}
	// Attempt 2: changed profile phone
	if err := store.VerifyPhoneOTP(ctx, uidBF, "0902222222", "0903333333", "777888", 5); !errors.Is(err, ErrOTPInvalid) {
		t.Fatalf("changed profile phone verify: want ErrOTPInvalid, got %v", err)
	}
	// Attempts 3 & 4: wrong OTP -> attempts counter increments and retains TTL.
	for i := 3; i <= 4; i++ {
		if err := store.VerifyPhoneOTP(ctx, uidBF, "0902222222", "0902222222", "000000", 5); !errors.Is(err, ErrOTPInvalid) {
			t.Fatalf("attempt %d: want ErrOTPInvalid, got %v", i, err)
		}
		if gotAttempts, err := rdb.Get(ctx, phoneOTPAttemptsKey(uidBF)).Int(); err != nil || gotAttempts != i {
			t.Fatalf("attempt %d counter = %d (err=%v); want %d", i, gotAttempts, err, i)
		}
	}
	// Attempt 5: hits maxAttempts -> ErrOTPTooManyAttempts and deletes both keys!
	if err := store.VerifyPhoneOTP(ctx, uidBF, "0902222222", "0902222222", "000000", 5); !errors.Is(err, ErrOTPTooManyAttempts) {
		t.Fatalf("attempt 5: want ErrOTPTooManyAttempts, got %v", err)
	}
	if exists, _ := rdb.Exists(ctx, phoneOTPKey(uidBF), phoneOTPAttemptsKey(uidBF)).Result(); exists != 0 {
		t.Fatalf("expected otp + attempts keys deleted at maxAttempts, got %d remaining", exists)
	}
	// Attempt 6 with real OTP -> ErrOTPNotFound!
	if err := store.VerifyPhoneOTP(ctx, uidBF, "0902222222", "0902222222", "777888", 5); !errors.Is(err, ErrOTPNotFound) {
		t.Fatalf("attempt 6 with real OTP after 5 wrong: want ErrOTPNotFound, got %v", err)
	}

	// 5a. Concurrent single-use race on real Redis (2 goroutines): strictly 1 nil, 1 ErrOTPNotFound.
	uidRace2 := "user-redis-race2"
	if _, err := store.SavePhoneOTP(ctx, uidRace2, "0904444441", "0904444441", "242424", 5*time.Minute, 0, 5, time.Hour); err != nil {
		t.Fatalf("SavePhoneOTP race2: %v", err)
	}
	start2 := make(chan struct{})
	errs2 := make([]error, 2)
	var wg2 sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg2.Add(1)
		go func(idx int) {
			defer wg2.Done()
			<-start2
			errs2[idx] = store.VerifyPhoneOTP(ctx, uidRace2, "0904444441", "0904444441", "242424", 5)
		}(i)
	}
	close(start2)
	wg2.Wait()
	var ok2, nf2 int
	for _, err := range errs2 {
		switch {
		case err == nil:
			ok2++
		case errors.Is(err, ErrOTPNotFound):
			nf2++
		default:
			t.Fatalf("unexpected 2-way concurrent redis verify error: %v", err)
		}
	}
	if ok2 != 1 || nf2 != 1 {
		t.Fatalf("2-way concurrent real-Redis VerifyPhoneOTP: want 1 ok + 1 ErrOTPNotFound, got ok=%d notFound=%d", ok2, nf2)
	}

	// 5b. Concurrent single-use race on real Redis (20 goroutines): strictly 1 nil, 19 ErrOTPNotFound.
	uidRace := "user-redis-race"
	if _, err := store.SavePhoneOTP(ctx, uidRace, "0904444444", "0904444444", "424242", 5*time.Minute, 0, 5, time.Hour); err != nil {
		t.Fatalf("SavePhoneOTP race: %v", err)
	}
	const workers = 20
	start := make(chan struct{})
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			errs[idx] = store.VerifyPhoneOTP(ctx, uidRace, "0904444444", "0904444444", "424242", 5)
		}(i)
	}
	close(start)
	wg.Wait()
	var okCnt, notFoundCnt int
	for _, err := range errs {
		switch {
		case err == nil:
			okCnt++
		case errors.Is(err, ErrOTPNotFound):
			notFoundCnt++
		default:
			t.Fatalf("unexpected concurrent redis verify error: %v", err)
		}
	}
	if okCnt != 1 || notFoundCnt != workers-1 {
		t.Fatalf("concurrent real-Redis VerifyPhoneOTP: want 1 ok + %d ErrOTPNotFound, got ok=%d notFound=%d", workers-1, okCnt, notFoundCnt)
	}

	// 6. DeletePhoneOTP removes otp + attempts but preserves cooldown + hourly quota.
	uidDel := "user-redis-del"
	if _, err := store.SavePhoneOTP(ctx, uidDel, "0905555555", "0905555555", "515151", 5*time.Minute, 10*time.Second, 5, time.Hour); err != nil {
		t.Fatalf("SavePhoneOTP del: %v", err)
	}
	if err := store.DeletePhoneOTP(ctx, uidDel); err != nil {
		t.Fatalf("DeletePhoneOTP: %v", err)
	}
	if err := store.VerifyPhoneOTP(ctx, uidDel, "0905555555", "0905555555", "515151", 5); !errors.Is(err, ErrOTPNotFound) {
		t.Fatalf("VerifyPhoneOTP after DeletePhoneOTP: want ErrOTPNotFound, got %v", err)
	}
	if _, err := store.SavePhoneOTP(ctx, uidDel, "0906666666", "0906666666", "616161", 5*time.Minute, 10*time.Second, 5, time.Hour); !errors.Is(err, ErrPhoneOTPCooldown) {
		t.Fatalf("DeletePhoneOTP must NOT clear cooldown: want ErrPhoneOTPCooldown, got %v", err)
	}

	// 7. Short TTL expiration on real Redis.
	uidTTL := "user-redis-ttl"
	if _, err := store.SavePhoneOTP(ctx, uidTTL, "0907777777", "0907777777", "717171", 150*time.Millisecond, 0, 5, time.Hour); err != nil {
		t.Fatalf("SavePhoneOTP ttl: %v", err)
	}
	time.Sleep(250 * time.Millisecond)
	if err := store.VerifyPhoneOTP(ctx, uidTTL, "0907777777", "0907777777", "717171", 5); !errors.Is(err, ErrOTPNotFound) {
		t.Fatalf("expired real-Redis OTP: want ErrOTPNotFound, got %v", err)
	}
}
