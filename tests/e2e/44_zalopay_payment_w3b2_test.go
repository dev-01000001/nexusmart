package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type w3b44ErrorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		TraceID string `json:"trace_id"`
	} `json:"error"`
}

type w3b44ZaloPayCallbackData struct {
	AppID       int    `json:"app_id"`
	AppTransID  string `json:"app_trans_id"`
	AppTime     int64  `json:"app_time"`
	AppUser     string `json:"app_user"`
	Amount      int64  `json:"amount"`
	EmbedData   string `json:"embed_data"`
	Item        string `json:"item"`
	ZPTransID   int64  `json:"zp_trans_id"`
	ServerTime  int64  `json:"server_time"`
	Channel     int    `json:"channel"`
	MerchantUID string `json:"merchant_user_id"`
	UserFeeAmt  int64  `json:"user_fee_amount"`
	DiscountAmt int64  `json:"discount_amount"`
}

type w3b44ZaloPayCallbackRequest struct {
	Data string `json:"data"`
	MAC  string `json:"mac"`
	Type int    `json:"type"`
}

type w3b44ZaloPayCallbackResponse struct {
	ReturnCode    int    `json:"return_code"`
	ReturnMessage string `json:"return_message"`
}

type w3b44RetryPaymentResponse struct {
	Payment       PaymentDTO `json:"payment"`
	SwitchedToCOD bool       `json:"switched_to_cod"`
	Orders        []OrderDTO `json:"orders"`
}

type w3b44PaymentMethodConfigDTO struct {
	Method                string `json:"method"`
	Enabled               bool   `json:"enabled"`
	MinOrderVND           int64  `json:"min_order_vnd"`
	MaxOrderVND           int64  `json:"max_order_vnd"`
	ExpiryMinutes         int32  `json:"expiry_minutes"`
	CredentialsConfigured bool   `json:"credentials_configured"`
	CredentialPreview     string `json:"credential_preview"`
	UpdatedBy             string `json:"updated_by"`
	UpdatedAt             string `json:"updated_at"`
	Note                  string `json:"note"`
	Version               int64  `json:"version"`
}

type w3b44PaymentMethodsListResponse struct {
	Methods []w3b44PaymentMethodConfigDTO `json:"methods"`
}

type w3b44PaymentMethodUpdateRequest struct {
	Enabled         bool   `json:"enabled"`
	MinOrderVND     int64  `json:"min_order_vnd"`
	MaxOrderVND     int64  `json:"max_order_vnd"`
	ExpiryMinutes   int32  `json:"expiry_minutes"`
	Note            string `json:"note"`
	ExpectedVersion int64  `json:"expected_version"`
}

type w3b44PaymentMethodUpdateResponse struct {
	Method w3b44PaymentMethodConfigDTO `json:"method"`
}

type w3b44RefundListResponse struct {
	Items    []RefundDTO `json:"items"`
	Page     int32       `json:"page"`
	PageSize int32       `json:"page_size"`
	Total    int64       `json:"total"`
}

type w3b44ReconQueueItemDTO struct {
	ID                string `json:"id"`
	Provider          string `json:"provider"`
	ProviderTxID      string `json:"provider_tx_id"`
	ExpectedPaymentID string `json:"expected_payment_id"`
	OrderRef          string `json:"order_ref"`
	ExpectedAmountVND int64  `json:"expected_amount_vnd"`
	ActualAmountVND   int64  `json:"actual_amount_vnd"`
	Reason            string `json:"reason"`
	ResolutionStatus  string `json:"resolution_status"`
}

type w3b44ReconQueueListResponse struct {
	Items    []w3b44ReconQueueItemDTO `json:"items"`
	Page     int32                    `json:"page"`
	PageSize int32                    `json:"page_size"`
	Total    int64                    `json:"total"`
}

func w3b44DecodeError(t *testing.T, label string, resp *RawResponse) (string, string) {
	t.Helper()
	var envErr w3b44ErrorEnvelope
	if err := DecodeJSON(resp, &envErr); err != nil {
		t.Fatalf("%s: decode error envelope (HTTP %d): %v; body=%s", label, resp.StatusCode, err, string(resp.Body))
	}
	return envErr.Error.Code, envErr.Error.Message
}

func w3b44AssertErrorCode(t *testing.T, label string, resp *RawResponse, wantCode string) {
	t.Helper()
	code, _ := w3b44DecodeError(t, label, resp)
	if code != wantCode {
		t.Fatalf("%s: error.code = %q, want %q (HTTP %d body=%s)", label, code, wantCode, resp.StatusCode, string(resp.Body))
	}
}

func w3b44AssertPreconditionPrefix(t *testing.T, label string, resp *RawResponse, wantPrefix string) {
	t.Helper()
	code, msg := w3b44DecodeError(t, label, resp)
	if code != "FAILED_PRECONDITION" || !strings.HasPrefix(strings.TrimSpace(msg), wantPrefix) {
		t.Fatalf("%s: got error.code=%q message=%q, want FAILED_PRECONDITION with prefix %q (HTTP %d body=%s)",
			label, code, msg, wantPrefix, resp.StatusCode, string(resp.Body))
	}
}

func w3b44AssertConflictPrefix(t *testing.T, label string, resp *RawResponse, wantPrefix string) {
	t.Helper()
	code, msg := w3b44DecodeError(t, label, resp)
	if code != "ALREADY_EXISTS" || !strings.HasPrefix(strings.TrimSpace(msg), wantPrefix) {
		t.Fatalf("%s: got error.code=%q message=%q, want ALREADY_EXISTS with prefix %q (HTTP %d body=%s)",
			label, code, msg, wantPrefix, resp.StatusCode, string(resp.Body))
	}
}

func w3b44AssertSafePayURL(t *testing.T, payURL string) {
	t.Helper()
	trimmed := strings.TrimSpace(payURL)
	if trimmed == "" {
		t.Fatalf("expected non-empty pay_url for ZALOPAY payment")
	}
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "javascript:") || strings.HasPrefix(lower, "data:") {
		t.Fatalf("unsafe pay_url scheme (F8): %q", trimmed)
	}
	if strings.HasPrefix(trimmed, "/") {
		return
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		t.Fatalf("invalid pay_url %q: err=%v", trimmed, err)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		t.Fatalf("unexpected pay_url scheme %q in %q", parsed.Scheme, trimmed)
	}
}

func w3b44FindAccount(summary LedgerSummaryResponse, accountCode string) AccountBalanceDTO {
	for _, acc := range summary.Accounts {
		if acc.AccountCode == accountCode {
			return acc
		}
	}
	return AccountBalanceDTO{}
}

func w3b44AssertLedgerBalanced(t *testing.T, env *testEnv) LedgerSummaryResponse {
	t.Helper()
	summary := env.getLedgerSummary(t)
	if !summary.IsBalanced || summary.TotalDebitsVND != summary.TotalCreditsVND {
		t.Fatalf("ledger unbalanced: is_balanced=%v debits=%d credits=%d",
			summary.IsBalanced, summary.TotalDebitsVND, summary.TotalCreditsVND)
	}
	if rp := w3b44FindAccount(summary, "refund_payable"); rp.BalanceVND < 0 {
		t.Fatalf("ledger invariant violation: refund_payable balance=%d < 0", rp.BalanceVND)
	}
	return summary
}

func w3b44NextZPTransID(env *testEnv) int64 {
	return time.Now().UTC().UnixMilli()*1000 + env.nextSeq()
}

func w3b44FlipHexChar(sig string) string {
	if sig == "" {
		return "0"
	}
	last := sig[len(sig)-1]
	if last == '0' {
		return sig[:len(sig)-1] + "1"
	}
	return sig[:len(sig)-1] + "0"
}

func w3b44BuildZaloPayCallbackWithAppID(
	t *testing.T,
	env *testEnv,
	appID int,
	appTransID string,
	zpTransID int64,
	amountVND int64,
	serverTimeMs int64,
	overrideMAC string,
) (w3b44ZaloPayCallbackRequest, string) {
	t.Helper()
	nowMs := time.Now().UTC().UnixMilli()
	if serverTimeMs == 0 {
		serverTimeMs = nowMs
	}
	dataObj := w3b44ZaloPayCallbackData{
		AppID:      appID,
		AppTransID: appTransID,
		AppTime:    nowMs,
		AppUser:    "nexusmart-e2e",
		Amount:     amountVND,
		EmbedData:  "{}",
		Item:       "[]",
		ZPTransID:  zpTransID,
		ServerTime: serverTimeMs,
		Channel:    38,
	}
	rawData, err := json.Marshal(dataObj)
	if err != nil {
		t.Fatalf("marshal zalopay callback data: %v", err)
	}
	dataStr := string(rawData)
	mac := overrideMAC
	if mac == "" {
		mac = ComputeHMACSHA256(env.cfg.ZaloPayKey2, rawData)
	}
	return w3b44ZaloPayCallbackRequest{
		Data: dataStr,
		MAC:  mac,
		Type: 1,
	}, dataStr
}

func w3b44BuildZaloPayCallback(
	t *testing.T,
	env *testEnv,
	appTransID string,
	zpTransID int64,
	amountVND int64,
	serverTimeMs int64,
	overrideMAC string,
) (w3b44ZaloPayCallbackRequest, string) {
	t.Helper()
	return w3b44BuildZaloPayCallbackWithAppID(t, env, env.cfg.ZaloPayAppID, appTransID, zpTransID, amountVND, serverTimeMs, overrideMAC)
}

func w3b44MustZaloPayCallback(
	t *testing.T,
	env *testEnv,
	label string,
	body any,
	wantHTTPStatus int,
	wantReturnCode int,
) w3b44ZaloPayCallbackResponse {
	t.Helper()
	resp, err := env.client.DoJSON(env.ctx(t), http.MethodPost, "/webhooks/zalopay", "", nil, body)
	if err != nil {
		t.Fatalf("%s: POST /webhooks/zalopay failed: %v", label, err)
	}
	if resp.StatusCode != wantHTTPStatus {
		t.Fatalf("%s: POST /webhooks/zalopay HTTP %d, want %d (body=%s)",
			label, resp.StatusCode, wantHTTPStatus, string(resp.Body))
	}
	var out w3b44ZaloPayCallbackResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("%s: decode zalopay callback response: %v", label, err)
	}
	if out.ReturnCode != wantReturnCode {
		t.Fatalf("%s: return_code = %d, want %d (HTTP %d body=%s)",
			label, out.ReturnCode, wantReturnCode, resp.StatusCode, string(resp.Body))
	}
	return out
}

func w3b44CheckoutSingleItem(
	t *testing.T,
	env *testEnv,
	buyer *userFixture,
	seller *sellerFixture,
	sku SKUDTO,
	qty int32,
	method string,
	quote CarrierQuoteDTO,
	flashSaleItemID string,
) CheckoutResponse {
	t.Helper()
	resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
		"Idempotency-Key": "w3b44-co-" + uuid.NewString(),
	}, CheckoutRequest{
		SalesChannel: "MARKETPLACE",
		AddressID:    buyer.AddressID,
		FromCart:     false,
		Items: []CheckoutItemInputDTO{
			{SKUID: sku.ID, Quantity: qty, FlashSaleItemID: flashSaleItemID},
		},
		PaymentMethod: method,
		ShippingChoices: []ShopShippingChoiceDTO{
			{ShopID: seller.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
		},
	}, http.StatusCreated)

	var out CheckoutResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode %s checkout: %v", method, err)
	}
	if out.PaymentID == "" || len(out.Orders) != 1 {
		t.Fatalf("unexpected %s checkout response: %s", method, string(resp.Body))
	}
	return out
}

func w3b44GetAdminPaymentMethods(t *testing.T, env *testEnv, adminTok string) map[string]w3b44PaymentMethodConfigDTO {
	t.Helper()
	resp := env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/payment-methods", adminTok, nil, nil, http.StatusOK)
	var out w3b44PaymentMethodsListResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode admin payment methods: %v", err)
	}
	byMethod := make(map[string]w3b44PaymentMethodConfigDTO, len(out.Methods))
	for _, m := range out.Methods {
		byMethod[NormalizeStatus(m.Method)] = m
	}
	return byMethod
}

func w3b44RestorePaymentMethods(t *testing.T, env *testEnv, adminTok string, orig map[string]w3b44PaymentMethodConfigDTO) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if freshTok, err := env.adminTokenCtx(ctx); err == nil && freshTok != "" {
		adminTok = freshTok
	}

	fetchCurrent := func() (map[string]w3b44PaymentMethodConfigDTO, error) {
		resp, err := env.client.DoJSON(ctx, http.MethodGet, "/api/v1/admin/payment-methods", adminTok, nil, nil)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("GET /api/v1/admin/payment-methods returned HTTP %d: %s", resp.StatusCode, string(resp.Body))
		}
		var out w3b44PaymentMethodsListResponse
		if err := DecodeJSON(resp, &out); err != nil {
			return nil, err
		}
		m := make(map[string]w3b44PaymentMethodConfigDTO, len(out.Methods))
		for _, item := range out.Methods {
			m[NormalizeStatus(item.Method)] = item
		}
		return m, nil
	}

	cur, err := fetchCurrent()
	if err != nil {
		t.Errorf("cleanup fetch payment-methods failed: %v", err)
		return
	}

	// Pass 1: restore methods that should be enabled first so pass 2 never hits "cannot disable last method".
	for _, passEnabled := range []bool{true, false} {
		for method, want := range orig {
			if want.Enabled != passEnabled {
				continue
			}
			nowCfg, ok := cur[method]
			if !ok {
				continue
			}
			if nowCfg.Enabled == want.Enabled &&
				nowCfg.MinOrderVND == want.MinOrderVND &&
				nowCfg.MaxOrderVND == want.MaxOrderVND &&
				nowCfg.ExpiryMinutes == want.ExpiryMinutes &&
				nowCfg.Note == want.Note {
				continue
			}
			putPath := fmt.Sprintf("/api/v1/admin/payment-methods/%s", url.PathEscape(method))
			putResp, putErr := env.client.DoJSON(ctx, http.MethodPut, putPath, adminTok, nil, w3b44PaymentMethodUpdateRequest{
				Enabled:         want.Enabled,
				MinOrderVND:     want.MinOrderVND,
				MaxOrderVND:     want.MaxOrderVND,
				ExpiryMinutes:   want.ExpiryMinutes,
				Note:            want.Note,
				ExpectedVersion: nowCfg.Version,
			})
			if putErr != nil {
				t.Errorf("cleanup PUT %s failed: %v", putPath, putErr)
				continue
			}
			if putResp.StatusCode != http.StatusOK {
				t.Errorf("cleanup PUT %s returned HTTP %d: %s", putPath, putResp.StatusCode, string(putResp.Body))
			}
		}
		if passEnabled {
			if refreshed, refErr := fetchCurrent(); refErr == nil {
				cur = refreshed
			}
		}
	}
}

func w3b44CancelOrderBestEffort(t *testing.T, env *testEnv, buyerTok, orderID, reason string) {
	t.Helper()
	path := fmt.Sprintf("/api/v1/orders/%s/cancel", url.PathEscape(orderID))
	resp, err := env.client.DoJSON(env.ctx(t), http.MethodPost, path, buyerTok, nil, map[string]string{
		"reason": reason,
	})
	if err != nil {
		t.Logf("cleanup cancel order %s err=%v", orderID, err)
		return
	}
	if resp.StatusCode != http.StatusOK {
		t.Logf("cleanup cancel order %s HTTP %d body=%s", orderID, resp.StatusCode, string(resp.Body))
	}
}

func TestE2E_W3B2_A6_ZaloPayAndPaymentMethods(t *testing.T) {
	env := getEnv()
	adminTok := env.adminLogin(t)
	cat, seller1, _ := env.ensureBaseMarketplace(t)
	buyer := env.ensureVerifiedBuyer(t)

	// Reuse a compact set of products on fixture seller1 across all A6 subtests to stay well inside E2E budget.
	_, skuStd := env.createApprovedProduct(t, seller1, cat.ID, "W3B2 A6 Standard Item", 150000, 80, 400)
	_, skuSmall := env.createApprovedProduct(t, seller1, cat.ID, "W3B2 A6 Small Item", 10000, 50, 300)
	_, skuHigh := env.createApprovedProduct(t, seller1, cat.ID, "W3B2 A6 High Value Item", 600000, 60, 500)
	_, skuFlash := env.createApprovedProduct(t, seller1, cat.ID, "W3B2 A6 Flash Item", 300000, 30, 400)

	quote := env.calculateShippingQuote(t, buyer.AccessToken, seller1.ShopID, 400, 0)

	// 1. ZaloPay mock end-to-end (F1-F2, F2-bis, F8):
	//    - bad MAC or 1-byte tampered data -> HTTP 401 + return_code=-1, 0 DB writes;
	//    - malformed JSON, wrong app_id, unknown app_trans_id -> HTTP 400 + return_code=-1, 0 DB writes;
	//    - stale server_time (>300s) on unpaid mock order -> HTTP 200 + return_code=0, order stays PENDING;
	//    - amount mismatch -> HTTP 200 + return_code=1, enqueued in reconciliation_queue, order stays PENDING;
	//    - valid MAC + exact amount -> HTTP 200 + return_code=1, order CONFIRMED, ledger balanced;
	//    - 10x replay same zp_trans_id -> HTTP 200 + return_code=1, strictly 1 ledger entry.
	t.Run("1_ZaloPayMockE2E_MAC_Replay_Mismatch_Confirm_Idempotency", func(t *testing.T) {
		co := w3b44CheckoutSingleItem(t, env, buyer, seller1, skuStd, 1, "ZALOPAY", quote, "")
		ordID := co.Orders[0].ID

		if !StatusEquals(co.PaymentMethod, "ZALOPAY") {
			t.Fatalf("checkout payment_method = %q, want ZALOPAY", co.PaymentMethod)
		}
		if !StatusEquals(co.Orders[0].Status, "PENDING") {
			t.Fatalf("checkout order status = %q, want PENDING", co.Orders[0].Status)
		}
		w3b44AssertSafePayURL(t, co.PayURL)

		payBefore := env.getPayment(t, buyer.AccessToken, co.PaymentID)
		if !StatusEquals(payBefore.Method, "ZALOPAY") || !StatusEquals(payBefore.Status, "PENDING") {
			t.Fatalf("GET payment before IPN: method=%q status=%q, want ZALOPAY/PENDING", payBefore.Method, payBefore.Status)
		}
		w3b44AssertSafePayURL(t, payBefore.PayURL)
		appTransID := strings.TrimSpace(payBefore.ProviderRef)
		if appTransID == "" {
			t.Fatalf("GET ZALOPAY payment returned empty provider_ref (app_trans_id): %+v", payBefore)
		}

		ledger0 := w3b44AssertLedgerBalanced(t, env)
		gcBefore := w3b44FindAccount(ledger0, "gateway_clearing")

		// (a) Invalid MAC & 1-byte tampered data -> strictly HTTP 401 + return_code=-1, 0 DB writes.
		badTxID := w3b44NextZPTransID(env)
		validReqForTamper, validDataStr := w3b44BuildZaloPayCallback(t, env, appTransID, badTxID, co.TotalPayableVND, 0, "")
		badMACReq := w3b44ZaloPayCallbackRequest{
			Data: validDataStr,
			MAC:  w3b44FlipHexChar(validReqForTamper.MAC),
			Type: 1,
		}
		w3b44MustZaloPayCallback(t, env, "bad MAC callback", badMACReq, http.StatusUnauthorized, -1)

		tamperedDataReq := w3b44ZaloPayCallbackRequest{
			Data: strings.Replace(validDataStr, `"channel":38`, `"channel":39`, 1),
			MAC:  validReqForTamper.MAC,
			Type: 1,
		}
		w3b44MustZaloPayCallback(t, env, "tampered 1-byte data callback", tamperedDataReq, http.StatusUnauthorized, -1)

		// (b) Malformed JSON, wrong app_id, unknown app_trans_id -> strictly HTTP 400 + return_code=-1.
		w3b44MustZaloPayCallback(t, env, "malformed JSON callback", "{not-valid-json", http.StatusBadRequest, -1)

		wrongAppIDReq, _ := w3b44BuildZaloPayCallbackWithAppID(t, env, env.cfg.ZaloPayAppID+9999, appTransID, w3b44NextZPTransID(env), co.TotalPayableVND, 0, "")
		w3b44MustZaloPayCallback(t, env, "wrong app_id callback", wrongAppIDReq, http.StatusBadRequest, -1)

		unknownTransReq, _ := w3b44BuildZaloPayCallback(t, env, "261124_unknown_"+env.runID, w3b44NextZPTransID(env), co.TotalPayableVND, 0, "")
		w3b44MustZaloPayCallback(t, env, "unknown app_trans_id callback", unknownTransReq, http.StatusBadRequest, -1)

		if o := env.getOrder(t, buyer.AccessToken, ordID); !StatusEquals(o.Status, "PENDING") {
			t.Fatalf("rejected callbacks mutated order status to %q", o.Status)
		}
		if p := env.getPayment(t, buyer.AccessToken, co.PaymentID); !StatusEquals(p.Status, "PENDING") {
			t.Fatalf("rejected callbacks mutated payment status to %q", p.Status)
		}
		ledgerAfterReject := w3b44AssertLedgerBalanced(t, env)
		if ledgerAfterReject.TotalDebitsVND != ledger0.TotalDebitsVND || ledgerAfterReject.TotalCreditsVND != ledger0.TotalCreditsVND {
			t.Fatalf("rejected callbacks mutated ledger: before=(%d,%d) after=(%d,%d)",
				ledger0.TotalDebitsVND, ledger0.TotalCreditsVND, ledgerAfterReject.TotalDebitsVND, ledgerAfterReject.TotalCreditsVND)
		}

		// (c) Valid MAC with stale server_time (>300s skew) on unpaid mock order (F2 replay guard):
		//     must NOT confirm directly from body; falls back to /v2/query (processing=3 -> HTTP 200 + return_code=0).
		staleTxID := w3b44NextZPTransID(env)
		staleTimeMs := time.Now().UTC().Add(-10 * time.Minute).UnixMilli()
		staleReq, _ := w3b44BuildZaloPayCallback(t, env, appTransID, staleTxID, co.TotalPayableVND, staleTimeMs, "")
		w3b44MustZaloPayCallback(t, env, "stale server_time callback", staleReq, http.StatusOK, 0)
		if o := env.getOrder(t, buyer.AccessToken, ordID); !StatusEquals(o.Status, "PENDING") {
			t.Fatalf("stale server_time (>300s) callback directly confirmed unpaid order: status=%q", o.Status)
		}

		// (d) Amount mismatch with valid MAC -> HTTP 200 + return_code=1 (queued), order stays PENDING, appears in reconciliation_queue.
		mismatchTxID := w3b44NextZPTransID(env)
		mismatchAmt := co.TotalPayableVND + 10000
		mismatchReq, _ := w3b44BuildZaloPayCallback(t, env, appTransID, mismatchTxID, mismatchAmt, 0, "")
		w3b44MustZaloPayCallback(t, env, "amount mismatch callback", mismatchReq, http.StatusOK, 1)
		if o := env.getOrder(t, buyer.AccessToken, ordID); !StatusEquals(o.Status, "PENDING") {
			t.Fatalf("amount-mismatch callback unexpectedly confirmed order: status=%q", o.Status)
		}
		if p := env.getPayment(t, buyer.AccessToken, co.PaymentID); !StatusEquals(p.Status, "PENDING") {
			t.Fatalf("amount-mismatch callback unexpectedly mutated payment: status=%q", p.Status)
		}

		mismatchTxStr := strconv.FormatInt(mismatchTxID, 10)
		env.pollUntil(t, env.cfg.PollTimeout, 250*time.Millisecond, "zalopay amount mismatch in reconciliation queue", func() (bool, string) {
			qResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/reconciliation/queue?status=pending&page=1&page_size=100", adminTok, nil, nil, http.StatusOK)
			var qOut w3b44ReconQueueListResponse
			if err := DecodeJSON(qResp, &qOut); err != nil {
				return false, err.Error()
			}
			for _, it := range qOut.Items {
				if it.ProviderTxID == mismatchTxStr && it.ExpectedPaymentID == co.PaymentID && it.ActualAmountVND == mismatchAmt {
					return true, ""
				}
			}
			return false, fmt.Sprintf("queue pending total=%d, looking for provider_tx_id=%s payment=%s actual=%d",
				qOut.Total, mismatchTxStr, co.PaymentID, mismatchAmt)
		})
		_ = w3b44AssertLedgerBalanced(t, env)

		// (e) Valid MAC + exact amount -> strictly HTTP 200 + return_code=1, order CONFIRMED, payment SUCCEEDED, ledger balanced.
		validTxID := w3b44NextZPTransID(env)
		validReq, _ := w3b44BuildZaloPayCallback(t, env, appTransID, validTxID, co.TotalPayableVND, 0, "")
		w3b44MustZaloPayCallback(t, env, "valid confirm callback", validReq, http.StatusOK, 1)

		env.pollUntil(t, env.cfg.PollTimeout, 250*time.Millisecond, "zalopay order CONFIRMED", func() (bool, string) {
			o := env.getOrder(t, buyer.AccessToken, ordID)
			return StatusEquals(o.Status, "CONFIRMED") && StatusEquals(o.PaymentStatus, "SUCCEEDED"),
				fmt.Sprintf("status=%s payment_status=%s", o.Status, o.PaymentStatus)
		})
		payAfter := env.getPayment(t, buyer.AccessToken, co.PaymentID)
		if !StatusEquals(payAfter.Status, "SUCCEEDED") {
			t.Fatalf("payment %s status = %q, want SUCCEEDED", co.PaymentID, payAfter.Status)
		}

		ledgerAfterConfirm := w3b44AssertLedgerBalanced(t, env)
		gcAfterConfirm := w3b44FindAccount(ledgerAfterConfirm, "gateway_clearing")
		if delta := gcAfterConfirm.DebitTotalVND - gcBefore.DebitTotalVND; delta != co.TotalPayableVND {
			t.Fatalf("gateway_clearing debit delta = %d, want %d", delta, co.TotalPayableVND)
		}

		// (f) Replay 10x with the exact same zp_trans_id -> strictly HTTP 200 + return_code=1, strictly 1 ledger entry.
		for i := 1; i <= 10; i++ {
			w3b44MustZaloPayCallback(t, env, fmt.Sprintf("zalopay replay #%d", i), validReq, http.StatusOK, 1)
		}

		ledgerAfterReplay := w3b44AssertLedgerBalanced(t, env)
		gcAfterReplay := w3b44FindAccount(ledgerAfterReplay, "gateway_clearing")
		if gcAfterReplay.DebitTotalVND != gcAfterConfirm.DebitTotalVND {
			t.Fatalf("10x zalopay replay double-posted gateway_clearing: before=%d after=%d",
				gcAfterConfirm.DebitTotalVND, gcAfterReplay.DebitTotalVND)
		}
		if ledgerAfterReplay.TotalDebitsVND != ledgerAfterConfirm.TotalDebitsVND ||
			ledgerAfterReplay.TotalCreditsVND != ledgerAfterConfirm.TotalCreditsVND {
			t.Fatalf("10x zalopay replay modified ledger totals: before=(%d,%d) after=(%d,%d)",
				ledgerAfterConfirm.TotalDebitsVND, ledgerAfterConfirm.TotalCreditsVND,
				ledgerAfterReplay.TotalDebitsVND, ledgerAfterReplay.TotalCreditsVND)
		}

		// (g) Manual admin refund with reserved internal Idempotency-Key prefixes -> strictly 400 INVALID_ARGUMENT, 0 ledger writes (p4 Item E).
		manualRefundPath := fmt.Sprintf("/api/v1/admin/payments/%s/refund", url.PathEscape(co.PaymentID))
		reservedKeys := []string{
			"order-cancel-refund:e2e-fake-" + uuid.NewString(),
			"order-return-refund:e2e-fake-" + uuid.NewString(),
			"superseded-late-refund:e2e-fake-" + uuid.NewString(),
			"late-webhook:e2e-fake-" + uuid.NewString(),
			"recon-refund:e2e-fake-" + uuid.NewString(),
			"recon-overpay:e2e-fake-" + uuid.NewString(),
			"  ReFuNd-SeTtLe:e2e-fake-" + uuid.NewString() + "  ",
		}
		for _, badKey := range reservedKeys {
			badKeyResp := env.mustDoJSON(t, http.MethodPost, manualRefundPath, adminTok, map[string]string{
				"Idempotency-Key": badKey,
			}, RefundRequest{
				OrderID:   ordID,
				AmountVND: 1000,
				Reason:    "reserved prefix must be rejected",
			}, http.StatusBadRequest)
			w3b44AssertErrorCode(t, "manual refund reserved prefix "+strings.TrimSpace(badKey), badKeyResp, "INVALID_ARGUMENT")
		}
		ledgerAfterReserved := w3b44AssertLedgerBalanced(t, env)
		if ledgerAfterReserved.TotalDebitsVND != ledgerAfterReplay.TotalDebitsVND ||
			ledgerAfterReserved.TotalCreditsVND != ledgerAfterReplay.TotalCreditsVND {
			t.Fatalf("rejected reserved-prefix manual refunds mutated ledger: before=(%d,%d) after=(%d,%d)",
				ledgerAfterReplay.TotalDebitsVND, ledgerAfterReplay.TotalCreditsVND,
				ledgerAfterReserved.TotalDebitsVND, ledgerAfterReserved.TotalCreditsVND)
		}

		// (h) Overflowing page query parameter -> strictly 400 INVALID_ARGUMENT (gateway r1).
		for _, overflowQuery := range []string{
			"/api/v1/admin/refunds?page=2147483648&page_size=20",
			"/api/v1/admin/refunds?page=200000000&page_size=20",
		} {
			ovResp := env.mustDoJSON(t, http.MethodGet, overflowQuery, adminTok, nil, nil, http.StatusBadRequest)
			w3b44AssertErrorCode(t, "overflow pagination "+overflowQuery, ovResp, "INVALID_ARGUMENT")
		}
	})

	// 2. RetryPayment method switching (UC-B25 / F5):
	//    - ZALOPAY -> VIETQR keeps original expiry, late IPN on superseded ZALOPAY creates is_late_payment refund without confirming order.
	//    - VIETQR -> COD switches to CONFIRMED on normal order; blocked on Flash Sale (FLASH_SALE_COD_NOT_ALLOWED),
	//      unverified phone (COD_PHONE_NOT_VERIFIED), and over COD limit (COD_LIMIT_EXCEEDED).
	t.Run("2_RetryPayment_MethodSwitch_LateIPN_And_Guards", func(t *testing.T) {
		// 2A + 2B: ZALOPAY -> VIETQR + late ZaloPay IPN on superseded payment.
		zaloCo := w3b44CheckoutSingleItem(t, env, buyer, seller1, skuStd, 1, "ZALOPAY", quote, "")
		zaloOrdID := zaloCo.Orders[0].ID
		oldPayID := zaloCo.PaymentID
		oldPay := env.getPayment(t, buyer.AccessToken, oldPayID)
		oldAppTransID := strings.TrimSpace(oldPay.ProviderRef)
		if oldAppTransID == "" {
			t.Fatalf("old ZALOPAY payment missing provider_ref (app_trans_id)")
		}
		origExp, err := time.Parse(time.RFC3339, zaloCo.PaymentExpiresAt)
		if err != nil {
			t.Fatalf("parse original payment_expires_at %q: %v", zaloCo.PaymentExpiresAt, err)
		}

		// Missing Idempotency-Key -> strictly 400 MISSING_IDEMPOTENCY_KEY.
		retryPath := fmt.Sprintf("/api/v1/payments/%s/retry", url.PathEscape(oldPayID))
		noKeyResp := env.mustDoJSON(t, http.MethodPost, retryPath, buyer.AccessToken, nil, map[string]string{
			"method": "VIETQR",
		}, http.StatusBadRequest)
		w3b44AssertErrorCode(t, "retry without Idempotency-Key", noKeyResp, "MISSING_IDEMPOTENCY_KEY")

		// Switch ZALOPAY -> VIETQR.
		switchResp := env.mustDoJSON(t, http.MethodPost, retryPath, buyer.AccessToken, map[string]string{
			"Idempotency-Key": "w3b44-retry-vqr-" + uuid.NewString(),
		}, map[string]string{
			"method": "VIETQR",
		}, http.StatusOK)
		var switchOut w3b44RetryPaymentResponse
		if err := DecodeJSON(switchResp, &switchOut); err != nil {
			t.Fatalf("decode retry ZALOPAY->VIETQR: %v", err)
		}
		if switchOut.SwitchedToCOD {
			t.Fatalf("expected switched_to_cod=false when switching to VIETQR")
		}
		newPayID := switchOut.Payment.PaymentID
		if newPayID == "" || newPayID == oldPayID {
			t.Fatalf("expected new payment_id != %q, got %q", oldPayID, newPayID)
		}
		if !StatusEquals(switchOut.Payment.Method, "VIETQR") {
			t.Fatalf("switched payment method = %q, want VIETQR", switchOut.Payment.Method)
		}
		if switchOut.Payment.QRURL == "" || switchOut.Payment.QRCode == "" {
			t.Fatalf("switched VIETQR payment missing qr_url or qr_code: %+v", switchOut.Payment)
		}

		ordAfterSwitch := env.getOrder(t, buyer.AccessToken, zaloOrdID)
		if ordAfterSwitch.PaymentID != newPayID || !StatusEquals(ordAfterSwitch.PaymentMethod, "VIETQR") {
			t.Fatalf("order not synced after switch: payment_id=%q method=%q (want %q / VIETQR)",
				ordAfterSwitch.PaymentID, ordAfterSwitch.PaymentMethod, newPayID)
		}
		if ordAfterSwitch.TotalPayableVND != zaloCo.TotalPayableVND {
			t.Fatalf("method switch changed buyer total_payable_vnd: orig=%d after=%d",
				zaloCo.TotalPayableVND, ordAfterSwitch.TotalPayableVND)
		}
		newExp, parseErr := time.Parse(time.RFC3339, ordAfterSwitch.PaymentExpiresAt)
		if parseErr != nil {
			t.Fatalf("parse switched payment_expires_at %q: %v", ordAfterSwitch.PaymentExpiresAt, parseErr)
		}
		if newExp.After(origExp.Add(2 * time.Second)) {
			t.Fatalf("RetryPayment(ZALOPAY->VIETQR) extended payment_expires_at from %s to %s", origExp, newExp)
		}

		// Send late ZaloPay IPN for the SUPERSEDED old payment -> HTTP 200 + return_code=1, creates late_payment refund, order stays PENDING.
		// In mock mode, record_late_payment_refund_tx commits the wallet refund in PENDING with provider_m_refund_id without
		// synchronously calling /v2/refund (background poller ticks every 15s), letting us verify manual settle -> 409 ZALOPAY_REFUND_AUTO_MANAGED.
		lateTxID := w3b44NextZPTransID(env)
		lateReq, _ := w3b44BuildZaloPayCallback(t, env, oldAppTransID, lateTxID, zaloCo.TotalPayableVND, 0, "")
		w3b44MustZaloPayCallback(t, env, "late IPN on superseded ZALOPAY", lateReq, http.StatusOK, 1)

		var lateRf RefundDTO
		env.pollUntil(t, env.cfg.PollTimeout, 100*time.Millisecond, "late_payment refund created for superseded ZaloPay", func() (bool, string) {
			rfPath := fmt.Sprintf("/api/v1/admin/refunds?order_id=%s&page=1&page_size=50", url.QueryEscape(zaloOrdID))
			rfResp := env.mustDoJSON(t, http.MethodGet, rfPath, adminTok, nil, nil, http.StatusOK)
			var rfList w3b44RefundListResponse
			if err := DecodeJSON(rfResp, &rfList); err != nil {
				return false, err.Error()
			}
			for _, rf := range rfList.Items {
				if rf.PaymentID == oldPayID && rf.IsLatePayment && rf.AmountVND == zaloCo.TotalPayableVND {
					lateRf = rf
					return true, ""
				}
			}
			return false, fmt.Sprintf("refunds for order=%s count=%d (waiting for payment_id=%s is_late_payment=true)",
				zaloOrdID, len(rfList.Items), oldPayID)
		})
		if !StatusEquals(lateRf.Status, "PENDING") {
			t.Fatalf("late ZaloPay wallet refund %s expected PENDING before background poller settles it, got status=%q provider_status=%q",
				lateRf.RefundID, lateRf.Status, lateRf.ProviderStatus)
		}
		if strings.TrimSpace(lateRf.ProviderMRefundID) == "" {
			t.Fatalf("late ZaloPay wallet refund %s missing provider_m_refund_id: %+v", lateRf.RefundID, lateRf)
		}

		// Manual settle on an active PENDING ZaloPay wallet refund -> strictly 409 ZALOPAY_REFUND_AUTO_MANAGED (payment p4 Item C).
		settleAutoPath := fmt.Sprintf("/api/v1/admin/refunds/%s/settle", url.PathEscape(lateRf.RefundID))
		settleAutoResp := env.mustDoJSON(t, http.MethodPost, settleAutoPath, adminTok, map[string]string{
			"Idempotency-Key": "w3b44-settle-auto-zp-" + uuid.NewString(),
		}, map[string]string{
			"provider_refund_id": "FT-W3B44-SHOULD-FAIL",
			"note":               "manual settle must fail while ZaloPay refund is auto-managed",
		}, http.StatusConflict)
		w3b44AssertConflictPrefix(t, "settle auto-managed PENDING ZaloPay refund", settleAutoResp, "ZALOPAY_REFUND_AUTO_MANAGED:")

		ordAfterLate := env.getOrder(t, buyer.AccessToken, zaloOrdID)
		if !StatusEquals(ordAfterLate.Status, "PENDING") {
			t.Fatalf("late IPN on superseded payment must NOT confirm order; got status=%q", ordAfterLate.Status)
		}
		if ordAfterLate.PaymentID != newPayID {
			t.Fatalf("late IPN overwrote active payment_id: got %q, want %q", ordAfterLate.PaymentID, newPayID)
		}
		_ = w3b44AssertLedgerBalanced(t, env)
		w3b44CancelOrderBestEffort(t, env, buyer.AccessToken, zaloOrdID, "w3b44 cleanup pending switched order")

		// 2C: Normal order VIETQR -> RetryPayment(COD) -> switched_to_cod=true, order CONFIRMED.
		vqrCo := w3b44CheckoutSingleItem(t, env, buyer, seller1, skuStd, 1, "VIETQR", quote, "")
		vqrOrdID := vqrCo.Orders[0].ID
		codRetryPath := fmt.Sprintf("/api/v1/payments/%s/retry", url.PathEscape(vqrCo.PaymentID))
		codSwitchResp := env.mustDoJSON(t, http.MethodPost, codRetryPath, buyer.AccessToken, map[string]string{
			"Idempotency-Key": "w3b44-retry-cod-" + uuid.NewString(),
		}, map[string]string{
			"method": "COD",
		}, http.StatusOK)
		var codSwitchOut w3b44RetryPaymentResponse
		if err := DecodeJSON(codSwitchResp, &codSwitchOut); err != nil {
			t.Fatalf("decode retry VIETQR->COD: %v", err)
		}
		if !codSwitchOut.SwitchedToCOD {
			t.Fatalf("expected switched_to_cod=true on VIETQR->COD switch, got body=%s", string(codSwitchResp.Body))
		}
		if !StatusEquals(codSwitchOut.Payment.Method, "COD") {
			t.Fatalf("expected switched payment method COD, got %q", codSwitchOut.Payment.Method)
		}
		if len(codSwitchOut.Orders) != 1 || !StatusEquals(codSwitchOut.Orders[0].Status, "CONFIRMED") {
			t.Fatalf("expected switched order status CONFIRMED in retry response, got %+v", codSwitchOut.Orders)
		}
		ordAfterCOD := env.getOrder(t, buyer.AccessToken, vqrOrdID)
		if !StatusEquals(ordAfterCOD.Status, "CONFIRMED") || !StatusEquals(ordAfterCOD.PaymentMethod, "COD") {
			t.Fatalf("GET order after VIETQR->COD switch: status=%q method=%q, want CONFIRMED/COD",
				ordAfterCOD.Status, ordAfterCOD.PaymentMethod)
		}
		if ordAfterCOD.TotalPayableVND != vqrCo.TotalPayableVND {
			t.Fatalf("VIETQR->COD changed total_payable_vnd: orig=%d after=%d", vqrCo.TotalPayableVND, ordAfterCOD.TotalPayableVND)
		}
		w3b44CancelOrderBestEffort(t, env, buyer.AccessToken, vqrOrdID, "w3b44 cleanup switched COD order")

		// 2D: Flash Sale VIETQR -> RetryPayment(COD) -> strictly 409 FAILED_PRECONDITION (FLASH_SALE_COD_NOT_ALLOWED).
		now := time.Now().UTC()
		fsResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/admin/flash-sales", adminTok, nil, CreateFlashSaleRequest{
			Name:     "W3B2 A6 Flash Retry Guard " + env.runID,
			StartsAt: now.Add(-5 * time.Minute).Format(time.RFC3339),
			EndsAt:   now.Add(45 * time.Minute).Format(time.RFC3339),
		}, http.StatusCreated)
		var fsOut FlashSaleResponse
		if err := DecodeJSON(fsResp, &fsOut); err != nil || fsOut.FlashSale.ID == "" {
			t.Fatalf("create flash sale: err=%v body=%s", err, string(fsResp.Body))
		}
		regItemPath := fmt.Sprintf("/api/v1/seller/flash-sales/%s/items", url.PathEscape(fsOut.FlashSale.ID))
		regItemResp := env.mustDoJSON(t, http.MethodPost, regItemPath, seller1.AccessToken, nil, RegisterFlashSaleItemRequest{
			SKUID:         skuFlash.ID,
			FlashPriceVND: 150000,
			TotalSlots:    10,
			PerUserLimit:  5,
		}, http.StatusCreated)
		var regItemOut FlashSaleItemResponse
		if err := DecodeJSON(regItemResp, &regItemOut); err != nil || regItemOut.Item.ID == "" {
			t.Fatalf("register flash sale item: err=%v body=%s", err, string(regItemResp.Body))
		}
		approvePath := fmt.Sprintf("/api/v1/admin/flash-sale-items/%s/approve", url.PathEscape(regItemOut.Item.ID))
		env.mustDoJSON(t, http.MethodPost, approvePath, adminTok, nil, ApproveFlashSaleItemRequest{
			Approve: true,
			Reason:  "w3b44 flash retry guard",
		}, http.StatusOK)

		fsCo := w3b44CheckoutSingleItem(t, env, buyer, seller1, skuFlash, 1, "VIETQR", quote, regItemOut.Item.ID)
		fsRetryPath := fmt.Sprintf("/api/v1/payments/%s/retry", url.PathEscape(fsCo.PaymentID))
		fsSwitchResp := env.mustDoJSON(t, http.MethodPost, fsRetryPath, buyer.AccessToken, map[string]string{
			"Idempotency-Key": "w3b44-retry-fs-cod-" + uuid.NewString(),
		}, map[string]string{
			"method": "COD",
		}, http.StatusConflict)
		w3b44AssertPreconditionPrefix(t, "flash sale retry to COD", fsSwitchResp, "FLASH_SALE_COD_NOT_ALLOWED")
		w3b44CancelOrderBestEffort(t, env, buyer.AccessToken, fsCo.Orders[0].ID, "w3b44 cleanup flash order")

		// 2E: Unverified phone buyer VIETQR -> RetryPayment(COD) -> strictly 409 COD_PHONE_NOT_VERIFIED.
		unverifiedBuyer := env.registerVerifyAndLogin(t, "w3b44-unverified")
		env.addAddress(t, unverifiedBuyer)
		uvCo := w3b44CheckoutSingleItem(t, env, unverifiedBuyer, seller1, skuStd, 1, "VIETQR", quote, "")
		uvRetryPath := fmt.Sprintf("/api/v1/payments/%s/retry", url.PathEscape(uvCo.PaymentID))
		uvSwitchResp := env.mustDoJSON(t, http.MethodPost, uvRetryPath, unverifiedBuyer.AccessToken, map[string]string{
			"Idempotency-Key": "w3b44-retry-unverified-cod-" + uuid.NewString(),
		}, map[string]string{
			"method": "COD",
		}, http.StatusConflict)
		w3b44AssertErrorCode(t, "unverified buyer retry to COD", uvSwitchResp, "COD_PHONE_NOT_VERIFIED")
		w3b44CancelOrderBestEffort(t, env, unverifiedBuyer.AccessToken, uvCo.Orders[0].ID, "w3b44 cleanup unverified order")

		// 2F: Over default 5,000,000 VND COD limit: VIETQR (5,400,000 VND) -> RetryPayment(COD) -> strictly 409 FAILED_PRECONDITION (COD_LIMIT_EXCEEDED).
		bigCo := w3b44CheckoutSingleItem(t, env, buyer, seller1, skuHigh, 9, "VIETQR", quote, "")
		bigRetryPath := fmt.Sprintf("/api/v1/payments/%s/retry", url.PathEscape(bigCo.PaymentID))
		bigSwitchResp := env.mustDoJSON(t, http.MethodPost, bigRetryPath, buyer.AccessToken, map[string]string{
			"Idempotency-Key": "w3b44-retry-overlimit-cod-" + uuid.NewString(),
		}, map[string]string{
			"method": "COD",
		}, http.StatusConflict)
		w3b44AssertPreconditionPrefix(t, "over-limit retry to COD", bigSwitchResp, "COD_LIMIT_EXCEEDED")
		w3b44CancelOrderBestEffort(t, env, buyer.AccessToken, bigCo.Orders[0].ID, "w3b44 cleanup over-limit order")
	})

	// 3. ZaloPay Refund (F4): cancelling a paid ZALOPAY order triggers automatic mock wallet refund -> refund reaches DONE, ledger balanced.
	t.Run("3_ZaloPayRefund_CancelPaidOrder_AutoRefundDone", func(t *testing.T) {
		co := w3b44CheckoutSingleItem(t, env, buyer, seller1, skuStd, 1, "ZALOPAY", quote, "")
		ordID := co.Orders[0].ID
		pay := env.getPayment(t, buyer.AccessToken, co.PaymentID)
		appTransID := strings.TrimSpace(pay.ProviderRef)
		if appTransID == "" {
			t.Fatalf("paid ZALOPAY refund test: missing provider_ref (app_trans_id)")
		}

		// Confirm payment via valid ZaloPay IPN (HTTP 200 + return_code=1).
		ipnReq, _ := w3b44BuildZaloPayCallback(t, env, appTransID, w3b44NextZPTransID(env), co.TotalPayableVND, 0, "")
		w3b44MustZaloPayCallback(t, env, "confirm zalopay before cancel", ipnReq, http.StatusOK, 1)

		env.pollUntil(t, env.cfg.PollTimeout, 250*time.Millisecond, "zalopay order CONFIRMED before cancel", func() (bool, string) {
			o := env.getOrder(t, buyer.AccessToken, ordID)
			return StatusEquals(o.Status, "CONFIRMED") && StatusEquals(o.PaymentStatus, "SUCCEEDED"),
				fmt.Sprintf("status=%s payment_status=%s", o.Status, o.PaymentStatus)
		})

		// Cancel paid ZALOPAY order -> automatic mock wallet refund -> DONE.
		cancelPath := fmt.Sprintf("/api/v1/orders/%s/cancel", url.PathEscape(ordID))
		cancelResp := env.mustDoJSON(t, http.MethodPost, cancelPath, buyer.AccessToken, nil, map[string]string{
			"reason": "w3b44 buyer cancel paid zalopay order",
		}, http.StatusOK)
		var cancelOut OrderResponse
		if err := DecodeJSON(cancelResp, &cancelOut); err != nil {
			t.Fatalf("decode cancel zalopay order: %v", err)
		}
		if !StatusEquals(cancelOut.Order.Status, "CANCELLED") {
			t.Fatalf("cancelled order status = %q, want CANCELLED", cancelOut.Order.Status)
		}

		var doneRefund RefundDTO
		env.pollUntil(t, 45*time.Second, 300*time.Millisecond, "zalopay auto-refund reaches DONE", func() (bool, string) {
			rfPath := fmt.Sprintf("/api/v1/admin/refunds?order_id=%s&page=1&page_size=20", url.QueryEscape(ordID))
			rfResp := env.mustDoJSON(t, http.MethodGet, rfPath, adminTok, nil, nil, http.StatusOK)
			var rfList w3b44RefundListResponse
			if err := DecodeJSON(rfResp, &rfList); err != nil {
				return false, err.Error()
			}
			for _, rf := range rfList.Items {
				if rf.OrderID == ordID && StatusEquals(rf.Status, "DONE") {
					doneRefund = rf
					return true, ""
				}
			}
			if len(rfList.Items) > 0 {
				return false, fmt.Sprintf("refund %s status=%s provider_status=%s m_refund_id=%s",
					rfList.Items[0].RefundID, rfList.Items[0].Status, rfList.Items[0].ProviderStatus, rfList.Items[0].ProviderMRefundID)
			}
			return false, "no refund row created yet"
		})
		if doneRefund.AmountVND != co.TotalPayableVND {
			t.Fatalf("zalopay auto-refund amount = %d, want %d", doneRefund.AmountVND, co.TotalPayableVND)
		}
		if strings.TrimSpace(doneRefund.ProviderMRefundID) == "" {
			t.Fatalf("zalopay auto-refund missing provider_m_refund_id (F4): %+v", doneRefund)
		}
		_ = w3b44AssertLedgerBalanced(t, env)
	})

	// 4. Payment method configs (UC-A13 / F6-F7):
	//    - public GET /payment-methods hides admin metadata; admin GET never leaks plaintext keys;
	//    - PUT with wrong expected_version -> 409 VERSION_CONFLICT;
	//    - disable ZALOPAY, lower COD max=500k, compute VIETQR min directly from real order totals
	//      so below-min fails 409 ORDER_AMOUNT_OUT_OF_RANGE, above-min succeeds 201, and 0 VND (voucher+coins) succeeds 201;
	//    - in-flight ZALOPAY order created before disable still confirms on IPN (HTTP 200 + return_code=1);
	//    - disabling the last enabled method -> 400;
	//    - t.Cleanup always restores original method configs even if test fails.
	t.Run("4_PaymentMethodConfig_OptimisticLock_Limits_ZeroVND_InFlightIPN_LastMethod", func(t *testing.T) {
		// Verify public GET /api/v1/payment-methods exposes public fields only (F6/F8).
		pubResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/payment-methods", "", nil, nil, http.StatusOK)
		pubBody := string(pubResp.Body)
		for _, forbiddenKey := range []string{`"credential_preview"`, `"credentials_configured"`, `"updated_by"`, `"note"`} {
			if strings.Contains(pubBody, forbiddenKey) {
				t.Fatalf("public GET /api/v1/payment-methods leaked admin field %s: %s", forbiddenKey, pubBody)
			}
		}

		// Snapshot original admin configs and register cleanup BEFORE any mutation.
		adminRawResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/payment-methods", adminTok, nil, nil, http.StatusOK)
		adminBody := string(adminRawResp.Body)
		for _, secret := range []string{env.cfg.ZaloPayKey1, env.cfg.ZaloPayKey2} {
			if secret != "" && strings.Contains(adminBody, secret) {
				t.Fatalf("admin GET /api/v1/admin/payment-methods leaked plaintext secret!")
			}
		}
		origConfigs := w3b44GetAdminPaymentMethods(t, env, adminTok)
		for _, requiredMethod := range []string{"COD", "VIETQR", "ZALOPAY"} {
			if _, ok := origConfigs[requiredMethod]; !ok {
				t.Fatalf("admin payment-methods missing %s: %+v", requiredMethod, origConfigs)
			}
		}
		t.Cleanup(func() {
			w3b44RestorePaymentMethods(t, env, adminTok, origConfigs)
		})

		// Create in-flight ZALOPAY order and pending VIETQR order BEFORE disabling ZALOPAY.
		inflightCo := w3b44CheckoutSingleItem(t, env, buyer, seller1, skuStd, 1, "ZALOPAY", quote, "")
		inflightOrdID := inflightCo.Orders[0].ID
		inflightPay := env.getPayment(t, buyer.AccessToken, inflightCo.PaymentID)
		inflightAppTransID := strings.TrimSpace(inflightPay.ProviderRef)
		if inflightAppTransID == "" {
			t.Fatalf("in-flight ZALOPAY payment missing provider_ref")
		}
		retryBaseCo := w3b44CheckoutSingleItem(t, env, buyer, seller1, skuStd, 1, "VIETQR", quote, "")

		// Wrong expected_version -> strictly 409 VERSION_CONFLICT.
		origZalo := origConfigs["ZALOPAY"]
		badVerResp := env.mustDoJSON(t, http.MethodPut, "/api/v1/admin/payment-methods/ZALOPAY", adminTok, nil, w3b44PaymentMethodUpdateRequest{
			Enabled:         false,
			MinOrderVND:     origZalo.MinOrderVND,
			MaxOrderVND:     origZalo.MaxOrderVND,
			ExpiryMinutes:   origZalo.ExpiryMinutes,
			Note:            "w3b44 bad version probe",
			ExpectedVersion: origZalo.Version + 99,
		}, http.StatusConflict)
		w3b44AssertErrorCode(t, "payment-method wrong version", badVerResp, "VERSION_CONFLICT")

		// Disable ZALOPAY with correct version.
		zaloPutResp := env.mustDoJSON(t, http.MethodPut, "/api/v1/admin/payment-methods/ZALOPAY", adminTok, nil, w3b44PaymentMethodUpdateRequest{
			Enabled:         false,
			MinOrderVND:     origZalo.MinOrderVND,
			MaxOrderVND:     origZalo.MaxOrderVND,
			ExpiryMinutes:   origZalo.ExpiryMinutes,
			Note:            "w3b44 disable zalopay",
			ExpectedVersion: origZalo.Version,
		}, http.StatusOK)
		var zaloUpdated w3b44PaymentMethodUpdateResponse
		if err := DecodeJSON(zaloPutResp, &zaloUpdated); err != nil {
			t.Fatalf("decode disable ZALOPAY: %v", err)
		}
		if zaloUpdated.Method.Enabled || zaloUpdated.Method.Version <= origZalo.Version {
			t.Fatalf("disable ZALOPAY unexpected result: %+v (orig version=%d)", zaloUpdated.Method, origZalo.Version)
		}

		// Lower COD max_order_vnd to 500,000 VND.
		origCOD := origConfigs["COD"]
		codPutResp := env.mustDoJSON(t, http.MethodPut, "/api/v1/admin/payment-methods/COD", adminTok, nil, w3b44PaymentMethodUpdateRequest{
			Enabled:         true,
			MinOrderVND:     origCOD.MinOrderVND,
			MaxOrderVND:     500000,
			ExpiryMinutes:   origCOD.ExpiryMinutes,
			Note:            "w3b44 lower COD max to 500k",
			ExpectedVersion: origCOD.Version,
		}, http.StatusOK)
		var codUpdated w3b44PaymentMethodUpdateResponse
		if err := DecodeJSON(codPutResp, &codUpdated); err != nil {
			t.Fatalf("decode update COD max: %v", err)
		}

		// Compute VIETQR min_order_vnd strictly between the two real order totals (underTotal < vietqrMin < passTotal).
		smallQuote := env.calculateShippingQuote(t, buyer.AccessToken, seller1.ShopID, 300, 0)
		underTotal := skuSmall.PriceVND + smallQuote.FeeVND
		passTotal := skuStd.PriceVND + quote.FeeVND
		if passTotal <= underTotal+2000 {
			t.Fatalf("expected std order total (%d) > small order total (%d) + 2000", passTotal, underTotal)
		}
		vietqrMin := (underTotal + passTotal) / 2

		origVietQR := origConfigs["VIETQR"]
		vqrPutResp := env.mustDoJSON(t, http.MethodPut, "/api/v1/admin/payment-methods/VIETQR", adminTok, nil, w3b44PaymentMethodUpdateRequest{
			Enabled:         true,
			MinOrderVND:     vietqrMin,
			MaxOrderVND:     origVietQR.MaxOrderVND,
			ExpiryMinutes:   origVietQR.ExpiryMinutes,
			Note:            "w3b44 set VIETQR min from real order totals",
			ExpectedVersion: origVietQR.Version,
		}, http.StatusOK)
		var vqrUpdated w3b44PaymentMethodUpdateResponse
		if err := DecodeJSON(vqrPutResp, &vqrUpdated); err != nil {
			t.Fatalf("decode update VIETQR min: %v", err)
		}

		// New ZALOPAY checkout -> strictly 409 PAYMENT_METHOD_DISABLED.
		disCoResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "w3b44-dis-zalo-" + uuid.NewString(),
		}, CheckoutRequest{
			SalesChannel:  "MARKETPLACE",
			AddressID:     buyer.AddressID,
			Items:         []CheckoutItemInputDTO{{SKUID: skuStd.ID, Quantity: 1}},
			PaymentMethod: "ZALOPAY",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
			},
		}, http.StatusConflict)
		w3b44AssertErrorCode(t, "checkout disabled ZALOPAY", disCoResp, "PAYMENT_METHOD_DISABLED")

		// Retry switching to disabled ZALOPAY -> strictly 409 PAYMENT_METHOD_DISABLED.
		disRetryPath := fmt.Sprintf("/api/v1/payments/%s/retry", url.PathEscape(retryBaseCo.PaymentID))
		disRetryResp := env.mustDoJSON(t, http.MethodPost, disRetryPath, buyer.AccessToken, map[string]string{
			"Idempotency-Key": "w3b44-dis-retry-zalo-" + uuid.NewString(),
		}, map[string]string{
			"method": "ZALOPAY",
		}, http.StatusConflict)
		w3b44AssertErrorCode(t, "retry switch to disabled ZALOPAY", disRetryResp, "PAYMENT_METHOD_DISABLED")
		w3b44CancelOrderBestEffort(t, env, buyer.AccessToken, retryBaseCo.Orders[0].ID, "w3b44 cleanup retry base order")

		// COD checkout exceeding lowered 500,000 VND max -> strictly 409 FAILED_PRECONDITION (COD_LIMIT_EXCEEDED).
		codOverResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "w3b44-cod-over500k-" + uuid.NewString(),
		}, CheckoutRequest{
			SalesChannel:  "MARKETPLACE",
			AddressID:     buyer.AddressID,
			Items:         []CheckoutItemInputDTO{{SKUID: skuHigh.ID, Quantity: 1}},
			PaymentMethod: "COD",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
			},
		}, http.StatusConflict)
		w3b44AssertPreconditionPrefix(t, "COD over lowered 500k max", codOverResp, "COD_LIMIT_EXCEEDED")

		// VIETQR checkout strictly below vietqrMin -> strictly 409 ORDER_AMOUNT_OUT_OF_RANGE.
		vqrUnderResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "w3b44-vqr-undermin-" + uuid.NewString(),
		}, CheckoutRequest{
			SalesChannel:  "MARKETPLACE",
			AddressID:     buyer.AddressID,
			Items:         []CheckoutItemInputDTO{{SKUID: skuSmall.ID, Quantity: 1}},
			PaymentMethod: "VIETQR",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: smallQuote.CarrierCode, ServiceCode: smallQuote.ServiceCode},
			},
		}, http.StatusConflict)
		w3b44AssertErrorCode(t, "VIETQR below min_order_vnd", vqrUnderResp, "ORDER_AMOUNT_OUT_OF_RANGE")

		// VIETQR checkout strictly above vietqrMin -> strictly 201 Created.
		vqrPassCo := w3b44CheckoutSingleItem(t, env, buyer, seller1, skuStd, 1, "VIETQR", quote, "")
		if vqrPassCo.TotalPayableVND < vietqrMin {
			t.Fatalf("pass order total %d unexpectedly below vietqrMin %d", vqrPassCo.TotalPayableVND, vietqrMin)
		}
		w3b44CancelOrderBestEffort(t, env, buyer.AccessToken, vqrPassCo.Orders[0].ID, "w3b44 cleanup above-min order")

		// 0 VND order fully covered by voucher + freeship + coins while VIETQR min_order_vnd > 0 -> MUST succeed (201, CONFIRMED, SUCCEEDED).
		freeshipV := env.createAdminVoucher(t, "FREESHIP", smallQuote.FeeVND+10000, 0, 5)
		platV := env.createAdminVoucher(t, "PLATFORM", 6000, 0, 5)
		env.saveVoucher(t, buyer.AccessToken, freeshipV)
		env.saveVoucher(t, buyer.AccessToken, platV)
		env.creditCoins(t, buyer.UserID, 10000, "w3b44_zero_order_min_exempt")

		zeroResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "w3b44-zero-min-exempt-" + uuid.NewString(),
		}, CheckoutRequest{
			SalesChannel:      "MARKETPLACE",
			AddressID:         buyer.AddressID,
			Items:             []CheckoutItemInputDTO{{SKUID: skuSmall.ID, Quantity: 1}},
			PlatformVoucherID: platV.ID,
			FreeshipVoucherID: freeshipV.ID,
			CoinsToUse:        4000,
			PaymentMethod:     "VIETQR",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: smallQuote.CarrierCode, ServiceCode: smallQuote.ServiceCode},
			},
		}, http.StatusCreated)
		var zeroCo CheckoutResponse
		if err := DecodeJSON(zeroResp, &zeroCo); err != nil {
			t.Fatalf("decode 0 VND checkout while min>0: %v", err)
		}
		if zeroCo.TotalPayableVND != 0 {
			t.Fatalf("expected 0 VND checkout total_payable_vnd=0, got %d", zeroCo.TotalPayableVND)
		}
		if len(zeroCo.Orders) != 1 || !StatusEquals(zeroCo.Orders[0].Status, "CONFIRMED") || !StatusEquals(zeroCo.Orders[0].PaymentStatus, "SUCCEEDED") {
			t.Fatalf("0 VND checkout expected CONFIRMED/SUCCEEDED, got %+v", zeroCo.Orders)
		}
		zeroPay := env.getPayment(t, buyer.AccessToken, zeroCo.PaymentID)
		if !StatusEquals(zeroPay.Status, "SUCCEEDED") || zeroPay.AmountVND != 0 {
			t.Fatalf("0 VND payment expected SUCCEEDED/0, got status=%q amount=%d", zeroPay.Status, zeroPay.AmountVND)
		}
		w3b44CancelOrderBestEffort(t, env, buyer.AccessToken, zeroCo.Orders[0].ID, "w3b44 cleanup zero-vnd order")

		// In-flight ZALOPAY order created BEFORE disable still receives valid IPN and confirms (HTTP 200 + return_code=1, F7).
		inflightIPN, _ := w3b44BuildZaloPayCallback(t, env, inflightAppTransID, w3b44NextZPTransID(env), inflightCo.TotalPayableVND, 0, "")
		w3b44MustZaloPayCallback(t, env, "in-flight ZALOPAY IPN while disabled", inflightIPN, http.StatusOK, 1)

		env.pollUntil(t, env.cfg.PollTimeout, 250*time.Millisecond, "in-flight ZALOPAY order CONFIRMED while method disabled", func() (bool, string) {
			o := env.getOrder(t, buyer.AccessToken, inflightOrdID)
			return StatusEquals(o.Status, "CONFIRMED") && StatusEquals(o.PaymentStatus, "SUCCEEDED"),
				fmt.Sprintf("status=%s payment_status=%s", o.Status, o.PaymentStatus)
		})
		_ = w3b44AssertLedgerBalanced(t, env)

		// Disable COD (leaving VIETQR as the only enabled method), then trying to disable VIETQR -> strictly 400.
		env.mustDoJSON(t, http.MethodPut, "/api/v1/admin/payment-methods/COD", adminTok, nil, w3b44PaymentMethodUpdateRequest{
			Enabled:         false,
			MinOrderVND:     codUpdated.Method.MinOrderVND,
			MaxOrderVND:     codUpdated.Method.MaxOrderVND,
			ExpiryMinutes:   codUpdated.Method.ExpiryMinutes,
			Note:            "w3b44 disable COD to test last-method guard",
			ExpectedVersion: codUpdated.Method.Version,
		}, http.StatusOK)

		env.mustDoJSON(t, http.MethodPut, "/api/v1/admin/payment-methods/VIETQR", adminTok, nil, w3b44PaymentMethodUpdateRequest{
			Enabled:         false,
			MinOrderVND:     vqrUpdated.Method.MinOrderVND,
			MaxOrderVND:     vqrUpdated.Method.MaxOrderVND,
			ExpiryMinutes:   vqrUpdated.Method.ExpiryMinutes,
			Note:            "w3b44 attempt disable last enabled method",
			ExpectedVersion: vqrUpdated.Method.Version,
		}, http.StatusBadRequest)

		// Verify payment-method config updates were recorded in admin audit logs (F7).
		env.pollUntil(t, env.cfg.PollTimeout, 250*time.Millisecond, "payment_method_config audit log recorded", func() (bool, string) {
			audResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/audit-logs?target_type=payment_method_config&page=1&page_size=20",
				adminTok, nil, nil, http.StatusOK)
			var audOut struct {
				Total int64 `json:"total"`
			}
			if err := DecodeJSON(audResp, &audOut); err != nil {
				return false, err.Error()
			}
			return audOut.Total > 0, fmt.Sprintf("total=%d", audOut.Total)
		})
	})
}
