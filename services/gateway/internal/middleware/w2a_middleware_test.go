package middleware

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/gateway/internal/httpx"
	"github.com/redis/go-redis/v9"
)

// fakeRESPServer speaks minimal Redis RESP2 (HELLO, CLIENT, MGET, PING) so tests
// verify the exact single MGET command sent by go-redis without an external Redis process.
type fakeRESPServer struct {
	ln       net.Listener
	mu       sync.Mutex
	store    map[string]string
	commands [][]string
	failNext bool
}

func newFakeRESPServer(t *testing.T, initial map[string]string) *fakeRESPServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen fake redis: %v", err)
	}
	s := &fakeRESPServer{
		ln:    ln,
		store: make(map[string]string),
	}
	for k, v := range initial {
		s.store[k] = v
	}
	go s.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

func (s *fakeRESPServer) Addr() string { return s.ln.Addr().String() }

func (s *fakeRESPServer) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handleConn(conn)
	}
}

func (s *fakeRESPServer) handleConn(conn net.Conn) {
	defer conn.Close()
	br := bufio.NewReader(conn)
	for {
		args, err := readRESPArray(br)
		if err != nil {
			return
		}
		if len(args) == 0 {
			continue
		}
		cmd := strings.ToUpper(args[0])
		switch cmd {
		case "HELLO":
			_, _ = io.WriteString(conn, "-ERR unknown command 'HELLO'\r\n")
		case "CLIENT":
			_, _ = io.WriteString(conn, "+OK\r\n")
		case "PING":
			_, _ = io.WriteString(conn, "+PONG\r\n")
		case "MGET":
			s.mu.Lock()
			s.commands = append(s.commands, append([]string(nil), args...))
			fail := s.failNext
			vals := make([]*string, len(args)-1)
			for i, k := range args[1:] {
				if v, ok := s.store[k]; ok {
					cp := v
					vals[i] = &cp
				}
			}
			s.mu.Unlock()

			if fail {
				_, _ = io.WriteString(conn, "-ERR simulated redis failure\r\n")
				continue
			}
			var out strings.Builder
			fmt.Fprintf(&out, "*%d\r\n", len(vals))
			for _, v := range vals {
				if v == nil {
					out.WriteString("$-1\r\n")
				} else {
					fmt.Fprintf(&out, "$%d\r\n%s\r\n", len(*v), *v)
				}
			}
			_, _ = io.WriteString(conn, out.String())
		default:
			s.mu.Lock()
			s.commands = append(s.commands, append([]string(nil), args...))
			s.mu.Unlock()
			_, _ = io.WriteString(conn, "-ERR unexpected command\r\n")
		}
	}
}

func readRESPArray(br *bufio.Reader) ([]string, error) {
	line, err := br.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	if !strings.HasPrefix(line, "*") {
		return nil, fmt.Errorf("expected *, got %q", line)
	}
	n, err := strconv.Atoi(line[1:])
	if err != nil {
		return nil, err
	}
	args := make([]string, 0, n)
	for i := 0; i < n; i++ {
		hdr, err := br.ReadString('\n')
		if err != nil {
			return nil, err
		}
		hdr = strings.TrimSuffix(strings.TrimSuffix(hdr, "\n"), "\r")
		if !strings.HasPrefix(hdr, "$") {
			return nil, fmt.Errorf("expected $, got %q", hdr)
		}
		size, err := strconv.Atoi(hdr[1:])
		if err != nil {
			return nil, err
		}
		buf := make([]byte, size+2)
		if _, err := io.ReadFull(br, buf); err != nil {
			return nil, err
		}
		args = append(args, string(buf[:size]))
	}
	return args, nil
}

func (s *fakeRESPServer) mgetCommands() [][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]string, len(s.commands))
	copy(out, s.commands)
	return out
}

func (s *fakeRESPServer) resetCommands() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commands = nil
}

func signTestTokenWithIAT(t *testing.T, secret, userID, role, jti string, iat *time.Time) string {
	t.Helper()
	claims := auth.Claims{
		Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			ID:        jti,
			ExpiresAt: jwt.NewNumericDate(time.Now().UTC().Add(15 * time.Minute)),
			Issuer:    "nexusmart",
		},
	}
	if iat != nil {
		claims.IssuedAt = jwt.NewNumericDate(*iat)
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return tok
}

func TestRedisRevocationCheckerMGETSingleCommandAndIATBoundary(t *testing.T) {
	const secret = "usr-b1-mget-secret"
	authMgr, err := auth.NewManager(secret, 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}

	cutoff := time.Now().UTC().Truncate(time.Second)
	beforeCutoff := cutoff.Add(-1 * time.Second)
	atCutoff := cutoff
	afterCutoff := cutoff.Add(1 * time.Second)

	srv := newFakeRESPServer(t, map[string]string{
		"user:revoked_jti:jti-revoked":    "1",
		"user:revoked_before:user-locked": strconv.FormatInt(cutoff.Unix(), 10),
	})
	rdb := redis.NewClient(&redis.Options{
		Addr:         srv.Addr(),
		Protocol:     2,
		MaxRetries:   0,
		DialTimeout:  time.Second,
		ReadTimeout:  time.Second,
		WriteTimeout: time.Second,
	})
	t.Cleanup(func() { _ = rdb.Close() })

	checker := NewRedisRevocationChecker(rdb)
	log := logger.New("gw-rev-test", logger.WithWriter(io.Discard))
	protected := Auth(authMgr, checker, log, true)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	cases := []struct {
		name       string
		userID     string
		jti        string
		iat        *time.Time
		wantStatus int
		wantKeys   []string
	}{
		{
			name:       "IATBeforeRevokedBefore_401",
			userID:     "user-locked",
			jti:        "jti-ok-1",
			iat:        &beforeCutoff,
			wantStatus: http.StatusUnauthorized,
			wantKeys:   []string{"user:revoked_jti:jti-ok-1", "user:revoked_before:user-locked"},
		},
		{
			name:       "IATEqualRevokedBefore_401",
			userID:     "user-locked",
			jti:        "jti-ok-2",
			iat:        &atCutoff,
			wantStatus: http.StatusUnauthorized,
			wantKeys:   []string{"user:revoked_jti:jti-ok-2", "user:revoked_before:user-locked"},
		},
		{
			name:       "IATAfterRevokedBefore_200",
			userID:     "user-locked",
			jti:        "jti-ok-3",
			iat:        &afterCutoff,
			wantStatus: http.StatusOK,
			wantKeys:   []string{"user:revoked_jti:jti-ok-3", "user:revoked_before:user-locked"},
		},
		{
			name:       "MissingIATWhenRevokedBeforeExists_401",
			userID:     "user-locked",
			jti:        "jti-ok-4",
			iat:        nil,
			wantStatus: http.StatusUnauthorized,
			wantKeys:   []string{"user:revoked_jti:jti-ok-4", "user:revoked_before:user-locked"},
		},
		{
			name:       "MissingIATWhenNoRevocationKey_200",
			userID:     "user-clean",
			jti:        "jti-ok-5",
			iat:        nil,
			wantStatus: http.StatusOK,
			wantKeys:   []string{"user:revoked_jti:jti-ok-5", "user:revoked_before:user-clean"},
		},
		{
			name:       "JTIRevokedEvenIfIATAfterCutoff_401",
			userID:     "user-locked",
			jti:        "jti-revoked",
			iat:        &afterCutoff,
			wantStatus: http.StatusUnauthorized,
			wantKeys:   []string{"user:revoked_jti:jti-revoked", "user:revoked_before:user-locked"},
		},
		{
			name:       "EmptyJTIStillChecksRevokedBefore_401",
			userID:     "user-locked",
			jti:        "",
			iat:        &atCutoff,
			wantStatus: http.StatusUnauthorized,
			wantKeys:   []string{"user:revoked_before:user-locked"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv.resetCommands()
			tok := signTestTokenWithIAT(t, secret, tc.userID, "buyer", tc.jti, tc.iat)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/users/me", nil)
			req.Header.Set("Authorization", "Bearer "+tok)
			rec := httptest.NewRecorder()
			protected.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("got HTTP %d, want %d (body=%s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			cmds := srv.mgetCommands()
			if len(cmds) != 1 {
				t.Fatalf("expected exactly 1 Redis MGET command, got %d: %v", len(cmds), cmds)
			}
			gotCmd := cmds[0]
			if strings.ToUpper(gotCmd[0]) != "MGET" {
				t.Fatalf("expected MGET command, got %v", gotCmd)
			}
			gotKeys := gotCmd[1:]
			if len(gotKeys) != len(tc.wantKeys) {
				t.Fatalf("MGET keys=%v, want %v", gotKeys, tc.wantKeys)
			}
			for i := range tc.wantKeys {
				if gotKeys[i] != tc.wantKeys[i] {
					t.Fatalf("MGET key[%d]=%q, want %q", i, gotKeys[i], tc.wantKeys[i])
				}
			}
		})
	}

	t.Run("RedisErrorFailsClosedWith503", func(t *testing.T) {
		srv.mu.Lock()
		srv.failNext = true
		srv.mu.Unlock()
		defer func() {
			srv.mu.Lock()
			srv.failNext = false
			srv.mu.Unlock()
		}()

		tok := signTestTokenWithIAT(t, secret, "user-clean", "buyer", "jti-err", &afterCutoff)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/users/me", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		protected.ServeHTTP(rec, req)

		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503 on Redis error, got %d (body=%s)", rec.Code, rec.Body.String())
		}
	})
}

func TestSecurityHeadersMiddleware(t *testing.T) {
	h := SecurityHeaders()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/categories", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("X-Frame-Options = %q, want DENY", got)
	}
	if got := rec.Header().Get("Referrer-Policy"); got == "" {
		t.Fatalf("expected non-empty Referrer-Policy header")
	}
	if got := rec.Header().Get("Content-Security-Policy"); !strings.Contains(got, "default-src 'none'") {
		t.Fatalf("Content-Security-Policy = %q, want minimal API CSP containing default-src 'none'", got)
	}
}

func TestMaxBodyBytesMiddleware413(t *testing.T) {
	h := MaxBodyBytes(64)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := httpx.DecodeJSON(r, &body); err != nil {
			httpx.WriteDecodeError(w, r, err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	}))

	t.Run("KnownContentLengthOverLimit", func(t *testing.T) {
		payload := `{"data":"` + strings.Repeat("a", 100) + `"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/test", strings.NewReader(payload))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("expected 413, got %d (body=%s)", rec.Code, rec.Body.String())
		}
		var env httpx.ErrorEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || env.Error.Code != "PAYLOAD_TOO_LARGE" {
			t.Fatalf("expected PAYLOAD_TOO_LARGE envelope, got %s", rec.Body.String())
		}
	})

	t.Run("ChunkedUnknownLengthOverLimit", func(t *testing.T) {
		payload := `{"data":"` + strings.Repeat("b", 100) + `"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/test", io.NopCloser(strings.NewReader(payload)))
		req.ContentLength = -1
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("expected 413 for chunked body over limit, got %d (body=%s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("WithinLimitSucceeds", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/test", strings.NewReader(`{"data":"ok"}`))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 within limit, got %d (%s)", rec.Code, rec.Body.String())
		}
	})
}

func TestAdminAuditRedactsSensitiveFields(t *testing.T) {
	authMgr, err := auth.NewManager("audit-redact-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	adminTok, _, _, _ := authMgr.SignAccessToken("admin-redact", "admin", "")

	var logBuf syncBuffer
	log := logger.New("audit-redact-test", logger.WithWriter(&logBuf))
	stub := &stubAuditUserClient{}

	r := chi.NewRouter()
	r.Use(Auth(authMgr, nil, log, true))
	r.Use(AdminAudit(AdminAuditConfig{
		UserClient: stub,
		Logger:     log,
	}))
	r.Put("/api/v1/admin/carriers/{code}", func(w http.ResponseWriter, req *http.Request) {
		var body map[string]any
		if err := httpx.DecodeJSON(req, &body); err != nil {
			httpx.WriteDecodeError(w, req, err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"carrier": map[string]any{
				"carrier_code":     chi.URLParam(req, "code"),
				"enabled":          true,
				"token_configured": true,
			},
		})
	})
	r.Post("/api/v1/admin/refunds/{id}/settle", func(w http.ResponseWriter, req *http.Request) {
		var body map[string]any
		if err := httpx.DecodeJSON(req, &body); err != nil {
			httpx.WriteDecodeError(w, req, err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"refund": map[string]any{
				"refund_id":           chi.URLParam(req, "id"),
				"bank_code":           "VCB",
				"bank_account_masked": "****6789",
				"bank_account_number": "10293847566789",
				"bank_account_name":   "NGUYEN VAN A",
			},
			"already_settled": false,
		})
	})

	const secretCarrierToken = "ghn-live-super-secret-token-998877"
	req1 := httptest.NewRequest(http.MethodPut, "/api/v1/admin/carriers/ghn",
		strings.NewReader(`{"enabled":true,"api_token":"`+secretCarrierToken+`","fallback_rates":[]}`))
	req1.Header.Set("Authorization", "Bearer "+adminTok)
	rec1 := httptest.NewRecorder()
	r.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("PUT carrier want 200, got %d (%s)", rec1.Code, rec1.Body.String())
	}

	const rawBankAcc = "10293847566789"
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/admin/refunds/ref-42/settle",
		strings.NewReader(`{"provider_refund_id":"TX-99","note":"manual transfer"}`))
	req2.Header.Set("Authorization", "Bearer "+adminTok)
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("POST settle want 200, got %d (%s)", rec2.Code, rec2.Body.String())
	}

	stub.mu.Lock()
	reqs := append([]*userv1.WriteAuditLogRequest(nil), stub.reqs...)
	stub.mu.Unlock()
	if len(reqs) != 2 {
		t.Fatalf("expected 2 audit writes, got %d", len(reqs))
	}

	carrierAudit := reqs[0]
	if carrierAudit.GetTargetType() != "carrier" || carrierAudit.GetTargetId() != "ghn" {
		t.Fatalf("unexpected carrier audit target: type=%q id=%q", carrierAudit.GetTargetType(), carrierAudit.GetTargetId())
	}
	if strings.Contains(carrierAudit.GetBeforeJson(), secretCarrierToken) || strings.Contains(carrierAudit.GetAfterJson(), secretCarrierToken) {
		t.Fatalf("carrier api_token leaked into audit json: before=%s after=%s", carrierAudit.GetBeforeJson(), carrierAudit.GetAfterJson())
	}
	if !strings.Contains(carrierAudit.GetAfterJson(), `"[REDACTED]"`) || !strings.Contains(carrierAudit.GetAfterJson(), `"token_configured":true`) {
		t.Fatalf("expected redacted api_token and preserved token_configured in after_json, got %s", carrierAudit.GetAfterJson())
	}

	settleAudit := reqs[1]
	if settleAudit.GetTargetType() != "refund" || settleAudit.GetTargetId() != "ref-42" {
		t.Fatalf("unexpected settle audit target: type=%q id=%q", settleAudit.GetTargetType(), settleAudit.GetTargetId())
	}
	if strings.Contains(settleAudit.GetBeforeJson(), rawBankAcc) || strings.Contains(settleAudit.GetAfterJson(), rawBankAcc) {
		t.Fatalf("bank_account_number leaked into audit json: before=%s after=%s", settleAudit.GetBeforeJson(), settleAudit.GetAfterJson())
	}
	if !strings.Contains(settleAudit.GetAfterJson(), `"[REDACTED]"`) || !strings.Contains(settleAudit.GetAfterJson(), `"bank_account_masked":"****6789"`) {
		t.Fatalf("expected redacted bank_account_number and preserved bank_account_masked in after_json, got %s", settleAudit.GetAfterJson())
	}

	if strings.Contains(logBuf.String(), secretCarrierToken) || strings.Contains(logBuf.String(), rawBankAcc) {
		t.Fatalf("sensitive field leaked into logs: %s", logBuf.String())
	}
}

func TestWebhookAndAuthRateLimitBuckets(t *testing.T) {
	log := logger.New("gw-rl-buckets", logger.WithWriter(io.Discard))
	lim := newMemRateLimiter()
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wh := RateLimitWebhook(lim, 2, log, nil)(ok)
	authRL := RateLimitAuth(lim, 1, log, nil)(ok)

	// Webhook bucket allows 2, blocks 3rd.
	for i := 1; i <= 3; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/webhooks/payment", bytes.NewReader([]byte(`{}`)))
		req.RemoteAddr = "198.51.100.55:1000"
		wh.ServeHTTP(rec, req)
		want := http.StatusOK
		if i == 3 {
			want = http.StatusTooManyRequests
		}
		if rec.Code != want {
			t.Fatalf("webhook req %d: got %d, want %d", i, rec.Code, want)
		}
	}

	// Auth bucket for the SAME IP is independent: allows 1, blocks 2nd.
	for i := 1; i <= 2; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/forgot-password", bytes.NewReader([]byte(`{}`)))
		req.RemoteAddr = "198.51.100.55:1001"
		authRL.ServeHTTP(rec, req)
		want := http.StatusOK
		if i == 2 {
			want = http.StatusTooManyRequests
		}
		if rec.Code != want {
			t.Fatalf("auth req %d: got %d, want %d", i, rec.Code, want)
		}
	}
}
