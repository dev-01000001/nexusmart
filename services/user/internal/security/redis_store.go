package security

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	defaultOTPMaxAttempts = 5
	resendWindow          = 15 * time.Minute
)

// verifyOTPScript atomically checks OTP digest, increments attempts on failure,
// and deletes the OTP once max_attempts is reached to prevent concurrent brute-force races.
// Returns:
//
//	 1: matched (OTP, attempts, and resend keys deleted)
//	 0: mismatch (attempt incremented, still under limit)
//	-1: not found / expired
//	-2: too many invalid attempts (OTP and attempts deleted)
const verifyOTPScript = `
local otp_key = KEYS[1]
local attempts_key = KEYS[2]
local resend_key = KEYS[3]
local expected_digest = ARGV[1]
local max_attempts = tonumber(ARGV[2])

local stored = redis.call('GET', otp_key)
if not stored then
  return -1
end
if stored == expected_digest then
  redis.call('DEL', otp_key, attempts_key, resend_key)
  return 1
end
local attempts = redis.call('INCR', attempts_key)
if attempts == 1 then
  local pttl = redis.call('PTTL', otp_key)
  if pttl > 0 then
    redis.call('PEXPIRE', attempts_key, pttl)
  end
end
if attempts >= max_attempts then
  redis.call('DEL', otp_key, attempts_key)
  return -2
end
return 0
`

var (
	ErrOTPNotFound               = errors.New("otp not found or expired")
	ErrOTPInvalid                = errors.New("invalid otp")
	ErrOTPTooManyAttempts        = errors.New("too many invalid otp attempts")
	ErrOTPResendLimitReached     = errors.New("otp resend limit exceeded")
	ErrPasswordResetLimitReached = errors.New("too many password reset requests")
)

// Store manages OTP codes, password reset rate limits, login brute-force lockouts, and token revocations.
type Store interface {
	SaveOTP(ctx context.Context, email, code string, ttl time.Duration) error
	VerifyOTP(ctx context.Context, email, code string) error
	IncrementOTPResend(ctx context.Context, email string, maxResends int) error
	IncrementPasswordResetRequest(ctx context.Context, email string, maxRequests int, window time.Duration) error

	IsLoginLocked(ctx context.Context, email string) (bool, error)
	RecordLoginFailure(ctx context.Context, email string, maxAttempts int, lockout time.Duration) (locked bool, attempts int, err error)
	ClearLoginFailures(ctx context.Context, email string) error

	RevokeJTI(ctx context.Context, jti string, ttl time.Duration) error
	IsJTIRevoked(ctx context.Context, jti string) (bool, error)
	SetRevokedBefore(ctx context.Context, userID string, unixSec int64, ttl time.Duration) error
	GetRevokedBefore(ctx context.Context, userID string) (int64, error)
	IsTokenRevoked(ctx context.Context, userID, jti string, issuedAt *time.Time) (bool, error)
	Ping(ctx context.Context) error
}

// RedisStore implements Store backed by Redis.
type RedisStore struct {
	client         *redis.Client
	verifyScript   *redis.Script
	otpMaxAttempts int
}

// NewRedisStore creates a Redis-backed security store.
func NewRedisStore(client *redis.Client) *RedisStore {
	return &RedisStore{
		client:         client,
		verifyScript:   redis.NewScript(verifyOTPScript),
		otpMaxAttempts: defaultOTPMaxAttempts,
	}
}

func otpKey(email string) string {
	return "user:otp:" + strings.ToLower(strings.TrimSpace(email))
}

func otpAttemptsKey(email string) string {
	return "user:otp_attempts:" + strings.ToLower(strings.TrimSpace(email))
}

func otpResendKey(email string) string {
	return "user:otp_resend:" + strings.ToLower(strings.TrimSpace(email))
}

func loginFailKey(email string) string {
	return "user:login_fail:" + strings.ToLower(strings.TrimSpace(email))
}

func loginLockKey(email string) string {
	return "user:login_lock:" + strings.ToLower(strings.TrimSpace(email))
}

func revokedJTIKey(jti string) string {
	return "user:revoked_jti:" + jti
}

func revokedBeforeKey(userID string) string {
	return "user:revoked_before:" + strings.TrimSpace(userID)
}

func passwordResetReqKey(email string) string {
	return "user:pwd_reset_req:" + strings.ToLower(strings.TrimSpace(email))
}

func hashOTP(email, code string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email)) + ":" + strings.TrimSpace(code)))
	return hex.EncodeToString(sum[:])
}

func (s *RedisStore) SaveOTP(ctx context.Context, email, code string, ttl time.Duration) error {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	pipe := s.client.TxPipeline()
	pipe.Set(ctx, otpKey(email), hashOTP(email, code), ttl)
	pipe.Del(ctx, otpAttemptsKey(email))
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("save otp: %w", err)
	}
	return nil
}

func (s *RedisStore) VerifyOTP(ctx context.Context, email, code string) error {
	res, err := s.verifyScript.Run(
		ctx,
		s.client,
		[]string{otpKey(email), otpAttemptsKey(email), otpResendKey(email)},
		hashOTP(email, code),
		s.otpMaxAttempts,
	).Int64()
	if err != nil {
		return fmt.Errorf("verify otp lua: %w", err)
	}
	return mapVerifyOTPResult(res)
}

func mapVerifyOTPResult(res int64) error {
	switch res {
	case 1:
		return nil
	case -1:
		return ErrOTPNotFound
	case -2:
		return ErrOTPTooManyAttempts
	default:
		return ErrOTPInvalid
	}
}

func (s *RedisStore) IncrementOTPResend(ctx context.Context, email string, maxResends int) error {
	if maxResends <= 0 {
		maxResends = 3
	}
	key := otpResendKey(email)
	count, err := s.client.Incr(ctx, key).Result()
	if err != nil {
		return fmt.Errorf("incr otp resend: %w", err)
	}
	if count == 1 {
		_ = s.client.Expire(ctx, key, resendWindow).Err()
	}
	if int(count) > maxResends {
		return ErrOTPResendLimitReached
	}
	return nil
}

func (s *RedisStore) IsLoginLocked(ctx context.Context, email string) (bool, error) {
	exists, err := s.client.Exists(ctx, loginLockKey(email)).Result()
	if err != nil {
		return false, fmt.Errorf("check login lock: %w", err)
	}
	return exists > 0, nil
}

func (s *RedisStore) RecordLoginFailure(ctx context.Context, email string, maxAttempts int, lockout time.Duration) (bool, int, error) {
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	if lockout <= 0 {
		lockout = 15 * time.Minute
	}

	failKey := loginFailKey(email)
	count, err := s.client.Incr(ctx, failKey).Result()
	if err != nil {
		return false, 0, fmt.Errorf("incr login fail: %w", err)
	}
	if count == 1 {
		_ = s.client.Expire(ctx, failKey, lockout).Err()
	}
	if int(count) >= maxAttempts {
		if err := s.client.Set(ctx, loginLockKey(email), "1", lockout).Err(); err != nil {
			return false, int(count), fmt.Errorf("set login lock: %w", err)
		}
		_ = s.client.Del(ctx, failKey).Err()
		return true, int(count), nil
	}
	return false, int(count), nil
}

func (s *RedisStore) ClearLoginFailures(ctx context.Context, email string) error {
	return s.client.Del(ctx, loginFailKey(email), loginLockKey(email)).Err()
}

func (s *RedisStore) RevokeJTI(ctx context.Context, jti string, ttl time.Duration) error {
	if strings.TrimSpace(jti) == "" {
		return nil
	}
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	return s.client.Set(ctx, revokedJTIKey(jti), "1", ttl).Err()
}

func (s *RedisStore) IsJTIRevoked(ctx context.Context, jti string) (bool, error) {
	if strings.TrimSpace(jti) == "" {
		return false, nil
	}
	exists, err := s.client.Exists(ctx, revokedJTIKey(jti)).Result()
	if err != nil {
		return false, fmt.Errorf("check revoked jti: %w", err)
	}
	return exists > 0, nil
}

func (s *RedisStore) IncrementPasswordResetRequest(ctx context.Context, email string, maxRequests int, window time.Duration) error {
	if maxRequests <= 0 {
		maxRequests = 3
	}
	if window <= 0 {
		window = time.Hour
	}
	key := passwordResetReqKey(email)
	count, err := s.client.Incr(ctx, key).Result()
	if err != nil {
		return fmt.Errorf("incr password reset request: %w", err)
	}
	if count == 1 {
		_ = s.client.Expire(ctx, key, window).Err()
	}
	if int(count) > maxRequests {
		return ErrPasswordResetLimitReached
	}
	return nil
}

func (s *RedisStore) SetRevokedBefore(ctx context.Context, userID string, unixSec int64, ttl time.Duration) error {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil
	}
	if ttl <= 0 {
		ttl = 16 * time.Minute
	}
	if err := s.client.Set(ctx, revokedBeforeKey(userID), strconv.FormatInt(unixSec, 10), ttl).Err(); err != nil {
		return fmt.Errorf("set revoked_before: %w", err)
	}
	return nil
}

func (s *RedisStore) GetRevokedBefore(ctx context.Context, userID string) (int64, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return 0, nil
	}
	val, err := s.client.Get(ctx, revokedBeforeKey(userID)).Result()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("get revoked_before: %w", err)
	}
	ts, err := strconv.ParseInt(strings.TrimSpace(val), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse revoked_before: %w", err)
	}
	return ts, nil
}

func (s *RedisStore) IsTokenRevoked(ctx context.Context, userID, jti string, issuedAt *time.Time) (bool, error) {
	userID = strings.TrimSpace(userID)
	jti = strings.TrimSpace(jti)
	if userID == "" && jti == "" {
		return false, nil
	}
	keys := make([]string, 0, 2)
	jtiIdx, userIdx := -1, -1
	if jti != "" {
		jtiIdx = len(keys)
		keys = append(keys, revokedJTIKey(jti))
	}
	if userID != "" {
		userIdx = len(keys)
		keys = append(keys, revokedBeforeKey(userID))
	}
	vals, err := s.client.MGet(ctx, keys...).Result()
	if err != nil {
		return false, fmt.Errorf("check token revocation: %w", err)
	}
	var jtiVal, revokedBeforeVal any
	if jtiIdx >= 0 && jtiIdx < len(vals) {
		jtiVal = vals[jtiIdx]
	}
	if userIdx >= 0 && userIdx < len(vals) {
		revokedBeforeVal = vals[userIdx]
	}
	return evalTokenRevocation(jtiVal, revokedBeforeVal, issuedAt)
}

func evalTokenRevocation(jtiVal, revokedBeforeVal any, issuedAt *time.Time) (bool, error) {
	if jtiVal != nil {
		return true, nil
	}
	if revokedBeforeVal == nil {
		return false, nil
	}
	raw, ok := revokedBeforeVal.(string)
	if !ok {
		return false, fmt.Errorf("unexpected revoked_before value type %T", revokedBeforeVal)
	}
	revokedBefore, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return false, fmt.Errorf("parse revoked_before: %w", err)
	}
	if issuedAt == nil {
		return true, nil
	}
	return issuedAt.Unix() <= revokedBefore, nil
}

func (s *RedisStore) Ping(ctx context.Context) error {
	return s.client.Ping(ctx).Err()
}
