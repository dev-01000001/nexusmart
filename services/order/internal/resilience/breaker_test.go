package resilience

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestCircuitBreaker_TripsHalfOpenAndRecovers(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	cb := NewCircuitBreaker(2, 10*time.Second)
	cb.nowFn = func() time.Time { return now }

	// Non-transient business error (PermissionDenied) must NOT retry or trip breaker.
	calls := 0
	_, err := CallWithRetry(context.Background(), cb, 3, time.Millisecond, time.Second, func(context.Context) (string, error) {
		calls++
		return "", status.Error(codes.PermissionDenied, "denied")
	})
	if status.Code(err) != codes.PermissionDenied || calls != 1 {
		t.Fatalf("business error: calls=%d err=%v, want calls=1 PermissionDenied", calls, err)
	}

	// Two consecutive transient failures trip the breaker.
	_, err = CallWithRetry(context.Background(), cb, 2, time.Millisecond, time.Second, func(context.Context) (string, error) {
		return "", status.Error(codes.Unavailable, "down")
	})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("expected Unavailable, got %v", err)
	}

	// Open breaker short-circuits locally without calling fn.
	calledWhileOpen := false
	_, err = CallWithRetry(context.Background(), cb, 2, time.Millisecond, time.Second, func(context.Context) (string, error) {
		calledWhileOpen = true
		return "ok", nil
	})
	if !errors.Is(err, ErrCircuitOpen) || calledWhileOpen {
		t.Fatalf("open breaker: called=%v err=%v, want called=false ErrCircuitOpen", calledWhileOpen, err)
	}

	// Advance past resetTimeout -> single half-open probe succeeds and closes breaker.
	now = now.Add(11 * time.Second)
	res, err := CallWithRetry(context.Background(), cb, 2, time.Millisecond, time.Second, func(context.Context) (string, error) {
		return "recovered", nil
	})
	if err != nil || res != "recovered" {
		t.Fatalf("half-open recovery: res=%q err=%v, want recovered/nil", res, err)
	}
}

func TestCircuitBreaker_AttemptTimeoutTrips_ParentCancelDoesNotTrip(t *testing.T) {
	cb := NewCircuitBreaker(1, time.Minute)

	// Parent cancel must NOT trip the circuit breaker.
	parentCtx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := CallWithRetry(parentCtx, cb, 1, time.Millisecond, time.Second, func(attemptCtx context.Context) (string, error) {
		return "", attemptCtx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("parent cancel: got %v, want context.Canceled", err)
	}
	if !cb.Allow() {
		t.Fatalf("parent cancellation must not trip circuit breaker")
	}

	// Per-attempt timeout while parent is alive MUST trip the circuit breaker.
	_, err = CallWithRetry(context.Background(), cb, 1, time.Millisecond, 15*time.Millisecond, func(attemptCtx context.Context) (string, error) {
		<-attemptCtx.Done()
		return "", attemptCtx.Err()
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("attempt timeout: got %v, want DeadlineExceeded", err)
	}
	if cb.Allow() {
		t.Fatalf("attempt timeout with maxFailures=1 must open circuit breaker")
	}
}
