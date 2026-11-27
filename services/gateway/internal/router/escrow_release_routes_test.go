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
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type escrowReleaseMockPaymentClient struct {
	paymentv1.PaymentServiceClient
	mu       sync.Mutex
	calls    int
	lastReq  *paymentv1.ReleaseEscrowRequest
	lastAuth string
	resp     *paymentv1.ReleaseEscrowResponse
	err      error
}

func (m *escrowReleaseMockPaymentClient) ReleaseEscrow(ctx context.Context, in *paymentv1.ReleaseEscrowRequest, _ ...grpc.CallOption) (*paymentv1.ReleaseEscrowResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	m.lastReq = in
	if md, ok := metadata.FromOutgoingContext(ctx); ok {
		if vals := md.Get(auth.MDAuthorization); len(vals) > 0 {
			m.lastAuth = vals[0]
		}
	}
	if m.err != nil {
		return nil, m.err
	}
	if m.resp != nil {
		return m.resp, nil
	}
	return &paymentv1.ReleaseEscrowResponse{
		Released:          true,
		ReleasedAmountVnd: 320000,
		AlreadyReleased:   false,
	}, nil
}

func TestAdminReleaseEscrowRoutePermissionsValidationAndAudit(t *testing.T) {
	mgr, err := auth.NewManager("escrow-gw-test-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	payMock := &escrowReleaseMockPaymentClient{}
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

	const validOrderID = "11111111-2222-4333-8444-555555555555"
	validPath := "/api/v1/admin/orders/" + validOrderID + "/escrow/release"

	do := func(target, tok string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, target, nil)
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// 1. Anonymous -> 401.
	if rec := do(validPath, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: want 401, got %d (%s)", rec.Code, rec.Body.String())
	}

	// 2. Unauthorized roles (buyer, seller, moderator, cskh, ops) -> 403 without hitting payment RPC.
	deniedRoles := []struct {
		role   string
		shopID string
	}{
		{auth.RoleBuyer, ""},
		{auth.RoleSeller, "shop-1"},
		{auth.RoleModerator, ""},
		{auth.RoleCSKH, ""},
		{auth.RoleOps, ""},
	}
	for _, tc := range deniedRoles {
		tok := sign("u-"+tc.role, tc.role, tc.shopID)
		rec := do(validPath, tok)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("role=%s: want 403, got %d (%s)", tc.role, rec.Code, rec.Body.String())
		}
	}
	if payMock.calls != 0 {
		t.Fatalf("expected 0 RPC calls for unauthorized roles, got %d", payMock.calls)
	}

	// 3. Invalid UUID -> 400 INVALID_ARGUMENT without hitting payment RPC.
	accountantTok := sign("acc-1", auth.RoleAccountant, "")
	for _, badID := range []string{"not-a-uuid", "ord-123", "11111111-2222-4333-8444-55555555555z"} {
		rec := do("/api/v1/admin/orders/"+badID+"/escrow/release", accountantTok)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":"INVALID_ARGUMENT"`) {
			t.Fatalf("bad id %q: want 400 INVALID_ARGUMENT, got %d (%s)", badID, rec.Code, rec.Body.String())
		}
	}
	if payMock.calls != 0 {
		t.Fatalf("expected 0 RPC calls for invalid UUIDs, got %d", payMock.calls)
	}

	// 4. Allowed roles (super_admin, admin, accountant) succeed with empty body + JWT forwarded + audit log written.
	for _, role := range []string{auth.RoleSuperAdmin, auth.RoleAdmin, auth.RoleAccountant} {
		userMock.mu.Lock()
		userMock.auditReqs = nil
		userMock.mu.Unlock()

		tok := sign("u-"+role, role, "")
		rec := do(validPath, tok)
		if rec.Code != http.StatusOK {
			t.Fatalf("role=%s: want 200, got %d (%s)", role, rec.Code, rec.Body.String())
		}
		var out struct {
			Released          bool  `json:"released"`
			ReleasedAmountVND int64 `json:"released_amount_vnd"`
			AlreadyReleased   bool  `json:"already_released"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("role=%s decode response: %v", role, err)
		}
		if !out.Released || out.ReleasedAmountVND != 320000 || out.AlreadyReleased {
			t.Fatalf("role=%s unexpected response: %+v", role, out)
		}
		if payMock.lastReq.GetOrderId() != validOrderID || payMock.lastReq.GetShopId() != "" {
			t.Fatalf("role=%s unexpected ReleaseEscrowRequest: %+v", role, payMock.lastReq)
		}
		if payMock.lastAuth != "Bearer "+tok {
			t.Fatalf("role=%s expected admin JWT forwarded in gRPC metadata", role)
		}

		userMock.mu.Lock()
		audits := append([]*userv1.WriteAuditLogRequest(nil), userMock.auditReqs...)
		userMock.mu.Unlock()
		if len(audits) != 1 {
			t.Fatalf("role=%s expected 1 audit log, got %d", role, len(audits))
		}
		if audits[0].GetTargetType() != "order" || audits[0].GetTargetId() != validOrderID ||
			audits[0].GetAction() != "POST /api/v1/admin/orders/{id}/escrow/release" ||
			!strings.Contains(audits[0].GetAfterJson(), `"released_amount_vnd":320000`) {
			t.Fatalf("role=%s unexpected audit entry: %+v", role, audits[0])
		}
	}

	// 5. Already released -> 200 with already_released=true.
	payMock.mu.Lock()
	payMock.resp = &paymentv1.ReleaseEscrowResponse{
		Released:          true,
		ReleasedAmountVnd: 0,
		AlreadyReleased:   true,
	}
	payMock.mu.Unlock()
	recAlready := do(validPath, accountantTok)
	if recAlready.Code != http.StatusOK || !strings.Contains(recAlready.Body.String(), `"already_released":true`) {
		t.Fatalf("already released: want 200 already_released=true, got %d (%s)", recAlready.Code, recAlready.Body.String())
	}

	// 6. Conflict from payment (codes.AlreadyExists) -> 409 FAILED_PRECONDITION (and codes.FailedPrecondition too).
	for _, grpcCode := range []codes.Code{codes.AlreadyExists, codes.FailedPrecondition} {
		payMock.mu.Lock()
		payMock.err = status.Error(grpcCode, "cannot release escrow for order: allocation is in status 'held'")
		payMock.mu.Unlock()

		rec := do(validPath, accountantTok)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"FAILED_PRECONDITION"`) {
			t.Fatalf("grpcCode=%s: want 409 FAILED_PRECONDITION, got %d (%s)", grpcCode, rec.Code, rec.Body.String())
		}
	}
}
