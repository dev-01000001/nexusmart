package e2e

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type payW2bOrderFeeDTO struct {
	ID             string         `json:"id"`
	SubtotalVND    int64          `json:"subtotal_vnd"`
	CommissionVND  int64          `json:"commission_vnd"`
	ShippingFeeVND int64          `json:"shipping_fee_vnd"`
	TotalPayable   int64          `json:"total_payable_vnd"`
	PaymentFeeVND  int64          `json:"payment_fee_vnd"`
	PaymentFeeBps  int32          `json:"payment_fee_bps"`
	Items          []OrderItemDTO `json:"items"`
}

type payW2bOrderWrap struct {
	Order payW2bOrderFeeDTO `json:"order"`
}

type payW2bCheckoutWrap struct {
	CheckoutGroupID string              `json:"checkout_group_id"`
	Orders          []payW2bOrderFeeDTO `json:"orders"`
	PaymentID       string              `json:"payment_id"`
	TotalPayableVND int64               `json:"total_payable_vnd"`
}

type payW2bShipOrderRequest struct {
	CarrierCode string `json:"carrier_code"`
	ServiceCode string `json:"service_code"`
}

type payW2bShipOrderResponse struct {
	TrackingCode string `json:"tracking_code"`
	LabelURL     string `json:"label_url"`
}

func payW2bDialGRPC(t *testing.T, env *testEnv) paymentv1.PaymentServiceClient {
	t.Helper()
	conn, err := grpc.NewClient(env.cfg.PaymentGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial PaymentService gRPC %s: %v", env.cfg.PaymentGRPCAddr, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return paymentv1.NewPaymentServiceClient(conn)
}

func payW2bAuthCtx(ctx context.Context, token string) context.Context {
	return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+token))
}

func payW2bServiceSecret() string {
	if s := strings.TrimSpace(os.Getenv("SERVICE_JWT_SECRET")); s != "" {
		return s
	}
	return "dev-only-nexusmart-service-jwt-secret-change-me"
}

func payW2bMintServiceToken(secret, svcName string) string {
	enc := base64.RawURLEncoding
	now := time.Now().Unix()
	claims, _ := json.Marshal(map[string]any{
		"sub":     svcName,
		"role":    "admin",
		"service": svcName,
		"iss":     "nexusmart",
		"jti":     uuid.NewString(),
		"iat":     now,
		"exp":     now + 300,
	})
	unsigned := enc.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." + enc.EncodeToString(claims)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(unsigned))
	return unsigned + "." + enc.EncodeToString(mac.Sum(nil))
}

func payW2bAssertLedgerBalanced(t *testing.T, env *testEnv) LedgerSummaryResponse {
	t.Helper()
	summary := env.getLedgerSummary(t)
	if !summary.IsBalanced || summary.TotalDebitsVND != summary.TotalCreditsVND {
		t.Fatalf("ledger unbalanced: is_balanced=%v debits=%d credits=%d",
			summary.IsBalanced, summary.TotalDebitsVND, summary.TotalCreditsVND)
	}
	return summary
}

func payW2bGetActiveFeeBps(
	ctx context.Context,
	t *testing.T,
	client paymentv1.PaymentServiceClient,
	token string,
	method paymentv1.PaymentMethod,
	atTime *timestamppb.Timestamp,
) int32 {
	t.Helper()
	resp, err := client.GetFeeConfig(payW2bAuthCtx(ctx, token), &paymentv1.GetFeeConfigRequest{
		PaymentMethod:  method,
		IncludeHistory: false,
		AtTime:         atTime,
	})
	if err != nil {
		t.Fatalf("GetFeeConfig(%v): %v", method, err)
	}
	for _, cfg := range resp.GetActiveConfigs() {
		if cfg.GetPaymentMethod() == method {
			return cfg.GetFeeBps()
		}
	}
	t.Fatalf("GetFeeConfig(%v) returned no active config: %+v", method, resp)
	return 0
}

func payW2bCollectAllSellerLedger(
	ctx context.Context,
	t *testing.T,
	client paymentv1.PaymentServiceClient,
	token string,
	shopID string,
	accountFilter string,
	refType string,
	pageSize int32,
) ([]*paymentv1.SellerLedgerEntry, int64) {
	t.Helper()
	var all []*paymentv1.SellerLedgerEntry
	var total int64
	for page := int32(1); page <= 50; page++ {
		resp, err := client.ListSellerLedger(payW2bAuthCtx(ctx, token), &paymentv1.ListSellerLedgerRequest{
			ShopId:        shopID,
			AccountFilter: accountFilter,
			ReferenceType: refType,
			Page:          page,
			PageSize:      pageSize,
		})
		if err != nil {
			t.Fatalf("ListSellerLedger(shop=%q acct=%q ref=%q page=%d): %v", shopID, accountFilter, refType, page, err)
		}
		total = resp.GetTotal()
		all = append(all, resp.GetEntries()...)
		if int64(len(all)) >= total || len(resp.GetEntries()) == 0 {
			break
		}
	}
	return all, total
}

func payW2bCompleteVietQROrder(
	t *testing.T,
	env *testEnv,
	buyer *userFixture,
	seller *sellerFixture,
	sku SKUDTO,
	quote CarrierQuoteDTO,
	idemPrefix string,
) (payW2bCheckoutWrap, payW2bOrderFeeDTO) {
	t.Helper()
	ctx := env.ctx(t)

	resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
		"Idempotency-Key": idemPrefix + "-" + uuid.NewString(),
	}, CheckoutRequest{
		SalesChannel:  "MARKETPLACE",
		AddressID:     buyer.AddressID,
		FromCart:      false,
		Items:         []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 1}},
		PaymentMethod: "VIETQR",
		ShippingChoices: []ShopShippingChoiceDTO{
			{ShopID: seller.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
		},
	}, http.StatusCreated, http.StatusOK)

	var co payW2bCheckoutWrap
	if err := DecodeJSON(resp, &co); err != nil {
		t.Fatalf("decode checkout response: %v (body=%s)", err, string(resp.Body))
	}
	if len(co.Orders) != 1 || co.PaymentID == "" {
		t.Fatalf("unexpected checkout response: %s", string(resp.Body))
	}
	ord := co.Orders[0]

	// Pay VietQR via signed webhook
	whResp, err := env.client.PostSignedWebhook(ctx, "/webhooks/vietqr", PaymentWebhookPayload{
		Provider:     "vietqr_mock",
		ProviderTxID: "PAYW2B-TX-" + uuid.NewString(),
		PaymentID:    co.PaymentID,
		OrderGroupID: co.CheckoutGroupID,
		AmountVND:    co.TotalPayableVND,
		Status:       "SUCCEEDED",
		Reference:    "PAYW2B " + co.CheckoutGroupID,
		OccurredAt:   time.Now().UTC().Format(time.RFC3339),
	}, env.cfg.PaymentWebhookSecret, time.Now().UTC(), "")
	if err != nil || whResp.StatusCode != http.StatusOK {
		t.Fatalf("VietQR webhook failed: err=%v status=%d body=%s", err, whResp.StatusCode, string(whResp.Body))
	}

	// Wait for order -> CONFIRMED
	env.pollUntil(t, 20*time.Second, 300*time.Millisecond, "order CONFIRMED after VietQR webhook", func() (bool, string) {
		got := env.getOrder(t, buyer.AccessToken, ord.ID)
		return StatusEquals(got.Status, "CONFIRMED"), got.Status
	})

	// Seller confirms & ships
	confirmPath := fmt.Sprintf("/api/v1/seller/orders/%s/confirm", url.PathEscape(ord.ID))
	env.mustDoJSON(t, http.MethodPost, confirmPath, seller.AccessToken, nil, nil, http.StatusOK)

	shipPath := fmt.Sprintf("/api/v1/seller/orders/%s/ship", url.PathEscape(ord.ID))
	shipResp := env.mustDoJSON(t, http.MethodPost, shipPath, seller.AccessToken, nil, payW2bShipOrderRequest{
		CarrierCode: quote.CarrierCode,
		ServiceCode: quote.ServiceCode,
	}, http.StatusOK)
	var shipOut payW2bShipOrderResponse
	if err := DecodeJSON(shipResp, &shipOut); err != nil {
		t.Fatalf("decode ship response: %v", err)
	}

	carrier := strings.ToLower(quote.CarrierCode)
	if carrier == "" {
		carrier = "ghn"
	}
	whPath := "/webhooks/shipping/" + url.PathEscape(carrier)
	for _, st := range []string{"delivering", "delivered"} {
		sResp, sErr := env.client.PostSignedWebhook(ctx, whPath, ShippingWebhookPayload{
			EventID:      "payw2b-shp-" + st + "-" + uuid.NewString(),
			TrackingCode: shipOut.TrackingCode,
			OrderID:      ord.ID,
			RawStatus:    st,
			Description:  "E2E 29 " + st,
			OccurredAt:   time.Now().UTC().Format(time.RFC3339),
		}, env.cfg.ShippingWebhookSecret, time.Now().UTC(), "")
		if sErr != nil || sResp.StatusCode != http.StatusOK {
			t.Fatalf("shipping webhook %s failed: err=%v status=%d", st, sErr, sResp.StatusCode)
		}
	}

	env.pollUntil(t, 20*time.Second, 300*time.Millisecond, "order DELIVERED", func() (bool, string) {
		got := env.getOrder(t, buyer.AccessToken, ord.ID)
		return StatusEquals(got.Status, "DELIVERED"), got.Status
	})

	// Buyer confirms received -> COMPLETED -> order.completed settles escrow to seller_pending
	recvPath := fmt.Sprintf("/api/v1/orders/%s/confirm-received", url.PathEscape(ord.ID))
	env.mustDoJSON(t, http.MethodPost, recvPath, buyer.AccessToken, nil, nil, http.StatusOK)

	return co, ord
}

// TestPaymentW2bFeeConfigLifecycleAndRBAC verifies GetFeeConfig/SetFeeConfig RBAC,
// validation bounds (0-2000 bps, effective_from >= now), immutable history, and at_time resolution.
func TestPaymentW2bFeeConfigLifecycleAndRBAC(t *testing.T) {
	env := getEnv()
	ctx := env.ctx(t)
	adminTok := env.adminLogin(t)
	_, seller1, _ := env.ensureBaseMarketplace(t)
	buyer := env.registerVerifyAndLogin(t, "payw2b-fee-buyer")
	svcTok := payW2bMintServiceToken(payW2bServiceSecret(), "order-service")
	grpcClient := payW2bDialGRPC(t, env)

	// Snapshot current VIETQR fee and restore on cleanup so later E2E tests stay unaffected.
	origVietQRBps := payW2bGetActiveFeeBps(ctx, t, grpcClient, adminTok, paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR, nil)
	t.Cleanup(func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, _ = grpcClient.SetFeeConfig(payW2bAuthCtx(cleanCtx, adminTok), &paymentv1.SetFeeConfigRequest{
			PaymentMethod: paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
			FeeBps:        origVietQRBps,
			Note:          "e2e 29 restore baseline",
		})
	})

	// 1. RBAC: unauthenticated -> Unauthenticated
	if _, err := grpcClient.GetFeeConfig(ctx, &paymentv1.GetFeeConfigRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("unauthenticated GetFeeConfig: want Unauthenticated, got %v", err)
	}
	if _, err := grpcClient.SetFeeConfig(ctx, &paymentv1.SetFeeConfigRequest{
		PaymentMethod: paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
		FeeBps:        100,
	}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("unauthenticated SetFeeConfig: want Unauthenticated, got %v", err)
	}

	// 2. RBAC (payment r4 / contracts §4.10):
	//    - buyer & seller: GetFeeConfig -> PermissionDenied for BOTH include_history=false & true (and future at_time),
	//      so non-admin/non-service callers never receive created_by/note or future schedules.
	//    - service token: GetFeeConfig(include_history=false) succeeds; SetFeeConfig -> PermissionDenied.
	for name, tok := range map[string]string{
		"buyer":  buyer.AccessToken,
		"seller": seller1.AccessToken,
	} {
		for _, inclHist := range []bool{false, true} {
			if _, err := grpcClient.GetFeeConfig(payW2bAuthCtx(ctx, tok), &paymentv1.GetFeeConfigRequest{
				IncludeHistory: inclHist,
			}); status.Code(err) != codes.PermissionDenied {
				t.Fatalf("GetFeeConfig(include_history=%v) as %s: want PermissionDenied, got %v", inclHist, name, err)
			}
		}
		if _, err := grpcClient.GetFeeConfig(payW2bAuthCtx(ctx, tok), &paymentv1.GetFeeConfigRequest{
			IncludeHistory: false,
			AtTime:         timestamppb.New(time.Now().UTC().Add(24 * time.Hour)),
		}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("GetFeeConfig(future at_time) as %s: want PermissionDenied, got %v", name, err)
		}
		if _, err := grpcClient.SetFeeConfig(payW2bAuthCtx(ctx, tok), &paymentv1.SetFeeConfigRequest{
			PaymentMethod: paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
			FeeBps:        100,
		}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("SetFeeConfig as %s: want PermissionDenied, got %v", name, err)
		}
	}
	svcFeeResp, err := grpcClient.GetFeeConfig(payW2bAuthCtx(ctx, svcTok), &paymentv1.GetFeeConfigRequest{
		IncludeHistory: false,
	})
	if err != nil {
		t.Fatalf("GetFeeConfig as service failed: %v", err)
	}
	if len(svcFeeResp.GetActiveConfigs()) < 2 {
		t.Fatalf("GetFeeConfig as service: want >=2 active configs (COD, VIETQR), got %d", len(svcFeeResp.GetActiveConfigs()))
	}
	// Non-allowlisted service name ("order" instead of "order-service") must be rejected by payment SERVICE_METHOD_ALLOWLIST.
	wrongSvcTok := payW2bMintServiceToken(payW2bServiceSecret(), "order")
	if _, err := grpcClient.GetFeeConfig(payW2bAuthCtx(ctx, wrongSvcTok), &paymentv1.GetFeeConfigRequest{
		IncludeHistory: false,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("GetFeeConfig as non-allowlisted service 'order': want PermissionDenied, got %v", err)
	}
	if len(svcFeeResp.GetHistory()) != 0 {
		t.Fatalf("GetFeeConfig(include_history=false) as service should return empty history, got %d", len(svcFeeResp.GetHistory()))
	}
	if _, err := grpcClient.SetFeeConfig(payW2bAuthCtx(ctx, svcTok), &paymentv1.SetFeeConfigRequest{
		PaymentMethod: paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
		FeeBps:        100,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("SetFeeConfig as service: want PermissionDenied, got %v", err)
	}

	// 3. Validation bounds: bps out of [0, 2000], past effective_from, UNSPECIFIED method
	for _, badBps := range []int32{-1, 2001} {
		if _, err := grpcClient.SetFeeConfig(payW2bAuthCtx(ctx, adminTok), &paymentv1.SetFeeConfigRequest{
			PaymentMethod: paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
			FeeBps:        badBps,
		}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("SetFeeConfig(bps=%d): want InvalidArgument, got %v", badBps, err)
		}
	}
	if _, err := grpcClient.SetFeeConfig(payW2bAuthCtx(ctx, adminTok), &paymentv1.SetFeeConfigRequest{
		PaymentMethod: paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
		FeeBps:        100,
		EffectiveFrom: timestamppb.New(time.Now().UTC().Add(-2 * time.Minute)),
		Note:          "past should fail",
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("SetFeeConfig(past effective_from): want InvalidArgument, got %v", err)
	}
	if _, err := grpcClient.SetFeeConfig(payW2bAuthCtx(ctx, adminTok), &paymentv1.SetFeeConfigRequest{
		PaymentMethod: paymentv1.PaymentMethod_PAYMENT_METHOD_UNSPECIFIED,
		FeeBps:        100,
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("SetFeeConfig(UNSPECIFIED): want InvalidArgument, got %v", err)
	}

	// 4. Immediate config (150 bps) + two far-future scheduled configs (200 bps & 275 bps in +10y)
	//    so boundary checks are immune to other concurrent tests and never activate unexpectedly in 2026.
	immResp, err := grpcClient.SetFeeConfig(payW2bAuthCtx(ctx, adminTok), &paymentv1.SetFeeConfigRequest{
		PaymentMethod: paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
		FeeBps:        150,
		Note:          "e2e29-immediate-150",
	})
	if err != nil {
		t.Fatalf("SetFeeConfig immediate 150: %v", err)
	}
	if immResp.GetConfig().GetFeeBps() != 150 || immResp.GetConfig().GetCreatedBy() == "" {
		t.Fatalf("unexpected immediate fee config response: %+v", immResp.GetConfig())
	}

	futureAt1 := time.Now().UTC().AddDate(10, 0, 0).Truncate(time.Second)
	futureAt2 := futureAt1.Add(10 * time.Second)
	fut1Resp, err := grpcClient.SetFeeConfig(payW2bAuthCtx(ctx, adminTok), &paymentv1.SetFeeConfigRequest{
		PaymentMethod: paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
		FeeBps:        200,
		EffectiveFrom: timestamppb.New(futureAt1),
		Note:          "e2e29-future-200",
	})
	if err != nil {
		t.Fatalf("SetFeeConfig future1 200: %v", err)
	}
	fut2Resp, err := grpcClient.SetFeeConfig(payW2bAuthCtx(ctx, adminTok), &paymentv1.SetFeeConfigRequest{
		PaymentMethod: paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
		FeeBps:        275,
		EffectiveFrom: timestamppb.New(futureAt2),
		Note:          "e2e29-future-275",
	})
	if err != nil {
		t.Fatalf("SetFeeConfig future2 275: %v", err)
	}
	if fut1Resp.GetConfig().GetFeeBps() != 200 || fut2Resp.GetConfig().GetFeeBps() != 275 {
		t.Fatalf("unexpected future fee configs: %+v / %+v", fut1Resp.GetConfig(), fut2Resp.GetConfig())
	}

	// Right now -> still 150 (scheduled future rates not active yet)
	if got := payW2bGetActiveFeeBps(ctx, t, grpcClient, svcTok, paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR, nil); got != 150 {
		t.Fatalf("active VIETQR bps now = %d, want 150", got)
	}
	// Exactly at futureAt1 -> switches to 200
	if got := payW2bGetActiveFeeBps(ctx, t, grpcClient, svcTok, paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR, timestamppb.New(futureAt1)); got != 200 {
		t.Fatalf("active VIETQR bps at futureAt1 = %d, want 200", got)
	}
	// 1 second before futureAt2 -> still 200
	if got := payW2bGetActiveFeeBps(ctx, t, grpcClient, svcTok, paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR, timestamppb.New(futureAt2.Add(-time.Second))); got != 200 {
		t.Fatalf("active VIETQR bps at futureAt2-1s = %d, want 200", got)
	}
	// Exactly at futureAt2 -> switches to 275
	if got := payW2bGetActiveFeeBps(ctx, t, grpcClient, svcTok, paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR, timestamppb.New(futureAt2)); got != 275 {
		t.Fatalf("active VIETQR bps at futureAt2 = %d, want 275", got)
	}

	// History as admin contains all new entries in effective_from DESC order (immutable append-only)
	histResp, err := grpcClient.GetFeeConfig(payW2bAuthCtx(ctx, adminTok), &paymentv1.GetFeeConfigRequest{
		PaymentMethod:  paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
		IncludeHistory: true,
	})
	if err != nil {
		t.Fatalf("GetFeeConfig(include_history=true) as admin: %v", err)
	}
	var sawImm, sawFut1, sawFut2 bool
	for _, h := range histResp.GetHistory() {
		if h.GetId() == immResp.GetConfig().GetId() {
			sawImm = true
		}
		if h.GetId() == fut1Resp.GetConfig().GetId() {
			sawFut1 = true
		}
		if h.GetId() == fut2Resp.GetConfig().GetId() {
			sawFut2 = true
		}
	}
	if !sawImm || !sawFut1 || !sawFut2 {
		t.Fatalf("immutable history missing expected records (sawImm=%v, sawFut1=%v, sawFut2=%v, total=%d)", sawImm, sawFut1, sawFut2, len(histResp.GetHistory()))
	}
}

// TestPaymentW2bSellerLedgerMatchesBalanceAndIsolation verifies ListSellerLedger:
// sum(delta_vnd) == GetSellerBalance for pending, available, and combined; Vietnamese descriptions;
// pagination; strict shop isolation; and RBAC (seller/admin/buyer/service).
func TestPaymentW2bSellerLedgerMatchesBalanceAndIsolation(t *testing.T) {
	env := getEnv()
	ctx := env.ctx(t)
	adminTok := env.adminLogin(t)
	cat := env.ensureCategory(t)

	// Fresh isolated shops so we verify exact balance == sum(ledger entries) from zero!
	sellerA := env.createApprovedSeller(t, "payw2b-ledger-a")
	sellerB := env.createApprovedSeller(t, "payw2b-ledger-b")
	buyer := env.registerVerifyAndLogin(t, "payw2b-ledger-buyer")
	env.addAddress(t, buyer)

	_, skuA := env.createApprovedProduct(t, sellerA, cat.ID, "Ledger Item A", 200_000, 10, 500)
	_, skuB := env.createApprovedProduct(t, sellerB, cat.ID, "Ledger Item B", 150_000, 10, 500)
	quoteA := env.calculateShippingQuote(t, buyer.AccessToken, sellerA.ShopID, 500, 0)
	quoteB := env.calculateShippingQuote(t, buyer.AccessToken, sellerB.ShopID, 500, 0)

	grpcClient := payW2bDialGRPC(t, env)
	svcTok := payW2bMintServiceToken(payW2bServiceSecret(), "order-service")

	// Complete 1 order for shopA and 1 order for shopB
	coA, ordA := payW2bCompleteVietQROrder(t, env, buyer, sellerA, skuA, quoteA, "payw2b-led-a")
	_, _ = payW2bCompleteVietQROrder(t, env, buyer, sellerB, skuB, quoteB, "payw2b-led-b")

	// Wait until both shops settle into seller_pending
	env.pollUntil(t, 25*time.Second, 400*time.Millisecond, "shopA pending balance settled", func() (bool, string) {
		bal, err := grpcClient.GetSellerBalance(payW2bAuthCtx(ctx, sellerA.AccessToken), &paymentv1.GetSellerBalanceRequest{
			ShopId: sellerA.ShopID,
		})
		if err != nil {
			return false, err.Error()
		}
		return bal.GetPendingVnd() > 0, fmt.Sprintf("pending=%d", bal.GetPendingVnd())
	})
	env.pollUntil(t, 25*time.Second, 400*time.Millisecond, "shopB pending balance settled", func() (bool, string) {
		bal, err := grpcClient.GetSellerBalance(payW2bAuthCtx(ctx, sellerB.AccessToken), &paymentv1.GetSellerBalanceRequest{
			ShopId: sellerB.ShopID,
		})
		if err != nil {
			return false, err.Error()
		}
		return bal.GetPendingVnd() > 0, fmt.Sprintf("pending=%d", bal.GetPendingVnd())
	})

	// Release escrow for shopA -> posts escrow_release (debit pending, credit available)
	relResp, err := grpcClient.ReleaseEscrow(payW2bAuthCtx(ctx, adminTok), &paymentv1.ReleaseEscrowRequest{
		OrderId: ordA.ID,
		ShopId:  sellerA.ShopID,
	})
	if err != nil || !relResp.GetReleased() || relResp.GetReleasedAmountVnd() <= 0 {
		t.Fatalf("ReleaseEscrow shopA: err=%v resp=%+v", err, relResp)
	}

	// Post-settlement partial refund of 20,000 VND on shopA -> posts refund clawback (debit seller_available)
	_, err = grpcClient.CreateRefund(payW2bAuthCtx(ctx, adminTok), &paymentv1.CreateRefundRequest{
		PaymentId:      coA.PaymentID,
		OrderId:        ordA.ID,
		AmountVnd:      20_000,
		Reason:         "e2e29 post-settlement clawback",
		IdempotencyKey: "payw2b-clawback-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("CreateRefund post-settlement shopA: %v", err)
	}

	// Read authoritative balance for shopA
	balA, err := grpcClient.GetSellerBalance(payW2bAuthCtx(ctx, sellerA.AccessToken), &paymentv1.GetSellerBalanceRequest{
		ShopId: sellerA.ShopID,
	})
	if err != nil {
		t.Fatalf("GetSellerBalance shopA: %v", err)
	}

	// Collect all ledger entries for shopA using small page_size=2 to exercise pagination
	allA, totalAllA := payW2bCollectAllSellerLedger(ctx, t, grpcClient, sellerA.AccessToken, "", "", "", 2)
	pendingA, _ := payW2bCollectAllSellerLedger(ctx, t, grpcClient, sellerA.AccessToken, "", "pending", "", 2)
	availA, _ := payW2bCollectAllSellerLedger(ctx, t, grpcClient, sellerA.AccessToken, "", "available", "", 2)

	if totalAllA < 4 || int64(len(allA)) != totalAllA {
		t.Fatalf("expected >=4 paginated ledger entries for shopA (settle + 2 release + refund), got len=%d total=%d", len(allA), totalAllA)
	}

	var sumAll, sumPending, sumAvail int64
	var sawSettleVN, sawReleaseVN, sawRefundVN bool
	pendingAcctA := "seller_pending:" + sellerA.ShopID
	availAcctA := "seller_available:" + sellerA.ShopID

	for _, e := range allA {
		if e.GetAccountCode() != pendingAcctA && e.GetAccountCode() != availAcctA {
			t.Fatalf("shopA ledger leaked foreign account_code=%q", e.GetAccountCode())
		}
		if e.GetDeltaVnd() != e.GetCreditVnd()-e.GetDebitVnd() {
			t.Fatalf("delta_vnd mismatch: got %d want %d", e.GetDeltaVnd(), e.GetCreditVnd()-e.GetDebitVnd())
		}
		sumAll += e.GetDeltaVnd()
		switch e.GetReferenceType() {
		case "escrow_settle":
			if strings.Contains(e.GetDescription(), "Tiền đơn hàng") {
				sawSettleVN = true
			}
		case "escrow_release":
			if strings.Contains(e.GetDescription(), "Giải ngân") {
				sawReleaseVN = true
			}
		case "refund":
			if strings.Contains(e.GetDescription(), "Hoàn tiền") {
				sawRefundVN = true
			}
		}
	}
	for _, e := range pendingA {
		if e.GetAccountCode() != pendingAcctA {
			t.Fatalf("pending filter returned non-pending account %q", e.GetAccountCode())
		}
		sumPending += e.GetDeltaVnd()
	}
	for _, e := range availA {
		if e.GetAccountCode() != availAcctA {
			t.Fatalf("available filter returned non-available account %q", e.GetAccountCode())
		}
		sumAvail += e.GetDeltaVnd()
	}

	if sumPending != balA.GetPendingVnd() {
		t.Fatalf("pending ledger sum (%d) != GetSellerBalance.pending_vnd (%d)", sumPending, balA.GetPendingVnd())
	}
	if sumAvail != balA.GetAvailableVnd() {
		t.Fatalf("available ledger sum (%d) != GetSellerBalance.available_vnd (%d)", sumAvail, balA.GetAvailableVnd())
	}
	if sumAll != balA.GetPendingVnd()+balA.GetAvailableVnd() {
		t.Fatalf("all ledger sum (%d) != pending+available (%d)", sumAll, balA.GetPendingVnd()+balA.GetAvailableVnd())
	}
	if !sawSettleVN || !sawReleaseVN || !sawRefundVN {
		t.Fatalf("missing Vietnamese descriptions (settle=%v release=%v refund=%v)", sawSettleVN, sawReleaseVN, sawRefundVN)
	}

	// Reference type filter
	refOnly, _ := payW2bCollectAllSellerLedger(ctx, t, grpcClient, sellerA.AccessToken, "", "", "refund", 10)
	if len(refOnly) != 1 || refOnly[0].GetReferenceType() != "refund" || refOnly[0].GetDeltaVnd() != -20_000 {
		t.Fatalf("expected exactly 1 refund entry (-20000), got %+v", refOnly)
	}

	// RBAC & isolation: sellerA cannot query shopB; buyer & service token denied
	if _, err := grpcClient.ListSellerLedger(payW2bAuthCtx(ctx, sellerA.AccessToken), &paymentv1.ListSellerLedgerRequest{
		ShopId: sellerB.ShopID,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("sellerA querying shopB ledger: want PermissionDenied, got %v", err)
	}
	if _, err := grpcClient.ListSellerLedger(payW2bAuthCtx(ctx, buyer.AccessToken), &paymentv1.ListSellerLedgerRequest{
		ShopId: sellerA.ShopID,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("buyer querying seller ledger: want PermissionDenied, got %v", err)
	}
	if _, err := grpcClient.ListSellerLedger(payW2bAuthCtx(ctx, svcTok), &paymentv1.ListSellerLedgerRequest{
		ShopId: sellerA.ShopID,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service token querying seller ledger: want PermissionDenied, got %v", err)
	}

	// Admin can query shopB specifically AND all shops (shop_id = "")
	adminB, _ := payW2bCollectAllSellerLedger(ctx, t, grpcClient, adminTok, sellerB.ShopID, "", "", 10)
	if len(adminB) == 0 {
		t.Fatalf("admin querying shopB returned 0 entries")
	}
	for _, e := range adminB {
		if !strings.HasSuffix(e.GetAccountCode(), ":"+sellerB.ShopID) {
			t.Fatalf("admin shopB query leaked account %q", e.GetAccountCode())
		}
	}
	adminAll, adminAllTotal := payW2bCollectAllSellerLedger(ctx, t, grpcClient, adminTok, "", "", "", 50)
	if adminAllTotal < totalAllA+int64(len(adminB)) || len(adminAll) == 0 {
		t.Fatalf("admin all-shop ledger total=%d smaller than shopA(%d)+shopB(%d)", adminAllTotal, totalAllA, len(adminB))
	}

	payW2bAssertLedgerBalanced(t, env)
}

// TestPaymentW2bFeeChangeOrderSnapshot verifies cross-service flow ("đổi phí → đơn mới khác đơn cũ"):
// changing VIETQR fee_bps changes payment_fee_bps/payment_fee_vnd on new orders without mutating old orders.
func TestPaymentW2bFeeChangeOrderSnapshot(t *testing.T) {
	env := getEnv()
	ctx := env.ctx(t)
	adminTok := env.adminLogin(t)
	cat, seller1, _ := env.ensureBaseMarketplace(t)
	buyer := env.registerVerifyAndLogin(t, "payw2b-snap-buyer")
	env.addAddress(t, buyer)

	_, sku := env.createApprovedProduct(t, seller1, cat.ID, "Fee Snapshot Item", 200_000, 10, 500)
	quote := env.calculateShippingQuote(t, buyer.AccessToken, seller1.ShopID, 500, 0)
	grpcClient := payW2bDialGRPC(t, env)

	origVietQRBps := payW2bGetActiveFeeBps(ctx, t, grpcClient, adminTok, paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR, nil)
	t.Cleanup(func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, _ = grpcClient.SetFeeConfig(payW2bAuthCtx(cleanCtx, adminTok), &paymentv1.SetFeeConfigRequest{
			PaymentMethod: paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
			FeeBps:        origVietQRBps,
			Note:          "e2e 29 restore after snapshot test",
		})
	})

	// Step 1: Set VIETQR fee to 100 bps (1%) and place Order 1
	if _, err := grpcClient.SetFeeConfig(payW2bAuthCtx(ctx, adminTok), &paymentv1.SetFeeConfigRequest{
		PaymentMethod: paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
		FeeBps:        100,
		Note:          "e2e29 order 1 rate 100bps",
	}); err != nil {
		t.Fatalf("SetFeeConfig 100bps: %v", err)
	}
	co1, ord1 := payW2bCompleteVietQROrder(t, env, buyer, seller1, sku, quote, "payw2b-snap-1")

	// Step 2: Change VIETQR fee to 300 bps (3%) and place Order 2
	if _, err := grpcClient.SetFeeConfig(payW2bAuthCtx(ctx, adminTok), &paymentv1.SetFeeConfigRequest{
		PaymentMethod: paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
		FeeBps:        300,
		Note:          "e2e29 order 2 rate 300bps",
	}); err != nil {
		t.Fatalf("SetFeeConfig 300bps: %v", err)
	}
	co2, ord2 := payW2bCompleteVietQROrder(t, env, buyer, seller1, sku, quote, "payw2b-snap-2")

	// Wave 2b buyer fee privacy: buyer checkout masks fees to 0; fetch orders as owning seller for fee assertions.
	if ord1.CommissionVND != 0 || ord1.PaymentFeeVND != 0 || ord1.PaymentFeeBps != 0 {
		t.Fatalf("buyer checkout must mask fees to 0, got comm=%d pay_fee=%d pay_fee_bps=%d",
			ord1.CommissionVND, ord1.PaymentFeeVND, ord1.PaymentFeeBps)
	}
	for _, it := range ord1.Items {
		if it.CommissionBps != 0 {
			t.Fatalf("buyer checkout must mask item commission_bps to 0, got %d", it.CommissionBps)
		}
	}

	// Fetch both orders as owning seller and payments as admin to confirm snapshot immutability and fee delta
	r1 := env.mustDoJSON(t, http.MethodGet, "/api/v1/orders/"+url.PathEscape(ord1.ID), seller1.AccessToken, nil, nil, http.StatusOK)
	r2 := env.mustDoJSON(t, http.MethodGet, "/api/v1/orders/"+url.PathEscape(ord2.ID), seller1.AccessToken, nil, nil, http.StatusOK)
	var o1Wrap, o2Wrap payW2bOrderWrap
	if err := DecodeJSON(r1, &o1Wrap); err != nil {
		t.Fatalf("decode order 1: %v", err)
	}
	if err := DecodeJSON(r2, &o2Wrap); err != nil {
		t.Fatalf("decode order 2: %v", err)
	}

	if o1Wrap.Order.PaymentFeeBps != 100 || o2Wrap.Order.PaymentFeeBps != 300 {
		t.Fatalf("expected order1 payment_fee_bps=100 and order2=300; got order1=%d (fee=%d) order2=%d (fee=%d) — check if order/gateway w2b services are rebuilt",
			o1Wrap.Order.PaymentFeeBps, o1Wrap.Order.PaymentFeeVND, o2Wrap.Order.PaymentFeeBps, o2Wrap.Order.PaymentFeeVND)
	}
	if o1Wrap.Order.PaymentFeeVND <= 0 || o2Wrap.Order.PaymentFeeVND <= o1Wrap.Order.PaymentFeeVND {
		t.Fatalf("expected order2 payment_fee_vnd > order1 payment_fee_vnd > 0; got order1=%d order2=%d",
			o1Wrap.Order.PaymentFeeVND, o2Wrap.Order.PaymentFeeVND)
	}

	// Verify payment shop_allocations recorded C_total = C_cat + C_pay for both payments
	p1, err := grpcClient.GetPayment(payW2bAuthCtx(ctx, adminTok), &paymentv1.GetPaymentRequest{PaymentId: co1.PaymentID})
	if err != nil {
		t.Fatalf("GetPayment p1: %v", err)
	}
	p2, err := grpcClient.GetPayment(payW2bAuthCtx(ctx, adminTok), &paymentv1.GetPaymentRequest{PaymentId: co2.PaymentID})
	if err != nil {
		t.Fatalf("GetPayment p2: %v", err)
	}
	c1 := p1.GetPayment().GetShopAllocations()[0].GetCommissionVnd()
	c2 := p2.GetPayment().GetShopAllocations()[0].GetCommissionVnd()
	if c2 <= c1 {
		t.Fatalf("expected payment2 commission_vnd (C_cat+C_pay2=%d) > payment1 (%d)", c2, c1)
	}

	payW2bAssertLedgerBalanced(t, env)
}

// TestPaymentW2bGetPaymentRolePrivacyGRPCAndREST verifies payment r4 role-scoped GetPayment visibility
// across both direct gRPC and Gateway REST on a 2-shop VietQR payment:
//   - buyer sees all allocations with commission_vnd = 0 and retains qr_url/qr_code
//   - allocation-owning seller (not buyer) sees ONLY their own shop allocation (with commission_vnd) and empty qr_url/qr_code/provider_ref
//   - unrelated seller gets PermissionDenied / 403
//   - admin and service token see all allocations with full commission_vnd and qr/provider_ref
func TestPaymentW2bGetPaymentRolePrivacyGRPCAndREST(t *testing.T) {
	env := getEnv()
	ctx := env.ctx(t)
	adminTok := env.adminLogin(t)
	cat, seller1, seller2 := env.ensureBaseMarketplace(t)
	seller3 := env.createApprovedSeller(t, "payw2b-priv-s3")
	buyer := env.registerVerifyAndLogin(t, "payw2b-priv-buyer")
	env.addAddress(t, buyer)

	_, sku1 := env.createApprovedProduct(t, seller1, cat.ID, "PayPriv Item 1", 200_000, 10, 400)
	_, sku2 := env.createApprovedProduct(t, seller2, cat.ID, "PayPriv Item 2", 300_000, 10, 400)
	q1 := env.calculateShippingQuote(t, buyer.AccessToken, seller1.ShopID, 400, 0)
	q2 := env.calculateShippingQuote(t, buyer.AccessToken, seller2.ShopID, 400, 0)

	coResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
		"Idempotency-Key": "payw2b-priv-" + uuid.NewString(),
	}, CheckoutRequest{
		SalesChannel: "MARKETPLACE",
		AddressID:    buyer.AddressID,
		Items: []CheckoutItemInputDTO{
			{SKUID: sku1.ID, Quantity: 1},
			{SKUID: sku2.ID, Quantity: 1},
		},
		PaymentMethod: "VIETQR",
		ShippingChoices: []ShopShippingChoiceDTO{
			{ShopID: seller1.ShopID, CarrierCode: q1.CarrierCode, ServiceCode: q1.ServiceCode},
			{ShopID: seller2.ShopID, CarrierCode: q2.CarrierCode, ServiceCode: q2.ServiceCode},
		},
	}, http.StatusCreated, http.StatusOK)
	var co CheckoutResponse
	if err := DecodeJSON(coResp, &co); err != nil || co.PaymentID == "" {
		t.Fatalf("decode 2-shop VietQR checkout: err=%v body=%s", err, string(coResp.Body))
	}

	grpcClient := payW2bDialGRPC(t, env)
	svcTok := payW2bMintServiceToken(payW2bServiceSecret(), "order-service")

	// 1. Direct gRPC GetPayment visibility checks
	adminGRPC, err := grpcClient.GetPayment(payW2bAuthCtx(ctx, adminTok), &paymentv1.GetPaymentRequest{PaymentId: co.PaymentID})
	if err != nil {
		t.Fatalf("admin gRPC GetPayment: %v", err)
	}
	if len(adminGRPC.GetPayment().GetShopAllocations()) != 2 {
		t.Fatalf("admin gRPC expected 2 allocations, got %d", len(adminGRPC.GetPayment().GetShopAllocations()))
	}
	adminCommByShop := map[string]int64{}
	for _, a := range adminGRPC.GetPayment().GetShopAllocations() {
		if a.GetCommissionVnd() <= 0 {
			t.Fatalf("admin gRPC expected positive commission for shop %s, got %+v", a.GetShopId(), a)
		}
		adminCommByShop[a.GetShopId()] = a.GetCommissionVnd()
	}

	svcGRPC, err := grpcClient.GetPayment(payW2bAuthCtx(ctx, svcTok), &paymentv1.GetPaymentRequest{PaymentId: co.PaymentID})
	if err != nil || len(svcGRPC.GetPayment().GetShopAllocations()) != 2 {
		t.Fatalf("service token gRPC GetPayment mismatch: err=%v allocs=%d", err, len(svcGRPC.GetPayment().GetShopAllocations()))
	}

	buyerGRPC, err := grpcClient.GetPayment(payW2bAuthCtx(ctx, buyer.AccessToken), &paymentv1.GetPaymentRequest{PaymentId: co.PaymentID})
	if err != nil {
		t.Fatalf("buyer gRPC GetPayment: %v", err)
	}
	if len(buyerGRPC.GetPayment().GetShopAllocations()) != 2 {
		t.Fatalf("buyer gRPC expected 2 allocations, got %d", len(buyerGRPC.GetPayment().GetShopAllocations()))
	}
	for _, a := range buyerGRPC.GetPayment().GetShopAllocations() {
		if a.GetCommissionVnd() != 0 {
			t.Fatalf("buyer gRPC GetPayment leaked commission_vnd=%d on shop %s", a.GetCommissionVnd(), a.GetShopId())
		}
	}

	seller1GRPC, err := grpcClient.GetPayment(payW2bAuthCtx(ctx, seller1.AccessToken), &paymentv1.GetPaymentRequest{PaymentId: co.PaymentID})
	if err != nil {
		t.Fatalf("seller1 gRPC GetPayment: %v", err)
	}
	sp := seller1GRPC.GetPayment()
	if len(sp.GetShopAllocations()) != 1 || sp.GetShopAllocations()[0].GetShopId() != seller1.ShopID ||
		sp.GetShopAllocations()[0].GetCommissionVnd() != adminCommByShop[seller1.ShopID] {
		t.Fatalf("seller1 gRPC GetPayment must see ONLY own shop allocation with real commission: %+v", sp.GetShopAllocations())
	}
	if sp.GetQrUrl() != "" || sp.GetQrCode() != "" || sp.GetProviderRef() != "" {
		t.Fatalf("seller1 gRPC GetPayment leaked buyer payment secrets: qr_url=%q qr_code=%q provider_ref=%q",
			sp.GetQrUrl(), sp.GetQrCode(), sp.GetProviderRef())
	}

	if _, err := grpcClient.GetPayment(payW2bAuthCtx(ctx, seller3.AccessToken), &paymentv1.GetPaymentRequest{PaymentId: co.PaymentID}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("unrelated seller3 gRPC GetPayment: want PermissionDenied, got %v", err)
	}

	// 2. Gateway REST GET /api/v1/payments/{id} visibility checks
	type restPaymentWrap struct {
		Payment struct {
			PaymentID       string `json:"payment_id"`
			QRURL           string `json:"qr_url"`
			QRCode          string `json:"qr_code"`
			ProviderRef     string `json:"provider_ref"`
			ShopAllocations []struct {
				ShopID        string `json:"shop_id"`
				CommissionVND int64  `json:"commission_vnd"`
			} `json:"shop_allocations"`
		} `json:"payment"`
	}
	payPath := "/api/v1/payments/" + url.PathEscape(co.PaymentID)

	buyerRESTResp := env.mustDoJSON(t, http.MethodGet, payPath, buyer.AccessToken, nil, nil, http.StatusOK)
	var buyerREST restPaymentWrap
	if err := DecodeJSON(buyerRESTResp, &buyerREST); err != nil {
		t.Fatalf("decode buyer REST GetPayment: %v", err)
	}
	if len(buyerREST.Payment.ShopAllocations) != 2 {
		t.Fatalf("buyer REST expected 2 allocations, got %+v", buyerREST.Payment.ShopAllocations)
	}
	for _, a := range buyerREST.Payment.ShopAllocations {
		if a.CommissionVND != 0 {
			t.Fatalf("buyer REST GET /payments/{id} leaked commission_vnd=%d", a.CommissionVND)
		}
	}

	seller1RESTResp := env.mustDoJSON(t, http.MethodGet, payPath, seller1.AccessToken, nil, nil, http.StatusOK)
	var seller1REST restPaymentWrap
	if err := DecodeJSON(seller1RESTResp, &seller1REST); err != nil {
		t.Fatalf("decode seller1 REST GetPayment: %v", err)
	}
	if len(seller1REST.Payment.ShopAllocations) != 1 ||
		seller1REST.Payment.ShopAllocations[0].ShopID != seller1.ShopID ||
		seller1REST.Payment.ShopAllocations[0].CommissionVND != adminCommByShop[seller1.ShopID] {
		t.Fatalf("seller1 REST GET /payments/{id} allocation mismatch: %+v", seller1REST.Payment.ShopAllocations)
	}
	if seller1REST.Payment.QRURL != "" || seller1REST.Payment.QRCode != "" || seller1REST.Payment.ProviderRef != "" {
		t.Fatalf("seller1 REST GET /payments/{id} leaked qr_url=%q qr_code=%q provider_ref=%q",
			seller1REST.Payment.QRURL, seller1REST.Payment.QRCode, seller1REST.Payment.ProviderRef)
	}

	env.mustDoJSON(t, http.MethodGet, payPath, seller3.AccessToken, nil, nil, http.StatusForbidden)

	// 3. Payment r5: service-token CreatePayment validation & idempotent replay rules, plus tight SERVICE_METHODS allowlist.
	pAdmin := adminGRPC.GetPayment()
	if _, err := grpcClient.CreatePayment(payW2bAuthCtx(ctx, svcTok), &paymentv1.CreatePaymentRequest{
		OrderGroupId:      co.CheckoutGroupID,
		UserId:            buyer.UserID,
		AmountVnd:         pAdmin.GetAmountVnd(),
		Method:            pAdmin.GetMethod(),
		IdempotencyKey:    "checkout:" + co.CheckoutGroupID,
		SupersedePrevious: true,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service token CreatePayment with supersede_previous=true: want PermissionDenied, got %v", err)
	}
	if _, err := grpcClient.CreatePayment(payW2bAuthCtx(ctx, svcTok), &paymentv1.CreatePaymentRequest{
		OrderGroupId:   co.CheckoutGroupID,
		UserId:         buyer.UserID,
		AmountVnd:      pAdmin.GetAmountVnd(),
		Method:         pAdmin.GetMethod(),
		IdempotencyKey: "wrong-prefix:" + co.CheckoutGroupID,
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("service token CreatePayment with mismatched idempotency_key: want InvalidArgument, got %v", err)
	}
	svcReplay, err := grpcClient.CreatePayment(payW2bAuthCtx(ctx, svcTok), &paymentv1.CreatePaymentRequest{
		OrderGroupId:    co.CheckoutGroupID,
		UserId:          buyer.UserID,
		AmountVnd:       pAdmin.GetAmountVnd(),
		Method:          pAdmin.GetMethod(),
		IdempotencyKey:  "checkout:" + co.CheckoutGroupID,
		ShopAllocations: pAdmin.GetShopAllocations(),
	})
	if err != nil {
		t.Fatalf("service token idempotent CreatePayment replay failed: %v", err)
	}
	if svcReplay.GetPayment().GetPaymentId() != co.PaymentID {
		t.Fatalf("service token idempotent CreatePayment replay returned different payment_id: got=%s want=%s",
			svcReplay.GetPayment().GetPaymentId(), co.PaymentID)
	}
	if _, err := grpcClient.ReleaseEscrow(payW2bAuthCtx(ctx, svcTok), &paymentv1.ReleaseEscrowRequest{
		OrderId: co.Orders[0].ID,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service token ReleaseEscrow must remain denied: want PermissionDenied, got %v", err)
	}
}
