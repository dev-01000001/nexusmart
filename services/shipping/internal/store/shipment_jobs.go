package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	JobStatusPending    = "pending"
	JobStatusProcessing = "processing"
	JobStatusSucceeded  = "succeeded"
	JobStatusFailed     = "failed"
	JobStatusCancelled  = "cancelled"
)

// ShipmentCreateJob represents an async retry job persisted when a carrier fails transiently
// (5xx/timeout/circuit-open) during forward CreateShipment (E18).
type ShipmentCreateJob struct {
	ID              string
	OrderID         string
	ShopID          string
	Direction       string
	CarrierCode     string
	ServiceCode     string
	ClientOrderCode string
	RequestJSON     []byte
	Status          string
	Attempts        int32
	MaxAttempts     int32
	NextRetryAt     time.Time
	ClaimedUntil    *time.Time
	ClaimToken      string
	LastError       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// JobOutboxParams carries a pre-built outbox event to persist atomically inside a job/shipment transaction.
type JobOutboxParams struct {
	EventID     string
	Subject     string
	AggregateID string
	Payload     []byte
	Traceparent string
	TraceID     string
}

const shipmentCreateJobColumns = `
	id, order_id, shop_id, direction, carrier_code, service_code,
	client_order_code, request_json, status, attempts, max_attempts,
	next_retry_at, claimed_until, claim_token, last_error, created_at, updated_at
`

const shipmentCreateJobReturningColumns = `
	j.id, j.order_id, j.shop_id, j.direction, j.carrier_code, j.service_code,
	j.client_order_code, j.request_json, j.status, j.attempts, j.max_attempts,
	j.next_retry_at, j.claimed_until, j.claim_token, j.last_error, j.created_at, j.updated_at
`

func scanShipmentCreateJob(row pgx.Row) (*ShipmentCreateJob, error) {
	var j ShipmentCreateJob
	err := row.Scan(
		&j.ID,
		&j.OrderID,
		&j.ShopID,
		&j.Direction,
		&j.CarrierCode,
		&j.ServiceCode,
		&j.ClientOrderCode,
		&j.RequestJSON,
		&j.Status,
		&j.Attempts,
		&j.MaxAttempts,
		&j.NextRetryAt,
		&j.ClaimedUntil,
		&j.ClaimToken,
		&j.LastError,
		&j.CreatedAt,
		&j.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &j, nil
}

// GetActiveShipmentCreateJob returns the active (pending|processing) retry job for (orderID, direction).
func (s *PGStore) GetActiveShipmentCreateJob(ctx context.Context, orderID, direction string) (*ShipmentCreateJob, error) {
	dir := NormalizeDirection(direction)
	return scanShipmentCreateJob(s.pool.QueryRow(ctx, `
		SELECT `+shipmentCreateJobColumns+`
		FROM shipment_create_jobs
		WHERE order_id = $1 AND direction = $2 AND status IN ('pending', 'processing')
		LIMIT 1
	`, orderID, dir))
}

// GetShipmentCreateJobByID fetches a shipment creation retry job by its UUID.
func (s *PGStore) GetShipmentCreateJobByID(ctx context.Context, jobID string) (*ShipmentCreateJob, error) {
	return scanShipmentCreateJob(s.pool.QueryRow(ctx, `
		SELECT `+shipmentCreateJobColumns+`
		FROM shipment_create_jobs
		WHERE id = $1
	`, jobID))
}

// EnqueueShipmentCreateJob inserts a new active retry job idempotently per (order_id, direction).
// If an active pending/processing job already exists, returns it with alreadyExisted=true.
func (s *PGStore) EnqueueShipmentCreateJob(ctx context.Context, job *ShipmentCreateJob) (*ShipmentCreateJob, bool, error) {
	dir := NormalizeDirection(job.Direction)
	if job.ID == "" {
		job.ID = uuid.NewString()
	}
	if job.Status == "" {
		job.Status = JobStatusPending
	}
	if job.MaxAttempts <= 0 {
		job.MaxAttempts = 8
	}
	now := job.CreatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	nextRetry := job.NextRetryAt
	if nextRetry.IsZero() {
		nextRetry = now
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("begin enqueue shipment job tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
		INSERT INTO shipment_create_jobs (
			id, order_id, shop_id, direction, carrier_code, service_code,
			client_order_code, request_json, status, attempts, max_attempts,
			next_retry_at, last_error, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9,$10,$11,$12,$13,$14,$15)
		ON CONFLICT (order_id, direction) WHERE status IN ('pending', 'processing') DO NOTHING
	`,
		job.ID, job.OrderID, job.ShopID, dir, job.CarrierCode, job.ServiceCode,
		job.ClientOrderCode, string(job.RequestJSON), job.Status, job.Attempts, job.MaxAttempts,
		nextRetry, job.LastError, now, now,
	)
	if err != nil {
		return nil, false, fmt.Errorf("insert shipment_create_job: %w", err)
	}
	if tag.RowsAffected() == 0 {
		existing, fetchErr := scanShipmentCreateJob(tx.QueryRow(ctx, `
			SELECT `+shipmentCreateJobColumns+`
			FROM shipment_create_jobs
			WHERE order_id = $1 AND direction = $2 AND status IN ('pending', 'processing')
			LIMIT 1
		`, job.OrderID, dir))
		if fetchErr != nil {
			return nil, false, fmt.Errorf("fetch active shipment_create_job on conflict: %w", fetchErr)
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return nil, false, fmt.Errorf("commit conflict shipment_create_job tx: %w", commitErr)
		}
		return existing, true, nil
	}

	saved, err := scanShipmentCreateJob(tx.QueryRow(ctx, `
		SELECT `+shipmentCreateJobColumns+`
		FROM shipment_create_jobs
		WHERE id = $1
	`, job.ID))
	if err != nil {
		return nil, false, fmt.Errorf("read inserted shipment_create_job: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("commit enqueue shipment_create_job tx: %w", err)
	}
	return saved, false, nil
}

// CancelActiveShipmentCreateJobs transitions any pending/processing job for (orderID, direction) to cancelled.
func (s *PGStore) CancelActiveShipmentCreateJobs(ctx context.Context, orderID, direction, reason string) (int64, error) {
	dir := NormalizeDirection(direction)
	msg := strings.TrimSpace(reason)
	if msg == "" {
		msg = "order cancelled"
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE shipment_create_jobs
		SET status = 'cancelled',
		    claimed_until = NULL,
		    claim_token = '',
		    last_error = $3,
		    updated_at = NOW()
		WHERE order_id = $1
		  AND direction = $2
		  AND status IN ('pending', 'processing')
	`, orderID, dir, msg)
	if err != nil {
		return 0, fmt.Errorf("cancel shipment_create_jobs: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ClaimDueShipmentCreateJobs claims a batch of due pending jobs (or expired processing leases)
// in a short transaction using FOR UPDATE SKIP LOCKED + lease token, committing before returning (E18).
func (s *PGStore) ClaimDueShipmentCreateJobs(ctx context.Context, limit int, lease time.Duration, now time.Time) ([]ShipmentCreateJob, string, error) {
	if limit <= 0 {
		limit = 20
	}
	if lease <= 0 {
		lease = 30 * time.Second
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	leaseMs := lease.Milliseconds()
	if leaseMs <= 0 {
		leaseMs = 1
	}
	claimToken := uuid.NewString()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("begin claim shipment jobs tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
		WITH candidate AS (
			SELECT id
			FROM shipment_create_jobs
			WHERE (status = 'pending' AND next_retry_at <= $1)
			   OR (status = 'processing' AND claimed_until IS NOT NULL AND claimed_until <= $1)
			ORDER BY next_retry_at ASC, created_at ASC
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		UPDATE shipment_create_jobs j
		SET status = 'processing',
		    claimed_until = $1 + ($3::bigint * INTERVAL '1 millisecond'),
		    claim_token = $4,
		    updated_at = $1
		FROM candidate c
		WHERE j.id = c.id
		RETURNING `+shipmentCreateJobReturningColumns, now, limit, leaseMs, claimToken)
	if err != nil {
		return nil, "", fmt.Errorf("claim due shipment jobs: %w", err)
	}

	var out []ShipmentCreateJob
	for rows.Next() {
		j, scanErr := scanShipmentCreateJob(rows)
		if scanErr != nil {
			rows.Close()
			return nil, "", fmt.Errorf("scan claimed shipment job: %w", scanErr)
		}
		out = append(out, *j)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		rows.Close()
		return nil, "", fmt.Errorf("iterate claimed shipment jobs: %w", rowsErr)
	}
	rows.Close()

	if err := tx.Commit(ctx); err != nil {
		return nil, "", fmt.Errorf("commit claim shipment jobs tx: %w", err)
	}
	return out, claimToken, nil
}

// CompleteShipmentCreateJob atomically verifies the job is still owned by claimToken (and was not cancelled),
// inserts the shipment + initial event + shipment.updated outbox event, and marks the job succeeded in ONE transaction.
// Returns ErrJobCancelled if order.cancelled/CancelShipment transitioned the job to cancelled while the carrier call was in-flight.
// Returns ErrStaleClaimToken if the lease was lost (re-claimed by another worker, already failed, or already completed).
func (s *PGStore) CompleteShipmentCreateJob(
	ctx context.Context,
	jobID, claimToken string,
	sh *Shipment,
	initialEvent *ShipmentEvent,
	outbox *JobOutboxParams,
) (*Shipment, bool, error) {
	sh.Direction = NormalizeDirection(sh.Direction)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("begin complete shipment job tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	job, err := scanShipmentCreateJob(tx.QueryRow(ctx, `
		SELECT `+shipmentCreateJobColumns+`
		FROM shipment_create_jobs
		WHERE id = $1
		FOR UPDATE
	`, jobID))
	if err != nil {
		return nil, false, err
	}
	if job.Status == JobStatusCancelled {
		return nil, false, ErrJobCancelled
	}
	if job.Status != JobStatusProcessing || claimToken == "" || job.ClaimToken != claimToken {
		return nil, false, ErrStaleClaimToken
	}

	tag, err := tx.Exec(ctx, `
		INSERT INTO shipments (
			id, order_id, shop_id, buyer_id, direction, return_request_id, redelivery_count,
			carrier_code, service_code, tracking_code, label_url,
			status, fee_vnd, cod_amount_vnd, weight_grams,
			from_province_code, from_ward_code, to_province_code, to_ward_code,
			created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)
		ON CONFLICT (order_id, direction) WHERE status <> 'cancelled' AND NOT (direction = 'RETURN' AND status = 'returned') DO NOTHING
	`,
		sh.ID, sh.OrderID, sh.ShopID, sh.BuyerID, sh.Direction, sh.ReturnRequestID, sh.RedeliveryCount,
		sh.CarrierCode, sh.ServiceCode, sh.TrackingCode, sh.LabelURL,
		sh.Status, sh.FeeVND, sh.CODAmountVND, sh.WeightGrams,
		sh.FromProvinceCode, sh.FromWardCode, sh.ToProvinceCode, sh.ToWardCode,
		sh.CreatedAt, sh.UpdatedAt,
	)
	if err != nil {
		return nil, false, fmt.Errorf("insert shipment in job completion: %w", err)
	}

	var saved *Shipment
	alreadyExisted := false
	if tag.RowsAffected() == 0 {
		alreadyExisted = true
		existing, fetchErr := scanShipment(tx.QueryRow(ctx, `
			SELECT `+shipmentColumns+`
			FROM shipments
			WHERE order_id = $1 AND direction = $2
			  AND status <> 'cancelled'
			  AND NOT (direction = 'RETURN' AND status = 'returned')
			ORDER BY created_at DESC
			LIMIT 1
		`, sh.OrderID, sh.Direction))
		if fetchErr != nil {
			return nil, false, fmt.Errorf("fetch existing shipment in job completion: %w", fetchErr)
		}
		saved = existing
	} else {
		saved = sh
		if initialEvent != nil {
			if _, err := tx.Exec(ctx, `
				INSERT INTO shipment_events (id, shipment_id, status, raw_status, description, occurred_at)
				VALUES ($1, $2, $3, $4, $5, $6)
			`, initialEvent.ID, sh.ID, initialEvent.Status, initialEvent.RawStatus, initialEvent.Description, initialEvent.OccurredAt); err != nil {
				return nil, false, fmt.Errorf("insert initial shipment event in job completion: %w", err)
			}
		}
		if outbox != nil && outbox.Subject != "" {
			aggID := strings.TrimSpace(outbox.AggregateID)
			if aggID == "" {
				aggID = strings.TrimSpace(sh.ID)
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO outbox_events (id, event_id, subject, aggregate_id, payload, traceparent, trace_id)
				VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7)
			`, uuid.NewString(), outbox.EventID, outbox.Subject, aggID, string(outbox.Payload), outbox.Traceparent, outbox.TraceID); err != nil {
				return nil, false, fmt.Errorf("insert shipment.updated outbox in job completion: %w", err)
			}
		}
	}

	if _, err := tx.Exec(ctx, `
		UPDATE shipment_create_jobs
		SET status = 'succeeded',
		    claimed_until = NULL,
		    claim_token = '',
		    last_error = '',
		    updated_at = NOW()
		WHERE id = $1 AND status = 'processing' AND claim_token = $2
	`, jobID, claimToken); err != nil {
		return nil, false, fmt.Errorf("mark shipment job succeeded: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("commit complete shipment job tx: %w", err)
	}
	return saved, alreadyExisted, nil
}

// CreateShipmentWithOutbox atomically persists a shipment, initial event, and an optional outbox event.
// Used when compensating carrier cancel fails transiently after a cancelled job so downstream order
// receives shipment.updated (created) and can run its orphan-shipment review/cancel path.
func (s *PGStore) CreateShipmentWithOutbox(
	ctx context.Context,
	sh *Shipment,
	initialEvent *ShipmentEvent,
	outbox *JobOutboxParams,
) (*Shipment, bool, error) {
	sh.Direction = NormalizeDirection(sh.Direction)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("begin create shipment with outbox tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
		INSERT INTO shipments (
			id, order_id, shop_id, buyer_id, direction, return_request_id, redelivery_count,
			carrier_code, service_code, tracking_code, label_url,
			status, fee_vnd, cod_amount_vnd, weight_grams,
			from_province_code, from_ward_code, to_province_code, to_ward_code,
			created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)
		ON CONFLICT (order_id, direction) WHERE status <> 'cancelled' AND NOT (direction = 'RETURN' AND status = 'returned') DO NOTHING
	`,
		sh.ID, sh.OrderID, sh.ShopID, sh.BuyerID, sh.Direction, sh.ReturnRequestID, sh.RedeliveryCount,
		sh.CarrierCode, sh.ServiceCode, sh.TrackingCode, sh.LabelURL,
		sh.Status, sh.FeeVND, sh.CODAmountVND, sh.WeightGrams,
		sh.FromProvinceCode, sh.FromWardCode, sh.ToProvinceCode, sh.ToWardCode,
		sh.CreatedAt, sh.UpdatedAt,
	)
	if err != nil {
		return nil, false, fmt.Errorf("insert shipment with outbox: %w", err)
	}
	if tag.RowsAffected() == 0 {
		existing, fetchErr := scanShipment(tx.QueryRow(ctx, `
			SELECT `+shipmentColumns+`
			FROM shipments
			WHERE order_id = $1 AND direction = $2
			  AND status <> 'cancelled'
			  AND NOT (direction = 'RETURN' AND status = 'returned')
			ORDER BY created_at DESC
			LIMIT 1
		`, sh.OrderID, sh.Direction))
		if fetchErr != nil {
			return nil, false, fetchErr
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return nil, false, commitErr
		}
		return existing, true, nil
	}

	if initialEvent != nil {
		if _, err := tx.Exec(ctx, `
			INSERT INTO shipment_events (id, shipment_id, status, raw_status, description, occurred_at)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, initialEvent.ID, sh.ID, initialEvent.Status, initialEvent.RawStatus, initialEvent.Description, initialEvent.OccurredAt); err != nil {
			return nil, false, fmt.Errorf("insert initial event with outbox: %w", err)
		}
	}
	if outbox != nil && outbox.Subject != "" {
		aggID := strings.TrimSpace(outbox.AggregateID)
		if aggID == "" {
			aggID = strings.TrimSpace(sh.ID)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO outbox_events (id, event_id, subject, aggregate_id, payload, traceparent, trace_id)
			VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7)
		`, uuid.NewString(), outbox.EventID, outbox.Subject, aggID, string(outbox.Payload), outbox.Traceparent, outbox.TraceID); err != nil {
			return nil, false, fmt.Errorf("insert outbox in create shipment: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("commit create shipment with outbox tx: %w", err)
	}
	return sh, false, nil
}

// RecordShipmentCreateJobFailure records a failed retry attempt guarded by claim_token.
// When terminal is true or attempts reaches max_attempts, transitions status to 'failed' and atomically
// writes shipment.create_failed to outbox_events (aggregate_id = jobID) under the job row lock (E18/D9-bis).
func (s *PGStore) RecordShipmentCreateJobFailure(
	ctx context.Context,
	jobID, claimToken, errMsg string,
	nextRetryAt *time.Time,
	terminal bool,
	outbox *JobOutboxParams,
	now time.Time,
) (*ShipmentCreateJob, bool, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("begin record job failure tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	job, err := scanShipmentCreateJob(tx.QueryRow(ctx, `
		SELECT `+shipmentCreateJobColumns+`
		FROM shipment_create_jobs
		WHERE id = $1
		FOR UPDATE
	`, jobID))
	if err != nil {
		return nil, false, err
	}
	if job.Status != JobStatusProcessing || job.ClaimToken != claimToken {
		return nil, false, nil
	}

	newAttempts := job.Attempts + 1
	isFinal := terminal || newAttempts >= job.MaxAttempts

	var updated *ShipmentCreateJob
	if isFinal {
		updated, err = scanShipmentCreateJob(tx.QueryRow(ctx, `
			UPDATE shipment_create_jobs
			SET status = 'failed',
			    attempts = $2,
			    claimed_until = NULL,
			    claim_token = '',
			    last_error = $3,
			    updated_at = $4
			WHERE id = $1
			RETURNING `+shipmentCreateJobColumns, jobID, newAttempts, errMsg, now))
		if err != nil {
			return nil, false, fmt.Errorf("mark shipment job failed: %w", err)
		}
		if outbox != nil && outbox.Subject != "" {
			aggID := strings.TrimSpace(outbox.AggregateID)
			if aggID == "" {
				aggID = jobID
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO outbox_events (id, event_id, subject, aggregate_id, payload, traceparent, trace_id)
				VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7)
			`, uuid.NewString(), outbox.EventID, outbox.Subject, aggID, string(outbox.Payload), outbox.Traceparent, outbox.TraceID); err != nil {
				return nil, false, fmt.Errorf("insert shipment.create_failed outbox: %w", err)
			}
		}
	} else {
		retryAt := now
		if nextRetryAt != nil {
			retryAt = *nextRetryAt
		}
		updated, err = scanShipmentCreateJob(tx.QueryRow(ctx, `
			UPDATE shipment_create_jobs
			SET status = 'pending',
			    attempts = $2,
			    next_retry_at = $3,
			    claimed_until = NULL,
			    claim_token = '',
			    last_error = $4,
			    updated_at = $5
			WHERE id = $1
			RETURNING `+shipmentCreateJobColumns, jobID, newAttempts, retryAt, errMsg, now))
		if err != nil {
			return nil, false, fmt.Errorf("reschedule shipment job: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("commit record job failure tx: %w", err)
	}
	return updated, true, nil
}
