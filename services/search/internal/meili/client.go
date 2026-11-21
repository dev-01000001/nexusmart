package meili

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nexusmart/core/services/search/internal/normalize"
	"github.com/nexusmart/core/services/search/internal/resilience"
)

const (
	PriceBucketUnder200K    = "0-200000"
	PriceBucket200KTo500K   = "200000-500000"
	PriceBucket500KTo1M     = "500000-1000000"
	PriceBucketOver1M       = "1000000+"
	DefaultIndexName        = "products"
	defaultSuggestLimit     = 8
	maxSuggestLimit         = 20
	maxResponseBodyBytes    = 32 << 20
	defaultTaskPollInterval = 20 * time.Millisecond
	maxTaskPollInterval     = 250 * time.Millisecond
)

// PriceBucketOrder defines canonical display ordering for price_ranges facets.
var PriceBucketOrder = []string{
	PriceBucketUnder200K,
	PriceBucket200KTo500K,
	PriceBucket500KTo1M,
	PriceBucketOver1M,
}

// PriceBucket returns the canonical price facet bucket for a VND price.
func PriceBucket(priceVND int64) string {
	switch {
	case priceVND < 200_000:
		return PriceBucketUnder200K
	case priceVND < 500_000:
		return PriceBucket200KTo500K
	case priceVND < 1_000_000:
		return PriceBucket500KTo1M
	default:
		return PriceBucketOver1M
	}
}

// PriceBucketLabel returns a human-readable Vietnamese label for a price bucket.
func PriceBucketLabel(bucket string) string {
	switch bucket {
	case PriceBucketUnder200K:
		return "Dưới 200.000đ"
	case PriceBucket200KTo500K:
		return "200.000đ - 500.000đ"
	case PriceBucket500KTo1M:
		return "500.000đ - 1.000.000đ"
	case PriceBucketOver1M:
		return "Trên 1.000.000đ"
	default:
		return bucket
	}
}

// HTTPError represents a non-2xx HTTP response from Meilisearch.
type HTTPError struct {
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("meilisearch http %d: %s", e.StatusCode, e.Body)
}

// Document is the indexed product representation stored in Meilisearch.
type Document struct {
	ID                 string   `json:"id"`
	ShopID             string   `json:"shop_id"`
	CategoryID         string   `json:"category_id"`
	CategorySlug       string   `json:"category_slug"`
	CategoryName       string   `json:"category_name"`
	CategoryNameFolded string   `json:"category_name_folded"`
	Name               string   `json:"name"`
	NameFolded         string   `json:"name_folded"`
	Description        string   `json:"description"`
	DescriptionFolded  string   `json:"description_folded"`
	SKUTextFolded      string   `json:"sku_text_folded"`
	Images             []string `json:"images"`
	Status             string   `json:"status"`
	MinPriceVND        int64    `json:"min_price_vnd"`
	MaxPriceVND        int64    `json:"max_price_vnd"`
	PriceBucket        string   `json:"price_bucket"`
	TotalStock         int32    `json:"total_stock"`
	InStock            bool     `json:"in_stock"`
	CreatedAtUnix      int64    `json:"created_at_unix"`
	UpdatedAtUnix      int64    `json:"updated_at_unix"`
	RankingScore       float64  `json:"_rankingScore,omitempty"`
}

// SearchParams configures a Meilisearch query.
type SearchParams struct {
	Query       string
	ShopID      string
	Category    string
	MinPriceVND int64
	MaxPriceVND int64
	InStockOnly bool
	Sort        string
	Limit       int
	Offset      int
	Facets      []string
}

// SearchResult holds hits, total count, and facet distributions returned by Meilisearch.
type SearchResult struct {
	Hits              []Document
	TotalHits         int64
	FacetDistribution map[string]map[string]int64
}

// Suggestion represents a deduplicated autocomplete suggestion.
type Suggestion struct {
	Text         string
	Kind         string // "category" or "product"
	ProductID    string
	CategorySlug string
	MinPriceVND  int64
}

// Config configures Client.
type Config struct {
	BaseURL    string
	APIKey     string
	Index      string
	HTTPClient *http.Client
	Resilience *resilience.Executor
	Logger     *slog.Logger
}

// Client is a minimal stdlib net/http Meilisearch v1 client with timeout, retry, and circuit breaker.
type Client struct {
	baseURL    string
	apiKey     string
	index      string
	httpClient *http.Client
	exec       *resilience.Executor
}

// NewClient creates a Meilisearch v1 HTTP client.
func NewClient(cfg Config) *Client {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	index := strings.TrimSpace(cfg.Index)
	if index == "" {
		index = DefaultIndexName
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: 15 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 100,
				IdleConnTimeout:     90 * time.Second,
			},
		}
	}
	exec := cfg.Resilience
	if exec == nil {
		rcfg := resilience.DefaultConfig("meilisearch", cfg.Logger)
		rcfg.AttemptTimeout = 5 * time.Second
		rcfg.MaxAttempts = 3
		exec = resilience.NewExecutor(rcfg)
	}
	return &Client{
		baseURL:    baseURL,
		apiKey:     cfg.APIKey,
		index:      index,
		httpClient: httpClient,
		exec:       exec,
	}
}

type taskEnqueueResponse struct {
	TaskUID *int64 `json:"taskUid"`
	UID     *int64 `json:"uid"`
}

func (r taskEnqueueResponse) id() int64 {
	if r.TaskUID != nil {
		return *r.TaskUID
	}
	if r.UID != nil {
		return *r.UID
	}
	return -1
}

type taskStatusResponse struct {
	UID    int64  `json:"uid"`
	Status string `json:"status"`
	Error  *struct {
		Message string `json:"message"`
		Code    string `json:"code"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// EnsureIndex idempotently creates the Meilisearch index and applies NexusMart search settings.
func (c *Client) EnsureIndex(ctx context.Context) error {
	// Check if index exists first.
	_, err := c.doJSON(ctx, "get_index", http.MethodGet, "/indexes/"+url.PathEscape(c.index), nil)
	if err != nil {
		var httpErr *HTTPError
		if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusNotFound {
			return fmt.Errorf("check meilisearch index %s: %w", c.index, err)
		}
		createBody, err := c.doJSON(ctx, "create_index", http.MethodPost, "/indexes", map[string]string{
			"uid":        c.index,
			"primaryKey": "id",
		})
		if err != nil {
			return fmt.Errorf("create meilisearch index %s: %w", c.index, err)
		}
		var taskResp taskEnqueueResponse
		if err := json.Unmarshal(createBody, &taskResp); err == nil && taskResp.id() >= 0 {
			if waitErr := c.waitTaskAllowCode(ctx, taskResp.id(), "index_already_exists"); waitErr != nil {
				return fmt.Errorf("wait create index %s: %w", c.index, waitErr)
			}
		}
	}

	settings := map[string]any{
		"searchableAttributes": []string{
			"name_folded",
			"category_name_folded",
			"sku_text_folded",
			"description_folded",
		},
		"filterableAttributes": []string{
			"shop_id",
			"category_id",
			"category_slug",
			"min_price_vnd",
			"max_price_vnd",
			"price_bucket",
			"status",
			"in_stock",
		},
		"sortableAttributes": []string{
			"min_price_vnd",
			"max_price_vnd",
			"created_at_unix",
		},
		"faceting": map[string]any{
			"maxValuesPerFacet": 100,
		},
		"pagination": map[string]any{
			"maxTotalHits": 100000,
		},
		"typoTolerance": map[string]any{
			"enabled": true,
			"minWordSizeForTypos": map[string]int{
				"oneTypo":  4,
				"twoTypos": 8,
			},
		},
	}

	raw, err := c.doJSON(ctx, "update_settings", http.MethodPatch, "/indexes/"+url.PathEscape(c.index)+"/settings", settings)
	if err != nil {
		return fmt.Errorf("update meilisearch settings: %w", err)
	}
	var taskResp taskEnqueueResponse
	if err := json.Unmarshal(raw, &taskResp); err == nil && taskResp.id() >= 0 {
		if waitErr := c.WaitTask(ctx, taskResp.id()); waitErr != nil {
			return fmt.Errorf("wait meilisearch settings task: %w", waitErr)
		}
	}
	return nil
}

// UpsertDocuments upserts a batch of documents into the Meilisearch index and returns the task UID.
func (c *Client) UpsertDocuments(ctx context.Context, docs []Document) (int64, error) {
	if len(docs) == 0 {
		return -1, nil
	}
	raw, err := c.doJSON(
		ctx,
		"upsert_documents",
		http.MethodPut,
		"/indexes/"+url.PathEscape(c.index)+"/documents?primaryKey=id",
		docs,
	)
	if err != nil {
		return -1, err
	}
	var taskResp taskEnqueueResponse
	if err := json.Unmarshal(raw, &taskResp); err != nil {
		return -1, fmt.Errorf("decode upsert task response: %w", err)
	}
	return taskResp.id(), nil
}

// DeleteDocument deletes a single document by ID from Meilisearch and returns the task UID.
func (c *Client) DeleteDocument(ctx context.Context, id string) (int64, error) {
	if strings.TrimSpace(id) == "" {
		return -1, nil
	}
	raw, err := c.doJSON(
		ctx,
		"delete_document",
		http.MethodDelete,
		"/indexes/"+url.PathEscape(c.index)+"/documents/"+url.PathEscape(id),
		nil,
	)
	if err != nil {
		var httpErr *HTTPError
		if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
			return -1, nil
		}
		return -1, err
	}
	var taskResp taskEnqueueResponse
	if err := json.Unmarshal(raw, &taskResp); err != nil {
		return -1, fmt.Errorf("decode delete task response: %w", err)
	}
	return taskResp.id(), nil
}

// WaitTask polls Meilisearch until the given task finishes or ctx expires.
func (c *Client) WaitTask(ctx context.Context, taskUID int64) error {
	return c.waitTaskAllowCode(ctx, taskUID, "")
}

func (c *Client) waitTaskAllowCode(ctx context.Context, taskUID int64, allowErrCode string) error {
	if taskUID < 0 {
		return nil
	}
	interval := defaultTaskPollInterval
	for {
		raw, err := c.doJSON(ctx, "get_task", http.MethodGet, fmt.Sprintf("/tasks/%d", taskUID), nil)
		if err != nil {
			return err
		}
		var st taskStatusResponse
		if err := json.Unmarshal(raw, &st); err != nil {
			return fmt.Errorf("decode task status %d: %w", taskUID, err)
		}
		switch st.Status {
		case "succeeded":
			return nil
		case "failed", "canceled":
			if st.Error != nil && allowErrCode != "" && st.Error.Code == allowErrCode {
				return nil
			}
			if st.Error != nil {
				return fmt.Errorf("meilisearch task %d %s (%s): %s", taskUID, st.Status, st.Error.Code, st.Error.Message)
			}
			return fmt.Errorf("meilisearch task %d %s", taskUID, st.Status)
		}

		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if interval < maxTaskPollInterval {
			interval *= 2
			if interval > maxTaskPollInterval {
				interval = maxTaskPollInterval
			}
		}
	}
}

// Search queries Meilisearch with Vietnamese normalization, injection-safe CNF filters, sorting, and facets.
func (c *Client) Search(ctx context.Context, params SearchParams) (*SearchResult, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = 20
	}
	offset := params.Offset
	if offset < 0 {
		offset = 0
	}

	reqBody := map[string]any{
		"q":                normalize.NormalizeVietnamese(params.Query),
		"limit":            limit,
		"offset":           offset,
		"showRankingScore": true,
	}

	if filters := buildFilters(params); len(filters) > 0 {
		reqBody["filter"] = filters
	}
	if sortExpr := buildSort(params.Sort); len(sortExpr) > 0 {
		reqBody["sort"] = sortExpr
	}
	if len(params.Facets) > 0 {
		reqBody["facets"] = params.Facets
	}

	raw, err := c.doJSON(ctx, "search", http.MethodPost, "/indexes/"+url.PathEscape(c.index)+"/search", reqBody)
	if err != nil {
		return nil, err
	}

	var decoded struct {
		Hits               []Document                  `json:"hits"`
		EstimatedTotalHits int64                       `json:"estimatedTotalHits"`
		TotalHits          *int64                      `json:"totalHits,omitempty"`
		FacetDistribution  map[string]map[string]int64 `json:"facetDistribution,omitempty"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("decode meilisearch search response: %w", err)
	}

	total := decoded.EstimatedTotalHits
	if decoded.TotalHits != nil {
		total = *decoded.TotalHits
	}
	if decoded.Hits == nil {
		decoded.Hits = []Document{}
	}
	return &SearchResult{
		Hits:              decoded.Hits,
		TotalHits:         total,
		FacetDistribution: decoded.FacetDistribution,
	}, nil
}

// Suggest queries Meilisearch for prefix/typo-tolerant autocomplete suggestions,
// returning deduplicated category and product suggestions.
func (c *Client) Suggest(ctx context.Context, q, shopID string, limit int) ([]Suggestion, error) {
	folded := normalize.NormalizeVietnamese(q)
	if folded == "" {
		return []Suggestion{}, nil
	}
	if limit <= 0 {
		limit = defaultSuggestLimit
	}
	if limit > maxSuggestLimit {
		limit = maxSuggestLimit
	}

	fetchLimit := limit * 2
	if fetchLimit < 16 {
		fetchLimit = 16
	}

	res, err := c.Search(ctx, SearchParams{
		Query:  folded,
		ShopID: shopID,
		Limit:  fetchLimit,
		Offset: 0,
	})
	if err != nil {
		return nil, err
	}

	maxCats := limit / 2
	if maxCats > 3 {
		maxCats = 3
	}
	if maxCats == 0 && limit > 0 {
		maxCats = 1
	}

	seenCats := make(map[string]bool)
	seenProdIDs := make(map[string]bool)
	seenProdNames := make(map[string]bool)

	catSuggestions := make([]Suggestion, 0, maxCats)
	prodSuggestions := make([]Suggestion, 0, limit)

	for _, hit := range res.Hits {
		if len(catSuggestions) < maxCats && hit.CategorySlug != "" && !seenCats[hit.CategorySlug] {
			catFolded := hit.CategoryNameFolded
			if catFolded == "" {
				catFolded = normalize.NormalizeVietnamese(hit.CategoryName)
			}
			if strings.Contains(catFolded, folded) || strings.Contains(hit.CategorySlug, folded) {
				seenCats[hit.CategorySlug] = true
				label := hit.CategoryName
				if label == "" {
					label = hit.CategorySlug
				}
				catSuggestions = append(catSuggestions, Suggestion{
					Text:         label,
					Kind:         "category",
					CategorySlug: hit.CategorySlug,
				})
			}
		}

		if hit.ID != "" && !seenProdIDs[hit.ID] {
			nameKey := normalize.NormalizeVietnamese(hit.Name)
			if nameKey == "" || seenProdNames[nameKey] {
				continue
			}
			seenProdIDs[hit.ID] = true
			seenProdNames[nameKey] = true
			prodSuggestions = append(prodSuggestions, Suggestion{
				Text:         hit.Name,
				Kind:         "product",
				ProductID:    hit.ID,
				CategorySlug: hit.CategorySlug,
				MinPriceVND:  hit.MinPriceVND,
			})
		}
	}

	out := make([]Suggestion, 0, limit)
	out = append(out, catSuggestions...)
	for _, ps := range prodSuggestions {
		if len(out) >= limit {
			break
		}
		out = append(out, ps)
	}
	return out, nil
}

// Ping checks Meilisearch health via GET /health.
func (c *Client) Ping(ctx context.Context) error {
	raw, err := c.doJSON(ctx, "health", http.MethodGet, "/health", nil)
	if err != nil {
		return err
	}
	var resp struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(raw, &resp); err == nil && resp.Status != "" && resp.Status != "available" {
		return fmt.Errorf("meilisearch status %q", resp.Status)
	}
	return nil
}

// BuildFilters constructs injection-safe CNF filter expressions for Meilisearch.
func buildFilters(params SearchParams) []any {
	var filters []any
	if shopID := strings.TrimSpace(params.ShopID); shopID != "" {
		filters = append(filters, "shop_id = "+EscapeFilterString(shopID))
	}
	if cat := strings.TrimSpace(params.Category); cat != "" {
		escaped := EscapeFilterString(cat)
		filters = append(filters, []string{
			"category_slug = " + escaped,
			"category_id = " + escaped,
		})
	}
	if params.MinPriceVND > 0 {
		filters = append(filters, fmt.Sprintf("min_price_vnd >= %d", params.MinPriceVND))
	}
	if params.MaxPriceVND > 0 {
		filters = append(filters, fmt.Sprintf("min_price_vnd <= %d", params.MaxPriceVND))
	}
	if params.InStockOnly {
		filters = append(filters, "in_stock = true")
	}
	return filters
}

func buildSort(sortBy string) []string {
	switch strings.ToLower(strings.TrimSpace(sortBy)) {
	case "price_asc":
		return []string{"min_price_vnd:asc", "created_at_unix:desc"}
	case "price_desc":
		return []string{"max_price_vnd:desc", "min_price_vnd:desc", "created_at_unix:desc"}
	case "newest":
		return []string{"created_at_unix:desc"}
	default:
		return nil
	}
}

// EscapeFilterString safely quotes a string literal for Meilisearch filter expressions,
// neutralizing quotes, backslashes, and control characters to prevent filter injection.
func EscapeFilterString(raw string) string {
	var b strings.Builder
	b.Grow(len(raw) + 2)
	b.WriteByte('"')
	for _, r := range raw {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\r', '\n', '\x00':
			continue
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func (c *Client) doJSON(ctx context.Context, op, method, path string, body any) ([]byte, error) {
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal meilisearch %s body: %w", op, err)
		}
	}

	return resilience.Do(ctx, c.exec, op, func(attemptCtx context.Context) ([]byte, error) {
		var reader io.Reader
		if payload != nil {
			reader = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(attemptCtx, method, c.baseURL+path, reader)
		if err != nil {
			return nil, resilience.NonRetryable(fmt.Errorf("build meilisearch request: %w", err))
		}
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if c.apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.apiKey)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("meilisearch %s request: %w", op, err)
		}
		defer func() {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}()

		respBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))
		if err != nil {
			return nil, fmt.Errorf("read meilisearch %s response: %w", op, err)
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			httpErr := &HTTPError{
				StatusCode: resp.StatusCode,
				Body:       strings.TrimSpace(string(respBytes)),
			}
			if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
				return nil, resilience.NonRetryable(httpErr)
			}
			return nil, httpErr
		}
		return respBytes, nil
	})
}
