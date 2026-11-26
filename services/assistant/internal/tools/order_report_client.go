package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/assistant/internal/resilience"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// SellerReportClient fetches authoritative seller report metrics from OrderService.GetSellerReport
// using strictly the seller's own end-user JWT (never a service token or elevated credential).
type SellerReportClient interface {
	GetSellerReport(ctx context.Context, rawBearer string, req *orderv1.GetSellerReportRequest) (*orderv1.GetSellerReportResponse, error)
}

// GRPCOrderReportClient calls OrderService.GetSellerReport over gRPC with timeout, retry, and circuit breaker.
type GRPCOrderReportClient struct {
	conn    *grpc.ClientConn
	client  orderv1.OrderServiceClient
	timeout time.Duration
	cb      *resilience.CircuitBreaker
}

// NewGRPCOrderReportClient dials order gRPC lazily with OpenTelemetry propagation.
func NewGRPCOrderReportClient(addr, serviceName string, timeout time.Duration, cb *resilience.CircuitBreaker) (*GRPCOrderReportClient, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil, fmt.Errorf("order gRPC address is required")
	}
	if serviceName == "" {
		serviceName = "assistant"
	}
	conn, err := grpc.NewClient(
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(telemetry.GRPCUnaryClientInterceptor(serviceName)),
	)
	if err != nil {
		return nil, fmt.Errorf("init order grpc client (%s): %w", addr, err)
	}
	c := NewGRPCOrderReportClientWithClient(orderv1.NewOrderServiceClient(conn), timeout, cb)
	c.conn = conn
	return c, nil
}

// NewGRPCOrderReportClientWithClient wraps an existing OrderServiceClient (for unit tests).
func NewGRPCOrderReportClientWithClient(client orderv1.OrderServiceClient, timeout time.Duration, cb *resilience.CircuitBreaker) *GRPCOrderReportClient {
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	if cb == nil {
		cb = resilience.NewCircuitBreaker(5, 15*time.Second)
	}
	return &GRPCOrderReportClient{
		client:  client,
		timeout: timeout,
		cb:      cb,
	}
}

// Close closes the underlying gRPC connection if owned by this client.
func (c *GRPCOrderReportClient) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// GetSellerReport calls OrderService.GetSellerReport forwarding strictly the seller's own JWT.
func (c *GRPCOrderReportClient) GetSellerReport(
	ctx context.Context,
	rawBearer string,
	req *orderv1.GetSellerReportRequest,
) (*orderv1.GetSellerReportResponse, error) {
	if c == nil || c.client == nil {
		return nil, fmt.Errorf("order report client not configured")
	}
	token := sanitizeBearerToken(rawBearer)
	if token == "" {
		return nil, ErrMissingBearerToken
	}

	return resilience.CallWithRetry(
		ctx,
		c.cb,
		3,
		100*time.Millisecond,
		c.timeout,
		resilience.IsTransientGRPCError,
		func(attemptCtx context.Context) (*orderv1.GetSellerReportResponse, error) {
			callCtx := replaceOutgoingAuthorization(attemptCtx, "Bearer "+token)
			return c.client.GetSellerReport(callCtx, req)
		},
	)
}

func replaceOutgoingAuthorization(ctx context.Context, rawBearer string) context.Context {
	if md, ok := metadata.FromOutgoingContext(ctx); ok {
		cloned := md.Copy()
		cloned.Delete(auth.MDAuthorization)
		ctx = metadata.NewOutgoingContext(ctx, cloned)
	}
	return auth.InjectOutgoingGRPC(ctx, rawBearer)
}

func vietnamReportLocation() *time.Location {
	if loc, err := time.LoadLocation("Asia/Ho_Chi_Minh"); err == nil && loc != nil {
		return loc
	}
	return time.FixedZone("Asia/Ho_Chi_Minh", 7*3600)
}

// reportDateRangeForPeriod maps an assistant period label onto inclusive Asia/Ho_Chi_Minh
// YYYY-MM-DD bounds matching OrderService.GetSellerReport calendar semantics.
func reportDateRangeForPeriod(period string, now time.Time) (normalized, fromDate, toDate string) {
	loc := vietnamReportLocation()
	nowVN := now.In(loc)
	todayVN := time.Date(nowVN.Year(), nowVN.Month(), nowVN.Day(), 0, 0, 0, 0, loc)
	toDate = todayVN.Format("2006-01-02")

	switch strings.ToLower(strings.TrimSpace(period)) {
	case "today", "1d", "hom_nay":
		return "today", toDate, toDate
	case "30d", "month", "this_month", "thang":
		return "30d", todayVN.AddDate(0, 0, -29).Format("2006-01-02"), toDate
	case "90d", "quarter", "this_quarter", "quy":
		return "90d", todayVN.AddDate(0, 0, -89).Format("2006-01-02"), toDate
	default:
		return "7d", todayVN.AddDate(0, 0, -6).Format("2006-01-02"), toDate
	}
}
