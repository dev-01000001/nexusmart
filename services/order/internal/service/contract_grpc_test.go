package service

import (
	"context"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	cartv1 "github.com/nexusmart/core/gen/go/nexusmart/cart/v1"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/order/internal/clients"
	"github.com/nexusmart/core/services/order/internal/config"
	"github.com/nexusmart/core/services/order/internal/events"
	"github.com/nexusmart/core/services/order/internal/store"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// --- Wire-level contract test servers mounted over bufconn ---

type wireAuthRecord struct {
	Method  string
	UserID  string
	Role    string
	ShopID  string
	Service string
	HasAuth bool
}

func recordAuthFromCtx(ctx context.Context, method string) wireAuthRecord {
	rec := wireAuthRecord{Method: method}
	if md, ok := metadata.FromIncomingContext(ctx); ok && len(md.Get(auth.MDAuthorization)) > 0 {
		rec.HasAuth = true
	}
	if c, ok := auth.ClaimsFromContext(ctx); ok {
		rec.UserID = c.UserID()
		rec.Role = c.Role
		rec.ShopID = c.ShopID
		rec.Service = c.Service
	}
	return rec
}

// paymentAuthInterceptor mirrors Rust PaymentService's real token verification:
//   - CreatePayment requires an end-user JWT (rejects service tokens & anonymous callers).
//   - GetPayment, CancelPayment, CreateRefund, ReleaseEscrow accept end-user JWT OR internal service token.
func paymentAuthInterceptor(users, svc *auth.Manager) grpc.UnaryServerInterceptor {
	serviceAllowedMethods := map[string]bool{
		"/nexusmart.payment.v1.PaymentService/GetPayment":    true,
		"/nexusmart.payment.v1.PaymentService/CancelPayment": true,
		"/nexusmart.payment.v1.PaymentService/CreateRefund":  true,
		"/nexusmart.payment.v1.PaymentService/ReleaseEscrow": true,
	}
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		var rawBearer string
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			if vals := md.Get(auth.MDAuthorization); len(vals) > 0 {
				rawBearer = strings.TrimSpace(vals[0])
			}
		}
		if rawBearer == "" {
			return nil, status.Error(codes.Unauthenticated, "authentication required")
		}
		if claims, err := users.VerifyAccessToken(rawBearer); err == nil {
			return handler(auth.WithRawToken(auth.WithClaims(ctx, claims), rawBearer), req)
		}
		if claims, err := svc.VerifyServiceToken(rawBearer); err == nil {
			if !serviceAllowedMethods[info.FullMethod] {
				return nil, status.Error(codes.PermissionDenied, "service identity not allowed for this payment RPC")
			}
			return handler(auth.WithClaims(ctx, claims), req)
		}
		return nil, status.Error(codes.Unauthenticated, "invalid token")
	}
}

type wirePaymentServer struct {
	paymentv1.UnimplementedPaymentServiceServer
	mu            sync.Mutex
	authLog       []wireAuthRecord
	createCalls   []*paymentv1.CreatePaymentRequest
	getCalls      []*paymentv1.GetPaymentRequest
	cancelCalls   []*paymentv1.CancelPaymentRequest
	refundCalls   []*paymentv1.CreateRefundRequest
	escrowCalls   []*paymentv1.ReleaseEscrowRequest
	createErr     error
	getErr        error
	refundErr     error
	getStatus     paymentv1.PaymentStatus
	refundsByIdem map[string]*paymentv1.Refund
}

func newWirePaymentServer() *wirePaymentServer {
	return &wirePaymentServer{
		getStatus:     paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING,
		refundsByIdem: make(map[string]*paymentv1.Refund),
	}
}

func (s *wirePaymentServer) CreatePayment(ctx context.Context, req *paymentv1.CreatePaymentRequest) (*paymentv1.CreatePaymentResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authLog = append(s.authLog, recordAuthFromCtx(ctx, "CreatePayment"))
	s.createCalls = append(s.createCalls, req)
	if s.createErr != nil {
		return nil, s.createErr
	}
	st := paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING
	qrURL := "https://qr.sandbox/vietqr/" + req.GetOrderGroupId()
	qrCode := "000201010212"
	provider := "vietqr_mock"
	switch req.GetMethod() {
	case paymentv1.PaymentMethod_PAYMENT_METHOD_COD:
		st = paymentv1.PaymentStatus_PAYMENT_STATUS_SUCCEEDED
		qrURL, qrCode, provider = "", "", "cod"
	case paymentv1.PaymentMethod_PAYMENT_METHOD_ZALOPAY:
		qrURL = "https://sb-openapi.zalopay.vn/openinapp?order=" + req.GetOrderGroupId()
		qrCode = "zp_token_" + req.GetOrderGroupId()
		provider = "zalopay_mock"
	}
	return &paymentv1.CreatePaymentResponse{
		Payment: &paymentv1.Payment{
			PaymentId:        "pay-wire-" + req.GetOrderGroupId(),
			OrderGroupId:     req.GetOrderGroupId(),
			OrderId:          req.GetOrderId(),
			UserId:           req.GetUserId(),
			AmountVnd:        req.GetAmountVnd(),
			Method:           req.GetMethod(),
			Status:           st,
			QrUrl:            qrURL,
			QrCode:           qrCode,
			Provider:         provider,
			ShopAllocations:  req.GetShopAllocations(),
			ExpiresAt:        req.GetExpiresAt(),
			SalesChannel:     req.GetSalesChannel(),
			StorefrontShopId: req.GetStorefrontShopId(),
		},
	}, nil
}

func (s *wirePaymentServer) GetPayment(ctx context.Context, req *paymentv1.GetPaymentRequest) (*paymentv1.GetPaymentResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authLog = append(s.authLog, recordAuthFromCtx(ctx, "GetPayment"))
	s.getCalls = append(s.getCalls, req)
	if s.getErr != nil {
		return nil, s.getErr
	}
	return &paymentv1.GetPaymentResponse{
		Payment: &paymentv1.Payment{
			PaymentId:    req.GetPaymentId(),
			OrderGroupId: req.GetOrderGroupId(),
			Status:       s.getStatus,
		},
	}, nil
}

func (s *wirePaymentServer) CancelPayment(ctx context.Context, req *paymentv1.CancelPaymentRequest) (*paymentv1.CancelPaymentResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authLog = append(s.authLog, recordAuthFromCtx(ctx, "CancelPayment"))
	s.cancelCalls = append(s.cancelCalls, req)
	return &paymentv1.CancelPaymentResponse{
		Payment: &paymentv1.Payment{
			PaymentId: req.GetPaymentId(),
			Status:    paymentv1.PaymentStatus_PAYMENT_STATUS_CANCELLED,
		},
	}, nil
}

func (s *wirePaymentServer) CreateRefund(ctx context.Context, req *paymentv1.CreateRefundRequest) (*paymentv1.CreateRefundResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authLog = append(s.authLog, recordAuthFromCtx(ctx, "CreateRefund"))
	s.refundCalls = append(s.refundCalls, req)
	if s.refundErr != nil {
		return nil, s.refundErr
	}
	if prev, ok := s.refundsByIdem[req.GetIdempotencyKey()]; ok {
		return &paymentv1.CreateRefundResponse{Refund: prev, AlreadyExisted: true}, nil
	}
	ref := &paymentv1.Refund{
		RefundId:       "ref-wire-" + req.GetOrderId(),
		PaymentId:      req.GetPaymentId(),
		OrderId:        req.GetOrderId(),
		AmountVnd:      req.GetAmountVnd(),
		Status:         paymentv1.RefundStatus_REFUND_STATUS_DONE,
		Reason:         req.GetReason(),
		IdempotencyKey: req.GetIdempotencyKey(),
	}
	s.refundsByIdem[req.GetIdempotencyKey()] = ref
	return &paymentv1.CreateRefundResponse{Refund: ref, AlreadyExisted: false}, nil
}

func (s *wirePaymentServer) ReleaseEscrow(ctx context.Context, req *paymentv1.ReleaseEscrowRequest) (*paymentv1.ReleaseEscrowResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authLog = append(s.authLog, recordAuthFromCtx(ctx, "ReleaseEscrow"))
	s.escrowCalls = append(s.escrowCalls, req)
	return &paymentv1.ReleaseEscrowResponse{Released: true, ReleasedAmountVnd: 950000}, nil
}

type wireShippingServer struct {
	shippingv1.UnimplementedShippingServiceServer
	mu                sync.Mutex
	authLog           []wireAuthRecord
	feeCalls          []*shippingv1.CalculateFeeRequest
	createCalls       []*shippingv1.CreateShipmentRequest
	createReturnCalls []*shippingv1.CreateReturnShipmentRequest
	cancelCalls       []*shippingv1.CancelShipmentRequest
	feeErr            error
	createErr         error
	createReturnErr   error
	quotes            []*shippingv1.CarrierQuote
}

func newWireShippingServer() *wireShippingServer {
	return &wireShippingServer{
		quotes: []*shippingv1.CarrierQuote{
			{CarrierCode: "ghtk", CarrierName: "GHTK", ServiceCode: "ROAD", FeeVnd: 25000, EstimatedDays: 3},
			{CarrierCode: "ghn", CarrierName: "GHN", ServiceCode: "standard", FeeVnd: 32000, EstimatedDays: 2},
		},
	}
}

func (s *wireShippingServer) CalculateFee(ctx context.Context, req *shippingv1.CalculateFeeRequest) (*shippingv1.CalculateFeeResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authLog = append(s.authLog, recordAuthFromCtx(ctx, "CalculateFee"))
	s.feeCalls = append(s.feeCalls, req)
	if s.feeErr != nil {
		return nil, s.feeErr
	}
	return &shippingv1.CalculateFeeResponse{Quotes: s.quotes}, nil
}

func (s *wireShippingServer) CreateShipment(ctx context.Context, req *shippingv1.CreateShipmentRequest) (*shippingv1.CreateShipmentResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authLog = append(s.authLog, recordAuthFromCtx(ctx, "CreateShipment"))
	s.createCalls = append(s.createCalls, req)
	if s.createErr != nil {
		return nil, s.createErr
	}
	return &shippingv1.CreateShipmentResponse{
		Shipment: &shippingv1.Shipment{
			ShipmentId:   "shp-wire-" + req.GetOrderId(),
			OrderId:      req.GetOrderId(),
			ShopId:       req.GetShopId(),
			CarrierCode:  req.GetCarrierCode(),
			ServiceCode:  req.GetServiceCode(),
			TrackingCode: "GHTK-WIRE-001",
			LabelUrl:     "https://sandbox.ghtk.local/labels/GHTK-WIRE-001.pdf",
			Status:       shippingv1.ShipmentStatus_SHIPMENT_STATUS_CREATED,
			Direction:    "forward",
			CodAmountVnd: req.GetCodAmountVnd(),
		},
	}, nil
}

func (s *wireShippingServer) CreateReturnShipment(ctx context.Context, req *shippingv1.CreateReturnShipmentRequest) (*shippingv1.CreateReturnShipmentResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := recordAuthFromCtx(ctx, "CreateReturnShipment")
	s.authLog = append(s.authLog, rec)
	s.createReturnCalls = append(s.createReturnCalls, req)
	// Enforce same RBAC as ShippingService: seller owning shop or admin/super_admin.
	if rec.Role != "admin" && rec.Role != "super_admin" && !(rec.Role == "seller" && rec.ShopID == req.GetShopId()) {
		return nil, status.Error(codes.PermissionDenied, "only owning seller or admin can create return shipment")
	}
	if s.createReturnErr != nil {
		return nil, s.createReturnErr
	}
	return &shippingv1.CreateReturnShipmentResponse{
		Shipment: &shippingv1.Shipment{
			ShipmentId:      "rshp-wire-" + req.GetReturnRequestId(),
			OrderId:         req.GetOrderId(),
			ShopId:          req.GetShopId(),
			CarrierCode:     req.GetCarrierCode(),
			ServiceCode:     req.GetServiceCode(),
			TrackingCode:    "RET-GHTK-999",
			LabelUrl:        "https://sandbox.ghtk.local/labels/RET-GHTK-999.pdf",
			Status:          shippingv1.ShipmentStatus_SHIPMENT_STATUS_CREATED,
			Direction:       "return",
			ReturnRequestId: req.GetReturnRequestId(),
		},
	}, nil
}

func (s *wireShippingServer) CancelShipment(ctx context.Context, req *shippingv1.CancelShipmentRequest) (*shippingv1.CancelShipmentResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authLog = append(s.authLog, recordAuthFromCtx(ctx, "CancelShipment"))
	s.cancelCalls = append(s.cancelCalls, req)
	return &shippingv1.CancelShipmentResponse{
		Shipment: &shippingv1.Shipment{
			OrderId: req.GetOrderId(),
			Status:  shippingv1.ShipmentStatus_SHIPMENT_STATUS_CANCELLED,
		},
	}, nil
}

type wirePromotionServer struct {
	promotionv1.UnimplementedPromotionServiceServer
	mu           sync.Mutex
	authLog      []wireAuthRecord
	previewCalls []*promotionv1.PreviewDiscountsRequest
	reserveCalls []*promotionv1.ReservePromotionRequest
	releaseCalls []*promotionv1.ReleasePromotionRequest
	flashCalls   int
	reserveErr   error
	breakdown    *promotionv1.DiscountBreakdown
	flashSales   []*promotionv1.FlashSale
}

func (s *wirePromotionServer) PreviewDiscounts(ctx context.Context, req *promotionv1.PreviewDiscountsRequest) (*promotionv1.PreviewDiscountsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authLog = append(s.authLog, recordAuthFromCtx(ctx, "PreviewDiscounts"))
	s.previewCalls = append(s.previewCalls, req)
	return &promotionv1.PreviewDiscountsResponse{Breakdown: s.breakdown}, nil
}

func (s *wirePromotionServer) ReservePromotion(ctx context.Context, req *promotionv1.ReservePromotionRequest) (*promotionv1.ReservePromotionResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authLog = append(s.authLog, recordAuthFromCtx(ctx, "ReservePromotion"))
	s.reserveCalls = append(s.reserveCalls, req)
	if s.reserveErr != nil {
		return nil, s.reserveErr
	}
	return &promotionv1.ReservePromotionResponse{Reserved: true, Breakdown: s.breakdown}, nil
}

func (s *wirePromotionServer) ReleasePromotion(ctx context.Context, req *promotionv1.ReleasePromotionRequest) (*promotionv1.ReleasePromotionResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authLog = append(s.authLog, recordAuthFromCtx(ctx, "ReleasePromotion"))
	s.releaseCalls = append(s.releaseCalls, req)
	return &promotionv1.ReleasePromotionResponse{Released: true}, nil
}

func (s *wirePromotionServer) ListActiveFlashSales(ctx context.Context, _ *promotionv1.ListActiveFlashSalesRequest) (*promotionv1.ListActiveFlashSalesResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authLog = append(s.authLog, recordAuthFromCtx(ctx, "ListActiveFlashSales"))
	s.flashCalls++
	return &promotionv1.ListActiveFlashSalesResponse{FlashSales: s.flashSales}, nil
}

type wireUserServer struct {
	userv1.UnimplementedUserServiceServer
	mock *mockUserClient
}

func (s *wireUserServer) ValidateUser(ctx context.Context, req *userv1.ValidateUserRequest) (*userv1.ValidateUserResponse, error) {
	return s.mock.ValidateUser(ctx, req)
}
func (s *wireUserServer) GetAddress(ctx context.Context, req *userv1.GetAddressRequest) (*userv1.GetAddressResponse, error) {
	return s.mock.GetAddress(ctx, req)
}
func (s *wireUserServer) GetShop(ctx context.Context, req *userv1.GetShopRequest) (*userv1.GetShopResponse, error) {
	return s.mock.GetShop(ctx, req)
}

type wireProductServer struct {
	productv1.UnimplementedProductServiceServer
	mock *mockProductClient
}

func (s *wireProductServer) GetSKUsBatch(ctx context.Context, req *productv1.GetSKUsBatchRequest) (*productv1.GetSKUsBatchResponse, error) {
	return s.mock.GetSKUsBatch(ctx, req)
}
func (s *wireProductServer) UpdateStock(ctx context.Context, req *productv1.UpdateStockRequest) (*productv1.UpdateStockResponse, error) {
	return s.mock.UpdateStock(ctx, req)
}
func (s *wireProductServer) ReserveStock(ctx context.Context, req *productv1.ReserveStockRequest) (*productv1.ReserveStockResponse, error) {
	return s.mock.ReserveStock(ctx, req)
}
func (s *wireProductServer) ReleaseStock(ctx context.Context, req *productv1.ReleaseStockRequest) (*productv1.ReleaseStockResponse, error) {
	return s.mock.ReleaseStock(ctx, req)
}
func (s *wireProductServer) ConfirmStock(ctx context.Context, req *productv1.ConfirmStockRequest) (*productv1.ConfirmStockResponse, error) {
	return s.mock.ConfirmStock(ctx, req)
}

type wireCartServer struct {
	cartv1.UnimplementedCartServiceServer
	mock *mockCartClient
}

func (s *wireCartServer) GetCart(ctx context.Context, req *cartv1.GetCartRequest) (*cartv1.GetCartResponse, error) {
	return s.mock.GetCart(ctx, req)
}
func (s *wireCartServer) ClearItems(ctx context.Context, req *cartv1.ClearItemsRequest) (*cartv1.ClearItemsResponse, error) {
	return s.mock.ClearItems(ctx, req)
}

type fullOrderGRPCAdapter struct {
	orderv1.UnimplementedOrderServiceServer
	svc *OrderService
}

func (a *fullOrderGRPCAdapter) CreateCheckout(ctx context.Context, r *orderv1.CreateCheckoutRequest) (*orderv1.CreateCheckoutResponse, error) {
	return a.svc.CreateCheckout(ctx, r)
}
func (a *fullOrderGRPCAdapter) GetOrder(ctx context.Context, r *orderv1.GetOrderRequest) (*orderv1.GetOrderResponse, error) {
	return a.svc.GetOrder(ctx, r)
}
func (a *fullOrderGRPCAdapter) ListOrders(ctx context.Context, r *orderv1.ListOrdersRequest) (*orderv1.ListOrdersResponse, error) {
	return a.svc.ListOrders(ctx, r)
}
func (a *fullOrderGRPCAdapter) CancelOrder(ctx context.Context, r *orderv1.CancelOrderRequest) (*orderv1.CancelOrderResponse, error) {
	return a.svc.CancelOrder(ctx, r)
}
func (a *fullOrderGRPCAdapter) SellerConfirmOrder(ctx context.Context, r *orderv1.SellerConfirmOrderRequest) (*orderv1.SellerConfirmOrderResponse, error) {
	return a.svc.SellerConfirmOrder(ctx, r)
}
func (a *fullOrderGRPCAdapter) SellerShipOrder(ctx context.Context, r *orderv1.SellerShipOrderRequest) (*orderv1.SellerShipOrderResponse, error) {
	return a.svc.SellerShipOrder(ctx, r)
}
func (a *fullOrderGRPCAdapter) BuyerConfirmReceived(ctx context.Context, r *orderv1.BuyerConfirmReceivedRequest) (*orderv1.BuyerConfirmReceivedResponse, error) {
	return a.svc.BuyerConfirmReceived(ctx, r)
}
func (a *fullOrderGRPCAdapter) RequestReturn(ctx context.Context, r *orderv1.RequestReturnRequest) (*orderv1.RequestReturnResponse, error) {
	return a.svc.RequestReturn(ctx, r)
}
func (a *fullOrderGRPCAdapter) ListOrderReturns(ctx context.Context, r *orderv1.ListOrderReturnsRequest) (*orderv1.ListOrderReturnsResponse, error) {
	return a.svc.ListOrderReturns(ctx, r)
}
func (a *fullOrderGRPCAdapter) ApproveReturn(ctx context.Context, r *orderv1.ApproveReturnRequest) (*orderv1.ApproveReturnResponse, error) {
	return a.svc.ApproveReturn(ctx, r)
}
func (a *fullOrderGRPCAdapter) RejectReturn(ctx context.Context, r *orderv1.RejectReturnRequest) (*orderv1.RejectReturnResponse, error) {
	return a.svc.RejectReturn(ctx, r)
}
func (a *fullOrderGRPCAdapter) ConfirmReturnReceived(ctx context.Context, r *orderv1.ConfirmReturnReceivedRequest) (*orderv1.ConfirmReturnReceivedResponse, error) {
	return a.svc.ConfirmReturnReceived(ctx, r)
}
func (a *fullOrderGRPCAdapter) CreateComplaint(ctx context.Context, r *orderv1.CreateComplaintRequest) (*orderv1.CreateComplaintResponse, error) {
	return a.svc.CreateComplaint(ctx, r)
}
func (a *fullOrderGRPCAdapter) ListComplaints(ctx context.Context, r *orderv1.ListComplaintsRequest) (*orderv1.ListComplaintsResponse, error) {
	return a.svc.ListComplaints(ctx, r)
}
func (a *fullOrderGRPCAdapter) ResolveComplaint(ctx context.Context, r *orderv1.ResolveComplaintRequest) (*orderv1.ResolveComplaintResponse, error) {
	return a.svc.ResolveComplaint(ctx, r)
}
func (a *fullOrderGRPCAdapter) GetOrderTimeline(ctx context.Context, r *orderv1.GetOrderTimelineRequest) (*orderv1.GetOrderTimelineResponse, error) {
	return a.svc.GetOrderTimeline(ctx, r)
}
func (a *fullOrderGRPCAdapter) MarkPaymentResult(ctx context.Context, r *orderv1.MarkPaymentResultRequest) (*orderv1.MarkPaymentResultResponse, error) {
	return a.svc.MarkPaymentResult(ctx, r)
}

type contractEnv struct {
	orderClient orderv1.OrderServiceClient
	orderSvc    *OrderService
	store       *store.MemoryStore
	bundle      *clients.Bundle
	userMgr     *auth.Manager
	svcMgr      *auth.Manager
	payment     *wirePaymentServer
	shipping    *wireShippingServer
	promotion   *wirePromotionServer
	product     *mockProductClient
}

func newContractEnv(t *testing.T) *contractEnv {
	t.Helper()
	userMgr, err := auth.NewManager("contract-user-secret-key", 15*time.Minute)
	if err != nil {
		t.Fatalf("NewManager user: %v", err)
	}
	svcMgr, err := auth.NewManager("contract-svc-secret-key", auth.ServiceTokenTTL)
	if err != nil {
		t.Fatalf("NewManager svc: %v", err)
	}

	startBufconn := func(interceptor grpc.UnaryServerInterceptor, reg func(*grpc.Server)) *grpc.ClientConn {
		lis := bufconn.Listen(1 << 20)
		gs := grpc.NewServer(grpc.ChainUnaryInterceptor(interceptor))
		reg(gs)
		go func() { _ = gs.Serve(lis) }()
		t.Cleanup(gs.Stop)

		conn, err := grpc.NewClient(
			"passthrough:///bufnet",
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithChainUnaryInterceptor(
				clients.TimeoutAndAuthClientInterceptor(5*time.Second, svcMgr),
				telemetry.GRPCUnaryClientInterceptor("order-contract-test"),
			),
		)
		if err != nil {
			t.Fatalf("dial bufconn: %v", err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		return conn
	}

	paySrv := newWirePaymentServer()
	shipSrv := newWireShippingServer()
	promoSrv := &wirePromotionServer{}
	uMock := newMockUserClient()
	pMock := newMockProductClient()
	cMock := newMockCartClient()

	stdAuth := auth.GRPCUnaryServerInterceptor(userMgr, svcMgr)

	payConn := startBufconn(paymentAuthInterceptor(userMgr, svcMgr), func(s *grpc.Server) {
		paymentv1.RegisterPaymentServiceServer(s, paySrv)
	})
	shipConn := startBufconn(stdAuth, func(s *grpc.Server) {
		shippingv1.RegisterShippingServiceServer(s, shipSrv)
	})
	promoConn := startBufconn(stdAuth, func(s *grpc.Server) {
		promotionv1.RegisterPromotionServiceServer(s, promoSrv)
	})
	userConn := startBufconn(stdAuth, func(s *grpc.Server) {
		userv1.RegisterUserServiceServer(s, &wireUserServer{mock: uMock})
	})
	prodConn := startBufconn(stdAuth, func(s *grpc.Server) {
		productv1.RegisterProductServiceServer(s, &wireProductServer{mock: pMock})
	})
	cartConn := startBufconn(stdAuth, func(s *grpc.Server) {
		cartv1.RegisterCartServiceServer(s, &wireCartServer{mock: cMock})
	})

	bundle := &clients.Bundle{
		User:      userv1.NewUserServiceClient(userConn),
		Product:   productv1.NewProductServiceClient(prodConn),
		Cart:      cartv1.NewCartServiceClient(cartConn),
		Promotion: promotionv1.NewPromotionServiceClient(promoConn),
		Payment:   paymentv1.NewPaymentServiceClient(payConn),
		Shipping:  shippingv1.NewShippingServiceClient(shipConn),
	}

	st := store.NewMemoryStore()
	cfg := config.Config{
		ServiceName:          "order",
		CODMaxVND:            5000000,
		CODMaxRejectedCount:  3,
		PaymentExpiryMinutes: 15,
		DefaultCommissionBPS: 500,
		RewardCoinsBPS:       100,
		GRPCCallTimeout:      5 * time.Second,
	}
	log := logger.New("order-contract", logger.WithWriter(io.Discard))
	svc := NewOrderService(cfg, st, bundle, log)

	// Mount OrderService itself on bufconn with real auth.GRPCUnaryServerInterceptor!
	ordLis := bufconn.Listen(1 << 20)
	ordGS := grpc.NewServer(grpc.ChainUnaryInterceptor(stdAuth))
	orderv1.RegisterOrderServiceServer(ordGS, &fullOrderGRPCAdapter{svc: svc})
	go func() { _ = ordGS.Serve(ordLis) }()
	t.Cleanup(ordGS.Stop)

	ordConn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return ordLis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial order bufconn: %v", err)
	}
	t.Cleanup(func() { _ = ordConn.Close() })

	return &contractEnv{
		orderClient: orderv1.NewOrderServiceClient(ordConn),
		orderSvc:    svc,
		store:       st,
		bundle:      bundle,
		userMgr:     userMgr,
		svcMgr:      svcMgr,
		payment:     paySrv,
		shipping:    shipSrv,
		promotion:   promoSrv,
		product:     pMock,
	}
}

func (e *contractEnv) bearerCtx(t *testing.T, userID, role, shopID string) context.Context {
	t.Helper()
	tok, _, _, err := e.userMgr.SignAccessToken(userID, role, shopID)
	if err != nil {
		t.Fatalf("SignAccessToken: %v", err)
	}
	return metadata.NewOutgoingContext(context.Background(), metadata.Pairs(auth.MDAuthorization, "Bearer "+tok))
}

func (e *contractEnv) serviceCtx(t *testing.T, serviceName string) context.Context {
	t.Helper()
	tok, err := e.svcMgr.SignServiceToken(serviceName)
	if err != nil {
		t.Fatalf("SignServiceToken: %v", err)
	}
	return metadata.NewOutgoingContext(context.Background(), metadata.Pairs(auth.MDAuthorization, "Bearer "+tok))
}

// TestContract_OrderToPayment_WireGRPC exercises CreatePayment (COD, VietQR, ZaloPay),
// GetPayment, CancelPayment, CreateRefund, ReleaseEscrow, JWT vs service-token propagation, and error mapping over real gRPC/bufconn.
func TestContract_OrderToPayment_WireGRPC(t *testing.T) {
	env := newContractEnv(t)
	buyerCtx := env.bearerCtx(t, "buyer-1", "buyer", "")
	sellerCtx := env.bearerCtx(t, "seller-1", "seller", "shop-1")
	svcCtx := env.serviceCtx(t, "payment-service")

	// 1. COD checkout -> CreatePayment receives buyer JWT + PAYMENT_METHOD_COD; BuyerConfirmReceived -> ReleaseEscrow receives service token!
	codResp, err := env.orderClient.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "wire-pay-cod-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	if err != nil {
		t.Fatalf("wire COD checkout failed: %v", err)
	}
	if len(env.payment.createCalls) != 1 || env.payment.createCalls[0].GetMethod() != paymentv1.PaymentMethod_PAYMENT_METHOD_COD {
		t.Fatalf("expected wire CreatePayment(COD), got %+v", env.payment.createCalls)
	}
	if rec := env.payment.authLog[0]; rec.Method != "CreatePayment" || rec.UserID != "buyer-1" || rec.Service != "" {
		t.Fatalf("CreatePayment must propagate buyer JWT (not service token), got %+v", rec)
	}

	codOrdID := codResp.GetOrders()[0].GetId()
	if _, err := env.orderClient.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: codOrdID}); err != nil {
		t.Fatalf("wire SellerShipOrder failed: %v", err)
	}
	if err := env.orderSvc.HandleShipmentUpdated(context.Background(), "evt-wire-cod-deliv", events.ShipmentUpdatedPayload{
		OrderID: codOrdID, Status: "delivered",
	}); err != nil {
		t.Fatalf("HandleShipmentUpdated failed: %v", err)
	}
	if _, err := env.orderClient.BuyerConfirmReceived(buyerCtx, &orderv1.BuyerConfirmReceivedRequest{OrderId: codOrdID}); err != nil {
		t.Fatalf("wire BuyerConfirmReceived failed: %v", err)
	}
	if len(env.payment.escrowCalls) != 1 || env.payment.escrowCalls[0].GetOrderId() != codOrdID {
		t.Fatalf("expected ReleaseEscrow over wire, got %+v", env.payment.escrowCalls)
	}
	lastAuth := env.payment.authLog[len(env.payment.authLog)-1]
	if lastAuth.Method != "ReleaseEscrow" || lastAuth.Service != clients.InternalServiceName {
		t.Fatalf("ReleaseEscrow must send internal order-service token, got %+v", lastAuth)
	}

	// 2. VietQR checkout + expiry watchdog -> GetPayment & CancelPayment over wire with service token!
	vqrResp, err := env.orderClient.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "wire-pay-vqr-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
	})
	if err != nil || vqrResp.GetPaymentMethod() != orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR {
		t.Fatalf("wire VietQR checkout failed: resp=%+v err=%v", vqrResp, err)
	}
	env.store.SetOrderPaymentExpiresAt(vqrResp.GetOrders()[0].GetId(), time.Now().UTC().Add(-time.Minute))
	env.payment.getStatus = paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING
	if n, err := env.orderSvc.ScanExpiredPendingOrders(context.Background()); err != nil || n != 1 {
		t.Fatalf("ScanExpiredPendingOrders VietQR: n=%d err=%v", n, err)
	}
	if len(env.payment.getCalls) != 1 || len(env.payment.cancelCalls) != 1 {
		t.Fatalf("expected wire GetPayment & CancelPayment calls, got get=%d cancel=%d", len(env.payment.getCalls), len(env.payment.cancelCalls))
	}

	// 3. ZaloPay checkout -> CreatePayment(ZALOPAY), MarkPaymentResult(SUCCEEDED) with service token,
	// then CancelOrder -> CreateRefund over wire with service token!
	zpResp, err := env.orderClient.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "wire-pay-zp-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY,
	})
	if err != nil {
		t.Fatalf("wire ZaloPay checkout failed: %v", err)
	}
	if zpResp.GetPaymentMethod() != orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY ||
		!strings.Contains(zpResp.GetQrUrl(), "zalopay") {
		t.Fatalf("unexpected ZaloPay response: %+v", zpResp)
	}
	if env.payment.createCalls[2].GetMethod() != paymentv1.PaymentMethod_PAYMENT_METHOD_ZALOPAY {
		t.Fatalf("expected PAYMENT_METHOD_ZALOPAY over wire, got %v", env.payment.createCalls[2].GetMethod())
	}

	if _, err := env.orderClient.MarkPaymentResult(svcCtx, &orderv1.MarkPaymentResultRequest{
		CheckoutGroupId: zpResp.GetCheckoutGroupId(),
		PaymentId:       zpResp.GetPaymentId(),
		Status:          orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_SUCCEEDED,
	}); err != nil {
		t.Fatalf("MarkPaymentResult ZaloPay failed: %v", err)
	}
	if _, err := env.orderClient.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{
		OrderId: zpResp.GetOrders()[0].GetId(),
		Reason:  "cancel paid zalopay",
	}); err != nil {
		t.Fatalf("CancelOrder paid ZaloPay failed: %v", err)
	}
	if len(env.payment.refundCalls) != 1 || env.payment.refundCalls[0].GetOrderId() != zpResp.GetOrders()[0].GetId() {
		t.Fatalf("expected wire CreateRefund for paid ZaloPay cancel, got %+v", env.payment.refundCalls)
	}

	// 4. Error code mapping: PaymentService Unavailable propagates codes.Unavailable and triggers compensation.
	env.payment.createErr = status.Error(codes.Unavailable, "payment backend unavailable")
	_, err = env.orderClient.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "wire-pay-err-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY,
	})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("expected codes.Unavailable propagated from PaymentService, got %v", err)
	}
}

// TestContract_OrderToShipping_WireGRPC exercises CalculateFee, CreateShipment, CreateReturnShipment,
// CancelShipment, JWT propagation, and error handling over real gRPC/bufconn.
func TestContract_OrderToShipping_WireGRPC(t *testing.T) {
	env := newContractEnv(t)
	buyerCtx := env.bearerCtx(t, "buyer-1", "buyer", "")
	sellerCtx := env.bearerCtx(t, "seller-1", "seller", "shop-1")

	// 1. Checkout -> CalculateFee over wire with buyer JWT (selects cheapest GHTK 25k quote).
	resp, err := env.orderClient.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "wire-ship-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	if err != nil {
		t.Fatalf("wire checkout failed: %v", err)
	}
	ord := resp.GetOrders()[0]
	if ord.GetCarrierCode() != "ghtk" || ord.GetShippingFeeVnd() != 25000 {
		t.Fatalf("expected ghtk/25000 from wire CalculateFee, got %s/%d", ord.GetCarrierCode(), ord.GetShippingFeeVnd())
	}
	if len(env.shipping.feeCalls) != 1 {
		t.Fatalf("expected 1 wire CalculateFee call, got %d", len(env.shipping.feeCalls))
	}

	// 2. SellerShipOrder -> CreateShipment over wire with seller JWT.
	shipResp, err := env.orderClient.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{
		OrderId: ord.GetId(),
	})
	if err != nil || shipResp.GetTrackingCode() != "GHTK-WIRE-001" {
		t.Fatalf("wire SellerShipOrder failed: resp=%+v err=%v", shipResp, err)
	}
	if len(env.shipping.createCalls) != 1 || env.shipping.createCalls[0].GetCodAmountVnd() != ord.GetTotalPayableVnd() {
		t.Fatalf("unexpected wire CreateShipment req: %+v", env.shipping.createCalls)
	}

	// Deliver -> buyer requests return -> seller approves -> CreateReturnShipment over wire with seller JWT!
	if err := env.orderSvc.HandleShipmentUpdated(context.Background(), "evt-wire-ship-deliv", events.ShipmentUpdatedPayload{
		OrderID: ord.GetId(), Status: "delivered", TrackingCode: shipResp.GetTrackingCode(),
	}); err != nil {
		t.Fatalf("HandleShipmentUpdated delivered failed: %v", err)
	}
	retResp, err := env.orderClient.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{
		OrderId: ord.GetId(),
		Reason:  "damaged box",
	})
	if err != nil {
		t.Fatalf("wire RequestReturn failed: %v", err)
	}
	appResp, err := env.orderClient.ApproveReturn(sellerCtx, &orderv1.ApproveReturnRequest{
		ReturnRequestId: retResp.GetReturnRequestId(),
		Note:            "please ship back",
	})
	if err != nil {
		t.Fatalf("wire ApproveReturn failed: %v", err)
	}
	if appResp.GetOrder().GetStatus() != orderv1.OrderStatus_ORDER_STATUS_RETURNING ||
		appResp.GetReturnRequest().GetReverseTrackingCode() != "RET-GHTK-999" {
		t.Fatalf("unexpected ApproveReturn response: %+v", appResp)
	}
	if len(env.shipping.createReturnCalls) != 1 {
		t.Fatalf("expected 1 wire CreateReturnShipment call, got %d", len(env.shipping.createReturnCalls))
	}
	retShipReq := env.shipping.createReturnCalls[0]
	if retShipReq.GetFromAddress().GetProvinceCode() != "79" || retShipReq.GetToAddress().GetProvinceCode() != "01" {
		t.Fatalf("reverse shipment addresses must be buyer(79)->shop(01), got from=%+v to=%+v",
			retShipReq.GetFromAddress(), retShipReq.GetToAddress())
	}

	// Verify seller JWT propagated to CreateReturnShipment.
	foundRetAuth := false
	for _, rec := range env.shipping.authLog {
		if rec.Method == "CreateReturnShipment" && rec.UserID == "seller-1" && rec.Role == "seller" && rec.ShopID == "shop-1" {
			foundRetAuth = true
		}
	}
	if !foundRetAuth {
		t.Fatalf("expected seller JWT propagated to Shipping.CreateReturnShipment, authLog=%+v", env.shipping.authLog)
	}

	// 3. Direct wire CancelShipment contract check with user JWT vs service token rejection.
	buyerTok, _, _, _ := env.userMgr.SignAccessToken("seller-1", "seller", "shop-1")
	outBuyerCtx := auth.WithRawToken(context.Background(), "Bearer "+buyerTok)
	cancelResp, err := env.bundle.Shipping.CancelShipment(outBuyerCtx, &shippingv1.CancelShipmentRequest{
		OrderId: ord.GetId(), Reason: "cancelled",
	})
	if err != nil || cancelResp.GetShipment().GetStatus() != shippingv1.ShipmentStatus_SHIPMENT_STATUS_CANCELLED {
		t.Fatalf("wire CancelShipment failed: resp=%+v err=%v", cancelResp, err)
	}
	if _, err := env.bundle.Shipping.CancelShipment(clients.WithInternalAdminContext(context.Background()), &shippingv1.CancelShipmentRequest{
		OrderId: ord.GetId(), Reason: "svc-not-allowed",
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service token on CancelShipment must be PermissionDenied, got %v", err)
	}

	// 4. Error handling: CalculateFee failure aborts checkout cleanly with zero stock reserved.
	env.shipping.feeErr = status.Error(codes.Unavailable, "all carriers down")
	prevReserve := len(env.product.reserveCalls)
	_, err = env.orderClient.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "wire-ship-fee-err",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	if status.Code(err) != codes.Unavailable && !strings.Contains(err.Error(), "all carriers down") {
		t.Fatalf("expected shipping fee error propagated, got %v", err)
	}
	if len(env.product.reserveCalls) != prevReserve {
		t.Fatalf("stock must not be reserved when shipping CalculateFee fails")
	}
}

// TestContract_OrderToPromotion_WireGRPC exercises PreviewDiscounts, ReservePromotion, ReleasePromotion,
// ListActiveFlashSales, JWT vs service-token propagation, and error mapping over real gRPC/bufconn.
func TestContract_OrderToPromotion_WireGRPC(t *testing.T) {
	env := newContractEnv(t)
	buyerCtx := env.bearerCtx(t, "buyer-1", "buyer", "")
	buyerTok, _, _, _ := env.userMgr.SignAccessToken("buyer-1", "buyer", "")
	outBuyerCtx := auth.WithRawToken(context.Background(), "Bearer "+buyerTok)

	env.promotion.flashSales = []*promotionv1.FlashSale{
		{
			Id: "fs-wire-1",
			Items: []*promotionv1.FlashSaleItem{
				{Id: "fsi-wire-1", SkuId: "sku-1", FlashPriceVnd: 750000},
			},
		},
	}
	env.promotion.breakdown = &promotionv1.DiscountBreakdown{
		Shops: []*promotionv1.ShopDiscountBreakdown{
			{
				ShopId:                     "shop-1",
				SubtotalVnd:                1000000,
				FlashSaleDiscountVnd:       250000,
				PlatformVoucherDiscountVnd: 50000,
				CoinsDiscountVnd:           10000,
				ShippingFeeVnd:             25000,
			},
		},
	}

	// 1. Direct wire PreviewDiscounts with buyer JWT succeeds; service token rejected with PermissionDenied.
	prevResp, err := env.bundle.Promotion.PreviewDiscounts(outBuyerCtx, &promotionv1.PreviewDiscountsRequest{
		UserId: "buyer-1",
	})
	if err != nil || prevResp.GetBreakdown() == nil {
		t.Fatalf("wire PreviewDiscounts failed: resp=%+v err=%v", prevResp, err)
	}
	if _, err := env.bundle.Promotion.PreviewDiscounts(clients.WithInternalAdminContext(context.Background()), &promotionv1.PreviewDiscountsRequest{
		UserId: "buyer-1",
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service token on PreviewDiscounts must be PermissionDenied, got %v", err)
	}

	// 2. Flash-sale VietQR checkout exercises ReservePromotion + ListActiveFlashSales over wire with buyer JWT!
	resp, err := env.orderClient.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey:    "wire-promo-1",
		AddressId:         "addr-buyer-1",
		Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1, FlashSaleItemId: "fsi-wire-1"}},
		PlatformVoucherId: "v-plat-wire",
		CoinsToUse:        10000,
		PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
	})
	if err != nil {
		t.Fatalf("wire promo checkout failed: %v", err)
	}
	if len(env.promotion.reserveCalls) != 1 || env.promotion.flashCalls != 1 {
		t.Fatalf("expected wire ReservePromotion(1) and ListActiveFlashSales(1), got reserve=%d flash=%d",
			len(env.promotion.reserveCalls), env.promotion.flashCalls)
	}
	if got := resp.GetOrders()[0].GetFlashSaleDiscountVnd(); got != 250000 {
		t.Fatalf("expected flash_sale_discount_vnd=250000 from wire breakdown, got %d", got)
	}

	// 3. Cancel pending order -> ReleasePromotion over wire with internal service token (accepted by auth.GRPCUnaryServerInterceptor!).
	if _, err := env.orderClient.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{
		OrderId: resp.GetOrders()[0].GetId(),
		Reason:  "release promo wire check",
	}); err != nil {
		t.Fatalf("CancelOrder failed: %v", err)
	}
	if len(env.promotion.releaseCalls) != 1 || env.promotion.releaseCalls[0].GetOrderId() != resp.GetCheckoutGroupId() {
		t.Fatalf("expected wire ReleasePromotion(%s), got %+v", resp.GetCheckoutGroupId(), env.promotion.releaseCalls)
	}
	lastPromoAuth := env.promotion.authLog[len(env.promotion.authLog)-1]
	if lastPromoAuth.Method != "ReleasePromotion" || lastPromoAuth.Service != clients.InternalServiceName {
		t.Fatalf("ReleasePromotion must use order-service internal token, got %+v", lastPromoAuth)
	}

	// 4. Error mapping: PromotionService ResourceExhausted propagates exact gRPC code to Order client.
	env.promotion.reserveErr = status.Error(codes.ResourceExhausted, "FLASH_SALE_SOLD_OUT")
	_, err = env.orderClient.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "wire-promo-exhausted",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1, FlashSaleItemId: "fsi-wire-1"}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("expected ResourceExhausted propagated from PromotionService, got %v", err)
	}
}
