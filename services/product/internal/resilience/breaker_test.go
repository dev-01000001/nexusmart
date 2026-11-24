package resilience

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestCircuitBreakerTripsAndRecovers(t *testing.T) {
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	cb := NewCircuitBreaker(2, 10*time.Second)
	cb.SetClock(func() time.Time { return now })

	if !cb.Allow() {
		t.Fatal("expected initial closed breaker to allow")
	}
	cb.RecordFailure()
	if !cb.Allow() {
		t.Fatal("expected breaker to remain closed after 1/2 failures")
	}
	cb.RecordFailure()
	if cb.Allow() {
		t.Fatal("expected breaker to open after 2/2 failures")
	}

	now = now.Add(10 * time.Second)
	if !cb.Allow() {
		t.Fatal("expected half-open probe to be allowed after reset timeout")
	}
	if cb.Allow() {
		t.Fatal("expected second concurrent half-open probe to be rejected")
	}
	cb.RecordSuccess()
	if !cb.Allow() {
		t.Fatal("expected closed breaker after successful probe")
	}
}

func TestCallWithRetry_FastFailsWhenOpenAndDoesNotTripOnBusinessErrors(t *testing.T) {
	cb := NewCircuitBreaker(2, time.Minute)

	// Business error (PermissionDenied) must not retry or trip breaker.
	var calls atomic.Int32
	_, err := CallWithRetry(context.Background(), cb, 3, time.Millisecond, time.Second, func(_ context.Context) (string, error) {
		calls.Add(1)
		return "", status.Error(codes.PermissionDenied, "not owner")
	})
	if status.Code(err) != codes.PermissionDenied || calls.Load() != 1 {
		t.Fatalf("business error: calls=%d err=%v, want 1 PermissionDenied", calls.Load(), err)
	}

	// Two transient failures open the breaker.
	_, err = CallWithRetry(context.Background(), cb, 2, time.Millisecond, time.Second, func(_ context.Context) (string, error) {
		return "", status.Error(codes.Unavailable, "down")
	})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("transient error: want Unavailable, got %v", err)
	}

	// Next call fails immediately with ErrCircuitOpen without invoking fn.
	called := false
	_, err = CallWithRetry(context.Background(), cb, 2, time.Millisecond, time.Second, func(_ context.Context) (string, error) {
		called = true
		return "ok", nil
	})
	if !errors.Is(err, ErrCircuitOpen) || called {
		t.Fatalf("open breaker: called=%v err=%v, want called=false ErrCircuitOpen", called, err)
	}
}
