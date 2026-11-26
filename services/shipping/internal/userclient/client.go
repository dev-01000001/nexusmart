package userclient

import (
	"context"
	"fmt"
	"strings"
	"time"

	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

const serviceTokenName = "shipping-service"

// Client wraps a gRPC UserService client for resolving shop pickup addresses.
type Client struct {
	conn    *grpc.ClientConn
	client  userv1.UserServiceClient
	svc     *auth.Manager
	timeout time.Duration
}

// New creates a lazy-connecting gRPC client to UserService.
// svc signs internal service tokens for GetShopInternal and GetAddress calls.
func New(addr, serviceName string, svc *auth.Manager) (*Client, error) {
	if strings.TrimSpace(addr) == "" {
		return nil, fmt.Errorf("user grpc address is empty")
	}
	conn, err := grpc.NewClient(
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(
			telemetry.GRPCUnaryClientInterceptor(serviceName),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("dial user service %s: %w", addr, err)
	}
	return &Client{
		conn:    conn,
		client:  userv1.NewUserServiceClient(conn),
		svc:     svc,
		timeout: 3 * time.Second,
	}, nil
}

// Close closes the underlying gRPC connection.
func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// GetShopPickupCodes resolves a shop's pickup (province_code, ward_code) from UserService,
// matching the source used by OrderService during checkout.
func (c *Client) GetShopPickupCodes(ctx context.Context, shopID string) (string, string, error) {
	shopID = strings.TrimSpace(shopID)
	if c == nil || c.client == nil {
		return "", "", fmt.Errorf("user client not initialized")
	}
	if shopID == "" {
		return "", "", nil
	}

	if c.svc == nil {
		return "", "", fmt.Errorf("service token manager not initialized")
	}

	tok, err := c.svc.SignServiceToken(serviceTokenName)
	if err != nil {
		return "", "", fmt.Errorf("sign service token: %w", err)
	}

	shopCtx, cancelShop := context.WithTimeout(auth.InjectOutgoingGRPC(ctx, tok), c.timeout)
	defer cancelShop()

	shopResp, err := c.client.GetShopInternal(shopCtx, &userv1.GetShopInternalRequest{ShopId: shopID})
	if err != nil {
		return "", "", err
	}
	shop := shopResp.GetShop()
	if shop == nil {
		return "", "", status.Error(codes.NotFound, "shop not found")
	}
	pickupAddrID := strings.TrimSpace(shop.GetPickupAddressId())
	ownerUserID := strings.TrimSpace(shop.GetOwnerUserId())
	if pickupAddrID == "" || ownerUserID == "" {
		return "", "", nil
	}

	addrCtx, cancelAddr := context.WithTimeout(auth.InjectOutgoingGRPC(ctx, tok), c.timeout)
	defer cancelAddr()

	addrResp, err := c.client.GetAddress(addrCtx, &userv1.GetAddressRequest{
		UserId:    ownerUserID,
		AddressId: pickupAddrID,
	})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return "", "", nil
		}
		return "", "", err
	}
	addr := addrResp.GetAddress()
	if addr == nil {
		return "", "", nil
	}
	return strings.TrimSpace(addr.GetProvinceCode()), strings.TrimSpace(addr.GetWardCode()), nil
}
