package grpcserver

import (
	"context"
	"errors"
	"strings"

	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/shipping/internal/carrier"
	"github.com/nexusmart/core/services/shipping/internal/service"
	"github.com/nexusmart/core/services/shipping/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Server implements shippingv1.ShippingServiceServer.
type Server struct {
	shippingv1.UnimplementedShippingServiceServer
	svc *service.ShippingService
}

// New constructs a gRPC ShippingService server.
func New(svc *service.ShippingService) *Server {
	return &Server{svc: svc}
}

func (s *Server) ListProvinces(ctx context.Context, req *shippingv1.ListProvincesRequest) (*shippingv1.ListProvincesResponse, error) {
	provinces, err := s.svc.ListProvinces(ctx, req.GetQuery())
	if err != nil {
		return nil, mapError(err)
	}
	resp := &shippingv1.ListProvincesResponse{
		Provinces: make([]*shippingv1.Province, 0, len(provinces)),
	}
	for _, p := range provinces {
		resp.Provinces = append(resp.Provinces, &shippingv1.Province{
			Code: p.Code,
			Name: p.Name,
		})
	}
	return resp, nil
}

func (s *Server) ListWards(ctx context.Context, req *shippingv1.ListWardsRequest) (*shippingv1.ListWardsResponse, error) {
	wards, err := s.svc.ListWards(ctx, req.GetProvinceCode(), req.GetQuery())
	if err != nil {
		return nil, mapError(err)
	}
	resp := &shippingv1.ListWardsResponse{
		Wards: make([]*shippingv1.Ward, 0, len(wards)),
	}
	for _, w := range wards {
		resp.Wards = append(resp.Wards, &shippingv1.Ward{
			Code:         w.Code,
			ProvinceCode: w.ProvinceCode,
			Name:         w.Name,
			CodSupported: w.CODSupported,
		})
	}
	return resp, nil
}

func (s *Server) MapLegacyAddress(ctx context.Context, req *shippingv1.MapLegacyAddressRequest) (*shippingv1.MapLegacyAddressResponse, error) {
	match, err := s.svc.MapLegacyAddress(ctx, req.GetLegacyProvince(), req.GetLegacyDistrict(), req.GetLegacyWard())
	if err != nil {
		return nil, mapError(err)
	}
	return &shippingv1.MapLegacyAddressResponse{
		Found:              match.Found,
		ProvinceCode:       match.ProvinceCode,
		ProvinceName:       match.ProvinceName,
		WardCode:           match.WardCode,
		WardName:           match.WardName,
		LegacyProvinceCode: match.LegacyProvinceCode,
		LegacyDistrictCode: match.LegacyDistrictCode,
		LegacyWardCode:     match.LegacyWardCode,
	}, nil
}

func (s *Server) CalculateFee(ctx context.Context, req *shippingv1.CalculateFeeRequest) (*shippingv1.CalculateFeeResponse, error) {
	quotes, isEstimated, err := s.svc.CalculateFeeWithEstimate(ctx, carrier.FeeRequest{
		ShopID:                req.GetShopId(),
		FromProvinceCode:      req.GetFromProvinceCode(),
		FromWardCode:          req.GetFromWardCode(),
		ToProvinceCode:        req.GetToProvinceCode(),
		ToWardCode:            req.GetToWardCode(),
		WeightGrams:           req.GetWeightGrams(),
		CODAmountVND:          req.GetCodAmountVnd(),
		FallbackWeightItemQty: req.GetFallbackWeightItemQty(),
	})
	if err != nil {
		return nil, mapError(err)
	}

	resp := &shippingv1.CalculateFeeResponse{
		Quotes:      make([]*shippingv1.CarrierQuote, 0, len(quotes)),
		IsEstimated: isEstimated,
	}
	for _, q := range quotes {
		resp.Quotes = append(resp.Quotes, &shippingv1.CarrierQuote{
			CarrierCode:   q.CarrierCode,
			CarrierName:   q.CarrierName,
			ServiceCode:   q.ServiceCode,
			ServiceName:   q.ServiceName,
			FeeVnd:        q.FeeVND,
			EstimatedDays: q.EstimatedDays,
			IsFallback:    q.IsFallback,
			CodSupported:  q.CODSupported,
		})
	}
	return resp, nil
}

func (s *Server) CreateShipment(ctx context.Context, req *shippingv1.CreateShipmentRequest) (*shippingv1.CreateShipmentResponse, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}
	if claims.Service != "" {
		return nil, status.Error(codes.PermissionDenied, "service identity not allowed for CreateShipment")
	}
	isSeller := auth.HasRole(claims, auth.RoleSeller)
	if !isSeller && !auth.HasCapability(claims, auth.CapOrdersReviewResolve) {
		return nil, status.Error(codes.PermissionDenied, "only seller or authorized operations staff may create shipments")
	}
	if isSeller {
		if claims.ShopID == "" || req.GetShopId() == "" || claims.ShopID != req.GetShopId() {
			return nil, status.Error(codes.PermissionDenied, "seller cannot create shipment for another shop")
		}
	}

	in := service.CreateShipmentInput{
		OrderID:      req.GetOrderId(),
		ShopID:       req.GetShopId(),
		BuyerID:      req.GetBuyerId(),
		CarrierCode:  req.GetCarrierCode(),
		ServiceCode:  req.GetServiceCode(),
		CODAmountVND: req.GetCodAmountVnd(),
		WeightGrams:  req.GetWeightGrams(),
		ItemsSummary: req.GetItemsSummary(),
		FromAddress:  fromProtoAddress(req.GetFromAddress()),
		ToAddress:    fromProtoAddress(req.GetToAddress()),
	}

	out, err := s.svc.CreateShipmentWithQueue(ctx, in)
	if err != nil {
		return nil, mapError(err)
	}
	return &shippingv1.CreateShipmentResponse{
		Shipment:       toProtoShipment(out.Shipment),
		AlreadyExisted: out.AlreadyExisted,
		Queued:         out.Queued,
		JobId:          out.JobID,
	}, nil
}

func (s *Server) CreateReturnShipment(ctx context.Context, req *shippingv1.CreateReturnShipmentRequest) (*shippingv1.CreateReturnShipmentResponse, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}
	if !isServiceOrHasCapability(claims, auth.CapOrdersReviewResolve) {
		return nil, status.Error(codes.PermissionDenied, "only internal service or authorized operations staff may create return shipments")
	}

	in := service.CreateReturnShipmentInput{
		OrderID:         req.GetOrderId(),
		ReturnRequestID: req.GetReturnRequestId(),
		ShopID:          req.GetShopId(),
		BuyerID:         req.GetBuyerId(),
		CarrierCode:     req.GetCarrierCode(),
		ServiceCode:     req.GetServiceCode(),
		WeightGrams:     req.GetWeightGrams(),
		ItemsSummary:    req.GetItemsSummary(),
		FromAddress:     fromProtoAddress(req.GetFromAddress()),
		ToAddress:       fromProtoAddress(req.GetToAddress()),
	}

	sh, existed, err := s.svc.CreateReturnShipment(ctx, in)
	if err != nil {
		return nil, mapError(err)
	}
	return &shippingv1.CreateReturnShipmentResponse{
		Shipment:       toProtoShipment(sh),
		AlreadyExisted: existed,
	}, nil
}

func (s *Server) CancelShipment(ctx context.Context, req *shippingv1.CancelShipmentRequest) (*shippingv1.CancelShipmentResponse, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}
	if !isServiceOrHasCapability(claims, auth.CapOrdersReviewResolve) {
		return nil, status.Error(codes.PermissionDenied, "only internal service or authorized operations staff may cancel shipments")
	}

	sh, err := s.svc.CancelShipment(ctx, req.GetOrderId(), fromProtoDirection(req.GetDirection()), req.GetReason())
	if err != nil {
		return nil, mapError(err)
	}
	return &shippingv1.CancelShipmentResponse{
		Shipment: toProtoShipment(sh),
	}, nil
}

func (s *Server) GetShipment(ctx context.Context, req *shippingv1.GetShipmentRequest) (*shippingv1.GetShipmentResponse, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}

	sh, evList, err := s.svc.GetShipment(ctx, req.GetOrderId(), req.GetTrackingCode(), fromProtoDirection(req.GetDirection()))
	if err != nil {
		return nil, mapError(err)
	}
	if claims.Service == "" {
		canAdminRead := auth.HasCapability(claims, auth.CapOrdersRead) || auth.HasCapability(claims, auth.CapCarriersRead)
		isShopOwner := auth.HasRole(claims, auth.RoleSeller) && claims.ShopID != "" && claims.ShopID == sh.ShopID
		isBuyerOwner := sh.BuyerID != "" && sh.BuyerID == claims.UserID()
		if !canAdminRead && !isShopOwner && !isBuyerOwner {
			return nil, status.Error(codes.PermissionDenied, "caller is not authorized to access this shipment")
		}
	}

	protoEvents := make([]*shippingv1.ShipmentEvent, 0, len(evList))
	for _, ev := range evList {
		protoEvents = append(protoEvents, &shippingv1.ShipmentEvent{
			Id:          ev.ID,
			ShipmentId:  ev.ShipmentID,
			Status:      toProtoStatus(ev.Status),
			RawStatus:   ev.RawStatus,
			Description: ev.Description,
			OccurredAt:  timestamppb.New(ev.OccurredAt),
		})
	}
	return &shippingv1.GetShipmentResponse{
		Shipment: toProtoShipment(sh),
		Events:   protoEvents,
	}, nil
}

func (s *Server) RequestRedelivery(ctx context.Context, req *shippingv1.RequestRedeliveryRequest) (*shippingv1.RequestRedeliveryResponse, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}
	if claims.Service != "" {
		return nil, status.Error(codes.PermissionDenied, "service identity not allowed for RequestRedelivery")
	}

	sh, err := s.svc.RequestRedelivery(ctx, claims.UserID(), req.GetOrderId(), req.GetPreferredDate(), req.GetNote())
	if err != nil {
		return nil, mapError(err)
	}
	return &shippingv1.RequestRedeliveryResponse{
		Shipment: toProtoShipment(sh),
	}, nil
}

func (s *Server) GetShopShippingSettings(ctx context.Context, req *shippingv1.GetShopShippingSettingsRequest) (*shippingv1.GetShopShippingSettingsResponse, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}

	reqShopID := strings.TrimSpace(req.GetShopId())
	var (
		shopID     string
		fullAccess bool
	)
	switch {
	case claims.Service != "" || auth.HasCapability(claims, auth.CapCarriersRead) || auth.HasCapability(claims, auth.CapShopsReview):
		shopID = reqShopID
		fullAccess = true
	case auth.HasRole(claims, auth.RoleSeller) && claims.ShopID != "" && (reqShopID == "" || reqShopID == claims.ShopID):
		shopID = claims.ShopID
		fullAccess = true
	default:
		if reqShopID == "" {
			return nil, status.Error(codes.InvalidArgument, "shop_id is required")
		}
		shopID = reqShopID
		fullAccess = false
	}

	st, err := s.svc.GetShopShippingSettings(ctx, shopID)
	if err != nil {
		return nil, mapError(err)
	}
	if !fullAccess {
		return &shippingv1.GetShopShippingSettingsResponse{
			Settings: toProtoPublicShopSettings(st),
		}, nil
	}
	return &shippingv1.GetShopShippingSettingsResponse{
		Settings: toProtoShopSettings(st),
	}, nil
}

func (s *Server) UpdateShopShippingSettings(ctx context.Context, req *shippingv1.UpdateShopShippingSettingsRequest) (*shippingv1.UpdateShopShippingSettingsResponse, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}
	if claims.Service != "" {
		return nil, status.Error(codes.PermissionDenied, "service identity not allowed for UpdateShopShippingSettings")
	}

	var shopID string
	switch {
	case auth.HasRole(claims, auth.RoleSeller):
		if claims.ShopID == "" {
			return nil, status.Error(codes.PermissionDenied, "seller token missing shop_id")
		}
		if req.GetShopId() != "" && req.GetShopId() != claims.ShopID {
			return nil, status.Error(codes.PermissionDenied, "seller cannot update another shop's shipping settings")
		}
		shopID = claims.ShopID
	case auth.HasCapability(claims, auth.CapCarriersWrite):
		shopID = req.GetShopId()
	default:
		return nil, status.Error(codes.PermissionDenied, "only seller or authorized operations staff may update shop shipping settings")
	}

	st, err := s.svc.UpdateShopShippingSettings(ctx, &store.ShopShippingSettings{
		ShopID:              shopID,
		EnabledCarrierCodes: req.GetEnabledCarrierCodes(),
		DefaultWeightGrams:  req.GetDefaultWeightGrams(),
		DefaultLengthCM:     req.GetDefaultLengthCm(),
		DefaultWidthCM:      req.GetDefaultWidthCm(),
		DefaultHeightCM:     req.GetDefaultHeightCm(),
		FreeShipEnabled:     req.GetFreeShipEnabled(),
		FreeShipMinOrderVND: req.GetFreeShipMinOrderVnd(),
		FreeShipMaxVND:      req.GetFreeShipMaxVnd(),
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &shippingv1.UpdateShopShippingSettingsResponse{
		Settings: toProtoShopSettings(st),
	}, nil
}

func (s *Server) AdminListCarriers(ctx context.Context, _ *shippingv1.AdminListCarriersRequest) (*shippingv1.AdminListCarriersResponse, error) {
	if err := requireCapability(ctx, auth.CapCarriersRead); err != nil {
		return nil, err
	}
	views, err := s.svc.AdminListCarriers(ctx)
	if err != nil {
		return nil, mapError(err)
	}
	carriers := make([]*shippingv1.CarrierConfig, 0, len(views))
	for i := range views {
		carriers = append(carriers, toProtoCarrierConfig(&views[i]))
	}
	return &shippingv1.AdminListCarriersResponse{Carriers: carriers}, nil
}

func (s *Server) AdminUpdateCarrier(ctx context.Context, req *shippingv1.AdminUpdateCarrierRequest) (*shippingv1.AdminUpdateCarrierResponse, error) {
	if err := requireCapability(ctx, auth.CapCarriersWrite); err != nil {
		return nil, err
	}
	rates := make([]carrier.FallbackRate, 0, len(req.GetFallbackRates()))
	for _, r := range req.GetFallbackRates() {
		rates = append(rates, carrier.FallbackRate{
			Region:             fromProtoRegion(r.GetRegion()),
			BaseFeeVND:         r.GetBaseFeeVnd(),
			BaseWeightGrams:    r.GetBaseWeightGrams(),
			ExtraFeeVNDPer500g: r.GetExtraFeeVndPer_500G(),
		})
	}
	view, err := s.svc.AdminUpdateCarrier(ctx, service.AdminUpdateCarrierInput{
		CarrierCode:   req.GetCarrierCode(),
		Enabled:       req.GetEnabled(),
		APIToken:      req.GetApiToken(),
		FallbackRates: rates,
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &shippingv1.AdminUpdateCarrierResponse{
		Carrier: toProtoCarrierConfig(view),
	}, nil
}

func (s *Server) AdminTestCarrier(ctx context.Context, req *shippingv1.AdminTestCarrierRequest) (*shippingv1.AdminTestCarrierResponse, error) {
	if err := requireCapability(ctx, auth.CapCarriersWrite); err != nil {
		return nil, err
	}
	ok, msg, err := s.svc.AdminTestCarrier(ctx, req.GetCarrierCode())
	if err != nil {
		return nil, mapError(err)
	}
	return &shippingv1.AdminTestCarrierResponse{
		Ok:      ok,
		Message: msg,
	}, nil
}

func isServiceOrHasCapability(claims *auth.Claims, capName string) bool {
	if claims == nil {
		return false
	}
	return claims.Service != "" || auth.HasCapability(claims, capName)
}

func requireCapability(ctx context.Context, capName string) error {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "authentication required")
	}
	if !auth.HasCapability(claims, capName) {
		return status.Error(codes.PermissionDenied, "insufficient permissions")
	}
	return nil
}

func fromProtoAddress(a *shippingv1.ShipmentAddress) carrier.Address {
	if a == nil {
		return carrier.Address{}
	}
	return carrier.Address{
		Name:          a.GetName(),
		Phone:         a.GetPhone(),
		ProvinceCode:  a.GetProvinceCode(),
		WardCode:      a.GetWardCode(),
		StreetAddress: a.GetStreetAddress(),
	}
}

func fromProtoDirection(d shippingv1.ShipmentDirection) string {
	switch d {
	case shippingv1.ShipmentDirection_SHIPMENT_DIRECTION_RETURN:
		return store.DirectionReturn
	default:
		return store.DirectionForward
	}
}

func toProtoDirection(d string) shippingv1.ShipmentDirection {
	switch store.NormalizeDirection(d) {
	case store.DirectionReturn:
		return shippingv1.ShipmentDirection_SHIPMENT_DIRECTION_RETURN
	default:
		return shippingv1.ShipmentDirection_SHIPMENT_DIRECTION_FORWARD
	}
}

func fromProtoRegion(r shippingv1.ShippingRegion) string {
	switch r {
	case shippingv1.ShippingRegion_SHIPPING_REGION_SAME_PROVINCE:
		return carrier.RegionSameProvince
	case shippingv1.ShippingRegion_SHIPPING_REGION_SAME_REGION:
		return carrier.RegionSameRegion
	case shippingv1.ShippingRegion_SHIPPING_REGION_CROSS_REGION:
		return carrier.RegionCrossRegion
	default:
		return ""
	}
}

func toProtoRegion(r string) shippingv1.ShippingRegion {
	switch r {
	case carrier.RegionSameProvince:
		return shippingv1.ShippingRegion_SHIPPING_REGION_SAME_PROVINCE
	case carrier.RegionSameRegion:
		return shippingv1.ShippingRegion_SHIPPING_REGION_SAME_REGION
	case carrier.RegionCrossRegion:
		return shippingv1.ShippingRegion_SHIPPING_REGION_CROSS_REGION
	default:
		return shippingv1.ShippingRegion_SHIPPING_REGION_UNSPECIFIED
	}
}

func toProtoShipment(sh *store.Shipment) *shippingv1.Shipment {
	if sh == nil {
		return nil
	}
	return &shippingv1.Shipment{
		ShipmentId:      sh.ID,
		OrderId:         sh.OrderID,
		ShopId:          sh.ShopID,
		CarrierCode:     sh.CarrierCode,
		ServiceCode:     sh.ServiceCode,
		TrackingCode:    sh.TrackingCode,
		LabelUrl:        sh.LabelURL,
		Status:          toProtoStatus(sh.Status),
		FeeVnd:          sh.FeeVND,
		CodAmountVnd:    sh.CODAmountVND,
		CreatedAt:       timestamppb.New(sh.CreatedAt),
		UpdatedAt:       timestamppb.New(sh.UpdatedAt),
		Direction:       toProtoDirection(sh.Direction),
		ReturnRequestId: sh.ReturnRequestID,
		RedeliveryCount: sh.RedeliveryCount,
	}
}

func toProtoShopSettings(st *store.ShopShippingSettings) *shippingv1.ShopShippingSettings {
	out := &shippingv1.ShopShippingSettings{
		ShopId:              st.ShopID,
		EnabledCarrierCodes: append([]string(nil), st.EnabledCarrierCodes...),
		DefaultWeightGrams:  st.DefaultWeightGrams,
		DefaultLengthCm:     st.DefaultLengthCM,
		DefaultWidthCm:      st.DefaultWidthCM,
		DefaultHeightCm:     st.DefaultHeightCM,
		FreeShipEnabled:     st.FreeShipEnabled,
		FreeShipMinOrderVnd: st.FreeShipMinOrderVND,
		FreeShipMaxVnd:      st.FreeShipMaxVND,
	}
	if !st.UpdatedAt.IsZero() {
		out.UpdatedAt = timestamppb.New(st.UpdatedAt)
	}
	return out
}

func toProtoPublicShopSettings(st *store.ShopShippingSettings) *shippingv1.ShopShippingSettings {
	return &shippingv1.ShopShippingSettings{
		ShopId:              st.ShopID,
		DefaultWeightGrams:  st.DefaultWeightGrams,
		FreeShipEnabled:     st.FreeShipEnabled,
		FreeShipMinOrderVnd: st.FreeShipMinOrderVND,
		FreeShipMaxVnd:      st.FreeShipMaxVND,
	}
}

func toProtoCarrierConfig(v *service.CarrierConfigView) *shippingv1.CarrierConfig {
	rates := make([]*shippingv1.FallbackRate, 0, len(v.FallbackRates))
	for _, r := range v.FallbackRates {
		rates = append(rates, &shippingv1.FallbackRate{
			Region:              toProtoRegion(r.Region),
			BaseFeeVnd:          r.BaseFeeVND,
			BaseWeightGrams:     r.BaseWeightGrams,
			ExtraFeeVndPer_500G: r.ExtraFeeVNDPer500g,
		})
	}
	out := &shippingv1.CarrierConfig{
		CarrierCode:     v.CarrierCode,
		Name:            v.Name,
		Enabled:         v.Enabled,
		TokenConfigured: v.TokenConfigured,
		FallbackRates:   rates,
	}
	if !v.UpdatedAt.IsZero() {
		out.UpdatedAt = timestamppb.New(v.UpdatedAt)
	}
	return out
}

func toProtoStatus(s string) shippingv1.ShipmentStatus {
	switch s {
	case carrier.StatusCreated:
		return shippingv1.ShipmentStatus_SHIPMENT_STATUS_CREATED
	case carrier.StatusPicking:
		return shippingv1.ShipmentStatus_SHIPMENT_STATUS_PICKING
	case carrier.StatusDelivering:
		return shippingv1.ShipmentStatus_SHIPMENT_STATUS_DELIVERING
	case carrier.StatusDelivered:
		return shippingv1.ShipmentStatus_SHIPMENT_STATUS_DELIVERED
	case carrier.StatusFailed:
		return shippingv1.ShipmentStatus_SHIPMENT_STATUS_FAILED
	case carrier.StatusReturning:
		return shippingv1.ShipmentStatus_SHIPMENT_STATUS_RETURNING
	case carrier.StatusReturned:
		return shippingv1.ShipmentStatus_SHIPMENT_STATUS_RETURNED
	case carrier.StatusCancelled:
		return shippingv1.ShipmentStatus_SHIPMENT_STATUS_CANCELLED
	case carrier.StatusQueued:
		return shippingv1.ShipmentStatus_SHIPMENT_STATUS_QUEUED
	default:
		return shippingv1.ShipmentStatus_SHIPMENT_STATUS_UNSPECIFIED
	}
}

func mapError(err error) error {
	switch {
	case errors.Is(err, service.ErrInvalidArgument),
		errors.Is(err, service.ErrOutOfServiceArea),
		errors.Is(err, service.ErrUnknownCarrier),
		errors.Is(err, service.ErrUnknownStatus),
		errors.Is(err, service.ErrNoEnabledCarriers),
		errors.Is(err, service.ErrCarrierAuthFailed):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, service.ErrPermissionDenied), errors.Is(err, store.ErrBuyerMismatch):
		return status.Error(codes.PermissionDenied, err.Error())
	case errors.Is(err, store.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, service.ErrNotCancellable),
		errors.Is(err, store.ErrCannotCancel),
		errors.Is(err, service.ErrCarrierDisabled),
		errors.Is(err, service.ErrCarrierRejected),
		errors.Is(err, service.ErrReturnShipmentFailed),
		errors.Is(err, service.ErrLegacyShipmentNoBuyer),
		errors.Is(err, store.ErrLegacyNoBuyer),
		errors.Is(err, service.ErrRedeliveryNotAllowed),
		errors.Is(err, store.ErrRedeliveryNotAllowed),
		errors.Is(err, service.ErrRedeliveryAlreadyRequested),
		errors.Is(err, store.ErrRedeliveryAlreadyRequested),
		errors.Is(err, service.ErrMaxRedeliveryExceeded),
		errors.Is(err, store.ErrMaxRedeliveryExceeded):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, service.ErrCarrierUnavailable):
		return status.Error(codes.Unavailable, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}
