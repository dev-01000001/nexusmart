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

func TestQuoteCacheKeyRequiresRealOriginAndDestination(t *testing.T) {
	// Empty origin must NOT produce an empty-origin cache key.
	key := quoteCacheKey(carrier.FeeRequest{
		ShopID:         "shop-1",
		ToProvinceCode: "79",
		ToWardCode:     "26740",
		WeightGrams:    250,
		CODAmountVND:   200000,
	})
	if strings.Contains(key, ":::") || key != "" {
		t.Fatalf("expected empty cache key when origin is missing, got %q", key)
	}

	// Real origin must be included in the cache key.
	validKey := quoteCacheKey(carrier.FeeRequest{
		ShopID:           "shop-1",
		FromProvinceCode: "79",
		FromWardCode:     "26740",
		ToProvinceCode:   "79",
		ToWardCode:       "26740",
		WeightGrams:      250,
		CODAmountVND:     200000,
	})
	want := "shipping:quotes:v2:all:79:26740:79:26740:300:200000"
	if validKey != want {
		t.Fatalf("quoteCacheKey() = %q, want %q", validKey, want)
	}

	// Distinct COD amounts must produce distinct cache keys (GHN insurance fee depends on COD amount).
	kCOD0 := quoteCacheKey(carrier.FeeRequest{
		FromProvinceCode: "79", FromWardCode: "26740",
		ToProvinceCode: "01", ToWardCode: "00070",
		WeightGrams: 500, CODAmountVND: 0,
	})
	kCOD200k := quoteCacheKey(carrier.FeeRequest{
		FromProvinceCode: "79", FromWardCode: "26740",
		ToProvinceCode: "01", ToWardCode: "00070",
		WeightGrams: 500, CODAmountVND: 200000,
	})
	kCOD500k := quoteCacheKey(carrier.FeeRequest{
		FromProvinceCode: "79", FromWardCode: "26740",
		ToProvinceCode: "01", ToWardCode: "00070",
		WeightGrams: 500, CODAmountVND: 500000,
	})
	if kCOD0 == kCOD200k || kCOD200k == kCOD500k || kCOD0 == kCOD500k {
		t.Fatalf("distinct COD amounts must not collide in quote cache: k0=%q k200k=%q k500k=%q", kCOD0, kCOD200k, kCOD500k)
	}

	// AllowedCarriers must be order-independent and isolate distinct carrier subsets.
	k1 := quoteCacheKey(carrier.FeeRequest{
		FromProvinceCode: "79", FromWardCode: "26740",
		ToProvinceCode: "79", ToWardCode: "26740",
		WeightGrams: 500, AllowedCarriers: []string{"mock_express", "ghn"},
	})
	k2 := quoteCacheKey(carrier.FeeRequest{
		FromProvinceCode: "79", FromWardCode: "26740",
		ToProvinceCode: "79", ToWardCode: "26740",
		WeightGrams: 500, AllowedCarriers: []string{"ghn", "mock_express"},
	})
	kOnlyGHN := quoteCacheKey(carrier.FeeRequest{
		FromProvinceCode: "79", FromWardCode: "26740",
		ToProvinceCode: "79", ToWardCode: "26740",
		WeightGrams: 500, AllowedCarriers: []string{"ghn"},
	})
	if k1 != k2 {
		t.Fatalf("carrier order should not affect cache key: %q != %q", k1, k2)
	}
	if k1 == kOnlyGHN {
		t.Fatalf("distinct carrier sets must not collide in cache: %q == %q", k1, kOnlyGHN)
	}
}

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
	// Weight <= 0 must not be cached under a bogus default weight bucket.
	kZero := quoteCacheKey(carrier.FeeRequest{FromProvinceCode: "79", FromWardCode: "26740", ToProvinceCode: "01", ToWardCode: "00070", WeightGrams: 0, CODAmountVND: 0})
	if kZero != "" {
		t.Fatalf("expected empty cache key when weight <= 0, got %q", kZero)
	}
	// 100g buckets: 450g and 500g both bucket to 500g.
	k1 := quoteCacheKey(carrier.FeeRequest{FromProvinceCode: "79", FromWardCode: "26740", ToProvinceCode: "01", ToWardCode: "00070", WeightGrams: 450, CODAmountVND: 0})
	k2 := quoteCacheKey(carrier.FeeRequest{FromProvinceCode: "79", FromWardCode: "26740", ToProvinceCode: "01", ToWardCode: "00070", WeightGrams: 500, CODAmountVND: 0})
	if k1 != k2 {
		t.Fatalf("expected 450g and 500g (both 500g bucket) keys to match: %s vs %s", k1, k2)
	}
	kCOD := quoteCacheKey(carrier.FeeRequest{FromProvinceCode: "79", FromWardCode: "26740", ToProvinceCode: "01", ToWardCode: "00070", WeightGrams: 450, CODAmountVND: 100000})
	if kCOD == k1 {
		t.Fatalf("expected COD amount to differentiate cache key")
	}
}

func TestRedisQuoteCacheGetSetAndFallbacks(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	req := carrier.FeeRequest{
		FromProvinceCode: "79",
		FromWardCode:     "26740",
		ToProvinceCode:   "01",
		ToWardCode:       "00070",
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
