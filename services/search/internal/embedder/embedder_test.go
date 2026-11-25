package embedder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/sony/gobreaker/v2"
)

func TestMockEmbedder_DeterministicL2AndVietnameseSynonyms(t *testing.T) {
	emb, err := New(Config{Mode: ModeMock, Dim: DefaultDim})
	if err != nil {
		t.Fatalf("New(mock): %v", err)
	}
	if emb.Mode() != ModeMock || emb.Dim() != DefaultDim {
		t.Fatalf("got mode=%q dim=%d, want mock/1536", emb.Mode(), emb.Dim())
	}

	ctx := context.Background()
	v1, err := emb.Embed(ctx, "Áo phông nam cotton thoáng mát mùa hè")
	if err != nil {
		t.Fatalf("Embed v1: %v", err)
	}
	v1Repeat, err := emb.Embed(ctx, "ao phong nam cotton thoang mat mua he")
	if err != nil {
		t.Fatalf("Embed v1Repeat: %v", err)
	}
	if len(v1) != DefaultDim {
		t.Fatalf("len(v1) = %d, want %d", len(v1), DefaultDim)
	}

	var normSq float64
	for i := range v1 {
		normSq += float64(v1[i]) * float64(v1[i])
		if v1[i] != v1Repeat[i] {
			t.Fatalf("mock embedder not deterministic/accent-folded at idx %d: %f != %f", i, v1[i], v1Repeat[i])
		}
	}
	if math.Abs(math.Sqrt(normSq)-1.0) > 1e-4 {
		t.Fatalf("L2 norm = %f, want 1.0", math.Sqrt(normSq))
	}

	vSynonym, err := emb.Embed(ctx, "Áo thun nam cotton mát mẻ")
	if err != nil {
		t.Fatalf("Embed vSynonym: %v", err)
	}
	vUnrelated, err := emb.Embed(ctx, "Giày chạy bộ thể thao đế cao su")
	if err != nil {
		t.Fatalf("Embed vUnrelated: %v", err)
	}

	simRelated := CosineSimilarity(v1, vSynonym)
	simUnrelated := CosineSimilarity(v1, vUnrelated)
	if simRelated <= simUnrelated+0.15 {
		t.Fatalf("expected synonym cosine (%f) to be significantly higher than unrelated (%f)", simRelated, simUnrelated)
	}

	// Verify đ/Đ -> d folding in mock embeddings ("đồ lót" vs "do lot" vs "quần sịp").
	vDoLotAccented, _ := emb.Embed(ctx, "Đồ lót boxer nam gỗ sồi")
	vDoLotPlain, _ := emb.Embed(ctx, "do lot boxer nam go soi")
	if sim := CosineSimilarity(vDoLotAccented, vDoLotPlain); sim < 0.9999 {
		t.Fatalf("expected đ/Đ folding exact match, got cosine=%f", sim)
	}
}

func TestOpenAIEmbedder_RefusesEmptyKey_AndWorksWithHTTPTestServerAndBreaker(t *testing.T) {
	if _, err := New(Config{Mode: ModeOpenAI, Dim: DefaultDim, APIKey: ""}); err == nil {
		t.Fatalf("expected error when EMBEDDING_MODE=openai and APIKey is empty")
	}

	var (
		statusCode atomic.Int32
		delayMS    atomic.Int64
		calls      atomic.Int32
	)
	statusCode.Store(http.StatusOK)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/embeddings" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer sk-local-test-secret-999" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if d := delayMS.Load(); d > 0 {
			time.Sleep(time.Duration(d) * time.Millisecond)
		}
		code := int(statusCode.Load())
		if code != http.StatusOK {
			http.Error(w, "upstream failure", code)
			return
		}
		var req openAIEmbedRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		data := make([]map[string]any, len(req.Input))
		for i := range req.Input {
			vec := make([]float32, req.Dimensions)
			vec[i%req.Dimensions] = 3.0
			vec[(i+1)%req.Dimensions] = 4.0
			data[len(req.Input)-1-i] = map[string]any{
				"index":     i,
				"embedding": vec,
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer srv.Close()

	var logBuf bytes.Buffer
	testLog := logger.New("search", logger.WithWriter(&logBuf), logger.WithLevel(slog.LevelDebug))

	emb, err := NewOpenAIEmbedder(Config{
		Mode:      ModeOpenAI,
		Model:     "text-embedding-3-small",
		Dim:       DefaultDim,
		APIKey:    "sk-local-test-secret-999",
		BaseURL:   srv.URL,
		Timeout:   100 * time.Millisecond,
		CBMaxFail: 2,
		CBTimeout: 120 * time.Millisecond,
		Logger:    testLog,
	})
	if err != nil {
		t.Fatalf("NewOpenAIEmbedder: %v", err)
	}

	// 1. Happy path: preserves index order and L2-normalizes.
	vecs, err := emb.EmbedBatch(context.Background(), []string{"sensitive_query_never_log_1", "sensitive_query_never_log_2"})
	if err != nil {
		t.Fatalf("EmbedBatch happy path: %v", err)
	}
	if len(vecs) != 2 || len(vecs[0]) != DefaultDim {
		t.Fatalf("unexpected embedding batch shape: %d", len(vecs))
	}
	if math.Abs(float64(vecs[0][0])-0.6) > 1e-4 || math.Abs(float64(vecs[0][1])-0.8) > 1e-4 {
		t.Fatalf("expected L2-normalized [0.6, 0.8, ...], got [%f, %f]", vecs[0][0], vecs[0][1])
	}

	// 2. Timeout enforcement.
	delayMS.Store(250)
	if _, err := emb.Embed(context.Background(), "sensitive_timeout_query"); err == nil {
		t.Fatalf("expected timeout error")
	}
	delayMS.Store(0)

	// 3. Consecutive 500s trip gobreaker/v2 to Open and short-circuit subsequent requests.
	statusCode.Store(http.StatusInternalServerError)
	_, _ = emb.Embed(context.Background(), "sensitive_err_query_1")
	if emb.CircuitState() != gobreaker.StateOpen {
		t.Fatalf("expected circuit breaker Open after 2 consecutive failures, got %s", emb.CircuitState())
	}
	callsAtOpen := calls.Load()
	_, err = emb.Embed(context.Background(), "sensitive_err_query_open")
	if !errors.Is(err, gobreaker.ErrOpenState) {
		t.Fatalf("expected gobreaker.ErrOpenState while open, got %v", err)
	}
	if calls.Load() != callsAtOpen {
		t.Fatalf("expected 0 HTTP calls while breaker is Open, got delta %d", calls.Load()-callsAtOpen)
	}

	// 4. Recovery after CBTimeout in half-open state.
	statusCode.Store(http.StatusOK)
	time.Sleep(150 * time.Millisecond)
	if _, err := emb.Embed(context.Background(), "sensitive_recovery_query"); err != nil {
		t.Fatalf("expected recovery after half-open, got %v", err)
	}
	if emb.CircuitState() != gobreaker.StateClosed {
		t.Fatalf("expected circuit breaker Closed after recovery, got %s", emb.CircuitState())
	}

	// 5. Security: neither API key nor raw queries appear in logs (G12/G14).
	logs := logBuf.String()
	for _, forbidden := range []string{"sk-local-test-secret-999", "sensitive_query_never_log", "sensitive_timeout_query", "sensitive_err_query"} {
		if strings.Contains(logs, forbidden) {
			t.Fatalf("log leaked sensitive string %q: %s", forbidden, logs)
		}
	}
}
