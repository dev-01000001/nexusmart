package userclient

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

var (
	ErrShopNotFound = errors.New("shop not found")
	ErrShopInactive = errors.New("shop is not active")
	ErrCircuitOpen  = errors.New("user service circuit breaker open")
)

// Client verifies shop existence and ACTIVE status via UserService.GetShop
// with per-call timeout, bounded exponential retry, and a simple circuit breaker.
type Client struct {
	conn    *grpc.ClientConn
	client  userv1.UserServiceClient
	timeout time.Duration

	mu          sync.Mutex
	failures    int
	openUntil   time.Time
	maxFailures int
	cooldown    time.Duration
}

// New creates a lazy-connecting gRPC client to UserService.
func New(addr, serviceName string) (*Client, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil, errors.New("user grpc address is empty")
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
		conn:        conn,
		client:      userv1.NewUserServiceClient(conn),
		timeout:     2 * time.Second,
		maxFailures: 5,
		cooldown:    15 * time.Second,
	}, nil
}

// Close closes the underlying gRPC connection.
func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// VerifyActiveShop calls public UserService.GetShop to verify shopID exists and has status ACTIVE.
func (c *Client) VerifyActiveShop(ctx context.Context, shopID string) error {
	if c == nil || c.client == nil {
		return nil
	}
	if err := c.allowRequest(); err != nil {
		return err
	}

	// Forward end-user Bearer token if present (never mint a service token for public GetShop).
	outCtx := ctx
	if rawTok := auth.RawTokenFromContext(ctx); rawTok != "" {
		outCtx = auth.InjectOutgoingGRPC(outCtx, rawTok)
	}

	var lastErr error
	backoff := 100 * time.Millisecond
	for attempt := 0; attempt < 3; attempt++ {
		callCtx, cancel := context.WithTimeout(outCtx, c.timeout)
		resp, err := c.client.GetShop(callCtx, &userv1.GetShopRequest{ShopId: strings.TrimSpace(shopID)})
		cancel()
		if err == nil {
			c.recordSuccess()
			shop := resp.GetShop()
			if shop == nil {
				return fmt.Errorf("%w: empty shop response", ErrShopInactive)
			}
			if shop.GetStatus() != userv1.ShopStatus_SHOP_STATUS_ACTIVE {
				return fmt.Errorf("%w: status=%s", ErrShopInactive, shop.GetStatus().String())
			}
			return nil
		}

		switch status.Code(err) {
		case codes.NotFound:
			// Public UserService.GetShop returns NotFound for non-ACTIVE shops (PENDING/REJECTED/SUSPENDED)
			// as well as missing shops; map to ErrShopInactive so callers receive FailedPrecondition.
			c.recordSuccess()
			return fmt.Errorf("%w: shop not found or not active", ErrShopInactive)
		case codes.InvalidArgument:
			c.recordSuccess()
			return ErrShopNotFound
		case codes.Unavailable, codes.DeadlineExceeded, codes.ResourceExhausted:
			lastErr = err
			if attempt < 2 {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(backoff):
				}
				backoff *= 2
				continue
			}
			c.recordFailure()
			return fmt.Errorf("verify active shop: %w", lastErr)
		default:
			c.recordFailure()
			return fmt.Errorf("verify active shop: %w", err)
		}
	}
	c.recordFailure()
	return fmt.Errorf("verify active shop: %w", lastErr)
}

func (c *Client) allowRequest() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.openUntil.IsZero() && time.Now().Before(c.openUntil) {
		return ErrCircuitOpen
	}
	return nil
}

func (c *Client) recordSuccess() {
	c.mu.Lock()
	c.failures = 0
	c.openUntil = time.Time{}
	c.mu.Unlock()
}

func (c *Client) recordFailure() {
	c.mu.Lock()
	c.failures++
	if c.failures >= c.maxFailures {
		c.openUntil = time.Now().Add(c.cooldown)
	}
	c.mu.Unlock()
}
