package router

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/gateway/internal/httpx"
)

func (h *handler) mountOrderDepthWebhooks(main chi.Router, deps Dependencies) {
	if deps.PaymentWebhookProxy != nil {
		main.Post("/webhooks/zalopay", deps.PaymentWebhookProxy.ServeHTTP)
	}
}

func (h *handler) mountOrderDepthAuthedRoutes(authed chi.Router) {
	authed.Post("/orders/{id}/returns", h.handleCreateOrderReturn)
	authed.Get("/orders/{id}/returns", h.handleListOrderReturns)
	authed.Post("/orders/{id}/complaints", h.handleCreateOrderComplaint)
}

func (h *handler) mountOrderDepthSellerRoutes(seller chi.Router) {
	seller.Get("/returns", h.handleSellerListReturns)
	seller.Post("/returns/{id}/approve", h.handleSellerApproveReturn)
	seller.Post("/returns/{id}/reject", h.handleSellerRejectReturn)
	seller.Post("/returns/{id}/confirm-received", h.handleSellerConfirmReturnReceived)
}

func (h *handler) mountOrderDepthAdminRoutes(admin chi.Router) {
	admin.Get("/complaints", h.handleAdminListComplaints)
	admin.Post("/complaints/{id}/resolve", h.handleAdminResolveComplaint)
	admin.Get("/payments/reconciliation-reports", h.handleAdminListReconciliationReports)
}

type returnItemDTO struct {
	OrderItemID string `json:"order_item_id,omitempty"`
	SKUID       string `json:"sku_id,omitempty"`
	Quantity    int32  `json:"quantity"`
}

type returnRequestDTO struct {
	ID                  string           `json:"id"`
	OrderID             string           `json:"order_id"`
	BuyerID             string           `json:"buyer_id"`
	ShopID              string           `json:"shop_id"`
	Items               []*returnItemDTO `json:"items"`
	Reason              string           `json:"reason"`
	Photos              []string         `json:"photos"`
	Status              string           `json:"status"`
	SellerNote          string           `json:"seller_note,omitempty"`
	ReverseTrackingCode string           `json:"reverse_tracking_code,omitempty"`
	ReverseLabelURL     string           `json:"reverse_label_url,omitempty"`
	RefundAmountVND     int64            `json:"refund_amount_vnd"`
	RefundID            string           `json:"refund_id,omitempty"`
	CreatedAt           string           `json:"created_at,omitempty"`
	UpdatedAt           string           `json:"updated_at,omitempty"`
}

type complaintDTO struct {
	ID              string   `json:"id"`
	ReturnRequestID string   `json:"return_request_id"`
	OrderID         string   `json:"order_id"`
	BuyerID         string   `json:"buyer_id"`
	ShopID          string   `json:"shop_id"`
	Reason          string   `json:"reason"`
	EvidenceURLs    []string `json:"evidence_urls"`
	Status          string   `json:"status"`
	Resolution      string   `json:"resolution,omitempty"`
	AdminNote       string   `json:"admin_note,omitempty"`
	ResolvedBy      string   `json:"resolved_by,omitempty"`
	CreatedAt       string   `json:"created_at,omitempty"`
	ResolvedAt      string   `json:"resolved_at,omitempty"`
}

type reconciliationReportSummaryDTO struct {
	RunID                string `json:"run_id"`
	Provider             string `json:"provider"`
	RunDate              string `json:"run_date"`
	SalesChannel         string `json:"sales_channel"`
	MatchedCount         int32  `json:"matched_count"`
	MissingLocalCount    int32  `json:"missing_local_count"`
	MissingProviderCount int32  `json:"missing_provider_count"`
	AmountMismatchCount  int32  `json:"amount_mismatch_count"`
	AutoHealedCount      int32  `json:"auto_healed_count"`
	Status               string `json:"status"`
	CreatedAt            string `json:"created_at,omitempty"`
	CompletedAt          string `json:"completed_at,omitempty"`
}

func returnStatusString(st orderv1.ReturnRequestStatus) string {
	switch st {
	case orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_PENDING:
		return "PENDING"
	case orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_APPROVED:
		return "APPROVED"
	case orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_REJECTED:
		return "REJECTED"
	case orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_RETURNING:
		return "RETURNING"
	case orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_RETURNED:
		return "RETURNED"
	case orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_REFUNDED:
		return "REFUNDED"
	case orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_ESCALATED:
		return "ESCALATED"
	default:
		return ""
	}
}

func parseReturnStatus(s string) orderv1.ReturnRequestStatus {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "PENDING", "RETURN_REQUEST_STATUS_PENDING":
		return orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_PENDING
	case "APPROVED", "RETURN_REQUEST_STATUS_APPROVED":
		return orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_APPROVED
	case "REJECTED", "RETURN_REQUEST_STATUS_REJECTED":
		return orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_REJECTED
	case "RETURNING", "RETURN_REQUEST_STATUS_RETURNING":
		return orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_RETURNING
	case "RETURNED", "RETURN_REQUEST_STATUS_RETURNED":
		return orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_RETURNED
	case "REFUNDED", "RETURN_REQUEST_STATUS_REFUNDED":
		return orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_REFUNDED
	case "ESCALATED", "RETURN_REQUEST_STATUS_ESCALATED":
		return orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_ESCALATED
	default:
		return orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_UNSPECIFIED
	}
}

func complaintStatusString(st orderv1.ComplaintStatus) string {
	switch st {
	case orderv1.ComplaintStatus_COMPLAINT_STATUS_OPEN:
		return "OPEN"
	case orderv1.ComplaintStatus_COMPLAINT_STATUS_RESOLVED:
		return "RESOLVED"
	case orderv1.ComplaintStatus_COMPLAINT_STATUS_DISMISSED:
		return "DISMISSED"
	default:
		return ""
	}
}

func parseComplaintStatus(s string) orderv1.ComplaintStatus {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "OPEN", "COMPLAINT_STATUS_OPEN":
		return orderv1.ComplaintStatus_COMPLAINT_STATUS_OPEN
	case "RESOLVED", "COMPLAINT_STATUS_RESOLVED":
		return orderv1.ComplaintStatus_COMPLAINT_STATUS_RESOLVED
	case "DISMISSED", "COMPLAINT_STATUS_DISMISSED":
		return orderv1.ComplaintStatus_COMPLAINT_STATUS_DISMISSED
	default:
		return orderv1.ComplaintStatus_COMPLAINT_STATUS_UNSPECIFIED
	}
}

func complaintResolutionString(r orderv1.ComplaintResolution) string {
	switch r {
	case orderv1.ComplaintResolution_COMPLAINT_RESOLUTION_REFUND:
		return "REFUND"
	case orderv1.ComplaintResolution_COMPLAINT_RESOLUTION_REPLACE:
		return "REPLACE"
	case orderv1.ComplaintResolution_COMPLAINT_RESOLUTION_DISMISS:
		return "DISMISS"
	default:
		return ""
	}
}

func parseComplaintResolution(s string) orderv1.ComplaintResolution {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "REFUND", "COMPLAINT_RESOLUTION_REFUND", "APPROVE", "APPROVE_REFUND",
		"REFUND_BUYER", "PARTIAL_REFUND", "APPROVE_RETURN":
		return orderv1.ComplaintResolution_COMPLAINT_RESOLUTION_REFUND
	case "REPLACE", "EXCHANGE", "COMPLAINT_RESOLUTION_REPLACE":
		return orderv1.ComplaintResolution_COMPLAINT_RESOLUTION_REPLACE
	case "DISMISS", "REJECT", "DENY", "REJECT_COMPLAINT", "COMPLAINT_RESOLUTION_DISMISS":
		return orderv1.ComplaintResolution_COMPLAINT_RESOLUTION_DISMISS
	default:
		return orderv1.ComplaintResolution_COMPLAINT_RESOLUTION_UNSPECIFIED
	}
}

func mapReturnRequest(rr *orderv1.ReturnRequest) *returnRequestDTO {
	if rr == nil {
		return nil
	}
	items := make([]*returnItemDTO, 0, len(rr.GetItems()))
	for _, it := range rr.GetItems() {
		items = append(items, &returnItemDTO{
			OrderItemID: it.GetOrderItemId(),
			SKUID:       it.GetSkuId(),
			Quantity:    it.GetQuantity(),
		})
	}
	photos := rr.GetPhotos()
	if photos == nil {
		photos = []string{}
	}
	return &returnRequestDTO{
		ID:                  rr.GetId(),
		OrderID:             rr.GetOrderId(),
		BuyerID:             rr.GetBuyerId(),
		ShopID:              rr.GetShopId(),
		Items:               items,
		Reason:              rr.GetReason(),
		Photos:              photos,
		Status:              returnStatusString(rr.GetStatus()),
		SellerNote:          rr.GetSellerNote(),
		ReverseTrackingCode: rr.GetReverseTrackingCode(),
		ReverseLabelURL:     rr.GetReverseLabelUrl(),
		RefundAmountVND:     rr.GetRefundAmountVnd(),
		RefundID:            rr.GetRefundId(),
		CreatedAt:           httpx.FormatTimestamp(rr.GetCreatedAt()),
		UpdatedAt:           httpx.FormatTimestamp(rr.GetUpdatedAt()),
	}
}

func mapComplaint(c *orderv1.Complaint) *complaintDTO {
	if c == nil {
		return nil
	}
	ev := c.GetEvidenceUrls()
	if ev == nil {
		ev = []string{}
	}
	return &complaintDTO{
		ID:              c.GetId(),
		ReturnRequestID: c.GetReturnRequestId(),
		OrderID:         c.GetOrderId(),
		BuyerID:         c.GetBuyerId(),
		ShopID:          c.GetShopId(),
		Reason:          c.GetReason(),
		EvidenceURLs:    ev,
		Status:          complaintStatusString(c.GetStatus()),
		Resolution:      complaintResolutionString(c.GetResolution()),
		AdminNote:       c.GetAdminNote(),
		ResolvedBy:      c.GetResolvedBy(),
		CreatedAt:       httpx.FormatTimestamp(c.GetCreatedAt()),
		ResolvedAt:      httpx.FormatTimestamp(c.GetResolvedAt()),
	}
}

func (h *handler) handleCreateOrderReturn(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Items []struct {
			OrderItemID string `json:"order_item_id"`
			SKUID       string `json:"sku_id"`
			Quantity    int32  `json:"quantity"`
		} `json:"items"`
		Reason       string   `json:"reason"`
		Photos       []string `json:"photos"`
		EvidenceURLs []string `json:"evidence_urls"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
		return
	}
	photos := body.Photos
	if len(photos) == 0 && len(body.EvidenceURLs) > 0 {
		photos = body.EvidenceURLs
	}
	pbItems := make([]*orderv1.ReturnItemInput, 0, len(body.Items))
	for _, it := range body.Items {
		pbItems = append(pbItems, &orderv1.ReturnItemInput{
			OrderItemId: it.OrderItemID,
			SkuId:       it.SKUID,
			Quantity:    it.Quantity,
		})
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Order.RequestReturn(ctx, &orderv1.RequestReturnRequest{
		OrderId:      chi.URLParam(r, "id"),
		BuyerId:      auth.UserIDFromContext(r.Context()),
		Reason:       body.Reason,
		EvidenceUrls: photos,
		Items:        pbItems,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"order":             mapOrder(resp.GetOrder()),
		"return_request_id": resp.GetReturnRequestId(),
		"return_request":    mapReturnRequest(resp.GetReturnRequest()),
	})
}

func (h *handler) handleListOrderReturns(w http.ResponseWriter, r *http.Request) {
	page, pageSize := httpx.ParsePagination(r)
	q := r.URL.Query()
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Order.ListOrderReturns(ctx, &orderv1.ListOrderReturnsRequest{
		OrderId:  chi.URLParam(r, "id"),
		Status:   parseReturnStatus(q.Get("status")),
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	items := make([]*returnRequestDTO, 0, len(resp.GetReturns()))
	for _, rr := range resp.GetReturns() {
		items = append(items, mapReturnRequest(rr))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items":     items,
		"page":      page,
		"page_size": pageSize,
		"total":     resp.GetTotal(),
	})
}

func (h *handler) handleCreateOrderComplaint(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ReturnRequestID string   `json:"return_request_id"`
		Reason          string   `json:"reason"`
		EvidenceURLs    []string `json:"evidence_urls"`
		Photos          []string `json:"photos"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
		return
	}
	evidence := body.EvidenceURLs
	if len(evidence) == 0 && len(body.Photos) > 0 {
		evidence = body.Photos
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Order.CreateComplaint(ctx, &orderv1.CreateComplaintRequest{
		OrderId:         chi.URLParam(r, "id"),
		ReturnRequestId: body.ReturnRequestID,
		BuyerId:         auth.UserIDFromContext(r.Context()),
		Reason:          body.Reason,
		EvidenceUrls:    evidence,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"complaint": mapComplaint(resp.GetComplaint()),
	})
}

func (h *handler) handleSellerListReturns(w http.ResponseWriter, r *http.Request) {
	shopID, ok := requireSellerShopID(w, r)
	if !ok {
		return
	}
	page, pageSize := httpx.ParsePagination(r)
	q := r.URL.Query()
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Order.ListOrderReturns(ctx, &orderv1.ListOrderReturnsRequest{
		OrderId:  q.Get("order_id"),
		ShopId:   shopID,
		Status:   parseReturnStatus(q.Get("status")),
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	items := make([]*returnRequestDTO, 0, len(resp.GetReturns()))
	for _, rr := range resp.GetReturns() {
		items = append(items, mapReturnRequest(rr))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items":     items,
		"page":      page,
		"page_size": pageSize,
		"total":     resp.GetTotal(),
	})
}

func (h *handler) handleSellerApproveReturn(w http.ResponseWriter, r *http.Request) {
	shopID, ok := requireSellerShopID(w, r)
	if !ok {
		return
	}
	var body struct {
		Note            string `json:"note"`
		ImmediateRefund bool   `json:"immediate_refund"`
		CarrierCode     string `json:"carrier_code"`
		ServiceCode     string `json:"service_code"`
	}
	if err := httpx.DecodeOptionalJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Order.ApproveReturn(ctx, &orderv1.ApproveReturnRequest{
		ReturnRequestId: chi.URLParam(r, "id"),
		ShopId:          shopID,
		SellerUserId:    auth.UserIDFromContext(r.Context()),
		Note:            body.Note,
		ImmediateRefund: body.ImmediateRefund,
		CarrierCode:     body.CarrierCode,
		ServiceCode:     body.ServiceCode,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"order":          mapOrder(resp.GetOrder()),
		"return_request": mapReturnRequest(resp.GetReturnRequest()),
	})
}

func (h *handler) handleSellerRejectReturn(w http.ResponseWriter, r *http.Request) {
	shopID, ok := requireSellerShopID(w, r)
	if !ok {
		return
	}
	var body struct {
		Reason string `json:"reason"`
		Note   string `json:"note"`
	}
	if err := httpx.DecodeOptionalJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
		return
	}
	reason := strings.TrimSpace(body.Reason)
	if reason == "" {
		reason = strings.TrimSpace(body.Note)
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Order.RejectReturn(ctx, &orderv1.RejectReturnRequest{
		ReturnRequestId: chi.URLParam(r, "id"),
		ShopId:          shopID,
		SellerUserId:    auth.UserIDFromContext(r.Context()),
		Reason:          reason,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"order":          mapOrder(resp.GetOrder()),
		"return_request": mapReturnRequest(resp.GetReturnRequest()),
		"complaint":      mapComplaint(resp.GetComplaint()),
	})
}

func (h *handler) handleSellerConfirmReturnReceived(w http.ResponseWriter, r *http.Request) {
	shopID, ok := requireSellerShopID(w, r)
	if !ok {
		return
	}
	var body struct {
		Note string `json:"note"`
	}
	if err := httpx.DecodeOptionalJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Order.ConfirmReturnReceived(ctx, &orderv1.ConfirmReturnReceivedRequest{
		ReturnRequestId: chi.URLParam(r, "id"),
		ShopId:          shopID,
		SellerUserId:    auth.UserIDFromContext(r.Context()),
		Note:            body.Note,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"order":          mapOrder(resp.GetOrder()),
		"return_request": mapReturnRequest(resp.GetReturnRequest()),
	})
}

func (h *handler) handleAdminListComplaints(w http.ResponseWriter, r *http.Request) {
	page, pageSize := httpx.ParsePagination(r)
	q := r.URL.Query()
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Order.ListComplaints(ctx, &orderv1.ListComplaintsRequest{
		Status:   parseComplaintStatus(q.Get("status")),
		OrderId:  q.Get("order_id"),
		ShopId:   q.Get("shop_id"),
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	items := make([]*complaintDTO, 0, len(resp.GetComplaints()))
	for _, c := range resp.GetComplaints() {
		items = append(items, mapComplaint(c))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items":     items,
		"page":      page,
		"page_size": pageSize,
		"total":     resp.GetTotal(),
	})
}

func (h *handler) handleAdminResolveComplaint(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Resolution            string `json:"resolution"`
		Decision              string `json:"decision"`
		AdminNote             string `json:"admin_note"`
		Note                  string `json:"note"`
		RequireReturnShipping *bool  `json:"require_return_shipping"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
		return
	}
	rawResolution := strings.TrimSpace(body.Resolution)
	if rawResolution == "" {
		rawResolution = strings.TrimSpace(body.Decision)
	}
	resolution := parseComplaintResolution(rawResolution)
	if resolution == orderv1.ComplaintResolution_COMPLAINT_RESOLUTION_UNSPECIFIED {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "resolution must be refund, replace, or dismiss")
		return
	}
	adminNote := strings.TrimSpace(body.AdminNote)
	if adminNote == "" {
		adminNote = strings.TrimSpace(body.Note)
	}
	requireReturnShipping := false
	if body.RequireReturnShipping != nil {
		requireReturnShipping = *body.RequireReturnShipping
	} else if strings.EqualFold(strings.TrimSpace(body.Decision), "APPROVE_RETURN") {
		requireReturnShipping = true
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Order.ResolveComplaint(ctx, &orderv1.ResolveComplaintRequest{
		ComplaintId:           chi.URLParam(r, "id"),
		Resolution:            resolution,
		AdminNote:             adminNote,
		RequireReturnShipping: requireReturnShipping,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"complaint":      mapComplaint(resp.GetComplaint()),
		"order":          mapOrder(resp.GetOrder()),
		"return_request": mapReturnRequest(resp.GetReturnRequest()),
	})
}

func (h *handler) handleAdminListReconciliationReports(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	salesChannel, errMsg := validateSalesChannelFilter(q.Get("sales_channel"))
	if errMsg != "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", errMsg)
		return
	}
	var limit int32 = 30
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			if parsed > 100 {
				parsed = 100
			}
			limit = int32(parsed)
		}
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Payment.ListReconciliationReports(ctx, &paymentv1.ListReconciliationReportsRequest{
		Limit:        limit,
		SalesChannel: salesChannel,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	reports := make([]*reconciliationReportSummaryDTO, 0, len(resp.GetReports()))
	for _, rp := range resp.GetReports() {
		reports = append(reports, &reconciliationReportSummaryDTO{
			RunID:                rp.GetRunId(),
			Provider:             rp.GetProvider(),
			RunDate:              rp.GetRunDate(),
			SalesChannel:         rp.GetSalesChannel(),
			MatchedCount:         rp.GetMatchedCount(),
			MissingLocalCount:    rp.GetMissingLocalCount(),
			MissingProviderCount: rp.GetMissingProviderCount(),
			AmountMismatchCount:  rp.GetAmountMismatchCount(),
			AutoHealedCount:      rp.GetAutoHealedCount(),
			Status:               rp.GetStatus(),
			CreatedAt:            httpx.FormatTimestamp(rp.GetCreatedAt()),
			CompletedAt:          httpx.FormatTimestamp(rp.GetCompletedAt()),
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"reports": reports,
	})
}
