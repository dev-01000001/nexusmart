package loader

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nexusmart/core/tools/catalog-gen/internal/generator"
)

const (
	defaultHTTPTimeout    = 30 * time.Second
	defaultRetryBaseDelay = 200 * time.Millisecond
	defaultMaxRetries     = 6
)

// RESTClient interacts with NexusMart strictly through the Gateway REST API.
type RESTClient struct {
	baseURL        string
	httpClient     *http.Client
	retryBaseDelay time.Duration
	maxRetries     int

	mu          sync.RWMutex
	adminCreds  AdminCredentials
	adminToken  string
	adminUserID string
	sellerCreds map[string]SellerAccount // shopID -> credentials
	shopTokens  map[string]string        // shopID -> access token
}

// RESTDiscovery contains tokens and catalog metadata discovered via Gateway REST API.
type RESTDiscovery struct {
	AdminToken  string
	AdminUserID string
	Shops       []generator.ShopTarget
	ShopTokens  map[string]string
	Categories  []generator.CategoryTarget
	Skipped     []string
}

// NewRESTClient creates a pooled HTTP client targeting the Gateway base URL.
func NewRESTClient(gatewayURL string, concurrency int) *RESTClient {
	pool := concurrency * 2
	if pool < 32 {
		pool = 32
	}
	transport := &http.Transport{
		MaxIdleConns:        pool * 2,
		MaxIdleConnsPerHost: pool,
		IdleConnTimeout:     90 * time.Second,
	}
	return &RESTClient{
		baseURL: strings.TrimRight(strings.TrimSpace(gatewayURL), "/"),
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   defaultHTTPTimeout,
		},
		retryBaseDelay: defaultRetryBaseDelay,
		maxRetries:     defaultMaxRetries,
		sellerCreds:    make(map[string]SellerAccount),
		shopTokens:     make(map[string]string),
	}
}

// SetRetryConfig customizes retry base delay and max retries (useful for tests).
func (c *RESTClient) SetRetryConfig(baseDelay time.Duration, maxRetries int) {
	if baseDelay > 0 {
		c.retryBaseDelay = baseDelay
	}
	if maxRetries >= 0 {
		c.maxRetries = maxRetries
	}
}

type loginResponse struct {
	AccessToken string `json:"access_token"`
	User        struct {
		ID     string `json:"id"`
		ShopID string `json:"shop_id"`
		Role   string `json:"role"`
	} `json:"user"`
}

// Login authenticates an account via POST /api/v1/auth/login.
func (c *RESTClient) Login(ctx context.Context, email, password string) (string, string, string, error) {
	payload := map[string]string{
		"email":    email,
		"password": password,
	}
	var resp loginResponse
	statusCode, raw, err := c.doJSON(ctx, http.MethodPost, "/api/v1/auth/login", "", payload, &resp)
	if err != nil {
		return "", "", "", err
	}
	if statusCode != http.StatusOK {
		return "", "", "", fmt.Errorf("login %s failed: HTTP %d: %s", email, statusCode, truncateBody(raw))
	}
	if resp.AccessToken == "" {
		return "", "", "", fmt.Errorf("login %s returned empty access_token", email)
	}
	return resp.AccessToken, resp.User.ID, resp.User.ShopID, nil
}

// GetSellerShop fetches the authenticated seller's shop via GET /api/v1/seller/shop.
func (c *RESTClient) GetSellerShop(ctx context.Context, token, fallbackSlug string) (generator.ShopTarget, error) {
	var resp struct {
		Shop struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"shop"`
	}
	statusCode, raw, err := c.doJSON(ctx, http.MethodGet, "/api/v1/seller/shop", token, nil, &resp)
	if err != nil {
		return generator.ShopTarget{}, err
	}
	if statusCode != http.StatusOK {
		return generator.ShopTarget{}, fmt.Errorf("GET /api/v1/seller/shop failed: HTTP %d: %s", statusCode, truncateBody(raw))
	}
	if resp.Shop.ID == "" {
		return generator.ShopTarget{}, errors.New("GET /api/v1/seller/shop returned empty shop id")
	}
	slug := fallbackSlug
	if slug == "" {
		slug = resp.Shop.ID
	}
	return generator.ShopTarget{
		ID:   resp.Shop.ID,
		Slug: slug,
		Name: resp.Shop.Name,
	}, nil
}

// ListCategories discovers all categories via GET /api/v1/categories.
func (c *RESTClient) ListCategories(ctx context.Context) ([]generator.CategoryTarget, error) {
	var resp struct {
		Categories []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Slug string `json:"slug"`
		} `json:"categories"`
	}
	statusCode, raw, err := c.doJSON(ctx, http.MethodGet, "/api/v1/categories", "", nil, &resp)
	if err != nil {
		return nil, err
	}
	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("GET /api/v1/categories failed: HTTP %d: %s", statusCode, truncateBody(raw))
	}
	out := make([]generator.CategoryTarget, 0, len(resp.Categories))
	for _, cat := range resp.Categories {
		if cat.ID == "" {
			continue
		}
		out = append(out, generator.CategoryTarget{
			ID:   cat.ID,
			Slug: cat.Slug,
			Name: cat.Name,
		})
	}
	return out, nil
}

// Discover logs in admin (if needAdmin) and available seeded sellers, discovers shop IDs via
// GET /api/v1/seller/shop, and optionally discovers categories via GET /api/v1/categories.
func (c *RESTClient) Discover(
	ctx context.Context,
	adminCreds AdminCredentials,
	sellers []SellerAccount,
	needAdmin bool,
	discoverCategories bool,
) (*RESTDiscovery, error) {
	disc := &RESTDiscovery{
		ShopTokens: make(map[string]string),
	}

	if needAdmin {
		tok, uid, _, err := c.Login(ctx, adminCreds.Email, adminCreds.Password)
		if err != nil {
			return nil, fmt.Errorf("admin login: %w", err)
		}
		disc.AdminToken = tok
		disc.AdminUserID = uid
		c.mu.Lock()
		c.adminCreds = adminCreds
		c.adminToken = tok
		c.adminUserID = uid
		c.mu.Unlock()
	}

	seenShop := make(map[string]bool)
	for _, s := range sellers {
		if strings.TrimSpace(s.Email) == "" {
			continue
		}
		tok, _, _, err := c.Login(ctx, s.Email, s.Password)
		if err != nil {
			disc.Skipped = append(disc.Skipped, fmt.Sprintf("%s (%v)", s.Email, err))
			continue
		}
		shop, err := c.GetSellerShop(ctx, tok, s.Slug)
		if err != nil {
			disc.Skipped = append(disc.Skipped, fmt.Sprintf("%s shop discovery (%v)", s.Email, err))
			continue
		}
		if seenShop[shop.ID] {
			continue
		}
		seenShop[shop.ID] = true
		disc.Shops = append(disc.Shops, shop)
		disc.ShopTokens[shop.ID] = tok

		c.mu.Lock()
		c.shopTokens[shop.ID] = tok
		c.sellerCreds[shop.ID] = s
		c.mu.Unlock()
	}
	if len(disc.Shops) == 0 {
		return nil, fmt.Errorf("no seller shops available via %s (skipped: %s)", c.baseURL, strings.Join(disc.Skipped, "; "))
	}

	if discoverCategories {
		cats, err := c.ListCategories(ctx)
		if err != nil {
			return nil, fmt.Errorf("list categories: %w", err)
		}
		if len(cats) == 0 {
			return nil, errors.New("GET /api/v1/categories returned 0 categories; seed categories first")
		}
		disc.Categories = cats
	}

	return disc, nil
}

// CreateProduct submits a generated product via POST /api/v1/seller/products.
func (c *RESTClient) CreateProduct(ctx context.Context, spec generator.ProductSpec) (string, error) {
	token := c.getShopToken(spec.ShopID)
	if token == "" {
		return "", fmt.Errorf("no seller token available for shop %s", spec.ShopID)
	}

	body := map[string]any{
		"category_id": spec.CategoryID,
		"name":        spec.Name,
		"description": spec.Description,
		"images":      spec.Images,
		"skus":        spec.SKUs,
	}

	var resp struct {
		Product struct {
			ID string `json:"id"`
		} `json:"product"`
	}

	statusCode, raw, err := c.doJSON(ctx, http.MethodPost, "/api/v1/seller/products", token, body, &resp)
	if err == nil && statusCode == http.StatusUnauthorized {
		if refreshed, refErr := c.refreshShopToken(ctx, spec.ShopID); refErr == nil && refreshed != "" {
			statusCode, raw, err = c.doJSON(ctx, http.MethodPost, "/api/v1/seller/products", refreshed, body, &resp)
		}
	}
	if err != nil {
		return "", err
	}
	if statusCode != http.StatusCreated && statusCode != http.StatusOK {
		return "", fmt.Errorf("POST /api/v1/seller/products HTTP %d: %s", statusCode, truncateBody(raw))
	}
	if resp.Product.ID == "" {
		return "", errors.New("POST /api/v1/seller/products returned empty product id")
	}
	return resp.Product.ID, nil
}

// ApproveProduct approves a product via POST /api/v1/admin/products/{id}/review.
func (c *RESTClient) ApproveProduct(ctx context.Context, productID string) error {
	token := c.getAdminToken()
	if token == "" {
		return errors.New("no admin token available for product approval")
	}
	path := "/api/v1/admin/products/" + url.PathEscape(productID) + "/review"
	body := map[string]string{
		"decision": "APPROVE",
		"reason":   "catalog-gen auto-approve",
	}

	statusCode, raw, err := c.doJSON(ctx, http.MethodPost, path, token, body, nil)
	if err == nil && statusCode == http.StatusUnauthorized {
		if refreshed, refErr := c.refreshAdminToken(ctx); refErr == nil && refreshed != "" {
			statusCode, raw, err = c.doJSON(ctx, http.MethodPost, path, refreshed, body, nil)
		}
	}
	if err != nil {
		return err
	}
	if statusCode != http.StatusOK && statusCode != http.StatusCreated {
		return fmt.Errorf("POST %s HTTP %d: %s", path, statusCode, truncateBody(raw))
	}
	return nil
}

func (c *RESTClient) getShopToken(shopID string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.shopTokens[shopID]
}

func (c *RESTClient) getAdminToken() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.adminToken
}

func (c *RESTClient) refreshShopToken(ctx context.Context, shopID string) (string, error) {
	c.mu.RLock()
	creds, ok := c.sellerCreds[shopID]
	c.mu.RUnlock()
	if !ok || creds.Email == "" {
		return "", errors.New("missing seller credentials for token refresh")
	}
	tok, _, _, err := c.Login(ctx, creds.Email, creds.Password)
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	c.shopTokens[shopID] = tok
	c.mu.Unlock()
	return tok, nil
}

func (c *RESTClient) refreshAdminToken(ctx context.Context) (string, error) {
	c.mu.RLock()
	creds := c.adminCreds
	c.mu.RUnlock()
	if creds.Email == "" {
		return "", errors.New("missing admin credentials for token refresh")
	}
	tok, uid, _, err := c.Login(ctx, creds.Email, creds.Password)
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	c.adminToken = tok
	c.adminUserID = uid
	c.mu.Unlock()
	return tok, nil
}

func (c *RESTClient) doJSON(
	ctx context.Context,
	method string,
	path string,
	bearerToken string,
	reqBody any,
	respDst any,
) (int, []byte, error) {
	var encoded []byte
	if reqBody != nil {
		var err error
		encoded, err = json.Marshal(reqBody)
		if err != nil {
			return 0, nil, fmt.Errorf("marshal request body: %w", err)
		}
	}

	fullURL := c.baseURL + path
	var lastErr error

	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		statusCode, raw, retryAfter, err := c.doOnce(ctx, method, fullURL, bearerToken, encoded)
		if err != nil {
			lastErr = err
			if ctx.Err() != nil || attempt == c.maxRetries {
				return 0, nil, err
			}
			delay := c.retryBaseDelay << min(attempt, 5)
			if sleepErr := sleepContext(ctx, delay); sleepErr != nil {
				return 0, nil, sleepErr
			}
			continue
		}

		if statusCode == http.StatusTooManyRequests && attempt < c.maxRetries {
			delay := parseRetryAfter(retryAfter, c.retryBaseDelay<<min(attempt, 5))
			if sleepErr := sleepContext(ctx, delay); sleepErr != nil {
				return 0, nil, sleepErr
			}
			continue
		}

		if isRetryableHTTPStatus(statusCode) && attempt < c.maxRetries {
			delay := c.retryBaseDelay << min(attempt, 5)
			if sleepErr := sleepContext(ctx, delay); sleepErr != nil {
				return 0, nil, sleepErr
			}
			continue
		}

		if respDst != nil && len(raw) > 0 && statusCode >= 200 && statusCode < 300 {
			if err := json.Unmarshal(raw, respDst); err != nil {
				return statusCode, raw, fmt.Errorf("decode response from %s %s: %w", method, path, err)
			}
		}
		return statusCode, raw, nil
	}

	if lastErr != nil {
		return 0, nil, lastErr
	}
	return 0, nil, fmt.Errorf("%s %s: exhausted retries", method, path)
}

func (c *RESTClient) doOnce(
	ctx context.Context,
	method string,
	fullURL string,
	bearerToken string,
	encoded []byte,
) (int, []byte, string, error) {
	var bodyReader io.Reader
	if encoded != nil {
		bodyReader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, fullURL, bodyReader)
	if err != nil {
		return 0, nil, "", err
	}
	req.Header.Set("Accept", "application/json")
	if encoded != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return resp.StatusCode, nil, resp.Header.Get("Retry-After"), err
	}
	return resp.StatusCode, raw, resp.Header.Get("Retry-After"), nil
}

func isRetryableHTTPStatus(code int) bool {
	switch code {
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func parseRetryAfter(val string, fallback time.Duration) time.Duration {
	v := strings.TrimSpace(val)
	if v == "" {
		return fallback
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs <= 0 {
			return 10 * time.Millisecond
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		d := time.Until(t)
		if d < 10*time.Millisecond {
			return 10 * time.Millisecond
		}
		return d
	}
	return fallback
}

func truncateBody(raw []byte) string {
	s := strings.TrimSpace(string(raw))
	if len(s) > 300 {
		return s[:300] + "..."
	}
	return s
}
