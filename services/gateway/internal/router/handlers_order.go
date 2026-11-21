package router

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
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
	SalesChannel               string                   `json:"sales_channel"`
	StorefrontShopID           string                   `json:"storefront_shop_id"`
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
		SalesChannel:               normalizeSalesChannelOutput(o.GetSalesChannel()),
		StorefrontShopID:           o.GetStorefrontShopId(),
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
		SalesChannel     string `json:"sales_channel"`
		StorefrontShopID string `json:"storefront_shop_id"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
		return
	}
	salesChannel, storefrontShopID, errMsg := validateSalesChannelInput(body.SalesChannel, body.StorefrontShopID)
	if errMsg != "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", errMsg)
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
		SalesChannel:      salesChannel,
		StorefrontShopId:  storefrontShopID,
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
		"sales_channel":      normalizeSalesChannelOutput(resp.GetSalesChannel()),
		"storefront_shop_id": resp.GetStorefrontShopId(),
	})
}

func (h *handler) handleListBuyerOrders(w http.ResponseWriter, r *http.Request) {
	page, pageSize := httpx.ParsePagination(r)
	q := r.URL.Query()
	salesChannel, errMsg := validateSalesChannelFilter(q.Get("sales_channel"))
	if errMsg != "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", errMsg)
		return
	}
	// storefront_shop_id narrows the caller's OWN orders to one shop site; buyer_id always comes
	// from the JWT, so this filter can never widen access.
	storefrontShopID := strings.TrimSpace(q.Get("storefront_shop_id"))
	if storefrontShopID != "" && uuid.Validate(storefrontShopID) != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "storefront_shop_id must be a UUID")
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Order.ListOrders(ctx, &orderv1.ListOrdersRequest{
		BuyerId:          auth.UserIDFromContext(r.Context()),
		Status:           parseOrderStatus(q.Get("status")),
		RoleScope:        "buyer",
		Page:             page,
		PageSize:         pageSize,
		SalesChannel:     salesChannel,
		StorefrontShopId: storefrontShopID,
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
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
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
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
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
	shopID, ok := requireSellerShopID(w, r)
	if !ok {
		return
	}
	page, pageSize := httpx.ParsePagination(r)
	q := r.URL.Query()
	salesChannel, errMsg := validateSalesChannelFilter(q.Get("sales_channel"))
	if errMsg != "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", errMsg)
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Order.ListOrders(ctx, &orderv1.ListOrdersRequest{
		ShopId:       shopID,
		Status:       parseOrderStatus(q.Get("status")),
		RoleScope:    "seller",
		Page:         page,
		PageSize:     pageSize,
		SalesChannel: salesChannel,
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

// handleSellerGetOrder fetches a single order belonging to the caller's shop (derived from JWT).
func (h *handler) handleSellerGetOrder(w http.ResponseWriter, r *http.Request) {
	shopID, ok := requireSellerShopID(w, r)
	if !ok {
		return
	}
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
	orderShopID := strings.TrimSpace(resp.GetOrder().GetShopId())
	if resp.GetOrder() == nil || orderShopID == "" || orderShopID != shopID {
		httpx.WriteError(w, r, http.StatusForbidden, "PERMISSION_DENIED", "order does not belong to seller shop")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"order": mapOrder(resp.GetOrder())})
}

func (h *handler) handleSellerConfirmOrder(w http.ResponseWriter, r *http.Request) {
	shopID, ok := requireSellerShopID(w, r)
	if !ok {
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Order.SellerConfirmOrder(ctx, &orderv1.SellerConfirmOrderRequest{
		OrderId:      chi.URLParam(r, "id"),
		ShopId:       shopID,
		SellerUserId: auth.UserIDFromContext(r.Context()),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"order": mapOrder(resp.GetOrder())})
}

func (h *handler) handleSellerShipOrder(w http.ResponseWriter, r *http.Request) {
	shopID, ok := requireSellerShopID(w, r)
	if !ok {
		return
	}
	var body struct {
		CarrierCode string `json:"carrier_code"`
		ServiceCode string `json:"service_code"`
	}
	if err := httpx.DecodeOptionalJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Order.SellerShipOrder(ctx, &orderv1.SellerShipOrderRequest{
		OrderId:      chi.URLParam(r, "id"),
		ShopId:       shopID,
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
	salesChannel, errMsg := validateSalesChannelFilter(q.Get("sales_channel"))
	if errMsg != "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", errMsg)
		return
	}
	storefrontShopID := strings.TrimSpace(q.Get("storefront_shop_id"))
	if storefrontShopID != "" && uuid.Validate(storefrontShopID) != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "storefront_shop_id must be a UUID")
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Order.ListOrders(ctx, &orderv1.ListOrdersRequest{
		BuyerId:          q.Get("buyer_id"),
		ShopId:           q.Get("shop_id"),
		Status:           parseOrderStatus(q.Get("status")),
		RoleScope:        "admin",
		Page:             page,
		PageSize:         pageSize,
		SalesChannel:     salesChannel,
		StorefrontShopId: storefrontShopID,
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
