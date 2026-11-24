package jetstream

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

type testNetTimeoutErr struct{}

func (testNetTimeoutErr) Error() string   { return "i/o timeout" }
func (testNetTimeoutErr) Timeout() bool   { return true }
func (testNetTimeoutErr) Temporary() bool { return true }

var _ net.Error = testNetTimeoutErr{}

func TestIsInfraError(t *testing.T) {
	infraCases := []error{
		context.DeadlineExceeded,
		context.Canceled,
		nats.ErrTimeout,
		nats.ErrConnectionClosed,
		nats.ErrConnectionDraining,
		nats.ErrDrainTimeout,
		nats.ErrConnectionReconnecting,
		nats.ErrDisconnected,
		nats.ErrNoResponders,
		nats.ErrNoStreamResponse,
		nats.ErrNoServers,
		nats.ErrStaleConnection,
		nats.ErrReconnectBufExceeded,
		nats.ErrJetStreamNotEnabled,
		nats.ErrInvalidJSAck,
		&nats.APIError{Code: 500, Description: "internal server error"},
		&nats.APIError{Code: 503, Description: "no responders / stream offline"},
		fmt.Errorf("wrapped api 503: %w", &nats.APIError{Code: 503, Description: "service unavailable"}),
		fmt.Errorf("wrapped timeout: %w", nats.ErrTimeout),
		fmt.Errorf("wrapped net err: %w", testNetTimeoutErr{}),
	}
	for _, err := range infraCases {
		if !IsInfraError(err) {
			t.Fatalf("IsInfraError(%v) = false, want true", err)
		}
	}

	nonInfraCases := []error{
		nil,
		errors.New("marshal envelope failed"),
		errors.New("invalid payload"),
		&nats.APIError{Code: 400, Description: "bad request"},
		&nats.APIError{Code: 404, Description: "stream not found"},
	}
	for _, err := range nonInfraCases {
		if IsInfraError(err) {
			t.Fatalf("IsInfraError(%v) = true, want false", err)
		}
	}
}

func TestOutboxRetryBackoff(t *testing.T) {
	cases := []struct {
		attempts int32
		want     time.Duration
	}{
		{0, time.Second},
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{3, 8 * time.Second},
		{4, 16 * time.Second},
		{5, 32 * time.Second},
		{6, 60 * time.Second},
		{10, 60 * time.Second},
		{100, 60 * time.Second},
	}
	for _, tc := range cases {
		if got := OutboxRetryBackoff(tc.attempts); got != tc.want {
			t.Fatalf("OutboxRetryBackoff(%d) = %v, want %v", tc.attempts, got, tc.want)
		}
	}
}

func TestRunOutboxRelayLoop_BackoffIgnoresTriggerAndResetsOnSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	trig := make(chan struct{}, 1)
	var failFlag atomic.Bool
	failFlag.Store(true)
	var calls atomic.Int32
	recoveredCh := make(chan struct{}, 1)

	done := make(chan struct{})
	go func() {
		defer close(done)
		RunOutboxRelayLoop(ctx, 25*time.Millisecond, trig, func(_ context.Context) error {
			calls.Add(1)
			if failFlag.Load() {
				return nats.ErrDisconnected
			}
			select {
			case recoveredCh <- struct{}{}:
			default:
			}
			return nil
		})
	}()

	// Hammer triggerCh every 20ms for 1.1s while failing: backoff 250ms -> 500ms -> 1s must cap calls.
	deadline := time.Now().Add(1100 * time.Millisecond)
	for time.Now().Before(deadline) {
		select {
		case trig <- struct{}{}:
		default:
		}
		time.Sleep(20 * time.Millisecond)
	}

	failCalls := calls.Load()
	if failCalls < 1 || failCalls > 5 {
		cancel()
		<-done
		t.Fatalf("during 1.1s infra failure with 20ms triggers: expected 1..5 backed-off calls, got %d", failCalls)
	}

	// Recover -> wait for the next probe to succeed and reset backoff, then triggerCh wakes promptly.
	failFlag.Store(false)
	select {
	case <-recoveredCh:
	case <-time.After(3 * time.Second):
		cancel()
		<-done
		t.Fatalf("relay loop did not recover after errors cleared")
	}

	beforeTrig := calls.Load()
	select {
	case trig <- struct{}{}:
	default:
	}
	waitStart := time.Now()
	for calls.Load() == beforeTrig && time.Since(waitStart) < 500*time.Millisecond {
		time.Sleep(10 * time.Millisecond)
	}
	if calls.Load() <= beforeTrig {
		cancel()
		<-done
		t.Fatalf("expected triggerCh to wake relay promptly after recovery")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("RunOutboxRelayLoop did not exit promptly on cancel")
	}
}

func TestRunOutboxRelayLoop_SlowPollIntervalNeverPollsFasterOnError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 900*time.Millisecond)
	defer cancel()

	trig := make(chan struct{}, 1)
	var calls atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		// pollInterval (2s) > DefaultOutboxBackoffInitial (250ms): on error, wait must be >= 2s (max(poll, backoff)).
		RunOutboxRelayLoop(ctx, 2*time.Second, trig, func(_ context.Context) error {
			calls.Add(1)
			return nats.ErrDisconnected
		})
	}()

	tk := time.NewTicker(20 * time.Millisecond)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			<-done
			if got := calls.Load(); got != 1 {
				t.Fatalf("with pollInterval=2s and error over 900ms: want 1 call (never faster than pollInterval), got %d", got)
			}
			return
		case <-tk.C:
			select {
			case trig <- struct{}{}:
			default:
			}
		}
	}
}
