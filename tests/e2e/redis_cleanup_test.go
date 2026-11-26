package e2e

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeRedisServer struct {
	addr         string
	wantPassword string
	authReply    string // raw RESP line (including \r\n); defaults to +OK / -WRONGPASS

	mu       sync.Mutex
	commands [][]string
	ln       net.Listener
	wg       sync.WaitGroup
}

func startFakeRedis(t *testing.T, wantPassword, authReply string) *fakeRedisServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen fake redis: %v", err)
	}
	s := &fakeRedisServer{
		addr:         ln.Addr().String(),
		wantPassword: wantPassword,
		authReply:    authReply,
		ln:           ln,
	}
	s.wg.Add(1)
	go s.serve()
	t.Cleanup(func() {
		_ = ln.Close()
		s.wg.Wait()
	})
	return s
}

func (s *fakeRedisServer) serve() {
	defer s.wg.Done()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.wg.Add(1)
		go s.handleConn(conn)
	}
}

func (s *fakeRedisServer) handleConn(conn net.Conn) {
	defer s.wg.Done()
	defer func() { _ = conn.Close() }()

	r := bufio.NewReader(conn)
	authed := s.wantPassword == ""

	for {
		cmd, err := readFakeRESPCommand(r)
		if err != nil {
			return
		}
		if len(cmd) == 0 {
			continue
		}
		s.mu.Lock()
		s.commands = append(s.commands, cmd)
		s.mu.Unlock()

		verb := strings.ToUpper(cmd[0])
		if verb == "AUTH" {
			if s.authReply != "" {
				_, _ = io.WriteString(conn, s.authReply)
				continue
			}
			if len(cmd) == 2 && cmd[1] == s.wantPassword && s.wantPassword != "" {
				authed = true
				_, _ = io.WriteString(conn, "+OK\r\n")
			} else {
				_, _ = io.WriteString(conn, "-WRONGPASS invalid username-password pair\r\n")
			}
			continue
		}
		if !authed {
			_, _ = io.WriteString(conn, "-NOAUTH Authentication required.\r\n")
			continue
		}

		switch verb {
		case "SCAN":
			_, _ = io.WriteString(conn, "*2\r\n$1\r\n0\r\n*2\r\n$5\r\nrl:k1\r\n$5\r\nrl:k2\r\n")
		case "DEL":
			_, _ = fmt.Fprintf(conn, ":%d\r\n", len(cmd)-1)
		case "HSET", "HDEL":
			_, _ = io.WriteString(conn, ":1\r\n")
		default:
			_, _ = io.WriteString(conn, "+OK\r\n")
		}
	}
}

func (s *fakeRedisServer) recordedCommands() [][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]string, len(s.commands))
	copy(out, s.commands)
	return out
}

func readFakeRESPCommand(r *bufio.Reader) ([]string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	if !strings.HasPrefix(line, "*") {
		return nil, fmt.Errorf("expected array header, got %q", line)
	}
	n, err := strconv.Atoi(line[1:])
	if err != nil {
		return nil, err
	}
	args := make([]string, n)
	for i := 0; i < n; i++ {
		hdr, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		hdr = strings.TrimSuffix(strings.TrimSuffix(hdr, "\n"), "\r")
		if !strings.HasPrefix(hdr, "$") {
			return nil, fmt.Errorf("expected bulk header, got %q", hdr)
		}
		sz, err := strconv.Atoi(hdr[1:])
		if err != nil {
			return nil, err
		}
		buf := make([]byte, sz+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		args[i] = string(buf[:sz])
	}
	return args, nil
}

func TestRedisConfigLoadsPassword(t *testing.T) {
	t.Setenv("REDIS_PASSWORD", "")
	if got := LoadConfig().RedisPassword; got != "" {
		t.Fatalf("empty REDIS_PASSWORD: got %q, want empty", got)
	}

	// Trailing slash must be preserved (unlike URL env vars).
	const want = "s3cr3t/pass/"
	t.Setenv("REDIS_PASSWORD", want)
	if got := LoadConfig().RedisPassword; got != want {
		t.Fatalf("REDIS_PASSWORD: got %q, want %q", got, want)
	}
}

func TestRedisHelpers_AuthBehavior(t *testing.T) {
	const pass = "redis-test-secret/42"

	t.Run("ResetRateLimitKeys_AuthenticatesFirst", func(t *testing.T) {
		srv := startFakeRedis(t, pass, "")
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		deleted, err := ResetRateLimitKeys(ctx, srv.addr, pass)
		if err != nil {
			t.Fatalf("ResetRateLimitKeys failed: %v", err)
		}
		if deleted != 2 {
			t.Fatalf("deleted = %d, want 2", deleted)
		}

		cmds := srv.recordedCommands()
		if len(cmds) != 3 {
			t.Fatalf("recorded %d commands, want 3 (AUTH, SCAN, DEL)", len(cmds))
		}
		if cmds[0][0] != "AUTH" || len(cmds[0]) != 2 || cmds[0][1] != pass {
			t.Fatalf("first command must be AUTH with configured password, got verb=%q argc=%d", cmds[0][0], len(cmds[0]))
		}
		if cmds[1][0] != "SCAN" || cmds[2][0] != "DEL" {
			t.Fatalf("expected SCAN then DEL after AUTH, got %q then %q", cmds[1][0], cmds[2][0])
		}
	})

	t.Run("RedisDo_And_w3b41RedisCmd_AuthenticateFirst", func(t *testing.T) {
		srv := startFakeRedis(t, pass, "")
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		val, err := RedisDo(ctx, srv.addr, pass, "HSET", "cart:u1", "sku-1", "{}")
		if err != nil {
			t.Fatalf("RedisDo HSET failed: %v", err)
		}
		if n, ok := val.(int64); !ok || n != 1 {
			t.Fatalf("RedisDo HSET reply = %#v, want int64(1)", val)
		}

		if got := w3b41RedisCmd(t, srv.addr, pass, "HDEL", "cart:u1", "sku-1"); got != "1" {
			t.Fatalf("w3b41RedisCmd HDEL reply = %q, want \"1\"", got)
		}

		cmds := srv.recordedCommands()
		if len(cmds) != 4 {
			t.Fatalf("recorded %d commands, want 4 (AUTH, HSET, AUTH, HDEL)", len(cmds))
		}
		if cmds[0][0] != "AUTH" || cmds[1][0] != "HSET" || cmds[2][0] != "AUTH" || cmds[3][0] != "HDEL" {
			t.Fatalf("unexpected command sequence: %q, %q, %q, %q", cmds[0][0], cmds[1][0], cmds[2][0], cmds[3][0])
		}
	})

	t.Run("NoPassword_SkipsAuth", func(t *testing.T) {
		srv := startFakeRedis(t, "", "")
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		deleted, err := ResetRateLimitKeys(ctx, srv.addr, "")
		if err != nil {
			t.Fatalf("ResetRateLimitKeys without password failed: %v", err)
		}
		if deleted != 2 {
			t.Fatalf("deleted = %d, want 2", deleted)
		}
		cmds := srv.recordedCommands()
		if len(cmds) != 2 || cmds[0][0] != "SCAN" || cmds[1][0] != "DEL" {
			t.Fatalf("expected only SCAN and DEL when password is empty, got %d commands (first=%q)", len(cmds), cmds[0][0])
		}
	})

	t.Run("MissingPassword_FailsWhenServerRequiresAuth", func(t *testing.T) {
		srv := startFakeRedis(t, pass, "")
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		_, err := ResetRateLimitKeys(ctx, srv.addr, "")
		if err == nil || !strings.Contains(err.Error(), "NOAUTH") {
			t.Fatalf("expected NOAUTH error when password omitted, got %v", err)
		}
	})

	t.Run("WrongPassword_FailsWithoutLeakingSecret", func(t *testing.T) {
		const wrongPass = "wrong-secret-must-not-leak"
		srv := startFakeRedis(t, pass, "")
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		_, err := RedisDo(ctx, srv.addr, wrongPass, "HSET", "cart:u1", "sku-1", "{}")
		if err == nil {
			t.Fatal("expected error for wrong password, got nil")
		}
		if strings.Contains(err.Error(), wrongPass) {
			t.Fatalf("error leaked password: %v", err)
		}
		if cmds := srv.recordedCommands(); len(cmds) != 1 || cmds[0][0] != "AUTH" {
			t.Fatalf("must not send command after failed AUTH, got %d commands", len(cmds))
		}
	})

	t.Run("ServerEchoingPasswordInError_IsRedacted", func(t *testing.T) {
		const secret = "echoed-secret-xyz"
		srv := startFakeRedis(t, pass, "-ERR invalid password "+secret+"\r\n")
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		_, err := RedisDo(ctx, srv.addr, secret, "PING")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaked echoed password: %v", err)
		}
	})

	t.Run("AuthReplyMustBeSimpleStringOK", func(t *testing.T) {
		for _, tc := range []struct {
			name  string
			reply string
		}{
			{"bulk_OK", "$2\r\nOK\r\n"},
			{"simple_PONG", "+PONG\r\n"},
			{"integer_1", ":1\r\n"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				srv := startFakeRedis(t, pass, tc.reply)
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()

				_, err := RedisDo(ctx, srv.addr, pass, "PING")
				if err == nil {
					t.Fatalf("expected error for non-+OK AUTH reply %q, got nil", tc.reply)
				}
				if strings.Contains(err.Error(), pass) {
					t.Fatalf("error leaked password: %v", err)
				}
				if cmds := srv.recordedCommands(); len(cmds) != 1 || cmds[0][0] != "AUTH" {
					t.Fatalf("must not send command after non-+OK AUTH reply, got %d commands", len(cmds))
				}
			})
		}
	})
}
