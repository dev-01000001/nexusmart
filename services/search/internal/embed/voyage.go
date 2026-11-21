package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/nexusmart/core/services/search/internal/resilience"
)

const (
	DefaultVoyageAPIURL = "https://api.voyageai.com/v1/embeddings"
	DefaultVoyageModel  = "voyage-3-lite"
	voyageMaxBatchSize  = 64
)

// VoyageConfig configures VoyageEmbedder.
type VoyageConfig struct {
	APIURL     string
	APIKey     string
	Model      string
	OutputDim  int
	HTTPClient *http.Client
	Resilience *resilience.Executor
	Logger     *slog.Logger
}

// VoyageEmbedder calls the Voyage AI embeddings HTTP endpoint using stdlib net/http
// wrapped with timeout, retry with exponential backoff, and circuit breaker protection.
type VoyageEmbedder struct {
	apiURL     string
	apiKey     string
	model      string
	outputDim  int
	httpClient *http.Client
	exec       *resilience.Executor
}

// NewVoyageEmbedder creates a VoyageEmbedder with safe defaults.
func NewVoyageEmbedder(cfg VoyageConfig) *VoyageEmbedder {
	apiURL := strings.TrimSpace(cfg.APIURL)
	if apiURL == "" {
		apiURL = DefaultVoyageAPIURL
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		model = DefaultVoyageModel
	}
	dim := cfg.OutputDim
	if dim <= 0 {
		dim = Dimension
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: 15 * time.Second,
		}
	}
	exec := cfg.Resilience
	if exec == nil {
		rcfg := resilience.DefaultConfig("voyage-embed", cfg.Logger)
		rcfg.AttemptTimeout = 5 * time.Second
		rcfg.MaxAttempts = 3
		exec = resilience.NewExecutor(rcfg)
	}
	return &VoyageEmbedder{
		apiURL:     apiURL,
		apiKey:     cfg.APIKey,
		model:      model,
		outputDim:  dim,
		httpClient: httpClient,
		exec:       exec,
	}
}

// Dim returns the configured output embedding dimensionality.
func (v *VoyageEmbedder) Dim() int {
	return v.outputDim
}

// Embed computes an embedding for a single text via Voyage AI.
func (v *VoyageEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	vecs, err := v.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) != 1 {
		return nil, fmt.Errorf("voyage returned %d embeddings, want 1", len(vecs))
	}
	return vecs[0], nil
}

// EmbedBatch computes embeddings for texts, automatically splitting into batches of at most 64.
func (v *VoyageEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}
	out := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += voyageMaxBatchSize {
		end := start + voyageMaxBatchSize
		if end > len(texts) {
			end = len(texts)
		}
		chunk, err := v.embedChunk(ctx, texts[start:end])
		if err != nil {
			return nil, err
		}
		out = append(out, chunk...)
	}
	return out, nil
}

type voyageRequest struct {
	Input           []string `json:"input"`
	Model           string   `json:"model"`
	OutputDimension int      `json:"output_dimension"`
}

type voyageResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float64 `json:"embedding"`
	} `json:"data"`
}

func (v *VoyageEmbedder) embedChunk(ctx context.Context, texts []string) ([][]float32, error) {
	payload, err := json.Marshal(voyageRequest{
		Input:           texts,
		Model:           v.model,
		OutputDimension: v.outputDim,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal voyage request: %w", err)
	}

	return resilience.Do(ctx, v.exec, "embeddings", func(attemptCtx context.Context) ([][]float32, error) {
		req, err := http.NewRequestWithContext(attemptCtx, http.MethodPost, v.apiURL, bytes.NewReader(payload))
		if err != nil {
			return nil, resilience.NonRetryable(fmt.Errorf("build voyage request: %w", err))
		}
		req.Header.Set("Content-Type", "application/json")
		if v.apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+v.apiKey)
		}

		resp, err := v.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("voyage http call: %w", err)
		}
		defer func() {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}()

		body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		if err != nil {
			return nil, fmt.Errorf("read voyage response: %w", err)
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			apiErr := fmt.Errorf("voyage http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
			if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
				return nil, resilience.NonRetryable(apiErr)
			}
			return nil, apiErr
		}

		var decoded voyageResponse
		if err := json.Unmarshal(body, &decoded); err != nil {
			return nil, fmt.Errorf("decode voyage response: %w", err)
		}
		if len(decoded.Data) != len(texts) {
			return nil, fmt.Errorf("voyage returned %d vectors for %d inputs", len(decoded.Data), len(texts))
		}

		result := make([][]float32, len(texts))
		seen := make([]bool, len(texts))
		for _, item := range decoded.Data {
			if item.Index < 0 || item.Index >= len(texts) {
				return nil, fmt.Errorf("voyage returned out-of-range index %d", item.Index)
			}
			if len(item.Embedding) != v.outputDim {
				return nil, fmt.Errorf("voyage returned embedding dim %d, want %d", len(item.Embedding), v.outputDim)
			}
			result[item.Index] = normalizeFloat64To32(item.Embedding)
			seen[item.Index] = true
		}
		for i, ok := range seen {
			if !ok {
				return nil, fmt.Errorf("voyage response missing embedding at index %d", i)
			}
		}
		return result, nil
	})
}

func normalizeFloat64To32(vals []float64) []float32 {
	var sumSq float64
	for _, x := range vals {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			continue
		}
		sumSq += x * x
	}
	out := make([]float32, len(vals))
	if sumSq == 0 {
		out[0] = 1.0
		return out
	}
	inv := 1.0 / math.Sqrt(sumSq)
	for i, x := range vals {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			out[i] = 0
			continue
		}
		out[i] = float32(x * inv)
	}
	return out
}
