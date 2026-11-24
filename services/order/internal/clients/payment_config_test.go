package clients

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/order/internal/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type fakePaymentConfigGRPCServer struct {
	paymentv1.UnimplementedPaymentServiceServer

	mu            sync.Mutex
	configs       []*paymentv1.PaymentMethodConfig
	err           error
	calls         atomic.Int32
	lastService   string
	lastRole      string
	lastReqMethod paymentv1.PaymentMethod
}

func (s *fakePaymentConfigGRPCServer) GetPaymentMethodConfigs(ctx context.Context, req *paymentv1.GetPaymentMethodConfigsRequest) (*paymentv1.GetPaymentMethodConfigsResponse, error) {
	s.calls.Add(1)
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing auth claims")
	}
	s.mu.Lock()
	s.lastService = claims.Service
	s.lastRole = claims.Role
	s.lastReqMethod = req.GetMethod()
	err := s.err
	cfgs := append([]*paymentv1.PaymentMethodConfig(nil), s.configs...)
	s.mu.Unlock()

	if err != nil {
		return nil, err
	}
	return &paymentv1.GetPaymentMethodConfigsResponse{Configs: cfgs}, nil
}

func TestGRPCPaymentMethodConfigSource_RealGRPCServiceToken_NoCacheAndFailClosed(t *testing.T) {
	userMgr, err := auth.NewManager("test-user-jwt-secret-32-bytes-min!", auth.DefaultAccessTTL)
	if err != nil {
		t.Fatalf("NewManager(user): %v", err)
	}
	svcMgr, err := auth.NewManager("test-service-jwt-secret-32-bytes!", auth.ServiceTokenTTL)
	if err != nil {
		t.Fatalf("NewManager(svc): %v", err)
	}

	paySrv := &fakePaymentConfigGRPCServer{
		configs: []*paymentv1.PaymentMethodConfig{
			{Method: paymentv1.PaymentMethod_PAYMENT_METHOD_COD, Enabled: true, MinOrderVnd: 10000, MaxOrderVnd: 5000000, ExpiryMinutes: 0},
			{Method: paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR, Enabled: true, MinOrderVnd: 5000, MaxOrderVnd: 50000000, ExpiryMinutes: 15},
			{Method: paymentv1.PaymentMethod_PAYMENT_METHOD_ZALOPAY, Enabled: true, MinOrderVnd: 10000, MaxOrderVnd: 20000000, ExpiryMinutes: 20},
		},
	}

	lis := bufconn.Listen(1024 * 1024)
	// PaymentService is a Rust service (services/payment/src/auth.rs SERVICE_METHODS includes
	// GetPaymentMethodConfigs); verify the exact wire contract using real signed service JWTs.
	paymentServiceAuthInterceptor := func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		md, _ := metadata.FromIncomingContext(ctx)
		vals := md.Get(auth.MDAuthorization)
		if len(vals) == 0 {
			return nil, status.Error(codes.Unauthenticated, "missing authorization metadata")
		}
		claims, vErr := svcMgr.VerifyServiceToken(vals[0])
		if vErr != nil {
			return nil, status.Errorf(codes.PermissionDenied, "expected valid service token for %s: %v", info.FullMethod, vErr)
		}
		return handler(auth.WithClaims(ctx, claims), req)
	}
	grpcSrv := grpc.NewServer(
		grpc.UnaryInterceptor(paymentServiceAuthInterceptor),
	)
	paymentv1.RegisterPaymentServiceServer(grpcSrv, paySrv)
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
		grpc.WithChainUnaryInterceptor(AuthClientInterceptor(5*time.Second, svcMgr)),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	src := NewGRPCPaymentMethodConfigSource(paymentv1.NewPaymentServiceClient(conn))

	// Even when caller passes a buyer JWT context, the adapter MUST authenticate with a service token.
	buyerTok, _, _, err := userMgr.SignAccessToken("buyer-1", "buyer", "")
	if err != nil {
		t.Fatalf("SignAccessToken: %v", err)
	}
	buyerClaims, _ := userMgr.VerifyAccessToken(buyerTok)
	buyerCtx := auth.WithRawToken(auth.WithClaims(context.Background(), buyerClaims), "Bearer "+buyerTok)

	zlpCfg, err := src.GetPaymentMethodConfig(buyerCtx, "ZALOPAY")
	if err != nil {
		t.Fatalf("GetPaymentMethodConfig(ZALOPAY): %v", err)
	}
	if !zlpCfg.Enabled || zlpCfg.MinOrderVND != 10000 || zlpCfg.MaxOrderVND != 20000000 || zlpCfg.ExpiryMinutes != 20 {
		t.Fatalf("unexpected ZALOPAY config: %+v", zlpCfg)
	}
	paySrv.mu.Lock()
	gotSvc, gotRole, gotReqMethod := paySrv.lastService, paySrv.lastRole, paySrv.lastReqMethod
	paySrv.mu.Unlock()
	if gotSvc != InternalServiceName || gotRole != auth.ServiceRole {
		t.Fatalf("expected service token (%s/%s), got service=%q role=%q", InternalServiceName, auth.ServiceRole, gotSvc, gotRole)
	}
	if gotReqMethod != paymentv1.PaymentMethod_PAYMENT_METHOD_ZALOPAY {
		t.Fatalf("expected request method PAYMENT_METHOD_ZALOPAY, got %v", gotReqMethod)
	}

	// Zero caching: mutate config on server and verify immediate reflection on the next call.
	paySrv.mu.Lock()
	paySrv.configs[2] = &paymentv1.PaymentMethodConfig{
		Method:        paymentv1.PaymentMethod_PAYMENT_METHOD_ZALOPAY,
		Enabled:       false,
		MinOrderVnd:   50000,
		MaxOrderVnd:   10000000,
		ExpiryMinutes: 30,
	}
	paySrv.mu.Unlock()

	zlpCfg2, err := src.GetPaymentMethodConfig(buyerCtx, "zalopay")
	if err != nil {
		t.Fatalf("second GetPaymentMethodConfig(ZALOPAY): %v", err)
	}
	if zlpCfg2.Enabled || zlpCfg2.ExpiryMinutes != 30 || zlpCfg2.MinOrderVND != 50000 {
		t.Fatalf("F6 violation: config appears cached, got %+v", zlpCfg2)
	}
	if paySrv.calls.Load() != 2 {
		t.Fatalf("expected 2 direct gRPC calls (no cache), got %d", paySrv.calls.Load())
	}

	// Missing method in response -> NotFound.
	paySrv.mu.Lock()
	paySrv.configs = paySrv.configs[:1] // Only COD
	paySrv.mu.Unlock()
	if _, err := src.GetPaymentMethodConfig(buyerCtx, "VIETQR"); status.Code(err) != codes.NotFound {
		t.Fatalf("missing method config got %v, want NotFound", err)
	}

	// Downstream error -> Unavailable (fail closed).
	paySrv.mu.Lock()
	paySrv.err = status.Error(codes.Unavailable, "payment db down")
	paySrv.mu.Unlock()
	if _, err := src.GetPaymentMethodConfig(buyerCtx, "COD"); status.Code(err) != codes.Unavailable {
		t.Fatalf("downstream unavailable got %v, want Unavailable", err)
	}

	// Nil client fails closed with Unavailable.
	if _, err := NewGRPCPaymentMethodConfigSource(nil).GetPaymentMethodConfig(context.Background(), "COD"); status.Code(err) != codes.Unavailable {
		t.Fatalf("nil client got %v, want Unavailable", err)
	}

	// DialAll automatically wires Bundle.MethodConfigs to GRPCPaymentMethodConfigSource.
	bundle, err := DialAll(config.Config{
		ServiceName:       "order",
		UserGRPCAddr:      "127.0.0.1:19001",
		ProductGRPCAddr:   "127.0.0.1:19002",
		CartGRPCAddr:      "127.0.0.1:19003",
		PromotionGRPCAddr: "127.0.0.1:19004",
		PaymentGRPCAddr:   "127.0.0.1:19005",
		ShippingGRPCAddr:  "127.0.0.1:19006",
		GRPCCallTimeout:   time.Second,
	}, svcMgr)
	if err != nil {
		t.Fatalf("DialAll: %v", err)
	}
	t.Cleanup(func() { _ = bundle.Close() })
	if _, ok := bundle.MethodConfigs.(*GRPCPaymentMethodConfigSource); !ok {
		t.Fatalf("DialAll must wire MethodConfigs to *GRPCPaymentMethodConfigSource, got %T", bundle.MethodConfigs)
	}
}
