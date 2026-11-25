package retriever

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	searchv1 "github.com/nexusmart/core/gen/go/nexusmart/search/v1"
	"github.com/nexusmart/core/services/assistant/internal/guardrail"
	"github.com/nexusmart/core/services/assistant/internal/resilience"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"google.golang.org/grpc"
)

// ChainedConfig configures the multi-tier product retrieval chain.
type ChainedConfig struct {
	SearchGRPCAddr  string
	ProductGRPCAddr string
	GatewayURL      string
	ServiceName     string
	Timeout         time.Duration
	Guardrail       guardrail.Engine
	Logger          *slog.Logger
}

// ChainedRetriever queries Search gRPC first, transparently falling back to Gateway REST search
// and then Product gRPC while strictly enforcing shop_id isolation and untrusted-data sanitization.
type ChainedRetriever struct {
	searchConn    *grpc.ClientConn
	searchClient  searchv1.SearchServiceClient
	productConn   *grpc.ClientConn
	productClient productv1.ProductServiceClient

	gatewayURL string
	httpClient *http.Client

	searchCB  *resilience.CircuitBreaker
	gatewayCB *resilience.CircuitBreaker
	productCB *resilience.CircuitBreaker

	timeout   time.Duration
	guardrail guardrail.Engine
	log       *slog.Logger
}

// NewChainedRetriever constructs a ChainedRetriever with lazy gRPC channels and validated Gateway URL.
func NewChainedRetriever(cfg ChainedConfig) (*ChainedRetriever, error) {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}

	var parsedGateway string
	if rawGW := strings.TrimSpace(cfg.GatewayURL); rawGW != "" {
		u, err := url.Parse(rawGW)
		if err != nil {
			return nil, fmt.Errorf("invalid gateway URL: %w", err)
		}
		if (u.Scheme != "http" && u.Scheme != "https") || strings.TrimSpace(u.Host) == "" {
			return nil, fmt.Errorf("invalid gateway URL %q: must use http/https with host", rawGW)
		}
		parsedGateway = strings.TrimRight(rawGW, "/")
	}

	r := &ChainedRetriever{
		gatewayURL: parsedGateway,
		httpClient: &http.Client{
			Timeout: timeout + time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		searchCB:  resilience.NewCircuitBreaker(5, 15*time.Second),
		gatewayCB: resilience.NewCircuitBreaker(5, 15*time.Second),
		productCB: resilience.NewCircuitBreaker(5, 15*time.Second),
		timeout:   timeout,
		guardrail: cfg.Guardrail,
		log:       log,
	}

	if addr := strings.TrimSpace(cfg.SearchGRPCAddr); addr != "" {
		conn, err := dialGRPC(addr, cfg.ServiceName)
		if err != nil {
			return nil, fmt.Errorf("dial search grpc %q: %w", addr, err)
		}
		r.searchConn = conn
		r.searchClient = searchv1.NewSearchServiceClient(conn)
	}

	if addr := strings.TrimSpace(cfg.ProductGRPCAddr); addr != "" {
		conn, err := dialGRPC(addr, cfg.ServiceName)
		if err != nil {
			_ = r.Close()
			return nil, fmt.Errorf("dial product grpc %q: %w", addr, err)
		}
		r.productConn = conn
		r.productClient = productv1.NewProductServiceClient(conn)
	}

	return r, nil
}

// Close closes any underlying gRPC client connections.
func (r *ChainedRetriever) Close() error {
	var errs []error
	if r.searchConn != nil {
		if err := r.searchConn.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if r.productConn != nil {
		if err := r.productConn.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

type searchResult struct {
	hits  []ProductHit
	total int64
}

// Search retrieves products through Search gRPC -> Gateway REST -> Product gRPC,
// enforcing strict tenant isolation and indirect prompt-injection sanitization on every hit.
func (r *ChainedRetriever) Search(ctx context.Context, q SearchQuery) ([]ProductHit, int64, error) {
	if q.Page <= 0 {
		q.Page = 1
	}
	if q.PageSize <= 0 {
		q.PageSize = 10
	}
	q.ShopID = strings.TrimSpace(q.ShopID)

	var lastErr error

	// Tier 1: Search gRPC primary.
	if r.searchClient != nil {
		res, err := resilience.CallWithRetry(ctx, r.searchCB, 2, 50*time.Millisecond, r.timeout, resilience.IsTransientGRPCError,
			func(attemptCtx context.Context) (searchResult, error) {
				hits, total, callErr := r.searchViaSearchGRPC(attemptCtx, q)
				return searchResult{hits: hits, total: total}, callErr
			})
		if err == nil {
			filtered, adjTotal := r.isolateAndSanitize(res.hits, res.total, q.ShopID)
			return filtered, adjTotal, nil
		}
		lastErr = err
		r.log.Warn("search gRPC unavailable, falling back", slog.String("error", err.Error()))
	}

	// Tier 2: Gateway REST search fallback.
	if r.gatewayURL != "" {
		res, err := resilience.CallWithRetry(ctx, r.gatewayCB, 2, 50*time.Millisecond, r.timeout, resilience.IsTransientHTTPError,
			func(attemptCtx context.Context) (searchResult, error) {
				hits, total, callErr := r.searchViaGatewayREST(attemptCtx, q)
				return searchResult{hits: hits, total: total}, callErr
			})
		if err == nil {
			filtered, adjTotal := r.isolateAndSanitize(res.hits, res.total, q.ShopID)
			return filtered, adjTotal, nil
		}
		lastErr = err
		r.log.Warn("gateway search fallback unavailable, trying product gRPC", slog.String("error", err.Error()))
	}

	// Tier 3: Product gRPC fallback.
	if r.productClient != nil {
		res, err := resilience.CallWithRetry(ctx, r.productCB, 2, 50*time.Millisecond, r.timeout, resilience.IsTransientGRPCError,
			func(attemptCtx context.Context) (searchResult, error) {
				hits, total, callErr := r.searchViaProductGRPC(attemptCtx, q)
				return searchResult{hits: hits, total: total}, callErr
			})
		if err == nil {
			filtered, adjTotal := r.isolateAndSanitize(res.hits, res.total, q.ShopID)
			return filtered, adjTotal, nil
		}
		lastErr = err
	}

	if lastErr == nil {
		return nil, 0, errors.New("no product retrieval backend configured")
	}
	return nil, 0, fmt.Errorf("all product retrieval backends failed: %w", lastErr)
}

// SimilarProducts returns similar products, always enforcing shopID isolation and sanitization.
func (r *ChainedRetriever) SimilarProducts(ctx context.Context, productID, shopID string, limit int32) ([]ProductHit, error) {
	shopID = strings.TrimSpace(shopID)
	if limit <= 0 {
		limit = 6
	}

	var lastErr error
	if r.searchClient != nil {
		hits, err := resilience.CallWithRetry(ctx, r.searchCB, 2, 50*time.Millisecond, r.timeout, resilience.IsTransientGRPCError,
			func(attemptCtx context.Context) ([]ProductHit, error) {
				return r.similarViaSearchGRPC(attemptCtx, productID, shopID, limit)
			})
		if err == nil {
			filtered, _ := r.isolateAndSanitize(hits, int64(len(hits)), shopID)
			return filtered, nil
		}
		lastErr = err
	}

	// Fallback: fetch seed product and search by its name within the same shop scope.
	seed, err := r.GetProduct(ctx, productID)
	if err == nil && seed != nil {
		hits, _, searchErr := r.Search(ctx, SearchQuery{
			Query:    seed.Name,
			ShopID:   shopID,
			Page:     1,
			PageSize: limit + 1,
		})
		if searchErr == nil {
			out := make([]ProductHit, 0, len(hits))
			for _, h := range hits {
				if h.ID == productID {
					continue
				}
				out = append(out, h)
				if int32(len(out)) >= limit {
					break
				}
			}
			return out, nil
		}
		lastErr = searchErr
	}

	if lastErr != nil {
		return nil, fmt.Errorf("similar products retrieval failed: %w", lastErr)
	}
	return []ProductHit{}, nil
}

// GetProduct retrieves a single product via Product gRPC or Gateway REST fallback and sanitizes it.
func (r *ChainedRetriever) GetProduct(ctx context.Context, productID string) (*ProductHit, error) {
	productID = strings.TrimSpace(productID)
	if productID == "" {
		return nil, errors.New("productID is required")
	}

	var lastErr error
	if r.productClient != nil {
		hit, err := resilience.CallWithRetry(ctx, r.productCB, 2, 50*time.Millisecond, r.timeout, resilience.IsTransientGRPCError,
			func(attemptCtx context.Context) (*ProductHit, error) {
				return r.getProductViaProductGRPC(attemptCtx, productID)
			})
		if err == nil && hit != nil {
			sanitized := sanitizeHit(*hit, r.guardrail)
			return &sanitized, nil
		}
		lastErr = err
	}

	if r.gatewayURL != "" {
		hit, err := resilience.CallWithRetry(ctx, r.gatewayCB, 2, 50*time.Millisecond, r.timeout, resilience.IsTransientHTTPError,
			func(attemptCtx context.Context) (*ProductHit, error) {
				return r.getProductViaGatewayREST(attemptCtx, productID)
			})
		if err == nil && hit != nil {
			sanitized := sanitizeHit(*hit, r.guardrail)
			return &sanitized, nil
		}
		lastErr = err
	}

	if lastErr == nil {
		return nil, errors.New("no product backend configured")
	}
	return nil, fmt.Errorf("get product %q failed: %w", productID, lastErr)
}

func (r *ChainedRetriever) searchViaGatewayREST(ctx context.Context, q SearchQuery) ([]ProductHit, int64, error) {
	params := url.Values{}
	if q.Query != "" {
		params.Set("q", q.Query)
	}
	if q.ShopID != "" {
		params.Set("shop_id", q.ShopID)
	}
	if q.Category != "" {
		if looksLikeUUID(q.Category) {
			params.Set("category_id", q.Category)
		} else {
			params.Set("category_slug", q.Category)
		}
	}
	if q.MinPriceVND > 0 {
		params.Set("min_price_vnd", strconv.FormatInt(q.MinPriceVND, 10))
	}
	if q.MaxPriceVND > 0 {
		params.Set("max_price_vnd", strconv.FormatInt(q.MaxPriceVND, 10))
	}
	if q.Sort != "" {
		params.Set("sort_by", normalizeSearchSortBy(q.Sort))
	}
	params.Set("page", strconv.Itoa(int(q.Page)))
	params.Set("page_size", strconv.Itoa(int(q.PageSize)))

	endpoint := r.gatewayURL + "/api/v1/search?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, 0, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, 0, &resilience.HTTPStatusError{StatusCode: resp.StatusCode, Body: string(body)}
	}

	var payload struct {
		Items []gatewayProductItem `json:"items"`
		Total int64                `json:"total"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, 0, fmt.Errorf("decode gateway search response: %w", err)
	}

	hits := make([]ProductHit, 0, len(payload.Items))
	for _, it := range payload.Items {
		hits = append(hits, it.toProductHit())
	}
	return hits, payload.Total, nil
}

func (r *ChainedRetriever) getProductViaGatewayREST(ctx context.Context, productID string) (*ProductHit, error) {
	endpoint := r.gatewayURL + "/api/v1/products/" + url.PathEscape(productID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrProductNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &resilience.HTTPStatusError{StatusCode: resp.StatusCode, Body: string(body)}
	}

	var wrapped struct {
		Product *gatewayProductItem `json:"product"`
	}
	if err := json.Unmarshal(body, &wrapped); err != nil {
		return nil, fmt.Errorf("decode gateway product response: %w", err)
	}
	if wrapped.Product != nil {
		hit := wrapped.Product.toProductHit()
		return &hit, nil
	}

	var direct gatewayProductItem
	if err := json.Unmarshal(body, &direct); err != nil || direct.ID == "" {
		return nil, ErrProductNotFound
	}
	hit := direct.toProductHit()
	return &hit, nil
}

type gatewayProductItem struct {
	ID                 string   `json:"id"`
	ShopID             string   `json:"shop_id"`
	ShopName           string   `json:"shop_name"`
	CategoryID         string   `json:"category_id"`
	Category           string   `json:"category"`
	CategoryName       string   `json:"category_name"`
	CategorySlug       string   `json:"category_slug"`
	Name               string   `json:"name"`
	Description        string   `json:"description"`
	DescriptionSnippet string   `json:"description_snippet"`
	MinPriceVND        int64    `json:"min_price_vnd"`
	MaxPriceVND        int64    `json:"max_price_vnd"`
	ImageURL           string   `json:"image_url"`
	ThumbnailURL       string   `json:"thumbnail_url"`
	Images             []string `json:"images"`
	InStock            *bool    `json:"in_stock"`
	Score              float64  `json:"score"`
	SKUs               []SKUHit `json:"skus"`
}

func (g gatewayProductItem) toProductHit() ProductHit {
	img := strings.TrimSpace(g.ImageURL)
	if img == "" {
		img = strings.TrimSpace(g.ThumbnailURL)
	}
	if img == "" && len(g.Images) > 0 {
		img = g.Images[0]
	}
	desc := strings.TrimSpace(g.Description)
	if desc == "" {
		desc = strings.TrimSpace(g.DescriptionSnippet)
	}
	cat := strings.TrimSpace(g.Category)
	if cat == "" {
		cat = strings.TrimSpace(g.CategoryName)
	}
	if cat == "" {
		cat = strings.TrimSpace(g.CategorySlug)
	}
	inStock := true
	if g.InStock != nil {
		inStock = *g.InStock
	} else if len(g.SKUs) > 0 {
		inStock = false
		for _, s := range g.SKUs {
			if s.AvailableStock > 0 {
				inStock = true
				break
			}
		}
	}
	return ProductHit{
		ID:          g.ID,
		ShopID:      g.ShopID,
		ShopName:    g.ShopName,
		CategoryID:  g.CategoryID,
		Category:    cat,
		Name:        g.Name,
		Description: desc,
		MinPriceVND: g.MinPriceVND,
		MaxPriceVND: g.MaxPriceVND,
		ImageURL:    img,
		InStock:     inStock,
		Score:       g.Score,
		SKUs:        g.SKUs,
	}
}

func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return false
			}
		}
	}
	return true
}

// normalizeSearchSortBy maps LLM/tool sort arguments onto the exact sort_by values
// accepted by SearchService.SearchProducts, defaulting unknown values to "relevance".
func normalizeSearchSortBy(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "price_asc", "price-asc":
		return "price_asc"
	case "price_desc", "price-desc":
		return "price_desc"
	case "newest":
		return "newest"
	case "rating_desc", "rating-desc":
		return "rating_desc"
	default:
		return "relevance"
	}
}

func (r *ChainedRetriever) isolateAndSanitize(hits []ProductHit, total int64, requiredShopID string) ([]ProductHit, int64) {
	requiredShopID = strings.TrimSpace(requiredShopID)
	out := make([]ProductHit, 0, len(hits))
	dropped := 0
	for _, hit := range hits {
		if requiredShopID != "" && hit.ShopID != requiredShopID {
			dropped++
			continue
		}
		out = append(out, sanitizeHit(hit, r.guardrail))
	}
	if dropped > 0 {
		r.log.Warn("dropped cross-shop product hits during tenant isolation",
			slog.String("required_shop_id", requiredShopID),
			slog.Int("dropped_count", dropped),
		)
		if total > int64(len(out)) {
			total = int64(len(out))
		}
	}
	return out, total
}

func sanitizeHit(hit ProductHit, g guardrail.Engine) ProductHit {
	if g == nil || hit.Description == "" {
		return hit
	}
	res := g.SanitizeUntrustedData("product_description", hit.Description)
	if res.InjectionFound || res.SanitizedText != "" {
		hit.Description = res.SanitizedText
	}
	return hit
}
