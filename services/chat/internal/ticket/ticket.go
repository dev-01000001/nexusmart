package ticket

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/redis/go-redis/v9"
)

const (
	ticketKeyPrefix        = "chat:ws_ticket:"
	revokedJTIKeyPrefix    = "user:revoked_jti:"
	revokedBeforeKeyPrefix = "user:revoked_before:"
	defaultTicketTTL       = 60 * time.Second
	wsTokenTTL             = 60 * time.Second
	ticketEntropyBytes     = 32
)

var (
	ErrInvalidTicket = errors.New("invalid, expired, or already used ws ticket")
	ErrRevokedToken  = errors.New("session token has been revoked")
)

type storedPayload struct {
	UserID   string `json:"user_id"`
	Role     string `json:"role"`
	ShopID   string `json:"shop_id,omitempty"`
	JTI      string `json:"jti,omitempty"`
	IssuedAt int64  `json:"iat,omitempty"`
}

// Redeemed holds the authenticated identity and freshly minted short-lived WS JWT.
type Redeemed struct {
	UserID  string
	Role    string
	ShopID  string
	JTI     string
	WSToken string
}

// Manager issues and atomically redeems short-lived single-use WebSocket tickets via Redis.
type Manager struct {
	rdb        *redis.Client
	wsTokenMgr *auth.Manager
	ttl        time.Duration
}

// NewManager creates a single-use WS ticket manager.
// jwtSecret must match the shared JWT_SECRET so the gateway and chat WS server both verify ws_token.
func NewManager(rdb *redis.Client, jwtSecret string, ttl time.Duration) (*Manager, error) {
	if rdb == nil {
		return nil, errors.New("redis client is required for ticket manager")
	}
	if ttl <= 0 {
		ttl = defaultTicketTTL
	}
	wsMgr, err := auth.NewManager(jwtSecret, wsTokenTTL)
	if err != nil {
		return nil, fmt.Errorf("init ws token manager: %w", err)
	}
	return &Manager{
		rdb:        rdb,
		wsTokenMgr: wsMgr,
		ttl:        ttl,
	}, nil
}

// Create generates a 256-bit random base64url ticket and stores its SHA-256 hash in Redis with TTL.
func (m *Manager) Create(ctx context.Context, claims *auth.Claims) (string, time.Time, error) {
	if claims == nil || strings.TrimSpace(claims.UserID()) == "" {
		return "", time.Time{}, auth.ErrMissingClaims
	}

	rawBytes := make([]byte, ticketEntropyBytes)
	if _, err := rand.Read(rawBytes); err != nil {
		return "", time.Time{}, fmt.Errorf("generate ticket entropy: %w", err)
	}
	rawTicket := base64.RawURLEncoding.EncodeToString(rawBytes)

	var issuedAt int64
	if claims.IssuedAt != nil && !claims.IssuedAt.Time.IsZero() {
		issuedAt = claims.IssuedAt.Unix()
	}

	payload := storedPayload{
		UserID:   claims.UserID(),
		Role:     claims.Role,
		ShopID:   claims.ShopID,
		JTI:      claims.JTI(),
		IssuedAt: issuedAt,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("marshal ticket payload: %w", err)
	}

	key := hashKey(rawTicket)
	expiresAt := time.Now().UTC().Add(m.ttl)
	if err := m.rdb.Set(ctx, key, encoded, m.ttl).Err(); err != nil {
		return "", time.Time{}, fmt.Errorf("store ws ticket in redis: %w", err)
	}
	return rawTicket, expiresAt, nil
}

// Redeem atomically consumes rawTicket using Redis GETDEL (single-use!), verifies JTI revocation,
// and mints a short-lived 60s HS256 ws_token.
func (m *Manager) Redeem(ctx context.Context, rawTicket string) (Redeemed, error) {
	rawTicket = strings.TrimSpace(rawTicket)
	if rawTicket == "" {
		return Redeemed{}, ErrInvalidTicket
	}

	key := hashKey(rawTicket)
	rawPayload, err := m.rdb.GetDel(ctx, key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return Redeemed{}, ErrInvalidTicket
		}
		return Redeemed{}, fmt.Errorf("redeem ws ticket: %w", err)
	}

	var payload storedPayload
	if err := json.Unmarshal([]byte(rawPayload), &payload); err != nil || strings.TrimSpace(payload.UserID) == "" {
		return Redeemed{}, ErrInvalidTicket
	}

	checkClaims := &auth.Claims{
		Role:   payload.Role,
		ShopID: payload.ShopID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: payload.UserID,
			ID:      payload.JTI,
		},
	}
	if payload.IssuedAt > 0 {
		checkClaims.IssuedAt = jwt.NewNumericDate(time.Unix(payload.IssuedAt, 0).UTC())
	}
	revoked, err := m.IsTokenRevoked(ctx, checkClaims)
	if err != nil {
		return Redeemed{}, err
	}
	if revoked {
		return Redeemed{}, ErrRevokedToken
	}

	wsToken, _, _, err := m.wsTokenMgr.SignAccessToken(payload.UserID, payload.Role, payload.ShopID)
	if err != nil {
		return Redeemed{}, fmt.Errorf("sign ws token: %w", err)
	}

	return Redeemed{
		UserID:  payload.UserID,
		Role:    payload.Role,
		ShopID:  payload.ShopID,
		JTI:     payload.JTI,
		WSToken: wsToken,
	}, nil
}

// IsTokenRevoked checks both per-token JTI revocation (`user:revoked_jti:<jti>`) and
// global per-user revocation (`user:revoked_before:<uid>`) in a single Redis MGET round-trip,
// failing closed if `revoked_before` is set while `iat` is missing or unparseable.
func (m *Manager) IsTokenRevoked(ctx context.Context, claims *auth.Claims) (bool, error) {
	if claims == nil {
		return false, nil
	}
	jti := strings.TrimSpace(claims.JTI())
	userID := strings.TrimSpace(claims.UserID())

	keys := make([]string, 0, 2)
	jtiIdx, beforeIdx := -1, -1
	if jti != "" {
		jtiIdx = len(keys)
		keys = append(keys, revokedJTIKeyPrefix+jti)
	}
	if userID != "" {
		beforeIdx = len(keys)
		keys = append(keys, revokedBeforeKeyPrefix+userID)
	}
	if len(keys) == 0 {
		return false, nil
	}

	vals, err := m.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return false, fmt.Errorf("check token revocation: %w", err)
	}

	if jtiIdx >= 0 && jtiIdx < len(vals) && vals[jtiIdx] != nil {
		return true, nil
	}
	if beforeIdx >= 0 && beforeIdx < len(vals) && vals[beforeIdx] != nil {
		if claims.IssuedAt == nil || claims.IssuedAt.Time.IsZero() {
			return true, nil
		}
		raw := strings.TrimSpace(fmt.Sprint(vals[beforeIdx]))
		revokedBefore, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil {
			return true, nil
		}
		if claims.IssuedAt.Unix() <= revokedBefore {
			return true, nil
		}
	}
	return false, nil
}

// IsJTIRevoked checks whether a user's access token JTI has been revoked in Redis.
func (m *Manager) IsJTIRevoked(ctx context.Context, jti string) (bool, error) {
	jti = strings.TrimSpace(jti)
	if jti == "" {
		return false, nil
	}
	exists, err := m.rdb.Exists(ctx, revokedJTIKeyPrefix+jti).Result()
	if err != nil {
		return false, fmt.Errorf("check revoked jti: %w", err)
	}
	return exists > 0, nil
}

func hashKey(rawTicket string) string {
	sum := sha256.Sum256([]byte(rawTicket))
	return ticketKeyPrefix + hex.EncodeToString(sum[:])
}
