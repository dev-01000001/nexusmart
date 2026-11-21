package loader

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/tools/catalog-gen/internal/generator"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

const (
	defaultGRPCTimeout        = 15 * time.Second
	defaultGRPCRetryBaseDelay = 50 * time.Millisecond
	defaultGRPCMaxRetries     = 5
)

// GRPCClient ingests products directly via ProductService gRPC API with valid Bearer JWTs.
type GRPCClient struct {
	conn           *grpc.ClientConn
	client         productv1.ProductServiceClient
	adminToken     string
	adminUserID    string
	shopTokens     map[string]string
	callTimeout    time.Duration
	retryBaseDelay time.Duration
	maxRetries     int
}

// NewGRPCClient dials ProductService at addr and initializes a GRPCClient.
func NewGRPCClient(
	addr string,
	adminToken string,
	adminUserID string,
	shopTokens map[string]string,
) (*GRPCClient, error) {
	conn, err := grpc.NewClient(
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("dial product grpc (%s): %w", addr, err)
	}
	c := NewGRPCClientWithServiceClient(productv1.NewProductServiceClient(conn), adminToken, adminUserID, shopTokens)
	c.conn = conn
	return c, nil
}

// NewGRPCClientWithServiceClient wraps an existing ProductServiceClient (useful for tests/bufconn).
func NewGRPCClientWithServiceClient(
	svc productv1.ProductServiceClient,
	adminToken string,
	adminUserID string,
	shopTokens map[string]string,
) *GRPCClient {
	tokensCopy := make(map[string]string, len(shopTokens))
	for k, v := range shopTokens {
		tokensCopy[k] = v
	}
	if adminUserID == "" {
		adminUserID = "catalog-gen-admin"
	}
	return &GRPCClient{
		client:         svc,
		adminToken:     adminToken,
		adminUserID:    adminUserID,
		shopTokens:     tokensCopy,
		callTimeout:    defaultGRPCTimeout,
		retryBaseDelay: defaultGRPCRetryBaseDelay,
		maxRetries:     defaultGRPCMaxRetries,
	}
}

// SetRetryConfig customizes retry base delay and max retries.
func (c *GRPCClient) SetRetryConfig(baseDelay time.Duration, maxRetries int) {
	if baseDelay > 0 {
		c.retryBaseDelay = baseDelay
	}
	if maxRetries >= 0 {
		c.maxRetries = maxRetries
	}
}

// Close closes the underlying gRPC connection if owned by GRPCClient.
func (c *GRPCClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// MintIsolatedTokens mints valid end-user HS256 JWTs for isolated gRPC benchmarks where
// UserService/Gateway is not running.
func MintIsolatedTokens(jwtSecret string, shopIDs []string) (string, string, []generator.ShopTarget, map[string]string, error) {
	mgr, err := auth.NewManager(jwtSecret, 2*time.Hour)
	if err != nil {
		return "", "", nil, nil, fmt.Errorf("init jwt manager: %w", err)
	}

	adminUserID := "catalog-gen-admin"
	adminTok, _, _, err := mgr.SignAccessToken(adminUserID, "admin", "")
	if err != nil {
		return "", "", nil, nil, fmt.Errorf("sign admin jwt: %w", err)
	}

	shops := make([]generator.ShopTarget, 0, len(shopIDs))
	shopTokens := make(map[string]string, len(shopIDs))
	for _, raw := range shopIDs {
		shopID := strings.TrimSpace(raw)
		if shopID == "" {
			continue
		}
		if _, dup := shopTokens[shopID]; dup {
			continue
		}
		tok, _, _, signErr := mgr.SignAccessToken("catalog-gen-seller-"+shopID, "seller", shopID)
		if signErr != nil {
			return "", "", nil, nil, fmt.Errorf("sign seller jwt for %s: %w", shopID, signErr)
		}
		shops = append(shops, generator.ShopTarget{
			ID:   shopID,
			Slug: shopID,
			Name: shopID,
		})
		shopTokens[shopID] = tok
	}
	if len(shops) == 0 {
		return "", "", nil, nil, errors.New("at least one non-empty shop ID is required")
	}
	return adminTok, adminUserID, shops, shopTokens, nil
}

// ListCategories discovers categories via ProductService.ListCategories.
// If autoSeedDefaults is true and 0 categories exist (e.g., fresh isolated container benchmark),
// it creates the 6 default categories via ProductService.CreateCategory using the admin token.
func (c *GRPCClient) ListCategories(ctx context.Context, autoSeedDefaults bool) ([]generator.CategoryTarget, error) {
	callCtx, cancel := context.WithTimeout(ctx, c.callTimeout)
	defer cancel()

	resp, err := c.client.ListCategories(callCtx, &productv1.ListCategoriesRequest{})
	if err != nil {
		return nil, fmt.Errorf("grpc ListCategories: %w", err)
	}
	cats := make([]generator.CategoryTarget, 0, len(resp.GetCategories()))
	for _, cat := range resp.GetCategories() {
		if cat.GetId() == "" {
			continue
		}
		cats = append(cats, generator.CategoryTarget{
			ID:   cat.GetId(),
			Slug: cat.GetSlug(),
			Name: cat.GetName(),
		})
	}
	if len(cats) > 0 || !autoSeedDefaults || c.adminToken == "" {
		return cats, nil
	}

	defaults := generator.DefaultCategories()
	seeded := make([]generator.CategoryTarget, 0, len(defaults))
	for i, d := range defaults {
		cCtx, cCancel := context.WithTimeout(ctx, c.callTimeout)
		cCtx = auth.InjectOutgoingGRPC(cCtx, c.adminToken)
		created, createErr := c.client.CreateCategory(cCtx, &productv1.CreateCategoryRequest{
			Name:              d.Name,
			Slug:              d.Slug,
			CommissionRateBps: 500,
			SortOrder:         int32(i + 1),
		})
		cCancel()
		if createErr != nil {
			return nil, fmt.Errorf("auto-create category %s: %w", d.Slug, createErr)
		}
		cat := created.GetCategory()
		seeded = append(seeded, generator.CategoryTarget{
			ID:   cat.GetId(),
			Slug: cat.GetSlug(),
			Name: cat.GetName(),
		})
	}
	return seeded, nil
}

// CreateProduct calls ProductService.CreateProduct with the shop's seller Bearer JWT.
func (c *GRPCClient) CreateProduct(ctx context.Context, spec generator.ProductSpec) (string, error) {
	token := c.shopTokens[spec.ShopID]
	if token == "" {
		return "", fmt.Errorf("no seller token for shop %s", spec.ShopID)
	}

	protoSKUs := make([]*productv1.SKUInput, 0, len(spec.SKUs))
	for _, sk := range spec.SKUs {
		protoSKUs = append(protoSKUs, &productv1.SKUInput{
			SkuCode:     sk.SKUCode,
			Name:        sk.Name,
			Attributes:  sk.Attributes,
			PriceVnd:    sk.PriceVND,
			Stock:       sk.Stock,
			WeightGrams: sk.WeightGrams,
			ImageUrl:    sk.ImageURL,
		})
	}

	req := &productv1.CreateProductRequest{
		ShopId:      spec.ShopID,
		CategoryId:  spec.CategoryID,
		Name:        spec.Name,
		Description: spec.Description,
		Images:      spec.Images,
		Skus:        protoSKUs,
	}

	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		callCtx, cancel := context.WithTimeout(ctx, c.callTimeout)
		callCtx = auth.InjectOutgoingGRPC(callCtx, token)
		resp, err := c.client.CreateProduct(callCtx, req)
		cancel()
		if err == nil {
			prodID := resp.GetProduct().GetId()
			if prodID == "" {
				return "", errors.New("grpc CreateProduct returned empty product id")
			}
			return prodID, nil
		}
		lastErr = err
		if !isRetryableGRPCError(err) || attempt == c.maxRetries {
			return "", err
		}
		if sleepErr := sleepContext(ctx, c.retryBaseDelay<<min(attempt, 5)); sleepErr != nil {
			return "", sleepErr
		}
	}
	return "", lastErr
}

// ApproveProduct calls ProductService.AdminReviewProduct with the admin Bearer JWT.
func (c *GRPCClient) ApproveProduct(ctx context.Context, productID string) error {
	if c.adminToken == "" {
		return errors.New("no admin token available for grpc approval")
	}

	req := &productv1.AdminReviewProductRequest{
		AdminId:   c.adminUserID,
		ProductId: productID,
		Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
		Reason:    "catalog-gen auto-approve",
	}

	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		callCtx, cancel := context.WithTimeout(ctx, c.callTimeout)
		callCtx = auth.InjectOutgoingGRPC(callCtx, c.adminToken)
		_, err := c.client.AdminReviewProduct(callCtx, req)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		if !isRetryableGRPCError(err) || attempt == c.maxRetries {
			return err
		}
		if sleepErr := sleepContext(ctx, c.retryBaseDelay<<min(attempt, 5)); sleepErr != nil {
			return sleepErr
		}
	}
	return lastErr
}

func isRetryableGRPCError(err error) bool {
	st, ok := status.FromError(err)
	if !ok {
		return false
	}
	switch st.Code() {
	case codes.Unavailable, codes.ResourceExhausted, codes.DeadlineExceeded, codes.Aborted:
		return true
	default:
		return false
	}
}
