package productclient_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/promotion/internal/productclient"
)

type stubProductGRPC struct {
	productv1.ProductServiceClient
	svcMgr             *auth.Manager
	descCalls          atomic.Int32
	skuCalls           atomic.Int32
	lastDescHasSvcTok  atomic.Bool
	lastSKUsHasAuthHdr atomic.Bool
}

func (s *stubProductGRPC) GetCategoryDescendants(ctx context.Context, req *productv1.GetCategoryDescendantsRequest, _ ...grpc.CallOption) (*productv1.GetCategoryDescendantsResponse, error) {
	s.descCalls.Add(1)
	if md, ok := metadata.FromOutgoingContext(ctx); ok {
		vals := md.Get(auth.MDAuthorization)
		if len(vals) > 0 {
			if claims, err := s.svcMgr.VerifyServiceToken(vals[0]); err == nil && claims.Service != "" {
				s.lastDescHasSvcTok.Store(true)
			}
		}
	}
	out := append([]string(nil), req.GetCategoryIds()...)
	out = append(out, "child-1", "grandchild-1")
	return &productv1.GetCategoryDescendantsResponse{CategoryIds: out}, nil
}

func (s *stubProductGRPC) GetSKUsBatch(ctx context.Context, req *productv1.GetSKUsBatchRequest, _ ...grpc.CallOption) (*productv1.GetSKUsBatchResponse, error) {
	s.skuCalls.Add(1)
	if md, ok := metadata.FromOutgoingContext(ctx); ok && len(md.Get(auth.MDAuthorization)) > 0 {
		s.lastSKUsHasAuthHdr.Store(true)
	}
	skus := make([]*productv1.SKU, 0, len(req.GetSkuIds()))
	for _, id := range req.GetSkuIds() {
		skus = append(skus, &productv1.SKU{Id: id, ShopId: "shop-1", PriceVnd: 100000, MinPrice_30DVnd: 90000, AvailableStock: 20, IsActive: true})
	}
	return &productv1.GetSKUsBatchResponse{Skus: skus}, nil
}

func TestProductClient_CategoryDescendants_UsesServiceTokenAndShortCache(t *testing.T) {
	svcMgr, err := auth.NewManager("test-service-secret-key", auth.ServiceTokenTTL)
	if err != nil {
		t.Fatal(err)
	}
	stub := &stubProductGRPC{svcMgr: svcMgr}
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

	cl := productclient.NewWithClient(stub, svcMgr, 30*time.Second)
	cl.SetClock(func() time.Time { return now })

	ctx := context.Background()
	got1, err := cl.GetCategoryDescendants(ctx, []string{"root-cat"})
	if err != nil {
		t.Fatalf("1st GetCategoryDescendants: %v", err)
	}
	if !stub.lastDescHasSvcTok.Load() {
		t.Fatalf("expected GetCategoryDescendants to send valid service token")
	}
	if len(got1) != 3 || stub.descCalls.Load() != 1 {
		t.Fatalf("expected 3 ids and 1 RPC call, got ids=%v calls=%d", got1, stub.descCalls.Load())
	}

	// Second call within TTL hits cache -> still 1 RPC call
	now = now.Add(15 * time.Second)
	got2, err := cl.GetCategoryDescendants(ctx, []string{"root-cat"})
	if err != nil {
		t.Fatalf("2nd GetCategoryDescendants: %v", err)
	}
	if len(got2) != 3 || stub.descCalls.Load() != 1 {
		t.Fatalf("expected cache hit (1 RPC call), got ids=%v calls=%d", got2, stub.descCalls.Load())
	}

	// After TTL expires -> refreshes from ProductService (2 RPC calls)
	now = now.Add(20 * time.Second)
	if _, err := cl.GetCategoryDescendants(ctx, []string{"root-cat"}); err != nil {
		t.Fatalf("3rd GetCategoryDescendants: %v", err)
	}
	if stub.descCalls.Load() != 2 {
		t.Fatalf("expected cache expiry refresh (2 RPC calls), got %d", stub.descCalls.Load())
	}

	// Public GetSKUsBatch MUST NOT attach a service token (auth interceptor rejects service tokens on public RPCs)
	skus, err := cl.GetSKUsBatch(ctx, []string{"sku-1"})
	if err != nil {
		t.Fatalf("GetSKUsBatch: %v", err)
	}
	if stub.lastSKUsHasAuthHdr.Load() {
		t.Fatalf("GetSKUsBatch must NOT send Authorization service token header")
	}
	if skus["sku-1"] == nil || skus["sku-1"].MinPrice_30DVnd != 90000 {
		t.Fatalf("unexpected SKU map: %+v", skus)
	}
}
