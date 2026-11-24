package router

import (
	"context"
	"math"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/gateway/internal/httpx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type provinceDTO struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type wardDTO struct {
	Code         string `json:"code"`
	ProvinceCode string `json:"province_code"`
	Name         string `json:"name"`
	CODSupported bool   `json:"cod_supported"`
}

type carrierQuoteDTO struct {
	CarrierCode   string `json:"carrier_code"`
	CarrierName   string `json:"carrier_name"`
	ServiceCode   string `json:"service_code"`
	ServiceName   string `json:"service_name"`
	FeeVND        int64  `json:"fee_vnd"`
	EstimatedDays int32  `json:"estimated_days"`
	IsFallback    bool   `json:"is_fallback"`
	CODSupported  bool   `json:"cod_supported"`
}

type shipmentDTO struct {
	ShipmentID      string `json:"shipment_id"`
	OrderID         string `json:"order_id"`
	ShopID          string `json:"shop_id"`
	CarrierCode     string `json:"carrier_code"`
	ServiceCode     string `json:"service_code"`
	TrackingCode    string `json:"tracking_code"`
	LabelURL        string `json:"label_url"`
	Status          string `json:"status"`
	FeeVND          int64  `json:"fee_vnd"`
	CODAmountVND    int64  `json:"cod_amount_vnd"`
	Direction       string `json:"direction"`
	ReturnRequestID string `json:"return_request_id"`
	RedeliveryCount int32  `json:"redelivery_count"`
	CreatedAt       string `json:"created_at,omitempty"`
	UpdatedAt       string `json:"updated_at,omitempty"`
}

type shopShippingSettingsDTO struct {
	ShopID              string   `json:"shop_id"`
	EnabledCarrierCodes []string `json:"enabled_carrier_codes"`
	DefaultWeightGrams  int32    `json:"default_weight_grams"`
	DefaultLengthCM     int32    `json:"default_length_cm"`
	DefaultWidthCM      int32    `json:"default_width_cm"`
	DefaultHeightCM     int32    `json:"default_height_cm"`
	FreeShipEnabled     bool     `json:"free_ship_enabled"`
	FreeShipMinOrderVND int64    `json:"free_ship_min_order_vnd"`
	FreeShipMaxVND      int64    `json:"free_ship_max_vnd"`
	UpdatedAt           string   `json:"updated_at,omitempty"`
}

type fallbackRateDTO struct {
	Region             string `json:"region"`
	BaseFeeVND         int64  `json:"base_fee_vnd"`
	BaseWeightGrams    int32  `json:"base_weight_grams"`
	ExtraFeeVNDPer500g int64  `json:"extra_fee_vnd_per_500g"`
}

type carrierConfigDTO struct {
	CarrierCode     string             `json:"carrier_code"`
	Name            string             `json:"name"`
	Enabled         bool               `json:"enabled"`
	TokenConfigured bool               `json:"token_configured"`
	FallbackRates   []*fallbackRateDTO `json:"fallback_rates"`
	UpdatedAt       string             `json:"updated_at,omitempty"`
}

type shipmentEventDTO struct {
	ID          string `json:"id"`
	ShipmentID  string `json:"shipment_id"`
	Status      string `json:"status"`
	RawStatus   string `json:"raw_status"`
	Description string `json:"description"`
	OccurredAt  string `json:"occurred_at,omitempty"`
}

func shipmentStatusString(st shippingv1.ShipmentStatus) string {
	switch st {
	case shippingv1.ShipmentStatus_SHIPMENT_STATUS_CREATED:
		return "CREATED"
	case shippingv1.ShipmentStatus_SHIPMENT_STATUS_PICKING:
		return "PICKING"
	case shippingv1.ShipmentStatus_SHIPMENT_STATUS_DELIVERING:
		return "DELIVERING"
	case shippingv1.ShipmentStatus_SHIPMENT_STATUS_DELIVERED:
		return "DELIVERED"
	case shippingv1.ShipmentStatus_SHIPMENT_STATUS_FAILED:
		return "FAILED"
	case shippingv1.ShipmentStatus_SHIPMENT_STATUS_RETURNING:
		return "RETURNING"
	case shippingv1.ShipmentStatus_SHIPMENT_STATUS_RETURNED:
		return "RETURNED"
	case shippingv1.ShipmentStatus_SHIPMENT_STATUS_CANCELLED:
		return "CANCELLED"
	case shippingv1.ShipmentStatus_SHIPMENT_STATUS_QUEUED:
		return "QUEUED"
	default:
		return ""
	}
}

func shipmentDirectionString(d shippingv1.ShipmentDirection) string {
	switch d {
	case shippingv1.ShipmentDirection_SHIPMENT_DIRECTION_FORWARD:
		return "FORWARD"
	case shippingv1.ShipmentDirection_SHIPMENT_DIRECTION_RETURN:
		return "RETURN"
	default:
		return ""
	}
}

func parseShipmentDirection(s string) shippingv1.ShipmentDirection {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "FORWARD", "SHIPMENT_DIRECTION_FORWARD":
		return shippingv1.ShipmentDirection_SHIPMENT_DIRECTION_FORWARD
	case "RETURN", "SHIPMENT_DIRECTION_RETURN":
		return shippingv1.ShipmentDirection_SHIPMENT_DIRECTION_RETURN
	default:
		return shippingv1.ShipmentDirection_SHIPMENT_DIRECTION_UNSPECIFIED
	}
}

func shippingRegionString(r shippingv1.ShippingRegion) string {
	switch r {
	case shippingv1.ShippingRegion_SHIPPING_REGION_SAME_PROVINCE:
		return "SAME_PROVINCE"
	case shippingv1.ShippingRegion_SHIPPING_REGION_SAME_REGION:
		return "SAME_REGION"
	case shippingv1.ShippingRegion_SHIPPING_REGION_CROSS_REGION:
		return "CROSS_REGION"
	default:
		return ""
	}
}

func parseShippingRegion(s string) shippingv1.ShippingRegion {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "SAME_PROVINCE", "SHIPPING_REGION_SAME_PROVINCE":
		return shippingv1.ShippingRegion_SHIPPING_REGION_SAME_PROVINCE
	case "SAME_REGION", "SHIPPING_REGION_SAME_REGION":
		return shippingv1.ShippingRegion_SHIPPING_REGION_SAME_REGION
	case "CROSS_REGION", "SHIPPING_REGION_CROSS_REGION":
		return shippingv1.ShippingRegion_SHIPPING_REGION_CROSS_REGION
	default:
		return shippingv1.ShippingRegion_SHIPPING_REGION_UNSPECIFIED
	}
}

func mapShipment(s *shippingv1.Shipment) *shipmentDTO {
	if s == nil {
		return nil
	}
	return &shipmentDTO{
		ShipmentID:      s.GetShipmentId(),
		OrderID:         s.GetOrderId(),
		ShopID:          s.GetShopId(),
		CarrierCode:     s.GetCarrierCode(),
		ServiceCode:     s.GetServiceCode(),
		TrackingCode:    s.GetTrackingCode(),
		LabelURL:        s.GetLabelUrl(),
		Status:          shipmentStatusString(s.GetStatus()),
		FeeVND:          s.GetFeeVnd(),
		CODAmountVND:    s.GetCodAmountVnd(),
		Direction:       shipmentDirectionString(s.GetDirection()),
		ReturnRequestID: s.GetReturnRequestId(),
		RedeliveryCount: s.GetRedeliveryCount(),
		CreatedAt:       httpx.FormatTimestamp(s.GetCreatedAt()),
		UpdatedAt:       httpx.FormatTimestamp(s.GetUpdatedAt()),
	}
}

func mapShopShippingSettings(s *shippingv1.ShopShippingSettings) *shopShippingSettingsDTO {
	if s == nil {
		return nil
	}
	codes := s.GetEnabledCarrierCodes()
	if codes == nil {
		codes = []string{}
	}
	return &shopShippingSettingsDTO{
		ShopID:              s.GetShopId(),
		EnabledCarrierCodes: codes,
		DefaultWeightGrams:  s.GetDefaultWeightGrams(),
		DefaultLengthCM:     s.GetDefaultLengthCm(),
		DefaultWidthCM:      s.GetDefaultWidthCm(),
		DefaultHeightCM:     s.GetDefaultHeightCm(),
		FreeShipEnabled:     s.GetFreeShipEnabled(),
		FreeShipMinOrderVND: s.GetFreeShipMinOrderVnd(),
		FreeShipMaxVND:      s.GetFreeShipMaxVnd(),
		UpdatedAt:           httpx.FormatTimestamp(s.GetUpdatedAt()),
	}
}

func mapCarrierConfig(c *shippingv1.CarrierConfig) *carrierConfigDTO {
	if c == nil {
		return nil
	}
	rates := make([]*fallbackRateDTO, 0, len(c.GetFallbackRates()))
	for _, fr := range c.GetFallbackRates() {
		rates = append(rates, &fallbackRateDTO{
			Region:             shippingRegionString(fr.GetRegion()),
			BaseFeeVND:         fr.GetBaseFeeVnd(),
			BaseWeightGrams:    fr.GetBaseWeightGrams(),
			ExtraFeeVNDPer500g: fr.GetExtraFeeVndPer_500G(),
		})
	}
	return &carrierConfigDTO{
		CarrierCode:     c.GetCarrierCode(),
		Name:            c.GetName(),
		Enabled:         c.GetEnabled(),
		TokenConfigured: c.GetTokenConfigured(),
		FallbackRates:   rates,
		UpdatedAt:       httpx.FormatTimestamp(c.GetUpdatedAt()),
	}
}

func (h *handler) handleListProvinces(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Shipping.ListProvinces(ctx, &shippingv1.ListProvincesRequest{
		Query: r.URL.Query().Get("query"),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	provinces := make([]*provinceDTO, 0, len(resp.GetProvinces()))
	for _, p := range resp.GetProvinces() {
		provinces = append(provinces, &provinceDTO{
			Code: p.GetCode(),
			Name: p.GetName(),
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"provinces": provinces})
}

func (h *handler) handleListWards(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Shipping.ListWards(ctx, &shippingv1.ListWardsRequest{
		ProvinceCode: chi.URLParam(r, "code"),
		Query:        r.URL.Query().Get("query"),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	wards := make([]*wardDTO, 0, len(resp.GetWards()))
	for _, wd := range resp.GetWards() {
		wards = append(wards, &wardDTO{
			Code:         wd.GetCode(),
			ProvinceCode: wd.GetProvinceCode(),
			Name:         wd.GetName(),
			CODSupported: wd.GetCodSupported(),
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"wards": wards})
}

func (h *handler) handleMapLegacyAddress(w http.ResponseWriter, r *http.Request) {
	var body struct {
		LegacyProvince string `json:"legacy_province"`
		LegacyDistrict string `json:"legacy_district"`
		LegacyWard     string `json:"legacy_ward"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Shipping.MapLegacyAddress(ctx, &shippingv1.MapLegacyAddressRequest{
		LegacyProvince: body.LegacyProvince,
		LegacyDistrict: body.LegacyDistrict,
		LegacyWard:     body.LegacyWard,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"found":                resp.GetFound(),
		"province_code":        resp.GetProvinceCode(),
		"province_name":        resp.GetProvinceName(),
		"ward_code":            resp.GetWardCode(),
		"ward_name":            resp.GetWardName(),
		"legacy_province_code": resp.GetLegacyProvinceCode(),
		"legacy_district_code": resp.GetLegacyDistrictCode(),
		"legacy_ward_code":     resp.GetLegacyWardCode(),
	})
}

const (
	defaultShopWeightGrams int64 = 500
	maxCalculateFeeItems         = 100
	maxCalculateFeeItemQty int32 = 999
)

type calculateFeeItemInput struct {
	SKUID    string `json:"sku_id"`
	Quantity int32  `json:"quantity"`
}

// fetchShopShippingSettings loads a shop's shipping settings using the caller's forwarded JWT
// (gateway does not hold SERVICE_JWT_SECRET).
func (h *handler) fetchShopShippingSettings(ctx context.Context, shopID string) (*shippingv1.ShopShippingSettings, error) {
	resp, err := h.clients.Shipping.GetShopShippingSettings(ctx, &shippingv1.GetShopShippingSettingsRequest{
		ShopId: shopID,
	})
	if err != nil {
		return nil, err
	}
	return resp.GetSettings(), nil
}

func (h *handler) handleCalculateShippingFee(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ShopID           string                  `json:"shop_id"`
		FromProvinceCode string                  `json:"from_province_code"`
		FromWardCode     string                  `json:"from_ward_code"`
		ToProvinceCode   string                  `json:"to_province_code"`
		ToWardCode       string                  `json:"to_ward_code"`
		WeightGrams      int32                   `json:"weight_grams"`
		CODAmountVND     int64                   `json:"cod_amount_vnd"`
		Items            []calculateFeeItemInput `json:"items"`
		PaymentMethod    string                  `json:"payment_method"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}

	shopID := strings.TrimSpace(body.ShopID)
	weightGrams := body.WeightGrams
	codAmountVND := body.CODAmountVND
	var fallbackWeightItemQty int32

	_, authenticated := auth.ClaimsFromContext(r.Context())

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	if len(body.Items) > 0 {
		if shopID == "" {
			httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "shop_id is required when items are provided")
			return
		}
		if len(body.Items) > maxCalculateFeeItems {
			httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "items exceed maximum limit of 100")
			return
		}
		isCOD := false
		if rawPM := strings.TrimSpace(body.PaymentMethod); rawPM != "" {
			pm := parseOrderPaymentMethod(rawPM)
			if pm == orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_UNSPECIFIED {
				httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "payment_method must be COD, VIETQR, or ZALOPAY")
				return
			}
			isCOD = pm == orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD
		}
		qtyBySKU := make(map[string]int64, len(body.Items))
		for _, it := range body.Items {
			skuID := strings.TrimSpace(it.SKUID)
			if skuID == "" {
				httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "sku_id is required for every item")
				return
			}
			if it.Quantity < 1 || it.Quantity > maxCalculateFeeItemQty {
				httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "quantity must be between 1 and 999")
				return
			}
			qtyBySKU[skuID] += int64(it.Quantity)
		}
		if h.clients.Product == nil || h.clients.Shipping == nil {
			httpx.WriteError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "required upstream service unavailable")
			return
		}

		skuIDs := make([]string, 0, len(qtyBySKU))
		for id := range qtyBySKU {
			skuIDs = append(skuIDs, id)
		}
		sort.Strings(skuIDs)

		skuResp, err := h.clients.Product.GetSKUsBatch(ctx, &productv1.GetSKUsBatchRequest{SkuIds: skuIDs})
		if err != nil {
			httpx.WriteGRPCError(w, r, err)
			return
		}
		skuByID := make(map[string]*productv1.SKU, len(skuResp.GetSkus()))
		for _, sk := range skuResp.GetSkus() {
			if sk != nil {
				skuByID[sk.GetId()] = sk
			}
		}
		for _, skuID := range skuIDs {
			sk, ok := skuByID[skuID]
			// Uniform 400 error so cross-shop SKUs and unknown SKUs are indistinguishable.
			if !ok || strings.TrimSpace(sk.GetShopId()) != shopID {
				httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "one or more SKUs are invalid for this shop")
				return
			}
			if !sk.GetIsActive() {
				httpx.WriteError(w, r, http.StatusConflict, "SKU_INACTIVE", "sku "+skuID+" is not active")
				return
			}
			if sk.GetPriceVnd() < 0 {
				httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "one or more SKUs have an invalid price")
				return
			}
		}

		defaultWeight := defaultShopWeightGrams
		if authenticated {
			st, err := h.fetchShopShippingSettings(ctx, shopID)
			if err != nil {
				if status.Code(err) != codes.NotFound {
					httpx.WriteGRPCError(w, r, err)
					return
				}
			} else if st != nil && st.GetDefaultWeightGrams() > 0 {
				defaultWeight = int64(st.GetDefaultWeightGrams())
			}
		}

		var (
			totalWeightGrams int64
			fallbackQty      int64
			subtotalVND      int64
		)
		for _, skuID := range skuIDs {
			sk := skuByID[skuID]
			qty := qtyBySKU[skuID]

			unitWeight := int64(sk.GetWeightGrams())
			if unitWeight <= 0 {
				if !authenticated {
					// Anonymous callers never query GetShopShippingSettings (E17): defer default_weight_grams
					// multiplication to ShippingService via fallback_weight_item_qty.
					if fallbackQty > math.MaxInt32-qty {
						httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "total item quantity exceeds maximum supported value")
						return
					}
					fallbackQty += qty
				} else {
					unitWeight = defaultWeight
				}
			}
			if unitWeight > 0 {
				if qty > math.MaxInt64/unitWeight {
					httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "total weight exceeds maximum supported value")
					return
				}
				lineWeight := unitWeight * qty
				if totalWeightGrams > math.MaxInt64-lineWeight {
					httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "total weight exceeds maximum supported value")
					return
				}
				totalWeightGrams += lineWeight
			}

			unitPrice := sk.GetPriceVnd()
			if unitPrice > 0 && qty > math.MaxInt64/unitPrice {
				httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "order subtotal overflow")
				return
			}
			lineSubtotal := unitPrice * qty
			if subtotalVND > math.MaxInt64-lineSubtotal {
				httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "order subtotal overflow")
				return
			}
			subtotalVND += lineSubtotal
		}
		if authenticated && totalWeightGrams <= 0 {
			totalWeightGrams = defaultWeight
		}
		if totalWeightGrams > math.MaxInt32 {
			httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "total weight exceeds maximum supported value")
			return
		}
		weightGrams = int32(totalWeightGrams)
		fallbackWeightItemQty = int32(fallbackQty)
		if isCOD {
			codAmountVND = subtotalVND
		} else {
			codAmountVND = 0
		}
	}

	resp, err := h.clients.Shipping.CalculateFee(ctx, &shippingv1.CalculateFeeRequest{
		ShopId:                shopID,
		FromProvinceCode:      body.FromProvinceCode,
		FromWardCode:          body.FromWardCode,
		ToProvinceCode:        body.ToProvinceCode,
		ToWardCode:            body.ToWardCode,
		WeightGrams:           weightGrams,
		CodAmountVnd:          codAmountVND,
		FallbackWeightItemQty: fallbackWeightItemQty,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	quotes := make([]*carrierQuoteDTO, 0, len(resp.GetQuotes()))
	for _, q := range resp.GetQuotes() {
		quotes = append(quotes, &carrierQuoteDTO{
			CarrierCode:   q.GetCarrierCode(),
			CarrierName:   q.GetCarrierName(),
			ServiceCode:   q.GetServiceCode(),
			ServiceName:   q.GetServiceName(),
			FeeVND:        q.GetFeeVnd(),
			EstimatedDays: q.GetEstimatedDays(),
			IsFallback:    q.GetIsFallback(),
			CODSupported:  q.GetCodSupported(),
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"quotes":       quotes,
		"is_estimated": resp.GetIsEstimated(),
	})
}

func (h *handler) handleGetShipment(w http.ResponseWriter, r *http.Request) {
	orderID := chi.URLParam(r, "order_id")
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	claims, _ := auth.ClaimsFromContext(r.Context())
	if !auth.HasCapability(claims, auth.CapCarriersRead) && h.clients.Order != nil {
		if _, err := h.clients.Order.GetOrder(ctx, &orderv1.GetOrderRequest{
			OrderId:     orderID,
			ActorId:     claims.UserID(),
			ActorRole:   claims.Role,
			ActorShopId: claims.ShopID,
		}); err != nil {
			httpx.WriteGRPCError(w, r, err)
			return
		}
	}

	dirParam := strings.TrimSpace(r.URL.Query().Get("direction"))
	dir := parseShipmentDirection(dirParam)
	if dirParam != "" && dir == shippingv1.ShipmentDirection_SHIPMENT_DIRECTION_UNSPECIFIED {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "direction must be FORWARD or RETURN")
		return
	}

	resp, err := h.clients.Shipping.GetShipment(ctx, &shippingv1.GetShipmentRequest{
		OrderId:   orderID,
		Direction: dir,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	events := make([]*shipmentEventDTO, 0, len(resp.GetEvents()))
	for _, ev := range resp.GetEvents() {
		events = append(events, &shipmentEventDTO{
			ID:          ev.GetId(),
			ShipmentID:  ev.GetShipmentId(),
			Status:      shipmentStatusString(ev.GetStatus()),
			RawStatus:   ev.GetRawStatus(),
			Description: ev.GetDescription(),
			OccurredAt:  httpx.FormatTimestamp(ev.GetOccurredAt()),
		})
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"shipment": mapShipment(resp.GetShipment()),
		"events":   events,
	})
}

func (h *handler) handleRequestRedelivery(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PreferredDate string `json:"preferred_date"`
		Note          string `json:"note"`
	}
	if err := httpx.DecodeOptionalJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Shipping.RequestRedelivery(ctx, &shippingv1.RequestRedeliveryRequest{
		OrderId:       chi.URLParam(r, "id"),
		PreferredDate: body.PreferredDate,
		Note:          body.Note,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"shipment": mapShipment(resp.GetShipment()),
	})
}

func (h *handler) handleSellerGetShippingSettings(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Shipping.GetShopShippingSettings(ctx, &shippingv1.GetShopShippingSettingsRequest{
		ShopId: auth.ShopIDFromContext(r.Context()),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"settings": mapShopShippingSettings(resp.GetSettings()),
	})
}

func (h *handler) handleSellerUpdateShippingSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		EnabledCarrierCodes []string `json:"enabled_carrier_codes"`
		DefaultWeightGrams  int32    `json:"default_weight_grams"`
		DefaultLengthCM     int32    `json:"default_length_cm"`
		DefaultWidthCM      int32    `json:"default_width_cm"`
		DefaultHeightCM     int32    `json:"default_height_cm"`
		FreeShipEnabled     bool     `json:"free_ship_enabled"`
		FreeShipMinOrderVND *int64   `json:"free_ship_min_order_vnd"`
		FreeShipMaxVND      *int64   `json:"free_ship_max_vnd"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	var freeShipMin, freeShipMax int64
	if body.FreeShipMinOrderVND != nil {
		if *body.FreeShipMinOrderVND < 0 {
			httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "free_ship_min_order_vnd must be non-negative")
			return
		}
		freeShipMin = *body.FreeShipMinOrderVND
	}
	if body.FreeShipMaxVND != nil {
		if *body.FreeShipMaxVND < 0 {
			httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "free_ship_max_vnd must be non-negative")
			return
		}
		freeShipMax = *body.FreeShipMaxVND
	}
	if body.FreeShipEnabled {
		if body.FreeShipMinOrderVND == nil {
			httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "free_ship_min_order_vnd is required when free_ship_enabled is true")
			return
		}
		if body.FreeShipMaxVND == nil || freeShipMax <= 0 {
			httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "free_ship_max_vnd is required and must be > 0 when free_ship_enabled is true")
			return
		}
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	// Always enforce shop_id from verified seller claims.
	resp, err := h.clients.Shipping.UpdateShopShippingSettings(ctx, &shippingv1.UpdateShopShippingSettingsRequest{
		ShopId:              auth.ShopIDFromContext(r.Context()),
		EnabledCarrierCodes: body.EnabledCarrierCodes,
		DefaultWeightGrams:  body.DefaultWeightGrams,
		DefaultLengthCm:     body.DefaultLengthCM,
		DefaultWidthCm:      body.DefaultWidthCM,
		DefaultHeightCm:     body.DefaultHeightCM,
		FreeShipEnabled:     body.FreeShipEnabled,
		FreeShipMinOrderVnd: freeShipMin,
		FreeShipMaxVnd:      freeShipMax,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"settings": mapShopShippingSettings(resp.GetSettings()),
	})
}

func (h *handler) handleAdminListCarriers(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Shipping.AdminListCarriers(ctx, &shippingv1.AdminListCarriersRequest{})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	carriers := make([]*carrierConfigDTO, 0, len(resp.GetCarriers()))
	for _, c := range resp.GetCarriers() {
		carriers = append(carriers, mapCarrierConfig(c))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"carriers": carriers,
	})
}

func (h *handler) handleAdminUpdateCarrier(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled       *bool             `json:"enabled"`
		APIToken      string            `json:"api_token"`
		FallbackRates []fallbackRateDTO `json:"fallback_rates"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	if body.Enabled == nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "enabled is required")
		return
	}
	rates := make([]*shippingv1.FallbackRate, 0, len(body.FallbackRates))
	for _, fr := range body.FallbackRates {
		rates = append(rates, &shippingv1.FallbackRate{
			Region:              parseShippingRegion(fr.Region),
			BaseFeeVnd:          fr.BaseFeeVND,
			BaseWeightGrams:     fr.BaseWeightGrams,
			ExtraFeeVndPer_500G: fr.ExtraFeeVNDPer500g,
		})
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Shipping.AdminUpdateCarrier(ctx, &shippingv1.AdminUpdateCarrierRequest{
		CarrierCode:   chi.URLParam(r, "code"),
		Enabled:       *body.Enabled,
		ApiToken:      body.APIToken,
		FallbackRates: rates,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"carrier": mapCarrierConfig(resp.GetCarrier()),
	})
}

func (h *handler) handleAdminTestCarrier(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Shipping.AdminTestCarrier(ctx, &shippingv1.AdminTestCarrierRequest{
		CarrierCode: chi.URLParam(r, "code"),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"ok":      resp.GetOk(),
		"message": resp.GetMessage(),
	})
}
