package clients

import (
	"context"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	cartv1 "github.com/nexusmart/core/gen/go/nexusmart/cart/v1"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/order/internal/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// UserClient defines the subset of UserService RPCs required by OrderService.
type UserClient interface {
	ValidateUser(ctx context.Context, req *userv1.ValidateUserRequest, opts ...grpc.CallOption) (*userv1.ValidateUserResponse, error)
	GetAddress(ctx context.Context, req *userv1.GetAddressRequest, opts ...grpc.CallOption) (*userv1.GetAddressResponse, error)
	GetShop(ctx context.Context, req *userv1.GetShopRequest, opts ...grpc.CallOption) (*userv1.GetShopResponse, error)
}

// ProductClient defines the subset of ProductService RPCs required by OrderService.
type ProductClient interface {
	GetSKUsBatch(ctx context.Context, req *productv1.GetSKUsBatchRequest, opts ...grpc.CallOption) (*productv1.GetSKUsBatchResponse, error)
	UpdateStock(ctx context.Context, req *productv1.UpdateStockRequest, opts ...grpc.CallOption) (*productv1.UpdateStockResponse, error)
	ReserveStock(ctx context.Context, req *productv1.ReserveStockRequest, opts ...grpc.CallOption) (*productv1.ReserveStockResponse, error)
	ReleaseStock(ctx context.Context, req *productv1.ReleaseStockRequest, opts ...grpc.CallOption) (*productv1.ReleaseStockResponse, error)
	ConfirmStock(ctx context.Context, req *productv1.ConfirmStockRequest, opts ...grpc.CallOption) (*productv1.ConfirmStockResponse, error)
}

// CartClient defines the subset of CartService RPCs required by OrderService.
type CartClient interface {
	GetCart(ctx context.Context, req *cartv1.GetCartRequest, opts ...grpc.CallOption) (*cartv1.GetCartResponse, error)
	ClearItems(ctx context.Context, req *cartv1.ClearItemsRequest, opts ...grpc.CallOption) (*cartv1.ClearItemsResponse, error)
}

// PromotionClient defines the subset of PromotionService RPCs required by OrderService.
type PromotionClient interface {
	ListActiveFlashSales(ctx context.Context, req *promotionv1.ListActiveFlashSalesRequest, opts ...grpc.CallOption) (*promotionv1.ListActiveFlashSalesResponse, error)
	PreviewDiscounts(ctx context.Context, req *promotionv1.PreviewDiscountsRequest, opts ...grpc.CallOption) (*promotionv1.PreviewDiscountsResponse, error)
	ReservePromotion(ctx context.Context, req *promotionv1.ReservePromotionRequest, opts ...grpc.CallOption) (*promotionv1.ReservePromotionResponse, error)
	ReleasePromotion(ctx context.Context, req *promotionv1.ReleasePromotionRequest, opts ...grpc.CallOption) (*promotionv1.ReleasePromotionResponse, error)
}

// PaymentClient defines the subset of PaymentService RPCs required by OrderService.
type PaymentClient interface {
	CreatePayment(ctx context.Context, req *paymentv1.CreatePaymentRequest, opts ...grpc.CallOption) (*paymentv1.CreatePaymentResponse, error)
	GetPayment(ctx context.Context, req *paymentv1.GetPaymentRequest, opts ...grpc.CallOption) (*paymentv1.GetPaymentResponse, error)
	CancelPayment(ctx context.Context, req *paymentv1.CancelPaymentRequest, opts ...grpc.CallOption) (*paymentv1.CancelPaymentResponse, error)
	CreateRefund(ctx context.Context, req *paymentv1.CreateRefundRequest, opts ...grpc.CallOption) (*paymentv1.CreateRefundResponse, error)
	ReleaseEscrow(ctx context.Context, req *paymentv1.ReleaseEscrowRequest, opts ...grpc.CallOption) (*paymentv1.ReleaseEscrowResponse, error)
}

// ShippingClient defines the subset of ShippingService RPCs required by OrderService.
type ShippingClient interface {
	CalculateFee(ctx context.Context, req *shippingv1.CalculateFeeRequest, opts ...grpc.CallOption) (*shippingv1.CalculateFeeResponse, error)
	CreateShipment(ctx context.Context, req *shippingv1.CreateShipmentRequest, opts ...grpc.CallOption) (*shippingv1.CreateShipmentResponse, error)
	CreateReturnShipment(ctx context.Context, req *shippingv1.CreateReturnShipmentRequest, opts ...grpc.CallOption) (*shippingv1.CreateReturnShipmentResponse, error)
	CancelShipment(ctx context.Context, req *shippingv1.CancelShipmentRequest, opts ...grpc.CallOption) (*shippingv1.CancelShipmentResponse, error)
}

// Bundle groups all downstream gRPC clients used by OrderService.
type Bundle struct {
	User      UserClient
	Product   ProductClient
	Cart      CartClient
	Promotion PromotionClient
	Payment   PaymentClient
	Shipping  ShippingClient

	conns []*grpc.ClientConn
}

// DialAll initializes gRPC client connections to all 6 downstream services with OTel, auth propagation, and timeouts.
// svc signs service tokens for WithInternalAdminContext calls (internal key, not the user key).
func DialAll(cfg config.Config, svc *auth.Manager) (*Bundle, error) {
	dial := func(addr string) (*grpc.ClientConn, error) {
		conn, err := grpc.NewClient(
			addr,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithChainUnaryInterceptor(
				timeoutAndAuthClientInterceptor(cfg.GRPCCallTimeout, svc),
				telemetry.GRPCUnaryClientInterceptor(cfg.ServiceName),
			),
		)
		if err != nil {
			return nil, fmt.Errorf("dial %s: %w", addr, err)
		}
		return conn, nil
	}

	b := &Bundle{}
	cleanup := func() {
		_ = b.Close()
	}

	userConn, err := dial(cfg.UserGRPCAddr)
	if err != nil {
		cleanup()
		return nil, err
	}
	b.conns = append(b.conns, userConn)
	b.User = userv1.NewUserServiceClient(userConn)

	productConn, err := dial(cfg.ProductGRPCAddr)
	if err != nil {
		cleanup()
		return nil, err
	}
	b.conns = append(b.conns, productConn)
	b.Product = productv1.NewProductServiceClient(productConn)

	cartConn, err := dial(cfg.CartGRPCAddr)
	if err != nil {
		cleanup()
		return nil, err
	}
	b.conns = append(b.conns, cartConn)
	b.Cart = cartv1.NewCartServiceClient(cartConn)

	promoConn, err := dial(cfg.PromotionGRPCAddr)
	if err != nil {
		cleanup()
		return nil, err
	}
	b.conns = append(b.conns, promoConn)
	b.Promotion = promotionv1.NewPromotionServiceClient(promoConn)

	paymentConn, err := dial(cfg.PaymentGRPCAddr)
	if err != nil {
		cleanup()
		return nil, err
	}
	b.conns = append(b.conns, paymentConn)
	b.Payment = paymentv1.NewPaymentServiceClient(paymentConn)

	shippingConn, err := dial(cfg.ShippingGRPCAddr)
	if err != nil {
		cleanup()
		return nil, err
	}
	b.conns = append(b.conns, shippingConn)
	b.Shipping = shippingv1.NewShippingServiceClient(shippingConn)

	return b, nil
}

// Close shuts down all underlying gRPC client connections.
func (b *Bundle) Close() error {
	var firstErr error
	for _, conn := range b.conns {
		if err := conn.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// InternalServiceName is the service identity order presents on calls with no end user.
const InternalServiceName = "order-service"

// WithInternalAdminContext marks ctx for internal calls with no end user (saga compensation,
// payment-result consumer, expiry watchdog). The client interceptor then sends a short-lived
// service token instead of the caller's JWT; downstream accepts it only on internal RPCs.
func WithInternalAdminContext(ctx context.Context) context.Context {
	ctx = auth.WithRawToken(ctx, "")
	return auth.WithClaims(ctx, &auth.Claims{
		Role:             auth.ServiceRole,
		Service:          InternalServiceName,
		RegisteredClaims: jwt.RegisteredClaims{Subject: InternalServiceName},
	})
}

// RetryIdempotent executes fn up to maxAttempts with exponential backoff on transient gRPC failures.
func RetryIdempotent(ctx context.Context, maxAttempts int, fn func(context.Context) error) error {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	var lastErr error
	backoff := 50 * time.Millisecond
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		lastErr = fn(ctx)
		if lastErr == nil {
			return nil
		}
		if !isRetryable(lastErr) || attempt == maxAttempts {
			return lastErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
			backoff *= 2
		}
	}
	return lastErr
}

func isRetryable(err error) bool {
	code := status.Code(err)
	return code == codes.Unavailable || code == codes.DeadlineExceeded || code == codes.ResourceExhausted
}

func timeoutAndAuthClientInterceptor(defaultTimeout time.Duration, svc *auth.Manager) grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		req, reply any,
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		if defaultTimeout > 0 {
			if _, hasDeadline := ctx.Deadline(); !hasDeadline {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, defaultTimeout)
				defer cancel()
			}
		}

		if claims, ok := auth.ClaimsFromContext(ctx); ok && claims.Service != "" {
			tok, err := svc.SignServiceToken(claims.Service)
			if err != nil {
				return status.Error(codes.Internal, "sign service token")
			}
			ctx = auth.InjectOutgoingGRPC(ctx, tok)
		} else {
			ctx = auth.InjectOutgoingGRPC(ctx, auth.RawTokenFromContext(ctx))
		}

		return invoker(ctx, method, req, reply, cc, opts...)
	}
}
