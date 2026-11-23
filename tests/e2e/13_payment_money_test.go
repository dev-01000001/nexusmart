package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func payFixDialGRPC(t *testing.T, env *testEnv) paymentv1.PaymentServiceClient {
	t.Helper()
	conn, err := grpc.NewClient(env.cfg.PaymentGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial PaymentService gRPC %s: %v", env.cfg.PaymentGRPCAddr, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return paymentv1.NewPaymentServiceClient(conn)
}

func payFixAuthCtx(ctx context.Context, token string) context.Context {
	return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+token))
}

func payFixGetSellerBalance(t *testing.T, env *testEnv, seller *sellerFixture) SellerBalanceResponse {
	t.Helper()
	resp := env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/balance", seller.AccessToken, nil, nil, http.StatusOK)
	var out SellerBalanceResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode seller balance for %s: %v", seller.ShopID, err)
	}
	return out
}

func payFixFindAccount(summary LedgerSummaryResponse, code string) (AccountBalanceDTO, bool) {
	for _, acc := range summary.Accounts {
		if acc.AccountCode == code {
			return acc, true
		}
	}
	return AccountBalanceDTO{}, false
}

func payFixShipAndDeliver(
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
			EventID:      "payfix-ship-" + uuid.NewString(),
			TrackingCode: shipOut.TrackingCode,
			OrderID:      orderID,
			RawStatus:    step.rawStatus,
			Description:  step.desc,
			OccurredAt:   step.at.Format(time.RFC3339),
		}, env.cfg.ShippingWebhookSecret, time.Now().UTC(), "")
		if err != nil {
			t.Fatalf("shipping webhook %s (%s) failed: %v", orderID, step.rawStatus, err)
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

// TestE2E_PaymentMoney_PAY_B4_CODLifecycle reproduces PAY-B4:
// - COD starts PENDING (awaiting collection), NOT SUCCEEDED, and does not book cod_receivable at checkout.
// - Refund on uncollected or cancelled COD MUST be rejected with HTTP 409.
// - Cancelling COD transitions payment to CANCELLED with no dangling ledger/escrow.
// - Delivering COD transitions payment to SUCCEEDED and books cod_receivable -> escrow_liability.
func TestE2E_PaymentMoney_PAY_B4_CODLifecycle(t *testing.T) {
	env := getEnv()
	adminTok := env.adminLogin(t)

	cat := env.ensureCategory(t)
	seller := env.createApprovedSeller(t, "payfix-cod-seller")
	_, sku := env.createApprovedProduct(t, seller, cat.ID, "COD Lifecycle Shirt", 230000, 30, 500)

	buyer := env.registerVerifyAndLogin(t, "payfix-cod-buyer")
	addr := env.addAddress(t, buyer)
	quote := env.calculateShippingQuote(t, buyer.AccessToken, seller.ShopID, 500, 230000)

	// 1. Place COD order #1 and verify it starts PENDING (not SUCCEEDED) and cod_receivable does NOT increase (P3-10).
	codRecBefore1, _ := payFixFindAccount(env.getLedgerSummary(t), "cod_receivable")
	cod1Resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
		"Idempotency-Key": "payfix-cod-cancel-" + uuid.NewString(),
	}, CheckoutRequest{
		AddressID:     addr.ID,
		FromCart:      false,
		Items:         []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 1}},
		PaymentMethod: "COD",
		ShippingChoices: []ShopShippingChoiceDTO{
			{ShopID: seller.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
		},
	}, http.StatusCreated, http.StatusOK)

	var cod1 CheckoutResponse
	if err := DecodeJSON(cod1Resp, &cod1); err != nil {
		t.Fatalf("decode COD checkout #1: %v", err)
	}
	if len(cod1.Orders) != 1 || cod1.PaymentID == "" {
		t.Fatalf("unexpected COD checkout #1 response: %s", string(cod1Resp.Body))
	}
	ord1 := cod1.Orders[0]

	pay1 := env.getPayment(t, buyer.AccessToken, cod1.PaymentID)
	if !StatusEquals(pay1.Status, "PENDING") {
		t.Fatalf("PAY-B4: newly placed COD payment %s must start PENDING, got %q", cod1.PaymentID, pay1.Status)
	}
	codRecAfter1, _ := payFixFindAccount(env.getLedgerSummary(t), "cod_receivable")
	if codRecAfter1.DebitTotalVND != codRecBefore1.DebitTotalVND {
		t.Fatalf("PAY-B4 / P3-10: cod_receivable must NOT increase at COD checkout: before=%d after=%d",
			codRecBefore1.DebitTotalVND, codRecAfter1.DebitTotalVND)
	}

	// 2. Refund on uncollected COD MUST be rejected with 409 Conflict.
	refundPath1 := fmt.Sprintf("/api/v1/admin/payments/%s/refund", url.PathEscape(cod1.PaymentID))
	env.mustDoJSON(t, http.MethodPost, refundPath1, adminTok, map[string]string{
		"Idempotency-Key": "payfix-cod-rf-pending-" + uuid.NewString(),
	}, RefundRequest{
		OrderID:   ord1.ID,
		AmountVND: 1000,
		Reason:    "PAY-B4 test refund on pending COD",
	}, http.StatusConflict)

	// 3. Cancel COD order #1 -> payment transitions to CANCELLED; cod_receivable stays unchanged; refund rejected 409.
	cancelPath := fmt.Sprintf("/api/v1/orders/%s/cancel", url.PathEscape(ord1.ID))
	env.mustDoJSON(t, http.MethodPost, cancelPath, buyer.AccessToken, nil, map[string]string{
		"reason": "PAY-B4 cancel before delivery",
	}, http.StatusOK)

	env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "cancelled COD payment status CANCELLED", func() (bool, string) {
		p := env.getPayment(t, buyer.AccessToken, cod1.PaymentID)
		return StatusEquals(p.Status, "CANCELLED"), fmt.Sprintf("status=%s", p.Status)
	})
	codRecAfterCancel1, _ := payFixFindAccount(env.getLedgerSummary(t), "cod_receivable")
	if codRecAfterCancel1.DebitTotalVND != codRecBefore1.DebitTotalVND {
		t.Fatalf("PAY-B4 / P3-10: cancelled COD must leave cod_receivable unchanged: before=%d after=%d",
			codRecBefore1.DebitTotalVND, codRecAfterCancel1.DebitTotalVND)
	}

	env.mustDoJSON(t, http.MethodPost, refundPath1, adminTok, map[string]string{
		"Idempotency-Key": "payfix-cod-rf-cancelled-" + uuid.NewString(),
	}, RefundRequest{
		OrderID:   ord1.ID,
		AmountVND: 1000,
		Reason:    "PAY-B4 test refund on cancelled COD",
	}, http.StatusConflict)

	// 4. Place COD order #2 -> deliver -> payment becomes SUCCEEDED and cod_receivable increases by P -> confirm-received -> seller credited.
	cod2Resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
		"Idempotency-Key": "payfix-cod-deliver-" + uuid.NewString(),
	}, CheckoutRequest{
		AddressID:     addr.ID,
		FromCart:      false,
		Items:         []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 1}},
		PaymentMethod: "COD",
		ShippingChoices: []ShopShippingChoiceDTO{
			{ShopID: seller.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
		},
	}, http.StatusCreated, http.StatusOK)

	var cod2 CheckoutResponse
	if err := DecodeJSON(cod2Resp, &cod2); err != nil {
		t.Fatalf("decode COD checkout #2: %v", err)
	}
	ord2 := cod2.Orders[0]

	pay2Before := env.getPayment(t, buyer.AccessToken, cod2.PaymentID)
	if !StatusEquals(pay2Before.Status, "PENDING") {
		t.Fatalf("expected COD payment #2 PENDING before delivery, got %q", pay2Before.Status)
	}

	codRecBeforeDeliver, _ := payFixFindAccount(env.getLedgerSummary(t), "cod_receivable")
	payFixShipAndDeliver(t, env, seller, buyer, ord2.ID, quote)

	env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "COD payment #2 SUCCEEDED after order.delivered", func() (bool, string) {
		p := env.getPayment(t, buyer.AccessToken, cod2.PaymentID)
		return StatusEquals(p.Status, "SUCCEEDED"), fmt.Sprintf("status=%s", p.Status)
	})

	codRecAfterDeliver, _ := payFixFindAccount(env.getLedgerSummary(t), "cod_receivable")
	codRecDelta := codRecAfterDeliver.DebitTotalVND - codRecBeforeDeliver.DebitTotalVND
	if codRecDelta != ord2.TotalPayableVND {
		t.Fatalf("PAY-B4 / P3-10: expected cod_receivable debit delta=%d after delivery, got %d (before=%d after=%d)",
			ord2.TotalPayableVND, codRecDelta, codRecBeforeDeliver.DebitTotalVND, codRecAfterDeliver.DebitTotalVND)
	}

	confirmRecPath := fmt.Sprintf("/api/v1/orders/%s/confirm-received", url.PathEscape(ord2.ID))
	env.mustDoJSON(t, http.MethodPost, confirmRecPath, buyer.AccessToken, nil, nil, http.StatusOK)

	wantSellerNet := ord2.SubtotalVND - ord2.CommissionVND
	env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "COD seller balance credited after completion", func() (bool, string) {
		bal := payFixGetSellerBalance(t, env, seller)
		return bal.AvailableVND == wantSellerNet, fmt.Sprintf("got=%+v want_available=%d", bal, wantSellerNet)
	})

	summary := env.getLedgerSummary(t)
	if !summary.IsBalanced || summary.TotalDebitsVND != summary.TotalCreditsVND {
		t.Fatalf("ledger imbalanced after COD lifecycle: %+v", summary)
	}
}

// TestE2E_PaymentMoney_PAY_B2_RefundAndEscrowInvariant reproduces PAY-B2:
//   - Case A: Partial refund before completion deducts from settlement so seller receives net - refund
//     and escrow_liability is never overdrawn.
//   - Case B: Post-settlement refund debits seller_pending / seller_available (NOT escrow_liability),
//     and rejects with 409 when seller balance is insufficient.
func TestE2E_PaymentMoney_PAY_B2_RefundAndEscrowInvariant(t *testing.T) {
	env := getEnv()
	ctx := env.ctx(t)
	adminTok := env.adminLogin(t)

	cat := env.ensureCategory(t)
	seller1 := env.createApprovedSeller(t, "payfix-b2-shop1")
	seller2 := env.createApprovedSeller(t, "payfix-b2-shop2")

	_, sku1 := env.createApprovedProduct(t, seller1, cat.ID, "PAY-B2 Shop1 Item", 229000, 20, 500)
	_, sku2 := env.createApprovedProduct(t, seller2, cat.ID, "PAY-B2 Shop2 Item", 429000, 20, 600)

	buyer := env.registerVerifyAndLogin(t, "payfix-b2-buyer")
	addr := env.addAddress(t, buyer)

	quote1 := env.calculateShippingQuote(t, buyer.AccessToken, seller1.ShopID, 500, 0)
	quote2 := env.calculateShippingQuote(t, buyer.AccessToken, seller2.ShopID, 600, 0)

	// Create 2-shop VietQR checkout and pay via webhook.
	checkoutResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
		"Idempotency-Key": "payfix-b2-" + uuid.NewString(),
	}, CheckoutRequest{
		AddressID: addr.ID,
		FromCart:  false,
		Items: []CheckoutItemInputDTO{
			{SKUID: sku1.ID, Quantity: 1},
			{SKUID: sku2.ID, Quantity: 1},
		},
		PaymentMethod: "VIETQR",
		ShippingChoices: []ShopShippingChoiceDTO{
			{ShopID: seller1.ShopID, CarrierCode: quote1.CarrierCode, ServiceCode: quote1.ServiceCode},
			{ShopID: seller2.ShopID, CarrierCode: quote2.CarrierCode, ServiceCode: quote2.ServiceCode},
		},
	}, http.StatusCreated, http.StatusOK)

	var checkout CheckoutResponse
	if err := DecodeJSON(checkoutResp, &checkout); err != nil {
		t.Fatalf("decode 2-shop VietQR checkout: %v", err)
	}
	if len(checkout.Orders) != 2 {
		t.Fatalf("expected 2 sub-orders, got %d", len(checkout.Orders))
	}

	var ord1, ord2 OrderDTO
	for _, o := range checkout.Orders {
		if o.ShopID == seller1.ShopID {
			ord1 = o
		} else if o.ShopID == seller2.ShopID {
			ord2 = o
		}
	}

	escrowBeforeWebhook, _ := payFixFindAccount(env.getLedgerSummary(t), "escrow_liability")

	whResp, err := env.client.PostSignedWebhook(ctx, "/webhooks/payment", PaymentWebhookPayload{
		Provider:     "vietqr_mock",
		ProviderTxID: "TXN-B2-" + uuid.NewString(),
		PaymentID:    checkout.PaymentID,
		OrderGroupID: checkout.CheckoutGroupID,
		AmountVND:    checkout.TotalPayableVND,
		Status:       "SUCCEEDED",
		Reference:    "NEXUSMART " + checkout.CheckoutGroupID,
		OccurredAt:   time.Now().UTC().Format(time.RFC3339),
	}, env.cfg.PaymentWebhookSecret, time.Now().UTC(), "")
	if err != nil || whResp.StatusCode != http.StatusOK {
		t.Fatalf("confirm VietQR webhook failed: err=%v status=%d body=%s", err, whResp.StatusCode, string(whResp.Body))
	}

	for _, id := range []string{ord1.ID, ord2.ID} {
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "sub-order "+id+" CONFIRMED", func() (bool, string) {
			o := env.getOrder(t, buyer.AccessToken, id)
			return StatusEquals(o.Status, "CONFIRMED"), fmt.Sprintf("status=%s", o.Status)
		})
	}

	refundPath := fmt.Sprintf("/api/v1/admin/payments/%s/refund", url.PathEscape(checkout.PaymentID))

	// Case A: Refund 10,000 VND on Shop 2 BEFORE completion -> seller2 must get (R2 - C2 - 10,000).
	const partialBeforeVND int64 = 10000
	env.mustDoJSON(t, http.MethodPost, refundPath, adminTok, map[string]string{
		"Idempotency-Key": "payfix-b2-caseA-" + uuid.NewString(),
	}, RefundRequest{
		OrderID:   ord2.ID,
		AmountVND: partialBeforeVND,
		Reason:    "PAY-B2 Case A partial refund before completion",
	}, http.StatusOK, http.StatusCreated)

	payFixShipAndDeliver(t, env, seller2, buyer, ord2.ID, quote2)
	env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/orders/%s/confirm-received", url.PathEscape(ord2.ID)),
		buyer.AccessToken, nil, nil, http.StatusOK)

	wantSeller2Net := ord2.SubtotalVND - ord2.CommissionVND - partialBeforeVND
	env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "PAY-B2 Case A seller2 net reflects partial refund", func() (bool, string) {
		bal := payFixGetSellerBalance(t, env, seller2)
		return bal.AvailableVND == wantSeller2Net, fmt.Sprintf("got=%+v want_available=%d", bal, wantSeller2Net)
	})

	// Case B: Complete Shop 1 first -> escrow released to seller1 -> refund 5,000 VND AFTER completion.
	payFixShipAndDeliver(t, env, seller1, buyer, ord1.ID, quote1)
	env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/orders/%s/confirm-received", url.PathEscape(ord1.ID)),
		buyer.AccessToken, nil, nil, http.StatusOK)

	wantSeller1Full := ord1.SubtotalVND - ord1.CommissionVND
	env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "PAY-B2 Case B seller1 receives full net before post-settle refund", func() (bool, string) {
		bal := payFixGetSellerBalance(t, env, seller1)
		return bal.AvailableVND == wantSeller1Full, fmt.Sprintf("got=%+v want_available=%d", bal, wantSeller1Full)
	})

	escrowBeforePostSettle, _ := payFixFindAccount(env.getLedgerSummary(t), "escrow_liability")
	const postSettleRefundVND int64 = 5000
	env.mustDoJSON(t, http.MethodPost, refundPath, adminTok, map[string]string{
		"Idempotency-Key": "payfix-b2-caseB-" + uuid.NewString(),
	}, RefundRequest{
		OrderID:   ord1.ID,
		AmountVND: postSettleRefundVND,
		Reason:    "PAY-B2 Case B post-settlement refund",
	}, http.StatusOK, http.StatusCreated)

	escrowAfterPostSettle, _ := payFixFindAccount(env.getLedgerSummary(t), "escrow_liability")
	if escrowAfterPostSettle.DebitTotalVND != escrowBeforePostSettle.DebitTotalVND {
		t.Fatalf("PAY-B2 / P3-10: post-settlement refund must NOT debit escrow_liability: before=%d after=%d",
			escrowBeforePostSettle.DebitTotalVND, escrowAfterPostSettle.DebitTotalVND)
	}

	bal1After := payFixGetSellerBalance(t, env, seller1)
	wantSeller1After := wantSeller1Full - postSettleRefundVND
	if bal1After.AvailableVND != wantSeller1After {
		t.Fatalf("PAY-B2 Case B: expected seller1 available_vnd=%d (clawed back %d), got %+v",
			wantSeller1After, postSettleRefundVND, bal1After)
	}

	// Post-settlement refund exceeding seller1's remaining available balance (even if <= buyer paid P1 - 5000) MUST return 409.
	excessOverSellerBal := wantSeller1After + 1000
	if excessOverSellerBal <= ord1.TotalPayableVND-postSettleRefundVND {
		env.mustDoJSON(t, http.MethodPost, refundPath, adminTok, map[string]string{
			"Idempotency-Key": "payfix-b2-caseB-over-" + uuid.NewString(),
		}, RefundRequest{
			OrderID:   ord1.ID,
			AmountVND: excessOverSellerBal,
			Reason:    "PAY-B2 Case B exceeds seller balance",
		}, http.StatusConflict)
	}

	summary := env.getLedgerSummary(t)
	if !summary.IsBalanced || summary.TotalDebitsVND != summary.TotalCreditsVND {
		t.Fatalf("ledger imbalanced after PAY-B2: %+v", summary)
	}
	escrowAcc, ok := payFixFindAccount(summary, "escrow_liability")
	if !ok {
		t.Fatalf("escrow_liability account missing from ledger summary")
	}
	// P3-10: Per-payment escrow net across this 2-shop VietQR payment must equal 0 (credits == debits == P1 + P2).
	escrowCreditDelta := escrowAcc.CreditTotalVND - escrowBeforeWebhook.CreditTotalVND
	escrowDebitDelta := escrowAcc.DebitTotalVND - escrowBeforeWebhook.DebitTotalVND
	if escrowDebitDelta > escrowCreditDelta {
		t.Fatalf("PAY-B2 / P3-10: per-payment escrow overdrawn! credit_delta=%d debit_delta=%d",
			escrowCreditDelta, escrowDebitDelta)
	}
	if escrowCreditDelta != checkout.TotalPayableVND || escrowDebitDelta != checkout.TotalPayableVND {
		t.Fatalf("PAY-B2 / P3-10: expected per-payment escrow credit=debit=%d, got credit_delta=%d debit_delta=%d",
			checkout.TotalPayableVND, escrowCreditDelta, escrowDebitDelta)
	}
}

// TestE2E_PaymentMoney_R1_And_LatePayment verifies via PaymentService gRPC + Webhook:
//   - R1: platform_discount_vnd persisted, settlement posts platform_promo_expense and credits seller R - C.
//   - R1 0 VND order: succeeds immediately without QR, settlement still credits seller R - C.
//   - R3 / PAY-B1 / PAY-B6: supersede_previous rejects when group already has succeeded payment;
//     late webhook on superseded or failed payment queues pending late_payment refund without mutating succeeded payment.
func TestE2E_PaymentMoney_R1_And_LatePayment(t *testing.T) {
	env := getEnv()
	ctx := env.ctx(t)
	adminTok := env.adminLogin(t)
	payClient := payFixDialGRPC(t, env)

	// 1. R1 + R3 supersede + late webhook via gRPC + signed webhook.
	t.Run("PlatformDiscount_Supersede_LateWebhook", func(t *testing.T) {
		groupID := "payfix-grp-" + uuid.NewString()
		orderID := "payfix-ord-" + uuid.NewString()
		shopID := "payfix-shop-" + uuid.NewString()
		buyerID := "payfix-buyer-" + uuid.NewString()
		buyerTok := MintAccessToken(env.cfg.JWTSecret, buyerID, "buyer")

		// G=229,000, D=30,000 (platform voucher), C=11,450, Ship=26,000 => P=225,000, seller net=217,550.
		allocs := []*paymentv1.ShopAllocation{
			{
				OrderId:             orderID,
				ShopId:              shopID,
				SubtotalVnd:         229000,
				CommissionVnd:       11450,
				ShippingFeeVnd:      26000,
				PlatformDiscountVnd: 30000,
			},
		}

		// Reject mismatched amount_vnd (forgetting to subtract D).
		_, err := payClient.CreatePayment(payFixAuthCtx(ctx, buyerTok), &paymentv1.CreatePaymentRequest{
			OrderGroupId:    groupID,
			OrderId:         groupID,
			UserId:          buyerID,
			AmountVnd:       255000, // wrong: ignores platform_discount_vnd 30,000
			Method:          paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
			ShopAllocations: allocs,
			IdempotencyKey:  "payfix-bad-sum-" + uuid.NewString(),
		})
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("R1: expected InvalidArgument for allocation sum mismatch, got err=%v", err)
		}

		// Create P1 (valid P = 225,000).
		p1Resp, err := payClient.CreatePayment(payFixAuthCtx(ctx, buyerTok), &paymentv1.CreatePaymentRequest{
			OrderGroupId:    groupID,
			OrderId:         groupID,
			UserId:          buyerID,
			AmountVnd:       225000,
			Method:          paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
			ShopAllocations: allocs,
			IdempotencyKey:  "payfix-p1-" + uuid.NewString(),
		})
		if err != nil {
			t.Fatalf("CreatePayment P1 failed: %v", err)
		}
		p1 := p1Resp.GetPayment()
		if len(p1.GetShopAllocations()) != 1 || p1.GetShopAllocations()[0].GetPlatformDiscountVnd() != 30000 {
			t.Fatalf("R1: expected platform_discount_vnd=30000 persisted, got %+v", p1.GetShopAllocations())
		}

		// Supersede P1 with P2.
		p2Resp, err := payClient.CreatePayment(payFixAuthCtx(ctx, buyerTok), &paymentv1.CreatePaymentRequest{
			OrderGroupId:      groupID,
			OrderId:           groupID,
			UserId:            buyerID,
			AmountVnd:         225000,
			Method:            paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
			ShopAllocations:   allocs,
			IdempotencyKey:    "payfix-p2-" + uuid.NewString(),
			SupersedePrevious: true,
		})
		if err != nil {
			t.Fatalf("CreatePayment P2 (supersede) failed: %v", err)
		}
		p2 := p2Resp.GetPayment()

		// Confirm P2 via webhook -> SUCCEEDED.
		wh2, err := env.client.PostSignedWebhook(ctx, "/webhooks/payment", PaymentWebhookPayload{
			Provider:     "vietqr_mock",
			ProviderTxID: "TXN-P2-" + uuid.NewString(),
			PaymentID:    p2.GetPaymentId(),
			OrderGroupID: groupID,
			AmountVND:    225000,
			Status:       "SUCCEEDED",
			Reference:    "NEXUSMART " + groupID,
			OccurredAt:   time.Now().UTC().Format(time.RFC3339),
		}, env.cfg.PaymentWebhookSecret, time.Now().UTC(), "")
		if err != nil || wh2.StatusCode != http.StatusOK {
			t.Fatalf("P2 webhook failed: err=%v status=%d body=%s", err, wh2.StatusCode, string(wh2.Body))
		}

		// R3: CreatePayment with supersede_previous MUST fail when group already has succeeded payment.
		_, err = payClient.CreatePayment(payFixAuthCtx(ctx, buyerTok), &paymentv1.CreatePaymentRequest{
			OrderGroupId:      groupID,
			OrderId:           groupID,
			UserId:            buyerID,
			AmountVnd:         225000,
			Method:            paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
			ShopAllocations:   allocs,
			IdempotencyKey:    "payfix-p3-" + uuid.NewString(),
			SupersedePrevious: true,
		})
		if status.Code(err) != codes.AlreadyExists && status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("R3: expected conflict when superseding group with succeeded payment, got %v", err)
		}

		// R3 / PAY-B6: Late SUCCEEDED webhook for superseded P1 -> late_payment_refund_queued.
		whLate, err := env.client.PostSignedWebhook(ctx, "/webhooks/payment", PaymentWebhookPayload{
			Provider:     "vietqr_mock",
			ProviderTxID: "TXN-LATE-P1-" + uuid.NewString(),
			PaymentID:    p1.GetPaymentId(),
			OrderGroupID: groupID,
			AmountVND:    225000,
			Status:       "SUCCEEDED",
			Reference:    "NEXUSMART " + groupID,
			OccurredAt:   time.Now().UTC().Format(time.RFC3339),
		}, env.cfg.PaymentWebhookSecret, time.Now().UTC(), "")
		if err != nil || whLate.StatusCode != http.StatusOK {
			t.Fatalf("late P1 webhook failed: err=%v status=%d body=%s", err, whLate.StatusCode, string(whLate.Body))
		}
		var lateRes PaymentWebhookResult
		if err := DecodeJSON(whLate, &lateRes); err != nil {
			t.Fatalf("decode late webhook result: %v", err)
		}
		if lateRes.Status != "late_payment_refund_queued" {
			t.Fatalf("R3: expected late_payment_refund_queued for superseded P1, got %+v", lateRes)
		}

		// ReleaseEscrow for P2 -> seller gets full 217,550 VND (R - C) and platform_promo_expense booked.
		summaryBefore := env.getLedgerSummary(t)
		promoBefore, _ := payFixFindAccount(summaryBefore, "platform_promo_expense")

		relResp, err := payClient.ReleaseEscrow(payFixAuthCtx(ctx, adminTok), &paymentv1.ReleaseEscrowRequest{
			OrderId: orderID,
			ShopId:  shopID,
		})
		if err != nil {
			t.Fatalf("ReleaseEscrow failed: %v", err)
		}
		if !relResp.GetReleased() || relResp.GetReleasedAmountVnd() != 217550 {
			t.Fatalf("R1: expected released=true amount=217550, got %+v", relResp)
		}

		balResp, err := payClient.GetSellerBalance(payFixAuthCtx(ctx, adminTok), &paymentv1.GetSellerBalanceRequest{
			ShopId: shopID,
		})
		if err != nil {
			t.Fatalf("GetSellerBalance failed: %v", err)
		}
		if balResp.GetAvailableVnd() != 217550 || balResp.GetPendingVnd() != 0 {
			t.Fatalf("R1: expected seller available=217550 pending=0, got %+v", balResp)
		}

		summaryAfter := env.getLedgerSummary(t)
		promoAfter, ok := payFixFindAccount(summaryAfter, "platform_promo_expense")
		if !ok {
			t.Fatalf("R1: platform_promo_expense account missing from ledger summary")
		}
		if promoAfter.DebitTotalVND-promoBefore.DebitTotalVND < 30000 {
			t.Fatalf("R1: expected platform_promo_expense debit increase >= 30000, before=%+v after=%+v", promoBefore, promoAfter)
		}
		if !summaryAfter.IsBalanced {
			t.Fatalf("ledger imbalanced after R1 settlement: %+v", summaryAfter)
		}
	})

	// 2. 0 VND fully subsidized order (R1 / PRM-B1).
	t.Run("ZeroPayableOrder_SucceedsWithoutQR_SettlesSellerNet", func(t *testing.T) {
		groupID := "payfix-zero-grp-" + uuid.NewString()
		orderID := "payfix-zero-ord-" + uuid.NewString()
		shopID := "payfix-zero-shop-" + uuid.NewString()
		buyerID := "payfix-zero-buyer-" + uuid.NewString()
		buyerTok := MintAccessToken(env.cfg.JWTSecret, buyerID, "buyer")

		// R=200,000, C=10,000 (seller net 190,000), Ship=20,000, D=220,000 => P=0.
		resp, err := payClient.CreatePayment(payFixAuthCtx(ctx, buyerTok), &paymentv1.CreatePaymentRequest{
			OrderGroupId: groupID,
			OrderId:      groupID,
			UserId:       buyerID,
			AmountVnd:    0,
			Method:       paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
			ShopAllocations: []*paymentv1.ShopAllocation{
				{
					OrderId:             orderID,
					ShopId:              shopID,
					SubtotalVnd:         200000,
					CommissionVnd:       10000,
					ShippingFeeVnd:      20000,
					PlatformDiscountVnd: 220000,
				},
			},
			IdempotencyKey: "payfix-zero-" + uuid.NewString(),
		})
		if err != nil {
			t.Fatalf("0 VND CreatePayment failed: %v", err)
		}
		p := resp.GetPayment()
		if p.GetStatus() != paymentv1.PaymentStatus_PAYMENT_STATUS_SUCCEEDED {
			t.Fatalf("expected 0 VND payment immediately SUCCEEDED, got %v", p.GetStatus())
		}
		if p.GetQrUrl() != "" || p.GetQrCode() != "" {
			t.Fatalf("expected 0 VND payment to have no QR, got qr_url=%q qr_code=%q", p.GetQrUrl(), p.GetQrCode())
		}

		// Refund on 0 VND payment MUST be rejected (buyer paid 0).
		_, rErr := payClient.CreateRefund(payFixAuthCtx(ctx, adminTok), &paymentv1.CreateRefundRequest{
			PaymentId:      p.GetPaymentId(),
			OrderId:        orderID,
			AmountVnd:      1000,
			Reason:         "should reject refund on 0 VND payment",
			IdempotencyKey: "payfix-zero-rf-" + uuid.NewString(),
		})
		if status.Code(rErr) != codes.AlreadyExists && status.Code(rErr) != codes.FailedPrecondition {
			t.Fatalf("expected conflict refunding 0 VND payment, got %v", rErr)
		}

		// ReleaseEscrow -> seller still receives R - C = 190,000 VND via platform_promo_expense.
		relResp, err := payClient.ReleaseEscrow(payFixAuthCtx(ctx, adminTok), &paymentv1.ReleaseEscrowRequest{
			OrderId: orderID,
			ShopId:  shopID,
		})
		if err != nil {
			t.Fatalf("ReleaseEscrow on 0 VND order failed: %v", err)
		}
		if !relResp.GetReleased() || relResp.GetReleasedAmountVnd() != 190000 {
			t.Fatalf("expected 0 VND order to release 190000 to seller, got %+v", relResp)
		}
		balResp, err := payClient.GetSellerBalance(payFixAuthCtx(ctx, adminTok), &paymentv1.GetSellerBalanceRequest{
			ShopId: shopID,
		})
		if err != nil {
			t.Fatalf("GetSellerBalance failed: %v", err)
		}
		if balResp.GetAvailableVnd() != 190000 {
			t.Fatalf("expected 0 VND seller available=190000, got %+v", balResp)
		}
	})

	// 3. PAY-B1: Late SUCCEEDED webhook on a FAILED payment (after order cancelled) MUST NOT confirm payment.
	t.Run("PAY_B1_FailedOrderPayment_LateWebhookQueuesRefund", func(t *testing.T) {
		cat, seller1, _ := env.ensureBaseMarketplace(t)
		_, sku := env.createApprovedProduct(t, seller1, cat.ID, "PAY-B1 Late Webhook Item", 255000, 20, 500)
		buyer := env.registerVerifyAndLogin(t, "payfix-b1-buyer")
		addr := env.addAddress(t, buyer)
		quote := env.calculateShippingQuote(t, buyer.AccessToken, seller1.ShopID, 500, 0)

		checkoutResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "payfix-b1-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID:     addr.ID,
			FromCart:      false,
			Items:         []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 1}},
			PaymentMethod: "VIETQR",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
			},
		}, http.StatusCreated, http.StatusOK)

		var checkout CheckoutResponse
		if err := DecodeJSON(checkoutResp, &checkout); err != nil {
			t.Fatalf("decode PAY-B1 checkout: %v", err)
		}
		ordID := checkout.Orders[0].ID

		// Send FAILED webhook -> payment FAILED, order CANCELLED.
		whFail, err := env.client.PostSignedWebhook(ctx, "/webhooks/payment", PaymentWebhookPayload{
			Provider:     "vietqr_mock",
			ProviderTxID: "TXN-B1-FAIL-" + uuid.NewString(),
			PaymentID:    checkout.PaymentID,
			OrderGroupID: checkout.CheckoutGroupID,
			AmountVND:    0,
			Status:       "FAILED",
			Reference:    "NEXUSMART " + checkout.CheckoutGroupID,
			OccurredAt:   time.Now().UTC().Format(time.RFC3339),
		}, env.cfg.PaymentWebhookSecret, time.Now().UTC(), "")
		if err != nil || whFail.StatusCode != http.StatusOK {
			t.Fatalf("FAILED webhook error: err=%v status=%d body=%s", err, whFail.StatusCode, string(whFail.Body))
		}

		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "PAY-B1 order CANCELLED", func() (bool, string) {
			o := env.getOrder(t, buyer.AccessToken, ordID)
			return StatusEquals(o.Status, "CANCELLED"), fmt.Sprintf("status=%s", o.Status)
		})

		// Late SUCCEEDED webhook on the FAILED payment -> MUST queue late_payment refund, NOT succeed.
		whLate, err := env.client.PostSignedWebhook(ctx, "/webhooks/payment", PaymentWebhookPayload{
			Provider:     "vietqr_mock",
			ProviderTxID: "TXN-B1-LATE-" + uuid.NewString(),
			PaymentID:    checkout.PaymentID,
			OrderGroupID: checkout.CheckoutGroupID,
			AmountVND:    checkout.TotalPayableVND,
			Status:       "SUCCEEDED",
			Reference:    "NEXUSMART " + checkout.CheckoutGroupID,
			OccurredAt:   time.Now().UTC().Format(time.RFC3339),
		}, env.cfg.PaymentWebhookSecret, time.Now().UTC(), "")
		if err != nil || whLate.StatusCode != http.StatusOK {
			t.Fatalf("late webhook on FAILED payment error: err=%v status=%d body=%s", err, whLate.StatusCode, string(whLate.Body))
		}
		var lateRes PaymentWebhookResult
		if err := DecodeJSON(whLate, &lateRes); err != nil {
			t.Fatalf("decode late webhook result: %v", err)
		}
		if lateRes.Status != "late_payment_refund_queued" {
			t.Fatalf("PAY-B1: expected late_payment_refund_queued on FAILED payment, got %+v", lateRes)
		}

		payAfter := env.getPayment(t, buyer.AccessToken, checkout.PaymentID)
		if StatusEquals(payAfter.Status, "SUCCEEDED") {
			t.Fatalf("PAY-B1: FAILED payment must NOT transition to SUCCEEDED after late webhook")
		}
		ordAfter := env.getOrder(t, buyer.AccessToken, ordID)
		if !StatusEquals(ordAfter.Status, "CANCELLED") {
			t.Fatalf("PAY-B1: CANCELLED order must remain CANCELLED, got %q", ordAfter.Status)
		}

		// Note: POST /api/v1/payments/{id}/retry rejection on CANCELLED order is owned by gateway + order agents.
		retryPath := fmt.Sprintf("/api/v1/payments/%s/retry", url.PathEscape(checkout.PaymentID))
		retryResp, err := env.client.DoJSON(ctx, http.MethodPost, retryPath, buyer.AccessToken, nil, map[string]string{
			"method": "VIETQR",
		})
		if err != nil {
			t.Fatalf("retry request failed: %v", err)
		}
		if retryResp.StatusCode != http.StatusConflict {
			t.Logf("[cross-service note: gateway+order owned] POST %s returned HTTP %d (expected 409 once gateway+order finish R3): %s",
				retryPath, retryResp.StatusCode, string(retryResp.Body))
		}
	})
}

// TestE2E_PaymentMoney_Round2_ReviewB verifies review-B findings end-to-end:
// - P0-1: COD ReleaseEscrow beating NATS order.delivered auto-collects COD idempotently and pays seller.
// - P0-2: CreatePayment idempotency_key rejects cross-user / mismatched parameter reuse (409 / AlreadyExists).
// - P2-7: CreatePayment(supersede_previous=true) rejects cancelled/failed/expired payment groups.
// - P2-8: CreateRefund idempotency_key rejects reuse across different payment_id / order_id / amount.
func TestE2E_PaymentMoney_Round2_ReviewB(t *testing.T) {
	env := getEnv()
	ctx := env.ctx(t)
	adminTok := env.adminLogin(t)
	payClient := payFixDialGRPC(t, env)

	t.Run("P0_1_COD_ReleaseEscrowBeforeDeliveredEvent", func(t *testing.T) {
		groupID := "payfix-r2-cod-grp-" + uuid.NewString()
		orderID := "payfix-r2-cod-ord-" + uuid.NewString()
		shopID := "payfix-r2-cod-shop-" + uuid.NewString()
		buyerID := "payfix-r2-cod-buyer-" + uuid.NewString()
		buyerTok := MintAccessToken(env.cfg.JWTSecret, buyerID, "buyer")

		// Create pending COD payment (R=200k, C=10k, Ship=25k, D=0 => P=225k, seller net=190k).
		createResp, err := payClient.CreatePayment(payFixAuthCtx(ctx, buyerTok), &paymentv1.CreatePaymentRequest{
			OrderGroupId: groupID,
			OrderId:      groupID,
			UserId:       buyerID,
			AmountVnd:    225000,
			Method:       paymentv1.PaymentMethod_PAYMENT_METHOD_COD,
			ShopAllocations: []*paymentv1.ShopAllocation{
				{
					OrderId:        orderID,
					ShopId:         shopID,
					SubtotalVnd:    200000,
					CommissionVnd:  10000,
					ShippingFeeVnd: 25000,
				},
			},
			IdempotencyKey: "payfix-r2-cod-" + uuid.NewString(),
		})
		if err != nil {
			t.Fatalf("CreatePayment COD failed: %v", err)
		}
		payID := createResp.GetPayment().GetPaymentId()
		if createResp.GetPayment().GetStatus() != paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING {
			t.Fatalf("expected COD PENDING before delivery, got %v", createResp.GetPayment().GetStatus())
		}

		// Simulate sync ReleaseEscrow arriving BEFORE async NATS order.delivered!
		codRecBefore, _ := payFixFindAccount(env.getLedgerSummary(t), "cod_receivable")
		relResp, err := payClient.ReleaseEscrow(payFixAuthCtx(ctx, adminTok), &paymentv1.ReleaseEscrowRequest{
			OrderId: orderID,
			ShopId:  shopID,
		})
		if err != nil {
			t.Fatalf("P0-1: ReleaseEscrow failed: %v", err)
		}
		if !relResp.GetReleased() || relResp.GetReleasedAmountVnd() != 190000 {
			t.Fatalf("P0-1: expected ReleaseEscrow to auto-collect COD and release 190000, got %+v", relResp)
		}

		getResp, err := payClient.GetPayment(payFixAuthCtx(ctx, adminTok), &paymentv1.GetPaymentRequest{PaymentId: payID})
		if err != nil {
			t.Fatalf("GetPayment failed: %v", err)
		}
		if getResp.GetPayment().GetStatus() != paymentv1.PaymentStatus_PAYMENT_STATUS_SUCCEEDED {
			t.Fatalf("P0-1: expected COD payment SUCCEEDED after ReleaseEscrow auto-collect, got %v", getResp.GetPayment().GetStatus())
		}

		codRecAfter, _ := payFixFindAccount(env.getLedgerSummary(t), "cod_receivable")
		if codRecAfter.DebitTotalVND-codRecBefore.DebitTotalVND != 225000 {
			t.Fatalf("P0-1: expected cod_receivable debit delta=225000, got %d",
				codRecAfter.DebitTotalVND-codRecBefore.DebitTotalVND)
		}

		balResp, err := payClient.GetSellerBalance(payFixAuthCtx(ctx, adminTok), &paymentv1.GetSellerBalanceRequest{
			ShopId: shopID,
		})
		if err != nil {
			t.Fatalf("GetSellerBalance failed: %v", err)
		}
		if balResp.GetAvailableVnd() != 190000 || balResp.GetPendingVnd() != 0 {
			t.Fatalf("P0-1: expected seller available=190000 pending=0, got %+v", balResp)
		}
	})

	t.Run("P0_2_CreatePaymentIdempotencyCrossUserAndParamMismatch", func(t *testing.T) {
		sharedKey := "payfix-r2-idem-" + uuid.NewString()
		buyerA := "payfix-r2-buyerA-" + uuid.NewString()
		buyerB := "payfix-r2-buyerB-" + uuid.NewString()
		tokA := MintAccessToken(env.cfg.JWTSecret, buyerA, "buyer")
		tokB := MintAccessToken(env.cfg.JWTSecret, buyerB, "buyer")
		grpA := "payfix-r2-grpA-" + uuid.NewString()
		grpB := "payfix-r2-grpB-" + uuid.NewString()

		pA, err := payClient.CreatePayment(payFixAuthCtx(ctx, tokA), &paymentv1.CreatePaymentRequest{
			OrderGroupId:   grpA,
			OrderId:        grpA,
			UserId:         buyerA,
			AmountVnd:      150000,
			Method:         paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
			IdempotencyKey: sharedKey,
		})
		if err != nil {
			t.Fatalf("Buyer A CreatePayment failed: %v", err)
		}

		// Same user + identical params -> idempotent replay returns same payment ID
		pAReplay, err := payClient.CreatePayment(payFixAuthCtx(ctx, tokA), &paymentv1.CreatePaymentRequest{
			OrderGroupId:   grpA,
			OrderId:        grpA,
			UserId:         buyerA,
			AmountVnd:      150000,
			Method:         paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
			IdempotencyKey: sharedKey,
		})
		if err != nil {
			t.Fatalf("Buyer A identical replay failed: %v", err)
		}
		if pAReplay.GetPayment().GetPaymentId() != pA.GetPayment().GetPaymentId() {
			t.Fatalf("expected same payment_id on identical replay")
		}

		// Buyer B reusing Buyer A's idempotency_key -> MUST be rejected with Conflict/AlreadyExists
		_, err = payClient.CreatePayment(payFixAuthCtx(ctx, tokB), &paymentv1.CreatePaymentRequest{
			OrderGroupId:   grpB,
			OrderId:        grpB,
			UserId:         buyerB,
			AmountVnd:      150000,
			Method:         paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
			IdempotencyKey: sharedKey,
		})
		if status.Code(err) != codes.AlreadyExists && status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("P0-2: cross-user idempotency_key reuse must fail with Conflict, got err=%v", err)
		}

		// Buyer A reusing same key with different amount_vnd -> MUST be rejected
		_, err = payClient.CreatePayment(payFixAuthCtx(ctx, tokA), &paymentv1.CreatePaymentRequest{
			OrderGroupId:   grpA,
			OrderId:        grpA,
			UserId:         buyerA,
			AmountVnd:      200000,
			Method:         paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
			IdempotencyKey: sharedKey,
		})
		if status.Code(err) != codes.AlreadyExists && status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("P0-2: mismatched amount idempotency_key reuse must fail with Conflict, got err=%v", err)
		}
	})

	t.Run("P2_7_SupersedePreviousRejectsCancelledGroup", func(t *testing.T) {
		buyerID := "payfix-r2-p27-buyer-" + uuid.NewString()
		tok := MintAccessToken(env.cfg.JWTSecret, buyerID, "buyer")
		grpID := "payfix-r2-p27-grp-" + uuid.NewString()

		p1Resp, err := payClient.CreatePayment(payFixAuthCtx(ctx, tok), &paymentv1.CreatePaymentRequest{
			OrderGroupId:   grpID,
			OrderId:        grpID,
			UserId:         buyerID,
			AmountVnd:      120000,
			Method:         paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
			IdempotencyKey: "payfix-r2-p27-1-" + uuid.NewString(),
		})
		if err != nil {
			t.Fatalf("CreatePayment P1 failed: %v", err)
		}
		_, err = payClient.CancelPayment(payFixAuthCtx(ctx, tok), &paymentv1.CancelPaymentRequest{
			PaymentId:    p1Resp.GetPayment().GetPaymentId(),
			OrderGroupId: grpID,
			Reason:       "buyer cancelled",
		})
		if err != nil {
			t.Fatalf("CancelPayment P1 failed: %v", err)
		}

		// Attempting CreatePayment(supersede_previous=true) on cancelled group MUST fail with Conflict
		_, err = payClient.CreatePayment(payFixAuthCtx(ctx, tok), &paymentv1.CreatePaymentRequest{
			OrderGroupId:      grpID,
			OrderId:           grpID,
			UserId:            buyerID,
			AmountVnd:         120000,
			Method:            paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
			IdempotencyKey:    "payfix-r2-p27-2-" + uuid.NewString(),
			SupersedePrevious: true,
		})
		if status.Code(err) != codes.AlreadyExists && status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("P2-7: supersede_previous on cancelled group must fail with Conflict, got err=%v", err)
		}
	})

	t.Run("P2_8_CreateRefundIdempotencyMismatchRejected", func(t *testing.T) {
		buyerID := "payfix-r2-p28-buyer-" + uuid.NewString()
		tok := MintAccessToken(env.cfg.JWTSecret, buyerID, "buyer")
		grpID := "payfix-r2-p28-grp-" + uuid.NewString()
		ord1 := "payfix-r2-p28-ord1-" + uuid.NewString()
		ord2 := "payfix-r2-p28-ord2-" + uuid.NewString()

		pResp, err := payClient.CreatePayment(payFixAuthCtx(ctx, tok), &paymentv1.CreatePaymentRequest{
			OrderGroupId: grpID,
			OrderId:      grpID,
			UserId:       buyerID,
			AmountVnd:    300000,
			Method:       paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
			ShopAllocations: []*paymentv1.ShopAllocation{
				{OrderId: ord1, ShopId: "shop-p28-1", SubtotalVnd: 100000, CommissionVnd: 5000},
				{OrderId: ord2, ShopId: "shop-p28-2", SubtotalVnd: 200000, CommissionVnd: 10000},
			},
			IdempotencyKey: "payfix-r2-p28-pay-" + uuid.NewString(),
		})
		if err != nil {
			t.Fatalf("CreatePayment failed: %v", err)
		}
		payID := pResp.GetPayment().GetPaymentId()

		whResp, err := env.client.PostSignedWebhook(ctx, "/webhooks/payment", PaymentWebhookPayload{
			Provider:     "vietqr_mock",
			ProviderTxID: "TXN-P28-" + uuid.NewString(),
			PaymentID:    payID,
			OrderGroupID: grpID,
			AmountVND:    300000,
			Status:       "SUCCEEDED",
			Reference:    "NEXUSMART " + grpID,
			OccurredAt:   time.Now().UTC().Format(time.RFC3339),
		}, env.cfg.PaymentWebhookSecret, time.Now().UTC(), "")
		if err != nil || whResp.StatusCode != http.StatusOK {
			t.Fatalf("confirm webhook failed: err=%v status=%d", err, whResp.StatusCode)
		}

		rfKey := "payfix-r2-p28-rf-" + uuid.NewString()
		rf1, err := payClient.CreateRefund(payFixAuthCtx(ctx, adminTok), &paymentv1.CreateRefundRequest{
			PaymentId:      payID,
			OrderId:        ord1,
			AmountVnd:      10000,
			Reason:         "first refund",
			IdempotencyKey: rfKey,
		})
		if err != nil || rf1.GetAlreadyExisted() {
			t.Fatalf("first refund failed: err=%v already=%v", err, rf1.GetAlreadyExisted())
		}

		// Same key, different order_id -> MUST reject with Conflict
		_, err = payClient.CreateRefund(payFixAuthCtx(ctx, adminTok), &paymentv1.CreateRefundRequest{
			PaymentId:      payID,
			OrderId:        ord2,
			AmountVnd:      10000,
			Reason:         "cross-order reuse",
			IdempotencyKey: rfKey,
		})
		if status.Code(err) != codes.AlreadyExists && status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("P2-8: refund idempotency_key reuse for different order_id must fail with Conflict, got err=%v", err)
		}

		// Same key, same order_id, different amount -> MUST reject with Conflict
		_, err = payClient.CreateRefund(payFixAuthCtx(ctx, adminTok), &paymentv1.CreateRefundRequest{
			PaymentId:      payID,
			OrderId:        ord1,
			AmountVnd:      20000,
			Reason:         "different amount reuse",
			IdempotencyKey: rfKey,
		})
		if status.Code(err) != codes.AlreadyExists && status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("P2-8: refund idempotency_key reuse with different amount must fail with Conflict, got err=%v", err)
		}
	})

	t.Run("P0_3_PendingMultiShopVietQR_CancelSuborderThenRacingWebhook", func(t *testing.T) {
		buyerID := "payfix-r2-p03-buyer-" + uuid.NewString()
		tok := MintAccessToken(env.cfg.JWTSecret, buyerID, "buyer")
		grpID := "payfix-r2-p03-grp-" + uuid.NewString()
		ord1 := "payfix-r2-p03-ord1-" + uuid.NewString()
		ord2 := "payfix-r2-p03-ord2-" + uuid.NewString()

		pResp, err := payClient.CreatePayment(payFixAuthCtx(ctx, tok), &paymentv1.CreatePaymentRequest{
			OrderGroupId: grpID,
			OrderId:      grpID,
			UserId:       buyerID,
			AmountVnd:    500000,
			Method:       paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
			ShopAllocations: []*paymentv1.ShopAllocation{
				{OrderId: ord1, ShopId: "shop-p03-1", SubtotalVnd: 200000, CommissionVnd: 10000},
				{OrderId: ord2, ShopId: "shop-p03-2", SubtotalVnd: 300000, CommissionVnd: 15000},
			},
			IdempotencyKey: "payfix-r2-p03-" + uuid.NewString(),
		})
		if err != nil {
			t.Fatalf("CreatePayment 2-shop VietQR failed: %v", err)
		}
		payID := pResp.GetPayment().GetPaymentId()

		// Publish ONLY the first sub-order's `order.cancelled` event (simulating race before ord2).
		payFixPublishNATSEvent(t, "order.cancelled", map[string]any{
			"order_id": ord1,
		})

		// Wait briefly for consumer to process order.cancelled(ord1) (either cancels payment or marks alloc1 cancelled).
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			g, gErr := payClient.GetPayment(payFixAuthCtx(ctx, adminTok), &paymentv1.GetPaymentRequest{PaymentId: payID})
			if gErr == nil && g.GetPayment().GetStatus() == paymentv1.PaymentStatus_PAYMENT_STATUS_CANCELLED {
				break
			}
			time.Sleep(150 * time.Millisecond)
		}

		// Racing SUCCEEDED webhook for full 500,000 arrives before ord2's order.cancelled!
		whResp, err := env.client.PostSignedWebhook(ctx, "/webhooks/payment", PaymentWebhookPayload{
			Provider:     "vietqr_mock",
			ProviderTxID: "TXN-P03-RACE-" + uuid.NewString(),
			PaymentID:    payID,
			OrderGroupID: grpID,
			AmountVND:    500000,
			Status:       "SUCCEEDED",
			Reference:    "NEXUSMART " + grpID,
			OccurredAt:   time.Now().UTC().Format(time.RFC3339),
		}, env.cfg.PaymentWebhookSecret, time.Now().UTC(), "")
		if err != nil || whResp.StatusCode != http.StatusOK {
			t.Fatalf("P0-3 racing webhook failed: err=%v status=%d body=%s", err, whResp.StatusCode, string(whResp.Body))
		}
		var whRes PaymentWebhookResult
		if err := DecodeJSON(whResp, &whRes); err != nil {
			t.Fatalf("decode webhook result: %v", err)
		}
		if whRes.Status != "late_payment_refund_queued" {
			t.Fatalf("P0-3: racing webhook after first sub-order cancelled must trigger late_payment_refund_queued, got %+v", whRes)
		}

		after, err := payClient.GetPayment(payFixAuthCtx(ctx, adminTok), &paymentv1.GetPaymentRequest{PaymentId: payID})
		if err != nil {
			t.Fatalf("GetPayment failed: %v", err)
		}
		if after.GetPayment().GetStatus() == paymentv1.PaymentStatus_PAYMENT_STATUS_SUCCEEDED {
			t.Fatalf("P0-3: payment must NOT become SUCCEEDED after sub-order was cancelled")
		}
	})

	t.Run("P1_5_ConcurrentWebhooksAndSupersedeNoDeadlock", func(t *testing.T) {
		for iter := 0; iter < 5; iter++ {
			buyerID := "payfix-r2-p15-buyer-" + uuid.NewString()
			tok := MintAccessToken(env.cfg.JWTSecret, buyerID, "buyer")
			grpID := "payfix-r2-p15-grp-" + uuid.NewString()

			p1Resp, err := payClient.CreatePayment(payFixAuthCtx(ctx, tok), &paymentv1.CreatePaymentRequest{
				OrderGroupId:   grpID,
				OrderId:        grpID,
				UserId:         buyerID,
				AmountVnd:      180000,
				Method:         paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
				IdempotencyKey: "payfix-r2-p15-1-" + uuid.NewString(),
			})
			if err != nil {
				t.Fatalf("iter %d CreatePayment P1 failed: %v", iter, err)
			}
			p2Resp, err := payClient.CreatePayment(payFixAuthCtx(ctx, tok), &paymentv1.CreatePaymentRequest{
				OrderGroupId:      grpID,
				OrderId:           grpID,
				UserId:            buyerID,
				AmountVnd:         180000,
				Method:            paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
				IdempotencyKey:    "payfix-r2-p15-2-" + uuid.NewString(),
				SupersedePrevious: true,
			})
			if err != nil {
				t.Fatalf("iter %d CreatePayment P2 failed: %v", iter, err)
			}
			p1ID := p1Resp.GetPayment().GetPaymentId()
			p2ID := p2Resp.GetPayment().GetPaymentId()

			// Fire concurrent webhook on P2 + supersede P3 + late webhook on P1 simultaneously.
			var wg sync.WaitGroup
			errCh := make(chan error, 3)

			wg.Add(3)
			go func() {
				defer wg.Done()
				res, wErr := env.client.PostSignedWebhook(ctx, "/webhooks/payment", PaymentWebhookPayload{
					Provider:     "vietqr_mock",
					ProviderTxID: "TXN-P15-P2-" + uuid.NewString(),
					PaymentID:    p2ID,
					OrderGroupID: grpID,
					AmountVND:    180000,
					Status:       "SUCCEEDED",
					Reference:    "NEXUSMART " + grpID,
					OccurredAt:   time.Now().UTC().Format(time.RFC3339),
				}, env.cfg.PaymentWebhookSecret, time.Now().UTC(), "")
				if wErr != nil || res.StatusCode != http.StatusOK {
					errCh <- fmt.Errorf("iter %d webhook P2 failed: err=%w status=%d body=%s", iter, wErr, res.StatusCode, string(res.Body))
				}
			}()
			go func() {
				defer wg.Done()
				res, wErr := env.client.PostSignedWebhook(ctx, "/webhooks/payment", PaymentWebhookPayload{
					Provider:     "vietqr_mock",
					ProviderTxID: "TXN-P15-P1-" + uuid.NewString(),
					PaymentID:    p1ID,
					OrderGroupID: grpID,
					AmountVND:    180000,
					Status:       "SUCCEEDED",
					Reference:    "NEXUSMART " + grpID,
					OccurredAt:   time.Now().UTC().Format(time.RFC3339),
				}, env.cfg.PaymentWebhookSecret, time.Now().UTC(), "")
				if wErr != nil || res.StatusCode != http.StatusOK {
					errCh <- fmt.Errorf("iter %d webhook P1 failed: err=%w status=%d body=%s", iter, wErr, res.StatusCode, string(res.Body))
				}
			}()
			go func() {
				defer wg.Done()
				_, cErr := payClient.CreatePayment(payFixAuthCtx(ctx, tok), &paymentv1.CreatePaymentRequest{
					OrderGroupId:      grpID,
					OrderId:           grpID,
					UserId:            buyerID,
					AmountVnd:         180000,
					Method:            paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
					IdempotencyKey:    "payfix-r2-p15-3-" + uuid.NewString(),
					SupersedePrevious: true,
				})
				// Either succeeds (before P2 webhook) or returns AlreadyExists/FailedPrecondition (after P2 webhook),
				// NEVER Internal/Aborted deadlock!
				if cErr != nil && status.Code(cErr) != codes.AlreadyExists && status.Code(cErr) != codes.FailedPrecondition {
					errCh <- fmt.Errorf("iter %d concurrent supersede unexpected error (deadlock?): %w", iter, cErr)
				}
			}()

			wg.Wait()
			close(errCh)
			for e := range errCh {
				t.Fatalf("P1-5: %v", e)
			}
		}
	})

	t.Run("P2_6_ZeroPayableOrder_FailedDeliveryReturnCancelsAllocation", func(t *testing.T) {
		buyerID := "payfix-r2-p26-buyer-" + uuid.NewString()
		tok := MintAccessToken(env.cfg.JWTSecret, buyerID, "buyer")
		grpID := "payfix-r2-p26-grp-" + uuid.NewString()
		ordID := "payfix-r2-p26-ord-" + uuid.NewString()
		shopID := "payfix-r2-p26-shop-" + uuid.NewString()

		pResp, err := payClient.CreatePayment(payFixAuthCtx(ctx, tok), &paymentv1.CreatePaymentRequest{
			OrderGroupId: grpID,
			OrderId:      grpID,
			UserId:       buyerID,
			AmountVnd:    0,
			Method:       paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
			ShopAllocations: []*paymentv1.ShopAllocation{
				{
					OrderId:             ordID,
					ShopId:              shopID,
					SubtotalVnd:         200000,
					CommissionVnd:       10000,
					ShippingFeeVnd:      20000,
					PlatformDiscountVnd: 220000,
				},
			},
			IdempotencyKey: "payfix-r2-p26-" + uuid.NewString(),
		})
		if err != nil {
			t.Fatalf("CreatePayment 0 VND failed: %v", err)
		}
		payID := pResp.GetPayment().GetPaymentId()

		// Publish failed-delivery `order.returned` (from_status = SHIPPING).
		payFixPublishNATSEvent(t, "order.returned", map[string]any{
			"order_id":    ordID,
			"from_status": "SHIPPING",
		})

		env.pollUntil(t, 5*time.Second, 150*time.Millisecond, "P2-6 0 VND payment CANCELLED after failed delivery", func() (bool, string) {
			g, gErr := payClient.GetPayment(payFixAuthCtx(ctx, adminTok), &paymentv1.GetPaymentRequest{PaymentId: payID})
			if gErr != nil {
				return false, gErr.Error()
			}
			return g.GetPayment().GetStatus() == paymentv1.PaymentStatus_PAYMENT_STATUS_CANCELLED, g.GetPayment().GetStatus().String()
		})

		// ReleaseEscrow MUST NOT settle platform_promo_expense to seller for failed delivery.
		relResp, err := payClient.ReleaseEscrow(payFixAuthCtx(ctx, adminTok), &paymentv1.ReleaseEscrowRequest{
			OrderId: ordID,
			ShopId:  shopID,
		})
		if err != nil {
			t.Fatalf("ReleaseEscrow failed: %v", err)
		}
		if relResp.GetReleased() || relResp.GetReleasedAmountVnd() != 0 {
			t.Fatalf("P2-6: failed-delivery 0 VND order must not release escrow, got %+v", relResp)
		}
	})

	t.Run("P2_9_FullRefundInPendingReleaseMarksPaymentRefunded", func(t *testing.T) {
		buyerID := "payfix-r2-p29-buyer-" + uuid.NewString()
		tok := MintAccessToken(env.cfg.JWTSecret, buyerID, "buyer")
		grpID := "payfix-r2-p29-grp-" + uuid.NewString()
		ordID := "payfix-r2-p29-ord-" + uuid.NewString()
		shopID := "payfix-r2-p29-shop-" + uuid.NewString()

		pResp, err := payClient.CreatePayment(payFixAuthCtx(ctx, tok), &paymentv1.CreatePaymentRequest{
			OrderGroupId: grpID,
			OrderId:      grpID,
			UserId:       buyerID,
			AmountVnd:    100000,
			Method:       paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
			ShopAllocations: []*paymentv1.ShopAllocation{
				{
					OrderId:       ordID,
					ShopId:        shopID,
					SubtotalVnd:   100000,
					CommissionVnd: 0,
				},
			},
			IdempotencyKey: "payfix-r2-p29-" + uuid.NewString(),
		})
		if err != nil {
			t.Fatalf("CreatePayment failed: %v", err)
		}
		payID := pResp.GetPayment().GetPaymentId()

		whResp, err := env.client.PostSignedWebhook(ctx, "/webhooks/payment", PaymentWebhookPayload{
			Provider:     "vietqr_mock",
			ProviderTxID: "TXN-P29-" + uuid.NewString(),
			PaymentID:    payID,
			OrderGroupID: grpID,
			AmountVND:    100000,
			Status:       "SUCCEEDED",
			Reference:    "NEXUSMART " + grpID,
			OccurredAt:   time.Now().UTC().Format(time.RFC3339),
		}, env.cfg.PaymentWebhookSecret, time.Now().UTC(), "")
		if err != nil || whResp.StatusCode != http.StatusOK {
			t.Fatalf("confirm webhook failed: err=%v status=%d", err, whResp.StatusCode)
		}

		// Transition allocation `held -> pending_release` via `order.completed` event.
		payFixPublishNATSEvent(t, "order.completed", map[string]any{
			"order_id": ordID,
		})
		env.pollUntil(t, 5*time.Second, 150*time.Millisecond, "P2-9 seller_pending == 100000", func() (bool, string) {
			bal, bErr := payClient.GetSellerBalance(payFixAuthCtx(ctx, adminTok), &paymentv1.GetSellerBalanceRequest{ShopId: shopID})
			if bErr != nil {
				return false, bErr.Error()
			}
			return bal.GetPendingVnd() == 100000, fmt.Sprintf("pending=%d available=%d", bal.GetPendingVnd(), bal.GetAvailableVnd())
		})

		// Refund 100% while in `pending_release` (post-settlement clawback).
		_, err = payClient.CreateRefund(payFixAuthCtx(ctx, adminTok), &paymentv1.CreateRefundRequest{
			PaymentId:      payID,
			OrderId:        ordID,
			AmountVnd:      100000,
			Reason:         "P2-9 100% refund in pending_release",
			IdempotencyKey: "payfix-r2-p29-rf-" + uuid.NewString(),
		})
		if err != nil {
			t.Fatalf("P2-9 CreateRefund failed: %v", err)
		}

		after, err := payClient.GetPayment(payFixAuthCtx(ctx, adminTok), &paymentv1.GetPaymentRequest{PaymentId: payID})
		if err != nil {
			t.Fatalf("GetPayment failed: %v", err)
		}
		if after.GetPayment().GetStatus() != paymentv1.PaymentStatus_PAYMENT_STATUS_REFUNDED {
			t.Fatalf("P2-9: expected payment status REFUNDED after 100%% refund in pending_release, got %v", after.GetPayment().GetStatus())
		}
	})
}

// TestE2E_PaymentMoney_Round3_Review2P verifies review2-P findings end-to-end:
// - P1-2: COD order.completed without prior order.delivered / ReleaseEscrow auto-collects COD and settles seller_pending.
// - P1-3: Concurrent order.cancelled / CancelPayment(order_group_id) + CreatePayment(supersede_previous=true) never leaves a pending P2 orphaned.
// - P2-4: Concurrent ReleaseEscrow (and post-settlement CreateRefund) for the same shop never hits ledger_accounts lock-upgrade deadlock.
// - P2-5: Concurrent reconciliation runs succeed cleanly with per-item short group-locked transactions.
// - P2-7: Multi-shop COD allocations sharing the same order_id book the full combined cod_receivable + escrow_liability.
// - P3-8: CreateRefund idempotent replay with order_id=order_group_id succeeds on a single-shop payment.
// - P3-9: Multi-shop VietQR (paid sub-order 100% refunded + 0 VND sub-order failed delivery return) transitions payment -> REFUNDED.
func TestE2E_PaymentMoney_Round3_Review2P(t *testing.T) {
	env := getEnv()
	ctx := env.ctx(t)
	adminTok := env.adminLogin(t)
	payClient := payFixDialGRPC(t, env)

	t.Run("P1_2_COD_OrderCompletedWithoutDeliveredAutoCollectsAndSettles", func(t *testing.T) {
		buyerID := "payfix-r3-p12-buyer-" + uuid.NewString()
		tok := MintAccessToken(env.cfg.JWTSecret, buyerID, "buyer")
		grpID := "payfix-r3-p12-grp-" + uuid.NewString()
		ordID := "payfix-r3-p12-ord-" + uuid.NewString()
		shopID := "payfix-r3-p12-shop-" + uuid.NewString()

		pResp, err := payClient.CreatePayment(payFixAuthCtx(ctx, tok), &paymentv1.CreatePaymentRequest{
			OrderGroupId: grpID,
			OrderId:      grpID,
			UserId:       buyerID,
			AmountVnd:    225000,
			Method:       paymentv1.PaymentMethod_PAYMENT_METHOD_COD,
			ShopAllocations: []*paymentv1.ShopAllocation{
				{
					OrderId:        ordID,
					ShopId:         shopID,
					SubtotalVnd:    200000,
					CommissionVnd:  10000,
					ShippingFeeVnd: 25000,
				},
			},
			IdempotencyKey: "payfix-r3-p12-" + uuid.NewString(),
		})
		if err != nil {
			t.Fatalf("CreatePayment COD failed: %v", err)
		}
		payID := pResp.GetPayment().GetPaymentId()

		codBefore, _ := payFixFindAccount(env.getLedgerSummary(t), "cod_receivable")

		// Publish ONLY `order.completed` (simulating lost/delayed `order.delivered` and failed best-effort ReleaseEscrow).
		payFixPublishNATSEvent(t, "order.completed", map[string]any{
			"order_id": ordID,
		})

		env.pollUntil(t, 5*time.Second, 150*time.Millisecond, "P1-2 COD payment SUCCEEDED and seller_pending == 190000", func() (bool, string) {
			g, gErr := payClient.GetPayment(payFixAuthCtx(ctx, adminTok), &paymentv1.GetPaymentRequest{PaymentId: payID})
			if gErr != nil {
				return false, gErr.Error()
			}
			bal, bErr := payClient.GetSellerBalance(payFixAuthCtx(ctx, adminTok), &paymentv1.GetSellerBalanceRequest{ShopId: shopID})
			if bErr != nil {
				return false, bErr.Error()
			}
			return g.GetPayment().GetStatus() == paymentv1.PaymentStatus_PAYMENT_STATUS_SUCCEEDED && bal.GetPendingVnd() == 190000,
				fmt.Sprintf("status=%v pending=%d available=%d", g.GetPayment().GetStatus(), bal.GetPendingVnd(), bal.GetAvailableVnd())
		})

		codAfter, _ := payFixFindAccount(env.getLedgerSummary(t), "cod_receivable")
		if codAfter.DebitTotalVND-codBefore.DebitTotalVND != 225000 {
			t.Fatalf("P1-2: expected cod_receivable debit delta=225000, got %d", codAfter.DebitTotalVND-codBefore.DebitTotalVND)
		}
	})

	t.Run("P1_3_ConcurrentCancelAndSupersedeNeverLeavesPendingP2", func(t *testing.T) {
		for iter := 0; iter < 5; iter++ {
			buyerID := "payfix-r3-p13-buyer-" + uuid.NewString()
			tok := MintAccessToken(env.cfg.JWTSecret, buyerID, "buyer")
			grpID := "payfix-r3-p13-grp-" + uuid.NewString()
			ordID := "payfix-r3-p13-ord-" + uuid.NewString()

			_, err := payClient.CreatePayment(payFixAuthCtx(ctx, tok), &paymentv1.CreatePaymentRequest{
				OrderGroupId: grpID,
				OrderId:      grpID,
				UserId:       buyerID,
				AmountVnd:    150000,
				Method:       paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
				ShopAllocations: []*paymentv1.ShopAllocation{
					{OrderId: ordID, ShopId: "shop-p13", SubtotalVnd: 150000, CommissionVnd: 7500},
				},
				IdempotencyKey: "payfix-r3-p13-1-" + uuid.NewString(),
			})
			if err != nil {
				t.Fatalf("iter %d CreatePayment P1 failed: %v", iter, err)
			}

			var wg sync.WaitGroup
			var p2ID string
			wg.Add(2)
			go func() {
				defer wg.Done()
				resp, sErr := payClient.CreatePayment(payFixAuthCtx(ctx, tok), &paymentv1.CreatePaymentRequest{
					OrderGroupId: grpID,
					OrderId:      grpID,
					UserId:       buyerID,
					AmountVnd:    150000,
					Method:       paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
					ShopAllocations: []*paymentv1.ShopAllocation{
						{OrderId: ordID, ShopId: "shop-p13", SubtotalVnd: 150000, CommissionVnd: 7500},
					},
					IdempotencyKey:    "payfix-r3-p13-2-" + uuid.NewString(),
					SupersedePrevious: true,
				})
				if sErr == nil {
					p2ID = resp.GetPayment().GetPaymentId()
				}
			}()
			go func() {
				defer wg.Done()
				// Cancel via order.cancelled event racing with supersede_previous=true.
				payFixPublishNATSEvent(t, "order.cancelled", map[string]any{
					"order_id": ordID,
				})
			}()
			wg.Wait()

			// Wait until group has no pending payment (either P1 cancelled before P2 rejected, or P2 cancelled too).
			env.pollUntil(t, 5*time.Second, 150*time.Millisecond, fmt.Sprintf("P1-3 iter %d no pending payment left", iter), func() (bool, string) {
				g, gErr := payClient.GetPayment(payFixAuthCtx(ctx, adminTok), &paymentv1.GetPaymentRequest{OrderGroupId: grpID})
				if gErr != nil {
					return false, gErr.Error()
				}
				if g.GetPayment().GetStatus() == paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING {
					return false, fmt.Sprintf("group still has PENDING payment %s (p2=%s)", g.GetPayment().GetPaymentId(), p2ID)
				}
				return true, g.GetPayment().GetStatus().String()
			})
		}
	})

	t.Run("P2_4_ConcurrentReleaseEscrowSameShopNoDeadlock", func(t *testing.T) {
		shopID := "payfix-r3-p24-shop-" + uuid.NewString()
		const pairs = 6
		expectedTotalAvailable := int64(0)

		for round := 0; round < pairs; round++ {
			ordIDs := make([]string, 2)
			for i := 0; i < 2; i++ {
				buyerID := fmt.Sprintf("payfix-r3-p24-b-%d-%d-%s", round, i, uuid.NewString())
				tok := MintAccessToken(env.cfg.JWTSecret, buyerID, "buyer")
				grpID := fmt.Sprintf("payfix-r3-p24-g-%d-%d-%s", round, i, uuid.NewString())
				ordID := fmt.Sprintf("payfix-r3-p24-o-%d-%d-%s", round, i, uuid.NewString())
				ordIDs[i] = ordID

				pResp, err := payClient.CreatePayment(payFixAuthCtx(ctx, tok), &paymentv1.CreatePaymentRequest{
					OrderGroupId: grpID,
					OrderId:      grpID,
					UserId:       buyerID,
					AmountVnd:    100000,
					Method:       paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
					ShopAllocations: []*paymentv1.ShopAllocation{
						{OrderId: ordID, ShopId: shopID, SubtotalVnd: 100000, CommissionVnd: 5000},
					},
					IdempotencyKey: "payfix-r3-p24-pay-" + uuid.NewString(),
				})
				if err != nil {
					t.Fatalf("round %d/%d CreatePayment failed: %v", round, i, err)
				}
				whResp, err := env.client.PostSignedWebhook(ctx, "/webhooks/payment", PaymentWebhookPayload{
					Provider:     "vietqr_mock",
					ProviderTxID: "TXN-P24-" + uuid.NewString(),
					PaymentID:    pResp.GetPayment().GetPaymentId(),
					OrderGroupID: grpID,
					AmountVND:    100000,
					Status:       "SUCCEEDED",
					Reference:    "NEXUSMART " + grpID,
					OccurredAt:   time.Now().UTC().Format(time.RFC3339),
				}, env.cfg.PaymentWebhookSecret, time.Now().UTC(), "")
				if err != nil || whResp.StatusCode != http.StatusOK {
					t.Fatalf("round %d/%d confirm webhook failed: err=%v status=%d", round, i, err, whResp.StatusCode)
				}
				expectedTotalAvailable += 95000
			}

			// Fire both ReleaseEscrow calls simultaneously for the SAME shop (both still in `held` state).
			var wg sync.WaitGroup
			startGate := make(chan struct{})
			errCh := make(chan error, 2)
			for i := 0; i < 2; i++ {
				oid := ordIDs[i]
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-startGate
					rel, rErr := payClient.ReleaseEscrow(payFixAuthCtx(ctx, adminTok), &paymentv1.ReleaseEscrowRequest{
						OrderId: oid,
						ShopId:  shopID,
					})
					if rErr != nil {
						errCh <- fmt.Errorf("round %d ReleaseEscrow(%s) failed (deadlock?): %w", round, oid, rErr)
						return
					}
					if !rel.GetReleased() || rel.GetReleasedAmountVnd() != 95000 {
						errCh <- fmt.Errorf("round %d ReleaseEscrow(%s) expected 95000, got %+v", round, oid, rel)
					}
				}()
			}
			close(startGate)
			wg.Wait()
			close(errCh)
			for e := range errCh {
				t.Fatalf("P2-4: %v", e)
			}
		}

		bal, err := payClient.GetSellerBalance(payFixAuthCtx(ctx, adminTok), &paymentv1.GetSellerBalanceRequest{ShopId: shopID})
		if err != nil {
			t.Fatalf("GetSellerBalance failed: %v", err)
		}
		if bal.GetAvailableVnd() != expectedTotalAvailable || bal.GetPendingVnd() != 0 {
			t.Fatalf("P2-4: expected available=%d pending=0, got %+v", expectedTotalAvailable, bal)
		}
	})

	t.Run("P2_5_ConcurrentReconciliationRunsSucceedWithoutDeadlock", func(t *testing.T) {
		reconcileURL := strings.TrimRight(env.cfg.PaymentHTTPURL, "/") + "/internal/reconciliation/run"
		today := time.Now().UTC().Format("2006-01-02")
		var wg sync.WaitGroup
		errCh := make(chan error, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				resp, err := env.client.DoAbsolute(ctx, http.MethodPost, reconcileURL, adminTok, nil, map[string]string{
					"run_date": today,
				})
				if err != nil {
					errCh <- fmt.Errorf("reconciliation request failed: %w", err)
					return
				}
				if resp.StatusCode != http.StatusOK {
					errCh <- fmt.Errorf("reconciliation returned HTTP %d: %s", resp.StatusCode, string(resp.Body))
				}
			}()
		}
		wg.Wait()
		close(errCh)
		for e := range errCh {
			t.Fatalf("P2-5: %v", e)
		}
	})

	t.Run("P2_7_COD_MultiShopSharedOrderIdCollectsFullAmount", func(t *testing.T) {
		buyerID := "payfix-r3-p27-buyer-" + uuid.NewString()
		tok := MintAccessToken(env.cfg.JWTSecret, buyerID, "buyer")
		grpID := "payfix-r3-p27-grp-" + uuid.NewString()
		shop1 := "payfix-r3-p27-shop1-" + uuid.NewString()
		shop2 := "payfix-r3-p27-shop2-" + uuid.NewString()

		// Both allocations intentionally leave OrderId empty so both default to payment.order_id (`grpID`).
		_, err := payClient.CreatePayment(payFixAuthCtx(ctx, tok), &paymentv1.CreatePaymentRequest{
			OrderGroupId: grpID,
			OrderId:      grpID,
			UserId:       buyerID,
			AmountVnd:    300000,
			Method:       paymentv1.PaymentMethod_PAYMENT_METHOD_COD,
			ShopAllocations: []*paymentv1.ShopAllocation{
				{ShopId: shop1, SubtotalVnd: 100000, CommissionVnd: 5000},
				{ShopId: shop2, SubtotalVnd: 200000, CommissionVnd: 10000},
			},
			IdempotencyKey: "payfix-r3-p27-" + uuid.NewString(),
		})
		if err != nil {
			t.Fatalf("CreatePayment shared-order-id COD failed: %v", err)
		}

		sumBefore := env.getLedgerSummary(t)
		codBefore, _ := payFixFindAccount(sumBefore, "cod_receivable")
		escrowBefore, _ := payFixFindAccount(sumBefore, "escrow_liability")

		rel, err := payClient.ReleaseEscrow(payFixAuthCtx(ctx, adminTok), &paymentv1.ReleaseEscrowRequest{
			OrderId: grpID,
		})
		if err != nil {
			t.Fatalf("ReleaseEscrow shared-order-id COD failed: %v", err)
		}
		if !rel.GetReleased() || rel.GetReleasedAmountVnd() != 285000 {
			t.Fatalf("P2-7: expected total released=285000 (95k+190k), got %+v", rel)
		}

		sumAfter := env.getLedgerSummary(t)
		codAfter, _ := payFixFindAccount(sumAfter, "cod_receivable")
		escrowAfter, _ := payFixFindAccount(sumAfter, "escrow_liability")

		codDelta := codAfter.DebitTotalVND - codBefore.DebitTotalVND
		escrowCreditDelta := escrowAfter.CreditTotalVND - escrowBefore.CreditTotalVND
		escrowDebitDelta := escrowAfter.DebitTotalVND - escrowBefore.DebitTotalVND
		if codDelta != 300000 {
			t.Fatalf("P2-7: expected cod_receivable debit delta=300000 (100k+200k), got %d", codDelta)
		}
		if escrowCreditDelta != 300000 || escrowDebitDelta != 300000 {
			t.Fatalf("P2-7: expected escrow credit_delta=debit_delta=300000, got credit=%d debit=%d", escrowCreditDelta, escrowDebitDelta)
		}
	})

	t.Run("P3_8_CreateRefundReplayWithOrderGroupIdOnSingleShopPayment", func(t *testing.T) {
		buyerID := "payfix-r3-p38-buyer-" + uuid.NewString()
		tok := MintAccessToken(env.cfg.JWTSecret, buyerID, "buyer")
		grpID := "payfix-r3-p38-grp-" + uuid.NewString()
		ordID := "payfix-r3-p38-ord-" + uuid.NewString()

		pResp, err := payClient.CreatePayment(payFixAuthCtx(ctx, tok), &paymentv1.CreatePaymentRequest{
			OrderGroupId: grpID,
			OrderId:      grpID,
			UserId:       buyerID,
			AmountVnd:    100000,
			Method:       paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
			ShopAllocations: []*paymentv1.ShopAllocation{
				{OrderId: ordID, ShopId: "shop-p38", SubtotalVnd: 100000, CommissionVnd: 5000},
			},
			IdempotencyKey: "payfix-r3-p38-pay-" + uuid.NewString(),
		})
		if err != nil {
			t.Fatalf("CreatePayment failed: %v", err)
		}
		payID := pResp.GetPayment().GetPaymentId()

		whResp, err := env.client.PostSignedWebhook(ctx, "/webhooks/payment", PaymentWebhookPayload{
			Provider:     "vietqr_mock",
			ProviderTxID: "TXN-P38-" + uuid.NewString(),
			PaymentID:    payID,
			OrderGroupID: grpID,
			AmountVND:    100000,
			Status:       "SUCCEEDED",
			Reference:    "NEXUSMART " + grpID,
			OccurredAt:   time.Now().UTC().Format(time.RFC3339),
		}, env.cfg.PaymentWebhookSecret, time.Now().UTC(), "")
		if err != nil || whResp.StatusCode != http.StatusOK {
			t.Fatalf("confirm webhook failed: err=%v status=%d", err, whResp.StatusCode)
		}

		rfKey := "payfix-r3-p38-rf-" + uuid.NewString()
		// First call passes order_id = grpID on a single-shop payment (stored as alloc.order_id = ordID).
		rf1, err := payClient.CreateRefund(payFixAuthCtx(ctx, adminTok), &paymentv1.CreateRefundRequest{
			PaymentId:      payID,
			OrderId:        grpID,
			AmountVnd:      50000,
			Reason:         "P3-8 group id refund",
			IdempotencyKey: rfKey,
		})
		if err != nil {
			t.Fatalf("P3-8 first CreateRefund(order_id=grpID) failed: %v", err)
		}

		// Identical replay with order_id = grpID MUST succeed idempotently (NOT 409 Conflict).
		rf2, err := payClient.CreateRefund(payFixAuthCtx(ctx, adminTok), &paymentv1.CreateRefundRequest{
			PaymentId:      payID,
			OrderId:        grpID,
			AmountVnd:      50000,
			Reason:         "P3-8 group id refund",
			IdempotencyKey: rfKey,
		})
		if err != nil {
			t.Fatalf("P3-8 identical replay with order_id=grpID must succeed, got err=%v", err)
		}
		if !rf2.GetAlreadyExisted() || rf2.GetRefund().GetRefundId() != rf1.GetRefund().GetRefundId() {
			t.Fatalf("P3-8 expected AlreadyExisted=true same refund_id, got rf1=%+v rf2=%+v", rf1, rf2)
		}
	})

	t.Run("P3_9_MultiShopVietQR_RefundPaidSuborderThenZeroSuborderReturnMarksRefunded", func(t *testing.T) {
		buyerID := "payfix-r3-p39-buyer-" + uuid.NewString()
		tok := MintAccessToken(env.cfg.JWTSecret, buyerID, "buyer")
		grpID := "payfix-r3-p39-grp-" + uuid.NewString()
		ordPaid := "payfix-r3-p39-paid-" + uuid.NewString()
		ordZero := "payfix-r3-p39-zero-" + uuid.NewString()

		pResp, err := payClient.CreatePayment(payFixAuthCtx(ctx, tok), &paymentv1.CreatePaymentRequest{
			OrderGroupId: grpID,
			OrderId:      grpID,
			UserId:       buyerID,
			AmountVnd:    200000,
			Method:       paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
			ShopAllocations: []*paymentv1.ShopAllocation{
				{OrderId: ordPaid, ShopId: "shop-p39-1", SubtotalVnd: 200000, CommissionVnd: 10000},
				{OrderId: ordZero, ShopId: "shop-p39-2", SubtotalVnd: 100000, CommissionVnd: 5000, PlatformDiscountVnd: 100000},
			},
			IdempotencyKey: "payfix-r3-p39-pay-" + uuid.NewString(),
		})
		if err != nil {
			t.Fatalf("CreatePayment failed: %v", err)
		}
		payID := pResp.GetPayment().GetPaymentId()

		whResp, err := env.client.PostSignedWebhook(ctx, "/webhooks/payment", PaymentWebhookPayload{
			Provider:     "vietqr_mock",
			ProviderTxID: "TXN-P39-" + uuid.NewString(),
			PaymentID:    payID,
			OrderGroupID: grpID,
			AmountVND:    200000,
			Status:       "SUCCEEDED",
			Reference:    "NEXUSMART " + grpID,
			OccurredAt:   time.Now().UTC().Format(time.RFC3339),
		}, env.cfg.PaymentWebhookSecret, time.Now().UTC(), "")
		if err != nil || whResp.StatusCode != http.StatusOK {
			t.Fatalf("confirm webhook failed: err=%v status=%d", err, whResp.StatusCode)
		}

		// Refund 100% of paid sub-order (200k); payment remains SUCCEEDED while 0 VND sub-order is held.
		_, err = payClient.CreateRefund(payFixAuthCtx(ctx, adminTok), &paymentv1.CreateRefundRequest{
			PaymentId:      payID,
			OrderId:        ordPaid,
			AmountVnd:      200000,
			Reason:         "P3-9 refund paid sub-order",
			IdempotencyKey: "payfix-r3-p39-rf-" + uuid.NewString(),
		})
		if err != nil {
			t.Fatalf("CreateRefund paid sub-order failed: %v", err)
		}

		// Now 0 VND sub-order fails delivery (`SHIPPING -> RETURNED`) -> payment MUST transition to REFUNDED.
		payFixPublishNATSEvent(t, "order.returned", map[string]any{
			"order_id":    ordZero,
			"from_status": "SHIPPING",
		})

		env.pollUntil(t, 5*time.Second, 150*time.Millisecond, "P3-9 payment transitions to REFUNDED after 0 VND sibling returned", func() (bool, string) {
			g, gErr := payClient.GetPayment(payFixAuthCtx(ctx, adminTok), &paymentv1.GetPaymentRequest{PaymentId: payID})
			if gErr != nil {
				return false, gErr.Error()
			}
			return g.GetPayment().GetStatus() == paymentv1.PaymentStatus_PAYMENT_STATUS_REFUNDED, g.GetPayment().GetStatus().String()
		})
	})
}

func payFixPublishNATSEvent(t *testing.T, eventType string, payload map[string]any) {
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
	if _, err := reader.ReadString('\n'); err != nil { // INFO {...}
		t.Fatalf("read NATS INFO: %v", err)
	}
	envBody, err := json.Marshal(map[string]any{
		"event_id":    uuid.NewString(),
		"event_type":  eventType,
		"trace_id":    "e2e-round2-" + uuid.NewString(),
		"occurred_at": time.Now().UTC().Format(time.RFC3339Nano),
		"payload":     payload,
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
