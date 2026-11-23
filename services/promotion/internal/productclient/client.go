package productclient

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/telemetry"
)

const (
	serviceTokenName   = "promotion-service"
	defaultCallTimeout = 3 * time.Second
	defaultCacheTTL    = 60 * time.Second
	maxCacheEntries    = 1024
)

type descCacheEntry struct {
	ids       []string
	expiresAt time.Time
}

// GRPCClient wraps ProductService gRPC calls with OTel propagation, service-token auth for
// internal RPCs (GetCategoryDescendants), and a short-TTL in-memory descendant cache.
type GRPCClient struct {
	conn     *grpc.ClientConn
	client   productv1.ProductServiceClient
	svcMgr   *auth.Manager
	timeout  time.Duration
	cacheTTL time.Duration
	nowFn    func() time.Time

	mu    sync.RWMutex
	cache map[string]descCacheEntry
}

// New creates a lazy-connecting gRPC client to ProductService.
func New(addr, serviceName string, svcMgr *auth.Manager, cacheTTL time.Duration) (*GRPCClient, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil, fmt.Errorf("product grpc address is empty")
	}
	conn, err := grpc.NewClient(
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(
			telemetry.GRPCUnaryClientInterceptor(serviceName),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("dial product service %s: %w", addr, err)
	}
	c := NewWithClient(productv1.NewProductServiceClient(conn), svcMgr, cacheTTL)
	c.conn = conn
	return c, nil
}

// NewWithClient constructs a GRPCClient around an existing ProductServiceClient (useful for tests).
func NewWithClient(raw productv1.ProductServiceClient, svcMgr *auth.Manager, cacheTTL time.Duration) *GRPCClient {
	if cacheTTL <= 0 {
		cacheTTL = defaultCacheTTL
	}
	return &GRPCClient{
		client:   raw,
		svcMgr:   svcMgr,
		timeout:  defaultCallTimeout,
		cacheTTL: cacheTTL,
		nowFn:    func() time.Time { return time.Now().UTC() },
		cache:    make(map[string]descCacheEntry),
	}
}

// SetClock overrides the clock used for cache TTL checks (intended for deterministic unit tests).
func (c *GRPCClient) SetClock(fn func() time.Time) {
	if fn != nil {
		c.nowFn = fn
	}
}

// Close shuts down the underlying gRPC connection if owned by this client.
func (c *GRPCClient) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// GetSKUsBatch fetches SKUs from ProductService.
// GetSKUsBatch is a public RPC (not in internalRPCs), so NO service token is attached.
func (c *GRPCClient) GetSKUsBatch(ctx context.Context, skuIDs []string) (map[string]*productv1.SKU, error) {
	if c == nil || c.client == nil {
		return nil, fmt.Errorf("product client is not initialized")
	}
	unique := normalizeStrings(skuIDs)
	if len(unique) == 0 {
		return map[string]*productv1.SKU{}, nil
	}

	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	resp, err := c.client.GetSKUsBatch(callCtx, &productv1.GetSKUsBatchRequest{SkuIds: unique})
	if err != nil {
		return nil, err
	}
	out := make(map[string]*productv1.SKU, len(resp.GetSkus()))
	for _, sk := range resp.GetSkus() {
		if sk != nil && sk.GetId() != "" {
			out[sk.GetId()] = sk
		}
	}
	return out, nil
}

// GetCategoryDescendants resolves all recursive descendant category IDs via ProductService
// using a short-lived service token and a short-TTL cache.
func (c *GRPCClient) GetCategoryDescendants(ctx context.Context, categoryIDs []string) ([]string, error) {
	if c == nil || c.client == nil {
		return nil, fmt.Errorf("product client is not initialized")
	}
	sorted := normalizeSorted(categoryIDs)
	if len(sorted) == 0 {
		return nil, nil
	}

	cacheKey := strings.Join(sorted, ",")
	now := c.nowFn()

	c.mu.RLock()
	entry, hit := c.cache[cacheKey]
	c.mu.RUnlock()
	if hit && now.Before(entry.expiresAt) {
		return append([]string(nil), entry.ids...), nil
	}

	if c.svcMgr == nil {
		return nil, fmt.Errorf("service token manager is required for GetCategoryDescendants")
	}
	tok, err := c.svcMgr.SignServiceToken(serviceTokenName)
	if err != nil {
		return nil, fmt.Errorf("sign service token: %w", err)
	}

	callCtx, cancel := context.WithTimeout(auth.InjectOutgoingGRPC(ctx, tok), c.timeout)
	defer cancel()

	resp, err := c.client.GetCategoryDescendants(callCtx, &productv1.GetCategoryDescendantsRequest{
		CategoryIds: sorted,
	})
	if err != nil {
		return nil, err
	}

	merged := make([]string, 0, len(sorted)+len(resp.GetCategoryIds()))
	merged = append(merged, sorted...)
	merged = append(merged, resp.GetCategoryIds()...)
	result := normalizeSorted(merged)

	c.mu.Lock()
	if len(c.cache) >= maxCacheEntries {
		for k, e := range c.cache {
			if !now.Before(e.expiresAt) {
				delete(c.cache, k)
			}
		}
		if len(c.cache) >= maxCacheEntries {
			c.cache = make(map[string]descCacheEntry)
		}
	}
	c.cache[cacheKey] = descCacheEntry{
		ids:       append([]string(nil), result...),
		expiresAt: now.Add(c.cacheTTL),
	}
	c.mu.Unlock()

	return result, nil
}

func normalizeStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, raw := range in {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func normalizeSorted(in []string) []string {
	out := normalizeStrings(in)
	sort.Strings(out)
	return out
}
