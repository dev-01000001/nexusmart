package loader

import (
	"context"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nexusmart/core/tools/catalog-gen/internal/generator"
)

// AdminCredentials holds login details for the NexusMart admin account.
type AdminCredentials struct {
	Email    string
	Password string
}

// SellerAccount holds login details for a seeded NexusMart seller account.
type SellerAccount struct {
	Slug     string
	Email    string
	Password string
}

// ProductIngester creates (and optionally approves) a single product via Product API.
type ProductIngester interface {
	CreateProduct(ctx context.Context, spec generator.ProductSpec) (string, error)
	ApproveProduct(ctx context.Context, productID string) error
}

// Progress holds live progress counters reported periodically during ingestion.
type Progress struct {
	Processed int
	Total     int
	Created   int
	Approved  int
	Failed    int
	Elapsed   time.Duration
}

// Summary is the final structured report emitted by catalog-gen.
type Summary struct {
	Mode           string         `json:"mode"`
	DryRun         bool           `json:"dry_run"`
	Seed           uint64         `json:"seed"`
	Generated      int            `json:"generated"`
	Created        int            `json:"created"`
	Approved       int            `json:"approved"`
	Failed         int            `json:"failed"`
	ElapsedMs      int64          `json:"elapsed_ms"`
	RatePerSec     float64        `json:"rate_per_sec"`
	Shops          int            `json:"shops"`
	Categories     int            `json:"categories"`
	TotalSKUs      int            `json:"total_skus,omitempty"`
	OutOfStock     int            `json:"out_of_stock,omitempty"`
	ShopCounts     map[string]int `json:"shop_counts,omitempty"`
	CategoryCounts map[string]int `json:"category_counts,omitempty"`
	SampleErrors   []string       `json:"sample_errors,omitempty"`
}

// RunIngest executes concurrent CreateProduct (+ optional ApproveProduct) calls over products.
func RunIngest(
	ctx context.Context,
	mode string,
	seed uint64,
	products []generator.ProductSpec,
	concurrency int,
	approve bool,
	ingester ProductIngester,
	onProgress func(Progress),
) Summary {
	if concurrency <= 0 {
		concurrency = 1
	}
	if concurrency > len(products) && len(products) > 0 {
		concurrency = len(products)
	}

	start := time.Now()
	total := len(products)

	var (
		processed atomic.Int64
		created   atomic.Int64
		approved  atomic.Int64
		failed    atomic.Int64
		errMu     sync.Mutex
		sampleErr []string
	)

	recordErr := func(err error) {
		failed.Add(1)
		errMu.Lock()
		if len(sampleErr) < 10 {
			sampleErr = append(sampleErr, err.Error())
		}
		errMu.Unlock()
	}

	reportStep := total / 20
	if reportStep < 100 {
		reportStep = 100
	}
	if reportStep > 1000 {
		reportStep = 1000
	}

	jobs := make(chan generator.ProductSpec, concurrency*2)
	var wg sync.WaitGroup

	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for spec := range jobs {
				if ctx.Err() != nil {
					recordErr(ctx.Err())
					processed.Add(1)
					continue
				}
				prodID, err := ingester.CreateProduct(ctx, spec)
				if err != nil {
					recordErr(fmt.Errorf("product[%d] create: %w", spec.Index, err))
				} else {
					created.Add(1)
					if approve {
						if appErr := ingester.ApproveProduct(ctx, prodID); appErr != nil {
							recordErr(fmt.Errorf("product[%d] (%s) approve: %w", spec.Index, prodID, appErr))
						} else {
							approved.Add(1)
						}
					}
				}

				done := int(processed.Add(1))
				if onProgress != nil && (done%reportStep == 0 || done == total) {
					onProgress(Progress{
						Processed: done,
						Total:     total,
						Created:   int(created.Load()),
						Approved:  int(approved.Load()),
						Failed:    int(failed.Load()),
						Elapsed:   time.Since(start),
					})
				}
			}
		}()
	}

	for _, p := range products {
		if ctx.Err() != nil {
			break
		}
		jobs <- p
	}
	close(jobs)
	wg.Wait()

	elapsed := time.Since(start)
	rate := 0.0
	if elapsed > 0 {
		rate = math.Round((float64(created.Load())/elapsed.Seconds())*100) / 100
	}

	return Summary{
		Mode:         mode,
		DryRun:       false,
		Seed:         seed,
		Generated:    total,
		Created:      int(created.Load()),
		Approved:     int(approved.Load()),
		Failed:       int(failed.Load()),
		ElapsedMs:    elapsed.Milliseconds(),
		RatePerSec:   rate,
		SampleErrors: sampleErr,
	}
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
