package router

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/gateway/internal/httpx"
)

type shopAllocationDTO struct {
	OrderID             string `json:"order_id"`
	ShopID              string `json:"shop_id"`
	SubtotalVND         int64  `json:"subtotal_vnd"`
	CommissionVND       int64  `json:"commission_vnd"`
	ShippingFeeVND      int64  `json:"shipping_fee_vnd"`
	PlatformDiscountVND int64  `json:"platform_discount_vnd"`
}

type paymentDTO struct {
	PaymentID       string               `json:"payment_id"`
	OrderGroupID    string               `json:"order_group_id"`
	OrderID         string               `json:"order_id"`
	UserID          string               `json:"user_id"`
	AmountVND       int64                `json:"amount_vnd"`
	Method          string               `json:"method"`
	Status          string               `json:"status"`
	QRURL           string               `json:"qr_url"`
	QRCode          string               `json:"qr_code"`
	Provider        string               `json:"provider"`
	ProviderRef     string               `json:"provider_ref"`
	ShopAllocations []*shopAllocationDTO `json:"shop_allocations"`
	ExpiresAt       string               `json:"expires_at,omitempty"`
	CreatedAt       string               `json:"created_at,omitempty"`
	UpdatedAt       string               `json:"updated_at,omitempty"`
}

type refundDTO struct {
	RefundID          string `json:"refund_id"`
	PaymentID         string `json:"payment_id"`
	OrderID           string `json:"order_id"`
	AmountVND         int64  `json:"amount_vnd"`
	Status            string `json:"status"`
	Reason            string `json:"reason"`
	IdempotencyKey    string `json:"idempotency_key"`
	CreatedAt         string `json:"created_at,omitempty"`
	BankCode          string `json:"bank_code"`
	BankAccountMasked string `json:"bank_account_masked"`
	BankAccountName   string `json:"bank_account_name"`
	BankAccountNumber string `json:"bank_account_number,omitempty"`
	ProviderRefundID  string `json:"provider_refund_id"`
	IsLatePayment     bool   `json:"is_late_payment"`
	SettledAt         string `json:"settled_at,omitempty"`
	SettlementNote    string `json:"settlement_note"`
}

type reconciliationQueueItemDTO struct {
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
	CreatedAt         string `json:"created_at,omitempty"`
	ResolvedAt        string `json:"resolved_at,omitempty"`
}

type accountBalanceDTO struct {
	AccountCode    string `json:"account_code"`
	AccountType    string `json:"account_type"`
	DebitTotalVND  int64  `json:"debit_total_vnd"`
	CreditTotalVND int64  `json:"credit_total_vnd"`
	BalanceVND     int64  `json:"balance_vnd"`
}

type reconciliationItemDTO struct {
	PaymentID         string `json:"payment_id"`
	ProviderTxID      string `json:"provider_tx_id"`
	LocalAmountVND    int64  `json:"local_amount_vnd"`
	ProviderAmountVND int64  `json:"provider_amount_vnd"`
	Status            string `json:"status"`
	Note              string `json:"note"`
}

func mapPayment(p *paymentv1.Payment) *paymentDTO {
	if p == nil {
		return nil
	}
	allocs := make([]*shopAllocationDTO, 0, len(p.GetShopAllocations()))
	for _, a := range p.GetShopAllocations() {
		allocs = append(allocs, &shopAllocationDTO{
			OrderID:             a.GetOrderId(),
			ShopID:              a.GetShopId(),
			SubtotalVND:         a.GetSubtotalVnd(),
			CommissionVND:       a.GetCommissionVnd(),
			ShippingFeeVND:      a.GetShippingFeeVnd(),
			PlatformDiscountVND: a.GetPlatformDiscountVnd(),
		})
	}
	return &paymentDTO{
		PaymentID:       p.GetPaymentId(),
		OrderGroupID:    p.GetOrderGroupId(),
		OrderID:         p.GetOrderId(),
		UserID:          p.GetUserId(),
		AmountVND:       p.GetAmountVnd(),
		Method:          paymentMethodString(p.GetMethod()),
		Status:          paymentStatusString(p.GetStatus()),
		QRURL:           p.GetQrUrl(),
		QRCode:          p.GetQrCode(),
		Provider:        p.GetProvider(),
		ProviderRef:     p.GetProviderRef(),
		ShopAllocations: allocs,
		ExpiresAt:       httpx.FormatTimestamp(p.GetExpiresAt()),
		CreatedAt:       httpx.FormatTimestamp(p.GetCreatedAt()),
		UpdatedAt:       httpx.FormatTimestamp(p.GetUpdatedAt()),
	}
}

func mapRefund(rf *paymentv1.Refund, includeFullBankAccount bool) *refundDTO {
	if rf == nil {
		return nil
	}
	bankNum := ""
	if includeFullBankAccount {
		bankNum = rf.GetBankAccountNumber()
	}
	return &refundDTO{
		RefundID:          rf.GetRefundId(),
		PaymentID:         rf.GetPaymentId(),
		OrderID:           rf.GetOrderId(),
		AmountVND:         rf.GetAmountVnd(),
		Status:            refundStatusString(rf.GetStatus()),
		Reason:            rf.GetReason(),
		IdempotencyKey:    rf.GetIdempotencyKey(),
		CreatedAt:         httpx.FormatTimestamp(rf.GetCreatedAt()),
		BankCode:          rf.GetBankCode(),
		BankAccountMasked: rf.GetBankAccountMasked(),
		BankAccountName:   rf.GetBankAccountName(),
		BankAccountNumber: bankNum,
		ProviderRefundID:  rf.GetProviderRefundId(),
		IsLatePayment:     rf.GetIsLatePayment(),
		SettledAt:         httpx.FormatTimestamp(rf.GetSettledAt()),
		SettlementNote:    rf.GetSettlementNote(),
	}
}

func mapReconciliationQueueItem(it *paymentv1.ReconciliationQueueItem) *reconciliationQueueItemDTO {
	if it == nil {
		return nil
	}
	return &reconciliationQueueItemDTO{
		ID:                it.GetId(),
		WebhookEventID:    it.GetWebhookEventId(),
		Provider:          it.GetProvider(),
		ProviderTxID:      it.GetProviderTxId(),
		ExpectedPaymentID: it.GetExpectedPaymentId(),
		OrderRef:          it.GetOrderRef(),
		ExpectedAmountVND: it.GetExpectedAmountVnd(),
		ActualAmountVND:   it.GetActualAmountVnd(),
		Reason:            it.GetReason(),
		ResolutionStatus:  it.GetResolutionStatus(),
		Notes:             it.GetNotes(),
		CreatedAt:         httpx.FormatTimestamp(it.GetCreatedAt()),
		ResolvedAt:        httpx.FormatTimestamp(it.GetResolvedAt()),
	}
}

func paymentMethodString(m paymentv1.PaymentMethod) string {
	switch m {
	case paymentv1.PaymentMethod_PAYMENT_METHOD_COD:
		return "COD"
	case paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR:
		return "VIETQR"
	default:
		return ""
	}
}

func paymentStatusString(st paymentv1.PaymentStatus) string {
	switch st {
	case paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING:
		return "PENDING"
	case paymentv1.PaymentStatus_PAYMENT_STATUS_SUCCEEDED:
		return "SUCCEEDED"
	case paymentv1.PaymentStatus_PAYMENT_STATUS_FAILED:
		return "FAILED"
	case paymentv1.PaymentStatus_PAYMENT_STATUS_EXPIRED:
		return "EXPIRED"
	case paymentv1.PaymentStatus_PAYMENT_STATUS_CANCELLED:
		return "CANCELLED"
	case paymentv1.PaymentStatus_PAYMENT_STATUS_REFUNDED:
		return "REFUNDED"
	default:
		return ""
	}
}

func refundStatusString(st paymentv1.RefundStatus) string {
	switch st {
	case paymentv1.RefundStatus_REFUND_STATUS_PENDING:
		return "PENDING"
	case paymentv1.RefundStatus_REFUND_STATUS_DONE:
		return "DONE"
	case paymentv1.RefundStatus_REFUND_STATUS_FAILED:
		return "FAILED"
	default:
		return ""
	}
}

func parseRefundStatus(s string) paymentv1.RefundStatus {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "PENDING", "REFUND_STATUS_PENDING":
		return paymentv1.RefundStatus_REFUND_STATUS_PENDING
	case "DONE", "COMPLETED", "REFUND_STATUS_DONE":
		return paymentv1.RefundStatus_REFUND_STATUS_DONE
	case "FAILED", "REFUND_STATUS_FAILED":
		return paymentv1.RefundStatus_REFUND_STATUS_FAILED
	default:
		return paymentv1.RefundStatus_REFUND_STATUS_UNSPECIFIED
	}
}

func parseReconciliationAction(s string) paymentv1.ReconciliationAction {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "ASSIGN", "RECONCILIATION_ACTION_ASSIGN":
		return paymentv1.ReconciliationAction_RECONCILIATION_ACTION_ASSIGN
	case "REFUND", "RECONCILIATION_ACTION_REFUND":
		return paymentv1.ReconciliationAction_RECONCILIATION_ACTION_REFUND
	case "SUSPEND", "RECONCILIATION_ACTION_SUSPEND":
		return paymentv1.ReconciliationAction_RECONCILIATION_ACTION_SUSPEND
	default:
		return paymentv1.ReconciliationAction_RECONCILIATION_ACTION_UNSPECIFIED
	}
}

func reconciliationStatusString(st paymentv1.ReconciliationStatus) string {
	switch st {
	case paymentv1.ReconciliationStatus_RECONCILIATION_STATUS_MATCHED:
		return "MATCHED"
	case paymentv1.ReconciliationStatus_RECONCILIATION_STATUS_MISSING_LOCAL:
		return "MISSING_LOCAL"
	case paymentv1.ReconciliationStatus_RECONCILIATION_STATUS_MISSING_PROVIDER:
		return "MISSING_PROVIDER"
	case paymentv1.ReconciliationStatus_RECONCILIATION_STATUS_AMOUNT_MISMATCH:
		return "AMOUNT_MISMATCH"
	default:
		return ""
	}
}

func (h *handler) handleGetPayment(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Payment.GetPayment(ctx, &paymentv1.GetPaymentRequest{
		PaymentId: chi.URLParam(r, "id"),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	claims, _ := auth.ClaimsFromContext(r.Context())
	payment := resp.GetPayment()
	if payment != nil && payment.GetUserId() != "" &&
		!auth.HasRole(claims, "admin", "super_admin") &&
		payment.GetUserId() != claims.UserID() {
		httpx.WriteError(w, r, http.StatusForbidden, "PERMISSION_DENIED", "cannot access another user's payment")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"payment": mapPayment(payment)})
}

func (h *handler) handleRetryPayment(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Method string `json:"method"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	orderMethod := parseOrderPaymentMethod(body.Method)
	if orderMethod == orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_UNSPECIFIED {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "valid payment method (COD or VIETQR) is required")
		return
	}
	if orderMethod == orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD {
		httpx.WriteError(w, r, http.StatusConflict, "FAILED_PRECONDITION", "cannot retry or switch to COD via payment retry; create a new checkout instead")
		return
	}

	paymentID := chi.URLParam(r, "id")
	idemKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idemKey == "" {
		idemKey = "retry-" + paymentID + "-" + uuid.NewString()
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context(), "idempotency-key", idemKey)
	defer cancel()

	retryResp, err := h.clients.Order.RetryPayment(ctx, &orderv1.RetryPaymentRequest{
		PaymentId:      paymentID,
		PaymentMethod:  orderMethod,
		IdempotencyKey: idemKey,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	newPaymentID := strings.TrimSpace(retryResp.GetPaymentId())
	if newPaymentID == "" {
		httpx.WriteError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	getResp, err := h.clients.Payment.GetPayment(ctx, &paymentv1.GetPaymentRequest{
		PaymentId: newPaymentID,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"payment": mapPayment(getResp.GetPayment())})
}

func (h *handler) handleSellerBalance(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Payment.GetSellerBalance(ctx, &paymentv1.GetSellerBalanceRequest{
		ShopId: auth.ShopIDFromContext(r.Context()),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"shop_id":          resp.GetShopId(),
		"pending_vnd":      resp.GetPendingVnd(),
		"available_vnd":    resp.GetAvailableVnd(),
		"total_earned_vnd": resp.GetTotalEarnedVnd(),
	})
}

func (h *handler) handleAdminLedgerSummary(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Payment.GetLedgerSummary(ctx, &paymentv1.GetLedgerSummaryRequest{})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	accounts := make([]*accountBalanceDTO, 0, len(resp.GetAccounts()))
	for _, a := range resp.GetAccounts() {
		accounts = append(accounts, &accountBalanceDTO{
			AccountCode:    a.GetAccountCode(),
			AccountType:    a.GetAccountType(),
			DebitTotalVND:  a.GetDebitTotalVnd(),
			CreditTotalVND: a.GetCreditTotalVnd(),
			BalanceVND:     a.GetBalanceVnd(),
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"total_debits_vnd":  resp.GetTotalDebitsVnd(),
		"total_credits_vnd": resp.GetTotalCreditsVnd(),
		"is_balanced":       resp.GetIsBalanced(),
		"accounts":          accounts,
	})
}

func (h *handler) handleAdminRefundPayment(w http.ResponseWriter, r *http.Request) {
	idemKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idemKey == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "MISSING_IDEMPOTENCY_KEY", "Idempotency-Key header is required")
		return
	}
	var body struct {
		OrderID   string `json:"order_id"`
		AmountVND int64  `json:"amount_vnd"`
		Reason    string `json:"reason"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context(), "idempotency-key", idemKey)
	defer cancel()

	resp, err := h.clients.Payment.CreateRefund(ctx, &paymentv1.CreateRefundRequest{
		PaymentId:      chi.URLParam(r, "id"),
		OrderId:        body.OrderID,
		AmountVnd:      body.AmountVND,
		Reason:         body.Reason,
		IdempotencyKey: idemKey,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"refund":          mapRefund(resp.GetRefund(), true),
		"already_existed": resp.GetAlreadyExisted(),
	})
}

func (h *handler) handleAdminReconcilePayments(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Date string `json:"date"`
	}
	if err := httpx.DecodeOptionalJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Payment.RunReconciliation(ctx, &paymentv1.RunReconciliationRequest{
		Date: body.Date,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	items := make([]*reconciliationItemDTO, 0, len(resp.GetItems()))
	for _, it := range resp.GetItems() {
		items = append(items, &reconciliationItemDTO{
			PaymentID:         it.GetPaymentId(),
			ProviderTxID:      it.GetProviderTxId(),
			LocalAmountVND:    it.GetLocalAmountVnd(),
			ProviderAmountVND: it.GetProviderAmountVnd(),
			Status:            reconciliationStatusString(it.GetStatus()),
			Note:              it.GetNote(),
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"run_id":                 resp.GetRunId(),
		"matched_count":          resp.GetMatchedCount(),
		"missing_local_count":    resp.GetMissingLocalCount(),
		"missing_provider_count": resp.GetMissingProviderCount(),
		"amount_mismatch_count":  resp.GetAmountMismatchCount(),
		"items":                  items,
	})
}

func (h *handler) handleListOrderRefunds(w http.ResponseWriter, r *http.Request) {
	orderID := chi.URLParam(r, "id")
	page, pageSize := httpx.ParsePagination(r)
	claims, _ := auth.ClaimsFromContext(r.Context())
	isAdmin := auth.HasRole(claims, "admin", "super_admin")

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	if !isAdmin && h.clients.Order != nil {
		ordResp, err := h.clients.Order.GetOrder(ctx, &orderv1.GetOrderRequest{
			OrderId:     orderID,
			ActorId:     claims.UserID(),
			ActorRole:   claims.Role,
			ActorShopId: claims.ShopID,
		})
		if err != nil {
			httpx.WriteGRPCError(w, r, err)
			return
		}
		if ord := ordResp.GetOrder(); ord != nil && ord.GetBuyerId() != "" && ord.GetBuyerId() != claims.UserID() {
			httpx.WriteError(w, r, http.StatusForbidden, "PERMISSION_DENIED", "cannot access another buyer's order refunds")
			return
		}
	}

	resp, err := h.clients.Payment.ListRefunds(ctx, &paymentv1.ListRefundsRequest{
		Status:   parseRefundStatus(r.URL.Query().Get("status")),
		OrderId:  orderID,
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	items := make([]*refundDTO, 0, len(resp.GetRefunds()))
	for _, rf := range resp.GetRefunds() {
		items = append(items, mapRefund(rf, isAdmin))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items":     items,
		"page":      page,
		"page_size": pageSize,
		"total":     resp.GetTotal(),
	})
}

func (h *handler) handleSubmitRefundBankAccount(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BankCode          string `json:"bank_code"`
		BankAccountNumber string `json:"bank_account_number"`
		BankAccountName   string `json:"bank_account_name"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Payment.SubmitRefundBankAccount(ctx, &paymentv1.SubmitRefundBankAccountRequest{
		RefundId:          chi.URLParam(r, "id"),
		BankCode:          body.BankCode,
		BankAccountNumber: body.BankAccountNumber,
		BankAccountName:   body.BankAccountName,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"refund": mapRefund(resp.GetRefund(), false),
	})
}

func (h *handler) handleAdminListRefunds(w http.ResponseWriter, r *http.Request) {
	page, pageSize := httpx.ParsePagination(r)
	q := r.URL.Query()

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Payment.ListRefunds(ctx, &paymentv1.ListRefundsRequest{
		Status:   parseRefundStatus(q.Get("status")),
		OrderId:  q.Get("order_id"),
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	items := make([]*refundDTO, 0, len(resp.GetRefunds()))
	for _, rf := range resp.GetRefunds() {
		items = append(items, mapRefund(rf, true))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items":     items,
		"page":      page,
		"page_size": pageSize,
		"total":     resp.GetTotal(),
	})
}

func (h *handler) handleAdminSettleRefund(w http.ResponseWriter, r *http.Request) {
	idemKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idemKey == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "MISSING_IDEMPOTENCY_KEY", "Idempotency-Key header is required")
		return
	}
	var body struct {
		ProviderRefundID string `json:"provider_refund_id"`
		Note             string `json:"note"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context(), "idempotency-key", idemKey)
	defer cancel()

	resp, err := h.clients.Payment.SettleRefund(ctx, &paymentv1.SettleRefundRequest{
		RefundId:         chi.URLParam(r, "id"),
		ProviderRefundId: body.ProviderRefundID,
		Note:             body.Note,
		IdempotencyKey:   idemKey,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"refund":          mapRefund(resp.GetRefund(), true),
		"already_settled": resp.GetAlreadySettled(),
	})
}

func (h *handler) handleAdminListReconciliationQueue(w http.ResponseWriter, r *http.Request) {
	page, pageSize := httpx.ParsePagination(r)
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Payment.ListReconciliationQueue(ctx, &paymentv1.ListReconciliationQueueRequest{
		Status:   r.URL.Query().Get("status"),
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	items := make([]*reconciliationQueueItemDTO, 0, len(resp.GetItems()))
	for _, it := range resp.GetItems() {
		items = append(items, mapReconciliationQueueItem(it))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items":     items,
		"page":      page,
		"page_size": pageSize,
		"total":     resp.GetTotal(),
	})
}

func (h *handler) handleAdminResolveReconciliationItem(w http.ResponseWriter, r *http.Request) {
	idemKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idemKey == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "MISSING_IDEMPOTENCY_KEY", "Idempotency-Key header is required")
		return
	}
	var body struct {
		Action    string `json:"action"`
		PaymentID string `json:"payment_id"`
		Note      string `json:"note"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context(), "idempotency-key", idemKey)
	defer cancel()

	resp, err := h.clients.Payment.ResolveReconciliationItem(ctx, &paymentv1.ResolveReconciliationItemRequest{
		ItemId:         chi.URLParam(r, "id"),
		Action:         parseReconciliationAction(body.Action),
		PaymentId:      body.PaymentID,
		Note:           body.Note,
		IdempotencyKey: idemKey,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"item":             mapReconciliationQueueItem(resp.GetItem()),
		"refund":           mapRefund(resp.GetRefund(), true),
		"already_resolved": resp.GetAlreadyResolved(),
	})
}
