package embedder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/nexusmart/core/pkg/common/textnorm"
	searchcfg "github.com/nexusmart/core/services/search/config"
	"github.com/sony/gobreaker/v2"
)

const (
	ModeMock   = "mock"
	ModeOpenAI = "openai"

	DefaultDim        = 1536
	DefaultTimeoutMS  = 800
	DefaultOpenAIURL  = "https://api.openai.com/v1"
	DefaultOpenAIMod  = "text-embedding-3-small"
	maxRespBodyBytes  = 16 << 20
	cbFailureThresh   = 3
	cbOpenDuration    = 30 * time.Second
	cbCountWindow     = 60 * time.Second
	cbHalfOpenMaxReqs = 1
)

// Embedder computes fixed-dimension L2-normalized dense embeddings for search text.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
	Dim() int
	Mode() string
}

// Config configures the embedder factory.
type Config struct {
	Mode      string
	Model     string
	Dim       int
	APIKey    string
	BaseURL   string
	Timeout   time.Duration
	Synonyms  map[string][]string
	Logger    *slog.Logger
	CBTimeout time.Duration
	CBMaxFail uint32
}

// New constructs an Embedder according to G12 (mock default; openai refuses empty API key).
func New(cfg Config) (Embedder, error) {
	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	if mode == "" {
		mode = ModeMock
	}
	dim := cfg.Dim
	if dim <= 0 {
		dim = DefaultDim
	}

	switch mode {
	case ModeMock:
		syns := cfg.Synonyms
		if syns == nil {
			syns = searchcfg.MustLoadSynonyms()
		}
		return NewMockEmbedder(dim, syns), nil
	case ModeOpenAI:
		if strings.TrimSpace(cfg.APIKey) == "" {
			return nil, errors.New("EMBEDDING_API_KEY is required when EMBEDDING_MODE=openai")
		}
		return NewOpenAIEmbedder(cfg)
	default:
		return nil, fmt.Errorf("unsupported embedding mode %q", cfg.Mode)
	}
}

// MockEmbedder computes deterministic L2-normalized feature-hashed vectors with Vietnamese
// normalization and synonym concept grouping (0 network calls, G12).
type MockEmbedder struct {
	dim       int
	synGroups map[string]string
}

// NewMockEmbedder returns a deterministic offline embedder.
func NewMockEmbedder(dim int, synonyms map[string][]string) *MockEmbedder {
	if dim <= 0 {
		dim = DefaultDim
	}
	return &MockEmbedder{
		dim:       dim,
		synGroups: searchcfg.SynonymGroups(synonyms),
	}
}

func (m *MockEmbedder) Dim() int     { return m.dim }
func (m *MockEmbedder) Mode() string { return ModeMock }

func (m *MockEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return m.embedOne(text), nil
}

func (m *MockEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out[i] = m.embedOne(t)
	}
	return out, nil
}

func (m *MockEmbedder) embedOne(text string) []float32 {
	vec := make([]float32, m.dim)
	norm := textnorm.NormalizeSearchText(text)
	words := splitAlphaNumWords(norm)
	if len(words) == 0 {
		vec[0] = 1.0
		return vec
	}

	addFeature := func(key string, weight float64) {
		hIdx := fnv1a64(key)
		hSign := fnv1a64("s:" + key)
		idx := int(hIdx % uint64(m.dim))
		sign := 1.0
		if hSign&1 == 1 {
			sign = -1.0
		}
		vec[idx] += float32(sign * weight)
	}

	// Emit synonym concept tokens for 1-, 2-, and 3-word phrases.
	for i := 0; i < len(words); i++ {
		if g, ok := m.synGroups[words[i]]; ok {
			addFeature(g, 3.0)
		}
		if i+1 < len(words) {
			p2 := words[i] + " " + words[i+1]
			if g, ok := m.synGroups[p2]; ok {
				addFeature(g, 3.5)
			}
			p2h := words[i] + "-" + words[i+1]
			if g, ok := m.synGroups[p2h]; ok {
				addFeature(g, 3.5)
			}
		}
		if i+2 < len(words) {
			p3 := words[i] + " " + words[i+1] + " " + words[i+2]
			if g, ok := m.synGroups[p3]; ok {
				addFeature(g, 4.0)
			}
		}
	}

	// Unigrams, bigrams, and character 3-grams.
	for i, w := range words {
		addFeature("w:"+w, 1.5)
		if i+1 < len(words) {
			addFeature("bg:"+w+"_"+words[i+1], 1.0)
		}
		if len(w) >= 3 {
			for j := 0; j <= len(w)-3; j++ {
				addFeature("c3:"+w[j:j+3], 0.35)
			}
		}
	}

	normalizeL2(vec)
	return vec
}

func splitAlphaNumWords(s string) []string {
	var words []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			words = append(words, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return words
}

func fnv1a64(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

func normalizeL2(v []float32) {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum <= 0 {
		if len(v) > 0 {
			v[0] = 1.0
		}
		return
	}
	inv := float32(1.0 / math.Sqrt(sum))
	for i := range v {
		v[i] *= inv
	}
}

// CosineSimilarity computes cosine similarity between two equal-length vectors.
func CosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		fa, fb := float64(a[i]), float64(b[i])
		dot += fa * fb
		na += fa * fa
		nb += fb * fb
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// OpenAIEmbedder calls an OpenAI-compatible /embeddings endpoint with bounded timeout and circuit breaker (G12).
type OpenAIEmbedder struct {
	baseURL string
	apiKey  string
	model   string
	dim     int
	timeout time.Duration
	client  *http.Client
	cb      *gobreaker.CircuitBreaker[[][]float32]
	logger  *slog.Logger
}

type nonRetryableClientErr struct {
	err error
}

func (e nonRetryableClientErr) Error() string { return e.err.Error() }
func (e nonRetryableClientErr) Unwrap() error { return e.err }

// NewOpenAIEmbedder creates an OpenAI embeddings client protected by gobreaker/v2 and timeout.
func NewOpenAIEmbedder(cfg Config) (*OpenAIEmbedder, error) {
	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		return nil, errors.New("EMBEDDING_API_KEY is required when EMBEDDING_MODE=openai")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = DefaultOpenAIURL
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		model = DefaultOpenAIMod
	}
	dim := cfg.Dim
	if dim <= 0 {
		dim = DefaultDim
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeoutMS * time.Millisecond
	}
	cbTimeout := cfg.CBTimeout
	if cbTimeout <= 0 {
		cbTimeout = cbOpenDuration
	}
	cbMaxFail := cfg.CBMaxFail
	if cbMaxFail == 0 {
		cbMaxFail = cbFailureThresh
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	settings := gobreaker.Settings{
		Name:        "search-openai-embedder",
		MaxRequests: cbHalfOpenMaxReqs,
		Interval:    cbCountWindow,
		Timeout:     cbTimeout,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= cbMaxFail
		},
		IsSuccessful: func(err error) bool {
			if err == nil {
				return true
			}
			var clientErr nonRetryableClientErr
			return errors.As(err, &clientErr)
		},
		OnStateChange: func(name string, from, to gobreaker.State) {
			logger.Warn("embedder circuit breaker state changed",
				slog.String("breaker", name),
				slog.String("from", from.String()),
				slog.String("to", to.String()),
			)
		},
	}

	return &OpenAIEmbedder{
		baseURL: baseURL,
		apiKey:  apiKey,
		model:   model,
		dim:     dim,
		timeout: timeout,
		client:  &http.Client{Timeout: timeout},
		cb:      gobreaker.NewCircuitBreaker[[][]float32](settings),
		logger:  logger,
	}, nil
}

func (o *OpenAIEmbedder) Dim() int     { return o.dim }
func (o *OpenAIEmbedder) Mode() string { return ModeOpenAI }

// CircuitState exposes circuit breaker state for diagnostics and tests.
func (o *OpenAIEmbedder) CircuitState() gobreaker.State { return o.cb.State() }

func (o *OpenAIEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	vecs, err := o.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) != 1 {
		return nil, fmt.Errorf("expected 1 embedding, got %d", len(vecs))
	}
	return vecs[0], nil
}

type openAIEmbedRequest struct {
	Model      string   `json:"model"`
	Input      []string `json:"input"`
	Dimensions int      `json:"dimensions,omitempty"`
}

type openAIEmbedResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

func (o *OpenAIEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	res, err := o.cb.Execute(func() ([][]float32, error) {
		return o.callEndpoint(ctx, texts)
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (o *OpenAIEmbedder) callEndpoint(ctx context.Context, texts []string) ([][]float32, error) {
	reqCtx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()

	bodyBytes, err := json.Marshal(openAIEmbedRequest{
		Model:      o.model,
		Input:      texts,
		Dimensions: o.dim,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal embedding request: %w", err)
	}

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, o.baseURL+"/embeddings", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create embedding http request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.apiKey)

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedding request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxRespBodyBytes))
	if readErr != nil {
		return nil, fmt.Errorf("read embedding response: %w", readErr)
	}

	if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
		return nil, nonRetryableClientErr{err: fmt.Errorf("embedding endpoint rejected request with status %d", resp.StatusCode)}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedding endpoint returned status %d", resp.StatusCode)
	}

	var parsed openAIEmbedResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("decode embedding response: %w", err)
	}
	if len(parsed.Data) != len(texts) {
		return nil, fmt.Errorf("embedding response count mismatch: got %d want %d", len(parsed.Data), len(texts))
	}

	sort.Slice(parsed.Data, func(i, j int) bool {
		return parsed.Data[i].Index < parsed.Data[j].Index
	})

	out := make([][]float32, len(parsed.Data))
	for i, item := range parsed.Data {
		if len(item.Embedding) != o.dim {
			return nil, fmt.Errorf("embedding[%d] dimension mismatch: got %d want %d", i, len(item.Embedding), o.dim)
		}
		vec := make([]float32, o.dim)
		copy(vec, item.Embedding)
		normalizeL2(vec)
		out[i] = vec
	}
	return out, nil
}
