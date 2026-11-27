package e2e

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type payoutSellerBalanceResp struct {
	ShopID           string `json:"shop_id"`
	PendingVND       int64  `json:"pending_vnd"`
	AvailableVND     int64  `json:"available_vnd"`
	TotalEarnedVND   int64  `json:"total_earned_vnd"`
	PayoutPendingVND int64  `json:"payout_pending_vnd"`
}

type payoutDTO struct {
	ID                string `json:"id"`
	ShopID            string `json:"shop_id"`
	AmountVND         int64  `json:"amount_vnd"`
	Status            string `json:"status"`
	BankName          string `json:"bank_name"`
	BankAccountMasked string `json:"bank_account_masked"`
	AccountHolderName string `json:"account_holder_name"`
	RejectReason      string `json:"reject_reason"`
	TransferRef       string `json:"transfer_ref"`
	RequestedBy       string `json:"requested_by"`
	DecidedBy         string `json:"decided_by"`
	CreatedAt         string `json:"created_at"`
	DecidedAt         string `json:"decided_at"`
}

type payoutWrapResp struct {
	Payout payoutDTO `json:"payout"`
}

type payoutListResp struct {
	Items    []payoutDTO `json:"items"`
	Page     int32       `json:"page"`
	PageSize int32       `json:"page_size"`
	Total    int64       `json:"total"`
}

type payoutErrorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	AvailableVND *int64 `json:"available_vnd"`
}

func payoutGetSellerBalance(t *testing.T, env *testEnv, seller *sellerFixture) payoutSellerBalanceResp {
	t.Helper()
	resp := env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/balance", seller.AccessToken, nil, nil, http.StatusOK)
	var out payoutSellerBalanceResp
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode seller balance for %s: %v", seller.ShopID, err)
	}
	return out
}

// TestE2E_SellerPayouts_TwoStepFlow_BankChecks_And_RBAC verifies UC-PAY05 end-to-end:
// - Seller balance moves pending -> available after order completion + escrow release
// - Request before verified bank account -> 412 BANK_ACCOUNT_NOT_READY
// - Bank account OTP verify -> 400 validation, 409 INSUFFICIENT_BALANCE (with available_vnd)
// - Valid request -> 201 PENDING + hold ledger posting, replay idempotency, 1 PENDING/shop -> 409 PAYOUT_PENDING_EXISTS
// - RBAC checks on REST + direct gRPC (buyer, cross-shop seller, seller empty shop_id)
// - Admin REJECT (requires reason) restores available_vnd & rejects double decision (409)
// - Admin APPROVE settles hold -> gateway_clearing with MOCKBANK-* transfer_ref & rejects double decision (409)
// - Changing verified bank account triggers 24h lock -> 412 BANK_ACCOUNT_NOT_READY
func TestE2E_SellerPayouts_TwoStepFlow_BankChecks_And_RBAC(t *testing.T) {
	env := getEnv()
	adminTok := env.adminLogin(t)
	payGRPC := payW2DialGRPC(t, env)

	cat := env.ensureCategory(t)
	seller := usrW2BCreateApprovedSellerWithName(t, env, "payout-seller", "Trần Thị Minh Châu")
	otherSeller := env.createApprovedSeller(t, "payout-other")
	buyer := env.registerVerifyAndLogin(t, "payout-buyer")
	env.addAddress(t, buyer)

	_, sku := env.createApprovedProduct(t, seller, cat.ID, "UC-PAY05 Payout Test Item", 300000, 20, 500)
	quote := env.calculateShippingQuote(t, buyer.AccessToken, seller.ShopID, 500, 0)

	// 1. Complete a VietQR order and release escrow so seller has real available_vnd.
	co := payW2CreateVietQRCheckout(t, env, buyer, seller, sku, quote, "payout-co")
	ord := co.Orders[0]
	sellerOrd := env.getOrder(t, seller.AccessToken, ord.ID)
	wantSellerNet := sellerOrd.SubtotalVND - sellerOrd.CommissionVND - sellerOrd.PaymentFeeVND
	if wantSellerNet < 200000 {
		t.Fatalf("expected seller net >= 200000 VND for test scenarios, got %d", wantSellerNet)
	}

	payW2SendVietQRWebhook(t, env, co.PaymentID, co.CheckoutGroupID, "PAYOUT-TX-"+uuid.NewString(), co.TotalPayableVND)
	env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "order CONFIRMED", func() (bool, string) {
		o := env.getOrder(t, buyer.AccessToken, ord.ID)
		return StatusEquals(o.Status, "CONFIRMED"), o.Status
	})

	payW2ShipAndDeliver(t, env, seller, buyer, ord.ID, quote)

	confirmRecPath := fmt.Sprintf("/api/v1/orders/%s/confirm-received", url.PathEscape(ord.ID))
	env.mustDoJSON(t, http.MethodPost, confirmRecPath, buyer.AccessToken, nil, nil, http.StatusOK)

	env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "seller pending_vnd credited", func() (bool, string) {
		b := payoutGetSellerBalance(t, env, seller)
		return b.PendingVND == wantSellerNet, fmt.Sprintf("pending=%d want=%d", b.PendingVND, wantSellerNet)
	})

	if _, err := payGRPC.ReleaseEscrow(payW2AuthCtx(env.ctx(t), adminTok), &paymentv1.ReleaseEscrowRequest{
		OrderId: ord.ID,
		ShopId:  seller.ShopID,
	}); err != nil {
		t.Fatalf("admin ReleaseEscrow failed: %v", err)
	}

	balReady := payoutGetSellerBalance(t, env, seller)
	if balReady.AvailableVND != wantSellerNet || balReady.PendingVND != 0 || balReady.PayoutPendingVND != 0 {
		t.Fatalf("unexpected seller balance after escrow release: %+v (want available=%d)", balReady, wantSellerNet)
	}

	// 2. Request payout before verified bank account -> 412 BANK_ACCOUNT_NOT_READY.
	noBankResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/payouts", seller.AccessToken, map[string]string{
		"Idempotency-Key": "payout-nobank-" + uuid.NewString(),
	}, map[string]any{
		"amount_vnd": int64(50000),
	}, http.StatusPreconditionFailed)
	var noBankErr payoutErrorEnvelope
	if err := DecodeJSON(noBankResp, &noBankErr); err != nil {
		t.Fatalf("decode 412 no-bank error: %v", err)
	}
	if noBankErr.Error.Code != "BANK_ACCOUNT_NOT_READY" {
		t.Fatalf("expected BANK_ACCOUNT_NOT_READY, got %q (%s)", noBankErr.Error.Code, string(noBankResp.Body))
	}

	// 3. Add and OTP-verify shop bank account (matching owner name -> VERIFIED immediately, no 24h lock).
	seenMail := usrW2BSnapshotMailpitIDs(t, env)
	rawAcc1 := "19039988776655"
	addResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/bank-account", seller.AccessToken, nil, usrW2BAddBankAccountReq{
		BankCode:          "VCB",
		BankName:          "Vietcombank",
		AccountNumber:     rawAcc1,
		AccountHolderName: "TRAN THI MINH CHAU",
	}, http.StatusCreated, http.StatusOK)
	usrW2BAssertNoRawLeak(t, "POST /seller/bank-account", addResp.Body, rawAcc1)

	_, otp1 := usrW2BWaitNewOTPEmail(t, env, seller.Email, seenMail)
	verResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/bank-account/verify-otp", seller.AccessToken, nil, usrW2BVerifyBankOTPReq{
		OTP: otp1,
	}, http.StatusOK)
	usrW2BAssertNoRawLeak(t, "verify-otp #1", verResp.Body, rawAcc1, otp1)
	var verOut usrW2BBankAccountResp
	if err := DecodeJSON(verResp, &verOut); err != nil {
		t.Fatalf("decode verify bank account: %v", err)
	}
	if !StatusEquals(verOut.BankAccount.Status, "VERIFIED") || verOut.BankAccount.PayoutLockedUntil != "" {
		t.Fatalf("expected immediately VERIFIED bank account with no lock, got %+v", verOut.BankAccount)
	}

	// 4. Input validation & insufficient balance checks.
	env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/payouts", seller.AccessToken, nil, map[string]any{
		"amount_vnd": int64(50000),
	}, http.StatusBadRequest)
	env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/payouts", seller.AccessToken, map[string]string{
		"Idempotency-Key": "payout-zero-" + uuid.NewString(),
	}, map[string]any{
		"amount_vnd": int64(0),
	}, http.StatusBadRequest)
	env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/payouts", seller.AccessToken, map[string]string{
		"Idempotency-Key": "payout-neg-" + uuid.NewString(),
	}, map[string]any{
		"amount_vnd": int64(-1000),
	}, http.StatusBadRequest)

	insuffResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/payouts", seller.AccessToken, map[string]string{
		"Idempotency-Key": "payout-insuff-" + uuid.NewString(),
	}, map[string]any{
		"amount_vnd": wantSellerNet + 10000,
	}, http.StatusConflict)
	var insuffErr payoutErrorEnvelope
	if err := DecodeJSON(insuffResp, &insuffErr); err != nil {
		t.Fatalf("decode insufficient balance error: %v", err)
	}
	if insuffErr.Error.Code != "INSUFFICIENT_BALANCE" || insuffErr.AvailableVND == nil || *insuffErr.AvailableVND != wantSellerNet {
		t.Fatalf("expected INSUFFICIENT_BALANCE with available_vnd=%d, got code=%q avail=%v body=%s",
			wantSellerNet, insuffErr.Error.Code, insuffErr.AvailableVND, string(insuffResp.Body))
	}

	// 5. RBAC checks (REST & direct gRPC).
	env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/payouts", buyer.AccessToken, nil, nil, http.StatusForbidden)
	env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/payouts", buyer.AccessToken, map[string]string{
		"Idempotency-Key": "payout-buyer-" + uuid.NewString(),
	}, map[string]any{"amount_vnd": int64(10000)}, http.StatusForbidden)
	env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/payouts", buyer.AccessToken, nil, nil, http.StatusForbidden)
	env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/payouts", seller.AccessToken, nil, nil, http.StatusForbidden)

	if _, err := payGRPC.RequestSellerPayout(payW2AuthCtx(env.ctx(t), otherSeller.AccessToken), &paymentv1.RequestSellerPayoutRequest{
		ShopId:         seller.ShopID,
		AmountVnd:      50000,
		IdempotencyKey: "payout-xshop-" + uuid.NewString(),
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cross-shop RequestSellerPayout: want PermissionDenied, got %v", err)
	}
	if _, err := payGRPC.ListSellerPayouts(payW2AuthCtx(env.ctx(t), seller.AccessToken), &paymentv1.ListSellerPayoutsRequest{
		ShopId: "",
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("seller empty-shop ListSellerPayouts: want PermissionDenied, got %v", err)
	}
	if _, err := payGRPC.ListSellerPayouts(payW2AuthCtx(env.ctx(t), otherSeller.AccessToken), &paymentv1.ListSellerPayoutsRequest{
		ShopId: seller.ShopID,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cross-shop ListSellerPayouts: want PermissionDenied, got %v", err)
	}

	// 6. Request payout #1 (PENDING), verify replay idempotency, 1 PENDING/shop guard, balance & ledger.
	req1Amount := int64(100000)
	req1Key := "payout-req1-" + uuid.NewString()
	req1Resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/payouts", seller.AccessToken, map[string]string{
		"Idempotency-Key": req1Key,
	}, map[string]any{
		"amount_vnd": req1Amount,
	}, http.StatusCreated)
	usrW2BAssertNoRawLeak(t, "POST /seller/payouts #1", req1Resp.Body, rawAcc1)

	var p1Wrap payoutWrapResp
	if err := DecodeJSON(req1Resp, &p1Wrap); err != nil {
		t.Fatalf("decode payout #1: %v", err)
	}
	p1 := p1Wrap.Payout
	if p1.ID == "" || p1.ShopID != seller.ShopID || p1.AmountVND != req1Amount || p1.Status != "PENDING" ||
		p1.BankName != "Vietcombank" || p1.BankAccountMasked != "****6655" || p1.AccountHolderName == "" ||
		p1.TransferRef != "" || p1.DecidedAt != "" {
		t.Fatalf("unexpected payout #1 DTO: %+v", p1)
	}

	// Replay same Idempotency-Key -> returns identical payout without double hold.
	replayResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/payouts", seller.AccessToken, map[string]string{
		"Idempotency-Key": req1Key,
	}, map[string]any{
		"amount_vnd": req1Amount,
	}, http.StatusCreated, http.StatusOK)
	var replayWrap payoutWrapResp
	if err := DecodeJSON(replayResp, &replayWrap); err != nil {
		t.Fatalf("decode payout #1 replay: %v", err)
	}
	if replayWrap.Payout.ID != p1.ID || replayWrap.Payout.Status != "PENDING" {
		t.Fatalf("idempotent replay mismatch: got %+v want id=%s", replayWrap.Payout, p1.ID)
	}

	// Second distinct request while #1 is PENDING -> 409 PAYOUT_PENDING_EXISTS.
	dupPendingResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/payouts", seller.AccessToken, map[string]string{
		"Idempotency-Key": "payout-dup-pending-" + uuid.NewString(),
	}, map[string]any{
		"amount_vnd": int64(20000),
	}, http.StatusConflict)
	var dupPendingErr payoutErrorEnvelope
	if err := DecodeJSON(dupPendingResp, &dupPendingErr); err != nil {
		t.Fatalf("decode dup pending error: %v", err)
	}
	if dupPendingErr.Error.Code != "PAYOUT_PENDING_EXISTS" {
		t.Fatalf("expected PAYOUT_PENDING_EXISTS, got %q (%s)", dupPendingErr.Error.Code, string(dupPendingResp.Body))
	}

	// Verify seller balance reflects held amount & ledger stays balanced.
	balHeld := payoutGetSellerBalance(t, env, seller)
	if balHeld.AvailableVND != wantSellerNet-req1Amount || balHeld.PayoutPendingVND != req1Amount {
		t.Fatalf("expected available=%d payout_pending=%d after hold, got %+v",
			wantSellerNet-req1Amount, req1Amount, balHeld)
	}
	summaryAfterHold := payW2AssertLedgerBalanced(t, env)
	if holdAcc, ok := payW2FindAccount(summaryAfterHold, "seller_payout_hold:"+seller.ShopID); !ok || holdAcc.BalanceVND != req1Amount {
		t.Fatalf("expected seller_payout_hold:%s=%d, got ok=%v acc=%+v", seller.ShopID, req1Amount, ok, holdAcc)
	}

	// Verify seller list & admin list visibility.
	sellerListResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/payouts?page=1&page_size=20", seller.AccessToken, nil, nil, http.StatusOK)
	usrW2BAssertNoRawLeak(t, "GET /seller/payouts", sellerListResp.Body, rawAcc1)
	var sellerList payoutListResp
	if err := DecodeJSON(sellerListResp, &sellerList); err != nil {
		t.Fatalf("decode seller payout list: %v", err)
	}
	if sellerList.Total != 1 || len(sellerList.Items) != 1 || sellerList.Items[0].ID != p1.ID {
		t.Fatalf("unexpected seller payout list: %+v", sellerList)
	}

	otherListResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/payouts?page=1&page_size=20", otherSeller.AccessToken, nil, nil, http.StatusOK)
	var otherList payoutListResp
	if err := DecodeJSON(otherListResp, &otherList); err != nil {
		t.Fatalf("decode otherSeller payout list: %v", err)
	}
	if otherList.Total != 0 || len(otherList.Items) != 0 {
		t.Fatalf("otherSeller must not see seller payouts: %+v", otherList)
	}

	adminPendingPath := fmt.Sprintf("/api/v1/admin/payouts?status=PENDING&shop_id=%s&page=1&page_size=20", url.QueryEscape(seller.ShopID))
	adminPendingResp := env.mustDoJSON(t, http.MethodGet, adminPendingPath, adminTok, nil, nil, http.StatusOK)
	usrW2BAssertNoRawLeak(t, "GET /admin/payouts", adminPendingResp.Body, rawAcc1)
	var adminPendingList payoutListResp
	if err := DecodeJSON(adminPendingResp, &adminPendingList); err != nil {
		t.Fatalf("decode admin pending payout list: %v", err)
	}
	if adminPendingList.Total != 1 || len(adminPendingList.Items) != 1 || adminPendingList.Items[0].ID != p1.ID {
		t.Fatalf("unexpected admin pending payout list: %+v", adminPendingList)
	}

	// Non-admin seller cannot decide payout.
	decide1Path := fmt.Sprintf("/api/v1/admin/payouts/%s/decision", url.PathEscape(p1.ID))
	env.mustDoJSON(t, http.MethodPost, decide1Path, seller.AccessToken, nil, map[string]string{
		"decision": "APPROVE",
	}, http.StatusForbidden)
	env.mustDoJSON(t, http.MethodPost, decide1Path, buyer.AccessToken, nil, map[string]string{
		"decision": "APPROVE",
	}, http.StatusForbidden)

	// 7. Admin REJECT payout #1: empty reason -> 400; valid reason -> 200 REJECTED + balance restored.
	env.mustDoJSON(t, http.MethodPost, decide1Path, adminTok, nil, map[string]string{
		"decision": "REJECT",
		"reason":   "   ",
	}, http.StatusBadRequest)

	rejectResp := env.mustDoJSON(t, http.MethodPost, decide1Path, adminTok, nil, map[string]string{
		"decision": "REJECT",
		"reason":   "Kiểm tra lại thông tin tài khoản",
	}, http.StatusOK)
	var rejWrap payoutWrapResp
	if err := DecodeJSON(rejectResp, &rejWrap); err != nil {
		t.Fatalf("decode reject payout #1: %v", err)
	}
	if rejWrap.Payout.Status != "REJECTED" || rejWrap.Payout.RejectReason != "Kiểm tra lại thông tin tài khoản" ||
		rejWrap.Payout.DecidedBy == "" || rejWrap.Payout.DecidedAt == "" {
		t.Fatalf("unexpected rejected payout #1 DTO: %+v", rejWrap.Payout)
	}

	balRestored := payoutGetSellerBalance(t, env, seller)
	if balRestored.AvailableVND != wantSellerNet || balRestored.PayoutPendingVND != 0 {
		t.Fatalf("expected balance fully restored after reject (available=%d pending=0), got %+v", wantSellerNet, balRestored)
	}

	// Double decision on REJECTED payout -> 409 FAILED_PRECONDITION.
	env.mustDoJSON(t, http.MethodPost, decide1Path, adminTok, nil, map[string]string{
		"decision": "APPROVE",
	}, http.StatusConflict)
	payW2AssertLedgerBalanced(t, env)

	// 8. Request payout #2 & Admin APPROVE -> COMPLETED with MOCKBANK-* transfer_ref.
	req2Amount := int64(150000)
	req2Resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/payouts", seller.AccessToken, map[string]string{
		"Idempotency-Key": "payout-req2-" + uuid.NewString(),
	}, map[string]any{
		"amount_vnd": req2Amount,
	}, http.StatusCreated)
	var p2Wrap payoutWrapResp
	if err := DecodeJSON(req2Resp, &p2Wrap); err != nil {
		t.Fatalf("decode payout #2: %v", err)
	}
	p2 := p2Wrap.Payout
	if p2.ID == "" || p2.ID == p1.ID || p2.Status != "PENDING" {
		t.Fatalf("unexpected payout #2 DTO: %+v", p2)
	}

	decide2Path := fmt.Sprintf("/api/v1/admin/payouts/%s/decision", url.PathEscape(p2.ID))
	approveResp := env.mustDoJSON(t, http.MethodPost, decide2Path, adminTok, nil, map[string]string{
		"decision": "APPROVE",
	}, http.StatusOK)
	var appWrap payoutWrapResp
	if err := DecodeJSON(approveResp, &appWrap); err != nil {
		t.Fatalf("decode approve payout #2: %v", err)
	}
	if appWrap.Payout.Status != "COMPLETED" || !strings.HasPrefix(appWrap.Payout.TransferRef, "MOCKBANK-") ||
		appWrap.Payout.DecidedBy == "" || appWrap.Payout.DecidedAt == "" {
		t.Fatalf("unexpected approved payout #2 DTO: %+v", appWrap.Payout)
	}

	balFinal := payoutGetSellerBalance(t, env, seller)
	if balFinal.AvailableVND != wantSellerNet-req2Amount || balFinal.PayoutPendingVND != 0 {
		t.Fatalf("expected available=%d payout_pending=0 after approve, got %+v",
			wantSellerNet-req2Amount, balFinal)
	}

	summaryFinal := payW2AssertLedgerBalanced(t, env)
	if holdAcc, ok := payW2FindAccount(summaryFinal, "seller_payout_hold:"+seller.ShopID); ok && holdAcc.BalanceVND != 0 {
		t.Fatalf("expected seller_payout_hold:%s=0 after approve, got %d", seller.ShopID, holdAcc.BalanceVND)
	}

	// Double decision on COMPLETED payout -> 409 FAILED_PRECONDITION.
	env.mustDoJSON(t, http.MethodPost, decide2Path, adminTok, nil, map[string]string{
		"decision": "REJECT",
		"reason":   "too late",
	}, http.StatusConflict)

	// Admin filter status=COMPLETED lists payout #2.
	adminCompletedPath := fmt.Sprintf("/api/v1/admin/payouts?status=COMPLETED&shop_id=%s&page=1&page_size=20", url.QueryEscape(seller.ShopID))
	adminCompletedResp := env.mustDoJSON(t, http.MethodGet, adminCompletedPath, adminTok, nil, nil, http.StatusOK)
	var adminCompleted payoutListResp
	if err := DecodeJSON(adminCompletedResp, &adminCompleted); err != nil {
		t.Fatalf("decode admin completed payout list: %v", err)
	}
	if adminCompleted.Total != 1 || len(adminCompleted.Items) != 1 || adminCompleted.Items[0].ID != p2.ID {
		t.Fatalf("unexpected admin completed list: %+v", adminCompleted)
	}

	// 9. Changing a verified bank account sets a 24h payout lock -> 412 BANK_ACCOUNT_NOT_READY.
	rawAcc2 := "19039988779999"
	env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/bank-account", seller.AccessToken, nil, usrW2BAddBankAccountReq{
		BankCode:          "TCB",
		BankName:          "Techcombank",
		AccountNumber:     rawAcc2,
		AccountHolderName: "TRAN THI MINH CHAU",
	}, http.StatusCreated, http.StatusOK)
	_, otp2 := usrW2BWaitNewOTPEmail(t, env, seller.Email, seenMail)
	ver2Resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/bank-account/verify-otp", seller.AccessToken, nil, usrW2BVerifyBankOTPReq{
		OTP: otp2,
	}, http.StatusOK)
	var ver2Out usrW2BBankAccountResp
	if err := DecodeJSON(ver2Resp, &ver2Out); err != nil {
		t.Fatalf("decode verify bank account #2: %v", err)
	}
	if !StatusEquals(ver2Out.BankAccount.Status, "VERIFIED") || ver2Out.BankAccount.PayoutLockedUntil == "" {
		t.Fatalf("expected VERIFIED bank account #2 with 24h payout_locked_until, got %+v", ver2Out.BankAccount)
	}

	lockedResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/seller/payouts", seller.AccessToken, map[string]string{
		"Idempotency-Key": "payout-locked24h-" + uuid.NewString(),
	}, map[string]any{
		"amount_vnd": int64(10000),
	}, http.StatusPreconditionFailed)
	var lockedErr payoutErrorEnvelope
	if err := DecodeJSON(lockedResp, &lockedErr); err != nil {
		t.Fatalf("decode 412 locked bank error: %v", err)
	}
	if lockedErr.Error.Code != "BANK_ACCOUNT_NOT_READY" {
		t.Fatalf("expected BANK_ACCOUNT_NOT_READY during 24h lock, got %q (%s)", lockedErr.Error.Code, string(lockedResp.Body))
	}

	payW2AssertLedgerBalanced(t, env)
}
