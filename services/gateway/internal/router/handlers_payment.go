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
	SalesChannel        string `json:"sales_channel"`
	StorefrontShopID    string `json:"storefront_shop_id"`
}

type paymentDTO struct {
	PaymentID        string               `json:"payment_id"`
	OrderGroupID     string               `json:"order_group_id"`
	OrderID          string               `json:"order_id"`
	UserID           string               `json:"user_id"`
	AmountVND        int64                `json:"amount_vnd"`
	Method           string               `json:"method"`
	Status           string               `json:"status"`
	QRURL            string               `json:"qr_url"`
	QRCode           string               `json:"qr_code"`
	PayURL           string               `json:"pay_url"`
	Provider         string               `json:"provider"`
	ProviderRef      string               `json:"provider_ref"`
	ShopAllocations  []*shopAllocationDTO `json:"shop_allocations"`
	SalesChannel     string               `json:"sales_channel"`
	StorefrontShopID string               `json:"storefront_shop_id"`
	ExpiresAt        string               `json:"expires_at,omitempty"`
	CreatedAt        string               `json:"created_at,omitempty"`
	UpdatedAt        string               `json:"updated_at,omitempty"`
}

type publicPaymentMethodConfigDTO struct {
	Method        string `json:"method"`
	Enabled       bool   `json:"enabled"`
	MinOrderVND   int64  `json:"min_order_vnd"`
	MaxOrderVND   int64  `json:"max_order_vnd"`
	ExpiryMinutes int32  `json:"expiry_minutes"`
	Version       int64  `json:"version"`
}

type adminPaymentMethodConfigDTO struct {
	Method                string `json:"method"`
	Enabled               bool   `json:"enabled"`
	MinOrderVND           int64  `json:"min_order_vnd"`
	MaxOrderVND           int64  `json:"max_order_vnd"`
	ExpiryMinutes         int32  `json:"expiry_minutes"`
	CredentialsConfigured bool   `json:"credentials_configured"`
	CredentialPreview     string `json:"credential_preview"`
	UpdatedBy             string `json:"updated_by"`
	UpdatedAt             string `json:"updated_at,omitempty"`
	Note                  string `json:"note"`
	Version               int64  `json:"version"`
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
	RequestedBy       string `json:"requested_by"`
	ApprovedBy        string `json:"approved_by"`
	ApprovedAt        string `json:"approved_at,omitempty"`
	ApprovalNote      string `json:"approval_note"`
	ProviderMRefundID string `json:"provider_m_refund_id"`
	ProviderStatus    string `json:"provider_status"`
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
	SalesChannel      string `json:"sales_channel"`
	StorefrontShopID  string `json:"storefront_shop_id"`
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
	SalesChannel           string `json:"sales_channel,omitempty"`
	AutoHealedCount        int32  `json:"auto_healed_count,omitempty"`
	CompletedAt            string `json:"completed_at,omitempty"`
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
	case "ZALOPAY", "PAYMENT_METHOD_ZALOPAY":
		return paymentv1.PaymentMethod_PAYMENT_METHOD_ZALOPAY
	default:
		return paymentv1.PaymentMethod_PAYMENT_METHOD_UNSPECIFIED
	}
}

func mapPublicPaymentMethodConfig(c *paymentv1.PaymentMethodConfig) *publicPaymentMethodConfigDTO {
	if c == nil {
		return nil
	}
	return &publicPaymentMethodConfigDTO{
		Method:        paymentMethodString(c.GetMethod()),
		Enabled:       c.GetEnabled(),
		MinOrderVND:   c.GetMinOrderVnd(),
		MaxOrderVND:   c.GetMaxOrderVnd(),
		ExpiryMinutes: c.GetExpiryMinutes(),
		Version:       c.GetVersion(),
	}
}

func mapAdminPaymentMethodConfig(c *paymentv1.PaymentMethodConfig) *adminPaymentMethodConfigDTO {
	if c == nil {
		return nil
	}
	return &adminPaymentMethodConfigDTO{
		Method:                paymentMethodString(c.GetMethod()),
		Enabled:               c.GetEnabled(),
		MinOrderVND:           c.GetMinOrderVnd(),
		MaxOrderVND:           c.GetMaxOrderVnd(),
		ExpiryMinutes:         c.GetExpiryMinutes(),
		CredentialsConfigured: c.GetCredentialsConfigured(),
		CredentialPreview:     c.GetCredentialPreview(),
		UpdatedBy:             c.GetUpdatedBy(),
		UpdatedAt:             httpx.FormatTimestamp(c.GetUpdatedAt()),
		Note:                  c.GetNote(),
		Version:               c.GetVersion(),
	}
}

func canAdminReadPayment(claims *auth.Claims) bool {
	return auth.HasCapability(claims, auth.CapRefundsRead) || auth.HasCapability(claims, auth.CapLedgerRead)
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
	if canAdminReadPayment(claims) {
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
			SalesChannel:        normalizeSalesChannelOutput(a.GetSalesChannel()),
			StorefrontShopID:    a.GetStorefrontShopId(),
		})
	}
	return &paymentDTO{
		PaymentID:        p.GetPaymentId(),
		OrderGroupID:     p.GetOrderGroupId(),
		OrderID:          p.GetOrderId(),
		UserID:           p.GetUserId(),
		AmountVND:        p.GetAmountVnd(),
		Method:           paymentMethodString(p.GetMethod()),
		Status:           paymentStatusString(p.GetStatus()),
		QRURL:            p.GetQrUrl(),
		QRCode:           p.GetQrCode(),
		PayURL:           p.GetPayUrl(),
		Provider:         p.GetProvider(),
		ProviderRef:      p.GetProviderRef(),
		ShopAllocations:  allocs,
		SalesChannel:     normalizeSalesChannelOutput(p.GetSalesChannel()),
		StorefrontShopID: p.GetStorefrontShopId(),
		ExpiresAt:        httpx.FormatTimestamp(p.GetExpiresAt()),
		CreatedAt:        httpx.FormatTimestamp(p.GetCreatedAt()),
		UpdatedAt:        httpx.FormatTimestamp(p.GetUpdatedAt()),
	}
}

// mapPaymentForCaller enforces role-scoped allocation and payment-credential privacy as
// defense-in-depth: authorized staff (refunds.read/ledger.read) see full details; buyers see
// their payment allocations with commission_vnd zeroed; non-buyer sellers see only their own
// shop's allocation with qr_url, qr_code, pay_url, and provider_ref cleared.
func mapPaymentForCaller(ctx context.Context, p *paymentv1.Payment) *paymentDTO {
	dto := mapPayment(p)
	if dto == nil {
		return nil
	}
	claims, _ := auth.ClaimsFromContext(ctx)
	if canAdminReadPayment(claims) {
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
		dto.PayURL = ""
		dto.ProviderRef = ""
		return dto
	}
	dto.ShopAllocations = []*shopAllocationDTO{}
	dto.QRURL = ""
	dto.QRCode = ""
	dto.PayURL = ""
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
		RequestedBy:       rf.GetRequestedBy(),
		ApprovedBy:        rf.GetApprovedBy(),
		ApprovedAt:        httpx.FormatTimestamp(rf.GetApprovedAt()),
		ApprovalNote:      rf.GetApprovalNote(),
		ProviderMRefundID: rf.GetProviderMRefundId(),
		ProviderStatus:    rf.GetProviderStatus(),
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
		SalesChannel:      strings.TrimSpace(it.GetSalesChannel()),
		StorefrontShopID:  it.GetStorefrontShopId(),
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
		SalesChannel:           run.GetSalesChannel(),
		AutoHealedCount:        run.GetAutoHealedCount(),
		CompletedAt:            httpx.FormatTimestamp(run.GetCompletedAt()),
	}
}

func paymentMethodString(m paymentv1.PaymentMethod) string {
	switch m {
	case paymentv1.PaymentMethod_PAYMENT_METHOD_COD:
		return "COD"
	case paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR:
		return "VIETQR"
	case paymentv1.PaymentMethod_PAYMENT_METHOD_ZALOPAY:
		return "ZALOPAY"
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
	case paymentv1.RefundStatus_REFUND_STATUS_PENDING_APPROVAL:
		return "PENDING_APPROVAL"
	case paymentv1.RefundStatus_REFUND_STATUS_REJECTED:
		return "REJECTED"
	case paymentv1.RefundStatus_REFUND_STATUS_MANUAL_REVIEW:
		return "MANUAL_REVIEW"
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
	case "PENDING_APPROVAL", "REFUND_STATUS_PENDING_APPROVAL":
		return paymentv1.RefundStatus_REFUND_STATUS_PENDING_APPROVAL
	case "REJECTED", "REFUND_STATUS_REJECTED":
		return paymentv1.RefundStatus_REFUND_STATUS_REJECTED
	case "MANUAL_REVIEW", "REFUND_STATUS_MANUAL_REVIEW":
		return paymentv1.RefundStatus_REFUND_STATUS_MANUAL_REVIEW
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
	idemKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idemKey == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "MISSING_IDEMPOTENCY_KEY", "Idempotency-Key header is required")
		return
	}

	var body struct {
		Method string `json:"method"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	orderMethod := parseOrderPaymentMethod(body.Method)
	if orderMethod == orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_UNSPECIFIED {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "valid payment method (COD, VIETQR, or ZALOPAY) is required")
		return
	}

	paymentID := chi.URLParam(r, "id")
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

	payDTO := mapPaymentForCaller(r.Context(), getResp.GetPayment())
	payURL := retryResp.GetPayUrl()
	if payURL == "" && payDTO != nil {
		payURL = payDTO.PayURL
	} else if payDTO != nil && payDTO.PayURL == "" && payURL != "" {
		payDTO.PayURL = payURL
	}

	orders := make([]*orderDTO, 0, len(retryResp.GetOrders()))
	for _, o := range retryResp.GetOrders() {
		orders = append(orders, mapOrderForBuyer(o))
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"payment":         payDTO,
		"pay_url":         payURL,
		"switched_to_cod": retryResp.GetSwitchedToCod(),
		"orders":          orders,
	})
}

func (h *handler) handleSellerBalance(w http.ResponseWriter, r *http.Request) {
	shopID, ok := requireSellerShopID(w, r)
	if !ok {
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Payment.GetSellerBalance(ctx, &paymentv1.GetSellerBalanceRequest{
		ShopId: shopID,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"shop_id":            resp.GetShopId(),
		"pending_vnd":        resp.GetPendingVnd(),
		"available_vnd":      resp.GetAvailableVnd(),
		"total_earned_vnd":   resp.GetTotalEarnedVnd(),
		"payout_pending_vnd": resp.GetPayoutPendingVnd(),
	})
}

type sellerPayoutDTO struct {
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
	CreatedAt         string `json:"created_at,omitempty"`
	DecidedAt         string `json:"decided_at,omitempty"`
}

func mapSellerPayout(p *paymentv1.SellerPayout) *sellerPayoutDTO {
	if p == nil {
		return nil
	}
	return &sellerPayoutDTO{
		ID:                p.GetId(),
		ShopID:            p.GetShopId(),
		AmountVND:         p.GetAmountVnd(),
		Status:            strings.ToUpper(strings.TrimSpace(p.GetStatus())),
		BankName:          p.GetBankName(),
		BankAccountMasked: p.GetBankAccountMasked(),
		AccountHolderName: p.GetAccountHolderName(),
		RejectReason:      p.GetRejectReason(),
		TransferRef:       p.GetTransferRef(),
		RequestedBy:       p.GetRequestedBy(),
		DecidedBy:         p.GetDecidedBy(),
		CreatedAt:         httpx.FormatTimestamp(p.GetCreatedAt()),
		DecidedAt:         httpx.FormatTimestamp(p.GetDecidedAt()),
	}
}

func (h *handler) handleSellerListPayouts(w http.ResponseWriter, r *http.Request) {
	shopID, ok := requireSellerShopID(w, r)
	if !ok {
		return
	}
	page, pageSize, err := httpx.ParsePagination(r)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	statusFilter := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("status")))
	if statusFilter != "" && statusFilter != "PENDING" && statusFilter != "COMPLETED" && statusFilter != "REJECTED" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "status must be PENDING, COMPLETED, or REJECTED")
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Payment.ListSellerPayouts(ctx, &paymentv1.ListSellerPayoutsRequest{
		ShopId:   shopID,
		Status:   statusFilter,
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	items := make([]*sellerPayoutDTO, 0, len(resp.GetItems()))
	for _, p := range resp.GetItems() {
		items = append(items, mapSellerPayout(p))
	}
	respPage := resp.GetPage()
	if respPage <= 0 {
		respPage = page
	}
	respPageSize := resp.GetPageSize()
	if respPageSize <= 0 {
		respPageSize = pageSize
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items":     items,
		"page":      respPage,
		"page_size": respPageSize,
		"total":     resp.GetTotal(),
	})
}

func (h *handler) handleSellerRequestPayout(w http.ResponseWriter, r *http.Request) {
	shopID, ok := requireSellerShopID(w, r)
	if !ok {
		return
	}
	idemKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idemKey == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "Idempotency-Key header is required")
		return
	}

	var body struct {
		AmountVND *int64 `json:"amount_vnd"`
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

	resp, err := h.clients.Payment.RequestSellerPayout(ctx, &paymentv1.RequestSellerPayoutRequest{
		ShopId:            shopID,
		AmountVnd:         *body.AmountVND,
		IdempotencyKey:    idemKey,
		RequestedByUserId: auth.UserIDFromContext(r.Context()),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"payout": mapSellerPayout(resp.GetPayout()),
	})
}

func (h *handler) handleAdminListPayouts(w http.ResponseWriter, r *http.Request) {
	page, pageSize, err := httpx.ParsePagination(r)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	q := r.URL.Query()
	statusFilter := strings.ToUpper(strings.TrimSpace(q.Get("status")))
	if statusFilter != "" && statusFilter != "PENDING" && statusFilter != "COMPLETED" && statusFilter != "REJECTED" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "status must be PENDING, COMPLETED, or REJECTED")
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Payment.ListSellerPayouts(ctx, &paymentv1.ListSellerPayoutsRequest{
		ShopId:   strings.TrimSpace(q.Get("shop_id")),
		Status:   statusFilter,
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	items := make([]*sellerPayoutDTO, 0, len(resp.GetItems()))
	for _, p := range resp.GetItems() {
		items = append(items, mapSellerPayout(p))
	}
	respPage := resp.GetPage()
	if respPage <= 0 {
		respPage = page
	}
	respPageSize := resp.GetPageSize()
	if respPageSize <= 0 {
		respPageSize = pageSize
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items":     items,
		"page":      respPage,
		"page_size": respPageSize,
		"total":     resp.GetTotal(),
	})
}

func (h *handler) handleAdminDecidePayout(w http.ResponseWriter, r *http.Request) {
	payoutID := strings.TrimSpace(chi.URLParam(r, "id"))
	if payoutID == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "payout id is required")
		return
	}

	var body struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	decision := strings.ToUpper(strings.TrimSpace(body.Decision))
	if decision != "APPROVE" && decision != "REJECT" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "decision must be APPROVE or REJECT")
		return
	}
	reason := strings.TrimSpace(body.Reason)
	if decision == "REJECT" && reason == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "reason is required when rejecting a payout")
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Payment.DecideSellerPayout(ctx, &paymentv1.DecideSellerPayoutRequest{
		PayoutId:        payoutID,
		Decision:        decision,
		Reason:          reason,
		DecidedByUserId: auth.UserIDFromContext(r.Context()),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"payout": mapSellerPayout(resp.GetPayout()),
	})
}

func (h *handler) handleAdminLedgerSummary(w http.ResponseWriter, r *http.Request) {
	salesChannel, errMsg := validateSalesChannelFilter(r.URL.Query().Get("sales_channel"))
	if errMsg != "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", errMsg)
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Payment.GetLedgerSummary(ctx, &paymentv1.GetLedgerSummaryRequest{
		SalesChannel: salesChannel,
	})
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
		"sales_channel":     resp.GetSalesChannel(),
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
		Date         string `json:"date"`
		SalesChannel string `json:"sales_channel"`
	}
	if err := httpx.DecodeOptionalJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	rawChannel := body.SalesChannel
	if strings.TrimSpace(rawChannel) == "" {
		rawChannel = r.URL.Query().Get("sales_channel")
	}
	salesChannel, errMsg := validateSalesChannelFilter(rawChannel)
	if errMsg != "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", errMsg)
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Payment.RunReconciliation(ctx, &paymentv1.RunReconciliationRequest{
		Date:         body.Date,
		SalesChannel: salesChannel,
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
		"auto_healed_count":      resp.GetAutoHealedCount(),
		"sales_channel":          resp.GetSalesChannel(),
		"items":                  items,
	})
}

func (h *handler) handleListOrderRefunds(w http.ResponseWriter, r *http.Request) {
	orderID := chi.URLParam(r, "id")
	page, pageSize, err := httpx.ParsePagination(r)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	claims, _ := auth.ClaimsFromContext(r.Context())
	isAdmin := auth.HasCapability(claims, auth.CapRefundsRead)

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
	page, pageSize, err := httpx.ParsePagination(r)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
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
	page, pageSize, err := httpx.ParsePagination(r)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
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
	page, pageSize, err := httpx.ParsePagination(r)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
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
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "payment_method must be COD, VIETQR, or ZALOPAY")
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
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "payment_method must be COD, VIETQR, or ZALOPAY")
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
	page, pageSize, err := httpx.ParsePagination(r)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	runType := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("run_type")))
	if runType != "" && runType != "vietqr" && runType != "cod_file" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "run_type must be vietqr or cod_file")
		return
	}
	salesChannel, errMsg := validateSalesChannelFilter(r.URL.Query().Get("sales_channel"))
	if errMsg != "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", errMsg)
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Payment.ListReconciliationRuns(ctx, &paymentv1.ListReconciliationRunsRequest{
		RunType:      runType,
		Page:         page,
		PageSize:     pageSize,
		SalesChannel: salesChannel,
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

// --- Wave 3b-2 (A6 & A7): Payment Method Configs & Two-Person Refund Approval ---

func (h *handler) handleListPublicPaymentMethods(w http.ResponseWriter, r *http.Request) {
	rawMethod := strings.TrimSpace(r.URL.Query().Get("method"))
	pm := parsePaymentMethod(rawMethod)
	if rawMethod != "" && pm == paymentv1.PaymentMethod_PAYMENT_METHOD_UNSPECIFIED {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "method must be COD, VIETQR, or ZALOPAY")
		return
	}

	// Always strip Authorization on public payment-methods route so upstream enforces public-only view (F6/F8).
	ctx, cancel := h.clients.PublicOutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Payment.GetPaymentMethodConfigs(ctx, &paymentv1.GetPaymentMethodConfigsRequest{
		Method: pm,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	methods := make([]*publicPaymentMethodConfigDTO, 0, len(resp.GetConfigs()))
	for _, c := range resp.GetConfigs() {
		methods = append(methods, mapPublicPaymentMethodConfig(c))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"methods": methods,
	})
}

func (h *handler) handleAdminListPaymentMethods(w http.ResponseWriter, r *http.Request) {
	rawMethod := strings.TrimSpace(r.URL.Query().Get("method"))
	pm := parsePaymentMethod(rawMethod)
	if rawMethod != "" && pm == paymentv1.PaymentMethod_PAYMENT_METHOD_UNSPECIFIED {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "method must be COD, VIETQR, or ZALOPAY")
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Payment.GetPaymentMethodConfigs(ctx, &paymentv1.GetPaymentMethodConfigsRequest{
		Method: pm,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	methods := make([]*adminPaymentMethodConfigDTO, 0, len(resp.GetConfigs()))
	for _, c := range resp.GetConfigs() {
		methods = append(methods, mapAdminPaymentMethodConfig(c))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"methods": methods,
	})
}

func (h *handler) handleAdminUpdatePaymentMethodConfig(w http.ResponseWriter, r *http.Request) {
	pm := parsePaymentMethod(chi.URLParam(r, "method"))
	if pm == paymentv1.PaymentMethod_PAYMENT_METHOD_UNSPECIFIED {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "method must be COD, VIETQR, or ZALOPAY")
		return
	}

	var body struct {
		Enabled         *bool  `json:"enabled"`
		MinOrderVND     int64  `json:"min_order_vnd"`
		MaxOrderVND     int64  `json:"max_order_vnd"`
		ExpiryMinutes   int32  `json:"expiry_minutes"`
		Note            string `json:"note"`
		ExpectedVersion *int64 `json:"expected_version"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	if body.Enabled == nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "enabled is required")
		return
	}
	if body.MinOrderVND < 0 || body.MaxOrderVND < 0 {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "min_order_vnd and max_order_vnd must be non-negative")
		return
	}
	if body.MaxOrderVND > 0 && body.MinOrderVND > body.MaxOrderVND {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "min_order_vnd must not exceed max_order_vnd")
		return
	}
	if body.ExpiryMinutes < 0 || body.ExpiryMinutes > 1440 {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "expiry_minutes must be between 0 and 1440")
		return
	}
	if body.ExpectedVersion == nil || *body.ExpectedVersion < 0 {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "expected_version is required and must be non-negative")
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	// Capture best-effort before snapshot for full before/after audit logging (F7).
	if beforeResp, bErr := h.clients.Payment.GetPaymentMethodConfigs(ctx, &paymentv1.GetPaymentMethodConfigsRequest{Method: pm}); bErr == nil {
		for _, c := range beforeResp.GetConfigs() {
			if c.GetMethod() == pm {
				middleware.SetAuditBefore(r.Context(), map[string]any{
					"method":         paymentMethodString(c.GetMethod()),
					"enabled":        c.GetEnabled(),
					"min_order_vnd":  c.GetMinOrderVnd(),
					"max_order_vnd":  c.GetMaxOrderVnd(),
					"expiry_minutes": c.GetExpiryMinutes(),
					"note":           c.GetNote(),
					"version":        c.GetVersion(),
				})
				break
			}
		}
	}

	resp, err := h.clients.Payment.UpdatePaymentMethodConfig(ctx, &paymentv1.UpdatePaymentMethodConfigRequest{
		Method:          pm,
		Enabled:         *body.Enabled,
		MinOrderVnd:     body.MinOrderVND,
		MaxOrderVnd:     body.MaxOrderVND,
		ExpiryMinutes:   body.ExpiryMinutes,
		Note:            body.Note,
		ExpectedVersion: *body.ExpectedVersion,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"method": mapAdminPaymentMethodConfig(resp.GetConfig()),
	})
}

func (h *handler) handleAdminDecideRefundApproval(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Approve *bool  `json:"approve"`
		Note    string `json:"note"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	if body.Approve == nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "approve is required")
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Payment.DecideRefundApproval(ctx, &paymentv1.DecideRefundApprovalRequest{
		RefundId: chi.URLParam(r, "id"),
		Approve:  *body.Approve,
		Note:     body.Note,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"refund": mapRefund(resp.GetRefund(), true),
	})
}
