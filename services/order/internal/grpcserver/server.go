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

func (s *Server) ListOrderReturns(ctx context.Context, req *orderv1.ListOrderReturnsRequest) (*orderv1.ListOrderReturnsResponse, error) {
	return s.svc.ListOrderReturns(ctx, req)
}

func (s *Server) ApproveReturn(ctx context.Context, req *orderv1.ApproveReturnRequest) (*orderv1.ApproveReturnResponse, error) {
	return s.svc.ApproveReturn(ctx, req)
}

func (s *Server) RejectReturn(ctx context.Context, req *orderv1.RejectReturnRequest) (*orderv1.RejectReturnResponse, error) {
	return s.svc.RejectReturn(ctx, req)
}

func (s *Server) ConfirmReturnReceived(ctx context.Context, req *orderv1.ConfirmReturnReceivedRequest) (*orderv1.ConfirmReturnReceivedResponse, error) {
	return s.svc.ConfirmReturnReceived(ctx, req)
}

func (s *Server) CreateComplaint(ctx context.Context, req *orderv1.CreateComplaintRequest) (*orderv1.CreateComplaintResponse, error) {
	return s.svc.CreateComplaint(ctx, req)
}

func (s *Server) ListComplaints(ctx context.Context, req *orderv1.ListComplaintsRequest) (*orderv1.ListComplaintsResponse, error) {
	return s.svc.ListComplaints(ctx, req)
}

func (s *Server) ResolveComplaint(ctx context.Context, req *orderv1.ResolveComplaintRequest) (*orderv1.ResolveComplaintResponse, error) {
	return s.svc.ResolveComplaint(ctx, req)
}

func (s *Server) GetOrderTimeline(ctx context.Context, req *orderv1.GetOrderTimelineRequest) (*orderv1.GetOrderTimelineResponse, error) {
	return s.svc.GetOrderTimeline(ctx, req)
}

func (s *Server) MarkPaymentResult(ctx context.Context, req *orderv1.MarkPaymentResultRequest) (*orderv1.MarkPaymentResultResponse, error) {
	return s.svc.MarkPaymentResult(ctx, req)
}
