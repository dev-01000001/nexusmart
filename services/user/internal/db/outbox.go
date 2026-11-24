package db

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nexusmart/core/pkg/common/jetstream"
)

func (s *SQLStore) outboxLogger() *slog.Logger {
	s.clockMu.RLock()
	l := s.logger
	s.clockMu.RUnlock()
	if l != nil {
		return l
	}
	return slog.Default()
}

func (s *SQLStore) outboxNow() time.Time {
	s.clockMu.RLock()
	fn := s.outboxNowFn
	s.clockMu.RUnlock()
	if fn == nil {
		return time.Now()
	}
	return fn()
}

func (s *SQLStore) outboxTimings() (lease, pubTimeout time.Duration) {
	return clampOutboxTimings(
		time.Duration(s.outboxLease.Load()),
		time.Duration(s.outboxPublishTimeout.Load()),
	)
}

func clampOutboxTimings(lease, pubTimeout time.Duration) (time.Duration, time.Duration) {
	if lease <= 0 {
		lease = jetstream.DefaultOutboxLease
	}
	if lease < 2*time.Millisecond {
		lease = 2 * time.Millisecond
	}
	if pubTimeout <= 0 {
		pubTimeout = jetstream.DefaultOutboxPublishTimeout
	}
	if pubTimeout >= lease {
		pubTimeout = lease / 2
	}
	return lease, pubTimeout
}

func outboxAggregateKey(ev OutboxEvent) string {
	if agg := strings.TrimSpace(ev.AggregateID); agg != "" {
		return agg
	}
	return ev.ID
}

func collectOutboxClaimIDs(skipped []string, remaining []OutboxEvent) []string {
	total := len(skipped) + len(remaining)
	if total == 0 {
		return nil
	}
	ids := make([]string, 0, total)
	ids = append(ids, skipped...)
	for _, ev := range remaining {
		ids = append(ids, ev.ID)
	}
	return ids
}

func (s *SQLStore) releaseOutboxClaimIDs(ctx context.Context, ids []string, claimToken string) {
	if len(ids) == 0 {
		return
	}
	relCtx, relCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer relCancel()
	_, _ = s.ReleaseOutboxEventClaims(relCtx, ReleaseOutboxEventClaimsParams{
		Ids:        ids,
		ClaimToken: claimToken,
	})
}

// ProcessPendingOutbox implements the lease-based transactional outbox relay pattern (matching order):
//  1. Claims a batch in a short TX ordered strictly by seq ASC with per-aggregate ordering, then commits.
//  2. Publishes each event outside any DB transaction within remaining lease budget and pubTimeout.
//  3. Marks published/failed guarded by claim_token and releases remaining claims on abort/lost lease.
func (s *SQLStore) ProcessPendingOutbox(ctx context.Context, limit int32, publishFn func(ctx context.Context, ev OutboxEvent) error) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	lease, pubTimeout := s.outboxTimings()
	leaseDeadline := s.outboxNow().Add(lease)

	leaseMs := lease.Milliseconds()
	if leaseMs <= 0 {
		leaseMs = 1
	}
	claimToken := uuid.NewString()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin outbox claim tx: %w", err)
	}
	rows, err := s.Queries.WithTx(tx).ClaimPendingOutboxEvents(ctx, ClaimPendingOutboxEventsParams{
		LeaseMs:    leaseMs,
		ClaimToken: claimToken,
		BatchLimit: limit,
	})
	if err != nil {
		_ = tx.Rollback(ctx)
		return 0, fmt.Errorf("claim pending outbox events: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit outbox claim tx: %w", err)
	}
	if len(rows) == 0 {
		return 0, nil
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Seq != rows[j].Seq {
			return rows[i].Seq < rows[j].Seq
		}
		return rows[i].ID < rows[j].ID
	})

	published := 0
	failedAggs := make(map[string]struct{})
	var skippedIDs []string
	for i, ev := range rows {
		if ctx.Err() != nil {
			s.releaseOutboxClaimIDs(ctx, collectOutboxClaimIDs(skippedIDs, rows[i:]), claimToken)
			return published, ctx.Err()
		}
		if leaseDeadline.Sub(s.outboxNow()) < pubTimeout {
			s.releaseOutboxClaimIDs(ctx, collectOutboxClaimIDs(skippedIDs, rows[i:]), claimToken)
			return published, nil
		}

		aggKey := outboxAggregateKey(ev)
		if _, blocked := failedAggs[aggKey]; blocked {
			skippedIDs = append(skippedIDs, ev.ID)
			continue
		}

		pubCtx, pubCancel := context.WithTimeout(ctx, pubTimeout)
		pubErr := publishFn(pubCtx, ev)
		pubCancel()
		if pubErr != nil {
			isInfra := jetstream.IsInfraError(pubErr)
			failCtx, failCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			newAttempts, failErr := s.MarkOutboxEventAttemptFailed(failCtx, MarkOutboxEventAttemptFailedParams{
				ID:           ev.ID,
				LastError:    pubErr.Error(),
				ClaimToken:   claimToken,
				ApplyBackoff: !isInfra,
			})
			failCancel()
			if isInfra {
				releaseFrom := rows[i+1:]
				if failErr != nil {
					s.outboxLogger().WarnContext(ctx, "failed to record infra outbox failure; releasing current claim too",
						"event_id", ev.ID, "error", failErr)
					releaseFrom = rows[i:]
				}
				s.releaseOutboxClaimIDs(ctx, collectOutboxClaimIDs(skippedIDs, releaseFrom), claimToken)
				return published, pubErr
			}
			if errors.Is(failErr, pgx.ErrNoRows) {
				s.releaseOutboxClaimIDs(ctx, collectOutboxClaimIDs(skippedIDs, rows[i+1:]), claimToken)
				return published, nil
			}
			if failErr != nil {
				s.releaseOutboxClaimIDs(ctx, collectOutboxClaimIDs(skippedIDs, rows[i+1:]), claimToken)
				return published, fmt.Errorf("mark outbox event attempt failed: %w", failErr)
			}
			if newAttempts >= jetstream.OutboxPoisonLogAttempt {
				s.outboxLogger().ErrorContext(ctx, "outbox event repeatedly failing publish",
					"event_id", ev.ID,
					"event_type", ev.EventType,
					"attempts", newAttempts,
					"error", pubErr,
				)
			}
			failedAggs[aggKey] = struct{}{}
			continue
		}

		ackCtx, ackCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		affected, markErr := s.MarkOutboxEventPublished(ackCtx, MarkOutboxEventPublishedParams{
			ID:         ev.ID,
			ClaimToken: claimToken,
		})
		ackCancel()
		if markErr != nil {
			s.releaseOutboxClaimIDs(ctx, collectOutboxClaimIDs(skippedIDs, rows[i+1:]), claimToken)
			return published, fmt.Errorf("mark outbox event published: %w", markErr)
		}
		if affected == 0 {
			s.releaseOutboxClaimIDs(ctx, collectOutboxClaimIDs(skippedIDs, rows[i+1:]), claimToken)
			return published, nil
		}
		published++
	}

	if len(skippedIDs) > 0 {
		s.releaseOutboxClaimIDs(ctx, skippedIDs, claimToken)
	}
	return published, nil
}
