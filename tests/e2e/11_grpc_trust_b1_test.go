package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	cartv1 "github.com/nexusmart/core/gen/go/nexusmart/cart/v1"
	notificationv1 "github.com/nexusmart/core/gen/go/nexusmart/notification/v1"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata probes internal gRPC endpoints
// directly inside the compose network to confirm B1 residual trust fixes reject both
// anonymous callers and forged plain x-user-* metadata with Unauthenticated.
func TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata(t *testing.T) {
	env := getEnv()

	dial := func(t *testing.T, addr string) *grpc.ClientConn {
		t.Helper()
		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Fatalf("dial %s: %v", addr, err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		return conn
	}

	userClient := userv1.NewUserServiceClient(dial(t, env.cfg.UserGRPCAddr))
	productClient := productv1.NewProductServiceClient(dial(t, env.cfg.ProductGRPCAddr))
	cartClient := cartv1.NewCartServiceClient(dial(t, env.cfg.CartGRPCAddr))
	promoClient := promotionv1.NewPromotionServiceClient(dial(t, env.cfg.PromotionGRPCAddr))
	orderClient := orderv1.NewOrderServiceClient(dial(t, env.cfg.OrderGRPCAddr))
	paymentClient := paymentv1.NewPaymentServiceClient(dial(t, env.cfg.PaymentGRPCAddr))
	shippingClient := shippingv1.NewShippingServiceClient(dial(t, env.cfg.ShippingGRPCAddr))
	notifClient := notificationv1.NewNotificationServiceClient(dial(t, env.cfg.NotificationGRPCAddr))

	fakeID := uuid.NewString()
	fakeShopID := uuid.NewString()

	probes := []struct {
		name string
		call func(ctx context.Context) error
	}{
		{
			name: "ProductService.UpdateStock",
			call: func(ctx context.Context) error {
				_, err := productClient.UpdateStock(ctx, &productv1.UpdateStockRequest{
					SkuId:           fakeID,
					ShopId:          fakeShopID,
					NewStock:        999,
					ExpectedVersion: 1,
				})
				return err
			},
		},
		{
			name: "CartService.ClearItems",
			call: func(ctx context.Context) error {
				_, err := cartClient.ClearItems(ctx, &cartv1.ClearItemsRequest{
					UserId: fakeID,
				})
				return err
			},
		},
		{
			name: "CartService.GetCart",
			call: func(ctx context.Context) error {
				_, err := cartClient.GetCart(ctx, &cartv1.GetCartRequest{
					UserId: fakeID,
				})
				return err
			},
		},
		{
			name: "ShippingService.CreateShipment",
			call: func(ctx context.Context) error {
				_, err := shippingClient.CreateShipment(ctx, &shippingv1.CreateShipmentRequest{
					OrderId:     fakeID,
					ShopId:      fakeShopID,
					CarrierCode: "ghn",
					ServiceCode: "standard",
					WeightGrams: 500,
				})
				return err
			},
		},
		{
			name: "ShippingService.CancelShipment",
			call: func(ctx context.Context) error {
				_, err := shippingClient.CancelShipment(ctx, &shippingv1.CancelShipmentRequest{
					OrderId: fakeID,
					Reason:  "anonymous_probe",
				})
				return err
			},
		},
		{
			name: "ShippingService.GetShipment",
			call: func(ctx context.Context) error {
				_, err := shippingClient.GetShipment(ctx, &shippingv1.GetShipmentRequest{
					OrderId: fakeID,
				})
				return err
			},
		},
		{
			name: "UserService.ValidateUser",
			call: func(ctx context.Context) error {
				_, err := userClient.ValidateUser(ctx, &userv1.ValidateUserRequest{
					UserId: fakeID,
				})
				return err
			},
		},
		{
			name: "UserService.WriteAuditLog",
			call: func(ctx context.Context) error {
				_, err := userClient.WriteAuditLog(ctx, &userv1.WriteAuditLogRequest{
					ActorId:   fakeID,
					ActorRole: "admin",
					Action:    "anonymous_probe",
				})
				return err
			},
		},
		{
			name: "OrderService.MarkPaymentResult",
			call: func(ctx context.Context) error {
				_, err := orderClient.MarkPaymentResult(ctx, &orderv1.MarkPaymentResultRequest{
					CheckoutGroupId: fakeID,
					OrderId:         fakeID,
					PaymentId:       fakeID,
					Status:          orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_SUCCEEDED,
				})
				return err
			},
		},
		{
			name: "NotificationService.ListNotifications",
			call: func(ctx context.Context) error {
				_, err := notifClient.ListNotifications(ctx, &notificationv1.ListNotificationsRequest{
					UserId: fakeID,
				})
				return err
			},
		},
		{
			name: "NotificationService.MarkRead",
			call: func(ctx context.Context) error {
				_, err := notifClient.MarkRead(ctx, &notificationv1.MarkReadRequest{
					UserId:  fakeID,
					MarkAll: true,
				})
				return err
			},
		},
		{
			name: "PromotionService.CreditCoins",
			call: func(ctx context.Context) error {
				_, err := promoClient.CreditCoins(ctx, &promotionv1.CreditCoinsRequest{
					UserId:      fakeID,
					Amount:      999999,
					Reason:      "anonymous_probe",
					ReferenceId: "probe-" + fakeID,
				})
				return err
			},
		},
		{
			name: "PromotionService.PreviewDiscounts",
			call: func(ctx context.Context) error {
				_, err := promoClient.PreviewDiscounts(ctx, &promotionv1.PreviewDiscountsRequest{
					UserId: fakeID,
				})
				return err
			},
		},
		{
			name: "PromotionService.GetCoinBalance",
			call: func(ctx context.Context) error {
				_, err := promoClient.GetCoinBalance(ctx, &promotionv1.GetCoinBalanceRequest{
					UserId: fakeID,
				})
				return err
			},
		},
		{
			name: "PromotionService.SaveVoucher",
			call: func(ctx context.Context) error {
				_, err := promoClient.SaveVoucher(ctx, &promotionv1.SaveVoucherRequest{
					UserId:    fakeID,
					VoucherId: fakeID,
				})
				return err
			},
		},
		{
			name: "PaymentService.GetLedgerSummary",
			call: func(ctx context.Context) error {
				_, err := paymentClient.GetLedgerSummary(ctx, &paymentv1.GetLedgerSummaryRequest{})
				return err
			},
		},
		{
			name: "PaymentService.ReleaseEscrow",
			call: func(ctx context.Context) error {
				_, err := paymentClient.ReleaseEscrow(ctx, &paymentv1.ReleaseEscrowRequest{
					OrderId: fakeID,
					ShopId:  fakeShopID,
				})
				return err
			},
		},
		{
			name: "PaymentService.RunReconciliation",
			call: func(ctx context.Context) error {
				_, err := paymentClient.RunReconciliation(ctx, &paymentv1.RunReconciliationRequest{})
				return err
			},
		},
	}

	for _, p := range probes {
		p := p
		t.Run(p.name, func(t *testing.T) {
			// 1. Completely anonymous call (no metadata).
			anonCtx, cancel1 := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel1()
			if err := p.call(anonCtx); status.Code(err) != codes.Unauthenticated {
				t.Fatalf("%s (anonymous): expected Unauthenticated, got code=%s err=%v", p.name, status.Code(err), err)
			}

			// 2. Spoofed x-user-* metadata without a valid signed JWT.
			spoofCtx, cancel2 := context.WithTimeout(
				metadata.NewOutgoingContext(context.Background(), metadata.Pairs(
					"x-user-id", fakeID,
					"x-user-role", "admin",
					"x-shop-id", fakeShopID,
				)),
				10*time.Second,
			)
			defer cancel2()
			if err := p.call(spoofCtx); status.Code(err) != codes.Unauthenticated {
				t.Fatalf("%s (spoofed metadata): expected Unauthenticated, got code=%s err=%v", p.name, status.Code(err), err)
			}
		})
	}
}
