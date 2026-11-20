package grpcserver

import (
	"context"
	"errors"

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
	quotes, err := s.svc.CalculateFee(ctx, carrier.FeeRequest{
		ShopID:           req.GetShopId(),
		FromProvinceCode: req.GetFromProvinceCode(),
		FromWardCode:     req.GetFromWardCode(),
		ToProvinceCode:   req.GetToProvinceCode(),
		ToWardCode:       req.GetToWardCode(),
		WeightGrams:      req.GetWeightGrams(),
		CODAmountVND:     req.GetCodAmountVnd(),
	})
	if err != nil {
		return nil, mapError(err)
	}

	resp := &shippingv1.CalculateFeeResponse{
		Quotes: make([]*shippingv1.CarrierQuote, 0, len(quotes)),
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
		})
	}
	return resp, nil
}

func (s *Server) CreateShipment(ctx context.Context, req *shippingv1.CreateShipmentRequest) (*shippingv1.CreateShipmentResponse, error) {
	if claims, ok := auth.ClaimsFromContext(ctx); ok {
		if !auth.HasRole(claims, "seller", "admin", "super_admin") {
			return nil, status.Error(codes.PermissionDenied, "only seller or admin may create shipments")
		}
		if auth.HasRole(claims, "seller") {
			if claims.ShopID == "" || req.GetShopId() == "" || claims.ShopID != req.GetShopId() {
				return nil, status.Error(codes.PermissionDenied, "seller cannot create shipment for another shop")
			}
		}
	}

	in := service.CreateShipmentInput{
		OrderID:      req.GetOrderId(),
		ShopID:       req.GetShopId(),
		CarrierCode:  req.GetCarrierCode(),
		ServiceCode:  req.GetServiceCode(),
		CODAmountVND: req.GetCodAmountVnd(),
		WeightGrams:  req.GetWeightGrams(),
		ItemsSummary: req.GetItemsSummary(),
	}
	if from := req.GetFromAddress(); from != nil {
		in.FromAddress = carrier.Address{
			Name:          from.GetName(),
			Phone:         from.GetPhone(),
			ProvinceCode:  from.GetProvinceCode(),
			WardCode:      from.GetWardCode(),
			StreetAddress: from.GetStreetAddress(),
		}
	}
	if to := req.GetToAddress(); to != nil {
		in.ToAddress = carrier.Address{
			Name:          to.GetName(),
			Phone:         to.GetPhone(),
			ProvinceCode:  to.GetProvinceCode(),
			WardCode:      to.GetWardCode(),
			StreetAddress: to.GetStreetAddress(),
		}
	}

	sh, existed, err := s.svc.CreateShipment(ctx, in)
	if err != nil {
		return nil, mapError(err)
	}
	return &shippingv1.CreateShipmentResponse{
		Shipment:       toProtoShipment(sh),
		AlreadyExisted: existed,
	}, nil
}

func (s *Server) CancelShipment(ctx context.Context, req *shippingv1.CancelShipmentRequest) (*shippingv1.CancelShipmentResponse, error) {
	if claims, ok := auth.ClaimsFromContext(ctx); ok {
		if !auth.HasRole(claims, "seller", "admin", "super_admin") {
			return nil, status.Error(codes.PermissionDenied, "only seller or admin may cancel shipments")
		}
		if auth.HasRole(claims, "seller") {
			if claims.ShopID == "" {
				return nil, status.Error(codes.PermissionDenied, "seller token missing shop_id")
			}
			existing, _, getErr := s.svc.GetShipment(ctx, req.GetOrderId(), "")
			if getErr != nil {
				return nil, mapError(getErr)
			}
			if existing.ShopID != claims.ShopID {
				return nil, status.Error(codes.PermissionDenied, "seller cannot cancel another shop's shipment")
			}
		}
	}
	sh, err := s.svc.CancelShipment(ctx, req.GetOrderId(), req.GetReason())
	if err != nil {
		return nil, mapError(err)
	}
	return &shippingv1.CancelShipmentResponse{
		Shipment: toProtoShipment(sh),
	}, nil
}

func (s *Server) GetShipment(ctx context.Context, req *shippingv1.GetShipmentRequest) (*shippingv1.GetShipmentResponse, error) {
	sh, evList, err := s.svc.GetShipment(ctx, req.GetOrderId(), req.GetTrackingCode())
	if err != nil {
		return nil, mapError(err)
	}
	if claims, ok := auth.ClaimsFromContext(ctx); ok {
		if auth.HasRole(claims, "seller") {
			if claims.ShopID == "" || sh.ShopID != claims.ShopID {
				return nil, status.Error(codes.PermissionDenied, "seller cannot access another shop's shipment")
			}
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

func toProtoShipment(sh *store.Shipment) *shippingv1.Shipment {
	return &shippingv1.Shipment{
		ShipmentId:   sh.ID,
		OrderId:      sh.OrderID,
		ShopId:       sh.ShopID,
		CarrierCode:  sh.CarrierCode,
		ServiceCode:  sh.ServiceCode,
		TrackingCode: sh.TrackingCode,
		LabelUrl:     sh.LabelURL,
		Status:       toProtoStatus(sh.Status),
		FeeVnd:       sh.FeeVND,
		CodAmountVnd: sh.CODAmountVND,
		CreatedAt:    timestamppb.New(sh.CreatedAt),
		UpdatedAt:    timestamppb.New(sh.UpdatedAt),
	}
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
	default:
		return shippingv1.ShipmentStatus_SHIPMENT_STATUS_UNSPECIFIED
	}
}

func mapError(err error) error {
	switch {
	case errors.Is(err, service.ErrInvalidArgument), errors.Is(err, service.ErrUnknownCarrier), errors.Is(err, service.ErrUnknownStatus):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, store.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, service.ErrNotCancellable), errors.Is(err, store.ErrCannotCancel):
		return status.Error(codes.FailedPrecondition, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}
