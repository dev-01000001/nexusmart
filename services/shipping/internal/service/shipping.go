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

	// Enrich GHN district/ward codes from local ward table when available.
	if w, err := s.repo.GetWard(ctx, req.FromWardCode); err == nil && w != nil {
		req.FromDistrictID = w.GHNDistrictID
		req.FromGHNWardCode = w.GHNWardCode
		if req.FromProvinceCode == "" {
			req.FromProvinceCode = w.ProvinceCode
		}
	}
	if w, err := s.repo.GetWard(ctx, req.ToWardCode); err == nil && w != nil {
		req.ToDistrictID = w.GHNDistrictID
		req.ToGHNWardCode = w.GHNWardCode
		if req.ToProvinceCode == "" {
			req.ToProvinceCode = w.ProvinceCode
		}
	}

	quotes, isFallback := s.aggregator.CalculateFeeAll(ctx, req)
	if !isFallback && s.quoteCache != nil {
		s.quoteCache.Set(ctx, req, quotes)
	}
	return quotes, nil
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
