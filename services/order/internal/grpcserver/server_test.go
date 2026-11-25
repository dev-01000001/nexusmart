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
		ServiceName:               "order",
		CODMaxRejectedCount:       3,
		FlashPaymentExpiryMinutes: 5,
		RewardCoinsBPS:            100,
		GRPCCallTimeout:           5 * time.Second,
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
	sellerTok, _, _, err := userMgr.SignAccessToken("seller-1", "seller", "shop-1")
	if err != nil {
		t.Fatalf("sign seller token: %v", err)
	}
	svcTok, err := svcMgr.SignServiceToken("payment-service")
	if err != nil {
		t.Fatalf("sign svc token: %v", err)
	}
	adminCtx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+adminTok))
	sellerCtx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+sellerTok))
	svcCtx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+svcTok))

	// Exercise all 27 RPC wrappers on grpcserver.Server over bufconn.
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
	if _, err := client.GetOrderTimeline(adminCtx, &orderv1.GetOrderTimelineRequest{OrderId: "missing"}); status.Code(err) != codes.NotFound {
		t.Fatalf("GetOrderTimeline = %v, want NotFound", err)
	}
	if resp, err := client.MarkPaymentResult(svcCtx, &orderv1.MarkPaymentResultRequest{
		CheckoutGroupId: "grp-1",
		Status:          orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_SUCCEEDED,
	}); err != nil || !resp.GetOk() {
		t.Fatalf("MarkPaymentResult = (%+v, %v), want ok", resp, err)
	}
	if _, err := client.RetryPayment(adminCtx, &orderv1.RetryPaymentRequest{PaymentId: "missing"}); status.Code(err) != codes.NotFound {
		t.Fatalf("RetryPayment = %v, want NotFound", err)
	}
	if _, err := client.GetReturnRequest(adminCtx, &orderv1.GetReturnRequestRequest{ReturnRequestId: "missing"}); status.Code(err) != codes.NotFound {
		t.Fatalf("GetReturnRequest = %v, want NotFound", err)
	}
	if resp, err := client.ListReturnRequests(adminCtx, &orderv1.ListReturnRequestsRequest{RoleScope: "admin"}); err != nil || resp.GetTotal() != 0 {
		t.Fatalf("ListReturnRequests = (%+v, %v), want empty OK", resp, err)
	}
	if _, err := client.RespondReturn(adminCtx, &orderv1.RespondReturnRequest{
		ReturnRequestId: "missing",
		Decision:        orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_RETURN,
	}); status.Code(err) != codes.NotFound {
		t.Fatalf("RespondReturn = %v, want NotFound", err)
	}
	if _, err := client.EscalateReturn(adminCtx, &orderv1.EscalateReturnRequest{ReturnRequestId: "missing", Reason: "dispute"}); status.Code(err) != codes.NotFound {
		t.Fatalf("EscalateReturn = %v, want NotFound", err)
	}
	if _, err := client.AddReturnEvidence(adminCtx, &orderv1.AddReturnEvidenceRequest{ReturnRequestId: "missing", EvidenceUrls: []string{"https://example.com/a.jpg"}}); status.Code(err) != codes.NotFound {
		t.Fatalf("AddReturnEvidence = %v, want NotFound", err)
	}
	if _, err := client.ConfirmReturnReceived(adminCtx, &orderv1.ConfirmReturnReceivedRequest{ReturnRequestId: "missing"}); status.Code(err) != codes.NotFound {
		t.Fatalf("ConfirmReturnReceived = %v, want NotFound", err)
	}
	if _, err := client.DisputeReturnGoods(adminCtx, &orderv1.DisputeReturnGoodsRequest{ReturnRequestId: "missing", Reason: "damaged"}); status.Code(err) != codes.NotFound {
		t.Fatalf("DisputeReturnGoods = %v, want NotFound", err)
	}
	if _, err := client.AdminResolveReturn(adminCtx, &orderv1.AdminResolveReturnRequest{
		ReturnRequestId: "missing",
		Decision:        orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_REJECT,
		Note:            "closed",
	}); status.Code(err) != codes.NotFound {
		t.Fatalf("AdminResolveReturn = %v, want NotFound", err)
	}
	if _, err := client.GetSellerReport(sellerCtx, &orderv1.GetSellerReportRequest{ShopId: "shop-1"}); err != nil {
		t.Fatalf("GetSellerReport = %v, want OK", err)
	}
	if _, err := client.GetPlatformReport(adminCtx, &orderv1.GetPlatformReportRequest{}); err != nil {
		t.Fatalf("GetPlatformReport = %v, want OK", err)
	}
	if resp, err := client.ListOrdersNeedingReview(adminCtx, &orderv1.ListOrdersNeedingReviewRequest{}); err != nil || resp.GetTotal() != 0 {
		t.Fatalf("ListOrdersNeedingReview = (%+v, %v), want empty OK", resp, err)
	}
	if _, err := client.ResolveOrderReview(adminCtx, &orderv1.ResolveOrderReviewRequest{OrderId: "missing", Note: "ok"}); status.Code(err) != codes.NotFound {
		t.Fatalf("ResolveOrderReview = %v, want NotFound", err)
	}
	if _, err := client.LookupOrder(adminCtx, &orderv1.LookupOrderRequest{OrderCode: "NM-MISSING", Phone: "0901234567"}); status.Code(err) != codes.NotFound {
		t.Fatalf("LookupOrder = %v, want NotFound", err)
	}
	if _, err := client.UpdateOrderAddress(adminCtx, &orderv1.UpdateOrderAddressRequest{OrderId: "missing", IdempotencyKey: "k1"}); status.Code(err) != codes.InvalidArgument && status.Code(err) != codes.NotFound {
		t.Fatalf("UpdateOrderAddress = %v, want InvalidArgument/NotFound", err)
	}
	if _, err := client.BatchShipOrders(sellerCtx, &orderv1.BatchShipOrdersRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("BatchShipOrders = %v, want InvalidArgument", err)
	}
	if _, err := client.GetPickingList(sellerCtx, &orderv1.GetPickingListRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("GetPickingList = %v, want InvalidArgument", err)
	}
}
