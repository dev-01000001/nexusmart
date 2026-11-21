package resilience

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sony/gobreaker/v2"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestExecutorRetrySucceeds(t *testing.T) {
	exec := NewExecutor(Config{
		Name:           "test-dep",
		AttemptTimeout: 200 * time.Millisecond,
		MaxAttempts:    3,
		BaseBackoff:    5 * time.Millisecond,
		MaxBackoff:     20 * time.Millisecond,
		CBMaxFailures:  5,
		CBOpenTimeout:  time.Second,
		Logger:         quietLogger(),
	})

	var calls atomic.Int32
	got, err := Do(context.Background(), exec, "fetch", func(_ context.Context) (string, error) {
		if calls.Add(1) < 3 {
			return "", errors.New("transient failure")
		}
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("Do returned unexpected error: %v", err)
	}
	if got != "ok" {
		t.Fatalf("Do returned %q, want %q", got, "ok")
	}
	if calls.Load() != 3 {
		t.Fatalf("calls = %d, want 3", calls.Load())
	}
}

func TestExecutorNonRetryableDoesNotTripCircuitBreaker(t *testing.T) {
	exec := NewExecutor(Config{
		Name:           "test-nr",
		AttemptTimeout: 200 * time.Millisecond,
		MaxAttempts:    3,
		BaseBackoff:    5 * time.Millisecond,
		CBMaxFailures:  2,
		CBOpenTimeout:  time.Second,
		Logger:         quietLogger(),
	})

	sentinel := errors.New("not found")
	var calls atomic.Int32
	for i := 0; i < 4; i++ {
		err := exec.Run(context.Background(), "get", func(_ context.Context) error {
			calls.Add(1)
			return NonRetryable(sentinel)
		})
		if !errors.Is(err, sentinel) {
			t.Fatalf("expected sentinel error, got %v", err)
		}
	}
	if calls.Load() != 4 {
		t.Fatalf("expected 4 calls (no retries), got %d", calls.Load())
	}
	if exec.State() != gobreaker.StateClosed {
		t.Fatalf("circuit breaker state = %s, want closed", exec.State())
	}
}

func TestExecutorTripsCircuitBreaker(t *testing.T) {
	exec := NewExecutor(Config{
		Name:           "test-cb",
		AttemptTimeout: 100 * time.Millisecond,
		MaxAttempts:    1,
		BaseBackoff:    5 * time.Millisecond,
		CBMaxFailures:  2,
		CBOpenTimeout:  2 * time.Second,
		Logger:         quietLogger(),
	})

	for i := 0; i < 2; i++ {
		err := exec.Run(context.Background(), "call", func(_ context.Context) error {
			return errors.New("upstream 503")
		})
		if err == nil {
			t.Fatal("expected error")
		}
	}
	if exec.State() != gobreaker.StateOpen {
		t.Fatalf("state = %s, want open", exec.State())
	}

	var calls atomic.Int32
	err := exec.Run(context.Background(), "call", func(_ context.Context) error {
		calls.Add(1)
		return nil
	})
	if !errors.Is(err, gobreaker.ErrOpenState) {
		t.Fatalf("expected ErrOpenState, got %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("expected 0 calls while breaker open, got %d", calls.Load())
	}
}

func TestExecutorAttemptTimeout(t *testing.T) {
	exec := NewExecutor(Config{
		Name:           "test-timeout",
		AttemptTimeout: 25 * time.Millisecond,
		MaxAttempts:    1,
		CBMaxFailures:  5,
		Logger:         quietLogger(),
	})

	err := exec.Run(context.Background(), "slow", func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
}
