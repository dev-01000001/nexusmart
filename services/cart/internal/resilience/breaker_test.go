package resilience

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestCircuitBreakerTripsAndRecovers(t *testing.T) {
	cb := NewCircuitBreaker(2, 40*time.Millisecond)

	// Business error (NotFound) should NOT retry and should NOT trip circuit breaker
	calls := 0
	_, err := CallWithRetry(context.Background(), cb, 3, 5*time.Millisecond, time.Second, func(_ context.Context) (int, error) {
		calls++
		return 0, status.Error(codes.NotFound, "missing")
	})
	if status.Code(err) != codes.NotFound || calls != 1 {
		t.Fatalf("expected 1 call returning NotFound, got calls=%d err=%v", calls, err)
	}
	if !cb.Allow() {
		t.Fatalf("business error must not open circuit breaker")
	}

	// Two transient failures open the circuit breaker
	_, err = CallWithRetry(context.Background(), cb, 2, 5*time.Millisecond, time.Second, func(_ context.Context) (int, error) {
		return 0, status.Error(codes.Unavailable, "down")
	})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("expected Unavailable, got %v", err)
	}

	// Subsequent call immediately rejected with ErrCircuitOpen
	_, err = CallWithRetry(context.Background(), cb, 2, 5*time.Millisecond, time.Second, func(_ context.Context) (int, error) {
		t.Fatalf("fn should not be invoked while circuit is open")
		return 0, nil
	})
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen, got %v", err)
	}

	// Wait past resetTimeout -> half-open probe succeeds and closes circuit
	time.Sleep(50 * time.Millisecond)
	val, err := CallWithRetry(context.Background(), cb, 1, 5*time.Millisecond, time.Second, func(_ context.Context) (int, error) {
		return 42, nil
	})
	if err != nil || val != 42 || !cb.Allow() {
		t.Fatalf("expected half-open probe to succeed and close breaker, val=%d err=%v", val, err)
	}
}

func TestCircuitBreakerHalfOpenSingleProbe(t *testing.T) {
	cb := NewCircuitBreaker(1, 30*time.Millisecond)

	// Open the circuit
	_, err := CallWithRetry(context.Background(), cb, 1, 5*time.Millisecond, time.Second, func(_ context.Context) (int, error) {
		return 0, status.Error(codes.Unavailable, "down")
	})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("expected Unavailable, got %v", err)
	}

	// Wait for resetTimeout so breaker transitions to half-open
	time.Sleep(40 * time.Millisecond)

	const workers = 10
	probeEntered := make(chan struct{})
	releaseProbe := make(chan struct{})
	var enteredCount atomic.Int32
	var openErrCount atomic.Int32
	var succeededCount atomic.Int32

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, callErr := CallWithRetry(context.Background(), cb, 3, 5*time.Millisecond, time.Second, func(_ context.Context) (int, error) {
				if enteredCount.Add(1) == 1 {
					close(probeEntered)
				}
				<-releaseProbe
				return 1, nil
			})
			switch {
			case callErr == nil:
				succeededCount.Add(1)
			case errors.Is(callErr, ErrCircuitOpen):
				openErrCount.Add(1)
			default:
				t.Errorf("unexpected error: %v", callErr)
			}
		}()
	}

	close(start)
	<-probeEntered
	// Give other goroutines time to hit the half-open gate while probe is in flight
	time.Sleep(15 * time.Millisecond)
	close(releaseProbe)
	wg.Wait()

	if enteredCount.Load() != 1 || succeededCount.Load() != 1 || openErrCount.Load() != workers-1 {
		t.Fatalf("expected exactly 1 half-open probe and %d ErrCircuitOpen, got entered=%d succeeded=%d open=%d",
			workers-1, enteredCount.Load(), succeededCount.Load(), openErrCount.Load())
	}

	// Circuit is now closed again
	val, err := CallWithRetry(context.Background(), cb, 1, 5*time.Millisecond, time.Second, func(_ context.Context) (int, error) {
		return 7, nil
	})
	if err != nil || val != 7 {
		t.Fatalf("expected closed circuit after successful probe, val=%d err=%v", val, err)
	}
}
