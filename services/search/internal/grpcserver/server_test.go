package grpcserver

import (
	"context"
	"net"
	"testing"

	searchv1 "github.com/nexusmart/core/gen/go/nexusmart/search/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type stubSearchService struct {
	lastSearchUserID string
}

func (s *stubSearchService) SearchProducts(ctx context.Context, _ *searchv1.SearchProductsRequest) (*searchv1.SearchProductsResponse, error) {
	if c, ok := auth.ClaimsFromContext(ctx); ok && c.Service == "" {
		s.lastSearchUserID = c.UserID()
	} else {
		s.lastSearchUserID = ""
	}
	return &searchv1.SearchProductsResponse{Total: 0}, nil
}

func (s *stubSearchService) HybridSearch(ctx context.Context, _ *searchv1.HybridSearchRequest) (*searchv1.HybridSearchResponse, error) {
	if _, ok := auth.ClaimsFromContext(ctx); !ok {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}
	return &searchv1.HybridSearchResponse{}, nil
}

func (s *stubSearchService) Suggest(_ context.Context, _ *searchv1.SuggestRequest) (*searchv1.SuggestResponse, error) {
	return &searchv1.SuggestResponse{}, nil
}

func (s *stubSearchService) SimilarProducts(_ context.Context, _ *searchv1.SimilarProductsRequest) (*searchv1.SimilarProductsResponse, error) {
	return &searchv1.SimilarProductsResponse{}, nil
}

func (s *stubSearchService) ListSearchHistory(ctx context.Context, _ *searchv1.ListSearchHistoryRequest) (*searchv1.ListSearchHistoryResponse, error) {
	c, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}
	if c.Service != "" {
		return nil, status.Error(codes.PermissionDenied, "service tokens forbidden")
	}
	return &searchv1.ListSearchHistoryResponse{}, nil
}

func (s *stubSearchService) DeleteSearchHistory(ctx context.Context, _ *searchv1.DeleteSearchHistoryRequest) (*searchv1.DeleteSearchHistoryResponse, error) {
	c, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}
	if c.Service != "" {
		return nil, status.Error(codes.PermissionDenied, "service tokens forbidden")
	}
	return &searchv1.DeleteSearchHistoryResponse{Ok: true}, nil
}

func (s *stubSearchService) ReindexAll(ctx context.Context, _ *searchv1.ReindexAllRequest) (*searchv1.ReindexAllResponse, error) {
	c, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}
	if c.Service == "" && !auth.HasCapability(c, auth.CapSearchReindex) {
		return nil, status.Error(codes.PermissionDenied, "missing search.reindex")
	}
	return &searchv1.ReindexAllResponse{JobId: "job-1", Status: searchv1.ReindexJobStatus_REINDEX_JOB_STATUS_RUNNING}, nil
}

func (s *stubSearchService) GetReindexStatus(ctx context.Context, _ *searchv1.GetReindexStatusRequest) (*searchv1.GetReindexStatusResponse, error) {
	c, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}
	if c.Service == "" && !auth.HasCapability(c, auth.CapSearchReindex) {
		return nil, status.Error(codes.PermissionDenied, "missing search.reindex")
	}
	return &searchv1.GetReindexStatusResponse{JobId: "job-1", Status: searchv1.ReindexJobStatus_REINDEX_JOB_STATUS_COMPLETED}, nil
}

func TestGRPCServer_AuthAndRBACMatrix(t *testing.T) {
	jwtMgr, err := auth.NewManager("test-user-jwt-secret", auth.DefaultAccessTTL)
	if err != nil {
		t.Fatalf("NewManager user: %v", err)
	}
	svcMgr, err := auth.NewManager("test-service-jwt-secret", auth.ServiceTokenTTL)
	if err != nil {
		t.Fatalf("NewManager service: %v", err)
	}

	lis := bufconn.Listen(1 << 20)
	stub := &stubSearchService{}
	grpcSrv := grpc.NewServer(grpc.UnaryInterceptor(auth.GRPCUnaryServerInterceptor(jwtMgr, svcMgr)))
	searchv1.RegisterSearchServiceServer(grpcSrv, New(stub))
	go func() { _ = grpcSrv.Serve(lis) }()
	t.Cleanup(func() {
		grpcSrv.Stop()
		_ = lis.Close()
	})

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := searchv1.NewSearchServiceClient(conn)

	ctxWithRole := func(userID, role string) context.Context {
		tok, _, _, signErr := jwtMgr.SignAccessToken(userID, role, "")
		if signErr != nil {
			t.Fatalf("sign token: %v", signErr)
		}
		return auth.InjectOutgoingGRPC(context.Background(), tok)
	}
	svcToken, err := svcMgr.SignServiceToken("ai-assistant")
	if err != nil {
		t.Fatalf("sign service token: %v", err)
	}
	ctxService := auth.InjectOutgoingGRPC(context.Background(), svcToken)

	// 1. Public RPCs work anonymously, ignore spoofed metadata, and reject service tokens.
	spoofCtx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("x-user-id", "victim", "x-user-role", "admin"))
	if _, err := client.SearchProducts(spoofCtx, &searchv1.SearchProductsRequest{Q: "ao"}); err != nil {
		t.Fatalf("SearchProducts anonymous failed: %v", err)
	}
	if stub.lastSearchUserID != "" {
		t.Fatalf("spoofed x-user-id must be ignored, got %q", stub.lastSearchUserID)
	}
	if _, err := client.SearchProducts(ctxService, &searchv1.SearchProductsRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("SearchProducts with service token code = %v, want PermissionDenied", status.Code(err))
	}
	if _, err := client.Suggest(ctxService, &searchv1.SuggestRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("Suggest with service token code = %v, want PermissionDenied", status.Code(err))
	}
	if _, err := client.SimilarProducts(spoofCtx, &searchv1.SimilarProductsRequest{ProductId: "p-1"}); err != nil {
		t.Fatalf("SimilarProducts anonymous failed: %v", err)
	}
	if _, err := client.SimilarProducts(ctxService, &searchv1.SimilarProductsRequest{ProductId: "p-1"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("SimilarProducts with service token code = %v, want PermissionDenied", status.Code(err))
	}

	// 2. History RPCs require end-user JWT and reject anonymous & service tokens.
	if _, err := client.ListSearchHistory(context.Background(), &searchv1.ListSearchHistoryRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("ListSearchHistory anonymous code = %v, want Unauthenticated", status.Code(err))
	}
	if _, err := client.ListSearchHistory(ctxService, &searchv1.ListSearchHistoryRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("ListSearchHistory service token code = %v, want PermissionDenied", status.Code(err))
	}
	if _, err := client.DeleteSearchHistory(ctxService, &searchv1.DeleteSearchHistoryRequest{ClearAll: true}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("DeleteSearchHistory service token code = %v, want PermissionDenied", status.Code(err))
	}

	// 3. HybridSearch rejects anonymous, allows service token and end-user JWT.
	if _, err := client.HybridSearch(context.Background(), &searchv1.HybridSearchRequest{Query: "ao thun"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("HybridSearch anonymous code = %v, want Unauthenticated", status.Code(err))
	}
	if _, err := client.HybridSearch(ctxService, &searchv1.HybridSearchRequest{Query: "ao thun"}); err != nil {
		t.Fatalf("HybridSearch service token failed: %v", err)
	}
	if _, err := client.HybridSearch(ctxWithRole("buyer-1", auth.RoleBuyer), &searchv1.HybridSearchRequest{Query: "ao thun"}); err != nil {
		t.Fatalf("HybridSearch buyer JWT failed: %v", err)
	}

	// 4. ReindexAll & GetReindexStatus RBAC matrix (mandatory attack test).
	if _, err := client.ReindexAll(context.Background(), &searchv1.ReindexAllRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("ReindexAll anonymous code = %v, want Unauthenticated", status.Code(err))
	}
	for _, deniedRole := range []string{auth.RoleBuyer, auth.RoleSeller, auth.RoleModerator, auth.RoleCSKH, auth.RoleAccountant} {
		if _, err := client.ReindexAll(ctxWithRole("u-"+deniedRole, deniedRole), &searchv1.ReindexAllRequest{}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("ReindexAll role=%s code = %v, want PermissionDenied", deniedRole, status.Code(err))
		}
		if _, err := client.GetReindexStatus(ctxWithRole("u-"+deniedRole, deniedRole), &searchv1.GetReindexStatusRequest{JobId: "job-1"}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("GetReindexStatus role=%s code = %v, want PermissionDenied", deniedRole, status.Code(err))
		}
	}
	for _, allowedRole := range []string{auth.RoleAdmin, auth.RoleSuperAdmin, auth.RoleOps} {
		if _, err := client.ReindexAll(ctxWithRole("u-"+allowedRole, allowedRole), &searchv1.ReindexAllRequest{}); err != nil {
			t.Fatalf("ReindexAll role=%s failed: %v", allowedRole, err)
		}
		if _, err := client.GetReindexStatus(ctxWithRole("u-"+allowedRole, allowedRole), &searchv1.GetReindexStatusRequest{JobId: "job-1"}); err != nil {
			t.Fatalf("GetReindexStatus role=%s failed: %v", allowedRole, err)
		}
	}
	if _, err := client.ReindexAll(ctxService, &searchv1.ReindexAllRequest{}); err != nil {
		t.Fatalf("ReindexAll service token failed: %v", err)
	}
	if _, err := client.GetReindexStatus(ctxService, &searchv1.GetReindexStatusRequest{JobId: "job-1"}); err != nil {
		t.Fatalf("GetReindexStatus service token failed: %v", err)
	}
}
