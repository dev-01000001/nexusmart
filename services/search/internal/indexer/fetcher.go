package indexer

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	"github.com/nexusmart/core/services/search/internal/resilience"
)

// ProductFetcher retrieves product and category snapshots from ProductService via gRPC.
type ProductFetcher interface {
	GetProduct(ctx context.Context, productID string) (*productv1.Product, error)
	ListProducts(ctx context.Context, page, pageSize int32) ([]*productv1.Product, int64, error)
	ListCategories(ctx context.Context) ([]*productv1.Category, error)
}

// GRPCProductFetcher wraps productv1.ProductServiceClient with timeout, retry, and circuit breaker.
type GRPCProductFetcher struct {
	client productv1.ProductServiceClient
	exec   *resilience.Executor
}

// NewGRPCProductFetcher constructs a resilient ProductService gRPC client adapter.
func NewGRPCProductFetcher(client productv1.ProductServiceClient, exec *resilience.Executor, logger *slog.Logger) *GRPCProductFetcher {
	if exec == nil {
		cfg := resilience.DefaultConfig("product-grpc", logger)
		cfg.AttemptTimeout = 5 * time.Second
		cfg.MaxAttempts = 3
		exec = resilience.NewExecutor(cfg)
	}
	return &GRPCProductFetcher{
		client: client,
		exec:   exec,
	}
}

// GetProduct fetches a single public product by ID. Returns ErrProductNotFound on codes.NotFound.
func (f *GRPCProductFetcher) GetProduct(ctx context.Context, productID string) (*productv1.Product, error) {
	return resilience.Do(ctx, f.exec, "GetProduct", func(attemptCtx context.Context) (*productv1.Product, error) {
		resp, err := f.client.GetProduct(attemptCtx, &productv1.GetProductRequest{Id: productID})
		if err != nil {
			st, ok := status.FromError(err)
			if ok {
				switch st.Code() {
				case codes.NotFound:
					return nil, resilience.NonRetryable(ErrProductNotFound)
				case codes.InvalidArgument, codes.PermissionDenied, codes.Unauthenticated:
					return nil, resilience.NonRetryable(err)
				}
			}
			return nil, fmt.Errorf("product.GetProduct(%s): %w", productID, err)
		}
		if resp.GetProduct() == nil {
			return nil, resilience.NonRetryable(ErrProductNotFound)
		}
		return resp.GetProduct(), nil
	})
}

// ListProducts fetches a page of public products from ProductService.
func (f *GRPCProductFetcher) ListProducts(ctx context.Context, page, pageSize int32) ([]*productv1.Product, int64, error) {
	type listResult struct {
		products []*productv1.Product
		total    int64
	}
	res, err := resilience.Do(ctx, f.exec, "ListProducts", func(attemptCtx context.Context) (listResult, error) {
		resp, err := f.client.ListProducts(attemptCtx, &productv1.ListProductsRequest{
			Page:     page,
			PageSize: pageSize,
			SortBy:   "oldest",
		})
		if err != nil {
			st, ok := status.FromError(err)
			if ok && (st.Code() == codes.InvalidArgument || st.Code() == codes.PermissionDenied) {
				return listResult{}, resilience.NonRetryable(err)
			}
			return listResult{}, fmt.Errorf("product.ListProducts(page=%d): %w", page, err)
		}
		return listResult{
			products: resp.GetProducts(),
			total:    resp.GetTotal(),
		}, nil
	})
	if err != nil {
		return nil, 0, err
	}
	return res.products, res.total, nil
}

// ListCategories fetches all categories from ProductService.
func (f *GRPCProductFetcher) ListCategories(ctx context.Context) ([]*productv1.Category, error) {
	return resilience.Do(ctx, f.exec, "ListCategories", func(attemptCtx context.Context) ([]*productv1.Category, error) {
		resp, err := f.client.ListCategories(attemptCtx, &productv1.ListCategoriesRequest{})
		if err != nil {
			return nil, fmt.Errorf("product.ListCategories: %w", err)
		}
		return resp.GetCategories(), nil
	})
}
