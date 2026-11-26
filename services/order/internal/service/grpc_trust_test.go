package service

import (
	"context"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
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

type trustDownstreamStub struct {
	shippingv1.UnimplementedShippingServiceServer
	productv1.UnimplementedProductServiceServer
	promotionv1.UnimplementedPromotionServiceServer
	userv1.UnimplementedUserServiceServer

	mu                 sync.Mutex
	seenServiceMethods map[string]int
	seenUserMethods    map[string]int

	cancelShipErr      error
	getShipStatus      shippingv1.ShipmentStatus
	getShipTracking    string
	createRetSeq       int
	releaseStockResult bool
}

func newTrustDownstreamStub() *trustDownstreamStub {
	return &trustDownstreamStub{
		seenServiceMethods: make(map[string]int),
		seenUserMethods:    make(map[string]int),
		getShipStatus:      shippingv1.ShipmentStatus_SHIPMENT_STATUS_DELIVERED,
	}
}

func (s *trustDownstreamStub) requireServiceClaims(ctx context.Context, method string) error {
	c, ok := auth.ClaimsFromContext(ctx)
	if !ok || c.Service != clients.InternalServiceName || c.Role != auth.ServiceRole {
		return status.Errorf(codes.PermissionDenied, "%s expected order-service token, got claims=%+v", method, c)
	}
	s.mu.Lock()
	s.seenServiceMethods[method]++
	s.mu.Unlock()
	return nil
}

func (s *trustDownstreamStub) countService(method string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seenServiceMethods[method]
}

// --- ShippingService ---

func (s *trustDownstreamStub) GetShipment(ctx context.Context, req *shippingv1.GetShipmentRequest) (*shippingv1.GetShipmentResponse, error) {
	if err := s.requireServiceClaims(ctx, "Shipping.GetShipment"); err != nil {
		return nil, err
	}
	s.mu.Lock()
	st := s.getShipStatus
	trk := s.getShipTracking
	s.mu.Unlock()
	if trk == "" {
		trk = req.GetTrackingCode()
	}
	return &shippingv1.GetShipmentResponse{
		Shipment: &shippingv1.Shipment{
			ShipmentId:   "shp-ret-trust",
			OrderId:      req.GetOrderId(),
			TrackingCode: trk,
			Status:       st,
			Direction:    req.GetDirection(),
		},
	}, nil
}

func (s *trustDownstreamStub) CancelShipment(ctx context.Context, req *shippingv1.CancelShipmentRequest) (*shippingv1.CancelShipmentResponse, error) {
	if err := s.requireServiceClaims(ctx, "Shipping.CancelShipment"); err != nil {
		return nil, err
	}
	s.mu.Lock()
	err := s.cancelShipErr
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &shippingv1.CancelShipmentResponse{
		Shipment: &shippingv1.Shipment{OrderId: req.GetOrderId(), Status: shippingv1.ShipmentStatus_SHIPMENT_STATUS_CANCELLED},
	}, nil
}

func (s *trustDownstreamStub) CreateReturnShipment(ctx context.Context, req *shippingv1.CreateReturnShipmentRequest) (*shippingv1.CreateReturnShipmentResponse, error) {
	if err := s.requireServiceClaims(ctx, "Shipping.CreateReturnShipment"); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.createRetSeq++
	trk := "TRK-RET-TRUST-1"
	s.mu.Unlock()
	return &shippingv1.CreateReturnShipmentResponse{
		Shipment: &shippingv1.Shipment{
			ShipmentId:      "rshp-trust-1",
			OrderId:         req.GetOrderId(),
			ReturnRequestId: req.GetReturnRequestId(),
			CarrierCode:     "ghn",
			ServiceCode:     "standard",
			TrackingCode:    trk,
			LabelUrl:        "https://labels.sandbox/trust-ret-1",
			FeeVnd:          25000,
			Direction:       shippingv1.ShipmentDirection_SHIPMENT_DIRECTION_RETURN,
		},
	}, nil
}

func (s *trustDownstreamStub) CreateShipment(ctx context.Context, req *shippingv1.CreateShipmentRequest) (*shippingv1.CreateShipmentResponse, error) {
	c, ok := auth.ClaimsFromContext(ctx)
	if !ok || c.Service != "" {
		return nil, status.Error(codes.PermissionDenied, "end-user JWT required")
	}
	s.mu.Lock()
	s.seenUserMethods["Shipping.CreateShipment"]++
	s.mu.Unlock()
	return &shippingv1.CreateShipmentResponse{
		Shipment: &shippingv1.Shipment{
			ShipmentId:   "shp-fwd-trust-1",
			OrderId:      req.GetOrderId(),
			CarrierCode:  "ghn",
			ServiceCode:  "standard",
			TrackingCode: "TRK-FWD-TRUST-1",
			LabelUrl:     "https://labels.sandbox/trust-fwd-1",
		},
	}, nil
}

func (s *trustDownstreamStub) CalculateFee(ctx context.Context, _ *shippingv1.CalculateFeeRequest) (*shippingv1.CalculateFeeResponse, error) {
	c, ok := auth.ClaimsFromContext(ctx)
	if !ok || c.Service != "" {
		return nil, status.Error(codes.PermissionDenied, "end-user JWT required")
	}
	return &shippingv1.CalculateFeeResponse{
		Quotes: []*shippingv1.CarrierQuote{{CarrierCode: "ghn", ServiceCode: "standard", FeeVnd: 30000, CodSupported: true}},
	}, nil
}

func (s *trustDownstreamStub) GetShopShippingSettings(ctx context.Context, req *shippingv1.GetShopShippingSettingsRequest) (*shippingv1.GetShopShippingSettingsResponse, error) {
	if err := s.requireServiceClaims(ctx, "Shipping.GetShopShippingSettings"); err != nil {
		return nil, err
	}
	return &shippingv1.GetShopShippingSettingsResponse{
		Settings: &shippingv1.ShopShippingSettings{
			ShopId:             req.GetShopId(),
			DefaultWeightGrams: 500,
		},
	}, nil
}

// --- ProductService ---

func (s *trustDownstreamStub) GetSKUsBatch(ctx context.Context, _ *productv1.GetSKUsBatchRequest) (*productv1.GetSKUsBatchResponse, error) {
	if c, ok := auth.ClaimsFromContext(ctx); ok && c.Service != "" {
		return nil, status.Error(codes.PermissionDenied, "service token forbidden on public RPC")
	}
	return &productv1.GetSKUsBatchResponse{
		Skus: []*productv1.SKU{
			{Id: "sku-1", ProductId: "prod-1", ShopId: "shop-1", ProductName: "Item A", Name: "Default", PriceVnd: 100000, Stock: 10, AvailableStock: 10, WeightGrams: 500, CommissionRateBps: 500, IsActive: true},
		},
	}, nil
}

func (s *trustDownstreamStub) ReserveStock(ctx context.Context, _ *productv1.ReserveStockRequest) (*productv1.ReserveStockResponse, error) {
	if c, ok := auth.ClaimsFromContext(ctx); !ok || c.Service != "" {
		return nil, status.Error(codes.PermissionDenied, "end-user JWT required")
	}
	return &productv1.ReserveStockResponse{Reserved: true}, nil
}

func (s *trustDownstreamStub) ConfirmStock(ctx context.Context, _ *productv1.ConfirmStockRequest) (*productv1.ConfirmStockResponse, error) {
	if err := s.requireServiceClaims(ctx, "Product.ConfirmStock"); err != nil {
		return nil, err
	}
	return &productv1.ConfirmStockResponse{Confirmed: true}, nil
}

func (s *trustDownstreamStub) ReleaseStock(ctx context.Context, _ *productv1.ReleaseStockRequest) (*productv1.ReleaseStockResponse, error) {
	if err := s.requireServiceClaims(ctx, "Product.ReleaseStock"); err != nil {
		return nil, err
	}
	s.mu.Lock()
	rel := s.releaseStockResult
	s.mu.Unlock()
	return &productv1.ReleaseStockResponse{Released: rel}, nil
}

func (s *trustDownstreamStub) RestockOrderItems(ctx context.Context, _ *productv1.RestockOrderItemsRequest) (*productv1.RestockOrderItemsResponse, error) {
	if err := s.requireServiceClaims(ctx, "Product.RestockOrderItems"); err != nil {
		return nil, err
	}
	return &productv1.RestockOrderItemsResponse{Restocked: true}, nil
}

// --- PromotionService ---

func (s *trustDownstreamStub) ReservePromotion(ctx context.Context, req *promotionv1.ReservePromotionRequest) (*promotionv1.ReservePromotionResponse, error) {
	// D3: ReservePromotion accepts ONLY order-service's internal service token (not buyer/admin JWT or other services)
	// and requires non-empty user_id from request.
	if err := s.requireServiceClaims(ctx, "Promotion.ReservePromotion"); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.GetUserId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	return &promotionv1.ReservePromotionResponse{Reserved: true}, nil
}

func (s *trustDownstreamStub) ReleasePromotion(ctx context.Context, _ *promotionv1.ReleasePromotionRequest) (*promotionv1.ReleasePromotionResponse, error) {
	if err := s.requireServiceClaims(ctx, "Promotion.ReleasePromotion"); err != nil {
		return nil, err
	}
	return &promotionv1.ReleasePromotionResponse{Released: true}, nil
}

func (s *trustDownstreamStub) ReleasePromotionShop(ctx context.Context, _ *promotionv1.ReleasePromotionShopRequest) (*promotionv1.ReleasePromotionShopResponse, error) {
	if err := s.requireServiceClaims(ctx, "Promotion.ReleasePromotionShop"); err != nil {
		return nil, err
	}
	return &promotionv1.ReleasePromotionShopResponse{Released: true}, nil
}

func (s *trustDownstreamStub) ListActiveShopDiscounts(ctx context.Context, _ *promotionv1.ListActiveShopDiscountsRequest) (*promotionv1.ListActiveShopDiscountsResponse, error) {
	if c, ok := auth.ClaimsFromContext(ctx); ok && c.Service != "" {
		return nil, status.Error(codes.PermissionDenied, "service token forbidden on public RPC")
	}
	return &promotionv1.ListActiveShopDiscountsResponse{}, nil
}

// --- UserService ---

func (s *trustDownstreamStub) ValidateUser(ctx context.Context, _ *userv1.ValidateUserRequest) (*userv1.ValidateUserResponse, error) {
	if c, ok := auth.ClaimsFromContext(ctx); !ok || c.Service != "" {
		return nil, status.Error(codes.PermissionDenied, "end-user JWT required")
	}
	return &userv1.ValidateUserResponse{Valid: true, Status: userv1.UserStatus_USER_STATUS_ACTIVE, PhoneVerified: true}, nil
}

func (s *trustDownstreamStub) GetShopInternal(ctx context.Context, req *userv1.GetShopInternalRequest) (*userv1.GetShopInternalResponse, error) {
	if err := s.requireServiceClaims(ctx, "User.GetShopInternal"); err != nil {
		return nil, err
	}
	return &userv1.GetShopInternalResponse{
		Shop: &userv1.Shop{
			Id:              req.GetShopId(),
			Name:            "Trust Shop",
			ContactPhone:    "0901234567",
			OwnerUserId:     "seller-1",
			PickupAddressId: "addr-shop-1",
			Status:          userv1.ShopStatus_SHOP_STATUS_ACTIVE,
		},
	}, nil
}

func (s *trustDownstreamStub) GetAddress(ctx context.Context, req *userv1.GetAddressRequest) (*userv1.GetAddressResponse, error) {
	if c, ok := auth.ClaimsFromContext(ctx); ok && c.Service != "" {
		if err := s.requireServiceClaims(ctx, "User.GetAddress"); err != nil {
			return nil, err
		}
	}
	return &userv1.GetAddressResponse{
		Address: &userv1.Address{
			Id:             req.GetAddressId(),
			UserId:         req.GetUserId(),
			RecipientName:  "User",
			RecipientPhone: "0901234567",
			ProvinceCode:   "79",
			WardCode:       "79001",
			StreetAddress:  "123 Trust St",
		},
	}, nil
}

func (s *trustDownstreamStub) RecordCODRejection(ctx context.Context, _ *userv1.RecordCODRejectionRequest) (*userv1.RecordCODRejectionResponse, error) {
	if err := s.requireServiceClaims(ctx, "User.RecordCODRejection"); err != nil {
		return nil, err
	}
	return &userv1.RecordCODRejectionResponse{}, nil
}

// TestGRPCTrust_OrderJobsAndConsumersRealAuthInterceptor exercises OrderService's real
// client auth interceptor (clients.AuthClientInterceptor) against the real server
// auth.GRPCUnaryServerInterceptor(users, svc) over bufconn for every Go internal RPC
// invoked by order's watchdogs and event consumers:
//   - Shipping.GetShipment (from watchdog ScanReturnDeadlines + AdminResolveReturn)
//   - Shipping.CancelShipment (FORWARD & RETURN)
//   - Shipping.CreateReturnShipment (+ User.GetShop anonymous + User.GetAddress service token)
//   - Product.ConfirmStock, Product.ReleaseStock, Product.RestockOrderItems
//   - Promotion.ReleasePromotion, Promotion.ReleasePromotionShop
//   - User.RecordCODRejection
//
// Plus negative trust checks: anonymous/forged-metadata rejected with Unauthenticated,
// wrong/expired service tokens rejected with Unauthenticated, and service tokens
// rejected with PermissionDenied on public / end-user-only RPCs.
func TestGRPCTrust_OrderJobsAndConsumersRealAuthInterceptor(t *testing.T) {
	users, err := auth.NewManager("order-trust-user-key", 15*time.Minute)
	if err != nil {
		t.Fatalf("NewManager(users): %v", err)
	}
	svcMgr, err := auth.NewManager("order-trust-service-key", auth.ServiceTokenTTL)
	if err != nil {
		t.Fatalf("NewManager(svc): %v", err)
	}

	stub := newTrustDownstreamStub()
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer(grpc.ChainUnaryInterceptor(auth.GRPCUnaryServerInterceptor(users, svcMgr)))
	shippingv1.RegisterShippingServiceServer(gs, stub)
	productv1.RegisterProductServiceServer(gs, stub)
	promotionv1.RegisterPromotionServiceServer(gs, stub)
	userv1.RegisterUserServiceServer(gs, stub)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	// Dial with OrderService's REAL AuthClientInterceptor (signs service tokens or forwards user JWT).
	authedConn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(clients.AuthClientInterceptor(5*time.Second, svcMgr)),
	)
	if err != nil {
		t.Fatalf("dial authed bufconn: %v", err)
	}
	t.Cleanup(func() { _ = authedConn.Close() })

	// Raw connection (no client interceptor) for negative auth checks against real server interceptor.
	rawConn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial raw bufconn: %v", err)
	}
	t.Cleanup(func() { _ = rawConn.Close() })

	mem := store.NewMemoryStore()
	payMock := &mockPaymentClient{}
	methodCfgs := clients.NewFakePaymentMethodConfigSource(nil)
	methodCfgs.SetConfig(clients.PaymentMethodConfig{
		Method:        "COD",
		Enabled:       true,
		MinOrderVND:   0,
		MaxOrderVND:   20000000,
		ExpiryMinutes: 0,
	})
	bundle := &clients.Bundle{
		User:          userv1.NewUserServiceClient(authedConn),
		Product:       productv1.NewProductServiceClient(authedConn),
		Promotion:     promotionv1.NewPromotionServiceClient(authedConn),
		Shipping:      shippingv1.NewShippingServiceClient(authedConn),
		Payment:       payMock,
		MethodConfigs: methodCfgs,
	}
	cfg := config.Config{
		ServiceName:              "order",
		GRPCCallTimeout:          5 * time.Second,
		CODMaxRejectedCount:      3,
		CODRequirePhoneVerified:  true,
		CODMinOrdersForRate:      3,
		CODMaxRejectRateBPS:      3000,
		RewardCoinsBPS:           100,
		ReturnSellerResponseDays: 2,
		ReturnShipDays:           5,
		ReturnInspectDays:        3,
		SellerConfirmHours:       48,
		AutoCompleteDays:         7,
		StaleCheckoutAfter:       time.Minute,
	}
	svc := NewOrderService(cfg, mem, bundle, logger.New("order-trust-test", logger.WithWriter(io.Discard)))
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	svc.nowFn = func() time.Time { return now }

	signUserCtx := func(userID, role, shopID string) context.Context {
		tok, _, _, sErr := users.SignAccessToken(userID, role, shopID)
		if sErr != nil {
			t.Fatalf("SignAccessToken: %v", sErr)
		}
		claims, _ := users.VerifyAccessToken(tok)
		return auth.WithRawToken(auth.WithClaims(context.Background(), claims), "Bearer "+tok)
	}
	buyerCtx := signUserCtx("buyer-1", "buyer", "")
	sellerCtx := signUserCtx("seller-1", "seller", "shop-1")
	adminCtx := signUserCtx("admin-1", "admin", "")

	// 1. Negative trust checks on the real GRPCUnaryServerInterceptor.
	t.Run("NegativeTrust_AnonymousForgedWrongKeyAndServiceTokenBoundaries", func(t *testing.T) {
		rawShip := shippingv1.NewShippingServiceClient(rawConn)
		rawProd := productv1.NewProductServiceClient(rawConn)
		rawPromo := promotionv1.NewPromotionServiceClient(rawConn)
		rawUser := userv1.NewUserServiceClient(rawConn)

		forgedMD := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(
			"x-user-id", "order-service", "x-user-role", "admin", "x-shop-id", "shop-1",
		))
		wrongKey, _ := auth.NewManager("wrong-service-key", auth.ServiceTokenTTL)
		wrongTok, _ := wrongKey.SignServiceToken("order-service")
		wrongCtx := auth.InjectOutgoingGRPC(context.Background(), wrongTok)

		expiredMgr, _ := auth.NewManager("order-trust-service-key", time.Millisecond)
		expiredTok, _ := expiredMgr.SignServiceToken("order-service")
		time.Sleep(10 * time.Millisecond)
		expiredCtx := auth.InjectOutgoingGRPC(context.Background(), expiredTok)

		goodSvcTok, _ := svcMgr.SignServiceToken("order-service")
		goodSvcCtx := auth.InjectOutgoingGRPC(context.Background(), goodSvcTok)

		// All internal Go RPCs MUST reject anonymous / forged metadata AND wrong-key tokens with Unauthenticated.
		internalCalls := map[string]func(context.Context) error{
			"Shipping.GetShipment": func(c context.Context) error {
				_, e := rawShip.GetShipment(c, &shippingv1.GetShipmentRequest{OrderId: "o1", Direction: shippingv1.ShipmentDirection_SHIPMENT_DIRECTION_RETURN})
				return e
			},
			"Shipping.CancelShipment": func(c context.Context) error {
				_, e := rawShip.CancelShipment(c, &shippingv1.CancelShipmentRequest{OrderId: "o1"})
				return e
			},
			"Shipping.CreateReturnShipment": func(c context.Context) error {
				_, e := rawShip.CreateReturnShipment(c, &shippingv1.CreateReturnShipmentRequest{OrderId: "o1"})
				return e
			},
			"Product.ConfirmStock": func(c context.Context) error {
				_, e := rawProd.ConfirmStock(c, &productv1.ConfirmStockRequest{OrderId: "g1"})
				return e
			},
			"Product.ReleaseStock": func(c context.Context) error {
				_, e := rawProd.ReleaseStock(c, &productv1.ReleaseStockRequest{OrderId: "g1"})
				return e
			},
			"Product.RestockOrderItems": func(c context.Context) error {
				_, e := rawProd.RestockOrderItems(c, &productv1.RestockOrderItemsRequest{OrderId: "o1"})
				return e
			},
			"Promotion.ReservePromotion": func(c context.Context) error {
				_, e := rawPromo.ReservePromotion(c, &promotionv1.ReservePromotionRequest{OrderId: "g1", UserId: "buyer-1"})
				return e
			},
			"Promotion.ReleasePromotion": func(c context.Context) error {
				_, e := rawPromo.ReleasePromotion(c, &promotionv1.ReleasePromotionRequest{OrderId: "g1"})
				return e
			},
			"Promotion.ReleasePromotionShop": func(c context.Context) error {
				_, e := rawPromo.ReleasePromotionShop(c, &promotionv1.ReleasePromotionShopRequest{OrderId: "g1", ShopId: "shop-1"})
				return e
			},
			"User.GetShopInternal": func(c context.Context) error {
				_, e := rawUser.GetShopInternal(c, &userv1.GetShopInternalRequest{ShopId: "shop-1"})
				return e
			},
			"User.RecordCODRejection": func(c context.Context) error {
				_, e := rawUser.RecordCODRejection(c, &userv1.RecordCODRejectionRequest{UserId: "buyer-1", OrderId: "o1"})
				return e
			},
		}
		for name, fn := range internalCalls {
			if e := fn(forgedMD); status.Code(e) != codes.Unauthenticated {
				t.Fatalf("%s with forged metadata: got %v, want Unauthenticated", name, e)
			}
			if e := fn(wrongCtx); status.Code(e) != codes.Unauthenticated {
				t.Fatalf("%s with wrong service key: got %v, want Unauthenticated", name, e)
			}
			if e := fn(expiredCtx); status.Code(e) != codes.Unauthenticated {
				t.Fatalf("%s with expired service token: got %v, want Unauthenticated", name, e)
			}
		}

		// Public RPC (User.GetShop) and user-only false RPCs MUST reject service tokens with PermissionDenied.
		if _, e := rawUser.GetShop(goodSvcCtx, &userv1.GetShopRequest{ShopId: "shop-1"}); status.Code(e) != codes.PermissionDenied {
			t.Fatalf("User.GetShop with service token: got %v, want PermissionDenied", e)
		}
		if _, e := rawShip.CreateShipment(goodSvcCtx, &shippingv1.CreateShipmentRequest{OrderId: "o1"}); status.Code(e) != codes.PermissionDenied {
			t.Fatalf("Shipping.CreateShipment with service token: got %v, want PermissionDenied", e)
		}
		if _, e := rawProd.ReserveStock(goodSvcCtx, &productv1.ReserveStockRequest{OrderId: "g1"}); status.Code(e) != codes.PermissionDenied {
			t.Fatalf("Product.ReserveStock with service token: got %v, want PermissionDenied", e)
		}

		// D3: Promotion.ReservePromotion MUST accept order-service token (with user_id),
		// and reject buyer JWT, admin JWT, and other service tokens with PermissionDenied.
		buyerTok, _, _, _ := users.SignAccessToken("buyer-1", "buyer", "")
		adminTok, _, _, _ := users.SignAccessToken("admin-1", "admin", "")
		otherSvcTok, _ := svcMgr.SignServiceToken("payment-service")
		for label, ctx := range map[string]context.Context{
			"buyer JWT":           auth.InjectOutgoingGRPC(context.Background(), buyerTok),
			"admin JWT":           auth.InjectOutgoingGRPC(context.Background(), adminTok),
			"other service token": auth.InjectOutgoingGRPC(context.Background(), otherSvcTok),
		} {
			if _, e := rawPromo.ReservePromotion(ctx, &promotionv1.ReservePromotionRequest{OrderId: "g1", UserId: "buyer-1"}); status.Code(e) != codes.PermissionDenied {
				t.Fatalf("Promotion.ReservePromotion with %s: got %v, want PermissionDenied", label, e)
			}
		}
		if _, e := rawPromo.ReservePromotion(goodSvcCtx, &promotionv1.ReservePromotionRequest{OrderId: "g1", UserId: "buyer-1"}); e != nil {
			t.Fatalf("Promotion.ReservePromotion with order-service token: got %v, want nil", e)
		}
		// User.GetShopInternal MUST reject end-user JWTs and accept service tokens.
		for label, uCtx := range map[string]context.Context{
			"buyer JWT": auth.InjectOutgoingGRPC(context.Background(), buyerTok),
			"admin JWT": auth.InjectOutgoingGRPC(context.Background(), adminTok),
		} {
			if _, e := rawUser.GetShopInternal(uCtx, &userv1.GetShopInternalRequest{ShopId: "shop-1"}); status.Code(e) != codes.PermissionDenied {
				t.Fatalf("User.GetShopInternal with %s: got %v, want PermissionDenied", label, e)
			}
		}
		if _, e := rawUser.GetShopInternal(goodSvcCtx, &userv1.GetShopInternalRequest{ShopId: "shop-1"}); e != nil {
			t.Fatalf("User.GetShopInternal with order-service token: got %v, want nil", e)
		}
	})

	// 2. Watchdog GetShipment + CancelShipment(RETURN) + CreateReturnShipment (+ User.GetShopInternal & User.GetAddress service tokens).
	t.Run("Watchdog_GetShipment_CancelShipment_CreateReturnShipment", func(t *testing.T) {
		co, err := svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "trust-ret-1",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		ordID := co.Orders[0].Id
		shipResp, err := svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID, ShopId: "shop-1"})
		if err != nil {
			t.Fatalf("SellerShipOrder: %v", err)
		}
		if err := svc.HandleShipmentUpdated(context.Background(), "evt-trust-deliv-1", events.ShipmentUpdatedPayload{
			OrderID: ordID, TrackingCode: shipResp.TrackingCode, Status: "delivered",
		}); err != nil {
			t.Fatalf("HandleShipmentUpdated(delivered): %v", err)
		}
		retResp, err := svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ordID, Reason: "defect"})
		if err != nil {
			t.Fatalf("RequestReturn: %v", err)
		}
		retID := retResp.ReturnRequestId

		// Auto-accept PENDING via watchdog (context.Background() — NO user JWT!) -> calls User.GetShopInternal (service token),
		// User.GetAddress (service token), Shipping.CreateReturnShipment (service token)!
		beforeWatchdogShops := stub.countService("User.GetShopInternal")
		if beforeWatchdogShops < 2 {
			t.Fatalf("expected CreateCheckout + SellerShipOrder to call User.GetShopInternal with service token, got %d", beforeWatchdogShops)
		}
		now = now.Add(49 * time.Hour)
		if n, err := svc.ScanReturnDeadlines(context.Background()); err != nil || n != 1 {
			t.Fatalf("ScanReturnDeadlines(PENDING auto-accept) = %d, %v; want 1, nil", n, err)
		}
		gotRet, _ := svc.GetReturnRequest(buyerCtx, &orderv1.GetReturnRequestRequest{ReturnRequestId: retID})
		if gotRet.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT || gotRet.ReturnRequest.ReturnTrackingCode == "" {
			t.Fatalf("watchdog CreateReturnShipment failed: status=%v tracking=%q", gotRet.ReturnRequest.Status, gotRet.ReturnRequest.ReturnTrackingCode)
		}
		if stub.countService("Shipping.CreateReturnShipment") < 1 || stub.countService("User.GetAddress") < 1 {
			t.Fatalf("expected service-token CreateReturnShipment & GetAddress, got %+v", stub.seenServiceMethods)
		}
		if stub.countService("User.GetShopInternal") <= beforeWatchdogShops {
			t.Fatalf("expected watchdog to call User.GetShopInternal with service token (%d -> %d)", beforeWatchdogShops, stub.countService("User.GetShopInternal"))
		}

		// Now expire AWAITING_SHIPMENT with CancelShipment -> FailedPrecondition so watchdog calls GetShipment(RETURN)
		// from context.Background() (NO user JWT!). MUST send service token and sync status to RECEIVED!
		stub.mu.Lock()
		stub.cancelShipErr = status.Error(codes.FailedPrecondition, "already delivered")
		stub.getShipStatus = shippingv1.ShipmentStatus_SHIPMENT_STATUS_DELIVERED
		stub.getShipTracking = gotRet.ReturnRequest.ReturnTrackingCode
		stub.mu.Unlock()
		t.Cleanup(func() {
			stub.mu.Lock()
			stub.cancelShipErr = nil
			stub.mu.Unlock()
		})

		now = now.Add(5*24*time.Hour + time.Minute)
		if _, err := svc.ScanReturnDeadlines(context.Background()); err != nil {
			t.Fatalf("ScanReturnDeadlines(AWAITING_SHIPMENT sync): %v", err)
		}
		gotRet, _ = svc.GetReturnRequest(buyerCtx, &orderv1.GetReturnRequestRequest{ReturnRequestId: retID})
		if gotRet.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_RECEIVED {
			t.Fatalf("watchdog GetShipment sync did not transition return to RECEIVED (status=%v); check GetShipment service token!", gotRet.ReturnRequest.Status)
		}
		if stub.countService("Shipping.CancelShipment") < 1 || stub.countService("Shipping.GetShipment") < 1 {
			t.Fatalf("expected service-token CancelShipment & GetShipment from watchdog, got %+v", stub.seenServiceMethods)
		}

		// Also test AdminResolveReturn(RETURN_AND_REFUND) calling GetShipment with service token
		// when escalated from failed/lost (GoodsReceived == false, so GetShipment is invoked).
		coAdm, err := svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "trust-ret-adm-getship",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil {
			t.Fatalf("CreateCheckout adm: %v", err)
		}
		ordAdmID := coAdm.Orders[0].Id
		shipAdm, _ := svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordAdmID, ShopId: "shop-1"})
		_ = svc.HandleShipmentUpdated(context.Background(), "evt-trust-adm-deliv", events.ShipmentUpdatedPayload{
			OrderID: ordAdmID, TrackingCode: shipAdm.TrackingCode, Status: "delivered",
		})
		retAdmResp, _ := svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ordAdmID, Reason: "defect"})
		retAdmID := retAdmResp.ReturnRequestId
		accAdm, _ := svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: retAdmID, Decision: orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_RETURN,
		})
		_ = svc.HandleShipmentUpdated(context.Background(), "evt-trust-adm-lost", events.ShipmentUpdatedPayload{
			OrderID: ordAdmID, Direction: "RETURN", ReturnRequestID: retAdmID,
			TrackingCode: accAdm.ReturnRequest.ReturnTrackingCode, Status: "failed", RawStatus: "lost",
		})
		stub.mu.Lock()
		stub.getShipStatus = shippingv1.ShipmentStatus_SHIPMENT_STATUS_FAILED
		stub.getShipTracking = accAdm.ReturnRequest.ReturnTrackingCode
		stub.mu.Unlock()
		beforeGetShip := stub.countService("Shipping.GetShipment")
		if _, err := svc.AdminResolveReturn(adminCtx, &orderv1.AdminResolveReturnRequest{
			ReturnRequestId: retAdmID, Decision: orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_RETURN_AND_REFUND,
		}); status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("AdminResolveReturn(RETURN_AND_REFUND) on failed shipment = %v, want FailedPrecondition", err)
		}
		if stub.countService("Shipping.GetShipment") <= beforeGetShip {
			t.Fatalf("AdminResolveReturn MUST call Shipping.GetShipment with service token, counts=%+v", stub.seenServiceMethods)
		}
	})

	// 3. Consumer & Watchdog: ConfirmStock, ReleaseStock, RestockOrderItems, ReleasePromotion, ReleasePromotionShop, RecordCODRejection, CancelShipment(FORWARD).
	t.Run("JobsAndConsumers_StockPromotionCODRejectionAndForwardCancel", func(t *testing.T) {
		// 3a. VietQR checkout -> HandlePaymentSucceeded (consumer: ConfirmStock) -> HandlePaymentFailed on 2nd checkout (consumer: ReleaseStock + ReleasePromotion).
		co1, err := svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "trust-vqr-ok",
			BuyerId:           "buyer-1",
			AddressId:         "addr-buyer-1",
			Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PlatformVoucherId: "v-plat",
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout #1: %v", err)
		}
		if err := svc.HandlePaymentSucceeded(context.Background(), "evt-trust-pay-ok", events.PaymentSucceededPayload{
			PaymentID: co1.PaymentId, OrderGroupID: co1.CheckoutGroupId,
		}); err != nil {
			t.Fatalf("HandlePaymentSucceeded: %v", err)
		}
		if stub.countService("Product.ConfirmStock") < 1 {
			t.Fatalf("expected service-token Product.ConfirmStock from HandlePaymentSucceeded")
		}
		if stub.countService("Promotion.ReservePromotion") < 1 {
			t.Fatalf("D3: expected service-token Promotion.ReservePromotion from CreateCheckout")
		}
		if stub.countService("Shipping.GetShopShippingSettings") < 1 {
			t.Fatalf("expected service-token Shipping.GetShopShippingSettings from CreateCheckout")
		}

		co2, err := svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "trust-vqr-fail",
			BuyerId:           "buyer-1",
			AddressId:         "addr-buyer-1",
			Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PlatformVoucherId: "v-plat",
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout #2: %v", err)
		}
		if err := svc.HandlePaymentFailed(context.Background(), "evt-trust-pay-fail", events.PaymentFailedPayload{
			PaymentID: co2.PaymentId, OrderGroupID: co2.CheckoutGroupId, Reason: "declined",
		}); err != nil {
			t.Fatalf("HandlePaymentFailed: %v", err)
		}
		if stub.countService("Product.ReleaseStock") < 1 || stub.countService("Promotion.ReleasePromotion") < 1 {
			t.Fatalf("expected service-token ReleaseStock & ReleasePromotion, got %+v", stub.seenServiceMethods)
		}

		// 3b. COD order shipped -> FORWARD shipment returned (consumer: ConfirmStock + RestockOrderItems + ReleasePromotionShop + RecordCODRejection).
		co3, err := svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "trust-cod-ret",
			BuyerId:           "buyer-1",
			AddressId:         "addr-buyer-1",
			Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PlatformVoucherId: "v-plat",
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil {
			t.Fatalf("CreateCheckout #3: %v", err)
		}
		ord3ID := co3.Orders[0].Id
		ship3, err := svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord3ID, ShopId: "shop-1"})
		if err != nil {
			t.Fatalf("SellerShipOrder #3: %v", err)
		}
		if err := svc.HandleShipmentUpdated(context.Background(), "evt-trust-fwd-ret", events.ShipmentUpdatedPayload{
			OrderID: ord3ID, TrackingCode: ship3.TrackingCode, Status: "returned", Description: "refused COD",
		}); err != nil {
			t.Fatalf("HandleShipmentUpdated(returned): %v", err)
		}
		for _, m := range []string{"Product.ConfirmStock", "Product.RestockOrderItems", "Promotion.ReleasePromotionShop", "User.RecordCODRejection"} {
			if stub.countService(m) < 1 {
				t.Fatalf("expected service-token %s from consumer, counts=%+v", m, stub.seenServiceMethods)
			}
		}

		// 3c. Forward CancelShipment from watchdog (ScanPendingOrphanShipmentCancels).
		beforeCancel := stub.countService("Shipping.CancelShipment")
		co4, err := svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "trust-orphan-can",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil {
			t.Fatalf("CreateCheckout #4: %v", err)
		}
		ord4ID := co4.Orders[0].Id
		_, _ = mem.MarkOrderShipmentCreating(context.Background(), ord4ID, now)
		if _, err := svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: ord4ID, Reason: "cancel"}); err != nil {
			t.Fatalf("CancelOrder #4: %v", err)
		}
		if n, err := svc.ScanPendingOrphanShipmentCancels(context.Background()); err != nil || n != 1 {
			t.Fatalf("ScanPendingOrphanShipmentCancels = %d, %v; want 1, nil", n, err)
		}
		if stub.countService("Shipping.CancelShipment") <= beforeCancel {
			t.Fatalf("expected service-token Shipping.CancelShipment from orphan watchdog")
		}
	})
}
