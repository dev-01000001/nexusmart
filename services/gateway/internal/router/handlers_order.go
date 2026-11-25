package router

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/go-chi/chi/v5"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/gateway/internal/httpx"
	"github.com/nexusmart/core/services/gateway/internal/middleware"
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
	CommissionBps         int32  `json:"commission_bps"`
	ShopProgramItemID     string `json:"shop_program_item_id"`
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
	ShopProgramDiscountVND     int64                    `json:"shop_program_discount_vnd"`
	ShopVoucherDiscountVND     int64                    `json:"shop_voucher_discount_vnd"`
	ShopFreeshipDiscountVND    int64                    `json:"shop_freeship_discount_vnd"`
	PlatformVoucherDiscountVND int64                    `json:"platform_voucher_discount_vnd"`
	FreeshipDiscountVND        int64                    `json:"freeship_discount_vnd"`
	CoinsDiscountVND           int64                    `json:"coins_discount_vnd"`
	ShippingFeeVND             int64                    `json:"shipping_fee_vnd"`
	CommissionVND              int64                    `json:"commission_vnd"`
	PaymentFeeVND              int64                    `json:"payment_fee_vnd"`
	PaymentFeeBps              int32                    `json:"payment_fee_bps"`
	NeedsManualReview          bool                     `json:"needs_manual_review"`
	ShipmentJobStatus          string                   `json:"shipment_job_status,omitempty"`
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

func mapOrderAddressSnapshot(a *orderv1.OrderAddressSnapshot) *orderAddressSnapshotDTO {
	if a == nil {
		return nil
	}
	return &orderAddressSnapshotDTO{
		RecipientName:  a.GetRecipientName(),
		RecipientPhone: a.GetRecipientPhone(),
		ProvinceCode:   a.GetProvinceCode(),
		ProvinceName:   a.GetProvinceName(),
		WardCode:       a.GetWardCode(),
		WardName:       a.GetWardName(),
		StreetAddress:  a.GetStreetAddress(),
	}
}

func mapOrderForBuyer(o *orderv1.Order) *orderDTO {
	return mapOrderView(o, false, false)
}

func mapOrderForSeller(o *orderv1.Order) *orderDTO {
	return mapOrderView(o, true, false)
}

func mapOrderForAdmin(o *orderv1.Order) *orderDTO {
	return mapOrderView(o, true, true)
}

// mapOrderForCaller applies role-scoped visibility on shared order endpoints:
// admins see fees, manual-review flag, and shipment job status; the selling shop owner
// sees fees and shipment job status; buyers (including sellers purchasing from another shop)
// get zero values for platform/seller fee fields and internal operational flags.
func mapOrderForCaller(ctx context.Context, o *orderv1.Order) *orderDTO {
	claims, _ := auth.ClaimsFromContext(ctx)
	isAdmin := auth.HasCapability(claims, auth.CapOrdersRead)
	isSellerOwner := o != nil && auth.HasRole(claims, "seller") && claims.ShopID != "" && claims.ShopID == o.GetShopId()
	return mapOrderView(o, isAdmin || isSellerOwner, isAdmin)
}

func mapOrderView(o *orderv1.Order, showFees, isAdmin bool) *orderDTO {
	if o == nil {
		return nil
	}
	items := make([]*orderItemDTO, 0, len(o.GetItems()))
	for _, it := range o.GetItems() {
		var commBps int32
		if showFees {
			commBps = it.GetCommissionBps()
		}
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
			CommissionBps:         commBps,
			ShopProgramItemID:     it.GetShopProgramItemId(),
		})
	}
	timeline := make([]*orderTimelineEventDTO, 0, len(o.GetTimeline()))
	for _, ev := range o.GetTimeline() {
		timeline = append(timeline, mapOrderTimelineEvent(ev))
	}
	addr := mapOrderAddressSnapshot(o.GetShippingAddress())
	var commVND, payFeeVND int64
	var payFeeBps int32
	var shipmentJobStatus string
	if showFees || isAdmin {
		shipmentJobStatus = o.GetShipmentJobStatus()
	}
	if showFees {
		commVND = o.GetCommissionVnd()
		payFeeVND = o.GetPaymentFeeVnd()
		payFeeBps = o.GetPaymentFeeBps()
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
		ShopProgramDiscountVND:     o.GetShopProgramDiscountVnd(),
		ShopVoucherDiscountVND:     o.GetShopVoucherDiscountVnd(),
		ShopFreeshipDiscountVND:    o.GetShopFreeshipDiscountVnd(),
		PlatformVoucherDiscountVND: o.GetPlatformVoucherDiscountVnd(),
		FreeshipDiscountVND:        o.GetFreeshipDiscountVnd(),
		CoinsDiscountVND:           o.GetCoinsDiscountVnd(),
		ShippingFeeVND:             o.GetShippingFeeVnd(),
		CommissionVND:              commVND,
		PaymentFeeVND:              payFeeVND,
		PaymentFeeBps:              payFeeBps,
		NeedsManualReview:          isAdmin && o.GetNeedsManualReview(),
		ShipmentJobStatus:          shipmentJobStatus,
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
	case orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY:
		return "ZALOPAY"
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
	case "ZALOPAY", "ORDER_PAYMENT_METHOD_ZALOPAY":
		return orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY
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
		orders = append(orders, mapOrderForBuyer(o))
	}

	statusCode := http.StatusCreated
	if resp.GetIdempotentReplay() {
		statusCode = http.StatusOK
	}

	warnings := resp.GetWarnings()
	if warnings == nil {
		warnings = []string{}
	}

	httpx.WriteJSON(w, statusCode, map[string]any{
		"checkout_group_id":  resp.GetCheckoutGroupId(),
		"orders":             orders,
		"payment_id":         resp.GetPaymentId(),
		"payment_method":     orderPaymentMethodString(resp.GetPaymentMethod()),
		"payment_status":     resp.GetPaymentStatus(),
		"qr_url":             resp.GetQrUrl(),
		"qr_code":            resp.GetQrCode(),
		"pay_url":            resp.GetPayUrl(),
		"payment_expires_at": httpx.FormatTimestamp(resp.GetPaymentExpiresAt()),
		"total_payable_vnd":  resp.GetTotalPayableVnd(),
		"idempotent_replay":  resp.GetIdempotentReplay(),
		"warnings":           warnings,
	})
}

func (h *handler) handleListBuyerOrders(w http.ResponseWriter, r *http.Request) {
	page, pageSize, err := httpx.ParsePagination(r)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
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
		items = append(items, mapOrderForBuyer(o))
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
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"order": mapOrderForCaller(r.Context(), resp.GetOrder())})
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
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"order": mapOrderForCaller(r.Context(), resp.GetOrder())})
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
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"order": mapOrderForBuyer(resp.GetOrder())})
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
		"order":             mapOrderForBuyer(resp.GetOrder()),
		"return_request_id": resp.GetReturnRequestId(),
	})
}

func (h *handler) handleSellerListOrders(w http.ResponseWriter, r *http.Request) {
	page, pageSize, err := httpx.ParsePagination(r)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	q := r.URL.Query()

	var statuses []orderv1.OrderStatus
	for _, raw := range q["statuses"] {
		for _, part := range strings.Split(raw, ",") {
			tok := strings.TrimSpace(part)
			if tok == "" {
				continue
			}
			st := parseOrderStatus(tok)
			if st == orderv1.OrderStatus_ORDER_STATUS_UNSPECIFIED {
				httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid status filter in statuses")
				return
			}
			statuses = append(statuses, st)
		}
	}

	var payMethod orderv1.OrderPaymentMethod
	if rawPM := strings.TrimSpace(q.Get("payment_method")); rawPM != "" {
		payMethod = parseOrderPaymentMethod(rawPM)
		if payMethod == orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_UNSPECIFIED {
			httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid payment_method filter")
			return
		}
	}

	dueBefore, err := httpx.ParseTimestamp(q.Get("due_before"))
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid due_before timestamp")
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Order.ListOrders(ctx, &orderv1.ListOrdersRequest{
		ShopId:              auth.ShopIDFromContext(r.Context()),
		Status:              parseOrderStatus(q.Get("status")),
		Statuses:            statuses,
		CarrierCode:         strings.TrimSpace(q.Get("carrier_code")),
		PaymentMethod:       payMethod,
		Keyword:             strings.TrimSpace(q.Get("keyword")),
		DueBefore:           dueBefore,
		IncludeStatusCounts: httpx.ParseBoolQuery(r, "include_status_counts"),
		RoleScope:           "seller",
		Page:                page,
		PageSize:            pageSize,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	items := make([]*orderDTO, 0, len(resp.GetOrders()))
	for _, o := range resp.GetOrders() {
		items = append(items, mapOrderForSeller(o))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items":         items,
		"page":          page,
		"page_size":     pageSize,
		"total":         resp.GetTotal(),
		"status_counts": mapOrderStatusCounts(resp.GetStatusCounts()),
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
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"order": mapOrderForSeller(ord)})
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
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"order": mapOrderForSeller(resp.GetOrder())})
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
		"order":           mapOrderForSeller(resp.GetOrder()),
		"tracking_code":   resp.GetTrackingCode(),
		"label_url":       resp.GetLabelUrl(),
		"queued":          resp.GetQueued(),
		"shipment_job_id": resp.GetShipmentJobId(),
	})
}

// --- Wave 3b-3 A8: Public Order Lookup, Buyer Address Update, Seller Batch Ship & Picking List ---

type publicOrderItemDTO struct {
	ProductName  string `json:"product_name"`
	SKUName      string `json:"sku_name"`
	Quantity     int32  `json:"quantity"`
	LineTotalVND int64  `json:"line_total_vnd"`
}

type publicOrderTimelineEventDTO struct {
	ToStatus  string `json:"to_status"`
	CreatedAt string `json:"created_at,omitempty"`
}

type publicOrderSummaryDTO struct {
	OrderCode            string                         `json:"order_code"`
	Status               string                         `json:"status"`
	PaymentMethod        string                         `json:"payment_method"`
	PaymentStatus        string                         `json:"payment_status"`
	SubtotalVND          int64                          `json:"subtotal_vnd"`
	ShippingFeeVND       int64                          `json:"shipping_fee_vnd"`
	TotalPayableVND      int64                          `json:"total_payable_vnd"`
	CarrierCode          string                         `json:"carrier_code"`
	TrackingCode         string                         `json:"tracking_code"`
	ProvinceName         string                         `json:"province_name"`
	WardName             string                         `json:"ward_name"`
	RecipientNameMasked  string                         `json:"recipient_name_masked"`
	RecipientPhoneMasked string                         `json:"recipient_phone_masked"`
	Items                []*publicOrderItemDTO          `json:"items"`
	Timeline             []*publicOrderTimelineEventDTO `json:"timeline"`
	CreatedAt            string                         `json:"created_at,omitempty"`
	UpdatedAt            string                         `json:"updated_at,omitempty"`
	DeliveredAt          string                         `json:"delivered_at,omitempty"`
}

func mapPublicOrderSummary(o *orderv1.PublicOrderSummary) *publicOrderSummaryDTO {
	if o == nil {
		return nil
	}
	items := make([]*publicOrderItemDTO, 0, len(o.GetItems()))
	for _, it := range o.GetItems() {
		if it == nil {
			continue
		}
		items = append(items, &publicOrderItemDTO{
			ProductName:  it.GetProductName(),
			SKUName:      it.GetSkuName(),
			Quantity:     it.GetQuantity(),
			LineTotalVND: it.GetLineTotalVnd(),
		})
	}
	timeline := make([]*publicOrderTimelineEventDTO, 0, len(o.GetTimeline()))
	for _, ev := range o.GetTimeline() {
		if ev == nil {
			continue
		}
		timeline = append(timeline, &publicOrderTimelineEventDTO{
			ToStatus:  orderStatusString(ev.GetToStatus()),
			CreatedAt: httpx.FormatTimestamp(ev.GetCreatedAt()),
		})
	}
	return &publicOrderSummaryDTO{
		OrderCode:            o.GetOrderCode(),
		Status:               orderStatusString(o.GetStatus()),
		PaymentMethod:        orderPaymentMethodString(o.GetPaymentMethod()),
		PaymentStatus:        o.GetPaymentStatus(),
		SubtotalVND:          o.GetSubtotalVnd(),
		ShippingFeeVND:       o.GetShippingFeeVnd(),
		TotalPayableVND:      o.GetTotalPayableVnd(),
		CarrierCode:          o.GetCarrierCode(),
		TrackingCode:         o.GetTrackingCode(),
		ProvinceName:         o.GetProvinceName(),
		WardName:             o.GetWardName(),
		RecipientNameMasked:  o.GetRecipientNameMasked(),
		RecipientPhoneMasked: o.GetRecipientPhoneMasked(),
		Items:                items,
		Timeline:             timeline,
		CreatedAt:            httpx.FormatTimestamp(o.GetCreatedAt()),
		UpdatedAt:            httpx.FormatTimestamp(o.GetUpdatedAt()),
		DeliveredAt:          httpx.FormatTimestamp(o.GetDeliveredAt()),
	}
}

func (h *handler) handleLookupOrder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OrderCode string `json:"order_code"`
		Phone     string `json:"phone"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	orderCode := strings.TrimSpace(body.OrderCode)
	phone := strings.TrimSpace(body.Phone)
	if orderCode == "" || phone == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "order_code and phone are required")
		return
	}
	if !middleware.AllowOrderLookupCode(w, r, h.rateLimiter, h.rateLimitOrderLookupCodePer10Min, orderCode, h.log) {
		return
	}

	// Strictly strip any caller Authorization header so public lookup remains guest-only (G7).
	ctx, cancel := h.clients.PublicOutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Order.LookupOrder(ctx, &orderv1.LookupOrderRequest{
		OrderCode: orderCode,
		Phone:     phone,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"order": mapPublicOrderSummary(resp.GetOrder()),
	})
}

func (h *handler) handleUpdateOrderAddress(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AddressID                 string `json:"address_id"`
		PreviewOnly               bool   `json:"preview_only"`
		AcceptFeeIncrease         bool   `json:"accept_fee_increase"`
		ExpectedNewShippingFeeVND int64  `json:"expected_new_shipping_fee_vnd"`
		IdempotencyKey            string `json:"idempotency_key"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}

	addressID := strings.TrimSpace(body.AddressID)
	if addressID == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "address_id is required")
		return
	}

	idemKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idemKey == "" {
		idemKey = strings.TrimSpace(body.IdempotencyKey)
	}

	var extraMD []string
	if idemKey != "" {
		extraMD = []string{"idempotency-key", idemKey}
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context(), extraMD...)
	defer cancel()

	resp, err := h.clients.Order.UpdateOrderAddress(ctx, &orderv1.UpdateOrderAddressRequest{
		OrderId:                   chi.URLParam(r, "id"),
		AddressId:                 addressID,
		PreviewOnly:               body.PreviewOnly,
		AcceptFeeIncrease:         body.AcceptFeeIncrease,
		ExpectedNewShippingFeeVnd: body.ExpectedNewShippingFeeVND,
		IdempotencyKey:            idemKey,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"order":                     mapOrderForBuyer(resp.GetOrder()),
		"old_shipping_fee_vnd":      resp.GetOldShippingFeeVnd(),
		"new_shipping_fee_vnd":      resp.GetNewShippingFeeVnd(),
		"fee_delta_vnd":             resp.GetFeeDeltaVnd(),
		"old_total_payable_vnd":     resp.GetOldTotalPayableVnd(),
		"new_total_payable_vnd":     resp.GetNewTotalPayableVnd(),
		"preview_only":              resp.GetPreviewOnly(),
		"fee_adjusted":              resp.GetFeeAdjusted(),
		"requires_fee_confirmation": resp.GetRequiresFeeConfirmation(),
		"blocked_prepaid_increase":  resp.GetBlockedPrepaidIncrease(),
		"shipment_recreated":        resp.GetShipmentRecreated(),
		"shipment_queued":           resp.GetShipmentQueued(),
		"shipment_job_id":           resp.GetShipmentJobId(),
		"resolved_address":          mapOrderAddressSnapshot(resp.GetResolvedAddress()),
	})
}

type batchShipOrderResultDTO struct {
	OrderID       string `json:"order_id"`
	OrderCode     string `json:"order_code"`
	Outcome       string `json:"outcome"`
	ReasonCode    string `json:"reason_code"`
	Message       string `json:"message"`
	ShipmentJobID string `json:"shipment_job_id"`
	TrackingCode  string `json:"tracking_code"`
}

func batchShipOutcomeString(o orderv1.BatchShipOrderOutcome) string {
	switch o {
	case orderv1.BatchShipOrderOutcome_BATCH_SHIP_ORDER_OUTCOME_QUEUED:
		return "QUEUED"
	case orderv1.BatchShipOrderOutcome_BATCH_SHIP_ORDER_OUTCOME_SKIPPED:
		return "SKIPPED"
	default:
		return ""
	}
}

func (h *handler) handleSellerBatchShipOrders(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OrderIDs    []string `json:"order_ids"`
		CarrierCode string   `json:"carrier_code"`
		ServiceCode string   `json:"service_code"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Order.BatchShipOrders(ctx, &orderv1.BatchShipOrdersRequest{
		OrderIds:    body.OrderIDs,
		CarrierCode: strings.TrimSpace(body.CarrierCode),
		ServiceCode: strings.TrimSpace(body.ServiceCode),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	results := make([]*batchShipOrderResultDTO, 0, len(resp.GetResults()))
	for _, res := range resp.GetResults() {
		if res == nil {
			continue
		}
		results = append(results, &batchShipOrderResultDTO{
			OrderID:       res.GetOrderId(),
			OrderCode:     res.GetOrderCode(),
			Outcome:       batchShipOutcomeString(res.GetOutcome()),
			ReasonCode:    res.GetReasonCode(),
			Message:       res.GetMessage(),
			ShipmentJobID: res.GetShipmentJobId(),
			TrackingCode:  res.GetTrackingCode(),
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"results":       results,
		"queued_count":  resp.GetQueuedCount(),
		"skipped_count": resp.GetSkippedCount(),
	})
}

type pickingListItemDTO struct {
	SKUID         string   `json:"sku_id"`
	ProductID     string   `json:"product_id"`
	ProductName   string   `json:"product_name"`
	SKUName       string   `json:"sku_name"`
	TotalQuantity int32    `json:"total_quantity"`
	OrderCount    int32    `json:"order_count"`
	OrderCodes    []string `json:"order_codes"`
}

func (h *handler) handleSellerPickingList(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OrderIDs []string `json:"order_ids"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Order.GetPickingList(ctx, &orderv1.GetPickingListRequest{
		OrderIds: body.OrderIDs,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	items := make([]*pickingListItemDTO, 0, len(resp.GetItems()))
	for _, it := range resp.GetItems() {
		if it == nil {
			continue
		}
		codes := it.GetOrderCodes()
		if codes == nil {
			codes = []string{}
		}
		items = append(items, &pickingListItemDTO{
			SKUID:         it.GetSkuId(),
			ProductID:     it.GetProductId(),
			ProductName:   it.GetProductName(),
			SKUName:       it.GetSkuName(),
			TotalQuantity: it.GetTotalQuantity(),
			OrderCount:    it.GetOrderCount(),
			OrderCodes:    codes,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items":       items,
		"order_count": resp.GetOrderCount(),
		"total_units": resp.GetTotalUnits(),
	})
}

func (h *handler) handleAdminListOrders(w http.ResponseWriter, r *http.Request) {
	page, pageSize, err := httpx.ParsePagination(r)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
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
		items = append(items, mapOrderForAdmin(o))
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
	page, pageSize, err := httpx.ParsePagination(r)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
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

// --- Wave 2b: Manual-Review Orders, Seller/Admin Reports & CSV Export ---

type orderStatusCountDTO struct {
	Status string `json:"status"`
	Count  int64  `json:"count"`
}

type dailyReportPointDTO struct {
	Date           string `json:"date"`
	OrderCount     int64  `json:"order_count"`
	CompletedCount int64  `json:"completed_count"`
	CancelledCount int64  `json:"cancelled_count"`
	ReturnedCount  int64  `json:"returned_count"`
	GMVVND         int64  `json:"gmv_vnd"`
	NetRevenueVND  int64  `json:"net_revenue_vnd"`
	CommissionVND  int64  `json:"commission_vnd"`
	PaymentFeeVND  int64  `json:"payment_fee_vnd"`
}

type adminDailyReportPointDTO struct {
	dailyReportPointDTO
	PlatformSubsidyVND int64 `json:"platform_subsidy_vnd"`
}

type topProductStatDTO struct {
	ProductID   string `json:"product_id"`
	ProductName string `json:"product_name"`
	UnitsSold   int64  `json:"units_sold"`
	RevenueVND  int64  `json:"revenue_vnd"`
	OrderCount  int64  `json:"order_count"`
}

type sellerReportDTO struct {
	ShopID          string                 `json:"shop_id"`
	FromDate        string                 `json:"from_date"`
	ToDate          string                 `json:"to_date"`
	GMVVND          int64                  `json:"gmv_vnd"`
	NetRevenueVND   int64                  `json:"net_revenue_vnd"`
	CommissionVND   int64                  `json:"commission_vnd"`
	PaymentFeeVND   int64                  `json:"payment_fee_vnd"`
	TotalOrders     int64                  `json:"total_orders"`
	CompletedOrders int64                  `json:"completed_orders"`
	CancelledOrders int64                  `json:"cancelled_orders"`
	ReturnedOrders  int64                  `json:"returned_orders"`
	CancelRateBps   int32                  `json:"cancel_rate_bps"`
	ReturnRateBps   int32                  `json:"return_rate_bps"`
	OrdersByStatus  []*orderStatusCountDTO `json:"orders_by_status"`
	Daily           []*dailyReportPointDTO `json:"daily"`
	TopProducts     []*topProductStatDTO   `json:"top_products"`
}

type platformReportDTO struct {
	FromDate           string                      `json:"from_date"`
	ToDate             string                      `json:"to_date"`
	GMVVND             int64                       `json:"gmv_vnd"`
	NetRevenueVND      int64                       `json:"net_revenue_vnd"`
	CommissionVND      int64                       `json:"commission_vnd"`
	PaymentFeeVND      int64                       `json:"payment_fee_vnd"`
	PlatformSubsidyVND int64                       `json:"platform_subsidy_vnd"`
	TotalOrders        int64                       `json:"total_orders"`
	CompletedOrders    int64                       `json:"completed_orders"`
	CancelledOrders    int64                       `json:"cancelled_orders"`
	ReturnedOrders     int64                       `json:"returned_orders"`
	CancelRateBps      int32                       `json:"cancel_rate_bps"`
	ReturnRateBps      int32                       `json:"return_rate_bps"`
	OrdersByStatus     []*orderStatusCountDTO      `json:"orders_by_status"`
	Daily              []*adminDailyReportPointDTO `json:"daily"`
	TopProducts        []*topProductStatDTO        `json:"top_products"`
	UserStats          *platformUserStatsDTO       `json:"user_stats,omitempty"`
}

func mapOrderStatusCounts(in []*orderv1.OrderStatusCount) []*orderStatusCountDTO {
	out := make([]*orderStatusCountDTO, 0, len(in))
	for _, s := range in {
		out = append(out, &orderStatusCountDTO{
			Status: orderStatusString(s.GetStatus()),
			Count:  s.GetCount(),
		})
	}
	return out
}

func mapDailyReportPoints(in []*orderv1.DailyReportPoint) []*dailyReportPointDTO {
	out := make([]*dailyReportPointDTO, 0, len(in))
	for _, d := range in {
		out = append(out, &dailyReportPointDTO{
			Date:           d.GetDate(),
			OrderCount:     d.GetOrderCount(),
			CompletedCount: d.GetCompletedCount(),
			CancelledCount: d.GetCancelledCount(),
			ReturnedCount:  d.GetReturnedCount(),
			GMVVND:         d.GetGmvVnd(),
			NetRevenueVND:  d.GetNetRevenueVnd(),
			CommissionVND:  d.GetCommissionVnd(),
			PaymentFeeVND:  d.GetPaymentFeeVnd(),
		})
	}
	return out
}

func mapAdminDailyReportPoints(in []*orderv1.DailyReportPoint) []*adminDailyReportPointDTO {
	out := make([]*adminDailyReportPointDTO, 0, len(in))
	for _, d := range in {
		out = append(out, &adminDailyReportPointDTO{
			dailyReportPointDTO: dailyReportPointDTO{
				Date:           d.GetDate(),
				OrderCount:     d.GetOrderCount(),
				CompletedCount: d.GetCompletedCount(),
				CancelledCount: d.GetCancelledCount(),
				ReturnedCount:  d.GetReturnedCount(),
				GMVVND:         d.GetGmvVnd(),
				NetRevenueVND:  d.GetNetRevenueVnd(),
				CommissionVND:  d.GetCommissionVnd(),
				PaymentFeeVND:  d.GetPaymentFeeVnd(),
			},
			PlatformSubsidyVND: d.GetPlatformSubsidyVnd(),
		})
	}
	return out
}

func mapTopProductStats(in []*orderv1.TopProductStat) []*topProductStatDTO {
	out := make([]*topProductStatDTO, 0, len(in))
	for _, p := range in {
		out = append(out, &topProductStatDTO{
			ProductID:   p.GetProductId(),
			ProductName: p.GetProductName(),
			UnitsSold:   p.GetUnitsSold(),
			RevenueVND:  p.GetRevenueVnd(),
			OrderCount:  p.GetOrderCount(),
		})
	}
	return out
}

func (h *handler) handleAdminListOrdersNeedingReview(w http.ResponseWriter, r *http.Request) {
	page, pageSize, err := httpx.ParsePagination(r)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Order.ListOrdersNeedingReview(ctx, &orderv1.ListOrdersNeedingReviewRequest{
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	items := make([]*orderDTO, 0, len(resp.GetOrders()))
	for _, o := range resp.GetOrders() {
		items = append(items, mapOrderForAdmin(o))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items":     items,
		"page":      page,
		"page_size": pageSize,
		"total":     resp.GetTotal(),
	})
}

func (h *handler) handleAdminResolveOrderReview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Note string `json:"note"`
	}
	if err := httpx.DecodeOptionalJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Order.ResolveOrderReview(ctx, &orderv1.ResolveOrderReviewRequest{
		OrderId: chi.URLParam(r, "id"),
		Note:    body.Note,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"order": mapOrderForAdmin(resp.GetOrder()),
	})
}

func (h *handler) handleSellerReports(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Order.GetSellerReport(ctx, &orderv1.GetSellerReportRequest{
		ShopId:   auth.ShopIDFromContext(r.Context()),
		FromDate: strings.TrimSpace(q.Get("from_date")),
		ToDate:   strings.TrimSpace(q.Get("to_date")),
		TopLimit: httpx.ParseInt32Query(r, "top_limit", 0),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"report": &sellerReportDTO{
			ShopID:          resp.GetShopId(),
			FromDate:        resp.GetFromDate(),
			ToDate:          resp.GetToDate(),
			GMVVND:          resp.GetGmvVnd(),
			NetRevenueVND:   resp.GetNetRevenueVnd(),
			CommissionVND:   resp.GetCommissionVnd(),
			PaymentFeeVND:   resp.GetPaymentFeeVnd(),
			TotalOrders:     resp.GetTotalOrders(),
			CompletedOrders: resp.GetCompletedOrders(),
			CancelledOrders: resp.GetCancelledOrders(),
			ReturnedOrders:  resp.GetReturnedOrders(),
			CancelRateBps:   resp.GetCancelRateBps(),
			ReturnRateBps:   resp.GetReturnRateBps(),
			OrdersByStatus:  mapOrderStatusCounts(resp.GetOrdersByStatus()),
			Daily:           mapDailyReportPoints(resp.GetDaily()),
			TopProducts:     mapTopProductStats(resp.GetTopProducts()),
		},
	})
}

func (h *handler) handleAdminReports(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	fromDate := strings.TrimSpace(q.Get("from_date"))
	toDate := strings.TrimSpace(q.Get("to_date"))

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	ordResp, err := h.clients.Order.GetPlatformReport(ctx, &orderv1.GetPlatformReportRequest{
		FromDate: fromDate,
		ToDate:   toDate,
		TopLimit: httpx.ParseInt32Query(r, "top_limit", 0),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	var userStats *platformUserStatsDTO
	if h.clients.User != nil {
		uFrom := ordResp.GetFromDate()
		if uFrom == "" {
			uFrom = fromDate
		}
		uTo := ordResp.GetToDate()
		if uTo == "" {
			uTo = toDate
		}
		uResp, uErr := h.clients.User.GetPlatformUserStats(ctx, &userv1.GetPlatformUserStatsRequest{
			FromDate: uFrom,
			ToDate:   uTo,
		})
		if uErr != nil {
			httpx.WriteGRPCError(w, r, uErr)
			return
		}
		userStats = mapPlatformUserStats(uResp)
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"report": &platformReportDTO{
			FromDate:           ordResp.GetFromDate(),
			ToDate:             ordResp.GetToDate(),
			GMVVND:             ordResp.GetGmvVnd(),
			NetRevenueVND:      ordResp.GetNetRevenueVnd(),
			CommissionVND:      ordResp.GetCommissionVnd(),
			PaymentFeeVND:      ordResp.GetPaymentFeeVnd(),
			PlatformSubsidyVND: ordResp.GetPlatformSubsidyVnd(),
			TotalOrders:        ordResp.GetTotalOrders(),
			CompletedOrders:    ordResp.GetCompletedOrders(),
			CancelledOrders:    ordResp.GetCancelledOrders(),
			ReturnedOrders:     ordResp.GetReturnedOrders(),
			CancelRateBps:      ordResp.GetCancelRateBps(),
			ReturnRateBps:      ordResp.GetReturnRateBps(),
			OrdersByStatus:     mapOrderStatusCounts(ordResp.GetOrdersByStatus()),
			Daily:              mapAdminDailyReportPoints(ordResp.GetDaily()),
			TopProducts:        mapTopProductStats(ordResp.GetTopProducts()),
			UserStats:          userStats,
		},
	})
}

const maxCSVExportBytes = 5 << 20 // 5 MiB cap per CSV report export

var (
	utf8BOM            = []byte{0xEF, 0xBB, 0xBF}
	errCSVSizeExceeded = errors.New("csv export exceeds maximum allowed size")
)

// isPureInteger reports whether v matches ^-?[0-9]+$.
func isPureInteger(v string) bool {
	if v == "" {
		return false
	}
	start := 0
	if v[0] == '-' {
		if len(v) == 1 {
			return false
		}
		start = 1
	}
	for i := start; i < len(v); i++ {
		if v[i] < '0' || v[i] > '9' {
			return false
		}
	}
	return true
}

// sanitizeCSVCell guards against spreadsheet formula injection by prefixing non-integer
// values starting with '=', '+', '-', '@', '\t', '\r', '\n', or leading whitespace followed
// by '=', '+', '-', '@' with a single quote.
// Pure integers (including negative integers like "-500") are preserved verbatim.
func sanitizeCSVCell(v string) string {
	if v == "" || isPureInteger(v) {
		return v
	}
	switch v[0] {
	case '=', '+', '-', '@', '\t', '\r', '\n':
		return "'" + v
	}
	trimmed := strings.TrimLeftFunc(v, unicode.IsSpace)
	if trimmed != "" {
		switch trimmed[0] {
		case '=', '+', '-', '@':
			return "'" + v
		}
	}
	return v
}

func renderCSVWithLimit(header []string, rows [][]string, maxBytes int) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = maxCSVExportBytes
	}
	var buf bytes.Buffer
	buf.Write(utf8BOM)
	cw := csv.NewWriter(&buf)

	writeRow := func(rec []string) error {
		safe := make([]string, len(rec))
		for i, c := range rec {
			safe[i] = sanitizeCSVCell(c)
		}
		if err := cw.Write(safe); err != nil {
			return err
		}
		cw.Flush()
		if err := cw.Error(); err != nil {
			return err
		}
		if buf.Len() > maxBytes {
			return errCSVSizeExceeded
		}
		return nil
	}

	if err := writeRow(header); err != nil {
		return nil, err
	}
	for _, row := range rows {
		if err := writeRow(row); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

func sanitizeFilenamePart(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "all"
	}
	return b.String()
}

func writeReportCSV(w http.ResponseWriter, r *http.Request, filename string, header []string, rows [][]string) {
	payload, err := renderCSVWithLimit(header, rows, maxCSVExportBytes)
	if err != nil {
		if errors.Is(err, errCSVSizeExceeded) {
			httpx.WriteError(w, r, http.StatusRequestEntityTooLarge, "EXPORT_TOO_LARGE", "CSV export exceeds maximum size limit; narrow the date range")
			return
		}
		httpx.WriteError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to generate CSV report")
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}

func i64s(v int64) string { return strconv.FormatInt(v, 10) }

func (h *handler) handleSellerReportsExport(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Order.GetSellerReport(ctx, &orderv1.GetSellerReportRequest{
		ShopId:   auth.ShopIDFromContext(r.Context()),
		FromDate: strings.TrimSpace(q.Get("from_date")),
		ToDate:   strings.TrimSpace(q.Get("to_date")),
		TopLimit: 50,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	header := []string{
		"section", "date", "label", "order_count", "completed_count", "cancelled_count",
		"returned_count", "gmv_vnd", "net_revenue_vnd", "commission_vnd", "payment_fee_vnd",
		"units_sold", "revenue_vnd",
	}
	rows := make([][]string, 0, 1+len(resp.GetOrdersByStatus())+len(resp.GetDaily())+len(resp.GetTopProducts()))
	rows = append(rows, []string{
		"summary", resp.GetFromDate() + ".." + resp.GetToDate(), resp.GetShopId(),
		i64s(resp.GetTotalOrders()), i64s(resp.GetCompletedOrders()), i64s(resp.GetCancelledOrders()),
		i64s(resp.GetReturnedOrders()), i64s(resp.GetGmvVnd()), i64s(resp.GetNetRevenueVnd()),
		i64s(resp.GetCommissionVnd()), i64s(resp.GetPaymentFeeVnd()), "", "",
	})
	for _, st := range resp.GetOrdersByStatus() {
		rows = append(rows, []string{
			"status", "", orderStatusString(st.GetStatus()), i64s(st.GetCount()),
			"", "", "", "", "", "", "", "", "",
		})
	}
	for _, d := range resp.GetDaily() {
		rows = append(rows, []string{
			"daily", d.GetDate(), "",
			i64s(d.GetOrderCount()), i64s(d.GetCompletedCount()), i64s(d.GetCancelledCount()),
			i64s(d.GetReturnedCount()), i64s(d.GetGmvVnd()), i64s(d.GetNetRevenueVnd()),
			i64s(d.GetCommissionVnd()), i64s(d.GetPaymentFeeVnd()), "", "",
		})
	}
	for _, p := range resp.GetTopProducts() {
		rows = append(rows, []string{
			"top_product", "", p.GetProductName(),
			i64s(p.GetOrderCount()), "", "", "", "", "", "", "",
			i64s(p.GetUnitsSold()), i64s(p.GetRevenueVnd()),
		})
	}

	filename := fmt.Sprintf("seller-report-%s-%s.csv",
		sanitizeFilenamePart(resp.GetFromDate()),
		sanitizeFilenamePart(resp.GetToDate()),
	)
	writeReportCSV(w, r, filename, header, rows)
}

func (h *handler) handleAdminReportsExport(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	fromDate := strings.TrimSpace(q.Get("from_date"))
	toDate := strings.TrimSpace(q.Get("to_date"))

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	ordResp, err := h.clients.Order.GetPlatformReport(ctx, &orderv1.GetPlatformReportRequest{
		FromDate: fromDate,
		ToDate:   toDate,
		TopLimit: 50,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	var uResp *userv1.GetPlatformUserStatsResponse
	if h.clients.User != nil {
		uFrom := ordResp.GetFromDate()
		if uFrom == "" {
			uFrom = fromDate
		}
		uTo := ordResp.GetToDate()
		if uTo == "" {
			uTo = toDate
		}
		uResp, err = h.clients.User.GetPlatformUserStats(ctx, &userv1.GetPlatformUserStatsRequest{
			FromDate: uFrom,
			ToDate:   uTo,
		})
		if err != nil {
			httpx.WriteGRPCError(w, r, err)
			return
		}
	}

	userDailyByDate := make(map[string]*userv1.DailyUserShopStat)
	if uResp != nil {
		for _, d := range uResp.GetDaily() {
			userDailyByDate[d.GetDate()] = d
		}
	}
	orderDailyByDate := make(map[string]*orderv1.DailyReportPoint, len(ordResp.GetDaily()))
	dateSet := make(map[string]struct{}, len(ordResp.GetDaily())+len(userDailyByDate))
	for _, d := range ordResp.GetDaily() {
		orderDailyByDate[d.GetDate()] = d
		dateSet[d.GetDate()] = struct{}{}
	}
	for dt := range userDailyByDate {
		dateSet[dt] = struct{}{}
	}
	sortedDates := make([]string, 0, len(dateSet))
	for dt := range dateSet {
		sortedDates = append(sortedDates, dt)
	}
	sort.Strings(sortedDates)

	header := []string{
		"section", "date", "label", "order_count", "completed_count", "cancelled_count",
		"returned_count", "gmv_vnd", "net_revenue_vnd", "commission_vnd", "payment_fee_vnd",
		"platform_subsidy_vnd", "units_sold", "revenue_vnd", "new_users", "new_shops",
	}
	rows := make([][]string, 0, 1+len(ordResp.GetOrdersByStatus())+len(sortedDates)+len(ordResp.GetTopProducts()))
	rows = append(rows, []string{
		"summary", ordResp.GetFromDate() + ".." + ordResp.GetToDate(), "platform",
		i64s(ordResp.GetTotalOrders()), i64s(ordResp.GetCompletedOrders()), i64s(ordResp.GetCancelledOrders()),
		i64s(ordResp.GetReturnedOrders()), i64s(ordResp.GetGmvVnd()), i64s(ordResp.GetNetRevenueVnd()),
		i64s(ordResp.GetCommissionVnd()), i64s(ordResp.GetPaymentFeeVnd()), i64s(ordResp.GetPlatformSubsidyVnd()),
		"", "", i64s(uResp.GetNewUsers()), i64s(uResp.GetNewShops()),
	})
	for _, st := range ordResp.GetOrdersByStatus() {
		rows = append(rows, []string{
			"status", "", orderStatusString(st.GetStatus()), i64s(st.GetCount()),
			"", "", "", "", "", "", "", "", "", "", "", "",
		})
	}
	for _, dt := range sortedDates {
		d := orderDailyByDate[dt]
		u := userDailyByDate[dt]
		rows = append(rows, []string{
			"daily", dt, "",
			i64s(d.GetOrderCount()), i64s(d.GetCompletedCount()), i64s(d.GetCancelledCount()),
			i64s(d.GetReturnedCount()), i64s(d.GetGmvVnd()), i64s(d.GetNetRevenueVnd()),
			i64s(d.GetCommissionVnd()), i64s(d.GetPaymentFeeVnd()), i64s(d.GetPlatformSubsidyVnd()),
			"", "", i64s(u.GetNewUsers()), i64s(u.GetNewShops()),
		})
	}
	for _, p := range ordResp.GetTopProducts() {
		rows = append(rows, []string{
			"top_product", "", p.GetProductName(),
			i64s(p.GetOrderCount()), "", "", "", "", "", "", "", "",
			i64s(p.GetUnitsSold()), i64s(p.GetRevenueVnd()), "", "",
		})
	}

	filename := fmt.Sprintf("admin-report-%s-%s.csv",
		sanitizeFilenamePart(ordResp.GetFromDate()),
		sanitizeFilenamePart(ordResp.GetToDate()),
	)
	writeReportCSV(w, r, filename, header, rows)
}
