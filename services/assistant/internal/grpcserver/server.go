package grpcserver

import (
	"context"
	"errors"
	"strings"

	assistantv1 "github.com/nexusmart/core/gen/go/nexusmart/assistant/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/assistant/internal/llm"
	"github.com/nexusmart/core/services/assistant/internal/service"
	"github.com/nexusmart/core/services/assistant/internal/tools"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// AssistantBackend is the service interface required by the gRPC server.
type AssistantBackend interface {
	Handoff(ctx context.Context, cc tools.CallContext, req service.HandoffRequest) (*service.HandoffResult, error)
	SellerReport(ctx context.Context, cc tools.CallContext, question, period string) (*service.SellerReportResult, error)
	DescribeProduct(ctx context.Context, cc tools.CallContext, req service.DescribeProductRequest) (*service.DescribeProductResult, error)
	SuggestReply(ctx context.Context, cc tools.CallContext, req service.SuggestReplyRequest) (*service.SuggestReplyResult, error)
	GetReviewSummary(ctx context.Context, productID string) (*service.ReviewSummaryResult, error)
	RefreshReviewSummary(ctx context.Context, productID, shopID string, reviews []service.ReviewInput) (*service.ReviewSummaryResult, error)
	SubmitFeedback(ctx context.Context, userID string, req service.FeedbackRequest) error
}

// Server implements assistantv1.AssistantServiceServer.
type Server struct {
	assistantv1.UnimplementedAssistantServiceServer
	svc AssistantBackend
}

// New constructs a gRPC AssistantServiceServer.
func New(svc AssistantBackend) *Server {
	return &Server{svc: svc}
}

func (s *Server) Handoff(ctx context.Context, req *assistantv1.HandoffRequest) (*assistantv1.HandoffResponse, error) {
	cc, err := requireAuthCallContext(ctx)
	if err != nil {
		return nil, err
	}
	msgs := make([]service.ChatMessage, 0, len(req.GetMessages()))
	for _, m := range req.GetMessages() {
		msgs = append(msgs, service.ChatMessage{
			Role:    m.GetRole(),
			Content: m.GetContent(),
		})
	}
	res, err := s.svc.Handoff(ctx, cc, service.HandoffRequest{
		ShopID:         req.GetShopId(),
		ConversationID: req.GetConversationId(),
		Reason:         req.GetReason(),
		Messages:       msgs,
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &assistantv1.HandoffResponse{
		TicketId:            res.TicketID,
		ConversationId:      res.ConversationID,
		ShopId:              res.ShopID,
		Summary:             res.Summary,
		Status:              res.Status,
		WithinBusinessHours: res.WithinBusinessHours,
		EstimatedResponse:   res.EstimatedResponse,
	}, nil
}

func (s *Server) SellerReport(ctx context.Context, req *assistantv1.SellerReportRequest) (*assistantv1.SellerReportResponse, error) {
	cc, err := requireAuthCallContext(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.svc.SellerReport(ctx, cc, req.GetQuestion(), req.GetPeriod())
	if err != nil {
		return nil, mapError(err)
	}
	top := make([]*assistantv1.TopProductStat, 0, len(res.Metrics.TopProducts))
	for _, tp := range res.Metrics.TopProducts {
		top = append(top, &assistantv1.TopProductStat{
			ProductId:   tp.ProductID,
			ProductName: tp.ProductName,
			UnitsSold:   tp.UnitsSold,
			RevenueVnd:  tp.RevenueVND,
		})
	}
	return &assistantv1.SellerReportResponse{
		ShopId: res.ShopID,
		Period: res.Period,
		Answer: res.Answer,
		Metrics: &assistantv1.SellerReportMetrics{
			TotalOrders:         res.Metrics.TotalOrders,
			CompletedOrders:     res.Metrics.CompletedOrders,
			CancelledOrders:     res.Metrics.CancelledOrders,
			CancelRatePct:       res.Metrics.CancelRatePct,
			GrossRevenueVnd:     res.Metrics.GrossRevenueVND,
			AvailableBalanceVnd: res.Metrics.AvailableBalanceVND,
			PendingBalanceVnd:   res.Metrics.PendingBalanceVND,
			TopProducts:         top,
		},
		Usage: toProtoUsage(res.Usage),
	}, nil
}

func (s *Server) DescribeProduct(ctx context.Context, req *assistantv1.DescribeProductRequest) (*assistantv1.DescribeProductResponse, error) {
	cc, err := requireAuthCallContext(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.svc.DescribeProduct(ctx, cc, service.DescribeProductRequest{
		Name:       req.GetName(),
		Category:   req.GetCategory(),
		Attributes: req.GetAttributes(),
		PriceVND:   req.GetPriceVnd(),
		Tone:       req.GetTone(),
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &assistantv1.DescribeProductResponse{
		TitleSuggestion: res.TitleSuggestion,
		Description:     res.Description,
		Highlights:      res.Highlights,
		AiGenerated:     res.AIGenerated,
		Usage:           toProtoUsage(res.Usage),
	}, nil
}

func (s *Server) SuggestReply(ctx context.Context, req *assistantv1.SuggestReplyRequest) (*assistantv1.SuggestReplyResponse, error) {
	cc, err := requireAuthCallContext(ctx)
	if err != nil {
		return nil, err
	}
	history := make([]service.ChatHistoryMessage, 0, len(req.GetHistory()))
	for _, h := range req.GetHistory() {
		history = append(history, service.ChatHistoryMessage{
			Sender: h.GetSender(),
			Body:   h.GetBody(),
		})
	}
	res, err := s.svc.SuggestReply(ctx, cc, service.SuggestReplyRequest{
		ConversationID:  req.GetConversationId(),
		ProductID:       req.GetProductId(),
		CustomerMessage: req.GetCustomerMessage(),
		History:         history,
	})
	if err != nil {
		return nil, mapError(err)
	}
	suggestions := make([]*assistantv1.ReplySuggestion, 0, len(res.Suggestions))
	for _, sug := range res.Suggestions {
		suggestions = append(suggestions, &assistantv1.ReplySuggestion{
			Id:   sug.ID,
			Text: sug.Text,
			Tone: sug.Tone,
		})
	}
	return &assistantv1.SuggestReplyResponse{
		Suggestions:       suggestions,
		NeedsMoreInfo:     res.NeedsMoreInfo,
		InjectionDetected: res.InjectionDetected,
		AiGenerated:       res.AIGenerated,
		Usage:             toProtoUsage(res.Usage),
	}, nil
}

func (s *Server) GetReviewSummary(ctx context.Context, req *assistantv1.GetReviewSummaryRequest) (*assistantv1.GetReviewSummaryResponse, error) {
	res, err := s.svc.GetReviewSummary(ctx, req.GetProductId())
	if err != nil {
		return nil, mapError(err)
	}
	return toProtoReviewSummary(res), nil
}

func (s *Server) RefreshReviewSummary(ctx context.Context, req *assistantv1.RefreshReviewSummaryRequest) (*assistantv1.RefreshReviewSummaryResponse, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}
	if !auth.HasRole(claims, auth.RoleSeller) && !auth.HasCapability(claims, auth.CapProductsReview) {
		return nil, status.Error(codes.PermissionDenied, "only seller or authorized staff can refresh review summaries")
	}
	shopID := strings.TrimSpace(req.GetShopId())
	if strings.EqualFold(claims.Role, "seller") {
		if claims.ShopID == "" {
			return nil, status.Error(codes.PermissionDenied, "seller shop claim missing")
		}
		if shopID != "" && shopID != claims.ShopID {
			return nil, status.Error(codes.PermissionDenied, "cannot refresh reviews for another shop")
		}
		shopID = claims.ShopID
	}

	revs := make([]service.ReviewInput, 0, len(req.GetReviews()))
	for _, r := range req.GetReviews() {
		revs = append(revs, service.ReviewInput{
			ReviewID: r.GetReviewId(),
			Rating:   r.GetRating(),
			Comment:  r.GetComment(),
		})
	}
	res, err := s.svc.RefreshReviewSummary(ctx, req.GetProductId(), shopID, revs)
	if err != nil {
		return nil, mapError(err)
	}
	return &assistantv1.RefreshReviewSummaryResponse{
		Summary: toProtoReviewSummary(res),
	}, nil
}

func (s *Server) SubmitFeedback(ctx context.Context, req *assistantv1.SubmitFeedbackRequest) (*assistantv1.SubmitFeedbackResponse, error) {
	userID := ""
	if claims, ok := auth.ClaimsFromContext(ctx); ok {
		userID = claims.UserID()
	}
	err := s.svc.SubmitFeedback(ctx, userID, service.FeedbackRequest{
		ConversationID: req.GetConversationId(),
		MessageID:      req.GetMessageId(),
		Helpful:        req.GetHelpful(),
		Comment:        req.GetComment(),
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &assistantv1.SubmitFeedbackResponse{Ok: true}, nil
}

// requireAuthCallContext extracts verified JWT claims & raw Bearer token ONLY from
// auth.ClaimsFromContext / auth.RawTokenFromContext (never plain gRPC metadata).
func requireAuthCallContext(ctx context.Context) (tools.CallContext, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return tools.CallContext{}, status.Error(codes.Unauthenticated, "authentication required")
	}
	rawBearer := auth.RawTokenFromContext(ctx)
	if strings.TrimSpace(rawBearer) == "" {
		return tools.CallContext{}, status.Error(codes.Unauthenticated, "end-user bearer token required")
	}
	return tools.CallContext{
		UserID:       claims.UserID(),
		UserRole:     claims.Role,
		SellerShopID: claims.ShopID,
		RawBearer:    rawBearer,
	}, nil
}

func mapError(err error) error {
	switch {
	case errors.Is(err, service.ErrUnauthorized):
		return status.Error(codes.Unauthenticated, err.Error())
	case errors.Is(err, service.ErrForbidden):
		return status.Error(codes.PermissionDenied, err.Error())
	case errors.Is(err, service.ErrInvalidArgument):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, service.ErrRateLimited), errors.Is(err, service.ErrBudgetExceeded), errors.Is(err, service.ErrAbuseLocked):
		return status.Error(codes.ResourceExhausted, err.Error())
	case errors.Is(err, service.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	default:
		return status.Error(codes.Internal, "internal assistant error")
	}
}

func toProtoUsage(u llm.Usage) *assistantv1.UsageMetrics {
	return &assistantv1.UsageMetrics{
		InputTokens:  int32(u.InputTokens),
		OutputTokens: int32(u.OutputTokens),
		TotalTokens:  int32(u.TotalTokens),
		CostVnd:      u.CostVND,
	}
}

func toProtoReviewSummary(res *service.ReviewSummaryResult) *assistantv1.GetReviewSummaryResponse {
	if res == nil {
		return &assistantv1.GetReviewSummaryResponse{}
	}
	out := &assistantv1.GetReviewSummaryResponse{
		ProductId:          res.ProductID,
		Available:          res.Available,
		ReviewCount:        res.ReviewCount,
		MinReviewsRequired: res.MinReviewsRequired,
		AverageRating:      res.AverageRating,
		Summary:            res.Summary,
		Pros:               res.Pros,
		Cons:               res.Cons,
		AiGenerated:        res.AIGenerated,
	}
	if !res.UpdatedAt.IsZero() {
		out.UpdatedAt = timestamppb.New(res.UpdatedAt)
	}
	return out
}
