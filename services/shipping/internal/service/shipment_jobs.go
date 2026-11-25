package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/shipping/internal/carrier"
	"github.com/nexusmart/core/services/shipping/internal/events"
	"github.com/nexusmart/core/services/shipping/internal/store"
)

// CreateShipmentOutcome holds the result of CreateShipmentWithQueue (immediate shipment or async queued retry job).
type CreateShipmentOutcome struct {
	Shipment       *store.Shipment
	AlreadyExisted bool
	Queued         bool
	JobID          string
}

// CreateShipmentWithQueue creates a FORWARD shipment idempotently, or persists a shipment_create_jobs retry job
// when the carrier fails transiently (5xx/timeout/circuit-open) while returning carrier 4xx rejections immediately (E18).
func (s *ShippingService) CreateShipmentWithQueue(ctx context.Context, in CreateShipmentInput) (*CreateShipmentOutcome, error) {
	return s.createForwardShipment(ctx, in, true)
}

func (s *ShippingService) createForwardShipment(ctx context.Context, in CreateShipmentInput, enqueueOnTransient bool) (*CreateShipmentOutcome, error) {
	in.OrderID = strings.TrimSpace(in.OrderID)
	in.ShopID = strings.TrimSpace(in.ShopID)
	in.BuyerID = strings.TrimSpace(in.BuyerID)
	if in.OrderID == "" {
		return nil, fmt.Errorf("%w: order_id is required", ErrInvalidArgument)
	}
	if in.ShopID == "" {
		return nil, fmt.Errorf("%w: shop_id is required", ErrInvalidArgument)
	}
	if in.CODAmountVND < 0 {
		return nil, fmt.Errorf("%w: cod_amount_vnd must be >= 0", ErrInvalidArgument)
	}
	if in.WeightGrams <= 0 {
		in.WeightGrams = s.resolveDefaultWeight(ctx, in.ShopID)
	}
	allowJob := enqueueOnTransient || in.EnqueueOnly

	// Fast-path idempotency: existing active FORWARD shipment or active retry job.
	if existing, err := s.repo.GetActiveShipmentByOrderID(ctx, in.OrderID, store.DirectionForward); err == nil && existing != nil {
		return &CreateShipmentOutcome{Shipment: existing, AlreadyExisted: true}, nil
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, fmt.Errorf("check existing shipment: %w", err)
	}
	if allowJob {
		if job, err := s.repo.GetActiveShipmentCreateJob(ctx, in.OrderID, store.DirectionForward); err == nil && job != nil {
			return &CreateShipmentOutcome{Queued: true, JobID: job.ID}, nil
		} else if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("check existing shipment create job: %w", err)
		}
	}

	// Serialize check -> carrier create -> DB insert/job-enqueue per (order_id, FORWARD) across replicas.
	unlock, err := s.repo.AcquireOrderDirectionLock(ctx, in.OrderID, store.DirectionForward)
	if err != nil {
		return nil, fmt.Errorf("%w: lock forward shipment creation: %w", ErrCarrierUnavailable, err)
	}
	defer unlock()

	// Re-check both active shipment and active job under advisory lock.
	if existing, err := s.repo.GetActiveShipmentByOrderID(ctx, in.OrderID, store.DirectionForward); err == nil && existing != nil {
		return &CreateShipmentOutcome{Shipment: existing, AlreadyExisted: true}, nil
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, fmt.Errorf("recheck existing shipment: %w", err)
	}
	if allowJob {
		if job, err := s.repo.GetActiveShipmentCreateJob(ctx, in.OrderID, store.DirectionForward); err == nil && job != nil {
			return &CreateShipmentOutcome{Queued: true, JobID: job.ID}, nil
		} else if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("recheck existing shipment create job: %w", err)
		}
	}

	_, _, _ = s.loadCachedConfigs(ctx)

	provider, err := s.resolveForwardCarrier(ctx, in.CarrierCode)
	if err != nil {
		return nil, err
	}

	inactiveCount, err := s.repo.CountInactiveShipmentsByOrder(ctx, in.OrderID, store.DirectionForward)
	if err != nil {
		return nil, fmt.Errorf("count inactive forward shipments: %w", err)
	}
	clientOrderCode := in.OrderID
	if inactiveCount > 0 {
		clientOrderCode = fmt.Sprintf("%s-R%d", in.OrderID, inactiveCount)
	}

	s.enrichGHNAddress(ctx, &in.FromAddress)
	s.enrichGHNAddress(ctx, &in.ToAddress)

	// G4 (UC-S15): Validate non-retryable preconditions and enqueue directly without calling carrier synchronously.
	if in.EnqueueOnly {
		if err := s.validateEnqueuePreconditions(ctx, provider, in); err != nil {
			return nil, err
		}
		return s.enqueueShipmentCreateJob(ctx, in, provider.Code(), clientOrderCode, s.nowFn(), "")
	}

	res, err := provider.CreateShipment(ctx, carrier.ShipmentRequest{
		OrderID:         in.OrderID,
		ClientOrderCode: clientOrderCode,
		ShopID:          in.ShopID,
		ServiceCode:     in.ServiceCode,
		CODAmountVND:    in.CODAmountVND,
		WeightGrams:     in.WeightGrams,
		FromAddress:     in.FromAddress,
		ToAddress:       in.ToAddress,
		ItemsSummary:    in.ItemsSummary,
	})
	if err != nil {
		if enqueueOnTransient && s.isTransientCreateCarrierError(ctx, err) {
			return s.enqueueShipmentCreateJob(ctx, in, provider.Code(), clientOrderCode, s.nowFn().Add(s.createRetryBackoff(0)), err.Error())
		}
		return nil, s.classifyCarrierCreateError(ctx, provider.Code(), err)
	}

	sh, initEv := s.buildCreatedForwardShipment(in, res)
	saved, alreadyExisted, err := s.repo.CreateShipment(ctx, sh, initEv)
	if err != nil {
		return nil, fmt.Errorf("persist shipment: %w", err)
	}
	return &CreateShipmentOutcome{Shipment: saved, AlreadyExisted: alreadyExisted}, nil
}

func (s *ShippingService) resolveForwardCarrier(ctx context.Context, rawCode string) (carrier.Carrier, error) {
	reqCarrier := strings.ToLower(strings.TrimSpace(rawCode))
	if reqCarrier != "" && reqCarrier != carrier.CodeFallback {
		provider, ok := s.aggregator.Get(reqCarrier)
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrUnknownCarrier, reqCarrier)
		}
		return provider, nil
	}
	return s.defaultEnabledCarrier(ctx)
}

func (s *ShippingService) isTransientCreateCarrierError(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	// Do not enqueue when the caller explicitly cancelled the request.
	if errors.Is(ctx.Err(), context.Canceled) {
		return false
	}
	// 401/403 auth misconfiguration and 4xx validation rejections fail immediately with no retry job.
	if errors.Is(err, carrier.ErrInvalidCarrierToken) || carrier.IsNonRetryable(err) {
		return false
	}
	// 5xx, timeouts, network errors, and open circuit breaker are transient and queued for retry.
	return true
}

func (s *ShippingService) validateEnqueuePreconditions(ctx context.Context, provider carrier.Carrier, in CreateShipmentInput) error {
	if provider.Code() == carrier.CodeGHN && !provider.IsMock() {
		if in.ToAddress.GHNDistrictID <= 0 || strings.TrimSpace(in.ToAddress.GHNWardCode) == "" {
			return s.classifyCarrierCreateError(ctx, provider.Code(), &carrier.NonRetryableError{
				Err: fmt.Errorf("%w (to_ward_code=%s)", carrier.ErrMissingGHNMapping, in.ToAddress.WardCode),
			})
		}
	}
	return nil
}

func (s *ShippingService) enqueueShipmentCreateJob(
	ctx context.Context,
	in CreateShipmentInput,
	carrierCode string,
	clientOrderCode string,
	nextRetryAt time.Time,
	lastError string,
) (*CreateShipmentOutcome, error) {
	inCopy := in
	inCopy.CarrierCode = carrierCode
	inCopy.EnqueueOnly = false
	reqJSON, err := json.Marshal(inCopy)
	if err != nil {
		return nil, fmt.Errorf("marshal shipment create job request: %w", err)
	}

	now := s.nowFn()
	if nextRetryAt.IsZero() {
		nextRetryAt = now
	}
	enqueueCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	job, _, err := s.repo.EnqueueShipmentCreateJob(enqueueCtx, &store.ShipmentCreateJob{
		ID:              uuid.NewString(),
		OrderID:         in.OrderID,
		ShopID:          in.ShopID,
		Direction:       store.DirectionForward,
		CarrierCode:     carrierCode,
		ServiceCode:     in.ServiceCode,
		ClientOrderCode: clientOrderCode,
		RequestJSON:     reqJSON,
		Status:          store.JobStatusPending,
		Attempts:        0,
		MaxAttempts:     s.createRetryMax,
		NextRetryAt:     nextRetryAt,
		LastError:       lastError,
		CreatedAt:       now,
		UpdatedAt:       now,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: persist shipment retry job: %w", ErrCarrierUnavailable, err)
	}

	if lastError != "" {
		s.logger.WarnContext(ctx, "carrier transient error on CreateShipment; queued async retry job",
			slog.String("job_id", job.ID),
			slog.String("order_id", in.OrderID),
			slog.String("carrier", carrierCode),
			slog.String("error", lastError),
		)
	}
	return &CreateShipmentOutcome{Queued: true, JobID: job.ID}, nil
}

func (s *ShippingService) buildCreatedForwardShipment(in CreateShipmentInput, res *carrier.ShipmentResult) (*store.Shipment, *store.ShipmentEvent) {
	now := s.nowFn()
	sh := &store.Shipment{
		ID:                uuid.NewString(),
		OrderID:           in.OrderID,
		ShopID:            in.ShopID,
		BuyerID:           in.BuyerID,
		Direction:         store.DirectionForward,
		CarrierCode:       res.CarrierCode,
		ServiceCode:       res.ServiceCode,
		TrackingCode:      res.TrackingCode,
		LabelURL:          res.LabelURL,
		Status:            carrier.StatusCreated,
		FeeVND:            res.FeeVND,
		CODAmountVND:      in.CODAmountVND,
		WeightGrams:       in.WeightGrams,
		FromName:          strings.TrimSpace(in.FromAddress.Name),
		FromPhone:         strings.TrimSpace(in.FromAddress.Phone),
		FromStreetAddress: strings.TrimSpace(in.FromAddress.StreetAddress),
		FromProvinceCode:  in.FromAddress.ProvinceCode,
		FromWardCode:      in.FromAddress.WardCode,
		ToName:            strings.TrimSpace(in.ToAddress.Name),
		ToPhone:           strings.TrimSpace(in.ToAddress.Phone),
		ToStreetAddress:   strings.TrimSpace(in.ToAddress.StreetAddress),
		ToProvinceCode:    in.ToAddress.ProvinceCode,
		ToWardCode:        in.ToAddress.WardCode,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	initEv := &store.ShipmentEvent{
		ID:          uuid.NewString(),
		ShipmentID:  sh.ID,
		Status:      carrier.StatusCreated,
		RawStatus:   carrier.StatusCreated,
		Description: carrier.StatusDescriptionVI(carrier.StatusCreated),
		OccurredAt:  now.Truncate(time.Second),
	}
	return sh, initEv
}

// createRetryBackoff computes exponential backoff starting at createRetryBase (2s) capped at createRetryMaxBackoff (5m).
func (s *ShippingService) createRetryBackoff(failedAttempts int32) time.Duration {
	base := s.createRetryBase
	if base <= 0 {
		base = defaultCreateRetryBase
	}
	maxD := s.createRetryMaxBackoff
	if maxD <= 0 {
		maxD = defaultCreateRetryMaxBackof
	}
	if maxD < base {
		maxD = base
	}
	delay := base
	for i := int32(0); i < failedAttempts; i++ {
		if delay >= maxD/2 {
			return maxD
		}
		delay *= 2
	}
	if delay > maxD {
		return maxD
	}
	return delay
}

// ProcessDueShipmentCreateJobs claims at most one due shipment_create_job per iteration (up to limit)
// using FOR UPDATE SKIP LOCKED and executes each job under its own per-job timeout strictly shorter than the lease (E18).
func (s *ShippingService) ProcessDueShipmentCreateJobs(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = 20
	}
	processed := 0
	for i := 0; i < limit; i++ {
		if ctx.Err() != nil {
			return processed, ctx.Err()
		}
		jobs, claimToken, err := s.repo.ClaimDueShipmentCreateJobs(ctx, 1, s.jobLease, s.nowFn())
		if err != nil {
			return processed, err
		}
		if len(jobs) == 0 {
			break
		}

		jobCtx, jobCancel := context.WithTimeout(ctx, s.effectiveJobExecTimeout())
		execErr := s.executeClaimedShipmentCreateJob(jobCtx, jobs[0], claimToken)
		jobCancel()

		if execErr != nil && ctx.Err() == nil {
			s.logger.WarnContext(ctx, "execute shipment create job error",
				slog.String("job_id", jobs[0].ID),
				slog.String("order_id", jobs[0].OrderID),
				slog.String("error", execErr.Error()),
			)
		}
		processed++
		if ctx.Err() != nil {
			return processed, ctx.Err()
		}
	}
	return processed, nil
}

func (s *ShippingService) executeClaimedShipmentCreateJob(ctx context.Context, job store.ShipmentCreateJob, claimToken string) error {
	var in CreateShipmentInput
	if err := json.Unmarshal(job.RequestJSON, &in); err != nil {
		return s.recordShipmentJobFailure(ctx, job, claimToken, fmt.Errorf("corrupt shipment job payload: %w", err), true)
	}

	unlock, err := s.repo.AcquireOrderDirectionLock(ctx, job.OrderID, job.Direction)
	if err != nil {
		// Transient lock contention/failure: let lease expire or retry on next tick without burning attempts.
		return fmt.Errorf("acquire lock for job %s: %w", job.ID, err)
	}
	defer unlock()

	// Re-check both job status AND claim_token under advisory lock so cancelled, failed, completed,
	// or lease-stolen jobs never call the carrier with a stale token.
	curJob, err := s.repo.GetShipmentCreateJobByID(ctx, job.ID)
	if err != nil {
		return err
	}
	if curJob.Status != store.JobStatusProcessing || curJob.ClaimToken != claimToken {
		return nil
	}

	// If an active shipment already exists for (order_id, direction), mark job succeeded without calling carrier again.
	if existing, err := s.repo.GetActiveShipmentByOrderID(ctx, job.OrderID, job.Direction); err == nil && existing != nil {
		_, _, compErr := s.repo.CompleteShipmentCreateJob(ctx, job.ID, claimToken, existing, nil, nil)
		if errors.Is(compErr, store.ErrJobCancelled) || errors.Is(compErr, store.ErrStaleClaimToken) {
			return nil
		}
		return compErr
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}

	_, _, _ = s.loadCachedConfigs(ctx)
	provider, err := s.resolveForwardCarrier(ctx, job.CarrierCode)
	if err != nil {
		return s.recordShipmentJobFailure(ctx, *curJob, claimToken, err, true)
	}

	s.enrichGHNAddress(ctx, &in.FromAddress)
	s.enrichGHNAddress(ctx, &in.ToAddress)

	res, err := provider.CreateShipment(ctx, carrier.ShipmentRequest{
		OrderID:         in.OrderID,
		ClientOrderCode: job.ClientOrderCode, // Immutable per job so carrier deduplicates retries.
		ShopID:          in.ShopID,
		ServiceCode:     in.ServiceCode,
		CODAmountVND:    in.CODAmountVND,
		WeightGrams:     in.WeightGrams,
		FromAddress:     in.FromAddress,
		ToAddress:       in.ToAddress,
		ItemsSummary:    in.ItemsSummary,
	})
	if err != nil {
		// If OUR worker/per-job context expired (shutdown or tick timeout), do NOT burn a retry attempt.
		// Genuine carrier timeouts inside ResilientCarrier (attemptCtx) leave ctx.Err() == nil and STILL count.
		if ctx.Err() != nil {
			return ctx.Err()
		}
		terminal := carrier.IsNonRetryable(err) && !errors.Is(err, carrier.ErrInvalidCarrierToken)
		return s.recordShipmentJobFailure(ctx, *curJob, claimToken, err, terminal)
	}

	sh, initEv := s.buildCreatedForwardShipment(in, res)
	outbox, err := s.buildShipmentCreatedOutbox(ctx, sh)
	if err != nil {
		return err
	}

	compCtx, compCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer compCancel()

	_, alreadyExisted, compErr := s.repo.CompleteShipmentCreateJob(compCtx, job.ID, claimToken, sh, initEv, outbox)
	if errors.Is(compErr, store.ErrStaleClaimToken) {
		// Lease expired and job was re-claimed, failed, or already completed by another worker:
		// write nothing, emit no event, and do NOT cancel carrier shipment (immutable client_order_code
		// allows the current token holder to adopt it).
		s.logger.WarnContext(ctx, "shipment create job lost claim_token before completion; dropping stale result",
			slog.String("job_id", job.ID),
			slog.String("order_id", job.OrderID),
		)
		return nil
	}
	if errors.Is(compErr, store.ErrJobCancelled) {
		// E18 race compensation: order was cancelled while carrier.CreateShipment was in-flight.
		// Cancel the newly created carrier shipment immediately so no orphan remains at the carrier.
		cancelCtx, cancelCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		cancelErr := provider.CancelShipment(cancelCtx, res.TrackingCode)
		cancelCancel()
		if cancelErr == nil {
			s.logger.InfoContext(ctx, "compensated shipment created for cancelled order",
				slog.String("job_id", job.ID),
				slog.String("order_id", job.OrderID),
				slog.String("tracking_code", res.TrackingCode),
			)
			return nil
		}
		// Double-fault fallback: carrier cancel failed transiently after carrier create succeeded.
		// Persist the created shipment + emit shipment.updated (created) so order's cancelled-order
		// compensation / manual-review path can cancel or escalate it without losing tracking_code.
		s.logger.ErrorContext(ctx, "compensating carrier CancelShipment failed; persisting shipment for downstream cancel compensation",
			slog.String("job_id", job.ID),
			slog.String("order_id", job.OrderID),
			slog.String("tracking_code", res.TrackingCode),
			slog.String("error", cancelErr.Error()),
		)
		if _, _, persistErr := s.repo.CreateShipmentWithOutbox(compCtx, sh, initEv, outbox); persistErr != nil {
			return fmt.Errorf("persist orphan shipment after cancel compensation failure: %w", persistErr)
		}
		if s.outboxWake != nil {
			s.outboxWake.Wake()
		}
		return cancelErr
	}
	if compErr != nil {
		return compErr
	}
	if !alreadyExisted && s.outboxWake != nil {
		s.outboxWake.Wake()
	}
	return nil
}

func (s *ShippingService) buildShipmentCreatedOutbox(ctx context.Context, sh *store.Shipment) (*store.JobOutboxParams, error) {
	traceHeaders := map[string]string{}
	telemetry.InjectTraceHeaders(ctx, traceHeaders)
	rawPayload, err := json.Marshal(events.ShipmentUpdatedPayload{
		ShipmentID:      sh.ID,
		OrderID:         sh.OrderID,
		ShopID:          sh.ShopID,
		CarrierCode:     sh.CarrierCode,
		ServiceCode:     sh.ServiceCode,
		TrackingCode:    sh.TrackingCode,
		LabelURL:        sh.LabelURL,
		Status:          carrier.StatusCreated,
		RawStatus:       carrier.StatusCreated,
		Description:     carrier.StatusDescriptionVI(carrier.StatusCreated),
		OccurredAt:      sh.CreatedAt.UTC().Format(time.RFC3339),
		Direction:       store.NormalizeDirection(sh.Direction),
		ReturnRequestID: sh.ReturnRequestID,
		RedeliveryCount: sh.RedeliveryCount,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal shipment.updated created payload: %w", err)
	}
	return &store.JobOutboxParams{
		EventID:     uuid.NewString(),
		Subject:     events.SubjectShipmentUpdated,
		AggregateID: sh.ID,
		Payload:     rawPayload,
		Traceparent: traceHeaders["traceparent"],
		TraceID:     telemetry.TraceIDFromContext(ctx),
	}, nil
}

func (s *ShippingService) recordShipmentJobFailure(
	ctx context.Context,
	job store.ShipmentCreateJob,
	claimToken string,
	cause error,
	terminal bool,
) error {
	now := s.nowFn()
	nextAttempts := job.Attempts + 1
	isFinal := terminal || nextAttempts >= job.MaxAttempts

	var outbox *store.JobOutboxParams
	if isFinal {
		traceHeaders := map[string]string{}
		telemetry.InjectTraceHeaders(ctx, traceHeaders)
		rawPayload, err := json.Marshal(events.ShipmentCreateFailedPayload{
			JobID:        job.ID,
			OrderID:      job.OrderID,
			ShopID:       job.ShopID,
			CarrierCode:  job.CarrierCode,
			ServiceCode:  job.ServiceCode,
			Attempts:     nextAttempts,
			ErrorMessage: cause.Error(),
			OccurredAt:   now.UTC().Format(time.RFC3339),
		})
		if err != nil {
			return fmt.Errorf("marshal shipment.create_failed payload: %w", err)
		}
		outbox = &store.JobOutboxParams{
			EventID:     uuid.NewString(),
			Subject:     events.SubjectShipmentCreateFailed,
			AggregateID: job.ID,
			Payload:     rawPayload,
			Traceparent: traceHeaders["traceparent"],
			TraceID:     telemetry.TraceIDFromContext(ctx),
		}
	}

	nextRetry := now.Add(s.createRetryBackoff(nextAttempts))
	recCtx, recCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer recCancel()

	updated, ok, err := s.repo.RecordShipmentCreateJobFailure(recCtx, job.ID, claimToken, cause.Error(), &nextRetry, terminal, outbox, now)
	if err != nil {
		return err
	}
	if ok && updated != nil && updated.Status == store.JobStatusFailed && s.outboxWake != nil {
		s.outboxWake.Wake()
	}
	return nil
}
