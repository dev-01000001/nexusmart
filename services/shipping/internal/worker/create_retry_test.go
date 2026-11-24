package worker

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/nexusmart/core/services/shipping/internal/carrier"
	"github.com/nexusmart/core/services/shipping/internal/service"
	"github.com/nexusmart/core/services/shipping/internal/store"
)

// workerTestRepo embeds store.Repository so only the methods exercised by CreateRetryWorker are defined.
type workerTestRepo struct {
	store.Repository
	mu        sync.Mutex
	shipments map[string]*store.Shipment
	jobs      map[string]*store.ShipmentCreateJob
	outbox    []store.JobOutboxParams
}

func newWorkerTestRepo() *workerTestRepo {
	return &workerTestRepo{
		shipments: make(map[string]*store.Shipment),
		jobs:      make(map[string]*store.ShipmentCreateJob),
	}
}

func (r *workerTestRepo) AcquireOrderDirectionLock(context.Context, string, string) (func(), error) {
	return func() {}, nil
}
func (r *workerTestRepo) CountInactiveShipmentsByOrder(context.Context, string, string) (int, error) {
	return 0, nil
}
func (r *workerTestRepo) ListCarrierConfigs(context.Context) ([]store.CarrierConfig, error) {
	return []store.CarrierConfig{{CarrierCode: carrier.CodeMockExpress, Name: "Mock Express", Enabled: true}}, nil
}
func (r *workerTestRepo) ListFallbackRates(context.Context, string) ([]store.FallbackRate, error) {
	return nil, nil
}
func (r *workerTestRepo) GetShipmentByOrderID(_ context.Context, orderID, _ string) (*store.Shipment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sh, ok := r.shipments[orderID]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *sh
	return &cp, nil
}
func (r *workerTestRepo) GetActiveShipmentByOrderID(ctx context.Context, orderID, dir string) (*store.Shipment, error) {
	return r.GetShipmentByOrderID(ctx, orderID, dir)
}
func (r *workerTestRepo) GetActiveShipmentCreateJob(_ context.Context, orderID, _ string) (*store.ShipmentCreateJob, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, j := range r.jobs {
		if j.OrderID == orderID && (j.Status == store.JobStatusPending || j.Status == store.JobStatusProcessing) {
			cp := *j
			return &cp, nil
		}
	}
	return nil, store.ErrNotFound
}
func (r *workerTestRepo) GetShipmentCreateJobByID(_ context.Context, jobID string) (*store.ShipmentCreateJob, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	j, ok := r.jobs[jobID]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *j
	return &cp, nil
}
func (r *workerTestRepo) EnqueueShipmentCreateJob(_ context.Context, job *store.ShipmentCreateJob) (*store.ShipmentCreateJob, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *job
	r.jobs[cp.ID] = &cp
	return &cp, false, nil
}
func (r *workerTestRepo) ClaimDueShipmentCreateJobs(_ context.Context, limit int, lease time.Duration, now time.Time) ([]store.ShipmentCreateJob, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	token := "tok-worker"
	var out []store.ShipmentCreateJob
	for _, j := range r.jobs {
		if j.Status == store.JobStatusPending && !j.NextRetryAt.After(now) {
			j.Status = store.JobStatusProcessing
			until := now.Add(lease)
			j.ClaimedUntil = &until
			j.ClaimToken = token
			out = append(out, *j)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, token, nil
}
func (r *workerTestRepo) CompleteShipmentCreateJob(_ context.Context, jobID, claimToken string, sh *store.Shipment, _ *store.ShipmentEvent, outbox *store.JobOutboxParams) (*store.Shipment, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	j, ok := r.jobs[jobID]
	if !ok {
		return nil, false, store.ErrNotFound
	}
	if j.Status == store.JobStatusCancelled {
		return nil, false, store.ErrJobCancelled
	}
	if j.Status != store.JobStatusProcessing || claimToken == "" || j.ClaimToken != claimToken {
		return nil, false, store.ErrStaleClaimToken
	}
	cp := *sh
	r.shipments[sh.OrderID] = &cp
	if outbox != nil {
		r.outbox = append(r.outbox, *outbox)
	}
	j.Status = store.JobStatusSucceeded
	j.ClaimToken = ""
	j.ClaimedUntil = nil
	return &cp, false, nil
}

func TestCreateRetryWorker_ProcessesDueJobAndStopsOnCancel(t *testing.T) {
	repo := newWorkerTestRepo()
	mockExp := carrier.NewMockExpressAdapter()
	mockExp.SetTestFaultInjectionEnabled(true)
	if err := mockExp.InjectCreateFault("ord-worker-1", http.StatusServiceUnavailable, 1); err != nil {
		t.Fatalf("InjectCreateFault: %v", err)
	}
	agg := carrier.NewAggregator(slog.New(slog.NewTextHandler(io.Discard, nil)), mockExp)
	svc := service.NewShippingService(repo, agg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.SetCreateRetryPolicy(8, 5*time.Millisecond, 20*time.Millisecond)

	out, err := svc.CreateShipmentWithQueue(context.Background(), service.CreateShipmentInput{
		OrderID:     "ord-worker-1",
		ShopID:      "shop-1",
		CarrierCode: carrier.CodeMockExpress,
		ServiceCode: "STANDARD",
		WeightGrams: 500,
	})
	if err != nil || !out.Queued {
		t.Fatalf("expected queued job, got out=%+v err=%v", out, err)
	}

	w := NewCreateRetryWorker(svc, 15*time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Run(ctx)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		j, _ := repo.GetShipmentCreateJobByID(context.Background(), out.JobID)
		if j != nil && j.Status == store.JobStatusSucceeded {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("CreateRetryWorker.Run did not stop after ctx cancel")
	}

	j, err := repo.GetShipmentCreateJobByID(context.Background(), out.JobID)
	if err != nil || j.Status != store.JobStatusSucceeded {
		t.Fatalf("expected worker to complete job, got job=%+v err=%v", j, err)
	}
	sh, err := repo.GetShipmentByOrderID(context.Background(), "ord-worker-1", store.DirectionForward)
	if err != nil || sh == nil || sh.TrackingCode == "" {
		t.Fatalf("expected created shipment after worker retry, got sh=%+v err=%v", sh, err)
	}
}
