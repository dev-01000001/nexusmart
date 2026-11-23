package carrier

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"sync"
)

const (
	CodeFallback = "fallback"
	NameFallback = "NexusMart Standard Fallback"
)

// ErrRouteOutOfServiceArea is returned when every enabled carrier explicitly rejects a route as unserviceable.
var ErrRouteOutOfServiceArea = errors.New("route outside carrier service area")

// Aggregator queries enabled carriers in parallel and falls back to the static fee table
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
	c, ok := a.carriers[code]
	return c, ok
}

// DefaultCarrier returns the first registered carrier (GHN when enabled).
func (a *Aggregator) DefaultCarrier() (Carrier, bool) {
	if len(a.order) == 0 {
		return nil, false
	}
	return a.carriers[a.order[0]], true
}

// CalculateFeeAll fans out CalculateFee to all enabled carriers concurrently via goroutines.
// Partial carrier failures are omitted; if all carriers fail transiently, FallbackQuotes is returned.
// If every enabled carrier explicitly rejects the route with a NonRetryableError, ErrRouteOutOfServiceArea is returned.
func (a *Aggregator) CalculateFeeAll(ctx context.Context, req FeeRequest) ([]Quote, bool, error) {
	if len(a.order) == 0 {
		return FallbackQuotes(req), true, nil
	}

	var (
		wg                   sync.WaitGroup
		mu                   sync.Mutex
		quotes               []Quote
		nonRetryableFailures int
	)

	for _, code := range a.order {
		c := a.carriers[code]
		wg.Add(1)
		go func(provider Carrier) {
			defer wg.Done()
			res, err := provider.CalculateFee(ctx, req)
			if err != nil {
				a.logger.WarnContext(ctx, "carrier fee calculation failed; omitting carrier",
					slog.String("carrier", provider.Code()),
					slog.String("error", err.Error()),
				)
				if IsNonRetryable(err) {
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
		if nonRetryableFailures == len(a.order) {
			return nil, false, ErrRouteOutOfServiceArea
		}
		a.logger.WarnContext(ctx, "all shipping carriers failed; serving fallback fee table")
		return FallbackQuotes(req), true, nil
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

// FallbackQuotes returns deterministic integer-VND quotes from the platform fallback fee table (UC-SHIP02).
func FallbackQuotes(req FeeRequest) []Quote {
	baseFee, days := routeZoneFee(req.FromProvinceCode, req.ToProvinceCode, 25000, 35000, 45000)
	weightSurcharge := weightFeeVND(req.WeightGrams, 1000, 500, 6000)
	return []Quote{
		{
			CarrierCode:   CodeFallback,
			CarrierName:   NameFallback,
			ServiceCode:   "FALLBACK_STD",
			ServiceName:   "Vận chuyển tiêu chuẩn (Bảng phí dự phòng)",
			FeeVND:        baseFee + weightSurcharge,
			EstimatedDays: days + 1,
			IsFallback:    true,
		},
	}
}
