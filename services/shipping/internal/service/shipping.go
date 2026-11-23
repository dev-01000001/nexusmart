package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/shipping/internal/address"
	"github.com/nexusmart/core/services/shipping/internal/cache"
	"github.com/nexusmart/core/services/shipping/internal/carrier"
	"github.com/nexusmart/core/services/shipping/internal/events"
	"github.com/nexusmart/core/services/shipping/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	ErrInvalidArgument  = errors.New("invalid argument")
	ErrOutOfServiceArea = errors.New("address or route outside service area")
	ErrNotCancellable   = errors.New("shipment cannot be cancelled after pickup")
	ErrUnknownCarrier   = errors.New("unknown carrier")
	ErrUnknownStatus    = errors.New("unsupported carrier status")
)

// OutboxNotifier wakes the outbox relay immediately after a webhook transaction commits.
type OutboxNotifier interface {
	Wake()
}

// ShopPickupResolver resolves a shop's pickup (province_code, ward_code) from UserService.
type ShopPickupResolver interface {
	GetShopPickupCodes(ctx context.Context, shopID string) (provinceCode string, wardCode string, err error)
}

// ShippingService orchestrates address lookup, carrier quoting, shipment lifecycle, and webhooks.
type ShippingService struct {
	repo       store.Repository
	aggregator *carrier.Aggregator
	quoteCache cache.QuoteCache
	outboxWake OutboxNotifier
	shopPickup ShopPickupResolver
	logger     *slog.Logger
}

// SetShopPickupResolver configures the resolver used when CalculateFee receives shop_id without origin codes.
func (s *ShippingService) SetShopPickupResolver(r ShopPickupResolver) {
	s.shopPickup = r
}

// NewShippingService constructs a ShippingService.
func NewShippingService(
	repo store.Repository,
	aggregator *carrier.Aggregator,
	quoteCache cache.QuoteCache,
	outboxWake OutboxNotifier,
	logger *slog.Logger,
) *ShippingService {
	if logger == nil {
		logger = slog.Default()
	}
	return &ShippingService{
		repo:       repo,
		aggregator: aggregator,
		quoteCache: quoteCache,
		outboxWake: outboxWake,
		logger:     logger,
	}
}

func (s *ShippingService) ListProvinces(ctx context.Context, query string) ([]address.Province, error) {
	return s.repo.ListProvinces(ctx, query)
}

func (s *ShippingService) ListWards(ctx context.Context, provinceCode, query string) ([]address.Ward, error) {
	if strings.TrimSpace(provinceCode) == "" {
		return nil, fmt.Errorf("%w: province_code is required", ErrInvalidArgument)
	}
	return s.repo.ListWards(ctx, provinceCode, query)
}

func (s *ShippingService) MapLegacyAddress(ctx context.Context, legacyProv, legacyDist, legacyWard string) (*address.LegacyMatch, error) {
	if strings.TrimSpace(legacyProv) == "" {
		return nil, fmt.Errorf("%w: legacy_province is required", ErrInvalidArgument)
	}
	return s.repo.MapLegacyAddress(ctx, legacyProv, legacyDist, legacyWard)
}

// CalculateFee validates input and service area, resolves shop pickup when origin is omitted,
// checks Redis quote cache using real origin/destination codes, then fans out to enabled carriers.
func (s *ShippingService) CalculateFee(ctx context.Context, req carrier.FeeRequest) ([]carrier.Quote, error) {
	req.ShopID = strings.TrimSpace(req.ShopID)
	req.FromProvinceCode = strings.TrimSpace(req.FromProvinceCode)
	req.FromWardCode = strings.TrimSpace(req.FromWardCode)
	req.ToProvinceCode = strings.TrimSpace(req.ToProvinceCode)
	req.ToWardCode = strings.TrimSpace(req.ToWardCode)

	if req.WeightGrams <= 0 {
		return nil, fmt.Errorf("%w: weight_grams must be > 0", ErrInvalidArgument)
	}
	if req.CODAmountVND < 0 {
		return nil, fmt.Errorf("%w: cod_amount_vnd must be >= 0", ErrInvalidArgument)
	}

	// Resolve origin from ward or shop pickup address when from_province_code is omitted (SHP-B1).
	if req.FromProvinceCode == "" {
		if req.FromWardCode != "" {
			w, err := s.repo.GetWard(ctx, req.FromWardCode)
			if err != nil {
				if errors.Is(err, store.ErrNotFound) {
					return nil, fmt.Errorf("%w: unsupported origin ward_code %q", ErrOutOfServiceArea, req.FromWardCode)
				}
				return nil, err
			}
			req.FromProvinceCode = w.ProvinceCode
		} else if req.ShopID != "" {
			if s.shopPickup == nil {
				return nil, fmt.Errorf("shop pickup resolver not configured")
			}
			prov, ward, err := s.shopPickup.GetShopPickupCodes(ctx, req.ShopID)
			if err != nil {
				switch status.Code(err) {
				case codes.NotFound:
					return nil, fmt.Errorf("%w: shop %s not found", store.ErrNotFound, req.ShopID)
				case codes.InvalidArgument:
					return nil, fmt.Errorf("%w: %w", ErrInvalidArgument, err)
				default:
					return nil, fmt.Errorf("resolve shop pickup address: %w", err)
				}
			}
			if strings.TrimSpace(prov) == "" {
				return nil, fmt.Errorf("%w: shop %s has no pickup address configured", ErrInvalidArgument, req.ShopID)
			}
			req.FromProvinceCode = strings.TrimSpace(prov)
			req.FromWardCode = strings.TrimSpace(ward)
		} else {
			return nil, fmt.Errorf("%w: from_province_code or shop_id is required", ErrInvalidArgument)
		}
	}

	// Validate origin province & ward against 34-province catalog and enrich GHN codes (SHP-B7).
	fromDistrictID, fromGHNWard, err := s.validateAndEnrichAddress(ctx, "origin", req.FromProvinceCode, req.FromWardCode)
	if err != nil {
		return nil, err
	}
	req.FromDistrictID = fromDistrictID
	req.FromGHNWardCode = fromGHNWard

	// Resolve destination province from ward when omitted, then validate against 34-province catalog.
	if req.ToProvinceCode == "" && req.ToWardCode != "" {
		w, wErr := s.repo.GetWard(ctx, req.ToWardCode)
		if wErr != nil {
			if errors.Is(wErr, store.ErrNotFound) {
				return nil, fmt.Errorf("%w: unsupported destination ward_code %q", ErrOutOfServiceArea, req.ToWardCode)
			}
			return nil, wErr
		}
		req.ToProvinceCode = w.ProvinceCode
	}
	if req.ToProvinceCode == "" {
		return nil, fmt.Errorf("%w: to_province_code is required", ErrInvalidArgument)
	}
	toDistrictID, toGHNWard, err := s.validateAndEnrichAddress(ctx, "destination", req.ToProvinceCode, req.ToWardCode)
	if err != nil {
		return nil, err
	}
	req.ToDistrictID = toDistrictID
	req.ToGHNWardCode = toGHNWard

	// Check cache ONLY after resolving and validating real origin/destination codes.
	if s.quoteCache != nil {
		if cached, ok := s.quoteCache.Get(ctx, req); ok {
			return cached, nil
		}
	}

	quotes, isFallback, err := s.aggregator.CalculateFeeAll(ctx, req)
	if err != nil {
		if errors.Is(err, carrier.ErrRouteOutOfServiceArea) {
			return nil, fmt.Errorf("%w: %w", ErrOutOfServiceArea, err)
		}
		return nil, err
	}
	if !isFallback && s.quoteCache != nil {
		s.quoteCache.Set(ctx, req, quotes)
	}
	return quotes, nil
}

func (s *ShippingService) validateAndEnrichAddress(ctx context.Context, role, provinceCode, wardCode string) (int, string, error) {
	prov, err := s.repo.GetProvince(ctx, provinceCode)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return 0, "", fmt.Errorf("%w: unsupported %s province_code %q", ErrOutOfServiceArea, role, provinceCode)
		}
		return 0, "", err
	}
	if !isSupportedRegion(prov.Region) {
		return 0, "", fmt.Errorf("%w: %s province %q is outside service area", ErrOutOfServiceArea, role, provinceCode)
	}
	if wardCode == "" {
		return 0, "", nil
	}
	w, err := s.repo.GetWard(ctx, wardCode)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return 0, "", fmt.Errorf("%w: unsupported %s ward_code %q", ErrOutOfServiceArea, role, wardCode)
		}
		return 0, "", err
	}
	if w.ProvinceCode != provinceCode {
		return 0, "", fmt.Errorf("%w: %s ward_code %q does not belong to province_code %q", ErrInvalidArgument, role, wardCode, provinceCode)
	}
	return w.GHNDistrictID, w.GHNWardCode, nil
}

func isSupportedRegion(region string) bool {
	return region == address.RegionNorth || region == address.RegionCentral || region == address.RegionSouth
}

// CreateShipmentInput represents a shipment creation request.
type CreateShipmentInput struct {
	OrderID      string
	ShopID       string
	CarrierCode  string
	ServiceCode  string
	CODAmountVND int64
	WeightGrams  int32
	FromAddress  carrier.Address
	ToAddress    carrier.Address
	ItemsSummary string
}

// CreateShipment creates a carrier shipment idempotently keyed by order_id (UC-SHIP03).
func (s *ShippingService) CreateShipment(ctx context.Context, in CreateShipmentInput) (*store.Shipment, bool, error) {
	if strings.TrimSpace(in.OrderID) == "" {
		return nil, false, fmt.Errorf("%w: order_id is required", ErrInvalidArgument)
	}
	if strings.TrimSpace(in.ShopID) == "" {
		return nil, false, fmt.Errorf("%w: shop_id is required", ErrInvalidArgument)
	}
	if in.WeightGrams <= 0 {
		in.WeightGrams = 500
	}

	// Fast-path idempotency check before calling external carrier APIs.
	if existing, err := s.repo.GetShipmentByOrderID(ctx, in.OrderID); err == nil && existing != nil {
		return existing, true, nil
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, false, fmt.Errorf("check existing shipment: %w", err)
	}

	// Select requested carrier (or default carrier if empty/fallback).
	provider, err := s.resolveCarrier(in.CarrierCode)
	if err != nil {
		return nil, false, err
	}

	// Enrich GHN ward/district metadata from our address catalog when present.
	if w, wErr := s.repo.GetWard(ctx, in.FromAddress.WardCode); wErr == nil && w != nil {
		in.FromAddress.GHNDistrictID = w.GHNDistrictID
		in.FromAddress.GHNWardCode = w.GHNWardCode
	}
	if w, wErr := s.repo.GetWard(ctx, in.ToAddress.WardCode); wErr == nil && w != nil {
		in.ToAddress.GHNDistrictID = w.GHNDistrictID
		in.ToAddress.GHNWardCode = w.GHNWardCode
	}

	res, err := provider.CreateShipment(ctx, carrier.ShipmentRequest{
		OrderID:      in.OrderID,
		ShopID:       in.ShopID,
		ServiceCode:  in.ServiceCode,
		CODAmountVND: in.CODAmountVND,
		WeightGrams:  in.WeightGrams,
		FromAddress:  in.FromAddress,
		ToAddress:    in.ToAddress,
		ItemsSummary: in.ItemsSummary,
	})
	if err != nil {
		return nil, false, fmt.Errorf("carrier create shipment: %w", err)
	}

	now := time.Now().UTC()
	sh := &store.Shipment{
		ID:               uuid.NewString(),
		OrderID:          in.OrderID,
		ShopID:           in.ShopID,
		CarrierCode:      res.CarrierCode,
		ServiceCode:      res.ServiceCode,
		TrackingCode:     res.TrackingCode,
		LabelURL:         res.LabelURL,
		Status:           carrier.StatusCreated,
		FeeVND:           res.FeeVND,
		CODAmountVND:     in.CODAmountVND,
		WeightGrams:      in.WeightGrams,
		FromProvinceCode: in.FromAddress.ProvinceCode,
		FromWardCode:     in.FromAddress.WardCode,
		ToProvinceCode:   in.ToAddress.ProvinceCode,
		ToWardCode:       in.ToAddress.WardCode,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	initEv := &store.ShipmentEvent{
		ID:          uuid.NewString(),
		ShipmentID:  sh.ID,
		Status:      carrier.StatusCreated,
		RawStatus:   carrier.StatusCreated,
		Description: "Vận đơn đã được tạo",
		OccurredAt:  now,
	}

	saved, alreadyExisted, err := s.repo.CreateShipment(ctx, sh, initEv)
	if err != nil {
		return nil, false, fmt.Errorf("persist shipment: %w", err)
	}
	return saved, alreadyExisted, nil
}

// CancelShipment cancels a shipment if it has not yet been picked up (CREATED/PICKING).
func (s *ShippingService) CancelShipment(ctx context.Context, orderID, reason string) (*store.Shipment, error) {
	if strings.TrimSpace(orderID) == "" {
		return nil, fmt.Errorf("%w: order_id is required", ErrInvalidArgument)
	}
	sh, err := s.repo.GetShipmentByOrderID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if sh.Status == carrier.StatusCancelled {
		return sh, nil
	}
	if !carrier.IsCancellableStatus(sh.Status) {
		return nil, fmt.Errorf("%w: current status is %s", ErrNotCancellable, sh.Status)
	}

	if provider, ok := s.aggregator.Get(sh.CarrierCode); ok {
		if err := provider.CancelShipment(ctx, sh.TrackingCode); err != nil {
			return nil, fmt.Errorf("carrier cancel shipment: %w", err)
		}
	}

	return s.repo.CancelShipment(ctx, orderID, reason)
}

// GetShipment retrieves a shipment and its timeline events by order_id or tracking_code.
func (s *ShippingService) GetShipment(ctx context.Context, orderID, trackingCode string) (*store.Shipment, []store.ShipmentEvent, error) {
	orderID = strings.TrimSpace(orderID)
	trackingCode = strings.TrimSpace(trackingCode)
	if orderID == "" && trackingCode == "" {
		return nil, nil, fmt.Errorf("%w: order_id or tracking_code is required", ErrInvalidArgument)
	}

	var (
		sh  *store.Shipment
		err error
	)
	if orderID != "" {
		sh, err = s.repo.GetShipmentByOrderID(ctx, orderID)
	} else {
		sh, err = s.repo.GetShipmentByTrackingCode(ctx, trackingCode)
	}
	if err != nil {
		return nil, nil, err
	}

	evList, err := s.repo.ListShipmentEvents(ctx, sh.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("list shipment events: %w", err)
	}
	return sh, evList, nil
}

// WebhookInput represents a normalized incoming carrier webhook payload.
type WebhookInput struct {
	EventID      string
	CarrierCode  string
	TrackingCode string
	OrderID      string
	RawStatus    string
	Description  string
	OccurredAt   time.Time
}

// HandleWebhook validates, maps status, deduplicates, rejects stale out-of-order updates,
// and atomically persists the status + outbox shipment.updated event (UC-SHIP04).
func (s *ShippingService) HandleWebhook(ctx context.Context, in WebhookInput) (*store.Shipment, store.WebhookUpdateOutcome, error) {
	if strings.TrimSpace(in.EventID) == "" {
		return nil, "", fmt.Errorf("%w: event_id is required", ErrInvalidArgument)
	}
	carrierCode := strings.ToLower(strings.TrimSpace(in.CarrierCode))
	if carrierCode == "" {
		return nil, "", fmt.Errorf("%w: carrier is required", ErrInvalidArgument)
	}
	trackingCode := strings.TrimSpace(in.TrackingCode)
	orderID := strings.TrimSpace(in.OrderID)
	if trackingCode == "" && orderID == "" {
		return nil, "", fmt.Errorf("%w: tracking_code or order_id is required", ErrInvalidArgument)
	}

	internalStatus, ok := carrier.MapCarrierStatus(in.RawStatus)
	if !ok {
		return nil, "", fmt.Errorf("%w: %s", ErrUnknownStatus, in.RawStatus)
	}

	// Verify the webhook path carrier matches the shipment's carrier_code before applying (SHP-B6).
	var (
		existing *store.Shipment
		findErr  error
	)
	if trackingCode != "" {
		existing, findErr = s.repo.GetShipmentByTrackingCode(ctx, trackingCode)
	}
	if (existing == nil || errors.Is(findErr, store.ErrNotFound)) && orderID != "" {
		existing, findErr = s.repo.GetShipmentByOrderID(ctx, orderID)
	}
	if findErr != nil {
		return nil, "", findErr
	}
	if !strings.EqualFold(carrierCode, existing.CarrierCode) {
		return nil, "", fmt.Errorf("%w: webhook carrier %q does not match shipment carrier %q", store.ErrCarrierMismatch, carrierCode, existing.CarrierCode)
	}

	occurredAt := in.OccurredAt.UTC()
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}

	traceHeaders := map[string]string{}
	telemetry.InjectTraceHeaders(ctx, traceHeaders)
	traceparent := traceHeaders["traceparent"]
	traceID := telemetry.TraceIDFromContext(ctx)

	outboxEventID := uuid.NewString()
	params := store.WebhookUpdateParams{
		EventID:        in.EventID,
		CarrierCode:    carrierCode,
		TrackingCode:   trackingCode,
		OrderID:        orderID,
		InternalStatus: internalStatus,
		RawStatus:      strings.TrimSpace(in.RawStatus),
		Description:    strings.TrimSpace(in.Description),
		OccurredAt:     occurredAt,
		OutboxEventID:  outboxEventID,
		OutboxSubject:  events.SubjectShipmentUpdated,
		Traceparent:    traceparent,
		TraceID:        traceID,
		BuildOutboxPayload: func(sh *store.Shipment) ([]byte, error) {
			return json.Marshal(events.ShipmentUpdatedPayload{
				ShipmentID:   sh.ID,
				OrderID:      sh.OrderID,
				ShopID:       sh.ShopID,
				CarrierCode:  sh.CarrierCode,
				TrackingCode: sh.TrackingCode,
				Status:       internalStatus,
				RawStatus:    strings.TrimSpace(in.RawStatus),
				Description:  strings.TrimSpace(in.Description),
				OccurredAt:   occurredAt.Format(time.RFC3339),
			})
		},
	}

	updated, outcome, err := s.repo.ApplyWebhookEvent(ctx, params)
	if err != nil {
		return nil, "", err
	}
	if outcome == store.WebhookOutcomeApplied && s.outboxWake != nil {
		s.outboxWake.Wake()
	}
	return updated, outcome, nil
}

// HandleOrderCancelled handles an async order.cancelled NATS event idempotently (UC-SHIP07).
func (s *ShippingService) HandleOrderCancelled(ctx context.Context, eventID, orderID, reason string) error {
	sh, err := s.repo.GetShipmentByOrderID(ctx, orderID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// Order cancelled before any shipment was created — mark processed cleanly.
			_, markErr := s.repo.MarkEventProcessed(ctx, eventID, events.SubjectOrderCancelled)
			return markErr
		}
		return err
	}

	if sh.Status != carrier.StatusCancelled && carrier.IsCancellableStatus(sh.Status) {
		if provider, ok := s.aggregator.Get(sh.CarrierCode); ok {
			if cancelErr := provider.CancelShipment(ctx, sh.TrackingCode); cancelErr != nil {
				return fmt.Errorf("carrier cancel shipment: %w", cancelErr)
			}
		}
		_, _, txErr := s.repo.CancelShipmentWithEvent(ctx, eventID, events.SubjectOrderCancelled, orderID, reason)
		return txErr
	}

	if !carrier.IsCancellableStatus(sh.Status) && sh.Status != carrier.StatusCancelled {
		s.logger.InfoContext(ctx, "shipment already past pickup on order.cancelled; skipping cancel",
			slog.String("order_id", orderID),
			slog.String("status", sh.Status),
		)
	}

	_, err = s.repo.MarkEventProcessed(ctx, eventID, events.SubjectOrderCancelled)
	return err
}

// PollActiveShipments checks active non-terminal shipments for live (non-mock) carriers (UC-SHIP04).
func (s *ShippingService) PollActiveShipments(ctx context.Context) error {
	active, err := s.repo.ListActiveShipments(ctx, 100)
	if err != nil {
		return err
	}
	for _, sh := range active {
		provider, ok := s.aggregator.Get(sh.CarrierCode)
		if !ok || provider.IsMock() {
			continue
		}
		st, pollErr := provider.GetStatus(ctx, sh.TrackingCode)
		if pollErr != nil {
			s.logger.WarnContext(ctx, "fallback poll failed",
				slog.String("tracking_code", sh.TrackingCode),
				slog.String("carrier", sh.CarrierCode),
				slog.String("error", pollErr.Error()),
			)
			continue
		}
		if st == nil || !st.Changed || st.Status == "" || st.Status == sh.Status {
			continue
		}
		eventID := fmt.Sprintf("poll:%s:%s:%d", sh.TrackingCode, st.Status, st.OccurredAt.UTC().Unix())
		if _, _, err := s.HandleWebhook(ctx, WebhookInput{
			EventID:      eventID,
			CarrierCode:  sh.CarrierCode,
			TrackingCode: sh.TrackingCode,
			OrderID:      sh.OrderID,
			RawStatus:    st.RawStatus,
			Description:  st.Description,
			OccurredAt:   st.OccurredAt,
		}); err != nil {
			s.logger.WarnContext(ctx, "apply polled shipment status failed",
				slog.String("tracking_code", sh.TrackingCode),
				slog.String("error", err.Error()),
			)
		}
	}
	return nil
}

func (s *ShippingService) resolveCarrier(code string) (carrier.Carrier, error) {
	code = strings.ToLower(strings.TrimSpace(code))
	if code == "" || code == carrier.CodeFallback {
		c, ok := s.aggregator.DefaultCarrier()
		if !ok {
			return nil, fmt.Errorf("%w: no carriers enabled", ErrUnknownCarrier)
		}
		return c, nil
	}
	c, ok := s.aggregator.Get(code)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownCarrier, code)
	}
	return c, nil
}
