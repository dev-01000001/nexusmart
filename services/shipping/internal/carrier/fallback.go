package carrier

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/nexusmart/core/services/shipping/internal/address"
)

const (
	CodeFallback = "fallback"
	NameFallback = "NexusMart Standard Fallback"

	RegionSameProvince = "SAME_PROVINCE"
	RegionSameRegion   = "SAME_REGION"
	RegionCrossRegion  = "CROSS_REGION"
)

var (
	// ErrRouteOutOfServiceArea is returned when every enabled carrier explicitly rejects a route as unserviceable.
	ErrRouteOutOfServiceArea = errors.New("route outside carrier service area")
	// ErrNoEnabledCarriers is returned when no carrier is enabled for quoting.
	ErrNoEnabledCarriers = errors.New("no shipping carriers enabled")
)

// FallbackRate defines one region row in the DB-backed fallback fee table (UC-A12).
type FallbackRate struct {
	Region             string
	BaseFeeVND         int64
	BaseWeightGrams    int32
	ExtraFeeVNDPer500g int64
}

// DefaultFallbackRates returns the platform default fallback rates matching the original hardcoded VND table.
func DefaultFallbackRates() []FallbackRate {
	return []FallbackRate{
		{Region: RegionSameProvince, BaseFeeVND: 25000, BaseWeightGrams: 1000, ExtraFeeVNDPer500g: 6000},
		{Region: RegionSameRegion, BaseFeeVND: 35000, BaseWeightGrams: 1000, ExtraFeeVNDPer500g: 6000},
		{Region: RegionCrossRegion, BaseFeeVND: 45000, BaseWeightGrams: 1000, ExtraFeeVNDPer500g: 6000},
	}
}

// Aggregator queries enabled carriers in parallel and falls back to the DB/static fee table
// when all enabled carriers fail or have tripped circuit breakers (UC-SHIP02).
type Aggregator struct {
	carriers map[string]Carrier
	order    []string
	logger   *slog.Logger
}

// NewAggregator creates a multi-carrier aggregator.
func NewAggregator(logger *slog.Logger, carriers ...Carrier) *Aggregator {
	if logger == nil {
		logger = slog.Default()
	}
	agg := &Aggregator{
		carriers: make(map[string]Carrier, len(carriers)),
		order:    make([]string, 0, len(carriers)),
		logger:   logger,
	}
	for _, c := range carriers {
		if c == nil {
			continue
		}
		if _, exists := agg.carriers[c.Code()]; !exists {
			agg.order = append(agg.order, c.Code())
		}
		agg.carriers[c.Code()] = c
	}
	return agg
}

// Get returns a registered carrier by code.
func (a *Aggregator) Get(code string) (Carrier, bool) {
	c, ok := a.carriers[strings.ToLower(strings.TrimSpace(code))]
	return c, ok
}

// ListCodes returns registered carrier codes in registration order.
func (a *Aggregator) ListCodes() []string {
	out := make([]string, len(a.order))
	copy(out, a.order)
	return out
}

// CalculateFeeAll fans out CalculateFee to all registered carriers concurrently via goroutines.
func (a *Aggregator) CalculateFeeAll(ctx context.Context, req FeeRequest) ([]Quote, bool, error) {
	return a.CalculateFeeForCarriers(ctx, req, nil, nil)
}

// CalculateFeeForCarriers fans out CalculateFee only to the specified allowedCodes (nil = all registered).
// If allowedCodes is explicitly non-nil and empty, ErrNoEnabledCarriers is returned.
// Partial carrier failures are omitted; if all queried carriers fail transiently, FallbackQuotesWithRates is returned.
// If every queried carrier explicitly rejects the route with a NonRetryableError, ErrRouteOutOfServiceArea is returned.
func (a *Aggregator) CalculateFeeForCarriers(
	ctx context.Context,
	req FeeRequest,
	allowedCodes []string,
	fallbackRates []FallbackRate,
) ([]Quote, bool, error) {
	var targets []Carrier
	if allowedCodes == nil {
		for _, code := range a.order {
			targets = append(targets, a.carriers[code])
		}
		if len(targets) == 0 {
			return FallbackQuotesWithRates(req, fallbackRates), true, nil
		}
	} else {
		allowSet := make(map[string]struct{}, len(allowedCodes))
		for _, code := range allowedCodes {
			allowSet[strings.ToLower(strings.TrimSpace(code))] = struct{}{}
		}
		for _, code := range a.order {
			if _, ok := allowSet[code]; ok {
				targets = append(targets, a.carriers[code])
			}
		}
		if len(targets) == 0 {
			return nil, false, ErrNoEnabledCarriers
		}
	}

	var (
		wg                   sync.WaitGroup
		mu                   sync.Mutex
		quotes               []Quote
		nonRetryableFailures int
	)

	for _, c := range targets {
		wg.Add(1)
		go func(provider Carrier) {
			defer wg.Done()
			res, err := provider.CalculateFee(ctx, req)
			if err != nil {
				a.logger.WarnContext(ctx, "carrier fee calculation failed; omitting carrier",
					slog.String("carrier", provider.Code()),
					slog.String("error", err.Error()),
				)
				if IsNonRetryable(err) && !errors.Is(err, ErrInvalidCarrierToken) && !errors.Is(err, ErrMissingGHNMapping) {
					mu.Lock()
					nonRetryableFailures++
					mu.Unlock()
				}
				return
			}
			if len(res) == 0 {
				return
			}
			mu.Lock()
			quotes = append(quotes, res...)
			mu.Unlock()
		}(c)
	}
	wg.Wait()

	if len(quotes) == 0 {
		if nonRetryableFailures == len(targets) {
			return nil, false, ErrRouteOutOfServiceArea
		}
		a.logger.WarnContext(ctx, "all shipping carriers failed; serving fallback fee table")
		return FallbackQuotesWithRates(req, fallbackRates), true, nil
	}

	sort.Slice(quotes, func(i, j int) bool {
		if quotes[i].FeeVND != quotes[j].FeeVND {
			return quotes[i].FeeVND < quotes[j].FeeVND
		}
		if quotes[i].CarrierCode != quotes[j].CarrierCode {
			return quotes[i].CarrierCode < quotes[j].CarrierCode
		}
		return quotes[i].ServiceCode < quotes[j].ServiceCode
	})
	return quotes, false, nil
}

// FallbackQuotesWithRates calculates integer-VND fallback quotes using DB-configured rates (defaulting to DefaultFallbackRates).
func FallbackQuotesWithRates(req FeeRequest, rates []FallbackRate) []Quote {
	region, days := routeRegionAndDays(req.FromProvinceCode, req.ToProvinceCode)

	chosen := defaultRateForRegion(region)
	for _, r := range rates {
		if strings.EqualFold(strings.TrimSpace(r.Region), region) {
			chosen = r
			break
		}
	}
	baseWeight := chosen.BaseWeightGrams
	if baseWeight <= 0 {
		baseWeight = 1000
	}
	weightSurcharge := weightFeeVND(req.WeightGrams, baseWeight, 500, chosen.ExtraFeeVNDPer500g)
	return []Quote{
		{
			CarrierCode:   CodeFallback,
			CarrierName:   NameFallback,
			ServiceCode:   "FALLBACK_STD",
			ServiceName:   "Vận chuyển tiêu chuẩn (Bảng phí dự phòng)",
			FeeVND:        chosen.BaseFeeVND + weightSurcharge,
			EstimatedDays: days + 1,
			IsFallback:    true,
		},
	}
}

func routeRegionAndDays(fromProv, toProv string) (string, int32) {
	if fromProv != "" && fromProv == toProv {
		return RegionSameProvince, 2
	}
	if address.RegionOfProvince(fromProv) == address.RegionOfProvince(toProv) {
		return RegionSameRegion, 3
	}
	return RegionCrossRegion, 4
}

func defaultRateForRegion(region string) FallbackRate {
	for _, r := range DefaultFallbackRates() {
		if r.Region == region {
			return r
		}
	}
	return FallbackRate{Region: RegionCrossRegion, BaseFeeVND: 45000, BaseWeightGrams: 1000, ExtraFeeVNDPer500g: 6000}
}
