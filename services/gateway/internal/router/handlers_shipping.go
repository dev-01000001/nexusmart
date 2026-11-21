package router

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/gateway/internal/httpx"
)

type provinceDTO struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type wardDTO struct {
	Code         string `json:"code"`
	ProvinceCode string `json:"province_code"`
	Name         string `json:"name"`
}

type carrierQuoteDTO struct {
	CarrierCode   string `json:"carrier_code"`
	CarrierName   string `json:"carrier_name"`
	ServiceCode   string `json:"service_code"`
	ServiceName   string `json:"service_name"`
	FeeVND        int64  `json:"fee_vnd"`
	EstimatedDays int32  `json:"estimated_days"`
	IsFallback    bool   `json:"is_fallback"`
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
	Direction       string `json:"direction,omitempty"`
	ReturnRequestID string `json:"return_request_id,omitempty"`
	CreatedAt       string `json:"created_at,omitempty"`
	UpdatedAt       string `json:"updated_at,omitempty"`
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
	default:
		return ""
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
		Direction:       s.GetDirection(),
		ReturnRequestID: s.GetReturnRequestId(),
		CreatedAt:       httpx.FormatTimestamp(s.GetCreatedAt()),
		UpdatedAt:       httpx.FormatTimestamp(s.GetUpdatedAt()),
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
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
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

func (h *handler) handleCalculateShippingFee(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ShopID           string `json:"shop_id"`
		FromProvinceCode string `json:"from_province_code"`
		FromWardCode     string `json:"from_ward_code"`
		ToProvinceCode   string `json:"to_province_code"`
		ToWardCode       string `json:"to_ward_code"`
		WeightGrams      int32  `json:"weight_grams"`
		CODAmountVND     int64  `json:"cod_amount_vnd"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Shipping.CalculateFee(ctx, &shippingv1.CalculateFeeRequest{
		ShopId:           body.ShopID,
		FromProvinceCode: body.FromProvinceCode,
		FromWardCode:     body.FromWardCode,
		ToProvinceCode:   body.ToProvinceCode,
		ToWardCode:       body.ToWardCode,
		WeightGrams:      body.WeightGrams,
		CodAmountVnd:     body.CODAmountVND,
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
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"quotes": quotes})
}

func (h *handler) handleGetShipment(w http.ResponseWriter, r *http.Request) {
	orderID := chi.URLParam(r, "order_id")
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	claims, _ := auth.ClaimsFromContext(r.Context())
	if !auth.HasRole(claims, "admin", "super_admin") && h.clients.Order != nil {
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

	resp, err := h.clients.Shipping.GetShipment(ctx, &shippingv1.GetShipmentRequest{
		OrderId:   orderID,
		Direction: r.URL.Query().Get("direction"),
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
