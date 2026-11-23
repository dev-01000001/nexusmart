package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	"github.com/nexusmart/core/services/product/internal/events"
	"github.com/nexusmart/core/services/product/internal/store"
	"github.com/nexusmart/core/services/product/migrations"
)

type pgTestPublisher struct {
	mu   sync.Mutex
	msgs []*nats.Msg
}

func (p *pgTestPublisher) PublishMsg(m *nats.Msg, _ ...nats.PubOpt) (*nats.PubAck, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cp := *m
	cp.Data = append([]byte(nil), m.Data...)
	cp.Header = nats.Header{}
	for k, v := range m.Header {
		cp.Header[k] = append([]string(nil), v...)
	}
	p.msgs = append(p.msgs, &cp)
	return &nats.PubAck{Stream: "NEXUSMART", Sequence: uint64(len(p.msgs))}, nil
}

// TestPGStore_W2BIntegration runs real PostgreSQL integration and concurrency tests
// when TEST_PRODUCT_DB_URL is set (same convention as shipping's TEST_SHIPPING_DB_URL).
func TestPGStore_W2BIntegration(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_PRODUCT_DB_URL"))
	if dsn == "" {
		t.Skip("set TEST_PRODUCT_DB_URL to run real Postgres integration tests")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()

	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatalf("first migrations.Apply: %v", err)
	}
	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatalf("second migrations.Apply (idempotency): %v", err)
	}

	pgStore := store.NewPgStore(pool)
	adminCtx := ctxWithRole("pg-admin-1", "admin", "")
	sellerCtx := ctxWithRole("pg-seller-1", "seller", "pg-shop-1")
	runPrefix := "pg2b-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]

	// 1. Category tree advisory lock (concurrent cycle prevention), 3-level recursive CTE, CAT-B8 & P3 #20.
	t.Run("CategoryTreeLock_RecursiveCTE_And_DeleteGuard", func(t *testing.T) {
		srv := New(pgStore, nil)

		// Concurrent mutual reparenting across 5 pairs: pg_advisory_xact_lock must prevent ANY cycle.
		for round := 0; round < 5; round++ {
			catA, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
				Name:              fmt.Sprintf("RaceA %s-%d", runPrefix, round),
				Slug:              fmt.Sprintf("%s-race-a-%d", runPrefix, round),
				CommissionRateBps: 500,
			})
			if err != nil {
				t.Fatalf("CreateCategory RaceA round %d: %v", round, err)
			}
			catB, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
				Name:              fmt.Sprintf("RaceB %s-%d", runPrefix, round),
				Slug:              fmt.Sprintf("%s-race-b-%d", runPrefix, round),
				CommissionRateBps: 500,
			})
			if err != nil {
				t.Fatalf("CreateCategory RaceB round %d: %v", round, err)
			}

			var okCount, invalidArgCount atomic.Int32
			start := make(chan struct{})
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				<-start
				_, rErr := srv.UpdateCategory(adminCtx, &productv1.UpdateCategoryRequest{
					Id: catA.GetCategory().GetId(), ParentId: catB.GetCategory().GetId(), HasParentId: true,
				})
				if rErr == nil {
					okCount.Add(1)
				} else if status.Code(rErr) == codes.InvalidArgument {
					invalidArgCount.Add(1)
				}
			}()
			go func() {
				defer wg.Done()
				<-start
				_, rErr := srv.UpdateCategory(adminCtx, &productv1.UpdateCategoryRequest{
					Id: catB.GetCategory().GetId(), ParentId: catA.GetCategory().GetId(), HasParentId: true,
				})
				if rErr == nil {
					okCount.Add(1)
				} else if status.Code(rErr) == codes.InvalidArgument {
					invalidArgCount.Add(1)
				}
			}()
			close(start)
			wg.Wait()

			if okCount.Load() != 1 || invalidArgCount.Load() != 1 {
				t.Fatalf("round %d concurrent mutual reparent: ok=%d invalidArg=%d, want 1 and 1",
					round, okCount.Load(), invalidArgCount.Load())
			}

			// Verify directly in Postgres that A and B do not form a cycle.
			hasCycle, err := pgStore.CategoryHasAncestor(ctx, catA.GetCategory().GetId(), catB.GetCategory().GetId())
			if err != nil {
				t.Fatalf("CategoryHasAncestor A->B: %v", err)
			}
			revCycle, err := pgStore.CategoryHasAncestor(ctx, catB.GetCategory().GetId(), catA.GetCategory().GetId())
			if err != nil {
				t.Fatalf("CategoryHasAncestor B->A: %v", err)
			}
			if hasCycle && revCycle {
				t.Fatalf("round %d committed a cycle in Postgres between %s and %s", round, catA.GetCategory().GetId(), catB.GetCategory().GetId())
			}
		}

		// 3-level tree Cha -> Con -> Chau: recursive CTE ListProducts + GetCategoryDescendants + CAT-B8.
		cha, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
			Name: "PG Cha " + runPrefix, Slug: runPrefix + "-cha", CommissionRateBps: 500, SortOrder: 1,
		})
		if err != nil {
			t.Fatalf("CreateCategory Cha: %v", err)
		}
		con, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
			Name: "PG Con " + runPrefix, Slug: runPrefix + "-con", ParentId: cha.GetCategory().GetId(), CommissionRateBps: 600, SortOrder: 2,
		})
		if err != nil {
			t.Fatalf("CreateCategory Con: %v", err)
		}
		chau, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
			Name: "PG Chau " + runPrefix, Slug: runPrefix + "-chau", ParentId: con.GetCategory().GetId(), CommissionRateBps: 700, SortOrder: 3,
		})
		if err != nil {
			t.Fatalf("CreateCategory Chau: %v", err)
		}

		prod, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
			CategoryId: chau.GetCategory().GetId(),
			Name:       "PG 3rd-level Product " + runPrefix,
			Skus:       []*productv1.SKUInput{{SkuCode: runPrefix + "-L3", Name: "M", PriceVnd: 180000, Stock: 5}},
		})
		if err != nil {
			t.Fatalf("CreateProduct in Chau: %v", err)
		}
		if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
			ProductId: prod.GetProduct().GetId(),
			Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
		}); err != nil {
			t.Fatalf("AdminReviewProduct: %v", err)
		}

		byCha, err := srv.ListProducts(ctx, &productv1.ListProductsRequest{CategoryId: cha.GetCategory().GetId(), Page: 1, PageSize: 20})
		if err != nil || byCha.GetTotal() != 1 || byCha.GetProducts()[0].GetId() != prod.GetProduct().GetId() {
			t.Fatalf("Postgres recursive ListProducts by Cha: got total=%d err=%v", byCha.GetTotal(), err)
		}

		desc, err := srv.GetCategoryDescendants(adminCtx, &productv1.GetCategoryDescendantsRequest{
			CategoryIds: []string{cha.GetCategory().GetId()},
		})
		if err != nil || len(desc.GetCategoryIds()) != 3 {
			t.Fatalf("Postgres GetCategoryDescendants: got %v err=%v", desc.GetCategoryIds(), err)
		}

		// CAT-B8 on real Postgres: soft-delete product -> DeleteCategory returns FailedPrecondition (not Internal 23503).
		if _, err := srv.DeleteProduct(sellerCtx, &productv1.DeleteProductRequest{Id: prod.GetProduct().GetId()}); err != nil {
			t.Fatalf("DeleteProduct: %v", err)
		}
		if _, err := srv.DeleteCategory(adminCtx, &productv1.DeleteCategoryRequest{Id: chau.GetCategory().GetId()}); status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("Postgres DeleteCategory with soft-deleted product: want FailedPrecondition, got %v", err)
		}

		// P3 #20 on real Postgres: SQLSTATE 23503 FK violations map to store.ErrForeignKeyViolation -> codes.NotFound.
		missingCatID := "missing-fk-cat-" + runPrefix
		if _, err := pgStore.CreateProduct(ctx, store.Product{
			ID: uuid.NewString(), ShopID: "pg-shop-1", CategoryID: missingCatID, Name: "FK Test", Slug: "fk-" + runPrefix, Status: store.ProductStatusPending,
		}); !errors.Is(err, store.ErrForeignKeyViolation) {
			t.Fatalf("pgStore.CreateProduct missing category: want ErrForeignKeyViolation, got %v", err)
		}
		if _, err := pgStore.InsertCategoryFeeSchedule(ctx, store.CategoryFeeSchedule{
			ID: uuid.NewString(), CategoryID: missingCatID, CommissionRateBps: 500, EffectiveFrom: time.Now().UTC(),
		}); !errors.Is(err, store.ErrForeignKeyViolation) {
			t.Fatalf("pgStore.InsertCategoryFeeSchedule missing category: want ErrForeignKeyViolation, got %v", err)
		}
	})

	// 2. sku_price_history & min_price_30d_vnd on real Postgres (before-cutoff price, 30d expiry, seq tie-breaker).
	t.Run("SKUPriceHistory_MinPrice30d_RealSQL", func(t *testing.T) {
		srv := New(pgStore, nil)
		now := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
		srv.SetClock(func() time.Time { return now })

		cat, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
			Name: "PG Price Cat " + runPrefix, Slug: runPrefix + "-price", CommissionRateBps: 500,
		})
		if err != nil {
			t.Fatalf("CreateCategory: %v", err)
		}
		prod, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
			CategoryId: cat.GetCategory().GetId(),
			Name:       "PG Price Product " + runPrefix,
			Skus:       []*productv1.SKUInput{{SkuCode: runPrefix + "-PH", Name: "M", PriceVnd: 100000, Stock: 10}},
		})
		if err != nil {
			t.Fatalf("CreateProduct: %v", err)
		}
		prodID := prod.GetProduct().GetId()
		skuID := prod.GetProduct().GetSkus()[0].GetId()

		assertMin := func(label string, want int64) {
			t.Helper()
			b, err := srv.GetSKUsBatch(ctx, &productv1.GetSKUsBatchRequest{SkuIds: []string{skuID}})
			if err != nil || len(b.GetSkus()) != 1 || b.GetSkus()[0].GetMinPrice_30DVnd() != want {
				t.Fatalf("%s GetSKUsBatch: want min_price_30d_vnd=%d, got %+v err=%v", label, want, b.GetSkus(), err)
			}
			gp, err := srv.GetProduct(sellerCtx, &productv1.GetProductRequest{Id: prodID})
			if err != nil || len(gp.GetProduct().GetSkus()) != 1 || gp.GetProduct().GetSkus()[0].GetMinPrice_30DVnd() != want {
				t.Fatalf("%s GetProduct: want min_price_30d_vnd=%d, got %+v err=%v", label, want, gp.GetProduct().GetSkus(), err)
			}
		}

		// Day 40: raise 100k -> 200k. Price 100k was active at cutoff (Day 10), so min_price_30d_vnd == 100k.
		now = now.Add(40 * 24 * time.Hour)
		if _, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
			Id:   prodID,
			Skus: []*productv1.SKUInput{{Id: skuID, SkuCode: runPrefix + "-PH", Name: "M", PriceVnd: 200000, Stock: 10}},
		}); err != nil {
			t.Fatalf("Day 40 raise: %v", err)
		}
		assertMin("Day 40 (price effective before 30d cutoff)", 100000)

		// Day 71: 31 days after raise -> 100k aged out -> min_price_30d_vnd == 200k.
		now = now.Add(31 * 24 * time.Hour)
		assertMin("Day 71 (31d after raise)", 200000)

		// Day 72 (exact same timestamp): drop to 110k then raise to 190k.
		// Inside 30d window -> min is 110k. After +31d (cutoff > Day 72) -> seq tie-break picks 190k as price active at cutoff!
		now = now.Add(24 * time.Hour)
		if _, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
			Id:   prodID,
			Skus: []*productv1.SKUInput{{Id: skuID, SkuCode: runPrefix + "-PH", Name: "M", PriceVnd: 110000, Stock: 10}},
		}); err != nil {
			t.Fatalf("Day 72 drop: %v", err)
		}
		if _, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
			Id:   prodID,
			Skus: []*productv1.SKUInput{{Id: skuID, SkuCode: runPrefix + "-PH", Name: "M", PriceVnd: 190000, Stock: 10}},
		}); err != nil {
			t.Fatalf("Day 72 raise same timestamp: %v", err)
		}
		assertMin("Day 72 (same timestamp 110k -> 190k)", 110000)

		now = now.Add(31 * 24 * time.Hour)
		assertMin("Day 103 (31d after same-timestamp 110k->190k, seq tie-break picks 190k)", 190000)
	})

	// 3. Category fee schedule effective_from on real Postgres.
	t.Run("CategoryFeeSchedule_EffectiveFrom_RealSQL", func(t *testing.T) {
		srv := New(pgStore, nil)
		now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
		srv.SetClock(func() time.Time { return now })

		cat, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
			Name: "PG Fee Cat " + runPrefix, Slug: runPrefix + "-fee", CommissionRateBps: 500,
		})
		if err != nil {
			t.Fatalf("CreateCategory: %v", err)
		}
		catID := cat.GetCategory().GetId()
		prod, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
			CategoryId: catID,
			Name:       "PG Fee Product " + runPrefix,
			Skus:       []*productv1.SKUInput{{SkuCode: runPrefix + "-FEE", Name: "M", PriceVnd: 150000, Stock: 5}},
		})
		if err != nil {
			t.Fatalf("CreateProduct: %v", err)
		}
		skuID := prod.GetProduct().GetSkus()[0].GetId()

		if _, err := srv.SetCategoryFeeSchedule(adminCtx, &productv1.SetCategoryFeeScheduleRequest{
			CategoryId:        catID,
			CommissionRateBps: 750,
			EffectiveFrom:     timestamppb.New(now.Add(-time.Minute)),
		}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("past effective_from: want InvalidArgument, got %v", err)
		}

		futureEff := now.Add(48 * time.Hour)
		if _, err := srv.SetCategoryFeeSchedule(adminCtx, &productv1.SetCategoryFeeScheduleRequest{
			CategoryId:        catID,
			CommissionRateBps: 850,
			EffectiveFrom:     timestamppb.New(futureEff),
			Note:              "scheduled hike",
		}); err != nil {
			t.Fatalf("SetCategoryFeeSchedule future: %v", err)
		}

		bBefore, err := srv.GetSKUsBatch(ctx, &productv1.GetSKUsBatchRequest{SkuIds: []string{skuID}})
		if err != nil || bBefore.GetSkus()[0].GetCommissionRateBps() != 500 {
			t.Fatalf("before effective_from: want 500, got %+v err=%v", bBefore.GetSkus(), err)
		}

		now = futureEff
		bAt, err := srv.GetSKUsBatch(ctx, &productv1.GetSKUsBatchRequest{SkuIds: []string{skuID}})
		if err != nil || bAt.GetSkus()[0].GetCommissionRateBps() != 850 {
			t.Fatalf("at effective_from: want 850, got %+v err=%v", bAt.GetSkus(), err)
		}

		list, err := srv.ListCategoryFeeSchedules(adminCtx, &productv1.ListCategoryFeeSchedulesRequest{
			CategoryId: catID, Page: 1, PageSize: 10,
		})
		if err != nil || list.GetTotal() != 2 || list.GetSchedules()[0].GetCommissionRateBps() != 850 {
			t.Fatalf("ListCategoryFeeSchedules: want total=2 first=850, got total=%d schedules=%+v err=%v",
				list.GetTotal(), list.GetSchedules(), err)
		}

		// Tie-break on same effective_from MUST use (effective_from DESC, seq DESC), ignoring created_at.
		tieEff := now.Add(72 * time.Hour)
		now = tieEff.Add(-1 * time.Hour) // later created_at for lower seq
		if _, err := srv.SetCategoryFeeSchedule(adminCtx, &productv1.SetCategoryFeeScheduleRequest{
			CategoryId:        catID,
			CommissionRateBps: 700,
			EffectiveFrom:     timestamppb.New(tieEff),
			Note:              "tie-break first (later created_at)",
		}); err != nil {
			t.Fatalf("SetCategoryFeeSchedule tie #1: %v", err)
		}
		now = tieEff.Add(-2 * time.Hour) // earlier created_at for higher seq
		if _, err := srv.SetCategoryFeeSchedule(adminCtx, &productv1.SetCategoryFeeScheduleRequest{
			CategoryId:        catID,
			CommissionRateBps: 920,
			EffectiveFrom:     timestamppb.New(tieEff),
			Note:              "tie-break second (earlier created_at, higher seq)",
		}); err != nil {
			t.Fatalf("SetCategoryFeeSchedule tie #2: %v", err)
		}

		tieList, err := srv.ListCategoryFeeSchedules(adminCtx, &productv1.ListCategoryFeeSchedulesRequest{
			CategoryId: catID, Page: 1, PageSize: 10,
		})
		if err != nil || len(tieList.GetSchedules()) < 2 ||
			tieList.GetSchedules()[0].GetCommissionRateBps() != 920 ||
			tieList.GetSchedules()[1].GetCommissionRateBps() != 700 {
			t.Fatalf("Postgres ListCategoryFeeSchedules tie-break: want [920, 700], got %+v err=%v", tieList.GetSchedules(), err)
		}

		now = tieEff
		bTie, err := srv.GetSKUsBatch(ctx, &productv1.GetSKUsBatchRequest{SkuIds: []string{skuID}})
		if err != nil || bTie.GetSkus()[0].GetCommissionRateBps() != 920 {
			t.Fatalf("Postgres GetSKUsBatch tie-break: want 920, got %+v err=%v", bTie.GetSkus(), err)
		}
		gpTie, err := srv.GetProduct(sellerCtx, &productv1.GetProductRequest{Id: prod.GetProduct().GetId()})
		if err != nil || gpTie.GetProduct().GetSkus()[0].GetCommissionRateBps() != 920 {
			t.Fatalf("Postgres GetProduct tie-break: want 920, got %+v err=%v", gpTie.GetProduct().GetSkus(), err)
		}
		catsTie, err := srv.ListCategories(ctx, &productv1.ListCategoriesRequest{})
		if err != nil {
			t.Fatalf("Postgres ListCategories tie-break: %v", err)
		}
		foundCatTie := false
		for _, c := range catsTie.GetCategories() {
			if c.GetId() == catID {
				foundCatTie = true
				if c.GetCommissionRateBps() != 920 {
					t.Fatalf("Postgres ListCategories tie-break: want 920, got %d", c.GetCommissionRateBps())
				}
			}
		}
		if !foundCatTie {
			t.Fatalf("Postgres ListCategories tie-break: category %s not found", catID)
		}
	})

	// 4. AdminReviewProduct FOR UPDATE lock, idempotency, 0-stock->OUT_OF_STOCK, HIDDEN guard, and Outbox SKIP LOCKED.
	t.Run("AdminReviewProduct_LockIdempotency_And_OutboxSkipLocked", func(t *testing.T) {
		srv := New(pgStore, nil)

		cat, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
			Name: "PG Outbox Cat " + runPrefix, Slug: runPrefix + "-outbox", CommissionRateBps: 500,
		})
		if err != nil {
			t.Fatalf("CreateCategory: %v", err)
		}
		catID := cat.GetCategory().GetId()

		prod, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
			CategoryId: catID,
			Name:       "PG Concurrent Review " + runPrefix,
			Skus:       []*productv1.SKUInput{{SkuCode: runPrefix + "-OB", Name: "M", PriceVnd: 120000, Stock: 5}},
		})
		if err != nil {
			t.Fatalf("CreateProduct: %v", err)
		}
		prodID := prod.GetProduct().GetId()

		// Fire 8 concurrent AdminReviewProduct(APPROVE) calls on real Postgres -> FOR UPDATE ensures EXACTLY 1 outbox row!
		const workers = 8
		start := make(chan struct{})
		var wg sync.WaitGroup
		errCh := make(chan error, workers)
		wg.Add(workers)
		for i := 0; i < workers; i++ {
			go func() {
				defer wg.Done()
				<-start
				resp, rErr := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
					ProductId: prodID,
					Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
				})
				if rErr != nil {
					errCh <- rErr
					return
				}
				if resp.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_APPROVED {
					errCh <- fmt.Errorf("unexpected status %v", resp.GetProduct().GetStatus())
				}
			}()
		}
		close(start)
		wg.Wait()
		close(errCh)
		for e := range errCh {
			t.Fatalf("concurrent AdminReviewProduct failed: %v", e)
		}

		var outboxCount int64
		if err := pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM outbox_events WHERE event_type = 'product.reviewed' AND payload_json->>'product_id' = $1`,
			prodID,
		).Scan(&outboxCount); err != nil {
			t.Fatalf("count outbox_events: %v", err)
		}
		if outboxCount != 1 {
			t.Fatalf("8 concurrent APPROVE calls produced %d outbox_events rows, want EXACTLY 1", outboxCount)
		}

		// Approving 0-stock product transitions to OUT_OF_STOCK.
		zeroProd, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
			CategoryId: catID,
			Name:       "PG Zero Stock " + runPrefix,
			Skus:       []*productv1.SKUInput{{SkuCode: runPrefix + "-Z0", Name: "M", PriceVnd: 100000, Stock: 0}},
		})
		if err != nil {
			t.Fatalf("CreateProduct zero stock: %v", err)
		}
		revZero, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
			ProductId: zeroProd.GetProduct().GetId(),
			Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
		})
		if err != nil || revZero.GetProduct().GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_OUT_OF_STOCK {
			t.Fatalf("approving 0-stock product on Postgres: want OUT_OF_STOCK, got %+v err=%v", revZero, err)
		}

		// Two concurrent OutboxRelay workers using FOR UPDATE SKIP LOCKED must publish each pending row ONCE.
		pub := &pgTestPublisher{}
		relay1 := events.NewOutboxRelay(pgStore, pub, nil, 50*time.Millisecond, nil)
		relay2 := events.NewOutboxRelay(pgStore, pub, nil, 50*time.Millisecond, nil)
		rStart := make(chan struct{})
		var rWG sync.WaitGroup
		rWG.Add(2)
		go func() { defer rWG.Done(); <-rStart; relay1.FlushOnce(ctx) }()
		go func() { defer rWG.Done(); <-rStart; relay2.FlushOnce(ctx) }()
		close(rStart)
		rWG.Wait()

		seenIDs := make(map[string]bool)
		var sawProdID bool
		for _, m := range pub.msgs {
			var env events.Envelope
			if err := json.Unmarshal(m.Data, &env); err != nil {
				t.Fatalf("unmarshal published envelope: %v", err)
			}
			if seenIDs[env.EventID] {
				t.Fatalf("duplicate publish detected for outbox event %s", env.EventID)
			}
			seenIDs[env.EventID] = true
			var payload events.ProductReviewedPayload
			if err := json.Unmarshal(env.Payload, &payload); err == nil && payload.ProductID == prodID {
				sawProdID = true
			}
		}
		if !sawProdID {
			t.Fatalf("expected published product.reviewed event for %s", prodID)
		}

		var unpublished int64
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM outbox_events WHERE published_at IS NULL`).Scan(&unpublished); err != nil {
			t.Fatalf("count unpublished outbox_events: %v", err)
		}
		if unpublished != 0 {
			t.Fatalf("expected 0 unpublished outbox_events after relay flush, got %d", unpublished)
		}
	})

	// 5. ReserveStock / ReleaseStock tombstone & concurrent race on real Postgres.
	t.Run("ReserveStock_ReleaseStock_TombstoneAndConcurrency_RealPG", func(t *testing.T) {
		srv := New(pgStore, nil)

		cat, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
			Name: "PG Stock Cat " + runPrefix, Slug: runPrefix + "-stock", CommissionRateBps: 500,
		})
		if err != nil {
			t.Fatalf("CreateCategory: %v", err)
		}
		prod, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
			CategoryId: cat.GetCategory().GetId(),
			Name:       "PG Tombstone Product " + runPrefix,
			Skus:       []*productv1.SKUInput{{SkuCode: runPrefix + "-STK", Name: "M", PriceVnd: 100000, Stock: 50}},
		})
		if err != nil {
			t.Fatalf("CreateProduct: %v", err)
		}
		if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
			ProductId: prod.GetProduct().GetId(),
			Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
		}); err != nil {
			t.Fatalf("AdminReviewProduct: %v", err)
		}
		skuID := prod.GetProduct().GetSkus()[0].GetId()

		svcCtx := ctxWithService("order-service")
		buyerCtx := ctxWithRole("pg-buyer-1", "buyer", "")

		// 5a. Auth check + sequential Release -> Confirm (FailedPrecondition) -> Release (Released=true) on real Postgres.
		earlyOrder := runPrefix + "-early-rel"
		if _, err := srv.ReleaseStock(buyerCtx, &productv1.ReleaseStockRequest{OrderId: earlyOrder}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("buyer ReleaseStock on Postgres: want PermissionDenied, got %v", err)
		}
		if _, err := srv.ConfirmStock(sellerCtx, &productv1.ConfirmStockRequest{OrderId: earlyOrder}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("seller ConfirmStock on Postgres: want PermissionDenied, got %v", err)
		}
		relResp, err := srv.ReleaseStock(svcCtx, &productv1.ReleaseStockRequest{OrderId: earlyOrder})
		if err != nil || relResp.GetReleased() || relResp.GetAlreadyReleased() {
			t.Fatalf("early ReleaseStock (1st tombstone): want Released=false AlreadyReleased=false, got resp=%+v err=%v", relResp, err)
		}
		if _, err := srv.ReserveStock(ctx, &productv1.ReserveStockRequest{
			OrderId: earlyOrder,
			Items:   []*productv1.StockReservationItem{{SkuId: skuID, Quantity: 4}},
		}); status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("late ReserveStock after ReleaseStock on Postgres: want FailedPrecondition, got %v", err)
		}
		if _, err := srv.ConfirmStock(svcCtx, &productv1.ConfirmStockRequest{OrderId: earlyOrder}); status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("ConfirmStock on tombstoned order on Postgres: want FailedPrecondition, got %v", err)
		}
		relResp2, err := srv.ReleaseStock(svcCtx, &productv1.ReleaseStockRequest{OrderId: earlyOrder})
		if err != nil || !relResp2.GetReleased() || !relResp2.GetAlreadyReleased() {
			t.Fatalf("repeat ReleaseStock on tombstoned order on Postgres: want Released=true AlreadyReleased=true, got resp=%+v err=%v", relResp2, err)
		}
		skAfterSeq, err := pgStore.GetSKUWithContext(ctx, skuID)
		if err != nil || skAfterSeq.ReservedStock != 0 {
			t.Fatalf("after sequential release-before-reserve: reserved_stock=%d err=%v, want 0", skAfterSeq.ReservedStock, err)
		}

		// 5b. Concurrent ReserveStock vs ReleaseStock across 10 unique orders on real Postgres -> 0 leaked stock!
		for i := 0; i < 10; i++ {
			raceOrd := fmt.Sprintf("%s-race-stk-%02d", runPrefix, i)
			start := make(chan struct{})
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				<-start
				_, _ = srv.ReserveStock(ctx, &productv1.ReserveStockRequest{
					OrderId: raceOrd,
					Items:   []*productv1.StockReservationItem{{SkuId: skuID, Quantity: 3}},
				})
			}()
			go func() {
				defer wg.Done()
				<-start
				_, _ = srv.ReleaseStock(svcCtx, &productv1.ReleaseStockRequest{OrderId: raceOrd})
			}()
			close(start)
			wg.Wait()
		}

		skFinal, err := pgStore.GetSKUWithContext(ctx, skuID)
		if err != nil {
			t.Fatalf("GetSKUWithContext final: %v", err)
		}
		if skFinal.ReservedStock != 0 {
			t.Fatalf("concurrent ReserveStock/ReleaseStock on Postgres leaked reserved_stock=%d (want 0)", skFinal.ReservedStock)
		}
	})
}
