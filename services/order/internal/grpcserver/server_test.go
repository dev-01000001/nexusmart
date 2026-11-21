package grpcserver

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/order/internal/clients"
	"github.com/nexusmart/core/services/order/internal/config"
	"github.com/nexusmart/core/services/order/internal/service"
	"github.com/nexusmart/core/services/order/internal/store"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func TestServer_AllRPCsWiredOverBufconn(t *testing.T) {
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
	log := logger.New("order-grpc-test", logger.WithWriter(io.Discard))
	svc := service.NewOrderService(cfg, st, &clients.Bundle{}, log)

	userMgr, err := auth.NewManager("grpc-server-user-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("NewManager user: %v", err)
	}
	svcMgr, err := auth.NewManager("grpc-server-svc-secret", auth.ServiceTokenTTL)
	if err != nil {
		t.Fatalf("NewManager svc: %v", err)
	}

	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer(grpc.ChainUnaryInterceptor(auth.GRPCUnaryServerInterceptor(userMgr, svcMgr)))
	orderv1.RegisterOrderServiceServer(gs, New(svc))
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := orderv1.NewOrderServiceClient(conn)

	adminTok, _, _, err := userMgr.SignAccessToken("admin-1", "admin", "")
	if err != nil {
		t.Fatalf("sign admin token: %v", err)
	}
	svcTok, err := svcMgr.SignServiceToken("payment-service")
	if err != nil {
		t.Fatalf("sign svc token: %v", err)
	}
	adminCtx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+adminTok))
	svcCtx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+svcTok))

	// Exercise every RPC wrapper on grpcserver.Server so all 17 handlers are verified wired end-to-end.
	if _, err := client.CreateCheckout(adminCtx, &orderv1.CreateCheckoutRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("CreateCheckout = %v, want InvalidArgument", err)
	}
	if _, err := client.GetOrder(adminCtx, &orderv1.GetOrderRequest{OrderId: "missing"}); status.Code(err) != codes.NotFound {
		t.Fatalf("GetOrder = %v, want NotFound", err)
	}
	if resp, err := client.ListOrders(adminCtx, &orderv1.ListOrdersRequest{RoleScope: "admin"}); err != nil || resp.GetTotal() != 0 {
		t.Fatalf("ListOrders = (%+v, %v), want empty OK", resp, err)
	}
	if _, err := client.CancelOrder(adminCtx, &orderv1.CancelOrderRequest{OrderId: "missing"}); status.Code(err) != codes.NotFound {
		t.Fatalf("CancelOrder = %v, want NotFound", err)
	}
	if _, err := client.SellerConfirmOrder(adminCtx, &orderv1.SellerConfirmOrderRequest{OrderId: "missing"}); status.Code(err) != codes.NotFound {
		t.Fatalf("SellerConfirmOrder = %v, want NotFound", err)
	}
	if _, err := client.SellerShipOrder(adminCtx, &orderv1.SellerShipOrderRequest{OrderId: "missing"}); status.Code(err) != codes.NotFound {
		t.Fatalf("SellerShipOrder = %v, want NotFound", err)
	}
	if _, err := client.BuyerConfirmReceived(adminCtx, &orderv1.BuyerConfirmReceivedRequest{OrderId: "missing"}); status.Code(err) != codes.NotFound {
		t.Fatalf("BuyerConfirmReceived = %v, want NotFound", err)
	}
	if _, err := client.RequestReturn(adminCtx, &orderv1.RequestReturnRequest{OrderId: "missing", Reason: "damaged"}); status.Code(err) != codes.NotFound {
		t.Fatalf("RequestReturn = %v, want NotFound", err)
	}
	if resp, err := client.ListOrderReturns(adminCtx, &orderv1.ListOrderReturnsRequest{}); err != nil || resp.GetTotal() != 0 {
		t.Fatalf("ListOrderReturns = (%+v, %v), want empty OK", resp, err)
	}
	if _, err := client.ApproveReturn(adminCtx, &orderv1.ApproveReturnRequest{ReturnRequestId: "missing"}); status.Code(err) != codes.NotFound {
		t.Fatalf("ApproveReturn = %v, want NotFound", err)
	}
	if _, err := client.RejectReturn(adminCtx, &orderv1.RejectReturnRequest{ReturnRequestId: "missing", Reason: "no"}); status.Code(err) != codes.NotFound {
		t.Fatalf("RejectReturn = %v, want NotFound", err)
	}
	if _, err := client.ConfirmReturnReceived(adminCtx, &orderv1.ConfirmReturnReceivedRequest{ReturnRequestId: "missing"}); status.Code(err) != codes.NotFound {
		t.Fatalf("ConfirmReturnReceived = %v, want NotFound", err)
	}
	if _, err := client.CreateComplaint(adminCtx, &orderv1.CreateComplaintRequest{ReturnRequestId: "missing", Reason: "dispute"}); status.Code(err) != codes.NotFound {
		t.Fatalf("CreateComplaint = %v, want NotFound", err)
	}
	if resp, err := client.ListComplaints(adminCtx, &orderv1.ListComplaintsRequest{}); err != nil || resp.GetTotal() != 0 {
		t.Fatalf("ListComplaints = (%+v, %v), want empty OK", resp, err)
	}
	if _, err := client.ResolveComplaint(adminCtx, &orderv1.ResolveComplaintRequest{
		ComplaintId: "missing",
		Resolution:  orderv1.ComplaintResolution_COMPLAINT_RESOLUTION_DISMISS,
	}); status.Code(err) != codes.NotFound {
		t.Fatalf("ResolveComplaint = %v, want NotFound", err)
	}
	if _, err := client.GetOrderTimeline(adminCtx, &orderv1.GetOrderTimelineRequest{OrderId: "missing"}); status.Code(err) != codes.NotFound {
		t.Fatalf("GetOrderTimeline = %v, want NotFound", err)
	}
	if resp, err := client.MarkPaymentResult(svcCtx, &orderv1.MarkPaymentResultRequest{
		CheckoutGroupId: "grp-1",
		Status:          orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_SUCCEEDED,
	}); err != nil || !resp.GetOk() {
		t.Fatalf("MarkPaymentResult = (%+v, %v), want ok", resp, err)
	}
}
