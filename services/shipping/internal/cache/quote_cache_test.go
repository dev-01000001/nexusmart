package cache

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nexusmart/core/services/shipping/internal/carrier"
	"github.com/redis/go-redis/v9"
)

// miniRESPServer is a tiny in-process RESP2 server supporting GET, SET, PING, HELLO, CLIENT for unit testing RedisQuoteCache.
type miniRESPServer struct {
	ln   net.Listener
	mu   sync.Mutex
	data map[string]string
}

func startMiniRESPServer(t *testing.T) *miniRESPServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen mini RESP: %v", err)
	}
	s := &miniRESPServer{
		ln:   ln,
		data: make(map[string]string),
	}
	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			go s.handleConn(conn)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

func (s *miniRESPServer) handleConn(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	r := bufio.NewReader(conn)
	for {
		args, err := readRESPArray(r)
		if err != nil {
			return
		}
		if len(args) == 0 {
			continue
		}
		cmd := strings.ToUpper(args[0])
		switch cmd {
		case "PING":
			_, _ = conn.Write([]byte("+PONG\r\n"))
		case "HELLO":
			_, _ = conn.Write([]byte("-ERR unknown command 'HELLO'\r\n"))
		case "CLIENT":
			_, _ = conn.Write([]byte("+OK\r\n"))
		case "GET":
			s.mu.Lock()
			val, ok := s.data[args[1]]
			s.mu.Unlock()
			if !ok {
				_, _ = conn.Write([]byte("$-1\r\n"))
			} else {
				_, _ = fmt.Fprintf(conn, "$%d\r\n%s\r\n", len(val), val)
			}
		case "SET":
			if len(args) >= 3 {
				s.mu.Lock()
				s.data[args[1]] = args[2]
				s.mu.Unlock()
			}
			_, _ = conn.Write([]byte("+OK\r\n"))
		default:
			_, _ = conn.Write([]byte("+OK\r\n"))
		}
	}
}

func readRESPArray(r *bufio.Reader) ([]string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimRight(line, "\r\n")
	if !strings.HasPrefix(line, "*") {
		return nil, io.EOF
	}
	n, err := strconv.Atoi(line[1:])
	if err != nil {
		return nil, err
	}
	args := make([]string, 0, n)
	for i := 0; i < n; i++ {
		lenLine, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		lenLine = strings.TrimRight(lenLine, "\r\n")
		if !strings.HasPrefix(lenLine, "$") {
			return nil, io.EOF
		}
		blen, err := strconv.Atoi(lenLine[1:])
		if err != nil {
			return nil, err
		}
		buf := make([]byte, blen+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		args = append(args, string(buf[:blen]))
	}
	return args, nil
}

func TestQuoteCacheKeyBucketing(t *testing.T) {
	k1 := quoteCacheKey(carrier.FeeRequest{FromProvinceCode: "79", FromWardCode: "79001", ToProvinceCode: "01", ToWardCode: "01001", WeightGrams: 0, CODAmountVND: 0})
	k2 := quoteCacheKey(carrier.FeeRequest{FromProvinceCode: "79", FromWardCode: "79001", ToProvinceCode: "01", ToWardCode: "01001", WeightGrams: 450, CODAmountVND: 0})
	if k1 != k2 {
		t.Fatalf("expected 0g (default 500g) and 450g (bucket 500g) keys to match: %s vs %s", k1, k2)
	}
	kCOD := quoteCacheKey(carrier.FeeRequest{FromProvinceCode: "79", FromWardCode: "79001", ToProvinceCode: "01", ToWardCode: "01001", WeightGrams: 450, CODAmountVND: 100000})
	if kCOD == k1 {
		t.Fatalf("expected COD flag to differentiate cache key")
	}
}

func TestRedisQuoteCacheGetSetAndFallbacks(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	req := carrier.FeeRequest{
		FromProvinceCode: "79",
		FromWardCode:     "79001",
		ToProvinceCode:   "01",
		ToWardCode:       "01001",
		WeightGrams:      750,
	}

	// Nil safety
	nilCache := NewRedisQuoteCache(nil, 0, nil)
	if nilCache.ttl != 5*time.Minute {
		t.Fatalf("expected default 5m ttl, got %v", nilCache.ttl)
	}
	if _, ok := nilCache.Get(context.Background(), req); ok {
		t.Fatalf("expected miss on nil client")
	}
	nilCache.Set(context.Background(), req, []carrier.Quote{{CarrierCode: "ghtk", FeeVND: 20000}})

	// Real go-redis client talking to local in-process RESP server.
	srv := startMiniRESPServer(t)
	rdb := redis.NewClient(&redis.Options{
		Addr:     srv.ln.Addr().String(),
		Protocol: 2,
	})
	t.Cleanup(func() { _ = rdb.Close() })

	qc := NewRedisQuoteCache(rdb, time.Minute, logger)

	// Cache miss
	if _, ok := qc.Get(context.Background(), req); ok {
		t.Fatalf("expected initial cache miss")
	}

	// Set and cache hit
	want := []carrier.Quote{
		{CarrierCode: carrier.CodeGHTK, ServiceCode: "ROAD", FeeVND: 35000, EstimatedDays: 4},
		{CarrierCode: carrier.CodeGHN, ServiceCode: "STANDARD", FeeVND: 38000, EstimatedDays: 4},
	}
	qc.Set(context.Background(), req, want)

	got, ok := qc.Get(context.Background(), req)
	if !ok || len(got) != 2 || got[0].CarrierCode != carrier.CodeGHTK || got[0].FeeVND != 35000 {
		t.Fatalf("expected cache hit with 2 quotes, got ok=%v quotes=%+v", ok, got)
	}

	// Corrupt JSON entry treated as miss
	srv.mu.Lock()
	srv.data[quoteCacheKey(req)] = "{not-valid-json"
	srv.mu.Unlock()
	if _, ok := qc.Get(context.Background(), req); ok {
		t.Fatalf("expected corrupt cache entry to return miss")
	}

	// Empty array treated as miss
	srv.mu.Lock()
	srv.data[quoteCacheKey(req)] = "[]"
	srv.mu.Unlock()
	if _, ok := qc.Get(context.Background(), req); ok {
		t.Fatalf("expected empty cached quote slice to return miss")
	}

	// Empty Set ignored
	qc.Set(context.Background(), req, nil)
}
