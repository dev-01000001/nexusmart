package resilience

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/sony/gobreaker/v2"
)

// NonRetryableError marks an error as permanent (e.g. 4xx validation or NotFound)
// so retry loops fail fast and the circuit breaker does not trip.
type NonRetryableError struct {
	Err error
}

func (e *NonRetryableError) Error() string {
	if e == nil || e.Err == nil {
		return "non-retryable error"
	}
	return e.Err.Error()
}

func (e *NonRetryableError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// NonRetryable wraps err so Executor skips retries and treats it as non-tripping.
func NonRetryable(err error) error {
	if err == nil {
		return nil
	}
	return &NonRetryableError{Err: err}
}

// IsNonRetryable reports whether err is wrapped with NonRetryableError.
func IsNonRetryable(err error) bool {
	var nr *NonRetryableError
	return errors.As(err, &nr)
}

// Config configures per-attempt timeout, exponential backoff retry with jitter,
// and a circuit breaker for outbound dependencies.
type Config struct {
	Name            string
	AttemptTimeout  time.Duration
	MaxAttempts     int
	BaseBackoff     time.Duration
	MaxBackoff      time.Duration
	CBMaxFailures   uint32
	CBOpenTimeout   time.Duration
	CBHalfOpenLimit uint32
	Logger          *slog.Logger
}

// DefaultConfig returns safe defaults for outbound HTTP/gRPC dependencies.
func DefaultConfig(name string, logger *slog.Logger) Config {
	return Config{
		Name:            name,
		AttemptTimeout:  3 * time.Second,
		MaxAttempts:     3,
		BaseBackoff:     50 * time.Millisecond,
		MaxBackoff:      2 * time.Second,
		CBMaxFailures:   5,
		CBOpenTimeout:   30 * time.Second,
		CBHalfOpenLimit: 2,
		Logger:          logger,
	}
}

// Executor wraps outbound calls with timeout, retry/backoff, and a circuit breaker.
type Executor struct {
	name   string
	cfg    Config
	cb     *gobreaker.CircuitBreaker[any]
	logger *slog.Logger
}

// NewExecutor constructs an Executor with normalized defaults.
func NewExecutor(cfg Config) *Executor {
	if cfg.Name == "" {
		cfg.Name = "outbound"
	}
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
		cfg.CBMaxFailures = 5
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
		Name:        cfg.Name,
		MaxRequests: cfg.CBHalfOpenLimit,
		Interval:    60 * time.Second,
		Timeout:     cfg.CBOpenTimeout,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= cfg.CBMaxFailures
		},
		IsSuccessful: func(err error) bool {
			return err == nil || IsNonRetryable(err) || errors.Is(err, context.Canceled)
		},
		OnStateChange: func(name string, from, to gobreaker.State) {
			logger.Warn("circuit breaker state changed",
				slog.String("breaker", name),
				slog.String("from", from.String()),
				slog.String("to", to.String()),
			)
		},
	}

	return &Executor{
		name:   cfg.Name,
		cfg:    cfg,
		cb:     gobreaker.NewCircuitBreaker[any](settings),
		logger: logger,
	}
}

// Name returns the configured dependency name.
func (e *Executor) Name() string { return e.name }

// State returns the current circuit breaker state.
func (e *Executor) State() gobreaker.State { return e.cb.State() }

// Run executes fn returning only an error under timeout, retry, and circuit breaker protection.
func (e *Executor) Run(ctx context.Context, op string, fn func(context.Context) error) error {
	_, err := Do(ctx, e, op, func(attemptCtx context.Context) (struct{}, error) {
		return struct{}{}, fn(attemptCtx)
	})
	return err
}

// Do executes fn returning a typed result under timeout, retry, and circuit breaker protection.
func Do[T any](ctx context.Context, e *Executor, op string, fn func(context.Context) (T, error)) (T, error) {
	var zero T
	if e == nil {
		return fn(ctx)
	}

	var lastErr error
	for attempt := 0; attempt < e.cfg.MaxAttempts; attempt++ {
		if ctx.Err() != nil {
			return zero, ctx.Err()
		}

		res, err := e.cb.Execute(func() (any, error) {
			attemptCtx, cancel := context.WithTimeout(ctx, e.cfg.AttemptTimeout)
			defer cancel()
			return fn(attemptCtx)
		})
		if err == nil {
			typed, _ := res.(T)
			return typed, nil
		}
		lastErr = err

		if errors.Is(err, gobreaker.ErrOpenState) || errors.Is(err, gobreaker.ErrTooManyRequests) {
			return zero, fmt.Errorf("%s %s rejected by circuit breaker: %w", e.name, op, err)
		}
		if IsNonRetryable(err) || errors.Is(ctx.Err(), context.Canceled) {
			return zero, err
		}

		if attempt+1 < e.cfg.MaxAttempts {
			delay := e.backoffDelay(attempt)
			e.logger.WarnContext(ctx, "retrying outbound dependency call",
				slog.String("dependency", e.name),
				slog.String("op", op),
				slog.Int("attempt", attempt+1),
				slog.Duration("backoff", delay),
				slog.String("error", err.Error()),
			)
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return zero, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return zero, fmt.Errorf("%s %s failed after %d attempts: %w", e.name, op, e.cfg.MaxAttempts, lastErr)
}

func (e *Executor) backoffDelay(attempt int) time.Duration {
	delay := e.cfg.BaseBackoff
	for i := 0; i < attempt; i++ {
		delay *= 2
		if delay >= e.cfg.MaxBackoff {
			delay = e.cfg.MaxBackoff
			break
		}
	}
	half := delay / 2
	if half <= 0 {
		return delay
	}
	return half + time.Duration(rand.Int64N(int64(half)+1))
}
