package resilience

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestCircuitBreaker_StateTransitions(t *testing.T) {
	cb := NewCircuitBreaker(2, 40*time.Millisecond)
	transientErr := status.Error(codes.Unavailable, "downstream unavailable")

	// 1st transient failure keeps breaker closed.
	_, err := CallWithRetry(context.Background(), cb, 1, time.Millisecond, time.Second, IsTransientGRPCError,
		func(context.Context) (string, error) {
			return "", transientErr
		})
	if !errors.Is(err, transientErr) {
		t.Fatalf("expected transientErr, got %v", err)
	}

	// 2nd transient failure opens breaker.
	_, err = CallWithRetry(context.Background(), cb, 1, time.Millisecond, time.Second, IsTransientGRPCError,
		func(context.Context) (string, error) {
			return "", transientErr
		})
	if !errors.Is(err, transientErr) {
		t.Fatalf("expected transientErr, got %v", err)
	}

	// While open, calls are rejected immediately without invoking fn.
	var invoked atomic.Bool
	_, err = CallWithRetry(context.Background(), cb, 3, time.Millisecond, time.Second, IsTransientGRPCError,
		func(context.Context) (string, error) {
			invoked.Store(true)
			return "ok", nil
		})
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen, got %v", err)
	}
	if invoked.Load() {
		t.Fatalf("expected fn not to be invoked while circuit is open")
	}

	// Wait for resetTimeout -> half-open probe failure re-opens immediately (1 attempt max).
	time.Sleep(50 * time.Millisecond)
	var probeAttempts atomic.Int32
	_, err = CallWithRetry(context.Background(), cb, 3, time.Millisecond, time.Second, IsTransientGRPCError,
		func(context.Context) (string, error) {
			probeAttempts.Add(1)
			return "", transientErr
		})
	if !errors.Is(err, transientErr) {
		t.Fatalf("expected probe transientErr, got %v", err)
	}
	if probeAttempts.Load() != 1 {
		t.Fatalf("expected half-open probe to run at most 1 attempt, got %d", probeAttempts.Load())
	}

	// Immediately after failed probe, breaker is open again.
	_, err = CallWithRetry(context.Background(), cb, 1, time.Millisecond, time.Second, IsTransientGRPCError,
		func(context.Context) (string, error) {
			return "ok", nil
		})
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen after failed half-open probe, got %v", err)
	}

	// Wait again -> successful probe closes breaker.
	time.Sleep(50 * time.Millisecond)
	val, err := CallWithRetry(context.Background(), cb, 1, time.Millisecond, time.Second, IsTransientGRPCError,
		func(context.Context) (string, error) {
			return "recovered", nil
		})
	if err != nil || val != "recovered" {
		t.Fatalf("expected recovery, val=%q err=%v", val, err)
	}
}

func TestCircuitBreaker_HalfOpenSingleProbeConcurrency(t *testing.T) {
	cb := NewCircuitBreaker(1, 30*time.Millisecond)
	cb.RecordFailure() // open immediately
	time.Sleep(40 * time.Millisecond)

	gate := make(chan struct{})
	started := make(chan struct{}, 1)
	var admitted atomic.Int32
	var rejected atomic.Int32

	var wg sync.WaitGroup
	const workers = 6
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := CallWithRetry(context.Background(), cb, 1, time.Millisecond, time.Second, IsTransientGRPCError,
				func(context.Context) (string, error) {
					admitted.Add(1)
					select {
					case started <- struct{}{}:
					default:
					}
					<-gate
					return "ok", nil
				})
			if errors.Is(err, ErrCircuitOpen) {
				rejected.Add(1)
			}
		}()
	}

	<-started
	// Give other goroutines a moment to hit acquire() while probe is in flight.
	time.Sleep(15 * time.Millisecond)
	close(gate)
	wg.Wait()

	if admitted.Load() != 1 {
		t.Errorf("expected exactly 1 half-open probe admitted, got %d", admitted.Load())
	}
	if rejected.Load() != workers-1 {
		t.Errorf("expected %d concurrent callers rejected with ErrCircuitOpen, got %d", workers-1, rejected.Load())
	}
}

func TestCallWithRetry_RetryBackoffNonTransientAndCancel(t *testing.T) {
	t.Run("retries transient error and succeeds", func(t *testing.T) {
		cb := NewCircuitBreaker(5, time.Second)
		var tries atomic.Int32
		res, err := CallWithRetry(context.Background(), cb, 3, 5*time.Millisecond, time.Second, IsTransientHTTPError,
			func(context.Context) (int, error) {
				n := tries.Add(1)
				if n < 3 {
					return 0, &HTTPStatusError{StatusCode: http.StatusServiceUnavailable}
				}
				return 42, nil
			})
		if err != nil || res != 42 || tries.Load() != 3 {
			t.Fatalf("expected success on 3rd attempt, res=%d err=%v tries=%d", res, err, tries.Load())
		}
	})

	t.Run("non-transient error stops immediately and does not trip breaker", func(t *testing.T) {
		cb := NewCircuitBreaker(1, time.Minute)
		var tries atomic.Int32
		badReq := &HTTPStatusError{StatusCode: http.StatusBadRequest, Body: "bad"}
		_, err := CallWithRetry(context.Background(), cb, 3, 5*time.Millisecond, time.Second, IsTransientHTTPError,
			func(context.Context) (string, error) {
				tries.Add(1)
				return "", badReq
			})
		if !errors.Is(err, badReq) || tries.Load() != 1 {
			t.Fatalf("expected immediate failure without retry, err=%v tries=%d", err, tries.Load())
		}
		// Breaker must remain closed.
		val, err := CallWithRetry(context.Background(), cb, 1, time.Millisecond, time.Second, IsTransientHTTPError,
			func(context.Context) (string, error) { return "ok", nil })
		if err != nil || val != "ok" {
			t.Fatalf("expected breaker still closed, val=%q err=%v", val, err)
		}
	})

	t.Run("respects context cancellation during backoff", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		var tries atomic.Int32
		_, err := CallWithRetry(ctx, nil, 5, 200*time.Millisecond, time.Second, IsTransientHTTPError,
			func(context.Context) (string, error) {
				if tries.Add(1) == 1 {
					go func() {
						time.Sleep(15 * time.Millisecond)
						cancel()
					}()
				}
				return "", &HTTPStatusError{StatusCode: http.StatusTooManyRequests}
			})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
		if tries.Load() != 1 {
			t.Errorf("expected cancel during backoff after 1 attempt, got %d", tries.Load())
		}
	})

	t.Run("attempt timeout triggers deadline exceeded and retries", func(t *testing.T) {
		var tries atomic.Int32
		res, err := CallWithRetry(context.Background(), nil, 2, 5*time.Millisecond, 25*time.Millisecond, IsTransientGRPCError,
			func(attemptCtx context.Context) (string, error) {
				if tries.Add(1) == 1 {
					<-attemptCtx.Done()
					return "", attemptCtx.Err()
				}
				return "fast", nil
			})
		if err != nil || res != "fast" || tries.Load() != 2 {
			t.Fatalf("expected retry after timeout to succeed, res=%q err=%v tries=%d", res, err, tries.Load())
		}
	})
}

func TestErrorClassifiersAndDefaults(t *testing.T) {
	// Defaults when <= 0
	cb := NewCircuitBreaker(0, 0)
	if cb.maxFailures != 5 || cb.resetTimeout != 15*time.Second {
		t.Errorf("unexpected defaults: maxFailures=%d resetTimeout=%v", cb.maxFailures, cb.resetTimeout)
	}

	// Nil HTTPStatusError
	var nilErr *HTTPStatusError
	if nilErr.Error() != "" {
		t.Errorf("expected empty string for nil HTTPStatusError, got %q", nilErr.Error())
	}
	if got := (&HTTPStatusError{StatusCode: http.StatusTooManyRequests}).Error(); got != http.StatusText(http.StatusTooManyRequests) {
		t.Errorf("unexpected Error(): %q", got)
	}

	// IsTransientGRPCError
	if IsTransientGRPCError(nil) {
		t.Error("nil should not be transient")
	}
	for _, err := range []error{
		context.DeadlineExceeded,
		status.Error(codes.Unavailable, "u"),
		status.Error(codes.DeadlineExceeded, "d"),
		status.Error(codes.ResourceExhausted, "r"),
	} {
		if !IsTransientGRPCError(err) {
			t.Errorf("expected %v to be transient gRPC error", err)
		}
	}
	for _, err := range []error{
		status.Error(codes.InvalidArgument, "i"),
		status.Error(codes.PermissionDenied, "p"),
		status.Error(codes.NotFound, "n"),
	} {
		if IsTransientGRPCError(err) {
			t.Errorf("expected %v NOT to be transient gRPC error", err)
		}
	}

	// IsTransientHTTPError
	if IsTransientHTTPError(nil) || IsTransientHTTPError(context.Canceled) {
		t.Error("nil and context.Canceled should not be transient HTTP errors")
	}
	for _, code := range []int{429, 500, 502, 503, 504, 529} {
		if !IsTransientHTTPError(&HTTPStatusError{StatusCode: code}) {
			t.Errorf("expected HTTP %d to be transient", code)
		}
	}
	for _, code := range []int{400, 401, 403, 404, 422} {
		if IsTransientHTTPError(&HTTPStatusError{StatusCode: code}) {
			t.Errorf("expected HTTP %d NOT to be transient", code)
		}
	}
	if !IsTransientHTTPError(errors.New("connection reset by peer")) {
		t.Error("generic network error should be transient")
	}
}
