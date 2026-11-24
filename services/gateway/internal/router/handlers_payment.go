package router

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/gateway/internal/httpx"
	"github.com/nexusmart/core/services/gateway/internal/middleware"
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
	SourceType        string `json:"source_type"`
	TrackingCode      string `json:"tracking_code"`
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
	TrackingCode      string `json:"tracking_code"`
	OrderID           string `json:"order_id"`
	LineNumber        int32  `json:"line_number"`
}

type reconciliationRunSummaryDTO struct {
	RunID                  string `json:"run_id"`
	RunType                string `json:"run_type"`
	Provider               string `json:"provider"`
	RunDate                string `json:"run_date"`
	FileName               string `json:"file_name"`
	TotalRows              int32  `json:"total_rows"`
	MatchedCount           int32  `json:"matched_count"`
	AlreadyReconciledCount int32  `json:"already_reconciled_count"`
	MissingLocalCount      int32  `json:"missing_local_count"`
	MissingProviderCount   int32  `json:"missing_provider_count"`
	AmountMismatchCount    int32  `json:"amount_mismatch_count"`
	NotDeliveredCount      int32  `json:"not_delivered_count"`
	TotalMatchedVND        int64  `json:"total_matched_vnd"`
	Status                 string `json:"status"`
	CreatedBy              string `json:"created_by"`
	CreatedAt              string `json:"created_at,omitempty"`
}

type sellerLedgerEntryDTO struct {
	ID            string `json:"id"`
	TransactionID string `json:"transaction_id"`
	AccountCode   string `json:"account_code"`
	ReferenceType string `json:"reference_type"`
	ReferenceID   string `json:"reference_id"`
	Description   string `json:"description"`
	DebitVND      int64  `json:"debit_vnd"`
	CreditVND     int64  `json:"credit_vnd"`
	DeltaVND      int64  `json:"delta_vnd"`
	CreatedAt     string `json:"created_at,omitempty"`
}

type paymentFeeConfigDTO struct {
	ID            string `json:"id"`
	PaymentMethod string `json:"payment_method"`
	FeeBps        int32  `json:"fee_bps"`
	EffectiveFrom string `json:"effective_from,omitempty"`
	CreatedBy     string `json:"created_by"`
	Note          string `json:"note"`
	CreatedAt     string `json:"created_at,omitempty"`
}

func mapPaymentFeeConfig(c *paymentv1.PaymentFeeConfigItem) *paymentFeeConfigDTO {
	if c == nil {
		return nil
	}
	return &paymentFeeConfigDTO{
		ID:            c.GetId(),
		PaymentMethod: paymentMethodString(c.GetPaymentMethod()),
		FeeBps:        c.GetFeeBps(),
		EffectiveFrom: httpx.FormatTimestamp(c.GetEffectiveFrom()),
		CreatedBy:     c.GetCreatedBy(),
		Note:          c.GetNote(),
		CreatedAt:     httpx.FormatTimestamp(c.GetCreatedAt()),
	}
}

func parsePaymentMethod(s string) paymentv1.PaymentMethod {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "COD", "PAYMENT_METHOD_COD":
		return paymentv1.PaymentMethod_PAYMENT_METHOD_COD
	case "VIETQR", "PAYMENT_METHOD_VIETQR":
		return paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR
	default:
		return paymentv1.PaymentMethod_PAYMENT_METHOD_UNSPECIFIED
	}
}

func paymentHasShopAllocation(p *paymentv1.Payment, shopID string) bool {
	if p == nil || shopID == "" {
		return false
	}
	for _, a := range p.GetShopAllocations() {
		if a.GetShopId() == shopID {
			return true
		}
	}
	return false
}

func canAccessPayment(claims *auth.Claims, p *paymentv1.Payment) bool {
	if auth.HasRole(claims, "admin", "super_admin") {
		return true
	}
	if claims == nil || p == nil || strings.TrimSpace(p.GetUserId()) == "" {
		return false
	}
	if p.GetUserId() == claims.UserID() {
		return true
	}
	return auth.HasRole(claims, "seller") && claims.ShopID != "" && paymentHasShopAllocation(p, claims.ShopID)
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

// mapPaymentForCaller enforces role-scoped allocation and payment-credential privacy as
// defense-in-depth: admins see full details; buyers see their payment allocations with
// commission_vnd zeroed; non-buyer sellers see only their own shop's allocation with
// qr_url, qr_code, and provider_ref cleared.
func mapPaymentForCaller(ctx context.Context, p *paymentv1.Payment) *paymentDTO {
	dto := mapPayment(p)
	if dto == nil {
		return nil
	}
	claims, _ := auth.ClaimsFromContext(ctx)
	if auth.HasRole(claims, "admin", "super_admin") {
		return dto
	}
	if claims != nil && claims.UserID() != "" && p.GetUserId() == claims.UserID() {
		for _, a := range dto.ShopAllocations {
			a.CommissionVND = 0
		}
		return dto
	}
	if auth.HasRole(claims, "seller") && claims.ShopID != "" {
		filtered := make([]*shopAllocationDTO, 0, len(dto.ShopAllocations))
		for _, a := range dto.ShopAllocations {
			if a.ShopID == claims.ShopID {
				filtered = append(filtered, a)
			}
		}
		dto.ShopAllocations = filtered
		dto.QRURL = ""
		dto.QRCode = ""
		dto.ProviderRef = ""
		return dto
	}
	dto.ShopAllocations = []*shopAllocationDTO{}
	dto.QRURL = ""
	dto.QRCode = ""
	dto.ProviderRef = ""
	return dto
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
		SourceType:        it.GetSourceType(),
		TrackingCode:      it.GetTrackingCode(),
		CreatedAt:         httpx.FormatTimestamp(it.GetCreatedAt()),
		ResolvedAt:        httpx.FormatTimestamp(it.GetResolvedAt()),
	}
}

func mapReconciliationItem(it *paymentv1.ReconciliationItem) *reconciliationItemDTO {
	if it == nil {
		return nil
	}
	return &reconciliationItemDTO{
		PaymentID:         it.GetPaymentId(),
		ProviderTxID:      it.GetProviderTxId(),
		LocalAmountVND:    it.GetLocalAmountVnd(),
		ProviderAmountVND: it.GetProviderAmountVnd(),
		Status:            reconciliationStatusString(it.GetStatus()),
		Note:              it.GetNote(),
		TrackingCode:      it.GetTrackingCode(),
		OrderID:           it.GetOrderId(),
		LineNumber:        it.GetLineNumber(),
	}
}

func mapReconciliationRunSummary(run *paymentv1.ReconciliationRunSummary) *reconciliationRunSummaryDTO {
	if run == nil {
		return nil
	}
	return &reconciliationRunSummaryDTO{
		RunID:                  run.GetRunId(),
		RunType:                run.GetRunType(),
		Provider:               run.GetProvider(),
		RunDate:                run.GetRunDate(),
		FileName:               run.GetFileName(),
		TotalRows:              run.GetTotalRows(),
		MatchedCount:           run.GetMatchedCount(),
		AlreadyReconciledCount: run.GetAlreadyReconciledCount(),
		MissingLocalCount:      run.GetMissingLocalCount(),
		MissingProviderCount:   run.GetMissingProviderCount(),
		AmountMismatchCount:    run.GetAmountMismatchCount(),
		NotDeliveredCount:      run.GetNotDeliveredCount(),
		TotalMatchedVND:        run.GetTotalMatchedVnd(),
		Status:                 run.GetStatus(),
		CreatedBy:              run.GetCreatedBy(),
		CreatedAt:              httpx.FormatTimestamp(run.GetCreatedAt()),
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
	case paymentv1.ReconciliationStatus_RECONCILIATION_STATUS_ALREADY_RECONCILED:
		return "ALREADY_RECONCILED"
	case paymentv1.ReconciliationStatus_RECONCILIATION_STATUS_NOT_DELIVERED:
		return "NOT_DELIVERED"
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
	if !canAccessPayment(claims, payment) {
		httpx.WriteError(w, r, http.StatusForbidden, "PERMISSION_DENIED", "cannot access another user's payment")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"payment": mapPaymentForCaller(r.Context(), payment)})
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

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"payment": mapPaymentForCaller(r.Context(), getResp.GetPayment())})
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
		AmountVND *int64 `json:"amount_vnd"`
		Reason    string `json:"reason"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	if body.AmountVND == nil || *body.AmountVND <= 0 {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "amount_vnd is required and must be > 0")
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context(), "idempotency-key", idemKey)
	defer cancel()

	resp, err := h.clients.Payment.CreateRefund(ctx, &paymentv1.CreateRefundRequest{
		PaymentId:      chi.URLParam(r, "id"),
		OrderId:        body.OrderID,
		AmountVnd:      *body.AmountVND,
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
		items = append(items, mapReconciliationItem(it))
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

func (h *handler) handleSellerLedger(w http.ResponseWriter, r *http.Request) {
	page, pageSize := httpx.ParsePagination(r)
	q := r.URL.Query()
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	// Always enforce shop_id from verified seller claims.
	resp, err := h.clients.Payment.ListSellerLedger(ctx, &paymentv1.ListSellerLedgerRequest{
		ShopId:        auth.ShopIDFromContext(r.Context()),
		AccountFilter: strings.TrimSpace(q.Get("account_filter")),
		ReferenceType: strings.TrimSpace(q.Get("reference_type")),
		Page:          page,
		PageSize:      pageSize,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	items := make([]*sellerLedgerEntryDTO, 0, len(resp.GetEntries()))
	for _, e := range resp.GetEntries() {
		items = append(items, &sellerLedgerEntryDTO{
			ID:            e.GetId(),
			TransactionID: e.GetTransactionId(),
			AccountCode:   e.GetAccountCode(),
			ReferenceType: e.GetReferenceType(),
			ReferenceID:   e.GetReferenceId(),
			Description:   e.GetDescription(),
			DebitVND:      e.GetDebitVnd(),
			CreditVND:     e.GetCreditVnd(),
			DeltaVND:      e.GetDeltaVnd(),
			CreatedAt:     httpx.FormatTimestamp(e.GetCreatedAt()),
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items":     items,
		"page":      page,
		"page_size": pageSize,
		"total":     resp.GetTotal(),
	})
}

func (h *handler) handleAdminGetPaymentFeeConfig(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rawMethod := strings.TrimSpace(q.Get("payment_method"))
	pm := parsePaymentMethod(rawMethod)
	if rawMethod != "" && pm == paymentv1.PaymentMethod_PAYMENT_METHOD_UNSPECIFIED {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "payment_method must be COD or VIETQR")
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Payment.GetFeeConfig(ctx, &paymentv1.GetFeeConfigRequest{
		PaymentMethod:  pm,
		IncludeHistory: httpx.ParseBoolQuery(r, "include_history"),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	active := make([]*paymentFeeConfigDTO, 0, len(resp.GetActiveConfigs()))
	for _, c := range resp.GetActiveConfigs() {
		active = append(active, mapPaymentFeeConfig(c))
	}
	history := make([]*paymentFeeConfigDTO, 0, len(resp.GetHistory()))
	for _, c := range resp.GetHistory() {
		history = append(history, mapPaymentFeeConfig(c))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"active_configs": active,
		"history":        history,
	})
}

func (h *handler) handleAdminSetPaymentFeeConfig(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PaymentMethod string `json:"payment_method"`
		FeeBps        *int32 `json:"fee_bps"`
		EffectiveFrom string `json:"effective_from"`
		Note          string `json:"note"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	pm := parsePaymentMethod(body.PaymentMethod)
	if pm == paymentv1.PaymentMethod_PAYMENT_METHOD_UNSPECIFIED {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "payment_method must be COD or VIETQR")
		return
	}
	if body.FeeBps == nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "fee_bps is required")
		return
	}
	if *body.FeeBps < 0 {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "fee_bps must be non-negative")
		return
	}
	effFrom, err := httpx.ParseTimestamp(body.EffectiveFrom)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid effective_from timestamp")
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Payment.SetFeeConfig(ctx, &paymentv1.SetFeeConfigRequest{
		PaymentMethod: pm,
		FeeBps:        *body.FeeBps,
		EffectiveFrom: effFrom,
		Note:          body.Note,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"config": mapPaymentFeeConfig(resp.GetConfig()),
	})
}

const (
	maxCODCSVBytes         = httpx.MaxBodyBytes // 2 MiB (E20)
	maxCODCSVRows          = 5000
	maxCODCSVCellLen       = 128
	maxCODCSVDateLen       = 64
	maxCODCSVCarrierLen    = 64
	maxCODCSVFileNameLen   = 255
	codImportGRPCTimeout   = 110 * time.Second
	codImportWriteDeadline = 115 * time.Second
)

type importCodReconciliationResponseDTO struct {
	Run   *reconciliationRunSummaryDTO `json:"run"`
	Items []*reconciliationItemDTO     `json:"items"`
}

func isASCIIUnsignedDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func hasControlChars(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// hasCSVFormulaPrefix detects spreadsheet formula injection prefixes ('=', '+', '-', '@',
// tab/CR/LF, or leading whitespace followed by any of those) so malicious payloads cannot
// enter reconciliation tables and execute if later exported to CSV.
func hasCSVFormulaPrefix(v string) bool {
	if v == "" {
		return false
	}
	switch v[0] {
	case '=', '+', '-', '@', '\t', '\r', '\n':
		return true
	}
	trimmed := strings.TrimLeftFunc(v, unicode.IsSpace)
	if trimmed != "" {
		switch trimmed[0] {
		case '=', '+', '-', '@', '\t', '\r', '\n':
			return true
		}
	}
	return false
}

func cleanUploadFileName(raw string) (string, error) {
	if !utf8.ValidString(raw) {
		return "", errors.New("invalid file_name")
	}
	s := strings.TrimSpace(raw)
	if s == "" {
		return "cod-reconciliation.csv", nil
	}
	// Strip any POSIX or Windows path components.
	s = strings.ReplaceAll(s, `\`, "/")
	s = path.Base(s)
	if s == "." || s == "/" || s == "" {
		return "cod-reconciliation.csv", nil
	}
	if !utf8.ValidString(s) || len(s) > maxCODCSVFileNameLen || hasControlChars(s) || hasCSVFormulaPrefix(s) {
		return "", errors.New("invalid file_name")
	}
	return s, nil
}

func readCODReconciliationUpload(r *http.Request) (raw []byte, carrierCode, fileName, runDate string, httpStatus int, errCode, errMsg string) {
	q := r.URL.Query()
	carrierCode = strings.TrimSpace(q.Get("carrier_code"))
	if carrierCode == "" {
		carrierCode = strings.TrimSpace(r.Header.Get("X-Carrier-Code"))
	}
	fileName = strings.TrimSpace(q.Get("file_name"))
	if fileName == "" {
		fileName = strings.TrimSpace(r.Header.Get("X-File-Name"))
	}
	runDate = strings.TrimSpace(q.Get("run_date"))
	if runDate == "" {
		runDate = strings.TrimSpace(r.Header.Get("X-Run-Date"))
	}

	mediaType := ""
	if ct := strings.TrimSpace(r.Header.Get("Content-Type")); ct != "" {
		mt, _, err := mime.ParseMediaType(ct)
		if err != nil {
			return nil, "", "", "", http.StatusBadRequest, "INVALID_ARGUMENT", "invalid Content-Type header"
		}
		mediaType = strings.ToLower(strings.TrimSpace(mt))
	}

	if mediaType == "multipart/form-data" {
		if err := r.ParseMultipartForm(maxCODCSVBytes + codImportMultipartOverheadBytes); err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				return nil, "", "", "", http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "CSV upload exceeds maximum size of 2 MiB"
			}
			return nil, "", "", "", http.StatusBadRequest, "INVALID_ARGUMENT", "invalid multipart/form-data payload"
		}
		if r.MultipartForm != nil {
			defer func() { _ = r.MultipartForm.RemoveAll() }()
		}
		if v := strings.TrimSpace(r.FormValue("carrier_code")); v != "" {
			carrierCode = v
		}
		if v := strings.TrimSpace(r.FormValue("run_date")); v != "" {
			runDate = v
		}
		if v := strings.TrimSpace(r.FormValue("file_name")); v != "" {
			fileName = v
		}
		file, fh, err := r.FormFile("file")
		if err != nil {
			file, fh, err = r.FormFile("csv")
			if err != nil {
				return nil, "", "", "", http.StatusBadRequest, "INVALID_ARGUMENT", "multipart file field 'file' is required"
			}
		}
		defer func() { _ = file.Close() }()
		if fileName == "" && fh != nil {
			fileName = fh.Filename
		}
		data, readErr := io.ReadAll(io.LimitReader(file, maxCODCSVBytes+1))
		if readErr != nil {
			var maxErr *http.MaxBytesError
			if errors.As(readErr, &maxErr) {
				return nil, "", "", "", http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "CSV upload exceeds maximum size of 2 MiB"
			}
			return nil, "", "", "", http.StatusBadRequest, "INVALID_ARGUMENT", "failed to read uploaded CSV file"
		}
		if int64(len(data)) > maxCODCSVBytes {
			return nil, "", "", "", http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "CSV upload exceeds maximum size of 2 MiB"
		}
		return data, carrierCode, fileName, runDate, 0, "", ""
	}

	switch mediaType {
	case "", "text/csv", "text/plain", "application/csv", "application/octet-stream", "application/vnd.ms-excel":
	default:
		return nil, "", "", "", http.StatusBadRequest, "INVALID_ARGUMENT", "Content-Type must be text/csv or multipart/form-data"
	}

	if r.Body == nil {
		return nil, "", "", "", http.StatusBadRequest, "INVALID_ARGUMENT", "CSV request body is required"
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, maxCODCSVBytes+1))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return nil, "", "", "", http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "CSV upload exceeds maximum size of 2 MiB"
		}
		return nil, "", "", "", http.StatusBadRequest, "INVALID_ARGUMENT", "failed to read CSV request body"
	}
	if int64(len(data)) > maxCODCSVBytes {
		return nil, "", "", "", http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "CSV upload exceeds maximum size of 2 MiB"
	}
	return data, carrierCode, fileName, runDate, 0, "", ""
}

func parseCODReconciliationCSV(raw []byte) ([]*paymentv1.CodReconciliationRow, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, errors.New("CSV file is empty")
	}
	if !utf8.Valid(raw) {
		return nil, errors.New("CSV file must be valid UTF-8")
	}
	trimmed := bytes.TrimPrefix(raw, utf8BOM)

	cr := csv.NewReader(bytes.NewReader(trimmed))
	cr.FieldsPerRecord = -1

	header, err := cr.Read()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("CSV header row is required")
		}
		return nil, fmt.Errorf("invalid CSV header: %w", err)
	}

	colIdx := map[string]int{
		"tracking_code":  -1,
		"cod_amount_vnd": -1,
		"delivered_at":   -1,
		"order_code":     -1,
	}
	for i, col := range header {
		name := strings.ToLower(strings.TrimSpace(col))
		if i == 0 {
			name = strings.TrimPrefix(name, "\uFEFF")
		}
		prev, known := colIdx[name]
		if !known {
			return nil, fmt.Errorf("unexpected CSV column %q", name)
		}
		if prev >= 0 {
			return nil, fmt.Errorf("duplicate CSV column %q", name)
		}
		colIdx[name] = i
	}
	for _, reqCol := range []string{"tracking_code", "cod_amount_vnd", "delivered_at"} {
		if colIdx[reqCol] < 0 {
			return nil, fmt.Errorf("missing required CSV column %q", reqCol)
		}
	}

	idxTrack := colIdx["tracking_code"]
	idxAmount := colIdx["cod_amount_vnd"]
	idxDelivered := colIdx["delivered_at"]
	idxOrderCode := colIdx["order_code"]
	headerLen := len(header)

	rows := make([]*paymentv1.CodReconciliationRow, 0, 64)
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("malformed CSV record: %w", err)
		}
		lineNum, _ := cr.FieldPos(0)
		if len(rows) >= maxCODCSVRows {
			return nil, fmt.Errorf("CSV exceeds maximum limit of %d rows", maxCODCSVRows)
		}
		if len(rec) > headerLen || idxTrack >= len(rec) || idxAmount >= len(rec) || idxDelivered >= len(rec) {
			return nil, fmt.Errorf("line %d: unexpected column count %d", lineNum, len(rec))
		}

		rawTrack := rec[idxTrack]
		if hasCSVFormulaPrefix(rawTrack) || hasControlChars(rawTrack) {
			return nil, fmt.Errorf("line %d: invalid tracking_code", lineNum)
		}
		trackCode := strings.TrimSpace(rawTrack)
		if trackCode == "" || len(trackCode) > maxCODCSVCellLen {
			return nil, fmt.Errorf("line %d: tracking_code is required and must be <= %d characters", lineNum, maxCODCSVCellLen)
		}

		rawAmount := rec[idxAmount]
		if hasControlChars(rawAmount) {
			return nil, fmt.Errorf("line %d: invalid cod_amount_vnd", lineNum)
		}
		amountStr := strings.TrimSpace(rawAmount)
		if !isASCIIUnsignedDigits(amountStr) {
			return nil, fmt.Errorf("line %d: cod_amount_vnd must contain digits only", lineNum)
		}
		amountVND, parseErr := strconv.ParseInt(amountStr, 10, 64)
		if parseErr != nil {
			return nil, fmt.Errorf("line %d: cod_amount_vnd out of int64 range", lineNum)
		}

		rawDelivered := rec[idxDelivered]
		if hasCSVFormulaPrefix(rawDelivered) || hasControlChars(rawDelivered) {
			return nil, fmt.Errorf("line %d: invalid delivered_at", lineNum)
		}
		deliveredAt := strings.TrimSpace(rawDelivered)
		if len(deliveredAt) > maxCODCSVDateLen {
			return nil, fmt.Errorf("line %d: delivered_at exceeds maximum length", lineNum)
		}

		orderCode := ""
		if idxOrderCode >= 0 && idxOrderCode < len(rec) {
			rawOrder := rec[idxOrderCode]
			if hasCSVFormulaPrefix(rawOrder) || hasControlChars(rawOrder) {
				return nil, fmt.Errorf("line %d: invalid order_code", lineNum)
			}
			orderCode = strings.TrimSpace(rawOrder)
			if len(orderCode) > maxCODCSVCellLen {
				return nil, fmt.Errorf("line %d: order_code exceeds maximum length", lineNum)
			}
		}

		rows = append(rows, &paymentv1.CodReconciliationRow{
			LineNumber:   int32(lineNum),
			TrackingCode: trackCode,
			CodAmountVnd: amountVND,
			DeliveredAt:  deliveredAt,
			OrderCode:    orderCode,
		})
	}

	if len(rows) == 0 {
		return nil, errors.New("CSV contains no data rows")
	}
	return rows, nil
}

func (h *handler) handleAdminImportCodReconciliation(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w) //nolint:bodyclose // http.ResponseController wraps ResponseWriter and has no body to close
	deadline := time.Now().Add(codImportWriteDeadline)
	_ = rc.SetReadDeadline(deadline)
	_ = rc.SetWriteDeadline(deadline)

	raw, carrierCode, rawFileName, runDate, httpStatus, errCode, errMsg := readCODReconciliationUpload(r)
	if httpStatus != 0 {
		httpx.WriteError(w, r, httpStatus, errCode, errMsg)
		return
	}
	if !utf8.ValidString(carrierCode) || len(carrierCode) > maxCODCSVCarrierLen || hasControlChars(carrierCode) || hasCSVFormulaPrefix(carrierCode) {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid carrier_code")
		return
	}
	if runDate != "" {
		if _, err := time.Parse("2006-01-02", runDate); err != nil {
			httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "run_date must be YYYY-MM-DD")
			return
		}
	}
	fileName, err := cleanUploadFileName(rawFileName)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}

	rows, err := parseCODReconciliationCSV(raw)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}

	sum := sha256.Sum256(raw)
	fileSHA256 := hex.EncodeToString(sum[:])

	// Refresh connection deadlines right before the long-running 5,000-row import RPC
	// so upload time does not consume the 110s gRPC + response write budget.
	rpcDeadline := time.Now().Add(codImportWriteDeadline)
	_ = rc.SetReadDeadline(rpcDeadline)
	_ = rc.SetWriteDeadline(rpcDeadline)

	ctx, cancel := h.clients.OutgoingContextWithTimeout(r.Context(), codImportGRPCTimeout)
	defer cancel()

	resp, err := h.clients.Payment.ImportCodReconciliation(ctx, &paymentv1.ImportCodReconciliationRequest{
		CarrierCode: carrierCode,
		FileName:    fileName,
		FileSha256:  fileSHA256,
		Rows:        rows,
		RunDate:     runDate,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	if run := resp.GetRun(); run != nil {
		middleware.SetAuditSummary(r.Context(), run.GetRunId(), map[string]any{
			"run_id":                   run.GetRunId(),
			"run_type":                 run.GetRunType(),
			"provider":                 run.GetProvider(),
			"run_date":                 run.GetRunDate(),
			"status":                   run.GetStatus(),
			"total_rows":               run.GetTotalRows(),
			"matched_count":            run.GetMatchedCount(),
			"already_reconciled_count": run.GetAlreadyReconciledCount(),
			"missing_local_count":      run.GetMissingLocalCount(),
			"missing_provider_count":   run.GetMissingProviderCount(),
			"amount_mismatch_count":    run.GetAmountMismatchCount(),
			"not_delivered_count":      run.GetNotDeliveredCount(),
			"total_matched_vnd":        run.GetTotalMatchedVnd(),
		})
	}

	items := make([]*reconciliationItemDTO, 0, len(resp.GetItems()))
	for _, it := range resp.GetItems() {
		items = append(items, mapReconciliationItem(it))
	}
	httpx.WriteJSON(w, http.StatusOK, importCodReconciliationResponseDTO{
		Run:   mapReconciliationRunSummary(resp.GetRun()),
		Items: items,
	})
}

func (h *handler) handleAdminListReconciliationRuns(w http.ResponseWriter, r *http.Request) {
	page, pageSize := httpx.ParsePagination(r)
	runType := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("run_type")))
	if runType != "" && runType != "vietqr" && runType != "cod_file" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "run_type must be vietqr or cod_file")
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Payment.ListReconciliationRuns(ctx, &paymentv1.ListReconciliationRunsRequest{
		RunType:  runType,
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	items := make([]*reconciliationRunSummaryDTO, 0, len(resp.GetRuns()))
	for _, run := range resp.GetRuns() {
		items = append(items, mapReconciliationRunSummary(run))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items":     items,
		"page":      page,
		"page_size": pageSize,
		"total":     resp.GetTotal(),
	})
}
