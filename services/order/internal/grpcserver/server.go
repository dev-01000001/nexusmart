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
