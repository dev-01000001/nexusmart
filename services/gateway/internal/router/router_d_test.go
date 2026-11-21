package router

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/gateway/internal/clients"
	"github.com/nexusmart/core/services/gateway/internal/proxy"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type fakeOrderDepthClient struct {
	orderv1.OrderServiceClient
	lastReturnReq    *orderv1.RequestReturnRequest
	lastListRetReq   *orderv1.ListOrderReturnsRequest
	lastApproveReq   *orderv1.ApproveReturnRequest
	lastRejectReq    *orderv1.RejectReturnRequest
	lastConfirmReq   *orderv1.ConfirmReturnReceivedRequest
	lastComplaintReq *orderv1.CreateComplaintRequest
	lastListCompReq  *orderv1.ListComplaintsRequest
	lastResolveReq   *orderv1.ResolveComplaintRequest
	lastCheckoutReq  *orderv1.CreateCheckoutRequest
}

func (f *fakeOrderDepthClient) CreateCheckout(_ context.Context, in *orderv1.CreateCheckoutRequest, _ ...grpc.CallOption) (*orderv1.CreateCheckoutResponse, error) {
	f.lastCheckoutReq = in
	return &orderv1.CreateCheckoutResponse{
		CheckoutGroupId: "grp-zp-1",
		PaymentId:       "pay-zp-1",
		PaymentMethod:   in.GetPaymentMethod(),
		PaymentStatus:   "PENDING",
		QrUrl:           "https://sbgateway.zalopay.vn/openinapp?order=mock",
		QrCode:          "zp-token-mock",
		TotalPayableVnd: 150000,
		Orders: []*orderv1.Order{
			{
				Id:            "ord-zp-1",
				Status:        orderv1.OrderStatus_ORDER_STATUS_PENDING,
				PaymentMethod: in.GetPaymentMethod(),
			},
		},
	}, nil
}

func (f *fakeOrderDepthClient) RequestReturn(_ context.Context, in *orderv1.RequestReturnRequest, _ ...grpc.CallOption) (*orderv1.RequestReturnResponse, error) {
	f.lastReturnReq = in
	now := timestamppb.Now()
	return &orderv1.RequestReturnResponse{
		ReturnRequestId: "ret-1",
		Order: &orderv1.Order{
			Id:     in.GetOrderId(),
			Status: orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED,
		},
		ReturnRequest: &orderv1.ReturnRequest{
			Id:              "ret-1",
			OrderId:         in.GetOrderId(),
			BuyerId:         in.GetBuyerId(),
			ShopId:          "shop-42",
			Items:           in.GetItems(),
			Reason:          in.GetReason(),
			Photos:          in.GetEvidenceUrls(),
			Status:          orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_PENDING,
			RefundAmountVnd: 150000,
			CreatedAt:       now,
			UpdatedAt:       now,
		},
	}, nil
}

func (f *fakeOrderDepthClient) ListOrderReturns(_ context.Context, in *orderv1.ListOrderReturnsRequest, _ ...grpc.CallOption) (*orderv1.ListOrderReturnsResponse, error) {
	f.lastListRetReq = in
	return &orderv1.ListOrderReturnsResponse{
		Returns: []*orderv1.ReturnRequest{
			{
				Id:              "ret-1",
				OrderId:         in.GetOrderId(),
				ShopId:          "shop-42",
				Reason:          "Defective item",
				Status:          orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_PENDING,
				RefundAmountVnd: 150000,
			},
		},
		Total: 1,
	}, nil
}

func (f *fakeOrderDepthClient) ApproveReturn(_ context.Context, in *orderv1.ApproveReturnRequest, _ ...grpc.CallOption) (*orderv1.ApproveReturnResponse, error) {
	f.lastApproveReq = in
	status := orderv1.OrderStatus_ORDER_STATUS_RETURNING
	rrStatus := orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_RETURNING
	if in.GetImmediateRefund() {
		status = orderv1.OrderStatus_ORDER_STATUS_REFUNDED
		rrStatus = orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_REFUNDED
	}
	return &orderv1.ApproveReturnResponse{
		Order: &orderv1.Order{Id: "ord-1", Status: status},
		ReturnRequest: &orderv1.ReturnRequest{
			Id:                  in.GetReturnRequestId(),
			OrderId:             "ord-1",
			ShopId:              in.GetShopId(),
			Status:              rrStatus,
			ReverseTrackingCode: "GHTK-RET001",
			ReverseLabelUrl:     "https://sandbox.ghtk.local/labels/GHTK-RET001.pdf",
		},
	}, nil
}

func (f *fakeOrderDepthClient) RejectReturn(_ context.Context, in *orderv1.RejectReturnRequest, _ ...grpc.CallOption) (*orderv1.RejectReturnResponse, error) {
	f.lastRejectReq = in
	return &orderv1.RejectReturnResponse{
		Order: &orderv1.Order{Id: "ord-1", Status: orderv1.OrderStatus_ORDER_STATUS_RETURN_REJECTED},
		ReturnRequest: &orderv1.ReturnRequest{
			Id:         in.GetReturnRequestId(),
			OrderId:    "ord-1",
			Status:     orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_REJECTED,
			SellerNote: in.GetReason(),
		},
		Complaint: &orderv1.Complaint{
			Id:              "cmp-1",
			ReturnRequestId: in.GetReturnRequestId(),
			OrderId:         "ord-1",
			ShopId:          in.GetShopId(),
			Status:          orderv1.ComplaintStatus_COMPLAINT_STATUS_OPEN,
			Reason:          in.GetReason(),
		},
	}, nil
}

func (f *fakeOrderDepthClient) ConfirmReturnReceived(_ context.Context, in *orderv1.ConfirmReturnReceivedRequest, _ ...grpc.CallOption) (*orderv1.ConfirmReturnReceivedResponse, error) {
	f.lastConfirmReq = in
	return &orderv1.ConfirmReturnReceivedResponse{
		Order: &orderv1.Order{Id: "ord-1", Status: orderv1.OrderStatus_ORDER_STATUS_REFUNDED},
		ReturnRequest: &orderv1.ReturnRequest{
			Id:       in.GetReturnRequestId(),
			OrderId:  "ord-1",
			Status:   orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_REFUNDED,
			RefundId: "ref-1",
		},
	}, nil
}

func (f *fakeOrderDepthClient) CreateComplaint(_ context.Context, in *orderv1.CreateComplaintRequest, _ ...grpc.CallOption) (*orderv1.CreateComplaintResponse, error) {
	f.lastComplaintReq = in
	return &orderv1.CreateComplaintResponse{
		Complaint: &orderv1.Complaint{
			Id:              "cmp-1",
			ReturnRequestId: in.GetReturnRequestId(),
			OrderId:         in.GetOrderId(),
			BuyerId:         in.GetBuyerId(),
			Reason:          in.GetReason(),
			EvidenceUrls:    in.GetEvidenceUrls(),
			Status:          orderv1.ComplaintStatus_COMPLAINT_STATUS_OPEN,
		},
	}, nil
}

func (f *fakeOrderDepthClient) ListComplaints(_ context.Context, in *orderv1.ListComplaintsRequest, _ ...grpc.CallOption) (*orderv1.ListComplaintsResponse, error) {
	f.lastListCompReq = in
	return &orderv1.ListComplaintsResponse{
		Complaints: []*orderv1.Complaint{
			{
				Id:              "cmp-1",
				ReturnRequestId: "ret-1",
				OrderId:         "ord-1",
				Status:          orderv1.ComplaintStatus_COMPLAINT_STATUS_OPEN,
			},
		},
		Total: 1,
	}, nil
}

func (f *fakeOrderDepthClient) ResolveComplaint(_ context.Context, in *orderv1.ResolveComplaintRequest, _ ...grpc.CallOption) (*orderv1.ResolveComplaintResponse, error) {
	f.lastResolveReq = in
	return &orderv1.ResolveComplaintResponse{
		Complaint: &orderv1.Complaint{
			Id:         in.GetComplaintId(),
			Status:     orderv1.ComplaintStatus_COMPLAINT_STATUS_RESOLVED,
			Resolution: in.GetResolution(),
			AdminNote:  in.GetAdminNote(),
		},
		Order: &orderv1.Order{
			Id:     "ord-1",
			Status: orderv1.OrderStatus_ORDER_STATUS_REFUNDED,
		},
		ReturnRequest: &orderv1.ReturnRequest{
			Id:     "ret-1",
			Status: orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_REFUNDED,
		},
	}, nil
}

func (f *fakeOrderDepthClient) GetOrder(_ context.Context, in *orderv1.GetOrderRequest, _ ...grpc.CallOption) (*orderv1.GetOrderResponse, error) {
	return &orderv1.GetOrderResponse{
		Order: &orderv1.Order{Id: in.GetOrderId(), BuyerId: "buyer-1", ShopId: "shop-42"},
	}, nil
}

type fakePaymentDepthClient struct {
	paymentv1.PaymentServiceClient
	lastReportsReq *paymentv1.ListReconciliationReportsRequest
}

func (f *fakePaymentDepthClient) ListReconciliationReports(_ context.Context, in *paymentv1.ListReconciliationReportsRequest, _ ...grpc.CallOption) (*paymentv1.ListReconciliationReportsResponse, error) {
	f.lastReportsReq = in
	return &paymentv1.ListReconciliationReportsResponse{
		Reports: []*paymentv1.ReconciliationReportSummary{
			{
				RunId:           "run-1",
				Provider:        "vietqr_mock+zalopay_mock",
				RunDate:         "2026-11-21",
				MatchedCount:    4,
				AutoHealedCount: 1,
				Status:          "completed",
			},
		},
	}, nil
}

type fakeShippingDepthClient struct {
	shippingv1.ShippingServiceClient
	lastGetReq *shippingv1.GetShipmentRequest
}

func (f *fakeShippingDepthClient) GetShipment(_ context.Context, in *shippingv1.GetShipmentRequest, _ ...grpc.CallOption) (*shippingv1.GetShipmentResponse, error) {
	f.lastGetReq = in
	return &shippingv1.GetShipmentResponse{
		Shipment: &shippingv1.Shipment{
			ShipmentId:      "ship-ret-1",
			OrderId:         in.GetOrderId(),
			ShopId:          "shop-42",
			CarrierCode:     "ghtk",
			TrackingCode:    "GHTK-RET001",
			Status:          shippingv1.ShipmentStatus_SHIPMENT_STATUS_CREATED,
			Direction:       in.GetDirection(),
			ReturnRequestId: "ret-1",
		},
	}, nil
}

func TestGatewayOrderDepthRoutesAndRBAC(t *testing.T) {
	authMgr, err := auth.NewManager("router-d-test-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	buyerTok, _, _, _ := authMgr.SignAccessToken("buyer-1", "buyer", "")
	sellerTok, _, _, _ := authMgr.SignAccessToken("seller-1", "seller", "shop-42")
	adminTok, _, _, _ := authMgr.SignAccessToken("admin-1", "admin", "")

	orderMock := &fakeOrderDepthClient{}
	paymentMock := &fakePaymentDepthClient{}
	shippingMock := &fakeShippingDepthClient{}

	webhookUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"return_code": 1,
			"path":        r.URL.Path,
			"len":         len(raw),
		})
	}))
	defer webhookUpstream.Close()

	log := logger.New("gateway-d-test", logger.WithWriter(io.Discard))
	wp, _ := proxy.NewWebhookProxy(webhookUpstream.URL, log)

	h := New(Dependencies{
		ServiceName: "gateway-d-test",
		Clients: &clients.Clients{
			Order:    orderMock,
			Payment:  paymentMock,
			Shipping: shippingMock,
			Timeout:  5 * time.Second,
		},
		AuthManager:         authMgr,
		PaymentWebhookProxy: wp,
		Logger:              log,
	})

	// 1. ZaloPay checkout maps enum cleanly.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/checkout", strings.NewReader(`{"address_id":"a1","from_cart":true,"payment_method":"ZALOPAY"}`))
	req.Header.Set("Authorization", "Bearer "+buyerTok)
	req.Header.Set("Idempotency-Key", "idem-zp-1")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for ZALOPAY checkout, got %d (%s)", rec.Code, rec.Body.String())
	}
	if orderMock.lastCheckoutReq.GetPaymentMethod() != orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY {
		t.Fatalf("expected ZALOPAY enum, got %v", orderMock.lastCheckoutReq.GetPaymentMethod())
	}

	// 2. ZaloPay webhook proxies raw body without JWT.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/webhooks/zalopay", strings.NewReader(`{"data":"{}","mac":"abc","type":1}`))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"/webhooks/zalopay"`) {
		t.Fatalf("expected proxied /webhooks/zalopay 200, got %d (%s)", rec.Code, rec.Body.String())
	}

	// 3. Buyer creates & lists return requests (contract: POST/GET /api/v1/orders/{id}/returns).
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/orders/ord-1/returns",
		strings.NewReader(`{"items":[{"sku_id":"sku-1","quantity":1}],"reason":"Broken screen","photos":["https://cdn.local/1.jpg"]}`))
	req.Header.Set("Authorization", "Bearer "+buyerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for create return, got %d (%s)", rec.Code, rec.Body.String())
	}
	if len(orderMock.lastReturnReq.GetItems()) != 1 || len(orderMock.lastReturnReq.GetEvidenceUrls()) != 1 {
		t.Fatalf("return request fields not mapped: %+v", orderMock.lastReturnReq)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/orders/ord-1/returns", nil)
	req.Header.Set("Authorization", "Bearer "+buyerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ret-1"`) {
		t.Fatalf("expected 200 for list returns, got %d (%s)", rec.Code, rec.Body.String())
	}

	// 4. Seller RBAC + approve/reject/confirm-received.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/seller/returns/ret-1/approve", nil)
	req.Header.Set("Authorization", "Bearer "+buyerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for buyer calling seller approve, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/seller/returns/ret-1/approve", strings.NewReader(`{"carrier_code":"ghtk"}`))
	req.Header.Set("Authorization", "Bearer "+sellerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"RETURNING"`) {
		t.Fatalf("expected 200 RETURNING on seller approve, got %d (%s)", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/seller/returns/ret-1/confirm-received", strings.NewReader(`{"note":"Item inspected"}`))
	req.Header.Set("Authorization", "Bearer "+sellerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"REFUNDED"`) {
		t.Fatalf("expected 200 REFUNDED on confirm received, got %d (%s)", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/seller/returns/ret-1/reject", strings.NewReader(`{"reason":"Seal broken"}`))
	req.Header.Set("Authorization", "Bearer "+sellerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"cmp-1"`) {
		t.Fatalf("expected 200 with escalated complaint on reject, got %d (%s)", rec.Code, rec.Body.String())
	}

	// 5. Buyer escalates / updates complaint.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/orders/ord-1/complaints",
		strings.NewReader(`{"return_request_id":"ret-1","reason":"Unboxing video proves defect","photos":["https://cdn.local/vid.mp4"]}`))
	req.Header.Set("Authorization", "Bearer "+buyerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 on create complaint, got %d (%s)", rec.Code, rec.Body.String())
	}

	// 6. Admin complaints RBAC + list + resolve + reconciliation reports.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/admin/complaints", nil)
	req.Header.Set("Authorization", "Bearer "+sellerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for seller calling admin complaints, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/admin/complaints?status=OPEN", nil)
	req.Header.Set("Authorization", "Bearer "+adminTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || orderMock.lastListCompReq.GetStatus() != orderv1.ComplaintStatus_COMPLAINT_STATUS_OPEN {
		t.Fatalf("expected 200 admin list complaints, got %d (%s)", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/admin/complaints/cmp-1/resolve",
		strings.NewReader(`{"resolution":"refund","admin_note":"Approved in buyer favor"}`))
	req.Header.Set("Authorization", "Bearer "+adminTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || orderMock.lastResolveReq.GetResolution() != orderv1.ComplaintResolution_COMPLAINT_RESOLUTION_REFUND {
		t.Fatalf("expected 200 admin resolve complaint, got %d (%s)", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/admin/payments/reconciliation-reports?limit=10", nil)
	req.Header.Set("Authorization", "Bearer "+adminTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || paymentMock.lastReportsReq.GetLimit() != 10 {
		t.Fatalf("expected 200 reconciliation reports, got %d (%s)", rec.Code, rec.Body.String())
	}

	// 7. Shipment lookup forwards direction=return.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/shipping/shipments/ord-1?direction=return", nil)
	req.Header.Set("Authorization", "Bearer "+buyerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || shippingMock.lastGetReq.GetDirection() != "return" {
		t.Fatalf("expected 200 return shipment lookup, got %d (%s)", rec.Code, rec.Body.String())
	}
}
