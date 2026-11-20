package poller

import (
	"context"
	"log/slog"
	"time"

	"github.com/nexusmart/core/services/shipping/internal/service"
)

// Poller periodically queries live carriers for active non-terminal shipments in case a webhook is missed (UC-SHIP04).
type Poller struct {
	svc      *service.ShippingService
	interval time.Duration
	logger   *slog.Logger
}

// New creates a fallback shipment status poller.
func New(svc *service.ShippingService, interval time.Duration, logger *slog.Logger) *Poller {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Poller{
		svc:      svc,
		interval: interval,
		logger:   logger,
	}
}

// Run polls active shipments until ctx is cancelled.
func (p *Poller) Run(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pollCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			if err := p.svc.PollActiveShipments(pollCtx); err != nil {
				p.logger.WarnContext(pollCtx, "fallback shipment polling failed", slog.String("error", err.Error()))
			}
			cancel()
		}
	}
}
