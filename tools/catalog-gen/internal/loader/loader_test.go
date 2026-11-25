package loader

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/tools/catalog-gen/internal/generator"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func TestRESTLoaderDiscoverCreateApproveAnd429Retry(t *testing.T) {
	var (
		mu              sync.Mutex
		createdCount    atomic.Int64
		approvedIDs     = make(map[string]bool)
		rateLimitedOnce atomic.Int64
		serverErrOnce   atomic.Int64
		createdShopByID = make(map[string]string)
	)

	sellerTokens := map[string]struct {
		shopID string
		name   string
	}{
		"tok-seller-1": {shopID: "shop-uuid-1", name: "Xưởng Dệt Bạch Đằng"},
		"tok-seller-2": {shopID: "shop-uuid-2", name: "Kho Basic Sài Gòn"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/auth/login":
			var body struct {
				Email    string `json:"email"`
				Password string `json:"password"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			switch body.Email {
			case "admin@nexusmart.local":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"access_token": "tok-admin",
					"user":         map[string]any{"id": "admin-1", "role": "admin"},
				})
			case "seller@nexusmart.local":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"access_token": "tok-seller-1",
					"user":         map[string]any{"id": "seller-1", "role": "seller", "shop_id": "shop-uuid-1"},
				})
			case "seller-saigon@nexusmart.local":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"access_token": "tok-seller-2",
					"user":         map[string]any{"id": "seller-2", "role": "seller", "shop_id": "shop-uuid-2"},
				})
			default:
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "UNAUTHENTICATED"}})
			}

		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/seller/shop":
			tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			info, ok := sellerTokens[tok]
			if !ok {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"shop": map[string]any{"id": info.shopID, "name": info.name, "status": "ACTIVE"},
			})

		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/categories":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"categories": []map[string]any{
					{"id": "cat-1", "slug": "ao-thun", "name": "Áo thun"},
					{"id": "cat-2", "slug": "do-lot", "name": "Đồ lót"},
				},
			})

		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/seller/products":
			tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			info, ok := sellerTokens[tok]
			if !ok {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			// Simulate 429 with Retry-After header on first request, and transient 503 on second.
			if rateLimitedOnce.Add(1) == 1 {
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error":{"code":"RATE_LIMITED"}}`))
				return
			}
			if serverErrOnce.Add(1) == 2 {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"error":{"code":"UNAVAILABLE"}}`))
				return
			}

			var body struct {
				CategoryID string `json:"category_id"`
				Name       string `json:"name"`
				SKUs       []any  `json:"skus"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" || len(body.SKUs) == 0 {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			id := fmt.Sprintf("prod-%d", createdCount.Add(1))
			mu.Lock()
			createdShopByID[id] = info.shopID
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"product": map[string]any{"id": id, "shop_id": info.shopID},
			})

		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/v1/admin/products/") && strings.HasSuffix(r.URL.Path, "/review"):
			if r.Header.Get("Authorization") != "Bearer tok-admin" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			var body struct {
				Decision string `json:"decision"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Decision != "APPROVE" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			prodID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/admin/products/"), "/review")
			mu.Lock()
			approvedIDs[prodID] = true
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{"product": map[string]any{"id": prodID, "status": "APPROVED"}})

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client := NewRESTClient(srv.URL, 4)
	client.SetRetryConfig(5*time.Millisecond, 4)

	ctx := context.Background()
	disc, err := client.Discover(
		ctx,
		AdminCredentials{Email: "admin@nexusmart.local", Password: "AdminDevPassword123!"},
		[]SellerAccount{
			{Slug: "shop-bach-dang", Email: "seller@nexusmart.local", Password: "SellerDemo123!"},
			{Slug: "shop-sai-gon", Email: "seller-saigon@nexusmart.local", Password: "SellerDemo123!"},
			{Slug: "shop-missing", Email: "missing@nexusmart.local", Password: "SellerDemo123!"},
		},
		true,
		true,
	)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(disc.Shops) != 2 || len(disc.Categories) != 2 || len(disc.Skipped) != 1 {
		t.Fatalf("unexpected discovery: shops=%d cats=%d skipped=%d", len(disc.Shops), len(disc.Categories), len(disc.Skipped))
	}

	gen := generator.New(generator.DefaultSeed, disc.Shops, disc.Categories)
	products := gen.Generate(12)

	summary := RunIngest(ctx, "rest", generator.DefaultSeed, products, 4, true, client, nil)
	if summary.Generated != 12 || summary.Created != 12 || summary.Approved != 12 || summary.Failed != 0 {
		t.Fatalf("unexpected REST ingest summary: %+v", summary)
	}
	if rateLimitedOnce.Load() < 2 {
		t.Fatalf("expected 429 retry to trigger, saw %d calls", rateLimitedOnce.Load())
	}

	mu.Lock()
	defer mu.Unlock()
	if len(approvedIDs) != 12 || len(createdShopByID) != 12 {
		t.Fatalf("expected 12 created & approved products, got created=%d approved=%d", len(createdShopByID), len(approvedIDs))
	}
}

type fakeProductGRPCServer struct {
	productv1.UnimplementedProductServiceServer
	mu             sync.Mutex
	seq            int64
	createdByShop  map[string]int
	approvedIDs    map[string]bool
	failOnceCreate atomic.Bool
}

func (s *fakeProductGRPCServer) ListCategories(_ context.Context, _ *productv1.ListCategoriesRequest) (*productv1.ListCategoriesResponse, error) {
	return &productv1.ListCategoriesResponse{
		Categories: []*productv1.Category{
			{Id: "cat-ao-thun", Slug: "ao-thun", Name: "Áo thun"},
			{Id: "cat-tat-phu-kien", Slug: "tat-phu-kien", Name: "Tất & Phụ kiện"},
		},
	}, nil
}

func (s *fakeProductGRPCServer) CreateProduct(ctx context.Context, req *productv1.CreateProductRequest) (*productv1.CreateProductResponse, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing jwt claims")
	}
	if !auth.HasRole(claims, "seller") || claims.ShopID != req.GetShopId() {
		return nil, status.Errorf(codes.PermissionDenied, "seller shop mismatch: claim=%q req=%q", claims.ShopID, req.GetShopId())
	}
	if !s.failOnceCreate.Swap(true) {
		return nil, status.Error(codes.Unavailable, "transient overload")
	}
	s.mu.Lock()
	s.seq++
	id := fmt.Sprintf("grpc-prod-%d", s.seq)
	s.createdByShop[req.GetShopId()]++
	s.mu.Unlock()
	return &productv1.CreateProductResponse{
		Product: &productv1.Product{
			Id:         id,
			ShopId:     req.GetShopId(),
			CategoryId: req.GetCategoryId(),
			Name:       req.GetName(),
			Status:     productv1.ProductStatus_PRODUCT_STATUS_PENDING,
		},
	}, nil
}

func (s *fakeProductGRPCServer) AdminReviewProduct(ctx context.Context, req *productv1.AdminReviewProductRequest) (*productv1.AdminReviewProductResponse, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing jwt claims")
	}
	if !auth.HasRole(claims, "admin", "super_admin") {
		return nil, status.Error(codes.PermissionDenied, "admin role required")
	}
	if req.GetDecision() != productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE {
		return nil, status.Error(codes.InvalidArgument, "expected APPROVE decision")
	}
	s.mu.Lock()
	s.approvedIDs[req.GetProductId()] = true
	s.mu.Unlock()
	return &productv1.AdminReviewProductResponse{
		Product: &productv1.Product{
			Id:     req.GetProductId(),
			Status: productv1.ProductStatus_PRODUCT_STATUS_APPROVED,
		},
	}, nil
}

func TestGRPCLoaderBufconnJWTForwardingAndApprove(t *testing.T) {
	const jwtSecret = "catalog-gen-bufconn-test-secret"

	authMgr, err := auth.NewManager(jwtSecret, 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}

	lis := bufconn.Listen(1 << 20)
	fakeSrv := &fakeProductGRPCServer{
		createdByShop: make(map[string]int),
		approvedIDs:   make(map[string]bool),
	}
	grpcSrv := grpc.NewServer(
		grpc.UnaryInterceptor(auth.GRPCUnaryServerInterceptor(authMgr, nil)),
	)
	productv1.RegisterProductServiceServer(grpcSrv, fakeSrv)
	go func() { _ = grpcSrv.Serve(lis) }()
	defer grpcSrv.Stop()

	dialer := func(context.Context, string) (net.Conn, error) {
		return lis.Dial()
	}
	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	defer func() { _ = conn.Close() }()

	adminTok, adminUID, shops, shopTokens, err := MintIsolatedTokens(jwtSecret, []string{"shop-bach-dang", "shop-ha-noi"})
	if err != nil {
		t.Fatalf("MintIsolatedTokens: %v", err)
	}

	cli := NewGRPCClientWithServiceClient(productv1.NewProductServiceClient(conn), adminTok, adminUID, shopTokens)
	cli.SetRetryConfig(5*time.Millisecond, 3)

	ctx := context.Background()
	cats, err := cli.ListCategories(ctx, false)
	if err != nil {
		t.Fatalf("ListCategories: %v", err)
	}
	if len(cats) != 2 {
		t.Fatalf("expected 2 categories, got %d", len(cats))
	}

	gen := generator.New(generator.DefaultSeed, shops, cats)
	products := gen.Generate(16)

	summary := RunIngest(ctx, "grpc", generator.DefaultSeed, products, 4, true, cli, nil)
	if summary.Generated != 16 || summary.Created != 16 || summary.Approved != 16 || summary.Failed != 0 {
		t.Fatalf("unexpected gRPC ingest summary: %+v", summary)
	}

	fakeSrv.mu.Lock()
	defer fakeSrv.mu.Unlock()
	if fakeSrv.createdByShop["shop-bach-dang"] != 8 || fakeSrv.createdByShop["shop-ha-noi"] != 8 {
		t.Fatalf("expected balanced 8/8 per shop, got %+v", fakeSrv.createdByShop)
	}
	if len(fakeSrv.approvedIDs) != 16 {
		t.Fatalf("expected 16 approved products, got %d", len(fakeSrv.approvedIDs))
	}
}

func TestParseRetryAfter(t *testing.T) {
	fallback := 250 * time.Millisecond
	if got := parseRetryAfter("", fallback); got != fallback {
		t.Fatalf("empty Retry-After: got %v want %v", got, fallback)
	}
	if got := parseRetryAfter("3", fallback); got != 3*time.Second {
		t.Fatalf("integer Retry-After: got %v want 3s", got)
	}
	if got := parseRetryAfter("0", fallback); got != 10*time.Millisecond {
		t.Fatalf("zero Retry-After: got %v want 10ms", got)
	}
	future := time.Now().UTC().Add(2 * time.Second).Format(http.TimeFormat)
	if got := parseRetryAfter(future, fallback); got < 500*time.Millisecond || got > 4*time.Second {
		t.Fatalf("HTTP-date Retry-After out of range: got %v", got)
	}
}
