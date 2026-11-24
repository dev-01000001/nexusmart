package e2e

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
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

type payW2RefundDTO struct {
	RefundID          string `json:"refund_id"`
	PaymentID         string `json:"payment_id"`
	OrderID           string `json:"order_id"`
	AmountVND         int64  `json:"amount_vnd"`
	Status            string `json:"status"`
	Reason            string `json:"reason"`
	IdempotencyKey    string `json:"idempotency_key"`
	BankCode          string `json:"bank_code"`
	BankAccountMasked string `json:"bank_account_masked"`
	BankAccountName   string `json:"bank_account_name"`
	BankAccountNumber string `json:"bank_account_number"`
	ProviderRefundID  string `json:"provider_refund_id"`
	IsLatePayment     bool   `json:"is_late_payment"`
	SettledAt         string `json:"settled_at"`
	SettlementNote    string `json:"settlement_note"`
}

type payW2RefundListResp struct {
	Items    []payW2RefundDTO `json:"items"`
	Page     int32            `json:"page"`
	PageSize int32            `json:"page_size"`
	Total    int64            `json:"total"`
}

type payW2RefundWrap struct {
	Refund         payW2RefundDTO `json:"refund"`
	AlreadyExisted bool           `json:"already_existed"`
	AlreadySettled bool           `json:"already_settled"`
}

type payW2QueueItemDTO struct {
	ID                string `json:"id"`
	WebhookEventID    string `json:"webhook_event_id"`
	Provider          string `json:"provider"`
	ProviderTxID      string `json:"provider_tx_id"`
	ExpectedPaymentID string `json:"expected_payment_id"`
	OrderRef          string `json:"order_ref"`
	ExpectedAmountVND int64  `json:"expected_amount_vnd"`
	ActualAmountVND   int64  `json:"actual_amount_vnd"`
	Reason            string `json:"reason"`
	ResolutionStatus  string `json:"resolution_status"`
	Notes             string `json:"notes"`
	CreatedAt         string `json:"created_at"`
	ResolvedAt        string `json:"resolved_at"`
}

type payW2QueueListResp struct {
	Items    []payW2QueueItemDTO `json:"items"`
	Page     int32               `json:"page"`
	PageSize int32               `json:"page_size"`
	Total    int64               `json:"total"`
}

type payW2ResolveQueueResp struct {
	Item            payW2QueueItemDTO `json:"item"`
	Refund          *payW2RefundDTO   `json:"refund"`
	AlreadyResolved bool              `json:"already_resolved"`
}

type payW2ReconcileItemDTO struct {
	PaymentID         string `json:"payment_id"`
	ProviderTxID      string `json:"provider_tx_id"`
	LocalAmountVND    int64  `json:"local_amount_vnd"`
	ProviderAmountVND int64  `json:"provider_amount_vnd"`
	Status            string `json:"status"`
	Note              string `json:"note"`
}

type payW2ReconcileResp struct {
	RunID                string                  `json:"run_id"`
	MatchedCount         int32                   `json:"matched_count"`
	MissingLocalCount    int32                   `json:"missing_local_count"`
	MissingProviderCount int32                   `json:"missing_provider_count"`
	AmountMismatchCount  int32                   `json:"amount_mismatch_count"`
	Items                []payW2ReconcileItemDTO `json:"items"`
}

func payW2DialGRPC(t *testing.T, env *testEnv) paymentv1.PaymentServiceClient {
	t.Helper()
	conn, err := grpc.NewClient(env.cfg.PaymentGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial PaymentService gRPC %s: %v", env.cfg.PaymentGRPCAddr, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return paymentv1.NewPaymentServiceClient(conn)
}

func payW2AuthCtx(ctx context.Context, token string) context.Context {
	return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+token))
}

func payW2MintServiceToken(secret, svcName string) string {
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

func payW2FindAccount(summary LedgerSummaryResponse, code string) (AccountBalanceDTO, bool) {
	for _, acc := range summary.Accounts {
		if acc.AccountCode == code {
			return acc, true
		}
	}
	return AccountBalanceDTO{}, false
}

func payW2AssertLedgerBalanced(t *testing.T, env *testEnv) LedgerSummaryResponse {
	t.Helper()
	summary := env.getLedgerSummary(t)
	if !summary.IsBalanced || summary.TotalDebitsVND != summary.TotalCreditsVND {
		t.Fatalf("ledger unbalanced: is_balanced=%v debits=%d credits=%d",
			summary.IsBalanced, summary.TotalDebitsVND, summary.TotalCreditsVND)
	}
	if rp, ok := payW2FindAccount(summary, "refund_payable"); ok && rp.BalanceVND < 0 {
		t.Fatalf("invariant violation: refund_payable is negative (%d)", rp.BalanceVND)
	}
	return summary
}

func payW2GetSellerBalance(t *testing.T, env *testEnv, seller *sellerFixture) SellerBalanceResponse {
	t.Helper()
	resp := env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/balance", seller.AccessToken, nil, nil, http.StatusOK)
	var out SellerBalanceResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode seller balance for %s: %v", seller.ShopID, err)
	}
	return out
}

func payW2CreateVietQRCheckout(
	t *testing.T,
	env *testEnv,
	buyer *userFixture,
	seller *sellerFixture,
	sku SKUDTO,
	quote CarrierQuoteDTO,
	idemPrefix string,
) CheckoutResponse {
	t.Helper()
	resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
		"Idempotency-Key": idemPrefix + "-" + uuid.NewString(),
	}, CheckoutRequest{
		AddressID:     buyer.AddressID,
		FromCart:      false,
		Items:         []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 1}},
		PaymentMethod: "VIETQR",
		ShippingChoices: []ShopShippingChoiceDTO{
			{ShopID: seller.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
		},
	}, http.StatusCreated, http.StatusOK)
	var out CheckoutResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode VietQR checkout: %v", err)
	}
	if len(out.Orders) != 1 || out.PaymentID == "" {
		t.Fatalf("unexpected VietQR checkout response: %s", string(resp.Body))
	}
	return out
}

func payW2SendVietQRWebhook(
	t *testing.T,
	env *testEnv,
	paymentID, orderGroupID, providerTxID string,
	amountVND int64,
) PaymentWebhookResult {
	t.Helper()
	ctx := env.ctx(t)
	resp, err := env.client.PostSignedWebhook(ctx, "/webhooks/vietqr", PaymentWebhookPayload{
		Provider:     "vietqr_mock",
		ProviderTxID: providerTxID,
		PaymentID:    paymentID,
		OrderGroupID: orderGroupID,
		AmountVND:    amountVND,
		Status:       "SUCCEEDED",
		Reference:    "PAYW2 " + orderGroupID,
		OccurredAt:   time.Now().UTC().Format(time.RFC3339),
	}, env.cfg.PaymentWebhookSecret, time.Now().UTC(), "")
	if err != nil {
		t.Fatalf("post VietQR webhook %s: %v", providerTxID, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("post VietQR webhook %s HTTP %d: %s", providerTxID, resp.StatusCode, string(resp.Body))
	}
	var out PaymentWebhookResult
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode VietQR webhook result: %v", err)
	}
	return out
}

func payW2ShipAndDeliver(
	t *testing.T,
	env *testEnv,
	seller *sellerFixture,
	buyer *userFixture,
	orderID string,
	quote CarrierQuoteDTO,
) {
	t.Helper()
	ctx := env.ctx(t)

	confirmPath := fmt.Sprintf("/api/v1/seller/orders/%s/confirm", url.PathEscape(orderID))
	env.mustDoJSON(t, http.MethodPost, confirmPath, seller.AccessToken, nil, nil, http.StatusOK)

	carrier := strings.ToLower(quote.CarrierCode)
	if carrier == "" {
		carrier = "ghn"
	}
	svc := quote.ServiceCode
	if svc == "" {
		svc = "standard"
	}

	shipPath := fmt.Sprintf("/api/v1/seller/orders/%s/ship", url.PathEscape(orderID))
	shipResp := env.mustDoJSON(t, http.MethodPost, shipPath, seller.AccessToken, nil, SellerShipRequest{
		CarrierCode: carrier,
		ServiceCode: svc,
	}, http.StatusOK)
	var shipOut SellerShipResponse
	if err := DecodeJSON(shipResp, &shipOut); err != nil {
		t.Fatalf("decode seller ship %s: %v", orderID, err)
	}

	whPath := fmt.Sprintf("/webhooks/shipping/%s", url.PathEscape(carrier))
	now := time.Now().UTC()
	for _, step := range []struct {
		rawStatus string
		desc      string
		at        time.Time
	}{
		{"delivering", "Out for delivery", now.Add(-2 * time.Second)},
		{"delivered", "Delivered to buyer", now},
	} {
		whResp, err := env.client.PostSignedWebhook(ctx, whPath, ShippingWebhookPayload{
			EventID:      "payw2-ship-" + uuid.NewString(),
			TrackingCode: shipOut.TrackingCode,
			OrderID:      orderID,
			RawStatus:    step.rawStatus,
			Description:  step.desc,
			OccurredAt:   step.at.Format(time.RFC3339),
		}, env.cfg.ShippingWebhookSecret, time.Now().UTC(), "")
		if err != nil {
			t.Fatalf("shipping webhook %s (%s): %v", orderID, step.rawStatus, err)
		}
		if whResp.StatusCode != http.StatusOK {
			t.Fatalf("shipping webhook %s (%s) HTTP %d: %s", orderID, step.rawStatus, whResp.StatusCode, string(whResp.Body))
		}
	}

	env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "order "+orderID+" DELIVERED", func() (bool, string) {
		o := env.getOrder(t, buyer.AccessToken, orderID)
		return StatusEquals(o.Status, "DELIVERED"), fmt.Sprintf("status=%s", o.Status)
	})
}

func payW2FindQueueItemByTx(t *testing.T, env *testEnv, adminTok, providerTxID string) payW2QueueItemDTO {
	t.Helper()
	resp := env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/reconciliation/queue?page=1&page_size=100", adminTok, nil, nil, http.StatusOK)
	var list payW2QueueListResp
	if err := DecodeJSON(resp, &list); err != nil {
		t.Fatalf("decode reconciliation queue list: %v", err)
	}
	for _, it := range list.Items {
		if it.ProviderTxID == providerTxID {
			return it
		}
	}
	t.Fatalf("reconciliation queue item for provider_tx_id=%s not found (total=%d)", providerTxID, list.Total)
	return payW2QueueItemDTO{}
}

func payW2CleanupResolveQueueItem(t *testing.T, env *testEnv, adminTok, itemID, action, paymentID, note string) {
	t.Helper()
	t.Cleanup(func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		body := map[string]string{
			"action": action,
			"note":   note,
		}
		if paymentID != "" {
			body["payment_id"] = paymentID
		}
		path := fmt.Sprintf("/api/v1/admin/reconciliation/queue/%s/resolve", url.PathEscape(itemID))
		resp, err := env.client.DoJSON(cleanCtx, http.MethodPost, path, adminTok, map[string]string{
			"Idempotency-Key": "payw2-cleanup-" + uuid.NewString(),
		}, body)
		if err != nil {
			t.Errorf("cleanup resolve reconciliation_queue %s (%s) failed: %v", itemID, action, err)
			return
		}
		if resp.StatusCode != http.StatusOK {
			t.Errorf("cleanup resolve reconciliation_queue %s (%s) returned HTTP %d: %s", itemID, action, resp.StatusCode, string(resp.Body))
		}
	})
}

// TestE2E_PaymentOps_TwoStepRefund_BankAccount_And_IdempotentSettle verifies PAY-B11 / UC-PAY04:
// CreateRefund (pending) -> another buyer bank submit rejected (403) -> owner buyer submits masked bank account
// -> admin SettleRefund (2x calls = 1 ledger posting, refund_payable returns to 0 delta) -> post-settle edit blocked (409).
func TestE2E_PaymentOps_TwoStepRefund_BankAccount_And_IdempotentSettle(t *testing.T) {
	env := getEnv()
	adminTok := env.adminLogin(t)

	cat := env.ensureCategory(t)
	seller := env.createApprovedSeller(t, "payw2-rf-seller")
	_, sku := env.createApprovedProduct(t, seller, cat.ID, "W2a TwoStep Refund Shirt", 220000, 20, 500)

	buyer := env.registerVerifyAndLogin(t, "payw2-rf-buyer")
	env.addAddress(t, buyer)
	otherBuyer := env.registerVerifyAndLogin(t, "payw2-rf-other")

	quote := env.calculateShippingQuote(t, buyer.AccessToken, seller.ShopID, 500, 0)
	co := payW2CreateVietQRCheckout(t, env, buyer, seller, sku, quote, "payw2-rf")
	ord := co.Orders[0]

	payW2SendVietQRWebhook(t, env, co.PaymentID, co.CheckoutGroupID, "PAYW2-TX-RF-"+uuid.NewString(), co.TotalPayableVND)
	env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "order CONFIRMED", func() (bool, string) {
		o := env.getOrder(t, buyer.AccessToken, ord.ID)
		return StatusEquals(o.Status, "CONFIRMED"), o.Status
	})

	rpBefore, _ := payW2FindAccount(payW2AssertLedgerBalanced(t, env), "refund_payable")
	refundAmt := int64(50000)

	// 1. Admin creates refund -> MUST be PENDING (two-step refund)
	createPath := fmt.Sprintf("/api/v1/admin/payments/%s/refund", url.PathEscape(co.PaymentID))
	rfResp := env.mustDoJSON(t, http.MethodPost, createPath, adminTok, map[string]string{
		"Idempotency-Key": "payw2-rf-create-" + uuid.NewString(),
	}, RefundRequest{
		OrderID:   ord.ID,
		AmountVND: refundAmt,
		Reason:    "W2a two-step refund test",
	}, http.StatusOK)

	var created payW2RefundWrap
	if err := DecodeJSON(rfResp, &created); err != nil {
		t.Fatalf("decode create refund: %v", err)
	}
	if !StatusEquals(created.Refund.Status, "PENDING") {
		t.Fatalf("PAY-B11: newly created refund must start PENDING, got %q", created.Refund.Status)
	}
	refundID := created.Refund.RefundID

	rpAfterCreate, _ := payW2FindAccount(payW2AssertLedgerBalanced(t, env), "refund_payable")
	if rpAfterCreate.BalanceVND-rpBefore.BalanceVND != refundAmt {
		t.Fatalf("expected refund_payable delta=%d after CreateRefund, got %d",
			refundAmt, rpAfterCreate.BalanceVND-rpBefore.BalanceVND)
	}

	// 2. Other buyer submitting bank account MUST be rejected with 403/404
	bankPath := fmt.Sprintf("/api/v1/refunds/%s/bank-account", url.PathEscape(refundID))
	env.mustDoJSON(t, http.MethodPost, bankPath, otherBuyer.AccessToken, nil, map[string]string{
		"bank_code":           "VCB",
		"bank_account_number": "1234567890",
		"bank_account_name":   "ATTACKER",
	}, http.StatusForbidden, http.StatusNotFound)

	// Invalid bank account (<6 digits) -> 400
	env.mustDoJSON(t, http.MethodPost, bankPath, buyer.AccessToken, nil, map[string]string{
		"bank_code":           "VCB",
		"bank_account_number": "12345",
		"bank_account_name":   "BUYER OWNER",
	}, http.StatusBadRequest)

	// 3. Owner buyer submits receiving bank account -> 200, masked in response
	bankResp := env.mustDoJSON(t, http.MethodPost, bankPath, buyer.AccessToken, nil, map[string]string{
		"bank_code":           "VCB",
		"bank_account_number": "19034567890123",
		"bank_account_name":   "NGUYEN VAN BUYER",
	}, http.StatusOK)
	var afterBank payW2RefundWrap
	if err := DecodeJSON(bankResp, &afterBank); err != nil {
		t.Fatalf("decode submit bank account: %v", err)
	}
	if afterBank.Refund.BankAccountMasked != "****0123" {
		t.Fatalf("expected masked bank account ****0123, got %q", afterBank.Refund.BankAccountMasked)
	}
	if afterBank.Refund.BankAccountNumber != "" {
		t.Fatalf("buyer response must NOT expose full bank_account_number")
	}

	// Buyer can update bank account while still PENDING
	bankResp2 := env.mustDoJSON(t, http.MethodPost, bankPath, buyer.AccessToken, nil, map[string]string{
		"bank_code":           "MB",
		"bank_account_number": "09876543219999",
		"bank_account_name":   "NGUYEN VAN BUYER",
	}, http.StatusOK)
	var afterBank2 payW2RefundWrap
	if err := DecodeJSON(bankResp2, &afterBank2); err != nil {
		t.Fatalf("decode updated bank account: %v", err)
	}
	if afterBank2.Refund.BankCode != "MB" || afterBank2.Refund.BankAccountMasked != "****9999" || afterBank2.Refund.BankAccountNumber != "" {
		t.Fatalf("unexpected updated buyer bank response: code=%q masked=%q hasFull=%v",
			afterBank2.Refund.BankCode, afterBank2.Refund.BankAccountMasked, afterBank2.Refund.BankAccountNumber != "")
	}

	// Buyer list refunds sees masked only; admin list refunds sees full account number
	buyerListPath := fmt.Sprintf("/api/v1/orders/%s/refunds", url.PathEscape(ord.ID))
	buyerListResp := env.mustDoJSON(t, http.MethodGet, buyerListPath, buyer.AccessToken, nil, nil, http.StatusOK)
	var buyerList payW2RefundListResp
	if err := DecodeJSON(buyerListResp, &buyerList); err != nil {
		t.Fatalf("decode buyer list refunds: %v", err)
	}
	if len(buyerList.Items) == 0 || buyerList.Items[0].BankAccountNumber != "" || buyerList.Items[0].BankAccountMasked != "****9999" {
		t.Fatalf("buyer list refunds leaked full number or missing masked account: total=%d", buyerList.Total)
	}

	adminListPath := fmt.Sprintf("/api/v1/admin/refunds?order_id=%s", url.QueryEscape(ord.ID))
	adminListResp := env.mustDoJSON(t, http.MethodGet, adminListPath, adminTok, nil, nil, http.StatusOK)
	var adminList payW2RefundListResp
	if err := DecodeJSON(adminListResp, &adminList); err != nil {
		t.Fatalf("decode admin list refunds: %v", err)
	}
	if len(adminList.Items) == 0 || adminList.Items[0].BankAccountNumber != "09876543219999" {
		t.Fatalf("admin list refunds must include full destination account number for transfer")
	}

	// 4. Admin SettleRefund (call twice -> only 1 ledger posting; refund_payable delta returns to 0)
	settlePath := fmt.Sprintf("/api/v1/admin/refunds/%s/settle", url.PathEscape(refundID))
	settleKey := "payw2-settle-" + uuid.NewString()
	settleResp1 := env.mustDoJSON(t, http.MethodPost, settlePath, adminTok, map[string]string{
		"Idempotency-Key": settleKey,
	}, map[string]string{
		"provider_refund_id": "FT-W2A-" + uuid.NewString()[:8],
		"note":               "Transferred via bank",
	}, http.StatusOK)
	var settled1 payW2RefundWrap
	if err := DecodeJSON(settleResp1, &settled1); err != nil {
		t.Fatalf("decode settle refund #1: %v", err)
	}
	if !StatusEquals(settled1.Refund.Status, "DONE") || settled1.AlreadySettled || settled1.Refund.SettledAt == "" {
		t.Fatalf("unexpected settle #1 result: status=%q already=%v settled_at=%q",
			settled1.Refund.Status, settled1.AlreadySettled, settled1.Refund.SettledAt)
	}

	// Replay with same Idempotency-Key -> already_settled=true, zero extra ledger entries
	settleResp2 := env.mustDoJSON(t, http.MethodPost, settlePath, adminTok, map[string]string{
		"Idempotency-Key": settleKey,
	}, map[string]string{
		"provider_refund_id": settled1.Refund.ProviderRefundID,
		"note":               "Replay",
	}, http.StatusOK)
	var settled2 payW2RefundWrap
	if err := DecodeJSON(settleResp2, &settled2); err != nil {
		t.Fatalf("decode settle refund #2: %v", err)
	}
	if !settled2.AlreadySettled {
		t.Fatalf("expected already_settled=true on 2nd SettleRefund call")
	}

	// P2-5: 2nd call with a different Idempotency-Key / provider_refund_id on already-settled refund -> 409 Conflict
	env.mustDoJSON(t, http.MethodPost, settlePath, adminTok, map[string]string{
		"Idempotency-Key": "payw2-settle-dup-" + uuid.NewString(),
	}, map[string]string{
		"provider_refund_id": "FT-W2A-OTHER",
		"note":               "Second attempt",
	}, http.StatusConflict)

	// refund_payable must return to rpBefore (0 net effect for this refund)
	rpAfterSettle, _ := payW2FindAccount(payW2AssertLedgerBalanced(t, env), "refund_payable")
	if rpAfterSettle.BalanceVND != rpBefore.BalanceVND {
		t.Fatalf("PAY-B11: expected refund_payable to return to %d after settlement, got %d",
			rpBefore.BalanceVND, rpAfterSettle.BalanceVND)
	}

	// Buyer can NO LONGER edit bank account after settlement -> 409
	env.mustDoJSON(t, http.MethodPost, bankPath, buyer.AccessToken, nil, map[string]string{
		"bank_code":           "TCB",
		"bank_account_number": "111122223333",
		"bank_account_name":   "LATE EDIT",
	}, http.StatusConflict)
}

// TestE2E_PaymentOps_ReconciliationQueue_And_VnReconcile verifies PAY-B9 / UC-A14 & PAY-B8:
// - Mismatched underpayment -> queue -> ASSIGN returns 409 -> SUSPEND -> REFUND creates pending refund
// - Mismatched overpayment -> queue -> ASSIGN confirms payment + creates pending excess refund
// - Cancelled payment -> ASSIGN returns 409
// - Reconciliation accepts VN date (durable mock from webhook_events) and rejects future date (400)
func TestE2E_PaymentOps_ReconciliationQueue_And_VnReconcile(t *testing.T) {
	env := getEnv()
	adminTok := env.adminLogin(t)

	cat := env.ensureCategory(t)
	seller := env.createApprovedSeller(t, "payw2-rq-seller")
	_, sku := env.createApprovedProduct(t, seller, cat.ID, "W2a Recon Queue Shirt", 200000, 30, 500)

	buyer := env.registerVerifyAndLogin(t, "payw2-rq-buyer")
	env.addAddress(t, buyer)
	quote := env.calculateShippingQuote(t, buyer.AccessToken, seller.ShopID, 500, 0)

	// --- Case 1: Underpayment -> ASSIGN 409 -> SUSPEND -> REFUND ---
	coUnder := payW2CreateVietQRCheckout(t, env, buyer, seller, sku, quote, "payw2-under")
	underAmt := coUnder.TotalPayableVND - 15000
	txUnder := "PAYW2-UNDER-" + uuid.NewString()
	whUnder := payW2SendVietQRWebhook(t, env, coUnder.PaymentID, coUnder.CheckoutGroupID, txUnder, underAmt)
	if !strings.Contains(whUnder.Status, "queued_for_reconciliation") {
		t.Fatalf("expected underpayment webhook to queue for reconciliation, got %q", whUnder.Status)
	}

	qUnder := payW2FindQueueItemByTx(t, env, adminTok, txUnder)
	resolveUnderPath := fmt.Sprintf("/api/v1/admin/reconciliation/queue/%s/resolve", url.PathEscape(qUnder.ID))

	// ASSIGN underpayment -> 409 Conflict
	env.mustDoJSON(t, http.MethodPost, resolveUnderPath, adminTok, map[string]string{
		"Idempotency-Key": "payw2-assign-under-" + uuid.NewString(),
	}, map[string]string{
		"action":     "ASSIGN",
		"payment_id": coUnder.PaymentID,
		"note":       "should fail underpayment",
	}, http.StatusConflict)

	// SUSPEND with note -> 200
	suspResp := env.mustDoJSON(t, http.MethodPost, resolveUnderPath, adminTok, map[string]string{
		"Idempotency-Key": "payw2-suspend-" + uuid.NewString(),
	}, map[string]string{
		"action": "SUSPEND",
		"note":   "Waiting for buyer contact",
	}, http.StatusOK)
	var suspOut payW2ResolveQueueResp
	if err := DecodeJSON(suspResp, &suspOut); err != nil {
		t.Fatalf("decode suspend response: %v", err)
	}
	if suspOut.Item.ResolutionStatus != "suspended" {
		t.Fatalf("expected resolution_status=suspended, got %q", suspOut.Item.ResolutionStatus)
	}

	// Suspended item CAN be resolved again -> REFUND
	rpBeforeUnder, _ := payW2FindAccount(payW2AssertLedgerBalanced(t, env), "refund_payable")
	refundKeyUnder := "payw2-rq-refund-" + uuid.NewString()
	rfUnderResp := env.mustDoJSON(t, http.MethodPost, resolveUnderPath, adminTok, map[string]string{
		"Idempotency-Key": refundKeyUnder,
	}, map[string]string{
		"action":     "REFUND",
		"payment_id": coUnder.PaymentID,
		"note":       "Refund underpaid transfer",
	}, http.StatusOK)
	var rfUnderOut payW2ResolveQueueResp
	if err := DecodeJSON(rfUnderResp, &rfUnderOut); err != nil {
		t.Fatalf("decode queue refund response: %v", err)
	}
	if rfUnderOut.Item.ResolutionStatus != "refunded" || rfUnderOut.Refund == nil || rfUnderOut.Refund.AmountVND != underAmt || !StatusEquals(rfUnderOut.Refund.Status, "PENDING") {
		t.Fatalf("unexpected queue REFUND result: status=%q refund=%+v", rfUnderOut.Item.ResolutionStatus, rfUnderOut.Refund)
	}

	rpAfterUnder, _ := payW2FindAccount(payW2AssertLedgerBalanced(t, env), "refund_payable")
	if rpAfterUnder.BalanceVND-rpBeforeUnder.BalanceVND != underAmt {
		t.Fatalf("expected refund_payable increase by %d, got %d", underAmt, rpAfterUnder.BalanceVND-rpBeforeUnder.BalanceVND)
	}

	// Replay with same Idempotency-Key -> already_resolved=true
	rfUnderReplay := env.mustDoJSON(t, http.MethodPost, resolveUnderPath, adminTok, map[string]string{
		"Idempotency-Key": refundKeyUnder,
	}, map[string]string{
		"action":     "REFUND",
		"payment_id": coUnder.PaymentID,
		"note":       "Replay",
	}, http.StatusOK)
	var rfUnderReplayOut payW2ResolveQueueResp
	if err := DecodeJSON(rfUnderReplay, &rfUnderReplayOut); err != nil {
		t.Fatalf("decode queue refund replay: %v", err)
	}
	if !rfUnderReplayOut.AlreadyResolved || rfUnderReplayOut.Refund == nil || rfUnderReplayOut.Refund.RefundID != rfUnderOut.Refund.RefundID {
		t.Fatalf("expected idempotent replay returning same refund")
	}

	// Finalized item rejects new resolution key -> 409
	env.mustDoJSON(t, http.MethodPost, resolveUnderPath, adminTok, map[string]string{
		"Idempotency-Key": "payw2-rq-again-" + uuid.NewString(),
	}, map[string]string{
		"action": "SUSPEND",
		"note":   "Cannot re-suspend finalized item",
	}, http.StatusConflict)

	// --- Case 2: Overpayment -> ASSIGN confirms payment + creates pending excess refund ---
	coOver := payW2CreateVietQRCheckout(t, env, buyer, seller, sku, quote, "payw2-over")
	excessAmt := int64(25000)
	txOver := "PAYW2-OVER-" + uuid.NewString()
	whOver := payW2SendVietQRWebhook(t, env, coOver.PaymentID, coOver.CheckoutGroupID, txOver, coOver.TotalPayableVND+excessAmt)
	if !strings.Contains(whOver.Status, "queued_for_reconciliation") {
		t.Fatalf("expected overpayment webhook to queue for reconciliation, got %q", whOver.Status)
	}

	qOver := payW2FindQueueItemByTx(t, env, adminTok, txOver)
	resolveOverPath := fmt.Sprintf("/api/v1/admin/reconciliation/queue/%s/resolve", url.PathEscape(qOver.ID))

	rpBeforeOver, _ := payW2FindAccount(payW2AssertLedgerBalanced(t, env), "refund_payable")
	escBeforeOver, _ := payW2FindAccount(payW2AssertLedgerBalanced(t, env), "escrow_liability")

	assignOverKey := "payw2-assign-over-" + uuid.NewString()
	assignResp := env.mustDoJSON(t, http.MethodPost, resolveOverPath, adminTok, map[string]string{
		"Idempotency-Key": assignOverKey,
	}, map[string]string{
		"action":     "ASSIGN",
		"payment_id": coOver.PaymentID,
		"note":       "Confirm and refund excess",
	}, http.StatusOK)
	var assignOut payW2ResolveQueueResp
	if err := DecodeJSON(assignResp, &assignOut); err != nil {
		t.Fatalf("decode assign overpay response: %v", err)
	}
	if assignOut.Item.ResolutionStatus != "assigned" || assignOut.Refund == nil || assignOut.Refund.AmountVND != excessAmt || !StatusEquals(assignOut.Refund.Status, "PENDING") {
		t.Fatalf("unexpected ASSIGN overpay result: status=%q refund=%+v", assignOut.Item.ResolutionStatus, assignOut.Refund)
	}
	if assignOut.Refund.IdempotencyKey != "recon-overpay:"+txOver {
		t.Fatalf("P1-2: overpay excess refund key must be scoped by provider_tx_id (recon-overpay:%s), got %q",
			txOver, assignOut.Refund.IdempotencyKey)
	}

	// Payment must now be SUCCEEDED, escrow += expected, refund_payable += excess
	payOver := env.getPayment(t, buyer.AccessToken, coOver.PaymentID)
	if !StatusEquals(payOver.Status, "SUCCEEDED") {
		t.Fatalf("expected overpaid ASSIGN payment SUCCEEDED, got %q", payOver.Status)
	}
	env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "overpaid order CONFIRMED", func() (bool, string) {
		o := env.getOrder(t, buyer.AccessToken, coOver.Orders[0].ID)
		return StatusEquals(o.Status, "CONFIRMED"), o.Status
	})

	sumAfterOver := payW2AssertLedgerBalanced(t, env)
	rpAfterOver, _ := payW2FindAccount(sumAfterOver, "refund_payable")
	escAfterOver, _ := payW2FindAccount(sumAfterOver, "escrow_liability")
	if rpAfterOver.BalanceVND-rpBeforeOver.BalanceVND != excessAmt {
		t.Fatalf("expected refund_payable delta=%d for excess, got %d", excessAmt, rpAfterOver.BalanceVND-rpBeforeOver.BalanceVND)
	}
	if escAfterOver.BalanceVND-escBeforeOver.BalanceVND != coOver.TotalPayableVND {
		t.Fatalf("expected escrow_liability delta=%d, got %d", coOver.TotalPayableVND, escAfterOver.BalanceVND-escBeforeOver.BalanceVND)
	}

	// --- Case 3: ASSIGN to cancelled order group -> 409 ---
	coCancel := payW2CreateVietQRCheckout(t, env, buyer, seller, sku, quote, "payw2-cancel-assign")
	txCancel := "PAYW2-CAN-" + uuid.NewString()
	payW2SendVietQRWebhook(t, env, coCancel.PaymentID, coCancel.CheckoutGroupID, txCancel, coCancel.TotalPayableVND+5000)
	qCancel := payW2FindQueueItemByTx(t, env, adminTok, txCancel)
	payW2CleanupResolveQueueItem(t, env, adminTok, qCancel.ID, "REFUND", coCancel.PaymentID, "e2e cleanup cancelled group transfer")

	cancelPath := fmt.Sprintf("/api/v1/orders/%s/cancel", url.PathEscape(coCancel.Orders[0].ID))
	env.mustDoJSON(t, http.MethodPost, cancelPath, buyer.AccessToken, nil, map[string]string{"reason": "cancel before assign"}, http.StatusOK)
	env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "cancelled payment CANCELLED", func() (bool, string) {
		p := env.getPayment(t, buyer.AccessToken, coCancel.PaymentID)
		return StatusEquals(p.Status, "CANCELLED"), p.Status
	})

	resolveCancelPath := fmt.Sprintf("/api/v1/admin/reconciliation/queue/%s/resolve", url.PathEscape(qCancel.ID))
	env.mustDoJSON(t, http.MethodPost, resolveCancelPath, adminTok, map[string]string{
		"Idempotency-Key": "payw2-assign-cancelled-" + uuid.NewString(),
	}, map[string]string{
		"action":     "ASSIGN",
		"payment_id": coCancel.PaymentID,
		"note":       "cannot assign cancelled group",
	}, http.StatusConflict)

	// --- Case 4: Reconciliation date validation & VN timezone durable mock (PAY-B8) ---
	env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/payments/reconcile", adminTok, nil, map[string]string{
		"date": "2099-12-31",
	}, http.StatusBadRequest)

	vnLoc := time.FixedZone("Asia/Ho_Chi_Minh", 7*3600)
	vnTodayStr := time.Now().In(vnLoc).Format("2006-01-02")
	recResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/payments/reconcile", adminTok, nil, map[string]string{
		"date": vnTodayStr,
	}, http.StatusOK)
	var recOut payW2ReconcileResp
	if err := DecodeJSON(recResp, &recOut); err != nil {
		t.Fatalf("decode reconcile response: %v", err)
	}
	if recOut.RunID == "" {
		t.Fatalf("expected non-empty run_id from reconciliation")
	}

	payW2AssertLedgerBalanced(t, env)
}

// TestE2E_PaymentOps_SellerHoldPeriod_And_AdminReleaseEscrow verifies PAY-B7 / ORD-B9:
// - confirm-received settles into pending_vnd; available_vnd stays unchanged (held for PAYMENT_SELLER_HOLD_HOURS)
// - Service token is rejected by ReleaseEscrow (PermissionDenied)
// - Admin ReleaseEscrow moves pending_vnd -> available_vnd idempotently
// - Ledger stays strictly balanced
func TestE2E_PaymentOps_SellerHoldPeriod_And_AdminReleaseEscrow(t *testing.T) {
	env := getEnv()
	adminTok := env.adminLogin(t)
	grpcClient := payW2DialGRPC(t, env)

	cat := env.ensureCategory(t)
	seller := env.createApprovedSeller(t, "payw2-hold-seller")
	_, sku := env.createApprovedProduct(t, seller, cat.ID, "W2a Seller Hold Shirt", 240000, 20, 500)

	buyer := env.registerVerifyAndLogin(t, "payw2-hold-buyer")
	env.addAddress(t, buyer)
	quote := env.calculateShippingQuote(t, buyer.AccessToken, seller.ShopID, 500, 0)

	balBefore := payW2GetSellerBalance(t, env, seller)

	co := payW2CreateVietQRCheckout(t, env, buyer, seller, sku, quote, "payw2-hold")
	ord := co.Orders[0]
	// Wave 2b buyer fee privacy: buyer checkout masks fees to 0; read seller order for authoritative commission.
	if ord.CommissionVND != 0 || ord.PaymentFeeVND != 0 || ord.PaymentFeeBps != 0 {
		t.Fatalf("buyer checkout must mask fees to 0, got comm=%d pay_fee=%d pay_fee_bps=%d",
			ord.CommissionVND, ord.PaymentFeeVND, ord.PaymentFeeBps)
	}
	for _, it := range ord.Items {
		if it.CommissionBps != 0 {
			t.Fatalf("buyer checkout must mask item commission_bps to 0, got %d", it.CommissionBps)
		}
	}
	sellerOrd := env.getOrder(t, seller.AccessToken, ord.ID)
	// Wave 2b (docs/contracts.md §3/§6): seller net = R - (C_cat + C_pay).
	wantSellerNet := sellerOrd.SubtotalVND - sellerOrd.CommissionVND - sellerOrd.PaymentFeeVND

	payW2SendVietQRWebhook(t, env, co.PaymentID, co.CheckoutGroupID, "PAYW2-HOLD-"+uuid.NewString(), co.TotalPayableVND)
	env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "order CONFIRMED", func() (bool, string) {
		o := env.getOrder(t, buyer.AccessToken, ord.ID)
		return StatusEquals(o.Status, "CONFIRMED"), o.Status
	})

	payW2ShipAndDeliver(t, env, seller, buyer, ord.ID, quote)

	// Buyer confirms received -> order COMPLETED -> payment settles into pending_vnd (NOT available_vnd!)
	confirmRecPath := fmt.Sprintf("/api/v1/orders/%s/confirm-received", url.PathEscape(ord.ID))
	env.mustDoJSON(t, http.MethodPost, confirmRecPath, buyer.AccessToken, nil, nil, http.StatusOK)

	env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "seller pending_vnd credited after confirm-received", func() (bool, string) {
		b := payW2GetSellerBalance(t, env, seller)
		return b.PendingVND == balBefore.PendingVND+wantSellerNet,
			fmt.Sprintf("pending=%d (want %d) available=%d (before %d)",
				b.PendingVND, balBefore.PendingVND+wantSellerNet, b.AvailableVND, balBefore.AvailableVND)
	})

	balSettled := payW2GetSellerBalance(t, env, seller)
	if balSettled.AvailableVND != balBefore.AvailableVND {
		t.Fatalf("PAY-B7: available_vnd must NOT increase immediately on confirm-received (before=%d after=%d)",
			balBefore.AvailableVND, balSettled.AvailableVND)
	}
	if balSettled.TotalEarnedVND != balBefore.TotalEarnedVND+wantSellerNet {
		t.Fatalf("expected total_earned_vnd=%d, got %d", balBefore.TotalEarnedVND+wantSellerNet, balSettled.TotalEarnedVND)
	}

	// Service token MUST be rejected on ReleaseEscrow (removed from SERVICE_METHODS)
	svcSecret := envOr("SERVICE_JWT_SECRET", "dev-only-nexusmart-service-hs256-secret-change-me")
	if v := strings.TrimSpace(os.Getenv("SERVICE_JWT_SECRET")); v != "" {
		svcSecret = v
	}
	svcTok := payW2MintServiceToken(svcSecret, "order-service")
	_, svcErr := grpcClient.ReleaseEscrow(payW2AuthCtx(env.ctx(t), svcTok), &paymentv1.ReleaseEscrowRequest{
		OrderId: ord.ID,
		ShopId:  seller.ShopID,
	})
	if status.Code(svcErr) != codes.PermissionDenied {
		t.Fatalf("PAY-B7: service token must receive PermissionDenied on ReleaseEscrow, got %v", svcErr)
	}

	// Admin calls ReleaseEscrow -> moves pending_vnd -> available_vnd
	relResp, err := grpcClient.ReleaseEscrow(payW2AuthCtx(env.ctx(t), adminTok), &paymentv1.ReleaseEscrowRequest{
		OrderId: ord.ID,
		ShopId:  seller.ShopID,
	})
	if err != nil {
		t.Fatalf("admin ReleaseEscrow failed: %v", err)
	}
	if !relResp.GetReleased() || relResp.GetAlreadyReleased() || relResp.GetReleasedAmountVnd() != wantSellerNet {
		t.Fatalf("unexpected ReleaseEscrow response: released=%v already=%v amount=%d want=%d",
			relResp.GetReleased(), relResp.GetAlreadyReleased(), relResp.GetReleasedAmountVnd(), wantSellerNet)
	}

	balReleased := payW2GetSellerBalance(t, env, seller)
	if balReleased.PendingVND != balBefore.PendingVND {
		t.Fatalf("expected pending_vnd=%d after ReleaseEscrow, got %d", balBefore.PendingVND, balReleased.PendingVND)
	}
	if balReleased.AvailableVND != balBefore.AvailableVND+wantSellerNet {
		t.Fatalf("expected available_vnd=%d after ReleaseEscrow, got %d", balBefore.AvailableVND+wantSellerNet, balReleased.AvailableVND)
	}

	// 2nd admin ReleaseEscrow is idempotent
	relResp2, err := grpcClient.ReleaseEscrow(payW2AuthCtx(env.ctx(t), adminTok), &paymentv1.ReleaseEscrowRequest{
		OrderId: ord.ID,
		ShopId:  seller.ShopID,
	})
	if err != nil {
		t.Fatalf("2nd admin ReleaseEscrow failed: %v", err)
	}
	if !relResp2.GetAlreadyReleased() || relResp2.GetReleasedAmountVnd() != 0 {
		t.Fatalf("expected already_released=true & 0 amount on 2nd ReleaseEscrow, got %+v", relResp2)
	}

	payW2AssertLedgerBalanced(t, env)
}

// fillCanonicalEventPayload returns a shallow copy of payload populated with any
// missing required fields from docs/contracts.md §5 so downstream consumers
// (especially notification) process synthetic E2E NATS events cleanly without DLQ noise.
func fillCanonicalEventPayload(eventType string, payload map[string]any) map[string]any {
	out := make(map[string]any, len(payload)+10)
	for k, v := range payload {
		out[k] = v
	}
	strField := func(k string) string {
		if v, ok := out[k]; ok {
			if s, ok := v.(string); ok {
				return strings.TrimSpace(s)
			}
		}
		return ""
	}
	setDefault := func(k string, v any) {
		if existing, ok := out[k]; ok {
			if s, isStr := existing.(string); !isStr || strings.TrimSpace(s) != "" {
				return
			}
		}
		out[k] = v
	}

	orderID := strField("order_id")
	if orderID == "" {
		orderID = "e2e-ord-" + uuid.NewString()
	}

	switch eventType {
	case "order.cancelled":
		setDefault("order_id", orderID)
		setDefault("order_code", orderID)
		setDefault("buyer_id", "e2e-synthetic-buyer")
		setDefault("shop_id", "e2e-synthetic-shop")
		setDefault("reason", "e2e_cancelled")
		setDefault("was_paid", false)
	case "order.delivered":
		setDefault("order_id", orderID)
		setDefault("order_code", orderID)
		setDefault("buyer_id", "e2e-synthetic-buyer")
		setDefault("shop_id", "e2e-synthetic-shop")
		setDefault("tracking_code", "E2E-TRK-"+orderID)
	case "order.returned":
		setDefault("order_id", orderID)
		setDefault("order_code", orderID)
		setDefault("checkout_group_id", orderID)
		setDefault("buyer_id", "e2e-synthetic-buyer")
		setDefault("shop_id", "e2e-synthetic-shop")
		setDefault("payment_method", "VIETQR")
		setDefault("from_status", "SHIPPING")
		setDefault("total_payable_vnd", int64(0))
		setDefault("reason", "e2e_returned")
		setDefault("was_paid", false)
	case "order.completed":
		setDefault("order_id", orderID)
		setDefault("order_code", orderID)
		setDefault("buyer_id", "e2e-synthetic-buyer")
		setDefault("shop_id", "e2e-synthetic-shop")
		setDefault("subtotal_vnd", int64(0))
		setDefault("commission_vnd", int64(0))
		setDefault("total_payable_vnd", int64(0))
		setDefault("coins_reward", int64(0))
	case "shipment.updated":
		setDefault("shipment_id", "e2e-shp-"+orderID)
		setDefault("order_id", orderID)
		setDefault("shop_id", "e2e-synthetic-shop")
		setDefault("carrier_code", "ghn")
		setDefault("tracking_code", "E2E-TRK-"+orderID)
		setDefault("occurred_at", time.Now().UTC().Format(time.RFC3339))
		setDefault("direction", "FORWARD")
		setDefault("redelivery_count", 0)
	}
	return out
}

func payW2PublishNATSEvent(t *testing.T, eventType string, payload map[string]any) {
	t.Helper()
	addr := strings.TrimPrefix(strings.TrimSpace(os.Getenv("NATS_URL")), "nats://")
	if addr == "" {
		addr = "nats:4222"
	}
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatalf("dial NATS %s: %v", addr, err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	reader := bufio.NewReader(conn)
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatalf("read NATS INFO: %v", err)
	}
	envBody, err := json.Marshal(map[string]any{
		"event_id":    uuid.NewString(),
		"event_type":  eventType,
		"trace_id":    "payw2-rev-" + uuid.NewString(),
		"occurred_at": time.Now().UTC().Format(time.RFC3339Nano),
		"payload":     fillCanonicalEventPayload(eventType, payload),
	})
	if err != nil {
		t.Fatalf("marshal NATS envelope: %v", err)
	}
	cmd := fmt.Sprintf("CONNECT {\"verbose\":false,\"pedantic\":false}\r\nPUB %s %d\r\n%s\r\nPING\r\n",
		eventType, len(envBody), string(envBody))
	if _, err := conn.Write([]byte(cmd)); err != nil {
		t.Fatalf("write NATS PUB %s: %v", eventType, err)
	}
	line, err := reader.ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "PONG") {
		t.Fatalf("expected NATS PONG after PUB %s, got line=%q err=%v", eventType, line, err)
	}
}

// TestE2E_PaymentOps_ReviewFixes covers all 5 manager review findings:
// 1. COD RETURN_REQUESTED across all 4 event/RPC orderings (exactly 1 cod-collect, 1 refund, 0 escrow)
// 2. Reconciliation queue duplicate-accounting guard per provider_tx_id
// 3. Cross-midnight reconciliation lookup (no created_at cutoff)
// 4. Same Idempotency-Key with different action/payment_id/provider_refund_id -> 409
// 5. SettleRefund snapshots masked destination bank account into settlement_note
func TestE2E_PaymentOps_ReviewFixes(t *testing.T) {
	env := getEnv()
	ctx := env.ctx(t)
	adminTok := env.adminLogin(t)
	grpcClient := payW2DialGRPC(t, env)

	t.Run("1_CODReturnRequested_All4Orderings", func(t *testing.T) {
		orderings := []string{
			"delivered_returned_refund",
			"returned_refund_delivered",
			"refund_returned_delivered",
			"refund_delivered_returned",
		}
		for _, mode := range orderings {
			t.Run(mode, func(t *testing.T) {
				buyerID := "payw2-codret-buyer-" + uuid.NewString()
				buyerTok := MintAccessToken(env.cfg.JWTSecret, buyerID, "buyer")
				grpID := "payw2-codret-grp-" + uuid.NewString()
				ordID := "payw2-codret-ord-" + uuid.NewString()
				shopID := "payw2-codret-shop-" + uuid.NewString()
				const payAmt int64 = 230000

				beforeSum := payW2AssertLedgerBalanced(t, env)
				codBefore, _ := payW2FindAccount(beforeSum, "cod_receivable")
				escBefore, _ := payW2FindAccount(beforeSum, "escrow_liability")
				rpBefore, _ := payW2FindAccount(beforeSum, "refund_payable")

				pResp, err := grpcClient.CreatePayment(payW2AuthCtx(ctx, buyerTok), &paymentv1.CreatePaymentRequest{
					OrderGroupId: grpID,
					OrderId:      grpID,
					UserId:       buyerID,
					AmountVnd:    payAmt,
					Method:       paymentv1.PaymentMethod_PAYMENT_METHOD_COD,
					ShopAllocations: []*paymentv1.ShopAllocation{
						{
							OrderId:        ordID,
							ShopId:         shopID,
							SubtotalVnd:    210000,
							CommissionVnd:  10500,
							ShippingFeeVnd: 20000,
						},
					},
					IdempotencyKey: "payw2-codret-pay-" + uuid.NewString(),
				})
				if err != nil {
					t.Fatalf("CreatePayment COD failed: %v", err)
				}
				payID := pResp.GetPayment().GetPaymentId()
				rfKey := "order-return-refund:" + ordID
				svcSecret := envOr("SERVICE_JWT_SECRET", "dev-only-nexusmart-service-hs256-secret-change-me")
				if v := strings.TrimSpace(os.Getenv("SERVICE_JWT_SECRET")); v != "" {
					svcSecret = v
				}
				orderSvcTok := payW2MintServiceToken(svcSecret, "order-service")

				callRefund := func() error {
					_, rErr := grpcClient.CreateRefund(payW2AuthCtx(ctx, orderSvcTok), &paymentv1.CreateRefundRequest{
						PaymentId:      payID,
						OrderId:        ordID,
						AmountVnd:      payAmt,
						Reason:         "post-delivery COD return",
						IdempotencyKey: rfKey,
					})
					return rErr
				}
				publishDelivered := func() {
					payW2PublishNATSEvent(t, "order.delivered", map[string]any{
						"order_id":      ordID,
						"order_code":    ordID,
						"buyer_id":      buyerID,
						"shop_id":       shopID,
						"tracking_code": "TRK-" + ordID,
					})
				}
				publishReturned := func() {
					payW2PublishNATSEvent(t, "order.returned", map[string]any{
						"order_id":          ordID,
						"order_code":        ordID,
						"checkout_group_id": grpID,
						"buyer_id":          buyerID,
						"shop_id":           shopID,
						"payment_method":    "COD",
						"from_status":       "RETURN_REQUESTED",
						"total_payable_vnd": payAmt,
						"reason":            "post_delivery_cod_return",
						"was_paid":          true,
					})
				}
				waitPaymentSucceeded := func() {
					env.pollUntil(t, 8*time.Second, 150*time.Millisecond, mode+" COD payment SUCCEEDED", func() (bool, string) {
						g, gErr := grpcClient.GetPayment(payW2AuthCtx(ctx, adminTok), &paymentv1.GetPaymentRequest{PaymentId: payID})
						if gErr != nil {
							return false, gErr.Error()
						}
						return g.GetPayment().GetStatus() == paymentv1.PaymentStatus_PAYMENT_STATUS_SUCCEEDED, g.GetPayment().GetStatus().String()
					})
				}

				switch mode {
				case "delivered_returned_refund":
					publishDelivered()
					waitPaymentSucceeded()
					publishReturned()
					time.Sleep(300 * time.Millisecond)
					if err := callRefund(); err != nil {
						t.Fatalf("CreateRefund after delivered->returned failed: %v", err)
					}
				case "returned_refund_delivered":
					publishReturned()
					waitPaymentSucceeded()
					if err := callRefund(); err != nil {
						t.Fatalf("CreateRefund after returned failed: %v", err)
					}
					publishDelivered()
					time.Sleep(400 * time.Millisecond)
				case "refund_returned_delivered":
					// First refund attempt before collection fails (409 / AlreadyExists); watchdog retries after returned collects COD.
					if err := callRefund(); err == nil {
						t.Fatalf("expected initial CreateRefund on uncollected COD to fail before collection")
					}
					publishReturned()
					waitPaymentSucceeded()
					if err := callRefund(); err != nil {
						t.Fatalf("watchdog retry CreateRefund after returned failed: %v", err)
					}
					publishDelivered()
					time.Sleep(400 * time.Millisecond)
				case "refund_delivered_returned":
					if err := callRefund(); err == nil {
						t.Fatalf("expected initial CreateRefund on uncollected COD to fail before collection")
					}
					publishDelivered()
					waitPaymentSucceeded()
					publishReturned()
					time.Sleep(300 * time.Millisecond)
					if err := callRefund(); err != nil {
						t.Fatalf("watchdog retry CreateRefund after delivered->returned failed: %v", err)
					}
				}

				// Late order.completed must be a strict no-op (seller earns 0)
				payW2PublishNATSEvent(t, "order.completed", map[string]any{
					"order_id":          ordID,
					"order_code":        ordID,
					"buyer_id":          buyerID,
					"shop_id":           shopID,
					"subtotal_vnd":      int64(210000),
					"commission_vnd":    int64(10500),
					"total_payable_vnd": payAmt,
					"coins_reward":      int64(0),
				})
				time.Sleep(350 * time.Millisecond)

				g, err := grpcClient.GetPayment(payW2AuthCtx(ctx, adminTok), &paymentv1.GetPaymentRequest{PaymentId: payID})
				if err != nil {
					t.Fatalf("GetPayment failed: %v", err)
				}
				if g.GetPayment().GetStatus() != paymentv1.PaymentStatus_PAYMENT_STATUS_REFUNDED {
					t.Fatalf("%s: expected payment REFUNDED, got %v", mode, g.GetPayment().GetStatus())
				}

				bal, err := grpcClient.GetSellerBalance(payW2AuthCtx(ctx, adminTok), &paymentv1.GetSellerBalanceRequest{ShopId: shopID})
				if err != nil {
					t.Fatalf("GetSellerBalance failed: %v", err)
				}
				if bal.GetPendingVnd() != 0 || bal.GetAvailableVnd() != 0 {
					t.Fatalf("%s: late order.completed must be no-op, got pending=%d available=%d", mode, bal.GetPendingVnd(), bal.GetAvailableVnd())
				}

				afterSum := payW2AssertLedgerBalanced(t, env)
				codAfter, _ := payW2FindAccount(afterSum, "cod_receivable")
				escAfter, _ := payW2FindAccount(afterSum, "escrow_liability")
				rpAfter, _ := payW2FindAccount(afterSum, "refund_payable")

				if codAfter.DebitTotalVND-codBefore.DebitTotalVND != payAmt {
					t.Fatalf("%s: expected exactly one cod-collect (%d), got cod_receivable debit delta=%d",
						mode, payAmt, codAfter.DebitTotalVND-codBefore.DebitTotalVND)
				}
				if escAfter.BalanceVND-escBefore.BalanceVND != 0 {
					t.Fatalf("%s: expected net escrow_liability delta=0, got %d",
						mode, escAfter.BalanceVND-escBefore.BalanceVND)
				}
				if rpAfter.BalanceVND-rpBefore.BalanceVND != payAmt {
					t.Fatalf("%s: expected exactly one refund (%d), got refund_payable delta=%d",
						mode, payAmt, rpAfter.BalanceVND-rpBefore.BalanceVND)
				}
			})
		}
	})

	t.Run("2_ReconciliationQueue_NoDuplicateAndLateWebhookConflict409", func(t *testing.T) {
		buyerID := "payw2-nodup-buyer-" + uuid.NewString()
		buyerTok := MintAccessToken(env.cfg.JWTSecret, buyerID, "buyer")
		grpDup := "payw2-nodup-grp-" + uuid.NewString()
		grpLate := "payw2-lateq-grp-" + uuid.NewString()
		const payAmt int64 = 150000

		pDupResp, err := grpcClient.CreatePayment(payW2AuthCtx(ctx, buyerTok), &paymentv1.CreatePaymentRequest{
			OrderGroupId:    grpDup,
			OrderId:         grpDup,
			UserId:          buyerID,
			AmountVnd:       payAmt,
			Method:          paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
			ShopAllocations: []*paymentv1.ShopAllocation{{OrderId: grpDup, ShopId: "shop-nodup", SubtotalVnd: payAmt}},
			IdempotencyKey:  "payw2-nodup-p-" + uuid.NewString(),
		})
		if err != nil {
			t.Fatalf("CreatePayment pDup: %v", err)
		}
		pLateResp, err := grpcClient.CreatePayment(payW2AuthCtx(ctx, buyerTok), &paymentv1.CreatePaymentRequest{
			OrderGroupId:    grpLate,
			OrderId:         grpLate,
			UserId:          buyerID,
			AmountVnd:       payAmt,
			Method:          paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
			ShopAllocations: []*paymentv1.ShopAllocation{{OrderId: grpLate, ShopId: "shop-nodup", SubtotalVnd: payAmt}},
			IdempotencyKey:  "payw2-lateq-p-" + uuid.NewString(),
		})
		if err != nil {
			t.Fatalf("CreatePayment pLate: %v", err)
		}
		pDupID := pDupResp.GetPayment().GetPaymentId()
		pLateID := pLateResp.GetPayment().GetPaymentId()

		// (c) Mismatch webhook queues item, then run_reconciliation for today VN MUST NOT insert a second row for same provider_tx_id
		txDup := "PAYW2-NODUP-" + uuid.NewString()
		payW2SendVietQRWebhook(t, env, pDupID, grpDup, txDup, payAmt-5000)

		// (b) Late webhook on cancelled payment creates late-webhook:<txLate> refund;
		// if reconciliation queues it as status mismatch, attempting REFUND or ASSIGN via queue MUST return 409.
		payW2PublishNATSEvent(t, "order.cancelled", map[string]any{
			"order_id":   grpLate,
			"order_code": grpLate,
			"buyer_id":   buyerID,
			"shop_id":    "shop-nodup",
			"reason":     "e2e_cancelled",
			"was_paid":   false,
		})
		env.pollUntil(t, 8*time.Second, 150*time.Millisecond, "pLate CANCELLED", func() (bool, string) {
			g, gErr := grpcClient.GetPayment(payW2AuthCtx(ctx, adminTok), &paymentv1.GetPaymentRequest{PaymentId: pLateID})
			if gErr != nil {
				return false, gErr.Error()
			}
			return g.GetPayment().GetStatus() == paymentv1.PaymentStatus_PAYMENT_STATUS_CANCELLED, g.GetPayment().GetStatus().String()
		})
		txLate := "PAYW2-LATEQ-" + uuid.NewString()
		whLate := payW2SendVietQRWebhook(t, env, pLateID, grpLate, txLate, payAmt)
		if !strings.Contains(whLate.Status, "late_payment_refund_queued") {
			t.Fatalf("expected late_payment_refund_queued for cancelled payment, got %q", whLate.Status)
		}

		vnLoc := time.FixedZone("Asia/Ho_Chi_Minh", 7*3600)
		vnTodayStr := time.Now().In(vnLoc).Format("2006-01-02")
		env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/payments/reconcile", adminTok, nil, map[string]string{
			"date": vnTodayStr,
		}, http.StatusOK)

		qResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/reconciliation/queue?page=1&page_size=200", adminTok, nil, nil, http.StatusOK)
		var qList payW2QueueListResp
		if err := DecodeJSON(qResp, &qList); err != nil {
			t.Fatalf("decode reconciliation queue: %v", err)
		}
		dupCount := 0
		var dupQueueID string
		var lateQueueID string
		for _, it := range qList.Items {
			if it.ProviderTxID == txDup {
				dupCount++
				dupQueueID = it.ID
			}
			if it.ProviderTxID == txLate {
				lateQueueID = it.ID
			}
		}
		if dupCount != 1 {
			t.Fatalf("P1-2(c): expected exactly 1 reconciliation_queue item for %s after webhook+reconcile, got %d", txDup, dupCount)
		}
		payW2CleanupResolveQueueItem(t, env, adminTok, dupQueueID, "REFUND", pDupID, "e2e cleanup underpaid txDup")
		if lateQueueID != "" {
			resolveLatePath := fmt.Sprintf("/api/v1/admin/reconciliation/queue/%s/resolve", url.PathEscape(lateQueueID))
			env.mustDoJSON(t, http.MethodPost, resolveLatePath, adminTok, map[string]string{
				"Idempotency-Key": "payw2-late-double-" + uuid.NewString(),
			}, map[string]string{
				"action":     "REFUND",
				"payment_id": pLateID,
				"note":       "must reject 409 because late-webhook refund already exists",
			}, http.StatusConflict)
		}
		payW2AssertLedgerBalanced(t, env)
	})

	t.Run("3_Reconciliation_CrossMidnightLookup", func(t *testing.T) {
		buyerID := "payw2-midnight-buyer-" + uuid.NewString()
		buyerTok := MintAccessToken(env.cfg.JWTSecret, buyerID, "buyer")
		grpID := "payw2-midnight-grp-" + uuid.NewString()
		ordID := "payw2-midnight-ord-" + uuid.NewString()
		shopID := "payw2-midnight-shop-" + uuid.NewString()
		const payAmt int64 = 175000

		pResp, err := grpcClient.CreatePayment(payW2AuthCtx(ctx, buyerTok), &paymentv1.CreatePaymentRequest{
			OrderGroupId: grpID,
			OrderId:      grpID,
			UserId:       buyerID,
			AmountVnd:    payAmt,
			Method:       paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
			ShopAllocations: []*paymentv1.ShopAllocation{
				{OrderId: ordID, ShopId: shopID, SubtotalVnd: payAmt},
			},
			IdempotencyKey: "payw2-midnight-pay-" + uuid.NewString(),
		})
		if err != nil {
			t.Fatalf("CreatePayment VietQR failed: %v", err)
		}
		payID := pResp.GetPayment().GetPaymentId()

		// Confirm payment with occurred_at on YESTERDAY VN (different calendar day than created_at=TODAY VN)
		vnLoc := time.FixedZone("Asia/Ho_Chi_Minh", 7*3600)
		yesterdayVN := time.Now().In(vnLoc).AddDate(0, 0, -1)
		yesterdayDateStr := yesterdayVN.Format("2006-01-02")
		occurredYesterday := time.Date(yesterdayVN.Year(), yesterdayVN.Month(), yesterdayVN.Day(), 23, 55, 0, 0, vnLoc).UTC()
		txID := "PAYW2-MIDNIGHT-" + uuid.NewString()

		whResp, err := env.client.PostSignedWebhook(ctx, "/webhooks/vietqr", PaymentWebhookPayload{
			Provider:     "vietqr_mock",
			ProviderTxID: txID,
			PaymentID:    payID,
			OrderGroupID: grpID,
			AmountVND:    payAmt,
			Status:       "SUCCEEDED",
			Reference:    "MIDNIGHT " + grpID,
			OccurredAt:   occurredYesterday.Format(time.RFC3339),
		}, env.cfg.PaymentWebhookSecret, time.Now().UTC(), "")
		if err != nil || whResp.StatusCode != http.StatusOK {
			t.Fatalf("post midnight webhook failed: err=%v status=%d", err, whResp.StatusCode)
		}

		// Reconciling yesterday VN MUST find this payment by provider_tx_id/provider_order_code
		// (NOT classify it as missing_local just because created_at is today VN).
		recResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/payments/reconcile", adminTok, nil, map[string]string{
			"date": yesterdayDateStr,
		}, http.StatusOK)
		var recOut payW2ReconcileResp
		if err := DecodeJSON(recResp, &recOut); err != nil {
			t.Fatalf("decode reconcile yesterday: %v", err)
		}
		foundMatched := false
		for _, it := range recOut.Items {
			if it.ProviderTxID == txID {
				if !StatusEquals(it.Status, "MATCHED") || it.PaymentID != payID {
					t.Fatalf("expected cross-midnight tx %s to be MATCHED with payment %s, got status=%q payment_id=%q",
						txID, payID, it.Status, it.PaymentID)
				}
				foundMatched = true
			}
		}
		if !foundMatched {
			t.Fatalf("cross-midnight tx %s not found in reconciliation items for %s", txID, yesterdayDateStr)
		}
	})

	t.Run("4_And_5_IdempotencyConflict409_And_MaskedAccountSnapshot", func(t *testing.T) {
		buyerID := "payw2-idem-buyer-" + uuid.NewString()
		buyerTok := MintAccessToken(env.cfg.JWTSecret, buyerID, "buyer")
		grp1 := "payw2-idem-grp1-" + uuid.NewString()
		grp2 := "payw2-idem-grp2-" + uuid.NewString()
		const payAmt int64 = 160000

		p1Resp, err := grpcClient.CreatePayment(payW2AuthCtx(ctx, buyerTok), &paymentv1.CreatePaymentRequest{
			OrderGroupId:    grp1,
			OrderId:         grp1,
			UserId:          buyerID,
			AmountVnd:       payAmt,
			Method:          paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
			ShopAllocations: []*paymentv1.ShopAllocation{{OrderId: grp1, ShopId: "shop-idem", SubtotalVnd: payAmt}},
			IdempotencyKey:  "payw2-idem-p1-" + uuid.NewString(),
		})
		if err != nil {
			t.Fatalf("CreatePayment p1: %v", err)
		}
		p2Resp, err := grpcClient.CreatePayment(payW2AuthCtx(ctx, buyerTok), &paymentv1.CreatePaymentRequest{
			OrderGroupId:    grp2,
			OrderId:         grp2,
			UserId:          buyerID,
			AmountVnd:       payAmt,
			Method:          paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
			ShopAllocations: []*paymentv1.ShopAllocation{{OrderId: grp2, ShopId: "shop-idem", SubtotalVnd: payAmt}},
			IdempotencyKey:  "payw2-idem-p2-" + uuid.NewString(),
		})
		if err != nil {
			t.Fatalf("CreatePayment p2: %v", err)
		}
		p1ID := p1Resp.GetPayment().GetPaymentId()
		p2ID := p2Resp.GetPayment().GetPaymentId()

		// Queue underpaid mismatch for p1 -> resolve REFUND with key K -> replay with different action/payment_id MUST 409
		txMismatch := "PAYW2-IDEM-MIS-" + uuid.NewString()
		payW2SendVietQRWebhook(t, env, p1ID, grp1, txMismatch, payAmt-10000)
		qItem := payW2FindQueueItemByTx(t, env, adminTok, txMismatch)

		resolvePath := fmt.Sprintf("/api/v1/admin/reconciliation/queue/%s/resolve", url.PathEscape(qItem.ID))
		rqKey := "payw2-rq-idem-" + uuid.NewString()
		res1 := env.mustDoJSON(t, http.MethodPost, resolvePath, adminTok, map[string]string{
			"Idempotency-Key": rqKey,
		}, map[string]string{
			"action":     "REFUND",
			"payment_id": p1ID,
			"note":       "refund mismatch",
		}, http.StatusOK)
		var out1 payW2ResolveQueueResp
		if err := DecodeJSON(res1, &out1); err != nil || out1.Refund == nil {
			t.Fatalf("decode resolve #1: err=%v out=%+v", err, out1)
		}
		if out1.Refund.IdempotencyKey != "recon-refund:"+txMismatch {
			t.Fatalf("P1-2: queue REFUND refund key must be scoped by provider_tx_id (recon-refund:%s), got %q",
				txMismatch, out1.Refund.IdempotencyKey)
		}

		// Same Idempotency-Key + DIFFERENT action -> 409
		env.mustDoJSON(t, http.MethodPost, resolvePath, adminTok, map[string]string{
			"Idempotency-Key": rqKey,
		}, map[string]string{
			"action":     "ASSIGN",
			"payment_id": p1ID,
			"note":       "mismatch action",
		}, http.StatusConflict)

		// Same Idempotency-Key + DIFFERENT payment_id -> 409
		env.mustDoJSON(t, http.MethodPost, resolvePath, adminTok, map[string]string{
			"Idempotency-Key": rqKey,
		}, map[string]string{
			"action":     "REFUND",
			"payment_id": p2ID,
			"note":       "mismatch payment_id",
		}, http.StatusConflict)

		// Submit buyer bank account on the refund, then settle and verify:
		// (a) settlement_note snapshots masked destination account without full number
		// (b) same SettleRefund Idempotency-Key with DIFFERENT provider_refund_id -> 409
		refundID := out1.Refund.RefundID
		bankPath := fmt.Sprintf("/api/v1/refunds/%s/bank-account", url.PathEscape(refundID))
		env.mustDoJSON(t, http.MethodPost, bankPath, buyerTok, nil, map[string]string{
			"bank_code":           "VCB",
			"bank_account_number": "19034567895678",
			"bank_account_name":   "NGUYEN VAN SNAP",
		}, http.StatusOK)

		settlePath := fmt.Sprintf("/api/v1/admin/refunds/%s/settle", url.PathEscape(refundID))
		settleKey := "payw2-settle-idem-" + uuid.NewString()
		settleResp := env.mustDoJSON(t, http.MethodPost, settlePath, adminTok, map[string]string{
			"Idempotency-Key": settleKey,
		}, map[string]string{
			"provider_refund_id": "FT-SNAP-A",
			"note":               "Manual FT",
		}, http.StatusOK)
		var settledOut payW2RefundWrap
		if err := DecodeJSON(settleResp, &settledOut); err != nil {
			t.Fatalf("decode settle: %v", err)
		}
		if !strings.Contains(settledOut.Refund.SettlementNote, "VCB") ||
			!strings.Contains(settledOut.Refund.SettlementNote, "****5678") ||
			strings.Contains(settledOut.Refund.SettlementNote, "19034567895678") {
			t.Fatalf("P3-5: settlement_note must snapshot masked account (VCB ****5678) without full number, got %q",
				settledOut.Refund.SettlementNote)
		}

		// Same Idempotency-Key + DIFFERENT provider_refund_id -> 409
		env.mustDoJSON(t, http.MethodPost, settlePath, adminTok, map[string]string{
			"Idempotency-Key": settleKey,
		}, map[string]string{
			"provider_refund_id": "FT-SNAP-DIFFERENT",
			"note":               "Manual FT",
		}, http.StatusConflict)

		payW2AssertLedgerBalanced(t, env)
	})
}

// TestE2E_PaymentOps_Review2Independent verifies independent review findings (P0-1, P1-3, P1-4, P1-5, P2-2, P2-4, P2-5, P2-6).
func TestE2E_PaymentOps_Review2Independent(t *testing.T) {
	env := getEnv()
	ctx := env.ctx(t)
	adminTok := env.adminLogin(t)
	grpcClient := payW2DialGRPC(t, env)
	vnLoc := time.FixedZone("Asia/Ho_Chi_Minh", 7*3600)

	t.Run("P0_1_And_P2_2_ResolvedQueueItemsMatchedNeverDoubleAccounted", func(t *testing.T) {
		buyerID := "payw2-r2-p01-buyer-" + uuid.NewString()
		buyerTok := MintAccessToken(env.cfg.JWTSecret, buyerID, "buyer")
		grpOver := "payw2-r2-over-grp-" + uuid.NewString()
		grpUnder := "payw2-r2-under-grp-" + uuid.NewString()
		grpOrphan := "payw2-r2-orphan-grp-" + uuid.NewString()
		const payAmt int64 = 200000

		createVietQR := func(grp string) string {
			resp, err := grpcClient.CreatePayment(payW2AuthCtx(ctx, buyerTok), &paymentv1.CreatePaymentRequest{
				OrderGroupId:    grp,
				OrderId:         grp,
				UserId:          buyerID,
				AmountVnd:       payAmt,
				Method:          paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
				ShopAllocations: []*paymentv1.ShopAllocation{{OrderId: grp, ShopId: "shop-r2-p01", SubtotalVnd: payAmt}},
				IdempotencyKey:  "payw2-r2-p01-" + grp,
			})
			if err != nil {
				t.Fatalf("CreatePayment %s: %v", grp, err)
			}
			return resp.GetPayment().GetPaymentId()
		}

		pOver := createVietQR(grpOver)
		pUnder := createVietQR(grpUnder)
		pOrphan := createVietQR(grpOrphan)

		// 1. Overpay ASSIGN (230k on 200k)
		txOver := "PAYW2-R2-OVER-" + uuid.NewString()
		payW2SendVietQRWebhook(t, env, pOver, grpOver, txOver, payAmt+30000)
		qOver := payW2FindQueueItemByTx(t, env, adminTok, txOver)
		env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/admin/reconciliation/queue/%s/resolve", url.PathEscape(qOver.ID)), adminTok, map[string]string{
			"Idempotency-Key": "r2-assign-over-" + uuid.NewString(),
		}, map[string]string{
			"action":     "ASSIGN",
			"payment_id": pOver,
			"note":       "assign overpay",
		}, http.StatusOK)

		// 2. Underpay REFUND (150k on 200k), then cancel payment -> P0-1 Branch B:
		// reconciliation MUST NOT create a 2nd `late-webhook:<tx>` refund or flag AMOUNT_MISMATCH!
		txUnder := "PAYW2-R2-UNDER-" + uuid.NewString()
		payW2SendVietQRWebhook(t, env, pUnder, grpUnder, txUnder, payAmt-50000)
		qUnder := payW2FindQueueItemByTx(t, env, adminTok, txUnder)
		env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/admin/reconciliation/queue/%s/resolve", url.PathEscape(qUnder.ID)), adminTok, map[string]string{
			"Idempotency-Key": "r2-refund-under-" + uuid.NewString(),
		}, map[string]string{
			"action":     "REFUND",
			"payment_id": pUnder,
			"note":       "refund underpay",
		}, http.StatusOK)
		payW2PublishNATSEvent(t, "order.cancelled", map[string]any{
			"order_id":   grpUnder,
			"order_code": grpUnder,
			"buyer_id":   buyerID,
			"shop_id":    "shop-r2-p01",
			"reason":     "e2e_cancelled",
			"was_paid":   false,
		})
		env.pollUntil(t, 6*time.Second, 150*time.Millisecond, "pUnder CANCELLED after queue REFUND", func() (bool, string) {
			g, gErr := grpcClient.GetPayment(payW2AuthCtx(ctx, adminTok), &paymentv1.GetPaymentRequest{PaymentId: pUnder})
			if gErr != nil {
				return false, gErr.Error()
			}
			return g.GetPayment().GetStatus() == paymentv1.PaymentStatus_PAYMENT_STATUS_CANCELLED, g.GetPayment().GetStatus().String()
		})

		// 3. Orphan order_not_found REFUND (200k)
		txOrphan := "PAYW2-R2-ORPHAN-" + uuid.NewString()
		whOrphan, err := env.client.PostSignedWebhook(ctx, "/webhooks/vietqr", PaymentWebhookPayload{
			Provider:     "vietqr_mock",
			ProviderTxID: txOrphan,
			AmountVND:    payAmt,
			Status:       "SUCCEEDED",
			Reference:    "NO-ORDER-REF",
			OccurredAt:   time.Now().UTC().Format(time.RFC3339),
		}, env.cfg.PaymentWebhookSecret, time.Now().UTC(), "")
		if err != nil || whOrphan.StatusCode != http.StatusOK {
			t.Fatalf("orphan webhook failed: err=%v status=%d", err, whOrphan.StatusCode)
		}
		qOrphan := payW2FindQueueItemByTx(t, env, adminTok, txOrphan)
		env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/admin/reconciliation/queue/%s/resolve", url.PathEscape(qOrphan.ID)), adminTok, map[string]string{
			"Idempotency-Key": "r2-refund-orphan-" + uuid.NewString(),
		}, map[string]string{
			"action":     "REFUND",
			"payment_id": pOrphan,
			"note":       "refund orphan",
		}, http.StatusOK)

		beforeRec := payW2AssertLedgerBalanced(t, env)
		gwBefore, _ := payW2FindAccount(beforeRec, "gateway_clearing")
		rpBefore, _ := payW2FindAccount(beforeRec, "refund_payable")

		// Run reconciliation for today VN: all 3 resolved txs MUST be classified MATCHED (never repeated AMOUNT_MISMATCH or MISSING_LOCAL, and 0 extra gateway_clearing debits).
		todayVN := time.Now().In(vnLoc).Format("2006-01-02")
		recResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/payments/reconcile", adminTok, nil, map[string]string{
			"date": todayVN,
		}, http.StatusOK)
		var recOut payW2ReconcileResp
		if err := DecodeJSON(recResp, &recOut); err != nil {
			t.Fatalf("decode reconcile: %v", err)
		}
		seen := map[string]string{}
		for _, it := range recOut.Items {
			if it.ProviderTxID == txOver || it.ProviderTxID == txUnder || it.ProviderTxID == txOrphan {
				seen[it.ProviderTxID] = it.Status
			}
		}
		for _, tx := range []string{txOver, txUnder, txOrphan} {
			if !StatusEquals(seen[tx], "MATCHED") {
				t.Fatalf("P0-1/P2-2: resolved tx %s must reconcile as MATCHED, got %q", tx, seen[tx])
			}
		}

		afterRec := payW2AssertLedgerBalanced(t, env)
		gwAfter, _ := payW2FindAccount(afterRec, "gateway_clearing")
		rpAfter, _ := payW2FindAccount(afterRec, "refund_payable")
		if gwAfter.DebitTotalVND != gwBefore.DebitTotalVND || rpAfter.BalanceVND != rpBefore.BalanceVND {
			t.Fatalf("P0-1: reconciliation must not double-debit gateway_clearing or double-credit refund_payable")
		}
	})

	t.Run("P1_3_PaidAtUsesOccurredAtAcrossMidnight_NoFalseMissingProvider", func(t *testing.T) {
		buyerID := "payw2-r2-p13-buyer-" + uuid.NewString()
		buyerTok := MintAccessToken(env.cfg.JWTSecret, buyerID, "buyer")
		grpWebhook := "payw2-r2-p13-wh-" + uuid.NewString()
		grpAssign := "payw2-r2-p13-as-" + uuid.NewString()
		const payAmt int64 = 185000

		createP := func(grp string) string {
			r, err := grpcClient.CreatePayment(payW2AuthCtx(ctx, buyerTok), &paymentv1.CreatePaymentRequest{
				OrderGroupId:    grp,
				OrderId:         grp,
				UserId:          buyerID,
				AmountVnd:       payAmt,
				Method:          paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
				ShopAllocations: []*paymentv1.ShopAllocation{{OrderId: grp, ShopId: "shop-r2-p13", SubtotalVnd: payAmt}},
				IdempotencyKey:  "payw2-r2-p13-" + grp,
			})
			if err != nil {
				t.Fatalf("CreatePayment %s: %v", grp, err)
			}
			return r.GetPayment().GetPaymentId()
		}

		pWh := createP(grpWebhook)
		pAs := createP(grpAssign)

		yesterdayVN := time.Now().In(vnLoc).AddDate(0, 0, -1)
		yesterdayStr := yesterdayVN.Format("2006-01-02")
		todayStr := time.Now().In(vnLoc).Format("2006-01-02")
		occurredYesterday := time.Date(yesterdayVN.Year(), yesterdayVN.Month(), yesterdayVN.Day(), 23, 59, 45, 0, vnLoc).UTC()

		// 1. Direct webhook confirmation with yesterday occurred_at
		txWh := "PAYW2-R2-P13-WH-" + uuid.NewString()
		whResp, err := env.client.PostSignedWebhook(ctx, "/webhooks/vietqr", PaymentWebhookPayload{
			Provider:     "vietqr_mock",
			ProviderTxID: txWh,
			PaymentID:    pWh,
			OrderGroupID: grpWebhook,
			AmountVND:    payAmt,
			Status:       "SUCCEEDED",
			Reference:    "P13WH " + grpWebhook,
			OccurredAt:   occurredYesterday.Format(time.RFC3339),
		}, env.cfg.PaymentWebhookSecret, time.Now().UTC(), "")
		if err != nil || whResp.StatusCode != http.StatusOK {
			t.Fatalf("P1-3 webhook failed: err=%v status=%d", err, whResp.StatusCode)
		}

		// 2. Yesterday overpay webhook resolved via ASSIGN today
		txAs := "PAYW2-R2-P13-AS-" + uuid.NewString()
		whAs, err := env.client.PostSignedWebhook(ctx, "/webhooks/vietqr", PaymentWebhookPayload{
			Provider:     "vietqr_mock",
			ProviderTxID: txAs,
			PaymentID:    pAs,
			OrderGroupID: grpAssign,
			AmountVND:    payAmt + 5000,
			Status:       "SUCCEEDED",
			Reference:    "P13AS " + grpAssign,
			OccurredAt:   occurredYesterday.Format(time.RFC3339),
		}, env.cfg.PaymentWebhookSecret, time.Now().UTC(), "")
		if err != nil || whAs.StatusCode != http.StatusOK {
			t.Fatalf("P1-3 assign webhook failed: err=%v status=%d", err, whAs.StatusCode)
		}
		qAs := payW2FindQueueItemByTx(t, env, adminTok, txAs)
		env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/admin/reconciliation/queue/%s/resolve", url.PathEscape(qAs.ID)), adminTok, map[string]string{
			"Idempotency-Key": "r2-p13-assign-" + uuid.NewString(),
		}, map[string]string{
			"action":     "ASSIGN",
			"payment_id": pAs,
			"note":       "assign yesterday transfer today",
		}, http.StatusOK)

		// Reconciling TODAY VN must NOT flag either payment as MISSING_PROVIDER!
		todayResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/payments/reconcile", adminTok, nil, map[string]string{
			"date": todayStr,
		}, http.StatusOK)
		var todayOut payW2ReconcileResp
		if err := DecodeJSON(todayResp, &todayOut); err != nil {
			t.Fatalf("decode today reconcile: %v", err)
		}
		for _, it := range todayOut.Items {
			if (it.PaymentID == pWh || it.PaymentID == pAs) && StatusEquals(it.Status, "MISSING_PROVIDER") {
				t.Fatalf("P1-3: yesterday-occurred payment %s was falsely flagged MISSING_PROVIDER on today %s (paid_at used NOW() instead of occurred_at)",
					it.PaymentID, todayStr)
			}
		}

		// Reconciling YESTERDAY VN must MATCH both!
		yestResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/payments/reconcile", adminTok, nil, map[string]string{
			"date": yesterdayStr,
		}, http.StatusOK)
		var yestOut payW2ReconcileResp
		if err := DecodeJSON(yestResp, &yestOut); err != nil {
			t.Fatalf("decode yesterday reconcile: %v", err)
		}
		matchedWh, matchedAs := false, false
		for _, it := range yestOut.Items {
			if it.ProviderTxID == txWh && StatusEquals(it.Status, "MATCHED") {
				matchedWh = true
			}
			if it.ProviderTxID == txAs && StatusEquals(it.Status, "MATCHED") {
				matchedAs = true
			}
		}
		if !matchedWh || !matchedAs {
			t.Fatalf("P1-3: expected both yesterday transfers MATCHED on %s (matchedWh=%v matchedAs=%v)", yesterdayStr, matchedWh, matchedAs)
		}
	})

	t.Run("P1_4_ReleaseEscrowRejectsHeldAndAwaitingCollectionWith409", func(t *testing.T) {
		buyerID := "payw2-r2-p14-buyer-" + uuid.NewString()
		buyerTok := MintAccessToken(env.cfg.JWTSecret, buyerID, "buyer")
		shopID := "payw2-r2-p14-shop-" + uuid.NewString()

		// 1. VietQR in `held` (not completed yet) -> ReleaseEscrow MUST return AlreadyExists (409)
		ordHeld := "payw2-r2-p14-held-" + uuid.NewString()
		pHeldResp, err := grpcClient.CreatePayment(payW2AuthCtx(ctx, buyerTok), &paymentv1.CreatePaymentRequest{
			OrderGroupId:    ordHeld,
			OrderId:         ordHeld,
			UserId:          buyerID,
			AmountVnd:       100000,
			Method:          paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
			ShopAllocations: []*paymentv1.ShopAllocation{{OrderId: ordHeld, ShopId: shopID, SubtotalVnd: 100000, CommissionVnd: 5000}},
			IdempotencyKey:  "payw2-r2-p14-held-" + uuid.NewString(),
		})
		if err != nil {
			t.Fatalf("CreatePayment held: %v", err)
		}
		payW2SendVietQRWebhook(t, env, pHeldResp.GetPayment().GetPaymentId(), ordHeld, "PAYW2-R2-P14-"+uuid.NewString(), 100000)

		_, heldErr := grpcClient.ReleaseEscrow(payW2AuthCtx(ctx, adminTok), &paymentv1.ReleaseEscrowRequest{
			OrderId: ordHeld,
			ShopId:  shopID,
		})
		if status.Code(heldErr) != codes.AlreadyExists {
			t.Fatalf("P1-4: ReleaseEscrow on held (uncompleted) allocation must return AlreadyExists (409), got %v", heldErr)
		}

		// 2. COD in `awaiting_collection` -> ReleaseEscrow MUST return AlreadyExists (409) and NOT collect COD
		ordCod := "payw2-r2-p14-cod-" + uuid.NewString()
		pCodResp, err := grpcClient.CreatePayment(payW2AuthCtx(ctx, buyerTok), &paymentv1.CreatePaymentRequest{
			OrderGroupId:    ordCod,
			OrderId:         ordCod,
			UserId:          buyerID,
			AmountVnd:       120000,
			Method:          paymentv1.PaymentMethod_PAYMENT_METHOD_COD,
			ShopAllocations: []*paymentv1.ShopAllocation{{OrderId: ordCod, ShopId: shopID, SubtotalVnd: 120000, CommissionVnd: 6000}},
			IdempotencyKey:  "payw2-r2-p14-cod-" + uuid.NewString(),
		})
		if err != nil {
			t.Fatalf("CreatePayment cod: %v", err)
		}
		_, codErr := grpcClient.ReleaseEscrow(payW2AuthCtx(ctx, adminTok), &paymentv1.ReleaseEscrowRequest{
			OrderId: ordCod,
			ShopId:  shopID,
		})
		if status.Code(codErr) != codes.AlreadyExists {
			t.Fatalf("P1-4: ReleaseEscrow on awaiting_collection COD must return AlreadyExists (409), got %v", codErr)
		}
		gCod, err := grpcClient.GetPayment(payW2AuthCtx(ctx, adminTok), &paymentv1.GetPaymentRequest{PaymentId: pCodResp.GetPayment().GetPaymentId()})
		if err != nil || gCod.GetPayment().GetStatus() != paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING {
			t.Fatalf("P1-4: rejected ReleaseEscrow must leave COD PENDING, got err=%v status=%v", err, gCod.GetPayment().GetStatus())
		}

		// 3. After `order.completed` settles `ordHeld` -> `pending_release`, admin ReleaseEscrow succeeds
		payW2PublishNATSEvent(t, "order.completed", map[string]any{
			"order_id":          ordHeld,
			"order_code":        ordHeld,
			"buyer_id":          buyerID,
			"shop_id":           shopID,
			"subtotal_vnd":      int64(100000),
			"commission_vnd":    int64(5000),
			"total_payable_vnd": int64(100000),
			"coins_reward":      int64(0),
		})
		env.pollUntil(t, 8*time.Second, 150*time.Millisecond, "P1-4 held order settled to pending_release", func() (bool, string) {
			b, bErr := grpcClient.GetSellerBalance(payW2AuthCtx(ctx, adminTok), &paymentv1.GetSellerBalanceRequest{ShopId: shopID})
			if bErr != nil {
				return false, bErr.Error()
			}
			return b.GetPendingVnd() == 95000, fmt.Sprintf("pending=%d available=%d", b.GetPendingVnd(), b.GetAvailableVnd())
		})
		relResp, err := grpcClient.ReleaseEscrow(payW2AuthCtx(ctx, adminTok), &paymentv1.ReleaseEscrowRequest{
			OrderId: ordHeld,
			ShopId:  shopID,
		})
		if err != nil || !relResp.GetReleased() || relResp.GetReleasedAmountVnd() != 95000 {
			t.Fatalf("P1-4: ReleaseEscrow after pending_release should release 95000, got err=%v resp=%+v", err, relResp)
		}
	})

	t.Run("P1_5_SuspendWithUnknownPaymentIDReturns404Not500", func(t *testing.T) {
		txID := "PAYW2-R2-P15-" + uuid.NewString()
		whResp, err := env.client.PostSignedWebhook(ctx, "/webhooks/vietqr", PaymentWebhookPayload{
			Provider:     "vietqr_mock",
			ProviderTxID: txID,
			AmountVND:    99000,
			Status:       "SUCCEEDED",
			Reference:    "UNKNOWN-P15",
			OccurredAt:   time.Now().UTC().Format(time.RFC3339),
		}, env.cfg.PaymentWebhookSecret, time.Now().UTC(), "")
		if err != nil || whResp.StatusCode != http.StatusOK {
			t.Fatalf("P1-5 webhook failed: err=%v status=%d", err, whResp.StatusCode)
		}
		qItem := payW2FindQueueItemByTx(t, env, adminTok, txID)
		payW2CleanupResolveQueueItem(t, env, adminTok, qItem.ID, "SUSPEND", "", "e2e cleanup orphan P1-5 item")
		resolvePath := fmt.Sprintf("/api/v1/admin/reconciliation/queue/%s/resolve", url.PathEscape(qItem.ID))
		env.mustDoJSON(t, http.MethodPost, resolvePath, adminTok, map[string]string{
			"Idempotency-Key": "r2-p15-suspend-" + uuid.NewString(),
		}, map[string]string{
			"action":     "SUSPEND",
			"payment_id": uuid.NewString(),
			"note":       "invalid payment uuid should 404 not 500",
		}, http.StatusNotFound)
	})

	t.Run("P2_4_PostSettlementClawbackDoesNotReleaseNewerOrderHoldEarly", func(t *testing.T) {
		buyerID := "payw2-r2-p24-buyer-" + uuid.NewString()
		buyerTok := MintAccessToken(env.cfg.JWTSecret, buyerID, "buyer")
		shopID := "payw2-r2-p24-shop-" + uuid.NewString()

		createAndComplete := func(tag string, subtotal, commission int64) (string, string) {
			ordID := "payw2-r2-p24-" + tag + "-" + uuid.NewString()
			pResp, err := grpcClient.CreatePayment(payW2AuthCtx(ctx, buyerTok), &paymentv1.CreatePaymentRequest{
				OrderGroupId:    ordID,
				OrderId:         ordID,
				UserId:          buyerID,
				AmountVnd:       subtotal,
				Method:          paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
				ShopAllocations: []*paymentv1.ShopAllocation{{OrderId: ordID, ShopId: shopID, SubtotalVnd: subtotal, CommissionVnd: commission}},
				IdempotencyKey:  "payw2-r2-p24-" + ordID,
			})
			if err != nil {
				t.Fatalf("CreatePayment %s: %v", tag, err)
			}
			payID := pResp.GetPayment().GetPaymentId()
			payW2SendVietQRWebhook(t, env, payID, ordID, "PAYW2-R2-P24-"+tag+"-"+uuid.NewString(), subtotal)
			payW2PublishNATSEvent(t, "order.completed", map[string]any{
				"order_id":          ordID,
				"order_code":        ordID,
				"buyer_id":          buyerID,
				"shop_id":           shopID,
				"subtotal_vnd":      subtotal,
				"commission_vnd":    commission,
				"total_payable_vnd": subtotal,
				"coins_reward":      int64(0),
			})
			return payID, ordID
		}

		// ord0: 200k (seller net 190k) -> settled & released => available=190k, pending=0
		pay0, ord0 := createAndComplete("ord0", 200000, 10000)
		env.pollUntil(t, 8*time.Second, 150*time.Millisecond, "ord0 pending=190k", func() (bool, string) {
			b, err := grpcClient.GetSellerBalance(payW2AuthCtx(ctx, adminTok), &paymentv1.GetSellerBalanceRequest{ShopId: shopID})
			if err != nil {
				return false, err.Error()
			}
			return b.GetPendingVnd() == 190000, fmt.Sprintf("pending=%d", b.GetPendingVnd())
		})
		if _, err := grpcClient.ReleaseEscrow(payW2AuthCtx(ctx, adminTok), &paymentv1.ReleaseEscrowRequest{OrderId: ord0, ShopId: shopID}); err != nil {
			t.Fatalf("ReleaseEscrow ord0: %v", err)
		}

		// ord1: 100k (seller net 95k) -> settled => available=190k, pending=95k
		_, ord1 := createAndComplete("ord1", 100000, 5000)
		env.pollUntil(t, 8*time.Second, 150*time.Millisecond, "ord1 pending=95k", func() (bool, string) {
			b, err := grpcClient.GetSellerBalance(payW2AuthCtx(ctx, adminTok), &paymentv1.GetSellerBalanceRequest{ShopId: shopID})
			if err != nil {
				return false, err.Error()
			}
			return b.GetPendingVnd() == 95000, fmt.Sprintf("pending=%d", b.GetPendingVnd())
		})

		// Post-settle refund 95k on ord0 drains pending -> pending=0, available=190k
		if _, err := grpcClient.CreateRefund(payW2AuthCtx(ctx, adminTok), &paymentv1.CreateRefundRequest{
			PaymentId:      pay0,
			OrderId:        ord0,
			AmountVnd:      95000,
			Reason:         "post-settle clawback draining pending",
			IdempotencyKey: "r2-p24-claw-" + uuid.NewString(),
		}); err != nil {
			t.Fatalf("CreateRefund clawback on ord0: %v", err)
		}

		// ord2: 100k (seller net 95k, freshly completed -> inside 72h hold window!) => pending=95k, available=190k
		_, _ = createAndComplete("ord2", 100000, 5000)
		env.pollUntil(t, 8*time.Second, 150*time.Millisecond, "ord2 pending=95k", func() (bool, string) {
			b, err := grpcClient.GetSellerBalance(payW2AuthCtx(ctx, adminTok), &paymentv1.GetSellerBalanceRequest{ShopId: shopID})
			if err != nil {
				return false, err.Error()
			}
			return b.GetPendingVnd() == 95000, fmt.Sprintf("pending=%d", b.GetPendingVnd())
		})

		// Releasing ord1 MUST NOT steal ord2's held 95k from seller_pending!
		rel1, err := grpcClient.ReleaseEscrow(payW2AuthCtx(ctx, adminTok), &paymentv1.ReleaseEscrowRequest{OrderId: ord1, ShopId: shopID})
		if err != nil {
			t.Fatalf("ReleaseEscrow ord1: %v", err)
		}
		if rel1.GetReleasedAmountVnd() != 0 {
			t.Fatalf("P2-4: releasing ord1 after pending clawback must release 0 (protecting ord2's 72h hold), got %d", rel1.GetReleasedAmountVnd())
		}
		balAfter, err := grpcClient.GetSellerBalance(payW2AuthCtx(ctx, adminTok), &paymentv1.GetSellerBalanceRequest{ShopId: shopID})
		if err != nil {
			t.Fatalf("GetSellerBalance: %v", err)
		}
		if balAfter.GetPendingVnd() != 95000 || balAfter.GetAvailableVnd() != 190000 {
			t.Fatalf("P2-4: ord2's 95k must remain in pending_vnd=95000 and available_vnd=190000, got pending=%d available=%d",
				balAfter.GetPendingVnd(), balAfter.GetAvailableVnd())
		}
	})

	t.Run("P2_5_SettleRefundDifferentIdempotencyKeyReturns409", func(t *testing.T) {
		buyerID := "payw2-r2-p25-buyer-" + uuid.NewString()
		buyerTok := MintAccessToken(env.cfg.JWTSecret, buyerID, "buyer")
		ordID := "payw2-r2-p25-ord-" + uuid.NewString()
		pResp, err := grpcClient.CreatePayment(payW2AuthCtx(ctx, buyerTok), &paymentv1.CreatePaymentRequest{
			OrderGroupId:    ordID,
			OrderId:         ordID,
			UserId:          buyerID,
			AmountVnd:       110000,
			Method:          paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
			ShopAllocations: []*paymentv1.ShopAllocation{{OrderId: ordID, ShopId: "shop-r2-p25", SubtotalVnd: 110000}},
			IdempotencyKey:  "payw2-r2-p25-pay-" + uuid.NewString(),
		})
		if err != nil {
			t.Fatalf("CreatePayment: %v", err)
		}
		payID := pResp.GetPayment().GetPaymentId()
		payW2SendVietQRWebhook(t, env, payID, ordID, "PAYW2-R2-P25-"+uuid.NewString(), 110000)

		rfResp, err := grpcClient.CreateRefund(payW2AuthCtx(ctx, adminTok), &paymentv1.CreateRefundRequest{
			PaymentId:      payID,
			OrderId:        ordID,
			AmountVnd:      110000,
			Reason:         "P2-5 test",
			IdempotencyKey: "payw2-r2-p25-rf-" + uuid.NewString(),
		})
		if err != nil {
			t.Fatalf("CreateRefund: %v", err)
		}
		refundID := rfResp.GetRefund().GetRefundId()
		settlePath := fmt.Sprintf("/api/v1/admin/refunds/%s/settle", url.PathEscape(refundID))

		env.mustDoJSON(t, http.MethodPost, settlePath, adminTok, map[string]string{
			"Idempotency-Key": "r2-p25-k1-" + uuid.NewString(),
		}, map[string]string{
			"provider_refund_id": "FT-P25-111",
			"note":               "manual transfer 1",
		}, http.StatusOK)

		// Different Idempotency-Key + different provider_refund_id on already-settled refund MUST return 409 Conflict!
		env.mustDoJSON(t, http.MethodPost, settlePath, adminTok, map[string]string{
			"Idempotency-Key": "r2-p25-k2-" + uuid.NewString(),
		}, map[string]string{
			"provider_refund_id": "FT-P25-222",
			"note":               "accidental 2nd transfer",
		}, http.StatusConflict)
	})

	t.Run("P2_6_LegacyCODWithoutAllocations_OrderReturned", func(t *testing.T) {
		buyerID := "payw2-r2-p26-buyer-" + uuid.NewString()
		buyerTok := MintAccessToken(env.cfg.JWTSecret, buyerID, "buyer")

		// Case A: SHIPPING (failed delivery) on legacy COD (no shop_allocations) -> CANCELLED
		ordFail := "payw2-r2-p26-fail-" + uuid.NewString()
		pFail, err := grpcClient.CreatePayment(payW2AuthCtx(ctx, buyerTok), &paymentv1.CreatePaymentRequest{
			OrderGroupId:   ordFail,
			OrderId:        ordFail,
			UserId:         buyerID,
			AmountVnd:      140000,
			Method:         paymentv1.PaymentMethod_PAYMENT_METHOD_COD,
			IdempotencyKey: "payw2-r2-p26-fail-" + uuid.NewString(),
		})
		if err != nil {
			t.Fatalf("CreatePayment legacy COD fail: %v", err)
		}
		payW2PublishNATSEvent(t, "order.returned", map[string]any{
			"order_id":          ordFail,
			"order_code":        ordFail,
			"checkout_group_id": ordFail,
			"buyer_id":          buyerID,
			"shop_id":           "shop-r2-p26",
			"payment_method":    "COD",
			"from_status":       "SHIPPING",
			"total_payable_vnd": int64(140000),
			"reason":            "delivery_failed",
			"was_paid":          false,
		})
		env.pollUntil(t, 6*time.Second, 150*time.Millisecond, "P2-6 legacy COD SHIPPING -> CANCELLED", func() (bool, string) {
			g, gErr := grpcClient.GetPayment(payW2AuthCtx(ctx, adminTok), &paymentv1.GetPaymentRequest{PaymentId: pFail.GetPayment().GetPaymentId()})
			if gErr != nil {
				return false, gErr.Error()
			}
			return g.GetPayment().GetStatus() == paymentv1.PaymentStatus_PAYMENT_STATUS_CANCELLED, g.GetPayment().GetStatus().String()
		})

		// Case B: RETURN_REQUESTED on legacy COD (no shop_allocations) -> collects COD (SUCCEEDED) and CreateRefund succeeds
		ordRet := "payw2-r2-p26-ret-" + uuid.NewString()
		pRet, err := grpcClient.CreatePayment(payW2AuthCtx(ctx, buyerTok), &paymentv1.CreatePaymentRequest{
			OrderGroupId:   ordRet,
			OrderId:        ordRet,
			UserId:         buyerID,
			AmountVnd:      145000,
			Method:         paymentv1.PaymentMethod_PAYMENT_METHOD_COD,
			IdempotencyKey: "payw2-r2-p26-ret-" + uuid.NewString(),
		})
		if err != nil {
			t.Fatalf("CreatePayment legacy COD ret: %v", err)
		}
		payW2PublishNATSEvent(t, "order.returned", map[string]any{
			"order_id":          ordRet,
			"order_code":        ordRet,
			"checkout_group_id": ordRet,
			"buyer_id":          buyerID,
			"shop_id":           "shop-r2-p26",
			"payment_method":    "COD",
			"from_status":       "RETURN_REQUESTED",
			"total_payable_vnd": int64(145000),
			"reason":            "post_delivery_return",
			"was_paid":          true,
		})
		env.pollUntil(t, 6*time.Second, 150*time.Millisecond, "P2-6 legacy COD RETURN_REQUESTED -> SUCCEEDED", func() (bool, string) {
			g, gErr := grpcClient.GetPayment(payW2AuthCtx(ctx, adminTok), &paymentv1.GetPaymentRequest{PaymentId: pRet.GetPayment().GetPaymentId()})
			if gErr != nil {
				return false, gErr.Error()
			}
			return g.GetPayment().GetStatus() == paymentv1.PaymentStatus_PAYMENT_STATUS_SUCCEEDED, g.GetPayment().GetStatus().String()
		})
		if _, err := grpcClient.CreateRefund(payW2AuthCtx(ctx, adminTok), &paymentv1.CreateRefundRequest{
			PaymentId:      pRet.GetPayment().GetPaymentId(),
			OrderId:        ordRet,
			AmountVnd:      145000,
			Reason:         "P2-6 legacy COD return refund",
			IdempotencyKey: "payw2-r2-p26-rf-" + uuid.NewString(),
		}); err != nil {
			t.Fatalf("P2-6 CreateRefund on legacy COD after RETURN_REQUESTED failed: %v", err)
		}
		payW2AssertLedgerBalanced(t, env)
	})
}

func payW2ExtractOrderCodeFromQRURL(t *testing.T, qrURL string) string {
	t.Helper()
	parsed, err := url.Parse(qrURL)
	if err != nil {
		t.Fatalf("parse qr_url %q: %v", qrURL, err)
	}
	code := strings.TrimSpace(parsed.Query().Get("addInfo"))
	if code == "" {
		t.Fatalf("qr_url %q missing addInfo provider_order_code", qrURL)
	}
	return code
}

func payW2ListAdminRefundsByOrder(t *testing.T, env *testEnv, adminTok, orderID string) []payW2RefundDTO {
	t.Helper()
	path := fmt.Sprintf("/api/v1/admin/refunds?order_id=%s&page=1&page_size=50", url.QueryEscape(orderID))
	resp := env.mustDoJSON(t, http.MethodGet, path, adminTok, nil, nil, http.StatusOK)
	var out payW2RefundListResp
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode admin refunds for %s: %v", orderID, err)
	}
	return out.Items
}

// TestE2E_PaymentOps_Review3EdgeCases covers review-round-2 payment fixes (1)-(5) strictly via public/admin/gRPC APIs:
// (1) provider_tx_id whitespace trimming + blank 400 + single gateway_clearing debit across webhook/replay/reconcile
// (2) FAILED webhook does not occupy payments.provider_tx_id; subsequent SUCCEEDED creates 1 refund & 1x ledger entry
// (3) CancelPayment idempotent OK on expired/failed/superseded; 409 (AlreadyExists) on succeeded
// (4) Pending reconciliation_queue items auto-close (assigned/refunded, notes contains auto_closed) when accounted via another path
// (5) transactionDate alias for occurred_at avoids false missing_provider across VN midnight
func TestE2E_PaymentOps_Review3EdgeCases(t *testing.T) {
	env := getEnv()
	ctx := env.ctx(t)
	adminTok := env.adminLogin(t)
	grpcClient := payW2DialGRPC(t, env)
	vnLoc := time.FixedZone("Asia/Ho_Chi_Minh", 7*3600)

	createVietQR := func(t *testing.T, tag string, amt int64, expiresAt *timestamppb.Timestamp) (*paymentv1.Payment, string, string) {
		t.Helper()
		buyerID := "payw2-r3-" + tag + "-buyer-" + uuid.NewString()
		buyerTok := MintAccessToken(env.cfg.JWTSecret, buyerID, "buyer")
		grpID := "payw2-r3-" + tag + "-grp-" + uuid.NewString()
		resp, err := grpcClient.CreatePayment(payW2AuthCtx(ctx, buyerTok), &paymentv1.CreatePaymentRequest{
			OrderGroupId:    grpID,
			OrderId:         grpID,
			UserId:          buyerID,
			AmountVnd:       amt,
			Method:          paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
			ShopAllocations: []*paymentv1.ShopAllocation{{OrderId: grpID, ShopId: "shop-r3-" + tag, SubtotalVnd: amt}},
			ExpiresAt:       expiresAt,
			IdempotencyKey:  "payw2-r3-" + tag + "-" + uuid.NewString(),
		})
		if err != nil {
			t.Fatalf("CreatePayment %s: %v", tag, err)
		}
		return resp.GetPayment(), grpID, buyerTok
	}

	// (1) provider_tx_id whitespace: padded webhook -> trimmed replay -> reconcile: 0 late refund, 1x gateway_clearing debit; blank -> 400.
	t.Run("1_ProviderTxIDWhitespaceTrimAndBlank400", func(t *testing.T) {
		const amt int64 = 152000
		p, grpID, _ := createVietQR(t, "trim", amt, nil)

		// Whitespace-only provider_tx_id -> 400 BadRequest.
		blankResp, err := env.client.PostSignedWebhook(ctx, "/webhooks/vietqr", PaymentWebhookPayload{
			Provider:     "vietqr_mock",
			ProviderTxID: "   \t  ",
			PaymentID:    p.GetPaymentId(),
			OrderGroupID: grpID,
			AmountVND:    amt,
			Status:       "SUCCEEDED",
			Reference:    "BLANK " + grpID,
			OccurredAt:   time.Now().UTC().Format(time.RFC3339),
		}, env.cfg.PaymentWebhookSecret, time.Now().UTC(), "")
		if err != nil {
			t.Fatalf("post blank provider_tx_id webhook: %v", err)
		}
		if blankResp.StatusCode != http.StatusBadRequest {
			t.Fatalf("blank provider_tx_id: want HTTP 400, got %d body=%s", blankResp.StatusCode, string(blankResp.Body))
		}

		sumBefore := payW2AssertLedgerBalanced(t, env)
		gwBefore, _ := payW2FindAccount(sumBefore, "gateway_clearing")
		rpBefore, _ := payW2FindAccount(sumBefore, "refund_payable")

		rawTx := "PAYW2-R3-TRIM-" + uuid.NewString()
		whPadded := payW2SendVietQRWebhook(t, env, p.GetPaymentId(), grpID, "  "+rawTx+"  ", amt)
		if whPadded.Status != "processed" || whPadded.ProviderTxID != rawTx {
			t.Fatalf("padded webhook: want status=processed provider_tx_id=%q, got %+v", rawTx, whPadded)
		}

		// Re-sending the trimmed provider_tx_id must be ignored as duplicate (no late-webhook refund).
		whTrimmed := payW2SendVietQRWebhook(t, env, p.GetPaymentId(), grpID, rawTx, amt)
		if !whTrimmed.Duplicate || whTrimmed.Status != "duplicate_ignored" {
			t.Fatalf("trimmed replay: want duplicate_ignored, got %+v", whTrimmed)
		}

		todayVN := time.Now().In(vnLoc).Format("2006-01-02")
		recResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/payments/reconcile", adminTok, nil, map[string]string{
			"date": todayVN,
		}, http.StatusOK)
		var recOut payW2ReconcileResp
		if err := DecodeJSON(recResp, &recOut); err != nil {
			t.Fatalf("decode reconcile: %v", err)
		}
		matched := false
		for _, it := range recOut.Items {
			if it.ProviderTxID == rawTx {
				if !StatusEquals(it.Status, "MATCHED") {
					t.Fatalf("trimmed tx %s reconcile status = %q, want MATCHED", rawTx, it.Status)
				}
				matched = true
			}
		}
		if !matched {
			t.Fatalf("trimmed tx %s missing from reconciliation items for %s", rawTx, todayVN)
		}

		if refunds := payW2ListAdminRefundsByOrder(t, env, adminTok, grpID); len(refunds) != 0 {
			t.Fatalf("expected 0 late-webhook refunds for trimmed replay + reconcile, got %+v", refunds)
		}

		sumAfter := payW2AssertLedgerBalanced(t, env)
		gwAfter, _ := payW2FindAccount(sumAfter, "gateway_clearing")
		rpAfter, _ := payW2FindAccount(sumAfter, "refund_payable")
		if gwAfter.DebitTotalVND-gwBefore.DebitTotalVND != amt {
			t.Fatalf("gateway_clearing debit delta = %d, want 1x (%d)", gwAfter.DebitTotalVND-gwBefore.DebitTotalVND, amt)
		}
		if rpAfter.BalanceVND != rpBefore.BalanceVND {
			t.Fatalf("refund_payable must not increase (before=%d after=%d)", rpBefore.BalanceVND, rpAfter.BalanceVND)
		}
	})

	// (2) FAILED webhook does not occupy payments.provider_tx_id; subsequent SUCCEEDED books refund & ledger 1x.
	t.Run("2_FailedWebhookDoesNotOccupyProviderTxID", func(t *testing.T) {
		const amt int64 = 165000
		p, grpID, _ := createVietQR(t, "fail", amt, nil)

		sumBefore := payW2AssertLedgerBalanced(t, env)
		gwBefore, _ := payW2FindAccount(sumBefore, "gateway_clearing")
		rpBefore, _ := payW2FindAccount(sumBefore, "refund_payable")

		txFail := "PAYW2-R3-FAIL-" + uuid.NewString()
		whFailResp, err := env.client.PostSignedWebhook(ctx, "/webhooks/vietqr", PaymentWebhookPayload{
			Provider:     "vietqr_mock",
			ProviderTxID: txFail,
			PaymentID:    p.GetPaymentId(),
			OrderGroupID: grpID,
			AmountVND:    amt,
			Status:       "FAILED",
			Reference:    "FAIL " + grpID,
			OccurredAt:   time.Now().UTC().Format(time.RFC3339),
		}, env.cfg.PaymentWebhookSecret, time.Now().UTC(), "")
		if err != nil || whFailResp.StatusCode != http.StatusOK {
			t.Fatalf("FAILED webhook failed: err=%v status=%v body=%s", err, whFailResp.StatusCode, string(whFailResp.Body))
		}
		var failOut PaymentWebhookResult
		if err := DecodeJSON(whFailResp, &failOut); err != nil {
			t.Fatalf("decode FAILED webhook: %v", err)
		}
		if failOut.Status != "payment_marked_failed" {
			t.Fatalf("FAILED webhook status = %q, want payment_marked_failed", failOut.Status)
		}

		gAfterFail, err := grpcClient.GetPayment(payW2AuthCtx(ctx, adminTok), &paymentv1.GetPaymentRequest{PaymentId: p.GetPaymentId()})
		if err != nil {
			t.Fatalf("GetPayment after FAILED: %v", err)
		}
		if gAfterFail.GetPayment().GetStatus() != paymentv1.PaymentStatus_PAYMENT_STATUS_FAILED {
			t.Fatalf("payment status after FAILED = %v, want FAILED", gAfterFail.GetPayment().GetStatus())
		}
		// Verify via SSE initial snapshot that FAILED webhook did NOT occupy payments.provider_tx_id.
		sseCtx, sseCancel := context.WithTimeout(ctx, 15*time.Second)
		sseResp, sseErr := openSSERequest(sseCtx, fmt.Sprintf("%s/api/v1/payments/%s/events", env.cfg.GatewayURL, url.PathEscape(p.GetPaymentId())), adminTok)
		if sseErr != nil {
			sseCancel()
			t.Fatalf("open payment SSE after FAILED: %v", sseErr)
		}
		frame, frameErr := readFirstSSEEvent(sseResp.Body)
		sseCancel()
		_ = sseResp.Body.Close()
		if frameErr != nil {
			t.Fatalf("read payment SSE event after FAILED: %v", frameErr)
		}
		var ssePayload struct {
			Status       string  `json:"status"`
			ProviderTxID *string `json:"provider_tx_id"`
		}
		if err := json.Unmarshal([]byte(frame.Data), &ssePayload); err != nil {
			t.Fatalf("decode SSE payload %q: %v", frame.Data, err)
		}
		if ssePayload.ProviderTxID != nil && strings.TrimSpace(*ssePayload.ProviderTxID) != "" {
			t.Fatalf("FAILED webhook must NOT occupy payments.provider_tx_id, got %q", *ssePayload.ProviderTxID)
		}

		// Subsequent SUCCEEDED transfer for the failed payment creates 1 late refund and posts 1x ledger entry.
		txSucc := "PAYW2-R3-SUCC-" + uuid.NewString()
		whSucc := payW2SendVietQRWebhook(t, env, p.GetPaymentId(), grpID, txSucc, amt)
		if whSucc.Status != "late_payment_refund_queued" {
			t.Fatalf("SUCCEEDED webhook after FAILED: want late_payment_refund_queued, got %+v", whSucc)
		}

		// Reconcile today VN must classify txSucc as MATCHED without double-booking or flagging txFail.
		todayVN := time.Now().In(vnLoc).Format("2006-01-02")
		recResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/payments/reconcile", adminTok, nil, map[string]string{
			"date": todayVN,
		}, http.StatusOK)
		var recOut payW2ReconcileResp
		if err := DecodeJSON(recResp, &recOut); err != nil {
			t.Fatalf("decode reconcile: %v", err)
		}
		matchedSucc := false
		for _, it := range recOut.Items {
			if it.ProviderTxID == txFail {
				t.Fatalf("FAILED webhook %s must not appear in reconcile feed: %+v", txFail, it)
			}
			if it.ProviderTxID == txSucc && StatusEquals(it.Status, "MATCHED") {
				matchedSucc = true
			}
		}
		if !matchedSucc {
			t.Fatalf("expected txSucc %s MATCHED in reconciliation items", txSucc)
		}

		refunds := payW2ListAdminRefundsByOrder(t, env, adminTok, grpID)
		if len(refunds) != 1 || refunds[0].AmountVND != amt || refunds[0].IdempotencyKey != "late-webhook:"+txSucc || !StatusEquals(refunds[0].Status, "PENDING") {
			t.Fatalf("expected exactly 1 pending late-webhook refund for %s (%d VND), got %+v", txSucc, amt, refunds)
		}

		sumAfter := payW2AssertLedgerBalanced(t, env)
		gwAfter, _ := payW2FindAccount(sumAfter, "gateway_clearing")
		rpAfter, _ := payW2FindAccount(sumAfter, "refund_payable")
		if gwAfter.DebitTotalVND-gwBefore.DebitTotalVND != amt {
			t.Fatalf("gateway_clearing debit delta = %d, want 1x (%d)", gwAfter.DebitTotalVND-gwBefore.DebitTotalVND, amt)
		}
		if rpAfter.BalanceVND-rpBefore.BalanceVND != amt {
			t.Fatalf("refund_payable delta = %d, want 1x (%d)", rpAfter.BalanceVND-rpBefore.BalanceVND, amt)
		}
	})

	// (3) CancelPayment on expired / failed / superseded -> OK; on succeeded -> 409 (AlreadyExists).
	t.Run("3_CancelPaymentTerminalStatesOK_Succeeded409", func(t *testing.T) {
		// Create already-past-expiry payment first so the 15s background expiration scanner picks it up while we run the other checks.
		pExp, _, _ := createVietQR(t, "can-exp", 110000, timestamppb.New(time.Now().Add(-2*time.Minute)))

		// Failed payment -> CancelPayment returns OK.
		pFail, grpFail, _ := createVietQR(t, "can-fail", 115000, nil)
		whFail, err := env.client.PostSignedWebhook(ctx, "/webhooks/vietqr", PaymentWebhookPayload{
			Provider:     "vietqr_mock",
			ProviderTxID: "PAYW2-R3-CANFAIL-" + uuid.NewString(),
			PaymentID:    pFail.GetPaymentId(),
			OrderGroupID: grpFail,
			AmountVND:    115000,
			Status:       "FAILED",
			Reference:    "CANFAIL " + grpFail,
			OccurredAt:   time.Now().UTC().Format(time.RFC3339),
		}, env.cfg.PaymentWebhookSecret, time.Now().UTC(), "")
		if err != nil || whFail.StatusCode != http.StatusOK {
			t.Fatalf("mark failed webhook: err=%v status=%v", err, whFail.StatusCode)
		}
		canFailResp, err := grpcClient.CancelPayment(payW2AuthCtx(ctx, adminTok), &paymentv1.CancelPaymentRequest{
			PaymentId: pFail.GetPaymentId(),
			Reason:    "idempotent cancel on failed",
		})
		if err != nil || canFailResp.GetPayment().GetStatus() != paymentv1.PaymentStatus_PAYMENT_STATUS_FAILED {
			t.Fatalf("CancelPayment on failed: want OK/FAILED, got err=%v resp=%+v", err, canFailResp)
		}

		// Superseded payment -> CancelPayment returns OK (preserve original deadline per payment p4 Item K).
		pSup1, grpSup, supBuyerTok := createVietQR(t, "can-sup", 120000, nil)
		if _, err := grpcClient.CreatePayment(payW2AuthCtx(ctx, supBuyerTok), &paymentv1.CreatePaymentRequest{
			OrderGroupId:      grpSup,
			OrderId:           grpSup,
			UserId:            pSup1.GetUserId(),
			AmountVnd:         120000,
			Method:            paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
			ShopAllocations:   []*paymentv1.ShopAllocation{{OrderId: grpSup, ShopId: "shop-r3-can-sup", SubtotalVnd: 120000}},
			ExpiresAt:         pSup1.GetExpiresAt(),
			SupersedePrevious: true,
			IdempotencyKey:    "payw2-r3-can-sup2-" + uuid.NewString(),
		}); err != nil {
			t.Fatalf("CreatePayment supersede: %v", err)
		}
		canSupResp, err := grpcClient.CancelPayment(payW2AuthCtx(ctx, adminTok), &paymentv1.CancelPaymentRequest{
			PaymentId: pSup1.GetPaymentId(),
			Reason:    "idempotent cancel on superseded",
		})
		if err != nil || canSupResp.GetPayment().GetStatus() != paymentv1.PaymentStatus_PAYMENT_STATUS_CANCELLED {
			t.Fatalf("CancelPayment on superseded: want OK/CANCELLED, got err=%v resp=%+v", err, canSupResp)
		}

		// Succeeded payment -> CancelPayment returns 409 (codes.AlreadyExists).
		pSucc, grpSucc, _ := createVietQR(t, "can-succ", 125000, nil)
		payW2SendVietQRWebhook(t, env, pSucc.GetPaymentId(), grpSucc, "PAYW2-R3-CANSUCC-"+uuid.NewString(), 125000)
		if _, err := grpcClient.CancelPayment(payW2AuthCtx(ctx, adminTok), &paymentv1.CancelPaymentRequest{
			PaymentId: pSucc.GetPaymentId(),
			Reason:    "cannot cancel succeeded",
		}); status.Code(err) != codes.AlreadyExists {
			t.Fatalf("CancelPayment on succeeded: want codes.AlreadyExists (409), got %v", err)
		}

		// Expired payment -> wait for expiration scanner to mark EXPIRED, then CancelPayment returns OK.
		env.pollUntil(t, 25*time.Second, 500*time.Millisecond, "pExp marked EXPIRED by scanner", func() (bool, string) {
			g, gErr := grpcClient.GetPayment(payW2AuthCtx(ctx, adminTok), &paymentv1.GetPaymentRequest{PaymentId: pExp.GetPaymentId()})
			if gErr != nil {
				return false, gErr.Error()
			}
			return g.GetPayment().GetStatus() == paymentv1.PaymentStatus_PAYMENT_STATUS_EXPIRED, g.GetPayment().GetStatus().String()
		})
		canExpResp, err := grpcClient.CancelPayment(payW2AuthCtx(ctx, adminTok), &paymentv1.CancelPaymentRequest{
			PaymentId: pExp.GetPaymentId(),
			Reason:    "idempotent cancel on expired",
		})
		if err != nil || canExpResp.GetPayment().GetStatus() != paymentv1.PaymentStatus_PAYMENT_STATUS_EXPIRED {
			t.Fatalf("CancelPayment on expired: want OK/EXPIRED, got err=%v resp=%+v", err, canExpResp)
		}
	})

	// (4) Pending reconciliation_queue item auto-closes (assigned/refunded + notes contains auto_closed)
	// when the same provider_tx_id is accounted via reconciliation auto-heal / late-refund.
	t.Run("4_PendingQueueAutoClosesWhenAccountedViaAnotherPath", func(t *testing.T) {
		todayVN := time.Now().In(vnLoc).Format("2006-01-02")

		// Path A: pending queue item -> reconciliation auto-heals pending payment -> queue auto-closes as assigned + auto_closed.
		const amtA int64 = 141000
		pA, _, _ := createVietQR(t, "ac-assign", amtA, nil)
		codeA := payW2ExtractOrderCodeFromQRURL(t, pA.GetQrUrl())
		txA := "PAYW2-R3-ACA-" + uuid.NewString()
		whA, err := env.client.PostSignedWebhook(ctx, "/webhooks/vietqr", map[string]any{
			"provider":       "vietqr_mock",
			"provider_tx_id": txA,
			"order_ref":      codeA,
			"amount_vnd":     amtA,
			"status":         "SUCCEEDED",
			"occurred_at":    time.Now().UTC().Format(time.RFC3339),
		}, env.cfg.PaymentWebhookSecret, time.Now().UTC(), "")
		if err != nil || whA.StatusCode != http.StatusOK {
			t.Fatalf("post Path A webhook: err=%v status=%v body=%s", err, whA.StatusCode, string(whA.Body))
		}
		qABefore := payW2FindQueueItemByTx(t, env, adminTok, txA)
		if qABefore.ResolutionStatus != "pending" {
			t.Fatalf("Path A expected initial pending queue item, got %+v", qABefore)
		}

		// Path B: pending queue item -> payment cancelled -> reconciliation books late refund -> queue auto-closes as refunded + auto_closed.
		const amtB int64 = 146000
		pB, _, _ := createVietQR(t, "ac-refund", amtB, nil)
		codeB := payW2ExtractOrderCodeFromQRURL(t, pB.GetQrUrl())
		txB := "PAYW2-R3-ACR-" + uuid.NewString()
		whB, err := env.client.PostSignedWebhook(ctx, "/webhooks/vietqr", map[string]any{
			"provider":       "vietqr_mock",
			"provider_tx_id": txB,
			"order_ref":      codeB,
			"amount_vnd":     amtB,
			"status":         "SUCCEEDED",
			"occurred_at":    time.Now().UTC().Format(time.RFC3339),
		}, env.cfg.PaymentWebhookSecret, time.Now().UTC(), "")
		if err != nil || whB.StatusCode != http.StatusOK {
			t.Fatalf("post Path B webhook: err=%v status=%v body=%s", err, whB.StatusCode, string(whB.Body))
		}
		qBBefore := payW2FindQueueItemByTx(t, env, adminTok, txB)
		if qBBefore.ResolutionStatus != "pending" {
			t.Fatalf("Path B expected initial pending queue item, got %+v", qBBefore)
		}
		if _, err := grpcClient.CancelPayment(payW2AuthCtx(ctx, adminTok), &paymentv1.CancelPaymentRequest{
			PaymentId: pB.GetPaymentId(),
			Reason:    "cancel before reconcile late-refund auto-close",
		}); err != nil {
			t.Fatalf("CancelPayment pB: %v", err)
		}

		// Run reconciliation -> accounts both txA (auto-heal -> assigned) and txB (late-refund -> refunded).
		env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/payments/reconcile", adminTok, nil, map[string]string{
			"date": todayVN,
		}, http.StatusOK)

		qAAfter := payW2FindQueueItemByTx(t, env, adminTok, txA)
		if qAAfter.ResolutionStatus != "assigned" || !strings.Contains(qAAfter.Notes, "auto_closed") {
			t.Fatalf("Path A: expected auto-closed assigned queue item with auto_closed note, got status=%q notes=%q",
				qAAfter.ResolutionStatus, qAAfter.Notes)
		}
		gA, err := grpcClient.GetPayment(payW2AuthCtx(ctx, adminTok), &paymentv1.GetPaymentRequest{PaymentId: pA.GetPaymentId()})
		if err != nil || gA.GetPayment().GetStatus() != paymentv1.PaymentStatus_PAYMENT_STATUS_SUCCEEDED {
			t.Fatalf("Path A: expected pA SUCCEEDED after auto-heal, got err=%v status=%v", err, gA.GetPayment().GetStatus())
		}

		qBAfter := payW2FindQueueItemByTx(t, env, adminTok, txB)
		if qBAfter.ResolutionStatus != "refunded" || !strings.Contains(qBAfter.Notes, "auto_closed") {
			t.Fatalf("Path B: expected auto-closed refunded queue item with auto_closed note, got status=%q notes=%q",
				qBAfter.ResolutionStatus, qBAfter.Notes)
		}
		payW2AssertLedgerBalanced(t, env)
	})

	// (5) transactionDate alias for occurred_at: no false missing_provider across VN midnight.
	t.Run("5_TransactionDateAliasCrossMidnight_NoFalseMissingProvider", func(t *testing.T) {
		const amt int64 = 178000
		p, grpID, _ := createVietQR(t, "txdate", amt, nil)

		yesterdayVN := time.Now().In(vnLoc).AddDate(0, 0, -1)
		yesterdayStr := yesterdayVN.Format("2006-01-02")
		todayStr := time.Now().In(vnLoc).Format("2006-01-02")
		txID := "PAYW2-R3-TXDATE-" + uuid.NewString()

		// Send webhook using SePay alias "transactionDate" (VN local wall time 23:55:00) without "occurred_at".
		whResp, err := env.client.PostSignedWebhook(ctx, "/webhooks/vietqr", map[string]any{
			"provider":        "vietqr_mock",
			"provider_tx_id":  txID,
			"payment_id":      p.GetPaymentId(),
			"order_group_id":  grpID,
			"amount_vnd":      amt,
			"status":          "SUCCEEDED",
			"reference":       "TXDATE " + grpID,
			"transactionDate": yesterdayStr + " 23:55:00",
		}, env.cfg.PaymentWebhookSecret, time.Now().UTC(), "")
		if err != nil || whResp.StatusCode != http.StatusOK {
			t.Fatalf("post transactionDate webhook: err=%v status=%v body=%s", err, whResp.StatusCode, string(whResp.Body))
		}

		todayResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/payments/reconcile", adminTok, nil, map[string]string{
			"date": todayStr,
		}, http.StatusOK)
		var todayOut payW2ReconcileResp
		if err := DecodeJSON(todayResp, &todayOut); err != nil {
			t.Fatalf("decode today reconcile: %v", err)
		}
		for _, it := range todayOut.Items {
			if it.PaymentID == p.GetPaymentId() && StatusEquals(it.Status, "MISSING_PROVIDER") {
				t.Fatalf("transactionDate payment %s falsely flagged MISSING_PROVIDER on today %s", p.GetPaymentId(), todayStr)
			}
		}

		yestResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/payments/reconcile", adminTok, nil, map[string]string{
			"date": yesterdayStr,
		}, http.StatusOK)
		var yestOut payW2ReconcileResp
		if err := DecodeJSON(yestResp, &yestOut); err != nil {
			t.Fatalf("decode yesterday reconcile: %v", err)
		}
		matchedYest := false
		for _, it := range yestOut.Items {
			if it.ProviderTxID == txID && StatusEquals(it.Status, "MATCHED") && it.PaymentID == p.GetPaymentId() {
				matchedYest = true
			}
		}
		if !matchedYest {
			t.Fatalf("expected transactionDate tx %s MATCHED on %s", txID, yesterdayStr)
		}
	})
}
