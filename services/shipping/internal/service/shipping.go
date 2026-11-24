package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nexusmart/core/pkg/common/secretbox"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/shipping/internal/address"
	"github.com/nexusmart/core/services/shipping/internal/cache"
	"github.com/nexusmart/core/services/shipping/internal/carrier"
	"github.com/nexusmart/core/services/shipping/internal/events"
	"github.com/nexusmart/core/services/shipping/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	defaultMaxRedelivery  = int32(3)
	defaultConfigCacheTTL = 30 * time.Second
	defaultWeightGrams    = int32(500)
	defaultLengthCM       = int32(20)
	defaultWidthCM        = int32(15)
	defaultHeightCM       = int32(10)
)

var (
	ErrInvalidArgument            = errors.New("invalid argument")
	ErrOutOfServiceArea           = errors.New("address or route outside service area")
	ErrNotCancellable             = errors.New("shipment cannot be cancelled after pickup")
	ErrUnknownCarrier             = errors.New("unknown carrier")
	ErrCarrierDisabled            = errors.New("carrier is disabled")
	ErrNoEnabledCarriers          = errors.New("no shipping carriers enabled")
	ErrCarrierAuthFailed          = errors.New("carrier authentication failed")
	ErrCarrierRejected            = errors.New("hãng vận chuyển từ chối tạo vận đơn")
	ErrCarrierUnavailable         = errors.New("carrier temporarily unavailable")
	ErrReturnShipmentFailed       = errors.New("vận đơn trả hàng hiện tại đang ở trạng thái giao thất bại, cần xử lý hoàn hàng hoặc huỷ trước khi tạo vận đơn trả hàng mới")
	ErrUnknownStatus              = errors.New("unsupported carrier status")
	ErrPermissionDenied           = errors.New("permission denied")
	ErrLegacyShipmentNoBuyer      = errors.New("legacy shipment has no recorded buyer_id")
	ErrRedeliveryNotAllowed       = errors.New("shipment is not in failed status for redelivery")
	ErrRedeliveryAlreadyRequested = errors.New("đã có yêu cầu giao lại cho lần giao thất bại hiện tại")
	ErrMaxRedeliveryExceeded      = errors.New("maximum redelivery attempts reached")
)

// OutboxNotifier wakes the outbox relay immediately after a webhook transaction commits.
type OutboxNotifier interface {
	Wake()
}

// ShopPickupResolver resolves a shop's pickup (province_code, ward_code) from UserService.
type ShopPickupResolver interface {
	GetShopPickupCodes(ctx context.Context, shopID string) (provinceCode string, wardCode string, err error)
}

// CarrierConfigView is the admin-facing view of a carrier config (token is never exposed).
type CarrierConfigView struct {
	CarrierCode     string
	Name            string
	Enabled         bool
	TokenConfigured bool
	FallbackRates   []carrier.FallbackRate
	UpdatedAt       time.Time
}

// AdminUpdateCarrierInput carries admin carrier configuration updates (UC-A12).
type AdminUpdateCarrierInput struct {
	CarrierCode   string
	Enabled       bool
	APIToken      string
	FallbackRates []carrier.FallbackRate
}

// ShippingService orchestrates address lookup, carrier quoting, shipment lifecycle, returns, redelivery, settings, and webhooks.
type ShippingService struct {
	repo          store.Repository
	aggregator    *carrier.Aggregator
	quoteCache    cache.QuoteCache
	outboxWake    OutboxNotifier
	shopPickup    ShopPickupResolver
	tokenCipher   *secretbox.Box
	maxRedelivery int32
	logger        *slog.Logger
	nowFn         func() time.Time

	cfgMu          sync.RWMutex
	cfgTTL         time.Duration
	cfgExpiresAt   time.Time
	cachedCarriers []store.CarrierConfig
	cachedRates    []store.FallbackRate
}

// SetShopPickupResolver configures the resolver used when CalculateFee receives shop_id without origin codes.
func (s *ShippingService) SetShopPickupResolver(r ShopPickupResolver) {
	s.shopPickup = r
}

// SetTokenCipher configures the AES-256-GCM cipher used to encrypt/decrypt carrier API tokens.
func (s *ShippingService) SetTokenCipher(c *secretbox.Box) {
	s.tokenCipher = c
}

// SetMaxRedelivery sets the maximum buyer redelivery attempts per shipment (default 3).
func (s *ShippingService) SetMaxRedelivery(n int32) {
	if n > 0 {
		s.maxRedelivery = n
	}
}

// SetConfigCacheTTL overrides the short TTL cache duration for DB carrier configs and fallback rates.
func (s *ShippingService) SetConfigCacheTTL(d time.Duration) {
	s.cfgMu.Lock()
	s.cfgTTL = d
	s.cfgExpiresAt = time.Time{}
	s.cfgMu.Unlock()
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
		repo:          repo,
		aggregator:    aggregator,
		quoteCache:    quoteCache,
		outboxWake:    outboxWake,
		maxRedelivery: defaultMaxRedelivery,
		cfgTTL:        defaultConfigCacheTTL,
		logger:        logger,
		nowFn:         func() time.Time { return time.Now().UTC() },
	}
}

// InitCarrierConfigs seeds initial encrypted env tokens when DB has none and syncs stored tokens onto adapters.
func (s *ShippingService) InitCarrierConfigs(ctx context.Context, initialTokens map[string]string) error {
	for code, tok := range initialTokens {
		tok = strings.TrimSpace(tok)
		if tok == "" || s.tokenCipher == nil {
			continue
		}
		enc, err := s.tokenCipher.Encrypt(tok)
		if err != nil {
			return fmt.Errorf("encrypt initial token for %s: %w", code, err)
		}
		if err := s.repo.SeedCarrierTokenIfEmpty(ctx, code, enc); err != nil {
			return fmt.Errorf("seed initial token for %s: %w", code, err)
		}
	}

	cfgs, err := s.repo.ListCarrierConfigs(ctx)
	if err != nil {
		return err
	}
	s.applyTokensFromConfigs(ctx, cfgs)
	s.invalidateConfigCache()
	return nil
}

func (s *ShippingService) invalidateConfigCache() {
	s.cfgMu.Lock()
	s.cfgExpiresAt = time.Time{}
	s.cfgMu.Unlock()
}

// ponytail: carrier tokens and enabled flags refresh across replicas via this in-memory TTL cache
// (default 30s), giving bounded eventual consistency without adding pub/sub invalidation.
// Upgrade path: broadcast config-changed notifications over NATS/Redis if instant multi-replica rotation is needed.
func (s *ShippingService) loadCachedConfigs(ctx context.Context) ([]store.CarrierConfig, []store.FallbackRate, error) {
	now := s.nowFn()
	s.cfgMu.RLock()
	if !s.cfgExpiresAt.IsZero() && now.Before(s.cfgExpiresAt) {
		cfgs := append([]store.CarrierConfig(nil), s.cachedCarriers...)
		rates := append([]store.FallbackRate(nil), s.cachedRates...)
		s.cfgMu.RUnlock()
		return cfgs, rates, nil
	}
	s.cfgMu.RUnlock()

	cfgs, err := s.repo.ListCarrierConfigs(ctx)
	if err != nil {
		return nil, nil, err
	}
	rates, err := s.repo.ListFallbackRates(ctx, "")
	if err != nil {
		return nil, nil, err
	}

	s.applyTokensFromConfigs(ctx, cfgs)

	s.cfgMu.Lock()
	s.cachedCarriers = append([]store.CarrierConfig(nil), cfgs...)
	s.cachedRates = append([]store.FallbackRate(nil), rates...)
	if s.cfgTTL > 0 {
		s.cfgExpiresAt = now.Add(s.cfgTTL)
	}
	s.cfgMu.Unlock()
	return cfgs, rates, nil
}

func (s *ShippingService) applyTokensFromConfigs(ctx context.Context, cfgs []store.CarrierConfig) {
	if s.tokenCipher == nil {
		return
	}
	for _, cfg := range cfgs {
		if len(cfg.EncryptedToken) == 0 {
			continue
		}
		plain, decErr := s.tokenCipher.Decrypt(cfg.EncryptedToken)
		if decErr != nil {
			s.logger.WarnContext(ctx, "stored carrier token could not be decrypted during config refresh",
				slog.String("carrier", cfg.CarrierCode),
			)
			continue
		}
		if provider, ok := s.aggregator.Get(cfg.CarrierCode); ok {
			provider.SetToken(plain)
		}
	}
}

// platformEnabledCarriers returns registered carriers that are enabled at the platform level.
// If carrier_configs has no rows (e.g. minimal test repo), all registered aggregator carriers are considered enabled.
func (s *ShippingService) platformEnabledCarriers(ctx context.Context) ([]string, map[string]bool, error) {
	cfgs, _, err := s.loadCachedConfigs(ctx)
	if err != nil {
		return nil, nil, err
	}
	registered := s.aggregator.ListCodes()
	if len(cfgs) == 0 {
		enabledMap := make(map[string]bool, len(registered))
		for _, c := range registered {
			enabledMap[c] = true
		}
		return registered, enabledMap, nil
	}

	cfgByCode := make(map[string]store.CarrierConfig, len(cfgs))
	for _, c := range cfgs {
		cfgByCode[strings.ToLower(c.CarrierCode)] = c
	}

	enabledList := make([]string, 0, len(registered))
	enabledMap := make(map[string]bool, len(registered))
	for _, code := range registered {
		c, ok := cfgByCode[code]
		if !ok || c.Enabled {
			enabledList = append(enabledList, code)
			enabledMap[code] = true
		}
	}
	return enabledList, enabledMap, nil
}

// selectFallbackRates returns DB fallback rates for the given carriers, preferring the most recently updated rate per region.
func (s *ShippingService) selectFallbackRates(ctx context.Context, carrierCodes []string) []carrier.FallbackRate {
	_, allRates, err := s.loadCachedConfigs(ctx)
	if err != nil || len(allRates) == 0 {
		return carrier.DefaultFallbackRates()
	}

	allowed := make(map[string]bool, len(carrierCodes))
	for _, c := range carrierCodes {
		allowed[strings.ToLower(strings.TrimSpace(c))] = true
	}

	bestByRegion := make(map[string]store.FallbackRate, 3)
	// First load platform-wide 'fallback' rows as baseline.
	for _, r := range allRates {
		if strings.EqualFold(r.CarrierCode, carrier.CodeFallback) {
			bestByRegion[r.Region] = r
		}
	}
	// Overlay carrier-specific rows for queried carriers (newest updated_at wins so admin updates take effect immediately).
	hasCarrierOverride := make(map[string]bool, 3)
	for _, r := range allRates {
		if len(allowed) > 0 && !allowed[strings.ToLower(r.CarrierCode)] {
			continue
		}
		if strings.EqualFold(r.CarrierCode, carrier.CodeFallback) {
			continue
		}
		prev, exists := bestByRegion[r.Region]
		if !exists || !hasCarrierOverride[r.Region] || r.UpdatedAt.After(prev.UpdatedAt) {
			bestByRegion[r.Region] = r
			hasCarrierOverride[r.Region] = true
		}
	}

	defaults := carrier.DefaultFallbackRates()
	out := make([]carrier.FallbackRate, 0, len(defaults))
	for _, d := range defaults {
		if r, ok := bestByRegion[d.Region]; ok {
			out = append(out, carrier.FallbackRate{
				Region:             r.Region,
				BaseFeeVND:         r.BaseFeeVND,
				BaseWeightGrams:    r.BaseWeightGrams,
				ExtraFeeVNDPer500g: r.ExtraFeeVNDPer500g,
			})
		} else {
			out = append(out, d)
		}
	}
	return out
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

// CalculateFee validates input and service area, applies shop default weight (when configured) and
// platform ∩ shop enabled carrier filtering (UC-SHIP06/UC-A12), resolves shop pickup when origin is omitted,
// checks Redis quote cache keyed by active carriers + real origin/destination, then fans out to eligible carriers.
// Returned quotes always carry raw pre-freeship carrier fees (Ship); shop and platform freeship discounts are
// calculated by order/promotion from GetShopShippingSettings.
func (s *ShippingService) CalculateFee(ctx context.Context, req carrier.FeeRequest) ([]carrier.Quote, error) {
	req.ShopID = strings.TrimSpace(req.ShopID)
	req.FromProvinceCode = strings.TrimSpace(req.FromProvinceCode)
	req.FromWardCode = strings.TrimSpace(req.FromWardCode)
	req.ToProvinceCode = strings.TrimSpace(req.ToProvinceCode)
	req.ToWardCode = strings.TrimSpace(req.ToWardCode)

	if req.WeightGrams < 0 {
		return nil, fmt.Errorf("%w: weight_grams must be > 0", ErrInvalidArgument)
	}
	if req.CODAmountVND < 0 {
		return nil, fmt.Errorf("%w: cod_amount_vnd must be >= 0", ErrInvalidArgument)
	}

	// Load shop shipping settings once if shop_id is provided (UC-SHIP06).
	var shopSettings *store.ShopShippingSettings
	if req.ShopID != "" {
		st, err := s.repo.GetShopShippingSettings(ctx, req.ShopID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("load shop shipping settings: %w", err)
		}
		shopSettings = st
	}

	// Apply configured shop default_weight_grams when weight_grams is omitted (0);
	// otherwise reject <= 0 to keep SHP-B7 validation intact.
	if req.WeightGrams == 0 {
		if shopSettings != nil && shopSettings.DefaultWeightGrams > 0 {
			req.WeightGrams = shopSettings.DefaultWeightGrams
		} else {
			return nil, fmt.Errorf("%w: weight_grams must be > 0", ErrInvalidArgument)
		}
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

	// Determine eligible carriers: platform-enabled ∩ shop-enabled (UC-SHIP06, UC-A12).
	platformEnabled, platformMap, err := s.platformEnabledCarriers(ctx)
	if err != nil {
		return nil, fmt.Errorf("load platform carrier configs: %w", err)
	}
	eligible := platformEnabled
	if shopSettings != nil {
		filtered := make([]string, 0, len(shopSettings.EnabledCarrierCodes))
		seen := make(map[string]bool, len(shopSettings.EnabledCarrierCodes))
		for _, code := range shopSettings.EnabledCarrierCodes {
			norm := strings.ToLower(strings.TrimSpace(code))
			if norm != "" && platformMap[norm] && !seen[norm] {
				filtered = append(filtered, norm)
				seen[norm] = true
			}
		}
		eligible = filtered
	}
	if len(eligible) == 0 {
		return nil, fmt.Errorf("%w: no shipping carriers available for this shop", ErrNoEnabledCarriers)
	}
	req.AllowedCarriers = eligible

	// Check cache ONLY after resolving origin/destination and eligible carrier set.
	if s.quoteCache != nil {
		if cached, ok := s.quoteCache.Get(ctx, req); ok {
			return cached, nil
		}
	}

	fallbackRates := s.selectFallbackRates(ctx, eligible)
	quotes, isFallback, err := s.aggregator.CalculateFeeForCarriers(ctx, req, eligible, fallbackRates)
	if err != nil {
		if errors.Is(err, carrier.ErrRouteOutOfServiceArea) {
			return nil, fmt.Errorf("%w: %w", ErrOutOfServiceArea, err)
		}
		if errors.Is(err, carrier.ErrNoEnabledCarriers) {
			return nil, fmt.Errorf("%w: %w", ErrNoEnabledCarriers, err)
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

// CreateShipmentInput represents a forward shipment creation request.
type CreateShipmentInput struct {
	OrderID      string
	ShopID       string
	BuyerID      string
	CarrierCode  string
	ServiceCode  string
	CODAmountVND int64
	WeightGrams  int32
	FromAddress  carrier.Address
	ToAddress    carrier.Address
	ItemsSummary string
}

// CreateShipment creates a FORWARD carrier shipment idempotently keyed by active (order_id, FORWARD) (UC-SHIP03).
func (s *ShippingService) CreateShipment(ctx context.Context, in CreateShipmentInput) (*store.Shipment, bool, error) {
	in.OrderID = strings.TrimSpace(in.OrderID)
	in.ShopID = strings.TrimSpace(in.ShopID)
	in.BuyerID = strings.TrimSpace(in.BuyerID)
	if in.OrderID == "" {
		return nil, false, fmt.Errorf("%w: order_id is required", ErrInvalidArgument)
	}
	if in.ShopID == "" {
		return nil, false, fmt.Errorf("%w: shop_id is required", ErrInvalidArgument)
	}
	if in.WeightGrams <= 0 {
		in.WeightGrams = s.resolveDefaultWeight(ctx, in.ShopID)
	}

	// Fast-path idempotency check only matches active (non-cancelled) FORWARD shipment.
	if existing, err := s.repo.GetActiveShipmentByOrderID(ctx, in.OrderID, store.DirectionForward); err == nil && existing != nil {
		return existing, true, nil
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, false, fmt.Errorf("check existing shipment: %w", err)
	}

	// Serialize check -> carrier create -> DB insert per (order_id, FORWARD) across replicas
	// without holding a DB transaction during external carrier calls.
	unlock, err := s.repo.AcquireOrderDirectionLock(ctx, in.OrderID, store.DirectionForward)
	if err != nil {
		return nil, false, fmt.Errorf("%w: lock forward shipment creation: %w", ErrCarrierUnavailable, err)
	}
	defer unlock()

	// Re-check under advisory lock so a concurrent caller returns the shipment created by the first caller.
	if existing, err := s.repo.GetActiveShipmentByOrderID(ctx, in.OrderID, store.DirectionForward); err == nil && existing != nil {
		return existing, true, nil
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, false, fmt.Errorf("recheck existing shipment: %w", err)
	}

	// Refresh carrier tokens across replicas when cache TTL elapses.
	_, _, _ = s.loadCachedConfigs(ctx)

	// Per UC-A12, disabling a carrier only applies to NEW orders (filtered in CalculateFee).
	// Existing orders that already selected a carrier MUST still be able to create their FORWARD shipment.
	var provider carrier.Carrier
	reqCarrier := strings.ToLower(strings.TrimSpace(in.CarrierCode))
	if reqCarrier != "" && reqCarrier != carrier.CodeFallback {
		var ok bool
		provider, ok = s.aggregator.Get(reqCarrier)
		if !ok {
			return nil, false, fmt.Errorf("%w: %s", ErrUnknownCarrier, reqCarrier)
		}
	} else {
		provider, err = s.defaultEnabledCarrier(ctx)
		if err != nil {
			return nil, false, err
		}
	}

	inactiveCount, err := s.repo.CountInactiveShipmentsByOrder(ctx, in.OrderID, store.DirectionForward)
	if err != nil {
		return nil, false, fmt.Errorf("count inactive forward shipments: %w", err)
	}
	clientOrderCode := in.OrderID
	if inactiveCount > 0 {
		clientOrderCode = fmt.Sprintf("%s-R%d", in.OrderID, inactiveCount)
	}

	s.enrichGHNAddress(ctx, &in.FromAddress)
	s.enrichGHNAddress(ctx, &in.ToAddress)

	res, err := provider.CreateShipment(ctx, carrier.ShipmentRequest{
		OrderID:         in.OrderID,
		ClientOrderCode: clientOrderCode,
		ShopID:          in.ShopID,
		ServiceCode:     in.ServiceCode,
		CODAmountVND:    in.CODAmountVND,
		WeightGrams:     in.WeightGrams,
		FromAddress:     in.FromAddress,
		ToAddress:       in.ToAddress,
		ItemsSummary:    in.ItemsSummary,
	})
	if err != nil {
		return nil, false, s.classifyCarrierCreateError(ctx, provider.Code(), err)
	}

	now := s.nowFn()
	sh := &store.Shipment{
		ID:               uuid.NewString(),
		OrderID:          in.OrderID,
		ShopID:           in.ShopID,
		BuyerID:          in.BuyerID,
		Direction:        store.DirectionForward,
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
		Description: carrier.StatusDescriptionVI(carrier.StatusCreated),
		OccurredAt:  now.Truncate(time.Second),
	}

	saved, alreadyExisted, err := s.repo.CreateShipment(ctx, sh, initEv)
	if err != nil {
		return nil, false, fmt.Errorf("persist shipment: %w", err)
	}
	return saved, alreadyExisted, nil
}

// CreateReturnShipmentInput represents a RETURN shipment creation request (UC-SHIP08, SHP-B4).
type CreateReturnShipmentInput struct {
	OrderID         string
	ReturnRequestID string
	ShopID          string
	BuyerID         string
	CarrierCode     string
	ServiceCode     string
	WeightGrams     int32
	FromAddress     carrier.Address // Buyer pickup address
	ToAddress       carrier.Address // Shop warehouse return address
	ItemsSummary    string
}

// CreateReturnShipment creates a RETURN shipment idempotently keyed by active (order_id, RETURN).
// Fee is calculated via the multi-carrier aggregator using platform-enabled carriers and DB fallback rates.
func (s *ShippingService) CreateReturnShipment(ctx context.Context, in CreateReturnShipmentInput) (*store.Shipment, bool, error) {
	in.OrderID = strings.TrimSpace(in.OrderID)
	in.ReturnRequestID = strings.TrimSpace(in.ReturnRequestID)
	in.ShopID = strings.TrimSpace(in.ShopID)
	in.BuyerID = strings.TrimSpace(in.BuyerID)
	in.CarrierCode = strings.ToLower(strings.TrimSpace(in.CarrierCode))
	in.ServiceCode = strings.TrimSpace(in.ServiceCode)

	if in.OrderID == "" {
		return nil, false, fmt.Errorf("%w: order_id is required", ErrInvalidArgument)
	}
	if in.ReturnRequestID == "" {
		return nil, false, fmt.Errorf("%w: return_request_id is required", ErrInvalidArgument)
	}
	if in.ShopID == "" {
		return nil, false, fmt.Errorf("%w: shop_id is required", ErrInvalidArgument)
	}
	if in.WeightGrams <= 0 {
		in.WeightGrams = s.resolveDefaultWeight(ctx, in.ShopID)
	}

	// Fast-path check on active (order_id, RETURN): failed requires intervention; other active states replay idempotently;
	// cancelled or returned RETURN shipments are inactive and allow creating a new RETURN shipment.
	if existing, err := s.repo.GetActiveShipmentByOrderID(ctx, in.OrderID, store.DirectionReturn); err == nil && existing != nil {
		if existing.Status == carrier.StatusFailed {
			return nil, false, ErrReturnShipmentFailed
		}
		return existing, true, nil
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, false, fmt.Errorf("check existing return shipment: %w", err)
	}

	// Serialize check -> carrier create -> DB insert per (order_id, RETURN) across replicas
	// without holding a DB transaction during external carrier calls.
	unlock, err := s.repo.AcquireOrderDirectionLock(ctx, in.OrderID, store.DirectionReturn)
	if err != nil {
		return nil, false, fmt.Errorf("%w: lock return shipment creation: %w", ErrCarrierUnavailable, err)
	}
	defer unlock()

	// Re-check under advisory lock so a concurrent caller returns the shipment created by the first caller.
	if existing, err := s.repo.GetActiveShipmentByOrderID(ctx, in.OrderID, store.DirectionReturn); err == nil && existing != nil {
		if existing.Status == carrier.StatusFailed {
			return nil, false, ErrReturnShipmentFailed
		}
		return existing, true, nil
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, false, fmt.Errorf("recheck existing return shipment: %w", err)
	}

	s.enrichGHNAddress(ctx, &in.FromAddress)
	s.enrichGHNAddress(ctx, &in.ToAddress)

	platformEnabled, platformMap, err := s.platformEnabledCarriers(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("load platform carrier configs: %w", err)
	}

	var quoteCarriers []string
	if in.CarrierCode != "" && in.CarrierCode != carrier.CodeFallback {
		if _, registered := s.aggregator.Get(in.CarrierCode); !registered {
			return nil, false, fmt.Errorf("%w: %s", ErrUnknownCarrier, in.CarrierCode)
		}
		if !platformMap[in.CarrierCode] {
			return nil, false, fmt.Errorf("%w: hãng %s hiện đang tắt trên toàn sàn", ErrCarrierDisabled, in.CarrierCode)
		}
		quoteCarriers = []string{in.CarrierCode}
	} else {
		if len(platformEnabled) == 0 {
			return nil, false, fmt.Errorf("%w: hiện không có hãng vận chuyển nào đang bật", ErrCarrierDisabled)
		}
		quoteCarriers = platformEnabled
	}

	feeReq := carrier.FeeRequest{
		ShopID:           in.ShopID,
		FromProvinceCode: in.FromAddress.ProvinceCode,
		FromWardCode:     in.FromAddress.WardCode,
		FromDistrictID:   in.FromAddress.GHNDistrictID,
		FromGHNWardCode:  in.FromAddress.GHNWardCode,
		ToProvinceCode:   in.ToAddress.ProvinceCode,
		ToWardCode:       in.ToAddress.WardCode,
		ToDistrictID:     in.ToAddress.GHNDistrictID,
		ToGHNWardCode:    in.ToAddress.GHNWardCode,
		WeightGrams:      in.WeightGrams,
		CODAmountVND:     0,
		AllowedCarriers:  quoteCarriers,
	}
	fallbackRates := s.selectFallbackRates(ctx, quoteCarriers)
	quotes, _, quoteErr := s.aggregator.CalculateFeeForCarriers(ctx, feeReq, quoteCarriers, fallbackRates)
	if quoteErr != nil {
		if errors.Is(quoteErr, carrier.ErrRouteOutOfServiceArea) {
			return nil, false, fmt.Errorf("%w: hãng vận chuyển không phục vụ tuyến địa chỉ trả hàng này", ErrCarrierRejected)
		}
		return nil, false, quoteErr
	}

	type returnCandidate struct {
		carrierCode string
		serviceCode string
		feeVND      int64
	}
	var candidates []returnCandidate
	seenCandidate := make(map[string]bool)

	if in.CarrierCode != "" && in.CarrierCode != carrier.CodeFallback {
		var matchedFee int64
		chosenSvc := in.ServiceCode
		for _, q := range quotes {
			if strings.EqualFold(q.CarrierCode, in.CarrierCode) &&
				(chosenSvc == "" || strings.EqualFold(q.ServiceCode, chosenSvc)) {
				matchedFee = q.FeeVND
				if chosenSvc == "" {
					chosenSvc = q.ServiceCode
				}
				break
			}
		}
		if matchedFee == 0 && len(quotes) > 0 {
			matchedFee = quotes[0].FeeVND
		}
		candidates = append(candidates, returnCandidate{
			carrierCode: in.CarrierCode,
			serviceCode: chosenSvc,
			feeVND:      matchedFee,
		})
	} else {
		var fallbackFee int64
		for _, q := range quotes {
			if q.CarrierCode == carrier.CodeFallback {
				fallbackFee = q.FeeVND
				continue
			}
			code := strings.ToLower(strings.TrimSpace(q.CarrierCode))
			if code == "" || seenCandidate[code] || !platformMap[code] {
				continue
			}
			svcCode := in.ServiceCode
			if svcCode == "" {
				svcCode = q.ServiceCode
			}
			seenCandidate[code] = true
			candidates = append(candidates, returnCandidate{
				carrierCode: code,
				serviceCode: svcCode,
				feeVND:      q.FeeVND,
			})
		}
		for _, code := range platformEnabled {
			if seenCandidate[code] {
				continue
			}
			seenCandidate[code] = true
			candidates = append(candidates, returnCandidate{
				carrierCode: code,
				serviceCode: in.ServiceCode,
				feeVND:      fallbackFee,
			})
		}
	}

	if len(candidates) == 0 {
		return nil, false, fmt.Errorf("%w: hiện không có hãng vận chuyển nào đang bật", ErrCarrierDisabled)
	}

	inactiveCount, err := s.repo.CountInactiveShipmentsByOrder(ctx, in.OrderID, store.DirectionReturn)
	if err != nil {
		return nil, false, fmt.Errorf("count inactive return shipments: %w", err)
	}
	clientOrderCode := fmt.Sprintf("RET-%s", in.ReturnRequestID)
	if inactiveCount > 0 {
		clientOrderCode = fmt.Sprintf("RET-%s-R%d", in.ReturnRequestID, inactiveCount)
	}

	var (
		res          *carrier.ShipmentResult
		quotedFeeVND int64
		lastErr      error
		lastCarrier  string
	)
	for _, cand := range candidates {
		provider, ok := s.aggregator.Get(cand.carrierCode)
		if !ok {
			continue
		}
		created, createErr := provider.CreateShipment(ctx, carrier.ShipmentRequest{
			OrderID:         in.OrderID,
			ClientOrderCode: clientOrderCode,
			ShopID:          in.ShopID,
			ServiceCode:     cand.serviceCode,
			CODAmountVND:    0,
			WeightGrams:     in.WeightGrams,
			FromAddress:     in.FromAddress,
			ToAddress:       in.ToAddress,
			ItemsSummary:    in.ItemsSummary,
		})
		if createErr == nil {
			res = created
			quotedFeeVND = cand.feeVND
			break
		}
		lastErr = createErr
		lastCarrier = provider.Code()
		if (!carrier.IsNonRetryable(createErr) && !carrier.IsCircuitBreakerOpen(createErr)) || len(candidates) == 1 {
			break
		}
	}
	if res == nil {
		return nil, false, s.classifyCarrierCreateError(ctx, lastCarrier, lastErr)
	}

	feeVND := quotedFeeVND
	if feeVND <= 0 {
		feeVND = res.FeeVND
	}
	now := s.nowFn()
	sh := &store.Shipment{
		ID:               uuid.NewString(),
		OrderID:          in.OrderID,
		ShopID:           in.ShopID,
		BuyerID:          in.BuyerID,
		Direction:        store.DirectionReturn,
		ReturnRequestID:  in.ReturnRequestID,
		CarrierCode:      res.CarrierCode,
		ServiceCode:      res.ServiceCode,
		TrackingCode:     res.TrackingCode,
		LabelURL:         res.LabelURL,
		Status:           carrier.StatusCreated,
		FeeVND:           feeVND,
		CODAmountVND:     0,
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
		Description: "Vận đơn trả hàng đã được tạo",
		OccurredAt:  now.Truncate(time.Second),
	}

	saved, alreadyExisted, err := s.repo.CreateShipment(ctx, sh, initEv)
	if err != nil {
		return nil, false, fmt.Errorf("persist return shipment: %w", err)
	}
	if alreadyExisted && saved.Status == carrier.StatusFailed {
		return nil, false, ErrReturnShipmentFailed
	}
	return saved, alreadyExisted, nil
}

// CancelShipment cancels a shipment for (orderID, direction) if not yet picked up (created|picking).
// Carrier errors return ErrCarrierUnavailable (gRPC Unavailable); concurrent pickup races return ErrCannotCancel (409).
func (s *ShippingService) CancelShipment(ctx context.Context, orderID, direction, reason string) (*store.Shipment, error) {
	orderID = strings.TrimSpace(orderID)
	if orderID == "" {
		return nil, fmt.Errorf("%w: order_id is required", ErrInvalidArgument)
	}
	dir, err := parseDirection(direction)
	if err != nil {
		return nil, err
	}

	sh, err := s.repo.GetShipmentByOrderID(ctx, orderID, dir)
	if err != nil {
		return nil, err
	}
	if sh.Status == carrier.StatusCancelled {
		return sh, nil
	}
	if !carrier.IsCancellableStatus(sh.Status) {
		return nil, fmt.Errorf("%w: current status is %s", ErrNotCancellable, sh.Status)
	}

	_, _, _ = s.loadCachedConfigs(ctx)

	if provider, ok := s.aggregator.Get(sh.CarrierCode); ok {
		if err := provider.CancelShipment(ctx, sh.TrackingCode); err != nil {
			if !errors.Is(err, carrier.ErrInvalidCarrierToken) && carrier.IsNonRetryable(err) {
				return nil, fmt.Errorf("%w: hãng vận chuyển từ chối huỷ vận đơn này (kiện hàng có thể đã được lấy)", ErrNotCancellable)
			}
			return nil, fmt.Errorf("%w: carrier cancel shipment: %w", ErrCarrierUnavailable, err)
		}
	}

	// Lock row FOR UPDATE and re-check status inside DB tx before writing cancelled.
	return s.repo.CancelShipmentByID(ctx, sh.ID, reason)
}

// RequestRedelivery lets the buyer who owns a failed FORWARD shipment request redelivery up to SHIPPING_MAX_REDELIVERY times (§1.3).
func (s *ShippingService) RequestRedelivery(ctx context.Context, buyerID, orderID, preferredDate, note string) (*store.Shipment, error) {
	buyerID = strings.TrimSpace(buyerID)
	orderID = strings.TrimSpace(orderID)
	preferredDate = strings.TrimSpace(preferredDate)
	note = strings.TrimSpace(note)

	if buyerID == "" {
		return nil, fmt.Errorf("%w: buyer identity required", ErrPermissionDenied)
	}
	if orderID == "" {
		return nil, fmt.Errorf("%w: order_id is required", ErrInvalidArgument)
	}
	if preferredDate != "" {
		if _, err := time.Parse("2006-01-02", preferredDate); err != nil {
			return nil, fmt.Errorf("%w: preferred_date must be YYYY-MM-DD", ErrInvalidArgument)
		}
	}
	if len([]rune(note)) > 500 {
		return nil, fmt.Errorf("%w: note must be <= 500 characters", ErrInvalidArgument)
	}

	sh, err := s.repo.GetActiveShipmentByOrderID(ctx, orderID, store.DirectionForward)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(sh.BuyerID) == "" {
		return nil, ErrLegacyShipmentNoBuyer
	}
	if sh.BuyerID != buyerID {
		return nil, fmt.Errorf("%w: shipment belongs to another buyer", ErrPermissionDenied)
	}
	if sh.Status != carrier.StatusFailed {
		return nil, fmt.Errorf("%w: current status is %s", ErrRedeliveryNotAllowed, sh.Status)
	}
	if sh.RedeliveryCount >= s.maxRedelivery {
		return nil, fmt.Errorf("%w: limit is %d", ErrMaxRedeliveryExceeded, s.maxRedelivery)
	}

	// Pre-check before calling carrier API: only one redelivery request is permitted per failed delivery event.
	// Compare by CreatedAt (matching PGStore ORDER BY created_at DESC) so carrier clock skew cannot invert order.
	if evList, evErr := s.repo.ListShipmentEvents(ctx, sh.ID); evErr == nil {
		var (
			latestFailedRaw string
			latestCreated   time.Time
			foundFailed     bool
		)
		for _, ev := range evList {
			if ev.Status != carrier.StatusFailed {
				continue
			}
			if !foundFailed || !ev.CreatedAt.Before(latestCreated) {
				latestFailedRaw = ev.RawStatus
				latestCreated = ev.CreatedAt
				foundFailed = true
			}
		}
		if (foundFailed && latestFailedRaw == "redelivery_requested") || (!foundFailed && sh.RedeliveryCount > 0) {
			return nil, ErrRedeliveryAlreadyRequested
		}
	}

	_, _, _ = s.loadCachedConfigs(ctx)

	if provider, ok := s.aggregator.Get(sh.CarrierCode); ok {
		if err := provider.RequestRedelivery(ctx, sh.TrackingCode, preferredDate, note); err != nil {
			if !errors.Is(err, carrier.ErrInvalidCarrierToken) && carrier.IsNonRetryable(err) {
				return nil, fmt.Errorf("%w: hãng vận chuyển không cho phép yêu cầu giao lại cho vận đơn này", ErrRedeliveryNotAllowed)
			}
			return nil, fmt.Errorf("%w: carrier redelivery request: %w", ErrCarrierUnavailable, err)
		}
	}

	updated, err := s.repo.RecordRedelivery(ctx, sh.ID, buyerID, s.maxRedelivery, preferredDate, note)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrLegacyNoBuyer):
			return nil, ErrLegacyShipmentNoBuyer
		case errors.Is(err, store.ErrBuyerMismatch):
			return nil, ErrPermissionDenied
		case errors.Is(err, store.ErrRedeliveryNotAllowed):
			return nil, ErrRedeliveryNotAllowed
		case errors.Is(err, store.ErrRedeliveryAlreadyRequested):
			return nil, ErrRedeliveryAlreadyRequested
		case errors.Is(err, store.ErrMaxRedeliveryExceeded):
			return nil, ErrMaxRedeliveryExceeded
		default:
			return nil, err
		}
	}
	return updated, nil
}

// GetShipment retrieves a shipment and its timeline events by order_id (+ direction) or tracking_code.
func (s *ShippingService) GetShipment(ctx context.Context, orderID, trackingCode, direction string) (*store.Shipment, []store.ShipmentEvent, error) {
	orderID = strings.TrimSpace(orderID)
	trackingCode = strings.TrimSpace(trackingCode)
	if orderID == "" && trackingCode == "" {
		return nil, nil, fmt.Errorf("%w: order_id or tracking_code is required", ErrInvalidArgument)
	}

	var (
		sh  *store.Shipment
		err error
	)
	if trackingCode != "" {
		sh, err = s.repo.GetShipmentByTrackingCode(ctx, trackingCode)
	} else {
		dir, dirErr := parseDirection(direction)
		if dirErr != nil {
			return nil, nil, dirErr
		}
		sh, err = s.repo.GetShipmentByOrderID(ctx, orderID, dir)
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

// GetShopShippingSettings returns a shop's persisted shipping settings or platform defaults when unconfigured (UC-SHIP06).
func (s *ShippingService) GetShopShippingSettings(ctx context.Context, shopID string) (*store.ShopShippingSettings, error) {
	shopID = strings.TrimSpace(shopID)
	if shopID == "" {
		return nil, fmt.Errorf("%w: shop_id is required", ErrInvalidArgument)
	}
	st, err := s.repo.GetShopShippingSettings(ctx, shopID)
	if err == nil && st != nil {
		return st, nil
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}

	enabled, _, pErr := s.platformEnabledCarriers(ctx)
	if pErr != nil {
		return nil, pErr
	}
	return &store.ShopShippingSettings{
		ShopID:              shopID,
		EnabledCarrierCodes: enabled,
		DefaultWeightGrams:  defaultWeightGrams,
		DefaultLengthCM:     defaultLengthCM,
		DefaultWidthCM:      defaultWidthCM,
		DefaultHeightCM:     defaultHeightCM,
		FreeShipEnabled:     false,
		FreeShipMinOrderVND: 0,
		FreeShipMaxVND:      0,
	}, nil
}

// UpdateShopShippingSettings validates and persists a shop's shipping configuration (UC-SHIP06).
// Disabling all carriers or configuring invalid freeship amounts is rejected with ErrInvalidArgument (HTTP 400).
func (s *ShippingService) UpdateShopShippingSettings(ctx context.Context, in *store.ShopShippingSettings) (*store.ShopShippingSettings, error) {
	if in == nil || strings.TrimSpace(in.ShopID) == "" {
		return nil, fmt.Errorf("%w: shop_id is required", ErrInvalidArgument)
	}
	if in.DefaultWeightGrams < 0 || in.DefaultLengthCM < 0 || in.DefaultWidthCM < 0 || in.DefaultHeightCM < 0 {
		return nil, fmt.Errorf("%w: parcel weight and dimensions must be >= 0", ErrInvalidArgument)
	}
	if in.FreeShipMinOrderVND < 0 || in.FreeShipMaxVND < 0 {
		return nil, fmt.Errorf("%w: free_ship_min_order_vnd and free_ship_max_vnd must be >= 0", ErrInvalidArgument)
	}
	if in.FreeShipEnabled && in.FreeShipMaxVND <= 0 {
		return nil, fmt.Errorf("%w: free_ship_max_vnd must be > 0 when free_ship_enabled is true", ErrInvalidArgument)
	}

	cleaned := make([]string, 0, len(in.EnabledCarrierCodes))
	seen := make(map[string]bool, len(in.EnabledCarrierCodes))
	for _, raw := range in.EnabledCarrierCodes {
		code := strings.ToLower(strings.TrimSpace(raw))
		if code == "" || seen[code] {
			continue
		}
		if _, ok := s.aggregator.Get(code); !ok {
			return nil, fmt.Errorf("%w: %s", ErrUnknownCarrier, code)
		}
		seen[code] = true
		cleaned = append(cleaned, code)
	}
	if len(cleaned) == 0 {
		return nil, fmt.Errorf("%w: at least one shipping carrier must be enabled", ErrInvalidArgument)
	}
	_, platformMap, err := s.platformEnabledCarriers(ctx)
	if err != nil {
		return nil, fmt.Errorf("load platform carrier configs: %w", err)
	}
	hasPlatformEnabled := false
	for _, code := range cleaned {
		if platformMap[code] {
			hasPlatformEnabled = true
			break
		}
	}
	if !hasPlatformEnabled {
		return nil, fmt.Errorf("%w: cần chọn ít nhất một hãng vận chuyển đang hoạt động trên sàn", ErrInvalidArgument)
	}

	shopID := strings.TrimSpace(in.ShopID)
	weight := in.DefaultWeightGrams
	length := in.DefaultLengthCM
	width := in.DefaultWidthCM
	height := in.DefaultHeightCM
	if weight == 0 || length == 0 || width == 0 || height == 0 {
		var existing *store.ShopShippingSettings
		if st, err := s.repo.GetShopShippingSettings(ctx, shopID); err == nil {
			existing = st
		} else if !errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("load existing shop shipping settings: %w", err)
		}
		if weight == 0 {
			if existing != nil && existing.DefaultWeightGrams > 0 {
				weight = existing.DefaultWeightGrams
			} else {
				weight = defaultWeightGrams
			}
		}
		if length == 0 {
			if existing != nil && existing.DefaultLengthCM > 0 {
				length = existing.DefaultLengthCM
			} else {
				length = defaultLengthCM
			}
		}
		if width == 0 {
			if existing != nil && existing.DefaultWidthCM > 0 {
				width = existing.DefaultWidthCM
			} else {
				width = defaultWidthCM
			}
		}
		if height == 0 {
			if existing != nil && existing.DefaultHeightCM > 0 {
				height = existing.DefaultHeightCM
			} else {
				height = defaultHeightCM
			}
		}
	}

	return s.repo.UpsertShopShippingSettings(ctx, &store.ShopShippingSettings{
		ShopID:              shopID,
		EnabledCarrierCodes: cleaned,
		DefaultWeightGrams:  weight,
		DefaultLengthCM:     length,
		DefaultWidthCM:      width,
		DefaultHeightCM:     height,
		FreeShipEnabled:     in.FreeShipEnabled,
		FreeShipMinOrderVND: in.FreeShipMinOrderVND,
		FreeShipMaxVND:      in.FreeShipMaxVND,
	})
}

// AdminListCarriers returns all platform carrier configs with token_configured flag and DB fallback rates (UC-A12).
// Encrypted or plaintext tokens are NEVER returned.
func (s *ShippingService) AdminListCarriers(ctx context.Context) ([]CarrierConfigView, error) {
	cfgs, err := s.repo.ListCarrierConfigs(ctx)
	if err != nil {
		return nil, err
	}
	allRates, err := s.repo.ListFallbackRates(ctx, "")
	if err != nil {
		return nil, err
	}

	cfgByCode := make(map[string]store.CarrierConfig, len(cfgs))
	for _, c := range cfgs {
		cfgByCode[strings.ToLower(c.CarrierCode)] = c
	}

	ratesByCarrier := make(map[string][]carrier.FallbackRate)
	for _, r := range allRates {
		code := strings.ToLower(r.CarrierCode)
		ratesByCarrier[code] = append(ratesByCarrier[code], carrier.FallbackRate{
			Region:             r.Region,
			BaseFeeVND:         r.BaseFeeVND,
			BaseWeightGrams:    r.BaseWeightGrams,
			ExtraFeeVNDPer500g: r.ExtraFeeVNDPer500g,
		})
	}

	codes := s.aggregator.ListCodes()
	out := make([]CarrierConfigView, 0, len(codes))
	for _, code := range codes {
		provider, _ := s.aggregator.Get(code)
		c, hasRow := cfgByCode[code]
		view := CarrierConfigView{
			CarrierCode:     code,
			Name:            provider.Name(),
			Enabled:         true,
			TokenConfigured: false,
			FallbackRates:   mergeWithDefaultRates(ratesByCarrier[code]),
		}
		if hasRow {
			if c.Name != "" {
				view.Name = c.Name
			}
			view.Enabled = c.Enabled
			view.TokenConfigured = len(c.EncryptedToken) > 0
			view.UpdatedAt = c.UpdatedAt
		}
		out = append(out, view)
	}
	return out, nil
}

// AdminUpdateCarrier updates a carrier's enabled status, encrypted API token, and/or fallback rates (UC-A12).
// Enabling a carrier tests connectivity with the effective token first; invalid tokens prevent enabling.
func (s *ShippingService) AdminUpdateCarrier(ctx context.Context, in AdminUpdateCarrierInput) (*CarrierConfigView, error) {
	code := strings.ToLower(strings.TrimSpace(in.CarrierCode))
	if code == "" {
		return nil, fmt.Errorf("%w: carrier_code is required", ErrInvalidArgument)
	}
	provider, ok := s.aggregator.Get(code)
	if !ok {
		return nil, fmt.Errorf("%w: carrier %s", store.ErrNotFound, code)
	}

	validatedRates, err := validateFallbackRates(code, in.FallbackRates)
	if err != nil {
		return nil, err
	}

	existing, getErr := s.repo.GetCarrierConfig(ctx, code)
	if getErr != nil && !errors.Is(getErr, store.ErrNotFound) {
		return nil, getErr
	}

	newToken := strings.TrimSpace(in.APIToken)
	var encryptedToken []byte
	var effectiveToken string

	if newToken != "" {
		if s.tokenCipher == nil {
			return nil, fmt.Errorf("carrier token encryption key (SHIPPING_CONFIG_KEY) not configured")
		}
		enc, encErr := s.tokenCipher.Encrypt(newToken)
		if encErr != nil {
			return nil, fmt.Errorf("encrypt carrier token: %w", encErr)
		}
		encryptedToken = enc
		effectiveToken = newToken
	} else if existing != nil && len(existing.EncryptedToken) > 0 {
		encryptedToken = existing.EncryptedToken
		if s.tokenCipher != nil {
			if plain, decErr := s.tokenCipher.Decrypt(existing.EncryptedToken); decErr == nil {
				effectiveToken = plain
			}
		}
	}

	// UC-A12: enabling a carrier requires passing adapter connection/token check first.
	if in.Enabled {
		if testErr := provider.TestConnection(ctx, effectiveToken); testErr != nil {
			if errors.Is(testErr, carrier.ErrInvalidCarrierToken) || carrier.IsNonRetryable(testErr) {
				return nil, fmt.Errorf("%w: cannot enable carrier %s with invalid token", ErrCarrierAuthFailed, code)
			}
			return nil, fmt.Errorf("%w: carrier %s connection check failed", ErrCarrierUnavailable, code)
		}
	}

	saved, err := s.repo.UpdateCarrierConfig(ctx, &store.CarrierConfig{
		CarrierCode:    code,
		Name:           provider.Name(),
		Enabled:        in.Enabled,
		EncryptedToken: encryptedToken,
	}, validatedRates)
	if err != nil {
		return nil, err
	}

	if newToken != "" {
		provider.SetToken(newToken)
	}
	s.invalidateConfigCache()

	rates, err := s.repo.ListFallbackRates(ctx, code)
	if err != nil {
		return nil, err
	}
	carrierRates := make([]carrier.FallbackRate, 0, len(rates))
	for _, r := range rates {
		carrierRates = append(carrierRates, carrier.FallbackRate{
			Region:             r.Region,
			BaseFeeVND:         r.BaseFeeVND,
			BaseWeightGrams:    r.BaseWeightGrams,
			ExtraFeeVNDPer500g: r.ExtraFeeVNDPer500g,
		})
	}
	return &CarrierConfigView{
		CarrierCode:     saved.CarrierCode,
		Name:            saved.Name,
		Enabled:         saved.Enabled,
		TokenConfigured: len(saved.EncryptedToken) > 0,
		FallbackRates:   mergeWithDefaultRates(carrierRates),
		UpdatedAt:       saved.UpdatedAt,
	}, nil
}

// AdminTestCarrier tests connectivity against the carrier adapter using the stored decrypted token (UC-A12).
func (s *ShippingService) AdminTestCarrier(ctx context.Context, carrierCode string) (bool, string, error) {
	code := strings.ToLower(strings.TrimSpace(carrierCode))
	if code == "" {
		return false, "", fmt.Errorf("%w: carrier_code is required", ErrInvalidArgument)
	}
	provider, ok := s.aggregator.Get(code)
	if !ok {
		return false, "", fmt.Errorf("%w: carrier %s", store.ErrNotFound, code)
	}

	var token string
	if cfg, err := s.repo.GetCarrierConfig(ctx, code); err == nil && cfg != nil && len(cfg.EncryptedToken) > 0 {
		if s.tokenCipher == nil {
			return false, "Khoá mã hoá cấu hình hãng chưa được thiết lập", nil
		}
		plain, decErr := s.tokenCipher.Decrypt(cfg.EncryptedToken)
		if decErr != nil {
			return false, "Không thể giải mã API token đã lưu", nil
		}
		token = plain
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		return false, "", err
	}

	if err := provider.TestConnection(ctx, token); err != nil {
		if errors.Is(err, carrier.ErrInvalidCarrierToken) {
			return false, "API token không hợp lệ hoặc bị hãng từ chối", nil
		}
		return false, "Không thể kết nối tới hãng vận chuyển", nil
	}
	return true, "Kết nối hãng vận chuyển thành công", nil
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
// and atomically persists the status + Vietnamese description + outbox shipment.updated event (UC-SHIP04).
// Works for both active and platform-disabled carriers so in-flight shipments continue tracking (UC-A12).
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
	// When tracking_code is omitted, only look up FORWARD shipment (never accidentally hit RETURN).
	var (
		existing *store.Shipment
		findErr  error
	)
	if trackingCode != "" {
		existing, findErr = s.repo.GetShipmentByTrackingCode(ctx, trackingCode)
	} else {
		existing, findErr = s.repo.GetShipmentByOrderID(ctx, orderID, store.DirectionForward)
	}
	if findErr != nil {
		return nil, "", findErr
	}
	if !strings.EqualFold(carrierCode, existing.CarrierCode) {
		return nil, "", fmt.Errorf("%w: webhook carrier %q does not match shipment carrier %q", store.ErrCarrierMismatch, carrierCode, existing.CarrierCode)
	}

	occurredAt := in.OccurredAt.UTC()
	if occurredAt.IsZero() {
		occurredAt = s.nowFn()
	}
	descVI := carrier.ResolveDescriptionVI(internalStatus, in.Description)

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
		Description:    descVI,
		OccurredAt:     occurredAt,
		OutboxEventID:  outboxEventID,
		OutboxSubject:  events.SubjectShipmentUpdated,
		Traceparent:    traceparent,
		TraceID:        traceID,
		BuildOutboxPayload: func(sh *store.Shipment) ([]byte, error) {
			dir := store.NormalizeDirection(sh.Direction)
			return json.Marshal(events.ShipmentUpdatedPayload{
				ShipmentID:      sh.ID,
				OrderID:         sh.OrderID,
				ShopID:          sh.ShopID,
				CarrierCode:     sh.CarrierCode,
				TrackingCode:    sh.TrackingCode,
				Status:          internalStatus,
				RawStatus:       strings.TrimSpace(in.RawStatus),
				Description:     descVI,
				OccurredAt:      occurredAt.Format(time.RFC3339),
				Direction:       dir,
				ReturnRequestID: sh.ReturnRequestID,
				RedeliveryCount: sh.RedeliveryCount,
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

// HandleOrderCancelled handles an async order.cancelled NATS event idempotently for the active FORWARD shipment (UC-SHIP07).
func (s *ShippingService) HandleOrderCancelled(ctx context.Context, eventID, orderID, reason string) error {
	sh, err := s.repo.GetActiveShipmentByOrderID(ctx, orderID, store.DirectionForward)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// Order cancelled before shipment creation or already cancelled synchronously.
			_, markErr := s.repo.MarkEventProcessed(ctx, eventID, events.SubjectOrderCancelled)
			return markErr
		}
		return err
	}

	if sh.Status != carrier.StatusCancelled && carrier.IsCancellableStatus(sh.Status) {
		_, _, _ = s.loadCachedConfigs(ctx)
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

// PollActiveShipments paginates through all active non-terminal shipments (FORWARD and RETURN) for live carriers (UC-SHIP04).
func (s *ShippingService) PollActiveShipments(ctx context.Context) error {
	_, _, _ = s.loadCachedConfigs(ctx)
	const batchSize = 100
	var (
		afterUpdated *time.Time
		afterID      string
	)
	for {
		active, err := s.repo.ListActiveShipments(ctx, batchSize, afterUpdated, afterID)
		if err != nil {
			return err
		}
		if len(active) == 0 {
			return nil
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
		if len(active) < batchSize {
			return nil
		}
		last := active[len(active)-1]
		ts := last.UpdatedAt
		afterUpdated = &ts
		afterID = last.ID
	}
}

func (s *ShippingService) classifyCarrierCreateError(ctx context.Context, carrierCode string, err error) error {
	if errors.Is(err, carrier.ErrInvalidCarrierToken) {
		s.logger.ErrorContext(ctx, "carrier authentication or configuration error during shipment creation",
			slog.String("carrier", carrierCode),
			slog.String("error", err.Error()),
		)
		return fmt.Errorf("%w: lỗi cấu hình kết nối hãng vận chuyển, vui lòng liên hệ quản trị viên", ErrCarrierUnavailable)
	}
	if carrier.IsNonRetryable(err) {
		s.logger.WarnContext(ctx, "carrier rejected shipment creation (non-retryable)",
			slog.String("carrier", carrierCode),
			slog.String("error", err.Error()),
		)
		return fmt.Errorf("%w: hãng vận chuyển từ chối địa chỉ hoặc thông tin kiện hàng, vui lòng kiểm tra lại địa chỉ giao/nhận", ErrCarrierRejected)
	}
	return fmt.Errorf("%w: hãng vận chuyển tạm thời không phản hồi, vui lòng thử lại sau: %w", ErrCarrierUnavailable, err)
}

func (s *ShippingService) defaultEnabledCarrier(ctx context.Context) (carrier.Carrier, error) {
	enabledList, _, err := s.platformEnabledCarriers(ctx)
	if err != nil {
		return nil, fmt.Errorf("load platform carrier configs: %w", err)
	}
	if len(enabledList) == 0 {
		return nil, fmt.Errorf("%w: no carriers enabled", ErrNoEnabledCarriers)
	}
	c, ok := s.aggregator.Get(enabledList[0])
	if !ok {
		return nil, fmt.Errorf("%w: no carriers enabled", ErrNoEnabledCarriers)
	}
	return c, nil
}

func (s *ShippingService) resolveDefaultWeight(ctx context.Context, shopID string) int32 {
	if shopID != "" {
		if st, err := s.repo.GetShopShippingSettings(ctx, shopID); err == nil && st != nil && st.DefaultWeightGrams > 0 {
			return st.DefaultWeightGrams
		}
	}
	return defaultWeightGrams
}

func (s *ShippingService) enrichGHNAddress(ctx context.Context, addr *carrier.Address) {
	if addr == nil || strings.TrimSpace(addr.WardCode) == "" {
		return
	}
	if w, err := s.repo.GetWard(ctx, addr.WardCode); err == nil && w != nil {
		addr.GHNDistrictID = w.GHNDistrictID
		addr.GHNWardCode = w.GHNWardCode
	}
}

func parseDirection(raw string) (string, error) {
	dir := store.NormalizeDirection(raw)
	if dir != store.DirectionForward && dir != store.DirectionReturn {
		return "", fmt.Errorf("%w: invalid shipment direction %q", ErrInvalidArgument, raw)
	}
	return dir, nil
}

func validateFallbackRates(carrierCode string, in []carrier.FallbackRate) ([]store.FallbackRate, error) {
	if len(in) == 0 {
		return nil, nil
	}
	seen := make(map[string]bool, len(in))
	out := make([]store.FallbackRate, 0, len(in))
	for _, r := range in {
		region := strings.ToUpper(strings.TrimSpace(r.Region))
		switch region {
		case carrier.RegionSameProvince, carrier.RegionSameRegion, carrier.RegionCrossRegion:
		default:
			return nil, fmt.Errorf("%w: invalid fallback rate region %q", ErrInvalidArgument, r.Region)
		}
		if seen[region] {
			return nil, fmt.Errorf("%w: duplicate fallback rate for region %s", ErrInvalidArgument, region)
		}
		seen[region] = true
		if r.BaseFeeVND < 0 || r.ExtraFeeVNDPer500g < 0 {
			return nil, fmt.Errorf("%w: fallback fees must be >= 0", ErrInvalidArgument)
		}
		if r.BaseWeightGrams <= 0 {
			return nil, fmt.Errorf("%w: base_weight_grams must be > 0", ErrInvalidArgument)
		}
		out = append(out, store.FallbackRate{
			CarrierCode:        carrierCode,
			Region:             region,
			BaseFeeVND:         r.BaseFeeVND,
			BaseWeightGrams:    r.BaseWeightGrams,
			ExtraFeeVNDPer500g: r.ExtraFeeVNDPer500g,
		})
	}
	return out, nil
}

func mergeWithDefaultRates(existing []carrier.FallbackRate) []carrier.FallbackRate {
	byRegion := make(map[string]carrier.FallbackRate, 3)
	for _, d := range carrier.DefaultFallbackRates() {
		byRegion[d.Region] = d
	}
	for _, r := range existing {
		byRegion[strings.ToUpper(strings.TrimSpace(r.Region))] = r
	}
	order := []string{carrier.RegionSameProvince, carrier.RegionSameRegion, carrier.RegionCrossRegion}
	out := make([]carrier.FallbackRate, 0, 3)
	for _, region := range order {
		if r, ok := byRegion[region]; ok {
			out = append(out, r)
		}
	}
	return out
}
