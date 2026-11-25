package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/jetstream"
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
// when TEST_PRODUCT_DB_URL is set.
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
		if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
			ProductId: prodID,
			Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
		}); err != nil {
			t.Fatalf("AdminReviewProduct approve: %v", err)
		}

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
		if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
			ProductId: prod.GetProduct().GetId(),
			Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
		}); err != nil {
			t.Fatalf("AdminReviewProduct approve: %v", err)
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
		if _, err := srv.ReserveStock(buyerCtx, &productv1.ReserveStockRequest{
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
				_, _ = srv.ReserveStock(buyerCtx, &productv1.ReserveStockRequest{
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

	// 6. W3A D2: GetSKUsBatch hidden SKU redaction on real Postgres.
	t.Run("W3A_D2_GetSKUsBatch_Redaction_RealPG", func(t *testing.T) {
		srv := New(pgStore, nil)
		buyerCtx := ctxWithRole("pg-d2-buyer", "buyer", "")
		otherSellerCtx := ctxWithRole("pg-d2-other", "seller", "pg-shop-other")

		cat, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
			Name: "PG D2 Cat " + runPrefix, Slug: runPrefix + "-d2", CommissionRateBps: 650,
		})
		if err != nil {
			t.Fatalf("CreateCategory: %v", err)
		}
		catID := cat.GetCategory().GetId()

		approvedProd, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
			CategoryId: catID,
			Name:       "PG D2 Active " + runPrefix,
			Skus: []*productv1.SKUInput{
				{SkuCode: runPrefix + "-D2-ACT", Name: "Active", Attributes: map[string]string{"k": "v"}, PriceVnd: 199000, Stock: 10, WeightGrams: 250, ImageUrl: "https://cdn.local/act.jpg"},
				{SkuCode: runPrefix + "-D2-OFF", Name: "Off", Attributes: map[string]string{"k": "v"}, PriceVnd: 210000, Stock: 5, WeightGrams: 260, ImageUrl: "https://cdn.local/off.jpg"},
			},
		})
		if err != nil {
			t.Fatalf("CreateProduct active: %v", err)
		}
		if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
			ProductId: approvedProd.GetProduct().GetId(),
			Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
		}); err != nil {
			t.Fatalf("Approve: %v", err)
		}
		skuAct := findSKUByCode(t, approvedProd.GetProduct().GetSkus(), runPrefix+"-D2-ACT")
		skuOff := findSKUByCode(t, approvedProd.GetProduct().GetSkus(), runPrefix+"-D2-OFF")
		if _, err := srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
			Id:   approvedProd.GetProduct().GetId(),
			Skus: []*productv1.SKUInput{{Id: skuAct.GetId(), SkuCode: runPrefix + "-D2-ACT", Name: "Active", Attributes: map[string]string{"k": "v"}, PriceVnd: 199000, Stock: 10, WeightGrams: 250, ImageUrl: "https://cdn.local/act.jpg"}},
		}); err != nil {
			t.Fatalf("deactivate skuOff: %v", err)
		}

		pendingProd, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
			CategoryId: catID,
			Name:       "PG D2 Pending " + runPrefix,
			Skus:       []*productv1.SKUInput{{SkuCode: runPrefix + "-D2-PEND", Name: "Pend", PriceVnd: 220000, Stock: 4, WeightGrams: 200, ImageUrl: "https://cdn.local/pend.jpg"}},
		})
		if err != nil {
			t.Fatalf("CreateProduct pending: %v", err)
		}
		skuPend := pendingProd.GetProduct().GetSkus()[0]

		ids := []string{skuAct.GetId(), skuOff.GetId(), skuPend.GetId()}
		for label, caller := range map[string]context.Context{
			"anon":         ctx,
			"buyer":        buyerCtx,
			"other_seller": otherSellerCtx,
		} {
			resp, bErr := srv.GetSKUsBatch(caller, &productv1.GetSKUsBatchRequest{SkuIds: ids})
			if bErr != nil || len(resp.GetSkus()) != 3 {
				t.Fatalf("%s GetSKUsBatch: len=%d err=%v", label, len(resp.GetSkus()), bErr)
			}
			for _, sk := range resp.GetSkus() {
				if sk.GetId() == skuAct.GetId() {
					if !sk.GetIsActive() || sk.GetPriceVnd() != 199000 || sk.GetCommissionRateBps() != 650 {
						t.Fatalf("%s active SKU altered: %+v", label, sk)
					}
					continue
				}
				if sk.GetIsActive() || sk.GetPriceVnd() != 0 || sk.GetStock() != 0 || sk.GetSkuCode() != "" ||
					sk.GetCommissionRateBps() != 0 || sk.GetCategoryId() != "" || sk.GetMinPrice_30DVnd() != 0 ||
					sk.GetShopId() != "pg-shop-1" || sk.GetProductName() == "" {
					t.Fatalf("%s hidden SKU redaction failed: %+v", label, sk)
				}
			}
		}

		for label, caller := range map[string]context.Context{
			"owner_seller": sellerCtx,
			"admin":        adminCtx,
		} {
			resp, bErr := srv.GetSKUsBatch(caller, &productv1.GetSKUsBatchRequest{SkuIds: ids})
			if bErr != nil || len(resp.GetSkus()) != 3 {
				t.Fatalf("%s GetSKUsBatch: len=%d err=%v", label, len(resp.GetSkus()), bErr)
			}
			for _, sk := range resp.GetSkus() {
				if sk.GetPriceVnd() <= 0 || sk.GetCommissionRateBps() != 650 || sk.GetSkuCode() == "" {
					t.Fatalf("%s must see full hidden SKU fields: %+v", label, sk)
				}
			}
		}
	})

	// 7. W3A D8: ListProducts.sku_ids filtering, pagination, and real Postgres index plan (no Seq Scan on products).
	t.Run("W3A_D8_ListProducts_SKUIDs_And_IndexPlan_RealPG", func(t *testing.T) {
		srv := New(pgStore, nil)
		cat, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
			Name: "PG D8 Cat " + runPrefix, Slug: runPrefix + "-d8", CommissionRateBps: 500,
		})
		if err != nil {
			t.Fatalf("CreateCategory: %v", err)
		}
		catID := cat.GetCategory().GetId()

		// Bulk-seed 2,000 products & SKUs so Postgres planner prefers index scan over seq scan.
		if _, err := pool.Exec(ctx, `
			INSERT INTO products (id, shop_id, category_id, name, description, images, status, min_price_vnd, max_price_vnd, slug, search_text)
			SELECT $1 || '-p-' || g, 'pg-shop-bulk', $2, 'Bulk Product ' || g, '', '[]'::jsonb, 'APPROVED',
			       100000 + g * 100, 100000 + g * 100, $1 || '-bulk-' || g, 'bulk product ' || g
			FROM generate_series(1, 2000) AS g
		`, runPrefix, catID); err != nil {
			t.Fatalf("bulk insert products: %v", err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO skus (id, product_id, sku_code, name, attributes, price_vnd, stock, reserved_stock, version, weight_grams, image_url, status)
			SELECT $1 || '-s-' || g, $1 || '-p-' || g, 'BLK-' || g, 'Std', '{}'::jsonb, 100000 + g * 100, 10, 0, 1, 200, '', 'ACTIVE'
			FROM generate_series(1, 2000) AS g
		`, runPrefix); err != nil {
			t.Fatalf("bulk insert skus: %v", err)
		}
		// Add a PENDING product and a soft-deleted product to ensure sku_ids excludes them.
		if _, err := pool.Exec(ctx, `
			INSERT INTO products (id, shop_id, category_id, name, description, images, status, min_price_vnd, max_price_vnd, slug, search_text, deleted_at)
			VALUES
				($1 || '-p-pend', 'pg-shop-1', $2, 'Pending Bulk', '', '[]'::jsonb, 'PENDING', 150000, 150000, $1 || '-pend', 'pending bulk', NULL),
				($1 || '-p-del',  'pg-shop-1', $2, 'Deleted Bulk', '', '[]'::jsonb, 'APPROVED', 150000, 150000, $1 || '-del', 'deleted bulk', NOW())
		`, runPrefix, catID); err != nil {
			t.Fatalf("insert edge-case products: %v", err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO skus (id, product_id, sku_code, name, attributes, price_vnd, stock, reserved_stock, version, weight_grams, image_url, status)
			VALUES
				($1 || '-s-pend',  $1 || '-p-pend', 'PEND-1', 'Std',  '{}'::jsonb, 150000, 5, 0, 1, 200, '', 'ACTIVE'),
				($1 || '-s-del',   $1 || '-p-del',  'DEL-1',  'Std',  '{}'::jsonb, 150000, 5, 0, 1, 200, '', 'ACTIVE'),
				($1 || '-s-dup1',  $1 || '-p-1',    'DUP-1',  'Var2', '{}'::jsonb, 100200, 5, 0, 1, 200, '', 'ACTIVE'),
				($1 || '-s-inact', $1 || '-p-5',    'OFF-5',  'Off',  '{}'::jsonb, 100500, 5, 0, 1, 200, '', 'INACTIVE')
		`, runPrefix); err != nil {
			t.Fatalf("insert edge-case skus: %v", err)
		}
		if _, err := pool.Exec(ctx, `ANALYZE products; ANALYZE skus;`); err != nil {
			t.Fatalf("ANALYZE: %v", err)
		}

		// Query with 3 valid public SKUs (2 on same product p-1), plus pending, deleted, and deactivated SKU.
		querySKUs := []string{
			runPrefix + "-s-1",
			runPrefix + "-s-dup1", // same product p-1
			runPrefix + "-s-2",
			runPrefix + "-s-3",
			runPrefix + "-s-pend",
			runPrefix + "-s-del",
			runPrefix + "-s-inact",
		}
		p1, err := srv.ListProducts(adminCtx, &productv1.ListProductsRequest{
			SkuIds:   querySKUs,
			SortBy:   "price_asc",
			Page:     1,
			PageSize: 2,
		})
		if err != nil {
			t.Fatalf("ListProducts(sku_ids) page 1: %v", err)
		}
		if p1.GetTotal() != 3 || len(p1.GetProducts()) != 2 ||
			p1.GetProducts()[0].GetId() != runPrefix+"-p-1" ||
			p1.GetProducts()[1].GetId() != runPrefix+"-p-2" {
			t.Fatalf("ListProducts(sku_ids) page 1: want total=3 [p-1,p-2], got total=%d len=%d", p1.GetTotal(), len(p1.GetProducts()))
		}
		p2, err := srv.ListProducts(ctx, &productv1.ListProductsRequest{
			SkuIds:   querySKUs,
			SortBy:   "price_asc",
			Page:     2,
			PageSize: 2,
		})
		if err != nil || p2.GetTotal() != 3 || len(p2.GetProducts()) != 1 || p2.GetProducts()[0].GetId() != runPrefix+"-p-3" {
			t.Fatalf("ListProducts(sku_ids) page 2: want total=3 [p-3], got total=%d products=%+v err=%v", p2.GetTotal(), p2.GetProducts(), err)
		}

		// Verify EXPLAIN plan uses index scans on skus & products (no Seq Scan on products).
		planRows, err := pool.Query(ctx, `
			EXPLAIN (ANALYZE, FORMAT TEXT)
			WITH matched_products AS (
				SELECT DISTINCT s.product_id
				FROM skus s
				WHERE s.id = ANY($1::text[])
				  AND s.status <> 'INACTIVE'
			)
			SELECT p.id
			FROM matched_products mp
			JOIN products p ON p.id = mp.product_id
			WHERE p.deleted_at IS NULL
			  AND p.status IN ('APPROVED', 'OUT_OF_STOCK')
			ORDER BY p.min_price_vnd ASC, p.created_at DESC, p.id DESC
			LIMIT 20 OFFSET 0
		`, querySKUs)
		if err != nil {
			t.Fatalf("EXPLAIN ListProductsBySKUIDs: %v", err)
		}
		defer planRows.Close()
		var planLines []string
		for planRows.Next() {
			var line string
			if err := planRows.Scan(&line); err != nil {
				t.Fatalf("scan plan line: %v", err)
			}
			planLines = append(planLines, line)
		}
		planText := strings.Join(planLines, "\n")
		t.Logf("ListProductsBySKUIDs EXPLAIN plan:\n%s", planText)
		if strings.Contains(planText, "Seq Scan on products") {
			t.Fatalf("expected index scan on products, got Seq Scan:\n%s", planText)
		}
		if strings.Contains(planText, "Seq Scan on skus") {
			t.Fatalf("expected index scan on skus, got Seq Scan:\n%s", planText)
		}
		if !strings.Contains(planText, "Index") {
			t.Fatalf("expected Index Scan / Index Only Scan in plan:\n%s", planText)
		}
	})

	// 8. W3A D9: Outbox claim-lease outside DB tx, concurrent workers, crash recovery, stale token, aggregate ordering.
	t.Run("W3A_D9_OutboxLease_OutsideTx_CrashRecovery_And_AggregateOrder_RealPG", func(t *testing.T) {
		subPrefix := runPrefix + "-d9-lease-"
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(), `DELETE FROM outbox_events WHERE id LIKE $1`, subPrefix+"%")
		})

		// Drain any unpublished outbox events from earlier subtests first.
		drainRelay := events.NewOutboxRelay(pgStore, &pgTestPublisher{}, nil, 50*time.Millisecond, nil)
		drainRelay.FlushOnce(ctx)

		baseTime := time.Now().UTC().Add(-10 * time.Minute)
		insertEv := func(id, aggID string, at time.Time) {
			t.Helper()
			payloadBytes, _ := json.Marshal(events.ProductReviewedPayload{
				ProductID: aggID, ProductName: "Prod " + aggID, ShopID: "pg-shop-1", Decision: "APPROVE", OccurredAt: at.Format(time.RFC3339),
			})
			if err := pgStore.InsertOutboxEvent(ctx, store.OutboxEvent{
				ID: id, EventType: events.SubjectProductReviewed, AggregateID: aggID, PayloadJSON: payloadBytes, CreatedAt: at,
			}); err != nil {
				t.Fatalf("InsertOutboxEvent %s: %v", id, err)
			}
		}

		// 8a. Prove publishFn runs OUTSIDE any DB transaction (zero RowExclusiveLock on outbox_events during publish).
		evTxProof := subPrefix + "outtx"
		insertEv(evTxProof, runPrefix+"-agg-tx", baseTime)
		var sawLocksDuringPublish int64
		var sawClaimTokenSet bool
		publishedCount, err := pgStore.ProcessPendingOutbox(ctx, 10, func(pCtx context.Context, ev store.OutboxEvent) error {
			if ev.ID != evTxProof {
				return nil
			}
			// Check from a separate pool connection that NO row-level/table-level write lock is held on outbox_events,
			// and that claimed_until + claim_token are ALREADY committed and visible!
			var lockCnt int64
			if qErr := pool.QueryRow(pCtx, `
				SELECT COUNT(*)
				FROM pg_locks l
				JOIN pg_class c ON c.oid = l.relation
				WHERE c.relname = 'outbox_events'
				  AND l.mode IN ('RowExclusiveLock', 'ShareRowExclusiveLock', 'ExclusiveLock', 'AccessExclusiveLock')
				  AND l.granted = true
			`).Scan(&lockCnt); qErr != nil {
				return qErr
			}
			sawLocksDuringPublish = lockCnt

			var token string
			var until *time.Time
			if qErr := pool.QueryRow(pCtx, `SELECT claim_token, claimed_until FROM outbox_events WHERE id = $1`, ev.ID).Scan(&token, &until); qErr != nil {
				return qErr
			}
			sawClaimTokenSet = token != "" && until != nil && until.After(time.Now().UTC().Add(-time.Second))
			return nil
		})
		if err != nil || publishedCount != 1 {
			t.Fatalf("out-of-tx proof publish: count=%d err=%v", publishedCount, err)
		}
		if sawLocksDuringPublish != 0 {
			t.Fatalf("outbox publish held %d write locks on outbox_events during publishFn (must publish outside tx!)", sawLocksDuringPublish)
		}
		if !sawClaimTokenSet {
			t.Fatalf("expected committed claim_token and claimed_until visible to other connections during publishFn")
		}

		// 8b. Crash recovery + stale token + cross-worker per-aggregate blocking on real Postgres.
		aggCrash := runPrefix + "-agg-crash"
		aggFree := runPrefix + "-agg-free"
		evC1 := subPrefix + "c1"
		evC2 := subPrefix + "c2"
		evFree := subPrefix + "free"
		insertEv(evC1, aggCrash, baseTime.Add(time.Second))
		insertEv(evC2, aggCrash, baseTime.Add(2*time.Second))
		insertEv(evFree, aggFree, baseTime.Add(3*time.Second))

		// Worker 1 claims only evC1 and "crashes" (never marks published).
		claimed1, staleToken, err := pgStore.ClaimPendingOutbox(ctx, 1)
		if err != nil || len(claimed1) != 1 || claimed1[0].ID != evC1 {
			t.Fatalf("worker 1 claim: got=%+v err=%v", claimed1, err)
		}

		// Worker 2 runs while evC1 is leased: MUST NOT claim evC1 OR evC2 (same aggregate), ONLY evFree!
		var w2Published []string
		n2, err := pgStore.ProcessPendingOutbox(ctx, 10, func(_ context.Context, ev store.OutboxEvent) error {
			w2Published = append(w2Published, ev.ID)
			return nil
		})
		if err != nil || n2 != 1 || len(w2Published) != 1 || w2Published[0] != evFree {
			t.Fatalf("worker 2 while evC1 leased: want [%s], got n=%d ids=%v err=%v", evFree, n2, w2Published, err)
		}

		// Simulate lease expiry in Postgres -> Worker 2 re-claims and publishes [evC1, evC2] in strict order!
		if _, err := pool.Exec(ctx, `UPDATE outbox_events SET claimed_until = NOW() - INTERVAL '1 second' WHERE id = $1`, evC1); err != nil {
			t.Fatalf("expire lease: %v", err)
		}
		var recovered []string
		nRec, err := pgStore.ProcessPendingOutbox(ctx, 10, func(_ context.Context, ev store.OutboxEvent) error {
			recovered = append(recovered, ev.ID)
			return nil
		})
		if err != nil || nRec != 2 || len(recovered) != 2 || recovered[0] != evC1 || recovered[1] != evC2 {
			t.Fatalf("recovery after lease expiry: want [%s %s], got n=%d ids=%v err=%v", evC1, evC2, nRec, recovered, err)
		}

		// Zombie Worker 1 trying to mark published with staleToken MUST affect 0 rows (marked=false).
		zombieMarked, err := pgStore.MarkOutboxPublished(ctx, evC1, staleToken)
		if err != nil || zombieMarked {
			t.Fatalf("zombie MarkOutboxPublished: want marked=false err=nil, got marked=%v err=%v", zombieMarked, err)
		}

		// 8c. Failure on evF1 blocks evF2 (same aggregate) in batch while evOther publishes, then retry drains [evF1, evF2] in order.
		aggFail := runPrefix + "-agg-fail"
		aggOK := runPrefix + "-agg-ok"
		evF1 := subPrefix + "f1"
		evOK := subPrefix + "ok"
		evF2 := subPrefix + "f2"
		insertEv(evF1, aggFail, baseTime.Add(4*time.Second))
		insertEv(evOK, aggOK, baseTime.Add(5*time.Second))
		insertEv(evF2, aggFail, baseTime.Add(6*time.Second))

		var attempt1IDs []string
		nFailBatch, err := pgStore.ProcessPendingOutbox(ctx, 10, func(_ context.Context, ev store.OutboxEvent) error {
			attempt1IDs = append(attempt1IDs, ev.ID)
			if ev.ID == evF1 {
				return errors.New("simulated nats outage for evF1")
			}
			return nil
		})
		if err != nil || nFailBatch != 1 || len(attempt1IDs) != 2 || attempt1IDs[0] != evF1 || attempt1IDs[1] != evOK {
			t.Fatalf("aggregate failure batch: want published=1 tried=[%s %s] (evF2 skipped), got n=%d tried=%v err=%v",
				evF1, evOK, nFailBatch, attempt1IDs, err)
		}
		var evF2Attempts int32
		if err := pool.QueryRow(ctx, `SELECT attempts FROM outbox_events WHERE id = $1`, evF2).Scan(&evF2Attempts); err != nil {
			t.Fatalf("check evF2 attempts: %v", err)
		}
		if evF2Attempts != 0 {
			t.Fatalf("skipped evF2 should have attempts=0, got %d", evF2Attempts)
		}
		// Non-infra failure puts evF1 into backoff (claimed_until > NOW()), so expire backoff before testing retry order.
		var evF1Until *time.Time
		if err := pool.QueryRow(ctx, `SELECT claimed_until FROM outbox_events WHERE id = $1`, evF1).Scan(&evF1Until); err != nil {
			t.Fatalf("check evF1 backoff: %v", err)
		}
		if evF1Until == nil || !evF1Until.After(time.Now().UTC()) {
			t.Fatalf("expected evF1 in future backoff after non-infra failure, got claimed_until=%v", evF1Until)
		}
		if _, err := pool.Exec(ctx, `UPDATE outbox_events SET claimed_until = NOW() - INTERVAL '1 second' WHERE id = $1`, evF1); err != nil {
			t.Fatalf("expire evF1 backoff: %v", err)
		}

		var attempt2IDs []string
		nRetry, err := pgStore.ProcessPendingOutbox(ctx, 10, func(_ context.Context, ev store.OutboxEvent) error {
			attempt2IDs = append(attempt2IDs, ev.ID)
			return nil
		})
		if err != nil || nRetry != 2 || len(attempt2IDs) != 2 || attempt2IDs[0] != evF1 || attempt2IDs[1] != evF2 {
			t.Fatalf("retry after aggregate failure: want [%s %s], got n=%d ids=%v err=%v", evF1, evF2, nRetry, attempt2IDs, err)
		}
	})

	// 9. W3A D9 follow-up: slow publisher stops before lease expiry, 2 concurrent workers keep strict order,
	//    and NATS infra error aborts batch after 1 attempt releasing remaining events immediately.
	t.Run("W3A_D9_LeaseBudgetAndInfraError_RealPG", func(t *testing.T) {
		subPrefix := runPrefix + "-d9-budget-"
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(), `DELETE FROM outbox_events WHERE id LIKE $1`, subPrefix+"%")
		})

		// Use a dedicated PgStore instance with custom lease/timeout so we don't mutate pgStore.
		slowStore := store.NewPgStore(pool)
		defer slowStore.SetOutboxLease(jetstream.DefaultOutboxLease)

		// Drain any leftover unpublished events.
		_, _ = slowStore.ProcessPendingOutbox(ctx, 100, func(_ context.Context, _ store.OutboxEvent) error { return nil })

		// 9a. NATS ErrTimeout aborts batch after 1 attempt and releases remaining events immediately.
		base := time.Now().UTC().Add(-5 * time.Minute)
		infraIDs := make([]string, 4)
		for i := 0; i < 4; i++ {
			id := fmt.Sprintf("%sinfra-%d", subPrefix, i)
			infraIDs[i] = id
			agg := fmt.Sprintf("%s-agg-infra-%d", runPrefix, i)
			raw, _ := json.Marshal(events.ProductReviewedPayload{
				ProductID: agg, ProductName: agg, ShopID: "pg-shop-1", Decision: "APPROVE",
				OccurredAt: base.Add(time.Duration(i) * time.Second).Format(time.RFC3339),
			})
			if err := slowStore.InsertOutboxEvent(ctx, store.OutboxEvent{
				ID: id, EventType: events.SubjectProductReviewed, AggregateID: agg, PayloadJSON: raw, CreatedAt: base.Add(time.Duration(i) * time.Second),
			}); err != nil {
				t.Fatalf("insert infra %d: %v", i, err)
			}
		}

		infraAttempts := 0
		nInfra, infraErr := slowStore.ProcessPendingOutbox(ctx, 10, func(_ context.Context, _ store.OutboxEvent) error {
			infraAttempts++
			return fmt.Errorf("nats down: %w", nats.ErrTimeout)
		})
		if infraErr == nil || !errors.Is(infraErr, nats.ErrTimeout) {
			t.Fatalf("expected nats.ErrTimeout returned, got n=%d err=%v", nInfra, infraErr)
		}
		if infraAttempts != 1 {
			t.Fatalf("expected only 1 publish attempt on nats.ErrTimeout, got %d", infraAttempts)
		}
		for i, id := range infraIDs {
			var attempts int32
			var token string
			var until *time.Time
			if err := pool.QueryRow(ctx, `SELECT attempts, claim_token, claimed_until FROM outbox_events WHERE id = $1`, id).Scan(&attempts, &token, &until); err != nil {
				t.Fatalf("query infra row %s: %v", id, err)
			}
			if token != "" || until != nil {
				t.Fatalf("row %s still leased after infra error: token=%q until=%v", id, token, until)
			}
			if i == 0 && attempts != 1 {
				t.Fatalf("first row attempts=%d, want 1", attempts)
			}
			if i > 0 && attempts != 0 {
				t.Fatalf("remaining row %s attempts=%d, want 0", id, attempts)
			}
		}
		// Drain the 4 infra rows now that NATS is healthy.
		nDrained, err := slowStore.ProcessPendingOutbox(ctx, 10, func(_ context.Context, _ store.OutboxEvent) error { return nil })
		if err != nil || nDrained != 4 {
			t.Fatalf("drain after infra error: want 4, got %d err=%v", nDrained, err)
		}

		// 9b. Slow publisher with 30s lease (simulated via Go clock advancing 4s/event) stops at 7 events (< 30s),
		//     releases remaining events immediately, and never publishes past lease deadline.
		var clkMu sync.Mutex
		fakeNow := time.Now().UTC()
		getFakeNow := func() time.Time {
			clkMu.Lock()
			defer clkMu.Unlock()
			return fakeNow
		}
		advFakeNow := func(d time.Duration) time.Time {
			clkMu.Lock()
			defer clkMu.Unlock()
			fakeNow = fakeNow.Add(d)
			return fakeNow
		}
		slowStore.SetOutboxLease(30 * time.Second)
		slowStore.SetOutboxClock(getFakeNow)

		const slowTotal = 10
		slowAgg := runPrefix + "-agg-slow"
		slowIDs := make([]string, slowTotal)
		for i := 0; i < slowTotal; i++ {
			id := fmt.Sprintf("%sslow-%02d", subPrefix, i)
			slowIDs[i] = id
			raw, _ := json.Marshal(events.ProductReviewedPayload{
				ProductID: slowAgg, ProductName: "Slow", ShopID: "pg-shop-1", Decision: "APPROVE",
				OccurredAt: base.Add(time.Duration(10+i) * time.Second).Format(time.RFC3339),
			})
			if err := slowStore.InsertOutboxEvent(ctx, store.OutboxEvent{
				ID: id, EventType: events.SubjectProductReviewed, AggregateID: slowAgg, PayloadJSON: raw, CreatedAt: base.Add(time.Duration(10+i) * time.Second),
			}); err != nil {
				t.Fatalf("insert slow %d: %v", i, err)
			}
		}

		deadline1 := getFakeNow().Add(30 * time.Second)
		var publishedSeq []string
		var overranLease bool
		nSlow1, err := slowStore.ProcessPendingOutbox(ctx, 50, func(_ context.Context, ev store.OutboxEvent) error {
			if advFakeNow(4 * time.Second).After(deadline1) {
				overranLease = true
			}
			publishedSeq = append(publishedSeq, ev.ID)
			return nil
		})
		if err != nil {
			t.Fatalf("slow batch 1: %v", err)
		}
		if overranLease {
			t.Fatalf("slow batch 1 published after lease deadline (published=%d)", nSlow1)
		}
		if nSlow1 != 7 {
			t.Fatalf("slow batch 1 (30s lease, 5s max publish, 4s/event): want 7 published, got %d", nSlow1)
		}
		// Remaining 3 rows must have lease cleared and attempts=0 in Postgres right away.
		for _, id := range slowIDs[7:] {
			var attempts int32
			var token string
			var until *time.Time
			if err := pool.QueryRow(ctx, `SELECT attempts, claim_token, claimed_until FROM outbox_events WHERE id = $1`, id).Scan(&attempts, &token, &until); err != nil {
				t.Fatalf("query released slow row %s: %v", id, err)
			}
			if token != "" || until != nil || attempts != 0 {
				t.Fatalf("remaining slow row %s should be released with attempts=0, got token=%q until=%v attempts=%d", id, token, until, attempts)
			}
		}
		// Second batch drains remaining 3 in exact order.
		nSlow2, err := slowStore.ProcessPendingOutbox(ctx, 50, func(_ context.Context, ev store.OutboxEvent) error {
			advFakeNow(4 * time.Second)
			publishedSeq = append(publishedSeq, ev.ID)
			return nil
		})
		if err != nil || nSlow2 != 3 {
			t.Fatalf("slow batch 2: want 3, got %d err=%v", nSlow2, err)
		}
		for i, wantID := range slowIDs {
			if publishedSeq[i] != wantID {
				t.Fatalf("slow publishedSeq[%d]=%q, want %q", i, publishedSeq[i], wantID)
			}
		}

		// 9c. Two concurrent real-Postgres workers with second-scale lease (3s), publish timeout (1s),
		//     and slow publisher (350ms/event <= 400ms) on 10 events of ONE aggregate (10*350ms = 3.5s > 3s):
		//     every event published once in strict seq order, resilient under high CPU load.
		concStore := store.NewPgStore(pool)
		concStore.SetOutboxLease(3 * time.Second)
		concStore.SetOutboxPublishTimeout(1 * time.Second)

		concAgg := runPrefix + "-agg-conc"
		const concTotal = 10
		concIDs := make([]string, concTotal)
		for i := 0; i < concTotal; i++ {
			id := fmt.Sprintf("%sconc-%02d", subPrefix, i)
			concIDs[i] = id
			raw, _ := json.Marshal(events.ProductReviewedPayload{
				ProductID: concAgg, ProductName: "Conc", ShopID: "pg-shop-1", Decision: "APPROVE",
				OccurredAt: base.Add(time.Duration(30+i) * time.Second).Format(time.RFC3339),
			})
			if err := concStore.InsertOutboxEvent(ctx, store.OutboxEvent{
				ID: id, EventType: events.SubjectProductReviewed, AggregateID: concAgg, PayloadJSON: raw, CreatedAt: base.Add(time.Duration(30+i) * time.Second),
			}); err != nil {
				t.Fatalf("insert conc %d: %v", i, err)
			}
		}

		var orderMu sync.Mutex
		var concFirstSeen []string
		seenMsgID := make(map[string]string, concTotal)
		var dupMismatch bool
		pubFn := func(_ context.Context, ev store.OutboxEvent) error {
			time.Sleep(350 * time.Millisecond)

			orderMu.Lock()
			if prev, ok := seenMsgID[ev.ID]; !ok {
				seenMsgID[ev.ID] = ev.ID
				concFirstSeen = append(concFirstSeen, ev.ID)
			} else if prev != ev.ID {
				dupMismatch = true
			}
			orderMu.Unlock()
			return nil
		}

		workCtx, workCancel := context.WithTimeout(ctx, 30*time.Second)
		defer workCancel()
		var wg sync.WaitGroup
		worker := func() {
			defer wg.Done()
			for workCtx.Err() == nil {
				_, _ = concStore.ProcessPendingOutbox(workCtx, 50, pubFn)
				var left int64
				if err := pool.QueryRow(workCtx, `SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = $1 AND published_at IS NULL`, concAgg).Scan(&left); err == nil && left == 0 {
					return
				}
				time.Sleep(100 * time.Millisecond)
			}
		}
		wg.Add(2)
		go worker()
		go worker()
		wg.Wait()

		// Per D9-bis #7: duplicates after lease expiry under heavy host load are valid with identical Nats-Msg-Id (ev.ID);
		// verify every event is delivered and FIRST delivery order within the aggregate is strictly preserved.
		if dupMismatch {
			t.Fatalf("D9 violation: duplicate delivery carried mismatched event ID")
		}
		if len(concFirstSeen) != concTotal {
			t.Fatalf("concFirstSeen len=%d, want %d (ids=%v)", len(concFirstSeen), concTotal, concFirstSeen)
		}
		for i, wantID := range concIDs {
			if concFirstSeen[i] != wantID {
				t.Fatalf("concFirstSeen[%d]=%q, want %q (all=%v)", i, concFirstSeen[i], wantID, concFirstSeen)
			}
		}
	})

	// 10. W3A D9-bis missing tests on real Postgres:
	//     (a) lost lease mid-batch releases remaining & aborts;
	//     (b) row-locked E1 with expired claimed_until blocks worker 2 from claiming E2;
	//     (c) inverted created_at vs seq still publishes strictly by seq;
	//     (d) 1 poison + 49 same-aggregate events AND 50 distinct-aggregate poisons never starve healthy aggregates;
	//     (e) NATS down -> relay backoff caps claim attempts even under rapid triggers.
	t.Run("W3A_D9bis_RealPG", func(t *testing.T) {
		bisPrefix := runPrefix + "-d9bis-"
		t.Cleanup(func() {
			_, _ = pool.Exec(context.Background(), `DELETE FROM outbox_events WHERE id LIKE $1`, bisPrefix+"%")
		})
		cleanByPrefix := func(t *testing.T, prefix string) {
			t.Helper()
			t.Cleanup(func() {
				_, _ = pool.Exec(context.Background(), `DELETE FROM outbox_events WHERE id LIKE $1`, prefix+"%")
			})
		}
		insertEv := func(t *testing.T, id, aggID string, createdAt time.Time) {
			t.Helper()
			raw, _ := json.Marshal(events.ProductReviewedPayload{
				ProductID: aggID, ProductName: "Prod " + aggID, ShopID: "pg-shop-1", Decision: "APPROVE",
				OccurredAt: createdAt.Format(time.RFC3339Nano),
			})
			if err := pgStore.InsertOutboxEvent(ctx, store.OutboxEvent{
				ID: id, EventType: events.SubjectProductReviewed, AggregateID: aggID, PayloadJSON: raw, CreatedAt: createdAt,
			}); err != nil {
				t.Fatalf("InsertOutboxEvent %s: %v", id, err)
			}
		}

		t.Run("a_LostLeaseMidBatch_ReleasesRemainingAndAborts", func(t *testing.T) {
			pfx := bisPrefix + "a-"
			cleanByPrefix(t, pfx)
			t0 := time.Now().UTC().Add(-2 * time.Minute)
			e1 := pfx + "1"
			e2 := pfx + "2"
			e3 := pfx + "3"
			insertEv(t, e1, runPrefix+"-agg-a1", t0)
			insertEv(t, e2, runPrefix+"-agg-a2", t0.Add(time.Second))
			insertEv(t, e3, runPrefix+"-agg-a3", t0.Add(2*time.Second))

			var attempted []string
			n, err := pgStore.ProcessPendingOutbox(ctx, 10, func(_ context.Context, ev store.OutboxEvent) error {
				attempted = append(attempted, ev.ID)
				if ev.ID == e1 {
					// Simulate another worker stealing/replacing the lease while e1 is publishing.
					if _, uErr := pool.Exec(ctx, `UPDATE outbox_events SET claim_token = 'stolen-token' WHERE id = $1`, e1); uErr != nil {
						return uErr
					}
				}
				return nil
			})
			if err != nil {
				t.Fatalf("ProcessPendingOutbox unexpected err: %v", err)
			}
			if n != 0 {
				t.Fatalf("expected 0 marked published after losing lease on e1, got %d", n)
			}
			if len(attempted) != 1 || attempted[0] != e1 {
				t.Fatalf("expected batch to abort immediately after lost lease on %s, got attempted=%v", e1, attempted)
			}
			for _, remID := range []string{e2, e3} {
				var tok string
				var until *time.Time
				if qErr := pool.QueryRow(ctx, `SELECT claim_token, claimed_until FROM outbox_events WHERE id = $1`, remID).Scan(&tok, &until); qErr != nil {
					t.Fatalf("query %s: %v", remID, qErr)
				}
				if tok != "" || until != nil {
					t.Fatalf("remaining event %s still holds lease after mid-batch lease loss: token=%q until=%v", remID, tok, until)
				}
			}
		})

		t.Run("b_LockedExpiredRow_BlocksLaterSameAggregateEvent", func(t *testing.T) {
			pfx := bisPrefix + "b-"
			cleanByPrefix(t, pfx)
			t0 := time.Now().UTC().Add(-2 * time.Minute)
			e1 := pfx + "1"
			e2 := pfx + "2"
			eOther := pfx + "other"
			aggLock := runPrefix + "-agg-lock"
			insertEv(t, e1, aggLock, t0)
			insertEv(t, e2, aggLock, t0.Add(time.Second))
			insertEv(t, eOther, runPrefix+"-agg-free-b", t0.Add(2*time.Second))

			// E1 has an EXPIRED claimed_until, and tx1 holds FOR UPDATE lock on E1.
			if _, err := pool.Exec(ctx, `UPDATE outbox_events SET claimed_until = NOW() - INTERVAL '1 second', claim_token = 'expired-tok' WHERE id = $1`, e1); err != nil {
				t.Fatalf("set expired lease on e1: %v", err)
			}
			lockTx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatalf("begin lockTx: %v", err)
			}
			defer func() { _ = lockTx.Rollback(ctx) }()
			var lockedID string
			if err := lockTx.QueryRow(ctx, `SELECT id FROM outbox_events WHERE id = $1 FOR UPDATE`, e1).Scan(&lockedID); err != nil {
				t.Fatalf("lock e1 FOR UPDATE: %v", err)
			}

			// Worker 2 MUST NOT claim E2 while E1 (earlier, unpublished, row-locked) is skipped by SKIP LOCKED!
			var w2IDs []string
			n, err := pgStore.ProcessPendingOutbox(ctx, 10, func(_ context.Context, ev store.OutboxEvent) error {
				w2IDs = append(w2IDs, ev.ID)
				return nil
			})
			if err != nil {
				t.Fatalf("worker 2 ProcessPendingOutbox: %v", err)
			}
			if n != 1 || len(w2IDs) != 1 || w2IDs[0] != eOther {
				t.Fatalf("worker 2 while E1 row-locked with expired lease: want ONLY [%s], got n=%d ids=%v", eOther, n, w2IDs)
			}

			// Release lock on E1 -> now [E1, E2] claim and publish in strict order.
			if err := lockTx.Rollback(ctx); err != nil {
				t.Fatalf("rollback lockTx: %v", err)
			}
			var afterIDs []string
			nAfter, err := pgStore.ProcessPendingOutbox(ctx, 10, func(_ context.Context, ev store.OutboxEvent) error {
				afterIDs = append(afterIDs, ev.ID)
				return nil
			})
			if err != nil || nAfter != 2 || len(afterIDs) != 2 || afterIDs[0] != e1 || afterIDs[1] != e2 {
				t.Fatalf("after unlocking E1: want [%s %s], got n=%d ids=%v err=%v", e1, e2, nAfter, afterIDs, err)
			}
		})

		t.Run("c_CreatedAtInvertedVsSeq_OrdersBySeqOnly", func(t *testing.T) {
			pfx := bisPrefix + "c-"
			cleanByPrefix(t, pfx)
			tBase := time.Now().UTC().Add(-2 * time.Minute)
			aggSeq := runPrefix + "-agg-seq"
			e1 := pfx + "seq1"
			e2 := pfx + "seq2"
			// Insert E1 first (smaller seq) with LATER created_at, then E2 (larger seq) with EARLIER created_at.
			insertEv(t, e1, aggSeq, tBase.Add(10*time.Second))
			insertEv(t, e2, aggSeq, tBase)

			var got []string
			n, err := pgStore.ProcessPendingOutbox(ctx, 10, func(_ context.Context, ev store.OutboxEvent) error {
				got = append(got, ev.ID)
				return nil
			})
			if err != nil || n != 2 {
				t.Fatalf("ProcessPendingOutbox inverted created_at: n=%d err=%v got=%v", n, err, got)
			}
			if len(got) != 2 || got[0] != e1 || got[1] != e2 {
				t.Fatalf("expected strict seq order [%s %s], got %v (created_at leaked into ordering!)", e1, e2, got)
			}
		})

		t.Run("d_PoisonBackoff_DoesNotStarveHealthyAggregate", func(t *testing.T) {
			// d1: 1 poison + 49 events on SAME aggregate + 1 healthy aggregate (seq after all 50).
			pfx1 := bisPrefix + "d1-"
			pfx2 := bisPrefix + "d2-"
			cleanByPrefix(t, pfx1)
			cleanByPrefix(t, pfx2)
			t0 := time.Now().UTC().Add(-2 * time.Minute)
			poisonAgg := runPrefix + "-agg-poison-same"
			healthyAgg1 := runPrefix + "-agg-healthy-1"
			p0 := pfx1 + "p00"
			for i := 0; i < 50; i++ {
				insertEv(t, fmt.Sprintf("%sp%02d", pfx1, i), poisonAgg, t0.Add(time.Duration(i)*time.Millisecond))
			}
			h1 := pfx1 + "h1"
			insertEv(t, h1, healthyAgg1, t0.Add(time.Second))

			n1, err := pgStore.ProcessPendingOutbox(ctx, 50, func(_ context.Context, ev store.OutboxEvent) error {
				if ev.ID == p0 {
					return errors.New("poison non-infra error")
				}
				return nil
			})
			if err != nil || n1 != 0 {
				t.Fatalf("d1 pass 1: want (0, nil), got (%d, %v)", n1, err)
			}
			var p0Until *time.Time
			var p0Tok string
			if err := pool.QueryRow(ctx, `SELECT claimed_until, claim_token FROM outbox_events WHERE id = $1`, p0).Scan(&p0Until, &p0Tok); err != nil {
				t.Fatalf("query p0 backoff: %v", err)
			}
			if p0Until == nil || !p0Until.After(time.Now().UTC()) || p0Tok != "" {
				t.Fatalf("d1 p0 must be in future backoff with empty token, got until=%v tok=%q", p0Until, p0Tok)
			}

			// Immediately run pass 2 (limit=50): P0..P49 must NOT occupy LIMIT 50; H1 MUST publish!
			var d1Pass2 []string
			n2, err := pgStore.ProcessPendingOutbox(ctx, 50, func(_ context.Context, ev store.OutboxEvent) error {
				d1Pass2 = append(d1Pass2, ev.ID)
				return nil
			})
			if err != nil || n2 != 1 || len(d1Pass2) != 1 || d1Pass2[0] != h1 {
				t.Fatalf("d1 pass 2 starved healthy aggregate! want [%s], got n=%d ids=%v err=%v", h1, n2, d1Pass2, err)
			}
			// Clean d1 rows before starting d2 so d2's LIMIT 50 sees strictly d2's own rows.
			if _, err := pool.Exec(ctx, `DELETE FROM outbox_events WHERE id LIKE $1`, pfx1+"%"); err != nil {
				t.Fatalf("clean d1 rows: %v", err)
			}

			// d2: 50 poisons on 50 DISTINCT aggregates + 1 healthy aggregate (seq after all 50).
			for i := 0; i < 50; i++ {
				agg := fmt.Sprintf("%s-agg-poison-dist-%02d", runPrefix, i)
				insertEv(t, fmt.Sprintf("%sx%02d", pfx2, i), agg, t0.Add(time.Duration(i)*time.Millisecond))
			}
			h2 := pfx2 + "h2"
			insertEv(t, h2, runPrefix+"-agg-healthy-2", t0.Add(time.Second))

			nx1, err := pgStore.ProcessPendingOutbox(ctx, 50, func(_ context.Context, ev store.OutboxEvent) error {
				if strings.HasPrefix(ev.ID, pfx2+"x") {
					return errors.New("poison distinct agg")
				}
				return nil
			})
			if err != nil || nx1 != 0 {
				t.Fatalf("d2 pass 1: want (0, nil), got (%d, %v)", nx1, err)
			}

			// Immediately run pass 2 (limit=50): all 50 poisons are in backoff -> H2 MUST publish!
			var d2Pass2 []string
			nx2, err := pgStore.ProcessPendingOutbox(ctx, 50, func(_ context.Context, ev store.OutboxEvent) error {
				d2Pass2 = append(d2Pass2, ev.ID)
				return nil
			})
			if err != nil || nx2 != 1 || len(d2Pass2) != 1 || d2Pass2[0] != h2 {
				t.Fatalf("d2 pass 2 starved healthy aggregate! want [%s], got n=%d ids=%v err=%v", h2, nx2, d2Pass2, err)
			}
		})

		t.Run("e_NATSDown_RelayBackoffCapsClaimRate", func(t *testing.T) {
			pfx := bisPrefix + "e-"
			cleanByPrefix(t, pfx)
			insertEv(t, pfx+"natsdown", runPrefix+"-agg-natsdown", time.Now().UTC().Add(-time.Minute))

			var pubAttempts atomic.Int32
			downPub := &pgErrPublisher{
				err:      nats.ErrDisconnected,
				attempts: &pubAttempts,
			}
			trig := make(chan struct{}, 1)
			relay := events.NewOutboxRelay(pgStore, downPub, nil, 25*time.Millisecond, trig)

			runCtx, runCancel := context.WithTimeout(ctx, 1200*time.Millisecond)
			defer runCancel()

			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				relay.Run(runCtx)
			}()
			go func() {
				defer wg.Done()
				tk := time.NewTicker(20 * time.Millisecond)
				defer tk.Stop()
				for {
					select {
					case <-runCtx.Done():
						return
					case <-tk.C:
						select {
						case trig <- struct{}{}:
						default:
						}
					}
				}
			}()
			wg.Wait()

			gotAttempts := pubAttempts.Load()
			// With 250ms -> 500ms -> 1s backoff ignoring triggers, 1.2s allows at most ~3-4 attempts (vs 40+ without backoff).
			if gotAttempts < 1 || gotAttempts > 6 {
				t.Fatalf("NATS down for 1.2s with 20ms triggers: expected 1..6 backed-off attempts, got %d", gotAttempts)
			}
		})

		t.Run("f_NonInfraError_ErrorLogAtAttempts10WithoutPayload", func(t *testing.T) {
			pfx := bisPrefix + "f-"
			cleanByPrefix(t, pfx)
			logStore := store.NewPgStore(pool)
			var logBuf bytes.Buffer
			logStore.SetLogger(slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo})))

			const secretSentinel = "SECRET-PAYLOAD-NEVER-LOG-PG-10"
			evID := pfx + "poison10"
			raw, _ := json.Marshal(events.ProductReviewedPayload{
				ProductID: runPrefix + "-agg-p10", ProductName: secretSentinel, ShopID: "pg-shop-1", Decision: "APPROVE",
				OccurredAt: time.Now().UTC().Format(time.RFC3339),
			})
			if err := logStore.InsertOutboxEvent(ctx, store.OutboxEvent{
				ID: evID, EventType: events.SubjectProductReviewed, AggregateID: runPrefix + "-agg-p10", PayloadJSON: raw, CreatedAt: time.Now().UTC(),
			}); err != nil {
				t.Fatalf("insert poison10: %v", err)
			}

			// Attempt 9 (set attempts=8 -> fails -> attempts=9): MUST NOT log ERROR yet.
			if _, err := pool.Exec(ctx, `UPDATE outbox_events SET attempts = 8 WHERE id = $1`, evID); err != nil {
				t.Fatalf("seed attempts=8: %v", err)
			}
			_, err := logStore.ProcessPendingOutbox(ctx, 10, func(_ context.Context, _ store.OutboxEvent) error {
				return errors.New("bad payload schema")
			})
			if err != nil {
				t.Fatalf("attempt 9 process err: %v", err)
			}
			if strings.Contains(logBuf.String(), `"level":"ERROR"`) {
				t.Fatalf("attempt 9 should not emit ERROR log, got: %s", logBuf.String())
			}

			// Expire backoff so attempt 10 executes immediately -> MUST emit ERROR with event_id & event_type, NO payload!
			if _, err := pool.Exec(ctx, `UPDATE outbox_events SET claimed_until = NOW() - INTERVAL '1 second' WHERE id = $1`, evID); err != nil {
				t.Fatalf("expire backoff for attempt 10: %v", err)
			}
			logBuf.Reset()
			_, err = logStore.ProcessPendingOutbox(ctx, 10, func(_ context.Context, _ store.OutboxEvent) error {
				return errors.New("bad payload schema")
			})
			if err != nil {
				t.Fatalf("attempt 10 process err: %v", err)
			}
			logged := logBuf.String()
			if !strings.Contains(logged, `"level":"ERROR"`) || !strings.Contains(logged, evID) || !strings.Contains(logged, events.SubjectProductReviewed) {
				t.Fatalf("attempt 10 missing expected ERROR log fields: %s", logged)
			}
			if strings.Contains(logged, secretSentinel) {
				t.Fatalf("attempt 10 ERROR log leaked payload secret: %s", logged)
			}
		})

		t.Run("g_Migration000007_ReassignsSeqByCreatedAt_AfterMVCCUpdate_AndDropsOldIndex", func(t *testing.T) {
			// Spin up an isolated temporary database on the same Postgres server and apply 000001..000006 ONLY.
			u, err := url.Parse(dsn)
			if err != nil {
				t.Fatalf("parse TEST_PRODUCT_DB_URL: %v", err)
			}
			adminURL := *u
			adminURL.Path = "/postgres"
			adminConn, err := pgx.Connect(ctx, adminURL.String())
			if err != nil {
				t.Fatalf("connect admin postgres: %v", err)
			}
			dbName := "prod_mig07_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
			if _, err := adminConn.Exec(ctx, "CREATE DATABASE "+dbName); err != nil {
				_ = adminConn.Close(ctx)
				t.Fatalf("create db %s: %v", dbName, err)
			}
			_ = adminConn.Close(ctx)

			testURL := *u
			testURL.Path = "/" + dbName
			migPool, err := pgxpool.New(ctx, testURL.String())
			if err != nil {
				t.Fatalf("pgxpool.New(%s): %v", dbName, err)
			}
			t.Cleanup(func() {
				migPool.Close()
				dropCtx, dropCancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer dropCancel()
				if dc, dErr := pgx.Connect(dropCtx, adminURL.String()); dErr == nil {
					_, _ = dc.Exec(dropCtx, "DROP DATABASE IF EXISTS "+dbName+" WITH (FORCE)")
					_ = dc.Close(dropCtx)
				}
			})

			// Apply 000001..000008 manually and record them in schema_migrations so migrations.Apply runs 000009+.
			if _, err := migPool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (filename TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`); err != nil {
				t.Fatalf("create schema_migrations: %v", err)
			}
			migDir := filepath.Join("..", "..", "migrations")
			entries, err := os.ReadDir(migDir)
			if err != nil {
				t.Fatalf("ReadDir migrations: %v", err)
			}
			var preFiles []string
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") && e.Name() < "000009_" {
					preFiles = append(preFiles, e.Name())
				}
			}
			sort.Strings(preFiles)
			for _, fname := range preFiles {
				raw, rErr := os.ReadFile(filepath.Join(migDir, fname))
				if rErr != nil {
					t.Fatalf("read %s: %v", fname, rErr)
				}
				if _, eErr := migPool.Exec(ctx, string(raw)); eErr != nil {
					t.Fatalf("apply %s: %v", fname, eErr)
				}
				if _, iErr := migPool.Exec(ctx, `INSERT INTO schema_migrations (filename) VALUES ($1)`, fname); iErr != nil {
					t.Fatalf("record %s: %v", fname, iErr)
				}
			}

			t0 := time.Date(2026, 11, 24, 17, 0, 0, 0, time.UTC)
			p1, _ := json.Marshal(events.ProductReviewedPayload{ProductID: "prod-mig-1", ProductName: "P1", ShopID: "s1", Decision: "REJECT", OccurredAt: t0.Format(time.RFC3339)})
			p2, _ := json.Marshal(events.ProductReviewedPayload{ProductID: "prod-mig-1", ProductName: "P1", ShopID: "s1", Decision: "APPROVE", OccurredAt: t0.Add(5 * time.Second).Format(time.RFC3339)})
			pWS, _ := json.Marshal(events.ProductReviewedPayload{ProductID: "   ", ProductName: "WS", ShopID: "s1", Decision: "APPROVE", OccurredAt: t0.Add(10 * time.Second).Format(time.RFC3339)})
			if _, err := migPool.Exec(ctx, `
				INSERT INTO outbox_events (id, event_type, payload_json, created_at)
				VALUES ('ev-mig-1', 'product.reviewed', $1::jsonb, $2),
				       ('ev-mig-2', 'product.reviewed', $3::jsonb, $4),
				       ('ev-mig-ws', 'product.reviewed', $5::jsonb, $6)
			`, string(p1), t0, string(p2), t0.Add(5*time.Second), string(pWS), t0.Add(10*time.Second)); err != nil {
				t.Fatalf("insert pre-000007 outbox rows: %v", err)
			}

			// Simulate pre-3a failed publish attempt on older E1 -> MVCC moves E1 physical ctid AFTER E2!
			if _, err := migPool.Exec(ctx, `UPDATE outbox_events SET attempts = attempts + 1, last_error = 'pre-3a transient' WHERE id = 'ev-mig-1'`); err != nil {
				t.Fatalf("update E1 to move ctid: %v", err)
			}
			var ctid1, ctid2 string
			_ = migPool.QueryRow(ctx, `SELECT ctid::text FROM outbox_events WHERE id = 'ev-mig-1'`).Scan(&ctid1)
			_ = migPool.QueryRow(ctx, `SELECT ctid::text FROM outbox_events WHERE id = 'ev-mig-2'`).Scan(&ctid2)
			if ctid1 <= ctid2 {
				t.Fatalf("expected MVCC update to place E1 ctid (%s) after E2 ctid (%s)", ctid1, ctid2)
			}

			// Apply 000007 migration via migrations.Apply.
			if err := migrations.Apply(ctx, migPool); err != nil {
				t.Fatalf("migrations.Apply(000007): %v", err)
			}

			// Verify E1 publishes BEFORE E2 despite physical ctid(E1) > ctid(E2)!
			migStore := store.NewPgStore(migPool)
			var got []string
			n, err := migStore.ProcessPendingOutbox(ctx, 10, func(_ context.Context, ev store.OutboxEvent) error {
				got = append(got, ev.ID)
				return nil
			})
			if err != nil || n != 3 {
				t.Fatalf("ProcessPendingOutbox post-000007 = (%d, %v), got=%v", n, err, got)
			}
			if fmt.Sprint(got) != "[ev-mig-1 ev-mig-2 ev-mig-ws]" {
				t.Fatalf("post-000007 publish order = %v, want [ev-mig-1 ev-mig-2 ev-mig-ws] (MVCC ctid inverted seq!)", got)
			}

			// Old created_at partial index from 000005 MUST be dropped (P2-8).
			var oldIdxCount int
			if err := migPool.QueryRow(ctx, `SELECT COUNT(*) FROM pg_indexes WHERE schemaname = current_schema() AND indexname = 'idx_product_outbox_events_unpublished'`).Scan(&oldIdxCount); err != nil {
				t.Fatalf("check old index dropped: %v", err)
			}
			if oldIdxCount != 0 {
				t.Fatalf("expected legacy idx_product_outbox_events_unpublished to be dropped, found %d", oldIdxCount)
			}

			// Whitespace product_id must backfill to empty string '' (P3-11).
			var wsAgg string
			if err := migPool.QueryRow(ctx, `SELECT aggregate_id FROM outbox_events WHERE id = 'ev-mig-ws'`).Scan(&wsAgg); err != nil {
				t.Fatalf("query ev-mig-ws aggregate_id: %v", err)
			}
			if wsAgg != "" {
				t.Fatalf("whitespace product_id backfilled to %q, want empty string", wsAgg)
			}
		})
	})

	// 9. W3B (E5–E14, E6-bis, E10-bis): real Postgres reviews race (10 goroutines), ratings, moderation, prohibited category tree, rescan, E13 backfill & stubs.
	t.Run("W3B_Reviews_Moderation_Discontinued_RealPG", func(t *testing.T) {
		t.Run("CreateReview_10GoroutineRace_And_RatingsConsistency", func(t *testing.T) {
			srv := New(pgStore, nil)
			cat, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
				Name: "PG W3B Rev Cat " + runPrefix, Slug: runPrefix + "-w3b-rev", CommissionRateBps: 500,
			})
			if err != nil {
				t.Fatalf("CreateCategory: %v", err)
			}
			prod, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
				CategoryId: cat.GetCategory().GetId(),
				Name:       "PG W3B Reviewed Product " + runPrefix,
				Slug:       runPrefix + "-w3b-reviewed",
				Skus:       []*productv1.SKUInput{{SkuCode: runPrefix + "-REV-1", Name: "L / Đen", PriceVnd: 250000, Stock: 20}},
			})
			if err != nil {
				t.Fatalf("CreateProduct: %v", err)
			}
			prodID := prod.GetProduct().GetId()
			skuID := prod.GetProduct().GetSkus()[0].GetId()
			if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
				ProductId: prodID,
				Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
			}); err != nil {
				t.Fatalf("Approve: %v", err)
			}
			var updatedAtBefore time.Time
			if err := pool.QueryRow(ctx, `SELECT updated_at FROM products WHERE id = $1`, prodID).Scan(&updatedAtBefore); err != nil {
				t.Fatalf("query products.updated_at before review: %v", err)
			}

			raceOrderID := runPrefix + "-ord-race"
			raceItemID := runPrefix + "-oi-race"
			secondOrderID := runPrefix + "-ord-second"
			secondItemID := runPrefix + "-oi-second"
			thirdItemID := runPrefix + "-oi-third"

			srv.SetOrderClient(&fakeOrderClient{
				fn: func(_ context.Context, req *orderv1.GetOrderRequest) (*orderv1.GetOrderResponse, error) {
					switch req.GetOrderId() {
					case raceOrderID:
						return &orderv1.GetOrderResponse{Order: &orderv1.Order{
							Id: raceOrderID, BuyerId: "pg-buyer-1", ShopId: "pg-shop-1",
							Status: orderv1.OrderStatus_ORDER_STATUS_COMPLETED,
							Items:  []*orderv1.OrderItem{{Id: raceItemID, OrderId: raceOrderID, ProductId: prodID, SkuId: skuID, SkuName: "L / Đen"}},
						}}, nil
					case secondOrderID:
						return &orderv1.GetOrderResponse{Order: &orderv1.Order{
							Id: secondOrderID, BuyerId: "pg-buyer-2", ShopId: "pg-shop-1",
							Status: orderv1.OrderStatus_ORDER_STATUS_COMPLETED,
							Items: []*orderv1.OrderItem{
								{Id: secondItemID, OrderId: secondOrderID, ProductId: prodID, SkuId: skuID, SkuName: "L / Đen"},
								{Id: thirdItemID, OrderId: secondOrderID, ProductId: prodID, SkuId: skuID, SkuName: "L / Đen"},
							},
						}}, nil
					default:
						return nil, status.Error(codes.NotFound, "not found")
					}
				},
			})
			srv.SetUserClient(&fakeUserClient{
				fn: func(_ context.Context, req *userv1.GetProfileRequest) (*userv1.GetProfileResponse, error) {
					if req.GetUserId() == "pg-buyer-1" {
						return &userv1.GetProfileResponse{User: &userv1.User{Id: "pg-buyer-1", FullName: "Nguyễn Minh Linh"}}, nil
					}
					return nil, status.Error(codes.Unavailable, "user svc down")
				},
			})

			// 10 goroutines race on the EXACT same order_item_id on real Postgres -> 1 OK, 9 AlreadyExists, review_count +1, 1 outbox!
			buyer1Ctx := ctxWithRoleAndToken("pg-buyer-1", "buyer", "", "Bearer pg-b1")
			const workers = 10
			var okCnt, dupCnt atomic.Int32
			otherErrCh := make(chan error, workers)
			start := make(chan struct{})
			var wg sync.WaitGroup
			wg.Add(workers)
			for i := 0; i < workers; i++ {
				go func(idx int) {
					defer wg.Done()
					<-start
					_, rErr := srv.CreateReview(buyer1Ctx, &productv1.CreateReviewRequest{
						OrderId:     raceOrderID,
						OrderItemId: raceItemID,
						Rating:      5,
						Comment:     fmt.Sprintf("Rất tốt #%d", idx),
						ImageUrls:   []string{"https://cdn.example.com/ok.jpg"},
					})
					if rErr == nil {
						okCnt.Add(1)
					} else if status.Code(rErr) == codes.AlreadyExists {
						dupCnt.Add(1)
					} else {
						otherErrCh <- rErr
					}
				}(i)
			}
			close(start)
			wg.Wait()
			close(otherErrCh)
			for e := range otherErrCh {
				t.Fatalf("unexpected concurrent CreateReview error: %v", e)
			}
			if okCnt.Load() != 1 || dupCnt.Load() != 9 {
				t.Fatalf("10-goroutine CreateReview race: ok=%d alreadyExists=%d, want 1 and 9", okCnt.Load(), dupCnt.Load())
			}

			var dbReviewCount int32
			var dbRatingSum int64
			var updatedAtAfter time.Time
			if err := pool.QueryRow(ctx, `SELECT review_count, rating_sum, updated_at FROM products WHERE id = $1`, prodID).Scan(&dbReviewCount, &dbRatingSum, &updatedAtAfter); err != nil {
				t.Fatalf("query products rating: %v", err)
			}
			if dbReviewCount != 1 || dbRatingSum != 5 {
				t.Fatalf("after race: review_count=%d rating_sum=%d, want 1 and 5", dbReviewCount, dbRatingSum)
			}
			if !updatedAtAfter.Equal(updatedAtBefore) {
				t.Fatalf("PG IncrementProductRating mutated products.updated_at: before=%v after=%v", updatedAtBefore, updatedAtAfter)
			}
			var outboxCnt int64
			if err := pool.QueryRow(ctx,
				`SELECT COUNT(*) FROM outbox_events WHERE event_type = 'review.created' AND payload_json->>'order_item_id' = $1`,
				raceItemID,
			).Scan(&outboxCnt); err != nil {
				t.Fatalf("count review.created outbox: %v", err)
			}
			if outboxCnt != 1 {
				t.Fatalf("expected EXACTLY 1 review.created outbox row for %s, got %d", raceItemID, outboxCnt)
			}

			// Add 2 concurrent reviews (ratings 5 and 4) on different order items of the same product -> sum=14, count=3, avg_x100=467.
			buyer2Ctx := ctxWithRoleAndToken("pg-buyer-2", "buyer", "", "Bearer pg-b2")
			start2 := make(chan struct{})
			var wg2 sync.WaitGroup
			errCh2 := make(chan error, 2)
			wg2.Add(2)
			go func() {
				defer wg2.Done()
				<-start2
				_, e := srv.CreateReview(buyer2Ctx, &productv1.CreateReviewRequest{
					OrderId: secondOrderID, OrderItemId: secondItemID, Rating: 5, Comment: "Chuẩn size",
				})
				if e != nil {
					errCh2 <- e
				}
			}()
			go func() {
				defer wg2.Done()
				<-start2
				_, e := srv.CreateReview(buyer2Ctx, &productv1.CreateReviewRequest{
					OrderId: secondOrderID, OrderItemId: thirdItemID, Rating: 4, Comment: "Giao hơi lâu",
				})
				if e != nil {
					errCh2 <- e
				}
			}()
			close(start2)
			wg2.Wait()
			close(errCh2)
			for e := range errCh2 {
				t.Fatalf("concurrent distinct-item review failed: %v", e)
			}

			// Verify all public Product read paths + ListProductReviews agree (rating_avg_x100=467, review_count=3) and E6-bis redaction holds.
			gp, err := srv.GetProduct(ctx, &productv1.GetProductRequest{Slug: runPrefix + "-w3b-reviewed"})
			if err != nil || gp.GetProduct().GetReviewCount() != 3 || gp.GetProduct().GetRatingAvgX100() != 467 {
				t.Fatalf("PG GetProduct rating = %d/%d err=%v, want 3/467", gp.GetProduct().GetReviewCount(), gp.GetProduct().GetRatingAvgX100(), err)
			}
			lp, err := srv.ListProducts(ctx, &productv1.ListProductsRequest{SkuIds: []string{skuID}})
			if err != nil || len(lp.GetProducts()) != 1 || lp.GetProducts()[0].GetReviewCount() != 3 || lp.GetProducts()[0].GetRatingAvgX100() != 467 {
				t.Fatalf("PG ListProducts(sku_ids) rating = %+v err=%v", lp.GetProducts(), err)
			}
			pubRevs, err := srv.ListProductReviews(ctx, &productv1.ListProductReviewsRequest{ProductId: prodID, Page: 1, PageSize: 10})
			if err != nil || pubRevs.GetTotal() != 3 || pubRevs.GetReviewCount() != 3 || pubRevs.GetRatingAvgX100() != 467 {
				t.Fatalf("PG ListProductReviews = total:%d count:%d avg:%d err=%v", pubRevs.GetTotal(), pubRevs.GetReviewCount(), pubRevs.GetRatingAvgX100(), err)
			}
			for _, r := range pubRevs.GetReviews() {
				if r.GetBuyerId() != "" || r.GetOrderId() != "" || r.GetOrderItemId() != "" || r.GetSkuId() != "" {
					t.Fatalf("PG E6-bis leaked private review fields: %+v", r)
				}
			}
			ordRevs, err := srv.ListOrderReviews(buyer1Ctx, &productv1.ListOrderReviewsRequest{OrderId: raceOrderID})
			if err != nil || len(ordRevs.GetReviews()) != 1 || ordRevs.GetReviews()[0].GetOrderItemId() != raceItemID || ordRevs.GetReviews()[0].GetBuyerName() != "N. Linh" {
				t.Fatalf("PG ListOrderReviews = %+v err=%v", ordRevs.GetReviews(), err)
			}
		})

		t.Run("Moderation_ProhibitedTree_Rescan_And_Discontinued_RealPG", func(t *testing.T) {
			srv := New(pgStore, nil)

			parentCat, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
				Name: "PG Prohib Parent " + runPrefix, Slug: runPrefix + "-prohib-p", CommissionRateBps: 500, IsProhibited: true,
			})
			if err != nil {
				t.Fatalf("CreateCategory prohib parent: %v", err)
			}
			childCat, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
				Name: "PG Prohib Child " + runPrefix, Slug: runPrefix + "-prohib-c", ParentId: parentCat.GetCategory().GetId(), CommissionRateBps: 500,
			})
			if err != nil {
				t.Fatalf("CreateCategory prohib child: %v", err)
			}
			if _, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
				CategoryId: childCat.GetCategory().GetId(),
				Name:       "PG Child Prohib Product",
				Skus:       []*productv1.SKUInput{{SkuCode: runPrefix + "-PRH", PriceVnd: 100000, Stock: 5}},
			}); status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "PROHIBITED_CATEGORY") {
				t.Fatalf("PG CreateProduct in prohibited child: want PROHIBITED_CATEGORY, got %v", err)
			}

			normCat, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
				Name: "PG Mod Norm " + runPrefix, Slug: runPrefix + "-mod-norm", CommissionRateBps: 500,
			})
			if err != nil {
				t.Fatalf("CreateCategory norm: %v", err)
			}
			normCatID := normCat.GetCategory().GetId()

			// Seed 2 APPROVED products BEFORE creating banned keywords.
			pBlock, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
				CategoryId: normCatID,
				Name:       "PG Rescan " + runPrefix + " độc hại xyz",
				Slug:       runPrefix + "-rescan-block",
				Skus:       []*productv1.SKUInput{{SkuCode: runPrefix + "-RSB", PriceVnd: 100000, Stock: 5}},
			})
			if err != nil {
				t.Fatalf("CreateProduct pBlock: %v", err)
			}
			pReview, err := srv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
				CategoryId: normCatID,
				Name:       "PG Rescan " + runPrefix + " bảo hành trọn đời xyz",
				Slug:       runPrefix + "-rescan-review",
				Skus:       []*productv1.SKUInput{{SkuCode: runPrefix + "-RSR", PriceVnd: 100000, Stock: 5}},
			})
			if err != nil {
				t.Fatalf("CreateProduct pReview: %v", err)
			}
			for _, pid := range []string{pBlock.GetProduct().GetId(), pReview.GetProduct().GetId()} {
				if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
					ProductId: pid, Decision: productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
				}); err != nil {
					t.Fatalf("Approve %s: %v", pid, err)
				}
			}

			// Create unique-scoped keywords so other tests' products are unaffected.
			kwBlk, err := srv.CreateBannedKeyword(adminCtx, &productv1.CreateBannedKeywordRequest{
				Term: runPrefix + " độc hại xyz", Action: productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_BLOCK,
			})
			if err != nil {
				t.Fatalf("CreateBannedKeyword BLOCK: %v", err)
			}
			if _, err := srv.CreateBannedKeyword(adminCtx, &productv1.CreateBannedKeywordRequest{
				Term: strings.ToUpper(runPrefix) + "   ĐỘC HẠI XYZ", Action: productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_BLOCK,
			}); status.Code(err) != codes.AlreadyExists {
				t.Fatalf("PG duplicate normalized_term: want AlreadyExists, got %v", err)
			}
			kwRev, err := srv.CreateBannedKeyword(adminCtx, &productv1.CreateBannedKeywordRequest{
				Term: runPrefix + " bảo hành trọn đời xyz", Action: productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_REVIEW,
			})
			if err != nil {
				t.Fatalf("CreateBannedKeyword REVIEW: %v", err)
			}

			// Preview finds pBlock.
			prev, err := srv.PreviewBannedKeyword(adminCtx, &productv1.PreviewBannedKeywordRequest{Term: runPrefix + " độc hại xyz"})
			if err != nil || prev.GetAffectedCount() != 1 {
				t.Fatalf("PG PreviewBannedKeyword: affected=%d err=%v, want 1", prev.GetAffectedCount(), err)
			}

			// Rescan scoped to these 2 keyword IDs (loop cursor until has_more=false).
			kwIDs := []string{kwBlk.GetKeyword().GetId(), kwRev.GetKeyword().GetId()}
			var blockedTot, flaggedTot int32
			cursor := ""
			for {
				res, rErr := srv.RescanProductsForBannedContent(adminCtx, &productv1.RescanProductsForBannedContentRequest{
					KeywordIds: kwIDs, Cursor: cursor, Limit: 500,
				})
				if rErr != nil {
					t.Fatalf("PG Rescan: %v", rErr)
				}
				blockedTot += res.GetBlockedCount()
				flaggedTot += res.GetFlaggedCount()
				if !res.GetHasMore() {
					break
				}
				cursor = res.GetNextCursor()
			}
			if blockedTot != 1 || flaggedTot != 1 {
				t.Fatalf("PG Rescan totals: blocked=%d flagged=%d, want 1 and 1", blockedTot, flaggedTot)
			}

			// Verify 1 REJECT product.reviewed outbox event for pBlock; re-run emits 0 more!
			var rejOutbox int64
			if err := pool.QueryRow(ctx,
				`SELECT COUNT(*) FROM outbox_events WHERE event_type = 'product.reviewed' AND payload_json->>'product_id' = $1 AND payload_json->>'decision' = 'REJECT'`,
				pBlock.GetProduct().GetId(),
			).Scan(&rejOutbox); err != nil {
				t.Fatalf("count REJECT outbox: %v", err)
			}
			if rejOutbox != 1 {
				t.Fatalf("PG Rescan REJECT outbox count = %d, want 1", rejOutbox)
			}
			cursor = ""
			for {
				res, rErr := srv.RescanProductsForBannedContent(adminCtx, &productv1.RescanProductsForBannedContentRequest{
					KeywordIds: kwIDs, Cursor: cursor, Limit: 500,
				})
				if rErr != nil {
					t.Fatalf("PG Rescan rerun: %v", rErr)
				}
				if res.GetBlockedCount() != 0 || res.GetFlaggedCount() != 0 {
					t.Fatalf("PG Rescan rerun not idempotent: blocked=%d flagged=%d", res.GetBlockedCount(), res.GetFlaggedCount())
				}
				if !res.GetHasMore() {
					break
				}
				cursor = res.GetNextCursor()
			}
			var rejOutboxAfter int64
			_ = pool.QueryRow(ctx,
				`SELECT COUNT(*) FROM outbox_events WHERE event_type = 'product.reviewed' AND payload_json->>'product_id' = $1 AND payload_json->>'decision' = 'REJECT'`,
				pBlock.GetProduct().GetId(),
			).Scan(&rejOutboxAfter)
			if rejOutboxAfter != 1 {
				t.Fatalf("PG Rescan rerun emitted duplicate REJECT outbox: %d", rejOutboxAfter)
			}
			// Item 9 on real Postgres: Vietnamese rejection_reason recorded on product & outbox.
			pBlockAfter, _ := srv.GetProduct(adminCtx, &productv1.GetProductRequest{Id: pBlock.GetProduct().GetId()})
			wantVietReason := "Nội dung chứa từ khoá cấm: " + runPrefix + " độc hại xyz"
			if pBlockAfter.GetProduct().GetRejectionReason() != wantVietReason {
				t.Fatalf("PG Rescan rejection_reason = %q, want %q", pBlockAfter.GetProduct().GetRejectionReason(), wantVietReason)
			}

			// E13 on real Postgres: pBlock was previously approved and is now REJECTED -> discontinued stub with EMPTY name!
			stubRej, err := srv.GetProduct(ctx, &productv1.GetProductRequest{
				Slug: runPrefix + "-rescan-block", IncludeDiscontinuedStub: true,
			})
			if err != nil || !stubRej.GetDiscontinued() || stubRej.GetProduct() != nil || stubRej.GetDiscontinuedCategoryId() != normCatID || stubRej.GetDiscontinuedName() != "" {
				t.Fatalf("PG E13 REJECTED stub = %+v err=%v", stubRej, err)
			}
			// Soft-delete pBlock -> still discontinued stub with EMPTY name!
			if _, err := srv.DeleteProduct(sellerCtx, &productv1.DeleteProductRequest{Id: pBlock.GetProduct().GetId()}); err != nil {
				t.Fatalf("DeleteProduct pBlock: %v", err)
			}
			stubDel, err := srv.GetProduct(ctx, &productv1.GetProductRequest{
				Slug: runPrefix + "-rescan-block", IncludeDiscontinuedStub: true,
			})
			if err != nil || !stubDel.GetDiscontinued() || stubDel.GetProduct() != nil || stubDel.GetDiscontinuedCategoryId() != normCatID || stubDel.GetDiscontinuedName() != "" {
				t.Fatalf("PG E13 deleted stub = %+v err=%v", stubDel, err)
			}
		})

		// Verify 000008 migration backfill of first_approved_at on an isolated pre-000008 DB (E13 DB evidence).
		t.Run("Migration000008_FirstApprovedAt_Backfill_RealPG", func(t *testing.T) {
			u, err := url.Parse(dsn)
			if err != nil {
				t.Fatalf("parse dsn: %v", err)
			}
			adminURL := *u
			adminURL.Path = "/postgres"
			dbName := "product_mig0008_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]

			adminConn, err := pgx.Connect(ctx, adminURL.String())
			if err != nil {
				t.Fatalf("connect admin postgres: %v", err)
			}
			if _, err := adminConn.Exec(ctx, "CREATE DATABASE "+dbName); err != nil {
				_ = adminConn.Close(ctx)
				t.Fatalf("CREATE DATABASE %s: %v", dbName, err)
			}
			_ = adminConn.Close(ctx)

			testURL := *u
			testURL.Path = "/" + dbName
			migPool, err := pgxpool.New(ctx, testURL.String())
			if err != nil {
				t.Fatalf("pgxpool.New(%s): %v", dbName, err)
			}
			t.Cleanup(func() {
				migPool.Close()
				dropCtx, dropCancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer dropCancel()
				if dc, dErr := pgx.Connect(dropCtx, adminURL.String()); dErr == nil {
					_, _ = dc.Exec(dropCtx, "DROP DATABASE IF EXISTS "+dbName+" WITH (FORCE)")
					_ = dc.Close(dropCtx)
				}
			})

			// Apply 000001..000009 manually so migrations.Apply runs 000010+.
			if _, err := migPool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (filename TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`); err != nil {
				t.Fatalf("create schema_migrations: %v", err)
			}
			migDir := filepath.Join("..", "..", "migrations")
			entries, err := os.ReadDir(migDir)
			if err != nil {
				t.Fatalf("ReadDir migrations: %v", err)
			}
			var preFiles []string
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") && e.Name() < "000010_" {
					preFiles = append(preFiles, e.Name())
				}
			}
			sort.Strings(preFiles)
			for _, fname := range preFiles {
				raw, rErr := os.ReadFile(filepath.Join(migDir, fname))
				if rErr != nil {
					t.Fatalf("read %s: %v", fname, rErr)
				}
				if _, eErr := migPool.Exec(ctx, string(raw)); eErr != nil {
					t.Fatalf("apply %s: %v", fname, eErr)
				}
				if _, iErr := migPool.Exec(ctx, `INSERT INTO schema_migrations (filename) VALUES ($1)`, fname); iErr != nil {
					t.Fatalf("record %s: %v", fname, iErr)
				}
			}

			// Seed pre-000008 data:
			// 1) prod-seeded-live: APPROVED live (no outbox row) -> must backfill first_approved_at != NULL.
			// 2) prod-hidden-was-approved: HIDDEN with historical APPROVE outbox row -> must backfill first_approved_at != NULL -> stub WITH name!
			// 3) prod-hidden-never-approved: HIDDEN with NO APPROVE outbox row -> first_approved_at MUST stay NULL -> 404!
			// 4) prod-pending: PENDING -> first_approved_at MUST stay NULL -> 404!
			if _, err := migPool.Exec(ctx, `INSERT INTO categories (id, name, slug, commission_rate_bps, sort_order) VALUES ('cat-m8', 'M8', 'm8', 500, 0)`); err != nil {
				t.Fatalf("seed category: %v", err)
			}
			if _, err := migPool.Exec(ctx, `
				INSERT INTO products (id, shop_id, category_id, name, description, images, status, min_price_vnd, max_price_vnd, slug, search_text)
				VALUES
					('prod-seeded-live', 's1', 'cat-m8', 'Live Seeded', '', '[]'::jsonb, 'APPROVED', 100000, 100000, 'live-seeded', 'live seeded'),
					('prod-hidden-was-approved', 's1', 'cat-m8', 'Hidden Was Live', '', '[]'::jsonb, 'HIDDEN', 100000, 100000, 'hidden-was-live', 'hidden was live'),
					('prod-hidden-never-approved', 's1', 'cat-m8', 'Hidden Never Live', '', '[]'::jsonb, 'HIDDEN', 100000, 100000, 'hidden-never-live', 'hidden never live'),
					('prod-pending', 's1', 'cat-m8', 'Draft Pending', '', '[]'::jsonb, 'PENDING', 100000, 100000, 'draft-m8', 'draft m8')
			`); err != nil {
				t.Fatalf("seed pre-000008 products: %v", err)
			}
			appPayload, _ := json.Marshal(events.ProductReviewedPayload{
				ProductID: "prod-hidden-was-approved", ProductName: "Hidden Was Live", ShopID: "s1", Decision: "APPROVE", OccurredAt: "2026-11-20T00:00:00Z",
			})
			if _, err := migPool.Exec(ctx, `
				INSERT INTO outbox_events (id, event_type, aggregate_id, payload_json, created_at)
				VALUES ('ev-m8-app', 'product.reviewed', 'prod-hidden-was-approved', $1::jsonb, '2026-11-20T00:00:00Z')
			`, string(appPayload)); err != nil {
				t.Fatalf("seed approve outbox: %v", err)
			}

			if err := migrations.Apply(ctx, migPool); err != nil {
				t.Fatalf("migrations.Apply(000008): %v", err)
			}

			migSrv := New(store.NewPgStore(migPool), nil)
			// prod-hidden-was-approved -> 200 discontinued stub WITH name!
			stub, err := migSrv.GetProduct(ctx, &productv1.GetProductRequest{Slug: "hidden-was-live", IncludeDiscontinuedStub: true})
			if err != nil || !stub.GetDiscontinued() || stub.GetDiscontinuedCategoryId() != "cat-m8" || stub.GetDiscontinuedName() != "Hidden Was Live" {
				t.Fatalf("backfilled hidden-was-live stub = %+v err=%v", stub, err)
			}
			// prod-hidden-never-approved & prod-pending -> 404 NotFound (proving DB evidence, never inferred from HIDDEN status!).
			if _, err := migSrv.GetProduct(ctx, &productv1.GetProductRequest{Slug: "hidden-never-live", IncludeDiscontinuedStub: true}); status.Code(err) != codes.NotFound {
				t.Fatalf("hidden-never-live: want NotFound, got %v", err)
			}
			if _, err := migSrv.GetProduct(ctx, &productv1.GetProductRequest{Slug: "draft-m8", IncludeDiscontinuedStub: true}); status.Code(err) != codes.NotFound {
				t.Fatalf("draft-m8: want NotFound, got %v", err)
			}
			// prod-seeded-live has first_approved_at populated -> hiding it later yields discontinued stub!
			var liveFirstApproved *time.Time
			if err := migPool.QueryRow(ctx, `SELECT first_approved_at FROM products WHERE id = 'prod-seeded-live'`).Scan(&liveFirstApproved); err != nil || liveFirstApproved == nil {
				t.Fatalf("prod-seeded-live first_approved_at = %v err=%v, want non-nil", liveFirstApproved, err)
			}
		})

		// RevB P1-2: Real-Postgres TOCTOU concurrency tests for UpdateProduct.
		// Uses a store wrapper hook to deterministically interleave between pre-tx read (buggy old code)
		// and tx row lock (fixed code), proving zero reliance on timing luck.
		t.Run("RevB_P1_2_UpdateProduct_TOCTOU_RealPG", func(t *testing.T) {
			cat, err := New(pgStore, nil).CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
				Name: "PG TOCTOU Cat " + runPrefix, Slug: runPrefix + "-toctou", CommissionRateBps: 500,
			})
			if err != nil {
				t.Fatalf("CreateCategory: %v", err)
			}
			catID := cat.GetCategory().GetId()

			// (a) Admin REJECT interleaves during Seller UpdateProduct -> final status MUST NOT be APPROVED!
			t.Run("AdminRejectInterleavesSellerUpdate_NeverEndsApproved", func(t *testing.T) {
				baseSrv := New(pgStore, nil)
				prod, err := baseSrv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
					CategoryId: catID,
					Name:       "PG TOCTOU Reject " + runPrefix,
					Slug:       runPrefix + "-toctou-rej",
					Skus:       []*productv1.SKUInput{{SkuCode: runPrefix + "-TR1", Name: "M", PriceVnd: 100000, Stock: 10}},
				})
				if err != nil {
					t.Fatalf("CreateProduct: %v", err)
				}
				prodID := prod.GetProduct().GetId()
				if _, err := baseSrv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
					ProductId: prodID, Decision: productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
				}); err != nil {
					t.Fatalf("Approve: %v", err)
				}

				readReached := make(chan struct{})
				releaseSeller := make(chan struct{})
				hookStore := &pgTOCTOUStore{
					Store:        pgStore,
					targetID:     prodID,
					afterPreRead: func() { close(readReached); <-releaseSeller },
					afterRowLock: func() { close(readReached); <-releaseSeller },
				}
				sellerSrv := New(hookStore, nil)

				var sellerWG sync.WaitGroup
				sellerWG.Add(1)
				go func() {
					defer sellerWG.Done()
					_, _ = sellerSrv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
						Id:          prodID,
						Description: "Seller concurrent edit",
					})
				}()

				<-readReached
				adminDone := make(chan error, 1)
				go func() {
					_, aErr := baseSrv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
						ProductId: prodID,
						Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_REJECT,
						Reason:    "admin takedown during update",
					})
					adminDone <- aErr
				}()
				// If old code paused outside tx, admin finishes immediately before releaseSeller;
				// if new code holds FOR UPDATE, admin blocks until seller commits then applies REJECT.
				select {
				case aErr := <-adminDone:
					if aErr != nil {
						t.Fatalf("AdminReviewProduct(REJECT): %v", aErr)
					}
					close(releaseSeller)
					sellerWG.Wait()
				case <-time.After(120 * time.Millisecond):
					// New code path: admin is legitimately waiting on Postgres FOR UPDATE lock held by seller.
					close(releaseSeller)
					sellerWG.Wait()
					if aErr := <-adminDone; aErr != nil {
						t.Fatalf("AdminReviewProduct(REJECT) after seller lock: %v", aErr)
					}
				}

				var finalStatus string
				if err := pool.QueryRow(ctx, `SELECT status FROM products WHERE id = $1`, prodID).Scan(&finalStatus); err != nil {
					t.Fatalf("query final status: %v", err)
				}
				if finalStatus == store.ProductStatusApproved {
					t.Fatalf("TOCTOU bug: product ended APPROVED after concurrent AdminReviewProduct(REJECT)")
				}
			})

			// (b) Two concurrent UpdateProduct calls: Req1 adds REVIEW keyword to SKU attributes,
			// Req2 edits description WITHOUT sending skus -> MUST NEVER end APPROVED with unapproved REVIEW in SKU!
			t.Run("ConcurrentUpdateSKURuleVsNoSKUs_NeverEndsApprovedWithUnapprovedReview", func(t *testing.T) {
				baseSrv := New(pgStore, nil)
				reviewTerm := runPrefix + " toctou cam kết"
				if _, err := baseSrv.CreateBannedKeyword(adminCtx, &productv1.CreateBannedKeywordRequest{
					Term:   reviewTerm,
					Action: productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_REVIEW,
				}); err != nil {
					t.Fatalf("CreateBannedKeyword: %v", err)
				}

				prod, err := baseSrv.CreateProduct(sellerCtx, &productv1.CreateProductRequest{
					CategoryId: catID,
					Name:       "PG TOCTOU Review " + runPrefix,
					Slug:       runPrefix + "-toctou-rev",
					Skus:       []*productv1.SKUInput{{SkuCode: runPrefix + "-TV1", Name: "M", PriceVnd: 100000, Stock: 10}},
				})
				if err != nil {
					t.Fatalf("CreateProduct: %v", err)
				}
				prodID := prod.GetProduct().GetId()
				skuID := prod.GetProduct().GetSkus()[0].GetId()
				if _, err := baseSrv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
					ProductId: prodID, Decision: productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
				}); err != nil {
					t.Fatalf("Approve: %v", err)
				}

				readReached := make(chan struct{})
				releaseReq2 := make(chan struct{})
				hookStore := &pgTOCTOUStore{
					Store:            pgStore,
					targetID:         prodID,
					afterPreReadSKUs: func() { close(readReached); <-releaseReq2 },
					afterRowLock:     func() { close(readReached); <-releaseReq2 },
				}
				req2Srv := New(hookStore, nil)

				var req2WG sync.WaitGroup
				req2WG.Add(1)
				go func() {
					defer req2WG.Done()
					_, _ = req2Srv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
						Id:          prodID,
						Description: "Mô tả sạch cập nhật song song",
					})
				}()

				<-readReached
				req1Done := make(chan error, 1)
				go func() {
					_, uErr := baseSrv.UpdateProduct(sellerCtx, &productv1.UpdateProductRequest{
						Id: prodID,
						Skus: []*productv1.SKUInput{{
							Id:         skuID,
							SkuCode:    runPrefix + "-TV1",
							Name:       "M",
							Attributes: map[string]string{"note": reviewTerm},
							PriceVnd:   100000,
							Stock:      10,
						}},
					})
					req1Done <- uErr
				}()
				select {
				case uErr := <-req1Done:
					if uErr != nil {
						t.Fatalf("Req1 UpdateProduct(SKU REVIEW): %v", uErr)
					}
					close(releaseReq2)
					req2WG.Wait()
				case <-time.After(120 * time.Millisecond):
					close(releaseReq2)
					req2WG.Wait()
					if uErr := <-req1Done; uErr != nil {
						t.Fatalf("Req1 UpdateProduct(SKU REVIEW) after Req2 lock: %v", uErr)
					}
				}

				var finalStatus string
				var flaggedJSON []byte
				var skuAttrsJSON []byte
				if err := pool.QueryRow(ctx, `SELECT status, flagged_keywords FROM products WHERE id = $1`, prodID).Scan(&finalStatus, &flaggedJSON); err != nil {
					t.Fatalf("query final product: %v", err)
				}
				if err := pool.QueryRow(ctx, `SELECT attributes FROM skus WHERE id = $1`, skuID).Scan(&skuAttrsJSON); err != nil {
					t.Fatalf("query final sku: %v", err)
				}
				if !strings.Contains(string(skuAttrsJSON), reviewTerm) {
					t.Fatalf("expected SKU to contain review term %q, got %s", reviewTerm, string(skuAttrsJSON))
				}
				if finalStatus == store.ProductStatusApproved {
					t.Fatalf("TOCTOU bug: product ended APPROVED with unapproved REVIEW keyword in SKU! flagged=%s attrs=%s",
						string(flaggedJSON), string(skuAttrsJSON))
				}
			})
		})

		t.Run("R11_AdminReviewResponseSKUIsActive_RealPG", func(t *testing.T) {
			pgSrv := New(pgStore, nil)
			cat, err := pgSrv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
				Name: "PG R11 Cat " + runPrefix, Slug: runPrefix + "-r11-cat", CommissionRateBps: 500,
			})
			if err != nil {
				t.Fatalf("CreateCategory: %v", err)
			}
			assertAdminReviewSKUActiveLifecycle(t, pgSrv, "pg-shop-1", cat.GetCategory().GetId(), "PG-"+runPrefix)
		})
	})
}

type pgTOCTOUStore struct {
	store.Store
	targetID         string
	afterPreRead     func()
	afterPreReadSKUs func()
	afterRowLock     func()
	fired            atomic.Bool
}

type pgTOCTOUTx struct {
	store.Queries
	parent *pgTOCTOUStore
}

func (s *pgTOCTOUStore) GetProductByID(ctx context.Context, id string) (store.Product, error) {
	p, err := s.Store.GetProductByID(ctx, id)
	if err == nil && id == s.targetID && s.afterPreRead != nil && s.fired.CompareAndSwap(false, true) {
		s.afterPreRead()
	}
	return p, err
}

func (s *pgTOCTOUStore) ListSKUsByProductIDs(ctx context.Context, productIDs []string) ([]store.SKU, error) {
	skus, err := s.Store.ListSKUsByProductIDs(ctx, productIDs)
	if err == nil && s.afterPreReadSKUs != nil && slices.Contains(productIDs, s.targetID) && s.fired.CompareAndSwap(false, true) {
		s.afterPreReadSKUs()
	}
	return skus, err
}

func (s *pgTOCTOUStore) WithTx(ctx context.Context, fn func(q store.Queries) error) error {
	return s.Store.WithTx(ctx, func(q store.Queries) error {
		return fn(&pgTOCTOUTx{Queries: q, parent: s})
	})
}

func (q *pgTOCTOUTx) GetProductByIDForUpdate(ctx context.Context, id string) (store.Product, error) {
	p, err := q.Queries.GetProductByIDForUpdate(ctx, id)
	if err == nil && id == q.parent.targetID && q.parent.afterRowLock != nil && q.parent.fired.CompareAndSwap(false, true) {
		q.parent.afterRowLock()
	}
	return p, err
}

type pgErrPublisher struct {
	err      error
	attempts *atomic.Int32
}

func (p *pgErrPublisher) PublishMsg(_ *nats.Msg, _ ...nats.PubOpt) (*nats.PubAck, error) {
	if p.attempts != nil {
		p.attempts.Add(1)
	}
	return nil, p.err
}
