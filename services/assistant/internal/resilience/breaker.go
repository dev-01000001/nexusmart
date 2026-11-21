package resilience

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ErrCircuitOpen is returned when the circuit breaker rejects a call while open.
var ErrCircuitOpen = errors.New("upstream circuit breaker is open")

type breakerState int

const (
	stateClosed breakerState = iota
	stateOpen
	stateHalfOpen
)

// CircuitBreaker is a thread-safe circuit breaker for outgoing HTTP and gRPC calls
// that admits at most one concurrent probe when half-open.
type CircuitBreaker struct {
	mu               sync.Mutex
	state            breakerState
	failures         int
	maxFailures      int
	resetTimeout     time.Duration
	openedAt         time.Time
	halfOpenInFlight bool
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
	}
}

func (cb *CircuitBreaker) acquire() (allowed bool, isHalfOpenProbe bool) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case stateClosed:
		return true, false
	case stateOpen:
		if time.Since(cb.openedAt) >= cb.resetTimeout {
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

// RecordSuccess resets failure count and closes the breaker.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.state = stateClosed
	cb.failures = 0
	cb.halfOpenInFlight = false
}

// RecordFailure increments failure count and opens the breaker when maxFailures is reached.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if cb.state == stateHalfOpen {
		cb.state = stateOpen
		cb.openedAt = time.Now()
		cb.halfOpenInFlight = false
		return
	}
	cb.failures++
	if cb.failures >= cb.maxFailures {
		cb.state = stateOpen
		cb.openedAt = time.Now()
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

// IsTransientGRPCError reports whether err is a transient gRPC failure suitable for retry/circuit-breaking.
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

// HTTPStatusError wraps a non-2xx HTTP status code for retry classification.
type HTTPStatusError struct {
	StatusCode int
	Body       string
}

func (e *HTTPStatusError) Error() string {
	if e == nil {
		return ""
	}
	return http.StatusText(e.StatusCode)
}

// IsTransientHTTPError classifies network errors, timeouts, 429, 502, 503, 504, 529 as transient.
func IsTransientHTTPError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	var statusErr *HTTPStatusError
	if errors.As(err, &statusErr) {
		switch statusErr.StatusCode {
		case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable,
			http.StatusGatewayTimeout, http.StatusInternalServerError, 529:
			return true
		default:
			return false
		}
	}
	return true
}

// CallWithRetry executes fn with per-attempt timeout, exponential backoff, and circuit breaking.
func CallWithRetry[T any](
	ctx context.Context,
	cb *CircuitBreaker,
	attempts int,
	baseBackoff time.Duration,
	attemptTimeout time.Duration,
	isTransient func(error) bool,
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
		attemptTimeout = 5 * time.Second
	}
	if isTransient == nil {
		isTransient = IsTransientGRPCError
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
		if !isTransient(err) {
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
