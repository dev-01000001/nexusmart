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

// Resolver looks up user contact information (email + display name) via UserService.
type Resolver interface {
	GetUserContact(ctx context.Context, userID string) (email string, fullName string, err error)
}

// Client wraps a gRPC UserService client with timeout and OTel propagation.
type Client struct {
	conn    *grpc.ClientConn
	client  userv1.UserServiceClient
	svc     *auth.Manager
	timeout time.Duration
}

// serviceTokenName is the service identity notification presents to UserService.
const serviceTokenName = "notification-service"

// New creates a lazy-connecting gRPC client to UserService. svc signs the service token
// sent on each call (consumers have no end user).
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

// GetUserContact calls UserService.GetProfile to resolve a user's email and full name.
func (c *Client) GetUserContact(ctx context.Context, userID string) (string, string, error) {
	if c == nil || c.client == nil || strings.TrimSpace(userID) == "" {
		return "", "", nil
	}
	tok, err := c.svc.SignServiceToken(serviceTokenName)
	if err != nil {
		return "", "", err
	}
	callCtx, cancel := context.WithTimeout(auth.InjectOutgoingGRPC(ctx, tok), c.timeout)
	defer cancel()

	resp, err := c.client.GetProfile(callCtx, &userv1.GetProfileRequest{UserId: userID})
	if err != nil {
		return "", "", err
	}
	if resp.GetUser() == nil {
		return "", "", nil
	}
	return strings.TrimSpace(resp.GetUser().GetEmail()), strings.TrimSpace(resp.GetUser().GetFullName()), nil
}

// SiteResolver resolves the public host of a shop's PUBLISHED site.
type SiteResolver interface {
	// GetSitePrimaryHost returns the primary host (e.g. "bach-dang.nexusmart.vn") of the shop's
	// PUBLISHED site, or "" with a nil error when the shop has no published site.
	GetSitePrimaryHost(ctx context.Context, shopID string) (string, error)
}

// GetSitePrimaryHost calls the public UserService.GetSiteByShop RPC (no JWT required).
func (c *Client) GetSitePrimaryHost(ctx context.Context, shopID string) (string, error) {
	if c == nil || c.client == nil || strings.TrimSpace(shopID) == "" {
		return "", nil
	}
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	resp, err := c.client.GetSiteByShop(callCtx, &userv1.GetSiteByShopRequest{ShopId: strings.TrimSpace(shopID)})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return "", nil
		}
		return "", err
	}
	site := resp.GetSite()
	if site == nil || site.GetStatus() != userv1.SiteStatus_SITE_STATUS_PUBLISHED {
		return "", nil
	}
	return strings.TrimSpace(site.GetPrimaryHost()), nil
}
