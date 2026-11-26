package e2e

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

type respSimpleString string

// ResetRateLimitKeys deletes gateway rate-limit keys (rl:*) from Redis using minimal raw RESP2 over TCP
// so back-to-back test runs stay deterministic without adding runtime dependencies or relaxing stack limits.
func ResetRateLimitKeys(ctx context.Context, redisAddr, redisPassword string) (int, error) {
	return DeleteRedisKeysByPattern(ctx, redisAddr, redisPassword, "rl:*")
}

// RedisDo executes a single Redis command over raw RESP2, authenticating with AUTH when redisPassword is non-empty.
func RedisDo(ctx context.Context, redisAddr, redisPassword string, args ...string) (any, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("empty redis command")
	}
	conn, r, err := dialRedis(ctx, redisAddr, redisPassword)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()

	val, err := respDo(conn, r, args...)
	if err != nil {
		return nil, fmt.Errorf("redis %s: %w", args[0], err)
	}
	return val, nil
}

// DeleteRedisKeysByPattern scans and deletes Redis keys matching pattern via raw RESP2.
func DeleteRedisKeysByPattern(ctx context.Context, redisAddr, redisPassword, pattern string) (int, error) {
	if strings.TrimSpace(redisAddr) == "" {
		return 0, nil
	}

	conn, r, err := dialRedis(ctx, redisAddr, redisPassword)
	if err != nil {
		return 0, err
	}
	defer func() { _ = conn.Close() }()

	cursor := "0"
	deleted := 0

	for {
		val, err := respDo(conn, r, "SCAN", cursor, "MATCH", pattern, "COUNT", "200")
		if err != nil {
			return deleted, fmt.Errorf("redis SCAN %s: %w", pattern, err)
		}
		arr, ok := val.([]any)
		if !ok || len(arr) != 2 {
			return deleted, fmt.Errorf("unexpected SCAN response: %#v", val)
		}
		nextCursor, ok := arr[0].(string)
		if !ok {
			return deleted, fmt.Errorf("unexpected SCAN cursor: %#v", arr[0])
		}
		rawKeys, ok := arr[1].([]any)
		if !ok {
			return deleted, fmt.Errorf("unexpected SCAN keys: %#v", arr[1])
		}

		if len(rawKeys) > 0 {
			delArgs := make([]string, 0, len(rawKeys)+1)
			delArgs = append(delArgs, "DEL")
			for _, rk := range rawKeys {
				if ks, ok := rk.(string); ok && ks != "" {
					delArgs = append(delArgs, ks)
				}
			}
			if len(delArgs) > 1 {
				res, delErr := respDo(conn, r, delArgs...)
				if delErr != nil {
					return deleted, fmt.Errorf("redis DEL: %w", delErr)
				}
				if n, ok := res.(int64); ok {
					deleted += int(n)
				}
			}
		}

		cursor = nextCursor
		if cursor == "0" {
			break
		}
	}

	return deleted, nil
}

func dialRedis(ctx context.Context, redisAddr, redisPassword string) (net.Conn, *bufio.Reader, error) {
	addr := strings.TrimSpace(redisAddr)
	if addr == "" {
		return nil, nil, fmt.Errorf("empty redis address")
	}

	dialer := &net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, nil, fmt.Errorf("dial redis %s: %w", addr, err)
	}

	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(10 * time.Second)
	}
	if err := conn.SetDeadline(deadline); err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("set redis deadline: %w", err)
	}

	r := bufio.NewReader(conn)
	if redisPassword != "" {
		val, err := respDo(conn, r, "AUTH", redisPassword)
		if err != nil {
			_ = conn.Close()
			if strings.Contains(err.Error(), redisPassword) {
				return nil, nil, fmt.Errorf("redis AUTH failed")
			}
			return nil, nil, fmt.Errorf("redis AUTH: %w", err)
		}
		if s, ok := val.(respSimpleString); !ok || s != "OK" {
			_ = conn.Close()
			return nil, nil, fmt.Errorf("redis AUTH: unexpected reply (want +OK)")
		}
	}
	return conn, r, nil
}

func respDo(w io.Writer, r *bufio.Reader, args ...string) (any, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("empty redis command")
	}
	var b strings.Builder
	b.WriteString("*")
	b.WriteString(strconv.Itoa(len(args)))
	b.WriteString("\r\n")
	for _, arg := range args {
		b.WriteString("$")
		b.WriteString(strconv.Itoa(len(arg)))
		b.WriteString("\r\n")
		b.WriteString(arg)
		b.WriteString("\r\n")
	}
	if _, err := io.WriteString(w, b.String()); err != nil {
		return nil, err
	}
	return respReadValue(r)
}

func respReadValue(r *bufio.Reader) (any, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	if len(line) == 0 {
		return nil, fmt.Errorf("empty RESP line")
	}
	prefix := line[0]
	payload := line[1:]

	switch prefix {
	case '+':
		return respSimpleString(payload), nil
	case '-':
		return nil, fmt.Errorf("redis error: %s", payload)
	case ':':
		n, err := strconv.ParseInt(payload, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse RESP int %q: %w", payload, err)
		}
		return n, nil
	case '$':
		n, err := strconv.Atoi(payload)
		if err != nil {
			return nil, fmt.Errorf("parse RESP bulk length %q: %w", payload, err)
		}
		if n < 0 {
			return nil, nil
		}
		buf := make([]byte, n+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, fmt.Errorf("read RESP bulk payload: %w", err)
		}
		return string(buf[:n]), nil
	case '*':
		n, err := strconv.Atoi(payload)
		if err != nil {
			return nil, fmt.Errorf("parse RESP array length %q: %w", payload, err)
		}
		if n < 0 {
			return nil, nil
		}
		items := make([]any, n)
		for i := 0; i < n; i++ {
			v, err := respReadValue(r)
			if err != nil {
				return nil, err
			}
			items[i] = v
		}
		return items, nil
	default:
		return nil, fmt.Errorf("unsupported RESP prefix %q in %q", prefix, line)
	}
}
