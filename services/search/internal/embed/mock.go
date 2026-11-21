package embed

import (
	"context"
	"hash/fnv"
	"math"

	"github.com/nexusmart/core/services/search/internal/normalize"
)

// MockEmbedder generates deterministic 256-dimensional L2-normalized Vietnamese-aware
// embeddings using signed feature hashing over word unigrams, word bigrams, and char 3-grams.
type MockEmbedder struct {
	dim int
}

// NewMockEmbedder returns a MockEmbedder with the standard 256 dimensions.
func NewMockEmbedder() *MockEmbedder {
	return &MockEmbedder{dim: Dimension}
}

// Dim returns the embedding dimensionality (256).
func (m *MockEmbedder) Dim() int {
	return m.dim
}

// Embed computes a deterministic 256-dim L2-normalized embedding for text.
func (m *MockEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return m.embedOne(text), nil
}

// EmbedBatch computes deterministic 256-dim L2-normalized embeddings for all texts.
func (m *MockEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([][]float32, len(texts))
	for i, text := range texts {
		out[i] = m.embedOne(text)
	}
	return out, nil
}

func (m *MockEmbedder) embedOne(text string) []float32 {
	words := normalize.Tokenize(text)
	acc := make([]float64, m.dim)

	if len(words) == 0 {
		// Return a deterministic unit vector so pgvector cosine distance never produces NaN.
		addHashedFeature(acc, "__empty__", 1.0)
	} else {
		for i, w := range words {
			addHashedFeature(acc, "w:"+w, 2.0)
			if i+1 < len(words) {
				addHashedFeature(acc, "b:"+w+"_"+words[i+1], 1.5)
			}
			padded := []rune("^" + w + "$")
			if len(padded) >= 3 {
				for j := 0; j+3 <= len(padded); j++ {
					addHashedFeature(acc, "c:"+string(padded[j:j+3]), 0.5)
				}
			}
		}
	}

	return l2Normalize(acc)
}

func addHashedFeature(acc []float64, feat string, weight float64) {
	h := fnv.New64a()
	_, _ = h.Write([]byte(feat))
	sum := h.Sum64()

	idx := sum % uint64(len(acc))
	// Independent splitmix64 avalanche for unbiased sign bit.
	mixed := (sum ^ (sum >> 30)) * 0xbf58476d1ce4e5b9
	sign := 1.0
	if (mixed>>63)&1 == 1 {
		sign = -1.0
	}
	acc[idx] += sign * weight
}

func l2Normalize(acc []float64) []float32 {
	var sumSq float64
	for _, v := range acc {
		sumSq += v * v
	}
	out := make([]float32, len(acc))
	if sumSq == 0 || math.IsNaN(sumSq) || math.IsInf(sumSq, 0) {
		out[0] = 1.0
		return out
	}
	invNorm := 1.0 / math.Sqrt(sumSq)
	for i, v := range acc {
		out[i] = float32(v * invNorm)
	}
	return out
}
