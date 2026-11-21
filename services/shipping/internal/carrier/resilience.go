package carrier

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/sony/gobreaker/v2"
)

// ResilienceConfig configures timeout, exponential backoff retry, and circuit breaker per carrier.
type ResilienceConfig struct {
	AttemptTimeout  time.Duration
	MaxAttempts     int
	BaseBackoff     time.Duration
	MaxBackoff      time.Duration
	CBMaxFailures   uint32
	CBOpenTimeout   time.Duration
	CBHalfOpenLimit uint32
	Logger          *slog.Logger
}

// DefaultResilienceConfig returns safe production/sandbox defaults.
func DefaultResilienceConfig(logger *slog.Logger) ResilienceConfig {
	return ResilienceConfig{
		AttemptTimeout:  3 * time.Second,
		MaxAttempts:     3,
		BaseBackoff:     100 * time.Millisecond,
		MaxBackoff:      2 * time.Second,
		CBMaxFailures:   3,
		CBOpenTimeout:   30 * time.Second,
		CBHalfOpenLimit: 2,
		Logger:          logger,
	}
}

// ResilientCarrier wraps a Carrier with per-attempt timeout, exponential backoff retry with jitter,
// and a per-carrier circuit breaker.
type ResilientCarrier struct {
	inner  Carrier
	cfg    ResilienceConfig
	cb     *gobreaker.CircuitBreaker[any]
	logger *slog.Logger
}

// NewResilientCarrier wraps inner with timeout, retry/backoff, and circuit breaker protection.
func NewResilientCarrier(inner Carrier, cfg ResilienceConfig) *ResilientCarrier {
	if cfg.AttemptTimeout <= 0 {
		cfg.AttemptTimeout = 3 * time.Second
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 1
	}
	if cfg.BaseBackoff <= 0 {
		cfg.BaseBackoff = 50 * time.Millisecond
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 2 * time.Second
	}
	if cfg.CBMaxFailures == 0 {
		cfg.CBMaxFailures = 3
	}
	if cfg.CBOpenTimeout <= 0 {
		cfg.CBOpenTimeout = 30 * time.Second
	}
	if cfg.CBHalfOpenLimit == 0 {
		cfg.CBHalfOpenLimit = 2
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	settings := gobreaker.Settings{
		Name:        "carrier-" + inner.Code(),
		MaxRequests: cfg.CBHalfOpenLimit,
		Interval:    60 * time.Second,
		Timeout:     cfg.CBOpenTimeout,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= cfg.CBMaxFailures
		},
		IsSuccessful: func(err error) bool {
			// Client validation 4xx errors shouldn't trip the carrier's circuit breaker.
			return err == nil || IsNonRetryable(err)
		},
		OnStateChange: func(name string, from, to gobreaker.State) {
			logger.Warn("carrier circuit breaker state changed",
				slog.String("breaker", name),
				slog.String("from", from.String()),
				slog.String("to", to.String()),
			)
		},
	}

	return &ResilientCarrier{
		inner:  inner,
		cfg:    cfg,
		cb:     gobreaker.NewCircuitBreaker[any](settings),
		logger: logger,
	}
}

// Code returns the wrapped carrier code.
func (r *ResilientCarrier) Code() string { return r.inner.Code() }

// Name returns the wrapped carrier display name.
func (r *ResilientCarrier) Name() string { return r.inner.Name() }

// IsMock reports whether the wrapped carrier runs in mock mode.
func (r *ResilientCarrier) IsMock() bool { return r.inner.IsMock() }

// CanPoll reports whether the wrapped carrier supports active status polling.
func (r *ResilientCarrier) CanPoll() bool {
	if p, ok := r.inner.(interface{ CanPoll() bool }); ok {
		return p.CanPoll()
	}
	return !r.inner.IsMock()
}

// State returns the current circuit breaker state (for diagnostics/tests).
func (r *ResilientCarrier) State() gobreaker.State { return r.cb.State() }

// CalculateFee invokes CalculateFee with timeout, retry/backoff, and circuit breaker protection.
func (r *ResilientCarrier) CalculateFee(ctx context.Context, req FeeRequest) ([]Quote, error) {
	val, err := r.execute(ctx, "CalculateFee", func(attemptCtx context.Context) (any, error) {
		return r.inner.CalculateFee(attemptCtx, req)
	})
	if err != nil {
		return nil, err
	}
	quotes, _ := val.([]Quote)
	return quotes, nil
}

// CreateShipment invokes CreateShipment with timeout, retry/backoff, and circuit breaker protection.
func (r *ResilientCarrier) CreateShipment(ctx context.Context, req ShipmentRequest) (*ShipmentResult, error) {
	val, err := r.execute(ctx, "CreateShipment", func(attemptCtx context.Context) (any, error) {
		return r.inner.CreateShipment(attemptCtx, req)
	})
	if err != nil {
		return nil, err
	}
	res, _ := val.(*ShipmentResult)
	return res, nil
}

// CancelShipment invokes CancelShipment with timeout, retry/backoff, and circuit breaker protection.
func (r *ResilientCarrier) CancelShipment(ctx context.Context, trackingCode string) error {
	_, err := r.execute(ctx, "CancelShipment", func(attemptCtx context.Context) (any, error) {
		return nil, r.inner.CancelShipment(attemptCtx, trackingCode)
	})
	return err
}

// GetStatus invokes GetStatus with timeout, retry/backoff, and circuit breaker protection.
func (r *ResilientCarrier) GetStatus(ctx context.Context, trackingCode string) (*TrackingStatus, error) {
	val, err := r.execute(ctx, "GetStatus", func(attemptCtx context.Context) (any, error) {
		return r.inner.GetStatus(attemptCtx, trackingCode)
	})
	if err != nil {
		return nil, err
	}
	st, _ := val.(*TrackingStatus)
	return st, nil
}

func (r *ResilientCarrier) execute(ctx context.Context, op string, fn func(context.Context) (any, error)) (any, error) {
	var lastErr error
	for attempt := 0; attempt < r.cfg.MaxAttempts; attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		res, err := r.cb.Execute(func() (any, error) {
			attemptCtx, cancel := context.WithTimeout(ctx, r.cfg.AttemptTimeout)
			defer cancel()
			return fn(attemptCtx)
		})
		if err == nil {
			return res, nil
		}
		lastErr = err

		if errors.Is(err, gobreaker.ErrOpenState) || errors.Is(err, gobreaker.ErrTooManyRequests) {
			return nil, fmt.Errorf("carrier %s %s rejected by circuit breaker: %w", r.inner.Code(), op, err)
		}
		if IsNonRetryable(err) || errors.Is(ctx.Err(), context.Canceled) {
			return nil, err
		}

		if attempt+1 < r.cfg.MaxAttempts {
			delay := r.backoffDelay(attempt)
			r.logger.WarnContext(ctx, "retrying carrier call",
				slog.String("carrier", r.inner.Code()),
				slog.String("op", op),
				slog.Int("attempt", attempt+1),
				slog.Duration("backoff", delay),
				slog.String("error", err.Error()),
			)
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return nil, fmt.Errorf("carrier %s %s failed after %d attempts: %w", r.inner.Code(), op, r.cfg.MaxAttempts, lastErr)
}

func (r *ResilientCarrier) backoffDelay(attempt int) time.Duration {
	delay := r.cfg.BaseBackoff
	for i := 0; i < attempt; i++ {
		delay *= 2
		if delay >= r.cfg.MaxBackoff {
			delay = r.cfg.MaxBackoff
			break
		}
	}
	// Apply 50%-100% equal jitter to prevent thundering herds.
	half := delay / 2
	if half <= 0 {
		return delay
	}
	return half + time.Duration(rand.Int64N(int64(half)+1))
}
