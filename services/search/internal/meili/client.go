package meili

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/meilisearch/meilisearch-go"
	searchcfg "github.com/nexusmart/core/services/search/config"
)

const (
	ProductsIndexUID         = "products"
	defaultReconnectInterval = 3 * time.Second
	httpWriteTimeout         = 5 * time.Second
	// K3: Event path task wait is 15s (HTTP 5s + wait 15s = 20s < consumer procCtx 25s).
	EventTaskWaitTimeout    = 15 * time.Second
	ReindexTaskWaitTimeout  = 60 * time.Second
	settingsTaskWaitTimeout = 15 * time.Second
	defaultTaskPollInterval = 25 * time.Millisecond
)

type taskWaitTimeoutKey struct{}

// WithTaskWaitTimeout attaches a custom Meilisearch task-wait deadline for write calls on ctx.
func WithTaskWaitTimeout(ctx context.Context, d time.Duration) context.Context {
	if d <= 0 {
		return ctx
	}
	return context.WithValue(ctx, taskWaitTimeoutKey{}, d)
}

func taskWaitTimeoutFromContext(ctx context.Context, fallback time.Duration) time.Duration {
	if ctx != nil {
		if d, ok := ctx.Value(taskWaitTimeoutKey{}).(time.Duration); ok && d > 0 {
			return d
		}
	}
	return fallback
}

// ProductDoc is the public Meilisearch document schema.
// Strictly omits commission_rate_bps and flagged_keywords (D1/E10-bis/G9).
type ProductDoc struct {
	ID                  string   `json:"id"`
	ShopID              string   `json:"shop_id"`
	ShopName            string   `json:"shop_name"`
	ShopSlug            string   `json:"shop_slug"`
	CategoryID          string   `json:"category_id"`
	CategorySlug        string   `json:"category_slug"`
	CategoryName        string   `json:"category_name"`
	CategoryAncestorIDs []string `json:"category_ancestor_ids"`
	Name                string   `json:"name"`
	NameFolded          string   `json:"name_folded"`
	Slug                string   `json:"slug"`
	DescriptionSnippet  string   `json:"description_snippet"`
	DescriptionFolded   string   `json:"description_folded"`
	ThumbnailURL        string   `json:"thumbnail_url"`
	MinPriceVND         int64    `json:"min_price_vnd"`
	MaxPriceVND         int64    `json:"max_price_vnd"`
	Status              string   `json:"status"`
	RatingAvgX100       int32    `json:"rating_avg_x100"`
	ReviewCount         int32    `json:"review_count"`
	InStock             bool     `json:"in_stock"`
	SKUCount            int32    `json:"sku_count"`
	SKUCodes            []string `json:"sku_codes"`
	SKUAttributesText   string   `json:"sku_attributes_text"`
	SKUIDs              []string `json:"sku_ids"`
	DefaultSKUID        string   `json:"default_sku_id"`
	Colors              []string `json:"colors"`
	ColorsFolded        []string `json:"colors_folded"`
	ColorCodes          []string `json:"color_codes"`
	Sizes               []string `json:"sizes"`
	SizesFolded         []string `json:"sizes_folded"`
	UpdatedAtUnix       int64    `json:"updated_at_unix"`
}

// SearchQuery defines keyword/facet/filter parameters passed to the Meilisearch engine.
type SearchQuery struct {
	Query               string
	CategoryID          string
	ShopID              string
	ExcludedShopIDs     []string
	MinPriceVND         int64
	MaxPriceVND         int64
	MinRatingX100       int32
	InStockOnly         bool
	OnlyApprovedInStock bool
	SizeFolded          string
	ColorFolded         string
	SortBy              string
	Limit               int64
	Offset              int64
	Facets              []string
}

// SearchResult holds decoded hits, total count, and facet distributions.
type SearchResult struct {
	Hits              []ProductDoc
	Total             int64
	FacetDistribution map[string]map[string]int64
}

// Engine defines the Meilisearch operations required by the search service.
type Engine interface {
	EnsureConfigured(ctx context.Context) error
	StartReconnectLoop(ctx context.Context)
	IsConfigured() bool
	Search(ctx context.Context, req SearchQuery) (*SearchResult, error)
	UpsertProducts(ctx context.Context, docs []ProductDoc, wait bool) error
	DeleteProduct(ctx context.Context, productID string, wait bool) error
	DeleteProducts(ctx context.Context, productIDs []string, wait bool) error
}

// Client wraps meilisearch-go with auto-configuration, timeout enforcement, and
// resilient startup/reconnect behavior (G13/K3/K4).
type Client struct {
	svc               meilisearch.ServiceManager
	indexUID          string
	timeout           time.Duration
	reconnectInterval time.Duration
	synonyms          map[string][]string
	logger            *slog.Logger

	cfgMu                  sync.Mutex
	configured             atomic.Bool
	hasPendingSettingsTask bool
	pendingSettingsTaskUID int64
	cfgSubmitting          bool
	cfgSubmitCh            chan struct{}
}

// Options customizes Client construction.
type Options struct {
	URL               string
	APIKey            string
	IndexUID          string
	Timeout           time.Duration
	ReconnectInterval time.Duration
	Synonyms          map[string][]string
	HTTPClient        *http.Client
	Logger            *slog.Logger
}

// NewClient constructs a Meilisearch Client without requiring Meilisearch to be online at startup.
func NewClient(opts Options) *Client {
	indexUID := strings.TrimSpace(opts.IndexUID)
	if indexUID == "" {
		indexUID = ProductsIndexUID
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 1500 * time.Millisecond
	}
	reconnect := opts.ReconnectInterval
	if reconnect <= 0 {
		reconnect = defaultReconnectInterval
	}
	syns := opts.Synonyms
	if syns == nil {
		syns = searchcfg.MustLoadSynonyms()
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: maxDuration(timeout*2, httpWriteTimeout)}
	}

	meiliOpts := []meilisearch.Option{meilisearch.WithCustomClient(httpClient)}
	if strings.TrimSpace(opts.APIKey) != "" {
		meiliOpts = append(meiliOpts, meilisearch.WithAPIKey(opts.APIKey))
	}

	return &Client{
		svc:               meilisearch.New(strings.TrimRight(opts.URL, "/"), meiliOpts...),
		indexUID:          indexUID,
		timeout:           timeout,
		reconnectInterval: reconnect,
		synonyms:          syns,
		logger:            logger,
	}
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

// IsConfigured reports whether the Meilisearch index and settings have been applied since the last connection failure.
func (c *Client) IsConfigured() bool {
	return c.configured.Load()
}

// EnsureConfigured checks Meilisearch health, creates the products index if missing, and applies
// searchable/filterable/sortable/synonym/typo settings idempotently (K3).
// If the settings task has not finished within ctx, configured stays false and the task UID is
// remembered so subsequent calls wait on the same task instead of piling up UpdateSettings requests.
func (c *Client) EnsureConfigured(ctx context.Context) error {
	if c.configured.Load() {
		return nil
	}

	taskUID, hasTask, alreadyDone, err := c.acquireSettingsTaskUID(ctx)
	if err != nil {
		return err
	}
	if alreadyDone {
		return nil
	}

	if hasTask {
		waitTimeout := taskWaitTimeoutFromContext(ctx, settingsTaskWaitTimeout)
		if waitErr := c.waitTask(ctx, taskUID, waitTimeout); waitErr != nil {
			if !errors.Is(waitErr, context.DeadlineExceeded) && !errors.Is(waitErr, context.Canceled) {
				// Task failed or disappeared on server: forget UID so the next call re-submits settings.
				c.cfgMu.Lock()
				if c.hasPendingSettingsTask && c.pendingSettingsTaskUID == taskUID {
					c.hasPendingSettingsTask = false
					c.pendingSettingsTaskUID = 0
				}
				c.cfgMu.Unlock()
			}
			return fmt.Errorf("wait meilisearch settings task: %w", waitErr)
		}

		c.cfgMu.Lock()
		if c.hasPendingSettingsTask && c.pendingSettingsTaskUID == taskUID {
			c.hasPendingSettingsTask = false
			c.pendingSettingsTaskUID = 0
			c.configured.Store(true)
			c.cfgMu.Unlock()
			c.logger.Info("meilisearch index configured", slog.String("index", c.indexUID))
			return nil
		}
		isCfg := c.configured.Load()
		c.cfgMu.Unlock()
		if isCfg {
			return nil
		}
		return errors.New("meilisearch configuration invalidated while waiting for settings task")
	}

	c.cfgMu.Lock()
	c.configured.Store(true)
	c.cfgMu.Unlock()
	c.logger.Info("meilisearch index configured", slog.String("index", c.indexUID))
	return nil
}

func (c *Client) acquireSettingsTaskUID(ctx context.Context) (taskUID int64, hasTask bool, alreadyDone bool, err error) {
	for {
		c.cfgMu.Lock()
		if c.configured.Load() {
			c.cfgMu.Unlock()
			return 0, false, true, nil
		}
		if c.hasPendingSettingsTask {
			uid := c.pendingSettingsTaskUID
			c.cfgMu.Unlock()
			return uid, true, false, nil
		}
		if c.cfgSubmitting {
			waitCh := c.cfgSubmitCh
			c.cfgMu.Unlock()
			select {
			case <-waitCh:
				continue
			case <-ctx.Done():
				return 0, false, false, fmt.Errorf("wait meilisearch configuration submission: %w", ctx.Err())
			}
		}
		c.cfgSubmitting = true
		submitCh := make(chan struct{})
		c.cfgSubmitCh = submitCh
		c.cfgMu.Unlock()

		uid, gotTask, submitErr := c.submitIndexAndSettings(ctx)

		c.cfgMu.Lock()
		if submitErr == nil && gotTask {
			c.hasPendingSettingsTask = true
			c.pendingSettingsTaskUID = uid
		}
		c.cfgSubmitting = false
		c.cfgSubmitCh = nil
		close(submitCh)
		c.cfgMu.Unlock()

		return uid, gotTask, false, submitErr
	}
}

func (c *Client) submitIndexAndSettings(ctx context.Context) (int64, bool, error) {
	healthCtx, cancel := context.WithTimeout(ctx, c.timeout)
	_, err := c.svc.Health(healthCtx)
	cancel()
	if err != nil {
		return 0, false, fmt.Errorf("meilisearch health check failed: %w", err)
	}

	createCtx, createCancel := context.WithTimeout(ctx, httpWriteTimeout)
	task, createErr := c.svc.CreateIndex(createCtx, &meilisearch.IndexConfig{
		Uid:        c.indexUID,
		PrimaryKey: "id",
	})
	createCancel()
	if createErr != nil && !isIndexAlreadyExistsErr(createErr) {
		return 0, false, fmt.Errorf("create meilisearch index %s: %w", c.indexUID, createErr)
	}
	if createErr == nil && task != nil {
		waitTimeout := taskWaitTimeoutFromContext(ctx, settingsTaskWaitTimeout)
		if waitErr := c.waitTask(ctx, task.TaskUID, waitTimeout); waitErr != nil && !isIndexAlreadyExistsErr(waitErr) {
			return 0, false, fmt.Errorf("wait create index task: %w", waitErr)
		}
	}

	settings := &meilisearch.Settings{
		// "sort" first so an explicit price/rating/newest order is strict; it is a no-op for relevance searches.
		RankingRules: []string{"sort", "words", "typo", "proximity", "attribute", "exactness"},
		SearchableAttributes: []string{
			"name_folded",
			"name",
			"sku_codes",
			"sku_attributes_text",
			"description_folded",
			"shop_name",
			"category_name",
		},
		FilterableAttributes: []string{
			"status",
			"shop_id",
			"category_id",
			"category_ancestor_ids",
			"min_price_vnd",
			"max_price_vnd",
			"rating_avg_x100",
			"in_stock",
			"colors",
			"colors_folded",
			"color_codes",
			"sizes",
			"sizes_folded",
		},
		SortableAttributes: []string{
			"min_price_vnd",
			"max_price_vnd",
			"rating_avg_x100",
			"review_count",
			"updated_at_unix",
		},
		Synonyms: c.synonyms,
		TypoTolerance: &meilisearch.TypoTolerance{
			Enabled: true,
			MinWordSizeForTypos: meilisearch.MinWordSizeForTypos{
				OneTypo:  3,
				TwoTypos: 7,
			},
		},
		Faceting: &meilisearch.Faceting{
			MaxValuesPerFacet: 200,
		},
		Pagination: &meilisearch.Pagination{
			MaxTotalHits: 10000,
		},
	}

	setCtx, setCancel := context.WithTimeout(ctx, httpWriteTimeout)
	setTask, setErr := c.svc.Index(c.indexUID).UpdateSettings(setCtx, settings)
	setCancel()
	if setErr != nil {
		return 0, false, fmt.Errorf("update meilisearch settings: %w", setErr)
	}
	if setTask == nil {
		return 0, false, nil
	}
	return setTask.TaskUID, true, nil
}

// StartReconnectLoop watches for Meilisearch coming online when unconfigured and reloads index settings.
func (c *Client) StartReconnectLoop(ctx context.Context) {
	ticker := time.NewTicker(c.reconnectInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if c.configured.Load() {
				continue
			}
			if err := c.EnsureConfigured(ctx); err != nil {
				c.logger.Debug("meilisearch reconnect attempt failed", slog.String("error", err.Error()))
			}
		}
	}
}

// Search executes a filtered keyword search against Meilisearch (bounded by c.timeout).
func (c *Client) Search(ctx context.Context, req SearchQuery) (*SearchResult, error) {
	filters, err := BuildFilters(req)
	if err != nil {
		return nil, err
	}

	var sortExpr []string
	switch req.SortBy {
	case "", "relevance":
	case "price_asc":
		sortExpr = []string{"min_price_vnd:asc"}
	case "price_desc":
		sortExpr = []string{"min_price_vnd:desc"}
	case "newest":
		sortExpr = []string{"updated_at_unix:desc"}
	case "rating_desc":
		sortExpr = []string{"rating_avg_x100:desc", "review_count:desc"}
	default:
		return nil, fmt.Errorf("unsupported sort_by %q", req.SortBy)
	}

	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	if !c.configured.Load() {
		if err := c.EnsureConfigured(callCtx); err != nil {
			return nil, err
		}
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}
	searchReq := &meilisearch.SearchRequest{
		Limit:  limit,
		Offset: req.Offset,
		Filter: filters,
		Sort:   sortExpr,
		Facets: req.Facets,
	}
	if len(sortExpr) > 0 {
		// With an explicit sort, partial matches would otherwise jump ahead of real ones.
		searchReq.MatchingStrategy = meilisearch.All
	}

	resp, err := c.svc.Index(c.indexUID).Search(callCtx, req.Query, searchReq)
	if err != nil {
		c.markUnconfiguredOnError(err)
		return nil, fmt.Errorf("meilisearch search: %w", err)
	}

	res, err := decodeSearchResponse(resp)
	if err != nil {
		return nil, err
	}

	// K4: Under explicit sort, if strict matchingStrategy=all returns 0 hits for a multi-word query,
	// fall back ONCE to matchingStrategy=last with identical filter/sort/facet/pagination parameters.
	if len(sortExpr) > 0 && res.Total == 0 && len(res.Hits) == 0 && len(strings.Fields(req.Query)) >= 2 {
		fallbackReq := *searchReq
		fallbackReq.MatchingStrategy = meilisearch.Last
		fbResp, fbErr := c.svc.Index(c.indexUID).Search(callCtx, req.Query, &fallbackReq)
		if fbErr != nil {
			c.markUnconfiguredOnError(fbErr)
			return nil, fmt.Errorf("meilisearch search fallback: %w", fbErr)
		}
		return decodeSearchResponse(fbResp)
	}

	return res, nil
}

func decodeSearchResponse(resp *meilisearch.SearchResponse) (*SearchResult, error) {
	docs := make([]ProductDoc, 0, len(resp.Hits))
	if err := resp.Hits.DecodeInto(&docs); err != nil {
		return nil, fmt.Errorf("decode meilisearch hits: %w", err)
	}

	total := resp.EstimatedTotalHits
	if resp.TotalHits > total {
		total = resp.TotalHits
	}

	var facets map[string]map[string]int64
	if len(resp.FacetDistribution) > 0 {
		_ = json.Unmarshal(resp.FacetDistribution, &facets)
	}

	return &SearchResult{
		Hits:              docs,
		Total:             total,
		FacetDistribution: facets,
	}, nil
}

// UpsertProducts indexes one or more public product documents into Meilisearch.
func (c *Client) UpsertProducts(ctx context.Context, docs []ProductDoc, wait bool) error {
	if len(docs) == 0 {
		return nil
	}
	if !c.configured.Load() {
		if err := c.EnsureConfigured(ctx); err != nil {
			return err
		}
	}

	pk := "id"
	callCtx, cancel := context.WithTimeout(ctx, maxDuration(c.timeout*2, httpWriteTimeout))
	task, err := c.svc.Index(c.indexUID).AddDocuments(callCtx, docs, &meilisearch.DocumentOptions{PrimaryKey: &pk})
	cancel()
	if err != nil {
		c.markUnconfiguredOnError(err)
		return fmt.Errorf("meilisearch add documents: %w", err)
	}
	if wait && task != nil {
		waitTimeout := taskWaitTimeoutFromContext(ctx, EventTaskWaitTimeout)
		if waitErr := c.waitTask(ctx, task.TaskUID, waitTimeout); waitErr != nil {
			return waitErr
		}
	}
	return nil
}

// DeleteProduct deletes one product document by ID from Meilisearch.
func (c *Client) DeleteProduct(ctx context.Context, productID string, wait bool) error {
	if strings.TrimSpace(productID) == "" {
		return nil
	}
	if !c.configured.Load() {
		if err := c.EnsureConfigured(ctx); err != nil {
			return err
		}
	}

	callCtx, cancel := context.WithTimeout(ctx, maxDuration(c.timeout*2, httpWriteTimeout))
	task, err := c.svc.Index(c.indexUID).DeleteDocument(callCtx, productID, nil)
	cancel()
	if err != nil {
		c.markUnconfiguredOnError(err)
		return fmt.Errorf("meilisearch delete document: %w", err)
	}
	if wait && task != nil {
		waitTimeout := taskWaitTimeoutFromContext(ctx, EventTaskWaitTimeout)
		if waitErr := c.waitTask(ctx, task.TaskUID, waitTimeout); waitErr != nil {
			return waitErr
		}
	}
	return nil
}

// DeleteProducts deletes multiple product documents by ID from Meilisearch.
func (c *Client) DeleteProducts(ctx context.Context, productIDs []string, wait bool) error {
	if len(productIDs) == 0 {
		return nil
	}
	if !c.configured.Load() {
		if err := c.EnsureConfigured(ctx); err != nil {
			return err
		}
	}

	callCtx, cancel := context.WithTimeout(ctx, maxDuration(c.timeout*2, httpWriteTimeout))
	task, err := c.svc.Index(c.indexUID).DeleteDocuments(callCtx, productIDs, nil)
	cancel()
	if err != nil {
		c.markUnconfiguredOnError(err)
		return fmt.Errorf("meilisearch delete documents: %w", err)
	}
	if wait && task != nil {
		waitTimeout := taskWaitTimeoutFromContext(ctx, EventTaskWaitTimeout)
		if waitErr := c.waitTask(ctx, task.TaskUID, waitTimeout); waitErr != nil {
			return waitErr
		}
	}
	return nil
}

func (c *Client) waitTask(ctx context.Context, taskUID int64, maxWait time.Duration) error {
	if maxWait <= 0 {
		maxWait = EventTaskWaitTimeout
	}
	waitCtx, cancel := context.WithTimeout(ctx, maxWait)
	defer cancel()

	task, err := c.svc.WaitForTask(waitCtx, taskUID, defaultTaskPollInterval)
	if err != nil {
		return fmt.Errorf("wait meilisearch task %d: %w", taskUID, err)
	}
	if task == nil {
		return fmt.Errorf("meilisearch task %d returned empty status", taskUID)
	}
	switch task.Status {
	case meilisearch.TaskStatusSucceeded:
		return nil
	case meilisearch.TaskStatusFailed:
		if task.Error.Code == "index_already_exists" {
			return nil
		}
		return fmt.Errorf("meilisearch task %d failed: %s (%s)", taskUID, task.Error.Code, task.Error.Message)
	default:
		return fmt.Errorf("meilisearch task %d did not succeed (status=%s)", taskUID, task.Status)
	}
}

func (c *Client) resetConfigured() {
	c.cfgMu.Lock()
	c.configured.Store(false)
	c.hasPendingSettingsTask = false
	c.pendingSettingsTaskUID = 0
	c.cfgMu.Unlock()
}

func (c *Client) markUnconfiguredOnError(err error) {
	if err == nil {
		return
	}
	var meiliErr *meilisearch.Error
	if errors.As(err, &meiliErr) {
		switch {
		case meiliErr.APIError.Code == "index_not_found":
			c.resetConfigured()
		case meiliErr.StatusCode == http.StatusBadRequest && isConfigSearchErrorCode(meiliErr.APIError.Code):
			// K3: Filter/sort/facets rejection indicates index settings are missing or drifted.
			c.resetConfigured()
		case meiliErr.StatusCode == 0 || meiliErr.StatusCode >= 500:
			c.resetConfigured()
		}
		return
	}
	if !errors.Is(err, context.Canceled) {
		c.resetConfigured()
	}
}

func isConfigSearchErrorCode(code meilisearch.APIErrCode) bool {
	switch code {
	case "invalid_search_filter", "invalid_search_sort", "invalid_search_facets":
		return true
	default:
		return false
	}
}

func isIndexAlreadyExistsErr(err error) bool {
	if err == nil {
		return false
	}
	var meiliErr *meilisearch.Error
	if errors.As(err, &meiliErr) && meiliErr.APIError.Code == "index_already_exists" {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "index_already_exists")
}
