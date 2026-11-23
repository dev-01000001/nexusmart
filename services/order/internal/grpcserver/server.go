package grpcserver

import (
	"context"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	"github.com/nexusmart/core/services/order/internal/service"
)

// Server implements orderv1.OrderServiceServer backed by OrderService.
type Server struct {
	orderv1.UnimplementedOrderServiceServer
	svc *service.OrderService
}

// New constructs a gRPC OrderServiceServer.
func New(svc *service.OrderService) *Server {
	return &Server{svc: svc}
}

func (s *Server) CreateCheckout(ctx context.Context, req *orderv1.CreateCheckoutRequest) (*orderv1.CreateCheckoutResponse, error) {
	return s.svc.CreateCheckout(ctx, req)
}

func (s *Server) GetOrder(ctx context.Context, req *orderv1.GetOrderRequest) (*orderv1.GetOrderResponse, error) {
	return s.svc.GetOrder(ctx, req)
}

func (s *Server) ListOrders(ctx context.Context, req *orderv1.ListOrdersRequest) (*orderv1.ListOrdersResponse, error) {
	return s.svc.ListOrders(ctx, req)
}

func (s *Server) CancelOrder(ctx context.Context, req *orderv1.CancelOrderRequest) (*orderv1.CancelOrderResponse, error) {
	return s.svc.CancelOrder(ctx, req)
}

func (s *Server) SellerConfirmOrder(ctx context.Context, req *orderv1.SellerConfirmOrderRequest) (*orderv1.SellerConfirmOrderResponse, error) {
	return s.svc.SellerConfirmOrder(ctx, req)
}

func (s *Server) SellerShipOrder(ctx context.Context, req *orderv1.SellerShipOrderRequest) (*orderv1.SellerShipOrderResponse, error) {
	return s.svc.SellerShipOrder(ctx, req)
}

func (s *Server) BuyerConfirmReceived(ctx context.Context, req *orderv1.BuyerConfirmReceivedRequest) (*orderv1.BuyerConfirmReceivedResponse, error) {
	return s.svc.BuyerConfirmReceived(ctx, req)
}

func (s *Server) RequestReturn(ctx context.Context, req *orderv1.RequestReturnRequest) (*orderv1.RequestReturnResponse, error) {
	return s.svc.RequestReturn(ctx, req)
}

func (s *Server) GetOrderTimeline(ctx context.Context, req *orderv1.GetOrderTimelineRequest) (*orderv1.GetOrderTimelineResponse, error) {
	return s.svc.GetOrderTimeline(ctx, req)
}

func (s *Server) MarkPaymentResult(ctx context.Context, req *orderv1.MarkPaymentResultRequest) (*orderv1.MarkPaymentResultResponse, error) {
	return s.svc.MarkPaymentResult(ctx, req)
}

func (s *Server) RetryPayment(ctx context.Context, req *orderv1.RetryPaymentRequest) (*orderv1.RetryPaymentResponse, error) {
	return s.svc.RetryPayment(ctx, req)
}

func (s *Server) GetReturnRequest(ctx context.Context, req *orderv1.GetReturnRequestRequest) (*orderv1.GetReturnRequestResponse, error) {
	return s.svc.GetReturnRequest(ctx, req)
}

func (s *Server) ListReturnRequests(ctx context.Context, req *orderv1.ListReturnRequestsRequest) (*orderv1.ListReturnRequestsResponse, error) {
	return s.svc.ListReturnRequests(ctx, req)
}

func (s *Server) RespondReturn(ctx context.Context, req *orderv1.RespondReturnRequest) (*orderv1.RespondReturnResponse, error) {
	return s.svc.RespondReturn(ctx, req)
}

func (s *Server) EscalateReturn(ctx context.Context, req *orderv1.EscalateReturnRequest) (*orderv1.EscalateReturnResponse, error) {
	return s.svc.EscalateReturn(ctx, req)
}

func (s *Server) AddReturnEvidence(ctx context.Context, req *orderv1.AddReturnEvidenceRequest) (*orderv1.AddReturnEvidenceResponse, error) {
	return s.svc.AddReturnEvidence(ctx, req)
}

func (s *Server) ConfirmReturnReceived(ctx context.Context, req *orderv1.ConfirmReturnReceivedRequest) (*orderv1.ConfirmReturnReceivedResponse, error) {
	return s.svc.ConfirmReturnReceived(ctx, req)
}

func (s *Server) DisputeReturnGoods(ctx context.Context, req *orderv1.DisputeReturnGoodsRequest) (*orderv1.DisputeReturnGoodsResponse, error) {
	return s.svc.DisputeReturnGoods(ctx, req)
}

func (s *Server) AdminResolveReturn(ctx context.Context, req *orderv1.AdminResolveReturnRequest) (*orderv1.AdminResolveReturnResponse, error) {
	return s.svc.AdminResolveReturn(ctx, req)
}
