package router

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/gateway/internal/httpx"
)

type orderItemDTO struct {
	ID                    string `json:"id"`
	OrderID               string `json:"order_id"`
	SKUID                 string `json:"sku_id"`
	ProductID             string `json:"product_id"`
	ProductName           string `json:"product_name"`
	SKUName               string `json:"sku_name"`
	UnitPriceVND          int64  `json:"unit_price_vnd"`
	EffectiveUnitPriceVND int64  `json:"effective_unit_price_vnd"`
	Quantity              int32  `json:"quantity"`
	LineTotalVND          int64  `json:"line_total_vnd"`
	WeightGrams           int32  `json:"weight_grams"`
	FlashSaleItemID       string `json:"flash_sale_item_id"`
}

type orderTimelineEventDTO struct {
	ID         string `json:"id"`
	OrderID    string `json:"order_id"`
	FromStatus string `json:"from_status"`
	ToStatus   string `json:"to_status"`
	ActorID    string `json:"actor_id"`
	ActorRole  string `json:"actor_role"`
	Note       string `json:"note"`
	CreatedAt  string `json:"created_at,omitempty"`
}

type orderAddressSnapshotDTO struct {
	RecipientName  string `json:"recipient_name"`
	RecipientPhone string `json:"recipient_phone"`
	ProvinceCode   string `json:"province_code"`
	ProvinceName   string `json:"province_name"`
	WardCode       string `json:"ward_code"`
	WardName       string `json:"ward_name"`
	StreetAddress  string `json:"street_address"`
}

type orderDTO struct {
	ID                         string                   `json:"id"`
	OrderCode                  string                   `json:"order_code"`
	CheckoutGroupID            string                   `json:"checkout_group_id"`
	BuyerID                    string                   `json:"buyer_id"`
	ShopID                     string                   `json:"shop_id"`
	Status                     string                   `json:"status"`
	PaymentMethod              string                   `json:"payment_method"`
	PaymentID                  string                   `json:"payment_id"`
	PaymentStatus              string                   `json:"payment_status"`
	SubtotalVND                int64                    `json:"subtotal_vnd"`
	FlashSaleDiscountVND       int64                    `json:"flash_sale_discount_vnd"`
	ShopVoucherDiscountVND     int64                    `json:"shop_voucher_discount_vnd"`
	PlatformVoucherDiscountVND int64                    `json:"platform_voucher_discount_vnd"`
	FreeshipDiscountVND        int64                    `json:"freeship_discount_vnd"`
	CoinsDiscountVND           int64                    `json:"coins_discount_vnd"`
	ShippingFeeVND             int64                    `json:"shipping_fee_vnd"`
	CommissionVND              int64                    `json:"commission_vnd"`
	TotalPayableVND            int64                    `json:"total_payable_vnd"`
	CarrierCode                string                   `json:"carrier_code"`
	ServiceCode                string                   `json:"service_code"`
	TrackingCode               string                   `json:"tracking_code"`
	LabelURL                   string                   `json:"label_url"`
	CancelReason               string                   `json:"cancel_reason"`
	ShippingAddress            *orderAddressSnapshotDTO `json:"shipping_address,omitempty"`
	Items                      []*orderItemDTO          `json:"items"`
	Timeline                   []*orderTimelineEventDTO `json:"timeline"`
	PaymentExpiresAt           string                   `json:"payment_expires_at,omitempty"`
	CreatedAt                  string                   `json:"created_at,omitempty"`
	UpdatedAt                  string                   `json:"updated_at,omitempty"`
	DeliveredAt                string                   `json:"delivered_at,omitempty"`
	ReturnRequestID            string                   `json:"return_request_id"`
	ReturnStatus               string                   `json:"return_status"`
}

type returnEventDTO struct {
	ID         string `json:"id"`
	FromStatus string `json:"from_status"`
	ToStatus   string `json:"to_status"`
	ActorID    string `json:"actor_id"`
	ActorRole  string `json:"actor_role"`
	Note       string `json:"note"`
	CreatedAt  string `json:"created_at,omitempty"`
}

type returnRequestDTO struct {
	ID                 string            `json:"id"`
	OrderID            string            `json:"order_id"`
	OrderCode          string            `json:"order_code"`
	BuyerID            string            `json:"buyer_id"`
	ShopID             string            `json:"shop_id"`
	Status             string            `json:"status"`
	Reason             string            `json:"reason"`
	EvidenceURLs       []string          `json:"evidence_urls"`
	SellerResponse     string            `json:"seller_response"`
	SellerEvidenceURLs []string          `json:"seller_evidence_urls"`
	DisputeReason      string            `json:"dispute_reason"`
	AdminNote          string            `json:"admin_note"`
	RefundAmountVND    int64             `json:"refund_amount_vnd"`
	ReturnTrackingCode string            `json:"return_tracking_code"`
	ReturnLabelURL     string            `json:"return_label_url"`
	ReturnFeeVND       int64             `json:"return_fee_vnd"`
	ReturnFeeBearer    string            `json:"return_fee_bearer"`
	GoodsReceived      bool              `json:"goods_received"`
	DeadlineAt         string            `json:"deadline_at,omitempty"`
	CreatedAt          string            `json:"created_at,omitempty"`
	UpdatedAt          string            `json:"updated_at,omitempty"`
	Events             []*returnEventDTO `json:"events"`
}

func mapOrderTimelineEvent(e *orderv1.OrderTimelineEvent) *orderTimelineEventDTO {
	if e == nil {
		return nil
	}
	return &orderTimelineEventDTO{
		ID:         e.GetId(),
		OrderID:    e.GetOrderId(),
		FromStatus: orderStatusString(e.GetFromStatus()),
		ToStatus:   orderStatusString(e.GetToStatus()),
		ActorID:    e.GetActorId(),
		ActorRole:  e.GetActorRole(),
		Note:       e.GetNote(),
		CreatedAt:  httpx.FormatTimestamp(e.GetCreatedAt()),
	}
}

func mapOrder(o *orderv1.Order) *orderDTO {
	if o == nil {
		return nil
	}
	items := make([]*orderItemDTO, 0, len(o.GetItems()))
	for _, it := range o.GetItems() {
		items = append(items, &orderItemDTO{
			ID:                    it.GetId(),
			OrderID:               it.GetOrderId(),
			SKUID:                 it.GetSkuId(),
			ProductID:             it.GetProductId(),
			ProductName:           it.GetProductName(),
			SKUName:               it.GetSkuName(),
			UnitPriceVND:          it.GetUnitPriceVnd(),
			EffectiveUnitPriceVND: it.GetEffectiveUnitPriceVnd(),
			Quantity:              it.GetQuantity(),
			LineTotalVND:          it.GetLineTotalVnd(),
			WeightGrams:           it.GetWeightGrams(),
			FlashSaleItemID:       it.GetFlashSaleItemId(),
		})
	}
	timeline := make([]*orderTimelineEventDTO, 0, len(o.GetTimeline()))
	for _, ev := range o.GetTimeline() {
		timeline = append(timeline, mapOrderTimelineEvent(ev))
	}
	var addr *orderAddressSnapshotDTO
	if a := o.GetShippingAddress(); a != nil {
		addr = &orderAddressSnapshotDTO{
			RecipientName:  a.GetRecipientName(),
			RecipientPhone: a.GetRecipientPhone(),
			ProvinceCode:   a.GetProvinceCode(),
			ProvinceName:   a.GetProvinceName(),
			WardCode:       a.GetWardCode(),
			WardName:       a.GetWardName(),
			StreetAddress:  a.GetStreetAddress(),
		}
	}
	return &orderDTO{
		ID:                         o.GetId(),
		OrderCode:                  o.GetOrderCode(),
		CheckoutGroupID:            o.GetCheckoutGroupId(),
		BuyerID:                    o.GetBuyerId(),
		ShopID:                     o.GetShopId(),
		Status:                     orderStatusString(o.GetStatus()),
		PaymentMethod:              orderPaymentMethodString(o.GetPaymentMethod()),
		PaymentID:                  o.GetPaymentId(),
		PaymentStatus:              o.GetPaymentStatus(),
		SubtotalVND:                o.GetSubtotalVnd(),
		FlashSaleDiscountVND:       o.GetFlashSaleDiscountVnd(),
		ShopVoucherDiscountVND:     o.GetShopVoucherDiscountVnd(),
		PlatformVoucherDiscountVND: o.GetPlatformVoucherDiscountVnd(),
		FreeshipDiscountVND:        o.GetFreeshipDiscountVnd(),
		CoinsDiscountVND:           o.GetCoinsDiscountVnd(),
		ShippingFeeVND:             o.GetShippingFeeVnd(),
		CommissionVND:              o.GetCommissionVnd(),
		TotalPayableVND:            o.GetTotalPayableVnd(),
		CarrierCode:                o.GetCarrierCode(),
		ServiceCode:                o.GetServiceCode(),
		TrackingCode:               o.GetTrackingCode(),
		LabelURL:                   o.GetLabelUrl(),
		CancelReason:               o.GetCancelReason(),
		ShippingAddress:            addr,
		Items:                      items,
		Timeline:                   timeline,
		PaymentExpiresAt:           httpx.FormatTimestamp(o.GetPaymentExpiresAt()),
		CreatedAt:                  httpx.FormatTimestamp(o.GetCreatedAt()),
		UpdatedAt:                  httpx.FormatTimestamp(o.GetUpdatedAt()),
		DeliveredAt:                httpx.FormatTimestamp(o.GetDeliveredAt()),
		ReturnRequestID:            o.GetReturnRequestId(),
		ReturnStatus:               returnStatusString(o.GetReturnStatus()),
	}
}

func mapReturnRequest(rr *orderv1.ReturnRequest) *returnRequestDTO {
	if rr == nil {
		return nil
	}
	evURLs := rr.GetEvidenceUrls()
	if evURLs == nil {
		evURLs = []string{}
	}
	sellerEvURLs := rr.GetSellerEvidenceUrls()
	if sellerEvURLs == nil {
		sellerEvURLs = []string{}
	}
	events := make([]*returnEventDTO, 0, len(rr.GetEvents()))
	for _, ev := range rr.GetEvents() {
		events = append(events, &returnEventDTO{
			ID:         ev.GetId(),
			FromStatus: returnStatusString(ev.GetFromStatus()),
			ToStatus:   returnStatusString(ev.GetToStatus()),
			ActorID:    ev.GetActorId(),
			ActorRole:  ev.GetActorRole(),
			Note:       ev.GetNote(),
			CreatedAt:  httpx.FormatTimestamp(ev.GetCreatedAt()),
		})
	}
	return &returnRequestDTO{
		ID:                 rr.GetId(),
		OrderID:            rr.GetOrderId(),
		OrderCode:          rr.GetOrderCode(),
		BuyerID:            rr.GetBuyerId(),
		ShopID:             rr.GetShopId(),
		Status:             returnStatusString(rr.GetStatus()),
		Reason:             rr.GetReason(),
		EvidenceURLs:       evURLs,
		SellerResponse:     rr.GetSellerResponse(),
		SellerEvidenceURLs: sellerEvURLs,
		DisputeReason:      rr.GetDisputeReason(),
		AdminNote:          rr.GetAdminNote(),
		RefundAmountVND:    rr.GetRefundAmountVnd(),
		ReturnTrackingCode: rr.GetReturnTrackingCode(),
		ReturnLabelURL:     rr.GetReturnLabelUrl(),
		ReturnFeeVND:       rr.GetReturnFeeVnd(),
		ReturnFeeBearer:    rr.GetReturnFeeBearer(),
		GoodsReceived:      rr.GetGoodsReceived(),
		DeadlineAt:         httpx.FormatTimestamp(rr.GetDeadlineAt()),
		CreatedAt:          httpx.FormatTimestamp(rr.GetCreatedAt()),
		UpdatedAt:          httpx.FormatTimestamp(rr.GetUpdatedAt()),
		Events:             events,
	}
}

func returnStatusString(st orderv1.ReturnStatus) string {
	switch st {
	case orderv1.ReturnStatus_RETURN_STATUS_PENDING:
		return "PENDING"
	case orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT:
		return "AWAITING_SHIPMENT"
	case orderv1.ReturnStatus_RETURN_STATUS_IN_TRANSIT:
		return "IN_TRANSIT"
	case orderv1.ReturnStatus_RETURN_STATUS_RECEIVED:
		return "RECEIVED"
	case orderv1.ReturnStatus_RETURN_STATUS_REFUNDING:
		return "REFUNDING"
	case orderv1.ReturnStatus_RETURN_STATUS_REFUNDED:
		return "REFUNDED"
	case orderv1.ReturnStatus_RETURN_STATUS_REJECTED:
		return "REJECTED"
	case orderv1.ReturnStatus_RETURN_STATUS_ESCALATED:
		return "ESCALATED"
	case orderv1.ReturnStatus_RETURN_STATUS_EVIDENCE_REQUESTED:
		return "EVIDENCE_REQUESTED"
	case orderv1.ReturnStatus_RETURN_STATUS_CLOSED:
		return "CLOSED"
	default:
		return ""
	}
}

func parseReturnStatus(s string) orderv1.ReturnStatus {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "PENDING", "RETURN_STATUS_PENDING":
		return orderv1.ReturnStatus_RETURN_STATUS_PENDING
	case "AWAITING_SHIPMENT", "RETURN_STATUS_AWAITING_SHIPMENT":
		return orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT
	case "IN_TRANSIT", "RETURN_STATUS_IN_TRANSIT":
		return orderv1.ReturnStatus_RETURN_STATUS_IN_TRANSIT
	case "RECEIVED", "RETURN_STATUS_RECEIVED":
		return orderv1.ReturnStatus_RETURN_STATUS_RECEIVED
	case "REFUNDING", "RETURN_STATUS_REFUNDING":
		return orderv1.ReturnStatus_RETURN_STATUS_REFUNDING
	case "REFUNDED", "RETURN_STATUS_REFUNDED":
		return orderv1.ReturnStatus_RETURN_STATUS_REFUNDED
	case "REJECTED", "RETURN_STATUS_REJECTED":
		return orderv1.ReturnStatus_RETURN_STATUS_REJECTED
	case "ESCALATED", "RETURN_STATUS_ESCALATED":
		return orderv1.ReturnStatus_RETURN_STATUS_ESCALATED
	case "EVIDENCE_REQUESTED", "RETURN_STATUS_EVIDENCE_REQUESTED":
		return orderv1.ReturnStatus_RETURN_STATUS_EVIDENCE_REQUESTED
	case "CLOSED", "RETURN_STATUS_CLOSED":
		return orderv1.ReturnStatus_RETURN_STATUS_CLOSED
	default:
		return orderv1.ReturnStatus_RETURN_STATUS_UNSPECIFIED
	}
}

func parseReturnSellerDecision(s string) orderv1.ReturnSellerDecision {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "ACCEPT_REFUND", "RETURN_SELLER_DECISION_ACCEPT_REFUND":
		return orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_REFUND
	case "ACCEPT_RETURN", "RETURN_SELLER_DECISION_ACCEPT_RETURN":
		return orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_RETURN
	case "REJECT", "RETURN_SELLER_DECISION_REJECT":
		return orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_REJECT
	default:
		return orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_UNSPECIFIED
	}
}

func parseReturnAdminDecision(s string) orderv1.ReturnAdminDecision {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "REFUND", "RETURN_ADMIN_DECISION_REFUND":
		return orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_REFUND
	case "RETURN_AND_REFUND", "RETURN_ADMIN_DECISION_RETURN_AND_REFUND":
		return orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_RETURN_AND_REFUND
	case "REJECT", "RETURN_ADMIN_DECISION_REJECT":
		return orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_REJECT
	case "REQUEST_EVIDENCE", "RETURN_ADMIN_DECISION_REQUEST_EVIDENCE":
		return orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_REQUEST_EVIDENCE
	default:
		return orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_UNSPECIFIED
	}
}

func orderStatusString(st orderv1.OrderStatus) string {
	switch st {
	case orderv1.OrderStatus_ORDER_STATUS_PENDING:
		return "PENDING"
	case orderv1.OrderStatus_ORDER_STATUS_CONFIRMED:
		return "CONFIRMED"
	case orderv1.OrderStatus_ORDER_STATUS_PREPARING:
		return "PREPARING"
	case orderv1.OrderStatus_ORDER_STATUS_SHIPPING:
		return "SHIPPING"
	case orderv1.OrderStatus_ORDER_STATUS_DELIVERED:
		return "DELIVERED"
	case orderv1.OrderStatus_ORDER_STATUS_COMPLETED:
		return "COMPLETED"
	case orderv1.OrderStatus_ORDER_STATUS_CANCELLED:
		return "CANCELLED"
	case orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED:
		return "RETURN_REQUESTED"
	case orderv1.OrderStatus_ORDER_STATUS_RETURNED:
		return "RETURNED"
	default:
		return ""
	}
}

func parseOrderStatus(s string) orderv1.OrderStatus {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "PENDING", "ORDER_STATUS_PENDING":
		return orderv1.OrderStatus_ORDER_STATUS_PENDING
	case "CONFIRMED", "ORDER_STATUS_CONFIRMED":
		return orderv1.OrderStatus_ORDER_STATUS_CONFIRMED
	case "PREPARING", "ORDER_STATUS_PREPARING":
		return orderv1.OrderStatus_ORDER_STATUS_PREPARING
	case "SHIPPING", "ORDER_STATUS_SHIPPING":
		return orderv1.OrderStatus_ORDER_STATUS_SHIPPING
	case "DELIVERED", "ORDER_STATUS_DELIVERED":
		return orderv1.OrderStatus_ORDER_STATUS_DELIVERED
	case "COMPLETED", "ORDER_STATUS_COMPLETED":
		return orderv1.OrderStatus_ORDER_STATUS_COMPLETED
	case "CANCELLED", "ORDER_STATUS_CANCELLED":
		return orderv1.OrderStatus_ORDER_STATUS_CANCELLED
	case "RETURN_REQUESTED", "ORDER_STATUS_RETURN_REQUESTED":
		return orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED
	case "RETURNED", "ORDER_STATUS_RETURNED":
		return orderv1.OrderStatus_ORDER_STATUS_RETURNED
	default:
		return orderv1.OrderStatus_ORDER_STATUS_UNSPECIFIED
	}
}

func orderPaymentMethodString(m orderv1.OrderPaymentMethod) string {
	switch m {
	case orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD:
		return "COD"
	case orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR:
		return "VIETQR"
	default:
		return ""
	}
}

func parseOrderPaymentMethod(s string) orderv1.OrderPaymentMethod {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "COD", "ORDER_PAYMENT_METHOD_COD":
		return orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD
	case "VIETQR", "ORDER_PAYMENT_METHOD_VIETQR":
		return orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR
	default:
		return orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_UNSPECIFIED
	}
}

func (h *handler) handleCheckout(w http.ResponseWriter, r *http.Request) {
	idemKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idemKey == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "MISSING_IDEMPOTENCY_KEY", "Idempotency-Key header is required")
		return
	}

	var body struct {
		AddressID string `json:"address_id"`
		FromCart  bool   `json:"from_cart"`
		Items     []struct {
			SKUID           string `json:"sku_id"`
			Quantity        int32  `json:"quantity"`
			FlashSaleItemID string `json:"flash_sale_item_id"`
		} `json:"items"`
		ShopVoucherIDs    map[string]string `json:"shop_voucher_ids"`
		PlatformVoucherID string            `json:"platform_voucher_id"`
		FreeshipVoucherID string            `json:"freeship_voucher_id"`
		CoinsToUse        int64             `json:"coins_to_use"`
		PaymentMethod     string            `json:"payment_method"`
		ShippingChoices   []struct {
			ShopID      string `json:"shop_id"`
			CarrierCode string `json:"carrier_code"`
			ServiceCode string `json:"service_code"`
		} `json:"shipping_choices"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}

	items := make([]*orderv1.CheckoutItemInput, 0, len(body.Items))
	for _, it := range body.Items {
		items = append(items, &orderv1.CheckoutItemInput{
			SkuId:           it.SKUID,
			Quantity:        it.Quantity,
			FlashSaleItemId: it.FlashSaleItemID,
		})
	}
	choices := make([]*orderv1.ShopShippingChoice, 0, len(body.ShippingChoices))
	for _, c := range body.ShippingChoices {
		choices = append(choices, &orderv1.ShopShippingChoice{
			ShopId:      c.ShopID,
			CarrierCode: c.CarrierCode,
			ServiceCode: c.ServiceCode,
		})
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context(), "idempotency-key", idemKey)
	defer cancel()

	resp, err := h.clients.Order.CreateCheckout(ctx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey:    idemKey,
		BuyerId:           auth.UserIDFromContext(r.Context()),
		AddressId:         body.AddressID,
		FromCart:          body.FromCart,
		Items:             items,
		ShopVoucherIds:    body.ShopVoucherIDs,
		PlatformVoucherId: body.PlatformVoucherID,
		FreeshipVoucherId: body.FreeshipVoucherID,
		CoinsToUse:        body.CoinsToUse,
		PaymentMethod:     parseOrderPaymentMethod(body.PaymentMethod),
		ShippingChoices:   choices,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	orders := make([]*orderDTO, 0, len(resp.GetOrders()))
	for _, o := range resp.GetOrders() {
		orders = append(orders, mapOrder(o))
	}

	statusCode := http.StatusCreated
	if resp.GetIdempotentReplay() {
		statusCode = http.StatusOK
	}

	httpx.WriteJSON(w, statusCode, map[string]any{
		"checkout_group_id":  resp.GetCheckoutGroupId(),
		"orders":             orders,
		"payment_id":         resp.GetPaymentId(),
		"payment_method":     orderPaymentMethodString(resp.GetPaymentMethod()),
		"payment_status":     resp.GetPaymentStatus(),
		"qr_url":             resp.GetQrUrl(),
		"qr_code":            resp.GetQrCode(),
		"payment_expires_at": httpx.FormatTimestamp(resp.GetPaymentExpiresAt()),
		"total_payable_vnd":  resp.GetTotalPayableVnd(),
		"idempotent_replay":  resp.GetIdempotentReplay(),
	})
}

func (h *handler) handleListBuyerOrders(w http.ResponseWriter, r *http.Request) {
	page, pageSize := httpx.ParsePagination(r)
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Order.ListOrders(ctx, &orderv1.ListOrdersRequest{
		BuyerId:   auth.UserIDFromContext(r.Context()),
		Status:    parseOrderStatus(r.URL.Query().Get("status")),
		RoleScope: "buyer",
		Page:      page,
		PageSize:  pageSize,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	items := make([]*orderDTO, 0, len(resp.GetOrders()))
	for _, o := range resp.GetOrders() {
		items = append(items, mapOrder(o))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items":     items,
		"page":      page,
		"page_size": pageSize,
		"total":     resp.GetTotal(),
	})
}

func (h *handler) handleGetOrder(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Order.GetOrder(ctx, &orderv1.GetOrderRequest{
		OrderId:     chi.URLParam(r, "id"),
		ActorId:     auth.UserIDFromContext(r.Context()),
		ActorRole:   auth.RoleFromContext(r.Context()),
		ActorShopId: auth.ShopIDFromContext(r.Context()),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"order": mapOrder(resp.GetOrder())})
}

func (h *handler) handleGetOrderTimeline(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Order.GetOrderTimeline(ctx, &orderv1.GetOrderTimelineRequest{
		OrderId:     chi.URLParam(r, "id"),
		ActorId:     auth.UserIDFromContext(r.Context()),
		ActorRole:   auth.RoleFromContext(r.Context()),
		ActorShopId: auth.ShopIDFromContext(r.Context()),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	events := make([]*orderTimelineEventDTO, 0, len(resp.GetEvents()))
	for _, ev := range resp.GetEvents() {
		events = append(events, mapOrderTimelineEvent(ev))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (h *handler) handleCancelOrder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reason string `json:"reason"`
	}
	if err := httpx.DecodeOptionalJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Order.CancelOrder(ctx, &orderv1.CancelOrderRequest{
		OrderId:     chi.URLParam(r, "id"),
		ActorId:     auth.UserIDFromContext(r.Context()),
		ActorRole:   auth.RoleFromContext(r.Context()),
		ActorShopId: auth.ShopIDFromContext(r.Context()),
		Reason:      body.Reason,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"order": mapOrder(resp.GetOrder())})
}

func (h *handler) handleConfirmReceived(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Order.BuyerConfirmReceived(ctx, &orderv1.BuyerConfirmReceivedRequest{
		OrderId: chi.URLParam(r, "id"),
		BuyerId: auth.UserIDFromContext(r.Context()),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"order": mapOrder(resp.GetOrder())})
}

func (h *handler) handleRequestReturn(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reason       string   `json:"reason"`
		EvidenceURLs []string `json:"evidence_urls"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Order.RequestReturn(ctx, &orderv1.RequestReturnRequest{
		OrderId:      chi.URLParam(r, "id"),
		BuyerId:      auth.UserIDFromContext(r.Context()),
		Reason:       body.Reason,
		EvidenceUrls: body.EvidenceURLs,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"order":             mapOrder(resp.GetOrder()),
		"return_request_id": resp.GetReturnRequestId(),
	})
}

func (h *handler) handleSellerListOrders(w http.ResponseWriter, r *http.Request) {
	page, pageSize := httpx.ParsePagination(r)
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Order.ListOrders(ctx, &orderv1.ListOrdersRequest{
		ShopId:    auth.ShopIDFromContext(r.Context()),
		Status:    parseOrderStatus(r.URL.Query().Get("status")),
		RoleScope: "seller",
		Page:      page,
		PageSize:  pageSize,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	items := make([]*orderDTO, 0, len(resp.GetOrders()))
	for _, o := range resp.GetOrders() {
		items = append(items, mapOrder(o))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items":     items,
		"page":      page,
		"page_size": pageSize,
		"total":     resp.GetTotal(),
	})
}

func (h *handler) handleSellerGetOrder(w http.ResponseWriter, r *http.Request) {
	shopID := auth.ShopIDFromContext(r.Context())
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Order.GetOrder(ctx, &orderv1.GetOrderRequest{
		OrderId:     chi.URLParam(r, "id"),
		ActorId:     auth.UserIDFromContext(r.Context()),
		ActorRole:   auth.RoleFromContext(r.Context()),
		ActorShopId: shopID,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	ord := resp.GetOrder()
	if ord == nil {
		httpx.WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "order not found")
		return
	}
	if ord.GetShopId() != shopID {
		httpx.WriteError(w, r, http.StatusForbidden, "PERMISSION_DENIED", "cannot access another shop's order")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"order": mapOrder(ord)})
}

func (h *handler) handleSellerConfirmOrder(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Order.SellerConfirmOrder(ctx, &orderv1.SellerConfirmOrderRequest{
		OrderId:      chi.URLParam(r, "id"),
		ShopId:       auth.ShopIDFromContext(r.Context()),
		SellerUserId: auth.UserIDFromContext(r.Context()),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"order": mapOrder(resp.GetOrder())})
}

func (h *handler) handleSellerShipOrder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		CarrierCode string `json:"carrier_code"`
		ServiceCode string `json:"service_code"`
	}
	if err := httpx.DecodeOptionalJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Order.SellerShipOrder(ctx, &orderv1.SellerShipOrderRequest{
		OrderId:      chi.URLParam(r, "id"),
		ShopId:       auth.ShopIDFromContext(r.Context()),
		SellerUserId: auth.UserIDFromContext(r.Context()),
		CarrierCode:  body.CarrierCode,
		ServiceCode:  body.ServiceCode,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"order":         mapOrder(resp.GetOrder()),
		"tracking_code": resp.GetTrackingCode(),
		"label_url":     resp.GetLabelUrl(),
	})
}

func (h *handler) handleAdminListOrders(w http.ResponseWriter, r *http.Request) {
	page, pageSize := httpx.ParsePagination(r)
	q := r.URL.Query()
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Order.ListOrders(ctx, &orderv1.ListOrdersRequest{
		BuyerId:   q.Get("buyer_id"),
		ShopId:    q.Get("shop_id"),
		Status:    parseOrderStatus(q.Get("status")),
		RoleScope: "admin",
		Page:      page,
		PageSize:  pageSize,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	items := make([]*orderDTO, 0, len(resp.GetOrders()))
	for _, o := range resp.GetOrders() {
		items = append(items, mapOrder(o))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items":     items,
		"page":      page,
		"page_size": pageSize,
		"total":     resp.GetTotal(),
	})
}

func (h *handler) handleGetOrderReturn(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Order.GetReturnRequest(ctx, &orderv1.GetReturnRequestRequest{
		OrderId: chi.URLParam(r, "id"),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"return_request": mapReturnRequest(resp.GetReturnRequest())})
}

func (h *handler) handleGetReturn(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Order.GetReturnRequest(ctx, &orderv1.GetReturnRequestRequest{
		ReturnRequestId: chi.URLParam(r, "id"),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"return_request": mapReturnRequest(resp.GetReturnRequest())})
}

func (h *handler) handleListReturnsByScope(w http.ResponseWriter, r *http.Request, scope string) {
	page, pageSize := httpx.ParsePagination(r)
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Order.ListReturnRequests(ctx, &orderv1.ListReturnRequestsRequest{
		RoleScope: scope,
		Status:    parseReturnStatus(r.URL.Query().Get("status")),
		Page:      page,
		PageSize:  pageSize,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	items := make([]*returnRequestDTO, 0, len(resp.GetReturnRequests()))
	for _, rr := range resp.GetReturnRequests() {
		items = append(items, mapReturnRequest(rr))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items":     items,
		"page":      page,
		"page_size": pageSize,
		"total":     resp.GetTotal(),
	})
}

func (h *handler) handleListBuyerReturns(w http.ResponseWriter, r *http.Request) {
	h.handleListReturnsByScope(w, r, "buyer")
}

func (h *handler) handleEscalateReturn(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reason       string   `json:"reason"`
		EvidenceURLs []string `json:"evidence_urls"`
	}
	if err := httpx.DecodeOptionalJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Order.EscalateReturn(ctx, &orderv1.EscalateReturnRequest{
		ReturnRequestId: chi.URLParam(r, "id"),
		Reason:          body.Reason,
		EvidenceUrls:    body.EvidenceURLs,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"return_request": mapReturnRequest(resp.GetReturnRequest())})
}

func (h *handler) handleAddReturnEvidence(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Note         string   `json:"note"`
		EvidenceURLs []string `json:"evidence_urls"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Order.AddReturnEvidence(ctx, &orderv1.AddReturnEvidenceRequest{
		ReturnRequestId: chi.URLParam(r, "id"),
		Note:            body.Note,
		EvidenceUrls:    body.EvidenceURLs,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"return_request": mapReturnRequest(resp.GetReturnRequest())})
}

func (h *handler) handleSellerListReturns(w http.ResponseWriter, r *http.Request) {
	h.handleListReturnsByScope(w, r, "seller")
}

func (h *handler) handleSellerGetReturn(w http.ResponseWriter, r *http.Request) {
	shopID := auth.ShopIDFromContext(r.Context())
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Order.GetReturnRequest(ctx, &orderv1.GetReturnRequestRequest{
		ReturnRequestId: chi.URLParam(r, "id"),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	rr := resp.GetReturnRequest()
	if rr == nil {
		httpx.WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "return request not found")
		return
	}
	if rr.GetShopId() != shopID {
		httpx.WriteError(w, r, http.StatusForbidden, "PERMISSION_DENIED", "cannot access another shop's return request")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"return_request": mapReturnRequest(rr)})
}

func (h *handler) handleSellerRespondReturn(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Decision     string   `json:"decision"`
		Note         string   `json:"note"`
		EvidenceURLs []string `json:"evidence_urls"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Order.RespondReturn(ctx, &orderv1.RespondReturnRequest{
		ReturnRequestId: chi.URLParam(r, "id"),
		Decision:        parseReturnSellerDecision(body.Decision),
		Note:            body.Note,
		EvidenceUrls:    body.EvidenceURLs,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"return_request": mapReturnRequest(resp.GetReturnRequest())})
}

func (h *handler) handleSellerConfirmReturnReceived(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Order.ConfirmReturnReceived(ctx, &orderv1.ConfirmReturnReceivedRequest{
		ReturnRequestId: chi.URLParam(r, "id"),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"return_request": mapReturnRequest(resp.GetReturnRequest())})
}

func (h *handler) handleSellerDisputeReturnGoods(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reason       string   `json:"reason"`
		EvidenceURLs []string `json:"evidence_urls"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Order.DisputeReturnGoods(ctx, &orderv1.DisputeReturnGoodsRequest{
		ReturnRequestId: chi.URLParam(r, "id"),
		Reason:          body.Reason,
		EvidenceUrls:    body.EvidenceURLs,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"return_request": mapReturnRequest(resp.GetReturnRequest())})
}

func (h *handler) handleAdminListReturns(w http.ResponseWriter, r *http.Request) {
	h.handleListReturnsByScope(w, r, "admin")
}

func (h *handler) handleAdminGetReturn(w http.ResponseWriter, r *http.Request) {
	h.handleGetReturn(w, r)
}

func (h *handler) handleAdminResolveReturn(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Decision        string `json:"decision"`
		Note            string `json:"note"`
		ReturnFeeBearer string `json:"return_fee_bearer"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Order.AdminResolveReturn(ctx, &orderv1.AdminResolveReturnRequest{
		ReturnRequestId: chi.URLParam(r, "id"),
		Decision:        parseReturnAdminDecision(body.Decision),
		Note:            body.Note,
		ReturnFeeBearer: strings.ToUpper(strings.TrimSpace(body.ReturnFeeBearer)),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"return_request": mapReturnRequest(resp.GetReturnRequest())})
}
