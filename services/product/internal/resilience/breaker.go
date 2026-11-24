package resilience

import (
	"context"
	"errors"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ErrCircuitOpen is returned when the circuit breaker is open and rejects calls immediately.
var ErrCircuitOpen = errors.New("upstream circuit breaker is open")

type breakerState int

const (
	stateClosed breakerState = iota
	stateOpen
	stateHalfOpen
)

// CircuitBreaker is a lightweight thread-safe circuit breaker for outgoing gRPC calls
// that admits only a single in-flight probe when half-open.
type CircuitBreaker struct {
	mu               sync.Mutex
	state            breakerState
	failures         int
	maxFailures      int
	resetTimeout     time.Duration
	openedAt         time.Time
	halfOpenInFlight bool
	nowFn            func() time.Time
}

// NewCircuitBreaker constructs a CircuitBreaker that opens after maxFailures consecutive transient errors.
func NewCircuitBreaker(maxFailures int, resetTimeout time.Duration) *CircuitBreaker {
	if maxFailures <= 0 {
		maxFailures = 5
	}
	if resetTimeout <= 0 {
		resetTimeout = 15 * time.Second
	}
	return &CircuitBreaker{
		state:        stateClosed,
		maxFailures:  maxFailures,
		resetTimeout: resetTimeout,
		nowFn:        time.Now,
	}
}

// SetClock overrides the breaker clock (intended for deterministic unit tests).
func (cb *CircuitBreaker) SetClock(fn func() time.Time) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if fn == nil {
		cb.nowFn = time.Now
		return
	}
	cb.nowFn = fn
}

func (cb *CircuitBreaker) nowLocked() time.Time {
	if cb.nowFn == nil {
		return time.Now()
	}
	return cb.nowFn()
}

// Allow checks whether a request may proceed. In half-open state, at most one concurrent probe is admitted.
func (cb *CircuitBreaker) Allow() bool {
	allowed, _ := cb.acquire()
	return allowed
}

func (cb *CircuitBreaker) acquire() (allowed bool, isHalfOpenProbe bool) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case stateClosed:
		return true, false
	case stateOpen:
		if cb.nowLocked().Sub(cb.openedAt) >= cb.resetTimeout {
			cb.state = stateHalfOpen
			cb.halfOpenInFlight = true
			return true, true
		}
		return false, false
	case stateHalfOpen:
		if cb.halfOpenInFlight {
			return false, false
		}
		cb.halfOpenInFlight = true
		return true, true
	default:
		return false, false
	}
}

// RecordSuccess resets the breaker to the closed state.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.state = stateClosed
	cb.failures = 0
	cb.halfOpenInFlight = false
}

// RecordFailure increments consecutive failures and opens the breaker once the threshold is reached.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if cb.state == stateHalfOpen {
		cb.state = stateOpen
		cb.openedAt = cb.nowLocked()
		cb.halfOpenInFlight = false
		return
	}
	cb.failures++
	if cb.failures >= cb.maxFailures {
		cb.state = stateOpen
		cb.openedAt = cb.nowLocked()
		cb.halfOpenInFlight = false
	}
}

func (cb *CircuitBreaker) releaseUnfinishedProbe() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if cb.state == stateHalfOpen && cb.halfOpenInFlight {
		cb.halfOpenInFlight = false
	}
}

// IsTransientGRPCError reports whether err represents a transient upstream failure suitable for retry/circuit-breaking.
func IsTransientGRPCError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded, codes.ResourceExhausted:
		return true
	default:
		return false
	}
}

// CallWithRetry executes fn with per-attempt timeout, exponential backoff, and circuit breaking.
func CallWithRetry[T any](
	ctx context.Context,
	cb *CircuitBreaker,
	attempts int,
	baseBackoff time.Duration,
	attemptTimeout time.Duration,
	fn func(attemptCtx context.Context) (T, error),
) (T, error) {
	var zero T
	if attempts <= 0 {
		attempts = 1
	}
	if baseBackoff <= 0 {
		baseBackoff = 50 * time.Millisecond
	}
	if attemptTimeout <= 0 {
		attemptTimeout = 3 * time.Second
	}

	isHalfOpenProbe := false
	if cb != nil {
		var allowed bool
		allowed, isHalfOpenProbe = cb.acquire()
		if !allowed {
			return zero, ErrCircuitOpen
		}
		if isHalfOpenProbe {
			attempts = 1
			defer cb.releaseUnfinishedProbe()
		}
	}

	var lastErr error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			backoff := baseBackoff * time.Duration(1<<(i-1))
			timer := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return zero, ctx.Err()
			case <-timer.C:
			}
		}

		attemptCtx, cancel := context.WithTimeout(ctx, attemptTimeout)
		res, err := fn(attemptCtx)
		cancel()

		if err == nil {
			if cb != nil {
				cb.RecordSuccess()
			}
			return res, nil
		}

		lastErr = err
		if !IsTransientGRPCError(err) {
			if cb != nil {
				cb.RecordSuccess()
			}
			return zero, err
		}

		if cb != nil {
			cb.RecordFailure()
			if isHalfOpenProbe {
				return zero, lastErr
			}
		}
	}
	return zero, lastErr
}
