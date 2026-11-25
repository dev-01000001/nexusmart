package grpcserver

import (
	"context"

	searchv1 "github.com/nexusmart/core/gen/go/nexusmart/search/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// SearchService defines the business backend invoked by the gRPC transport layer.
type SearchService interface {
	SearchProducts(ctx context.Context, req *searchv1.SearchProductsRequest) (*searchv1.SearchProductsResponse, error)
	HybridSearch(ctx context.Context, req *searchv1.HybridSearchRequest) (*searchv1.HybridSearchResponse, error)
	Suggest(ctx context.Context, req *searchv1.SuggestRequest) (*searchv1.SuggestResponse, error)
	ListSearchHistory(ctx context.Context, req *searchv1.ListSearchHistoryRequest) (*searchv1.ListSearchHistoryResponse, error)
	DeleteSearchHistory(ctx context.Context, req *searchv1.DeleteSearchHistoryRequest) (*searchv1.DeleteSearchHistoryResponse, error)
	ReindexAll(ctx context.Context, req *searchv1.ReindexAllRequest) (*searchv1.ReindexAllResponse, error)
	GetReindexStatus(ctx context.Context, req *searchv1.GetReindexStatusRequest) (*searchv1.GetReindexStatusResponse, error)
}

// Server exposes all 7 SearchService RPCs over gRPC.
type Server struct {
	searchv1.UnimplementedSearchServiceServer
	svc SearchService
}

// New constructs a SearchService gRPC server backed by svc.
func New(svc SearchService) *Server {
	return &Server{svc: svc}
}

func (s *Server) SearchProducts(ctx context.Context, req *searchv1.SearchProductsRequest) (*searchv1.SearchProductsResponse, error) {
	if s.svc == nil {
		return nil, status.Error(codes.Unavailable, "search service unavailable")
	}
	return s.svc.SearchProducts(ctx, req)
}

func (s *Server) HybridSearch(ctx context.Context, req *searchv1.HybridSearchRequest) (*searchv1.HybridSearchResponse, error) {
	if s.svc == nil {
		return nil, status.Error(codes.Unavailable, "search service unavailable")
	}
	return s.svc.HybridSearch(ctx, req)
}

func (s *Server) Suggest(ctx context.Context, req *searchv1.SuggestRequest) (*searchv1.SuggestResponse, error) {
	if s.svc == nil {
		return nil, status.Error(codes.Unavailable, "search service unavailable")
	}
	return s.svc.Suggest(ctx, req)
}

func (s *Server) ListSearchHistory(ctx context.Context, req *searchv1.ListSearchHistoryRequest) (*searchv1.ListSearchHistoryResponse, error) {
	if s.svc == nil {
		return nil, status.Error(codes.Unavailable, "search service unavailable")
	}
	return s.svc.ListSearchHistory(ctx, req)
}

func (s *Server) DeleteSearchHistory(ctx context.Context, req *searchv1.DeleteSearchHistoryRequest) (*searchv1.DeleteSearchHistoryResponse, error) {
	if s.svc == nil {
		return nil, status.Error(codes.Unavailable, "search service unavailable")
	}
	return s.svc.DeleteSearchHistory(ctx, req)
}

func (s *Server) ReindexAll(ctx context.Context, req *searchv1.ReindexAllRequest) (*searchv1.ReindexAllResponse, error) {
	if s.svc == nil {
		return nil, status.Error(codes.Unavailable, "search service unavailable")
	}
	return s.svc.ReindexAll(ctx, req)
}

func (s *Server) GetReindexStatus(ctx context.Context, req *searchv1.GetReindexStatusRequest) (*searchv1.GetReindexStatusResponse, error) {
	if s.svc == nil {
		return nil, status.Error(codes.Unavailable, "search service unavailable")
	}
	return s.svc.GetReindexStatus(ctx, req)
}
