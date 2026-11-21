package embed

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/nexusmart/core/pkg/common/env"
)

// Dimension is the fixed embedding vector dimensionality shared with pgvector vector(256).
const Dimension = 256

// Embedder computes dense vector embeddings for product documents and search queries.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
	Dim() int
}

// NewFromEnv constructs an Embedder according to EMBED_MODE (mock|voyage, default mock).
func NewFromEnv(logger *slog.Logger) (Embedder, error) {
	mode := strings.ToLower(strings.TrimSpace(env.EnvString("EMBED_MODE", "mock")))
	switch mode {
	case "", "mock":
		return NewMockEmbedder(), nil
	case "voyage":
		apiKey := env.EnvString("VOYAGE_API_KEY", "")
		if apiKey == "" {
			return nil, fmt.Errorf("VOYAGE_API_KEY is required when EMBED_MODE=voyage")
		}
		apiURL := env.EnvString("VOYAGE_API_URL", DefaultVoyageAPIURL)
		model := env.EnvString("VOYAGE_MODEL", DefaultVoyageModel)
		return NewVoyageEmbedder(VoyageConfig{
			APIURL:    apiURL,
			APIKey:    apiKey,
			Model:     model,
			OutputDim: Dimension,
			Logger:    logger,
		}), nil
	default:
		return nil, fmt.Errorf("unsupported EMBED_MODE %q (expected mock or voyage)", mode)
	}
}
