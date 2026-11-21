package grpcserver

import (
	"context"
	"testing"
	"time"

	assistantv1 "github.com/nexusmart/core/gen/go/nexusmart/assistant/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/assistant/internal/llm"
	"github.com/nexusmart/core/services/assistant/internal/service"
	"github.com/nexusmart/core/services/assistant/internal/tools"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type fakeBackend struct {
	lastCC        tools.CallContext
	sellerErr     error
	describeErr   error
	feedbackSaved bool
}

func (f *fakeBackend) Handoff(_ context.Context, cc tools.CallContext, req service.HandoffRequest) (*service.HandoffResult, error) {
	f.lastCC = cc
	return &service.HandoffResult{
		TicketID:            "t-1",
		ConversationID:      "chat-1",
		ShopID:              req.ShopID,
		Summary:             "Tóm tắt",
		Status:              "open",
		WithinBusinessHours: true,
		EstimatedResponse:   "5-15 phút",
	}, nil
}

func (f *fakeBackend) SellerReport(_ context.Context, cc tools.CallContext, _, period string) (*service.SellerReportResult, error) {
	f.lastCC = cc
	if f.sellerErr != nil {
		return nil, f.sellerErr
	}
	return &service.SellerReportResult{
		ShopID: cc.SellerShopID,
		Period: period,
		Answer: "Doanh thu 1.000.000đ",
		Metrics: service.SellerReportMetrics{
			TotalOrders:     5,
			GrossRevenueVND: 1000000,
		},
		Usage: llm.Usage{TotalTokens: 50, CostVND: 20},
	}, nil
}

func (f *fakeBackend) DescribeProduct(_ context.Context, cc tools.CallContext, req service.DescribeProductRequest) (*service.DescribeProductResult, error) {
	f.lastCC = cc
	if f.describeErr != nil {
		return nil, f.describeErr
	}
	return &service.DescribeProductResult{
		TitleSuggestion: req.Name,
		Description:     "Mô tả hay",
		Highlights:      []string{"Cotton"},
		AIGenerated:     true,
	}, nil
}

func (f *fakeBackend) SuggestReply(_ context.Context, cc tools.CallContext, _ service.SuggestReplyRequest) (*service.SuggestReplyResult, error) {
	f.lastCC = cc
	return &service.SuggestReplyResult{
		Suggestions:       []service.ReplySuggestion{{ID: "s1", Text: "Dạ còn hàng ạ", Tone: "than_thien"}},
		InjectionDetected: true,
		AIGenerated:       true,
	}, nil
}

func (f *fakeBackend) GetReviewSummary(_ context.Context, productID string) (*service.ReviewSummaryResult, error) {
	return &service.ReviewSummaryResult{
		ProductID:          productID,
		Available:          true,
		ReviewCount:        12,
		MinReviewsRequired: 10,
		AverageRating:      4.8,
		Summary:            "Rất tốt",
		AIGenerated:        true,
		UpdatedAt:          time.Now().UTC(),
	}, nil
}

func (f *fakeBackend) RefreshReviewSummary(_ context.Context, productID, shopID string, _ []service.ReviewInput) (*service.ReviewSummaryResult, error) {
	return &service.ReviewSummaryResult{
		ProductID:   productID,
		ShopID:      shopID,
		Available:   true,
		ReviewCount: 10,
		AIGenerated: true,
	}, nil
}

func (f *fakeBackend) SubmitFeedback(_ context.Context, _ string, _ service.FeedbackRequest) error {
	f.feedbackSaved = true
	return nil
}

func TestGRPCServerNeverTrustsPlainHeaders(t *testing.T) {
	srv := New(&fakeBackend{})

	// Plain metadata headers x-user-id / x-user-role / x-shop-id MUST NOT authenticate!
	ctxWithSpoofedMD := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		"x-user-id", "spoofed-seller",
		"x-user-role", "seller",
		"x-shop-id", "shop-1",
	))
	if _, err := srv.SellerReport(ctxWithSpoofedMD, &assistantv1.SellerReportRequest{Question: "doanh thu"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated for spoofed headers, got %v", err)
	}
	if _, err := srv.Handoff(ctxWithSpoofedMD, &assistantv1.HandoffRequest{ShopId: "shop-1"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated for handoff without verified JWT, got %v", err)
	}
}

func TestGRPCServerVerifiedClaimsAndErrorMapping(t *testing.T) {
	backend := &fakeBackend{}
	srv := New(backend)

	claims := &auth.Claims{Role: "seller", ShopID: "shop-bach-dang"}
	claims.Subject = "seller-1"
	ctx := auth.WithRawToken(auth.WithClaims(context.Background(), claims), "Bearer real-jwt")

	rep, err := srv.SellerReport(ctx, &assistantv1.SellerReportRequest{Question: "doanh thu", Period: "7d"})
	if err != nil {
		t.Fatalf("SellerReport: %v", err)
	}
	if rep.GetShopId() != "shop-bach-dang" || backend.lastCC.RawBearer != "Bearer real-jwt" {
		t.Fatalf("unexpected report or CallContext: rep=%+v cc=%+v", rep, backend.lastCC)
	}

	// Cross-shop RefreshReviewSummary rejected for seller.
	if _, err := srv.RefreshReviewSummary(ctx, &assistantv1.RefreshReviewSummaryRequest{
		ProductId: "p1",
		ShopId:    "shop-other",
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for cross-shop refresh, got %v", err)
	}

	// Rate limit maps to ResourceExhausted.
	backend.sellerErr = &service.DomainError{Kind: service.ErrRateLimited, Code: "RATE_LIMITED", Message: "limit"}
	if _, err := srv.SellerReport(ctx, &assistantv1.SellerReportRequest{}); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("expected ResourceExhausted, got %v", err)
	}

	// Public GetReviewSummary works without auth.
	sum, err := srv.GetReviewSummary(context.Background(), &assistantv1.GetReviewSummaryRequest{ProductId: "p1"})
	if err != nil || !sum.GetAvailable() || sum.GetReviewCount() != 12 {
		t.Fatalf("GetReviewSummary failed: sum=%+v err=%v", sum, err)
	}

	// SubmitFeedback works.
	fb, err := srv.SubmitFeedback(ctx, &assistantv1.SubmitFeedbackRequest{ConversationId: "c1", Helpful: true})
	if err != nil || !fb.GetOk() || !backend.feedbackSaved {
		t.Fatalf("SubmitFeedback failed: fb=%+v err=%v", fb, err)
	}
}
