package clients

import (
	"context"
	"fmt"
	"time"

	cartv1 "github.com/nexusmart/core/gen/go/nexusmart/cart/v1"
	notificationv1 "github.com/nexusmart/core/gen/go/nexusmart/notification/v1"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/gateway/internal/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// Clients holds gRPC clients to all 8 NexusMart backend microservices.
type Clients struct {
	User         userv1.UserServiceClient
	Product      productv1.ProductServiceClient
	Cart         cartv1.CartServiceClient
	Promotion    promotionv1.PromotionServiceClient
	Order        orderv1.OrderServiceClient
	Payment      paymentv1.PaymentServiceClient
	Shipping     shippingv1.ShippingServiceClient
	Notification notificationv1.NotificationServiceClient

	Timeout time.Duration
	conns   []*grpc.ClientConn
}

// New initializes lazy gRPC client connections to all 8 backend services.
func New(cfg config.Config) (*Clients, error) {
	dial := func(name, addr string) (*grpc.ClientConn, error) {
		conn, err := grpc.NewClient(
			addr,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithUnaryInterceptor(telemetry.GRPCUnaryClientInterceptor(cfg.ServiceName)),
		)
		if err != nil {
			return nil, fmt.Errorf("init %s grpc client (%s): %w", name, addr, err)
		}
		return conn, nil
	}

	c := &Clients{Timeout: cfg.GRPCCallTimeout}
	if c.Timeout <= 0 {
		c.Timeout = 10 * time.Second
	}

	addConn := func(name, addr string) (*grpc.ClientConn, error) {
		conn, err := dial(name, addr)
		if err != nil {
			c.Close()
			return nil, err
		}
		c.conns = append(c.conns, conn)
		return conn, nil
	}

	userConn, err := addConn("user", cfg.UserGRPCAddr)
	if err != nil {
		return nil, err
	}
	productConn, err := addConn("product", cfg.ProductGRPCAddr)
	if err != nil {
		return nil, err
	}
	cartConn, err := addConn("cart", cfg.CartGRPCAddr)
	if err != nil {
		return nil, err
	}
	promoConn, err := addConn("promotion", cfg.PromotionGRPCAddr)
	if err != nil {
		return nil, err
	}
	orderConn, err := addConn("order", cfg.OrderGRPCAddr)
	if err != nil {
		return nil, err
	}
	paymentConn, err := addConn("payment", cfg.PaymentGRPCAddr)
	if err != nil {
		return nil, err
	}
	shippingConn, err := addConn("shipping", cfg.ShippingGRPCAddr)
	if err != nil {
		return nil, err
	}
	notifConn, err := addConn("notification", cfg.NotificationGRPCAddr)
	if err != nil {
		return nil, err
	}

	c.User = userv1.NewUserServiceClient(userConn)
	c.Product = productv1.NewProductServiceClient(productConn)
	c.Cart = cartv1.NewCartServiceClient(cartConn)
	c.Promotion = promotionv1.NewPromotionServiceClient(promoConn)
	c.Order = orderv1.NewOrderServiceClient(orderConn)
	c.Payment = paymentv1.NewPaymentServiceClient(paymentConn)
	c.Shipping = shippingv1.NewShippingServiceClient(shippingConn)
	c.Notification = notificationv1.NewNotificationServiceClient(notifConn)

	return c, nil
}

// Close shuts down all underlying gRPC client connections.
func (c *Clients) Close() {
	for _, conn := range c.conns {
		if conn != nil {
			_ = conn.Close()
		}
	}
}

// OutgoingContext creates a timed context forwarding the user's Authorization Bearer token
// (services trust identity only from the verified JWT) and optional extra key/value metadata pairs.
func (c *Clients) OutgoingContext(ctx context.Context, extraPairs ...string) (context.Context, context.CancelFunc) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	outCtx, cancel := context.WithTimeout(ctx, timeout)

	outCtx = auth.InjectOutgoingGRPC(outCtx, auth.RawTokenFromContext(ctx))

	if len(extraPairs) >= 2 {
		outCtx = metadata.AppendToOutgoingContext(outCtx, extraPairs...)
	}
	return outCtx, cancel
}
