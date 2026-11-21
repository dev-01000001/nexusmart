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
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrNotCancellable  = errors.New("shipment cannot be cancelled after pickup")
	ErrUnknownCarrier  = errors.New("unknown carrier")
	ErrUnknownStatus   = errors.New("unsupported carrier status")
)

// OutboxNotifier wakes the outbox relay immediately after a webhook transaction commits.
type OutboxNotifier interface {
	Wake()
}

// ShippingService orchestrates address lookup, carrier quoting, shipment lifecycle, and webhooks.
type ShippingService struct {
	repo       store.Repository
	aggregator *carrier.Aggregator
	quoteCache cache.QuoteCache
	outboxWake OutboxNotifier
	logger     *slog.Logger
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

// CalculateFee checks Redis quote cache, then fans out to enabled carriers in parallel with fallback.
func (s *ShippingService) CalculateFee(ctx context.Context, req carrier.FeeRequest) ([]carrier.Quote, error) {
	if req.WeightGrams <= 0 {
		req.WeightGrams = 500
	}
	if req.CODAmountVND < 0 {
		return nil, fmt.Errorf("%w: cod_amount_vnd must be >= 0", ErrInvalidArgument)
	}

	if s.quoteCache != nil {
		if cached, ok := s.quoteCache.Get(ctx, req); ok {
			return cached, nil
		}
	}

	// Enrich GHN district/ward codes and Vietnamese/legacy names from local catalog when available.
	fromEnriched := s.enrichAddress(ctx, carrier.Address{
		ProvinceCode: req.FromProvinceCode,
		WardCode:     req.FromWardCode,
	})
	req.FromProvinceCode = fromEnriched.ProvinceCode
	req.FromProvinceName = fromEnriched.ProvinceName
	req.FromWardName = fromEnriched.WardName
	req.FromLegacyProvince = fromEnriched.LegacyProvince
	req.FromLegacyDistrict = fromEnriched.LegacyDistrict
	req.FromDistrictID = fromEnriched.GHNDistrictID
	req.FromGHNWardCode = fromEnriched.GHNWardCode

	toEnriched := s.enrichAddress(ctx, carrier.Address{
		ProvinceCode: req.ToProvinceCode,
		WardCode:     req.ToWardCode,
	})
	req.ToProvinceCode = toEnriched.ProvinceCode
	req.ToProvinceName = toEnriched.ProvinceName
	req.ToWardName = toEnriched.WardName
	req.ToLegacyProvince = toEnriched.LegacyProvince
	req.ToLegacyDistrict = toEnriched.LegacyDistrict
	req.ToDistrictID = toEnriched.GHNDistrictID
	req.ToGHNWardCode = toEnriched.GHNWardCode

	quotes, isFallback := s.aggregator.CalculateFeeAll(ctx, req)
	if !isFallback && s.quoteCache != nil {
		s.quoteCache.Set(ctx, req, quotes)
	}
	return quotes, nil
}

// CreateShipmentInput represents a forward shipment creation request.
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

// CreateReturnShipmentInput represents a reverse/return shipment creation request (buyer -> shop).
type CreateReturnShipmentInput struct {
	OrderID         string
	ReturnRequestID string
	ShopID          string
	CarrierCode     string
	ServiceCode     string
	WeightGrams     int32
	FromAddress     carrier.Address
	ToAddress       carrier.Address
	ItemsSummary    string
}

// CreateShipment creates a forward carrier shipment idempotently keyed by (order_id, "forward") (UC-SHIP03).
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
	if existing, err := s.repo.GetShipmentByOrderIDAndDirection(ctx, in.OrderID, store.DirectionForward); err == nil && existing != nil {
		return existing, true, nil
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, false, fmt.Errorf("check existing shipment: %w", err)
	}

	// Select requested carrier (or default carrier if empty/fallback).
	provider, err := s.resolveCarrier(in.CarrierCode)
	if err != nil {
		return nil, false, err
	}

	in.FromAddress = s.enrichAddress(ctx, in.FromAddress)
	in.ToAddress = s.enrichAddress(ctx, in.ToAddress)

	res, err := provider.CreateShipment(ctx, carrier.ShipmentRequest{
		OrderID:      in.OrderID,
		ShopID:       in.ShopID,
		Direction:    store.DirectionForward,
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
		Direction:        store.DirectionForward,
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

// CreateReturnShipment creates a reverse/return carrier shipment idempotently keyed by (order_id, "return").
// Return shipments always have CODAmountVND=0 and pick up from buyer (FromAddress) to shop (ToAddress).
func (s *ShippingService) CreateReturnShipment(ctx context.Context, in CreateReturnShipmentInput) (*store.Shipment, bool, error) {
	if strings.TrimSpace(in.OrderID) == "" {
		return nil, false, fmt.Errorf("%w: order_id is required", ErrInvalidArgument)
	}
	if strings.TrimSpace(in.ShopID) == "" {
		return nil, false, fmt.Errorf("%w: shop_id is required", ErrInvalidArgument)
	}
	if strings.TrimSpace(in.ReturnRequestID) == "" {
		return nil, false, fmt.Errorf("%w: return_request_id is required", ErrInvalidArgument)
	}
	if in.WeightGrams <= 0 {
		in.WeightGrams = 500
	}

	// Fast-path idempotency check by (order_id, "return").
	if existing, err := s.repo.GetShipmentByOrderIDAndDirection(ctx, in.OrderID, store.DirectionReturn); err == nil && existing != nil {
		return existing, true, nil
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, false, fmt.Errorf("check existing return shipment: %w", err)
	}

	provider, err := s.resolveCarrier(in.CarrierCode)
	if err != nil {
		return nil, false, err
	}

	in.FromAddress = s.enrichAddress(ctx, in.FromAddress)
	in.ToAddress = s.enrichAddress(ctx, in.ToAddress)

	res, err := provider.CreateShipment(ctx, carrier.ShipmentRequest{
		OrderID:         in.OrderID,
		ShopID:          in.ShopID,
		Direction:       store.DirectionReturn,
		ReturnRequestID: in.ReturnRequestID,
		ServiceCode:     in.ServiceCode,
		CODAmountVND:    0,
		WeightGrams:     in.WeightGrams,
		FromAddress:     in.FromAddress,
		ToAddress:       in.ToAddress,
		ItemsSummary:    in.ItemsSummary,
	})
	if err != nil {
		return nil, false, fmt.Errorf("carrier create return shipment: %w", err)
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
		CODAmountVND:     0,
		WeightGrams:      in.WeightGrams,
		FromProvinceCode: in.FromAddress.ProvinceCode,
		FromWardCode:     in.FromAddress.WardCode,
		ToProvinceCode:   in.ToAddress.ProvinceCode,
		ToWardCode:       in.ToAddress.WardCode,
		Direction:        store.DirectionReturn,
		ReturnRequestID:  in.ReturnRequestID,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	initEv := &store.ShipmentEvent{
		ID:          uuid.NewString(),
		ShipmentID:  sh.ID,
		Status:      carrier.StatusCreated,
		RawStatus:   carrier.StatusCreated,
		Description: "Vận đơn hoàn hàng đã được tạo",
		OccurredAt:  now,
	}

	saved, alreadyExisted, err := s.repo.CreateShipment(ctx, sh, initEv)
	if err != nil {
		return nil, false, fmt.Errorf("persist return shipment: %w", err)
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

// GetShipment retrieves a shipment and its timeline events by order_id (defaulting to forward unless direction is provided) or tracking_code.
func (s *ShippingService) GetShipment(ctx context.Context, orderID, trackingCode string, direction ...string) (*store.Shipment, []store.ShipmentEvent, error) {
	orderID = strings.TrimSpace(orderID)
	trackingCode = strings.TrimSpace(trackingCode)
	if orderID == "" && trackingCode == "" {
		return nil, nil, fmt.Errorf("%w: order_id or tracking_code is required", ErrInvalidArgument)
	}

	var dirFilter string
	if len(direction) > 0 {
		dirFilter = strings.ToLower(strings.TrimSpace(direction[0]))
		if dirFilter != "" && dirFilter != store.DirectionForward && dirFilter != store.DirectionReturn {
			return nil, nil, fmt.Errorf("%w: invalid direction %q", ErrInvalidArgument, dirFilter)
		}
	}

	var (
		sh  *store.Shipment
		err error
	)
	if orderID != "" {
		sh, err = s.repo.GetShipmentByOrderIDAndDirection(ctx, orderID, store.NormalizeDirection(dirFilter))
	} else {
		sh, err = s.repo.GetShipmentByTrackingCode(ctx, trackingCode)
		if err == nil && sh != nil && dirFilter != "" && store.NormalizeDirection(sh.Direction) != dirFilter {
			return nil, nil, store.ErrNotFound
		}
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
	if strings.TrimSpace(in.TrackingCode) == "" && strings.TrimSpace(in.OrderID) == "" {
		return nil, "", fmt.Errorf("%w: tracking_code or order_id is required", ErrInvalidArgument)
	}

	internalStatus, ok := carrier.MapCarrierStatus(in.RawStatus)
	if !ok {
		return nil, "", fmt.Errorf("%w: %s", ErrUnknownStatus, in.RawStatus)
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
		CarrierCode:    strings.ToLower(strings.TrimSpace(in.CarrierCode)),
		TrackingCode:   strings.TrimSpace(in.TrackingCode),
		OrderID:        strings.TrimSpace(in.OrderID),
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
				ShipmentID:      sh.ID,
				OrderID:         sh.OrderID,
				ShopID:          sh.ShopID,
				CarrierCode:     sh.CarrierCode,
				TrackingCode:    sh.TrackingCode,
				Status:          internalStatus,
				RawStatus:       strings.TrimSpace(in.RawStatus),
				Description:     strings.TrimSpace(in.Description),
				OccurredAt:      occurredAt.Format(time.RFC3339),
				Direction:       store.NormalizeDirection(sh.Direction),
				ReturnRequestID: sh.ReturnRequestID,
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

// PollActiveShipments checks active non-terminal shipments for live/sandbox or pollable carriers (UC-SHIP04).
func (s *ShippingService) PollActiveShipments(ctx context.Context) error {
	active, err := s.repo.ListActiveShipments(ctx, 100)
	if err != nil {
		return err
	}
	for _, sh := range active {
		provider, ok := s.aggregator.Get(sh.CarrierCode)
		if !ok {
			continue
		}
		shouldPoll := !provider.IsMock()
		if p, ok := provider.(interface{ CanPoll() bool }); ok {
			shouldPoll = p.CanPoll()
		}
		if !shouldPoll {
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
		occurredAt := st.OccurredAt.UTC()
		if occurredAt.IsZero() {
			occurredAt = time.Now().UTC()
		}
		rawStatus := strings.TrimSpace(st.RawStatus)
		if rawStatus == "" {
			rawStatus = st.Status
		}
		eventID := fmt.Sprintf("poll:%s:%s:%d", sh.TrackingCode, st.Status, occurredAt.Unix())
		if _, _, err := s.HandleWebhook(ctx, WebhookInput{
			EventID:      eventID,
			CarrierCode:  sh.CarrierCode,
			TrackingCode: sh.TrackingCode,
			OrderID:      sh.OrderID,
			RawStatus:    rawStatus,
			Description:  st.Description,
			OccurredAt:   occurredAt,
		}); err != nil {
			s.logger.WarnContext(ctx, "apply polled shipment status failed",
				slog.String("tracking_code", sh.TrackingCode),
				slog.String("error", err.Error()),
			)
		}
	}
	return nil
}

func (s *ShippingService) enrichAddress(ctx context.Context, addr carrier.Address) carrier.Address {
	if w, err := s.repo.GetWard(ctx, addr.WardCode); err == nil && w != nil {
		if addr.GHNDistrictID == 0 {
			addr.GHNDistrictID = w.GHNDistrictID
		}
		if addr.GHNWardCode == "" {
			addr.GHNWardCode = w.GHNWardCode
		}
		if addr.WardName == "" {
			addr.WardName = w.Name
		}
		if addr.ProvinceCode == "" {
			addr.ProvinceCode = w.ProvinceCode
		}
	}
	if addr.WardCode != "" {
		if fb := address.LookupLegacyFallback(addr.WardCode); fb.Found {
			if addr.ProvinceCode == "" {
				addr.ProvinceCode = fb.ProvinceCode
			}
			if addr.ProvinceName == "" {
				addr.ProvinceName = fb.ProvinceName
			}
			if addr.WardName == "" {
				addr.WardName = fb.WardName
			}
			if addr.LegacyProvince == "" {
				addr.LegacyProvince = fb.LegacyProvince
			}
			if addr.LegacyDistrict == "" {
				addr.LegacyDistrict = fb.LegacyDistrict
			}
		}
	}
	if addr.ProvinceName == "" && addr.ProvinceCode != "" {
		addr.ProvinceName = address.ProvinceName(addr.ProvinceCode)
	}
	return addr
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
