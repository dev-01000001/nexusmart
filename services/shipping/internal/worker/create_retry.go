package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/nexusmart/core/services/shipping/internal/service"
)

const (
	defaultCreateWorkerInterval = 2 * time.Second
	defaultCreateWorkerBatch    = 20
)

// CreateRetryWorker periodically claims due shipment_create_jobs one at a time (up to batch per tick)
// and retries forward carrier creation under a per-job timeout shorter than the lease (E18).
type CreateRetryWorker struct {
	svc      *service.ShippingService
	interval time.Duration
	batch    int
	logger   *slog.Logger
}

// NewCreateRetryWorker creates a background worker for async shipment creation retries.
func NewCreateRetryWorker(svc *service.ShippingService, interval time.Duration, logger *slog.Logger) *CreateRetryWorker {
	if interval <= 0 {
		interval = defaultCreateWorkerInterval
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &CreateRetryWorker{
		svc:      svc,
		interval: interval,
		batch:    defaultCreateWorkerBatch,
		logger:   logger,
	}
}

// Run loops until ctx is cancelled, waiting for any in-flight job before returning.
func (w *CreateRetryWorker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.runOnce(ctx)
		}
	}
}

func (w *CreateRetryWorker) runOnce(ctx context.Context) {
	if _, err := w.svc.ProcessDueShipmentCreateJobs(ctx, w.batch); err != nil && ctx.Err() == nil {
		w.logger.WarnContext(ctx, "shipment create retry worker cycle failed", slog.String("error", err.Error()))
	}
}
