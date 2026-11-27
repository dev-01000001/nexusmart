package router

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/gateway/internal/clients"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type payoutMockPaymentClient struct {
	paymentv1.PaymentServiceClient
	mu             sync.Mutex
	balanceResp    *paymentv1.GetSellerBalanceResponse
	lastRequestReq *paymentv1.RequestSellerPayoutRequest
	requestResp    *paymentv1.RequestSellerPayoutResponse
	requestErr     error
	lastListReq    *paymentv1.ListSellerPayoutsRequest
	listResp       *paymentv1.ListSellerPayoutsResponse
	listErr        error
	lastDecideReq  *paymentv1.DecideSellerPayoutRequest
	decideResp     *paymentv1.DecideSellerPayoutResponse
	decideErr      error
}

func (m *payoutMockPaymentClient) GetSellerBalance(_ context.Context, in *paymentv1.GetSellerBalanceRequest, _ ...grpc.CallOption) (*paymentv1.GetSellerBalanceResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.balanceResp != nil {
		return m.balanceResp, nil
	}
	return &paymentv1.GetSellerBalanceResponse{
		ShopId:           in.GetShopId(),
		PendingVnd:       100000,
		AvailableVnd:     250000,
		TotalEarnedVnd:   500000,
		PayoutPendingVnd: 150000,
	}, nil
}

func (m *payoutMockPaymentClient) RequestSellerPayout(_ context.Context, in *paymentv1.RequestSellerPayoutRequest, _ ...grpc.CallOption) (*paymentv1.RequestSellerPayoutResponse, error) {
	m.mu.Lock()
	m.lastRequestReq = in
	resp, err := m.requestResp, m.requestErr
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if resp != nil {
		return resp, nil
	}
	return &paymentv1.RequestSellerPayoutResponse{
		Payout: &paymentv1.SellerPayout{
			Id:                "po-111",
			ShopId:            in.GetShopId(),
			AmountVnd:         in.GetAmountVnd(),
			Status:            "PENDING",
			BankName:          "Vietcombank",
			BankAccountMasked: "****0123",
			AccountHolderName: "NGUYEN VAN SHOP",
			RequestedBy:       in.GetRequestedByUserId(),
			CreatedAt:         timestamppb.Now(),
		},
	}, nil
}

func (m *payoutMockPaymentClient) ListSellerPayouts(_ context.Context, in *paymentv1.ListSellerPayoutsRequest, _ ...grpc.CallOption) (*paymentv1.ListSellerPayoutsResponse, error) {
	m.mu.Lock()
	m.lastListReq = in
	resp, err := m.listResp, m.listErr
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if resp != nil {
		return resp, nil
	}
	return &paymentv1.ListSellerPayoutsResponse{
		Items: []*paymentv1.SellerPayout{
			{
				Id:                "po-111",
				ShopId:            in.GetShopId(),
				AmountVnd:         150000,
				Status:            "PENDING",
				BankName:          "Vietcombank",
				BankAccountMasked: "****0123",
				AccountHolderName: "NGUYEN VAN SHOP",
				RequestedBy:       "seller-1",
				CreatedAt:         timestamppb.Now(),
			},
		},
		Page:     in.GetPage(),
		PageSize: in.GetPageSize(),
		Total:    1,
	}, nil
}

func (m *payoutMockPaymentClient) DecideSellerPayout(_ context.Context, in *paymentv1.DecideSellerPayoutRequest, _ ...grpc.CallOption) (*paymentv1.DecideSellerPayoutResponse, error) {
	m.mu.Lock()
	m.lastDecideReq = in
	resp, err := m.decideResp, m.decideErr
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if resp != nil {
		return resp, nil
	}
	now := timestamppb.Now()
	st := "COMPLETED"
	ref := "MOCKBANK-12345678"
	if in.GetDecision() == "REJECT" {
		st = "REJECTED"
		ref = ""
	}
	return &paymentv1.DecideSellerPayoutResponse{
		Payout: &paymentv1.SellerPayout{
			Id:                in.GetPayoutId(),
			ShopId:            "shop-a",
			AmountVnd:         150000,
			Status:            st,
			BankName:          "Vietcombank",
			BankAccountMasked: "****0123",
			AccountHolderName: "NGUYEN VAN SHOP",
			RejectReason:      in.GetReason(),
			TransferRef:       ref,
			RequestedBy:       "seller-1",
			DecidedBy:         in.GetDecidedByUserId(),
			CreatedAt:         now,
			DecidedAt:         now,
		},
	}, nil
}

type payoutMockUserClient struct {
	userv1.UserServiceClient
	mu        sync.Mutex
	auditReqs []*userv1.WriteAuditLogRequest
}

func (u *payoutMockUserClient) WriteAuditLog(_ context.Context, in *userv1.WriteAuditLogRequest, _ ...grpc.CallOption) (*userv1.WriteAuditLogResponse, error) {
	u.mu.Lock()
	u.auditReqs = append(u.auditReqs, in)
	u.mu.Unlock()
	return &userv1.WriteAuditLogResponse{AuditLog: &userv1.AuditLog{Id: "aud-po"}}, nil
}

func TestSellerAndAdminPayoutRoutesAndPermissions(t *testing.T) {
	mgr, err := auth.NewManager("payout-gw-test-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	payMock := &payoutMockPaymentClient{}
	userMock := &payoutMockUserClient{}

	h := New(Dependencies{
		ServiceName: "gateway-test",
		Clients: &clients.Clients{
			Payment: payMock,
			User:    userMock,
			Timeout: 5 * time.Second,
		},
		AuthManager: mgr,
		Logger:      logger.New("gateway-test", logger.WithWriter(io.Discard)),
	})

	sign := func(uid, role, shopID string) string {
		tok, _, _, sErr := mgr.SignAccessToken(uid, role, shopID)
		if sErr != nil {
			t.Fatalf("SignAccessToken: %v", sErr)
		}
		return tok
	}

	sellerATok := sign("seller-a", auth.RoleSeller, "shop-a")
	sellerBTok := sign("seller-b", auth.RoleSeller, "shop-b")
	sellerNoShopTok := sign("seller-none", auth.RoleSeller, "")
	buyerTok := sign("buyer-1", auth.RoleBuyer, "")
	adminTok := sign("admin-1", auth.RoleAdmin, "")
	accountantTok := sign("acc-1", auth.RoleAccountant, "")
	opsTok := sign("ops-1", auth.RoleOps, "")
	modTok := sign("mod-1", auth.RoleModerator, "")
	cskhTok := sign("cskh-1", auth.RoleCSKH, "")

	do := func(method, target, tok, body string, hdrs map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		for k, v := range hdrs {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// 1. GET /api/v1/seller/balance returns payout_pending_vnd
	recBal := do(http.MethodGet, "/api/v1/seller/balance", sellerATok, "", nil)
	if recBal.Code != http.StatusOK {
		t.Fatalf("GET /seller/balance: want 200, got %d (%s)", recBal.Code, recBal.Body.String())
	}
	var balBody struct {
		ShopID           string `json:"shop_id"`
		PendingVND       int64  `json:"pending_vnd"`
		AvailableVND     int64  `json:"available_vnd"`
		TotalEarnedVND   int64  `json:"total_earned_vnd"`
		PayoutPendingVND int64  `json:"payout_pending_vnd"`
	}
	if err := json.Unmarshal(recBal.Body.Bytes(), &balBody); err != nil {
		t.Fatalf("decode balance: %v", err)
	}
	if balBody.ShopID != "shop-a" || balBody.PayoutPendingVND != 150000 {
		t.Fatalf("unexpected balance payload: %+v", balBody)
	}

	// 2. GET /api/v1/seller/payouts — seller always scoped to JWT shop_id; buyer/admin/no-shop denied
	recList := do(http.MethodGet, "/api/v1/seller/payouts?shop_id=shop-b&page=1&page_size=10", sellerATok, "", nil)
	if recList.Code != http.StatusOK {
		t.Fatalf("GET /seller/payouts: want 200, got %d (%s)", recList.Code, recList.Body.String())
	}
	if payMock.lastListReq.GetShopId() != "shop-a" {
		t.Fatalf("seller list payouts must use JWT shop_id=shop-a, got %q", payMock.lastListReq.GetShopId())
	}
	for name, tok := range map[string]string{"buyer": buyerTok, "admin": adminTok, "seller_no_shop": sellerNoShopTok} {
		rec := do(http.MethodGet, "/api/v1/seller/payouts", tok, "", nil)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s GET /seller/payouts: want 403, got %d (%s)", name, rec.Code, rec.Body.String())
		}
	}

	// 3. POST /api/v1/seller/payouts — validation, auth, success, and domain errors
	// Missing Idempotency-Key -> 400 INVALID_ARGUMENT
	recNoKey := do(http.MethodPost, "/api/v1/seller/payouts", sellerATok, `{"amount_vnd":100000}`, nil)
	if recNoKey.Code != http.StatusBadRequest || !strings.Contains(recNoKey.Body.String(), `"INVALID_ARGUMENT"`) {
		t.Fatalf("missing key: want 400 INVALID_ARGUMENT, got %d (%s)", recNoKey.Code, recNoKey.Body.String())
	}
	// Amount <= 0 -> 400 INVALID_ARGUMENT
	recZero := do(http.MethodPost, "/api/v1/seller/payouts", sellerATok, `{"amount_vnd":0}`, map[string]string{"Idempotency-Key": "k-zero"})
	if recZero.Code != http.StatusBadRequest || !strings.Contains(recZero.Body.String(), `"INVALID_ARGUMENT"`) {
		t.Fatalf("zero amount: want 400 INVALID_ARGUMENT, got %d (%s)", recZero.Code, recZero.Body.String())
	}
	// Buyer / admin denied
	for name, tok := range map[string]string{"buyer": buyerTok, "admin": adminTok} {
		rec := do(http.MethodPost, "/api/v1/seller/payouts", tok, `{"amount_vnd":100000}`, map[string]string{"Idempotency-Key": "k-deny"})
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s POST /seller/payouts: want 403, got %d", name, rec.Code)
		}
	}

	// Valid seller request ignores any client shop_id and passes JWT shop_id -> 201
	recOK := do(http.MethodPost, "/api/v1/seller/payouts", sellerATok, `{"amount_vnd":150000,"shop_id":"shop-b"}`, map[string]string{"Idempotency-Key": "k-ok-1"})
	if recOK.Code != http.StatusCreated {
		t.Fatalf("POST /seller/payouts: want 201, got %d (%s)", recOK.Code, recOK.Body.String())
	}
	if payMock.lastRequestReq.GetShopId() != "shop-a" || payMock.lastRequestReq.GetIdempotencyKey() != "k-ok-1" {
		t.Fatalf("unexpected gRPC RequestSellerPayout req: %+v", payMock.lastRequestReq)
	}

	// Seller B calling gets shop-b, never shop-a
	recB := do(http.MethodPost, "/api/v1/seller/payouts", sellerBTok, `{"amount_vnd":50000}`, map[string]string{"Idempotency-Key": "k-b-1"})
	if recB.Code != http.StatusCreated || payMock.lastRequestReq.GetShopId() != "shop-b" {
		t.Fatalf("seller B payout failed: code=%d shop=%q", recB.Code, payMock.lastRequestReq.GetShopId())
	}

	// 409 INSUFFICIENT_BALANCE with available_vnd
	payMock.requestErr = status.Error(codes.FailedPrecondition, "INSUFFICIENT_BALANCE: available_vnd=80000 requested_vnd=150000")
	recInsuff := do(http.MethodPost, "/api/v1/seller/payouts", sellerATok, `{"amount_vnd":150000}`, map[string]string{"Idempotency-Key": "k-insuff"})
	if recInsuff.Code != http.StatusConflict {
		t.Fatalf("INSUFFICIENT_BALANCE: want 409, got %d (%s)", recInsuff.Code, recInsuff.Body.String())
	}
	var insuffEnv struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
		AvailableVND *int64 `json:"available_vnd"`
	}
	if err := json.Unmarshal(recInsuff.Body.Bytes(), &insuffEnv); err != nil {
		t.Fatalf("decode insuff: %v", err)
	}
	if insuffEnv.Error.Code != "INSUFFICIENT_BALANCE" || insuffEnv.AvailableVND == nil || *insuffEnv.AvailableVND != 80000 {
		t.Fatalf("INSUFFICIENT_BALANCE envelope mismatch: %s", recInsuff.Body.String())
	}

	// 409 PAYOUT_PENDING_EXISTS
	payMock.requestErr = status.Error(codes.FailedPrecondition, "PAYOUT_PENDING_EXISTS: shop already has a pending payout request")
	recPend := do(http.MethodPost, "/api/v1/seller/payouts", sellerATok, `{"amount_vnd":50000}`, map[string]string{"Idempotency-Key": "k-pend"})
	if recPend.Code != http.StatusConflict || !strings.Contains(recPend.Body.String(), `"PAYOUT_PENDING_EXISTS"`) {
		t.Fatalf("PAYOUT_PENDING_EXISTS: want 409, got %d (%s)", recPend.Code, recPend.Body.String())
	}

	// 412 BANK_ACCOUNT_NOT_READY
	payMock.requestErr = status.Error(codes.FailedPrecondition, "BANK_ACCOUNT_NOT_READY: bank account locked 24h")
	recBank := do(http.MethodPost, "/api/v1/seller/payouts", sellerATok, `{"amount_vnd":50000}`, map[string]string{"Idempotency-Key": "k-bank"})
	if recBank.Code != http.StatusPreconditionFailed || !strings.Contains(recBank.Body.String(), `"BANK_ACCOUNT_NOT_READY"`) {
		t.Fatalf("BANK_ACCOUNT_NOT_READY: want 412, got %d (%s)", recBank.Code, recBank.Body.String())
	}
	payMock.requestErr = nil

	// 4. GET /api/v1/admin/payouts — capability payouts.read (super_admin, admin, accountant, ops)
	for name, tok := range map[string]string{"admin": adminTok, "accountant": accountantTok, "ops": opsTok} {
		rec := do(http.MethodGet, "/api/v1/admin/payouts?status=PENDING&shop_id=shop-a", tok, "", nil)
		if rec.Code != http.StatusOK {
			t.Errorf("%s GET /admin/payouts: want 200, got %d (%s)", name, rec.Code, rec.Body.String())
		}
	}
	for name, tok := range map[string]string{"moderator": modTok, "cskh": cskhTok, "seller": sellerATok, "buyer": buyerTok} {
		rec := do(http.MethodGet, "/api/v1/admin/payouts", tok, "", nil)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s GET /admin/payouts: want 403, got %d (%s)", name, rec.Code, rec.Body.String())
		}
	}

	// 5. POST /api/v1/admin/payouts/{id}/decision — capability payouts.decide (super_admin, admin, accountant)
	for name, tok := range map[string]string{"ops": opsTok, "moderator": modTok, "cskh": cskhTok, "seller": sellerATok, "buyer": buyerTok} {
		rec := do(http.MethodPost, "/api/v1/admin/payouts/po-111/decision", tok, `{"decision":"APPROVE","reason":""}`, nil)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s POST /admin/payouts/po-111/decision: want 403, got %d (%s)", name, rec.Code, rec.Body.String())
		}
	}
	// REJECT missing reason -> 400 INVALID_ARGUMENT
	recBadRej := do(http.MethodPost, "/api/v1/admin/payouts/po-111/decision", accountantTok, `{"decision":"REJECT","reason":"   "}`, nil)
	if recBadRej.Code != http.StatusBadRequest || !strings.Contains(recBadRej.Body.String(), `"INVALID_ARGUMENT"`) {
		t.Fatalf("REJECT without reason: want 400 INVALID_ARGUMENT, got %d (%s)", recBadRej.Code, recBadRej.Body.String())
	}
	// APPROVE -> 200 + audit log
	recApprove := do(http.MethodPost, "/api/v1/admin/payouts/po-111/decision", accountantTok, `{"decision":"APPROVE","reason":""}`, nil)
	if recApprove.Code != http.StatusOK || !strings.Contains(recApprove.Body.String(), `"COMPLETED"`) || !strings.Contains(recApprove.Body.String(), `"MOCKBANK-`) {
		t.Fatalf("APPROVE payout: want 200 COMPLETED, got %d (%s)", recApprove.Code, recApprove.Body.String())
	}
	if payMock.lastDecideReq.GetDecidedByUserId() != "acc-1" {
		t.Fatalf("expected decided_by_user_id=acc-1, got %q", payMock.lastDecideReq.GetDecidedByUserId())
	}
	if len(userMock.auditReqs) == 0 || userMock.auditReqs[len(userMock.auditReqs)-1].GetTargetType() != "seller_payout" {
		t.Fatalf("expected seller_payout audit log, got %+v", userMock.auditReqs)
	}
	// Non-PENDING -> 409 FAILED_PRECONDITION
	payMock.decideErr = status.Error(codes.FailedPrecondition, "payout po-111 is already COMPLETED")
	recTwice := do(http.MethodPost, "/api/v1/admin/payouts/po-111/decision", adminTok, `{"decision":"APPROVE","reason":""}`, nil)
	if recTwice.Code != http.StatusConflict || !strings.Contains(recTwice.Body.String(), `"FAILED_PRECONDITION"`) {
		t.Fatalf("decide twice: want 409 FAILED_PRECONDITION, got %d (%s)", recTwice.Code, recTwice.Body.String())
	}
}
