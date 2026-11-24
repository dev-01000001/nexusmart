package jetstream

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/nats-io/nats.go"
)

const (
	// DefaultOutboxLease is the default claim lease duration held by an outbox relay worker.
	DefaultOutboxLease = 30 * time.Second
	// DefaultOutboxPublishTimeout is the default maximum per-event JetStream PubAck timeout budget.
	DefaultOutboxPublishTimeout = 5 * time.Second
	// DefaultOutboxPollInterval is the default idle polling interval for outbox relays.
	DefaultOutboxPollInterval = 250 * time.Millisecond
	// DefaultOutboxBackoffInitial is the starting delay when a relay backs off after an error.
	DefaultOutboxBackoffInitial = 250 * time.Millisecond
	// DefaultOutboxBackoffMax is the maximum delay when a relay backs off after consecutive errors.
	DefaultOutboxBackoffMax = 5 * time.Second
	// OutboxPoisonLogAttempt is the minimum attempt count at which non-infra publish failures log at ERROR level.
	OutboxPoisonLogAttempt int32 = 10
)

// IsInfraError reports whether err represents a broker/transport infrastructure failure
// (NATS timeout, disconnect, no responders, context timeout/cancel, or network error)
// for which an outbox relay must abort the current batch, release remaining claims, and back off.
func IsInfraError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, nats.ErrTimeout) ||
		errors.Is(err, nats.ErrConnectionClosed) ||
		errors.Is(err, nats.ErrConnectionDraining) ||
		errors.Is(err, nats.ErrDrainTimeout) ||
		errors.Is(err, nats.ErrConnectionReconnecting) ||
		errors.Is(err, nats.ErrDisconnected) ||
		errors.Is(err, nats.ErrNoResponders) ||
		errors.Is(err, nats.ErrNoStreamResponse) ||
		errors.Is(err, nats.ErrNoServers) ||
		errors.Is(err, nats.ErrStaleConnection) ||
		errors.Is(err, nats.ErrReconnectBufExceeded) ||
		errors.Is(err, nats.ErrJetStreamNotEnabled) ||
		errors.Is(err, nats.ErrInvalidJSAck) {
		return true
	}
	var apiErr *nats.APIError
	if errors.As(err, &apiErr) && apiErr.Code >= 500 {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

// OutboxRetryBackoff returns min(2^attempts, 60) seconds for non-infrastructure outbox publish failures.
func OutboxRetryBackoff(attempts int32) time.Duration {
	if attempts <= 0 {
		return time.Second
	}
	if attempts >= 6 {
		return 60 * time.Second
	}
	return time.Duration(1<<attempts) * time.Second
}

// RunOutboxRelayLoop executes flush on pollInterval ticks and triggerCh notifications until ctx is cancelled.
// When flush returns a non-nil error, the loop backs off exponentially from DefaultOutboxBackoffInitial (250ms)
// up to DefaultOutboxBackoffMax (5s) and ignores triggerCh during backoff, resetting immediately on success.
func RunOutboxRelayLoop(ctx context.Context, pollInterval time.Duration, triggerCh <-chan struct{}, flush func(context.Context) error) {
	if pollInterval <= 0 {
		pollInterval = DefaultOutboxPollInterval
	}
	var backoff time.Duration
	timer := time.NewTimer(pollInterval)
	defer timer.Stop()

	for {
		if ctx.Err() != nil {
			return
		}

		if err := flush(ctx); err != nil {
			maxBackoff := DefaultOutboxBackoffMax
			if pollInterval > maxBackoff {
				maxBackoff = pollInterval
			}
			if backoff <= 0 {
				backoff = DefaultOutboxBackoffInitial
				if pollInterval > backoff {
					backoff = pollInterval
				}
			} else {
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
		} else {
			backoff = 0
		}

		if ctx.Err() != nil {
			return
		}

		wait := pollInterval
		if backoff > wait {
			wait = backoff
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(wait)

		if backoff > 0 {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			// Drop any trigger buffered during backoff before the next probe.
			if triggerCh != nil {
				select {
				case <-triggerCh:
				default:
				}
			}
		} else {
			select {
			case <-ctx.Done():
				return
			case <-triggerCh:
			case <-timer.C:
			}
		}
	}
}
