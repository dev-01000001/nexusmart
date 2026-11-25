package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/product/internal/events"
	"github.com/nexusmart/core/services/product/internal/store"
	"github.com/nexusmart/core/services/product/migrations"
)

// TestPGStore_W3B3_A8_And_B1_RealPG exercises real PostgreSQL 16 migrations (000010_),
// ListStockHistory, 2-step CSV import (concurrent confirm + lease expiry recovery),
// ListProductsForIndex, and concurrent UpdateProduct+DeleteProduct+UpdateStock deadlock/ordering safety.
func TestPGStore_W3B3_A8_And_B1_RealPG(t *testing.T) {
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
		t.Fatalf("migrations.Apply: %v", err)
	}
	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatalf("migrations.Apply idempotent: %v", err)
	}

	pgStore := store.NewPgStore(pool)
	runPrefix := "pg3b3-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	adminCtx := ctxWithRole("pg-admin-3b3", auth.RoleAdmin, "")
	modCtx := ctxWithRole("pg-mod-3b3", auth.RoleModerator, "")
	buyerCtx := ctxWithRoleAndToken("pg-buyer-3b3", auth.RoleBuyer, "", "Bearer tok-pg-3b3")
	sellerACtx := ctxWithRole("pg-seller-a-3b3", auth.RoleSeller, runPrefix+"-shop-a")
	sellerBCtx := ctxWithRole("pg-seller-b-3b3", auth.RoleSeller, runPrefix+"-shop-b")
	svcCtx := auth.WithClaims(ctx, &auth.Claims{Role: auth.ServiceRole, Service: "search-service"})

	srv := New(pgStore, nil)

	// Shared 2-level category hierarchy + prohibited category.
	rootCat, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name: "PG3B3 Root " + runPrefix, Slug: runPrefix + "-root", CommissionRateBps: 500,
	})
	if err != nil {
		t.Fatalf("CreateCategory root: %v", err)
	}
	leafCat, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name: "PG3B3 Leaf " + runPrefix, Slug: runPrefix + "-leaf", ParentId: rootCat.GetCategory().GetId(), CommissionRateBps: 600,
	})
	if err != nil {
		t.Fatalf("CreateCategory leaf: %v", err)
	}
	leafCatID := leafCat.GetCategory().GetId()

	prohibCat, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
		Name: "PG3B3 Prohib " + runPrefix, Slug: runPrefix + "-prohib", CommissionRateBps: 500, IsProhibited: true,
	})
	if err != nil {
		t.Fatalf("CreateCategory prohib: %v", err)
	}

	// 1. Real PG ListStockHistory: filtering, ordering, pagination, cross-shop 403, staff products.read.
	t.Run("A8_ListStockHistory_RealPG", func(t *testing.T) {
		pResp, err := srv.CreateProduct(sellerACtx, &productv1.CreateProductRequest{
			CategoryId: leafCatID, Name: "PG Stock Prod " + runPrefix,
			Skus: []*productv1.SKUInput{{SkuCode: runPrefix + "-ST-1", Name: "M", PriceVnd: 150000, Stock: 20}},
		})
		if err != nil {
			t.Fatalf("CreateProduct: %v", err)
		}
		prodID := pResp.GetProduct().GetId()
		sku := pResp.GetProduct().GetSkus()[0]
		if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
			ProductId: prodID, Decision: productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
		}); err != nil {
			t.Fatalf("APPROVE: %v", err)
		}

		if _, err := srv.UpdateStock(sellerACtx, &productv1.UpdateStockRequest{
			SkuId: sku.GetId(), NewStock: 25, ExpectedVersion: sku.GetVersion(),
		}); err != nil {
			t.Fatalf("UpdateStock: %v", err)
		}
		if _, err := srv.ReserveStock(buyerCtx, &productv1.ReserveStockRequest{
			OrderId: runPrefix + "-ord-1", Items: []*productv1.StockReservationItem{{SkuId: sku.GetId(), Quantity: 4}},
		}); err != nil {
			t.Fatalf("ReserveStock: %v", err)
		}
		if _, err := srv.ReleaseStock(adminCtx, &productv1.ReleaseStockRequest{OrderId: runPrefix + "-ord-1"}); err != nil {
			t.Fatalf("ReleaseStock: %v", err)
		}

		got, err := srv.ListStockHistory(sellerACtx, &productv1.ListStockHistoryRequest{
			SkuId: sku.GetId(), Page: 1, PageSize: 10,
		})
		if err != nil || got.GetTotal() != 3 || len(got.GetItems()) != 3 {
			t.Fatalf("ListStockHistory PG: total=%d len=%d err=%v", got.GetTotal(), len(got.GetItems()), err)
		}
		if got.GetItems()[0].GetChangeType() != "RELEASE" || got.GetItems()[1].GetChangeType() != "RESERVE" || got.GetItems()[2].GetChangeType() != "MANUAL_UPDATE" {
			t.Fatalf("unexpected PG history order: %+v", got.GetItems())
		}

		// Seller B forbidden.
		if _, err := srv.ListStockHistory(sellerBCtx, &productv1.ListStockHistoryRequest{SkuId: sku.GetId()}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("seller B ListStockHistory sku: want PermissionDenied, got %v", err)
		}
		if _, err := srv.ListStockHistory(sellerBCtx, &productv1.ListStockHistoryRequest{ProductId: prodID}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("seller B ListStockHistory product: want PermissionDenied, got %v", err)
		}
		// Staff with products.read allowed.
		modGot, err := srv.ListStockHistory(modCtx, &productv1.ListStockHistoryRequest{ShopId: runPrefix + "-shop-a", SkuId: sku.GetId()})
		if err != nil || modGot.GetTotal() != 3 {
			t.Fatalf("moderator ListStockHistory PG: total=%d err=%v", modGot.GetTotal(), err)
		}
	})

	// 2. Real PG CSV Import: concurrent Confirm (50 rows -> exact 50 products) + expired-lease worker takeover.
	t.Run("A8_CSVImport_ConcurrentConfirmAndLeaseExpiry_RealPG", func(t *testing.T) {
		impShop1 := runPrefix + "-imp-s1"
		impSeller1 := ctxWithRole("pg-imp-u1", auth.RoleSeller, impShop1)
		impShop2 := runPrefix + "-imp-s2"
		impSeller2 := ctxWithRole("pg-imp-u2", auth.RoleSeller, impShop2)

		var b1 strings.Builder
		b1.WriteString("\xef\xbb\xbfproduct_name,sku_code,category_id,price_vnd,stock,weight_grams,color,size\n")
		for i := 1; i <= 50; i++ {
			b1.WriteString(fmt.Sprintf("PG SP %s %d,%s-A-%03d,%s,%d,10,250,Đen,L\n", runPrefix, i, runPrefix, i, leafCatID, 100000+i*1000))
		}
		// Add 2 invalid rows: prohibited category + negative price.
		b1.WriteString(fmt.Sprintf("PG Prohib Row,%s-BAD-1,%s,100000,5,100,,\n", runPrefix, prohibCat.GetCategory().GetId()))
		b1.WriteString(fmt.Sprintf("=CMD|' /C calc'!A0,%s-BAD-2,%s,-500,5,100,,\n", runPrefix, leafCatID))

		c1, err := srv.CreateProductImportJob(impSeller1, &productv1.CreateProductImportJobRequest{
			Filename: "pg50.csv", CsvContent: []byte(b1.String()),
		})
		if err != nil {
			t.Fatalf("CreateProductImportJob pg50: %v", err)
		}
		job1ID := c1.GetJob().GetId()

		// Cross-shop seller B forbidden.
		if _, err := srv.GetProductImportJob(impSeller2, &productv1.GetProductImportJobRequest{JobId: job1ID}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("cross-shop GetProductImportJob: want PermissionDenied, got %v", err)
		}
		if _, err := srv.ConfirmProductImportJob(impSeller2, &productv1.ConfirmProductImportJobRequest{JobId: job1ID}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("cross-shop ConfirmProductImportJob: want PermissionDenied, got %v", err)
		}

		// Drain any prior jobs and validate job1 -> READY.
		for {
			worked, wErr := srv.ProcessNextImportJob(ctx)
			if wErr != nil {
				t.Fatalf("ProcessNextImportJob validate: %v", wErr)
			}
			j, gErr := srv.GetProductImportJob(impSeller1, &productv1.GetProductImportJobRequest{JobId: job1ID})
			if gErr == nil && j.GetJob().GetStatus() == productv1.ProductImportJobStatus_PRODUCT_IMPORT_JOB_STATUS_READY {
				break
			}
			if !worked {
				t.Fatalf("job1 did not reach READY")
			}
		}

		// Verify raw_csv cleared to NULL in Postgres.
		var rawCSVNull bool
		if err := pool.QueryRow(ctx, `SELECT raw_csv IS NULL FROM product_import_jobs WHERE id = $1`, job1ID).Scan(&rawCSVNull); err != nil || !rawCSVNull {
			t.Fatalf("expected raw_csv IS NULL after validation, got null=%v err=%v", rawCSVNull, err)
		}

		// Race: 4 concurrent ConfirmProductImportJob calls on the same 50-row READY job.
		start := make(chan struct{})
		var wg sync.WaitGroup
		var firstCnt, alreadyCnt atomic.Int32
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				r, cErr := srv.ConfirmProductImportJob(impSeller1, &productv1.ConfirmProductImportJobRequest{JobId: job1ID})
				if cErr != nil {
					t.Errorf("concurrent confirm err: %v", cErr)
					return
				}
				if r.GetAlreadyConfirmed() {
					alreadyCnt.Add(1)
				} else {
					firstCnt.Add(1)
				}
			}()
		}
		close(start)
		wg.Wait()
		if firstCnt.Load() != 1 || alreadyCnt.Load() != 3 {
			t.Fatalf("PG concurrent confirm: first=%d already=%d, want 1 and 3", firstCnt.Load(), alreadyCnt.Load())
		}

		// Run 2 concurrent workers claiming jobs via FOR UPDATE SKIP LOCKED -> only 1 claims job1.
		var wWG sync.WaitGroup
		wStart := make(chan struct{})
		for i := 0; i < 2; i++ {
			wWG.Add(1)
			go func() {
				defer wWG.Done()
				<-wStart
				if _, wErr := srv.ProcessNextImportJob(ctx); wErr != nil {
					t.Errorf("worker confirm err: %v", wErr)
				}
			}()
		}
		close(wStart)
		wWG.Wait()

		j1Done, err := srv.GetProductImportJob(impSeller1, &productv1.GetProductImportJobRequest{JobId: job1ID})
		if err != nil || j1Done.GetJob().GetStatus() != productv1.ProductImportJobStatus_PRODUCT_IMPORT_JOB_STATUS_COMPLETED ||
			j1Done.GetJob().GetCreatedRows() != 50 || j1Done.GetJob().GetFailedRows() != 0 {
			t.Fatalf("PG job1 completed mismatch: %+v err=%v", j1Done, err)
		}
		var createdInDB int64
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM products WHERE shop_id = $1 AND status = 'PENDING' AND deleted_at IS NULL`, impShop1).Scan(&createdInDB); err != nil || createdInDB != 50 {
			t.Fatalf("expected exactly 50 PENDING products in DB for %s, got %d err=%v", impShop1, createdInDB, err)
		}

		// Lease expiry recovery on REAL Postgres: pause worker 1 at row 21, expire claimed_until in PG,
		// run worker 2 to finish rows 21..50, unblock worker 1 -> fenced out, exact 50 products in shop2!
		var b2 strings.Builder
		b2.WriteString("product_name,sku_code,category_id,price_vnd,stock,weight_grams\n")
		for i := 1; i <= 50; i++ {
			b2.WriteString(fmt.Sprintf("PG Lease %s %d,%s-L-%03d,%s,%d,5,200\n", runPrefix, i, runPrefix, i, leafCatID, 120000+i*500))
		}
		c2, err := srv.CreateProductImportJob(impSeller2, &productv1.CreateProductImportJobRequest{
			Filename: "pglease50.csv", CsvContent: []byte(b2.String()),
		})
		if err != nil {
			t.Fatalf("CreateProductImportJob pglease50: %v", err)
		}
		job2ID := c2.GetJob().GetId()
		if _, err := srv.ProcessNextImportJob(ctx); err != nil {
			t.Fatalf("validate job2: %v", err)
		}
		if _, err := srv.ConfirmProductImportJob(impSeller2, &productv1.ConfirmProductImportJobRequest{JobId: job2ID}); err != nil {
			t.Fatalf("confirm job2: %v", err)
		}

		w1Srv := New(pgStore, nil)
		w2Srv := New(pgStore, nil)
		paused := make(chan struct{})
		resume := make(chan struct{})
		var once sync.Once
		w1Srv.SetImportBeforeConfirmRowHook(func(jID string, rowNum int32) {
			if jID == job2ID && rowNum == 21 {
				once.Do(func() {
					close(paused)
					<-resume
				})
			}
		})

		w1ErrCh := make(chan error, 1)
		go func() {
			_, wErr := w1Srv.ProcessNextImportJob(ctx)
			w1ErrCh <- wErr
		}()

		<-paused
		// Force lease expiry directly in Postgres so worker 2 can reclaim job2.
		if _, err := pool.Exec(ctx, `UPDATE product_import_jobs SET claimed_until = NOW() - INTERVAL '1 second' WHERE id = $1`, job2ID); err != nil {
			t.Fatalf("expire lease in PG: %v", err)
		}
		worked2, err := w2Srv.ProcessNextImportJob(ctx)
		if err != nil || !worked2 {
			t.Fatalf("worker 2 PG takeover failed: worked=%v err=%v", worked2, err)
		}
		close(resume)
		if w1Err := <-w1ErrCh; w1Err != nil {
			t.Fatalf("worker 1 PG fenced exit error: %v", w1Err)
		}

		j2Done, err := srv.GetProductImportJob(impSeller2, &productv1.GetProductImportJobRequest{JobId: job2ID})
		if err != nil || j2Done.GetJob().GetStatus() != productv1.ProductImportJobStatus_PRODUCT_IMPORT_JOB_STATUS_COMPLETED ||
			j2Done.GetJob().GetCreatedRows() != 50 || j2Done.GetJob().GetFailedRows() != 0 {
			t.Fatalf("PG job2 after lease recovery: %+v err=%v", j2Done, err)
		}
		var shop2Count int64
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM products WHERE shop_id = $1 AND deleted_at IS NULL`, impShop2).Scan(&shop2Count); err != nil || shop2Count != 50 {
			t.Fatalf("expected exactly 50 products in DB for %s after lease recovery, got %d err=%v", impShop2, shop2Count, err)
		}
	})

	// 2b. A row Postgres can never save (poison) fails alone; the job still completes.
	t.Run("A8_CSVImport_PoisonRowDoesNotBlockJob_RealPG", func(t *testing.T) {
		seller := ctxWithRole("pg-poison-u", auth.RoleSeller, runPrefix+"-poison")
		var b strings.Builder
		b.WriteString("product_name,sku_code,category_id,price_vnd,stock\n")
		for i := 1; i <= 3; i++ {
			b.WriteString(fmt.Sprintf("PG Poison %s %d,%s-P-%d,%s,100000,5\n", runPrefix, i, runPrefix, i, leafCatID))
		}
		c, err := srv.CreateProductImportJob(seller, &productv1.CreateProductImportJobRequest{Filename: "poison.csv", CsvContent: []byte(b.String())})
		if err != nil {
			t.Fatalf("CreateProductImportJob: %v", err)
		}
		jobID := c.GetJob().GetId()
		if _, err := srv.ProcessNextImportJob(ctx); err != nil {
			t.Fatalf("validate: %v", err)
		}
		// Bypass row validation so the database itself rejects row 2 (jsonb cannot hold NUL).
		if _, err := pool.Exec(ctx, `UPDATE product_import_rows
			SET raw_row_json = jsonb_set(raw_row_json, '{attributes}', to_jsonb($2::text)) WHERE job_id = $1 AND row_number = 2`,
			jobID, `{"color":"a\u0000b"}`); err != nil {
			t.Fatalf("poison row: %v", err)
		}
		if _, err := srv.ConfirmProductImportJob(seller, &productv1.ConfirmProductImportJobRequest{JobId: jobID}); err != nil {
			t.Fatalf("confirm: %v", err)
		}
		if _, err := srv.ProcessNextImportJob(ctx); err != nil {
			t.Fatalf("process confirming job: %v", err)
		}
		j, err := srv.GetProductImportJob(seller, &productv1.GetProductImportJobRequest{JobId: jobID})
		if err != nil || j.GetJob().GetStatus() != productv1.ProductImportJobStatus_PRODUCT_IMPORT_JOB_STATUS_COMPLETED ||
			j.GetJob().GetCreatedRows() != 2 || j.GetJob().GetFailedRows() != 1 {
			t.Fatalf("poison job: %+v err=%v, want COMPLETED created=2 failed=1", j, err)
		}
		rows, err := srv.ListProductImportRows(seller, &productv1.ListProductImportRowsRequest{JobId: jobID, ErrorsOnly: true})
		if err != nil || len(rows.GetRows()) != 1 || rows.GetRows()[0].GetRowNumber() != 2 ||
			rows.GetRows()[0].GetErrorCode() != "INTERNAL_ERROR" || strings.Contains(rows.GetRows()[0].GetErrorMessage(), "SQLSTATE") {
			t.Fatalf("poison rows: %+v err=%v", rows, err)
		}
	})

	// 3. Real PG B1: ListProductsForIndex + concurrent UpdateProduct/UpdateStock/DeleteProduct (0 deadlocks, ordered seq & updated_at).
	t.Run("B1_ListProductsForIndex_And_ConcurrentOutboxOrder_RealPG", func(t *testing.T) {
		pResp, err := srv.CreateProduct(sellerACtx, &productv1.CreateProductRequest{
			CategoryId: leafCatID,
			Name:       "PG Indexable " + runPrefix,
			Images:     []string{"https://cdn.example/" + runPrefix + ".jpg"},
			Skus: []*productv1.SKUInput{
				{SkuCode: runPrefix + "-IX-1", Name: "Đen/M", Attributes: map[string]string{"color": "Đen", "size": "M"}, PriceVnd: 220000, Stock: 8},
			},
		})
		if err != nil {
			t.Fatalf("CreateProduct indexable: %v", err)
		}
		idxProdID := pResp.GetProduct().GetId()
		idxSKUID := pResp.GetProduct().GetSkus()[0].GetId()
		if _, err := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
			ProductId: idxProdID, Decision: productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
		}); err != nil {
			t.Fatalf("APPROVE indexable: %v", err)
		}
		srv.SetOrderClient(&fakeOrderClient{
			fn: func(_ context.Context, _ *orderv1.GetOrderRequest) (*orderv1.GetOrderResponse, error) {
				return &orderv1.GetOrderResponse{Order: &orderv1.Order{
					Id: runPrefix + "-ord-rev", BuyerId: "pg-buyer-3b3", ShopId: runPrefix + "-shop-a",
					Status: orderv1.OrderStatus_ORDER_STATUS_COMPLETED,
					Items:  []*orderv1.OrderItem{{Id: runPrefix + "-oi-rev", OrderId: runPrefix + "-ord-rev", ProductId: idxProdID, SkuId: idxSKUID, SkuName: "Đen/M"}},
				}}, nil
			},
		})
		if _, err := srv.CreateReview(buyerCtx, &productv1.CreateReviewRequest{
			OrderId: runPrefix + "-ord-rev", OrderItemId: runPrefix + "-oi-rev", Rating: 5, Comment: "Đẹp",
		}); err != nil {
			t.Fatalf("CreateReview indexable: %v", err)
		}

		// Buyer/seller rejected; service token reads full IndexableProduct via keyset cursor.
		if _, err := srv.ListProductsForIndex(sellerACtx, &productv1.ListProductsForIndexRequest{}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("seller ListProductsForIndex: want PermissionDenied, got %v", err)
		}
		var found *productv1.IndexableProduct
		cursor := ""
		for {
			resp, lErr := srv.ListProductsForIndex(svcCtx, &productv1.ListProductsForIndexRequest{AfterId: cursor, PageSize: 50})
			if lErr != nil {
				t.Fatalf("ListProductsForIndex PG: %v", lErr)
			}
			for _, it := range resp.GetItems() {
				if it.GetId() == idxProdID {
					found = it
				}
			}
			if !resp.GetHasMore() {
				break
			}
			cursor = resp.GetNextCursor()
		}
		if found == nil || found.GetRatingAvgX100() != 500 || found.GetReviewCount() != 1 ||
			len(found.GetCategoryAncestorIds()) != 2 || found.GetDefaultSkuId() != idxSKUID ||
			len(found.GetColors()) != 1 || len(found.GetSizes()) != 1 {
			t.Fatalf("unexpected PG IndexableProduct: %+v", found)
		}

		// Concurrent UpdateProduct + UpdateStock + DeleteProduct across 5 rounds on REAL Postgres:
		// 0 deadlocks (SQLSTATE 40P01), strict seq & updated_at order, and no product.upserted after product.deleted.
		for round := 0; round < 5; round++ {
			pr, cErr := srv.CreateProduct(sellerACtx, &productv1.CreateProductRequest{
				CategoryId: leafCatID,
				Name:       fmt.Sprintf("PG Race %s %d", runPrefix, round),
				Skus:       []*productv1.SKUInput{{SkuCode: fmt.Sprintf("%s-R-%d", runPrefix, round), PriceVnd: 150000, Stock: 5}},
			})
			if cErr != nil {
				t.Fatalf("CreateProduct race %d: %v", round, cErr)
			}
			pid := pr.GetProduct().GetId()
			sid := pr.GetProduct().GetSkus()[0].GetId()
			if _, aErr := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
				ProductId: pid, Decision: productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
			}); aErr != nil {
				t.Fatalf("APPROVE race %d: %v", round, aErr)
			}

			start := make(chan struct{})
			var wg sync.WaitGroup
			var errsMu sync.Mutex
			var unexpectedErrs []error
			recordErr := func(op string, opErr error) {
				if opErr == nil {
					return
				}
				code := status.Code(opErr)
				if code == codes.NotFound || code == codes.Aborted || code == codes.FailedPrecondition {
					return
				}
				errsMu.Lock()
				unexpectedErrs = append(unexpectedErrs, fmt.Errorf("%s: %w", op, opErr))
				errsMu.Unlock()
			}

			wg.Add(3)
			go func() {
				defer wg.Done()
				<-start
				_, e := srv.UpdateProduct(sellerACtx, &productv1.UpdateProductRequest{
					Id: pid, Name: fmt.Sprintf("PG Race %s %d Updated", runPrefix, round),
				})
				recordErr("UpdateProduct", e)
			}()
			go func() {
				defer wg.Done()
				<-start
				_, e := srv.UpdateStock(sellerACtx, &productv1.UpdateStockRequest{
					SkuId: sid, NewStock: 0, ExpectedVersion: 1,
				})
				recordErr("UpdateStock", e)
			}()
			go func() {
				defer wg.Done()
				<-start
				_, e := srv.DeleteProduct(sellerACtx, &productv1.DeleteProductRequest{Id: pid})
				recordErr("DeleteProduct", e)
			}()
			close(start)
			wg.Wait()

			if len(unexpectedErrs) > 0 {
				t.Fatalf("round %d concurrent PG mutations hit unexpected errors (possible deadlock): %v", round, unexpectedErrs)
			}

			rows, qErr := pool.Query(ctx, `
				SELECT seq, event_type, payload_json
				FROM outbox_events
				WHERE aggregate_id = $1
				ORDER BY seq ASC
			`, pid)
			if qErr != nil {
				t.Fatalf("query outbox_events round %d: %v", round, qErr)
			}
			var (
				prevSeq       int64
				prevUpdatedAt time.Time
				sawDelete     bool
			)
			for rows.Next() {
				var (
					seq     int64
					evType  string
					payload []byte
				)
				if sErr := rows.Scan(&seq, &evType, &payload); sErr != nil {
					rows.Close()
					t.Fatalf("scan outbox row: %v", sErr)
				}
				if seq <= prevSeq {
					rows.Close()
					t.Fatalf("round %d: non-increasing seq %d <= %d", round, seq, prevSeq)
				}
				prevSeq = seq
				if strings.Contains(string(payload), "commission_rate_bps") || strings.Contains(string(payload), "flagged_keywords") {
					rows.Close()
					t.Fatalf("round %d: forbidden field leaked in PG outbox %s: %s", round, evType, string(payload))
				}
				switch evType {
				case events.SubjectProductUpserted:
					if sawDelete {
						rows.Close()
						t.Fatalf("round %d: PG product.upserted (seq=%d) appeared AFTER product.deleted!", round, seq)
					}
					var p events.ProductUpsertedPayload
					if uErr := json.Unmarshal(payload, &p); uErr != nil {
						rows.Close()
						t.Fatalf("unmarshal upserted: %v", uErr)
					}
					ts, pErr := time.Parse(time.RFC3339Nano, p.UpdatedAt)
					if pErr != nil {
						rows.Close()
						t.Fatalf("parse updated_at: %v", pErr)
					}
					if !prevUpdatedAt.IsZero() && ts.Before(prevUpdatedAt) {
						rows.Close()
						t.Fatalf("round %d: PG updated_at regressed %v < %v", round, ts, prevUpdatedAt)
					}
					prevUpdatedAt = ts
				case events.SubjectProductDeleted:
					sawDelete = true
					var p events.ProductDeletedPayload
					if uErr := json.Unmarshal(payload, &p); uErr != nil {
						rows.Close()
						t.Fatalf("unmarshal deleted: %v", uErr)
					}
					ts, pErr := time.Parse(time.RFC3339Nano, p.UpdatedAt)
					if pErr != nil {
						rows.Close()
						t.Fatalf("parse deleted updated_at: %v", pErr)
					}
					if !prevUpdatedAt.IsZero() && ts.Before(prevUpdatedAt) {
						rows.Close()
						t.Fatalf("round %d: PG deleted updated_at regressed %v < %v", round, ts, prevUpdatedAt)
					}
					prevUpdatedAt = ts
				}
			}
			rows.Close()
			if !sawDelete {
				t.Fatalf("round %d: missing product.deleted event on PG", round)
			}
		}
	})

	// 4. Real PG P1 (seller sku_code keyword search + LIKE escape, no public/admin leak),
	//    P2 (low_stock=true boundary 5/6, inactive SKU, non-selling products, reserved stock, pagination/total, role scoping),
	//    P5 (ConfirmProductImportJob rejects 2nd READY job with IMPORT_JOB_IN_PROGRESS),
	//    P6 (slug > 200 rejected with InvalidArgument instead of SQLSTATE 54000; long name auto-truncated <= 200).
	t.Run("P1_P2_P5_P6_RealPG", func(t *testing.T) {
		p12ShopA := runPrefix + "-p12-a"
		p12ShopB := runPrefix + "-p12-b"
		p12SellerA := ctxWithRole("pg-p12-seller-a", auth.RoleSeller, p12ShopA)
		p12SellerB := ctxWithRole("pg-p12-seller-b", auth.RoleSeller, p12ShopB)

		catOther, err := srv.CreateCategory(adminCtx, &productv1.CreateCategoryRequest{
			Name: "PG3B3 P12 Other " + runPrefix, Slug: runPrefix + "-p12-other", CommissionRateBps: 500,
		})
		if err != nil {
			t.Fatalf("CreateCategory p12 other: %v", err)
		}
		otherCatID := catOther.GetCategory().GetId()

		mkProd := func(sCtx context.Context, catID, name string, skus []*productv1.SKUInput, approve bool) *productv1.Product {
			t.Helper()
			pr, cErr := srv.CreateProduct(sCtx, &productv1.CreateProductRequest{
				CategoryId: catID, Name: name, Skus: skus,
			})
			if cErr != nil {
				t.Fatalf("CreateProduct %s: %v", name, cErr)
			}
			if approve {
				rev, aErr := srv.AdminReviewProduct(adminCtx, &productv1.AdminReviewProductRequest{
					ProductId: pr.GetProduct().GetId(),
					Decision:  productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE,
				})
				if aErr != nil {
					t.Fatalf("APPROVE %s: %v", name, aErr)
				}
				return rev.GetProduct()
			}
			return pr.GetProduct()
		}

		// Boundary 5 (included) and 6 (excluded)
		p5 := mkProd(p12SellerA, leafCatID, "PG P12 Bien Nam "+runPrefix, []*productv1.SKUInput{
			{SkuCode: runPrefix + "-LOW-5", Name: "M", PriceVnd: 100000, Stock: 5},
		}, true)
		p6 := mkProd(p12SellerA, leafCatID, "PG P12 Bien Sau "+runPrefix, []*productv1.SKUInput{
			{SkuCode: runPrefix + "-OK-6", Name: "M", PriceVnd: 100000, Stock: 6},
		}, true)
		// Reserved: 10 - 5 = 5 (included) vs 10 - 4 = 6 (excluded)
		pRes5 := mkProd(p12SellerA, leafCatID, "PG P12 Res5 "+runPrefix, []*productv1.SKUInput{
			{SkuCode: runPrefix + "-RES-5", Name: "L", PriceVnd: 110000, Stock: 10},
		}, true)
		if _, err := srv.ReserveStock(buyerCtx, &productv1.ReserveStockRequest{
			OrderId: runPrefix + "-p12-ord-res5",
			Items:   []*productv1.StockReservationItem{{SkuId: pRes5.GetSkus()[0].GetId(), Quantity: 5}},
		}); err != nil {
			t.Fatalf("ReserveStock 5: %v", err)
		}
		pRes6 := mkProd(p12SellerA, leafCatID, "PG P12 Res6 "+runPrefix, []*productv1.SKUInput{
			{SkuCode: runPrefix + "-RES-6", Name: "L", PriceVnd: 110000, Stock: 10},
		}, true)
		if _, err := srv.ReserveStock(buyerCtx, &productv1.ReserveStockRequest{
			OrderId: runPrefix + "-p12-ord-res4",
			Items:   []*productv1.StockReservationItem{{SkuId: pRes6.GetSkus()[0].GetId(), Quantity: 4}},
		}); err != nil {
			t.Fatalf("ReserveStock 4: %v", err)
		}
		// OUT_OF_STOCK in otherCatID (included)
		pOOS := mkProd(p12SellerA, otherCatID, "PG P12 OOS "+runPrefix, []*productv1.SKUInput{
			{SkuCode: runPrefix + "-OOS-0", Name: "XL", PriceVnd: 130000, Stock: 0},
		}, true)
		// Wildcard % and _ in sku_code (stock 20, not low_stock)
		pWild := mkProd(p12SellerA, otherCatID, "PG P12 Wild "+runPrefix, []*productv1.SKUInput{
			{SkuCode: runPrefix + "_50%OFF", Name: "S", PriceVnd: 150000, Stock: 20},
		}, true)
		// Active SKU stock 20 + deactivated INACTIVE SKU stock 0 (excluded from low_stock & sku_code search)
		pInact := mkProd(p12SellerA, leafCatID, "PG P12 Inact "+runPrefix, []*productv1.SKUInput{
			{SkuCode: runPrefix + "-LIVE-20", Name: "Live", PriceVnd: 120000, Stock: 20},
			{SkuCode: runPrefix + "-DEAD-0", Name: "Dead", PriceVnd: 120000, Stock: 0},
		}, true)
		var liveSKUID string
		for _, sk := range pInact.GetSkus() {
			if sk.GetSkuCode() == runPrefix+"-LIVE-20" {
				liveSKUID = sk.GetId()
			}
		}
		if liveSKUID == "" {
			t.Fatalf("missing LIVE-20 sku on PG")
		}
		if _, err := srv.UpdateProduct(p12SellerA, &productv1.UpdateProductRequest{
			Id: pInact.GetId(),
			Skus: []*productv1.SKUInput{
				{Id: liveSKUID, SkuCode: runPrefix + "-LIVE-20", Name: "Live", PriceVnd: 120000, Stock: 20},
			},
		}); err != nil {
			t.Fatalf("deactivate DEAD-0 on PG: %v", err)
		}
		// Non-selling products (PENDING and HIDDEN) with low stock (excluded from low_stock)
		_ = mkProd(p12SellerA, leafCatID, "PG P12 Pending "+runPrefix, []*productv1.SKUInput{
			{SkuCode: runPrefix + "-PEND-2", Name: "S", PriceVnd: 100000, Stock: 2},
		}, false)
		pHid := mkProd(p12SellerA, leafCatID, "PG P12 Hidden "+runPrefix, []*productv1.SKUInput{
			{SkuCode: runPrefix + "-HID-2", Name: "S", PriceVnd: 100000, Stock: 2},
		}, true)
		if _, err := srv.UpdateProduct(p12SellerA, &productv1.UpdateProductRequest{
			Id: pHid.GetId(), Status: productv1.ProductStatus_PRODUCT_STATUS_HIDDEN,
		}); err != nil {
			t.Fatalf("hide PG product: %v", err)
		}
		// Another shop's SKU
		_ = mkProd(p12SellerB, leafCatID, "PG P12 ShopB "+runPrefix, []*productv1.SKUInput{
			{SkuCode: runPrefix + "-SHOPB-LOW", Name: "M", PriceVnd: 100000, Stock: 1},
		}, true)

		// P1 PG checks: case-insensitive own SKU match, no public/admin leak, escaped % and _.
		gotSKU, err := srv.ListProducts(p12SellerA, &productv1.ListProductsRequest{
			ShopId: p12ShopA, Keyword: strings.ToUpper(runPrefix + "-low-5"), Page: 1, PageSize: 20,
		})
		if err != nil || gotSKU.GetTotal() != 1 || gotSKU.GetProducts()[0].GetId() != p5.GetId() {
			t.Fatalf("PG P1 seller sku_code match: total=%d err=%v", gotSKU.GetTotal(), err)
		}
		pubSKU, err := srv.ListProducts(ctx, &productv1.ListProductsRequest{
			Keyword: runPrefix + "-low-5", Page: 1, PageSize: 20,
		})
		if err != nil || pubSKU.GetTotal() != 0 {
			t.Fatalf("PG P1 public must not leak sku_code: total=%d err=%v", pubSKU.GetTotal(), err)
		}
		admSKU, err := srv.ListProducts(adminCtx, &productv1.ListProductsRequest{
			ShopId: p12ShopA, Keyword: runPrefix + "-low-5", Page: 1, PageSize: 20,
		})
		if err != nil || admSKU.GetTotal() != 0 {
			t.Fatalf("PG P1 admin keeps name/desc-only search: total=%d err=%v", admSKU.GetTotal(), err)
		}
		deadRes, err := srv.ListProducts(p12SellerA, &productv1.ListProductsRequest{
			ShopId: p12ShopA, Keyword: runPrefix + "-DEAD-0", Page: 1, PageSize: 20,
		})
		if err != nil || deadRes.GetTotal() != 0 {
			t.Fatalf("PG P1 inactive sku_code must not match: total=%d err=%v", deadRes.GetTotal(), err)
		}
		for _, kw := range []string{"%", "_", runPrefix + "_50%off"} {
			wRes, wErr := srv.ListProducts(p12SellerA, &productv1.ListProductsRequest{
				ShopId: p12ShopA, Keyword: kw, Page: 1, PageSize: 20,
			})
			if wErr != nil || wRes.GetTotal() != 1 || wRes.GetProducts()[0].GetId() != pWild.GetId() {
				t.Fatalf("PG P1 escaped LIKE %q: want only pWild, got total=%d err=%v", kw, wRes.GetTotal(), wErr)
			}
		}

		// P2 PG checks: boundary 5/6, reserved, OOS, inactive SKU, non-selling products, total + pagination.
		pgLow1, err := srv.ListProducts(p12SellerA, &productv1.ListProductsRequest{
			ShopId: p12ShopA, LowStock: true, Page: 1, PageSize: 2,
		})
		if err != nil || pgLow1.GetTotal() != 3 || len(pgLow1.GetProducts()) != 2 {
			t.Fatalf("PG P2 page 1: want total=3 len=2, got total=%d len=%d err=%v", pgLow1.GetTotal(), len(pgLow1.GetProducts()), err)
		}
		pgLow2, err := srv.ListProducts(p12SellerA, &productv1.ListProductsRequest{
			ShopId: p12ShopA, LowStock: true, Page: 2, PageSize: 2,
		})
		if err != nil || pgLow2.GetTotal() != 3 || len(pgLow2.GetProducts()) != 1 {
			t.Fatalf("PG P2 page 2: want total=3 len=1, got total=%d len=%d err=%v", pgLow2.GetTotal(), len(pgLow2.GetProducts()), err)
		}
		gotIDs := map[string]bool{}
		for _, p := range append(pgLow1.GetProducts(), pgLow2.GetProducts()...) {
			gotIDs[p.GetId()] = true
		}
		for _, wantID := range []string{p5.GetId(), pRes5.GetId(), pOOS.GetId()} {
			if !gotIDs[wantID] {
				t.Fatalf("PG P2 missing expected product %s; got=%v", wantID, gotIDs)
			}
		}
		if gotIDs[p6.GetId()] || gotIDs[pRes6.GetId()] || gotIDs[pInact.GetId()] || gotIDs[pHid.GetId()] {
			t.Fatalf("PG P2 included unexpected product: %v", gotIDs)
		}
		// Combined with category_id / status / keyword (sku_code)
		pgCombCat, err := srv.ListProducts(p12SellerA, &productv1.ListProductsRequest{
			ShopId: p12ShopA, CategoryId: otherCatID, LowStock: true, Page: 1, PageSize: 20,
		})
		if err != nil || pgCombCat.GetTotal() != 1 || pgCombCat.GetProducts()[0].GetId() != pOOS.GetId() {
			t.Fatalf("PG P2 low_stock+category: total=%d err=%v", pgCombCat.GetTotal(), err)
		}
		pgCombStatus, err := srv.ListProducts(p12SellerA, &productv1.ListProductsRequest{
			ShopId: p12ShopA, Status: productv1.ProductStatus_PRODUCT_STATUS_APPROVED, LowStock: true, Page: 1, PageSize: 20,
		})
		if err != nil || pgCombStatus.GetTotal() != 2 {
			t.Fatalf("PG P2 low_stock+status=APPROVED: want 2, got %d err=%v", pgCombStatus.GetTotal(), err)
		}
		pgCombKW, err := srv.ListProducts(p12SellerA, &productv1.ListProductsRequest{
			ShopId: p12ShopA, Keyword: runPrefix + "-res-5", LowStock: true, Page: 1, PageSize: 20,
		})
		if err != nil || pgCombKW.GetTotal() != 1 || pgCombKW.GetProducts()[0].GetId() != pRes5.GetId() {
			t.Fatalf("PG P2 low_stock+sku keyword: total=%d err=%v", pgCombKW.GetTotal(), err)
		}
		// Public ignores low_stock=true; admin honors it.
		pgPubLow, err := srv.ListProducts(ctx, &productv1.ListProductsRequest{
			ShopId: p12ShopA, LowStock: true, Page: 1, PageSize: 20,
		})
		if err != nil || pgPubLow.GetTotal() != 7 {
			t.Fatalf("PG P2 public must ignore low_stock: want total=7, got %d err=%v", pgPubLow.GetTotal(), err)
		}
		pgAdmLow, err := srv.ListProducts(adminCtx, &productv1.ListProductsRequest{
			ShopId: p12ShopA, LowStock: true, Page: 1, PageSize: 20,
		})
		if err != nil || pgAdmLow.GetTotal() != 3 {
			t.Fatalf("PG P2 admin honors low_stock: want total=3, got %d err=%v", pgAdmLow.GetTotal(), err)
		}

		// P5 PG check: ConfirmProductImportJob rejects 2nd READY job while 1st is CONFIRMING.
		p5Shop := runPrefix + "-p5-shop"
		p5Seller := ctxWithRole("pg-p5-seller", auth.RoleSeller, p5Shop)
		csvA := fmt.Sprintf("product_name,sku_code,category_id,price_vnd,stock\nPG P5 A,%s-P5A,%s,100000,5\n", runPrefix, leafCatID)
		csvB := fmt.Sprintf("product_name,sku_code,category_id,price_vnd,stock\nPG P5 B,%s-P5B,%s,100000,5\n", runPrefix, leafCatID)
		ja, err := srv.CreateProductImportJob(p5Seller, &productv1.CreateProductImportJobRequest{Filename: "a.csv", CsvContent: []byte(csvA)})
		if err != nil {
			t.Fatalf("PG P5 create ja: %v", err)
		}
		for {
			w, wErr := srv.ProcessNextImportJob(ctx)
			if wErr != nil {
				t.Fatalf("PG P5 validate ja: %v", wErr)
			}
			j, _ := srv.GetProductImportJob(p5Seller, &productv1.GetProductImportJobRequest{JobId: ja.GetJob().GetId()})
			if j.GetJob().GetStatus() == productv1.ProductImportJobStatus_PRODUCT_IMPORT_JOB_STATUS_READY {
				break
			}
			if !w {
				t.Fatalf("PG P5 ja did not reach READY")
			}
		}
		jb, err := srv.CreateProductImportJob(p5Seller, &productv1.CreateProductImportJobRequest{Filename: "b.csv", CsvContent: []byte(csvB)})
		if err != nil {
			t.Fatalf("PG P5 create jb: %v", err)
		}
		for {
			w, wErr := srv.ProcessNextImportJob(ctx)
			if wErr != nil {
				t.Fatalf("PG P5 validate jb: %v", wErr)
			}
			j, _ := srv.GetProductImportJob(p5Seller, &productv1.GetProductImportJobRequest{JobId: jb.GetJob().GetId()})
			if j.GetJob().GetStatus() == productv1.ProductImportJobStatus_PRODUCT_IMPORT_JOB_STATUS_READY {
				break
			}
			if !w {
				t.Fatalf("PG P5 jb did not reach READY")
			}
		}
		if _, err := srv.ConfirmProductImportJob(p5Seller, &productv1.ConfirmProductImportJobRequest{JobId: ja.GetJob().GetId()}); err != nil {
			t.Fatalf("PG P5 confirm ja: %v", err)
		}
		if _, err := srv.ConfirmProductImportJob(p5Seller, &productv1.ConfirmProductImportJobRequest{JobId: jb.GetJob().GetId()}); status.Code(err) != codes.FailedPrecondition || !strings.HasPrefix(status.Convert(err).Message(), "IMPORT_JOB_IN_PROGRESS") {
			t.Fatalf("PG P5 confirm jb while ja CONFIRMING: want IMPORT_JOB_IN_PROGRESS, got %v", err)
		}
		// Cleanly drain ja so no stale active job lingers.
		for {
			w, wErr := srv.ProcessNextImportJob(ctx)
			if wErr != nil {
				t.Fatalf("PG P5 drain ja: %v", wErr)
			}
			j, _ := srv.GetProductImportJob(p5Seller, &productv1.GetProductImportJobRequest{JobId: ja.GetJob().GetId()})
			if j.GetJob().GetStatus() == productv1.ProductImportJobStatus_PRODUCT_IMPORT_JOB_STATUS_COMPLETED {
				break
			}
			if !w {
				t.Fatalf("PG P5 ja did not complete")
			}
		}

		// P6 PG check: 3000-char slug -> InvalidArgument (NOT SQLSTATE 54000 Internal); long name auto-truncated <= 200.
		if _, err := srv.CreateProduct(p12SellerA, &productv1.CreateProductRequest{
			CategoryId: leafCatID, Name: "PG Huge Slug", Slug: strings.Repeat("a", 3000),
			Skus: []*productv1.SKUInput{{SkuCode: runPrefix + "-HUGE-SLUG", PriceVnd: 100000, Stock: 5}},
		}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("PG P6 3000-char slug: want InvalidArgument, got %v", err)
		}
		longPGName := runPrefix + " " + strings.TrimSpace(strings.Repeat("Sản phẩm siêu dài tiếng Việt ", 10))
		pLongPG, err := srv.CreateProduct(p12SellerA, &productv1.CreateProductRequest{
			CategoryId: leafCatID, Name: longPGName,
			Skus: []*productv1.SKUInput{{SkuCode: runPrefix + "-LONG-AUTO", PriceVnd: 100000, Stock: 5}},
		})
		if err != nil || len(pLongPG.GetProduct().GetSlug()) == 0 || len(pLongPG.GetProduct().GetSlug()) > 200 {
			t.Fatalf("PG P6 auto-truncated slug: len=%d slug=%q err=%v", len(pLongPG.GetProduct().GetSlug()), pLongPG.GetProduct().GetSlug(), err)
		}
	})
}
