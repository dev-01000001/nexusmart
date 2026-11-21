package embed

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nexusmart/core/services/search/internal/resilience"
)

func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func TestMockEmbedderDeterministicAndVietnameseInvariant(t *testing.T) {
	emb := NewMockEmbedder()
	if emb.Dim() != 256 {
		t.Fatalf("Dim() = %d, want 256", emb.Dim())
	}

	v1, err := emb.Embed(context.Background(), "Áo thun thể thao đẹp")
	if err != nil {
		t.Fatalf("Embed accented: %v", err)
	}
	v2, err := emb.Embed(context.Background(), "ao thun the thao dep")
	if err != nil {
		t.Fatalf("Embed unaccented: %v", err)
	}
	if len(v1) != 256 || len(v2) != 256 {
		t.Fatalf("unexpected dims: %d, %d", len(v1), len(v2))
	}

	var normSq float64
	for i := range v1 {
		if v1[i] != v2[i] {
			t.Fatalf("vector mismatch at dim %d: %v vs %v", i, v1[i], v2[i])
		}
		normSq += float64(v1[i]) * float64(v1[i])
	}
	if math.Abs(math.Sqrt(normSq)-1.0) > 1e-5 {
		t.Fatalf("L2 norm = %v, want 1.0", math.Sqrt(normSq))
	}

	// Related Vietnamese products must have substantially higher cosine similarity than unrelated ones.
	related, _ := emb.Embed(context.Background(), "Áo thun chạy bộ thể thao nam thoáng khí")
	unrelated, _ := emb.Embed(context.Background(), "Tai nghe bluetooth chống ồn không dây")

	simRelated := cosine(v1, related)
	simUnrelated := cosine(v1, unrelated)
	if simRelated <= 0.3 {
		t.Fatalf("expected high similarity for related Vietnamese products, got %.4f", simRelated)
	}
	if simRelated <= simUnrelated+0.2 {
		t.Fatalf("expected related (%.4f) >> unrelated (%.4f)", simRelated, simUnrelated)
	}

	// Empty string must produce a valid unit vector (never NaN).
	emptyVec, err := emb.Embed(context.Background(), "   ")
	if err != nil {
		t.Fatalf("Embed empty: %v", err)
	}
	if math.Abs(cosine(emptyVec, emptyVec)-1.0) > 1e-5 {
		t.Fatalf("empty vector not unit normalized")
	}
}

func TestVoyageEmbedderRetryAndAuth(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-secret-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var req voyageRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Model != "voyage-3-lite" || req.OutputDimension != 256 {
			http.Error(w, "invalid model/dim", http.StatusBadRequest)
			return
		}

		// Fail first attempt with 503 to verify retry with backoff.
		if attempts.Add(1) == 1 {
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		}

		type item struct {
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		}
		data := make([]item, len(req.Input))
		for i := range req.Input {
			vec := make([]float64, 256)
			vec[i%256] = 2.0
			data[i] = item{Index: i, Embedding: vec}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer srv.Close()

	exec := resilience.NewExecutor(resilience.Config{
		Name:           "voyage-test",
		AttemptTimeout: time.Second,
		MaxAttempts:    3,
		BaseBackoff:    5 * time.Millisecond,
		MaxBackoff:     20 * time.Millisecond,
		CBMaxFailures:  5,
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	emb := NewVoyageEmbedder(VoyageConfig{
		APIURL:     srv.URL,
		APIKey:     "test-secret-key",
		Model:      "voyage-3-lite",
		OutputDim:  256,
		Resilience: exec,
	})

	vecs, err := emb.EmbedBatch(context.Background(), []string{"áo thun", "quần đùi"})
	if err != nil {
		t.Fatalf("EmbedBatch failed: %v", err)
	}
	if len(vecs) != 2 {
		t.Fatalf("len(vecs) = %d, want 2", len(vecs))
	}
	if attempts.Load() != 2 {
		t.Fatalf("attempts = %d, want 2", attempts.Load())
	}
	// Verify L2 normalization of [2.0, 0, ...] -> [1.0, 0, ...]
	if math.Abs(float64(vecs[0][0])-1.0) > 1e-5 || math.Abs(float64(vecs[1][1])-1.0) > 1e-5 {
		t.Fatalf("unexpected normalized vectors: v0[0]=%v v1[1]=%v", vecs[0][0], vecs[1][1])
	}
}

func TestVoyageEmbedder4xxFailsFast(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		http.Error(w, "bad key", http.StatusUnauthorized)
	}))
	defer srv.Close()

	exec := resilience.NewExecutor(resilience.Config{
		Name:           "voyage-4xx",
		AttemptTimeout: time.Second,
		MaxAttempts:    3,
		BaseBackoff:    5 * time.Millisecond,
		CBMaxFailures:  2,
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	emb := NewVoyageEmbedder(VoyageConfig{
		APIURL:     srv.URL,
		APIKey:     "bad",
		Resilience: exec,
	})
	if _, err := emb.Embed(context.Background(), "test"); err == nil {
		t.Fatal("expected error on 401")
	}
	if attempts.Load() != 1 {
		t.Fatalf("expected 1 attempt on 401 (no retry), got %d", attempts.Load())
	}
}

func TestNewFromEnv(t *testing.T) {
	t.Setenv("EMBED_MODE", "mock")
	emb, err := NewFromEnv(nil)
	if err != nil {
		t.Fatalf("NewFromEnv(mock): %v", err)
	}
	if emb.Dim() != 256 {
		t.Fatalf("dim = %d, want 256", emb.Dim())
	}

	t.Setenv("EMBED_MODE", "voyage")
	t.Setenv("VOYAGE_API_KEY", "")
	if _, err := NewFromEnv(nil); err == nil {
		t.Fatal("expected error when EMBED_MODE=voyage without VOYAGE_API_KEY")
	}

	t.Setenv("EMBED_MODE", "invalid")
	if _, err := NewFromEnv(nil); err == nil {
		t.Fatal("expected error on unsupported EMBED_MODE")
	}
}
